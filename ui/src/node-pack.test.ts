import { describe, expect, it } from 'vitest'
import { createGraphNodePack, mergeGraphNodePacks } from './index'

describe('graph-react NodePack', () => {
  it('keeps registry, library and defaults in one definition', () => {
    const pack = createGraphNodePack('biz', [
      {
        type: 'biz_task',
        role: 'step',
        groupKey: 'biz.nodes',
        label: '业务任务',
        subtitle: '业务任务',
        defaultConfig: () => ({ retries: 2 }),
      },
    ])
    expect(pack.registry.roleOf('biz_task')).toBe('step')
    expect(pack.library.items[0]?.label).toBe('业务任务')
    expect(pack.library.items[0]?.defaultConfig?.()).toEqual({ retries: 2 })
  })

  it('merges product nodes onto a core pack', () => {
    const core = createGraphNodePack('core', [
      { type: 'merge', role: 'merge', groupKey: 'core', label: '汇合' },
    ])
    const biz = createGraphNodePack('biz', [
      {
        type: 'biz_switch',
        role: 'switch',
        groupKey: 'biz.nodes',
        label: '业务条件',
      },
    ])
    const merged = mergeGraphNodePacks('core+biz', core, biz)
    expect(merged.registry.roleOf('biz_switch')).toBe('switch')
    expect(merged.library.items.some((item) => item.type === 'merge')).toBe(true)
    expect(merged.library.items.some((item) => item.type === 'biz_switch')).toBe(true)
  })
})
