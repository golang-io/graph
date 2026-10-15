package node

import (
	"fmt"
	"maps"
	"slices"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/google/jsonschema-go/jsonschema"
)

// 本文件是注册表的两类校验：保存 / 编译期对整张图的单次遍历校验（Validate / ValidateGraph），
// 以及运行时对节点返回值形态的校验（ValidateOutput）。

// Validated 是图在单次遍历校验过程中同步建立的节点索引、端点、汇合方式、内联标记、输出规则与静态参数。
// Validate 仅返回错误；kernel.Compile 调用 ValidateGraph 直接复用这些产物，不再二次遍历节点或重复解码参数。
//
// Validated holds the indexes and decoded static parameter structs produced during single-pass graph validation.
type Validated struct {
	Nodes     map[string]graph.Node
	Out       map[string][]graph.Edge
	Entry     string
	Endpoints map[string]EndpointSpec // 节点 ID → 实例实际启用的端点（同 NodeEndpoints）
	JoinModes map[string]string
	Inline    map[string]bool
	Params    map[string]any
	Outputs   map[string]OutputRule
	// Subs 为内嵌子图的校验结果：拥有子图的节点 ID → 按 Subgraphs() 顺序的子图 Validated。
	// ValidateLayout 清理子图布局时按序复用，不再解码子图节点。
	Subs map[string][]*Validated
}

// Validate 校验图：图自洽、逐节点参数、控制拓扑、输入引用与表达式标识符，以及参数中内嵌的子图（map 的 Body）。
// 每个节点实例的参数只解码一次。
func (r *Registry) Validate(g graph.Graph) error {
	_, err := r.validate(g, 0)
	return err
}

// ValidateGraph 一次遍历完成图校验与节点静态信息收集，供 kernel.Compile 构建 Compiled 使用。
//
// ValidateGraph validates g in a single pass and returns the prebuilt indexes and decoded static parameters.
func (r *Registry) ValidateGraph(g graph.Graph) (*Validated, error) {
	return r.validate(g, 0)
}

// ValidateLayout 一次解码完成整图校验与画布规整，供保存链路使用：
// 先 ValidateGraph（含全部语义校验），再用同一校验树复用清理布局，顶层与子图节点都不再二次解码或重复计算端点。
// 返回的 Validated 可按需复用，调用方只关心布局时可忽略。
//
// ValidateLayout validates the graph and normalizes the canvas layout in a single pass without re-decoding node params.
func (r *Registry) ValidateLayout(g graph.Graph, l graph.Layout) (*Validated, graph.Layout, error) {
	v, err := r.ValidateGraph(g)
	if err != nil {
		return nil, l, err
	}
	cleaned, err := r.cleanLayout(g, l, v)
	if err != nil {
		return nil, l, err
	}
	return v, cleaned, nil
}

// validate 校验一张图并同步收集节点索引与静态参数；depth 是内嵌子图的层数，超过 graph.MaxSubgraphDepth 时拒绝。
func (r *Registry) validate(g graph.Graph, depth int) (*Validated, error) {
	if depth > graph.MaxSubgraphDepth {
		return nil, fmt.Errorf("graph: nested graphs exceed max depth %d", graph.MaxSubgraphDepth)
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	v := &Validated{
		Nodes:     IndexNodes(g.Nodes),
		Out:       IndexOutEdges(g.Edges),
		Endpoints: make(map[string]EndpointSpec, len(g.Nodes)),
		JoinModes: map[string]string{},
		Inline:    map[string]bool{},
		Params:    make(map[string]any, len(g.Nodes)),
		Subs:      map[string][]*Validated{},
	}
	for _, n := range g.Nodes {
		// 整图校验的单次解码点：后继的语义校验、端点计算、输出规则全部复用同一 cfg。
		spec, cfg, err := r.decodeNode(n)
		if err != nil {
			return nil, err
		}
		if iv, ok := cfg.(InputValidator); ok {
			if err := iv.Validate(); err != nil {
				return nil, fmt.Errorf("graph: node %q kind %q params: %w", n.ID, n.Kind, err)
			}
		}
		// 子图校验树按节点收拢，供 ValidateLayout 复用，子图节点同样只解码一次。
		subs, err := r.validateSubgraphs(n, spec, cfg, depth)
		if err != nil {
			return nil, err
		}
		if len(subs) > 0 {
			v.Subs[n.ID] = subs
		}
		// 出边只能连在实例启用的出口上（switch 为配置中的分支），未声明时按种类全部出口。
		// 实例端点在此一次算出并存入 v.Endpoints：拓扑校验、输出规则、画布布局清理与编译索引共用同一结果。
		eps, err := spec.resolveEndpoints(n.ID, cfg)
		if err != nil {
			return nil, err
		}
		v.Params[n.ID] = cfg
		v.Endpoints[n.ID] = eps
		if d, ok := cfg.(JoinModeDeclarer); ok {
			if mode := d.JoinMode(); mode != "" {
				v.JoinModes[n.ID] = mode
			}
		}
		if r.Inline(n.Kind) {
			v.Inline[n.ID] = true
		}
	}
	entry, err := validateControlTopology(g, v.Endpoints, v.Out)
	if err != nil {
		return nil, err
	}
	v.Entry = entry
	outputs, err := r.checkReferences(g, v.Nodes, v.Out, v.Params, v.Endpoints)
	if err != nil {
		return nil, err
	}
	v.Outputs = outputs
	return v, nil
}

// validateSubgraphs 递归校验节点参数中内嵌的子图，并检查子图的必填输入都能由节点提供：
// 节点固定提供 Subgraph.Provides，另把实例上的自定义输入（不属于参数结构体 InputSchema 的输入名）按同名传入。
// 返回与 subs 入参顺序对应的子图校验树，调用方按节点收拢，供 ValidateLayout 复用。
func (r *Registry) validateSubgraphs(n graph.Node, spec NodeSpec, cfg any, depth int) ([]*Validated, error) {
	d, ok := cfg.(SubgraphDeclarer)
	if !ok {
		return nil, nil
	}
	subs := d.Subgraphs()
	if len(subs) == 0 {
		return nil, nil
	}
	provided := map[string]bool{}
	for name := range n.Inputs {
		if spec.InputSchema == nil || spec.InputSchema.Properties[name] == nil {
			provided[name] = true
		}
	}
	children := make([]*Validated, 0, len(subs))
	for _, sub := range subs {
		sv, err := r.validate(sub.Graph, depth+1)
		if err != nil {
			return nil, fmt.Errorf("graph: node %q %s: %w", n.ID, sub.Path, err)
		}
		for _, name := range jsonx.RequiredNames(sub.Graph.InputSchema) {
			if !provided[name] && !slices.Contains(sub.Provides, name) {
				return nil, fmt.Errorf("graph: node %q %s requires input %q that the node does not provide", n.ID, sub.Path, name)
			}
		}
		children = append(children, sv)
	}
	return children, nil
}

// ValidateOutput 检查 NodeOutput 是否可序列化，并符合四种互斥合法形态之一（见 NodeOutput 注释）。
// 便捷入口：每次调用都现场生成 OutputRule；内核在编译期生成规则，执行时只调用 OutputRule.Check。
func (r *Registry) ValidateOutput(n graph.Node, out NodeOutput) error {
	if _, err := jsonx.Canonical(out.Set); err != nil {
		return fmt.Errorf("graph: node %q output: %w", n.ID, err)
	}
	rule, err := r.OutputRule(n)
	if err != nil {
		return err
	}
	return rule.Check(out)
}

// OutputRule 是节点实例在运行期的输出约束，只由种类描述与实例字面量参数决定。
// kernel.Compile 为每个节点生成一次，执行时不再查询种类描述、不再解码参数。
//
// OutputRule holds the precomputed output constraints of one node instance.
type OutputRule struct {
	nodeID    string
	declared  map[string]bool // 非 Error 出口声明的输出名（含 DeclareOutputs 推导的部分）
	endpoints map[string]bool // 可显式返回的端点：实例启用的种类出口，不含 Error 与 DefaultOutput
}

// OutputRule 为节点实例生成输出规则；种类未注册或参数无法解码时返回错误。
// 参数实现 ActiveOutputsDeclarer 时只允许返回启用的出口（switch 为配置中的分支），否则为种类全部出口。
// 可返回集合与拓扑校验共用同一 resolveEndpoints 结果，不再各自重复解释 ActiveOutputs。
func (r *Registry) OutputRule(n graph.Node) (OutputRule, error) {
	spec, cfg, err := r.decodeNode(n)
	if err != nil {
		return OutputRule{}, err
	}
	eps, err := spec.resolveEndpoints(n.ID, cfg)
	if err != nil {
		return OutputRule{}, err
	}
	outs, err := nodeOutputs(spec, n.ID, cfg)
	if err != nil {
		return OutputRule{}, err
	}
	return newOutputRule(n.ID, eps, outs), nil
}

func newOutputRule(nodeID string, eps EndpointSpec, outs map[string]*jsonschema.Schema) OutputRule {
	rule := OutputRule{nodeID: nodeID, declared: map[string]bool{}, endpoints: map[string]bool{}}
	for ep, schema := range outs {
		if isErrorEndpoint(ep) {
			continue
		}
		for _, name := range jsonx.PropertyNames(schema) {
			rule.declared[name] = true
		}
	}
	for _, ep := range eps.Outputs {
		if eps.isReturnableOutput(ep) {
			rule.endpoints[ep] = true
		}
	}
	return rule
}

// Check 检查返回值形态、Effect、输出键与端点；不检查可序列化（由调用方在规整 Set 时完成）。
func (rule OutputRule) Check(out NodeOutput) error {
	// 互斥形态由 NodeOutput.ValidateShape 统一判定，此处只补节点上下文后查业务规则。
	if err := out.ValidateShape(); err != nil {
		return fmt.Errorf("graph: node %q: %w", rule.nodeID, err)
	}
	for _, key := range slices.Sorted(maps.Keys(out.Set)) {
		if !rule.declared[key] {
			return fmt.Errorf("graph: node %q returned undeclared output %q", rule.nodeID, key)
		}
	}
	if out.Endpoint == "" || rule.endpoints[out.Endpoint] {
		return nil
	}
	return fmt.Errorf("graph: node %q returned undeclared or disabled endpoint %q", rule.nodeID, out.Endpoint)
}
