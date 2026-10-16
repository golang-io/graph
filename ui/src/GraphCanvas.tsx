import {
  Background, BackgroundVariant, Controls, MiniMap, ReactFlow, ReactFlowProvider,
  applyNodeChanges, applyEdgeChanges, useReactFlow, useStoreApi, SelectionMode,
  type Node, type Edge, type NodeChange, type EdgeChange, type Connection, type Viewport,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode, type MouseEvent } from 'react'
import type { GraphUI } from './types'
import type { GraphNodePack } from './node-pack'
import { applyGraphViewState, documentFromProjection, projectDocument, type GraphEditorNode, type GraphEditorEdge, type GraphPoint, type GraphViewState, type ProjectionNodeCacheEntry } from './document'
import { specOf } from './node-size'
import { graphToFlow, type RFNodeData } from './graph-adapter'
import { setBodyCollapsed } from './body-scope'
import { connectDocument } from './canvas-edit'
import { defaultGraphEdgeOptions, isGraphDarkMode, withGraphEdgeStyle } from './edge-style'
import type { GraphCanvasConfig } from './entry-config'
import { graphMinimapBg, graphMinimapMask, graphMinimapNodeColor } from './minimap-theme'
import { GraphLayoutVersionContext, graphEdgeTypes } from './GraphEdge'
import { GraphBodyToggleContext, graphNodeTypes } from './GraphNodes'
import { CanvasRendererBadge, useCanvasRenderer } from './canvas-renderer'
import { X6Canvas } from './X6Canvas'
import { recallViewport, rememberViewport, viewportKey } from './viewport-memory'
import { useThemeVersion } from './theme-version'
import { GraphNodeRegistryProvider } from './GraphNodeRegistryContext'
import { cn } from './cn'
type CanvasEvent = MouseEvent | globalThis.MouseEvent
export type GraphCanvasInteraction = {
  onEdgeClick?: (event: CanvasEvent, edge: GraphEditorEdge) => void
  onMoveStart?: () => void
  onPaneContextMenu?: (event: CanvasEvent, point: GraphPoint) => void
  onNodeContextMenu?: (event: CanvasEvent, node: GraphEditorNode) => void
  onNodeDragStart?: () => void
  onNodeDragStop?: () => void
  onSelectionDragStart?: () => void
  onSelectionDragStop?: () => void
  onBeforeDelete?: (elements: { nodes: GraphEditorNode[]; edges: GraphEditorEdge[] }) => Promise<boolean | { nodes: GraphEditorNode[]; edges: GraphEditorEdge[] }>
  deleteKeyCode?: string[]
  selectNodesOnDrag?: boolean
  selectionOnDrag?: boolean
  selectionKeyCode?: string
  multiSelectionKeyCode?: string[]
  selectionMode?: 'partial' | 'full'
  panOnDrag?: boolean
  zoomOnScroll?: boolean
  minZoom?: number
  maxZoom?: number
}
export type GraphCanvasProps = {
  document: GraphUI
  nodePack: GraphNodePack
  view?: GraphViewState
  onChange?: (document: GraphUI) => void
  onViewChange?: (view: GraphViewState) => void
  onBeforeEdit?: () => void
  onNodeClick?: (event: CanvasEvent, node: GraphEditorNode) => void
  onNodeDoubleClick?: (event: CanvasEvent, node: GraphEditorNode) => void
  onPaneClick?: (event: CanvasEvent) => void
  canvas: GraphCanvasConfig
  interaction?: GraphCanvasInteraction
  backgroundColor?: string
  className?: string
  style?: CSSProperties
  children?: ReactNode
}
export function GraphCanvas(props: GraphCanvasProps) {
  useThemeVersion()
  const [renderer, setRenderer] = useCanvasRenderer(props.canvas.renderer)
  // 容器（map 循环体）展开 / 收起：可编辑画布写进 UI.SubLayouts[].Collapsed（子图层 Layout），作为一次可撤销的编辑；
  // 只读画布（未传 onChange，如运行详情）只在本组件内记住状态，用于预览，不写回文档。
  const [localBodies, setLocalBodies] = useState<Record<string, boolean>>({})
  const document = useMemo(
    () => props.onChange ? props.document : Object.entries(localBodies).reduce((doc, [id, collapsed]) => setBodyCollapsed(doc, id, collapsed), props.document),
    [props.onChange, props.document, localBodies],
  )
  const onToggleBody = (id: string, collapsed: boolean) => {
    if (!props.onChange) return setLocalBodies((cur) => ({ ...cur, [id]: collapsed }))
    props.onBeforeEdit?.()
    props.onChange(setBodyCollapsed(props.document, id, collapsed))
  }
  // 视口记忆：切页签重挂画布时恢复上次离开的视口，避免重新拟合造成缩放跳变。
  // 同一张图在不同渲染器下视口不通用，按“图 ID + 渲染器”分别记忆。
  const memoryKey = viewportKey(document.Graph.GraphID ?? '', renderer)
  const remembered = recallViewport(memoryKey)
  const minZoom = props.interaction?.minZoom ?? 0.02
  const maxZoom = props.interaction?.maxZoom ?? 2
  return (
    <GraphNodeRegistryProvider registry={props.nodePack.registry}>
      <div
        className={cn('relative h-full w-full overflow-hidden select-none', props.canvas.className, props.className)} style={props.style}
      >
        {renderer === 'x6' ? (
          <X6Canvas
            document={document} onToggleBody={onToggleBody} nodePack={props.nodePack} view={props.view} onChange={props.onChange} onViewChange={props.onViewChange} onBeforeEdit={props.onBeforeEdit} interaction={props.interaction}
            onNodeClick={props.onNodeClick} onNodeDoubleClick={props.onNodeDoubleClick} onEdgeClick={props.interaction?.onEdgeClick} onPaneClick={props.onPaneClick}
            canvas={props.canvas}
            className="h-full w-full"
            initialViewportMatrix={remembered?.kind === 'x6' ? remembered.matrix : undefined}
            onViewportMatrix={(matrix) => rememberViewport(memoryKey, { kind: 'x6', matrix: { ...matrix } })}
          />
        ) : (
          <ReactFlowProvider initialMinZoom={minZoom} initialMaxZoom={maxZoom}>
            <XyflowCanvasInner {...props} document={document} onToggleBody={onToggleBody}
              initialViewport={remembered?.kind === 'xyflow' ? remembered.viewport : undefined}
              onViewportChange={(viewport) => rememberViewport(memoryKey, { kind: 'xyflow', viewport: { ...viewport } })}
            />
          </ReactFlowProvider>
        )}
        {props.canvas.showRendererSwitch !== false && (
          <div className="absolute top-3 right-3 z-30 pointer-events-auto">
            <CanvasRendererBadge
              renderer={renderer} onToggle={() => setRenderer(renderer === 'x6' ? 'xyflow' : 'x6')}
            />
          </div>
        )}
        {props.children}
      </div>
    </GraphNodeRegistryProvider>
  )
}

/**
 * 判定 React Flow 的 onNodesChange 批次是否需要写回文档，并同步便签拖拽缩放（resizing）的生命周期状态。
 * - 拖拽移动节点（c.type === 'position' && c.dragging === true）与拖拽缩放便签（c.type === 'dimensions' && c.resizing === true）
 *   的中间帧仅更新画布本地节点状态（write = false），避免以 60Hz 频率在每一帧执行 projectDocument 深拷贝、JSON.stringify 对比与容器尺寸重算；
 * - 仅在松开鼠标结束拖拽（c.dragging === false）、结束便签缩放（c.resizing === false）或删除节点（c.type === 'remove'）时
 *   才将 write 设为 true，统一经 documentFromProjection 写回一次。
 *
 * Evaluates a batch of React Flow node changes to decide whether to commit to the document (write = true)
 * or only update local node state during in-flight dragging/resizing gestures (write = false).
 */
export function resolveFlowNodeChanges(
  changes: NodeChange<Node<RFNodeData>>[],
  resizing: { current: boolean },
  onBeforeEdit?: () => void,
): { write: boolean } {
  if (changes.some((c) => c.type === 'dimensions' && c.resizing) && !resizing.current) {
    onBeforeEdit?.()
    resizing.current = true
  }
  const resizeEnded = resizing.current && changes.some((c) => c.type === 'dimensions' && c.resizing === false)
  if (resizeEnded) resizing.current = false
  const write =
    resizeEnded ||
    changes.some(
      (c) =>
        c.type === 'remove' ||
        (c.type === 'position' && c.dragging !== true && !resizing.current),
    )
  return { write }
}

const PRO_OPTIONS = { hideAttribution: true } as const
const DEFAULT_DELETE_KEYS = ['Backspace', 'Delete']
const minimapNodeColor = (n: Node<RFNodeData>) => graphMinimapNodeColor(n, { preferRunStatus: true })

function sameStringRecord(a?: Record<string, string>, b?: Record<string, string>): boolean {
  if (a === b) return true
  if (!a || !b) return !a && !b
  const ka = Object.keys(a), kb = Object.keys(b)
  if (ka.length !== kb.length) return false
  for (const k of ka) if (a[k] !== b[k]) return false
  return true
}

function sameRFNodeData(a: RFNodeData, b: RFNodeData): boolean {
  if (a === b) return true
  return (
    a.label === b.label &&
    a.nodeType === b.nodeType &&
    a.body === b.body &&
    a.note === b.note &&
    a.color === b.color &&
    a.transparent === b.transparent &&
    a.runStatus === b.runStatus &&
    a.runProgress === b.runProgress &&
    Boolean(a.breakpoint) === Boolean(b.breakpoint) &&
    Boolean(a.inject) === Boolean(b.inject) &&
    a.endpointSpec === b.endpointSpec &&
    sameStringRecord(a.endpointSides, b.endpointSides) &&
    a.config === b.config &&
    a.inputs === b.inputs
  )
}

function sameRFNode(prev: Node<RFNodeData>, next: Node<RFNodeData>): boolean {
  if (prev === next) return true
  return (
    prev.id === next.id &&
    prev.type === next.type &&
    prev.parentId === next.parentId &&
    prev.position.x === next.position.x &&
    prev.position.y === next.position.y &&
    prev.width === next.width &&
    prev.height === next.height &&
    Boolean(prev.selected) === Boolean(next.selected) &&
    Boolean(prev.dragging) === Boolean(next.dragging) &&
    prev.zIndex === next.zIndex &&
    prev.style?.width === next.style?.width &&
    prev.style?.height === next.style?.height &&
    sameRFNodeData(prev.data, next.data)
  )
}

function sameRFEdge(prev: Edge, next: Edge): boolean {
  if (prev === next) return true
  const pd = prev.data as Record<string, unknown> | undefined
  const nd = next.data as Record<string, unknown> | undefined
  return (
    prev.id === next.id &&
    prev.source === next.source &&
    prev.target === next.target &&
    prev.sourceHandle === next.sourceHandle &&
    prev.targetHandle === next.targetHandle &&
    prev.label === next.label &&
    Boolean(prev.selected) === Boolean(next.selected) &&
    Boolean(prev.animated) === Boolean(next.animated) &&
    pd?.when === nd?.when &&
    pd?.rail === nd?.rail &&
    pd?.runStatus === nd?.runStatus &&
    prev.style?.stroke === next.style?.stroke &&
    prev.style?.strokeWidth === next.style?.strokeWidth
  )
}

function XyflowCanvasInner({
  document, nodePack, view = {}, onChange, onViewChange, onBeforeEdit, onNodeClick, onNodeDoubleClick, onPaneClick, canvas, interaction = {}, backgroundColor, onToggleBody,
  initialViewport, onViewportChange,
}: GraphCanvasProps & {
  onToggleBody?: (id: string, collapsed: boolean) => void
  /** 切页签回来时恢复的视口（有记忆时不再重新拟合）；缺省则首次挂载拟合。 */
  initialViewport?: Viewport
  /** 缩放 / 平移结束时上报当前视口，由外层按图记忆。 */
  onViewportChange?: (viewport: Viewport) => void
}) {
  const themeVersion = useThemeVersion()
  const { screenToFlowPosition } = useReactFlow()
  const store = useStoreApi<Node<RFNodeData>, Edge>()
  const resizing = useRef(false)
  const measuredRef = useRef<Map<string, { width: number; height: number }>>(new Map())
  const projectionCacheRef = useRef<Map<string, ProjectionNodeCacheEntry>>(new Map())

  // 将文档结构投影与视图叠加态（selected / runStatus）解耦，且在节点原始 Inputs 引用未变时直接复用已克隆的 config / inputs，
  // 避免仅拖拽移动单个节点或旋转端点时对其余 1000+ 未改动节点重复执行 structuredClone 与全图重渲染。
  const baseProjected = useMemo(
    () => projectDocument(document, {}, projectionCacheRef.current),
    [document],
  )

  // 视图状态的合并规则与 useGraphDocument、X6 画布共用 applyGraphViewState。
  const projected = useMemo(
    () => applyGraphViewState(baseProjected, view),
    [baseProjected, view],
  )
  const byId = useMemo(() => new Map(projected.nodes.map((n) => [n.id, n])), [projected.nodes])
  const edgeById = useMemo(() => new Map(projected.edges.map((e) => [e.id, e])), [projected.edges])

  const baseFlow = useMemo(() => {
    const model = graphToFlow(document.Graph, document.UI, nodePack.registry, baseProjected)
    return {
      nodes: model.nodes.map((n) => {
        if (n.width && n.height) {
          measuredRef.current.set(n.id, { width: n.width, height: n.height })
        }
        return {
          ...n,
          data: {
            ...n.data,
            // 端点声明的取值规则（spec 优先、displayEndpoints 兜底）与 node-size.specOf 共用一处。
            endpointSpec: specOf(nodePack.registry, n.data.nodeType)?.Endpoints,
          },
        }
      }),
      edges: model.edges,
    }
  }, [document, nodePack, baseProjected])

  const modelRef = useRef<{ nodes: Node<RFNodeData>[]; edges: Edge[] }>({ nodes: [], edges: [] })
  const prevBaseFlowRef = useRef<typeof baseFlow | null>(null)

  const { nodes: flowNodes, edges } = useMemo(() => {
    const prevNodes = modelRef.current.nodes
    const prevEdges = modelRef.current.edges
    const prevNodeById = new Map(prevNodes.map((n) => [n.id, n]))
    const prevEdgeById = new Map(prevEdges.map((e) => [e.id, e]))
    const sameBase = prevBaseFlowRef.current === baseFlow
    prevBaseFlowRef.current = baseFlow

    let allNodesSame = baseFlow.nodes.length === prevNodes.length
    const nextNodes = baseFlow.nodes.map((n, idx) => {
      const nv = view.nodes?.[n.id]
      const prev = prevNodeById.get(n.id)
      const measured = measuredRef.current.get(n.id) ?? n.measured
      // 若仅 view 更新而当前节点正处于拖拽/缩放手势中，保留其实时手势几何与标记
      const keepLiveGeometry = sameBase && prev && (prev.dragging || prev.resizing)
      // 运行态 / 断点 / 注入以 view 叠加层为准：显式写入（含 undefined）以便清除上次残留染色
      // Overlay fields come from view; write through undefined so stale runStatus can clear.
      const candidateData: RFNodeData = nv
        ? {
            ...n.data,
            runStatus: nv.runStatus,
            runProgress: nv.runProgress,
            breakpoint: nv.breakpoint,
            inject: nv.inject,
          }
        : n.data
      const candidate: Node<RFNodeData> = {
        ...n,
        ...(nv?.selected !== undefined ? { selected: nv.selected } : {}),
        ...(nv?.dragging !== undefined ? { dragging: nv.dragging } : {}),
        ...(nv?.style !== undefined ? { style: { ...n.style, ...nv.style } } : {}),
        ...(measured ? { measured } : {}),
        ...(keepLiveGeometry
          ? {
              position: prev.position,
              width: prev.width,
              height: prev.height,
              measured: prev.measured ?? measured,
              dragging: prev.dragging,
              resizing: prev.resizing,
            }
          : {}),
        data: prev && sameRFNodeData(prev.data, candidateData) ? prev.data : candidateData,
      }
      if (prev && sameRFNode(prev, candidate)) {
        if (prevNodes[idx] !== prev) allNodesSame = false
        return prev
      }
      allNodesSame = false
      return candidate
    })

    let allEdgesSame = baseFlow.edges.length === prevEdges.length
    const nextEdges = baseFlow.edges.map((e, idx) => {
      const ev = view.edges?.[e.id]
      const prev = prevEdgeById.get(e.id)
      let candidate: Edge = e
      if (ev) {
        // 无 runStatus 时回到基础边样式，避免上次执行的边染色残留
        const styled = ev.runStatus ? withGraphEdgeStyle(e, { runStatus: ev.runStatus }) : e
        candidate = {
          ...styled,
          ...(ev.selected !== undefined ? { selected: ev.selected } : {}),
          animated: ev.animated,
          ...(ev.style !== undefined ? { style: { ...styled.style, ...ev.style } } : {}),
          data: {
            ...styled.data,
            runStatus: ev.runStatus,
          },
        }
      }
      if (prev && sameRFEdge(prev, candidate)) {
        if (prevEdges[idx] !== prev) allEdgesSame = false
        return prev
      }
      allEdgesSame = false
      return candidate
    })

    return {
      nodes: allNodesSame ? prevNodes : nextNodes,
      edges: allEdgesSame ? prevEdges : nextEdges,
    }
  }, [baseFlow, view])

  modelRef.current = { nodes: flowNodes, edges }

  // 当受控文档或视图更新产生新的 flowNodes / edges 时，确保同步到 React Flow 内部 store
  useEffect(() => {
    if (store.getState().nodes !== flowNodes) {
      store.getState().setNodes(flowNodes)
    }
    if (store.getState().edges !== edges) {
      store.getState().setEdges(edges)
    }
  }, [store, flowNodes, edges])

  const callbacksRef = useRef({
    document,
    nodePack,
    view,
    canvas,
    interaction,
    onChange,
    onViewChange,
    onBeforeEdit,
    onNodeClick,
    onNodeDoubleClick,
    onPaneClick,
    onToggleBody,
    onViewportChange,
    baseProjected,
    projected,
    byId,
    edgeById,
    flowNodes,
    edges,
  })
  callbacksRef.current = {
    document,
    nodePack,
    view,
    canvas,
    interaction,
    onChange,
    onViewChange,
    onBeforeEdit,
    onNodeClick,
    onNodeDoubleClick,
    onPaneClick,
    onToggleBody,
    onViewportChange,
    baseProjected,
    projected,
    byId,
    edgeById,
    flowNodes,
    edges,
  }

  const update = useCallback((nextNodes: Node<RFNodeData>[], nextEdges: Edge[], write: boolean, checkView: boolean) => {
    const c = callbacksRef.current
    modelRef.current = { nodes: nextNodes, edges: nextEdges }
    if (checkView && c.onViewChange) {
      let viewChanged = false
      const nextNodeView: Record<string, { selected?: boolean }> = {}
      for (const n of nextNodes) {
        const prev = c.view.nodes?.[n.id]
        const sel = Boolean(n.selected)
        if (Boolean(prev?.selected) !== sel) viewChanged = true
        nextNodeView[n.id] = { ...prev, selected: sel }
      }
      const nextEdgeView: Record<string, { selected?: boolean }> = {}
      for (const e of nextEdges) {
        const prev = c.view.edges?.[e.id]
        const sel = Boolean(e.selected)
        if (Boolean(prev?.selected) !== sel) viewChanged = true
        nextEdgeView[e.id] = { ...prev, selected: sel }
      }
      if (viewChanged) {
        c.onViewChange({ nodes: nextNodeView, edges: nextEdgeView })
      }
    }
    if (!write) return
    // 经编辑模型写回：只取渲染器改动的坐标 / 便签尺寸与删除结果，其余字段沿用投影。
    // 循环体节点在 React Flow 里是相对父容器的坐标，这里沿 parentId 累加回画布绝对坐标。
    const rfById = new Map(nextNodes.map((n) => [n.id, n]))
    const absolute = (n: Node<RFNodeData>): GraphPoint => {
      const parent = n.parentId ? rfById.get(n.parentId) : undefined
      if (!parent) return n.position
      const at = absolute(parent)
      return { x: at.x + n.position.x, y: at.y + n.position.y }
    }
    const edgeIds = new Set(nextEdges.map((e) => e.id))
    const nextEditorNodes = c.projected.nodes.filter((n) => rfById.has(n.id)).map((n) => {
      const rf = rfById.get(n.id)!
      if (n.type !== 'annotation') return { ...n, position: absolute(rf) }
      const width = rf.measured?.width || rf.width || Number(rf.style?.width) || n.width
      const height = rf.measured?.height || rf.height || Number(rf.style?.height) || n.height
      return { ...n, position: absolute(rf), width, height }
    })
    const nextDoc = documentFromProjection(
      nextEditorNodes,
      c.projected.edges.filter((e) => edgeIds.has(e.id)),
      c.document,
      c.baseProjected,
    )
    c.onChange?.(nextDoc)
    // 若写回后文档未发生实质变化（例如循环体内子节点从左/上边界拖出后被夹回原位，nextDoc === c.document），
    // 父组件不会触发重渲染，此时直接将受控的 flowNodes 写回 store 使节点立即回弹到合法边界。
    if (c.onChange && nextDoc === c.document) {
      modelRef.current = { nodes: c.flowNodes, edges: c.edges }
      store.getState().setNodes(c.flowNodes)
    }
  }, [store])

  const toggleBody = useCallback((id: string) => {
    const c = callbacksRef.current
    c.onToggleBody?.(id, c.byId.get(id)?.body === 'expanded')
  }, [])

  const onNodesChange = useCallback((changes: NodeChange<Node<RFNodeData>>[]) => {
    const c = callbacksRef.current
    for (const ch of changes) {
      if (ch.type === 'dimensions' && ch.dimensions?.width && ch.dimensions?.height) {
        measuredRef.current.set(ch.id, { width: ch.dimensions.width, height: ch.dimensions.height })
      }
    }
    const { write } = resolveFlowNodeChanges(changes, resizing, c.onBeforeEdit)
    const curNodes = modelRef.current.nodes
    const curEdges = modelRef.current.edges
    const next = applyNodeChanges(changes, curNodes)
    const hasRemove = changes.some((ch) => ch.type === 'remove')
    const nextEdges = hasRemove
      ? (() => {
          const ids = new Set(next.map((n) => n.id))
          return curEdges.filter((e) => ids.has(e.source) && ids.has(e.target))
        })()
      : curEdges
    const checkView = hasRemove || changes.some((ch) => ch.type === 'select')
    modelRef.current = { nodes: next, edges: nextEdges }
    // 直接同步到 React Flow 内部 store，使拖拽/缩放中间帧在单次同步调度中只更新被拖节点及其相连边，不触发外层画布重渲染
    if (store.getState().nodes !== next) {
      store.getState().setNodes(next)
    }
    if (nextEdges !== curEdges && store.getState().edges !== nextEdges) {
      store.getState().setEdges(nextEdges)
    }
    update(next, nextEdges, write, checkView)
  }, [store, update])

  const onEdgesChange = useCallback((changes: EdgeChange<Edge>[]) => {
    const write = changes.some((ch) => ch.type === 'remove')
    const checkView = write || changes.some((ch) => ch.type === 'select')
    const curNodes = modelRef.current.nodes
    const nextEdges = applyEdgeChanges(changes, modelRef.current.edges)
    modelRef.current = { nodes: curNodes, edges: nextEdges }
    if (store.getState().edges !== nextEdges) {
      store.getState().setEdges(nextEdges)
    }
    update(curNodes, nextEdges, write, checkView)
  }, [store, update])

  const onConnect = useCallback((conn: Connection) => {
    const c = callbacksRef.current
    const next = connectDocument(c.document, c.nodePack, conn.source, conn.target, conn.sourceHandle, conn.targetHandle)
    if (!next) return
    c.onBeforeEdit?.()
    c.onChange?.(next)
  }, [])

  const onBeforeDelete = useCallback(async (elements: { nodes: Node<RFNodeData>[]; edges: Edge[] }) => {
    const c = callbacksRef.current
    if (c.canvas.structure !== 'free') return false
    const result = await c.interaction.onBeforeDelete?.({
      nodes: elements.nodes.map((n) => c.byId.get(n.id)!),
      edges: elements.edges.filter((e) => e.selected).map((e) => c.edgeById.get(e.id)!),
    }) ?? true
    if (typeof result === 'boolean') { if (result) c.onBeforeEdit?.(); return result }
    const ids = new Set(result.nodes.map((n) => n.id)), edgeIds = new Set(result.edges.map((e) => e.id))
    const accepted = {nodes: elements.nodes.filter((n) => ids.has(n.id)), edges: elements.edges.filter((e) => edgeIds.has(e.id) || ids.has(e.source) || ids.has(e.target))}
    if (!accepted.nodes.length && !accepted.edges.length) return false
    c.onBeforeEdit?.()
    return accepted
  }, [])

  const connectable = canvas.nodesConnectable ?? canvas.structure === 'free'
  const minZoom = interaction.minZoom ?? 0.02
  const maxZoom = interaction.maxZoom ?? 2
  const fitViewOptions = useMemo(
    () => ({ padding: 0.08, minZoom: Math.max(minZoom, 0.04), maxZoom: 1.1 }),
    [minZoom],
  )
  const edgeOptions = useMemo(() => defaultGraphEdgeOptions(), [themeVersion])
  const nodeClick = useCallback((e: CanvasEvent, n: Node) => {
    const c = callbacksRef.current
    c.onNodeClick?.(e, c.byId.get(n.id)!)
  }, [])
  const nodeDoubleClick = useCallback((e: CanvasEvent, n: Node) => {
    const c = callbacksRef.current
    c.onNodeDoubleClick?.(e, c.byId.get(n.id)!)
  }, [])
  const edgeClick = useCallback((e: CanvasEvent, ed: Edge) => {
    const c = callbacksRef.current
    c.interaction.onEdgeClick?.(e, c.edgeById.get(ed.id)!)
  }, [])
  const handleMoveEnd = useCallback((_: unknown, viewport: Viewport) => {
    callbacksRef.current.onViewportChange?.(viewport)
  }, [])
  const handlePaneContextMenu = useCallback((e: CanvasEvent) => {
    // 阻止浏览器默认右键菜单，交由业务层 onPaneContextMenu 处理
    e.preventDefault()
    const c = callbacksRef.current
    c.interaction.onPaneContextMenu?.(e, screenToFlowPosition({ x: e.clientX, y: e.clientY }))
  }, [screenToFlowPosition])
  const handleNodeContextMenu = useCallback((e: CanvasEvent, n: Node) => {
    // 阻止浏览器默认右键菜单，交由业务层 onNodeContextMenu 处理
    e.preventDefault()
    const c = callbacksRef.current
    c.interaction.onNodeContextMenu?.(e, c.byId.get(n.id)!)
  }, [])
  const handleNodeDragStart = useCallback(() => {
    const c = callbacksRef.current
    const fn = c.interaction.onNodeDragStart ?? c.onBeforeEdit
    fn?.()
  }, [])
  const handleSelectionDragStart = useCallback(() => {
    const c = callbacksRef.current
    const fn = c.interaction.onSelectionDragStart ?? c.onBeforeEdit
    fn?.()
  }, [])
  const handlePaneClick = useCallback((e: CanvasEvent) => {
    callbacksRef.current.onPaneClick?.(e)
  }, [])

  return (
    <GraphLayoutVersionContext.Provider value={baseFlow}>
    <GraphBodyToggleContext.Provider value={onToggleBody ? toggleBody : null}>
    <ReactFlow<Node<RFNodeData>, Edge>
      nodes={flowNodes} edges={edges} onNodesChange={onNodesChange} onEdgesChange={onEdgesChange} onConnect={connectable ? onConnect : undefined} nodeTypes={graphNodeTypes} edgeTypes={graphEdgeTypes}
      defaultEdgeOptions={edgeOptions} fitView={initialViewport == null && canvas.fitView !== false} fitViewOptions={fitViewOptions} defaultViewport={initialViewport}
      onMoveEnd={handleMoveEnd}
      nodesDraggable={canvas.nodesDraggable !== false} nodesConnectable={connectable}
      onlyRenderVisibleElements={flowNodes.length > 200}
      elementsSelectable={canvas.elementsSelectable !== false} zoomOnDoubleClick={false} onNodeClick={nodeClick} onNodeDoubleClick={nodeDoubleClick} onPaneClick={handlePaneClick}
      proOptions={PRO_OPTIONS}
      className="h-full w-full"
      {...interaction} minZoom={minZoom} maxZoom={maxZoom} deleteKeyCode={interaction.deleteKeyCode ?? DEFAULT_DELETE_KEYS}
      onNodeDragStart={handleNodeDragStart} onSelectionDragStart={handleSelectionDragStart}
      selectionMode={interaction.selectionMode === 'full' ? SelectionMode.Full : SelectionMode.Partial} onEdgeClick={edgeClick} onBeforeDelete={onBeforeDelete}
      onPaneContextMenu={handlePaneContextMenu}
      onNodeContextMenu={handleNodeContextMenu}
    >
      <Background variant={BackgroundVariant.Dots} gap={16} size={1} color={backgroundColor} />
      {canvas.showControls !== false && (
        <Controls showInteractive={canvas.controlsInteractive !== false} fitViewOptions={fitViewOptions} className="!m-3" />
      )}
      {canvas.showMiniMap !== false && (
        <MiniMap
          pannable
          zoomable
          className="!m-3"
          bgColor={graphMinimapBg()} maskColor={graphMinimapMask()} nodeColor={minimapNodeColor} nodeStrokeColor={isGraphDarkMode() ? '#52525b' : '#d4d4d8'}
          nodeStrokeWidth={1} nodeBorderRadius={4}
        />
      )}
    </ReactFlow>
    </GraphBodyToggleContext.Provider>
    </GraphLayoutVersionContext.Provider>
  )
}
