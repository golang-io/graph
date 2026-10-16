package node

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/google/jsonschema-go/jsonschema"
)

// EndpointPayloadDefault 是 OutputsByEndpoint 的默认键：表示所有正常出口共享的载荷 Schema。
const EndpointPayloadDefault = ""

// OutputFrom 由输出结构体生成 OutputsByEndpoint：字段名取 json 标签（与 Run.Outputs[节点ID] 的键一致），
// 全部归入 EndpointPayloadDefault。供 NodeSpec 装配；不参与运行时解码。
func OutputFrom[T any]() map[string]*jsonschema.Schema {
	base, err := jsonschema.For[T](&jsonschema.ForOptions{IgnoreInvalidTypes: true})
	if err != nil {
		var z T
		panic(fmt.Sprintf("graph/node: output schema for %T: %v", z, err))
	}
	base = ensureOutputProperties[T](base)
	if len(base.PropertyOrder) == 0 {
		return nil
	}
	props := make(map[string]*jsonschema.Schema, len(base.PropertyOrder))
	for _, name := range base.PropertyOrder {
		props[name] = base.Properties[name].CloneSchemas()
	}
	return map[string]*jsonschema.Schema{EndpointPayloadDefault: {
		Type:          "object",
		Properties:    props,
		PropertyOrder: slices.Clone(base.PropertyOrder),
	}}
}

// ensureOutputProperties 保证结构体每个导出 json 字段都在 properties 中（any 被 For 跳过时补空 Schema）。
func ensureOutputProperties[T any](schema *jsonschema.Schema) *jsonschema.Schema {
	if schema == nil {
		schema = &jsonschema.Schema{Type: "object"}
	}
	if schema.Properties == nil {
		schema.Properties = map[string]*jsonschema.Schema{}
	}
	t := reflect.TypeFor[T]()
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return schema
	}
	order := make([]string, 0, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || f.Anonymous {
			continue
		}
		name, _ := jsonFieldName(f)
		if name == "" || name == "-" {
			continue
		}
		if schema.Properties[name] == nil {
			schema.Properties[name] = &jsonschema.Schema{}
		}
		order = append(order, name)
	}
	schema.Type = "object"
	schema.PropertyOrder = order
	return schema
}

// WithOutput 将 T 的输出 Schema 写入 Spec.OutputsByEndpoint（所有正常出口共用），与 WithParams 配套使用。
//
// WithOutput sets spec.OutputsByEndpoint from the output struct T.
func WithOutput[T any](spec NodeSpec) NodeSpec {
	spec.OutputsByEndpoint = OutputFrom[T]()
	return spec
}

// cloneOutputsByEndpoint 深拷贝出端点载荷表。
func cloneOutputsByEndpoint(m map[string]*jsonschema.Schema) map[string]*jsonschema.Schema {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]*jsonschema.Schema, len(m))
	for ep, s := range m {
		out[ep] = jsonx.CloneSchema(s)
	}
	return out
}

// OutputSchema 由输出名列表构造无类型约束的输出 object Schema，
// 供 OutputsByEndpoint 与 OutputDeclarer.DeclareOutputs 使用；名字非法或重复时 panic（属于编写期错误）。
// OutputSchema builds an untyped object schema from output names.
func OutputSchema(names ...string) *jsonschema.Schema {
	if len(names) == 0 {
		return nil
	}
	props := make(map[string]*jsonschema.Schema, len(names))
	for _, name := range names {
		if !graph.ValidName(name) {
			panic(fmt.Sprintf("graph: invalid output name %q", name))
		}
		if _, dup := props[name]; dup {
			panic(fmt.Sprintf("graph: duplicate output name %q", name))
		}
		props[name] = &jsonschema.Schema{}
	}
	return &jsonschema.Schema{
		Type:          "object",
		Properties:    props,
		PropertyOrder: slices.Clone(names),
	}
}

// OutputFielder 是 OutputDeclarer 的可选配套接口：直接报告参数 cfg 是否声明了输出字段 field，
// 省去在引用校验与 CheckOutput 中为仅判断字段存在性而构造完整的 *jsonschema.Schema。
type OutputFielder interface {
	HasOutput(field string) bool
}

// hasDeclaredOutput 报告节点实例 (spec, cfg) 是否声明了输出字段 field。
func hasDeclaredOutput(spec NodeSpec, cfg any, field string) bool {
	if f, ok := cfg.(OutputFielder); ok {
		return f.HasOutput(field)
	}
	return hasOutputField(nodeOutputs(spec, cfg), field)
}

// hasOutputField 报告节点输出表中是否声明了字段 field。
func hasOutputField(byEndpoint map[string]*jsonschema.Schema, field string) bool {
	for _, schema := range byEndpoint {
		if schema != nil && schema.Properties != nil && schema.Properties[field] != nil {
			return true
		}
	}
	return false
}

// nodeOutputs 返回节点实例按出口划分的载荷表（出口名 → 载荷 Schema，只读）：
// 参数实现 OutputDeclarer 时取其推导的输出，否则取种类固定的 OutputsByEndpoint。
func nodeOutputs(spec NodeSpec, cfg any) map[string]*jsonschema.Schema {
	if d, ok := cfg.(OutputDeclarer); ok {
		if more := d.DeclareOutputs(); more != nil {
			return map[string]*jsonschema.Schema{EndpointPayloadDefault: more}
		}
		return nil
	}
	return spec.OutputsByEndpoint
}
