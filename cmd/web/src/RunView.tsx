import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import {
  Activity,
  ArrowDownUp,
  Bug,
  Check,
  ChevronDown,
  ChevronUp,
  CircleDot,
  Clock,
  Copy,
  Eraser,
  FastForward,
  History,
  Pause,
  Play,
  Redo2,
  RefreshCw,
  RotateCcw,
  Save,
  Settings2,
  SlidersHorizontal,
  Square,
  StepForward,
  Trash2,
  Undo2,
  Zap,
} from 'lucide-react'
import {
  buildDebugOptions,
  buildRunStatusMaps,
  canSetBreakpointOnNode,
  edgesOnRemovedOutputs,
  endpointIds,
  graphDecorationNodePack,
  graphEditorCanvasConfig,
  GraphInputsFields,
  GraphNodePropsPanel,
  GraphPalette,
  GraphWorkbench,
  inputSourcesFor,
  insertionPosition,
  insertionScope,
  isDebugInjectActive,
  libraryDefaultConfig,
  loadStoredGraphHistory,
  overlayDebugWaitingRuns,
  overlayIsHung,
  parseGraphUI,
  recallDebugSession,
  rememberDebugSession,
  resolveEndpointSides,
  resolveGraphNodeEndpoints,
  rotateEndpointSides,
  runInputFieldsFromSchema,
  sameGraphSnapshot,
  scopeGraphOf,
  scopedId,
  Select,
  applyWatchEvent,
  useGraphDocument,
  useGraphHistory,
  withEndpointSides,
  type EndpointSides,
  type GraphDebugInject,
  type GraphDefinition,
  type GraphHistoryPushOptions,
  type GraphInputRef,
  type GraphJSONSchema,
  type GraphNodeJSON,
  type GraphNodePack,
  type GraphRun,
  type GraphTaskPolicy,
  type GraphTaskSnapshot,
  type GraphTaskSummary,
  type GraphViewState,
  type GraphWatchEvent,
  type RunStatus,
} from '@golang-io/graph-ui'
import { api } from './api'

/** Task 终态：收到终态快照后关闭订阅。 */
const TERMINAL = new Set(['SUCCEEDED', 'FAILED', 'CANCELED'])

type BottomTab = 'detail' | 'io' | 'list'

/** 输入框文本 → 运行输入值：能解析为 JSON 时取解析结果（数字、布尔、数组等；兼容中文全角括号与逗号），否则按字符串传入。 */
function parseInputValue(text: string): unknown {
  try {
    return JSON.parse(text)
  } catch {
    const normalized = text
      .trim()
      .replace(/【/g, '[')
      .replace(/】/g, ']')
      .replace(/｛/g, '{')
      .replace(/｝/g, '}')
      .replace(/，/g, ',')
      .replace(/：/g, ':')
      .replace(/[“”]/g, '"')
    if (
      (normalized.startsWith('[') && normalized.endsWith(']')) ||
      (normalized.startsWith('{') && normalized.endsWith('}'))
    ) {
      try {
        return JSON.parse(normalized)
      } catch {
        // 仍无法解析为 JSON 时保留原文本
      }
    }
    return text
  }
}

/** 运行输入值 → 输入框文本：字符串原样显示，其余序列化为紧凑 JSON。 */
function formatInputValue(val: unknown): string {
  if (val === undefined || val === null) return ''
  if (typeof val === 'string') return val
  try {
    return JSON.stringify(val)
  } catch {
    return String(val)
  }
}

/** 格式化值供面板预览。 */
function formatPreview(val: unknown): string {
  if (val === undefined) return 'undefined'
  if (typeof val === 'string') return val
  try {
    return JSON.stringify(val, null, 2)
  } catch {
    return String(val)
  }
}

/** 计算两个 ISO 时间戳之间的耗时文本。 */
function formatDuration(startedAt?: string, finishedAt?: string): string {
  if (!startedAt) return '-'
  const start = Date.parse(startedAt)
  if (Number.isNaN(start)) return '-'
  const end = finishedAt ? Date.parse(finishedAt) : Date.now()
  if (Number.isNaN(end) || end < start) return '-'
  const ms = end - start
  if (ms < 1000) return `${ms}ms`
  if (ms < 60_000) return `${(ms / 1000).toFixed(2)}s`
  return `${Math.floor(ms / 60_000)}m ${Math.round((ms % 60_000) / 1000)}s`
}

/** 格式化本地时间（HH:mm:ss 或 MM-DD HH:mm:ss）。 */
function formatTime(iso?: string): string {
  if (!iso) return '-'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

/** 去掉内置节点种类前缀，便于界面展示。 */
function shortKind(kind: string): string {
  return kind.replace(/^__INNER__\./, '')
}

type Props = {
  doc: GraphDefinition
  pack: GraphNodePack | null
  onSaved?: (doc: GraphDefinition) => void
  /** 第一行左侧注入的工作流选择器插槽 */
  workflowSelector?: ReactNode
}

/** Task / Run 状态徽标样式：沿用 GraphNodes 的深浅色徽标语义。 */
function statusBadgeClass(status: string): string {
  switch (status.toUpperCase()) {
    case 'SUCCEEDED':
      return 'bg-emerald-500/12 text-emerald-700 ring-1 ring-emerald-500/25 dark:bg-emerald-500/20 dark:text-emerald-300'
    case 'FAILED':
    case 'CANCELED':
      return 'bg-rose-500/12 text-rose-700 ring-1 ring-rose-500/25 dark:bg-rose-500/20 dark:text-rose-300'
    case 'RUNNING':
      return 'bg-indigo-500/12 text-indigo-700 ring-1 ring-indigo-500/25 dark:bg-indigo-500/20 dark:text-indigo-300'
    case 'WAITING':
      return 'bg-amber-500/12 text-amber-700 ring-1 ring-amber-500/25 dark:bg-amber-500/20 dark:text-amber-300'
    default:
      return 'bg-slate-500/12 text-slate-600 ring-1 ring-slate-500/25 dark:bg-slate-500/20 dark:text-slate-300'
  }
}

/** 画布节点状态（running/succeeded/failed）转大写展示标签。 */
function nodeStatusLabel(status?: RunStatus): string {
  if (status === 'succeeded') return 'SUCCEEDED'
  if (status === 'failed') return 'FAILED'
  if (status === 'running') return 'RUNNING'
  return 'PENDING'
}

export default function RunView({ doc, pack, onSaved, workflowSelector }: Props) {
  const activePack = useMemo(() => pack ?? graphDecorationNodePack, [pack])
  // 若浏览器 localStorage 中已有当前图的编辑历史栈，优先恢复最新编辑态快照，使页面刷新或切图不丢进度
  const initialEditorDoc = useMemo(() => {
    const stored = loadStoredGraphHistory(doc.GraphID)
    const snap = stored?.entries?.[stored.index]?.snapshot
    return snap ? { ...doc, Graph: snap.Graph, UI: snap.UI } : doc
  }, [doc])

  // 画布编辑模型与浏览器前端持久化的历史栈（后退 / 前进 / 历史版本跳转）
  const editor = useGraphDocument(initialEditorDoc)
  const history = useGraphHistory({
    storageKey: doc.GraphID,
    currentDocument: editor.document,
    limit: 50,
  })
  const isDirty = useMemo(
    () => !sameGraphSnapshot(editor.document, { Graph: doc.Graph, UI: doc.UI }),
    [editor.document, doc.Graph, doc.UI],
  )
  const [saving, setSaving] = useState(false)
  const [savedToast, setSavedToast] = useState(false)
  const [showHistoryPopover, setShowHistoryPopover] = useState(false)
  // 右侧节点属性编辑抽屉开关：单击画布节点时打开，关闭或点空白处收起
  const [showNodeProps, setShowNodeProps] = useState(false)

  const [engines, setEngines] = useState<string[]>([])
  const [engine, setEngine] = useState(doc.Engine || 'immediate')
  // 运行输入：直接从当前画布文档的 Graph.InputSchema 实时读取，修改定义后立即同步输入框
  const declared = runInputFieldsFromSchema(editor.document.Graph.InputSchema)
  const [values, setValues] = useState<Record<string, string>>({})
  const [showSchemaPopover, setShowSchemaPopover] = useState(false)
  // watchTaskID 仅在需要 SSE 实时订阅时非空；查看已结束的历史任务时直接设置 snap
  const [watchTaskID, setWatchTaskID] = useState('')
  const [snap, setSnap] = useState<GraphTaskSnapshot | null>(null)
  const [error, setError] = useState('')

  // 底部多页签面板状态：执行明细 / 输入输出 / 执行列表
  const [bottomTab, setBottomTab] = useState<BottomTab>('detail')
  const [panelOpen, setPanelOpen] = useState(true)
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null)
  const [copiedKey, setCopiedKey] = useState<string | null>(null)
  // 执行明细左栏虚拟列表滚动偏移（固定行高 32px，避免 1000+ 节点同时挂载 DOM）
  const [stepScrollTop, setStepScrollTop] = useState(0)
  const stepListRef = useRef<HTMLDivElement>(null)

  // 历史执行列表状态
  const [taskList, setTaskList] = useState<GraphTaskSummary[]>([])
  const [listLoading, setListLoading] = useState(false)
  const [statusFilter, setStatusFilter] = useState<string>('')

  // 在线 DEBUG 状态：断点、节点级调试注入、重试策略与指定放行 Run（按 GraphID 在会话中保持）
  const initialDebug = useMemo(() => recallDebugSession(doc.GraphID), [doc.GraphID])
  const [breakpoints, setBreakpoints] = useState<string[]>(initialDebug.breakpoints)
  const [injects, setInjects] = useState<Record<string, GraphDebugInject>>(initialDebug.injects)
  const [maxAttempts, setMaxAttempts] = useState<number>(initialDebug.maxAttempts)
  const [retryDelayMS, setRetryDelayMS] = useState<number>(initialDebug.retryDelayMS)
  const [failPolicy, setFailPolicy] = useState<'fail_fast' | 'continue'>(initialDebug.failPolicy)
  const [showPolicyPopover, setShowPolicyPopover] = useState(false)
  const [targetRunID, setTargetRunID] = useState<string>('')
  const [debugHint, setDebugHint] = useState<string>('')
  // 节点注入 JSON 编辑草稿与报错提示
  const [injectVarsDraft, setInjectVarsDraft] = useState<string>('')
  const [injectVarsError, setInjectVarsError] = useState<string>('')
  const lastAutoFocusedDebugRunRef = useRef<string>('')

  // 同步在线调试状态到会话记忆（切换「返回编辑」与「去运行」不丢失断点与注入）
  useEffect(() => {
    rememberDebugSession(doc.GraphID, {
      breakpoints,
      injects,
      maxAttempts,
      retryDelayMS,
      failPolicy,
    })
  }, [doc.GraphID, breakpoints, injects, maxAttempts, retryDelayMS, failPolicy])

  useEffect(() => {
    api.engines().then(setEngines).catch((e: unknown) => setError(String(e)))
  }, [])

  /** 拉取当前图的 Task 历史列表。 */
  const refreshTaskList = useCallback(async () => {
    if (!doc.GraphID) return
    setListLoading(true)
    try {
      const rows = await api.tasks({
        GraphID: doc.GraphID,
        Status: statusFilter || undefined,
        Limit: 50,
      })
      setTaskList(rows)
    } catch (e) {
      setError(String(e))
    } finally {
      setListLoading(false)
    }
  }, [doc.GraphID, statusFilter])

  // 初次进入或切换状态过滤时拉取任务列表
  useEffect(() => {
    refreshTaskList()
  }, [refreshTaskList])

  // 画布编辑历史与撤销/重做/跳转/保存回调（全部存储在浏览器前端，无需后端存储）
  const captureBefore = useCallback(
    (labelOrOpts?: string | GraphHistoryPushOptions) => {
      if (history.isRestoring()) return
      history.push(editor.getDocument(), labelOrOpts)
    },
    [history, editor],
  )

  const applySnapshot = editor.load
  const onUndo = useCallback(() => {
    history.undo(editor.getDocument(), applySnapshot)
  }, [history, editor, applySnapshot])

  const onRedo = useCallback(() => {
    history.redo(editor.getDocument(), applySnapshot)
  }, [history, editor, applySnapshot])

  const onJumpHistory = useCallback(
    (targetIdx: number) => {
      history.jumpTo(targetIdx, editor.getDocument(), applySnapshot)
    },
    [history, editor, applySnapshot],
  )

  const addNode = useCallback(
    (type: string) => {
      const item = activePack.library.items.find((i) => i.type === type)
      const nodeType = item?.nodeType ?? type
      const scope = insertionScope(editor.nodes, selectedNodeId)
      const id = scopedId(scope, `n${Date.now().toString(36)}`)
      const displayLabel = item?.label ?? nodeType
      captureBefore(`添加节点 · ${displayLabel} (${id})`)
      editor.setNodes((ns) => [
        ...ns,
        {
          id,
          type: nodeType,
          name: displayLabel,
          position: insertionPosition(ns, scope),
          config: libraryDefaultConfig(activePack.library, type),
        },
      ])
      setSelectedNodeId(id)
      setShowNodeProps(true)
    },
    [activePack, captureBefore, editor, selectedNodeId],
  )

  const selectedEditorNode = useMemo(
    () => editor.nodes.find((n) => n.id === selectedNodeId) ?? null,
    [editor.nodes, selectedNodeId],
  )

  const patchEndpointSides = useCallback(
    (id: string, sides: EndpointSides) => {
      captureBefore(`旋转端点方位 · ${id}`)
      editor.setNodes((ns) => ns.map((n) => (n.id === id ? { ...n, endpointSides: sides } : n)))
    },
    [captureBefore, editor],
  )

  const specOf = useCallback(
    (type: string) => pack?.definitions.find((d) => d.nodeType === type)?.spec,
    [pack],
  )
  const selectedSpec = selectedEditorNode ? specOf(selectedEditorNode.type) : undefined
  const selectedEndpoints = useMemo(
    () =>
      selectedEditorNode
        ? resolveGraphNodeEndpoints(
            selectedEditorNode.type,
            selectedSpec?.Endpoints ?? activePack.registry.get(selectedEditorNode.type)?.displayEndpoints,
            selectedEditorNode.config,
            selectedSpec?.Renderer,
          )
        : undefined,
    [selectedEditorNode, selectedSpec, activePack],
  )
  const selectedSources = useMemo(() => {
    const at = selectedEditorNode ? scopeGraphOf(editor.document, selectedEditorNode.id) : undefined
    return at ? inputSourcesFor(at.graph, at.localId, specOf) : []
  }, [editor.document, selectedEditorNode, specOf])

  /** 保存当前画布结构、节点配置与运行参数 Schema。 */
  const save = useCallback(async (): Promise<boolean> => {
    setSaving(true)
    setError('')
    try {
      const current = withEndpointSides(editor.getDocument(), activePack.registry)
      const saved = await api.save(doc.GraphID, { Name: doc.Name, Description: doc.Description, ...current })
      const normalized = { ...saved, UI: parseGraphUI(saved.UI) }
      editor.load(normalized)
      onSaved?.(normalized)
      setSavedToast(true)
      setTimeout(() => setSavedToast(false), 1800)
      return true
    } catch (e) {
      setError(String(e))
      return false
    } finally {
      setSaving(false)
    }
  }, [editor, activePack.registry, doc.GraphID, doc.Name, doc.Description, onSaved])

  const activeGraph = snap?.Task.Graph?.Nodes?.length ? snap.Task.Graph : editor.document.Graph

  // 节点字典与合法断点集合（过滤掉已删除或不支持断点的 ID）
  const nodeById = useMemo(
    () => new Map((activeGraph.Nodes ?? []).map((n) => [n.ID, n])),
    [activeGraph.Nodes],
  )
  const validBreakpoints = useMemo(
    () =>
      breakpoints.filter((id) => {
        const n = nodeById.get(id)
        return n ? canSetBreakpointOnNode(id, n.Kind) : false
      }),
    [breakpoints, nodeById],
  )
  const breakpointSet = useMemo(() => new Set(validBreakpoints), [validBreakpoints])

  // 当前停在调试关口（WaitReason === 'debug'）上的所有 Run
  const debugWaitingRuns = useMemo(() => overlayDebugWaitingRuns(snap), [snap])
  const debugWaitingNodeIds = useMemo(
    () => new Set(debugWaitingRuns.map((r) => r.NodeID).filter(Boolean)),
    [debugWaitingRuns],
  )
  const activeDebugRun = useMemo(() => {
    if (!debugWaitingRuns.length) return null
    if (targetRunID) {
      const matched = debugWaitingRuns.find((r) => r.RunID === targetRunID)
      if (matched) return matched
    }
    if (selectedNodeId) {
      const onSelected = debugWaitingRuns.find((r) => r.NodeID === selectedNodeId)
      if (onSelected) return onSelected
    }
    return debugWaitingRuns[0]
  }, [debugWaitingRuns, targetRunID, selectedNodeId])

  const maps = useMemo(
    () => (snap ? buildRunStatusMaps(snap.Runs, activeGraph.Edges) : null),
    [snap, activeGraph.Edges],
  )

  const view = useMemo<GraphViewState>(() => {
    const nodes: NonNullable<GraphViewState['nodes']> = { ...(editor.view.nodes ?? {}) }
    if (maps) {
      for (const [id, status] of Object.entries(maps.nodeStatus)) {
        const isDebugParked = debugWaitingNodeIds.has(id)
        const progress = isDebugParked
          ? breakpointSet.has(id)
            ? '⏸ 断点'
            : '⏸ 单步'
          : maps.nodeProgress[id]
        nodes[id] = {
          ...nodes[id],
          runStatus: status,
          runProgress: progress,
        }
      }
    }
    for (const bpId of validBreakpoints) {
      nodes[bpId] = {
        ...nodes[bpId],
        breakpoint: true,
      }
    }
    for (const [injId, inj] of Object.entries(injects)) {
      if (isDebugInjectActive(inj)) {
        nodes[injId] = {
          ...nodes[injId],
          inject: true,
        }
      }
    }
    if (selectedNodeId) {
      nodes[selectedNodeId] = {
        ...nodes[selectedNodeId],
        selected: true,
      }
    }
    const edges: NonNullable<GraphViewState['edges']> = { ...(editor.view.edges ?? {}) }
    if (maps) {
      for (const [id, status] of Object.entries(maps.edgeStatus)) {
        edges[id] = {
          ...edges[id],
          runStatus: status,
          animated: status === 'running',
        }
      }
    }
    return { nodes, edges }
  }, [editor.view, maps, debugWaitingNodeIds, breakpointSet, validBreakpoints, injects, selectedNodeId])

  // 当任务停在新的调试关口（断点或单步）时，自动选中该节点并将左侧步骤列表滚动到可视区
  useEffect(() => {
    const first = debugWaitingRuns[0]
    if (!first?.NodeID) return
    const key = `${first.RunID}:${first.Seq}:${first.NodeID}`
    if (lastAutoFocusedDebugRunRef.current === key) return
    lastAutoFocusedDebugRunRef.current = key
    setSelectedNodeId(first.NodeID)
    setTargetRunID(first.RunID)
    setPanelOpen(true)
    setBottomTab('detail')
    const idx = (activeGraph.Nodes ?? []).findIndex((n) => n.ID === first.NodeID)
    if (idx >= 0 && stepListRef.current) {
      const ROW_H = 32
      const targetTop = Math.max(0, idx * ROW_H - 64)
      stepListRef.current.scrollTop = targetTop
      setStepScrollTop(targetTop)
    }
  }, [debugWaitingRuns, activeGraph.Nodes])

  // 订阅执行进度：第一个事件是完整快照，之后只含变化的 Run。
  // 高频增量事件按 requestAnimationFrame 合并（每帧最多渲染一次），避免千节点并发推进时高频阻塞主线程；终态时立即刷新。
  useEffect(() => {
    if (!watchTaskID) return
    const es = new EventSource(api.watchURL(watchTaskID))
    let pending: GraphWatchEvent[] = []
    let rafId = 0
    const flush = () => {
      rafId = 0
      if (!pending.length) return
      const batch = pending
      pending = []
      setSnap((cur) => batch.reduce((acc, item) => applyWatchEvent(acc, item), cur))
    }
    es.onmessage = (ev) => {
      const next = JSON.parse(ev.data) as GraphWatchEvent
      pending.push(next)
      const status = next.Snapshot ? next.Snapshot.Task.Status : next.Task?.Status
      if (status && TERMINAL.has(status)) {
        if (rafId) cancelAnimationFrame(rafId)
        flush()
        es.close()
        refreshTaskList()
        return
      }
      if (!rafId) {
        rafId = requestAnimationFrame(flush)
      }
    }
    es.addEventListener('error', (ev) => {
      // 服务端的 event: error 带 {Error, Code}；连接级错误没有 data，交给 EventSource 自动重连。
      const data = (ev as MessageEvent).data
      if (typeof data === 'string' && data) {
        if (rafId) cancelAnimationFrame(rafId)
        flush()
        setError(data)
        es.close()
      }
    })
    return () => {
      if (rafId) cancelAnimationFrame(rafId)
      es.close()
    }
  }, [watchTaskID, refreshTaskList])

  const currentTaskID = snap?.Task.TaskID || watchTaskID
  const isRunning = snap?.Task.Status === 'RUNNING' || snap?.Task.Status === 'PENDING'
  const isHung = overlayIsHung(snap)
  const isPausedOrDebugWaiting = Boolean(snap?.Task.Paused || debugWaitingRuns.length > 0 || isHung)

  const flashDebugHint = useCallback((msg: string) => {
    setDebugHint(msg)
    setTimeout(() => setDebugHint((cur) => (cur === msg ? '' : cur)), 3800)
  }, [])

  /** 构造当前表单填写的运行输入字典。 */
  const collectFormInput = useCallback((): Record<string, unknown> => {
    return Object.fromEntries(
      declared.filter((f) => (values[f.name] ?? '') !== '').map((f) => [f.name, parseInputValue(values[f.name])]),
    )
  }, [declared, values])

  /** 组装启动或重试时的 TaskPolicy（含重试策略、失败策略、单步、断点与节点注入）。 */
  const buildTaskPolicy = useCallback(
    (stepMode: boolean, overrideMaxAttempts?: number): GraphTaskPolicy => {
      const attempts = overrideMaxAttempts ?? maxAttempts
      const debug = buildDebugOptions({
        step: stepMode,
        breakpoints: validBreakpoints,
        injects,
      })
      return {
        ExecutionMode: doc.UI?.ExecutionMode ?? 'parallel',
        FailPolicy: failPolicy,
        MaxAttempts: attempts,
        RetryDelayMS: attempts > 1 ? retryDelayMS : 0,
        ...(debug ? { Debug: debug } : {}),
      }
    },
    [doc.UI?.ExecutionMode, failPolicy, maxAttempts, retryDelayMS, validBreakpoints, injects],
  )

  /** 启动新任务：若画布有未保存的编辑改动先自动保存，再按最新图版本启动。stepMode=true 时以单步调试模式启动。 */
  const startWithMode = useCallback(
    async (stepMode: boolean, overrideInput?: Record<string, unknown>, overrideMaxAttempts?: number) => {
      setError('')
      setDebugHint('')
      if (isDirty) {
        const ok = await save()
        if (!ok) return
      }
      setSnap(null)
      setWatchTaskID('')
      setShowNodeProps(false)
      lastAutoFocusedDebugRunRef.current = ''
      try {
        const input = overrideInput ?? collectFormInput()
        const receipt = await api.startTask({
          GraphID: doc.GraphID,
          Engine: engine,
          Input: input,
          Policy: buildTaskPolicy(stepMode, overrideMaxAttempts),
        })
        setWatchTaskID(receipt.TaskID)
        setPanelOpen(true)
        if (bottomTab === 'list') setBottomTab('detail')
        refreshTaskList()
      } catch (e) {
        setError(String(e))
      }
    },
    [isDirty, save, doc.GraphID, engine, collectFormInput, buildTaskPolicy, bottomTab, refreshTaskList],
  )

  const run = () => startWithMode(false)

  /**
   * 单步执行（Step）：
   * - 任务尚未运行或已结束：直接以 Step=true 启动新任务，自动停在第一个活动节点前；
   * - 任务处于调试挂起或已暂停：发送 DebugAction="step"，放行 1 个活动节点并在下一个活动节点前停住。
   */
  const stepOnce = useCallback(
    async (explicitRunID?: string) => {
      if (!currentTaskID || (!isRunning && !isHung)) {
        await startWithMode(true)
        return
      }
      setError('')
      try {
        // 若任务正在运行且尚未暂停、也没有停在 debug 的 Run，则先发送 pause 让其在下一个活动节点停下
        if (isRunning && !snap?.Task.Paused && debugWaitingRuns.length === 0) {
          await api.pause(currentTaskID)
          flashDebugHint('已请求单步挂起，当前节点完成后将在下一个活动节点前停住')
          return
        }
        const chosenRunID = explicitRunID ?? activeDebugRun?.RunID ?? ''
        const debugPayload = buildDebugOptions({
          step: true,
          breakpoints: validBreakpoints,
          injects,
        }) ?? { Step: true }
        await api.resume(currentTaskID, {
          DebugAction: 'step',
          Debug: debugPayload,
          ...(chosenRunID ? { TargetRunID: chosenRunID } : {}),
        })
      } catch (e) {
        setError(String(e))
      }
    },
    [
      currentTaskID,
      isRunning,
      isHung,
      startWithMode,
      snap?.Task.Paused,
      debugWaitingRuns.length,
      activeDebugRun?.RunID,
      validBreakpoints,
      injects,
      flashDebugHint,
    ],
  )

  /**
   * 执行到断点 / 继续（Continue）：
   * - 任务尚未运行或已结束：启动任务并一直运行至命中第一个断点（或结束）；
   * - 任务处于调试挂起或已暂停：发送 DebugAction="continue"，关闭单步并放行所有挂起 Run，直至命中下一个断点或结束。
   */
  const continueToBreakpoint = useCallback(async () => {
    if (!currentTaskID || (!isRunning && !isHung)) {
      await startWithMode(false)
      return
    }
    setError('')
    try {
      const debugPayload =
        buildDebugOptions({
          step: false,
          breakpoints: validBreakpoints,
          injects,
        }) ?? {}
      await api.resume(currentTaskID, {
        DebugAction: 'continue',
        Debug: debugPayload,
      })
    } catch (e) {
      setError(String(e))
    }
  }, [currentTaskID, isRunning, isHung, startWithMode, validBreakpoints, injects])

  const pause = useCallback(async () => {
    if (!currentTaskID) return
    setError('')
    try {
      await api.pause(currentTaskID)
    } catch (e) {
      setError(String(e))
    }
  }, [currentTaskID])

  const cancel = useCallback(async () => {
    if (!currentTaskID) return
    setError('')
    try {
      await api.cancel(currentTaskID)
      refreshTaskList()
    } catch (e) {
      setError(String(e))
    }
  }, [currentTaskID, refreshTaskList])

  /**
   * 重试 / 重新运行（Retry）：
   * - 若当前任务仍在运行或挂起，先取消当前任务；
   * - 若顶部输入框为空且上一次任务有输入，自动沿用上一次任务的输入并回填输入框；
   * - 携带最新断点、调试注入与重试策略重新启动任务。
   */
  const retryTask = useCallback(
    async (opts?: { step?: boolean; overrideMaxAttempts?: number }) => {
      setError('')
      try {
        if (currentTaskID && (isRunning || isHung)) {
          await api.cancel(currentTaskID, 'retry')
        }
        const formInput = collectFormInput()
        const hasFormInput = Object.keys(formInput).length > 0
        const prevInput = snap?.Task.Input
        const inputToUse = !hasFormInput && prevInput && Object.keys(prevInput).length > 0 ? prevInput : formInput
        if (!hasFormInput && prevInput && Object.keys(prevInput).length > 0) {
          const nextVals: Record<string, string> = {}
          for (const f of declared) {
            if (f.name in prevInput) nextVals[f.name] = formatInputValue(prevInput[f.name])
          }
          setValues(nextVals)
        }
        if (opts?.overrideMaxAttempts !== undefined) {
          setMaxAttempts(opts.overrideMaxAttempts)
        }
        await startWithMode(Boolean(opts?.step), inputToUse, opts?.overrideMaxAttempts)
      } catch (e) {
        setError(String(e))
      }
    },
    [currentTaskID, isRunning, isHung, collectFormInput, snap?.Task.Input, declared, startWithMode],
  )

  /**
   * 在运行中（RUNNING 且未暂停、无 debug 等待 Run）动态热同步断点与调试注入到后端 Task。
   */
  const syncDebugToRunningTask = useCallback(
    async (nextBreakpoints: string[], nextInjects: Record<string, GraphDebugInject>) => {
      if (!currentTaskID || !isRunning || snap?.Task.Paused || debugWaitingRuns.length > 0) return
      try {
        const validBps = nextBreakpoints.filter((id) => {
          const n = nodeById.get(id)
          return n ? canSetBreakpointOnNode(id, n.Kind) : false
        })
        const debugPayload =
          buildDebugOptions({
            step: Boolean(snap?.Task.Policy?.Debug?.Step),
            breakpoints: validBps,
            injects: nextInjects,
          }) ?? {}
        await api.resume(currentTaskID, { Debug: debugPayload })
      } catch {
        // 任务可能刚好在此瞬间进入终态，忽略竞争错误
      }
    },
    [currentTaskID, isRunning, snap?.Task.Paused, snap?.Task.Policy?.Debug?.Step, debugWaitingRuns.length, nodeById],
  )

  /** 切换某个节点的断点状态（所有顶层可执行节点均支持断点）。 */
  const toggleBreakpoint = useCallback(
    (nodeId: string, nodeKind?: string) => {
      const kind = nodeKind ?? nodeById.get(nodeId)?.Kind
      if (!canSetBreakpointOnNode(nodeId, kind)) {
        flashDebugHint('注释便签或循环体内部节点不支持顶层断点')
        return
      }
      setBreakpoints((prev) => {
        const exists = prev.includes(nodeId)
        const next = exists ? prev.filter((id) => id !== nodeId) : [...prev, nodeId]
        syncDebugToRunningTask(next, injects)
        return next
      })
    },
    [nodeById, flashDebugHint, syncDebugToRunningTask, injects],
  )

  const clearAllBreakpoints = useCallback(() => {
    setBreakpoints([])
    syncDebugToRunningTask([], injects)
  }, [syncDebugToRunningTask, injects])

  /** 更新某个节点的调试注入（Vars / Failed / Error）。 */
  const updateNodeInject = useCallback(
    (nodeId: string, patch: GraphDebugInject | null) => {
      setInjects((prev) => {
        const next = { ...prev }
        if (!patch || !isDebugInjectActive(patch)) {
          delete next[nodeId]
        } else {
          next[nodeId] = patch
        }
        syncDebugToRunningTask(breakpoints, next)
        return next
      })
    },
    [breakpoints, syncDebugToRunningTask],
  )

  /** 在「执行列表」中点击某条历史任务：加载快照并回放画布轨迹。 */
  const selectHistoryTask = async (row: GraphTaskSummary) => {
    setError('')
    try {
      if (TERMINAL.has(row.Status)) {
        setWatchTaskID('')
        const s = await api.snapshot(row.TaskID)
        setSnap(s)
      } else {
        const s = await api.snapshot(row.TaskID)
        setSnap(s)
        setWatchTaskID(row.TaskID)
      }
    } catch (e) {
      setError(String(e))
    }
  }

  /** 把某次任务的运行输入回填到顶部输入框。 */
  const fillInputsFromTask = (input?: Record<string, unknown>) => {
    if (!input) return
    const next: Record<string, string> = {}
    for (const f of declared) {
      if (f.name in input) {
        next[f.name] = formatInputValue(input[f.name])
      }
    }
    setValues(next)
  }

  const copyText = (key: string, text: string) => {
    navigator.clipboard?.writeText(text)
    setCopiedKey(key)
    setTimeout(() => setCopiedKey((cur) => (cur === key ? null : cur)), 1500)
  }

  // 汇总各路径 Run 上的节点输出：nodeID -> { field: value }
  // 内核每次离开节点时以 setOutputs 整表写入 Run.Outputs[nodeID]，直接引用该字典即可，
  // 避免在 100+ 并行子 Run × 1000 节点时产生数万次对象展开拷贝。
  const mergedNodeOutputs = useMemo(() => {
    const out: Record<string, Record<string, unknown>> = {}
    for (const r of snap?.Runs ?? []) {
      if (r.Outputs) {
        for (const [nid, fields] of Object.entries(r.Outputs)) {
          if (fields && (!out[nid] || Object.keys(fields).length >= Object.keys(out[nid]).length)) {
            out[nid] = fields
          }
        }
      }
      if (r.Output && Object.keys(r.Output).length > 0 && r.NodeID) {
        out[r.NodeID] = r.Output
      }
    }
    return out
  }, [snap?.Runs])

  // 按节点归集停在该节点上的 Run（用于展示错误信息、重试次数、等待原因）
  const runsByNode = useMemo(() => {
    const map: Record<string, GraphRun[]> = {}
    for (const r of snap?.Runs ?? []) {
      if (!r.NodeID) continue
      const list = map[r.NodeID]
      if (list) list.push(r)
      else map[r.NodeID] = [r]
    }
    return map
  }, [snap?.Runs])

  // 执行明细中当前高亮查看的节点：优先用户点击的节点，否则优先失败节点 -> 运行中节点 -> 第一个已执行节点 -> 第一个节点
  const activeDetailNode = useMemo<GraphNodeJSON | null>(() => {
    const nodes = activeGraph.Nodes ?? []
    if (!nodes.length) return null
    if (selectedNodeId) {
      const found = nodes.find((n) => n.ID === selectedNodeId)
      if (found) return found
    }
    if (maps) {
      const failed = nodes.find((n) => maps.nodeStatus[n.ID] === 'failed')
      if (failed) return failed
      const running = nodes.find((n) => maps.nodeStatus[n.ID] === 'running')
      if (running) return running
      const succeeded = nodes.find((n) => maps.nodeStatus[n.ID] === 'succeeded')
      if (succeeded) return succeeded
    }
    return nodes[0]
  }, [activeGraph.Nodes, selectedNodeId, maps])

  // 切换选中节点时同步该节点的 Vars 注入 JSON 草稿
  const activeDetailNodeId = activeDetailNode?.ID ?? ''
  useEffect(() => {
    if (!activeDetailNodeId) {
      setInjectVarsDraft('')
      setInjectVarsError('')
      return
    }
    const curVars = injects[activeDetailNodeId]?.Vars
    setInjectVarsDraft(curVars && Object.keys(curVars).length > 0 ? JSON.stringify(curVars, null, 2) : '')
    setInjectVarsError('')
  }, [activeDetailNodeId, injects])

  // 全局快捷键：
  // - 画布编辑：⌘S/Ctrl+S 保存，⌘Z/Ctrl+Z 后退，⌘⇧Z/Ctrl+Y 前进，R 旋转选中节点端点
  // - 运行调试：F10 单步执行，F8 执行到断点/继续，F9 为当前选中节点切换断点
  useEffect(() => {
    const onKeyDown = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null
      if (el && (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.isContentEditable)) return
      const mod = e.metaKey || e.ctrlKey
      if (mod && (e.key === 's' || e.key === 'S')) {
        e.preventDefault()
        void save()
        return
      }
      if (mod && (e.key === 'z' || e.key === 'Z')) {
        e.preventDefault()
        if (e.shiftKey) onRedo()
        else onUndo()
        return
      }
      if (mod && (e.key === 'y' || e.key === 'Y')) {
        e.preventDefault()
        onRedo()
        return
      }
      if (!mod && !e.altKey && (e.key === 'r' || e.key === 'R') && selectedEditorNode && selectedEndpoints) {
        e.preventDefault()
        patchEndpointSides(
          selectedEditorNode.id,
          rotateEndpointSides(selectedEditorNode.endpointSides, endpointIds(selectedEndpoints)),
        )
        return
      }
      if (e.key === 'F10') {
        e.preventDefault()
        void stepOnce()
      } else if (e.key === 'F8') {
        e.preventDefault()
        void continueToBreakpoint()
      } else if (e.key === 'F9' && activeDetailNode) {
        e.preventDefault()
        toggleBreakpoint(activeDetailNode.ID, activeDetailNode.Kind)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [
    save,
    onUndo,
    onRedo,
    selectedEditorNode,
    selectedEndpoints,
    patchEndpointSides,
    stepOnce,
    continueToBreakpoint,
    activeDetailNode,
    toggleBreakpoint,
  ])

  // 还原选中节点的输入明细（引用来源 + 实际解析值）
  const activeNodeInputs = useMemo(() => {
    if (!activeDetailNode?.Inputs) return []
    return Object.entries(activeDetailNode.Inputs).map(([name, ref]: [string, GraphInputRef]) => {
      const injectedVal = injects[activeDetailNode.ID]?.Vars?.[name]
      const hasInjected =
        injects[activeDetailNode.ID]?.Vars != null &&
        name in (injects[activeDetailNode.ID]?.Vars ?? {})
      if (ref.From === '$input' && ref.Field) {
        return {
          name,
          source: `$input.${ref.Field}`,
          kind: hasInjected ? '调试注入覆盖' : '运行输入',
          value: hasInjected ? injectedVal : snap?.Task.Input?.[ref.Field],
          injected: hasInjected,
        }
      }
      if (ref.From && ref.Field) {
        return {
          name,
          source: `${ref.From}.${ref.Field}`,
          kind: hasInjected ? '调试注入覆盖' : '节点输出',
          value: hasInjected ? injectedVal : mergedNodeOutputs[ref.From]?.[ref.Field],
          injected: hasInjected,
        }
      }
      return {
        name,
        source: '字面量',
        kind: hasInjected ? '调试注入覆盖' : '静态配置',
        value: hasInjected ? injectedVal : ref.Value,
        injected: hasInjected,
      }
    })
  }, [activeDetailNode, injects, snap?.Task.Input, mergedNodeOutputs])

  // 还原选中节点的输出字典
  const activeNodeOutput = useMemo<Record<string, unknown> | undefined>(() => {
    if (!activeDetailNode) return undefined
    const direct = mergedNodeOutputs[activeDetailNode.ID]
    if (direct && Object.keys(direct).length > 0) return direct
    if (shortKind(activeDetailNode.Kind) === 'end' && snap?.Task.Output) {
      return snap.Task.Output
    }
    return direct
  }, [activeDetailNode, mergedNodeOutputs, snap?.Task.Output])

  /** 修改运行输入 Schema（写入画布文档并标记为待保存；空时去掉 InputSchema 键，与后端 omitempty 一致）。 */
  const replaceGraphInputs = useCallback(
    (next: GraphJSONSchema | undefined) => {
      captureBefore({ label: '修改运行参数声明', coalesceKey: 'graph-inputs-schema' })
      const cur = editor.getDocument()
      const { InputSchema: _, ...graph } = cur.Graph
      editor.load({ ...cur, Graph: next ? { ...graph, InputSchema: next } : graph })
    },
    [captureBefore, editor],
  )

  const visitedNodeCount = useMemo(
    () => (maps ? Object.keys(maps.nodeStatus).length : 0),
    [maps],
  )
  const totalNodeCount = activeGraph.Nodes?.length ?? 0
  const activeInjectCount = useMemo(
    () => Object.values(injects).filter((inj) => isDebugInjectActive(inj)).length,
    [injects],
  )

  return (
    <div className="flex h-full min-h-0 flex-col">
      {/* ========================================================================
          第一行（Row 1）：工作流选择 + 画布编辑历史（前端存储）与保存组
          ======================================================================== */}
      <div className="relative z-40 flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-border bg-card/50 px-3 py-2">
        <div className="flex flex-wrap items-center gap-2">
          {workflowSelector ? (
            <>
              {workflowSelector}
              <div className="h-4 w-px shrink-0 bg-border" />
            </>
          ) : null}

          <span className="rounded bg-muted/80 px-2 py-1 text-[11px] font-medium text-muted-foreground select-none">
            画布与编辑保存
          </span>

          {/* 后退 / 前进 / 浏览器本地历史时间线 */}
          <div className="relative inline-flex h-8 items-center rounded-ui border border-border bg-background">
            <button
              type="button"
              disabled={!history.canUndo}
              onClick={onUndo}
              title={
                history.canUndo
                  ? `后退：撤销「${history.currentEntry?.label ?? '上一步'}」(⌘Z / Ctrl+Z)`
                  : '暂无可后退的操作 (⌘Z / Ctrl+Z)'
              }
              className="inline-flex h-full items-center gap-1.5 rounded-l-ui border-r border-border px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted/60 disabled:opacity-40 disabled:pointer-events-none cursor-pointer"
            >
              <Undo2 className="h-3.5 w-3.5" />
              后退
              {history.undoCount > 0 ? (
                <span className="rounded-full bg-muted px-1.5 py-0.2 font-mono text-[10px] text-muted-foreground">
                  {history.undoCount}
                </span>
              ) : null}
            </button>
            <button
              type="button"
              disabled={!history.canRedo}
              onClick={onRedo}
              title={
                history.canRedo
                  ? `前进：重做「${history.entries[history.currentIndex + 1]?.label ?? '下一步'}」(⌘⇧Z / Ctrl+Y)`
                  : '暂无可前进的操作 (⌘⇧Z / Ctrl+Y)'
              }
              className="inline-flex h-full items-center gap-1.5 border-r border-border px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted/60 disabled:opacity-40 disabled:pointer-events-none cursor-pointer"
            >
              <Redo2 className="h-3.5 w-3.5" />
              前进
              {history.redoCount > 0 ? (
                <span className="rounded-full bg-primary/15 px-1.5 py-0.2 font-mono text-[10px] text-primary">
                  {history.redoCount}
                </span>
              ) : null}
            </button>
            <button
              type="button"
              onClick={() => {
                setShowHistoryPopover((v) => !v)
                setShowSchemaPopover(false)
                setShowPolicyPopover(false)
              }}
              title="查看浏览器本地保存的流程图编辑历史记录（点击任意步骤可直接跳转）"
              className={`inline-flex h-full items-center gap-1.5 rounded-r-ui px-2.5 text-xs font-medium transition-colors cursor-pointer ${
                showHistoryPopover
                  ? 'bg-primary/10 text-primary'
                  : 'text-foreground hover:bg-muted/60'
              }`}
            >
              <History className="h-3.5 w-3.5" />
              历史记录
              <span className="rounded-full bg-muted px-1.5 py-0.2 font-mono text-[10px] text-muted-foreground">
                {history.entries.length}
              </span>
            </button>

            {/* 浏览器本地编辑历史时间线弹出面板 */}
            {showHistoryPopover ? (
              <div className="absolute left-0 top-full mt-1.5 z-50 flex w-96 flex-col overflow-hidden rounded-ui border border-border bg-card text-xs shadow-xl">
                <div className="flex items-center justify-between border-b border-border/60 bg-muted/30 px-3 py-2">
                  <div className="flex items-center gap-1.5">
                    <History className="h-3.5 w-3.5 text-primary" />
                    <span className="font-semibold text-foreground">流程图编辑历史</span>
                    <span className="rounded bg-emerald-500/15 px-1.5 py-0.5 text-[10px] font-medium text-emerald-600 dark:text-emerald-400">
                      浏览器本地存储
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    {isDirty ? (
                      <button
                        type="button"
                        onClick={() => {
                          captureBefore('还原为已保存版本')
                          editor.load(doc)
                        }}
                        title="丢弃未保存修改，恢复到服务端最新保存版本"
                        className="text-[11px] text-amber-600 hover:underline dark:text-amber-400 cursor-pointer"
                      >
                        还原已保存版
                      </button>
                    ) : null}
                    {history.entries.length > 1 ? (
                      <button
                        type="button"
                        onClick={() => history.reset(editor.getDocument())}
                        title="清空浏览器本地存储的历史步骤，仅保留当前画布状态"
                        className="text-[11px] text-muted-foreground hover:text-destructive cursor-pointer"
                      >
                        清空历史
                      </button>
                    ) : null}
                    <button
                      type="button"
                      onClick={() => setShowHistoryPopover(false)}
                      className="text-[11px] text-muted-foreground hover:text-foreground cursor-pointer"
                    >
                      关闭
                    </button>
                  </div>
                </div>

                <div className="max-h-72 overflow-y-auto divide-y divide-border/40">
                  {[...history.entries]
                    .map((entry, idx) => ({ entry, idx }))
                    .reverse()
                    .map(({ entry, idx }) => {
                      const isCurrent = idx === history.currentIndex
                      const isFuture = idx > history.currentIndex
                      return (
                        <button
                          key={entry.id}
                          type="button"
                          onClick={() => onJumpHistory(idx)}
                          className={`flex w-full items-center justify-between gap-2 px-3 py-2 text-left transition-colors cursor-pointer ${
                            isCurrent
                              ? 'bg-primary/10 text-foreground'
                              : isFuture
                                ? 'bg-muted/20 text-muted-foreground hover:bg-muted/50'
                                : 'hover:bg-muted/40 text-foreground'
                          }`}
                        >
                          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                            <div className="flex items-center gap-1.5">
                              <span
                                className={`shrink-0 rounded px-1.5 py-0.2 font-mono text-[10px] font-semibold ${
                                  isCurrent
                                    ? 'bg-primary text-primary-foreground'
                                    : isFuture
                                      ? 'bg-amber-500/15 text-amber-600 dark:text-amber-400'
                                      : 'bg-muted text-muted-foreground'
                                }`}
                              >
                                #{idx}
                              </span>
                              <span className={`truncate font-medium ${isFuture ? ' opacity-75' : ''}`}>
                                {entry.label}
                              </span>
                            </div>
                            <div className="flex items-center gap-2 pl-1 font-mono text-[10px] text-muted-foreground">
                              <span>
                                {entry.nodeCount} 节点 · {entry.edgeCount} 连线
                              </span>
                              <span>·</span>
                              <span>{formatTime(new Date(entry.timestamp).toISOString())}</span>
                            </div>
                          </div>
                          <div className="shrink-0">
                            {isCurrent ? (
                              <span className="rounded-full bg-primary/15 px-2 py-0.5 text-[10px] font-semibold text-primary">
                                当前状态
                              </span>
                            ) : isFuture ? (
                              <span className="inline-flex items-center gap-0.5 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                                <Redo2 className="h-3 w-3" />
                                前进至此
                              </span>
                            ) : (
                              <span className="inline-flex items-center gap-0.5 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground">
                                <Undo2 className="h-3 w-3" />
                                回退至此
                              </span>
                            )}
                          </div>
                        </button>
                      )
                    })}
                </div>

                <div className="border-t border-border/60 bg-muted/20 px-3 py-1.5 text-[10px] text-muted-foreground">
                  历史操作自动保存在浏览器本地存储（localStorage），刷新页面不丢失，无需后端落盘。
                </div>
              </div>
            ) : null}
          </div>

          {/* 编辑保存 */}
          <button
            type="button"
            onClick={() => void save()}
            disabled={saving}
            title="保存画布结构、节点配置与参数定义到服务端 (⌘S / Ctrl+S)"
            className="inline-flex h-8 items-center gap-1.5 rounded-ui bg-primary px-3 text-xs font-semibold text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50 cursor-pointer"
          >
            {savedToast ? <Check className="h-3.5 w-3.5" /> : <Save className="h-3.5 w-3.5" />}
            {saving ? '保存中…' : savedToast ? '已保存' : '编辑保存'}
            {isDirty && !saving && !savedToast ? (
              <span className="h-2 w-2 rounded-full bg-amber-300" title="有未保存的画布修改" />
            ) : null}
          </button>

          {/* 选中节点时可一键开关右侧节点属性编辑面板 */}
          {selectedEditorNode ? (
            <button
              type="button"
              onClick={() => setShowNodeProps((v) => !v)}
              title="打开或关闭右侧节点属性编辑面板"
              className={`inline-flex h-8 items-center gap-1.5 rounded-ui border px-2.5 text-xs font-medium transition-colors cursor-pointer ${
                showNodeProps
                  ? 'border-primary/40 bg-primary/10 text-primary'
                  : 'border-border bg-background text-foreground hover:bg-muted/60'
              }`}
            >
              节点属性 ({selectedEditorNode.id})
            </button>
          ) : null}
        </div>

        <div className="flex items-center gap-2 text-[11px] text-muted-foreground select-none">
          <span>
            {editor.document.Graph.Nodes.length} 节点 · {editor.document.Graph.Edges.length} 连线
          </span>
          <span className="hidden lg:inline text-muted-foreground/60">
            · 单击编辑节点 · 双击打断点 · 按 R 旋转端点 · ⌘S 保存
          </span>
        </div>
      </div>

      {/* ========================================================================
          第二行（Row 2）：节点执行与在线 DEBUG 控制组（执行、单步、暂停、继续、重试、运行参数、断点）
          ======================================================================== */}
      <div className="relative z-30 flex shrink-0 flex-wrap items-center gap-2 border-b border-border bg-background px-3 py-2">
        <span className="rounded bg-indigo-500/10 px-2 py-1 text-[11px] font-medium text-indigo-600 dark:text-indigo-400 select-none">
          节点执行控制
        </span>

        {/* 1. 执行、单步、暂停、继续、重试、取消 */}
        <div className="flex shrink-0 flex-wrap items-center gap-1.5">
          {!isRunning && !isHung ? (
            <>
              <button
                type="button"
                onClick={run}
                title={
                  validBreakpoints.length > 0
                    ? `启动任务并执行到首个命中的断点（当前共 ${validBreakpoints.length} 个断点，快捷键 F8）`
                    : '启动任务（快捷键 F8）'
                }
                className="inline-flex h-8 items-center gap-1.5 rounded-ui bg-primary px-3 text-xs font-semibold text-primary-foreground transition-colors hover:bg-primary/90 cursor-pointer"
              >
                <Play className="h-3.5 w-3.5 fill-current" />
                {validBreakpoints.length > 0 ? `执行到断点 (${validBreakpoints.length})` : '开始执行'}
              </button>
              <button
                type="button"
                onClick={() => void stepOnce()}
                title="以单步模式启动调试：在进入第一个节点前自动停住（快捷键 F10）"
                className="inline-flex h-8 items-center gap-1 rounded-ui border border-amber-500/40 bg-amber-500/10 px-2.5 text-xs font-medium text-amber-700 transition-colors hover:bg-amber-500/20 dark:text-amber-300 cursor-pointer"
              >
                <StepForward className="h-3.5 w-3.5" />
                单步执行
              </button>
            </>
          ) : null}

          {isRunning && !snap?.Task.Paused ? (
            <button
              type="button"
              onClick={pause}
              title="暂停任务：当前节点完成后，在进入下一个节点前挂起"
              className="inline-flex h-8 items-center gap-1 rounded-ui border border-amber-500/40 bg-amber-500/10 px-2.5 text-xs font-medium text-amber-700 transition-colors hover:bg-amber-500/20 dark:text-amber-300 cursor-pointer"
            >
              <Pause className="h-3.5 w-3.5" />
              暂停
            </button>
          ) : null}

          {isPausedOrDebugWaiting ? (
            <>
              <button
                type="button"
                onClick={() => void stepOnce()}
                title="单步执行当前挂起的节点，并在下一个节点前再次停住（快捷键 F10）"
                className="inline-flex h-8 items-center gap-1 rounded-ui bg-amber-500 px-2.5 text-xs font-semibold text-white transition-colors hover:bg-amber-600 cursor-pointer"
              >
                <StepForward className="h-3.5 w-3.5" />
                单步下一步
              </button>
              <button
                type="button"
                onClick={() => void continueToBreakpoint()}
                title={
                  validBreakpoints.length > 0
                    ? '关闭单步并继续运行，直到命中下一个断点或任务结束（快捷键 F8）'
                    : '关闭单步并继续全速运行至结束（快捷键 F8）'
                }
                className="inline-flex h-8 items-center gap-1 rounded-ui bg-primary px-2.5 text-xs font-semibold text-primary-foreground transition-colors hover:bg-primary/90 cursor-pointer"
              >
                <FastForward className="h-3.5 w-3.5" />
                {validBreakpoints.length > 0 ? '执行到断点' : '继续'}
              </button>
            </>
          ) : null}

          {snap ? (
            <button
              type="button"
              onClick={() => void retryTask()}
              title="使用当前输入、断点与重试策略重新执行任务"
              className="inline-flex h-8 items-center gap-1 rounded-ui border border-border bg-background px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted/60 cursor-pointer"
            >
              <RotateCcw className="h-3.5 w-3.5" />
              重试
            </button>
          ) : null}

          {isRunning || isHung ? (
            <button
              type="button"
              onClick={cancel}
              title="取消正在运行或挂起的任务"
              className="inline-flex h-8 items-center gap-1 rounded-ui border border-destructive/40 bg-destructive/10 px-2.5 text-xs font-medium text-destructive transition-colors hover:bg-destructive/20 cursor-pointer"
            >
              <Square className="h-3 w-3 fill-current" />
              取消
            </button>
          ) : null}
        </div>

        <div className="h-4 w-px shrink-0 bg-border" />

        {/* 2. 引擎与运行参数组（引擎选择 + 参数输入框 + 运行参数定义） */}
        <div className="flex shrink-0 flex-wrap items-center gap-1.5">
          <div className="inline-flex h-8 shrink-0 items-center">
            <span className="flex h-full shrink-0 items-center rounded-l-ui border border-r-0 border-input bg-muted/40 px-2.5 text-xs text-muted-foreground select-none">
              引擎
            </span>
            <Select
              value={engine}
              onChange={setEngine}
              aria-label="选择引擎"
              options={(engines.length ? engines : [engine]).map((k) => ({ value: k, label: k }))}
              className="w-28"
              buttonClassName="rounded-l-none font-mono text-xs"
            />
          </div>

          {declared.map((f) => (
            <label
              key={f.name}
              className="inline-flex h-8 w-36 shrink-0 items-center overflow-hidden rounded-ui border border-input bg-background transition-colors focus-within:border-primary"
            >
              <span className="flex h-full shrink-0 items-center border-r border-input bg-muted/40 px-2 font-mono text-xs text-muted-foreground select-none">
                {f.name}
                {f.required ? <span className="ml-0.5 text-destructive">*</span> : null}
              </span>
              <input
                aria-label={`${f.name}${f.required ? ' *' : ''}`}
                value={values[f.name] ?? ''}
                onChange={(e) => setValues((cur) => ({ ...cur, [f.name]: e.target.value }))}
                className="h-full min-w-0 flex-1 bg-transparent px-2 font-mono text-xs text-foreground placeholder:text-muted-foreground/50 focus:outline-none"
                placeholder='如 3、["a"]'
              />
            </label>
          ))}

          {/* 运行参数定义（InputSchema）弹出管理 */}
          <div className="relative">
            <button
              type="button"
              onClick={() => {
                setShowSchemaPopover((v) => !v)
                setShowPolicyPopover(false)
              }}
              title="定义或增删本工作流的运行输入参数 Schema"
              className={`inline-flex h-8 items-center gap-1.5 rounded-ui border px-2.5 text-xs font-medium transition-colors cursor-pointer ${
                showSchemaPopover
                  ? 'border-primary/40 bg-primary/10 text-primary'
                  : 'border-border bg-background text-foreground hover:bg-muted/60'
              }`}
            >
              <Settings2 className="h-3.5 w-3.5" />
              运行参数{declared.length ? ` (${declared.length})` : ''}
            </button>
            {showSchemaPopover ? (
              <div className="absolute left-0 top-full mt-1.5 z-50 flex w-80 flex-col gap-3 rounded-ui border border-border bg-card p-3 text-xs shadow-xl">
                <div className="flex items-center justify-between border-b border-border/60 pb-1.5">
                  <span className="font-semibold text-foreground">运行参数定义 (InputSchema)</span>
                  <button
                    type="button"
                    onClick={() => setShowSchemaPopover(false)}
                    className="text-muted-foreground hover:text-foreground cursor-pointer"
                  >
                    关闭
                  </button>
                </div>
                <GraphInputsFields
                  schema={editor.document.Graph.InputSchema}
                  onReplace={replaceGraphInputs}
                />
              </div>
            ) : null}
          </div>
        </div>

        <div className="h-4 w-px shrink-0 bg-border" />

        {/* 3. 断点、重试策略与清除轨迹组 */}
        <div className="flex shrink-0 items-center gap-1.5">
          {activeDetailNode && canSetBreakpointOnNode(activeDetailNode.ID, activeDetailNode.Kind) ? (
            <button
              type="button"
              onClick={() => toggleBreakpoint(activeDetailNode.ID, activeDetailNode.Kind)}
              title="为当前选中的节点添加或移除断点（快捷键 F9，也可双击画布节点）"
              className={`inline-flex h-8 items-center gap-1.5 rounded-ui border px-2.5 text-xs font-medium transition-colors cursor-pointer ${
                breakpointSet.has(activeDetailNode.ID)
                  ? 'border-red-500/40 bg-red-500/12 text-red-600 dark:text-red-400'
                  : 'border-border bg-background text-foreground hover:bg-muted/60'
              }`}
            >
              <CircleDot className="h-3.5 w-3.5 text-red-500" />
              {breakpointSet.has(activeDetailNode.ID)
                ? `取消断点 (${activeDetailNode.ID})`
                : `打断点 (${activeDetailNode.ID})`}
            </button>
          ) : null}

          {validBreakpoints.length > 0 ? (
            <button
              type="button"
              onClick={clearAllBreakpoints}
              title={`已设断点: ${validBreakpoints.join(', ')}（点击清空全部断点）`}
              className="inline-flex h-8 items-center gap-1 rounded-ui border border-red-500/30 bg-red-500/10 px-2 text-xs font-medium text-red-600 transition-colors hover:bg-red-500/20 dark:text-red-400 cursor-pointer"
            >
              <span className="h-2 w-2 rounded-full bg-red-500" />
              断点 ({validBreakpoints.length})
              <Trash2 className="ml-0.5 h-3 w-3 opacity-75" />
            </button>
          ) : null}

          {/* 重试与容错策略弹出配置 */}
          <div className="relative">
            <button
              type="button"
              onClick={() => {
                setShowPolicyPopover((v) => !v)
                setShowSchemaPopover(false)
              }}
              title="配置节点失败自动重试次数、重试间隔与失败策略"
              className={`inline-flex h-8 items-center gap-1.5 rounded-ui border px-2.5 text-xs font-medium transition-colors cursor-pointer ${
                maxAttempts > 1 || failPolicy !== 'fail_fast' || showPolicyPopover
                  ? 'border-primary/40 bg-primary/10 text-primary'
                  : 'border-border bg-background text-muted-foreground hover:bg-muted/60 hover:text-foreground'
              }`}
            >
              <SlidersHorizontal className="h-3.5 w-3.5" />
              <span>
                {maxAttempts > 1 ? `自动重试×${maxAttempts - 1}` : '重试策略'}
                {failPolicy === 'continue' ? ' · 容错继续' : ''}
              </span>
            </button>
            {showPolicyPopover ? (
              <div className="absolute left-0 top-full mt-1.5 z-50 flex w-72 flex-col gap-2.5 rounded-ui border border-border bg-card p-3 text-xs shadow-xl">
                <div className="flex items-center justify-between border-b border-border/60 pb-1.5">
                  <span className="font-semibold text-foreground">节点重试与容错策略</span>
                  <button
                    type="button"
                    onClick={() => setShowPolicyPopover(false)}
                    className="text-muted-foreground hover:text-foreground cursor-pointer"
                  >
                    关闭
                  </button>
                </div>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-muted-foreground">最大尝试次数 (MaxAttempts)</span>
                  <select
                    value={maxAttempts}
                    onChange={(e) => setMaxAttempts(Number(e.target.value))}
                    className="h-7 rounded border border-input bg-background px-2 font-mono text-xs text-foreground"
                  >
                    <option value={1}>1 次（不重试）</option>
                    <option value={2}>2 次（失败重试 1 次）</option>
                    <option value={3}>3 次（失败重试 2 次）</option>
                    <option value={5}>5 次（失败重试 4 次）</option>
                  </select>
                </div>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-muted-foreground">重试间隔 (RetryDelayMS)</span>
                  <select
                    value={retryDelayMS}
                    onChange={(e) => setRetryDelayMS(Number(e.target.value))}
                    disabled={maxAttempts <= 1}
                    className="h-7 rounded border border-input bg-background px-2 font-mono text-xs text-foreground disabled:opacity-50"
                  >
                    <option value={0}>0 ms（立即重试）</option>
                    <option value={200}>200 ms</option>
                    <option value={500}>500 ms</option>
                    <option value={1000}>1000 ms</option>
                    <option value={2000}>2000 ms</option>
                  </select>
                </div>
                <div className="flex items-center justify-between gap-2">
                  <span className="text-muted-foreground">分支失败策略 (FailPolicy)</span>
                  <select
                    value={failPolicy}
                    onChange={(e) => setFailPolicy(e.target.value as 'fail_fast' | 'continue')}
                    className="h-7 rounded border border-input bg-background px-2 font-mono text-xs text-foreground"
                  >
                    <option value="fail_fast">fail_fast（立即终止）</option>
                    <option value="continue">continue（其余分支继续）</option>
                  </select>
                </div>
                <p className="text-[11px] leading-relaxed text-muted-foreground">
                  提示：双击画布上的任意节点可直接打断点；在下方「执行明细与调试」可为节点注入变量或模拟报错以测试自动重试。
                </p>
              </div>
            ) : null}
          </div>

          {snap && !isRunning && !isHung ? (
            <button
              type="button"
              onClick={() => {
                setSnap(null)
                setWatchTaskID('')
              }}
              title="清除画布上的运行轨迹高亮染色，恢复干净编辑态"
              className="inline-flex h-8 items-center gap-1 rounded-ui border border-border bg-background px-2 text-xs text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground cursor-pointer"
            >
              <Eraser className="h-3.5 w-3.5" />
              清除轨迹
            </button>
          ) : null}
        </div>

        {snap || error || debugHint ? (
          <>
            <div className="h-4 w-px shrink-0 bg-border" />
            <div className="flex min-w-0 flex-1 items-center gap-2 text-xs">
              {snap ? (
                <>
                  <span className={`shrink-0 rounded-full px-2 py-0.5 font-medium ${statusBadgeClass(snap.Task.Status)}`}>
                    {snap.Task.Status}
                  </span>
                  <span className="shrink-0 font-mono text-muted-foreground" title={snap.Task.TaskID}>
                    {snap.Task.TaskID.slice(0, 8)}
                  </span>
                  <span className="shrink-0 text-muted-foreground">
                    {formatDuration(snap.Task.StartedAt, snap.Task.FinishedAt)}
                  </span>
                  {snap.Task.Error ? (
                    <span className="min-w-0 flex-1 truncate text-destructive" title={snap.Task.Error}>
                      {snap.Task.Error}
                    </span>
                  ) : null}
                </>
              ) : null}
              {debugHint ? (
                <span className="min-w-0 flex-1 truncate text-amber-600 dark:text-amber-300" title={debugHint}>
                  {debugHint}
                </span>
              ) : null}
              {error ? (
                <span className="min-w-0 flex-1 truncate text-destructive" title={error}>
                  {error}
                </span>
              ) : null}
            </div>
          </>
        ) : null}
      </div>

      {/* 在线 DEBUG 挂起状态横幅：当任务停在断点、单步或暂停时醒目提示并提供快捷单步/继续控制 */}
      {debugWaitingRuns.length > 0 || snap?.Task.Paused ? (
        <div className="flex shrink-0 flex-wrap items-center justify-between gap-2 border-b border-amber-500/30 bg-amber-500/10 px-3 py-1.5 text-xs">
          <div className="flex min-w-0 flex-wrap items-center gap-2">
            <span className="inline-flex items-center gap-1 rounded-full bg-amber-500/20 px-2 py-0.5 font-semibold text-amber-700 dark:text-amber-300">
              <Bug className="h-3.5 w-3.5" />
              {activeDebugRun && breakpointSet.has(activeDebugRun.NodeID)
                ? '已命中断点挂起'
                : snap?.Task.Policy?.Debug?.Step
                  ? '单步执行挂起'
                  : '调试暂停挂起'}
            </span>
            {activeDebugRun ? (
              <span className="font-mono text-foreground">
                即将执行节点：
                <button
                  type="button"
                  onClick={() => {
                    setSelectedNodeId(activeDebugRun.NodeID)
                    setBottomTab('detail')
                    setPanelOpen(true)
                  }}
                  className="ml-1 font-semibold text-amber-700 underline underline-offset-2 hover:opacity-80 dark:text-amber-300 cursor-pointer"
                >
                  {nodeById.get(activeDebugRun.NodeID)?.Name || activeDebugRun.NodeID} ({activeDebugRun.NodeID})
                </button>
              </span>
            ) : (
              <span className="text-muted-foreground">等待当前节点完成后进入调试挂起…</span>
            )}
            {debugWaitingRuns.length > 1 ? (
              <label className="inline-flex items-center gap-1 text-[11px] text-muted-foreground">
                <span>选择放行分支 Run:</span>
                <select
                  value={activeDebugRun?.RunID ?? ''}
                  onChange={(e) => {
                    const rid = e.target.value
                    setTargetRunID(rid)
                    const found = debugWaitingRuns.find((r) => r.RunID === rid)
                    if (found?.NodeID) setSelectedNodeId(found.NodeID)
                  }}
                  className="h-6 rounded border border-amber-500/40 bg-background px-1.5 font-mono text-[11px] text-foreground"
                >
                  {debugWaitingRuns.map((r) => (
                    <option key={r.RunID} value={r.RunID}>
                      {r.NodeID} (Run {r.RunID.slice(0, 6)})
                    </option>
                  ))}
                </select>
              </label>
            ) : null}
          </div>
          <div className="flex shrink-0 items-center gap-1.5">
            <button
              type="button"
              onClick={() => void stepOnce(activeDebugRun?.RunID)}
              className="inline-flex h-6 items-center gap-1 rounded bg-amber-500 px-2.5 text-[11px] font-semibold text-white hover:bg-amber-600 cursor-pointer"
            >
              <StepForward className="h-3 w-3" />
              单步下一步 (F10)
            </button>
            <button
              type="button"
              onClick={() => void continueToBreakpoint()}
              className="inline-flex h-6 items-center gap-1 rounded bg-primary px-2.5 text-[11px] font-semibold text-primary-foreground hover:bg-primary/90 cursor-pointer"
            >
              <FastForward className="h-3 w-3" />
              {validBreakpoints.length > 0 ? '执行到下一断点 (F8)' : '继续运行 (F8)'}
            </button>
          </div>
        </div>
      ) : null}

      {/* 一体化画布与节点库工作区：同时支持自由编排、右侧节点属性编辑与运行染色叠加 */}
      <GraphWorkbench
        registry={activePack.registry}
        palette={<GraphPalette library={activePack.library} onAdd={addNode} />}
        paletteLabel="节点库"
        propsPanel={
          selectedEditorNode && showNodeProps ? (
            <GraphNodePropsPanel
              node={{
                id: selectedEditorNode.id,
                nodeType: selectedEditorNode.type,
                label: selectedEditorNode.name || selectedEditorNode.type,
                config: selectedEditorNode.config ?? {},
                inputs: selectedEditorNode.inputs,
              }}
              spec={selectedSpec}
              inputSources={selectedSources}
              endpointSides={
                selectedEndpoints
                  ? resolveEndpointSides(selectedEditorNode.endpointSides, endpointIds(selectedEndpoints))
                  : undefined
              }
              endpoints={selectedEndpoints}
              onEndpointSidesChange={(sides) => patchEndpointSides(selectedEditorNode.id, sides)}
              onChange={(patch) => {
                const actionLabel =
                  patch.label !== undefined
                    ? `重命名节点 · ${patch.label || selectedEditorNode.id}`
                    : patch.inputs !== undefined
                      ? `修改节点输入绑定 · ${selectedEditorNode.name || selectedEditorNode.id}`
                      : `修改节点配置 · ${selectedEditorNode.name || selectedEditorNode.id}`
                const coalesceField =
                  patch.label !== undefined ? 'label' : patch.inputs !== undefined ? 'inputs' : 'config'
                captureBefore({
                  label: actionLabel,
                  coalesceKey: `node-prop:${selectedEditorNode.id}:${coalesceField}`,
                })
                editor.setNodes((ns) =>
                  ns.map((n) =>
                    n.id === selectedEditorNode.id
                      ? {
                          ...n,
                          ...(patch.label !== undefined ? { name: patch.label } : {}),
                          ...(patch.config !== undefined ? { config: patch.config } : {}),
                          ...(patch.inputs !== undefined ? { inputs: patch.inputs } : {}),
                        }
                      : n,
                  ),
                )
                if (patch.config !== undefined) {
                  const outputs = resolveGraphNodeEndpoints(
                    selectedEditorNode.type,
                    selectedSpec?.Endpoints ?? activePack.registry.get(selectedEditorNode.type)?.displayEndpoints,
                    patch.config,
                    selectedSpec?.Renderer,
                  ).outputs
                  const stale = new Set(edgesOnRemovedOutputs(editor.edges, selectedEditorNode.id, outputs).map((e) => e.id))
                  if (stale.size > 0) editor.setEdges((es) => es.filter((e) => !stale.has(e.id)))
                }
              }}
              onDelete={() => {
                captureBefore(`删除节点 · ${selectedEditorNode.name || selectedEditorNode.id}`)
                editor.setNodes((ns) => ns.filter((n) => n.id !== selectedEditorNode.id))
                editor.setEdges((es) =>
                  es.filter((e) => e.from !== selectedEditorNode.id && e.to !== selectedEditorNode.id),
                )
                setSelectedNodeId(null)
                setShowNodeProps(false)
              }}
              onClose={() => setShowNodeProps(false)}
            />
          ) : undefined
        }
        canvas={{
          document: editor.document,
          nodePack: activePack,
          view,
          onChange: editor.load,
          onViewChange: editor.setView,
          onBeforeEdit: () => captureBefore(),
          canvas: graphEditorCanvasConfig,
          onNodeClick: (_e, node) => {
            setSelectedNodeId(node.id)
            setShowHistoryPopover(false)
            if (!isRunning && !isHung) {
              setShowNodeProps(true)
            }
          },
          onNodeDoubleClick: (_e, node) => {
            setSelectedNodeId(node.id)
            setShowHistoryPopover(false)
            setBottomTab('detail')
            setPanelOpen(true)
            toggleBreakpoint(node.id, node.type)
          },
          onPaneClick: () => {
            setSelectedNodeId(null)
            setShowNodeProps(false)
            setShowHistoryPopover(false)
            setShowPolicyPopover(false)
            setShowSchemaPopover(false)
          },
          className: 'h-full',
        }}
      />

      {/* 底部多页签运行面板：执行明细 & 在线调试 / 输入输出 / 执行列表 */}
      <section className="flex shrink-0 flex-col border-t border-border bg-card">
        {/* 页签导航栏 */}
        <div className="flex h-9 shrink-0 items-center justify-between border-b border-border/60 px-3 text-xs">
          <div className="flex h-full items-center gap-1">
            <button
              type="button"
              onClick={() => {
                setBottomTab('detail')
                setPanelOpen(true)
              }}
              className={`inline-flex h-full items-center gap-1.5 border-b-2 px-2.5 font-medium transition-colors cursor-pointer ${
                bottomTab === 'detail' && panelOpen
                  ? 'border-primary text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              }`}
            >
              <Activity className="h-3.5 w-3.5" />
              执行明细与调试
              {snap ? (
                <span className="rounded-full bg-muted px-1.5 py-0.2 font-mono text-[10px] text-muted-foreground">
                  {visitedNodeCount}/{totalNodeCount}
                </span>
              ) : null}
              {validBreakpoints.length > 0 ? (
                <span className="rounded-full bg-red-500/15 px-1.5 py-0.2 font-mono text-[10px] text-red-500">
                  断点×{validBreakpoints.length}
                </span>
              ) : null}
              {activeInjectCount > 0 ? (
                <span className="rounded-full bg-amber-500/15 px-1.5 py-0.2 font-mono text-[10px] text-amber-500">
                  注入×{activeInjectCount}
                </span>
              ) : null}
            </button>
            <button
              type="button"
              onClick={() => {
                setBottomTab('io')
                setPanelOpen(true)
              }}
              className={`inline-flex h-full items-center gap-1.5 border-b-2 px-2.5 font-medium transition-colors cursor-pointer ${
                bottomTab === 'io' && panelOpen
                  ? 'border-primary text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              }`}
            >
              <ArrowDownUp className="h-3.5 w-3.5" />
              输入输出
              {snap?.Task.Output ? (
                <span className="rounded-full bg-emerald-500/15 px-1.5 py-0.2 font-mono text-[10px] text-emerald-400">
                  {Object.keys(snap.Task.Output).length}
                </span>
              ) : null}
            </button>
            <button
              type="button"
              onClick={() => {
                setBottomTab('list')
                setPanelOpen(true)
              }}
              className={`inline-flex h-full items-center gap-1.5 border-b-2 px-2.5 font-medium transition-colors cursor-pointer ${
                bottomTab === 'list' && panelOpen
                  ? 'border-primary text-foreground'
                  : 'border-transparent text-muted-foreground hover:text-foreground'
              }`}
            >
              <History className="h-3.5 w-3.5" />
              执行列表
              <span className="rounded-full bg-muted px-1.5 py-0.2 font-mono text-[10px] text-muted-foreground">
                {taskList.length}
              </span>
            </button>
          </div>

          {/* 页签右侧辅助工具与折叠开关 */}
          <div className="flex items-center gap-2">
            {panelOpen && bottomTab === 'list' ? (
              <>
                <div className="flex items-center gap-1">
                  {(['', 'SUCCEEDED', 'FAILED', 'RUNNING', 'WAITING'] as const).map((st) => (
                    <button
                      key={st || 'ALL'}
                      type="button"
                      onClick={() => setStatusFilter(st)}
                      className={`rounded px-2 py-0.5 text-[11px] transition-colors cursor-pointer ${
                        statusFilter === st
                          ? 'bg-primary/15 font-medium text-primary'
                          : 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'
                      }`}
                    >
                      {st || '全部'}
                    </button>
                  ))}
                </div>
                <button
                  type="button"
                  onClick={refreshTaskList}
                  disabled={listLoading}
                  title="刷新执行列表"
                  className="inline-flex items-center gap-1 rounded px-2 py-0.5 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground disabled:opacity-50 cursor-pointer"
                >
                  <RefreshCw className={`h-3 w-3 ${listLoading ? 'animate-spin' : ''}`} />
                  刷新
                </button>
              </>
            ) : null}
            {panelOpen && bottomTab !== 'list' && snap?.Task.Input ? (
              <button
                type="button"
                onClick={() => fillInputsFromTask(snap.Task.Input)}
                title="将本次任务的运行输入填入上方输入框"
                className="inline-flex items-center gap-1 rounded px-2 py-0.5 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground cursor-pointer"
              >
                <RotateCcw className="h-3 w-3" />
                回填输入参数
              </button>
            ) : null}
            <button
              type="button"
              onClick={() => setPanelOpen((v) => !v)}
              title={panelOpen ? '收起面板' : '展开面板'}
              className="inline-flex items-center gap-1 rounded px-1.5 py-0.5 text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground cursor-pointer"
            >
              {panelOpen ? <ChevronDown className="h-3.5 w-3.5" /> : <ChevronUp className="h-3.5 w-3.5" />}
            </button>
          </div>
        </div>

        {/* 面板内容区（固定高度 h-56，保持画布视口稳定） */}
        {panelOpen ? (
          <div className="h-56 overflow-hidden">
            {/* Tab 1: 执行明细与在线调试（未运行时也可浏览节点、打断点、配置调试注入） */}
            {bottomTab === 'detail' ? (
              <div className="grid h-full grid-cols-12 divide-x divide-border">
                {/* 左栏：节点执行步骤列表（带 IDE 风格断点槽 Gutter，固定行高 32px 虚拟滚动） */}
                <div
                  ref={stepListRef}
                  onScroll={(e) => setStepScrollTop(e.currentTarget.scrollTop)}
                  className="col-span-4 flex h-full flex-col overflow-y-auto"
                >
                  {(() => {
                    const allNodes = activeGraph.Nodes ?? []
                    const ROW_H = 32
                    const startIdx = Math.max(0, Math.floor(stepScrollTop / ROW_H) - 6)
                    const endIdx = Math.min(allNodes.length, startIdx + 22)
                    const topPad = startIdx * ROW_H
                    const bottomPad = Math.max(0, (allNodes.length - endIdx) * ROW_H)
                    return (
                      <div style={{ paddingTop: topPad, paddingBottom: bottomPad }}>
                        {allNodes.slice(startIdx, endIdx).map((n) => {
                          const st = maps?.nodeStatus[n.ID]
                          const isSelected = activeDetailNode?.ID === n.ID
                          const nodeRuns = runsByNode[n.ID] ?? []
                          const maxNodeAttempt = nodeRuns.reduce((m, r) => Math.max(m, r.Attempt ?? 1), 1)
                          const outCount = Object.keys(mergedNodeOutputs[n.ID] ?? {}).length
                          const canBp = canSetBreakpointOnNode(n.ID, n.Kind)
                          const hasBp = breakpointSet.has(n.ID)
                          const hasInj = isDebugInjectActive(injects[n.ID])
                          const isDebugParked = debugWaitingNodeIds.has(n.ID)
                          return (
                            <div
                              key={n.ID}
                              onClick={() => setSelectedNodeId(n.ID)}
                              className={`group flex h-8 w-full items-center justify-between gap-2 border-b border-border/40 px-2.5 text-left text-xs transition-colors cursor-pointer ${
                                isSelected
                                  ? 'bg-primary/10 text-foreground'
                                  : isDebugParked
                                    ? 'bg-amber-500/10 text-foreground'
                                    : 'hover:bg-muted/40'
                              }`}
                            >
                              <div className="flex min-w-0 items-center gap-2">
                                {/* 左侧断点槽（Gutter）：活动节点可点击切换红点断点 */}
                                <button
                                  type="button"
                                  disabled={!canBp}
                                  onClick={(e) => {
                                    e.stopPropagation()
                                    toggleBreakpoint(n.ID, n.Kind)
                                  }}
                                  title={
                                    canBp
                                      ? hasBp
                                        ? `点击移除节点 ${n.ID} 的断点`
                                        : `点击在活动节点 ${n.ID} 上设置断点`
                                      : `内联节点（${shortKind(n.Kind)}）同步直通，不支持断点`
                                  }
                                  className={`flex h-4 w-4 shrink-0 items-center justify-center rounded-full transition-colors ${
                                    canBp ? 'hover:bg-red-500/20 cursor-pointer' : 'opacity-30 cursor-not-allowed'
                                  }`}
                                >
                                  <span
                                    className={`h-2.5 w-2.5 rounded-full transition-all ${
                                      hasBp
                                        ? 'bg-red-500 ring-2 ring-red-500/30 scale-100'
                                        : canBp
                                          ? 'bg-transparent ring-1 ring-muted-foreground/40 group-hover:bg-red-500/40 group-hover:ring-red-500/60'
                                          : 'bg-transparent ring-1 ring-muted-foreground/20'
                                    }`}
                                  />
                                </button>
                                {/* 运行状态指示点 */}
                                <span
                                  className={`h-2 w-2 shrink-0 rounded-full ${
                                    isDebugParked
                                      ? 'bg-amber-400 animate-ping'
                                      : st === 'succeeded'
                                        ? 'bg-emerald-400'
                                        : st === 'failed'
                                          ? 'bg-rose-400'
                                          : st === 'running'
                                            ? 'bg-indigo-400 animate-pulse'
                                            : 'bg-muted-foreground/30'
                                  }`}
                                />
                                <span className="truncate font-medium">{n.Name || n.ID}</span>
                                <span className="shrink-0 font-mono text-[11px] text-muted-foreground">{n.ID}</span>
                              </div>
                              <div className="flex shrink-0 items-center gap-1">
                                {isDebugParked ? (
                                  <span className="rounded bg-amber-500/20 px-1.5 py-0.5 font-mono text-[10px] font-semibold text-amber-500">
                                    ⏸ 挂起
                                  </span>
                                ) : null}
                                {hasInj ? (
                                  <span
                                    className="rounded bg-amber-500/15 px-1 py-0.5 font-mono text-[10px] text-amber-400"
                                    title="已配置调试注入"
                                  >
                                    注入
                                  </span>
                                ) : null}
                                <span className="rounded bg-muted/60 px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                                  {shortKind(n.Kind)}
                                </span>
                                {maxNodeAttempt > 1 ? (
                                  <span className="rounded bg-amber-500/15 px-1.5 py-0.5 font-mono text-[10px] text-amber-400">
                                    重试×{maxNodeAttempt}
                                  </span>
                                ) : null}
                                {outCount > 0 ? (
                                  <span className="font-mono text-[10px] text-emerald-400">{outCount}项</span>
                                ) : null}
                              </div>
                            </div>
                          )
                        })}
                      </div>
                    )
                  })()}
                </div>

                {/* 右栏：选中节点的输入、输出、在线断点/单步/重试与调试注入详情 */}
                <div className="col-span-8 flex h-full flex-col overflow-y-auto p-3 text-xs">
                  {activeDetailNode ? (
                    <div className="flex flex-col gap-3">
                      {/* 节点头与在线调试快捷操作栏 */}
                      <div className="flex flex-wrap items-center justify-between gap-2 border-b border-border/50 pb-2">
                        <div className="flex flex-wrap items-center gap-2">
                          <span className="font-semibold text-sm text-foreground">
                            {activeDetailNode.Name || activeDetailNode.ID}
                          </span>
                          <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                            ID: {activeDetailNode.ID}
                          </span>
                          <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[11px] text-muted-foreground">
                            类型: {shortKind(activeDetailNode.Kind)}
                          </span>
                        </div>
                        <div className="flex flex-wrap items-center gap-1.5">
                          {canSetBreakpointOnNode(activeDetailNode.ID, activeDetailNode.Kind) ? (
                            <button
                              type="button"
                              onClick={() => toggleBreakpoint(activeDetailNode.ID, activeDetailNode.Kind)}
                              className={`inline-flex h-6 items-center gap-1 rounded px-2 text-[11px] font-medium transition-colors cursor-pointer ${
                                breakpointSet.has(activeDetailNode.ID)
                                  ? 'bg-red-500/15 text-red-500 ring-1 ring-red-500/30'
                                  : 'bg-muted hover:bg-muted/80 text-foreground'
                              }`}
                            >
                              <CircleDot className="h-3 w-3 text-red-500" />
                              {breakpointSet.has(activeDetailNode.ID) ? '移除断点' : '打断点'}
                            </button>
                          ) : null}
                          {debugWaitingNodeIds.has(activeDetailNode.ID) ? (
                            <>
                              <button
                                type="button"
                                onClick={() => {
                                  const r = debugWaitingRuns.find((item) => item.NodeID === activeDetailNode.ID)
                                  void stepOnce(r?.RunID)
                                }}
                                className="inline-flex h-6 items-center gap-1 rounded bg-amber-500 px-2 text-[11px] font-semibold text-white hover:bg-amber-600 cursor-pointer"
                              >
                                <StepForward className="h-3 w-3" />
                                单步执行此节点
                              </button>
                              <button
                                type="button"
                                onClick={() => void continueToBreakpoint()}
                                className="inline-flex h-6 items-center gap-1 rounded bg-primary px-2 text-[11px] font-semibold text-primary-foreground hover:bg-primary/90 cursor-pointer"
                              >
                                <FastForward className="h-3 w-3" />
                                继续
                              </button>
                            </>
                          ) : null}
                          {maps?.nodeStatus[activeDetailNode.ID] === 'failed' ? (
                            <>
                              <button
                                type="button"
                                onClick={() => void retryTask()}
                                className="inline-flex h-6 items-center gap-1 rounded bg-primary px-2 text-[11px] font-semibold text-primary-foreground hover:bg-primary/90 cursor-pointer"
                              >
                                <RotateCcw className="h-3 w-3" />
                                重试任务
                              </button>
                              <button
                                type="button"
                                onClick={() => void retryTask({ overrideMaxAttempts: Math.max(3, maxAttempts) })}
                                className="inline-flex h-6 items-center gap-1 rounded border border-amber-500/40 bg-amber-500/10 px-2 text-[11px] font-medium text-amber-600 hover:bg-amber-500/20 dark:text-amber-300 cursor-pointer"
                              >
                                <RefreshCw className="h-3 w-3" />
                                开启3次自动重试并重跑
                              </button>
                            </>
                          ) : null}
                          <span
                            className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${statusBadgeClass(
                              debugWaitingNodeIds.has(activeDetailNode.ID)
                                ? 'WAITING'
                                : nodeStatusLabel(maps?.nodeStatus[activeDetailNode.ID]),
                            )}`}
                          >
                            {debugWaitingNodeIds.has(activeDetailNode.ID)
                              ? 'WAITING (DEBUG)'
                              : nodeStatusLabel(maps?.nodeStatus[activeDetailNode.ID])}
                          </span>
                        </div>
                      </div>

                      {/* 若该节点上有报错的 Run，醒目展示节点错误与重试次数 */}
                      {(runsByNode[activeDetailNode.ID] ?? [])
                        .filter((r) => r.Error)
                        .map((r) => (
                          <div
                            key={r.RunID}
                            className="flex items-center justify-between gap-2 rounded-ui border border-destructive/40 bg-destructive/10 px-2.5 py-1.5 font-mono text-[11px] text-destructive"
                          >
                            <span>
                              [Run {r.RunID.slice(0, 8)} · Attempt {r.Attempt ?? 1}/{snap?.Task.Policy?.MaxAttempts ?? maxAttempts}]{' '}
                              {r.Error}
                            </span>
                            <button
                              type="button"
                              onClick={() => void retryTask({ step: true })}
                              className="shrink-0 rounded border border-destructive/40 bg-background/80 px-2 py-0.5 text-[10px] font-medium text-destructive hover:bg-destructive/20 cursor-pointer"
                            >
                              单步重试
                            </button>
                          </div>
                        ))}

                      {/* 三栏或双列布局：节点输入 + 节点输出 + 节点在线调试注入（Mock / 故障模拟） */}
                      <div className="grid grid-cols-12 gap-3">
                        {/* 左列：节点输入 (4 列) */}
                        <div className="col-span-4 flex flex-col gap-1.5 rounded-ui border border-border/60 bg-background/60 p-2.5">
                          <div className="flex items-center justify-between text-[11px] font-medium text-muted-foreground">
                            <span>节点输入参数 ({activeNodeInputs.length})</span>
                          </div>
                          {activeNodeInputs.length === 0 ? (
                            <span className="text-[11px] text-muted-foreground/60">无输入参数</span>
                          ) : (
                            <div className="flex flex-col gap-1.5">
                              {activeNodeInputs.map((item) => (
                                <div
                                  key={item.name}
                                  className={`flex flex-col gap-0.5 rounded border px-2 py-1 ${
                                    item.injected
                                      ? 'border-amber-500/40 bg-amber-500/5'
                                      : 'border-border/40 bg-card/60'
                                  }`}
                                >
                                  <div className="flex items-center justify-between gap-1">
                                    <span className="font-mono font-medium text-foreground">{item.name}</span>
                                    <span
                                      className={`font-mono text-[10px] ${
                                        item.injected ? 'text-amber-500 font-semibold' : 'text-muted-foreground'
                                      }`}
                                    >
                                      {item.kind}: {item.source}
                                    </span>
                                  </div>
                                  <pre className="max-h-16 overflow-auto font-mono text-[11px] text-muted-foreground whitespace-pre-wrap break-all">
                                    {formatPreview(item.value)}
                                  </pre>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>

                        {/* 中列：节点输出 (4 列) */}
                        <div className="col-span-4 flex flex-col gap-1.5 rounded-ui border border-border/60 bg-background/60 p-2.5">
                          <div className="flex items-center justify-between text-[11px] font-medium text-muted-foreground">
                            <span>
                              节点输出结果 ({activeNodeOutput ? Object.keys(activeNodeOutput).length : 0})
                            </span>
                            {activeNodeOutput && Object.keys(activeNodeOutput).length > 0 ? (
                              <button
                                type="button"
                                onClick={() =>
                                  copyText(`node-${activeDetailNode.ID}`, JSON.stringify(activeNodeOutput, null, 2))
                                }
                                className="inline-flex items-center gap-1 text-[10px] text-muted-foreground hover:text-foreground cursor-pointer"
                              >
                                {copiedKey === `node-${activeDetailNode.ID}` ? (
                                  <Check className="h-3 w-3 text-emerald-400" />
                                ) : (
                                  <Copy className="h-3 w-3" />
                                )}
                                复制
                              </button>
                            ) : null}
                          </div>
                          {!activeNodeOutput ? (
                            <span className="text-[11px] text-muted-foreground/60">
                              {debugWaitingNodeIds.has(activeDetailNode.ID)
                                ? '当前已在此节点前挂起，点击「单步下一步」或「继续」执行'
                                : '该节点尚未产出输出'}
                            </span>
                          ) : Object.keys(activeNodeOutput).length === 0 ? (
                            <span className="text-[11px] text-emerald-400/90">
                              控制流节点已执行通过（无数据字段输出）
                            </span>
                          ) : (
                            <div className="flex flex-col gap-1.5">
                              {Object.entries(activeNodeOutput).map(([k, v]) => (
                                <div
                                  key={k}
                                  className="flex flex-col gap-0.5 rounded border border-border/40 bg-card/60 px-2 py-1"
                                >
                                  <span className="font-mono font-medium text-emerald-400">{k}</span>
                                  <pre className="max-h-16 overflow-auto font-mono text-[11px] text-foreground whitespace-pre-wrap break-all">
                                    {formatPreview(v)}
                                  </pre>
                                </div>
                              ))}
                            </div>
                          )}
                        </div>

                        {/* 右列：节点在线调试注入（Mock 输入变量 / 模拟故障报错） (4 列) */}
                        <div className="col-span-4 flex flex-col gap-2 rounded-ui border border-amber-500/30 bg-amber-500/[0.04] p-2.5">
                          <div className="flex items-center justify-between">
                            <span className="inline-flex items-center gap-1 text-[11px] font-semibold text-amber-600 dark:text-amber-300">
                              <Zap className="h-3.5 w-3.5" />
                              调试注入 (Debug Inject)
                            </span>
                            {isDebugInjectActive(injects[activeDetailNode.ID]) ? (
                              <button
                                type="button"
                                onClick={() => {
                                  updateNodeInject(activeDetailNode.ID, null)
                                  setInjectVarsDraft('')
                                  setInjectVarsError('')
                                }}
                                className="text-[10px] text-muted-foreground hover:text-destructive cursor-pointer"
                              >
                                清除注入
                              </button>
                            ) : null}
                          </div>

                          {/* 1. 强制此节点报错（模拟异常以验证重试或 Error 分支） */}
                          <label className="flex items-center gap-2 text-[11px] text-foreground cursor-pointer select-none">
                            <input
                              type="checkbox"
                              checked={Boolean(injects[activeDetailNode.ID]?.Failed)}
                              onChange={(e) => {
                                const cur = injects[activeDetailNode.ID] ?? {}
                                updateNodeInject(activeDetailNode.ID, {
                                  ...cur,
                                  Failed: e.target.checked,
                                  Error: e.target.checked ? cur.Error || `debug injected failure at ${activeDetailNode.ID}` : undefined,
                                })
                              }}
                              className="rounded border-input"
                            />
                            <span>强制此节点报错失败（测试重试 / 错误分支）</span>
                          </label>
                          {injects[activeDetailNode.ID]?.Failed ? (
                            <input
                              type="text"
                              value={injects[activeDetailNode.ID]?.Error ?? ''}
                              onChange={(e) => {
                                const cur = injects[activeDetailNode.ID] ?? {}
                                updateNodeInject(activeDetailNode.ID, {
                                  ...cur,
                                  Failed: true,
                                  Error: e.target.value,
                                })
                              }}
                              placeholder="自定义注入错误消息…"
                              className="h-7 w-full rounded border border-amber-500/40 bg-background px-2 font-mono text-[11px] text-foreground focus:outline-none"
                            />
                          ) : null}

                          {/* 2. 注入/覆盖节点输入变量 Vars (JSON) */}
                          <div className="flex flex-col gap-1">
                            <div className="flex items-center justify-between text-[10px] text-muted-foreground">
                              <span>覆盖输入变量 Vars (JSON 对象):</span>
                              {activeNodeInputs.length > 0 ? (
                                <button
                                  type="button"
                                  onClick={() => {
                                    const sample = Object.fromEntries(
                                      activeNodeInputs
                                        .filter((i) => i.name !== 'Outputs' && i.name !== 'Body' && i.name !== 'Cases')
                                        .map((i) => [i.name, i.value ?? null]),
                                    )
                                    const text = JSON.stringify(sample, null, 2)
                                    setInjectVarsDraft(text)
                                    setInjectVarsError('')
                                    const cur = injects[activeDetailNode.ID] ?? {}
                                    updateNodeInject(activeDetailNode.ID, { ...cur, Vars: sample })
                                  }}
                                  className="text-amber-600 hover:underline dark:text-amber-300 cursor-pointer"
                                >
                                  填入当前入参
                                </button>
                              ) : null}
                            </div>
                            <textarea
                              value={injectVarsDraft}
                              onChange={(e) => {
                                const raw = e.target.value
                                setInjectVarsDraft(raw)
                                const trimmed = raw.trim()
                                if (!trimmed) {
                                  setInjectVarsError('')
                                  const cur = injects[activeDetailNode.ID] ?? {}
                                  updateNodeInject(activeDetailNode.ID, { ...cur, Vars: undefined })
                                  return
                                }
                                try {
                                  const parsed = JSON.parse(trimmed)
                                  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
                                    setInjectVarsError('Vars 必须是 JSON 对象，如 {"a": 10}')
                                    return
                                  }
                                  setInjectVarsError('')
                                  const cur = injects[activeDetailNode.ID] ?? {}
                                  updateNodeInject(activeDetailNode.ID, {
                                    ...cur,
                                    Vars: parsed as Record<string, unknown>,
                                  })
                                } catch {
                                  setInjectVarsError('JSON 格式未完成或有误')
                                }
                              }}
                              rows={2}
                              placeholder='如 {"a": 100, "score": 90}'
                              className="w-full rounded border border-input bg-background p-1.5 font-mono text-[11px] text-foreground focus:border-amber-500 focus:outline-none"
                            />
                            {injectVarsError ? (
                              <span className="text-[10px] text-destructive">{injectVarsError}</span>
                            ) : null}
                          </div>
                        </div>
                      </div>
                    </div>
                  ) : null}
                </div>
              </div>
            ) : null}

            {/* Tab 2: 输入输出（Task.Input 与 Task.Output 完整对照） */}
            {bottomTab === 'io' ? (
              !snap ? (
                <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
                  暂无运行记录，请点击「开始运行」或在「执行列表」中选择一条任务。
                </div>
              ) : (
                <div className="grid h-full grid-cols-2 divide-x divide-border">
                  {/* 左栏：Task.Input */}
                  <div className="flex h-full flex-col overflow-hidden p-3 text-xs">
                    <div className="mb-2 flex shrink-0 items-center justify-between">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-foreground">运行输入 (Task.Input)</span>
                        <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                          {Object.keys(snap.Task.Input ?? {}).length} 个字段
                        </span>
                      </div>
                      <button
                        type="button"
                        onClick={() => copyText('task-input', JSON.stringify(snap.Task.Input ?? {}, null, 2))}
                        className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground cursor-pointer"
                      >
                        {copiedKey === 'task-input' ? (
                          <Check className="h-3 w-3 text-emerald-400" />
                        ) : (
                          <Copy className="h-3 w-3" />
                        )}
                        复制 JSON
                      </button>
                    </div>
                    <pre className="flex-1 overflow-auto rounded-ui border border-border/60 bg-background/60 p-2.5 font-mono text-xs text-foreground">
                      {JSON.stringify(snap.Task.Input ?? {}, null, 2)}
                    </pre>
                  </div>

                  {/* 右栏：Task.Output & Task.Error */}
                  <div className="flex h-full flex-col overflow-hidden p-3 text-xs">
                    <div className="mb-2 flex shrink-0 items-center justify-between">
                      <div className="flex items-center gap-2">
                        <span className="font-semibold text-foreground">运行输出 (Task.Output)</span>
                        <span className="rounded bg-muted px-1.5 py-0.5 font-mono text-[10px] text-muted-foreground">
                          {Object.keys(snap.Task.Output ?? {}).length} 个字段
                        </span>
                      </div>
                      {snap.Task.Output ? (
                        <button
                          type="button"
                          onClick={() => copyText('task-output', JSON.stringify(snap.Task.Output ?? {}, null, 2))}
                          className="inline-flex items-center gap-1 text-[11px] text-muted-foreground hover:text-foreground cursor-pointer"
                        >
                          {copiedKey === 'task-output' ? (
                            <Check className="h-3 w-3 text-emerald-400" />
                          ) : (
                            <Copy className="h-3 w-3" />
                          )}
                          复制 JSON
                        </button>
                      ) : null}
                    </div>
                    {snap.Task.Error ? (
                      <div className="mb-2 shrink-0 rounded-ui border border-destructive/40 bg-destructive/10 px-2.5 py-1.5 font-mono text-xs text-destructive">
                        {snap.Task.Error}
                      </div>
                    ) : null}
                    <pre className="flex-1 overflow-auto rounded-ui border border-border/60 bg-background/60 p-2.5 font-mono text-xs text-foreground">
                      {snap.Task.Output
                        ? JSON.stringify(snap.Task.Output, null, 2)
                        : snap.Task.Status === 'RUNNING'
                          ? '// 任务正在运行中…'
                          : '// 暂无输出'}
                    </pre>
                  </div>
                </div>
              )
            ) : null}

            {/* Tab 3: 执行列表（历史 Task 列表，点击回放轨迹） */}
            {bottomTab === 'list' ? (
              <div className="h-full overflow-y-auto">
                {taskList.length === 0 ? (
                  <div className="flex h-full items-center justify-center text-xs text-muted-foreground">
                    {listLoading ? '正在加载执行记录…' : '当前工作流暂无历史执行记录'}
                  </div>
                ) : (
                  <table className="w-full border-collapse text-left text-xs">
                    <thead className="sticky top-0 z-10 border-b border-border bg-card text-[11px] text-muted-foreground">
                      <tr>
                        <th className="px-3 py-1.5 font-medium">状态</th>
                        <th className="px-3 py-1.5 font-medium">Task ID</th>
                        <th className="px-3 py-1.5 font-medium">引擎 / 版本</th>
                        <th className="px-3 py-1.5 font-medium">开始时间</th>
                        <th className="px-3 py-1.5 font-medium">耗时</th>
                        <th className="px-3 py-1.5 font-medium">错误信息</th>
                        <th className="px-3 py-1.5 text-right font-medium">操作</th>
                      </tr>
                    </thead>
                    <tbody className="divide-y divide-border/40">
                      {taskList.map((row) => {
                        const active = snap?.Task.TaskID === row.TaskID
                        return (
                          <tr
                            key={row.TaskID}
                            onClick={() => selectHistoryTask(row)}
                            className={`transition-colors cursor-pointer ${
                              active ? 'bg-primary/10' : 'hover:bg-muted/40'
                            }`}
                          >
                            <td className="px-3 py-1.5 whitespace-nowrap">
                              <span
                                className={`inline-block rounded-full px-2 py-0.5 text-[10px] font-medium ${statusBadgeClass(
                                  row.Status,
                                )}`}
                              >
                                {row.Status}
                              </span>
                            </td>
                            <td className="px-3 py-1.5 font-mono text-[11px] whitespace-nowrap" title={row.TaskID}>
                              {row.TaskID.slice(0, 12)}…
                              {active ? (
                                <span className="ml-1.5 rounded bg-primary/20 px-1.5 py-0.2 text-[10px] text-primary">
                                  当前
                                </span>
                              ) : null}
                            </td>
                            <td className="px-3 py-1.5 font-mono text-[11px] text-muted-foreground whitespace-nowrap">
                              {row.EngineKind} · v{row.GraphVersion}
                            </td>
                            <td className="px-3 py-1.5 font-mono text-[11px] text-muted-foreground whitespace-nowrap">
                              <span className="inline-flex items-center gap-1">
                                <Clock className="h-3 w-3" />
                                {formatTime(row.StartedAt)}
                              </span>
                            </td>
                            <td className="px-3 py-1.5 font-mono text-[11px] text-muted-foreground whitespace-nowrap">
                              {formatDuration(row.StartedAt, row.FinishedAt)}
                            </td>
                            <td
                              className="max-w-xs truncate px-3 py-1.5 font-mono text-[11px] text-destructive"
                              title={row.Error}
                            >
                              {row.Error || <span className="text-muted-foreground/40">-</span>}
                            </td>
                            <td className="px-3 py-1.5 text-right whitespace-nowrap">
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.stopPropagation()
                                  selectHistoryTask(row)
                                  setBottomTab('detail')
                                }}
                                className="rounded px-2 py-0.5 text-[11px] font-medium text-primary hover:bg-primary/15 cursor-pointer"
                              >
                                查看明细
                              </button>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                )}
              </div>
            ) : null}
          </div>
        ) : null}
      </section>
    </div>
  )
}
