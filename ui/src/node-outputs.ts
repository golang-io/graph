/**
 * node-outputs — 编辑器侧的节点出口载荷声明与输入来源候选。
 *
 * 后端 DeclareOutputs 是函数，不随 /node-specs 下发；内置节点中由配置决定输出的
 * template / code / merge / wait 在这里按相同规则推导，其余节点取 NodeSpec.OutputsByEndpoint。
 * 结果只用于属性面板的下拉候选，引用是否合法以服务端保存时的 Validate 为准。
 *
 * Editor-side output declarations and input source candidates; the server remains authoritative.
 */
import { splitInputs } from './inputs-split'
import { catalogKind } from './node-registry'
import type { GraphNodeSpec } from './node-spec'
import { parseEndpointID, resolveGraphNodeEndpoints } from './endpoints'
import { GRAPH_ENDPOINT_ERROR, GRAPH_INPUT_SOURCE, type GraphJSON, type GraphJSONSchema } from './types'

/** 与 graph.ErrorField 一致：错误出口之后可用的输出名。 */
export const GRAPH_ERROR_FIELD = 'Error'

/** 与 node.EndpointPayloadDefault 一致：所有正常出口共享的载荷键。 */
export const ENDPOINT_PAYLOAD_DEFAULT = ''

/** 与 graph.ValidName 一致：字母或下划线开头，后接字母、数字、下划线，最长 64。 */
export function isValidDataName(s: string): boolean {
  return /^[A-Za-z_][A-Za-z0-9_]{0,63}$/.test(s)
}

/** Schema.properties 的键（插入序）。 */
export function schemaPropertyNames(schema?: GraphJSONSchema | null): string[] {
  return schema?.properties ? Object.keys(schema.properties) : []
}

/**
 * 节点实例出口载荷表（与后端 DeclaredOutput / OutputsByEndpoint 对齐）。
 * 键为控制流出口 ID；空串表示所有正常出口（不含 error）共享。
 */
export type DeclaredOutput = Record<string, GraphJSONSchema>

/** 由输出名构造无类型约束的 object Schema。 */
function namesSchema(names: string[]): GraphJSONSchema | undefined {
  if (!names.length) return undefined
  const properties: Record<string, GraphJSONSchema> = {}
  for (const name of names) properties[name] = {}
  return { type: 'object', properties }
}

/** 合并多个输出 Schema 的 properties（后者覆盖同名键）。 */
function mergeSchemas(...parts: Array<GraphJSONSchema | undefined | null>): GraphJSONSchema | undefined {
  const properties: Record<string, GraphJSONSchema> = {}
  const order: string[] = []
  for (const s of parts) {
    if (!s?.properties) continue
    for (const name of Object.keys(s.properties)) {
      if (!(name in properties)) order.push(name)
      properties[name] = s.properties[name] ?? {}
    }
  }
  if (!order.length) return undefined
  const ordered: Record<string, GraphJSONSchema> = {}
  for (const name of order) ordered[name] = properties[name]
  return { type: 'object', properties: ordered }
}

/** 合并多份出口表；同出口上的 Schema 做 properties 合并。 */
function mergeOutputsByEndpoint(...parts: Array<DeclaredOutput | undefined | null>): DeclaredOutput {
  const out: DeclaredOutput = {}
  for (const part of parts) {
    if (!part) continue
    for (const [endpoint, schema] of Object.entries(part)) {
      const merged = mergeSchemas(out[endpoint], schema)
      if (merged) out[endpoint] = merged
    }
  }
  return out
}

/** 全部出口载荷中的字段名（稳定：先默认出口，再按 endpoint 键排序，再 properties 插入序）。 */
export function allOutputFields(byEndpoint: DeclaredOutput | undefined | null): string[] {
  if (!byEndpoint) return []
  const endpoints = Object.keys(byEndpoint).sort((a, b) => {
    if (a === ENDPOINT_PAYLOAD_DEFAULT && b !== ENDPOINT_PAYLOAD_DEFAULT) return -1
    if (b === ENDPOINT_PAYLOAD_DEFAULT && a !== ENDPOINT_PAYLOAD_DEFAULT) return 1
    return a.localeCompare(b)
  })
  const out: string[] = []
  const seen = new Set<string>()
  for (const endpoint of endpoints) {
    for (const name of schemaPropertyNames(byEndpoint[endpoint])) {
      if (seen.has(name)) continue
      seen.add(name)
      out.push(name)
    }
  }
  return out
}

/** 字段所属出口；不存在时返回 undefined。 */
export function fieldEndpoint(byEndpoint: DeclaredOutput | undefined | null, field: string): string | undefined {
  if (!byEndpoint) return undefined
  for (const [endpoint, schema] of Object.entries(byEndpoint)) {
    if (schema?.properties && field in schema.properties) return endpoint
  }
  return undefined
}

/** 扁平 Schema（全部字段）与 字段→出口 表，供下拉展示。 */
function flattenDeclaredOutput(byEndpoint: DeclaredOutput): {
  schema?: GraphJSONSchema
  fieldEndpoints?: Record<string, string>
} {
  const names = allOutputFields(byEndpoint)
  if (!names.length) return {}
  const properties: Record<string, GraphJSONSchema> = {}
  const fieldEndpoints: Record<string, string> = {}
  for (const name of names) {
    const endpoint = fieldEndpoint(byEndpoint, name) ?? ENDPOINT_PAYLOAD_DEFAULT
    for (const schema of Object.values(byEndpoint)) {
      if (schema?.properties?.[name]) {
        properties[name] = schema.properties[name]
        break
      }
    }
    if (!properties[name]) properties[name] = {}
    if (endpoint) fieldEndpoints[name] = endpoint
  }
  return {
    schema: { type: 'object', properties },
    fieldEndpoints: Object.keys(fieldEndpoints).length ? fieldEndpoints : undefined,
  }
}

/** 读取 Config[key] 列表中每项的 field 字符串，丢弃空值与非法名字。 */
function namesFrom(config: Record<string, unknown> | undefined, key: string, field: string): string[] {
  const list = config?.[key]
  if (!Array.isArray(list)) return []
  const out: string[] = []
  const seen = new Set<string>()
  for (const item of list) {
    const name = (item as Record<string, unknown> | null)?.[field]
    if (typeof name === 'string' && isValidDataName(name) && !seen.has(name)) {
      seen.add(name)
      out.push(name)
    }
  }
  return out
}

/**
 * 节点实例的出口载荷表，规则与后端 NodeOutputs 相同：
 * NodeSpec.OutputsByEndpoint，合并按配置推导的默认出口载荷；节点有 error 出口时追加 error。
 */
export function nodeDeclaredOutput(
  type: string,
  config: Record<string, unknown> | undefined,
  spec?: GraphNodeSpec,
): DeclaredOutput {
  let dynamic: DeclaredOutput | undefined
  switch (catalogKind(type)) {
    case 'template':
    case 'code': {
      const schema = namesSchema(namesFrom(config, 'Outputs', 'Key'))
      if (schema) dynamic = { [ENDPOINT_PAYLOAD_DEFAULT]: schema }
      break
    }
    case 'merge': {
      const schema = namesSchema(namesFrom(config, 'Outputs', 'Name'))
      if (schema) dynamic = { [ENDPOINT_PAYLOAD_DEFAULT]: schema }
      break
    }
  }
  let errOut: DeclaredOutput | undefined
  if (resolveGraphNodeEndpoints(type, spec?.Endpoints, config, spec?.Renderer).outputs.some((p) => p.id === GRAPH_ENDPOINT_ERROR)) {
    errOut = { [GRAPH_ENDPOINT_ERROR]: { type: 'object', properties: { [GRAPH_ERROR_FIELD]: {} } } }
  }
  return mergeOutputsByEndpoint(spec?.OutputsByEndpoint, dynamic, errOut)
}

/** 输入来源候选：运行输入（$input）或一个上游节点及其输出声明。 */
export type GraphInputSourceOption = {
  /** InputRef.From 的取值：节点 ID 或 '$input' */
  id: string
  label: string
  /** 扁平合并后的全部输出字段（供下拉） */
  schema?: GraphJSONSchema
  /** 输出名 → 控制流出口；未列出表示所有正常出口之后可用 */
  fieldEndpoints?: Record<string, string>
}

/**
 * 列出节点 nodeId 可引用的来源：运行输入，以及沿控制边能到达该节点的上游节点（不含自身）。
 * 循环体边界与出口可达性（如 error 字段只能在 error 出口之后引用）不在这里过滤，由服务端校验报错。
 */
export function inputSourcesFor(
  graph: GraphJSON,
  nodeId: string,
  specOf: (type: string) => GraphNodeSpec | undefined,
): GraphInputSourceOption[] {
  const incoming = new Map<string, string[]>()
  for (const e of graph.Edges ?? []) {
    // From / To 是端点 ID；按节点 ID 建立「目标节点 → 来源节点」邻接。
    const toNode = parseEndpointID(e.To).node
    const fromNode = parseEndpointID(e.From).node
    const list = incoming.get(toNode) ?? []
    list.push(fromNode)
    incoming.set(toNode, list)
  }
  const ancestors = new Set<string>()
  const stack = [...(incoming.get(nodeId) ?? [])]
  while (stack.length) {
    const id = stack.pop()!
    if (id === nodeId || ancestors.has(id)) continue
    ancestors.add(id)
    stack.push(...(incoming.get(id) ?? []))
  }

  const out: GraphInputSourceOption[] = []
  if (graph.InputSchema?.properties && Object.keys(graph.InputSchema.properties).length) {
    out.push({ id: GRAPH_INPUT_SOURCE, label: '运行输入', schema: graph.InputSchema })
  }
  for (const n of graph.Nodes) {
    if (!ancestors.has(n.ID)) continue
    const flat = flattenDeclaredOutput(nodeDeclaredOutput(n.Kind, splitInputs(n.Inputs).literals, specOf(n.Kind)))
    if (schemaPropertyNames(flat.schema).length) {
      out.push({
        id: n.ID,
        label: n.Name ? `${n.Name}（${n.ID}）` : n.ID,
        schema: flat.schema,
        fieldEndpoints: flat.fieldEndpoints,
      })
    }
  }
  return out
}
