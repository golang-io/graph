import { describe, expect, it } from 'vitest'
import { createGraphNodePackFromCatalog } from './node-spec'
import { graphToFlow } from './graph-adapter'
import { editorNodeToX6, editorEdgeToX6 } from './x6-adapter'
import { cardHeightFor, branchCardHeight } from './node-size'
import { projectDocument } from './document'
import { applyAlignToNodes } from './align-selection'
import { snapCollinearHandles } from './layout'
import type { GraphJSON, GraphUI } from './types'

const pack = createGraphNodePackFromCatalog('slots', [
  { ID: 'fan', Name: 'Fan', Renderer: 'parallel', Endpoints: { Inputs: ['In'], Outputs: ['Out'] } },
  { ID: 'join', Name: 'Join', Renderer: 'merge', Endpoints: { Inputs: ['In'], Outputs: ['Out'] } },
])
const graph: GraphJSON = {
  Nodes: [
    { ID: 'f', Kind: 'fan' },
    { ID: 'j', Kind: 'join' },
  ],
  Edges: [
    { ID: 'a', From: 'f.Out', To: 'j.In' },
    { ID: 'b', From: 'f.Out', To: 'j.In' },
  ],
}

describe('catalog canvas contracts', () => {
  it('projects fixed catalog endpoints for business types', () => {
    const edges = graphToFlow(graph, {}, pack.registry).edges
    expect(edges.map((e) => [e.sourceHandle, e.targetHandle])).toEqual([
      ['Out', 'In'],
      ['Out', 'In'],
    ])
  })
  it('uses the same declared edge slots in X6', () => {
    const flow = graphToFlow(graph, {}, pack.registry)
    const model = projectDocument({ Graph: graph })
    expect(flow.edges.map((e) => editorEdgeToX6(model.edges.find((g) => g.id === e.id)!, e).source)).toEqual([
      { cell: 'f', port: 'Out' },
      { cell: 'f', port: 'Out' },
    ])
  })
  it('sizes business branches from fixed catalog endpoints', () => {
    const h = branchCardHeight(1)
    expect(cardHeightFor('fan', {}, pack.registry)).toBe(h)
    expect(graphToFlow(graph, {}, pack.registry).nodes[0].height).toBe(h)
  })
  it('keeps node metadata scoped to its catalog and author directions', () => {
    const n = {
      id: 'f',
      type: 'fan',
      name: 'Fan',
      config: {},
      position: { x: 0, y: 0 },
      endpointSides: { In: 'top' as const, Out: 'bottom' as const },
    }
    const native = editorNodeToX6(n, pack.registry)
    expect(native.data.registry).toBe(pack.registry)
    expect(native.ports).toMatchObject({ groups: { in: { position: 'top' }, out: { position: 'bottom' } } })
  })
  it('aligns business nodes using catalog dimensions', () => {
    const document: GraphUI = {
      Graph: graph,
      UI: { Positions: { f: { X: 0, Y: 0 }, j: { X: 400, Y: 0 } } },
    }
    const aligned = applyAlignToNodes(projectDocument(document).nodes, new Set(['f', 'j']), 'bottom', pack.registry)
    expect(aligned.map((n) => n.position.y)).toEqual([0, 0])
  })
  it('snaps layout using projected dimensions instead of default types', () => {
    const model = graphToFlow(graph, { Positions: { f: { X: 0, Y: 0 }, j: { X: 420, Y: 0 } } }, pack.registry)
    const snapped = snapCollinearHandles(model.nodes, model.edges)
    expect(snapped[1].position.y - snapped[0].position.y).toBe(0)
  })
})
