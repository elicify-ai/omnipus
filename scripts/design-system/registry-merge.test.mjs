import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'

import { mergeRegistryRules, tripleKey, serializeDeterministic } from './registry-merge.mjs'

const repoRoot = fileURLToPath(new URL('../../', import.meta.url))
const rehearsalDir = `${repoRoot}dist/design-system-baseline/cli-lanes/wave4-rehearsal/`
const toolsDir = `${repoRoot}dist/design-system-baseline/cli-lanes/tools/`

function loadJson(p) {
  return JSON.parse(readFileSync(p, 'utf8'))
}

function sha256(text) {
  return createHash('sha256').update(text).digest('hex')
}

// ---------------------------------------------------------------------------
// Small, hand-built fixture exercising every branch: a current rule that
// still matches a finding (kept), a current rule that no longer matches
// (stale, dropped and reported), a PASS draft rule, an approved NEEDS-READ
// (becomes a lead rule), an unapproved NEEDS-READ (blocker), a REJECT
// (blocker), and a duplicate [ruleId,path,syntax] appearing in both the kept
// set and the drafts (deduplicated, kept copy wins by array-order).
// ---------------------------------------------------------------------------

function fixture() {
  const audit = {
    debt: {
      fingerprints: [
        { ruleId: 'ts-colors/extension-boundary', path: 'src/a.tsx', syntax: 'A#kept-and-dup' },
        { ruleId: 'ts-colors/extension-boundary', path: 'src/b.tsx', syntax: 'B#draft-only' },
        { ruleId: 'ts-colors/extension-boundary', path: 'src/c.tsx', syntax: 'C#approved-needs-read' },
        { ruleId: 'ts-colors/extension-boundary', path: 'src/d.tsx', syntax: 'D#unapproved-needs-read' },
        { ruleId: 'ts-colors/extension-boundary', path: 'src/e.tsx', syntax: 'E#rejected' },
      ],
    },
  }

  const rulesDoc = {
    version: 1,
    source: 'fixture',
    rules: [
      {
        ruleId: 'ts-colors/extension-boundary',
        path: 'src/a.tsx',
        syntax: 'A#kept-and-dup',
        category: 'user-authored colours',
        owner: 'user-authored colours exception registry',
        reason: 'kept: still matches a finding',
      },
      {
        ruleId: 'ts-colors/extension-boundary',
        path: 'src/stale.tsx',
        syntax: 'Stale#gone',
        category: 'caller pass-through',
        owner: 'caller pass-through exception registry',
        reason: 'stale: no longer matches any finding',
      },
    ],
  }

  const draftsRaw = {
    rules: [
      {
        ruleId: 'ts-colors/extension-boundary',
        path: 'src/b.tsx',
        syntax: 'B#draft-only',
        category: 'user-authored colours',
        owner: 'user-authored colours exception registry',
        reason: 'PASS draft',
      },
      // Duplicate of the kept current rule above — must be deduplicated, the
      // kept copy (array position 0 in `all`) wins.
      {
        ruleId: 'ts-colors/extension-boundary',
        path: 'src/a.tsx',
        syntax: 'A#kept-and-dup',
        category: 'user-authored colours',
        owner: 'user-authored colours exception registry',
        reason: 'duplicate draft — must be dropped in favour of the kept rule',
      },
    ],
  }

  const verdictsRaw = [
    { verdict: 'PASS', ruleId: 'ts-colors/extension-boundary', path: 'src/b.tsx', syntax: 'B#draft-only' },
    { verdict: 'NEEDS-READ', ruleId: 'ts-colors/extension-boundary', path: 'src/c.tsx', syntax: 'C#approved-needs-read' },
    { verdict: 'NEEDS-READ', ruleId: 'ts-colors/extension-boundary', path: 'src/d.tsx', syntax: 'D#unapproved-needs-read' },
    { verdict: 'REJECT', ruleId: 'ts-colors/extension-boundary', path: 'src/e.tsx', syntax: 'E#rejected' },
  ]

  const dec = {
    date: '2026-09-20',
    decisions: [
      {
        path: 'src/c.tsx',
        syntax: 'C#approved-needs-read',
        decision: 'approve',
        category: 'user-authored colours',
        evidence: 'line 1: lead read this and approved it',
      },
    ],
  }

  return { audit, rulesDoc, draftsRaw, verdictsRaw, dec }
}

test('mergeRegistryRules — drops a stale current rule and reports it', () => {
  const { audit, rulesDoc, draftsRaw, verdictsRaw, dec } = fixture()
  // Isolate: no NEEDS-READ/REJECT verdicts, so this run only exercises kept/stale.
  const onlyPass = verdictsRaw.filter((v) => v.verdict === 'PASS')
  const result = mergeRegistryRules({ audit, rulesDoc, draftsRaw, verdictsRaw: onlyPass, dec })

  assert.equal(result.ok, true)
  assert.deepEqual(
    result.stale.map((r) => tripleKey(r)),
    [tripleKey({ ruleId: 'ts-colors/extension-boundary', path: 'src/stale.tsx', syntax: 'Stale#gone' })],
  )
  assert.equal(result.kept.length, 1)
  assert.equal(result.kept[0].path, 'src/a.tsx')
})

test('mergeRegistryRules — refuses (nothing written) on an unapproved NEEDS-READ', () => {
  const { audit, rulesDoc, verdictsRaw, dec } = fixture()
  // Only the two NEEDS-READ verdicts; approve just the first (c.tsx), leave
  // d.tsx unapproved.
  const verdicts = verdictsRaw.filter((v) => v.verdict === 'NEEDS-READ')
  const result = mergeRegistryRules({ audit, rulesDoc, draftsRaw: { rules: [] }, verdictsRaw: verdicts, dec })

  assert.equal(result.ok, false)
  assert.equal(result.rulesDoc, null)
  assert.ok(result.blockers.some((b) => b.startsWith('NEEDS-READ ') && b.includes('src/d.tsx')))
  // The approved one must NOT also appear as a blocker.
  assert.ok(!result.blockers.some((b) => b.includes('src/c.tsx')))
})

test('mergeRegistryRules — refuses (nothing written) on any REJECT', () => {
  const { audit, rulesDoc, verdictsRaw, dec } = fixture()
  const verdicts = verdictsRaw.filter((v) => v.verdict === 'REJECT')
  const result = mergeRegistryRules({ audit, rulesDoc, draftsRaw: { rules: [] }, verdictsRaw: verdicts, dec })

  assert.equal(result.ok, false)
  assert.equal(result.rulesDoc, null)
  assert.ok(result.blockers.some((b) => b.startsWith('REJECT ') && b.includes('src/e.tsx')))
})

test('mergeRegistryRules — refuses (nothing written) when a resulting rule matches no finding', () => {
  const { audit, rulesDoc, dec } = fixture()
  // An approved NEEDS-READ verdict for a triple the audit never reported —
  // by construction a lead rule always matches a finding when the pipeline
  // is used correctly, but this is the safety net: it must still be caught
  // and refused rather than silently registered.
  const decisions = {
    date: dec.date,
    decisions: [{ path: 'src/f.tsx', syntax: 'F#not-in-audit', decision: 'approve', category: 'user-authored colours', evidence: 'phantom finding' }],
  }
  const verdictsRaw = [{ verdict: 'NEEDS-READ', ruleId: 'ts-colors/extension-boundary', path: 'src/f.tsx', syntax: 'F#not-in-audit' }]
  const result = mergeRegistryRules({ audit, rulesDoc, draftsRaw: { rules: [] }, verdictsRaw, dec: decisions })

  assert.equal(result.ok, false)
  assert.equal(result.rulesDoc, null)
  assert.ok(result.blockers.some((b) => b.includes('rule matches no finding') && b.includes('src/f.tsx')))
})

test('mergeRegistryRules — deduplicates by [ruleId, path, syntax], keeping the earlier occurrence', () => {
  const { audit, rulesDoc, draftsRaw, dec } = fixture()
  const result = mergeRegistryRules({ audit, rulesDoc, draftsRaw, verdictsRaw: [], dec })

  assert.equal(result.ok, true)
  const aRules = result.rulesDoc.rules.filter((r) => r.path === 'src/a.tsx')
  assert.equal(aRules.length, 1)
  // The kept (current-rules) copy wins, not the drafts copy.
  assert.equal(aRules[0].reason, 'kept: still matches a finding')
  assert.deepEqual(
    result.dupes,
    [tripleKey({ ruleId: 'ts-colors/extension-boundary', path: 'src/a.tsx', syntax: 'A#kept-and-dup' })],
  )
})

test('mergeRegistryRules — an approved NEEDS-READ becomes a lead rule with the decision date/evidence/category', () => {
  const { audit, rulesDoc, dec } = fixture()
  const verdicts = [{ verdict: 'NEEDS-READ', ruleId: 'ts-colors/extension-boundary', path: 'src/c.tsx', syntax: 'C#approved-needs-read' }]
  const result = mergeRegistryRules({ audit, rulesDoc, draftsRaw: { rules: [] }, verdictsRaw: verdicts, dec })

  assert.equal(result.ok, true)
  assert.equal(result.leadRules.length, 1)
  const [leadRule] = result.leadRules
  assert.equal(leadRule.category, 'user-authored colours')
  assert.equal(leadRule.owner, 'user-authored colours exception registry')
  assert.equal(leadRule.reason, 'Lead read (2026-09-20): line 1: lead read this and approved it')
  assert.ok(result.rulesDoc.rules.some((r) => r.path === 'src/c.tsx'))
})

test('mergeRegistryRules — output rules are sorted deterministically by [ruleId, path, syntax]', () => {
  const { audit, rulesDoc, draftsRaw, dec } = fixture()
  const result = mergeRegistryRules({ audit, rulesDoc, draftsRaw, verdictsRaw: [], dec })
  const keys = result.rulesDoc.rules.map((r) => tripleKey(r))
  assert.deepEqual(keys, [...keys].sort((a, b) => a.localeCompare(b)))
})

test('serializeDeterministic — pretty-printed JSON with a trailing newline', () => {
  const text = serializeDeterministic({ b: 1, a: 2 })
  assert.ok(text.endsWith('\n'))
  assert.equal(JSON.parse(text).a, 2)
})

// ---------------------------------------------------------------------------
// Regression: running registry-merge on the exact wave-4 rehearsal inputs
// must reproduce the rehearsal's own registry-rules.merged.json
// byte-identically.
// ---------------------------------------------------------------------------

test('regression — reproduces the rehearsal registry-rules.merged.json byte-identically', () => {
  const audit = loadJson(`${rehearsalDir}audit-A.json`)
  const rulesDoc = loadJson(`${repoRoot}scripts/design-system/registry-rules.json`)
  const verdictsRaw = loadJson(`${rehearsalDir}verify/verdicts.json`)
  const draftsRaw = loadJson(`${rehearsalDir}verify/draft-rules.json`)
  const dec = loadJson(`${toolsDir}lead-needs-read-decisions.json`)

  const result = mergeRegistryRules({ audit, rulesDoc, verdictsRaw, draftsRaw, dec })
  assert.equal(result.ok, true)

  const produced = serializeDeterministic(result.rulesDoc)
  const expected = readFileSync(`${rehearsalDir}registry-rules.merged.json`, 'utf8')

  assert.equal(sha256(produced), sha256(expected), 'byte-identical to the rehearsal registry-rules.merged.json')
  assert.equal(produced, expected)
})
