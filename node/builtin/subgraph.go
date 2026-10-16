package builtin

import (
	"context"
	"fmt"
	"maps"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
)

// SubGraph 调用另一图。
const SubGraph = node.InnerNodePrefix + "subgraph"

// SubGraphOutput 是子图节点写入 Run.Outputs 的结构；Spec.OutputsByEndpoint 由 OutputFrom 推导。
type SubGraphOutput struct {
	Output  map[string]any `json:"Output"`
	GraphID string         `json:"GraphID"`
}

// 与 SubGraphOutput 的 json 标签一致，供下游 InputRef.Field 引用。
const (
	SubGraphOutputField  = "Output"
	SubGraphGraphIDField = "GraphID"
)

// SubGraphInput 是子图节点的输入。
type SubGraphInput struct {
	GraphID string `json:"GraphID" title:"子图"`
}

func (c SubGraphInput) Validate() error {
	if strings.TrimSpace(c.GraphID) == "" {
		return fmt.Errorf("GraphID required")
	}
	return nil
}

func (c SubGraphInput) Expressions() []node.Expression {
	return []node.Expression{{Path: "GraphID", Text: c.GraphID, Template: true}}
}

// SubGraphNode 实现子图节点。
type SubGraphNode struct{}

func (SubGraphNode) Spec() node.NodeSpec {
	return node.WithOutput[SubGraphOutput](node.WithParams(node.NodeSpec{
		ID:          SubGraph,
		Name:        "子图",
		Description: "调用已保存子图",
		Group:       "control",
		GroupLabel:  "控制",
		Icon:        "network",
		Tone:        "cyan",
		Renderer:    "subgraph",
		Endpoints:   node.DefaultEndpoints(),
	}, SubGraphInput{}))
}

func (SubGraphNode) Control(_ context.Context, in node.ControlInput) (node.NodeOutput, error) {
	cfg, err := node.Params[SubGraphInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	// 启动与回填使用同一组 Vars，渲染出的 GraphID 相同；子图解析器按该 ID 读取已保存版本。
	gid := strings.TrimSpace(graph.RenderTemplate(cfg.GraphID, in.Vars))
	switch in.Event.Kind {
	case "", node.EventStart:
		if gid == "" {
			return node.NodeOutput{}, fmt.Errorf("subgraph: GraphID required")
		}
		// 子图运行输入只含数据槽；GraphID 等本节点参数不得泄漏进子任务 Input。
		input := maps.Clone(in.Vars)
		delete(input, "GraphID")
		return node.SpawnChildren([]node.Spawn{{GraphID: gid, Input: input}}, nil), nil
	case node.EventChildDone:
		if in.Event.Error != "" {
			return node.NodeOutput{}, fmt.Errorf("subgraph: %s", in.Event.Error)
		}
		// 内核只给子任务输出（Event.Payload）；本节点输出结构由 SubGraphOutput 决定。
		output := in.Event.Payload
		if output == nil {
			output = map[string]any{}
		}
		return node.Continue(map[string]any{SubGraphOutputField: output, SubGraphGraphIDField: gid}, ""), nil
	default:
		return node.NodeOutput{}, fmt.Errorf("subgraph: unexpected event %q", in.Event.Kind)
	}
}
