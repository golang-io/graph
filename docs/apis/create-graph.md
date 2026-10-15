# 新建图

新建一个空图草稿：`GraphID` 由服务端生成（UUID），`Version` 为 0。草稿不能直接运行，首次 [保存](save-graph.md) 后成为版本 1。

- 路径：`/graphs`
- Method：`POST`

## 请求体

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `Name` | string | 是 | 图名称，去除首尾空白后不能为空。 |
| `Description` | string | 否 | 描述。 |

## 响应

`201 Created`：新建的 `Definition`（`Graph` 为 `{"GraphID": 新 GraphID, "Nodes": [], "Edges": []}`，`GraphVersion` 为 0 故省略，没有 `Inputs`）。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | 请求体缺失、未知键、`Name` 为空 |
| 500 | `Internal` | 写入失败 |

## 示例

```bash
curl -s -X POST http://127.0.0.1:8080/api/graphs \
  -H 'Content-Type: application/json' \
  -d '{"Name":"文档示例","Description":"docs"}'
```

```json
{
  "GraphID": "eec1cfb6-c159-4d5e-a591-87ae8307c689",
  "Version": 0,
  "Name": "文档示例",
  "Description": "docs",
  "Graph": {"GraphID": "eec1cfb6-c159-4d5e-a591-87ae8307c689", "Nodes": [], "Edges": []},
  "CreatedAt": "2026-10-05T18:17:55.782834Z",
  "UpdatedAt": "2026-10-05T18:17:55.782834Z"
}
```
