package node

import (
	"reflect"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// 参数结构体字段标签（由 applyStructInputSchema 读入 InputSchema）：
//   - title:"表单标签" → 进属性面板（JSON Schema title）
//   - enum:"A,B,C" → 下拉候选项
//   - bind:"" → 输入绑定建议槽（无 title）；无 omitempty 时加入 required
// 既无 title 也无 bind 的字段仍参与解码 / DefaultInputs，但不出现在 InputSchema.properties。

// applyStructInputSchema 按结构体标签裁剪并标注 InputSchema（须先经 jsonschema.For 生成底稿）。
func applyStructInputSchema[T any](schema *jsonschema.Schema) *jsonschema.Schema {
	if schema == nil {
		schema = &jsonschema.Schema{Type: "object"}
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	schema.AdditionalProperties = &jsonschema.Schema{}

	t := reflect.TypeFor[T]()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return schema
	}

	keep := map[string]bool{}
	order := make([]string, 0, t.NumField())
	required := map[string]bool{}

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || f.Anonymous {
			continue
		}
		name, omitempty := jsonFieldName(f)
		if name == "" || name == "-" {
			continue
		}
		title := strings.TrimSpace(f.Tag.Get("title"))
		_, hasBind := f.Tag.Lookup("bind")
		if title == "" && !hasBind {
			continue // 不进属性面板 / 绑定槽
		}
		prop := schema.Properties[name]
		if prop == nil {
			prop = &jsonschema.Schema{}
			schema.Properties[name] = prop
		}
		if title != "" {
			prop.Title = title
		}
		if enumTag := strings.TrimSpace(f.Tag.Get("enum")); enumTag != "" {
			parts := strings.Split(enumTag, ",")
			prop.Enum = make([]any, 0, len(parts))
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					prop.Enum = append(prop.Enum, p)
				}
			}
		}
		keep[name] = true
		order = append(order, name)
		if !omitempty {
			required[name] = true
		}
	}

	for name := range schema.Properties {
		if !keep[name] {
			delete(schema.Properties, name)
		}
	}
	newReq := make([]string, 0, len(required))
	for _, name := range order {
		if required[name] {
			newReq = append(newReq, name)
		}
	}
	for _, name := range schema.Required {
		if keep[name] && !slices.Contains(newReq, name) {
			newReq = append(newReq, name)
		}
	}
	schema.Required = newReq
	schema.PropertyOrder = order
	return schema
}

// jsonFieldName 返回 json 标签中的字段名，以及是否带 omitempty/omitzero。
func jsonFieldName(f reflect.StructField) (name string, omitempty bool) {
	tag := f.Tag.Get("json")
	if tag == "" {
		return f.Name, false
	}
	parts := strings.Split(tag, ",")
	name = parts[0]
	if name == "" {
		name = f.Name
	}
	for _, p := range parts[1:] {
		if p == "omitempty" || p == "omitzero" {
			omitempty = true
		}
	}
	return name, omitempty
}

// inputSchemaRequired 返回 InputSchema.required（副本）。
func inputSchemaRequired(spec NodeSpec) []string {
	if spec.InputSchema == nil {
		return nil
	}
	return slices.Clone(spec.InputSchema.Required)
}
