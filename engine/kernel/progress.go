package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
)

// 本文件实现图语义（设计文档 5.4）：下一个节点的选择、内联节点执行、活动节点前置检查、
// 并行派生与汇合、重试、子任务、错误出口与 Task 结束。所有修改都作用在 state 的工作副本上。

// onStarted 创建根 Run 并从入口节点推进。Task 不是 PENDING 时（重复投递）忽略。
// 运行输入已由 NewTask / spawn 校验、规整并写入 Task.Input；不复制进 Run.Outputs，节点通过 $input 引用。
func (st *state) onStarted() error {
	t := st.task
	root := &engine.Run{
		RunID: st.env.newID(), TaskID: t.TaskID, EngineKind: t.EngineKind,
		Outputs: engine.Outputs{}, Status: graph.PENDING, CreatedAt: st.now,
	}
	t.Status, t.StartedAt = graph.RUNNING, st.now
	st.touchTask()
	st.insert(root)
	return st.enter(root, st.c.entryNode(), "")
}

// onActivityDone 应用活动节点的执行结果。Run 不在 RUNNING 或已不在这次执行上（RunID/Seq/Attempt 不一致）时忽略；
// 这同时覆盖了取消、any 汇合落选之后迟到的结果。
func (st *state) onActivityDone(ev ActivityDone) error {
	r := st.runs[ev.RunID]
	n := st.c.node(r.NodeID)
	out, err := ev.Output, ev.Err
	if err == nil {
		// 活动节点返回非法形态按节点失败处理（可重试），不视为内核缺陷：
		// 业务节点在内核之外执行，其实现问题不应使整个引擎判定为内核故障。
		out, err = st.checkOutput(n, out)
	}
	if err != nil {
		if r.Attempt < st.task.Policy.MaxAttempts {
			r.Attempt++
			r.Status, r.Error = graph.PENDING, err.Error()
			r.AvailableAt = st.nowMS() + st.task.Policy.RetryDelayMS
			st.save(r)
			return nil
		}
		return st.nodeFailed(r, n, err)
	}
	return st.applyOutput(r, n, out)
}

// onChildDone 把子 Task 的结果交给发起它的 Controller（以 EventChildDone 再次调用 Control）。
// 父 Run 不在等待该子 Task（等待视图中 Slot 指向的不是这个 TaskID）时忽略，
// 因此重复送达、迟到的结果都没有副作用。接收后从等待视图移除，记入 Changes.Released。
func (st *state) onChildDone(ev ChildDone) error {
	r := st.runs[ev.RunID]
	st.unawait(r.RunID, ev.Slot)
	r.WaitReason = ""
	st.save(r)
	n := st.c.node(r.NodeID)
	cev := node.Event{Kind: node.EventChildDone, Slot: ev.Slot}
	if ev.Status != graph.SUCCEEDED {
		cev.Error = ev.Error
		if cev.Error == "" {
			cev.Error = "subgraph " + strings.ToLower(string(ev.Status))
		}
	} else {
		cev.Payload = maps.Clone(ev.Output)
		if cev.Payload == nil {
			cev.Payload = map[string]any{}
		}
	}
	// 与首次进入时使用同一组输入（含调试注入的值），Controller 每轮看到的 Vars 一致。
	vars, _ := projectNodeInputs(st.c.reg, n, st.task.Input, r.Outputs, st.task.Policy.Debug.InjectFor(n.ID))
	return st.invoke(r, n, vars, cev)
}

// enter 把 Run 移到节点 to（经由边 via），Seq 加 1，然后判断汇合或执行该节点。
// via 为空表示不经由边进入（入口节点、汇合完成后父 Run 进入汇合节点）。
// 一次节点访问的字段在进入时重置：Attempt 从 1 开始、State 清空，并取消上个节点仍在运行的子 Task。
func (st *state) enter(r *engine.Run, to, via string) error {
	r.FromEdgeID, r.NodeID = via, to
	r.Seq++
	r.Attempt = 1
	r.WaitReason, r.Error, r.State = "", "", nil
	st.dropChildren(r)
	r.StartedAt = st.now
	st.save(r)
	return st.reach(r)
}

// skip 把落选或被忽略的分支 Run 置为 SKIPPED，并取消其子 Task。
func (st *state) skip(r *engine.Run) {
	st.dropChildren(r)
	clearJoin(r)
	r.Status = graph.SKIPPED
	st.save(r)
}

// reach 处理 Run 进入当前节点：多入边节点先走汇合门控（arrive），单入边或入口节点直接执行（visit）。
func (st *state) reach(r *engine.Run) error {
	if inEdges := st.c.inEdges(r.NodeID); len(inEdges) > 1 {
		return st.arrive(r, inEdges)
	}
	return st.visit(r)
}

// gated 报告进入节点前是否需要停住：
//   - 命中显式断点（HasBreakpoint）：所有节点（含内联节点与活动节点）均停住；
//   - Task 暂停或调试单步（Paused / Step）：除起止节点（start / end）外的全部内联节点与活动节点均停住。
func (st *state) gated(nodeID string) bool {
	t := st.task
	if t.Policy.Debug != nil && t.Policy.Debug.HasBreakpoint(nodeID) {
		return true
	}
	if !t.Paused && (t.Policy.Debug == nil || !t.Policy.Debug.Step) {
		return false
	}
	ep, _ := st.c.reg.NodeEndpoints(st.c.node(nodeID))
	if ep.IsEntry() || ep.IsTerminal() {
		return false
	}
	return true
}

// visit 执行 Run 当前所在节点：先过调试关口（断点、单步、暂停），通过后由 proceed 执行内联节点或激活活动节点。
func (st *state) visit(r *engine.Run) error {
	n := st.c.node(r.NodeID)
	if st.gated(n.ID) {
		r.Status, r.WaitReason = graph.WAITING, engine.WaitDebug
		st.save(r)
		return nil
	}
	return st.proceed(r, n)
}

// proceed 在通过调试关口（或 Resume 放行）后执行节点：内联节点在本次 Apply 内同步执行，活动节点做前置检查后置为 PENDING。
func (st *state) proceed(r *engine.Run, n graph.Node) error {
	if st.c.reg.Inline(n.Kind) {
		return st.runInline(r, n)
	}
	return st.activate(r, n)
}

// prepare 按节点 Inputs 取节点输入并合入调试注入。返回的 failure 非空表示节点应直接失败：
// 调试注入要求失败，或输入来源缺失且未被调试注入覆盖（不读到空值）。
func (st *state) prepare(r *engine.Run, n graph.Node) (vars map[string]any, failure error) {
	inj := st.task.Policy.Debug.InjectFor(n.ID)
	vars, missing := projectNodeInputs(st.c.reg, n, st.task.Input, r.Outputs, inj)
	if inj != nil && inj.Failed {
		return vars, errors.New(injectMessage(inj))
	}
	return vars, missing
}

// activate 让 Run 进入活动节点（调试放行之后也走这里）：前置检查失败时直接按节点失败处理，
// 不输出 ExecuteActivity、也不重试（设计文档 5.4）；否则置为 PENDING，由 finish 输出 ExecuteActivity。
func (st *state) activate(r *engine.Run, n graph.Node) error {
	if _, failure := st.prepare(r, n); failure != nil {
		return st.nodeFailed(r, n, failure)
	}
	r.Status, r.AvailableAt = graph.PENDING, st.nowMS()
	st.save(r)
	return nil
}

// runInline 执行内联节点：前置检查与活动节点相同，然后在本次 Apply 内同步调用。
func (st *state) runInline(r *engine.Run, n graph.Node) error {
	vars, failure := st.prepare(r, n)
	if failure != nil {
		return st.nodeFailed(r, n, failure)
	}
	return st.invoke(r, n, vars, node.Event{Kind: node.EventStart})
}

// invoke 同步调用内联节点：实现 Controller 的走 Control（带 Event / State），否则走 Execute。
// 节点返回 error 是业务失败（走错误出口或使 Task 失败）；返回非法形态或 panic 是内核缺陷，Apply 返回 error。
//
// 子 Task 未全部结束时父 Run 不离开节点（设计文档 5.4）：此时 Controller 只能返回 State 或继续 spawn，
// 返回 error、出端点、输出或 finish / fail 都属于内核缺陷。
func (st *state) invoke(r *engine.Run, n graph.Node, vars map[string]any, ev node.Event) (err error) {
	out, callErr, panicked := st.call(r, n, vars, ev)
	if panicked != nil {
		return fmt.Errorf("kernel: inline node %s panic: %v", n.ID, panicked)
	}
	// “等待中不得离开节点”与 NodeOutput.Leaves 共用同一定义，避免两处各自解释形态。
	if waiting := len(st.awaited(r.RunID)); waiting > 0 && (callErr != nil || out.Leaves()) {
		return fmt.Errorf("kernel: node %s returned a result while %d child tasks are still running", n.ID, waiting)
	}
	if callErr != nil {
		return st.nodeFailed(r, n, callErr)
	}
	out, verr := st.checkOutput(n, out)
	if verr != nil {
		return fmt.Errorf("kernel: inline node %s returned invalid output: %w", n.ID, verr)
	}
	return st.applyOutput(r, n, out)
}

// call 调用节点并捕获 panic。内联节点不做 IO，使用 context.Background()。
func (st *state) call(r *engine.Run, n graph.Node, vars map[string]any, ev node.Event) (out node.NodeOutput, err error, panicked any) {
	defer func() {
		if p := recover(); p != nil {
			panicked = p
		}
	}()
	typ, ok := st.c.reg.Lookup(n.Kind)
	if !ok {
		return out, nil, fmt.Sprintf("node kind %q not registered", n.Kind)
	}
	params := resolveNodeParams(st.c.reg, n, vars, st.task.Policy.Debug.InjectFor(n.ID))
	// vars 已由 projectNodeInputs 隔离；节点按只读约定使用，需要写入局部变量时自行复制。
	if ctrl, ok := typ.(node.Controller); ok {
		out, err = ctrl.Control(context.Background(), node.ControlInput{
			NodeID: n.ID, Vars: vars, Params: params,
			Event: ev, State: append(json.RawMessage(nil), r.State...),
		})
		return out, err, nil
	}
	if ev.Kind == node.EventChildDone {
		// 非 Controller 节点不处理子任务完成事件；若收到 EventChildDone 说明状态异常，按内核缺陷拦截。
		// Only Controller nodes handle EventChildDone; reject non-Controller kinds defensively.
		return out, nil, fmt.Sprintf("node kind %q is not a controller", n.Kind)
	}
	exec, ok := typ.(node.Executor)
	if !ok {
		return out, nil, fmt.Sprintf("node kind %q has no executor or controller", n.Kind)
	}
	out, err = exec.Execute(context.Background(), node.NodeInput{
		NodeID: n.ID, Vars: vars, Params: params,
		Key:    node.RunKey{TaskID: r.TaskID, RunID: r.RunID, Seq: r.Seq, Attempt: r.Attempt},
		Lookup: lookup(st.task.Input, r.Outputs),
	})
	return out, err, nil
}

// checkOutput 把 Set 与 Finish.Output 经一次严格 JSON 处理规整（整数为 int64），使 immediate 与经过数据库的 durable
// 对同一值得到相同类型；再经 Registry.CheckOutput 校验形态、Controller 专属字段（State / EffectSpawn）、输出键与端点。
func (st *state) checkOutput(n graph.Node, out node.NodeOutput) (node.NodeOutput, error) {
	set, err := Normalize(out.Set)
	if err != nil {
		return out, fmt.Errorf("node %s output: %w", n.ID, err)
	}
	out.Set = set
	if out.Effect != nil && len(out.Effect.Output) > 0 {
		effOut, err := Normalize(out.Effect.Output)
		if err != nil {
			return out, fmt.Errorf("node %s output: %w", n.ID, err)
		}
		eff := *out.Effect
		eff.Output = effOut
		out.Effect = &eff
	}
	return out, st.c.reg.CheckOutput(n, out)
}

// applyOutput 按 NodeOutput 推进：Effect 类由 applyEffect 处理；无输出且有子任务时继续等待；其余写入输出后沿 Endpoint 继续。
func (st *state) applyOutput(r *engine.Run, n graph.Node, out node.NodeOutput) error {
	if len(out.State) > 0 {
		r.State = append(json.RawMessage(nil), out.State...)
		st.save(r)
	}
	if out.Effect != nil {
		return st.applyEffect(r, n, out.Effect)
	}
	if !out.Leaves() {
		if len(st.awaited(r.RunID)) > 0 {
			st.park(r)
			return nil
		}
		if len(out.State) > 0 {
			return fmt.Errorf("kernel: node %s returned only State without running child tasks", n.ID)
		}
	}
	st.setOutputs(r, n.ID, out.Set)
	return st.next(r, n, out.Endpoint)
}

// lookup 返回 NodeInput.Lookup：按引用读取运行输入或本 Run 已产生的节点输出。
func lookup(input map[string]any, outs engine.Outputs) func(graph.InputRef) (any, bool) {
	return func(ref graph.InputRef) (any, bool) {
		if ref.From == graph.InputSource {
			v, ok := input[ref.Field]
			return engine.CloneValue(v), ok
		}
		v, ok := outs[ref.From][ref.Field]
		return engine.CloneValue(v), ok
	}
}

func (st *state) applyEffect(r *engine.Run, n graph.Node, eff *node.Effect) error {
	// 与 Kind / validateEffect 一致按去空格后的 Kind 分派，避免 " finish " 通过校验却命中 default。
	switch strings.TrimSpace(eff.Kind) {
	case node.EffectFinish:
		output := maps.Clone(eff.Output)
		if output == nil {
			output = map[string]any{}
		}
		r.Status, r.Output, r.State = graph.SUCCEEDED, output, nil
		st.save(r)
		return st.ended(r)
	case node.EffectFail:
		msg := strings.TrimSpace(eff.Message)
		if msg == "" {
			msg = "failed"
		}
		return st.nodeFailed(r, n, errors.New(msg))
	case node.EffectSpawn:
		return st.applySpawn(r, n, eff)
	default:
		return st.nodeFailed(r, n, fmt.Errorf("unknown effect %q", eff.Kind))
	}
}

// 子任务派发见 spawn.go（applySpawn、embedded、spawn、park、dropChildren）。
// next 沿匹配 branch 的出边继续：一条直接进入，多条则 fork。
func (st *state) next(r *engine.Run, n graph.Node, branch string) error {
	outEdges := st.c.outEdges(n.ID)
	outs := matchingOutEdges(outEdges, branch)
	if len(outs) == 0 {
		return st.nodeFailed(r, n, errNoMatchingOutgoing(n.ID, branch, outEdges))
	}
	if len(outs) == 1 {
		return st.enter(r, outs[0].ToNode(), outs[0].ID)
	}
	return st.fork(r, outs)
}

// nodeFailed 处理节点失败：当前 Run 标记为 FAILED，整个 Task 立即终止为 FAILED。
func (st *state) nodeFailed(r *engine.Run, n graph.Node, err error) error {
	msg := fmt.Sprintf("node %s: %v", n.ID, err)
	r.Status, r.Error = graph.FAILED, msg
	st.dropChildren(r)
	st.save(r)
	st.endTask(graph.FAILED, msg)
	return nil
}

// endTask 让 Task 进入终态 status（FAILED / CANCELED），其余未结束的 Run 置为 CANCELED、子 Task 级联取消，
// 有父 Task 时通知父 Run。
func (st *state) endTask(status graph.StatusCode, msg string) {
	t := st.task
	if graph.IsTerminal(t.Status) {
		return
	}
	now := st.now
	t.Status, t.Error, t.FinishedAt = status, msg, &now
	st.touchTask()
	for _, r := range st.list(engine.Live...) {
		st.dropChildren(r)
		clearJoin(r)
		r.Status = graph.CANCELED
		st.save(r)
	}
	st.taskEnded()
}

// taskEnded 在 Task 到达终态后调用：有父 Task 时生成 NotifyParent。
func (st *state) taskEnded() {
	t := st.task
	if t.ParentTaskID == "" || t.ParentRunID == "" {
		return
	}
	st.notify = &NotifyParent{ParentTaskID: t.ParentTaskID, Event: ChildDone{
		RunID: t.ParentRunID, Slot: t.Slot, ChildTaskID: t.TaskID,
		Status: t.Status, Output: maps.Clone(t.Output), Error: t.Error,
	}}
}

// ended 在一条分支完成（到达 end 或被跳过）时检查：当图中不再有未结束的 Run 时结束 Task。
func (st *state) ended(_ *engine.Run) error {
	if len(st.list(engine.Live...)) == 0 {
		st.finishTask()
	}
	return nil
}

// finishTask 在全部可达分支执行完毕后汇总终态：收集所有真实完成 Finish 的终点 Run 输出（按 (FromEdgeID, RunID) 稳定浅合并），
// 将 Task 置为 SUCCEEDED。
func (st *state) finishTask() {
	t := st.task
	if graph.IsTerminal(t.Status) {
		return
	}
	var ends []*engine.Run
	for _, r := range st.list(graph.SUCCEEDED) {
		if r.Output != nil {
			if ep, err := st.c.reg.NodeEndpoints(st.c.node(r.NodeID)); err == nil && ep.IsTerminal() {
				ends = append(ends, r)
			}
		}
	}
	slices.SortFunc(ends, func(x, y *engine.Run) int {
		if c := strings.Compare(x.FromEdgeID, y.FromEdgeID); c != 0 {
			return c
		}
		return strings.Compare(x.RunID, y.RunID)
	})
	output := map[string]any{}
	for _, r := range ends {
		maps.Copy(output, r.Output)
	}
	now := st.now
	t.FinishedAt = &now
	t.Status, t.Output = graph.SUCCEEDED, output
	st.touchTask()
	st.taskEnded()
}

// fork / 汇合推进见 join.go（fork、branch、arrive、skipDescendants、settle、clearJoin）。
// setOutputs 把节点本次的输出整体写入 Run.Outputs[nodeID]（覆盖上一次）；
// 首次修改入参带入的 Run.Outputs 时先浅拷贝顶层 map，保证不修改 Apply 的入参。
func (st *state) setOutputs(r *engine.Run, nodeID string, out map[string]any) {
	if out == nil {
		out = map[string]any{}
	}
	if r.Outputs == nil {
		r.Outputs = engine.Outputs{nodeID: out}
		st.ownOutputs[r.RunID] = true
		return
	}
	if !st.ownOutputs[r.RunID] {
		r.Outputs = maps.Clone(r.Outputs)
		st.ownOutputs[r.RunID] = true
	}
	r.Outputs[nodeID] = out
}

// injectMessage 返回调试注入失败的错误信息。
func injectMessage(inj *engine.DebugInject) string {
	if msg := strings.TrimSpace(inj.Error); msg != "" {
		return msg
	}
	return "debug inject failed"
}
