package builtin

import (
	"context"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// HTTP 外呼节点。
const HTTP = "http"

// HTTPInput 是 HTTP 节点的输入：字段即 Node.Inputs 中的输入名，由 WithParams 推导 InputSchema，
// 编译期单次解码为静态参数并经 NodeInput.Params 传入 Execute。数据流为 HTTPInput → HTTPNode.Execute → HTTPOutput。
// URL、Body、Headers 的模板只能使用本节点输入名（含 Inputs 中额外声明的自定义输入）。
// 输出见 HTTPOutput，写入 Run.Outputs[节点ID]；需要加工响应时由下游 template / code 完成。
type HTTPInput struct {
	Method     string            `json:"Method,omitempty" title:"方法" enum:"GET,POST,PUT,PATCH,DELETE"`
	URL        string            `json:"URL" title:"URL"` // 支持 {{expr}}；可为绑定引用
	Headers    map[string]string `json:"Headers,omitempty"`
	Body       string            `json:"Body,omitempty"` // 支持 {{expr}}
	TimeoutSec int               `json:"TimeoutSec,omitempty"`
}

// HTTPOutput 是 HTTP 节点写入 Run.Outputs 的结构；Spec.OutputsByEndpoint 由 OutputFrom 推导。
// json 标签即下游 InputRef.Field 可用的输出名（Body / Status / OK）。
type HTTPOutput struct {
	Body   string `json:"Body"`
	Status int64  `json:"Status"`
	OK     bool   `json:"OK"`
}

// Validate 在定义期检查 URL、Body、Headers 中的模板插值能否编译。
// URL 可为绑定引用；字面量为空时由 InputSchema.required 保证键存在。
func (c HTTPInput) Validate() error {
	if err := graph.ValidateTemplate(c.URL); err != nil {
		return err
	}
	if err := graph.ValidateTemplate(c.Body); err != nil {
		return err
	}
	for _, value := range c.Headers {
		if err := graph.ValidateTemplate(value); err != nil {
			return err
		}
	}
	return nil
}

func (c HTTPInput) Expressions() []node.Expression {
	exprs := []node.Expression{
		{Path: "URL", Text: c.URL, Template: true},
		{Path: "Body", Text: c.Body, Template: true},
	}
	for _, key := range slices.Sorted(maps.Keys(c.Headers)) {
		exprs = append(exprs, node.Expression{Path: "Headers." + key, Text: c.Headers[key], Template: true})
	}
	return exprs
}

// HTTPNode 是可选的 HTTP 外呼节点；默认注册表不含，业务应用显式注册 HTTPNode{}。
type HTTPNode struct{}

func (HTTPNode) Spec() node.NodeSpec {
	return node.WithOutput[HTTPOutput](node.WithParams(node.NodeSpec{
		ID:          HTTP,
		Name:        "HTTP 请求",
		Description: "调用外部 HTTP 服务",
		Group:       "integration",
		GroupLabel:  "集成",
		Icon:        "globe",
		Tone:        "cyan",
		Endpoints:   node.DefaultEndpoints(),
	}, HTTPInput{Method: "GET", TimeoutSec: 30}))
}

// Execute 渲染 URL / Body / Headers 模板后发出请求，把响应体、状态码与是否 2xx 写入输出。
// Method 为空时用 GET；TimeoutSec 不大于 0 时用 30 秒。请求头带上幂等键 RunID:Seq，重试与租约重领时不变，下游可据此去重。
func (HTTPNode) Execute(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
	input, err := node.Params[HTTPInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	vars := maps.Clone(in.Vars)
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = http.MethodGet
	}
	timeout := 30 * time.Second
	if input.TimeoutSec > 0 {
		timeout = time.Duration(input.TimeoutSec) * time.Second
	}
	var body io.Reader
	if method != http.MethodGet && method != http.MethodHead {
		body = strings.NewReader(graph.RenderTemplate(input.Body, vars))
	}
	address := strings.TrimSpace(graph.RenderTemplate(input.URL, vars))
	req, err := http.NewRequestWithContext(ctx, method, address, body)
	if err != nil {
		return node.NodeOutput{}, fmt.Errorf("http: invalid request: %w", err)
	}
	for key, value := range input.Headers {
		req.Header.Set(key, graph.RenderTemplate(value, vars))
	}
	if key := in.Key.IdempotencyKey(); key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	res, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return node.NodeOutput{}, fmt.Errorf("http: request failed: %w", err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err != nil {
		return node.NodeOutput{}, fmt.Errorf("http: read response: %w", err)
	}
	return node.Continue(node.OutputMap(HTTPOutput{
		Body:   string(data),
		Status: int64(res.StatusCode),
		OK:     res.StatusCode >= 200 && res.StatusCode < 300,
	}), ""), nil
}
