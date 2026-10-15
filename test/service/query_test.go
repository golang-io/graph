package service_test

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/durable"
	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/service"
	"github.com/golang-io/graph/test"
	"github.com/golang-io/graph/test/engine/enginetest"
)

// waitTerminal 轮询 rt.Engines.Snapshot 直到 Task 进入终态。
func waitTerminal(t *testing.T, rt *service.Runtime, id string) engine.TaskSnapshot {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, err := rt.Engines.Snapshot(context.Background(), id)
		if err == nil && graph.IsTerminal(snap.Task.Status) {
			return snap
		}
		if time.Now().After(deadline) {
			t.Fatalf("task %s not finished: %+v, err %v", id, snap.Task, err)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// 已结束的 Task 直接读执行表：产生它的 immediate 引擎关闭后，另一个只注册了 durable 的 Runtime
// 仍能通过 Snapshot / Watch 查到终态结果（转发给引擎时会因 immediate 未注册而返回 ErrNotFound）。
func TestEndedTaskReadFromTable(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "graph.db")
	a, err := service.Start(ctx, service.Options{DB: test.MustOpen(t, path), Builders: []engine.Builder{immediate.Build}})
	if err != nil {
		t.Fatal(err)
	}
	rec, err := a.Start(ctx, engine.StartRequest{GraphID: "q", Graph: versionGraph("one"), Engine: engine.KindImmediate})
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, a, rec.TaskID)
	// immediate 关闭时等待处理循环结束，终态结果已写入执行表。
	if err := a.Close(ctx); err != nil {
		t.Fatal(err)
	}

	b, err := service.Start(ctx, service.Options{DB: test.MustOpen(t, path), Builders: []engine.Builder{durable.Build}})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(ctx)
	snap, err := b.Engines.Snapshot(ctx, rec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Task.Status != graph.SUCCEEDED || snap.Task.EngineKind != engine.KindImmediate || snap.Task.Output["answer"] != "one" || len(snap.Runs) == 0 {
		t.Fatalf("snapshot = %+v", snap.Task)
	}

	stream, err := b.Engines.Watch(ctx, rec.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	ev, err := stream.Recv()
	if err != nil || ev.Snapshot == nil || ev.Snapshot.Task.Status != graph.SUCCEEDED {
		t.Fatalf("first watch event = %+v, err %v", ev, err)
	}
	if _, err := stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatalf("second recv err = %v, want EOF", err)
	}

	// 控制动作仍需所属引擎：immediate 未注册时返回 ErrNotFound。
	if _, err := b.Engines.Cancel(ctx, rec.TaskID, "x"); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("cancel err = %v, want ErrNotFound", err)
	}
}

// 执行中的 immediate Task 尚未写入执行表，查询转发给引擎（读内存）；结束后改为读表。
func TestLiveTaskForwardedToEngine(t *testing.T) {
	ctx := context.Background()
	blocking := enginetest.FuncNode{NodeSpec: enginetest.OpSpec().Spec(), Exec: func(ctx context.Context, _ node.NodeInput) (node.NodeOutput, error) {
		<-ctx.Done()
		return node.NodeOutput{}, ctx.Err()
	}}
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))
	rt, err := service.Start(ctx, service.Options{DB: db, Nodes: []node.Node{blocking}, Builders: []engine.Builder{immediate.Build}})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)
	g := enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{}),
		enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("a", "finish"))
	rec, err := rt.Start(ctx, engine.StartRequest{GraphID: "live", Graph: g, Engine: engine.KindImmediate})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := rt.Engines.Snapshot(ctx, rec.TaskID)
	if err != nil || graph.IsTerminal(snap.Task.Status) {
		t.Fatalf("live snapshot = %+v, err %v", snap.Task, err)
	}
	if _, err := engine.LoadTask(db, rec.TaskID, false); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("live immediate task in table: %v", err)
	}
	if _, err := rt.Engines.Cancel(ctx, rec.TaskID, "stop"); err != nil {
		t.Fatal(err)
	}
	if s := waitTerminal(t, rt, rec.TaskID); s.Task.Status != graph.CANCELED {
		t.Fatalf("status = %s", s.Task.Status)
	}
}

// TestCrossEnginePagination 验证跨引擎分页游标前置解析：
//   - 当 After 是仅在内存中的 immediate 任务时，service 先将其解析为 (AfterAt, After) 再传给各引擎，
//     durable 正确返回排在它之后的旧任务，绝不静默退回第一页；
//   - 当 After 在所有引擎与执行表中均不存在时，返回 ErrNotFound。
func TestCrossEnginePagination(t *testing.T) {
	ctx := context.Background()
	started, release := make(chan struct{}, 1), make(chan struct{})
	blocking := enginetest.FuncNode{NodeSpec: enginetest.OpSpec().Spec(), Exec: func(ctx context.Context, _ node.NodeInput) (node.NodeOutput, error) {
		started <- struct{}{}
		select {
		case <-release:
			return node.NodeOutput{}, nil
		case <-ctx.Done():
			return node.NodeOutput{}, ctx.Err()
		}
	}}
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))
	rt, err := service.Start(ctx, service.Options{
		DB:       db,
		Nodes:    []node.Node{blocking},
		Builders: []engine.Builder{immediate.Build, durable.Build},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close(ctx)

	// 1. 先创建旧的 durable 任务并等待完成。
	oldRec, err := rt.Start(ctx, engine.StartRequest{GraphID: "pag", Graph: versionGraph("old"), Engine: engine.KindDurable})
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, rt, oldRec.TaskID)
	time.Sleep(10 * time.Millisecond)

	// 2. 创建运行中的 immediate 任务（仅在内存中，尚未写入 tb_graph_task）。
	blockG := enginetest.G([]graph.Node{
		enginetest.N("a", enginetest.OpType, enginetest.OpInput{}),
		enginetest.N("finish", builtin.End, nil),
	}, enginetest.E("a", "finish"))
	midRec, err := rt.Start(ctx, engine.StartRequest{GraphID: "pag", Graph: blockG, Engine: engine.KindImmediate})
	if err != nil {
		t.Fatal(err)
	}
	<-started
	if _, err := engine.LoadTask(db, midRec.TaskID, false); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("live immediate task should not be in db yet: %v", err)
	}
	time.Sleep(10 * time.Millisecond)

	// 3. 创建新的 durable 任务并等待完成。
	newRec, err := rt.Start(ctx, engine.StartRequest{GraphID: "pag", Graph: versionGraph("new"), Engine: engine.KindDurable})
	if err != nil {
		t.Fatal(err)
	}
	waitTerminal(t, rt, newRec.TaskID)

	// 按每页 1 条依次翻页：newDurable -> midImmediate -> oldDurable -> 空。
	wantOrder := []string{newRec.TaskID, midRec.TaskID, oldRec.TaskID}
	after := ""
	for i, wantID := range wantOrder {
		page, err := rt.ListTasks(ctx, engine.ListQuery{GraphID: "pag", Limit: 1, After: after})
		if err != nil {
			t.Fatalf("page %d (after=%q) err = %v", i+1, after, err)
		}
		if len(page) != 1 || page[0].TaskID != wantID {
			t.Fatalf("page %d (after=%q) = %+v, want [%s]", i+1, after, page, wantID)
		}
		after = page[0].TaskID
	}
	lastPage, err := rt.ListTasks(ctx, engine.ListQuery{GraphID: "pag", Limit: 1, After: after})
	if err != nil || len(lastPage) != 0 {
		t.Fatalf("page 4 (after=%q) = %+v, err %v; want empty", after, lastPage, err)
	}

	// 不存在的游标在 service 与各引擎独立调用时均返回 ErrNotFound，不退回第一页。
	if _, err := rt.ListTasks(ctx, engine.ListQuery{After: "missing-cursor"}); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("Runtime.ListTasks with missing cursor err = %v, want ErrNotFound", err)
	}
	for _, kind := range rt.Engines.Kinds() {
		eng, _ := rt.Engines.Engine(kind)
		if _, err := eng.List(ctx, engine.ListQuery{After: "missing-cursor"}); !errors.Is(err, engine.ErrNotFound) {
			t.Fatalf("%s.List with missing cursor err = %v, want ErrNotFound", kind, err)
		}
	}

	close(release)
	waitTerminal(t, rt, midRec.TaskID)
}
