# graph/ui

`@golang-io/graph-ui`：图编辑与运行轨迹展示的 React 组件库，`private: true`，不发布 npm。

- 渲染器：React Flow（`@xyflow/react`）与 AntV X6，两者共用同一份 `GraphJSON` / `GraphUI` 文档，可在画布上切换。
- 文档结构与 Go 端 `graph.Graph`、`graph.UI` 一致；节点种类、端点和参数 Schema 来自后端 `GET /node-specs` 返回的 NodeSpec 注册表。
- HTTP 请求、鉴权、i18n 与业务操作由使用方通过 props 注入，本包不直接访问后端。
- 业务应用只从包入口（`src/index.ts`）导入，不直接导入 `@xyflow/react` / `@antv/x6`，也不深度引用本包内部文件。

## 接入

示例应用见 `cmd/web`，依赖写法：

```json
"@golang-io/graph-ui": "file:../../ui"
```

`cmd/web/.npmrc` 需保留 `install-links=false`，使依赖以符号链接方式安装。

## 开发

```bash
npm install
npm run typecheck   # tsc --noEmit
npm test            # vitest run
```
