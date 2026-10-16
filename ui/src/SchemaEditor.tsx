/**
 * SchemaEditor — 递归编辑运行输入属性的 JSON Schema 子集：
 * type / description / enum / items / properties / required / additionalProperties。
 *
 * Recursive JSON Schema subset editor for Graph.InputSchema properties.
 */
import { Button, Input, Select } from './host-ui'
import { ListRowRemoveButton } from './ListRowRemoveButton'
import {
  ANY_VALUE_SCHEMA,
  RUN_INPUT_TYPES,
  defaultRunInputSchema,
  type RunInputType,
} from './run-input-schema'
import type { GraphJSONSchema } from './types'

const TYPE_OPTIONS = [
  ...RUN_INPUT_TYPES.map((t) => ({ value: t, label: t })),
  { value: 'any', label: 'any（任意 JSON）' },
]

function primaryType(schema: GraphJSONSchema): string {
  const t = schema.type
  if (Array.isArray(t)) {
    if (
      t.length === RUN_INPUT_TYPES.length &&
      RUN_INPUT_TYPES.every((x) => t.includes(x))
    ) {
      return 'any'
    }
    return t[0] ?? 'string'
  }
  return typeof t === 'string' && t ? t : 'string'
}

function schemaForType(typ: string, prev: GraphJSONSchema): GraphJSONSchema {
  if (typ === 'any') {
    return { ...ANY_VALUE_SCHEMA, description: prev.description }
  }
  const next: GraphJSONSchema = { type: typ as RunInputType, description: prev.description }
  if (typ === 'array') {
    next.items =
      prev.items && !Array.isArray(prev.items) ? prev.items : defaultRunInputSchema()
  }
  if (typ === 'object') {
    next.properties = prev.properties ?? {}
    next.required = prev.required
    next.additionalProperties = false
  }
  return next
}

export function SchemaEditor({
  schema,
  onChange,
  depth = 0,
}: {
  schema: GraphJSONSchema
  onChange: (next: GraphJSONSchema) => void
  depth?: number
}) {
  const typ = primaryType(schema)
  const props = schema.properties ?? {}
  const propNames = Object.keys(props)
  const required = new Set(schema.required ?? [])

  const setProp = (name: string, prop: GraphJSONSchema) => {
    onChange({
      ...schema,
      type: 'object',
      additionalProperties: false,
      properties: { ...props, [name]: prop },
    })
  }

  const renameProp = (oldName: string, newName: string) => {
    if (!newName || newName === oldName || props[newName]) return
    const nextProps: Record<string, GraphJSONSchema> = {}
    for (const [k, v] of Object.entries(props)) {
      nextProps[k === oldName ? newName : k] = v
    }
    const nextRequired = (schema.required ?? []).map((r) => (r === oldName ? newName : r))
    onChange({
      ...schema,
      type: 'object',
      additionalProperties: false,
      properties: nextProps,
      required: nextRequired.length ? nextRequired : undefined,
    })
  }

  const removeProp = (name: string) => {
    const { [name]: _, ...rest } = props
    const nextRequired = (schema.required ?? []).filter((r) => r !== name)
    onChange({
      ...schema,
      type: 'object',
      additionalProperties: false,
      properties: rest,
      required: nextRequired.length ? nextRequired : undefined,
    })
  }

  const toggleRequired = (name: string, on: boolean) => {
    const set = new Set(schema.required ?? [])
    if (on) set.add(name)
    else set.delete(name)
    const list = [...set]
    onChange({
      ...schema,
      type: 'object',
      additionalProperties: false,
      properties: props,
      required: list.length ? list : undefined,
    })
  }

  const nextNestedName = () => {
    const used = new Set(propNames)
    for (let i = 1; ; i++) if (!used.has(`field${i}`)) return `field${i}`
  }

  return (
    <div
      className={`space-y-1.5 rounded border border-border/60 bg-muted/20 p-2 ${depth > 0 ? 'ml-2' : ''}`}
    >
      <div className="flex flex-wrap items-center gap-1.5">
        <Select
          aria-label="类型"
          value={typ}
          options={TYPE_OPTIONS}
          onChange={(v) => onChange(schemaForType(v, schema))}
          className="h-7 w-36 text-xs"
        />
        <Input
          className="h-7 min-w-0 flex-1 text-xs"
          placeholder="说明（可选）"
          value={schema.description ?? ''}
          onChange={(e) =>
            onChange({
              ...schema,
              description: e.target.value || undefined,
            })
          }
        />
      </div>

      {typ === 'array' ? (
        <div className="space-y-1">
          <div className="text-[11px] text-muted-foreground">元素类型 items</div>
          <SchemaEditor
            depth={depth + 1}
            schema={
              schema.items && !Array.isArray(schema.items)
                ? schema.items
                : defaultRunInputSchema()
            }
            onChange={(items) =>
              onChange({
                ...schema,
                type: 'array',
                items,
              })
            }
          />
        </div>
      ) : null}

      {typ === 'object' ? (
        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <span className="text-[11px] text-muted-foreground">对象字段 properties</span>
            <Button
              type="button"
              size="sm"
              variant="secondary"
              onClick={() => {
                const name = nextNestedName()
                setProp(name, defaultRunInputSchema())
              }}
            >
              + 字段
            </Button>
          </div>
          {propNames.length === 0 ? (
            <p className="text-[11px] text-muted-foreground">暂无字段；也可保持空对象。</p>
          ) : (
            propNames.map((name) => (
              <div key={name} className="space-y-1 rounded border border-border/40 p-1.5">
                <div className="flex items-center gap-1.5">
                  <Input
                    className="h-7 w-28 font-mono text-xs"
                    value={name}
                    onChange={(e) => renameProp(name, e.target.value)}
                  />
                  <label className="flex shrink-0 items-center gap-1 text-[11px] text-muted-foreground select-none cursor-pointer">
                    <input
                      type="checkbox"
                      checked={required.has(name)}
                      onChange={(e) => toggleRequired(name, e.target.checked)}
                    />
                    必填
                  </label>
                  <ListRowRemoveButton onClick={() => removeProp(name)} />
                </div>
                <SchemaEditor
                  depth={depth + 1}
                  schema={props[name] ?? defaultRunInputSchema()}
                  onChange={(prop) => setProp(name, prop)}
                />
              </div>
            ))
          )}
        </div>
      ) : null}
    </div>
  )
}
