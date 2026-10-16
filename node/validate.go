package node

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
)

// 本文件是注册表的两类校验：保存 / 启动前对整张图的单次遍历校验（Validate / ValidateLayout），
// 以及运行时对节点返回值形态的校验（CheckOutput）。

// Validate 校验图：图自洽、逐节点参数、控制拓扑、输入引用与表达式标识符，以及参数中内嵌的子图（map 的 Body）。
// 每个节点实例的参数只解码一次。
func (r *Registry) Validate(g graph.Graph) error {
	return r.validate(g, 0)
}

// ValidateLayout 完成整图校验与画布布局端点所在边规整，供保存链路使用。
func (r *Registry) ValidateLayout(g graph.Graph, l graph.Layout) (graph.Layout, error) {
	if err := r.Validate(g); err != nil {
		return l, err
	}
	return r.CleanEndpointSides(g, l)
}

// validate 校验一张图；depth 是内嵌子图的层数，超过 graph.MaxSubgraphDepth 时拒绝。
func (r *Registry) validate(g graph.Graph, depth int) error {
	if depth > graph.MaxSubgraphDepth {
		return fmt.Errorf("graph: nested graphs exceed max depth %d", graph.MaxSubgraphDepth)
	}
	if err := g.Validate(); err != nil {
		return err
	}
	nodes := IndexNodes(g.Nodes)
	out := IndexOutEdges(g.Edges)
	endpoints := make(map[string]EndpointSpec, len(g.Nodes))
	joinModes := map[string]string{}
	params := make(map[string]any, len(g.Nodes))

	for _, n := range g.Nodes {
		spec, cfg, err := r.decodeNode(n)
		if err != nil {
			return err
		}
		if iv, ok := cfg.(InputValidator); ok {
			if err := iv.Validate(); err != nil {
				return fmt.Errorf("graph: node %q kind %q params: %w", n.ID, n.Kind, err)
			}
		}
		if err := r.validateSubgraphs(n, spec, cfg, depth); err != nil {
			return err
		}
		eps, err := spec.resolveEndpoints(n.ID, cfg)
		if err != nil {
			return err
		}
		params[n.ID] = cfg
		endpoints[n.ID] = eps
		if d, ok := cfg.(JoinModeDeclarer); ok {
			if mode := d.JoinMode(); mode != "" {
				joinModes[n.ID] = mode
			}
		}
	}
	if _, err := validateControlTopology(g, endpoints, joinModes, out); err != nil {
		return err
	}
	return r.checkReferences(g, nodes, out, params)
}

// validateSubgraphs 递归校验节点参数中内嵌的子图，并检查子图的必填输入都能由节点提供：
// 节点固定提供 Subgraph.Provides，另把实例上的自定义输入（不属于参数结构体 InputSchema 的输入名）按同名传入。
func (r *Registry) validateSubgraphs(n graph.Node, spec NodeSpec, cfg any, depth int) error {
	d, ok := cfg.(SubgraphDeclarer)
	if !ok {
		return nil
	}
	subs := d.Subgraphs()
	if len(subs) == 0 {
		return nil
	}
	provided := map[string]bool{}
	for name := range n.Inputs {
		if spec.InputSchema == nil || spec.InputSchema.Properties[name] == nil {
			provided[name] = true
		}
	}
	for _, sub := range subs {
		if err := r.validate(sub.Graph, depth+1); err != nil {
			return fmt.Errorf("graph: node %q %s: %w", n.ID, sub.Path, err)
		}
		for _, name := range jsonx.RequiredNames(sub.Graph.InputSchema) {
			if !provided[name] && !slices.Contains(sub.Provides, name) {
				return fmt.Errorf("graph: node %q %s requires input %q that the node does not provide", n.ID, sub.Path, name)
			}
		}
	}
	return nil
}

// CheckOutput 校验节点实例 n 的返回值形态、输出键与返回端点是否合法（调用方已完成 Set 的可序列化规整）。
// State 与 EffectSpawn 仅允许实现 Controller 的节点种类返回；纯 Executor 返回时直接拒绝。
//
// CheckOutput validates the shape, controller-only fields (State / EffectSpawn), output keys, and endpoint of out.
func (r *Registry) CheckOutput(n graph.Node, out NodeOutput) error {
	if _, ok := r.spec(n.Kind); !ok {
		return fmt.Errorf("graph: node %q kind %q is not registered", n.ID, n.Kind)
	}
	if err := out.ValidateShape(); err != nil {
		return fmt.Errorf("graph: node %q: %w", n.ID, err)
	}
	if _, ok := r.nodes[n.Kind].(Controller); !ok {
		if len(out.State) > 0 {
			return fmt.Errorf("graph: node %q: State is only allowed for Controller", n.ID)
		}
		if out.Effect != nil && strings.TrimSpace(out.Effect.Kind) == EffectSpawn {
			return fmt.Errorf("graph: node %q: spawn effect is only allowed for Controller", n.ID)
		}
	}
	if len(out.Set) == 0 && out.Endpoint == "" {
		return nil
	}
	spec, cfg, err := r.decodeNode(n)
	if err != nil {
		return err
	}
	if len(out.Set) > 0 {
		for key := range out.Set {
			if !hasDeclaredOutput(spec, cfg, key) {
				for _, sortedKey := range slices.Sorted(maps.Keys(out.Set)) {
					if !hasDeclaredOutput(spec, cfg, sortedKey) {
						return fmt.Errorf("graph: node %q returned undeclared output %q", n.ID, sortedKey)
					}
				}
			}
		}
	}
	if out.Endpoint == "" {
		return nil
	}
	eps, err := spec.resolveEndpoints(n.ID, cfg)
	if err != nil {
		return err
	}
	if eps.isReturnableOutput(out.Endpoint) {
		return nil
	}
	return fmt.Errorf("graph: node %q returned undeclared or disabled endpoint %q", n.ID, out.Endpoint)
}
