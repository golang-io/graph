package builtin

import (
	"context"
	"maps"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
	"github.com/google/jsonschema-go/jsonschema"
)

// Template 文案插值节点。
const Template = "template"

// TemplateInput 按序渲染 Outputs：Key 为字面量输出名，Value 为模板。
type TemplateInput struct {
	Outputs []graph.OutKV `json:"Outputs" title:"输出"`
}

func (c TemplateInput) Validate() error {
	return checkOutputTexts(c.Outputs, true, graph.ValidateTemplate)
}

func (c TemplateInput) DeclareOutputs() *jsonschema.Schema {
	s, _ := outKVSchema(c.Outputs, false)
	return s
}

func (c TemplateInput) Expressions() []node.Expression {
	return outKVExpressions(c.Outputs, true)
}

// TemplateNode 实现模板节点。
type TemplateNode struct{}

func (TemplateNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID:          Template,
		Name:        "模板",
		Description: "渲染文本并作为本节点输出",
		Group:       "transform",
		GroupLabel:  "转换",
		Icon:        "braces",
		Tone:        "blue",
		// 只渲染输入变量、不做 IO，作为内联节点在内核中同步执行，省去活动节点的调度与写回。
		Inline:    true,
		Endpoints: node.DefaultEndpoints(),
	}, TemplateInput{Outputs: []graph.OutKV{}})
}

func (TemplateNode) Execute(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return node.NodeOutput{}, err
	}
	cfg, err := node.Params[TemplateInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	set, err := ApplyTemplateOutputs(cfg.Outputs, maps.Clone(in.Vars))
	if err != nil {
		return node.NodeOutput{}, err
	}

	return node.Continue(set, ""), nil
}
