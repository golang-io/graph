// Package test 是 graph 仓库的集成测试模块（独立 go.mod，github.com/golang-io/graph/test）。
//
// 本包提供本地 sqlite 打开辅助（Open / OpenPlain / MustOpen / TempDB），把 *gorm.DB 注入框架；
// 子目录按根模块的包结构存放集成、故障与端到端用例（api、service、example、
// engine/kernel、engine/immediate、engine/durable，graph 目录对应根包的跨引擎一致性用例）。
//
// sqlite 驱动只出现在 test/ 与 cmd/ 两个子模块中；graph 根模块不依赖任何数据库驱动。
// 引擎契约测试、测试节点与构图辅助在 engine/enginetest 包中，执行库由调用方经 OpenDB 提供。
//
// Package test is the integration test module of graph. It opens local sqlite databases;
// the graph root module itself imports no database driver.
package test
