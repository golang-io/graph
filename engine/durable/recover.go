package durable

import (
	"context"
	"errors"
	"fmt"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
	"gorm.io/gorm/clause"
)

// 恢复扫描（设计文档 8.4、8.5）：构造时执行一次，之后每 LeaseDuration/3 执行一次。不需要额外的消息表，全部根据状态判断：
//   - 租约过期的 RUNNING Run：没有保存结果的视为执行器失联，单行条件更新放回 PENDING（LostCount 加 1、
//     Attempt 不变，不经过内核）；已保存结果（DurableRun.Result，worker 写回失败或在保存结果后退出）的锁 Task 后应用该结果；
//     失联超过 MaxLost 次的锁 Task 后以执行失败（ActivityDone）交给内核；
//   - 子 Task 已结束、父 Run 仍在等待它（Awaited 为 true）：补发 ChildDone（提交后、投递 NotifyParent 前进程退出）；
//   - 子 Task 未结束、父 Run 已不再等待它（Awaited 为 false）：补发 Cancel（提交后、投递 CancelChild 前进程退出）；
//     两项都只查询 tb_graph_task，父子之间的等待关系只由子 Task 的 Awaited 表示；
//   - 本进程编译结果表 byTask 中已不再执行的 Task：删除条目（只清理内存，不写库）。
// 补发与正常投递可能重复，两者都是幂等的；ChildDone 只补发结束时间早于一个租约时长的子 Task，减少与正常投递的重复。

// recoverBatch 是恢复扫描每次查询读取的行数；按主键分页读完。
const recoverBatch = 500

// recoverOnce 执行一轮恢复扫描，返回各项遇到的错误（不因一项失败而跳过其他项）。
// 引擎关闭（stop 已取消）后各项在处理下一行前退出：剩余的行状态不变，由下一轮扫描（本进程重启后或其他进程）处理。
func (e *Engine) recoverOnce() error {
	return errors.Join(e.reclaimExpired(), e.redeliverChildDone(), e.redeliverCancel(), e.pruneCompiled())
}

// pruneCompiled 删除 byTask 中已不再执行的 Task：已由其他进程写入终态、或事务回滚后从未插入的 Task。
// 按 recoverBatch 分批查询仍处于 PENDING / RUNNING / WAITING 的 TaskID，不在结果中的条目删除。
// 只读不写，查询失败时保留条目、下一轮再试。
//
// pruneCompiled drops byTask entries whose task is no longer live in the database.
func (e *Engine) pruneCompiled() error {
	var ids []any
	e.byTask.Range(func(k, _ any) bool {
		ids = append(ids, k)
		return true
	})
	for start := 0; start < len(ids); start += recoverBatch {
		if e.stopping() {
			return nil
		}
		chunk := ids[start:min(start+recoverBatch, len(ids))]
		var live []string
		err := e.cfg.DB.Model(&engine.Task{}).Where(clause.IN{Column: engine.Col("TaskID"), Values: chunk}).
			Where(engine.StatusIn(engine.Live...)).Pluck("TaskID", &live).Error
		if err != nil {
			return fmt.Errorf("durable: prune compiled: %w", err)
		}
		keep := make(map[string]bool, len(live))
		for _, id := range live {
			keep[id] = true
		}
		for _, id := range chunk {
			if !keep[id.(string)] {
				e.byTask.Delete(id)
			}
		}
	}
	return nil
}

// stopping 报告引擎是否正在关闭；恢复扫描据此提前结束。
func (e *Engine) stopping() bool { return e.stop.Err() != nil }

// MaxLost 是同一次执行（Seq、Attempt 不变）允许的执行器失联次数。失联（租约过期且没有保存结果）时 Run 放回 PENDING
// 重新执行，不消耗 Attempt；第 MaxLost+1 次失联时按执行失败（"executor lost N times"）交给内核，
// 由节点的业务重试策略决定重试或失败，防止反复导致进程退出的节点无限重新执行。
const MaxLost = 3

// expiredRun 是恢复扫描读取的租约过期 Run：RunID、TaskID 与当前持有者的令牌（同 job），LostCount 是已失联次数。
// 字段逐个声明而不内嵌 job：GORM 不读写未导出的内嵌结构体。
type expiredRun struct {
	RunID      string `gorm:"column:RunID"`
	TaskID     string `gorm:"column:TaskID"`
	LeaseToken int64  `gorm:"column:LeaseToken"`
	LostCount  int    `gorm:"column:LostCount"`
}

// reclaimExpired 回收租约已过期的 Run，按以下顺序处理每一行：
//   - 没有保存结果且未超过失联上限：requeue 用单行条件更新放回 PENDING（LostCount 加 1），不锁 Task 行、不经过内核；
//   - 否则锁 Task 行写回：已保存结果的直接应用（节点不重新执行）；失联超过上限的以执行失败交给内核。
//
// 旧 worker 之后的续租、保存结果与写回都因 Status 或令牌不一致失败，结果丢弃。
func (e *Engine) reclaimExpired() error {
	now := e.cfg.Now().UnixMilli()
	var errs []error
	for after := ""; ; {
		var expired []expiredRun
		err := e.cfg.DB.Model(&DurableRun{}).Select("RunID", "TaskID", "LeaseToken", "LostCount").
			Where(map[string]any{"EngineKind": engine.KindDurable, "Status": graph.RUNNING}).
			Where(clause.Lt{Column: engine.Col("LeaseUntil"), Value: now}).
			Where(clause.Gt{Column: engine.Col("RunID"), Value: after}).
			Order(engine.Asc("RunID")).Limit(recoverBatch).Find(&expired).Error
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, x := range expired {
			if e.stopping() {
				return errors.Join(errs...)
			}
			if x.LostCount < MaxLost {
				done, err := e.requeue(x, now)
				if err != nil {
					errs = append(errs, fmt.Errorf("durable: recover: requeue run %s: %w", x.RunID, err))
					continue
				}
				if done {
					continue
				}
			}
			j := job{RunID: x.RunID, TaskID: x.TaskID, LeaseToken: x.LeaseToken}
			b, err := e.event(context.Background(), j.TaskID, func(_ *engine.Task, runs map[string]*DurableRun) act {
				r := runs[j.RunID]
				if r == nil || r.Status != graph.RUNNING || r.LeaseToken != j.LeaseToken || r.LeaseUntil >= now {
					return act{}
				}
				if r.Result != nil {
					return applyResult(r)
				}
				if r.LostCount < MaxLost {
					// requeue 未命中但仍可重新执行：读取后状态有变化，留给下一轮扫描。
					return act{}
				}
				return act{ev: kernel.ActivityDone{
					RunID: r.RunID, Seq: r.Seq, Attempt: r.Attempt,
					Err: fmt.Errorf("executor lost %d times", r.LostCount+1),
				}}
			})
			errs = e.keep(errs, b, err)
		}
		if len(expired) < recoverBatch {
			return errors.Join(errs...)
		}
		after = expired[len(expired)-1].RunID
	}
}

// requeue 把执行器失联的 Run 放回 PENDING 立即可领取：LostCount 加 1，清除租约到期时刻，Seq、Attempt 不变，
// 重新执行时幂等键相同（设计文档 8.3）。这是调度动作，不改变图语义，因此只做单行条件更新，不锁 Task 行、不调用 Apply。
// 条件为读取时的状态：仍为 RUNNING、令牌与失联次数未变、租约仍已过期、没有保存结果（有结果时应当应用它）。
// 返回 false 表示未命中（状态已变化或已保存结果），由调用方按写回路径处理。
//
// requeue puts a run whose executor was lost back to PENDING without touching the task row.
func (e *Engine) requeue(x expiredRun, now int64) (bool, error) {
	res := e.cfg.DB.Model(&DurableRun{}).
		Where(map[string]any{"RunID": x.RunID, "Status": graph.RUNNING, "LeaseToken": x.LeaseToken,
			"LostCount": x.LostCount, "Result": nil}).
		Where(clause.Lt{Column: engine.Col("LeaseUntil"), Value: now}).
		Updates(map[string]any{"Status": graph.PENDING, "AvailableAt": now, "LeaseUntil": 0, "LostCount": x.LostCount + 1})
	if res.Error != nil {
		if engine.Retryable(res.Error) {
			return false, nil
		}
		return false, res.Error
	}
	if res.RowsAffected != 1 {
		return false, nil
	}
	// 万一原执行仍在本进程内运行（续租失败但尚未结束），立即中断；它之后的保存结果与写回会因 Status 不一致而丢弃。
	e.interruptLocal(x.RunID, x.LeaseToken)
	e.cfg.Changed(x.TaskID)
	e.kickAll()
	return true, nil
}

// redeliverChildDone 对已结束、父 Run 仍在等待（Awaited 为 true）的子 Task 补发 ChildDone 到父 Task。
// 只补发结束时间早于一个租约时长的子 Task，给正常投递留出时间；父 Run 收到结果的写回清除 Awaited，之后不再补发。
// 父 Run 已不再等待时内核忽略 ChildDone，重复补发没有副作用。
//
// redeliverChildDone re-posts ChildDone for finished children whose parent run still awaits them.
func (e *Engine) redeliverChildDone() error {
	grace := e.cfg.Now().Add(-e.opt.LeaseDuration)
	var errs []error
	for after := ""; ; {
		var children []DurableTask
		err := e.cfg.DB.Select("TaskID", "ParentTaskID", "ParentRunID", "Slot", "Status", "Output", "Error").
			Where(map[string]any{"EngineKind": engine.KindDurable, "Awaited": true}).
			Where(engine.StatusIn(graph.SUCCEEDED, graph.FAILED, graph.CANCELED)).
			Where(clause.Lt{Column: engine.Col("FinishedAt"), Value: grace}).
			Where(clause.Gt{Column: engine.Col("TaskID"), Value: after}).
			Order(engine.Asc("TaskID")).Limit(recoverBatch).Find(&children).Error
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, c := range children {
			if e.stopping() {
				return errors.Join(errs...)
			}
			b, err := e.post(c.ParentTaskID, kernel.ChildDone{
				RunID: c.ParentRunID, Slot: c.Slot, ChildTaskID: c.TaskID,
				Status: c.Status, Output: c.Output, Error: c.Error,
			})
			errs = e.keep(errs, b, err)
		}
		if len(children) < recoverBatch {
			return errors.Join(errs...)
		}
		after = children[len(children)-1].TaskID
	}
}

// redeliverCancel 对未结束、父 Run 已不再等待（Awaited 为 false）的子 Task 补发 Cancel。
// 父 Run 在子 Task 未结束时放弃等待（any 汇合落选、父 Task 取消或结束，设计文档 5.4）的写回同时清除 Awaited 并输出 CancelChild，
// 因此 Awaited 为 false 的未结束子 Task 就是 CancelChild 未送达的子 Task。已结束的 Task 忽略 Cancel，重复补发没有副作用。
//
// redeliverCancel re-posts Cancel for live children that their parent run no longer awaits.
func (e *Engine) redeliverCancel() error {
	var errs []error
	for after := ""; ; {
		var children []string
		err := e.cfg.DB.Model(&DurableTask{}).
			Where(map[string]any{"EngineKind": engine.KindDurable, "Awaited": false}).
			Where(clause.Neq{Column: engine.Col("ParentRunID"), Value: ""}).
			Where(engine.StatusIn(engine.Live...)).
			Where(clause.Gt{Column: engine.Col("TaskID"), Value: after}).
			Order(engine.Asc("TaskID")).Limit(recoverBatch).Pluck("TaskID", &children).Error
		if err != nil {
			return errors.Join(append(errs, err)...)
		}
		for _, id := range children {
			if e.stopping() {
				return errors.Join(errs...)
			}
			b, err := e.post(id, kernel.Cancel{Reason: parentEndedReason})
			errs = e.keep(errs, b, err)
		}
		if len(children) < recoverBatch {
			return errors.Join(errs...)
		}
		after = children[len(children)-1]
	}
}

// parentEndedReason 是恢复扫描补发 Cancel 的原因。
const parentEndedReason = "parent run no longer awaits"

// keep 处理一次补发写回的结果：成功时完成提交后的处理（含继续投递），失败时记下错误（Task 已不存在除外）。
func (e *Engine) keep(errs []error, b *batch, err error) []error {
	if err != nil {
		if !errors.Is(err, engine.ErrNotFound) {
			errs = append(errs, fmt.Errorf("durable: recover: %w", err))
		}
		return errs
	}
	e.settle(b)
	return errs
}
