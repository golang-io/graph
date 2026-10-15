import { describe, expect, it } from 'vitest'
import { engineWhenFromHandle } from './handle-when'

describe('engineWhenFromHandle', () => {
  it('strips canvas-only In/Out handles', () => {
    expect(engineWhenFromHandle(undefined)).toBeUndefined()
    expect(engineWhenFromHandle('')).toBeUndefined()
    expect(engineWhenFromHandle('In')).toBeUndefined()
    expect(engineWhenFromHandle('Out')).toBeUndefined()
  })

  it('keeps branch labels as when', () => {
    expect(engineWhenFromHandle('Case0')).toBe('Case0')
    expect(engineWhenFromHandle('Case15')).toBe('Case15')
    expect(engineWhenFromHandle('Error')).toBe('Error')
  })
})
