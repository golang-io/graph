import { useCallback, useEffect, useState } from 'react'
import { Box, Zap } from 'lucide-react'
import { cn } from './cn'

/** 画布渲染器：React Flow 或 AntV X6。与图执行引擎（immediate / durable）不是同一概念。 */
export type CanvasRenderer = 'xyflow' | 'x6'

const STORAGE_KEY = 'graph-ui.renderer'
const DEFAULT_RENDERER: CanvasRenderer = 'xyflow'

function parseRenderer(value: string | null): CanvasRenderer | null {
  if (value === 'x6' || value === 'xyflow') return value
  return null
}

function getStoredCanvasRenderer(): CanvasRenderer {
  if (typeof window === 'undefined') return DEFAULT_RENDERER
  try {
    const params = new URLSearchParams(window.location.search)
    const fromQuery = parseRenderer(params.get('renderer'))
    if (fromQuery) return fromQuery

    const stored = parseRenderer(localStorage.getItem(STORAGE_KEY))
    if (stored) return stored
  } catch {
    // ignore storage errors
  }
  return DEFAULT_RENDERER
}

function setStoredCanvasRenderer(renderer: CanvasRenderer): void {
  if (typeof window === 'undefined') return
  try {
    localStorage.setItem(STORAGE_KEY, renderer)
  } catch {
    // ignore
  }
}

export function useCanvasRenderer(
  propRenderer?: CanvasRenderer,
): [CanvasRenderer, (next: CanvasRenderer) => void] {
  const [renderer, setRendererState] = useState<CanvasRenderer>(
    () => propRenderer ?? getStoredCanvasRenderer(),
  )

  useEffect(() => {
    if (propRenderer && propRenderer !== renderer) {
      setRendererState(propRenderer)
    }
  }, [propRenderer, renderer])

  const setRenderer = useCallback((next: CanvasRenderer) => {
    setRendererState(next)
    setStoredCanvasRenderer(next)
  }, [])

  return [renderer, setRenderer]
}

/**
 * 画布渲染器切换徽章：悬浮在画布右上角，在 React Flow 与 AntV X6 之间切换。
 */
export function CanvasRendererBadge({
  renderer,
  onToggle,
  className,
}: {
  renderer: CanvasRenderer
  onToggle?: () => void
  className?: string
}) {
  const isX6 = renderer === 'x6'

  return (
    <button
      type="button"
      onClick={onToggle}
      title={isX6 ? '当前: AntV X6 渲染器 (点击切换为 React Flow)' : '当前: React Flow 渲染器 (点击切换为 AntV X6)'}
      className={cn(
        'group flex items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-medium shadow-sm backdrop-blur-md transition-all duration-200 select-none cursor-pointer',
        'border border-border/80 bg-card/90 hover:bg-card hover:border-foreground/20 text-foreground',
        className,
      )}
    >
      {isX6 ? (
        <>
          <Box className="h-3.5 w-3.5 text-blue-500 transition-transform group-hover:rotate-12" />
          <span className="font-mono text-[11px] text-muted-foreground">渲染器:</span>
          <span className="font-semibold text-blue-600 dark:text-blue-400">AntV X6</span>
        </>
      ) : (
        <>
          <Zap className="h-3.5 w-3.5 text-violet-500 transition-transform group-hover:scale-110" />
          <span className="font-mono text-[11px] text-muted-foreground">渲染器:</span>
          <span className="font-semibold text-violet-600 dark:text-violet-400">React Flow</span>
        </>
      )}
      <span className="ml-0.5 rounded bg-muted px-1 py-0.2 text-[9px] text-muted-foreground group-hover:text-foreground">
        切换
      </span>
    </button>
  )
}
