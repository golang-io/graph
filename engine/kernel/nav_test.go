package kernel

import (
	"fmt"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
)

func TestMatchingOutEdges(t *testing.T) {
	edges := []graph.Edge{
		{ID: "e2", From: "a.Out", To: "n2.In"},
		{ID: "e1", From: "a.Out", To: "n1.In"},
		{ID: "e3", From: "a.Case0", To: "n3.In"},
	}
	// 匹配 Case0 分支（按 From 端点本地名）。
	if m := matchingOutEdges(edges, "Case0"); len(m) != 1 || m[0].ID != "e3" {
		t.Fatalf("want e3, got %+v", m)
	}
	// 空分支按默认出口 Out 处理，命中全部默认边并稳定排序。
	if m := matchingOutEdges(edges, ""); len(m) != 2 || m[0].ID != "e1" || m[1].ID != "e2" {
		t.Fatalf("want [e1, e2], got %+v", m)
	}
	// 不存在的分支不回落到默认边。
	if m := matchingOutEdges(edges, "Case1"); len(m) != 0 {
		t.Fatalf("want empty, got %+v", m)
	}
}

func TestErrorRouting(t *testing.T) {
	edges := []graph.Edge{
		{ID: "e1", From: "node1.Out", To: "next.In"},
		{ID: "e_err", From: graph.FormatEndpointID("node1", graph.EndpointError), To: "handler.In"},
	}
	if e, ok := findErrorEdge(edges); !ok || e.ID != "e_err" {
		t.Fatalf("findErrorEdge = %+v %v", e, ok)
	}
	if _, ok := findErrorEdge(edges[:1]); ok {
		t.Fatal("expected no error edge")
	}
	if out := errorOutputs("failed"); len(out) != 1 || out[graph.ErrorField] != "failed" {
		t.Fatalf("errorOutputs = %+v", out)
	}
}

// 汇合按 Slot 升序合并各分支的节点输出：分叉前的节点在各分支相同，分叉后的节点互不重叠。
func TestMergeArrivals(t *testing.T) {
	before := map[string]any{"x": 1}
	merged := mergeArrivals(map[string]engine.Outputs{
		"eb": {"pre": before, "b": {"v": "valB"}},
		"ea": {"pre": before, "a": {"v": "valA"}},
	})
	if len(merged) != 3 || merged["a"]["v"] != "valA" || merged["b"]["v"] != "valB" || merged["pre"]["x"] != 1 {
		t.Fatalf("merged = %+v", merged)
	}
}

func je(id, from, to string) graph.Edge {
	return graph.Edge{ID: id, From: graph.FormatEndpointID(from, node.DefaultOutput), To: graph.FormatEndpointID(to, node.DefaultInput)}
}

// joinTarget 按内置节点注册表整理端点与汇合方式（与 Compile 相同），再调用 commonJoinTarget。
func joinTarget(t *testing.T, nodes []graph.Node, edges []graph.Edge) string {
	t.Helper()
	reg, err := node.NewRegistry(builtin.Nodes()...)
	if err != nil {
		t.Fatal(err)
	}
	byID := node.IndexNodes(nodes)
	eps := map[string]node.EndpointSpec{}
	modes := map[string]string{}
	for id, n := range byID {
		if ep, ok := reg.Endpoints(n.Kind); ok {
			eps[id] = ep
		}
		if mode := reg.JoinMode(n); mode != "" {
			modes[id] = mode
		}
	}
	out := node.IndexOutEdges(edges)
	return commonJoinTarget(modes, eps, byID, out, node.IndexInEdges(edges), out["p"])
}

// 85 层菱形（2^85 条路径）：记忆化后应在毫秒级给出最近汇入点 m0，不随路径数指数增长。
func TestCommonJoinTargetDeepDiamondIsLinear(t *testing.T) {
	nodes := []graph.Node{{ID: "p", Kind: builtin.Parallel}, {ID: "e", Kind: builtin.End}}
	var edges []graph.Edge
	prev := "p"
	for i := range 85 {
		a, b, m := fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i), fmt.Sprintf("m%d", i)
		nodes = append(nodes, graph.Node{ID: a, Kind: builtin.Template}, graph.Node{ID: b, Kind: builtin.Template}, graph.Node{ID: m, Kind: builtin.Merge})
		edges = append(edges, je(prev+"-"+a, prev, a), je(prev+"-"+b, prev, b), je(a+"-"+m, a, m), je(b+"-"+m, b, m))
		prev = m
	}
	edges = append(edges, je(prev+"-e", prev, "e"))
	start := time.Now()
	if got := joinTarget(t, nodes, edges); got != "m0" {
		t.Fatalf("join target = %q, want m0", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("join analysis took %s", elapsed)
	}
}

func TestCommonJoinTargetSemantics(t *testing.T) {
	tpl := func(id string) graph.Node { return graph.Node{ID: id, Kind: builtin.Template} }
	p := graph.Node{ID: "p", Kind: builtin.Parallel}
	m := graph.Node{ID: "m", Kind: builtin.Merge}
	end := func(id string) graph.Node { return graph.Node{ID: id, Kind: builtin.End} }
	cases := []struct {
		name  string
		nodes []graph.Node
		edges []graph.Edge
		want  string
	}{
		{"分支各自结束时无汇入点", []graph.Node{p, tpl("a"), tpl("b"), end("e1"), end("e2")},
			[]graph.Edge{je("pa", "p", "a"), je("pb", "p", "b"), je("ae", "a", "e1"), je("be", "b", "e2")}, ""},
		{"多入边的普通节点也可作为汇入点，且取最近的一个", []graph.Node{p, tpl("a"), tpl("b"), tpl("j"), m, end("e")},
			[]graph.Edge{je("pa", "p", "a"), je("pb", "p", "b"), je("aj", "a", "j"), je("bj", "b", "j"), je("jm", "j", "m"), je("me", "m", "e")}, "j"},
		{"一条分支提前结束时不能汇入", []graph.Node{p, tpl("a"), tpl("b"), m, end("e"), end("e2")},
			[]graph.Edge{je("pa", "p", "a"), je("pb", "p", "b"), je("am", "a", "m"), je("be2", "b", "e2"), je("me", "m", "e")}, ""},
		{"map 是单出口的普通节点，沿 out 汇入 merge", []graph.Node{p, {ID: "l", Kind: builtin.Map}, tpl("b"), m, end("e")},
			[]graph.Edge{je("pl", "p", "l"), je("pb", "p", "b"), je("lm", "l", "m"), je("bm", "b", "m"), je("me", "m", "e")}, "m"},
		{"分支进入不经过候选的环时不能汇入", []graph.Node{p, tpl("x"), tpl("y"), tpl("b"), m, end("e")},
			[]graph.Edge{je("px", "p", "x"), je("pb", "p", "b"), je("xy", "x", "y"), je("yx", "y", "x"), je("bm", "b", "m"), je("me", "m", "e")}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := joinTarget(t, tc.nodes, tc.edges); got != tc.want {
				t.Fatalf("join target = %q, want %q", got, tc.want)
			}
		})
	}
}
