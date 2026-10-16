package service

import (
	"reflect"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
)

// testGraph 返回一张非空图，用来确认图只随完整快照发出、增量合并后保留。
func testGraph() graph.Graph {
	return graph.Graph{Nodes: []graph.Node{{ID: "a"}}, Edges: []graph.Edge{}}
}

// TestDiffSnapshotDelta 验证增量只带变化的 Run，合并后与完整快照一致。
func TestDiffSnapshotDelta(t *testing.T) {
	prev := engine.TaskSnapshot{
		Task: engine.Task{TaskID: "t1", Status: graph.RUNNING, Graph: testGraph()},
		Runs: []engine.Run{{RunID: "r1", Status: graph.SUCCEEDED, NodeID: "a"}, {RunID: "r2", Status: graph.PENDING, NodeID: "b"}},
	}
	next := prev
	next.Runs = []engine.Run{
		{RunID: "r1", Status: graph.SUCCEEDED, NodeID: "a"},
		{RunID: "r2", Status: graph.RUNNING, NodeID: "b"},
		{RunID: "r3", Status: graph.PENDING, NodeID: "c"},
	}
	next.Task.Version = 2

	ev := DiffSnapshot(prev, next)
	if ev.Snapshot != nil {
		t.Fatalf("want delta, got full snapshot")
	}
	if ev.Task == nil || len(ev.Task.Graph.Nodes) != 0 {
		t.Fatalf("delta task must be present without graph: %+v", ev.Task)
	}
	if len(ev.Runs) != 2 {
		t.Fatalf("runs = %+v", ev.Runs)
	}
	got := ApplyWatch(&prev, ev)
	if len(got.Task.Graph.Nodes) != 1 || got.Task.Version != 2 || len(got.Runs) != 3 || got.Runs[1].Status != graph.RUNNING {
		t.Fatalf("applied task=%+v runs=%d", got.Task, len(got.Runs))
	}
}

func TestDiffSnapshotDeltaPreservesSource(t *testing.T) {
	next := engine.TaskSnapshot{Task: engine.Task{
		TaskID: "t1", Version: 2, Graph: testGraph(),
		Input:  map[string]any{"obj": map[string]any{"x": int64(1)}},
		Output: map[string]any{"items": []any{int64(2)}},
		Policy: engine.TaskPolicy{Debug: &engine.DebugOptions{
			Breakpoints: []string{"a"},
			Injects: map[string]engine.DebugInject{
				"a": {Vars: map[string]any{"items": []any{int64(3)}}},
			},
		}},
	}}
	before := next.Task.Clone()
	ev := DiffSnapshot(engine.TaskSnapshot{Task: engine.Task{TaskID: "t1", Version: 1}}, next)
	if ev.Task == nil || len(ev.Task.Graph.Nodes) != 0 {
		t.Fatalf("delta must contain task without graph: %+v", ev.Task)
	}
	ev.Task.Input["obj"].(map[string]any)["x"] = int64(10)
	ev.Task.Output["items"].([]any)[0] = int64(20)
	ev.Task.Policy.Debug.Breakpoints[0] = "changed"
	ev.Task.Policy.Debug.Injects["a"].Vars["items"].([]any)[0] = int64(30)
	if !reflect.DeepEqual(&next.Task, before) {
		t.Fatalf("delta modified source snapshot: got %+v, want %+v", next.Task, before)
	}
}

// TestDiffSnapshotFull 验证首次推送为完整快照。
func TestDiffSnapshotFull(t *testing.T) {
	next := engine.TaskSnapshot{
		Task: engine.Task{TaskID: "t1", Status: graph.SUCCEEDED, Graph: testGraph()},
		Runs: []engine.Run{{RunID: "r1", Status: graph.SUCCEEDED, NodeID: "a"}},
	}
	first := DiffSnapshot(engine.TaskSnapshot{}, next)
	if first.Snapshot == nil || len(first.Snapshot.Task.Graph.Nodes) != 1 {
		t.Fatalf("first = %+v", first)
	}
	empty := DiffSnapshot(engine.TaskSnapshot{}, engine.TaskSnapshot{
		Task: engine.Task{TaskID: "t2", Status: graph.WAITING},
	})
	if empty.Snapshot == nil || empty.Snapshot.Runs == nil {
		t.Fatalf("empty runs must be non-nil: %+v", empty.Snapshot)
	}
}

func TestApplyWatchRemoveAndAdd(t *testing.T) {
	cur := engine.TaskSnapshot{
		Task: engine.Task{TaskID: "t1", Status: graph.RUNNING},
		Runs: []engine.Run{{RunID: "r1", Status: graph.SUCCEEDED}, {RunID: "r2", Status: graph.PENDING}},
	}
	ev := WatchEvent{RemovedRunIDs: []string{"r2"}, Runs: []engine.Run{{RunID: "r3", Status: graph.PENDING}}}
	got := ApplyWatch(&cur, ev)
	if len(got.Runs) != 2 || got.Runs[0].RunID != "r1" || got.Runs[1].RunID != "r3" {
		t.Fatalf("runs = %+v", got.Runs)
	}
}
