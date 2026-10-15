package graph_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
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
	"github.com/google/jsonschema-go/jsonschema"
)

// withStart 为测试图接上 start：g.Nodes[0] 是入口节点（见 enginetest.PrependStart）。
func withStart(g graph.Graph) graph.Graph { return enginetest.PrependStart(g) }

// ge 构造端点到端点的边：From 默认取来源节点的 Out 出端点，To 取目标节点的 In 入端点；
// 可选的 fromLocal 覆盖来源出端点本地名（如 switch 的 Case0、stop_error 的 Error）。
func ge(id, from, to string, fromLocal ...string) graph.Edge {
	fl := node.DefaultOutput
	if len(fromLocal) > 0 {
		fl = fromLocal[0]
	}
	return graph.Edge{ID: id, From: graph.FormatEndpointID(from, fl), To: graph.FormatEndpointID(to, node.DefaultInput)}
}

// runBoth 用 immediate（纯内存）与 durable（sqlite）各执行一次图，返回两份终态快照。
func runBoth(t *testing.T, registry *node.Registry, g graph.Graph, input map[string]any) [2]engine.TaskSnapshot {
	t.Helper()
	ctx := context.Background()
	imm, err := immediate.Execute(ctx, engine.EngineConfig{Nodes: registry}, engine.StartRequest{Graph: g, Input: input})
	if err != nil {
		t.Fatalf("immediate: %v", err)
	}
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "engine.db"))
	eng, err := durable.With(durable.Options{Workers: 2, Shards: 2, PollInterval: 5 * time.Millisecond})(ctx,
		engine.EngineConfig{DB: db, Nodes: registry})
	if err != nil {
		t.Fatal(err)
	}
	// durable 的后台调度随 Build 启动、随 Close 停止。
	defer func() { _ = eng.Close(ctx) }()
	rec, err := eng.Start(ctx, engine.StartRequest{Graph: g, Input: input})
	if err != nil {
		t.Fatalf("durable start: %v", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		dur, err := eng.Snapshot(ctx, rec.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		if graph.IsTerminal(dur.Task.Status) {
			return [2]engine.TaskSnapshot{imm, dur}
		}
		if time.Now().After(deadline) {
			t.Fatalf("durable task did not finish: %s", dur.Task.Status)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// activity 注册一个无配置的活动节点：出口为 outputs，输出为 fields，可自定义输入名。
func activity(t *testing.T, registry *node.Registry, id string, outputs, fields []string, exec enginetest.ExecFunc) {
	t.Helper()
	err := registry.Register(enginetest.FuncNode{
		NodeSpec: node.WithParams(node.NodeSpec{
			ID: id, Name: id,
			Endpoints: node.EndpointSpec{Inputs: []string{node.DefaultInput}, Outputs: outputs},
			OutputsByEndpoint: map[string]*jsonschema.Schema{
				node.EndpointPayloadDefault: node.OutputSchema(fields...),
			},
		}, struct{}{}),
		Exec: exec,
	})
	if err != nil {
		t.Fatal(err)
	}
}

// ref 构造对节点输出（或 graph.InputSource）的输入引用。
// newRegistry 返回只含核心内置节点的注册表；各用例再用 activity 追加自己的测试节点。
func newRegistry(t *testing.T) *node.Registry {
	t.Helper()
	registry, err := service.NewNodeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func ref(from, field string) graph.InputRef { return graph.InputRef{From: from, Field: field} }

func mustRaw(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// required 把输入名声明为必填的运行输入 Schema。
func required(names ...string) *jsonschema.Schema {
	return graph.RunInputSchema(names...)
}

func TestSameNodeExecutorServesImmediateAndDurable(t *testing.T) {
	var calls atomic.Int64
	registry := newRegistry(t)
	activity(t, registry, "shared_activity", []string{node.DefaultOutput}, []string{"shared"}, func(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
		calls.Add(1)
		return node.NodeOutput{Set: map[string]any{"shared": in.Vars["value"]}}, nil
	})
	g := withStart(graph.Graph{
		InputSchema: required("value"),
		Nodes: []graph.Node{
			{ID: "work", Kind: "shared_activity", Inputs: map[string]graph.InputRef{"value": ref(graph.InputSource, "value")}},
			{ID: "end", Kind: builtin.End, Inputs: map[string]graph.InputRef{"shared": ref("work", "shared")}},
		},
		Edges: []graph.Edge{ge("last", "work", "end")},
	})
	for i, s := range runBoth(t, registry, g, map[string]any{"value": "ok"}) {
		if s.Task.Status != graph.SUCCEEDED || s.Task.Output["shared"] != "ok" {
			t.Fatalf("engine %d: %s %v", i, s.Task.Status, s.Task.Output)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("shared executor calls=%d, want 2", calls.Load())
	}
}

func TestCustomConditionBranchConsistency(t *testing.T) {
	registry := newRegistry(t)
	activity(t, registry, "condition_activity", []string{"yes", "no"}, []string{"choice_result"}, func(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
		choice, _ := in.Vars["choice"].(string)
		return node.NodeOutput{Set: map[string]any{"choice_result": choice}, Endpoint: choice}, nil
	})
	activity(t, registry, "path_activity", []string{node.DefaultOutput}, []string{"path_visited"}, func(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
		path, _ := in.Vars["path_taken"].(string)
		return node.NodeOutput{Set: map[string]any{"path_visited": path}}, nil
	})
	pathIn := map[string]graph.InputRef{"path_taken": ref(graph.InputSource, "path_taken")}
	mergeInputs := map[string]graph.InputRef{
		"Mode": {Value: json.RawMessage(`"any"`)},
		"Outputs": {Value: mustRaw(t, []builtin.MergeFirstField{
			{Name: "path_visited", First: []graph.InputRef{ref("path_yes", "path_visited"), ref("path_no", "path_visited")}},
		})},
	}
	g := withStart(graph.Graph{
		InputSchema: required("choice", "path_taken"),
		Nodes: []graph.Node{
			{ID: "decide", Kind: "condition_activity", Inputs: map[string]graph.InputRef{"choice": ref(graph.InputSource, "choice")}},
			{ID: "path_yes", Kind: "path_activity", Inputs: pathIn},
			{ID: "path_no", Kind: "path_activity", Inputs: pathIn},
			{ID: "m", Kind: builtin.Merge, Inputs: mergeInputs},
			{ID: "end", Kind: builtin.End, Inputs: map[string]graph.InputRef{
				"choice_result": ref("decide", "choice_result"), "path_visited": ref("m", "path_visited")}},
		},
		Edges: []graph.Edge{
			ge("e_yes", "decide", "path_yes", "yes"),
			ge("e_no", "decide", "path_no", "no"),
			ge("e_y_m", "path_yes", "m"),
			ge("e_n_m", "path_no", "m"),
			ge("e_m_end", "m", "end"),
		},
	})
	for _, choice := range []string{"yes", "no"} {
		t.Run("choice="+choice, func(t *testing.T) {
			for i, s := range runBoth(t, registry, g, map[string]any{"choice": choice, "path_taken": choice}) {
				if s.Task.Status != graph.SUCCEEDED || s.Task.Output["choice_result"] != choice || s.Task.Output["path_visited"] != choice {
					t.Fatalf("engine %d: %s %v", i, s.Task.Status, s.Task.Output)
				}
			}
		})
	}
}

func TestCustomConditionEndpointErrorContract(t *testing.T) {
	for _, badBranch := range []string{graph.EndpointError, "unregistered_tag"} {
		t.Run("branch="+badBranch, func(t *testing.T) {
			registry := newRegistry(t)
			activity(t, registry, "cond_bad", []string{"yes", "no"}, []string{"test"}, func(context.Context, node.NodeInput) (node.NodeOutput, error) {
				return node.NodeOutput{Set: map[string]any{"test": "val"}, Endpoint: badBranch}, nil
			})
			g := withStart(graph.Graph{
				Nodes: []graph.Node{{ID: "decide", Kind: "cond_bad"}, {ID: "end", Kind: builtin.End}},
				Edges: []graph.Edge{ge("e_yes", "decide", "end", "yes")},
			})
			for i, s := range runBoth(t, registry, g, nil) {
				if s.Task.Status != graph.FAILED {
					t.Fatalf("engine %d: want FAILED, got %s", i, s.Task.Status)
				}
			}
		})
	}
}

// 精确匹配的出边优先；没有精确匹配时走全部默认出边（分叉）。按 Run 状态与 Outputs 判断走过的节点。
func TestDefaultAndMultipleMatchingEdgesConsistency(t *testing.T) {
	registry := newRegistry(t)
	activity(t, registry, "branch_activity", []string{"exact", node.DefaultOutput}, nil, func(_ context.Context, in node.NodeInput) (node.NodeOutput, error) {
		branch, _ := in.Vars["branch"].(string)
		return node.NodeOutput{Endpoint: branch}, nil
	})
	activity(t, registry, "recorder", []string{node.DefaultOutput}, []string{"hit"}, func(context.Context, node.NodeInput) (node.NodeOutput, error) {
		return node.NodeOutput{Set: map[string]any{"hit": true}}, nil
	})
	g := withStart(graph.Graph{
		InputSchema: required("branch"),
		Nodes: []graph.Node{
			{ID: "decide", Kind: "branch_activity", Inputs: map[string]graph.InputRef{"branch": ref(graph.InputSource, "branch")}},
			{ID: "exact_path", Kind: "recorder"},
			{ID: "def1", Kind: "recorder"},
			{ID: "def2", Kind: "recorder"},
			{ID: "end", Kind: builtin.End},
		},
		Edges: []graph.Edge{
			ge("e_exact", "decide", "exact_path", "exact"),
			ge("e_def1", "decide", "def1"),
			ge("e_def2", "decide", "def2"),
			ge("e_ex_end", "exact_path", "end"),
			ge("e_d1_end", "def1", "end"),
			ge("e_d2_end", "def2", "end"),
		},
	})
	visited := func(s engine.TaskSnapshot) map[string]bool {
		out := map[string]bool{}
		for _, r := range s.Runs {
			if r.Status == graph.SUCCEEDED || r.Status == graph.SKIPPED {
				if r.NodeID != "" {
					out[r.NodeID] = r.Status == graph.SUCCEEDED
				}
			}
			for id := range r.Outputs {
				out[id] = true
			}
		}
		return out
	}
	t.Run("exact_match", func(t *testing.T) {
		for i, s := range runBoth(t, registry, g, map[string]any{"branch": "exact"}) {
			v := visited(s)
			if s.Task.Status != graph.SUCCEEDED || !v["exact_path"] || v["def1"] || v["def2"] {
				t.Fatalf("engine %d: %s %v", i, s.Task.Status, v)
			}
		}
	})
	t.Run("fallback_defaults", func(t *testing.T) {
		for i, s := range runBoth(t, registry, g, map[string]any{"branch": ""}) {
			v := visited(s)
			if s.Task.Status != graph.SUCCEEDED || v["exact_path"] || !v["def1"] || !v["def2"] {
				t.Fatalf("engine %d: %s (%s) %v", i, s.Task.Status, s.Task.Error, v)
			}
		}
	})
}
