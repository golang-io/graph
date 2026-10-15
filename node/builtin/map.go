package builtin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"strconv"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/internal/jsonx"
	"github.com/golang-io/graph/node"
	"github.com/google/jsonschema-go/jsonschema"
)

// Map 列表遍历容器节点：对输入 List 的每一项（或每 Chunk 项）运行一次内嵌循环体 Body，
// 结果按顺序收集为 Results。循环体是独立的一张图，主图因此保持无环；循环体内外的节点互相不可见。
const Map = node.InnerNodePrefix + "map"

const MapListField = "List"

// MapOutput 是 map 节点写入 Run.Outputs 的结构：Results[i] 是第 i 轮循环体子任务的输出（其 end 节点输出的合并）。
type MapOutput struct {
	Results any `json:"Results"`
}

// 循环体运行输入中的固定字段，以及 map 自身的输出名。
const (
	MapItemField    = "Item"    // 本轮元素：Chunk=1 时为单个元素，否则为元素切片
	MapIndexField   = "Index"   // 本轮第一个元素在 List 中的下标
	MapResultsField = "Results" // map 节点输出
)

// MapInput 描述遍历方式与循环体；遍历的列表来自输入 List（bind 槽）。
// 循环体的运行输入：Item、Index，以及 map 实例上的自定义输入（按同名传入）；只传循环体 InputSchema 声明过的键。
type MapInput struct {
	List        any          `json:"List" bind:""`
	Chunk       int          `json:"Chunk,omitempty" title:"每轮条数"`
	MaxParallel int          `json:"MaxParallel,omitempty" title:"并发数（1=串行）"`
	Body        *graph.Graph `json:"Body,omitempty" title:"循环体"`
}

// mapParamNames 是 MapInput 自身的参数名，不作为自定义输入传给循环体。
var mapParamNames = []string{MapListField, "Chunk", "MaxParallel", "Body"}

// Validate 要求 Chunk、MaxParallel 不为负（0 表示取默认值 1），且必须有循环体。
func (c MapInput) Validate() error {
	if c.Chunk < 0 {
		return fmt.Errorf("Chunk must not be negative")
	}
	if c.MaxParallel < 0 {
		return fmt.Errorf("MaxParallel must not be negative")
	}
	if c.Body == nil {
		return fmt.Errorf("Body required")
	}
	return nil
}

// Subgraphs 交出循环体供 Registry.Validate 递归校验；Item / Index 由本节点固定提供。
func (c MapInput) Subgraphs() []node.Subgraph {
	if c.Body == nil {
		return nil
	}
	return []node.Subgraph{{Path: "Body", Graph: *c.Body, Provides: []string{MapItemField, MapIndexField}}}
}

// NormalizeMapInput 补齐默认值：Chunk 与 MaxParallel 至少为 1。
func NormalizeMapInput(cfg MapInput) MapInput {
	if cfg.Chunk <= 0 {
		cfg.Chunk = 1
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = 1
	}
	return cfg
}

// MapNode 实现列表遍历节点。
type MapNode struct{}

func (MapNode) Spec() node.NodeSpec {
	return node.WithOutput[MapOutput](node.WithParams(node.NodeSpec{
		ID:          Map,
		Name:        "遍历",
		Description: "对输入 List 逐项运行循环体（内嵌子图），循环体输入 Item/Index，完成后输出 Results",
		Group:       "control",
		GroupLabel:  "控制",
		Icon:        "repeat",
		Tone:        "violet",
		Renderer:    "map",
		Endpoints:   node.DefaultEndpoints(),
	}, MapInput{Chunk: 1, MaxParallel: 1, Body: defaultMapBody()}))
}

// defaultMapBody 是新建 map 节点时的循环体：start → end，运行输入声明 Item / Index。
// 有了它，刚从节点库拖出的 map 即可通过校验；画布再往循环体里添加节点。
func defaultMapBody() *graph.Graph {
	return &graph.Graph{
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				MapItemField:  {Description: "本轮元素"},
				MapIndexField: {Type: "integer", Description: "本轮第一个元素在 List 中的下标"},
			},
		},
		Nodes: []graph.Node{{ID: "start", Kind: Start, Name: "开始"}, {ID: "end", Kind: End, Name: "结束"}},
		Edges: []graph.Edge{{ID: "e1", From: graph.FormatEndpointID("start", node.DefaultOutput), To: graph.FormatEndpointID("end", node.DefaultInput)}},
	}
}

// mapVisitState 是一次 map 访问的进度，保存在 Run.State；离开节点时内核清空。
type mapVisitState struct {
	Units   []any `json:"Units"`   // 每轮的 item（已按 Chunk 切分）
	Next    int   `json:"Next"`    // 下一个待启动的轮次
	Done    int   `json:"Done"`    // 已结束的轮次数
	Results []any `json:"Results"` // 按轮次下标收集的循环体输出
	// Failed 是第一个失败轮次的错误；非空后不再启动新轮次，已启动的轮次全部结束后本节点以它失败。
	// 内核要求子 Task 未全部结束时父 Run 不离开节点，因此不能在仍有轮次运行时返回失败。
	Failed string `json:"Failed,omitempty"`
}

// Control 处理两类事件：start 时切分列表并启动首批循环体子任务；
// child_done 时记录该轮结果，再补启动下一轮或在全部结束后输出 Results。
// 任一轮失败后不再启动新轮次，等已启动的轮次全部结束，再以第一个失败原因使本节点失败。
func (MapNode) Control(_ context.Context, in node.ControlInput) (node.NodeOutput, error) {
	cfg, err := node.Params[MapInput](in.Params)
	if err != nil {
		return node.NodeOutput{}, err
	}
	cfg = NormalizeMapInput(cfg)
	switch in.Event.Kind {
	case "", node.EventStart:
		items, err := ParseMapItems(in.NodeID, in.Vars)
		if err != nil {
			return node.NodeOutput{}, err
		}
		st := mapVisitState{Units: mapUnits(items, cfg.Chunk)}
		st.Results = make([]any, len(st.Units))
		return mapAdvance(cfg, in, st, cfg.MaxParallel)
	case node.EventChildDone:
		var st mapVisitState
		if err := json.Unmarshal(in.State, &st); err != nil {
			return node.NodeOutput{}, fmt.Errorf("map node %s: invalid state: %w", in.NodeID, err)
		}
		idx, err := strconv.Atoi(in.Event.Slot)
		if err != nil || idx < 0 || idx >= len(st.Units) {
			return node.NodeOutput{}, fmt.Errorf("map node %s: invalid iteration slot %q", in.NodeID, in.Event.Slot)
		}
		st.Done++
		if in.Event.Error != "" {
			if st.Failed == "" {
				st.Failed = fmt.Sprintf("map node %s: iteration %d: %s", in.NodeID, idx, in.Event.Error)
			}
		} else {
			result := in.Event.Payload
			if result == nil {
				result = map[string]any{}
			}
			st.Results[idx] = result
		}
		return mapAdvance(cfg, in, st, 1)
	default:
		return node.NodeOutput{}, fmt.Errorf("map: unexpected event %q", in.Event.Kind)
	}
}

// mapAdvance 决定下一步：已失败且已启动的轮次全部结束时返回失败；全部轮次结束时输出 Results；
// 否则在并发上限内最多再启动 room 轮（已失败时不再启动）；没有可启动的轮次时只写 State，继续等待在运行的子任务。
func mapAdvance(cfg MapInput, in node.ControlInput, st mapVisitState, room int) (node.NodeOutput, error) {
	if st.Failed != "" && st.Done == st.Next {
		return node.NodeOutput{}, errors.New(st.Failed)
	}
	if st.Done == len(st.Units) {
		return node.Continue(map[string]any{MapResultsField: st.Results}, ""), nil
	}
	running := st.Next - st.Done
	var spawns []node.Spawn
	for st.Failed == "" && st.Next < len(st.Units) && running < cfg.MaxParallel && len(spawns) < room {
		spawns = append(spawns, node.Spawn{
			Slot:  strconv.Itoa(st.Next),
			Graph: cfg.Body,
			Input: mapBodyInput(cfg, in.Vars, st.Units[st.Next], int64(st.Next*cfg.Chunk)),
		})
		st.Next++
		running++
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return node.NodeOutput{}, err
	}
	if len(spawns) == 0 {
		return node.Wait(raw), nil
	}
	return node.SpawnChildren(spawns, raw), nil
}

// mapBodyInput 组装一轮循环体的运行输入：map 实例上的自定义输入（去掉 map 自身参数）加 item / index，
// 只保留循环体 InputSchema 声明过的键（子任务启动时会拒绝未声明的运行输入）。
func mapBodyInput(cfg MapInput, vars map[string]any, item any, index int64) map[string]any {
	all := maps.Clone(vars)
	for _, name := range mapParamNames {
		delete(all, name)
	}
	if all == nil {
		all = map[string]any{}
	}
	all[MapItemField], all[MapIndexField] = item, index
	input := map[string]any{}
	for _, name := range jsonx.PropertyNames(cfg.Body.InputSchema) {
		if v, ok := all[name]; ok {
			input[name] = v
		}
	}
	return input
}

// mapUnits 按 Chunk 切分列表：Chunk=1 时每轮一个元素，否则每轮一个元素切片（最后一轮可能不足 Chunk）。
func mapUnits(items []any, chunk int) []any {
	units := make([]any, 0, (len(items)+chunk-1)/chunk)
	for start := 0; start < len(items); start += chunk {
		if chunk == 1 {
			units = append(units, items[start])
			continue
		}
		units = append(units, items[start:min(start+chunk, len(items))])
	}
	return units
}

func asAnySlice(v any) ([]any, error) {
	if v == nil {
		return nil, fmt.Errorf("value is nil")
	}
	if items, ok := v.([]any); ok {
		return items, nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice {
		return nil, fmt.Errorf("want array/slice, got %T", v)
	}
	out := make([]any, rv.Len())
	for i := 0; i < rv.Len(); i++ {
		out[i] = rv.Index(i).Interface()
	}
	return out, nil
}

// ParseMapItems 从 Map 节点的输入中取出 List。
func ParseMapItems(nodeID string, inputs map[string]any) ([]any, error) {
	prefix := ""
	if nodeID != "" {
		prefix = fmt.Sprintf("map node %s: ", nodeID)
	}
	raw, exists := inputs[MapListField]
	if !exists {
		return nil, fmt.Errorf("%sinput %q missing", prefix, MapListField)
	}
	items, err := asAnySlice(raw)
	if err != nil {
		return nil, fmt.Errorf("%sinput %q: %w", prefix, MapListField, err)
	}
	return items, nil
}
