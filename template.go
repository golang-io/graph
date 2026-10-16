package graph

import "strings"

// ValidateTemplate 定义期检查文案插值：每个 {{…}} 括号内的表达式可编译。
// 不检查标识符是否存在；按输入名检查见 ValidateTemplateWith。
func ValidateTemplate(tpl string) error {
	return checkTemplate(tpl, ValidateExpr)
}

// ValidateTemplateWith 与 ValidateTemplate 相同，但括号内表达式只能使用 names 中的名字。
//
// ValidateTemplateWith validates every {{…}} hole against a strict env of names.
func ValidateTemplateWith(tpl string, names []string) error {
	return checkTemplate(tpl, func(src string) error { return ValidateExprWith(src, names) })
}

// checkTemplate 线性扫描模板中的每个 {{…}} 插值并调用 check，避免非贪婪正则状态机分配。
func checkTemplate(tpl string, check func(string) error) error {
	pos := 0
	for pos < len(tpl) {
		open := strings.Index(tpl[pos:], "{{")
		if open < 0 {
			return nil
		}
		holeStart := pos + open + 2
		closeRel := strings.Index(tpl[holeStart:], "}}")
		if closeRel < 0 {
			return nil
		}
		if err := check(tpl[holeStart : holeStart+closeRel]); err != nil {
			return err
		}
		pos = holeStart + closeRel + 2
	}
	return nil
}
