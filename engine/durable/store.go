package durable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
	"gorm.io/gorm"
)

// 本文件定义 durable 引擎的表行结构与写回事务（设计文档 8.4）：
//   - DurableTask / DurableRun 内嵌 engine.Task / engine.Run，并附加上本引擎私有的调度与持久化列；
//   - 一次写回 = 锁 Task 行 → 读取该 Task 的全部 Run 并按 Awaited 构造等待视图 → 决定事件 → Apply → 写回 → 提交；
//   - 等待关系只记在子 Task 行上：子 Task 的 Awaited 表示父 Run 仍在等待它（loadAwaiting 构造成
//     Env.Awaiting 传给 Apply，writeChanges 按 Changes.Released 清除），父子关系由子 Task 的
//     ParentTaskID / ParentRunID / Slot 记录；
//   - 写回已有 Run 的条件为读取时的 Status 与 LeaseToken（领取不锁 Task 行，可能与写回并发）；
//     任何一行没有命中就回滚并重做整个事务（重新读取、重新 Apply），见设计文档 8.4、迁移计划第 33 条；
//   - StartChild 在同一事务内插入子 Task 并对它应用 Started；
//   - NotifyParent / CancelChild 在提交后各自用独立事务投递到目标 Task（设计文档 8.5）；
//   - 事务内不调用任何业务应用代码：Apply 不做 IO，Observer 回调记入 batch、提交后调用（notify）；
//     Apply 缺少按 GraphID 引用的子图时在 Changes.NeedGraphs 中返回，txn 回滚后在事务外加载、再重做整个事务。

// DurableTask 是 durable 引擎在 tb_graph_task 表上的行结构：内嵌语义模型 engine.Task，并加上 Awaited 列。
//
// Awaited 表示父 Run 仍在等待本子 Task 的结果（「已结束」与「父 Run 已收到」是两件事，前者看 Status，后者看它）：
// 子 Task 插入时为 true；父 Task 的写回事务在父 Run 收到结果、或放弃等待（取消、any 汇合落选、父 Task 结束）时
// 置为 false。durable 在写回父 Task 前据此构造等待视图（Env.Awaiting），恢复扫描据此补发 ChildDone 与 Cancel。
//
// IdxTaskAwaited（Awaited, Status）服务于 durable 恢复扫描的两项查询：已结束且仍被等待的子 Task（补发 ChildDone）、
// 未结束且不再被等待的子 Task（补发 Cancel）。构造等待视图按 ParentTaskID 查询，走 IdxTaskParent。
//
// DurableTask is the durable table row for tb_graph_task, embedding engine.Task with Awaited.
type DurableTask struct {
	engine.Task `gorm:"embedded"`
	Awaited     bool `gorm:"column:Awaited"`
}

// TableName 固定表名。
func (DurableTask) TableName() string { return "tb_graph_task" }

// DurableRun 是 durable 引擎在 tb_graph_run 表上的行结构（设计文档 8.2–8.4）：
// 内嵌语义模型 engine.Run，并加上租约、失联次数与已保存的活动结果列。
//
// DurableRun is the durable table row for tb_graph_run, embedding engine.Run with scheduling columns.
type DurableRun struct {
	engine.Run `gorm:"embedded"`
	// LeaseUntil 是租约到期时刻（Unix 毫秒），只在 RUNNING 时非零；过期后由恢复扫描回收。
	LeaseUntil int64 `gorm:"column:LeaseUntil"`
	// LeaseToken 每次领取加 1，只由领取修改；续租与写回都要求令牌一致，不一致说明租约已被收回，结果丢弃。
	// 与业务重试次数 Attempt 无关。
	LeaseToken int64 `gorm:"column:LeaseToken"`
	// LostCount 是同一次执行（Seq、Attempt 不变）中执行器失联（租约过期被回收）的次数。
	// 回收时加 1、Run 回到 PENDING 重新领取，不消耗 Attempt；超过上限时 durable 以执行失败交给内核。
	// Run 的 Seq 或 Attempt 变化（进入新节点、业务重试）时由写回清零。
	LostCount int `gorm:"column:LostCount"`
	// Result 是活动节点已执行完、尚未应用到 Task 的结果。worker 先用单行条件更新写入（不锁 Task 行），
	// 再在写回事务中应用；Run 离开 RUNNING 时清空。写回失败或进程在两步之间退出时，恢复扫描在租约过期后
	// 直接应用它，不重新执行节点。
	Result *RunResult `gorm:"column:Result;serializer:graphjson"`
}

// TableName 固定表名。
func (DurableRun) TableName() string { return "tb_graph_run" }

// RunResult 是 DurableRun.Result 的内容：一次活动节点执行（Seq、Attempt 标识）的输出或错误。
// Output 是 node.NodeOutput 的 JSON，写入前已确认可以序列化；Error 非空表示执行失败。
//
// RunResult is one activity execution's output or error, keyed by Seq and Attempt.
type RunResult struct {
	Seq     int64           `json:"Seq"`
	Attempt int             `json:"Attempt"`
	Output  json.RawMessage `json:"Output,omitempty"`
	Error   string          `json:"Error,omitempty"`
}

// taskIndex 与 runIndex 仅用于建表时声明跨越公共语义列与 durable 私有列的组合索引，
// 使 engine 包的公共模型无需感知 durable 的索引。
type taskIndex struct {
	Awaited bool             `gorm:"column:Awaited;index:IdxTaskAwaited,priority:1"`
	Status  graph.StatusCode `gorm:"column:Status;size:16;index:IdxTaskAwaited,priority:2"`
}

func (taskIndex) TableName() string { return "tb_graph_task" }

type runIndex struct {
	EngineKind  string           `gorm:"column:EngineKind;size:32;index:IdxRunClaim,priority:1;index:IdxRunLease,priority:1"`
	Status      graph.StatusCode `gorm:"column:Status;size:16;index:IdxRunClaim,priority:2;index:IdxRunLease,priority:2"`
	AvailableAt int64            `gorm:"column:AvailableAt;index:IdxRunClaim,priority:3"`
	LeaseUntil  int64            `gorm:"column:LeaseUntil;index:IdxRunLease,priority:3"`
}

func (runIndex) TableName() string { return "tb_graph_run" }

// migrate 建立执行表及 durable 专属列与组合索引。
func migrate(ctx context.Context, db *gorm.DB) error {
	gdb := db.WithContext(ctx)
	if err := gdb.AutoMigrate(&DurableTask{}, &DurableRun{}); err != nil {
		return fmt.Errorf("graph runtime: migrate durable tables: %w", err)
	}
	m := gdb.Migrator()
	if !m.HasIndex(&taskIndex{}, "IdxTaskAwaited") {
		if err := m.CreateIndex(&taskIndex{}, "IdxTaskAwaited"); err != nil {
			return fmt.Errorf("graph runtime: create index IdxTaskAwaited: %w", err)
		}
	}
	for _, name := range []string{"IdxRunClaim", "IdxRunLease"} {
		if !m.HasIndex(&runIndex{}, name) {
			if err := m.CreateIndex(&runIndex{}, name); err != nil {
				return fmt.Errorf("graph runtime: create index %s: %w", name, err)
			}
		}
	}
	return nil
}

// loadRuns 读取 Task 的所有 DurableRun 行，按 RunID 排序。
func loadRuns(db *gorm.DB, taskID string) ([]DurableRun, error) {
	var runs []DurableRun
	err := db.Where(map[string]any{"TaskID": taskID}).Order(engine.Asc("RunID")).Find(&runs).Error
	return runs, err
}

// errStale 表示写回时某个 Run 已被并发修改（被其他 worker 领取），需要重做事务。
var errStale = errors.New("durable: run changed concurrently")

// maxTxnAttempts 是一次写回在并发冲突（errStale、死锁、锁等待超时、SQLite busy）时的最多尝试次数。
const maxTxnAttempts = 9

// act 是在事务内锁住 Task 后决定的动作：应用事件 ev；或 fail 非空时按引擎发现的缺陷写入 kernel.Fail 的结果
// （例如活动节点无法准备调用）。两者都为空表示无需写回（如租约令牌已不一致）。
type act struct {
	ev   kernel.Event
	fail error
}

func (a act) none() bool { return a.ev == nil && a.fail == nil }

// decide 在事务内根据锁住的 Task 与它的全部 Run（RunID → DurableRun）决定动作。
type decide func(t *engine.Task, runs map[string]*DurableRun) act

// batch 汇总一个事务写入的内容，提交后用于通知、中断本地执行、唤醒 worker 与跨 Task 投递。
type batch struct {
	head  *engine.Task     // 事务目标 Task 写回后的状态（无改动时为读取时的状态），用于回执
	tasks []string         // 写入过的 TaskID（含同一事务内发起的子 Task）
	runs  []DurableRun     // 写入过的 Run 行（含租约令牌）
	after []kernel.Command // 提交后投递的 NotifyParent / CancelChild
	fails []error          // 写入 kernel.Fail 结果的原因；提交后才上报 Observer，回滚重做时随 batch 丢弃，不重复上报
	// graphs 是本次写回已加载的子图（GraphID → 加载结果），作为 kernel.Env.Graphs 传给 Apply。
	// 同一次 txn 的各次重做共用它：已加载的子图不重复加载。
	graphs map[string]kernel.LoadedGraph
}

// txn 在事务内执行 fn；遇到 errStale 或可重试的数据库冲突时回滚并重做，每次重做使用新的 batch。
// Apply 报告缺少子图（Changes.NeedGraphs，由 applyIn 包装为 *kernel.NeedGraph 触发回滚）时同样回滚，
// 在事务之外用 engine.LoadGraphs 加载后重做；加载不计入冲突重试次数，每次至少加载一个新的子图，因此重做次数有界。
func (e *Engine) txn(ctx context.Context, fn func(tx *gorm.DB, b *batch) error) (*batch, error) {
	graphs := map[string]kernel.LoadedGraph{}
	for attempt := 1; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		b := &batch{graphs: graphs}
		err := e.cfg.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error { return fn(tx, b) })
		if err == nil {
			return b, nil
		}
		if ids, ok := kernel.AsNeedGraph(err); ok {
			if err := engine.LoadGraphs(ctx, e.cfg.ResolveGraph, ids, graphs); err != nil {
				return nil, err
			}
			attempt--
			continue
		}
		if !(errors.Is(err, errStale) || engine.Retryable(err)) || attempt >= maxTxnAttempts {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(2<<attempt) * time.Millisecond):
		}
	}
}

// event 对已存在的 Task 执行一次写回：锁 Task 行、读取全部 Run，由 d 决定动作后 Apply 并写回。
// Task 不存在或不属于本引擎时返回 ErrNotFound；Apply 的拒绝类错误原样返回且不写入任何改动。
func (e *Engine) event(ctx context.Context, taskID string, d decide) (*batch, error) {
	return e.txn(ctx, func(tx *gorm.DB, b *batch) error {
		t, err := lockTask(tx, taskID)
		if err != nil {
			return err
		}
		b.head = t
		rows, err := loadRuns(tx, taskID)
		if err != nil {
			return err
		}
		byID := make(map[string]*DurableRun, len(rows))
		for i := range rows {
			byID[rows[i].RunID] = &rows[i]
		}
		a := d(t, byID)
		if a.none() {
			return nil
		}
		_, err = e.applyIn(tx, t, rows, a, false, b)
		return err
	})
}

// loadAwaiting 查询 taskID 下仍被等待的子 Task，构造传给 Apply 的等待视图（RunID → Slot → TaskID）。
// 父 Run 仍在等待的子 Task 就是 ParentTaskID = taskID 且 Awaited 为 true 的 Task。
// 只有 WAITING(subgraph) 的 Run 才等待子 Task；没有这样的 Run 时不查询，查询走 IdxTaskParent。
// 子 Task 指向的 Run 不处于 WAITING(subgraph) 时不计入（离开等待的写回已在同一事务内清除标记，正常情况下不出现）。
//
// loadAwaiting builds the awaiting view Apply reads from children still marked Awaited.
func loadAwaiting(db *gorm.DB, taskID string, runs []*engine.Run) (map[string]map[string]string, error) {
	waiting := make(map[string]bool)
	for _, r := range runs {
		if r != nil && r.Status == graph.WAITING && r.WaitReason == engine.WaitSubgraph {
			waiting[r.RunID] = true
		}
	}
	if len(waiting) == 0 {
		return nil, nil
	}
	var children []DurableTask
	if err := db.Select("TaskID", "ParentRunID", "Slot").
		Where(map[string]any{"ParentTaskID": taskID, "Awaited": true}).Find(&children).Error; err != nil {
		return nil, err
	}
	out := make(map[string]map[string]string, len(children))
	for _, c := range children {
		if !waiting[c.ParentRunID] {
			continue
		}
		m := out[c.ParentRunID]
		if m == nil {
			m = make(map[string]string)
			out[c.ParentRunID] = m
		}
		m[c.Slot] = c.TaskID
	}
	return out, nil
}

// lockTask 加行锁读取 Task；SQLite 由单写事务（_txlock=immediate）保证串行。
func lockTask(tx *gorm.DB, taskID string) (*engine.Task, error) {
	var t engine.Task
	err := engine.Locking(tx).Where(map[string]any{"TaskID": taskID}).Take(&t).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, engine.NotFoundTask(taskID)
	}
	if err != nil {
		return nil, err
	}
	if t.EngineKind != engine.KindDurable {
		return nil, engine.NotFoundTask(taskID)
	}
	return &t, nil
}

// applyIn 在事务内对 Task t 执行动作 a 并写回，返回写回后的 Task。
// fresh 为 true 表示 t 尚未写入（Start 或 StartChild）：写回时插入 Task 行。
// Apply 返回内核缺陷类错误时改为写入 kernel.Fail 的结果（设计文档 5.5），错误记入 b.fails、提交后上报；
// 拒绝类错误原样返回；Changes.NeedGraphs 非空时返回 *kernel.NeedGraph，由 txn 回滚并在事务外加载子图后重做。
// StartChild 在同一事务内递归处理；NotifyParent / CancelChild 记入 b.after，提交后投递。
func (e *Engine) applyIn(tx *gorm.DB, t *engine.Task, prev []DurableRun, a act, fresh bool, b *batch) (*engine.Task, error) {
	runs := make([]*engine.Run, len(prev))
	for i := range prev {
		runs[i] = &prev[i].Run
	}
	env := e.env()
	env.Graphs = b.graphs
	if !fresh {
		// 等待视图与 Apply 同一事务内读取：新建的 Task 还没有子 Task，只查已存在的 Task。
		// Fail 路径同样需要它（结束时放弃等待的子 Task 要清除标记）。
		awaiting, err := loadAwaiting(tx, t.TaskID, runs)
		if err != nil {
			return nil, err
		}
		env.Awaiting = awaiting
	}
	var ch kernel.Changes
	var cmds []kernel.Command
	if a.fail != nil {
		b.fails = append(b.fails, fmt.Errorf("durable: task %s: %w", t.TaskID, a.fail))
		ch, cmds = kernel.Fail(t, runs, a.fail, env)
	} else {
		var err error
		ch, cmds, err = kernel.Apply(e.cfg.Nodes, t, runs, a.ev, env)
		if err != nil {
			if kernel.IsRejection(err) {
				return nil, err
			}
			b.fails = append(b.fails, fmt.Errorf("durable: task %s: %w", t.TaskID, err))
			ch, cmds = kernel.Fail(t, runs, err, env)
		} else if len(ch.NeedGraphs) > 0 {
			return nil, &kernel.NeedGraph{GraphIDs: ch.NeedGraphs}
		}
	}
	after := t
	if ch.Task != nil {
		after = ch.Task
	}
	written, err := e.writeChanges(tx, prev, ch, after, fresh)
	if err != nil {
		return nil, err
	}
	if b.head == nil || b.head.TaskID == after.TaskID {
		b.head = after
	}
	if fresh || !ch.Empty() {
		b.tasks = append(b.tasks, after.TaskID)
	}
	b.runs = append(b.runs, written...)
	for _, cmd := range cmds {
		switch cmd := cmd.(type) {
		case kernel.StartChild:
			if _, err := e.applyIn(tx, cmd.Task, nil, act{ev: kernel.Started{}}, true, b); err != nil {
				return nil, err
			}
		case kernel.NotifyParent, kernel.CancelChild:
			b.after = append(b.after, cmd)
		}
	}
	return after, nil
}

// writeChanges 写回一次 Apply 的改动。prev 是事务内读取的 DurableRun（未出现在其中的 Run 是新建的，直接插入）。
// 已有 Run 的更新以读取时的 Status、LeaseToken 为条件：未命中说明它在读取后被领取，返回 errStale 重做事务。
// 不再处于 RUNNING 的 Run 清空租约到期时刻与未应用的结果（已应用，或已离开那次执行）；
// LeaseToken 只由领取修改，这里原样写回。
// Changes.Released 的子 Task 在同一事务内置 Awaited=false：之后不再计入等待视图，
// 恢复扫描也不再为它补发 ChildDone；重复清除无副作用。
// fresh 时插入 DurableTask 行（含图快照；子 Task 的 Awaited 为 true），否则按主键更新语义列（不改图快照与 Awaited）。
func (e *Engine) writeChanges(tx *gorm.DB, prev []DurableRun, ch kernel.Changes, t *engine.Task, fresh bool) ([]DurableRun, error) {
	old := make(map[string]DurableRun, len(prev))
	for _, r := range prev {
		old[r.RunID] = r
	}
	written := make([]DurableRun, 0, len(ch.Runs))
	for _, r := range ch.Runs {
		o, ok := old[r.RunID]
		if !ok {
			row := DurableRun{Run: *r}
			if err := tx.Create(&row).Error; err != nil {
				return nil, err
			}
			written = append(written, row)
			continue
		}
		row := DurableRun{
			Run:        *r,
			LeaseUntil: o.LeaseUntil,
			LeaseToken: o.LeaseToken,
			LostCount:  o.LostCount,
			Result:     o.Result,
		}
		if row.Status != graph.RUNNING {
			row.LeaseUntil, row.Result = 0, nil
		}
		// 失联次数只统计同一次执行：Run 进入新节点（Seq 变化）或业务重试（Attempt 变化）后清零。
		if row.Seq != o.Seq || row.Attempt != o.Attempt {
			row.LostCount = 0
		}
		res := tx.Model(&row).Where(map[string]any{"Status": o.Status, "LeaseToken": o.LeaseToken}).
			Select("*").Omit("RunID", "CreatedAt").Updates(&row)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			// MySQL 对取值未变化的行返回 0；重读确认 Status 与 LeaseToken 是否真的变化。
			// 重读必须加锁：MySQL 默认 REPEATABLE READ 下普通读返回事务快照，看不到并发领取已提交的修改，
			// 会把「被并发领取」误判为「取值未变化」而丢失本次更新；加锁读取返回最新已提交版本。
			var cur DurableRun
			if err := engine.Locking(tx).Select("Status", "LeaseToken").Where(map[string]any{"RunID": r.RunID}).Take(&cur).Error; err != nil {
				return nil, err
			}
			if cur.Status != o.Status || cur.LeaseToken != o.LeaseToken {
				return nil, errStale
			}
		}
		written = append(written, row)
	}
	if len(ch.Released) > 0 {
		// 子 Task 行的锁在父 Task 行之后获取，与全局「先父后子」的加锁顺序一致。
		if err := tx.Model(&DurableTask{}).Where(map[string]any{"TaskID": ch.Released}).
			Update("Awaited", false).Error; err != nil {
			return nil, err
		}
	}
	if fresh {
		// 子 Task 由父 Run 发起，插入时父 Run 正在等待它的结果。
		row := DurableTask{Task: *t, Awaited: t.ParentRunID != ""}
		return written, tx.Create(&row).Error
	}
	if ch.Task == nil {
		return written, nil
	}
	return written, engine.UpdateTask(tx, ch.Task)
}

// settle 在事务提交后处理：notify，然后投递跨 Task 动作。
func (e *Engine) settle(b *batch) {
	e.notify(b)
	e.deliver(b.after)
}

// notify 通知 Observer（事务内记下的错误与 Task 变化）、中断本进程内已不再 RUNNING 的执行（取消、any 汇合落选、租约回收），
// 有新的 PENDING Run 时唤醒空闲 worker。中断按 (RunID, LeaseToken) 匹配：Run 在提交后被重新领取时令牌已变化，不受影响。
func (e *Engine) notify(b *batch) {
	for _, err := range b.fails {
		e.cfg.Fail(err)
	}
	for _, id := range b.tasks {
		e.cfg.Changed(id)
	}
	pending := false
	for _, r := range b.runs {
		if r.Status == graph.RUNNING {
			continue
		}
		pending = pending || r.Status == graph.PENDING
		e.interruptLocal(r.RunID, r.LeaseToken)
	}
	if pending {
		e.kickAll()
	}
}

// deliver 依次把 NotifyParent / CancelChild 投递到目标 Task，每个一个独立事务（设计文档 8.5）；
// 投递产生的跨 Task 动作继续排队。两者都是幂等的：父 Run 只接收仍在等待列表中的子任务结果，已结束的 Task 忽略 Cancel。
// 投递失败只上报 Observer，由恢复扫描根据状态补发。
// 引擎关闭（stop 已取消）后不再投递剩余命令：每次投递是一个完整事务，级联投递在高延迟数据库上可能远超 Close 的等待时间；
// 未投递的命令与进程在提交后退出的情况相同，由恢复扫描补发（设计文档 8.5）。
func (e *Engine) deliver(cmds []kernel.Command) {
	queue := append([]kernel.Command(nil), cmds...)
	for len(queue) > 0 && e.stop.Err() == nil {
		cmd := queue[0]
		queue = queue[1:]
		if drop := e.drop.Load(); drop != nil && (*drop)(cmd) {
			continue
		}
		var target string
		var ev kernel.Event
		switch cmd := cmd.(type) {
		case kernel.NotifyParent:
			target, ev = cmd.ParentTaskID, cmd.Event
		case kernel.CancelChild:
			target, ev = cmd.ChildTaskID, kernel.Cancel{Reason: kernel.CancelChildReason}
		default:
			continue
		}
		b, err := e.post(target, ev)
		if err != nil {
			if !errors.Is(err, engine.ErrNotFound) {
				e.cfg.Fail(fmt.Errorf("durable: deliver %T to task %s: %w", cmd, target, err))
			}
			continue
		}
		e.notify(b)
		queue = append(queue, b.after...)
	}
}

// post 在独立事务内把事件应用到目标 Task。
func (e *Engine) post(taskID string, ev kernel.Event) (*batch, error) {
	return e.event(context.Background(), taskID, func(*engine.Task, map[string]*DurableRun) act { return act{ev: ev} })
}
