/**
 * GraphNodePropsPanel — 通用节点属性浮层，放入 GraphWorkbench propsPanel 插槽。
 *
 * 表单由 NodeSpec.InputSchema（properties 有 title）编辑字面量；绑定区编辑引用。
 * 落盘时两者合并为 Node.Inputs。无表单字段或复杂 widget 回退 JSON 编辑字面量。
 * 提供 inputSources 时显示「输入」区（见 NodeInputsFields）。
 */
import { cn } from './cn'
import { Button, Input, Select, Textarea } from './host-ui'
import {
  bindingSlotsFromSpec,
  formFieldsFromSpec,
  inferInputWidget,
  type GraphInputField,
  type GraphNodeSpec,
} from './node-spec'
import { catalogKind } from './node-registry'
import type { GraphInputSourceOption } from './node-outputs'
import { NodeInputsFields } from './NodeInputsFields'
import { SwitchConfigFields } from './SwitchConfigFields'
import type { GraphInputRef } from './types'
import {
  SIDES,
  SIDE_LABEL,
  endpointIds,
  endpointSide,
  setEndpointSide,
  type EndpointSides,
  type Side,
} from './orient'

/** 属性面板逐个列出的端点（resolveGraphNodeEndpoints 的结果即可） */
type PanelEndpoints = { inputs: { id: string; label: string }[]; outputs: { id: string; label: string }[] }

export type GraphNodePropsValue = {
  id: string
  nodeType: string
  label: string
  config: Record<string, unknown>
  /** 节点输入（Node.Inputs）；缺省视为没有输入 */
  inputs?: Record<string, GraphInputRef>
}

export type GraphNodePropsPanelProps = {
  node: GraphNodePropsValue
  /** 后端下发的节点规格；缺省时整个 config 走 JSON 编辑 */
  spec?: GraphNodeSpec
  onChange: (patch: Partial<Pick<GraphNodePropsValue, 'label' | 'config' | 'inputs'>>) => void
  /** 可引用的输入来源（inputSourcesFor 的结果）；缺省时不显示「输入」区 */
  inputSources?: GraphInputSourceOption[]
  /** 节点各端点当前所在边（须已按 endpoints 经 resolveEndpointSides 补齐）；与 endpoints、onEndpointSidesChange 同时提供时显示「端点方位」 */
  endpointSides?: EndpointSides
  /** 当前配置下的端点（resolveGraphNodeEndpoints(...)），逐个设置所在边 */
  endpoints?: PanelEndpoints
  onEndpointSidesChange?: (sides: EndpointSides) => void
  onDelete?: () => void
  onClose?: () => void
  className?: string
}

export function GraphNodePropsPanel({
  node,
  spec,
  onChange,
  endpointSides,
  endpoints,
  onEndpointSidesChange,
  inputSources,
  onDelete,
  onClose,
  className,
}: GraphNodePropsPanelProps) {
  const configFields = formFieldsFromSpec(spec)
  const isSwitch = spec?.Renderer === 'switch' || catalogKind(node.nodeType) === 'switch'
  const inputs = node.inputs ?? {}
  // 提供 inputSources 即显示「输入」区（统一允许自定义输入名；无 title 的 Schema 属性作建议槽）。
  const showInputs = inputSources !== undefined
  const setConfig = (name: string, value: unknown) =>
    onChange({ config: { ...node.config, [name]: value } })
  const raw = (v: unknown) => (v == null ? '' : typeof v === 'string' ? v : JSON.stringify(v, null, 2))

  return (
    // h-full：与画布等高；内容区 flex-1 + overflow-auto，超出时面板内部滚动
    <div className={cn('flex h-full min-h-0 flex-col rounded-ui border border-border bg-card text-xs shadow-lg', className)}>
      <div className="flex shrink-0 items-center justify-between border-b border-border px-3 py-2">
        <span className="text-sm font-semibold">{spec?.Name || node.nodeType}</span>
        {onClose ? (
          <button type="button" className="text-muted-foreground hover:text-foreground" onClick={onClose}>
            关闭
          </button>
        ) : null}
      </div>

      <div className="flex min-h-0 flex-1 flex-col gap-3 overflow-auto p-3">
      <label className="flex flex-col gap-1">
        <span className="text-muted-foreground">节点 ID</span>
        <span className="font-mono">{node.id}</span>
      </label>

      <label className="flex flex-col gap-1">
        <span className="text-muted-foreground">名称</span>
        <Input value={node.label} onChange={(e) => onChange({ label: e.target.value })} className="h-8 text-sm" />
      </label>

      {showInputs ? (
        <NodeInputsFields
          inputs={inputs}
          sources={inputSources ?? []}
          slots={bindingSlotsFromSpec(spec)}
          onReplace={(next) => onChange({ inputs: next })}
        />
      ) : null}

      {/* 条件分支用专用表单编辑 IF / ELIF / ELSE（Cases），不走 InputSchema 通用字段 */}
      {isSwitch ? (
        <SwitchConfigFields config={node.config} onReplace={(config) => onChange({ config })} />
      ) : configFields.length > 0 ? (
        configFields.map((f) => (
          <FieldRow
            key={f.Name}
            field={f}
            value={node.config[f.Name]}
            onCommit={(v) => setConfig(f.Name, v)}
          />
        ))
      ) : (
        <label className="flex flex-col gap-1">
          <span className="text-muted-foreground">参数（JSON）</span>
          <Textarea
            key={node.id}
            defaultValue={raw(node.config)}
            className="min-h-40 font-mono text-xs"
            onBlur={(e) => {
              try {
                onChange({ config: JSON.parse(e.target.value || '{}') })
              } catch {
                /* 非法 JSON 忽略本次修改 */
              }
            }}
          />
        </label>
      )}

      {endpointSides && endpoints && onEndpointSidesChange ? (
        <EndpointSidesSection sides={endpointSides} endpoints={endpoints} onChange={onEndpointSidesChange} />
      ) : null}

      </div>

      {onDelete ? (
        <div className="shrink-0 border-t border-border p-3">
          <Button type="button" variant="outline" className="w-full text-destructive" onClick={onDelete}>
            删除节点
          </Button>
        </div>
      ) : null}
    </div>
  )
}

/**
 * 端点方位：每个端点（入口、各个出口）各选一条边。
 * 入口改到出口所在的边时，这些出口移到入口原来的边（对调）；出口不能选入口所在的边，选项里不列出。
 */
function EndpointSidesSection({
  sides,
  endpoints,
  onChange,
}: {
  sides: EndpointSides
  endpoints: PanelEndpoints
  onChange: (sides: EndpointSides) => void
}) {
  const eps = endpointIds(endpoints)
  const inSides = new Set(endpoints.inputs.map((e) => endpointSide(sides, e.id, 'in')))
  const row = (ep: { id: string; label: string }, dir: 'in' | 'out') => {
    const value = endpointSide(sides, ep.id, dir)
    const options = dir === 'in'
      ? SIDES.map((s) => ({
          value: s,
          label: s !== value && endpoints.outputs.some((o) => endpointSide(sides, o.id, 'out') === s) ? `${SIDE_LABEL[s]}（与出口对调）` : SIDE_LABEL[s],
        }))
      : SIDES.filter((s) => !inSides.has(s)).map((s) => ({ value: s, label: SIDE_LABEL[s] }))
    return (
      <div key={ep.id} className="flex items-center gap-2">
        <span className="w-16 shrink-0 truncate font-mono text-muted-foreground" title={ep.id}>{ep.label}</span>
        <Select value={value} onChange={(v) => onChange(setEndpointSide(sides, eps, ep.id, v as Side))} options={options} className="w-full" />
      </div>
    )
  }
  return (
    <div className="flex flex-col gap-2 border-t border-border pt-3">
      <span className="text-sm font-semibold">端点方位</span>
      {endpoints.inputs.map((ep) => row(ep, 'in'))}
      {endpoints.outputs.map((ep) => row(ep, 'out'))}
    </div>
  )
}

function FieldRow({
  field,
  value,
  onCommit,
}: {
  field: GraphInputField
  value: unknown
  onCommit: (v: unknown) => void
}) {
  const raw = (v: unknown) => (v == null ? '' : typeof v === 'string' ? v : JSON.stringify(v, null, 2))
  const widget = inferInputWidget(field)
  const label = (
    <span className="text-muted-foreground">
      {field.Label || field.Name}
      {field.Required ? <span className="text-destructive"> *</span> : null}
    </span>
  )

  if (widget === 'select' && field.Options?.length) {
    return (
      <div className="flex flex-col gap-1">
        <span className="text-xs text-muted-foreground">{label}</span>
        <Select
          value={typeof value === 'string' ? value : (field.Options[0] ?? '')}
          onChange={(v) => onCommit(v)}
          options={field.Options.map((o) => ({ value: o, label: o }))}
          className="w-full"
        />
      </div>
    )
  }

  if (widget === 'number') {
    return (
      <label className="flex flex-col gap-1">
        {label}
        <Input
          type="number"
          value={typeof value === 'number' ? String(value) : ''}
          onChange={(e) => onCommit(e.target.value === '' ? undefined : Number(e.target.value))}
          className="h-8 text-sm"
        />
      </label>
    )
  }

  if (widget === 'json') {
    return (
      <label className="flex flex-col gap-1">
        {label}
        <Textarea
          key={field.Name}
          defaultValue={raw(value)}
          className="min-h-28 font-mono text-xs"
          onBlur={(e) => {
            try {
              onCommit(JSON.parse(e.target.value))
            } catch {
              /* 非法 JSON 忽略本次修改 */
            }
          }}
        />
      </label>
    )
  }

  if (widget === 'textarea') {
    return (
      <label className="flex flex-col gap-1">
        {label}
        <Textarea
          value={typeof value === 'string' ? value : raw(value)}
          onChange={(e) => onCommit(e.target.value)}
          className="min-h-20 text-sm"
        />
      </label>
    )
  }

  return (
    <label className="flex flex-col gap-1">
      {label}
      <Input
        value={raw(value)}
        onChange={(e) => onCommit(e.target.value)}
        className="h-8 text-sm"
      />
    </label>
  )
}
