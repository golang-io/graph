/**
 * GraphEdge — 自定义边：正交路径绕开节点；线可交叉；句柄已标分支时不在线上重复 label。
 */
import {
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  Position,
  useNodes,
  useStore,
  type EdgeProps,
} from '@xyflow/react'
import { useMemo } from 'react'
import {
  isHandleBrandedWhen,
  parseEdgeRail,
  pointsToRoundedPath,
  routeAroundNodes,
  type Box,
} from './edge-route'
import { isAnnotationNode } from './document'
import { isInsideContainer } from './body-scope'
import {
  isGraphDarkMode,
  GRAPH_EDGE_STROKE_WIDTH,
  GRAPH_RUN_STROKE_WIDTH,
  GRAPH_SELECTION_COLOR,
} from './edge-style'

function posOf(p?: Position | string | null): 'left' | 'right' | 'top' | 'bottom' {
  if (p === Position.Left || p === 'left') return 'left'
  if (p === Position.Top || p === 'top') return 'top'
  if (p === Position.Bottom || p === 'bottom') return 'bottom'
  return 'right'
}

function nodeBox(n: {
  measured?: { width?: number; height?: number }
  width?: number
  height?: number
}, at: { x: number; y: number }): Box {
  const w = n.measured?.width ?? n.width ?? 220
  const h = n.measured?.height ?? n.height ?? 72
  return {
    left: at.x,
    top: at.y,
    right: at.x + w,
    bottom: at.y + h,
  }
}

export function GraphSmartEdge(props: EdgeProps) {
  const {
    id,
    source,
    target,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    style,
    markerEnd,
    label,
    sourceHandleId,
    data,
  } = props


  const nodes = useNodes()
  const railIndex = useStore((s) => {
    let idx = 0
    let found = false
    for (const e of s.edges) {
      if (e.source !== source || e.target !== target) continue
      if (e.id === id) {
        found = true
        break
      }
      idx++
    }
    if (!found || idx <= 0) return 0
    const k = Math.ceil(idx / 2)
    return idx % 2 === 1 ? k : -k
  })

  const { path, labelX, labelY } = useMemo(() => {
    const sDir = posOf(sourcePosition)
    const tDir = posOf(targetPosition)
    const isBack =
      (sDir === 'right' && targetX < sourceX - 8) ||
      (sDir === 'left' && targetX > sourceX + 8) ||
      (sDir === 'bottom' && targetY < sourceY - 8) ||
      (sDir === 'top' && targetY > sourceY + 8)

    const x0 = Math.min(sourceX, targetX)
    const x1 = Math.max(sourceX, targetX)
    const y0 = Math.min(sourceY, targetY)
    const y1 = Math.max(sourceY, targetY)
    const margin = 48 + Math.abs(railIndex) * 16

    const obstacles: Box[] = []
    let sourceBox: Box | undefined
    let targetBox: Box | undefined
    let bandHit = false
    // 仅在存在循环体子节点（parentId）时才按需构建父节点查找表，避免在千节点主图中每条边重复 new Map
    let byId: Map<string, (typeof nodes)[number]> | undefined
    const absolute = (n: (typeof nodes)[number]): { x: number; y: number } => {
      if (!n.parentId) return n.position
      byId ??= new Map(nodes.map((item) => [item.id, item]))
      const parent = byId.get(n.parentId)
      if (!parent) return n.position
      const at = absolute(parent)
      return { x: at.x + n.position.x, y: at.y + n.position.y }
    }

    for (const n of nodes) {
      if (n.hidden) continue
      // 注释虚线框仅展示，不参与绕障碰撞（否则边会贴框绕行出 U 勾）
      if (isAnnotationNode(n)) continue
      const box = nodeBox(n, absolute(n))
      if (n.id === source) {
        sourceBox = box
        continue
      }
      if (n.id === target) {
        targetBox = box
        continue
      }
      // 非回边只需收集水平走廊范围内的节点，跳过全图其余列节点
      if (!isBack && (box.right < x0 - margin || box.left > x1 + margin)) continue
      // 循环体内的边在所在容器的框内走，容器本身不是障碍
      if (isInsideContainer(source, n.id) || isInsideContainer(target, n.id)) continue
      if (!(box.right < x0 || box.left > x1 || box.bottom < y0 || box.top > y1)) {
        bandHit = true
      }
      obstacles.push(box)
    }

    const canNative = !isBack && !bandHit && railIndex === 0
    // 无碰撞且非回边时直接走原生平滑折线，跳过 routeAroundNodes 绕障计算
    if (canNative) {
      const [p, lx, ly] = getSmoothStepPath({
        sourceX,
        sourceY,
        targetX,
        targetY,
        sourcePosition,
        targetPosition,
        borderRadius: 16,
        offset: 16,
      })
      return { path: p, labelX: lx, labelY: ly }
    }

    // 回边 / 走廊有障：走绕障（回边强制 U 型从 source 出边离开再进 target 入边）
    const railRaw =
      data && typeof data === 'object' && data !== null && 'rail' in data
        ? String((data as { rail?: string }).rail ?? '')
        : ''
    // top/bottom 用于出口在左右边，left/right 用于出口在上下边（纵向图）
    const preferRail = parseEdgeRail(railRaw) ?? 'auto'

    const routed = routeAroundNodes(sourceX, sourceY, targetX, targetY, obstacles, {
      railIndex,
      sourceDir: sDir,
      targetDir: tDir,
      sourceBox,
      targetBox,
      preferRail,
    })

    return {
      path: pointsToRoundedPath(routed.points, 16),
      labelX: routed.labelX,
      labelY: routed.labelY,
    }
  }, [
    nodes,
    source,
    target,
    sourceX,
    sourceY,
    targetX,
    targetY,
    sourcePosition,
    targetPosition,
    railIndex,
    data,
  ])

  const when =
    (typeof sourceHandleId === 'string' && sourceHandleId) ||
    (data && typeof data === 'object' && data !== null && 'when' in data
      ? String((data as { when?: string }).when ?? '')
      : '')
  const showLabel =
    label != null &&
    String(label) !== '' &&
    !isHandleBrandedWhen(when) &&
    !isHandleBrandedWhen(typeof label === 'string' ? label : undefined)

  const isDark = isGraphDarkMode()
  const finalStyle = useMemo(() => {
    const defaultColor = isDark ? '#71717a' : '#94a3b8'
    const baseColor = style?.stroke || defaultColor
    if (!props.selected) {
      return {
        ...style,
        stroke: baseColor,
        strokeWidth: style?.strokeWidth ?? GRAPH_EDGE_STROKE_WIDTH,
      }
    }
    return {
      ...style,
      stroke: GRAPH_SELECTION_COLOR,
      strokeWidth: GRAPH_RUN_STROKE_WIDTH,
      filter: `drop-shadow(0 0 4px color-mix(in srgb, ${GRAPH_SELECTION_COLOR} 45%, transparent))`,
    }
  }, [props.selected, style, isDark])

  return (
    <>
      <BaseEdge id={id} path={path} style={finalStyle} markerEnd={markerEnd} />
      {showLabel ? (
        <EdgeLabelRenderer>
          <div
            className="nodrag nopan pointer-events-none absolute select-none text-[10px] font-medium tracking-wide text-foreground shadow-sm backdrop-blur-sm"
            style={{
              transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)`,
              background: 'hsl(var(--card) / 0.9)',
              padding: '1px 6px',
              borderRadius: 9999,
              border: '1px solid hsl(var(--border) / 0.8)',
              zIndex: 1,
            }}
          >
            {label}
          </div>
        </EdgeLabelRenderer>
      ) : null}
    </>
  )
}

/** 覆盖内置边类型，统一走避障路由 */
export const graphEdgeTypes = {
  default: GraphSmartEdge,
  smoothstep: GraphSmartEdge,
  step: GraphSmartEdge,
  straight: GraphSmartEdge,
}
