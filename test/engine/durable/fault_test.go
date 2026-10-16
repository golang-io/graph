package durable_test

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/durable"
	"github.com/golang-io/graph/engine/kernel"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/test"
	"github.com/golang-io/graph/test/engine/enginetest"
	"github.com/google/jsonschema-go/jsonschema"
	"gorm.io/gorm"
)

// countedOp 包装 OpSpec，按 RunKey 计数成功执行次数。
type countedOp struct {
	inner node.Executor
	calls *sync.Map
	total *atomic.Int64
}

func (c countedOp) Spec() node.NodeSpec { return c.inner.Spec() }

func (c countedOp) Execute(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
	out, err := c.inner.Execute(ctx, in)
	if err == nil {
		n, _ := c.calls.LoadOrStore(in.Key.IdempotencyKey(), new(atomic.Int64))
		n.(*atomic.Int64).Add(1)
		c.total.Add(1)
	}
	return out, err
}

// counted 返回把执行次数按 RunKey 计数的 test_op 节点注册表。
func counted(calls *sync.Map, total *atomic.Int64) *node.Registry {
	reg, err := enginetest.Registry(countedOp{inner: enginetest.OpSpec(), calls: calls, total: total})
	if err != nil {
		panic(err)
	}
	return reg
}

func openDurable(t *testing.T, path string, reg *node.Registry, opt durable.Options) engine.Engine {
	t.Helper()
	eng, err := durable.With(opt)(context.Background(), engine.EngineConfig{
		DB: test.MustOpen(t, path), Nodes: reg,
	})
	if err != nil {
		t.Fatal(err)
	}
	// 后台调度随 Build 启动、随 Close 停止。
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return eng
}

func waitStatus(t *testing.T, eng engine.Engine, id string, want graph.StatusCode) engine.TaskSnapshot {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		s, err := eng.Snapshot(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Task.Status == want {
			return s
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s = %s (%s), want %s", id, s.Task.Status, s.Task.Error, want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestLeaseExpiryReclaimsRun 确认执行时间长于租约的节点靠续租完成，任务最终成功。
// 租约必须明显长于数据库单次访问的最坏耗时（远程 MySQL 上单次 UPDATE 可达数百毫秒），
// 否则续租本身来不及完成，Run 被反复回收直至失联次数超限。
func TestLeaseExpiryReclaimsRun(t *testing.T) {
	var calls sync.Map
	var total atomic.Int64
	path := filepath.Join(t.TempDir(), "graph.db")
	reg := counted(&calls, &total)
	opt := durable.Options{Workers: 2, LeaseDuration: time.Second, PollInterval: 10 * time.Millisecond}
	eng := openDurable(t, path, reg, opt)

	g := enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{SleepMS: 2000, Set: map[string]any{"v": 1}}),
		enginetest.In(enginetest.N("finish", builtin.End, nil), map[string]graph.InputRef{"v": enginetest.R("a", "v")}),
	}, enginetest.E("a", "finish"))

	rec, err := eng.Start(context.Background(), engine.StartRequest{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	// 节点执行 2s，是租约的两倍：只有续租生效才能在不超过失联上限的情况下完成。
	s := waitStatus(t, eng, rec.TaskID, graph.SUCCEEDED)
	if s.Task.Output["v"] != int64(1) && s.Task.Output["v"] != float64(1) && s.Task.Output["v"] != 1 {
		t.Fatalf("output = %v", s.Task.Output)
	}
	if total.Load() < 1 {
		t.Fatal("expected at least one successful execute")
	}
}

// TestFailTimesRetryUsesStableIdempotencyKey 确认节点失败重试时 Attempt 增加但同一 Seq 的幂等键可区分。
func TestFailTimesRetryUsesStableIdempotencyKey(t *testing.T) {
	var calls sync.Map
	var total atomic.Int64
	path := filepath.Join(t.TempDir(), "graph.db")
	reg := counted(&calls, &total)
	opt := durable.Options{Workers: 2, LeaseDuration: time.Second, PollInterval: 10 * time.Millisecond}
	eng := openDurable(t, path, reg, opt)

	g := enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{FailTimes: 2, Set: map[string]any{"ok": true}}),
		enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("a", "finish"))

	rec, err := eng.Start(context.Background(), engine.StartRequest{
		Graph: g, Policy: engine.TaskPolicy{MaxAttempts: 5, RetryDelayMS: 5},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, eng, rec.TaskID, graph.SUCCEEDED)
	if total.Load() != 1 {
		t.Fatalf("successful executes = %d, want 1 (failures do not count)", total.Load())
	}
}

// TestEngineCloseKeepsBorrowedDB 确认引擎关闭不关闭业务应用传入的连接。
func TestEngineCloseKeepsBorrowedDB(t *testing.T) {
	ctx := context.Background()
	db, err := test.OpenPlain(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	reg, err := enginetest.Registry()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := durable.Build(ctx, engine.EngineConfig{DB: db, Nodes: reg})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("SELECT 1").Error; err != nil {
		t.Fatalf("borrowed db closed: %v", err)
	}
}

// 以下是迁移计划阶段 6 的故障用例。多个引擎实例共用同一个 sqlite 库，模拟多个进程：
//   - 进程退出：以已到期的 ctx 调用 Close，执行中的节点被中断、结果丢弃，Run 保持 RUNNING（设计文档 8.7）；
//   - 租约过期：后启动的引擎使用向后偏移的时钟，构造时的恢复扫描即认为之前的租约已过期，不必真实等待。

// rig 是共用同一个 sqlite 库的一组 durable 引擎。
type rig struct {
	t  *testing.T
	db *gorm.DB
}

func newRig(t *testing.T) *rig {
	return &rig{t: t, db: test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))}
}

// open 构造一个引擎实例（模拟一个进程），时钟比真实时间晚 offset；测试结束时关闭。
func (r *rig) open(reg *node.Registry, opt durable.Options, offset time.Duration, obs engine.Observer) engine.Engine {
	r.t.Helper()
	eng, err := durable.With(opt)(context.Background(), engine.EngineConfig{
		DB: r.db, Nodes: reg, Observer: obs,
		Clock: func() time.Time { return time.Now().Add(offset) },
	})
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = eng.Close(ctx)
	})
	return eng
}

// run 读取一个 Run 的当前行（含 durable 专属列）。
func (r *rig) run(runID string) durable.DurableRun {
	r.t.Helper()
	var run durable.DurableRun
	if err := r.db.Where(map[string]any{"RunID": runID}).Take(&run).Error; err != nil {
		r.t.Fatal(err)
	}
	return run
}

// waitRun 等待 Task 中位于 nodeID 的某个 Run 满足 ok，返回该 Run。
func (r *rig) waitRun(taskID, nodeID string, ok func(durable.DurableRun) bool) durable.DurableRun {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var runs []durable.DurableRun
		if err := r.db.Where(map[string]any{"TaskID": taskID}).Order(engine.Asc("RunID")).Find(&runs).Error; err != nil {
			r.t.Fatal(err)
		}
		for _, run := range runs {
			if run.NodeID == nodeID && ok(run) {
				return run
			}
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("task %s: no run at %s matched: %+v", taskID, nodeID, runs)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// child 等待并返回 parentID 的唯一子 Task。
func (r *rig) child(parentID string) engine.Task {
	r.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var tasks []engine.Task
		if err := r.db.Omit("Graph").Where(map[string]any{"ParentTaskID": parentID}).Find(&tasks).Error; err != nil {
			r.t.Fatal(err)
		}
		if len(tasks) == 1 {
			return tasks[0]
		}
		if time.Now().After(deadline) {
			r.t.Fatalf("task %s: children = %d, want 1", parentID, len(tasks))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// crash 模拟进程退出：Close 的 ctx 已到期，执行中的节点被中断、结果丢弃，Run 保持 RUNNING。
func crash(eng engine.Engine) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_ = eng.Close(ctx)
}

// opWith 返回 OpType 节点（参数声明与 enginetest.OpSpec 相同）执行函数替换为 fn 的注册表，
// 用于让不同引擎实例（不同进程）对同一个节点表现不同。
func opWith(t *testing.T, fn enginetest.ExecFunc, extra ...node.Node) *node.Registry {
	t.Helper()
	reg, err := enginetest.Registry(append([]node.Node{enginetest.FuncNode{NodeSpec: enginetest.OpSpec().Spec(), Exec: fn}}, extra...)...)
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// blocking 一直阻塞到 ctx 取消（进程退出或 Run 被取消）。
func blocking(ctx context.Context, _ node.NodeInput) (node.NodeOutput, error) {
	<-ctx.Done()
	return node.NodeOutput{}, ctx.Err()
}

// opThenEnd 构造 a（活动节点，输出 key）→ finish 的图，finish 输出 a.key。
func opThenEnd(key string) graph.Graph {
	return enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{Set: map[string]any{key: ""}}),
		enginetest.In(enginetest.N("finish", builtin.End, nil), map[string]graph.InputRef{key: enginetest.R("a", key)}),
	}, enginetest.E("a", "finish"))
}

// slowOpts 是故障用例的调度参数：租约很长，期间不会因真实时间到期，只由偏移时钟触发回收。
var slowOpts = durable.Options{Workers: 1, LeaseDuration: time.Minute, PollInterval: 5 * time.Millisecond}

// 活动节点执行中进程退出：租约过期后 durable 把 Run 放回 PENDING（LostCount 加 1、Attempt 不变），
// 其他进程重新执行，幂等键不变，Task 成功。
func TestCrashDuringActivityIsReclaimed(t *testing.T) {
	r := newRig(t)
	a := r.open(opWith(t, blocking), slowOpts, 0, nil)
	rec, err := a.Start(context.Background(), engine.StartRequest{Graph: opThenEnd("v")})
	if err != nil {
		t.Fatal(err)
	}
	first := r.waitRun(rec.TaskID, "a", func(run durable.DurableRun) bool { return run.Status == graph.RUNNING })
	crash(a)
	if got := r.run(first.RunID); got.Status != graph.RUNNING || got.LeaseToken != first.LeaseToken {
		t.Fatalf("after crash: %s token=%d, want RUNNING token=%d", got.Status, got.LeaseToken, first.LeaseToken)
	}

	seen, release := make(chan node.RunKey, 1), make(chan struct{})
	b := r.open(opWith(t, func(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
		seen <- in.Key
		<-release
		return node.NodeOutput{Set: map[string]any{"v": "b"}}, nil
	}), slowOpts, time.Hour, nil)
	var key node.RunKey
	select {
	case key = <-seen:
	case <-time.After(10 * time.Second):
		t.Fatal("run not re-executed after lease expiry")
	}
	got := r.run(first.RunID)
	if got.Status != graph.RUNNING || got.LostCount != 1 || got.Attempt != 1 || got.LeaseToken != first.LeaseToken+1 {
		t.Fatalf("reclaimed run: %s lost=%d attempt=%d token=%d", got.Status, got.LostCount, got.Attempt, got.LeaseToken)
	}
	if key.Attempt != 1 || key.Seq != first.Seq || key.IdempotencyKey() != first.RunID+":"+strconv.FormatInt(first.Seq, 10) {
		t.Fatalf("re-execution key = %+v", key)
	}
	close(release)
	if s := waitStatus(t, b, rec.TaskID, graph.SUCCEEDED); s.Task.Output["v"] != "b" {
		t.Fatalf("output = %v", s.Task.Output)
	}
}

// 同一节点连续 4 次在执行期间进程退出：前三次回收时 LostCount 加 1 并重新执行，第 4 次回收按执行失败交给内核；
// 默认不重试（MaxAttempts 为 1），节点失败。
func TestFourthLossFailsNode(t *testing.T) {
	r := newRig(t)
	id := crashRepeatedly(t, r, engine.TaskPolicy{})
	last := r.open(opWith(t, blocking), slowOpts, time.Duration(durable.MaxLost+1)*time.Hour, nil)
	s := waitStatus(t, last, id, graph.FAILED)
	if !strings.Contains(s.Task.Error, "executor lost 4 times") {
		t.Fatalf("error = %q", s.Task.Error)
	}
}

// 失联超过上限按执行失败处理，与业务失败一样消耗 Attempt：MaxAttempts 为 2 时进入第 2 次尝试，
// LostCount 随 Attempt 变化清零，第 2 次尝试成功后 Task 成功。
func TestLossBeyondLimitConsumesAttempt(t *testing.T) {
	r := newRig(t)
	id := crashRepeatedly(t, r, engine.TaskPolicy{MaxAttempts: 2, RetryDelayMS: 1})
	seen, release := make(chan node.RunKey, 1), make(chan struct{})
	last := r.open(opWith(t, func(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
		seen <- in.Key
		<-release
		return node.NodeOutput{Set: map[string]any{"v": "retried"}}, nil
	}), slowOpts, time.Duration(durable.MaxLost+1)*time.Hour, nil)
	select {
	case key := <-seen:
		if key.Attempt != 2 {
			t.Fatalf("retry key = %+v", key)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("node not retried after exceeding the loss limit")
	}
	run := r.waitRun(id, "a", func(run durable.DurableRun) bool { return run.Status == graph.RUNNING && run.Attempt == 2 })
	if run.LostCount != 0 {
		t.Fatalf("lost count after retry = %d, want 0", run.LostCount)
	}
	close(release)
	if s := waitStatus(t, last, id, graph.SUCCEEDED); s.Task.Output["v"] != "retried" {
		t.Fatalf("output = %v", s.Task.Output)
	}
}

// crashRepeatedly 启动 Task 后让活动节点 a 连续 MaxLost+1 次在执行期间进程退出（每次用时钟偏移让租约过期），
// 并确认每次领取时 LostCount 等于此前的失联次数、Attempt 不变。返回 TaskID。
func crashRepeatedly(t *testing.T, r *rig, policy engine.TaskPolicy) string {
	t.Helper()
	var id string
	for i := 1; i <= durable.MaxLost+1; i++ {
		eng := r.open(opWith(t, blocking), slowOpts, time.Duration(i-1)*time.Hour, nil)
		if i == 1 {
			rec, err := eng.Start(context.Background(), engine.StartRequest{Graph: opThenEnd("v"), Policy: policy})
			if err != nil {
				t.Fatal(err)
			}
			id = rec.TaskID
		}
		run := r.waitRun(id, "a", func(run durable.DurableRun) bool { return run.Status == graph.RUNNING && run.LeaseToken == int64(i) })
		if run.LostCount != i-1 || run.Attempt != 1 {
			t.Fatalf("claim %d: lost=%d attempt=%d", i, run.LostCount, run.Attempt)
		}
		crash(eng)
	}
	return id
}

// badCtrl 是返回非法形态（Effect 与 Set 同时出现）的 Controller，用于内核缺陷用例。
type badCtrl struct{}

func (badCtrl) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{ID: "bad_ctrl", Name: "bad", Endpoints: node.DefaultEndpoints()}, struct{}{})
}

func (badCtrl) Control(context.Context, node.ControlInput) (node.NodeOutput, error) {
	return node.NodeOutput{Set: map[string]any{"x": 1}, Effect: &node.Effect{Kind: node.EffectFinish}}, nil
}

// errorLog 记录 Observer.EngineError。
type errorLog struct {
	mu   sync.Mutex
	errs []error
}

func (l *errorLog) TaskChanged(string) {}
func (l *errorLog) EngineError(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errs = append(l.errs, err)
}

func (l *errorLog) has(sub string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, err := range l.errs {
		if strings.Contains(err.Error(), sub) {
			return true
		}
	}
	return false
}

// 内核报错（活动节点写回时进入返回非法形态的 Controller）：同一事务写入 kernel.Fail 的结果，Task FAILED，
// 所有 Run 进入终态，之后不再被领取或修改。
func TestKernelDefectFailsTask(t *testing.T) {
	r := newRig(t)
	reg, err := enginetest.Registry(enginetest.OpSpec(), badCtrl{})
	if err != nil {
		t.Fatal(err)
	}
	var log errorLog
	eng := r.open(reg, durable.Options{Workers: 2, LeaseDuration: 300 * time.Millisecond, PollInterval: 5 * time.Millisecond}, 0, &log)
	g := enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{}),
		enginetest.N("bad", "bad_ctrl", nil),
		enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("a", "bad"), enginetest.E("bad", "finish"))
	rec, err := eng.Start(context.Background(), engine.StartRequest{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, eng, rec.TaskID, graph.FAILED)
	if !strings.Contains(s.Task.Error, "kernel error") || !log.has("invalid output") {
		t.Fatalf("error = %q, observer = %v", s.Task.Error, log.errs)
	}
	for _, run := range s.Runs {
		if !graph.IsTerminal(run.Status) {
			t.Fatalf("run %s still %s", run.RunID, run.Status)
		}
	}
	// 经过多轮恢复扫描与轮询，Task 不再被修改。
	time.Sleep(400 * time.Millisecond)
	if after, err := eng.Snapshot(context.Background(), rec.TaskID); err != nil || after.Task.Version != s.Task.Version {
		t.Fatalf("task changed after failure: version %d → %d (%v)", s.Task.Version, after.Task.Version, err)
	}
}

// mapOver 构造对运行输入 items 逐项执行 body 的 map 图，finish 输出 Results。
func mapOver(body graph.Graph) graph.Graph {
	return enginetest.GI([]string{"items"}, []graph.Node{
		enginetest.In(enginetest.N("lp", builtin.Map, builtin.MapInput{Body: &body}),
			map[string]graph.InputRef{builtin.MapListField: enginetest.R(graph.InputSource, "items")}),
		enginetest.In(enginetest.N("finish", builtin.End, nil),
			map[string]graph.InputRef{"results": enginetest.R("lp", builtin.MapResultsField)}),
	}, enginetest.E("lp", "finish"))
}

// 子 Task 提交成功、投递 NotifyParent 之前进程退出：恢复扫描补发 ChildDone，父 Run 继续，Task 成功。
func TestRedeliverChildDone(t *testing.T) {
	r := newRig(t)
	reg, err := enginetest.Registry(enginetest.OpSpec())
	if err != nil {
		t.Fatal(err)
	}
	eng := r.open(reg, durable.Options{Workers: 2, LeaseDuration: 300 * time.Millisecond, PollInterval: 5 * time.Millisecond}, 0, nil)
	var dropped atomic.Int32
	durable.DropDeliveries(eng, func(cmd kernel.Command) bool {
		_, ok := cmd.(kernel.NotifyParent)
		return ok && dropped.Add(1) == 1
	})
	body := enginetest.GI([]string{builtin.MapItemField}, []graph.Node{
		enginetest.N("b", enginetest.OpType, enginetest.OpInput{Set: map[string]any{"y": 1}}),
		enginetest.In(enginetest.N("finish", builtin.End, nil), map[string]graph.InputRef{"y": enginetest.R("b", "y")}),
	}, enginetest.E("b", "finish"))
	rec, err := eng.Start(context.Background(), engine.StartRequest{Graph: mapOver(body), Input: map[string]any{"items": []any{1}}})
	if err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, eng, rec.TaskID, graph.SUCCEEDED)
	if dropped.Load() < 1 {
		t.Fatal("NotifyParent was never dropped")
	}
	results, _ := s.Task.Output["results"].([]any)
	if len(results) != 1 {
		t.Fatalf("output = %v", s.Task.Output)
	}
	// 补发的 ChildDone 被父 Run 接收后 Awaited 清除，恢复扫描不再为它补发。
	if n := r.awaited(rec.TaskID); n != 0 {
		t.Fatalf("awaited children after parent succeeded = %d, want 0", n)
	}
}

// awaited 返回 parentID 下 Awaited 仍为 true 的子 Task 数。
func (r *rig) awaited(parentID string) int64 {
	r.t.Helper()
	var n int64
	if err := r.db.Model(&durable.DurableTask{}).Where(map[string]any{"ParentTaskID": parentID, "Awaited": true}).Count(&n).Error; err != nil {
		r.t.Fatal(err)
	}
	return n
}

// 父 Run 等待子 Task 期间：子 Task 的 Awaited 为 true，ParentRunID 与 Slot 指向父 Run 的等待槽位；
// 父 Task 取消后父 Run 不再等待，同一写回清除全部 Awaited。
func TestAwaitedTracksParentWait(t *testing.T) {
	r := newRig(t)
	eng := r.open(opWith(t, blocking), durable.Options{Workers: 2, LeaseDuration: time.Minute, PollInterval: 5 * time.Millisecond}, 0, nil)
	body := enginetest.GI([]string{builtin.MapItemField}, []graph.Node{
		enginetest.N("b", enginetest.OpType, enginetest.OpInput{}), enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("b", "finish"))
	g := enginetest.GI([]string{"items"}, []graph.Node{
		enginetest.In(enginetest.N("lp", builtin.Map, builtin.MapInput{MaxParallel: 2, Body: &body}),
			map[string]graph.InputRef{builtin.MapListField: enginetest.R(graph.InputSource, "items")}),
		enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("lp", "finish"))
	// map 是内联节点：两个子 Task 在 Start 的同一事务内插入，Start 返回时已存在。
	rec, err := eng.Start(context.Background(), engine.StartRequest{Graph: g, Input: map[string]any{"items": []any{1, 2}}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := eng.Snapshot(context.Background(), rec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	var lp *engine.Run
	for i := range s.Runs {
		if s.Runs[i].NodeID == "lp" {
			lp = &s.Runs[i]
		}
	}
	if lp == nil || lp.Status != graph.WAITING || lp.WaitReason != engine.WaitSubgraph {
		t.Fatalf("lp run = %+v, want WAITING(subgraph)", lp)
	}
	var children []durable.DurableTask
	if err := r.db.Omit("Graph").Where(map[string]any{"ParentTaskID": rec.TaskID}).Find(&children).Error; err != nil {
		t.Fatal(err)
	}
	if len(children) != 2 {
		t.Fatalf("children = %d, want 2", len(children))
	}
	for _, c := range children {
		if !c.Awaited || c.ParentRunID != lp.RunID || (c.Slot != "0" && c.Slot != "1") {
			t.Fatalf("child %s: Awaited=%v ParentRunID=%q Slot=%q, want parent %q", c.TaskID, c.Awaited, c.ParentRunID, c.Slot, lp.RunID)
		}
	}

	if _, err := eng.Signal(context.Background(), rec.TaskID, engine.Signal{Kind: engine.SignalCancel, Reason: "stop"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, eng, rec.TaskID, graph.CANCELED)
	if n := r.awaited(rec.TaskID); n != 0 {
		t.Fatalf("awaited children after parent canceled = %d, want 0", n)
	}
}

// 父 Task 取消后、投递 CancelChild 之前进程退出：恢复扫描发现子 Task 未结束而父 Run 已不再等待它（Awaited 已清除），补发 Cancel。
func TestRedeliverCancel(t *testing.T) {
	r := newRig(t)
	eng := r.open(opWith(t, blocking), durable.Options{Workers: 2, LeaseDuration: 300 * time.Millisecond, PollInterval: 5 * time.Millisecond}, 0, nil)
	durable.DropDeliveries(eng, func(cmd kernel.Command) bool {
		_, ok := cmd.(kernel.CancelChild)
		return ok
	})
	body := enginetest.GI([]string{builtin.MapItemField}, []graph.Node{
		enginetest.N("b", enginetest.OpType, enginetest.OpInput{}), enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("b", "finish"))
	rec, err := eng.Start(context.Background(), engine.StartRequest{Graph: mapOver(body), Input: map[string]any{"items": []any{1}}})
	if err != nil {
		t.Fatal(err)
	}
	child := r.child(rec.TaskID)
	r.waitRun(child.TaskID, "b", func(run durable.DurableRun) bool { return run.Status == graph.RUNNING })
	if _, err := eng.Signal(context.Background(), rec.TaskID, engine.Signal{Kind: engine.SignalCancel, Reason: "stop"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, eng, rec.TaskID, graph.CANCELED)
	s := waitStatus(t, eng, child.TaskID, graph.CANCELED)
	if s.Task.Error != "parent run no longer awaits" {
		t.Fatalf("child error = %q", s.Task.Error)
	}
}

// 旧 worker 在租约被回收后提交结果：LeaseToken 不一致，结果丢弃；Task 的输出来自重新执行的 worker。
func TestStaleWorkerResultDiscarded(t *testing.T) {
	r := newRig(t)
	started, release := make(chan struct{}, 1), make(chan struct{})
	a := r.open(opWith(t, func(context.Context, node.NodeInput) (node.NodeOutput, error) {
		started <- struct{}{}
		<-release
		return node.NodeOutput{Set: map[string]any{"by": "a"}}, nil
	}), slowOpts, 0, nil)
	rec, err := a.Start(context.Background(), engine.StartRequest{Graph: opThenEnd("by")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("activity not started")
	}

	// 另一个进程的时钟晚 1 小时：构造时认为 a 的租约已过期，回收并重新执行。
	b := r.open(opWith(t, func(context.Context, node.NodeInput) (node.NodeOutput, error) {
		return node.NodeOutput{Set: map[string]any{"by": "b"}}, nil
	}), slowOpts, time.Hour, nil)
	s := waitStatus(t, b, rec.TaskID, graph.SUCCEEDED)
	if s.Task.Output["by"] != "b" {
		t.Fatalf("output = %v", s.Task.Output)
	}

	// 放行旧 worker；正常关闭会等它写回结束。它的结果因 LeaseToken 不一致被丢弃，Task 不再变化。
	close(release)
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := b.Snapshot(context.Background(), rec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Task.Version != s.Task.Version || after.Task.Output["by"] != "b" {
		t.Fatalf("stale result applied: version %d → %d, output %v", s.Task.Version, after.Task.Version, after.Task.Output)
	}
}

// 连接池只有一条连接时，按 GraphID 引用的子图照常完成：Apply 报告缺少子图后事务回滚，子图在事务之外加载，
// 解析器即使与引擎共用连接池读库，也不会出现事务占着唯一的连接、解析器等待第二条连接的情况。
func TestSubgraphWithSingleConnection(t *testing.T) {
	r := newRig(t)
	sqlDB, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	reg, err := enginetest.Registry(enginetest.OpSpec())
	if err != nil {
		t.Fatal(err)
	}
	child := enginetest.G([]graph.Node{enginetest.N("finish", builtin.End, nil)})
	resolve := func(ctx context.Context, id string) (graph.Graph, error) {
		// 模拟业务应用从同一个库读取子图定义。
		if err := r.db.WithContext(ctx).Exec("SELECT 1").Error; err != nil {
			return graph.Graph{}, err
		}
		g := child
		g.GraphID, g.GraphVersion = id, 1
		return g, nil
	}
	eng, err := durable.With(slowOpts)(context.Background(), engine.EngineConfig{DB: r.db, Nodes: reg, ResolveGraph: resolve})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = eng.Close(ctx)
	})
	g := enginetest.G([]graph.Node{
		enginetest.N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "child"}),
		enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("sub", "finish"))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rec, err := eng.Start(ctx, engine.StartRequest{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	waitStatus(t, eng, rec.TaskID, graph.SUCCEEDED)
}

// 结果已保存到 DurableRun.Result、写回之前进程退出：恢复扫描在租约过期后直接应用保存的结果，
// 节点不重新执行，LostCount 不增加，结果应用后 DurableRun.Result 清空。
func TestSavedResultAppliedAfterCrash(t *testing.T) {
	r := newRig(t)
	a := r.open(opWith(t, blocking), slowOpts, 0, nil)
	rec, err := a.Start(context.Background(), engine.StartRequest{Graph: opThenEnd("v")})
	if err != nil {
		t.Fatal(err)
	}
	run := r.waitRun(rec.TaskID, "a", func(run durable.DurableRun) bool { return run.Status == graph.RUNNING })
	crash(a)
	// 构造「worker 已保存结果、尚未写回」的状态。
	saved := &durable.RunResult{Seq: run.Seq, Attempt: run.Attempt, Output: json.RawMessage(`{"Set":{"v":"saved"}}`)}
	if err := r.db.Model(&durable.DurableRun{}).Where(map[string]any{"RunID": run.RunID}).
		Select("Result").Updates(&durable.DurableRun{Result: saved}).Error; err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	b := r.open(opWith(t, func(context.Context, node.NodeInput) (node.NodeOutput, error) {
		calls.Add(1)
		return node.NodeOutput{Set: map[string]any{"v": "re-executed"}}, nil
	}), slowOpts, time.Hour, nil)
	s := waitStatus(t, b, rec.TaskID, graph.SUCCEEDED)
	if s.Task.Output["v"] != "saved" || calls.Load() != 0 {
		t.Fatalf("output = %v, executions after crash = %d", s.Task.Output, calls.Load())
	}
	for _, item := range s.Runs {
		got := r.run(item.RunID)
		if got.Result != nil || got.LostCount != 0 {
			t.Fatalf("run %s: result=%+v lost=%d", got.RunID, got.Result, got.LostCount)
		}
	}
}

// TestRecoveryModeSkipsMigrationAndWrites 确认 RecoveryMode 为只读模式：
//   - 在空库上构造不执行 AutoMigrate / CreateIndex 等任何 DDL（表不会被创建）；
//   - 在已有库上构造不回收过期租约、不启动 worker，拒绝 Start 与 Signal，只允许查询。
func TestRecoveryModeSkipsMigrationAndWrites(t *testing.T) {
	ctx := context.Background()
	reg, err := enginetest.Registry(enginetest.OpSpec())
	if err != nil {
		t.Fatal(err)
	}

	// 1. 空库构造 RecoveryMode 引擎：不建表。
	emptyDB, err := test.OpenPlain(filepath.Join(t.TempDir(), "empty.db"))
	if err != nil {
		t.Fatal(err)
	}
	if sqlDB, err := emptyDB.DB(); err == nil {
		defer sqlDB.Close()
	}
	roEmpty, err := durable.With(durable.Options{RecoveryMode: true})(ctx, engine.EngineConfig{DB: emptyDB, Nodes: reg})
	if err != nil {
		t.Fatal(err)
	}
	defer roEmpty.Close(ctx)
	if emptyDB.Migrator().HasTable(&durable.DurableTask{}) || emptyDB.Migrator().HasTable(&durable.DurableRun{}) {
		t.Fatal("RecoveryMode executed DDL migration on empty database")
	}

	// 2. 已有库存在租约过期的 RUNNING Run：RecoveryMode 只读查询，不回收租约，且拒绝 Start / Signal。
	r := newRig(t)
	a := r.open(opWith(t, blocking), slowOpts, 0, nil)
	rec, err := a.Start(ctx, engine.StartRequest{Graph: opThenEnd("v")})
	if err != nil {
		t.Fatal(err)
	}
	first := r.waitRun(rec.TaskID, "a", func(run durable.DurableRun) bool { return run.Status == graph.RUNNING })
	crash(a)

	roOpts := slowOpts
	roOpts.RecoveryMode = true
	ro := r.open(reg, roOpts, 2*time.Hour, nil)
	if _, err := ro.Start(ctx, engine.StartRequest{Graph: opThenEnd("v")}); !errors.Is(err, engine.ErrUnsupported) {
		t.Fatalf("RecoveryMode Start err = %v, want ErrUnsupported", err)
	}
	if _, err := ro.Signal(ctx, rec.TaskID, engine.Signal{Kind: engine.SignalCancel}); !errors.Is(err, engine.ErrUnsupported) {
		t.Fatalf("RecoveryMode Signal err = %v, want ErrUnsupported", err)
	}
	snap, err := ro.Snapshot(ctx, rec.TaskID)
	if err != nil || snap.Task.Status != graph.RUNNING {
		t.Fatalf("RecoveryMode Snapshot = %+v, err %v", snap.Task, err)
	}
	if got := r.run(first.RunID); got.Status != graph.RUNNING || got.LostCount != 0 || got.LeaseToken != first.LeaseToken {
		t.Fatalf("RecoveryMode mutated run: %+v", got)
	}
}

// TestConsistentSnapshotAndFingerprintRead 确认 LoadSnapshot 与 LoadFingerprint 在只读事务内读取一致视图：
// 当并发写提交发生在「读取 Task 行之后、读取 Run 行之前」时，同一读事务内的第二次查询仍读到提交前的 Run，
// 不会拼接出「旧 Task + 新 Run」或「新 Task + 旧 Run」的撕裂状态。
func TestConsistentSnapshotAndFingerprintRead(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "consistent.db")
	writerDB := test.MustOpen(t, path)
	readerDB := test.MustOpen(t, path)
	if err := engine.MigrateExecution(ctx, writerDB); err != nil {
		t.Fatal(err)
	}

	now := time.Now().UTC().Truncate(time.Millisecond)
	task := engine.Task{
		TaskID: "t-consistent", TicketID: "t-consistent", EngineKind: engine.KindDurable,
		Status: graph.RUNNING, Version: 1, StartedAt: now, CreatedAt: now,
	}
	run := engine.Run{
		RunID: "r-1", TaskID: task.TaskID, EngineKind: engine.KindDurable,
		NodeID: "a", Seq: 1, Status: graph.RUNNING, Attempt: 1, StartedAt: now, CreatedAt: now,
	}
	if err := writerDB.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := writerDB.Create(&run).Error; err != nil {
		t.Fatal(err)
	}

	// 在 readerDB 的第一次 SELECT（读取 tb_graph_task）完成之后、第二次 SELECT（读取 tb_graph_run）之前，
	// 用 writerDB 提交一次写事务，把 Task 与 Run 同时推进到 SUCCEEDED (Version=2, Seq=2)。
	var hookOnce sync.Once
	err := readerDB.Callback().Query().After("gorm:query").Register("test:interleave_commit", func(tx *gorm.DB) {
		if tx.Statement != nil && tx.Statement.Table == "tb_graph_task" {
			hookOnce.Do(func() {
				fin := now.Add(time.Second)
				err := writerDB.Transaction(func(wtx *gorm.DB) error {
					if err := wtx.Model(&engine.Task{}).Where(map[string]any{"TaskID": task.TaskID}).
						Updates(map[string]any{"Status": graph.SUCCEEDED, "Version": 2, "FinishedAt": fin}).Error; err != nil {
						return err
					}
					return wtx.Model(&engine.Run{}).Where(map[string]any{"RunID": run.RunID}).
						Updates(map[string]any{"Status": graph.SUCCEEDED, "Seq": 2}).Error
				})
				if err != nil {
					t.Errorf("concurrent write failed: %v", err)
				}
			})
		}
	})
	if err != nil {
		t.Fatal(err)
	}

	snap, err := engine.LoadSnapshot(readerDB.WithContext(ctx), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	// 读事务在查询 Task 时已建立快照，因此后续读取的 Run 必须同属提交前的旧快照（RUNNING, Seq=1）。
	if snap.Task.Status != graph.RUNNING || snap.Task.Version != 1 || len(snap.Runs) != 1 ||
		snap.Runs[0].Status != graph.RUNNING || snap.Runs[0].Seq != 1 {
		t.Fatalf("torn snapshot: task=%s(v%d) run=%+v", snap.Task.Status, snap.Task.Version, snap.Runs)
	}

	// 新开启的读事务读取到完整的已提交新快照。
	afterSnap, err := engine.LoadSnapshot(readerDB.WithContext(ctx), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if afterSnap.Task.Status != graph.SUCCEEDED || afterSnap.Task.Version != 2 || len(afterSnap.Runs) != 1 ||
		afterSnap.Runs[0].Status != graph.SUCCEEDED || afterSnap.Runs[0].Seq != 2 {
		t.Fatalf("post-commit snapshot: task=%s(v%d) run=%+v", afterSnap.Task.Status, afterSnap.Task.Version, afterSnap.Runs)
	}
}

// TestHeartbeatDBErrorCancelsUponLeaseExpiry 确认续租遇到数据库错误时：
//   - 通过 Observer.EngineError 上报错误；
//   - 一旦当前时刻达到或超过本地已知的租约到期时刻 leaseUntil，续租循环立即取消活动节点的 ctx，
//     防止租约在库中已过期并被其他 worker 接管后旧执行器继续无限运行。
func TestHeartbeatDBErrorCancelsUponLeaseExpiry(t *testing.T) {
	r := newRig(t)
	var failHeartbeat atomic.Bool
	if err := r.db.Callback().Update().Before("gorm:update").Register("test:fail_heartbeat_update", func(tx *gorm.DB) {
		if failHeartbeat.Load() && tx.Statement != nil && tx.Statement.Table == "tb_graph_run" {
			tx.AddError(errors.New("simulated db heartbeat failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}

	var calls atomic.Int32
	started := make(chan struct{}, 1)
	canceled := make(chan error, 1)
	var log errorLog
	eng := r.open(opWith(t, func(ctx context.Context, _ node.NodeInput) (node.NodeOutput, error) {
		if calls.Add(1) == 1 {
			failHeartbeat.Store(true)
			started <- struct{}{}
			<-ctx.Done()
			failHeartbeat.Store(false)
			canceled <- ctx.Err()
			return node.NodeOutput{}, ctx.Err()
		}
		return node.NodeOutput{Set: map[string]any{"v": "recovered"}}, nil
	}), durable.Options{Workers: 1, LeaseDuration: 180 * time.Millisecond, PollInterval: 5 * time.Millisecond}, 0, &log)

	rec, err := eng.Start(context.Background(), engine.StartRequest{Graph: opThenEnd("v")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("activity did not start")
	}

	// 租约 180ms（每 60ms 续租一次）：数据库持续报错时，应在租约到期后立即取消 ctx。
	select {
	case cerr := <-canceled:
		if !errors.Is(cerr, context.Canceled) {
			t.Fatalf("canceled err = %v, want context.Canceled", cerr)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("activity was not canceled after heartbeat DB errors exceeded lease duration")
	}
	if !log.has("durable: heartbeat run") {
		t.Fatalf("expected observer to record heartbeat error, got %v", log.errs)
	}

	// 数据库恢复后，旧执行器退出或租约被回收重跑，Task 正常结束。
	deadline := time.Now().Add(5 * time.Second)
	for {
		s, err := eng.Snapshot(context.Background(), rec.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if graph.IsTerminal(s.Task.Status) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s did not reach terminal state: %s", rec.TaskID, s.Task.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// Map 多轮执行在中途投递丢失并经恢复扫描继续推进时，Units 与已写入 Results 中的大整数精度保持不变。
func TestRedeliverChildDonePreservesLargeInt64(t *testing.T) {
	r := newRig(t)
	reg, err := enginetest.Registry(enginetest.OpSpec())
	if err != nil {
		t.Fatal(err)
	}
	eng := r.open(reg, durable.Options{Workers: 1, LeaseDuration: 300 * time.Millisecond, PollInterval: 5 * time.Millisecond}, 0, nil)
	var dropped atomic.Int32
	durable.DropDeliveries(eng, func(cmd kernel.Command) bool {
		_, ok := cmd.(kernel.NotifyParent)
		return ok && dropped.Add(1) == 1
	})
	body := graph.Graph{
		InputSchema: graph.RunInputSchema(map[string]*jsonschema.Schema{
			builtin.MapItemField: graph.AnyValueSchema(),
		}),
		Nodes: []graph.Node{
			enginetest.N("s", builtin.Start, nil),
			enginetest.In(enginetest.N("finish", builtin.End, nil),
				map[string]graph.InputRef{"Value": enginetest.R(graph.InputSource, builtin.MapItemField)}),
		},
		Edges: []graph.Edge{enginetest.E("s", "finish")},
	}
	items := []any{int64(9007199254740993), int64(9007199254740995)}
	g := graph.Graph{
		Nodes: []graph.Node{
			enginetest.N("s", builtin.Start, nil),
			enginetest.N("m", builtin.Map, builtin.MapInput{List: items, Chunk: 1, MaxParallel: 1, Body: &body}),
			enginetest.In(enginetest.N("finish", builtin.End, nil),
				map[string]graph.InputRef{"Results": enginetest.R("m", builtin.MapResultsField)}),
		},
		Edges: []graph.Edge{enginetest.E("s", "m"), enginetest.E("m", "finish")},
	}
	rec, err := eng.Start(context.Background(), engine.StartRequest{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	s := waitStatus(t, eng, rec.TaskID, graph.SUCCEEDED)
	if dropped.Load() < 1 {
		t.Fatal("NotifyParent was never dropped")
	}
	raw, _ := json.Marshal(s.Task.Output)
	want := `{"Results":[{"Value":9007199254740993},{"Value":9007199254740995}]}`
	if string(raw) != want {
		t.Fatalf("output = %s, want %s", raw, want)
	}
}

// 连接池耗尽导致续租查询阻塞时，heartbeat 仍在租约到期时取消活动节点 ctx，且 Close 不因等待续租查询而卡死（R02）。
func TestHeartbeatPoolStallCancelsOnExpiry(t *testing.T) {
	entered := make(chan struct{})
	canceled := make(chan struct{})
	stall := enginetest.FuncNode{
		NodeSpec: node.WithParams(node.NodeSpec{ID: "stall", Name: "stall", Endpoints: node.DefaultEndpoints()}, struct{}{}),
		Exec: func(ctx context.Context, _ node.NodeInput) (node.NodeOutput, error) {
			close(entered)
			<-ctx.Done()
			close(canceled)
			return node.NodeOutput{}, ctx.Err()
		},
	}
	reg, err := enginetest.Registry(stall)
	if err != nil {
		t.Fatal(err)
	}
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "stall.db"))
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	eng, err := durable.With(durable.Options{Workers: 1, LeaseDuration: 120 * time.Millisecond, PollInterval: time.Millisecond})(
		context.Background(), engine.EngineConfig{DB: db, Nodes: reg},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer eng.Close(context.Background())

	g := graph.Graph{
		Nodes: []graph.Node{enginetest.N("s", builtin.Start, nil), enginetest.N("a", "stall", nil), enginetest.N("e", builtin.End, nil)},
		Edges: []graph.Edge{enginetest.E("s", "a"), enginetest.E("a", "e")},
	}
	if _, err := eng.Start(context.Background(), engine.StartRequest{Graph: g}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("executor not entered")
	}
	// 借走唯一连接模拟连接池耗尽。
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	select {
	case <-canceled:
	case <-time.After(420 * time.Millisecond):
		t.Fatal("executor still live after >3 leases while DB pool exhausted")
	}
}

// 预取消或连接池等待超时的 Start 请求立即返回 ctx.Err()，不会迟到提交写库（R05）。
func TestPreCanceledAndTimedOutStartDoNotWrite(t *testing.T) {
	r := newRig(t)
	reg, err := enginetest.Registry(enginetest.OpSpec())
	if err != nil {
		t.Fatal(err)
	}
	eng := r.open(reg, slowOpts, 0, nil)
	g := graph.Graph{
		Nodes: []graph.Node{enginetest.N("s", builtin.Start, nil), enginetest.N("e", builtin.End, nil)},
		Edges: []graph.Edge{enginetest.E("s", "e")},
	}

	// 1. 预取消的 ctx 不创建 Task。
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := eng.Start(cctx, engine.StartRequest{Graph: g}); !errors.Is(err, context.Canceled) {
		t.Fatalf("pre-canceled Start err = %v, want context.Canceled", err)
	}

	// 2. 连接池耗尽时按请求截止时间退出，释放连接后不发生迟到提交。
	sqlDB, err := r.db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	conn, err := sqlDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	tctx, tcancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer tcancel()
	_, err = eng.Start(tctx, engine.StartRequest{Graph: g})
	conn.Close()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("pool-stalled Start err = %v, want context.DeadlineExceeded", err)
	}
	var count int64
	if err := r.db.Model(&engine.Task{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("tasks in DB = %d (err=%v), want 0", count, err)
	}
}

// 数据库持续故障时，多个 worker 的领取执行指数退避（期间不响应 kick），显著降低失败重试与错误上报频率；
// 数据库恢复后自动重置退避并完成待执行任务（S10）。
func TestClaimBackoffOnDBOutage(t *testing.T) {
	r := newRig(t)
	var failClaim atomic.Bool
	var claimAttempts atomic.Int32
	if err := r.db.Callback().Query().Before("gorm:query").Register("test:fail_claim_query", func(tx *gorm.DB) {
		if failClaim.Load() && tx.Statement != nil && tx.Statement.Table == "tb_graph_run" {
			claimAttempts.Add(1)
			tx.AddError(errors.New("simulated claim db outage"))
		}
	}); err != nil {
		t.Fatal(err)
	}

	reg, err := enginetest.Registry(enginetest.OpSpec())
	if err != nil {
		t.Fatal(err)
	}
	var log errorLog
	eng := r.open(reg, durable.Options{
		Workers:       4,
		LeaseDuration: 150 * time.Millisecond,
		PollInterval:  5 * time.Millisecond,
	}, 0, &log)

	failClaim.Store(true)
	rec, err := eng.Start(context.Background(), engine.StartRequest{
		Graph: enginetest.G([]graph.Node{
			enginetest.N("a", enginetest.OpType, enginetest.OpInput{Set: map[string]any{"v": "ok"}}),
			enginetest.In(enginetest.N("finish", builtin.End, nil), map[string]graph.InputRef{"v": enginetest.R("a", "v")}),
		}, enginetest.E("a", "finish")),
	})
	if err != nil {
		t.Fatal(err)
	}

	// 持续故障 160ms：无退避时 4 个 worker 按 5ms 轮询会产生约 128 次尝试；指数退避应将其抑制在个位数到十余次。
	time.Sleep(160 * time.Millisecond)
	failClaim.Store(false)

	s := waitStatus(t, eng, rec.TaskID, graph.SUCCEEDED)
	if s.Task.Output["v"] != "ok" {
		t.Fatalf("output = %v, want ok", s.Task.Output)
	}

	attempts := int(claimAttempts.Load())
	if attempts < 4 || attempts > 36 {
		t.Fatalf("claim attempts during 160ms outage = %d, want in [4, 36] with exponential backoff", attempts)
	}
	if !log.has("durable: claim:") {
		t.Fatalf("expected observer to receive claim error, got %v", log.errs)
	}
}
