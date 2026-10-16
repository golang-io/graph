/**
 * 与后端 JSON 一一对应的类型：键名与 Go 字段名一致（大驼峰，缩写全大写，如 GraphID、TaskID）。
 * 画布内部的编辑模型（GraphEditorNode / GraphEditorEdge）是另一套小驼峰结构，由 document.ts 在边界转换。
 *
 * Wire types mirror the Go JSON (PascalCase keys); the editor model lives in document.ts.
 */
import type { GraphLayoutUI } from './document'

/** 运行输入的引用来源（graph.InputSource）：InputRef.From 取此值时读取 Task 的运行输入。 */
export const GRAPH_INPUT_SOURCE = '$input'

/** 默认入端点本地名（node.DefaultInput）：入边落在此端点，也是画布入口句柄 ID。 */
export const GRAPH_DEFAULT_INPUT = 'In'

/** 默认出端点本地名（node.DefaultOutput）：普通出边从此端点出发，也是画布主出口句柄 ID。 */
export const GRAPH_DEFAULT_OUTPUT = 'Out'

/** 失败捕获出端点本地名（graph.EndpointError）：节点失败时沿此出端点的边继续。 */
export const GRAPH_ENDPOINT_ERROR = 'Error'

/**
 * JSON Schema 片段（图级 Graph.InputSchema / 节点 NodeSpec.InputSchema）。
 * title → 表单标签；无 title → 绑定建议槽；enum → 下拉。
 * 图级运行输入要求每个 property 带 type（可嵌套 items / properties）。
 */
export type GraphJSONSchema = {
  type?: string | string[]
  title?: string
  description?: string
  enum?: unknown[]
  required?: string[]
  properties?: Record<string, GraphJSONSchema>
  additionalProperties?: boolean | GraphJSONSchema
  /** array 元素 Schema */
  items?: GraphJSONSchema | GraphJSONSchema[]
  minimum?: number
  maximum?: number
  minLength?: number
  maxLength?: number
  pattern?: string
  minItems?: number
  maxItems?: number
}

/**
 * 节点输入引用（graph.InputRef）。三选一：
 *   - { From: 节点ID, Field: 输出名 }：读取上游节点的输出；
 *   - { From: '$input', Field: 运行输入名 }：读取运行输入；
 *   - { Value: 任意 JSON }：字面量。
 */
export type GraphInputRef = {
  From?: string
  Field?: string
  Value?: unknown
}

export type GraphNodeJSON = {
  ID: string
  Kind: string
  Name?: string
  /** 全部参数与数据绑定（字面量 Value 或 From/Field 引用） */
  Inputs?: Record<string, GraphInputRef>
}

/**
 * 边（graph.Edge）：From / To 都是端点 ID（endpointID = nodeID + "." + localName）。
 * From 为来源节点的出端点（本地名承载分支语义：out/case0..case15/error），
 * To 为目标节点的入端点（通常为 in）。不再有 When 字段。
 */
export type GraphEdgeJSON = {
  ID: string
  From: string
  To: string
}

export type GraphJSON = {
  /**
   * 图身份：GraphID + GraphVersion 唯一确定一个已保存版本（草稿为 0，临时图 GraphID 可为空）。
   * 只由服务端写入；保存、运行时请求中的取值一律被服务端覆盖，前端原样带回即可。
   */
  GraphID?: string
  GraphVersion?: number
  /**
   * 运行输入 JSON Schema：properties 为声明的键；required 必填；
   * additionalProperties 应为 false（未声明键拒绝）。
   */
  InputSchema?: GraphJSONSchema
  Nodes: GraphNodeJSON[]
  Edges: GraphEdgeJSON[]
}

/** 图定义的一个版本（tb_graph_definition 的一行）。Version=0 为未保存的草稿。 */
export type GraphDefinition = {
  GraphID: string
  Name: string
  Description?: string
  Graph: GraphJSON
  UI?: GraphLayoutUI
  Engine?: string
  Version?: number
  Label?: string
  CreatedAt?: string
  UpdatedAt?: string
}

/** 画布编辑的文档：图 + 布局，GraphID / Name 可缺省（临时图）。 */
export type GraphUI = Partial<Pick<GraphDefinition, 'GraphID' | 'Name'>> & Omit<GraphDefinition, 'GraphID' | 'Name'>

/** 后端状态值（大写）。 */
export type GraphStatus = 'PENDING' | 'RUNNING' | 'WAITING' | 'SUCCEEDED' | 'FAILED' | 'CANCELED' | 'SKIPPED'

/**
 * 节点级调试注入（对应后端 engine.DebugInject）：
 * - Vars：按输入名覆盖或补充该节点的输入参数；
 * - Failed：为 true 时进入该节点直接按失败处理（不实际调用执行器），用于测试重试或错误出口；
 * - Error：Failed 为 true 时的自定义失败信息。
 *
 * Node-level debug injection (engine.DebugInject).
 */
export type GraphDebugInject = {
  Vars?: Record<string, unknown>
  Failed?: boolean
  Error?: string
}

/**
 * Task 调试选项（对应后端 engine.DebugOptions）：
 * - Step：每进入一个活动节点前停在 WAITING(debug)；
 * - Breakpoints：活动节点 ID 列表（内联节点不可设断点）；
 * - Injects：按节点 ID 覆盖输入或模拟失败（对活动节点与内联节点均生效）。
 *
 * Task debug options (engine.DebugOptions).
 */
export type GraphDebugOptions = {
  Step?: boolean
  Breakpoints?: string[]
  Injects?: Record<string, GraphDebugInject>
}

/**
 * Task 执行策略（对应后端 engine.TaskPolicy）。
 *
 * Task execution policy (engine.TaskPolicy).
 */
export type GraphTaskPolicy = {
  FailPolicy?: 'fail_fast' | 'continue'
  ExecutionMode?: 'serial' | 'parallel'
  NodeTimeoutMS?: number
  MaxAttempts?: number
  MaxParallel?: number
  RetryDelayMS?: number
  Debug?: GraphDebugOptions
}

/**
 * 恢复或推进调试等待中的 Task 请求体（对应后端 engine.ResumeRequest）：
 * - DebugAction：''（普通恢复）| 'step'（开启单步并放行一个调试等待 Run）| 'continue'（关闭单步并放行全部调试等待 Run）
 * - Debug：非空时替换 Task 的调试选项（如在线增删断点、修改注入）
 * - TargetRunID：只放行指定的 Run
 *
 * Resume request payload (engine.ResumeRequest).
 */
export type GraphResumeRequest = {
  Debug?: GraphDebugOptions
  DebugAction?: '' | 'step' | 'continue'
  TargetRunID?: string
}

/** 一次完整的图执行（runtime.Task）。 */
export type GraphTask = {
  TaskID: string
  TicketID: string
  EngineKind: string
  GraphID: string
  /** 执行的图定义版本号；临时图为 0。 */
  GraphVersion: number
  /** Task 行的写回计数，每次写入加 1（不是图版本）。 */
  Version?: number
  Status: GraphStatus
  Policy?: GraphTaskPolicy & Record<string, unknown>
  Input?: Record<string, unknown>
  Output?: Record<string, unknown>
  Error?: string
  Paused?: boolean
  ParentTaskID?: string
  /** 本次执行使用的图快照；只在完整快照中出现，watch 增量的 Task 省略它。 */
  Graph?: GraphJSON
  StartedAt: string
  FinishedAt?: string
}

/** 列表项摘要（GET /tasks 返回的 engine.TaskSummary）：不含输入输出与图快照。 */
export type GraphTaskSummary = {
  TaskID: string
  TicketID: string
  EngineKind: string
  GraphID: string
  GraphVersion: number
  Status: GraphStatus
  Error?: string
  ParentTaskID?: string
  StartedAt: string
  FinishedAt?: string
}

/** 调度单元（runtime.Run）：包含画布染色与运行明细面板所需的字段。 */
export type GraphRun = {
  RunID: string
  TaskID: string
  EngineKind?: string
  ParentRunID?: string
  Slot?: string
  NodeID: string
  FromEdgeID?: string
  Seq: number
  Attempt?: number
  Status: GraphStatus
  /** 等待原因：join（汇合）/ subgraph（子任务）/ debug（暂停、单步、断点） */
  WaitReason?: string
  Error?: string
  /** 本路径上各节点的输出：节点ID → 输出名 → 值 */
  Outputs?: Record<string, Record<string, unknown>>
  /** 到达 end 节点时的最终输出 */
  Output?: Record<string, unknown>
  JoinNodeID?: string
  LiveChildren?: number
  StartedAt?: string
  CreatedAt?: string
}

/** GET /tasks/{id} 与 watch 首包的快照（Task + Run 当前态）。 */
export type GraphTaskSnapshot = {
  Task: GraphTask
  Runs: GraphRun[]
}

/**
 * GET /tasks/{id}/watch 的一个 SSE 事件。
 * 带 Snapshot 时是完整快照，用它替换本地状态；否则把 Task / Runs 合并进已有快照。
 */
export type GraphWatchEvent = {
  Snapshot?: GraphTaskSnapshot
  Task?: GraphTask
  Runs?: GraphRun[]
  RemovedRunIDs?: string[]
}
