package node

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
)

// Registry 是实例级节点注册表：保存种类实现（Node）与 Register 时固定的种类描述。
// 注册信息在 Register 时冻结：只调用一次 Spec()，之后 Spec() 返回值的变化不再生效；
// 需要改变节点描述时应构造新的 Registry 并重建引擎。
// Register 只允许在启动装配期、尚未并发读之前调用；装配完成后只读，
// 多引擎 / HTTP 并发 Get、List、Lookup 不需要锁。
// 构造请用 NewRegistry；本包不预装任何节点，内置节点见 node/builtin，常规装配入口是 service.NewNodeRegistry。
//
// 勿与 service.EngineRegistry（引擎表）混淆。
type Registry struct {
	nodes map[string]Node     // 种类实现：执行时调用其 Execute / Control
	specs map[string]NodeSpec // Register 时调用一次 Spec() 的结果；包内只读，对外经 cloneNodeSpec 返回副本
}

// NewRegistry 创建注册表并按顺序注册 nodes；任一节点非法或 ID 重复时返回该错误。
// 不预装任何节点：内置节点由 node/builtin 提供，service.NewNodeRegistry 负责组装内置节点与业务节点。
//
// NewRegistry builds a registry holding exactly the given nodes.
func NewRegistry(nodes ...Node) (*Registry, error) {
	r := &Registry{nodes: make(map[string]Node, len(nodes)), specs: make(map[string]NodeSpec, len(nodes))}
	for _, n := range nodes {
		if err := r.Register(n); err != nil {
			return nil, err
		}
	}
	return r, nil
}

// Register 按 Spec().ID 唯一注册。重复 ID 不会静默替换。
// 必须实现 Executor 或 Controller（可兼有）。
// 只在启动装配期调用；与后续并发读之间必须有 happens-before。
func (r *Registry) Register(t Node) error {
	_, isExec := t.(Executor)
	_, isCtrl := t.(Controller)
	if !isExec && !isCtrl {
		return fmt.Errorf("graph: executor or controller required")
	}
	spec := cloneNodeSpec(t.Spec())
	if err := validateNodeSpec(spec); err != nil {
		return err
	}
	if _, exists := r.nodes[spec.ID]; exists {
		return fmt.Errorf("graph: node %s already registered", spec.ID)
	}
	r.nodes[spec.ID] = t
	r.specs[spec.ID] = spec
	return nil
}

// spec 返回 Register 时固定的种类描述；未注册时 ok=false。
// 返回值与注册表共享内部切片、map 与 Schema，包内只读，不得修改或交给包外。
func (r *Registry) spec(id string) (NodeSpec, bool) {
	spec, ok := r.specs[id]
	return spec, ok
}

// decodeNode 按节点实例一次查出种类描述并解码字面量参数，返回冻结的静态参数 cfg。
// NodeEndpoints、JoinMode、Outputs、OutputRule、CleanEndpointSides 与整图校验共用同一入口，
// 错误文案在此统一，调用方不再各自拼接“未注册 / 参数”错误。
func (r *Registry) decodeNode(n graph.Node) (NodeSpec, any, error) {
	spec, ok := r.spec(n.Kind)
	if !ok {
		return NodeSpec{}, nil, fmt.Errorf("graph: node %q kind %q is not registered", n.ID, n.Kind)
	}
	cfg, err := spec.decodeParams(n.Inputs)
	if err != nil {
		return NodeSpec{}, nil, fmt.Errorf("graph: node %q kind %q params: %w", n.ID, n.Kind, err)
	}
	return spec, cfg, nil
}

func validateNodeSpec(spec NodeSpec) error {
	if !graph.ValidID(spec.ID) {
		return fmt.Errorf("graph: node id required")
	}
	if strings.TrimSpace(spec.Name) == "" {
		return fmt.Errorf("graph: node %s requires name", spec.ID)
	}
	if spec.paramsDecode == nil {
		return fmt.Errorf("graph: node %s requires params (use WithParams)", spec.ID)
	}
	if len(spec.Endpoints.Inputs) == 0 && len(spec.Endpoints.Outputs) == 0 {
		return fmt.Errorf("graph: node %s requires endpoints", spec.ID)
	}
	eps := map[string]bool{}
	for _, id := range slices.Concat(spec.Endpoints.Inputs, spec.Endpoints.Outputs) {
		if !graph.ValidID(id) || id != strings.TrimSpace(id) || eps[id] {
			return fmt.Errorf("graph: node %s has invalid or duplicate endpoint %q", spec.ID, id)
		}
		eps[id] = true
		if slices.Contains(spec.Endpoints.Outputs, id) && canvasHandle(id) {
			return fmt.Errorf("graph: node %s output %q collides with a canvas handle", spec.ID, id)
		}
	}
	if err := validateSpecFields(spec); err != nil {
		return fmt.Errorf("graph: node %s: %w", spec.ID, err)
	}
	presets := map[string]bool{}
	for _, preset := range spec.Presets {
		if !graph.ValidID(preset.ID) || strings.TrimSpace(preset.Label) == "" || presets[preset.ID] {
			return fmt.Errorf("graph: node %s has invalid preset", spec.ID)
		}
		presets[preset.ID] = true
		for name := range preset.Inputs {
			if !graph.ValidName(name) {
				return fmt.Errorf("graph: node %s preset %s: invalid input %q", spec.ID, preset.ID, name)
			}
		}
	}
	return nil
}

// validateSpecFields 校验 InputSchema 与 OutputsByEndpoint（出口键与载荷属性名）。
func validateSpecFields(spec NodeSpec) error {
	if spec.InputSchema != nil {
		for name := range spec.InputSchema.Properties {
			if !graph.ValidName(name) {
				return fmt.Errorf("invalid input property %q", name)
			}
		}
		for _, name := range spec.InputSchema.Required {
			if !graph.ValidName(name) {
				return fmt.Errorf("invalid required input %q", name)
			}
		}
	}
	for name := range spec.DefaultInputs {
		if !graph.ValidName(name) {
			return fmt.Errorf("invalid default input %q", name)
		}
	}
	if _, err := mergeOutputsByEndpoint(spec.OutputsByEndpoint); err != nil {
		return err
	}
	for ep, schema := range spec.OutputsByEndpoint {
		if ep != EndpointPayloadDefault && !graph.ValidID(ep) {
			return fmt.Errorf("invalid OutputsByEndpoint key %q", ep)
		}
		if schema == nil {
			continue
		}
		for name := range schema.Properties {
			if !graph.ValidName(name) {
				return fmt.Errorf("invalid output property %q on endpoint %q", name, ep)
			}
		}
	}
	return nil
}

// Get 返回该种类 ID 的安全副本；图里 nodes[].type 用同一把钥匙。
func (r *Registry) Get(id string) (NodeSpec, bool) {
	spec, ok := r.spec(id)
	if !ok {
		return NodeSpec{}, false
	}
	return cloneNodeSpec(spec), true
}

// Lookup 返回该种类 ID 的注册实体（Spec；另实现 Executor 和/或 Controller）。
func (r *Registry) Lookup(id string) (Node, bool) {
	t, ok := r.nodes[id]
	return t, ok
}

// ExecutorOf 返回该种类 ID 的 Executor；未实现 Execute 时 ok=false。
func (r *Registry) ExecutorOf(id string) (Executor, bool) {
	t, ok := r.Lookup(id)
	if !ok {
		return nil, false
	}
	e, ok := t.(Executor)
	return e, ok
}

// Inline 报告该种类 ID 是否为内联节点：实现了 Controller，或 Spec().Inline 为 true。
// 内联节点由内核在处理事件时同步执行；其余 Executor 为活动节点，由引擎在内核之外并发执行。
// 未注册的种类返回 false（图校验已拒绝未注册种类，执行路径不会遇到）。
//
// Inline reports whether the kind runs inline inside the kernel.
func (r *Registry) Inline(id string) bool {
	if _, isCtrl := r.nodes[id].(Controller); isCtrl {
		return true
	}
	return r.specs[id].Inline
}

// List 返回稳定排序的注册表副本，可直接用于只读 HTTP API。
func (r *Registry) List() []NodeSpec {
	out := make([]NodeSpec, 0, len(r.specs))
	for _, spec := range r.specs {
		out = append(out, cloneNodeSpec(spec))
	}
	slices.SortFunc(out, func(a, b NodeSpec) int {
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func cloneNodeSpec(spec NodeSpec) NodeSpec {
	// paramsDecode 函数指针按值复制即可（闭包无可变状态）。
	spec.DefaultInputs = cloneInputRefs(spec.DefaultInputs)
	spec.InputSchema = jsonx.CloneSchema(spec.InputSchema)
	spec.Endpoints.Inputs = slices.Clone(spec.Endpoints.Inputs)
	spec.Endpoints.Outputs = slices.Clone(spec.Endpoints.Outputs)
	spec.Presets = slices.Clone(spec.Presets)
	for i := range spec.Presets {
		spec.Presets[i].Inputs = cloneInputRefs(spec.Presets[i].Inputs)
	}
	spec.OutputsByEndpoint = cloneOutputsByEndpoint(spec.OutputsByEndpoint)
	return spec
}

// cloneInputRefs 深拷贝输入表：map 与每个字面量 Value 的字节切片都复制一份。
func cloneInputRefs(in map[string]graph.InputRef) map[string]graph.InputRef {
	out := maps.Clone(in)
	for k, ref := range out {
		ref.Value = append(json.RawMessage(nil), ref.Value...)
		out[k] = ref
	}
	return out
}
