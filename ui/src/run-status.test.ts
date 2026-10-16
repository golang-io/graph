import { describe, expect, it } from 'vitest'
import type { GraphEdgeJSON, GraphRun } from './types'
import { buildRunStatusMaps } from './run-status'

function edge(ID: string, From: string, To: string, branch?: string): GraphEdgeJSON {
  return { ID, From: `${From}.${branch || 'Out'}`, To: `${To}.In` }
}

function run(partial: Partial<GraphRun> & Pick<GraphRun, 'RunID' | 'NodeID' | 'Status'>): GraphRun {
  return { TaskID: 't', Seq: 1, ...partial }
}

describe('buildRunStatusMaps null-safe', () => {
  it('treats null Runs and Edges as empty lists', () => {
    expect(buildRunStatusMaps(null, null)).toEqual({
      nodeStatus: {},
      nodeProgress: {},
      edgeStatus: {},
    })
  })
})

describe('buildRunStatusMaps from Runs', () => {
  it('paints current nodes and FromEdgeID', () => {
    const edges = [
      edge('e_out1', 'fan', 'opt'),
      edge('e_out2', 'fan', 'pess'),
    ]
    const runs: GraphRun[] = [
      run({
        RunID: 'a',
        NodeID: 'opt',
        Status: 'RUNNING',
        FromEdgeID: 'e_out1',
        Outputs: { fan: { n: 1 } },
      }),
      run({
        RunID: 'b',
        NodeID: 'pess',
        Status: 'SUCCEEDED',
        FromEdgeID: 'e_out2',
        Outputs: { fan: { n: 1 }, pess: { ok: true } },
      }),
    ]
    const maps = buildRunStatusMaps(runs, edges)
    expect(maps.nodeStatus.opt).toBe('running')
    expect(maps.nodeStatus.pess).toBe('succeeded')
    expect(maps.nodeStatus.fan).toBe('succeeded')
    expect(maps.edgeStatus.e_out1).toBe('running')
    expect(maps.edgeStatus.e_out2).toBe('succeeded')
  })

  it('infers traversed edges and stateless nodes (start/switch) along a single run path', () => {
    const edges = [
      edge('e_start', 'start', 'greet'),
      edge('e2', 'greet', 'gate'),
      edge('e3', 'gate', 'hi', 'Case0'),
      edge('e4', 'gate', 'lo', 'Case1'),
      edge('e5', 'hi', 'join_sw'),
      edge('e6', 'lo', 'join_sw'),
      edge('e7', 'join_sw', 'mark'),
    ]
    const runs: GraphRun[] = [
      run({
        RunID: 'root',
        NodeID: 'mark',
        Status: 'FAILED',
        FromEdgeID: 'e7',
        Outputs: {
          start: {},
          greet: { greeting: 'hi' },
          gate: {},
          lo: { tag: 'lo' },
          join_sw: { tag: 'lo' },
        },
      }),
    ]
    const maps = buildRunStatusMaps(runs, edges)
    expect(maps.nodeStatus).toEqual({
      start: 'succeeded',
      greet: 'succeeded',
      gate: 'succeeded',
      lo: 'succeeded',
      join_sw: 'succeeded',
      mark: 'failed',
    })
    expect(maps.edgeStatus).toEqual({
      e_start: 'succeeded',
      e2: 'succeeded',
      e4: 'succeeded',
      e6: 'succeeded',
      e7: 'failed',
    })
  })

  it('does not paint an untaken direct switch branch to a visited merge node', () => {
    const edges = [
      edge('e_start', 'start', 'gate'),
      edge('e_direct', 'gate', 'join_sw', 'Case0'),
      edge('e_via_lo', 'gate', 'lo', 'Case1'),
      edge('e_lo_join', 'lo', 'join_sw'),
      edge('e_end', 'join_sw', 'end'),
    ]
    const runs: GraphRun[] = [
      run({
        RunID: 'root',
        NodeID: 'end',
        Status: 'SUCCEEDED',
        FromEdgeID: 'e_end',
        Outputs: {
          start: {},
          gate: {},
          lo: { tag: 'lo' },
          join_sw: { tag: 'lo' },
        },
      }),
    ]
    const maps = buildRunStatusMaps(runs, edges)
    expect(maps.edgeStatus).toEqual({
      e_start: 'succeeded',
      e_via_lo: 'succeeded',
      e_lo_join: 'succeeded',
      e_end: 'succeeded',
    })
    expect(maps.edgeStatus.e_direct).toBeUndefined()
  })
})
