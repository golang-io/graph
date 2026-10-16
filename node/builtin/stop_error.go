package builtin

import (
	"context"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// StopError 主动失败节点。
const StopError = node.InnerNodePrefix + "stop_error"

// StopErrorInput 主动失败；Message 支持 {{expr}}，只能使用本节点输入名。
type StopErrorInput struct {
	Message string `json:"Message,omitempty" title:"错误消息"`
}

func (c StopErrorInput) Expressions() []node.Expression {
	return []node.Expression{{Path: "Message", Text: c.Message, Template: true}}
}

// StopErrorNode 实现主动失败节点。
type StopErrorNode struct{}

func (StopErrorNode) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID:          StopError,
		Name:        "停止并报错",
		Description: "主动终止当前任务并报错",
		Group:       "control",
		GroupLabel:  "控制",
		Icon:        "circle-x",
		Tone:        "red",
		Renderer:    "stop_error",
		Inline:      true,
		// 无出端点：进入后直接以错误消息终止任务。
		Endpoints: node.EndpointSpec{Inputs: []string{node.DefaultInput}},
	}, StopErrorInput{Message: "stopped"})
}

func (StopErrorNode) Execute(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
	cfg, err := node.Params[StopErrorInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	msg := strings.TrimSpace(graph.RenderTemplate(cfg.Message, in.Vars))
	if msg == "" {
		msg = "stopped"
	}
	return node.Fail(msg), nil
}
