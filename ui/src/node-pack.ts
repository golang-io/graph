import type { NodeTypeDef, NodeTypeRegistry } from './node-registry'
import { createNodeRegistry } from './node-registry'
import type { NodeLibrary, NodeLibraryItem } from './node-library'
import { mergeNodeLibraries } from './node-library'
import type { GraphNodeSpec } from './node-spec'

/** 一个节点类型在 graph-react 中的唯一声明：外观、几何角色、节点库入口与默认配置。 */
export type GraphNodeDefinition = NodeTypeDef &
  Omit<NodeLibraryItem, 'type'> & {
    type: string
  }

/** 产品只组合 NodePack，不再分别维护 registry/library 两张容易漂移的表。 */
export type GraphNodePack = {
  id: string
  registry: NodeTypeRegistry
  library: NodeLibrary
  definitions: readonly GraphNodeDefinition[]
}

export function createGraphNodePack(
  id: string,
  definitions: readonly GraphNodeDefinition[],
  options?: {
    baseRegistry?: NodeTypeRegistry
    baseLibrary?: NodeLibrary
  },
): GraphNodePack {
  const ownRegistry = createNodeRegistry(definitions.filter((d) => !d.nodeType || d.type === d.nodeType))
  const ownLibrary: NodeLibrary = {
    id,
    items: definitions.map((definition) => ({
      type: definition.type,
      nodeType: definition.nodeType,
      label: definition.label,
      labelKey: definition.labelKey,
      groupKey: definition.groupKey,
      groupLabel: definition.groupLabel,
      defaultConfig: definition.defaultConfig,
      disabled: definition.disabled,
      disabledReason: definition.disabledReason,
    })),
  }
  return {
    id,
    definitions: [...definitions],
    registry: options?.baseRegistry
      ? options.baseRegistry.merge(ownRegistry)
      : ownRegistry,
    library: options?.baseLibrary
      ? mergeNodeLibraries(options.baseLibrary, ownLibrary)
      : ownLibrary,
  }
}

/**
 * 按落图种类查找种类描述：definitions 中同一 spec 的各条目（主条目与各 preset 条目）共用同一份 spec，
 * 且 nodeType 都等于 spec.ID，因此取第一条命中即可。pack 为空（节点目录尚未加载）时返回 undefined。
 * 页面层（Editor / RunView）统一经此查找，避免各自再写一遍 definitions.find。
 */
export function packSpecOf(pack: GraphNodePack | null | undefined, nodeType: string): GraphNodeSpec | undefined {
  return pack?.definitions.find((d) => d.nodeType === nodeType)?.spec
}

export function mergeGraphNodePacks(
  id: string,
  ...packs: readonly GraphNodePack[]
): GraphNodePack {
  const definitions = packs.flatMap((pack) => pack.definitions)
  const registries = packs.map((pack) => pack.registry)
  const registry = registries[0]?.merge(...registries.slice(1)) ?? createNodeRegistry([])
  return {
    id,
    definitions,
    registry,
    library: mergeNodeLibraries(...packs.map((pack) => pack.library)),
  }
}
