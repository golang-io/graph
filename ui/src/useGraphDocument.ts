import { useCallback, useMemo, useRef, useState, type SetStateAction } from 'react'
import type { GraphUI } from './types'
import { documentFromProjection, projectDocument, type GraphEditorNode, type GraphEditorEdge, type GraphViewState } from './document'

const empty: GraphUI = { Graph: { Nodes: [], Edges: [] } }

export function useGraphDocument(initial: GraphUI = empty) {
  const [state, setState] = useState(() => { const p = projectDocument(initial); return { document: documentFromProjection(p.nodes, p.edges, initial), view: {} as GraphViewState } })
  const current = useRef(state)
  current.current = state
  // 将文档结构投影与视图叠加态（selected / dragging 等）解耦：setView 时不重跑 projectDocument 的 structuredClone。
  const baseProjected = useMemo(() => projectDocument(state.document), [state.document])
  const projected = useMemo(() => {
    const { view } = state
    if (!view.nodes && !view.edges) return baseProjected
    return {
      nodes: view.nodes
        ? baseProjected.nodes.map((n) => (view.nodes?.[n.id] ? { ...n, ...view.nodes[n.id] } : n))
        : baseProjected.nodes,
      edges: view.edges
        ? baseProjected.edges.map((e) => (view.edges?.[e.id] ? { ...e, ...view.edges[e.id] } : e))
        : baseProjected.edges,
    }
  }, [baseProjected, state])
  const getDocument = useCallback(() => current.current.document, [])
  const load = useCallback((document: GraphUI) => { const p = projectDocument(document); setState((s) => ({ document: documentFromProjection(p.nodes, p.edges, document), view: s.view })) }, [])
  const setNodes = useCallback((action: SetStateAction<GraphEditorNode[]>) => setState((s) => {
    const projection = projectDocument(s.document, s.view)
    const nodes = typeof action === 'function' ? action(projection.nodes) : action
    const view = Object.fromEntries(nodes.map(({id, selected, dragging, runStatus, runProgress, breakpoint, inject, style}) => [id, {selected, dragging, runStatus, runProgress, breakpoint, inject, style}]))
    return { document: documentFromProjection(nodes, projection.edges, s.document), view: { ...s.view, nodes: view } }
  }), [])
  const setEdges = useCallback((action: SetStateAction<GraphEditorEdge[]>) => setState((s) => {
    const projection = projectDocument(s.document, s.view)
    const edges = typeof action === 'function' ? action(projection.edges) : action
    const view = Object.fromEntries(edges.map(({id, selected, runStatus, animated, style}) => [id, {selected, runStatus, animated, style}]))
    return { document: documentFromProjection(projection.nodes, edges, s.document), view: { ...s.view, edges: view } }
  }), [])
  const setView = useCallback((view: GraphViewState) => setState((s) => ({ ...s, view })), [])
  return { ...projected, document: state.document, view: state.view, getDocument, load, setNodes, setEdges, setView }
}
