package graph

import (
	"slices"
	"strings"

	"github.com/golang-io/graph/internal/jsonx"
)

// 内容哈希只在需要比较时实时计算，不落库：GraphID + GraphVersion 唯一确定一个已保存版本，
// 哈希仅用于判断两份内容是否相同（保存时是否追加版本、编译缓存去重、启动时核对图与版本）。
// 哈希包含 Graph.GraphID / GraphVersion：身份不同即视为不同的图。需要只比较内容时，
// 调用方先把两边的身份设为相同的值再比较。计算前按 Graph.filled 补齐 nil 切片，与落库形态一致。

// HashContent 作者态身份：图或布局任一变化都会变。
func HashContent(g Graph, ui UI) (string, error) {
	g = g.filled()
	graphJSON, err := jsonx.Canonical(g)
	if err != nil {
		return "", err
	}
	uiJSON, err := MarshalUI(ui, g.Nodes...)
	if err != nil {
		return "", err
	}
	return jsonx.Digest(slices.Concat(graphJSON, []byte{0}, uiJSON)), nil
}

// HashDefinition 排序节点/边，再哈希规范化后的图 JSON。
// HashDefinition sorts nodes/edges, then hashes canonical graph JSON.
func HashDefinition(g Graph) (Graph, string, error) {
	g = g.filled()
	g.Nodes = slices.Clone(g.Nodes)
	g.Edges = slices.Clone(g.Edges)
	slices.SortFunc(g.Nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(g.Edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	graphJSON, err := jsonx.Canonical(g)
	if err != nil {
		return Graph{}, "", err
	}
	return g, jsonx.Digest(graphJSON), nil
}
