// Package builtin 提供 graph 的全部内置节点实现，基于 node 包的合同与辅助函数编写。
//
// 核心节点（Nodes 返回，service.NewNodeRegistry 总会装入）：
//   - 控制节点（种类 ID 带 __INNER__. 前缀）：Start、End、Switch、Merge、Parallel、Map、SubGraph、StopError
//   - 活动节点：Template、Code、Reduce
//
// 可选节点（会访问外部系统，需调用方显式注册）：HTTPNode、LLMNode（LLMNode.Invoke 由业务应用注入模型调用）。
//
// 依赖方向：builtin → node → graph；node 包不 import 本包。
//
// Package builtin implements all built-in graph nodes on top of package node.
package builtin
