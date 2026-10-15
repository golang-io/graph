// 本文件是所有引擎共用的契约测试：同一组场景、同一组断言跑每个引擎，
// 保证各引擎只在调度方式上不同、执行语义一致。

package enginetest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node/builtin"
	"gorm.io/gorm"
)

// OpenDB 为一个场景返回一个空的执行库，供 EngineConfig.DB 使用。
// 本包不依赖任何数据库驱动：调用方自选方言（sqlite / MySQL / PostgreSQL），
// 用 engine.GormConfig() 打开连接，并负责在测试结束时关闭（如 tb.Cleanup）。
// 每次调用都必须返回互不共享数据的库，场景之间依赖空表起步。
//
// OpenDB returns an empty execution database for one scenario; the caller picks the
// dialect, opens it with engine.GormConfig(), and closes it when tb finishes.
type OpenDB func(tb testing.TB) *gorm.DB

// 常用节点。
func end(refs map[string]graph.InputRef) graph.Node {
	return In(N("finish", builtin.End, nil), refs)
}
func op(id string, c OpInput) graph.Node { return N(id, OpType, c) }
func tpl(id string, refs map[string]graph.InputRef, kv ...graph.OutKV) graph.Node {
	return In(N(id, builtin.Template, builtin.TemplateInput{Outputs: kv}), refs)
}
func code(id string, refs map[string]graph.InputRef, kv ...graph.OutKV) graph.Node {
	return In(N(id, builtin.Code, builtin.CodeInput{Outputs: kv}), refs)
}
func kv(key, value string) graph.OutKV { return graph.OutKV{Key: key, Value: value} }

type refs = map[string]graph.InputRef

// env 是一个场景的被测环境。
type env struct {
	t      *testing.T
	ctx    context.Context
	eng    engine.Engine
	db     *gorm.DB
	graphs map[string]graph.Graph
	mu     sync.Mutex
}

// resolve 实现 engine.GraphResolver：注册的图都视为 (id, 1) 版本。
func (e *env) resolve(_ context.Context, id string) (graph.Graph, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	g, ok := e.graphs[id]
	if !ok {
		return graph.Graph{}, fmt.Errorf("%w: graph %s", engine.ErrNotFound, id)
	}
	g.GraphID, g.GraphVersion = id, 1
	return g, nil
}

// setup 通过 open 取得一个空库、构造引擎，测试结束时关闭引擎（连接由 open 的提供方关闭）。
// 后台调度（如 durable 的 worker 与租约回收）属于引擎生命周期：Builder 返回时已启动，Close 时停止。
func setup(t *testing.T, open OpenDB, build engine.Builder) *env {
	t.Helper()
	db := open(t)
	reg, err := Registry(OpSpec())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &env{t: t, ctx: ctx, db: db, graphs: map[string]graph.Graph{}}
	eng, err := build(ctx, engine.EngineConfig{DB: db, Nodes: reg, ResolveGraph: e.resolve})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	e.eng = eng
	t.Cleanup(func() {
		cancel()
		closeCtx, done := context.WithTimeout(context.Background(), 10*time.Second)
		defer done()
		if err := eng.Close(closeCtx); err != nil {
			t.Errorf("close: %v", err)
		}
	})
	return e
}

func (e *env) start(g graph.Graph, input map[string]any, policy engine.TaskPolicy) string {
	e.t.Helper()
	rec, err := e.eng.Start(e.ctx, engine.StartRequest{GraphID: "g", Graph: g, Input: input, Policy: policy})
	if err != nil {
		e.t.Fatalf("start: %v", err)
	}
	return rec.TaskID
}

// signal 向 Task 投递控制信号。
func (e *env) signal(id string, sig engine.Signal) (engine.Receipt, error) {
	return e.eng.Signal(e.ctx, id, sig)
}

// resume 投递 resume 信号（携带调试动作 / 人工输入）。
func (e *env) resume(id string, req engine.ResumeRequest) (engine.Receipt, error) {
	return e.signal(id, engine.Signal{Kind: engine.SignalResume, Resume: &req})
}

// drain 读完 Watch channel，返回收到的全部快照；channel 15 秒内未关闭视为失败。
func (e *env) drain(ch <-chan engine.TaskSnapshot) []engine.TaskSnapshot {
	e.t.Helper()
	var out []engine.TaskSnapshot
	timeout := time.After(15 * time.Second)
	for {
		select {
		case s, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, s)
		case <-timeout:
			e.t.Fatalf("watch channel not closed after %d snapshots", len(out))
		}
	}
}

// settle 等到 Task 结束，或整体进入等待且没有待执行 / 执行中的 Run。
func (e *env) settle(id string) engine.TaskSnapshot {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		s, err := e.eng.Snapshot(e.ctx, id)
		if err != nil {
			e.t.Fatalf("snapshot: %v", err)
		}
		if graph.IsTerminal(s.Task.Status) || quiet(s) {
			return s
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("task %s did not settle: %s %s", id, s.Task.Status, dump(s.Runs))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// quiet 报告 Task 是否处于等待：没有 PENDING / RUNNING 的 Run，且至少有一个 WAITING。
func quiet(s engine.TaskSnapshot) bool {
	waiting := false
	for _, r := range s.Runs {
		switch r.Status {
		case graph.PENDING, graph.RUNNING:
			return false
		case graph.WAITING:
			if r.WaitReason != engine.WaitJoin && r.WaitReason != engine.WaitSubgraph {
				waiting = true
			}
		}
	}
	return waiting && s.Task.Status == graph.WAITING
}

func dump(runs []engine.Run) string {
	var out strings.Builder
	for _, r := range runs {
		fmt.Fprintf(&out, "\n  %s node=%s %s/%s seq=%d", r.RunID[24:], r.NodeID, r.Status, r.WaitReason, r.Seq)
	}
	return out.String()
}

func (e *env) done(id string, want graph.StatusCode) engine.TaskSnapshot {
	e.t.Helper()
	s := e.settle(id)
	if s.Task.Status != want {
		e.t.Fatalf("task status = %s (%s), want %s%s", s.Task.Status, s.Task.Error, want, dump(s.Runs))
	}
	return s
}

func (e *env) waiting(id string) engine.TaskSnapshot {
	e.t.Helper()
	s := e.settle(id)
	if s.Task.Status != graph.WAITING {
		e.t.Fatalf("task status = %s (%s), want WAITING%s", s.Task.Status, s.Task.Error, dump(s.Runs))
	}
	return s
}

func eq(t *testing.T, got, want any) {
	t.Helper()
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("got %s, want %s", g, w)
	}
}

// Option 调整契约测试的运行方式。
type Option func(*config)

type config struct {
	skip map[string]string // 场景名 → 跳过原因
}

// Skip 跳过指定场景并注明原因。只用于迁移期间语义尚未切换的引擎（如阶段 5 到阶段 6 之间的 durable），
// 切换完成后必须删除。
func Skip(reason string, names ...string) Option {
	return func(c *config) {
		for _, n := range names {
			c.skip[n] = reason
		}
	}
}

// Run 对 build 构造的引擎运行全部契约场景；每个场景调用一次 open 取得独立的空库。
func Run(t *testing.T, open OpenDB, build engine.Builder, opts ...Option) {
	c := &config{skip: map[string]string{}}
	for _, o := range opts {
		o(c)
	}
	for _, mode := range []string{graph.ExecSerial, graph.ExecParallel} {
		t.Run(mode, func(t *testing.T) { runAll(t, open, build, engine.TaskPolicy{ExecutionMode: mode}, c) })
	}
}

func runAll(t *testing.T, open OpenDB, build engine.Builder, base engine.TaskPolicy, c *config) {
	// scenario 运行一个场景；被 Skip 的场景以 t.Skip 注明原因。
	scenario := func(t *testing.T, name string, fn func(t *testing.T)) {
		t.Run(name, func(t *testing.T) {
			if reason, ok := c.skip[name]; ok {
				t.Skip(reason)
			}
			fn(t)
		})
	}
	scenario(t, "Linear", func(t *testing.T) {
		e := setup(t, open, build)
		g := GI([]string{"name"}, []graph.Node{op("a", OpInput{Set: map[string]any{"x": 1}}),
			tpl("b", refs{"x": R("a", "x"), "name": R(graph.InputSource, "name")}, kv("y", "{{x}}-{{name}}")),
			end(refs{"x": R("a", "x"), "y": R("b", "y")})}, E("a", "b"), E("b", "finish"))
		s := e.done(e.start(g, map[string]any{"name": "Ada"}, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"x": 1, "y": "1-Ada"})
		if _, ok := s.Task.Output["x"].(int64); !ok {
			t.Fatalf("x type = %T, want int64", s.Task.Output["x"])
		}
		if len(s.Runs) == 0 {
			t.Fatal("expected runs in snapshot")
		}
		// 路径输出仍挂在 Run.Outputs 上。
		found := false
		for _, r := range s.Runs {
			if out := r.Outputs["b"]; out != nil {
				eq(t, out, map[string]any{"y": "1-Ada"})
				found = true
			}
		}
		if !found {
			t.Fatalf("missing outputs for node b: %+v", s.Runs)
		}
	})

	scenario(t, "RunInput", func(t *testing.T) {
		e := setup(t, open, build)
		g := GI([]string{"name"}, []graph.Node{end(refs{"name": R(graph.InputSource, "name")})})
		for _, input := range []map[string]any{nil, {"name": "Ada", "extra": 1}} {
			if _, err := e.eng.Start(e.ctx, engine.StartRequest{GraphID: "g", Graph: g, Input: input, Policy: base}); !errors.Is(err, engine.ErrInvalid) {
				t.Fatalf("start %v err = %v, want ErrInvalid", input, err)
			}
		}
		s := e.done(e.start(g, map[string]any{"name": "Ada"}, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"name": "Ada"})
	})

	// 超过 64KB 的值经过 Task 输入、图快照（节点参数）、Run.Outputs 与 Task 输出后写库，读回后完整一致。
	// 防止 JSON 列被建成有长度上限的类型（如 MySQL 的 TEXT 最多 64KB）。
	scenario(t, "LargeValues", func(t *testing.T) {
		e := setup(t, open, build)
		in, param := strings.Repeat("i", 200<<10), strings.Repeat("p", 100<<10)
		g := GI([]string{"blob"}, []graph.Node{op("a", OpInput{Set: map[string]any{"p": param}}),
			end(refs{"blob": R(graph.InputSource, "blob"), "p": R("a", "p")})}, E("a", "finish"))
		id := e.start(g, map[string]any{"blob": in}, base)
		e.done(id, graph.SUCCEEDED)
		// immediate 在 Task 结束后才写库：轮询到表中出现终态行为止。
		var s engine.TaskSnapshot
		for deadline := time.Now().Add(10 * time.Second); ; {
			var err error
			s, err = engine.LoadSnapshot(e.db, id)
			if err == nil && graph.IsTerminal(s.Task.Status) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("stored snapshot not available: %v", err)
			}
			time.Sleep(10 * time.Millisecond)
		}
		if s.Task.Input["blob"] != in || s.Task.Output["blob"] != in || s.Task.Output["p"] != param {
			t.Fatalf("stored task values truncated: input %d, output %d / %d bytes",
				len(fmt.Sprint(s.Task.Input["blob"])), len(fmt.Sprint(s.Task.Output["blob"])), len(fmt.Sprint(s.Task.Output["p"])))
		}
		if len(s.Task.Graph.Nodes) != len(g.Nodes) {
			t.Fatalf("stored graph snapshot = %+v", s.Task.Graph)
		}
		found := false
		for _, r := range s.Runs {
			if out := r.Outputs["a"]; out != nil && out["p"] == param {
				found = true
			}
		}
		if !found {
			t.Fatal("stored Run.Outputs truncated")
		}
		// Watch 轮询用只读指纹列的 LoadFingerprint 判断变化，结果必须与完整快照的指纹一致，
		// 否则订阅者会漏推或重复推送。
		fp, err := engine.LoadFingerprint(e.db, id)
		if err != nil || fp != engine.Fingerprint(s) {
			t.Fatalf("LoadFingerprint = %q, %v; want %q", fp, err, engine.Fingerprint(s))
		}
	})

	scenario(t, "Switch", func(t *testing.T) {
		e := setup(t, open, build)
		g := GI([]string{"score"}, []graph.Node{
			In(N("gate", builtin.Switch, builtin.SwitchInput{Expr: "score >= 60"}), refs{"score": R(graph.InputSource, "score")}),
			op("hi", OpInput{Set: map[string]any{"tag": "hi"}}), op("lo", OpInput{Set: map[string]any{"tag": "lo"}}),
			N("m", builtin.Merge, builtin.MergeInput{Mode: "any", Outputs: []builtin.MergeFirstField{
				{Name: "tag", First: []graph.InputRef{R("hi", "tag"), R("lo", "tag")}}}}),
			end(refs{"tag": R("m", "tag")})},
			E("gate", "hi", "Case0"), E("gate", "lo", "Case1"), E("hi", "m"), E("lo", "m"), E("m", "finish"))
		s := e.done(e.start(g, map[string]any{"score": 70}, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"tag": "hi"})
		s = e.done(e.start(g, map[string]any{"score": 10}, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"tag": "lo"})
	})

	scenario(t, "MissingInput", func(t *testing.T) {
		e := setup(t, open, build)
		// finish 直接引用 hi.tag：走 false 分支时 hi 没有执行，finish 失败而不是读到空值。
		g := GI([]string{"score"}, []graph.Node{
			In(N("gate", builtin.Switch, builtin.SwitchInput{Expr: "score >= 60"}), refs{"score": R(graph.InputSource, "score")}),
			op("hi", OpInput{Set: map[string]any{"tag": "hi"}}), op("lo", OpInput{Set: map[string]any{"tag": "lo"}}),
			end(refs{"tag": R("hi", "tag")})},
			E("gate", "hi", "Case0"), E("gate", "lo", "Case1"), E("hi", "finish"), E("lo", "finish"))
		s := e.done(e.start(g, map[string]any{"score": 10}, base), graph.FAILED)
		if !strings.Contains(s.Task.Error, `input "tag" unavailable`) {
			t.Fatalf("error = %q", s.Task.Error)
		}
	})

	scenario(t, "ForkJoinAll", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("fork", OpInput{}),
			op("l", OpInput{Set: map[string]any{"l": 1}, SleepMS: 20}), op("r", OpInput{Set: map[string]any{"r": 2}}),
			tpl("join", refs{"l": R("l", "l"), "r": R("r", "r")}, kv("sum", "{{l}}+{{r}}")),
			end(refs{"l": R("l", "l"), "r": R("r", "r"), "sum": R("join", "sum")})},
			E("fork", "l"), E("fork", "r"), E("l", "join"), E("r", "join"), E("join", "finish"))
		s := e.done(e.start(g, nil, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"l": 1, "r": 2, "sum": "1+2"})
		// 汇合后路径输出含 join。
		ok := false
		for _, r := range s.Runs {
			if r.Outputs["join"] != nil {
				ok = true
				break
			}
		}
		if !ok {
			t.Fatalf("join outputs missing: %+v", s.Runs)
		}
	})

	scenario(t, "MergeAny", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("fork", OpInput{}),
			op("fast", OpInput{Set: map[string]any{"w": "fast"}}), op("slow", OpInput{Set: map[string]any{"w": "slow"}, SleepMS: 300}),
			N("m", builtin.Merge, builtin.MergeInput{Mode: "any", Outputs: []builtin.MergeFirstField{
				{Name: "w", First: []graph.InputRef{R("slow", "w"), R("fast", "w")}}}}),
			end(refs{"w": R("m", "w")})},
			E("fork", "fast"), E("fork", "slow"), E("fast", "m"), E("slow", "m"), E("m", "finish"))
		s := e.done(e.start(g, nil, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"w": "fast"})
		for _, r := range s.Runs {
			if r.NodeID == "slow" && r.Status == graph.SUCCEEDED {
				t.Fatalf("losing branch still succeeded: %+v", r)
			}
		}
	})

	scenario(t, "NestedFork", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("f1", OpInput{}),
			op("a", OpInput{}), op("a1", OpInput{Set: map[string]any{"a1": 1}}), op("a2", OpInput{Set: map[string]any{"a2": 2}}),
			op("aj", OpInput{Set: map[string]any{"aj": true}}), op("b", OpInput{Set: map[string]any{"b": 3}}),
			op("j", OpInput{}), end(refs{"a1": R("a1", "a1"), "a2": R("a2", "a2"), "aj": R("aj", "aj"), "b": R("b", "b")})},
			E("f1", "a"), E("f1", "b"), E("a", "a1"), E("a", "a2"), E("a1", "aj"), E("a2", "aj"),
			E("aj", "j"), E("b", "j"), E("j", "finish"))
		s := e.done(e.start(g, nil, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"a1": 1, "a2": 2, "aj": true, "b": 3})
	})

	scenario(t, "MapReduce", func(t *testing.T) {
		e := setup(t, open, build)
		// 循环体是独立的一张图：运行输入 item / index，end 的输出即一轮结果。
		body := GI([]string{builtin.MapItemField, builtin.MapIndexField}, []graph.Node{
			tpl("body", refs{"item": R(graph.InputSource, builtin.MapItemField), "index": R(graph.InputSource, builtin.MapIndexField)}, kv("v", "{{index}}:{{item}}")),
			end(refs{"v": R("body", "v")})}, E("body", "finish"))
		g := GI([]string{"items"}, []graph.Node{
			In(N("lp", builtin.Map, builtin.MapInput{Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
			In(N("sum", builtin.Reduce, builtin.ReduceInput{Init: `""`, Expr: `Acc + Item.v`}), refs{builtin.ReduceListField: R("lp", builtin.MapResultsField)}),
			end(refs{"results": R("lp", builtin.MapResultsField), "joined": R("sum", builtin.ReduceResultField)})},
			E("lp", "sum"), E("sum", "finish"))
		s := e.done(e.start(g, map[string]any{"items": []any{"a", "b", "c"}}, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"joined": "0:a1:b2:c",
			"results": []any{map[string]any{"v": "0:a"}, map[string]any{"v": "1:b"}, map[string]any{"v": "2:c"}}})
		// 空列表不启动子任务，results 为空数组。
		s = e.done(e.start(g, map[string]any{"items": []any{}}, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"joined": "", "results": []any{}})
	})

	scenario(t, "MapParallel", func(t *testing.T) {
		e := setup(t, open, build)
		body := GI([]string{builtin.MapItemField}, []graph.Node{
			code("body", refs{"item": R(graph.InputSource, builtin.MapItemField)}, kv("y", "item * 10")),
			end(refs{"y": R("body", "y")})}, E("body", "finish"))
		g := GI([]string{"items"}, []graph.Node{
			In(N("lp", builtin.Map, builtin.MapInput{MaxParallel: 2, Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
			end(refs{"results": R("lp", builtin.MapResultsField)})},
			E("lp", "finish"))
		s := e.done(e.start(g, map[string]any{"items": []any{1, 2, 3, 4, 5}}, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"results": []any{
			map[string]any{"y": 10}, map[string]any{"y": 20}, map[string]any{"y": 30}, map[string]any{"y": 40}, map[string]any{"y": 50}}})
	})

	scenario(t, "NestedMap", func(t *testing.T) {
		e := setup(t, open, build)
		// 内层循环体需要外层下标 row：外层循环体把 $input.index 作为内层 map 的自定义输入 row 传入。
		cell := GI([]string{builtin.MapItemField, "row"}, []graph.Node{
			code("cell", refs{"item": R(graph.InputSource, builtin.MapItemField), "row": R(graph.InputSource, "row")}, kv("y", "row * 100 + item")),
			end(refs{"y": R("cell", "y")})}, E("cell", "finish"))
		row := GI([]string{builtin.MapItemField, builtin.MapIndexField}, []graph.Node{
			In(N("inner", builtin.Map, builtin.MapInput{MaxParallel: 2, Body: &cell}),
				refs{builtin.MapListField: R(graph.InputSource, builtin.MapItemField), "row": R(graph.InputSource, builtin.MapIndexField)}),
			end(refs{"results": R("inner", builtin.MapResultsField)})}, E("inner", "finish"))
		g := GI([]string{"rows"}, []graph.Node{
			In(N("outer", builtin.Map, builtin.MapInput{MaxParallel: 2, Body: &row}), refs{builtin.MapListField: R(graph.InputSource, "rows")}),
			end(refs{"results": R("outer", builtin.MapResultsField)})},
			E("outer", "finish"))
		s := e.done(e.start(g, map[string]any{"rows": []any{[]any{1, 2}, []any{3}}}, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"results": []any{
			map[string]any{"results": []any{map[string]any{"y": 1}, map[string]any{"y": 2}}},
			map[string]any{"results": []any{map[string]any{"y": 103}}},
		}})
	})

	scenario(t, "MapFailure", func(t *testing.T) {
		e := setup(t, open, build)
		// 任一轮失败即 map 节点失败；没有错误出口时 Task 失败。
		body := GI([]string{builtin.MapItemField}, []graph.Node{N("x", builtin.StopError, builtin.StopErrorInput{Message: "bad item"})})
		g := GI([]string{"items"}, []graph.Node{
			In(N("lp", builtin.Map, builtin.MapInput{MaxParallel: 2, Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
			end(nil)}, E("lp", "finish"))
		s := e.done(e.start(g, map[string]any{"items": []any{1, 2, 3}}, base), graph.FAILED)
		if !strings.Contains(s.Task.Error, "bad item") {
			t.Fatalf("error = %q", s.Task.Error)
		}
	})

	scenario(t, "SubGraph", func(t *testing.T) {
		e := setup(t, open, build)
		e.graphs["child"] = GI([]string{"name"}, []graph.Node{
			tpl("c", refs{"name": R(graph.InputSource, "name")}, kv("echo", "child:{{name}}")), end(refs{"echo": R("c", "echo")})},
			E("c", "finish"))
		g := GI([]string{"name"}, []graph.Node{
			In(N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "child"}), refs{"name": R(graph.InputSource, "name")}),
			end(refs{"out": R("sub", builtin.SubGraphOutputField), "graph_id": R("sub", builtin.SubGraphGraphIDField)})}, E("sub", "finish"))
		id := e.start(g, map[string]any{"name": "Ada"}, base)
		s := e.done(id, graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"graph_id": "child", "out": map[string]any{"echo": "child:Ada"}})
		var childTask engine.Task
		if err := e.db.Select("TaskID").Where(map[string]any{"ParentTaskID": id}).Take(&childTask).Error; err != nil {
			t.Fatalf("child task for parent %s: %v", id, err)
		}
		child := e.done(childTask.TaskID, graph.SUCCEEDED)
		if child.Task.ParentTaskID != s.Task.TaskID || child.Task.ParentRunID == "" {
			t.Fatalf("child parent = %q / %q", child.Task.ParentTaskID, child.Task.ParentRunID)
		}
	})

	scenario(t, "SubGraphFailure", func(t *testing.T) {
		e := setup(t, open, build)
		e.graphs["bad"] = G([]graph.Node{N("x", builtin.StopError, builtin.StopErrorInput{Message: "boom"}), end(nil)},
			E("x", "finish"))
		g := G([]graph.Node{N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "bad"}), end(nil)}, E("sub", "finish"))
		e.done(e.start(g, nil, base), graph.FAILED)
		// 子图声明的必填输入没有提供时，子图节点失败。
		e.graphs["needs"] = GI([]string{"name"}, []graph.Node{end(refs{"name": R(graph.InputSource, "name")})})
		g = G([]graph.Node{N("sub", builtin.SubGraph, builtin.SubGraphInput{GraphID: "needs"}), end(nil)}, E("sub", "finish"))
		s := e.done(e.start(g, nil, base), graph.FAILED)
		if !strings.Contains(s.Task.Error, `"name" required`) {
			t.Fatalf("error = %q", s.Task.Error)
		}
	})

	scenario(t, "Retry", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("a", OpInput{FailTimes: 2, Set: map[string]any{"ok": true}}), end(refs{"ok": R("a", "ok")})},
			E("a", "finish"))
		p := base
		p.MaxAttempts, p.RetryDelayMS = 3, 10
		e.done(e.start(g, nil, p), graph.SUCCEEDED)
		p.MaxAttempts = 2
		e.done(e.start(g, nil, p), graph.FAILED)
	})

	scenario(t, "ErrorEdge", func(t *testing.T) {
		e := setup(t, open, build)
		// stop_error 沿 error 出边继续。
		g := G([]graph.Node{
			N("a", builtin.StopError, builtin.StopErrorInput{Message: "boom"}),
			op("fix", OpInput{Set: map[string]any{"fixed": true}}),
			end(refs{"fixed": R("fix", "fixed"), "error": R("a", graph.ErrorField)}),
		}, E("a", "fix", graph.EndpointError), E("fix", "finish"))
		s := e.done(e.start(g, nil, base), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"error": "boom", "fixed": true})
	})

	scenario(t, "FailPolicy", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("fork", OpInput{}),
			op("bad", OpInput{FailTimes: 99}), op("good", OpInput{Set: map[string]any{"g": 1}, SleepMS: 30}),
			end(refs{"g": R("good", "g")})},
			E("fork", "bad"), E("fork", "good"), E("bad", "finish"), E("good", "finish"))
		e.done(e.start(g, nil, base), graph.FAILED)
		p := base
		p.FailPolicy = engine.FailContinue
		s := e.done(e.start(g, nil, p), graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"g": 1})
	})

	scenario(t, "Cancel", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("a", OpInput{Block: true}), end(nil)}, E("a", "finish"))
		id := e.start(g, nil, base)
		time.Sleep(50 * time.Millisecond)
		if _, err := e.signal(id, engine.Signal{Kind: engine.SignalCancel, Reason: "stop"}); err != nil {
			t.Fatal(err)
		}
		s := e.done(id, graph.CANCELED)
		for _, r := range s.Runs {
			if !graph.IsTerminal(r.Status) {
				t.Fatalf("run %s still %s", r.RunID, r.Status)
			}
		}
		// 已结束的 Task 再收到信号：不报错，回执是当前（终态）状态。
		rec, err := e.signal(id, engine.Signal{Kind: engine.SignalCancel, Reason: "again"})
		if err != nil || rec.Status != graph.CANCELED {
			t.Fatalf("second cancel = %+v, err %v; want CANCELED receipt without error", rec, err)
		}
	})

	scenario(t, "PauseResume", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("a", OpInput{SleepMS: 100}), op("b", OpInput{Set: map[string]any{"b": 1}}), end(refs{"b": R("b", "b")})},
			E("a", "b"), E("b", "finish"))
		id := e.start(g, nil, base)
		if _, err := e.signal(id, engine.Signal{Kind: engine.SignalPause}); err != nil {
			t.Fatal(err)
		}
		s := e.waiting(id)
		for _, r := range s.Runs {
			if r.NodeID == "b" && r.Status == graph.SUCCEEDED {
				t.Fatal("b ran while paused")
			}
			if out := r.Outputs["b"]; out != nil {
				t.Fatal("b produced output while paused")
			}
		}
		if _, err := e.resume(id, engine.ResumeRequest{}); err != nil {
			t.Fatal(err)
		}
		e.done(id, graph.SUCCEEDED)
	})

	scenario(t, "DebugStep", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("a", OpInput{Set: map[string]any{"a": 1}}), op("b", OpInput{Set: map[string]any{"b": 2}}),
			end(refs{"a": R("a", "a"), "b": R("b", "b")})},
			E("a", "b"), E("b", "finish"))
		p := base
		// 调试注入按输入名覆盖 finish（内联节点）的输入：b 被替换，x 为新增输入。
		p.Debug = &engine.DebugOptions{Step: true, Injects: map[string]engine.DebugInject{"finish": {Vars: map[string]any{"b": "inj", "x": "new"}}}}
		id := e.start(g, nil, p)
		for i := 0; ; i++ {
			s := e.settle(id)
			if graph.IsTerminal(s.Task.Status) {
				eq(t, s.Task.Output, map[string]any{"a": 1, "b": "inj", "x": "new"})
				// 单步只在活动节点前停住（设计文档 4.4）：入口与 end 是内联节点，不计入。
				if i != 2 {
					t.Fatalf("stepped %d times, want 2 (a, b)", i)
				}
				break
			}
			for _, r := range s.Runs {
				if r.Status == graph.WAITING && r.NodeID != "a" && r.NodeID != "b" {
					t.Fatalf("step paused at inline node %s", r.NodeID)
				}
			}
			if _, err := e.resume(id, engine.ResumeRequest{DebugAction: engine.DebugActionStep}); err != nil {
				t.Fatalf("step %d: %v", i, err)
			}
			if i > 10 {
				t.Fatal("too many steps")
			}
		}
	})

	scenario(t, "Breakpoint", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("a", OpInput{}), op("b", OpInput{Set: map[string]any{"b": 1}}), end(refs{"b": R("b", "b")})},
			E("a", "b"), E("b", "finish"))
		p := base
		p.Debug = &engine.DebugOptions{Breakpoints: []string{"b"}}
		id := e.start(g, nil, p)
		s := e.waiting(id)
		for _, r := range s.Runs {
			if r.Status == graph.WAITING && r.NodeID != "b" {
				t.Fatalf("paused at %s", r.NodeID)
			}
		}
		// 未知的调试动作在改动任何实体前拒绝，Run 仍停在断点上。
		if _, err := e.resume(id, engine.ResumeRequest{DebugAction: "later"}); !errors.Is(err, engine.ErrInvalid) {
			t.Fatalf("unknown DebugAction err = %v, want ErrInvalid", err)
		}
		if _, err := e.resume(id, engine.ResumeRequest{DebugAction: engine.DebugActionContinue}); err != nil {
			t.Fatal(err)
		}
		e.done(id, graph.SUCCEEDED)
		// 已结束的 Task 再恢复：不报错，回执是当前（终态）状态。
		rec, err := e.resume(id, engine.ResumeRequest{})
		if err != nil || rec.Status != graph.SUCCEEDED {
			t.Fatalf("resume after finish = %+v, err %v; want SUCCEEDED receipt without error", rec, err)
		}
	})

	scenario(t, "InlineBreakpoint", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("a", OpInput{}), tpl("b", nil, kv("v", "x")), end(refs{"v": R("b", "v")})}, E("a", "b"), E("b", "finish"))
		// 断点设在内联节点（template、end）上：依次在 b、finish 前停住，放行后完成执行。
		p := base
		p.Debug = &engine.DebugOptions{Breakpoints: []string{"b", "finish"}}
		id := e.start(g, nil, p)
		if s := e.waiting(id); len(s.Runs) != 1 || s.Runs[0].NodeID != "b" {
			t.Fatalf("expected pause at inline node b: %s", dump(s.Runs))
		}
		if _, err := e.resume(id, engine.ResumeRequest{DebugAction: engine.DebugActionContinue}); err != nil {
			t.Fatal(err)
		}
		if s := e.waiting(id); len(s.Runs) != 1 || s.Runs[0].NodeID != "finish" {
			t.Fatalf("expected pause at inline node finish: %s", dump(s.Runs))
		}
		if _, err := e.resume(id, engine.ResumeRequest{DebugAction: engine.DebugActionContinue}); err != nil {
			t.Fatal(err)
		}
		s := e.done(id, graph.SUCCEEDED)
		eq(t, s.Task.Output, map[string]any{"v": "x"})
	})

	scenario(t, "Watch", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{op("a", OpInput{SleepMS: 30}), op("b", OpInput{SleepMS: 30}), end(nil)},
			E("a", "b"), E("b", "finish"))
		id := e.start(g, nil, base)
		// 引擎推送完整快照；Task 结束后推送终态快照并关闭 channel。
		ch, err := e.eng.Watch(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		snaps := e.drain(ch)
		if n := len(snaps); n < 2 || snaps[n-1].Task.Status != graph.SUCCEEDED {
			t.Fatalf("watch got %d snapshots, last %+v", n, snaps)
		}
		for _, s := range snaps {
			if s.Task.TaskID != id {
				t.Fatalf("watch snapshot task = %q, want %q", s.Task.TaskID, id)
			}
		}
		// 订阅已结束的 Task：只收到一个终态快照，随后 channel 关闭。
		again, err := e.eng.Watch(e.ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		snaps = e.drain(again)
		if len(snaps) != 1 || snaps[0].Task.Status != graph.SUCCEEDED {
			t.Fatalf("resubscribe got %+v, want one SUCCEEDED snapshot", snaps)
		}
		// 不存在的 Task 不能订阅。
		if _, err := e.eng.Watch(e.ctx, "missing"); !errors.Is(err, engine.ErrNotFound) {
			t.Fatalf("watch missing err = %v, want ErrNotFound", err)
		}
	})

	scenario(t, "List", func(t *testing.T) {
		e := setup(t, open, build)
		g := G([]graph.Node{end(nil)})
		id := e.start(g, nil, base)
		e.done(id, graph.SUCCEEDED)
		rows, err := e.eng.List(e.ctx, engine.ListQuery{GraphID: "g"})
		if err != nil || len(rows) != 1 || rows[0].TaskID != id {
			t.Fatalf("list = %+v, err %v", rows, err)
		}
		if _, err := e.eng.Snapshot(e.ctx, "missing"); !errors.Is(err, engine.ErrNotFound) {
			t.Fatalf("snapshot missing err = %v, want ErrNotFound", err)
		}
	})
}

// Bench 对 build 构造的引擎运行基准：10 个活动节点的线性图、200 项的并行 Map。
// 每个子基准调用一次 open 取得独立的空库。
func Bench(b *testing.B, open OpenDB, build engine.Builder) {
	var chain []graph.Node
	var edges []graph.Edge
	prev := R(graph.InputSource, "x")
	for i := range 10 {
		id := fmt.Sprintf("n%d", i)
		chain = append(chain, code(id, refs{"x": prev}, kv("x", "x + 1")))
		if i > 0 {
			edges = append(edges, E(prev.From, id))
		}
		prev = R(id, "x")
	}
	chain = append(chain, end(refs{"x": prev}))
	edges = append(edges, E(prev.From, "finish"))
	items := make([]any, 200)
	for i := range items {
		items[i] = i
	}
	body := GI([]string{builtin.MapItemField}, []graph.Node{
		code("body", refs{"item": R(graph.InputSource, builtin.MapItemField)}, kv("y", "item * 2")),
		end(refs{"y": R("body", "y")})}, E("body", "finish"))
	cases := []struct {
		name  string
		g     graph.Graph
		input map[string]any
	}{
		{"Linear10", GI([]string{"x"}, chain, edges...), map[string]any{"x": 0}},
		{"MapParallel200", GI([]string{"items"}, []graph.Node{
			In(N("lp", builtin.Map, builtin.MapInput{MaxParallel: 16, Body: &body}), refs{builtin.MapListField: R(graph.InputSource, "items")}),
			end(refs{"results": R("lp", builtin.MapResultsField)})},
			E("lp", "finish")),
			map[string]any{"items": items}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			e := setupB(b, open, build)
			policy := engine.TaskPolicy{ExecutionMode: graph.ExecParallel}
			b.ResetTimer()
			for b.Loop() {
				rec, err := e.eng.Start(e.ctx, engine.StartRequest{GraphID: "bench", Graph: c.g, Input: c.input, Policy: policy})
				if err != nil {
					b.Fatal(err)
				}
				for {
					s, err := e.eng.Snapshot(e.ctx, rec.TaskID)
					if err != nil {
						b.Fatal(err)
					}
					if graph.IsTerminal(s.Task.Status) {
						if s.Task.Status != graph.SUCCEEDED {
							b.Fatalf("task %s: %s", s.Task.Status, s.Task.Error)
						}
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
		})
	}
}

// setupB 是 setup 的基准版本。
func setupB(b *testing.B, open OpenDB, build engine.Builder) *env {
	b.Helper()
	db := open(b)
	reg, err := Registry(OpSpec())
	if err != nil {
		b.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	e := &env{ctx: ctx, graphs: map[string]graph.Graph{}}
	eng, err := build(ctx, engine.EngineConfig{DB: db, Nodes: reg, ResolveGraph: e.resolve})
	if err != nil {
		cancel()
		b.Fatal(err)
	}
	e.eng = eng
	b.Cleanup(func() {
		cancel()
		_ = eng.Close(context.Background())
	})
	return e
}
