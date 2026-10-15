import { describe, expect, it } from 'vitest'
import type { Edge, Node } from '@xyflow/react'
import { orderTrueBranchAbove } from './layout'

describe('orderTrueBranchAbove', () => {
  it('swaps crossed true/false targets and their exclusive successors', () => {
    const nodes: Node<{ nodeType: string }>[] = [
      { id: 'cond', type: 'switch', position: { x: 0, y: 100 }, data: { nodeType: 'quant_cond' } },
      // false 目标错误地在上方
      { id: 'down', type: 'switch', position: { x: 200, y: 0 }, data: { nodeType: 'quant_cond' } },
      { id: 'up', type: 'switch', position: { x: 200, y: 200 }, data: { nodeType: 'quant_cond' } },
      { id: 'sell', type: 'step', position: { x: 400, y: 0 }, data: { nodeType: 'quant_sell' } },
      { id: 'buy', type: 'step', position: { x: 400, y: 200 }, data: { nodeType: 'quant_buy' } },
      { id: 'end', type: 'end', position: { x: 600, y: 100 }, data: { nodeType: 'end' } },
    ]
    const edges: Edge[] = [
      { id: 'e1', source: 'cond', target: 'up', data: { when: 'true' } },
      { id: 'e2', source: 'cond', target: 'down', data: { when: 'false' } },
      { id: 'e3', source: 'up', target: 'buy', data: { when: 'true' } },
      { id: 'e4', source: 'up', target: 'end', data: { when: 'false' } },
      { id: 'e5', source: 'down', target: 'sell', data: { when: 'true' } },
      { id: 'e6', source: 'down', target: 'end', data: { when: 'false' } },
      { id: 'e7', source: 'buy', target: 'end' },
      { id: 'e8', source: 'sell', target: 'end' },
    ]
    const out = orderTrueBranchAbove(nodes, edges)
    const y = (id: string) => out.find((n) => n.id === id)!.position.y
    expect(y('up')).toBeLessThan(y('down'))
    expect(y('buy')).toBeLessThan(y('sell'))
  })
})
