package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/golang-io/graph"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// 本文件是 immediate、durable 与 service 共用的执行表访问（tb_graph_task / tb_graph_run）。
// 所有条件、排序、更新都用 map / clause 生成，列名由 GORM 加引号
// （PostgreSQL 会把未加引号的大驼峰标识符折叠为小写）。
//
// Shared access to the execution tables; identifiers are always quoted by GORM.

// Col 返回列标识符。
func Col(name string) clause.Column { return clause.Column{Name: name} }

// Desc 返回倒序排序项。
func Desc(name string) clause.OrderByColumn {
	return clause.OrderByColumn{Column: Col(name), Desc: true}
}

// Asc 返回正序排序项。
func Asc(name string) clause.OrderByColumn { return clause.OrderByColumn{Column: Col(name)} }

// Live 是未结束的 Run 状态。
var Live = []graph.StatusCode{graph.PENDING, graph.RUNNING, graph.WAITING}

// StatusIn 生成 Status IN (...) 条件。
func StatusIn(statuses ...graph.StatusCode) clause.IN {
	values := make([]any, len(statuses))
	for i, s := range statuses {
		values[i] = s
	}
	return clause.IN{Column: Col("Status"), Values: values}
}

// Locking 返回加行锁的查询；SQLite 不支持 FOR UPDATE，由单写事务（_txlock=immediate）保证串行。
func Locking(db *gorm.DB) *gorm.DB {
	if db.Dialector.Name() == "sqlite" {
		return db
	}
	return db.Clauses(clause.Locking{Strength: "UPDATE"})
}

// notFound 把 gorm.ErrRecordNotFound 转为 ErrNotFound。
func notFound(err error, what string) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return err
}

// LoadTask 读取 Task；withGraph 为 false 时不读取图快照列（推进路径只需要策略与标记）。
func LoadTask(db *gorm.DB, taskID string, withGraph bool) (*Task, error) {
	var t Task
	q := db.Where(map[string]any{"TaskID": taskID})
	if !withGraph {
		q = q.Omit("Graph")
	}
	if err := q.Take(&t).Error; err != nil {
		return nil, notFound(err, "task "+taskID)
	}
	return &t, nil
}

// LoadRuns 读取 Task 的所有 Run，按 RunID 排序。
func LoadRuns(db *gorm.DB, taskID string) ([]Run, error) {
	var runs []Run
	err := db.Where(map[string]any{"TaskID": taskID}).Order(Asc("RunID")).Find(&runs).Error
	return runs, err
}

// Owns 报告 Task 是否属于该引擎。
func Owns(db *gorm.DB, kind, taskID string) (bool, error) {
	var n int64
	err := db.Model(&Task{}).Where(map[string]any{"TaskID": taskID, "EngineKind": kind}).Count(&n).Error
	return n > 0, err
}

// readTx 在同一个只读事务中执行多次查询，保证读取到一致的数据库快照（避免分步裸读导致状态撕裂）。
// 若调用方已处于事务中则直接复用当前事务连接；SQLite 使用 ReadOnly 事务以建立 WAL 一致性读快照（且不触发 _txlock=immediate 写锁），
// 其他数据库使用 REPEATABLE READ 只读事务。
//
// readTx runs fn inside a consistent read-only transaction, reusing db if already in a transaction.
func readTx(db *gorm.DB, fn func(tx *gorm.DB) error) error {
	if db.Statement != nil {
		if _, ok := db.Statement.ConnPool.(gorm.TxCommitter); ok {
			return fn(db)
		}
	}
	opts := &sql.TxOptions{ReadOnly: true}
	if db.Dialector.Name() != "sqlite" {
		opts.Isolation = sql.LevelRepeatableRead
	}
	return db.Transaction(fn, opts)
}

// ListTasks 按 StartedAt 倒序列出本引擎的根 Task（子 Task 通过 ParentTaskID 访问）。
// q.After 非空时，若 q.AfterAt 已由上层前置解析则直接按 (q.AfterAt, q.After) 过滤；
// 否则在表内解析 q.After 的 StartedAt，游标不存在时返回 ErrNotFound，绝不静默跳过条件返回第一页。
//
// ListTasks lists root tasks of kind ordered by (StartedAt DESC, TaskID DESC).
func ListTasks(db *gorm.DB, kind string, q ListQuery) ([]TaskSummary, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = 50
	}
	q.After = strings.TrimSpace(q.After)
	query := db.Model(&Task{}).Where(map[string]any{"EngineKind": kind, "ParentTaskID": ""})
	if q.GraphID != "" {
		query = query.Where(map[string]any{"GraphID": q.GraphID})
	}
	if q.Status != "" {
		query = query.Where(map[string]any{"Status": q.Status})
	}
	if q.After != "" {
		afterAt := q.AfterAt
		if afterAt.IsZero() {
			var after Task
			if err := db.Select("StartedAt").Where(map[string]any{"TaskID": q.After}).Take(&after).Error; err != nil {
				return nil, notFound(err, "task "+q.After)
			}
			afterAt = after.StartedAt
		}
		query = query.Where(clause.Or(
			clause.Lt{Column: Col("StartedAt"), Value: afterAt},
			clause.And(clause.Eq{Column: Col("StartedAt"), Value: afterAt}, clause.Lt{Column: Col("TaskID"), Value: q.After}),
		))
	}
	var out []TaskSummary
	err := query.Order(clause.OrderBy{Columns: []clause.OrderByColumn{Desc("StartedAt"), Desc("TaskID")}}).Limit(limit).Find(&out).Error
	return out, err
}

// LoadSnapshot 在同一个只读事务中从表里读取 Task 最新快照（Task + Run），避免并发写回提交导致状态撕裂。
//
// LoadSnapshot reads Task and Runs inside a single read-only transaction.
func LoadSnapshot(db *gorm.DB, taskID string) (TaskSnapshot, error) {
	var t *Task
	var runs []Run
	err := readTx(db, func(tx *gorm.DB) error {
		var err error
		t, err = LoadTask(tx, taskID, true)
		if err != nil {
			return err
		}
		runs, err = LoadRuns(tx, taskID)
		return err
	})
	if err != nil {
		return TaskSnapshot{}, err
	}
	return Snapshot(*t, runs), nil
}

// UpdateTask 按主键整行更新 Task 的语义字段，不改 TaskID、Graph（启动时写入后只读）与 CreatedAt。
// 各引擎写回 Task 时共用，保证所有路径更新同一组语义列。
func UpdateTask(tx *gorm.DB, t *Task) error {
	return tx.Model(t).Select("*").Omit("TaskID", "Graph", "CreatedAt").Updates(t).Error
}

// SortRuns 按 RunID（UUID v7，时间有序）排序，快照使用同一顺序。
func SortRuns(runs []Run) {
	slices.SortFunc(runs, func(x, y Run) int { return strings.Compare(x.RunID, y.RunID) })
}

// Snapshot 组装快照；runs 会被排序。Runs 保证非 nil，HTTP 与 SSE 输出空数组而非 null。
func Snapshot(task Task, runs []Run) TaskSnapshot {
	SortRuns(runs)
	if runs == nil {
		runs = []Run{}
	}
	return TaskSnapshot{Task: task, Runs: runs}
}

// Fingerprint 由快照计算 Watch 判断变化用的指纹：Task 的状态与写回计数，各 Run 的状态、节点、Seq。
func Fingerprint(s TaskSnapshot) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s:%d", s.Task.Status, s.Task.Version)
	for _, r := range s.Runs {
		fmt.Fprintf(&b, "|%s:%s:%s:%d", r.RunID, r.Status, r.NodeID, r.Seq)
	}
	return b.String()
}

// LoadFingerprint 在同一个只读事务中从表里计算 Task 当前的指纹，结果与 Fingerprint(LoadSnapshot(...)) 相同。
// 只读取 Task 的 Status、Version 与各 Run 的 RunID、Status、NodeID、Seq，
// 不读取图快照与 JSON 列（输入、输出、状态），Watch 轮询在 Task 无变化时只付出这两次小查询。
//
// LoadFingerprint reads only the fingerprint columns inside a single read-only transaction;
// same result as Fingerprint(LoadSnapshot(...)).
func LoadFingerprint(db *gorm.DB, taskID string) (string, error) {
	var t Task
	var runs []Run
	err := readTx(db, func(tx *gorm.DB) error {
		if err := tx.Select("Status", "Version").Where(map[string]any{"TaskID": taskID}).Take(&t).Error; err != nil {
			return notFound(err, "task "+taskID)
		}
		return tx.Select("RunID", "Status", "NodeID", "Seq").Where(map[string]any{"TaskID": taskID}).Find(&runs).Error
	})
	if err != nil {
		return "", err
	}
	// 与 Snapshot 使用同一排序（Go 字节序），不依赖数据库排序规则。
	// Same order as Snapshot, independent of the database collation.
	SortRuns(runs)
	return Fingerprint(TaskSnapshot{Task: t, Runs: runs}), nil
}

// PollWatch 按 interval 轮询，指纹变化时推送一份完整快照；第一份总是推送。
// 每轮先用 probe 只读取指纹（通常为 LoadFingerprint），与上次推送相同则跳过，变化时才调用 load 读取完整快照。
// 推送终态快照后、ctx 结束时或读取失败时关闭 channel。
//
// PollWatch polls the fingerprint via probe and loads/sends a full snapshot only on change.
func PollWatch(ctx context.Context, interval time.Duration,
	probe func(context.Context) (string, error), load func(context.Context) (TaskSnapshot, error)) <-chan TaskSnapshot {
	if interval <= 0 {
		interval = 200 * time.Millisecond
	}
	ch := make(chan TaskSnapshot)
	go func() {
		defer close(ch)
		timer := time.NewTimer(0)
		defer timer.Stop()
		last := ""
		for first := true; ; first = false {
			if !first {
				timer.Reset(interval)
			}
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if !first {
				fp, err := probe(ctx)
				if err != nil {
					return
				}
				if fp == last {
					continue
				}
			}
			snap, err := load(ctx)
			if err != nil {
				return
			}
			fp := Fingerprint(snap)
			if !first && fp == last {
				continue
			}
			last = fp
			select {
			case ch <- snap:
			case <-ctx.Done():
				return
			}
			if graph.IsTerminal(snap.Task.Status) {
				return
			}
		}
	}()
	return ch
}
