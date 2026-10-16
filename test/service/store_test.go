package service_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/service"
	"github.com/golang-io/graph/test"
	"github.com/golang-io/graph/test/engine/enginetest"
	"gorm.io/gorm/clause"
)

func TestOpenDBCreatesSchema(t *testing.T) {
	if _, err := service.OpenDB(context.Background(), test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db")), nil); err != nil {
		t.Fatal(err)
	}
}

func TestSaveCreatesVersionAndRunBinds(t *testing.T) {
	ctx := context.Background()
	st, err := service.OpenDB(ctx, test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db")), nil)
	if err != nil {
		t.Fatal(err)
	}

	dto, err := st.Create(ctx, "ver-test", "")
	if err != nil {
		t.Fatal(err)
	}
	g := enginetest.PrependStart(graph.Graph{
		Nodes: []graph.Node{{ID: "t", Kind: builtin.Merge}, {ID: "e", Kind: builtin.End}},
		Edges: []graph.Edge{{ID: "te", From: "t.Out", To: "e.In"}},
	})
	saved, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: dto.GraphID, Graph: g}))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Version == 0 {
		t.Fatal("expected version on save")
	}
	ver, err := st.GetVersion(ctx, saved.GraphID, saved.Version)
	if err != nil {
		t.Fatal(err)
	}
	if ver.Version != saved.Version {
		t.Fatalf("version mismatch: %d vs %d", ver.Version, saved.Version)
	}
	forRun, err := st.EnsureRunVersion(ctx, dto.GraphID)
	if err != nil {
		t.Fatal(err)
	}
	if forRun.Version != saved.Version {
		t.Fatalf("run version mismatch: %d vs %d", forRun.Version, saved.Version)
	}
	snapshot, err := st.GetVersion(ctx, saved.GraphID, saved.Version)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Graph.Nodes) != 3 || snapshot.UI.Positions == nil {
		t.Fatalf("graph in version invalid: nodes=%d ui=%v", len(snapshot.Graph.Nodes), snapshot.UI)
	}
}

func TestSaveRejectsInvalidGraphWithoutDBWrite(t *testing.T) {
	ctx := context.Background()
	st, err := service.OpenDB(ctx, test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db")), nil)
	if err != nil {
		t.Fatal(err)
	}

	invalid := graph.Graph{
		Nodes: []graph.Node{{ID: "t", Kind: "unknown_type"}},
	}
	_, err = st.Save(ctx, withEndpointSides(service.Definition{GraphID: "invalid-1", Graph: invalid}))
	if err == nil {
		t.Fatal("expected error for invalid graph")
	}
	if !errors.Is(err, engine.ErrInvalid) {
		t.Fatalf("expected engine.ErrInvalid, got %v", err)
	}
	if _, getErr := st.Get(ctx, "invalid-1"); !errors.Is(getErr, engine.ErrNotFound) {
		t.Fatalf("expected not found after rejected save, got %v", getErr)
	}
}

// TestSaveRejectsInvalidUI 确认非法的端点所在边与便签 ID 冲突的 UI 不能保存，且不留下定义。
func TestSaveRejectsInvalidUI(t *testing.T) {
	ctx := context.Background()
	st, err := service.OpenDB(ctx, test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db")), nil)
	if err != nil {
		t.Fatal(err)
	}
	g := enginetest.PrependStart(graph.Graph{Nodes: []graph.Node{{ID: "work", Kind: builtin.End}}})
	_, saveErr := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "bad_ui", Graph: g, UI: graph.UI{Layout: graph.Layout{EndpointSides: map[string]graph.EndpointSides{"work": {"In": "middle"}}}}}))
	_, getErr := st.Get(ctx, "bad_ui")
	if !errors.Is(saveErr, engine.ErrInvalid) || !errors.Is(getErr, engine.ErrNotFound) {
		t.Fatalf("save=%v get=%v", saveErr, getErr)
	}
	_, noteErr := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "note_collision", Graph: g, UI: graph.UI{StickyNotes: []graph.StickyNote{{ID: "work", Width: 300, Height: 180}}}}))
	if !errors.Is(noteErr, engine.ErrInvalid) {
		t.Fatalf("collision=%v", noteErr)
	}
}

func TestSetEngineUpdatesChoice(t *testing.T) {
	ctx := context.Background()
	st, err := service.OpenDB(ctx, test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db")), nil)
	if err != nil {
		t.Fatal(err)
	}

	dto, err := st.Create(ctx, "eng-test", "")
	if err != nil {
		t.Fatal(err)
	}
	updated, err := st.SetEngine(ctx, dto.GraphID, "durable")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Engine != "durable" {
		t.Fatalf("engine=%s", updated.Engine)
	}
	if st.EngineFor(ctx, dto.GraphID) != "durable" {
		t.Fatalf("engineFor=%s", st.EngineFor(ctx, dto.GraphID))
	}
}

func TestStoreSaveDefinition(t *testing.T) {
	ctx := context.Background()
	st, err := service.OpenDB(ctx, test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db")), nil)
	if err != nil {
		t.Fatal(err)
	}

	g := enginetest.PrependStart(graph.Graph{
		Nodes: []graph.Node{{ID: "t", Kind: builtin.Merge}, {ID: "e", Kind: builtin.End}},
		Edges: []graph.Edge{{ID: "te", From: "t.Out", To: "e.In"}},
	})
	_, err = st.Save(ctx, withEndpointSides(service.Definition{GraphID: "test_upsert", Name: "Upsert Test", Description: "Desc", Graph: g}))
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.Get(ctx, "test_upsert")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Upsert Test" {
		t.Fatalf("got name %q, want 'Upsert Test'", got.Name)
	}

	// 非法 Engine 名称在 Save 时直接拒绝。
	if _, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "test_upsert", Engine: "bad engine!", Graph: g})); !errors.Is(err, engine.ErrInvalid) {
		t.Fatalf("expected ErrInvalid for invalid engine kind, got %v", err)
	}

	// v1 带 Label，追加新版本 v2 未传 Label 时不应沿用 v1 的 Label。
	v1WithLabel, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "test_upsert", Label: "release-v1", Graph: g}))
	if err != nil || v1WithLabel.Label != "release-v1" {
		t.Fatalf("v1 label = %+v, err = %v", v1WithLabel, err)
	}
	g2 := g
	g2.Nodes = append([]graph.Node(nil), g.Nodes...)
	g2.Nodes[len(g2.Nodes)-1].Name = "end-v2"
	v2NoLabel, err := st.Save(ctx, withEndpointSides(service.Definition{GraphID: "test_upsert", Graph: g2}))
	if err != nil {
		t.Fatal(err)
	}
	if v2NoLabel.Version != v1WithLabel.Version+1 || v2NoLabel.Label != "" {
		t.Fatalf("v2 = version %d label %q, want version %d and empty label", v2NoLabel.Version, v2NoLabel.Label, v1WithLabel.Version+1)
	}
}

// TestSaveRollsBackWhenVersionInsertFails 确认新版本写入失败时整个 Save 回滚，当前版本保持不变。
func TestSaveRollsBackWhenVersionInsertFails(t *testing.T) {
	ctx := context.Background()
	nodes, err := service.NewNodeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	db := test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))
	store, err := service.OpenDB(ctx, db, nodes)
	if err != nil {
		t.Fatal(err)
	}
	doc := withEndpointSides(service.Definition{GraphID: "atomic", Graph: enginetest.PrependStart(graph.Graph{
		Nodes: []graph.Node{{ID: "t", Kind: builtin.Merge}, {ID: "e", Kind: builtin.End}},
		Edges: []graph.Edge{{ID: "te", From: "t.Out", To: "e.In"}},
	})})
	before, err := store.Save(ctx, doc)
	if err != nil {
		t.Fatal(err)
	}
	if err = db.Exec(`CREATE TRIGGER reject_head BEFORE INSERT ON tb_graph_definition BEGIN SELECT RAISE(ABORT, 'injected version insert failure'); END`).Error; err != nil {
		t.Fatal(err)
	}
	// Nodes[0]=_start, Nodes[1]=t, Nodes[2]=e；改 end 的 Name 以触发新版本。
	doc.Graph.Nodes[2].Name = "changed"
	if _, err = store.Save(ctx, doc); err == nil {
		t.Fatal("injected version insert failure was not returned")
	}
	var head service.Definition
	if err = db.Where(map[string]any{"GraphID": doc.GraphID}).Order(clause.OrderByColumn{Column: clause.Column{Name: "Version"}, Desc: true}).Take(&head).Error; err != nil {
		t.Fatal(err)
	}
	if head.Version != before.Version {
		t.Fatalf("partial save: head=%d original=%d", head.Version, before.Version)
	}
}
