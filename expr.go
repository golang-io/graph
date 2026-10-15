package graph

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/ast"
	"github.com/expr-lang/expr/parser"
	"github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"
)

// 表达式与文案插值直接使用 expr-lang/expr 的原生语义。
// 用户写法：文案插值 {{expr}}；公式框可写裸表达式或外包 {{expr}}。

// TemplateHoleRE 是两套引擎共用的模板插值边界。
// Shared {{expr}} hole matcher for template rendering.
var TemplateHoleRE = regexp.MustCompile(`(?s)\{\{(.*?)\}\}`)

// EvalExpr 求值表达式（code / 条件 / 插值括号内内容）。
// 允许整串外包一层 {{…}}。变量必须出现在 vars 里，内置函数与运算符走 expr-lang 默认集合。
//
// 编译结果按「表达式原文 + 所引用变量的类型」缓存：类型检查（未定义变量、类型不匹配）
// 与每次重新编译完全一致，只是相同类型组合下不再重复编译。
//
// EvalExpr compiles with expr.Env(vars) semantics; programs are cached per source and
// per types of the referenced variables, so results and errors match a fresh compile.
func EvalExpr(src string, vars map[string]any) (any, error) {
	e := lookupExpr(src)
	if e.prepErr != nil {
		return nil, e.prepErr
	}
	if vars == nil {
		vars = map[string]any{}
	}
	program, err := e.program(vars)
	if err != nil {
		return nil, fmt.Errorf("expr: %w", err)
	}
	out, err := expr.Run(program, vars)
	if err != nil {
		return nil, fmt.Errorf("expr: %w", err)
	}
	return out, nil
}

// ValidateExpr 在定义期检查表达式能否被 expr-lang 编译。
// 此时还没有运行期变量，未声明的名字留给求值时再按 env 检查。
// 检查结果只依赖原文，按原文缓存。
func ValidateExpr(src string) error {
	e := lookupExpr(src)
	if e.prepErr != nil {
		return e.prepErr
	}
	e.validateOnce.Do(func() {
		if _, err := expr.Compile(e.src, expr.AllowUndefinedVariables(), expr.AsAny()); err != nil {
			e.validateErr = fmt.Errorf("expr: %w", err)
		}
	})
	return e.validateErr
}

// ValidateExprWith 在定义期按名字环境检查表达式：names 中的名字作为类型未知的变量，
// 其他标识符（未声明的变量）一律报错；expr-lang 内置函数与运算符不受影响。
// 只在保存图与编译时调用，不缓存。
//
// ValidateExprWith compiles src in a strict env whose variables are exactly names (typed as any).
func ValidateExprWith(src string, names []string) error {
	e := lookupExpr(src)
	if e.prepErr != nil {
		return e.prepErr
	}
	env := make(types.Map, len(names))
	for _, name := range names {
		env[name] = types.Any
	}
	if _, err := expr.Compile(e.src, expr.Env(env), expr.AsAny()); err != nil {
		return fmt.Errorf("expr: %w", err)
	}
	return nil
}

// exprCache 按原文（未规整）缓存 *exprEntry。表达式来自图定义，数量随定义增长。
var exprCache sync.Map

// exprEntry 是一条表达式原文的缓存：规整结果、引用的标识符、定义期检查结果，以及按变量类型区分的编译结果。
type exprEntry struct {
	src     string // 规整后的原文
	prepErr error  // 规整失败（空串）时的错误，原样返回
	// idents 是原文中出现的全部标识符（变量名，也可能含 let 局部名、函数名）。
	// 它是「影响类型检查的变量」的超集：多出来的名字只会让缓存键更细，不会让结果出错。
	idents []string
	// dynamic 为 true 表示不能按 idents 缓存：解析失败（交给 expr.Compile 给出原有错误），
	// 或原文使用了 $env（类型检查依赖整个变量表）。
	dynamic bool

	validateOnce sync.Once
	validateErr  error

	variants atomic.Pointer[[]exprVariant] // 只追加的副本切片，读取无锁
}

// exprVariant 是一种变量类型组合下的编译结果；编译错误同样缓存，避免对失败的表达式反复编译。
type exprVariant struct {
	types   []reflect.Type // 与 idents 一一对应；变量不存在时为 missingVarType
	program *vm.Program
	err     error
}

// missingVar 只用于标记「变量不存在」，与「变量存在且值为 nil」（类型为 nil）区分。
type missingVar struct{}

var missingVarType = reflect.TypeFor[missingVar]()

// lookupExpr 返回原文对应的缓存项，首次出现时规整并写入缓存。
func lookupExpr(raw string) *exprEntry {
	if v, ok := exprCache.Load(raw); ok {
		return v.(*exprEntry)
	}
	v, _ := exprCache.LoadOrStore(raw, newExprEntry(raw))
	return v.(*exprEntry)
}

// newExprEntry 规整原文并收集其中的标识符。
func newExprEntry(raw string) *exprEntry {
	src, err := prepareExprSrc(raw)
	if err != nil {
		return &exprEntry{prepErr: err}
	}
	e := &exprEntry{src: src}
	tree, err := parser.Parse(src)
	if err != nil {
		e.dynamic = true
		return e
	}
	c := &identCollector{seen: map[string]bool{}}
	ast.Walk(&tree.Node, c)
	e.idents, e.dynamic = c.names, c.seen["$env"]
	return e
}

// identCollector 收集语法树中所有 IdentifierNode 的名字（去重，保持首次出现顺序）。
type identCollector struct {
	seen  map[string]bool
	names []string
}

// Visit 实现 ast.Visitor。
func (c *identCollector) Visit(node *ast.Node) {
	if id, ok := (*node).(*ast.IdentifierNode); ok && !c.seen[id.Value] {
		c.seen[id.Value] = true
		c.names = append(c.names, id.Value)
	}
}

// program 返回 vars 当前类型组合下的编译结果：命中缓存直接返回，否则按 expr.Env(vars) 编译并记录。
func (e *exprEntry) program(vars map[string]any) (*vm.Program, error) {
	if e.dynamic {
		return expr.Compile(e.src, expr.Env(vars), expr.AsAny())
	}
	if list := e.variants.Load(); list != nil {
		for _, v := range *list {
			if e.sameTypes(v.types, vars) {
				return v.program, v.err
			}
		}
	}
	program, err := expr.Compile(e.src, expr.Env(vars), expr.AsAny())
	types := make([]reflect.Type, len(e.idents))
	for i, name := range e.idents {
		types[i] = varType(vars, name)
	}
	// 写时复制追加；并发编译同一组合时可能重复追加，读取时取第一条，结果相同。
	for {
		old := e.variants.Load()
		var next []exprVariant
		if old != nil {
			next = append(slices.Clone(*old), exprVariant{types, program, err})
		} else {
			next = []exprVariant{{types, program, err}}
		}
		if e.variants.CompareAndSwap(old, &next) {
			break
		}
	}
	return program, err
}

// sameTypes 判断 vars 中各标识符的类型是否与缓存记录一致。
func (e *exprEntry) sameTypes(types []reflect.Type, vars map[string]any) bool {
	for i, name := range e.idents {
		if varType(vars, name) != types[i] {
			return false
		}
	}
	return true
}

// varType 返回变量值的动态类型；变量不存在时返回 missingVarType。
func varType(vars map[string]any, name string) reflect.Type {
	v, ok := vars[name]
	if !ok {
		return missingVarType
	}
	return reflect.TypeOf(v)
}

func prepareExprSrc(src string) (string, error) {
	src = normalizeExprSrc(src)
	if src == "" {
		return "", fmt.Errorf("expr: empty")
	}
	return src, nil
}

// normalizeExprSrc 去空白；若整串被 {{ }} 包一层则剥掉。
func normalizeExprSrc(src string) string {
	src = strings.TrimSpace(src)
	if strings.HasPrefix(src, "{{") && strings.HasSuffix(src, "}}") && len(src) >= 4 {
		src = strings.TrimSpace(src[2 : len(src)-2])
	}
	return src
}

// RenderTemplate 文案插值：扫描 {{…}}，括号内 EvalExpr，拼回字符串。
// 求值失败或结果为 nil → 空串（避免打断整段文案）。
func RenderTemplate(tpl string, vars map[string]any) string {
	if !strings.Contains(tpl, "{{") {
		return tpl
	}
	return TemplateHoleRE.ReplaceAllStringFunc(tpl, func(m string) string {
		if len(m) < 4 {
			return ""
		}
		v, err := EvalExpr(m[2:len(m)-2], vars)
		if err != nil || v == nil {
			return ""
		}
		return fmt.Sprint(v)
	})
}
