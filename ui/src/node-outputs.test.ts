import { describe, expect, it } from 'vitest'
import {
  allOutputFields,
  fieldEndpoint,
  inputSourcesFor,
  isValidDataName,
  nodeDeclaredOutput,
  schemaPropertyNames,
} from './node-outputs'
import {
  buildRunInputJsonTemplate,
  collectRunInputValues,
  formatRunInputJsonObject,
  formatRunInputValue,
  mergeUndeclaredRunInputFields,
  parseRunInputJsonObject,
  parseRunInputValue,
} from './run-input-schema'
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
    InputSchema: { type: 'object', required: ['q'], properties: { q: { type: 'string' } }, additionalProperties: false },
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

describe('run-input-schema value & JSON helpers', () => {
  const declared = [
    { name: 'items', required: true, schema: { type: 'array' as const, items: { type: 'string' } } },
    { name: 'mode', schema: { type: 'string' as const } },
    { name: 'seed', schema: { type: 'integer' as const } },
  ]

  it('parses and formats row values while preserving string vs JSON types', () => {
    expect(parseRunInputValue('3')).toBe(3)
    expect(parseRunInputValue('["a","b"]')).toEqual(['a', 'b'])
    expect(parseRunInputValue('【"a"，"b"】')).toEqual(['a', 'b'])
    expect(parseRunInputValue('fast')).toBe('fast')
    expect(parseRunInputValue('"123"')).toBe('123')

    expect(formatRunInputValue(['a', 'b'])).toBe('["a","b"]')
    expect(formatRunInputValue('fast')).toBe('fast')
    expect(formatRunInputValue('123')).toBe('"123"')
    expect(formatRunInputValue('')).toBe('""')
    expect(parseRunInputValue(formatRunInputValue('123'))).toBe('123')
    expect(parseRunInputValue(formatRunInputValue(''))).toBe('')
  })

  it('parses complete JSON object into row values and round-trips back to JSON', () => {
    const raw = '{\n  "items": ["a", "b"],\n  "mode": "fast",\n  "seed": 3\n}'
    const res = parseRunInputJsonObject(raw)
    expect(res.ok).toBe(true)
    if (!res.ok) return
    expect(res.values).toEqual({
      items: '["a","b"]',
      mode: 'fast',
      seed: '3',
    })
    expect(collectRunInputValues(declared, res.values)).toEqual({
      items: ['a', 'b'],
      mode: 'fast',
      seed: 3,
    })
    expect(JSON.parse(formatRunInputJsonObject(declared, res.values))).toEqual({
      items: ['a', 'b'],
      mode: 'fast',
      seed: 3,
    })
  })

  it('rejects non-object JSON or invalid syntax in multiline JSON mode', () => {
    expect(parseRunInputJsonObject('[1, 2, 3]').ok).toBe(false)
    expect(parseRunInputJsonObject('123').ok).toBe(false)
    expect(parseRunInputJsonObject('{bad json').ok).toBe(false)
    expect(parseRunInputJsonObject('   ')).toEqual({ ok: true, value: {}, values: {} })
  })

  it('builds template and merges undeclared valid keys', () => {
    const tpl = buildRunInputJsonTemplate(declared, { seed: '7' })
    expect(JSON.parse(tpl)).toEqual({
      items: ['a', 'b', 'c'],
      mode: 'Ada',
      seed: 7,
    })

    const merged = mergeUndeclaredRunInputFields(declared, {
      items: '["x"]',
      extra_key: 'true',
      extra_num: '42',
      extra_str: 'hello',
      '1invalid': '99',
    })
    expect(merged.map((f) => f.name)).toEqual([
      'items',
      'mode',
      'seed',
      'extra_key',
      'extra_num',
      'extra_str',
    ])
    expect(merged[3]?.schema).toEqual({ type: 'boolean' })
    expect(merged[4]?.schema).toEqual({ type: 'integer' })
    expect(merged[5]?.schema).toEqual({ type: 'string' })
  })
})
