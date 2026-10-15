package kernel

import (
	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
)

// 本文件收拢 fork / 汇合推进：子 Run 派生、到达汇合点、落选分支跳过与汇合进入。
// 节点执行、子任务派发、失败与结束仍在 progress.go；导航计算（汇合点查找、输出合并）在 nav.go。
// 拆分只为阅读分组，不改变语义。

// fork 让 r 转为 WAITING(join)，为每条出边插入一个子 Run。
// 汇合点为各分支共同到达的最近节点。
func (st *state) fork(r *engine.Run, outs []graph.Edge) error {
	r.Status, r.WaitReason = graph.WAITING, engine.WaitJoin
	r.JoinNodeID = st.c.JoinTarget(outs)
	r.LiveChildren, r.Arrivals = len(outs), nil
	st.save(r)
	for _, e := range outs {
		// 每个分支复制一份父 Run 的节点输出，分支内的写入互不影响。
		child := st.branch(r, e.ID, r.Outputs.Clone())
		if err := st.enter(child, e.ToNode(), e.ID); err != nil {
			return err
		}
		// 分支已让 Task 结束（fail_fast）或 any 汇合已完成时，不再派发后续分支；
		// 此时父 Run 已离开 WAITING(join)，未派发分支的计数不再使用。
		if graph.IsTerminal(st.task.Status) || r.Status != graph.WAITING || r.WaitReason != engine.WaitJoin {
			return nil
		}
	}
	return nil
}

// branch 新建 p 的子 Run，outs 为子 Run 的初始节点输出（调用方已复制）。
func (st *state) branch(p *engine.Run, slot string, outs engine.Outputs) *engine.Run {
	if outs == nil {
		outs = engine.Outputs{}
	}
	child := &engine.Run{
		RunID: st.env.newID(), TaskID: p.TaskID, EngineKind: p.EngineKind,
		ParentRunID: p.RunID, Slot: slot, NodeID: p.NodeID,
		Outputs: outs, Status: graph.PENDING, CreatedAt: st.now,
	}
	st.insert(child)
	return child
}

// arrive 处理子 Run r 到达父 Run p 的汇合点。all 汇合在全部子 Run 结束时只触发一次；
// any 汇合只采纳第一个到达者，其余未结束的后代置为 SKIPPED。
// 汇合方式取汇合节点的声明（Compiled.JoinModes），未声明时按 all 处理。
func (st *state) arrive(p *engine.Run, r *engine.Run) error {
	r.Status = graph.SUCCEEDED
	st.save(r)
	p.LiveChildren--
	st.save(p)
	if st.c.JoinModes[p.JoinNodeID] == node.JoinAny {
		p.Arrivals = map[string]engine.Outputs{r.Slot: r.Outputs}
		st.skipDescendants(p, r.RunID)
		p.LiveChildren = 0
		return st.settle(p)
	}
	if p.Arrivals == nil {
		p.Arrivals = map[string]engine.Outputs{}
	}
	p.Arrivals[r.Slot] = r.Outputs
	if p.LiveChildren <= 0 {
		return st.settle(p)
	}
	return nil
}

// skipDescendants 把 p 除 keep 以外的所有未结束后代置为 SKIPPED（any 汇合的落选分支），并取消它们的子 Task。
// 执行中的落选分支之后交回的 ActivityDone 因 Run 已不是 RUNNING 而被忽略。
func (st *state) skipDescendants(p *engine.Run, keep string) {
	byParent := map[string][]*engine.Run{}
	for _, r := range st.list(engine.Live...) {
		byParent[r.ParentRunID] = append(byParent[r.ParentRunID], r)
	}
	queue := []string{p.RunID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		for _, r := range byParent[id] {
			if r.RunID == keep {
				continue
			}
			st.skip(r)
			queue = append(queue, r.RunID)
		}
	}
}

// settle 在父 Run 的子 Run 全部结束后调用：有到达者时按 Slot 合并各分支输出并进入汇合节点；
// 无人到达时父 Run 结束。
func (st *state) settle(p *engine.Run) error {
	if len(p.Arrivals) == 0 {
		clearJoin(p)
		p.Status = graph.SUCCEEDED
		st.save(p)
		return st.ended(p)
	}
	join := p.JoinNodeID
	p.Outputs = mergeArrivals(p.Arrivals)
	clearJoin(p)
	return st.enter(p, join, "")
}

// clearJoin 清空父 Run 上的汇合字段。
func clearJoin(p *engine.Run) {
	p.WaitReason, p.JoinNodeID = "", ""
	p.LiveChildren, p.Arrivals = 0, nil
}
