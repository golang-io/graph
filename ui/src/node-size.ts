/**
 * 节点卡片几何：尺寸常量、按类型与参数估算的卡片尺寸。React Flow 与 X6 两个渲染器、布局与对齐共用，
 * 视图组件（GraphNodes.tsx）也从这里取尺寸，保证估算值与实际渲染一致。
 * Node card geometry shared by both renderers, layout and alignment.
 */
import { ANNOTATION_DEFAULT_SIZE } from './annotation-style'
import { resolveGraphNodeEndpoints, type GraphNodeSpec } from './endpoints'
import { catalogKind, emptyNodeRegistry, type GraphNodeRole, type NodeTypeRegistry } from './node-registry'
import { endpointIds, type NodeEndpointIds } from './orient'

export const CARD_W = 220
export const CARD_H = 72
/** 智能体节点略高：容纳 AI 徽章与模型副标题 */
export const AGENT_CARD_H = 84
export const ENDPOINT_W = 148
export const ENDPOINT_H = CARD_H

/**
 * 开始/结束节点外框圆角：开始节点左侧半圆弧、右侧 rounded-xl；结束节点左右镜像。React Flow 与 X6 渲染器共用。
 * 半圆弧半径必须写成 ENDPOINT_H / 2（36px），不能用 rounded-full：CSS 在相邻圆角之和超过边长时会把四个角按同一比例缩小，
 * 9999px 会把另一侧的 12px 圆角压到接近 0。
 * Endpoint shell radius shared by both renderers; the arc must use ENDPOINT_H / 2 instead of rounded-full,
 * otherwise CSS proportionally shrinks the small corners on the other side to ~0.
 */
export const ENDPOINT_SHAPE_CLASS = {
  start: 'rounded-l-[36px] rounded-r-xl',
  end: 'rounded-l-xl rounded-r-[36px]',
} as const

/**
 * 多句柄分支卡高度：IN1/IN2、IF/ELSE 等沿边排布时抬高卡片，
 * 避免贴圆角、且 merge/map/switch 同口数同高（否则单路出/入中心错位 → 线崴）。
 */
export function branchCardHeight(handleCount: number): number {
  const n = Math.max(2, handleCount)
  return Math.max(96, 44 + n * 26)
}

/** 取节点种类的端点声明（注册表 Spec，或自定义节点的 displayEndpoints）。 */
export function specOf(registry: NodeTypeRegistry | null | undefined, nodeType?: string): Pick<GraphNodeSpec, 'Endpoints' | 'Renderer'> | undefined {
  const { spec, displayEndpoints } = registry?.get(nodeType || '') ?? {}
  return spec ?? (displayEndpoints ? { Endpoints: displayEndpoints } : undefined)
}

/**
 * 节点实例实际拥有的端点 ID（入口 + 按配置启用的出口，如 switch 的分支），与后端 node.Registry.NodeEndpoints 对应。
 * 注册表里没有该种类时返回 undefined，调用方据此保留已有的端点条目而不是清空。
 */
export function instanceEndpointIds(registry: NodeTypeRegistry | null | undefined, nodeType: string, config?: Record<string, unknown> | null): NodeEndpointIds | undefined {
  const spec = specOf(registry, nodeType)
  return spec ? endpointIds(resolveGraphNodeEndpoints(nodeType, spec.Endpoints, config ?? {}, spec.Renderer)) : undefined
}

/** 业务 type → RF 几何壳；可选 registry（股票/短剧自定义 role） */
export function rfTypeFor(
  nodeType: string,
  registry?: NodeTypeRegistry | null,
): GraphNodeRole {
  return (registry ?? emptyNodeRegistry).roleOf(nodeType)
}

/** 布局 / 吸附用：按类型 + config 估真实卡片高度（与视图公式一致） */
export function cardHeightFor(nodeType: string, config?: Record<string, unknown>, registry?: NodeTypeRegistry | null): number {
  const kind = rfTypeFor(nodeType, registry)
  if (kind === 'annotation') return ANNOTATION_DEFAULT_SIZE.h
  const cfg = config ?? {}
  const spec = specOf(registry, nodeType)
  const resolved = resolveGraphNodeEndpoints(nodeType, spec?.Endpoints, cfg, spec?.Renderer)
  if (kind === 'merge') return branchCardHeight(resolved.inputs.length)
  if (kind === 'parallel') return branchCardHeight(resolved.outputs.length)
  if (kind === 'map') return branchCardHeight(resolved.outputs.length)
  if (kind === 'switch') return branchCardHeight(Math.max(2, resolved.outputs.length))
  // 智能体略高于普通 step，布局/吸附与视图一致
  if (catalogKind(nodeType) === 'llm' || nodeType === 'agent') return AGENT_CARD_H
  return NODE_SIZE[kind].h
}

const BRANCH_H = branchCardHeight(2)

export const NODE_SIZE = {
  start: { w: ENDPOINT_W, h: ENDPOINT_H },
  end: { w: ENDPOINT_W, h: ENDPOINT_H },
  step: { w: CARD_W, h: CARD_H },
  switch: { w: CARD_W, h: BRANCH_H },
  map: { w: CARD_W, h: BRANCH_H },
  merge: { w: CARD_W, h: BRANCH_H },
  parallel: { w: CARD_W, h: BRANCH_H },
  annotation: { w: ANNOTATION_DEFAULT_SIZE.w, h: ANNOTATION_DEFAULT_SIZE.h },
} as const

/**
 * 编辑节点的卡片尺寸，React Flow 与 X6 两个渲染器共用：宽度按节点角色取 NODE_SIZE，
 * 高度按类型与参数估算（出口数等）。展开容器的尺寸由 fitContainers 另算。
 * Card size shared by both renderers; expanded containers are sized by fitContainers.
 */
export function editorNodeSize(n: { type: string; config?: Record<string, unknown> }, registry?: NodeTypeRegistry | null): { w: number; h: number } {
  return { w: NODE_SIZE[rfTypeFor(n.type, registry)]?.w ?? CARD_W, h: cardHeightFor(n.type, n.config, registry) }
}
