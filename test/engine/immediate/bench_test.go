package immediate_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/engine/kernel"
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

// BenchmarkExecute 衡量纯内存模式下 immediate.Execute 端到端的开销（含引擎构造、编译、调度、节点执行）。
// 与 BenchmarkCompile、BenchmarkExecutorOnly 对照，可以扣除编译和节点本身的开销，得到调度开销。
//
// BenchmarkExecute measures end-to-end in-memory execution; subtract BenchmarkCompile and
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

// BenchmarkCompile 衡量编译一张图的开销（Execute 每次新建引擎，编译缓存不复用，所以每次都会编译）。
// BenchmarkCompile measures graph compilation, which Execute pays on every call.
func BenchmarkCompile(b *testing.B) {
	reg, _ := service.NewNodeRegistry()
	for _, n := range []int{10, 100} {
		g := linearGraph(n)
		b.Run(fmt.Sprintf("Linear%d", n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := kernel.Compile(&g, reg); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
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
