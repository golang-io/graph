package builtin

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// LLM 模型调用节点。
const LLM = "llm"

const (
	// LLMQueryField 是必填输入槽：主查询/用户内容，供 Prompt/System 模板引用。
	LLMQueryField = "Query"
	// LLMContextField 是可选输入槽：附加上下文（如上游 HTTP 状态或正文）。
	LLMContextField = "Context"
)

// LLMOutput 是 LLM 节点写入 Run.Outputs 的结构；Spec.OutputsByEndpoint 由 OutputFrom 推导。
type LLMOutput struct {
	Text string `json:"Text"`
}

// 与 LLMOutput 的 json 标签一致，供 InputRef.Field 使用。
const LLMTextField = "Text"

// LLMInput 是 LLM 节点的输入；实际调用由业务应用注入。
// Query/Context 为绑定槽；Model/Prompt 进属性表单；其余字段仅解码。
// Prompt/System/Model 模板可引用本节点 Inputs 中的任意输入名。
type LLMInput struct {
	Query       any     `json:"Query" bind:""`
	Context     any     `json:"Context,omitempty" bind:""`
	Model       string  `json:"Model" title:"模型"`
	System      string  `json:"System,omitempty"`
	Prompt      string  `json:"Prompt" title:"提示词"`
	Temperature float64 `json:"Temperature,omitempty"`
	MaxTokens   int     `json:"MaxTokens,omitempty"`
}

func (c LLMInput) Validate() error {
	// Model/Prompt 可为绑定引用；字面量为空时由 InputSchema.required 保证键存在。
	if c.MaxTokens < 0 {
		return fmt.Errorf("MaxTokens must be >= 0")
	}
	for _, text := range []string{c.Model, c.System, c.Prompt} {
		if text == "" {
			continue
		}
		if err := graph.ValidateTemplate(text); err != nil {
			return err
		}
	}
	return nil
}

func (c LLMInput) Expressions() []node.Expression {
	return []node.Expression{
		{Path: "Model", Text: c.Model, Template: true},
		{Path: "System", Text: c.System, Template: true},
		{Path: "Prompt", Text: c.Prompt, Template: true},
	}
}

// ErrUnavailable 表示执行器未装配，例如业务应用未注入 LLM 调用函数。
var ErrUnavailable = errors.New("graph node: executor unavailable")

// LLMInvoker 由业务应用注入，负责实际的模型调用。
type LLMInvoker func(context.Context, node.NodeInput, LLMInput) (string, error)

// LLMNode 是可选的 LLM 节点；Invoke 为 nil 时 Execute 返回 ErrUnavailable。
type LLMNode struct {
	Invoke LLMInvoker
}

func (LLMNode) Spec() node.NodeSpec {
	return node.WithOutput[LLMOutput](node.WithParams(node.NodeSpec{
		ID:          LLM,
		Name:        "LLM",
		Description: "调用业务应用注入的模型网关",
		Group:       "ai",
		GroupLabel:  "智能",
		// 默认 Prompt 提示作者使用常用输入名；仍可新增自定义输入名。
		Icon:      "sparkles",
		Tone:      "purple",
		Endpoints: node.DefaultEndpoints(),
	}, LLMInput{Prompt: "{{" + LLMQueryField + "}}"}))
}

func (n LLMNode) Execute(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
	if n.Invoke == nil {
		return node.NodeOutput{}, ErrUnavailable
	}
	cfg, err := node.Params[LLMInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	vars := maps.Clone(in.Vars)
	cfg.Model = strings.TrimSpace(graph.RenderTemplate(cfg.Model, vars))
	cfg.System = graph.RenderTemplate(cfg.System, vars)
	cfg.Prompt = graph.RenderTemplate(cfg.Prompt, vars)
	if cfg.Model == "" || strings.TrimSpace(cfg.Prompt) == "" {
		return node.NodeOutput{}, fmt.Errorf("node: Model and Prompt required")
	}
	text, err := n.Invoke(ctx, in, cfg)
	if err != nil {
		return node.NodeOutput{}, err
	}
	return node.Continue(node.OutputMap(LLMOutput{Text: text}), ""), nil
}
