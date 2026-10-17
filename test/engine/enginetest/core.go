package enginetest

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
)

type finishController struct{ FuncNode }

func (n finishController) Control(ctx context.Context, _ node.ControlInput) (node.NodeOutput, error) {
	return n.Exec(ctx, node.NodeInput{})
}

// runCore 验证提前结束、超时和恢复目标在各引擎中使用相同语义。
func runCore(t *testing.T, open OpenDB, build engine.Builder) {
	for _, name := range []string{"early", "terminal", "controller", "fork", "subgraph"} {
		t.Run("Finish/"+name, func(t *testing.T) {
			eps := node.DefaultEndpoints()
			if name == "terminal" {
				eps.Outputs = nil
			}
			n := FuncNode{NodeSpec: node.WithParams(node.NodeSpec{ID: "finish_now", Name: "finish now", Endpoints: eps}, struct{}{}),
				Exec: func(context.Context, node.NodeInput) (node.NodeOutput, error) {
					return node.Finish(map[string]any{"Answer": int64(42)}), nil
				}}
			var impl node.Node = n
			if name == "controller" {
				impl = finishController{n}
			}
			e := setup(t, open, build, impl)
			g := G([]graph.Node{N("early", "finish_now", nil), end(nil)}, E("early", "finish"))
			switch name {
			case "terminal":
				g = G([]graph.Node{N("early", "finish_now", nil)})
			case "fork":
				g = G([]graph.Node{N("fork", builtin.Parallel, nil), N("early", "finish_now", nil), N("unreached", builtin.End, nil), end(nil)}, E("fork", "early"), E("fork", "finish"), E("early", "unreached"))
			case "subgraph":
				e.graphs["child"] = g
				g = G([]graph.Node{N("call", builtin.SubGraph, builtin.SubGraphInput{GraphID: "child"}), end(refs{"Child": R("call", builtin.SubGraphOutputField)})}, E("call", "finish"))
			}
			s := e.done(e.start(g, nil, engine.TaskPolicy{}), graph.SUCCEEDED)
			want := map[string]any{"Answer": 42}
			if name == "subgraph" {
				want = map[string]any{"Child": want}
			}
			eq(t, s.Task.Output, want)
			if name != "fork" && name != "subgraph" && (len(s.Runs) != 1 || s.Runs[0].NodeID != "early") {
				t.Fatalf("Finish did not end the current Run: %+v", s.Runs)
			}
		})
	}
	for _, name := range []string{"late success", "timeout error", "timely success"} {
		t.Run("Timeout/"+name, func(t *testing.T) {
			var calls atomic.Int32
			n := FuncNode{NodeSpec: node.WithParams(node.NodeSpec{ID: "timed", Name: "timed", Endpoints: node.DefaultEndpoints()}, OpInput{}),
				Exec: func(ctx context.Context, _ node.NodeInput) (node.NodeOutput, error) {
					calls.Add(1)
					if name != "timely success" {
						<-ctx.Done()
						if name == "timeout error" {
							return node.NodeOutput{}, ctx.Err()
						}
					}
					return node.Continue(map[string]any{"Value": "value"}, ""), nil
				}}
			e := setup(t, open, build, n)
			g := G([]graph.Node{N("timed", "timed", OpInput{Set: map[string]any{"Value": nil}}), end(refs{"Value": R("timed", "Value")})}, E("timed", "finish"))
			policy := engine.TaskPolicy{NodeTimeoutMS: 10, MaxAttempts: 2, RetryDelayMS: 1}
			if name == "timely success" {
				policy.NodeTimeoutMS = 1000
				s := e.done(e.start(g, nil, policy), graph.SUCCEEDED)
				eq(t, s.Task.Output, map[string]any{"Value": "value"})
				if calls.Load() != 1 {
					t.Fatalf("successful call retried: %d", calls.Load())
				}
				return
			}
			s := e.done(e.start(g, nil, policy), graph.FAILED)
			if calls.Load() != 2 || len(s.Task.Output) != 0 || !strings.Contains(s.Task.Error, context.DeadlineExceeded.Error()) {
				t.Fatalf("timeout did not discard output and retry: calls=%d task=%+v", calls.Load(), s.Task)
			}
		})
	}
	for _, name := range []string{"breakpoint", "paused"} {
		t.Run("ResumeTarget/"+name, func(t *testing.T) {
			e := setup(t, open, build)
			id := e.start(G([]graph.Node{end(nil)}), nil, engine.TaskPolicy{Debug: &engine.DebugOptions{Breakpoints: []string{"finish"}}})
			e.waiting(id)
			if name == "paused" {
				if _, err := e.signal(id, engine.Signal{Kind: engine.SignalPause}); err != nil {
					t.Fatal(err)
				}
			}
			before := e.waiting(id)
			for _, debug := range []*engine.DebugOptions{nil, {}} {
				if _, err := e.resume(id, engine.ResumeRequest{TargetRunID: "missing", Debug: debug}); !errors.Is(err, engine.ErrConflict) {
					t.Fatalf("wrong target: %v, want ErrConflict", err)
				}
				after, err := e.eng.Snapshot(e.ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(before, after) {
					t.Fatal("rejected resume changed task or runs")
				}
			}
			if len(before.Runs) != 1 {
				t.Fatalf("expected one debug gate: %+v", before.Runs)
			}
			if _, err := e.resume(id, engine.ResumeRequest{TargetRunID: before.Runs[0].RunID}); err != nil {
				t.Fatal(err)
			}
			e.done(id, graph.SUCCEEDED)
		})
	}
}
