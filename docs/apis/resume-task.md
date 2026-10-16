# 继续 Task

放行停在调试等待上的 Run（`WaitReason` 为 `debug`：暂停、单步、断点），并清除暂停标记。也用于调试中的单步与继续，以及修改断点。

- 路径：`/tasks/{id}/resume`
- Method：`POST`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `TaskID` |

## 请求体

可省略（等同 `{}`）。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `DebugAction` | string | 否 | 空：清除暂停，放行所有调试等待中的 Run；`step`：开启单步并放行一个调试等待中的 Run；`continue`：关闭单步并放行所有调试等待中的 Run。 |
| `Debug` | object | 否 | 非空时替换 Task 的调试选项（`Step`、`Breakpoints`、`Injects`，见 [start-task](start-task.md#taskpolicy)）。传 `{}` 可清除全部断点与单步。 |
| `TargetRunID` | string | 否 | 只放行指定的 Run。 |

## 响应

`202 Accepted`：`Receipt`，`Status` 为放行后 Task 的状态。Task 已结束（`SUCCEEDED` / `FAILED` / `CANCELED` / `SKIPPED`）时不报错，返回 `202` 与当前终态回执，不做任何修改。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | 未知键（含已删除的 `Input`）、`DebugAction` 取值非法、`Debug.Injects` 无法规整或不符合节点参数类型与校验规则 |
| 400 | `Unsupported` | 引擎已关闭，或 durable 处于恢复模式 |
| 404 | `NotFound` | Task 不存在（immediate 执行中的 Task 只能在执行它的进程内找到） |
| 409 | `Conflict` | Task 未结束，但没有调试等待中的 Run 且未暂停（`DebugAction` 为空且提供了 `Debug` 时除外，此时只替换调试选项）；目标 Run 已不在等待 |
| 500 | `Internal` | 写入失败 |

## 示例

以断点启动 `demo_code`，Run 停在 `calc` 节点前（`WaitReason` 为 `debug`），Task 为 `WAITING`：

```bash
curl -s -X POST http://127.0.0.1:8080/api/tasks \
  -d '{"GraphID":"demo_code","Engine":"durable","Input":{"a":3,"b":8,"name":"Ada"},"Policy":{"Debug":{"Breakpoints":["calc"]}}}'
```

放行：

```bash
curl -s -X POST http://127.0.0.1:8080/api/tasks/01a10d49-42c4-7214-a41e-746a7dd2d297/resume
```

```json
{"TaskID": "01a10d49-42c4-7214-a41e-746a7dd2d297", "EngineKind": "durable", "Status": "RUNNING", "Version": 3, "GraphVersion": 1}
```

Task 随后到达 end，`Output` 中 `label` 为 `"Ada:11"`。再次 resume 返回 `202` 与当前终态：

```json
{"TaskID": "01a10d49-42c4-7214-a41e-746a7dd2d297", "EngineKind": "durable", "Status": "SUCCEEDED", "Version": 5, "GraphVersion": 1}
```
