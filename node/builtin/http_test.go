package builtin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-io/graph/node"
)

// TestHTTPReachesLoopbackServer 确认执行器发出请求，并写出响应体、状态码与是否 2xx。
func TestHTTPReachesLoopbackServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
}
