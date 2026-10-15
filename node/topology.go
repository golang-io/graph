package node

import (
	"fmt"
	"sort"
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
	return indexEdges(edges, func(e graph.Edge) string { return e.FromNode() }, func(a, b graph.Edge) bool {
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.To < b.To
	})
}

// IndexInEdges 按目标节点 ID 分组（节点 ID 从 To 端点 ID 解析），并按边 ID、来源端点稳定排序。
// IndexInEdges groups edges by target node ID (parsed from the To endpoint ID).
func IndexInEdges(edges []graph.Edge) map[string][]graph.Edge {
	return indexEdges(edges, func(e graph.Edge) string { return e.ToNode() }, func(a, b graph.Edge) bool {
		if a.ID != b.ID {
			return a.ID < b.ID
		}
		return a.From < b.From
	})
}

func indexEdges(edges []graph.Edge, key func(graph.Edge) string, less func(a, b graph.Edge) bool) map[string][]graph.Edge {
	m := make(map[string][]graph.Edge)
	for _, e := range edges {
		k := key(e)
		m[k] = append(m[k], e)
	}
	for id := range m {
		sort.Slice(m[id], func(i, j int) bool { return less(m[id][i], m[id][j]) })
	}
	return m
}

// validateControlTopology 按端点属性校验出边、入口与可达性，并返回唯一入口节点 ID：
// 唯一入口（无入端点且无入边）、每条出边对应已声明出口、所有节点从入口可达、图中无环。
func validateControlTopology(g graph.Graph, eps map[string]EndpointSpec, outgoing map[string][]graph.Edge) (string, error) {
	for _, n := range g.Nodes {
		if err := validateNodeOutgoing(eps[n.ID], outgoing[n.ID]); err != nil {
			return "", fmt.Errorf("graph: node %q: %w", n.ID, err)
		}
	}
	incoming := make(map[string]int, len(g.Nodes))
	for _, edge := range g.Edges {
		incoming[edge.ToNode()]++
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
	return entry, nil
}

// validateNodeOutgoing 按实例启用出口校验出边：每条出边的 From 端点本地名必须是已声明出口。
// 启用的出口集合由 resolveEndpoints 统一算出，此处只做成员检查与错误边唯一下限检查。
func validateNodeOutgoing(eps EndpointSpec, edges []graph.Edge) error {
	if len(eps.Outputs) == 0 {
		if len(edges) != 0 {
			return fmt.Errorf("terminal node cannot have successors")
		}
		return nil
	}
	outIDs := map[string]bool{}
	for _, id := range eps.Outputs {
		outIDs[id] = true
	}
	errEdges := 0
	for _, edge := range edges {
		name := edge.FromName()
		if !outIDs[name] {
			return fmt.Errorf("edge %q targets undeclared output %q", edge.ID, name)
		}
		if isErrorEndpoint(name) {
			errEdges++
		}
	}
	if errEdges > 1 {
		return fmt.Errorf("multiple error edges")
	}
	// 仅声明 Error 出口的节点（如 stop_error）允许没有后继：Fail 时整路径失败。
	onlyError := true
	for _, id := range eps.Outputs {
		if !isErrorEndpoint(id) {
			onlyError = false
			break
		}
	}
	if len(edges) == 0 {
		if onlyError {
			return nil
		}
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
	active := map[string]bool{}
	seen := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if active[id] {
			return fmt.Errorf("graph: cycle through node %q (use a map node body for iteration)", id)
		}
		if seen[id] {
			return nil
		}
		active[id] = true
		for _, e := range outgoing[id] {
			if err := visit(e.ToNode()); err != nil {
				return err
			}
		}
		delete(active, id)
		seen[id] = true
		return nil
	}
	if err := visit(entry); err != nil {
		return err
	}
	if len(seen) != total {
		return fmt.Errorf("graph: unreachable nodes")
	}
	return nil
}
