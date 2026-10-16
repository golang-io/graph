package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/golang-io/graph"
)

// RunKey 标识一次节点调用：哪个 Task、哪个 Run、Run 上第几个节点（Seq）、第几次尝试（Attempt）。
// 幂等键是 RunID + Seq：重试与租约过期重领都不改变它；Attempt 只用于日志与重试判断。
// RunKey identifies one node invocation. RunID+Seq is the idempotency key; Attempt is informational.
type RunKey struct {
	TaskID  string `json:"TaskID"`
	RunID   string `json:"RunID"`
	Seq     int64  `json:"Seq"`
	Attempt int    `json:"Attempt,omitempty"`
}

// IdempotencyKey 返回稳定幂等键（RunID:Seq）；RunID 为空时返回空串，表示调用方未提供身份。
// 有外部副作用的执行器（如 HTTP 的 Idempotency-Key 头）用它去重。
func (k RunKey) IdempotencyKey() string {
	if strings.TrimSpace(k.RunID) == "" {
		return ""
	}
	return k.RunID + ":" + strconv.FormatInt(k.Seq, 10)
}

// Event 说明本次调用节点的原因。一次节点访问从 EventStart 开始，
// 到走出出口、返回 finish/fail Effect、或 Control 报错为止；访问期间内核可能多次调用 Control。
//
// Event describes why Control is being invoked for the current node visit.
type Event struct {
	Kind    string         `json:"Kind"`              // start | child_done
	Payload map[string]any `json:"Payload,omitempty"` // child_done 时为子任务输出
	Error   string         `json:"Error,omitempty"`   // child_done 时子任务的失败原因
	Slot    string         `json:"Slot,omitempty"`    // child_done 时为该子任务启动时的 Spawn.Slot
}

// Event.Kind 取值。
const (
	EventStart     = "start"      // 首次进入节点
	EventChildDone = "child_done" // 本节点启动的一个子任务结束（同时启动多个时逐个送达）
)

// NodeInput 是业务节点 Execute 的入参：身份、投影后的 Vars、解码后的参数 Params、幂等键。
// 不含图定义与输入引用表；也不含 Event/State（多轮协调见 ControlInput）。
// Vars 已合入调试注入；执行器不得原地修改 Vars（含嵌套值），需要局部变量表时自行复制。
//
// NodeInput is the business Execute argument (no Event/State).
type NodeInput struct {
	NodeID string         `json:"NodeID"` // 图上的节点实例 ID（graph.Node.ID；日志、错误消息）
	Vars   map[string]any `json:"Vars"`
	// Params 是由 WithParams[T] 解码的节点参数结构体 T（含动态绑定与调试注入覆盖），执行期直接通过 Params[T] 读取。
	// Params holds the decoded parameter struct T for the node invocation.
	Params any    `json:"-"`
	Key    RunKey `json:"Key"`

	// Lookup 按引用读取本次运行中已产生的值：From 为 $input 时读运行输入，否则读 Run.Outputs[From][Field]。
	// 只应查询节点通过 ReferenceDeclarer 声明过的引用（定义期已校验来源与可达性），例如 Merge 的 First。
	// 内核调用 Execute 时总会提供；直接调用 Execute 的测试可以不填。
	Lookup func(ref graph.InputRef) (any, bool) `json:"-"`
}

// ControlInput 是控制节点 Control 的入参：在业务字段之外携带参数 Params、框架组装的 Event 与 Run.State。
// 用于 map / subgraph 等需要等待子任务的多轮协调；单次推进的内置节点（switch/merge 等）仍走 Execute。
// Vars 与 NodeInput.Vars 使用同一只读约定。
//
// ControlInput is the Control argument for multi-round coordination nodes.
type ControlInput struct {
	NodeID string          `json:"NodeID"` // 同 NodeInput.NodeID
	Vars   map[string]any  `json:"Vars"`
	Params any             `json:"-"` // 由 WithParams[T] 解码的节点参数结构体 T
	Event  Event           `json:"Event"`
	State  json.RawMessage `json:"State,omitempty"` // 同一次访问中上一次 Control 返回的 State；首次为空
}

// Effect.Kind 取值：内核原语种类；节点通过 Effect 请求，不能直接操作 Run。
// 节点不能让 Run 停下等待外部事件；Run 只会因汇合、子任务或调试暂停而等待，均由内核决定。
// Nodes cannot park a Run on external events; only joins, child tasks and debug pauses park Runs.
const (
	EffectSpawn  = "spawn"  // 启动一个或多个子图 Task
	EffectFinish = "finish" // 结束当前 Run（成功）
	EffectFail   = "fail"   // 节点失败（可走错误出口）
)

// Effect 是内核原语：Kind 恰好一种，字段按 Kind 解释。
// Effect is one kernel primitive; Kind selects which fields apply.
type Effect struct {
	Kind string `json:"Kind"`

	// spawn：至少一个；每个子任务结束时内核以 EventChildDone（Slot 相同）再次调用本节点
	Spawns []Spawn `json:"Spawns,omitempty"`

	// finish
	Output map[string]any `json:"Output,omitempty"`

	// fail
	Message string `json:"Message,omitempty"`
}

// Spawn 是一次子图启动请求：GraphID（已保存的图）与 Graph（内嵌图，如 map 的循环体）恰选其一。
// Slot 是该子任务在本次节点访问内的标识（并行 map 用迭代下标），结束时原样放进 Event.Slot；
// 同一 Run 上同时在等的子任务 Slot 不得重复，只启动一个时可为空。
//
// Spawn requests one child task; exactly one of GraphID / Graph is set.
type Spawn struct {
	Slot    string         `json:"Slot,omitempty"`
	GraphID string         `json:"GraphID,omitempty"`
	Graph   *graph.Graph   `json:"Graph,omitempty"`
	Input   map[string]any `json:"Input,omitempty"`
}

// NodeOutput 是 Execute / Control 唯一允许返回的结果。合法形态互斥，恰选其一：
//
//  1. 继续推进：可选 Set + 可选 Endpoint（空=默认 out）；无 Effect
//  2. 内核原语：Effect（可带 State）；无 Endpoint、无顶层 Set
//  3. 仅写 State（可为空）：其余皆空，只在仍有子任务未结束时合法，Run 继续等待子任务
//
// Set 的键必须是已声明的正常输出名。
//
// NodeOutput is the only result Execute/Control may return; shapes above are mutually exclusive.
type NodeOutput struct {
	Set      map[string]any  `json:"Set,omitempty"`
	Endpoint string          `json:"Endpoint,omitempty"` // 本地出端点名；空表示默认 out
	State    json.RawMessage `json:"State,omitempty"`    // Control 多轮协调时写回 Run.State；离开节点时清空
	Effect   *Effect         `json:"Effect,omitempty"`
}

// Continue 构造继续推进结果：可选 Set + 可选 Endpoint（空为默认 out），无 Effect。
// Set 为 nil 时置空 map 之外的原样保留，调用方不得复用传入 map。
func Continue(set map[string]any, endpoint string) NodeOutput {
	return NodeOutput{Set: set, Endpoint: endpoint}
}

// Finish 构造结束当前 Run 的内核原语；output 为空时置空 map，由内核补齐。
func Finish(output map[string]any) NodeOutput {
	if output == nil {
		output = map[string]any{}
	}
	return NodeOutput{Effect: &Effect{Kind: EffectFinish, Output: output}}
}

// Fail 构造节点失败的内核原语；空消息由内核补默认文案。
func Fail(message string) NodeOutput {
	return NodeOutput{Effect: &Effect{Kind: EffectFail, Message: message}}
}

// SpawnChildren 构造启动子任务的内核原语，可附带 Control 多轮状态 State。
func SpawnChildren(spawns []Spawn, state json.RawMessage) NodeOutput {
	return NodeOutput{Effect: &Effect{Kind: EffectSpawn, Spawns: spawns}, State: state}
}

// Wait 构造仅写 State 的等待结果：仍有子任务未结束时 Run 继续等待，不离开节点。
func Wait(state json.RawMessage) NodeOutput {
	return NodeOutput{State: state}
}

// ValidateShape 只校验互斥形态，不校验业务规则（输出名、端点是否声明由 OutputRule.Check 负责）。
func (o NodeOutput) ValidateShape() error {
	if o.Effect == nil {
		return nil
	}
	if o.Endpoint != "" || len(o.Set) > 0 {
		return fmt.Errorf("Effect cannot combine with Set or Endpoint")
	}
	return validateEffect(o.Effect)
}

func validateEffect(eff *Effect) error {
	switch strings.TrimSpace(eff.Kind) {
	case EffectSpawn:
		if len(eff.Spawns) == 0 {
			return fmt.Errorf("spawn requires at least one Spawns entry")
		}
		slots := make(map[string]bool, len(eff.Spawns))
		for i, sp := range eff.Spawns {
			if (strings.TrimSpace(sp.GraphID) == "") == (sp.Graph == nil) {
				return fmt.Errorf("spawn[%d] requires exactly one of GraphID and Graph", i)
			}
			if slots[sp.Slot] {
				return fmt.Errorf("spawn[%d] duplicate Slot %q", i, sp.Slot)
			}
			slots[sp.Slot] = true
		}
	case EffectFinish, EffectFail:
	default:
		return fmt.Errorf("Effect.Kind %q invalid", eff.Kind)
	}
	return nil
}

// Leaves 报告返回值是否会让 Run 离开当前节点（或结束）：
// Spawn 与空 Set/Endpoint 的等待结果仍停在本节点；其余均计为离开。
func (o NodeOutput) Leaves() bool {
	if o.Effect != nil {
		return strings.TrimSpace(o.Effect.Kind) != EffectSpawn
	}
	return o.Endpoint != "" || len(o.Set) > 0
}

// Node 是种类合同：至少提供 Spec。执行入口由 Executor 或 Controller 提供。
// 与 graph.Node（图上的节点实例）不同：本接口描述种类实现。
//
// Node is the kind contract (Spec only); execution is Executor or Controller.
type Node interface {
	Spec() NodeSpec
}

// Executor 是业务节点入口：单次 Execute(NodeInput)，不接收 Event/State。
type Executor interface {
	Node
	Execute(context.Context, NodeInput) (NodeOutput, error)
}

// Controller 是多轮协调控制节点入口：Control(ControlInput) 接收框架组装的 Event/State。
// 当前实现：map / subgraph。
type Controller interface {
	Node
	Control(context.Context, ControlInput) (NodeOutput, error)
}

// InnerNodePrefix 是引擎内置控制节点的 ID 前缀，只是命名约定。
// 图里的 Nodes[].Kind 等于种类 ID：内置控制节点为 __INNER__.map 等，其余为 template 等。
// 节点是否内联执行由 NodeSpec.Inline 与是否实现 Controller 决定（Registry.Inline），与前缀无关。
const InnerNodePrefix = "__INNER__."
