package service_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/api"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/durable"
	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/service"
	"github.com/golang-io/graph/test"
	"github.com/golang-io/graph/test/engine/enginetest"
)

func versionGraph(value string) graph.Graph {
	outputs, _ := json.Marshal([]map[string]string{{"Key": "answer", "Value": value}})
	return enginetest.PrependStart(graph.Graph{Nodes: []graph.Node{
		{ID: "m", Kind: builtin.Template, Inputs: map[string]graph.InputRef{"Outputs": {Value: outputs}}},
		{ID: "e", Kind: builtin.End, Inputs: map[string]graph.InputRef{"answer": {From: "m", Field: "answer"}}},
	}, Edges: []graph.Edge{{ID: "me", From: "m.Out", To: "e.In"}}})
}

func TestVersionHistoryPreservesGraphAndUI(t *testing.T) {
	ctx := context.Background()
	st, err := service.OpenDB(ctx, test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db")), nil)
	if err != nil {
		t.Fatal(err)
	}
	first := withEndpointSides(service.Definition{GraphID: "history", Graph: versionGraph("one"), UI: graph.UI{Layout: graph.Layout{Positions: map[string]graph.Point{"m": {X: 10, Y: 20}}}}})
	v1, err := st.Save(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	second := withEndpointSides(service.Definition{GraphID: first.GraphID, Graph: versionGraph("two"), UI: graph.UI{Layout: graph.Layout{Positions: map[string]graph.Point{"m": {X: 30, Y: 40}}}}})
	v2, err := st.Save(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	old, err := st.GetVersion(ctx, first.GraphID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.RowID == v2.RowID || v2.Version != 2 || !reflect.DeepEqual(old.Graph, v1.Graph) || !reflect.DeepEqual(old.UI, v1.UI) {
		t.Fatalf("historical snapshot changed: old=%+v v1=%+v v2=%+v", old, v1, v2)
	}
	second.Name = "renamed"
	same, err := st.Save(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if same.Version != 2 || same.RowID != v2.RowID {
		t.Fatalf("same content allocated new version: %+v", same)
	}
	restored, err := st.Save(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	if restored.Version != 3 {
		t.Fatalf("restore version=%d", restored.Version)
	}
	current, err := st.Get(ctx, first.GraphID)
	if err != nil || current.Version != 3 {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	versions, err := st.ListVersions(ctx, first.GraphID, 2)
	if err != nil || len(versions) != 2 || versions[0].Version != 3 || versions[1].Version != 2 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	other, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "other", Graph: versionGraph("other")}))
	if err != nil || other.Version != 1 {
		t.Fatalf("other=%+v err=%v", other, err)
	}
	if err = st.Delete(ctx, first.GraphID); err != nil {
		t.Fatal(err)
	}
	if _, err = st.Get(ctx, first.GraphID); !errors.Is(err, engine.ErrNotFound) {
		t.Fatalf("deleted graph resurrected: %v", err)
	}
	rows, err := st.List(ctx)
	if err != nil || len(rows) != 1 || rows[0].GraphID != "other" {
		t.Fatalf("list=%+v err=%v", rows, err)
	}
	if _, err = st.GetVersion(ctx, first.GraphID, 1); err != nil {
		t.Fatalf("deleted history lost: %v", err)
	}
}

func TestConcurrentVersionsAcrossStoreHandles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	a, err := service.OpenDB(ctx, test.MustOpen(t, filepath.Join(dir, "graph.db")), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := service.OpenDB(ctx, test.MustOpen(t, filepath.Join(dir, "graph.db")), nil)
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			st := a
			if i%2 == 1 {
				st = b
			}
			_, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "concurrent", Graph: versionGraph(fmt.Sprint(i))}))
			results <- err
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	versions, err := a.ListVersions(ctx, "concurrent", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 8 {
		t.Fatalf("lost saves: %d", len(versions))
	}
	for i, v := range versions {
		if v.Version != int64(8-i) {
			t.Fatalf("version gap: %d", v.Version)
		}
	}
}

func TestHistoricalVersionExecutionAndRerun(t *testing.T) {
	for _, kind := range []string{engine.KindImmediate, engine.KindDurable} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			rt, err := startTestRuntime(t, ctx, service.Options{Builders: []engine.Builder{immediate.Build, durable.Build}})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close(ctx)
			v1, err := rt.Store.Save(ctx, withEndpointSides(service.Definition{GraphID: "execute", Graph: versionGraph("one"), Engine: kind}))
			if err != nil {
				t.Fatal(err)
			}
			v2, err := rt.Store.Save(ctx, withEndpointSides(service.Definition{GraphID: v1.GraphID, Graph: versionGraph("two")}))
			if err != nil {
				t.Fatal(err)
			}
			wait := func(id string, wantVersion int64, wantAnswer string) engine.TaskSnapshot {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					snap, err := rt.Engines.Snapshot(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					if graph.IsTerminal(snap.Task.Status) {
						if snap.Task.Status != graph.SUCCEEDED || snap.Task.GraphVersion != wantVersion || snap.Task.Output["answer"] != wantAnswer {
							t.Fatalf("wrong version execution: task=%+v", snap.Task)
						}
						return snap
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("run timed out")
				return engine.TaskSnapshot{}
			}
			old, err := rt.Start(ctx, engine.StartRequest{GraphID: v1.GraphID, GraphVersion: 1})
			if err != nil {
				t.Fatal(err)
			}
			if old.GraphVersion != 1 {
				t.Fatalf("receipt=%+v", old)
			}
			snap := wait(old.TaskID, 1, "one")
			if len(snap.Task.Graph.Nodes) == 0 {
				t.Fatal("missing historical run graph")
			}
			_, actualHash, err := graph.HashDefinition(snap.Task.Graph)
			_, wantHash, wantErr := graph.HashDefinition(v1.Graph)
			if err != nil || wantErr != nil || actualHash != wantHash {
				t.Fatalf("historical graph hash=%s want=%s err=%v/%v", actualHash, wantHash, err, wantErr)
			}
			latest, err := rt.Start(ctx, engine.StartRequest{GraphID: v1.GraphID})
			if err != nil {
				t.Fatal(err)
			}
			wait(latest.TaskID, 2, "two")
			if _, err = rt.Start(ctx, engine.StartRequest{GraphID: v1.GraphID, GraphVersion: 1, Graph: v2.Graph}); !errors.Is(err, engine.ErrInvalid) {
				t.Fatalf("mismatched version accepted: %v", err)
			}
			if _, err = rt.Start(ctx, engine.StartRequest{GraphID: v1.GraphID, GraphVersion: 99}); !errors.Is(err, engine.ErrNotFound) {
				t.Fatalf("unknown version accepted: %v", err)
			}
			h := api.Handler(rt)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/graphs/execute/versions/1", nil))
			var result service.Definition
			if err = json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || result.Version != 1 || !reflect.DeepEqual(result.Graph, v1.Graph) {
				t.Fatalf("historical API: %d %s", w.Code, w.Body.String())
			}
		})
	}
}

// spoofed 返回身份被改成任意值的图，模拟客户端在请求体 Graph 中自带 GraphID / GraphVersion。
func spoofed(g graph.Graph) graph.Graph {
	g.GraphID, g.GraphVersion = "spoof", 99
	return g
}

// 图身份只由服务端写入：保存时覆盖请求中的值，库中 JSON 与 GraphID / Version 列同值，各读取路径原样返回。
func TestGraphIdentityAssignedByServer(t *testing.T) {
	ctx := context.Background()
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))
	st, err := service.OpenDB(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity := func(what string, g graph.Graph, id string, version int64) {
		t.Helper()
		if g.GraphID != id || g.GraphVersion != version {
			t.Fatalf("%s: graph identity %s@%d, want %s@%d", what, g.GraphID, g.GraphVersion, id, version)
		}
	}

	draft, err := st.Create(ctx, "draft", "")
	if err != nil {
		t.Fatal(err)
	}
	identity("draft", draft.Graph, draft.GraphID, 0)

	v1, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "ident", Graph: spoofed(versionGraph("one"))}))
	if err != nil {
		t.Fatal(err)
	}
	if v1.Version != 1 {
		t.Fatalf("v1 version = %d", v1.Version)
	}
	identity("saved v1", v1.Graph, "ident", 1)

	// 库中 JSON 直接带身份，与列同值。
	var raw string
	if err := db.Raw(`SELECT "Graph" FROM tb_graph_definition WHERE "GraphID" = ? AND "Version" = 1`, "ident").Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"GraphID":"ident"`) || !strings.Contains(raw, `"GraphVersion":1`) || strings.Contains(raw, "spoof") {
		t.Fatalf("stored graph JSON = %s", raw)
	}

	// 内容不变、请求自带另一个身份：不追加版本。
	same, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "ident", Name: "renamed", Graph: spoofed(versionGraph("one"))}))
	if err != nil || same.Version != 1 {
		t.Fatalf("same content: version=%d err=%v", same.Version, err)
	}
	identity("unchanged save", same.Graph, "ident", 1)

	v2, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "ident", Graph: spoofed(versionGraph("two"))}))
	if err != nil || v2.Version != 2 {
		t.Fatalf("v2: version=%d err=%v", v2.Version, err)
	}
	identity("saved v2", v2.Graph, "ident", 2)

	old, err := st.GetVersion(ctx, "ident", 1)
	if err != nil {
		t.Fatal(err)
	}
	identity("GetVersion(1)", old.Graph, "ident", 1)
	current, err := st.Get(ctx, "ident")
	if err != nil {
		t.Fatal(err)
	}
	identity("Get", current.Graph, "ident", 2)
	versions, err := st.ListVersions(ctx, "ident", 0)
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions=%d err=%v", len(versions), err)
	}
	for _, v := range versions {
		identity("ListVersions", v.Graph, "ident", v.Version)
	}
	resolved, err := st.ResolveGraph(ctx, "ident")
	if err != nil {
		t.Fatal(err)
	}
	identity("ResolveGraph", resolved, "ident", 2)
}

// 启动时 Task 的 GraphID / GraphVersion 列与图快照身份同值，请求中 Graph 自带的身份被覆盖。
func TestStartPinsGraphIdentity(t *testing.T) {
	for _, kind := range []string{engine.KindImmediate, engine.KindDurable} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			rt, err := startTestRuntime(t, ctx, service.Options{Builders: []engine.Builder{immediate.Build, durable.Build}})
			if err != nil {
				t.Fatal(err)
			}
			defer rt.Close(ctx)
			if _, err := rt.Store.Save(ctx, withEndpointSides(service.Definition{GraphID: "pinned", Graph: versionGraph("one"), Engine: kind})); err != nil {
				t.Fatal(err)
			}
			done := func(id string) engine.Task {
				t.Helper()
				deadline := time.Now().Add(5 * time.Second)
				for time.Now().Before(deadline) {
					snap, err := rt.Engines.Snapshot(ctx, id)
					if err != nil {
						t.Fatal(err)
					}
					if graph.IsTerminal(snap.Task.Status) {
						if snap.Task.Status != graph.SUCCEEDED {
							t.Fatalf("task %s = %s %q", id, snap.Task.Status, snap.Task.Error)
						}
						return snap.Task
					}
					time.Sleep(10 * time.Millisecond)
				}
				t.Fatal("run timed out")
				return engine.Task{}
			}
			check := func(what string, task engine.Task, id string, version int64) {
				t.Helper()
				if task.GraphID != id || task.GraphVersion != version || task.Graph.GraphID != id || task.Graph.GraphVersion != version {
					t.Fatalf("%s: column %s@%d graph %s@%d, want %s@%d", what,
						task.GraphID, task.GraphVersion, task.Graph.GraphID, task.Graph.GraphVersion, id, version)
				}
			}

			// 指定版本并附带内容相同、身份伪造的图：核对通过，身份为该版本。
			rec, err := rt.Start(ctx, engine.StartRequest{GraphID: "pinned", GraphVersion: 1, Graph: spoofed(versionGraph("one"))})
			if err != nil {
				t.Fatal(err)
			}
			check("saved version", done(rec.TaskID), "pinned", 1)

			// 临时图：身份取请求顶层（GraphID 为空、GraphVersion 为 0），图自带的值被覆盖。
			rec, err = rt.Start(ctx, engine.StartRequest{Engine: kind, Graph: spoofed(versionGraph("tmp"))})
			if err != nil {
				t.Fatal(err)
			}
			check("temporary graph", done(rec.TaskID), "", 0)
		})
	}
}

func TestListUsesVersionInsteadOfInsertionOrder(t *testing.T) {
	ctx := context.Background()
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))
	st, err := service.OpenDB(ctx, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	g := graph.Graph{Nodes: []graph.Node{{ID: "t", Kind: builtin.Merge}, {ID: "e", Kind: builtin.End}}, Edges: []graph.Edge{{ID: "te", From: "t.Out", To: "e.In"}}}
	newer := service.Definition{GraphID: "reordered", Version: 2, Graph: g}
	older := service.Definition{GraphID: "reordered", Version: 1, Graph: g}
	if err = db.Create(&newer).Error; err != nil {
		t.Fatal(err)
	}
	if err = db.Create(&older).Error; err != nil {
		t.Fatal(err)
	}
	rows, err := st.List(ctx)
	if err != nil || len(rows) != 1 || rows[0].Version != 2 {
		t.Fatalf("list selected row order instead of version: %+v err=%v", rows, err)
	}
	duplicate := service.Definition{GraphID: newer.GraphID, Version: newer.Version, Graph: g}
	if err = db.Create(&duplicate).Error; err == nil {
		t.Fatal("database accepted duplicate graph+version")
	}
	if err = st.Delete(ctx, newer.GraphID); err != nil {
		t.Fatal(err)
	}
	rows, err = st.List(ctx)
	if err != nil || len(rows) != 0 {
		t.Fatalf("deleted graph reappeared through older row: %+v err=%v", rows, err)
	}
}
