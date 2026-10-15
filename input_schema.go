package graph

import (
	"fmt"

	"github.com/google/jsonschema-go/jsonschema"
)

// schemaFalse 在 JSON 中编码为 false（jsonschema-go：{"not":true} → false）。
var schemaFalse = &jsonschema.Schema{Not: &jsonschema.Schema{}}

// RunInputSchema 构造图级运行输入 Schema：所列名为 properties 且全部 required，禁止未声明键。
// 供测试与示例装配；作者态也可手写等价 JSON。
func RunInputSchema(names ...string) *jsonschema.Schema {
	if len(names) == 0 {
		return nil
	}
	props := make(map[string]*jsonschema.Schema, len(names))
	order := make([]string, 0, len(names))
	required := make([]string, 0, len(names))
	for _, name := range names {
		if !ValidName(name) {
			panic(fmt.Sprintf("graph: invalid run input name %q", name))
		}
		if _, dup := props[name]; dup {
			panic(fmt.Sprintf("graph: duplicate run input name %q", name))
		}
		props[name] = &jsonschema.Schema{}
		order = append(order, name)
		required = append(required, name)
	}
	return &jsonschema.Schema{
		Type:                 "object",
		Properties:           props,
		Required:             required,
		PropertyOrder:        order,
		AdditionalProperties: schemaFalse,
	}
}
