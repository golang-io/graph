package immediate

import (
	"context"
	"fmt"
	"maps"
	"strings"
	"sync"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
)

// task 是一个执行中（或已结束、仍在内存中保留）的 Task，由一个处理循环（loop）驱动（设计文档 9.2）。
//
// 并发约定：
//   - 处理循环是 t、runs 的唯一写入者，逐个处理 mailbox 中的事件：调用 kernel.Apply、保存改动、执行命令、派发活动节点；
//   - 处理循环读取自己的状态不加锁；写入时持有 mu，Snapshot / Watch 等读取方持有 mu 复制；
//   - 活动节点在独立 goroutine 中执行，结果以 ActivityDone 投递回 mailbox；
//   - ready、running、cancels、timer、notify、graphs、awaiting 只由处理循环访问；queued、granted 由 slots.mu 保护。
//
// task is one task's in-memory state, owned by its processing loop.
type task struct {
	e   *Engine
	id  string
	c   *kernel.Compiled
	box *mailbox

	mu      sync.Mutex
	t       *engine.Task
	runs    map[string]*engine.Run
	changed chan struct{} // 每次状态变化时关闭并替换，供 Watch 等待

	ready   map[string]kernel.ExecuteActivity // 待派发的活动节点（RunID → 命令）
	running int                               // 本 Task 执行中的活动节点数（含已取消但尚未返回的）
	cancels map[string]context.CancelFunc     // RunID → 执行中节点的取消函数
	timer   *time.Timer                       // 最早一个未到期重试的唤醒定时器
	notify  []kernel.NotifyParent             // Task 结束时、写库之后才投递给父 Task
	graphs  map[string]kernel.LoadedGraph     // 已加载的子图（GraphID → 加载结果），作为 Env.Graphs 传给 Apply
	// awaiting 是内存中的等待关系（RunID → Slot → 子 TaskID），作为 Env.Awaiting 传给 Apply；
	// 内核只读它，增减由保存 Changes.Released 与执行 StartChild 时维护。
	// durable 侧以子 Task 行的 Awaited 落库实现同一关系，见 engine/durable。
	awaiting map[string]map[string]string

	queued  bool // 在 slots 等待队列中
	granted int  // slots 已交给本 Task、尚未使用的名额

	ctx    context.Context // Task 结束时取消，中断仍在执行的活动节点
	cancel context.CancelFunc
}

func (e *Engine) newTask(t *engine.Task, c *kernel.Compiled) *task {
	ctx, cancel := context.WithCancel(context.Background())
	return &task{
		e: e, id: t.TaskID, c: c, box: newMailbox(),
		t: t, runs: map[string]*engine.Run{}, changed: make(chan struct{}),
		ready: map[string]kernel.ExecuteActivity{}, cancels: map[string]context.CancelFunc{},
		graphs: map[string]kernel.LoadedGraph{}, awaiting: map[string]map[string]string{},
		ctx: ctx, cancel: cancel,
	}
}

// loop 是处理循环：取出全部消息逐个应用 → Task 已结束则收尾退出 → 否则派发活动节点 → 等待下一次唤醒。
func (t *task) loop() {
	defer t.e.wg.Done()
	for {
		for _, m := range t.box.take() {
			t.handle(m)
		}
		if graph.IsTerminal(t.t.Status) {
			t.finalize()
			return
		}
		t.dispatch()
		<-t.box.signal
	}
}

// call 投递一个需要回执的事件并等待处理结果；Task 已结束（mailbox 已关闭）时返回当前状态的回执。
func (t *task) call(ctx context.Context, ev kernel.Event) (engine.Receipt, error) {
	ch := make(chan reply, 1)
	if !t.box.put(message{ev: ev, reply: ch}) {
		return t.receipt(), nil
	}
	select {
	case r := <-ch:
		return r.rec, r.err
	case <-ctx.Done():
		return engine.Receipt{}, ctx.Err()
	}
}

// handle 处理一条消息：活动节点的结果先结算执行计数，然后应用事件并回复调用方。
// ActivityDone 只由本 Task 的 launch 投递，因此这里的 running-- 与 launch 中的 running++ 一一对应；
// 加载子图后重新投递的消息（m.graphs 非空）已在首次投递时结算过，只并入子图后重新应用。
// Apply 缺少子图时不回复调用方，由 load 在后台加载后重新投递同一条消息，届时再回复。
func (t *task) handle(m message) {
	if m.fail != nil {
		t.fail(m.fail)
		if m.reply != nil {
			m.reply <- reply{rec: engine.TaskReceipt(t.t)}
		}
		return
	}
	if m.graphs != nil {
		maps.Copy(t.graphs, m.graphs)
	} else if done, ok := m.ev.(kernel.ActivityDone); ok {
		t.running--
		if cancel := t.cancels[done.RunID]; cancel != nil {
			cancel()
			delete(t.cancels, done.RunID)
		}
	}
	need, err := t.apply(m.ev)
	if len(need) > 0 {
		t.load(need, m)
		return
	}
	if m.reply != nil {
		m.reply <- reply{rec: engine.TaskReceipt(t.t), err: err}
	}
}

// load 在处理循环之外用 engine.LoadGraphs 加载缺少的子图（加载失败同样记录，内核据此让节点失败），
// 然后把原消息连同加载结果重新投递，处理循环不因加载阻塞。加载期间其他事件照常处理；
// 重新应用时事件若已过期（如 Run 已被取消），内核按过期事件忽略。
// Task 已结束（mailbox 已关闭）时投递失败，直接回复调用方当前状态。
func (t *task) load(ids []string, m message) {
	got := maps.Clone(t.graphs)
	t.e.wg.Go(func() {
		if err := engine.LoadGraphs(t.ctx, t.e.cfg.ResolveGraph, ids, got); err != nil {
			m.fail = err
		}
		m.graphs = got
		if !t.box.put(m) && m.reply != nil {
			m.reply <- reply{rec: t.receipt()}
		}
	})
}

// apply 把事件交给内核，保存改动并执行命令。拒绝类错误原样返回、不改状态；
// 其余错误属于内核缺陷：上报 Observer，并改为保存 kernel.Fail 的结果（Task FAILED，设计文档 5.5）。
// Changes.NeedGraphs 非空时原样返回缺少的 GraphID 列表，由调用方在后台加载后重试。
func (t *task) apply(ev kernel.Event) ([]string, error) {
	runs := t.list()
	env := t.e.env()
	env.Graphs = t.graphs
	env.Awaiting = t.awaiting
	ch, cmds, err := kernel.Apply(t.c, t.t, runs, ev, env)
	if err != nil {
		if kernel.IsRejection(err) {
			return nil, err
		}
		t.e.cfg.Fail(fmt.Errorf("immediate: task %s: %w", t.id, err))
		ch, cmds = kernel.Fail(t.t, runs, err, env)
	} else if len(ch.NeedGraphs) > 0 {
		return ch.NeedGraphs, nil
	}
	t.save(ch)
	t.exec(cmds)
	return nil, nil
}

// fail 在内核之外发现的缺陷（如活动节点无法准备调用）同样按 kernel.Fail 结束 Task。
func (t *task) fail(err error) {
	t.e.cfg.Fail(fmt.Errorf("immediate: task %s: %w", t.id, err))
	env := t.e.env()
	env.Awaiting = t.awaiting
	ch, cmds := kernel.Fail(t.t, t.list(), err, env)
	t.save(ch)
	t.exec(cmds)
}

// dropAwaiting 从内存等待关系中删除这些子 Task（Changes.Released 的应用，重复删除无副作用）。
func (t *task) dropAwaiting(ids []string) {
	doomed := make(map[string]bool, len(ids))
	for _, id := range ids {
		doomed[id] = true
	}
	for runID, slots := range t.awaiting {
		for slot, taskID := range slots {
			if doomed[taskID] {
				delete(slots, slot)
			}
		}
		if len(slots) == 0 {
			delete(t.awaiting, runID)
		}
	}
}

// list 返回全部 Run（处理循环内调用，不加锁）。
func (t *task) list() []*engine.Run {
	out := make([]*engine.Run, 0, len(t.runs))
	for _, r := range t.runs {
		out = append(out, r)
	}
	return out
}

// save 保存内核返回的改动（内存中替换为内核返回的新对象），并中断已不再 RUNNING 的执行中节点
// （取消、any 汇合落选）：它们之后交回的结果因 Run 状态不符被内核忽略。
func (t *task) save(ch kernel.Changes) {
	if ch.Empty() {
		return
	}
	t.dropAwaiting(ch.Released)
	t.mu.Lock()
	t.t = ch.Task
	for _, r := range ch.Runs {
		t.runs[r.RunID] = r
	}
	t.bump()
	t.mu.Unlock()
	for _, r := range ch.Runs {
		if r.Status != graph.RUNNING {
			if cancel := t.cancels[r.RunID]; cancel != nil {
				cancel()
				delete(t.cancels, r.RunID)
			}
		}
	}
	t.e.cfg.Changed(t.id)
}

// bump 通知 Watch 状态已变化（调用方持有 mu）。
func (t *task) bump() {
	close(t.changed)
	t.changed = make(chan struct{})
}

// exec 执行内核命令：活动节点进入待派发集合；子 Task 立即启动；级联取消投递给子 Task；
// 通知父 Task 推迟到收尾写库之后（父 Task 结束时，它的子 Task 已经落库）。
func (t *task) exec(cmds []kernel.Command) {
	for _, cmd := range cmds {
		switch c := cmd.(type) {
		case kernel.ExecuteActivity:
			t.ready[c.RunID] = c
		case kernel.StartChild:
			// 先记入等待关系，再启动子 Task：spawn 失败时同样以 ChildDone 交回，
			// 父 Run 凭这条等待记录接收它，不会停在 WAITING 上。
			m := t.awaiting[c.Task.ParentRunID]
			if m == nil {
				m = make(map[string]string)
				t.awaiting[c.Task.ParentRunID] = m
			}
			m[c.Task.Slot] = c.Task.TaskID
			t.e.spawn(t, c)
		case kernel.CancelChild:
			t.e.post(c.ChildTaskID, kernel.Cancel{Reason: kernel.CancelChildReason})
		case kernel.NotifyParent:
			t.notify = append(t.notify, c)
		}
	}
}

// limit 返回本 Task 同时执行的活动节点上限：只有 parallel 模式且未启用单步 / 断点时并行。
func (t *task) limit() int {
	p := t.t.Policy
	if p.ExecutionMode != graph.ExecParallel || p.Debug.Active() {
		return 1
	}
	return p.MaxParallel
}

// dispatch 在单 Task 上限与全局名额内派发到期的活动节点，然后为最早一个未到期的重试设置定时唤醒。
// 全局名额不足时 Run 保持 PENDING，本 Task 留在等待队列中，拿到名额时被唤醒再派发；
// 其余情况（无可派发、受单 Task 上限限制）退出等待队列并交还未使用的名额。
func (t *task) dispatch() {
	now := t.e.cfg.Now()
	waiting := false
	for t.running < t.limit() && !graph.IsTerminal(t.t.Status) {
		cmd, ok := t.next(now)
		if !ok {
			break
		}
		if !t.e.slots.acquire(t) {
			waiting = true
			break
		}
		t.launch(cmd)
	}
	if !waiting {
		t.e.slots.leave(t)
	}
	t.arm(now)
}

// next 返回已到期的待派发命令中 NotBefore 最早（相同时 RunID 最小）的一个；
// 顺带丢弃已失效的命令（Run 不再是 PENDING，或已不在命令对应的执行上）。
func (t *task) next(now time.Time) (kernel.ExecuteActivity, bool) {
	var pick kernel.ExecuteActivity
	found := false
	for id, c := range t.ready {
		r := t.runs[id]
		if !c.Current(r) || r.Status != graph.PENDING {
			delete(t.ready, id)
			continue
		}
		if c.NotBefore.After(now) {
			continue
		}
		if !found || c.NotBefore.Before(pick.NotBefore) ||
			(c.NotBefore.Equal(pick.NotBefore) && strings.Compare(c.RunID, pick.RunID) < 0) {
			pick, found = c, true
		}
	}
	return pick, found
}

// launch 派发一个活动节点（已取得全局名额）：Run 置为 RUNNING，执行器在独立 goroutine 中运行，
// 返回后先归还名额再把结果投递回 mailbox。Task 已结束时投递被丢弃。
func (t *task) launch(cmd kernel.ExecuteActivity) {
	delete(t.ready, cmd.RunID)
	r := t.runs[cmd.RunID]
	act, err := kernel.NewActivity(t.c, t.t, r)
	if err != nil {
		t.e.slots.release()
		t.fail(err)
		return
	}
	t.mu.Lock()
	r.Status = graph.RUNNING
	t.bump()
	t.mu.Unlock()
	t.e.cfg.Changed(t.id)
	t.running++
	ctx, cancel := context.WithCancel(t.ctx)
	t.cancels[r.RunID] = cancel
	t.e.wg.Go(func() {
		out, err := act.Run(ctx)
		t.e.slots.release()
		t.box.put(message{ev: act.Done(out, err)})
	})
}

// arm 按待派发命令中最早的未到期 NotBefore（重试间隔）设置定时唤醒；没有未到期的命令时不设定时器。
func (t *task) arm(now time.Time) {
	var at time.Time
	for _, c := range t.ready {
		if c.NotBefore.After(now) && (at.IsZero() || c.NotBefore.Before(at)) {
			at = c.NotBefore
		}
	}
	if t.timer != nil {
		t.timer.Stop()
		t.timer = nil
	}
	if !at.IsZero() {
		t.timer = time.AfterFunc(at.Sub(now), t.box.kick)
	}
}

// finalize 在 Task 进入终态后收尾：关闭 mailbox、中断仍在执行的节点、退出名额队列、写库，
// 然后回复关闭前已投递的调用、通知父 Task，并按 Options.Retain 在内存中保留一段时间（第 9 条）。
func (t *task) finalize() {
	pending := t.box.close()
	t.cancel()
	if t.timer != nil {
		t.timer.Stop()
	}
	t.e.slots.leave(t)
	t.e.persist(t)
	rec := engine.TaskReceipt(t.t)
	for _, m := range pending {
		if m.reply != nil {
			m.reply <- reply{rec: rec}
		}
	}
	for _, n := range t.notify {
		t.e.post(n.ParentTaskID, n.Event)
	}
	t.e.retire(t)
}

// receipt 返回当前状态的回执（加锁读取，可在处理循环之外调用）。
func (t *task) receipt() engine.Receipt {
	t.mu.Lock()
	defer t.mu.Unlock()
	return engine.TaskReceipt(t.t)
}

// view 返回当前快照与下一次变化的通知 channel（同一把锁内取得，不会漏掉变化）。
func (t *task) view() (engine.TaskSnapshot, <-chan struct{}) {
	t.mu.Lock()
	defer t.mu.Unlock()
	task := t.t.Clone()
	runs := make([]engine.Run, 0, len(t.runs))
	for _, r := range t.runs {
		runs = append(runs, *r.Clone())
	}
	return engine.Snapshot(*task, runs), t.changed
}

// summary 返回列表摘要。
func (t *task) summary() engine.TaskSummary {
	t.mu.Lock()
	defer t.mu.Unlock()
	return summarize(t.t)
}

func summarize(t *engine.Task) engine.TaskSummary {
	s := engine.TaskSummary{TaskID: t.TaskID, TicketID: t.TicketID, EngineKind: t.EngineKind,
		GraphID: t.GraphID, GraphVersion: t.GraphVersion, Status: t.Status, Error: t.Error,
		ParentTaskID: t.ParentTaskID, StartedAt: t.StartedAt}
	if t.FinishedAt != nil {
		at := *t.FinishedAt
		s.FinishedAt = &at
	}
	return s
}
