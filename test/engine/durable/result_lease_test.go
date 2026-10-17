package durable_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/durable"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/test"
	"github.com/golang-io/graph/test/engine/enginetest"
	"gorm.io/gorm"
)

func TestResultSaveLeaseBoundary(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := "saving keeps lease"
		if committed {
			name = "waiting for result acknowledgement keeps lease"
		}
		t.Run(name, func(t *testing.T) {
			db := test.TempDB(t)
			blocked, release, renewed, duplicate := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var held atomic.Bool
			var beats atomic.Int32
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			hold := func(tx *gorm.DB) {
				if tx.Error != nil || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "tb_graph_run" || len(tx.Statement.Selects) != 1 || tx.Statement.Selects[0] != "Result" || !held.CompareAndSwap(false, true) {
					return
				}
				close(blocked)
				select {
				case <-release:
				case <-ctx.Done():
				}
			}
			var err error
			if committed {
				err = db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("test:hold_result", hold)
			} else {
				err = db.Callback().Update().Before("gorm:begin_transaction").Register("test:hold_result", hold)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err = db.Callback().Update().After("gorm:commit_or_rollback_transaction").Register("test:observe_renewal", func(tx *gorm.DB) {
				values, ok := tx.Statement.Dest.(map[string]any)
				if !ok || len(values) != 1 || values["LeaseUntil"] == nil || tx.Error != nil || tx.RowsAffected != 1 || !held.Load() {
					return
				}
				if beats.Add(1) == 4 {
					close(renewed)
				}
			}); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			work := enginetest.FuncNode{NodeSpec: node.WithParams(node.NodeSpec{ID: "work", Name: "work", Endpoints: node.DefaultEndpoints()}, struct{}{}),
				Exec: func(context.Context, node.NodeInput) (node.NodeOutput, error) {
					if calls.Add(1) == 2 {
						close(duplicate)
					}
					return node.Continue(nil, ""), nil
				},
			}
			reg, err := enginetest.Registry(work)
			if err != nil {
				t.Fatal(err)
			}
			e, err := durable.With(durable.Options{Workers: 2, LeaseDuration: 300 * time.Millisecond, PollInterval: 5 * time.Millisecond})(ctx, engine.EngineConfig{DB: db, Nodes: reg})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				unblock()
				closeCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
				defer done()
				if err := e.Close(closeCtx); err != nil {
					t.Error(err)
				}
			}()
			g := enginetest.G([]graph.Node{enginetest.N("work", "work", nil), enginetest.N("finish", builtin.End, nil)}, enginetest.E("work", "finish"))
			rec, err := e.Start(ctx, engine.StartRequest{Graph: g})
			if err != nil {
				t.Fatal(err)
			}
			select {
			case <-blocked:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			// 四次成功续租跨越一个租约时长；收到保存回执之前必须一直持有归属。
			select {
			case <-renewed:
			case <-duplicate:
				t.Fatal("activity reexecuted while saving result")
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			unblock()
			waitStatus(t, e, rec.TaskID, graph.SUCCEEDED)
			if calls.Load() != 1 {
				t.Fatalf("activity called %d times, want 1", calls.Load())
			}
		})
	}
}

type errorObserver struct {
	mu   sync.Mutex
	errs []error
}

func (o *errorObserver) TaskChanged(string) {}

func (o *errorObserver) EngineError(err error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.errs = append(o.errs, err)
}

func TestResultSaveInterruptedOnCancel(t *testing.T) {
	db := test.TempDB(t)
	blocked := make(chan struct{})
	var held atomic.Bool
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := db.Callback().Update().Before("gorm:begin_transaction").Register("test:hold_save_result", func(tx *gorm.DB) {
		if tx.Error != nil || tx.Statement.Schema == nil || tx.Statement.Schema.Table != "tb_graph_run" || len(tx.Statement.Selects) != 1 || tx.Statement.Selects[0] != "Result" || !held.CompareAndSwap(false, true) {
			return
		}
		close(blocked)
		select {
		case <-tx.Statement.Context.Done():
		case <-ctx.Done():
		}
	}); err != nil {
		t.Fatal(err)
	}

	work := enginetest.FuncNode{NodeSpec: node.WithParams(node.NodeSpec{ID: "work", Name: "work", Endpoints: node.DefaultEndpoints()}, struct{}{}),
		Exec: func(context.Context, node.NodeInput) (node.NodeOutput, error) {
			return node.Continue(nil, ""), nil
		},
	}
	reg, err := enginetest.Registry(work)
	if err != nil {
		t.Fatal(err)
	}
	obs := &errorObserver{}
	e, err := durable.With(durable.Options{Workers: 1, LeaseDuration: time.Second, PollInterval: 5 * time.Millisecond})(ctx, engine.EngineConfig{DB: db, Nodes: reg, Observer: obs})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		closeCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = e.Close(closeCtx)
	}()

	g := enginetest.G([]graph.Node{enginetest.N("work", "work", nil), enginetest.N("finish", builtin.End, nil)}, enginetest.E("work", "finish"))
	rec, err := e.Start(ctx, engine.StartRequest{Graph: g})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-blocked:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	if _, err := e.Signal(ctx, rec.TaskID, engine.Signal{Kind: engine.SignalCancel, Reason: "canceled during save"}); err != nil {
		t.Fatal(err)
	}
	waitStatus(t, e, rec.TaskID, graph.CANCELED)

	obs.mu.Lock()
	defer obs.mu.Unlock()
	for _, err := range obs.errs {
		t.Errorf("unexpected engine error: %v", err)
	}
}
