/**
 * layout.ts — dagre 自动排布（固定从左到右）；近共线边句柄中心吸附，避免折角。
 * 不依赖 graph-adapter，避免循环引用。
 */
import dagre from '@dagrejs/dagre'
import type { Edge, Node } from '@xyflow/react'
import type { GraphUI } from './types'
import type { GraphNodePack } from './node-pack'
import { graphToFlow } from './graph-adapter'
import { isAnnotationNode } from './document'
import { cardHeightFor, NODE_SIZE, rfTypeFor } from './node-size'
import { documentFromProjection, projectDocument } from './document'
import { BODY_HEADER, BODY_MIN_SIZE, BODY_PAD, SCOPE_SEP } from './body-scope'

/** 布局 / 吸附：nodeType + 可选 config（多句柄卡高度依赖 config） */
type LayoutNodeData = { nodeType: string; config?: Record<string, unknown> }

function sizeOf(n: Node<LayoutNodeData>): { w: number; h: number } {
  const kind = rfTypeFor(n.data.nodeType)
  return { w: n.width ?? NODE_SIZE[kind].w, h: n.height ?? cardHeightFor(n.data.nodeType, n.data.config) }
}

/**
 * 交叉轴：与流向垂直的坐标轴。自动布局固定从左到右，交叉轴为 y；保留 x 供纵向排布的调用方使用。
 * 分支上下（或左右）排序、共线吸附都只改交叉轴坐标。
 */
type CrossAxis = 'x' | 'y'

export function layoutWithDagre<T extends LayoutNodeData>(
  nodes: Node<T>[],
  edges: Edge[],
): Node<T>[] {
  // 注释框不参与自动布局，原位保留
  const annotations = nodes.filter((n) => isAnnotationNode(n))
  const flow = nodes.filter((n) => !isAnnotationNode(n))
  const g = new dagre.graphlib.Graph()
  g.setDefaultEdgeLabel(() => ({}))
  g.setGraph({ rankdir: 'LR', nodesep: 80, ranksep: 120, marginx: 32, marginy: 40 })
  for (const n of flow) {
    const sz = sizeOf(n)
    g.setNode(n.id, { width: sz.w, height: sz.h })
  }
  for (const e of edges) {
    if (flow.some((n) => n.id === e.source) && flow.some((n) => n.id === e.target)) {
      g.setEdge(e.source, e.target)
    }
  }
  dagre.layout(g)
  const laid = flow.map((n) => {
    const p = g.node(n.id)
    const sz = sizeOf(n)
    return {
      ...n,
      position: { x: p.x - sz.w / 2, y: p.y - sz.h / 2 },
    }
  })
  // IF/true 出边目标应在上方，避免与 ELSE/false 交叉（如动量演示流收涨/收跌）
  const ordered = orderTrueBranchAbove(laid, edges, 'y')
  return [...annotations, ...snapCollinearHandles(ordered, edges, 'y')]
}

/** 边的分支标签：true|false（兼容 IF/ELSE 与 sourceHandle） */
function edgeBranchWhen(e: Edge): 'true' | 'false' | '' {
  const raw =
    (typeof e.sourceHandle === 'string' && e.sourceHandle) ||
    (e.data as { when?: string } | undefined)?.when ||
    (typeof e.label === 'string' ? e.label : '') ||
    ''
  const w = String(raw).trim().toLowerCase()
  if (w === 'true' || w === 'if') return 'true'
  if (w === 'false' || w === 'else') return 'false'
  return ''
}

/**
 * 对每个 true/false 双出边节点：若 true 目标在 false 目标之后（交叉轴坐标更大），互换二者交叉轴坐标，
 * 并递归互换各自「独占」后继（不含汇合到同一节点的边，如共同指向 end），
 * 使整条 IF 链在上（纵向图在左）、ELSE 链在下（纵向图在右），消除交叉。
 */
export function orderTrueBranchAbove<T extends LayoutNodeData>(
  nodes: Node<T>[],
  edges: Edge[],
  cross: CrossAxis = 'y',
): Node<T>[] {
  const byId = new Map(nodes.map((n) => [n.id, { ...n, position: { ...n.position } }]))
  const outs = new Map<string, Edge[]>()
  for (const e of edges) {
    const list = outs.get(e.source) ?? []
    list.push(e)
    outs.set(e.source, list)
  }

  const swapCross = (aId: string, bId: string) => {
    const a = byId.get(aId)
    const b = byId.get(bId)
    if (!a || !b) return
    const av = a.position[cross]
    byId.set(aId, { ...a, position: { ...a.position, [cross]: b.position[cross] } })
    byId.set(bId, { ...b, position: { ...b.position, [cross]: av } })
  }

  /** 沿 true 优先的独占后继（出度目标不被另一分支共享） */
  const exclusiveNext = (fromId: string, otherBranchRoots: Set<string>): string | null => {
    const list = outs.get(fromId) ?? []
    const candidates = list
      .map((e) => e.target)
      .filter((tid) => tid && !otherBranchRoots.has(tid))
    // 优先 true 出边，否则取第一条不汇合的
    const trueEdge = list.find((e) => edgeBranchWhen(e) === 'true')
    if (trueEdge && !otherBranchRoots.has(trueEdge.target)) return trueEdge.target
    return candidates[0] ?? null
  }

  for (const [, list] of outs) {
    const tEdge = list.find((e) => edgeBranchWhen(e) === 'true')
    const fEdge = list.find((e) => edgeBranchWhen(e) === 'false')
    if (!tEdge || !fEdge || tEdge.target === fEdge.target) continue
    const tId = tEdge.target
    const fId = fEdge.target
    const tNode = byId.get(tId)
    const fNode = byId.get(fId)
    if (!tNode || !fNode) continue
    if (tNode.position[cross] <= fNode.position[cross]) continue

    // 互换 true/false 目标，并沿链互换独占后继（买入/卖出等）
    let a: string | null = tId
    let b: string | null = fId
    const visited = new Set<string>()
    while (a && b && a !== b && !visited.has(a) && !visited.has(b)) {
      visited.add(a)
      visited.add(b)
      swapCross(a, b)
      const nextA = exclusiveNext(a, new Set([b, fId, tId]))
      const nextB = exclusiveNext(b, new Set([a, fId, tId]))
      // 若后继汇合（同指向 end），停止
      if (!nextA || !nextB || nextA === nextB) break
      a = nextA
      b = nextB
    }
  }

  return nodes.map((n) => byId.get(n.id) ?? n)
}

/**
 * 近共线连线：若两端句柄中心在交叉轴上只差几像素（常见于旧布局高度差），吸附到同一条线，
 * 使 default/smoothstep/step 画成直线而非折角。横向图对齐 y，纵向图对齐 x。
 */
export function snapCollinearHandles<T extends LayoutNodeData>(
  nodes: Node<T>[],
  edges: Edge[],
  cross: CrossAxis = 'y',
): Node<T>[] {
  // position 用可变副本：@xyflow 的 XYPosition.y 为 readonly，不能原地赋值
  const next = new Map(
    nodes.map((n) => [n.id, { ...n, position: { x: n.position.x, y: n.position.y } }]),
  )
  const main: CrossAxis = cross === 'y' ? 'x' : 'y'
  const extent = (n: Node<T>) => (cross === 'y' ? sizeOf(n).h : sizeOf(n).w)
  for (const e of edges) {
    const a = next.get(e.source)
    const b = next.get(e.target)
    if (!a || !b) continue
    const ae = extent(a)
    const be = extent(b)
    const ac = a.position[cross] + ae / 2
    const bc = b.position[cross] + be / 2
    const dCross = Math.abs(ac - bc)
    const dMain = Math.abs(a.position[main] - b.position[main])
    // 仅修正「几乎该共线」却差几到二十多像素的情况；大幅错开视为刻意分支，不碰
    if (dCross < 0.5 || dCross > 28 || dMain < Math.max(80, dCross * 3)) continue
    const mid = (ac + bc) / 2
    next.set(e.source, { ...a, position: { ...a.position, [cross]: mid - ae / 2 } })
    next.set(e.target, { ...b, position: { ...b.position, [cross]: mid - be / 2 } })
  }
  return nodes.map((n) => next.get(n.id) ?? n)
}

/**
 * 整图自动布局。各作用域分别排布、由内向外：先排每个展开循环体（坐标相对容器，从标题栏下方开始），
 * 得到容器尺寸后再排外层，容器按排好后的尺寸参与外层布局。结果经 documentFromProjection 写回。
 */
export function layoutGraph(document: GraphUI, pack: GraphNodePack): GraphUI {
  const { nodes, edges } = graphToFlow(document.Graph, document.UI, pack.registry)
  const scopeKey = (n: Node) => n.parentId ?? ''
  const depth = (scope: string) => (scope ? scope.split(SCOPE_SEP).length : 0)
  const scopes = [...new Set(nodes.map(scopeKey))].sort((a, b) => depth(b) - depth(a))
  const laidById = new Map<string, Node<LayoutNodeData>>()
  const containerSize = new Map<string, { w: number; h: number }>()
  for (const scope of scopes) {
    const members = nodes.filter((n) => scopeKey(n) === scope).map((n) => {
      const size = containerSize.get(n.id)
      return size ? { ...n, width: size.w, height: size.h } : n
    })
    const ids = new Set(members.map((n) => n.id))
    let laid = layoutWithDagre(members, edges.filter((e) => ids.has(e.source) && ids.has(e.target)))
    if (scope) {
      const minX = Math.min(...laid.map((n) => n.position.x)), minY = Math.min(...laid.map((n) => n.position.y))
      laid = laid.map((n) => ({ ...n, position: { x: n.position.x - minX + BODY_PAD, y: n.position.y - minY + BODY_HEADER } }))
      containerSize.set(scope, {
        w: Math.max(BODY_MIN_SIZE.w, ...laid.map((n) => n.position.x + sizeOf(n).w + BODY_PAD)),
        h: Math.max(BODY_MIN_SIZE.h, ...laid.map((n) => n.position.y + sizeOf(n).h + BODY_PAD)),
      })
    }
    for (const n of laid) laidById.set(n.id, n)
  }
  // 循环体内是相对容器的坐标，沿父链累加成画布绝对坐标后写回
  const absolute = (id: string): { x: number; y: number } => {
    const n = laidById.get(id)!
    if (!n.parentId) return n.position
    const at = absolute(n.parentId)
    return { x: at.x + n.position.x, y: at.y + n.position.y }
  }
  const projected = projectDocument(document)
  return documentFromProjection(
    projected.nodes.map((n) => (n.type === 'annotation' || !laidById.has(n.id) ? n : { ...n, position: absolute(n.id) })),
    projected.edges,
    document,
  )
}
