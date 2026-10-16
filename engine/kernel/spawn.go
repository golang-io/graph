package kernel

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
)

// 本文件收拢子任务派发：EffectSpawn 的图解析、子 Task 组装、等待登记与取消。
// fork / 汇合见 join.go；节点执行与失败结束仍在 progress.go；命令组装见 apply.go 的 collectCommands。
// 拆分只为阅读分组，不改变语义。

// applySpawn 按 Effect.Spawns 发起子 Task。Spawn.Graph 是内嵌图（map 循环体），直接使用；
// Spawn.GraphID 引用已保存的子图，从 Env.Graphs 取已加载的版本：
//   - 有未加载的 GraphID 时返回 *NeedGraph（由 Apply 转为 Changes.NeedGraphs，error 为 nil），
//     本次 Apply 没有任何状态改动，引擎在 Apply 之外加载后重新调用（内核不做 IO）；
//   - 加载失败，或加载到的图身份与请求的 GraphID 不一致时，本节点失败（可走错误出口，不按 MaxAttempts 重试）。
func (st *state) applySpawn(r *engine.Run, n graph.Node, eff *node.Effect) error {
	if st.task.Depth >= graph.MaxSubgraphDepth {
		return st.nodeFailed(r, n, fmt.Errorf("spawn: max nesting depth %d exceeded", graph.MaxSubgraphDepth))
	}
	for _, sp := range eff.Spawns {
		if _, dup := st.awaited(r.RunID)[sp.Slot]; dup {
			return st.nodeFailed(r, n, fmt.Errorf("spawn %s: slot %q is already running", n.ID, sp.Slot))
		}
	}
	var missing []string
	for _, sp := range eff.Spawns {
		if id := strings.TrimSpace(sp.GraphID); sp.Graph == nil {
			if _, ok := st.env.Graphs[id]; !ok && !slices.Contains(missing, id) {
				missing = append(missing, id)
			}
		}
	}
	if len(missing) > 0 {
		return &NeedGraph{GraphIDs: missing}
	}
	graphs := make([]graph.Graph, len(eff.Spawns))
	for i, sp := range eff.Spawns {
		if sp.Graph != nil {
			graphs[i] = st.embedded(sp)
			continue
		}
		id := strings.TrimSpace(sp.GraphID)
		got := st.env.Graphs[id]
		if got.Err != nil {
			return st.nodeFailed(r, n, fmt.Errorf("spawn %s: load %q: %w", n.ID, id, got.Err))
		}
		if got.Graph.GraphID != id {
			return st.nodeFailed(r, n, fmt.Errorf("spawn %s: resolver returned graph %q for %q", n.ID, got.Graph.GraphID, id))
		}
		graphs[i] = got.Graph
	}
	return st.spawn(r, n, eff.Spawns, graphs)
}

// embedded 返回内嵌图：图在节点参数中，自带的身份不可信，一律沿用父 Task 的 GraphID / GraphVersion。
func (st *state) embedded(sp node.Spawn) graph.Graph {
	g := *sp.Graph
	g.GraphID, g.GraphVersion = st.task.GraphID, st.task.GraphVersion
	return g
}

// spawn 发起子 Task：graphs[i] 是 spawns[i] 使用的图（身份已确定），子 Task 的 GraphID / GraphVersion 列取自该图。
// 全部子 Task 先校验图定义与输入，任一失败则本节点失败、一个也不发起。
// 发起的等待记入工作视图（Slot → TaskID），Run 进入 WAITING(subgraph)，由 finish 输出 StartChild；
// 引擎收到 StartChild 后把子 Task 记为被等待（durable 置 Awaited=true，immediate 记入内存等待关系）。
func (st *state) spawn(r *engine.Run, n graph.Node, spawns []node.Spawn, graphs []graph.Graph) error {
	t := st.task
	// 子 Task 继承父 Task 的 Policy，调试选项除外（设计文档第 10 节）。
	policy := t.Policy
	policy.Debug = nil
	starts := make([]StartChild, 0, len(spawns))
	for i, sp := range spawns {
		g := graphs[i]
		if err := st.c.reg.Validate(g); err != nil {
			return st.nodeFailed(r, n, fmt.Errorf("spawn %s: %w", n.ID, err))
		}
		input := maps.Clone(sp.Input)
		if input == nil {
			input = map[string]any{}
		}
		if err := CheckInput(g.InputSchema, input); err != nil {
			return st.nodeFailed(r, n, fmt.Errorf("spawn %s: %w", n.ID, err))
		}
		var err error
		if input, err = Normalize(input); err != nil {
			return st.nodeFailed(r, n, fmt.Errorf("spawn %s: %w", n.ID, err))
		}
		child := &engine.Task{
			TaskID: st.env.newID(), TicketID: t.TicketID, EngineKind: t.EngineKind,
			GraphID: g.GraphID, GraphVersion: g.GraphVersion,
			Status: graph.PENDING, Policy: policy, Input: input,
			ParentTaskID: t.TaskID, ParentRunID: r.RunID, Slot: sp.Slot, Depth: t.Depth + 1,
			Graph: g, StartedAt: st.now, CreatedAt: st.now,
		}
		starts = append(starts, StartChild{Task: child})
	}
	for i, sc := range starts {
		st.await(r.RunID, spawns[i].Slot, sc.Task.TaskID)
	}
	st.children = append(st.children, starts...)
	st.park(r)
	return nil
}

// park 让 Run 等待子 Task 结束。
func (st *state) park(r *engine.Run) {
	r.Status, r.WaitReason = graph.WAITING, engine.WaitSubgraph
	st.save(r)
}

// dropChildren 取消本次节点访问中仍在运行的子 Task；Run 离开节点或结束时调用。
// 取消的子 Task 同时从等待视图移除（记入 Changes.Released），引擎清除其等待标记并投递取消。
func (st *state) dropChildren(r *engine.Run) {
	for _, slot := range slices.Sorted(maps.Keys(st.awaited(r.RunID))) {
		id := st.unawait(r.RunID, slot)
		if id != "" {
			st.cancels = append(st.cancels, id)
		}
	}
}
