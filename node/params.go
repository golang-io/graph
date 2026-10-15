package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/google/jsonschema-go/jsonschema"
)

// WithParams 为 Spec 挂上 DefaultInputs、InputSchema 与编译期参数钩子。
// InputSchema：jsonschema.For 推导类型，再按 title/enum/bind 标签裁剪（见 input_schema.go）。
// 面板与绑定槽由参数结构体标签生成，无需再写 InputMeta。
func WithParams[T any](spec NodeSpec, defaults T) NodeSpec {
	raw, err := json.Marshal(defaults)
	if err != nil {
		panic(fmt.Sprintf("graph/node: marshal defaults: %v", err))
	}
	// IgnoreInvalidTypes：bind 槽常用 any，For 会跳过，随后由 applyStructInputSchema 补上。
	// graph.Graph（map 的 Body）内含递归的 jsonschema.Schema，无法反射推导，按普通对象声明；结构由 Validate 递归校验。
	schema, err := jsonschema.For[T](&jsonschema.ForOptions{IgnoreInvalidTypes: true, TypeSchemas: map[reflect.Type]*jsonschema.Schema{
		reflect.TypeFor[graph.Graph](): {Type: "object", Description: "内嵌子图（graph.Graph）"},
	}})
	if err != nil {
		panic(fmt.Sprintf("graph/node: input schema: %v", err))
	}
	spec.DefaultInputs = objectToInputRefs(raw)
	spec.InputSchema = applyStructInputSchema[T](schema)
	fields := structParamFields[T]()
	spec.paramFields = fields
	spec.paramsDecode = func(raw json.RawMessage) (any, error) {
		var cfg T
		if err := decodeStrictParams(raw, &cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	spec.paramsMerge = func(base any, overrides map[string]any) (any, error) {
		var cfg T
		if base != nil {
			cfg, _ = base.(T)
		}
		if err := applyParamOverrides(&cfg, fields, overrides); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	return spec
}

// structParamFields 提取参数结构体 T 的导出 JSON 字段名及其在 T 中的字段下标。
func structParamFields[T any]() map[string]int {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct || t.NumField() == 0 {
		return nil
	}
	var out map[string]int
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" || f.Anonymous {
			continue
		}
		name, _ := jsonFieldName(f)
		if name == "" || name == "-" {
			continue
		}
		if out == nil {
			out = make(map[string]int, t.NumField())
		}
		out[name] = i
	}
	return out
}

// applyParamOverrides 将运行期覆盖的参数字段（动态绑定或调试注入）写入静态参数副本 cfg：
//   - 先克隆非空的顶层 slice / map 字段，确保未覆盖的容器字段不与编译缓存中的 base 共享底层引用；
//   - 常见标量与 any 字段直接按反射赋值，零 JSON 编解码开销；
//   - 复合结构字段（或类型不匹配需要报错的字段）先将原字段清零，再仅对这些覆盖字段做一次 DecodeStored。
func applyParamOverrides[T any](cfg *T, fields map[string]int, overrides map[string]any) error {
	rv := reflect.ValueOf(cfg).Elem()
	cloneCompositeFields(rv)
	var slow map[string]any
	for name, val := range overrides {
		idx, ok := fields[name]
		if !ok {
			continue
		}
		fv := rv.Field(idx)
		if setFastParamField(fv, val) {
			continue
		}
		fv.SetZero()
		if slow == nil {
			slow = make(map[string]any, len(overrides))
		}
		slow[name] = val
	}
	if len(slow) == 0 {
		return nil
	}
	raw, err := json.Marshal(slow)
	if err != nil {
		return fmt.Errorf("node: invalid node params: %w", err)
	}
	if err := jsonx.DecodeStored(raw, cfg); err != nil {
		return fmt.Errorf("node: invalid node params: %w", err)
	}
	return nil
}

// cloneCompositeFields 克隆结构体中非空的顶层 slice 与 map 字段，
// 防止运行期合并参数后修改容器字段时污染编译期缓存的静态参数 base。
//
// cloneCompositeFields clones non-empty top-level slice and map fields on a struct copy
// so runtime parameter overrides never share mutable containers with the cached base struct.
func cloneCompositeFields(rv reflect.Value) {
	for i := 0; i < rv.NumField(); i++ {
		fv := rv.Field(i)
		if !fv.CanSet() {
			continue
		}
		switch fv.Kind() {
		case reflect.Slice:
			if !fv.IsNil() {
				cp := reflect.MakeSlice(fv.Type(), fv.Len(), fv.Len())
				reflect.Copy(cp, fv)
				fv.Set(cp)
			}
		case reflect.Map:
			if !fv.IsNil() {
				cp := reflect.MakeMapWithSize(fv.Type(), fv.Len())
				iter := fv.MapRange()
				for iter.Next() {
					cp.SetMapIndex(iter.Key(), iter.Value())
				}
				fv.Set(cp)
			}
		}
	}
}

// setFastParamField 对 any 及常见基础标量类型直接赋值，成功返回 true；
// 复合类型或类型不匹配返回 false，交由 DecodeStored 处理并报告类型错误。
func setFastParamField(fv reflect.Value, val any) bool {
	switch fv.Type() {
	case reflect.TypeFor[any]():
		if val == nil {
			fv.SetZero()
		} else {
			fv.Set(reflect.ValueOf(val))
		}
		return true
	case reflect.TypeFor[string]():
		if s, ok := val.(string); ok {
			fv.SetString(s)
			return true
		}
	case reflect.TypeFor[bool]():
		if b, ok := val.(bool); ok {
			fv.SetBool(b)
			return true
		}
	case reflect.TypeFor[int](), reflect.TypeFor[int64]():
		switch n := val.(type) {
		case int64:
			fv.SetInt(n)
			return true
		case int:
			fv.SetInt(int64(n))
			return true
		}
	case reflect.TypeFor[float64]():
		switch n := val.(type) {
		case float64:
			fv.SetFloat(n)
			return true
		case int64:
			fv.SetFloat(float64(n))
			return true
		case int:
			fv.SetFloat(float64(n))
			return true
		}
	}
	return false
}

// objectToInputRefs 把 JSON 对象的每个键变成字面量 InputRef（供 DefaultInputs / 测试构造）。
// 跳过 null / ""：bind 槽与未填必填字段不进新建节点默认值。
func objectToInputRefs(raw json.RawMessage) map[string]graph.InputRef {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || string(raw) == "null" || string(raw) == "{}" {
		return nil
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(raw, &obj); err != nil {
		panic(fmt.Sprintf("graph/node: defaults must be object: %v", err))
	}
	if len(obj) == 0 {
		return nil
	}
	out := make(map[string]graph.InputRef, len(obj))
	for k, v := range obj {
		v = bytes.TrimSpace(v)
		if len(v) == 0 || string(v) == "null" || string(v) == `""` {
			continue
		}
		out[k] = graph.InputRef{Value: append(json.RawMessage(nil), v...)}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// InputsLiteralObject 从实例 Inputs 抽出字面量键，拼成 JSON 对象。
// 供端点展开、编译期 Validate / DeclareOutputs / Expressions 等使用；绑定引用的键不出现在结果中。
func InputsLiteralObject(inputs map[string]graph.InputRef) json.RawMessage {
	if len(inputs) == 0 {
		return json.RawMessage(`{}`)
	}
	obj := make(map[string]json.RawMessage, len(inputs))
	for k, ref := range inputs {
		if strings.TrimSpace(ref.From) != "" {
			continue
		}
		if len(bytes.TrimSpace(ref.Value)) == 0 {
			continue
		}
		obj[k] = ref.Value
	}
	raw, err := json.Marshal(obj)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return raw
}

// decodeStrictParams 经 jsonx.Strict 校验后解码参数 JSON，any 字段中的整数保持 int64。
// 不拒绝未知字段：实例 Inputs 可含自定义键（供模板/表达式），字面量对象会一并带上。
func decodeStrictParams(raw json.RawMessage, target any) error {
	if err := jsonx.Decode(normalizeParamsRaw(raw), target); err != nil {
		return fmt.Errorf("invalid params: %w", err)
	}
	return nil
}

func normalizeParamsRaw(raw json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(raw)) == 0 {
		return json.RawMessage(`{}`)
	}
	return raw
}

// decodeParams 将节点实例 Inputs 中的字面量参数单次解码为 WithParams[T] 注册的参数结构体实例。
// 调用方拿到返回的 cfg any 后直接对其做接口断言（InputValidator、OutputDeclarer 等），不再重复解码。
//
// decodeParams decodes the literal inputs of a node instance once into its parameter struct.
func (s NodeSpec) decodeParams(inputs map[string]graph.InputRef) (any, error) {
	raw := normalizeParamsRaw(InputsLiteralObject(inputs))
	if s.paramsDecode == nil {
		return jsonx.Strict(raw)
	}
	return s.paramsDecode(raw)
}

// paramError 包装运行期合并动态绑定或调试注入参数时的类型错误，延迟到节点调用 Params[T] 时作为节点执行错误返回。
type paramError struct{ err error }

// ResolveParams 生成节点运行期的参数结构体（NodeInput.Params / ControlInput.Params）：
//   - 当节点的所有参数结构体字段均来自定义期字面量、且未被调试注入（injected）覆盖时，直接返回编译期冻结的 base（零分配）；
//   - 当存在动态绑定（n.Inputs[name].From != ""）或调试注入（injected[name]）命中参数结构体字段时，
//     在 base 的值拷贝上按字段合入运行期 vars[name]；类型不匹配时返回延迟错误，由 Params[T] 在节点执行时返回。
//
// ResolveParams returns base when all parameter fields are static literals, or merges runtime bound/injected overrides onto a copy.
func (r *Registry) ResolveParams(n graph.Node, base any, vars, injected map[string]any) any {
	spec, ok := r.spec(n.Kind)
	if !ok || spec.paramsMerge == nil || len(spec.paramFields) == 0 {
		return base
	}
	var overrides map[string]any
	for name := range spec.paramFields {
		ref, hasInput := n.Inputs[name]
		_, hasInj := injected[name]
		if (hasInput && ref.From != "") || hasInj {
			if overrides == nil {
				overrides = make(map[string]any, len(spec.paramFields))
			}
			overrides[name] = vars[name]
		}
	}
	if len(overrides) == 0 {
		return base
	}
	merged, err := spec.paramsMerge(base, overrides)
	if err != nil {
		return paramError{err: err}
	}
	return merged
}

// Params 从 NodeInput.Params 或 ControlInput.Params 中取出参数结构体 T。
// 直接调用 Execute / Control 的测试未传入 Params（为 nil）时返回 T 的零值；运行期覆盖类型不匹配或类型断言失败时返回错误。
//
// Params extracts the parameter struct T from NodeInput.Params or ControlInput.Params.
func Params[T any](params any) (T, error) {
	if params == nil {
		var zero T
		return zero, nil
	}
	if pe, ok := params.(paramError); ok {
		var zero T
		return zero, pe.err
	}
	cfg, ok := params.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("node: invalid node params: got %T", params)
	}
	return cfg, nil
}
