package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	"github.com/golang-io/graph/example"
)

// ExampleIDs 返回内置样例的 GraphID 列表（即 example/*.json 的文件名）。
func ExampleIDs() ([]string, error) {
	entries, err := fs.ReadDir(example.FS, ".")
	if err != nil {
		return nil, fmt.Errorf("graph/service: list examples: %w", err)
	}
	var ids []string
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
			continue
		}
		ids = append(ids, strings.TrimSuffix(ent.Name(), ".json"))
	}
	sort.Strings(ids)
	return ids, nil
}

// exampleFile 是样例文件结构：Definition 字段加 Kind。
type exampleFile struct {
	Definition
	Kind string
}

// readDefinition 严格解码一份样例：未知字段直接报错，保证样例与当前结构一致。
func readDefinition(id string) (Definition, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return Definition{}, fmt.Errorf("graph/service: GraphID required")
	}
	path := id + ".json"
	raw, err := example.FS.ReadFile(path)
	if err != nil {
		return Definition{}, fmt.Errorf("graph/service: read example %s: %w", path, err)
	}
	var file exampleFile
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return Definition{}, fmt.Errorf("graph/service: parse example %s: %w", path, err)
	}
	g := file.Definition
	g.GraphID = strings.TrimSpace(g.GraphID)
	if g.GraphID != id {
		return Definition{}, fmt.Errorf("graph/service: example %s GraphID mismatch %q", path, g.GraphID)
	}
	if file.Kind != BundleKind {
		return Definition{}, fmt.Errorf("graph/service: example %s Kind=%q", path, file.Kind)
	}
	if strings.TrimSpace(g.Name) == "" {
		return Definition{}, fmt.Errorf("graph/service: example %s Name required", path)
	}
	return g, nil
}

// ExampleGraph 读取 graph/example 全部样例，先物理清空同 GraphID 的全部历史版本，再 Save 写入最新内容。
// demo 启动即与仓库内 JSON 对齐；不做「内容相同则跳过」的兼容保留。
func ExampleGraph(ctx context.Context, s *Store) error {
	ids, err := ExampleIDs()
	if err != nil {
		return err
	}
	for _, id := range ids {
		g, err := readDefinition(id)
		if err != nil {
			return err
		}
		// 删光该样例全部版本行，再写入（Save 内部执行完整 ValidateLayout 校验），避免旧历史版本残留。
		if err := s.db.WithContext(ctx).Where(map[string]any{"GraphID": id}).Delete(&Definition{}).Error; err != nil {
			return fmt.Errorf("graph/service: reset example %s: %w", id, err)
		}
		if _, err := s.Save(ctx, g); err != nil {
			return fmt.Errorf("graph/service: example %s: %w", id, err)
		}
	}
	return nil
}
