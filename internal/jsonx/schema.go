package jsonx

import (
	"slices"

	"github.com/google/jsonschema-go/jsonschema"
)

// CloneSchema 深拷贝 Schema（含 Required / PropertyOrder），供编译缓存与注册表副本使用。
// CloneSchemas 只深拷贝嵌套 Schema，Required / PropertyOrder 切片仍共享，这里再各拷一份。
func CloneSchema(s *jsonschema.Schema) *jsonschema.Schema {
	if s == nil {
		return nil
	}
	out := s.CloneSchemas()
	out.Required = slices.Clone(out.Required)
	out.PropertyOrder = slices.Clone(out.PropertyOrder)
	return out
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
