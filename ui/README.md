# @golang-io/graph-ui 接入与开发指南

`@golang-io/graph-ui` 是 `graph` 项目的现代化可视化图编辑与运行轨迹追踪 React 组件库。采用 **React Flow (`@xyflow/react`) + AntV X6 (`@antv/x6`)** 双渲染引擎架构，业务文档结构与 Go 后端（`graph.Graph` / `graph.UI`）完全对齐。

本文档面向 **开发者（人类）** 与 **AI 编码助手（AI Agents）**，提供标准化的跨工程接入规范、配置要点与核心 API 范例。

---

## 目录

- [一、核心架构与设计原则](#一核心架构与设计原则)
- [二、跨工程引入（基于 Vite 的极简方案）](#二跨工程引入基于-vite-的极简方案)
  - [1. 目录结构推荐](#1-目录结构推荐)
  - [2. 配置 package.json](#2-配置-packagejson)
  - [3. 配置 vite.config.ts（关键避坑）](#3-配置-viteconfigts关键避坑)
  - [4. 配置 .npmrc 与 tsconfig.json](#4-配置-npmrc-与-tsconfigjson)
  - [5. 样式与主题配置 (CSS & Tailwind)](#5-样式与主题配置-css--tailwind)
- [三、最小可用示例 (Minimal Working Example)](#三最小可用示例-minimal-working-example)
- [四、核心 API 与组件速查表](#四核心-api-与组件速查表)
- [五、与 Go 后端接口协同机制](#五与-go-后端接口协同机制)
- [六、常见问题与排查 (Troubleshooting)](#六常见问题与排查-troubleshooting)

---

## 一、核心架构与设计原则

1. **零构建门槛 (No Prebuild Required)**：
   本包直接输出经过严密类型约束的 TypeScript/TSX 源码（`src/index.ts`），利用接入方工程的 **Vite** 进行按需实时转译与 HMR 热更新，无需等待 `dist` 预构建。
2. **纯前端表现层 (Decoupled & Headless Network)**：
   本组件库**不内置任何写死的 HTTP 请求逻辑**。图定义保存、节点列表加载、Task 控制与调试均由接入方通过标准 props 或 hooks 传入。
3. **单向文档流与后端强契约**：
   画布编辑产出的 JSON 数据格式与 Go 端 `graph.Graph` 以及 `docs/apis/` 中的接口定义保持 100% 一致。
4. **安全隔离原则**：
   外部使用方仅需从包根入口导入（`import { ... } from '@golang-io/graph-ui'`），禁止直接导入 `@xyflow/react` 或 `@antv/x6` 底层对象。

---

## 二、跨工程引入（基于 Vite 的极简方案）

外部前端应用若要复用本画布能力，无需发布至 npm，可直接通过 `file:` 相对路径或 Git Submodule 形式引入。

### 1. 目录结构推荐

以本地同级目录或父子模块为例：
```text
workspace/
├── golang-io-graph/          # 当前仓库 (内含 ui/)
└── your-web-app/             # 您的外部前端业务工程 (基于 Vite)
    ├── package.json
    ├── vite.config.ts
    ├── tsconfig.json
    ├── .npmrc
    └── src/
```

### 2. 配置 package.json

在外部业务工程的 `package.json` 中声明依赖：

```json
{
  "name": "your-web-app",
  "dependencies": {
    "@golang-io/graph-ui": "file:../golang-io-graph/ui",
    "react": "^19.3.0",
    "react-dom": "^19.3.0",
    "@xyflow/react": "^12.11.0",
    "@antv/x6": "^3.0.0",
    "@dagrejs/dagre": "^3.0.0",
    "lucide-react": ">=0.500.0",
    "clsx": "^2.1.0",
    "tailwind-merge": "^3.0.0",
    "tslib": "^2.8.1"
  },
  "devDependencies": {
    "@vitejs/plugin-react": "^5.0.0",
    "tailwindcss": "^4.0.0",
    "typescript": "^5.9.0",
    "vite": "^7.0.0"
  }
}
```

### 3. 配置 vite.config.ts（关键避坑）

跨本地目录引用包含 React 组件的代码时，必须在 `vite.config.ts` 中声明 **`resolve.dedupe`**，防止因多份 React 实例引发 `"Invalid hook call"` 运行时错误：

```ts
import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  resolve: {
    // 强制全局复用业务工程的单例依赖，防止与 ui/node_modules 冲突
    dedupe: ['react', 'react-dom']
  }
});
```

### 4. 配置 .npmrc 与 tsconfig.json

在业务工程根目录创建/更新 `.npmrc`，保留符号链接机制：
```ini
install-links=false
```

业务工程的 `tsconfig.json` 需开启现代打包模式：
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "lib": ["ES2022", "DOM", "DOM.Iterable"],
    "module": "ESNext",
    "moduleResolution": "bundler",
    "jsx": "react-jsx",
    "strict": true,
    "skipLibCheck": true
  }
}
```

### 5. 样式与主题配置 (CSS & Tailwind)

若使用 **Tailwind CSS v4**，因本地 `file:` 依赖位于 `node_modules` 之外，需在业务入口 CSS（如 `src/index.css`）显式添加扫描范围与基础 HSL 颜色变量：

```css
@import "tailwindcss";

/* 显式声明扫描范围，确保编译 ui/src 内的原子样式 */
@source "./";
@source "../golang-io-graph/ui/src";

/* 暗色主题类名对齐 */
@custom-variant dark (&:where(.dark, .dark *));

/* 基础 HSL 色彩 Token (与 Shadcn 规范对齐) */
:root {
  --background: 0 0% 100%;
  --foreground: 240 10% 4%;
  --card: 0 0% 100%;
  --card-foreground: 240 10% 4%;
  --primary: 243 75% 59%;
  --primary-foreground: 0 0% 100%;
  --muted: 240 5% 96%;
  --muted-foreground: 240 4% 46%;
  --border: 240 6% 90%;
  --input: 240 6% 90%;
  --ring: 239 83% 67%;
}

.dark {
  --background: 240 10% 4%;
  --foreground: 0 0% 98%;
  --card: 240 6% 10%;
  --card-foreground: 0 0% 98%;
  --primary: 239 84% 67%;
  --primary-foreground: 0 0% 100%;
  --muted: 240 4% 16%;
  --muted-foreground: 240 5% 65%;
  --border: 240 6% 26%;
  --input: 240 6% 26%;
}
```

---

## 三、最小可用示例 (Minimal Working Example)

以下为集成图编辑与画布的完整 React 组件示例：

```tsx
import React, { useEffect, useMemo, useState } from 'react';
import {
  GraphWorkbench,
  GraphPalette,
  GraphNodePropsPanel,
  GraphEditToolbar,
  useGraphDocument,
  useGraphNodeCatalog,
  createGraphNodePackFromCatalog,
  graphDecorationNodePack,
  type GraphDefinition,
  type GraphNodePack,
} from '@golang-io/graph-ui';

export function WorkflowEditorPage({ graphId }: { graphId: string }) {
  const [doc, setDoc] = useState<GraphDefinition | null>(null);

  // 1. 从后端加载节点规格注册表 (对应 GET /api/node-specs)
  const { specs, loading: specsLoading } = useGraphNodeCatalog(async () => {
    const res = await fetch('/api/node-specs');
    return res.json();
  });

  // 2. 将规格注册表解析为组件库可用的物料包
  const nodePack = useMemo<GraphNodePack>(
    () => (specs.length ? createGraphNodePackFromCatalog('main', specs) : graphDecorationNodePack),
    [specs]
  );

  // 3. 从后端拉取图定义 (对应 GET /api/graphs/{id})
  useEffect(() => {
    fetch(`/api/graphs/${graphId}`)
      .then((res) => res.json())
      .then((data) => setDoc(data));
  }, [graphId]);

  // 4. 文档状态管理（提供历史回退、节点移动、连线管理能力）
  const editor = useGraphDocument(doc ?? { GraphID: graphId, Version: 0, Graph: { Nodes: [], Edges: [] } });

  if (specsLoading || !doc) {
    return <div className="p-4 text-sm text-muted-foreground">正在加载画布与节点物料…</div>;
  }

  // 5. 保存图操作 (对应 PUT /api/graphs/{id})
  const handleSave = async () => {
    const payload = editor.savePayload();
    await fetch(`/api/graphs/${graphId}`, {
      method: 'PUT',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(payload),
    });
    alert('保存成功！');
  };

  return (
    <div className="flex h-screen w-screen flex-col overflow-hidden bg-background text-foreground">
      {/* 顶部操作工具栏 */}
      <header className="flex h-12 items-center justify-between border-b px-4">
        <span className="font-semibold">{doc.Name || doc.GraphID}</span>
        <div className="flex items-center gap-2">
          <GraphEditToolbar editor={editor} />
          <button
            onClick={handleSave}
            className="rounded bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:opacity-90"
          >
            保存图定义
          </button>
        </div>
      </header>

      {/* 核心工作台：左物料、中画布、右属性 */}
      <div className="flex flex-1 overflow-hidden">
        <aside className="w-56 border-r">
          <GraphPalette pack={nodePack} />
        </aside>

        <main className="flex-1 relative">
          <GraphWorkbench editor={editor} pack={nodePack} />
        </main>

        <aside className="w-80 border-l">
          <GraphNodePropsPanel editor={editor} pack={nodePack} />
        </aside>
      </div>
    </div>
  );
}
```

---

## 四、核心 API 与组件速查表

| 组件 / Hook | 作用 | 典型使用方式 |
| :--- | :--- | :--- |
| **`GraphWorkbench`** | 统一图工作台主画布 | `<GraphWorkbench editor={editor} pack={pack} />` |
| **`GraphCanvas`** | 纯底图画布（不含侧边栏与外挂控制） | 用于嵌入式运行回放或轻量展示 |
| **`GraphPalette`** | 左侧节点拖拽物料库 | 显示由 `nodeSpecs` 解析生成的全部可用节点分类 |
| **`GraphNodePropsPanel`**| 右侧选中节点属性编辑器 | 配置节点参数字面量、连线输入字段与分支选项 |
| **`GraphEditToolbar`** | 撤销、重做、对齐、清空工具栏 | 传入 `editor` 实例即可 |
| **`useGraphDocument`** | 图编辑内核状态 Hook | 提供 `editor.canUndo`、`editor.undo()`、`editor.savePayload()` |
| **`useGraphNodeCatalog`**| 异步加载并缓存节点类型列表 | `const { specs } = useGraphNodeCatalog(fetchApi)` |
| **`createGraphNodePackFromCatalog`** | 将接口规格转化为画布节点 Pack | 生成带有图标、端点配置和参数规格的对象 |
| **`parseGraphUI`** | 解析图的布局坐标与折叠状态 | 解析后端返回的 `UI` 结构体 |

---

## 五、与 Go 后端接口协同机制

组件库与后端 API（详细契约见 [docs/apis/README.md](file:///Users/luckluo/Tencent/codes/src/github.com/golang-io/graph/docs/apis/README.md)）完全协同：

1. **物料注册与 Spec 获取**：
   - 调用 `GET /node-specs`，获得包含全部节点端口（`In`、`Out`、`Case0`..`Case15`）及参数声明的注册表。
2. **图加载与结构保存**：
   - 加载：`GET /graphs/{id}`，返回 `GraphDefinition`（包含 `Graph` 执行拓扑与 `UI` 视觉坐标）；
   - 保存：`PUT /graphs/{id}`，`editor.savePayload()` 会自动生成符合后端严格校验（大驼峰、无非法未声明字段）的请求体。
3. **实时运行轨迹展示 (Task Watch)**：
   - 监听 `GET /tasks/{id}/watch`（SSE 流式事件）；
   - 将流式推过来的 `TaskSnapshot` 挂载到画布，画布内置自动着色（`RUNNING` 呼吸动效、`SUCCEEDED` 绿色高亮、`FAILED` 错误回溯）。

---

## 六、常见问题与排查 (Troubleshooting)

### Q1: 控制台报错 `Invalid hook call` 或 `Cannot read property of null (reading 'useMemo')`
- **根因**：本地通过 `file:` 引入时，存在两个独立的 React 副本（`your-app/node_modules/react` 和 `graph/ui/node_modules/react`）。
- **解决**：在外部工程的 `vite.config.ts` 中配置 `resolve: { dedupe: ['react', 'react-dom'] }`。

### Q2: 画布中的节点、边框或文字没有样式（完全脱发或样式错乱）
- **根因**：使用了 Tailwind CSS v4，但 Tailwind 未扫描上级 `ui/src` 目录中的 CSS 类。
- **解决**：在工程入口 CSS 中添加 `@source "../golang-io-graph/ui/src";`，并引入基础 CSS 变量（参见第二节第 5 步）。

### Q3: 运行 `npm install` 报错符号链接无法解析
- **根因**：npm 默认会将 `file:` 作为独立目录拷贝处理。
- **解决**：在外部工程创建 `.npmrc` 并写入 `install-links=false`，确保以符号链接（symlink）方式加载。
