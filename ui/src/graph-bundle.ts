/**
 * graph-bundle — 图的导入 / 导出文件格式，键名与后端 JSON 一致（大驼峰）。
 *
 * 导入接受三种结构：完整导出文件 {Kind, Name, Description, Graph, UI}、{Graph, UI?}、只有图的 {Nodes, Edges}。
 */
import type { GraphEdgeJSON, GraphInputRef, GraphJSON, GraphJSONSchema, GraphNodeJSON } from './types'
import type { GraphLayoutUI } from './document'
import { parseGraphUI } from './graph-adapter'
import { parseEndpointID } from './endpoints'
import { runInputNames } from './run-input-schema'

export const GRAPH_BUNDLE_KIND = 'graph.bundle' as const

/** 导出文件的外层结构。 */
export type GraphBundle = {
  Kind: typeof GRAPH_BUNDLE_KIND
  Name: string
  Description?: string
  Graph: GraphJSON
  UI?: GraphLayoutUI
}

export type ParseBundleOk = { ok: true; bundle: GraphBundle }
export type ParseBundleErr = { ok: false; error: string }
export type ParseBundleResult = ParseBundleOk | ParseBundleErr

export type BuildBundleInput = {
  name: string
  description?: string
  graph: GraphJSON
  ui?: unknown
}

/** 布局里是否有需要导出的内容。 */
function layoutHasContent(ui: GraphLayoutUI): boolean {
  return Boolean(
    (ui.Positions && Object.keys(ui.Positions).length > 0) ||
    (ui.EndpointSides && Object.keys(ui.EndpointSides).length > 0) ||
    (ui.EdgeRails && Object.keys(ui.EdgeRails).length > 0) ||
    (ui.SubLayouts && Object.keys(ui.SubLayouts).length > 0) ||
    ui.ExecutionMode != null ||
    (ui.StickyNotes && ui.StickyNotes.length > 0),
  )
}

/** 组装可下载的导出文件（UI 经 parseGraphUI 规范化）。 */
export function buildGraphBundle(input: BuildBundleInput): GraphBundle {
  const name = (input.name || '').trim() || 'untitled'
  const description = (input.description || '').trim() || undefined
  const ui = parseGraphUI(input.ui)
  return {
    Kind: GRAPH_BUNDLE_KIND,
    Name: name,
    ...(description ? { Description: description } : {}),
    Graph: {
      ...(input.graph.InputSchema && runInputNames(input.graph.InputSchema).length
        ? { InputSchema: input.graph.InputSchema }
        : {}),
      Nodes: input.graph.Nodes ?? [],
      Edges: input.graph.Edges ?? [],
    },
    ...(layoutHasContent(ui) ? { UI: ui } : {}),
  }
}

/** 解析并校验导入 JSON。 */
export function parseGraphBundle(raw: unknown, defaultName = 'imported'): ParseBundleResult {
  if (raw == null || typeof raw !== 'object' || Array.isArray(raw)) {
    return { ok: false, error: 'root_not_object' }
  }
  const o = raw as Record<string, unknown>

  // 完整导出文件：Kind 存在时必须匹配。
  if (o.Kind != null && o.Kind !== GRAPH_BUNDLE_KIND) {
    return { ok: false, error: 'unknown_kind' }
  }

  let graphRaw: unknown
  let name = defaultName
  let description: string | undefined
  let uiRaw: unknown

  if (isGraphShape(o)) {
    graphRaw = o
  } else if (o.Graph != null && typeof o.Graph === 'object' && !Array.isArray(o.Graph)) {
    graphRaw = o.Graph
    if (typeof o.Name === 'string' && o.Name.trim()) name = o.Name.trim()
    if (typeof o.Description === 'string' && o.Description.trim()) description = o.Description.trim()
    uiRaw = o.UI
  } else {
    return { ok: false, error: 'missing_graph' }
  }

  const graphResult = parseAndValidateGraph(graphRaw)
  if (!graphResult.ok) return graphResult

  const ui = parseGraphUI(uiRaw)
  return {
    ok: true,
    bundle: {
      Kind: GRAPH_BUNDLE_KIND,
      Name: name,
      ...(description ? { Description: description } : {}),
      Graph: graphResult.graph,
      ...(layoutHasContent(ui) ? { UI: ui } : {}),
    },
  }
}

type GraphOk = { ok: true; graph: GraphJSON }
type GraphErr = { ok: false; error: string }

function isGraphShape(o: Record<string, unknown>): boolean {
  return Array.isArray(o.Nodes) && Array.isArray(o.Edges) && o.Graph == null
}

function parseAndValidateGraph(raw: unknown): GraphOk | GraphErr {
  if (raw == null || typeof raw !== 'object' || Array.isArray(raw)) {
    return { ok: false, error: 'invalid_graph' }
  }
  const g = raw as Record<string, unknown>
  if (!Array.isArray(g.Nodes) || !Array.isArray(g.Edges)) {
    return { ok: false, error: 'invalid_graph' }
  }

  const nodes: GraphNodeJSON[] = []
  const ids = new Set<string>()

  for (let i = 0; i < g.Nodes.length; i++) {
    const item = g.Nodes[i]
    if (item == null || typeof item !== 'object' || Array.isArray(item)) {
      return { ok: false, error: `invalid_node:${i}` }
    }
    const n = item as Record<string, unknown>
    const id = typeof n.ID === 'string' ? n.ID.trim() : ''
    const kind = typeof n.Kind === 'string' ? n.Kind.trim() : ''
    if (!id) return { ok: false, error: `empty_node_id:${i}` }
    if (!kind) return { ok: false, error: `empty_node_kind:${i}` }
    if (ids.has(id)) return { ok: false, error: `duplicate_node_id:${id}` }
    ids.add(id)
    const node: GraphNodeJSON = { ID: id, Kind: kind }
    if (typeof n.Name === 'string' && n.Name.trim()) node.Name = n.Name.trim()
    // 输入引用的合法性（来源节点、字段名）由服务端 Validate 判定，这里只保证结构是对象。
    if (n.Inputs != null) {
      if (typeof n.Inputs !== 'object' || Array.isArray(n.Inputs)) return { ok: false, error: `invalid_node:${i}` }
      node.Inputs = n.Inputs as Record<string, GraphInputRef>
    }
    nodes.push(node)
  }

  const edges: GraphEdgeJSON[] = []
  const edgeIds = new Set<string>()
  for (let i = 0; i < g.Edges.length; i++) {
    const item = g.Edges[i]
    if (item == null || typeof item !== 'object' || Array.isArray(item)) {
      return { ok: false, error: `invalid_edge:${i}` }
    }
    const e = item as Record<string, unknown>
    const id = typeof e.ID === 'string' && e.ID.trim() ? e.ID.trim() : `e${i}`
    const from = typeof e.From === 'string' ? e.From.trim() : ''
    const to = typeof e.To === 'string' ? e.To.trim() : ''
    if (!from || !to) return { ok: false, error: `empty_edge_endpoint:${i}` }
    // From / To 是端点 ID（nodeID.localName）：按节点 ID 校验存在性。
    const fromNode = parseEndpointID(from).node
    const toNode = parseEndpointID(to).node
    if (!ids.has(fromNode)) return { ok: false, error: `edge_from_missing:${fromNode}` }
    if (!ids.has(toNode)) return { ok: false, error: `edge_to_missing:${toNode}` }
    if (edgeIds.has(id)) return { ok: false, error: `duplicate_edge_id:${id}` }
    edgeIds.add(id)
    const edge: GraphEdgeJSON = { ID: id, From: from, To: to }
    edges.push(edge)
  }

  // 运行输入 Schema：只做浅层结构校验，名字与 required 由服务端判定。
  let inputSchema: GraphJSONSchema | undefined
  if (g.InputSchema != null) {
    if (typeof g.InputSchema !== 'object' || Array.isArray(g.InputSchema)) {
      return { ok: false, error: 'invalid_graph' }
    }
    const s = g.InputSchema as GraphJSONSchema
    if (s.properties != null && (typeof s.properties !== 'object' || Array.isArray(s.properties))) {
      return { ok: false, error: 'invalid_graph' }
    }
    if (runInputNames(s).length) inputSchema = s
  }

  return {
    ok: true,
    graph: { ...(inputSchema ? { InputSchema: inputSchema } : {}), Nodes: nodes, Edges: edges },
  }
}

/** 将名称压成文件名安全 slug */
export function slugGraphName(name: string): string {
  const s = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9\u4e00-\u9fff]+/gi, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 48)
  return s || 'graph'
}

/** 浏览器下载 Bundle JSON */
export function downloadGraphBundle(bundle: GraphBundle): void {
  const text = JSON.stringify(bundle, null, 2)
  const blob = new Blob([text], { type: 'application/json;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `${slugGraphName(bundle.Name)}-graph.json`
  a.rel = 'noopener'
  document.body.appendChild(a)
  a.click()
  a.remove()
  URL.revokeObjectURL(url)
}

/** 读本地文件为文本 */
export function readFileAsText(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onload = () => resolve(String(reader.result ?? ''))
    reader.onerror = () => reject(reader.error ?? new Error('read_failed'))
    reader.readAsText(file, 'utf-8')
  })
}

/** 把校验错误码映射为可读文案键后缀（i18n 用 graph.import.err.*） */
export function bundleErrorI18nKey(error: string): string {
  const code = error.split(':')[0] ?? error
  const known = new Set([
    'root_not_object',
    'unknown_kind',
    'missing_graph',
    'invalid_graph',
    'invalid_node',
    'empty_node_id',
    'empty_node_kind',
    'duplicate_node_id',
    'unknown_node_type',
    'invalid_edge',
    'empty_edge_endpoint',
    'edge_from_missing',
    'edge_to_missing',
    'duplicate_edge_id',
    'invalid_json',
  ])
  return known.has(code) ? code : 'unknown'
}
