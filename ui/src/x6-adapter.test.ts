import { describe, expect, it } from 'vitest'
import type { RFNodeData } from './graph-adapter'
import { editorNodeToX6, isX6NodePayloadChanged, x6EdgeLineAttrs } from './x6-adapter'
import { createNodeRegistry } from './node-registry'
import { RUN_STATUS_COLOR, GRAPH_RUN_STROKE_WIDTH } from './edge-style'
import type { Edge as RFEdge } from '@xyflow/react'

const base: RFNodeData = {
  label: 'LLM',
  nodeType: 'llm',
  config: { model: 'x' },
}

describe('isX6NodePayloadChanged', () => {
  it('ignores selected-only changes so HTML nodes are not remounted', () => {
    expect(isX6NodePayloadChanged({ ...base, selected: false }, base)).toBe(false)
    expect(isX6NodePayloadChanged({ ...base, selected: true }, base)).toBe(false)
  })

  it('ignores runStatus / runProgress so overlay paint does not remount HTML', () => {
    expect(isX6NodePayloadChanged(base, { ...base, runStatus: 'running' })).toBe(false)
    expect(isX6NodePayloadChanged({ ...base, runStatus: 'succeeded' }, { ...base, runStatus: 'failed' })).toBe(false)
    expect(isX6NodePayloadChanged(base, { ...base, runProgress: '1/2' })).toBe(false)
  })

  it('detects business field changes that should rebuild HTML', () => {
    expect(isX6NodePayloadChanged(undefined, base)).toBe(true)
    expect(isX6NodePayloadChanged(base, { ...base, label: 'Agent' })).toBe(true)
    expect(isX6NodePayloadChanged(base, { ...base, config: { model: 'y' } })).toBe(true)
  })
})

describe('x6EdgeLineAttrs', () => {
  const edge = { id: 'e1', source: 'a', target: 'b' } as RFEdge

  it('paints overlay runStatus with the same colors as React Flow', () => {
    expect(x6EdgeLineAttrs({ ...edge, data: { runStatus: 'succeeded' } }).stroke).toBe(RUN_STATUS_COLOR.succeeded)
    expect(x6EdgeLineAttrs({ ...edge, data: { runStatus: 'failed' } }).strokeWidth).toBe(GRAPH_RUN_STROKE_WIDTH)
    expect(x6EdgeLineAttrs({ ...edge, data: { runStatus: 'running' } }).strokeDasharray).toBe('8 4')
  })
})

describe('editorNodeToX6', () => {
  it('records rawEndpointSides alongside resolved endpointSides so fast-path comparison works when endpointSides is undefined', () => {
    const reg = createNodeRegistry([
      {
        type: 'code',
        role: 'step',
        spec: { ID: 'code', Endpoints: { Inputs: ['In'], Outputs: ['Out'] } },
      },
    ])
    const meta = editorNodeToX6(
      {
        id: 'n1',
        type: 'code',
        name: 'Code',
        config: {},
        position: { x: 10, y: 20 },
      },
      reg,
    )
    expect((meta.data as { rawEndpointSides?: unknown }).rawEndpointSides).toBeUndefined()
    expect((meta.data as { endpointSides?: unknown }).endpointSides).toEqual({ In: 'left', Out: 'right' })
  })
})
