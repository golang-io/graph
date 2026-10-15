# 删除图

把图的最新一行标记为删除。删除后查询、列表、运行都视为不存在，再次保存返回 `Conflict`；历史版本快照与已有 Task 保留，[版本详情](describe-graph-version.md) 仍可读取。

- 路径：`/graphs/{id}`
- Method：`DELETE`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `GraphID` |

## 响应

`204 No Content`，无响应体。对已删除的图再次删除同样返回 204。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 404 | `NotFound` | 图不存在 |
| 500 | `Internal` | 写入失败 |

## 示例

```bash
curl -s -o /dev/null -w '%{http_code}\n' -X DELETE http://127.0.0.1:8080/api/graphs/doc_demo
# 204
curl -s http://127.0.0.1:8080/api/graphs/doc_demo
# {"Error":"graph runtime: not found","Code":"NotFound"}
```
