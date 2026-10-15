import { describe, expect, it } from 'vitest'
import { documentFromProjection, projectDocument } from './document'
import {
  clearStoredGraphHistory,
  createHistoryEntry,
  describeGraphDiff,
  loadStoredGraphHistory,
  sameGraphSnapshot,
  saveStoredGraphHistory,
} from './useGraphHistory'
import type { GraphUI } from './types'

const document: GraphUI = {
  Graph: { Nodes: [{ ID: 'a', Kind: 'template', Name: 'A', Inputs: { value: { Value: 1 } } }], Edges: [] },
  UI: { Positions: { a: { X: 12, Y: 34 } } },
}

describe('document snapshots used by history', () => {
  it('retains layout and business data while excluding transient overlays', () => {
    const projection = projectDocument(document, { nodes: { a: { selected: true, dragging: true, breakpoint: true, runStatus: 'failed' } } })
    const snapshot = documentFromProjection(projection.nodes, projection.edges, document)
    expect(snapshot.Graph).toEqual(document.Graph)
    expect(snapshot.UI?.Positions).toEqual(document.UI?.Positions)
    expect(JSON.stringify(snapshot)).not.toMatch(/selected|dragging|breakpoint|runStatus/)
  })
  it('produces a snapshot independent of subsequent edits', () => {
    const before = structuredClone(document)
    const projection = projectDocument(before)
    projection.nodes[0].config.value = 2
    expect(document.Graph.Nodes[0].Inputs?.value).toEqual({ Value: 1 })
  })

  it('infers human-readable operation labels via describeGraphDiff', () => {
    const addedNode: GraphUI = {
      ...document,
      Graph: {
        ...document.Graph,
        Nodes: [...document.Graph.Nodes, { ID: 'b', Kind: 'code', Name: '计算' }],
      },
    }
    expect(describeGraphDiff(document, addedNode)).toBe('添加节点 · 计算 (b)')
    expect(describeGraphDiff(addedNode, document)).toBe('删除节点 · 计算 (b)')

    const connectedEdge: GraphUI = {
      ...addedNode,
      Graph: {
        ...addedNode.Graph,
        Edges: [{ ID: 'e1', From: 'a.Out', To: 'b.In' }],
      },
    }
    expect(describeGraphDiff(addedNode, connectedEdge)).toBe('连接连线 · a.Out → b.In')
    expect(describeGraphDiff(connectedEdge, addedNode)).toBe('删除连线 · a.Out → b.In')

    const movedNode: GraphUI = {
      ...document,
      UI: { Positions: { a: { X: 120, Y: 240 } } },
    }
    expect(describeGraphDiff(document, movedNode)).toBe('移动节点 · A (a)')

    const rotatedSides: GraphUI = {
      ...document,
      UI: { ...document.UI, EndpointSides: { a: { In: 'top', Out: 'bottom' } } },
    }
    expect(describeGraphDiff(document, rotatedSides)).toBe('旋转端点方位 · A (a)')

    const modifiedConfig: GraphUI = {
      ...document,
      Graph: {
        ...document.Graph,
        Nodes: [{ ID: 'a', Kind: 'template', Name: 'A', Inputs: { value: { Value: 99 } } }],
      },
    }
    expect(describeGraphDiff(document, modifiedConfig)).toBe('修改节点配置 · A (a)')
  })

  it('persists and restores history stack in frontend storage without backend', () => {
    const key = 'test_history_graph'
    clearStoredGraphHistory(key)
    expect(loadStoredGraphHistory(key)).toBeNull()

    const step0 = createHistoryEntry(document, '初始版本', 'h0', 1000)
    const editedDoc: GraphUI = {
      ...document,
      UI: { Positions: { a: { X: 200, Y: 300 } } },
    }
    const step1 = createHistoryEntry(editedDoc, '移动节点 · A (a)', 'h1', 2000)

    saveStoredGraphHistory(key, {
      entries: [step0, step1],
      index: 1,
      updatedAt: 2000,
    })

    const restored = loadStoredGraphHistory(key)
    expect(restored).not.toBeNull()
    expect(restored?.index).toBe(1)
    expect(restored?.entries.map((e) => e.label)).toEqual(['初始版本', '移动节点 · A (a)'])
    expect(sameGraphSnapshot(restored?.entries[1]?.snapshot, editedDoc)).toBe(true)

    clearStoredGraphHistory(key)
    expect(loadStoredGraphHistory(key)).toBeNull()
  })
})
