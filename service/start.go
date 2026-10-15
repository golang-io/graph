package service

import (
	"context"
	"fmt"

	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
	"gorm.io/gorm"
)

// Options 装配定义库、节点与引擎。
//
// DB 同时用作定义库与执行库（两个引擎共用 tb_graph_task / tb_graph_run）；
// 必须用 GormConfig 打开。需要把引擎放在其他库时，自行构造引擎并通过 Engines 传入。
type Options struct {
	DB       *gorm.DB
	Nodes    []node.Node      // 额外的节点（核心节点总是装配）
	Builders []engine.Builder // 由 DB 构造的引擎
	Engines  []engine.Engine  // 已构造好的引擎
	Observer engine.Observer
}

// Start 检查数据库连接，打开定义库并构造、注册引擎。
// 引擎的后台调度（如 durable 的 worker 与恢复扫描）由各引擎在构造时自行启动，Runtime.Close 时停止。
func Start(ctx context.Context, opt Options) (_ *Runtime, err error) {
	if opt.DB == nil {
		return nil, fmt.Errorf("%w: Options.DB required", engine.ErrInvalid)
	}
	if err := engine.Ping(ctx, opt.DB); err != nil {
		return nil, fmt.Errorf("graph: ping database: %w", err)
	}
	nodes, err := NewNodeRegistry(opt.Nodes...)
	if err != nil {
		return nil, err
	}
	store, err := OpenDB(ctx, opt.DB, nodes)
	if err != nil {
		return nil, err
	}
	engines := NewEngineRegistry(opt.DB)
	defer func() {
		if err != nil {
			_ = engines.Close(ctx)
		}
	}()

	cfg := engine.EngineConfig{
		DB:           opt.DB,
		Nodes:        nodes,
		ResolveGraph: store.ResolveGraph,
		Observer:     opt.Observer,
	}
	for _, build := range opt.Builders {
		if build == nil {
			return nil, fmt.Errorf("%w: engine builder required", engine.ErrInvalid)
		}
		eng, err := build(ctx, cfg)
		if err != nil {
			return nil, fmt.Errorf("graph: open engine: %w", err)
		}
		if err := engines.Register(eng); err != nil {
			_ = eng.Close(ctx)
			return nil, err
		}
	}
	for _, extra := range opt.Engines {
		if err := engines.Register(extra); err != nil {
			return nil, err
		}
	}
	return &Runtime{Store: store, Engines: engines}, nil
}
