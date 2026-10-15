package graph

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

// checkTemplate 对模板中的每个 {{…}} 插值调用 check。
func checkTemplate(tpl string, check func(string) error) error {
	for _, match := range TemplateHoleRE.FindAllStringSubmatch(tpl, -1) {
		if err := check(match[1]); err != nil {
			return err
		}
	}
	return nil
}
