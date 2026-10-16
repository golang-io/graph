package node

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/google/jsonschema-go/jsonschema"
)

var outKVSliceType = reflect.TypeFor[[]graph.OutKV]()

// WithParams 为 Spec 挂上 DefaultInputs、InputSchema 与参数解码/合并钩子。
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
	fields, paramOnly := structParamFields[T]()
	spec.paramFields = fields
	spec.paramOnlyLiterals = paramOnly
	customUnmarshal := reflect.PointerTo(reflect.TypeFor[T]()).Implements(reflect.TypeFor[json.Unmarshaler]())
	spec.paramsDecode = func(inputs map[string]graph.InputRef) (any, error) {
		var cfg T
		if customUnmarshal {
			rawObj := normalizeParamsRaw(InputsLiteralObject(inputs))
			if err := decodeStrictParams(rawObj, &cfg); err != nil {
				return nil, err
			}
			return cfg, nil
		}
		if len(fields) == 0 || len(inputs) == 0 {
			return cfg, nil
		}
		if err := decodeMergedParams(inputs, nil, fields, &cfg); err != nil {
			return nil, err
		}
		return cfg, nil
	}
	spec.paramsMerge = func(inputs map[string]graph.InputRef, overrides map[string]any) (any, error) {
		var cfg T
		if customUnmarshal {
			if err := decodeMergedCustomParams(inputs, overrides, fields, &cfg); err != nil {
				return nil, err
			}
		} else if err := decodeMergedParams(inputs, overrides, fields, &cfg); err != nil {
			return nil, err
		}
		if iv, ok := any(cfg).(InputValidator); ok {
			if err := iv.Validate(); err != nil {
				return nil, fmt.Errorf("node: invalid node params: %w", err)
			}
		}
		return cfg, nil
	}
	return spec
}

// structParamFields 提取参数结构体 T 的导出 JSON 字段名集合，以及仅作静态参数、无需重复解码进运行期 Vars 的复合字段集合。
func structParamFields[T any]() (map[string]int, map[string]bool) {
	t := reflect.TypeFor[T]()
	if t.Kind() != reflect.Struct || t.NumField() == 0 {
		return nil, nil
	}
	var out map[string]int
	var paramOnly map[string]bool
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
		if _, hasBind := f.Tag.Lookup("bind"); !hasBind {
			switch f.Type.Kind() {
			case reflect.Slice, reflect.Array, reflect.Struct, reflect.Pointer, reflect.Map:
				if paramOnly == nil {
					paramOnly = make(map[string]bool, t.NumField())
				}
				paramOnly[name] = true
			}
		}
	}
	return out, paramOnly
}

// IsParamOnlyLiteral 报告节点种类 kind 的输入字段 name 是否为仅通过 Params[T] 读取的静态复合配置字段。
func (r *Registry) IsParamOnlyLiteral(kind, name string) bool {
	spec, ok := r.spec(kind)
	if !ok || spec.paramOnlyLiterals == nil {
		return false
	}
	return spec.paramOnlyLiterals[name]
}

// decodeMergedParams 按参数结构体导出字段直接从 inputs 解码未覆盖的字面量字段，并将运行期 overrides 直接写入对应字段，
// 省去将全部字面量拼装为 map[string]json.RawMessage 再二次 JSON 序列化的开销。
func decodeMergedParams[T any](inputs map[string]graph.InputRef, overrides map[string]any, fields map[string]int, cfg *T) error {
	if len(fields) == 0 {
		return nil
	}
	rv := reflect.ValueOf(cfg).Elem()
	if rv.Kind() != reflect.Struct {
		return decodeStrictParams(InputsLiteralObject(inputs), cfg)
	}
	for name, idx := range fields {
		if overrides != nil {
			if val, over := overrides[name]; over {
				if err := assignParamField(rv.Field(idx), val); err != nil {
					return fmt.Errorf("node: invalid node params: %w", err)
				}
				continue
			}
		}
		ref, ok := inputs[name]
		if !ok || strings.TrimSpace(ref.From) != "" {
			continue
		}
		raw := bytes.TrimSpace(ref.Value)
		if len(raw) == 0 {
			continue
		}
		if err := decodeParamField(rv.Field(idx), raw); err != nil {
			return fmt.Errorf("invalid params: %w", err)
		}
	}
	return nil
}

// decodeMergedCustomParams 处理实现了自定义 json.Unmarshaler 的参数结构体。
func decodeMergedCustomParams[T any](inputs map[string]graph.InputRef, overrides map[string]any, fields map[string]int, cfg *T) error {
	var literals map[string]json.RawMessage
	for k, ref := range inputs {
		if _, over := overrides[k]; over {
			continue
		}
		if strings.TrimSpace(ref.From) == "" && len(bytes.TrimSpace(ref.Value)) > 0 {
			if literals == nil {
				literals = make(map[string]json.RawMessage, len(inputs))
			}
			literals[k] = ref.Value
		}
	}
	if len(literals) > 0 {
		raw, err := json.Marshal(literals)
		if err != nil {
			return fmt.Errorf("node: invalid node params: %w", err)
		}
		if err := decodeStrictParams(raw, cfg); err != nil {
			return fmt.Errorf("node: invalid node params: %w", err)
		}
	}
	rv := reflect.ValueOf(cfg).Elem()
	if rv.Kind() == reflect.Struct {
		for name, val := range overrides {
			idx, ok := fields[name]
			if !ok {
				continue
			}
			if err := assignParamField(rv.Field(idx), val); err != nil {
				return fmt.Errorf("node: invalid node params: %w", err)
			}
		}
	}
	return nil
}

// decodeParamField 校验并解码单个参数结构体字段的 JSON 字面量 raw：
// 常见标量与 []graph.OutKV 直接写入，其余复杂类型经 jsonx.DecodeStored 解码（整数保持 int64）。
func decodeParamField(fv reflect.Value, raw []byte) error {
	if err := jsonx.CheckValid(raw); err != nil {
		return err
	}
	if bytes.Equal(raw, []byte("null")) {
		fv.SetZero()
		return nil
	}
	switch fv.Kind() {
	case reflect.String:
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' && bytes.IndexByte(raw, '\\') < 0 {
			fv.SetString(string(raw[1 : len(raw)-1]))
			return nil
		}
	case reflect.Bool:
		if bytes.Equal(raw, []byte("true")) {
			fv.SetBool(true)
			return nil
		}
		if bytes.Equal(raw, []byte("false")) {
			fv.SetBool(false)
			return nil
		}
	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Int16, reflect.Int8:
		if n, err := strconv.ParseInt(string(raw), 10, 64); err == nil && !fv.OverflowInt(n) {
			fv.SetInt(n)
			return nil
		}
	case reflect.Slice:
		if fv.Type() == outKVSliceType {
			if out, ok := parseSimpleOutKVSlice(raw); ok {
				fv.Set(reflect.ValueOf(out))
				return nil
			}
		}
	}
	return jsonx.DecodeStored(raw, fv.Addr().Interface())
}

// parseSimpleOutKVSlice 在已通过 CheckValid 的字节切片上直接解析无转义的 []graph.OutKV，
// 遇到含转义字符、额外键或非数组形态时返回 false，交由标准 JSON 解码处理。
func parseSimpleOutKVSlice(raw []byte) ([]graph.OutKV, bool) {
	if len(raw) < 2 || raw[0] != '[' {
		return nil, false
	}
	i := skipSpace(raw, 1)
	if i < len(raw) && raw[i] == ']' {
		if skipSpace(raw, i+1) == len(raw) {
			return []graph.OutKV{}, true
		}
		return nil, false
	}
	var stack [4]graph.OutKV
	var out []graph.OutKV
	count := 0
	for i < len(raw) {
		if raw[i] != '{' {
			return nil, false
		}
		i = skipSpace(raw, i+1)
		var item graph.OutKV
		if i < len(raw) && raw[i] != '}' {
			for {
				k, next, ok := readUnescapedString(raw, i)
				if !ok {
					return nil, false
				}
				i = skipSpace(raw, next)
				if i >= len(raw) || raw[i] != ':' {
					return nil, false
				}
				i = skipSpace(raw, i+1)
				v, next, ok := readUnescapedString(raw, i)
				if !ok {
					return nil, false
				}
				switch k {
				case "Key":
					item.Key = v
				case "Value":
					item.Value = v
				default:
					return nil, false
				}
				i = skipSpace(raw, next)
				if i < len(raw) && raw[i] == '}' {
					break
				}
				if i >= len(raw) || raw[i] != ',' {
					return nil, false
				}
				i = skipSpace(raw, i+1)
			}
		}
		if i >= len(raw) || raw[i] != '}' {
			return nil, false
		}
		if out == nil {
			if count < len(stack) {
				stack[count] = item
			} else {
				out = make([]graph.OutKV, count, 8)
				copy(out, stack[:count])
				out = append(out, item)
			}
		} else {
			out = append(out, item)
		}
		count++
		i = skipSpace(raw, i+1)
		if i < len(raw) && raw[i] == ']' {
			if skipSpace(raw, i+1) != len(raw) {
				return nil, false
			}
			if out == nil {
				return slices.Clone(stack[:count]), true
			}
			return out, true
		}
		if i >= len(raw) || raw[i] != ',' {
			return nil, false
		}
		i = skipSpace(raw, i+1)
	}
	return nil, false
}

func skipSpace(raw []byte, i int) int {
	for i < len(raw) {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func readUnescapedString(raw []byte, i int) (string, int, bool) {
	if i >= len(raw) || raw[i] != '"' {
		return "", 0, false
	}
	start := i + 1
	for j := start; j < len(raw); j++ {
		if raw[j] == '\\' {
			return "", 0, false
		}
		if raw[j] == '"' {
			return string(raw[start:j]), j + 1, true
		}
	}
	return "", 0, false
}

// assignParamField 将运行期值 val 赋给结构体字段 fv：基础类型与 any 直接反射写入，复合类型单字段 JSON 解码。
func assignParamField(fv reflect.Value, val any) error {
	if val == nil {
		fv.SetZero()
		return nil
	}
	switch fv.Kind() {
	case reflect.Interface:
		if fv.NumMethod() == 0 {
			if n, ok := val.(int); ok {
				val = int64(n)
			}
			fv.Set(reflect.ValueOf(val))
			return nil
		}
	case reflect.String:
		if s, ok := val.(string); ok {
			fv.SetString(s)
			return nil
		}
	case reflect.Bool:
		if b, ok := val.(bool); ok {
			fv.SetBool(b)
			return nil
		}
	case reflect.Int, reflect.Int64, reflect.Int32, reflect.Int16, reflect.Int8:
		switch n := val.(type) {
		case int64:
			if !fv.OverflowInt(n) {
				fv.SetInt(n)
				return nil
			}
		case int:
			if !fv.OverflowInt(int64(n)) {
				fv.SetInt(int64(n))
				return nil
			}
		}
	case reflect.Float64, reflect.Float32:
		switch n := val.(type) {
		case float64:
			if !fv.OverflowFloat(n) {
				fv.SetFloat(n)
				return nil
			}
		case int64:
			fv.SetFloat(float64(n))
			return nil
		case int:
			fv.SetFloat(float64(n))
			return nil
		}
	}
	fv.SetZero()
	raw, err := json.Marshal(val)
	if err != nil {
		return err
	}
	return jsonx.DecodeStored(raw, fv.Addr().Interface())
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
// 供端点展开、定义期 Validate / DeclareOutputs / Expressions 等使用；绑定引用的键不出现在结果中。
func InputsLiteralObject(inputs map[string]graph.InputRef) json.RawMessage {
	if len(inputs) == 0 {
		return json.RawMessage(`{}`)
	}
	var obj map[string]json.RawMessage
	for k, ref := range inputs {
		if strings.TrimSpace(ref.From) != "" {
			continue
		}
		if len(bytes.TrimSpace(ref.Value)) == 0 {
			continue
		}
		if obj == nil {
			obj = make(map[string]json.RawMessage, len(inputs))
		}
		obj[k] = ref.Value
	}
	if len(obj) == 0 {
		return json.RawMessage(`{}`)
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
	if s.paramsDecode == nil {
		return jsonx.Strict(normalizeParamsRaw(InputsLiteralObject(inputs)))
	}
	return s.paramsDecode(inputs)
}

// paramError 包装运行期合并动态绑定或调试注入参数时的类型错误，延迟到节点调用 Params[T] 时作为节点执行错误返回。
type paramError struct{ err error }

// ResolveParams 生成节点运行期的参数结构体（NodeInput.Params / ControlInput.Params）：
//   - 当节点的所有参数结构体字段均来自定义期字面量、且未被调试注入（injected）覆盖时，直接解码字面量参数；
//   - 当存在动态绑定（n.Inputs[name].From != ""）或调试注入（injected[name]）命中参数结构体字段时，
//     将字面量与运行期 vars[name] 单次合并解码为新结构体实例；类型不匹配时返回延迟错误，由 Params[T] 在节点执行时返回。
func (r *Registry) ResolveParams(n graph.Node, vars, injected map[string]any) any {
	spec, ok := r.spec(n.Kind)
	if !ok {
		return paramError{err: fmt.Errorf("graph: node %q kind %q is not registered", n.ID, n.Kind)}
	}
	var overrides map[string]any
	if len(n.Inputs) > 0 || len(injected) > 0 {
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
	}
	if len(overrides) == 0 || spec.paramsMerge == nil {
		cfg, err := spec.decodeParams(n.Inputs)
		if err != nil {
			return paramError{err: err}
		}
		return cfg
	}
	merged, err := spec.paramsMerge(n.Inputs, overrides)
	if err != nil {
		return paramError{err: err}
	}
	return merged
}

// CheckParams 检查 ResolveParams 的返回值是否包含参数类型或校验错误。
// CheckParams reports any parameter type or validation error produced by ResolveParams.
func CheckParams(params any) error {
	if pe, ok := params.(paramError); ok {
		return pe.err
	}
	return nil
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
