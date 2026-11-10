// mapEffortCase.test.ts — the TypeScript half of the odd-case twin check
// (effort-levels-spec-r10.md, "Mapping table (DE-5)", row "Unranked name";
// SC-002). The Go half is pkg/providers/effortdialect/mapping_case_test.go;
// both read the one shared fixture below.

import { describe, it, expect } from 'vitest'
import { mapEffort } from './mapEffort'
import fixture from './fixtures/effort-mapping-case.json'

interface CaseRow {
  n: number
  source: string
  target: { levels: string[]; switch: boolean; reasons: boolean }
  value: string
  noteKind: string | null
  blocked: boolean
  nearest: string
  providerDefaultEscape: boolean
}

const cases = (fixture as { cases: CaseRow[] }).cases

// Traces to: effort-levels-spec-r10.md DE-5 "Unranked name" (exact only), SC-002.
describe('mapEffort — odd-case stored values are unranked names (shared Go/TS fixture)', () => {
  it('the shared fixture has rows', () => {
    expect(cases.length).toBeGreaterThan(0)
  })

  it.each(cases)('row $n: source "$source" maps exactly as the Go twin', (row) => {
    const got = mapEffort(row.source, { ...row.target })
    expect(got.value).toBe(row.value)
    expect(got.noteKind).toBe(row.noteKind)
    expect(got.blocked).toBe(row.blocked)
    expect(got.nearest).toBe(row.nearest)
    expect(got.providerDefaultEscape).toBe(row.providerDefaultEscape)
  })
})
