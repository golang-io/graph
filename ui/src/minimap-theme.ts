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

/** 运行态优先，否则按节点类型着色 */
export function graphMinimapNodeColor(
  n: Node,
  opts?: { preferRunStatus?: boolean },
): string {
  const data = n.data as RFNodeData | undefined
  if (opts?.preferRunStatus) {
    const st = data?.runStatus as RunStatus | undefined
    if (st === 'succeeded') return '#16a34a'
    if (st === 'failed') return '#dc2626'
    if (st === 'running') return '#2563eb'
  }
  const nt = catalogKind(data?.nodeType ?? '')
  if (nt === 'annotation') {
    const c = data?.color?.trim()
    if (c && /^#[0-9a-fA-F]{3,8}$/.test(c)) return c
    return isGraphDarkMode() ? '#52525b' : '#d4d4d8'
  }
  if (nt === 'start') return '#06b6d4'
  if (nt === 'end') return isGraphDarkMode() ? '#a1a1aa' : '#171717'
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
