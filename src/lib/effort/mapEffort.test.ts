// mapEffort.test.ts — RED tests for the effort mapping table (TypeScript twin
// of pkg/agent/effort_red_mapping_d2_test.go; SC-002 requires identical
// results from both implementations for every shared D2 row).
//
// Spec: /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
//
// RED reason: src/lib/effort/mapEffort.ts is scaffolding and throws — every
// mapping and note assertion fails until GREEN implements the DE-5 table.
// The expected note strings are pinned verbatim from the spec's S-09 note
// column and the FR-058 per-turn texts, in exact parity with the Go test.

import { describe, it, expect } from 'vitest'
import {
  mapEffort,
  effortSwitchNote,
  effortTurnNote,
  type EffortTarget,
  type EffortMapping,
} from './mapEffort'
import fixture from './fixtures/effort-mapping-d2.json'

// Traces to: effort-levels-spec-r10.md D2 (dataset), SC-002 (one shared Go/TS fixture).

interface D2Target {
  levels: string[]
  switch: boolean
  reasons: boolean
}

interface D2Case {
  n: number
  source: string
  target: D2Target
  value: string
  noteKind: string | null
  blocked: boolean
  nearest: string
  providerDefaultEscape: boolean
}

const cases = (fixture as { cases: D2Case[] }).cases

function targetOf(row: D2Case): EffortTarget {
  return { levels: row.target.levels, switch: row.target.switch, reasons: row.target.reasons }
}

// The ranked level names (DE-5 rank order minimal 1 … max 6; "none" ranks 0
// but is Off, "on" is the boolean switch value, "default"/"provider_default"
// are unranked). Mirrors the Go test's effortRank usage.
const RANKED_LEVELS = ['minimal', 'low', 'medium', 'high', 'xhigh', 'max']

function isOffSource(source: string): boolean {
  return source === 'off' || source === 'none'
}

function isUnrankedSource(source: string): boolean {
  return (
    source !== '' &&
    source !== 'off' &&
    source !== 'none' &&
    source !== 'on' &&
    source !== 'provider_default' &&
    !RANKED_LEVELS.includes(source)
  )
}

// Traces to: effort-levels-spec-r10.md D2 rows 1-27, SC-002, FR-046.
describe('mapEffort — D2 fixture, all 27 rows (SC-002: identical Go/TS results)', () => {
  it.each(cases)('row $n: source "$source" maps to the shared expected result', (row) => {
    expect(cases).toHaveLength(27)
    const got: EffortMapping = mapEffort(row.source, targetOf(row))
    // SC-002 hard rules first, so a wrong result never hides behind a kind.
    if (!row.target.reasons || (row.target.levels.length === 0 && !row.target.switch)) {
      expect(got.value).not.toBe('none') // non-Off sources never map into none (D2 preamble)
    }
    if (isOffSource(row.source)) {
      expect(['low', 'medium', 'high', 'minimal', 'xhigh', 'max', 'on']).not.toContain(got.value) // Off never maps to a non-Off value (SC-002)
    }
    if (isUnrankedSource(row.source)) {
      expect(got.value).not.toBe('on') // an unranked name never maps to On (D2 row 16, S-09)
    }
    expect(got.value).toBe(row.value)
    expect(got.blocked).toBe(row.blocked)
    expect(got.nearest).toBe(row.nearest)
    expect(got.providerDefaultEscape).toBe(row.providerDefaultEscape)
    expect(got.noteKind).toBe(row.noteKind)
  })
})

// Traces to: effort-levels-spec-r10.md SC-002 (Off/unranked/none hard rules).
describe('mapEffort — SC-002 hard rules over every fixture row', () => {
  it('never maps an Off source (off/none) to a non-Off value', () => {
    const offRows = cases.filter((row) => isOffSource(row.source))
    expect(offRows.length).toBeGreaterThan(0)
    for (const row of offRows) {
      const got = mapEffort(row.source, targetOf(row))
      expect(['low', 'medium', 'high', 'minimal', 'xhigh', 'max', 'on']).not.toContain(got.value)
    }
  })

  it('never maps an unranked source name to On', () => {
    const unrankedRows = cases.filter((row) => isUnrankedSource(row.source))
    expect(unrankedRows.length).toBeGreaterThan(0)
    for (const row of unrankedRows) {
      const got = mapEffort(row.source, targetOf(row))
      expect(got.value).not.toBe('on')
    }
  })

  it('never maps a non-Off source into none', () => {
    for (const row of cases.filter((row) => !isOffSource(row.source))) {
      const got = mapEffort(row.source, targetOf(row))
      expect(got.value).not.toBe('none')
    }
  })
})

// Traces to: effort-levels-spec-r10.md S-09 (note column), FR-058 (picker note
// is a pure function of kind + model + source); strings in exact parity with
// pkg/agent/effort_red_mapping_d2_test.go::TestT_S1_Mapping_S09_NoteTexts.
describe('effortSwitchNote — S-09 exact picker note strings', () => {
  const model = 'deepseek/deepseek-v4.1-flash'

  it.each([
    { kind: 'mapped', source: 'medium', expected: `Effort set to Low. ${model} doesn't offer Medium.` },
    { kind: 'mapped', source: 'xhigh', expected: `Effort set to High. ${model} doesn't offer XHigh.` },
    { kind: 'mapped_on', source: 'on', expected: 'Effort set to Medium.' },
    { kind: 'switch_only', source: 'high', expected: `Effort set to On. ${model} only turns reasoning on or off.` },
    {
      kind: 'nothing_sendable',
      source: 'high',
      expected: `Effort set to Provider default. Omnipus can't change effort for ${model}.`,
    },
    {
      kind: 'unranked_default',
      source: 'default',
      expected: `Effort set to Provider default. ${model} doesn't offer that setting.`,
    },
    {
      kind: 'empty_candidate',
      source: 'high',
      expected: `Effort set to Provider default. ${model} offers no level here that High maps to.`,
    },
  ])('$kind note for source "$source" reads exactly as the spec pins', ({ kind, source, expected }) => {
    expect(effortSwitchNote(kind as never, model, source)).toBe(expected)
  })
})

// Traces to: effort-levels-spec-r10.md FR-058, S-72, S-48 (one-text rule), S-87,
// S-91 (one neutral not_supported text); parity with the Go turn-note test.
describe('effortTurnNote — exact per-turn note strings', () => {
  const model = 'm1'

  it.each([
    { kind: 'mapped', source: 'high', expected: `Sent as Low: ${model} doesn't offer High.` },
    { kind: 'not_supported', source: 'off', expected: `Effort not sent: ${model} can't turn reasoning off.` },
    {
      kind: 'empty_candidate',
      source: 'high',
      expected: `Effort not sent: ${model} offers no level here that high maps to.`,
    },
  ])('$kind turn note for source "$source" reads exactly as the spec pins', ({ kind, source, expected }) => {
    expect(effortTurnNote(kind as never, model, source)).toBe(expected)
  })
})

// Traces to: effort-levels-spec-r10.md S-14, D2 rows 20-21.
describe('mapEffort — S-14 cleared when the model does not reason', () => {
  it.each(['high', 'off'])('source "%s" clears to Not set and never blocks', (source) => {
    const got = mapEffort(source, { levels: [], switch: false, reasons: false })
    expect(got.value).toBe('')
    expect(got.blocked).toBe(false)
  })
})

// Traces to: effort-levels-spec-r10.md S-73, D2 row 22.
describe('mapEffort — S-73 nothing-sendable blocked step', () => {
  it('Off into a reasons-but-nothing-sendable target blocks with the provider-default escape', () => {
    const got = mapEffort('off', { levels: [], switch: false, reasons: true })
    expect(got.blocked).toBe(true)
    expect(got.providerDefaultEscape).toBe(true)
  })
})

// Traces to: effort-levels-spec-r10.md S-48, S-49, D2 rows 5 and 8.
describe('mapEffort — S-48/S-49 stale stored values', () => {
  it('stored Off with no Off on the target blocks at the picker with Low marked nearest (S-48, row 8)', () => {
    const got = mapEffort('off', { levels: ['low', 'high'], switch: false, reasons: true })
    expect(got.blocked).toBe(true)
    expect(got.nearest).toBe('low')
  })

  it('stored max maps to the nearest offered high with a note (S-49, row 5)', () => {
    const got = mapEffort('max', { levels: ['low', 'medium', 'high'], switch: false, reasons: true })
    expect(got.value).toBe('high')
    expect(got.noteKind).toBe('mapped')
  })
})

// Traces to: effort-levels-spec-r10.md S-72, D2 row 24.
describe('mapEffort — S-72 inherited level sent as nearest', () => {
  it("the agent's High inherited into a low, medium chat model maps to medium with the note", () => {
    const got = mapEffort('high', { levels: ['low', 'medium'], switch: false, reasons: true })
    expect(got.value).toBe('medium')
    expect(got.noteKind).toBe('mapped')
  })
})
