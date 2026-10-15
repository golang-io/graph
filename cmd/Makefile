# graph demo：前端（cmd/web → dist）+ 后端（Go demo）组合构建与启动
# 用法：在仓库根目录执行 make -C cmd <目标>，或进入 cmd/ 后直接 make <目标>
#
# Frontend (Vite) build under cmd/web → web/dist, then Go demo binary that serves it.
SHELL := /bin/bash
ROOT := $(abspath $(CURDIR)/..)
WEB := web
DIST := $(WEB)/dist
BIN := demo

ADDR ?= :8080
DATA ?= $(ROOT)/.data

# cmd 为独立 Go module；关闭 workspace，避免与仓库根 go.work 冲突
export GOWORK := off

.DEFAULT_GOAL := help

.PHONY: help all deps web go build run serve dev clean

help: ## 显示可用目标与变量（默认目标）
	@echo "graph demo — 前后端组合构建"
	@echo ""
	@echo "常用目标:"
	@echo "  make deps     安装前端依赖（cmd/web npm install）"
	@echo "  make web      构建前端 → $(DIST)"
	@echo "  make go       编译后端 → ./$(BIN)"
	@echo "  make build    前端 + 后端（web → go）"
	@echo "  make run      构建并启动（加载 $(DIST)）"
	@echo "  make serve    仅启动已有二进制与 dist（不重建）"
	@echo "  make dev      前端 Vite 热更新（需另开终端跑后端）"
	@echo "  make clean    删除二进制与前端 dist"
	@echo "  make help     显示本说明"
	@echo ""
	@echo "变量（可覆盖）:"
	@echo "  ADDR=$(ADDR)   监听地址"
	@echo "  DATA=$(DATA)   数据目录（sqlite）"
	@echo ""
	@echo "示例:"
	@echo "  make -C cmd run"
	@echo "  make -C cmd run ADDR=:9090 DATA=/tmp/graph-data"
	@echo "  make -C cmd build && make -C cmd serve"

all: build ## 同 build

# 前端依赖：首次或 node_modules 缺失时执行
deps: ## 安装前端 npm 依赖
	cd $(WEB) && npm install

# 前端产物：依赖 node_modules，产出 web/dist
web: $(WEB)/node_modules ## 构建前端 dist
	cd $(WEB) && npm run build

$(WEB)/node_modules:
	$(MAKE) deps

# Go 服务：产物为 cmd/demo（独立 module，自带 sqlite Dialector）
go: ## 编译 Go demo 二进制
	cd $(CURDIR) && go build -o $(CURDIR)/$(BIN) .

# 前后端组合：先 Vite 再 Go
build: web go ## 组合构建前端 + 后端

# 构建并启动（默认加载 web/dist）
run: build ## 构建后启动 demo
	./$(BIN) -addr $(ADDR) -data $(DATA) -web $(DIST)

# 不重建，直接用已有 binary + dist 启动（便于改完后快速重启）
serve: ## 启动已有 ./demo 与 dist（不重建）
	@test -x ./$(BIN) || { echo "缺少 ./$(BIN)，请先 make build 或 make go"; exit 1; }
	@test -d $(DIST) || { echo "缺少 $(DIST)，请先 make web"; exit 1; }
	./$(BIN) -addr $(ADDR) -data $(DATA) -web $(DIST)

# 前端热更新：另开终端执行 make go && make serve（或 make run）
dev: deps ## 前端 Vite 开发服务器
	cd $(WEB) && npm run dev

clean: ## 清理二进制与前端 dist
	rm -f $(CURDIR)/$(BIN)
	rm -rf $(CURDIR)/$(WEB)/dist
