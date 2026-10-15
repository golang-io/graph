import { parseSide, type Side } from './orient'

/**
 * edge-route — 正交折线路由，绕开中间节点包围盒。
 * 约定：线与线可交叉；线不得穿过其它节点矩形。
 * 回边（如 body→loop）中段也不得穿过 source/target 本体，否则会画成「从对面句柄穿回来」。
 */

export type Pt = { x: number; y: number }

export type Box = { left: number; top: number; right: number; bottom: number }

const PAD = 14
const EXIT = 28
const RAIL_GAP = 36

export function inflateBox(b: Box, pad = PAD): Box {
  return {
    left: b.left - pad,
    top: b.top - pad,
    right: b.right + pad,
    bottom: b.bottom + pad,
  }
}

export function hSegHitsBox(y: number, x1: number, x2: number, b: Box): boolean {
  const lo = Math.min(x1, x2)
  const hi = Math.max(x1, x2)
  if (y < b.top || y > b.bottom) return false
  return hi >= b.left && lo <= b.right
}

export function vSegHitsBox(x: number, y1: number, y2: number, b: Box): boolean {
  const lo = Math.min(y1, y2)
  const hi = Math.max(y1, y2)
  if (x < b.left || x > b.right) return false
  return hi >= b.top && lo <= b.bottom
}

function segHitsBox(a: Pt, b: Pt, ob: Box): boolean {
  if (Math.abs(a.y - b.y) < 0.5) return hSegHitsBox(a.y, a.x, b.x, ob)
  if (Math.abs(a.x - b.x) < 0.5) return vSegHitsBox(a.x, a.y, b.y, ob)
  const box: Box = {
    left: Math.min(a.x, b.x),
    right: Math.max(a.x, b.x),
    top: Math.min(a.y, b.y),
    bottom: Math.max(a.y, b.y),
  }
  return !(box.right < ob.left || box.left > ob.right || box.bottom < ob.top || box.top > ob.bottom)
}

/**
 * 检测折线是否穿障。
 * 首尾短节允许贴 source/target 句柄；中段必须避开端点盒，
 * 否则回边会从 body 左侧「穿出」，看起来像第二根 LOOP→body。
 */
export function polyHitsObstacles(
  pts: Pt[],
  midObstacles: Box[],
  endpointBoxes: Box[] = [],
): boolean {
  for (let i = 0; i < pts.length - 1; i++) {
    const a = pts[i]
    const b = pts[i + 1]
    const isEndCap = i === 0 || i === pts.length - 2
    for (const ob of midObstacles) {
      if (segHitsBox(a, b, ob)) return true
    }
    if (!isEndCap) {
      for (const ob of endpointBoxes) {
        if (segHitsBox(a, b, ob)) return true
      }
    }
  }
  return false
}

function exitPoint(x: number, y: number, dir: 'left' | 'right' | 'top' | 'bottom'): Pt {
  if (dir === 'right') return { x: x + EXIT, y }
  if (dir === 'left') return { x: x - EXIT, y }
  if (dir === 'bottom') return { x, y: y + EXIT }
  return { x, y: y - EXIT }
}

/**
 * 回边专用：source 出边离开 → 绕到节点带上方或下方 → 再进 target 入边。
 * 不走两节点之间的水平走廊（避免和 LOOP→body 叠在一起）。
 *
 * 轨道高度只由 **两端节点** 决定（局部折回）。不得把同列更上方的其它节点
 * （如教学图里「高分支」）计入 tops，否则 top 轨道会被拽到整画布顶。
 * 中间障仅用于碰撞检测：局部轨穿障则试另一侧，再必要时按走廊障抬升。
 *
 * preferRail：
 * - auto（默认）：选离 source 更近的一侧（拼接在上则走上，在下则走下）
 * - top / bottom：强制轨道走两端节点上方 / 下方（出口在左右边时有效）
 * - left / right：强制轨道走两端节点左侧 / 右侧（出口在上下边时有效，即纵向图）
 * 与出口方向不在同一轴上的取值按 auto 处理。
 */
export function routeBackEdge(
  sx: number,
  sy: number,
  tx: number,
  ty: number,
  sourceBox: Box | undefined,
  targetBox: Box | undefined,
  midObstacles: Box[],
  opts?: {
    railIndex?: number
    sourceDir?: Side
    targetDir?: Side
    preferRail?: BackEdgeRail
  },
): { points: Pt[]; labelX: number; labelY: number } {
  const sDir = opts?.sourceDir ?? 'right'
  if (sDir === 'left' || sDir === 'right') {
    const prefer = opts?.preferRail === 'top' || opts?.preferRail === 'bottom' ? opts.preferRail : 'auto'
    return routeBackEdgeHorizontal(sx, sy, tx, ty, sourceBox, targetBox, midObstacles, { ...opts, preferRail: prefer })
  }
  // 出口在上下边：交换 x/y 后复用横向轨道算法，结果再换回。
  // 交换后原坐标的 left/right 轨道对应横向算法的 top/bottom 轨道。
  const prefer = opts?.preferRail === 'left' ? 'top' : opts?.preferRail === 'right' ? 'bottom' : 'auto'
  const r = routeBackEdgeHorizontal(sy, sx, ty, tx, sourceBox && transposeBox(sourceBox), targetBox && transposeBox(targetBox), midObstacles.map(transposeBox), {
    railIndex: opts?.railIndex,
    sourceDir: transposeSide(sDir),
    targetDir: transposeSide(opts?.targetDir ?? 'top'),
    preferRail: prefer,
  })
  return { points: r.points.map(transposePt), labelX: r.labelY, labelY: r.labelX }
}

/** 回边轨道偏好：auto 或强制走某一侧（取值与端点所在边相同） */
export type BackEdgeRail = 'auto' | Side

/** 解析存储的回边轨道（graph.UI.EdgeRails）；非法值返回 undefined */
export function parseEdgeRail(v: unknown): Side | undefined {
  return parseSide(v) ?? undefined
}

const transposePt = (p: Pt): Pt => ({ x: p.y, y: p.x })
const transposeBox = (b: Box): Box => ({ left: b.top, right: b.bottom, top: b.left, bottom: b.right })
function transposeSide(side: Side): Side {
  switch (side) {
    case 'left':
      return 'top'
    case 'right':
      return 'bottom'
    case 'top':
      return 'left'
    case 'bottom':
      return 'right'
  }
}

/** 出口在左右边时的回边：轨道在两端节点的上方或下方 */
function routeBackEdgeHorizontal(
  sx: number,
  sy: number,
  tx: number,
  ty: number,
  sourceBox: Box | undefined,
  targetBox: Box | undefined,
  midObstacles: Box[],
  opts?: {
    railIndex?: number
    sourceDir?: Side
    targetDir?: Side
    preferRail?: 'auto' | 'top' | 'bottom'
  },
): { points: Pt[]; labelX: number; labelY: number } {
  const railIndex = opts?.railIndex ?? 0
  const sDir = opts?.sourceDir ?? 'right'
  const tDir = opts?.targetDir ?? 'left'
  const prefer = opts?.preferRail ?? 'auto'
  const p1 = exitPoint(sx, sy, sDir)
  const p2 = exitPoint(tx, ty, tDir)

  const localBottoms = [sy, ty]
  const localTops = [sy, ty]
  if (sourceBox) {
    localBottoms.push(sourceBox.bottom)
    localTops.push(sourceBox.top)
  }
  if (targetBox) {
    localBottoms.push(targetBox.bottom)
    localTops.push(targetBox.top)
  }

  const railOff = RAIL_GAP + Math.abs(railIndex) * 18
  const localAbove = Math.min(...localTops) - railOff
  const localBelow = Math.max(...localBottoms) + railOff

  // 走廊内且贴近两端垂直范围的障：局部轨穿不过时再抬升（仍忽略更上方整列节点）
  // 先按水平走廊 [xLo, xHi] 过滤一次：走廊外的障碍不可能与回边折线相交，避免在千节点大图上逐个膨胀全图节点。
  // Prune obstacles to the horizontal corridor [xLo, xHi] upfront: boxes outside cannot intersect the back-edge path.
  const xLo = Math.min(p1.x, p2.x, sx, tx) - PAD
  const xHi = Math.max(p1.x, p2.x, sx, tx) + PAD
  const corridorObstacles = midObstacles.filter((b) => b.right >= xLo && b.left <= xHi)
  const zoneTop = Math.min(...localTops) - RAIL_GAP * 3
  const zoneBottom = Math.max(...localBottoms) + RAIL_GAP * 3
  const nearBottoms = [...localBottoms]
  const nearTops = [...localTops]
  for (const b of corridorObstacles) {
    if (b.bottom < zoneTop || b.top > zoneBottom) continue
    nearBottoms.push(b.bottom)
    nearTops.push(b.top)
  }
  const clearedAbove = Math.min(...nearTops) - railOff
  const clearedBelow = Math.max(...nearBottoms) + railOff

  const endpoints: Box[] = []
  if (sourceBox) endpoints.push(inflateBox(sourceBox, PAD))
  if (targetBox) endpoints.push(inflateBox(targetBox, PAD))
  const mids = corridorObstacles.map((b) => inflateBox(b))

  const pathAt = (railY: number): Pt[] => [
    { x: sx, y: sy },
    p1,
    { x: p1.x, y: railY },
    { x: p2.x, y: railY },
    p2,
    { x: tx, y: ty },
  ]

  const preferOrder = (above: number, below: number): number[] => {
    if (prefer === 'top') return [above, below]
    if (prefer === 'bottom') return [below, above]
    return Math.abs(above - sy) <= Math.abs(below - sy) ? [above, below] : [below, above]
  }

  // 先局部折回，再走廊清障抬升
  const candidates = [
    ...preferOrder(localAbove, localBelow),
    ...preferOrder(clearedAbove, clearedBelow),
  ]

  for (const railY of candidates) {
    const cleaned = dedupePoints(pathAt(railY))
    if (!polyHitsObstacles(cleaned, mids, endpoints)) {
      return labelAt(cleaned)
    }
  }
  return labelAt(dedupePoints(pathAt(candidates[0]!)))
}

/** 是否回边：目标在源出边方向的「背后」 */
export function isBackEdge(
  sx: number,
  sy: number,
  tx: number,
  ty: number,
  sourceDir: 'left' | 'right' | 'top' | 'bottom',
): boolean {
  if (sourceDir === 'right') return tx < sx - 8
  if (sourceDir === 'left') return tx > sx + 8
  if (sourceDir === 'bottom') return ty < sy - 8
  return ty > sy + 8
}

/**
 * 正交绕障。railIndex 用于同 pair 多条边错开轨道。
 * opts.sourceBox / targetBox：端点节点盒，中段不得穿过。
 */
export function routeAroundNodes(
  sx: number,
  sy: number,
  tx: number,
  ty: number,
  obstacles: Box[],
  opts?: {
    railIndex?: number
    sourceDir?: 'left' | 'right' | 'top' | 'bottom'
    targetDir?: 'left' | 'right' | 'top' | 'bottom'
    sourceBox?: Box
    targetBox?: Box
    /** 回边轨道：auto | top | bottom | left | right */
    preferRail?: BackEdgeRail
  },
): { points: Pt[]; labelX: number; labelY: number } {
  const railIndex = opts?.railIndex ?? 0
  const sDir = opts?.sourceDir ?? 'right'
  const tDir = opts?.targetDir ?? 'left'

  if (isBackEdge(sx, sy, tx, ty, sDir)) {
    return routeBackEdge(sx, sy, tx, ty, opts?.sourceBox, opts?.targetBox, obstacles, {
      railIndex,
      sourceDir: sDir,
      targetDir: tDir,
      preferRail: opts?.preferRail,
    })
  }

  const endpoints: Box[] = []
  if (opts?.sourceBox) endpoints.push(inflateBox(opts.sourceBox, PAD))
  if (opts?.targetBox) endpoints.push(inflateBox(opts.targetBox, PAD))

  const p1 = exitPoint(sx, sy, sDir)
  const p2 = exitPoint(tx, ty, tDir)

  const midX = (p1.x + p2.x) / 2 + railIndex * 10
  const midY = sy + ((ty - sy) * 0.5 || 0) + railIndex * 12

  // 仅保留水平走廊内的障碍（膨胀后与 [xMin, xMax] 相交），跳过全图其余无关节点。
  // Only inflate obstacles overlapping the horizontal corridor [xMin, xMax].
  const xMin = Math.min(p1.x, p2.x, sx, tx, midX)
  const xMax = Math.max(p1.x, p2.x, sx, tx, midX)
  const band: Box[] = []
  for (const b of obstacles) {
    if (b.right + PAD < xMin || b.left - PAD > xMax) continue
    band.push(inflateBox(b))
  }

  // 主候选折线的 y 坐标全部落在 [yMin, yMax] 内，只需与该矩形区内的障碍比对。
  // Primary candidates stay within [yMin, yMax]; only obstacles intersecting that vertical range can collide.
  const yMin = Math.min(p1.y, p2.y, sy, ty, midY)
  const yMax = Math.max(p1.y, p2.y, sy, ty, midY)
  const directObstacles = band.filter((b) => !(b.bottom < yMin || b.top > yMax))

  const candidates: Pt[][] = [
    [
      { x: sx, y: sy },
      p1,
      { x: midX, y: p1.y },
      { x: midX, y: p2.y },
      p2,
      { x: tx, y: ty },
    ],
    [
      { x: sx, y: sy },
      p1,
      { x: p1.x, y: midY },
      { x: p2.x, y: midY },
      p2,
      { x: tx, y: ty },
    ],
  ]

  for (const cand of candidates) {
    const cleaned = dedupePoints(cand)
    if (!polyHitsObstacles(cleaned, directObstacles, endpoints)) {
      return labelAt(cleaned)
    }
  }

  const below =
    (band.length ? Math.max(...band.map((b) => b.bottom), sy, ty) : Math.max(sy, ty)) +
    RAIL_GAP +
    Math.abs(railIndex) * 16
  const above =
    (band.length ? Math.min(...band.map((b) => b.top), sy, ty) : Math.min(sy, ty)) -
    RAIL_GAP -
    Math.abs(railIndex) * 16
  const railY =
    Math.abs(below - (sy + ty) / 2) <= Math.abs(above - (sy + ty) / 2) ? below : above

  return labelAt(
    dedupePoints([
      { x: sx, y: sy },
      p1,
      { x: p1.x, y: railY },
      { x: p2.x, y: railY },
      p2,
      { x: tx, y: ty },
    ]),
  )
}

function dedupePoints(pts: Pt[]): Pt[] {
  const out: Pt[] = []
  for (const p of pts) {
    const last = out[out.length - 1]
    if (last && Math.abs(last.x - p.x) < 0.5 && Math.abs(last.y - p.y) < 0.5) continue
    out.push(p)
  }
  const merged: Pt[] = []
  for (let i = 0; i < out.length; i++) {
    if (i > 0 && i < out.length - 1) {
      const a = merged[merged.length - 1]
      const b = out[i]
      const c = out[i + 1]
      if ((a.x === b.x && b.x === c.x) || (a.y === b.y && b.y === c.y)) continue
    }
    merged.push(out[i])
  }
  return merged
}

function labelAt(points: Pt[]): { points: Pt[]; labelX: number; labelY: number } {
  if (points.length < 2) {
    return { points, labelX: points[0]?.x ?? 0, labelY: points[0]?.y ?? 0 }
  }
  let best = 0
  let bestLen = -1
  for (let i = 0; i < points.length - 1; i++) {
    const len = Math.hypot(points[i + 1].x - points[i].x, points[i + 1].y - points[i].y)
    if (len > bestLen) {
      bestLen = len
      best = i
    }
  }
  const a = points[best]
  const b = points[best + 1]
  return { points, labelX: (a.x + b.x) / 2, labelY: (a.y + b.y) / 2 }
}

/** 正交折线 → SVG path（圆角） */
export function pointsToRoundedPath(points: Pt[], radius = 8): string {
  if (points.length === 0) return ''
  if (points.length === 1) return `M ${points[0].x} ${points[0].y}`
  let d = `M ${points[0].x} ${points[0].y}`
  for (let i = 1; i < points.length - 1; i++) {
    const prev = points[i - 1]
    const cur = points[i]
    const next = points[i + 1]
    const dx1 = cur.x - prev.x
    const dy1 = cur.y - prev.y
    const dx2 = next.x - cur.x
    const dy2 = next.y - cur.y
    const len1 = Math.hypot(dx1, dy1)
    const len2 = Math.hypot(dx2, dy2)
    const r = Math.min(radius, len1 / 2, len2 / 2)
    if (r < 1 || len1 < 1 || len2 < 1) {
      d += ` L ${cur.x} ${cur.y}`
      continue
    }
    const x1 = cur.x - (dx1 / len1) * r
    const y1 = cur.y - (dy1 / len1) * r
    const x2 = cur.x + (dx2 / len2) * r
    const y2 = cur.y + (dy2 / len2) * r
    d += ` L ${x1} ${y1} Q ${cur.x} ${cur.y} ${x2} ${y2}`
  }
  const last = points[points.length - 1]
  d += ` L ${last.x} ${last.y}`
  return d
}

/** 句柄上已展示的分支名：线上不再重复 label，避免挡线 */
export function isHandleBrandedWhen(when?: string | null): boolean {
  if (!when) return false
  const w = when.trim().toLowerCase()
  // 比较前统一转小写，因此 In1/Out1/Error 等大驼峰端点名同样命中。
  // merge/parallel 固定 In/Out：标签贴在句柄旁，边上不写
  if (/^in\d+$/.test(w) || /^out\d+$/.test(w)) return true
  return (
    w === 'true' ||
    w === 'false' ||
    w === 'error' ||
    w === 'if' ||
    w === 'else'
  )
}
