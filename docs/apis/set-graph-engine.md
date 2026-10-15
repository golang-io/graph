# 设置图的默认执行引擎

修改图的默认执行引擎：[启动 Task](start-task.md) 未指定 `Engine` 时使用它。只修改最新一行，不追加版本。

- 路径：`/graphs/{id}/engine`
- Method：`PUT`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `GraphID` |

## 请求体

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `Engine` | string | 是 | 已注册的引擎名（见 [list-engines](list-engines.md)）；空字符串表示清除默认引擎。 |

## 响应

`200 OK`：更新后的 `Definition`。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | 请求体缺失、未知键、`Engine` 未注册 |
| 404 | `NotFound` | 图不存在或已删除 |
| 500 | `Internal` | 写入失败 |

## 示例

```bash
curl -s -X PUT http://127.0.0.1:8080/api/graphs/demo_code/engine \
  -H 'Content-Type: application/json' -d '{"Engine":"durable"}'
```

```json
{
  "GraphID": "demo_code",
  "Version": 1,
  "Name": "Demo · Code 表达式",
  "Graph": {"GraphID": "demo_code", "GraphVersion": 1, "Nodes": ["…"], "Edges": ["…"]},
  "Engine": "durable",
  "CreatedAt": "2026-10-05T18:16:22.651955Z",
  "UpdatedAt": "2026-10-05T18:17:55.822237Z"
}
```

```bash
curl -s -X PUT http://127.0.0.1:8080/api/graphs/demo_code/engine -d '{"Engine":"nope"}'
# {"Error":"graph runtime: invalid definition or input: engine \"nope\" is not registered","Code":"Invalid"}
```
