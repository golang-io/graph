/**
 * GraphEdge — 自定义边：正交路径绕开节点；线可交叉；句柄已标分支时不在线上重复 label。
 */
import {
  BaseEdge,
  EdgeLabelRenderer,
  getSmoothStepPath,
  Position,
  useStore,
  useStoreApi,
  type Edge,
  type EdgeProps,
} from '@xyflow/react'
import { createContext, useContext, useMemo } from 'react'
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

/**
 * 画布已提交布局的版本上下文（由 GraphCanvas 提供）。
 * 节点拖拽过程中仅与被拖节点相连的边因 sourceX/Y 或 targetX/Y 变化实时重算路径，
 * 其余无关边不订阅 60Hz 的全量 nodes 更新；待拖拽结束或布局写回时再通过此版本触发重算。
 *
 * Committed layout version context so unconnected edges do not re-render at 60Hz during node dragging.
 */
export const GraphLayoutVersionContext = createContext<unknown>(null)

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

/**
 * 按 edges 数组引用缓存同源同宿兄弟边的轨道偏移（railIndex），
 * 将每条边在每次 store 更新时的 O(E) 线性扫描降为 O(1) 查表。
 */
const railIndexByEdges = new WeakMap<readonly Edge[], Map<string, number>>()

function getRailIndex(edges: readonly Edge[], id: string): number {
  let byId = railIndexByEdges.get(edges)
  if (!byId) {
    byId = new Map()
    const countByPair = new Map<string, number>()
    for (const e of edges) {
      const key = `${e.source}\0${e.target}`
      const idx = countByPair.get(key) ?? 0
      countByPair.set(key, idx + 1)
      if (idx > 0) {
        const k = Math.ceil(idx / 2)
        byId.set(e.id, idx % 2 === 1 ? k : -k)
      }
    }
    railIndexByEdges.set(edges, byId)
  }
  return byId.get(id) ?? 0
}

/**
 * 按 React Flow store 实例维护节点障碍包围盒缓存，并在同一同步渲染批次（microtask 周期内）共享：
 * - 同一渲染批次内的多条边直接复用当帧缓存（turnFresh === true）；
 * - 跨帧（如拖拽连续帧）时，对比 nodeLookup 中每个节点的 internalNode 引用（internalRefById），
 *   静止节点（引用未变）直接跳过，仅对当帧实际移动或缩放的节点重算包围盒，将每帧障碍更新开销降为 O(移动节点数)。
 */
type FlowObstacleCache = {
  nodes: Array<{ id: string; box: Box }>
  boxById: Map<string, Box>
  indexById: Map<string, number>
  internalRefById: Map<string, object>
  turnFresh: boolean
}

const obstacleCacheByStore = new WeakMap<object, FlowObstacleCache>()

function getFlowObstacleCache(store: ReturnType<typeof useStoreApi>): FlowObstacleCache {
  let cached = obstacleCacheByStore.get(store)
  if (cached?.turnFresh) return cached

  const state = store.getState()

  // 增量路径：节点集合未增删时，仅更新 internalNode 引用发生变化的节点（拖拽帧通常仅 1 个节点）
  if (cached && state.nodeLookup && state.nodeLookup.size > 0) {
    let structureChanged = false
    let visibleCount = 0
    for (const [id, n] of state.nodeLookup) {
      if (n.hidden || isAnnotationNode(n)) continue
      visibleCount++
      if (cached.internalRefById.get(id) === n) continue
      const idx = cached.indexById.get(id)
      if (idx === undefined) {
        structureChanged = true
        break
      }
      const at = n.internals?.positionAbsolute ?? n.position
      const box = nodeBox(n, at)
      cached.nodes[idx]!.box = box
      cached.boxById.set(id, box)
      cached.internalRefById.set(id, n)
    }
    if (!structureChanged && visibleCount === cached.nodes.length) {
      const ref = cached
      ref.turnFresh = true
      queueMicrotask(() => {
        ref.turnFresh = false
      })
      return ref
    }
  }

  const nodes: Array<{ id: string; box: Box }> = []
  const boxById = new Map<string, Box>()
  const indexById = new Map<string, number>()
  const internalRefById = new Map<string, object>()

  if (state.nodeLookup && state.nodeLookup.size > 0) {
    for (const [id, n] of state.nodeLookup) {
      if (n.hidden || isAnnotationNode(n)) continue
      const at = n.internals?.positionAbsolute ?? n.position
      const box = nodeBox(n, at)
      indexById.set(id, nodes.length)
      nodes.push({ id, box })
      boxById.set(id, box)
      internalRefById.set(id, n)
    }
  } else {
    let byId: Map<string, (typeof state.nodes)[number]> | undefined
    const absolute = (n: (typeof state.nodes)[number]): { x: number; y: number } => {
      if (!n.parentId) return n.position
      byId ??= new Map(state.nodes.map((item) => [item.id, item]))
      const parent = byId.get(n.parentId)
      if (!parent) return n.position
      const at = absolute(parent)
      return { x: at.x + n.position.x, y: at.y + n.position.y }
    }
    for (const n of state.nodes) {
      if (n.hidden || isAnnotationNode(n)) continue
      const box = nodeBox(n, absolute(n))
      indexById.set(n.id, nodes.length)
      nodes.push({ id: n.id, box })
      boxById.set(n.id, box)
      internalRefById.set(n.id, n)
    }
  }

  const nextCache: FlowObstacleCache = {
    nodes,
    boxById,
    indexById,
    internalRefById,
    turnFresh: true,
  }
  obstacleCacheByStore.set(store, nextCache)
  queueMicrotask(() => {
    nextCache.turnFresh = false
  })
  return nextCache
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

  const store = useStoreApi()
  const layoutVersion = useContext(GraphLayoutVersionContext)
  const railIndex = useStore((s) => getRailIndex(s.edges, id))

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

    const snap = getFlowObstacleCache(store)
    const sourceBox = snap.boxById.get(source)
    const targetBox = snap.boxById.get(target)
    const obstacles: Box[] = []
    let bandHit = false

    for (const item of snap.nodes) {
      if (item.id === source || item.id === target) continue
      const box = item.box
      // 非回边只需收集水平走廊范围内的节点，跳过全图其余列节点
      if (!isBack && (box.right < x0 - margin || box.left > x1 + margin)) continue
      // 循环体内的边在所在容器的框内走，容器本身不是障碍
      if (isInsideContainer(source, item.id) || isInsideContainer(target, item.id)) continue
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
    store,
    layoutVersion,
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
