package durable

import (
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/kernel"
)

// DropDeliveries 仅供测试使用，生产代码不得调用：之后 f 返回 true 的提交后投递
// （NotifyParent / CancelChild）被丢弃，模拟「事务已提交、投递前进程退出」，
// 用于验证恢复扫描会补发这些投递。f 为 nil 时恢复正常投递。eng 必须是本包构造的引擎。
// 故障用例位于独立的 test/ 模块，无法使用本包的 export_test.go，因此导出在非测试文件中。
//
// DropDeliveries is for tests only: post-commit deliveries for which f returns true are dropped,
// simulating a crash between commit and delivery. A nil f restores normal delivery.
func DropDeliveries(eng engine.Engine, f func(kernel.Command) bool) {
	e := eng.(*Engine)
	if f == nil {
		e.drop.Store(nil)
		return
	}
	e.drop.Store(&f)
}
