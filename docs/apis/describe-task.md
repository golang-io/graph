# 查询 Task 快照

返回 Task 的最新快照：Task（含执行所用的图 `Task.Graph`）与全部 Run。

- 路径：`/tasks/{id}`
- Method：`GET`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `TaskID` |

## 响应

`200 OK`：`TaskSnapshot`，字段见 [README](README.md#tasksnapshottask-快照)。

读取来源按 Task 是否结束区分：

- 已结束且已写入执行表（`tb_graph_task` 中为终态）的 Task：直接读表，不经过引擎。immediate 与 durable 结束后的记录格式相同；产生该 Task 的引擎在当前进程未注册时也能查询。
- 执行中的 Task：转发给所属引擎。durable 读取数据库；immediate 读取执行进程的内存状态（运行期间不写库，执行中的 Task 在其他进程查询返回 404）。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 404 | `NotFound` | Task 不存在 |
| 500 | `Corrupt` | 存储中的执行数据损坏 |
| 500 | `Internal` | 读取失败 |

## 示例

```bash
curl -s http://127.0.0.1:8080/api/tasks/01a10d48-f4f2-74ac-b3b5-b9f126a208a6
```

```json
{
  "Task": {
    "TaskID": "01a10d48-f4f2-74ac-b3b5-b9f126a208a6",
    "TicketID": "01a10d48-f4f2-74ac-b3b5-b9f126a208a6",
    "EngineKind": "durable",
    "GraphID": "demo_code",
    "GraphVersion": 1,
    "Version": 4,
    "Status": "SUCCEEDED",
    "Policy": {"FailPolicy": "fail_fast", "ExecutionMode": "serial", "MaxParallel": 8, "MaxAttempts": 1},
    "Input": {"a": 3, "b": 8, "name": "Ada"},
    "Output": {"label": "Ada:11", "pass": true, "total": 11},
    "Paused": false,
    "Graph": {"GraphID": "demo_code", "GraphVersion": 1, "Nodes": ["…"], "Edges": ["…"]},
    "StartedAt": "2026-10-05T18:17:27.538284Z",
    "FinishedAt": "2026-10-05T18:17:27.556236Z",
    "CreatedAt": "2026-10-05T18:17:27.538284Z"
  },
  "Runs": [
    {
      "RunID": "01a10d48-f4f2-74ac-b3b5-b9f126a208a7",
      "TaskID": "01a10d48-f4f2-74ac-b3b5-b9f126a208a6",
      "EngineKind": "durable",
      "NodeID": "finish",
      "FromEdgeID": "e2",
      "Seq": 3,
      "Outputs": {
        "calc": {"label": "Ada:11", "pass": true, "total": 11}
      },
      "Output": {"label": "Ada:11", "pass": true, "total": 11},
      "Status": "SUCCEEDED",
      "Attempt": 1,
      "StartedAt": "2026-10-05T18:17:27.550000Z",
      "CreatedAt": "2026-10-05T18:17:27.538284Z"
    }
  ]
}
```
