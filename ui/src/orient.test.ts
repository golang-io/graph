import { describe, expect, it } from 'vitest'
import { parseEndpointSides, resolveEndpointSides, rotateEndpointSides, setEndpointSide } from './orient'
import { documentFromProjection, projectDocument, withEndpointSides } from './document'
import { parseGraphUI } from './graph-adapter'
import { createGraphNodePackFromCatalog } from './node-spec'
import { routeBackEdge } from './edge-route'
import type { GraphUI } from './types'

const io = { inputs: ['In'], outputs: ['Out'] }

describe('endpoint sides', () => {
  it('reads only valid entries', () => {
    expect(parseEndpointSides(undefined)).toEqual({})
    expect(parseEndpointSides({ In: 'top', Out: 'middle', n: 3, Error: 'bottom', Sides: { Out: 'left' } })).toEqual({ In: 'top', Error: 'bottom' })
  })

  it('fills every instance endpoint and drops stale entries', () => {
    expect(resolveEndpointSides(undefined, io)).toEqual({ In: 'left', Out: 'right' })
    // Error 旁边另有出口时放下边；只有 Error 一个出口时按普通出口放右边
    expect(resolveEndpointSides(undefined, { inputs: ['In'], outputs: ['Out', 'Error'] })).toEqual({ In: 'left', Out: 'right', Error: 'bottom' })
    expect(resolveEndpointSides(undefined, { inputs: ['In'], outputs: ['Error'] })).toEqual({ In: 'left', Error: 'right' })
    // 旧格式 switch 的 Out 不是实例端点：被丢弃，但其所在边作为缺失分支的缺省边；删掉的 Case3 也被丢弃
    const sw = { inputs: ['In'], outputs: ['Case0', 'Case1'] }
    expect(resolveEndpointSides({ In: 'top', Out: 'bottom', Case1: 'left', Case3: 'right' }, sw)).toEqual({ In: 'top', Case0: 'bottom', Case1: 'left' })
  })

  it('moves an output off any input side clockwise', () => {
    expect(resolveEndpointSides({ In: 'right' }, io)).toEqual({ In: 'right', Out: 'bottom' })
    expect(resolveEndpointSides({ In: 'top', Out: 'top' }, io)).toEqual({ In: 'top', Out: 'right' })
    // 两个入口占用 left / top：写在 top 的出口移到 right
    expect(resolveEndpointSides({ In0: 'left', In1: 'top', Out: 'top' }, { inputs: ['In0', 'In1'], outputs: ['Out'] })).toEqual({ In0: 'left', In1: 'top', Out: 'right' })
  })

  it('edits one endpoint: an input swaps with outputs on the target side, an output cannot take an input side', () => {
    expect(setEndpointSide(undefined, io, 'In', 'right')).toEqual({ In: 'right', Out: 'left' })
    expect(setEndpointSide(undefined, io, 'In', 'top')).toEqual({ In: 'top', Out: 'right' })
    expect(setEndpointSide(undefined, io, 'Out', 'left')).toEqual({ In: 'left', Out: 'right' })
    expect(setEndpointSide(undefined, io, 'Out', 'bottom')).toEqual({ In: 'left', Out: 'bottom' })
    // 不属于实例的端点不写入
    expect(setEndpointSide(undefined, io, 'Case9', 'top')).toEqual({ In: 'left', Out: 'right' })
  })

  it('rotates every endpoint clockwise', () => {
    expect(rotateEndpointSides(undefined, io)).toEqual({ In: 'top', Out: 'bottom' })
    expect(rotateEndpointSides({ In: 'top', Out: 'bottom', Error: 'left' }, { inputs: ['In'], outputs: ['Out', 'Error'] })).toEqual({ In: 'right', Out: 'left', Error: 'top' })
  })
})

describe('endpoint sides in the document', () => {
  const pack = createGraphNodePackFromCatalog('orient', [
    { ID: 'template', Name: 'Template', Endpoints: { Inputs: ['In'], Outputs: ['Out'] } },
    { ID: 'end', Name: 'End', Endpoints: { Inputs: ['In'] } },
  ])
  const base: GraphUI = { Graph: { Nodes: [{ ID: 'a', Kind: 'template' }, { ID: 'b', Kind: 'end' }], Edges: [] }, UI: { EndpointSides: { a: { In: 'top', Out: 'bottom' } } } }

  it('keeps the projection raw and fills every endpoint before save', () => {
    const p = projectDocument(base)
    expect(p.nodes.find((n) => n.id === 'b')?.endpointSides).toEqual({})
    // 画布未改动时原样返回；保存前由 withEndpointSides 按端点补齐，补齐后再调用返回同一引用
    expect(documentFromProjection(p.nodes, p.edges, base)).toBe(base)
    const filled = withEndpointSides(base, pack.registry)
    expect(filled.UI?.EndpointSides).toEqual({ a: { In: 'top', Out: 'bottom' }, b: { In: 'left' } })
    expect(withEndpointSides(filled, pack.registry)).toBe(filled)
    // 编辑过的画布：写入节点上已有的端点方位
    const nodes = p.nodes.map((n) => (n.id === 'b' ? { ...n, endpointSides: { In: 'top' as const } } : n))
    expect(documentFromProjection(nodes, p.edges, base).UI?.EndpointSides).toEqual({ a: { In: 'top', Out: 'bottom' }, b: { In: 'top' } })
  })

  it('drops a legacy Direction key and parses four rails', () => {
    const ui = parseGraphUI({ Direction: 'RL', EndpointSides: { a: { In: 'top', Error: 'bottom' } }, EdgeRails: { e1: 'left', e2: 'bottom', e3: 'above' } })
    expect(ui).not.toHaveProperty('Direction')
    expect(ui.EndpointSides).toEqual({ a: { In: 'top', Error: 'bottom' } })
    expect(ui.EdgeRails).toEqual({ e1: 'left', e2: 'bottom' })
  })
})

describe('vertical back edge rails', () => {
  // 纵向图：source 在下方、出口朝下，target 在上方、入口朝上
  const source = { left: 0, top: 300, right: 200, bottom: 372 }
  const target = { left: 0, top: 0, right: 200, bottom: 72 }
  const route = (preferRail: 'left' | 'right') =>
    routeBackEdge(100, 372, 100, 0, source, target, [], { sourceDir: 'bottom', targetDir: 'top', preferRail }).points

  it('runs the rail beside the nodes on the requested side', () => {
    const left = route('left'), right = route('right')
    expect(Math.min(...left.map((p) => p.x))).toBeLessThan(source.left)
    expect(Math.max(...right.map((p) => p.x))).toBeGreaterThan(source.right)
    expect(left[0]).toEqual({ x: 100, y: 372 })
    expect(left[left.length - 1]).toEqual({ x: 100, y: 0 })
  })
})
