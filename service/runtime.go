// Package service 提供定义版本管理、节点装配、引擎注册与路由，以及启动生命周期。
// 执行模型与 Engine 接口见 engine 包。
//
// Package service holds the definition store, node assembly, engine routing and lifecycle.
package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
)

// Runtime 是一次 Start 得到的组合：定义库 Store（内含 node.Registry）与引擎表 Engines。
// 节点注册表经 Store.Nodes() 访问；引擎控制面经 Engines（或下方转发方法）访问。
type Runtime struct {
	Store   *Store
	Engines *EngineRegistry
}

// Close 关闭所有引擎（各引擎按自己的语义停止后台调度、处理执行中的 Task）；DB 连接由业务应用关闭。
func (rt *Runtime) Close(ctx context.Context) error {
	return rt.Engines.Close(ctx)
}

// SetGraphEngine 确认 engineKind 已注册后，更新图的默认执行引擎。
func (rt *Runtime) SetGraphEngine(ctx context.Context, graphID, engineKind string) (*Definition, error) {
	engineKind = strings.TrimSpace(engineKind)
	if engineKind != "" {
		if _, err := rt.Engines.Engine(engineKind); err != nil {
			return nil, fmt.Errorf("%w: engine %q is not registered", engine.ErrInvalid, engineKind)
		}
	}
	return rt.Store.SetEngine(ctx, graphID, engineKind)
}

// Start 按 GraphID + GraphVersion 加载定义后启动 Task；未指定 GraphVersion 时绑定当前已保存版本。
// 请求自带 Graph 且未指定 GraphVersion 时直接执行该图（临时图，不要求已保存，GraphVersion 为 0）。
func (rt *Runtime) Start(ctx context.Context, req engine.StartRequest) (engine.Receipt, error) {
	req.GraphID = strings.TrimSpace(req.GraphID)
	if req.GraphVersion < 0 {
		return engine.Receipt{}, fmt.Errorf("%w: negative GraphVersion", engine.ErrInvalid)
	}
	if req.GraphVersion > 0 || len(req.Graph.Nodes) == 0 {
		var def *Definition
		var err error
		if req.GraphVersion > 0 {
			def, err = rt.Store.GetVersion(ctx, req.GraphID, req.GraphVersion)
		} else {
			def, err = rt.Store.EnsureRunVersion(ctx, req.GraphID)
		}
		if err != nil {
			return engine.Receipt{}, err
		}
		if len(req.Graph.Nodes) > 0 {
			// 请求中 Graph 自带的身份不参与核对：设为该版本的身份后只比较内容。
			cand := req.Graph
			cand.GraphID, cand.GraphVersion = def.Graph.GraphID, def.Graph.GraphVersion
			if !sameGraph(cand, def.Graph) {
				return engine.Receipt{}, fmt.Errorf("%w: Graph does not match GraphVersion", engine.ErrInvalid)
			}
		}
		// 引擎按 req.PinnedGraph() 把图身份设为 (GraphID, def.Version)，与定义表的列一致。
		req.Graph, req.GraphVersion = def.Graph, def.Version
	}
	// 先加载定义再选引擎：图不存在时返回 ErrNotFound，而不是「缺少 Engine」。
	kind := strings.TrimSpace(req.Engine)
	if kind == "" {
		kind = rt.Store.EngineFor(ctx, req.GraphID)
	}
	if kind == "" {
		return engine.Receipt{}, fmt.Errorf("%w: Engine required", engine.ErrInvalid)
	}
	return rt.Engines.Start(ctx, kind, req)
}

// sameGraph 报告两张执行图是否相同（含 GraphID / GraphVersion；节点、边顺序不同也算相同），
// 两边都由 graph.HashDefinition 实时计算。任一侧无法规范化时视为不同。
func sameGraph(a, b graph.Graph) bool {
	if a.GraphID != b.GraphID || a.GraphVersion != b.GraphVersion {
		return false
	}
	if len(a.Nodes) != len(b.Nodes) || len(a.Edges) != len(b.Edges) {
		return false
	}
	_, ha, err := graph.HashDefinition(a)
	if err != nil {
		return false
	}
	_, hb, err := graph.HashDefinition(b)
	return err == nil && ha == hb
}

// ListTasks 汇总已注册引擎的 Task 列表，按 StartedAt 倒序（相同时按 TaskID 倒序）截断。
// 分页游标 query.After 在此处前置解析为确定的 (AfterAt, After) 时间戳游标再传给各引擎；
// 若 After 在所有引擎与执行表中都不存在，直接返回 ErrNotFound，绝不静默返回第一页。
//
// ListTasks aggregates root tasks across registered engines, resolving the After cursor once up front.
func (rt *Runtime) ListTasks(ctx context.Context, query engine.ListQuery) ([]engine.TaskSummary, error) {
	if query.Limit <= 0 {
		query.Limit = 50
	}
	query.After = strings.TrimSpace(query.After)
	if query.After != "" && query.AfterAt.IsZero() {
		at, err := rt.Engines.cursor(ctx, query.After)
		if err != nil {
			return nil, err
		}
		query.AfterAt = at
	}
	var all []engine.TaskSummary
	seen := make(map[string]bool)
	for _, kind := range rt.Engines.Kinds() {
		eng, _ := rt.Engines.Engine(kind)
		rows, err := eng.List(ctx, query)
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if !seen[r.TaskID] {
				seen[r.TaskID] = true
				all = append(all, r)
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool {
		if !all[i].StartedAt.Equal(all[j].StartedAt) {
			return all[i].StartedAt.After(all[j].StartedAt)
		}
		return all[i].TaskID > all[j].TaskID
	})
	if len(all) > query.Limit {
		all = all[:query.Limit]
	}
	return all, nil
}
