package kernel_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	. "github.com/golang-io/graph/test/engine/enginetest"
)

// 本文件覆盖迁移计划阶段 4 列出的内核用例；全部是纯函数测试，不连数据库。

type refs = map[string]graph.InputRef

func op(id string, set map[string]any) graph.Node { return N(id, OpType, OpInput{Set: set}) }
func finish(r refs) graph.Node                    { return In(N("finish", builtin.End, nil), r) }
func tpl(id string, r refs, key, value string) graph.Node {
	return In(N(id, builtin.Template, builtin.TemplateInput{Outputs: []graph.OutKV{{Key: key, Value: value}}}), r)
}

func jsonEq(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("got %s, want %s", g, w)
	}
}

// all 汇合：两个分支的 ActivityDone 依次应用，汇合只触发一次，Merge 节点与下游只进入一次。
func TestJoinAllFiresOnce(t *testing.T) {
	w := newWorld(t, false)
	g := G([]graph.Node{op("fork", nil), op("l", map[string]any{"l": 1}), op("r", map[string]any{"r": 2}),
		N("m", builtin.Merge, builtin.MergeInput{Mode: "all"}),
		tpl("join", refs{"l": R("l", "l"), "r": R("r", "r")}, "sum", "{{l}}+{{r}}"),
		finish(refs{"sum": R("join", "sum")})},
		E("fork", "l"), E("fork", "r"), E("l", "m"), E("r", "m"), E("m", "join"), E("join", "finish"))
	id := w.start(g, nil, engine.TaskPolicy{})
	fork := w.dispatch(id, w.at(id, "fork", graph.PENDING).RunID)
	cmds := w.must(id, done(fork, nil, nil))
	if n := countCmds[kernel.ExecuteActivity](cmds); n != 2 {
		t.Fatalf("fork produced %d ExecuteActivity, want 2", n)
	}
	l := w.dispatch(id, w.at(id, "l", graph.PENDING).RunID)
	r := w.dispatch(id, w.at(id, "r", graph.PENDING).RunID)

	w.must(id, done(l, map[string]any{"l": 1}, nil))
	waiter := w.at(id, "m", graph.WAITING)
	if len(waiter.Arrivals) != 1 || waiter.WaitReason != engine.WaitJoin {
		t.Fatalf("after first arrival: %+v", waiter)
	}
	w.must(id, done(r, map[string]any{"r": 2}, nil))
	if task := w.task(id); task.Status != graph.SUCCEEDED {
		t.Fatalf("task = %s %s%s", task.Status, task.Error, w.dump(id))
	}
	jsonEq(t, w.task(id).Output, map[string]any{"sum": "1+2"})
	// 汇合后继续推进的 Run 依次经过 l、m、join、finish（Seq 为 4），无输出节点（_start、fork、m）均写入空 map {}。
	merged := w.runs[id][waiter.RunID]
	if merged.Seq != 4 || merged.Outputs["_start"] == nil || merged.Outputs["fork"] == nil || merged.Outputs["m"] == nil || merged.Outputs["join"] == nil {
		t.Fatalf("merged run = seq %d outputs %v", merged.Seq, merged.Outputs)
	}
}

// any 汇合：只采纳第一个到达者，其余分支置为 SKIPPED，执行中的分支之后交回的结果不再推进。
func TestJoinAnyTakesFirst(t *testing.T) {
	w := newWorld(t, false)
	g := G([]graph.Node{op("fork", nil), op("fast", map[string]any{"w": "fast"}), op("slow", map[string]any{"w": "slow"}),
		N("m", builtin.Merge, builtin.MergeInput{Mode: "any", Outputs: []builtin.MergeFirstField{
			{Name: "w", First: []graph.InputRef{R("slow", "w"), R("fast", "w")}}}}),
		op("after", map[string]any{"ok": true}),
		finish(refs{"w": R("m", "w")})},
		E("fork", "fast"), E("fork", "slow"), E("fast", "m"), E("slow", "m"), E("m", "after"), E("after", "finish"))
	id := w.start(g, nil, engine.TaskPolicy{})
	w.must(id, done(w.dispatch(id, w.at(id, "fork", graph.PENDING).RunID), nil, nil))
	fast := w.dispatch(id, w.at(id, "fast", graph.PENDING).RunID)
	slow := w.dispatch(id, w.at(id, "slow", graph.PENDING).RunID)

	w.must(id, done(fast, map[string]any{"w": "fast"}, nil))
	if got := w.runs[id][slow.RunID].Status; got != graph.SKIPPED {
		t.Fatalf("slow = %s, want SKIPPED", got)
	}
	after := w.at(id, "after", graph.PENDING)
	// 落选分支的迟到结果：Run 已是 SKIPPED，忽略。
	ch, cmds, err := w.call(id, done(slow, map[string]any{"w": "slow"}, nil))
	if err != nil || !ch.Empty() || cmds != nil {
		t.Fatalf("late loser result = %+v %v %v", ch, cmds, err)
	}
	w.must(id, done(w.dispatch(id, after.RunID), map[string]any{"ok": true}, nil))
	jsonEq(t, w.task(id).Output, map[string]any{"w": "fast"})
}

// Seq 或 Attempt 不一致的 ActivityDone 被忽略：无改动、无命令。
func TestActivityDoneMismatchIgnored(t *testing.T) {
	w := newWorld(t, false)
	id := w.start(G([]graph.Node{op("a", nil), finish(nil)}, E("a", "finish")), nil, engine.TaskPolicy{})
	a := w.dispatch(id, w.at(id, "a", graph.PENDING).RunID)
	for _, ev := range []kernel.ActivityDone{
		{RunID: a.RunID, Seq: a.Seq + 1, Attempt: a.Attempt},
		{RunID: a.RunID, Seq: a.Seq, Attempt: a.Attempt + 1},
		{RunID: "missing", Seq: a.Seq, Attempt: a.Attempt},
	} {
		ch, cmds, err := w.call(id, ev)
		if err != nil || !ch.Empty() || cmds != nil {
			t.Fatalf("mismatched %+v = %+v %v %v", ev, ch, cmds, err)
		}
	}
}

// 同一子 Task 的 ChildDone 送达两次：第二次忽略。父 Run 等待子 Task 时 Task 为 WAITING。
func TestChildDoneDeliveredTwice(t *testing.T) {
	w := newWorld(t, false)
	body := GI([]string{builtin.MapItemField}, []graph.Node{
		In(op("b", map[string]any{"y": 1}), refs{"item": R(graph.InputSource, builtin.MapItemField)}),
		finish(refs{"y": R("b", "y")})}, E("b", "finish"))
	g := GI([]string{"items"}, []graph.Node{
		In(N("lp", builtin.Map, builtin.MapInput{MaxParallel: 1, Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
		finish(refs{"results": R("lp", builtin.MapResultsField)})}, E("lp", "finish"))
	id := w.start(g, map[string]any{"items": []any{"x", "y"}}, engine.TaskPolicy{})
	if st := w.task(id).Status; st != graph.WAITING {
		t.Fatalf("parent waiting on child: task status = %s, want WAITING", st)
	}
	parent := w.at(id, "lp", graph.WAITING)
	child := w.awaited(id, parent.RunID)["0"]
	if child == "" || w.task(child).Slot != "0" || w.task(child).ParentRunID != parent.RunID {
		t.Fatalf("child task = %q %+v", child, w.task(child))
	}
	cmds := w.must(child, done(w.dispatch(child, w.at(child, "b", graph.PENDING).RunID), map[string]any{"y": 1}, nil))
	var notify kernel.NotifyParent
	for _, c := range cmds {
		if n, ok := c.(kernel.NotifyParent); ok {
			notify = n
		}
	}
	if notify.ParentTaskID != id || notify.Event.Slot != "0" || notify.Event.Status != graph.SUCCEEDED {
		t.Fatalf("notify = %+v", notify)
	}
	// world 已投递过一次（map 随即发起第 2 轮）；再次送达同一结果应无改动。
	if w.awaited(id, w.at(id, "lp", graph.WAITING).RunID)["1"] == "" {
		t.Fatalf("second iteration not started:%s", w.dump(id))
	}
	ch, again, err := w.call(id, notify.Event)
	if err != nil || !ch.Empty() || again != nil {
		t.Fatalf("duplicate ChildDone = %+v %v %v", ch, again, err)
	}
}

// ChildDone 的接收与忽略按等待视图（Env.Awaiting）中 Slot 对应的 ChildTaskID 核对；
// 接收后输出 Changes.Released，引擎据此清除等待关系。
func TestChildDoneCheckedAgainstAwaiting(t *testing.T) {
	w := newWorld(t, false)
	body := GI([]string{builtin.MapItemField}, []graph.Node{
		In(op("b", map[string]any{"y": 1}), refs{"item": R(graph.InputSource, builtin.MapItemField)}),
		finish(refs{"y": R("b", "y")})}, E("b", "finish"))
	g := GI([]string{"items"}, []graph.Node{
		In(N("lp", builtin.Map, builtin.MapInput{MaxParallel: 1, Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
		finish(refs{"results": R("lp", builtin.MapResultsField)})}, E("lp", "finish"))
	id := w.start(g, map[string]any{"items": []any{"x"}}, engine.TaskPolicy{})
	parent := w.at(id, "lp", graph.WAITING)
	child := w.awaited(id, parent.RunID)["0"]
	// 伪造结果：等待视图中 Slot "0" 指向真子 Task，"bogus-task" 被忽略，无改动。
	if ch, cmds, err := w.call(id, kernel.ChildDone{RunID: parent.RunID, Slot: "0",
		ChildTaskID: "bogus-task", Status: graph.SUCCEEDED}); err != nil || !ch.Empty() || cmds != nil {
		t.Fatalf("bogus ChildDone = %+v %v %v", ch, cmds, err)
	}
	// 真结果经正常流程被接收：等待清除，Task 成功。
	w.must(child, done(w.dispatch(child, w.at(child, "b", graph.PENDING).RunID), map[string]any{"y": 1}, nil))
	if len(w.awaiting[id]) != 0 {
		t.Fatalf("awaiting = %v", w.awaiting[id])
	}
	if task := w.task(id); task.Status != graph.SUCCEEDED {
		t.Fatalf("task = %s %q%s", task.Status, task.Error, w.dump(id))
	}
}

// Cancel 之后到达的 ActivityDone 被忽略；等待中的子 Task 级联取消。
func TestCancelThenLateResult(t *testing.T) {
	w := newWorld(t, false)
	body := GI([]string{builtin.MapItemField}, []graph.Node{op("b", nil), finish(nil)}, E("b", "finish"))
	g := GI([]string{"items"}, []graph.Node{op("a", nil),
		In(N("lp", builtin.Map, builtin.MapInput{MaxParallel: 2, Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
		finish(nil)}, E("a", "lp"), E("lp", "finish"))

	// 活动节点执行中取消：Task CANCELED，迟到的结果无改动。
	id := w.start(g, map[string]any{"items": []any{1, 2}}, engine.TaskPolicy{})
	a := w.dispatch(id, w.at(id, "a", graph.PENDING).RunID)
	w.must(id, kernel.Cancel{Reason: "stop"})
	if task := w.task(id); task.Status != graph.CANCELED || task.Error != "stop" || w.runs[id][a.RunID].Status != graph.CANCELED {
		t.Fatalf("after cancel: %+v%s", task, w.dump(id))
	}
	ch, cmds, err := w.call(id, done(a, nil, nil))
	if err != nil || !ch.Empty() || cmds != nil {
		t.Fatalf("late result after cancel = %+v %v %v", ch, cmds, err)
	}

	// 等待子 Task 时取消：输出 CancelChild，两个子 Task 都进入 CANCELED。
	id = w.start(g, map[string]any{"items": []any{1, 2}}, engine.TaskPolicy{})
	w.must(id, done(w.dispatch(id, w.at(id, "a", graph.PENDING).RunID), nil, nil))
	children := w.awaited(id, w.at(id, "lp", graph.WAITING).RunID)
	cmds = w.must(id, kernel.Cancel{})
	if n := countCmds[kernel.CancelChild](cmds); n != 2 {
		t.Fatalf("CancelChild count = %d, want 2", n)
	}
	for _, c := range children {
		if st := w.task(c).Status; st != graph.CANCELED {
			t.Fatalf("child %s = %s, want CANCELED", c, st)
		}
	}
}

// 节点失败且 MaxAttempts = 3：前两次回到 PENDING 并设置 AvailableAt；第三次使 Task 失败。
func TestRetryUntilMaxAttempts(t *testing.T) {
	w := newWorld(t, false)
	id := w.start(G([]graph.Node{op("a", nil), finish(nil)}, E("a", "finish")), nil,
		engine.TaskPolicy{MaxAttempts: 3, RetryDelayMS: 500})
	for attempt := 1; attempt <= 3; attempt++ {
		a := w.dispatch(id, w.at(id, "a", graph.PENDING).RunID)
		if a.Attempt != attempt {
			t.Fatalf("attempt = %d, want %d", a.Attempt, attempt)
		}
		cmds := w.must(id, done(a, nil, errors.New("boom")))
		if attempt == 3 {
			break
		}
		r := w.at(id, "a", graph.PENDING)
		want := t0.Add(500 * time.Millisecond)
		if r.AvailableAt != want.UnixMilli() || len(cmds) != 1 || cmds[0].(kernel.ExecuteActivity).NotBefore != want {
			t.Fatalf("retry %d: AvailableAt=%d cmds=%+v", attempt, r.AvailableAt, cmds)
		}
	}
	if task := w.task(id); task.Status != graph.FAILED || !strings.Contains(task.Error, "boom") {
		t.Fatalf("task = %s %q", task.Status, task.Error)
	}
}

// 活动节点输入来源缺失、调试注入要求失败：直接按节点失败处理，不输出 ExecuteActivity，也不重试。
func TestActivityPrecheckFailure(t *testing.T) {
	w := newWorld(t, false)
	g := GI([]string{"score"}, []graph.Node{
		In(N("gate", builtin.Switch, builtin.SwitchInput{Expr: "score >= 60"}), refs{"score": R(graph.InputSource, "score")}),
		op("hi", map[string]any{"tag": "hi"}), op("lo", nil),
		N("m", builtin.Merge, builtin.MergeInput{Mode: "any"}),
		In(op("use", nil), refs{"tag": R("hi", "tag")}), finish(nil)},
		E("gate", "hi", "Case0"), E("gate", "lo", "Case1"), E("hi", "m"), E("lo", "m"), E("m", "use"), E("use", "finish"))
	id := w.start(g, map[string]any{"score": 10}, engine.TaskPolicy{MaxAttempts: 3})
	cmds := w.must(id, done(w.dispatch(id, w.at(id, "lo", graph.PENDING).RunID), nil, nil))
	if n := countCmds[kernel.ExecuteActivity](cmds); n != 0 {
		t.Fatalf("missing input still dispatched %d activities", n)
	}
	if task := w.task(id); task.Status != graph.FAILED || !strings.Contains(task.Error, `input "tag" unavailable`) {
		t.Fatalf("task = %s %q", task.Status, task.Error)
	}

	inject := engine.TaskPolicy{MaxAttempts: 3, Debug: &engine.DebugOptions{
		Injects: map[string]engine.DebugInject{"a": {Failed: true, Error: "injected"}}}}
	id = w.start(G([]graph.Node{op("a", nil), finish(nil)}, E("a", "finish")), nil, inject)
	if task := w.task(id); task.Status != graph.FAILED || !strings.Contains(task.Error, "injected") {
		t.Fatalf("inject failed: task = %s %q", task.Status, task.Error)
	}
	for _, q := range w.log {
		if _, ok := q.cmd.(kernel.ExecuteActivity); ok && q.taskID == id {
			t.Fatal("inject failure still dispatched an activity")
		}
	}
}

// 调试注入对内联节点同样生效（设计决定第 22 条）：end 的输入被替换并新增；
// 已有字面量参数的内联节点（如 stop_error 的 Message）同样可被调试注入覆盖。
func TestInjectInlineNode(t *testing.T) {
	w := newWorld(t, true)
	policy := engine.TaskPolicy{Debug: &engine.DebugOptions{
		Injects: map[string]engine.DebugInject{"finish": {Vars: map[string]any{"b": "inj", "x": "new"}}}}}
	id := w.start(G([]graph.Node{op("a", map[string]any{"b": 1}), finish(refs{"b": R("a", "b")})}, E("a", "finish")), nil, policy)
	jsonEq(t, w.task(id).Output, map[string]any{"b": "inj", "x": "new"})

	stopPolicy := engine.TaskPolicy{Debug: &engine.DebugOptions{
		Injects: map[string]engine.DebugInject{"boom": {Vars: map[string]any{"Message": "injected boom"}}}}}
	stopID := w.start(G([]graph.Node{
		N("boom", builtin.StopError, builtin.StopErrorInput{Message: "literal boom"}),
	}), nil, stopPolicy)
	if task := w.task(stopID); task.Status != graph.FAILED || !strings.Contains(task.Error, "injected boom") {
		t.Fatalf("stop_error inject = %s %q, want injected boom", task.Status, task.Error)
	}

	// 调试注入可 Mock 尚未执行的分支节点的输出，使依赖该输出的下游节点正常推进，且整数自动规整为 int64。
	swGraph := GI([]string{"score"}, []graph.Node{
		In(N("gate", builtin.Switch, builtin.SwitchInput{Expr: "score >= 60"}), refs{"score": R(graph.InputSource, "score")}),
		op("hi", map[string]any{"tag": "hi"}), op("lo", nil),
		N("m", builtin.Merge, builtin.MergeInput{Mode: "any"}),
		finish(refs{"tag": R("hi", "tag")}),
	}, E("gate", "hi", "Case0"), E("gate", "lo", "Case1"), E("hi", "m"), E("lo", "m"), E("m", "finish"))
	mockPolicy := engine.TaskPolicy{Debug: &engine.DebugOptions{
		Injects: map[string]engine.DebugInject{"finish": {Vars: map[string]any{"tag": 42}}},
	}}
	mockID := w.start(swGraph, map[string]any{"score": 10}, mockPolicy)
	if got := w.task(mockID); got.Status != graph.SUCCEEDED || got.Output["tag"] != int64(42) {
		t.Fatalf("mock missing upstream = %s %#v", got.Status, got.Output)
	}

	// 调试注入参数类型不合法或违反节点参数 Validate 时，NewTask / Resume 均前置拒绝（返回 ErrInvalid）。
	badMapBody := GI([]string{builtin.MapItemField}, []graph.Node{finish(nil)})
	mapG := GI([]string{"items"}, []graph.Node{
		In(N("lp", builtin.Map, builtin.MapInput{Body: &badMapBody}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
		finish(nil),
	}, E("lp", "finish"))
	for _, badVars := range []map[string]any{
		{"Chunk": "not-an-int"},
		{"Chunk": -5},
	} {
		_, err := kernel.NewTask(engine.KindImmediate, engine.StartRequest{
			Graph: mapG,
			Input: map[string]any{"items": []any{1}},
			Policy: engine.TaskPolicy{Debug: &engine.DebugOptions{
				Injects: map[string]engine.DebugInject{"lp": {Vars: badVars}},
			}},
		}, w.reg, w.env)
		if !errors.Is(err, engine.ErrInvalid) {
			t.Fatalf("NewTask with bad inject %v: err = %v, want ErrInvalid", badVars, err)
		}
	}
}

// 子图嵌套超过 graph.MaxSubgraphDepth：节点失败。
func TestSubgraphDepthLimit(t *testing.T) {
	w := newWorld(t, true)
	w.graphs["child"] = G([]graph.Node{finish(nil)})
	g := G([]graph.Node{N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "child"}), finish(nil)}, E("sub", "finish"))
	task, err := kernel.NewTask(engine.KindImmediate, engine.StartRequest{Graph: g}, w.reg, w.env)
	if err != nil {
		t.Fatal(err)
	}
	task.Depth = graph.MaxSubgraphDepth
	w.add(task)
	w.must(task.TaskID, kernel.Started{})
	if got := w.task(task.TaskID); got.Status != graph.FAILED || !strings.Contains(got.Error, "max nesting depth") {
		t.Fatalf("task = %s %q", got.Status, got.Error)
	}
}

// Pause 后 Run 到达内联节点或活动节点前进入 debug 等待；Resume 放行后内联节点同步执行并推进到下一活动节点。
func TestPauseStopsBeforeActivityOnly(t *testing.T) {
	w := newWorld(t, false)
	g := G([]graph.Node{op("a", map[string]any{"x": 1}), tpl("t", refs{"x": R("a", "x")}, "y", "v{{x}}"),
		op("b", nil), finish(refs{"y": R("t", "y")})}, E("a", "t"), E("t", "b"), E("b", "finish"))
	id := w.start(g, nil, engine.TaskPolicy{})
	a := w.dispatch(id, w.at(id, "a", graph.PENDING).RunID)
	w.must(id, kernel.Pause{})
	if !w.task(id).Paused || w.task(id).Status != graph.RUNNING {
		t.Fatalf("paused task = %+v", w.task(id))
	}
	cmds := w.must(id, done(a, map[string]any{"x": 1}, nil))
	tr := w.at(id, "t", graph.WAITING)
	if tr.WaitReason != engine.WaitDebug || len(cmds) != 0 {
		t.Fatalf("after pause: run=%+v cmds=%+v", tr, cmds)
	}
	if w.task(id).Status != graph.WAITING {
		t.Fatalf("task = %s, want WAITING", w.task(id).Status)
	}
	cmds = w.must(id, kernel.Resume{})
	b := w.at(id, "b", graph.PENDING)
	if w.task(id).Paused || countCmds[kernel.ExecuteActivity](cmds) != 1 || b == nil || b.Outputs["t"]["y"] != "v1" {
		t.Fatalf("resume: task=%+v cmds=%+v b=%+v", w.task(id), cmds, b)
	}
}

// 单步在除起止节点（_start / end）外的每个节点前停住：a、t（template）、b 依次停住并放行。
func TestStepStopsBeforeActivityOnly(t *testing.T) {
	w := newWorld(t, true)
	g := G([]graph.Node{op("a", map[string]any{"x": 1}), tpl("t", refs{"x": R("a", "x")}, "y", "{{x}}"),
		op("b", nil), finish(refs{"y": R("t", "y")})}, E("a", "t"), E("t", "b"), E("b", "finish"))
	id := w.start(g, nil, engine.TaskPolicy{Debug: &engine.DebugOptions{Step: true}})
	var visited []string
	for !graph.IsTerminal(w.task(id).Status) {
		var waiting *engine.Run
		for _, r := range w.list(id) {
			if r.Status == graph.WAITING && r.WaitReason == engine.WaitDebug {
				waiting = r
			}
		}
		if waiting == nil {
			t.Fatalf("step %d: unexpected state:%s", len(visited), w.dump(id))
		}
		visited = append(visited, waiting.NodeID)
		w.must(id, kernel.Resume{Request: engine.ResumeRequest{DebugAction: engine.DebugActionStep}})
	}
	if len(visited) != 3 || visited[0] != "a" || visited[1] != "t" || visited[2] != "b" || w.task(id).Status != graph.SUCCEEDED {
		t.Fatalf("visited = %v, task = %s", visited, w.task(id).Status)
	}
}

// 控制事件前提不满足时返回拒绝：包装 ErrConflict / ErrInvalid、没有改动；Task 结束后的事件返回空改动且无错误。
func TestRejections(t *testing.T) {
	w := newWorld(t, false)
	g := G([]graph.Node{op("a", nil), finish(nil)}, E("a", "finish"))
	id := w.start(g, nil, engine.TaskPolicy{})
	w.must(id, kernel.Pause{})
	cases := []struct {
		ev   kernel.Event
		want error
	}{
		{kernel.Pause{}, engine.ErrConflict},
		{kernel.Resume{Request: engine.ResumeRequest{DebugAction: "later"}}, engine.ErrInvalid},
	}
	for _, tc := range cases {
		ch, cmds, err := w.call(id, tc.ev)
		if !errors.Is(err, tc.want) || !kernel.IsRejection(err) || !ch.Empty() || cmds != nil {
			t.Fatalf("%T%+v = %+v %v %v, want rejection %v", tc.ev, tc.ev, ch, cmds, err, tc.want)
		}
	}
	w.must(id, kernel.Resume{})
	// 没有暂停、也没有调试等待的 Run：ErrConflict。
	if _, _, err := w.call(id, kernel.Resume{}); !errors.Is(err, engine.ErrConflict) || !kernel.IsRejection(err) {
		t.Fatalf("resume without paused runs err = %v", err)
	}
	w.must(id, kernel.Cancel{})
	for _, ev := range []kernel.Event{kernel.Pause{}, kernel.Resume{}, kernel.Cancel{}} {
		ch, cmds, err := w.call(id, ev)
		if err != nil || !ch.Empty() || cmds != nil {
			t.Fatalf("%T on finished task = %+v %v %v", ev, ch, cmds, err)
		}
	}
}

// 同一输入调用两次 Apply：结果相同；入参未被修改。每次有改动时 Version 加 1。
func TestApplyDeterministicAndPure(t *testing.T) {
	w := newWorld(t, false)
	g := G([]graph.Node{op("fork", nil), op("l", nil), op("r", nil),
		N("m", builtin.Merge, builtin.MergeInput{Mode: "all"}), finish(nil)},
		E("fork", "l"), E("fork", "r"), E("l", "m"), E("r", "m"), E("m", "finish"))
	id := w.start(g, nil, engine.TaskPolicy{})
	fork := w.dispatch(id, w.at(id, "fork", graph.PENDING).RunID)
	task, runs := w.task(id), w.list(id)
	before, _ := json.Marshal([]any{task, runs})
	envAt := func() kernel.Env {
		n := 0
		env := w.env
		env.NewID = func() string { n++; return fmt.Sprintf("x%04d", n) }
		return env
	}
	ev := done(fork, nil, nil)
	ch1, cmds1, err1 := kernel.Apply(w.reg, task, runs, ev, envAt())
	ch2, cmds2, err2 := kernel.Apply(w.reg, task, runs, ev, envAt())
	if err1 != nil || err2 != nil {
		t.Fatal(err1, err2)
	}
	jsonEq(t, []any{ch1, cmds1}, []any{ch2, cmds2})
	after, _ := json.Marshal([]any{task, runs})
	if string(before) != string(after) {
		t.Fatalf("Apply modified its input:\nbefore %s\nafter  %s", before, after)
	}
	if ch1.Task.Version != task.Version+1 || len(ch1.Runs) != 3 {
		t.Fatalf("Version %d → %d, runs %d", task.Version, ch1.Task.Version, len(ch1.Runs))
	}
}

// badCtrl 是返回非法形态（Effect 与 Set 同时出现）的 Controller，用于内核缺陷用例。
type badCtrl struct{}

func (badCtrl) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{ID: "bad_ctrl", Name: "bad", Endpoints: node.DefaultEndpoints()}, struct{}{})
}

func (badCtrl) Control(context.Context, node.ControlInput) (node.NodeOutput, error) {
	return node.NodeOutput{Set: map[string]any{"x": 1}, Effect: &node.Effect{Kind: node.EffectFinish}}, nil
}

// Controller 返回非法形态：Apply 返回非拒绝类 error；引擎改为保存 Fail 的结果，Task FAILED。
func TestControllerInvalidOutputIsDefect(t *testing.T) {
	w := newWorld(t, false, badCtrl{})
	g := G([]graph.Node{N("bad", "bad_ctrl", nil), finish(nil)}, E("bad", "finish"))
	task, err := kernel.NewTask(engine.KindImmediate, engine.StartRequest{Graph: g}, w.reg, w.env)
	if err != nil {
		t.Fatal(err)
	}
	w.add(task)
	if _, err := w.apply(task.TaskID, kernel.Started{}); err == nil || kernel.IsRejection(err) {
		t.Fatalf("err = %v, want kernel defect", err)
	}
	if got := w.task(task.TaskID); got.Status != graph.FAILED || !strings.Contains(got.Error, "kernel error") {
		t.Fatalf("task = %s %q", got.Status, got.Error)
	}
}

// spawnExec 是试图返回 EffectSpawn 的纯 Executor（未实现 Controller），可按 inline 配置为内联或活动节点。
type spawnExec struct {
	id     string
	inline bool
	body   graph.Graph
}

func (e spawnExec) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{
		ID: e.id, Name: e.id, Inline: e.inline, Endpoints: node.DefaultEndpoints(),
	}, struct{}{})
}

func (e spawnExec) Execute(context.Context, node.NodeInput) (node.NodeOutput, error) {
	body := e.body
	return node.SpawnChildren([]node.Spawn{{Slot: "0", Graph: &body}}, nil), nil
}

// 纯 Executor（活动节点与内联节点）返回 EffectSpawn 时被拒绝，不会派生任何子 Task。
func TestExecutorSpawnRejected(t *testing.T) {
	body := G([]graph.Node{op("b", nil), finish(nil)}, E("b", "finish"))

	// 1. 活动 Executor 返回 EffectSpawn：按活动节点非法输出处理，不派生子任务，最终使 Task FAILED。
	wAct := newWorld(t, false, spawnExec{id: "act_spawn_exec", inline: false, body: body})
	gAct := G([]graph.Node{N("sp", "act_spawn_exec", nil), finish(nil)}, E("sp", "finish"))
	actID := wAct.start(gAct, nil, engine.TaskPolicy{})
	cmd := wAct.dispatch(actID, wAct.at(actID, "sp", graph.PENDING).RunID)
	actOut := node.SpawnChildren([]node.Spawn{{Slot: "0", Graph: &body}}, nil)
	wAct.must(actID, kernel.ActivityDone{RunID: cmd.RunID, Seq: cmd.Seq, Attempt: cmd.Attempt, Output: actOut})
	if got := wAct.task(actID); got.Status != graph.FAILED || !strings.Contains(got.Error, "spawn effect is only allowed for Controller") {
		t.Fatalf("activity executor spawn task = %s %q", got.Status, got.Error)
	}
	if n := countLogged[kernel.StartChild](wAct); n != 0 {
		t.Fatalf("activity executor StartChild count = %d, want 0", n)
	}

	// 2. 内联 Executor 返回 EffectSpawn：Apply 返回内核缺陷 error，Task FAILED，不派生子任务。
	wInline := newWorld(t, false, spawnExec{id: "inline_spawn_exec", inline: true, body: body})
	gInline := G([]graph.Node{N("sp", "inline_spawn_exec", nil), finish(nil)}, E("sp", "finish"))
	inlineTask, err := kernel.NewTask(engine.KindImmediate, engine.StartRequest{Graph: gInline}, wInline.reg, wInline.env)
	if err != nil {
		t.Fatal(err)
	}
	wInline.add(inlineTask)
	if _, err := wInline.apply(inlineTask.TaskID, kernel.Started{}); err == nil || kernel.IsRejection(err) || !strings.Contains(err.Error(), "spawn effect is only allowed for Controller") {
		t.Fatalf("inline executor spawn err = %v, want kernel defect", err)
	}
	if got := wInline.task(inlineTask.TaskID); got.Status != graph.FAILED || !strings.Contains(got.Error, "kernel error") {
		t.Fatalf("inline executor spawn task = %s %q", got.Status, got.Error)
	}
	if n := countLogged[kernel.StartChild](wInline); n != 0 {
		t.Fatalf("inline executor StartChild count = %d, want 0", n)
	}

	// 3. 异常状态下非 Controller 节点处于 WAITING(subgraph) 并收到 ChildDone：被防御性拦截为内核缺陷。
	corruptRun := &engine.Run{
		RunID: "r1", TaskID: actID, EngineKind: engine.KindImmediate,
		NodeID: "sp", Seq: 1, Attempt: 1, Status: graph.WAITING, WaitReason: engine.WaitSubgraph,
		Outputs: engine.Outputs{},
	}
	corruptTask := &engine.Task{
		TaskID: actID, EngineKind: engine.KindImmediate, Status: graph.WAITING,
		Graph: gAct, Policy: engine.TaskPolicy{MaxAttempts: 1},
	}
	_, _, err = kernel.Apply(wAct.reg, corruptTask, []*engine.Run{corruptRun},
		kernel.ChildDone{RunID: "r1", Slot: "0", ChildTaskID: "c1", Status: graph.SUCCEEDED},
		kernel.Env{Now: t0, Awaiting: map[string]map[string]string{"r1": {"0": "c1"}}})
	if err == nil || kernel.IsRejection(err) || !strings.Contains(err.Error(), "is not a controller") {
		t.Fatalf("non-controller ChildDone err = %v, want kernel defect", err)
	}
}

// spawnCtrl 启动时发起两个子 Task（循环体为一个活动节点），第一个 ChildDone 到达就返回输出，
// 违反「子 Task 未全部结束时父 Run 不离开节点」的契约。
type spawnCtrl struct{ body graph.Graph }

func (spawnCtrl) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{ID: "spawn_ctrl", Name: "spawn", Endpoints: node.DefaultEndpoints()}, struct{}{})
}

func (c spawnCtrl) Control(_ context.Context, in node.ControlInput) (node.NodeOutput, error) {
	if in.Event.Kind == node.EventChildDone {
		return node.NodeOutput{Set: map[string]any{"x": 1}}, nil
	}
	body := c.body
	return node.NodeOutput{Effect: &node.Effect{Kind: node.EffectSpawn, Spawns: []node.Spawn{
		{Slot: "0", Graph: &body}, {Slot: "1", Graph: &body},
	}}}, nil
}

// Controller 在仍有子 Task 运行时返回输出：Apply 返回内核缺陷类 error（设计文档 5.4、5.5）。
func TestControllerLeavesWithRunningChildrenIsDefect(t *testing.T) {
	body := G([]graph.Node{op("b", nil), finish(nil)}, E("b", "finish"))
	w := newWorld(t, false, spawnCtrl{body: body})
	id := w.start(G([]graph.Node{N("sp", "spawn_ctrl", nil), finish(nil)}, E("sp", "finish")), nil, engine.TaskPolicy{})
	p := w.at(id, "sp", graph.WAITING)
	children := w.awaited(id, p.RunID)
	if len(children) != 2 {
		t.Fatalf("children = %v", children)
	}
	ev := kernel.ChildDone{RunID: p.RunID, Slot: "0", ChildTaskID: children["0"], Status: graph.SUCCEEDED}
	_, _, err := w.call(id, ev)
	if err == nil || kernel.IsRejection(err) || !strings.Contains(err.Error(), "child tasks are still running") {
		t.Fatalf("err = %v, want kernel defect", err)
	}
}

// map 某一轮失败：不再启动新轮次、不取消已启动的轮次，父 Run 继续等待；已启动的轮次全部结束后节点失败。
func TestMapFailureWaitsForStartedRounds(t *testing.T) {
	w := newWorld(t, false)
	body := GI([]string{builtin.MapItemField}, []graph.Node{op("b", nil), finish(nil)}, E("b", "finish"))
	g := GI([]string{"items"}, []graph.Node{
		In(N("lp", builtin.Map, builtin.MapInput{MaxParallel: 2, Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
		finish(nil)}, E("lp", "finish"))
	id := w.start(g, map[string]any{"items": []any{1, 2, 3}}, engine.TaskPolicy{})
	p := w.at(id, "lp", graph.WAITING)
	children := w.awaited(id, p.RunID)
	if len(children) != 2 {
		t.Fatalf("children = %v", children)
	}
	first, second := children["0"], children["1"]

	w.must(first, done(w.dispatch(first, w.at(first, "b", graph.PENDING).RunID), nil, errors.New("bad item")))
	p = w.at(id, "lp", graph.WAITING)
	if w.task(id).Status != graph.WAITING || !maps.Equal(w.awaited(id, p.RunID), map[string]string{"1": second}) {
		t.Fatalf("after first failure: %+v%s", w.awaited(id, p.RunID), w.dump(id))
	}
	if n := countLogged[kernel.CancelChild](w); n != 0 {
		t.Fatalf("CancelChild count = %d, want 0", n)
	}

	w.must(second, done(w.dispatch(second, w.at(second, "b", graph.PENDING).RunID), nil, nil))
	if task := w.task(id); task.Status != graph.FAILED || !strings.Contains(task.Error, "iteration 0: ") {
		t.Fatalf("task = %s %q", task.Status, task.Error)
	}
	if n := countLogged[kernel.StartChild](w); n != 2 {
		t.Fatalf("StartChild count = %d, want 2 (third round must not start)", n)
	}
}

// countLogged 统计 world 处理过的全部命令中某种类型的个数。
func countLogged[T kernel.Command](w *world) int {
	n := 0
	for _, q := range w.log {
		if _, ok := q.cmd.(T); ok {
			n++
		}
	}
	return n
}

// kernel.Fail：Task FAILED、其余 Run CANCELED，输出 CancelChild 与 NotifyParent。
func TestFail(t *testing.T) {
	task := &engine.Task{TaskID: "t", Status: graph.RUNNING, Version: 3,
		ParentTaskID: "p", ParentRunID: "pr", Slot: "2"}
	runs := []*engine.Run{
		{RunID: "r1", TaskID: "t", Status: graph.WAITING, WaitReason: engine.WaitSubgraph},
		{RunID: "r2", TaskID: "t", Status: graph.PENDING},
		{RunID: "r3", TaskID: "t", Status: graph.SUCCEEDED},
	}
	ch, cmds := kernel.Fail(task, runs, errors.New("bad shape"),
		kernel.Env{Now: t0, Awaiting: map[string]map[string]string{"r1": {"0": "c1"}}})
	if ch.Task.Status != graph.FAILED || ch.Task.Error != "kernel error: bad shape" || ch.Task.Version != 4 {
		t.Fatalf("task = %+v", ch.Task)
	}
	if len(ch.Runs) != 2 || ch.Runs[0].Status != graph.CANCELED || ch.Runs[1].Status != graph.CANCELED || !slices.Equal(ch.Released, []string{"c1"}) {
		t.Fatalf("runs = %+v, released = %v", ch.Runs, ch.Released)
	}
	jsonEq(t, cmds, []kernel.Command{
		kernel.CancelChild{ChildTaskID: "c1"},
		kernel.NotifyParent{ParentTaskID: "p", Event: kernel.ChildDone{RunID: "pr", Slot: "2", ChildTaskID: "t",
			Status: graph.FAILED, Error: "kernel error: bad shape"}},
	})
	if runs[0].Status != graph.WAITING || task.Status != graph.RUNNING {
		t.Fatal("Fail modified its input")
	}
	if ch, cmds := kernel.Fail(ch.Task, ch.Runs, errors.New("again"), kernel.Env{Now: t0}); !ch.Empty() || cmds != nil {
		t.Fatalf("Fail on finished task = %+v %v", ch, cmds)
	}
}

// 按 GraphID 引用的子图按需加载：Env.Graphs 中缺少时 Apply 在 Changes.NeedGraphs 中返回缺少的 GraphID，
// 没有任何状态改动与命令，error 为 nil；引擎在 Apply 之外加载后重新应用同一事件，才发起子 Task
// （子 Task 固定加载到的版本）。已加载的子图之后不再报告。加载失败时子图节点失败，错误中带有请求的 GraphID。
func TestSubgraphLoadedOnDemand(t *testing.T) {
	w := newWorld(t, true)
	w.graphs["child"] = G([]graph.Node{finish(nil)})
	g := G([]graph.Node{N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "child"}), finish(nil)}, E("sub", "finish"))

	task, err := kernel.NewTask(engine.KindImmediate, engine.StartRequest{Graph: g}, w.reg, w.env)
	if err != nil {
		t.Fatal(err)
	}
	ch, cmds, err := kernel.Apply(w.reg, task, nil, kernel.Started{}, w.env)
	if err != nil || !slices.Equal(ch.NeedGraphs, []string{"child"}) || !ch.Empty() || len(cmds) != 0 {
		t.Fatalf("first apply = %+v %+v %v", ch, cmds, err)
	}

	id := w.start(g, nil, engine.TaskPolicy{})
	if len(w.needs) != 1 || !slices.Equal(w.needs[0], []string{"child"}) {
		t.Fatalf("needs = %v", w.needs)
	}
	if n := countLogged[kernel.StartChild](w); n != 1 {
		t.Fatalf("StartChild count = %d, want 1", n)
	}
	if task := w.task(id); task.Status != graph.SUCCEEDED {
		t.Fatalf("task = %s %q%s", task.Status, task.Error, w.dump(id))
	}
	for _, task := range w.tasks {
		if task.ParentTaskID == id && (task.GraphID != "child" || task.GraphVersion != 7) {
			t.Fatalf("child pinned to %s@%d", task.GraphID, task.GraphVersion)
		}
	}
	// 已加载的子图直接使用，不再报告 NeedGraphs；若用已加载的 ID 再次调用 engine.LoadGraphs 则返回死循环保护错误。
	if id := w.start(g, nil, engine.TaskPolicy{}); w.task(id).Status != graph.SUCCEEDED || len(w.needs) != 1 {
		t.Fatalf("second start: %s needs=%v", w.task(id).Status, w.needs)
	}
	if err := engine.LoadGraphs(context.Background(), w.resolve, []string{"child"}, w.loaded); err == nil {
		t.Fatal("LoadGraphs for already-loaded graph returned nil error")
	}

	missing := newWorld(t, true)
	g = G([]graph.Node{N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "nope"}), finish(nil)}, E("sub", "finish"))
	id = missing.start(g, nil, engine.TaskPolicy{})
	if task := missing.task(id); task.Status != graph.FAILED || !strings.Contains(task.Error, `spawn sub: load "nope"`) {
		t.Fatalf("task = %s %q", task.Status, task.Error)
	}
}

// 解析器返回的图 GraphID 与请求的不一致：子图节点失败，不发起子 Task。
func TestResolverIdentityMismatchFails(t *testing.T) {
	w := newWorld(t, true)
	w.resolve = func(context.Context, string) (graph.Graph, error) {
		g := GI(nil, []graph.Node{finish(nil)})
		g.GraphID, g.GraphVersion = "other", 1
		return g, nil
	}
	g := G([]graph.Node{
		N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "child"}),
		finish(nil)}, E("sub", "finish"))
	id := w.start(g, nil, engine.TaskPolicy{})
	task := w.task(id)
	if task.Status != graph.FAILED || !strings.Contains(task.Error, `resolver returned graph "other" for "child"`) {
		t.Fatalf("task = %s %q%s", task.Status, task.Error, w.dump(id))
	}
	if n := countLogged[kernel.StartChild](w); n != 0 {
		t.Fatalf("StartChild count = %d, want 0", n)
	}
}

// 端到端：map 循环体（含活动节点）+ reduce + 按 GraphID 引用的子图，经 world 的命令处理全部跑通。
// 验证 StartChild / NotifyParent 的往返、子 Task 固定的子图版本与 Slot。
func TestEndToEnd(t *testing.T) {
	w := newWorld(t, true)
	body := GI([]string{builtin.MapItemField, builtin.MapIndexField}, []graph.Node{
		In(N("body", builtin.Code, builtin.CodeInput{Outputs: []graph.OutKV{{Key: "v", Value: "Index * 10 + Item"}}}),
			refs{"Item": R(graph.InputSource, builtin.MapItemField), "Index": R(graph.InputSource, builtin.MapIndexField)}),
		finish(refs{"v": R("body", "v")})}, E("body", "finish"))
	// 循环体自带的身份不可信：子 Task 必须改用父 Task 的 (g, 0)。
	body.GraphID, body.GraphVersion = "bogus", 9
	w.graphs["child"] = GI([]string{"name"}, []graph.Node{
		tpl("c", refs{"name": R(graph.InputSource, "name")}, "echo", "child:{{name}}"), finish(refs{"echo": R("c", "echo")})},
		E("c", "finish"))
	g := GI([]string{"items", "name"}, []graph.Node{
		In(N("lp", builtin.Map, builtin.MapInput{MaxParallel: 2, Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
		In(N("sum", builtin.Reduce, builtin.ReduceInput{Init: `0`, Expr: `Acc + Item.v`}), refs{builtin.ReduceListField: R("lp", builtin.MapResultsField)}),
		In(N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "child"}), refs{"name": R(graph.InputSource, "name")}),
		finish(refs{"total": R("sum", builtin.ReduceResultField), "sub": R("sub", builtin.SubGraphOutputField)})},
		E("lp", "sum"), E("sum", "sub"), E("sub", "finish"))
	id := w.start(g, map[string]any{"items": []any{1, 2, 3}, "name": "Ada"}, engine.TaskPolicy{})
	if task := w.task(id); task.Status != graph.SUCCEEDED {
		t.Fatalf("task = %s %q%s", task.Status, task.Error, w.dump(id))
	}
	jsonEq(t, w.task(id).Output, map[string]any{"sub": map[string]any{"echo": "child:Ada"}, "total": 36})
	if root := w.task(id); root.Graph.GraphID != "g" || root.Graph.GraphVersion != 0 || root.GraphID != "g" {
		t.Fatalf("root identity: column %s@%d graph %s@%d", root.GraphID, root.GraphVersion, root.Graph.GraphID, root.Graph.GraphVersion)
	}
	var sub *engine.Task
	rounds := 0
	for _, task := range w.tasks {
		if task.ParentTaskID != id {
			continue
		}
		// 每个子 Task 的列与图快照身份一致。
		if task.GraphID != task.Graph.GraphID || task.GraphVersion != task.Graph.GraphVersion {
			t.Fatalf("child %s: column %s@%d graph %s@%d", task.TaskID, task.GraphID, task.GraphVersion, task.Graph.GraphID, task.Graph.GraphVersion)
		}
		switch task.GraphID {
		case "child":
			sub = task
		case "g":
			rounds++
		default:
			t.Fatalf("child %s has unexpected graph %s@%d", task.TaskID, task.GraphID, task.GraphVersion)
		}
	}
	if rounds != 3 {
		t.Fatalf("map rounds with parent identity = %d, want 3", rounds)
	}
	if sub == nil || sub.GraphVersion != 7 || sub.Status != graph.SUCCEEDED || sub.Depth != 1 {
		t.Fatalf("subgraph task = %+v", sub)
	}
	if len(w.awaiting[id]) > 0 {
		t.Fatalf("task %s still awaits children: %+v", id, w.awaiting[id])
	}
	for _, r := range w.list(id) {
		if r.State != nil {
			t.Fatalf("run %s still holds visit state: %+v", r.RunID, r)
		}
	}
}
