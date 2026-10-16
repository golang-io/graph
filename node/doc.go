// Package node 定义节点合同与注册表框架，不含任何具体节点。
//
// 合同（本包）：
//   - Node：Spec()（种类实现）；执行入口为 Executor 或 Controller
//   - NodeSpec / NodeInput / NodeOutput / Registry
//   - 拓扑（无环）、数据绑定与内嵌子图校验（Registry.Validate）、WithParams（InputSchema 由参数结构体标签生成）
//   - 节点实现可复用的辅助：WithOutput / OutputMap（输出结构体 ↔ 输出 Schema / Set）、Params、
//     DefaultEndpoints
//
// 内置节点（start、end、switch、merge、parallel、map、subgraph、stop_error、template、code、reduce、http、llm）
// 在子包 node/builtin；本包不 import builtin，依赖方向为 builtin → node。
//
// 图模型在 graph 包：graph.Node 是落图实例（含 Inputs 引用表），与本包 Node 接口不同。
// Registry.Validate 单次遍历完成全图校验与节点静态参数解码；状态机推进在 engine/kernel（kernel.Apply），本包不做推进。
//
// 依赖：node → graph；graph 永不 import node。
// 装配在 service.NewNodeRegistry：builtin.Nodes() 加业务节点，一并传给 NewRegistry。
//
// Package node defines the node contract and registry framework; built-in nodes live in node/builtin.
//
// 命名对照：
//
//	graph.Node           图上的节点实例
//	node.Node            种类实现（Spec）
//	node.Registry        节点注册表
//	builtin.MapNode 等   内置节点实现
//	service.EngineRegistry  引擎表（immediate/durable），不是节点注册表
package node
