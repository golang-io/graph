import { GRAPH_DEFAULT_INPUT, GRAPH_DEFAULT_OUTPUT, type GraphUI } from './types'
import type { GraphNodePack } from './node-pack'
import { splitInputs } from './inputs-split'
import { resolveGraphNodeEndpoints } from './endpoints'
import { documentFromProjection, projectDocument } from './document'
import { scopeGraphOf, scopedId, scopeOf } from './body-scope'

/**
 * Both renderers accept only handles declared by the executable catalog.
 * 连线只能在同一作用域内：主图节点之间，或同一循环体内的节点之间；跨作用域返回 null。
 */
export function connectDocument(document: GraphUI, pack: GraphNodePack, sourceId: string, targetId: string, sourceHandle?: string | null, targetHandle?: string | null): GraphUI | null {
  const scope = scopeOf(sourceId)
  if (scope !== scopeOf(targetId)) return null
  const at = scopeGraphOf(document, sourceId), graph = at?.graph
  const source = graph?.Nodes.find((n) => n.ID === at!.localId)
  const target = graph?.Nodes.find((n) => n.ID === scopeGraphOf(document, targetId)?.localId)
  const sourceSpec = source && pack.registry.get(source.Kind)?.spec
  const targetSpec = target && pack.registry.get(target.Kind)?.spec
  if (!source || !target || !sourceSpec || !targetSpec) return null
  const sourceCfg = splitInputs(source.Inputs).literals
  const targetCfg = splitInputs(target.Inputs).literals
  const outName = sourceHandle || GRAPH_DEFAULT_OUTPUT
  const inName = targetHandle || GRAPH_DEFAULT_INPUT
  if (!resolveGraphNodeEndpoints(source.Kind, sourceSpec.Endpoints, sourceCfg, sourceSpec.Renderer).outputs.some((p) => p.id === outName) ||
      !resolveGraphNodeEndpoints(target.Kind, targetSpec.Endpoints, targetCfg, targetSpec.Renderer).inputs.some((p) => p.id === inName)) return null
  // 经编辑模型写回：边落进所在作用域的图（主图或循环体），From / To 写成 nodeID.localName。
  const p = projectDocument(document)
  const edge = { id: scopedId(scope, `edge_${crypto.randomUUID()}`), from: sourceId, to: targetId, ...(outName !== GRAPH_DEFAULT_OUTPUT ? { when: outName } : {}) }
  return documentFromProjection(p.nodes, [...p.edges, edge], document)
}
