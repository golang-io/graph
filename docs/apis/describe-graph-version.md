# 查询图版本

按 `GraphID` 与版本号读取已保存的版本快照。图删除后仍可读取历史版本。

- 路径：`/graphs/{id}/versions/{version}`
- Method：`GET`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `GraphID` |
| `version` | 正整数版本号 |

## 响应

`200 OK`：该版本的 `Definition`。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | `version` 不是正整数（如 `0`、`abc`） |
| 404 | `NotFound` | 图或该版本不存在 |
| 500 | `Internal` | 读取失败 |

## 示例

```bash
curl -s http://127.0.0.1:8080/api/graphs/demo_code/versions/1
```

```json
{
  "GraphID": "demo_code",
  "Version": 1,
  "Name": "Demo · Code 表达式",
  "Graph": {"GraphID": "demo_code", "GraphVersion": 1, "Nodes": ["…"], "Edges": ["…"]},
  "UI": {
    "Positions": {"start": {"X": 40, "Y": 80}, "calc": {"X": 280, "Y": 80}, "finish": {"X": 520, "Y": 80}},
    "EndpointSides": {"start": {"Out": "right"}, "calc": {"In": "left", "Out": "right"}, "finish": {"In": "left"}}
  },
  "CreatedAt": "2026-10-05T18:16:22.651955Z",
  "UpdatedAt": "2026-10-05T18:17:16.222919Z"
}
```

```bash
curl -s http://127.0.0.1:8080/api/graphs/demo_code/versions/0
# {"Error":"graph runtime: invalid definition or input: version must be a positive integer","Code":"Invalid"}
```
