import { describe, expect, it } from 'vitest'
import {
  allOutputFields,
  fieldEndpoint,
  inputSourcesFor,
  isValidDataName,
  nodeDeclaredOutput,
  schemaPropertyNames,
} from './node-outputs'
import type { GraphNodeSpec } from './node-spec'
import type { GraphJSON } from './types'

const spec = (s: Partial<GraphNodeSpec>): GraphNodeSpec => ({ ID: 'x', Name: 'x', ...s }) as GraphNodeSpec

/** 字段 → 出口（不依赖跨出口遍历顺序）。 */
function endpointsByName(decl: ReturnType<typeof nodeDeclaredOutput>) {
  return Object.fromEntries(allOutputFields(decl).map((name) => [name, fieldEndpoint(decl, name) ?? '']))
}

describe('nodeDeclaredOutput', () => {
  it('derives outputs from config for template, code and merge', () => {
    expect(endpointsByName(nodeDeclaredOutput('template', { Outputs: [{ Key: 'a' }, { Key: '{{bad}}' }, { Key: 'a' }] }))).toEqual({
      a: '',
    })
    expect(endpointsByName(nodeDeclaredOutput('code', { Outputs: [{ Key: 'sum', Value: 'x+1' }] }))).toEqual({ sum: '' })
    expect(endpointsByName(nodeDeclaredOutput('__INNER__.merge', { Outputs: [{ Name: 'v', First: [] }] }))).toEqual({ v: '' })
  })
  it('uses OutputsByEndpoint and appends error when the node has an Error endpoint', () => {
    const http = spec({
      OutputsByEndpoint: {
        '': {
          type: 'object',
          properties: { Body: {}, Status: {}, OK: {} },
        },
        Case1: {
          type: 'object',
          properties: { Item: {} },
        },
      },
      Endpoints: { Inputs: ['In'], Outputs: ['Out', 'Error'] },
    })
    expect(endpointsByName(nodeDeclaredOutput('http', {}, http))).toEqual({
      Body: '',
      Status: '',
      OK: '',
      Item: 'Case1',
      Error: 'Error',
    })
  })
  it('validates names like graph.ValidName', () => {
    expect(['a', '_x', 'a1'].every(isValidDataName)).toBe(true)
    expect(['', '1a', 'a-b', '{{a}}', 'a'.repeat(65)].some(isValidDataName)).toBe(false)
  })
})

describe('inputSourcesFor', () => {
  const graph: GraphJSON = {
    InputSchema: { type: 'object', required: ['q'], properties: { q: {} }, additionalProperties: false },
    Nodes: [
      { ID: 'start', Kind: '__INNER__.start' },
      { ID: 'a', Kind: 'template', Inputs: { Outputs: { Value: [{ Key: 'r' }] } } },
      { ID: 'b', Kind: 'code', Inputs: { Outputs: { Value: [{ Key: 's' }] } } },
      { ID: 'side', Kind: 'template', Inputs: { Outputs: { Value: [{ Key: 't' }] } } },
      { ID: 'e', Kind: 'end' },
    ],
    Edges: [
      { ID: 'sa', From: 'start.Out', To: 'a.In' },
      { ID: 'ab', From: 'a.Out', To: 'b.In' },
      { ID: 'be', From: 'b.Out', To: 'e.In' },
      { ID: 'as', From: 'a.Out', To: 'side.In' },
    ],
  }
  it('lists run inputs and upstream nodes only', () => {
    const got = inputSourcesFor(graph, 'e', () => undefined)
    expect(got.map((s) => [s.id, schemaPropertyNames(s.schema)])).toEqual([
      ['$input', ['q']],
      ['a', ['r']],
      ['b', ['s']],
    ])
  })
  it('offers only run inputs to the start entry node', () => {
    expect(inputSourcesFor(graph, 'start', () => undefined).map((s) => s.id)).toEqual(['$input'])
  })
})
