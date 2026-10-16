/**
 * GraphNodes — 产品向节点视图
 *
 * - 句柄：出边圆点、入边小矩形；每个端点各自选上下左右（出口不能与入口同边）
 * - runStatus：运行详情叠加绿/红/蓝边框
 */
import { Handle, Position, useUpdateNodeInternals, type Node, type NodeProps } from '@xyflow/react'
import { CirclePlay, CircleStop, GitBranch, GitFork, GitMerge, Maximize2, Minimize2, Repeat, Sparkles, type LucideIcon } from 'lucide-react'
import { createContext, memo, useContext, useEffect, useRef, type CSSProperties, type ReactNode } from 'react'
import { cn } from './cn'
import { runStatusBorderStyle } from './edge-style'
import type { RFNodeData } from './graph-adapter'
import { catalogKind, resolveStepAppearance } from './node-registry'
import { useGraphNodeRegistry } from './GraphNodeRegistryContext'
import { endpointIds, endpointSide, SIDE_LABEL, resolveEndpointSides, sideAxis, type EndpointSides, type Side } from './orient'
import type { RunStatus } from './run-status'
import { AnnotationBoxView } from './AnnotationBox'
import { switchSummary } from './switch-config'
import { nodeHasErrorOutput, resolveGraphNodeEndpoints, type GraphEndpointSet } from './endpoints'
import { BODY_HEADER, bodyGraphOf } from './body-scope'
import { AGENT_CARD_H, branchCardHeight, CARD_H, CARD_W, ENDPOINT_H, ENDPOINT_SHAPE_CLASS, ENDPOINT_W, specOf } from './node-size'
import { GRAPH_DEFAULT_INPUT, GRAPH_DEFAULT_OUTPUT, GRAPH_ENDPOINT_ERROR } from './types'
import './graph-handles.css'

function sideToPosition(side: Side): Position {
  switch (side) {
    case 'left':
      return Position.Left
    case 'right':
      return Position.Right
    case 'top':
      return Position.Top
    case 'bottom':
      return Position.Bottom
  }
}

/**
 * 卡片句柄位置：inPos 为第一个入口所在边，outPos 为第一个出口所在边（卡片内边距按它留白），axis 为 outPos 所在轴。
 * 没有入口（start）或出口（end）时按缺省边。sides 须已按 set 补齐（useEndpointSides）。
 */
export function handlePositions(sides: EndpointSides, set: GraphEndpointSet): {
  inPos: Position
  outPos: Position
  axis: 'x' | 'y'
} {
  const inSide = endpointSide(sides, set.inputs[0]?.id ?? GRAPH_DEFAULT_INPUT, 'in')
  const outSide = endpointSide(sides, set.outputs[0]?.id ?? GRAPH_DEFAULT_OUTPUT, 'out')
  return { inPos: sideToPosition(inSide), outPos: sideToPosition(outSide), axis: sideAxis(outSide) }
}

/** 节点实例的端点（按注册表 Spec 或自定义节点的 displayEndpoints，并按配置展开 switch 分支等） */
function useEndpointSet(data: RFNodeData, fallbackType: string): GraphEndpointSet {
  const nodeType = data.nodeType || fallbackType
  const spec = specOf(useGraphNodeRegistry(), nodeType)
  return resolveGraphNodeEndpoints(nodeType, spec?.Endpoints ?? data.endpointSpec, data.config, spec?.Renderer)
}

const handleSourceCls = cn(
  'graph-handle-source',
  '!bg-slate-400 hover:!bg-primary',
  'dark:!bg-slate-500 dark:hover:!bg-primary',
)
const handleTargetCls = cn(
  'graph-handle-target',
  '!bg-slate-400 hover:!bg-primary',
  'dark:!bg-slate-500 dark:hover:!bg-primary',
)

const nodeShell = 'graph-node-shell'
const nodeShellSelected = 'graph-node-shell-selected'

/**
 * 节点内图标：轻拟物色彩托衬（Squircle Badge），统一视觉锚点。
 */
function NodeIcon({
  icon: Icon,
  className,
  badgeCls,
}: {
  icon: LucideIcon
  className?: string
  badgeCls?: string
}) {
  return (
    <span
      className={cn(
        'flex h-9 w-9 shrink-0 items-center justify-center rounded-xl transition-all duration-200',
        badgeCls || 'bg-muted/70 text-foreground ring-1 ring-border/50',
      )}
    >
      <Icon className={cn('h-4.5 w-4.5', className)} strokeWidth={2} aria-hidden />
    </span>
  )
}

/**
 * 智能体核心图标：霓虹青环 + Sparkles，一眼区别于普通 step 的单色线标。
 * Agent core: cyan ring + Sparkles so AI nodes read differently from plain steps.
 */
function AgentCoreIcon() {
  return (
    <span
      className={cn(
        'graph-agent-core relative flex h-10 w-10 shrink-0 items-center justify-center rounded-xl',
        'bg-cyan-500/15 ring-1 ring-cyan-400/50 dark:bg-cyan-400/15 dark:ring-cyan-300/40',
      )}
      aria-hidden
    >
      <Sparkles className="h-5 w-5 text-cyan-600 dark:text-cyan-300" strokeWidth={2.25} />
    </span>
  )
}

function runStatusPulseCls(runStatus?: RunStatus): string | false {
  return runStatus === 'running' && 'graph-node-running'
}

/** 按节点入口所在边返回断点红点的定位 class（贴在入口边中点，与入口端口重合） */
export function breakpointMarkPositionCls(inSide?: Position | Side): string {
  switch (inSide) {
    case Position.Right:
    case 'right':
      return '-right-px top-1/2 translate-x-1/2 -translate-y-1/2'
    case Position.Top:
    case 'top':
      return 'left-1/2 -top-px -translate-x-1/2 -translate-y-1/2'
    case Position.Bottom:
    case 'bottom':
      return '-bottom-px left-1/2 -translate-x-1/2 translate-y-1/2'
    default:
      return '-left-px top-1/2 -translate-x-1/2 -translate-y-1/2'
  }
}

/** 断点徽标（节点入口处）/ 注入黄点（左上角）（编辑画布调试，不进流程配置） */
function NodeDebugMarks({
  breakpoint,
  inject,
  inPos,
}: {
  breakpoint?: boolean
  inject?: boolean
  inPos?: Position | Side
}) {
  return (
    <>
      {inject ? (
        <span
          data-debug-inj="1"
          className="pointer-events-none absolute -left-1.5 -top-1.5 z-10 h-3.5 w-3.5 rounded-full bg-amber-500 ring-2 ring-background shadow-sm"
          title="debug inject"
          aria-hidden
        />
      ) : null}
      {breakpoint ? (
        <span
          data-debug-bp="1"
          className={cn(
            'graph-debug-bp pointer-events-none absolute z-20',
            breakpointMarkPositionCls(inPos),
          )}
          title="breakpoint"
          aria-hidden
        />
      ) : null}
    </>
  )
}

/**
 * 多出口沿边排布。正交轴必须显式清掉，否则从上下改左右时 left/top 残留，
 * 句柄看起来在新边、边却仍锚在旧坐标。
 */
function multiOutStyle(axis: 'x' | 'y', pct: number): CSSProperties {
  if (axis === 'x') {
    return { top: `${pct}%`, left: 'auto', right: 'auto', bottom: 'auto' }
  }
  return { left: `${pct}%`, top: 'auto', right: 'auto', bottom: 'auto' }
}

export function branchLabelStyle(pos: Position | Side, pct: number): CSSProperties {
  if (pos === Position.Right) return { right: 14, top: `${pct}%`, left: 'auto', bottom: 'auto', transform: 'translateY(-50%)' }
  if (pos === Position.Left) return { left: 14, top: `${pct}%`, right: 'auto', bottom: 'auto', transform: 'translateY(-50%)' }
  if (pos === Position.Bottom) return { bottom: 12, left: `${pct}%`, top: 'auto', right: 'auto', transform: 'translateX(-50%)' }
  return { top: 12, left: `${pct}%`, right: 'auto', bottom: 'auto', transform: 'translateX(-50%)' }
}

/** 选中节点的提示：各端点所在边，如「In 左 · Out 右 · Error 下」 */
function sidesHint(sides: EndpointSides): string {
  return Object.entries(sides).map(([id, side]) => `${id} ${SIDE_LABEL[side]}`).join(' · ')
}

/**
 * 统一节点壳：在 DOM 上标记节点 ID / 类型（data-node-id / data-node-type），供测试与样式定位。
 * 删除节点走键盘 Delete 与属性面板，不在节点上挂悬停工具条。
 * Node shell: tags the DOM with node id/type; deletion goes through keyboard and the props panel.
 */
function NodeShell({
  id,
  nodeType,
  className,
  style,
  title,
  children,
}: {
  id: string
  nodeType: string
  className?: string
  style?: CSSProperties
  title?: string
  children: ReactNode
}) {
  return (
    <div data-node-id={id} data-node-type={nodeType} className={className} style={style} title={title}>
      {children}
    </div>
  )
}

/**
 * 按实例端点补齐各端点所在边（未写明的入口左、出口右，另有其他出口时 Error 下），
 * 并在任一端点所在边变化后强制 RF 重测句柄，连线才会跟着走。
 * 首次挂载由 React Flow 内置 ResizeObserver 统一批量测量，仅在挂载后方位变化时触发单节点重测。
 */
function useEndpointSides(id: string, sides: EndpointSides | undefined, set: GraphEndpointSet): EndpointSides {
  const update = useUpdateNodeInternals()
  const resolved = resolveEndpointSides(sides, endpointIds(set))
  const key = JSON.stringify(resolved)
  const prevKeyRef = useRef(key)
  useEffect(() => {
    if (prevKeyRef.current === key) return
    prevKeyRef.current = key
    const t = requestAnimationFrame(() => update(id))
    return () => cancelAnimationFrame(t)
  }, [id, key, update])
  return resolved
}

function useErrorHandleInternals(id: string, showError: boolean) {
  const update = useUpdateNodeInternals()
  const prevRef = useRef(showError)
  useEffect(() => {
    if (prevRef.current === showError) return
    prevRef.current = showError
    const t = requestAnimationFrame(() => update(id))
    return () => cancelAnimationFrame(t)
  }, [id, showError, update])
}

/**
 * 出口徽标配色：plain 不画徽标（单出口节点）；ok/muted 用于真/假；
 * error 为 ERROR 出口；indigo 为并行分支。
 */
type OutputTone = 'plain' | 'ok' | 'muted' | 'error' | 'indigo'
type OutputItem = { id: string; label?: string; tone: OutputTone }

const badgeBase = 'pointer-events-none absolute select-none rounded-full px-1.5 py-0.5 text-[9px] font-semibold tracking-wide shadow-2xs'
const toneCls: Record<Exclude<OutputTone, 'plain'>, string> = {
  ok: 'bg-emerald-500/12 text-emerald-700 ring-1 ring-emerald-500/25 dark:bg-emerald-500/20 dark:text-emerald-300',
  muted: 'bg-rose-500/12 text-rose-700 ring-1 ring-rose-500/25 dark:bg-rose-500/20 dark:text-rose-300',
  error: 'bg-rose-500/12 text-rose-700 ring-1 ring-rose-500/25 dark:bg-rose-500/20 dark:text-rose-300',
  indigo: 'bg-indigo-500/12 text-indigo-700 ring-1 ring-indigo-500/25 dark:bg-indigo-500/20 dark:text-indigo-300',
}

/** ERROR 出口条目：仅静态声明了 Error 出口的节点（如 stop_error）使用 */
const ERROR_OUTPUT: OutputItem = { id: GRAPH_ENDPOINT_ERROR, label: 'ERROR', tone: 'error' }

/**
 * 出口句柄：每个出口取 sides 中写明的所在边（sides 须已 useEndpointSides 补齐），同一条边上的出口按百分比均匀排布。
 */
function OutputHandles({ sides, items }: {
  sides: EndpointSides
  items: OutputItem[]
}) {
  const bySide = new Map<Side, OutputItem[]>()
  for (const item of items) {
    const side = endpointSide(sides, item.id, 'out')
    bySide.set(side, [...(bySide.get(side) ?? []), item])
  }
  return (
    <>
      {[...bySide].flatMap(([side, list]) => list.map((item, i) => {
        const pct = ((i + 1) / (list.length + 1)) * 100
        const pos = sideToPosition(side)
        return (
          <span key={item.id}>
            {item.tone !== 'plain' && item.label ? (
              <span className={cn(badgeBase, toneCls[item.tone])} style={branchLabelStyle(pos, pct)}>
                {item.label}
              </span>
            ) : null}
            <Handle
              type="source"
              id={item.id}
              position={pos}
              className={handleSourceCls}
              style={multiOutStyle(sideAxis(side), pct)}
              title={item.label}
            />
          </span>
        )
      }))}
    </>
  )
}

/**
 * 开始节点：左侧半圆弧、右侧竖直边（仅保留小圆角），与结束节点左右镜像；仅出边（控制入口，无数据输出）。
 * Start node: rounded left arc, flat right edge; mirrors the end node.
 */
export function StartNodeView({ id, data, selected }: NodeProps<Node<RFNodeData>>) {
  const set = useEndpointSet(data, 'start')
  const sides = useEndpointSides(id, data.endpointSides, set)
  const { outPos } = handlePositions(sides, set)
  const registry = useGraphNodeRegistry()
  const def = registry.get(data.nodeType || 'start')
  const Icon = def?.icon ?? CirclePlay
  const iconClass = def?.iconClass ?? 'text-cyan-700 dark:text-cyan-300'
  const subtitle = def?.subtitle ?? '控制入口'
  return (
    <NodeShell
      id={id}
      nodeType={data.nodeType || 'start'}
      className={cn(
        'relative flex items-center gap-2.5 px-3.5',
        ENDPOINT_SHAPE_CLASS.start,
        nodeShell,
        runStatusPulseCls(data.runStatus),
        selected && nodeShellSelected,
      )}
      style={{ width: ENDPOINT_W, height: ENDPOINT_H, ...runStatusBorderStyle(data.runStatus) }}
      title={selected ? `${sidesHint(sides)} · R 旋转端点` : undefined}
    >
      <NodeDebugMarks
        breakpoint={data.breakpoint}
        inject={data.inject}
        inPos={outPos === Position.Left ? Position.Right : Position.Left}
      />
      <NodeIcon
        icon={Icon}
        className={iconClass}
        badgeCls="bg-cyan-500/12 ring-1 ring-cyan-500/25"
      />
      <div className="min-w-0">
        <div className="truncate text-sm font-semibold">{data.label || '开始'}</div>
        <div className="truncate text-[11px] text-muted-foreground">{subtitle}</div>
      </div>
      <Handle type="source" id={GRAPH_DEFAULT_OUTPUT} position={outPos} className={handleSourceCls} />
    </NodeShell>
  )
}

/**
 * 结束节点：左侧竖直边（仅保留小圆角）、右侧半圆弧，与开始节点左右镜像；仅入边。
 * End node: flat left edge, rounded right arc; mirrors the start node.
 */
export function EndNodeView({ id, data, selected }: NodeProps<Node<RFNodeData>>) {
  const set = useEndpointSet(data, 'end')
  const sides = useEndpointSides(id, data.endpointSides, set)
  const { inPos } = handlePositions(sides, set)
  const registry = useGraphNodeRegistry()
  const def = registry.get(data.nodeType || 'end')
  const Icon = def?.icon ?? CircleStop
  const iconClass = def?.iconClass ?? 'text-slate-700 dark:text-slate-300'
  const subtitle = def?.subtitle ?? '输出终点'
  return (
    <NodeShell
      id={id}
      nodeType={data.nodeType || 'end'}
      className={cn(
        'relative flex items-center gap-2.5 px-3.5',
        ENDPOINT_SHAPE_CLASS.end,
        nodeShell,
        runStatusPulseCls(data.runStatus),
        selected && nodeShellSelected,
      )}
      style={{ width: ENDPOINT_W, height: ENDPOINT_H, ...runStatusBorderStyle(data.runStatus) }}
      title={selected ? `${sidesHint(sides)} · R 旋转端点` : undefined}
    >
      <NodeDebugMarks breakpoint={data.breakpoint} inject={data.inject} inPos={inPos} />
      <Handle type="target" id={GRAPH_DEFAULT_INPUT} position={inPos} className={handleTargetCls} />
      <NodeIcon
        icon={Icon}
        className={iconClass}
        badgeCls="bg-slate-500/12 ring-1 ring-slate-500/25"
      />
      <div className="min-w-0">
        <div className="truncate text-sm font-semibold">{data.label || '结束'}</div>
        <div className="truncate text-[11px] text-muted-foreground">{subtitle}</div>
      </div>
    </NodeShell>
  )
}

export function StepNodeView({ id, data, selected }: NodeProps<Node<RFNodeData>>) {
  const set = useEndpointSet(data, 'step')
  const sides = useEndpointSides(id, data.endpointSides, set)
  const { inPos } = handlePositions(sides, set)
  const registry = useGraphNodeRegistry()
  // stop_error：唯一出口即 ERROR；其它节点不再动态追加 error 口
  const isStopError = catalogKind(data.nodeType) === 'stop_error'
  /** llm 引擎类型在产品侧呈现为「智能体」：专属壳与普通 step 拉开层级 */
  const isAgent = catalogKind(data.nodeType) === 'llm' || data.nodeType === 'agent'
  const showErrorHandle = !isStopError && nodeHasErrorOutput(data.nodeType, data.config, specOf(registry, data.nodeType))
  useErrorHandleInternals(id, showErrorHandle)
  const meta = resolveStepAppearance(data.nodeType, data.config ?? {}, registry)

  return (
    <NodeShell
      id={id}
      nodeType={data.nodeType}
      className={cn(
        'relative rounded-xl px-3.5 py-2.5',
        isAgent ? 'graph-agent-node' : nodeShell,
        runStatusPulseCls(data.runStatus),
        selected && (isAgent ? 'graph-agent-node-selected' : nodeShellSelected),
      )}
      style={{
        width: CARD_W,
        height: isAgent ? AGENT_CARD_H : CARD_H,
        ...runStatusBorderStyle(data.runStatus),
      }}
      title={selected ? `${sidesHint(sides)} · R 旋转端点` : undefined}
    >
      <NodeDebugMarks breakpoint={data.breakpoint} inject={data.inject} inPos={inPos} />
      <Handle type="target" id={GRAPH_DEFAULT_INPUT} position={inPos} className={handleTargetCls} />
      <div className="relative z-[1] flex h-full items-center gap-3">
        {isAgent ? (
          <AgentCoreIcon />
        ) : (
          <NodeIcon
            icon={meta.icon}
            className={meta.iconClass}
            badgeCls={cn(
              'bg-primary/10 text-primary ring-1 ring-primary/20',
              isStopError && 'bg-rose-500/15 text-rose-600 ring-rose-500/30 dark:text-rose-400',
            )}
          />
        )}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-1.5">
            <div className="truncate text-sm font-semibold">{data.label}</div>
            {isAgent ? (
              <span
                className={cn(
                  'shrink-0 rounded px-1.5 py-0.5 text-[9px] font-bold tracking-wide',
                  'bg-cyan-500/15 text-cyan-700 ring-1 ring-cyan-500/30',
                  'dark:bg-cyan-400/15 dark:text-cyan-300 dark:ring-cyan-400/35',
                )}
              >
                AI
              </span>
            ) : null}
          </div>
          <div className="truncate text-[11px] text-muted-foreground">{meta.hint}</div>
        </div>
      </div>
      <OutputHandles
        sides={sides}
        items={isStopError ? [] : [{ id: GRAPH_DEFAULT_OUTPUT, tone: 'plain' }, ...(showErrorHandle ? [ERROR_OUTPUT] : [])]}
      />
    </NodeShell>
  )
}

/**
 * 汇合：多路入边 + 单路出边（无 ERROR）。
 * mode=any 任意一路到即继续；mode=all 等齐（单游标下首次到达挂起，续跑放行）。
 */
export function MergeNodeView({ id, data, selected }: NodeProps<Node<RFNodeData>>) {
  const set = useEndpointSet(data, 'merge')
  const sides = useEndpointSides(id, data.endpointSides, set)
  const { inPos } = handlePositions(sides, set)
  // 多路入口沿第一个入口所在边排布，轴取入口边（不能取出口边，入/出可能不在同一轴上）
  const inAxis = sideAxis(endpointSide(sides, set.inputs[0]?.id ?? GRAPH_DEFAULT_INPUT, 'in'))
  const inputs = set.inputs
  const modeRaw = String(data.config?.Mode || 'any')
  const mode = modeRaw === 'all' ? 'all' : 'any'
  const h = branchCardHeight(inputs.length)
  // 入边句柄数在挂载后变化时重测锚点
  const update = useUpdateNodeInternals()
  const prevInLenRef = useRef(inputs.length)
  useEffect(() => {
    if (prevInLenRef.current === inputs.length) return
    prevInLenRef.current = inputs.length
    const t = requestAnimationFrame(() => update(id))
    return () => cancelAnimationFrame(t)
  }, [id, inputs.length, update])

  return (
    <NodeShell
      id={id}
      nodeType={data.nodeType || 'merge'}
      className={cn(
        'relative rounded-xl px-3.5 py-2.5',
        nodeShell,
        runStatusPulseCls(data.runStatus),
        selected && nodeShellSelected,
      )}
      style={{ width: CARD_W, height: h, ...runStatusBorderStyle(data.runStatus) }}
      title={selected ? `${sidesHint(sides)} · R 旋转端点` : undefined}
    >
      <NodeDebugMarks breakpoint={data.breakpoint} inject={data.inject} inPos={inPos} />
      <div
        className={cn(
          'flex h-full items-center gap-2.5',
          inPos === Position.Left && 'pl-10',
          inPos === Position.Right && 'pr-10',
          inPos === Position.Top && 'pt-5',
          inPos === Position.Bottom && 'pb-5',
        )}
      >
        <NodeIcon
          icon={GitMerge}
          className="text-teal-600 dark:text-teal-400"
          badgeCls="bg-teal-500/12 ring-1 ring-teal-500/25"
        />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">
            {data.label || '汇合'}
            {data.runProgress ? (
              <span className="ml-1 font-mono text-[11px] font-medium text-teal-700 dark:text-teal-300">
                ({data.runProgress})
              </span>
            ) : null}
          </div>
          <div className="truncate text-[11px] text-muted-foreground">
            {mode} · {inputs.length} 入
          </div>
        </div>
      </div>
      {inputs.map((hnd, i) => {
        const pct = ((i + 1) / (inputs.length + 1)) * 100
        // 每个入口取自己所在的边；与第一个入口同边时沿 inAxis 均匀排布
        const side = endpointSide(sides, hnd.id, 'in'), pos = sideToPosition(side)
        return (
          <span key={hnd.id}>
            <span
              className="pointer-events-none absolute select-none rounded-full bg-teal-500/12 px-1.5 py-0.5 text-[9px] font-semibold tracking-wide text-teal-700 shadow-2xs ring-1 ring-teal-500/25 dark:bg-teal-500/20 dark:text-teal-300"
              style={branchLabelStyle(pos, pct)}
            >
              {hnd.label}
            </span>
            <Handle
              type="target"
              position={pos}
              id={hnd.id}
              className={handleTargetCls}
              style={multiOutStyle(pos === inPos ? inAxis : sideAxis(side), pct)}
              title={hnd.label}
            />
          </span>
        )
      })}
      <OutputHandles sides={sides} items={[{ id: GRAPH_DEFAULT_OUTPUT, tone: 'plain' }]} />
    </NodeShell>
  )
}

/**
 * 并行：一进多出；各出边同时推进。
 * 出边 when 存图时空（句柄 out 仅 UI）。
 */
export function ParallelNodeView({ id, data, selected }: NodeProps<Node<RFNodeData>>) {
  const set = useEndpointSet(data, 'parallel')
  const sides = useEndpointSides(id, data.endpointSides, set)
  const { inPos, outPos, axis } = handlePositions(sides, set)
  const outs = set.outputs
  const h = branchCardHeight(outs.length)
  const update = useUpdateNodeInternals()
  const prevOutLenRef = useRef(outs.length)
  useEffect(() => {
    if (prevOutLenRef.current === outs.length) return
    prevOutLenRef.current = outs.length
    const t = requestAnimationFrame(() => update(id))
    return () => cancelAnimationFrame(t)
  }, [id, outs.length, update])

  return (
    <NodeShell
      id={id}
      nodeType={data.nodeType || 'parallel'}
      className={cn(
        'relative rounded-xl px-3.5 py-2.5',
        nodeShell,
        runStatusPulseCls(data.runStatus),
        selected && nodeShellSelected,
      )}
      style={{ width: CARD_W, height: h, ...runStatusBorderStyle(data.runStatus) }}
      title={selected ? `${sidesHint(sides)} · R 旋转端点` : undefined}
    >
      <NodeDebugMarks breakpoint={data.breakpoint} inject={data.inject} inPos={inPos} />
      <Handle type="target" id={GRAPH_DEFAULT_INPUT} position={inPos} className={handleTargetCls} />
      <div
        className={cn(
          'flex h-full items-start gap-2.5 pt-1',
          axis === 'x' && outPos === Position.Right && 'pr-14',
          axis === 'x' && outPos === Position.Left && 'pl-14',
          axis === 'y' && 'pb-6',
        )}
      >
        <NodeIcon
          icon={GitFork}
          className="text-indigo-600 dark:text-indigo-400"
          badgeCls="bg-indigo-500/12 ring-1 ring-indigo-500/25"
        />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">{data.label || '并行'}</div>
          <div className="truncate text-[11px] text-muted-foreground">{outs.length} 路出边</div>
        </div>
      </div>
      <OutputHandles
        sides={sides}
        items={outs.map((hnd) => ({ id: hnd.id, label: hnd.label, tone: 'indigo' as const }))}
      />
    </NodeShell>
  )
}

export function SwitchNodeView({ id, data, selected }: NodeProps<Node<RFNodeData>>) {
  const set = useEndpointSet(data, 'switch')
  const sides = useEndpointSides(id, data.endpointSides, set)
  const { inPos, outPos, axis } = handlePositions(sides, set)
  const nodeType = data.nodeType || 'switch'
  const spec = specOf(useGraphNodeRegistry(), nodeType)
  const showError = nodeHasErrorOutput(nodeType, data.config, spec)
  const handles = set.outputs.filter((p) => !showError || p.id !== GRAPH_ENDPOINT_ERROR)
  useErrorHandleInternals(id, showError)
  const h = branchCardHeight(handles.length)
  const subtitle = switchSummary(data.config ?? {}).slice(0, 28)

  return (
    <NodeShell
      id={id}
      nodeType={data.nodeType || 'switch'}
      className={cn(
        'relative rounded-xl px-3.5 py-2.5',
        nodeShell,
        runStatusPulseCls(data.runStatus),
        selected && nodeShellSelected,
      )}
      style={{ width: CARD_W, height: h, ...runStatusBorderStyle(data.runStatus) }}
      title={selected ? `${sidesHint(sides)} · R 旋转端点` : undefined}
    >
      <NodeDebugMarks breakpoint={data.breakpoint} inject={data.inject} inPos={inPos} />
      <Handle type="target" id={GRAPH_DEFAULT_INPUT} position={inPos} className={handleTargetCls} />
      <div
        className={cn(
          'flex h-full items-start gap-2.5 pt-1',
          axis === 'x' && outPos === Position.Right && 'pr-14',
          axis === 'x' && outPos === Position.Left && 'pl-14',
          axis === 'y' && 'pb-6',
        )}
      >
        <NodeIcon
          icon={GitBranch}
          className="text-amber-600 dark:text-amber-400"
          badgeCls="bg-amber-500/12 ring-1 ring-amber-500/25"
        />
        <div className="min-w-0 flex-1">
          <div className="truncate text-sm font-semibold">{data.label || '条件分支'}</div>
          <div className="truncate text-[11px] text-muted-foreground">{subtitle}</div>
        </div>
      </div>
      <OutputHandles
        sides={sides}
        items={[
          ...handles.map((hnd) => ({
            id: hnd.id,
            label: hnd.label,
            tone: hnd.fallback ? ('muted' as const) : ('ok' as const),
          })),
          ...(showError ? [ERROR_OUTPUT] : []),
        ]}
      />
    </NodeShell>
  )
}

/**
 * 展开 / 收起容器的回调（参数为容器的画布 ID）。由 GraphCanvas 提供；为 null 时不显示按钮。
 * Toggle callback for map containers; provided by GraphCanvas.
 */
export const GraphBodyToggleContext = createContext<((id: string) => void) | null>(null)

/** 容器标题栏右侧的展开 / 收起按钮；nodrag / nopan 避免点击被当成拖动节点或平移画布。 */
function BodyToggleButton({ id, expanded }: { id: string; expanded: boolean }) {
  const toggle = useContext(GraphBodyToggleContext)
  if (!toggle) return null
  const Icon = expanded ? Minimize2 : Maximize2
  return (
    <button
      type="button"
      className="nodrag nopan flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted-foreground hover:bg-muted hover:text-foreground"
      title={expanded ? '收起为节点' : '展开循环体'}
      onClick={(e) => { e.stopPropagation(); toggle(id) }}
      onDoubleClick={(e) => e.stopPropagation()}
    >
      <Icon className="h-3.5 w-3.5" />
    </button>
  )
}

/**
 * Map：对输入 list 逐项运行循环体（参数 Body 中的内嵌子图），完成后沿 out 输出 results。
 * 展开时渲染为包住循环体节点的大框（循环体节点是它的子节点），收起时渲染为普通卡片，用于概览。
 */
export function MapNodeView({ id, data, selected }: NodeProps<Node<RFNodeData>>) {
  const set = useEndpointSet(data, '__INNER__.map')
  const sides = useEndpointSides(id, data.endpointSides, set)
  const { inPos } = handlePositions(sides, set)
  const nodeType = data.nodeType || '__INNER__.map'
  const spec = specOf(useGraphNodeRegistry(), nodeType)
  const showError = nodeHasErrorOutput(nodeType, data.config, spec)
  const handles = set.outputs.filter((p) => !showError || p.id !== GRAPH_ENDPOINT_ERROR)
  useErrorHandleInternals(id, showError)
  const chunk = Number(data.config?.Chunk ?? 1) || 1
  const maxP = Number(data.config?.MaxParallel ?? 1) || 1
  const expanded = data.body === 'expanded'
  // 卡片摘要：list 逐项（或每 Chunk 项）运行循环体；并发数 1 为串行
  const mode = maxP > 1 ? `并发×${maxP}` : chunk > 1 ? `串行 · 批${chunk}` : '串行'
  const subtitle = expanded ? `List → Item · ${mode}` : `循环体 ${bodyGraphOf(data.config)?.Nodes.length ?? 0} 个节点 · ${mode}`
  const outputs = (
    <OutputHandles
      sides={sides}
      items={[
        ...handles.map((hnd) => ({ id: hnd.id, label: hnd.label, tone: handles.length > 1 ? ('ok' as const) : ('plain' as const) })),
        ...(showError ? [ERROR_OUTPUT] : []),
      ]}
    />
  )
  const header = (
    <div className="flex items-center gap-2.5">
      <NodeIcon icon={Repeat} className="text-fuchsia-600 dark:text-fuchsia-400" badgeCls="bg-fuchsia-500/12 ring-1 ring-fuchsia-500/25" />
      <div className="min-w-0 flex-1">
        <div className="truncate text-sm font-semibold">{data.label || '遍历'}</div>
        <div className="truncate text-[11px] text-muted-foreground">{subtitle}</div>
      </div>
      <BodyToggleButton id={id} expanded={expanded} />
    </div>
  )

  if (expanded) {
    return (
      <NodeShell
        id={id}
        nodeType={nodeType}
        className={cn(
          'relative rounded-2xl border-2 border-fuchsia-500/30 bg-fuchsia-500/[0.03] dark:border-fuchsia-400/30 dark:bg-fuchsia-400/[0.05]',
          runStatusPulseCls(data.runStatus),
          selected && 'ring-2 ring-[#8b5cf6]',
        )}
        style={{ width: '100%', height: '100%', ...runStatusBorderStyle(data.runStatus) }}
      >
        <NodeDebugMarks breakpoint={data.breakpoint} inject={data.inject} inPos={inPos} />
        <Handle type="target" id={GRAPH_DEFAULT_INPUT} position={inPos} className={handleTargetCls} />
        <div className="border-b border-fuchsia-500/20 px-3 py-1.5 dark:border-fuchsia-400/20" style={{ height: BODY_HEADER - 8 }}>
          {header}
        </div>
        {outputs}
      </NodeShell>
    )
  }

  return (
    <NodeShell
      id={id}
      nodeType={nodeType}
      className={cn(
        'relative rounded-xl px-3.5 py-2.5',
        nodeShell,
        runStatusPulseCls(data.runStatus),
        selected && nodeShellSelected,
      )}
      style={{ width: CARD_W, height: branchCardHeight(handles.length), ...runStatusBorderStyle(data.runStatus) }}
      title={selected ? `${sidesHint(sides)} · R 旋转端点` : undefined}
    >
      <NodeDebugMarks breakpoint={data.breakpoint} inject={data.inject} inPos={inPos} />
      <Handle type="target" id={GRAPH_DEFAULT_INPUT} position={inPos} className={handleTargetCls} />
      <div className="flex h-full items-center">{header}</div>
      {outputs}
    </NodeShell>
  )
}

/**
 * 节点卡片 props 相等判定：忽略拖拽途中每帧变化的 positionAbsoluteX / positionAbsoluteY / dragging，
 * 使被拖节点仅由 React Flow 外层容器通过 CSS transform 平移，跳过内部卡片虚拟 DOM 重渲染。
 */
function areNodePropsEqual<T extends Node>(prev: NodeProps<T>, next: NodeProps<T>): boolean {
  return (
    prev.id === next.id &&
    prev.selected === next.selected &&
    prev.data === next.data &&
    prev.width === next.width &&
    prev.height === next.height
  )
}

export const graphNodeTypes = {
  start: memo(StartNodeView, areNodePropsEqual),
  end: memo(EndNodeView, areNodePropsEqual),
  step: memo(StepNodeView, areNodePropsEqual),
  switch: memo(SwitchNodeView, areNodePropsEqual),
  map: memo(MapNodeView, areNodePropsEqual),
  merge: memo(MergeNodeView, areNodePropsEqual),
  parallel: memo(ParallelNodeView, areNodePropsEqual),
  annotation: memo(AnnotationBoxView, areNodePropsEqual),
}
