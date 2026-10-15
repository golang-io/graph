/**
 * run-status — 由 Task 的 Runs 当前态计算画布上节点与边的运行染色。
 * 边：Run.FromEdgeID 有值时直接染该边；否则按已访问节点推断。
 */
import type { GraphEdgeJSON, GraphRun } from './types'
import { parseEndpointID } from './endpoints'

export type RunStatus = 'running' | 'succeeded' | 'failed'
export type RunStatusMaps = {
  nodeStatus: Record<string, RunStatus>
  nodeProgress: Record<string, string>
  edgeStatus: Record<string, RunStatus>
}

/** 后端状态（大写）映射为画布染色；PENDING / SKIPPED 不染色。 */
export function normalizeRunStatus(raw: string | undefined): RunStatus | null {
  const s = (raw ?? '').trim().toUpperCase()
  if (s === 'RUNNING' || s === 'WAITING') return 'running'
  if (s === 'SUCCEEDED') return 'succeeded'
  if (s === 'FAILED' || s === 'CANCELED') return 'failed'
  return null
}

/** 由 Runs 当前态染色：当前 NodeID + 路径上已有 Outputs 的节点视为已成功过。 */
export function buildRunStatusMaps(
  runs: GraphRun[] | null | undefined,
  edges: GraphEdgeJSON[] | null | undefined,
): RunStatusMaps {
  const edgeList = edges ?? []
  const edgeById = new Map(edgeList.map((e) => [e.ID, e]))
  const nodeStatus: Record<string, RunStatus> = {}
  const nodeProgress: Record<string, string> = {}
  const edgeStatus: Record<string, RunStatus> = {}
  const paintEdge = (edge: GraphEdgeJSON | undefined, status: RunStatus) => {
    if (!edge) return
    const prev = edgeStatus[edge.ID]
    if (prev !== 'failed' && (status === 'failed' || prev == null || prev === 'running')) edgeStatus[edge.ID] = status
  }

  const completedNodes = new Set<string>()
  const errorExitNodes = new Set<string>()

  for (const run of runs ?? []) {
    for (const [nodeID, out] of Object.entries(run.Outputs ?? {})) {
      completedNodes.add(nodeID)
      if (out && typeof out === 'object' && 'Error' in out) {
        errorExitNodes.add(nodeID)
      }
      if (!nodeStatus[nodeID]) nodeStatus[nodeID] = 'succeeded'
    }
    if (!run.NodeID) continue
    const status = normalizeRunStatus(run.Status)
    if (status) nodeStatus[run.NodeID] = status
    if (run.FromEdgeID) {
      paintEdge(edgeById.get(run.FromEdgeID), status ?? 'succeeded')
    }
  }
  for (const edge of edgeList) {
    if (edgeStatus[edge.ID] === 'running') {
      const status = nodeStatus[parseEndpointID(edge.To).node]
      if (status === 'succeeded' || status === 'failed') edgeStatus[edge.ID] = status
      continue
    }
    // 历史走过的边（单路推进或汇合后 FromEdgeID 已被后续边覆盖）：
    // 起点已成功离开且终点已被访问时，按出口类别（Error 出口 vs 正常出口）推断为 succeeded。
    if (!edgeStatus[edge.ID]) {
      const from = parseEndpointID(edge.From)
      const to = parseEndpointID(edge.To)
      const isErrorEdge = from.name === 'Error'
      const tookPort = isErrorEdge ? errorExitNodes.has(from.node) : !errorExitNodes.has(from.node)
      if (completedNodes.has(from.node) && tookPort && nodeStatus[to.node] != null) {
        edgeStatus[edge.ID] = 'succeeded'
      }
    }
  }
  return { nodeStatus, nodeProgress, edgeStatus }
}

