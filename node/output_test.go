package node

import (
	"testing"

	"github.com/golang-io/graph/internal/jsonx"
)

// TestOutputSchemaEmpty 确认空名字列表得到 nil Schema（节点没有声明输出）。
func TestOutputSchemaEmpty(t *testing.T) {
	if OutputSchema() != nil {
		t.Fatal("empty names should yield nil schema")
	}
}

// TestMergeOutputSchemas 确认合并后按各 Schema 的 PropertyOrder 依次追加属性。
func TestMergeOutputSchemas(t *testing.T) {
	got, err := mergeOutputSchemas(OutputSchema("a", "b"), OutputSchema("err"))
	if err != nil {
		t.Fatal(err)
	}
	if names := jsonx.PropertyNames(got); len(names) != 3 || names[2] != "err" {
		t.Fatalf("names = %v", names)
	}
}
