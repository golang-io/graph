package jsonx

import (
	"reflect"
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
)

// CloneSchema 在图与注册表边界隔离整棵 Schema，包括每层的列表、约束指针和动态 JSON 值。
// 不经 JSON 往返，保留 PropertyOrder、类型化扩展值以及 nil / 空容器的区别。
func CloneSchema(s *jsonschema.Schema) *jsonschema.Schema {
	if s == nil {
		return nil
	}
	return cloneSchemaValue(reflect.ValueOf(s)).Interface().(*jsonschema.Schema)
}

// Schema 的子树和动态字段统一复制，避免依赖新增字段时遗漏某一层的可变引用。
func cloneSchemaValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type().Elem())
		out.Elem().Set(cloneSchemaValue(v.Elem()))
		return out
	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		out.Set(cloneSchemaValue(v.Elem()))
		return out
	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			out.Index(i).Set(cloneSchemaValue(v.Index(i)))
		}
		return out
	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		for it := v.MapRange(); it.Next(); {
			out.SetMapIndex(it.Key(), cloneSchemaValue(it.Value()))
		}
		return out
	case reflect.Struct, reflect.Array:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		if v.Kind() == reflect.Array {
			for i := range v.Len() {
				out.Index(i).Set(cloneSchemaValue(v.Index(i)))
			}
		} else {
			for i := range v.NumField() {
				if out.Field(i).CanSet() {
					out.Field(i).Set(cloneSchemaValue(v.Field(i)))
				}
			}
		}
		return out
	default:
		return v
	}
}

// PropertyNames 返回 properties 的键：先按 PropertyOrder，其余按名字排序，保证顺序稳定。
func PropertyNames(s *jsonschema.Schema) []string {
	if s == nil || len(s.Properties) == 0 {
		return nil
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(s.Properties))
	for _, name := range s.PropertyOrder {
		if _, ok := s.Properties[name]; !ok || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	rest := make([]string, 0, len(s.Properties)-len(out))
	for name := range s.Properties {
		if !seen[name] {
			rest = append(rest, name)
		}
	}
	slices.Sort(rest)
	return append(out, rest...)
}

// RequiredNames 返回 required 的副本；nil Schema 返回 nil。
func RequiredNames(s *jsonschema.Schema) []string {
	if s == nil {
		return nil
	}
	return slices.Clone(s.Required)
}
