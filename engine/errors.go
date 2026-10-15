// Package engine 定义执行模型：两个引擎共用的 Task / Run 表结构、执行策略、控制面请求与回执、
// Engine 接口与错误值。immediate / durable 实现同一个 Engine 接口，图语义由纯函数内核 engine/kernel 统一实现。
//
// Package engine holds the execution model: shared Task/Run tables, policies, control-plane
// requests, the Engine contract and errors. Graph semantics live in engine/kernel.
package engine

import (
	"errors"
	"fmt"
)

// 控制面错误；HTTP 层按这些错误映射状态码。
var (
	ErrNotFound    = errors.New("graph runtime: not found")
	ErrConflict    = errors.New("graph runtime: conflict")
	ErrInvalid     = errors.New("graph runtime: invalid definition or input")
	ErrUnsupported = errors.New("graph runtime: unsupported capability")
	ErrCorrupt     = errors.New("graph runtime: stored state unavailable")
)

// NotFoundTask 表示按 ID 找不到 Task，或 Task 不属于当前引擎。
func NotFoundTask(taskID string) error {
	return fmt.Errorf("%w: task %s", ErrNotFound, taskID)
}
