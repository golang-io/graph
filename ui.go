package graph

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/golang-io/graph/internal/jsonx"
)

// UI 是画布文档，与可执行 Graph 分开保存。
// 字段：主图层布局（匿名内嵌的 Layout）、试跑模式、便签注释。
// 图没有整体方向：每个节点的每个端点所在边写在 EndpointSides 中，保存时必须全部写明（见 node.Registry.CleanEndpointSides）。
// 断点、调试注入等未声明的键不属于这份文档，读入时丢弃。
//
// 注意：UI.EndpointSides 外层键为节点 ID，内层 EndpointSides 的键是执行图的本地端点名（In / Out / Case0 / Error 等），
// 值是该端点句柄在画布卡片上所在的边（left/right/top/bottom），EdgeRails 复用同一组取值；
// 只影响渲染，不影响执行，不参与 definition_hash。端点本身（有哪些端点）由节点种类声明，见 node.NodeSpec.Endpoints。
type UI struct {
	// Layout 是主图层的布局。匿名内嵌且不带 json tag，字段在 JSON 中展开到 UI 同一层。
	// 子图层（SubLayouts 的值）也是 Layout，前后端不另设 Body 类型。
	Layout
	ExecutionMode string       `json:"ExecutionMode,omitempty"`
	StickyNotes   []StickyNote `json:"StickyNotes,omitempty"`
}

// Layout 是一层画布作用域的布局：主图层（内嵌在 UI 中）与子图层（SubLayouts 的值）共用，结构递归。
// 主图层：Positions 为画布绝对坐标，EndpointSides、EdgeRails 的键是主图的节点 / 边 ID。
// 子图层：Positions 相对容器左上角，便于整体拖动容器；EndpointSides、EdgeRails 的键是子图内的本地节点 / 边 ID。
//
// Layout 不得实现 MarshalJSON / UnmarshalJSON：这些方法会提升到 UI 上，改变 UI 的序列化行为。
type Layout struct {
	// Collapsed 只对子图层有效：true 时容器收起成普通节点卡片，仅用于概览；缺省展开为可编辑的大框。
	// 主图层若出现该字段，CleanUI 清为 false，不进入规范化 JSON（也就不影响 HashContent）；渲染时忽略。
	Collapsed bool             `json:"Collapsed,omitempty"`
	Positions map[string]Point `json:"Positions,omitempty"`
	// EndpointSides 是各节点端点句柄所在的边（渲染用），不是执行端点声明；执行端点见 NodeSpec.Endpoints。
	// 键为本层节点 ID；保存时本层每个节点的每个端点都必须写明所在边。
	EndpointSides map[string]EndpointSides `json:"EndpointSides,omitempty"`
	// EdgeRails 是回边绕行的轨道，取值与端点所在边相同（SideLeft 等）：top / bottom 走两端节点上方 / 下方（出口在左右边时有效），
	// left / right 走左侧 / 右侧（出口在上下边，即纵向图）。与出口不在同一轴上的取值由渲染器按自动选择处理。
	EdgeRails map[string]string `json:"EdgeRails,omitempty"`
	// SubLayouts 是内嵌子图（如 map 节点的循环体）在画布上的布局，键为拥有子图的节点 ID，值仍为 Layout。
	// 这里不是可执行子图本身（那份图在节点参数 Body 里）。子图节点不进入本层 Positions；子图里可再嵌套子图。
	SubLayouts map[string]Layout `json:"SubLayouts,omitempty"`
}

// 端点句柄在画布卡片上所在的边（仅渲染；与执行无关），也是回边轨道 EdgeRails 的取值。
//
// Canvas sides an endpoint handle (or an edge rail) can take.
const (
	SideLeft   = "left"
	SideRight  = "right"
	SideTop    = "top"
	SideBottom = "bottom"
)

// Point 是画布坐标。0 是合法位置，序列化时保留。
type Point struct {
	X float64 `json:"X"`
	Y float64 `json:"Y"`
}

// EndpointSides 是单个节点各端点句柄在画布卡片上所在的边（渲染用）。
// 键为节点实例的本地端点名（入口 In，出口 Out / Case0 / Error 等），值为 left、right、top、bottom，
// 例如 {"In":"left","Out":"right","Error":"bottom"}。
//
// 读取时只检查键与取值是否合法（不知道哪些键是入口、哪些是出口）；
// 保存时由 node.Registry.CleanEndpointSides 要求覆盖实例的全部端点、任何出口不与入口同边，并清除失效条目。
//
// EndpointSides maps each local endpoint name of a node to the canvas side its handle sits on.
type EndpointSides map[string]string

// StickyNote 是画布便签与注释框，不进入 Graph.Nodes。
// Transparent 为 false 时表示不透明便签；缺省或 true 不写入。
type StickyNote struct {
	ID          string  `json:"ID"`
	X           float64 `json:"X"`
	Y           float64 `json:"Y"`
	Width       float64 `json:"Width"`
	Height      float64 `json:"Height"`
	Title       string  `json:"Title,omitempty"`
	Note        string  `json:"Note,omitempty"`
	Color       string  `json:"Color,omitempty"`
	Transparent *bool   `json:"Transparent,omitempty"`
}

// CleanUI 规范化空 UI，保证 Positions、EndpointSides、EdgeRails 初始化为 non-nil map。
// 主图层 Collapsed 无意义，一律清为 false，避免进入规范化 JSON / HashContent。
func CleanUI(u UI) UI {
	u.Collapsed = false
	if u.Positions == nil {
		u.Positions = map[string]Point{}
	}
	if u.EndpointSides == nil {
		u.EndpointSides = map[string]EndpointSides{}
	}
	if u.EdgeRails == nil {
		u.EdgeRails = map[string]string{}
	}
	if u.StickyNotes == nil {
		u.StickyNotes = []StickyNote{}
	}
	return u
}

// ParseUI 把库里的 ui_json 解成画布文档。空串和 null 都是空文档。
func ParseUI(raw []byte) (UI, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return CleanUI(UI{}), nil
	}
	var ui UI
	if err := json.Unmarshal(raw, &ui); err != nil {
		return UI{}, fmt.Errorf("graph: invalid ui: %w", err)
	}
	if err := ui.Validate(); err != nil {
		return UI{}, err
	}
	return CleanUI(ui), nil
}

// MarshalUI 校验画布文档并返回规范化 JSON。空文档为 {}。
func MarshalUI(ui UI, nodes ...Node) ([]byte, error) {
	if err := ui.Validate(nodes...); err != nil {
		return nil, err
	}
	return jsonx.Canonical(ui)
}

// Validate 检查画布文档里已经声明的字段。未知 JSON 键在 Unmarshal 时丢弃。
func (u UI) Validate(nodes ...Node) error {
	switch u.ExecutionMode {
	case "", ExecSerial, ExecParallel:
	default:
		return fmt.Errorf("graph: invalid ExecutionMode %q", u.ExecutionMode)
	}
	if err := u.Layout.validate(); err != nil {
		return err
	}
	seen := map[string]struct{}{}
	for _, node := range nodes {
		seen[node.ID] = struct{}{}
	}
	for i := range u.StickyNotes {
		u.StickyNotes[i].ID = strings.TrimSpace(u.StickyNotes[i].ID)
		if err := u.StickyNotes[i].validate(); err != nil {
			return err
		}
		if _, ok := seen[u.StickyNotes[i].ID]; ok {
			return fmt.Errorf("graph: duplicate canvas node %q", u.StickyNotes[i].ID)
		}
		seen[u.StickyNotes[i].ID] = struct{}{}
	}
	return nil
}

// validate 校验一层布局（主图层或某个子图层）的回边轨道、句柄所在边，并递归校验嵌套子图。
// 只检查已写入的取值；端点是否写全、入出是否同边由 node.Registry.CleanEndpointSides 在保存时检查。
func (l Layout) validate() error {
	for id, rail := range l.EdgeRails {
		// 轨道只存显式设置，取值与端点所在边相同。
		if !ValidSide(rail) {
			return fmt.Errorf("graph: invalid edge rail %s=%q", id, rail)
		}
	}
	for id, p := range l.EndpointSides {
		if err := p.validate(); err != nil {
			return fmt.Errorf("graph: node %s endpoint sides: %w", id, err)
		}
	}
	for id, b := range l.SubLayouts {
		if err := b.validate(); err != nil {
			return fmt.Errorf("graph: sublayout %s: %w", id, err)
		}
	}
	return nil
}

// validate 检查每个条目的端点名合法、所在边为 left / right / top / bottom 之一（空值没有意义，一并拒绝）。
func (p EndpointSides) validate() error {
	for ep, side := range p {
		if !ValidID(ep) {
			return fmt.Errorf("invalid endpoint %q", ep)
		}
		if !ValidSide(side) {
			return fmt.Errorf("endpoint %s side %q", ep, side)
		}
	}
	return nil
}

// ValidSide 报告 s 是否为合法画布边（left | right | top | bottom）。
func ValidSide(s string) bool {
	switch s {
	case SideLeft, SideRight, SideTop, SideBottom:
		return true
	default:
		return false
	}
}

func (s StickyNote) validate() error {
	if s.ID == "" {
		return fmt.Errorf("graph: invalid sticky note id %q", s.ID)
	}
	if !finite(s.X) || !finite(s.Y) || !finite(s.Width) || !finite(s.Height) {
		return fmt.Errorf("graph: sticky note %s geometry is not finite", s.ID)
	}
	if s.Width <= 0 || s.Height <= 0 {
		return fmt.Errorf("graph: sticky note %s width and height must be positive", s.ID)
	}
	if s.Color != "" && !validHexColor(s.Color) {
		return fmt.Errorf("graph: sticky note %s color %q", s.ID, s.Color)
	}
	return nil
}

func finite(n float64) bool { return !math.IsNaN(n) && !math.IsInf(n, 0) }

func validHexColor(s string) bool {
	if len(s) != 4 && len(s) != 7 || s[0] != '#' {
		return false
	}
	for _, c := range s[1:] {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
			return false
		}
	}
	return true
}
