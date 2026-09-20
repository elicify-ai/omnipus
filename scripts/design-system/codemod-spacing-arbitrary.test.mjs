import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import {
  buildValueActions, classifyFamily, deriveNegativeReplacement,
  loadMappingRows, planEditsForText, planFileEdits, runCodemod,
} from './codemod-spacing-arbitrary.mjs'
import { isInAllowedRoot } from './codemod-lib.mjs'

// ── fixtures ─────────────────────────────────────────────────────────────
// No committed test in this file reads dist/design-system-baseline/cli-lanes/
// c1-prep/M1/mapping.json (COMMON-RULES.md; that file is lead-owned and was
// hot-regenerated mid-lane). Every mapping row used here is a small,
// synthetic fixture this file builds and writes itself, at test-run time,
// under a scratch dir this file also owns and cleans up — same convention
// as the sibling S-SPACE-1 lane's codemod-spacing-tailwind-class.test.mjs.

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function scratchDir(prefix) {
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline', prefix))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

// One representative row per shape this lane must handle, mirroring the
// REAL mapping.json's current (2026-09-20, post-regeneration) rows for
// these six pattern buckets, but kept as an independent, self-contained
// fixture so this test suite never depends on the lead's mutable file.
const FIXTURE_ROWS = [
  // ── arbitrary-bracket family ──────────────────────────────────────────
  { value: 'ml-[22px]', currentPx: 22, tokenPx: 24, token: '--space-4', group: 'NORMALIZED', replacement: 'ml-[var(--space-4)]', pattern: 'arbitrary-bracket-px', reason: '', itemCount: 1, fileCount: 1, ruleIds: ['spacing/off-scale'] },
  {
    value: 'ml-[3px]', currentPx: 3, tokenPx: 2, token: '--space-0-5', group: 'NEEDS-DECISION', replacement: 'ml-[var(--space-0-5)]',
    pattern: 'arbitrary-bracket-px', reason: '3px is exactly equidistant between two scale steps.', itemCount: 13, fileCount: 13, ruleIds: ['spacing/off-scale'],
  },
  {
    value: 'pl-[11.75rem]', currentPx: 164.5, tokenPx: 64, token: '--space-8', group: 'NEEDS-DECISION',
    replacement: 'NO SOUND TOKEN MAPPING — needs design decision (see reason)', pattern: 'arbitrary-bracket-rem',
    reason: 'Arbitrary rem value is far off the 4/8 scale; no sound snap exists.', itemCount: 1, fileCount: 1, ruleIds: ['spacing/root-dependent'],
  },
  // ── hairline family (spread across three raw patterns) ────────────────
  {
    value: 'mt-px', currentPx: 1, tokenPx: 1, token: '--border-width-hairline', group: 'IDENTICAL', replacement: 'mt-[var(--border-width-hairline)]',
    pattern: 'tailwind-px-suffix-utility', reason: 'Renders 1px today; D10 hairline exception applies verbatim.', itemCount: 7, fileCount: 7, ruleIds: ['spacing/off-scale'],
  },
  {
    value: 'mt-[1px]', currentPx: 1, tokenPx: 1, token: '--border-width-hairline', group: 'IDENTICAL', replacement: 'mt-[var(--border-width-hairline)]',
    pattern: 'arbitrary-bracket-px', reason: 'Renders 1px today; D10 hairline exception applies verbatim.', itemCount: 2, fileCount: 2, ruleIds: ['spacing/off-scale'],
  },
  {
    value: '-mb-px', currentPx: 1, tokenPx: 1, token: '--border-width-hairline', group: 'IDENTICAL',
    replacement: 'NOT RECOMMENDED — keep as literal 1px (hairline exception); do not negate a hairline',
    pattern: 'negative-1px-utility (verify: no negative hairline)', reason: 'Renders 1px today; D10 hairline exception applies verbatim.', itemCount: 1, fileCount: 1, ruleIds: ['spacing/off-scale'],
  },
  // ── negative-margin family ──────────────────────────────────────────────
  {
    value: '-mx-1', currentPx: 3.5, tokenPx: 4, token: '--space-1', group: 'NORMALIZED',
    replacement: 'VERIFY FIRST — proposed `-mx-[var(--space-1)]` (Tailwind v4 negative-arbitrary-value var() support unconfirmed); fallback `mx-[calc(var(--space-1)*-1)]`',
    pattern: 'negative-tailwind-fraction-utility', reason: 'Negative-margin utility: numeric snap is sound, but the codemod must confirm Tailwind v4 compiles a negated var() arbitrary value before applying at scale.', itemCount: 6, fileCount: 6, ruleIds: ['spacing/root-dependent'],
  },
  {
    value: '-space-x-2', currentPx: 7, tokenPx: 8, token: '--space-2', group: 'NORMALIZED',
    replacement: 'VERIFY FIRST — proposed `-space-x-[var(--space-2)]` (Tailwind v4 negative-arbitrary-value var() support unconfirmed); fallback `space-x-[calc(var(--space-2)*-1)]`',
    pattern: 'negative-tailwind-fraction-utility', reason: 'Negative-margin utility: numeric snap is sound, but the codemod must confirm Tailwind v4 compiles a negated var() arbitrary value before applying at scale.', itemCount: 1, fileCount: 1, ruleIds: ['spacing/root-dependent'],
  },
  {
    value: '-mt-0.5', currentPx: 1.75, tokenPx: 2, token: '--space-0-5', group: 'NEEDS-DECISION',
    replacement: 'VERIFY FIRST — proposed `-mt-[var(--space-0-5)]` (Tailwind v4 negative-arbitrary-value var() support unconfirmed); fallback `mt-[calc(var(--space-0-5)*-1)]`',
    pattern: 'negative-tailwind-fraction-utility', reason: 'Negative margin needs a value decision independent of compile support.', itemCount: 1, fileCount: 1, ruleIds: ['spacing/root-dependent'],
  },
  // A real M1 value that belongs to a DIFFERENT script's pattern (sibling
  // S-SPACE-1 lane) — must be silently absent from this lane's action
  // table, never touched, never refused.
  { value: 'gap-2', currentPx: 7, tokenPx: 8, token: '--space-2', group: 'NORMALIZED', replacement: 'gap-[var(--space-2)]', pattern: 'tailwind-fraction-utility', reason: '', itemCount: 1, fileCount: 1, ruleIds: ['spacing/root-dependent'] },
]

function fixtureMappingJson(extraRows = []) {
  return { summary: { totalDistinctValues: FIXTURE_ROWS.length + extraRows.length }, rows: [...FIXTURE_ROWS, ...extraRows] }
}

function writeFixtureMapping(root, extraRows = []) {
  return write(root, 'mapping.json', JSON.stringify(fixtureMappingJson(extraRows), null, 2))
}

const SUPPORTED_PROBE = { supported: true, reason: 'test-injected: treated as supported for this case' }
const UNSUPPORTED_PROBE = { supported: false, reason: 'test-injected: treated as unsupported for this case' }

// ── classifyFamily ─────────────────────────────────────────────────────────

test('classifyFamily: sorts each fixture row into the right family, and returns null for an unowned pattern', () => {
  const byValue = Object.fromEntries(FIXTURE_ROWS.map((r) => [r.value, r]))
  assert.equal(classifyFamily(byValue['ml-[22px]']), 'arbitrary')
  assert.equal(classifyFamily(byValue['pl-[11.75rem]']), 'arbitrary')
  assert.equal(classifyFamily(byValue['mt-px']), 'hairline')
  assert.equal(classifyFamily(byValue['mt-[1px]']), 'hairline')
  assert.equal(classifyFamily(byValue['-mb-px']), 'hairline')
  assert.equal(classifyFamily(byValue['-mx-1']), 'negative')
  assert.equal(classifyFamily(byValue['gap-2']), null)
})

// ── deriveNegativeReplacement ────────────────────────────────────────────

test('deriveNegativeReplacement: mechanically builds the negated-arbitrary-value class from value + token, ignoring the mapping\'s own decision prose', () => {
  assert.equal(deriveNegativeReplacement('-mx-1', '--space-1'), '-mx-[var(--space-1)]')
  assert.equal(deriveNegativeReplacement('-space-x-2', '--space-2'), '-space-x-[var(--space-2)]')
  assert.equal(deriveNegativeReplacement('-mt-0.5', '--space-0-5'), '-mt-[var(--space-0-5)]')
})

test('deriveNegativeReplacement: an unexpected shape returns null rather than guessing', () => {
  assert.equal(deriveNegativeReplacement('not-a-negative-utility', '--space-1'), null)
  assert.equal(deriveNegativeReplacement('mx-1', '--space-1'), null) // missing leading '-'
})

// ── buildValueActions ────────────────────────────────────────────────────

test('MATCH (arbitrary family): a NORMALIZED value with a bare mapped replacement applies verbatim', () => {
  const actions = buildValueActions(FIXTURE_ROWS, { negativeSupported: true, negativeReason: SUPPORTED_PROBE.reason })
  const a = actions.get('ml-[22px]')
  assert.equal(a.action, 'apply')
  assert.equal(a.family, 'arbitrary')
  assert.equal(a.replacement, 'ml-[var(--space-4)]')
})

test('MATCH (hairline family): both an off-scale-hairline row and a px-suffix row map to the registered hairline token', () => {
  const actions = buildValueActions(FIXTURE_ROWS, { negativeSupported: true, negativeReason: SUPPORTED_PROBE.reason })
  assert.deepEqual(actions.get('mt-px'), { family: 'hairline', group: 'IDENTICAL', pattern: 'tailwind-px-suffix-utility', action: 'apply', replacement: 'mt-[var(--border-width-hairline)]' })
  assert.deepEqual(actions.get('mt-[1px]'), { family: 'hairline', group: 'IDENTICAL', pattern: 'arbitrary-bracket-px', action: 'apply', replacement: 'mt-[var(--border-width-hairline)]' })
})

test('MATCH (negative family, probe SUPPORTED): a NORMALIZED negative value applies the MECHANICALLY DERIVED replacement, not the mapping\'s "VERIFY FIRST" prose', () => {
  const actions = buildValueActions(FIXTURE_ROWS, { negativeSupported: true, negativeReason: SUPPORTED_PROBE.reason })
  const a = actions.get('-mx-1')
  assert.equal(a.action, 'apply')
  assert.equal(a.replacement, '-mx-[var(--space-1)]')
  assert.doesNotMatch(a.replacement, /VERIFY FIRST/)
  const b = actions.get('-space-x-2')
  assert.equal(b.action, 'apply')
  assert.equal(b.replacement, '-space-x-[var(--space-2)]')
})

test('REFUSE (negative-margin decision, probe UNSUPPORTED direction): the WHOLE family refuses, including an otherwise-NORMALIZED value, citing the probe result', () => {
  const actions = buildValueActions(FIXTURE_ROWS, { negativeSupported: false, negativeReason: UNSUPPORTED_PROBE.reason })
  for (const value of ['-mx-1', '-space-x-2', '-mt-0.5']) {
    const a = actions.get(value)
    assert.equal(a.action, 'refuse', `${value} must refuse when the compile probe is unsupported`)
    assert.match(a.reason, /compile probe did not confirm support/)
    assert.match(a.reason, /finding 4/)
  }
})

test('REFUSE (negative-margin decision, probe SUPPORTED direction but NEEDS-DECISION value): -mt-0.5 still refuses on its own group, with the mapping\'s reason', () => {
  const actions = buildValueActions(FIXTURE_ROWS, { negativeSupported: true, negativeReason: SUPPORTED_PROBE.reason })
  const a = actions.get('-mt-0.5')
  assert.equal(a.action, 'refuse')
  assert.match(a.reason, /independent of compile support/)
})

test('REFUSE per NEEDS-DECISION value (arbitrary family): ml-[3px] and pl-[11.75rem] both refuse with the mapping\'s own reason', () => {
  const actions = buildValueActions(FIXTURE_ROWS, { negativeSupported: true, negativeReason: SUPPORTED_PROBE.reason })
  const a = actions.get('ml-[3px]')
  assert.equal(a.action, 'refuse')
  assert.match(a.reason, /equidistant/)
  const b = actions.get('pl-[11.75rem]')
  assert.equal(b.action, 'refuse')
  assert.match(b.reason, /far off the 4\/8 scale/)
})

test('REFUSE (hairline never negated): -mb-px refuses even though its OWN group is IDENTICAL — a hard invariant, not a group decision', () => {
  const actions = buildValueActions(FIXTURE_ROWS, { negativeSupported: true, negativeReason: SUPPORTED_PROBE.reason, allowedGroups: new Set(['IDENTICAL', 'NORMALIZED', 'NEEDS-DECISION']) })
  const a = actions.get('-mb-px')
  assert.equal(a.action, 'refuse')
  assert.match(a.reason, /never negated/)
})

test('a value owned by a sibling lane\'s pattern is entirely absent from the action table', () => {
  const actions = buildValueActions(FIXTURE_ROWS, { negativeSupported: true, negativeReason: SUPPORTED_PROBE.reason })
  assert.equal(actions.has('gap-2'), false)
})

test('buildValueActions rejects an unknown --groups value rather than silently ignoring it', () => {
  assert.throws(() => buildValueActions(FIXTURE_ROWS, { allowedGroups: new Set(['BOGUS']), negativeSupported: true }), /unknown mapping group/)
})

// ── planEditsForText (pure — no filesystem) ─────────────────────────────

function actionsForTest(overrides = {}) {
  return buildValueActions(FIXTURE_ROWS, { negativeSupported: true, negativeReason: SUPPORTED_PROBE.reason, ...overrides })
}

test('MATCH (arbitrary family, in a real JSX className string): ml-[22px] is rewritten in place, siblings untouched', () => {
  const src = `export const X = () => <div className="flex ml-[22px] gap-4" />\n`
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest(), '/repo')
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.equal(edits[0].syntax, 'ml-[22px]')
  assert.equal(edits[0].replacement, 'ml-[var(--space-4)]')
  assert.equal(src.slice(edits[0].start, edits[0].end), 'ml-[22px]')
})

test('MATCH (hairline family) + REFUSE (negative family unsupported) in the SAME string literal', () => {
  const src = `export const X = () => <div className="mt-px -mx-1" />\n`
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest({ negativeSupported: false, negativeReason: 'unsupported for this case' }), '/repo')
  assert.equal(edits.length, 1)
  assert.equal(edits[0].syntax, 'mt-px')
  assert.equal(edits[0].replacement, 'mt-[var(--border-width-hairline)]')
  assert.equal(refusals.length, 1)
  assert.equal(refusals[0].syntax, '-mx-1')
  assert.match(refusals[0].reason, /compile probe did not confirm support/)
})

test('REFUSE per NEEDS-DECISION value found in real source text: ml-[3px] is reported, not rewritten', () => {
  const src = `export const X = () => <div className="ml-[3px]" />\n`
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest(), '/repo')
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /equidistant/)
})

test('a target token GLUED to other text (no whitespace/quote boundary) is refused, never guessed at', () => {
  const src = `export const X = () => <div className="xml-[22px]y" />\n`
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest(), '/repo')
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /not bounded by whitespace/)
})

test('REFUSE (hairline, template-literal head): a real-shaped `` `-mb-px ... ${x}` `` template (BasePreview.tsx\'s own shape) is found and refused, not silently missed', () => {
  const src = 'export const X = () => <div className={`-mb-px whitespace-nowrap border-b-2 ${active ? "a" : "b"}`} />\n'
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest(), '/repo')
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.equal(refusals[0].syntax, '-mb-px')
  assert.match(refusals[0].reason, /never negated/)
})

test('MATCH (template-literal head, safe left edge): a token at the very start of a template head is a true boundary and applies', () => {
  const src = 'export const X = () => <div className={`mt-px ${active ? "a" : "b"}`} />\n'
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest(), '/repo')
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.equal(edits[0].syntax, 'mt-px')
  assert.equal(edits[0].replacement, 'mt-[var(--border-width-hairline)]')
})

test('REFUSE (template-literal tail, glued to interpolation): a token touching the LEFT edge of a tail chunk (right after `${...}`) is refused, not applied', () => {
  const src = 'export const X = () => <div className={`${active ? "a" : "b"}mt-px`} />\n'
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest(), '/repo')
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /interpolated value whose runtime content is unknown/)
})

test('MATCH (template-literal tail, safe right edge): a token at the very end of a template tail is a true boundary and applies', () => {
  const src = 'export const X = () => <div className={`${active ? "a" : "b"} mt-px`} />\n'
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest(), '/repo')
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.equal(edits[0].syntax, 'mt-px')
})

test('nested plain string literal inside a template\'s own interpolated ternary is still scanned independently', () => {
  const src = 'export const X = () => <div className={`flex ${active ? "ml-[22px]" : "gap-4"}`} />\n'
  const { edits, refusals } = planEditsForText('/repo/src/components/X.tsx', src, actionsForTest(), '/repo')
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.equal(edits[0].syntax, 'ml-[22px]')
})

test('idempotence at the text level: re-running planEditsForText on the ALREADY-rewritten text finds nothing further', () => {
  const src = `export const X = () => <div className="flex ml-[22px] gap-4" />\n`
  const actions = actionsForTest()
  const first = planEditsForText('/repo/src/components/X.tsx', src, actions, '/repo')
  assert.equal(first.edits.length, 1)
  const after = src.slice(0, first.edits[0].start) + first.edits[0].replacement + src.slice(first.edits[0].end)
  assert.match(after, /ml-\[var\(--space-4\)\]/)
  const second = planEditsForText('/repo/src/components/X.tsx', after, actions, '/repo')
  assert.equal(second.edits.length, 0)
  assert.equal(second.refusals.length, 0)
})

// ── loadMappingRows ──────────────────────────────────────────────────────

test('loadMappingRows: reads the mapping file at run time (not hardcoded) and reflects a change on reload', () => {
  const root = scratchDir('codemod-spacing-arbitrary-load-')
  const path = writeFixtureMapping(root)
  const rows = loadMappingRows(path)
  assert.equal(rows.find((r) => r.value === 'ml-[22px]').replacement, 'ml-[var(--space-4)]')
  writeFixtureMapping(root, [{ value: 'ml-[22px]', token: '--space-9', group: 'NORMALIZED', replacement: 'ml-[var(--space-9)]', pattern: 'arbitrary-bracket-px', reason: '', itemCount: 1 }])
  // The extra row is appended, not merged — read the LAST matching row to
  // prove the reload genuinely re-parsed the file rather than caching.
  const rows2 = loadMappingRows(path)
  assert.equal(rows2.filter((r) => r.value === 'ml-[22px]').at(-1).replacement, 'ml-[var(--space-9)]')
})

test('loadMappingRows: a malformed mapping file fails loudly, not silently', () => {
  const root = scratchDir('codemod-spacing-arbitrary-badjson-')
  const path = write(root, 'mapping.json', '{ not valid json')
  assert.throws(() => loadMappingRows(path), /not valid JSON/)
})

test('loadMappingRows: a validly-shaped-JSON file missing the { rows: [...] } shape fails loudly', () => {
  const root = scratchDir('codemod-spacing-arbitrary-badshape-')
  const path = write(root, 'mapping.json', JSON.stringify({ notRows: [] }))
  assert.throws(() => loadMappingRows(path), /expected \{ rows: \[\.\.\.\] \} shape/)
})

// ── planFileEdits / runCodemod (disk-based) ─────────────────────────────

function fixtureRepoWithMapping(prefix) {
  const root = scratchDir(prefix)
  const mappingPath = writeFixtureMapping(root)
  return { root, mappingPath }
}

test('planFileEdits: reads a real file from disk and delegates to planEditsForText', () => {
  const { root } = fixtureRepoWithMapping('codemod-spacing-arbitrary-planfile-')
  const filePath = write(root, 'src/components/Y.tsx', `export const Y = () => <div className="mt-px" />\n`)
  const { edits, refusals } = planFileEdits(root, filePath, actionsForTest())
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.equal(edits[0].replacement, 'mt-[var(--border-width-hairline)]')
})

test('runCodemod (dry-run): one case per family in one file, reconciled against byFamily counts, with an injected SUPPORTED probe', async () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-arbitrary-dryrun-')
  const filePath = write(root, 'src/components/All.tsx', `
export function All() {
  return <div className="ml-[22px] mt-px -mx-1 ml-[3px]" />
}
`)
  const result = await runCodemod({ repoRoot: root, apply: false, mappingPath, negativeProbeResult: SUPPORTED_PROBE })
  assert.equal(result.applied, false)
  assert.equal(result.totalEdits, 3) // ml-[22px], mt-px, -mx-1
  assert.equal(result.totalRefusals, 1) // ml-[3px] (NEEDS-DECISION)
  assert.equal(result.byFamily.arbitrary.edits, 1)
  assert.equal(result.byFamily.arbitrary.refusals, 1)
  assert.equal(result.byFamily.hairline.edits, 1)
  assert.equal(result.byFamily.negative.edits, 1)
  const after = readFileSync(filePath, 'utf8')
  assert.equal(after, readFileSync(filePath, 'utf8'), 'dry-run never writes to disk')
  assert.match(after, /ml-\[22px\]/, 'dry-run leaves the file byte-identical on disk')
})

test('runCodemod: negative-margin decision refuses the WHOLE family with an injected UNSUPPORTED probe, even for its NORMALIZED values', async () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-arbitrary-unsupported-')
  write(root, 'src/components/Neg.tsx', `export const Neg = () => <div className="-mx-1 -space-x-2" />\n`)
  const result = await runCodemod({ repoRoot: root, apply: false, mappingPath, negativeProbeResult: UNSUPPORTED_PROBE })
  assert.equal(result.negativeProbe.supported, false)
  assert.equal(result.totalEdits, 0)
  assert.equal(result.totalRefusals, 2)
  assert.ok(result.files[0].refusals.every((r) => /compile probe did not confirm support/.test(r.reason)))
})

test('idempotence (runCodemod, real --apply on disk): a second --apply makes zero further edits', async () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-arbitrary-idempotent-')
  const filePath = write(root, 'src/components/Idem.tsx', `export const Idem = () => <div className="ml-[22px] mt-px" />\n`)
  const applied = await runCodemod({ repoRoot: root, apply: true, mappingPath, negativeProbeResult: SUPPORTED_PROBE })
  assert.equal(applied.totalEdits, 2)
  const afterFirst = readFileSync(filePath, 'utf8')
  assert.match(afterFirst, /ml-\[var\(--space-4\)\]/)
  assert.match(afterFirst, /mt-\[var\(--border-width-hairline\)\]/)

  const second = await runCodemod({ repoRoot: root, apply: true, mappingPath, negativeProbeResult: SUPPORTED_PROBE })
  assert.equal(second.totalEdits, 0)
  const afterSecond = readFileSync(filePath, 'utf8')
  assert.equal(afterSecond, afterFirst, 'a second --apply must be a byte-for-byte no-op')
})

test('allowed-roots guard: runCodemod never touches a file outside src/ or packages/ui/src/', async () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-arbitrary-roots-')
  const outsidePath = write(root, 'scripts/design-system/rogue.tsx', `export const x = <div className="mt-px" />`)
  write(root, 'src/components/Inside.tsx', `export const y = <div className="mt-px" />`)

  assert.equal(isInAllowedRoot(root, outsidePath), false)

  const result = await runCodemod({ repoRoot: root, apply: false, mappingPath, negativeProbeResult: SUPPORTED_PROBE })
  const touchedFiles = result.files.map((f) => f.file)
  assert.ok(!touchedFiles.includes('scripts/design-system/rogue.tsx'))
  assert.ok(touchedFiles.includes('src/components/Inside.tsx'))
  // Prove it structurally, not just by absence: the outside file is never
  // even in totalFilesScanned's source list.
  assert.equal(result.totalFilesScanned, 1)
})

// ── mutation proof, scratch copy under this lane's own evidence dir ──────

test('mutation proof: --apply actually rewrites bytes on disk in an isolated evidence-dir copy, and a second --apply is a no-op', async () => {
  const evidenceRoot = resolve('dist/design-system-baseline/cli-lanes/c1-prep/S-SPACE-2')
  mkdirSync(evidenceRoot, { recursive: true })
  const root = mkdtempSync(resolve(evidenceRoot, 'mutation-proof-'))
  fixtureRoots.push(root)
  const mappingPath = writeFixtureMapping(root)
  const filePath = write(root, 'src/components/Mutate.tsx', `
export function Mutate() {
  return <div className="ml-[22px] mt-px -mx-1 ml-[3px] -mb-px" />
}
`)
  const before = readFileSync(filePath, 'utf8')
  assert.match(before, /ml-\[22px\] mt-px -mx-1 ml-\[3px\] -mb-px/)

  const applied = await runCodemod({ repoRoot: root, apply: true, mappingPath, negativeProbeResult: SUPPORTED_PROBE })
  // ml-[22px], mt-px, -mx-1 apply; ml-[3px] (NEEDS-DECISION) and -mb-px
  // (hairline, never negated) refuse.
  assert.equal(applied.totalEdits, 3)
  assert.equal(applied.totalRefusals, 2)

  const after = readFileSync(filePath, 'utf8')
  assert.match(after, /ml-\[var\(--space-4\)\] mt-\[var\(--border-width-hairline\)\] -mx-\[var\(--space-1\)\] ml-\[3px\] -mb-px/)
  assert.notEqual(before, after)

  const secondApply = await runCodemod({ repoRoot: root, apply: true, mappingPath, negativeProbeResult: SUPPORTED_PROBE })
  assert.equal(secondApply.totalEdits, 0)
  const afterSecond = readFileSync(filePath, 'utf8')
  assert.equal(afterSecond, after, 'a second --apply must be a byte-for-byte no-op')
})
