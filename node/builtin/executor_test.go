package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

func TestCodeDoesNotMutateCallerVars(t *testing.T) {
	vars := map[string]any{
		"a": int64(4), "name": "SQLite",
	}
	cfg := CodeInput{
		Outputs: []graph.OutKV{
			{Key: "sum", Value: "a + 6"},
			{Key: "label", Value: "name + string(sum)"},
		},
	}
	before, _ := json.Marshal(vars)
	// CodeNode 不得原地改动调用方传入的 Vars。表达式求值得到 Go 的 int；落库前由内核统一规整为 int64。
	out, err := CodeNode{}.Execute(context.Background(), node.NodeInput{Vars: vars, Params: cfg})
	if err != nil || out.Set["sum"] != 10 || out.Set["label"] != "SQLite10" {
		t.Fatalf("output=%v err=%v", out, err)
	}
	after, _ := json.Marshal(vars)
	if string(before) != string(after) {
		t.Fatal("executor mutated caller vars")
	}
}

func TestLLMRendersPromptAndWritesText(t *testing.T) {
	called := false
	executor := LLMNode{Invoke: func(_ context.Context, in node.NodeInput, cfg LLMInput) (string, error) {
		called = true
		if in.Key.IdempotencyKey() != "run-1:3" || cfg.Prompt != "Hello SQLite" || cfg.Model != "test/model" {
			t.Fatalf("LLM input: %+v %+v", in, cfg)
		}
		return "generated", nil
	}}
	out, err := executor.Execute(context.Background(), node.NodeInput{
		Key:    node.RunKey{TaskID: "task-1", RunID: "run-1", Seq: 3, Attempt: 1},
		Vars:   map[string]any{"name": "SQLite"},
		Params: LLMInput{Model: "test/model", Prompt: "Hello {{name}}"},
	})
	if err != nil || !called || len(out.Set) != 1 || out.Set[LLMTextField] != "generated" {
		t.Fatalf("LLM=%v err=%v", out, err)
	}
}

func TestLLMRequiresInvoker(t *testing.T) {
	_, err := LLMNode{}.Execute(context.Background(), node.NodeInput{
		Params: LLMInput{Model: "m", Prompt: "p"},
	})
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err=%v", err)
	}
}
