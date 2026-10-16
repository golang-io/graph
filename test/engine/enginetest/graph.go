package enginetest

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/google/jsonschema-go/jsonschema"
)

// OpType 是测试用活动节点：写入 Set、可选睡眠、前 FailTimes 次执行失败、或一直阻塞到 ctx 取消。
const OpType = "test_op"

// OpInput 是 OpType 节点的输入。
type OpInput struct {
	Set       map[string]any `json:"Set,omitempty"`
	SleepMS   int64          `json:"SleepMS,omitempty"`
	FailTimes int            `json:"FailTimes,omitempty"` // 第 1..FailTimes 次尝试返回错误（按 RunKey.Attempt 判断）
	Block     bool           `json:"Block,omitempty"`
}

// DeclareOutputs 声明 Set 的键为本节点输出（供 OutputDeclarer）。
func (c OpInput) DeclareOutputs() *jsonschema.Schema {
	return node.OutputSchema(slices.Sorted(maps.Keys(c.Set))...)
}

// FuncNode 把种类描述与执行函数组合成 node.Executor，供测试按用例临时构造节点（生产节点请定义具名类型）。
//
// FuncNode adapts a spec plus a closure into a node.Executor for tests.
type FuncNode struct {
	NodeSpec node.NodeSpec
	Exec     ExecFunc
}

// ExecFunc 是 FuncNode 的执行函数，签名与 node.Executor.Execute 相同。
type ExecFunc func(context.Context, node.NodeInput) (node.NodeOutput, error)

// Spec 实现 node.Node。
func (n FuncNode) Spec() node.NodeSpec { return n.NodeSpec }

// Execute 实现 node.Executor。
func (n FuncNode) Execute(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
	return n.Exec(ctx, in)
}

// OpSpec 返回 OpType 节点类型：输出即参数 Set 的键。
func OpSpec() node.Executor { return opNode{} }

// opNode 是 OpType 的实现：按 OpInput 睡眠、阻塞或按尝试次数失败，成功时原样输出 Set。
type opNode struct{}

// Spec 实现 node.Node。
func (opNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID: OpType, Name: "test op", Group: "test", GroupLabel: "test",
		Endpoints: node.DefaultEndpoints(),
	}, OpInput{})
}

// Execute 实现 node.Executor。
func (opNode) Execute(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
	c, err := node.Params[OpInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	if c.Block {
		<-ctx.Done()
		return node.NodeOutput{}, ctx.Err()
	}
	if c.SleepMS > 0 {
		select {
		case <-time.After(time.Duration(c.SleepMS) * time.Millisecond):
		case <-ctx.Done():
			return node.NodeOutput{}, ctx.Err()
		}
	}
	if in.Key.Attempt <= c.FailTimes {
		return node.NodeOutput{}, fmt.Errorf("op %s attempt %d failed", in.NodeID, in.Key.Attempt)
	}
	return node.NodeOutput{Set: c.Set}, nil
}

// Registry 返回核心内置节点加 extra 的注册表（不经过 service，避免引擎测试依赖 service）。
func Registry(extra ...node.Node) (*node.Registry, error) {
	return node.NewRegistry(append(builtin.Nodes(), extra...)...)
}

// N 构造节点；cfg 为 nil 时不带参数；非 nil 时编成字面量 Inputs。
func N(id, typ string, cfg any) graph.Node {
	n := graph.Node{ID: id, Kind: typ}
	if cfg != nil {
		raw, err := json.Marshal(cfg)
		if err != nil {
			panic(err)
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal(raw, &obj); err != nil {
			panic(err)
		}
		n.Inputs = make(map[string]graph.InputRef, len(obj))
		for k, v := range obj {
			n.Inputs[k] = graph.InputRef{Value: v}
		}
	}
	return n
}

// In 为节点合并输入引用；与 N 写入的字面量参数并存。
func In(n graph.Node, refs map[string]graph.InputRef) graph.Node {
	if n.Inputs == nil {
		n.Inputs = maps.Clone(refs)
		return n
	}
	n.Inputs = maps.Clone(n.Inputs)
	maps.Copy(n.Inputs, refs)
	return n
}

// R 构造对节点输出（或 graph.InputSource 运行输入）的引用。
func R(from, field string) graph.InputRef { return graph.InputRef{From: from, Field: field} }

// E 构造端点到端点的边：From 默认取来源节点的 Out 出端点，To 取目标节点的 In 入端点。
// 可选的 fromLocal 覆盖来源出端点本地名（如 switch 的 Case0、stop_error 的 Error）。
// 边 ID 仍为 from_to（同一对节点多条边时请自行保证唯一）。
//
// E("a","b")          -> From:"a.Out"   To:"b.In"
// E("a","b","Case0")  -> From:"a.Case0" To:"b.In"
func E(from, to string, fromLocal ...string) graph.Edge {
	fl := node.DefaultOutput
	if len(fromLocal) > 0 {
		fl = fromLocal[0]
	}
	return graph.Edge{
		ID:   from + "_" + to,
		From: graph.FormatEndpointID(from, fl),
		To:   graph.FormatEndpointID(to, node.DefaultInput),
	}
}

// G 构造图：nodes[0] 是入口节点，前面自动接上 start（见 PrependStart）。
func G(nodes []graph.Node, edges ...graph.Edge) graph.Graph {
	return PrependStart(graph.Graph{Nodes: nodes, Edges: edges})
}

// GI 构造声明了运行输入的图；names 中的输入都为必填，类型为任意 JSON 值（测试快捷方式）。
func GI(names []string, nodes []graph.Node, edges ...graph.Edge) graph.Graph {
	g := G(nodes, edges...)
	if len(names) == 0 {
		return g
	}
	props := make(map[string]*jsonschema.Schema, len(names))
	for _, name := range names {
		props[name] = graph.AnyValueSchema()
	}
	g.InputSchema = graph.RunInputSchema(props)
	return g
}

// PrependStart 在测试图最前面插入 start 节点（ID `_start`），并从 start 的 out 连到 g.Nodes[0] 的 in。
// 约定 g.Nodes[0] 是入口节点；调用方不应再自带 start 节点。
//
// PrependStart inserts a start node wired to g.Nodes[0], which callers declare as the entry node.
func PrependStart(g graph.Graph) graph.Graph {
	if len(g.Nodes) == 0 {
		return g
	}
	entry := g.Nodes[0].ID
	g.Nodes = append([]graph.Node{{ID: "_start", Kind: builtin.Start}}, g.Nodes...)
	g.Edges = append([]graph.Edge{{
		ID:   "_start_" + entry,
		From: graph.FormatEndpointID("_start", node.DefaultOutput),
		To:   graph.FormatEndpointID(entry, node.DefaultInput),
	}}, g.Edges...)
	return g
}
