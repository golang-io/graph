package immediate_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strconv"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/example"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/service"
	"github.com/golang-io/graph/test/engine/enginetest"
)

// linearGraph 构造 n 个 code 节点串联的线性图：每个节点执行 x = x + 1。
// linearGraph builds a chain of n code nodes, each computing x = x + 1.
// 第一个节点的输入 x 引用运行输入，之后每个节点引用上一个节点的输出 x。
func linearGraph(n int) graph.Graph {
	var nodes []graph.Node
	var edges []graph.Edge
	prev := enginetest.R(graph.InputSource, "x")
	for i := range n {
		id := fmt.Sprintf("n%d", i)
		nodes = append(nodes, enginetest.In(enginetest.N(id, builtin.Code, builtin.CodeInput{Outputs: []graph.OutKV{{Key: "x", Value: "x + 1"}}}),
			map[string]graph.InputRef{"x": prev}))
		if i > 0 {
			edges = append(edges, enginetest.E(prev.From, id))
		}
		prev = enginetest.R(id, "x")
	}
	nodes = append(nodes, enginetest.In(enginetest.N("finish", builtin.End, nil), map[string]graph.InputRef{"x": prev}))
	edges = append(edges, enginetest.E(prev.From, "finish"))
	return enginetest.GI([]string{"x"}, nodes, edges...)
}

// mapParallelGraph 构造并行 Map：items 共 n 项，最多 16 个迭代（子任务）同时执行，循环体执行 y = item * 2。
func mapParallelGraph() graph.Graph {
	body := enginetest.GI([]string{builtin.MapItemField}, []graph.Node{
		enginetest.In(enginetest.N("body", builtin.Code, builtin.CodeInput{Outputs: []graph.OutKV{{Key: "y", Value: "item * 2"}}}),
			map[string]graph.InputRef{"item": enginetest.R(graph.InputSource, builtin.MapItemField)}),
		enginetest.In(enginetest.N("finish", builtin.End, nil),
			map[string]graph.InputRef{"y": enginetest.R("body", "y")}),
	}, enginetest.E("body", "finish"))
	return enginetest.GI([]string{"items"}, []graph.Node{
		enginetest.In(enginetest.N("lp", builtin.Map, builtin.MapInput{MaxParallel: 16, Body: &body}),
			map[string]graph.InputRef{builtin.MapListField: enginetest.R(graph.InputSource, "items")}),
		enginetest.In(enginetest.N("finish", builtin.End, nil),
			map[string]graph.InputRef{"results": enginetest.R("lp", builtin.MapResultsField)}),
	}, enginetest.E("lp", "finish"))
}

// BenchmarkExecute 衡量纯内存模式下 immediate.Execute 端到端的开销（含引擎构造、校验、调度、节点执行）。
// 与 BenchmarkValidate、BenchmarkExecutorOnly 对照，可以扣除校验和节点本身的开销，得到调度开销。
//
// BenchmarkExecute measures end-to-end in-memory execution; subtract BenchmarkValidate and
// BenchmarkExecutorOnly to estimate the scheduling overhead.
func BenchmarkExecute(b *testing.B) {
	reg, _ := service.NewNodeRegistry()
	cfg := engine.EngineConfig{Nodes: reg}
	items := make([]any, 200)
	for i := range items {
		items[i] = i
	}
	cases := []struct {
		name   string
		g      graph.Graph
		input  map[string]any
		policy engine.TaskPolicy
	}{
		{"Linear10", linearGraph(10), map[string]any{"x": 0}, engine.TaskPolicy{}},
		{"Linear100", linearGraph(100), map[string]any{"x": 0}, engine.TaskPolicy{}},
		{"MapParallel200", mapParallelGraph(), map[string]any{"items": items},
			engine.TaskPolicy{ExecutionMode: graph.ExecParallel}},
		{"DemoPerf1000_Parallel", perf1000Graph(b), map[string]any{},
			engine.TaskPolicy{ExecutionMode: graph.ExecParallel}},
		{"DemoPerf1000_Serial", perf1000Graph(b), map[string]any{},
			engine.TaskPolicy{ExecutionMode: graph.ExecSerial}},
	}
	for _, c := range cases {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				s, err := immediate.Execute(context.Background(), cfg, engine.StartRequest{Graph: c.g, Input: c.input, Policy: c.policy})
				if err != nil || s.Task.Status != graph.SUCCEEDED {
					b.Fatal(err, s.Task.Status, s.Task.Error)
				}
			}
		})
	}
}

// BenchmarkValidate 衡量校验一张图的开销（Start 每次都会校验图定义）。
// BenchmarkValidate measures graph validation, which Start pays on every call.
func BenchmarkValidate(b *testing.B) {
	reg, _ := service.NewNodeRegistry()
	for _, n := range []int{10, 100} {
		g := linearGraph(n)
		b.Run(fmt.Sprintf("Linear%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if err := reg.Validate(g); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	g1000 := perf1000Graph(b)
	b.Run("DemoPerf1000", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if err := reg.Validate(g1000); err != nil {
				b.Fatal(err)
			}
		}
	})
}

// BenchmarkExecutorOnly 直接按顺序调用 n 次 code 执行器，不经过引擎，是节点本身开销的下限。
// BenchmarkExecutorOnly calls the code executor n times directly: the lower bound without any engine.
func BenchmarkExecutorOnly(b *testing.B) {
	reg, _ := service.NewNodeRegistry()
	for _, n := range []int{10, 100} {
		g := linearGraph(n)
		exec, ok := reg.ExecutorOf(builtin.Code)
		if !ok {
			b.Fatal("code executor missing")
		}
		cfg := builtin.CodeInput{Outputs: []graph.OutKV{{Key: "x", Value: "x + 1"}}}
		b.Run(fmt.Sprintf("Linear%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				vars := map[string]any{"x": int64(0)}
				// 前 n 个是 code 节点，最后一个是 end；每个节点的输出 {x} 即下一个节点的输入。
				for _, nd := range g.Nodes[:n] {
					v := map[string]any{"x": vars["x"]}
					out, err := exec.Execute(context.Background(), node.NodeInput{
						NodeID: nd.ID, Vars: v, Params: cfg,
					})
					if err != nil {
						b.Fatal(err)
					}
					vars = out.Set
				}
			}
		})
	}
}

// perf1000Graph 加载内置的 1000 节点综合压测图（example/demo_perf_1000.json）。
// perf1000Graph loads the built-in 1000-node benchmark graph (example/demo_perf_1000.json).
func perf1000Graph(tb testing.TB) graph.Graph {
	tb.Helper()
	raw, err := example.FS.ReadFile("demo_perf_1000.json")
	if err != nil {
		tb.Fatal(err)
	}
	var doc struct{ Graph graph.Graph }
	if err := json.Unmarshal(raw, &doc); err != nil {
		tb.Fatal(err)
	}
	return doc.Graph
}

// TestProfileDemoPerf1000 连续运行 100 次（可通过环境变量 GRAPH_PERF_RUNS 调整）1000 节点压测图，
// 并使用 runtime/pprof 采集 CPU 与内存（heap / allocs）采样文件。
// 默认在测试构建时若传入了 -short 则仅运行 1 次验证通路；显式运行本用例或设置 GRAPH_PPROF_DIR 时写采样文件。
//
// TestProfileDemoPerf1000 runs the 1000-node graph 100 times while recording CPU and memory pprof profiles.
func TestProfileDemoPerf1000(t *testing.T) {
	runs := 100
	if testing.Short() {
		runs = 1
	}
	if v := os.Getenv("GRAPH_PERF_RUNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			runs = n
		}
	}

	outDir := os.Getenv("GRAPH_PPROF_DIR")
	if outDir == "" {
		outDir = t.TempDir()
	} else if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cpuPath := filepath.Join(outDir, "demo_perf_1000_cpu.pprof")
	memPath := filepath.Join(outDir, "demo_perf_1000_mem.pprof")

	reg, err := service.NewNodeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	cfg := engine.EngineConfig{Nodes: reg}
	g := perf1000Graph(t)
	req := engine.StartRequest{
		Graph:  g,
		Input:  map[string]any{},
		Policy: engine.TaskPolicy{ExecutionMode: graph.ExecParallel},
	}

	// 启动 CPU pprof 采样（若外部 go test -cpuprofile 已启动则跳过内部重复启动）。
	// Start CPU profiling unless already active via go test -cpuprofile.
	cpuFile, err := os.Create(cpuPath)
	if err != nil {
		t.Fatal(err)
	}
	cpuStarted := pprof.StartCPUProfile(cpuFile) == nil

	var beforeMem, afterMem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&beforeMem)
	start := time.Now()

	for i := range runs {
		s, err := immediate.Execute(context.Background(), cfg, req)
		if err != nil || s.Task.Status != graph.SUCCEEDED {
			if cpuStarted {
				pprof.StopCPUProfile()
			}
			_ = cpuFile.Close()
			t.Fatalf("run %d: err=%v status=%s taskErr=%s", i, err, s.Task.Status, s.Task.Error)
		}
	}

	elapsed := time.Since(start)
	runtime.ReadMemStats(&afterMem)

	if cpuStarted {
		pprof.StopCPUProfile()
	}
	_ = cpuFile.Close()

	// 采集内存 pprof（包含累计分配 alloc_space / alloc_objects 与当前存活 inuse_space / inuse_objects）。
	// Write heap profile containing both cumulative allocations and live in-use objects.
	memFile, err := os.Create(memPath)
	if err != nil {
		t.Fatal(err)
	}
	runtime.GC()
	if err := pprof.WriteHeapProfile(memFile); err != nil {
		_ = memFile.Close()
		t.Fatal(err)
	}
	_ = memFile.Close()

	totalAllocMB := float64(afterMem.TotalAlloc-beforeMem.TotalAlloc) / (1024 * 1024)
	mallocs := afterMem.Mallocs - beforeMem.Mallocs
	t.Logf("demo_perf_1000 x %d finished in %v (avg %v/run), total_alloc=%.2f MB (%.2f MB/run), mallocs=%d (%d/run), cpu=%s, mem=%s",
		runs, elapsed, elapsed/time.Duration(runs),
		totalAllocMB, totalAllocMB/float64(runs),
		mallocs, mallocs/uint64(runs),
		cpuPath, memPath)
}
