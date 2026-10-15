// 用法：
//
//	cd cmd/web && npm install && npm run build   # 产出 cmd/web/dist
//	go run ./cmd -addr :8080 -data ./.data -web ./cmd/web/dist
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"syscall"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/golang-io/graph/api"
	"github.com/golang-io/graph/engine"
	"github.com/golang-io/graph/engine/durable"
	"github.com/golang-io/graph/engine/immediate"
	"github.com/golang-io/graph/node"
	"github.com/golang-io/graph/node/builtin"
	"github.com/golang-io/graph/service"
	"gorm.io/gorm"
)

//go:embed index.html
var web embed.FS

func main() {
	addr := flag.String("addr", ":8080", "监听地址")
	data := flag.String("data", "./.data", "数据目录（demo 用本地 sqlite：graph.db，定义库与两个引擎共用）")
	dist := flag.String("web", "", "前端静态目录（web/dist）；为空则自动探测 web/dist，探测不到退回内置简易页")
	flag.Parse()
	if *dist == "" { // 自动探测：make build 产出 dist 后无需再传 -web
		for _, cand := range []string{"web/dist", "cmd/web/dist"} {
			if info, err := os.Stat(cand); err == nil && info.IsDir() {
				*dist = cand
				break
			}
		}
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// demo 由本程序打开 sqlite；框架只使用注入的 *gorm.DB（必须使用 engine.GormConfig 的命名策略）。
	db, err := openDemoSQLite(filepath.Join(*data, "graph.db"))
	if err != nil {
		log.Fatal(err)
	}
	defer closeDB(db)

	// 样例 demo_all_nodes 含 http 与 llm 节点：HTTP 节点默认不注册，这里显式注册；
	// 本程序不接模型网关，llm 只注册空执行器以便写入定义。
	rt, err := service.Start(ctx, service.Options{
		DB:       db,
		Builders: []engine.Builder{immediate.Build, durable.Build},
		Nodes:    []node.Node{builtin.HTTPNode{}, builtin.LLMNode{}},
	})
	if err != nil {
		log.Fatal(err)
	}
	if err := service.ExampleGraph(ctx, rt.Store); err != nil { // 幂等写入 graph/example 样例
		log.Fatal(err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := rt.Close(closeCtx); err != nil {
			log.Printf("graph runtime close error: %v", err)
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/api/", api.Handler(rt, "/api"))
	// 与 /api/ 同为全方法子树：更具体的 /api/ 优先，GET / 会和方法更窄的模式冲突。
	if *dist != "" {
		mux.Handle("/", spaHandler(*dist))
	} else {
		log.Printf("graph demo: 未找到前端 dist，回退内置简易页；先执行 make -C cmd web 再启动可获得完整画布")

		mux.Handle("/", http.FileServer(http.FS(web)))
	}

	httpSrv := &http.Server{Addr: *addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}()
	// -addr 只写端口（":8080"）时主机部分为空，打印为 localhost；写了主机（"127.0.0.1:18080"）时原样打印。
	// An empty host (":8080") is shown as localhost; an explicit host is printed as is.
	shown := *addr
	if host, port, err := net.SplitHostPort(*addr); err == nil && host == "" {
		shown = net.JoinHostPort("localhost", port)
	}
	log.Printf("graph demo: http://%s", shown)
	if err := httpSrv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

// openDemoSQLite 仅 cmd demo 使用；生产应用可换成 mysql.Open / postgres.Open 等，配置同样取 engine.GormConfig()。
func openDemoSQLite(path string) (*gorm.DB, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, err
	}
	db, err := gorm.Open(sqlite.Open(abs+"?_txlock=immediate&_busy_timeout=10000"), engine.GormConfig())
	if err != nil {
		return nil, fmt.Errorf("open sqlite %s: %w", abs, err)
	}
	_ = db.Exec("PRAGMA journal_mode=WAL")
	_ = db.Exec("PRAGMA foreign_keys=OFF")
	_ = db.Exec("PRAGMA busy_timeout=10000")
	return db, nil
}

func closeDB(db *gorm.DB) {
	if db == nil {
		return
	}
	sqlDB, err := db.DB()
	if err != nil {
		return
	}
	_ = sqlDB.Close()
}

// spaHandler 托管前端 dist：命中文件即返回，否则回落 index.html 交给前端路由。
func spaHandler(dir string) http.Handler {
	root := http.Dir(dir)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := path.Clean("/" + r.URL.Path)[1:]
		if file, err := root.Open(name); err == nil {
			_ = file.Close()
		} else {
			name = "index.html"
		}
		http.ServeFile(w, r, filepath.Join(dir, filepath.FromSlash(name)))
	})
}
