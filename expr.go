package graph

import (
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/types"
	"github.com/expr-lang/expr/vm"
)

// 表达式与文案插值直接使用 expr-lang/expr 的原生语义。
// 用户写法：文案插值 {{expr}}；公式框可写裸表达式或外包 {{expr}}。

// TemplateHoleRE 是两套引擎共用的模板插值边界。
// Shared {{expr}} hole matcher for template rendering.
var TemplateHoleRE = regexp.MustCompile(`(?s)\{\{(.*?)\}\}`)

// maxExprVariants 限制单个表达式原文最多缓存的变量类型签名数量，防止极端动态变量集合下缓存无限增长。
const maxExprVariants = 16

// exprVarType 记录运行期变量表中单个键及其顶层动态类型（值为 nil 时 typ 为 nil）。
// expr.Env(map[string]any) 的编译结果完全由顶层键集合与各键的 reflect.Type 唯一决定。
type exprVarType struct {
	name string
	typ  reflect.Type
}

type exprVariant struct {
	vars    []exprVarType
	program *vm.Program
	err     error
}

type exprBucket struct {
	mu       sync.RWMutex
	variants []exprVariant
}

type validateExprEntry struct {
	err error
}

type validateWithVariant struct {
	names []string
	err   error
}

type validateWithBucket struct {
	mu       sync.RWMutex
	variants []validateWithVariant
}

var (
	evalExprCache         sync.Map // normalized src (string) -> *exprBucket
	validateExprCache     sync.Map // normalized src (string) -> validateExprEntry
	validateExprWithCache sync.Map // normalized src (string) -> *validateWithBucket
)

// EvalExpr 求值表达式（code / 条件 / 插值括号内内容）。
// 允许整串外包一层 {{…}}。变量必须出现在 vars 里，内置函数与运算符走 expr-lang 默认集合。
// 编译产出的字节码按「规整后原文 + 顶层变量名与动态类型签名」缓存复用，相同类型签名下零重复编译开销。
func EvalExpr(src string, vars map[string]any) (any, error) {
	src, err := prepareExprSrc(src)
	if err != nil {
		return nil, err
	}
	if vars == nil {
		vars = map[string]any{}
	}
	program, err := evalExprProgram(src, vars)
	if err != nil {
		return nil, err
	}
	out, err := expr.Run(program, vars)
	if err != nil {
		return nil, fmt.Errorf("expr: %w", err)
	}
	return out, nil
}

// evalExprProgram 按表达式原文与 vars 的顶层类型签名获取或编译 *vm.Program。
func evalExprProgram(src string, vars map[string]any) (*vm.Program, error) {
	var bucket *exprBucket
	if v, ok := evalExprCache.Load(src); ok {
		bucket = v.(*exprBucket)
		bucket.mu.RLock()
		for i := range bucket.variants {
			variant := &bucket.variants[i]
			if matchExprVars(variant.vars, vars) {
				prog, err := variant.program, variant.err
				bucket.mu.RUnlock()
				return prog, err
			}
		}
		bucket.mu.RUnlock()
	} else {
		actual, _ := evalExprCache.LoadOrStore(src, &exprBucket{})
		bucket = actual.(*exprBucket)
	}

	program, compileErr := expr.Compile(src, expr.Env(vars), expr.AsAny())
	if compileErr != nil {
		compileErr = fmt.Errorf("expr: %w", compileErr)
	}
	sig := buildExprVarSignature(vars)

	bucket.mu.Lock()
	for i := range bucket.variants {
		if matchExprVars(bucket.variants[i].vars, vars) {
			prog, err := bucket.variants[i].program, bucket.variants[i].err
			bucket.mu.Unlock()
			return prog, err
		}
	}
	if len(bucket.variants) < maxExprVariants {
		bucket.variants = append(bucket.variants, exprVariant{
			vars:    sig,
			program: program,
			err:     compileErr,
		})
	} else {
		bucket.variants[len(bucket.variants)-1] = exprVariant{
			vars:    sig,
			program: program,
			err:     compileErr,
		}
	}
	bucket.mu.Unlock()
	return program, compileErr
}

// matchExprVars 判断 vars 的键集合与各键的动态类型是否与签名 sig 完全一致（全程零分配）。
func matchExprVars(sig []exprVarType, vars map[string]any) bool {
	if len(sig) != len(vars) {
		return false
	}
	for _, vt := range sig {
		val, ok := vars[vt.name]
		if !ok || reflect.TypeOf(val) != vt.typ {
			return false
		}
	}
	return true
}

// buildExprVarSignature 构造 vars 的有序类型签名，仅在首次编译未命中缓存时调用。
func buildExprVarSignature(vars map[string]any) []exprVarType {
	if len(vars) == 0 {
		return nil
	}
	keys := slices.Sorted(maps.Keys(vars))
	sig := make([]exprVarType, len(keys))
	for i, k := range keys {
		sig[i] = exprVarType{name: k, typ: reflect.TypeOf(vars[k])}
	}
	return sig
}

// ValidateExpr 在定义期检查表达式能否被 expr-lang 编译。
// 此时还没有运行期变量，未声明的名字留给求值时再按 env 检查。
func ValidateExpr(src string) error {
	src, err := prepareExprSrc(src)
	if err != nil {
		return err
	}
	if v, ok := validateExprCache.Load(src); ok {
		return v.(validateExprEntry).err
	}
	var compileErr error
	if _, err := expr.Compile(src, expr.AllowUndefinedVariables(), expr.AsAny()); err != nil {
		compileErr = fmt.Errorf("expr: %w", err)
	}
	validateExprCache.Store(src, validateExprEntry{err: compileErr})
	return compileErr
}

// ValidateExprWith 在定义期按名字环境检查表达式：names 中的名字作为类型未知的变量，
// 其他标识符（未声明的变量）一律报错；expr-lang 内置函数与运算符不受影响。
//
// ValidateExprWith compiles src in a strict env whose variables are exactly names (typed as any).
func ValidateExprWith(src string, names []string) error {
	src, err := prepareExprSrc(src)
	if err != nil {
		return err
	}
	var canonNames []string
	if len(names) > 0 {
		canonNames = slices.Clone(names)
		slices.Sort(canonNames)
		canonNames = slices.Compact(canonNames)
	}
	var bucket *validateWithBucket
	if v, ok := validateExprWithCache.Load(src); ok {
		bucket = v.(*validateWithBucket)
		bucket.mu.RLock()
		for i := range bucket.variants {
			if slices.Equal(bucket.variants[i].names, canonNames) {
				cachedErr := bucket.variants[i].err
				bucket.mu.RUnlock()
				return cachedErr
			}
		}
		bucket.mu.RUnlock()
	} else {
		actual, _ := validateExprWithCache.LoadOrStore(src, &validateWithBucket{})
		bucket = actual.(*validateWithBucket)
	}

	env := make(types.Map, len(canonNames))
	for _, name := range canonNames {
		env[name] = types.Any
	}
	var compileErr error
	if _, err := expr.Compile(src, expr.Env(env), expr.AsAny()); err != nil {
		compileErr = fmt.Errorf("expr: %w", err)
	}

	bucket.mu.Lock()
	for i := range bucket.variants {
		if slices.Equal(bucket.variants[i].names, canonNames) {
			cachedErr := bucket.variants[i].err
			bucket.mu.Unlock()
			return cachedErr
		}
	}
	clonedNames := slices.Clone(canonNames)
	if len(bucket.variants) < maxExprVariants {
		bucket.variants = append(bucket.variants, validateWithVariant{names: clonedNames, err: compileErr})
	} else {
		bucket.variants[len(bucket.variants)-1] = validateWithVariant{names: clonedNames, err: compileErr}
	}
	bucket.mu.Unlock()
	return compileErr
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
		inner := src[2 : len(src)-2]
		if !strings.Contains(inner, "}}") {
			src = strings.TrimSpace(inner)
		}
	}
	return src
}

// RenderTemplate 文案插值：线性扫描 {{…}}，括号内 EvalExpr，拼回字符串。
// 求值失败或结果为 nil → 空串（避免打断整段文案）。
func RenderTemplate(tpl string, vars map[string]any) string {
	open := strings.Index(tpl, "{{")
	if open < 0 {
		return tpl
	}
	if !strings.Contains(tpl[open+2:], "}}") {
		return tpl
	}
	var b strings.Builder
	b.Grow(len(tpl))
	pos := 0
	for {
		b.WriteString(tpl[pos : pos+open])
		holeStart := pos + open + 2
		closeRel := strings.Index(tpl[holeStart:], "}}")
		if closeRel < 0 {
			b.WriteString(tpl[pos+open:])
			break
		}
		hole := tpl[holeStart : holeStart+closeRel]
		if v, err := EvalExpr(hole, vars); err == nil && v != nil {
			writeTemplateValue(&b, v)
		}
		pos = holeStart + closeRel + 2
		if pos >= len(tpl) {
			break
		}
		open = strings.Index(tpl[pos:], "{{")
		if open < 0 {
			b.WriteString(tpl[pos:])
			break
		}
	}
	return b.String()
}

func writeTemplateValue(b *strings.Builder, v any) {
	switch x := v.(type) {
	case string:
		b.WriteString(x)
	case int64:
		b.WriteString(strconv.FormatInt(x, 10))
	case int:
		b.WriteString(strconv.Itoa(x))
	case bool:
		b.WriteString(strconv.FormatBool(x))
	default:
		b.WriteString(fmt.Sprint(v))
	}
}
