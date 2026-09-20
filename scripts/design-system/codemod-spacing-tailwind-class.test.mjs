import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import ts from 'typescript'
import {
  buildTokenTable, classifyToken, classifyTokens, collectTemplateTokens,
  loadMapping, parseGroupsArg, parseMappingArg, parseSpacingUtility,
  planFileEdits, runCodemod, splitVariantPrefix,
} from './codemod-spacing-tailwind-class.mjs'
import { applyEdits, isInAllowedRoot, parseSourceFile } from './codemod-lib.mjs'

// ── fixtures ─────────────────────────────────────────────────────────────
// No committed test reads dist/design-system-baseline/cli-lanes/c1-prep/M1/
// mapping.json (COMMON-RULES.md) — every mapping used here is a small,
// synthetic fixture this file builds and writes itself, at test-run time,
// under a scratch dir this file also owns and cleans up.

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

const FIXTURE_ROWS = [
  { value: 'gap-2', group: 'NORMALIZED', replacement: 'gap-[var(--space-2)]', pattern: 'tailwind-fraction-utility', reason: '', itemCount: 1 },
  { value: 'gap-1', group: 'NORMALIZED', replacement: 'gap-[var(--space-1)]', pattern: 'tailwind-fraction-utility', reason: '', itemCount: 1 },
  { value: 'px-4', group: 'NORMALIZED', replacement: 'px-[var(--space-3)]', pattern: 'tailwind-fraction-utility', reason: '', itemCount: 1 },
  { value: 'p-2', group: 'NORMALIZED', replacement: 'p-[var(--space-2)]', pattern: 'tailwind-fraction-utility', reason: '', itemCount: 1 },
  { value: 'p-4', group: 'NORMALIZED', replacement: 'p-[var(--space-3)]', pattern: 'tailwind-fraction-utility', reason: '', itemCount: 1 },
  { value: 'mt-px', group: 'IDENTICAL', replacement: 'mt-[var(--border-width-hairline)]', pattern: 'tailwind-fraction-utility', reason: '', itemCount: 1 },
  {
    value: 'focus:p-2', group: 'NORMALIZED', replacement: 'focus:p-[var(--space-2)]',
    pattern: 'variant-prefixed-tailwind-fraction-utility', reason: '', itemCount: 1,
  },
  {
    value: 'px-3', group: 'NEEDS-DECISION', replacement: 'px-[var(--space-2)]', pattern: 'tailwind-fraction-utility',
    reason: '10.5px is 2.5px from the nearest token (--space-2 = 8px) — outside the 2px render-alike band.', itemCount: 1,
  },
  {
    value: 'mt-0.5', group: 'NEEDS-DECISION', replacement: 'mt-[var(--space-0)]', pattern: 'tailwind-fraction-utility',
    reason: 'Nearest token is --space-0 (0px): snapping removes the 1.75px gap entirely rather than approximating it.', itemCount: 1,
  },
  {
    value: 'py-6', group: 'NEEDS-DECISION', replacement: 'py-[var(--space-4)]', pattern: 'tailwind-fraction-utility',
    reason: '21px is 3px from the nearest token (--space-4 = 24px) — outside the 2px render-alike band.', itemCount: 1,
  },
  // A real M1 value that belongs to a DIFFERENT script's pattern — must be
  // silently skipped by this script, never touched, never refused.
  { value: 'ml-[3px]', group: 'NORMALIZED', replacement: 'ml-[var(--space-1)]', pattern: 'arbitrary-bracket-px', reason: '', itemCount: 1 },
]

function fixtureMappingJson(extraRows = []) {
  return { summary: { totalDistinctValues: FIXTURE_ROWS.length + extraRows.length }, rows: [...FIXTURE_ROWS, ...extraRows] }
}

function writeFixtureMapping(root, extraRows = []) {
  return write(root, 'mapping.json', JSON.stringify(fixtureMappingJson(extraRows), null, 2))
}

function table(extraRows = []) {
  return buildTokenTable(fixtureMappingJson(extraRows), '<fixture>')
}

// ── splitVariantPrefix ───────────────────────────────────────────────────

test('splitVariantPrefix: bare utility has no prefix chain', () => {
  assert.deepEqual(splitVariantPrefix('gap-2'), { prefixChain: '', base: 'gap-2' })
})

test('splitVariantPrefix: simple modifier splits at the colon', () => {
  assert.deepEqual(splitVariantPrefix('sm:px-8'), { prefixChain: 'sm:', base: 'px-8' })
})

test('splitVariantPrefix: stacked modifiers split at the LAST top-level colon', () => {
  assert.deepEqual(splitVariantPrefix('max-sm:pointer-coarse:gap-6'), { prefixChain: 'max-sm:pointer-coarse:', base: 'gap-6' })
})

test('splitVariantPrefix: an arbitrary-variant colon inside [...] is not the split point', () => {
  assert.deepEqual(splitVariantPrefix('[&:has(*)]:pb-2'), { prefixChain: '[&:has(*)]:', base: 'pb-2' })
  assert.deepEqual(splitVariantPrefix('[&_p]:my-0.5'), { prefixChain: '[&_p]:', base: 'my-0.5' })
})

// ── parseSpacingUtility ──────────────────────────────────────────────────

test('parseSpacingUtility: recognizes numeric, px, and arbitrary-bracket suffixes', () => {
  assert.deepEqual(parseSpacingUtility('px-3'), { prefix: 'px', rest: '3', negative: false })
  assert.deepEqual(parseSpacingUtility('gap-0.5'), { prefix: 'gap', rest: '0.5', negative: false })
  assert.deepEqual(parseSpacingUtility('gap-px'), { prefix: 'gap', rest: 'px', negative: false })
  assert.deepEqual(parseSpacingUtility('ml-[3px]'), { prefix: 'ml', rest: '[3px]', negative: false })
})

test('parseSpacingUtility: negative sign is reported, not consumed into skip', () => {
  assert.deepEqual(parseSpacingUtility('-mt-1'), { prefix: 'mt', rest: '1', negative: true })
})

test('parseSpacingUtility: a keyword suffix (auto/full/...) is not spacing-shaped — matches spacing.mjs IGNORE_KEYWORDS intent', () => {
  assert.equal(parseSpacingUtility('mx-auto'), null)
})

test('parseSpacingUtility: an exact-zero utility (p-0, gap-0, ...) is never spacing-shaped — 0 is not root-dependent and never a real M1 violation', () => {
  assert.equal(parseSpacingUtility('p-0'), null)
  assert.equal(parseSpacingUtility('gap-0'), null)
  assert.equal(parseSpacingUtility('space-y-0'), null)
})

test('parseSpacingUtility: an already-migrated arbitrary var() reference is not spacing-shaped (idempotence)', () => {
  assert.equal(parseSpacingUtility('gap-[var(--space-2)]'), null)
  assert.equal(parseSpacingUtility('p-[var(--border-width-hairline)]'), null)
})

test('parseSpacingUtility: an unrelated utility never matches a spacing prefix by accident', () => {
  assert.equal(parseSpacingUtility('pointer-events-none'), null)
  assert.equal(parseSpacingUtility('flex'), null)
})

test('parseSpacingUtility: longest-prefix-first — gap-x-2 resolves to prefix gap-x, not gap', () => {
  assert.deepEqual(parseSpacingUtility('gap-x-2'), { prefix: 'gap-x', rest: '2', negative: false })
})

// ── classifyToken ────────────────────────────────────────────────────────

test('classifyToken: plain mapped NORMALIZED value applies', () => {
  const c = classifyToken('gap-2', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'apply')
  assert.equal(c.replacement, 'gap-[var(--space-2)]')
})

test('classifyToken: an explicitly enumerated variant-prefixed row applies verbatim', () => {
  const c = classifyToken('focus:p-2', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'apply')
  assert.equal(c.replacement, 'focus:p-[var(--space-2)]')
})

test('classifyToken: a variant NOT explicitly enumerated composes generically from the bare mapped value', () => {
  const c = classifyToken('hover:gap-2', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'apply')
  assert.equal(c.replacement, 'hover:gap-[var(--space-2)]')
})

test('classifyToken: a bracket-modifier composes correctly too', () => {
  const c = classifyToken('[&:has(*)]:p-2', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'apply')
  assert.equal(c.replacement, '[&:has(*)]:p-[var(--space-2)]')
})

test('classifyToken: NEEDS-DECISION refuses with the mapping\'s OWN reason text, family 1 (N=3 shape)', () => {
  const c = classifyToken('px-3', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'refuse')
  assert.equal(c.category, 'group-not-applied')
  assert.match(c.reason, /2\.5px from the nearest token/)
})

test('classifyToken: NEEDS-DECISION refuses with the mapping\'s OWN reason text, family 2 (N=0.5 shape)', () => {
  const c = classifyToken('mt-0.5', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'refuse')
  assert.equal(c.category, 'group-not-applied')
  assert.match(c.reason, /snapping removes the 1\.75px gap/)
})

test('classifyToken: NEEDS-DECISION refuses with the mapping\'s OWN reason text, family 3 (21/28px step shape)', () => {
  const c = classifyToken('py-6', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'refuse')
  assert.equal(c.category, 'group-not-applied')
  assert.match(c.reason, /3px from the nearest token/)
})

test('classifyToken: --groups can widen to include NEEDS-DECISION once a value is re-classified — data-driven, not hardcoded', () => {
  const c = classifyToken('px-3', table(), ['IDENTICAL', 'NORMALIZED', 'NEEDS-DECISION'])
  assert.equal(c.kind, 'apply')
})

test('classifyToken: a negative utility always refuses, regardless of --groups, and is never silently skipped', () => {
  const c = classifyToken('-mt-1', table(), ['IDENTICAL', 'NORMALIZED', 'NEEDS-DECISION'])
  assert.equal(c.kind, 'refuse')
  assert.equal(c.category, 'negative-utility')
  assert.match(c.reason, /sibling negative-utility lane/)
})

test('classifyToken: a real M1 value owned by a different script\'s pattern is silently skipped', () => {
  const c = classifyToken('ml-[3px]', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'skip')
})

test('classifyToken: a spacing-shaped token entirely absent from the mapping snapshot refuses defensively', () => {
  const c = classifyToken('pl-7', table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(c.kind, 'refuse')
  assert.equal(c.category, 'unmapped-spacing-shaped-token')
})

test('classifyToken: an ordinary non-spacing utility is silently skipped', () => {
  assert.equal(classifyToken('flex', table(), ['IDENTICAL', 'NORMALIZED']).kind, 'skip')
  assert.equal(classifyToken('items-center', table(), ['IDENTICAL', 'NORMALIZED']).kind, 'skip')
  assert.equal(classifyToken('mx-auto', table(), ['IDENTICAL', 'NORMALIZED']).kind, 'skip')
})

test('classifyToken: an exact-zero utility is silently skipped, never a defensive refusal (real-repo false-positive fix)', () => {
  assert.equal(classifyToken('p-0', table(), ['IDENTICAL', 'NORMALIZED']).kind, 'skip')
  assert.equal(classifyToken('sm:gap-0', table(), ['IDENTICAL', 'NORMALIZED']).kind, 'skip')
})

// ── classifyTokens (conflict detection) ──────────────────────────────────

test('classifyTokens: two same-prefix, differing-value tokens in one class list refuse as conflicting, not applied', () => {
  const toks = [{ text: 'p-2', start: 0, end: 3 }, { text: 'p-4', start: 4, end: 7 }]
  const result = classifyTokens(toks, table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(result[0].classification.kind, 'refuse')
  assert.equal(result[0].classification.category, 'conflicting-spacing-token')
  assert.equal(result[1].classification.kind, 'refuse')
  assert.equal(result[1].classification.category, 'conflicting-spacing-token')
})

test('classifyTokens: same prefix under DIFFERENT variant chains does not conflict (hover: vs bare)', () => {
  const toks = [{ text: 'p-2', start: 0, end: 3 }, { text: 'hover:p-4', start: 4, end: 13 }]
  const result = classifyTokens(toks, table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(result[0].classification.kind, 'apply')
  assert.equal(result[1].classification.kind, 'apply')
})

test('classifyTokens: an exact duplicate of the same token is not a conflict', () => {
  const toks = [{ text: 'p-2', start: 0, end: 3 }, { text: 'p-2', start: 4, end: 7 }]
  const result = classifyTokens(toks, table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(result[0].classification.kind, 'apply')
  assert.equal(result[1].classification.kind, 'apply')
})

test('classifyTokens: conflict takes priority over an otherwise-refusable NEEDS-DECISION reason', () => {
  const toks = [{ text: 'px-3', start: 0, end: 4 }, { text: 'px-4', start: 5, end: 9 }]
  const result = classifyTokens(toks, table(), ['IDENTICAL', 'NORMALIZED'])
  assert.equal(result[0].classification.category, 'conflicting-spacing-token')
  assert.equal(result[1].classification.category, 'conflicting-spacing-token')
})

// ── collectTemplateTokens (interpolation boundary) ──────────────────────

function templateTokensOf(src) {
  const sourceFile = parseSourceFile('fixture.tsx', src)
  let found = null
  const walk = (node) => {
    if (!found && ts.isTemplateExpression(node)) { found = node; return }
    ts.forEachChild(node, walk)
  }
  walk(sourceFile)
  assert.ok(found, 'fixture source must contain a TemplateExpression')
  return collectTemplateTokens(found, sourceFile)
}

test('collectTemplateTokens: whitespace-bounded tokens on both sides of an interpolation are eligible', () => {
  const toks = templateTokensOf('const x = `p-2 ${dynamic} gap-4`')
  assert.deepEqual(toks.map((t) => t.text), ['p-2', 'gap-4'])
})

test('collectTemplateTokens: a token glued to an interpolation (no whitespace) is excluded, not guessed at', () => {
  const toks = templateTokensOf('const x = `${dynamic}px-4 foo-${dynamic}`')
  // "px-4" touches the LEFT of its segment (glued to the interpolation before it) -> excluded.
  // "foo-" touches the RIGHT of its segment (glued to the interpolation after it) -> excluded.
  assert.deepEqual(toks.map((t) => t.text), [])
})

// ── mapping loading ──────────────────────────────────────────────────────

test('loadMapping + buildTokenTable: reads the mapping file at run time, not hardcoded', () => {
  const root = scratchDir('codemod-spacing-tailwind-class-load-')
  writeFixtureMapping(root)
  const { json, path } = loadMapping(root, resolve(root, 'mapping.json'))
  assert.equal(path, resolve(root, 'mapping.json'))
  const t = buildTokenTable(json, path)
  assert.equal(t.ours.get('gap-2').replacement, 'gap-[var(--space-2)]')
  // Change the fixture on disk and reload — a *second* load must reflect
  // the new content, proving nothing was cached/hardcoded at import time.
  writeFixtureMapping(root, [{ value: 'gap-2', group: 'NORMALIZED', replacement: 'gap-[var(--space-9)]', pattern: 'tailwind-fraction-utility', reason: '', itemCount: 1 }])
  const { json: json2 } = loadMapping(root, resolve(root, 'mapping.json'))
  const t2 = buildTokenTable(json2, resolve(root, 'mapping.json'))
  assert.equal(t2.ours.get('gap-2').replacement, 'gap-[var(--space-9)]')
})

test('loadMapping: a malformed mapping file fails loudly, not silently', () => {
  const root = scratchDir('codemod-spacing-tailwind-class-badjson-')
  write(root, 'mapping.json', '{ not valid json')
  assert.throws(() => loadMapping(root, resolve(root, 'mapping.json')), /not valid JSON/)
})

// ── CLI arg parsing ──────────────────────────────────────────────────────

test('parseGroupsArg: defaults to IDENTICAL,NORMALIZED', () => {
  assert.deepEqual(parseGroupsArg([]), ['IDENTICAL', 'NORMALIZED'])
})

test('parseGroupsArg: --groups=A,B parses a custom list', () => {
  assert.deepEqual(parseGroupsArg(['--groups=IDENTICAL,NORMALIZED,NEEDS-DECISION']), ['IDENTICAL', 'NORMALIZED', 'NEEDS-DECISION'])
  assert.deepEqual(parseGroupsArg(['--groups', 'NORMALIZED']), ['NORMALIZED'])
})

test('parseMappingArg: reads --mapping=<path> or --mapping <path>', () => {
  assert.equal(parseMappingArg(['--mapping=/tmp/x.json']), '/tmp/x.json')
  assert.equal(parseMappingArg(['--mapping', '/tmp/y.json']), '/tmp/y.json')
  assert.equal(parseMappingArg([]), null)
})

// ── file-level: planFileEdits over real source shapes ────────────────────

function fixtureRepoWithMapping(prefix) {
  const root = scratchDir(prefix)
  const mappingPath = writeFixtureMapping(root)
  return { root, mappingPath }
}

test('planFileEdits: JSX className string literal, cn()/clsx() args, cva variants map, class-string constant, and a negative utility', () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-tailwind-class-file-')
  const filePath = write(root, 'src/components/Example.tsx', `
import { cva } from 'class-variance-authority'
import { cn } from '@/lib/utils'

const BASE_CLASSES = 'p-4 gap-2'

const buttonVariants = cva('gap-1', {
  variants: {
    intent: {
      primary: 'p-2 gap-2',
    },
  },
})

export function Example({ isActive }: { isActive: boolean }) {
  return (
    <div className="gap-2 p-4">
      <span className={cn('p-2', isActive && 'gap-1', -1 ? '' : 'gap-2')} />
      <i className={BASE_CLASSES} />
      <em className={\`-mt-1 gap-2\`} />
    </div>
  )
}
`)
  const table1 = buildTokenTable(JSON.parse(readFileSync(mappingPath, 'utf8')), mappingPath)
  const { edits, refusals } = planFileEdits(root, filePath, table1, ['IDENTICAL', 'NORMALIZED'])

  const editSyntax = edits.map((e) => e.syntax).sort()
  // gap-2 (x5: JSX literal, cn arg's ternary false-branch, BASE_CLASSES,
  // template literal, cva variant map), p-4 (x2: JSX literal, BASE_CLASSES),
  // p-2 (x2: cn arg, cva variant map), gap-1 (x2: cn's &&-guarded right
  // side, cva base arg) — the guard (`isActive`) itself is never visited as
  // a class value.
  assert.equal(editSyntax.filter((s) => s === 'gap-2').length, 5)
  assert.equal(editSyntax.filter((s) => s === 'p-4').length, 2)
  assert.equal(editSyntax.filter((s) => s === 'p-2').length, 2)
  assert.equal(editSyntax.filter((s) => s === 'gap-1').length, 2)
  assert.ok(!refusals.some((r) => r.syntax === 'isActive'), '&&\'s left (guard) operand must never be treated as a class value')

  const negRefusal = refusals.find((r) => r.syntax === '-mt-1')
  assert.ok(negRefusal, 'expected a refusal for the negative utility')
  assert.equal(negRefusal.category, 'negative-utility')
})

test('planFileEdits: a class-string constant referenced from two usage sites is only walked once (no duplicate/overlapping edits)', () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-tailwind-class-dedupe-')
  const filePath = write(root, 'src/components/Dedupe.tsx', `
const SHARED_CLASSES = 'gap-2 p-4'

export function Dedupe() {
  return (
    <div>
      <span className={SHARED_CLASSES} />
      <em className={SHARED_CLASSES} />
    </div>
  )
}
`)
  const table1 = buildTokenTable(JSON.parse(readFileSync(mappingPath, 'utf8')), mappingPath)
  // Must not throw (applyEdits throws on overlapping edits) and must plan
  // the declaration's two tokens exactly once each, not once per usage site.
  const { edits, text } = planFileEdits(root, filePath, table1, ['IDENTICAL', 'NORMALIZED'])
  assert.equal(edits.length, 2)
  assert.deepEqual(edits.map((e) => e.syntax).sort(), ['gap-2', 'p-4'])
  const after = applyEdits(text, edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
  assert.match(after, /const SHARED_CLASSES = 'gap-\[var\(--space-2\)\] p-\[var\(--space-3\)\]'/)
})

test('planFileEdits: NEEDS-DECISION value refuses with its mapping reason, applied value in the same file still edits', () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-tailwind-class-needsdec-')
  const filePath = write(root, 'src/components/NeedsDecision.tsx', `
export function NeedsDecision() {
  return <div className="px-3 gap-1" />
}
`)
  const table1 = buildTokenTable(JSON.parse(readFileSync(mappingPath, 'utf8')), mappingPath)
  const { edits, refusals } = planFileEdits(root, filePath, table1, ['IDENTICAL', 'NORMALIZED'])
  assert.equal(edits.length, 1)
  assert.equal(edits[0].syntax, 'gap-1')
  assert.equal(refusals.length, 1)
  assert.equal(refusals[0].syntax, 'px-3')
  assert.equal(refusals[0].category, 'group-not-applied')
})

test('planFileEdits: an unprovable class string (imported identifier) refuses instead of being skipped silently', () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-tailwind-class-unprovable-')
  const filePath = write(root, 'src/components/Unprovable.tsx', `
import { EXTERNAL_CLASSES } from './constants'

export function Unprovable() {
  return <div className={EXTERNAL_CLASSES} />
}
`)
  const table1 = buildTokenTable(JSON.parse(readFileSync(mappingPath, 'utf8')), mappingPath)
  const { edits, refusals } = planFileEdits(root, filePath, table1, ['IDENTICAL', 'NORMALIZED'])
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.equal(refusals[0].category, 'unprovable-class-string')
  assert.match(refusals[0].reason, /EXTERNAL_CLASSES/)
})

test('planFileEdits: a same-property conflict inside one class list refuses both sides', () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-tailwind-class-conflict-')
  const filePath = write(root, 'src/components/Conflict.tsx', `
export function Conflict() {
  return <div className="p-2 p-4" />
}
`)
  const table1 = buildTokenTable(JSON.parse(readFileSync(mappingPath, 'utf8')), mappingPath)
  const { edits, refusals } = planFileEdits(root, filePath, table1, ['IDENTICAL', 'NORMALIZED'])
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 2)
  assert.ok(refusals.every((r) => r.category === 'conflicting-spacing-token'))
})

// ── idempotence ────────────────────────────────────────────────────────

test('idempotence: applying the plan once leaves nothing further for a second pass to edit', () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-tailwind-class-idempotent-')
  const filePath = write(root, 'src/components/Idempotent.tsx', `
export function Idempotent() {
  return <div className="gap-2 p-4 px-3 -mt-1" />
}
`)
  const table1 = buildTokenTable(JSON.parse(readFileSync(mappingPath, 'utf8')), mappingPath)
  const first = planFileEdits(root, filePath, table1, ['IDENTICAL', 'NORMALIZED'])
  assert.ok(first.edits.length > 0)
  const after = applyEdits(first.text, first.edits.map((e) => ({ start: e.start, end: e.end, replacement: e.replacement })))
  writeFileSync(filePath, after, 'utf8')

  const second = planFileEdits(root, filePath, table1, ['IDENTICAL', 'NORMALIZED'])
  assert.equal(second.edits.length, 0, 'a second pass over already-applied output must make zero further edits')
  // The refusals that were never applied (NEEDS-DECISION, negative) are still there, unchanged.
  assert.equal(second.refusals.length, first.refusals.length)

  const third = applyEdits(second.text, [])
  assert.equal(third, second.text)
})

// ── allowed-roots guard ───────────────────────────────────────────────────

test('allowed-roots guard: runCodemod never touches a file outside src/ or packages/ui/src/', () => {
  const { root, mappingPath } = fixtureRepoWithMapping('codemod-spacing-tailwind-class-roots-')
  const outsidePath = write(root, 'scripts/design-system/rogue.tsx', `export const x = <div className="gap-2" />`)
  write(root, 'src/components/Inside.tsx', `export const y = <div className="gap-2" />`)

  assert.equal(isInAllowedRoot(root, outsidePath), false)

  const result = runCodemod({ repoRoot: root, apply: false, mappingPath, allowedGroups: ['IDENTICAL', 'NORMALIZED'] })
  const touchedFiles = result.files.map((f) => f.file)
  assert.ok(!touchedFiles.includes('scripts/design-system/rogue.tsx'))
  assert.ok(touchedFiles.includes('src/components/Inside.tsx'))

  // Prove it structurally, not just by absence: the outside file is never
  // even in totalFilesScanned's source list.
  assert.equal(result.totalFilesScanned, 1)
})

// ── mutation proof, scratch copy under the lane's own evidence dir ───────

test('mutation proof: --apply actually rewrites bytes on disk in an isolated evidence-dir copy, and a second --apply is a no-op', () => {
  const evidenceRoot = resolve('dist/design-system-baseline/cli-lanes/c1-prep/S-SPACE-1')
  mkdirSync(evidenceRoot, { recursive: true })
  const root = mkdtempSync(resolve(evidenceRoot, 'mutation-proof-'))
  fixtureRoots.push(root)
  const mappingPath = writeFixtureMapping(root)
  const filePath = write(root, 'src/components/Mutate.tsx', `
export function Mutate() {
  return <div className="gap-2 p-4 px-3" />
}
`)
  const before = readFileSync(filePath, 'utf8')
  assert.match(before, /gap-2 p-4 px-3/)

  const applied = runCodemod({ repoRoot: root, apply: true, mappingPath, allowedGroups: ['IDENTICAL', 'NORMALIZED'] })
  assert.equal(applied.totalEdits, 2) // gap-2, p-4 apply; px-3 refuses (NEEDS-DECISION)
  assert.equal(applied.totalRefusals, 1)

  const after = readFileSync(filePath, 'utf8')
  assert.match(after, /gap-\[var\(--space-2\)\] p-\[var\(--space-3\)\] px-3/)
  assert.notEqual(before, after)

  const secondApply = runCodemod({ repoRoot: root, apply: true, mappingPath, allowedGroups: ['IDENTICAL', 'NORMALIZED'] })
  assert.equal(secondApply.totalEdits, 0)
  const afterSecond = readFileSync(filePath, 'utf8')
  assert.equal(afterSecond, after, 'a second --apply must be a byte-for-byte no-op')
})
