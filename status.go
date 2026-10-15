package graph

// StatusCode 是全局唯一的状态码类型，Task 与 Run 共用。
// StatusCode is the shared status-code type for Task and Run.
type StatusCode string

const (
	PENDING   StatusCode = "PENDING"   // 已接受，尚未开始推进
	RUNNING   StatusCode = "RUNNING"   // 正在推进，或还有可调度的工作
	WAITING   StatusCode = "WAITING"   // 没有可调度工作，仍在等待汇合、子任务结束或调试放行
	SUCCEEDED StatusCode = "SUCCEEDED" // 终态：成功完成
	FAILED    StatusCode = "FAILED"    // 终态：失败
	CANCELED  StatusCode = "CANCELED"  // 终态：已取消
	SKIPPED   StatusCode = "SKIPPED"   // 终态：未命中或被跳过
)

// IsTerminal 判断状态是否为终态。Task 终态由引擎按执行事实写入，不从 Run 状态反推。
// IsTerminal reports whether the status code is terminal.
func IsTerminal(s StatusCode) bool {
	return s == SUCCEEDED || s == FAILED || s == CANCELED || s == SKIPPED
}
