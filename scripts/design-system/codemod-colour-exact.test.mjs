import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, existsSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import {
  loadSafeSplit, buildTables, planCssFile, planTsFile, runCodemod,
} from './codemod-colour-exact.mjs'

// ── fixture plumbing (mirrors the sibling codemod-*.test.mjs convention: a
// fresh mkdtemp fixture repo per test, under dist/design-system-baseline so
// CI's fresh-checkout `node --test` run has somewhere to create it) ────────
// No committed test may read anything under dist/ — this file never reads
// the real dist/design-system-baseline/cli-lanes/c1-prep/COLOUR-SPLIT/
// safe-split.json; every test below writes its OWN synthetic safe-split.json
// (deliberately using fake token names distinct from real brand colours, the
// same convention codemod-spacing-css.test.mjs uses for spacing values).

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-colour-exact-'))
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

// A synthetic safe-split.json — fake token names/hexes so nobody mistakes
// this for real design-system data. Exercises all four SAFE rule families
// plus one deliberate refusal per family (a mismatched theme-block hex, a
// mismatched var() fallback).
const SYNTHETIC_SAFE_SPLIT = {
  generatedBy: 'synthetic fixture for codemod-colour-exact.test.mjs',
  sourceMapping: 'n/a (test fixture)',
  verifiedAgainst: 'n/a (test fixture)',
  safe: {
    tsVarSubstrings: [
      { id: 'fixture-undefined-gold', find: 'var(--fixture-gold)', replace: 'var(--fixture-accent)', reason: 'fixture: undefined token renamed to its registered equivalent' },
      { id: 'fixture-primary-fg-comma-nospace', find: 'var(--fixture-primary-fg,var(--fixture-secondary))', replace: 'var(--fixture-secondary)', reason: 'fixture: dead fallback collapse' },
      { id: 'fixture-primary-fg-bare', find: 'var(--fixture-primary-fg)', replace: 'var(--fixture-secondary)', reason: 'fixture: dead fallback collapse (bare)' },
    ],
    cssThemeBlockPrimitiveSwap: {
      file: 'src/styles/fixture-theme.css',
      properties: [
        { prop: '--fixture-primary', expectedHex: '#0a0a0b', replacement: 'var(--fixture-primitive-surface-0)', reason: 'fixture: byte-identical primitive swap' },
        { prop: '--fixture-broken', expectedHex: '#f97316', refuse: true, reason: 'fixture: known status-mismatch bug, never applied' },
      ],
    },
    cssVarIndirectionUnwrap: {
      files: ['src/styles/fixture-fc.css'],
    },
    cssVarFallbackDropTokenValues: {
      '--fixture-primary': '#0a0a0b',
      '--fixture-secondary': '#e2e8f0',
      '--fixture-accent': '#d4af37',
      '--fixture-accent-hover': '#c49e2f',
      '--fixture-success': '#10b981',
      '--fixture-primitive-surface-0': '#0a0a0b',
    },
  },
  alwaysRefuse: {
    governedDataFiles: ['src/lib/fixtureRegistry.ts'],
    alphaConcatenationKnownSites: ['src/components/fixture/FixtureBadge.tsx'],
    tailwindPaletteHues: ['red', 'amber', 'blue', 'white', 'black'],
  },
}

function writeSafeSplit(root, data = SYNTHETIC_SAFE_SPLIT) {
  return write(root, 'safe-split.json', JSON.stringify(data, null, 2))
}

// ── unit-level: loading & table-building ────────────────────────────────

test('loadSafeSplit parses a safe-split.json and validates required top-level sections', () => {
  const root = fixtureRepo()
  const path = writeSafeSplit(root)
  const data = loadSafeSplit(path)
  assert.equal(data.safe.tsVarSubstrings.length, 3)
  assert.equal(data.alwaysRefuse.governedDataFiles.length, 1)
})

test('loadSafeSplit fails loud on a file missing "safe"/"alwaysRefuse" (fail closed, not silent-empty)', () => {
  const root = fixtureRepo()
  const path = write(root, 'bad.json', JSON.stringify({ foo: 1 }))
  assert.throws(() => loadSafeSplit(path), /missing "safe"\/"alwaysRefuse"/)
})

test('buildTables inverts the token table into hex -> candidate-names, sorted, for ambiguous-name refusals', () => {
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  assert.deepEqual(tables.hexToTokenNames.get('#0a0a0b'), ['--fixture-primary', '--fixture-primitive-surface-0'])
})

// ── Rule 1 (apply): TS var() substring swap — style prop AND Tailwind arbitrary value ──

test('TS FILE: an exact swap in a JSX style-prop string literal (var(--fixture-gold) -> var(--fixture-accent))', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Fixture.tsx', `
    export function Fixture() {
      return <span style={{ color: 'var(--fixture-gold)' }}>hi</span>
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planTsFile(root, filePath, tables)
  assert.equal(result.edits.length, 1, JSON.stringify(result.edits))
  assert.match(result.after, /color: 'var\(--fixture-accent\)'/)
  assert.doesNotMatch(result.after, /fixture-gold/)
})

test('TS FILE: an exact swap inside a Tailwind arbitrary-value className token (compound fallback collapse)', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Fixture.tsx', `
    export function Fixture() {
      return <span className="text-[var(--fixture-primary-fg,var(--fixture-secondary))] border-[var(--fixture-accent)]">hi</span>
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planTsFile(root, filePath, tables)
  assert.equal(result.edits.length, 1, JSON.stringify(result.edits))
  assert.match(result.after, /className="text-\[var\(--fixture-secondary\)\] border-\[var\(--fixture-accent\)\]"/)
})

// ── Rule 2 (apply + refuse): CSS theme-block primitive swap ─────────────

test('CSS FILE: an exact swap of a globals.css-shaped @theme declaration to its differently-named primitive alias', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/fixture-theme.css', `
    @theme {
      --fixture-primary: #0a0a0b;
      --fixture-broken: #f97316;
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planCssFile(root, filePath, tables)
  assert.equal(result.edits.length, 1, JSON.stringify(result.edits))
  assert.match(result.after, /--fixture-primary: var\(--fixture-primitive-surface-0\);/)
  // The known-bug property is refused, not swapped to its OWN name (which
  // would be circular) or silently left as an unexplained no-op.
  const refusal = result.refusals.find((r) => r.prop === '--fixture-broken')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.match(refusal.reason, /known status-mismatch bug/)
  assert.match(result.after, /--fixture-broken: #f97316;/, 'the refused declaration is left byte-identical')
})

test('CSS FILE: a theme-block property whose live value has drifted from the verified snapshot is refused, never guessed at', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/fixture-theme.css', `
    @theme {
      --fixture-primary: #123456;
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planCssFile(root, filePath, tables)
  assert.equal(result.edits.length, 0)
  assert.equal(result.refusals.length, 1)
  assert.match(result.refusals[0].reason, /has drifted/)
  assert.equal(result.after, result.before)
})

// ── Rule 3 (apply): CSS var() indirection unwrap ─────────────────────────

test('CSS FILE: a --fc-*-shaped indirection whose OWN declaration already reads a registered var() is unwrapped at consumption sites, never at the declaration line', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/fixture-fc.css', `
    :root {
      --fc-fixture-color: var(--fixture-accent);
    }
    .a { color: var(--fc-fixture-color); }
    .b { background: var(--fc-fixture-color) !important; }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planCssFile(root, filePath, tables)
  assert.equal(result.edits.length, 2, JSON.stringify(result.edits))
  assert.match(result.after, /--fc-fixture-color: var\(--fixture-accent\);/, 'declaration line untouched')
  assert.match(result.after, /\.a \{ color: var\(--fixture-accent\); \}/)
  assert.match(result.after, /\.b \{ background: var\(--fixture-accent\) !important; \}/)
  assert.doesNotMatch(result.after, /var\(--fc-fixture-color\)/, 'no consumption site still reads the indirection')
})

// ── Rule 4 (apply + refuse): var(token, #hex) fallback drop ─────────────

test('CSS FILE: a var(token, #hex) fallback that is byte-identical to the live token value is dropped', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/other.css', `.a { color: var(--fixture-accent-hover, #c49e2f); }`)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planCssFile(root, filePath, tables)
  assert.equal(result.edits.length, 1, JSON.stringify(result.edits))
  assert.match(result.after, /color: var\(--fixture-accent-hover\);/)
})

test('TS FILE: a var(token, #hex) fallback that MISMATCHES the live token value is refused, not silently dropped', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Fixture.tsx', `
    export function Fixture() {
      return <span style={{ color: 'var(--fixture-success,#22c55e)' }}>hi</span>
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planTsFile(root, filePath, tables)
  assert.equal(result.edits.length, 0, JSON.stringify(result.edits))
  const refusal = result.refusals.find((r) => r.family === 'ts-var-fallback-drop')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.match(refusal.reason, /does not match/)
  assert.match(result.after, /fixture-success,#22c55e/, 'mismatched fallback left byte-identical')
})

// ── always-refuse: Tailwind palette-hue utility (structural, visible change) ──

test('TS FILE: a Tailwind palette-hue utility class is always refused (visible change under Tailwind v4 OKLCH regeneration), never rewritten', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Fixture.tsx', `
    export function Fixture() {
      return <span className="text-red-500 bg-amber-400/20">hi</span>
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planTsFile(root, filePath, tables)
  assert.equal(result.edits.length, 0)
  const families = result.refusals.map((r) => r.family)
  assert.ok(families.includes('tailwind-palette-utility'), JSON.stringify(result.refusals))
  const values = result.refusals.map((r) => r.value)
  assert.ok(values.includes('text-red-500'))
  assert.ok(values.includes('bg-amber-400/20'))
  assert.equal(result.after, result.before)
})

// ── always-refuse: alpha-concatenation site ──────────────────────────────

test('TS FILE: a `${expr}HH` alpha-concatenation template literal is always refused — cannot prove the result stays valid CSS once the source becomes a token', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/fixture/FixtureBadge.tsx', `
    export function FixtureBadge({ color }) {
      return <span style={{ background: \`\${color}1a\` }}>hi</span>
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planTsFile(root, filePath, tables)
  assert.equal(result.edits.length, 0)
  const refusal = result.refusals.find((r) => r.family === 'alpha-concatenation')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.equal(refusal.value, '`${color}1a`')
  assert.match(refusal.reason, /invalid CSS/)
  assert.equal(result.after, result.before)
})

test('TS FILE: a template literal that is NOT the alpha-concatenation shape (plain interpolation, no trailing hex pair) is not flagged', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/components/Fixture.tsx', `
    export function Fixture({ name }) {
      return <span>{\`Hello \${name}!\`}</span>
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planTsFile(root, filePath, tables)
  assert.equal(result.refusals.filter((r) => r.family === 'alpha-concatenation').length, 0)
})

// ── always-refuse: governed-data file ────────────────────────────────────

test('TS FILE: a hex literal inside a registered governed-data file is refused, never edited, even if it would otherwise match an apply rule', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/lib/fixtureRegistry.ts', `
    export const FIXTURE_COLORS = {
      brandGold: '#D4AF37',
      accentFallback: 'var(--fixture-accent-hover, #c49e2f)',
    }
  `)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planTsFile(root, filePath, tables)
  assert.equal(result.edits.length, 0, JSON.stringify(result.edits))
  const reasons = result.refusals.map((r) => r.family)
  assert.ok(reasons.every((f) => f === 'governed-data'), JSON.stringify(result.refusals))
  assert.ok(result.refusals.some((r) => r.value === '#D4AF37'))
  assert.equal(result.after, result.before, 'governed file is completely untouched even though it contains a byte-identical, otherwise-droppable fallback')
})

// ── always-refuse: ambiguous bare-hex token name (CSS + TS) ─────────────

test('CSS FILE: a bare hex declaration that byte-matches a registered token, but is outside the theme-block/fc-unwrap rules, is refused with the candidate token names', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/other.css', `.a { --fixture-event-text-color: #0a0a0b; }`)
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planCssFile(root, filePath, tables)
  assert.equal(result.edits.length, 0)
  const refusal = result.refusals.find((r) => r.family === 'css-ambiguous-token-name')
  assert.ok(refusal, JSON.stringify(result.refusals))
  assert.match(refusal.reason, /--fixture-primary, --fixture-primitive-surface-0/)
})

// ── idempotence ───────────────────────────────────────────────────────────

test('IDEMPOTENT: a second --apply over an already-rewritten tree makes zero further edits, and refusals persist unchanged', () => {
  const root = fixtureRepo()
  const safeSplitPath = writeSafeSplit(root)
  write(root, 'src/styles/fixture-theme.css', `@theme {\n  --fixture-primary: #0a0a0b;\n  --fixture-broken: #f97316;\n}\n`)
  write(root, 'src/styles/fixture-fc.css', `:root { --fc-fixture-color: var(--fixture-accent); }\n.a { color: var(--fc-fixture-color); }\n`)
  write(root, 'src/components/Fixture.tsx', `export const x = <span style={{ color: 'var(--fixture-gold)' }} />`)

  const first = runCodemod({ repoRoot: root, apply: true, safeSplitPath })
  assert.ok(first.totalEdits >= 3, JSON.stringify(first.files))
  const firstRefusals = first.totalRefusals

  const second = runCodemod({ repoRoot: root, apply: true, safeSplitPath })
  assert.equal(second.totalEdits, 0, 'the OLD values no longer exist anywhere, so the lookup can never match again')
  assert.equal(second.totalRefusals, firstRefusals, 'refusals are a standing fact, not a one-time notice')
})

// ── allowed-roots guard ───────────────────────────────────────────────────

test('ALLOWED-ROOTS GUARD: a matching violation outside src/ and packages/ui/src/ is never scanned, edited, or reported', () => {
  const root = fixtureRepo()
  const safeSplitPath = writeSafeSplit(root)
  write(root, 'other/outside.css', `@theme { --fixture-primary: #0a0a0b; }\n`)
  write(root, 'src/styles/inside.css', `@theme { --fixture-primary: #0a0a0b; }\n`)
  // Neither file is literally named fixture-theme.css, so the theme-block
  // rule (which is scoped by exact relPath) does not fire for either — this
  // test only needs to prove the outside file is never even SCANNED, via a
  // rule that fires regardless of filename: the ambiguous-hex ammunition.
  const result = runCodemod({ repoRoot: root, apply: false, safeSplitPath })
  assert.ok(!result.files.some((f) => f.file.includes('outside.css')), JSON.stringify(result.files.map((f) => f.file)))
  assert.ok(result.files.some((f) => f.file.includes('inside.css')))

  const applied = runCodemod({ repoRoot: root, apply: true, safeSplitPath })
  assert.equal(read(root, 'other/outside.css'), '@theme { --fixture-primary: #0a0a0b; }\n', 'the out-of-root file is untouched even under --apply')
  assert.ok(applied.totalFilesScanned > 0)
})

// ── mutation proof, isolated copy under this lane's evidence dir ─────────
// (dist/design-system-baseline/cli-lanes/c1-prep/COLOUR-SPLIT/mutation-proof/
// — per COMMON-RULES.md, red-state/mutation proofs run on isolated copies in
// the lane's own evidence dir, never against the live repo tree.)

test('MUTATION PROOF: --apply against an isolated evidence-dir copy actually rewrites bytes on disk, and a byte-shifted mutation is correctly left alone instead of mis-transformed', () => {
  const evidenceRoot = resolve('dist/design-system-baseline/cli-lanes/c1-prep/COLOUR-SPLIT/mutation-proof')
  rmSync(evidenceRoot, { recursive: true, force: true })
  mkdirSync(evidenceRoot, { recursive: true })
  const safeSplitPath = writeSafeSplit(evidenceRoot)
  const cssPath = write(evidenceRoot, 'src/styles/fixture-theme.css', `@theme {\n  --fixture-primary: #0a0a0b;\n}\n`)

  const before = readFileSync(cssPath, 'utf8')
  assert.match(before, /--fixture-primary: #0a0a0b;/)

  const result = runCodemod({ repoRoot: evidenceRoot, apply: true, safeSplitPath })
  assert.ok(result.totalEdits > 0)

  const afterDisk = readFileSync(cssPath, 'utf8')
  assert.notEqual(afterDisk, before, 'the file on disk actually changed — a real write, not just a returned diff')
  assert.match(afterDisk, /--fixture-primary: var\(--fixture-primitive-surface-0\);/)
  assert.doesNotMatch(afterDisk, /#0a0a0b/)

  // Mutation: shift the hex by one nibble (#0a0a0c instead of #0a0a0b), at
  // the SAME repo-relative path the theme-block rule is scoped to (a
  // differently-named file wouldn't exercise this rule at all — the point is
  // proving the property-scoped rule itself refuses a drifted value rather
  // than a generic "unknown file" no-op). Isolated in its own fixture repo
  // so it can never collide with the already-rewritten copy above.
  const mutatedRoot = resolve('dist/design-system-baseline/cli-lanes/c1-prep/COLOUR-SPLIT/mutation-proof-mutated')
  rmSync(mutatedRoot, { recursive: true, force: true })
  mkdirSync(mutatedRoot, { recursive: true })
  const mutatedSafeSplitPath = writeSafeSplit(mutatedRoot)
  const mutatedPath = write(mutatedRoot, 'src/styles/fixture-theme.css', `@theme {\n  --fixture-primary: #0a0a0c;\n}\n`)
  const mutatedResult = runCodemod({ repoRoot: mutatedRoot, apply: true, safeSplitPath: mutatedSafeSplitPath })
  const mutatedFile = mutatedResult.files.find((f) => f.file.includes('fixture-theme.css'))
  assert.ok(mutatedFile, 'the file is still scanned and reported')
  assert.equal(mutatedFile.editCount, 0, 'a one-nibble-off hex is refused, not mis-transformed')
  assert.ok(mutatedFile.refusals.some((r) => /has drifted/.test(r.reason)))
  assert.equal(readFileSync(mutatedPath, 'utf8'), '@theme {\n  --fixture-primary: #0a0a0c;\n}\n')

  // Idempotence, proved again on this same disk copy — the artifact this
  // test leaves behind under the evidence dir for the final report.
  const second = runCodemod({ repoRoot: evidenceRoot, apply: true, safeSplitPath })
  assert.equal(second.files.find((f) => f.file.includes('fixture-theme.css'))?.editCount ?? 0, 0)
  assert.ok(existsSync(cssPath))
})

// ── malformed input fails closed, never silently empty ───────────────────

test('an unparseable CSS file produces a parse-error refusal instead of silently reporting zero findings', () => {
  const root = fixtureRepo()
  const filePath = write(root, 'src/styles/broken.css', '.a { color: #0a0a0b \n') // unterminated rule
  const tables = buildTables(SYNTHETIC_SAFE_SPLIT)
  const result = planCssFile(root, filePath, tables)
  assert.equal(result.edits.length, 0)
  assert.equal(result.refusals.length, 1)
  assert.equal(result.refusals[0].family, 'parse-error')
})
