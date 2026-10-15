package node

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/google/jsonschema-go/jsonschema"
)

// 数据绑定协议：节点输出按节点隔离（Run.Outputs[节点ID][输出名]），输入在 Node.Inputs 中显式引用来源。
// 本文件负责定义期绑定校验，由 Registry.Validate 在拓扑校验通过后调用：
//
//  1. 来源存在：From 指向的节点存在且 Field 在其某出口载荷中，或 From 为 $input 且 Field 在 Graph.InputSchema.properties 中；
//  2. 出口可达：当前节点能从来源字段所属出口沿控制边到达（图无环，可达即一定先执行）；
//  3. 表达式标识符只能是本节点输入名与节点局部名；
//  4. 必填输入槽必须出现；
//  5. 输出名合法且不跨出口重复。
//
// map 的循环体是独立子图，由 Validate 递归校验；循环体内外的节点互相不可见。

// checkReferences 执行上面 5 条规则；复用已解码的节点参数 cfgs、已算出的实例端点 eps 与已建立的节点/出边索引，
// 并一并生成各节点的运行期 OutputRule。节点按 ID 升序检查，保证同一张图的错误消息稳定。
func (r *Registry) checkReferences(g graph.Graph, byID map[string]graph.Node, out map[string][]graph.Edge, cfgs map[string]any, eps map[string]EndpointSpec) (map[string]OutputRule, error) {
	outputs := make(map[string]map[string]*jsonschema.Schema, len(g.Nodes))
	rules := make(map[string]OutputRule, len(g.Nodes))
	for _, n := range g.Nodes {
		spec, _ := r.spec(n.Kind)
		cfg := cfgs[n.ID]
		outs, err := nodeOutputs(spec, n.ID, cfg)
		if err != nil {
			return nil, err
		}
		outputs[n.ID] = outs
		rules[n.ID] = newOutputRule(n.ID, eps[n.ID], outs)
	}
	runInputs := map[string]bool{}
	for _, name := range jsonx.PropertyNames(g.InputSchema) {
		runInputs[name] = true
	}
	reach := &reachIndex{out: out, cache: map[string]map[string]bool{}}
	check := func(n graph.Node, what string, ref graph.InputRef) error {
		if ref.From == "" {
			return nil
		}
		if ref.From == graph.InputSource {
			if !runInputs[ref.Field] {
				return fmt.Errorf("graph: node %q %s: run input %q is not declared in Graph.InputSchema", n.ID, what, ref.Field)
			}
			return nil
		}
		if _, ok := byID[ref.From]; !ok {
			return fmt.Errorf("graph: node %q %s: source node %q not found", n.ID, what, ref.From)
		}
		if ref.From == n.ID {
			return fmt.Errorf("graph: node %q %s: a node cannot reference its own output", n.ID, what)
		}
		endpoint, ok := fieldEndpoint(outputs[ref.From], ref.Field)
		if !ok {
			return fmt.Errorf("graph: node %q %s: node %q has no output %q", n.ID, what, ref.From, ref.Field)
		}
		if reach.reaches(ref.From, endpoint, n.ID) {
			return nil
		}
		return fmt.Errorf("graph: node %q %s: not reachable from output %q of node %q", n.ID, what, ref.Field, ref.From)
	}
	nodes := slices.Clone(g.Nodes)
	slices.SortFunc(nodes, func(a, b graph.Node) int { return strings.Compare(a.ID, b.ID) })
	for _, n := range nodes {
		spec, _ := r.spec(n.Kind)
		cfg := cfgs[n.ID]
		if err := checkInputSlots(n, spec); err != nil {
			return nil, err
		}
		for _, name := range slices.Sorted(maps.Keys(n.Inputs)) {
			if err := check(n, fmt.Sprintf("input %q", name), n.Inputs[name]); err != nil {
				return nil, err
			}
		}
		if d, ok := cfg.(ReferenceDeclarer); ok {
			for _, nr := range d.References() {
				if err := check(n, nr.Path, nr.Ref); err != nil {
					return nil, err
				}
			}
		}
		if d, ok := cfg.(ExpressionDeclarer); ok {
			if err := checkExpressions(n, d.Expressions()); err != nil {
				return nil, err
			}
		}
	}
	return rules, nil
}

func checkInputSlots(n graph.Node, spec NodeSpec) error {
	for _, name := range inputSchemaRequired(spec) {
		if _, ok := n.Inputs[name]; !ok {
			return fmt.Errorf("graph: node %q requires input %q", n.ID, name)
		}
	}
	return nil
}

func checkExpressions(n graph.Node, exprs []Expression) error {
	inputs := slices.Sorted(maps.Keys(n.Inputs))
	for _, e := range exprs {
		for _, local := range e.Locals {
			if _, clash := n.Inputs[local]; clash {
				return fmt.Errorf("graph: node %q input %q conflicts with local name used by %s", n.ID, local, e.Path)
			}
		}
		if strings.TrimSpace(e.Text) == "" {
			continue
		}
		names := slices.Concat(inputs, e.Locals)
		var err error
		if e.Template {
			err = graph.ValidateTemplateWith(e.Text, names)
		} else {
			err = graph.ValidateExprWith(e.Text, names)
		}
		if err != nil {
			return fmt.Errorf("graph: node %q %s: %w", n.ID, e.Path, err)
		}
	}
	return nil
}

// reachIndex 回答「从节点 src 的出端点 endpoint 出发，能否沿控制边到达节点 dst」。
type reachIndex struct {
	out   map[string][]graph.Edge
	cache map[string]map[string]bool
}

func endpointMatches(endpoint, name string) bool {
	if endpoint == "" || endpoint == DefaultOutput {
		return name != graph.EndpointError
	}
	return name == endpoint
}

func (x *reachIndex) reaches(src, endpoint, dst string) bool {
	key := src + "\x00" + endpoint
	set, ok := x.cache[key]
	if !ok {
		set = map[string]bool{}
		var queue []string
		for _, e := range x.out[src] {
			if endpointMatches(endpoint, e.FromName()) {
				queue = append(queue, e.ToNode())
			}
		}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if set[cur] || cur == src {
				continue
			}
			set[cur] = true
			for _, e := range x.out[cur] {
				queue = append(queue, e.ToNode())
			}
		}
		x.cache[key] = set
	}
	return set[dst]
}
