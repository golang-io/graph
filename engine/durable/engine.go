// Package durable 是数据库持久化的执行引擎（设计文档第 8 节）。
//
// 本引擎的边界：
//   - 图语义全部由 engine/kernel 的 Apply 决定；本包只负责调度与持久化。本包的策略数据（分片、租约、
//     已保存的结果、子 Task 的等待标记）定义在本包的表行结构 DurableTask / DurableRun 中，engine 与内核不感知（设计文档第 2 节）；
//   - 每次写回是一个事务：锁 Task 行 → 读取该 Task 的全部 Run 并按 Awaited 构造等待视图 → Apply → 写回 → 提交（store.go）；
//   - Run 行即任务队列：多个 worker（可跨进程）按分片用条件更新领取到期的 PENDING Run，
//     领取时 LeaseToken 加 1；活动节点在事务外执行并定期续租（worker.go）；
//   - 按 GraphID 引用的子图在事务外加载：Apply 返回 Changes.NeedGraphs 时回滚，加载后重做事务（store.go txn）；
//   - 执行结果先用单行条件更新写入自己的 Run（DurableRun.Result，不锁 Task 行），再在写回事务中应用；
//     写回失败时结果仍在库中，恢复扫描在租约过期后应用它，不重新执行节点；
//   - 写回事务内只有本事务的读写与纯计算（kernel.Apply），Observer 回调在提交后调用；
//   - 跨 Task 动作（NotifyParent / CancelChild）在提交后各自用独立事务投递；
//     进程在提交后、投递前退出时，由恢复扫描根据状态补发（recover.go）；
//   - 进程崩溃后从数据库状态继续：租约过期的 Run 已保存结果的直接应用；否则视为执行器失联，
//     由本引擎用单行条件更新放回 PENDING 重新领取（LostCount 加 1、Attempt 不变，不经过内核），
//     失联超过 MaxLost 次时以执行失败（ActivityDone）交给内核，按业务重试策略处理（recover.go）。
//
// Package durable persists every state change in a database transaction and resumes after crashes.
package durable

import (
	"context"
	"fmt"
	"hash/crc32"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
	"gorm.io/gorm"
)

// Options 是 durable 的调度参数；零值使用默认值。
type Options struct {
	Workers       int           // 本进程的 worker 数，默认 CPU 数（至少 2）
	Shards        int           // Run 分片数，默认 16；同一数据库上的所有进程必须一致
	LeaseDuration time.Duration // 执行租约，默认 30s；每 1/3 续租一次，恢复扫描也按这个间隔运行
	PollInterval  time.Duration // 没有可领取的 Run 时的轮询间隔，默认 100ms
	// RecoveryMode 为 true 时完全只读：不启动 worker 与恢复扫描、不回收租约、不补发投递，
	// 拒绝 Start 与 Signal，只允许查询，便于运维检查数据库中的执行状态（迁移计划第 34 条）。
	RecoveryMode bool
}

func (o Options) normalize() Options {
	if o.Workers <= 0 {
		o.Workers = max(2, runtime.NumCPU())
	}
	if o.Shards <= 0 {
		o.Shards = 16
	}
	if o.LeaseDuration <= 0 {
		o.LeaseDuration = 30 * time.Second
	}
	if o.PollInterval <= 0 {
		o.PollInterval = 100 * time.Millisecond
	}
	return o
}

// Engine 是 durable 引擎。
//
// 生命周期使用两个 context：
//   - stop：Close 开始时取消，worker 停止领取、恢复扫描停止，Start / Signal 被拒绝；
//   - life：Close 的 ctx 到期时取消，中断仍在执行的活动节点并丢弃其结果（Run 保持 RUNNING，由租约过期回收）。
//
// 数据库访问不使用这两个 context：已开始的事务总是执行完，避免中途打断导致写回不完整。
type Engine struct {
	cfg   engine.EngineConfig
	opt   Options
	cache *kernel.Cache
	// byTask 是 TaskID → *kernel.Compiled：每次写回都要取编译结果，命中时不读取 Task 的图快照列。
	// 值指向 cache 中按图内容去重的同一份 Compiled，条目本身只占一个指针。
	// Task 在本进程写入终态时删除；由其他进程结束的 Task 由恢复扫描（pruneCompiled）清理。
	byTask sync.Map

	stop, life             context.Context
	cancelStop, cancelLife context.CancelFunc
	wg                     sync.WaitGroup // worker 与恢复扫描（含 worker 内执行中的活动节点）
	kick                   chan struct{}  // 有新的到期 Run 时唤醒本进程空闲的 worker
	local                  sync.Map       // RunID → 本进程执行中节点的取消函数

	// drop 只供测试使用：返回 true 的提交后投递被丢弃，模拟「提交后、投递前进程退出」。
	drop atomic.Pointer[func(kernel.Command) bool]
}

var (
	_ engine.Engine  = (*Engine)(nil)
	_ engine.Builder = Build
)

// Build 是使用默认 Options 的 engine.Builder。
func Build(ctx context.Context, cfg engine.EngineConfig) (engine.Engine, error) {
	return With(Options{})(ctx, cfg)
}

// With 返回使用指定 Options 的 engine.Builder。
// 非恢复模式下先建表、执行一次恢复扫描（回收过期租约、补发投递），再启动 worker 与周期恢复扫描，由 Close 停止；
// 恢复模式（RecoveryMode 为 true）完全只读：跳过建表迁移（不执行任何 DDL），不运行恢复扫描与 worker。
//
// With returns an engine.Builder configured with opt; RecoveryMode skips DDL migration and background workers.
func With(opt Options) engine.Builder {
	return func(ctx context.Context, cfg engine.EngineConfig) (engine.Engine, error) {
		if err := cfg.Check(); err != nil {
			return nil, err
		}
		opt := opt.normalize()
		e := &Engine{
			cfg: cfg, opt: opt, cache: kernel.NewCache(cfg.Nodes),
			kick: make(chan struct{}, opt.Workers),
		}
		e.stop, e.cancelStop = context.WithCancel(context.Background())
		e.life, e.cancelLife = context.WithCancel(context.Background())
		if opt.RecoveryMode {
			return e, nil
		}
		if err := migrate(ctx, cfg.DB); err != nil {
			e.cancelStop()
			e.cancelLife()
			return nil, err
		}
		if err := e.recoverOnce(); err != nil {
			e.cancelStop()
			e.cancelLife()
			return nil, err
		}
		e.wg.Go(e.schedule)
		return e, nil
	}
}

// env 返回一次 Apply 的环境。Env 只做内存计算；已加载的子图（Env.Graphs）由 applyIn 从 batch 填入，
// 缺少时由 txn 在事务外加载（engine.LoadGraphs）。
func (e *Engine) env() kernel.Env {
	return kernel.Env{Now: e.cfg.Now(), Compile: e.cache.Compile}
}

// Kind 返回引擎标识。
func (e *Engine) Kind() string { return engine.KindDurable }

// writable 报告是否接受写入类调用（Start / Signal）。
func (e *Engine) writable() error {
	if e.opt.RecoveryMode {
		return fmt.Errorf("%w: engine is in recovery mode", engine.ErrUnsupported)
	}
	if e.stop.Err() != nil {
		return fmt.Errorf("%w: engine closed", engine.ErrUnsupported)
	}
	return nil
}

// Start 编译图、构造 Task，在一个事务内插入 Task 并应用 Started（执行到第一批活动节点或等待点），返回回执。
// 根 Task 的分片为 shardOf(TaskID)；同一事务内发起的子 Task 沿用它。
func (e *Engine) Start(ctx context.Context, req engine.StartRequest) (engine.Receipt, error) {
	if err := e.writable(); err != nil {
		return engine.Receipt{}, err
	}
	c, err := e.cache.Compile(req.PinnedGraph())
	if err != nil {
		return engine.Receipt{}, err
	}
	t, err := kernel.NewTask(engine.KindDurable, req, c, e.env())
	if err != nil {
		return engine.Receipt{}, err
	}
	shard := e.shardOf(t.TaskID)
	b, err := e.txn(ctx, func(tx *gorm.DB, b *batch) error {
		_, err := e.applyIn(tx, c, t, nil, act{ev: kernel.Started{}}, true, shard, b)
		return err
	})
	if err != nil {
		return engine.Receipt{}, err
	}
	e.settle(b)
	return engine.TaskReceipt(b.head), nil
}

// Signal 在一个事务内投递取消、暂停或恢复，返回处理后的回执。
// Task 已结束时返回当前状态的回执，不报错；控制事件不满足前提时返回 ErrConflict / ErrInvalid，Task 照常执行。
// 取消后本进程内执行中的节点立即中断，其他进程的在下次续租时中断。
func (e *Engine) Signal(ctx context.Context, taskID string, sig engine.Signal) (engine.Receipt, error) {
	if err := e.writable(); err != nil {
		return engine.Receipt{}, err
	}
	ev, err := kernel.SignalEvent(sig)
	if err != nil {
		return engine.Receipt{}, err
	}
	b, err := e.event(ctx, taskID, func(*engine.Task, map[string]*DurableRun) act { return act{ev: ev} })
	if err != nil {
		return engine.Receipt{}, err
	}
	e.settle(b)
	return engine.TaskReceipt(b.head), nil
}

// Snapshot 读取最新快照。
func (e *Engine) Snapshot(ctx context.Context, taskID string) (engine.TaskSnapshot, error) {
	snap, err := engine.LoadSnapshot(e.cfg.DB.WithContext(ctx), taskID)
	return engine.ForEngine(engine.KindDurable, taskID, snap, err)
}

// shardOf 计算根 Task 的分片 crc32(TaskID) % Shards。同一 Task 的 Run 都在这个分片；
// 子 Task 不按自己的 TaskID 计算，沿用父 Task 的分片（applyIn 的 shard 参数），一棵 Task 树在同一分片。
func (e *Engine) shardOf(taskID string) int {
	return int(crc32.ChecksumIEEE([]byte(taskID)) % uint32(e.opt.Shards))
}

// List 列出本引擎的根 Task。
func (e *Engine) List(ctx context.Context, q engine.ListQuery) ([]engine.TaskSummary, error) {
	return engine.ListTasks(e.cfg.DB.WithContext(ctx), engine.KindDurable, q)
}

// Watch 订阅 Task 快照：按 PollInterval 轮询，指纹（含写回计数 Version）变化时推送完整快照，终态后关闭 channel。
// 其他进程的写回同样通过轮询发现（迁移计划第 7 条）。每轮只读取指纹列（engine.LoadFingerprint），
// 指纹变化时才读取完整快照，订阅者的数据库开销不随图快照与 Run 输出的大小增长。
func (e *Engine) Watch(ctx context.Context, taskID string) (<-chan engine.TaskSnapshot, error) {
	owned, err := engine.Owns(e.cfg.DB.WithContext(ctx), engine.KindDurable, taskID)
	if err != nil {
		return nil, err
	}
	if !owned {
		return nil, engine.NotFoundTask(taskID)
	}
	probe := func(ctx context.Context) (string, error) {
		return engine.LoadFingerprint(e.cfg.DB.WithContext(ctx), taskID)
	}
	load := func(ctx context.Context) (engine.TaskSnapshot, error) { return e.Snapshot(ctx, taskID) }
	return engine.PollWatch(ctx, e.opt.PollInterval, probe, load), nil
}

// Close 停止领取新的 Run 与恢复扫描，等待执行中的活动节点结束并正常写回（设计文档 8.7）。
// ctx 到期时中断仍在执行的节点、丢弃其结果并返回 ctx 的错误；这些 Run 保持 RUNNING，
// 由其他进程（或下次启动）在租约过期后按执行器失联回收（recover.go reclaimExpired）。
func (e *Engine) Close(ctx context.Context) error {
	e.cancelStop()
	done := make(chan struct{})
	go func() { e.wg.Wait(); close(done) }()
	defer e.cancelLife()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
