import type { Viewport } from '@xyflow/react'

// 切页签时画布会卸载重挂：记住每个“图 + 渲染器”上次离开时的视口，
// 重新挂载时直接恢复，避免每次重新 fitView 造成缩放跳变。
// Viewport memory: canvases unmount/remount on tab switches; remember the last
// viewport per graph + renderer and restore it instead of re-fitting every time.
export interface ViewportMatrix {
  a: number
  b: number
  c: number
  d: number
  e: number
  f: number
}

export type RememberedViewport = { kind: 'x6'; matrix: ViewportMatrix } | { kind: 'xyflow'; viewport: Viewport }

const memory = new Map<string, RememberedViewport>()

/** 记忆键：同一张图在不同渲染器下视口不通用，按“图 ID + 渲染器”区分。 */
export function viewportKey(graphID: string, renderer: string): string {
  return `${renderer}:${graphID}`
}

export function recallViewport(key: string): RememberedViewport | undefined {
  return memory.get(key)
}

export function rememberViewport(key: string, viewport: RememberedViewport): void {
  memory.set(key, viewport)
}
