/**
 * X6 渲染器的连线路由：在 ensureX6NodeRegistered 中以 ROUTER_GRAPH_EDGE 注册，边与画布默认连线都使用它。
 * X6 renderer edge router, registered as ROUTER_GRAPH_EDGE.
 */
import { isBackEdge, parseEdgeRail, routeAroundNodes, type Box } from './edge-route'
import { isInsideContainer } from './body-scope'
import { GRAPH_DEFAULT_OUTPUT } from './types'

export const ROUTER_GRAPH_EDGE = 'graph-edge-router'

/** 注释底衬的 X6 形状名；路由据此跳过注释框（注释框不是障碍，连线可以从其上方经过）。 */
export const NODE_SHAPE_ANNOTATION = 'graph-annotation-node'

/**
 * 同一同步渲染批次（microtask 周期内）按 Graph 实例缓存节点包围盒与同源同宿兄弟边列表。
 * 在 1000+ 节点、1100+ 边的画布上，避免每条边重复执行 graph.getNodes() / node.getBBox() 与 graph.getEdges()，
 * 将整图连线路由复杂度从 O(E * (N + E)) 降为 O(N + E)。
 *
 * Per-turn cache of node bounding boxes and parallel sibling edge IDs keyed by Graph instance.
 */
type RouterTurnCache = {
  nodes: Array<{ id: string; box: Box }>
  boxById: Map<string, Box>
  siblingsByPair: Map<string, string[]>
}

const turnCacheByGraph = new WeakMap<object, RouterTurnCache>()

function getRouterTurnCache(graph: any): RouterTurnCache {
  let cached = turnCacheByGraph.get(graph)
  if (cached) return cached

  const nodes: Array<{ id: string; box: Box }> = []
  const boxById = new Map<string, Box>()
  for (const node of graph.getNodes()) {
    if (node.shape === NODE_SHAPE_ANNOTATION) continue
    const bbox = node.getBBox()
    const box: Box = {
      left: bbox.x,
      top: bbox.y,
      right: bbox.x + bbox.width,
      bottom: bbox.y + bbox.height,
    }
    nodes.push({ id: node.id, box })
    boxById.set(node.id, box)
  }

  const siblingsByPair = new Map<string, string[]>()
  for (const e of graph.getEdges()) {
    const s = e.getSourceCellId()
    const t = e.getTargetCellId()
    if (!s || !t) continue
    const key = `${s}\0${t}`
    const list = siblingsByPair.get(key)
    if (list) list.push(e.id)
    else siblingsByPair.set(key, [e.id])
  }

  cached = { nodes, boxById, siblingsByPair }
  turnCacheByGraph.set(graph, cached)
  queueMicrotask(() => {
    turnCacheByGraph.delete(graph)
  })
  return cached
}

/**
 * 图连线路由：严格保证入桩水平靠入（禁止从上方/下方垂直掉入导致箭头畸变），
 * 出桩水平右出（错误桩底部下出），并绕开节点本体（注释底衬不参与避障）。
 */
export function graphEdgeRouter(
  this: any,
  _vertices: any[],
  _options: any,
  edgeView: any,
) {
  const view = edgeView || this
  if (!view || !view.cell) return []

  const edge = view.cell
  const sourceId = edge.getSourceCellId()
  const targetId = edge.getTargetCellId()

  const sx = view.sourceAnchor?.x ?? (typeof view.sourcePoint?.x === 'number' ? view.sourcePoint.x : 0)
  const sy = view.sourceAnchor?.y ?? (typeof view.sourcePoint?.y === 'number' ? view.sourcePoint.y : 0)
  const tx = view.targetAnchor?.x ?? (typeof view.targetPoint?.x === 'number' ? view.targetPoint.x : sx)
  const ty = view.targetAnchor?.y ?? (typeof view.targetPoint?.y === 'number' ? view.targetPoint.y : sy)

  if (!sourceId || !targetId || (sx === tx && sy === ty)) {
    const midX = (sx + tx) / 2
    return [{ x: midX, y: sy }, { x: midX, y: ty }]
  }

  const sourcePort = edge.getSourcePortId() || GRAPH_DEFAULT_OUTPUT
  const sourceNode = edge.getSourceCell(), targetNode = edge.getTargetCell()
  const sourceDir = sourceNode?.getProp('ports')?.groups?.[sourceNode?.getPort(sourcePort)?.group]?.position ?? 'right'
  const targetDir = targetNode?.getProp('ports')?.groups?.[targetNode?.getPort(edge.getTargetPortId())?.group]?.position ?? 'left'

  const graph = view.graph
  let railIndex = 0
  const obstacles: Box[] = []
  let sourceBox: Box | undefined
  let targetBox: Box | undefined

  if (graph) {
    const snap = getRouterTurnCache(graph)
    sourceBox = snap.boxById.get(sourceId)
    targetBox = snap.boxById.get(targetId)

    const siblings = snap.siblingsByPair.get(`${sourceId}\0${targetId}`)
    if (siblings && siblings.length > 1) {
      const idx = siblings.indexOf(edge.id)
      if (idx > 0) {
        const k = Math.ceil(idx / 2)
        railIndex = idx % 2 === 1 ? k : -k
      }
    }

    // 非回边的候选折线与碰撞走廊仅落在 [min(sx,tx), max(sx,tx)] 附近（含 EXIT=28、PAD=14 与轨道偏移），
    // 预先按水平范围过滤可跳过全图 95% 以上不相邻列的节点。
    const back = isBackEdge(sx, sy, tx, ty, sourceDir)
    const margin = 48 + Math.abs(railIndex) * 16
    const xLo = Math.min(sx, tx) - margin
    const xHi = Math.max(sx, tx) + margin

    for (const item of snap.nodes) {
      if (item.id === sourceId || item.id === targetId) continue
      if (!back && (item.box.right < xLo || item.box.left > xHi)) continue
      // 循环体内的边从所在容器的框内走，容器本身不是障碍
      if (isInsideContainer(sourceId, item.id) || isInsideContainer(targetId, item.id)) continue
      obstacles.push(item.box)
    }
  }

  const res = routeAroundNodes(sx, sy, tx, ty, obstacles, {
    railIndex,
    sourceDir,
    targetDir,
    sourceBox,
    targetBox,
    // 回边轨道偏好（UI.EdgeRails），由 editorEdgeToX6 写入边 data
    preferRail: parseEdgeRail(edge.getData?.()?.rail) ?? 'auto',
  })

  return res.points.slice(1, -1)
}
