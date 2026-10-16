package graph

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func TestValidateGraphInputSchemaRequiresTypes(t *testing.T) {
	g := Graph{
		Nodes: []Node{{ID: "a", Kind: "x"}},
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				"name": {},
			},
			AdditionalProperties: SchemaFalse(),
		},
	}
	err := g.Validate()
	if err == nil || !strings.Contains(err.Error(), "missing type") {
		t.Fatalf("want missing type, got %v", err)
	}
}

func TestValidateGraphInputSchemaTypedOK(t *testing.T) {
	g := Graph{
		Nodes: []Node{{ID: "a", Kind: "x"}},
		InputSchema: RunInputSchema(map[string]*jsonschema.Schema{
			"name":  {Type: "string"},
			"score": {Type: "integer"},
			"items": {Type: "array", Items: &jsonschema.Schema{Type: "string"}},
		}),
	}
	if err := g.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestRunInputSchemaPanicsWithoutType(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic")
		}
	}()
	_ = RunInputSchema(map[string]*jsonschema.Schema{"x": {}})
}

// TestValidateGraphInputSchemaRejectsNonFalseAdditionalProperties 确认 additionalProperties 仅接受规范的 false Schema，
// 拒绝 {"not":{"enum":[1]}} 等非空 Not 约束（R08）。
func TestValidateGraphInputSchemaRejectsNonFalseAdditionalProperties(t *testing.T) {
	for _, raw := range []string{
		`{"type":"object","properties":{},"additionalProperties":{"not":{"enum":[1]}}}`,
		`{"type":"object","properties":{},"additionalProperties":{"not":{"const":1}}}`,
		`{"type":"object","properties":{},"additionalProperties":true}`,
	} {
		var schema jsonschema.Schema
		if err := json.Unmarshal([]byte(raw), &schema); err != nil {
			t.Fatal(err)
		}
		g := Graph{InputSchema: &schema, Nodes: []Node{{ID: "a", Kind: "x"}}}
		if err := g.Validate(); err == nil || !strings.Contains(err.Error(), "additionalProperties must be false") {
			t.Fatalf("schema %s: err = %v, want additionalProperties must be false", raw, err)
		}
	}
}
