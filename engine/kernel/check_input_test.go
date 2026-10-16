package kernel

import (
	"strings"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/google/jsonschema-go/jsonschema"
)

func TestCheckInputRejectsWrongType(t *testing.T) {
	schema := graph.RunInputSchema(map[string]*jsonschema.Schema{
		"name":  {Type: "string"},
		"score": {Type: "number"},
		"items": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
	})
	err := CheckInput(schema, map[string]any{
		"name":  int64(22),
		"score": int64(33),
		"items": []any{"a", "b"},
	})
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("want type error on name, got %v", err)
	}
	if !strings.Contains(err.Error(), engine.ErrInvalid.Error()) {
		t.Fatalf("want ErrInvalid wrap, got %v", err)
	}
}

func TestCheckInputAcceptsTypedValues(t *testing.T) {
	schema := graph.RunInputSchema(map[string]*jsonschema.Schema{
		"name":  {Type: "string"},
		"score": {Type: "number"},
		"items": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
	})
	if err := CheckInput(schema, map[string]any{
		"name":  "Ada",
		"score": int64(40),
		"items": []any{"a", "b", "c"},
	}); err != nil {
		t.Fatal(err)
	}
}
