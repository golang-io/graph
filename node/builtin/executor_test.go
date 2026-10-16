package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
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

func TestMapControlPreservesLargeInt64AcrossRounds(t *testing.T) {
	items := []any{
		int64(9007199254740993),  // 2^53 + 1
		int64(9007199254740995),  // 2^53 + 3
		int64(-9007199254740993), // -(2^53 + 1)
		int64(math.MaxInt64),
		int64(math.MinInt64),
		map[string]any{"id": int64(9007199254740993), "arr": []any{int64(9007199254740995)}},
	}
	cfg := MapInput{
		Chunk:       1,
		MaxParallel: 1,
		Body:        defaultMapBody(),
	}
	vars := map[string]any{MapListField: items}

	// 首轮 start：启动第 0 轮，后续 5 项保存在 State.Units 中。
	out, err := MapNode{}.Control(context.Background(), node.ControlInput{
		NodeID: "m",
		Vars:   vars,
		Params: cfg,
		Event:  node.Event{Kind: node.EventStart},
	})
	if err != nil {
		t.Fatalf("start err: %v", err)
	}

	// 逐轮推进 child_done，检查每轮派发的 Spawn.Input[Item] 以及最终汇总的 Results 均保持精确 int64。
	for i := range items {
		if len(out.Effect.Spawns) != 1 {
			t.Fatalf("round %d spawns = %d, want 1", i, len(out.Effect.Spawns))
		}
		gotItem := out.Effect.Spawns[0].Input[MapItemField]
		if !reflect.DeepEqual(gotItem, items[i]) {
			t.Fatalf("round %d spawn item = %#v (%T), want %#v (%T)", i, gotItem, gotItem, items[i], items[i])
		}
		out, err = MapNode{}.Control(context.Background(), node.ControlInput{
			NodeID: "m",
			Vars:   vars,
			Params: cfg,
			State:  out.State,
			Event: node.Event{
				Kind:    node.EventChildDone,
				Slot:    out.Effect.Spawns[0].Slot,
				Payload: map[string]any{"Value": gotItem},
			},
		})
		if err != nil {
			t.Fatalf("round %d child_done err: %v", i, err)
		}
	}

	wantResults := make([]any, len(items))
	for i, item := range items {
		wantResults[i] = map[string]any{"Value": item}
	}
	if !reflect.DeepEqual(out.Set[MapResultsField], wantResults) {
		t.Fatalf("final results = %#v, want %#v", out.Set[MapResultsField], wantResults)
	}
}
