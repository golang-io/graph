package jsonx

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
)

// Decode 先经 Strict 校验，再解码到 target；动态值中的整数保持 int64。
// Decode validates raw with Strict and decodes it, keeping integers as int64.
func Decode(raw []byte, target any) error {
	raw, err := Strict(raw)
	if err != nil {
		return err
	}
	return decodeKeepingInts(raw, target)
}

// DecodeStored 解码引擎自己写入数据库的 JSON（Run 变量等）：同样保留 int64，但不经 Strict 规范化。
// DecodeStored decodes engine-written JSON, keeping int64 without Strict canonicalization.
func DecodeStored(raw []byte, target any) error {
	return decodeKeepingInts(raw, target)
}

func decodeKeepingInts(raw []byte, target any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(target); err != nil {
		return err
	}
	return NormalizeNumbers(target)
}

// NormalizeNumbers 把 target 内的 json.Number 转为 int64/float64（就地修改）。
// 含小数点或指数（json.Marshal 对 |x|<1e-6 或 >=1e21 的 float64 输出 1e-7 这类写法）的为 float64，其余为 int64。
// 供使用 UseNumber 解码（如开启 DisallowUnknownFields）的调用方在解码后统一规整数字。
func NormalizeNumbers(target any) error {
	return decodeNumbers(reflect.ValueOf(target))
}

func decodeNumbers(v reflect.Value) error {
	switch v.Kind() {
	case reflect.Pointer:
		if !v.IsNil() {
			return decodeNumbers(v.Elem())
		}
	case reflect.Interface:
		if v.IsNil() {
			return nil
		}
		if n, ok := v.Interface().(json.Number); ok {
			var number any
			var err error
			if strings.ContainsAny(string(n), ".eE") {
				number, err = n.Float64()
			} else {
				number, err = n.Int64()
			}
			if err != nil {
				return err
			}
			v.Set(reflect.ValueOf(number))
			return nil
		}
		return decodeNumbers(v.Elem())
	case reflect.Map:
		for _, key := range v.MapKeys() {
			item := reflect.New(v.Type().Elem()).Elem()
			item.Set(v.MapIndex(key))
			if err := decodeNumbers(item); err != nil {
				return err
			}
			v.SetMapIndex(key, item)
		}
	case reflect.Slice:
		for i := 0; i < v.Len(); i++ {
			if err := decodeNumbers(v.Index(i)); err != nil {
				return err
			}
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Field(i).CanSet() {
				if err := decodeNumbers(v.Field(i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
