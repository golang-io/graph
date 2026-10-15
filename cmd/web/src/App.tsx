import { useCallback, useEffect, useMemo, useState } from 'react'
import {
  createGraphNodePackFromCatalog,
  Select,
  useGraphNodeCatalog,
  type GraphDefinition,
  type GraphNodePack,
} from '@golang-io/graph-ui'
import { api } from './api'
import RunView from './RunView'

const loadSpecs = () => api.nodeSpecs()

export default function App() {
  const [list, setList] = useState<GraphDefinition[]>([])
  const [id, setId] = useState('')
  const [doc, setDoc] = useState<GraphDefinition | null>(null)
  const [error, setError] = useState('')

  const { specs, loading } = useGraphNodeCatalog(loadSpecs)
  const pack = useMemo<GraphNodePack | null>(
    () => (specs.length ? createGraphNodePackFromCatalog('demo', specs) : null),
    [specs],
  )

  useEffect(() => {
    api
      .graphs()
      .then((rows) => {
        setList(rows)
        if (rows[0]) setId(rows[0].GraphID)
      })
      .catch((e: unknown) => setError(String(e)))
  }, [])

  useEffect(() => {
    if (!id) return
    setDoc(null)
    api.graph(id).then(setDoc).catch((e: unknown) => setError(String(e)))
  }, [id])

  const handleSaved = useCallback((saved: GraphDefinition) => {
    setDoc(saved)
    setList((prev) => prev.map((item) => (item.GraphID === saved.GraphID ? { ...item, ...saved } : item)))
  }, [])

  const workflowSelector = (
    <div className="flex shrink-0 items-center gap-2">
      <div className="inline-flex h-8 max-w-full shrink-0 items-center">
        <span className="flex h-full shrink-0 items-center rounded-l-ui border border-r-0 border-input bg-muted/40 px-2.5 text-xs text-muted-foreground select-none">
          工作流
        </span>
        <Select
          value={id}
          onChange={setId}
          aria-label="选择图"
          placeholder="选择图"
          options={list.map((w) => ({ value: w.GraphID, label: `${w.Name} · ${w.GraphID}` }))}
          className="w-64 max-w-full"
          buttonClassName="rounded-l-none text-xs font-medium"
        />
      </div>
      {doc?.Version ? (
        <span
          className="shrink-0 rounded bg-muted px-2 py-0.5 font-mono text-[11px] text-muted-foreground"
          title="当前已保存版本号"
        >
          v{doc.Version}
        </span>
      ) : null}
    </div>
  )

  return (
    <div className="flex h-full flex-col">
      {!doc || loading ? (
        <header className="flex shrink-0 items-center gap-2.5 border-b border-border px-3 py-2">
          {workflowSelector}
          {loading ? <span className="text-xs text-muted-foreground">加载节点目录…</span> : null}
          {error ? <span className="ml-auto text-xs text-destructive">{error}</span> : null}
        </header>
      ) : null}
      {/* flex 容器：一体化工作台（两行分栏：第一行画布编辑保存，第二行节点执行与在线 DEBUG 控制） */}
      <main className="flex min-h-0 flex-1 flex-col">
        {!doc || loading ? (
          <p className="p-4 text-sm text-muted-foreground">加载中…</p>
        ) : (
          <RunView
            key={doc.GraphID}
            doc={doc}
            pack={pack}
            onSaved={handleSaved}
            workflowSelector={workflowSelector}
          />
        )}
      </main>
    </div>
  )
}
