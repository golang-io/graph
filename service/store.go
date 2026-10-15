package service

import (
	"context"
	"fmt"

	"github.com/golang-io/graph/node"
	"gorm.io/gorm"
)

// Store 是图定义库（表 tb_graph_definition），持有注入的 DB 与节点注册表。
// 连接由业务应用注入；本包不打开、不关闭数据库，也不区分 sqlite/mysql。
type Store struct {
	db    *gorm.DB
	nodes *node.Registry
}

// OpenDB 使用注入的 *gorm.DB 作为定义库并建表。
// 不关闭传入连接；生命周期由业务应用管理。
func OpenDB(ctx context.Context, db *gorm.DB, nodes *node.Registry) (*Store, error) {
	gdb := db.WithContext(ctx)
	if err := gdb.AutoMigrate(&Definition{}); err != nil {
		return nil, fmt.Errorf("graph store: migrate: %w", err)
	}
	if nodes == nil {
		var err error
		if nodes, err = NewNodeRegistry(); err != nil {
			return nil, err
		}
	}
	return &Store{db: db, nodes: nodes}, nil
}

// Nodes 返回绑定在此定义库上的节点注册表。
func (s *Store) Nodes() *node.Registry {
	return s.nodes
}
