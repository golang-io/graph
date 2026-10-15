import { describe, expect, it } from 'vitest'
import { documentFromProjection, projectDocument } from './document'
import { connectDocument } from './canvas-edit'
import { createGraphNodePackFromCatalog } from './node-spec'
import { graphToFlow, flowToGraph } from './graph-adapter'
import { buildGraphBundle } from './graph-bundle'
import type { GraphJSON, GraphUI } from './types'

const document: GraphUI = { Graph: { Nodes: [{ID: 'a', Kind: 'biz_branch'}, {ID: 'b', Kind: 'end'}], Edges: [] } }
const pack = createGraphNodePackFromCatalog('catalog', [
  {ID: 'biz_branch', Name: 'Branch', Endpoints: {Inputs: ['In'], Outputs: ['buy', 'hold']}},
  {ID: 'end', Name: 'End', Endpoints: {Inputs: ['In']}},
])

describe('author document boundary', () => {
  it('preserves supplied positions and names default endpoint handles', () => {
    const graph: GraphJSON = {Nodes: [{ID: 'a', Kind: 'template'}, {ID: 'b', Kind: 'end'}], Edges: [{ID: 'ab', From: 'a.Out', To: 'b.In'}]}
    const flow = graphToFlow(graph, {Positions: {a: {X: 0, Y: 100}, b: {X: 300, Y: 137}}})
    expect(flow.nodes.map((n) => n.position)).toEqual([{x: 0, y: 100}, {x: 300, y: 137}])
    expect(flow.edges[0]).toMatchObject({sourceHandle: 'Out', targetHandle: 'In'})
  })
  it('does not persist display defaults during node or edge overlays', () => {
    const p = projectDocument(document, {nodes: {a: {selected: true, dragging: true, runStatus: 'failed', style: {width: 400}}}})
    expect(documentFromProjection(p.nodes, p.edges, document)).toBe(document)
    expect(p.nodes[0].name).toBe('biz_branch')
    p.nodes[0].config.x = 1
    expect(document.Graph.Nodes[0].Inputs).toBeUndefined()
  })
  it('preserves Graph.InputSchema and Node.Inputs across edits', () => {
    const base: GraphUI = {
      Graph: {
        InputSchema: { type: 'object', required: ['q'], properties: { q: {} }, additionalProperties: false },
        Nodes: [
          {ID: 'a', Kind: 'template', Inputs: {
            q: {From: '$input', Field: 'q'},
            Outputs: {Value: [{Key: 'r', Value: '{{q}}'}]},
          }},
          {ID: 'b', Kind: 'end', Inputs: {r: {From: 'a', Field: 'r'}, n: {Value: 3}}},
        ],
        Edges: [{ID: 'ab', From: 'a.Out', To: 'b.In'}],
      },
    }
    const p = projectDocument(base)
    p.nodes.find((n) => n.id === 'a')!.position = {x: 1, y: 2}
    const moved = documentFromProjection(p.nodes, p.edges, base)
    expect(moved.Graph).toEqual(base.Graph)
    // 清空字面量与引用后不写出 Inputs 键
    const b = p.nodes.find((n) => n.id === 'b')!
    b.inputs = {}
    b.config = {}
    expect(documentFromProjection(p.nodes, p.edges, base).Graph.Nodes[1]).toEqual({ID: 'b', Kind: 'end'})
  })
  it('keeps business fields absent when moving a node', () => {
    const p = projectDocument(document)
    p.nodes[0].position = {x: 33, y: 44}
    const next = documentFromProjection(p.nodes, p.edges, document)
    expect(next.Graph).toEqual(document.Graph)
    expect(next.UI?.Positions?.a).toEqual({X: 33, Y: 44})
  })
  it('excludes unknown layout keys from save and bundle', () => {
    const raw = {...document, UI: {Breakpoints: ['a'], Positions: {a: {X: 0, Y: 0}}}} as unknown as GraphUI
    const p = projectDocument(raw)
    const saved = documentFromProjection(p.nodes, p.edges, raw)
    expect(JSON.stringify(saved)).not.toContain('Breakpoints')
    expect(JSON.stringify(buildGraphBundle({name: 'test', graph: raw.Graph, ui: raw.UI}))).not.toContain('Breakpoints')
  })
  it('uses real business endpoints and refuses renderer or undeclared handles', () => {
    const next = connectDocument(document, pack, 'a', 'b', 'buy', 'In')!
    expect(next.Graph.Edges[0].From).toBe('a.buy')
    expect(next.Graph.Edges[0].To).toBe('b.In')
    expect(document.Graph.Edges).toHaveLength(0)
    expect(connectDocument(document, pack, 'a', 'b', 'true', 'In')).toBeNull()
    expect(connectDocument(document, pack, 'a', 'b', 'Out0', 'In')).toBeNull()
    expect(connectDocument(document, pack, 'a', 'b', 'buy', 'missing')).toBeNull()
  })
  it('writes explicit resized dimensions ahead of old display styles', () => {
    const flow = graphToFlow(document.Graph, {StickyNotes: [{ID: 'note', X: 0, Y: 0, Width: 300, Height: 150}]})
    const note = flow.nodes.find((n) => n.id === 'note')!
    note.width = 400; note.height = 200
    expect(flowToGraph(flow.nodes, flow.edges).ui.StickyNotes?.[0]).toMatchObject({Width: 400, Height: 200})
  })
  it('persists annotation geometry outside the executable graph', () => {
    const base: GraphUI = {...document, UI: {StickyNotes: [{ID: 'note', X: 0, Y: 0, Width: 300, Height: 180}]}}
    const p = projectDocument(base)
    const note = p.nodes.find((n) => n.id === 'note')!
    note.position = {x: 25, y: 50}; note.width = 480; note.height = 260
    const next = documentFromProjection(p.nodes, p.edges, base)
    expect(next.Graph).toEqual(document.Graph)
    expect(next.UI?.StickyNotes?.[0]).toMatchObject({X: 25, Y: 50, Width: 480, Height: 260})
  })
})
