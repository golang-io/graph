/**
 * GraphPalette — 按 NodeLibrary 渲染可收拢分组列表（节点库浮层正文）。
 * 布局对齐 ModelTreeSelect：顶栏搜索 + 分组右侧数量；有搜索词时自动展开匹配组。
 */
import { useEffect, useMemo, useRef, useState } from 'react'
import { Input } from './host-ui'
import { useGraphI18n } from './i18n'
import {
  groupLibraryItems,
  libraryItemLabel,
  type NodeLibrary,
  type NodeLibraryItem,
} from './node-library'
import { GraphPaletteGroup } from './GraphPaletteGroup'

export type GraphPaletteProps = {
  library: NodeLibrary
  onAdd: (type: string) => void
}

/** groupKey 可能已是完整 i18n key（如 graph.palette.other），避免再套一层前缀。 */
function paletteGroupTitle(
  group: { groupKey: string; groupLabel?: string },
  t: (key: string) => string,
): string {
  if (group.groupLabel) return group.groupLabel
  if (group.groupKey.startsWith('graph.palette.')) return t(group.groupKey)
  return t(`graph.palette.${group.groupKey}`)
}

function itemMatchesQuery(item: NodeLibraryItem, groupTitle: string, q: string, t: (key: string) => string): boolean {
  const label = libraryItemLabel(item, t)
  return (
    item.type.toLowerCase().includes(q) ||
    label.toLowerCase().includes(q) ||
    groupTitle.toLowerCase().includes(q) ||
    item.groupKey.toLowerCase().includes(q)
  )
}

export function GraphPalette({ library, onAdd }: GraphPaletteProps) {
  const { t } = useGraphI18n()
  const searchRef = useRef<HTMLInputElement>(null)
  const [query, setQuery] = useState('')
  const [expanded, setExpanded] = useState<Record<string, boolean>>({})

  const groups = useMemo(() => groupLibraryItems(library.items), [library.items])

  const filteredGroups = useMemo(() => {
    const q = query.trim().toLowerCase()
    return groups
      .map((g) => {
        const title = paletteGroupTitle(g, t)
        const items = q ? g.items.filter((item) => itemMatchesQuery(item, title, q, t)) : g.items
        return { ...g, title, items }
      })
      .filter((g) => g.items.length > 0)
  }, [groups, query, t])

  useEffect(() => {
    searchRef.current?.focus()
  }, [])

  // 有搜索词：展开全部命中分组；清空后恢复收拢（与模型树 defaultCollapsed 一致）
  useEffect(() => {
    const q = query.trim()
    if (!q) {
      setExpanded({})
      return
    }
    const next: Record<string, boolean> = {}
    for (const g of filteredGroups) next[g.groupKey] = true
    setExpanded(next)
  }, [query, filteredGroups])

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b border-border/55 p-2">
        <Input
          ref={searchRef}
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t('graph.palette.search')}
          className="h-8 text-xs"
          onKeyDown={(e) => e.stopPropagation()}
          aria-label={t('graph.palette.search')}
        />
      </div>
      <div
        className="max-h-72 min-h-0 flex-1 overflow-y-auto overscroll-contain p-1"
        onWheel={(e) => e.stopPropagation()}
      >
        {filteredGroups.length === 0 ? (
          <p className="px-2 py-6 text-center text-xs text-muted-foreground/85">
            {t('graph.palette.empty')}
          </p>
        ) : (
          filteredGroups.map((g) => (
            <GraphPaletteGroup
              key={g.groupKey}
              title={g.title}
              count={g.items.length}
              open={!!expanded[g.groupKey]}
              onOpenChange={(next) => setExpanded((prev) => ({ ...prev, [g.groupKey]: next }))}
            >
              {g.items.map((item) => (
                <button
                  key={item.type}
                  type="button"
                  role="menuitem"
                  className="flex w-full rounded-ui px-2 py-1.5 text-left text-xs transition-colors hover:bg-primary/8 disabled:cursor-not-allowed disabled:opacity-45"
                  disabled={item.disabled}
                  title={item.disabledReason}
                  onClick={() => onAdd(item.type)}
                >
                  {libraryItemLabel(item, t)}
                </button>
              ))}
            </GraphPaletteGroup>
          ))
        )}
      </div>
    </div>
  )
}
