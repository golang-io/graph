/**
 * SwitchConfigFields — 条件分支属性表单：IF / ELIF… / ELSE。
 * 默认 IF + ELSE 两路；「+ ELIF」在 ELSE 之前追加条件分支，最多 MAX_SWITCH_CASES 路。
 * 总是以 Cases 写回；分支 ID 由 nextSwitchCaseID 分配且不随删除重排，已连的边不受影响。
 */
import { Button, Input } from './host-ui'
import { ListRowRemoveButton } from './ListRowRemoveButton'
import {
  autoCaseLabel,
  MAX_SWITCH_CASES,
  nextSwitchCaseID,
  readCases,
  type SwitchCase,
} from './switch-config'

export type { SwitchCase }

export function SwitchConfigFields({
  config,
  onReplace,
}: {
  config: Record<string, unknown>
  onReplace: (cfg: Record<string, unknown>) => void
}) {
  const cases = readCases(config)
  const conds = cases.filter((c) => !c.IsDefault)
  const fallback = cases.find((c) => c.IsDefault)

  // 条件分支在前、ELSE 在后写回；空 Label 不落盘，显示时回退为自动名
  function write(nextConds: SwitchCase[], nextFallback = fallback) {
    const clean = ({ Label, ...rest }: SwitchCase): SwitchCase => (Label?.trim() ? { ...rest, Label } : rest)
    onReplace({ Cases: [...nextConds, ...(nextFallback ? [nextFallback] : [])].map(clean) })
  }
  const patchCond = (idx: number, patch: Partial<SwitchCase>) =>
    write(conds.map((c, i) => (i === idx ? { ...c, ...patch } : c)))
  const addID = cases.length < MAX_SWITCH_CASES ? nextSwitchCaseID(cases.map((c) => c.ID)) : null

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-center justify-between gap-2">
        <span className="text-sm font-semibold">分支</span>
        <Button
          type="button"
          size="sm"
          variant="secondary"
          disabled={!addID}
          onClick={() => addID && write([...conds, { ID: addID, Expr: '' }])}
        >
          + ELIF
        </Button>
      </div>

      {conds.map((c, idx) => (
        <div key={c.ID} className="flex flex-col gap-1.5 rounded-lg border border-border p-2.5">
          <div className="flex items-center gap-1.5">
            <span className="w-12 shrink-0 font-mono font-semibold text-emerald-600 dark:text-emerald-400">
              {autoCaseLabel(cases, idx)}
            </span>
            <Input
              className="h-7 text-xs"
              placeholder="显示名（可选）"
              value={c.Label ?? ''}
              onChange={(e) => patchCond(idx, { Label: e.target.value })}
            />
            {/* 至少保留一条条件分支（IF） */}
            {conds.length > 1 ? (
              <ListRowRemoveButton onClick={() => write(conds.filter((_, i) => i !== idx))} />
            ) : null}
          </div>
          <Input
            className="font-mono text-xs"
            value={c.Expr ?? ''}
            placeholder='score >= 60 / status == "ok"'
            onChange={(e) => patchCond(idx, { Expr: e.target.value })}
          />
        </div>
      ))}

      {fallback ? (
        <div className="flex flex-col gap-1.5 rounded-lg border border-dashed border-border p-2.5">
          <div className="flex items-center gap-1.5">
            <span className="w-12 shrink-0 font-mono font-semibold text-muted-foreground">ELSE</span>
            <Input
              className="h-7 text-xs"
              placeholder="显示名（可选）"
              value={fallback.Label ?? ''}
              onChange={(e) => write(conds, { ...fallback, Label: e.target.value })}
            />
          </div>
          <p className="text-muted-foreground">以上条件都不满足时走此分支</p>
        </div>
      ) : null}

      <p className="text-muted-foreground">
        expr-lang 布尔表达式，自上而下求值，命中第一条即走对应出口。删除分支会同时删除连在该出口上的边。
      </p>
    </div>
  )
}
