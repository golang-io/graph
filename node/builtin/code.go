package builtin

import (
	"context"
	"maps"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
	"github.com/google/jsonschema-go/jsonschema"
)

// Code 安全表达式节点。
const Code = "code"

// CodeInput 按序对 Outputs 求值。
type CodeInput struct {
	Outputs []graph.OutKV `json:"Outputs" title:"输出表达式"`
}

func (c CodeInput) Validate() error {
	return checkOutKVs(c.Outputs, true)
}

func (c CodeInput) HasOutput(field string) bool {
	return hasOutKVKey(c.Outputs, field)
}

func (c CodeInput) DeclareOutputs() *jsonschema.Schema {
	s, _ := outKVSchema(c.Outputs, false)
	return s
}

func (c CodeInput) Expressions() []node.Expression {
	return outKVExpressions(c.Outputs, false)
}

// CodeNode 实现表达式节点。
type CodeNode struct{}

func (CodeNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID:          Code,
		Name:        "表达式",
		Description: "安全表达式求值并作为本节点输出",
		Group:       "transform",
		GroupLabel:  "转换",
		Icon:        "code",
		Tone:        "blue",
		Endpoints:   node.DefaultEndpoints(),
	}, CodeInput{Outputs: []graph.OutKV{}})
}

func (CodeNode) Execute(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return node.NodeOutput{}, err
	}
	cfg, err := node.Params[CodeInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	set, err := ApplyCodeOutputs(cfg.Outputs, maps.Clone(in.Vars))
	if err != nil {
		return node.NodeOutput{}, err
	}
	return node.Continue(set, ""), nil
}
