/**
 * edge-style — 有向边：终点实心箭头接到目标节点左侧矩形入边；
 * 描边随明暗；统一使用圆角正交连线（smoothstep）；运行态可覆盖为绿/红/蓝。
 */
import { MarkerType, type DefaultEdgeOptions, type Edge } from '@xyflow/react'
import type { RunStatus } from './run-status'

/** 运行轨迹色：成功翠绿 / 失败绯红 / 运行中青蓝 */
export const RUN_STATUS_COLOR: Record<RunStatus, string> = {
  succeeded: '#10b981',
  failed: '#ef4444',
  running: '#06b6d4',
}

/**
 * 选中交互色：固定紫罗兰，不跟主题 --primary。
 * 与成功绿 / 失败红 / 运行青都错开；浅色深色同一色。
 */
export const GRAPH_SELECTION_COLOR = '#8b5cf6'

/** 默认连线描边；未执行节点边框保持更轻的 1px，不与此对齐 */
export const GRAPH_EDGE_STROKE_WIDTH = 1.75
/**
 * 运行结果 / 选中连线描边宽度。
 * 取整数，避免 CSS border 亚像素被浏览器收成 2px，和 SVG 线对不齐。
 * 节点执行态边框必须同宽。
 */
export const GRAPH_RUN_STROKE_WIDTH = 3

/** 节点运行态边框：与连线同色同宽，供 RF / X6 共用。 */
export function runStatusBorderStyle(
  runStatus?: RunStatus | null,
): { borderColor: string; borderWidth: number } | undefined {
  if (!runStatus) return undefined
  return {
    borderColor: RUN_STATUS_COLOR[runStatus],
    borderWidth: GRAPH_RUN_STROKE_WIDTH,
  }
}

/** 当前是否深色外观（data-color-mode 与 html.dark 双保险） */
export function isGraphDarkMode(): boolean {
  if (typeof document === 'undefined') return false
  const root = document.documentElement
  return root.getAttribute('data-color-mode') === 'dark' || root.classList.contains('dark')
}

export function graphEdgeStroke(): string {
  // 默认使用清透中性冷灰蓝，不使用绿色（绿色专属留给成功状态），杜绝暗黑沉重感
  return isGraphDarkMode() ? '#71717a' : '#94a3b8'
}

export function defaultGraphEdgeOptions(): DefaultEdgeOptions {
  const stroke = graphEdgeStroke()
  return {
    type: 'smoothstep',
    style: { stroke, strokeWidth: GRAPH_EDGE_STROKE_WIDTH },
    markerEnd: {
      type: MarkerType.ArrowClosed,
      width: 14,
      height: 14,
      color: stroke,
    },
  }
}

/**
 * 给边补上箭头、统一圆角形态与主题描边（保留 when / label 等业务字段）。
 * runStatus 有值时覆盖描边色；running 时 animated。
 */
export function withGraphEdgeStyle<T extends Edge>(
  edge: T,
  opts?: { runStatus?: RunStatus | null },
): T {
  const runStatus = opts?.runStatus
  const base = defaultGraphEdgeOptions()
  const stroke = runStatus ? RUN_STATUS_COLOR[runStatus] : base.style?.stroke
  const width = runStatus ? GRAPH_RUN_STROKE_WIDTH : GRAPH_EDGE_STROKE_WIDTH
  return {
    ...edge,
    type: 'smoothstep',
    animated: runStatus === 'running' ? true : (edge.animated ?? false),
    style: {
      ...base.style,
      ...edge.style,
      stroke,
      strokeWidth: width,
    },
    markerEnd: {
      type: MarkerType.ArrowClosed,
      width: 14,
      height: 14,
      color: stroke,
    },
  }
}
