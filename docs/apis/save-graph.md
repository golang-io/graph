# 保存图

保存图的执行图与布局。服务端把本次的执行图 + 布局与最新已保存版本实时比较（不保存内容哈希）：有变化时追加新版本（`Version + 1`）；内容不变时只更新名称、描述、默认引擎、标签，不追加版本。`GraphID` 不存在、或当前只有草稿（`Version` 为 0）时追加版本 1。

- 路径：`/graphs/{id}`
- Method：`PUT`

## 路径参数

| 参数 | 说明 |
| --- | --- |
| `id` | `GraphID`；以路径为准，请求体中的 `GraphID` 被忽略。 |

## 请求体

请求体按 `Definition` 解码，可写字段如下；`Version`、`CreatedAt`、`UpdatedAt` 由服务端生成，传入也会被忽略。

| 字段 | 类型 | 必填 | 说明 |
| --- | --- | --- | --- |
| `Graph` | Graph | 是 | 执行图，按节点注册表校验（节点类型已注册、配置符合 Schema、边引用存在的节点、唯一入口），并校验 `Graph.InputSchema` 与各节点 `Inputs` 的引用（见 [README](README.md#graph-的输入与输出)）。`Graph.GraphID` / `Graph.GraphVersion` 可省略，传入也会被忽略：服务端写入 `(GraphID, 新版本号)`；判断内容是否变化时不比较请求中的这两个值。 |
| `UI` | object | 是 | 布局：主图层 `graph.Layout`（JSON 中与 `UI` 同层展开：`Positions`、`EndpointSides`、`EdgeRails`、`SubLayouts`）、`ExecutionMode`、`StickyNotes`（`{ID,X,Y,Width,Height,Title,Note,Color,Transparent}`）。图没有整体方向，已删除的 `Direction` 键按未知键处理。`Positions`（节点 ID → `{X,Y}`）；`EndpointSides`（节点 ID → `{端点名: 边}`，端点名是该节点实例的本地端点名（如 `In`、`Out`、switch 的 `Case0`…），边取 `left` / `right` / `top` / `bottom`；**每个节点都必须有一项，且实例的每个端点都必须写明所在边**：入口取 `Endpoints.Inputs`，出口取当前参数下启用的出口（switch 为 `Cases` 中的分支，未配置 `Cases` 时为 `Case0`、`Case1`；其他种类为 `Endpoints.Outputs`），包括 `SubLayouts` 各层里内嵌子图（如 map 的 `Body`）的节点；任何出口都不能与该节点的任一入口同边；不属于实例端点的条目（如已删除的分支）保存时静默清除，不报错；新建的普通节点通常写 `{"In":"left","Out":"right"}`，`__INNER__.start` 写 `{"Out":"right"}`，`__INNER__.end` 与 `__INNER__.stop_error` 写 `{"In":"left"}`）；`EdgeRails`（边 ID → `top` / `bottom` / `left` / `right`，与 `EndpointSides` 取值相同，表示回边绕行一侧；`top` / `bottom` 用于出口在左右边的图，`left` / `right` 用于出口在上下边的纵向图；旧取值 `above` / `below` 已不再接受）；`SubLayouts`（拥有内嵌子图的节点 ID → 子图层布局，值类型与主图层共用 `Layout`：`{Collapsed,Positions,EndpointSides,EdgeRails,SubLayouts}`；`Positions` 相对容器左上角，键为子图内本地节点 ID；`Collapsed` 仅对子图层有效，为 `true` 时画布把容器收起成普通节点；主图层上的 `Collapsed` 无意义，规范化时清除；`SubLayouts` 可再嵌套，最多 16 层）。出现其他键或取值非法返回 `400 Invalid`。`UI` 参与「内容是否变化」的比较，但不影响执行。 |
| `Name` | string | 否 | 非空时更新名称；新建图且未提供时名称为 `GraphID`。 |
| `Description` | string | 否 | 非空时更新描述。 |
| `Engine` | string | 否 | 非空时更新默认执行引擎（不校验是否已注册；需要校验时用 [set-graph-engine](set-graph-engine.md)）。 |
| `Label` | string | 否 | 版本标签：追加新版本时按本次传入值写入（省略则新版本标签为空，不沿用旧版本）；图内容与布局未变化（仅更新元数据）时，非空才更新当前版本的标签。 |

## 响应

`200 OK`：保存后的 `Definition`。

## 错误码

| HTTP | Code | 场景 |
| --- | --- | --- |
| 400 | `Invalid` | 请求体缺失、未知键（含 `UI` 内的键，如已删除的 `Direction`、改名前的 `Ports`）、`UI` 取值非法（如边的取值不是 `left` / `right` / `top` / `bottom`）、有节点缺少 `UI.EndpointSides` 项或缺少某个端点的所在边、有出口与入口同边（均含 `SubLayouts` 内的子图节点）、图校验失败（如节点类型未注册、图为空、有多个入口节点、输入引用的节点或字段不存在、引用的节点在控制流上不可达、表达式使用了未声明的名字） |
| 409 | `Conflict` | 图已删除 |
| 500 | `Internal` | 写入失败 |

## 示例

```bash
curl -s -X PUT http://127.0.0.1:8080/api/graphs/doc_demo \
  -H 'Content-Type: application/json' \
  -d '{
    "Name": "文档示例图",
    "Graph": {
      "InputSchema": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}}, "additionalProperties": false},
      "Nodes": [
        {"ID": "start", "Kind": "__INNER__.start", "Name": "开始"},
        {"ID": "hello", "Kind": "template", "Name": "问候", "Inputs": {
           "name": {"From": "$input", "Field": "name"},
           "Outputs": {"Value": [{"Key": "text", "Value": "hi {{name}}"}]}
         }},
        {"ID": "finish", "Kind": "__INNER__.end", "Name": "结束", "Inputs": {"text": {"From": "hello", "Field": "text"}}}
      ],
      "Edges": [
        {"ID": "e1", "From": "start.Out", "To": "hello.In"},
        {"ID": "e2", "From": "hello.Out", "To": "finish.In"}
      ]
    },
    "UI": {
      "Positions": {"start": {"X": 20, "Y": 80}, "hello": {"X": 160, "Y": 80}, "finish": {"X": 320, "Y": 80}},
      "EndpointSides": {"start": {"Out": "right"}, "hello": {"In": "left", "Out": "right"}, "finish": {"In": "left"}}
    }
  }'
```

```json
{
  "GraphID": "doc_demo",
  "Version": 1,
  "Name": "文档示例图",
  "Graph": {
    "GraphID": "doc_demo",
    "GraphVersion": 1,
    "InputSchema": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}}, "additionalProperties": false},
    "Nodes": ["…"],
    "Edges": [
      {"ID": "e1", "From": "start.Out", "To": "hello.In"},
      {"ID": "e2", "From": "hello.Out", "To": "finish.In"}
    ]
  },
  "UI": {
    "Positions": {"start": {"X": 20, "Y": 80}, "hello": {"X": 160, "Y": 80}, "finish": {"X": 320, "Y": 80}},
    "EndpointSides": {"start": {"Out": "right"}, "hello": {"In": "left", "Out": "right"}, "finish": {"In": "left"}},
    "EdgeRails": {},
    "StickyNotes": []
  },
  "CreatedAt": "2026-10-05T18:18:30.120000Z",
  "UpdatedAt": "2026-10-05T18:18:30.120000Z"
}
```

只改名称再次保存，`Version` 仍为 1。图校验失败：

```json
{"Error": "graph runtime: invalid definition or input: graph: node \"x\" kind \"nope\" is not registered", "Code": "Invalid"}
{"Error": "graph runtime: invalid definition or input: graph: node finish endpoint sides required", "Code": "Invalid"}
{"Error": "graph runtime: invalid definition or input: graph: node hello endpoint sides: endpoint Out side required", "Code": "Invalid"}
{"Error": "graph runtime: invalid definition or input: graph: node hello endpoint sides: output Out and an input are both \"left\"", "Code": "Invalid"}
{"Error": "graph runtime: invalid definition or input: graph: node \"finish\" input \"text\": node \"hello\" has no output \"txt\"", "Code": "Invalid"}
{"Error": "graph runtime: invalid definition or input: graph: node \"hello\" Outputs[0].Value: expr: unknown name nmae (1:1)", "Code": "Invalid"}
```
