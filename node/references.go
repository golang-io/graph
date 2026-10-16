package node

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
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

// checkReferences 执行上面 5 条规则；复用已解码的节点参数 cfgs 与已建立的节点/出边索引。
// 节点按 ID 升序检查，保证同一张图的错误消息稳定。
func (r *Registry) checkReferences(g graph.Graph, byID map[string]graph.Node, out map[string][]graph.Edge, cfgs map[string]any) error {
	runInputs := map[string]bool{}
	for _, name := range jsonx.PropertyNames(g.InputSchema) {
		runInputs[name] = true
	}
	reach := newReachIndex(g.Nodes, out)
	checkRef := func(nodeID string, ref graph.InputRef) bool {
		if ref.From == "" {
			return true
		}
		if ref.From == graph.InputSource {
			return runInputs[ref.Field]
		}
		srcNode, ok := byID[ref.From]
		if !ok || ref.From == nodeID {
			return false
		}
		srcSpec, _ := r.spec(srcNode.Kind)
		if !hasDeclaredOutput(srcSpec, cfgs[ref.From], ref.Field) {
			return false
		}
		return reach.reaches(ref.From, nodeID)
	}
	formatRefErr := func(n graph.Node, what string, ref graph.InputRef) error {
		if ref.From == "" {
			return nil
		}
		if ref.From == graph.InputSource {
			if !runInputs[ref.Field] {
				return fmt.Errorf("graph: node %q %s: run input %q is not declared in Graph.InputSchema", n.ID, what, ref.Field)
			}
			return nil
		}
		srcNode, ok := byID[ref.From]
		if !ok {
			return fmt.Errorf("graph: node %q %s: source node %q not found", n.ID, what, ref.From)
		}
		if ref.From == n.ID {
			return fmt.Errorf("graph: node %q %s: a node cannot reference its own output", n.ID, what)
		}
		srcSpec, _ := r.spec(srcNode.Kind)
		if !hasDeclaredOutput(srcSpec, cfgs[ref.From], ref.Field) {
			return fmt.Errorf("graph: node %q %s: node %q has no output %q", n.ID, what, ref.From, ref.Field)
		}
		if reach.reaches(ref.From, n.ID) {
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
			return err
		}
		for _, ref := range n.Inputs {
			if !checkRef(n.ID, ref) {
				for _, name := range slices.Sorted(maps.Keys(n.Inputs)) {
					if err := formatRefErr(n, fmt.Sprintf("input %q", name), n.Inputs[name]); err != nil {
						return err
					}
				}
			}
		}
		if d, ok := cfg.(ReferenceDeclarer); ok {
			for _, nr := range d.References() {
				if !checkRef(n.ID, nr.Ref) {
					return formatRefErr(n, nr.Path, nr.Ref)
				}
			}
		}
		if d, ok := cfg.(ExpressionDeclarer); ok {
			if err := checkExpressions(n, d.Expressions()); err != nil {
				return err
			}
		}
	}
	return nil
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
	if len(exprs) == 0 {
		return nil
	}
	var inputs []string
	inputsSorted := false
	for _, e := range exprs {
		for _, local := range e.Locals {
			if _, clash := n.Inputs[local]; clash {
				return fmt.Errorf("graph: node %q input %q conflicts with local name used by %s", n.ID, local, e.Path)
			}
		}
		if strings.TrimSpace(e.Text) == "" {
			continue
		}
		if !inputsSorted {
			inputs = slices.Sorted(maps.Keys(n.Inputs))
			inputsSorted = true
		}
		names := inputs
		if len(e.Locals) > 0 {
			names = slices.Concat(inputs, e.Locals)
		}
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

// reachIndex 回答「从节点 src 出发，能否沿控制边到达节点 dst」：
// 为图中节点分配连续整数下标并用 uint64 位集合按需计算可达性，消除对每个源节点分配 map[string]bool 与 BFS 队列的 O(N^2) 开销。
type reachIndex struct {
	idx   map[string]int
	out   [][]int
	bits  [][]uint64
	words int
}

func newReachIndex(nodes []graph.Node, outEdges map[string][]graph.Edge) *reachIndex {
	n := len(nodes)
	words := (n + 63) / 64
	idx := make(map[string]int, n)
	for i, nd := range nodes {
		idx[nd.ID] = i
	}
	out := make([][]int, n)
	for i, nd := range nodes {
		edges := outEdges[nd.ID]
		if len(edges) == 0 {
			continue
		}
		targets := make([]int, 0, len(edges))
		for _, e := range edges {
			if to, ok := idx[e.ToNode()]; ok {
				targets = append(targets, to)
			}
		}
		out[i] = targets
	}
	return &reachIndex{
		idx:   idx,
		out:   out,
		bits:  make([][]uint64, n),
		words: words,
	}
}

func (x *reachIndex) reaches(src, dst string) bool {
	u, okU := x.idx[src]
	v, okV := x.idx[dst]
	if !okU || !okV || u == v {
		return false
	}
	b := x.row(u)
	return (b[v/64] & (uint64(1) << (v % 64))) != 0
}

func (x *reachIndex) row(u int) []uint64 {
	if b := x.bits[u]; b != nil {
		return b
	}
	b := make([]uint64, x.words)
	x.bits[u] = b
	for _, next := range x.out[u] {
		b[next/64] |= uint64(1) << (next % 64)
		child := x.row(next)
		for w := range x.words {
			b[w] |= child[w]
		}
	}
	return b
}
