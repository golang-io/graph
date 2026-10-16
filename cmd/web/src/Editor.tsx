import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Check, Redo2, Save, Undo2 } from 'lucide-react'
import {
  edgesOnRemovedOutputs,
  endpointIds,
  graphDecorationNodePack,
  graphEditorCanvasConfig,
  inputSourcesFor,
  GraphNodeContextMenu,
  GraphNodePropsPanel,
  GraphPalette,
  GraphWorkbench,
  libraryDefaultConfig,
  insertionPosition,
  insertionScope,
  loadStoredGraphHistory,
  packSpecOf,
  parseGraphUI,
  resolveEffectiveEndpoints,
  resolveEndpointSides,
  rotateEndpointSides,
  scopeGraphOf,
  scopedId,
  useGraphDocument,
  useGraphHistory,
  withEndpointSides,
  withNodeEndpointSides,
  type GraphDefinition,
  type GraphNodePack,
  type EndpointSides,
} from '@golang-io/graph'
import { api } from './api'

type Props = {
  doc: GraphDefinition
  pack: GraphNodePack | null
  onSaved: (doc: GraphDefinition) => void
}

export default function Editor({ doc, pack, onSaved }: Props) {
  const activePack = useMemo(() => pack ?? graphDecorationNodePack, [pack])
  const initialEditorDoc = useMemo(() => {
    const stored = loadStoredGraphHistory(doc.GraphID)
    const snap = stored?.entries?.[stored.index]?.snapshot
    return snap ? { ...doc, Graph: snap.Graph, UI: snap.UI } : doc
  }, [doc])
  const editor = useGraphDocument(initialEditorDoc)
  const [saving, setSaving] = useState(false)
  const [savedToast, setSavedToast] = useState(false)
  const [error, setError] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  // 属性面板开关：单击只选中，打开属性走右键菜单「打开属性」
  const [showNodeProps, setShowNodeProps] = useState(false)
  const canvasContainerRef = useRef<HTMLDivElement>(null)
  const [nodeContextMenu, setNodeContextMenu] = useState<{
    x: number
    y: number
    nodeId: string
  } | null>(null)

  const history = useGraphHistory({
    storageKey: doc.GraphID,
    currentDocument: editor.document,
    limit: 50,
  })
  const captureBefore = useCallback(() => {
    if (history.isRestoring()) return
    history.push(editor.getDocument())
  }, [history, editor])

  const applySnapshot = editor.load
  const onUndo = useCallback(
    () => history.undo(editor.getDocument(), applySnapshot),
    [history, editor, applySnapshot],
  )
  const onRedo = useCallback(
    () => history.redo(editor.getDocument(), applySnapshot),
    [history, editor, applySnapshot],
  )

  const addNode = useCallback(
    (type: string) => {
      captureBefore()
      const item = activePack.library.items.find((i) => i.type === type)
      const nodeType = item?.nodeType ?? type
      // 选中展开的 map 容器或循环体内的节点时，新节点放进该循环体（画布 ID 带容器前缀）
      const scope = insertionScope(editor.nodes, selectedId)
      const id = scopedId(scope, `n${Date.now().toString(36)}`)
      editor.setNodes((ns) => [
        ...ns,
        {
          id,
          type: nodeType,
          name: item?.label ?? nodeType,
          position: insertionPosition(ns, scope),
          config: libraryDefaultConfig(activePack.library, type),
        },
      ])
      setSelectedId(id)
      // 从节点库新增后直接打开属性，便于立刻改配置
      setShowNodeProps(true)
      setNodeContextMenu(null)
    },
    [activePack, captureBefore, editor, selectedId],
  )

  const selectedNode = useMemo(
    () => editor.nodes.find((n) => n.id === selectedId) ?? null,
    [editor.nodes, selectedId],
  )

  // 修改单个节点的端点方位；保存时每个节点的每个端点都写入 UI.EndpointSides
  const patchEndpointSides = useCallback(
    (id: string, sides: EndpointSides) => {
      captureBefore()
      editor.setNodes((ns) => withNodeEndpointSides(ns, id, sides))
    },
    [captureBefore, editor],
  )

  // 按落图种类查种类描述（与 RunView 共用 ui 包的 packSpecOf，同一 spec 的 preset 条目共用同一份 spec）
  const specOf = useCallback((type: string) => packSpecOf(pack, type), [pack])
  const selectedSpec = selectedNode ? specOf(selectedNode.type) : undefined
  // 选中节点在当前参数下的全部端点（switch 的分支随 Cases 变化），用于属性面板与 R 键旋转
  const selectedEndpoints = useMemo(
    () =>
      selectedNode
        ? resolveEffectiveEndpoints(selectedNode.type, selectedSpec, activePack.registry, selectedNode.config)
        : undefined,
    [selectedNode, selectedSpec, activePack],
  )

  const save = useCallback(async () => {
    setSaving(true)
    setError('')
    try {
      // 后端要求每个节点（含循环体内节点）的每个端点都写明所在边；缺失的按入口在左、出口在右补齐后再提交，
      // 已不存在的端点（如删掉的 switch 分支）同时清除
      const current = withEndpointSides(editor.getDocument(), activePack.registry)
      const saved = await api.save(doc.GraphID, { Name: doc.Name, Description: doc.Description, ...current })
      // 服务端返回规范化后的定义（含新版本号）；布局再经 parseGraphUI 过滤为画布认识的键。
      const normalized = { ...saved, UI: parseGraphUI(saved.UI) }
      editor.load(normalized)
      onSaved(normalized)
      setSavedToast(true)
      setTimeout(() => setSavedToast(false), 1800)
    } catch (e) {
      setError(String(e))
    } finally {
      setSaving(false)
    }
  }, [editor, activePack.registry, doc.GraphID, doc.Name, doc.Description, onSaved])

  // 画布操作快捷键：R 旋转端点，Cmd/Ctrl+Z 后退，Cmd/Ctrl+Shift+Z 或 Cmd/Ctrl+Y 前进，Cmd/Ctrl+S 编辑保存
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
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
      if (!mod && !e.altKey && (e.key === 'r' || e.key === 'R') && selectedNode && selectedEndpoints) {
        e.preventDefault()
        patchEndpointSides(selectedNode.id, rotateEndpointSides(selectedNode.endpointSides, endpointIds(selectedEndpoints)))
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [selectedNode, selectedEndpoints, patchEndpointSides, save, onUndo, onRedo])

  // 选中节点可引用的来源：运行输入与上游节点的输出（仅依赖 Graph 结构与选中节点 ID，拖拽移动节点或缩放便签时不重算）。
  // 循环体内的节点只看所在循环体：上游为体内节点，运行输入为 item / index 与 map 的自定义输入。
  const selectedSources = useMemo(() => {
    const at = selectedId ? scopeGraphOf({ Graph: editor.document.Graph }, selectedId) : undefined
    return at ? inputSourcesFor(at.graph, at.localId, specOf) : []
  }, [editor.document.Graph, selectedId, specOf])

  return (
    <GraphWorkbench
      registry={activePack.registry}
      toolbar={
        <div className="flex w-full flex-wrap items-center justify-between gap-2">
          {/* 画布操作专属分组：后退、前进、编辑保存单独聚合在一起 */}
          <div className="flex items-center gap-2">
            <span className="rounded bg-muted/70 px-2 py-1 text-[11px] font-medium text-muted-foreground select-none">
              画布操作
            </span>
            <div className="inline-flex h-8 items-center overflow-hidden rounded-ui border border-border bg-background">
              <button
                type="button"
                disabled={!history.canUndo}
                onClick={onUndo}
                title="后退 / 撤销上一步画布修改 (⌘Z / Ctrl+Z)"
                className="inline-flex h-full items-center gap-1.5 border-r border-border px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted/60 disabled:opacity-40 disabled:pointer-events-none cursor-pointer"
              >
                <Undo2 className="h-3.5 w-3.5" />
                后退
              </button>
              <button
                type="button"
                disabled={!history.canRedo}
                onClick={onRedo}
                title="前进 / 重做下一步画布修改 (⌘⇧Z / Ctrl+Y)"
                className="inline-flex h-full items-center gap-1.5 px-2.5 text-xs font-medium text-foreground transition-colors hover:bg-muted/60 disabled:opacity-40 disabled:pointer-events-none cursor-pointer"
              >
                <Redo2 className="h-3.5 w-3.5" />
                前进
              </button>
            </div>
            <button
              type="button"
              onClick={() => void save()}
              disabled={saving}
              title="保存画布结构与节点属性修改 (⌘S / Ctrl+S)"
              className="inline-flex h-8 items-center gap-1.5 rounded-ui bg-primary px-3 text-xs font-semibold text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50 cursor-pointer"
            >
              {savedToast ? <Check className="h-3.5 w-3.5" /> : <Save className="h-3.5 w-3.5" />}
              {saving ? '保存中…' : savedToast ? '已保存' : '编辑保存'}
            </button>
            {error ? <span className="text-xs text-destructive">{error}</span> : null}
          </div>

          <div className="flex items-center gap-2 text-[11px] text-muted-foreground select-none">
            <span>
              {editor.document.Graph.Nodes.length} 节点 · {editor.document.Graph.Edges.length} 连线
            </span>
            <span className="hidden sm:inline text-muted-foreground/60">
              · 单击选中 · 右键打开属性 · 选中按 R 旋转端点 · ⌘Z 后退 · ⌘S 保存
            </span>
          </div>
        </div>
      }
      palette={<GraphPalette library={activePack.library} onAdd={addNode} />}
      canvasContainerRef={canvasContainerRef}
      canvasOverlay={
        nodeContextMenu ? (
          <GraphNodeContextMenu
            x={nodeContextMenu.x}
            y={nodeContextMenu.y}
            title={nodeContextMenu.nodeId}
            onClose={() => setNodeContextMenu(null)}
            items={[
              {
                label: '打开属性',
                onClick: () => {
                  setSelectedId(nodeContextMenu.nodeId)
                  setShowNodeProps(true)
                },
              },
            ]}
          />
        ) : null
      }
      propsPanel={
        selectedNode && showNodeProps ? (
          <GraphNodePropsPanel
            node={{
              id: selectedNode.id,
              nodeType: selectedNode.type,
              label: selectedNode.name || selectedNode.type,
              config: selectedNode.config ?? {},
              inputs: selectedNode.inputs,
            }}
            spec={selectedSpec}
            inputSources={selectedSources}
            endpointSides={selectedEndpoints ? resolveEndpointSides(selectedNode.endpointSides, endpointIds(selectedEndpoints)) : undefined}
            endpoints={selectedEndpoints}
            onEndpointSidesChange={(sides) => patchEndpointSides(selectedNode.id, sides)}
            onChange={(patch) => {
              captureBefore()
              editor.setNodes((ns) =>
                ns.map((n) =>
                  n.id === selectedNode.id
                    ? {
                        ...n,
                        ...(patch.label !== undefined ? { name: patch.label } : {}),
                        ...(patch.config !== undefined ? { config: patch.config } : {}),
                        ...(patch.inputs !== undefined ? { inputs: patch.inputs } : {}),
                      }
                    : n,
                ),
              )
              // 参数改变出口集合时（删除 switch 分支），联动删除连在已移除出口上的边
              if (patch.config !== undefined) {
                const outputs = resolveEffectiveEndpoints(
                  selectedNode.type,
                  selectedSpec,
                  activePack.registry,
                  patch.config,
                ).outputs
                const stale = new Set(edgesOnRemovedOutputs(editor.edges, selectedNode.id, outputs).map((e) => e.id))
                if (stale.size > 0) editor.setEdges((es) => es.filter((e) => !stale.has(e.id)))
              }
            }}
            onDelete={() => {
              captureBefore()
              editor.setNodes((ns) => ns.filter((n) => n.id !== selectedNode.id))
              editor.setEdges((es) =>
                es.filter((e) => e.from !== selectedNode.id && e.to !== selectedNode.id),
              )
              setSelectedId(null)
              setShowNodeProps(false)
              setNodeContextMenu(null)
            }}
            onClose={() => setShowNodeProps(false)}
          />
        ) : undefined
      }
      canvas={{
        document: editor.document,
        nodePack: activePack,
        view: editor.view,
        onChange: editor.load,
        onViewChange: editor.setView,
        onBeforeEdit: captureBefore,
        canvas: graphEditorCanvasConfig,
        // 单击仅选中，不自动打开属性面板
        onNodeClick: (_e, node) => {
          setSelectedId(node.id)
          setNodeContextMenu(null)
        },
        onNodeDoubleClick: (_e, node) => {
          setSelectedId(node.id)
          setNodeContextMenu(null)
        },
        onPaneClick: () => {
          setSelectedId(null)
          setShowNodeProps(false)
          setNodeContextMenu(null)
        },
        interaction: {
          onNodeContextMenu: (e, node) => {
            const rect = canvasContainerRef.current?.getBoundingClientRect()
            const clientX = 'clientX' in e ? e.clientX : 0
            const clientY = 'clientY' in e ? e.clientY : 0
            setSelectedId(node.id)
            setNodeContextMenu({
              x: clientX - (rect?.left ?? 0),
              y: clientY - (rect?.top ?? 0),
              nodeId: node.id,
            })
          },
        },
      }}
    />
  )
}
