package kernel

import (
	"time"

	"github.com/golang-io/graph/engine"
)

// 本文件定义 Apply 的输出：状态改动 Changes 与命令 Command（设计文档 5.3）。
// Apply 的输入事件见 event.go；事件与命令是两套封闭类型，互相不能替代。

// Command 是 Apply 要求引擎在保存改动之后执行的动作（设计文档 5.3）；具体类型见下方各结构体。
//
// Command is an action the engine performs after persisting Changes.
type Command interface{ isCommand() }

// ExecuteActivity 要求引擎在 NotBefore 之后执行该 Run 所在的活动节点（Run 已是 PENDING + AvailableAt）。
// 引擎用 NewActivity 准备调用，派发前把 Run 置为 RUNNING；执行结果以 ActivityDone（同一 RunID、Seq、Attempt）交回。
type ExecuteActivity struct {
	RunID     string
	Seq       int64
	Attempt   int
	NotBefore time.Time
}

// Current 报告 r 是否仍处在这次执行上：r 非 nil，且 RunID、Seq、Attempt 都一致。不检查 Run 的状态。
func (c ExecuteActivity) Current(r *engine.Run) bool {
	return r != nil && r.RunID == c.RunID && r.Seq == c.Seq && r.Attempt == c.Attempt
}

// StartChild 要求引擎创建子 Task 并对它应用 Started{}。Task 为 PENDING 状态，已填好父子关系、
// 图快照与校验规整后的运行输入；Compiled 是子 Task 的编译结果，引擎可直接使用。
// 创建的同时引擎必须记下这条等待（durable 置子 Task 的 Awaited=true，immediate 记入内存等待关系），
// 且与本次改动的保存原子、早于任何后续事件的 Apply：后到的 ChildDone 可能在子 Task 启动前到达，
// 等待视图必须已包含新子 Task，否则内核会误判为“只返回 State 却无等待”。
// durable 靠同一事务保证，immediate 靠处理循环内先登记、再启动的同步顺序保证。
type StartChild struct {
	Task     *engine.Task
	Compiled *Compiled
}

// CancelChild 要求引擎向子 Task 投递 Cancel{Reason: CancelChildReason}（父 Run 离开节点、Task 结束或取消时产生）。
type CancelChild struct {
	ChildTaskID string
}

// CancelChildReason 是引擎处理 CancelChild 时投递给子 Task 的 Cancel.Reason。
const CancelChildReason = "parent task canceled the child"

// NotifyParent 要求引擎向父 Task 投递 ChildDone（本 Task 是子 Task 且已进入终态时产生）。
type NotifyParent struct {
	ParentTaskID string
	Event        ChildDone
}

func (ExecuteActivity) isCommand() {}
func (StartChild) isCommand()      {}
func (CancelChild) isCommand()     {}
func (NotifyParent) isCommand()    {}

// Changes 是一次 Apply 的状态改动，由引擎原样保存。
// 只要有改动（Task、任一 Run 或等待关系），Task 就非 nil，且 Version 已在入参基础上加 1；
// Task 为 nil 表示没有任何改动。
//
// Changes lists the modified Task and Runs; the engine persists them as-is.
type Changes struct {
	Task *engine.Task  // 修改后的 Task；nil 表示本次没有任何改动
	Runs []*engine.Run // 新增或修改的 Run，按首次修改顺序（按 RunID 覆盖保存）
	// Released 是本次不再等待的子 TaskID：子 Task 结果被接收（ChildDone），或父 Run 离开节点、
	// Task 结束时放弃等待。引擎清除这些子 Task 的等待标记（durable 在同一事务内置 Awaited=false，
	// immediate 从内存等待关系中删除），与保存 Task / Run 原子进行；重复清除无副作用。
	// Released lists child tasks no longer awaited; clearing them is idempotent.
	Released []string
	// NeedGraphs 是本次需要引擎在 Apply 之外按 GraphID 加载的子图列表。
	// 非空时本次没有任何状态改动（Empty() 为 true）且不输出任何命令；
	// 引擎把加载结果（含加载错误）放入 Env.Graphs 后，对同一事件重新调用 Apply。
	// NeedGraphs lists subgraph IDs the engine must load outside Apply before retrying the same event.
	NeedGraphs []string
}

// Empty 报告本次是否没有任何状态改动（Task、Runs、Released 均为空）。
// NeedGraphs 非空时本次同样没有任何状态改动，Empty 返回 true。
func (c Changes) Empty() bool { return c.Task == nil && len(c.Runs) == 0 && len(c.Released) == 0 }
