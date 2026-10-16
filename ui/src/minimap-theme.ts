/**
 * minimap-theme — MiniMap 颜色（仅用 hex/rgba，SVG fill 不吃现代 hsl 空格语法）。
 */
import type { Node } from '@xyflow/react'
import type { RFNodeData } from './graph-adapter'
import { isGraphDarkMode } from './edge-style'
import type { RunStatus } from './run-status'
import { catalogKind } from './node-registry'

export function graphMinimapBg(): string {
  return isGraphDarkMode() ? '#12121a' : '#fafafa'
}

export function graphMinimapMask(): string {
  return isGraphDarkMode() ? 'rgba(0,0,0,0.55)' : 'rgba(240,240,245,0.7)'
}

const minimapColorCache = new WeakMap<object, { dark: boolean; prefer: boolean; color: string }>()

function computeMinimapNodeColor(data: RFNodeData | undefined, prefer: boolean, dark: boolean): string {
  if (prefer) {
    const st = data?.runStatus as RunStatus | undefined
    if (st === 'succeeded') return '#16a34a'
    if (st === 'failed') return '#dc2626'
    if (st === 'running') return '#2563eb'
  }
  const nt = catalogKind(data?.nodeType ?? '')
  if (nt === 'annotation') {
    const c = data?.color?.trim()
    if (c && /^#[0-9a-fA-F]{3,8}$/.test(c)) return c
    return dark ? '#52525b' : '#d4d4d8'
  }
  if (nt === 'start') return '#06b6d4'
  if (nt === 'end') return dark ? '#a1a1aa' : '#171717'
  if (nt === 'switch') return '#f59e0b'
  if (nt === 'merge') return '#0d9488'
  if (nt === 'parallel') return '#4f46e5'
  if (nt === 'map') return '#c026d3'
  if (nt === 'stop_error') return '#e11d48'
  if (nt === 'subgraph') return '#4f46e5'
  if (nt === 'code') return '#059669'
  if (nt === 'llm' || nt === 'agent') return '#06b6d4'
  if (nt === 'http') return '#0ea5e9'
  return '#8b5cf6'
}

/** 运行态优先，否则按节点类型着色；按 n.data 引用缓存避免缩略图高频更新时重复解析 */
export function graphMinimapNodeColor(
  n: Node,
  opts?: { preferRunStatus?: boolean },
): string {
  const data = n.data as RFNodeData | undefined
  const prefer = Boolean(opts?.preferRunStatus)
  const dark = isGraphDarkMode()
  if (data && typeof data === 'object') {
    const cached = minimapColorCache.get(data)
    if (cached && cached.dark === dark && cached.prefer === prefer) {
      return cached.color
    }
    const color = computeMinimapNodeColor(data, prefer, dark)
    minimapColorCache.set(data, { dark, prefer, color })
    return color
  }
  return computeMinimapNodeColor(data, prefer, dark)
}
