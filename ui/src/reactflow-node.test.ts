import { describe, it, expect } from 'vitest'
import React from 'react'
import { renderToString } from 'react-dom/server'
import { GraphCanvas } from './GraphCanvas'
import { graphDecorationNodePack } from './core-pack'
import { graphToFlow } from './graph-adapter'
import type { GraphUI } from './types'

const doc: GraphUI = {
  Graph: {
    InputSchema: { type: 'object', required: ['name'], properties: { name: {} }, additionalProperties: false },
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
})
