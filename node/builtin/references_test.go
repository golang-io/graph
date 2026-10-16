package builtin

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
	"github.com/google/jsonschema-go/jsonschema"
)

// dfNode 构造测试节点；cfg 为 nil 时不带参数字面量；非 nil 时与 inputs 合并进 Inputs。
func dfNode(id, typ string, cfg any, inputs map[string]graph.InputRef) graph.Node {
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
		n.Inputs = make(map[string]graph.InputRef, len(obj)+len(inputs))
		for k, v := range obj {
			// 与 DefaultInputs 一致：跳过 null / ""，避免 bind 槽零值被当成已提供。
			if len(v) == 0 || string(v) == "null" || string(v) == `""` {
				continue
			}
			n.Inputs[k] = graph.InputRef{Value: v}
		}
	}
	if len(inputs) > 0 {
		if n.Inputs == nil {
			n.Inputs = make(map[string]graph.InputRef, len(inputs))
		}
		for k, v := range inputs {
			n.Inputs[k] = v
		}
	}
	return n
}

func dfTemplate(id, key, value string, inputs map[string]graph.InputRef) graph.Node {
	return dfNode(id, Template, TemplateInput{Outputs: []graph.OutKV{{Key: key, Value: value}}}, inputs)
}

func dfEdge(from, to string, fromLocal ...string) graph.Edge {
	fl := node.DefaultOutput
	if len(fromLocal) > 0 {
		fl = fromLocal[0]
	}
	return graph.Edge{
		ID:   from + "_" + to + strings.Join(fromLocal, ""),
		From: graph.FormatEndpointID(from, fl),
		To:   graph.FormatEndpointID(to, node.DefaultInput),
	}
}

type dfRefs = map[string]graph.InputRef

// testRegistry 返回装入全部核心内置节点与 extra 的注册表；装配失败直接 panic（测试夹具）。
func testRegistry(extra ...node.Node) *node.Registry {
	r, err := node.NewRegistry(append(Nodes(), extra...)...)
	if err != nil {
		panic(err)
	}
	return r
}

// dfRegistry 是包含全部内置节点（含可选 HTTP/LLM）的注册表，这里只做定义期校验。
func dfRegistry() *node.Registry {
	return testRegistry(HTTPNode{}, LLMNode{})
}

// mapBody 是 map 的循环体：运行输入 item 与 greeting，start → body → end。
// mutate 在循环体上修改出待测的错误。
func mapBody(mutate func(g *graph.Graph)) graph.Graph {
	g := graph.Graph{
		InputSchema: graph.RunInputSchema(map[string]*jsonschema.Schema{
			MapItemField: graph.AnyValueSchema(),
			"greeting":   {Type: "string"},
		}),
		Nodes: []graph.Node{
			dfNode("start", Start, nil, nil),
			dfTemplate("body", "v", "{{greeting}}:{{item}}", dfRefs{
				"greeting": {From: graph.InputSource, Field: "greeting"}, "item": {From: graph.InputSource, Field: MapItemField}}),
			dfNode("end", End, nil, dfRefs{"v": {From: "body", Field: "v"}}),
		},
		Edges: []graph.Edge{dfEdge("start", "body"), dfEdge("body", "end")},
	}
	if mutate != nil {
		mutate(&g)
	}
	return g
}

// mapGraph 是带 Map 的基础图：start → greet → lp → after → end；lp 的循环体为 mapBody(nil)，
// greet 的输出经 lp 的自定义输入 greeting 传入循环体。mutate 在基础图上修改出待测的错误。
func mapGraph(mutate func(g *graph.Graph)) graph.Graph {
	return mapGraphWith(mapBody(nil), mutate)
}

func mapGraphWith(body graph.Graph, mutate func(g *graph.Graph)) graph.Graph {
	g := graph.Graph{
		InputSchema: graph.RunInputSchema(map[string]*jsonschema.Schema{
			"name":  {Type: "string"},
			"items": {Type: "array", Items: graph.AnyValueSchema()},
		}),
		Nodes: []graph.Node{
			dfNode("start", Start, nil, nil),
			dfTemplate("greet", "greeting", "hi {{name}}", dfRefs{"name": {From: graph.InputSource, Field: "name"}}),
			dfNode("lp", Map, MapInput{Body: &body}, dfRefs{
				MapListField: {From: graph.InputSource, Field: "items"}, "greeting": {From: "greet", Field: "greeting"}}),
			dfTemplate("after", "n", "{{greeting}}", dfRefs{"greeting": {From: "greet", Field: "greeting"}}),
			dfNode("end", End, nil, dfRefs{"results": {From: "lp", Field: MapResultsField}, "n": {From: "after", Field: "n"}}),
		},
		Edges: []graph.Edge{dfEdge("start", "greet"), dfEdge("greet", "lp"), dfEdge("lp", "after"), dfEdge("after", "end")},
	}
	if mutate != nil {
		mutate(&g)
	}
	return g
}

// setInput 修改节点 id 的输入 name；ref 为零值时删除该输入。
func setInput(g *graph.Graph, id, name string, ref graph.InputRef) {
	for i := range g.Nodes {
		if g.Nodes[i].ID != id {
			continue
		}
		if g.Nodes[i].Inputs == nil {
			g.Nodes[i].Inputs = dfRefs{}
		}
		if ref.From == "" && ref.Value == nil {
			delete(g.Nodes[i].Inputs, name)
		} else {
			g.Nodes[i].Inputs[name] = ref
		}
	}
}

// TestReferencesValidation 覆盖定义期的数据绑定校验：引用来源、可达性、循环体（内嵌子图）、输入槽与表达式标识符。
func TestReferencesValidation(t *testing.T) {
	registry := dfRegistry()
	if err := registry.Validate(mapGraph(nil)); err != nil {
		t.Fatalf("valid graph rejected: %v", err)
	}
	cases := map[string]struct {
		g    graph.Graph
		want string
	}{
		"unknown source node": {mapGraph(func(g *graph.Graph) {
			setInput(g, "after", "greeting", graph.InputRef{From: "nope", Field: "greeting"})
		}), `source node "nope" not found`},
		"unknown output field": {mapGraph(func(g *graph.Graph) {
			setInput(g, "after", "greeting", graph.InputRef{From: "greet", Field: "nope"})
		}), `node "greet" has no output "nope"`},
		"undeclared run input": {mapGraph(func(g *graph.Graph) {
			setInput(g, "after", "x", graph.InputRef{From: graph.InputSource, Field: "x"})
		}), `run input "x" is not declared`},
		"downstream node not reachable": {mapGraph(func(g *graph.Graph) {
			setInput(g, "greet", "n", graph.InputRef{From: "after", Field: "n"})
		}), `not reachable from output "n" of node "after"`},
		"body node not visible outside": {mapGraph(func(g *graph.Graph) {
			setInput(g, "after", "v", graph.InputRef{From: "body", Field: "v"})
		}), `source node "body" not found`},
		"item is not a map output": {mapGraph(func(g *graph.Graph) {
			setInput(g, "after", "item", graph.InputRef{From: "lp", Field: MapItemField})
		}), `node "lp" has no output "Item"`},
		"outer node not visible inside body": {mapGraphWith(mapBody(func(b *graph.Graph) {
			setInput(b, "body", "greeting", graph.InputRef{From: "greet", Field: "greeting"})
		}), nil), `node "lp" Body: graph: node "body" input "greeting": source node "greet" not found`},
		"body requires input the map does not provide": {mapGraph(func(g *graph.Graph) {
			setInput(g, "lp", "greeting", graph.InputRef{})
		}), `Body requires input "greeting" that the node does not provide`},
		"map Body required": {mapGraph(func(g *graph.Graph) {
			setInput(g, "lp", "Body", graph.InputRef{})
		}), `Body required`},
		"cycle rejected": {mapGraph(func(g *graph.Graph) {
			g.Edges = append(g.Edges, dfEdge("after", "greet"))
		}), `cycle through node`},
		"self reference": {mapGraph(func(g *graph.Graph) {
			setInput(g, "after", "n", graph.InputRef{From: "after", Field: "n"})
		}), `cannot reference its own output`},
		"map list required": {mapGraph(func(g *graph.Graph) {
			setInput(g, "lp", MapListField, graph.InputRef{})
		}), `requires input "List"`},
		"template uses undeclared name": {mapGraph(func(g *graph.Graph) {
			setInput(g, "after", "greeting", graph.InputRef{})
		}), `unknown name greeting`},
		"non-merge multiple incoming edges rejected": {mapGraph(func(g *graph.Graph) {
			g.Edges = append(g.Edges, dfEdge("greet", "after"))
		}), `cannot have multiple incoming edges (use a merge node)`},
		"undeclared target input endpoint rejected": {mapGraph(func(g *graph.Graph) {
			g.Edges[0].To = "greet.DoesNotExist"
		}), `targets undeclared input "DoesNotExist"`},
		"orphan not reachable from start": {mapGraph(func(g *graph.Graph) {
			g.Nodes = append(g.Nodes, dfTemplate("orphan", "o", "o", nil))
			g.Edges = append(g.Edges, dfEdge("orphan", "end"))
		}), `unreachable`},
		"merge First unknown source": {graph.Graph{
			Nodes: []graph.Node{
				dfNode("start", Start, nil, nil),
				dfTemplate("a", "x", "1", nil),
				dfNode("m", Merge, MergeInput{Outputs: []MergeFirstField{{Name: "x", First: []graph.InputRef{{From: "ghost", Field: "x"}}}}}, nil),
				dfNode("end", End, nil, nil),
			},
			Edges: []graph.Edge{dfEdge("start", "a"), dfEdge("a", "m"), dfEdge("m", "end")},
		}, `source node "ghost" not found`},
		"literal and reference together": {mapGraph(func(g *graph.Graph) {
			setInput(g, "after", "lit", graph.InputRef{From: "greet", Field: "greeting", Value: json.RawMessage(`1`)})
		}), `exactly one of From or Value`},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := registry.Validate(tc.g)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

// LLM InputSchema：query 必填；额外自定义输入名允许（与其它节点同一套规则）。
func TestReferencesLLMFields(t *testing.T) {
	registry := dfRegistry()
	base := func(inputs dfRefs) graph.Graph {
		return graph.Graph{
			InputSchema: graph.RunInputSchema(graph.StringProps("q")),
			Nodes: []graph.Node{
				dfNode("start", Start, nil, nil),
				dfNode("ask", LLM, LLMInput{Model: "m", Prompt: "{{Query}}"}, inputs),
				dfNode("end", End, nil, dfRefs{"text": {From: "ask", Field: LLMTextField}}),
			},
			Edges: []graph.Edge{dfEdge("start", "ask"), dfEdge("ask", "end")},
		}
	}
	if err := registry.Validate(base(dfRefs{LLMQueryField: {From: graph.InputSource, Field: "q"}})); err != nil {
		t.Fatalf("valid llm graph rejected: %v", err)
	}
	if err := registry.Validate(base(nil)); err == nil || !strings.Contains(err.Error(), `requires input "Query"`) {
		t.Fatalf("missing query: err=%v", err)
	}
	// 额外输入名合法；Prompt 未引用时不触发表达式校验
	if err := registry.Validate(base(dfRefs{
		LLMQueryField: {From: graph.InputSource, Field: "q"},
		"extra":       {From: graph.InputSource, Field: "q"},
	})); err != nil {
		t.Fatalf("extra input should be allowed: %v", err)
	}
}

// 字面量输入不需要来源；stop_error 为终止节点，不能有后继出边。
func TestReferencesStopErrorTerminalAndLiteral(t *testing.T) {
	registry := dfRegistry()
	g := graph.Graph{
		Nodes: []graph.Node{
			dfNode("start", Start, nil, nil),
			dfTemplate("fix", "msg", "{{tag}}", dfRefs{"tag": {Value: json.RawMessage(`"lit"`)}}),
			dfNode("end", End, nil, dfRefs{"msg": {From: "fix", Field: "msg"}}),
		},
		Edges: []graph.Edge{dfEdge("start", "fix"), dfEdge("fix", "end")},
	}
	if err := registry.Validate(g); err != nil {
		t.Fatalf("compile: %v", err)
	}
	badStop := graph.Graph{
		Nodes: []graph.Node{
			dfNode("start", Start, nil, nil),
			dfNode("boom", StopError, StopErrorInput{Message: "x"}, nil),
			dfNode("end", End, nil, nil),
		},
		Edges: []graph.Edge{dfEdge("start", "boom"), dfEdge("boom", "end")},
	}
	if err := registry.Validate(badStop); err == nil || !strings.Contains(err.Error(), "terminal node cannot have successors") {
		t.Fatalf("stop_error with successor err = %v", err)
	}
}

// 入口规则：必须有且仅有一个无入边的 start；start → map 合法。
// Registry.Outputs 的契约用例在 test/node（TestNodeOutputTables）。
func TestEntryRule(t *testing.T) {
	registry := dfRegistry()
	noStart := graph.Graph{
		Nodes: []graph.Node{dfTemplate("a", "x", "1", nil), dfNode("end", End, nil, nil)},
		Edges: []graph.Edge{dfEdge("a", "end")},
	}
	if err := registry.Validate(noStart); err == nil || !strings.Contains(err.Error(), "entry node required") {
		t.Fatalf("no start err = %v", err)
	}
	twoStart := graph.Graph{
		Nodes: []graph.Node{
			dfNode("s1", Start, nil, nil), dfNode("s2", Start, nil, nil),
			dfTemplate("a", "x", "1", nil), dfNode("end", End, nil, nil),
		},
		Edges: []graph.Edge{dfEdge("s1", "a"), dfEdge("s2", "a"), dfEdge("a", "end")},
	}
	if err := registry.Validate(twoStart); err == nil || !strings.Contains(err.Error(), "multiple entry") {
		t.Fatalf("two start err = %v", err)
	}
	startWithIn := graph.Graph{
		Nodes: []graph.Node{
			dfTemplate("a", "x", "1", nil), dfNode("s", Start, nil, nil), dfNode("end", End, nil, nil),
		},
		Edges: []graph.Edge{dfEdge("a", "s"), dfEdge("s", "end")},
	}
	if err := registry.Validate(startWithIn); err == nil || !strings.Contains(err.Error(), "cannot have predecessors") {
		t.Fatalf("start with predecessors err = %v", err)
	}
	startToMap := mapGraph(func(g *graph.Graph) {
		// 去掉 greet，让 start 直接连 map。
		var nodes []graph.Node
		for _, n := range g.Nodes {
			if n.ID != "greet" {
				nodes = append(nodes, n)
			}
		}
		g.Nodes = nodes
		g.Edges = []graph.Edge{dfEdge("start", "lp"), dfEdge("lp", "after"), dfEdge("after", "end")}
		setInput(g, "lp", "greeting", graph.InputRef{From: graph.InputSource, Field: "name"})
		setInput(g, "after", "greeting", graph.InputRef{From: graph.InputSource, Field: "name"})
	})
	if err := registry.Validate(startToMap); err != nil {
		t.Fatalf("start → map rejected: %v", err)
	}
}

// probeNode 是无参数、单入单出的测试活动节点，只用于图结构校验，Execute 什么也不做。
type probeNode struct{ id string }

func (p probeNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{ID: p.id, Name: "测试活动", Endpoints: node.DefaultEndpoints()}, struct{}{})
}

func (probeNode) Execute(context.Context, node.NodeInput) (node.NodeOutput, error) {
	return node.NodeOutput{}, nil
}

// TestValidateLoopDiamondCost 确认 map 循环体内多层菱形分叉的图校验在 1 秒内完成。
func TestValidateLoopDiamondCost(t *testing.T) {
	registry := testRegistry(probeNode{id: "probe"})
	for _, levels := range []int{12, 20, 70} {
		g := loopDiamondGraph(levels)
		start := time.Now()
		err := registry.Validate(g)
		if err != nil || time.Since(start) > time.Second {
			t.Fatalf("levels=%d time=%s err=%v", levels, time.Since(start), err)
		}
	}
}

// loopDiamondGraph 构造 map 循环体内 levels 层菱形分叉的图：每层 f 分叉到 a、b，经 merge 汇入下一层。
func loopDiamondGraph(levels int) graph.Graph {
	body := graph.Graph{Nodes: []graph.Node{dfNode("start", Start, nil, nil)}, Edges: []graph.Edge{dfEdge("start", "f0")}}
	for i := range levels {
		f, a, b, m := fmt.Sprintf("f%d", i), fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i), fmt.Sprintf("m%d", i)
		next := "end"
		if i+1 < levels {
			next = fmt.Sprintf("f%d", i+1)
		}
		for _, id := range []string{f, a, b} {
			body.Nodes = append(body.Nodes, dfNode(id, "probe", nil, nil))
		}
		body.Nodes = append(body.Nodes, dfNode(m, Merge, MergeInput{Mode: node.JoinAll}, nil))
		body.Edges = append(body.Edges, dfEdge(f, a), dfEdge(f, b), dfEdge(a, m), dfEdge(b, m), dfEdge(m, next))
	}
	body.Nodes = append(body.Nodes, dfNode("end", End, nil, nil))
	return graph.Graph{
		InputSchema: graph.RunInputSchema(map[string]*jsonschema.Schema{
			"items": {Type: "array", Items: graph.AnyValueSchema()},
		}),
		Nodes: []graph.Node{
			dfNode("start", Start, nil, nil),
			dfNode("loop", Map, MapInput{Body: &body}, dfRefs{MapListField: {From: graph.InputSource, Field: "items"}}),
			dfNode("end", End, nil, nil),
		},
		Edges: []graph.Edge{dfEdge("start", "loop"), dfEdge("loop", "end")},
	}
}
