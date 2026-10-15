package node

import (
	"slices"
	"strings"
)

// 汇合策略（JoinModeDeclarer，即 MergeInput.Mode）。merge 的空 Mode 运行时视为 any；
// 没有声明汇合方式的节点被多条分支汇入时按 all 处理。
// 内核按汇合节点的这两个值（Compiled.JoinModes）决定父 Run 等待子 Run 的方式。
const (
	JoinAny = "any" // 第一个到达者继续，其余子 Run 置为 SKIPPED
	JoinAll = "all" // 所有存活子 Run 到达后继续
)

// joinModes 是合法汇合取值的唯一清单；校验与默认值都从这里读。
var joinModes = []string{"", JoinAny, JoinAll}

// ValidJoinMode 报告 mode 是否为已知汇合策略（含空）。
func ValidJoinMode(mode string) bool {
	return slices.Contains(joinModes, strings.TrimSpace(mode))
}

// NormalizeJoinMode 把实例 Mode 收成运行时汇合策略：all 或 any（空与其它非 all 视为 any）。
func NormalizeJoinMode(mode string) string {
	if strings.TrimSpace(mode) == JoinAll {
		return JoinAll
	}
	return JoinAny
}
