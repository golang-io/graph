import { describe, expect, it } from 'vitest'
import { documentFromProjection, projectDocument, withEndpointSides } from './document'
import { connectDocument } from './canvas-edit'
import { graphToFlow } from './graph-adapter'
import { layoutGraph } from './layout'
import { createGraphNodePackFromCatalog } from './node-spec'
import { BODY_HEADER, BODY_MIN_SIZE, BODY_PAD, insertionPosition, insertionScope, scopeGraphOf, setBodyCollapsed } from './body-scope'
import type { GraphJSON, GraphUI } from './types'

const pack = createGraphNodePackFromCatalog('catalog', [
  { ID: 'start', Name: 'Start', Endpoints: { Outputs: ['Out'] } },
  { ID: 'end', Name: 'End', Endpoints: { Inputs: ['In'] } },
  { ID: 'template', Name: 'Template', Endpoints: { Inputs: ['In'], Outputs: ['Out'] } },
  { ID: 'map', Name: 'Map', Renderer: 'map', Endpoints: { Inputs: ['In'], Outputs: ['Out'] } },
])

const body: GraphJSON = {
  InputSchema: { type: 'object', properties: { Item: {} }, required: ['Item'], additionalProperties: false },
  Nodes: [{ ID: 'start', Kind: 'start' }, { ID: 'body', Kind: 'template' }, { ID: 'end', Kind: 'end' }],
  Edges: [{ ID: 'b1', From: 'start.Out', To: 'body.In' }, { ID: 'b2', From: 'body.Out', To: 'end.In' }],
}

// 主图 start → lp(map) → end；lp 的循环体为 start → body → end（本地 ID 与主图重名）
const doc: GraphUI = {
  Graph: {
    Nodes: [
      { ID: 'start', Kind: 'start' },
      { ID: 'lp', Kind: 'map', Inputs: { List: { From: '$input', Field: 'items' }, prefix: { Value: 'p' }, Body: { Value: body } } },
      { ID: 'end', Kind: 'end' },
    ],
    Edges: [{ ID: 'e1', From: 'start.Out', To: 'lp.In' }, { ID: 'e2', From: 'lp.Out', To: 'end.In' }],
  },
  UI: {
    Positions: { start: { X: 0, Y: 0 }, lp: { X: 200, Y: 0 }, end: { X: 900, Y: 0 } },
    SubLayouts: { lp: { Positions: { start: { X: 24, Y: 56 }, body: { X: 240, Y: 56 }, end: { X: 480, Y: 56 } } } },
  },
}

const bodyOf = (d: GraphUI) => d.Graph.Nodes.find((n) => n.ID === 'lp')!.Inputs!.Body.Value as GraphJSON

describe('map body on canvas', () => {
  it('projects an expanded body with prefixed ids and absolute positions', () => {
    const p = projectDocument(doc)
    expect(p.nodes.map((n) => n.id)).toEqual(['start', 'lp', 'lp/start', 'lp/body', 'lp/end', 'end'])
    expect(p.nodes.find((n) => n.id === 'lp')?.body).toBe('expanded')
    expect(p.nodes.find((n) => n.id === 'lp/body')?.position).toEqual({ x: 440, y: 56 })
    expect(p.edges.find((e) => e.id === 'lp/b1')).toMatchObject({ from: 'lp/start', to: 'lp/body' })
    // 未改动时原样返回，不触发保存差异
    expect(documentFromProjection(p.nodes, p.edges, doc)).toBe(doc)
    // 保存前补齐：主图与循环体的每个节点按各自种类的端点写明所在边，已有布局保留
    const sides = { start: { Out: 'right' }, body: { In: 'left', Out: 'right' }, end: { In: 'left' } }
    const filled = withEndpointSides(doc, pack.registry)
    expect(filled.UI?.EndpointSides).toEqual({ start: sides.start, lp: { In: 'left', Out: 'right' }, end: sides.end })
    expect(filled.UI?.SubLayouts?.lp).toEqual({ ...doc.UI!.SubLayouts!.lp, EndpointSides: sides })
    expect(withEndpointSides(filled, pack.registry)).toBe(filled)
  })

  it('collapses to a plain card and keeps the body layout', () => {
    const collapsed = setBodyCollapsed(doc, 'lp', true)
    const p = projectDocument(collapsed)
    expect(p.nodes.map((n) => n.id)).toEqual(['start', 'lp', 'end'])
    expect(p.nodes[1].body).toBe('collapsed')
    // 收起状态下移动主图节点，循环体布局保留，再展开回到原位
    const moved = documentFromProjection(p.nodes.map((n) => (n.id === 'end' ? { ...n, position: { x: 1000, y: 0 } } : n)), p.edges, collapsed)
    // 收起的循环体不在画布上，原有布局原样保留；缺失的端点方位在保存前由 withEndpointSides 补齐
    expect(moved.UI?.SubLayouts?.lp).toEqual({ ...doc.UI!.SubLayouts!.lp, Collapsed: true })
    const reopened = projectDocument(setBodyCollapsed(moved, 'lp', false))
    expect(reopened.nodes.find((n) => n.id === 'lp/body')?.position).toEqual({ x: 440, y: 56 })
  })

  it('writes body positions relative to the container and clamps into the content area', () => {
    const p = projectDocument(doc)
    const dragged = p.nodes.map((n) => {
      if (n.id === 'lp/body') return { ...n, position: { x: 500, y: 120 } }
      if (n.id === 'lp/end') return { ...n, position: { x: 100, y: -50 } } // 拖到容器左上方之外
      return n
    })
    const next = documentFromProjection(dragged, p.edges, doc)
    expect(next.UI?.SubLayouts?.lp.Positions).toEqual({ start: { X: 24, Y: 56 }, body: { X: 300, Y: 120 }, end: { X: BODY_PAD, Y: BODY_HEADER } })
    // 容器连同子节点整体平移：相对坐标不变
    const shifted = p.nodes.map((n) => (n.id === 'lp' || n.id.startsWith('lp/') ? { ...n, position: { x: n.position.x + 50, y: n.position.y + 30 } } : n))
    const moved = documentFromProjection(shifted, p.edges, doc)
    expect(moved.UI?.Positions?.lp).toEqual({ X: 250, Y: 30 })
    expect(moved.UI?.SubLayouts?.lp.Positions).toEqual(doc.UI!.SubLayouts!.lp.Positions)
  })

  it('adds nodes into the selected body and completes the body input schema', () => {
    const p = projectDocument(doc)
    const scope = insertionScope(p.nodes, 'lp/body')
    expect(scope).toBe('lp')
    expect(insertionScope(p.nodes, 'lp')).toBe('lp')
    expect(insertionScope(p.nodes, 'end')).toBe('')
    const at = insertionPosition(p.nodes, scope)
    const next = documentFromProjection([...p.nodes, { id: 'lp/n1', type: 'template', name: 'T', config: {}, position: at }], p.edges, doc)
    const b = bodyOf(next)
    expect(b.Nodes.map((n) => n.ID)).toEqual(['start', 'body', 'end', 'n1'])
    expect(next.Graph.Nodes.map((n) => n.ID)).toEqual(['start', 'lp', 'end'])
    // Item 已声明；补上 Index 与 map 的自定义输入 prefix，原有 required 保留
    expect(Object.keys(b.InputSchema!.properties!)).toEqual(['Item', 'Index', 'prefix'])
    expect(b.InputSchema!.required).toEqual(['Item'])
    expect(next.UI?.SubLayouts?.lp.Positions?.n1).toEqual({ X: at.x - 200, Y: at.y })
  })

  it('connects only within one scope', () => {
    const withNode = documentFromProjection(
      [...projectDocument(doc).nodes, { id: 'lp/n1', type: 'template', name: 'T', config: {}, position: { x: 300, y: 200 } }],
      projectDocument(doc).edges, doc,
    )
    const next = connectDocument(withNode, pack, 'lp/body', 'lp/n1', 'Out', 'In')!
    expect(bodyOf(next).Edges.at(-1)).toMatchObject({ From: 'body.Out', To: 'n1.In' })
    expect(next.Graph.Edges).toHaveLength(2)
    expect(connectDocument(doc, pack, 'start', 'lp/body', 'Out', 'In')).toBeNull()
    expect(connectDocument(doc, pack, 'lp/end', 'end', 'Out', 'In')).toBeNull()
  })

  it('renders body nodes as React Flow children sized by their container', () => {
    const flow = graphToFlow(doc.Graph, doc.UI, pack.registry)
    const child = flow.nodes.find((n) => n.id === 'lp/body')!
    expect(child.parentId).toBe('lp')
    expect(child.position).toEqual({ x: 240, y: 56 })
    const container = flow.nodes.find((n) => n.id === 'lp')!
    expect(container.data.body).toBe('expanded')
    expect(container.width).toBeGreaterThanOrEqual(BODY_MIN_SIZE.w)
    expect(container.width).toBeGreaterThan(480 + BODY_PAD)
    expect(flow.edges.find((e) => e.id === 'lp/b2')).toMatchObject({ source: 'lp/body', target: 'lp/end' })
  })

  it('auto layout keeps body nodes inside the container content area', () => {
    const laid = layoutGraph(doc, pack)
    const pos = Object.values(laid.UI?.SubLayouts?.lp.Positions ?? {})
    expect(pos).toHaveLength(3)
    expect(Math.min(...pos.map((p) => p.X))).toBe(BODY_PAD)
    expect(Math.min(...pos.map((p) => p.Y))).toBe(BODY_HEADER)
    // 外层按展开后的容器尺寸排布：end 位于容器右侧
    const lp = laid.UI!.Positions!.lp, end = laid.UI!.Positions!.end
    expect(end.X).toBeGreaterThan(lp.X + BODY_MIN_SIZE.w)
  })

  it('resolves the scope graph for reference sources', () => {
    expect(scopeGraphOf(doc, 'lp/body')).toEqual({ graph: body, localId: 'body' })
    expect(scopeGraphOf(doc, 'end')?.graph).toBe(doc.Graph)
    expect(scopeGraphOf(doc, 'end/x')).toBeUndefined()
  })
})
