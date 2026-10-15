/**
 * switch-config — 条件分支 config 读写辅助，统一为 IF / ELIF… / ELSE 一种模型。
 * 引擎约定（builtin.SwitchInput，配置键大驼峰）：
 * - 无 Cases（旧的 IF/ELSE 存储）：Expr 为真走 Case0，否则走 Case1；
 * - 有 Cases：按序求值条件分支，命中走对应 ID，都不命中走 IsDefault 那条。
 * 编辑器总是以 Cases 写回；分支 ID 一经分配不再变化，出边按 ID 连接，删改其它分支不影响它。
 */
import { GRAPH_ENDPOINT_ERROR } from './types'

/** switch 出口上限（与 node.MaxSwitchCases 一致）。 */
export const MAX_SWITCH_CASES = 16

/** 分支的一项（builtin.SwitchCase）。IsDefault 为兜底分支（ELSE）。 */
export type SwitchCase = {
  ID: string
  Label?: string
  Expr?: string
  IsDefault?: boolean
}

/** 第 i 个分支出口 ID（与 builtin.SwitchCaseID 一致：Case0..Case15）。 */
export function switchCaseID(i: number): string {
  return `Case${i}`
}

/**
 * 读出分支列表，顺序为：条件分支（第一条 IF，其余 ELIF）在前，兜底（ELSE）恰好一条且排最后。
 * - 无 Cases：视为 [IF(Expr)=Case0, ELSE=Case1]，与引擎对旧存储的求值一致；
 * - 有 Cases：条件分支保持原顺序；缺兜底时补一条未占用 ID 的 ELSE，多条时只保留第一条。
 */
export function readCases(config: Record<string, unknown>): SwitchCase[] {
  const raw = Array.isArray(config.Cases) ? (config.Cases as SwitchCase[]) : []
  if (raw.length === 0) {
    return [
      { ID: switchCaseID(0), Expr: String(config.Expr ?? '') },
      { ID: switchCaseID(1), IsDefault: true },
    ]
  }
  const all = raw.map((c): SwitchCase => ({
    ID: String(c.ID),
    ...(c.Label ? { Label: c.Label } : {}),
    ...(c.IsDefault ? { IsDefault: true } : { Expr: c.Expr ?? '' }),
  }))
  const conds = all.filter((c) => !c.IsDefault)
  const fallback = all.find((c) => c.IsDefault)
  if (fallback) return [...conds, fallback]
  const id = nextSwitchCaseID(all.map((c) => c.ID))
  // 16 个 ID 全被条件分支占用时无法补兜底，原样返回，由后端校验报告
  return id ? [...conds, { ID: id, IsDefault: true }] : conds
}

/** 按位置给出分支的自动显示名：第一条 IF，中间 ELIF n，兜底 ELSE。 */
export function autoCaseLabel(cases: SwitchCase[], index: number): string {
  if (cases[index]?.IsDefault) return 'ELSE'
  return index === 0 ? 'IF' : `ELIF ${index}`
}

/** 分支显示名：作者填写的 Label 优先，否则用自动显示名。 */
export function switchCaseLabel(cases: SwitchCase[], index: number): string {
  return cases[index]?.Label?.trim() || autoCaseLabel(cases, index)
}

/** 卡片副标题：只有一条条件分支时显示其表达式，否则显示分支路数。 */
export function switchSummary(config: Record<string, unknown>): string {
  const cases = readCases(config)
  if (cases.length <= 2) return cases[0]?.Expr || '条件表达式'
  return `${cases.length} 路分支`
}

/** 分配下一个未占用的 CaseN（0..15）；满则返回 null。 */
export function nextSwitchCaseID(used: Iterable<string>): string | null {
  const set = new Set(used)
  for (let i = 0; i < MAX_SWITCH_CASES; i++) {
    const id = switchCaseID(i)
    if (!set.has(id)) return id
  }
  return null
}

export function edgeWhenLabel(
  when?: string,
  casesOrConfig?: SwitchCase[] | Record<string, unknown>,
): string | undefined {
  if (!when) return undefined
  if (when === GRAPH_ENDPOINT_ERROR) return 'ERROR'
  const cases = Array.isArray(casesOrConfig)
    ? casesOrConfig
    : casesOrConfig
      ? readCases(casesOrConfig)
      : undefined
  const index = cases?.findIndex((c) => c.ID === when) ?? -1
  if (cases && index >= 0) return switchCaseLabel(cases, index)
  if (when === switchCaseID(0)) return 'IF'
  if (when === switchCaseID(1)) return 'ELSE'
  return when
}
