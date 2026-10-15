// Package api 把定义库与执行引擎暴露为标准 http.Handler。
// 请求体、响应体与查询参数的键名都与 Go 字段名一致（大驼峰，缩写全大写，如 TaskID、GraphID）。
// 错误响应体为 {"Error": "...", "Code": "..."}，Code 取 NotFound / Conflict / Invalid / Unsupported / Corrupt / Internal。
//
// Package api serves definitions and the task control plane over HTTP.
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/service"
)

// Handler 构造承载图定义与 Task 控制面的 http.Handler；prefix 为可选路径前缀（如 /api）。
func Handler(rt *service.Runtime, prefix ...string) http.Handler {
	var p string
	if len(prefix) > 0 {
		p = strings.TrimRight(strings.TrimSpace(prefix[0]), "/")
	}
	h := &handler{rt: rt}
	mux := http.NewServeMux()

	// 图定义
	mux.HandleFunc("GET "+p+"/graphs", h.graphs)
	mux.HandleFunc("POST "+p+"/graphs", h.createGraph)
	mux.HandleFunc("GET "+p+"/graphs/{id}", h.graph)
	mux.HandleFunc("PUT "+p+"/graphs/{id}", h.saveGraph)
	mux.HandleFunc("DELETE "+p+"/graphs/{id}", h.deleteGraph)
	mux.HandleFunc("GET "+p+"/graphs/{id}/versions", h.graphVersions)
	mux.HandleFunc("GET "+p+"/graphs/{id}/versions/{version}", h.graphVersion)
	mux.HandleFunc("PUT "+p+"/graphs/{id}/engine", h.setEngine)
	mux.HandleFunc("POST "+p+"/graphs/examples", h.examples)
	mux.HandleFunc("GET "+p+"/node-specs", h.nodeSpecs)
	mux.HandleFunc("GET "+p+"/engines", h.engines)

	// Task 控制面
	mux.HandleFunc("POST "+p+"/tasks", h.startTask)
	mux.HandleFunc("GET "+p+"/tasks", h.listTasks)
	mux.HandleFunc("GET "+p+"/tasks/{id}", h.snapshot)
	mux.HandleFunc("GET "+p+"/tasks/{id}/watch", h.watch)
	mux.HandleFunc("POST "+p+"/tasks/{id}/resume", h.resume)
	mux.HandleFunc("POST "+p+"/tasks/{id}/pause", h.pause)
	mux.HandleFunc("POST "+p+"/tasks/{id}/cancel", h.cancel)
	return mux
}

type handler struct {
	rt *service.Runtime
}

// listBody 是列表响应的外层结构；保留外层对象，便于以后增加分页字段。
type listBody[T any] struct {
	Data []T `json:"Data"`
}

func list[T any](data []T) listBody[T] {
	if data == nil {
		data = []T{}
	}
	return listBody[T]{Data: data}
}

// pathID 读取路径参数 {id}；为空时返回 ErrInvalid。
func pathID(r *http.Request) (string, error) {
	id := strings.TrimSpace(r.PathValue("id"))
	if id == "" {
		return "", fmt.Errorf("%w: id required", engine.ErrInvalid)
	}
	return id, nil
}

// queryInt 读取整数查询参数；缺省时返回 def，格式错误时返回 ErrInvalid。
func queryInt(r *http.Request, key string, def int64) (int64, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return def, nil
	}
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || v < 0 {
		return 0, fmt.Errorf("%w: query %s must be a non-negative integer", engine.ErrInvalid, key)
	}
	return v, nil
}

// requireID 读取路径 {id}；失败时已写错误响应。
func (h *handler) requireID(w http.ResponseWriter, r *http.Request) (string, bool) {
	id, err := pathID(r)
	if err != nil {
		fail(w, err)
		return "", false
	}
	return id, true
}

// readJSON 解码请求体；失败时已写错误响应。
func (h *handler) readJSON(w http.ResponseWriter, r *http.Request, target any, optional bool) bool {
	if err := decodeRequest(r, target, optional); err != nil {
		fail(w, err)
		return false
	}
	return true
}

// writeResult 成功时写 JSON，失败时按错误类型返回。
func writeResult(w http.ResponseWriter, code int, v any, err error) {
	if err != nil {
		fail(w, err)
		return
	}
	writeJSON(w, code, v)
}

// respond 把 (值, error) 写成指定状态码，便于直接接引擎方法的返回值。
func respond(w http.ResponseWriter, code int) func(any, error) {
	return func(v any, err error) { writeResult(w, code, v, err) }
}

func (h *handler) graphs(w http.ResponseWriter, r *http.Request) {
	defs, err := h.rt.Store.List(r.Context())
	writeResult(w, http.StatusOK, list(defs), err)
}

func (h *handler) createGraph(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"Name"`
		Description string `json:"Description,omitempty"`
	}
	if !h.readJSON(w, r, &body, false) {
		return
	}
	def, err := h.rt.Store.Create(r.Context(), body.Name, body.Description)
	writeResult(w, http.StatusCreated, def, err)
}

func (h *handler) graph(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	def, err := h.rt.Store.Get(r.Context(), id)
	writeResult(w, http.StatusOK, def, err)
}

func (h *handler) saveGraph(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	var def service.Definition
	if !h.readJSON(w, r, &def, false) {
		return
	}
	def.GraphID = id
	saved, err := h.rt.Store.Save(r.Context(), def)
	writeResult(w, http.StatusOK, saved, err)
}

func (h *handler) deleteGraph(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	if err := h.rt.Store.Delete(r.Context(), id); err != nil {
		fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *handler) graphVersions(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	limit, err := queryInt(r, "Limit", 0)
	if err != nil {
		fail(w, err)
		return
	}
	defs, err := h.rt.Store.ListVersions(r.Context(), id, int(limit))
	writeResult(w, http.StatusOK, list(defs), err)
}

func (h *handler) graphVersion(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	version, err := strconv.ParseInt(r.PathValue("version"), 10, 64)
	if err != nil || version < 1 {
		fail(w, fmt.Errorf("%w: version must be a positive integer", engine.ErrInvalid))
		return
	}
	def, err := h.rt.Store.GetVersion(r.Context(), id, version)
	writeResult(w, http.StatusOK, def, err)
}

func (h *handler) setEngine(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	var body struct{ Engine string }
	if !h.readJSON(w, r, &body, false) {
		return
	}
	def, err := h.rt.SetGraphEngine(r.Context(), id, body.Engine)
	writeResult(w, http.StatusOK, def, err)
}

func (h *handler) examples(w http.ResponseWriter, r *http.Request) {
	if err := service.ExampleGraph(r.Context(), h.rt.Store); err != nil {
		fail(w, err)
		return
	}
	ids, err := service.ExampleIDs()
	writeResult(w, http.StatusOK, list(ids), err)
}

func (h *handler) nodeSpecs(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, list(h.rt.Store.Nodes().List()))
}

func (h *handler) engines(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, list(h.rt.Engines.Kinds()))
}

func (h *handler) startTask(w http.ResponseWriter, r *http.Request) {
	var req engine.StartRequest
	if !h.readJSON(w, r, &req, false) {
		return
	}
	respond(w, http.StatusAccepted)(h.rt.Start(r.Context(), req))
}

func (h *handler) listTasks(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, err := queryInt(r, "Limit", 50)
	if err != nil {
		fail(w, err)
		return
	}
	query := engine.ListQuery{
		GraphID: strings.TrimSpace(q.Get("GraphID")),
		Status:  graphStatus(q.Get("Status")),
		Limit:   int(limit),
		After:   strings.TrimSpace(q.Get("After")),
	}
	rows, err := h.rt.ListTasks(r.Context(), query)
	writeResult(w, http.StatusOK, list(rows), err)
}

func (h *handler) snapshot(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	respond(w, http.StatusOK)(h.rt.Engines.Snapshot(r.Context(), id))
}

func (h *handler) resume(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	var req engine.ResumeRequest
	if !h.readJSON(w, r, &req, true) {
		return
	}
	respond(w, http.StatusAccepted)(h.rt.Engines.Resume(r.Context(), id, req))
}

func (h *handler) pause(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	respond(w, http.StatusAccepted)(h.rt.Engines.Pause(r.Context(), id))
}

func (h *handler) cancel(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	var body struct {
		Reason string `json:"Reason,omitempty"`
	}
	if !h.readJSON(w, r, &body, true) {
		return
	}
	respond(w, http.StatusAccepted)(h.rt.Engines.Cancel(r.Context(), id, body.Reason))
}

// watch 以 Server-Sent Events 推送 Task 变化：每个事件 data 为 WatchEvent。
// 一条连接的第一个事件是完整快照（带 Snapshot），之后只含增量。
// 终态事件发出后结束。
func (h *handler) watch(w http.ResponseWriter, r *http.Request) {
	id, ok := h.requireID(w, r)
	if !ok {
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		fail(w, fmt.Errorf("%w: streaming", engine.ErrUnsupported))
		return
	}
	stream, err := h.rt.Engines.Watch(r.Context(), id)
	if err != nil {
		fail(w, err)
		return
	}
	defer stream.Close()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher.Flush()

	type item struct {
		ev  service.WatchEvent
		err error
	}
	ch := make(chan item, 1)
	go func() {
		for {
			ev, err := stream.Recv()
			select {
			case ch <- item{ev, err}:
			case <-r.Context().Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		case it := <-ch:
			if it.err != nil {
				if !errors.Is(it.err, io.EOF) {
					_, body := errorBody(it.err)
					data, _ := json.Marshal(body)
					fmt.Fprintf(w, "event: error\ndata: %s\n\n", data)
					flusher.Flush()
				}
				return
			}
			data, err := json.Marshal(it.ev)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// apiError 是错误响应体。
type apiError struct {
	Error string `json:"Error"`
	Code  string `json:"Code"`
}

// errorBody 把错误映射为错误码；ErrCorrupt 不回显内部细节。
func errorBody(err error) (int, apiError) {
	switch {
	case errors.Is(err, engine.ErrNotFound):
		return http.StatusNotFound, apiError{err.Error(), "NotFound"}
	case errors.Is(err, engine.ErrConflict):
		return http.StatusConflict, apiError{err.Error(), "Conflict"}
	case errors.Is(err, engine.ErrInvalid):
		return http.StatusBadRequest, apiError{err.Error(), "Invalid"}
	case errors.Is(err, engine.ErrUnsupported):
		return http.StatusBadRequest, apiError{err.Error(), "Unsupported"}
	case errors.Is(err, engine.ErrCorrupt):
		return http.StatusInternalServerError, apiError{"stored execution data is corrupt; operator inspection required", "Corrupt"}
	default:
		return http.StatusInternalServerError, apiError{err.Error(), "Internal"}
	}
}

func fail(w http.ResponseWriter, err error) {
	code, body := errorBody(err)
	writeJSON(w, code, body)
}
