package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"gorm.io/gorm"
)

// EngineRegistry 注册 immediate / durable 等引擎，并把按 TaskID 的控制动作转发给所属引擎。
// Task 的归属先查 tb_graph_task.EngineKind；表中没有时依次询问各引擎的 Snapshot
// （执行中、尚未写库的 Task 只在所属引擎的内存中）。
// 查询（Snapshot / Watch）区分执行中与已结束（设计文档第 11 节）：已结束且已写入执行表的 Task 直接读表，
// 不经过引擎，因此产生它的引擎未注册或已重启时仍可查询；执行中的 Task 转发给所属引擎。
// 勿与 node.Registry（节点注册表）混淆。
//
// EngineRegistry routes task-level actions to the engine that owns the task.
type EngineRegistry struct {
	db      *gorm.DB // 执行库；为 nil 时只按引擎 Snapshot 判断归属
	mu      sync.RWMutex
	engines map[string]engine.Engine
	closing bool
	active  sync.WaitGroup
	drained chan struct{}
}

// NewEngineRegistry 创建空引擎表；db 为执行库（tb_graph_task 所在库），用于按 TaskID 查归属。
func NewEngineRegistry(db *gorm.DB) *EngineRegistry {
	return &EngineRegistry{db: db, engines: map[string]engine.Engine{}}
}

// Register 注册引擎；Kind 必须合法且不重复。
func (r *EngineRegistry) Register(eng engine.Engine) error {
	kind := strings.TrimSpace(eng.Kind())
	if !graph.ValidID(kind) {
		return fmt.Errorf("graph runtime: invalid engine kind %q", kind)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || r.engines[kind] != nil {
		return fmt.Errorf("%w: cannot register engine %q", engine.ErrConflict, kind)
	}
	r.engines[kind] = eng
	return nil
}

// Engine 按 Kind 取引擎。
func (r *EngineRegistry) Engine(kind string) (engine.Engine, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	eng := r.engines[strings.TrimSpace(kind)]
	if eng == nil {
		return nil, fmt.Errorf("%w: engine %q", engine.ErrNotFound, kind)
	}
	return eng, nil
}

// Kinds 返回已注册引擎的 Kind，按字母序。
func (r *EngineRegistry) Kinds() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.engines))
	for kind := range r.engines {
		out = append(out, kind)
	}
	sort.Strings(out)
	return out
}

// each 依次对所有引擎执行 fn，合并错误。
func (r *EngineRegistry) each(fn func(kind string, e engine.Engine) error) error {
	var result error
	for _, kind := range r.Kinds() {
		eng, err := r.Engine(kind)
		if err != nil {
			continue
		}
		if err = fn(kind, eng); err != nil {
			result = errors.Join(result, fmt.Errorf("graph runtime: engine %s: %w", kind, err))
		}
	}
	return result
}

// Close 等待进行中的 Start 结束后关闭所有引擎。
func (r *EngineRegistry) Close(ctx context.Context) error {
	r.mu.Lock()
	if !r.closing {
		r.closing = true
		r.drained = make(chan struct{})
		go func() { r.active.Wait(); close(r.drained) }()
	}
	done := r.drained
	r.mu.Unlock()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-done:
	}
	return r.each(func(_ string, e engine.Engine) error { return e.Close(ctx) })
}

// Start 在指定引擎上创建 Task。关闭中拒绝执行。
func (r *EngineRegistry) Start(ctx context.Context, kind string, req engine.StartRequest) (engine.Receipt, error) {
	r.mu.Lock()
	eng := r.engines[strings.TrimSpace(kind)]
	if r.closing {
		r.mu.Unlock()
		return engine.Receipt{}, fmt.Errorf("%w: runtime is closing", engine.ErrConflict)
	}
	if eng == nil {
		r.mu.Unlock()
		return engine.Receipt{}, fmt.Errorf("%w: engine %q", engine.ErrNotFound, kind)
	}
	r.active.Add(1)
	r.mu.Unlock()
	defer r.active.Done()
	receipt, err := eng.Start(ctx, req)
	if err != nil {
		return engine.Receipt{}, err
	}
	if receipt.TaskID == "" {
		return engine.Receipt{}, fmt.Errorf("graph runtime: engine %q returned empty TaskID", kind)
	}
	return receipt, nil
}

// ForTask 返回 Task 所属引擎：先查 tb_graph_task.EngineKind，表中不存在时依次询问各引擎的 Snapshot。
func (r *EngineRegistry) ForTask(ctx context.Context, taskID string) (engine.Engine, error) {
	eng, _, err := r.route(ctx, taskID, false)
	return eng, err
}

// route 查找 Task 的去向，并在已经拿到当前快照时一并返回（snap 非 nil）：
//   - 表中有该行、已结束且 readEnded 为 true：直接读表（设计文档第 11 节），返回表中快照，eng 为 nil；
//   - 表中有该行：返回 EngineKind 对应的引擎，引擎未注册时返回 ErrNotFound；
//   - 表中没有（immediate 执行中尚未落库，或未配置执行库）：依次询问各引擎的 Snapshot，返回找到的引擎与它的快照。
//
// 不缓存 TaskID → 引擎的归属：缓存会随启动过的 Task 数无限增长，而按表查找只是一次主键查询。
//
// route resolves where a task lives; finished tasks are read straight from the table.
func (r *EngineRegistry) route(ctx context.Context, taskID string, readEnded bool) (engine.Engine, *engine.TaskSnapshot, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, nil, fmt.Errorf("%w: TaskID required", engine.ErrInvalid)
	}
	row, inTable, err := r.stored(ctx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if inTable && readEnded && graph.IsTerminal(row.Status) {
		snap, err := engine.LoadSnapshot(r.db.WithContext(ctx), taskID)
		if err == nil {
			return nil, &snap, nil
		}
		if !errors.Is(err, engine.ErrNotFound) {
			return nil, nil, err
		}
		// 两次读取之间行被删除：按表中没有处理，交给引擎判断。
		inTable = false
	}
	if inTable {
		eng, err := r.Engine(row.EngineKind)
		if err != nil {
			return nil, nil, fmt.Errorf("%w: task %q belongs to unregistered engine %q", engine.ErrNotFound, taskID, row.EngineKind)
		}
		return eng, nil, nil
	}
	return r.findInEngines(ctx, taskID)
}

// findInEngines 依次询问所有已注册引擎的 Snapshot，寻找尚未写入执行表的 Task（如执行中的 immediate 任务）。
//
// findInEngines queries registered engines for a task not yet present in the execution table.
func (r *EngineRegistry) findInEngines(ctx context.Context, taskID string) (engine.Engine, *engine.TaskSnapshot, error) {
	var found engine.Engine
	var snap engine.TaskSnapshot
	err := r.each(func(_ string, e engine.Engine) error {
		if found != nil {
			return nil
		}
		s, err := e.Snapshot(ctx, taskID)
		if errors.Is(err, engine.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		found, snap = e, s
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if found == nil {
		return nil, nil, fmt.Errorf("%w: task %q", engine.ErrNotFound, taskID)
	}
	return found, &snap, nil
}

// cursor 解析分页游标 taskID 的 StartedAt：先查执行表，表中没有再询问各已注册引擎；
// 在执行表与所有引擎中都不存在时返回 ErrNotFound。
//
// cursor resolves the StartedAt timestamp for pagination cursor taskID across table and engines.
func (r *EngineRegistry) cursor(ctx context.Context, taskID string) (time.Time, error) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return time.Time{}, fmt.Errorf("%w: TaskID required", engine.ErrInvalid)
	}
	row, inTable, err := r.stored(ctx, taskID)
	if err != nil {
		return time.Time{}, err
	}
	if inTable {
		return row.StartedAt, nil
	}
	_, snap, err := r.findInEngines(ctx, taskID)
	if err != nil {
		return time.Time{}, err
	}
	return snap.Task.StartedAt, nil
}

// stored 读取 tb_graph_task 中 Task 的 EngineKind、Status 与 StartedAt（只读这三列）；
// 行不存在或未配置执行库时 found 为 false。
func (r *EngineRegistry) stored(ctx context.Context, taskID string) (row engine.Task, found bool, err error) {
	if r.db == nil {
		return engine.Task{}, false, nil
	}
	var rows []engine.Task
	err = r.db.WithContext(ctx).Model(&engine.Task{}).Select("EngineKind", "Status", "StartedAt").
		Where(map[string]any{"TaskID": taskID}).Limit(1).Find(&rows).Error
	if err != nil || len(rows) == 0 {
		return engine.Task{}, false, err
	}
	return rows[0], true, nil
}

// Snapshot 读取 Task 最新快照：已结束的 Task 读执行表，执行中的 Task 转发给所属引擎。
func (r *EngineRegistry) Snapshot(ctx context.Context, taskID string) (engine.TaskSnapshot, error) {
	taskID = strings.TrimSpace(taskID)
	eng, snap, err := r.route(ctx, taskID, true)
	if err != nil {
		return engine.TaskSnapshot{}, err
	}
	if snap != nil {
		return *snap, nil
	}
	return eng.Snapshot(ctx, taskID)
}

// Watch 订阅 Task：引擎推送完整快照，这里转为「首个完整快照 + 增量」的事件流。
// 已结束的 Task 读执行表，只推送一次终态的完整快照后结束；执行中的 Task 订阅所属引擎。
func (r *EngineRegistry) Watch(ctx context.Context, taskID string) (SnapshotStream, error) {
	taskID = strings.TrimSpace(taskID)
	eng, snap, err := r.route(ctx, taskID, true)
	if err != nil {
		return nil, err
	}
	wctx, cancel := context.WithCancel(ctx)
	if eng == nil {
		ch := make(chan engine.TaskSnapshot, 1)
		ch <- *snap
		close(ch)
		return newSnapshotStream(wctx, cancel, ch), nil
	}
	ch, err := eng.Watch(wctx, taskID)
	if err != nil {
		cancel()
		return nil, err
	}
	return newSnapshotStream(wctx, cancel, ch), nil
}

// Signal 向 Task 投递控制信号；对已结束的 Task 返回当前状态，不报错。
func (r *EngineRegistry) Signal(ctx context.Context, taskID string, sig engine.Signal) (engine.Receipt, error) {
	taskID = strings.TrimSpace(taskID)
	eng, err := r.ForTask(ctx, taskID)
	if err != nil {
		return engine.Receipt{}, err
	}
	return eng.Signal(ctx, taskID, sig)
}

// Resume 把 HTTP 恢复请求转为 resume 信号。
func (r *EngineRegistry) Resume(ctx context.Context, taskID string, req engine.ResumeRequest) (engine.Receipt, error) {
	return r.Signal(ctx, taskID, engine.Signal{Kind: engine.SignalResume, Resume: &req})
}

// Pause 把 HTTP 暂停请求转为 pause 信号。
func (r *EngineRegistry) Pause(ctx context.Context, taskID string) (engine.Receipt, error) {
	return r.Signal(ctx, taskID, engine.Signal{Kind: engine.SignalPause})
}

// Cancel 把 HTTP 取消请求转为 cancel 信号。
func (r *EngineRegistry) Cancel(ctx context.Context, taskID, reason string) (engine.Receipt, error) {
	return r.Signal(ctx, taskID, engine.Signal{Kind: engine.SignalCancel, Reason: reason})
}
