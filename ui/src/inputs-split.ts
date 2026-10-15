/**
 * 画布编辑态仍用「字面量 map + 引用 map」两份视图，落盘只写 Node.Inputs。
 * Wire format has a single Inputs map; the editor splits literals vs bindings for widgets.
 */
import type { GraphInputRef } from './types'

/** 从实例 Inputs 拆出字面量（供端点展开 / 原 config 控件）与引用绑定。 */
export function splitInputs(all?: Record<string, GraphInputRef>): {
  literals: Record<string, unknown>
  bindings: Record<string, GraphInputRef>
} {
  const literals: Record<string, unknown> = {}
  const bindings: Record<string, GraphInputRef> = {}
  for (const [k, ref] of Object.entries(all ?? {})) {
    if (!ref) continue
    if (ref.From) {
      bindings[k] = { From: ref.From, Field: ref.Field }
      continue
    }
    if (ref.Value !== undefined) {
      literals[k] = ref.Value
      continue
    }
    bindings[k] = { ...ref }
  }
  return { literals, bindings }
}

/** 合并字面量与引用为落盘 Inputs；同一键以引用优先。 */
export function mergeInputs(
  literals: Record<string, unknown> | undefined,
  bindings?: Record<string, GraphInputRef>,
): Record<string, GraphInputRef> {
  const out: Record<string, GraphInputRef> = {}
  for (const [k, v] of Object.entries(literals ?? {})) {
    out[k] = { Value: v }
  }
  for (const [k, ref] of Object.entries(bindings ?? {})) {
    out[k] = ref
  }
  return out
}

/** DefaultInputs / Preset.Inputs → 字面量默认表（忽略引用项）。 */
export function literalsFromInputRefs(refs?: Record<string, GraphInputRef>): Record<string, unknown> {
  return splitInputs(refs).literals
}
