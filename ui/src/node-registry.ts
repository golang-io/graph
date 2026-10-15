import type { GraphEndpointSpec } from './endpoints'
import type { GraphNodeSpec } from './node-spec'
/**
 * node-registry — 业务节点类型注册表。
 *
 * 共享画布只认几何角色（end/step/switch/…）；产品通过 registry 注入
 * icon / 副标题 / hint，以及 type → role 映射，避免 GraphNodes 里堆产品 if/else。
 */
import type { LucideIcon } from 'lucide-react'
import { Braces } from 'lucide-react'

/** React Flow node.type（几何壳） */
export type GraphNodeRole =
  | 'start'
  | 'end'
  | 'step'
  | 'switch'
  | 'map'
  | 'merge'
  | 'parallel'
  | 'annotation'

/** 单类业务节点的外观与角色 */
export type NodeTypeDef = {
  type: string
  spec?: GraphNodeSpec
  displayEndpoints?: GraphEndpointSpec
  /** 映射到共享几何壳 */
  role: GraphNodeRole
  icon?: LucideIcon
  /** Tailwind 色类，如 text-blue-600 dark:text-blue-400 */
  iconClass?: string
  /** 默认副标题（无 hint 时） */
  subtitle?: string
  /** 副标题旁动态提示；返回空则用 subtitle */
  hint?: (config: Record<string, unknown>) => string
}

export type NodeTypeRegistry = {
  get(type: string): NodeTypeDef | undefined
  roleOf(type: string): GraphNodeRole
  /** 合并另一张表（后者覆盖同名 type） */
  merge(...others: NodeTypeRegistry[]): NodeTypeRegistry
  list(): NodeTypeDef[]
}

/** 内置控制节点目录 ID 前缀；图 nodes[].type 等于目录 id。 */
export const INNER_NODE_PREFIX = '__INNER__.'

/** 去掉内置前缀，得到 end/switch 这类几何角色名。 */
export function catalogKind(type: string): string {
  return type.startsWith(INNER_NODE_PREFIX) ? type.slice(INNER_NODE_PREFIX.length) : type
}

function roleFromType(type: string): GraphNodeRole {
  switch (catalogKind(type)) {
    case 'start':
      return 'start'
    case 'end':
      return 'end'
    case 'switch':
      return 'switch'
    case 'map':
      return 'map'
    case 'merge':
      return 'merge'
    case 'parallel':
      return 'parallel'
    case 'annotation':
      return 'annotation'
    default:
      return 'step'
  }
}

/** 从定义列表创建注册表 */
export function createNodeRegistry(defs: NodeTypeDef[]): NodeTypeRegistry {
  const map = new Map<string, NodeTypeDef>()
  for (const d of defs) {
    if (!d.type.trim()) continue
    map.set(d.type, d)
  }
  const api: NodeTypeRegistry = {
    get(type) {
      return map.get(type)
    },
    roleOf(type) {
      return map.get(type)?.role ?? roleFromType(type)
    },
    merge(...others) {
      const merged = [...map.values()]
      for (const o of others) merged.push(...o.list())
      return createNodeRegistry(merged)
    },
    list() {
      return [...map.values()]
    },
  }
  return api
}

export const emptyNodeRegistry = createNodeRegistry([])

export function resolveStepAppearance(
  nodeType: string,
  config: Record<string, unknown>,
  registry?: NodeTypeRegistry | null,
): { icon: LucideIcon; iconClass: string; subtitle: string; hint: string } {
  const def = registry?.get(nodeType)
  if (def?.icon) {
    const subtitle = def.subtitle || nodeType
    const hint = (def.hint?.(config) || '').trim() || subtitle
    return {
      icon: def.icon,
      iconClass: def.iconClass || 'text-violet-600 dark:text-violet-400',
      subtitle,
      hint,
    }
  }
  return {
    icon: Braces,
    iconClass: 'text-violet-600 dark:text-violet-400',
    subtitle: nodeType,
    hint: nodeType,
  }
}
