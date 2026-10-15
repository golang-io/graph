package test

import (
	"encoding/json"
	"maps"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/service"
)

// WithEndpointSides 返回 ui 的副本：为 g 中每个节点没有写明所在边的端点补上缺省值（入口 left、出口 right），
// map 节点的循环体（参数 Body）逐层补到 SubLayouts。端点按内置节点注册表计算（node.Registry.NodeEndpoints），
// 未注册的种类按默认端点 In / Out 处理。
// 保存定义要求每个端点都写明所在边（node.Registry.CleanEndpointSides），用例构造的图用它满足这一要求；
// 已写入的条目保持不变，因此也可以先手写个别端点再调用。
//
// WithEndpointSides fills a side for every endpoint of every node of g (including map bodies) that has none.
func WithEndpointSides(g graph.Graph, ui graph.UI) graph.UI {
	reg, err := service.NewNodeRegistry()
	if err != nil {
		panic(err)
	}
	ui.Layout = layoutWithEndpointSides(reg, g, ui.Layout)
	return ui
}

func layoutWithEndpointSides(reg *node.Registry, g graph.Graph, l graph.Layout) graph.Layout {
	l.EndpointSides = maps.Clone(l.EndpointSides)
	if l.EndpointSides == nil {
		l.EndpointSides = map[string]graph.EndpointSides{}
	}
	l.SubLayouts = maps.Clone(l.SubLayouts)
	for _, n := range g.Nodes {
		eps, err := reg.NodeEndpoints(n)
		if err != nil {
			eps = node.DefaultEndpoints()
		}
		p := maps.Clone(l.EndpointSides[n.ID])
		if p == nil {
			p = graph.EndpointSides{}
		}
		for _, ep := range eps.Inputs {
			if _, ok := p[ep]; !ok {
				p[ep] = graph.SideLeft
			}
		}
		for _, ep := range eps.Outputs {
			if _, ok := p[ep]; !ok {
				p[ep] = graph.SideRight
			}
		}
		l.EndpointSides[n.ID] = p
		if n.Kind != builtin.Map {
			continue
		}
		var body graph.Graph
		if err := json.Unmarshal(n.Inputs["Body"].Value, &body); err != nil || len(body.Nodes) == 0 {
			continue
		}
		if l.SubLayouts == nil {
			l.SubLayouts = map[string]graph.Layout{}
		}
		l.SubLayouts[n.ID] = layoutWithEndpointSides(reg, body, l.SubLayouts[n.ID])
	}
	return l
}
