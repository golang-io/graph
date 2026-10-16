/**
 * useGraphHistory — 流程图编辑历史与前进/后退（Undo / Redo / Timeline Jump）。
 *
 * 核心特性：
 *   1. 纯浏览器前端存储（localStorage + 内存缓存兜底），按 GraphID 隔离保存完整历史操作栈，无需后端存储；
 *   2. 自动对比前后快照推导中文操作描述（如「添加节点 · 表达式」「移动节点 · 入学问候」「连接连线」「旋转端点方位」）；
 *   3. 自动过滤未发生实质变化的空点击（如仅单击选中节点未拖动），不会误清空前进（Redo）栈或产生空操作记录；
 *   4. 支持连续输入合并（coalesceKey）与任意历史版本一键跳转（jumpTo）。
 *
 * Browser-persisted flowchart edit history (Undo / Redo / Timeline Jump) with automatic diff labels.
 */
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { GraphNodeJSON, GraphUI } from './types'

const DEFAULT_LIMIT = 50
// v2：运行输入 Schema 强制带 type 后作废旧历史，避免 localStorage 里无 type / 错 type 的快照盖住服务端图。
const STORAGE_PREFIX = 'graph-ui.history.v2:'
const COALESCE_WINDOW_MS = 800

/** 单条流程图编辑历史记录。 */
export type GraphHistoryEntry = {
  /** 历史步骤唯一 ID */
  id: string
  /** 操作语义名称（如「初始版本」「添加节点 · 表达式」「移动节点 · 入学问候」） */
  label: string
  /** 记录时间戳（Unix 毫秒） */
  timestamp: number
  /** 该版本的顶层节点数 */
  nodeCount: number
  /** 该版本的顶层连线数 */
  edgeCount: number
  /** 画布文档快照（Graph + UI） */
  snapshot: GraphUI
}

/** 持久化到浏览器 localStorage 的历史记录结构。 */
export type StoredGraphHistory = {
  entries: GraphHistoryEntry[]
  index: number
  updatedAt: number
}

export type GraphHistoryPushOptions = {
  /** 显式指定本次编辑的操作名称；缺省时由 describeGraphDiff 自动对比前后快照生成 */
  label?: string
  /** 连续编辑合并键（如修改同一节点文本属性时传入同一 key，800ms 内的连续按键合并为一步历史） */
  coalesceKey?: string
}

export type GraphHistoryOptions = {
  /**
   * 浏览器本地存储键（通常传 GraphID，如 "demo_tour"）。
   * 提供时自动将编辑历史栈持久化到浏览器 localStorage，刷新页面或切换图后自动恢复。
   */
  storageKey?: string
  /** 最大保留的历史步数（默认 50） */
  limit?: number
  /**
   * 当前画布文档（可选）：传入时自动监听文档变化并生成带语义标签的历史记录，
   * 自动过滤未发生实质变化的点击。
   */
  currentDocument?: GraphUI
}

/** 会话内存级后备存储：即使在无 localStorage 环境（SSR / 单测）或超大画布超出配额时也能完整保留。 */
const memoryStore = new Map<string, StoredGraphHistory>()

let seqCounter = 0
function nextEntryId(): string {
  seqCounter += 1
  return `h_${Date.now().toString(36)}_${seqCounter.toString(36)}`
}

function shortKind(kind?: string): string {
  return (kind ?? '').replace(/^__INNER__\./, '')
}

function nodeTitle(n: GraphNodeJSON): string {
  return n.Name ? `${n.Name} (${n.ID})` : `${shortKind(n.Kind) || n.ID} (${n.ID})`
}

/** 判断两份画布快照在业务与布局上是否完全相同。 */
export function sameGraphSnapshot(a?: GraphUI | null, b?: GraphUI | null): boolean {
  if (a === b) return true
  if (!a || !b) return false
  const ag = a.Graph, bg = b.Graph
  const sameGraph =
    ag === bg ||
    (ag?.Nodes === bg?.Nodes &&
      ag?.Edges === bg?.Edges &&
      (ag?.InputSchema === bg?.InputSchema || JSON.stringify(ag?.InputSchema) === JSON.stringify(bg?.InputSchema))) ||
    JSON.stringify(ag) === JSON.stringify(bg)
  if (!sameGraph) return false
  return a.UI === b.UI || JSON.stringify(a.UI ?? {}) === JSON.stringify(b.UI ?? {})
}

/** 构造一条标准化的历史记录项。 */
export function createHistoryEntry(
  document: GraphUI,
  label: string,
  id?: string,
  timestamp?: number,
): GraphHistoryEntry {
  const snap = structuredClone(document)
  return {
    id: id ?? nextEntryId(),
    label: label.trim() || '编辑流程图',
    timestamp: timestamp ?? Date.now(),
    nodeCount: snap.Graph?.Nodes?.length ?? 0,
    edgeCount: snap.Graph?.Edges?.length ?? 0,
    snapshot: snap,
  }
}

/**
 * 自动对比两个画布快照，生成可读的中文编辑操作描述。
 * Infers a human-readable Chinese operation description from two GraphUI snapshots.
 */
export function describeGraphDiff(before: GraphUI, after: GraphUI): string {
  const bNodes = before.Graph?.Nodes ?? []
  const aNodes = after.Graph?.Nodes ?? []
  const sameNodesRef = before.Graph === after.Graph || bNodes === aNodes
  const bEdges = before.Graph?.Edges ?? []
  const aEdges = after.Graph?.Edges ?? []
  const sameEdgesRef = before.Graph === after.Graph || bEdges === aEdges

  let bNodeMap: Map<string, GraphNodeJSON> | undefined
  let aNodeMap: Map<string, GraphNodeJSON> | undefined
  const getANode = (id: string) => {
    aNodeMap ??= new Map(aNodes.map((n) => [n.ID, n]))
    return aNodeMap.get(id)
  }

  // 1. 顶层节点增删（Nodes 引用相同时直接跳过）
  if (!sameNodesRef) {
    bNodeMap = new Map(bNodes.map((n) => [n.ID, n]))
    aNodeMap = new Map(aNodes.map((n) => [n.ID, n]))
    const addedNodes = aNodes.filter((n) => !bNodeMap!.has(n.ID))
    const removedNodes = bNodes.filter((n) => !aNodeMap!.has(n.ID))
    if (addedNodes.length === 1 && removedNodes.length === 0) {
      return `添加节点 · ${nodeTitle(addedNodes[0]!)}`
    }
    if (addedNodes.length > 1 && removedNodes.length === 0) {
      return `添加 ${addedNodes.length} 个节点`
    }
    if (removedNodes.length === 1 && addedNodes.length === 0) {
      return `删除节点 · ${nodeTitle(removedNodes[0]!)}`
    }
    if (removedNodes.length > 1 && addedNodes.length === 0) {
      return `删除 ${removedNodes.length} 个节点`
    }
  }

  // 2. 顶层连线增删（Edges 引用相同时直接跳过）
  if (!sameEdgesRef) {
    const bEdgeMap = new Map(bEdges.map((e) => [e.ID, e]))
    const aEdgeMap = new Map(aEdges.map((e) => [e.ID, e]))
    const addedEdges = aEdges.filter((e) => !bEdgeMap.has(e.ID))
    const removedEdges = bEdges.filter((e) => !aEdgeMap.has(e.ID))
    if (addedEdges.length === 1 && removedEdges.length === 0) {
      return `连接连线 · ${addedEdges[0]!.From} → ${addedEdges[0]!.To}`
    }
    if (addedEdges.length > 1 && removedEdges.length === 0) {
      return `添加 ${addedEdges.length} 条连线`
    }
    if (removedEdges.length === 1 && addedEdges.length === 0) {
      return `删除连线 · ${removedEdges[0]!.From} → ${removedEdges[0]!.To}`
    }
    if (removedEdges.length > 1 && addedEdges.length === 0) {
      return `删除 ${removedEdges.length} 条连线`
    }
  }

  // 3. 便签注释框（StickyNotes）增删改
  const bNotes = before.UI?.StickyNotes ?? []
  const aNotes = after.UI?.StickyNotes ?? []
  if (bNotes !== aNotes) {
    if (aNotes.length > bNotes.length) {
      const added = aNotes.find((n) => !bNotes.some((b) => b.ID === n.ID))
      return `添加便签 · ${added?.Title || added?.ID || '便签'}`
    }
    if (aNotes.length < bNotes.length) {
      const removed = bNotes.find((n) => !aNotes.some((a) => a.ID === n.ID))
      return `删除便签 · ${removed?.Title || removed?.ID || '便签'}`
    }
    for (let i = 0; i < aNotes.length; i++) {
      if (aNotes[i] !== bNotes[i] && JSON.stringify(aNotes[i]) !== JSON.stringify(bNotes[i])) {
        return `编辑便签 · ${aNotes[i]!.Title || aNotes[i]!.ID}`
      }
    }
  }

  // 4. 容器（Map 循环体）展开 / 收起状态切换
  const bSubs = before.UI?.SubLayouts ?? {}
  const aSubs = after.UI?.SubLayouts ?? {}
  const subKeys = new Set([...Object.keys(bSubs), ...Object.keys(aSubs)])
  for (const k of subKeys) {
    const bCol = Boolean(bSubs[k]?.Collapsed)
    const aCol = Boolean(aSubs[k]?.Collapsed)
    if (bCol !== aCol) {
      const n = getANode(k)
      return `${aCol ? '收起循环体' : '展开循环体'} · ${n ? nodeTitle(n) : k}`
    }
  }

  // 5. 节点名称或输入参数配置修改（含循环体内部增删改；Nodes 引用相同时跳过）
  if (!sameNodesRef) {
    bNodeMap ??= new Map(bNodes.map((n) => [n.ID, n]))
    for (const n of aNodes) {
      const prev = bNodeMap.get(n.ID)
      if (!prev || prev === n) continue
      if ((prev.Name ?? '') !== (n.Name ?? '')) {
        return `重命名节点 · ${nodeTitle(n)}`
      }
      if (prev.Inputs !== n.Inputs && JSON.stringify(prev.Inputs ?? {}) !== JSON.stringify(n.Inputs ?? {})) {
        if (JSON.stringify(prev.Inputs?.Body ?? null) !== JSON.stringify(n.Inputs?.Body ?? null)) {
          return `编辑循环体 · ${nodeTitle(n)}`
        }
        return `修改节点配置 · ${nodeTitle(n)}`
      }
    }
  }

  // 6. 运行输入参数 Schema 修改
  if (
    before.Graph !== after.Graph &&
    before.Graph?.InputSchema !== after.Graph?.InputSchema &&
    JSON.stringify(before.Graph?.InputSchema ?? null) !== JSON.stringify(after.Graph?.InputSchema ?? null)
  ) {
    return '修改运行参数定义'
  }

  // 7. 节点端点方位旋转 / 调整
  const bSides = before.UI?.EndpointSides ?? {}
  const aSides = after.UI?.EndpointSides ?? {}
  if (bSides !== aSides) {
    for (const n of aNodes) {
      if (bSides[n.ID] !== aSides[n.ID] && JSON.stringify(bSides[n.ID] ?? {}) !== JSON.stringify(aSides[n.ID] ?? {})) {
        return `旋转端点方位 · ${nodeTitle(n)}`
      }
    }
  }

  // 8. 节点位置移动（主图或循环体内）
  const bPos = before.UI?.Positions ?? {}
  const aPos = after.UI?.Positions ?? {}
  if (bPos !== aPos) {
    const movedNodes: GraphNodeJSON[] = []
    for (const n of aNodes) {
      const bp = bPos[n.ID]
      const ap = aPos[n.ID]
      if ((bp?.X ?? 0) !== (ap?.X ?? 0) || (bp?.Y ?? 0) !== (ap?.Y ?? 0)) {
        movedNodes.push(n)
      }
    }
    if (movedNodes.length === 1) {
      return `移动节点 · ${nodeTitle(movedNodes[0]!)}`
    }
    if (movedNodes.length > 1) {
      return `移动 ${movedNodes.length} 个节点`
    }
  }

  for (const k of subKeys) {
    if (
      bSubs[k]?.Positions !== aSubs[k]?.Positions &&
      JSON.stringify(bSubs[k]?.Positions ?? {}) !== JSON.stringify(aSubs[k]?.Positions ?? {})
    ) {
      const n = getANode(k)
      return `移动循环体节点 · ${n ? nodeTitle(n) : k}`
    }
  }

  return '编辑流程图'
}

/** 从浏览器前端存储（localStorage 优先，内存兜底）读取指定图的历史栈。 */
export function loadStoredGraphHistory(storageKey?: string): StoredGraphHistory | null {
  if (!storageKey) return null
  const key = STORAGE_PREFIX + storageKey
  if (typeof window !== 'undefined' && typeof localStorage !== 'undefined') {
    try {
      const raw = localStorage.getItem(key)
      if (raw) {
        const parsed = JSON.parse(raw) as StoredGraphHistory
        if (
          parsed &&
          Array.isArray(parsed.entries) &&
          parsed.entries.length > 0 &&
          typeof parsed.index === 'number' &&
          parsed.index >= 0 &&
          parsed.index < parsed.entries.length
        ) {
          memoryStore.set(key, parsed)
          return parsed
        }
      }
    } catch {
      // JSON 损坏或存储不可用时回退到内存
    }
  }
  const mem = memoryStore.get(key)
  if (mem && mem.entries.length > 0) {
    return {
      entries: structuredClone(mem.entries),
      index: mem.index,
      updatedAt: mem.updatedAt,
    }
  }
  return null
}

/**
 * 将编辑历史栈写入浏览器前端存储（localStorage + 内存缓存）。
 * 若因 1000 节点大图导致超出浏览器 localStorage 5MB 配额，自动裁剪较早的持久化快照以保证最新历史成功落盘。
 */
export function saveStoredGraphHistory(storageKey: string | undefined, state: StoredGraphHistory): void {
  if (!storageKey || state.entries.length === 0) return
  const key = STORAGE_PREFIX + storageKey
  // 每个 GraphHistoryEntry 的 snapshot 在 createHistoryEntry 时已独立深拷贝，这里仅浅拷贝条目列表即可，
  // 避免每次持久化时对最多 50 份千节点大图快照重复执行 structuredClone。
  const copy: StoredGraphHistory = {
    entries: state.entries.map((e) => ({ ...e })),
    index: state.index,
    updatedAt: state.updatedAt,
  }
  memoryStore.set(key, copy)

  if (typeof window === 'undefined' || typeof localStorage === 'undefined') return
  // 优先尝试完整写入；若触发 QuotaExceededError 则逐步裁剪最早的快照重试
  let entriesToSave = copy.entries
  let indexToSave = copy.index
  while (entriesToSave.length > 0) {
    try {
      localStorage.setItem(
        key,
        JSON.stringify({
          entries: entriesToSave,
          index: indexToSave,
          updatedAt: copy.updatedAt,
        }),
      )
      return
    } catch {
      if (entriesToSave.length <= 2) return
      // 优先裁掉最旧的历史步骤，同时修正游标 indexToSave
      if (indexToSave > 0) {
        entriesToSave = entriesToSave.slice(1)
        indexToSave -= 1
      } else {
        entriesToSave = entriesToSave.slice(0, -1)
      }
    }
  }
}

/** 清除指定图在浏览器前端存储中的编辑历史。 */
export function clearStoredGraphHistory(storageKey?: string): void {
  if (!storageKey) return
  const key = STORAGE_PREFIX + storageKey
  memoryStore.delete(key)
  if (typeof window !== 'undefined' && typeof localStorage !== 'undefined') {
    try {
      localStorage.removeItem(key)
    } catch {
      // ignore
    }
  }
}

function normalizePushOptions(labelOrOpts?: string | GraphHistoryPushOptions): GraphHistoryPushOptions {
  if (!labelOrOpts) return {}
  if (typeof labelOrOpts === 'string') return { label: labelOrOpts }
  return labelOrOpts
}

export function useGraphHistory(limitOrOptions: number | GraphHistoryOptions = DEFAULT_LIMIT) {
  const opts: GraphHistoryOptions =
    typeof limitOrOptions === 'number' ? { limit: limitOrOptions } : limitOrOptions
  const limit = Math.max(2, opts.limit ?? DEFAULT_LIMIT)
  const storageKey = opts.storageKey
  const currentDocument = opts.currentDocument

  // 初始化：优先从浏览器 localStorage 恢复该图的编辑历史栈；无历史时以 currentDocument 作为「初始版本」
  const initialRef = useRef<{
    past: GraphHistoryEntry[]
    current: GraphHistoryEntry | null
    future: GraphHistoryEntry[]
  } | null>(null)

  if (initialRef.current === null) {
    const stored = loadStoredGraphHistory(storageKey)
    if (stored && stored.entries.length > 0) {
      const idx = Math.min(Math.max(0, stored.index), stored.entries.length - 1)
      initialRef.current = {
        past: stored.entries.slice(0, idx),
        current: stored.entries[idx]!,
        future: stored.entries.slice(idx + 1).reverse(),
      }
    } else if (currentDocument) {
      initialRef.current = {
        past: [],
        current: createHistoryEntry(currentDocument, '初始版本'),
        future: [],
      }
    } else {
      initialRef.current = {
        past: [],
        current: null,
        future: [],
      }
    }
  }

  const pastRef = useRef<GraphHistoryEntry[]>(initialRef.current.past)
  const currentRef = useRef<GraphHistoryEntry | null>(initialRef.current.current)
  // futureRef 按栈存储：栈顶（futureRef.current[length - 1]）是紧接着要前进（Redo）的下一步
  const futureRef = useRef<GraphHistoryEntry[]>(initialRef.current.future)

  // stagedRef 保存在 onBeforeEdit / push 时记录的突变前快照；只有当文档实际变化时才真正入栈，
  // 从而避免单击节点未移动就误清空 Redo 栈或产生重复快照。
  const stagedRef = useRef<{
    snapshot: GraphUI
    label?: string
    coalesceKey?: string
    at: number
  } | null>(null)

  const lastCoalesceRef = useRef<{ key: string; at: number }>({ key: '', at: 0 })
  const restoringRef = useRef(false)
  const lastSeenDocRef = useRef<GraphUI | undefined>(currentDocument)
  // 仅当历史栈内容实际发生变化时递增版本号，驱动防抖持久化 effect，避免宿主组件因选中、运行态或面板切换重渲染时反复触发全栈序列化
  const historyVersionRef = useRef(initialRef.current.current ? 1 : 0)
  const [, setTick] = useState(0)
  const bump = useCallback(() => setTick((n) => n + 1), [])

  /** 将当前 past + current + future 组装为有序时间线并持久化到浏览器 localStorage。 */
  const persistNow = useCallback(() => {
    if (!storageKey) return
    const cur =
      currentRef.current ??
      (currentDocument ? createHistoryEntry(currentDocument, '初始版本') : null)
    if (!cur) return
    const chronologicalFuture = [...futureRef.current].reverse()
    const allEntries = [...pastRef.current, cur, ...chronologicalFuture]
    const curIdx = pastRef.current.length
    saveStoredGraphHistory(storageKey, {
      entries: allEntries,
      index: curIdx,
      updatedAt: Date.now(),
    })
  }, [storageKey, currentDocument])

  // 当传入了 currentDocument 时，在渲染阶段同步检测实质编辑并生成历史条目
  if (currentDocument !== undefined && currentDocument !== lastSeenDocRef.current) {
    lastSeenDocRef.current = currentDocument
    if (!restoringRef.current) {
      if (!currentRef.current) {
        currentRef.current = createHistoryEntry(currentDocument, '初始版本')
        historyVersionRef.current += 1
      } else if (!sameGraphSnapshot(currentRef.current.snapshot, currentDocument)) {
        historyVersionRef.current += 1
        const staged = stagedRef.current
        stagedRef.current = null
        const now = Date.now()
        const coalesceKey = staged?.coalesceKey ?? ''
        const canCoalesce =
          Boolean(coalesceKey) &&
          coalesceKey === lastCoalesceRef.current.key &&
          now - lastCoalesceRef.current.at < COALESCE_WINDOW_MS &&
          pastRef.current.length > 0

        if (canCoalesce) {
          // 800ms 内同一属性的连续输入合并到当前步骤，描述基于上一步快照计算
          const prevSnap = pastRef.current[pastRef.current.length - 1]!.snapshot
          const label = staged?.label || describeGraphDiff(prevSnap, currentDocument)
          currentRef.current = createHistoryEntry(
            currentDocument,
            label,
            currentRef.current.id,
            now,
          )
          lastCoalesceRef.current = { key: coalesceKey, at: now }
        } else if (staged) {
          // 由 push / onBeforeEdit 触发的正式编辑：将突变前状态入栈 past，清空 future
          const baseSnap = staged.snapshot
          if (!sameGraphSnapshot(currentRef.current.snapshot, baseSnap)) {
            currentRef.current = createHistoryEntry(
              baseSnap,
              currentRef.current.label,
              currentRef.current.id,
              currentRef.current.timestamp,
            )
          }
          pastRef.current = [...pastRef.current, currentRef.current].slice(-(limit - 1))
          futureRef.current = []
          const label = staged.label || describeGraphDiff(baseSnap, currentDocument)
          currentRef.current = createHistoryEntry(currentDocument, label, undefined, now)
          lastCoalesceRef.current = { key: coalesceKey, at: now }
        } else {
          // 拖拽中间帧或保存后规范化写回：若已有 past 则就地更新当前步骤快照与描述
          if (pastRef.current.length > 0) {
            const prevSnap = pastRef.current[pastRef.current.length - 1]!.snapshot
            const label = describeGraphDiff(prevSnap, currentDocument)
            currentRef.current = createHistoryEntry(
              currentDocument,
              label,
              currentRef.current.id,
              currentRef.current.timestamp,
            )
          } else {
            currentRef.current = createHistoryEntry(
              currentDocument,
              currentRef.current.label,
              currentRef.current.id,
              currentRef.current.timestamp,
            )
          }
        }
      }
    }
  }

  // 仅当历史栈版本实际变化时防抖写入 localStorage，避免无关重渲染或拖拽途中触发全栈序列化阻塞主线程
  const historyVersion = historyVersionRef.current
  useEffect(() => {
    if (!storageKey || historyVersion === 0) return
    const timer = setTimeout(() => {
      persistNow()
    }, 180)
    return () => clearTimeout(timer)
  }, [storageKey, historyVersion, persistNow])

  const isRestoring = useCallback(() => restoringRef.current, [])

  /** 重置/清空当前图的历史记录（同步清除浏览器 localStorage 中的历史栈）。 */
  const reset = useCallback(
    (baseDocument?: GraphUI) => {
      const base = baseDocument ?? currentDocument
      pastRef.current = []
      futureRef.current = []
      stagedRef.current = null
      restoringRef.current = false
      lastCoalesceRef.current = { key: '', at: 0 }
      currentRef.current = base ? createHistoryEntry(base, '初始版本') : null
      clearStoredGraphHistory(storageKey)
      if (base && storageKey) {
        saveStoredGraphHistory(storageKey, {
          entries: [currentRef.current!],
          index: 0,
          updatedAt: Date.now(),
        })
      }
      bump()
    },
    [bump, currentDocument, storageKey],
  )

  /**
   * 在修改前记录当前图快照：
   * - 当使用受控 currentDocument 模式时，先暂存到 stagedRef，待文档真正发生变化时才入栈，避免空点击污染历史；
   * - 当未传 currentDocument（传统命令式调用）时，立即压入 pastRef（自动去重连续相同快照）。
   */
  const push = useCallback(
    (document: GraphUI, labelOrOpts?: string | GraphHistoryPushOptions) => {
      if (restoringRef.current) return
      const pushOpts = normalizePushOptions(labelOrOpts)
      if (currentDocument !== undefined) {
        // 若上一次 staged 已经发生了未同步的变化，先将其提交
        if (
          stagedRef.current &&
          currentRef.current &&
          !sameGraphSnapshot(stagedRef.current.snapshot, document)
        ) {
          const prevBase = stagedRef.current.snapshot
          pastRef.current = [...pastRef.current, currentRef.current].slice(-(limit - 1))
          futureRef.current = []
          const prevLabel = stagedRef.current.label || describeGraphDiff(prevBase, document)
          currentRef.current = createHistoryEntry(document, prevLabel)
        }
        // 画布文档在 React 状态链路中不可变，直接暂存引用即可，避免在鼠标按下起拖第 1 帧或空点击时执行全图 structuredClone
        stagedRef.current = {
          snapshot: document,
          label: pushOpts.label,
          coalesceKey: pushOpts.coalesceKey,
          at: Date.now(),
        }
        return
      }

      // 传统命令式模式（未传 currentDocument）
      const top = pastRef.current[pastRef.current.length - 1]
      if (top && sameGraphSnapshot(top.snapshot, document)) {
        if (pushOpts.label) top.label = pushOpts.label
        return
      }
      const entry = createHistoryEntry(
        document,
        pushOpts.label || (pastRef.current.length === 0 ? '初始版本' : '编辑流程图'),
      )
      pastRef.current = [...pastRef.current, entry].slice(-limit)
      futureRef.current = []
      persistNow()
      bump()
    },
    [bump, currentDocument, limit, persistNow],
  )

  /** 显式提交一次编辑后的新文档快照到历史栈。 */
  const commit = useCallback(
    (nextDocument: GraphUI, labelOrOpts?: string | GraphHistoryPushOptions) => {
      if (restoringRef.current) return
      const pushOpts = normalizePushOptions(labelOrOpts)
      if (!currentRef.current) {
        currentRef.current = createHistoryEntry(nextDocument, pushOpts.label || '初始版本')
        persistNow()
        bump()
        return
      }
      if (sameGraphSnapshot(currentRef.current.snapshot, nextDocument)) return
      const baseSnap = currentRef.current.snapshot
      pastRef.current = [...pastRef.current, currentRef.current].slice(-(limit - 1))
      futureRef.current = []
      stagedRef.current = null
      const label = pushOpts.label || describeGraphDiff(baseSnap, nextDocument)
      currentRef.current = createHistoryEntry(nextDocument, label)
      lastSeenDocRef.current = nextDocument
      persistNow()
      bump()
    },
    [bump, limit, persistNow],
  )

  /** 后退（Undo）：恢复到上一个历史版本，并将当前版本压入 future 栈。 */
  const undo = useCallback(
    (current: GraphUI, apply: (s: GraphUI) => void): boolean => {
      stagedRef.current = null
      // 丢弃与 current 完全相同的无效栈顶快照（防御空点击残留）
      while (
        pastRef.current.length > 0 &&
        sameGraphSnapshot(pastRef.current[pastRef.current.length - 1]!.snapshot, current)
      ) {
        pastRef.current = pastRef.current.slice(0, -1)
      }
      if (!pastRef.current.length) return false

      const prev = pastRef.current[pastRef.current.length - 1]!
      pastRef.current = pastRef.current.slice(0, -1)

      const curEntry =
        currentRef.current && sameGraphSnapshot(currentRef.current.snapshot, current)
          ? currentRef.current
          : createHistoryEntry(current, describeGraphDiff(prev.snapshot, current))

      futureRef.current = [...futureRef.current, curEntry]
      currentRef.current = prev

      const nextDoc = structuredClone(prev.snapshot)
      lastSeenDocRef.current = nextDoc
      restoringRef.current = true
      apply(nextDoc)
      queueMicrotask(() => {
        restoringRef.current = false
      })
      persistNow()
      bump()
      return true
    },
    [bump, persistNow],
  )

  /** 前进（Redo）：恢复到下一个历史版本，并将当前版本压入 past 栈。 */
  const redo = useCallback(
    (current: GraphUI, apply: (s: GraphUI) => void): boolean => {
      stagedRef.current = null
      if (!futureRef.current.length) return false

      const next = futureRef.current[futureRef.current.length - 1]!
      futureRef.current = futureRef.current.slice(0, -1)

      const curEntry =
        currentRef.current && sameGraphSnapshot(currentRef.current.snapshot, current)
          ? currentRef.current
          : createHistoryEntry(
              current,
              pastRef.current.length === 0 ? '初始版本' : '编辑流程图',
            )

      pastRef.current = [...pastRef.current, curEntry]
      currentRef.current = next

      const nextDoc = structuredClone(next.snapshot)
      lastSeenDocRef.current = nextDoc
      restoringRef.current = true
      apply(nextDoc)
      queueMicrotask(() => {
        restoringRef.current = false
      })
      persistNow()
      bump()
      return true
    },
    [bump, persistNow],
  )

  /**
   * 跳转到时间线上的任意历史步骤（targetIndex 为 entries 中的下标）：
   * targetIndex < currentIndex 时相当于连续后退，targetIndex > currentIndex 时相当于连续前进，
   * 中间步骤自动在 past 与 future 之间迁移，不丢失任何历史分支。
   */
  const jumpTo = useCallback(
    (targetIndex: number, current: GraphUI, apply: (s: GraphUI) => void): boolean => {
      stagedRef.current = null
      const curEntry =
        currentRef.current && sameGraphSnapshot(currentRef.current.snapshot, current)
          ? currentRef.current
          : createHistoryEntry(
              current,
              pastRef.current.length === 0 ? '初始版本' : '编辑流程图',
            )
      const chronologicalFuture = [...futureRef.current].reverse()
      const all = [...pastRef.current, curEntry, ...chronologicalFuture]
      const curIdx = pastRef.current.length
      if (targetIndex < 0 || targetIndex >= all.length || targetIndex === curIdx) {
        return false
      }

      const target = all[targetIndex]!
      pastRef.current = all.slice(0, targetIndex)
      currentRef.current = target
      futureRef.current = all.slice(targetIndex + 1).reverse()

      const nextDoc = structuredClone(target.snapshot)
      lastSeenDocRef.current = nextDoc
      restoringRef.current = true
      apply(nextDoc)
      queueMicrotask(() => {
        restoringRef.current = false
      })
      persistNow()
      bump()
      return true
    },
    [bump, persistNow],
  )

  const undoCount = pastRef.current.length
  const redoCount = futureRef.current.length
  const canUndo = undoCount > 0
  const canRedo = redoCount > 0

  // 组装完整的时间线列表（entries[0] 为初始版本，entries[currentIndex] 为当前激活版本）
  const timeline = useMemo(() => {
    const cur =
      currentRef.current ??
      (currentDocument ? createHistoryEntry(currentDocument, '初始版本') : null)
    const chronologicalFuture = [...futureRef.current].reverse()
    const entries = cur
      ? [...pastRef.current, cur, ...chronologicalFuture]
      : [...pastRef.current, ...chronologicalFuture]
    const currentIndex = cur ? pastRef.current.length : Math.max(0, entries.length - 1)
    return {
      entries,
      currentIndex,
      currentEntry: entries[currentIndex],
    }
  }, [
    currentDocument,
    undoCount,
    redoCount,
    // currentRef.current 随编辑更新
    currentRef.current,
  ])

  return useMemo(
    () => ({
      canUndo,
      canRedo,
      undoCount,
      redoCount,
      entries: timeline.entries,
      currentIndex: timeline.currentIndex,
      currentEntry: timeline.currentEntry,
      isRestoring,
      reset,
      push,
      commit,
      undo,
      redo,
      jumpTo,
    }),
    [
      canUndo,
      canRedo,
      undoCount,
      redoCount,
      timeline.entries,
      timeline.currentIndex,
      timeline.currentEntry,
      isRestoring,
      reset,
      push,
      commit,
      undo,
      redo,
      jumpTo,
    ],
  )
}

