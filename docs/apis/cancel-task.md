# 取消 Task

把未结束的 Task 置为 `CANCELED`：未结束的 Run 置为 `CANCELED`，等待中的子图 Task 级联取消；正在执行的节点在续租或提交时发现取消后停止，其结果不再生效。

- 路径：`/tasks/{id}/cancel`
- Method：`POST`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `TaskID` |

## 请求体

可省略。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `Reason` | string | 否 | 取消原因，写入 `Task.Error`；省略时为 `canceled`。 |

## 响应

`202 Accepted`：`Receipt`，`Status` 为取消后的状态（`CANCELED`）。Task 已结束（含重复取消）时不报错，返回 `202` 与当前终态回执，不做任何修改。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | 未知键 |
| 400 | `Unsupported` | 引擎已关闭，或 durable 处于恢复模式 |
| 404 | `NotFound` | Task 不存在（immediate 执行中的 Task 只能在执行它的进程内找到） |
| 500 | `Internal` | 写入失败 |

## 示例

```bash
curl -s -X POST http://127.0.0.1:8080/api/tasks/01a10d49-469b-7391-ba21-e921c68ffc95/cancel \
  -H 'Content-Type: application/json' -d '{"Reason":"user canceled"}'
```

```json
{"TaskID": "01a10d49-469b-7391-ba21-e921c68ffc95", "EngineKind": "immediate", "Status": "CANCELED", "Version": 2}
```

之后 Task 的 `Status` 为 `CANCELED`，`Error` 为 `user canceled`。对已结束的 Task 取消，返回 `202` 与当前终态：

```json
{"TaskID": "01a10d48-f4f2-74ac-b3b5-b9f126a208a6", "EngineKind": "durable", "Status": "SUCCEEDED", "Version": 2, "GraphVersion": 1}
```
