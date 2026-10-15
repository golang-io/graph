package kernel

import (
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
)

// 本文件是只依赖图结构的导航辅助：出边匹配、错误出口、汇合点分析、汇合输出合并。

// matchingOutEdges 返回本次节点成功退出应激活的全部边。
// branch 为空时按默认出口 Out 处理；匹配 From 端点本地名等于 branch 的出边；
// 没有匹配的出边时返回空。按 ID、To 稳定排序。
//
// matchingOutEdges returns edges whose From endpoint local name equals branch
// (empty branch means the default output "Out"); it sorts stably by ID then To.
func matchingOutEdges(edges []graph.Edge, branch string) []graph.Edge {
	if branch == "" {
		branch = node.DefaultOutput
	}
	matches := make([]graph.Edge, 0, len(edges))
	for _, edge := range edges {
		if edge.FromName() == branch {
			matches = append(matches, edge)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].ID != matches[j].ID {
			return matches[i].ID < matches[j].ID
		}
		return matches[i].To < matches[j].To
	})
	return matches
}

// errNoMatchingOutgoing 格式化无匹配出边的错误信息（branch 为匹配的出口本地名）。
func errNoMatchingOutgoing(nodeID, branch string, edges []graph.Edge) error {
	if branch == "" {
		branch = node.DefaultOutput
	}
	if len(edges) == 0 {
		return fmt.Errorf("node %s has no matching outgoing edge (branch=%q): no outgoing edges", nodeID, branch)
	}
	have := make([]string, 0, len(edges))
	for _, e := range edges {
		have = append(have, fmt.Sprintf("%q", e.FromName()))
	}
	return fmt.Errorf("node %s has no matching outgoing edge (branch=%q, have [%s])", nodeID, branch, strings.Join(have, ", "))
}

// findErrorEdge 精确匹配 From 端点本地名为 Error 的出边（不回落到其他出边）。
// 节点失败时有这条边就沿它继续；拓扑校验保证只有声明了 Error 出口的节点才能连 Error 出边。
func findErrorEdge(edges []graph.Edge) (graph.Edge, bool) {
	for _, edge := range edges {
		if edge.FromName() == graph.EndpointError {
			return edge, true
		}
	}
	return graph.Edge{}, false
}

// errorOutputs 是节点走错误出口时写入 Run.Outputs[节点ID] 的输出：只有 Error（失败信息）。
// 下游通过 {"From": 节点ID, "Field": "Error"} 引用，且只能位于 Error 出端点的出边之后。
func errorOutputs(msg string) map[string]any {
	return map[string]any{graph.ErrorField: msg}
}

// commonJoinTarget 返回所有分支共同到达的最近汇入节点；空表示分支独立结束。
// 声明了汇合方式（merge）或有多条入边的节点都可作为汇入候选；joinModes 来自 Compiled.JoinModes。
// 每个候选按节点记忆化可达性与距离，复杂度与节点数成线性，不随路径数指数增长。
func commonJoinTarget(joinModes map[string]string, eps map[string]node.EndpointSpec, nodes map[string]graph.Node, outEdges, inEdges map[string][]graph.Edge, sourceEdges []graph.Edge) string {
	candidates := make([]string, 0)
	for id := range nodes {
		if joinModes[id] != "" || len(inEdges[id]) > 1 {
			candidates = append(candidates, id)
		}
	}
	sort.Strings(candidates)

	type reach struct {
		ok       bool
		distance int
	}
	best := ""
	bestDistance := int(^uint(0) >> 1)
	for _, candidate := range candidates {
		visiting := map[string]bool{}
		memo := map[string]reach{}
		var reaches func(string) (bool, int)
		reaches = func(id string) (bool, int) {
			if id == candidate {
				return true, 0
			}
			if r, done := memo[id]; done {
				return r.ok, r.distance
			}
			if visiting[id] {
				return false, 0
			}
			visiting[id] = true
			r := reach{}
			r.ok, r.distance = joinChildren(eps[id], outEdges[id], reaches)
			delete(visiting, id)
			memo[id] = r
			return r.ok, r.distance
		}

		valid := true
		distance := 0
		for _, edge := range sourceEdges {
			ok, childDistance := reaches(edge.ToNode())
			if !ok {
				valid = false
				break
			}
			if childDistance > distance {
				distance = childDistance
			}
		}
		if valid && distance < bestDistance {
			best = candidate
			bestDistance = distance
		}
	}
	return best
}

// joinChildren 计算节点的所有出边是否都汇入候选，以及到候选的最远距离。
func joinChildren(ep node.EndpointSpec, out []graph.Edge, reaches func(string) (bool, int)) (bool, int) {
	if ep.IsTerminal() || len(out) == 0 {
		return false, 0
	}
	distance := 0
	for _, edge := range out {
		ok, childDistance := reaches(edge.ToNode())
		if !ok {
			return false, 0
		}
		if childDistance > distance {
			distance = childDistance
		}
	}
	return true, distance + 1
}

// mergeArrivals 按 Slot 升序合并各分支到达时的节点输出：分叉前的节点在各分支相同，分叉后的节点互不重叠。
func mergeArrivals(arrivals map[string]engine.Outputs) engine.Outputs {
	out := engine.Outputs{}
	for _, slot := range slices.Sorted(maps.Keys(arrivals)) {
		for id, fields := range arrivals[slot] {
			out[id] = fields
		}
	}
	return out
}
