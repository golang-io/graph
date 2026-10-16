/**
 * Graph.InputSchema ↔ 运行输入编辑行（名字 + 必填 + 类型 Schema）互转，
 * 以及运行参数按行文本与 JSON 对象的双向转换。
 *
 * 图级 Schema：additionalProperties=false；有声明时 type=object；每个 property 必须带 type。
 *
 * Bidirectional conversion between Graph.InputSchema, typed row fields, and JSON object text.
 */
import { isValidDataName } from './node-outputs'
import type { GraphJSONSchema } from './types'

/** 运行输入允许的单一 JSON Schema type。 */
export const RUN_INPUT_TYPES = [
  'string',
  'integer',
  'number',
  'boolean',
  'array',
  'object',
  'null',
] as const

export type RunInputType = (typeof RUN_INPUT_TYPES)[number]

/** 与后端 graph.AnyValueSchema 对齐：接受任意 JSON 值。 */
export const ANY_VALUE_SCHEMA: GraphJSONSchema = {
  type: [...RUN_INPUT_TYPES],
}

/** 属性面板编辑行（仅 UI；协议侧为 Graph.InputSchema.properties[name]）。 */
export type RunInputRow = {
  name: string
  required?: boolean
  /** 属性子 Schema，必须含 type */
  schema: GraphJSONSchema
}

/** 默认新建参数：string。 */
export function defaultRunInputSchema(): GraphJSONSchema {
  return { type: 'string' }
}

/** 取出 Schema 的主类型展示文案（多类型并集显示为 any）。 */
export function schemaTypeLabel(schema?: GraphJSONSchema | null): string {
  if (!schema) return '?'
  const t = schema.type
  if (Array.isArray(t)) {
    if (t.length === RUN_INPUT_TYPES.length && RUN_INPUT_TYPES.every((x) => t.includes(x))) return 'any'
    return t.join('|')
  }
  if (typeof t === 'string' && t) {
    if (t === 'array') {
      const items = schema.items
      if (items && !Array.isArray(items)) return `array<${schemaTypeLabel(items)}>`
      return 'array'
    }
    return t
  }
  return '?'
}

/** Schema.properties → 编辑行（顺序：键插入序；必填来自 required）。 */
export function runInputFieldsFromSchema(schema?: GraphJSONSchema | null): RunInputRow[] {
  if (!schema?.properties) return []
  const required = new Set(schema.required ?? [])
  return Object.keys(schema.properties).map((name) => {
    const prop = schema.properties![name] ?? defaultRunInputSchema()
    const withType: GraphJSONSchema =
      prop.type !== undefined ? prop : { ...prop, type: 'string' }
    return required.has(name)
      ? { name, required: true, schema: withType }
      : { name, schema: withType }
  })
}

/** 编辑行 → Graph.InputSchema；空列表返回 undefined（不落盘）。 */
export function runInputSchemaFromFields(fields: RunInputRow[]): GraphJSONSchema | undefined {
  if (!fields.length) return undefined
  const properties: Record<string, GraphJSONSchema> = {}
  const required: string[] = []
  for (const f of fields) {
    const name = f.name.trim()
    if (!name || properties[name]) continue
    const schema = f.schema?.type !== undefined ? f.schema : defaultRunInputSchema()
    properties[name] = schema
    if (f.required) required.push(name)
  }
  if (!Object.keys(properties).length) return undefined
  return {
    type: 'object',
    properties,
    additionalProperties: false,
    ...(required.length ? { required } : {}),
  }
}

/** 运行输入名列表（供来源下拉）。 */
export function runInputNames(schema?: GraphJSONSchema | null): string[] {
  return schema?.properties ? Object.keys(schema.properties) : []
}

/** 将中文全角括号、引号、冒号、逗号规整为半角 ASCII 符号，兼容用户手输或粘贴的近似 JSON。 */
function normalizeJsonPunctuation(text: string): string {
  return text
    .trim()
    .replace(/【/g, '[')
    .replace(/】/g, ']')
    .replace(/｛/g, '{')
    .replace(/｝/g, '}')
    .replace(/，/g, ',')
    .replace(/：/g, ':')
    .replace(/[“”]/g, '"')
}

/**
 * 单行输入框文本 → 运行输入值：
 * 能解析为 JSON 时取解析结果（数字、布尔、null、数组、对象等；兼容中文全角符号），否则按原字符串传入。
 */
export function parseRunInputValue(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    const normalized = normalizeJsonPunctuation(text)
    if (
      (normalized.startsWith('[') && normalized.endsWith(']')) ||
      (normalized.startsWith('{') && normalized.endsWith('}'))
    ) {
      try {
        return JSON.parse(normalized)
      } catch {
        // 仍无法解析为 JSON 时保留原文本
      }
    }
    return text
  }
}

/**
 * 运行输入值 → 单行输入框文本：
 * 普通字符串原样显示；空串或可被 JSON.parse 歧义解析为数字/布尔/null/结构的字符串加双引号保留类型；其余序列化为紧凑 JSON。
 */
export function formatRunInputValue(val: unknown): string {
  if (val === undefined) return ''
  if (typeof val === 'string') {
    if (val === '') return '""'
    try {
      JSON.parse(val)
      return JSON.stringify(val)
    } catch {
      return val
    }
  }
  try {
    return JSON.stringify(val)
  } catch {
    return String(val)
  }
}

/**
 * 按 Schema 类型生成占位值（用于 JSON 模板与「参数示例」输入框占位）。
 */
export function placeholderForSchema(schema?: GraphJSONSchema | null): unknown {
  const t = schema?.type
  if (Array.isArray(t)) {
    // any 并集：给一个常见示例
    return 'Ada'
  }
  const primary = typeof t === 'string' ? t : undefined
  switch (primary) {
    case 'integer':
      return 1
    case 'number':
      return 40
    case 'boolean':
      return true
    case 'array': {
      const items = schema?.items && !Array.isArray(schema.items) ? schema.items : undefined
      const itemType = items?.type
      const itemPrimary = Array.isArray(itemType) ? itemType[0] : itemType
      if (itemPrimary === 'integer' || itemPrimary === 'number') return [1, 2, 3]
      if (itemPrimary === 'boolean') return [true, false]
      if (itemPrimary === 'object') return [{}]
      return ['a', 'b', 'c']
    }
    case 'object':
      return {}
    case 'null':
      return null
    case 'string':
    default:
      return 'Ada'
  }
}

/** 输入框占位文案：展示该类型的参数示例（紧凑 JSON / 字符串）。 */
export function examplePlaceholderText(schema?: GraphJSONSchema | null): string {
  return formatRunInputValue(placeholderForSchema(schema))
}

/**
 * 汇总当前填写的运行输入字典（按已声明参数优先保序，同时保留通过 JSON 粘贴的其余非空键）。
 */
export function collectRunInputValues(
  declared: RunInputRow[],
  values: Record<string, string>,
): Record<string, unknown> {
  const out: Record<string, unknown> = {}
  const seen = new Set<string>()
  for (const f of declared) {
    seen.add(f.name)
    const raw = values[f.name] ?? ''
    if (raw !== '') {
      out[f.name] = parseRunInputValue(raw)
    }
  }
  for (const [k, raw] of Object.entries(values)) {
    if (!seen.has(k) && raw !== '') {
      out[k] = parseRunInputValue(raw)
    }
  }
  return out
}

export type ParseRunInputJsonResult =
  | { ok: true; value: Record<string, unknown>; values: Record<string, string> }
  | { ok: false; error: string }

/**
 * 解析多行 JSON 输入框内容：
 * 要求为合法的 JSON 对象（Record<string, unknown>），并转换为按参数名索引的行文本字典 values。
 */
export function parseRunInputJsonObject(raw: string): ParseRunInputJsonResult {
  const trimmed = raw.trim()
  if (!trimmed) {
    return { ok: true, value: {}, values: {} }
  }
  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    const normalized = normalizeJsonPunctuation(trimmed)
    try {
      parsed = JSON.parse(normalized)
    } catch {
      return { ok: false, error: 'JSON 语法有误，请粘贴合法的 JSON 对象，如 {"items": ["a"], "seed": 3}' }
    }
  }
  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return { ok: false, error: '运行参数顶层必须是 JSON 对象（{...}），不能是数组或基础类型' }
  }
  const obj = parsed as Record<string, unknown>
  const nextValues: Record<string, string> = {}
  for (const [k, v] of Object.entries(obj)) {
    if (v !== undefined) {
      nextValues[k] = formatRunInputValue(v)
    }
  }
  return { ok: true, value: obj, values: nextValues }
}

/**
 * 将当前运行参数值格式化为多行缩进 JSON 文本（无已填值时返回空串）。
 */
export function formatRunInputJsonObject(
  declared: RunInputRow[],
  values: Record<string, string>,
): string {
  const obj = collectRunInputValues(declared, values)
  if (Object.keys(obj).length === 0) return ''
  return JSON.stringify(obj, null, 2)
}

/**
 * 根据已声明的运行参数生成 JSON 模板文本（保留已填值，未填参数按类型填入占位）。
 */
export function buildRunInputJsonTemplate(
  declared: RunInputRow[],
  values: Record<string, string>,
): string {
  const obj = collectRunInputValues(declared, values)
  const template: Record<string, unknown> = {}
  for (const f of declared) {
    if (!f.name.trim()) continue
    template[f.name] = f.name in obj ? obj[f.name] : placeholderForSchema(f.schema)
  }
  for (const [k, v] of Object.entries(obj)) {
    if (!(k in template)) {
      template[k] = v
    }
  }
  return JSON.stringify(template, null, 2)
}

/** 根据输入框文本推断对应的运行输入 Schema 类型（未声明键同步时使用）。 */
function inferRunInputSchema(raw: string): GraphJSONSchema {
  const val = parseRunInputValue(raw)
  if (val === null) return { type: 'null' }
  if (typeof val === 'boolean') return { type: 'boolean' }
  if (typeof val === 'number') return { type: Number.isInteger(val) ? 'integer' : 'number' }
  if (Array.isArray(val)) {
    const first = val[0]
    const itemType: RunInputType =
      typeof first === 'boolean'
        ? 'boolean'
        : typeof first === 'number'
          ? Number.isInteger(first)
            ? 'integer'
            : 'number'
          : first && typeof first === 'object' && !Array.isArray(first)
            ? 'object'
            : 'string'
    return { type: 'array', items: { type: itemType } }
  }
  if (typeof val === 'object') {
    // 开放 object（不限键），避免 properties:{} + additionalProperties:false 误拒非空对象。
    return { type: 'object' }
  }
  return defaultRunInputSchema()
}

/**
 * 检查 values 中是否包含尚未在 declared 中声明且符合变量命名规范的参数键；
 * 若存在则按值推断类型后追加到 declared 末尾返回新数组，若不存在则返回原数组引用。
 */
export function mergeUndeclaredRunInputFields(
  declared: RunInputRow[],
  values: Record<string, string>,
): RunInputRow[] {
  const existing = new Set(declared.map((f) => f.name))
  const added: RunInputRow[] = []
  for (const [k, raw] of Object.entries(values)) {
    if (raw !== '' && !existing.has(k) && isValidDataName(k)) {
      existing.add(k)
      added.push({ name: k, schema: inferRunInputSchema(raw) })
    }
  }
  return added.length > 0 ? [...declared, ...added] : declared
}
