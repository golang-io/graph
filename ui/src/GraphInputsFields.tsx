/**
 * GraphInputsFields — 运行输入声明（Graph.InputSchema）编辑：每行一个输入名与是否必填。
 *
 * 启动 Task 时服务端按 Schema 校验 Input：缺少 required 或带未声明键都会被拒绝；
 * 节点通过 { From: '$input', Field: 名字 } 引用这些输入。
 *
 * Editor for Graph.InputSchema: run input names and whether each is required.
 */
import { Button, Input } from './host-ui'
import { ListRowRemoveButton } from './ListRowRemoveButton'
import { isValidDataName } from './node-outputs'
import { runInputFieldsFromSchema, runInputSchemaFromFields, type RunInputRow } from './run-input-schema'
import type { GraphJSONSchema } from './types'

export function GraphInputsFields({
  schema,
  onReplace,
}: {
  schema?: GraphJSONSchema | null
  onReplace: (schema: GraphJSONSchema | undefined) => void
}) {
  const inputs = runInputFieldsFromSchema(schema)
  const commit = (fields: RunInputRow[]) => onReplace(runInputSchemaFromFields(fields))
  const nextName = () => {
    const used = new Set(inputs.map((f) => f.name))
    for (let i = inputs.length + 1; ; i++) if (!used.has(`input${i}`)) return `input${i}`
  }
  const replaceAt = (idx: number, f: RunInputRow) =>
    commit(inputs.map((x, i) => (i === idx ? f : x)))

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-semibold">运行输入</span>
        <Button type="button" size="sm" variant="secondary" onClick={() => commit([...inputs, { name: nextName() }])}>
          + 输入
        </Button>
      </div>
      {inputs.map((f, idx) => {
        // 与 graph.ValidName 一致；不合法或重复时提示，保存时服务端会拒绝。
        const error = !isValidDataName(f.name)
          ? '名字只能包含字母、数字、下划线，且不能以数字开头'
          : inputs.some((x, i) => i < idx && x.name === f.name)
            ? '名字重复'
            : ''
        return (
          <div key={idx} className="space-y-1">
            <div className="flex items-center gap-2">
              <Input
                className="h-8 font-mono text-xs"
                value={f.name}
                aria-invalid={error ? true : undefined}
                onChange={(e) => replaceAt(idx, { ...f, name: e.target.value })}
              />
              <label className="flex shrink-0 items-center gap-1 text-xs text-muted-foreground">
                <input
                  type="checkbox"
                  checked={Boolean(f.required)}
                  onChange={(e) =>
                    replaceAt(idx, e.target.checked ? { name: f.name, required: true } : { name: f.name })
                  }
                />
                必填
              </label>
              <ListRowRemoveButton onClick={() => commit(inputs.filter((_, i) => i !== idx))} />
            </div>
            {error ? <p className="text-[11px] text-destructive">{error}</p> : null}
          </div>
        )
      })}
      <p className="text-[11px] text-muted-foreground">
        启动时 Input 只能包含这里声明的键；节点在「输入」中选择来源「运行输入」引用它们。
      </p>
    </div>
  )
}
