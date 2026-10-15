import { expect, it } from 'vitest'
import { parseGraphUI } from './graph-adapter'

it('reads StickyNotes and drops unknown layout keys', () => {
  const note = { ID: 'note', X: 0, Y: 0, Width: 100, Height: 80, Title: 'note' }
  expect(parseGraphUI({ Groups: [note] }).StickyNotes).toBeUndefined()
  expect(parseGraphUI({ sticky_notes: [note] }).StickyNotes).toBeUndefined()
  expect(parseGraphUI({ StickyNotes: [note] }).StickyNotes).toHaveLength(1)
})

it('reads SubLayouts and drops the old Bodies key', () => {
  const layout = { Positions: { start: { X: 1, Y: 2 } } }
  expect(parseGraphUI({ Bodies: { lp: layout } }).SubLayouts).toBeUndefined()
  expect(parseGraphUI({ SubLayouts: { lp: layout }, Bodies: { old: layout } }).SubLayouts).toEqual({ lp: layout })
})
