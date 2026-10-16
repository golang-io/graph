import { describe, it, expect, vi } from 'vitest'
import React from 'react'
import { renderToString } from 'react-dom/server'
import { applyNodeChanges, type Node, type NodeChange } from '@xyflow/react'
import { GraphCanvas, resolveFlowNodeChanges } from './GraphCanvas'
import { graphDecorationNodePack } from './core-pack'
import { cleanDocumentUI, documentFromProjection, projectDocument, type ProjectionNodeCacheEntry } from './document'
import { graphToFlow, type RFNodeData } from './graph-adapter'
import type { GraphUI } from './types'

const doc: GraphUI = {
  Graph: {
    InputSchema: { type: 'object', required: ['name'], properties: { name: { type: 'string' } }, additionalProperties: false },
    Nodes: [
      { ID: 'start', Kind: 'template', Name: '开始', Inputs: {
        name: { From: '$input', Field: 'name' },
        Outputs: { Value: [{ Key: 'greeting', Value: 'hi {{name}}' }] },
      } },
      { ID: 'calc', Kind: 'code', Name: '计算', Inputs: {
        greeting: { From: 'start', Field: 'greeting' },
        Outputs: { Value: [{ Key: 'payload', Value: 'greeting' }] },
      } },
      { ID: 'after', Kind: 'template', Name: '汇总', Inputs: {
        payload: { From: 'calc', Field: 'payload' },
        Outputs: { Value: [{ Key: 'result', Value: 'done:{{payload}}' }] },
      } },
      { ID: 'finish', Kind: 'end', Name: '结束', Inputs: { result: { From: 'after', Field: 'result' } } },
    ],
    Edges: [
      { ID: 'e1', From: 'start.Out', To: 'calc.In' },
      { ID: 'e2', From: 'calc.Out', To: 'after.In' },
      { ID: 'e3', From: 'after.Out', To: 'finish.In' },
    ],
  },
  UI: {
    Positions: {
      start: { X: 40, Y: 80 },
      calc: { X: 280, Y: 80 },
      after: { X: 520, Y: 80 },
      finish: { X: 760, Y: 80 },
    },
  },
}

describe('ReactFlow linear graph', () => {
  it('projects every node at its saved position', () => {
    const flow = graphToFlow(doc.Graph, doc.UI, graphDecorationNodePack.registry)
    expect(flow.nodes.map((n) => [n.id, n.position.x])).toEqual([['start', 40], ['calc', 280], ['after', 520], ['finish', 760]])
    expect(flow.edges.map((e) => e.id)).toEqual(['e1', 'e2', 'e3'])
  })

  it('renders GraphCanvas without throwing', () => {
    const html = renderToString(
      React.createElement(GraphCanvas, {
        document: doc,
        nodePack: graphDecorationNodePack,
        canvas: { renderer: 'xyflow' } as never,
      }),
    )
    expect(typeof html).toBe('string')
  })

  it('defers document write during dragging frames and commits once on drag stop', () => {
    const resizing = { current: false }
    let nodes = graphToFlow(doc.Graph, doc.UI, graphDecorationNodePack.registry).nodes as Node<RFNodeData>[]

    // 连续拖拽中间帧：仅更新本地节点坐标，不触发文档写回，其余节点引用保持不变
    const frame1: NodeChange<Node<RFNodeData>>[] = [
      { id: 'calc', type: 'position', position: { x: 300, y: 95 }, dragging: true },
    ]
    expect(resolveFlowNodeChanges(frame1, resizing).write).toBe(false)
    const afterFrame1 = applyNodeChanges(frame1, nodes)
    expect(afterFrame1.find((n) => n.id === 'calc')?.position).toEqual({ x: 300, y: 95 })
    expect(afterFrame1.find((n) => n.id === 'start')).toBe(nodes.find((n) => n.id === 'start'))
    nodes = afterFrame1

    const frame2: NodeChange<Node<RFNodeData>>[] = [
      { id: 'calc', type: 'position', position: { x: 340, y: 120 }, dragging: true },
    ]
    expect(resolveFlowNodeChanges(frame2, resizing).write).toBe(false)
    nodes = applyNodeChanges(frame2, nodes)
    expect(nodes.find((n) => n.id === 'calc')?.position).toEqual({ x: 340, y: 120 })

    // 松开鼠标（dragging: false）：触发唯一一次文档写回
    const stopFrame: NodeChange<Node<RFNodeData>>[] = [
      { id: 'calc', type: 'position', position: { x: 360, y: 140 }, dragging: false },
    ]
    expect(resolveFlowNodeChanges(stopFrame, resizing).write).toBe(true)
    nodes = applyNodeChanges(stopFrame, nodes)
    expect(nodes.find((n) => n.id === 'calc')?.position).toEqual({ x: 360, y: 140 })
    expect(nodes.find((n) => n.id === 'calc')?.dragging).toBe(false)
  })

  it('defers document write during annotation resizing (including top-left handle position updates) and commits on resize end', () => {
    const resizing = { current: false }
    const onBeforeEdit = vi.fn()

    // 初始 ResizeObserver 测量或空点缩放手柄：不触发 onBeforeEdit，也不写回文档
    expect(
      resolveFlowNodeChanges(
        [{ id: 'note1', type: 'dimensions', dimensions: { width: 300, height: 180 } }],
        resizing,
        onBeforeEdit,
      ).write,
    ).toBe(false)
    expect(
      resolveFlowNodeChanges(
        [{ id: 'note1', type: 'dimensions', resizing: false, dimensions: { width: 300, height: 180 } }],
        resizing,
        onBeforeEdit,
      ).write,
    ).toBe(false)
    expect(onBeforeEdit).not.toHaveBeenCalled()

    // 从左上角拖拽缩放第 1 帧（同时带 position 与 dimensions resizing: true）：仅记录一次 onBeforeEdit，不写回文档
    const resizeFrame1: NodeChange<Node<RFNodeData>>[] = [
      { id: 'note1', type: 'position', position: { x: 80, y: 60 } },
      { id: 'note1', type: 'dimensions', resizing: true, setAttributes: true, dimensions: { width: 320, height: 200 } },
    ]
    expect(resolveFlowNodeChanges(resizeFrame1, resizing, onBeforeEdit).write).toBe(false)
    expect(resizing.current).toBe(true)
    expect(onBeforeEdit).toHaveBeenCalledTimes(1)

    // 拖拽缩放第 2 帧：不重复触发 onBeforeEdit，不写回文档
    const resizeFrame2: NodeChange<Node<RFNodeData>>[] = [
      { id: 'note1', type: 'position', position: { x: 70, y: 50 } },
      { id: 'note1', type: 'dimensions', resizing: true, setAttributes: true, dimensions: { width: 330, height: 210 } },
    ]
    expect(resolveFlowNodeChanges(resizeFrame2, resizing, onBeforeEdit).write).toBe(false)
    expect(onBeforeEdit).toHaveBeenCalledTimes(1)

    // 释放缩放手柄（resizing: false）：触发文档写回并重置 resizing 状态
    const resizeEnd: NodeChange<Node<RFNodeData>>[] = [
      { id: 'note1', type: 'dimensions', resizing: false, dimensions: { width: 330, height: 210 } },
    ]
    expect(resolveFlowNodeChanges(resizeEnd, resizing, onBeforeEdit).write).toBe(true)
    expect(resizing.current).toBe(false)
    expect(onBeforeEdit).toHaveBeenCalledTimes(1)
  })

  it('commits on node removal and skips document write on selection changes', () => {
    const resizing = { current: false }
    expect(
      resolveFlowNodeChanges([{ id: 'calc', type: 'select', selected: true }], resizing).write,
    ).toBe(false)
    expect(
      resolveFlowNodeChanges([{ id: 'calc', type: 'remove' }], resizing).write,
    ).toBe(true)
  })

  it('populates measured dimensions on nodes so adoptUserNodes preserves handleBounds across drags', () => {
    const flow = graphToFlow(
      doc.Graph,
      { ...doc.UI, StickyNotes: [{ ID: 'note1', X: 10, Y: 20, Width: 320, Height: 160 }] },
      graphDecorationNodePack.registry,
    )
    for (const n of flow.nodes) {
      expect(n.measured).toEqual({ width: n.width, height: n.height })
    }
    const moved = applyNodeChanges(
      [{ id: 'calc', type: 'position', position: { x: 400, y: 120 }, dragging: true }],
      flow.nodes,
    )
    expect(moved.find((n) => n.id === 'calc')?.measured).toEqual({ width: 220, height: 72 })
  })

  it('preserves Graph reference and reuses node config/inputs references when only positions change', () => {
    const cache = new Map<string, ProjectionNodeCacheEntry>()
    const p1 = projectDocument(doc, {}, cache)
    const movedNodes = p1.nodes.map((n) =>
      n.id === 'calc' ? { ...n, position: { x: 360, y: 140 } } : n,
    )
    const nextDoc = documentFromProjection(movedNodes, p1.edges, doc, p1)

    // 仅改变节点坐标时，Graph 引用与 Nodes / Edges 引用保持不变
    expect(nextDoc.Graph).toBe(doc.Graph)
    expect(nextDoc.UI?.Positions?.calc).toEqual({ X: 360, Y: 140 })

    // 增量重投影直接复用未改动节点的 config 与 inputs 引用，跳过 structuredClone
    const p2 = projectDocument(nextDoc, {}, cache)
    for (let i = 0; i < p1.nodes.length; i++) {
      expect(p2.nodes[i]?.config).toBe(p1.nodes[i]?.config)
      expect(p2.nodes[i]?.inputs).toBe(p1.nodes[i]?.inputs)
    }

    // cleanDocumentUI 在 UI 键全部合法时原样返回同一文档引用
    expect(cleanDocumentUI(nextDoc)).toBe(nextDoc)
  })
})
