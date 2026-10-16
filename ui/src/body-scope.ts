/**
 * body-scope — 内嵌子图（map 节点参数 Body 中的循环体）在画布上的作用域约定。
 *
 * 画布把展开的循环体节点与主图节点放在同一张画布上，用「作用域前缀」区分：
 *   - 主图节点的画布 ID 即节点 ID（如 lp）；
 *   - 循环体节点的画布 ID 为「容器画布 ID/本地节点 ID」（如 lp/body，嵌套时 lp/inner/x）。
 * 后端 ID 字符集不含 '/'（graph.ValidID），因此前缀不会与真实节点 ID 冲突。
 * 边只能连在同一作用域内；循环体与外层互不可见，与执行侧一致。
 *
 * Body scope: canvas IDs of body nodes are "<containerCanvasID>/<localID>"; edges never cross scopes.
 */
import type { GraphInputRef, GraphJSON, GraphJSONSchema, GraphUI } from './types'
import type { GraphEditorNode, GraphLayoutUI } from './document'

/** 承载内嵌子图的参数名（后端 builtin.MapInput.Body）。 */
export const BODY_INPUT = 'Body'

/** 作用域分隔符：容器画布 ID 与本地节点 ID 之间。 */
export const SCOPE_SEP = '/'

/** 展开容器的几何：子节点距容器左/右/下边的留白、标题栏高度、最小尺寸。 */
export const BODY_PAD = 24
export const BODY_HEADER = 56
export const BODY_MIN_SIZE = { w: 360, h: 200 }

/**
 * map 节点自身的参数名（与后端 node.mapParamNames 一致）。
 * 其余 Inputs 键是自定义输入，按同名传给循环体，因此需要出现在循环体 InputSchema 里。
 */
const MAP_PARAM_NAMES = new Set(['List', 'Chunk', 'MaxParallel', BODY_INPUT])

/** 循环体运行输入里由 map 固定提供的字段（后端 MapItemField / MapIndexField）。 */
const MAP_PROVIDED: Record<string, GraphJSONSchema> = {
  // Item 元素类型由上游 List 决定，与后端 graph.AnyValueSchema 对齐。
  Item: {
    type: ['string', 'integer', 'number', 'boolean', 'object', 'array', 'null'],
    description: '本轮元素',
  },
  Index: { type: 'integer', description: '本轮第一个元素在 List 中的下标' },
}

/** 画布 ID 所在作用域：主图节点为 ''，循环体节点为容器的画布 ID。 */
export function scopeOf(canvasId: string): string {
  const i = canvasId.lastIndexOf(SCOPE_SEP)
  return i < 0 ? '' : canvasId.slice(0, i)
}

/** 画布 ID 去掉作用域前缀后的本地节点 / 边 ID。 */
export function localIdOf(canvasId: string): string {
  return canvasId.slice(canvasId.lastIndexOf(SCOPE_SEP) + 1)
}

/** 由作用域与本地 ID 拼出画布 ID。 */
export function scopedId(scope: string, localId: string): string {
  return scope ? `${scope}${SCOPE_SEP}${localId}` : localId
}

/** containerId 是否为 canvasId 的祖先容器（canvasId 在它的循环体内，含多层嵌套）。 */
export function isInsideContainer(canvasId: string, containerId: string): boolean {
  return canvasId.startsWith(containerId + SCOPE_SEP)
}

/**
 * 取字面量参数里的内嵌子图（Body）。不是含 Nodes 数组的对象时返回 undefined，
 * 此时节点按普通卡片渲染。
 */
export function bodyGraphOf(literals?: Record<string, unknown>): GraphJSON | undefined {
  const v = literals?.[BODY_INPUT]
  if (!v || typeof v !== 'object' || !Array.isArray((v as GraphJSON).Nodes)) return undefined
  const g = v as GraphJSON
  return { ...g, Edges: Array.isArray(g.Edges) ? g.Edges : [] }
}

/** 同上，入参是落盘的 Node.Inputs。 */
function bodyGraphOfInputs(inputs?: Record<string, GraphInputRef>): GraphJSON | undefined {
  return bodyGraphOf(inputs?.[BODY_INPUT] ? { [BODY_INPUT]: inputs[BODY_INPUT].Value } : undefined)
}

/**
 * 循环体 InputSchema：在原声明上补齐 item / index 与 map 的自定义输入名（containerInputNames）。
 * 都已声明时原样返回，避免无改动时内容哈希变化；用户已写的类型、required 保留。
 */
export function bodyInputSchema(schema: GraphJSONSchema | undefined, containerInputNames: string[]): GraphJSONSchema {
  const want: Record<string, GraphJSONSchema> = { ...MAP_PROVIDED }
  // 自定义输入默认 string；调用方随后可在运行参数面板改类型。
  for (const name of containerInputNames) {
    if (!MAP_PARAM_NAMES.has(name)) want[name] ??= { type: 'string' }
  }
  const props = schema?.properties ?? {}
  const missing = Object.keys(want).filter((k) => !(k in props))
  if (schema && missing.length === 0) {
    // 字段已齐全且根合法时原样返回，避免拖拽等无改动写回产生脏数据；
    // 存量缺根 type / additionalProperties 时仍补齐（一次性迁移，下次写回即稳定）。
    if (schema.additionalProperties === false && schema.type === 'object') return schema
  }
  return {
    ...schema,
    type: 'object',
    additionalProperties: false,
    properties: { ...props, ...Object.fromEntries(missing.map((k) => [k, want[k]])) },
  }
}

/**
 * 求画布 ID 所在作用域的图与本地节点 ID：主图节点返回 document.Graph，
 * 循环体节点逐层进入容器的 Body。路径上任何一层缺失时返回 undefined。
 * 用于属性面板计算可引用来源（循环体只能引用本体内的上游节点与 item / index 等运行输入）。
 */
export function scopeGraphOf(document: GraphUI, canvasId: string): { graph: GraphJSON; localId: string } | undefined {
  const segments = canvasId.split(SCOPE_SEP)
  let graph: GraphJSON = document.Graph
  for (const seg of segments.slice(0, -1)) {
    const body = bodyGraphOfInputs(graph.Nodes.find((n) => n.ID === seg)?.Inputs)
    if (!body) return undefined
    graph = body
  }
  return { graph, localId: segments[segments.length - 1] }
}

/**
 * 设置容器的展开 / 收起状态：写在 UI.SubLayouts 对应路径的 Layout.Collapsed 上
 *（嵌套容器写进上层子图层 SubLayouts；值类型与主图层同为 Layout，无独立 Body 类型）。
 * 收起时保留循环体布局，再展开时恢复原位。
 */
export function setBodyCollapsed(document: GraphUI, containerId: string, collapsed: boolean): GraphUI {
  const setIn = (subLayouts: Record<string, GraphLayoutUI> | undefined, path: string[]): Record<string, GraphLayoutUI> => {
    const [head, ...rest] = path
    const cur = subLayouts?.[head] ?? {}
    let next: GraphLayoutUI
    if (rest.length) next = { ...cur, SubLayouts: setIn(cur.SubLayouts, rest) }
    else {
      const { Collapsed: _, ...open } = cur
      next = collapsed ? { ...cur, Collapsed: true } : open
    }
    return { ...subLayouts, [head]: next }
  }
  return { ...document, UI: { ...document.UI, SubLayouts: setIn(document.UI?.SubLayouts, containerId.split(SCOPE_SEP)) } }
}

/**
 * 计算展开容器的尺寸：包住全部子节点（含嵌套展开容器）再加留白，不小于 BODY_MIN_SIZE。
 * sizeOf 由渲染器提供普通卡片尺寸。由内向外计算，外层容器使用内层已算好的尺寸。
 */
export function fitContainers(
  nodes: GraphEditorNode[],
  sizeOf: (n: GraphEditorNode) => { w: number; h: number },
): Map<string, { w: number; h: number }> {
  const sizes = new Map<string, { w: number; h: number }>()
  const containers = nodes.filter((n) => n.body === 'expanded')
  if (containers.length === 0) return sizes

  // 单次遍历按父作用域归组子节点，将多容器场景下的复杂度从 O(C * N) 降为 O(N)
  const childrenByScope = new Map<string, GraphEditorNode[]>()
  for (const child of nodes) {
    const scope = scopeOf(child.id)
    if (!scope) continue
    const list = childrenByScope.get(scope)
    if (list) list.push(child)
    else childrenByScope.set(scope, [child])
  }

  const depth = (id: string) => id.split(SCOPE_SEP).length
  containers.sort((a, b) => depth(b.id) - depth(a.id))
  for (const c of containers) {
    let w = BODY_MIN_SIZE.w, h = BODY_MIN_SIZE.h
    for (const child of childrenByScope.get(c.id) ?? []) {
      const s = sizes.get(child.id) ?? sizeOf(child)
      w = Math.max(w, child.position.x + s.w - c.position.x + BODY_PAD)
      h = Math.max(h, child.position.y + s.h - c.position.y + BODY_PAD)
    }
    sizes.set(c.id, { w, h })
  }
  return sizes
}

/**
 * 新节点应放入的作用域：选中展开的容器时放进它的循环体；选中循环体内的节点时放进同一循环体；
 * 否则放在主图。
 */
export function insertionScope(nodes: GraphEditorNode[], selectedId: string | null | undefined): string {
  if (!selectedId) return ''
  const selected = nodes.find((n) => n.id === selectedId)
  if (selected?.body === 'expanded') return selected.id
  return scopeOf(selectedId)
}

/** 在作用域内为新节点选一个位置：容器内排在最右侧子节点之后；主图按原来的网格摆放。 */
export function insertionPosition(nodes: GraphEditorNode[], scope: string): { x: number; y: number } {
  if (!scope) {
    const n = nodes.length
    return { x: 80 + (n % 5) * 240, y: 80 + Math.floor(n / 5) * 140 }
  }
  const container = nodes.find((n) => n.id === scope)
  const siblings = nodes.filter((n) => scopeOf(n.id) === scope)
  const origin = container?.position ?? { x: 0, y: 0 }
  if (!siblings.length) return { x: origin.x + BODY_PAD, y: origin.y + BODY_HEADER }
  return { x: Math.max(...siblings.map((n) => n.position.x)) + 260, y: origin.y + BODY_HEADER }
}
