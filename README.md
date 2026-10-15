# Graph

<div align="center">

**通用图执行与编排框架 (Go Workflow & DAG Execution Engine with Canvas UI)**

[![Build Status](https://github.com/golang-io/graph/actions/workflows/go.yml/badge.svg?branch=main)](https://github.com/golang-io/graph/actions/workflows/go.yml)
[![Go Version](https://img.shields.io/badge/go-1.27-blue.svg)](go.mod)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

</div>

---

## 📖 简介 (Overview)

`graph` 是一个高性能、可扩展的 Go 语言工作流与图执行引擎，支持 DAG 编排、分支控制（Switch）、并行分发（Fork/Join）、子图嵌套（Subgraph/Map）、单步调试与断点恢复，并内置现代化的可视化画布界面。

### ✨ 核心特性

- **双引擎架构**：
  - `immediate`：纯内存/低延迟单进程调度，极简轻量。
  - `durable`：基于分片租约机制的高可用持久化引擎，支持节点崩溃恢复、任务重放与多节点分布式调度。
- **丰富的控制流原语**：原生支持条件分支、并发分发、汇合合并、Map-Reduce、循环迭代与子图调用。
- **现代化可视化画布**：基于 React 19、X6 与 React Flow 的可视化图编辑与运行追踪工作台。
- **标准 HTTP 接口**：提供图定义管理（`/graphs`）与 Task 生命周期控制面（`/tasks`，支持 SSE 实时事件流推送）。

---

## 🚀 快速启动 Demo

启动前后端一体的本地 Demo：

```bash
# 方式 1：使用 Makefile 组合构建并启动
make -C cmd run

# 方式 2：手动构建并启动
cd cmd/web && npm install && npm run build
cd ../..
go run ./cmd -addr :8080 -data ./.data -web ./cmd/web/dist
```
访问 `http://localhost:8080` 即可进入图编辑与任务运行控制台。

---

## 🎨 前端 UI 画布接入 (`@golang-io/graph-ui`)

本项目包含开箱即用的前端可视化工作台组件库，基于 React Flow 与 AntV X6 双渲染内核。任何外部前端工程（基于 Vite）均可直接以源码依赖形式接入，无需发布 npm：

- **极简依赖**：在外部工程 `package.json` 中配置 `"@golang-io/graph-ui": "file:../path/to/graph/ui"`。
- **单例避坑**：在外部项目的 `vite.config.ts` 中配置 `resolve: { dedupe: ['react', 'react-dom'] }`。
- **完整指南**：详细接入步骤、最小可用代码示例与核心 API 请参阅 [前端 UI 画布接入与开发完整指南 (ui/README.md)](ui/README.md)。

---

## 🧪 测试指南 (Testing Guide)

本项目包含核心库、集成测试及前端 UI 画布套件，支持多种测试场景：

### 1. 日常快速单测

执行根模块核心库单测（速度极快，带缓存）：
```bash
go test ./...
```

执行引擎合约与端到端集成测试（位于 `test/` 独立模块）：
```bash
cd test && go test ./...
```

### 2. 代码覆盖率测试（Coverage）

**查看核心库单测覆盖率：**
```bash
# 生成 coverage.txt 并在浏览器自动打开可视化 HTML 报告
go test -coverprofile=coverage.txt ./... && go tool cover -html=coverage.txt
```

**跨模块全量覆盖率（推荐，包含引擎与组件集成测试）：**
使用 `-coverpkg` 将 `test/` 模块执行到的核心库代码完整纳入统计：
```bash
cd test && go test -coverpkg=github.com/golang-io/graph/... -coverprofile=coverage.txt ./... && go tool cover -html=coverage.txt
```

### 3. 并发竞态检测（严格模式）

排查多线程并发冲突与数据竞争（推荐提交代码前执行）：
```bash
# 根模块
go test -race ./...

# test 模块
cd test && go test -race ./...
```

### 4. 性能基准测试（Benchmark）

过滤普通测试，仅运行基准测试并统计每次操作的内存分配指标（`B/op`、`allocs/op`）：
```bash
# 核心表达式与序列化 Benchmark
go test -race -run=^$ -bench=. -benchmem ./...

# 引擎高并发与深层 DAG 执行性能 Benchmark
cd test && go test -race -run=^$ -bench=. -benchmem ./...
```

### 5. 前端 UI 画布测试

若修改了 `ui/` 前端画布内核代码，可执行 TypeScript 检查与 Vitest 单元测试：
```bash
# 执行全部 22 个测试套件（98 个测试用例）
cd ui && npm test

# 交互式监听模式（热更新重测）
cd ui && npx vitest

# 类型检查
cd ui && npm run typecheck
```

### 6. 一键全量验证

与 CI 流水线一致，一键执行后端与前端的所有静态检查、竞态测试与单测：
```bash
go test -race ./... && (cd test && go test -race ./...) && (cd ui && npm test)
```

---

## 📚 接口文档

详细的 API 设计与通信契约请参阅：
- [API 规范总览](docs/apis/README.md)
- [创建与保存图定义 (save-graph)](docs/apis/save-graph.md)
- [启动执行任务 (start-task)](docs/apis/start-task.md)
- [任务实时推送 (watch-task)](docs/apis/watch-task.md)
- [任务调试与恢复 (resume-task)](docs/apis/resume-task.md)

---

## 📄 License

本项目基于 [MIT License](LICENSE) 开源。