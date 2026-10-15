/**
 * Graph.InputSchema ↔ 运行输入编辑行（名字 + 必填）互转。
 * 图级 Schema：additionalProperties=false；有声明时 type=object。
 */
import type { GraphJSONSchema } from './types'

/** 属性面板编辑行（仅 UI；协议侧为 Graph.InputSchema）。 */
export type RunInputRow = { name: string; required?: boolean }

/** Schema.properties → 编辑行（顺序：键插入序；必填来自 required）。 */
export function runInputFieldsFromSchema(schema?: GraphJSONSchema | null): RunInputRow[] {
  if (!schema?.properties) return []
  const required = new Set(schema.required ?? [])
  return Object.keys(schema.properties).map((name) =>
    required.has(name) ? { name, required: true } : { name },
  )
}

/** 编辑行 → Graph.InputSchema；空列表返回 undefined（不落盘）。 */
export function runInputSchemaFromFields(fields: RunInputRow[]): GraphJSONSchema | undefined {
  if (!fields.length) return undefined
  const properties: Record<string, GraphJSONSchema> = {}
  const required: string[] = []
  for (const f of fields) {
    const name = f.name.trim()
    if (!name || properties[name]) continue
    properties[name] = {}
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
