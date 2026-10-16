package jsonx

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
)

func ownershipSchema() *jsonschema.Schema {
	maximum := 10.0
	var constant any = map[string]any{"items": []any{int64(9007199254740993)}}
	return &jsonschema.Schema{
		Type: "object", Required: []string{"obj"}, PropertyOrder: []string{"obj"},
		Properties: map[string]*jsonschema.Schema{"obj": {
			Types: []string{"object", "null"}, Required: []string{"x"}, PropertyOrder: []string{"x"},
			Properties: map[string]*jsonschema.Schema{"x": {Type: "number", Maximum: &maximum}},
		}},
		Enum:     []any{map[string]any{"items": []any{int64(1)}}},
		Examples: []any{map[string]any{"obj": map[string]any{"x": int64(2)}}},
		Const:    &constant, Default: json.RawMessage(`{"obj":{"x":1}}`),
		DependentRequired: map[string][]string{"obj": {"other"}},
		DependencyStrings: map[string][]string{"obj": {"legacy"}},
		Vocabulary:        map[string]bool{"extension": true},
		Extra:             map[string]any{"typed": []int64{9007199254740993}, "array": [1][]string{{"x"}}},
		AllOf:             []*jsonschema.Schema{{Required: []string{"obj"}}},
		Defs:              map[string]*jsonschema.Schema{"list": {Items: &jsonschema.Schema{Enum: []any{"x"}}}},
	}
}

func TestCloneSchemaIsolatesMutableConstraints(t *testing.T) {
	for name, mutate := range map[string]func(*jsonschema.Schema){
		"nested required":       func(s *jsonschema.Schema) { s.Properties["obj"].Required[0] = "changed" },
		"nested property order": func(s *jsonschema.Schema) { s.Properties["obj"].PropertyOrder[0] = "changed" },
		"types":                 func(s *jsonschema.Schema) { s.Properties["obj"].Types[0] = "string" },
		"numeric constraint":    func(s *jsonschema.Schema) { *s.Properties["obj"].Properties["x"].Maximum = 999 },
		"enum":                  func(s *jsonschema.Schema) { s.Enum[0].(map[string]any)["items"].([]any)[0] = int64(999) },
		"examples":              func(s *jsonschema.Schema) { s.Examples[0].(map[string]any)["obj"].(map[string]any)["x"] = int64(999) },
		"const":                 func(s *jsonschema.Schema) { (*s.Const).(map[string]any)["items"].([]any)[0] = int64(999) },
		"default":               func(s *jsonschema.Schema) { s.Default[0] = '[' },
		"dependent required":    func(s *jsonschema.Schema) { s.DependentRequired["obj"][0] = "changed" },
		"legacy dependencies":   func(s *jsonschema.Schema) { s.DependencyStrings["obj"][0] = "changed" },
		"vocabulary":            func(s *jsonschema.Schema) { s.Vocabulary["extension"] = false },
		"typed extra":           func(s *jsonschema.Schema) { s.Extra["typed"].([]int64)[0] = 999 },
		"array extra":           func(s *jsonschema.Schema) { s.Extra["array"].([1][]string)[0][0] = "changed" },
		"allOf":                 func(s *jsonschema.Schema) { s.AllOf[0].Required[0] = "changed" },
		"defs items":            func(s *jsonschema.Schema) { s.Defs["list"].Items.Enum[0] = "changed" },
	} {
		t.Run(name, func(t *testing.T) {
			original, expected := ownershipSchema(), ownershipSchema()
			cloned := CloneSchema(original)
			if !reflect.DeepEqual(original, cloned) {
				t.Fatal("clone changed schema values or types")
			}
			mutate(cloned)
			if !reflect.DeepEqual(original, expected) {
				t.Fatal("mutating clone changed original schema")
			}
		})
	}
}

func TestCloneSchemaPreservesEmptyAndNil(t *testing.T) {
	if CloneSchema(nil) != nil {
		t.Fatal("nil schema must remain nil")
	}
	// 空 Enum 拒绝所有值，nil Enum 没有约束，不能互换。
	for _, s := range []*jsonschema.Schema{{}, {Enum: []any{}, Required: []string{}, Properties: map[string]*jsonschema.Schema{}}} {
		if cloned := CloneSchema(s); !reflect.DeepEqual(cloned, s) {
			t.Fatalf("clone changed empty/nil semantics: %+v", cloned)
		}
	}
}
