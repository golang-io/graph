// Package graph 定义执行图模型（Graph / Node 实例 / Edge / InputRef）、
// 形状校验、表达式与模板工具，以及 JSON/哈希辅助。
// 节点合同（Spec/Execute）、注册表与编译校验位于 graph/node；
// 执行模型与 Engine 接口位于 graph/engine；定义存储、装配与引擎路由位于 graph/service。graph 永不依赖 node。
package graph

import (
	"encoding/json"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
)

// EndpointError 是失败捕获出端点的本地名（值为 "Error"）。
// 仅声明了该出端点的节点（如 stop_error）可走 From 本地名为 Error 的出边。
//
// EndpointError is the local name of the failure-capture outgoing endpoint.
const EndpointError = "Error"

// ErrorField 是错误出口之后可用的输出名：节点失败并沿 Error 出边继续时写入 Outputs[节点ID]["Error"]。
const ErrorField = "Error"

// EndpointSep 分隔端点 ID 中的「节点 ID」与「本地端点名」：endpointID = nodeID + "." + name。
//
// EndpointSep separates the node ID and the local endpoint name inside an endpoint ID.
const EndpointSep = "."

// FormatEndpointID 由节点 ID 与本地端点名拼出端点 ID：nodeID + "." + name。
// 本地端点名（如 Out / In / Case0 / Error）不含 '.'，因此可用最后一个 '.' 反解。
//
// FormatEndpointID returns nodeID + "." + name (local endpoint name).
func FormatEndpointID(nodeID, name string) string {
	return nodeID + EndpointSep + name
}

// ParseEndpointID 按最后一个 '.' 拆分端点 ID，得到节点 ID 与本地端点名。
// 本地端点名不含 '.'（见 ValidName），而节点 ID 允许含 '.'，故从右侧切分。
// 当缺少分隔符、节点 ID 为空或端点名为空时 ok=false。
//
// ParseEndpointID splits on the last '.' into node ID and local endpoint name.
func ParseEndpointID(id string) (nodeID, name string, ok bool) {
	i := strings.LastIndex(id, EndpointSep)
	if i <= 0 || i >= len(id)-1 {
		return "", "", false
	}
	return id[:i], id[i+1:], true
}

// OutKV 是节点的一条输出声明：Key 为字面量输出名（定义期确定，不支持插值），
// Value 为模板或表达式，由具体节点解释。这是通用 JSON 片段，不属于某个节点种类。
// JSON 键显式写为大驼峰字段名（与 Go 导出字段名一致），不使用 json:",omitempty" 省略键名。
type OutKV struct {
	Key   string `json:"Key"`
	Value string `json:"Value,omitempty"`
}

// 执行模式（TaskPolicy.ExecutionMode 与 UI.ExecutionMode 共用）。
const (
	ExecSerial   = "serial"   // 同一时刻只执行一个活动节点
	ExecParallel = "parallel" // immediate 引擎下并行分支同时执行，上限 MaxParallel
)

// MaxSubgraphDepth 是子图最大嵌套深度，根图深度为 0。子图可按 GraphID 引用自身，
// 没有这个上限时递归引用会无限派生子任务。
const MaxSubgraphDepth = 5

// InputSource 是 InputRef.From 的保留来源，表示运行输入（Task.Input）。
// `$` 不在 ValidID 允许的字符内，不会与节点 ID 冲突。
//
// InputSource is the reserved InputRef.From value referring to the run input.
const InputSource = "$input"

// Graph 执行图（与 UI 布局分离）。JSON 键：GraphID / GraphVersion / InputSchema / Nodes / Edges。
//
// GraphID + GraphVersion 是图的身份：二者任一不同即为不同的图（内容哈希包含它们）。
// 身份只由服务端写入，客户端请求中的取值一律被覆盖：
//   - 已保存版本：(GraphID, 版本号)，保存时与定义表的 GraphID / Version 列用同一个值写入；草稿为 (GraphID, 0)；
//   - 临时图：取启动请求顶层的 GraphID，GraphVersion 为 0；
//   - map 循环体等内嵌子图：沿用父 Task 的 GraphID / GraphVersion。
//
// InputSchema 声明运行输入（JSON Schema）：required 的键启动时必须存在，未在 properties 中声明的键拒绝；
// 节点用 {"From":"$input","Field":键} 引用。additionalProperties 应为 false。
// 入口节点为唯一没有入端点（也没有入边）的节点；图不允许成环。
//
// Graph is the executable flow, separate from canvas layout. GraphID + GraphVersion identify it
// and are always assigned by the server. InputSchema declares the run input.
type Graph struct {
	GraphID      string             `json:"GraphID,omitempty"`
	GraphVersion int64              `json:"GraphVersion,omitempty"`
	InputSchema  *jsonschema.Schema `json:"InputSchema,omitempty"`
	Nodes        []Node             `json:"Nodes"`
	Edges        []Edge             `json:"Edges"`
}

// filled 返回 Nodes / Edges 为 nil 时补成空切片的副本，使 nil 与空切片不会因编码为 null / [] 而被判为内容不同。
func (g Graph) filled() Graph {
	if g.Nodes == nil {
		g.Nodes = []Node{}
	}
	if g.Edges == nil {
		g.Edges = []Edge{}
	}
	return g
}

// ValidID 实体 id / 类型名的字符集限制。
func ValidID(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.:-", c)) {
			return false
		}
	}
	return true
}

// ValidName 报告 s 能否作为输入名、输出名或运行输入名：字母或下划线开头，后接字母、数字、下划线，最长 64。
// 输入名会作为 expr-lang 变量使用，因此必须是合法标识符。
func ValidName(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for i, c := range s {
		letter := c == '_' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
		if !letter && (i == 0 || c < '0' || c > '9') {
			return false
		}
	}
	return true
}

// Node 图节点。Kind 对接可执行节点种类，取值等于 NodeSpec.ID
// （控制节点为 __INNER__.map 等，activity 为 template 等）；不是类型系统意义上的 Type。
//
// Inputs 是定义期写入的「输入名 → 取值约定」表（保存进 Graph，运行时只读不改写）：
// 键为本节点本地输入名（表达式/模板可用）；值见 InputRef。
// 配置项（URL、Model、Chunk 等）与数据绑定共用这一张表。
// 各键允许的形态由 NodeSpec.InputSchema.properties[键] 约束（Resolve 时校验），
// 不是靠把 Value 做成 Go 泛型或 any。
//
// Node is a graph node; Kind joins the instance to its executable NodeSpec.ID.
// Inputs is the definition-time binding table (name → InputRef).
type Node struct {
	ID     string              `json:"ID"`
	Kind   string              `json:"Kind"`
	Name   string              `json:"Name,omitempty"`
	Inputs map[string]InputRef `json:"Inputs,omitempty"`
}

// InputRef 是单条输入的取值约定，形状封闭，From/Field 与 Value 互斥（恰选其一）：
//
//	引用：From + Field（Value 必须为空）
//	  - From 为来源节点 ID → 执行时取 Run.Outputs[From][Field]（同 Run 内多次执行取最新覆盖值）
//	  - From 为 "$input"（InputSource）→ 取本次任务运行输入的 Field
//	字面量：仅 Value（From、Field 必须为空）
//	  - Value 为一段合法 JSON 原文（可为 null、标量、对象、数组）
//
// Value 使用 json.RawMessage 而非 any / map[string]any：
// 定义库存的是未经二次解码的 JSON 字节，Compile 时再 DecodeStored 成具体值，
// 避免 float64 吃掉整数、也避免在模型层引入开放结构。
// 「这个字面量该是什么类型」不由 InputRef 表达，而由该输入名对应的
// NodeSpec.InputSchema（或图级 InputSchema，当 From=$input）在 Resolve 阶段校验。
//
// InputRef is a closed binding: either From+Field, or a literal JSON Value.
type InputRef struct {
	From  string          `json:"From,omitempty"`
	Field string          `json:"Field,omitempty"`
	Value json.RawMessage `json:"Value,omitempty"`
}

// Edge 有向边：从一个出端点连到一个入端点。同一次退出命中的全部边都会处理，
// 各分支持有独立的节点输出副本。边只决定执行走哪条路，不传递数据。
//
// From / To 都是端点 ID（endpointID = nodeID + "." + name）：
// From 为来源节点的某个出端点（本地名承载分支语义：Out 默认出口、Case0..Case15 分支、
// Error 失败捕获），To 为目标节点的某个入端点（通常为 In）。
// 分支语义由 From 端点的本地名表达（见 ParseEndpointID）。
//
// Edge connects one output endpoint to one input endpoint; both From and To are endpoint IDs.
type Edge struct {
	ID   string `json:"ID"`
	From string `json:"From"` // endpoint ID: sourceNodeID + "." + outputName
	To   string `json:"To"`   // endpoint ID: targetNodeID + "." + inputName
}

// FromNode 返回 From 端点所属的节点 ID（解析失败时为空）。
// FromNode returns the node ID of the From endpoint.
func (e Edge) FromNode() string { id, _, _ := ParseEndpointID(e.From); return id }

// FromName 返回 From 端点的本地名（承载分支语义：Out/Case0/Error 等）。
// FromName returns the local endpoint name of the From endpoint (branch semantics).
func (e Edge) FromName() string { _, name, _ := ParseEndpointID(e.From); return name }

// ToNode 返回 To 端点所属的节点 ID（解析失败时为空）。
// ToNode returns the node ID of the To endpoint.
func (e Edge) ToNode() string { id, _, _ := ParseEndpointID(e.To); return id }

// ToName 返回 To 端点的本地名（通常为 In）。
// ToName returns the local endpoint name of the To endpoint.
func (e Edge) ToName() string { _, name, _ := ParseEndpointID(e.To); return name }
