# 暂停 Task

请求暂停：正在执行的节点继续完成，各 Run 在进入下一个节点前停在调试等待（`WaitReason` 为 `debug`）。用 [resume-task](resume-task.md) 恢复。

- 路径：`/tasks/{id}/pause`
- Method：`POST`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `TaskID` |

## 请求

无请求体。

## 响应

`202 Accepted`：`Receipt`。Task 已结束时不报错，返回 `202` 与当前终态回执，不做任何修改。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Unsupported` | 引擎已关闭，或 durable 处于恢复模式 |
| 404 | `NotFound` | Task 不存在（immediate 执行中的 Task 只能在执行它的进程内找到） |
| 409 | `Conflict` | Task 未结束但已暂停 |
| 500 | `Internal` | 写入失败 |

## 示例

```bash
curl -s -X POST http://127.0.0.1:8080/api/tasks/01a10d49-42c4-7214-a41e-746a7dd2d297/pause
```

```json
{"TaskID": "01a10d49-42c4-7214-a41e-746a7dd2d297", "EngineKind": "durable", "Status": "RUNNING", "Version": 2, "GraphVersion": 1}
```

再次暂停：

```json
{"Error": "graph runtime: conflict: task is already paused", "Code": "Conflict"}
```
