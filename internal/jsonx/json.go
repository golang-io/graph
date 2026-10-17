// Package jsonx 是 graph 模块内部的 JSON 工具：面向用户输入的严格解码、规范化编码与内容摘要，
// 整数保持 int64 的解码，以及 jsonschema.Schema 的深拷贝与属性名读取。
// 它不含图的业务语义，只供本模块各包共用，不对外暴露。
//
// Package jsonx holds the module-internal JSON helpers: strict decoding, canonical encoding,
// content digests, int64-preserving decoding and jsonschema.Schema helpers.
package jsonx

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"
)

// 规范 JSON：同一份内容必须编码出同一串字节，否则内容寻址的哈希就不可信。
// 两套引擎与版本仓都走这里，保证 definition_hash 跨引擎一致。

// Digest 返回内容的 sha256 十六进制摘要（definition_hash / content_hash 使用）。
func Digest(data []byte) string { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }

// Strict 校验并规范化 JSON：拒绝空输入、重复 key 与尾随内容，归一化数字写法，
// 且不让整数经过 float64 损失精度。同一内容总是得到同一串字节，供内容哈希使用。
func Strict(raw []byte) ([]byte, error) {
	v, err := strictValue(raw)
	if err != nil {
		return nil, err
	}
	encoded, err := jsonv2.Marshal(v, jsonv2.Deterministic(true))
	if err != nil {
		return nil, fmt.Errorf("graph: encode JSON: %w", err)
	}
	return encoded, nil
}

// CheckValid 按 Strict 规则在字节切片上校验 raw 是否为合法 JSON（拒绝空输入、重复 key、越界数字与尾随内容）。
func CheckValid(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("graph: empty JSON")
	}
	dec := jsontext.NewDecoder(bytes.NewReader(raw))
	topLevelRead := false
	var depth int
	for {
		tok, err := dec.ReadToken()
		if err != nil {
			if err == io.EOF {
				if !topLevelRead || depth != 0 {
					return fmt.Errorf("graph: invalid JSON")
				}
				return nil
			}
			return fmt.Errorf("graph: invalid JSON: %w", err)
		}
		if topLevelRead && depth == 0 {
			return fmt.Errorf("graph: trailing JSON value")
		}
		switch tok.Kind() {
		case '{', '[':
			depth++
		case '}', ']':
			depth--
			if depth == 0 {
				topLevelRead = true
			}
		case '0':
			if err := checkNumberString(tok.String()); err != nil {
				return err
			}
			if depth == 0 {
				topLevelRead = true
			}
		default:
			if depth == 0 {
				topLevelRead = true
			}
		}
	}
}

func checkNumberString(src string) error {
	if len(src) > 64 {
		return fmt.Errorf("graph: numeric literal out of range")
	}
	if !strings.ContainsAny(src, ".eE") {
		if _, err := strconv.ParseInt(src, 10, 64); err != nil {
			return fmt.Errorf("graph: integer outside int64 range")
		}
		return nil
	}
	f, err := strconv.ParseFloat(src, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return fmt.Errorf("graph: non-finite JSON number")
	}
	if f == 0 {
		for i := 0; i < len(src); i++ {
			b := src[i]
			if b == 'e' || b == 'E' {
				break
			}
			if b >= '1' && b <= '9' {
				return fmt.Errorf("graph: non-finite JSON number")
			}
		}
	}
	return nil
}

// strictValue 按 Strict 的规则解出 JSON 值：对象为 map[string]any、数组为 []any，数字为归一化写法的 json.Number。
func strictValue(raw []byte) (any, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("graph: empty JSON")
	}
	dec := jsontext.NewDecoder(bytes.NewReader(trimmed))
	v, err := readStrictJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err = dec.ReadToken(); err != io.EOF {
		return nil, fmt.Errorf("graph: trailing JSON value")
	}
	return v, nil
}

func readStrictJSONValue(dec *jsontext.Decoder) (any, error) {
	tok, err := dec.ReadToken()
	if err != nil {
		return nil, fmt.Errorf("graph: invalid JSON: %w", err)
	}
	switch tok.Kind() {
	case '{':
		out := map[string]any{}
		for dec.PeekKind() != '}' {
			keyTok, err := dec.ReadToken()
			if err != nil {
				return nil, err
			}
			keyStr := keyTok.String()
			val, err := readStrictJSONValue(dec)
			if err != nil {
				return nil, err
			}
			out[keyStr] = val
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		return out, nil
	case '[':
		var out []any
		for dec.PeekKind() != ']' {
			val, err := readStrictJSONValue(dec)
			if err != nil {
				return nil, err
			}
			out = append(out, val)
		}
		if _, err := dec.ReadToken(); err != nil {
			return nil, err
		}
		if out == nil {
			return []any{}, nil
		}
		return out, nil
	case '0':
		text, err := normalizeNumber(tok.String())
		if err != nil {
			return nil, err
		}
		return json.Number(text), nil
	case '"':
		return tok.String(), nil
	case 't':
		return true, nil
	case 'f':
		return false, nil
	case 'n':
		return nil, nil
	default:
		return nil, fmt.Errorf("graph: unsupported JSON token")
	}
}

// normalizeNumber 归一化数字写法，并校验 int64 / float64 有效范围。
func normalizeNumber(src string) (string, error) {
	if len(src) > 64 {
		return "", fmt.Errorf("graph: numeric literal out of range")
	}
	if !strings.ContainsAny(src, ".eE") {
		n, err := strconv.ParseInt(src, 10, 64)
		if err != nil {
			return "", fmt.Errorf("graph: integer outside int64 range")
		}
		return strconv.FormatInt(n, 10), nil
	}
	f, err := strconv.ParseFloat(src, 64)
	mantissa, _, _ := strings.Cut(strings.ToLower(src), "e")
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) || (f == 0 && strings.ContainsAny(mantissa, "123456789")) {
		return "", fmt.Errorf("graph: non-finite JSON number")
	}
	return strconv.FormatFloat(f, 'g', -1, 64), nil
}

// Canonical 序列化结构体后再做严格规范化：同一内容总是得到同一串字节。
func Canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("graph: value is not JSON serializable: %w", err)
	}
	return Strict(raw)
}

// CanonicalObject 把 m 序列化一次并按 Strict 规则检查，返回新分配的 JSON 形态副本：
// 整数为 int64、小数为 float64、对象为 map[string]any、数组为 []any。m 为 nil 时返回 nil。
// 节点输出与运行输入用它在一次处理中完成可序列化检查与类型规整。
//
// CanonicalObject validates m with Strict rules and returns a fresh JSON-shaped copy.
func CanonicalObject(m map[string]any) (map[string]any, error) {
	if m == nil {
		return nil, nil
	}
	if fast, ok := canonicalMapFast(m); ok {
		return fast, nil
	}
	raw, err := json.Marshal(m)
	if err != nil {
		return nil, fmt.Errorf("graph: value is not JSON serializable: %v", err)
	}
	v, err := strictValue(raw)
	if err != nil {
		return nil, err
	}
	out, _ := v.(map[string]any)
	if err := NormalizeNumbers(&out); err != nil {
		return nil, err
	}
	return out, nil
}

// canonicalMapFast 对只包含标准动态类型（nil、bool、合法 UTF-8 string、int/int64、map[string]any、[]any）的 map
// 直接深拷贝并规整整数为 int64，省去 JSON 序列化与反序列化往返；遇到其他类型时返回 false 交由完整路径处理。
func canonicalMapFast(m map[string]any) (map[string]any, bool) {
	out := make(map[string]any, len(m))
	for k, v := range m {
		if !utf8.ValidString(k) {
			return nil, false
		}
		cv, ok := canonicalValueFast(v)
		if !ok {
			return nil, false
		}
		out[k] = cv
	}
	return out, true
}

func canonicalValueFast(v any) (any, bool) {
	switch x := v.(type) {
	case nil, bool, int64:
		return x, true
	case int:
		return int64(x), true
	case string:
		if !utf8.ValidString(x) {
			return nil, false
		}
		return x, true
	case map[string]any:
		if x == nil {
			return nil, true
		}
		return canonicalMapFast(x)
	case []any:
		if x == nil {
			return nil, true
		}
		out := make([]any, len(x))
		for i, item := range x {
			cv, ok := canonicalValueFast(item)
			if !ok {
				return nil, false
			}
			out[i] = cv
		}
		return out, true
	default:
		return nil, false
	}
}
