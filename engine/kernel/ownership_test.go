package kernel

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
)

func TestIgnoredEventsDoNotAllocateWorkingState(t *testing.T) {
	reg, err := node.NewRegistry(builtin.Nodes()...)
	if err != nil {
		t.Fatal(err)
	}
	task := &engine.Task{TaskID: "t", Status: graph.WAITING}
	runs := []*engine.Run{{RunID: "r", TaskID: "t", Status: graph.WAITING, WaitReason: engine.WaitSubgraph, Seq: 1, Attempt: 1, State: json.RawMessage(`{"large":"` + strings.Repeat("x", 4096) + `"}`)}}
	env := Env{Now: time.Unix(1, 0), Awaiting: map[string]map[string]string{"r": {"0": "child"}}}
	for _, ev := range []Event{Started{}, ActivityDone{RunID: "obsolete"}, ActivityDone{RunID: "r", Seq: 1, Attempt: 1}, ChildDone{RunID: "r", Slot: "0", ChildTaskID: "obsolete"}} {
		allocs := testing.AllocsPerRun(20, func() {
			ch, cmds, err := Apply(reg, task, runs, ev, env)
			if err != nil || !ch.Empty() || len(cmds) != 0 {
				t.Fatalf("ignored event: %+v %v %v", ch, cmds, err)
			}
		})
		if allocs != 0 {
			t.Fatalf("%T allocated working state: %v", ev, allocs)
		}
	}
}

func TestResumeDoesNotMutateOriginalDebugStep(t *testing.T) {
	reg, err := node.NewRegistry(builtin.Nodes()...)
	if err != nil {
		t.Fatal(err)
	}
	task := &engine.Task{TaskID: "t", Status: graph.RUNNING, Paused: true, Policy: engine.TaskPolicy{Debug: &engine.DebugOptions{Step: true}}}
	ch, _, err := Apply(reg, task, nil, Resume{Request: engine.ResumeRequest{DebugAction: engine.DebugActionContinue}}, Env{Now: time.Unix(1, 0)})
	if err != nil {
		t.Fatal(err)
	}
	if !task.Policy.Debug.Step || !task.Paused || ch.Task.Policy.Debug.Step || ch.Task.Paused {
		t.Fatal("resume modified original task or failed to continue")
	}
}

type abortingController struct{}

func (abortingController) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{ID: "aborting", Name: "aborting", Endpoints: node.DefaultEndpoints()}, struct{}{})
}

func (abortingController) Control(_ context.Context, in node.ControlInput) (node.NodeOutput, error) {
	in.State[0] = '['
	in.Vars["obj"].(map[string]any)["x"] = int64(99)
	panic("abort apply")
}

func TestControllerAbortDoesNotMutateInputs(t *testing.T) {
	reg, err := node.NewRegistry(abortingController{})
	if err != nil {
		t.Fatal(err)
	}
	task := &engine.Task{TaskID: "t", Status: graph.WAITING,
		Input: map[string]any{"obj": map[string]any{"x": int64(1)}},
		Graph: graph.Graph{Nodes: []graph.Node{{ID: "ctrl", Kind: "aborting", Inputs: map[string]graph.InputRef{"obj": {From: graph.InputSource, Field: "obj"}}}}},
	}
	run := &engine.Run{RunID: "r", TaskID: "t", NodeID: "ctrl", Seq: 1, Attempt: 1, Status: graph.WAITING, WaitReason: engine.WaitSubgraph, State: json.RawMessage(`{"value":1}`)}
	env := Env{Now: time.Unix(1, 0), Awaiting: map[string]map[string]string{"r": {"0": "child", "1": "other"}}}
	ch, cmds, err := Apply(reg, task, []*engine.Run{run}, ChildDone{RunID: "r", Slot: "0", ChildTaskID: "child", Status: graph.SUCCEEDED}, env)
	if err == nil || !ch.Empty() || len(cmds) != 0 {
		t.Fatalf("aborted Apply returned %v %+v", err, ch)
	}
	if string(run.State) != `{"value":1}` || task.Input["obj"].(map[string]any)["x"] != int64(1) || env.Awaiting["r"]["0"] != "child" {
		t.Fatal("aborted Apply modified input state")
	}
}

type controllerFunc func(node.ControlInput) (node.NodeOutput, error)

func (controllerFunc) Spec() node.NodeSpec {
	return node.WithParams(node.NodeSpec{ID: "controller", Name: "controller", Endpoints: node.DefaultEndpoints()}, struct{}{})
}

func (f controllerFunc) Control(_ context.Context, in node.ControlInput) (node.NodeOutput, error) {
	return f(in)
}

func TestChildPayloadRetryDoesNotMutateEvent(t *testing.T) {
	for _, mode := range []string{"need graph", "panic", "invalid output"} {
		t.Run(mode, func(t *testing.T) {
			reg, err := node.NewRegistry(controllerFunc(func(in node.ControlInput) (node.NodeOutput, error) {
				obj := in.Event.Payload["Items"].([]any)[0].(map[string]any)
				if obj["Value"] != int64(1) {
					t.Fatal("replayed event contains mutation from previous Apply")
				}
				obj["Value"] = int64(99)
				if mode == "panic" {
					panic("abort apply")
				}
				out := node.SpawnChildren([]node.Spawn{{GraphID: "next"}}, nil)
				if mode == "invalid output" {
					out.Set = map[string]any{"invalid": true}
				}
				return out, nil
			}))
			if err != nil {
				t.Fatal(err)
			}
			task := &engine.Task{TaskID: "t", Status: graph.WAITING, Graph: graph.Graph{Nodes: []graph.Node{{ID: "ctrl", Kind: "controller"}}}}
			run := &engine.Run{RunID: "r", TaskID: "t", NodeID: "ctrl", Seq: 1, Attempt: 1, Status: graph.WAITING, WaitReason: engine.WaitSubgraph}
			ev := ChildDone{RunID: "r", Slot: "0", ChildTaskID: "child", Status: graph.SUCCEEDED, Output: map[string]any{"Items": []any{map[string]any{"Value": int64(1)}}}}
			before := engine.CloneMap(ev.Output)
			env := Env{Now: time.Unix(1, 0), Awaiting: map[string]map[string]string{"r": {"0": "child"}}}
			for range 2 {
				ch, cmds, err := Apply(reg, task, []*engine.Run{run}, ev, env)
				if !ch.Empty() || len(cmds) != 0 {
					t.Fatalf("aborted Apply returned changes: %+v %v", ch, cmds)
				}
				if mode == "need graph" {
					if err != nil || !reflect.DeepEqual(ch.NeedGraphs, []string{"next"}) {
						t.Fatalf("missing graph: %+v %v", ch, err)
					}
				} else if err == nil {
					t.Fatal("expected Apply error")
				}
				if !reflect.DeepEqual(ev.Output, before) || env.Awaiting["r"]["0"] != "child" || run.WaitReason != engine.WaitSubgraph {
					t.Fatal("aborted Apply changed event or original waiting state")
				}
			}
		})
	}
}

func TestControllerReplacesState(t *testing.T) {
	child := graph.Graph{Nodes: []graph.Node{{ID: "start", Kind: builtin.Start}, {ID: "end", Kind: builtin.End}},
		Edges: []graph.Edge{{ID: "edge", From: "start.Out", To: "end.In"}}}
	for _, mode := range []string{"wait", "spawn"} {
		for _, next := range []string{"", `{"New":true}`} {
			t.Run(mode+"/"+next, func(t *testing.T) {
				nextState := json.RawMessage(next)
				reg, err := node.NewRegistry(append(builtin.Nodes(), controllerFunc(func(in node.ControlInput) (node.NodeOutput, error) {
					if string(in.State) != `{"Old":true}` {
						t.Fatalf("previous state not delivered: %s", in.State)
					}
					if mode == "spawn" {
						return node.SpawnChildren([]node.Spawn{{Slot: "2", Graph: &child}}, nextState), nil
					}
					return node.Wait(nextState), nil
				}))...)
				if err != nil {
					t.Fatal(err)
				}
				task := &engine.Task{TaskID: "t", Status: graph.WAITING, Graph: graph.Graph{Nodes: []graph.Node{{ID: "ctrl", Kind: "controller"}}}}
				run := &engine.Run{RunID: "r", TaskID: "t", NodeID: "ctrl", Seq: 1, Attempt: 1, Status: graph.WAITING, WaitReason: engine.WaitSubgraph, State: json.RawMessage(`{"Old":true}`)}
				env := Env{Now: time.Unix(1, 0), Awaiting: map[string]map[string]string{"r": {"0": "child", "1": "other"}}}
				ch, cmds, err := Apply(reg, task, []*engine.Run{run}, ChildDone{RunID: "r", Slot: "0", ChildTaskID: "child", Status: graph.SUCCEEDED}, env)
				if err != nil || len(ch.Runs) != 1 {
					t.Fatalf("Apply=%+v %v", ch, err)
				}
				if mode == "spawn" && len(cmds) != 1 {
					t.Fatalf("expected new child: %v", cmds)
				}
				if len(nextState) > 0 {
					nextState[0] = '['
				}
				if string(ch.Runs[0].State) != next || ch.Runs[0].WaitReason != engine.WaitSubgraph || string(run.State) != `{"Old":true}` {
					t.Fatalf("state was not replaced independently: %s (original %s)", ch.Runs[0].State, run.State)
				}
			})
		}
	}
}

func TestResumeRejectsNonDebugTarget(t *testing.T) {
	reg, err := node.NewRegistry(builtin.Nodes()...)
	if err != nil {
		t.Fatal(err)
	}
	task := &engine.Task{TaskID: "t", Status: graph.WAITING, Paused: true}
	run := &engine.Run{RunID: "r", TaskID: "t", Status: graph.WAITING, WaitReason: engine.WaitSubgraph}
	ch, cmds, err := Apply(reg, task, []*engine.Run{run}, Resume{Request: engine.ResumeRequest{TargetRunID: "r"}}, Env{Now: time.Unix(1, 0)})
	if !errors.Is(err, engine.ErrConflict) || !IsRejection(err) || !ch.Empty() || len(cmds) != 0 || !task.Paused {
		t.Fatalf("non-debug target was not rejected without changes: %+v %v %v", ch, cmds, err)
	}
}
