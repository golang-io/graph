package service

import (
	"testing"

	"github.com/golang-io/graph/node/builtin"
)

// TestNodeRegistryHTTPIsOptIn 确认默认注册表不含 HTTP 节点，业务应用显式注册后才可用。
func TestNodeRegistryHTTPIsOptIn(t *testing.T) {
	defaults, err := NewNodeRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := defaults.Get(builtin.HTTP); ok {
		t.Fatal("default registry must not include the http node")
	}
	withHTTP, err := NewNodeRegistry(builtin.HTTPNode{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := withHTTP.ExecutorOf(builtin.HTTP); !ok {
		t.Fatal("explicitly registered http node has no executor")
	}
}
