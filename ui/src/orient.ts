/**
 * orient — 节点端点句柄所在边（graph.EndpointSides）：端点 ID（In / Out / Case0 / Error 等）→ left / right / top / bottom。
 * 图没有整体方向，节点也没有统一的入口 / 出口方向：每个端点各自写明所在边。
 * 保存时每个实例端点都必须写明（后端 node.Registry.CleanEndpointSides），保存前由 withEndpointSides 按 resolveEndpointSides 补齐。
 * 唯一约束：任何出口都不能与该节点的入口同边。
 */
import { GRAPH_ENDPOINT_ERROR } from './types'

/** 端点句柄在节点卡片上所在的边，也是回边轨道的取值（对应 Go 端 graph.SideLeft 等） */
export type Side = 'left' | 'right' | 'top' | 'bottom'

/** 单个节点的端点所在边：端点 ID → 边 */
export type EndpointSides = Record<string, Side>

/** 节点实例实际拥有的端点 ID（入口、出口），由 resolveGraphNodeEndpoints 的结果经 endpointIds 得到 */
export type NodeEndpointIds = { inputs: readonly string[]; outputs: readonly string[] }

export const SIDES: Side[] = ['left', 'right', 'top', 'bottom']

export const SIDE_LABEL: Record<Side, string> = {
  left: '左',
  right: '右',
  top: '上',
  bottom: '下',
}

/** 未写明的端点补齐时使用的边：入口左、出口右；节点另有其他出口时 Error 放下边 */
export const DEFAULT_INPUT_SIDE: Side = 'left'
export const DEFAULT_OUTPUT_SIDE: Side = 'right'
export const DEFAULT_ERROR_SIDE: Side = 'bottom'

export function parseSide(v: unknown): Side | null {
  if (v === 'left' || v === 'right' || v === 'top' || v === 'bottom') return v
  return null
}

/** 顺时针下一条边：top → right → bottom → left → top */
export function clockwise(side: Side): Side {
  switch (side) {
    case 'top':
      return 'right'
    case 'right':
      return 'bottom'
    case 'bottom':
      return 'left'
    case 'left':
      return 'top'
  }
}

/** 边所在轴：左右为 x（句柄沿纵向排布），上下为 y（沿横向排布） */
export function sideAxis(side: Side): 'x' | 'y' {
  return side === 'left' || side === 'right' ? 'x' : 'y'
}

/** 解析出的端点列表 → 端点 ID */
export function endpointIds(set: { inputs: readonly { id: string }[]; outputs: readonly { id: string }[] }): NodeEndpointIds {
  return { inputs: set.inputs.map((e) => e.id), outputs: set.outputs.map((e) => e.id) }
}

/**
 * 读取布局里的一项：只保留取值合法的端点条目。
 * 不补齐、不判断入出同边（此处不知道哪些键是入口），这些由 resolveEndpointSides 按实例端点处理。
 */
export function parseEndpointSides(raw: unknown): EndpointSides {
  const out: EndpointSides = {}
  if (!raw || typeof raw !== 'object') return out
  const o = raw as Record<string, unknown>
  for (const [id, v] of Object.entries(o)) {
    const side = parseSide(v)
    if (id && side) out[id] = side
  }
  return out
}

/**
 * 按实例端点补齐并规整，结果恰好覆盖 eps 中的全部端点：
 *  - 不属于实例的条目（删掉的 switch 分支、旧格式遗留的 Out 等）丢弃；
 *  - 缺失的入口取 left；
 *  - 缺失的 Error 出口：节点另有其他出口时取 bottom，否则按普通出口处理（如 stop_error）；
 *  - 缺失的其他出口：沿用 Out 条目所在边（含旧格式 switch 的主出口），否则沿用第一个已写明的非 Error 出口，都没有时取 right；
 *  - 出口与任一入口同边时，顺时针移到下一条不被入口占用的边。
 */
export function resolveEndpointSides(sides: EndpointSides | null | undefined, eps: NodeEndpointIds): EndpointSides {
  const raw = sides ?? {}
  const out: EndpointSides = {}
  for (const id of eps.inputs) out[id] = parseSide(raw[id]) ?? DEFAULT_INPUT_SIDE
  const inSides = new Set(eps.inputs.map((id) => out[id]))
  const written = eps.outputs.find((id) => id !== GRAPH_ENDPOINT_ERROR && parseSide(raw[id]))
  const mainSide = parseSide(raw.Out) ?? (written ? raw[written] : undefined) ?? DEFAULT_OUTPUT_SIDE
  const errorOnly = eps.outputs.every((id) => id === GRAPH_ENDPOINT_ERROR)
  for (const id of eps.outputs) {
    let side = parseSide(raw[id]) ?? (id === GRAPH_ENDPOINT_ERROR && !errorOnly ? DEFAULT_ERROR_SIDE : mainSide)
    // 入口最多占三条边（四条边都被入口占用时无解，保持原值，由后端校验报告）
    for (let i = 0; i < 3 && inSides.has(side); i++) side = clockwise(side)
    out[id] = side
  }
  return out
}

/** 端点所在边；sides 已经 resolveEndpointSides 时总能取到，未知端点按入口左 / 出口右回落 */
export function endpointSide(sides: EndpointSides | null | undefined, id: string, dir: 'in' | 'out'): Side {
  return parseSide(sides?.[id]) ?? (dir === 'in' ? DEFAULT_INPUT_SIDE : DEFAULT_OUTPUT_SIDE)
}

/**
 * 设置一个端点所在边，返回补齐后的结果。
 * 入口改到出口所在的边时，这些出口移到该入口原来的边（两者对调）；出口不能选入口所在边，此时保持不变。
 */
export function setEndpointSide(sides: EndpointSides | null | undefined, eps: NodeEndpointIds, id: string, side: Side): EndpointSides {
  const cur = resolveEndpointSides(sides, eps)
  if (eps.inputs.includes(id)) {
    const old = cur[id]
    const next: EndpointSides = { ...cur, [id]: side }
    for (const out of eps.outputs) if (cur[out] === side) next[out] = old
    return resolveEndpointSides(next, eps)
  }
  if (!eps.outputs.includes(id) || eps.inputs.some((i) => cur[i] === side)) return cur
  return { ...cur, [id]: side }
}

/** 整个节点顺时针旋转 90°（快捷键 R）：每个端点移到顺时针的下一条边，入出不同边的约束保持成立 */
export function rotateEndpointSides(sides: EndpointSides | null | undefined, eps: NodeEndpointIds): EndpointSides {
  const cur = resolveEndpointSides(sides, eps)
  return Object.fromEntries(Object.entries(cur).map(([id, side]) => [id, clockwise(side)]))
}
