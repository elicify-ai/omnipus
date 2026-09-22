import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, existsSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import {
  buildTables, extractReplacementValue, loadMapping, normalizeDeclValue,
  parseDeclarationText, planCssFile, planTsFile, runCodemod,
} from './codemod-spacing-css.mjs'

// ── fixture plumbing (mirrors the sibling codemod-*.test.mjs convention:
// a fresh mkdtemp fixture repo per test, under dist/design-system-baseline/
// so CI's fresh-checkout `node --test` run has somewhere to create it) ────

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-spacing-css-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

function read(root, relPath) {
  return readFileSync(resolve(root, relPath), 'utf8')
}

// A synthetic mapping.json — deliberately NOT the real dist/ mapping (no
// committed test may read anything under dist/, and the real mapping also
// changes shape as Lane M1 regenerates it). Covers one IDENTICAL and one
// NORMALIZED raw-css-declaration (proving both applyable groups work, not
// just IDENTICAL), one NEEDS-DECISION raw-css-declaration, one
// unregistered-custom-property-calc, and all three safe-area-env-fallback
// surface shapes (plain declaration, max()-floor declaration, Tailwind
// bracket utility).
const SYNTHETIC_MAPPING_ROWS = [
  { value: 'padding: 4px 2px', group: 'IDENTICAL', pattern: 'raw-css-declaration', replacement: 'padding: var(--space-1) var(--space-0-5)', reason: '', itemCount: 1, fileCount: 1 },
  { value: 'gap: 6px', group: 'NORMALIZED', pattern: 'raw-css-declaration', replacement: 'gap: var(--space-2)', reason: '', itemCount: 1, fileCount: 1 },
  { value: 'padding: 10px 10px', group: 'NEEDS-DECISION', pattern: 'raw-css-declaration', replacement: 'padding: var(--space-2) var(--space-2)', reason: 'Per-axis: 10px is ambiguous between --space-2(8px) and a wider step — needs central review.', itemCount: 1, fileCount: 1 },
  { value: 'padding-left: calc(var(--widget-indent-depth-px) * 1px)', group: 'NEEDS-DECISION', pattern: 'unregistered-custom-property-calc', replacement: 'NO SOUND TOKEN MAPPING — computed per-instance multiplier.', reason: 'This is a tree-depth indent multiplier, not a constant — no token can replace it.', itemCount: 1, fileCount: 1 },
  { value: 'padding-bottom: env(safe-area-inset-bottom)', group: 'NEEDS-DECISION', pattern: 'safe-area-env-fallback', replacement: 'env(safe-area-inset-bottom, var(--space-N)) — pick the closed-scale fallback token per surface (central review).', reason: 'No fallback at all today; needs a chosen closed-scale fallback value (design decision, not purely mechanical).', itemCount: 1, fileCount: 1 },
  { value: 'padding-top: max(0.5rem, env(safe-area-inset-top))', group: 'NEEDS-DECISION', pattern: 'safe-area-env-fallback', replacement: 'VERIFY BEHAVIOR CHANGE — ledger suggests env(safe-area-inset-*, <token>), but this already uses max(<fixed>, env(...)) which enforces a floor even when the inset is 0; switching would drop that floor.', reason: 'Ledger replacement text is not behavior-preserving here: max() guarantees a minimum on devices with zero inset.', itemCount: 1, fileCount: 1 },
  { value: 'pb-[env(safe-area-inset-bottom)]', group: 'NEEDS-DECISION', pattern: 'safe-area-env-fallback', replacement: 'env(safe-area-inset-*, var(--space-N)) — pick the closed-scale fallback token per surface (central review).', reason: 'No fallback at all today; needs a chosen closed-scale fallback value (design decision, not purely mechanical).', itemCount: 1, fileCount: 1 },
  { value: 'pb-[max(0.5rem,env(safe-area-inset-bottom))]', group: 'NEEDS-DECISION', pattern: 'safe-area-env-fallback', replacement: 'VERIFY BEHAVIOR CHANGE — ledger suggests env(safe-area-inset-*, <token>), but this already uses max(<fixed>, env(...)) which enforces a floor even when the inset is 0; switching would drop that floor.', reason: 'Ledger replacement text is not behavior-preserving here: max() guarantees a minimum on devices with zero inset.', itemCount: 1, fileCount: 1 },
]

function writeMapping(root, rows = SYNTHETIC_MAPPING_ROWS) {
  return write(root, 'mapping.json', JSON.stringify({
    summary: { totalDistinctValues: rows.length, totalItems: rows.length },
    rows,
  }, null, 2))
}

const CSS_FIXTURE = `/* header comment on the stylesheet */
.a {
  color: red; /* unrelated declaration — must stay untouched byte-for-byte */
  padding: 4px 2px; /* IDENTICAL */
}

@media (max-width: 600px) {
  .b {
    gap: 6px; /* NORMALIZED, nested inside an at-rule */
  }
}

.c {
  padding: 10px 10px !important;
}

[data-shell] {
  padding-bottom: env(safe-area-inset-bottom);
}

[data-shell] > header {
  padding-top: max(0.5rem, env(safe-area-inset-top));
}

.vendor {
  -webkit-margin-start: 4px;
}
`

const TSX_FIXTURE = `import React from 'react'

export function Chip() {
  return (
    <div
      style={{
        padding: '4px 2px',
        gap: '6px',
        paddingLeft: 'calc(var(--widget-indent-depth-px) * 1px)',
      }}
      className="flex pb-[env(safe-area-inset-bottom)] pb-[max(0.5rem,env(safe-area-inset-bottom))]"
    >
      hi
    </div>
  )
}
`

// ── unit-level: parsing/normalizing helpers ─────────────────────────────

test('parseDeclarationText extracts prop/value from a "prop: value" string via postcss', () => {
  assert.deepEqual(parseDeclarationText('padding: 6px 4px'), { prop: 'padding', value: '6px 4px' })
  assert.deepEqual(parseDeclarationText('gap: var(--space-1)'), { prop: 'gap', value: 'var(--space-1)' })
})

test('parseDeclarationText returns null for prose / non-declaration text (Tailwind bracket utility, NEEDS-DECISION warning text)', () => {
  assert.equal(parseDeclarationText('pb-[env(safe-area-inset-bottom)]'), null)
  assert.equal(parseDeclarationText('NO SOUND TOKEN MAPPING — needs a design decision'), null)
  assert.equal(parseDeclarationText(''), null)
})

test('normalizeDeclValue folds whitespace and case for comparison only', () => {
  assert.equal(normalizeDeclValue('  6px   4px '), '6px 4px')
  assert.equal(normalizeDeclValue('VAR(--Space-1)'), 'var(--space-1)')
})

test('buildTables indexes both raw-css-declaration and unregistered-custom-property-calc under one key space, and separately collects safe-area entries with floor detection', () => {
  const { rawCssIndex, safeAreaEntries } = buildTables(SYNTHETIC_MAPPING_ROWS)
  assert.equal(rawCssIndex.size, 4)
  assert.equal(safeAreaEntries.length, 4)
  const floorEntries = safeAreaEntries.filter((e) => e.hasFloor)
  assert.equal(floorEntries.length, 2, 'exactly the two max()-wrapped rows are floor entries')
  const tailwindEntries = safeAreaEntries.filter((e) => e.isTailwindUtility)
  assert.equal(tailwindEntries.length, 2, 'exactly the two bracket-utility rows are Tailwind-shaped, not CSS-declaration-shaped')
})

test('extractReplacementValue parses only the value half of row.replacement', () => {
  const row = { replacement: 'padding: var(--space-1) var(--space-0-5)' }
  assert.equal(extractReplacementValue(row), 'var(--space-1) var(--space-0-5)')
})

// ── CSS declaration case (apply) ─────────────────────────────────────────

test('CSS FILE: IDENTICAL and NORMALIZED raw-css-declaration values are rewritten in place; everything else in the stylesheet is preserved exactly', () => {
  const root = fixtureRepo()
  writeMapping(root)
  const filePath = write(root, 'src/styles/fixture.css', CSS_FIXTURE)
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planCssFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))

  assert.equal(result.edits.length, 2, JSON.stringify(result.edits))
  assert.match(result.after, /padding: var\(--space-1\) var\(--space-0-5\);/)
  assert.match(result.after, /gap: var\(--space-2\);/)
  // Untouched surroundings, verbatim: header comment, unrelated decl, the
  // at-rule wrapper itself, !important, vendor prefix, comment placement.
  assert.match(result.after, /\/\* header comment on the stylesheet \*\//)
  assert.match(result.after, /color: red; \/\* unrelated declaration — must stay untouched byte-for-byte \*\//)
  assert.match(result.after, /@media \(max-width: 600px\) \{/)
  assert.match(result.after, /padding: 10px 10px !important;/, 'NEEDS-DECISION declaration is untouched, including !important')
  assert.match(result.after, /-webkit-margin-start: 4px;/, 'vendor-prefixed declaration outside scope is untouched')
})

// ── refusal per NEEDS-DECISION value ─────────────────────────────────────

test('CSS FILE: a NEEDS-DECISION raw-css-declaration is refused with the mapping row\'s own reason, file left byte-identical', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/fixture.css', CSS_FIXTURE)
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planCssFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  const refusal = result.refusals.find((r) => r.value === '10px 10px')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.equal(refusal.family, 'raw-css-declaration')
  assert.match(refusal.reason, /ambiguous between --space-2/)
  assert.equal(result.after.includes('padding: 10px 10px !important;'), true)
})

test('TS FILE: unregistered-custom-property-calc (tree-depth indent multiplier) is always refused, never guessed at', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Chip.tsx', TSX_FIXTURE)
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planTsFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  const refusal = result.refusals.find((r) => r.family === 'unregistered-custom-property-calc')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.match(refusal.reason, /tree-depth indent multiplier/)
  assert.equal(result.after.includes("calc(var(--widget-indent-depth-px) * 1px)"), true, 'the calc() site itself is never rewritten')
})

test('TS FILE: a structurally-identical calc(var(--…) * 1px) NOT literally enumerated in mapping.json is still refused (fail-closed shape match, never silently skipped)', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Other.tsx', `
    export function Row({ depth }) {
      return <div style={{ paddingLeft: 'calc(var(--other-indent-depth-px) * 1px)' }} />
    }
  `)
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planTsFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  assert.equal(result.edits.length, 0)
  assert.equal(result.refusals.length, 1)
  assert.match(result.refusals[0].reason, /computed per-instance multiplier/)
})

// ── safe-area: keeps its floor ────────────────────────────────────────────

test('SAFE-AREA (CSS): a max(<fixed>, env(...)) floor site is refused with a floor-preserving explanation, and that exact declaration is left byte-identical (the floor keeps working because nothing touched it)', () => {
  const root = fixtureRepo()
  // Isolate this assertion to ONLY the safe-area declaration: a fixture
  // with zero applyable (IDENTICAL/NORMALIZED) declarations elsewhere in
  // it, so "the file is untouched" is a meaningful, whole-file claim here
  // rather than being true only by accident of what else happens to be on
  // the page (the shared CSS_FIXTURE also carries two real edits).
  const onlyFloorFixture = `[data-shell] > header {\n  padding-top: max(0.5rem, env(safe-area-inset-top));\n}\n`
  const filePath = write(root, 'src/styles/floor-only.css', onlyFloorFixture)
  const before = readFileSync(filePath, 'utf8')
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planCssFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  const refusal = result.refusals.find((r) => r.value === 'max(0.5rem, env(safe-area-inset-top))')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.match(refusal.reason, /cannot preserve behaviour exactly/i)
  assert.match(refusal.reason, /floor/i)
  assert.match(refusal.reason, /max\(<token>, env\(\.\.\.\)\)/, 'the refusal states the CORRECT eventual form, not the ledger\'s behaviour-changing env(prop, fallback) suggestion')
  assert.equal(result.edits.length, 0)
  assert.equal(result.after, before, 'the file is completely untouched, so the live floor keeps applying exactly as it does today')

  // Same declaration, still present verbatim, even in the fixture that ALSO
  // carries real edits elsewhere on the page.
  const mixedPath = write(root, 'src/styles/fixture.css', CSS_FIXTURE)
  const mixedResult = planCssFile(root, mixedPath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  assert.match(mixedResult.after, /padding-top: max\(0\.5rem, env\(safe-area-inset-top\)\);/, 'the floor declaration itself is untouched even when sibling declarations in the same file are rewritten')
})

test('SAFE-AREA (Tailwind className bracket): a pb-[max(...)] floor utility is refused with the same floor-preserving reasoning as the CSS-declaration shape', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Chip.tsx', TSX_FIXTURE)
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planTsFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  const refusal = result.refusals.find((r) => r.value === 'pb-[max(0.5rem,env(safe-area-inset-bottom))]')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.match(refusal.reason, /floor/i)
  assert.equal(result.edits.length, 2, 'only the two applyable style-object declarations are edits; className is never rewritten')
})

// ── safe-area: a site you cannot preserve (no floor to protect, but still no chosen fallback) ──

test('SAFE-AREA (CSS): a bare env() declaration with no floor is refused for a DIFFERENT reason (missing fallback choice, not a floor risk)', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/fixture.css', CSS_FIXTURE)
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planCssFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  const refusal = result.refusals.find((r) => r.value === 'env(safe-area-inset-bottom)')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.doesNotMatch(refusal.reason, /floor/i, 'a non-floor site must not be told it has a floor to protect')
  assert.match(refusal.reason, /no fallback token has been chosen/i)
})

test('SAFE-AREA (Tailwind className bracket): a bare pb-[env(...)] utility (no floor) is refused with the missing-fallback reason', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Chip.tsx', TSX_FIXTURE)
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planTsFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  const refusal = result.refusals.find((r) => r.value === 'pb-[env(safe-area-inset-bottom)]')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.doesNotMatch(refusal.reason, /floor/i)
})

// ── TS FILE apply path ───────────────────────────────────────────────────

test('TS FILE: IDENTICAL/NORMALIZED style-object string values are rewritten, preserving quote style and every untouched property', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Chip.tsx', TSX_FIXTURE)
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planTsFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  assert.equal(result.edits.length, 2, JSON.stringify(result.edits))
  assert.match(result.after, /padding: 'var\(--space-1\) var\(--space-0-5\)',/)
  assert.match(result.after, /gap: 'var\(--space-2\)',/)
  assert.match(result.after, /import React from 'react'/, 'unrelated code untouched')
})

// ── idempotence ───────────────────────────────────────────────────────────

test('IDEMPOTENT: a second --apply over an already-rewritten tree makes zero further edits', () => {
  const root = fixtureRepo()
  writeMapping(root)
  write(root, 'src/styles/fixture.css', CSS_FIXTURE)
  write(root, 'src/components/Chip.tsx', TSX_FIXTURE)

  const first = runCodemod({ repoRoot: root, apply: true, mappingPath: resolve(root, 'mapping.json') })
  assert.equal(first.totalEdits, 4, JSON.stringify(first.files))

  const second = runCodemod({ repoRoot: root, apply: true, mappingPath: resolve(root, 'mapping.json') })
  assert.equal(second.totalEdits, 0, 'the OLD raw values no longer exist anywhere, so the lookup key can never match again')
  // Refusals persist across runs — they are not a one-time notice, they are
  // a standing "still needs a human" fact until mapping.json itself changes.
  assert.ok(second.totalRefusals > 0)
  assert.equal(second.totalRefusals, first.totalRefusals)
})

// ── allowed-roots guard ───────────────────────────────────────────────────

test('ALLOWED-ROOTS GUARD: a matching violation outside src/ and packages/ui/src/ is never scanned, edited, or reported', () => {
  const root = fixtureRepo()
  writeMapping(root)
  write(root, 'other/outside.css', '.z { padding: 4px 2px; }\n')
  write(root, 'src/styles/inside.css', '.z { padding: 4px 2px; }\n')

  const result = runCodemod({ repoRoot: root, apply: false, mappingPath: resolve(root, 'mapping.json') })
  assert.ok(!result.files.some((f) => f.file.includes('outside.css')), JSON.stringify(result.files.map((f) => f.file)))
  assert.ok(result.files.some((f) => f.file.includes('inside.css')))

  const applied = runCodemod({ repoRoot: root, apply: true, mappingPath: resolve(root, 'mapping.json') })
  assert.equal(applied.totalEdits, 1, 'only the in-root file is ever edited')
  assert.equal(read(root, 'other/outside.css'), '.z { padding: 4px 2px; }\n', 'the out-of-root file is untouched even under --apply')
})

// ── --groups flag ─────────────────────────────────────────────────────────

test('--groups narrows which mechanical group is applied; NEEDS-DECISION can never be promoted into it', () => {
  const root = fixtureRepo()
  writeMapping(root)
  write(root, 'src/styles/fixture.css', CSS_FIXTURE)

  const identicalOnly = runCodemod({ repoRoot: root, apply: false, groups: ['IDENTICAL'], mappingPath: resolve(root, 'mapping.json') })
  assert.equal(identicalOnly.totalEdits, 1, 'only the IDENTICAL padding declaration applies')
  const file = identicalOnly.files.find((f) => f.file.includes('fixture.css'))
  const gapRefusal = file.refusals.find((r) => r.value === '6px' && r.prop === 'gap')
  assert.ok(gapRefusal, 'the NORMALIZED gap declaration is refused when NORMALIZED is excluded from --groups')

  const attemptPromoteNeedsDecision = runCodemod({ repoRoot: root, apply: false, groups: ['IDENTICAL', 'NORMALIZED', 'NEEDS-DECISION'], mappingPath: resolve(root, 'mapping.json') })
  const stillRefused = attemptPromoteNeedsDecision.files.find((f) => f.file.includes('fixture.css')).refusals.find((r) => r.value === '10px 10px')
  assert.ok(stillRefused, 'NEEDS-DECISION stays refused even when named in --groups — the universe of applyable groups excludes it structurally')
})

// ── mutation proof, isolated copy under this lane's evidence dir ─────────
// (dist/design-system-baseline/cli-lanes/c1-prep/S-SPACE-3/ — per
// COMMON-RULES.md, red-state/mutation proofs run on isolated copies in the
// lane's own evidence dir, never against the live repo tree.)

test('MUTATION PROOF: --apply against an isolated evidence-dir copy actually rewrites bytes on disk, and a corrupted variant of the fixture is correctly refused instead of mis-transformed', () => {
  const evidenceRoot = resolve('dist/design-system-baseline/cli-lanes/c1-prep/S-SPACE-3/mutation-proof-fixture')
  rmSync(evidenceRoot, { recursive: true, force: true })
  mkdirSync(evidenceRoot, { recursive: true })
  writeMapping(evidenceRoot)
  const cssPath = write(evidenceRoot, 'src/styles/fixture.css', CSS_FIXTURE)

  const before = readFileSync(cssPath, 'utf8')
  assert.match(before, /padding: 4px 2px;/)

  const result = runCodemod({ repoRoot: evidenceRoot, apply: true, mappingPath: resolve(evidenceRoot, 'mapping.json') })
  assert.ok(result.totalEdits > 0)

  const afterDisk = readFileSync(cssPath, 'utf8')
  assert.notEqual(afterDisk, before, 'the file on disk actually changed — this is a real write, not just a returned diff')
  assert.match(afterDisk, /padding: var\(--space-1\) var\(--space-0-5\);/)
  assert.doesNotMatch(afterDisk, /padding: 4px 2px;/)

  // Mutation: shift the IDENTICAL value by 1px (4px 3px instead of 4px 2px).
  // This must no longer match the mapping's exact lookup key, so the
  // codemod must leave it alone entirely — not "close enough, apply anyway".
  // This proves the match is an exact declaration-value lookup, not a loose
  // shape/regex match that could mis-transform a merely similar value.
  const mutatedPath = write(evidenceRoot, 'src/styles/mutated.css', '.m { padding: 4px 3px; }\n')
  const mutatedResult = runCodemod({ repoRoot: evidenceRoot, apply: true, mappingPath: resolve(evidenceRoot, 'mapping.json') })
  const mutatedFile = mutatedResult.files.find((f) => f.file.includes('mutated.css'))
  assert.ok(!mutatedFile, 'a value one pixel off the exact mapped value is not a known row at all — silently out of scope, not mis-transformed')
  assert.equal(readFileSync(mutatedPath, 'utf8'), '.m { padding: 4px 3px; }\n')

  // Idempotence, proved again on this same disk copy (not just the temp
  // fixture above) — the mutation-proof copy is the one artifact this test
  // leaves behind under the evidence dir for the final report.
  const second = runCodemod({ repoRoot: evidenceRoot, apply: true, mappingPath: resolve(evidenceRoot, 'mapping.json') })
  assert.equal(second.totalEdits, 0)
  assert.ok(existsSync(cssPath))
})

// ── malformed input fails closed, never silently empty ───────────────────

test('an unparseable CSS file produces a parse-error refusal instead of silently reporting zero findings', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/broken.css', '.a { padding: 4px 2px \n') // unterminated rule
  const tables = buildTables(SYNTHETIC_MAPPING_ROWS)
  const result = planCssFile(root, filePath, tables, new Set(['IDENTICAL', 'NORMALIZED']))
  assert.equal(result.edits.length, 0)
  assert.equal(result.refusals.length, 1)
  assert.equal(result.refusals[0].family, 'parse-error')
})

test('loadMapping surfaces a clear error for a mapping file with no rows array (fail loud, not silent-empty)', () => {
  const root = fixtureRepo()
  const mappingPath = write(root, 'bad-mapping.json', JSON.stringify({ summary: {} }))
  assert.throws(() => loadMapping(mappingPath), /no "rows" array/)
})
