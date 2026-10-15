# 列出已注册节点种类

返回已注册的节点类型说明，供前端生成节点库、属性面板与端点句柄。内置控制节点的 `ID` 以 `__INNER__.` 开头。

默认注册表只含控制节点与 `template`、`code`、`reduce`；`http`、`llm` 只有在业务应用显式注册（`builtin.HTTPNode{}`、`builtin.LLMNode{Invoke: …}`）后才出现在列表中。下方示例为演示程序注册了 `http` 与 `llm` 后的结果。

- 路径：`/node-specs`
- Method：`GET`

## 请求

无请求体、无查询参数。

## 响应

`200 OK`：`{"Data": NodeSpec[]}`。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `ID` | string | 节点种类，即 `Graph.Nodes[].Kind`（对接可执行 Node）。 |
| `Name` | string | 名称。 |
| `Description` | string | 种类说明（可省略）；节点库副标题等。 |
| `Group` | string | 节点库分组键（可省略，缺省 `general`），如 `control` / `transform`。 |
| `GroupLabel` | string | 分组显示名（可省略）；缺省由后端按 `Group` 映射。 |
| `DefaultInputs` | object | 新建节点默认 `Inputs`（可省略）：键为参数名，值为 `InputRef`（多为字面量 `Value`）。 |
| `Icon` | string | 画布图标键（可省略）。 |
| `Tone` | string | 画布色调（可省略），如 `cyan` / `violet`。 |
| `Renderer` | string | 几何角色扩展点名（可省略）；未知或缺省时前端回落通用节点。每个端点句柄所在的边由节点实例在定义的 `UI.EndpointSides` 中逐个写明（图布局，与本字段无关）。 |
| `Endpoints` | object | `Inputs`、`Outputs`：种类级固定端点，同一种类的所有实例相同。图不允许成环；merge 的汇合方式由参数 `Mode` 决定，不在端点上声明。 |
| `InputSchema` | object | 参数与数据槽的 JSON Schema（可省略；由参数结构体自动生成）。`properties` 描述各输入名；属性有 `title` 的进属性表单（控件由前端按名/`enum` 推测），无 `title` 的作输入绑定建议槽（结构体 `bind` 标签）；`required` 表示实例 `Inputs` 必须出现该键；`enum` 为下拉候选项；`additionalProperties` 允许自定义输入名。全部写入实例 `Node.Inputs`（字面量或引用均可）。 |
| `OutputsByEndpoint` | object | 各控制流出端点的载荷 JSON Schema（可省略；仅标准关键字）。键为本地出端点名；空串 `""` 表示所有正常出口（不含 `Error`）共享的载荷。每份 Schema 的 `type` 为 `object`，`properties` 的键为输出名。省略时输出由参数决定：template / code 为 `Outputs[].Key`，merge 为 `Outputs[].Name`（均写入默认键）。声明了 `Error` 出端点的节点另有出口 `Error` 的载荷 `{"Error":…}`（只在错误出口之后可用），不列在此处。同一字段不得出现在多个出端点中。 |
| `Presets` | array | 预设：`ID`、`Label`、`Inputs`（可省略）。 |

## 错误码

无业务错误码。

## 示例

```bash
curl -s http://127.0.0.1:8080/api/node-specs
```

```json
{
  "Data": [
    {
      "ID": "__INNER__.map",
      "Name": "遍历",
      "DefaultInputs": {
        "Chunk": {"Value": 1},
        "MaxParallel": {"Value": 1},
        "Body": {"Value": {
          "InputSchema": {"type": "object", "properties": {"Item": {"description": "本轮元素"}, "Index": {"type": "integer", "description": "本轮第一个元素在 List 中的下标"}}},
          "Nodes": [{"ID": "start", "Kind": "__INNER__.start", "Name": "开始"}, {"ID": "end", "Kind": "__INNER__.end", "Name": "结束"}],
          "Edges": [{"ID": "e1", "From": "start.Out", "To": "end.In"}]
        }}
      },
      "Description": "对输入 List 逐项运行循环体（内嵌子图），循环体输入 Item/Index，完成后输出 Results",
      "Group": "control",
      "GroupLabel": "控制",
      "Icon": "repeat",
      "Tone": "violet",
      "Renderer": "map",
      "Endpoints": {"Inputs": ["In"], "Outputs": ["Out"]},
      "InputSchema": {
        "type": "object",
        "required": ["List"],
        "additionalProperties": true,
        "properties": {
          "List": {},
          "Chunk": {"title": "每轮条数", "type": "integer"},
          "MaxParallel": {"title": "并发数（1=串行）", "type": "integer"},
          "Body": {"title": "循环体", "type": ["null", "object"], "description": "内嵌子图（graph.Graph）"}
        }
      },
      "OutputsByEndpoint": {
        "": {
          "type": "object",
          "properties": {
            "Results": {}
          }
        }
      }
    }
  ]
}
```
