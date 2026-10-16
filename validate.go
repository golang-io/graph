package graph

import (
	"bytes"
	"encoding/json"
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

// allowedRunInputTypes 是运行输入 Schema 允许的 JSON Schema type 取值。
var allowedRunInputTypes = map[string]bool{
	"string": true, "integer": true, "number": true, "boolean": true,
	"array": true, "object": true, "null": true,
}

// validateGraphInputSchema 校验运行输入 Schema：
//   - 根必须是 type=object，且 additionalProperties=false；
//   - properties 名字合法，每个属性 Schema 必须带 Type/Types，禁止空 {} / 布尔 Schema；
//   - array 必须有 items；object 对其 properties 递归同一类型规则；
//   - required 必须落在 properties 中。
func validateGraphInputSchema(schema *jsonschema.Schema) error {
	if schema == nil {
		return nil
	}
	return validateRunInputObjectSchema(schema, "InputSchema")
}

// validateRunInputObjectSchema 校验一层 object Schema（图级根或嵌套 object 属性）。
func validateRunInputObjectSchema(schema *jsonschema.Schema, path string) error {
	if schema == nil {
		return fmt.Errorf("graph: %s is nil", path)
	}
	// 布尔 Schema（JSON true/false）在 jsonschema-go 里 Extra 或特殊编码；属性侧禁止，见 validateRunInputPropSchema。
	if schema.Type != "object" {
		return fmt.Errorf("graph: %s.type must be object", path)
	}
	if !isSchemaFalse(schema.AdditionalProperties) {
		return fmt.Errorf("graph: %s.additionalProperties must be false", path)
	}
	for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
		if !ValidName(name) {
			return fmt.Errorf("graph: invalid input %q", name)
		}
		propPath := path + ".properties." + name
		if err := validateRunInputPropSchema(schema.Properties[name], propPath); err != nil {
			return err
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

// isSchemaFalse 判断是否为规范的 JSON Schema false（禁止一切实例）。
// jsonschema-go 仅对无附加约束的 {"not":{}} 编码为字面量 "false"，含 enum/const 等其他字段的 Not 不会误判。
func isSchemaFalse(s *jsonschema.Schema) bool {
	if s == nil {
		return false
	}
	raw, err := json.Marshal(s)
	return err == nil && bytes.Equal(raw, []byte("false"))
}

// validateRunInputPropSchema 递归校验属性 / items 子 Schema：必须声明 type，禁止空对象与布尔 Schema。
func validateRunInputPropSchema(schema *jsonschema.Schema, path string) error {
	if schema == nil {
		return fmt.Errorf("graph: %s is nil", path)
	}
	// 空 Schema（{}）与「仅 description」均视为缺少 type。
	types, err := runInputSchemaTypes(schema)
	if err != nil {
		return fmt.Errorf("graph: %s: %w", path, err)
	}
	for _, typ := range types {
		if !allowedRunInputTypes[typ] {
			return fmt.Errorf("graph: %s: unsupported type %q", path, typ)
		}
	}
	// 仅当唯一类型为 array / object 时强制嵌套结构；多类型并集（如 AnyValueSchema）不要求 items/properties。
	onlyArray := len(types) == 1 && types[0] == "array"
	onlyObject := len(types) == 1 && types[0] == "object"
	if onlyArray {
		if schema.Items == nil && len(schema.ItemsArray) == 0 {
			return fmt.Errorf("graph: %s: array type requires items", path)
		}
		if schema.Items != nil {
			if err := validateRunInputPropSchema(schema.Items, path+".items"); err != nil {
				return err
			}
		}
		for i, item := range schema.ItemsArray {
			if err := validateRunInputPropSchema(item, fmt.Sprintf("%s.items[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	if onlyObject {
		// 嵌套 object：若声明了 properties，则按 object Schema 规则校验（含 additionalProperties=false）。
		if schema.Properties != nil || len(schema.Required) > 0 {
			if err := validateRunInputObjectSchema(schema, path); err != nil {
				return err
			}
		} else if schema.AdditionalProperties != nil && !isSchemaFalse(schema.AdditionalProperties) {
			// 无 properties 的开放 object：additionalProperties 子 Schema 仍须有 type。
			if err := validateRunInputPropSchema(schema.AdditionalProperties, path+".additionalProperties"); err != nil {
				return err
			}
		}
	}
	return nil
}

// runInputSchemaTypes 取出 Schema 的 type / types；二者皆空则报错（拒绝 {}）。
func runInputSchemaTypes(schema *jsonschema.Schema) ([]string, error) {
	if schema.Type != "" && len(schema.Types) > 0 {
		return nil, fmt.Errorf("both Type and Types set")
	}
	if schema.Type != "" {
		return []string{schema.Type}, nil
	}
	if len(schema.Types) > 0 {
		return append([]string(nil), schema.Types...), nil
	}
	return nil, fmt.Errorf("missing type")
}

// validateInputRefs 校验节点输入引用的格式：输入名合法；From/Field 与 Value 二选一；字面量是合法 JSON。
// 来源能否解析由 node 包的数据绑定分析检查。
// 正常路径直接遍历 map 零切片分配；发现错误时按输入名升序返回首个错误以保证错误消息确定。
func validateInputRefs(n Node) error {
	var badName string
	for name, ref := range n.Inputs {
		if !ValidName(name) || validateRefShape(ref) != nil {
			if badName == "" || name < badName {
				badName = name
			}
		}
	}
	if badName == "" {
		return nil
	}
	if !ValidName(badName) {
		return fmt.Errorf("graph: node %q input name %q invalid", n.ID, badName)
	}
	return fmt.Errorf("graph: node %q input %q: %w", n.ID, badName, validateRefShape(n.Inputs[badName]))
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
		if err := jsonx.CheckValid(ref.Value); err != nil {
			return fmt.Errorf("Value: %w", err)
		}
	}
	return nil
}
