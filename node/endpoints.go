package node

import (
	"fmt"
	"slices"

	"github.com/golang-io/graph"
)

// 默认端点本地名：入边落在 In，普通出边从 Out 出发；其余出口本地名即分支语义标签（Case0..Case15）。
// Default endpoint local names for ordinary nodes.
const (
	DefaultInput  = "In"
	DefaultOutput = "Out"
)

// EndpointSpec 声明节点端点集合（Inputs/Outputs）。
// 在 NodeSpec 上表示种类级固定端点上限（见 Registry.Endpoints）；
// 在 Registry.NodeEndpoints 上表示节点实例实际启用的端点。
// 拓扑校验只读两条属性：Inputs 为空是入口节点，Outputs 为空是终止节点。
// 图不允许成环；遍历用 map 节点的内嵌循环体（子图），多入边的汇合方式见 JoinModeDeclarer。
//
// EndpointSpec declares the input and output endpoints of a node kind or instance.
type EndpointSpec struct {
	Inputs  []string `json:"Inputs,omitempty"`
	Outputs []string `json:"Outputs,omitempty"`
}

// isDefaultOutput 报告本地端点名是否为默认出口。
// isDefaultOutput reports whether ep is the default output endpoint.
func isDefaultOutput(ep string) bool { return ep == DefaultOutput }

// isReturnableOutput 报告 ep 是否允许经 NodeOutput.Endpoint 显式返回：
// 必须是实例已启用的分支出口，不含默认 Out（默认 Out 由空 Endpoint 表达）。
//
// isReturnableOutput reports whether ep may be explicitly returned in NodeOutput.Endpoint.
func (e EndpointSpec) isReturnableOutput(ep string) bool {
	if isDefaultOutput(ep) {
		return false
	}
	return slices.Contains(e.Outputs, ep)
}

// IsTerminal 报告是否为终止节点（无输出端点）。
func (e EndpointSpec) IsTerminal() bool { return len(e.Outputs) == 0 }

// IsEntry 报告是否为入口节点（无输入端点）。
func (e EndpointSpec) IsEntry() bool { return len(e.Inputs) == 0 }

// NodeEndpoints 返回节点实例实际拥有的端点：入口为种类声明的 Inputs；出口为参数声明的启用出口
// （ActiveOutputsDeclarer，如 switch 配置中的分支），未声明时为种类全部 Outputs。
// 拓扑校验（Validate）、画布句柄校验（CleanEndpointSides）与内核推进都按这组端点工作。
// 种类未注册或参数无法解码时返回错误。返回值是副本。
//
// NodeEndpoints returns the endpoints a node instance actually has.
func (r *Registry) NodeEndpoints(n graph.Node) (EndpointSpec, error) {
	spec, cfg, err := r.decodeNode(n)
	if err != nil {
		return EndpointSpec{}, err
	}
	eps, err := spec.resolveEndpoints(n.ID, cfg)
	if err != nil {
		return EndpointSpec{}, err
	}
	return EndpointSpec{
		Inputs:  slices.Clone(eps.Inputs),
		Outputs: slices.Clone(eps.Outputs),
	}, nil
}

// resolveEndpoints 按已解码参数结构体 cfg 算出实例实际启用的端点（见 NodeEndpoints）。
// ActiveOutputs 未声明（返回 nil）时直接复用种类只读切片；否则校验必须为种类出口的子集且无重复。
//
// resolveEndpoints computes the active endpoints for a node instance from its decoded parameter struct.
func (s NodeSpec) resolveEndpoints(nodeID string, cfg any) (EndpointSpec, error) {
	d, ok := cfg.(ActiveOutputsDeclarer)
	if !ok {
		return s.Endpoints, nil
	}
	declared := d.ActiveOutputs()
	if declared == nil {
		return s.Endpoints, nil
	}
	for i, ep := range declared {
		if !slices.Contains(s.Endpoints.Outputs, ep) {
			return EndpointSpec{}, fmt.Errorf("graph: node %q enables unknown output %q", nodeID, ep)
		}
		if slices.Contains(declared[:i], ep) {
			return EndpointSpec{}, fmt.Errorf("graph: node %q enables duplicate output %q", nodeID, ep)
		}
	}
	return EndpointSpec{Inputs: s.Endpoints.Inputs, Outputs: declared}, nil
}

// JoinMode 返回节点实例声明的汇合方式：参数结构体实现 JoinModeDeclarer 时取其返回值（merge 的 Mode），
// 否则为空，表示没有声明；被多条分支汇入时内核按 JoinAll 处理。
// 参数解码失败时返回空，参数本身的错误由 Validate 报告。
func (r *Registry) JoinMode(n graph.Node) string {
	_, cfg, err := r.decodeNode(n)
	if err != nil {
		return ""
	}
	if d, ok := cfg.(JoinModeDeclarer); ok {
		return d.JoinMode()
	}
	return ""
}
