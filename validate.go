package graph

import (
	"bytes"
	"fmt"
	"maps"
	"slices"

	"github.com/golang-io/graph/internal/jsonx"
	"github.com/google/jsonschema-go/jsonschema"
)

// Validate 只校验图自身是否自洽：非空、id 合法唯一、边指向存在的节点、
// 运行输入声明与输入引用的格式。入口 / 出口规则与引用能否解析由 node.Registry.Validate 把关。
func (g Graph) Validate() error {
	if len(g.Nodes) == 0 {
		return fmt.Errorf("graph: no nodes")
	}
	if err := validateGraphInputSchema(g.InputSchema); err != nil {
		return err
	}
	types := make(map[string]string, len(g.Nodes))
	for _, n := range g.Nodes {
		if !ValidID(n.ID) || !ValidID(n.Kind) {
			return fmt.Errorf("graph: invalid node %q", n.ID)
		}
		if _, dup := types[n.ID]; dup {
			return fmt.Errorf("graph: duplicate node %q", n.ID)
		}
		types[n.ID] = n.Kind
		if err := validateInputRefs(n); err != nil {
			return err
		}
	}
	seen := make(map[string]bool, len(g.Edges))
	for _, e := range g.Edges {
		if !ValidID(e.ID) || seen[e.ID] {
			return fmt.Errorf("graph: invalid or duplicate edge %q", e.ID)
		}
		seen[e.ID] = true
		// From / To 必须是端点 ID（nodeID.name）：解析成功、本地端点名合法、所属节点存在。
		// From / To must each parse into an existing node and a valid local endpoint name.
		fromNode, fromName, okFrom := ParseEndpointID(e.From)
		toNode, toName, okTo := ParseEndpointID(e.To)
		if !okFrom || !okTo {
			return fmt.Errorf("graph: edge %q has invalid endpoint id", e.ID)
		}
		if !ValidID(fromName) || !ValidID(toName) {
			return fmt.Errorf("graph: edge %q has invalid endpoint name", e.ID)
		}
		if types[fromNode] == "" || types[toNode] == "" {
			return fmt.Errorf("graph: edge %q references unknown node", e.ID)
		}
	}
	return nil
}

// validateGraphInputSchema 校验运行输入 Schema：properties 名字合法；required 必须落在 properties 中。
func validateGraphInputSchema(schema *jsonschema.Schema) error {
	if schema == nil {
		return nil
	}
	if schema.Type != "" && schema.Type != "object" {
		return fmt.Errorf("graph: InputSchema.type must be object")
	}
	for name := range schema.Properties {
		if !ValidName(name) {
			return fmt.Errorf("graph: invalid input %q", name)
		}
	}
	for _, name := range schema.Required {
		if !ValidName(name) {
			return fmt.Errorf("graph: invalid required input %q", name)
		}
		if _, ok := schema.Properties[name]; !ok {
			return fmt.Errorf("graph: required input %q is not in properties", name)
		}
	}
	return nil
}

// validateInputRefs 校验节点输入引用的格式：输入名合法；From/Field 与 Value 二选一；字面量是合法 JSON。
// 来源能否解析由 node 包的数据绑定分析检查。
func validateInputRefs(n Node) error {
	for _, name := range slices.Sorted(maps.Keys(n.Inputs)) {
		if !ValidName(name) {
			return fmt.Errorf("graph: node %q input name %q invalid", n.ID, name)
		}
		if err := validateRefShape(n.Inputs[name]); err != nil {
			return fmt.Errorf("graph: node %q input %q: %w", n.ID, name, err)
		}
	}
	return nil
}

// validateRefShape 校验一条引用的格式（不检查来源是否存在）。
func validateRefShape(ref InputRef) error {
	hasRef := ref.From != ""
	hasValue := len(bytes.TrimSpace(ref.Value)) > 0
	switch {
	case hasRef == hasValue:
		return fmt.Errorf("exactly one of From or Value required")
	case hasRef:
		if ref.From != InputSource && !ValidID(ref.From) {
			return fmt.Errorf("invalid From %q", ref.From)
		}
		if !ValidName(ref.Field) {
			return fmt.Errorf("invalid Field %q", ref.Field)
		}
	default:
		if ref.Field != "" {
			return fmt.Errorf("Field requires From")
		}
		if _, err := jsonx.Strict(ref.Value); err != nil {
			return fmt.Errorf("Value: %w", err)
		}
	}
	return nil
}
