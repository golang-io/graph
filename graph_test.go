package graph

import (
	"encoding/json"
	"testing"
)

func demo() Graph {
	return Graph{
		Nodes: []Node{
			{ID: "t", Kind: "trigger"},
			{ID: "h", Kind: "http", Inputs: map[string]InputRef{
				"URL":    {Value: json.RawMessage(`"https://x.test"`)},
				"Method": {Value: json.RawMessage(`"GET"`)},
			}},
			{ID: "e", Kind: "end"},
		},
		Edges: []Edge{
			{ID: "e1", From: "t.Out", To: "h.In"},
			{ID: "e2", From: "h.Out", To: "e.In"},
		},
	}
}

func TestValidate(t *testing.T) {
	if err := demo().Validate(); err != nil {
		t.Fatalf("demo graph should be valid: %v", err)
	}
	ok := Graph{Nodes: []Node{{ID: "h", Kind: "http"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("graph without entry is structurally valid: %v", err)
	}
	inbound := Graph{
		Nodes: []Node{{ID: "t", Kind: "trigger"}, {ID: "e", Kind: "end"}},
		Edges: []Edge{{ID: "e1", From: "e.Out", To: "t.In"}},
	}
	if err := inbound.Validate(); err != nil {
		t.Fatalf("entry inbound is structurally valid: %v", err)
	}
	bad := map[string]Graph{
		"empty":          {},
		"dangling edge":  {Nodes: []Node{{ID: "t", Kind: "trigger"}}, Edges: []Edge{{ID: "e1", From: "t.Out", To: "nope.In"}}},
		"duplicate node": {Nodes: []Node{{ID: "t", Kind: "trigger"}, {ID: "t", Kind: "end"}}},
	}
	for name, g := range bad {
		if err := g.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

// 节点与边的书写顺序不参与执行图内容。HashDefinition 先按 ID 排序再哈希，供编译缓存与图比较使用。
func TestHashDefinitionIgnoresOrder(t *testing.T) {
	a := demo()
	b := demo()
	b.Nodes[0], b.Nodes[2] = b.Nodes[2], b.Nodes[0]
	b.Edges[0], b.Edges[1] = b.Edges[1], b.Edges[0]

	_, hashA, err := HashDefinition(a)
	if err != nil {
		t.Fatal(err)
	}
	_, hashB, err := HashDefinition(b)
	if err != nil {
		t.Fatal(err)
	}
	if hashA != hashB {
		t.Fatalf("hash changed with node order: %s vs %s", hashA, hashB)
	}
	if len(hashA) != 64 {
		t.Fatalf("unexpected hash %q", hashA)
	}
}
