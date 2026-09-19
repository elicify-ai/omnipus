import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'

import { computeFingerprint, validateLedger } from './ledger-validate.mjs'

const repoRoot = fileURLToPath(new URL('../../', import.meta.url))
const baselineSchema = JSON.parse(readFileSync(`${repoRoot}design-system/enforcement/baseline.schema.json`, 'utf8'))
const ledgerSchema = JSON.parse(readFileSync(`${repoRoot}design-system/enforcement/ledger.schema.json`, 'utf8'))

function row(ruleId, p, syntax, count = 1) {
  return { fingerprint: computeFingerprint(ruleId, p, syntax), ruleId, path: p, syntax, count }
}

function goodPair() {
  const r1 = row('spacing/off-scale', 'src/a.tsx', 'p-[7px]')
  const r2 = row('ts-colors/raw-color', 'src/b.tsx', '#ff0000')
  const baseline = { version: 1, fingerprints: [r1, r2] }
  const ledger = {
    version: 1,
    entries: [
      { fingerprint: r1.fingerprint, ruleId: r1.ruleId, path: r1.path, syntax: r1.syntax, owner: 'C1 Lane 1: X', replacement: 'fix it', expiryCheckpoint: 'C1' },
      { fingerprint: r2.fingerprint, ruleId: r2.ruleId, path: r2.path, syntax: r2.syntax, owner: 'C1 Lead—shared foundations: Y', replacement: 'fix it', expiryCheckpoint: 'C1' },
    ],
    exceptions: [],
  }
  return { baseline, ledger }
}

test('computeFingerprint — sha256 of JSON.stringify([ruleId, path, syntax])', () => {
  const fp = computeFingerprint('spacing/off-scale', 'src/x.tsx', 'p-[7px]')
  assert.match(fp, /^[a-f0-9]{64}$/)
  assert.equal(fp, computeFingerprint('spacing/off-scale', 'src/x.tsx', 'p-[7px]'))
})

test('validateLedger — schema validity: a well-formed pair validates clean', () => {
  const { baseline, ledger } = goodPair()
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema })
  assert.equal(result.ok, true)
  assert.equal(result.schema.baselineValid, true)
  assert.equal(result.schema.ledgerValid, true)
  assert.equal(result.fingerprintMismatches, 0)
  assert.equal(result.alignment.lengthMatch, true)
  assert.equal(result.alignment.rowIssues, 0)
})

test('validateLedger — schema validity: an extra unknown field fails baseline.schema.json (additionalProperties: false)', () => {
  const { baseline, ledger } = goodPair()
  baseline.fingerprints[0].extra = 'not allowed'
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema })
  assert.equal(result.ok, false)
  assert.equal(result.schema.baselineValid, false)
  assert.ok(result.errors.some((e) => e.includes('baseline.json validation failed')))
})

test('validateLedger — schema validity: a bad expiryCheckpoint value fails ledger.schema.json', () => {
  const { baseline, ledger } = goodPair()
  ledger.entries[0].expiryCheckpoint = 'C99'
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema })
  assert.equal(result.ok, false)
  assert.equal(result.schema.ledgerValid, false)
})

test('validateLedger — fingerprint recomputation: a mutated ledger row (path changed, fingerprint left stale) is caught', () => {
  const { baseline, ledger } = goodPair()
  ledger.entries[0].path = 'src/a-renamed.tsx'
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema })
  assert.equal(result.ok, false)
  assert.equal(result.fingerprintMismatches, 1)
  assert.ok(result.errors.some((e) => e.includes('fingerprint mismatch')))
})

test('validateLedger — alignment: baseline/ledger length mismatch is reported', () => {
  const { baseline, ledger } = goodPair()
  ledger.entries.pop()
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema })
  assert.equal(result.ok, false)
  assert.equal(result.alignment.lengthMatch, false)
})

test('validateLedger — alignment: same length but rows out of order is reported', () => {
  const { baseline, ledger } = goodPair()
  const [a, b] = baseline.fingerprints
  baseline.fingerprints = [b, a]
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema })
  assert.equal(result.ok, false)
  // Swapping a two-row array puts BOTH rows out of alignment (row 0 and row
  // 1 each now disagree with their ledger counterpart), not just one.
  assert.equal(result.alignment.rowIssues, 2)
})

test('validateLedger — bucket arithmetic: decidedByLead + decidedByLane sums to the total ledger entries', () => {
  const { baseline, ledger } = goodPair()
  const meta = { leadDecisionsApplied: { 'Some decision': { count: 1, paths: ['src/b.tsx'] } } }
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema, meta })
  assert.equal(result.ok, true)
  assert.deepEqual(result.bucketArithmetic, { totalEntries: 2, decidedByLead: 1, decidedByLane: 1, balances: true })
})

test('validateLedger — bucket arithmetic: sums across multiple decisions correctly', () => {
  const { baseline, ledger } = goodPair()
  const meta = { leadDecisionsApplied: { A: { count: 1, paths: ['x'] }, B: { count: 1, paths: ['y'] } } }
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema, meta })
  assert.equal(result.bucketArithmetic.decidedByLead, 2)
  assert.equal(result.bucketArithmetic.decidedByLane, 0)
  assert.equal(result.bucketArithmetic.balances, true)
})

test('validateLedger — bucket arithmetic: no estimate/guess when leadDecisionsApplied is missing — hard failure instead (no invented number)', () => {
  const { baseline, ledger } = goodPair()
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema, meta: {} })
  assert.equal(result.ok, false)
  assert.equal(result.bucketArithmetic, null)
  assert.ok(result.errors.some((e) => e.includes('leadDecisionsApplied')))
})

test('validateLedger — no meta at all simply skips bucket arithmetic (does not fail on its own)', () => {
  const { baseline, ledger } = goodPair()
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema })
  assert.equal(result.ok, true)
  assert.equal(result.bucketArithmetic, null)
})

test('validateLedger — regression: validating the real W4-ledger build output (if present) reports ok with 3378 entries', { skip: !presentEvidence() }, () => {
  const dir = `${repoRoot}dist/design-system-baseline/cli-lanes/fanout/W4-ledger/ledger-build-out/`
  const baseline = JSON.parse(readFileSync(`${dir}proposal-baseline.json`, 'utf8'))
  const ledger = JSON.parse(readFileSync(`${dir}proposal-ledger.json`, 'utf8'))
  const meta = JSON.parse(readFileSync(`${dir}proposal-meta.json`, 'utf8'))
  const result = validateLedger({ baseline, ledger, baselineSchema, ledgerSchema, meta })
  assert.equal(result.ok, true)
  assert.equal(ledger.entries.length, 3378)
  assert.equal(result.bucketArithmetic.decidedByLead, 227)
})

function presentEvidence() {
  try {
    readFileSync(`${repoRoot}dist/design-system-baseline/cli-lanes/fanout/W4-ledger/ledger-build-out/proposal-ledger.json`)
    return true
  } catch {
    return false
  }
}
