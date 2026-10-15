import { describe, expect, it } from 'vitest'
import { createElement } from 'react'
import { renderToString } from 'react-dom/server'
import { ReactFlowProvider } from '@xyflow/react'
import { StepNodeView } from './GraphNodes'
import { GraphNodeRegistryProvider } from './GraphNodeRegistryContext'
import { createGraphNodePackFromCatalog } from './node-spec'

const pack = createGraphNodePackFromCatalog('test', [
  {ID: '__INNER__.stop_error', Name: 'Stop', Endpoints: {Inputs: ['In'], Outputs: ['Error']}},
])

describe('React Flow static error output', () => {
  it('renders stop_error error handle once', () => {
    const html = renderToString(createElement(ReactFlowProvider, null,
      createElement(GraphNodeRegistryProvider, {registry: pack.registry, children:
        createElement(StepNodeView, {
          id: 'n',
          data: {label: 'Stop', nodeType: '__INNER__.stop_error', config: {}},
          type: '__INNER__.stop_error',
          selected: false,
          dragging: false,
          draggable: true,
          selectable: true,
          deletable: true,
          isConnectable: true,
          zIndex: 0,
          positionAbsoluteX: 0,
          positionAbsoluteY: 0,
        }),
      })))
    expect(html.match(/data-handleid="Error"/g)).toHaveLength(1)
  })
})
