import { useCallback, useMemo, useRef, useState, type SetStateAction } from 'react'
import type { GraphUI } from './types'
import {
  applyGraphViewState,
  cleanDocumentUI,
  documentFromProjection,
  projectDocument,
  type GraphEditorNode,
  type GraphEditorEdge,
  type GraphViewState,
  type ProjectionNodeCacheEntry,
} from './document'

const empty: GraphUI = { Graph: { Nodes: [], Edges: [] } }

export function useGraphDocument(initial: GraphUI = empty) {
  const [state, setState] = useState(() => ({ document: cleanDocumentUI(initial), view: {} as GraphViewState }))
  const current = useRef(state)
  current.current = state
  const projectionCacheRef = useRef<Map<string, ProjectionNodeCacheEntry>>(new Map())
  // 将文档结构投影与视图叠加态（selected / dragging 等）解耦，且在节点原始 Inputs 未变时复用 config / inputs 引用，
  // 避免 setView 或仅坐标变化时重复执行 structuredClone。
  const baseProjected = useMemo(
    () => projectDocument(state.document, {}, projectionCacheRef.current),
    [state.document],
  )
  // 视图状态的合并规则与两个渲染器共用 applyGraphViewState，保证三处行为一致。
  const projected = useMemo(
    () => applyGraphViewState(baseProjected, state.view),
    [baseProjected, state],
  )
  const getDocument = useCallback(() => current.current.document, [])
  const load = useCallback((document: GraphUI) => {
    setState((s) => ({ document: cleanDocumentUI(document), view: s.view }))
  }, [])
  const setNodes = useCallback((action: SetStateAction<GraphEditorNode[]>) => setState((s) => {
    const projection = projectDocument(s.document, s.view, projectionCacheRef.current)
    const nodes = typeof action === 'function' ? action(projection.nodes) : action
    const view = Object.fromEntries(nodes.map(({id, selected, dragging, runStatus, runProgress, breakpoint, inject, style}) => [id, {selected, dragging, runStatus, runProgress, breakpoint, inject, style}]))
    return { document: documentFromProjection(nodes, projection.edges, s.document, projection), view: { ...s.view, nodes: view } }
  }), [])
  const setEdges = useCallback((action: SetStateAction<GraphEditorEdge[]>) => setState((s) => {
    const projection = projectDocument(s.document, s.view, projectionCacheRef.current)
    const edges = typeof action === 'function' ? action(projection.edges) : action
    const view = Object.fromEntries(edges.map(({id, selected, runStatus, animated, style}) => [id, {selected, runStatus, animated, style}]))
    return { document: documentFromProjection(projection.nodes, edges, s.document, projection), view: { ...s.view, edges: view } }
  }), [])
  const setView = useCallback((view: GraphViewState) => setState((s) => ({ ...s, view })), [])
  return { ...projected, document: state.document, view: state.view, getDocument, load, setNodes, setEdges, setView }
}
