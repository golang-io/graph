package builtin

import (
	"strings"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// sidesFor 为 nodes 中每个节点的每个实例端点写入所在边：入口 left，出口 right。
func sidesFor(t *testing.T, r *node.Registry, nodes []graph.Node) map[string]graph.EndpointSides {
	t.Helper()
	sides := map[string]graph.EndpointSides{}
	for _, n := range nodes {
		eps, err := r.NodeEndpoints(n)
		if err != nil {
			t.Fatal(err)
		}
		p := graph.EndpointSides{}
		for _, ep := range eps.Inputs {
			p[ep] = graph.SideLeft
		}
		for _, ep := range eps.Outputs {
			p[ep] = graph.SideRight
		}
		sides[n.ID] = p
	}
	return sides
}

// TestCleanEndpointSides 覆盖保存时的端点所在边：主图与 map 循环体（SubLayouts）每个节点的每个端点都要写明，
// 出口不能与入口同边，失效条目被清除且不修改入参。
func TestCleanEndpointSides(t *testing.T) {
	registry := dfRegistry()
	g, body := mapGraph(nil), mapBody(nil)
	layout := func() graph.Layout {
		return graph.Layout{
			EndpointSides: sidesFor(t, registry, g.Nodes),
			SubLayouts:    map[string]graph.Layout{"lp": {EndpointSides: sidesFor(t, registry, body.Nodes)}},
		}
	}
	full := layout()
	// 失效条目：不存在的端点、不在图中的节点
	full.EndpointSides["greet"]["Case3"] = graph.SideTop
	full.EndpointSides["gone"] = graph.EndpointSides{"In": graph.SideLeft}
	cleaned, err := registry.CleanEndpointSides(g, full)
	if err != nil {
		t.Fatalf("complete layout rejected: %v", err)
	}
	if _, ok := cleaned.EndpointSides["greet"]["Case3"]; ok {
		t.Fatal("stale endpoint entry kept")
	}
	if _, ok := cleaned.EndpointSides["gone"]; ok {
		t.Fatal("entry of removed node kept")
	}
	if _, ok := full.EndpointSides["greet"]["Case3"]; !ok {
		t.Fatal("input layout modified")
	}
	if len(cleaned.SubLayouts["lp"].EndpointSides) != len(body.Nodes) {
		t.Fatalf("body endpoint sides: %+v", cleaned.SubLayouts["lp"].EndpointSides)
	}

	cases := map[string]func(l *graph.Layout){
		// 主图节点缺整项 EndpointSides
		`node after endpoint sides required`: func(l *graph.Layout) { delete(l.EndpointSides, "after") },
		// 主图节点缺一个端点
		`node greet endpoint sides: endpoint Out side required`: func(l *graph.Layout) { delete(l.EndpointSides["greet"], "Out") },
		// 出口与入口同边
		`node greet endpoint sides: output Out and an input are both "left"`: func(l *graph.Layout) { l.EndpointSides["greet"]["Out"] = graph.SideLeft },
		// 循环体缺少整层子图布局
		`node "lp" Body: graph: node start endpoint sides required`: func(l *graph.Layout) { delete(l.SubLayouts, "lp") },
		// 循环体中某个节点缺 EndpointSides
		`node "lp" Body: graph: node body endpoint sides required`: func(l *graph.Layout) { delete(l.SubLayouts["lp"].EndpointSides, "body") },
	}
	for want, mutate := range cases {
		l := layout()
		mutate(&l)
		if _, err := registry.CleanEndpointSides(g, l); err == nil || !strings.Contains(err.Error(), want) {
			t.Fatalf("want error containing %q, got %v", want, err)
		}
	}
}

// TestCleanEndpointSidesSwitchCases 覆盖 switch：只要求配置中启用的分支写明所在边，删掉的分支条目被清除。
func TestCleanEndpointSidesSwitchCases(t *testing.T) {
	registry := dfRegistry()
	sw := graph.Node{ID: "sw", Kind: Switch}
	eps, err := registry.NodeEndpoints(sw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(eps.Outputs, ",") != "Case0,Case1" {
		t.Fatalf("IF/ELSE outputs: %v", eps.Outputs)
	}
	g := graph.Graph{Nodes: []graph.Node{sw}}
	l := graph.Layout{EndpointSides: map[string]graph.EndpointSides{"sw": {"In": "left", "Case0": "right", "Case1": "bottom", "Case2": "right", "Out": "right"}}}
	cleaned, err := registry.CleanEndpointSides(g, l)
	if err != nil {
		t.Fatal(err)
	}
	if got := cleaned.EndpointSides["sw"]; len(got) != 3 || got["Case1"] != "bottom" {
		t.Fatalf("switch endpoint sides: %+v", got)
	}
	delete(l.EndpointSides["sw"], "Case1")
	if _, err := registry.CleanEndpointSides(g, l); err == nil || !strings.Contains(err.Error(), "endpoint Case1 side required") {
		t.Fatalf("missing case: %v", err)
	}
}
