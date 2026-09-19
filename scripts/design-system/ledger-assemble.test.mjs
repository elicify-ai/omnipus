import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'

import { assembleLedger, loadJson, serializeDeterministic } from './ledger-assemble.mjs'
import { computeFingerprint } from './ledger-build.mjs'

const repoRoot = fileURLToPath(new URL('../../', import.meta.url))
const rehearsalDir = `${repoRoot}dist/design-system-baseline/cli-lanes/wave4-rehearsal/`
const baselineSchema = JSON.parse(readFileSync(`${repoRoot}design-system/enforcement/baseline.schema.json`, 'utf8'))
const ledgerSchema = JSON.parse(readFileSync(`${repoRoot}design-system/enforcement/ledger.schema.json`, 'utf8'))

function sha256(text) {
  return createHash('sha256').update(text).digest('hex')
}

// ---------------------------------------------------------------------------
// Small, hand-built fixture: two proposal-ledger entries (A, B), one
// calendar-ledger entry that duplicates B's fingerprint (must NOT be
// carried over) and one that's genuinely new (D, must be appended), a
// fragment exception that matches neither entry, and a currentLedger with
// its own reviewedBoundaries (must be carried over byte-for-byte).
// ---------------------------------------------------------------------------

function entry(ruleId, path, syntax, owner, extra = {}) {
  return {
    fingerprint: computeFingerprint(ruleId, path, syntax),
    ruleId,
    path,
    syntax,
    owner,
    replacement: 'replace it',
    expiryCheckpoint: 'C1',
    ...extra,
  }
}

function fpRow(ruleId, path, syntax, count = 1) {
  return { fingerprint: computeFingerprint(ruleId, path, syntax), ruleId, path, syntax, count }
}

function fixture() {
  const entryA = entry('css-colors/unregistered-token', 'src/a.css', 'var(--a)', 'C1 Lane 1: fixture')
  const entryB = entry('css-colors/unregistered-token', 'src/b.css', 'var(--b)', 'C1 Lane 1: fixture')
  const entryDCalendarOnly = entry('css-colors/unregistered-token', 'src/styles/fullcalendar-theme.css', 'var(--fc-d)', 'C1 Lane 2: Calendar and workspace management')

  const proposalLedger = { version: 1, entries: [entryA, entryB], exceptions: [] }
  const proposalBaseline = {
    version: 1,
    fingerprints: [fpRow('css-colors/unregistered-token', 'src/a.css', 'var(--a)'), fpRow('css-colors/unregistered-token', 'src/b.css', 'var(--b)')],
  }

  const fragment = {
    version: 1,
    entries: [],
    exceptions: [
      {
        ruleId: 'ts-colors/extension-boundary',
        path: 'src/widget.tsx',
        syntax: 'Widget#dom-style.color<-c',
        reason: 'user-authored colour, mechanically verified',
        owner: 'user-authored colours exception registry',
      },
    ],
  }

  const currentLedger = {
    version: 1,
    entries: [],
    exceptions: [],
    reviewedBoundaries: [
      { path: 'src/some/Fixture.test.tsx', kind: 'test', reason: 'test fixture, never a real paint site' },
      { path: 'src/generated/tokens.ts', kind: 'generated-tokens', reason: 'generated token surface' },
    ],
  }

  // Calendar ledger repeats entryB's fingerprint (must be dropped) and adds
  // a genuinely new one (must be appended, in the calendar file's order).
  const calendarLedger = {
    version: 1,
    entries: [
      entry('css-colors/unregistered-token', 'src/b.css', 'var(--b)', 'SHOULD NOT SURVIVE — duplicate of proposal entryB'),
      entryDCalendarOnly,
    ],
    exceptions: [],
  }
  const calendarBaseline = {
    version: 1,
    fingerprints: [fpRow('css-colors/unregistered-token', 'src/b.css', 'var(--b)'), fpRow('css-colors/unregistered-token', 'src/styles/fullcalendar-theme.css', 'var(--fc-d)')],
  }

  return { proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, entryA, entryB, entryDCalendarOnly }
}

test('assembleLedger — calendar entries are carried over, and a calendar entry duplicating a proposal fingerprint is not', () => {
  const { proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, entryA, entryB, entryDCalendarOnly } = fixture()
  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })

  assert.equal(result.ok, true, JSON.stringify(result.errors))
  // Proposal entries first, unchanged order, then only the genuinely new
  // calendar entry — the duplicate-fingerprint calendar entry never appears.
  assert.deepEqual(result.ledger.entries, [entryA, entryB, entryDCalendarOnly])
  assert.equal(result.ledger.entries.length, 3)
})

test('assembleLedger — baseline is the fingerprint union, deduplicated and sorted, with no duplicate calendar row', () => {
  const { proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline } = fixture()
  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })

  assert.equal(result.ok, true)
  const fps = result.baseline.fingerprints.map((f) => f.fingerprint)
  assert.equal(fps.length, 3) // a, b, calendar-d — not 4; b's calendar duplicate is dropped
  assert.deepEqual(fps, [...fps].sort())
})

test('assembleLedger — reviewedBoundaries is carried from the current ledger byte-for-byte', () => {
  const { proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline } = fixture()
  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })

  assert.equal(result.ok, true)
  assert.deepEqual(result.ledger.reviewedBoundaries, currentLedger.reviewedBoundaries)
  assert.equal(JSON.stringify(result.ledger.reviewedBoundaries), JSON.stringify(currentLedger.reviewedBoundaries))
})

test('assembleLedger — exceptions are exactly the fragment exceptions', () => {
  const { proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline } = fixture()
  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })

  assert.equal(result.ok, true)
  assert.deepEqual(result.ledger.exceptions, fragment.exceptions)
})

test('assembleLedger — refuses on a duplicate fingerprint across ledger entries', () => {
  const { proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline } = fixture()
  const dupEntry = entry('css-colors/unregistered-token', 'src/a.css', 'var(--a)', 'C1 Lane 1: fixture')
  const proposalLedger = { version: 1, entries: [dupEntry, { ...dupEntry, path: 'src/a-again.css' }], exceptions: [] }

  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })

  assert.equal(result.ok, false)
  assert.equal(result.ledger, null)
  assert.equal(result.baseline, null)
  assert.ok(result.errors.some((e) => e.includes('duplicate fingerprint across ledger entries')))
})

test('assembleLedger — refuses when an exception duplicates a ledger entry triple', () => {
  const { proposalLedger, proposalBaseline, currentLedger, calendarLedger, calendarBaseline, entryA } = fixture()
  const fragment = {
    version: 1,
    entries: [],
    exceptions: [{ ruleId: entryA.ruleId, path: entryA.path, syntax: entryA.syntax, reason: 'colliding exception', owner: 'someone' }],
  }

  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })

  assert.equal(result.ok, false)
  assert.equal(result.ledger, null)
  assert.ok(result.errors.some((e) => e.includes('exception duplicates a ledger entry')))
})

test('assembleLedger — refuses on ledger schema failure (missing required field)', () => {
  const { proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline } = fixture()
  const badEntry = entry('css-colors/unregistered-token', 'src/bad.css', 'var(--bad)', 'C1 Lane 1: fixture')
  delete badEntry.replacement // required by ledger.schema.json
  const proposalLedger = { version: 1, entries: [badEntry], exceptions: [] }

  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })

  assert.equal(result.ok, false)
  assert.equal(result.ledger, null)
  assert.equal(result.baseline, null)
  assert.ok(result.errors.some((e) => e.startsWith('ledger schema validation failed')))
})

test('assembleLedger — refuses on baseline schema failure (bad fingerprint pattern)', () => {
  const { proposalLedger, fragment, currentLedger, calendarLedger, calendarBaseline } = fixture()
  const proposalBaseline = { version: 1, fingerprints: [{ fingerprint: 'not-a-sha256', ruleId: 'r', path: 'p', syntax: 's', count: 1 }] }

  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })

  assert.equal(result.ok, false)
  assert.equal(result.baseline, null)
  assert.ok(result.errors.some((e) => e.startsWith('baseline schema validation failed')))
})

test('loadJson / serializeDeterministic — round-trips and ends with a trailing newline', () => {
  const text = serializeDeterministic({ z: 1, a: 2 })
  assert.ok(text.endsWith('\n'))
  assert.equal(JSON.parse(text).z, 1)
})

// ---------------------------------------------------------------------------
// Regression: running ledger-assemble on the exact wave-4 rehearsal inputs
// must reproduce the rehearsal's own ledger.candidate3.json and
// baseline.candidate3.json byte-identically.
// ---------------------------------------------------------------------------

test('regression — reproduces the rehearsal ledger.candidate3.json and baseline.candidate3.json byte-identically', () => {
  const proposalLedger = loadJson(`${rehearsalDir}ledger-out2/proposal-ledger.json`)
  const proposalBaseline = loadJson(`${rehearsalDir}ledger-out2/proposal-baseline.json`)
  const fragment = loadJson(`${rehearsalDir}fragment.json`)
  const currentLedger = loadJson(`${repoRoot}design-system/enforcement/ledger.json`)
  const calendarLedger = loadJson(`${repoRoot}design-system/enforcement/approved-fragments/calendar-token-references.ledger.json`)
  const calendarBaseline = loadJson(`${repoRoot}design-system/enforcement/approved-fragments/calendar-token-references.baseline.json`)

  const result = assembleLedger({ proposalLedger, proposalBaseline, fragment, currentLedger, calendarLedger, calendarBaseline, baselineSchema, ledgerSchema })
  assert.equal(result.ok, true, JSON.stringify(result.errors))

  const producedLedger = serializeDeterministic(result.ledger)
  const producedBaseline = serializeDeterministic(result.baseline)
  const expectedLedger = readFileSync(`${rehearsalDir}ledger.candidate3.json`, 'utf8')
  const expectedBaseline = readFileSync(`${rehearsalDir}baseline.candidate3.json`, 'utf8')

  assert.equal(sha256(producedLedger), sha256(expectedLedger), 'byte-identical to the rehearsal ledger.candidate3.json')
  assert.equal(producedLedger, expectedLedger)
  assert.equal(sha256(producedBaseline), sha256(expectedBaseline), 'byte-identical to the rehearsal baseline.candidate3.json')
  assert.equal(producedBaseline, expectedBaseline)
})
