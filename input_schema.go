package graph

import (
	"fmt"
	"maps"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
)

// SchemaFalse 返回 JSON Schema 的 false（禁止匹配），供图级 / 子图 InputSchema 的 additionalProperties 使用。
// 每次返回独立实例，避免调用方修改共享指针。
func SchemaFalse() *jsonschema.Schema { return &jsonschema.Schema{Not: &jsonschema.Schema{}} }

// AnyValueSchema 表示可接受任意 JSON 值的属性 Schema（七种 JSON 类型并集）。
// 用于 map 循环体的 Item 等「元素类型由上游 List 决定」的槽位；禁止再用空 {}。
func AnyValueSchema() *jsonschema.Schema {
	return &jsonschema.Schema{
		Types: []string{"string", "integer", "number", "boolean", "object", "array", "null"},
	}
}

// RunInputSchema 构造图级运行输入 Schema：props 每个值必须带 Type（或 Types），全部列为 required，禁止未声明键。
// 供测试与示例装配；作者态也可手写等价 JSON。props 为空时返回 nil。
func RunInputSchema(props map[string]*jsonschema.Schema) *jsonschema.Schema {
	if len(props) == 0 {
		return nil
	}
	out := make(map[string]*jsonschema.Schema, len(props))
	order := make([]string, 0, len(props))
	required := make([]string, 0, len(props))
	for _, name := range slices.Sorted(maps.Keys(props)) {
		if !ValidName(name) {
			panic(fmt.Sprintf("graph: invalid run input name %q", name))
		}
		prop := props[name]
		if prop == nil {
			panic(fmt.Sprintf("graph: run input %q has nil schema", name))
		}
		if prop.Type == "" && len(prop.Types) == 0 {
			panic(fmt.Sprintf("graph: run input %q missing type", name))
		}
		out[name] = prop
		order = append(order, name)
		required = append(required, name)
	}
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           out,
		Required:             required,
		PropertyOrder:        order,
		AdditionalProperties: SchemaFalse(),
	}
}

// StringProps 为测试快捷构造：每个名字对应 type=string 的属性。
func StringProps(names ...string) map[string]*jsonschema.Schema {
	props := make(map[string]*jsonschema.Schema, len(names))
	for _, name := range names {
		props[name] = &jsonschema.Schema{Type: "string"}
	}
	return props
}
