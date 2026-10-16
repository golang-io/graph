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

// TestOutputSchemaPropertyOrder 确认按传入顺序生成 PropertyOrder。
func TestOutputSchemaPropertyOrder(t *testing.T) {
	got := OutputSchema("a", "b", "c")
	if names := jsonx.PropertyNames(got); len(names) != 3 || names[0] != "a" || names[2] != "c" {
		t.Fatalf("names = %v", names)
	}
}
