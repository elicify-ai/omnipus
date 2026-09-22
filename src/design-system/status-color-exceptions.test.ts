import { describe, expect, it } from 'vitest'

import { colorExceptionRegistry } from './status-color-exceptions'

describe('constitutional colour exception registry', () => {
  it('contains exactly the five D3/D4/D14 governed boundaries', () => {
    expect(Object.keys(colorExceptionRegistry)).toEqual([
      'documentSurfaces',
      'qrCodes',
      'syntaxHighlighting',
      'dataVisualization',
      'userAuthoredColors',
    ])
  })

  it('gives every exception an owner, rationale, and narrow non-empty scope', () => {
    expect(Object.values(colorExceptionRegistry)).toHaveLength(5)
    for (const exception of Object.values(colorExceptionRegistry)) {
      expect(exception.owner.length).toBeGreaterThan(0)
      expect(exception.rationale.length).toBeGreaterThan(0)
      expect(exception.scope.length).toBeGreaterThan(0)
      expect(new Set(exception.scope).size).toBe(exception.scope.length)
    }
  })
})
