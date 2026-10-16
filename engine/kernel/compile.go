package kernel

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/golang-io/graph/node"
	"github.com/google/jsonschema-go/jsonschema"
)

// graphView 是单次 Apply 按 Task.Graph 构建的轻量只读视图：
// 节点与边直接按需从 Graph 切片读取（边匹配使用端点前缀直接比对），
// 仅在需要整图出边索引（如 Merge(any) 跳过其他在途分支）时构建一次，避免每次单步推进分配全图节点/边 map。
type graphView struct {
	reg *node.Registry
	g   graph.Graph
	out map[string][]graph.Edge
}

func newGraphView(reg *node.Registry, g graph.Graph) *graphView {
	return &graphView{reg: reg, g: g}
}

func (gv *graphView) node(id string) graph.Node {
	n, _ := findNode(gv.g.Nodes, id)
	return n
}

func findNode(nodes []graph.Node, id string) (graph.Node, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return graph.Node{}, false
}

// edgeMatchesNode 判断端点 ID（nodeID + "." + localName）是否属于节点 nodeID，
// 直接按长度与字节位置比对，避免对全图每条边调用 strings.LastIndex。
func edgeMatchesNode(endpointID, nodeID string) bool {
	n := len(nodeID)
	return len(endpointID) > n+1 &&
		endpointID[n] == '.' &&
		endpointID[:n] == nodeID &&
		strings.IndexByte(endpointID[n+1:], '.') < 0
}

func (gv *graphView) entryNode() string {
	for _, n := range gv.g.Nodes {
		if !gv.hasInEdge(n.ID) {
			if eps, err := gv.reg.NodeEndpoints(n); err == nil && eps.IsEntry() {
				return n.ID
			}
		}
	}
	return ""
}

func (gv *graphView) hasInEdge(nodeID string) bool {
	for _, e := range gv.g.Edges {
		if edgeMatchesNode(e.To, nodeID) {
			return true
		}
	}
	return false
}

// inEdges 返回指向 nodeID 的多条入边（按 ID、From 稳定排序）；入边数 <= 1 时返回 nil（零分配）。
func (gv *graphView) inEdges(nodeID string) []graph.Edge {
	var first graph.Edge
	var more []graph.Edge
	count := 0
	for _, e := range gv.g.Edges {
		if edgeMatchesNode(e.To, nodeID) {
			count++
			if count == 1 {
				first = e
			} else {
				if more == nil {
					more = make([]graph.Edge, 0, 4)
					more = append(more, first)
				}
				more = append(more, e)
			}
		}
	}
	if count <= 1 {
		return nil
	}
	slices.SortFunc(more, func(a, b graph.Edge) int {
		if c := strings.Compare(a.ID, b.ID); c != 0 {
			return c
		}
		return strings.Compare(a.From, b.From)
	})
	return more
}

// outEdges 返回从 nodeID 出发的全部出边（按 ID、To 稳定排序）。
func (gv *graphView) outEdges(nodeID string) []graph.Edge {
	if gv.out != nil {
		return gv.out[nodeID]
	}
	var out []graph.Edge
	for _, e := range gv.g.Edges {
		if edgeMatchesNode(e.From, nodeID) {
			if out == nil {
				out = make([]graph.Edge, 0, 2)
			}
			out = append(out, e)
		}
	}
	if len(out) > 1 {
		slices.SortFunc(out, func(a, b graph.Edge) int {
			if c := strings.Compare(a.ID, b.ID); c != 0 {
				return c
			}
			return strings.Compare(a.To, b.To)
		})
	}
	return out
}

func (gv *graphView) allOutEdges() map[string][]graph.Edge {
	if gv.out == nil {
		gv.out = node.IndexOutEdges(gv.g.Edges)
	}
	return gv.out
}

// projectNodeInputs 按节点 Inputs 取节点运行期输入（字面量、运行输入 $input 或本路径上已执行节点的输出），并合入调试注入 inj.Vars。
// 仅作静态配置的复合参数字段（如 Outputs / Body / Cases）由 ResolveParams 直接解码进 Params，此处不再重复反序列化到 vars。
// 已被 inj.Vars 覆盖的输入名不再要求上游来源存在；未被覆盖且来源缺失时按输入名升序返回首个缺失错误。
func projectNodeInputs(reg *node.Registry, n graph.Node, input map[string]any, outs engine.Outputs, inj *engine.DebugInject) (map[string]any, error) {
	vars := make(map[string]any, len(n.Inputs))
	var errName string
	var firstErr error
	recordErr := func(name string, err error) {
		if errName == "" || name < errName {
			errName = name
			firstErr = err
		}
	}
	for name, ref := range n.Inputs {
		switch ref.From {
		case "":
			if reg != nil && reg.IsParamOnlyLiteral(n.Kind, name) {
				if err := jsonx.CheckValid(ref.Value); err != nil {
					recordErr(name, fmt.Errorf("input %q value: %v", name, err))
				}
				continue
			}
			var val any
			if err := jsonx.DecodeStored(ref.Value, &val); err != nil {
				recordErr(name, fmt.Errorf("input %q value: %v", name, err))
				continue
			}
			vars[name] = val
		case graph.InputSource:
			vars[name] = engine.CloneValue(input[ref.Field])
		default:
			if inj != nil {
				if _, injected := inj.Vars[name]; injected {
					continue
				}
			}
			fields, ok := outs[ref.From]
			if !ok {
				recordErr(name, fmt.Errorf("input %q unavailable: node %q not executed", name, ref.From))
				continue
			}
			v, ok := fields[ref.Field]
			if !ok {
				recordErr(name, fmt.Errorf("input %q unavailable: node %q has no output %q", name, ref.From, ref.Field))
				continue
			}
			vars[name] = engine.CloneValue(v)
		}
	}
	if inj != nil {
		for k, v := range inj.Vars {
			vars[k] = engine.CloneValue(v)
		}
	}
	return vars, firstErr
}

// resolveNodeParams 解码节点静态字面量参数，并在存在动态绑定或调试注入覆盖时合入运行期值。
func resolveNodeParams(reg *node.Registry, n graph.Node, vars map[string]any, inj *engine.DebugInject) any {
	var injected map[string]any
	if inj != nil {
		injected = inj.Vars
	}
	return reg.ResolveParams(n, vars, injected)
}

// CheckInput 按 Graph.InputSchema 校验运行输入（JSON Schema Resolve + Validate）。
// Schema 为 nil 时不允许携带任何键；非 nil 时由 required / additionalProperties / type 等关键字约束。
func CheckInput(schema *jsonschema.Schema, input map[string]any) error {
	if input == nil {
		input = map[string]any{}
	}
	if schema == nil {
		if len(input) == 0 {
			return nil
		}
		keys := slices.Sorted(maps.Keys(input))
		return fmt.Errorf("%w: run input %q is not declared in Graph.InputSchema", engine.ErrInvalid, keys[0])
	}
	resolved, err := schema.Resolve(nil)
	if err != nil {
		return fmt.Errorf("%w: run input schema: %v", engine.ErrInvalid, err)
	}
	if err := resolved.Validate(input); err != nil {
		return fmt.Errorf("%w: run input: %v", engine.ErrInvalid, err)
	}
	return nil
}

// CheckDebug 校验调试选项（内联节点与活动节点均支持设置断点；调试注入的 Vars 须可规整且符合节点参数类型与校验规则）。
func CheckDebug(reg *node.Registry, g graph.Graph, d *engine.DebugOptions) error {
	if d == nil {
		return nil
	}
	norm, err := d.Normalize()
	if err != nil {
		return err
	}
	nodes := node.IndexNodes(g.Nodes)
	for _, id := range slices.Sorted(maps.Keys(norm.Injects)) {
		inj := norm.Injects[id]
		n, ok := nodes[id]
		if !ok || len(inj.Vars) == 0 {
			continue
		}
		if err := node.CheckParams(reg.ResolveParams(n, inj.Vars, inj.Vars)); err != nil {
			return fmt.Errorf("%w: debug inject %s: %v", engine.ErrInvalid, id, err)
		}
	}
	return nil
}
