/**
 * align-selection — 多选节点对齐 / 等距分布（相对选区包围盒）。
 *
 * 对齐：相对选中集合的 bbox。
 * 分布：首尾固定，中间按外边距均分（≥3 个才有效）。
 */
import type { GraphEditorNode } from './document'
import type { NodeTypeRegistry } from './node-registry'
import { ANNOTATION_DEFAULT_SIZE } from './annotation-style'
import { isAnnotationNode } from './document'
import { cardHeightFor, NODE_SIZE, rfTypeFor } from './node-size'

export type AlignOp =
  | 'left'
  | 'centerH'
  | 'right'
  | 'top'
  | 'middleV'
  | 'bottom'
  | 'distributeH'
  | 'distributeV'

export type NodeBox = {
  id: string
  x: number
  y: number
  w: number
  h: number
}

/** 取节点外接尺寸：优先 measured / style，否则按类型默认 */
export function nodeOuterSize(n: GraphEditorNode, registry?: NodeTypeRegistry): { w: number; h: number } {
  if (isAnnotationNode(n)) {
    return {
      w: n.width || Number(n.style?.width) || ANNOTATION_DEFAULT_SIZE.w,
      h: n.height || Number(n.style?.height) || ANNOTATION_DEFAULT_SIZE.h,
    }
  }
  const mw = n.width
  const mh = n.height
  if (mw != null && mh != null && mw > 0 && mh > 0) return { w: mw, h: mh }
  const kind = rfTypeFor(n.type, registry)
  const sz = NODE_SIZE[kind as keyof typeof NODE_SIZE] ?? NODE_SIZE.step
  return { w: sz.w, h: cardHeightFor(n.type, n.config, registry) }
}

export function nodesToBoxes(nodes: GraphEditorNode[], registry?: NodeTypeRegistry): NodeBox[] {
  return nodes.map((n) => {
    const { w, h } = nodeOuterSize(n, registry)
    return { id: n.id, x: n.position.x, y: n.position.y, w, h }
  })
}

/** 计算对齐/分布后的 position 映射；不足 2 个（分布不足 3 个）返回空 map */
export function computeAlignPositions(boxes: NodeBox[], op: AlignOp): Map<string, { x: number; y: number }> {
  const out = new Map<string, { x: number; y: number }>()
  if (boxes.length < 2) return out
  if ((op === 'distributeH' || op === 'distributeV') && boxes.length < 3) return out

  const minX = Math.min(...boxes.map((b) => b.x))
  const minY = Math.min(...boxes.map((b) => b.y))
  const maxR = Math.max(...boxes.map((b) => b.x + b.w))
  const maxB = Math.max(...boxes.map((b) => b.y + b.h))
  const midX = (minX + maxR) / 2
  const midY = (minY + maxB) / 2

  switch (op) {
    case 'left':
      for (const b of boxes) out.set(b.id, { x: minX, y: b.y })
      break
    case 'centerH':
      for (const b of boxes) out.set(b.id, { x: midX - b.w / 2, y: b.y })
      break
    case 'right':
      for (const b of boxes) out.set(b.id, { x: maxR - b.w, y: b.y })
      break
    case 'top':
      for (const b of boxes) out.set(b.id, { x: b.x, y: minY })
      break
    case 'middleV':
      for (const b of boxes) out.set(b.id, { x: b.x, y: midY - b.h / 2 })
      break
    case 'bottom':
      for (const b of boxes) out.set(b.id, { x: b.x, y: maxB - b.h })
      break
    case 'distributeH': {
      const sorted = [...boxes].sort((a, b) => a.x - b.x || a.id.localeCompare(b.id))
      const first = sorted[0]
      const last = sorted[sorted.length - 1]
      const span = last.x + last.w - first.x
      const sumW = sorted.reduce((s, b) => s + b.w, 0)
      const gap = (span - sumW) / (sorted.length - 1)
      let cursor = first.x
      for (let i = 0; i < sorted.length; i++) {
        const b = sorted[i]
        if (i === 0) {
          out.set(b.id, { x: first.x, y: b.y })
        } else if (i === sorted.length - 1) {
          out.set(b.id, { x: last.x, y: b.y })
        } else {
          cursor += sorted[i - 1].w + gap
          out.set(b.id, { x: cursor, y: b.y })
        }
      }
      break
    }
    case 'distributeV': {
      const sorted = [...boxes].sort((a, b) => a.y - b.y || a.id.localeCompare(b.id))
      const first = sorted[0]
      const last = sorted[sorted.length - 1]
      const span = last.y + last.h - first.y
      const sumH = sorted.reduce((s, b) => s + b.h, 0)
      const gap = (span - sumH) / (sorted.length - 1)
      let cursor = first.y
      for (let i = 0; i < sorted.length; i++) {
        const b = sorted[i]
        if (i === 0) {
          out.set(b.id, { x: b.x, y: first.y })
        } else if (i === sorted.length - 1) {
          out.set(b.id, { x: b.x, y: last.y })
        } else {
          cursor += sorted[i - 1].h + gap
          out.set(b.id, { x: b.x, y: cursor })
        }
      }
      break
    }
  }
  return out
}

export function applyAlignToNodes(
  nodes: GraphEditorNode[],
  selectedIds: Set<string>,
  op: AlignOp, registry?: NodeTypeRegistry,
): GraphEditorNode[] {
  const selected = nodes.filter((n) => selectedIds.has(n.id))
  const pos = computeAlignPositions(nodesToBoxes(selected, registry), op)
  if (pos.size === 0) return nodes
  return nodes.map((n) => {
    const p = pos.get(n.id)
    if (!p) return n
    return { ...n, position: { x: p.x, y: p.y } }
  })
}
