package service_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/golang-io/graph/service"
	"github.com/golang-io/graph/test"
)

// withEndpointSides 为定义的画布布局补齐每个节点的句柄朝向（保存要求），已写入的朝向保持不变。
func withEndpointSides(def service.Definition) service.Definition {
	def.UI = test.WithEndpointSides(def.Graph, def.UI)
	return def
}

// startTestRuntime 在临时库上启动完整 Runtime（定义库 + opt.Builders 指定的引擎）。
func startTestRuntime(t *testing.T, ctx context.Context, opt service.Options) (*service.Runtime, error) {
	t.Helper()
	opt.DB = test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db"))
	return service.Start(ctx, opt)
}
