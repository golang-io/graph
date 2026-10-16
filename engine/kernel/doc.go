// Package kernel 是所有执行引擎共用的执行语义：Apply 把一个事件应用到
// 一个 Task 的当前状态上，返回状态改动（Changes）与需要引擎执行的命令（Command）。
//
// 内核是纯函数：不打开数据库连接、不调用 gorm 与 database/sql、不读时钟（时间由 Env.Now 提供）；
// 不做 IO，不修改入参，相同输入得到相同输出。
// 内联节点（node.Registry.Inline）在 Apply 内同步执行；活动节点以 ExecuteActivity 命令交给引擎，
// 引擎用 NewActivity / Activity.Run 在 Apply 之外并发执行，结果以 ActivityDone 交回。
// 按 GraphID 引用的子图由引擎在 Apply 之外加载后放入 Env.Graphs：缺少时 Apply 在 Changes.NeedGraphs 中返回缺少的 GraphID、
// 没有状态改动且 error 为 nil，引擎加载后重新调用，因此引擎在写回事务内调用 Apply 时不会访问外部资源。
// 调度相关的状态（领取、租约、执行器失联）属于引擎，不进入内核。
//
// 汇合、重试、子任务、取消、调试等语义只在本包实现，引擎只负责调度、并发与持久化
// （设计文档 docs/design/engine-architecture-20261010.md 第 5 节）。第三方引擎应复用本包，
// 而不是自行实现这些语义。
//
// Package kernel implements the execution semantics shared by all engines as pure functions.
package kernel
