# Graph HTTP 接口

`api.Handler(rt, prefix)` 返回标准 `http.Handler`，承载两组接口：图定义（`/graphs`、`/node-specs`、`/engines`）与 Task 控制面（`/tasks`）。本目录每个文件描述一个接口；本文件说明所有接口共用的约定与数据结构。

## 通用约定

| 项目 | 约定 |
| --- | --- |
| 路径前缀 | 由 `api.Handler` 的 `prefix` 参数决定；demo（`cmd/`）为 `/api`。各文档中的路径不含前缀。 |
| 路径段 | 小写，如 `/tasks/{id}/resume`。 |
| 编码 | 请求、响应均为 JSON（`Content-Type: application/json`）；`watch` 为 `text/event-stream`。 |
| 键名 | 请求体、响应体、查询参数的键名与 Go 字段名一致：大驼峰，缩写全大写（`TaskID`、`GraphID`、`URL`、`NodeTimeoutMS`）。 |
| 严格解码 | 请求体出现未定义的键返回 `400 Invalid`（例如旧的 `graph_id`）。Go `encoding/json` 匹配键名时不区分大小写，客户端仍应按文档的大小写发送。 |
| 数字 | 输入与节点输出中的整数保持整数（`11`，不是 `11.0`）。 |
| 时间 | RFC 3339，UTC。 |
| 列表响应 | `{"Data": [...]}`；条数取 `Data` 的长度。 |

## 错误响应

所有错误都返回 `{"Error": "可读的错误信息", "Code": "错误码"}`。

| HTTP 状态 | Code | 含义 |
| --- | --- | --- |
| 404 | `NotFound` | 图、版本、Task、执行引擎不存在；草稿（Version 0）在启动 Task 时也返回 NotFound（需先保存）。 |
| 409 | `Conflict` | 状态不允许该操作：没有可放行的调试等待、已暂停、图已删除等。对已结束的 Task 发 resume / pause / cancel 不返回 409，而是 `202` 与当前终态回执。 |
| 400 | `Invalid` | 请求体缺失或格式错误、未知键、图校验失败、策略越界、查询参数非法。 |
| 400 | `Unsupported` | 引擎已关闭或 durable 处于恢复模式、响应不支持流式输出。 |
| 500 | `Corrupt` | 存储中的执行数据损坏；不回显内部细节，需要运维排查。 |
| 500 | `Internal` | 其他内部错误。 |

```json
{"Error": "graph runtime: conflict: task is already paused", "Code": "Conflict"}
```

## 执行引擎

两个引擎共用同一套图语义（`engine/kernel` 的 `Apply`）：谁进哪个节点、fork/join、子图、重试、调试两边相同。
差别只在调度方式与持久化方式——写图 / 调节点的人不需要关心引擎；部署与运维需要。

| 维度 | `immediate` | `durable` |
| --- | --- | --- |
| 调度单元 | 每个 Task 一个进程内处理循环，逐个处理事件；活动节点受全局并发上限（默认 256，名额不足时 Run 保持 `PENDING`）与 `MaxParallel` 限制 | 多 worker 按分片用条件更新领取到期的 PENDING Run |
| 热路径状态 | 全量在内存；运行期间不读写库 | 库即调度队列；claim / lease 是主路径 |
| 写库时机 | 只在 Task 结束时一次写入 Task 与全部 Run；执行中的 Task 只能在执行它的进程内查询与控制；结束并写库后，查询直接读表（任何进程都可查询），引擎内存中另外保留 1 分钟 | 每次写回是一个事务（锁 Task 行）；活动节点的结果先单独保存到自己的 Run 行，再由写回事务应用 |
| 租约含义 | 不使用租约 | Run 级执行租约；每次领取时租约令牌加 1，续租与写回都校验令牌，旧执行者的结果因令牌不一致被丢弃。分片、租约等调度数据只供 durable 内部使用，不出现在 Run 的 JSON 中 |
| 故障语义 | 进程退出：执行中的 Task 丢失，库中没有记录；引擎正常关闭时执行中与停在调试等待上的 Task 置为 `CANCELED`（原因 `engine closed`） | 进程退出后，租约过期的 Run 回到 `PENDING`（`Attempt` 不变，失联次数由 durable 内部记录、不在 JSON 中输出），由其他 worker 重新执行；同一节点第 4 次失联时按执行失败（`executor lost 4 times`）处理，配置了 `MaxAttempts` 时重试，否则节点失败。结果已保存但尚未应用的 Run 不重新执行，租约过期后直接应用该结果。引擎正常关闭时等待执行中的节点写回，未完成的 Run 保持 `RUNNING`，由租约过期回收 |
| 适用场景 | 低延迟、单进程；也可纯内存 `Execute`（不写库） | 多实例、多分片、需要崩溃后续跑 |

两个引擎的 HTTP 行为一致：同一个图、同一份输入在两个引擎下得到相同的快照结构与输出。

## 状态值

`Status` 取值（Task、Run 共用）：`PENDING`、`RUNNING`、`WAITING`、`SUCCEEDED`、`FAILED`、`CANCELED`、`SKIPPED`。终态为 `SUCCEEDED`、`FAILED`、`CANCELED`、`SKIPPED`。

Task 的 `WAITING` 表示当前没有 `PENDING` / `RUNNING` 的 Run，且有 Run 停在等待上（子图、调试）；它用于展示，Task 仍未结束，等待结束后 Task 回到 `RUNNING`。节点不能自行挂起 Run 等待外部事件，所有等待都由引擎产生。

Run 的 `WaitReason`（只在 `WAITING` 时有值）：

| 值 | 含义 | 由谁结束等待 |
| --- | --- | --- |
| `join` | fork 后等待子 Run 汇合（父 Run 上） | 子 Run 到达汇合节点 |
| `subgraph` | 子图节点或 map 节点等待自己启动的子 Task 结束；全部子 Task 结束前 Run 不离开该节点（map 某一轮失败后等已启动的轮次结束再失败） | 子 Task 结束 |
| `debug` | 暂停、单步或断点，在进入节点前停住 | [resume-task](resume-task.md) |

## 数据结构

### Definition（图定义）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `GraphID` | string | 图标识。 |
| `Version` | int64 | 版本号；`0` 为新建后尚未保存的草稿，保存后从 `1` 递增。`GraphID` + `Version` 唯一确定一个已保存版本。 |
| `Name` / `Description` | string | 名称、描述（可省略）。 |
| `Graph` | Graph | 执行图：`{"GraphID": "…", "GraphVersion": 1, "InputSchema": {"type":"object","properties":{…},"required":[…],"additionalProperties":false}, "Nodes": [{"ID","Kind","Name","Inputs"}], "Edges": [{"ID","From","To"}]}`。`GraphID` / `GraphVersion` 是图身份（任一不同即为不同的图），只由服务端写入，与 `Definition` 的 `GraphID` / `Version` 同值（草稿为 0）；保存、启动请求以及 map `Body` 中自带的取值一律被覆盖。`Edge.From` / `Edge.To` 为端点 ID（`节点ID.本地端点名`，如 `calc.Out` / `finish.In`）；分支语义由 `From` 端点本地名承载（`Out` / `Case0`..`Case15` / `Error`）；switch 的出边只能连在其参数中已配置的分支上（`Cases` 为空时为 `Case0`/`Case1`），否则保存返回校验错误。图不允许成环；map 的循环体是参数 `Body` 中的内嵌子图。图级与节点级 `Inputs` 结构见下文「Graph 的输入与输出」。 |
| `UI` | object | 布局：主图层 `Layout` 字段（`Positions`、`EndpointSides`、`EdgeRails`、`SubLayouts`）、`ExecutionMode`、`StickyNotes`（可省略）。`SubLayouts` 的值仍是同一套 `Layout`（含子图层 `Collapsed`）。图没有整体方向；保存时每个节点（含内嵌子图节点）的每个实例端点（如 `In`、`Out`、switch 的各个分支）都必须在 `EndpointSides` 中写明所在边，出口不能与入口同边。字段说明见 [save-graph.md](save-graph.md)。 |
| `Engine` | string | 默认执行引擎（可省略）。 |
| `Label` | string | 版本标签（可省略）。 |
| `CreatedAt` / `UpdatedAt` | time | 创建、更新时间。 |

### Graph 的输入与输出

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `Graph.InputSchema` | object | 运行输入 JSON Schema：`properties` 为声明的键，`required` 为必填键，`additionalProperties` 应为 `false`。启动时缺少必填项、或带了未声明的键，返回 `Invalid`。 |
| `Node.Inputs` | object | 输入名 → 引用。引用三选一：`{"From": 节点ID, "Field": 输出名}`、`{"From": "$input", "Field": 运行输入名}`、`{"Value": 任意 JSON}`（字面量）。输入名须为标识符（字母或下划线开头，最长 64）。 |

节点的出口载荷见 [list-node-specs](list-node-specs.md) 返回的 `OutputsByEndpoint`；template、code、merge 的输出由参数（`Node.Inputs` 字面量）决定并写入默认出口键。保存与编译时校验：引用的节点存在且有该输出、在控制流上能从来源字段所属出口到达引用方、`Error` 字段只在错误出口之后使用、表达式只使用本节点输入名。map 的循环体按同样规则递归校验：循环体内只能引用循环体内的节点与自己的 `$input`；循环体的必填运行输入必须由 map 提供（`Item`、`Index`，或 map 实例上同名的自定义输入）。运行时引用的节点在本路径上没有执行，引用方节点失败。

入口节点为唯一的 `__INNER__.start`（无入边、至少一条出边）；运行输入仍由 `Graph.InputSchema` / `$input` 声明。

### Receipt（动作回执）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `TaskID` | string | 目标 Task。 |
| `EngineKind` | string | 执行该 Task 的引擎。 |
| `Status` | string | 动作完成后 Task 的状态；对已结束的 Task 发控制动作时为当前终态。 |
| `Version` | int64 | Task 的写回计数（每次写回加 1），不是图版本。 |
| `GraphVersion` | int64 | 执行所用的图定义版本号；[start-task](start-task.md) 与 resume / pause / cancel 的回执都返回，执行临时图时省略。 |

### TaskSnapshot（Task 快照）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `Task` | Task | Task 当前状态，含图快照 `Graph`。 |
| `Runs` | Run[] | Task 的全部 Run（含已结束），按 RunID 排序。 |

### Task

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `TaskID` | string | Task 标识（UUID v7，时间有序）。 |
| `TicketID` | string | 业务分组；未指定时等于 `TaskID`。 |
| `EngineKind` | string | `immediate` / `durable`。 |
| `GraphID` / `GraphVersion` | | 执行所用的图；`GraphVersion` 为图定义版本号，临时图为 0。与 `Graph` 快照中的 `GraphID` / `GraphVersion` 同值。map 循环体派生的子 Task 沿用父 Task 的这两个值，实际执行的图以 `Graph` 快照为准。 |
| `Version` | int64 | Task 的写回计数：Task 或任一 Run 每次写回加 1；订阅方可用它判断 Task 是否变化。 |
| `Status` | string | 见「状态值」。 |
| `Policy` | TaskPolicy | 规整后的执行策略，见 [start-task](start-task.md)。 |
| `Input` / `Output` | object | 运行输入；到达 end 节点后的输出（即 end 节点的输入）。 |
| `Error` | string | 失败或取消原因。 |
| `Paused` | bool | 是否已暂停。 |
| `ParentTaskID` / `ParentRunID` / `Slot` | string | 子图 Task 指向父 Task、父 Task 中等待它的 Run，以及发起时的槽位（子图节点为空串，map 节点为轮次下标 `"0"`、`"1"`…）。 |
| `Depth` | int | 子图嵌套深度，根为 0（为 0 时省略）。起始 Run 是 `Runs` 中 `ParentRunID` 为空的唯一一条。 |
| `Graph` | Graph | 本次执行使用的图快照（不随定义库后续修改而变化）。只在完整快照中出现；[watch-task](watch-task.md) 增量中的 `Task` 省略它。 |
| `StartedAt` / `FinishedAt` / `CreatedAt` | time | 时间。 |

### Run

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `RunID` / `TaskID` / `EngineKind` | string | 标识。 |
| `ParentRunID` / `Slot` | string | fork 产生的子 Run 指向父 Run；`Slot` 为出边 ID。 |
| `NodeID` / `FromEdgeID` / `Seq` | | 当前节点、进入该节点经由的边、移动次数（每进入一个节点加 1）。 |
| `Outputs` / `Output` | object | 节点输出：`{节点ID: {输出名: 值}}`，fork 时复制给子 Run，汇合时按节点 ID 合并；到达 end 时的输出。 |
| `State` | object / JSON | 当前节点访问中控制节点返回的状态（如 map 的遍历进度）；Run 进入下一个节点时清空。 |
| `JoinNodeID` / `LiveChildren` / `Arrivals` | | 汇合进度（只在父 Run 上）：汇合节点、尚未结束的子 Run 数、各分支到达时的 `Outputs`（键为 Slot）。汇合方式由汇合节点的声明决定（merge 的 `Mode`）。 |
| `Status` / `WaitReason` / `AvailableAt` | | 调度状态；`AvailableAt` 为 Unix 毫秒，`PENDING` 时不早于该时刻执行。 |
| `Attempt` | int | 当前节点访问的第几次尝试（业务重试），从 1 开始；durable 租约回收不增加，同一节点第 4 次失联按执行失败处理时增加。 |
| `Error` / `StartedAt` / `CreatedAt` | | 错误与时间。 |



## 接口索引

### 图定义

| 接口 | 文档 |
| --- | --- |
| `GET /graphs` | [list-graphs](list-graphs.md) |
| `POST /graphs` | [create-graph](create-graph.md) |
| `GET /graphs/{id}` | [describe-graph](describe-graph.md) |
| `PUT /graphs/{id}` | [save-graph](save-graph.md) |
| `DELETE /graphs/{id}` | [delete-graph](delete-graph.md) |
| `GET /graphs/{id}/versions` | [list-graph-versions](list-graph-versions.md) |
| `GET /graphs/{id}/versions/{version}` | [describe-graph-version](describe-graph-version.md) |
| `PUT /graphs/{id}/engine` | [set-graph-engine](set-graph-engine.md) |
| `POST /graphs/examples` | [load-example-graphs](load-example-graphs.md) |
| `GET /node-specs` | [list-node-specs](list-node-specs.md) |
| `GET /engines` | [list-engines](list-engines.md) |

### Task 控制面

| 接口 | 文档 |
| --- | --- |
| `POST /tasks` | [start-task](start-task.md) |
| `GET /tasks` | [list-tasks](list-tasks.md) |
| `GET /tasks/{id}` | [describe-task](describe-task.md) |
| `GET /tasks/{id}/watch` | [watch-task](watch-task.md) |
| `POST /tasks/{id}/resume` | [resume-task](resume-task.md) |
| `POST /tasks/{id}/pause` | [pause-task](pause-task.md) |
| `POST /tasks/{id}/cancel` | [cancel-task](cancel-task.md) |
