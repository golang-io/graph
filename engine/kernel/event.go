package kernel

import (
	"fmt"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
)

// 本文件定义 Apply 的输入事件 Event（设计文档 5.2）。
// Apply 的输出（Changes 与 Command）见 command.go；事件与命令是两套封闭类型，互相不能替代。

// Event 是交给 Apply 的一个事件；具体类型见下方各结构体（设计文档 5.2）。
// isEvent 未导出，只有本包的类型能实现 Event：引擎只能投递内核定义的事件，传入命令或其他类型在编译期报错。
//
// Event is one input to Apply; the interface is sealed to this package.
type Event interface{ isEvent() }

// Started 在引擎保存 Task 后投递：创建根 Run，从入口节点执行到第一个活动节点或等待点。
// Task 必须处于 PENDING（由 NewTask 或 StartChild 构造，运行输入已写入 Task.Input）；其余状态下忽略。
type Started struct{}

// ActivityDone 是活动节点执行器返回的结果。
// RunID、Seq（Run 上第几次进入节点）、Attempt（该节点的第几次尝试）共同标识一次活动节点执行；
// Run 已不是 RUNNING，或这三项与 Run 当前值不一致（Run 已离开节点、已重试或已被取消）时忽略。
// Err 非空表示执行失败（含超时、执行器 panic、引擎判定的执行器多次失联）：
// 按 MaxAttempts 重试，耗尽后走错误出口或使 Task 失败。
//
// ActivityDone carries the result of one activity execution of a Run (RunID, Seq, Attempt).
type ActivityDone struct {
	RunID   string
	Seq     int64
	Attempt int
	Output  node.NodeOutput
	Err     error
}

// Current 报告 r 是否仍处在这次执行上：r 非 nil，且 RunID、Seq、Attempt 都一致。不检查 Run 的状态。
func (ev ActivityDone) Current(r *engine.Run) bool {
	return r != nil && r.RunID == ev.RunID && r.Seq == ev.Seq && r.Attempt == ev.Attempt
}

// ChildDone 是子 Task 结束后交回父 Run 的结果。父 Run 不在 WAITING(subgraph)，
// 或等待视图（Env.Awaiting）中该 Slot 指向的不是 ChildTaskID 时忽略，因此重复送达没有副作用。
type ChildDone struct {
	RunID       string
	Slot        string
	ChildTaskID string
	Status      graph.StatusCode // 子 Task 的终态
	Output      map[string]any   // 子 Task 输出（SUCCEEDED 时）
	Error       string           // 子 Task 失败原因（非 SUCCEEDED 时）
}

// Resume 是调试放行：清除暂停，按 Request.DebugAction 放行调试等待（WaitReason=debug）中的 Run，
// Request.Debug 非空时替换 Task 的调试选项（含注入值与断点）。
// 前提不满足时 Apply 返回包装 ErrConflict / ErrInvalid 的错误且没有改动（设计文档 5.5）。
type Resume struct {
	Request engine.ResumeRequest
}

// Pause 是调试暂停：之后到达活动节点的 Run 进入 WAITING(debug)。已暂停时返回 ErrConflict。
type Pause struct{}

// Cancel 取消 Task：所有非终态 Run 置为 CANCELED，等待中的子 Task 级联取消。Reason 为空时为 "canceled"。
type Cancel struct {
	Reason string
}

// SignalEvent 把 Engine.Signal 转为对应的控制事件（Cancel / Pause / Resume），供各引擎共用。
// resume 的 Signal.Resume 为 nil 时按普通恢复处理；种类未知时返回 ErrInvalid。
//
// SignalEvent maps an engine.Signal to the kernel control event.
func SignalEvent(sig engine.Signal) (Event, error) {
	switch sig.Kind {
	case engine.SignalCancel:
		return Cancel{Reason: sig.Reason}, nil
	case engine.SignalPause:
		return Pause{}, nil
	case engine.SignalResume:
		var req engine.ResumeRequest
		if sig.Resume != nil {
			req = *sig.Resume
		}
		return Resume{Request: req}, nil
	default:
		return nil, fmt.Errorf("%w: signal kind %q", engine.ErrInvalid, sig.Kind)
	}
}

func (Started) isEvent()      {}
func (ActivityDone) isEvent() {}
func (ChildDone) isEvent()    {}
func (Resume) isEvent()       {}
func (Pause) isEvent()        {}
func (Cancel) isEvent()       {}
