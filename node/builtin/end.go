package builtin

import (
	"context"
	"maps"

	"github.com/golang-io/graph/node"
)

// End 收口节点：无出端点，输入即运行输出。
const End = node.InnerNodePrefix + "end"

// EndInput 没有专用参数：运行输出由节点的 Inputs 决定，输入名即运行输出的键。
type EndInput struct{}

// EndNode 实现终止节点。
type EndNode struct{}

func (EndNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID:          End,
		Name:        "结束",
		Description: "输入即图的最终输出",
		Group:       "control",
		GroupLabel:  "控制",
		Icon:        "circle-check",
		Tone:        "green",
		Renderer:    "end",
		Inline:      true,
		Endpoints:   node.EndpointSpec{Inputs: []string{node.DefaultInput}},
	}, EndInput{})
}

func (EndNode) Execute(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
	out := maps.Clone(in.Vars)
	if out == nil {
		out = map[string]any{}
	}
	return node.Finish(out), nil
}
