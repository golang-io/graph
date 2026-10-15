package builtin

import (
	"context"

	"github.com/golang-io/graph/node"
)

// Parallel 显式 fan-out。普通节点多条同标签（默认 Out）出边也会分裂 Token。
const Parallel = node.InnerNodePrefix + "parallel"

// ParallelInput 无额外参数；语义与普通节点的多条默认 Out 出边一致。
type ParallelInput struct{}

func (ParallelInput) Validate() error { return nil }

// ParallelNode 实现并行分叉节点。
type ParallelNode struct{}

func (ParallelNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID:          Parallel,
		Name:        "并行",
		Description: "显式展开全部分支；单出口 Out，多条出边即 fork",
		Group:       "control",
		GroupLabel:  "控制",
		Icon:        "split",
		Tone:        "violet",
		Renderer:    "parallel",
		Inline:      true,
		Endpoints: node.EndpointSpec{
			Inputs:  []string{node.DefaultInput},
			Outputs: []string{node.DefaultOutput},
		},
	}, ParallelInput{})
}

func (ParallelNode) Execute(_ context.Context, _ node.NodeInput) (node.NodeOutput, error) {
	// 走默认出口；多条未标记出边由内核分叉。
	return node.NodeOutput{}, nil
}
