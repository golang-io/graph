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

// 本文件是只依赖图结构的导航辅助：出边匹配、必经汇合点判断与汇合输出合并。

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

// onlyLeadsTo 报告从节点 start 出发的所有控制流路径是否都必须经过 target。
// 主图无环（DAG）：当 start 等于 target，或 start 存在出边且每条出边都通向 target 时返回 true。
// 供 Merge(any) 在首条入边触发后跳过仅通向该汇合节点的未完成上游分支。
func onlyLeadsTo(outEdges map[string][]graph.Edge, start, target string) bool {
	if start == target {
		return true
	}
	return onlyLeadsToMemo(outEdges, start, target, map[string]bool{})
}

func onlyLeadsToMemo(outEdges map[string][]graph.Edge, start, target string, memo map[string]bool) bool {
	if start == target {
		return true
	}
	var check func(string) bool
	check = func(id string) bool {
		if id == target {
			return true
		}
		if v, ok := memo[id]; ok {
			return v
		}
		outs := outEdges[id]
		if len(outs) == 0 {
			memo[id] = false
			return false
		}
		memo[id] = false
		for _, e := range outs {
			if !check(e.ToNode()) {
				return false
			}
		}
		memo[id] = true
		return true
	}
	return check(start)
}

// mergeArrivals 按入边 ID（Slot）升序合并各分支到达时的节点输出：分叉前的节点在各分支相同，分叉后的节点互不重叠。
func mergeArrivals(arrivals map[string]engine.Outputs) engine.Outputs {
	out := engine.Outputs{}
	for _, slot := range slices.Sorted(maps.Keys(arrivals)) {
		for id, fields := range arrivals[slot] {
			out[id] = fields
		}
	}
	return out
}
