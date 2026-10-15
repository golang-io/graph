import { describe, expect, it } from 'vitest'
import {
  buildGraphBundle,
  parseGraphBundle,
  slugGraphName,
  GRAPH_BUNDLE_KIND,
} from './graph-bundle'
import type { GraphJSON } from './types'

const validGraph = {
  Nodes: [
    { ID: 't1', Kind: 'template', Name: '开始' },
    { ID: 'e1', Kind: 'end', Name: '结束' },
  ],
  Edges: [{ ID: 'c1', From: 't1.Out', To: 'e1.In' }],
}

describe('buildGraphBundle', () => {
  it('wraps graph with Kind', () => {
    const b = buildGraphBundle({ name: 'Demo', description: 'd', graph: validGraph })
    expect(b.Kind).toBe(GRAPH_BUNDLE_KIND)
    expect(b.Name).toBe('Demo')
    expect(b.Description).toBe('d')
    expect(b.Graph.Nodes).toHaveLength(2)
  })

  it('normalizes UI positions', () => {
    const b = buildGraphBundle({
      name: 'x',
      graph: validGraph,
      ui: { Positions: { t1: { X: 1, Y: 2 } } },
    })
    expect(b.UI?.Positions?.t1).toEqual({ X: 1, Y: 2 })
  })
})

describe('parseGraphBundle', () => {
  it('accepts a full export file', () => {
    const raw = {
      Kind: GRAPH_BUNDLE_KIND,
      Name: 'Full',
      Graph: validGraph,
      UI: { Positions: { t1: { X: 0, Y: 0 } } },
    }
    const r = parseGraphBundle(raw)
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.bundle.Name).toBe('Full')
    expect(r.bundle.Graph.Edges).toHaveLength(1)
    expect(r.bundle.UI?.Positions?.t1).toEqual({ X: 0, Y: 0 })
  })

  it('accepts a bare graph', () => {
    const r = parseGraphBundle(validGraph, 'from-bare')
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.bundle.Name).toBe('from-bare')
    expect(r.bundle.Graph.Nodes[0]?.Kind).toBe('template')
  })

  it('accepts {Graph, UI} without Kind', () => {
    const r = parseGraphBundle({ Name: 'Partial', Graph: validGraph })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.bundle.Name).toBe('Partial')
  })

  it('rejects the retired snake_case shape', () => {
    const r = parseGraphBundle({ name: 'Old', graph: { nodes: [], edges: [] } })
    expect(r.ok).toBe(false)
    if (r.ok) return
    expect(r.error).toBe('missing_graph')
  })

  it('accepts a graph with only an end node', () => {
    const r = parseGraphBundle({
      Nodes: [{ ID: 'a', Kind: 'end' }],
      Edges: [],
    })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.bundle.Graph.Nodes[0]?.Kind).toBe('end')
  })

  it('round-trips Graph.InputSchema and Node.Inputs', () => {
    const graph: GraphJSON = {
      InputSchema: {
        type: 'object',
        required: ['q'],
        properties: { q: {}, opt: {} },
        additionalProperties: false,
      },
      Nodes: [
        { ID: 'a', Kind: 'template', Inputs: { q: { From: '$input', Field: 'q' } } },
        { ID: 'e', Kind: 'end', Inputs: { r: { From: 'a', Field: 'r' } } },
      ],
      Edges: [{ ID: 'c1', From: 'a.Out', To: 'e.In' }],
    }
    const r = parseGraphBundle(buildGraphBundle({ name: 'io', graph }))
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.bundle.Graph).toEqual(graph)
  })

  it('rejects malformed inputs', () => {
    const bad = parseGraphBundle({ InputSchema: [], Nodes: [], Edges: [] })
    expect(bad.ok).toBe(false)
    const badNode = parseGraphBundle({ Nodes: [{ ID: 'a', Kind: 'end', Inputs: [] }], Edges: [] })
    expect(badNode.ok).toBe(false)
  })

  it('rejects bad edge refs', () => {
    const r = parseGraphBundle({
      Nodes: [{ ID: 't1', Kind: 'template' }],
      Edges: [{ ID: 'c1', From: 't1.Out', To: 'missing.In' }],
    })
    expect(r.ok).toBe(false)
    if (r.ok) return
    expect(r.error).toBe('edge_to_missing:missing')
  })

  it('accepts product node types', () => {
    const r = parseGraphBundle({
      Nodes: [
        { ID: 'q1', Kind: 'quant_signal' },
        { ID: 'e1', Kind: 'end' },
      ],
      Edges: [{ ID: 'c1', From: 'q1.Out', To: 'e1.In' }],
    })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.bundle.Graph.Nodes[0]?.Kind).toBe('quant_signal')
  })

  it('ignores unknown top-level fields', () => {
    const r = parseGraphBundle({
      Kind: GRAPH_BUNDLE_KIND,
      Version: 99,
      Name: 'Old',
      Graph: validGraph,
    })
    expect(r.ok).toBe(true)
    if (!r.ok) return
    expect(r.bundle.Name).toBe('Old')
  })

  it('rejects unknown Kind', () => {
    const r = parseGraphBundle({ Kind: 'other', Graph: validGraph })
    expect(r.ok).toBe(false)
    if (r.ok) return
    expect(r.error).toBe('unknown_kind')
  })
})

describe('slugGraphName', () => {
  it('slugifies', () => {
    expect(slugGraphName('Hello World')).toBe('hello-world')
    expect(slugGraphName('  ')).toBe('graph')
  })
})
