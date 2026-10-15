/**
 * run-overlay — 运行详情页叠加层的快照合并、挂起判断与在线调试辅助。
 * 快照来自 GET /tasks/{id}。watch 的第一个事件是完整快照，之后是增量，用 applyWatchEvent 合并。
 */
import { SCOPE_SEP } from './body-scope'
import type {
  GraphDebugInject,
  GraphDebugOptions,
  GraphRun,
  GraphTaskSnapshot,
  GraphWatchEvent,
} from './types'

/**
 * 判断节点种类是否支持设置断点：除画布注释框（annotation）外，所有可执行节点（含活动节点与内联控制节点）均支持断点。
 * Returns true if the node kind is an executable node eligible for breakpoints.
 */
export function canSetBreakpointOnKind(kind?: string | null): boolean {
  if (!kind) return false
  const trimmed = kind.trim()
  return Boolean(trimmed && trimmed !== 'annotation')
}

/**
 * 判断画布节点是否允许设置断点：必须是顶层可执行节点（子图 / Map 循环体不继承父 Task 断点）。
 * Returns true if the canvas node is a top-level executable node that can hold a breakpoint.
 */
export function canSetBreakpointOnNode(nodeId: string, kind?: string | null): boolean {
  if (!nodeId || nodeId.includes(SCOPE_SEP)) return false
  return canSetBreakpointOnKind(kind)
}

/**
 * 判断单条节点调试注入是否包含有效配置（强制失败，或至少注入了 1 个输入变量）。
 * Reports whether a node debug injection is active.
 */
export function isDebugInjectActive(inj?: GraphDebugInject | null): boolean {
  if (!inj) return false
  return Boolean(inj.Failed || (inj.Vars && Object.keys(inj.Vars).length > 0))
}

/**
 * 组装发送给后端的 GraphDebugOptions：过滤掉空断点与无效注入。
 * Builds a clean GraphDebugOptions payload for startTask / resume.
 */
export function buildDebugOptions(opts: {
  step?: boolean
  breakpoints?: Iterable<string>
  injects?: Record<string, GraphDebugInject>
}): GraphDebugOptions | undefined {
  const bps = [...new Set(opts.breakpoints ?? [])].filter(Boolean)
  const injects: Record<string, GraphDebugInject> = {}
  for (const [id, inj] of Object.entries(opts.injects ?? {})) {
    if (isDebugInjectActive(inj)) {
      injects[id] = {
        ...(inj.Vars && Object.keys(inj.Vars).length > 0 ? { Vars: inj.Vars } : {}),
        ...(inj.Failed ? { Failed: true } : {}),
        ...(inj.Failed && inj.Error ? { Error: inj.Error } : {}),
      }
    }
  }
  const hasStep = Boolean(opts.step)
  const hasBps = bps.length > 0
  const hasInjects = Object.keys(injects).length > 0
  if (!hasStep && !hasBps && !hasInjects) return undefined
  return {
    ...(hasStep ? { Step: true } : {}),
    ...(hasBps ? { Breakpoints: bps } : {}),
    ...(hasInjects ? { Injects: injects } : {}),
  }
}

/**
 * 按 GraphID 在会话内存中保持在线调试配置（断点、节点注入、重试策略），
 * 使编辑页（Editor）与运行页（RunView）切换时不丢失已设置的断点与调试参数。
 *
 * Per-graph session memory for online debug settings (breakpoints, injects, retry policy).
 */
export type GraphDebugSession = {
  breakpoints: string[]
  injects: Record<string, GraphDebugInject>
  maxAttempts: number
  retryDelayMS: number
  failPolicy: 'fail_fast' | 'continue'
}

const debugSessionMemory = new Map<string, GraphDebugSession>()

export function recallDebugSession(graphID: string): GraphDebugSession {
  const existing = debugSessionMemory.get(graphID)
  if (existing) {
    return {
      breakpoints: [...existing.breakpoints],
      injects: { ...existing.injects },
      maxAttempts: existing.maxAttempts,
      retryDelayMS: existing.retryDelayMS,
      failPolicy: existing.failPolicy,
    }
  }
  return {
    breakpoints: [],
    injects: {},
    maxAttempts: 1,
    retryDelayMS: 1000,
    failPolicy: 'fail_fast',
  }
}

export function rememberDebugSession(graphID: string, session: Partial<GraphDebugSession>): GraphDebugSession {
  const prev = recallDebugSession(graphID)
  const next: GraphDebugSession = {
    breakpoints: session.breakpoints ? [...session.breakpoints] : prev.breakpoints,
    injects: session.injects ? { ...session.injects } : prev.injects,
    maxAttempts: session.maxAttempts ?? prev.maxAttempts,
    retryDelayMS: session.retryDelayMS ?? prev.retryDelayMS,
    failPolicy: session.failPolicy ?? prev.failPolicy,
  }
  if (graphID) {
    debugSessionMemory.set(graphID, next)
  }
  return next
}

/** Task 是否整体停在等待（调试暂停 / 单步 / 断点 / 子图）：调试等待需用户 resume，子图等待随子 Task 结束继续。 */
export function overlayIsHung(snap?: Pick<GraphTaskSnapshot, 'Task'> | null): boolean {
  return (snap?.Task.Status ?? '').toUpperCase() === 'WAITING'
}

/** 返回所有停在调试关口（WaitReason === 'debug'）上的 Run。 */
export function overlayDebugWaitingRuns(snap?: Pick<GraphTaskSnapshot, 'Runs'> | null): GraphRun[] {
  return (snap?.Runs ?? []).filter((r) => r.Status === 'WAITING' && r.WaitReason === 'debug')
}

/** 处于 WAITING 的 Run 的等待原因（join 除外，join 只是内部汇合）。 */
export function overlayWaitReasons(snap?: Pick<GraphTaskSnapshot, 'Runs'> | null): string[] {
  return (snap?.Runs ?? [])
    .filter((r) => r.Status === 'WAITING' && r.WaitReason && r.WaitReason !== 'join')
    .map((r) => r.WaitReason!)
}

/** 合并新到达的完整快照：不同 Task 保留当前；同一 Task 取新到达者。 */
export function mergeOverlaySnapshot(
  current: GraphTaskSnapshot | null,
  next: GraphTaskSnapshot,
): GraphTaskSnapshot | null {
  if (!current) return next
  if (current.Task.TaskID !== next.Task.TaskID) return current
  return next
}

/** 把快照里可能为 null 的 Runs 归一成数组（Go nil 切片 JSON 为 null）。 */
function normalizeSnapshot(snap: GraphTaskSnapshot): GraphTaskSnapshot {
  if (snap.Runs != null) return snap
  return { ...snap, Runs: [] }
}

/** 合并 watch 的一次推送：完整快照替换；增量合并 Runs。 */
export function applyWatchEvent(
  current: GraphTaskSnapshot | null,
  ev: GraphWatchEvent,
): GraphTaskSnapshot | null {
  if (ev.Snapshot) {
    return mergeOverlaySnapshot(current, normalizeSnapshot(ev.Snapshot))
  }
  if (!current) return null
  const base = normalizeSnapshot(current)
  const drop = new Set(ev.RemovedRunIDs ?? [])
  const runs = new Map<string, GraphRun>()
  for (const run of base.Runs) {
    if (!drop.has(run.RunID)) runs.set(run.RunID, run)
  }
  for (const run of ev.Runs ?? []) runs.set(run.RunID, run)
  return {
    ...base,
    // 增量的 Task 不带图，沿用完整快照中的 Task.Graph。
    Task: ev.Task ? { ...ev.Task, Graph: base.Task.Graph } : base.Task,
    Runs: [...runs.values()],
  }
}
