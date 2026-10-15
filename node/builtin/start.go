package builtin

import (
	"context"

	"github.com/golang-io/graph/node"
)

// Start 入口节点：无入端点，图必须有且仅有一个入口。
const Start = node.InnerNodePrefix + "start"

// StartInput 没有参数项：开始节点只承担控制入口，不声明数据输入/输出。
type StartInput struct{}

// StartNode 实现入口节点。
type StartNode struct{}

func (StartNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID:          Start,
		Name:        "开始",
		Description: "图的控制入口",
		Group:       "control",
		GroupLabel:  "控制",
		Icon:        "play",
		Tone:        "cyan",
		Renderer:    "start",
		Inline:      true,
		Endpoints:   node.EndpointSpec{Outputs: []string{node.DefaultOutput}},
	}, StartInput{})
}

func (StartNode) Execute(_ context.Context, _ node.NodeInput) (node.NodeOutput, error) {
	return node.NodeOutput{}, nil
}
