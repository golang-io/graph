package builtin

import (
	"context"
	"testing"

	"github.com/golang-io/graph/node"
)

func TestApplyReduceSum(t *testing.T) {
	set, err := ApplyReduce(ReduceInput{Init: "0", Expr: "Acc + Item"}, map[string]any{ReduceListField: []any{1, 2, 3, 4}})
	if err != nil {
		t.Fatal(err)
	}
	switch v := set[ReduceResultField].(type) {
	case int:
		if v != 10 {
			t.Fatalf("sum = %v", v)
		}
	case int64:
		if v != 10 {
			t.Fatalf("sum = %v", v)
		}
	case float64:
		if v != 10 {
			t.Fatalf("sum = %v", v)
		}
	default:
		t.Fatalf("sum = %T %v", set[ReduceResultField], set[ReduceResultField])
	}
}

func TestReduceExecutor(t *testing.T) {
	// 自定义输入 sep 可在 Init 与 Expr 中使用；输出只有 result。
	out, err := ReduceNode{}.Execute(context.Background(), node.NodeInput{
		Vars:   map[string]any{ReduceListField: []any{"a", "b", "c"}, "sep": "-"},
		Params: ReduceInput{Init: `sep`, Expr: `Acc + Item + sep`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Set) != 1 || out.Set[ReduceResultField] != "-a-b-c-" {
		t.Fatalf("output = %v", out.Set)
	}
}
