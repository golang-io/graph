import { useEffect, useMemo, useRef, useState, type CSSProperties, type MouseEvent, type ReactNode } from 'react'
import { Graph, Selection, Transform, MiniMap, NodeView } from '@antv/x6'
import { Maximize2, Minus, Plus } from 'lucide-react'
import { GRAPH_DEFAULT_INPUT, GRAPH_DEFAULT_OUTPUT, type GraphUI } from './types'
import type { GraphNodePack } from './node-pack'
import { projectDocument, documentFromProjection, type GraphEditorNode, type GraphEditorEdge, type GraphViewState } from './document'
import type { GraphCanvasConfig } from './entry-config'
import { isGraphDarkMode } from './edge-style'
import { connectDocument } from './canvas-edit'
import type { GraphCanvasInteraction } from './GraphCanvas'
import {
  applyX6HtmlSelected,
  applyX6HtmlRunStatus,
  applyX6EdgeRunStyle,
  ensureX6NodeRegistered,
  isX6NodePayloadChanged,
  editorEdgeToX6,
  editorNodeToX6,
  ROUTER_GRAPH_EDGE,
} from './x6-adapter'
import { cn } from './cn'
import type { ViewportMatrix } from './viewport-memory'
import { fitContainers, scopeOf } from './body-scope'
import { editorNodeSize } from './node-size'
import { useThemeVersion } from './theme-version'

type CanvasEvent = MouseEvent | globalThis.MouseEvent

/**
 * MiniMap 专用轻量节点视图：在 160×100 的缩略图中只渲染底层 <rect selector="body">，
 * 跳过 HTML foreignObject 挂载与端口圆点渲染，避免在千节点大图下把整套 HTML 卡片重复挂载两遍。
 * Lightweight NodeView for MiniMap: renders only the body rect, skipping HTML foreignObject and ports.
 */
class MiniMapNodeView extends NodeView {
  protected renderPorts() {}
  confirmUpdate(flag: number) {
    const ret = super.confirmUpdate(flag)
    const body = this.selectors?.body as SVGElement | undefined
    if (body) {
      const data = this.cell.getData<GraphEditorNode | undefined>()
      const rs = data?.runStatus
      const fill =
        rs === 'succeeded'
          ? '#10b981'
          : rs === 'failed'
            ? '#f43f5e'
            : rs === 'running'
              ? '#6366f1'
              : data?.type === 'annotation'
                ? 'rgba(148, 163, 184, 0.14)'
                : 'rgba(148, 163, 184, 0.55)'
      body.setAttribute('fill', fill)
      body.setAttribute('rx', '6')
      body.setAttribute('ry', '6')
    }
    return ret
  }
}

/** 浅比较两个端点方位表，避免每次视图状态变化时对全部节点调用 JSON.stringify。 */
function sameEndpointSidesMap(a?: Record<string, string>, b?: Record<string, string>): boolean {
  if (a === b) return true
  if (!a || !b) return !a && !b
  const ka = Object.keys(a)
  const kb = Object.keys(b)
  if (ka.length !== kb.length) return false
  for (const k of ka) {
    if (a[k] !== b[k]) return false
  }
  return true
}

export type X6CanvasProps = {
  interaction?: GraphCanvasInteraction
  active?: boolean
  document: GraphUI
  nodePack: GraphNodePack
  view?: GraphViewState
  onChange?: (document: GraphUI) => void
  onViewChange?: (view: GraphViewState) => void
  onBeforeEdit?: () => void
  onNodeClick?: (event: CanvasEvent, node: GraphEditorNode) => void
  onNodeDoubleClick?: (event: CanvasEvent, node: GraphEditorNode) => void
  onEdgeClick?: (event: CanvasEvent, edge: GraphEditorEdge) => void
  onPaneClick?: (event: CanvasEvent) => void
  /** 容器（map 循环体）展开 / 收起；collapsed 为目标状态。缺省时不显示按钮 */
  onToggleBody?: (id: string, collapsed: boolean) => void
  canvas: GraphCanvasConfig
  className?: string
  style?: CSSProperties
  children?: ReactNode
  /** 切页签回来时恢复的视口（有记忆时不再重新拟合）；缺省则首次挂载拟合。 */
  initialViewportMatrix?: ViewportMatrix
  /** 缩放 / 平移时上报当前矩阵，由外层按图记忆。 */
  onViewportMatrix?: (matrix: ViewportMatrix) => void
}

export function X6Canvas({
  interaction = {},
  active = true,
  document: doc,
  nodePack,
  view = {},
  onChange,
  onViewChange,
  onBeforeEdit,
  onNodeClick,
  onNodeDoubleClick,
  onEdgeClick,
  onPaneClick,
  onToggleBody,
  canvas,
  className,
  style,
  children,
  initialViewportMatrix,
  onViewportMatrix,
}: X6CanvasProps) {
  useThemeVersion()
  const dark = isGraphDarkMode(), miniMapRef = useRef<HTMLDivElement>(null)
  const containerRef = useRef<HTMLDivElement>(null)
  const graphRef = useRef<Graph | null>(null)
  const isUpdatingFromProps = useRef(false)
  const hasFitViewRef = useRef(false)
  const viewportReportRef = useRef(onViewportMatrix)
  viewportReportRef.current = onViewportMatrix
  // 切页签重挂时恢复的视口：只消费挂载瞬间的值，用 ref 镜像避免 effect 依赖抖动。
  const initialMatrixRef = useRef(initialViewportMatrix)
  initialMatrixRef.current = initialViewportMatrix
  const [zoomLevel, setZoomLevel] = useState(100)

  // 将图结构投影（含 structuredClone）与视图叠加态（selected / runStatus）解耦：
  // 选中节点或收到运行态推送时直接复用 baseProjected，避免对 1000+ 节点重复深拷贝与重算容器尺寸。
  const baseProjected = useMemo(() => projectDocument(doc), [doc])
  const projected = useMemo(() => {
    if (!view.nodes && !view.edges) return baseProjected
    return {
      nodes: view.nodes
        ? baseProjected.nodes.map((n) => (view.nodes?.[n.id] ? { ...n, ...view.nodes[n.id] } : n))
        : baseProjected.nodes,
      edges: view.edges
        ? baseProjected.edges.map((e) => (view.edges?.[e.id] ? { ...e, ...view.edges[e.id] } : e))
        : baseProjected.edges,
    }
  }, [baseProjected, view])
  const { nodes, edges } = projected

  // 展开容器的尺寸包住循环体节点；随子节点移动重新计算
  const bodyToggle = !!onToggleBody
  const containerSizes = useMemo(
    () => fitContainers(baseProjected.nodes, (n) => editorNodeSize(n, nodePack.registry)),
    [baseProjected.nodes, nodePack.registry],
  )

  const callbacks = { doc, nodePack, view, interaction, onChange, onViewChange, onBeforeEdit, onNodeClick, onNodeDoubleClick, onEdgeClick, onPaneClick, onToggleBody, nodes, edges }
  const callbacksRef = useRef(callbacks)
  callbacksRef.current = callbacks

  useEffect(() => {
    if (!containerRef.current) return
    const host = containerRef.current
    ensureX6NodeRegistered()

    const minScale = interaction.minZoom ?? 0.02
    const maxScale = interaction.maxZoom ?? 2
    const graph = new Graph({
      container: containerRef.current, async: false,
      autoResize: true, width: Math.max(1, host.clientWidth), height: Math.max(1, host.clientHeight),
      scaling: {
        min: minScale,
        max: maxScale,
      },
      grid: {
        visible: true,
        type: 'dot',
        size: 16,
        args: {
          color: dark ? 'rgba(255, 255, 255, 0.08)' : 'rgba(0, 0, 0, 0.08)',
          thickness: 1,
        },
      },
      panning: interaction.panOnDrag !== false,
      interacting: { nodeMovable: canvas.nodesDraggable !== false, magnetConnectable: canvas.nodesConnectable ?? canvas.structure === 'free' },
      mousewheel: {
        enabled: interaction.zoomOnScroll !== false,
        modifiers: [],
        factor: 1.1,
        maxScale,
        minScale,
      },
      connecting: {
        snap: true,
        allowBlank: false,
        allowLoop: false,
        highlight: true,
        router: { name: ROUTER_GRAPH_EDGE },
        connector: { name: 'rounded', args: { radius: 16 } },
        createEdge() {
          return this.createEdge({
            shape: 'edge',
            router: { name: ROUTER_GRAPH_EDGE },
            connector: { name: 'rounded', args: { radius: 16 } },
            attrs: {
              line: {
                stroke: dark ? '#71717a' : '#94a3b8',
                strokeWidth: 1.75,
                targetMarker: {
                  name: 'classic',
                  size: 7,
                  fill: dark ? '#71717a' : '#94a3b8',
                },
              },
            },
          })
        },
      },
    })
    graphRef.current = graph
    const DBLCLICK_MS = 400
    let lastNodeClick = { id: '', at: 0 }
    let lastNodeDbl = { id: '', at: 0 }

    const selection = new Selection({ enabled: canvas.elementsSelectable !== false, rubberband: true, modifiers: interaction.selectionOnDrag ? null : 'shift', multiple: true, strict: interaction.selectionMode === 'full', movable: canvas.nodesDraggable !== false, multipleSelectionModifiers: ['ctrl', 'meta'] })
    graph.use(selection)
    graph.use(new Transform({ resizing: { enabled: (node) => canvas.nodesDraggable !== false && node.getData<GraphEditorNode>()?.type === 'annotation', minWidth: 120, minHeight: 80 }, rotating: false }))
    graph.on('selection:changed', ({selected}) => {
      if (isUpdatingFromProps.current) return
      const c = callbacksRef.current, ids = new Set(selected.map((cell) => cell.id))
      c.onViewChange?.({ ...c.view, nodes: Object.fromEntries(c.nodes.map((n) => [n.id, {...c.view.nodes?.[n.id], selected: ids.has(n.id)}])), edges: Object.fromEntries(c.edges.map((ed) => [ed.id, {...c.view.edges?.[ed.id], selected: ids.has(ed.id)}])) })
    })
    let selectionDragging = false, nodeDragging = false, groupDragging = false
    selection.on('box:mousedown', () => {
      selectionDragging = true
      const c = callbacksRef.current
      if (c.interaction.onSelectionDragStart) c.interaction.onSelectionDragStart()
      else c.onBeforeEdit?.()
    })
    selection.on('box:mouseup', () => {
      const c = callbacksRef.current
      const next = c.nodes.map((n) => ({
        ...n,
        position: graph.getCellById(n.id)?.isNode() ? graph.getCellById(n.id)!.getBBox().topLeft.toJSON() : n.position,
      }))
      if (next.some((n, i) => n.position.x !== c.nodes[i].position.x || n.position.y !== c.nodes[i].position.y)) {
        c.onChange?.(documentFromProjection(next, c.edges, c.doc))
      }
      selectionDragging = false
      nodeDragging = false
      c.interaction.onSelectionDragStop?.()
    })
    graph.on('node:resize', () => callbacksRef.current.onBeforeEdit?.())
    graph.on('node:resized', ({node}) => {
      const c = callbacksRef.current, size = node.size(), position = node.position()
      c.onChange?.(documentFromProjection(c.nodes.map((n) => n.id === node.id ? {...n, position, width: size.width, height: size.height} : n), c.edges, c.doc))
    })

    const emitNodeDblclick = (node: { id: string }, e: unknown) => {
      const now = Date.now()
      if (lastNodeDbl.id === node.id && now - lastNodeDbl.at < 80) return
      lastNodeDbl = { id: node.id, at: now }
      const item = callbacksRef.current.nodes.find((n) => n.id === node.id)
      if (!item) return
      callbacksRef.current.onNodeDoubleClick?.(e as unknown as CanvasEvent, item)
    }

    graph.on('scale', ({ sx }) => {
      setZoomLevel(Math.round(sx * 100))
    })

    graph.on('node:move', () => {
      if (nodeDragging || selectionDragging || isUpdatingFromProps.current) return
      nodeDragging = true
      const c = callbacksRef.current
      groupDragging = c.nodes.filter((n) => c.view.nodes?.[n.id]?.selected).length > 1
      c.interaction.onMoveStart?.()
      const begin = groupDragging ? c.interaction.onSelectionDragStart : c.interaction.onNodeDragStart
      if (begin) begin(); else c.onBeforeEdit?.()
    })
    graph.on('node:moved', () => {
      const wasNodeDragging = nodeDragging
      nodeDragging = false
      if (isUpdatingFromProps.current || selectionDragging || !wasNodeDragging) return
      const c = callbacksRef.current
      const next = c.nodes.map((n) => ({...n, position: graph.getCellById(n.id)?.isNode() ? graph.getCellById(n.id)!.getBBox().topLeft.toJSON() : n.position}))
      c.onChange?.(documentFromProjection(next, c.edges, c.doc))
      if (groupDragging) c.interaction.onSelectionDragStop?.(); else c.interaction.onNodeDragStop?.()
    })
    graph.on('node:contextmenu', ({ node, e }) => {
      e.preventDefault()
      const c = callbacksRef.current, item = c.nodes.find((n) => n.id === node.id)
      if (item) c.interaction.onNodeContextMenu?.(e as unknown as CanvasEvent, item)
    })
    graph.on('blank:contextmenu', ({ e, x, y }) => {
      e.preventDefault()
      callbacksRef.current.interaction.onPaneContextMenu?.(e as unknown as CanvasEvent, {x, y})
    })
    graph.on('edge:connected', ({ isNew, edge }) => {
      if (!isNew) return
      const c = callbacksRef.current
      const next = connectDocument(c.doc, c.nodePack, edge.getSourceCellId(), edge.getTargetCellId(), edge.getSourcePortId(), edge.getTargetPortId())
      graph.removeEdge(edge)
      if (!next || !(canvas.nodesConnectable ?? canvas.structure === 'free')) return
      c.onBeforeEdit?.()
      c.onChange?.(next)
    })
    const onDelete = async (e: KeyboardEvent) => {
      if (canvas.structure !== 'free' || !(callbacksRef.current.interaction.deleteKeyCode ?? ['Backspace', 'Delete']).includes(e.key) || (e.target instanceof Element && e.target.closest('input,textarea,[contenteditable]'))) return
      const c = callbacksRef.current
      let selected = { nodes: c.nodes.filter((n) => c.view.nodes?.[n.id]?.selected), edges: c.edges.filter((ed) => c.view.edges?.[ed.id]?.selected) }
      if (!selected.nodes.length && !selected.edges.length) return
      e.preventDefault()
      const result = await c.interaction.onBeforeDelete?.(selected)
      if (result === false || callbacksRef.current.doc !== c.doc) return
      if (result && typeof result !== 'boolean') selected = result
      if (!selected.nodes.length && !selected.edges.length) return
      const nodeIds = new Set(selected.nodes.map((n) => n.id)), edgeIds = new Set(selected.edges.map((ed) => ed.id))
      c.onBeforeEdit?.()
      c.onChange?.(documentFromProjection(c.nodes.filter((n) => !nodeIds.has(n.id)), c.edges.filter((ed) => !edgeIds.has(ed.id)), c.doc))
    }
    host.tabIndex = 0
    const onFocus = () => host.focus({preventScroll: true})
    host.addEventListener('pointerdown', onFocus)
    host.addEventListener('keydown', onDelete)

    graph.on('node:click', ({ node, e }) => {
      // 容器标题栏上的展开 / 收起按钮
      if ((e.target as Element | null)?.closest?.('[data-body-toggle]')) {
        const c = callbacksRef.current, item = c.nodes.find((n) => n.id === node.id)
        if (item?.body) c.onToggleBody?.(item.id, item.body === 'expanded')
        return
      }
      const now = Date.now()
      const isDbl = lastNodeClick.id === node.id && now - lastNodeClick.at < DBLCLICK_MS
      lastNodeClick = { id: node.id, at: now }

      if (isDbl) {
        emitNodeDblclick(node, e)
        return
      }

      const item = callbacksRef.current.nodes.find((n) => n.id === node.id)
      if (!item) return
      callbacksRef.current.onNodeClick?.(e as unknown as CanvasEvent, item)
    })

    graph.on('node:dblclick', ({ node, e }) => {
      lastNodeClick = { id: node.id, at: Date.now() }
      emitNodeDblclick(node, e)
    })

    graph.on('edge:click', ({ edge, e }) => {
      const item = callbacksRef.current.edges.find((ed) => ed.id === edge.id)
      if (item) {
        callbacksRef.current.onEdgeClick?.(e as unknown as CanvasEvent, item)
      }
    })

    graph.on('blank:click', ({ e, x, y }) => {
      if (lastNodeClick.id && Date.now() - lastNodeClick.at < DBLCLICK_MS) {
        const cell = graph.getCellById(lastNodeClick.id)
        if (cell?.isNode()) {
          const box = cell.getBBox()
          if (x >= box.x && x <= box.x + box.width && y >= box.y && y <= box.y + box.height) {
            return
          }
        }
      }
      selection.clean()
      callbacksRef.current.onPaneClick?.(e as unknown as CanvasEvent)
    })

    // 缩放 / 平移时上报矩阵：切页签重挂后恢复同一视口，避免重新拟合造成缩放跳变。
    const reportViewport = () => {
      const report = viewportReportRef.current
      if (!report) return
      const matrix = graph.matrix()
      report({ a: matrix.a, b: matrix.b, c: matrix.c, d: matrix.d, e: matrix.e, f: matrix.f })
    }
    graph.on('scale', reportViewport)
    graph.on('translate', reportViewport)

    return () => {
      host.removeEventListener('pointerdown', onFocus)
      host.removeEventListener('keydown', onDelete)
      graph.disposePlugins(['minimap', 'selection', 'transform'])
      graph.dispose()
      graphRef.current = null
      hasFitViewRef.current = false
    }
  }, [dark, canvas.showMiniMap, canvas.elementsSelectable, interaction.selectionOnDrag, interaction.selectionMode, canvas.nodesDraggable, canvas.nodesConnectable, canvas.structure, interaction.panOnDrag, interaction.zoomOnScroll, interaction.minZoom, interaction.maxZoom])

  useEffect(() => {
    const graph = graphRef.current
    if (!graph) return

    isUpdatingFromProps.current = true; ensureX6NodeRegistered()
    graph.getPlugin<Selection>('selection')!.reset([...nodes.filter((n) => view.nodes?.[n.id]?.selected).map((n) => n.id), ...edges.filter((ed) => view.edges?.[ed.id]?.selected).map((ed) => ed.id)])

    const nodeIds = new Set(nodes.map((n) => n.id)), edgeIds = new Set(edges.map((e) => e.id))

    if (graph.getCells().length === 0 && (nodes.length > 0 || edges.length > 0)) {
      // 首次挂载大批量节点与边时，使用 resetCells 一次性写入并触发单次调度冲洗，避免逐个 addNode/addEdge 触发数千次重排。
      const createdNodes = nodes.map((n) =>
        graph.createNode(editorNodeToX6(n, nodePack.registry, containerSizes.get(n.id), bodyToggle)),
      )
      const createdEdges = edges.map((e) => graph.createEdge(editorEdgeToX6(e)))
      graph.resetCells([...createdNodes, ...createdEdges])
      for (const n of nodes) {
        const scope = scopeOf(n.id)
        if (!scope) continue
        const child = graph.getCellById(n.id)
        const parent = graph.getCellById(scope)
        if (child?.isNode() && parent?.isNode() && child.getParentId() !== scope) {
          parent.addChild(child)
        }
      }
    } else {
      graph.batchUpdate('sync-cells', () => {
        for (const edge of graph.getEdges()) if (!edgeIds.has(edge.id)) graph.removeEdge(edge)
        for (const node of graph.getNodes()) if (!nodeIds.has(node.id)) graph.removeNode(node)
        for (const n of nodes) {
          const existing = graph.getCellById(n.id)
          if (!existing?.isNode()) {
            const metadata = editorNodeToX6(n, nodePack.registry, containerSizes.get(n.id), bodyToggle)
            if (existing) graph.removeCell(existing)
            graph.addNode(metadata)
            continue
          }
          const previous = existing.getData<GraphEditorNode & { registry?: unknown; bodyToggle?: boolean }>()
          const targetSize = containerSizes.get(n.id) ?? editorNodeSize(n, nodePack.registry)
          const wantW = n.type === 'annotation' ? Number(n.width ?? targetSize.w) : targetSize.w
          const wantH = n.type === 'annotation' ? Number(n.height ?? targetSize.h) : targetSize.h
          const position = existing.position(), size = existing.size()
          const posChanged = position.x !== n.position.x || position.y !== n.position.y
          const sizeChanged = size.width !== wantW || size.height !== wantH
          const payloadChanged = isX6NodePayloadChanged(previous, {
            ...n,
            registry: nodePack.registry,
            ...(bodyToggle && n.body ? { bodyToggle: true } : {}),
          })
          const sidesUnchanged =
            previous !== undefined &&
            n.endpointSides !== undefined &&
            sameEndpointSidesMap(previous.endpointSides, n.endpointSides)

          // 快速路径：节点几何、配置与端点方位均未变化（仅选中态、断点/注入或运行态更新）时，跳过 editorNodeToX6 与 JSON.stringify
          if (!posChanged && !sizeChanged && !payloadChanged && sidesUnchanged) {
            const selChanged = Boolean(previous?.selected) !== Boolean(n.selected)
            const runChanged =
              previous?.runStatus !== n.runStatus ||
              previous?.runProgress !== n.runProgress ||
              Boolean(previous?.breakpoint) !== Boolean(n.breakpoint) ||
              Boolean(previous?.inject) !== Boolean(n.inject)
            if (selChanged || runChanged) {
              existing.setData(
                {
                  ...previous,
                  selected: n.selected,
                  runStatus: n.runStatus,
                  runProgress: n.runProgress,
                  breakpoint: n.breakpoint,
                  inject: n.inject,
                },
                { silent: true, overwrite: true },
              )
              if (selChanged) applyX6HtmlSelected(graph, existing, !!n.selected)
              if (runChanged) applyX6HtmlRunStatus(graph, existing, n)
            }
            continue
          }

          const metadata = editorNodeToX6(n, nodePack.registry, containerSizes.get(n.id), bodyToggle)
          if (existing.shape !== metadata.shape) {
            graph.removeCell(existing)
            graph.addNode(metadata)
            continue
          }
          if (posChanged) existing.position(n.position.x, n.position.y)
          if (sizeChanged) existing.resize(metadata.width!, metadata.height!)
          const portsChanged =
            !sidesUnchanged && JSON.stringify(existing.getProp('ports')) !== JSON.stringify(metadata.ports)
          if (portsChanged) existing.setProp('ports', metadata.ports)
          if (portsChanged || payloadChanged) existing.setData(metadata.data, { overwrite: true })
          else {
            existing.setData(metadata.data, { silent: true, overwrite: true })
            applyX6HtmlSelected(graph, existing, !!n.selected)
            applyX6HtmlRunStatus(graph, existing, n)
          }
        }
        // 循环体节点嵌入所在容器：拖动容器时 X6 连同子节点一起平移，写回时子节点相对容器的位置不变
        for (const n of nodes) {
          const scope = scopeOf(n.id), child = graph.getCellById(n.id), parent = scope ? graph.getCellById(scope) : null
          if (child?.isNode() && parent?.isNode() && child.getParentId() !== scope) parent.addChild(child)
        }
        for (const e of edges) {
          const existing = graph.getCellById(e.id)
          if (!existing?.isEdge()) {
            graph.addEdge(editorEdgeToX6(e))
            continue
          }
          const sourcePort = e.when || GRAPH_DEFAULT_OUTPUT
          const targetPort = GRAPH_DEFAULT_INPUT
          const curSource = existing.getSource() as { cell?: string; port?: string }
          const curTarget = existing.getTarget() as { cell?: string; port?: string }
          // 仅在端点连接实际变化时才调用 setSource/setTarget，避免触发全图连线路由重算
          if (curSource?.cell !== e.from || curSource?.port !== sourcePort) {
            existing.setSource({ cell: e.from, port: sourcePort })
          }
          if (curTarget?.cell !== e.to || curTarget?.port !== targetPort) {
            existing.setTarget({ cell: e.to, port: targetPort })
          }
          const prevData = existing.getData<{ rail?: string; runStatus?: string }>()
          const railChanged = (prevData?.rail ?? undefined) !== e.rail
          const runChanged = (prevData?.runStatus ?? undefined) !== e.runStatus
          if (railChanged || runChanged) {
            existing.setData(
              {
                ...(e.rail ? { rail: e.rail } : {}),
                ...(e.runStatus ? { runStatus: e.runStatus } : {}),
              },
              { silent: !railChanged, overwrite: true },
            )
          }
          if (e.label || existing.getLabels().length > 0) {
            const metadata = editorEdgeToX6(e)
            existing.setLabels(metadata.labels ?? [])
          }
          if (runChanged || e.style) {
            applyX6EdgeRunStyle(existing, e)
          }
        }
      })
    }

    if (canvas.showMiniMap !== false && miniMapRef.current && !graph.getPlugin('minimap')) {
      const mini = new MiniMap({
        container: miniMapRef.current,
        width: 160,
        height: 100,
        padding: 10,
        graphOptions: {
          async: true,
          createCellView(cell) {
            if (cell.isEdge()) return null
            if (cell.isNode()) return MiniMapNodeView
            return undefined
          },
        },
      })
      graph.use(mini); mini.container.style.backgroundColor = dark ? '#18181b' : '#fff'
    }
    isUpdatingFromProps.current = false
  }, [nodes, edges, containerSizes, bodyToggle, nodePack.registry, dark, canvas.showMiniMap, canvas.elementsSelectable, interaction.selectionOnDrag, interaction.selectionMode, canvas.nodesDraggable, canvas.nodesConnectable, canvas.structure, interaction.panOnDrag, interaction.zoomOnScroll, interaction.minZoom, interaction.maxZoom])

  useEffect(() => {
    if (!active) return
    const graph = graphRef.current
    if (!graph || hasFitViewRef.current) return

    const el = containerRef.current
    if (!el) return

    const tryFit = () => {
      const rect = el.getBoundingClientRect()
      if (rect.width > 0 && rect.height > 0) {
        graph.resize(rect.width, rect.height)
        // 有记忆视口（切页签回来）直接恢复，不再重新拟合，避免缩放跳变。
        const remembered = initialMatrixRef.current
        if (remembered) {
          graph.matrix({ ...remembered })
          // matrix() 恢复不触发 scale 事件，用矩阵自身的 a 分量同步左下角百分比，避免显示 100% 与实际不符。
          setZoomLevel(Math.round(remembered.a * 100))
        } else if (canvas.fitView !== false) {
          graph.zoomToFit({ padding: 48, maxScale: 1.1, minScale: Math.max(interaction.minZoom ?? 0.02, 0.04) })
        }
        hasFitViewRef.current = true
      } else {
        requestAnimationFrame(tryFit)
      }
    }

    requestAnimationFrame(tryFit)
  }, [active, canvas.fitView, dark, interaction.minZoom])

  const minScale = interaction.minZoom ?? 0.02
  const maxScale = interaction.maxZoom ?? 2
  const handleZoomIn = () => {
    const g = graphRef.current
    if (!g) return
    const cur = g.zoom()
    const step = cur < 0.2 ? 0.02 : 0.1
    g.zoom(Math.min(maxScale, cur + step), { absolute: true, minScale, maxScale })
  }
  const handleZoomOut = () => {
    const g = graphRef.current
    if (!g) return
    const cur = g.zoom()
    const step = cur <= 0.2 ? 0.02 : 0.1
    g.zoom(Math.max(minScale, cur - step), { absolute: true, minScale, maxScale })
  }
  const handleFitView = () => graphRef.current?.zoomToFit({ padding: 48, maxScale: 1, minScale: Math.max(minScale, 0.04) })

  return (
    <div
      className={cn('relative h-full w-full overflow-hidden select-none', className)}
      style={style}
    >
      <div ref={containerRef} className="h-full w-full" />

      {canvas.showControls !== false && <div className="absolute bottom-4 left-4 z-20 flex items-center gap-1 rounded-xl border border-border/80 bg-card/90 p-1 shadow-sm backdrop-blur-md">
        <button
          type="button"
          onClick={handleZoomOut}
          title="缩小"
          className="rounded-lg p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors cursor-pointer"
        >
          <Minus className="h-3.5 w-3.5" />
        </button>
        <span className="min-w-[42px] text-center font-mono text-[11px] text-muted-foreground select-none">
          {zoomLevel}%
        </span>
        <button
          type="button"
          onClick={handleZoomIn}
          title="放大"
          className="rounded-lg p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors cursor-pointer"
        >
          <Plus className="h-3.5 w-3.5" />
        </button>
        <div className="h-3.5 w-[1px] bg-border mx-0.5" />
        <button
          type="button"
          onClick={handleFitView}
          title="适应视口"
          className="rounded-lg p-1.5 text-muted-foreground hover:bg-muted hover:text-foreground transition-colors cursor-pointer"
        >
          <Maximize2 className="h-3.5 w-3.5" />
        </button>
      </div>}
      {canvas.showMiniMap !== false && <div ref={miniMapRef} className="absolute bottom-4 right-4 overflow-hidden rounded-lg border border-border bg-card" />}
      {children}
    </div>
  )
}
