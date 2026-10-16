/**
 * document — 后端文档（大驼峰 JSON）与画布编辑模型（小驼峰）之间的转换。
 * projectDocument 把 GraphUI 投影为编辑节点 / 边；documentFromProjection 把编辑结果写回 GraphUI。
 * 展开的 map 循环体（参数 Body）与主图投影在同一张画布上，画布 ID 带容器前缀，写回时按前缀拆回各自的图（见 body-scope.ts）。
 */
import type { CSSProperties } from 'react'
import { GRAPH_DEFAULT_INPUT, GRAPH_DEFAULT_OUTPUT, type GraphInputRef, type GraphJSON, type GraphNodeJSON, type GraphUI } from './types'
import { BODY_HEADER, BODY_INPUT, BODY_PAD, bodyGraphOf, bodyInputSchema, localIdOf, scopedId, scopeOf } from './body-scope'
import { mergeInputs, splitInputs } from './inputs-split'
import { formatEndpointID, parseEndpointID } from './endpoints'
import { parseEndpointSides, resolveEndpointSides, type EndpointSides, type Side } from './orient'
import { instanceEndpointIds } from './node-size'
import type { NodeTypeRegistry } from './node-registry'
import type { RunStatus } from './run-status'

export type GraphPoint = { x: number; y: number }

/** 布局里的坐标（graph.Point）。 */
export type GraphUIPoint = { X: number; Y: number }

/** 便签（graph.StickyNote）。 */
export type GraphUIStickyNote = {
  ID: string
  X: number
  Y: number
  Width: number
  Height: number
  Title?: string
  Note?: string
  Color?: string
  Transparent?: boolean
}

/** 单个节点的端点所在边（graph.EndpointSides）：端点 ID → 边，与编辑模型的 EndpointSides 同形。 */
export type GraphUIEndpointSides = EndpointSides

/**
 * 回边轨道（graph.UI.EdgeRails）：与端点所在边共用取值。
 * top/bottom 走两端节点上方/下方（出口在左右边时有效），left/right 走左侧/右侧（出口在上下边，即纵向图）。
 */
export type GraphEdgeRail = Side

/**
 * 画布布局文档字段（对应 Go 端 graph.UI + 匿名内嵌的 graph.Layout）。
 * 主图层与子图层（UI.SubLayouts 的值）共用 Layout 形状，不另设 Body 类型；结构递归。
 *
 * Collapsed 只对子图层有效：true 时容器收起成普通卡片；缺省展开为可编辑的大框。
 * 主图层的 Collapsed 无意义：解析不读、写回不写（与后端 CleanUI 清除一致）。
 */
export type GraphLayoutUI = {
  /** 只对 SubLayouts 中的子图层有效；主图层忽略 */
  Collapsed?: boolean
  Positions?: Record<string, GraphUIPoint>
  /** 本层各节点的端点所在边；保存时每个节点的每个端点都必须写明（保存前由 withEndpointSides 补齐） */
  EndpointSides?: Record<string, GraphUIEndpointSides>
  EdgeRails?: Record<string, GraphEdgeRail>
  ExecutionMode?: 'serial' | 'parallel'
  StickyNotes?: GraphUIStickyNote[]
  /** 拥有内嵌子图的节点 ID → 子图层布局（仍为 GraphLayoutUI / graph.Layout） */
  SubLayouts?: Record<string, GraphLayoutUI>
}

/** 一层作用域的 Layout 字段（主图或某个循环体），对应 Go 端 graph.Layout。 */
type ScopeLayout = Pick<GraphLayoutUI, 'Collapsed' | 'Positions' | 'EndpointSides' | 'EdgeRails' | 'SubLayouts'>

export type GraphViewState = {
  nodes?: Record<string, {
    selected?: boolean; dragging?: boolean; runStatus?: RunStatus
    runProgress?: string; breakpoint?: boolean; inject?: boolean; style?: CSSProperties
  }>
  edges?: Record<string, { selected?: boolean; runStatus?: RunStatus; animated?: boolean; style?: CSSProperties }>
}

/**
 * 把视图状态（选中、拖拽、运行态等只影响展示的字段）合并进结构投影。
 * projectDocument 的结果只含图结构，三个使用方（useGraphDocument、React Flow 画布、X6 画布）
 * 统一经此合并，合并规则只有一份：
 * - view 中没有对应 ID 的条目时，节点 / 边保持原引用，避免下游按引用比较时误判为已变化；
 * - view 既无 nodes 也无 edges 时原样返回 base（同一引用），不重建数组。
 */
export function applyGraphViewState(
  base: { nodes: GraphEditorNode[]; edges: GraphEditorEdge[] },
  view: GraphViewState,
): { nodes: GraphEditorNode[]; edges: GraphEditorEdge[] } {
  if (!view.nodes && !view.edges) return base
  return {
    nodes: view.nodes
      ? base.nodes.map((n) => (view.nodes?.[n.id] ? { ...n, ...view.nodes[n.id] } : n))
      : base.nodes,
    edges: view.edges
      ? base.edges.map((e) => (view.edges?.[e.id] ? { ...e, ...view.edges[e.id] } : e))
      : base.edges,
  }
}

/**
 * 返回把编辑节点 id 的端点所在边替换为 sides 后的节点列表，未命中的节点保持原引用。
 * 供页面层修改单个节点的端点方位时共用；历史记录等副作用由调用方在调用前自行处理。
 */
export function withNodeEndpointSides(nodes: GraphEditorNode[], id: string, sides: EndpointSides): GraphEditorNode[] {
  return nodes.map((n) => (n.id === id ? { ...n, endpointSides: sides } : n))
}

/** 画布编辑模型中的节点（渲染器内部结构，非 JSON）。便签以 type='annotation' 的节点表示。 */
export interface GraphEditorNode {
  id: string
  type: string
  name: string
  config: Record<string, unknown>
  /** 节点输入（Node.Inputs）：输入名 → 引用；缺省表示没有输入 */
  inputs?: Record<string, GraphInputRef>
  position: GraphPoint
  endpointSides?: EndpointSides
  note?: string
  color?: string
  transparent?: boolean
  width?: number
  height?: number
  /**
   * 拥有内嵌子图（参数 Body）的节点：expanded 展开为包住循环体节点的大框，collapsed 收起为普通卡片。
   * 由 UI.SubLayouts[id].Collapsed（子图层 Layout）推导，不是编辑字段；循环体节点的画布 ID 带容器前缀（见 body-scope.ts）。
   */
  body?: 'expanded' | 'collapsed'
  selected?: boolean
  dragging?: boolean
  runStatus?: RunStatus
  runProgress?: string
  breakpoint?: boolean
  inject?: boolean
  style?: CSSProperties
}

/** 画布编辑模型中的边（渲染器内部结构，非 JSON）。 */
export interface GraphEditorEdge {
  id: string
  from: string
  to: string
  when?: string
  rail?: GraphEdgeRail
  label?: string
  selected?: boolean
  runStatus?: RunStatus
  animated?: boolean
  style?: CSSProperties
}

/**
 * 为 graph 中每个节点的每个实例端点写明所在边（resolveEndpointSides：补齐缺失、丢弃失效条目、避开入口所在边），
 * 带内嵌子图的节点逐层补到 SubLayouts。注册表里没有的种类不知道端点，保留原有条目。
 */
function fillEndpointSides(graph: GraphJSON, layout: ScopeLayout, registry: NodeTypeRegistry | null | undefined): ScopeLayout {
  const sides: Record<string, GraphUIEndpointSides> = {}
  let subLayouts = layout.SubLayouts
  for (const n of graph.Nodes) {
    const { literals } = splitInputs(n.Inputs)
    const eps = instanceEndpointIds(registry, n.Kind, literals)
    const old = parseEndpointSides(layout.EndpointSides?.[n.ID])
    sides[n.ID] = eps ? resolveEndpointSides(old, eps) : old
    const body = bodyGraphOf(literals)
    if (body) subLayouts = { ...subLayouts, [n.ID]: fillEndpointSides(body, subLayouts?.[n.ID] ?? {}, registry) }
  }
  return {
    ...layout,
    ...(graph.Nodes.length || layout.EndpointSides ? { EndpointSides: sides } : {}),
    ...(subLayouts ? { SubLayouts: subLayouts } : {}),
  }
}

/**
 * 保存前调用：按注册表为主图与各层循环体每个节点的每个端点写明 UI.EndpointSides，
 * 缺失的补齐（入口左、出口右、另有其他出口时 Error 下），已删除端点（如 switch 分支）的条目丢弃。
 * 后端要求每个端点都写明，新拖入的节点、新增的分支、旧文档都靠这里补齐，因此保存路径必须经过这里。
 * 已完整的文档原样返回（同一引用）。
 */
export function withEndpointSides(document: GraphUI, registry: NodeTypeRegistry | null | undefined): GraphUI {
  const ui = document.UI ?? {}
  const filled = { ...ui, ...fillEndpointSides(document.Graph, ui, registry) }
  return same(filled, ui) ? document : { ...document, UI: filled }
}

/**
 * projectDocument 增量复用缓存项：记录节点原始 Inputs 引用与已克隆的 config / inputs，
 * 在仅坐标或布局变化时跳过对未改动节点的 splitInputs 与 structuredClone。
 * Incremental cache entry for projectDocument to skip splitInputs/structuredClone on unchanged nodes.
 */
export type ProjectionNodeCacheEntry = {
  rawInputs: unknown
  body?: 'expanded' | 'collapsed'
  config: Record<string, unknown>
  inputs?: Record<string, GraphInputRef>
}

const KNOWN_UI_KEYS = new Set([
  'Positions',
  'EndpointSides',
  'EdgeRails',
  'ExecutionMode',
  'StickyNotes',
  'SubLayouts',
])

/**
 * 过滤掉画布不认识的顶层 UI 键（主图层 Collapsed 等不写出，与后端 CleanUI 一致）；
 * 若全部顶层键均合法则原样返回 base 同一引用，避免无谓的浅拷贝与重投影。
 * Strips unknown top-level UI keys; returns the same reference if all keys are valid.
 */
export function cleanDocumentUI(base: GraphUI): GraphUI {
  if (!base.UI) return base
  const entries = Object.entries(base.UI)
  const filtered = entries.filter(([key]) => KNOWN_UI_KEYS.has(key))
  if (filtered.length === entries.length) return base
  return { ...base, UI: Object.fromEntries(filtered) as GraphLayoutUI }
}

export function projectDocument(
  document: GraphUI,
  view: GraphViewState = {},
  reuseCache?: Map<string, ProjectionNodeCacheEntry>,
) {
  const ui = document.UI ?? {}
  const nodes: GraphEditorNode[] = [], edges: GraphEditorEdge[] = []
  const nextCache = reuseCache ? new Map<string, ProjectionNodeCacheEntry>() : undefined
  projectScope(document.Graph, ui, '', { x: 0, y: 0 }, view, nodes, edges, reuseCache, nextCache)
  if (reuseCache && nextCache) {
    reuseCache.clear()
    for (const [k, v] of nextCache) reuseCache.set(k, v)
  }
  const ids = new Set(nodes.map((n) => n.id))
  for (const n of (ui.StickyNotes ?? []).slice().reverse()) {
    if (ids.has(n.ID)) throw new Error(`Duplicate canvas node ${n.ID}`)
    ids.add(n.ID)
    nodes.unshift({ ...annotationEditorNode(n), ...view.nodes?.[n.ID] })
  }
  return { nodes, edges }
}

/**
 * 投影一层作用域（主图或某个循环体）。scope 为容器画布 ID（主图为 ''），origin 为容器左上角（主图为原点）；
 * 布局里的循环体坐标相对容器，投影后统一为画布绝对坐标。
 * 展开的容器紧接着投影它的循环体，保证容器排在子节点之前（React Flow 要求父节点先于子节点）。
 */
function projectScope(
  graph: GraphJSON, layout: ScopeLayout, scope: string, origin: GraphPoint,
  view: GraphViewState, nodes: GraphEditorNode[], edges: GraphEditorEdge[],
  reuseCache?: Map<string, ProjectionNodeCacheEntry>,
  nextCache?: Map<string, ProjectionNodeCacheEntry>,
) {
  // 没有坐标的节点从左到右沿 x 递增排布；循环体内从标题栏下方开始。
  graph.Nodes.forEach((n, i) => {
    const id = scopedId(scope, n.ID), at = layout.Positions?.[n.ID]
    const rel = at ? { x: at.X, y: at.Y }
      : scope ? { x: BODY_PAD + i * 240, y: BODY_HEADER }
        : { x: i * 240, y: 100 }
    const position = { x: origin.x + rel.x, y: origin.y + rel.y }
    const cached = reuseCache?.get(id)
    let config: Record<string, unknown>
    let inputs: Record<string, GraphInputRef> | undefined
    let body: GraphJSON | undefined
    let mode: 'expanded' | 'collapsed' | undefined
    const bodyUI = layout.SubLayouts?.[n.ID]

    if (cached && cached.rawInputs === n.Inputs && cached.body === undefined) {
      config = cached.config
      inputs = cached.inputs
    } else {
      const { literals, bindings } = splitInputs(n.Inputs)
      body = bodyGraphOf(literals)
      mode = body ? (bodyUI?.Collapsed ? 'collapsed' : 'expanded') : undefined
      if (cached && cached.rawInputs === n.Inputs && cached.body === mode) {
        config = cached.config
        inputs = cached.inputs
      } else {
        config = structuredClone(literals)
        inputs = Object.keys(bindings).length ? structuredClone(bindings) : undefined
      }
    }

    if (nextCache) {
      nextCache.set(id, { rawInputs: n.Inputs, body: mode, config, inputs })
    }

    nodes.push({
      id, type: n.Kind, name: n.Name ?? n.Kind, config,
      ...(inputs ? { inputs } : {}),
      // 端点所在边原样投影（未写明的端点由渲染器按 resolveEndpointSides 补齐显示，保存前由 withEndpointSides 写入）
      position, endpointSides: parseEndpointSides(layout.EndpointSides?.[n.ID]), ...(mode ? { body: mode } : {}), ...view.nodes?.[id],
    })
    if (mode === 'expanded') projectScope(body!, bodyUI ?? {}, id, position, view, nodes, edges, reuseCache, nextCache)
  })
  for (const e of graph.Edges) {
    // 端点 ID → 编辑模型：from/to 取节点画布 ID，when 取 From 端点本地名（默认出口 Out 记为无分支）。
    const from = parseEndpointID(e.From), id = scopedId(scope, e.ID)
    const when = from.name && from.name !== GRAPH_DEFAULT_OUTPUT ? from.name : undefined
    edges.push({
      id, from: scopedId(scope, from.node), to: scopedId(scope, parseEndpointID(e.To).node),
      ...(when !== undefined ? { when } : {}),
      rail: layout.EdgeRails?.[e.ID], ...view.edges?.[id],
    })
  }
}

/** documentFromProjection 写回各作用域时共用的上下文。 */
type WriteContext = {
  nodesByScope: Map<string, GraphEditorNode[]>
  edgesByScope: Map<string, GraphEditorEdge[]>
  /** 原文档中已展开的容器：只有它们的子节点在画布上，Body 才按画布重建 */
  expanded: Set<string>
  originalPosition: Map<string, GraphPoint>
  originalNode: Map<string, GraphEditorNode>
}

function sameAuthorNode(a: GraphEditorNode, b: GraphEditorNode): boolean {
  if (a === b) return true
  return (
    a.id === b.id &&
    a.type === b.type &&
    a.name === b.name &&
    a.position.x === b.position.x &&
    a.position.y === b.position.y &&
    a.width === b.width &&
    a.height === b.height &&
    a.note === b.note &&
    a.color === b.color &&
    a.transparent === b.transparent &&
    (a.endpointSides === b.endpointSides || same(a.endpointSides, b.endpointSides)) &&
    (a.config === b.config || same(a.config, b.config)) &&
    (a.inputs === b.inputs || same(a.inputs, b.inputs))
  )
}

function sameAuthorNodes(a: GraphEditorNode[], b: GraphEditorNode[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    if (!sameAuthorNode(a[i]!, b[i]!)) return false
  }
  return true
}

function sameAuthorEdges(a: GraphEditorEdge[], b: GraphEditorEdge[]): boolean {
  if (a.length !== b.length) return false
  for (let i = 0; i < a.length; i++) {
    const ea = a[i]!, eb = b[i]!
    if (ea.id !== eb.id || ea.from !== eb.from || ea.to !== eb.to || ea.when !== eb.when || ea.rail !== eb.rail) {
      return false
    }
  }
  return true
}

export function documentFromProjection(
  nodes: GraphEditorNode[],
  edges: GraphEditorEdge[],
  base: GraphUI,
  originalProjection?: { nodes: GraphEditorNode[]; edges: GraphEditorEdge[] },
): GraphUI {
  const original = originalProjection ?? projectDocument(base)
  // 画布未改动（只有选中、运行态等展示叠加）：只去掉画布不认识的 UI 键，否则原样返回，不产生保存差异；
  // 缺失的 EndpointSides 由保存前的 withEndpointSides 补齐。
  if (sameAuthorNodes(nodes, original.nodes) && sameAuthorEdges(edges, original.edges)) {
    return cleanDocumentUI(base)
  }
  // 只保留 graph.UI 已知的键；主图层 Collapsed 不写出（与后端 CleanUI 一致）。
  const ui = Object.fromEntries(Object.entries(base.UI ?? {}).filter(([key]) => KNOWN_UI_KEYS.has(key))) as GraphLayoutUI
  const group = <T,>(list: T[], key: (item: T) => string) => {
    const out = new Map<string, T[]>()
    for (const item of list) {
      const k = key(item)
      const bucket = out.get(k)
      if (bucket) bucket.push(item)
      else out.set(k, [item])
    }
    return out
  }
  const ctx: WriteContext = {
    nodesByScope: group(nodes.filter((n) => n.type !== 'annotation'), (n) => scopeOf(n.id)),
    // 跨作用域的边（循环体与外层之间）在执行侧不存在，写回时丢弃
    edgesByScope: group(edges.filter((e) => scopeOf(e.from) === scopeOf(e.to)), (e) => scopeOf(e.from)),
    expanded: new Set(original.nodes.filter((n) => n.body === 'expanded').map((n) => n.id)),
    originalPosition: new Map(original.nodes.map((n) => [n.id, n.position])),
    originalNode: new Map(original.nodes.map((n) => [n.id, n])),
  }
  const root = writeScope('', base.Graph, ui, { x: 0, y: 0 }, ctx)
  const has = (m?: Record<string, unknown>) => !!m && Object.keys(m).length > 0
  // 当顶层 Nodes、Edges 均与原图引用一致且 base.Graph 不含额外未知键时，直接复用 base.Graph 引用，
  // 使仅改动布局（拖拽节点、缩放便签、旋转端点）时不触发下游对 Graph 的重新解析与序列化比对。
  const sameGraphRef =
    root.graph.Nodes === base.Graph.Nodes &&
    root.graph.Edges === base.Graph.Edges &&
    Object.keys(base.Graph).every((k) => k === 'InputSchema' || k === 'Nodes' || k === 'Edges')
  return {
    ...base,
    Graph: sameGraphRef
      ? base.Graph
      : {
          // 运行输入声明不在画布上编辑，原样保留。
          ...(base.Graph.InputSchema ? { InputSchema: base.Graph.InputSchema } : {}),
          Nodes: root.graph.Nodes,
          Edges: root.graph.Edges,
        },
    UI: {
      ...ui,
      ...(ui.Positions || has(root.layout.Positions) ? { Positions: root.layout.Positions ?? {} } : {}),
      ...(ui.EndpointSides || has(root.layout.EndpointSides) ? { EndpointSides: root.layout.EndpointSides ?? {} } : {}),
      ...(ui.EdgeRails || has(root.layout.EdgeRails) ? { EdgeRails: root.layout.EdgeRails ?? {} } : {}),
      ...(ui.SubLayouts || has(root.layout.SubLayouts) ? { SubLayouts: root.layout.SubLayouts ?? {} } : {}),
      ...(ui.StickyNotes || nodes.some((n) => n.type === 'annotation') ? { StickyNotes: nodes.filter((n) => n.type === 'annotation').map((n) => ({
        ID: n.id, X: n.position.x, Y: n.position.y, Width: n.width ?? 300, Height: n.height ?? 180,
        Title: base.UI?.StickyNotes?.some((s) => s.ID === n.id && s.Title === undefined) && n.name === '便签' ? undefined : n.name, Note: n.note, Color: n.color, Transparent: n.transparent,
      })) } : {}),
    },
  }
}

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b)

/**
 * 把一层作用域的画布节点 / 边写回为图与布局。scope 为容器画布 ID（主图为 ''），origin 为容器左上角。
 * - 主图坐标沿用原规则：原来存过或被移动过的节点才写入 Positions；
 * - 循环体坐标一律写成相对容器的坐标，且不进入标题栏与左侧留白（拖出时夹回）；
 * - 原文档中已展开的容器递归写回循环体，Body 中画布不编辑的字段（如 InputSchema）保留，
 *   InputSchema 补齐 item / index 与容器的自定义输入名；收起或新建的容器保留参数里的 Body 与原布局。
 */
function writeScope(scope: string, oldGraph: GraphJSON, oldLayout: ScopeLayout, origin: GraphPoint, ctx: WriteContext): { graph: Pick<GraphJSON, 'Nodes' | 'Edges'>; layout: ScopeLayout } {
  const list = ctx.nodesByScope.get(scope) ?? [], ids = new Set(list.map((n) => n.id))
  const oldById = new Map(oldGraph.Nodes.map((o) => [o.ID, o]))
  const positions: Record<string, GraphUIPoint> = {}, sides: Record<string, GraphUIEndpointSides> = {}, subLayouts: Record<string, GraphLayoutUI> = {}
  const nodes = list.map((n): GraphNodeJSON => {
    const local = localIdOf(n.id), old = oldById.get(local), orig = ctx.originalNode.get(n.id)
    let config = n.config
    const body = bodyGraphOf(config)
    if (body && ctx.expanded.has(n.id)) {
      const sub = writeScope(n.id, body, oldLayout.SubLayouts?.[local] ?? {}, n.position, ctx)
      const rawBody = (config[BODY_INPUT] as GraphJSON | undefined) ?? body
      const canSkipMerge = config === orig?.config && n.inputs === orig?.inputs && old?.Inputs !== undefined
      const inputNames = canSkipMerge ? Object.keys(old.Inputs!) : Object.keys(mergeInputs(config, n.inputs))
      const schema = bodyInputSchema(body.InputSchema, inputNames)
      // 先展开原 Body 再覆盖，保持键顺序；若循环体 Nodes、Edges 与 InputSchema 均未变化则保持 config 引用不变
      if (sub.graph.Nodes !== rawBody.Nodes || sub.graph.Edges !== rawBody.Edges || schema !== body.InputSchema) {
        config = { ...config, [BODY_INPUT]: { ...(config[BODY_INPUT] as object), Nodes: sub.graph.Nodes, Edges: sub.graph.Edges, ...(schema !== body.InputSchema ? { InputSchema: schema } : {}) } }
      }
      if (Object.keys(sub.layout).length) subLayouts[local] = sub.layout
    } else if (oldLayout.SubLayouts?.[local]) subLayouts[local] = oldLayout.SubLayouts[local]
    const origPos = ctx.originalPosition.get(n.id)
    if (scope) positions[local] = { X: Math.max(BODY_PAD, n.position.x - origin.x), Y: Math.max(BODY_HEADER, n.position.y - origin.y) }
    else if (oldLayout.Positions?.[local] || !origPos || n.position.x !== origPos.x || n.position.y !== origPos.y) positions[local] = { X: n.position.x, Y: n.position.y }
    // 端点所在边原样写回；未写明的端点由保存前的 withEndpointSides 补齐
    if (n.endpointSides && Object.keys(n.endpointSides).length) sides[local] = { ...n.endpointSides }
    // 快速路径：当节点 config 与 inputs 引用与原投影一致（如仅拖拽改变坐标）时，直接复用原节点或原 Inputs，跳过整表序列化对比
    if (old && orig && config === orig.config && n.inputs === orig.inputs) {
      const wantName = old.Name === undefined && n.name === n.type ? undefined : n.name
      const hasInputs = Boolean(old.Inputs && Object.keys(old.Inputs).length > 0)
      if (old.ID === local && old.Kind === n.type && old.Name === wantName && (hasInputs || old.Inputs === undefined)) {
        return old
      }
      return {
        ID: local, Kind: n.type, ...(wantName === undefined ? {} : { Name: wantName }),
        ...(hasInputs ? { Inputs: old.Inputs } : {}),
      }
    }
    const merged = mergeInputs(config, n.inputs)
    const oldMerged = mergeInputs(splitInputs(old?.Inputs).literals, splitInputs(old?.Inputs).bindings)
    const wantName = old?.Name === undefined && n.name === n.type ? undefined : n.name
    const emptyInputs = Object.keys(merged).length === 0 && (!old?.Inputs || Object.keys(old.Inputs).length === 0)
    const keepOldInputs = !emptyInputs && same(merged, oldMerged) && Boolean(old?.Inputs)
    if (old && old.ID === local && old.Kind === n.type && old.Name === wantName && (keepOldInputs || (emptyInputs && old.Inputs === undefined))) {
      return old
    }
    return {
      ID: local, Kind: n.type, ...(wantName === undefined ? {} : { Name: wantName }),
      // Inputs 为空时不写出，与后端 omitempty 一致，避免内容哈希无故变化。
      ...(emptyInputs
        ? {}
        : keepOldInputs
          ? { Inputs: old!.Inputs }
          : Object.keys(merged).length
            ? { Inputs: merged }
            : {}),
    }
  })
  const scopeEdges = (ctx.edgesByScope.get(scope) ?? []).filter((e) => ids.has(e.from) && ids.has(e.to))
  const rails = Object.fromEntries(scopeEdges.filter((e) => e.rail).map((e) => [localIdOf(e.id), e.rail!]))
  const layout: ScopeLayout = {
    ...(Object.keys(positions).length ? { Positions: positions } : {}),
    ...(Object.keys(sides).length ? { EndpointSides: sides } : {}),
    ...(Object.keys(rails).length ? { EdgeRails: rails } : {}),
    ...(Object.keys(subLayouts).length ? { SubLayouts: subLayouts } : {}),
  }
  // 编辑模型 → 端点 ID：From 写成 fromNode.(when||Out)，To 写成 toNode.In（均为本地 ID）；未改动的边与数组保持原引用。
  const mappedEdges = scopeEdges.map(({ id, from, to, when }, idx) => {
    const eid = localIdOf(id)
    const eFrom = formatEndpointID(localIdOf(from), when || GRAPH_DEFAULT_OUTPUT)
    const eTo = formatEndpointID(localIdOf(to), GRAPH_DEFAULT_INPUT)
    const prevEdge = oldGraph.Edges[idx]
    if (prevEdge && prevEdge.ID === eid && prevEdge.From === eFrom && prevEdge.To === eTo) {
      return prevEdge
    }
    return { ID: eid, From: eFrom, To: eTo }
  })
  const sameNodes = nodes.length === oldGraph.Nodes.length && nodes.every((node, i) => node === oldGraph.Nodes[i])
  const sameEdges = mappedEdges.length === oldGraph.Edges.length && mappedEdges.every((edge, i) => edge === oldGraph.Edges[i])
  return {
    graph: {
      Nodes: sameNodes ? oldGraph.Nodes : nodes,
      Edges: sameEdges ? oldGraph.Edges : mappedEdges,
    },
    layout,
  }
}

/**
 * 是否为便签注释框（不进 Graph.Nodes）。编辑模型节点看 type；React Flow 节点另看 data.nodeType。
 * Whether a node is a sticky-note annotation (editor or React Flow node).
 */
export function isAnnotationNode(node: { type?: string; data?: { nodeType?: string } }): boolean {
  return node.type === 'annotation' || node.data?.nodeType === 'annotation'
}

export function annotationEditorNode(note: GraphUIStickyNote): GraphEditorNode {
  return {
    id: note.ID, type: 'annotation', name: note.Title ?? '便签', config: {}, position: { x: note.X, y: note.Y },
    width: note.Width, height: note.Height,
    ...(note.Note !== undefined ? { note: note.Note } : {}),
    ...(note.Color !== undefined ? { color: note.Color } : {}),
    ...(note.Transparent !== undefined ? { transparent: note.Transparent } : {}),
  }
}
