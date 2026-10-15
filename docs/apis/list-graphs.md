# 列出图

返回每个图的最新一行（不含已删除的图），按 `UpdatedAt` 倒序。包含 `Version` 为 0 的草稿。

- 路径：`/graphs`
- Method：`GET`

## 请求

无请求体、无查询参数。

## 响应

`200 OK`：`{"Data": Definition[]}`，`Definition` 见 [README](README.md#definition图定义)。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 500 | `Internal` | 读取定义库失败 |

## 示例

```bash
curl -s http://127.0.0.1:8080/api/graphs
```

```json
{
  "Data": [
    {
      "GraphID": "demo_code",
      "Version": 1,
      "Name": "Demo · Code 表达式",
      "Graph": {"GraphID": "demo_code", "GraphVersion": 1, "InputSchema": {"type": "object", "required": ["a", "b", "name"], "properties": {"a": {}, "b": {}, "name": {}}, "additionalProperties": false}, "Nodes": [{"ID": "start", "Kind": "__INNER__.start", "Name": "开始"}, "…"], "Edges": ["…"]},
      "UI": {"Positions": {"start": {"X": 40, "Y": 80}, "calc": {"X": 280, "Y": 80}, "finish": {"X": 520, "Y": 80}}, "EndpointSides": {"start": {"Out": "right"}, "calc": {"In": "left", "Out": "right"}, "finish": {"In": "left"}}},
      "CreatedAt": "2026-10-05T18:16:22.651955Z",
      "UpdatedAt": "2026-10-05T18:16:22.651955Z"
    }
  ]
}
```
