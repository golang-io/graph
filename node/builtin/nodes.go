package builtin

import "github.com/golang-io/graph/node"

// Nodes 返回核心内置节点：内联节点 start、end、switch、merge、parallel、map、subgraph、stop_error、template，
// 以及活动节点 code、reduce。可选节点（HTTPNode、LLMNode，均为活动节点）不在其中，由调用方按需追加注册。
//
// Nodes returns the core built-in nodes; optional nodes (HTTP, LLM) are registered by the caller.
func Nodes() []node.Node {
	return []node.Node{
		TemplateNode{},
		SwitchNode{},
		MergeNode{},
		ParallelNode{},
		MapNode{},
		StopErrorNode{},
		SubGraphNode{},
		CodeNode{},
		ReduceNode{},
		StartNode{},
		EndNode{},
	}
}
