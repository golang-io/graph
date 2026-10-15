/**
 * GraphWorkbench — 共享工作台布局：顶栏 + 可选节点库 + 画布 + 右侧属性浮层。
 *
 * 创作中心 / 短剧流水线 / 股票策略流统一走此壳，保证 chrome 与画布视觉一致。
 * 节点库：点击「节点库」按钮展开浮层列表，提供显式关闭按钮。
 */
import { PanelLeft, X } from 'lucide-react'
import { useEffect, useState, type ReactNode, type Ref } from 'react'
import { Button } from './host-ui'
import { ScrollArea } from './host-ui'
import { cn } from './cn'
import { useGraphI18n } from './i18n'
import type { NodeTypeRegistry } from './node-registry'
import {
  GraphCanvas,
  type GraphCanvasProps,
} from './GraphCanvas'
import { GraphNodeRegistryProvider } from './GraphNodeRegistryContext'

export type GraphWorkbenchProps = {
  /** 顶栏：保存、批跑、剧集选择等（产品自绘） */
  toolbar?: ReactNode
  /** 工具栏上方区域（创作中心：试跑状态条 / 调试输入） */
  banner?: ReactNode
  /** 节点库列表内容；点击「节点库」按钮时以浮层展示 */
  palette?: ReactNode
  /** 节点库按钮文案 */
  paletteLabel?: string
  /** 画布右上角属性浮层 */
  propsPanel?: ReactNode
  /** 画布同层覆盖（右键菜单等，不进 RF children） */
  canvasOverlay?: ReactNode
  /** 加载/空态：盖在画布上（短剧拉 pipeline 时） */
  emptyState?: ReactNode
  /** 画布容器 ref（右键坐标等） */
  canvasContainerRef?: Ref<HTMLDivElement>
  /** 传给 CanvasShell */
  canvas: GraphCanvasProps
  registry?: NodeTypeRegistry | null
  /** 外层高度类，默认 flex-1 撑满；短剧可用 h-[560px] */
  className?: string
  /** 强制 remount 画布（如切换剧集） */
  canvasKey?: string
}

function WorkbenchInner({
  toolbar,
  banner,
  palette,
  paletteLabel,
  propsPanel,
  canvasOverlay,
  emptyState,
  canvasContainerRef,
  canvas,
  className,
  canvasKey,
}: Omit<GraphWorkbenchProps, 'registry'>) {
  const { t } = useGraphI18n()
  const label = paletteLabel || 'Nodes'
  const showPalette = Boolean(palette)
  const [paletteOpen, setPaletteOpen] = useState(false)

  useEffect(() => {
    if (!paletteOpen) return
    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setPaletteOpen(false)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [paletteOpen])

  const { children: canvasChildren, ...canvasRest } = canvas

  return (
    <div className={cn('flex min-h-0 flex-1 flex-col', className)}>
      {banner}
      {toolbar ? (
        <div className="relative z-20 flex flex-wrap items-center gap-2 border-b border-border px-3 py-2">
          {toolbar}
        </div>
      ) : null}
      <div className="relative flex min-h-0 flex-1">
        {showPalette ? (
          <div className="absolute left-3 top-3 z-30">
            <Button
              type="button"
              variant="outline"
              size="sm"
              className={cn(
                'h-8 gap-1 bg-background/95 shadow-sm backdrop-blur-sm',
                paletteOpen && 'border-primary text-primary bg-primary/5',
              )}
              aria-expanded={paletteOpen}
              aria-haspopup="menu"
              aria-controls="graph-workbench-palette"
              title={label}
              onClick={() => setPaletteOpen((v) => !v)}
            >
              <PanelLeft className="h-4 w-4" />
              <span className="text-xs">{label}</span>
            </Button>
            {paletteOpen ? (
              <aside
                id="graph-workbench-palette"
                role="menu"
                className="absolute left-0 top-full mt-1 flex max-h-[min(28rem,calc(100vh-8rem))] w-72 flex-col overflow-hidden rounded-ui border border-border/70 bg-card text-xs shadow-lg"
                onMouseDown={(e) => e.stopPropagation()}
                onWheel={(e) => e.stopPropagation()}
              >
                <div className="flex shrink-0 items-center justify-between border-b border-border/55 px-3 py-2">
                  <span className="font-semibold text-xs text-foreground">{label}</span>
                  <button
                    type="button"
                    onClick={() => setPaletteOpen(false)}
                    className="flex items-center gap-1 rounded px-1.5 py-0.5 text-xs text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
                    title={t('graph.palette.close')}
                    aria-label={t('graph.palette.close')}
                  >
                    <X className="h-3.5 w-3.5" />
                    <span className="text-[11px]">{t('graph.palette.close')}</span>
                  </button>
                </div>
                {palette}
              </aside>
            ) : null}
          </div>
        ) : null}
        <div ref={canvasContainerRef} className="relative min-h-0 min-w-0 flex-1">
          <GraphCanvas key={canvasKey} {...canvasRest}>
            {canvasChildren}
          </GraphCanvas>
          {emptyState ? (
            <div className="absolute inset-0 z-10 flex items-center justify-center bg-background/80 text-sm text-muted-foreground backdrop-blur-[1px]">
              {emptyState}
            </div>
          ) : null}
          {propsPanel ? (
            /* 整列高度：属性面板与画布等高，内容超出由面板自身内部滚动 */
            <div className="absolute right-3 top-3 z-40 flex h-[calc(100%-1.5rem)] w-[min(420px,calc(100%-1.5rem))] min-w-0 flex-col overflow-hidden">
              {/* ScrollArea 根节点会写 inline position:relative，不能把 absolute 直接打在它身上，否则浮层掉到画布下方被 overflow 裁掉 */}
              <ScrollArea
                className="min-h-0 w-full flex-1"
                viewportProps={{
                  onMouseDown: (e) => e.stopPropagation(),
                  onWheel: (e) => e.stopPropagation(),
                }}
              >
                {propsPanel}
              </ScrollArea>
            </div>
          ) : null}
          {canvasOverlay}
        </div>
      </div>
    </div>
  )
}

export function GraphWorkbench({
  registry,
  ...rest
}: GraphWorkbenchProps) {
  const body = (
    <GraphNodeRegistryProvider registry={registry}>
      <WorkbenchInner {...rest} />
    </GraphNodeRegistryProvider>
  )
  return body
}
