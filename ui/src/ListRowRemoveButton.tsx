/**
 * 属性面板列表行删除：统一 ghost + Lucide X，避免「× / 删」混用。
 */
import { X } from 'lucide-react'
import { Button } from './host-ui'
import { cn } from './cn'

export function ListRowRemoveButton({
  onClick,
  className,
  label = '删除',
}: {
  onClick: () => void
  className?: string
  label?: string
}) {
  return (
    <Button
      type="button"
      size="sm"
      variant="ghost"
      className={cn(
        'h-8 w-8 shrink-0 px-0 text-muted-foreground hover:text-destructive',
        className,
      )}
      aria-label={label}
      title={label}
      onClick={onClick}
    >
      <X className="h-4 w-4" strokeWidth={2} />
    </Button>
  )
}
