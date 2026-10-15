import { describe, expect, it } from 'vitest'
import { computeAlignPositions, type NodeBox } from './align-selection'

const boxes: NodeBox[] = [
  { id: 'a', x: 100, y: 50, w: 40, h: 20 },
  { id: 'b', x: 200, y: 80, w: 40, h: 20 },
  { id: 'c', x: 300, y: 10, w: 40, h: 20 },
]

describe('computeAlignPositions', () => {
  it('aligns left to min x', () => {
    const m = computeAlignPositions(boxes, 'left')
    expect(m.get('a')?.x).toBe(100)
    expect(m.get('b')?.x).toBe(100)
    expect(m.get('c')?.x).toBe(100)
    expect(m.get('b')?.y).toBe(80)
  })

  it('aligns right to max right edge', () => {
    const m = computeAlignPositions(boxes, 'right')
    // maxR = 340
    expect(m.get('a')?.x).toBe(300)
    expect(m.get('c')?.x).toBe(300)
  })

  it('aligns top / bottom', () => {
    expect(computeAlignPositions(boxes, 'top').get('a')?.y).toBe(10)
    expect(computeAlignPositions(boxes, 'bottom').get('c')?.y).toBe(80)
  })

  it('distributes horizontally with equal gaps', () => {
    const m = computeAlignPositions(boxes, 'distributeH')
    // first/last fixed; middle between
    expect(m.get('a')?.x).toBe(100)
    expect(m.get('c')?.x).toBe(300)
    // span=240, sumW=120, gap=60 → b at 100+40+60=200
    expect(m.get('b')?.x).toBe(200)
  })

  it('requires 3+ for distribute', () => {
    expect(computeAlignPositions(boxes.slice(0, 2), 'distributeH').size).toBe(0)
  })
})
