package builtin

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// Reduce 把列表归约为单个结果的活动节点。
const Reduce = "reduce"

const (
	ReduceListField  = "List"
	ReduceAccLocal   = "Acc"
	ReduceItemLocal  = "Item"
	ReduceIndexLocal = "Index"
)

// ReduceOutput 是 reduce 节点写入 Run.Outputs 的结构；Spec.OutputsByEndpoint 由 OutputFrom 推导。
type ReduceOutput struct {
	Result any `json:"Result"`
}

// 与 ReduceOutput 的 json 标签一致，供 InputRef.Field 使用。
const ReduceResultField = "Result"

// ReduceInput 对输入 List 逐项求值 Expr。
// List 为绑定槽（bind）；Init/Expr 进属性表单（title）。
type ReduceInput struct {
	List any    `json:"List" bind:""`
	Init string `json:"Init,omitempty" title:"初值表达式"`
	Expr string `json:"Expr" title:"归约表达式"`
}

func (c ReduceInput) Validate() error {
	if strings.TrimSpace(c.Expr) == "" {
		return fmt.Errorf("Expr required")
	}
	if strings.TrimSpace(c.Init) != "" {
		if err := graph.ValidateExpr(c.Init); err != nil {
			return fmt.Errorf("Init: %w", err)
		}
	}
	return graph.ValidateExpr(c.Expr)
}

func (c ReduceInput) Expressions() []node.Expression {
	return []node.Expression{
		{Path: "Init", Text: c.Init},
		{Path: "Expr", Text: c.Expr, Locals: []string{ReduceAccLocal, ReduceItemLocal, ReduceIndexLocal}},
	}
}

// ReduceNode 实现归约节点。
type ReduceNode struct{}

func (ReduceNode) Spec() node.NodeSpec {
	return node.WithOutput[ReduceOutput](node.WithParams(node.NodeSpec{
		ID:          Reduce,
		Name:        "归约",
		Description: "把输入 List 归约为单个结果",
		Group:       "transform",
		GroupLabel:  "转换",
		Icon:        "sigma",
		Tone:        "blue",
		Endpoints:   node.DefaultEndpoints(),
	}, ReduceInput{Expr: ReduceAccLocal}))
}

func (ReduceNode) Execute(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return node.NodeOutput{}, err
	}
	cfg, err := node.Params[ReduceInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	set, err := ApplyReduce(cfg, maps.Clone(in.Vars))
	if err != nil {
		return node.NodeOutput{}, err
	}

	return node.Continue(set, ""), nil
}

// ApplyReduce 对输入 List 执行归约，返回本节点输出 {Result: Acc}。
func ApplyReduce(cfg ReduceInput, inputs map[string]any) (map[string]any, error) {
	cfg.Expr = strings.TrimSpace(cfg.Expr)
	if cfg.Expr == "" {
		return nil, fmt.Errorf("Expr required")
	}
	raw, ok := inputs[ReduceListField]
	if !ok {
		return nil, fmt.Errorf("input %q missing", ReduceListField)
	}
	items, err := asAnySlice(raw)
	if err != nil {
		return nil, fmt.Errorf("input %q: %w", ReduceListField, err)
	}
	var acc any
	if init := strings.TrimSpace(cfg.Init); init != "" {
		acc, err = graph.EvalExpr(init, inputs)
		if err != nil {
			return nil, fmt.Errorf("Init: %w", err)
		}
	}
	for i, item := range items {
		local := maps.Clone(inputs)
		if local == nil {
			local = map[string]any{}
		}
		local[ReduceAccLocal] = acc
		local[ReduceItemLocal] = item
		local[ReduceIndexLocal] = int64(i)
		acc, err = graph.EvalExpr(cfg.Expr, local)
		if err != nil {
			return nil, fmt.Errorf("item %d: %w", i, err)
		}
	}
	return node.OutputMap(ReduceOutput{Result: acc}), nil
}
