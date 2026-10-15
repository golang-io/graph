# 订阅 Task 执行进度

以 Server-Sent Events 推送 Task 的变化。一条连接的第一个事件是完整快照，之后每个事件只含相对上一次推送的增量（Task / Runs）。服务端按引擎的轮询间隔合并多次变化后再推送。推送终态事件后结束响应。

- 路径：`/tasks/{id}/watch`
- Method：`GET`
- 响应类型：`text/event-stream`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `TaskID` |

## 响应

`200 OK`。每个 `message` 事件的 `data` 是一行 JSON，结构为 `WatchEvent`：

| 字段 | 说明 |
| --- | --- |
| `Snapshot` | 完整快照，字段与 [describe-task](describe-task.md) 相同，含 `Task.Graph`。出现时订阅方用它替换该 Task 的本地状态；省略时下面的字段是增量。 |
| `Task` | 当前任务行，不含 `Graph`，订阅方保留已有的 `Task.Graph`。任务行没有变化时省略（只续租约不算变化）。 |
| `Runs` | 本次状态有变化的 Run，按 `RunID` 替换或追加。只续了租约的 Run 不出现。 |
| `RemovedRunIDs` | 从上一次推送之后消失的 Run 的 `RunID`。 |

| 事件 | 内容 |
| --- | --- |
| 默认事件（`message`） | `data:` 为 `WatchEvent`。 |
| `event: error` | `data:` 为 `{"Error","Code"}`；订阅过程中出错时发送，随后结束响应。 |
| 注释行 `: ping` | 每 15 秒发送一次，保持连接。 |

客户端合并由 `ui` 包的 `applyWatchEvent` 完成。

已结束且已写入执行表的 Task 直接读表：只推送一个带 `Snapshot` 的终态完整快照，随后结束响应。执行中的 Task 订阅所属引擎，读取来源与 [describe-task](describe-task.md) 相同。

## 错误码

订阅建立前的错误以普通 JSON 响应返回：

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Unsupported` | 响应不支持流式输出 |
| 404 | `NotFound` | Task 不存在 |
| 500 | `Internal` | 读取失败 |

## 示例

```bash
curl -sN http://127.0.0.1:8080/api/tasks/01a10d48-f4f2-74ac-b3b5-b9f126a208a6/watch
```

```text
data: {"Snapshot":{"Task":{…,"Graph":{…}},"Runs":[…]}}

data: {"Task":{"TaskID":"…","Status":"SUCCEEDED"},"Runs":[…]}
```
