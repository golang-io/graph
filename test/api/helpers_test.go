package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/api"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/durable"
	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/service"
	"github.com/golang-io/graph/test"
)

// startTestRuntime 在一个临时 sqlite 上启动完整 Runtime（定义库 + immediate + durable），并写入样例图。
func startTestRuntime(t *testing.T) *service.Runtime {
	t.Helper()
	ctx := context.Background()
	rt, err := service.Start(ctx, service.Options{
		DB:       test.MustOpen(t, filepath.Join(t.TempDir(), "graph.db")),
		Builders: []engine.Builder{immediate.Build, durable.With(durable.Options{PollInterval: 5 * time.Millisecond})},
		Nodes:    []node.Node{builtin.HTTPNode{}, builtin.LLMNode{}},
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close(context.Background()) })
	if err := service.ExampleGraph(ctx, rt.Store); err != nil {
		t.Fatalf("ExampleGraph: %v", err)
	}
	return rt
}

// client 是测试用的 HTTP 调用封装：call 发请求并按状态码断言，返回响应体。
type client struct {
	t *testing.T
	h http.Handler
	p string // 路径前缀
}

func newClient(t *testing.T, rt *service.Runtime, prefix string) client {
	return client{t: t, h: api.Handler(rt, prefix), p: prefix}
}

func (c client) call(method, path, body string, wantCode int, out any) []byte {
	c.t.Helper()
	var r io.Reader
	if body != "" {
		r = bytes.NewBufferString(body)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, httptest.NewRequest(method, c.p+path, r))
	if w.Code != wantCode {
		c.t.Fatalf("%s %s: code=%d want %d body=%s", method, path, w.Code, wantCode, w.Body)
	}
	if out != nil {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			c.t.Fatalf("%s %s: decode %T: %v body=%s", method, path, out, err, w.Body)
		}
	}
	return w.Body.Bytes()
}

// waitTask 轮询 GET /tasks/{id} 直到状态为 want。
func (c client) waitTask(id string, want graph.StatusCode) engine.TaskSnapshot {
	c.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var snap engine.TaskSnapshot
		c.call(http.MethodGet, "/tasks/"+id, "", http.StatusOK, &snap)
		if snap.Task.Status == want {
			return snap
		}
		if time.Now().After(deadline) || (graph.IsTerminal(snap.Task.Status) && !graph.IsTerminal(want)) {
			c.t.Fatalf("task %s status=%s want %s err=%s", id, snap.Task.Status, want, snap.Task.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// listOf 是列表响应 {Data} 的解码目标。
type listOf[T any] struct {
	Data []T
}

// apiError 是错误响应 {Error, Code} 的解码目标。
type apiError struct {
	Error string
	Code  string
}
