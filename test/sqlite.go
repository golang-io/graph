package test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/golang-io/graph/engine"
	"gorm.io/gorm"
)

// Open 打开带 WAL / busy_timeout 的本地 sqlite，供引擎与定义库注入。
// 调用方负责 Close；MustOpen 会在测试结束时关闭。
func Open(path string) (*gorm.DB, error) {
	return open(path, true)
}

// OpenPlain 仅打开 sqlite、不设 WAL/PRAGMA，供「借用业务应用连接」类用例。
func OpenPlain(path string) (*gorm.DB, error) {
	return open(path, false)
}

// MustOpen 打开 Open 风格连接，并在 t.Cleanup 关闭底层 *sql.DB。
func MustOpen(t testing.TB, path string) *gorm.DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	cleanupDB(t, db)
	return db
}

// TempDB 在 tb 的临时目录下打开一个新的空库，签名与 enginetest.OpenDB 一致，
// 可直接传给 enginetest.Run / enginetest.Bench。
//
// TempDB opens a fresh sqlite database under tb.TempDir(); it satisfies enginetest.OpenDB.
func TempDB(tb testing.TB) *gorm.DB {
	tb.Helper()
	return MustOpen(tb, filepath.Join(tb.TempDir(), "graph.db"))
}

func open(path string, tuned bool) (*gorm.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, err
	}
	dsn := abs
	if tuned {
		dsn = abs + "?_txlock=immediate&_busy_timeout=10000"
	}
	// 与业务应用相同，使用框架要求的 engine.GormConfig（列名即字段名、时间按 UTC 写入）。
	db, err := gorm.Open(sqlite.Open(dsn), engine.GormConfig())
	if err != nil {
		return nil, fmt.Errorf("graph test: open sqlite %s: %w", abs, err)
	}
	if tuned {
		_ = db.Exec("PRAGMA journal_mode=WAL")
		_ = db.Exec("PRAGMA foreign_keys=OFF")
		_ = db.Exec("PRAGMA busy_timeout=10000")
	}
	return db, nil
}

func cleanupDB(t testing.TB, db *gorm.DB) {
	t.Helper()
	t.Cleanup(func() {
		sqlDB, err := db.DB()
		if err != nil {
			return
		}
		_ = sqlDB.Close()
	})
}
