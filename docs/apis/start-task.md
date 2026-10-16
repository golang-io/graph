# 启动 Task

按图定义启动一次执行（Task），立即返回回执；执行在后台进行，用 [describe-task](describe-task.md) 或 [watch-task](watch-task.md) 查看进度。

- 路径：`/tasks`
- Method：`POST`

## 请求体

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `GraphID` | string | 条件 | 要执行的图。未提供 `Graph` 时必填。 |
| `GraphVersion` | int64 | 否 | 指定图定义版本号；省略时使用当前已保存版本（草稿 Version 0 不能运行）。 |
| `Engine` | string | 条件 | `immediate` / `durable`；省略时使用图的默认引擎（见 [set-graph-engine](set-graph-engine.md)），两者都没有时返回 `400 Invalid`。 |
| `TicketID` | string | 否 | 业务分组标识；省略时等于新 `TaskID`。 |
| `Graph` | Graph | 否 | 临时图：提供且未指定 `GraphVersion` 时直接执行该图，不要求已保存；与 `GraphVersion` 同时提供时必须与该版本的执行图内容一致。`Graph.GraphID` / `Graph.GraphVersion` 会被忽略：Task 与图快照的身份取本请求顶层的 `GraphID` / `GraphVersion`（临时图为 0）。 |
| `Input` | object | 条件 | 运行输入，按 `Graph.InputSchema` 做 JSON Schema 校验：键必须在 `properties` 中声明，`required` 中的键必须提供，且值类型须符合各属性声明的 `type`。节点通过 `{"From": "$input", "Field": 键}` 引用。 |
| `Policy` | TaskPolicy | 否 | 执行策略，见下表；省略的字段取默认值。 |

### TaskPolicy

| 字段 | 类型 | 默认 | 取值范围 | 说明 |
| --- | --- | --- | --- | --- |
| `FailPolicy` | string | `fail_fast` | `fail_fast` | `fail_fast`：任一节点执行失败（重试耗尽后）即终止整个 Task 并记为 `FAILED`。 |
| `ExecutionMode` | string | `serial` | `serial` / `parallel` | 只影响 immediate：`serial` 同一时刻只执行一个活动节点，`parallel` 时并行分支同时执行。durable 的并行分支总是由 worker 并发领取。 |
| `MaxParallel` | int | 8 | 正整数 | immediate `parallel` 模式的并发上限。 |
| `MaxAttempts` | int | 1 | 正整数 | 活动节点最多执行次数。 |
| `RetryDelayMS` | int64 | `MaxAttempts>1` 时 1000，否则 0 | 非负 | 重试间隔。 |
| `NodeTimeoutMS` | int64 | 0（不限） | 非负 | 单个活动节点的超时。 |
| `Debug` | object | 无 | | 调试：`Step`（bool，每进入一个节点前停住，起止节点 `start` / `end` 除外）、`Breakpoints`（节点 ID 列表；活动节点与内联节点均支持断点）、`Injects`（节点 ID → `{Vars, Failed, Error}`，按输入名覆盖或新增该节点的输入，`Failed` 为 true 时该节点直接失败）。调试时强制串行。 |

## 响应

`202 Accepted`：`Receipt`，见 [README](README.md#receipt动作回执)。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | 请求体缺失或超过 16 MiB、未知键（如 `graph_id`，以及已改名为 `GraphVersion` 的旧字段 `Version`）、未确定引擎、`GraphVersion` 为负、`Graph` 与 `GraphVersion` 不一致、图校验失败、`Policy` 越界或 `Debug.Injects` 参数类型/校验非法、输入无法序列化、`Input` 不符合 `Graph.InputSchema`（缺少必填项、含未声明键或类型不匹配） |
| 400 | `Unsupported` | 引擎已关闭，或 durable 处于恢复模式 |
| 404 | `NotFound` | 图不存在、已删除或从未保存；指定版本不存在；`Engine` 未注册 |
| 500 | `Internal` | 写入失败 |

## 示例

```bash
curl -s -X POST http://127.0.0.1:8080/api/tasks \
  -H 'Content-Type: application/json' \
  -d '{"GraphID":"demo_code","Engine":"durable","Input":{"a":3,"b":8,"name":"Ada"}}'
```

```json
{"TaskID": "01a10d48-f4f2-74ac-b3b5-b9f126a208a6", "EngineKind": "durable", "Status": "RUNNING", "Version": 1, "GraphVersion": 1}
```

执行临时图（不保存）：

```bash
curl -s -X POST http://127.0.0.1:8080/api/tasks -d '{
  "Engine": "immediate",
  "Graph": {
    "InputSchema": {"type": "object", "properties": {"x": {"type": "number"}}, "additionalProperties": false},
    "Nodes": [
      {"ID": "start", "Kind": "__INNER__.start"},
      {"ID": "finish", "Kind": "__INNER__.end", "Inputs": {"x": {"From": "$input", "Field": "x"}}}
    ],
    "Edges": [
      {"ID": "e1", "From": "start.Out", "To": "finish.In"}
    ]
  },
  "Input": {"x": 1}
}'
# {"TaskID":"01a10d4a-05f3-7262-8d37-ada16b1d1e92","EngineKind":"immediate","Status":"RUNNING","Version":1}
```

错误：

```json
{"Error": "graph runtime: invalid definition or input: json: unknown field \"graph_id\"", "Code": "Invalid"}
{"Error": "graph runtime: not found: engine \"nope\"", "Code": "NotFound"}
{"Error": "graph runtime: invalid definition or input: ExecutionMode \"turbo\"", "Code": "Invalid"}
{"Error": "graph runtime: invalid definition or input: run input: validating root: required: missing properties: [\"name\"]", "Code": "Invalid"}
{"Error": "graph runtime: invalid definition or input: run input: validating root: unexpected additional properties [\"acc\"]", "Code": "Invalid"}
```
