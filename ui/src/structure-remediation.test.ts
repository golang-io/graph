import { describe, expect, it } from 'vitest'
import { createGraphNodePackFromCatalog, type GraphNodeSpec } from './node-spec'
import { libraryDefaultConfig } from './node-library'
import { projectDocument } from './document'

const spec = (ID: string, value: string): GraphNodeSpec => ({
  ID,
  Name: ID,
  Endpoints: { Inputs: ['In'], Outputs: ['Out'] },
  DefaultInputs: { value: { Value: value } },
})

describe('structure remediation', () => {
  it('keeps presets and real node types in separate identity spaces', () => {
    const a = spec('a', 'a')
    a.Presets = [{ ID: 'b', Label: 'A preset', Inputs: { value: { Value: 'preset' } } }]
    const pack = createGraphNodePackFromCatalog('test', [a, spec('b', 'b')])
    const preset = pack.library.items.find((item) => item.label === 'A preset')!
    expect(new Set(pack.library.items.map((item) => item.type)).size).toBe(3)
    expect(preset.type).not.toBe('b')
    expect(preset.nodeType).toBe('a')
    expect(libraryDefaultConfig(pack.library, preset.type)).toEqual({ value: 'preset' })
    expect(libraryDefaultConfig(pack.library, 'b')).toEqual({ value: 'b' })
    expect(pack.registry.get('b')?.spec?.ID).toBe('b')
    expect(pack.registry.get(preset.type)).toBeUndefined()
  })

  it('rejects ambiguous annotation identities at the document boundary', () => {
    const note = { ID: 'n', X: 0, Y: 0, Width: 300, Height: 180 }
    expect(() => projectDocument({ Graph: { Nodes: [{ ID: 'n', Kind: 'template' }], Edges: [] }, UI: { StickyNotes: [note] } })).toThrow('Duplicate canvas node')
    expect(() => projectDocument({ Graph: { Nodes: [], Edges: [] }, UI: { StickyNotes: [note, note] } })).toThrow('Duplicate canvas node')
    expect(projectDocument({ Graph: { Nodes: [{ ID: 's', Kind: 'template' }], Edges: [] }, UI: { StickyNotes: [note] } }).nodes.map((node) => node.id)).toEqual(['n', 's'])
  })
})
