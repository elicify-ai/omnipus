import test from 'node:test'
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { createHash } from 'node:crypto'
import { fileURLToPath } from 'node:url'

import { computeFingerprint, loadDecisions, applyLeadDecision, buildLedgerProposal, RULE_CHECKPOINT, EXTENSION_RULES } from './ledger-build.mjs'

const repoRoot = fileURLToPath(new URL('../../', import.meta.url))
const baselineSchema = JSON.parse(readFileSync(`${repoRoot}design-system/enforcement/baseline.schema.json`, 'utf8'))
const ledgerSchema = JSON.parse(readFileSync(`${repoRoot}design-system/enforcement/ledger.schema.json`, 'utf8'))

// ---------------------------------------------------------------------------
// Fixture: a small, hand-built audit + supporting-inputs set that exercises
// every branch of buildLedgerProposal — lane ownership, both lead-decision
// types, a reviewed boundary, an extension-boundary rule, a UI-catalog
// unresolved, an unassigned unresolved, an accepted (pre-approved calendar)
// finding, and a blocking list with a repeated (ruleId,path,syntax) identity
// (to exercise the occurrence-vs-identity distinction). This mirrors the
// live regression fixture used for the byte-identity proof against the L12
// script (see dist/design-system-baseline/cli-lanes/fanout/W4-ledger/), but
// is fully self-contained so it runs without any dist/ input.
// ---------------------------------------------------------------------------

function fixture() {
  const catalog = {
    entries: [
      { source: 'src/components/ui/button.tsx', classification: 'primitive' },
      { source: 'src/components/widgets/Foo.tsx', classification: 'composite-widget' },
    ],
  }
  const surfaces = { sourceOwners: [{ source: 'src/lib/widgets/Bar.tsx', lane: 3 }] }
  const boundariesFrag = { reviewedBoundaries: [{ path: 'src/test/Ignored.test.tsx', kind: 'test', reason: 'test file' }] }

  const calFp = computeFingerprint('css-colors/unregistered-token', 'src/styles/fullcalendar-theme.css', 'var(--fc-today-bg-color)')
  const calBaseline = {
    fingerprints: [{ fingerprint: calFp, ruleId: 'css-colors/unregistered-token', path: 'src/styles/fullcalendar-theme.css', syntax: 'var(--fc-today-bg-color)', count: 1 }],
  }

  const rows = [
    ['spacing/off-scale', 'src/lib/widgets/Bar.tsx', 'p-[7px]', 1, false], // lane 3 -> ledger
    ['ts-colors/raw-color', 'src/components/ui/button.tsx', '#ff0000', 2, false], // catalogClassification decision -> ledger
    ['controls/raw-button', 'src/lib/exact/Exact.ts', '<button>', 1, false], // exactPaths decision -> ledger
    ['spacing/off-scale', 'src/test/Ignored.test.tsx', 'gap-[5px]', 1, false], // reviewed boundary -> excluded
    ['ts-colors/extension-boundary', 'src/components/widgets/Foo.tsx', 'Foo#dom-style.color<-c', 1, false], // extension rule -> unresolved
    ['typography/arbitrary-text-size', 'src/components/widgets/Foo.tsx', 'text-[10px]', 1, false], // in catalog, no lane/decision -> unresolved UI
    ['css-colors/raw-color', 'src/lib/orphan/Orphan.ts', 'color:#123', 1, false], // no catalog, no lane -> unassigned
    ['css-colors/unregistered-token', 'src/styles/fullcalendar-theme.css', 'var(--fc-today-bg-color)', 1, true], // accepted
  ]
  const fingerprints = rows.map(([ruleId, p, syntax, count, accepted]) => ({ fingerprint: computeFingerprint(ruleId, p, syntax), ruleId, path: p, syntax, count, accepted }))
  const perRule = {}
  for (const f of fingerprints) perRule[f.ruleId] = (perRule[f.ruleId] || 0) + f.count

  const diag = {
    scannedFileCount: 10,
    debt: { fingerprints, perRule, acceptedCount: 1 },
    errors: [
      { code: 'unsupported', ruleId: 'ts-colors/unsupported', path: 'src/components/Weird.tsx', syntax: 'someExpr', message: 'm1' },
      { code: 'unsupported', ruleId: 'ts-colors/unsupported', path: 'src/components/Weird.tsx', syntax: 'someExpr', message: 'm1 duplicate occurrence' },
      { code: 'unsupported', ruleId: 'typography/unsupported', path: 'src/components/Other.tsx', syntax: 'otherExpr', message: 'm2' },
    ],
  }

  const decisionsDoc = {
    version: 1,
    decisions: [
      { name: 'Foundations', type: 'catalogClassification', classifications: ['primitive'], checkpoint: 'C1', owner: 'Lead—shared foundations: Primitives' },
      { name: 'ExactWidget', type: 'exactPaths', paths: ['src/lib/exact/Exact.ts'], checkpoint: 'C4', owner: 'Lead—shared foundations: Exact widget' },
    ],
  }

  return { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions: loadDecisions(decisionsDoc) }
}

test('computeFingerprint — sha256 of JSON.stringify([ruleId, path, syntax]), matches baseline.schema.json fingerprint format', () => {
  const fp = computeFingerprint('spacing/off-scale', 'src/x.tsx', 'p-[7px]')
  const expected = createHash('sha256').update(JSON.stringify(['spacing/off-scale', 'src/x.tsx', 'p-[7px]'])).digest('hex')
  assert.equal(fp, expected)
  assert.match(fp, /^[a-f0-9]{64}$/)
})

test('computeFingerprint — differs when any one of the three inputs changes', () => {
  const base = computeFingerprint('r', 'p', 's')
  assert.notEqual(computeFingerprint('r2', 'p', 's'), base)
  assert.notEqual(computeFingerprint('r', 'p2', 's'), base)
  assert.notEqual(computeFingerprint('r', 'p', 's2'), base)
})

test('loadDecisions — catalogClassification and exactPaths decisions match as designed', () => {
  const decisions = loadDecisions({
    decisions: [
      { name: 'A', type: 'catalogClassification', classifications: ['primitive', 'composite'], checkpoint: 'C1', owner: 'Lead A' },
      { name: 'B', type: 'exactPaths', paths: ['src/only/This.ts'], checkpoint: 'C2', owner: 'Lead B' },
    ],
  })
  assert.equal(applyLeadDecision('src/x.tsx', { classification: 'primitive' }, decisions).name, 'A')
  assert.equal(applyLeadDecision('src/x.tsx', { classification: 'foundation' }, decisions), null)
  assert.equal(applyLeadDecision('src/only/This.ts', undefined, decisions).name, 'B')
  assert.equal(applyLeadDecision('src/other/That.ts', undefined, decisions), null)
})

test('loadDecisions — first match wins on ties (array order)', () => {
  const decisions = loadDecisions({
    decisions: [
      { name: 'First', type: 'exactPaths', paths: ['src/x.tsx'], checkpoint: 'C1', owner: 'X' },
      { name: 'Second', type: 'exactPaths', paths: ['src/x.tsx'], checkpoint: 'C2', owner: 'Y' },
    ],
  })
  assert.equal(applyLeadDecision('src/x.tsx', undefined, decisions).name, 'First')
})

test('loadDecisions — rejects a decision with an unknown type', () => {
  assert.throws(() => loadDecisions({ decisions: [{ name: 'Bad', type: 'nonsense', checkpoint: 'C1', owner: 'X' }] }), /unknown type/)
})

test('loadDecisions — rejects a catalogClassification decision with no classifications', () => {
  assert.throws(() => loadDecisions({ decisions: [{ name: 'Bad', type: 'catalogClassification', checkpoint: 'C1', owner: 'X' }] }), /classifications/)
})

test('the real design-system/enforcement/ledger-decisions.json loads without error and keeps the six original lead decisions first', () => {
  const doc = JSON.parse(readFileSync(`${repoRoot}design-system/enforcement/ledger-decisions.json`, 'utf8'))
  const decisions = loadDecisions(doc)
  // The six decisions ported from L12 stay first and unchanged (first match
  // wins, so their order matters); lead decisions added since follow them.
  assert.ok(decisions.length >= 6)
  assert.deepEqual(
    decisions.slice(0, 6).map((d) => d.name),
    [
      'Shared catalog foundations (foundation/primitive/composite classification)',
      'Lane 2: Calendar CSS and eventMapping',
      'Lane 1: React Flow theme CSS',
      'Shared foundations: Global stylesheets',
      'D4 status foundations',
      'Lane 6: Provider model groups',
    ],
  )
  // Spot-check the two decision shapes against known-matching paths from the
  // original L12 FINAL-REPORT.md.
  assert.ok(applyLeadDecision('src/components/ui/button.tsx', { classification: 'primitive' }, decisions))
  assert.ok(applyLeadDecision('src/styles/fullcalendar-theme.css', undefined, decisions))
  assert.ok(applyLeadDecision('src/lib/providerModelGroups.ts', undefined, decisions))
  assert.equal(applyLeadDecision('src/completely/unrelated/File.ts', undefined, decisions), null)
  // Every later decision is well-formed and names a schema checkpoint.
  for (const d of decisions.slice(6)) {
    assert.ok(d.name && d.owner, `decision missing name/owner: ${JSON.stringify(d)}`)
    assert.match(d.checkpoint, /^C[1-6]$/)
  }
  // Wave-4 additions resolve their paths (spot-check one per decision).
  assert.ok(applyLeadDecision('src/components/ui/model-selector.tsx', undefined, decisions))
  assert.ok(applyLeadDecision('src/assets/logo/omnipus-logo.svg', undefined, decisions))
  assert.ok(applyLeadDecision('src/components/layout/Sidebar.tsx', undefined, decisions))
})

test('buildLedgerProposal — schema validity: output baseline and ledger validate against the real schemas', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  // buildLedgerProposal throws on schema failure, so reaching here already
  // proves validity — assert the documents exist and are shaped right too.
  assert.equal(result.proposalBaseline.version, 1)
  assert.equal(result.proposalLedger.version, 1)
  assert.ok(Array.isArray(result.proposalBaseline.fingerprints))
  assert.ok(Array.isArray(result.proposalLedger.entries))
})

test('buildLedgerProposal — every ledger fingerprint recomputes from its own [ruleId, path, syntax]', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  assert.ok(result.proposalLedger.entries.length > 0)
  for (const entry of result.proposalLedger.entries) {
    assert.equal(computeFingerprint(entry.ruleId, entry.path, entry.syntax), entry.fingerprint)
  }
  // baseline rows line up 1:1 with ledger rows, same fingerprint, same order.
  assert.equal(result.proposalBaseline.fingerprints.length, result.proposalLedger.entries.length)
  for (let i = 0; i < result.proposalBaseline.fingerprints.length; i += 1) {
    assert.equal(result.proposalBaseline.fingerprints[i].fingerprint, result.proposalLedger.entries[i].fingerprint)
  }
})

test('buildLedgerProposal — bucket arithmetic: every debt fingerprint is accounted for exactly once', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  const s = result.proposalStats
  const accountedForNewDebt = s.proposal.ledger.entries + s.proposal.unresolved.uiCatalog + s.proposal.unresolved.unassignedPaths + s.proposal.unresolved.runtimeExtensionBoundaries
  // 1 boundary-excluded finding ('src/test/Ignored.test.tsx') is not new debt
  // and not unresolved either — it is neither ledgered nor blocking, so it
  // is accounted for only in the fingerprints.total - accepted - ledger -
  // unresolved - boundaryExcluded reconciliation below, matching the
  // original build-with-decisions.mjs `accounted` check.
  const boundaryExcluded = 1
  assert.equal(s.diagnostic.fingerprints.total, s.diagnostic.fingerprints.accepted + accountedForNewDebt + boundaryExcluded)
  // decidedByLead + lane-based rules = total ledger entries
  assert.equal(s.proposal.ledger.decidedByLead, 2)
  assert.equal(s.proposal.ledger.entries - s.proposal.ledger.decidedByLead, 1)
})

test('buildLedgerProposal — lead-decision checkpoint overrides the rule-family default checkpoint', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  const exact = result.proposalLedger.entries.find((e) => e.path === 'src/lib/exact/Exact.ts')
  // RULE_CHECKPOINT['controls/raw-button'] is 'C2', but the ExactWidget
  // decision in the fixture assigns 'C4' — the decision must win.
  assert.equal(RULE_CHECKPOINT['controls/raw-button'], 'C2')
  assert.equal(exact.expiryCheckpoint, 'C4')
  assert.equal(exact.owner, 'C4 Lead—shared foundations: Exact widget')
})

test('buildLedgerProposal — lane-owned entry gets the lane checkpoint and a lane-named owner', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  const bar = result.proposalLedger.entries.find((e) => e.path === 'src/lib/widgets/Bar.tsx')
  assert.equal(bar.expiryCheckpoint, 'C1')
  assert.equal(bar.owner, 'C1 Lane 3: Chat, sessions and conversation surfaces')
})

test('buildLedgerProposal — a reviewed-boundary path is excluded from both ledger and baseline', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  assert.equal(result.proposalLedger.entries.some((e) => e.path === 'src/test/Ignored.test.tsx'), false)
  assert.equal(result.proposalBaseline.fingerprints.some((f) => f.path === 'src/test/Ignored.test.tsx'), false)
})

test('buildLedgerProposal — an extension-boundary rule is unresolved (never ledgered), even though EXTENSION_RULES.has() is true', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  assert.equal(EXTENSION_RULES.has('ts-colors/extension-boundary'), true)
  assert.equal(result.proposalLedger.entries.some((e) => e.ruleId === 'ts-colors/extension-boundary'), false)
  assert.equal(result.proposalStats.proposal.unresolved.runtimeExtensionBoundaries, 1)
})

test('buildLedgerProposal — unsupported findings are always blocking, never in the ledger or the baseline', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  assert.equal(result.proposalBlocking.unsupported.length, 3)
  assert.equal(result.proposalStats.proposal.blocking, 3)
  // 3 occurrences, 2 unique (ruleId,path,syntax) identities — the Weird.tsx
  // row appears twice with an identical triple.
  assert.equal(result.proposalStats.diagnostic.fingerprints.unsupportedIdentities, 2)
  const ledgerAndBaselinePaths = new Set([...result.proposalLedger.entries.map((e) => e.path), ...result.proposalBaseline.fingerprints.map((f) => f.path)])
  assert.equal(ledgerAndBaselinePaths.has('src/components/Weird.tsx'), false)
  assert.equal(ledgerAndBaselinePaths.has('src/components/Other.tsx'), false)
})

test('buildLedgerProposal — a fingerprint that does not recompute to its stated value aborts the build', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  diag.debt.fingerprints[0].fingerprint = 'f'.repeat(64)
  assert.throws(() => buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema }), /fingerprint mismatch/)
})

test('buildLedgerProposal — a perRule total that disagrees with the fingerprint counts aborts the build', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  diag.debt.perRule['spacing/off-scale'] = 999
  assert.throws(() => buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema }), /perRule mismatch/)
})

test('buildLedgerProposal — an accepted finding not present in the approved calendar fragment aborts the build', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  calBaseline.fingerprints = []
  assert.throws(() => buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema }), /approved calendar fragment/)
})

test('buildLedgerProposal — regression snapshot: fixture output is byte-stable (sha256 of the four proposal documents)', () => {
  const { diag, boundariesFrag, calBaseline, surfaces, catalog, decisions } = fixture()
  const result = buildLedgerProposal({ diag, boundariesFrag, calBaseline, surfaces, catalog, decisions, baselineSchema, ledgerSchema })
  const combined = JSON.stringify({ b: result.proposalBaseline, l: result.proposalLedger, bl: result.proposalBlocking, s: result.proposalStats })
  const hash = createHash('sha256').update(combined).digest('hex')
  // Golden value computed once from this exact fixture and locked here. A
  // change to this hash means the builder's output for identical input
  // changed — that must be a deliberate, reviewed change, never incidental.
  assert.equal(hash, '132c823ae98f0ea6aefe42cbfdb6a981e18a19e07cda5b4778033874bdc5d963')
})
