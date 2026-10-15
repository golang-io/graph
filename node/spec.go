package node

import (
	"encoding/json"

	"github.com/golang-io/graph"
	"github.com/google/jsonschema-go/jsonschema"
)

// DefaultEndpoints 返回普通活动节点的端点声明：一个默认入端点 DefaultInput、一个默认出端点 DefaultOutput。
//
// DefaultEndpoints returns the single-in / single-out endpoints of a plain activity node.
func DefaultEndpoints() EndpointSpec {
	return EndpointSpec{Inputs: []string{DefaultInput}, Outputs: []string{DefaultOutput}}
}

// NodeSpec 是一种节点在注册表中的可序列化描述（种类级，不是图上某个实例）。
// 由 Node.Spec() 返回，经 Registry.Register / List / Get 下发；执行逻辑仍在 Node.Execute。
// 参数语义钩子（Validate / DeclareOutputs / Expressions 等）挂在 paramsDecode 解码后的结构体上，不序列化。
//
// NodeSpec is the serializable catalog description of a node kind.
type NodeSpec struct {
	// ID 是种类身份：注册表主键，也是图实例 Nodes[].Kind 的取值。
	// 后端：Register 按 ID 唯一入库；Lookup / Compile / Execute 用 Kind 反查本 Spec。
	// 前端：节点库与画布上的 nodeType；拖入画布时写入实例 Kind。
	// 内置控制节点使用 __INNER__.{kind}；业务节点不得占用该前缀。
	ID string `json:"ID"`

	// Name 是列表与画布卡片用的展示名；Register 时必填。
	// 前端：节点库条目与卡片标题。
	Name string `json:"Name"`

	// Description 是种类级说明文案（节点库副标题、卡片 hint 等）。
	// 后端：Get/List 原样下发；不参与 Execute。
	// 前端：节点库与画布辅助展示；与图定义 Definition.Description 无关。
	Description string `json:"Description,omitempty"`

	// Group 是节点库分组键（如 control / transform）；空则前端归入 general。
	// 后端：Get/List 原样下发；不参与 Execute。
	Group string `json:"Group,omitempty"`

	// GroupLabel 是分组在节点库中的显示名（如「控制」）；空则前端用 Group 键展示。
	GroupLabel string `json:"GroupLabel,omitempty"`

	// DefaultInputs 是新建节点时的默认 Inputs（字面量为主）。
	// 后端：Get/List 原样下发；Compile 不自动合并进实例。
	// 前端：拖入画布时写入实例 Inputs。
	DefaultInputs map[string]graph.InputRef `json:"DefaultInputs,omitempty"`

	// paramsDecode 严格解码字面量参数对象，由 NodeSpec.decodeParams 调用；不序列化。
	// paramsDecode decodes the literal inputs JSON object into the typed parameter struct.
	paramsDecode func(json.RawMessage) (any, error) `json:"-"`

	// paramsMerge 在节点参数字段存在动态绑定（From != ""）或调试注入覆盖时，
	// 将运行期覆盖值合入编译期静态参数结构体副本；不序列化。
	paramsMerge func(base any, overrides map[string]any) (any, error) `json:"-"`

	// paramFields 是参数结构体的导出 JSON 字段索引（字段名 → 结构体字段下标），只读；不序列化。
	paramFields map[string]int `json:"-"`

	// Icon / Tone / Renderer 是画布展示字段（图标键、色调、几何角色扩展点名）。
	// 后端：Get/List 原样下发；不参与 Execute。
	// 前端：卡片样式；未知 Renderer 回落通用节点。
	// 每个端点句柄所在的边由节点实例写在 graph.UI.EndpointSides（保存时实例的每个端点都必须写明），与本字段无关。
	Icon     string `json:"Icon,omitempty"`
	Tone     string `json:"Tone,omitempty"`
	Renderer string `json:"Renderer,omitempty"`

	// Endpoints 声明种类级固定端点上限（Inputs/Outputs）；Register 时至少声明一类。
	// 后端：Register 校验；Registry.Endpoints 原样返回种类端点；实例实际启用的出口由 ActiveOutputsDeclarer
	// 决定（见 Registry.NodeEndpoints），拓扑校验、画布句柄校验与编译索引均按实例端点工作。
	// 前端：默认按 Spec.Endpoints 画句柄；动态分支节点（如 switch）按实例配置收敛启用出口。
	// 效果：决定图上有哪些端点、能否保存非法连线、运行时走哪个出端点。
	// 普通活动节点通常声明 Inputs:in、Outputs:out。
	Endpoints EndpointSpec `json:"Endpoints"`

	// Inline 声明本种类的 Executor 为内联节点：内核在处理事件时同步调用 Execute，
	// 不经过 PENDING / RUNNING，没有重试与节点超时，也不在其前停下调试。
	// 只能用于不做 IO、不读时钟与随机数、只依赖输入且耗时可以忽略的节点（如 switch、template）。
	// 实现了 Controller 的种类无论本字段取值都是内联节点（见 Registry.Inline）。
	// 种类级声明，不进入图定义；不下发前端。业务节点默认 false，即活动节点。
	//
	// Inline marks an Executor kind as an inline node run synchronously by the kernel.
	Inline bool `json:"-"`

	// Presets 是同一 ID 下的作者态快捷入口（不同默认 Inputs / 展示名）。
	// 后端：只随注册表下发，不单独注册种类；落图 Kind 仍等于本 Spec.ID。
	// 前端：节点库里多出若干可拖条目（如「多路分支」），拖入时用 Preset.Inputs 作初始 Inputs。
	Presets []Preset `json:"Presets,omitempty"`

	// InputSchema 描述实例 Node.Inputs 的 JSON Schema（WithParams 从参数结构体推导：类型来自字段，title/enum/bind 来自结构体标签）。
	// 后端：绑定校验检查 required 键必须出现；additionalProperties 允许自定义输入名。
	// 前端：properties 中有 title 的进属性表单（控件自行推测），无 title 的作绑定建议槽；enum → 下拉。
	InputSchema *jsonschema.Schema `json:"InputSchema,omitempty"`

	// OutputsByEndpoint 是各控制流出端点的载荷 JSON Schema（仅标准关键字）。
	// 键为本地出端点名；空字符串表示所有正常出口（不含 error）共享的载荷。
	// 后端：与 DeclareOutputs（并入默认键）及 Error 出口合并后做引用校验与 ValidateOutput。
	// 前端：按出端点分组展示可引用字段。
	OutputsByEndpoint map[string]*jsonschema.Schema `json:"OutputsByEndpoint,omitempty"`
}

// 节点输入结构体（如 builtin.HTTPInput）可选实现的方法；decodeParams 解码后直接对结果做接口断言。
// Optional interfaces implemented by node parameter structs, checked via type assertion after decodeParams.
type (
	// InputValidator 做语义校验；未实现时只做严格解码（拒绝未知字段）。
	InputValidator interface{ Validate() error }
	// OutputDeclarer 由参数决定默认出口（空键）上的输出 Schema（template / code / merge）。
	// 方法名不用 Outputs，避免与参数字段 Outputs 同名冲突。
	OutputDeclarer interface{ DeclareOutputs() *jsonschema.Schema }
	// ExpressionDeclarer 交出参数中的模板与表达式，供标识符校验。
	ExpressionDeclarer interface{ Expressions() []Expression }
	// JoinModeDeclarer 声明本节点作为多入边汇合点时的方式（JoinAny / JoinAll，merge 的 Mode）。
	// 未实现的节点被多条分支汇入时按 JoinAll 等待全部到齐。
	JoinModeDeclarer interface{ JoinMode() string }
	// ReferenceDeclarer 交出参数中的额外引用（merge First），参与来源校验。
	ReferenceDeclarer interface{ References() []NamedRef }
	// SubgraphDeclarer 交出参数中内嵌的子图（map 的 Body），Registry.Validate 递归校验它们。
	SubgraphDeclarer interface{ Subgraphs() []Subgraph }
	// ActiveOutputsDeclarer 由参数给出实例实际启用的出口，须是种类 Endpoints.Outputs 的子集
	// （switch 只启用配置中出现的分支）。Registry.Validate 拒绝连在未启用出口上的边；
	// 种类端点（Registry.Endpoints）不受影响。返回 nil 表示全部启用。
	ActiveOutputsDeclarer interface{ ActiveOutputs() []string }
)

// Subgraph 是参数中内嵌的一张图。Provides 是节点运行时固定传给子图的输入名（如 map 的 item / index）；
// 此外节点实例上的自定义输入（不属于参数结构体的输入名）也会按同名传入。
// 校验要求子图 InputSchema 的必填输入都在这两类名字之内。
type Subgraph struct {
	Path     string
	Graph    graph.Graph
	Provides []string
}

// NamedRef 是参数中的一条命名引用，Path 用于错误消息。
type NamedRef struct {
	Path string
	Ref  graph.InputRef
}

// Expression 是节点参数中的一条表达式或模板，交给 graph 包统一校验标识符：
// 表达式只能使用本节点的输入名与 Locals，其他名字在定义期拒绝。
//
// Expression is one expression or template inside node params, exposed for identifier validation.
type Expression struct {
	Path     string   `json:"Path"`               // 参数中的位置，用于错误消息，例如 "Outputs[0].Value"
	Text     string   `json:"Text"`               // 表达式或模板文本
	Template bool     `json:"Template,omitempty"` // true 表示 {{…}} 模板，false 表示 expr-lang 表达式
	Locals   []string `json:"Locals,omitempty"`   // 节点自身的局部名，例如 reduce 的 Acc、Item、Index
}

// Preset 是同一节点协议的作者态快捷入口；ID 只用于节点库，落图仍写 graph.Graph.Node.Kind（等于 NodeSpec.ID）。
type Preset struct {
	ID     string                    `json:"ID"`
	Label  string                    `json:"Label"`
	Inputs map[string]graph.InputRef `json:"Inputs,omitempty"`
}
