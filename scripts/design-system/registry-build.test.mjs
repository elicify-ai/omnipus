import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import Ajv2020 from 'ajv/dist/2020.js'

import { buildRegistry, serializeDeterministic, isAllowedRuleId, tripleKey } from './registry-build.mjs'
import { infrastructureKind, isAllowedExceptionRuleId } from '../design-system-locks/audit.mjs'

const repoRoot = fileURLToPath(new URL('../../', import.meta.url))
const schemaPath = `${repoRoot}design-system/enforcement/ledger.schema.json`

function rule(overrides = {}) {
  return {
    ruleId: 'ts-colors/extension-boundary',
    path: 'src/components/example/Example.tsx',
    syntax: 'Example#dom-style.backgroundColor<-selectedColor',
    category: 'user-authored colours',
    reason: 'Category: user-authored colours — test fixture reason with enough detail to be non-empty.',
    owner: 'user-authored colours exception registry',
    ...overrides,
  }
}

function finding(overrides = {}) {
  return {
    fingerprint: 'f'.repeat(64),
    ruleId: 'ts-colors/extension-boundary',
    path: 'src/components/example/Example.tsx',
    syntax: 'Example#dom-style.backgroundColor<-selectedColor',
    count: 1,
    accepted: false,
    ...overrides,
  }
}

function auditWith(...fingerprints) {
  return { debt: { fingerprints } }
}

function rulesWith(...rules) {
  return { rules }
}

test('isAllowedRuleId — only */extension-boundary or exact ts-colors/unverified-governed-value pass', () => {
  assert.equal(isAllowedRuleId('ts-colors/extension-boundary'), true)
  assert.equal(isAllowedRuleId('spacing/extension-boundary'), true)
  assert.equal(isAllowedRuleId('typography/extension-boundary'), true)
  assert.equal(isAllowedRuleId('ts-colors/unverified-governed-value'), true)
  assert.equal(isAllowedRuleId('ts-colors/unsupported'), false)
  assert.equal(isAllowedRuleId('spacing/unsupported'), false)
  assert.equal(isAllowedRuleId('spacing/off-scale'), false)
  assert.equal(isAllowedRuleId('ts-colors/raw-color'), false)
  assert.equal(isAllowedRuleId('css-colors/unregistered-token'), false)
  assert.equal(isAllowedRuleId('typography/unsupported-text-utility'), false)
  assert.equal(isAllowedRuleId(''), false)
  assert.equal(isAllowedRuleId(undefined), false)
  // Not fooled by a ruleId that merely contains the substring.
  assert.equal(isAllowedRuleId('spacing/extension-boundary-ish'), false)
  assert.equal(isAllowedRuleId('not-ts-colors/unverified-governed-value'), false)
})

test('registry-build.mjs::isAllowedRuleId IS scripts/design-system-locks/audit.mjs::isAllowedExceptionRuleId — same function, not a copy that can drift', () => {
  // This is the fix for the reviewed gap: registry-build.mjs used to define
  // its own allow-list and scripts/design-system-locks/audit.mjs (the CI
  // gate) never checked it at all. Both now consult one shared function.
  // Asserting reference identity (not just matching behaviour on a few
  // inputs) is the strongest proof there is no second copy to fall out of
  // sync with the CI gate's ledger.exceptions check.
  assert.equal(isAllowedRuleId, isAllowedExceptionRuleId)
})

test('exact matching — a rule matches only on an identical [ruleId,path,syntax] triple', () => {
  const r = rule()
  const audit = auditWith(finding())
  const { fragment, report } = buildRegistry({ auditReport: audit, rulesDoc: rulesWith(r) })

  assert.equal(report.counts.matched, 1)
  assert.equal(report.counts.stale, 0)
  assert.deepEqual(fragment.exceptions, [
    { ruleId: r.ruleId, path: r.path, syntax: r.syntax, reason: r.reason, owner: r.owner },
  ])
})

test('exact matching — a near-miss on path, syntax, or ruleId alone does not match', () => {
  const r = rule()
  const nearMisses = [
    finding({ path: 'src/components/example/Other.tsx' }),
    finding({ syntax: 'Example#dom-style.backgroundColor<-otherColor' }),
    finding({ ruleId: 'spacing/extension-boundary' }),
  ]
  for (const f of nearMisses) {
    const { fragment, report } = buildRegistry({ auditReport: auditWith(f), rulesDoc: rulesWith(r) })
    assert.equal(report.counts.matched, 0, JSON.stringify(f))
    assert.equal(report.counts.stale, 1, JSON.stringify(f))
    assert.deepEqual(fragment.exceptions, [])
  }
})

test('refusal — a rule naming an unsupported ruleId is refused, never emitted, even though the audit has a matching finding', () => {
  const r = rule({ ruleId: 'ts-colors/unsupported', syntax: 'className' })
  const audit = auditWith(finding({ ruleId: 'ts-colors/unsupported', syntax: 'className', path: r.path }))
  const { fragment, report } = buildRegistry({ auditReport: audit, rulesDoc: rulesWith(r) })

  assert.deepEqual(fragment.exceptions, [])
  assert.equal(report.counts.matched, 0)
  assert.equal(report.counts.refused, 1)
  assert.equal(report.refused[0].ruleId, 'ts-colors/unsupported')
  assert.match(report.refused[0].refusalReason, /not an extension-boundary-class ruleId/)
})

test('refusal — every other debt ruleId (raw-color, off-scale, root-dependent, missing-safe-area-fallback) is refused', () => {
  const debtRuleIds = [
    'ts-colors/raw-color',
    'css-colors/unregistered-token',
    'spacing/off-scale',
    'spacing/root-dependent',
    'spacing/missing-safe-area-fallback',
    'typography/arbitrary-text-size',
    'typography/text-size-below-floor',
    'controls/raw-button',
    'design-system/status-mismatch',
  ]
  for (const ruleId of debtRuleIds) {
    const r = rule({ ruleId, syntax: 'irrelevant' })
    const audit = auditWith(finding({ ruleId, syntax: 'irrelevant', path: r.path }))
    const { fragment, report } = buildRegistry({ auditReport: audit, rulesDoc: rulesWith(r) })
    assert.deepEqual(fragment.exceptions, [], ruleId)
    assert.equal(report.counts.refused, 1, ruleId)
    assert.equal(report.counts.matched, 0, ruleId)
  }
})

test('refusal — a duplicate [ruleId,path,syntax] rule is refused after the first is kept', () => {
  const r = rule()
  const dup = rule()
  const audit = auditWith(finding())
  const { fragment, report } = buildRegistry({ auditReport: audit, rulesDoc: rulesWith(r, dup) })

  assert.equal(fragment.exceptions.length, 1)
  assert.equal(report.counts.matched, 1)
  assert.equal(report.counts.refused, 1)
  assert.match(report.refused[0].refusalReason, /duplicate/)
})

test('refusal — a malformed rule (missing reason/owner/category) is refused, not emitted', () => {
  const malformed = { ruleId: 'ts-colors/extension-boundary', path: 'src/x.tsx', syntax: 'X#y' }
  const { fragment, report } = buildRegistry({ auditReport: auditWith(), rulesDoc: rulesWith(malformed) })
  assert.deepEqual(fragment.exceptions, [])
  assert.equal(report.counts.refused, 1)
  assert.match(report.refused[0].refusalReason, /malformed rule/)
})

test('stale-rule reporting — a rule with no matching finding in the audit is reported stale, not emitted, not dropped silently', () => {
  const r = rule({ path: 'src/components/nowhere/Nowhere.tsx', syntax: 'Nowhere#dom-style.color<-x' })
  const { fragment, report } = buildRegistry({ auditReport: auditWith(), rulesDoc: rulesWith(r) })

  assert.deepEqual(fragment.exceptions, [])
  assert.equal(report.counts.stale, 1)
  assert.equal(report.stale[0].ruleId, r.ruleId)
  assert.equal(report.stale[0].path, r.path)
  assert.equal(report.stale[0].syntax, r.syntax)
})

test('uncovered reporting — an extension-boundary finding with no covering rule is listed uncovered, and matched findings are excluded from it', () => {
  const covered = rule()
  const uncoveredFinding = finding({ path: 'src/components/other/Other.tsx', syntax: 'Other#dom-style.color<-y' })
  const audit = auditWith(finding(), uncoveredFinding)
  const { report } = buildRegistry({ auditReport: audit, rulesDoc: rulesWith(covered) })

  assert.equal(report.counts.uncovered, 1)
  assert.equal(report.uncovered[0].path, 'src/components/other/Other.tsx')
  assert.equal(report.uncovered[0].syntax, 'Other#dom-style.color<-y')
  // The matched one must not also show up as uncovered.
  assert.ok(!report.uncovered.some((u) => u.syntax === covered.syntax && u.path === covered.path))
})

test('uncovered reporting — non-extension-boundary debt findings never appear in uncovered (they are not this builder’s registrable class)', () => {
  const audit = auditWith(finding({ ruleId: 'spacing/root-dependent', syntax: 'gap-2', path: 'src/components/x/X.tsx' }))
  const { report } = buildRegistry({ auditReport: audit, rulesDoc: rulesWith() })
  assert.equal(report.counts.uncovered, 0)
})

test('determinism — the same input produces byte-identical serialized output across repeated calls', () => {
  const rules = rulesWith(
    rule(),
    rule({ path: 'src/components/b/B.tsx', syntax: 'B#dom-style.color<-c', ruleId: 'spacing/extension-boundary' }),
    rule({ path: 'src/components/nostale/NoStale.tsx', syntax: 'NoStale#x<-y' }),
  )
  const audit = auditWith(
    finding(),
    finding({ path: 'src/components/b/B.tsx', syntax: 'B#dom-style.color<-c', ruleId: 'spacing/extension-boundary' }),
    finding({ path: 'src/components/zzz/Unrelated.tsx', syntax: 'Zzz#w<-v' }),
  )

  const run1 = buildRegistry({ auditReport: audit, rulesDoc: rules })
  const run2 = buildRegistry({ auditReport: audit, rulesDoc: rules })

  assert.equal(serializeDeterministic(run1.fragment), serializeDeterministic(run2.fragment))
  assert.equal(serializeDeterministic(run1.report), serializeDeterministic(run2.report))

  // Rule order in the source file must not matter: reversed input rules give the same fragment.
  const reversedRules = { rules: [...rules.rules].reverse() }
  const run3 = buildRegistry({ auditReport: audit, rulesDoc: reversedRules })
  assert.equal(serializeDeterministic(run1.fragment), serializeDeterministic(run3.fragment))
})

test('determinism — tripleKey is a stable, order-sensitive encoding', () => {
  assert.equal(tripleKey('a', 'b', 'c'), tripleKey('a', 'b', 'c'))
  assert.notEqual(tripleKey('a', 'b', 'c'), tripleKey('b', 'a', 'c'))
})

test('schema validity — the emitted fragment validates against the real design-system/enforcement/ledger.schema.json via Ajv2020', () => {
  const schema = JSON.parse(readFileSync(schemaPath, 'utf8'))
  const ajv = new Ajv2020({ allErrors: true })
  const validate = ajv.compile(schema)

  const rules = rulesWith(
    rule(),
    rule({ path: 'src/components/b/B.tsx', syntax: 'B#dom-style.color<-c', ruleId: 'spacing/extension-boundary' }),
  )
  const audit = auditWith(
    finding(),
    finding({ path: 'src/components/b/B.tsx', syntax: 'B#dom-style.color<-c', ruleId: 'spacing/extension-boundary' }),
  )
  const { fragment } = buildRegistry({ auditReport: audit, rulesDoc: rules })

  const valid = validate(fragment)
  assert.equal(valid, true, JSON.stringify(validate.errors, null, 2))
})

test('schema validity — the real registry-rules.json, run against a synthetic audit exposing every rule, produces a schema-valid fragment with zero refusals', () => {
  const rulesDocPath = fileURLToPath(new URL('./registry-rules.json', import.meta.url))
  const rulesDoc = JSON.parse(readFileSync(rulesDocPath, 'utf8'))

  const schema = JSON.parse(readFileSync(schemaPath, 'utf8'))
  const ajv = new Ajv2020({ allErrors: true })
  const validate = ajv.compile(schema)

  const audit = auditWith(...rulesDoc.rules.map((r) => finding({ ruleId: r.ruleId, path: r.path, syntax: r.syntax })))
  const { fragment, report } = buildRegistry({ auditReport: audit, rulesDoc })

  assert.equal(report.counts.refused, 0, JSON.stringify(report.refused, null, 2))
  assert.equal(report.counts.matched, rulesDoc.rules.length)
  assert.equal(validate(fragment), true, JSON.stringify(validate.errors, null, 2))

  // No duplicate [ruleId,path,syntax] triples in the shipped rules file itself.
  const seen = new Set()
  for (const r of rulesDoc.rules) {
    const key = tripleKey(r.ruleId, r.path, r.syntax)
    assert.equal(seen.has(key), false, `duplicate rule triple: ${key}`)
    seen.add(key)
  }

  // Every ruleId in the shipped rules file is an allowed, extension-boundary-class ruleId.
  for (const r of rulesDoc.rules) {
    assert.equal(isAllowedRuleId(r.ruleId), true, `${r.ruleId} at ${r.path} :: ${r.syntax} is not registrable`)
  }
})

test('accountFindings replay — every emitted exception would be applied by the real audit.mjs gate (infrastructureKind never fires on an extension-boundary/unverified-governed-value ruleId)', () => {
  // Imports the REAL scripts/design-system-locks/audit.mjs::infrastructureKind
  // — the exact gate accountFindings runs BEFORE checking exceptions; no
  // copy lives here to drift from it. An emitted exception must never have
  // a ruleId this function would reject as infrastructure debt, or the
  // ledger entry would never apply.
  const rulesDocPath = fileURLToPath(new URL('./registry-rules.json', import.meta.url))
  const rulesDoc = JSON.parse(readFileSync(rulesDocPath, 'utf8'))
  for (const r of rulesDoc.rules) {
    assert.equal(infrastructureKind(r.ruleId), null, `${r.ruleId} would be rejected by accountFindings before exceptions are even checked`)
  }
})
