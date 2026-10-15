import { createContext, useContext, type ReactNode } from 'react'

/** 宿主注入的翻译函数。内核自带英文默认值。 / Translator from the host; English fallbacks live here. */
export type GraphTranslate = (key: string, params?: Record<string, string | number>) => string

const DEFAULTS: Record<string, string> = {
  'graph.palette.search': 'Search nodes',
  'graph.palette.empty': 'No matching nodes',
  'graph.palette.close': 'Close',
  'graph.undo': 'Undo',
  'graph.redo': 'Redo',
  'graph.addAnnotation': 'Annotation',
}

function defaultTranslate(key: string, _params?: Record<string, string | number>): string {
  return DEFAULTS[key] ?? key
}

const GraphI18nContext = createContext<GraphTranslate>(defaultTranslate)

export function GraphI18nProvider({
  t,
  children,
}: {
  t?: GraphTranslate
  children: ReactNode
}) {
  return <GraphI18nContext.Provider value={t ?? defaultTranslate}>{children}</GraphI18nContext.Provider>
}

export function useGraphI18n(): { t: GraphTranslate } {
  return { t: useContext(GraphI18nContext) }
}
