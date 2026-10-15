package api_test

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/api"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/service"
)

// TestAPIGraphs 覆盖图定义接口：列表、详情、创建、保存、版本、默认引擎、删除、节点注册表、引擎列表。
func TestAPIGraphs(t *testing.T) {
	c := newClient(t, startTestRuntime(t), "/api")

	var graphs listOf[service.Definition]
	c.call(http.MethodGet, "/graphs", "", http.StatusOK, &graphs)
	if len(graphs.Data) == 0 {
		t.Fatalf("graphs=%+v", graphs)
	}
	var tour service.Definition
	c.call(http.MethodGet, "/graphs/demo_tour", "", http.StatusOK, &tour)
	if tour.GraphID != "demo_tour" || tour.Version == 0 || len(tour.Graph.Nodes) == 0 {
		t.Fatalf("tour=%+v", tour)
	}

	var created service.Definition
	c.call(http.MethodPost, "/graphs", `{"Name":"Test Graph","Description":"Created by test"}`, http.StatusCreated, &created)
	if created.GraphID == "" || created.Name != "Test Graph" {
		t.Fatalf("created=%+v", created)
	}
	// 画布布局缺少端点所在边时保存失败：每个节点的每个端点都必须在 EndpointSides 中写明。
	noSides, _ := json.Marshal(service.Definition{Name: "Updated", Graph: tour.Graph})
	var rejected apiError
	c.call(http.MethodPut, "/graphs/"+created.GraphID, string(noSides), http.StatusBadRequest, &rejected)
	if rejected.Code != "Invalid" || !strings.Contains(rejected.Error, "endpoint sides required") {
		t.Fatalf("missing endpoint sides: %+v", rejected)
	}
	body, _ := json.Marshal(service.Definition{Name: "Updated", Graph: tour.Graph, UI: tour.UI})
	var saved service.Definition
	c.call(http.MethodPut, "/graphs/"+created.GraphID, string(body), http.StatusOK, &saved)
	if saved.GraphID != created.GraphID || saved.Name != "Updated" || saved.Version <= created.Version {
		t.Fatalf("saved=%+v created=%+v", saved, created)
	}
	var versions listOf[service.Definition]
	c.call(http.MethodGet, "/graphs/"+created.GraphID+"/versions?Limit=10", "", http.StatusOK, &versions)
	// Create 得到 Version=0 的草稿，第一次保存产生 Version=1。
	if created.Version != 0 || len(versions.Data) != 1 || versions.Data[0].Version != saved.Version {
		t.Fatalf("created=%d versions=%+v", created.Version, versions)
	}
	var first service.Definition
	c.call(http.MethodGet, fmt.Sprintf("/graphs/%s/versions/%d", created.GraphID, saved.Version), "", http.StatusOK, &first)
	if first.Version != saved.Version || first.Name != "Updated" {
		t.Fatalf("version=%+v", first)
	}
	var withEngine service.Definition
	c.call(http.MethodPut, "/graphs/"+created.GraphID+"/engine", `{"Engine":"durable"}`, http.StatusOK, &withEngine)
	if withEngine.Engine != "durable" {
		t.Fatalf("engine=%q", withEngine.Engine)
	}
	c.call(http.MethodDelete, "/graphs/"+created.GraphID, "", http.StatusNoContent, nil)
	c.call(http.MethodGet, "/graphs/"+created.GraphID, "", http.StatusNotFound, nil)

	var specs listOf[map[string]any]
	c.call(http.MethodGet, "/node-specs", "", http.StatusOK, &specs)
	if len(specs.Data) == 0 {
		t.Fatal("empty node specs")
	}
	var engines listOf[string]
	c.call(http.MethodGet, "/engines", "", http.StatusOK, &engines)
	if !slices.Equal(engines.Data, []string{"durable", "immediate"}) {
		t.Fatalf("engines=%v", engines.Data)
	}
	var examples listOf[string]
	c.call(http.MethodPost, "/graphs/examples", "", http.StatusOK, &examples)
	if !slices.Contains(examples.Data, "demo_tour") {
		t.Fatalf("examples=%v", examples.Data)
	}
}

// TestAPITaskLifecycle 在两个引擎上跑同一张样例图：启动、快照、列表、watch。
func TestAPITaskLifecycle(t *testing.T) {
	rt := startTestRuntime(t)
	c := newClient(t, rt, "/api")
	for _, kind := range []string{"immediate", "durable"} {
		t.Run(kind, func(t *testing.T) {
			c.t = t
			var rec engine.Receipt
			c.call(http.MethodPost, "/tasks", fmt.Sprintf(`{"GraphID":"demo_code","Engine":%q,"Input":{"a":3,"b":8,"name":"Ada"}}`, kind), http.StatusAccepted, &rec)
			if rec.TaskID == "" || rec.EngineKind != kind || rec.Version == 0 {
				t.Fatalf("receipt=%+v", rec)
			}
			snap := c.waitTask(rec.TaskID, graph.SUCCEEDED)
			if snap.Task.Output["label"] != "Ada:11" || len(snap.Task.Graph.Nodes) == 0 || len(snap.Runs) == 0 {
				t.Fatalf("snapshot=%+v runs=%d", snap.Task, len(snap.Runs))
			}

			var tasks listOf[engine.TaskSummary]
			c.call(http.MethodGet, "/tasks?GraphID=demo_code&Status=SUCCEEDED&Limit=50", "", http.StatusOK, &tasks)
			if !slices.ContainsFunc(tasks.Data, func(s engine.TaskSummary) bool { return s.TaskID == rec.TaskID }) {
				t.Fatalf("task %s missing from list %+v", rec.TaskID, tasks.Data)
			}

			watched := watchAll(t, rt, "/tasks/"+rec.TaskID+"/watch")
			if watched.Task.Status != graph.SUCCEEDED || len(watched.Runs) == 0 {
				t.Fatalf("watch status=%s runs=%d", watched.Task.Status, len(watched.Runs))
			}
		})
	}
}

// TestAPIBreakpointResumeAndCancel 覆盖断点等待 / 恢复 / 暂停 / 取消，以及终态后再控制返回 202 与当前状态。
// 断点设在 calc 节点前，Task 进入 WAITING(debug)。
func TestAPIBreakpointResumeAndCancel(t *testing.T) {
	c := newClient(t, startTestRuntime(t), "")
	for _, kind := range []string{"immediate", "durable"} {
		t.Run(kind, func(t *testing.T) {
			c.t = t
			start := fmt.Sprintf(`{"GraphID":"demo_code","Engine":%q,"Input":{"a":3,"b":8,"name":"Ada"},`+
				`"Policy":{"Debug":{"Breakpoints":["calc"]}}}`, kind)
			var rec engine.Receipt
			c.call(http.MethodPost, "/tasks", start, http.StatusAccepted, &rec)
			c.waitTask(rec.TaskID, graph.WAITING)
			c.call(http.MethodPost, "/tasks/"+rec.TaskID+"/resume", "", http.StatusAccepted, nil)
			if done := c.waitTask(rec.TaskID, graph.SUCCEEDED); done.Task.Output["label"] != "Ada:11" {
				t.Fatalf("output=%v", done.Task.Output)
			}
			// 已结束的 Task 再控制：202，回执为当前终态。
			var after engine.Receipt
			c.call(http.MethodPost, "/tasks/"+rec.TaskID+"/resume", "", http.StatusAccepted, &after)
			if after.TaskID != rec.TaskID || after.Status != graph.SUCCEEDED {
				t.Fatalf("resume after finish receipt=%+v", after)
			}

			c.call(http.MethodPost, "/tasks", start, http.StatusAccepted, &rec)
			c.waitTask(rec.TaskID, graph.WAITING)
			c.call(http.MethodPost, "/tasks/"+rec.TaskID+"/pause", "", http.StatusAccepted, nil)
			c.call(http.MethodPost, "/tasks/"+rec.TaskID+"/cancel", `{"Reason":"user canceled"}`, http.StatusAccepted, nil)
			if s := c.waitTask(rec.TaskID, graph.CANCELED); !strings.Contains(s.Task.Error, "user canceled") {
				t.Fatalf("cancel error=%q", s.Task.Error)
			}
			c.call(http.MethodPost, "/tasks/"+rec.TaskID+"/resume", "", http.StatusAccepted, &after)
			if after.Status != graph.CANCELED {
				t.Fatalf("resume after cancel receipt=%+v", after)
			}
		})
	}
}

// TestAPIServicePrefix 确认前缀 /api/graph 下路由完整可用。
func TestAPIServicePrefix(t *testing.T) {
	c := newClient(t, startTestRuntime(t), "/api/graph")
	c.call(http.MethodGet, "/graphs", "", http.StatusOK, nil)
	c.call(http.MethodGet, "/engines", "", http.StatusOK, nil)
	var rec engine.Receipt
	c.call(http.MethodPost, "/tasks", `{"GraphID":"demo_tour","Engine":"immediate","Input":{"name":"Ada","score":40,"items":["a"]}}`, http.StatusAccepted, &rec)
	c.waitTask(rec.TaskID, graph.SUCCEEDED)
	c.call(http.MethodGet, "/tasks", "", http.StatusOK, nil)
}

// TestAPIStartTaskKeepsPolicy 确认 POST /tasks 携带的 Policy 原样写入 durable 任务。
func TestAPIStartTaskKeepsPolicy(t *testing.T) {
	c := newClient(t, startTestRuntime(t), "")
	var rec engine.Receipt
	c.call(http.MethodPost, "/tasks", `{"GraphID":"demo_code","Engine":"durable","Input":{"a":3,"b":8,"name":"Ada"},`+
		`"Policy":{"FailPolicy":"continue","MaxAttempts":3,"RetryDelayMS":456}}`, http.StatusAccepted, &rec)
	p := c.waitTask(rec.TaskID, graph.SUCCEEDED).Task.Policy
	if p.FailPolicy != "continue" || p.MaxAttempts != 3 || p.RetryDelayMS != 456 {
		t.Fatalf("policy=%+v", p)
	}
}

// watchAll 通过真实 HTTP 连接读取 SSE，直到服务端在终态后结束流。
// 事件按增量合并，返回订阅方最终看到的快照。
func watchAll(t *testing.T, rt *service.Runtime, path string) engine.TaskSnapshot {
	t.Helper()
	srv := httptest.NewServer(api.Handler(rt))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+path, nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("content-type=%s", res.Header.Get("Content-Type"))
	}
	var out engine.TaskSnapshot
	n := 0
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 1<<20), 1<<22)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: error"):
			t.Fatalf("watch error event")
		case strings.HasPrefix(line, "data: "):
			var ev service.WatchEvent
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev); err != nil {
				t.Fatal(err)
			}
			if n == 0 && ev.Snapshot == nil {
				t.Fatal("first watch event is not a full snapshot")
			}
			if n > 0 && ev.Snapshot != nil {
				t.Fatal("later watch event repeated the full snapshot")
			}
			out = service.ApplyWatch(&out, ev)
			n++
		}
	}
	// Scan 在 EOF、I/O 错误、单行超过缓冲区时都返回 false。
	// Err 只在非 EOF 失败时非空；上下文已超时则交给下面的检查，保留原有失败信息。
	if err := sc.Err(); err != nil && ctx.Err() == nil {
		t.Fatal(err)
	}
	if ctx.Err() != nil {
		t.Fatal("watch stream did not end after terminal snapshot")
	}
	return out
}

// TestAPIErrorContract 确认错误响应体为 {Error, Code}，且状态码与 Code 对应。
func TestAPIErrorContract(t *testing.T) {
	c := newClient(t, startTestRuntime(t), "/api")
	cases := []struct {
		method, path, body string
		code               int
		want               string
	}{
		{http.MethodGet, "/tasks/missing", "", http.StatusNotFound, "NotFound"},
		{http.MethodGet, "/tasks?After=missing", "", http.StatusNotFound, "NotFound"},
		{http.MethodGet, "/graphs/missing", "", http.StatusNotFound, "NotFound"},
		{http.MethodPost, "/tasks/missing/cancel", "", http.StatusNotFound, "NotFound"},
		{http.MethodGet, "/tasks?Limit=abc", "", http.StatusBadRequest, "Invalid"},
		{http.MethodGet, "/graphs/demo_tour/versions/0", "", http.StatusBadRequest, "Invalid"},
		{http.MethodPost, "/tasks", "", http.StatusBadRequest, "Invalid"},
		{http.MethodPost, "/tasks", `{"GraphID":`, http.StatusBadRequest, "Invalid"},
		// 未声明的键（如 snake_case 的 graph_id）被拒绝。
		{http.MethodPost, "/tasks", `{"graph_id":"demo_tour"}`, http.StatusBadRequest, "Invalid"},
		{http.MethodPost, "/tasks", `{"GraphID":"demo_tour","Engine":"nope"}`, http.StatusNotFound, "NotFound"},
		{http.MethodPost, "/tasks", `{"GraphID":"missing"}`, http.StatusNotFound, "NotFound"},
	}
	for _, tc := range cases {
		var e apiError
		c.call(tc.method, tc.path, tc.body, tc.code, &e)
		if e.Code != tc.want || e.Error == "" {
			t.Fatalf("%s %s: error=%+v want Code %s", tc.method, tc.path, e, tc.want)
		}
	}
}

// TestStartInvalidPolicyReturnsClientError 确认非法 Policy 在两个引擎上都返回 400 Invalid。
func TestStartInvalidPolicyReturnsClientError(t *testing.T) {
	c := newClient(t, startTestRuntime(t), "/api")
	for _, kind := range []string{"immediate", "durable"} {
		for _, policy := range []string{`{"ExecutionMode":"invalid"}`, `{"NodeTimeoutMS":-1}`, `{"MaxParallel":-1}`, `{"MaxAttempts":-1}`, `{"FailPolicy":"later"}`} {
			var e apiError
			c.call(http.MethodPost, "/tasks", `{"GraphID":"demo_tour","Engine":"`+kind+`","Policy":`+policy+`}`, http.StatusBadRequest, &e)
			if e.Code != "Invalid" {
				t.Fatalf("%s %s: %+v", kind, policy, e)
			}
		}
	}
}
