package builtin

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-io/graph/node"
)

// TestHTTPReachesLoopbackServer 确认执行器发出请求，并写出响应体、状态码与是否 2xx；超出响应大小上限时报错。
func TestHTTPReachesLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/huge" {
			_, _ = w.Write(bytes.Repeat([]byte("a"), maxHTTPResponseBytes+1))
			return
		}
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()
	out, err := HTTPNode{}.Execute(context.Background(), node.NodeInput{
		Params: HTTPInput{URL: srv.URL},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Set["Body"] != "pong" || out.Set["Status"] != int64(http.StatusOK) || out.Set["OK"] != true {
		t.Fatalf("unexpected output: %#v", out.Set)
	}

	_, err = HTTPNode{}.Execute(context.Background(), node.NodeInput{
		Params: HTTPInput{URL: srv.URL + "/huge"},
	})
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}
