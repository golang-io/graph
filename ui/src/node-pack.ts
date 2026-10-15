import type { NodeTypeDef, NodeTypeRegistry } from './node-registry'
import { createNodeRegistry } from './node-registry'
import type { NodeLibrary, NodeLibraryItem } from './node-library'
import { mergeNodeLibraries } from './node-library'

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
