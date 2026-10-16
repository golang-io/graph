package jsonx

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// Decode 先经 CheckValid 严格校验，再解码到 target；动态值中的整数保持 int64。
// Decode validates raw with strict rules and decodes it, keeping integers as int64.
func Decode(raw []byte, target any) error {
	if err := CheckValid(raw); err != nil {
		return err
	}
	return decodeKeepingInts(raw, target)
}

// DecodeStored 解码引擎自己写入数据库的 JSON（Run 变量等）：同样保留 int64，但不经 Strict 规范化。
// DecodeStored decodes engine-written JSON, keeping int64 without Strict canonicalization.
func DecodeStored(raw []byte, target any) error {
	return decodeKeepingInts(raw, target)
}

func decodeKeepingInts(raw []byte, target any) error {
	switch p := target.(type) {
	case *any:
		if p != nil {
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) > 0 && json.Valid(trimmed) {
				v, _, err := parseValidAny(trimmed, 0)
				if err != nil {
					return err
				}
				*p = v
				return nil
			}
		}
	case *map[string]any:
		if p != nil {
			trimmed := bytes.TrimSpace(raw)
			if len(trimmed) > 0 && (trimmed[0] == '{' || bytes.Equal(trimmed, []byte("null"))) && json.Valid(trimmed) {
				v, _, err := parseValidAny(trimmed, 0)
				if err != nil {
					return err
				}
				if v == nil {
					*p = nil
				} else if m, ok := v.(map[string]any); ok {
					*p = m
				}
				return nil
			}
		}
	}
	if !containsDigit(raw) || !targetHasInterface(target) {
		return json.Unmarshal(raw, target)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(target); err != nil {
		return err
	}
	return NormalizeNumbers(target)
}

func containsDigit(raw []byte) bool {
	for _, b := range raw {
		if b >= '0' && b <= '9' {
			return true
		}
	}
	return false
}

var interfaceTypeCache sync.Map // reflect.Type -> bool

func targetHasInterface(target any) bool {
	if target == nil {
		return false
	}
	t := reflect.TypeOf(target)
	if v, ok := interfaceTypeCache.Load(t); ok {
		return v.(bool)
	}
	has := checkTypeHasInterface(t, nil)
	interfaceTypeCache.Store(t, has)
	return has
}

func checkTypeHasInterface(t reflect.Type, visited map[reflect.Type]bool) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Interface:
		return true
	case reflect.Slice, reflect.Array:
		return checkTypeHasInterface(t.Elem(), visited)
	case reflect.Map:
		return checkTypeHasInterface(t.Key(), visited) || checkTypeHasInterface(t.Elem(), visited)
	case reflect.Struct:
		if visited == nil {
			visited = map[reflect.Type]bool{}
		}
		if visited[t] {
			return false
		}
		visited[t] = true
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			if f.PkgPath != "" && !f.Anonymous {
				continue
			}
			if checkTypeHasInterface(f.Type, visited) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// parseValidAny 在已通过 json.Valid 的字节切片上直接解析出动态值（整数为 int64，小数为 float64），不分配流式 Decoder。
func parseValidAny(raw []byte, i int) (any, int, error) {
	i = skipWS(raw, i)
	switch raw[i] {
	case '{':
		i = skipWS(raw, i+1)
		out := map[string]any{}
		if raw[i] == '}' {
			return out, i + 1, nil
		}
		for {
			keyStart := i
			keyEnd, hasEsc := scanString(raw, keyStart)
			keyStr, err := decodeKeyString(raw[keyStart:keyEnd], hasEsc)
			if err != nil {
				return nil, 0, err
			}
			i = skipWS(raw, keyEnd)
			i++ // ':'
			val, next, err := parseValidAny(raw, i)
			if err != nil {
				return nil, 0, err
			}
			out[keyStr] = val
			i = skipWS(raw, next)
			if raw[i] == '}' {
				return out, i + 1, nil
			}
			i = skipWS(raw, i+1) // ','
		}
	case '[':
		i = skipWS(raw, i+1)
		if raw[i] == ']' {
			return []any{}, i + 1, nil
		}
		out := make([]any, 0, 4)
		for {
			val, next, err := parseValidAny(raw, i)
			if err != nil {
				return nil, 0, err
			}
			out = append(out, val)
			i = skipWS(raw, next)
			if raw[i] == ']' {
				return out, i + 1, nil
			}
			i++ // ','
		}
	case '"':
		end, hasEsc := scanString(raw, i)
		s, err := decodeKeyString(raw[i:end], hasEsc)
		return s, end, err
	case 't':
		return true, i + 4, nil
	case 'f':
		return false, i + 5, nil
	case 'n':
		return nil, i + 4, nil
	default:
		end := scanNumber(raw, i)
		num := raw[i:end]
		if !bytes.ContainsAny(num, ".eE") {
			n, err := strconv.ParseInt(string(num), 10, 64)
			if err != nil {
				return nil, 0, err
			}
			return n, end, nil
		}
		f, err := strconv.ParseFloat(string(num), 64)
		if err != nil {
			return nil, 0, err
		}
		return f, end, nil
	}
}

// NormalizeNumbers 把 target 内的 json.Number 转为 int64/float64（就地修改）。
// 含小数点或指数（json.Marshal 对 |x|<1e-6 或 >=1e21 的 float64 输出 1e-7 这类写法）的为 float64，其余为 int64。
// 供使用 UseNumber 解码（如开启 DisallowUnknownFields）的调用方在解码后统一规整数字。
func NormalizeNumbers(target any) error {
	switch p := target.(type) {
	case *any:
		if p == nil {
			return nil
		}
		norm, err := normalizeAny(*p)
		if err != nil {
			return err
		}
		*p = norm
		return nil
	case *map[string]any:
		if p == nil {
			return nil
		}
		return normalizeMap(*p)
	case *[]any:
		if p == nil {
			return nil
		}
		return normalizeSlice(*p)
	}
	return decodeNumbers(reflect.ValueOf(target))
}

func normalizeAny(val any) (any, error) {
	switch x := val.(type) {
	case nil, bool, string, int64, float64, int:
		return val, nil
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return x.Float64()
		}
		return x.Int64()
	case map[string]any:
		if err := normalizeMap(x); err != nil {
			return nil, err
		}
		return x, nil
	case []any:
		if err := normalizeSlice(x); err != nil {
			return nil, err
		}
		return x, nil
	default:
		return val, decodeNumbers(reflect.ValueOf(val))
	}
}

func normalizeMap(m map[string]any) error {
	for k, v := range m {
		norm, err := normalizeAny(v)
		if err != nil {
			return err
		}
		m[k] = norm
	}
	return nil
}

func normalizeSlice(s []any) error {
	for i, v := range s {
		norm, err := normalizeAny(v)
		if err != nil {
			return err
		}
		s[i] = norm
	}
	return nil
}

func decodeNumbers(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			return decodeNumbers(v.Elem())
		}
	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		if v.CanSet() {
			norm, err := normalizeAny(v.Interface())
			if err != nil {
				return err
			}
			if norm != nil {
				v.Set(reflect.ValueOf(norm))
			}
			return nil
		}
		return decodeNumbers(v.Elem())
	case reflect.Map:
		if v.CanInterface() {
			if m, ok := v.Interface().(map[string]any); ok {
				return normalizeMap(m)
			}
		}
		for _, key := range v.MapKeys() {
			item := reflect.New(v.Type().Elem()).Elem()
			item.Set(v.MapIndex(key))
			if err := decodeNumbers(item); err != nil {
				return err
			}
			v.SetMapIndex(key, item)
		}
	case reflect.Slice:
		if v.CanInterface() {
			if s, ok := v.Interface().([]any); ok {
				return normalizeSlice(s)
			}
		}
		for i := 0; i < v.Len(); i++ {
			if err := decodeNumbers(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				if err := decodeNumbers(v.Field(i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
