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
// Item 元素类型由上游 List 决定，用 AnyValueSchema；Index 为 integer。
func defaultMapBody() *graph.Graph {
	item := graph.AnyValueSchema()
	item.Description = "本轮元素"
	return &graph.Graph{
		InputSchema: &jsonschema.Schema{
			Type: "object",
			Properties: map[string]*jsonschema.Schema{
				MapItemField:  item,
				MapIndexField: {Type: "integer", Description: "本轮第一个元素在 List 中的下标"},
			},
			Required:             []string{MapItemField},
			AdditionalProperties: graph.SchemaFalse(),
		},
		Nodes: []graph.Node{{ID: "start", Kind: Start, Name: "开始"}, {ID: "end", Kind: End, Name: "结束"}},
		Edges: []graph.Edge{{ID: "e1", From: graph.FormatEndpointID("start", node.DefaultOutput), To: graph.FormatEndpointID("end", node.DefaultInput)}},
	}
}

// mapVisitState 是一次 map 访问的进度，保存在 Run.State；离开节点时内核清空。
// 待遍历元素每轮直接从 in.Vars[List] 按下标切分，不重复存进 State，避免每轮序列化整份列表。
type mapVisitState struct {
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
	if cfg.Body == nil {
		return node.NodeOutput{}, fmt.Errorf("map node %s: Body required", in.NodeID)
	}
	items, err := ParseMapItems(in.NodeID, in.Vars)
	if err != nil {
		return node.NodeOutput{}, err
	}
	total := (len(items) + cfg.Chunk - 1) / cfg.Chunk
	switch in.Event.Kind {
	case "", node.EventStart:
		st := mapVisitState{Results: make([]any, total)}
		return mapAdvance(cfg, in, items, total, st, cfg.MaxParallel)
	case node.EventChildDone:
		var st mapVisitState
		// 使用 jsonx.DecodeStored 反序列化多轮状态，保证已收集 Results 中的动态整数保持 int64，
		// 避免标准 json.Unmarshal 向 any 解码时将超过 2^53 的整数转为 float64 造成精度丢失。
		// Decode multi-round state via jsonx.DecodeStored so dynamic integers in Results remain int64.
		if err := jsonx.DecodeStored(in.State, &st); err != nil {
			return node.NodeOutput{}, fmt.Errorf("map node %s: invalid state: %w", in.NodeID, err)
		}
		idx, err := strconv.Atoi(in.Event.Slot)
		if err != nil || idx < 0 || idx >= total || len(st.Results) != total {
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
		return mapAdvance(cfg, in, items, total, st, 1)
	default:
		return node.NodeOutput{}, fmt.Errorf("map: unexpected event %q", in.Event.Kind)
	}
}

// mapAdvance 决定下一步：已失败且已启动的轮次全部结束时返回失败；全部轮次结束时输出 Results；
// 否则在并发上限内最多再启动 room 轮（已失败时不再启动）；没有可启动的轮次时只写 State，继续等待在运行的子任务。
func mapAdvance(cfg MapInput, in node.ControlInput, items []any, total int, st mapVisitState, room int) (node.NodeOutput, error) {
	if st.Failed != "" && st.Done == st.Next {
		return node.NodeOutput{}, errors.New(st.Failed)
	}
	if st.Done == total {
		return node.Continue(map[string]any{MapResultsField: st.Results}, ""), nil
	}
	running := st.Next - st.Done
	var spawns []node.Spawn
	for st.Failed == "" && st.Next < total && running < cfg.MaxParallel && len(spawns) < room {
		start := st.Next * cfg.Chunk
		spawns = append(spawns, node.Spawn{
			Slot:  strconv.Itoa(st.Next),
			Graph: cfg.Body,
			Input: mapBodyInput(cfg, in.Vars, mapUnitAt(items, start, cfg.Chunk), int64(start)),
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

// mapUnitAt 取第 start 个元素起始的一轮数据：Chunk=1 时为单个元素，否则为元素切片（最后一轮可能不足 Chunk）。
func mapUnitAt(items []any, start, chunk int) any {
	if chunk == 1 {
		return items[start]
	}
	return items[start:min(start+chunk, len(items))]
}

func asAnySlice(v any) ([]any, error) {
	if v == nil {
		return nil, fmt.Errorf("value is nil")
	}
	if items, ok := v.([]any); ok {
		return items, nil
	}
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
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
