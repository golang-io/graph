package builtin

import (
	"context"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// Merge 的汇合字段按 First 顺序取第一个已产生的来源；都没有时报错。
func TestMergeExecuteFirst(t *testing.T) {
	produced := map[string]map[string]any{"lo": {"tag": "lo"}}
	runInput := map[string]any{"name": "Ada"}
	lookup := func(ref graph.InputRef) (any, bool) {
		if ref.From == graph.InputSource {
			v, ok := runInput[ref.Field]
			return v, ok
		}
		v, ok := produced[ref.From][ref.Field]
		return v, ok
	}
	cfg := MergeInput{Mode: node.JoinAny, Outputs: []MergeFirstField{
		{Name: "tag", First: []graph.InputRef{{From: "hi", Field: "tag"}, {From: "lo", Field: "tag"}}},
		{Name: "name", First: []graph.InputRef{{From: graph.InputSource, Field: "name"}}},
	}}
	out, err := MergeNode{}.Execute(context.Background(), node.NodeInput{Params: cfg, Lookup: lookup})
	if err != nil || out.Set["tag"] != "lo" || out.Set["name"] != "Ada" {
		t.Fatalf("Execute = %+v, %v", out, err)
	}

	cfg.Outputs = cfg.Outputs[:1]
	empty := func(graph.InputRef) (any, bool) { return nil, false }
	if _, err := (MergeNode{}).Execute(context.Background(), node.NodeInput{Params: cfg, Lookup: empty}); err == nil {
		t.Fatal("expected error when no First source has produced the field")
	}
}

// 同名汇合字段在运行期会互相覆盖，配置校验直接拒绝。
func TestMergeInputDuplicateName(t *testing.T) {
	ref := []graph.InputRef{{From: "a", Field: "v"}}
	cfg := MergeInput{Mode: node.JoinAny, Outputs: []MergeFirstField{{Name: "v", First: ref}, {Name: "v", First: ref}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected duplicate Name to be rejected")
	}
}
