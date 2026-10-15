package node

import (
	"fmt"
	"maps"

	"github.com/golang-io/graph"
)

// CleanEndpointSides 校验并规整画布布局中的端点所在边，返回新的 Layout（不修改 l 及其中的 map）：
//   - 图中每个节点实例的每个端点（NodeEndpoints：入口 + 启用的出口）都必须在 EndpointSides[节点ID] 中写明所在边；
//   - 任何出口都不能与该节点的任一入口同边；
//   - 不属于实例端点的条目（如已删除的 switch 分支、旧格式遗留的 Out）与不在图中的节点条目被清除；
//   - 拥有内嵌子图（map 的 Body）的节点，子图节点按 SubLayouts[节点ID] 逐层处理。
//
// 保存定义链路请使用 ValidateLayout，直接复用 ValidateGraph 已算出的实例端点与静态参数；
// 单独规整画布布局时调用本方法。
//
// CleanEndpointSides requires a side for every endpoint of every node and drops entries for endpoints that no longer exist.
func (r *Registry) CleanEndpointSides(g graph.Graph, l graph.Layout) (graph.Layout, error) {
	return r.cleanLayout(g, l, nil)
}

// cleanLayout 规整一层画布布局（主图或子图层）：
//   - v 非 nil 时（经 ValidateLayout 调用）直接复用 v.Endpoints、v.Params 与子图校验树 v.Subs，全程零解码、零重复计算端点；
//   - v 为 nil 时（经 CleanEndpointSides 直接调用）逐节点现场解码参数并计算实例端点。
//
// cleanLayout validates and normalizes one layout level, reusing prevalidated endpoints and params when v is provided.
func (r *Registry) cleanLayout(g graph.Graph, l graph.Layout, v *Validated) (graph.Layout, error) {
	sides := make(map[string]graph.EndpointSides, len(g.Nodes))
	subLayouts := maps.Clone(l.SubLayouts)
	for _, n := range g.Nodes {
		var (
			cfg any
			eps EndpointSpec
			ok  bool
		)
		if v != nil {
			cfg, ok = v.Params[n.ID]
			eps = v.Endpoints[n.ID]
		}
		if !ok {
			spec, decoded, err := r.decodeNode(n)
			if err != nil {
				return l, err
			}
			eps, err = spec.resolveEndpoints(n.ID, decoded)
			if err != nil {
				return l, err
			}
			cfg = decoded
		}
		p, ok := l.EndpointSides[n.ID]
		if !ok {
			return l, fmt.Errorf("graph: node %s endpoint sides required", n.ID)
		}
		var err error
		if sides[n.ID], err = cleanEndpointSides(eps, p); err != nil {
			return l, fmt.Errorf("graph: node %s endpoint sides: %w", n.ID, err)
		}
		// 子图层布局按拥有子图的节点 ID 存放；一个节点只对应一层子图布局。
		if d, ok := cfg.(SubgraphDeclarer); ok {
			subs := d.Subgraphs()
			for i, sub := range subs {
				var child *Validated
				if v != nil && i < len(v.Subs[n.ID]) {
					child = v.Subs[n.ID][i]
				}
				cleaned, err := r.cleanLayout(sub.Graph, l.SubLayouts[n.ID], child)
				if err != nil {
					return l, fmt.Errorf("graph: node %q %s: %w", n.ID, sub.Path, err)
				}
				if subLayouts == nil {
					subLayouts = map[string]graph.Layout{}
				}
				subLayouts[n.ID] = cleaned
			}
		}
	}
	l.EndpointSides, l.SubLayouts = sides, subLayouts
	return l, nil
}

// cleanEndpointSides 只保留 eps 中的端点条目；缺少任一端点、取值非法或出口与入口同边时报错。
func cleanEndpointSides(eps EndpointSpec, p graph.EndpointSides) (graph.EndpointSides, error) {
	out := make(graph.EndpointSides, len(eps.Inputs)+len(eps.Outputs))
	inSides := map[string]bool{}
	side := func(ep string) (string, error) {
		s, ok := p[ep]
		if !ok {
			return "", fmt.Errorf("endpoint %s side required", ep)
		}
		if !graph.ValidSide(s) {
			return "", fmt.Errorf("endpoint %s side %q", ep, s)
		}
		return s, nil
	}
	for _, ep := range eps.Inputs {
		s, err := side(ep)
		if err != nil {
			return nil, err
		}
		out[ep], inSides[s] = s, true
	}
	for _, ep := range eps.Outputs {
		s, err := side(ep)
		if err != nil {
			return nil, err
		}
		if inSides[s] {
			return nil, fmt.Errorf("output %s and an input are both %q", ep, s)
		}
		out[ep] = s
	}
	return out, nil
}
