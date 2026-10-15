import { useCallback, useEffect, useState } from 'react'
import type { GraphNodeSpec } from './node-spec'

export type GraphNodeCatalogState = {
  specs: GraphNodeSpec[]
  loading: boolean
  error: unknown
  reload: () => void
}

/** graph-react 只依赖注入的 loader，不直接依赖 FreeToken API 或鉴权实现。 */
export function useGraphNodeCatalog(
  loader: () => Promise<GraphNodeSpec[]>,
): GraphNodeCatalogState {
  const [specs, setSpecs] = useState<GraphNodeSpec[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<unknown>(null)
  const [revision, setRevision] = useState(0)

  const reload = useCallback(() => setRevision((value) => value + 1), [])

  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setError(null)
    void loader()
      .then((result) => {
        if (!cancelled) setSpecs(result)
      })
      .catch((reason: unknown) => {
        if (!cancelled) {
          setSpecs([])
          setError(reason)
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [loader, revision])

  return { specs, loading, error, reload }
}
