package immediate_test

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/test"
	"github.com/golang-io/graph/test/engine/enginetest"
	"gorm.io/gorm"
)

func registry(t *testing.T) *node.Registry {
	t.Helper()
	reg, err := enginetest.Registry(enginetest.OpSpec())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// probeRegistry 返回核心节点注册表，并注册一个由 fn 执行的 probe 节点（活动节点）。
func probeRegistry(t *testing.T, fn enginetest.ExecFunc) *node.Registry {
	t.Helper()
	reg, err := enginetest.Registry(enginetest.FuncNode{
		NodeSpec: node.WithParams(node.NodeSpec{ID: "probe", Name: "probe", Endpoints: node.DefaultEndpoints()}, struct{}{}),
		Exec:     fn,
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// openProbe 打开带库的 immediate 引擎，probe 节点由 fn 执行；测试结束时关闭。
func openProbe(t *testing.T, opt immediate.Options, fn enginetest.ExecFunc) (engine.Engine, *gorm.DB) {
	t.Helper()
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))
	eng, err := immediate.With(opt)(context.Background(), engine.EngineConfig{DB: db, Nodes: probeRegistry(t, fn)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close(context.Background()) })
	return eng, db
}

// probeGraph 是 start → work(probe) → finish 的最小图。
func probeGraph() graph.Graph {
	return enginetest.G([]graph.Node{
		enginetest.N("work", "probe", nil),
		enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("work", "finish"))
}

// blocking 返回阻塞到 release 关闭（或 ctx 取消）的执行函数，开始执行时把 TaskID 写入 started。
func blocking(started chan<- string, release <-chan struct{}) enginetest.ExecFunc {
	return func(c context.Context, in node.NodeInput) (node.NodeOutput, error) {
		started <- in.Key.TaskID
		select {
		case <-release:
			return node.NodeOutput{}, nil
		case <-c.Done():
			return node.NodeOutput{}, c.Err()
		}
	}
}

func snapshot(t *testing.T, eng engine.Engine, id string, want graph.StatusCode) engine.TaskSnapshot {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
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
		time.Sleep(5 * time.Millisecond)
	}
}

// breakAt 返回在活动节点 nodeIDs 前停住的调试策略；Task 因此进入 WAITING(debug)，用 Resume 放行。
func breakAt(nodeIDs ...string) engine.TaskPolicy {
	return engine.TaskPolicy{Debug: &engine.DebugOptions{Breakpoints: nodeIDs}}
}

func stored(t *testing.T, db *gorm.DB, id string) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&engine.Task{}).Where(map[string]any{"TaskID": id}).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

// 运行期间不写库；Task 结束时一次写入 Task 与全部 Run（设计文档 9.1、9.3）。
func TestWriteOnlyWhenFinished(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan string, 1), make(chan struct{})
	eng, db := openProbe(t, immediate.Options{}, blocking(started, release))
	rec, err := eng.Start(ctx, engine.StartRequest{Graph: probeGraph()})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if n := stored(t, db, rec.TaskID); n != 0 {
		t.Fatalf("running task already stored (%d rows)", n)
	}
	if s := snapshot(t, eng, rec.TaskID, graph.RUNNING); len(s.Runs) == 0 {
		t.Fatal("running snapshot has no runs")
	}
	close(release)
	snapshot(t, eng, rec.TaskID, graph.SUCCEEDED)
	deadline := time.Now().Add(5 * time.Second)
	for stored(t, db, rec.TaskID) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("finished task not stored")
		}
		time.Sleep(5 * time.Millisecond)
	}
	s, err := engine.LoadSnapshot(db, rec.TaskID)
	if err != nil || s.Task.Status != graph.SUCCEEDED || len(s.Runs) == 0 {
		t.Fatalf("stored snapshot = %+v, err %v", s.Task, err)
	}
	for _, r := range s.Runs {
		if !graph.IsTerminal(r.Status) {
			t.Fatalf("stored run %s is %s", r.RunID, r.Status)
		}
	}
}

// Close 取消执行中与停在调试等待上的 Task（原因 engine closed），写入 CANCELED 后返回；
// 之后同一数据库上的新引擎从库中读到终态（设计文档 9.4）。
func TestCloseCancelsTasks(t *testing.T) {
	ctx := context.Background()
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))
	cfg := engine.EngineConfig{DB: db, Nodes: registry(t)}
	a, err := immediate.Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	blockGraph := enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{Block: true}), enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("a", "finish"))
	waitGraph := enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{}), enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("a", "finish"))
	running, err := a.Start(ctx, engine.StartRequest{Graph: blockGraph})
	if err != nil {
		t.Fatal(err)
	}
	waiting, err := a.Start(ctx, engine.StartRequest{Graph: waitGraph, Policy: breakAt("a")})
	if err != nil {
		t.Fatal(err)
	}
	snapshot(t, a, waiting.TaskID, graph.WAITING)
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Start(ctx, engine.StartRequest{Graph: waitGraph}); !errors.Is(err, engine.ErrUnsupported) {
		t.Fatalf("start after close err = %v, want ErrUnsupported", err)
	}

	b, err := immediate.Build(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	for _, id := range []string{running.TaskID, waiting.TaskID} {
		s, err := b.Snapshot(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if s.Task.Status != graph.CANCELED || !strings.Contains(s.Task.Error, "engine closed") {
			t.Fatalf("task %s = %s (%s), want CANCELED by engine close", id, s.Task.Status, s.Task.Error)
		}
		// 已结束的 Task 再收到信号：返回终态回执，不报错。
		rec, err := b.Signal(ctx, id, engine.Signal{Kind: engine.SignalResume})
		if err != nil || rec.Status != graph.CANCELED {
			t.Fatalf("resume stored task = %+v, err %v", rec, err)
		}
	}
}

// 没有数据库时，已结束的 Task 在保留期后移出内存，之后查询返回 ErrNotFound。
func TestRetainWithoutDB(t *testing.T) {
	ctx := context.Background()
	eng := immediate.New(engine.EngineConfig{Nodes: registry(t)}, immediate.Options{Retain: 50 * time.Millisecond})
	defer eng.Close(ctx)
	rec, err := eng.Start(ctx, engine.StartRequest{Graph: enginetest.G([]graph.Node{enginetest.N("finish", builtin.End, nil)})})
	if err != nil {
		t.Fatal(err)
	}
	snapshot(t, eng, rec.TaskID, graph.SUCCEEDED)
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := eng.Snapshot(ctx, rec.TaskID)
		if errors.Is(err, engine.ErrNotFound) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("task still retained, err %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, err := eng.Watch(ctx, rec.TaskID); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("watch after retain err = %v, want ErrNotFound", err)
	}
}

// Retain 小于等于 0 时任务结束即释放，不走 time.AfterFunc 延迟注销；纯内存场景下结束后立即返回 ErrNotFound。
func TestRetainZero(t *testing.T) {
	for _, retain := range []time.Duration{0, -time.Second} {
		t.Run(retain.String(), func(t *testing.T) {
			ctx := context.Background()
			eng := immediate.New(engine.EngineConfig{Nodes: registry(t)}, immediate.Options{Retain: retain})
			defer eng.Close(ctx)
			rec, err := eng.Start(ctx, engine.StartRequest{Graph: enginetest.G([]graph.Node{enginetest.N("finish", builtin.End, nil)})})
			if err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				_, err := eng.Snapshot(ctx, rec.TaskID)
				if errors.Is(err, engine.ErrNotFound) {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("task still retained with Retain: %v, err = %v", retain, err)
				}
				time.Sleep(5 * time.Millisecond)
			}
			if _, err := eng.Watch(ctx, rec.TaskID); !errors.Is(err, engine.ErrNotFound) {
				t.Fatalf("watch err = %v, want ErrNotFound", err)
			}
		})
	}
}

// 超过 MaxRetainedTasks 时按完成先后顺序（FIFO）自动淘汰最老完成的任务。
func TestMaxRetainedTasksCap(t *testing.T) {
	ctx := context.Background()
	eng := immediate.New(engine.EngineConfig{Nodes: registry(t)}, immediate.Options{
		Retain:           time.Minute,
		MaxRetainedTasks: 3,
	})
	defer eng.Close(ctx)

	startAndFinish := func() string {
		rec, err := eng.Start(ctx, engine.StartRequest{
			Graph: enginetest.G([]graph.Node{enginetest.N("finish", builtin.End, nil)}),
		})
		if err != nil {
			t.Fatal(err)
		}
		snapshot(t, eng, rec.TaskID, graph.SUCCEEDED)
		return rec.TaskID
	}

	t1 := startAndFinish()
	t2 := startAndFinish()
	t3 := startAndFinish()

	for _, id := range []string{t1, t2, t3} {
		if s, err := eng.Snapshot(ctx, id); err != nil || s.Task.Status != graph.SUCCEEDED {
			t.Fatalf("task %s not retained: %v", id, err)
		}
	}

	// 启动并完成第 4 个 Task：容量上限为 3，最老完成的 t1 被淘汰，t2、t3、t4 保留。
	t4 := startAndFinish()
	if _, err := eng.Snapshot(ctx, t1); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("t1 should be evicted, err = %v", err)
	}
	for _, id := range []string{t2, t3, t4} {
		if s, err := eng.Snapshot(ctx, id); err != nil || s.Task.Status != graph.SUCCEEDED {
			t.Fatalf("task %s should be retained: %v", id, err)
		}
	}
}

// Execute 在纯内存中执行，不需要数据库；停在调试等待上时返回当时的快照。
func TestExecuteInMemory(t *testing.T) {
	g := enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{Set: map[string]any{"a": 1}}),
		enginetest.N("b", enginetest.OpType, enginetest.OpInput{Set: map[string]any{"b": 2}}),
		enginetest.In(enginetest.N("finish", builtin.End, nil), map[string]graph.InputRef{"a": enginetest.R("a", "a"), "b": enginetest.R("b", "b")}),
	}, enginetest.E("a", "b"), enginetest.E("b", "finish"))
	cfg := engine.EngineConfig{Nodes: registry(t)}
	req := engine.StartRequest{Graph: g, Policy: breakAt("b")}
	s, err := immediate.Execute(context.Background(), cfg, req)
	if err != nil {
		t.Fatal(err)
	}
	// a 已成功，停在 b 前的断点。
	if s.Task.Status != graph.WAITING || len(s.Runs) != 1 || s.Runs[0].NodeID != "b" || s.Runs[0].WaitReason != engine.WaitDebug {
		t.Fatalf("snapshot = %s %+v", s.Task.Status, s.Runs)
	}

	s, err = immediate.Execute(context.Background(), cfg, engine.StartRequest{Graph: g})
	if err != nil || s.Task.Status != graph.SUCCEEDED {
		t.Fatalf("execute = %s (%s), err %v", s.Task.Status, s.Task.Error, err)
	}
	if s.Task.Output["a"] != int64(1) || s.Task.Output["b"] != int64(2) {
		t.Fatalf("output = %v", s.Task.Output)
	}
}

// TestCancelDiscardsLateOutput 确认取消后执行中节点的 context 被取消，晚到的结果被丢弃。
func TestCancelDiscardsLateOutput(t *testing.T) {
	ctx := context.Background()
	started := make(chan string, 1)
	release := make(chan struct{})
	observed := make(chan error, 1)
	eng, _ := openProbe(t, immediate.Options{}, func(c context.Context, in node.NodeInput) (node.NodeOutput, error) {
		started <- in.Key.TaskID
		<-release
		observed <- c.Err()
		return node.NodeOutput{Set: map[string]any{"effect": "finished after cancellation"}}, nil
	})
	if _, err := eng.Start(ctx, engine.StartRequest{Graph: probeGraph()}); err != nil {
		t.Fatal(err)
	}
	id := <-started
	if _, err := eng.Signal(ctx, id, engine.Signal{Kind: engine.SignalCancel, Reason: "user canceled"}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-observed; !errors.Is(err, context.Canceled) {
		t.Fatalf("context=%v", err)
	}
	if s := snapshot(t, eng, id, graph.CANCELED); s.Task.Output["effect"] != nil {
		t.Fatalf("late output leaked: %v", s.Task.Output)
	}
}

// TestResumeKeepsMaxParallel 确认从断点恢复后仍按 Policy.MaxParallel=1 限制并行分支。
// 断点生效期间 immediate 强制串行，因此恢复时传入空的 Debug 清除断点，让并行上限只由 MaxParallel 决定。
func TestResumeKeepsMaxParallel(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan string, 2), make(chan struct{})
	eng, _ := openProbe(t, immediate.Options{}, blocking(started, release))
	g := enginetest.G([]graph.Node{
		enginetest.N("p", builtin.Parallel, nil),
		enginetest.N("a", "probe", nil),
		enginetest.N("b", "probe", nil),
		enginetest.N("m", builtin.Merge, builtin.MergeInput{Mode: node.JoinAll}),
		enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("p", "a"), enginetest.E("p", "b"),
		enginetest.E("a", "m"), enginetest.E("b", "m"), enginetest.E("m", "finish"))
	policy := breakAt("a", "b")
	policy.ExecutionMode, policy.MaxParallel = graph.ExecParallel, 1
	rec, err := eng.Start(ctx, engine.StartRequest{Graph: g, Policy: policy})
	if err != nil {
		t.Fatal(err)
	}
	s := snapshot(t, eng, rec.TaskID, graph.WAITING)
	if _, err := eng.Signal(ctx, rec.TaskID, engine.Signal{Kind: engine.SignalResume, Resume: &engine.ResumeRequest{Debug: &engine.DebugOptions{}}}); err != nil {
		t.Fatal(err)
	}
	<-started
	simultaneous := 1
	select {
	case <-started:
		simultaneous = 2
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	if s.Task.Policy.MaxParallel != 1 || simultaneous != 1 {
		t.Fatalf("policy=%+v started=%d", s.Task.Policy, simultaneous)
	}
	snapshot(t, eng, rec.TaskID, graph.SUCCEEDED)
}

// TestInvalidNodeOutputFailsTask 确认执行器返回无法序列化的值时按节点失败处理，任务失败并带错误信息。
func TestInvalidNodeOutputFailsTask(t *testing.T) {
	reg := probeRegistry(t, func(context.Context, node.NodeInput) (node.NodeOutput, error) {
		return node.NodeOutput{Set: map[string]any{"bad": func() {}}}, nil
	})
	s, err := immediate.Execute(context.Background(), engine.EngineConfig{Nodes: reg}, engine.StartRequest{Graph: probeGraph()})
	if err != nil {
		t.Fatal(err)
	}
	if s.Task.Status != graph.FAILED || s.Task.Error == "" {
		t.Fatalf("snapshot=%+v", s.Task)
	}
}

// TestCloseTimeoutAndRetry 验证 G06：Close 超时后再次调用不能误报成功，必须持续等待直到真正退出。
func TestCloseTimeoutAndRetry(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan string, 1), make(chan struct{})
	uncooperative := func(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
		started <- in.Key.TaskID
		<-release
		return node.NodeOutput{}, nil
	}
	reg := probeRegistry(t, uncooperative)
	eng := immediate.New(engine.EngineConfig{Nodes: reg}, immediate.DefaultOptions())
	var releaseOnce sync.Once
	doRelease := func() { releaseOnce.Do(func() { close(release) }) }
	defer func() {
		doRelease()
		_ = eng.Close(context.Background())
	}()

	rec, err := eng.Start(ctx, engine.StartRequest{Graph: probeGraph()})
	if err != nil {
		t.Fatal(err)
	}
	<-started

	// 首次 Close 超时
	ctx1, cancel1 := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel1()
	err1 := eng.Close(ctx1)
	if !errors.Is(err1, context.DeadlineExceeded) {
		t.Fatalf("first close err = %v, want DeadlineExceeded", err1)
	}

	// 第二次短超时 Close 仍应超时，不能误报 nil
	ctx2, cancel2 := context.WithTimeout(ctx, 30*time.Millisecond)
	defer cancel2()
	err2 := eng.Close(ctx2)
	if !errors.Is(err2, context.DeadlineExceeded) {
		t.Fatalf("second close err = %v, want DeadlineExceeded", err2)
	}

	// 释放阻塞后，Close 必须成功返回 nil
	doRelease()
	ctx3, cancel3 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel3()
	if err3 := eng.Close(ctx3); err3 != nil {
		t.Fatalf("final close err = %v, want nil", err3)
	}
	_ = rec
}
