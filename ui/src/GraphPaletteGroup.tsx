/**
 * GraphPaletteGroup — 节点库分组：标题可点击收拢/展开，默认收拢。
 * 视觉对齐 ModelTreeSelect：左侧箭头、标题、右侧数量胶囊。
 */
import { ChevronDown, ChevronRight } from 'lucide-react'
import { useState, type ReactNode } from 'react'
import { cn } from './cn'

export type GraphPaletteGroupProps = {
  title: string
  /** 右侧数量；与模型树 Endpoint 分组 count 同位置 */
  count?: number
  children: ReactNode
  /** 受控展开；不传则用内部状态 */
  open?: boolean
  onOpenChange?: (open: boolean) => void
  /** 非受控默认 false：收拢 */
  defaultOpen?: boolean
  className?: string
}

export function GraphPaletteGroup({
  title,
  count,
  children,
  open: openProp,
  onOpenChange,
  defaultOpen = false,
  className,
}: GraphPaletteGroupProps) {
  const [uncontrolledOpen, setUncontrolledOpen] = useState(defaultOpen)
  const controlled = openProp !== undefined
  const open = controlled ? openProp : uncontrolledOpen

  function toggle() {
    const next = !open
    if (!controlled) setUncontrolledOpen(next)
    onOpenChange?.(next)
  }

  return (
    <div className={cn('mb-0.5 min-w-0', className)}>
      <button
        type="button"
        className={cn(
          'flex w-full items-center gap-1.5 rounded-ui px-2 py-1.5 text-left text-xs font-medium text-foreground transition-colors',
          'hover:bg-muted/70',
        )}
        aria-expanded={open}
        onClick={toggle}
      >
        {open ? (
          <ChevronDown className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
        ) : (
          <ChevronRight className="h-3.5 w-3.5 shrink-0 text-muted-foreground" aria-hidden />
        )}
        <span className="min-w-0 flex-1 truncate">{title}</span>
        {count != null ? (
          <span className="shrink-0 rounded-full bg-muted/50 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground/70">
            {count}
          </span>
        ) : null}
      </button>
      {open ? <div className="mb-1 ml-2 space-y-0.5 border-l border-border/50 pl-1">{children}</div> : null}
    </div>
  )
}
