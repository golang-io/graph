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
  // 记录多分支节点已确认走过的非 Error 出端点名（如 switch 的 Case1），避免直接连向已访问汇合点的未命中分支（如 Case0 -> M）被误染为 succeeded
  const confirmedPort = new Map<string, string>()

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
      const fromEdge = edgeById.get(run.FromEdgeID)
      paintEdge(fromEdge, status ?? 'succeeded')
      if (fromEdge) {
        const from = parseEndpointID(fromEdge.From)
        const port = from.name || 'Out'
        if (port !== 'Error') confirmedPort.set(from.node, port)
      }
    }
  }

  const candidates: Array<{ edge: GraphEdgeJSON; fromNode: string; fromPort: string; toNode: string }> = []
  const incomingCount = new Map<string, number>()

  for (const edge of edgeList) {
    if (edgeStatus[edge.ID] === 'running') {
      const status = nodeStatus[parseEndpointID(edge.To).node]
      if (status === 'succeeded' || status === 'failed') edgeStatus[edge.ID] = status
      continue
    }
    if (edgeStatus[edge.ID]) {
      const toNode = parseEndpointID(edge.To).node
      incomingCount.set(toNode, (incomingCount.get(toNode) ?? 0) + 1)
      continue
    }
    // 历史走过的边（单路推进或汇合后 FromEdgeID 已被后续边覆盖）：
    // 起点已成功离开且终点已被访问时，按出口类别（Error 出口 vs 正常出口）作为候选。
    const from = parseEndpointID(edge.From)
    const to = parseEndpointID(edge.To)
    const fromPort = from.name || 'Out'
    const isErrorEdge = fromPort === 'Error'
    const tookPort = isErrorEdge ? errorExitNodes.has(from.node) : !errorExitNodes.has(from.node)
    if (completedNodes.has(from.node) && tookPort && nodeStatus[to.node] != null) {
      candidates.push({ edge, fromNode: from.node, fromPort, toNode: to.node })
      incomingCount.set(to.node, (incomingCount.get(to.node) ?? 0) + 1)
    }
  }

  // 若某个已访问目标节点只有唯一一条候选入边，则该入边必然被走过，其起点必然走了对应的出端点
  for (const c of candidates) {
    if (c.fromPort !== 'Error' && incomingCount.get(c.toNode) === 1 && !confirmedPort.has(c.fromNode)) {
      confirmedPort.set(c.fromNode, c.fromPort)
    }
  }

  for (const c of candidates) {
    const confirmed = confirmedPort.get(c.fromNode)
    if (c.fromPort !== 'Error' && confirmed !== undefined && confirmed !== c.fromPort) {
      continue
    }
    edgeStatus[c.edge.ID] = 'succeeded'
  }
  return { nodeStatus, nodeProgress, edgeStatus }
}

