package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
	"gorm.io/gorm/schema"
)

// graphJSONSerializer 是执行表与定义表 JSON 列的 GORM 序列化器（标签 serializer:graphjson）。
// 与 GORM 自带 json 序列化器的区别：读取时整数保持 int64（jsonx.DecodeStored），
// 与 HTTP 入参解码一致，避免同一变量在两个引擎中一个是 int64、一个是 float64；
// 对 graph.Graph 与 graph.UI 统一补齐空切片/空映射并处理旧键兼容，使 graph 根包不依赖 database/sql。
//
// graphJSONSerializer stores JSON columns and decodes integers as int64.
type graphJSONSerializer struct{}

func init() {
	schema.RegisterSerializer("graphjson", graphJSONSerializer{})
}

// Scan 把数据库值解码到字段；NULL 或空串得到零值（UI 规范化为非 nil 空映射，Graph 补齐非 nil 空切片）。
func (graphJSONSerializer) Scan(ctx context.Context, field *schema.Field, dst reflect.Value, dbValue any) error {
	target := reflect.New(field.FieldType)
	var raw []byte
	switch v := dbValue.(type) {
	case nil:
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return fmt.Errorf("graph runtime: unsupported JSON column value %T", dbValue)
	}
	switch ptr := target.Interface().(type) {
	case *graph.UI:
		parsed, err := graph.ParseUI(raw)
		if err != nil {
			return fmt.Errorf("%w: decode %s: %v", ErrCorrupt, field.Name, err)
		}
		*ptr = parsed
	case *graph.Graph:
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
			if err := jsonx.DecodeStored(trimmed, ptr); err != nil {
				return fmt.Errorf("%w: decode %s: %v", ErrCorrupt, field.Name, err)
			}
		}
		if ptr.Nodes == nil {
			ptr.Nodes = []graph.Node{}
		}
		if ptr.Edges == nil {
			ptr.Edges = []graph.Edge{}
		}
	default:
		if len(raw) > 0 {
			if err := jsonx.DecodeStored(raw, target.Interface()); err != nil {
				return fmt.Errorf("%w: decode %s: %v", ErrCorrupt, field.Name, err)
			}
		}
	}
	field.ReflectValueOf(ctx, dst).Set(target.Elem())
	return nil
}

// Value 把字段编码为 JSON 文本；nil 写为 NULL。
func (graphJSONSerializer) Value(_ context.Context, _ *schema.Field, _ reflect.Value, fieldValue any) (any, error) {
	switch v := fieldValue.(type) {
	case graph.Graph:
		if v.Nodes == nil {
			v.Nodes = []graph.Node{}
		}
		if v.Edges == nil {
			v.Edges = []graph.Edge{}
		}
		fieldValue = v
	case graph.UI:
		raw, err := graph.MarshalUI(v)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		return string(raw), nil
	}
	raw, err := json.Marshal(fieldValue)
	if err != nil {
		return nil, fmt.Errorf("%w: value is not JSON serializable: %v", ErrInvalid, err)
	}
	if string(raw) == "null" {
		return nil, nil
	}
	return string(raw), nil
}
