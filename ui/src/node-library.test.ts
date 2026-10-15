import { describe, expect, it } from 'vitest'
import {
  groupLibraryItems,
  libraryDefaultConfig,
  mergeNodeLibraries,
  type NodeLibrary,
} from './node-library'
import { annotationLibrary } from './core-pack'

const core: NodeLibrary = {
  id: 'core',
  items: [{ type: 'llm', label: '智能体', groupKey: 'graph.palette.nodes' }],
}

const biz: NodeLibrary = {
  id: 'biz',
  items: [
    {
      type: 'biz_a',
      label: '业务A',
      groupKey: 'biz.group',
      defaultConfig: () => ({ x: 1 }),
    },
  ],
}

describe('mergeNodeLibraries', () => {
  it('appends business items onto core', () => {
    const merged = mergeNodeLibraries(core, annotationLibrary, biz)
    expect(merged.items.some((i) => i.type === 'llm')).toBe(true)
    expect(merged.items.some((i) => i.type === 'annotation')).toBe(true)
    expect(merged.items.some((i) => i.type === 'biz_a')).toBe(true)
  })

  it('later library overrides same type', () => {
    const override: NodeLibrary = {
      id: 'override',
      items: [
        {
          type: 'llm',
          label: '覆盖智能体',
          groupKey: 'graph.palette.nodes',
          defaultConfig: () => ({ model: 'x' }),
        },
      ],
    }
    const merged = mergeNodeLibraries(core, override)
    const llm = merged.items.find((i) => i.type === 'llm')
    expect(llm?.label).toBe('覆盖智能体')
    expect(libraryDefaultConfig(merged, 'llm')).toEqual({ model: 'x' })
  })
})

describe('groupLibraryItems', () => {
  it('keeps first-seen group order', () => {
    const groups = groupLibraryItems(mergeNodeLibraries(core, annotationLibrary, biz).items)
    const keys = groups.map((g) => g.groupKey)
    expect(keys[0]).toBe('graph.palette.nodes')
    expect(keys).toContain('graph.palette.other')
    expect(keys).toContain('biz.group')
  })
})
