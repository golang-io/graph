/**
 * document-view — applyGraphViewState / withNodeEndpointSides（document.ts）、
 * resolveEffectiveEndpoints（endpoints.ts）、packSpecOf（node-pack.ts）的共享规则测试。
 * 这三处函数由 useGraphDocument、两个渲染器与两个页面共用，规则在此集中验证。
 */
import { describe, expect, it } from 'vitest'
import { applyGraphViewState, withNodeEndpointSides, type GraphEditorEdge, type GraphEditorNode } from './document'
import { resolveEffectiveEndpoints } from './endpoints'
import { createNodeRegistry } from './node-registry'
import { packSpecOf, type GraphNodePack } from './node-pack'
import type { GraphNodeSpec } from './node-spec'

const node = (id: string): GraphEditorNode => ({ id, type: 'step', name: id, config: {}, position: { x: 0, y: 0 } })
const edge = (id: string): GraphEditorEdge => ({ id, from: 'a', to: 'b' })

describe('applyGraphViewState', () => {
  it('视图为空时原样返回同一引用', () => {
    const base = { nodes: [node('a')], edges: [edge('e')] }
    expect(applyGraphViewState(base, {})).toBe(base)
  })

  it('只合并 view 中有条目的节点与边，未命中的保持原引用', () => {
    const a = node('a')
    const b = node('b')
    const e1 = edge('e1')
    const e2 = edge('e2')
    const out = applyGraphViewState({ nodes: [a, b], edges: [e1, e2] }, {
      nodes: { b: { selected: true, runStatus: 'running' } },
      edges: { e2: { animated: true } },
    })
    expect(out.nodes[0]).toBe(a)
    expect(out.nodes[1]).toEqual({ ...b, selected: true, runStatus: 'running' })
    expect(out.edges[0]).toBe(e1)
    expect(out.edges[1]).toEqual({ ...e2, animated: true })
  })

  it('只给 nodes 时 edges 数组保持原引用', () => {
    const base = { nodes: [node('a')], edges: [edge('e')] }
    const out = applyGraphViewState(base, { nodes: { a: { dragging: true } } })
    expect(out.edges).toBe(base.edges)
  })
})

describe('withNodeEndpointSides', () => {
  it('只替换目标节点的端点所在边，其余节点保持原引用', () => {
    const a = node('a')
    const b = node('b')
    const out = withNodeEndpointSides([a, b], 'b', { In: 'left', Out: 'right' })
    expect(out[0]).toBe(a)
    expect(out[1]).toEqual({ ...b, endpointSides: { In: 'left', Out: 'right' } })
  })
})

describe('packSpecOf', () => {
  const spec = { ID: 'biz.step', Name: '步骤' } as GraphNodeSpec
  const pack = {
    id: 'p',
    definitions: [
      { type: 'biz.step', nodeType: 'biz.step', label: '步骤', role: 'step', spec },
      // preset 条目：type 是作者态入口键，nodeType 与 spec 与主条目相同
      { type: '["biz.step","fast"]', nodeType: 'biz.step', label: '快速步骤', role: 'step', spec },
    ],
    registry: createNodeRegistry([]),
    library: { id: 'p', items: [] },
  } as unknown as GraphNodePack

  it('按 nodeType 返回主条目与 preset 条目共用的同一份 spec', () => {
    expect(packSpecOf(pack, 'biz.step')).toBe(spec)
  })

  it('种类不存在或 pack 未加载时返回 undefined', () => {
    expect(packSpecOf(pack, 'missing')).toBeUndefined()
    expect(packSpecOf(null, 'biz.step')).toBeUndefined()
    expect(packSpecOf(undefined, 'biz.step')).toBeUndefined()
  })
})

describe('resolveEffectiveEndpoints', () => {
  it('优先用 spec 的 Endpoints', () => {
    const registry = createNodeRegistry([
      { type: 'biz.step', role: 'step', displayEndpoints: { Inputs: ['In'], Outputs: ['Alt'] } },
    ])
    const set = resolveEffectiveEndpoints('biz.step', { Endpoints: { Inputs: ['In'], Outputs: ['Out'] } }, registry, {})
    expect(set.outputs.map((o) => o.id)).toEqual(['Out'])
  })

  it('spec 缺失时用注册表条目的 displayEndpoints 兜底', () => {
    const registry = createNodeRegistry([
      { type: 'biz.step', role: 'step', displayEndpoints: { Inputs: ['In'], Outputs: ['Alt'] } },
    ])
    const set = resolveEffectiveEndpoints('biz.step', undefined, registry, {})
    expect(set.outputs.map((o) => o.id)).toEqual(['Alt'])
    expect(set.inputs.map((i) => i.id)).toEqual(['In'])
  })

  it('switch 按配置只保留启用的分支（spec 与 displayEndpoints 两条来源一致）', () => {
    const declared = { Inputs: ['In'], Outputs: ['Case0', 'Case1', 'Case2'] }
    const config = { Cases: [{ ID: 'Case0', Expr: 'a' }, { ID: 'Case2', IsDefault: true }] }
    const fromSpec = resolveEffectiveEndpoints('__INNER__.switch', { Endpoints: declared }, undefined, config)
    const registry = createNodeRegistry([{ type: '__INNER__.switch', role: 'switch', displayEndpoints: declared }])
    const fromDisplay = resolveEffectiveEndpoints('__INNER__.switch', undefined, registry, config)
    expect(fromSpec.outputs.map((o) => o.id)).toEqual(['Case0', 'Case2'])
    expect(fromDisplay.outputs).toEqual(fromSpec.outputs)
  })
})
