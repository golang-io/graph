package service

import (
	"fmt"

	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
)

// NewNodeRegistry 装配引擎使用的节点注册表：先装入 builtin.Nodes() 的全部核心内置节点，再追加 extra
// （可选内置节点 HTTPNode / LLMNode，或业务节点）。任一节点非法或 ID 与已注册节点重复时返回 ErrInvalid。
//
// NewNodeRegistry assembles the core built-in nodes plus extra nodes.
func NewNodeRegistry(extra ...node.Node) (*node.Registry, error) {
	r, err := node.NewRegistry(append(builtin.Nodes(), extra...)...)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	return r, nil
}
