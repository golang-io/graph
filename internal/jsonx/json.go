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

// normalizeNumber 归一化数字写法，并拒绝无法无损往返的精度。
func normalizeNumber(src string) (string, error) {
	out, err := decimalSpelling(src)
	if err != nil {
		return "", err
	}
	if !strings.Contains(out, ".") {
		if _, err = strconv.ParseInt(out, 10, 64); err != nil {
			return "", fmt.Errorf("graph: integer outside int64 range")
		}
		return out, nil
	}
	f, err := strconv.ParseFloat(out, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return "", fmt.Errorf("graph: non-finite JSON number")
	}
	roundtrip, err := decimalSpelling(strconv.FormatFloat(f, 'g', -1, 64))
	if err != nil || roundtrip != out {
		return "", fmt.Errorf("graph: unsupported JSON decimal precision")
	}
	return out, nil
}

// decimalSpelling 把科学计数法与冗余零展开成唯一的十进制写法。
func decimalSpelling(src string) (string, error) {
	negative := strings.HasPrefix(src, "-")
	src = strings.TrimPrefix(src, "-")
	exponent := 0
	if i := strings.IndexAny(src, "eE"); i >= 0 {
		n, err := strconv.Atoi(src[i+1:])
		if err != nil {
			return "", fmt.Errorf("graph: invalid numeric exponent")
		}
		exponent = n
		src = src[:i]
	}
	point := len(src)
	if i := strings.IndexByte(src, '.'); i >= 0 {
		point = i
		src = src[:i] + src[i+1:]
	}
	point += exponent
	var out string
	if point <= 0 {
		out = "0." + strings.Repeat("0", -point) + src
	} else if point >= len(src) {
		out = src + strings.Repeat("0", point-len(src))
	} else {
		out = src[:point] + "." + src[point:]
	}
	parts := strings.SplitN(out, ".", 2)
	parts[0] = strings.TrimLeft(parts[0], "0")
	if parts[0] == "" {
		parts[0] = "0"
	}
	out = parts[0]
	if len(parts) == 2 {
		fraction := strings.TrimRight(parts[1], "0")
		if fraction != "" {
			out += "." + fraction
		}
	}
	if negative && out != "0" {
		out = "-" + out
	}
	return out, nil
}
