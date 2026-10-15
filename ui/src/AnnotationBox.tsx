/**
 * AnnotationBox — 画布纯展示虚线框（不参与执行、无句柄）。
 * 最底层 zIndex；顶部标题；说明按 Markdown 渲染；可拖拽缩放；可自定义描边色。
 */
import { useMemo } from 'react'
import { NodeResizer, type Node, type NodeProps } from '@xyflow/react'
import { cn } from './cn'
import {
  ANNOTATION_DEFAULT_SIZE,
  annotationColorRgba,
  normalizeAnnotationColor,
} from './annotation-style'
import './annotation-md.css'

function renderMarkdown(text: string): string {
  return text
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/\n/g, '<br/>')
}

export {
  ANNOTATION_COLOR_PRESETS,
  ANNOTATION_DEFAULT_COLOR,
  ANNOTATION_DEFAULT_SIZE,
  normalizeAnnotationColor,
  parseAnnotationColor,
} from './annotation-style'

type AnnotationData = {
  label?: string
  note?: string
  color?: string
  transparent?: boolean
}

export function AnnotationBoxView({
  data,
  selected,
  width,
  height,
}: NodeProps<Node<AnnotationData>>) {
  const w = width ?? ANNOTATION_DEFAULT_SIZE.w
  const h = height ?? ANNOTATION_DEFAULT_SIZE.h
  const title = data.label?.trim() || '分组'
  const note = (data.note ?? '').trim()
  const color = normalizeAnnotationColor(data.color)
  const transparent = data.transparent !== false
  const borderColor = selected ? color : annotationColorRgba(color, 0.72)
  const headerBorder = annotationColorRgba(color, 0.45)
  const fill = transparent ? annotationColorRgba(color, 0.06) : 'hsl(var(--card))'
  const noteHtml = useMemo(() => (note ? renderMarkdown(note) : ''), [note])

  return (
    <>
      <NodeResizer
        minWidth={140}
        minHeight={88}
        isVisible={selected}
        lineStyle={{ borderColor: annotationColorRgba(color, 0.55) }}
        handleStyle={{ borderColor: color, background: 'var(--background, #fff)' }}
        handleClassName="!h-2.5 !w-2.5 !rounded-sm"
      />
      <div
        className={cn(
          'box-border flex h-full w-full flex-col rounded-lg border-2',
          transparent ? 'border-dashed' : 'border-solid shadow-sm',
        )}
        style={{
          width: w,
          height: h,
          borderColor,
          backgroundColor: fill,
        }}
      >
        <div
          className={cn(
            'pointer-events-none shrink-0 border-b px-2.5 py-1.5',
            transparent ? 'border-dashed' : 'border-solid',
          )}
          style={{ borderColor: headerBorder }}
        >
          <div className="truncate text-xs font-semibold tracking-wide" style={{ color }}>
            {title}
          </div>
        </div>
        {noteHtml ? (
          <div
            className="ann-md nodrag nowheel min-h-0 flex-1 overflow-auto px-2.5 py-1.5"
            dangerouslySetInnerHTML={{ __html: noteHtml }}
          />
        ) : (
          <div className="min-h-0 flex-1" aria-hidden />
        )}
      </div>
    </>
  )
}
