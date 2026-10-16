package kernel

import (
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
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

// 汇合按入边 ID（Slot）升序合并各分支的节点输出：分叉前的节点在各分支相同，分叉后的节点互不重叠。
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

func TestOnlyLeadsTo(t *testing.T) {
	// fork -> a, b, c; a, b -> m1 -> sideeffect -> m2; c -> m2 -> end
	edges := []graph.Edge{
		je("fa", "fork", "a"),
		je("fb", "fork", "b"),
		je("fc", "fork", "c"),
		je("am1", "a", "m1"),
		je("bm1", "b", "m1"),
		je("m1s", "m1", "sideeffect"),
		je("sm2", "sideeffect", "m2"),
		je("cm2", "c", "m2"),
		je("m2e", "m2", "end"),
	}
	out := node.IndexOutEdges(edges)
	if !onlyLeadsTo(out, "a", "m1") || !onlyLeadsTo(out, "b", "m1") {
		t.Fatal("a and b must only lead to m1")
	}
	if onlyLeadsTo(out, "c", "m1") {
		t.Fatal("c does not lead to m1")
	}
	if !onlyLeadsTo(out, "a", "m2") || !onlyLeadsTo(out, "c", "m2") {
		t.Fatal("a and c must lead to m2")
	}
	if onlyLeadsTo(out, "end", "m2") {
		t.Fatal("terminal node without outgoing edges cannot lead to m2")
	}
}
