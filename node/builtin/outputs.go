package builtin

import (
	"fmt"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
	"github.com/google/jsonschema-go/jsonschema"
)

// CheckOutputs 在 require 为 true 时要求 outputs 非空。
func CheckOutputs(outputs []graph.OutKV, require bool) error {
	if require && len(outputs) == 0 {
		return fmt.Errorf("Outputs required")
	}
	return nil
}

// checkOutKVs 校验 OutKV 列表的 Key 均为合法字面量输出名且不重复，不分配 Schema。
func checkOutKVs(outputs []graph.OutKV, require bool) error {
	if err := CheckOutputs(outputs, require); err != nil {
		return err
	}
	for i, o := range outputs {
		key := strings.TrimSpace(o.Key)
		if !graph.ValidName(key) || key != o.Key {
			return fmt.Errorf("Outputs[%d]: Key %q must be a literal name (letters, digits, underscore)", i, o.Key)
		}
		for j := range i {
			if outputs[j].Key == key {
				return fmt.Errorf("Outputs[%d]: duplicate Key %q", i, key)
			}
		}
	}
	return nil
}

// hasOutKVKey 报告 outputs 中是否声明了合法的输出键 field。
func hasOutKVKey(outputs []graph.OutKV, field string) bool {
	if !graph.ValidName(field) {
		return false
	}
	for i := range outputs {
		if outputs[i].Key == field {
			return true
		}
	}
	return false
}

// outKVSchema 由 OutKV 列表生成输出 Schema（template / code 的 DeclareOutputs）。
// Key 必须是合法输出名（graph.ValidName，因此不能含 {{）且同一节点内不重复。
func outKVSchema(outputs []graph.OutKV, require bool) (*jsonschema.Schema, error) {
	if err := checkOutKVs(outputs, require); err != nil {
		return nil, err
	}
	names := make([]string, len(outputs))
	for i, o := range outputs {
		names[i] = o.Key
	}
	return node.OutputSchema(names...), nil
}

// outKVExpressions 交出 OutKV 的 Value 供标识符校验。条目按序求值，后面的条目可以使用前面条目的 Key，
// 因此每条的 Locals 为它之前的全部 Key。
func outKVExpressions(outputs []graph.OutKV, template bool) []node.Expression {
	exprs := make([]node.Expression, 0, len(outputs))
	var earlier []string
	for i, o := range outputs {
		exprs = append(exprs, node.Expression{
			Path: fmt.Sprintf("Outputs[%d].Value", i), Text: o.Value, Template: template, Locals: earlier,
		})
		earlier = append(earlier[:len(earlier):len(earlier)], o.Key)
	}
	return exprs
}

// ApplyTemplateOutputs 文案插值写出：每条 Value 可含 {{expr}}，按序写入 vars，返回本节点输出。
// vars 为本节点输入的副本；后面的条目可以引用前面条目的 Key。
func ApplyTemplateOutputs(outputs []graph.OutKV, vars map[string]any) (map[string]any, error) {
	return mergeOutputs(outputs, vars, func(_, value string) (any, error) {
		return graph.RenderTemplate(value, vars), nil
	})
}

// ApplyCodeOutputs 表达式写出：Value 走 EvalExpr，按序写入 vars，返回本节点输出。
func ApplyCodeOutputs(outputs []graph.OutKV, vars map[string]any) (map[string]any, error) {
	return mergeOutputs(outputs, vars, func(key, value string) (any, error) {
		val, err := graph.EvalExpr(value, vars)
		if err != nil {
			return nil, fmt.Errorf("output %q: %w", key, err)
		}
		return val, nil
	})
}

// mergeOutputs 按序求值并写回 vars（供后续条目引用），Key 为字面量输出名。
func mergeOutputs(outputs []graph.OutKV, vars map[string]any, eval func(key, value string) (any, error)) (map[string]any, error) {
	if err := CheckOutputs(outputs, true); err != nil {
		return nil, err
	}
	out := map[string]any{}
	for i, o := range outputs {
		key := strings.TrimSpace(o.Key)
		if key == "" {
			return nil, fmt.Errorf("Outputs[%d]: output key empty", i)
		}
		val, err := eval(key, o.Value)
		if err != nil {
			return nil, err
		}
		out[key] = val
		vars[key] = val
	}
	return out, nil
}
