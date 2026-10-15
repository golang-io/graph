// Package enginetest 是执行引擎的公共契约测试：同一组场景、同一组断言跑所有引擎
// （immediate、durable 与第三方引擎），保证各引擎只在调度与持久化方式上不同、执行语义一致。
//
//   - contract.go：契约场景（Run）与基准（Bench）；
//   - graph.go：测试节点（OpSpec / FuncNode）与构图辅助（G / GI / N / In / E / R / PrependStart）。
//
// 本包位于 test 子模块（github.com/golang-io/graph/test/engine/enginetest），根模块不导入它。
// 包内代码只依赖根模块的 engine、node 与 node/builtin，不依赖 service，也不直接导入数据库驱动；
// 被测引擎通过 engine.Builder 传入，执行库通过 OpenDB 由调用方提供（方言自选）。
// 本仓库传入 test.TempDB（临时 sqlite）。第三方引擎导入本包时，会把 test 子模块及其 sqlite 依赖带入依赖图。
//
// Package enginetest is the public contract suite every engine must pass.
// It imports no database driver; callers supply the execution database via OpenDB.
package enginetest
