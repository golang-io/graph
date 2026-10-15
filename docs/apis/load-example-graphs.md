# 写入示例图

把内置示例图（`example/*.json`）保存到定义库。按 [保存图](save-graph.md) 的规则写入：内容不变时不追加版本，因此可重复调用。

- 路径：`/graphs/examples`
- Method：`POST`

## 请求

无请求体。

## 响应

`200 OK`：`{"Data": string[]}`，`Data` 为示例图的 `GraphID`。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | 示例图未通过当前节点注册表的校验（如缺少示例依赖的节点类型：`demo_all_nodes` 需要业务应用显式注册 `http` 与 `llm` 节点） |
| 500 | `Internal` | 读取内置示例或写入失败 |

## 示例

```bash
curl -s -X POST http://127.0.0.1:8080/api/graphs/examples
```

```json
{
  "Data": [
    "demo_all_nodes", "demo_child_echo", "demo_code", "demo_implicit_fork",
    "demo_loop_merge", "demo_map_reduce", "demo_p1_control", "demo_perf_1000", "demo_tour"
  ]
}
```
