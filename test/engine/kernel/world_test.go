package kernel_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/test/engine/enginetest"
)

// world 是测试用的内存引擎：保存多个 Task 的状态，按 FIFO 处理内核输出的命令。
// auto 为 true 时 ExecuteActivity 立即用 kernel.NewActivity 同步执行；否则只记下待执行的 Run，
// 由测试用 dispatch / done 手动推进，便于构造并发到达、迟到结果等时序。
// 它只模拟引擎的职责（保存改动、PENDING → RUNNING、投递跨 Task 事件），不实现任何图语义。
type world struct {
	t      *testing.T
	reg    *node.Registry
	cache  *kernel.Cache
	env    kernel.Env
	auto   bool
	graphs map[string]graph.Graph // 按 GraphID 引用的子图（resolve 的数据源）
	// resolve 在 Apply 返回 Changes.NeedGraphs 后、Apply 之外调用（内核本身不加载子图），结果记入 loaded。
	resolve engine.GraphResolver
	loaded  map[string]kernel.LoadedGraph // 已加载的子图，即 env.Graphs
	needs   [][]string                    // 每次 NeedGraphs 报告的 GraphID，供断言加载时机

	tasks map[string]*engine.Task
	runs  map[string]map[string]*engine.Run // TaskID → RunID → Run
	// awaiting 是测试维护的等待关系（TaskID → RunID → Slot → TaskID），模拟引擎的职责：
	// Apply 前作为 Env.Awaiting 提供，Apply 后按 Changes.Released 与 StartChild 维护。
	awaiting map[string]map[string]map[string]string
	comp     map[string]*kernel.Compiled // TaskID → 编译结果
	queue    []queued
	log      []queued // 全部命令（含已处理的），供断言某类命令从未出现
	ids      int
}

type queued struct {
	taskID string
	cmd    kernel.Command
}

var t0 = time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)

func newWorld(t *testing.T, auto bool, extra ...node.Node) *world {
	t.Helper()
	reg, err := enginetest.Registry(append([]node.Node{enginetest.OpSpec()}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{t: t, reg: reg, cache: kernel.NewCache(reg), auto: auto, graphs: map[string]graph.Graph{},
		loaded: map[string]kernel.LoadedGraph{},
		tasks:  map[string]*engine.Task{}, runs: map[string]map[string]*engine.Run{},
		awaiting: map[string]map[string]map[string]string{}, comp: map[string]*kernel.Compiled{}}
	w.env = kernel.Env{
		Now: t0,
		// 递增 ID 保证按 RunID 排序即创建顺序，且两次运行结果可比较。
		NewID:   func() string { w.ids++; return fmt.Sprintf("id%04d", w.ids) },
		Compile: w.cache.Compile,
		Graphs:  w.loaded,
	}
	w.resolve = func(_ context.Context, id string) (graph.Graph, error) {
		g, ok := w.graphs[id]
		if !ok {
			return graph.Graph{}, fmt.Errorf("%w: graph %s", engine.ErrNotFound, id)
		}
		g.GraphID, g.GraphVersion = id, 7
		return g, nil
	}
	return w
}

// start 编译并启动根 Task，返回 TaskID；之后处理命令队列。
func (w *world) start(g graph.Graph, input map[string]any, policy engine.TaskPolicy) string {
	w.t.Helper()
	req := engine.StartRequest{GraphID: "g", Graph: g, Input: input, Policy: policy}
	c, err := w.cache.Compile(req.PinnedGraph())
	if err != nil {
		w.t.Fatal(err)
	}
	task, err := kernel.NewTask(engine.KindImmediate, req, c, w.env)
	if err != nil {
		w.t.Fatal(err)
	}
	w.add(task, c)
	w.must(task.TaskID, kernel.Started{})
	return task.TaskID
}

func (w *world) add(task *engine.Task, c *kernel.Compiled) {
	w.tasks[task.TaskID] = task.Clone()
	w.runs[task.TaskID] = map[string]*engine.Run{}
	w.awaiting[task.TaskID] = map[string]map[string]string{}
	w.comp[task.TaskID] = c
}

// await 记录 parentTaskID 的 Run 开始等待子 Task（模拟引擎执行 StartChild）。
func (w *world) await(parentTaskID, runID, slot, taskID string) {
	view := w.awaiting[parentTaskID]
	if view == nil {
		view = map[string]map[string]string{}
		w.awaiting[parentTaskID] = view
	}
	m := view[runID]
	if m == nil {
		m = make(map[string]string)
		view[runID] = m
	}
	m[slot] = taskID
}

// release 从 parentTaskID 的等待关系中删除这些子 Task（模拟引擎应用 Changes.Released）。
func (w *world) release(parentTaskID string, ids []string) {
	doomed := make(map[string]bool, len(ids))
	for _, id := range ids {
		doomed[id] = true
	}
	for runID, slots := range w.awaiting[parentTaskID] {
		for slot, taskID := range slots {
			if doomed[taskID] {
				delete(slots, slot)
			}
		}
		if len(slots) == 0 {
			delete(w.awaiting[parentTaskID], runID)
		}
	}
}

// awaited 返回 taskID 中 runID 当前等待的子 Task（Slot → TaskID）副本，供测试断言。
func (w *world) awaited(taskID, runID string) map[string]string {
	return maps.Clone(w.awaiting[taskID][runID])
}

// list 返回 Task 的全部 Run（按 RunID 排序），作为 Apply 的 runs 入参。
func (w *world) list(taskID string) []*engine.Run {
	out := make([]*engine.Run, 0, len(w.runs[taskID]))
	for _, r := range w.runs[taskID] {
		out = append(out, r)
	}
	slices.SortFunc(out, func(x, y *engine.Run) int { return strings.Compare(x.RunID, y.RunID) })
	return out
}

// apply 对 Task 应用事件并保存改动；内核缺陷时改为保存 kernel.Fail 的结果（与引擎的处理一致）。
// Apply 报告缺少子图时（Changes.NeedGraphs 非空、状态改动为空），用 engine.LoadGraphs 加载后重新应用同一事件，与引擎的处理一致。
// 返回本次的命令（已入队）与 Apply 的 error。
func (w *world) apply(taskID string, ev kernel.Event) ([]kernel.Command, error) {
	w.env.Awaiting = w.awaiting[taskID]
	ch, cmds, err := kernel.Apply(w.comp[taskID], w.tasks[taskID], w.list(taskID), ev, w.env)
	if len(ch.NeedGraphs) > 0 {
		if err != nil || !ch.Empty() || len(cmds) != 0 {
			w.t.Fatalf("NeedGraphs with changes or err: %+v %+v %v", ch, cmds, err)
		}
		for _, id := range ch.NeedGraphs {
			if _, done := w.loaded[id]; done {
				w.t.Fatalf("NeedGraphs for loaded graph %s", id)
			}
		}
		w.needs = append(w.needs, ch.NeedGraphs)
		if err := engine.LoadGraphs(context.Background(), w.resolve, ch.NeedGraphs, w.loaded); err != nil {
			w.t.Fatal(err)
		}
		return w.apply(taskID, ev)
	}
	if err != nil {
		if kernel.IsRejection(err) {
			return nil, err
		}
		ch, cmds = kernel.Fail(w.tasks[taskID], w.list(taskID), err, w.env)
	}
	// 新发起的等待必须在保存改动前登记：后到的 ChildDone 可能在 drain 处理 StartChild 之前到达，
	// 视图必须已包含新轮次，否则内核会误判为“只返回 State 却无等待”。durable 靠同一事务、
	// immediate 靠处理循环内的同步顺序保证这一点，world 在这里同步登记。
	for _, c := range cmds {
		if sc, ok := c.(kernel.StartChild); ok {
			w.await(sc.Task.ParentTaskID, sc.Task.ParentRunID, sc.Task.Slot, sc.Task.TaskID)
		}
	}
	w.save(taskID, ch)
	for _, c := range cmds {
		w.queue = append(w.queue, queued{taskID: taskID, cmd: c})
		w.log = append(w.log, queued{taskID: taskID, cmd: c})
	}
	w.drain()
	return cmds, err
}

// must 应用事件并要求没有 error。
func (w *world) must(taskID string, ev kernel.Event) []kernel.Command {
	w.t.Helper()
	cmds, err := w.apply(taskID, ev)
	if err != nil {
		w.t.Fatalf("apply %T: %v", ev, err)
	}
	return cmds
}

// call 不保存改动，直接调用一次 Apply（用于断言忽略、拒绝等场景）；等待视图按该 Task 提供，
// 与 apply 一致，内核只读 Env.Awaiting。
func (w *world) call(taskID string, ev kernel.Event) (kernel.Changes, []kernel.Command, error) {
	w.t.Helper()
	w.env.Awaiting = w.awaiting[taskID]
	return kernel.Apply(w.comp[taskID], w.tasks[taskID], w.list(taskID), ev, w.env)
}

func (w *world) save(taskID string, ch kernel.Changes) {
	if ch.Task != nil {
		w.tasks[taskID] = ch.Task
	}
	for _, r := range ch.Runs {
		w.runs[taskID][r.RunID] = r
	}
	w.release(taskID, ch.Released)
}

// drain 按 FIFO 处理命令；非 auto 模式下 ExecuteActivity 留给测试手动推进。
func (w *world) drain() {
	for len(w.queue) > 0 {
		q := w.queue[0]
		w.queue = w.queue[1:]
		switch cmd := q.cmd.(type) {
		case kernel.StartChild:
			w.add(cmd.Task, cmd.Compiled)
			w.must(cmd.Task.TaskID, kernel.Started{})
		case kernel.NotifyParent:
			if _, ok := w.tasks[cmd.ParentTaskID]; ok {
				w.must(cmd.ParentTaskID, cmd.Event)
			}
		case kernel.CancelChild:
			if _, ok := w.tasks[cmd.ChildTaskID]; ok {
				w.must(cmd.ChildTaskID, kernel.Cancel{Reason: kernel.CancelChildReason})
			}
		case kernel.ExecuteActivity:
			if w.auto {
				w.must(q.taskID, w.run(q.taskID, cmd.RunID))
			}
		}
	}
}

// dispatch 模拟引擎派发：Run 由 PENDING 置为 RUNNING（只有引擎做这一步，设计决定第 13 条）。
func (w *world) dispatch(taskID, runID string) *engine.Run {
	w.t.Helper()
	r := w.runs[taskID][runID]
	if r == nil || r.Status != graph.PENDING {
		w.t.Fatalf("dispatch %s: run not pending: %+v", runID, r)
	}
	r = r.Clone()
	r.Status = graph.RUNNING
	w.runs[taskID][runID] = r
	return r
}

// run 派发并同步执行活动节点，返回 ActivityDone。
func (w *world) run(taskID, runID string) kernel.ActivityDone {
	w.t.Helper()
	r := w.dispatch(taskID, runID)
	act, err := kernel.NewActivity(w.comp[taskID], w.tasks[taskID], r)
	if err != nil {
		w.t.Fatal(err)
	}
	return act.Done(act.Run(context.Background()))
}

// at 返回 Task 中位于 nodeID、状态为 status 的唯一 Run。
func (w *world) at(taskID, nodeID string, status graph.StatusCode) *engine.Run {
	w.t.Helper()
	var found *engine.Run
	for _, r := range w.list(taskID) {
		if r.NodeID == nodeID && r.Status == status {
			if found != nil {
				w.t.Fatalf("more than one %s run at %s", status, nodeID)
			}
			found = r
		}
	}
	if found == nil {
		w.t.Fatalf("no %s run at %s:%s", status, nodeID, w.dump(taskID))
	}
	return found
}

// done 构造与 Run 当前 Seq、Attempt 一致的 ActivityDone。
func done(r *engine.Run, set map[string]any, err error) kernel.ActivityDone {
	return kernel.ActivityDone{RunID: r.RunID, Seq: r.Seq, Attempt: r.Attempt, Output: node.NodeOutput{Set: set}, Err: err}
}

func (w *world) task(taskID string) *engine.Task { return w.tasks[taskID] }

func (w *world) dump(taskID string) string {
	var b strings.Builder
	for _, r := range w.list(taskID) {
		fmt.Fprintf(&b, "\n  %s parent=%s node=%s %s/%s seq=%d attempt=%d err=%q",
			r.RunID, r.ParentRunID, r.NodeID, r.Status, r.WaitReason, r.Seq, r.Attempt, r.Error)
	}
	return b.String()
}

// countCmds 统计命令中某种类型的个数。
func countCmds[T kernel.Command](cmds []kernel.Command) int {
	n := 0
	for _, c := range cmds {
		if _, ok := c.(T); ok {
			n++
		}
	}
	return n
}
