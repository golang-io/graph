/**
 * GraphNodeContextMenu — 画布节点右键菜单浮层。
 * 由业务页经 GraphWorkbench.canvasOverlay 挂载；坐标为相对画布容器的像素位置。
 * Canvas node context menu overlay; position is relative to the canvas container.
 */
import { useEffect, useRef, type ReactNode } from 'react'
import { cn } from './cn'

export type GraphNodeContextMenuItem = {
  /** 菜单项文案 / Menu label */
  label: string
  /** 点击回调；调用后菜单会关闭 / Click handler; menu closes after invoke */
  onClick: () => void
  /** 危险操作（删除等）用破坏性样式 / Destructive styling for delete etc. */
  danger?: boolean
  /** 禁用态 / Disabled */
  disabled?: boolean
  /** 右侧快捷键提示 / Shortcut hint on the right */
  shortcut?: string
}

export type GraphNodeContextMenuProps = {
  /** 相对画布容器左上角的 X（px） */
  x: number
  /** 相对画布容器左上角的 Y（px） */
  y: number
  items: GraphNodeContextMenuItem[]
  /** 关闭菜单（点空白、Escape、点菜单项后） */
  onClose: () => void
  /** 可选标题（如节点 ID） */
  title?: ReactNode
  className?: string
}

export function GraphNodeContextMenu({
  x,
  y,
  items,
  onClose,
  title,
  className,
}: GraphNodeContextMenuProps) {
  const rootRef = useRef<HTMLDivElement>(null)

  // Escape / 点击菜单外关闭
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose()
    }
    const onPointer = (e: MouseEvent) => {
      const el = rootRef.current
      if (el && !el.contains(e.target as Node)) onClose()
    }
    window.addEventListener('keydown', onKey)
    // 下一帧再监听，避免本次右键 mouseup 立刻关掉菜单
    const timer = window.setTimeout(() => {
      window.addEventListener('mousedown', onPointer)
    }, 0)
    return () => {
      window.clearTimeout(timer)
      window.removeEventListener('keydown', onKey)
      window.removeEventListener('mousedown', onPointer)
    }
  }, [onClose])

  return (
    <div
      ref={rootRef}
      role="menu"
      className={cn(
        'absolute z-50 min-w-[160px] overflow-hidden rounded-ui border border-border bg-card py-1 text-xs shadow-lg',
        className,
      )}
      style={{ left: x, top: y }}
      onContextMenu={(e) => e.preventDefault()}
    >
      {title ? (
        <div className="truncate border-b border-border/60 px-3 py-1.5 text-[11px] text-muted-foreground">
          {title}
        </div>
      ) : null}
      {items.map((item) => (
        <button
          key={item.label}
          type="button"
          role="menuitem"
          disabled={item.disabled}
          onClick={() => {
            if (item.disabled) return
            item.onClick()
            onClose()
          }}
          className={cn(
            'flex w-full items-center justify-between gap-4 px-3 py-1.5 text-left transition-colors cursor-pointer',
            item.danger
              ? 'text-destructive hover:bg-destructive/10'
              : 'text-foreground hover:bg-muted/70',
            item.disabled && 'cursor-not-allowed opacity-40 hover:bg-transparent',
          )}
        >
          <span>{item.label}</span>
          {item.shortcut ? (
            <span className="shrink-0 font-mono text-[10px] text-muted-foreground">{item.shortcut}</span>
          ) : null}
        </button>
      ))}
    </div>
  )
}
