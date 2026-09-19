import test from 'node:test'
import assert from 'node:assert/strict'

import { computeByCheckpoint, computeByLane, computeByRuleFamily, computeBlockingReconciliation, computeUnresolvedBuckets, buildFounderSummary } from './ledger-founder-summary.mjs'

function fixture() {
  const ledger = {
    version: 1,
    entries: [
      { fingerprint: 'a'.repeat(64), ruleId: 'spacing/off-scale', path: 'src/a.tsx', syntax: 'p-[7px]', owner: 'C1 Lane 1: Workspace board', replacement: 'fix', expiryCheckpoint: 'C1' },
      { fingerprint: 'b'.repeat(64), ruleId: 'spacing/off-scale', path: 'src/b.tsx', syntax: 'p-[6px]', owner: 'C1 Lane 1: Workspace board', replacement: 'fix', expiryCheckpoint: 'C1' },
      { fingerprint: 'c'.repeat(64), ruleId: 'ts-colors/raw-color', path: 'src/c.tsx', syntax: '#fff', owner: 'C1 Lead—shared foundations: Primitives', replacement: 'fix', expiryCheckpoint: 'C1' },
      { fingerprint: 'd'.repeat(64), ruleId: 'controls/raw-button', path: 'src/d.tsx', syntax: '<button>', owner: 'C2 Lane 3: Chat', replacement: 'fix', expiryCheckpoint: 'C2' },
    ],
    exceptions: [],
  }
  const blocking = {
    version: 1,
    unsupported: [
      { computedFingerprint: 'x'.repeat(64), ruleId: 'ts-colors/unsupported', path: 'src/weird.tsx', syntax: 'someExpr', classification: 'unassigned', message: 'm1' },
      { computedFingerprint: 'x'.repeat(64), ruleId: 'ts-colors/unsupported', path: 'src/weird.tsx', syntax: 'someExpr', classification: 'unassigned', message: 'm1 again' },
      { computedFingerprint: 'y'.repeat(64), ruleId: 'typography/unsupported', path: 'src/other.tsx', syntax: 'otherExpr', classification: 'ui-catalog', message: 'm2' },
      { computedFingerprint: 'z'.repeat(64), ruleId: 'spacing/unsupported', path: 'src/third.tsx', syntax: 'thirdExpr', classification: 'lane-1', message: 'm3' },
    ],
  }
  const stats = {
    diagnostic: { scannedFileCount: 10, fingerprints: { total: 12, accepted: 1, unsupportedIdentities: 3 } },
    proposal: {
      ledger: { entries: 4, decidedByLead: 1 },
      unresolved: { uiCatalog: 2, unassignedPaths: 1, runtimeExtensionBoundaries: 5 },
      blocking: 4,
    },
  }
  const meta = { timestamp: '2026-09-19T17:52:56.187Z', diagnosticInput: 'x', diagnosticHash: 'y', gitHead: 'z', leadDecisionsApplied: {} }
  return { ledger, blocking, stats, meta }
}

test('computeByCheckpoint — sums ledger entries per expiryCheckpoint', () => {
  const { ledger } = fixture()
  assert.deepEqual(computeByCheckpoint(ledger), { C1: 3, C2: 1 })
})

test('computeByLane — parses the lane number out of the owner string; falls back to shared-foundation', () => {
  const { ledger } = fixture()
  assert.deepEqual(computeByLane(ledger), { 'Lane 1': 2, 'Lead—shared-foundation': 1, 'Lane 3': 1 })
})

test('computeByRuleFamily — groups by the part of ruleId before the slash', () => {
  const { ledger } = fixture()
  assert.deepEqual(computeByRuleFamily(ledger), { spacing: 2, 'ts-colors': 1, controls: 1 })
})

test('computeUnresolvedBuckets — bucket arithmetic sums to the stated total', () => {
  const { stats } = fixture()
  const buckets = computeUnresolvedBuckets(stats)
  assert.equal(buckets.uiCatalog, 2)
  assert.equal(buckets.unassignedPaths, 1)
  assert.equal(buckets.runtimeExtensionBoundaries, 5)
  assert.equal(buckets.total, buckets.uiCatalog + buckets.unassignedPaths + buckets.runtimeExtensionBoundaries)
  assert.equal(buckets.total, 8)
})

test('computeBlockingReconciliation — counts occurrences (rows) vs unique identities (distinct computedFingerprint)', () => {
  const { blocking } = fixture()
  const r = computeBlockingReconciliation(blocking)
  assert.equal(r.occurrences, 4)
  assert.equal(r.uniqueIdentities, 3)
  assert.equal(r.repeatingIdentities, 1)
  assert.equal(r.extraOccurrences, 1)
  // occurrences - extraOccurrences === uniqueIdentities, always
  assert.equal(r.occurrences - r.extraOccurrences, r.uniqueIdentities)
})

test('computeBlockingReconciliation — no repeats when every identity is unique', () => {
  const r = computeBlockingReconciliation({ unsupported: [{ computedFingerprint: 'a' }, { computedFingerprint: 'b' }] })
  assert.equal(r.occurrences, 2)
  assert.equal(r.uniqueIdentities, 2)
  assert.equal(r.repeatingIdentities, 0)
  assert.equal(r.extraOccurrences, 0)
})

test('buildFounderSummary — reconciles the stats-file identity count and the recomputed occurrence count (43 vs 47 in production data, 3 vs 4 here)', () => {
  const { ledger, blocking, stats, meta } = fixture()
  const summary = buildFounderSummary({ ledger, blocking, stats, meta })
  assert.match(summary, /\*\*4 occurrences\*\*/)
  assert.match(summary, /\*\*3 unique identities\*\*/)
  assert.match(summary, /1 of those 3 identities show up more than once, adding 1 extra occurrence/)
})

test('buildFounderSummary — throws instead of publishing a contradiction when proposal-stats.json disagrees with proposal-blocking.json', () => {
  const { ledger, blocking, stats, meta } = fixture()
  stats.proposal.blocking = 999
  assert.throws(() => buildFounderSummary({ ledger, blocking, stats, meta }), /blocking count mismatch/)
})

test('buildFounderSummary — throws when the identity count in stats disagrees with the recomputed identity count', () => {
  const { ledger, blocking, stats, meta } = fixture()
  stats.diagnostic.fingerprints.unsupportedIdentities = 999
  assert.throws(() => buildFounderSummary({ ledger, blocking, stats, meta }), /unsupported-identity count mismatch/)
})

test('buildFounderSummary — surfaces all three unresolved buckets by name, with their counts, and states the runtime-extension-boundary share', () => {
  const { ledger, blocking, stats, meta } = fixture()
  const summary = buildFounderSummary({ ledger, blocking, stats, meta })
  assert.match(summary, /Runtime extension boundaries awaiting registration \| 5/)
  assert.match(summary, /UI catalog, no lane owner yet \| 2/)
  assert.match(summary, /Unassigned \| 1/)
  assert.match(summary, /62\.5% of the unresolved items \(5 of 8\) are runtime extension boundaries/)
})

test('buildFounderSummary — the "Generated" date is exactly proposal-meta.json\'s own timestamp, not a freshly computed date', () => {
  const { ledger, blocking, stats, meta } = fixture()
  const summary = buildFounderSummary({ ledger, blocking, stats, meta })
  assert.match(summary, /\*\*Generated:\*\* 2026-09-19T17:52:56\.187Z/)
})

test('buildFounderSummary — contains no ISO-date string other than the one supplied in proposal-meta.json', () => {
  const { ledger, blocking, stats, meta } = fixture()
  const summary = buildFounderSummary({ ledger, blocking, stats, meta })
  const isoDatePattern = /\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?Z/g
  const found = summary.match(isoDatePattern) || []
  for (const d of found) assert.equal(d, meta.timestamp, `unexpected date string in summary: ${d}`)
  assert.ok(found.length >= 1, 'expected at least the meta timestamp to appear')
})

test('buildFounderSummary — contains no free-form "due at <deadline>" or release-version deadline language', () => {
  const { ledger, blocking, stats, meta } = fixture()
  const summary = buildFounderSummary({ ledger, blocking, stats, meta })
  assert.doesNotMatch(summary, /due at freeze/i)
  assert.doesNotMatch(summary, /v0\.\d/)
})

test('buildFounderSummary — every owner name in the "By Lane Ownership" table traces to a ledger entry owner string, none invented', () => {
  const { ledger, blocking, stats, meta } = fixture()
  const summary = buildFounderSummary({ ledger, blocking, stats, meta })
  const laneKeys = Object.keys(computeByLane(ledger))
  for (const lane of laneKeys) {
    assert.ok(summary.includes(lane), `expected summary to mention lane label "${lane}"`)
  }
})

test('buildFounderSummary — is one document, not per-entry lists (no individual file paths from the ledger appear)', () => {
  const { ledger, blocking, stats, meta } = fixture()
  const summary = buildFounderSummary({ ledger, blocking, stats, meta })
  for (const entry of ledger.entries) {
    assert.equal(summary.includes(entry.path), false, `summary should not list individual path ${entry.path}`)
  }
})
