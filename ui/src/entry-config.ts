/**
 * entry-config — 流水线/策略流入口配置类型。
 *
 * 产品页用这份配置声明画布能力与结构编辑自由度；业务按钮/面板仍由产品 slot 注入，
 * 避免 Shell 内堆积 showBatchImages 一类产品布尔开关。
 */
import type { CanvasRenderer } from './canvas-renderer'
import type { NodeTypeRegistry } from './node-registry'

/** 图结构来源与可改范围 */
export type GraphStructureMode =
  /** 用户自由增删节点与连线（创作中心 / 股票策略流） */
  | 'free'
  /** 后端投影 DAG；可拖布局、点选操作，一般不可手改拓扑（短剧流水线） */
  | 'bound'

export type GraphCanvasConfig = {
  /** 结构编辑自由度 */
  structure: GraphStructureMode
  /** 强制指定画布渲染器：'xyflow' | 'x6'；缺省根据本地偏好或 URL 自动判定 */
  renderer?: CanvasRenderer
  /** 是否在画布右上角展示渲染器切换徽标（默认 true） */
  showRendererSwitch?: boolean
  nodesDraggable?: boolean
  nodesConnectable?: boolean
  elementsSelectable?: boolean
  fitView?: boolean
  showMiniMap?: boolean
  showControls?: boolean
  /** Controls 是否允许锁定交互 */
  controlsInteractive?: boolean
  className?: string
}

/** 产品入口：registry + 画布能力；工具栏/属性面板用 React slot，不进本对象 */
export type GraphEntryConfig = {
  canvas: GraphCanvasConfig
  /** 业务节点注册（可与 default 合并后传入） */
  registry?: NodeTypeRegistry
}

/** 编辑画布：free 结构模式，可拖拽、连线、删除（其余能力由页面自行叠加） */
export const graphEditorCanvasConfig: GraphCanvasConfig = {
  structure: 'free',
  nodesDraggable: true,
  nodesConnectable: true,
  elementsSelectable: true,
  fitView: true,
  showMiniMap: true,
  showControls: true,
  controlsInteractive: true,
}

/** 运行详情：只读轨迹叠加 */
export const graphRunCanvasConfig: GraphCanvasConfig = {
  structure: 'bound',
  nodesDraggable: false,
  nodesConnectable: false,
  elementsSelectable: true,
  fitView: true,
  showMiniMap: true,
  showControls: true,
  controlsInteractive: false,
}
