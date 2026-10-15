import { describe, expect, it } from 'vitest'
import {
  applyWatchEvent,
  buildDebugOptions,
  canSetBreakpointOnKind,
  canSetBreakpointOnNode,
  isDebugInjectActive,
  mergeOverlaySnapshot,
  overlayDebugWaitingRuns,
  overlayIsHung,
  overlayWaitReasons,
  recallDebugSession,
  rememberDebugSession,
} from './run-overlay'
import { breakpointMarkPositionCls } from './GraphNodes'
import type { GraphTaskSnapshot } from './types'

const snap = (TaskID: string, Status: GraphTaskSnapshot['Task']['Status']): GraphTaskSnapshot => ({
  Task: {
    TaskID,
    TicketID: TaskID,
    EngineKind: 'immediate',
    GraphID: 'g',
    GraphVersion: 1,
    Status,
    StartedAt: '2020-01-01T00:00:00Z',
  },
  Runs: [],
})

describe('run-overlay', () => {
  it('keeps the latest snapshot for the same task', () => {
    const a = snap('t', 'RUNNING')
    const b = { ...snap('t', 'SUCCEEDED'), Runs: [{ RunID: 'r1', TaskID: 't', NodeID: 'a', Seq: 1, Status: 'SUCCEEDED' as const }] }
    expect(mergeOverlaySnapshot(a, b)?.Task.Status).toBe('SUCCEEDED')
    expect(mergeOverlaySnapshot(b, a)?.Task.Status).toBe('RUNNING')
  })

  it('applies full snapshot and delta runs', () => {
    const full = {
      ...snap('t', 'RUNNING'),
      Runs: [{ RunID: 'r1', TaskID: 't', NodeID: 'a', Seq: 1, Status: 'SUCCEEDED' as const }],
    }
    expect(applyWatchEvent(null, { Snapshot: full })).toEqual(full)
    const merged = applyWatchEvent(full, {
      Runs: [{ RunID: 'r2', TaskID: 't', NodeID: 'b', Seq: 1, Status: 'RUNNING' }],
      RemovedRunIDs: ['r1'],
    })
    expect(merged?.Runs.map((r) => r.RunID)).toEqual(['r2'])
  })

  it('keeps Task.Graph when a delta replaces the task row', () => {
    const graph = { Nodes: [{ ID: 'a', Kind: 'end' }], Edges: [] }
    const full = { ...snap('t', 'RUNNING'), Task: { ...snap('t', 'RUNNING').Task, Graph: graph } }
    const merged = applyWatchEvent(full, { Task: snap('t', 'SUCCEEDED').Task })
    expect(merged?.Task.Status).toBe('SUCCEEDED')
    expect(merged?.Task.Graph).toEqual(graph)
  })

  it('normalizes null Runs on a full snapshot', () => {
    const raw = { ...snap('t', 'WAITING'), Runs: null } as unknown as GraphTaskSnapshot
    const got = applyWatchEvent(null, { Snapshot: raw })
    expect(got?.Runs).toEqual([])
  })

  it('detects hung waiting tasks', () => {
    expect(overlayIsHung(snap('t', 'WAITING'))).toBe(true)
    expect(overlayWaitReasons({
      Runs: [
        { RunID: 'r', TaskID: 't', NodeID: 'w', Seq: 1, Status: 'WAITING', WaitReason: 'debug' },
        { RunID: 'j', TaskID: 't', NodeID: 'm', Seq: 1, Status: 'WAITING', WaitReason: 'join' },
      ],
    })).toEqual(['debug'])
    expect(overlayDebugWaitingRuns({
      Runs: [
        { RunID: 'r', TaskID: 't', NodeID: 'w', Seq: 1, Status: 'WAITING', WaitReason: 'debug' },
        { RunID: 'j', TaskID: 't', NodeID: 'm', Seq: 1, Status: 'WAITING', WaitReason: 'join' },
      ],
    }).map((r) => r.RunID)).toEqual(['r'])
  })

  it('allows breakpoints on all top-level executable nodes (both activity and inline)', () => {
    expect(canSetBreakpointOnKind('code')).toBe(true)
    expect(canSetBreakpointOnKind('reduce')).toBe(true)
    expect(canSetBreakpointOnKind('http')).toBe(true)
    expect(canSetBreakpointOnKind('llm')).toBe(true)
    expect(canSetBreakpointOnKind('template')).toBe(true)
    expect(canSetBreakpointOnKind('__INNER__.start')).toBe(true)
    expect(canSetBreakpointOnKind('__INNER__.end')).toBe(true)
    expect(canSetBreakpointOnKind('__INNER__.switch')).toBe(true)
    expect(canSetBreakpointOnKind('__INNER__.merge')).toBe(true)
    expect(canSetBreakpointOnKind('__INNER__.parallel')).toBe(true)
    expect(canSetBreakpointOnKind('__INNER__.map')).toBe(true)
    expect(canSetBreakpointOnKind('__INNER__.subgraph')).toBe(true)
    expect(canSetBreakpointOnKind('__INNER__.stop_error')).toBe(true)

    expect(canSetBreakpointOnKind('annotation')).toBe(false)
    expect(canSetBreakpointOnNode('calc', 'code')).toBe(true)
    expect(canSetBreakpointOnNode('fan', '__INNER__.parallel')).toBe(true)
    expect(canSetBreakpointOnNode('lp/step1', 'code')).toBe(false)
  })

  it('builds clean debug options and persists per-graph debug session', () => {
    expect(buildDebugOptions({ step: false, breakpoints: [], injects: {} })).toBeUndefined()
    expect(
      buildDebugOptions({
        step: true,
        breakpoints: ['calc', 'calc'],
        injects: {
          calc: { Vars: { a: 10 } },
          empty: { Vars: {}, Failed: false },
          boom: { Failed: true, Error: 'forced' },
        },
      }),
    ).toEqual({
      Step: true,
      Breakpoints: ['calc'],
      Injects: {
        calc: { Vars: { a: 10 } },
        boom: { Failed: true, Error: 'forced' },
      },
    })
    expect(isDebugInjectActive({ Vars: { a: 1 } })).toBe(true)
    expect(isDebugInjectActive({ Failed: true })).toBe(true)
    expect(isDebugInjectActive({ Vars: {}, Failed: false })).toBe(false)

    rememberDebugSession('demo_debug_test', {
      breakpoints: ['calc'],
      maxAttempts: 3,
      retryDelayMS: 500,
    })
    expect(recallDebugSession('demo_debug_test')).toMatchObject({
      breakpoints: ['calc'],
      maxAttempts: 3,
      retryDelayMS: 500,
      failPolicy: 'fail_fast',
    })
  })

  it('positions breakpoint indicator at the node entrance side', () => {
    expect(breakpointMarkPositionCls()).toContain('-left-px top-1/2')
    expect(breakpointMarkPositionCls('left')).toContain('-left-px top-1/2')
    expect(breakpointMarkPositionCls('right')).toContain('-right-px top-1/2')
    expect(breakpointMarkPositionCls('top')).toContain('left-1/2 -top-px')
    expect(breakpointMarkPositionCls('bottom')).toContain('-bottom-px left-1/2')
  })
})
