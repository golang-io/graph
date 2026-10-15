import { describe, expect, it } from 'vitest'
import { edgesOnRemovedOutputs, resolveGraphNodeEndpoints } from './endpoints'

const switchSpec = { Inputs: ['In'], Outputs: Array.from({ length: 16 }, (_, i) => `Case${i}`) }
const outs = (config: Record<string, unknown>) =>
  resolveGraphNodeEndpoints('__INNER__.switch', switchSpec, config).outputs

describe('resolveGraphNodeEndpoints', () => {
  it('uses static Spec.Endpoints for non-switch kinds', () => {
    expect(
      resolveGraphNodeEndpoints('fan', { Outputs: ['Out'] }, { Outputs: 4 }, 'parallel').outputs.map((p) => p.id),
    ).toEqual(['Out'])
    expect(resolveGraphNodeEndpoints('__INNER__.merge', { Inputs: ['In'], Outputs: ['Out'] }, { Inputs: 3 }).inputs).toEqual([
      { id: 'In', dir: 'in', label: 'IN' },
    ])
    expect(resolveGraphNodeEndpoints('__INNER__.parallel', { Outputs: ['Out'] }, { Outputs: 4 }).outputs.map((p) => p.id)).toEqual([
      'Out',
    ])
    expect(resolveGraphNodeEndpoints('__INNER__.map', { Inputs: ['In'], Outputs: ['Out'] }).outputs.map((p) => p.id)).toEqual([
      'Out',
    ])
  })

  it('shows only configured switch branches as IF / ELIF / ELSE', () => {
    // 空配置与旧 IF/ELSE 存储：两路
    expect(outs({})).toEqual([
      { id: 'Case0', dir: 'out', label: 'IF' },
      { id: 'Case1', dir: 'out', label: 'ELSE', fallback: true },
    ])
    expect(outs({ Expr: 'score >= 60' }).map((p) => p.id)).toEqual(['Case0', 'Case1'])
    // Cases：条件分支保持顺序，ELSE 排最后；作者 Label 优先
    expect(
      outs({ Cases: [{ ID: 'Case2', IsDefault: true }, { ID: 'Case0', Expr: 'a' }, { ID: 'Case5', Expr: 'b', Label: 'VIP' }] }).map(
        (p) => [p.id, p.label],
      ),
    ).toEqual([['Case0', 'IF'], ['Case5', 'VIP'], ['Case2', 'ELSE']])
    // 缺 ELSE 时补一条未占用的 ID
    expect(outs({ Cases: [{ ID: 'Case0', Expr: 'a' }] }).map((p) => [p.id, p.label])).toEqual([['Case0', 'IF'], ['Case1', 'ELSE']])
    // renderer 为 switch 的自定义种类同样按配置展开
    expect(resolveGraphNodeEndpoints('biz.branch', switchSpec, {}, 'switch').outputs).toHaveLength(2)
  })
})

describe('edgesOnRemovedOutputs', () => {
  it('returns edges whose branch is no longer an output of the node', () => {
    const edges = [
      { id: 'a', from: 'sw', when: 'Case0' },
      { id: 'b', from: 'sw', when: 'Case3' },
      { id: 'c', from: 'other', when: 'Case3' },
      { id: 'd', from: 'sw' },
    ]
    expect(edgesOnRemovedOutputs(edges, 'sw', outs({})).map((e) => e.id)).toEqual(['b'])
  })
})
