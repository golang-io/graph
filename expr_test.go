package graph

import (
	"math"
	"sync"
	"testing"
)

func numEq(t *testing.T, got any, want float64) {
	t.Helper()
	switch v := got.(type) {
	case float64:
		if math.Abs(v-want) > 1e-9 {
			t.Fatalf("got %v want %v", got, want)
		}
	case float32:
		if math.Abs(float64(v)-want) > 1e-9 {
			t.Fatalf("got %v want %v", got, want)
		}
	case int:
		if float64(v) != want {
			t.Fatalf("got %v want %v", got, want)
		}
	case int64:
		if float64(v) != want {
			t.Fatalf("got %v want %v", got, want)
		}
	default:
		t.Fatalf("got %T %v want float %v", got, got, want)
	}
}

func TestEvalExprArithmetic(t *testing.T) {
	v, err := EvalExpr("1 + 2 * 3", nil)
	if err != nil {
		t.Fatal(err)
	}
	numEq(t, v, 7)
}

func TestEvalExprVarsAndCmp(t *testing.T) {
	vars := map[string]any{"score": 80.0, "name": "Ada"}
	v, err := EvalExpr("score >= 60 && name == \"Ada\"", vars)
	if err != nil {
		t.Fatal(err)
	}
	if v != true {
		t.Fatalf("got %v", v)
	}
}

func TestEvalExprConcat(t *testing.T) {
	// 原生 expr：同类型 string 可用 + 拼接；混类型需显式 string(...)
	vars := map[string]any{"a": "hi", "b": "there", "n": 3.0}
	v, err := EvalExpr("a + \" \" + b", vars)
	if err != nil {
		t.Fatal(err)
	}
	if v != "hi there" {
		t.Fatalf("got %v", v)
	}
	v, err = EvalExpr("a + \":\" + string(n)", vars)
	if err != nil {
		t.Fatal(err)
	}
	if v != "hi:3" {
		t.Fatalf("got %v", v)
	}
	if _, err := EvalExpr("a + n", vars); err == nil {
		t.Fatal("want type mismatch error")
	}
}

func TestEvalExprUnknownVar(t *testing.T) {
	_, err := EvalExpr("missing + 1", map[string]any{})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestEvalExprDivZero(t *testing.T) {
	// expr-lang 返回 +Inf，不报错（与 IEEE 浮点一致）。
	v, err := EvalExpr("1 / 0", nil)
	if err != nil {
		t.Fatal(err)
	}
	f, ok := v.(float64)
	if !ok || !math.IsInf(f, 1) {
		t.Fatalf("got %v (%T)", v, v)
	}
}

func TestEvalExprPathAndIndex(t *testing.T) {
	vars := map[string]any{
		"user":  map[string]any{"name": "Ada"},
		"items": []any{"x", "y"},
	}
	v, err := EvalExpr("user.name", vars)
	if err != nil {
		t.Fatal(err)
	}
	if v != "Ada" {
		t.Fatalf("got %v", v)
	}
	v, err = EvalExpr("items[0]", vars)
	if err != nil {
		t.Fatal(err)
	}
	if v != "x" {
		t.Fatalf("got %v", v)
	}
}

func TestEvalExprWrapped(t *testing.T) {
	vars := map[string]any{"name": "Ada", "score": 10.0}
	v, err := EvalExpr("{{ name }}", vars)
	if err != nil {
		t.Fatal(err)
	}
	if v != "Ada" {
		t.Fatalf("got %v", v)
	}
	v, err = EvalExpr("{{score + 1}}", vars)
	if err != nil {
		t.Fatal(err)
	}
	numEq(t, v, 11)
}

func TestRenderTemplateExprHoles(t *testing.T) {
	vars := map[string]any{
		"name":  "Ada",
		"score": 90,
		"user":  map[string]any{"title": "CEO"},
		"items": []any{"a", "b"},
	}
	got := RenderTemplate("hi {{name}}, {{user.title}}, {{score + 1}}, {{items[0]}}", vars)
	want := "hi Ada, CEO, 91, a"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestRenderTemplateMissingIsEmpty(t *testing.T) {
	// 插值求值失败 → 空串；字面量「=」保留。
	got := RenderTemplate("a={{missing}}b", map[string]any{})
	if got != "a=b" {
		t.Fatalf("got %q", got)
	}
}

// TestEvalExprCacheFollowsTypes 验证编译缓存不改变语义：同一原文在不同变量类型、
// 变量缺失、值为 nil、let 局部名与同名变量并存时，结果与每次重新编译一致。
func TestEvalExprCacheFollowsTypes(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		vars    map[string]any
		want    any
		wantErr bool
	}{
		{"整数相加", "p + q", map[string]any{"p": 1, "q": 2}, 3, false},
		{"同一原文换成字符串", "p + q", map[string]any{"p": "a", "q": "b"}, "ab", false},
		{"同一原文类型不匹配", "p + q", map[string]any{"p": "a", "q": 2}, nil, true},
		{"同一原文变量缺失", "p + q", map[string]any{"p": 1}, nil, true},
		{"再次回到整数仍命中正确结果", "p + q", map[string]any{"p": 5, "q": 6}, 11, false},
		{"值为 nil 与缺失区分", "p == nil", map[string]any{"p": nil}, true, false},
		{"缺失时报错", "p == nil", map[string]any{}, nil, true},
		{"let 局部名", "let z = p * 2; z + 1", map[string]any{"p": 3}, 7, false},
		// expr-lang 不允许 let 重新声明变量表中已有的名字，重新编译同样报错。
		{"let 局部名与同名变量冲突", "let z = p * 2; z + 1", map[string]any{"p": 3, "z": "s"}, nil, true},
		{"冲突之后去掉同名变量恢复正常", "let z = p * 2; z + 1", map[string]any{"p": 4}, 9, false},
	}
	for _, c := range cases {
		got, err := EvalExpr(c.src, c.vars)
		if c.wantErr {
			if err == nil {
				t.Fatalf("%s: want error, got %v", c.name, got)
			}
			continue
		}
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got != c.want {
			t.Fatalf("%s: got %v (%T) want %v", c.name, got, got, c.want)
		}
	}
}

// TestEvalExprConcurrent 验证并发求值同一原文、不同类型组合时缓存安全（配合 -race）。
func TestEvalExprConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := range 32 {
		wg.Go(func() {
			vars := map[string]any{"p": i, "q": 1}
			if i%2 == 1 {
				vars = map[string]any{"p": float64(i), "q": 1.5}
			}
			for range 100 {
				if _, err := EvalExpr("p * 2 + q", vars); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}

// TestValidateExprCached 验证定义期检查结果按原文缓存，错误在重复调用时保持一致。
func TestValidateExprCached(t *testing.T) {
	for range 2 {
		if err := ValidateExpr("a +"); err == nil {
			t.Fatal("want syntax error")
		}
		if err := ValidateExpr("undefined_name + 1"); err != nil {
			t.Fatalf("undefined names are allowed at definition time: %v", err)
		}
	}
}

func BenchmarkEvalExpr(b *testing.B) {
	vars := map[string]any{"x": 10, "y": 20}
	for b.Loop() {
		_, _ = EvalExpr("x + y", vars)
	}
}
