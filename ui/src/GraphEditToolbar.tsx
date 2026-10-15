/**
 * GraphEditToolbar — graph-react 通用图形工具：撤销 / 重做。
 * 句柄朝向只在节点上单独修改，工具栏不提供整图方向。
 * 断点、执行等平台语义通过 Workbench toolbar slot 由产品注入。
 */
import { Redo2, Undo2 } from 'lucide-react'
import { Button } from './host-ui'
import { useGraphI18n } from './i18n'

export type GraphEditToolbarProps = {
  canUndo: boolean
  canRedo: boolean
  onUndo: () => void
  onRedo: () => void
}

export function GraphEditToolbar({
  canUndo,
  canRedo,
  onUndo,
  onRedo,
}: GraphEditToolbarProps) {
  const { t } = useGraphI18n()

  return (
    <>
      <div className="flex shrink-0 items-center gap-1.5">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={!canUndo}
          title={t('graph.undo')}
          aria-label={t('graph.undo')}
          onClick={onUndo}
        >
          <Undo2 className="h-4 w-4" />
        </Button>
        <Button
          type="button"
          size="sm"
          variant="ghost"
          disabled={!canRedo}
          title={t('graph.redo')}
          aria-label={t('graph.redo')}
          onClick={onRedo}
        >
          <Redo2 className="h-4 w-4" />
        </Button>
      </div>
    </>
  )
}
