package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/internal/jsonx"
)

// decodeRequest 读取请求体并经 jsonx.Strict 校验后解码；整数解码为 int64。
// 键名与 Go 字段名一致，未声明的键（如 snake_case 的 graph_id）直接拒绝，不会被静默忽略。
// optional 为 true 时允许空请求体（如不带参数的 resume / cancel）。解码失败返回 ErrInvalid。
func decodeRequest(r *http.Request, target any, optional bool) error {
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return fmt.Errorf("%w: read body: %v", engine.ErrInvalid, err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		if optional {
			return nil
		}
		return fmt.Errorf("%w: request body required", engine.ErrInvalid)
	}
	data, err := jsonx.Strict(raw)
	if err != nil {
		return fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	// 一次解码同时完成：拒绝未知键、数字先保留为 json.Number，随后统一规整为 int64 / float64。
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	if err := jsonx.NormalizeNumbers(target); err != nil {
		return fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	return nil
}

// graphStatus 把查询参数转为状态值（原样，大写）。
func graphStatus(s string) graph.StatusCode {
	return graph.StatusCode(strings.ToUpper(strings.TrimSpace(s)))
}
