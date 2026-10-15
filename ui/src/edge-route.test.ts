import { describe, expect, it } from 'vitest'
import { routeBackEdge, type Box } from './edge-route'

describe('routeBackEdge local rail', () => {
  it('folds just above body/loop, not to distant same-column nodes', () => {
    // body（上）→ lp（左下）：同列更上方还有「高分支」
    const body: Box = { left: 1260, top: 520, right: 1480, bottom: 592 }
    const lp: Box = { left: 980, top: 640, right: 1200, bottom: 732 }
    const hi: Box = { left: 980, top: 100, right: 1200, bottom: 192 }
    const join: Box = { left: 1260, top: 220, right: 1480, bottom: 312 }

    const sx = body.right
    const sy = (body.top + body.bottom) / 2
    const tx = lp.left
    const ty = (lp.top + lp.bottom) / 2

    const { points } = routeBackEdge(sx, sy, tx, ty, body, lp, [hi, join], {
      sourceDir: 'right',
      targetDir: 'left',
      preferRail: 'top',
    })

    const railYs = points.map((p) => p.y)
    const minY = Math.min(...railYs)
    // 局部折回应贴近 body 顶（约 520-36），绝不能被 hi.top=100 拽到画布顶
    expect(minY).toBeGreaterThan(400)
    expect(minY).toBeLessThan(body.top)
  })
})
