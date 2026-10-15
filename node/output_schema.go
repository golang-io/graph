package node

import (
	"fmt"
	"reflect"
	"slices"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/google/jsonschema-go/jsonschema"
)

// EndpointPayloadDefault 是 OutputsByEndpoint 的默认键：表示所有正常出口（不含 error）共享的载荷 Schema。
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

// mergeOutputsByEndpoint 合并多份出端点表；同端点上的 Schema 做 properties 合并（后者覆盖同名键）。
func mergeOutputsByEndpoint(parts ...map[string]*jsonschema.Schema) (map[string]*jsonschema.Schema, error) {
	var out map[string]*jsonschema.Schema
	for _, part := range parts {
		if len(part) == 0 {
			continue
		}
		if out == nil {
			out = map[string]*jsonschema.Schema{}
		}
		for ep, schema := range part {
			if ep != EndpointPayloadDefault && !graph.ValidID(ep) {
				return nil, fmt.Errorf("invalid output endpoint %q", ep)
			}
			merged, err := mergeOutputSchemas(out[ep], schema)
			if err != nil {
				return nil, err
			}
			out[ep] = merged
		}
	}
	// 同一字段不得出现在多个出端点的 Schema 中。
	owner := map[string]string{}
	for ep, schema := range out {
		for _, name := range jsonx.PropertyNames(schema) {
			if prev, ok := owner[name]; ok && prev != ep {
				return nil, fmt.Errorf("output field %q claimed by endpoints %q and %q", name, prev, ep)
			}
			owner[name] = ep
		}
	}
	return out, nil
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

// mergeOutputSchemas 合并多个输出 Schema 的 properties（后者覆盖同名键）；顺序为各 Schema 的 PropertyOrder 依次追加。
// 任一侧出现非法输出名时返回错误。
func mergeOutputSchemas(parts ...*jsonschema.Schema) (*jsonschema.Schema, error) {
	props := map[string]*jsonschema.Schema{}
	var order []string
	for _, s := range parts {
		if s == nil {
			continue
		}
		for _, name := range jsonx.PropertyNames(s) {
			if !graph.ValidName(name) {
				return nil, fmt.Errorf("invalid output property %q", name)
			}
			prop := s.Properties[name]
			if prop == nil {
				prop = &jsonschema.Schema{}
			}
			if _, exists := props[name]; !exists {
				order = append(order, name)
			}
			props[name] = prop.CloneSchemas()
		}
	}
	if len(props) == 0 {
		return nil, nil
	}
	return &jsonschema.Schema{
		Type:          "object",
		Properties:    props,
		PropertyOrder: order,
	}, nil
}

// fieldEndpoint 返回字段所属出端点；不存在时 ok=false。
func fieldEndpoint(byEndpoint map[string]*jsonschema.Schema, field string) (endpoint string, ok bool) {
	for ep, schema := range byEndpoint {
		if schema != nil && schema.Properties != nil && schema.Properties[field] != nil {
			return ep, true
		}
	}
	return "", false
}

// OutputMap 将输出结构体按 json 标签转为 NodeOutput.Set 用的 map（保留 Go 类型，不经 JSON 往返）。
// 带 omitempty / omitzero 的零值字段不写入；v 不是结构体（或其指针）时 panic。
//
// OutputMap converts an output struct into NodeOutput.Set keyed by json tags.
func OutputMap(v any) map[string]any {
	val := reflect.ValueOf(v)
	for val.Kind() == reflect.Pointer {
		if val.IsNil() {
			return nil
		}
		val = val.Elem()
	}
	if val.Kind() != reflect.Struct {
		panic(fmt.Sprintf("graph/node: OutputMap expects struct, got %T", v))
	}
	t := val.Type()
	out := make(map[string]any, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || f.Anonymous {
			continue
		}
		name, omitempty := jsonFieldName(f)
		if name == "" || name == "-" {
			continue
		}
		fv := val.Field(i)
		if omitempty && fv.IsZero() {
			continue
		}
		out[name] = fv.Interface()
	}
	return out
}

// Outputs 返回节点实例按出口划分的载荷表（出口名 → 载荷 Schema；键 EndpointPayloadDefault 表示所有正常出口共用）。
// 结果与 Validate 做引用检查时使用的输出表一致；种类未注册时返回错误。
//
// Outputs returns the per-endpoint payload schemas of a node instance.
func (r *Registry) Outputs(n graph.Node) (map[string]*jsonschema.Schema, error) {
	spec, cfg, err := r.decodeNode(n)
	if err != nil {
		return nil, err
	}
	return nodeOutputs(spec, n.ID, cfg)
}

// nodeOutputs 返回节点实例按出口划分的载荷表（出口名 → 载荷 Schema）：
// 种类固定的 OutputsByEndpoint，合并已解码参数 cfg 推导的输出（DeclareOutputs）与开启 Error 出口时的 Error 载荷。
func nodeOutputs(spec NodeSpec, nodeID string, cfg any) (map[string]*jsonschema.Schema, error) {
	var dynamic map[string]*jsonschema.Schema
	if d, ok := cfg.(OutputDeclarer); ok {
		if more := d.DeclareOutputs(); more != nil {
			dynamic = map[string]*jsonschema.Schema{EndpointPayloadDefault: more}
		}
	}
	var errOut map[string]*jsonschema.Schema
	if slices.Contains(spec.Endpoints.Outputs, graph.EndpointError) {
		errOut = map[string]*jsonschema.Schema{
			graph.EndpointError: OutputSchema(graph.ErrorField),
		}
	}
	byEndpoint, err := mergeOutputsByEndpoint(spec.OutputsByEndpoint, dynamic, errOut)
	if err != nil {
		return nil, fmt.Errorf("graph: node %q outputs: %w", nodeID, err)
	}
	return byEndpoint, nil
}
