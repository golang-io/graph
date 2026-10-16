package kernel

import (
	"maps"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
)

// 本文件收拢扇出（fork）与多入边汇合（Merge）推进：
//   - 节点命中多条出边时，为每条出边派生一条分支 Run 并逐条向目标节点推进；
//   - 目标节点为多入边 Merge 节点时，按该节点自身的入边就绪状态与 Mode（any / all）门控：
//     all 模式等待该节点的全部入边到达后合并输出并执行一次；
//     any 模式在首条入边到达时立即执行，并跳过仅通向该 Merge 的其余在途分支，后续迟到入边直接忽略。

// fork 在节点命中多条出边时为每条出边创建分支 Run 并逐条进入目标节点。
// 先将全部分支 Run 置入状态表，再依次推进，确保内联分支同步到达终点或触发 Merge(any) 时能完整感知同批兄弟分支。
func (st *state) fork(r *engine.Run, outs []graph.Edge) error {
	r.Status = graph.SUCCEEDED
	st.save(r)
	children := make([]*engine.Run, len(outs))
	for i, e := range outs {
		children[i] = st.branch(r, e, maps.Clone(r.Outputs))
	}
	for i, e := range outs {
		if graph.IsTerminal(st.task.Status) || children[i].Status != graph.PENDING {
			continue
		}
		if err := st.enter(children[i], e.ToNode(), e.ID); err != nil {
			return err
		}
	}
	return nil
}

// branch 新建从父 Run p 沿出边 e 派生的分支 Run，outs 为分支初始节点输出（调用方已克隆）。
func (st *state) branch(p *engine.Run, e graph.Edge, outs engine.Outputs) *engine.Run {
	if outs == nil {
		outs = engine.Outputs{}
	}
	child := &engine.Run{
		RunID: st.env.newID(), TaskID: p.TaskID, EngineKind: p.EngineKind,
		NodeID: e.ToNode(), FromEdgeID: e.ID,
		Outputs: outs, Status: graph.PENDING, CreatedAt: st.now,
	}
	st.insert(child)
	return child
}

// arrive 处理 Run r 经由入边 r.FromEdgeID 到达多入边汇合节点（Merge）。
//   - 若该汇合节点此前已被触发（any 模式下首条边已放行），当前分支直接置为 SKIPPED；
//   - 若是首条到达该节点的入边：解析有效 Mode（含上游动态绑定与调试注入），
//     any 模式立即跳过其余仅通向该节点的分支并执行本节点，all 模式进入 WAITING(join) 等待其余入边；
//   - 若已有 WAITING(join) 的 Run 在等待该节点：登记本条入边的输出，全部入边到齐后合并输出并执行一次该节点。
func (st *state) arrive(r *engine.Run, inEdges []graph.Edge) error {
	joinID := r.NodeID
	via := r.FromEdgeID
	if st.hasTriggered(joinID, r.RunID) {
		r.NodeID = edgeSourceNode(inEdges, via, r.NodeID)
		st.skip(r)
		return st.ended(r)
	}
	waiter := st.findWaitingJoin(joinID, r.RunID)
	if waiter == nil {
		if st.resolveJoinMode(joinID, r.Outputs) == node.JoinAny {
			st.skipLosers(joinID, r.RunID)
			return st.visit(r)
		}
		r.Status, r.WaitReason = graph.WAITING, engine.WaitJoin
		r.Arrivals = map[string]engine.Outputs{via: maps.Clone(r.Outputs)}
		st.save(r)
		return nil
	}

	waiter.Arrivals = maps.Clone(waiter.Arrivals)
	if waiter.Arrivals == nil {
		waiter.Arrivals = map[string]engine.Outputs{}
	}
	waiter.Arrivals[via] = maps.Clone(r.Outputs)
	st.save(waiter)

	// 后续到达的分支已将输出交由 waiter 汇合，自身的 NodeID 保持在入边来源节点，
	// 避免在 waiter 尚未执行完成前出现 NodeID=joinID 且 Status=SUCCEEDED 的记录。
	r.NodeID = edgeSourceNode(inEdges, via, r.NodeID)
	r.Status = graph.SUCCEEDED
	st.save(r)

	for _, edge := range inEdges {
		if _, ok := waiter.Arrivals[edge.ID]; !ok {
			return nil
		}
	}
	waiter.Outputs = mergeArrivals(waiter.Arrivals)
	st.ownOutputs[waiter.RunID] = true
	clearJoin(waiter)
	waiter.StartedAt = st.now
	st.save(waiter)
	return st.visit(waiter)
}

func edgeSourceNode(inEdges []graph.Edge, edgeID, fallback string) string {
	for _, e := range inEdges {
		if e.ID == edgeID {
			return e.FromNode()
		}
	}
	return fallback
}

// resolveJoinMode 解析汇合节点在运行期的有效 Mode（any / all）：
// 合并上游动态绑定与调试注入，未覆盖时回落到节点静态声明（Registry.JoinMode）。
func (st *state) resolveJoinMode(nodeID string, outs engine.Outputs) string {
	n := st.c.node(nodeID)
	inj := st.task.Policy.Debug.InjectFor(nodeID)
	if (inj != nil && len(inj.Vars) > 0) || hasDynamicInput(n.Inputs) {
		vars, _ := projectNodeInputs(st.c.reg, n, st.task.Input, outs, inj)
		params := resolveNodeParams(st.c.reg, n, vars, inj)
		if d, ok := params.(node.JoinModeDeclarer); ok {
			if mode := d.JoinMode(); mode != "" {
				return mode
			}
		}
	}
	if mode := st.c.reg.JoinMode(n); mode != "" {
		return mode
	}
	return node.JoinAll
}

func hasDynamicInput(inputs map[string]graph.InputRef) bool {
	for _, ref := range inputs {
		if ref.From != "" {
			return true
		}
	}
	return false
}

// hasTriggered 报告汇合节点是否已完成或已通过门控（含活动执行、重试延迟与子任务等待）。
// Seq=0 的 Run 是 fork 预建但尚未 enter 的分支，WAITING(join) 尚未通过门控，都不算触发。
func (st *state) hasTriggered(nodeID, excludeRunID string) bool {
	for _, other := range st.runs {
		if other.RunID == excludeRunID {
			continue
		}
		if other.Outputs[nodeID] != nil {
			return true
		}
		if other.NodeID == nodeID && other.Seq > 0 {
			switch other.Status {
			case graph.PENDING, graph.RUNNING:
				return true
			case graph.WAITING:
				if other.WaitReason == engine.WaitDebug || other.WaitReason == engine.WaitSubgraph {
					return true
				}
			}
		}
	}
	return false
}

// findWaitingJoin 查找当前正停在汇合节点 nodeID 上等待其余入边的 Run。
func (st *state) findWaitingJoin(nodeID, excludeRunID string) *engine.Run {
	for _, other := range st.list(graph.WAITING) {
		if other.RunID != excludeRunID && other.NodeID == nodeID && other.WaitReason == engine.WaitJoin {
			return other
		}
	}
	return nil
}

// skipLosers 在 Merge(any) 首条入边触发后，将所有仅通向该汇合节点的其余未结束 Run 置为 SKIPPED 并取消其子 Task。
func (st *state) skipLosers(joinNodeID, winnerRunID string) {
	var outEdges map[string][]graph.Edge
	var memo map[string]bool
	for _, other := range st.list(engine.Live...) {
		if other.RunID == winnerRunID {
			continue
		}
		if outEdges == nil {
			outEdges = st.c.allOutEdges()
			memo = map[string]bool{}
		}
		if onlyLeadsToMemo(outEdges, other.NodeID, joinNodeID, memo) {
			st.skip(other)
		}
	}
}

// clearJoin 清空 Run 上的汇合等待字段。
func clearJoin(r *engine.Run) {
	r.WaitReason, r.Arrivals = "", nil
}
