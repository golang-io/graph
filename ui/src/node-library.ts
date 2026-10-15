/**
 * node-library — 可插拔节点库：分组目录 + 默认 config。
 *
 * 与 NodeTypeRegistry（外观/几何角色）分离：
 * - Registry：画布怎么画
 * - Library：节点库里能加什么
 *
 * graph-react 通过 GraphNodePack 组合核心节点与业务节点。
 */
export type NodeLibraryItem = {
  /** 节点库条目 ID；preset 可与实际 graph type 不同。 */
  type: string
  /** 实际写入 graph.nodes[].type；省略时等于 type。 */
  nodeType?: string
  /** 直接文案（无 i18n 时） */
  label?: string
  /** i18n key，优先于 label */
  labelKey?: string
  /** 分组稳定 id；有 groupLabel 时直接展示，否则走 i18n key */
  groupKey: string
  /** 后端下发的分组标题，优先于 i18n */
  groupLabel?: string
  /** 新建时的默认 config */
  defaultConfig?: () => Record<string, unknown>
  disabled?: boolean
  disabledReason?: string
}

export type NodeLibrary = {
  /** 库标识，便于调试 */
  id: string
  items: readonly NodeLibraryItem[]
}

/** 合并多库：后库追加；同 type 后写覆盖前写（业务库可覆盖核心默认） */
export function mergeNodeLibraries(...libs: NodeLibrary[]): NodeLibrary {
  const byType = new Map<string, NodeLibraryItem>()
  const order: string[] = []
  for (const lib of libs) {
    for (const it of lib.items) {
      if (!byType.has(it.type)) order.push(it.type)
      byType.set(it.type, it)
    }
  }
  return {
    id: libs.map((l) => l.id).filter(Boolean).join('+') || 'merged',
    items: order.map((t) => byType.get(t)!),
  }
}

/** 按 groupKey 分组，保持首次出现顺序 */
export function groupLibraryItems(
  items: readonly NodeLibraryItem[],
): Array<{ groupKey: string; groupLabel?: string; items: NodeLibraryItem[] }> {
  const order: string[] = []
  const map = new Map<string, NodeLibraryItem[]>()
  for (const it of items) {
    if (!map.has(it.groupKey)) {
      order.push(it.groupKey)
      map.set(it.groupKey, [])
    }
    map.get(it.groupKey)!.push(it)
  }
  return order.map((groupKey) => {
    const groupItems = map.get(groupKey)!
    return { groupKey, groupLabel: groupItems[0]?.groupLabel, items: groupItems }
  })
}

/** 解析新建默认 config */
export function libraryDefaultConfig(
  library: NodeLibrary,
  type: string,
): Record<string, unknown> {
  const hit = library.items.find((i) => i.type === type)
  return hit?.defaultConfig?.() ?? {}
}

/** 解析展示名（调用方注入 t） */
export function libraryItemLabel(
  item: NodeLibraryItem,
  t: (key: string) => string,
): string {
  if (item.labelKey) return t(item.labelKey)
  if (item.label) return item.label
  return item.type
}
