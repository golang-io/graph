package builtin

import (
	"encoding/json"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// mustInputs 把参数结构体编成字面量 Inputs（各键为字面量 InputRef）；null 与空串字段跳过，与 WithParams 生成默认值的规则一致。
func mustInputs(v any) map[string]graph.InputRef {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil {
		panic(err)
	}
	out := make(map[string]graph.InputRef, len(obj))
	for k, val := range obj {
		if s := string(val); s == "null" || s == `""` {
			continue
		}
		out[k] = graph.InputRef{Value: val}
	}
	return out
}

// Registry.Endpoints 返回种类声明的固定端点上限；实例启用的出口由 ActiveOutputsDeclarer（Registry.NodeEndpoints）收敛，汇合方式由 Registry.JoinMode 按参数给出。
func TestRegistryEndpoints(t *testing.T) {
	registry := testRegistry(HTTPNode{})
	endpoints := func(kind string) node.EndpointSpec {
		t.Helper()
		eps, ok := registry.Endpoints(kind)
		if !ok {
			t.Fatalf("%s not registered", kind)
		}
		return eps
	}

	sw := endpoints(Switch)
	if len(sw.Outputs) != MaxSwitchCases || sw.Outputs[0] != "Case0" || sw.Outputs[MaxSwitchCases-1] != SwitchCaseID(MaxSwitchCases-1) {
		t.Fatalf("switch outputs = %+v", sw.Outputs)
	}
	if m := endpoints(Merge); len(m.Inputs) != 1 || m.Inputs[0] != node.DefaultInput {
		t.Fatalf("merge endpoints = %+v", m)
	}
	if lp := endpoints(Map); len(lp.Outputs) != 1 || lp.Outputs[0] != node.DefaultOutput {
		t.Fatalf("map endpoints = %+v", lp)
	}
	if st := endpoints(Start); !st.IsEntry() {
		t.Fatalf("start must be entry: %+v", st)
	}
	if end := endpoints(End); !end.IsTerminal() {
		t.Fatalf("end must be terminal: %+v", end)
	}
	if stop := endpoints(StopError); len(stop.Outputs) != 1 || stop.Outputs[0] != graph.EndpointError {
		t.Fatalf("stop_error endpoints = %+v", stop)
	}
	if _, ok := registry.Endpoints("missing"); ok {
		t.Fatal("unregistered kind must report ok=false")
	}

	// switch 的种类端点仍是 16 个上限；出边只能连在配置启用的分支上（ActiveOutputs）。
	validate := func(cfg SwitchInput, branch string) error {
		return registry.Validate(graph.Graph{
			Nodes: []graph.Node{dfNode("s", Start, nil, nil), dfNode("sw", Switch, cfg, nil), dfNode("e", End, nil, nil)},
			Edges: []graph.Edge{dfEdge("s", "sw"), dfEdge("sw", "e", branch)},
		})
	}
	ifElse := SwitchInput{Expr: "true"}
	cases := SwitchInput{Cases: []SwitchCase{{ID: "Case0", Expr: "true"}, {ID: "Case3", Expr: "false"}, {ID: "Case1", IsDefault: true}}}
	for _, tc := range []struct {
		cfg    SwitchInput
		branch string
		ok     bool
	}{
		{ifElse, "Case0", true},
		{ifElse, "Case1", true},
		{ifElse, "Case2", false},
		{cases, "Case3", true},
		{cases, "Case1", true},
		{cases, "Case2", false},
	} {
		if err := validate(tc.cfg, tc.branch); (err == nil) != tc.ok {
			t.Fatalf("switch %+v edge %s: err = %v, want ok=%v", tc.cfg, tc.branch, err, tc.ok)
		}
	}

	// 运行时 switch 只能返回实例启用的分支，种类声明但未启用的分支被拒绝。
	for _, tc := range []struct {
		cfg    SwitchInput
		branch string
		ok     bool
	}{
		{ifElse, "Case1", true},
		{ifElse, "Case2", false},
		{cases, "Case3", true},
		{cases, "Case2", false},
	} {
		n := graph.Node{ID: "sw", Kind: Switch, Inputs: mustInputs(tc.cfg)}
		if err := registry.ValidateOutput(n, node.NodeOutput{Endpoint: tc.branch}); (err == nil) != tc.ok {
			t.Fatalf("switch %+v returns %s: err = %v, want ok=%v", tc.cfg, tc.branch, err, tc.ok)
		}
	}

	// merge 的空 Mode 视为 any；未声明汇合方式的节点为空。
	for _, tc := range []struct {
		n    graph.Node
		want string
	}{
		{graph.Node{ID: "m", Kind: Merge, Inputs: mustInputs(MergeInput{Mode: node.JoinAll})}, node.JoinAll},
		{graph.Node{ID: "m", Kind: Merge, Inputs: mustInputs(MergeInput{Mode: node.JoinAny})}, node.JoinAny},
		{graph.Node{ID: "m", Kind: Merge}, node.JoinAny},
		{graph.Node{ID: "t", Kind: Template}, ""},
	} {
		if got := registry.JoinMode(tc.n); got != tc.want {
			t.Fatalf("JoinMode(%s %+v) = %q, want %q", tc.n.Kind, tc.n.Inputs, got, tc.want)
		}
	}
}
