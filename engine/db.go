package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/gorm/schema"
)

// GormConfig 返回框架要求的 GORM 配置：列名即字段名（NoLowerCase），不建外键约束；
// GORM 自动写入的 CreatedAt / UpdatedAt 统一取 UTC，与代码中显式写入的 time.Now().UTC() 一致。
// 业务应用打开 *gorm.DB 时使用它（或至少设置相同的 NamingStrategy），再注入 EngineConfig / Options。
// 使用 MySQL 时不要设置 mysql.Config.DefaultStringSize：未声明长度的 JSON 与长文本列会因此建成 VARCHAR，
// 而不是 LONGTEXT。
//
// GormConfig returns the required GORM config: column names equal Go field names, timestamps in UTC.
func GormConfig() *gorm.Config {
	return &gorm.Config{
		NamingStrategy:                           schema.NamingStrategy{NoLowerCase: true},
		DisableForeignKeyConstraintWhenMigrating: true,
		Logger:                                   logger.Default.LogMode(logger.Silent),
		NowFunc:                                  func() time.Time { return time.Now().UTC() },
	}
}

// Ping 检查数据库连接。db 为空时视为无需检查（纯内存引擎）。
func Ping(ctx context.Context, db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Retryable 报告错误是否为可重试的并发冲突：死锁、锁等待超时、序列化失败、SQLite busy。
// 判断按各数据库驱动的错误文本与错误码进行，不依赖具体驱动包。
//
// Retryable reports whether err is a transient concurrency conflict worth retrying.
func Retryable(err error) bool {
	return containsAny(err, "deadlock", "database is locked", "busy", "lock wait timeout", "could not serialize", "40001", "40p01", "1213")
}

// DuplicateKey 报告错误是否为唯一索引冲突（MySQL 1062、PostgreSQL 23505、SQLite UNIQUE constraint）。
// 用于「先读最大值再插入」类写入在并发下撞号后重做事务。
//
// DuplicateKey reports whether err is a unique-constraint violation.
func DuplicateKey(err error) bool {
	return containsAny(err, "duplicate entry", "duplicate key", "unique constraint")
}

// containsAny 报告 err 的文本（小写）是否包含任一片段；err 为 nil 时返回 false。
func containsAny(err error, parts ...string) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, p := range parts {
		if strings.Contains(msg, p) {
			return true
		}
	}
	return false
}

// MigrateExecution 建立两个引擎共用的执行表（tb_graph_task / tb_graph_run）。
// 两个引擎的 Open 都调用它，重复调用无副作用。AutoMigrate 只增列与索引，不删已废弃的列。
//
// MigrateExecution creates the shared execution tables.
func MigrateExecution(ctx context.Context, db *gorm.DB) error {
	if err := db.WithContext(ctx).AutoMigrate(&Task{}, &Run{}); err != nil {
		return fmt.Errorf("graph runtime: migrate execution tables: %w", err)
	}
	return nil
}
