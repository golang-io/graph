import type { Edge, Node } from '@xyflow/react'
import { GRAPH_DEFAULT_INPUT, GRAPH_DEFAULT_OUTPUT, type GraphJSON, type GraphEdgeJSON, type GraphNodeJSON } from './types'
import { mergeInputs } from './inputs-split'
import { isAnnotationNode, projectDocument, type GraphEdgeRail, type GraphLayoutUI, type GraphUIPoint, type GraphUIEndpointSides, type GraphUIStickyNote } from './document'
import { withGraphEdgeStyle } from './edge-style'
import { formatEndpointID, type GraphEndpointSpec } from './endpoints'
import type { RunStatus } from './run-status'
import { isHandleBrandedWhen, parseEdgeRail } from './edge-route'
import { engineWhenFromHandle } from './handle-when'
import { edgeWhenLabel } from './switch-config'
import { parseEndpointSides, type EndpointSides } from './orient'
import { editorNodeSize, rfTypeFor } from './node-size'
import { ANNOTATION_DEFAULT_SIZE, parseAnnotationColor } from './annotation-style'
import { type NodeTypeRegistry } from './node-registry'
import { fitContainers, scopeOf } from './body-scope'

export type RFNodeData = {
  endpointSpec?: GraphEndpointSpec
  label: string
  nodeType: string
  /** 字面量参数视图（由 Inputs 拆出）；供端点展开与表单 */
  config: Record<string, unknown>
  /** 引用绑定视图（由 Inputs 拆出） */
  inputs?: Record<string, import('./types').GraphInputRef>
  /** 运行详情页叠加：running / succeeded / failed */
  runStatus?: RunStatus
  /** merge(all) 当前汇入进度，如 1/2；仅运行叠加使用。 */
  runProgress?: string
  /** 调试断点（会话态，不进流程配置） */
  breakpoint?: boolean
  /** 调试注入（会话态，不进流程配置） */
  inject?: boolean
  /** 端点 ID → 所在边（出口不能与入口同边）；未写明的端点渲染时按 resolveEndpointSides 补齐 */
  endpointSides?: EndpointSides
  /** 注释框顶部说明（仅 annotation） */
  note?: string
  /** 注释框描边色 #rrggbb（仅 annotation；缺省中性灰） */
  color?: string
  /** 背景是否透明（仅 annotation；缺省浅底） */
  transparent?: boolean
  /** 带内嵌子图的节点：expanded 渲染为容器大框，collapsed 渲染为普通卡片（见 GraphEditorNode.body） */
  body?: 'expanded' | 'collapsed'
}

/** 解析后端 graph.UI（大驼峰键）；非法项丢弃。 */
export function parseGraphUI(raw: unknown): GraphLayoutUI {
  if (typeof raw === 'string') {
    try {
      raw = JSON.parse(raw)
    } catch {
      return {}
    }
  }
  if (!raw || typeof raw !== 'object') return {}
  const o = raw as Record<string, unknown>
  return {
    Positions: parsePositions(o.Positions),
    EndpointSides: parseEndpointSidesMap(o.EndpointSides),
    EdgeRails: parseEdgeRails(o.EdgeRails),
    ExecutionMode: parseExecutionMode(o.ExecutionMode),
    StickyNotes: parseUIStickyNotes(o.StickyNotes),
    SubLayouts: parseSubLayouts(o.SubLayouts),
  }
}

/**
 * 解析 UI.SubLayouts：子图层仍是 GraphLayoutUI（graph.Layout），可嵌套。
 * 非法项丢弃，空表返回 undefined。仅在此解析 Collapsed；主图层 parseGraphUI 不读顶层 Collapsed。
 */
function parseSubLayouts(raw: unknown, depth = 0): Record<string, GraphLayoutUI> | undefined {
  if (!raw || typeof raw !== 'object' || depth >= 16) return undefined
  const out: Record<string, GraphLayoutUI> = {}
  for (const [id, v] of Object.entries(raw as Record<string, unknown>)) {
    if (!v || typeof v !== 'object') continue
    const b = v as Record<string, unknown>
    const layout: GraphLayoutUI = {
      ...(b.Collapsed === true ? { Collapsed: true } : {}),
      Positions: parsePositions(b.Positions),
      EndpointSides: parseEndpointSidesMap(b.EndpointSides),
      EdgeRails: parseEdgeRails(b.EdgeRails),
      SubLayouts: parseSubLayouts(b.SubLayouts, depth + 1),
    }
    out[id] = Object.fromEntries(Object.entries(layout).filter(([, x]) => x !== undefined)) as GraphLayoutUI
  }
  return Object.keys(out).length ? out : undefined
}

function parsePositions(raw: unknown): Record<string, GraphUIPoint> | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const out: Record<string, GraphUIPoint> = {}
  for (const [id, v] of Object.entries(raw as Record<string, unknown>)) {
    const p = v as { X?: unknown; Y?: unknown } | null
    const x = Number(p?.X), y = Number(p?.Y)
    if (Number.isFinite(x) && Number.isFinite(y)) out[id] = { X: x, Y: y }
  }
  return Object.keys(out).length ? out : undefined
}

function parseExecutionMode(raw: unknown): 'serial' | 'parallel' | undefined {
  if (raw === 'serial' || raw === 'parallel') return raw
  return undefined
}

function parseUIStickyNotes(raw: unknown): GraphUIStickyNote[] | undefined {
  if (!Array.isArray(raw)) return undefined
  const out: GraphUIStickyNote[] = []
  for (const item of raw) {
    if (!item || typeof item !== 'object') continue
    const s = item as Record<string, unknown>
    const id = typeof s.ID === 'string' ? s.ID.trim() : ''
    if (!id) continue
    const x = Number(s.X)
    const y = Number(s.Y)
    const width = Number(s.Width)
    const height = Number(s.Height)
    if (![x, y, width, height].every((n) => Number.isFinite(n))) continue
    out.push({
      ID: id,
      X: x,
      Y: y,
      Width: Math.max(80, width),
      Height: Math.max(60, height),
      Title: typeof s.Title === 'string' ? s.Title : undefined,
      Note: typeof s.Note === 'string' ? s.Note : undefined,
      Color: parseAnnotationColor(s.Color),
      Transparent: s.Transparent === false ? false : undefined,
    })
  }
  return out.length ? out : undefined
}

function parseEdgeRails(raw: unknown): Record<string, GraphEdgeRail> | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const out: Record<string, GraphEdgeRail> = {}
  for (const [id, v] of Object.entries(raw as Record<string, unknown>)) {
    const rail = parseEdgeRail(v)
    if (rail) out[id] = rail
  }
  return Object.keys(out).length ? out : undefined
}

function parseEndpointSidesMap(raw: unknown): Record<string, GraphUIEndpointSides> | undefined {
  if (!raw || typeof raw !== 'object') return undefined
  const out: Record<string, GraphUIEndpointSides> = {}
  for (const [id, v] of Object.entries(raw as Record<string, unknown>)) {
    // 只保留合法的端点条目；补齐与入出同边由 resolveEndpointSides 按实例端点处理。
    const p = parseEndpointSides(v)
    if (Object.keys(p).length) out[id] = p
  }
  return Object.keys(out).length ? out : undefined
}


/**
 * 后端图 + 布局 → React Flow 节点与边。
 * 展开的 map 循环体节点作为容器的子节点（parentId）输出，坐标换成相对容器左上角，拖动容器时随之移动；
 * 容器尺寸包住全部子节点。
 */
export function graphToFlow(
  graph: GraphJSON,
  ui?: GraphLayoutUI | null,
  registry?: NodeTypeRegistry | null,
): { nodes: Node<RFNodeData>[]; edges: Edge[] } {
  const projected = projectDocument({ Graph: graph, UI: ui ?? undefined })
  const business = projected.nodes.filter((n) => n.type !== 'annotation')
  const byID = new Map(business.map((n) => [n.id, n]))
  const containers = fitContainers(business, (n) => editorNodeSize(n, registry))
  const nodes: Node<RFNodeData>[] = business.map((p) => {
    const kind = rfTypeFor(p.type, registry)
    const { w, h } = containers.get(p.id) ?? editorNodeSize(p, registry)
    const parent = byID.get(scopeOf(p.id))
    return {
      id: p.id,
      type: kind,
      position: parent ? { x: p.position.x - parent.position.x, y: p.position.y - parent.position.y } : p.position,
      ...(parent ? { parentId: parent.id } : {}),
      width: w,
      height: h,
      style: { width: w, height: h },
      data: {
        label: p.name || defaultLabel(p.type),
        nodeType: p.type,
        config: p.config ?? {},
        ...(p.inputs ? { inputs: p.inputs } : {}),
        ...(p.endpointSides && Object.keys(p.endpointSides).length ? { endpointSides: p.endpointSides } : {}),
        ...(p.body ? { body: p.body } : {}),
      },
      draggable: true,
      selectable: true,
    }
  })

  const edges: Edge[] = projected.edges.map((e) => {
    // 分支语义来自 From 端点本地名（默认出口 Out → 无分支标签）。
    const branch = engineWhenFromHandle(e.when ?? GRAPH_DEFAULT_OUTPUT)
    return withGraphEdgeStyle({
      id: e.id,
      source: e.from,
      target: e.to,
      sourceHandle: e.when ?? GRAPH_DEFAULT_OUTPUT,
      targetHandle: GRAPH_DEFAULT_INPUT,
      label: isHandleBrandedWhen(branch) ? undefined : edgeWhenLabel(branch),
      data: { when: branch, ...(e.rail ? { rail: e.rail } : {}) },
      animated: false,
    })
  })
  const annotations = (ui?.StickyNotes ?? []).map((s) => annotationNodeFromStickyNote(s))
  return { nodes: [...annotations, ...nodes], edges }
}

/** UI.StickyNotes → RF 便签注释框节点（底层、无连接） */
export function annotationNodeFromStickyNote(s: GraphUIStickyNote): Node<RFNodeData> {
  return {
    id: s.ID,
    type: 'annotation',
    position: { x: s.X, y: s.Y },
    // 同时写 width/height 与 style：RF 把前者传给 NodeProps；后者管 DOM
    width: s.Width,
    height: s.Height,
    style: { width: s.Width, height: s.Height },
    zIndex: -1,
    connectable: false,
    data: {
      label: s.Title?.trim() || '便签',
      note: s.Note ?? '',
      color: s.Color,
      transparent: s.Transparent !== false,
      nodeType: 'annotation',
      config: {},
    },
    draggable: true,
    selectable: true,
  }
}

/** React Flow 节点与边 → 后端图 + 布局。 */
export function flowToGraph(
  nodes: Node<RFNodeData>[],
  edges: Edge[],
  extraUI?: Pick<GraphLayoutUI, 'ExecutionMode'>,
): { graph: GraphJSON; ui: GraphLayoutUI } {
  const flowNodes = nodes.filter((n) => !isAnnotationNode(n))
  const annIds = new Set(nodes.filter((n) => isAnnotationNode(n)).map((n) => n.id))
  const gNodes: GraphNodeJSON[] = flowNodes.map((n) => {
    const inputs = mergeInputs(n.data.config, (n.data as RFNodeData).inputs)
    return {
      ID: n.id,
      Kind: n.data.nodeType,
      Name: n.data.label,
      ...(Object.keys(inputs).length ? { Inputs: inputs } : {}),
    }
  })
  const gEdges: GraphEdgeJSON[] = edges
    .filter((e) => !annIds.has(e.source) && !annIds.has(e.target))
    .map((e) => {
      // RF 句柄即端点本地名：sourceHandle 缺省 Out，targetHandle 缺省 In。
      const outName =
        engineWhenFromHandle(e.sourceHandle) ||
        engineWhenFromHandle((e.data as { when?: string } | undefined)?.when) ||
        (typeof e.label === 'string' && (e.label === 'IF' || e.label === 'true')
          ? 'true'
          : typeof e.label === 'string' && (e.label === 'ELSE' || e.label === 'false')
            ? 'false'
            : undefined) ||
        GRAPH_DEFAULT_OUTPUT
      const inName = typeof e.targetHandle === 'string' && e.targetHandle.trim() ? e.targetHandle.trim() : GRAPH_DEFAULT_INPUT
      return { ID: e.id, From: formatEndpointID(e.source, outName), To: formatEndpointID(e.target, inName) }
    })
  const positions: Record<string, GraphUIPoint> = {}
  const sides: Record<string, GraphUIEndpointSides> = {}
  const edgeRails: Record<string, GraphEdgeRail> = {}
  for (const n of flowNodes) {
    positions[n.id] = { X: n.position.x, Y: n.position.y }
    // 端点所在边原样写出；未写明的端点由保存前的 withEndpointSides 按注册表补齐
    if (n.data.endpointSides && Object.keys(n.data.endpointSides).length) sides[n.id] = { ...n.data.endpointSides }
  }
  for (const e of edges) {
    if (annIds.has(e.source) || annIds.has(e.target)) continue
    const rail = parseEdgeRail((e.data as { rail?: string } | undefined)?.rail)
    if (rail) edgeRails[e.id] = rail
  }
  const stickyNotes: GraphUIStickyNote[] = nodes.filter(isAnnotationNode).map((n) => {
    const w = n.width || Number(n.style?.width) || n.measured?.width || ANNOTATION_DEFAULT_SIZE.w
    const h = n.height || Number(n.style?.height) || n.measured?.height || ANNOTATION_DEFAULT_SIZE.h
    return {
      ID: n.id,
      X: n.position.x,
      Y: n.position.y,
      Width: w,
      Height: h,
      Title: n.data.label || undefined,
      Note: n.data.note || undefined,
      Color: parseAnnotationColor(n.data.color),
      ...(n.data.transparent === false ? { Transparent: false } : {}),
    }
  })
  return {
    graph: { Nodes: gNodes, Edges: gEdges },
    ui: {
      Positions: positions,
      ...(Object.keys(sides).length ? { EndpointSides: sides } : {}),
      ...(Object.keys(edgeRails).length ? { EdgeRails: edgeRails } : {}),
      ...(stickyNotes.length ? { StickyNotes: stickyNotes } : {}),
      ...(extraUI?.ExecutionMode ? { ExecutionMode: extraUI.ExecutionMode } : {}),
    },
  }
}

function defaultLabel(type: string): string {
  switch (type) {
    case 'end':
      return '结束'
    case 'switch':
      return '条件分支'
    case 'merge':
      return '汇合'
    case 'parallel':
      return '并行'
    case 'map':
      return '遍历'
    case 'reduce':
      return '归约'
    case 'stop_error':
      return '停止并报错'
    case 'subgraph':
      return '子图'
    case 'code':
      return '表达式'
    case 'template':
      return '模板'
    case 'http':
      return 'HTTP'
    case 'llm':
      return '智能体'
    default:
      return type
  }
}

