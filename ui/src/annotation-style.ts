/**
 * annotation-style — 注释框尺寸/颜色工具（无 React / graph-adapter 依赖，避免循环引用）。
 */
export const ANNOTATION_DEFAULT_SIZE = { w: 280, h: 160 }

/** 默认中性灰（与原先 Tailwind neutral 虚线框一致） */
export const ANNOTATION_DEFAULT_COLOR = '#a3a3a3'

/** 属性面板预设色（含默认灰） */
export const ANNOTATION_COLOR_PRESETS = [
  '#a3a3a3',
  '#64748b',
  '#3b82f6',
  '#06b6d4',
  '#22c55e',
  '#f59e0b',
  '#f43f5e',
  '#8b5cf6',
] as const

/** 规范化为 #rrggbb；非法则回默认 */
export function normalizeAnnotationColor(raw?: string | null): string {
  if (!raw || typeof raw !== 'string') return ANNOTATION_DEFAULT_COLOR
  const s = raw.trim()
  const m6 = /^#([0-9a-fA-F]{6})$/.exec(s)
  if (m6) return `#${m6[1].toLowerCase()}`
  const m3 = /^#([0-9a-fA-F]{3})$/.exec(s)
  if (m3) {
    const [a, b, c] = m3[1]
    return `#${a}${a}${b}${b}${c}${c}`.toLowerCase()
  }
  return ANNOTATION_DEFAULT_COLOR
}

/** 解析存储用颜色：合法则返回规范化 hex，否则 undefined（用默认样式） */
export function parseAnnotationColor(raw: unknown): string | undefined {
  if (typeof raw !== 'string' || !raw.trim()) return undefined
  const s = raw.trim()
  if (!/^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$/.test(s)) return undefined
  return normalizeAnnotationColor(s)
}

export function annotationColorRgba(hex: string, a: number): string {
  const h = normalizeAnnotationColor(hex).slice(1)
  const r = parseInt(h.slice(0, 2), 16)
  const g = parseInt(h.slice(2, 4), 16)
  const b = parseInt(h.slice(4, 6), 16)
  return `rgba(${r},${g},${b},${a})`
}
