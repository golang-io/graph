package node

import (
	"fmt"
	"slices"
	"strings"

	"github.com/golang-io/graph"
)

// IndexNodes 按 ID 建节点索引。
func IndexNodes(nodes []graph.Node) map[string]graph.Node {
	m := make(map[string]graph.Node, len(nodes))
	for _, n := range nodes {
		m[n.ID] = n
	}
	return m
}

// IndexOutEdges 按来源节点 ID 分组（节点 ID 从 From 端点 ID 解析），并按边 ID、目标端点稳定排序。
// IndexOutEdges groups edges by source node ID (parsed from the From endpoint ID).
func IndexOutEdges(edges []graph.Edge) map[string][]graph.Edge {
	return indexEdges(edges, func(e graph.Edge) string { return e.FromNode() }, func(a, b graph.Edge) int {
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return strings.Compare(a.To, b.To)
	})
}

// IndexInEdges 按目标节点 ID 分组（节点 ID 从 To 端点 ID 解析），并按边 ID、来源端点稳定排序。
// IndexInEdges groups edges by target node ID (parsed from the To endpoint ID).
func IndexInEdges(edges []graph.Edge) map[string][]graph.Edge {
	return indexEdges(edges, func(e graph.Edge) string { return e.ToNode() }, func(a, b graph.Edge) int {
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return strings.Compare(a.From, b.From)
	})
}

func indexEdges(edges []graph.Edge, key func(graph.Edge) string, cmp func(a, b graph.Edge) int) map[string][]graph.Edge {
	m := make(map[string][]graph.Edge, len(edges))
	for _, e := range edges {
		k := key(e)
		m[k] = append(m[k], e)
	}
	for _, list := range m {
		if len(list) > 1 {
			slices.SortFunc(list, cmp)
		}
	}
	return m
}

// validateControlTopology 按端点属性校验出边、入边、入口与可达性，并返回唯一入口节点 ID：
// 唯一入口（无入端点且无入边）、每条出边对应已声明出口、每条入边对应已声明入口、
// 非 Merge 节点最多一条入边、所有节点从入口可达、图中无环。
func validateControlTopology(g graph.Graph, eps map[string]EndpointSpec, joinModes map[string]string, outgoing map[string][]graph.Edge) (string, error) {
	for _, n := range g.Nodes {
		if err := validateNodeOutgoing(eps[n.ID], outgoing[n.ID]); err != nil {
			return "", fmt.Errorf("graph: node %q: %w", n.ID, err)
		}
	}
	incoming := make(map[string]int, len(g.Nodes))
	for _, edge := range g.Edges {
		toNode := edge.ToNode()
		toName := edge.ToName()
		if !eps[toNode].IsEntry() && !slices.Contains(eps[toNode].Inputs, toName) {
			return "", fmt.Errorf("graph: node %q: edge %q targets undeclared input %q", toNode, edge.ID, toName)
		}
		incoming[toNode]++
	}
	entry := ""
	for _, n := range g.Nodes {
		if !eps[n.ID].IsEntry() {
			continue
		}
		if entry != "" {
			return "", fmt.Errorf("graph: multiple entry nodes (%q, %q)", entry, n.ID)
		}
		if incoming[n.ID] > 0 {
			return "", fmt.Errorf("graph: entry node %q cannot have predecessors", n.ID)
		}
		entry = n.ID
	}
	if entry == "" {
		return "", fmt.Errorf("graph: entry node required (a node with no input endpoints)")
	}
	if err := validateReachability(entry, len(g.Nodes), outgoing); err != nil {
		return "", err
	}
	for _, n := range g.Nodes {
		if !eps[n.ID].IsEntry() && incoming[n.ID] > 1 && joinModes[n.ID] == "" {
			return "", fmt.Errorf("graph: node %q cannot have multiple incoming edges (use a merge node)", n.ID)
		}
	}
	return entry, nil
}

// validateNodeOutgoing 按实例启用出口校验出边：每条出边的 From 端点本地名必须是已声明出口。
// 启用的出口集合由 resolveEndpoints 统一算出，此处只做成员检查与后继存在性检查。
func validateNodeOutgoing(eps EndpointSpec, edges []graph.Edge) error {
	if len(eps.Outputs) == 0 {
		if len(edges) != 0 {
			return fmt.Errorf("terminal node cannot have successors")
		}
		return nil
	}
	for _, edge := range edges {
		name := edge.FromName()
		if !slices.Contains(eps.Outputs, name) {
			return fmt.Errorf("edge %q targets undeclared output %q", edge.ID, name)
		}
	}
	if len(edges) == 0 {
		return fmt.Errorf("requires a successor")
	}
	return nil
}

// canvasHandle 报告 id 是否与画布句柄 In / InN / OutN 同名；这类名字不能声明为业务出口。
func canvasHandle(id string) bool {
	for _, prefix := range []string{DefaultInput, DefaultOutput} {
		if rest, ok := strings.CutPrefix(id, prefix); ok && strings.Trim(rest, "0123456789") == "" && (rest != "" || id == DefaultInput) {
			return true
		}
	}
	return false
}

// validateReachability 从入口做深度优先遍历：所有节点必须可达，且不得成环。
// 需要重复执行的逻辑放进 map 节点的循环体（子图），主图保持无环。
func validateReachability(entry string, total int, outgoing map[string][]graph.Edge) error {
	state := make(map[string]uint8, total) // 0: 未访问, 1: 遍历栈上, 2: 已完成
	var visit func(string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("graph: cycle through node %q (use a map node body for iteration)", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, e := range outgoing[id] {
			if err := visit(e.ToNode()); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	if err := visit(entry); err != nil {
		return err
	}
	if len(state) != total {
		return fmt.Errorf("graph: unreachable nodes")
	}
	return nil
}
