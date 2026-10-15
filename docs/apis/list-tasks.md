# 列出 Task

汇总所有已注册引擎的根 Task（不含子图 Task），按 `StartedAt` 倒序（相同时按 `TaskID` 倒序）。

- 路径：`/tasks`
- Method：`GET`

## 查询参数

| 参数 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `GraphID` | string | | 只返回该图的 Task。 |
| `Status` | string | | 只返回该状态的 Task；大小写不敏感（`succeeded` 等同 `SUCCEEDED`）。 |
| `Limit` | int | 50 | 最多返回条数；不大于 0 时取 50。 |
| `After` | string | | 分页游标：上一页最后一条的 `TaskID`，只返回排在它之后（更早）的 Task。 |

## 响应

`200 OK`：`{"Data": TaskSummary[]}`。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `TaskID` / `TicketID` / `EngineKind` | string | 标识与执行引擎。 |
| `GraphID` / `GraphVersion` | | 执行所用的图与图定义版本号（临时图为 0）。 |
| `Status` / `Error` | string | 状态与错误。 |
| `ParentTaskID` | string | 子图 Task 的父 Task（可省略）。 |
| `StartedAt` / `FinishedAt` | time | 时间。 |

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | `Limit` 不是非负整数 |
| 404 | `NotFound` | `After` 指定的 Task 不存在 |
| 500 | `Internal` | 读取失败 |

## 示例

```bash
curl -s 'http://127.0.0.1:8080/api/tasks?GraphID=demo_code&Status=SUCCEEDED&Limit=1'
```

```json
{
  "Data": [
    {
      "TaskID": "01a10d48-f4f2-74ac-b3b5-b9f126a208a6",
      "TicketID": "01a10d48-f4f2-74ac-b3b5-b9f126a208a6",
      "EngineKind": "durable",
      "GraphID": "demo_code",
      "GraphVersion": 1,
      "Status": "SUCCEEDED",
      "StartedAt": "2026-10-05T18:17:27.538284Z",
      "FinishedAt": "2026-10-05T18:17:27.556236Z"
    }
  ]
}
```

下一页：`GET /tasks?GraphID=demo_code&Limit=1&After=01a10d48-f4f2-74ac-b3b5-b9f126a208a6`。
