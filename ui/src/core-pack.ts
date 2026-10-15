import { createNodeRegistry } from './node-registry'
import type { NodeLibrary } from './node-library'
import type { GraphNodePack } from './node-pack'

/** 注释框（画布装饰，不进后端 NodeCatalog） */
export const annotationLibrary: NodeLibrary = {
  id: 'annotation',
  items: [
    {
      type: 'annotation',
      labelKey: 'graph.addAnnotation',
      groupKey: 'graph.palette.other',
    },
  ],
}

/** 纯画布装饰，不进入后端 Graph NodeCatalog。 */
export const graphDecorationNodePack: GraphNodePack = {
  id: 'graph-decoration',
  definitions: annotationLibrary.items.map((item) => ({
    ...item,
    type: item.type,
    role: 'annotation',
  })),
  registry: createNodeRegistry([{ type: 'annotation', role: 'annotation' }]),
  library: annotationLibrary,
}
