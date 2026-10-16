/**
 * graph-react 端点（endpoints）：种类端点来自目录 NodeSpec.Endpoints（merge/parallel 为单 In/Out）。
 * switch 例外：种类声明 Case0..Case15 作为上限，实例只展示配置中的分支（与后端 SwitchInput.ActiveOutputs 一致）。
 *
 * 另提供端点 ID 的拼接/解析：endpointID = nodeID + "." + localName。
 */
import type { GraphNodeSpec } from './node-spec'
import { catalogKind, type NodeTypeRegistry } from './node-registry'
import { readCases, switchCaseLabel } from './switch-config'
import { GRAPH_ENDPOINT_ERROR } from './types'

export type { GraphNodeSpec }

/** 端点 ID 的分隔符（与后端 graph.EndpointSep 一致）。 */
export const ENDPOINT_SEP = '.'

/** 由节点 ID 与本地端点名拼出端点 ID：nodeID + "." + name。 */
export function formatEndpointID(nodeID: string, name: string): string {
  return `${nodeID}${ENDPOINT_SEP}${name}`
}

/**
 * 按最后一个 '.' 拆分端点 ID，得到节点 ID 与本地端点名。
 * 本地端点名不含 '.'；无分隔符时回退为 { node: id, name: '' }。
 */
export function parseEndpointID(id: string): { node: string; name: string } {
  const s = id ?? ''
  const i = s.lastIndexOf(ENDPOINT_SEP)
  if (i <= 0 || i >= s.length - 1) return { node: s, name: '' }
  return { node: s.slice(0, i), name: s.slice(i + 1) }
}

/** 端点声明（graph.EndpointSpec）。 */
export type GraphEndpointSpec = {
  Inputs?: string[]
  Outputs?: string[]
}

export type GraphResolvedEndpoint = {
  id: string
  dir: 'in' | 'out'
  label: string
  /** 兜底出口（switch 的 ELSE），徽标用弱化色 */
  fallback?: boolean
}

/**
 * 计算节点实例实际生效的端点集合，页面层（Editor / RunView 的属性面板与删分支清边）共用此入口：
 * 端点声明优先取种类描述 spec 的 Endpoints，spec 缺失时用注册表条目的 displayEndpoints 兜底
 * （与 node-size.specOf 的取值口径一致，只是此处保留页面已按 pack 查到的 spec，不重复查找）；
 * Renderer 同样取自 spec，随后按 resolveGraphNodeEndpoints 展开（switch 只保留配置中的分支）。
 */
export function resolveEffectiveEndpoints(
  nodeType: string | undefined,
  spec: Pick<GraphNodeSpec, 'Endpoints' | 'Renderer'> | undefined,
  registry: NodeTypeRegistry | null | undefined,
  config?: Record<string, unknown> | null,
): GraphEndpointSet {
  return resolveGraphNodeEndpoints(
    nodeType,
    spec?.Endpoints ?? registry?.get(nodeType ?? '')?.displayEndpoints,
    config,
    spec?.Renderer,
  )
}

export type GraphEndpointSet = {
  inputs: GraphResolvedEndpoint[]
  outputs: GraphResolvedEndpoint[]
}

/**
 * 按 Spec.Endpoints 展开句柄。switch（renderer 或种类为 switch）只保留 config 中的分支，
 * 按 IF / ELIF / ELSE 顺序排列；其它种类的端点集合与 config 无关。
 */
export function resolveGraphNodeEndpoints(
  nodeType: string | undefined,
  endpoints: GraphEndpointSpec | undefined,
  config?: Record<string, unknown> | null,
  renderer?: string | null,
): GraphEndpointSet {
  const set = staticEndpointSet(endpoints ?? {})
  if (renderer !== 'switch' && catalogKind(nodeType ?? '') !== 'switch') return set
  return { ...set, outputs: switchOutputs(set.outputs, config ?? {}) }
}

/**
 * 出口变化后需要删除的边：从 nodeID 出发、分支本地名（when）已不在 outputs 中的边。
 * 默认出口的边（when 为空）不受影响。用于删除 switch 分支时联动删除连在该分支上的边。
 */
export function edgesOnRemovedOutputs<E extends { from: string; when?: string }>(
  edges: E[],
  nodeID: string,
  outputs: GraphResolvedEndpoint[],
): E[] {
  const ids = new Set(outputs.map((o) => o.id))
  return edges.filter((e) => e.from === nodeID && !!e.when && !ids.has(e.when))
}

/** 展开后的端点是否含静态 Error 出口（如 stop_error）。 */
export function nodeHasErrorOutput(
  nodeType?: string,
  config?: Record<string, unknown> | null,
  spec?: Pick<GraphNodeSpec, 'Endpoints' | 'Renderer'>,
): boolean {
  return resolveGraphNodeEndpoints(nodeType, spec?.Endpoints, config, spec?.Renderer).outputs.some(
    (item) => item.id === GRAPH_ENDPOINT_ERROR,
  )
}

function staticEndpointSet(endpoints: GraphEndpointSpec): GraphEndpointSet {
  return {
    inputs: (endpoints.Inputs ?? []).filter(Boolean).map((id) => endpoint(id, 'in', id.toUpperCase())),
    outputs: (endpoints.Outputs ?? []).filter(Boolean).map((id) => endpoint(id, 'out', id.toUpperCase())),
  }
}

/** switch 实例出口：配置中的分支（须在种类声明内），标签为作者 Label 或 IF / ELIF n / ELSE。 */
function switchOutputs(declared: GraphResolvedEndpoint[], config: Record<string, unknown>): GraphResolvedEndpoint[] {
  const ids = new Set(declared.map((o) => o.id))
  const cases = readCases(config)
  return cases.flatMap((c, i) =>
    ids.has(c.ID)
      ? [{ ...endpoint(c.ID, 'out', switchCaseLabel(cases, i)), ...(c.IsDefault ? { fallback: true } : {}) }]
      : [],
  )
}

function endpoint(id: string, dir: 'in' | 'out', label: string): GraphResolvedEndpoint {
  return { id, dir, label }
}
