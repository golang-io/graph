# 查询图

返回图的最新一行（可能是 `Version` 为 0 的草稿）。

- 路径：`/graphs/{id}`
- Method：`GET`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `GraphID` |

## 响应

`200 OK`：`Definition`，见 [README](README.md#definition图定义)。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 404 | `NotFound` | 图不存在或已删除 |
| 500 | `Internal` | 读取失败 |

## 示例

```bash
curl -s http://127.0.0.1:8080/api/graphs/demo_code
```

```json
{
  "GraphID": "demo_code",
  "Version": 1,
  "Name": "Demo · Code 表达式",
  "Description": "验证 code 节点：a+b、比较、字符串拼接；后面的条目可以使用前面条目的 Key。试跑 {\"a\":3,\"b\":8,\"name\":\"Ada\"}。",
  "Graph": {
    "GraphID": "demo_code",
    "GraphVersion": 1,
    "InputSchema": {"type": "object", "required": ["a", "b", "name"], "properties": {"a": {"type": "number"}, "b": {"type": "number"}, "name": {"type": "string"}}, "additionalProperties": false},
    "Nodes": [
      {"ID": "start", "Kind": "__INNER__.start", "Name": "开始"},
      {"ID": "calc", "Kind": "code", "Name": "计算",
       "Inputs": {
         "a": {"From": "$input", "Field": "a"}, "b": {"From": "$input", "Field": "b"}, "name": {"From": "$input", "Field": "name"},
         "Outputs": {"Value": [{"Key": "total", "Value": "a + b"}, {"Key": "pass", "Value": "total >= 10"}, {"Key": "label", "Value": "name + \":\" + string(total)"}]}
       }},
      {"ID": "finish", "Kind": "__INNER__.end", "Name": "结束",
       "Inputs": {"label": {"From": "calc", "Field": "label"}, "pass": {"From": "calc", "Field": "pass"}, "total": {"From": "calc", "Field": "total"}}}
    ],
    "Edges": [
      {"ID": "e_start", "From": "start.Out", "To": "calc.In"},
      {"ID": "e2", "From": "calc.Out", "To": "finish.In"}
    ]
  },
  "UI": {
    "Positions": {"start": {"X": 40, "Y": 80}, "calc": {"X": 280, "Y": 80}, "finish": {"X": 520, "Y": 80}},
    "EndpointSides": {"start": {"Out": "right"}, "calc": {"In": "left", "Out": "right"}, "finish": {"In": "left"}}
  },
  "CreatedAt": "2026-10-05T18:16:22.651955Z",
  "UpdatedAt": "2026-10-05T18:16:22.651955Z"
}
```

```json
{"Error": "graph runtime: not found", "Code": "NotFound"}
```
