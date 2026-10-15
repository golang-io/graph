# 列出图版本

返回图已保存的版本（`Version > 0`），按版本号倒序。草稿（Version 0）不在其中；图删除后历史版本仍可列出。

- 路径：`/graphs/{id}/versions`
- Method：`GET`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `GraphID` |

## 查询参数

| 参数 | 类型 | 默认 | 说明 |
| --- | --- | --- | --- |
| `Limit` | int | 0 | 最多返回条数；0 表示不限。 |

## 响应

`200 OK`：`{"Data": Definition[]}`。图不存在时 `Data` 为空数组。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | `Limit` 不是非负整数 |
| 500 | `Internal` | 读取失败 |

## 示例

```bash
curl -s 'http://127.0.0.1:8080/api/graphs/demo_code/versions?Limit=10'
```

```json
{
  "Data": [
    {
      "GraphID": "demo_code",
      "Version": 1,
      "Name": "Demo · Code 表达式",
      "Graph": {"GraphID": "demo_code", "GraphVersion": 1, "Nodes": ["…"], "Edges": ["…"]},
      "CreatedAt": "2026-10-05T18:16:22.651955Z",
      "UpdatedAt": "2026-10-05T18:17:16.222919Z"
    }
  ]
}
```
