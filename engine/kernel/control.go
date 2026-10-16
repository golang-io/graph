package kernel

import (
	"reflect"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
)

// 本文件处理控制事件（Pause / Resume / Cancel）与内核缺陷时的 Fail。
// Pause / Resume 的前提不满足时返回拒绝（reject），此时 Apply 不产生任何改动。

// onPause 设置暂停标记：之后到达活动节点的 Run 进入 WAITING(debug)；执行中的活动节点照常完成。
func (st *state) onPause() error {
	if st.task.Paused {
		return reject(engine.ErrConflict, "task is already paused")
	}
	st.task.Paused = true
	st.touchTask()
	return nil
}

// onResume 恢复暂停或停在调试等待上的 Task（DebugAction 的含义见 engine.ResumeRequest）。
// 先完成全部校验再修改，校验失败时返回拒绝且没有改动：
//   - 未知的 DebugAction 或不合法的调试选项：ErrInvalid；
//   - 没有可放行的 Run 且 Task 未暂停：ErrConflict（普通恢复携带 Debug 时例外，仅替换调试选项）。
//
// 放行的 Run 进入活动节点：前置检查（输入来源、调试注入）按替换后的调试选项进行。
func (st *state) onResume(ev Resume) error {
	req := ev.Request
	action := strings.TrimSpace(req.DebugAction)
	if action != "" && action != engine.DebugActionStep && action != engine.DebugActionContinue {
		return reject(engine.ErrInvalid, "DebugAction %q", req.DebugAction)
	}
	if err := CheckDebug(st.c.reg, st.task.Graph, req.Debug); err != nil {
		return &rejection{err: err}
	}
	t := st.task
	var debug *engine.DebugOptions
	if req.Debug != nil {
		debug, _ = req.Debug.Normalize() // CheckDebug 已校验；比较规整后的值，避免 int / int64 假差异。
	}
	var targets []*engine.Run
	for _, r := range st.list(graph.WAITING) {
		// Controller 的一次访问尚未结束，旧进度必须继续使用原注入（含禁止删除旧注入）。
		// 断点与单步仍可更新；只拒绝会改变进行中访问的注入替换。
		if debug != nil && r.WaitReason == engine.WaitSubgraph &&
			!reflect.DeepEqual(t.Policy.Debug.InjectFor(r.NodeID), debug.InjectFor(r.NodeID)) {
			return reject(engine.ErrConflict, "cannot change debug inject for node %s while waiting for child tasks", r.NodeID)
		}
		if r.WaitReason == engine.WaitDebug && (req.TargetRunID == "" || r.RunID == req.TargetRunID) {
			targets = append(targets, r)
		}
	}
	if len(targets) == 0 && !t.Paused && (action != "" || req.Debug == nil) {
		return reject(engine.ErrConflict, "no run is paused for debugging")
	}
	if action == engine.DebugActionStep {
		targets = targets[:min(1, len(targets))]
	}
	if req.Debug != nil {
		t.Policy.Debug = debug
	}
	switch action {
	case engine.DebugActionStep:
		if t.Policy.Debug == nil {
			t.Policy.Debug = &engine.DebugOptions{}
		}
		t.Policy.Debug.Step = true
	case engine.DebugActionContinue:
		if t.Policy.Debug != nil {
			t.Policy.Debug.Step = false
		}
	}
	t.Paused = false
	st.touchTask()
	for _, r := range targets {
		// 前面的放行可能已让 Task 结束（fail_fast）或经 any 汇合跳过了其余分支。
		if graph.IsTerminal(t.Status) || r.Status != graph.WAITING || r.WaitReason != engine.WaitDebug {
			continue
		}
		r.WaitReason = ""
		if err := st.proceed(r, st.c.node(r.NodeID)); err != nil {
			return err
		}
	}
	return nil
}

// onCancel 取消 Task：未结束的 Run 置为 CANCELED，等待中的子 Task 级联取消，有父 Task 时通知父 Run。
func (st *state) onCancel(ev Cancel) {
	reason := strings.TrimSpace(ev.Reason)
	if reason == "" {
		reason = "canceled"
	}
	st.endTask(graph.CANCELED, reason)
}

// Fail 生成内核缺陷（Apply 返回非拒绝类 error）时的改动：Task 置为 FAILED 并记录错误，
// 所有非终态 Run 置为 CANCELED，输出 CancelChild 与 NotifyParent（设计文档 5.5）。
// 引擎不得保留 RUNNING 状态等待租约过期。Task 已结束时返回空改动。不修改入参。
//
// Fail returns the changes that fail a task after a kernel defect.
func Fail(task *engine.Task, runs []*engine.Run, err error, env Env) (Changes, []Command) {
	if task == nil || graph.IsTerminal(task.Status) {
		return Changes{}, nil
	}
	msg := "kernel error"
	if err != nil {
		msg = "kernel error: " + err.Error()
	}
	st := newState(nil, task, runs, env)
	st.endTask(graph.FAILED, msg)
	return st.finish()
}
