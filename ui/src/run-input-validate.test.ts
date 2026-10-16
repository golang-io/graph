import { describe, expect, it } from 'vitest'
import { validateRunInput } from './run-input-validate'
import type { GraphJSONSchema } from './types'

describe('validateRunInput', () => {
  const schema: GraphJSONSchema = {
    type: 'object',
    additionalProperties: false,
    required: ['name', 'score', 'items'],
    properties: {
      name: { type: 'string' },
      score: { type: 'number' },
      items: { type: 'array', items: { type: 'string' } },
    },
  }

  it('accepts typed run input matching demo_tour', () => {
    const res = validateRunInput(schema, {
      name: 'Ada',
      score: 40,
      items: ['a', 'b', 'c'],
    })
    expect(res).toEqual({ ok: true })
  })

  it('rejects name as integer (the original failure case)', () => {
    const res = validateRunInput(schema, {
      name: 22,
      score: 33,
      items: [1, 2, 3],
    })
    expect(res.ok).toBe(false)
    if (res.ok) return
    expect(res.issues.some((i) => i.path.includes('name'))).toBe(true)
  })

  it('rejects undeclared keys and missing required', () => {
    const res = validateRunInput(schema, { name: 'Ada', extra: 1 })
    expect(res.ok).toBe(false)
    if (res.ok) return
    const paths = res.issues.map((i) => i.path)
    expect(res.issues.some((i) => i.message === '请填写')).toBe(true)
    expect(paths.some((p) => p.includes('extra'))).toBe(true)
  })
})
