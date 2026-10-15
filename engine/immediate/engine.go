// Package immediate 是内存执行引擎（设计文档第 9 节）。
//
// 本引擎的边界：
//   - 全部状态在内存中：每个 Task 一个处理循环，逐个把事件交给 engine/kernel 的 Apply；运行期间不读写数据库，不使用租约；
//   - 活动节点在独立 goroutine 中执行；全局名额（Options.MaxActivities）限制总并发，Policy.MaxParallel 限制单个 Task；
//   - Task 结束时在一个事务内写入 Task 与全部 Run；没有配置数据库时跳过写入；
//   - 进程退出后执行中的 Task 丢失，数据库中没有它的记录；需要续跑应使用 durable。
//
// Package immediate is the in-memory engine built on engine/kernel.
package immediate

import (
	"container/list"
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
)

// Options 是 immediate 引擎的配置。
type Options struct {
	// MaxActivities 是本引擎同时执行的活动节点总数上限，默认 256。名额不足时 Run 保持 PENDING。
	MaxActivities int
	// Retain 是已结束的 Task 在内存中的保留时间，默认 1 分钟（使用 DefaultOptions）；期间 Snapshot / Watch / Signal 读内存，
	// 之后从数据库读取（没有数据库时返回 ErrNotFound）。
	// 小于等于 0 时结束即释放，不走 time.AfterFunc 延迟注销。
	Retain time.Duration
	// MaxRetainedTasks 是已结束 Task 在内存中保留的最大数量上限，默认 10000。
	// 当保留任务数超过该阈值时，按 LRU 自动驱逐最老已完成的任务。
	MaxRetainedTasks int
}

// DefaultOptions 返回 immediate 引擎的默认配置。
func DefaultOptions() Options {
	return Options{
		MaxActivities:    256,
		Retain:           time.Minute,
		MaxRetainedTasks: 10000,
	}
}

func (o Options) normalize() Options {
	if o.MaxActivities <= 0 {
		o.MaxActivities = 256
	}
	if o.MaxRetainedTasks <= 0 {
		o.MaxRetainedTasks = 10000
	}
	return o
}

// retainedEntry 是内存保留池中的条目。
type retainedEntry struct {
	tk    *task
	timer *time.Timer
}

// closeReason 是引擎关闭时取消执行中 Task 的原因（设计文档 9.4）。
const closeReason = "engine closed"

// Engine 是 immediate 引擎。
type Engine struct {
	cfg   engine.EngineConfig // cfg.DB 为 nil 表示不写库：结果只能在内存保留期内通过 Snapshot / Watch 获取
	opt   Options
	cache *kernel.Cache
	slots *slots
	wg    sync.WaitGroup // 处理循环与活动节点 goroutine

	mu            sync.Mutex
	live          map[string]*task         // 执行中与保留期内的 Task（含子 Task）
	retainedList  *list.List               // LRU 链表：Front 是最新访问/完成的，Back 是最老完成的
	retainedTasks map[string]*list.Element // TaskID -> *list.Element (Value 为 *retainedEntry)
	closed        bool
	closeDone     chan struct{}
}

var (
	_ engine.Engine  = (*Engine)(nil)
	_ engine.Builder = Build
)

// Build 是使用默认 Options 的 engine.Builder。
func Build(ctx context.Context, cfg engine.EngineConfig) (engine.Engine, error) {
	return With(DefaultOptions())(ctx, cfg)
}

// With 返回使用指定 Options 的 engine.Builder：校验配置、建立执行表（若配置了 DB）后构造引擎。
func With(opt Options) engine.Builder {
	return func(ctx context.Context, cfg engine.EngineConfig) (engine.Engine, error) {
		if err := cfg.Check(); err != nil {
			return nil, err
		}
		if cfg.DB != nil {
			if err := engine.MigrateExecution(ctx, cfg.DB); err != nil {
				return nil, err
			}
		}
		return New(cfg, opt), nil
	}
}

// New 直接构造引擎；cfg.DB 可以为 nil（不写库，见 Execute）。
func New(cfg engine.EngineConfig, opt Options) *Engine {
	opt = opt.normalize()
	return &Engine{
		cfg: cfg, opt: opt, cache: kernel.NewCache(cfg.Nodes), slots: newSlots(opt.MaxActivities),
		live:          map[string]*task{},
		retainedList:  list.New(),
		retainedTasks: map[string]*list.Element{},
		closeDone:     make(chan struct{}),
	}
}

// Execute 在内存中执行一张图（不读写数据库），直到 Task 结束或停在调试等待上，返回此时的快照；
// 返回后引擎关闭，停在调试等待上的 Task 随之取消。子图通过 cfg.ResolveGraph 解析。供测试与示例使用。
func Execute(ctx context.Context, cfg engine.EngineConfig, req engine.StartRequest) (engine.TaskSnapshot, error) {
	cfg.DB = nil
	e := New(cfg, DefaultOptions())
	defer e.Close(context.Background())
	rec, err := e.Start(ctx, req)
	if err != nil {
		return engine.TaskSnapshot{}, err
	}
	wctx, cancel := context.WithCancel(ctx)
	defer cancel()
	ch, err := e.Watch(wctx, rec.TaskID)
	if err != nil {
		return engine.TaskSnapshot{}, err
	}
	var last engine.TaskSnapshot
	for s := range ch {
		last = s
		if graph.IsTerminal(s.Task.Status) || debugIdle(s) {
			return s, nil
		}
	}
	return last, ctx.Err()
}

// debugIdle 报告 Task 是否停在调试等待上：没有 PENDING / RUNNING 的 Run，且至少一个 Run 处于 WAITING(debug)。
func debugIdle(s engine.TaskSnapshot) bool {
	parked := false
	for _, r := range s.Runs {
		switch r.Status {
		case graph.PENDING, graph.RUNNING:
			return false
		case graph.WAITING:
			parked = parked || r.WaitReason == engine.WaitDebug
		}
	}
	return parked
}

// Kind 返回引擎标识。
func (e *Engine) Kind() string { return engine.KindImmediate }

// env 返回一次 Apply 的环境。
func (e *Engine) env() kernel.Env {
	return kernel.Env{Now: e.cfg.Now(), Compile: e.cache.Compile}
}

// Start 编译图、构造 Task，启动它的处理循环并应用 Started（执行到第一批活动节点或等待点）后返回回执。
func (e *Engine) Start(ctx context.Context, req engine.StartRequest) (engine.Receipt, error) {
	c, err := e.cache.Compile(req.PinnedGraph())
	if err != nil {
		return engine.Receipt{}, err
	}
	t, err := kernel.NewTask(engine.KindImmediate, req, c, e.env())
	if err != nil {
		return engine.Receipt{}, err
	}
	tk := e.newTask(t, c)
	if err := e.register(tk); err != nil {
		return engine.Receipt{}, err
	}
	return tk.call(ctx, kernel.Started{})
}

// register 登记 Task 并启动其处理循环；引擎已关闭时拒绝。
func (e *Engine) register(tk *task) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("%w: engine closed", engine.ErrUnsupported)
	}
	e.live[tk.id] = tk
	e.wg.Add(1)
	go tk.loop()
	return nil
}

// spawn 执行 StartChild：子 Task 使用父 Task 的引擎，拥有自己的处理循环。
// 无法启动（引擎正在关闭）时把失败结果作为 ChildDone 交回父 Run，由父 Task 按节点失败处理。
func (e *Engine) spawn(parent *task, sc kernel.StartChild) {
	child := e.newTask(sc.Task, sc.Compiled)
	if err := e.register(child); err != nil {
		parent.box.put(message{ev: kernel.ChildDone{
			RunID: sc.Task.ParentRunID, Slot: sc.Task.Slot, ChildTaskID: sc.Task.TaskID,
			Status: graph.FAILED, Error: err.Error(),
		}})
		return
	}
	child.box.put(message{ev: kernel.Started{}})
}

// post 把事件投递给内存中的 Task（ChildDone、级联 Cancel）；Task 已不在内存中时丢弃
// （它已经结束，Apply 对任何事件都不会再有改动）。
func (e *Engine) post(taskID string, ev kernel.Event) {
	if tk := e.lookup(taskID); tk != nil {
		tk.box.put(message{ev: ev})
	}
}

// lookup 返回内存中的 Task；不在内存中时返回 nil。若该 Task 处于保留列表中，刷新其 LRU 顺序。
func (e *Engine) lookup(taskID string) *task {
	e.mu.Lock()
	defer e.mu.Unlock()
	tk := e.live[taskID]
	if tk != nil {
		if elem, ok := e.retainedTasks[taskID]; ok {
			e.retainedList.MoveToFront(elem)
		}
	}
	return tk
}

// retire 安排已结束的 Task 在保留期后移出内存，并在保留数超过 MaxRetainedTasks 时按 LRU 淘汰最老完成的任务。
// 当 Retain <= 0 时立即释放，不走 time.AfterFunc 延迟注销。
func (e *Engine) retire(tk *task) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.opt.Retain <= 0 {
		if e.live[tk.id] == tk {
			delete(e.live, tk.id)
		}
		return
	}
	if e.closed || e.live[tk.id] != tk {
		return
	}

	e.evictRetained(tk.id)

	for e.retainedList.Len() >= e.opt.MaxRetainedTasks {
		back := e.retainedList.Back()
		if back == nil {
			break
		}
		entry := back.Value.(*retainedEntry)
		e.evictRetained(entry.tk.id)
	}

	entry := &retainedEntry{tk: tk}
	entry.timer = time.AfterFunc(e.opt.Retain, func() {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.evictRetained(tk.id)
	})

	elem := e.retainedList.PushFront(entry)
	e.retainedTasks[tk.id] = elem
}

// evictRetained 从 LRU 链表与 live 中移出指定的已完成 Task（调用方必须持有 e.mu）。
func (e *Engine) evictRetained(id string) {
	elem, ok := e.retainedTasks[id]
	if !ok {
		return
	}
	delete(e.retainedTasks, id)
	e.retainedList.Remove(elem)
	entry := elem.Value.(*retainedEntry)
	if entry.timer != nil {
		entry.timer.Stop()
	}
	if e.live[id] == entry.tk {
		delete(e.live, id)
	}
}

// Signal 投递取消、暂停或恢复。Task 已结束时返回当前状态的回执，不报错；
// 控制事件不满足前提（已暂停、没有可放行的 Run 等）时返回 ErrConflict / ErrInvalid，Task 照常执行。
func (e *Engine) Signal(ctx context.Context, taskID string, sig engine.Signal) (engine.Receipt, error) {
	ev, err := kernel.SignalEvent(sig)
	if err != nil {
		return engine.Receipt{}, err
	}
	if tk := e.lookup(taskID); tk != nil {
		return tk.call(ctx, ev)
	}
	snap, err := e.Snapshot(ctx, taskID)
	if err != nil {
		return engine.Receipt{}, err
	}
	return engine.TaskReceipt(&snap.Task), nil
}

// Snapshot 返回最新快照：内存中的 Task 读内存，其余读库（只有已结束的 Task 会在库中）。
func (e *Engine) Snapshot(ctx context.Context, taskID string) (engine.TaskSnapshot, error) {
	if tk := e.lookup(taskID); tk != nil {
		snap, _ := tk.view()
		return snap, nil
	}
	if e.cfg.DB == nil {
		return engine.TaskSnapshot{}, engine.NotFoundTask(taskID)
	}
	snap, err := engine.LoadSnapshot(e.cfg.DB.WithContext(ctx), taskID)
	return engine.ForEngine(engine.KindImmediate, taskID, snap, err)
}

// Watch 推送 Task 的完整快照：第一条为当前状态，之后内存中的状态每变化一次推送一次（读取方较慢时合并），
// 推送终态快照后关闭。不在内存中的 Task（已结束并移出内存）只推送库中的快照一次。
func (e *Engine) Watch(ctx context.Context, taskID string) (<-chan engine.TaskSnapshot, error) {
	tk := e.lookup(taskID)
	if tk == nil {
		snap, err := e.Snapshot(ctx, taskID)
		if err != nil {
			return nil, err
		}
		ch := make(chan engine.TaskSnapshot, 1)
		ch <- snap
		close(ch)
		return ch, nil
	}
	out := make(chan engine.TaskSnapshot)
	go func() {
		defer close(out)
		last := ""
		for {
			snap, changed := tk.view()
			if fp := engine.Fingerprint(snap); fp != last {
				last = fp
				select {
				case out <- snap:
				case <-ctx.Done():
					return
				}
			}
			if graph.IsTerminal(snap.Task.Status) {
				return
			}
			select {
			case <-changed:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

// List 列出本引擎的根 Task，按 StartedAt 倒序：合并内存中的 Task（执行中与保留期内）与库中已结束的 Task，
// 同一 TaskID 以内存中的为准。After 为上一页最后一个 TaskID；游标不存在时返回 ErrNotFound。
//
// List lists root tasks of this engine ordered by (StartedAt DESC, TaskID DESC),
// merging live/retained in-memory tasks with persisted database rows.
func (e *Engine) List(ctx context.Context, q engine.ListQuery) ([]engine.TaskSummary, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	q.Limit = limit
	q.After = strings.TrimSpace(q.After)

	e.mu.Lock()
	tasks := make([]*task, 0, len(e.live))
	for _, tk := range e.live {
		tasks = append(tasks, tk)
	}
	e.mu.Unlock()

	summaries := make([]engine.TaskSummary, len(tasks))
	for i, tk := range tasks {
		s := tk.summary()
		summaries[i] = s
		if q.After != "" && q.AfterAt.IsZero() && s.TaskID == q.After {
			q.AfterAt = s.StartedAt
		}
	}
	if q.After != "" && q.AfterAt.IsZero() {
		if e.cfg.DB == nil {
			return nil, engine.NotFoundTask(q.After)
		}
		t, err := engine.LoadTask(e.cfg.DB.WithContext(ctx), q.After, false)
		if err != nil {
			return nil, err
		}
		q.AfterAt = t.StartedAt
	}

	seen := make(map[string]bool, len(summaries))
	var out []engine.TaskSummary
	for _, s := range summaries {
		seen[s.TaskID] = true
		if s.ParentTaskID != "" || (q.GraphID != "" && s.GraphID != q.GraphID) || (q.Status != "" && s.Status != q.Status) {
			continue
		}
		if q.After != "" && (s.StartedAt.After(q.AfterAt) || (s.StartedAt.Equal(q.AfterAt) && s.TaskID >= q.After)) {
			continue
		}
		out = append(out, s)
	}
	if e.cfg.DB != nil {
		rows, err := engine.ListTasks(e.cfg.DB.WithContext(ctx), engine.KindImmediate, q)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !seen[r.TaskID] {
				out = append(out, r)
			}
		}
	}
	slices.SortFunc(out, func(a, b engine.TaskSummary) int {
		if c := b.StartedAt.Compare(a.StartedAt); c != 0 {
			return c
		}
		if a.TaskID > b.TaskID {
			return -1
		}
		if a.TaskID < b.TaskID {
			return 1
		}
		return 0
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Close 停止接收新 Task，对执行中的 Task 投递 Cancel（原因 engine closed），等待处理循环写入 CANCELED 结果、
// 活动节点 goroutine 返回（设计文档 9.4）。返回 nil 表示工作已完全退出且清理完成；
// 若 ctx 截止时工作尚未完成，返回 ctx 的错误，后续再次调用 Close 仍会等待实际完成状态。
func (e *Engine) Close(ctx context.Context) error {
	e.mu.Lock()
	if !e.closed {
		e.closed = true
		tasks := make([]*task, 0, len(e.live))
		for _, tk := range e.live {
			tasks = append(tasks, tk)
		}
		for _, tk := range tasks {
			tk.box.put(message{ev: kernel.Cancel{Reason: closeReason}})
		}
		go func() {
			e.wg.Wait()
			e.mu.Lock()
			clear(e.live)
			for _, elem := range e.retainedTasks {
				entry := elem.Value.(*retainedEntry)
				if entry.timer != nil {
					entry.timer.Stop()
				}
			}
			clear(e.retainedTasks)
			e.retainedList.Init()
			e.mu.Unlock()
			close(e.closeDone)
		}()
	}
	done := e.closeDone
	e.mu.Unlock()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
