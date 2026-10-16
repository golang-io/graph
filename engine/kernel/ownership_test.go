package kernel

import (
	"context"
	"encoding/json"
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
