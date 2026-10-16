package service

import (
	"context"
	"errors"
	"io"
	"reflect"
	"slices"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
)

// SnapshotStream 逐次返回 WatchEvent。一条连接的第一个事件是完整快照，之后是增量。
// Recv 在终态事件发出后返回 io.EOF；ctx 取消时返回 ctx 的错误。
//
// SnapshotStream yields a full snapshot first, then deltas; io.EOF after the terminal event.
type SnapshotStream interface {
	Recv() (WatchEvent, error)
	Close() error
}

// errWatchClosed 表示引擎在 Task 结束前关闭了快照 channel（读取失败或引擎关闭）。
var errWatchClosed = errors.New("graph service: watch closed before task finished")

// newSnapshotStream 把引擎 Watch 返回的完整快照 channel 转为增量事件流。
// 引擎只推送完整快照；增量在这里相对上一次推送计算，HTTP 推送格式因此与引擎无关。
func newSnapshotStream(ctx context.Context, cancel context.CancelFunc, ch <-chan engine.TaskSnapshot) SnapshotStream {
	return &snapshotStream{ctx: ctx, cancel: cancel, ch: ch}
}

type snapshotStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	ch     <-chan engine.TaskSnapshot
	prev   engine.TaskSnapshot // 上一次推送对应的快照（不含图），用于计算增量
	done   bool                // 终态事件已发出
}

// Recv 阻塞到引擎推送下一份快照，返回相对上一次推送的事件。
func (s *snapshotStream) Recv() (WatchEvent, error) {
	if s.done {
		return WatchEvent{}, io.EOF
	}
	select {
	case <-s.ctx.Done():
		return WatchEvent{}, s.ctx.Err()
	case snap, ok := <-s.ch:
		if !ok {
			if err := s.ctx.Err(); err != nil {
				return WatchEvent{}, err
			}
			return WatchEvent{}, errWatchClosed
		}
		ev := DiffSnapshot(s.prev, snap)
		s.prev = snap
		// 图快照只随完整快照发出，比较增量时不需要保留。
		s.prev.Task.Graph = graph.Graph{}
		s.done = graph.IsTerminal(snap.Task.Status)
		return ev, nil
	}
}

// Close 结束订阅；引擎在 ctx 取消后关闭 channel。
func (s *snapshotStream) Close() error {
	s.cancel()
	return nil
}

// WatchEvent 是 Watch 的一次推送，也是 GET /tasks/{id}/watch 每个 SSE 事件的 data。
//
// Snapshot 非 nil 时是完整快照，订阅方用它替换该 Task 的本地状态。
// 一条连接的第一个事件总是如此。
//
// Snapshot 为 nil 时，是相对上一次推送的增量：
//   - Task：当前任务行（有变化时才有）。Task.Graph 清空后省略，订阅方保留已有的图。
//   - Runs：本次有变化的 Run，按 RunID 替换或追加。只续了租约的 Run 不算变化。
//   - RemovedRunIDs：从上一次推送之后消失的 Run。
type WatchEvent struct {
	Snapshot      *engine.TaskSnapshot `json:"Snapshot,omitempty"`
	Task          *engine.Task         `json:"Task,omitempty"`
	Runs          []engine.Run         `json:"Runs,omitempty"`
	RemovedRunIDs []string             `json:"RemovedRunIDs,omitempty"`
}

// DiffSnapshot 计算从 prev 到 next 的一次推送。
// prev.Task.TaskID 为空表示还没有推送过，返回完整快照。
func DiffSnapshot(prev, next engine.TaskSnapshot) WatchEvent {
	if prev.Task.TaskID == "" {
		snap := next
		snap.Task = *next.Task.Clone()
		snap.Runs = slices.Clone(next.Runs)
		if snap.Runs == nil {
			snap.Runs = []engine.Run{}
		}
		return WatchEvent{Snapshot: &snap}
	}
	ev := WatchEvent{}
	if !sameTask(prev.Task, next.Task) {
		// 先从局部副本排除图，避免深复制增量中不会发送的整张图。
		task := next.Task
		task.Graph = graph.Graph{}
		ev.Task = task.Clone()
	}
	prevRuns := make(map[string]engine.Run, len(prev.Runs))
	for _, r := range prev.Runs {
		prevRuns[r.RunID] = r
	}
	seen := make(map[string]bool, len(next.Runs))
	for _, r := range next.Runs {
		seen[r.RunID] = true
		old, ok := prevRuns[r.RunID]
		if ok && sameRun(old, r) {
			continue
		}
		ev.Runs = append(ev.Runs, *r.Clone())
	}
	for id := range prevRuns {
		if !seen[id] {
			ev.RemovedRunIDs = append(ev.RemovedRunIDs, id)
		}
	}
	slices.Sort(ev.RemovedRunIDs)
	return ev
}

// ApplyWatch 把一次推送合并进已有快照。cur 为 nil 时只接受完整快照。
// 增量的 Task 不含图，合并结果沿用 cur.Task.Graph。
func ApplyWatch(cur *engine.TaskSnapshot, ev WatchEvent) engine.TaskSnapshot {
	if ev.Snapshot != nil {
		return *ev.Snapshot
	}
	if cur == nil {
		return engine.TaskSnapshot{}
	}
	out := *cur
	out.Runs = slices.Clone(cur.Runs)
	if ev.Task != nil {
		g := out.Task.Graph
		out.Task = *ev.Task
		out.Task.Graph = g
	}
	if len(ev.RemovedRunIDs) > 0 {
		drop := make(map[string]bool, len(ev.RemovedRunIDs))
		for _, id := range ev.RemovedRunIDs {
			drop[id] = true
		}
		kept := make([]engine.Run, 0, len(out.Runs))
		for _, r := range out.Runs {
			if !drop[r.RunID] {
				kept = append(kept, r)
			}
		}
		out.Runs = kept
	}
	index := make(map[string]int, len(out.Runs))
	for i, r := range out.Runs {
		index[r.RunID] = i
	}
	for _, r := range ev.Runs {
		if i, ok := index[r.RunID]; ok {
			out.Runs[i] = r
			continue
		}
		index[r.RunID] = len(out.Runs)
		out.Runs = append(out.Runs, r)
	}
	return out
}

// sameTask 比较两份任务行；图在一次执行中不变，不参与比较。
func sameTask(a, b engine.Task) bool {
	a.Graph, b.Graph = graph.Graph{}, graph.Graph{}
	return reflect.DeepEqual(a, b)
}

// sameRun 比较两份 Run 行。
func sameRun(a, b engine.Run) bool {
	return reflect.DeepEqual(a, b)
}
