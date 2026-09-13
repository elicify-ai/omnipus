/**
 * writeSetValidation.test.ts
 *
 * UAT defect A (plan authoring): a plan member's `write_set` had no authoring
 * surface anywhere in the interface, so `pkg/plan/lint.go`'s overlapping-write
 * refusal named a fix the operator could not apply.
 *
 * The oracle for every case below is the SERVER's comparison rule, not this
 * module's implementation: `pkg/plan/lint.go::normalizeWriteSetPath` trims
 * whitespace, strips one trailing slash and applies POSIX `path.Clean`, then
 * `pathsOverlap` compares slash-delimited SEGMENTS. A value that survives this
 * validator must line up under that rule; a value that cannot is rejected here
 * rather than accepted into a lint that then passes for the wrong reason.
 */

import { describe, it, expect } from 'vitest'
import { normalizeWriteSetPath, validateWriteSetPath } from './writeSetValidation'

describe('normalizeWriteSetPath', () => {
  it('trims surrounding whitespace', () => {
    expect(normalizeWriteSetPath('  pkg/plan/lint.go  ')).toBe('pkg/plan/lint.go')
  })

  it('strips a single trailing slash, matching the server\'s own normalisation', () => {
    expect(normalizeWriteSetPath('pkg/plan/')).toBe('pkg/plan')
  })

  it('leaves a bare "/" alone rather than normalising it to the empty string', () => {
    // Reaching "" here would make the entry invisible to the overlap check
    // (the server explicitly never treats "" as matching everything); the
    // validator rejects a leading slash outright instead.
    expect(normalizeWriteSetPath('/')).toBe('/')
  })

  it('preserves case — a path is not a tag', () => {
    expect(normalizeWriteSetPath('pkg/Plan/Lint.go')).toBe('pkg/Plan/Lint.go')
  })

  it('collapses whitespace-only input to the empty string', () => {
    expect(normalizeWriteSetPath('   ')).toBe('')
  })
})

describe('validateWriteSetPath', () => {
  it('accepts an ordinary repo-relative path', () => {
    expect(validateWriteSetPath('pkg/plan/lint.go')).toEqual({
      ok: true,
      value: 'pkg/plan/lint.go',
      error: '',
    })
  })

  it('accepts a directory-style entry — the lint treats it as covering everything nested beneath', () => {
    expect(validateWriteSetPath('pkg/plan/').ok).toBe(true)
    expect(validateWriteSetPath('pkg/plan/').value).toBe('pkg/plan')
  })

  it('accepts a "./"-prefixed path — path.Clean resolves it, so it still compares correctly', () => {
    expect(validateWriteSetPath('./src/schema.go').ok).toBe(true)
  })

  it('rejects whitespace-only input silently — nothing to add, nothing went wrong', () => {
    const result = validateWriteSetPath('   ')
    expect(result.ok).toBe(false)
    expect(result.error).toBe('')
  })

  it('rejects an absolute path: path.Clean keeps the leading slash, so "/src/a.go" would never compare equal to "src/a.go"', () => {
    const result = validateWriteSetPath('/src/a.go')
    expect(result.ok).toBe(false)
    expect(result.error).toBe('Use a path relative to the workspace root')
  })

  it('rejects a backslash path: the lint splits on "/" only, so a Windows separator matches nothing', () => {
    const result = validateWriteSetPath('src\\a.go')
    expect(result.ok).toBe(false)
    expect(result.error).toBe('Use forward slashes in paths')
  })

  it('rejects a duplicate of an existing entry', () => {
    const result = validateWriteSetPath('src/a.go', ['src/a.go'])
    expect(result.ok).toBe(false)
    expect(result.error).toBe('Already in the write set')
  })

  it('rejects a duplicate that differs only by trailing slash or padding — the server would see one path, not two', () => {
    expect(validateWriteSetPath(' src/a/ ', ['src/a']).ok).toBe(false)
    expect(validateWriteSetPath('src/a', ['src/a/']).ok).toBe(false)
  })

  it('does NOT treat a distinct sibling as a duplicate', () => {
    expect(validateWriteSetPath('src/ab.go', ['src/a.go']).ok).toBe(true)
  })

  it('echoes the normalised value back even when it rejects, so the input can be corrected in place', () => {
    expect(validateWriteSetPath('  /src/a.go  ').value).toBe('/src/a.go')
  })
})
