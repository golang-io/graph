package jsonx

import (
	"encoding/json"
	"testing"
)

// TestDecodePreservesDynamicNumbers 确认动态值里的大整数保持 int64，且重复键、越界整数、尾随值被拒绝。
func TestDecodePreservesDynamicNumbers(t *testing.T) {
	var target struct {
		Vars map[string]any  `json:"vars"`
		Raw  json.RawMessage `json:"raw"`
	}
	if err := Decode([]byte(`{"vars":{"n":9007199254740993,"nested":[{"n":9223372036854775807}],"decimal":1.25},"raw":{"n":9007199254740993}}`), &target); err != nil {
		t.Fatal(err)
	}
	if target.Vars["n"] != int64(9007199254740993) || target.Vars["decimal"] != float64(1.25) {
		t.Fatalf("vars=%#v", target.Vars)
	}
	if target.Vars["nested"].([]any)[0].(map[string]any)["n"] != int64(9223372036854775807) {
		t.Fatal("nested integer lost")
	}
	if string(target.Raw) != `{"n":9007199254740993}` {
		t.Fatalf("raw=%s", target.Raw)
	}
	for _, raw := range []string{`{"n":1,"n":2}`, `{"n":9223372036854775808}`, `{"n":1} {}`} {
		var object map[string]any
		if err := Decode([]byte(raw), &object); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

// TestDecodeStoredExponentIsFloat 确认 json.Marshal 写出的指数形式小数（如 1e-7）读回为 float64，而不是按整数解析失败。
func TestDecodeStoredExponentIsFloat(t *testing.T) {
	raw, _ := json.Marshal(map[string]any{"small": 1e-7, "big": 1e21})
	var out map[string]any
	if err := DecodeStored(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["small"] != 1e-7 || out["big"] != 1e21 {
		t.Fatalf("out=%#v", out)
	}
}

// dupKeys 的 MarshalJSON 产出重复键，用于确认 CanonicalObject 仍做严格检查。
type dupKeys struct{}

func (dupKeys) MarshalJSON() ([]byte, error) { return []byte(`{"a":1,"a":2}`), nil }

// TestCanonicalObject 确认一次处理完成可序列化检查与类型规整，nil 保持 nil，返回值不与入参共享。
func TestCanonicalObject(t *testing.T) {
	in := map[string]any{"n": 3, "f": 1e-7, "list": []int{1}}
	out, err := CanonicalObject(in)
	if err != nil {
		t.Fatal(err)
	}
	if out["n"] != int64(3) || out["f"] != 1e-7 || out["list"].([]any)[0] != int64(1) {
		t.Fatalf("out=%#v", out)
	}
	if out, err := CanonicalObject(nil); out != nil || err != nil {
		t.Fatalf("nil: out=%#v err=%v", out, err)
	}
	if out, err := CanonicalObject(map[string]any{}); out == nil || len(out) != 0 || err != nil {
		t.Fatalf("empty: out=%#v err=%v", out, err)
	}
	for _, bad := range []map[string]any{{"c": make(chan int)}, {"d": dupKeys{}}, {"u": uint64(1 << 63)}} {
		if _, err := CanonicalObject(bad); err == nil {
			t.Fatalf("accepted %#v", bad)
		}
	}
}
