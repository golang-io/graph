import { useCallback, useEffect, useMemo, useState } from 'react'
import { Check, Redo2, Save, Undo2 } from 'lucide-react'
import {
  edgesOnRemovedOutputs,
  endpointIds,
  graphDecorationNodePack,
  graphEditorCanvasConfig,
  inputSourcesFor,
  GraphNodePropsPanel,
  GraphPalette,
  GraphWorkbench,
  libraryDefaultConfig,
  insertionPosition,
  insertionScope,
  parseGraphUI,
  resolveEndpointSides,
  rotateEndpointSides,
  scopeGraphOf,
  scopedId,
  resolveGraphNodeEndpoints,
  useGraphDocument,
  useGraphHistory,
  withEndpointSides,
  type GraphDefinition,
  type GraphNodePack,
  type EndpointSides,
} from '@golang-io/graph-ui'
import { api } from './api'

type Props = {
  doc: GraphDefinition
  pack: GraphNodePack | null
  onSaved: (doc: GraphDefinition) => void
}

export default function Editor({ doc, pack, onSaved }: Props) {
  const activePack = useMemo(() => pack ?? graphDecorationNodePack, [pack])
  const editor = useGraphDocument(doc)
  const [saving, setSaving] = useState(false)
  const [savedToast, setSavedToast] = useState(false)
  const [error, setError] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)

  const history = useGraphHistory(50)
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
      editor.setNodes((ns) => ns.map((n) => (n.id === id ? { ...n, endpointSides: sides } : n)))
    },
    [captureBefore, editor],
  )

  const specOf = useCallback(
    (type: string) => pack?.definitions.find((d) => d.nodeType === type)?.spec,
    [pack],
  )
  const selectedSpec = selectedNode ? specOf(selectedNode.type) : undefined
  // 选中节点在当前参数下的全部端点（switch 的分支随 Cases 变化），用于属性面板与 R 键旋转
  const selectedEndpoints = useMemo(
    () =>
      selectedNode
        ? resolveGraphNodeEndpoints(
            selectedNode.type,
            selectedSpec?.Endpoints ?? activePack.registry.get(selectedNode.type)?.displayEndpoints,
            selectedNode.config,
            selectedSpec?.Renderer,
          )
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
      onSaved({ ...saved, UI: parseGraphUI(saved.UI) })
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

  // 选中节点可引用的来源：运行输入与上游节点的输出（按当前未保存的图计算）。
  // 循环体内的节点只看所在循环体：上游为体内节点，运行输入为 item / index 与 map 的自定义输入。
  const selectedSources = useMemo(() => {
    const at = selectedNode ? scopeGraphOf(editor.document, selectedNode.id) : undefined
    return at ? inputSourcesFor(at.graph, at.localId, specOf) : []
  }, [editor.document, selectedNode, specOf])

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
              · 选中节点按 R 旋转端点 · ⌘Z 后退 · ⌘S 保存
            </span>
          </div>
        </div>
      }
      palette={<GraphPalette library={activePack.library} onAdd={addNode} />}
      propsPanel={
        selectedNode ? (
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
                const outputs = resolveGraphNodeEndpoints(
                  selectedNode.type,
                  selectedSpec?.Endpoints ?? activePack.registry.get(selectedNode.type)?.displayEndpoints,
                  patch.config,
                  selectedSpec?.Renderer,
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
            }}
            onClose={() => setSelectedId(null)}
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
        onNodeClick: (_e, node) => {
          setSelectedId(node.id)
        },
        onNodeDoubleClick: (_e, node) => {
          setSelectedId(node.id)
        },
        onPaneClick: () => setSelectedId(null),
      }}
    />
  )
}
