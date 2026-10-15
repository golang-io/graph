/**
 * handle-when — 画布句柄 id 与分支本地名的边界。
 *
 * 默认单口名为 In / Out：只用于 React Flow / X6 锚点，不承载分支语义。
 * Case0..Case15 | Error 才是分支本地名（写进端点 ID 的本地名部分）。
 */
import { GRAPH_DEFAULT_INPUT, GRAPH_DEFAULT_OUTPUT } from './types'

/** 把 sourceHandle 收成分支本地名；默认端点 In / Out 返回 undefined（无分支）。 */
export function engineWhenFromHandle(id?: string | null): string | undefined {
  if (!id) return undefined
  const s = id.trim()
  if (!s || s === GRAPH_DEFAULT_INPUT || s === GRAPH_DEFAULT_OUTPUT) return undefined
  return s
}
