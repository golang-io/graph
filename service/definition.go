package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/engine"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// BundleKind 是图定义文件（example/*.json、导入导出）的 Kind 取值。
const BundleKind = "graph"

// Definition 同表保存当前图与历史版本（表 tb_graph_definition）。
// GraphID 是图身份，GraphID + Version 唯一确定一个版本；RowID 是版本行 UUID 主键，只用于落盘。
// 不保存内容哈希：判断内容是否变化、核对请求中的图与版本是否一致时，都由 Graph / UI 实时计算。
// 列名、JSON 键与字段名相同；每个字段显式写 column 标签，列名不依赖 NamingStrategy。
// Graph / UI 不写 type 标签，原因同 engine 包执行表（MySQL 的 TEXT 最多 64KB）。
type Definition struct {
	RowID       string      `gorm:"column:RowID;primaryKey;size:64" json:"-"`
	GraphID     string      `gorm:"column:GraphID;size:64;uniqueIndex:IdxDefinitionVersion,priority:1" json:"GraphID"`
	Version     int64       `gorm:"column:Version;uniqueIndex:IdxDefinitionVersion,priority:2" json:"Version"`
	Name        string      `gorm:"column:Name;size:256" json:"Name,omitempty"`
	Description string      `gorm:"column:Description;size:2048" json:"Description,omitempty"`
	Graph       graph.Graph `gorm:"column:Graph;serializer:graphjson" json:"Graph"`
	UI          graph.UI    `gorm:"column:UI;serializer:graphjson" json:"UI,omitzero"`
	Engine      string      `gorm:"column:Engine;size:64" json:"Engine,omitempty"` // 默认执行引擎
	Label       string      `gorm:"column:Label;size:512" json:"Label,omitempty"`
	Deleted     bool        `gorm:"column:Deleted" json:"-"`
	CreatedAt   time.Time   `gorm:"column:CreatedAt" json:"CreatedAt"`
	UpdatedAt   time.Time   `gorm:"column:UpdatedAt" json:"UpdatedAt"`
}

// TableName 固定表名。
func (Definition) TableName() string { return "tb_graph_definition" }

// BeforeCreate 在插入前补齐版本行 UUID 主键，避免数据库自增整数主键。
func (d *Definition) BeforeCreate(_ *gorm.DB) error {
	if strings.TrimSpace(d.RowID) == "" {
		d.RowID = uuid.New().String()
	}
	return nil
}

// byVersionDesc 按版本倒序；列名通过 clause.Column 生成，保证在 PostgreSQL 下正确加引号。
var byVersionDesc = clause.OrderByColumn{Column: clause.Column{Name: "Version"}, Desc: true}

// latest 查询某个图的最新一行（含已删除标记）。
func (s *Store) latest(tx *gorm.DB, graphID string) (Definition, error) {
	var row Definition
	err := tx.Where(map[string]any{"GraphID": graphID}).Order(byVersionDesc).Take(&row).Error
	return row, err
}

// Save 内容变化时追加版本；内容不变时只更新名称等元数据。
// 「内容」指图 + 布局（graph.HashContent），与最新已保存版本（Version>0）实时比较；
// 草稿（Version 0）与首次保存一律追加版本。
// 请求中 Graph 自带的 GraphID / GraphVersion 一律覆盖：新版本行的 Version 列与 Graph.GraphVersion 用同一个值写入。
// 画布布局必须为每个节点（含 map 循环体中的节点）的每个端点写明所在边，缺少时返回 ErrInvalid；失效的端点条目被清除。
func (s *Store) Save(ctx context.Context, def Definition) (*Definition, error) {
	def.GraphID = strings.TrimSpace(def.GraphID)
	if !graph.ValidID(def.GraphID) {
		return nil, fmt.Errorf("%w: GraphID required", engine.ErrInvalid)
	}
	// 写库前校验画布文档（执行模式、轨道取值、便签几何）：读库经 ParseUI 严格校验，
	// 写库不校验会把脏数据落盘，后续读取报 ErrCorrupt（stored state unavailable）。
	if err := def.UI.Validate(); err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	// 校验与布局规整共用同一份解码参数（见 Registry.ValidateLayout），顶层节点只解码一次。
	// 每个端点都要写明所在边；已不存在的端点（如删掉的 switch 分支）条目在这里清除，不进入新版本。
	_, layout, err := s.nodes.ValidateLayout(def.Graph, def.UI.Layout)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", engine.ErrInvalid, err)
	}
	def.UI.Layout = layout
	def.UI = graph.CleanUI(def.UI)
	var row Definition
	err = retryConflict(ctx, func() error {
		return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			var err error
			// 锁住最新行，同一图的并发保存在已有版本时串行；首个版本没有可锁的行，靠 retryConflict 重做。
			row, err = s.latest(engine.Locking(tx), def.GraphID)
			if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			if row.Deleted {
				return fmt.Errorf("%w: graph is deleted", engine.ErrConflict)
			}
			if row.GraphID == "" {
				row.GraphID, row.Name = def.GraphID, def.GraphID
			}
			if name := strings.TrimSpace(def.Name); name != "" {
				row.Name = name
			}
			if strings.TrimSpace(def.Description) != "" {
				row.Description = def.Description
			}
			if engine := strings.TrimSpace(def.Engine); engine != "" {
				row.Engine = engine
			}
			if def.Label != "" {
				row.Label = def.Label
			}
			row.UpdatedAt = time.Now().UTC()
			// 候选身份设为最新版本：内容不变时本次保存就是最新版本，此时两边哈希相等。
			def.Graph.GraphID, def.Graph.GraphVersion = def.GraphID, row.Version
			contentHash, err := graph.HashContent(def.Graph, def.UI)
			if err != nil {
				return fmt.Errorf("%w: %v", engine.ErrInvalid, err)
			}
			if row.Version > 0 {
				// 旧行的哈希计算失败（库中数据不合法）时按内容已变化处理，追加新版本覆盖它。
				if prev, err := graph.HashContent(row.Graph, row.UI); err == nil && prev == contentHash {
					return tx.Model(&row).Select("Name", "Description", "Engine", "Label", "UpdatedAt").Updates(&row).Error
				}
			}
			row.RowID, row.Version = "", row.Version+1
			row.Graph, row.UI = def.Graph, def.UI
			row.Graph.GraphVersion = row.Version
			row.CreatedAt = row.UpdatedAt
			return tx.Create(&row).Error
		})
	})
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// saveAttempts 是 Save 遇到并发冲突时整个事务的最多尝试次数。
const saveAttempts = 5

// retryConflict 执行 fn；遇到并发冲突时等待后重做，最多 saveAttempts 次。
// 并发冲突包括：两个事务读到同一最新版本后插入相同版本号（唯一索引冲突），
// 以及 MySQL 对不存在的行加锁产生的间隙锁死锁、锁等待超时、SQLite busy。
// 每次重做都重新读取最新版本，因此重做后分配的是新的版本号。
//
// retryConflict reruns fn on unique-key or lock conflicts.
func retryConflict(ctx context.Context, fn func() error) error {
	for attempt := 1; ; attempt++ {
		err := fn()
		if err == nil || attempt >= saveAttempts || !(engine.DuplicateKey(err) || engine.Retryable(err)) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt*attempt*5) * time.Millisecond):
		}
	}
}

// Create 建立 Version=0 的未保存空图，GraphID 自动生成；图身份为 (GraphID, 0)。
func (s *Store) Create(ctx context.Context, name, description string) (*Definition, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: Name required", engine.ErrInvalid)
	}
	now := time.Now().UTC()
	id := uuid.New().String()
	row := Definition{GraphID: id, Name: name, Description: description,
		Graph: graph.Graph{GraphID: id, Nodes: []graph.Node{}, Edges: []graph.Edge{}}, UI: graph.UI{}, CreatedAt: now, UpdatedAt: now}
	if err := s.db.WithContext(ctx).Create(&row).Error; err != nil {
		return nil, err
	}
	return &row, nil
}

// Get 先取最新记录，再判断删除状态，避免旧版本重新成为当前图。
func (s *Store) Get(ctx context.Context, graphID string) (*Definition, error) {
	row, err := s.latest(s.db.WithContext(ctx), graphID)
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && row.Deleted) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// List 每个图只返回最新的未删除记录，按 UpdatedAt 倒序。
func (s *Store) List(ctx context.Context) ([]Definition, error) {
	var rows []Definition
	q := s.db.Statement.Quote
	// 相关子查询：不存在同 GraphID 的更高版本，即为最新行。标识符统一加引号。
	newest := fmt.Sprintf("NOT EXISTS (SELECT 1 FROM tb_graph_definition newer WHERE newer.%[1]s = tb_graph_definition.%[1]s AND newer.%[2]s > tb_graph_definition.%[2]s)",
		q("GraphID"), q("Version"))
	err := s.db.WithContext(ctx).Where(newest).Where(map[string]any{"Deleted": false}).
		Order(clause.OrderBy{Columns: []clause.OrderByColumn{
			{Column: clause.Column{Name: "UpdatedAt"}, Desc: true},
			{Column: clause.Column{Name: "RowID"}, Desc: true},
		}}).Find(&rows).Error
	return rows, err
}

// Delete 标记当前图为删除；版本快照与已有 Task 继续保留。
func (s *Store) Delete(ctx context.Context, graphID string) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := s.latest(tx, graphID)
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return engine.ErrNotFound
		}
		if err != nil {
			return err
		}
		return tx.Model(&Definition{}).Where(map[string]any{"RowID": row.RowID}).Update("Deleted", true).Error
	})
}

// ListVersions 返回已保存版本（Version>0），按版本倒序。
func (s *Store) ListVersions(ctx context.Context, graphID string, limit int) ([]Definition, error) {
	rows := []Definition{}
	q := s.db.WithContext(ctx).Where(map[string]any{"GraphID": graphID}).
		Where(clause.Gt{Column: clause.Column{Name: "Version"}, Value: 0}).Order(byVersionDesc)
	if limit > 0 {
		q = q.Limit(limit)
	}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

// GetVersion 按图身份与版本号读取快照；删除当前图不影响历史读取。
func (s *Store) GetVersion(ctx context.Context, graphID string, version int64) (*Definition, error) {
	if version < 1 {
		return nil, fmt.Errorf("%w: Version must be positive", engine.ErrInvalid)
	}
	var row Definition
	err := s.db.WithContext(ctx).Where(map[string]any{"GraphID": graphID, "Version": version}).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, engine.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// EnsureRunVersion 返回可执行的当前版本：必须已保存且非空。
func (s *Store) EnsureRunVersion(ctx context.Context, graphID string) (*Definition, error) {
	def, err := s.Get(ctx, graphID)
	if err != nil {
		return nil, err
	}
	if def.Version < 1 || len(def.Graph.Nodes) == 0 {
		return nil, fmt.Errorf("%w: save graph before running", engine.ErrNotFound)
	}
	return def, nil
}

// ResolveGraph 实现 GraphResolver：子图固定到当前已保存版本，返回的图自带 (GraphID, Version)。
func (s *Store) ResolveGraph(ctx context.Context, graphID string) (graph.Graph, error) {
	def, err := s.EnsureRunVersion(ctx, graphID)
	if err != nil {
		return graph.Graph{}, err
	}
	return def.Graph, nil
}

// EngineFor 返回图配置的默认执行引擎；图不存在时返回空串。
func (s *Store) EngineFor(ctx context.Context, graphID string) string {
	def, err := s.Get(ctx, graphID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(def.Engine)
}

// SetEngine 修改图的默认执行引擎（只改最新行，不追加版本）。
func (s *Store) SetEngine(ctx context.Context, graphID, kind string) (*Definition, error) {
	kind = strings.TrimSpace(kind)
	if kind != "" && !graph.ValidID(kind) {
		return nil, fmt.Errorf("%w: invalid engine kind %q", engine.ErrInvalid, kind)
	}
	var row *Definition
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		latest, err := s.latest(tx, graphID)
		if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && latest.Deleted) {
			return engine.ErrNotFound
		}
		if err != nil {
			return err
		}
		latest.Engine, latest.UpdatedAt = kind, time.Now().UTC()
		row = &latest
		return tx.Model(row).Select("Engine", "UpdatedAt").Updates(row).Error
	})
	return row, err
}
