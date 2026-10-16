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
	encoded, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("graph: encode JSON: %w", err)
	}
	return encoded, nil
}

// CheckValid 按 Strict 规则直接在字节切片上校验 raw 是否为合法 JSON（拒绝空输入、重复 key、越界数字与尾随内容），
// 不构造流式 json.Decoder，不分配中间值树。
func CheckValid(raw []byte) error {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return fmt.Errorf("graph: empty JSON")
	}
	if !json.Valid(raw) {
		return fmt.Errorf("graph: invalid JSON")
	}
	_, err := checkValidValue(raw, 0)
	return err
}

// checkValidValue 在已通过 json.Valid 语法校验的字节切片上检查对象重复键与数字有效范围。
func checkValidValue(raw []byte, i int) (int, error) {
	i = skipWS(raw, i)
	switch raw[i] {
	case '{':
		i = skipWS(raw, i+1)
		if raw[i] == '}' {
			return i + 1, nil
		}
		var smallRaw [16][]byte
		smallCount := 0
		var seen map[string]struct{}
		for {
			keyStart := i
			keyEnd, hasEsc := scanString(raw, keyStart)
			if !hasEsc && seen == nil && smallCount < len(smallRaw) {
				keyBytes := raw[keyStart+1 : keyEnd-1]
				for k := range smallCount {
					if bytes.Equal(smallRaw[k], keyBytes) {
						return 0, fmt.Errorf("graph: duplicate JSON key")
					}
				}
				smallRaw[smallCount] = keyBytes
				smallCount++
			} else {
				if seen == nil {
					seen = make(map[string]struct{}, smallCount+4)
					for k := range smallCount {
						seen[string(smallRaw[k])] = struct{}{}
					}
				}
				keyStr, err := decodeKeyString(raw[keyStart:keyEnd], hasEsc)
				if err != nil {
					return 0, err
				}
				if _, dup := seen[keyStr]; dup {
					return 0, fmt.Errorf("graph: duplicate JSON key")
				}
				seen[keyStr] = struct{}{}
			}
			i = skipWS(raw, keyEnd)
			i++ // ':'
			var err error
			i, err = checkValidValue(raw, i)
			if err != nil {
				return 0, err
			}
			i = skipWS(raw, i)
			if raw[i] == '}' {
				return i + 1, nil
			}
			i = skipWS(raw, i+1) // ','
		}
	case '[':
		i = skipWS(raw, i+1)
		if raw[i] == ']' {
			return i + 1, nil
		}
		for {
			var err error
			i, err = checkValidValue(raw, i)
			if err != nil {
				return 0, err
			}
			i = skipWS(raw, i)
			if raw[i] == ']' {
				return i + 1, nil
			}
			i++ // ','
		}
	case '"':
		end, _ := scanString(raw, i)
		return end, nil
	case 't', 'n':
		return i + 4, nil
	case 'f':
		return i + 5, nil
	default:
		end := scanNumber(raw, i)
		if err := checkNumberBytes(raw[i:end]); err != nil {
			return 0, err
		}
		return end, nil
	}
}

func skipWS(raw []byte, i int) int {
	for i < len(raw) {
		switch raw[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

// scanString 从 raw[i] == '"' 扫描到闭合引号之后的位置，并报告字符串内是否含转义符 '\\'。
func scanString(raw []byte, i int) (end int, hasEscape bool) {
	i++ // skip opening '"'
	for i < len(raw) {
		c := raw[i]
		if c == '"' {
			return i + 1, hasEscape
		}
		if c == '\\' {
			hasEscape = true
			i += 2
			continue
		}
		i++
	}
	return i, hasEscape
}

func scanNumber(raw []byte, i int) int {
	for i < len(raw) {
		c := raw[i]
		if (c >= '0' && c <= '9') || c == '-' || c == '+' || c == '.' || c == 'e' || c == 'E' {
			i++
			continue
		}
		break
	}
	return i
}

func decodeKeyString(quoted []byte, hasEsc bool) (string, error) {
	if !hasEsc {
		return string(quoted[1 : len(quoted)-1]), nil
	}
	var s string
	if err := json.Unmarshal(quoted, &s); err != nil {
		return "", fmt.Errorf("graph: invalid JSON")
	}
	return s, nil
}

func checkNumberBytes(src []byte) error {
	if len(src) > 64 {
		return fmt.Errorf("graph: numeric literal out of range")
	}
	if !bytes.ContainsAny(src, ".eE") {
		if _, err := strconv.ParseInt(string(src), 10, 64); err != nil {
			return fmt.Errorf("graph: integer outside int64 range")
		}
		return nil
	}
	f, err := strconv.ParseFloat(string(src), 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return fmt.Errorf("graph: non-finite JSON number")
	}
	if f == 0 {
		for _, b := range src {
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
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, fmt.Errorf("graph: empty JSON")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	v, err := readJSONValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err = dec.Token(); err != io.EOF {
		return nil, fmt.Errorf("graph: trailing JSON value")
	}
	return v, nil
}

// Canonical 序列化结构体后再做严格规范化：同一内容总是得到同一串字节。
func Canonical(v any) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("graph: value is not JSON serializable")
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

// readJSONValue 逐个 token 读出一个 JSON 值：对象拒绝重复 key，数字归一化写法。
func readJSONValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, fmt.Errorf("graph: invalid JSON")
	}
	switch v := token.(type) {
	case json.Delim:
		switch v {
		case '{':
			out := map[string]any{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return nil, err
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("graph: object key required")
				}
				if _, exists := out[key]; exists {
					return nil, fmt.Errorf("graph: duplicate JSON key")
				}
				value, err := readJSONValue(dec)
				if err != nil {
					return nil, err
				}
				out[key] = value
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim('}') {
				return nil, fmt.Errorf("graph: invalid object")
			}
			return out, nil
		case '[':
			out := []any{}
			for dec.More() {
				value, err := readJSONValue(dec)
				if err != nil {
					return nil, err
				}
				out = append(out, value)
			}
			end, err := dec.Token()
			if err != nil || end != json.Delim(']') {
				return nil, fmt.Errorf("graph: invalid array")
			}
			return out, nil
		}
	case json.Number:
		text, err := normalizeNumber(string(v))
		if err != nil {
			return nil, err
		}
		return json.Number(text), nil
	case nil, bool, string:
		return v, nil
	}
	return nil, fmt.Errorf("graph: unsupported JSON token")
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
