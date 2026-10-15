import { Graph, Shape, type Cell, type EdgeMetadata, type NodeMetadata } from '@antv/x6'
import type { GraphEditorNode, GraphEditorEdge } from './document'
import { isAnnotationNode } from './document'
import type { NodeTypeRegistry } from './node-registry'
import { catalogKind, resolveStepAppearance } from './node-registry'
import { isGraphDarkMode, RUN_STATUS_COLOR, runStatusBorderStyle, GRAPH_EDGE_STROKE_WIDTH, GRAPH_RUN_STROKE_WIDTH } from './edge-style'
import {
  resolveGraphNodeEndpoints,
  type GraphEndpointSet,
  type GraphEndpointSpec,
} from './endpoints'
import { branchLabelStyle, breakpointMarkPositionCls } from './GraphNodes'
import { editorNodeSize, ENDPOINT_SHAPE_CLASS } from './node-size'
import { endpointIds, endpointSide, resolveEndpointSides, type EndpointSides, type Side } from './orient'
import {
  ANNOTATION_DEFAULT_SIZE,
  annotationColorRgba,
  normalizeAnnotationColor,
} from './annotation-style'
import { BODY_HEADER, SCOPE_SEP, bodyGraphOf } from './body-scope'
import { switchSummary } from './switch-config'
import { NODE_SHAPE_ANNOTATION, ROUTER_GRAPH_EDGE, graphEdgeRouter } from './x6-router'
import { GRAPH_DEFAULT_INPUT, GRAPH_DEFAULT_OUTPUT, GRAPH_ENDPOINT_ERROR } from './types'

export { ROUTER_GRAPH_EDGE }

const NODE_SHAPE_GRAPH = 'graph-node'

let isRegistered = false

// 内置 SVG 图标字典（避免 React DOM 挂载开销与外部包依赖）
const ICONS: Record<string, string> = {
  start: '<circle cx="12" cy="12" r="10"/><polygon points="10 8 16 12 10 16 10 8"/>',
  end: '<circle cx="12" cy="12" r="10"/><rect width="6" height="6" x="9" y="9"/>',
  switch: '<line x1="6" x2="6" y1="3" y2="15"/><circle cx="18" cy="6" r="3"/><circle cx="6" cy="18" r="3"/><path d="M18 9a9 9 0 0 1-9 9"/>',
  merge: '<circle cx="18" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><path d="M6 21V9a9 9 0 0 0 9 9"/>',
  parallel: '<circle cx="12" cy="18" r="3"/><circle cx="6" cy="6" r="3"/><circle cx="18" cy="6" r="3"/><path d="M18 9v2a3 3 0 0 1-3 3H9a3 3 0 0 1-3-3V9"/><line x1="12" x2="12" y1="12" y2="15"/>',
  map: '<path d="m17 2 4 4-4 4"/><path d="M3 11v-1a4 4 0 0 1 4-4h14"/><path d="m7 22-4-4 4-4"/><path d="M21 13v1a4 4 0 0 1-4 4H3"/>',
  reduce: '<path d="M18 20V10"/><path d="M12 20V4"/><path d="M6 20v-6"/>',
  template: '<path d="M8 3H7a2 2 0 0 0-2 2v5a2 2 0 0 1-2 2 2 2 0 0 1 2 2v5c0 1.1.9 2 2 2h1"/><path d="M16 21h1a2 2 0 0 0 2-2v-5c0-1.1.9-2 2-2a2 2 0 0 1-2-2V5a2 2 0 0 0-2-2h-1"/>',
  code: '<polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/>',
  llm: '<path d="m12 3-1.9 5.8a2 2 0 0 1-1.3 1.3L3 12l5.8 1.9a2 2 0 0 1 1.3 1.3L12 21l1.9-5.8a2 2 0 0 1 1.3-1.3L21 12l-5.8-1.9a2 2 0 0 1-1.3-1.3Z"/><path d="M5 3v4"/><path d="M19 17v4"/><path d="M3 5h4"/><path d="M17 19h4"/>',
}

function renderSvgIcon(nodeType: string): string {
  const content = ICONS[catalogKind(nodeType)] || ICONS[nodeType] || ICONS.code
  return `<svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${content}</svg>`
}

function getNodeHint(nodeType: string, config: Record<string, unknown> | undefined, endpointSet: GraphEndpointSet, body?: unknown): string {
  const kind = catalogKind(nodeType)
  if (kind === 'start') return '控制入口'
  if (kind === 'end') return '输出终点'
  if (kind === 'switch') return switchSummary(config ?? {})
  if (kind === 'merge') {
    const mode = String(config?.Mode || 'any')
    return `${mode} · ${endpointSet.inputs.length} 入`
  }
  if (kind === 'parallel') {
    return `${endpointSet.outputs.length} 路出边`
  }
  if (kind === 'map') {
    // 列表来自输入槽 List（Node.Inputs）；收起时提示循环体规模
    const maxP = Number(config?.MaxParallel ?? 1) || 1
    const mode = maxP > 1 ? `并发×${maxP}` : '串行'
    return body === 'collapsed' ? `循环体 ${bodyGraphOf(config)?.Nodes.length ?? 0} 个节点 · ${mode}` : `List → Item · ${mode}`
  }
  if (kind === 'reduce') {
    // 输入槽 List 折叠为固定输出 Result
    return 'List → Result'
  }
  if (kind === 'template') {
    return '渲染文本并输出'
  }
  if (kind === 'code') {
    return '执行脚本'
  }
  if (kind === 'llm' || kind === 'agent') {
    return '大模型智能体'
  }
  if (config?.title) return String(config.title)
  return nodeType
}

/**
 * 比较业务字段是否变化（忽略 selected）。
 * X6 HTML 节点默认 data 一变就 empty+重建 DOM；选中态若走这条路径，双击第二下会打在空白上。
 * Compare payload without `selected` so selection styling can toggle in-place.
 */
export function isX6NodePayloadChanged(
  prev: any,
  next: any,
): boolean {
  if (!prev) return true
  const prevLabel = prev.name ?? prev.label
  const nextLabel = next.name ?? next.label
  const prevType = prev.type ?? prev.nodeType
  const nextType = next.type ?? next.nodeType
  return (
    prevLabel !== nextLabel ||
    prevType !== nextType ||
    prev.note !== next.note ||
    prev.color !== next.color ||
    prev.transparent !== next.transparent ||
    (prev.config !== next.config && JSON.stringify(prev.config) !== JSON.stringify(next.config)) ||
    prev.registry !== next.registry ||
    prev.body !== next.body || prev.bodyToggle !== next.bodyToggle
  )
}

/**
 * 只切换 HTML 根节点的选中 class，不触发 X6 html() 重建。
 * Toggle selected class on the existing HTML root; do not remount the foreignObject.
 */
export function applyX6HtmlSelected(graph: Graph, cell: Cell, selected: boolean) {
  if (cell.shape === NODE_SHAPE_ANNOTATION) return
  const root = x6NodeShell(graph, cell)
  if (!root) return
  root.classList.toggle('graph-node-shell-selected', selected)
  if (root.classList.contains('graph-agent-node')) {
    root.classList.toggle('graph-agent-node-selected', selected)
  }
}

function x6NodeShell(graph: Graph, cell: Cell): HTMLElement | null {
  const view = graph.findViewByCell(cell)
  return (view?.container.querySelector('.graph-node-shell, .graph-agent-node') as HTMLElement | null) ?? null
}

/**
 * 就地刷运行态边框 / 进度 / 失败重试，不重建 HTML（与选中态同一策略）。
 * Paint run appearance in-place; remounting would swallow dblclick like selection did.
 */
export function applyX6HtmlRunStatus(graph: Graph, cell: Cell, data: any) {
  if (cell.shape === NODE_SHAPE_ANNOTATION) return
  const root = x6NodeShell(graph, cell)
  if (!root) return
  const cellData = cell.getData<Record<string, unknown>>() || {}
  paintX6NodeRunAppearance(root, {
    ...cellData,
    ...data,
    endpointSides: data?.endpointSides ?? cellData.endpointSides,
  })
}

function paintX6NodeRunAppearance(root: HTMLElement, data: any) {
  const rs = data.runStatus
  root.classList.toggle('graph-node-running', rs === 'running')
  const box = runStatusBorderStyle(rs)
  if (box) {
    root.style.borderColor = box.borderColor
    root.style.borderWidth = `${box.borderWidth}px`
  } else {
    root.style.borderColor = ''
    root.style.borderWidth = ''
  }

  const titleEl = root.querySelector('.truncate.text-sm') as HTMLElement | null
  if (titleEl) {
    const label = data.name ?? data.label ?? '节点'
    titleEl.textContent = data.runProgress ? `${label} (${data.runProgress})` : label
  }

  // 断点徽标（节点入口处）与调试注入黄点（左上角）：就地增删，与 React Flow 渲染器的 NodeDebugMarks 一致
  const rawSides = data.endpointSides as EndpointSides | undefined
  const kind = catalogKind(String(data.type || data.nodeType || 'step'))
  const inSide: Side =
    kind === 'start'
      ? endpointSide(rawSides, GRAPH_DEFAULT_OUTPUT, 'out') === 'left'
        ? 'right'
        : 'left'
      : endpointSide(rawSides, GRAPH_DEFAULT_INPUT, 'in')
  root.closest('.x6-node')?.classList.toggle('x6-node-has-bp', Boolean(data.breakpoint))
  let bpEl = root.querySelector('[data-debug-bp]') as HTMLElement | null
  if (data.breakpoint) {
    if (!bpEl) {
      bpEl = document.createElement('span')
      bpEl.dataset.debugBp = '1'
      bpEl.title = 'breakpoint'
      root.appendChild(bpEl)
    }
    bpEl.className = `graph-debug-bp pointer-events-none absolute z-20 ${breakpointMarkPositionCls(inSide)}`
  } else if (bpEl) {
    bpEl.remove()
  }

  let injEl = root.querySelector('[data-debug-inj]') as HTMLElement | null
  if (data.inject) {
    if (!injEl) {
      injEl = document.createElement('span')
      injEl.dataset.debugInj = '1'
      injEl.title = 'debug inject'
      injEl.className = 'pointer-events-none absolute -left-1.5 -top-1.5 z-10 h-3.5 w-3.5 rounded-full bg-amber-500 ring-2 ring-background shadow-sm'
      root.appendChild(injEl)
    }
  } else if (injEl) {
    injEl.remove()
  }
}

/** Overlay 已写入 RF edge.style / data.runStatus；X6 只投影，不重算。 */
export function x6EdgeLineAttrs(e: any) {
  const runStatus = e.runStatus ?? e.data?.runStatus
  const fallback = isGraphDarkMode() ? '#71717a' : '#94a3b8'
  const stroke = runStatus ? RUN_STATUS_COLOR[runStatus as keyof typeof RUN_STATUS_COLOR] : ((e.style?.stroke as string | undefined) ?? fallback)
  const strokeWidth = runStatus
    ? GRAPH_RUN_STROKE_WIDTH
    : Number(e.style?.strokeWidth) || GRAPH_EDGE_STROKE_WIDTH
  return {
    stroke,
    strokeWidth,
    strokeDasharray: runStatus === 'running' ? '8 4' : undefined,
    targetMarker: {
      name: 'classic',
      size: 7,
      fill: stroke,
      stroke,
    },
  }
}

export function applyX6EdgeRunStyle(edge: { attr: (path: string, value: unknown) => unknown }, e: any) {
  const line = x6EdgeLineAttrs(e)
  edge.attr('line/stroke', line.stroke)
  edge.attr('line/strokeWidth', line.strokeWidth)
  edge.attr('line/strokeDasharray', line.strokeDasharray ?? '')
  edge.attr('line/targetMarker/fill', line.stroke)
  edge.attr('line/targetMarker/stroke', line.stroke)
}


export function ensureX6NodeRegistered() {
  if (isRegistered) return
  isRegistered = true

  // 1. 注册图连线专用的正交避障与水平入桩路由
  Graph.registerRouter(ROUTER_GRAPH_EDGE, graphEdgeRouter, true)

  // 2. 图节点卡片
  // effect 仅监听 data：拖拽 position/size 时不要重建 HTML，否则会吞掉 dblclick。
  Shape.HTML.register({
    shape: NODE_SHAPE_GRAPH,
    effect: ['data'],
    html(cell: Cell) {
      const data = cell.getData<Record<string, unknown>>() || {}
      const nodeType = String(data.type || data.nodeType || 'step')
      const kind = catalogKind(nodeType)
      const label = String(data.name || data.label || '节点')
      const config = (data.config as Record<string, unknown>) || {}, registry = data.registry as NodeTypeRegistry | undefined
      const isAgent = kind === 'llm' || kind === 'agent'
      const isSelected = Boolean(data.selected)

      const entry = registry?.get(nodeType)?.spec
      const endpointSpec = (data.endpointSpec as GraphEndpointSpec | undefined) ?? entry?.Endpoints
      // data.endpointSides 已由 editorNodeToX6 按实例端点补齐；这里按同一组端点再规整一次（结果不变）
      const endpointSet = resolveGraphNodeEndpoints(nodeType, endpointSpec, config, entry?.Renderer)
      const directions = resolveEndpointSides(data.endpointSides as EndpointSides | undefined, endpointIds(endpointSet))
      const appearance = resolveStepAppearance(nodeType, config, registry)
      // 带循环体的节点优先显示循环体摘要（与 React Flow 渲染器的卡片副标题一致）
      const hint = (data.body ? '' : appearance.hint) || getNodeHint(nodeType, config, endpointSet, data.body)

      const body = data.body as 'expanded' | 'collapsed' | undefined
      const container = document.createElement('div')
      container.className = body === 'expanded'
        // 展开的容器：大框 + 顶部标题栏，循环体节点作为嵌入的子节点画在框内
        ? `relative w-full h-full select-none rounded-2xl border-2 border-fuchsia-500/30 bg-fuchsia-500/[0.03] dark:border-fuchsia-400/30 dark:bg-fuchsia-400/[0.05] graph-node-shell ${
          isSelected ? 'graph-node-shell-selected ring-2 ring-[#8b5cf6]' : ''
        }`
        : `relative w-full h-full select-none flex items-center gap-2.5 px-3.5 py-2 ${
          kind === 'start' ? ENDPOINT_SHAPE_CLASS.start : kind === 'end' ? ENDPOINT_SHAPE_CLASS.end : 'rounded-xl'
        } ${isAgent ? 'graph-agent-node' : 'graph-node-shell'} ${
          isSelected ? 'graph-node-shell-selected ring-2 ring-[#8b5cf6]' : ''
        }`

      // 图标区
      const iconWrap = document.createElement('div')
      iconWrap.className = `flex h-9 w-9 shrink-0 items-center justify-center rounded-xl font-bold text-xs ${
        kind === 'start'
          ? 'bg-cyan-500/12 text-cyan-600 ring-1 ring-cyan-500/25 dark:text-cyan-400'
          : kind === 'end'
          ? 'bg-slate-500/12 text-slate-600 ring-1 ring-slate-500/25 dark:text-slate-400'
          : kind === 'switch'
          ? 'bg-amber-500/15 text-amber-600 ring-1 ring-amber-500/30 dark:text-amber-400'
          : kind === 'merge'
          ? 'bg-teal-500/15 text-teal-600 ring-1 ring-teal-500/30 dark:text-teal-400'
          : kind === 'parallel'
          ? 'bg-indigo-500/15 text-indigo-600 ring-1 ring-indigo-500/30 dark:text-indigo-400'
          : kind === 'map'
          ? 'bg-fuchsia-500/15 text-fuchsia-600 ring-1 ring-fuchsia-500/30 dark:text-fuchsia-400'
          : isAgent
          ? 'bg-cyan-500/15 text-cyan-600 ring-1 ring-cyan-400/50 dark:text-cyan-300'
          : 'bg-primary/10 text-primary ring-1 ring-primary/20'
      }`
      iconWrap.innerHTML = renderSvgIcon(nodeType)

      // 文本区
      const content = document.createElement('div')
      content.className = 'min-w-0 flex-1'

      const titleEl = document.createElement('div')
      titleEl.className = 'truncate text-sm font-semibold'
      titleEl.textContent = label

      const hintEl = document.createElement('div')
      hintEl.className = 'truncate text-[11px] text-muted-foreground'
      hintEl.textContent = hint

      content.appendChild(titleEl)
      content.appendChild(hintEl)

      if (body === 'expanded') {
        const header = document.createElement('div')
        header.className = 'flex items-center gap-2.5 border-b border-fuchsia-500/20 px-3 dark:border-fuchsia-400/20'
        header.style.height = `${BODY_HEADER - 8}px`
        header.appendChild(iconWrap)
        header.appendChild(content)
        if (data.bodyToggle) header.appendChild(bodyToggleButton(true))
        container.appendChild(header)
      } else {
        container.appendChild(iconWrap)
        container.appendChild(content)
        if (body && data.bodyToggle) container.appendChild(bodyToggleButton(false))
      }
      paintX6NodeRunAppearance(container, data)

      // 多出口徽标（IF/ELSE、OUT1/OUT2 等）：按出口实际所在边分组，与 X6 port 分组一致
      if (endpointSet.outputs.length > 1) {
        const sides = outputSides(endpointSet, directions)
        const sideCount = new Map<Side, number>(), sideIndex = new Map<Side, number>()
        for (const side of sides.values()) sideCount.set(side, (sideCount.get(side) ?? 0) + 1)
        endpointSet.outputs.forEach((out) => {
          const side = sides.get(out.id)!, i = sideIndex.get(side) ?? 0
          sideIndex.set(side, i + 1)
          const pct = ((i + 1) / (sideCount.get(side)! + 1)) * 100
          const badge = document.createElement('span')
          const isMuted = Boolean(out.fallback)
          badge.className = `pointer-events-none absolute select-none rounded-full px-1.5 py-0.5 text-[9px] font-semibold tracking-wide shadow-2xs ${
            isMuted
              ? 'bg-rose-500/12 text-rose-700 ring-1 ring-rose-500/25 dark:bg-rose-500/20 dark:text-rose-300'
              : kind === 'parallel'
              ? 'bg-indigo-500/12 text-indigo-700 ring-1 ring-indigo-500/25 dark:bg-indigo-500/20 dark:text-indigo-300'
              : 'bg-emerald-500/12 text-emerald-700 ring-1 ring-emerald-500/25 dark:bg-emerald-500/20 dark:text-emerald-300'
          }`
          for (const [key, value] of Object.entries(branchLabelStyle(side, pct))) badge.style.setProperty(key, typeof value === 'number' ? `${value}px` : String(value))
          badge.textContent = out.label
          container.appendChild(badge)
        })
      }

      // 多入口徽标（IN1/IN2 等）
      if (endpointSet.inputs.length > 1) {
        endpointSet.inputs.forEach((inp, i) => {
          const pct = ((i + 1) / (endpointSet.inputs.length + 1)) * 100
          const badge = document.createElement('span')
          badge.className = 'pointer-events-none absolute select-none rounded-full bg-teal-500/12 px-1.5 py-0.5 text-[9px] font-semibold tracking-wide text-teal-700 shadow-2xs ring-1 ring-teal-500/25 dark:bg-teal-500/20 dark:text-teal-300'
          for (const [key, value] of Object.entries(branchLabelStyle(endpointSide(directions, inp.id, 'in'), pct))) badge.style.setProperty(key, typeof value === 'number' ? `${value}px` : String(value))
          badge.textContent = inp.label
          container.appendChild(badge)
        })
      }

      return container
    },
  })

  // 3. 注释框节点（底层虚线大框）
  Shape.HTML.register({
    shape: NODE_SHAPE_ANNOTATION,
    effect: ['data'],
    html(cell: Cell) {
      const data = cell.getData<Record<string, unknown>>() || {}
      const title = String(data.name || data.label || '便签').trim()
      const note = String(data.note ?? '').trim()
      const color = normalizeAnnotationColor(data.color as string | undefined)
      const transparent = data.transparent !== false
      const borderColor = annotationColorRgba(color, 0.72)
      const headerBorder = annotationColorRgba(color, 0.45)
      const fill = transparent ? annotationColorRgba(color, 0.06) : 'hsl(var(--card))'

      const container = document.createElement('div')
      container.className = `box-border flex h-full w-full flex-col rounded-lg border-2 select-none overflow-hidden ${
        transparent ? 'border-dashed' : 'border-solid shadow-sm'
      }`
      container.style.borderColor = borderColor
      container.style.backgroundColor = fill
      container.style.pointerEvents = 'none'

      const header = document.createElement('div')
      header.className = `shrink-0 border-b px-2.5 py-1.5 ${
        transparent ? 'border-dashed' : 'border-solid'
      }`
      header.style.borderColor = headerBorder

      const titleEl = document.createElement('div')
      titleEl.className = 'truncate text-xs font-semibold tracking-wide'
      titleEl.style.color = color
      titleEl.textContent = title
      header.appendChild(titleEl)
      container.appendChild(header)

      if (note) {
        const body = document.createElement('div')
        body.className = 'min-h-0 flex-1 px-2.5 py-1.5 text-[11px] text-muted-foreground whitespace-pre-wrap overflow-hidden leading-relaxed'
        body.textContent = note
        container.appendChild(body)
      }

      return container
    },
  })
}

/** 各出口所在边：直接取补齐后的 endpointSides（与 React Flow 渲染器的 OutputHandles 一致） */
function outputSides(endpointSet: GraphEndpointSet, sides: EndpointSides): Map<string, Side> {
  return new Map(endpointSet.outputs.map((out) => [out.id, endpointSide(sides, out.id, 'out')]))
}

/** X6 port 分组（渲染器内部命名，不是端点 ID）：第一个出口所在边上的普通出口用 out；其它边用 out-<side>；Error 出口用 error-<side>（红色） */
function outputGroup(portId: string, side: Side, mainSide: Side): string {
  if (portId === GRAPH_ENDPOINT_ERROR) return `error-${side}`
  return side === mainSide ? 'out' : `out-${side}`
}

function portCircle(stroke: string, fill: string) {
  return { circle: { r: 4, magnet: true, stroke, strokeWidth: 1.5, fill } }
}

/** 容器展开 / 收起按钮（HTML 节点内）；X6Canvas 在 node:click 中按 data-body-toggle 识别。 */
function bodyToggleButton(expanded: boolean): HTMLElement {
  const btn = document.createElement('button')
  btn.type = 'button'
  btn.dataset.bodyToggle = '1'
  btn.title = expanded ? '收起为节点' : '展开循环体'
  btn.className = 'ml-auto flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground'
  btn.innerHTML = expanded
    ? '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m14 10 7-7"/><path d="M20 10h-6V4"/><path d="m3 21 7-7"/><path d="M4 14h6v6"/></svg>'
    : '<svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 3h6v6"/><path d="m21 3-7 7"/><path d="m3 21 7-7"/><path d="M9 21H3v-6"/></svg>'
  return btn
}

/**
 * 编辑模型节点 → X6 节点。每个端点句柄的所在边取节点的 endpointSides，未写明的端点按 resolveEndpointSides 补齐。
 * size 为展开容器的尺寸（fitContainers 计算）；bodyToggle 表示显示容器的展开 / 收起按钮。
 * 层级：容器在边之下、卡片在边之上，嵌套越深越靠上，循环体内的边和卡片不会被外层容器遮住。
 */
export function editorNodeToX6(n: GraphEditorNode, registry?: NodeTypeRegistry, size?: { w: number; h: number }, bodyToggle?: boolean): NodeMetadata {
  if (isAnnotationNode(n)) {
    const w = n.width ?? (Number(n.style?.width) || ANNOTATION_DEFAULT_SIZE.w)
    const h = n.height ?? (Number(n.style?.height) || ANNOTATION_DEFAULT_SIZE.h)
    return {
      id: n.id,
      shape: NODE_SHAPE_ANNOTATION,
      x: n.position.x,
      y: n.position.y,
      width: Number(w),
      height: Number(h),
      zIndex: -10,
      data: n,
      ports: { items: [] },
    }
  }

  const nodeType = n.type || 'step'
  const { w: width, h: height } = size ?? editorNodeSize(n, registry)
  const depth = n.id.split(SCOPE_SEP).length - 1

  const spec = registry?.get(nodeType)?.spec
  const endpointSet = resolveGraphNodeEndpoints(nodeType, spec?.Endpoints, n.config, spec?.Renderer)

  // 按实例端点补齐所在边：未写明的端点入口左、出口右（另有其他出口时 Error 下），失效条目丢弃
  const directions = resolveEndpointSides(n.endpointSides, endpointIds(endpointSet))
  // 第一个入口 / 出口所在边作为 in / out 分组的位置；没有入口（start）或出口（end）时按缺省边
  const inSide = endpointSide(directions, endpointSet.inputs[0]?.id ?? GRAPH_DEFAULT_INPUT, 'in')
  const outSide = endpointSide(directions, endpointSet.outputs[0]?.id ?? GRAPH_DEFAULT_OUTPUT, 'out')
  const inAttrs = portCircle('#94a3b8', isGraphDarkMode() ? '#1e293b' : '#ffffff')
  // X6 在同一分组内沿边均匀排布 port，因此每条边一个分组即可得到与 React Flow 一致的位置
  const groups: Record<string, { position: Side; attrs: ReturnType<typeof portCircle> }> = {
    in: { position: inSide, attrs: inAttrs },
    out: { position: outSide, attrs: portCircle('#94a3b8', '#94a3b8') },
  }
  const portItems = []
  for (const inp of endpointSet.inputs) {
    const side = endpointSide(directions, inp.id, 'in')
    const group = side === inSide ? 'in' : `in-${side}`
    groups[group] ??= { position: side, attrs: inAttrs }
    portItems.push({ id: inp.id, group })
  }
  for (const [id, side] of outputSides(endpointSet, directions)) {
    const group = outputGroup(id, side, outSide)
    groups[group] ??= { position: side, attrs: id === GRAPH_ENDPOINT_ERROR ? portCircle('#f43f5e', '#f43f5e') : portCircle('#94a3b8', '#94a3b8') }
    portItems.push({ id, group })
  }

  return {
    id: n.id,
    shape: NODE_SHAPE_GRAPH,
    x: n.position.x,
    y: n.position.y,
    width,
    height,
    zIndex: n.body === 'expanded' ? 1 + Math.min(depth, 3) : 10 + depth,
    // HTML 徽标按 data.endpointSides 计算，这里写入补齐后的端点所在边
    data: {...n, endpointSides: directions, registry, ...(bodyToggle && n.body ? { bodyToggle: true } : {})},
    ports: { groups, items: portItems },
  }
}

export function editorEdgeToX6(e: GraphEditorEdge, handles?: {sourceHandle?: string | null; targetHandle?: string | null}): EdgeMetadata & {source: {cell: string; port: string}; target: {cell: string; port: string}} {
  const dark = isGraphDarkMode()
  const line = x6EdgeLineAttrs(e)
  const sourcePort = handles?.sourceHandle || e.when || GRAPH_DEFAULT_OUTPUT
  const targetPort = handles?.targetHandle || GRAPH_DEFAULT_INPUT

  return {
    id: e.id,
    source: {
      cell: e.from,
      port: sourcePort,
    },
    target: {
      cell: e.to,
      port: targetPort,
    },
    zIndex: 5,
    // 回边轨道偏好由路由函数读取；同时记录 runStatus 供增量比对
    data: {
      ...(e.rail ? { rail: e.rail } : {}),
      ...(e.runStatus ? { runStatus: e.runStatus } : {}),
    },
    router: {
      name: ROUTER_GRAPH_EDGE,
    },
    connector: {
      name: 'rounded',
      args: { radius: 16 },
    },
    attrs: {
      line,
    },
    labels: e.label
      ? [
          {
            attrs: {
              text: {
                text: String(e.label),
                fontSize: 10,
                fill: dark ? '#e4e4e7' : '#27272a',
              },
              rect: {
                fill: dark ? 'rgba(24, 24, 27, 0.9)' : 'rgba(255, 255, 255, 0.9)',
                stroke: dark ? '#3f3f46' : '#e4e4e7',
                rx: 4,
                ry: 4,
              },
            },
          },
        ]
      : undefined,
  }
}
