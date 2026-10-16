/**
 * GraphInputsFields — 运行参数面板，两种用途分开：
 * 1. fill（默认）：按已声明的 InputSchema 填写值；类型与必填只读展示，不可改声明；
 * 2. declare：图设计时编辑参数名 / 类型 / 必填 / 嵌套 Schema。
 *
 * Fill vs declare: run-time only edits values; schema is authored at design time.
 */
import { useEffect, useMemo, useRef, useState } from 'react'
import { Button, Input, Select } from './host-ui'
import { ListRowRemoveButton } from './ListRowRemoveButton'
import { isValidDataName } from './node-outputs'
import { SchemaEditor } from './SchemaEditor'
import {
  ANY_VALUE_SCHEMA,
  RUN_INPUT_TYPES,
  buildRunInputJsonTemplate,
  defaultRunInputSchema,
  examplePlaceholderText,
  formatRunInputJsonObject,
  mergeUndeclaredRunInputFields,
  parseRunInputJsonObject,
  parseRunInputValue,
  placeholderForSchema,
  runInputFieldsFromSchema,
  runInputSchemaFromFields,
  schemaTypeLabel,
  type RunInputRow,
  type RunInputType,
} from './run-input-schema'
import { formatRunInputIssues, validateRunInput } from './run-input-validate'
import type { GraphJSONSchema } from './types'

export type GraphInputsTab = 'rows' | 'json'
/** fill=试跑填值；declare=设计期改 InputSchema */
export type GraphInputsMode = 'fill' | 'declare'

const TYPE_OPTIONS = [
  ...RUN_INPUT_TYPES.map((t) => ({ value: t, label: t })),
  { value: 'any', label: 'any' },
]

function rowPrimaryType(schema: GraphJSONSchema): string {
  const t = schema.type
  if (Array.isArray(t)) {
    if (t.length === RUN_INPUT_TYPES.length && RUN_INPUT_TYPES.every((x) => t.includes(x))) {
      return 'any'
    }
    return t[0] ?? 'string'
  }
  return typeof t === 'string' && t ? t : 'string'
}

function applyRowType(row: RunInputRow, typ: string): RunInputRow {
  if (typ === 'any') {
    return { ...row, schema: { ...ANY_VALUE_SCHEMA, description: row.schema.description } }
  }
  const next: GraphJSONSchema = { type: typ as RunInputType, description: row.schema.description }
  if (typ === 'array') {
    next.items =
      row.schema.items && !Array.isArray(row.schema.items)
        ? row.schema.items
        : defaultRunInputSchema()
  }
  if (typ === 'object') {
    next.properties = row.schema.properties ?? {}
    next.required = row.schema.required
    next.additionalProperties = false
  }
  return { ...row, schema: next }
}

export function GraphInputsFields({
  schema,
  onReplace,
  values: externalValues,
  onValuesChange,
  defaultTab = 'rows',
  /** 外部注入的按字段校验错误（如启动前校验），key 为参数名 */
  fieldErrors,
  /** fill=只填值（默认）；declare=编辑声明 */
  mode = 'fill',
  onModeChange,
}: {
  schema?: GraphJSONSchema | null
  onReplace: (schema: GraphJSONSchema | undefined) => void
  values?: Record<string, string>
  onValuesChange?: (next: Record<string, string>) => void
  defaultTab?: GraphInputsTab
  fieldErrors?: Record<string, string>
  mode?: GraphInputsMode
  onModeChange?: (mode: GraphInputsMode) => void
}) {
  const [internalValues, setInternalValues] = useState<Record<string, string>>({})
  const currentValues = externalValues ?? internalValues
  const updateValues = (next: Record<string, string>) => {
    if (onValuesChange) {
      onValuesChange(next)
    } else {
      setInternalValues(next)
    }
  }

  const inputs = useMemo(() => runInputFieldsFromSchema(schema), [schema])
  const commit = (fields: RunInputRow[]) => onReplace(runInputSchemaFromFields(fields))
  const declare = mode === 'declare'

  const [tab, setTab] = useState<GraphInputsTab>(defaultTab)
  const [jsonDraft, setJsonDraft] = useState<string>(() => formatRunInputJsonObject(inputs, currentValues))
  const [jsonError, setJsonError] = useState<string>('')
  const [expanded, setExpanded] = useState<Record<number, boolean>>({})
  const lastJsonValuesRef = useRef<Record<string, string> | null>(currentValues)

  useEffect(() => {
    if (currentValues === lastJsonValuesRef.current) return
    lastJsonValuesRef.current = currentValues
    setJsonDraft(formatRunInputJsonObject(inputs, currentValues))
    setJsonError('')
    // 草稿只跟随外部 values 变化；inputs（Schema）变化时不覆盖用户正在输入的 JSON。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentValues])

  // 填值模式：只对已填内容做类型校验，缺必填留给启动前（避免空行下一排红字「必填」）
  const liveIssues = useMemo(() => {
    if (declare) return {} as Record<string, string>
    const obj: Record<string, unknown> = {}
    for (const f of inputs) {
      const raw = currentValues[f.name] ?? ''
      if (raw !== '') obj[f.name] = parseRunInputValue(raw)
    }
    if (!schema?.properties) return {}
    const map: Record<string, string> = {}
    for (const f of inputs) {
      if (!(f.name in obj) || !f.schema) continue
      const one: GraphJSONSchema = {
        type: 'object',
        properties: { [f.name]: f.schema },
        additionalProperties: false,
      }
      const res = validateRunInput(one, { [f.name]: obj[f.name] })
      if (!res.ok) {
        map[f.name] = res.issues[0]?.message ?? '类型不符'
      }
    }
    return map
  }, [inputs, currentValues, schema, declare])

  const nextName = () => {
    const used = new Set(inputs.map((f) => f.name))
    for (let i = inputs.length + 1; ; i++) if (!used.has(`input${i}`)) return `input${i}`
  }

  const replaceAt = (idx: number, f: RunInputRow) => {
    const prevName = inputs[idx]?.name
    commit(inputs.map((x, i) => (i === idx ? f : x)))
    if (prevName && prevName !== f.name && f.name && prevName in currentValues && !(f.name in currentValues)) {
      const next = { ...currentValues }
      next[f.name] = next[prevName]!
      delete next[prevName]
      updateValues(next)
    }
  }

  const removeAt = (idx: number) => {
    const targetName = inputs[idx]?.name
    const nextFields = inputs.filter((_, i) => i !== idx)
    commit(nextFields)
    if (targetName && !nextFields.some((f) => f.name === targetName) && targetName in currentValues) {
      const next = { ...currentValues }
      delete next[targetName]
      updateValues(next)
    }
  }

  const jsonPlaceholder = useMemo(() => {
    if (inputs.length === 0) {
      return '{\n  "items": ["a", "b"],\n  "mode": "fast",\n  "seed": 3\n}'
    }
    const sample: Record<string, unknown> = {}
    for (const f of inputs) {
      if (!f.name) continue
      sample[f.name] = placeholderForSchema(f.schema)
    }
    return JSON.stringify(sample, null, 2)
  }, [inputs])

  const hasAnyValue = useMemo(
    () => Object.values(currentValues).some((v) => (v ?? '') !== ''),
    [currentValues],
  )

  const undeclaredKeys = useMemo(() => {
    if (!declare) return [] as string[]
    const declaredSet = new Set(inputs.map((f) => f.name))
    return Object.entries(currentValues)
      .filter(([k, v]) => v !== '' && !declaredSet.has(k) && isValidDataName(k))
      .map(([k]) => k)
  }, [inputs, currentValues, declare])

  const syncUndeclaredToSchema = () => {
    const merged = mergeUndeclaredRunInputFields(inputs, currentValues)
    if (merged !== inputs) {
      commit(merged)
    }
  }

  const switchTab = (nextTab: GraphInputsTab) => {
    if (nextTab === tab) return
    if (nextTab === 'json') {
      setJsonDraft(formatRunInputJsonObject(inputs, currentValues))
      setJsonError('')
    } else if (declare) {
      syncUndeclaredToSchema()
    }
    setTab(nextTab)
  }

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center justify-between gap-2">
        <div className="flex items-center gap-2 min-w-0">
          {/* 填写 / 声明 切换：类型与必填只在声明模式可改 */}
          {onModeChange ? (
            <div
              role="tablist"
              aria-label="运行参数用途"
              className="inline-flex h-7 items-center rounded-ui border border-border bg-muted/40 p-0.5 text-xs"
            >
              <button
                type="button"
                role="tab"
                aria-selected={!declare}
                onClick={() => onModeChange('fill')}
                className={`h-full rounded px-2.5 font-medium transition-colors cursor-pointer ${
                  !declare
                    ? 'bg-background text-foreground shadow-xs'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                填写
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={declare}
                onClick={() => onModeChange('declare')}
                className={`h-full rounded px-2.5 font-medium transition-colors cursor-pointer ${
                  declare
                    ? 'bg-background text-foreground shadow-xs'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                声明
              </button>
            </div>
          ) : null}
          {!declare ? (
            <div
              role="tablist"
              aria-label="运行参数输入方式"
              className="inline-flex h-7 items-center rounded-ui border border-border bg-muted/40 p-0.5 text-xs"
            >
              <button
                type="button"
                role="tab"
                aria-selected={tab === 'rows'}
                onClick={() => switchTab('rows')}
                className={`h-full rounded px-2.5 font-medium transition-colors cursor-pointer ${
                  tab === 'rows'
                    ? 'bg-background text-foreground shadow-xs'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                按行
              </button>
              <button
                type="button"
                role="tab"
                aria-selected={tab === 'json'}
                onClick={() => switchTab('json')}
                className={`h-full rounded px-2.5 font-medium transition-colors cursor-pointer ${
                  tab === 'json'
                    ? 'bg-background text-foreground shadow-xs'
                    : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                JSON
              </button>
            </div>
          ) : null}
        </div>

        <div className="flex items-center gap-1.5 shrink-0">
          {declare ? (
            <Button
              type="button"
              size="sm"
              variant="secondary"
              onClick={() =>
                commit([...inputs, { name: nextName(), schema: defaultRunInputSchema(), required: true }])
              }
            >
              + 参数
            </Button>
          ) : tab === 'rows' ? (
            hasAnyValue ? (
              <button
                type="button"
                onClick={() => updateValues({})}
                className="px-1.5 py-0.5 text-[11px] text-muted-foreground hover:text-foreground cursor-pointer"
              >
                清空值
              </button>
            ) : null
          ) : (
            <>
              {inputs.length > 0 ? (
                <button
                  type="button"
                  onClick={() => {
                    const tpl = buildRunInputJsonTemplate(inputs, currentValues)
                    setJsonDraft(tpl)
                    setJsonError('')
                    const parsed = parseRunInputJsonObject(tpl)
                    if (parsed.ok) {
                      lastJsonValuesRef.current = parsed.values
                      updateValues(parsed.values)
                    }
                  }}
                  title="按当前已声明的参数名与类型生成 JSON 骨架"
                  className="rounded border border-border bg-background px-2 py-0.5 text-[11px] text-foreground hover:bg-muted/60 cursor-pointer"
                >
                  填入模板
                </button>
              ) : null}
              {jsonDraft.trim() ? (
                <button
                  type="button"
                  onClick={() => {
                    const parsed = parseRunInputJsonObject(jsonDraft)
                    if (parsed.ok) {
                      setJsonDraft(JSON.stringify(parsed.value, null, 2))
                      setJsonError('')
                    } else {
                      setJsonError(parsed.error)
                    }
                  }}
                  className="rounded border border-border bg-background px-2 py-0.5 text-[11px] text-foreground hover:bg-muted/60 cursor-pointer"
                >
                  格式化
                </button>
              ) : null}
              {jsonDraft.trim() || hasAnyValue ? (
                <button
                  type="button"
                  onClick={() => {
                    setJsonDraft('')
                    setJsonError('')
                    const cleared: Record<string, string> = {}
                    lastJsonValuesRef.current = cleared
                    updateValues(cleared)
                  }}
                  className="px-1.5 py-0.5 text-[11px] text-muted-foreground hover:text-foreground cursor-pointer"
                >
                  清空
                </button>
              ) : null}
            </>
          )}
        </div>
      </div>

      {declare ? (
        /* —— 声明模式：编辑名字 / 类型 / 必填 / 嵌套 Schema —— */
        <div className="flex flex-col gap-2">
          {inputs.length === 0 ? (
            <div className="rounded-ui border border-dashed border-border/70 bg-muted/20 px-3 py-4 text-center text-xs text-muted-foreground">
              暂无参数声明，点击右上角「+ 参数」添加。类型与是否必填在此定稿，试跑时不可改。
            </div>
          ) : (
            <div className="max-h-80 space-y-2 overflow-y-auto pr-0.5">
              {inputs.map((f, idx) => {
                const nameError = !isValidDataName(f.name)
                  ? '名字只能包含字母、数字、下划线，且不能以数字开头'
                  : inputs.some((x, i) => i < idx && x.name === f.name)
                    ? '名字重复'
                    : ''
                const open = Boolean(expanded[idx])
                return (
                  <div key={idx} className="space-y-1">
                    <div className="flex flex-nowrap items-center gap-1.5">
                      <Input
                        className="h-8 w-28 shrink-0 font-mono text-xs"
                        placeholder="参数名"
                        aria-label={`参数名 ${idx + 1}`}
                        value={f.name}
                        aria-invalid={nameError ? true : undefined}
                        onChange={(e) => replaceAt(idx, { ...f, name: e.target.value })}
                      />
                      <Select
                        aria-label={`${f.name || `参数${idx + 1}`} 类型`}
                        value={rowPrimaryType(f.schema)}
                        options={TYPE_OPTIONS}
                        onChange={(v) => replaceAt(idx, applyRowType(f, v))}
                        className="h-8 w-28 shrink-0 text-xs"
                      />
                      <label className="flex shrink-0 items-center gap-1 whitespace-nowrap text-xs text-muted-foreground select-none cursor-pointer">
                        <input
                          type="checkbox"
                          checked={Boolean(f.required)}
                          onChange={(e) =>
                            replaceAt(idx, {
                              ...f,
                              required: e.target.checked ? true : undefined,
                            })
                          }
                        />
                        必填
                      </label>
                      <button
                        type="button"
                        title="编辑嵌套 Schema"
                        onClick={() => setExpanded((s) => ({ ...s, [idx]: !open }))}
                        className="shrink-0 whitespace-nowrap px-1 text-[11px] text-muted-foreground hover:text-foreground cursor-pointer"
                      >
                        {open ? '收起' : '高级'}
                      </button>
                      <ListRowRemoveButton onClick={() => removeAt(idx)} />
                    </div>
                    {nameError ? <p className="text-[11px] text-destructive">{nameError}</p> : null}
                    {open ? (
                      <SchemaEditor
                        schema={f.schema}
                        onChange={(next) => replaceAt(idx, { ...f, schema: next })}
                      />
                    ) : null}
                  </div>
                )
              })}
            </div>
          )}
          <p className="text-[11px] text-muted-foreground">
            此处修改的是图的 InputSchema（设计态）。保存图后，试跑「填写」页只按声明填值。
          </p>
        </div>
      ) : tab === 'rows' ? (
        /* —— 填写模式：类型与必填只读，只改值 —— */
        <div className="flex flex-col gap-2">
          {inputs.length === 0 ? (
            <div className="rounded-ui border border-dashed border-border/70 bg-muted/20 px-3 py-4 text-center text-xs text-muted-foreground">
              尚未声明运行参数。请切换到「声明」添加，或切换到「JSON」粘贴对象（未声明键不会自动写入 Schema）。
            </div>
          ) : (
            // 两列：参数名 + 全宽输入框；类型以内嵌右侧小徽章显示，不单独占一列
            <div className="max-h-80 space-y-2 overflow-y-auto pr-0.5">
              {inputs.map((f, idx) => {
                const valueError = fieldErrors?.[f.name] || liveIssues[f.name] || ''
                const typeText = schemaTypeLabel(f.schema)
                const example = examplePlaceholderText(f.schema)
                return (
                  <div key={idx} className="space-y-1">
                    <div className="grid grid-cols-[5.5rem_minmax(0,1fr)] items-center gap-x-2.5">
                      <span
                        className="flex h-8 items-center justify-end gap-0.5 font-mono text-xs text-foreground"
                        title={f.name}
                      >
                        <span className="truncate">{f.name}</span>
                        {f.required ? (
                          <span className="shrink-0 text-destructive" title="必填">
                            *
                          </span>
                        ) : (
                          <span className="inline-block w-2.5 shrink-0" aria-hidden />
                        )}
                      </span>
                      <div className="relative min-w-0">
                        <Input
                          className="h-8 min-w-0 w-full pr-28 font-mono text-xs"
                          placeholder={example}
                          title={`参数示例：${example}（类型：${typeText}）`}
                          aria-label={`${f.name}${f.required ? '（必填）' : ''}，类型 ${typeText}，参数示例 ${example}`}
                          value={currentValues[f.name] ?? ''}
                          aria-invalid={valueError ? true : undefined}
                          onChange={(e) =>
                            updateValues({
                              ...currentValues,
                              [f.name]: e.target.value,
                            })
                          }
                        />
                        <span
                          className="pointer-events-none absolute right-2 top-1/2 max-w-[6.25rem] -translate-y-1/2 truncate rounded bg-muted/60 px-1.5 py-0.5 font-mono text-[10px] leading-none text-muted-foreground"
                          title={`类型：${typeText}`}
                        >
                          {typeText}
                        </span>
                      </div>
                    </div>
                    {valueError ? (
                      <p className="pl-[calc(5.5rem+0.625rem)] text-[11px] text-destructive">
                        {valueError}
                      </p>
                    ) : null}
                  </div>
                )
              })}
            </div>
          )}
          <p className="text-[11px] text-muted-foreground">
            类型与是否必填由图的 InputSchema 决定；改声明请切到「声明」。启动时按 Schema 校验。
          </p>
        </div>
      ) : (
        /* —— JSON 填写 —— */
        <div className="flex flex-col gap-2">
          <textarea
            rows={7}
            aria-label="JSON 运行参数输入"
            value={jsonDraft}
            placeholder={jsonPlaceholder}
            onChange={(e) => {
              const raw = e.target.value
              setJsonDraft(raw)
              const parsed = parseRunInputJsonObject(raw)
              if (!parsed.ok) {
                setJsonError(parsed.error)
                return
              }
              const typeCheck = validateRunInput(schema, parsed.value)
              if (!typeCheck.ok) {
                setJsonError(formatRunInputIssues(typeCheck.issues))
              } else {
                setJsonError('')
              }
              lastJsonValuesRef.current = parsed.values
              updateValues(parsed.values)
            }}
            className="w-full rounded-ui border border-input bg-background p-2.5 font-mono text-xs leading-relaxed text-foreground placeholder:text-muted-foreground/50 focus:border-primary focus:outline-none"
          />
          {jsonError ? <p className="text-[11px] text-destructive">{jsonError}</p> : null}
          {undeclaredKeys.length > 0 ? (
            <div className="flex items-center justify-between gap-2 rounded border border-amber-500/30 bg-amber-500/10 px-2.5 py-1.5 text-[11px] text-amber-700 dark:text-amber-300">
              <span className="truncate">未声明参数键：{undeclaredKeys.join(', ')}</span>
              <button
                type="button"
                onClick={syncUndeclaredToSchema}
                className="shrink-0 font-semibold underline underline-offset-2 hover:opacity-80 cursor-pointer"
              >
                同步到参数定义
              </button>
            </div>
          ) : null}
          <p className="text-[11px] text-muted-foreground">
            JSON 仅用于填写已声明参数的值；要增删参数或改类型，请切到「声明」。
          </p>
        </div>
      )}
    </div>
  )
}
