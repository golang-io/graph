import { createContext, useContext, type ReactNode } from 'react'
import { emptyNodeRegistry, type NodeTypeRegistry } from './node-registry'

const Ctx = createContext<NodeTypeRegistry>(emptyNodeRegistry)

export function GraphNodeRegistryProvider({
  registry,
  children,
}: {
  registry?: NodeTypeRegistry | null
  children: ReactNode
}) {
  return (
    <Ctx.Provider value={registry ?? emptyNodeRegistry}>{children}</Ctx.Provider>
  )
}

export function useGraphNodeRegistry(): NodeTypeRegistry {
  return useContext(Ctx)
}
