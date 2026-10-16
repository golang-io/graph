/**
 * 运行输入值相对 Graph.InputSchema 的轻量校验（与 SchemaEditor 关键字对齐）。
 * 服务端仍用 jsonschema-go 做最终权威校验。
 *
 * Lightweight client-side validation of run input values against Graph.InputSchema.
 */
import { schemaTypeLabel } from './run-input-schema'
import type { GraphJSONSchema } from './types'

export type RunInputValidationIssue = {
  /** JSON Pointer 风格路径，如 /name 或 /items/0 */
  path: string
  message: string
}

export type RunInputValidationResult =
  | { ok: true }
  | { ok: false; issues: RunInputValidationIssue[] }

function jsonTypeOf(v: unknown): string {
  if (v === null) return 'null'
  if (Array.isArray(v)) return 'array'
  if (typeof v === 'number') {
    return Number.isInteger(v) ? 'integer' : 'number'
  }
  if (typeof v === 'boolean') return 'boolean'
  if (typeof v === 'string') return 'string'
  if (typeof v === 'object') return 'object'
  return typeof v
}

function typeMatches(got: string, want: string | string[] | undefined): boolean {
  if (want === undefined) return false
  const allowed = Array.isArray(want) ? want : [want]
  if (allowed.includes(got)) return true
  // integer 可满足 number
  if (got === 'integer' && allowed.includes('number')) return true
  return false
}

function validateAgainstSchema(
  value: unknown,
  schema: GraphJSONSchema,
  path: string,
  issues: RunInputValidationIssue[],
): void {
  if (!typeMatches(jsonTypeOf(value), schema.type)) {
    issues.push({
      path,
      message: `期望 ${schemaTypeLabel(schema)}，实际为 ${jsonTypeOf(value)}`,
    })
    return
  }

  if (schema.enum && !schema.enum.some((e) => Object.is(e, value))) {
    issues.push({ path, message: `值不在枚举 ${JSON.stringify(schema.enum)} 中` })
  }

  if (typeof value === 'string') {
    if (schema.minLength !== undefined && value.length < schema.minLength) {
      issues.push({ path, message: `长度至少 ${schema.minLength}` })
    }
    if (schema.maxLength !== undefined && value.length > schema.maxLength) {
      issues.push({ path, message: `长度至多 ${schema.maxLength}` })
    }
    if (schema.pattern) {
      try {
        if (!new RegExp(schema.pattern).test(value)) {
          issues.push({ path, message: `不匹配 pattern ${schema.pattern}` })
        }
      } catch {
        // 非法 pattern 交给服务端
      }
    }
  }

  if (typeof value === 'number') {
    if (schema.minimum !== undefined && value < schema.minimum) {
      issues.push({ path, message: `不得小于 ${schema.minimum}` })
    }
    if (schema.maximum !== undefined && value > schema.maximum) {
      issues.push({ path, message: `不得大于 ${schema.maximum}` })
    }
  }

  if (Array.isArray(value)) {
    if (schema.minItems !== undefined && value.length < schema.minItems) {
      issues.push({ path, message: `至少 ${schema.minItems} 个元素` })
    }
    if (schema.maxItems !== undefined && value.length > schema.maxItems) {
      issues.push({ path, message: `至多 ${schema.maxItems} 个元素` })
    }
    const items = schema.items
    if (items && !Array.isArray(items)) {
      value.forEach((el, i) => validateAgainstSchema(el, items, `${path}/${i}`, issues))
    } else if (Array.isArray(items)) {
      value.forEach((el, i) => {
        const itemSchema = items[i] ?? items[items.length - 1]
        if (itemSchema) validateAgainstSchema(el, itemSchema, `${path}/${i}`, issues)
      })
    }
  }

  if (value && typeof value === 'object' && !Array.isArray(value)) {
    const obj = value as Record<string, unknown>
    const props = schema.properties ?? {}
    for (const name of schema.required ?? []) {
      if (!(name in obj)) {
        issues.push({ path: `${path}/${name}`, message: '请填写' })
      }
    }
    for (const [k, v] of Object.entries(obj)) {
      if (props[k]) {
        validateAgainstSchema(v, props[k], `${path}/${k}`, issues)
      } else if (schema.additionalProperties === false) {
        issues.push({ path: `${path}/${k}`, message: '未在 Schema 中声明' })
      } else if (schema.additionalProperties && typeof schema.additionalProperties === 'object') {
        validateAgainstSchema(v, schema.additionalProperties, `${path}/${k}`, issues)
      }
    }
  }
}

/**
 * 按 Graph.InputSchema 校验运行输入对象。
 * schema 为 undefined/null 时：不允许任何键。
 */
export function validateRunInput(
  schema: GraphJSONSchema | null | undefined,
  input: Record<string, unknown>,
): RunInputValidationResult {
  const issues: RunInputValidationIssue[] = []
  if (!schema) {
    for (const key of Object.keys(input)) {
      issues.push({ path: `/${key}`, message: '未在 Graph.InputSchema 中声明' })
    }
    return issues.length ? { ok: false, issues } : { ok: true }
  }
  // 根按 object Schema 校验（含 required / additionalProperties / 各属性 type）
  validateAgainstSchema(input, { ...schema, type: 'object' }, '', issues)
  // 路径规范化：根属性写成 /name
  for (const issue of issues) {
    if (!issue.path.startsWith('/')) {
      issue.path = issue.path ? `/${issue.path}` : '/'
    }
  }
  return issues.length ? { ok: false, issues } : { ok: true }
}

/** 将校验问题格式化为单行提示（启动前弹错用）。 */
export function formatRunInputIssues(issues: RunInputValidationIssue[]): string {
  return issues.map((i) => `${i.path || '/'}: ${i.message}`).join('；')
}
