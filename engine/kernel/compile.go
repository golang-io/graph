package kernel

import (
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/golang-io/graph/node"
)

// Compiled 是校验后的图及其执行索引；结果只读，由 Cache 按图内容去重。
// 索引全部由 Graph 与种类声明推出，不保存图之外的新事实。汇合点在编译期一次算好，执行期只查表。
//
// Compiled is a validated graph with indexes and input projection plans.
type Compiled struct {
	Graph     graph.Graph // 校验通过的图：执行使用，也原样写入 Task.Graph
	Registry  *node.Registry
	Nodes     map[string]graph.Node
	Out       map[string][]graph.Edge      // 按来源节点 ID 分组（From 端点解析），ID 升序
	Entry     string                       // 入口节点：唯一没有入端点的节点
	Endpoints map[string]node.EndpointSpec // 节点 ID → 实例实际启用的端点（同 Validated.Endpoints）
	JoinModes map[string]string            // 节点 ID → 声明的汇合方式（JoinModeDeclarer，即 merge 的 Mode）；未声明的节点不在表中
	Inline    map[string]bool              // 节点 ID → 是否内联节点（Registry.Inline）；活动节点不在表中
	Params    map[string]any               // 节点 ID → 编译期解码并冻结的静态参数结构体（WithParams[T] 的 T）

	plans   map[string][]inputPlan     // 节点 ID → 输入投影计划，按输入名升序
	outputs map[string]node.OutputRule // 节点 ID → 输出规则，执行时校验返回值

	joins map[string]string // fork 出边集合 → 汇合节点，编译期预计算；执行期只读
	in    map[string][]graph.Edge
}

// inputPlan 是一个输入名的取值方式，编译时由 Node.Inputs 生成，执行时不再解析引用。
type inputPlan struct {
	Name  string // 本节点的输入名
	From  string // 来源节点 ID 或 "$input"；为空表示字面量
	Field string // 来源的字段名
	Value any    // 字面量（已按 JSON 解码，整数为 int64）
}

// Compile 调用 node.Registry.ValidateGraph 单次遍历校验图并同步建立执行索引：
// 节点 / 边索引、端点、入口、汇合方式、内联判定、冻结的静态参数、输出规则、输入投影计划与预计算的汇合点。
// 图定义不合法时返回包装 engine.ErrInvalid 的错误。
//
// Compile validates g in a single pass and builds the read-only execution indexes and frozen node parameters.
func Compile(g *graph.Graph, reg *node.Registry) (*Compiled, error) {
	if g == nil {
		return nil, fmt.Errorf("%w: graph required", engine.ErrInvalid)
	}
	v, err := reg.ValidateGraph(*g)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	plans := make(map[string][]inputPlan, len(g.Nodes))
	for _, n := range g.Nodes {
		p, err := planInputs(n)
		if err != nil {
			return nil, fmt.Errorf("%w: node %s: %v", engine.ErrInvalid, n.ID, err)
		}
		if len(p) > 0 {
			plans[n.ID] = p
		}
	}
	in := node.IndexInEdges(g.Edges)
	c := &Compiled{
		Graph:     *g,
		Registry:  reg,
		Nodes:     v.Nodes,
		Out:       v.Out,
		Entry:     v.Entry,
		Endpoints: v.Endpoints,
		JoinModes: v.JoinModes,
		Inline:    v.Inline,
		Params:    v.Params,
		plans:     plans,
		outputs:   v.Outputs,
		in:        in,
		joins:     map[string]string{},
	}
	// 汇合点只由图结构决定：有多条出边的节点在编译期一次算好，执行期 JoinTarget 只查表；
	// 未预计算的出边子集（如非常规分支组合）走纯函数回落计算，不写共享状态。
	for _, outs := range c.Out {
		if len(outs) < 2 {
			continue
		}
		c.joins[joinKey(outs)] = commonJoinTarget(c.JoinModes, c.Endpoints, c.Nodes, c.Out, c.in, outs)
	}
	return c, nil
}

func planInputs(n graph.Node) ([]inputPlan, error) {
	plans := make([]inputPlan, 0, len(n.Inputs))
	for _, name := range slices.Sorted(maps.Keys(n.Inputs)) {
		ref := n.Inputs[name]
		p := inputPlan{Name: name, From: ref.From, Field: ref.Field}
		if ref.From == "" {
			if err := jsonx.DecodeStored(ref.Value, &p.Value); err != nil {
				return nil, fmt.Errorf("input %q value: %v", name, err)
			}
		}
		plans = append(plans, p)
	}
	return plans, nil
}

// project 按投影计划取节点输入：字面量、运行输入（$input）或本路径上已执行节点的输出。
// 来源节点未执行或没有该输出时返回已取到的部分与错误（调用方按节点失败处理，不读到空值）。
func (c *Compiled) project(nodeID string, input map[string]any, outs engine.Outputs) (map[string]any, error) {
	plans := c.plans[nodeID]
	vars := make(map[string]any, len(plans))
	for _, p := range plans {
		switch p.From {
		case "":
			vars[p.Name] = p.Value
		case graph.InputSource:
			vars[p.Name] = input[p.Field]
		default:
			fields, ok := outs[p.From]
			if !ok {
				return vars, fmt.Errorf("input %q unavailable: node %q not executed", p.Name, p.From)
			}
			v, ok := fields[p.Field]
			if !ok {
				return vars, fmt.Errorf("input %q unavailable: node %q has no output %q", p.Name, p.From, p.Field)
			}
			vars[p.Name] = v
		}
	}
	return vars, nil
}

// resolveParams 返回节点运行期的参数结构体：纯静态字面量参数直接返回编译期冻结的 Params[n.ID]，
// 存在动态绑定或调试注入覆盖参数结构体字段时合入运行期值。
func (c *Compiled) resolveParams(n graph.Node, vars map[string]any, inj *engine.DebugInject) any {
	var injected map[string]any
	if inj != nil {
		injected = inj.Vars
	}
	return c.Registry.ResolveParams(n, c.Params[n.ID], vars, injected)
}

// CheckInput 按 Graph.InputSchema 校验运行输入：required 的键必须存在，未在 properties 中声明的键拒绝。
func (c *Compiled) CheckInput(input map[string]any) error {
	declared := map[string]bool{}
	for _, name := range jsonx.PropertyNames(c.Graph.InputSchema) {
		declared[name] = true
	}
	for _, name := range jsonx.RequiredNames(c.Graph.InputSchema) {
		if _, ok := input[name]; !ok {
			return fmt.Errorf("%w: run input %q required", engine.ErrInvalid, name)
		}
	}
	for _, key := range slices.Sorted(maps.Keys(input)) {
		if !declared[key] {
			return fmt.Errorf("%w: run input %q is not declared in Graph.InputSchema", engine.ErrInvalid, key)
		}
	}
	return nil
}

// CheckDebug 校验调试选项（内联节点与活动节点均支持设置断点）。
func (c *Compiled) CheckDebug(d *engine.DebugOptions) error {
	return nil
}

// JoinTarget 返回一组 fork 出边共同到达的最近汇合节点；空表示各分支独立结束。
// 结果只由图结构决定：编译期已预计算的出边集合直接查表，未覆盖的子集走纯函数回落计算，不写共享状态。
func (c *Compiled) JoinTarget(edges []graph.Edge) string {
	if target, ok := c.joins[joinKey(edges)]; ok {
		return target
	}
	return commonJoinTarget(c.JoinModes, c.Endpoints, c.Nodes, c.Out, c.in, edges)
}

// joinKey 是出边集合的缓存键：按出边 ID 顺序拼接，与 fork 传入的 matchingOutEdges 顺序一致。
func joinKey(edges []graph.Edge) string {
	ids := make([]string, len(edges))
	for i, e := range edges {
		ids[i] = e.ID
	}
	return strings.Join(ids, "\x00")
}

// Cache 按图内容缓存 Compiled，供引擎填写 Env.Compile；图版本数量通常很小，不做淘汰。
// 缓存键是每次调用时实时计算的 graph.HashDefinition，不读取也不写入任何存储。并发安全。
//
// Cache memoizes Compile by a content hash computed on each call.
type Cache struct {
	reg *node.Registry
	mu  sync.Mutex
	m   map[string]*Compiled
}

// NewCache 创建使用 reg 编译的缓存。
func NewCache(reg *node.Registry) *Cache {
	return &Cache{reg: reg, m: map[string]*Compiled{}}
}

// Compile 返回 g 的编译结果；内容相同的图（节点、边顺序不同也算相同）只编译一次。签名与 Env.Compile 一致。
func (k *Cache) Compile(g graph.Graph) (*Compiled, error) {
	_, hash, err := graph.HashDefinition(g)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	k.mu.Lock()
	c := k.m[hash]
	k.mu.Unlock()
	if c != nil {
		return c, nil
	}
	c, err = Compile(&g, k.reg)
	if err != nil {
		return nil, err
	}
	k.mu.Lock()
	defer k.mu.Unlock()
	if prev := k.m[hash]; prev != nil {
		return prev, nil
	}
	k.m[hash] = c
	return c, nil
}
