/**
 * vitest 配置。
 *
 * @antv/x6 3.x 的 package.json 声明了 "type": "module"，但 main 指向的 lib/ 是 CommonJS 产物；
 * vitest 在 Node 环境按 main 解析时会以 ES 模块加载 lib/index.js，报 `exports is not defined`。
 * 这里把包入口改指 module 字段对应的 es/ 产物（浏览器构建由 vite 按 module 字段解析，不受影响）。
 * es/ 内部使用目录导入（如 `./shape`），Node 原生 ESM 不支持，因此还要让 vite 转换 @antv 包（deps.inline）。
 * @antv/x6 declares "type": "module" while its `main` (lib/) is CommonJS; point tests at the ES build
 * and let vite transform it, since the ES build relies on directory imports.
 */
import { createRequire } from 'node:module'
import { dirname, join } from 'node:path'
import { defineConfig } from 'vitest/config'

const require = createRequire(import.meta.url)
// 按 Node 解析规则定位已安装的 @antv/x6，不假设 node_modules 的具体位置
const x6Dir = dirname(require.resolve('@antv/x6/package.json'))

export default defineConfig({
  resolve: {
    alias: [{ find: /^@antv\/x6$/, replacement: join(x6Dir, 'es/index.js') }],
  },
  test: {
    server: { deps: { inline: [/@antv\//] } },
  },
})
