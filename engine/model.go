package engine

import (
	"encoding/json"
	"time"

	"github.com/golang-io/graph"
)

// 本文件定义两个引擎共用的执行表，同一个结构体既是表结构也是 JSON 视图：
// 列名、JSON 键、Go 字段名完全相同。每个字段都显式写 column 标签，列名不依赖
// NamingStrategy（当前为 NoLowerCase: true，换策略也不会改列名）。
//
//   - Task（tb_graph_task）：一个图版本的一次完整执行。
//   - Run（tb_graph_run）：Task 中当前调度的执行单元，即一条执行路径当前所在的节点；
//     fork 后父 Run 还记录汇合进度。
//
// 语义与策略分层（设计文档第 2 节）：Task / Run 只保存跨引擎共用的纯执行语义字段，内核（engine/kernel）只读写它们，
// 各引擎对上层呈现完全相同的语义；引擎私有的持久化与调度字段由对应引擎包自行定义表行结构，不放入本包。
//
// Task and Run hold only the engine-agnostic execution semantics; engine-specific persistence columns
// live inside the respective engine package.
//
// 调度与续跑只依赖 Task / Run 当前态。
//
// JSON 列与长文本列（图快照、输入输出、Outputs、State、Error 等）不写 type 标签，也不写 size：
// 由 GORM 按方言选择不限长的类型（MySQL 为 LONGTEXT，PostgreSQL / SQLite 为 TEXT）。
// 不能写 type:text，MySQL 的 TEXT 最多 64KB，超出时报错或截断成损坏的 JSON。
//
// This file defines the shared execution tables. One struct is both the table schema
// and the JSON view; column = JSON key = Go field name.

// Task 是一个图版本的一次完整执行（表 tb_graph_task）。
// Status 是唯一的冗余字段：用于列表按状态过滤，只在启动、控制动作、根 Run 结束时写入；
// RUNNING 与 WAITING 之间的切换只作展示提示，调度不依赖它。
//
// Task is one complete execution of a graph version (table tb_graph_task).
type Task struct {
	// IdxTaskList（EngineKind, ParentTaskID, StartedAt, TaskID）服务于列表查询（engine.ListTasks）：
	// 按引擎列根 Task、StartedAt 倒序分页时按索引顺序读取，不对该引擎的全部 Task 排序。
	TaskID     string `gorm:"column:TaskID;primaryKey;size:64;index:IdxTaskList,priority:4" json:"TaskID"`
	TicketID   string `gorm:"column:TicketID;size:64;index:IdxTaskTicket" json:"TicketID"` // 业务分组；空则等于 TaskID
	EngineKind string `gorm:"column:EngineKind;size:32;index:IdxTaskEngine,priority:1;index:IdxTaskList,priority:1" json:"EngineKind"`
	GraphID    string `gorm:"column:GraphID;size:64;index:IdxTaskGraph,priority:1" json:"GraphID"`
	// GraphVersion 是执行的图定义版本号；临时图为 0。map 循环体等内嵌子图的子 Task 沿用父 Task 的
	// GraphID / GraphVersion，因此二者不能唯一确定 Task 执行的图，执行以 Graph 快照为准。
	GraphVersion int64 `gorm:"column:GraphVersion" json:"GraphVersion"`
	// Version 是 Task 的写回计数：每次写回（Task 或任一 Run 有改动）由内核加 1；用于 Watch 判断变化与回执。
	Version int64            `gorm:"column:Version" json:"Version"`
	Status  graph.StatusCode `gorm:"column:Status;size:16;index:IdxTaskStatus;index:IdxTaskEngine,priority:2" json:"Status"`
	Policy  TaskPolicy       `gorm:"column:Policy;serializer:graphjson" json:"Policy"`
	Input   map[string]any   `gorm:"column:Input;serializer:graphjson" json:"Input,omitempty"`
	Output  map[string]any   `gorm:"column:Output;serializer:graphjson" json:"Output,omitempty"`
	Error   string           `gorm:"column:Error" json:"Error,omitempty"`
	// Paused 由 Pause 写入；之后到达活动节点的 Run 进入 WAITING(debug)。
	Paused bool `gorm:"column:Paused" json:"Paused"`
	// ParentTaskID / ParentRunID：子图 Task 指向父 Task 及父 Task 中等待它的 Run。
	ParentTaskID string `gorm:"column:ParentTaskID;size:64;index:IdxTaskParent;index:IdxTaskList,priority:2" json:"ParentTaskID,omitempty"`
	ParentRunID  string `gorm:"column:ParentRunID;size:64" json:"ParentRunID,omitempty"`
	// Slot 是父 Run 发起本子 Task 时的 Spawn.Slot；子 Task 结束时随 ChildDone 交回父 Run，
	// 父 Run 按 Slot → TaskID 核对是否仍在等待它。
	Slot  string `gorm:"column:Slot;size:64" json:"Slot,omitempty"`
	Depth int    `gorm:"column:Depth" json:"Depth,omitempty"` // 子图嵌套深度，根为 0
	// Graph 是本次执行使用的不可变图快照，恢复与未保存的临时图都不依赖定义库。
	// 推进路径按需不读该列（LoadTask withGraph=false），此时为零值，JSON 中省略；
	// Watch 的增量也清空它，图只随完整快照发出一次。
	Graph      graph.Graph `gorm:"column:Graph;serializer:graphjson" json:"Graph,omitzero"`
	StartedAt  time.Time   `gorm:"column:StartedAt;index:IdxTaskGraph,priority:2;index:IdxTaskList,priority:3" json:"StartedAt"`
	FinishedAt *time.Time  `gorm:"column:FinishedAt" json:"FinishedAt,omitempty"`
	CreatedAt  time.Time   `gorm:"column:CreatedAt" json:"CreatedAt"`
}

// TableName 固定表名。
func (Task) TableName() string { return "tb_graph_task" }

// Clone 深复制 Task（含图定义、嵌套输入输出与调试选项），供快照与推进写回使用，确保外部修改快照不影响内部执行。
func (t *Task) Clone() *Task {
	if t == nil {
		return nil
	}
	out := *t
	out.Policy.Debug = t.Policy.Debug.Clone()
	out.Input = cloneMap(t.Input)
	out.Output = cloneMap(t.Output)
	out.Graph = t.Graph.Clone()
	if t.FinishedAt != nil {
		at := *t.FinishedAt
		out.FinishedAt = &at
	}
	return &out
}

// Outputs 是一条执行路径上各节点的输出：节点 ID → 输出名 → 值。
// 节点每执行一次就整体覆盖自己名下的输出；分叉时子 Run 复制一份，汇合时按节点 ID 合并。
//
// Outputs maps node ID → output name → value along one execution path.
type Outputs map[string]map[string]any

// Clone 深复制节点输出映射及其嵌套动态值。
func (o Outputs) Clone() Outputs {
	if o == nil {
		return nil
	}
	out := make(Outputs, len(o))
	for id, fields := range o {
		out[id] = CloneMap(fields)
	}
	return out
}

// CloneMap 深复制动态键值映射及其嵌套切片与映射。
func CloneMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = CloneValue(v)
	}
	return out
}

// CloneValue 深复制动态值中的嵌套 map[string]any 与 []any。
func CloneValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		return CloneMap(val)
	case []any:
		out := make([]any, len(val))
		for i, item := range val {
			out[i] = CloneValue(item)
		}
		return out
	default:
		return v
	}
}

func cloneMap(m map[string]any) map[string]any { return CloneMap(m) }

// WaitReason 说明 Run 为什么不在执行，是 Run.WaitReason 的取值，只在 WAITING 时非空。
// 等待结束（子任务结果到达、调试放行）在处理对应事件的 Apply 内直接继续推进，不经过中间状态。
// 只有下面三种等待，全部由内核产生：Executor 同步返回，Controller 只能经 Effect 启动子 Task，节点无法自行挂起 Run。
//
// WaitReason tells why a Run is not executing; empty means not waiting.
type WaitReason string

const (
	WaitJoin     WaitReason = "join"     // fork 后等待子 Run 汇合（父 Run 上）
	WaitSubgraph WaitReason = "subgraph" // 子图 / map 节点：等待子 Task 结束
	WaitDebug    WaitReason = "debug"    // 暂停、单步或断点：在进入节点前停住，由 Resume 放行
)

// Run 是 Task 中当前调度的执行单元（表 tb_graph_run）。
// 一个 Run 沿图逐个节点移动，每移动一次 Seq 加 1；fork 时自身转为 WAITING(join) 并插入子 Run。
//
// 状态含义：
//   - PENDING：待执行当前活动节点（AvailableAt 未到表示重试延迟）
//   - RUNNING：正在执行
//   - WAITING：等待汇合、子任务或调试放行，WaitReason 说明原因
//   - 终态：SUCCEEDED（到达 end 或汇合点）、FAILED、CANCELED、SKIPPED（any 汇合的落选分支）
//
// Run is the scheduling unit of a Task (table tb_graph_run).
type Run struct {
	RunID      string `gorm:"column:RunID;primaryKey;size:64" json:"RunID"`
	TaskID     string `gorm:"column:TaskID;size:64;index:IdxRunTask,priority:1" json:"TaskID"`
	EngineKind string `gorm:"column:EngineKind;size:32" json:"EngineKind"`

	// 位置与数据。
	NodeID string `gorm:"column:NodeID;size:64" json:"NodeID"`
	// FromEdgeID 是 Run 进入当前节点经由的边；起始节点为空。
	FromEdgeID string          `gorm:"column:FromEdgeID;size:64" json:"FromEdgeID,omitempty"`
	Seq        int64           `gorm:"column:Seq" json:"Seq"`
	Outputs    Outputs         `gorm:"column:Outputs;serializer:graphjson" json:"Outputs"`         // 本路径上各节点的输出
	State      json.RawMessage `gorm:"column:State;serializer:graphjson" json:"State,omitempty"`   // 当前节点访问中上一次 Control 返回的完整状态
	Output     map[string]any  `gorm:"column:Output;serializer:graphjson" json:"Output,omitempty"` // 节点通过 Finish 结束 Run 时的输出

	// Arrivals 记录 Merge(all) 节点已到达入边的节点输出（入边 ID → Outputs）。
	Arrivals map[string]Outputs `gorm:"column:Arrivals;serializer:graphjson" json:"Arrivals,omitempty"`

	// 调度。
	Status      graph.StatusCode `gorm:"column:Status;size:16;index:IdxRunTask,priority:2" json:"Status"`
	WaitReason  WaitReason       `gorm:"column:WaitReason;size:16" json:"WaitReason,omitempty"`
	AvailableAt int64            `gorm:"column:AvailableAt" json:"AvailableAt,omitempty"` // Unix 毫秒；PENDING 时不早于该时刻执行
	// Attempt 是当前节点访问的第几次尝试，从 1 开始；只由内核修改，只在业务重试时加 1。
	Attempt   int       `gorm:"column:Attempt" json:"Attempt"`
	Error     string    `gorm:"column:Error" json:"Error,omitempty"`
	StartedAt time.Time `gorm:"column:StartedAt" json:"StartedAt"` // 进入当前节点的时刻
	CreatedAt time.Time `gorm:"column:CreatedAt" json:"CreatedAt"`
}

// TableName 固定表名。
func (Run) TableName() string { return "tb_graph_run" }

// Clone 深复制 Run（含 Outputs、Output、State 与 Arrivals）。
func (r *Run) Clone() *Run {
	if r == nil {
		return nil
	}
	out := *r
	out.Outputs = r.Outputs.Clone()
	out.Output = cloneMap(r.Output)
	if r.State != nil {
		out.State = append(json.RawMessage(nil), r.State...)
	}
	if r.Arrivals != nil {
		out.Arrivals = make(map[string]Outputs, len(r.Arrivals))
		for slot, outs := range r.Arrivals {
			out.Arrivals[slot] = outs.Clone()
		}
	}
	return &out
}
