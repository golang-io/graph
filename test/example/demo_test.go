package example_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/example"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/service"
	"github.com/golang-io/graph/test"
)

// nodes 是样例执行用的节点注册表：核心节点、显式注册的 HTTP 节点，以及未接模型的 LLM 节点
// （样例执行路径不调用 HTTP 与 LLM，但 demo_all_nodes 含这两类节点，保存时需要已注册）。
var nodes = func() *node.Registry {
	registry, err := service.NewNodeRegistry(builtin.HTTPNode{}, builtin.LLMNode{})
	if err != nil {
		panic(err)
	}
	return registry
}()

func openTestStore(t *testing.T, dir string) *service.Store {
	t.Helper()
	db := test.MustOpen(t, filepath.Join(dir, "graph.db"))
	st, err := service.OpenDB(context.Background(), db, nodes)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ExampleGraph(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	return st
}

func mustGraph(t *testing.T, id string) graph.Graph {
	raw, err := example.FS.ReadFile(id + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ Graph graph.Graph }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if err := doc.Graph.Validate(); err != nil {
		t.Fatal(err)
	}
	return doc.Graph
}

// execute 用 immediate 纯内存模式执行样例图并返回终态 Task；mode 写入 Policy.ExecutionMode。
func execute(t *testing.T, g graph.Graph, input map[string]any, mode string, resolve engine.GraphResolver) engine.Task {
	t.Helper()
	snap, err := immediate.Execute(context.Background(), engine.EngineConfig{Nodes: nodes, ResolveGraph: resolve},
		engine.StartRequest{Graph: g, Input: input, Policy: engine.TaskPolicy{ExecutionMode: mode}})
	if err != nil {
		t.Fatal(err)
	}
	return snap.Task
}

func TestLoadDemoAllNodes(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	dto, err := st.Get(context.Background(), "demo_all_nodes")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Version == 0 {
		t.Fatal("expected version")
	}
	types := map[string]bool{}
	for _, n := range dto.Graph.Nodes {
		types[n.Kind] = true
	}
	for _, want := range []string{
		builtin.Start, builtin.Template, builtin.Switch, builtin.HTTP, builtin.LLM, builtin.Merge, builtin.End,
	} {
		if !types[want] {
			t.Fatalf("missing node type %s", want)
		}
	}
	if err := service.ExampleGraph(context.Background(), st); err != nil {
		t.Fatal(err)
	}
	def, err := st.EnsureRunVersion(context.Background(), "demo_all_nodes")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(def.Graph)
	if len(raw) < 50 {
		t.Fatalf("graph too small: %s", raw)
	}
}

func TestDemoAllNodesExecuteWithoutLLM(t *testing.T) {
	g := mustGraph(t, "demo_all_nodes")
	res := execute(t, g, map[string]any{"name": "Bob", "score": 85}, "", nil)
	if res.Output["result"] != "FAIL | 你好，Bob！当前得分 85 | score 未达标" {
		t.Fatalf("got %#v", res.Output)
	}
}

func TestLoadDemoLoopMerge(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	dto, err := st.Get(context.Background(), "demo_loop_merge")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Version == 0 {
		t.Fatal("expected version")
	}
	types := map[string]bool{}
	for _, n := range dto.Graph.Nodes {
		types[n.Kind] = true
	}
	for _, want := range []string{builtin.Merge, builtin.Map, builtin.Reduce, builtin.Switch} {
		if !types[want] {
			t.Fatalf("missing node type %s", want)
		}
	}
	if err := service.ExampleGraph(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

func TestDemoLoopMergeExecute(t *testing.T) {
	g := mustGraph(t, "demo_loop_merge")
	res := execute(t, g, map[string]any{"score": 40, "items": []any{"a", "b", "c"}}, "", nil)
	if res.Output["tag"] != "lo" || res.Output["acc"] != "abc" {
		t.Fatalf("got %#v", res.Output)
	}
	res2 := execute(t, g, map[string]any{"score": 80, "items": []any{"x"}}, "", nil)
	if res2.Output["tag"] != "hi" || res2.Output["acc"] != "x" {
		t.Fatalf("got %#v", res2.Output)
	}
}

func TestLoadDemoMapReduce(t *testing.T) {
	st := openTestStore(t, t.TempDir())
	dto, err := st.Get(context.Background(), "demo_map_reduce")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Version == 0 {
		t.Fatal("expected version")
	}
	// 循环体布局存放在 UI.SubLayouts（值类型仍为 graph.Layout），坐标相对 map 容器左上角。
	if len(dto.UI.SubLayouts["lp"].Positions) != 7 {
		t.Fatalf("body layout: %#v", dto.UI.SubLayouts)
	}
}

// TestDemoMapReduceExecute 按 UI 的方式（JSON 解码）传入订单：数字为 float64，
// 循环体内 switch 读取 map 透传的 threshold 分支，两个 reduce 分别合计金额与拼接明细。
func TestDemoMapReduceExecute(t *testing.T) {
	g := mustGraph(t, "demo_map_reduce")
	var input map[string]any
	raw := `{"orders":[{"name":"键盘","price":120,"qty":1},{"name":"苹果","price":3,"qty":4},{"name":"显示器","price":899,"qty":2}],"threshold":100}`
	if err := json.Unmarshal([]byte(raw), &input); err != nil {
		t.Fatal(err)
	}
	res := execute(t, g, input, "", nil)
	if res.Status != graph.SUCCEEDED {
		t.Fatalf("status=%s err=%s", res.Status, res.Error)
	}
	if fmt.Sprint(res.Output["total"]) != "1930" {
		t.Fatalf("total=%#v", res.Output["total"])
	}
	if want := "键盘：大单 120 元；苹果：小单 12 元；显示器：大单 1798 元"; res.Output["report"] != want {
		t.Fatalf("report=%q want %q", res.Output["report"], want)
	}
}

func TestLoadDemoP1(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	for _, id := range []string{"demo_child_echo", "demo_p1_control"} {
		dto, err := st.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		if dto.Version == 0 {
			t.Fatalf("%s missing version", id)
		}
	}
}

func TestDemoP1ControlExecute(t *testing.T) {
	child := mustGraph(t, "demo_child_echo")
	parent := mustGraph(t, "demo_p1_control")
	resolve := func(_ context.Context, id string) (graph.Graph, error) {
		if id == "demo_child_echo" {
			g := child
			g.GraphID, g.GraphVersion = id, 1
			return g, nil
		}
		return graph.Graph{}, fmt.Errorf("unknown %s", id)
	}

	failRes := execute(t, parent, map[string]any{"name": "Ada", "force_fail": 1}, "", resolve)
	if failRes.Output["result"] != "recovered:forced fail for Ada" {
		t.Fatalf("fail path: %#v", failRes.Output)
	}

	okRes := execute(t, parent, map[string]any{"name": "Ada", "force_fail": 0}, "", resolve)
	if okRes.Output["result"] != "ok" {
		t.Fatalf("ok path: %#v", okRes.Output)
	}
	sub, _ := okRes.Output["sub"].(map[string]any)
	if sub["greet"] != "echo:Ada" {
		t.Fatalf("sub: %#v", okRes.Output["sub"])
	}
}

func TestLoadDemoCode(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	dto, err := st.Get(context.Background(), "demo_code")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Version == 0 {
		t.Fatal("expected version")
	}
}

func TestDemoCodeExecute(t *testing.T) {
	g := mustGraph(t, "demo_code")
	res := execute(t, g, map[string]any{"a": 3.0, "b": 8.0, "name": "Ada"}, "", nil)
	if res.Output["total"] != int64(11) || res.Output["pass"] != true {
		t.Fatalf("%#v", res.Output)
	}
	if res.Output["label"] != "Ada:11" {
		t.Fatalf("label=%v", res.Output["label"])
	}
}

func TestLoadDemoTour(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	dto, err := st.Get(context.Background(), "demo_tour")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Version == 0 {
		t.Fatal("expected version")
	}
	types := map[string]bool{}
	for _, n := range dto.Graph.Nodes {
		types[n.Kind] = true
		if n.ID == "join_par" {
			var cfg builtin.MergeInput
			if err := json.Unmarshal(node.InputsLiteralObject(n.Inputs), &cfg); err != nil {
				t.Fatal(err)
			}
			if cfg.Mode != "all" {
				t.Fatalf("join_par params=%+v", cfg)
			}
		}
	}
	for _, want := range []string{
		builtin.Template, builtin.Switch, builtin.Merge,
		builtin.Start, builtin.Code, builtin.Parallel, builtin.Map, builtin.Reduce, builtin.End,
	} {
		if !types[want] {
			t.Fatalf("missing node type %s", want)
		}
	}
	if dto.UI.ExecutionMode != graph.ExecParallel {
		t.Fatalf("ExecutionMode=%v", dto.UI.ExecutionMode)
	}
	if len(dto.UI.StickyNotes) != 5 {
		t.Fatalf("want 5 annotation sticky notes, got %d", len(dto.UI.StickyNotes))
	}
	if err := service.ExampleGraph(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDemoImplicitFork(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	dto, err := st.Get(context.Background(), "demo_implicit_fork")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Version == 0 {
		t.Fatal("expected version")
	}
	fromFork := 0
	intoJoin := 0
	joinType := ""
	for _, n := range dto.Graph.Nodes {
		if n.ID == "join" {
			joinType = n.Kind
		}
	}
	if joinType != builtin.Template {
		t.Fatalf("join type=%s want template", joinType)
	}
	for _, e := range dto.Graph.Edges {
		if e.FromNode() == "fork" {
			fromFork++
			// 隐式分叉：fork 的所有出边都从默认出端点 out 出发。
			if e.FromName() != node.DefaultOutput {
				t.Fatalf("fork edge %s from=%q, want default output", e.ID, e.FromName())
			}
		}
		if e.ToNode() == "join" {
			intoJoin++
		}
	}
	if fromFork != 2 {
		t.Fatalf("fork outs=%d", fromFork)
	}
	if intoJoin != 2 {
		t.Fatalf("join ins=%d", intoJoin)
	}
	if err := service.ExampleGraph(context.Background(), st); err != nil {
		t.Fatal(err)
	}
}

func TestDemoImplicitForkExecute(t *testing.T) {
	g := mustGraph(t, "demo_implicit_fork")
	want := map[string]any{
		"greeting": "同源：Ada",
		"left":     "上路:Ada",
		"right":    "下路:Ada",
		"summary":  "上路:Ada | 下路:Ada",
	}
	for _, mode := range []string{"", graph.ExecSerial, graph.ExecParallel} {
		res := execute(t, g, map[string]any{"name": "Ada"}, mode, nil)
		if res.Status != graph.SUCCEEDED {
			t.Fatalf("mode=%q status=%s err=%s", mode, res.Status, res.Error)
		}
		for k, v := range want {
			if res.Output[k] != v {
				t.Fatalf("mode=%q %s=%v want %v output=%#v", mode, k, res.Output[k], v, res.Output)
			}
		}
	}
}

func TestDemoTourExecute(t *testing.T) {
	g := mustGraph(t, "demo_tour")
	res := execute(t, g, map[string]any{"name": "Ada", "score": 40, "items": []any{"a", "b", "c"}}, graph.ExecParallel, nil)
	if res.Status != graph.SUCCEEDED {
		t.Fatalf("status=%s err=%s", res.Status, res.Error)
	}
	if res.Output["tag"] != "lo" || res.Output["lane"] != "lo|Ada" {
		t.Fatalf("branch/code: %#v", res.Output)
	}
	if res.Output["opt"] != "乐观：你好，Ada！得分 40" {
		t.Fatalf("opt=%v", res.Output["opt"])
	}
	if res.Output["pess"] != "审慎：lo" {
		t.Fatalf("pess=%v", res.Output["pess"])
	}
	if res.Output["acc"] != "abc" {
		t.Fatalf("acc=%v", res.Output["acc"])
	}

	res2 := execute(t, g, map[string]any{"name": "Bob", "score": 80, "items": []any{"x"}}, graph.ExecParallel, nil)
	if res2.Output["tag"] != "hi" || res2.Output["acc"] != "x" {
		t.Fatalf("hi path: %#v", res2.Output)
	}
}

// TestLoadDemoPerf1000 校验 1000 节点性能压测样例的定义加载、节点规模与核心控制种类覆盖。
// TestLoadDemoPerf1000 verifies that the 1000-node benchmark example loads with all core control and activity kinds.
func TestLoadDemoPerf1000(t *testing.T) {
	dir := t.TempDir()
	st := openTestStore(t, dir)
	dto, err := st.Get(context.Background(), "demo_perf_1000")
	if err != nil {
		t.Fatal(err)
	}
	if dto.Version == 0 {
		t.Fatal("expected version")
	}
	if len(dto.Graph.Nodes) != 1000 {
		t.Fatalf("expected 1000 nodes, got %d", len(dto.Graph.Nodes))
	}
	types := map[string]int{}
	for _, n := range dto.Graph.Nodes {
		types[n.Kind]++
	}
	for _, want := range []string{
		builtin.Start, builtin.End, builtin.Template, builtin.Code,
		builtin.Parallel, builtin.Merge, builtin.Switch, builtin.Map, builtin.Reduce, builtin.StopError,
	} {
		if types[want] == 0 {
			t.Fatalf("missing node kind %s", want)
		}
	}
	if len(dto.UI.SubLayouts) != 6 {
		t.Fatalf("expected 6 map sub-layouts, got %d", len(dto.UI.SubLayouts))
	}
}

// TestDemoPerf1000Execute 验证 1000 节点压测图在默认空输入、自定义输入以及 serial / parallel 模式下的执行结果。
// TestDemoPerf1000Execute runs the 1000-node benchmark graph with default and custom inputs across execution modes.
func TestDemoPerf1000Execute(t *testing.T) {
	g := mustGraph(t, "demo_perf_1000")

	// 1. 默认空输入（由 init 节点补齐 seed=1, items=[1,2,3,4], mode="perf"），分别验证 parallel 与 serial 调度
	for _, mode := range []string{graph.ExecParallel, graph.ExecSerial} {
		res := execute(t, g, map[string]any{}, mode, nil)
		if res.Status != graph.SUCCEEDED {
			t.Fatalf("mode=%q status=%s err=%s", mode, res.Status, res.Error)
		}
		if res.Output["nodes"] != int64(1000) || res.Output["score"] != int64(207) || res.Output["grade"] != "MID" {
			t.Fatalf("mode=%q unexpected output: %#v", mode, res.Output)
		}
		wantSummary := "perf:MID:s5=26:s10=51:s15=76:s20=101|L10|L21|H32|H43|score=207"
		if res.Output["summary"] != wantSummary {
			t.Fatalf("mode=%q summary=%q want %q", mode, res.Output["summary"], wantSummary)
		}
	}

	// 2. 自定义输入（高分档 seed=120，触发 final_sw 的 Case0 HIGH 分支）
	resHigh := execute(t, g, map[string]any{
		"seed":  120,
		"items": []any{2, 5},
		"mode":  "bench",
	}, graph.ExecParallel, nil)
	if resHigh.Status != graph.SUCCEEDED {
		t.Fatalf("high status=%s err=%s", resHigh.Status, resHigh.Error)
	}
	if resHigh.Output["grade"] != "HIGH" || resHigh.Output["score"] != int64(291) || resHigh.Output["map_report"] != "L20|H51" {
		t.Fatalf("high output: %#v", resHigh.Output)
	}

	// 3. 容错输入：items 传入单个整数或中文全角括号字符串、mode 传入数字时，init 节点自动规整为合法切片与字符串
	for _, tc := range []map[string]any{
		{"seed": 1, "items": 1, "mode": 1},
		{"seed": 1, "items": "【1，2，3，4，5，6，7，8，9】", "mode": 1},
	} {
		resCompat := execute(t, g, tc, graph.ExecParallel, nil)
		if resCompat.Status != graph.SUCCEEDED {
			t.Fatalf("input=%#v status=%s err=%s", tc, resCompat.Status, resCompat.Error)
		}
	}
}

// 样例 ui 经 graph.UI 再规范化后，必须和原文的严格 JSON 相同，避免内容哈希无故变化。
func TestExampleUICanonicalStable(t *testing.T) {
	entries, err := fs.ReadDir(example.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, ent := range entries {
		if ent.IsDir() {
			continue
		}
		raw, err := example.FS.ReadFile(ent.Name())
		if err != nil {
			t.Fatal(err)
		}
		var doc struct{ UI json.RawMessage }
		if err := json.Unmarshal(raw, &doc); err != nil {
			t.Fatal(ent.Name(), err)
		}
		ui, err := graph.ParseUI(doc.UI)
		if err != nil {
			t.Fatal(ent.Name(), err)
		}
		got, err := jsonx.Canonical(ui)
		if err != nil {
			t.Fatal(ent.Name(), err)
		}
		want, err := jsonx.Strict(doc.UI)
		if err != nil {
			t.Fatal(ent.Name(), err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("%s\n got %s\nwant %s", ent.Name(), got, want)
		}
	}
}
