import {
  Activity,
  ArrowDownCircle,
  ArrowUpCircle,
  Braces,
  Database,
  CircleCheck,
  CircleX,
  Clock,
  Code2,
  GitBranch,
  GitMerge,
  Globe,
  Network,
  Play,
  Repeat,
  ShieldAlert,
  Sparkles,
  Split,
  StickyNote,
  Target,
  type LucideIcon,
} from 'lucide-react'
import { BODY_INPUT } from './body-scope'
import {
  createGraphNodePack,
  type GraphNodeDefinition,
  type GraphNodePack,
} from './node-pack'
import type { GraphNodeRole } from './node-registry'
import type { GraphInputRef, GraphJSONSchema } from './types'
import { literalsFromInputRefs } from './inputs-split'

export type { GraphJSONSchema }

/** 面板行视图（由 InputSchema.properties 展开，非后端独立类型）。 */
export type GraphInputField = {
  Name: string
  Required?: boolean
  Label?: string
  Description?: string
  Options?: string[]
}

/** 前端根据字段名与 Options 推测控件（不读后端 Widget）。 */
export type InferredWidget = 'select' | 'number' | 'textarea' | 'json' | 'text'

const NUMBER_NAMES = new Set([
  'Chunk', 'MaxParallel', 'Inputs', 'Outputs', 'TimeoutSec', 'Temperature', 'MaxTokens',
])

const TEXTAREA_NAMES = new Set([
  'Prompt', 'URL', 'Message', 'Body', 'Expr', 'Init', 'GraphID', 'System',
])

const JSON_NAMES = new Set(['Outputs', 'Cases', 'Headers'])

export function inferInputWidget(field: GraphInputField): InferredWidget {
  if (field.Options?.length) return 'select'
  // Outputs 既可能是并行出边数，也可能是 template/code 的键值表：有 Label「出边数」走 number，否则 JSON。
  if (field.Name === 'Outputs' && field.Label?.includes('出边')) return 'number'
  if (JSON_NAMES.has(field.Name)) return 'json'
  if (NUMBER_NAMES.has(field.Name)) return 'number'
  if (TEXTAREA_NAMES.has(field.Name)) return 'textarea'
  return 'text'
}

function enumStrings(prop?: GraphJSONSchema): string[] | undefined {
  const opts = prop?.enum?.filter((v): v is string => typeof v === 'string')
  return opts?.length ? opts : undefined
}

function propertyToField(
  name: string,
  prop: GraphJSONSchema,
  required: ReadonlySet<string>,
): GraphInputField {
  return {
    Name: name,
    Required: required.has(name),
    Label: prop.title,
    Description: prop.description,
    Options: enumStrings(prop),
  }
}

/** 按 JSON 对象键顺序展开 properties（后端 PropertyOrder 保证稳定）。 */
function schemaProperties(schema?: GraphJSONSchema): Array<[string, GraphJSONSchema]> {
  if (!schema?.properties) return []
  return Object.entries(schema.properties)
}

/** GET /node-specs 返回的节点目录项（graph.NodeSpec），键名与 Go 字段一致。 */
export type GraphNodeSpec = {
  ID: string
  Name?: string
  /** 种类级说明（节点库副标题等）；与图 Definition.Description 无关 */
  Description?: string
  /** 节点库分组键；缺省 general */
  Group?: string
  /** 分组显示名；缺省用 Group */
  GroupLabel?: string
  /** 新建节点默认 Inputs（字面量为主） */
  DefaultInputs?: Record<string, GraphInputRef>
  /** 画布图标键（lucide 映射） */
  Icon?: string
  /** 画布色调（如 cyan / violet） */
  Tone?: string
  /** 几何角色扩展点名；未知或缺省回落通用节点 */
  Renderer?: string
  /** 种类级固定端点（与后端 NodeSpec.Endpoints 一致）。 */
  Endpoints: {
    Inputs?: string[]
    Outputs?: string[]
  }
  Presets?: Array<{
    ID: string
    Label: string
    Inputs?: Record<string, GraphInputRef>
  }>
  /**
   * 参数与数据槽的 JSON Schema；写入 Node.Inputs。
   * properties 有 title 的进属性表单，无 title 的作绑定建议槽；required 必填；additionalProperties 允许自定义名。
   */
  InputSchema?: GraphJSONSchema
  /**
   * 各控制流出口的载荷 JSON Schema（标准关键字）。
   * 键为出口 ID；空串表示所有正常出口（不含 error）共享的载荷。
   * 省略时输出由参数决定（template / code 的 Outputs[].Key、merge 的 Outputs[].Name），见 node-outputs.ts。
   */
  OutputsByEndpoint?: Record<string, GraphJSONSchema>
}

const icons: Record<string, LucideIcon> = {
  play: Play,
  braces: Braces,
  'git-branch': GitBranch,
  'git-merge': GitMerge,
  split: Split,
  repeat: Repeat,
  'circle-x': CircleX,
  clock: Clock,
  network: Network,
  code: Code2,
  globe: Globe,
  sparkles: Sparkles,
  'circle-check': CircleCheck,
  database: Database,
  activity: Activity,
  'arrow-up-circle': ArrowUpCircle,
  'arrow-down-circle': ArrowDownCircle,
  'shield-alert': ShieldAlert,
  target: Target,
  'sticky-note': StickyNote,
}

function rendererRole(spec: GraphNodeSpec): GraphNodeRole {
  switch (spec.Renderer) {
    case 'switch':
      return 'switch'
    case 'merge':
      return 'merge'
    case 'parallel':
      return 'parallel'
    case 'map':
      return 'map'
    case 'start':
      return 'start'
    case 'end':
      return 'end'
    case 'annotation':
      return 'annotation'
    case 'stop_error':
      return 'end'
    default:
      return 'step'
  }
}

function toneClass(tone?: string): string | undefined {
  switch (tone) {
    case 'green':
      return 'text-emerald-600 dark:text-emerald-400'
    case 'red':
      return 'text-rose-600 dark:text-rose-400'
    case 'amber':
      return 'text-amber-600 dark:text-amber-400'
    case 'cyan':
      return 'text-cyan-600 dark:text-cyan-300'
    case 'purple':
    case 'violet':
      return 'text-violet-600 dark:text-violet-400'
    default:
      return undefined
  }
}

/** 有 title 的属性走属性表单；无 title 的（如必填 list/query）走输入绑定区建议槽。 */
export function formFieldsFromSpec(spec?: GraphNodeSpec): GraphInputField[] {
  const schema = spec?.InputSchema
  const required = new Set(schema?.required ?? [])
  return schemaProperties(schema)
    // 内嵌子图（map 的循环体 Body）在画布容器里编辑，不进属性表单
    .filter(([name, prop]) => Boolean(prop.title) && name !== BODY_INPUT)
    .map(([name, prop]) => propertyToField(name, prop, required))
}

/** 输入绑定建议槽（InputSchema 无 title 的属性）；仅 UI，协议为 InputSchema。 */
export type BindingSlot = { name: string; required?: boolean }

export function bindingSlotsFromSpec(spec?: GraphNodeSpec): BindingSlot[] {
  const schema = spec?.InputSchema
  const required = new Set(schema?.required ?? [])
  return schemaProperties(schema)
    .filter(([, prop]) => !prop.title)
    .map(([name]) => ({
      name,
      required: required.has(name) || undefined,
    }))
}

/** 后端 NodeSpec → graph-react NodePack；业务能力和默认值不在前端重复声明。 */
export function createGraphNodePackFromCatalog(
  id: string,
  specs: readonly GraphNodeSpec[],
): GraphNodePack {
  const definitions: GraphNodeDefinition[] = specs.flatMap((spec) => {
    const groupId = spec.Group || 'general'
    const common = {
      nodeType: spec.ID,
      role: rendererRole(spec),
      icon: spec.Icon ? icons[spec.Icon] : undefined,
      iconClass: toneClass(spec.Tone),
      subtitle: spec.Description || spec.Name || spec.ID,
      hint: () => spec.Description || spec.Name || spec.ID,
      groupKey: groupId,
      groupLabel: spec.GroupLabel || groupId,
      spec,
    } satisfies Omit<GraphNodeDefinition, 'type' | 'label' | 'defaultConfig'>
    return [
      {
        ...common,
        type: spec.ID,
        label: spec.Name || spec.ID,
        defaultConfig: () => structuredClone(literalsFromInputRefs(spec.DefaultInputs)),
      },
      ...(spec.Presets ?? []).map((preset) => ({
        ...common,
        type: JSON.stringify([spec.ID, preset.ID]),
        label: preset.Label,
        defaultConfig: () => structuredClone(literalsFromInputRefs(preset.Inputs)),
      })),
    ]
  })
  return createGraphNodePack(id, definitions)
}
