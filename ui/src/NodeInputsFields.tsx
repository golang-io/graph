/**
 * NodeInputsFields — 节点输入（Node.Inputs）编辑：每行一个输入名，来源为上游节点输出、运行输入或字面量。
 *
 * 输入名是表达式与模板中可用的变量名。NodeSpec.InputSchema 中无 title 的属性作建议槽（新增时优先补齐）；
 * 任意自定义输入名一律允许。
 *
 * Editor for Node.Inputs: each row binds an input name to a node output, a run input, or a literal.
 */
import { Button, Input, Select } from './host-ui'
import { ListRowRemoveButton } from './ListRowRemoveButton'
import type { GraphInputSourceOption } from './node-outputs'
import { schemaPropertyNames } from './node-outputs'
import type { BindingSlot } from './node-spec'
import type { GraphInputRef } from './types'

/** 来源下拉中表示字面量的取值；不会写入 InputRef.From。 */
const LITERAL = ''

/** 字面量的显示文本：字符串原样显示，其他值显示 JSON。 */
function literalText(v: unknown): string {
  if (v === undefined) return ''
  return typeof v === 'string' ? v : JSON.stringify(v)
}

/** 输入框文本 → 字面量：能解析为 JSON 时取解析结果（数字、布尔、对象等），否则按字符串保存。 */
function parseLiteral(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

export function NodeInputsFields({
  inputs,
  sources,
  slots,
  onReplace,
}: {
  inputs: Record<string, GraphInputRef>
  /** 可引用的来源（inputSourcesFor 的结果） */
  sources: GraphInputSourceOption[]
  /** 种类级绑定建议槽（InputSchema 无 title 的属性）；新增时优先补齐未填槽 */
  slots: BindingSlot[]
  onReplace: (inputs: Record<string, GraphInputRef>) => void
}) {
  const rows = Object.entries(inputs)
  const used = new Set(rows.map(([name]) => name))
  // 新增行优先补齐未填的 input 槽，再生成 in1、in2…
  const nextName = (): string => {
    const slot = slots.find((s) => !used.has(s.name))
    if (slot) return slot.name
    for (let i = rows.length + 1; ; i++) if (!used.has(`in${i}`)) return `in${i}`
  }

  // 以数组形式改写，保持行顺序；改名时替换键而不移动位置。
  // 改成另一行已有的名字会使两行合并、丢失一行，因此忽略这次改名。
  const replaceRow = (idx: number, name: string, ref: GraphInputRef) => {
    if (name !== rows[idx][0] && used.has(name)) return
    onReplace(Object.fromEntries(rows.map((r, i) => (i === idx ? [name, ref] : r))))
  }

  const missing = slots.filter((s) => s.required && !used.has(s.name))
  const slotHint = slots.map((s) => (s.required ? `${s.name}*` : s.name)).join('、')

  return (
    <div className="flex flex-col gap-2 border-t border-border pt-3">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-semibold">输入</span>
        <Button
          type="button"
          size="sm"
          variant="secondary"
          onClick={() => {
            const first = sources[0]
            const firstField = schemaPropertyNames(first?.schema)[0] ?? ''
            onReplace({
              ...inputs,
              [nextName()]: first ? { From: first.id, Field: firstField } : { Value: '' },
            })
          }}
        >
          + 输入
        </Button>
      </div>
      {rows.map(([name, ref], idx) => {
        const from = ref.From ?? LITERAL
        const source = sources.find((s) => s.id === from)
        const fieldNames = schemaPropertyNames(source?.schema)
        return (
          <div key={idx} className="space-y-1.5 rounded-lg border border-border p-2">
            <div className="flex items-center gap-1">
              <Input
                className="h-8 font-mono text-xs"
                value={name}
                placeholder="输入名"
                onChange={(e) => replaceRow(idx, e.target.value, ref)}
              />
              <ListRowRemoveButton onClick={() => onReplace(Object.fromEntries(rows.filter((_, i) => i !== idx)))} />
            </div>
            <Select
              value={from}
              onChange={(v) => {
                if (v === LITERAL) return replaceRow(idx, name, { Value: '' })
                const next = sources.find((s) => s.id === v)
                replaceRow(idx, name, { From: v, Field: schemaPropertyNames(next?.schema)[0] ?? '' })
              }}
              options={[
                ...sources.map((s) => ({ value: s.id, label: s.label })),
                // 当前引用的来源已不在候选中（节点被删除或不再位于上游）时仍显示，便于看出问题并修改。
                ...(from !== LITERAL && !source ? [{ value: from, label: `${from}（不可达）` }] : []),
                { value: LITERAL, label: '字面量' },
              ]}
              className="w-full"
            />
            {from === LITERAL ? (
              <Input
                key={`${idx}:${name}`}
                className="h-8 font-mono text-xs"
                defaultValue={literalText(ref.Value)}
                placeholder='值：文本，或 JSON（如 3、true、["a"]）'
                onBlur={(e) => replaceRow(idx, name, { Value: parseLiteral(e.target.value) })}
              />
            ) : fieldNames.length ? (
              <Select
                value={ref.Field ?? ''}
                onChange={(v) => replaceRow(idx, name, { From: from, Field: v })}
                options={[
                  ...fieldNames.map((fieldName) => {
                    const endpoint = source?.fieldEndpoints?.[fieldName]
                    return {
                      value: fieldName,
                      label: endpoint ? `${fieldName}（仅 ${endpoint} 出口之后）` : fieldName,
                    }
                  }),
                  ...(ref.Field && !fieldNames.includes(ref.Field)
                    ? [{ value: ref.Field, label: `${ref.Field}（未声明）` }]
                    : []),
                ]}
                className="w-full"
              />
            ) : (
              <Input
                className="h-8 font-mono text-xs"
                value={ref.Field ?? ''}
                placeholder="输出名"
                onChange={(e) => replaceRow(idx, name, { From: from, Field: e.target.value })}
              />
            )}
          </div>
        )
      })}
      {missing.length ? (
        <p className="text-[11px] text-destructive">缺少必填输入：{missing.map((s) => s.name).join('、')}</p>
      ) : null}
      <p className="text-[11px] text-muted-foreground">
        表达式与模板只能使用这里的输入名
        {slotHint ? `；建议槽：${slotHint}` : ''}
        ，例如 {'{{name}}'}。
      </p>
    </div>
  )
}
