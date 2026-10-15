import {
  Background, BackgroundVariant, Controls, MiniMap, ReactFlow, ReactFlowProvider,
  applyNodeChanges, applyEdgeChanges, useReactFlow, SelectionMode,
  type Node, type Edge, type NodeChange, type EdgeChange, type Connection, type Viewport,
} from '@xyflow/react'
import '@xyflow/react/dist/style.css'
import { useMemo, useRef, useState, type CSSProperties, type ReactNode, type MouseEvent } from 'react'
import type { GraphUI } from './types'
import type { GraphNodePack } from './node-pack'
import { documentFromProjection, projectDocument, type GraphEditorNode, type GraphEditorEdge, type GraphPoint, type GraphViewState } from './document'
import { graphToFlow, type RFNodeData } from './graph-adapter'
import { setBodyCollapsed } from './body-scope'
import { connectDocument } from './canvas-edit'
import { defaultGraphEdgeOptions, isGraphDarkMode } from './edge-style'
import type { GraphCanvasConfig } from './entry-config'
import { graphMinimapBg, graphMinimapMask, graphMinimapNodeColor } from './minimap-theme'
import { graphEdgeTypes } from './GraphEdge'
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
  const { screenToFlowPosition } = useReactFlow()
  const resizing = useRef(false)
  // 将文档结构投影与视图叠加态（selected / runStatus）解耦，避免选中或运行态更新时对 1000+ 节点重复执行 projectDocument 与 graphToFlow。
  const baseProjected = useMemo(() => projectDocument(document), [document])
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
  const byId = useMemo(() => new Map(projected.nodes.map((n) => [n.id, n])), [projected.nodes])
  const edgeById = useMemo(() => new Map(projected.edges.map((e) => [e.id, e])), [projected.edges])
  const baseFlow = useMemo(() => {
    const model = graphToFlow(document.Graph, document.UI, nodePack.registry)
    return {
      nodes: model.nodes.map((n) => ({
        ...n,
        data: {
          ...n.data,
          endpointSpec:
            nodePack.registry.get(n.data.nodeType)?.spec?.Endpoints ??
            nodePack.registry.get(n.data.nodeType)?.displayEndpoints,
        },
      })),
      edges: model.edges,
    }
  }, [document, nodePack])
  const { nodes, edges } = useMemo(() => {
    if (!view.nodes && !view.edges) return baseFlow
    return {
      nodes: view.nodes
        ? baseFlow.nodes.map((n) => {
            const nv = view.nodes?.[n.id]
            return nv ? { ...n, ...nv, data: { ...n.data, ...nv } } : n
          })
        : baseFlow.nodes,
      edges: view.edges
        ? baseFlow.edges.map((e) => {
            const ev = view.edges?.[e.id]
            return ev ? { ...e, ...ev, data: { ...e.data, ...ev } } : e
          })
        : baseFlow.edges,
    }
  }, [baseFlow, view])
  const modelRef = useRef<{nodes: Node<RFNodeData>[]; edges: Edge[]}>({ nodes, edges }); modelRef.current = { nodes, edges }
  const update = (nextNodes: Node<RFNodeData>[], nextEdges: Edge[], write: boolean) => {
    modelRef.current = { nodes: nextNodes, edges: nextEdges }
    let viewChanged = false
    const nextNodeView: Record<string, { selected?: boolean; dragging?: boolean }> = {}
    for (const n of nextNodes) {
      const prev = view.nodes?.[n.id]
      const sel = !!n.selected
      const drag = !!n.dragging
      if (prev?.selected !== sel || prev?.dragging !== drag) viewChanged = true
      nextNodeView[n.id] = { ...prev, selected: sel, dragging: drag }
    }
    const nextEdgeView: Record<string, { selected?: boolean }> = {}
    for (const e of nextEdges) {
      const prev = view.edges?.[e.id]
      const sel = !!e.selected
      if (prev?.selected !== sel) viewChanged = true
      nextEdgeView[e.id] = { ...prev, selected: sel }
    }
    if (viewChanged) {
      onViewChange?.({ nodes: nextNodeView, edges: nextEdgeView })
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
    const nextEditorNodes = projected.nodes.filter((n) => rfById.has(n.id)).map((n) => {
      const rf = rfById.get(n.id)!
      if (n.type !== 'annotation') return { ...n, position: absolute(rf) }
      const width = rf.width || Number(rf.style?.width) || rf.measured?.width || n.width
      const height = rf.height || Number(rf.style?.height) || rf.measured?.height || n.height
      return { ...n, position: absolute(rf), width, height }
    })
    onChange?.(documentFromProjection(nextEditorNodes, projected.edges.filter((e) => edgeIds.has(e.id)), document))
  }
  const toggleBody = (id: string) => onToggleBody?.(id, byId.get(id)?.body === 'expanded')
  const onNodesChange = (changes: NodeChange<Node<RFNodeData>>[]) => {
    const write = changes.some(
      (c) => c.type === 'remove' || c.type === 'position' || (c.type === 'dimensions' && (c.resizing || resizing.current)),
    )
    if (changes.some((c) => c.type === 'dimensions' && c.resizing) && !resizing.current) {
      onBeforeEdit?.()
      resizing.current = true
    }
    if (changes.some((c) => c.type === 'dimensions' && c.resizing === false)) resizing.current = false
    const next = applyNodeChanges(changes, modelRef.current.nodes)
    const ids = new Set(next.map((n) => n.id))
    update(next, modelRef.current.edges.filter((e) => ids.has(e.source) && ids.has(e.target)), write)
  }
  const onEdgesChange = (changes: EdgeChange<Edge>[]) => {
    const write = changes.some((c) => c.type === 'remove')
    update(modelRef.current.nodes, applyEdgeChanges(changes, modelRef.current.edges), write)
  }
  const onConnect = (c: Connection) => {
    const next = connectDocument(document, nodePack, c.source, c.target, c.sourceHandle, c.targetHandle)
    if (!next) return
    onBeforeEdit?.()
    onChange?.(next)
  }
  const onBeforeDelete = async (elements: { nodes: Node<RFNodeData>[]; edges: Edge[] }) => {
    if (canvas.structure !== 'free') return false
    const result = await interaction.onBeforeDelete?.({
      nodes: elements.nodes.map((n) => byId.get(n.id)!),
      edges: elements.edges.filter((e) => e.selected).map((e) => edgeById.get(e.id)!),
    }) ?? true
    if (typeof result === 'boolean') { if (result) onBeforeEdit?.(); return result }
    const ids = new Set(result.nodes.map((n) => n.id)), edgeIds = new Set(result.edges.map((e) => e.id))
    const accepted = {nodes: elements.nodes.filter((n) => ids.has(n.id)), edges: elements.edges.filter((e) => edgeIds.has(e.id) || ids.has(e.source) || ids.has(e.target))}
    if (!accepted.nodes.length && !accepted.edges.length) return false
    onBeforeEdit?.()
    return accepted
  }
  const connectable = canvas.nodesConnectable ?? canvas.structure === 'free'
  const minZoom = interaction.minZoom ?? 0.02
  const maxZoom = interaction.maxZoom ?? 2
  const fitViewOptions = useMemo(
    () => ({ padding: 0.08, minZoom: Math.max(minZoom, 0.04), maxZoom: 1.1 }),
    [minZoom],
  )
  const nodeClick = (e: CanvasEvent, n: Node) => onNodeClick?.(e, byId.get(n.id)!)
  const nodeDoubleClick = (e: CanvasEvent, n: Node) => onNodeDoubleClick?.(e, byId.get(n.id)!)
  const edgeClick = (e: CanvasEvent, n: Edge) => interaction.onEdgeClick?.(e, edgeById.get(n.id)!)
  return (
    <GraphBodyToggleContext.Provider value={onToggleBody ? toggleBody : null}>
    <ReactFlow<Node<RFNodeData>, Edge>
      nodes={nodes} edges={edges} onNodesChange={onNodesChange} onEdgesChange={onEdgesChange} onConnect={connectable ? onConnect : undefined} nodeTypes={graphNodeTypes} edgeTypes={graphEdgeTypes}
      defaultEdgeOptions={defaultGraphEdgeOptions()} fitView={initialViewport == null && canvas.fitView !== false} fitViewOptions={fitViewOptions} defaultViewport={initialViewport}
      onMoveEnd={(_, viewport) => onViewportChange?.(viewport)}
      nodesDraggable={canvas.nodesDraggable !== false} nodesConnectable={connectable}
      onlyRenderVisibleElements={nodes.length > 200}
      elementsSelectable={canvas.elementsSelectable !== false} zoomOnDoubleClick={false} onNodeClick={nodeClick} onNodeDoubleClick={nodeDoubleClick} onPaneClick={onPaneClick}
      proOptions={{ hideAttribution: true }}
      className="h-full w-full"
      {...interaction} minZoom={minZoom} maxZoom={maxZoom} deleteKeyCode={interaction.deleteKeyCode ?? ['Backspace', 'Delete']}
      onNodeDragStart={interaction.onNodeDragStart ?? onBeforeEdit} onSelectionDragStart={interaction.onSelectionDragStart ?? onBeforeEdit}
      selectionMode={interaction.selectionMode === 'full' ? SelectionMode.Full : SelectionMode.Partial} onEdgeClick={edgeClick} onBeforeDelete={onBeforeDelete}
      onPaneContextMenu={(e) =>
        interaction.onPaneContextMenu?.(e, screenToFlowPosition({ x: e.clientX, y: e.clientY }))
      }
      onNodeContextMenu={(e, n) => interaction.onNodeContextMenu?.(e, byId.get(n.id)!)}
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
          bgColor={graphMinimapBg()} maskColor={graphMinimapMask()} nodeColor={(n) => graphMinimapNodeColor(n, { preferRunStatus: true })} nodeStrokeColor={isGraphDarkMode() ? '#52525b' : '#d4d4d8'}
          nodeStrokeWidth={1} nodeBorderRadius={4}
        />
      )}
    </ReactFlow>
    </GraphBodyToggleContext.Provider>
  )
}
