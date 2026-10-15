package service

import (
	"testing"
)

func TestExampleJSON(t *testing.T) {
	ids, err := ExampleIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) == 0 {
		t.Fatal("no example graphs")
	}
	for _, id := range ids {
		g, err := readDefinition(id)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if len(g.Graph.Nodes) == 0 {
			t.Fatalf("%s: empty graph", id)
		}
		if err := g.Graph.Validate(); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
	}
}
