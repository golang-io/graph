package enginetest

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/google/jsonschema-go/jsonschema"
)

func runOwnership(t *testing.T, open OpenDB, build engine.Builder) {
	t.Run("ControllerInjectStable", func(t *testing.T) { controllerInjectStable(t, open, build) })
	for _, mode := range []string{node.JoinAny, node.JoinAll} {
		t.Run("ForkDirectToJoin/"+mode, func(t *testing.T) {
			e := setup(t, open, build)
			left, right := E("fork", "join"), E("fork", "join")
			left.ID, right.ID = "left", "right"
			g := G([]graph.Node{N("fork", builtin.Parallel, nil), N("join", builtin.Merge, builtin.MergeInput{Mode: mode}), end(nil)},
				left, right, E("join", "finish"))
			s := e.done(e.start(g, nil, engine.TaskPolicy{}), graph.SUCCEEDED)
			var ends int
			for _, r := range s.Runs {
				if r.NodeID == "finish" && r.Status == graph.SUCCEEDED {
					ends++
				}
			}
			if ends != 1 {
				t.Fatalf("finish visits = %d, want 1", ends)
			}
		})
	}
	for _, mode := range []string{"running", "retry", "subgraph"} {
		t.Run("AnyJoin/"+mode, func(t *testing.T) { anyJoinStable(t, open, build, mode) })
	}
	t.Run("NestedSchemaSnapshot", func(t *testing.T) {
		e := setup(t, open, build)
		g := GI([]string{"obj"}, []graph.Node{end(refs{"obj": R(graph.InputSource, "obj")})})
		g.InputSchema.Properties["obj"] = graph.RunInputSchema(map[string]*jsonschema.Schema{"x": {Type: "integer"}})
		id := e.start(g, map[string]any{"obj": map[string]any{"x": 1}}, engine.TaskPolicy{Debug: &engine.DebugOptions{Breakpoints: []string{"finish"}}})
		first := e.waiting(id)
		first.Task.Graph.InputSchema.Properties["obj"].Required[0] = "changed"
		second, err := e.eng.Snapshot(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if second.Task.Graph.InputSchema.Properties["obj"].Required[0] != "x" || g.InputSchema.Properties["obj"].Required[0] != "x" {
			t.Fatal("snapshot changed engine or Start graph schema")
		}
	})
}

func waitOwnershipSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(5 * time.Second):
		t.Fatal("execution barrier timed out")
	}
}

func controllerInjectStable(t *testing.T, open OpenDB, build engine.Builder) {
	for _, name := range []string{"list contents", "list length", "chunk", "body", "remove", "equivalent", "breakpoints"} {
		t.Run(name, func(t *testing.T) {
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			var calls atomic.Int32
			echo := FuncNode{NodeSpec: node.WithParams(node.NodeSpec{ID: "echo", Name: "echo", Endpoints: node.DefaultEndpoints()}, OpInput{}),
				Exec: func(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
					if calls.Add(1) == 1 {
						close(entered)
						select {
						case <-release:
						case <-ctx.Done():
							return node.NodeOutput{}, ctx.Err()
						}
					}
					return node.Continue(map[string]any{"Value": in.Vars["item"]}, ""), nil
				},
			}
			e := setup(t, open, build, echo)
			body := GI([]string{builtin.MapItemField}, []graph.Node{
				In(N("echo", "echo", OpInput{Set: map[string]any{"Value": nil}}), refs{"item": R(graph.InputSource, builtin.MapItemField)}),
				In(N("body_end", builtin.End, nil), refs{"Value": R("echo", "Value")}),
			}, E("echo", "body_end"))
			g := GI([]string{"items"}, []graph.Node{
				In(N("map", builtin.Map, builtin.MapInput{Body: &body, MaxParallel: 1}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
				end(refs{"Results": R("map", builtin.MapResultsField)}),
			}, E("map", "finish"))
			policy := engine.TaskPolicy{}
			if name == "remove" || name == "equivalent" {
				policy.Debug = &engine.DebugOptions{Injects: map[string]engine.DebugInject{"map": {Vars: map[string]any{builtin.MapListField: []any{1, 2}}}}}
			}
			id := e.start(g, map[string]any{"items": []any{1, 2}}, policy)
			waitOwnershipSignal(t, entered)
			debug := &engine.DebugOptions{}
			var vars map[string]any
			switch name {
			case "list contents":
				vars = map[string]any{builtin.MapListField: []any{100, 200}}
			case "list length":
				vars = map[string]any{builtin.MapListField: []any{100}}
			case "chunk":
				vars = map[string]any{"Chunk": 2}
			case "body":
				vars = map[string]any{"Body": body}
			case "equivalent":
				vars = map[string]any{builtin.MapListField: []any{1, 2}}
			case "breakpoints":
				debug.Breakpoints = []string{"_start"}
			}
			if vars != nil {
				debug.Injects = map[string]engine.DebugInject{"map": {Vars: vars}}
			}
			before, err := e.eng.Snapshot(e.ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			_, err = e.resume(id, engine.ResumeRequest{Debug: debug})
			if name == "equivalent" || name == "breakpoints" {
				if err != nil {
					t.Fatal(err)
				}
			} else {
				if !errors.Is(err, engine.ErrConflict) {
					t.Fatalf("update error = %v, want conflict", err)
				}
				after, readErr := e.eng.Snapshot(e.ctx, id)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if after.Task.Version != before.Task.Version || !reflect.DeepEqual(after.Task.Policy.Debug, before.Task.Policy.Debug) {
					t.Fatal("rejected update modified task")
				}
			}
			unblock()
			s := e.done(id, graph.SUCCEEDED)
			eq(t, s.Task.Output, map[string]any{"Results": []any{map[string]any{"Value": 1}, map[string]any{"Value": 2}}})
		})
	}
}

type anyVisitParams struct{}

func (anyVisitParams) JoinMode() string { return node.JoinAny }

type anyVisitController struct {
	body  graph.Graph
	calls *atomic.Int32
}

func (anyVisitController) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{ID: "async_join", Name: "async controller", Endpoints: node.DefaultEndpoints()}, anyVisitParams{})
}

func (c anyVisitController) Control(_ context.Context, in node.ControlInput) (node.NodeOutput, error) {
	if in.Event.Kind == node.EventStart {
		c.calls.Add(1)
		return node.SpawnChildren([]node.Spawn{{Slot: "child", Graph: &c.body}}, json.RawMessage(`{}`)), nil
	}
	return node.Continue(nil, ""), nil
}

func anyJoinStable(t *testing.T, open OpenDB, build engine.Builder, mode string) {
	retry := mode == "retry"
	joined, releaseJoin, releaseHub := make(chan struct{}, 3), make(chan struct{}), make(chan struct{})
	var hubOnce, joinOnce sync.Once
	unblockHub := func() { hubOnce.Do(func() { close(releaseHub) }) }
	defer unblockHub()
	unblockJoin := func() { joinOnce.Do(func() { close(releaseJoin) }) }
	defer unblockJoin()
	var calls atomic.Int32
	var join node.Node = FuncNode{NodeSpec: node.WithParams(node.NodeSpec{ID: "async_join", Name: "async join", Endpoints: node.DefaultEndpoints()}, anyVisitParams{}),
		Exec: func(ctx context.Context, in node.NodeInput) (node.NodeOutput, error) {
			calls.Add(1)
			joined <- struct{}{}
			if retry && in.Key.Attempt == 1 {
				return node.NodeOutput{}, errors.New("retry once")
			}
			select {
			case <-releaseJoin:
				return node.Continue(nil, ""), nil
			case <-ctx.Done():
				return node.NodeOutput{}, ctx.Err()
			}
		},
	}
	child := FuncNode{NodeSpec: node.WithParams(node.NodeSpec{ID: "child_hold", Name: "child hold", Endpoints: node.DefaultEndpoints()}, struct{}{}),
		Exec: func(ctx context.Context, _ node.NodeInput) (node.NodeOutput, error) {
			joined <- struct{}{}
			select {
			case <-releaseJoin:
				return node.Continue(nil, ""), nil
			case <-ctx.Done():
				return node.NodeOutput{}, ctx.Err()
			}
		},
	}
	if mode == "subgraph" {
		join = anyVisitController{calls: &calls, body: G([]graph.Node{N("child", "child_hold", nil), N("child_end", builtin.End, nil)}, E("child", "child_end"))}
	}
	hub := FuncNode{NodeSpec: node.WithParams(node.NodeSpec{ID: "hub", Name: "hub", Endpoints: node.DefaultEndpoints()}, struct{}{}),
		Exec: func(ctx context.Context, _ node.NodeInput) (node.NodeOutput, error) {
			select {
			case <-releaseHub:
				return node.Continue(nil, ""), nil
			case <-ctx.Done():
				return node.NodeOutput{}, ctx.Err()
			}
		},
	}
	e := setup(t, open, build, join, hub, child)
	g := G([]graph.Node{N("fork", builtin.Parallel, nil), op("fast", OpInput{}), N("hub", "hub", nil),
		N("join", "async_join", nil), end(nil), N("tail", builtin.End, nil)},
		E("fork", "fast"), E("fork", "hub"), E("fast", "join"), E("hub", "join"), E("hub", "tail"), E("join", "finish"))
	policy := engine.TaskPolicy{ExecutionMode: graph.ExecParallel, MaxParallel: 4}
	if retry {
		policy.MaxAttempts, policy.RetryDelayMS = 2, 100
	}
	id := e.start(g, nil, policy)
	waitOwnershipSignal(t, joined)
	if retry {
		waitOwnershipRun(t, e, id, func(s engine.TaskSnapshot) bool {
			for _, r := range s.Runs {
				if r.NodeID == "join" && r.Attempt == 2 {
					return true
				}
			}
			return false
		})
	}
	unblockHub()
	s := waitOwnershipRun(t, e, id, func(s engine.TaskSnapshot) bool {
		for _, r := range s.Runs {
			if r.NodeID == "hub" && r.Status == graph.SUCCEEDED {
				return true
			}
		}
		return false
	})
	for _, r := range s.Runs {
		if r.NodeID == "join" && r.Status == graph.SKIPPED {
			t.Fatal("late arrival canceled first join visit")
		}
	}
	unblockJoin()
	s = e.done(id, graph.SUCCEEDED)
	want := int32(1)
	if retry {
		want = 2
	}
	if calls.Load() != want {
		t.Fatalf("join called %d times, want %d", calls.Load(), want)
	}
	var tailDone bool
	for _, r := range s.Runs {
		tailDone = tailDone || (r.NodeID == "tail" && r.Status == graph.SUCCEEDED)
	}
	if !tailDone {
		t.Fatal("hub tail path did not complete")
	}
}

func waitOwnershipRun(t *testing.T, e *env, id string, ready func(engine.TaskSnapshot) bool) engine.TaskSnapshot {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		s, err := e.eng.Snapshot(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if ready(s) {
			return s
		}
		select {
		case <-tick.C:
		case <-deadline.C:
			t.Fatal("run transition timed out")
		}
	}
}
