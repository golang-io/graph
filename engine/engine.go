package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-io/graph"
	"github.com/golang-io/graph/node"
	"gorm.io/gorm"
)

// KindImmediate / KindDurable 是两个核心引擎的标识。
const (
	KindImmediate = "immediate"
	KindDurable   = "durable"
)

// 失败策略取值；执行模式取值见 graph.ExecSerial 等。
const (
	FailFast = "fail_fast" // 任一节点失败即整个 Task 失败
)

// TaskPolicy 是一次 Task 的执行策略，两个引擎接受同一取值范围并按同一语义执行。
// TaskPolicy is the execution policy; both engines accept and apply the same values.
type TaskPolicy struct {
	FailPolicy    string        `json:"FailPolicy,omitempty"`    // fail_fast（默认）
	ExecutionMode string        `json:"ExecutionMode,omitempty"` // serial | parallel；只影响 immediate 的并发度
	NodeTimeoutMS int64         `json:"NodeTimeoutMS,omitempty"` // 单个活动节点超时；0 表示不限
	MaxAttempts   int           `json:"MaxAttempts,omitempty"`   // 活动节点最多执行次数，默认 1
	MaxParallel   int           `json:"MaxParallel,omitempty"`   // immediate parallel 模式的并发上限，默认 8
	RetryDelayMS  int64         `json:"RetryDelayMS,omitempty"`  // 重试间隔；MaxAttempts>1 时默认 1000
	Debug         *DebugOptions `json:"Debug,omitempty"`         // 调试：单步、断点、注入；调试时强制串行
}

// NormalizePolicy 校验并补齐策略默认值；两个引擎在 Start 时都调用它，保证接受范围一致。
// NormalizePolicy validates the policy and fills defaults for both engines.
func NormalizePolicy(p TaskPolicy) (TaskPolicy, error) {
	p.FailPolicy = strings.TrimSpace(p.FailPolicy)
	if p.FailPolicy == "" {
		p.FailPolicy = FailFast
	}
	if p.FailPolicy != FailFast {
		return p, fmt.Errorf("%w: FailPolicy %q", ErrInvalid, p.FailPolicy)
	}
	p.ExecutionMode = strings.TrimSpace(p.ExecutionMode)
	if p.ExecutionMode == "" {
		p.ExecutionMode = graph.ExecSerial
	}
	switch p.ExecutionMode {
	case graph.ExecSerial, graph.ExecParallel:
	default:
		return p, fmt.Errorf("%w: ExecutionMode %q", ErrInvalid, p.ExecutionMode)
	}
	if p.MaxAttempts == 0 {
		p.MaxAttempts = 1
	}
	if p.MaxAttempts < 1 {
		return p, fmt.Errorf("%w: MaxAttempts must be positive", ErrInvalid)
	}
	if p.MaxParallel == 0 {
		p.MaxParallel = 8
	}
	if p.MaxParallel < 1 {
		return p, fmt.Errorf("%w: MaxParallel must be positive", ErrInvalid)
	}
	if p.RetryDelayMS < 0 || p.NodeTimeoutMS < 0 {
		return p, fmt.Errorf("%w: RetryDelayMS and NodeTimeoutMS must not be negative", ErrInvalid)
	}
	if p.MaxAttempts > 1 && p.RetryDelayMS == 0 {
		p.RetryDelayMS = 1000
	}
	var err error
	if p.Debug, err = p.Debug.Normalize(); err != nil {
		return p, err
	}
	return p, nil
}

// StartRequest 启动一次 Task。Graph 为空时按 GraphID + GraphVersion 从定义库加载（GraphVersion=0 取当前版本）。
type StartRequest struct {
	GraphID      string         `json:"GraphID,omitempty"`
	GraphVersion int64          `json:"GraphVersion,omitempty"` // 图定义版本号（tb_graph_definition.Version）
	Engine       string         `json:"Engine,omitempty"`       // 空则使用图定义上配置的默认引擎
	TicketID     string         `json:"TicketID,omitempty"`     // 业务分组；空则等于新 TaskID
	Graph        graph.Graph    `json:"Graph,omitzero"`
	Input        map[string]any `json:"Input,omitempty"`
	Policy       TaskPolicy     `json:"Policy,omitzero"`
}

// PinnedGraph 返回 Graph 的副本，身份设为请求顶层的 GraphID（去除首尾空白）与 GraphVersion；
// 请求体中 Graph 自带的 GraphID / GraphVersion 被覆盖。引擎在 Start 时用它编译，
// Task 的 GraphID / GraphVersion 列与图快照因此取自同一个值。
//
// PinnedGraph returns Graph with its identity overwritten by the request's GraphID and GraphVersion.
func (r StartRequest) PinnedGraph() graph.Graph {
	g := r.Graph.Clone()
	g.GraphID, g.GraphVersion = strings.TrimSpace(r.GraphID), r.GraphVersion
	return g
}

// ResumeRequest 恢复暂停或停在调试等待（WaitReason=debug）上的 Task。DebugAction：
//   - 空：清除暂停，放行全部调试等待中的 Run
//   - step：开启单步，只放行一个调试等待中的 Run
//   - continue：关闭单步，放行全部调试等待中的 Run
//
// Debug 替换不能改变正在等待子 Task 的节点注入（含删除旧注入），否则返回 ErrConflict。
// 进行中的 Controller 必须使用同一次访问开始时的输入与参数；断点和单步可照常更新。
//
// ResumeRequest resumes a paused task or Runs parked at debug gates.
type ResumeRequest struct {
	Debug       *DebugOptions `json:"Debug,omitempty"`       // 非空时替换 Task 的调试选项（如修改断点）
	DebugAction string        `json:"DebugAction,omitempty"` // "" | step | continue
	TargetRunID string        `json:"TargetRunID,omitempty"` // 只放行指定的调试等待 Run；无匹配时返回 ErrConflict
}

// ResumeRequest.DebugAction 取值；空串表示普通恢复。
const (
	DebugActionStep     = "step"
	DebugActionContinue = "continue"
)

// ListQuery 查询 Task 列表，按 StartedAt 倒序（相同时按 TaskID 倒序）。
// After 为上一页最后一个 TaskID；AfterAt 非零时表示游标已解析为确定的 (AfterAt, After) 位置，各引擎直接使用。
//
// ListQuery queries root tasks ordered by (StartedAt DESC, TaskID DESC).
// When AfterAt is non-zero, (AfterAt, After) is the pre-resolved cursor position.
type ListQuery struct {
	GraphID string           `json:"GraphID,omitempty"`
	Status  graph.StatusCode `json:"Status,omitempty"`
	Limit   int              `json:"Limit,omitempty"`
	After   string           `json:"After,omitempty"`
	AfterAt time.Time        `json:"-"`
}

// Receipt 是 Start 与 Signal 的回执：动作完成后 Task 的状态与写回计数。
// 引擎须填全所有字段（内置引擎统一用 TaskReceipt 生成），service 不再补写。
//
// Receipt reports the task state after Start / Signal; engines fill every field.
type Receipt struct {
	TaskID       string           `json:"TaskID"`
	EngineKind   string           `json:"EngineKind"`
	Status       graph.StatusCode `json:"Status"`
	Version      int64            `json:"Version"`                // Task 写回计数（Task.Version）
	GraphVersion int64            `json:"GraphVersion,omitempty"` // 图定义版本号（Task.GraphVersion）；临时图为 0，JSON 中省略
}

// TaskReceipt 由 Task 当前状态生成回执。
func TaskReceipt(t *Task) Receipt {
	return Receipt{TaskID: t.TaskID, EngineKind: t.EngineKind, Status: t.Status, Version: t.Version, GraphVersion: t.GraphVersion}
}

// SignalKind 是 Signal 的种类。
type SignalKind string

// Signal.Kind 取值。
const (
	SignalCancel SignalKind = "cancel" // 取消 Task：未结束的 Run 置为 CANCELED，子 Task 级联取消
	SignalPause  SignalKind = "pause"  // 暂停：之后到达活动节点的 Run 进入 WAITING(debug)
	SignalResume SignalKind = "resume" // 恢复：按 Resume 放行调试等待中的 Run
)

// Signal 是投递给 Task 的控制信号。
//   - cancel：Reason 为取消原因，空则为 "canceled"
//   - resume：Resume 为恢复参数（单步、继续、指定 Run、替换调试选项），nil 等同普通恢复
//
// 对已结束的 Task，Signal 返回当前状态的回执，不报错（重复取消是正常操作）。
//
// Signal is a control signal delivered to a task; signaling a finished task is not an error.
type Signal struct {
	Kind   SignalKind     `json:"Kind"`
	Reason string         `json:"Reason,omitempty"`
	Resume *ResumeRequest `json:"Resume,omitempty"`
}

// TaskSnapshot 是 Task 的当前视图（Task + Run；调度只依赖这两张表）。图快照在 Task.Graph 中。
type TaskSnapshot struct {
	Task Task  `json:"Task"`
	Runs []Run `json:"Runs"`
}

// ForEngine 确认快照属于指定引擎。加载失败时原样返回；属于其他引擎时改为 ErrNotFound，
// 避免把别的引擎的 Task 交给本引擎继续读。
func ForEngine(kind, taskID string, snap TaskSnapshot, err error) (TaskSnapshot, error) {
	if err == nil && snap.Task.EngineKind != kind {
		return TaskSnapshot{}, NotFoundTask(taskID)
	}
	return snap, err
}

// TaskSummary 是列表项：不含输入输出与图。
type TaskSummary struct {
	TaskID       string           `json:"TaskID"`
	TicketID     string           `json:"TicketID"`
	EngineKind   string           `json:"EngineKind"`
	GraphID      string           `json:"GraphID"`
	GraphVersion int64            `json:"GraphVersion"`
	Status       graph.StatusCode `json:"Status"`
	Error        string           `json:"Error,omitempty"`
	ParentTaskID string           `json:"ParentTaskID,omitempty"`
	StartedAt    time.Time        `json:"StartedAt"`
	FinishedAt   *time.Time       `json:"FinishedAt,omitempty"`
}

// Engine 是执行引擎的统一接口；immediate、durable 与第三方引擎都实现它。
// 后台调度（领取、续租、恢复扫描）由引擎在构造时自行启动、在 Close 时停止，不经过本接口。
// 同步等待结果由 service 基于 Watch 提供，不放进本接口。
//
// Engine is the contract every execution engine implements.
type Engine interface {
	Kind() string

	// Start 创建 Task 并开始执行，立即返回，不等待结束。
	Start(ctx context.Context, req StartRequest) (Receipt, error)
	// Signal 投递 Cancel / Pause / Resume；对已结束的 Task 返回当前状态，不报错。
	Signal(ctx context.Context, taskID string, sig Signal) (Receipt, error)
	// Snapshot 返回 Task 与全部 Run 的当前状态；Task 不存在或不属于本引擎时返回 ErrNotFound。
	Snapshot(ctx context.Context, taskID string) (TaskSnapshot, error)
	// List 查询本引擎的根 Task 摘要。
	List(ctx context.Context, query ListQuery) ([]TaskSummary, error)
	// Watch 推送 Task 的完整快照：第一条为订阅时的当前状态，之后每次变化推送一次；
	// 推送终态快照后关闭 channel。ctx 结束或读取失败时也关闭 channel。
	Watch(ctx context.Context, taskID string) (<-chan TaskSnapshot, error)
	// Close 停止接收新 Task，并按各引擎的语义处理执行中的 Task。
	Close(ctx context.Context) error
}

// GraphResolver 按 GraphID 解析子图的当前已保存版本。返回的图必须带身份：
// Graph.GraphID 等于 graphID（不一致时子图节点失败），Graph.GraphVersion 为该版本号；
// 子 Task 的 GraphID / GraphVersion 取自返回的图，之后只使用这一版本。
//
// GraphResolver returns the current saved version of graphID, carrying its GraphID and GraphVersion.
type GraphResolver func(ctx context.Context, graphID string) (graph.Graph, error)

// LoadedGraph 是引擎按 GraphID 加载子图的结果：成功时 Graph 带身份（GraphID、GraphVersion），失败时 Err 非空。
// 加载失败也要放入 kernel.Env.Graphs：内核据此让发起它的节点失败，而不是再次请求加载同一子图。
//
// LoadedGraph holds the result of resolving a subgraph by GraphID.
type LoadedGraph struct {
	Graph graph.Graph
	Err   error
}

// LoadGraphs 在 Apply 之外用 resolve 按 GraphID 加载 into 中尚未加载的子图，把结果写入 into
// （加载失败也写入，内核据此让节点失败）。resolve 为空时每一项记为加载失败。
// 若请求的 ids 为空或全部已在 into 中，说明内核重复请求已加载的子图，返回内核缺陷错误以避免死循环。
//
// LoadGraphs resolves missing subgraphs in ids into the map outside Apply, returning an error if none was missing.
func LoadGraphs(ctx context.Context, resolve GraphResolver, ids []string, into map[string]LoadedGraph) error {
	loaded := 0
	for _, id := range ids {
		if _, ok := into[id]; ok {
			continue
		}
		loaded++
		if resolve == nil {
			into[id] = LoadedGraph{Err: errors.New("graph resolver unavailable")}
			continue
		}
		g, err := resolve(ctx, id)
		into[id] = LoadedGraph{Graph: g, Err: err}
	}
	if loaded == 0 {
		return fmt.Errorf("kernel: subgraphs already loaded: %s", strings.Join(ids, ", "))
	}
	return nil
}

// Observer 接收引擎事件；两个引擎在相同时机调用。方法必须快速返回，不得阻塞推进。
type Observer interface {
	// TaskChanged 在 Task 状态写入后调用（immediate 为内存更新后，durable 为提交后）。
	TaskChanged(taskID string)
	// EngineError 报告后台调度错误（领取、续租、恢复、写库失败等）。
	EngineError(err error)
}

// EngineConfig 是两个引擎共用的装配配置。
// EngineConfig is the shared engine configuration.
type EngineConfig struct {
	// DB 由业务应用打开。使用 GormConfig 时，列名即字段名。
	DB *gorm.DB
	// Nodes 节点注册表；执行器由此查找。
	Nodes *node.Registry
	// ResolveGraph 子图解析；为空时子图节点执行失败。
	ResolveGraph GraphResolver
	// Observer 引擎事件回调；可为空。
	Observer Observer
	// Clock 测试用时钟；为空时使用 time.Now。
	Clock func() time.Time
}

// Builder 根据共用配置构造引擎。业务应用显式选择具体引擎包（immediate.Build、durable.Build 等），
// 交给 service 注册；引擎包与契约测试只依赖本包，不依赖 service。
//
// Builder constructs an engine from the shared configuration.
type Builder func(ctx context.Context, cfg EngineConfig) (Engine, error)

// Now 返回配置时钟的当前 UTC 时间。
func (c EngineConfig) Now() time.Time {
	if c.Clock != nil {
		return c.Clock().UTC()
	}
	return time.Now().UTC()
}

// Changed 通知 Observer；Observer 为空时什么也不做。
func (c EngineConfig) Changed(taskID string) {
	if c.Observer != nil {
		c.Observer.TaskChanged(taskID)
	}
}

// Fail 报告后台错误；Observer 为空时什么也不做。
func (c EngineConfig) Fail(err error) {
	if c.Observer != nil && err != nil {
		c.Observer.EngineError(err)
	}
}

// Check 校验必填配置。
func (c EngineConfig) Check() error {
	if c.DB == nil {
		return fmt.Errorf("%w: EngineConfig.DB required", ErrInvalid)
	}
	if c.Nodes == nil {
		return fmt.Errorf("%w: EngineConfig.Nodes required", ErrInvalid)
	}
	return nil
}
