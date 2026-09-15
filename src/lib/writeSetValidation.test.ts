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
import {
  normalizeWriteSetPath,
  validateWriteSetPath,
  writeSetComparisonKey,
} from './writeSetValidation'

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

// ── The server's comparison key ───────────────────────────────────────────────
//
// Oracle: `pkg/plan/lint.go::normalizeWriteSetPath` — TrimSpace, strip ONE
// trailing slash, then POSIX `path.Clean`. Every expectation below is what Go's
// `path.Clean` returns for that input, not what this module happens to compute.

describe('writeSetComparisonKey', () => {
  it('resolves a leading "./" — the server compares "./src/a.go" as "src/a.go"', () => {
    expect(writeSetComparisonKey('./src/a.go')).toBe('src/a.go')
  })

  it('resolves an embedded ".." against the segment before it', () => {
    expect(writeSetComparisonKey('src/x/../a.go')).toBe('src/a.go')
  })

  it('collapses a doubled slash the single-trailing-slash strip leaves behind', () => {
    // The client strips ONE trailing slash ("src/a//" → "src/a/"); path.Clean
    // finishes the job, which is why the raw text is not a safe comparison.
    expect(writeSetComparisonKey('src/a//')).toBe('src/a')
  })

  it('keeps a leading ".." on a relative path — there is nothing above the root to resolve it against', () => {
    expect(writeSetComparisonKey('../repo/src/a.go')).toBe('../repo/src/a.go')
  })

  it('resolves ".." segments that climb out of the path entirely', () => {
    expect(writeSetComparisonKey('src/../../a.go')).toBe('../a.go')
  })

  it('collapses a whole-workspace path to "."', () => {
    expect(writeSetComparisonKey('./')).toBe('.')
  })

  it('drops ".." above the root on an absolute path, as path.Clean does', () => {
    expect(writeSetComparisonKey('/../src/a.go')).toBe('/src/a.go')
  })
})

describe('validateWriteSetPath — spellings of an entry already declared', () => {
  // Each of these is the SAME file as the existing entry once the server
  // normalises it, so accepting it would let one member declare one file two
  // or three times — exactly the duplicate the guard exists to stop.
  it('rejects a "./"-prefixed spelling of an existing entry', () => {
    const result = validateWriteSetPath('./src/a.go', ['src/a.go'])
    expect(result.ok).toBe(false)
    expect(result.error).toBe('Already in the write set')
  })

  it('rejects an embedded-".." spelling of an existing entry', () => {
    expect(validateWriteSetPath('src/x/../a.go', ['src/a.go']).error).toBe('Already in the write set')
  })

  it('rejects a doubled-trailing-slash spelling of an existing entry', () => {
    expect(validateWriteSetPath('src/a//', ['src/a']).error).toBe('Already in the write set')
  })

  it('rejects a new entry that duplicates an existing "./"-prefixed one', () => {
    // The guard has to normalise BOTH sides: the existing entry may itself
    // have been stored in a non-canonical spelling (by an agent, or by an
    // earlier build of this input).
    expect(validateWriteSetPath('src/a.go', ['./src/a.go']).error).toBe('Already in the write set')
  })

  it('still accepts a "./"-prefixed path when nothing else declares that file', () => {
    expect(validateWriteSetPath('./src/schema.go', ['src/other.go']).ok).toBe(true)
  })
})

describe('validateWriteSetPath — paths the overlap check could never line up', () => {
  it('rejects a path that climbs above the workspace root', () => {
    // "../repo/src/a.go" and a sibling's "src/a.go" can name the identical
    // file, but path.Clean keeps the leading "..", so pathsOverlap
    // (pkg/plan/lint.go) reports no conflict and the plan runs both members
    // at that file in parallel.
    const result = validateWriteSetPath('../repo/src/a.go')
    expect(result.ok).toBe(false)
    expect(result.error).toBe('Use a path inside the workspace root — ".." climbs outside it')
  })

  it('rejects a bare ".."', () => {
    expect(validateWriteSetPath('..').ok).toBe(false)
  })

  it('rejects an embedded ".." that resolves to a climb above the root', () => {
    // Not a LEADING "..", so a guard on the raw text would let this through;
    // it cleans to "../a.go" all the same.
    const result = validateWriteSetPath('src/../../a.go')
    expect(result.ok).toBe(false)
    expect(result.error).toBe('Use a path inside the workspace root — ".." climbs outside it')
  })

  it('rejects a climbing path even when it would otherwise read as a duplicate', () => {
    expect(validateWriteSetPath('../src/a.go', ['src/a.go']).error).toBe(
      'Use a path inside the workspace root — ".." climbs outside it',
    )
  })

  it('rejects the whole workspace (".") — it overlaps nothing, so it protects nothing', () => {
    const result = validateWriteSetPath('./')
    expect(result.ok).toBe(false)
    expect(result.error).toBe('Name the files or folders this task writes, not the whole workspace')
  })

  it('still accepts an embedded ".." that stays inside the root', () => {
    expect(validateWriteSetPath('src/x/../a.go').ok).toBe(true)
  })
})
