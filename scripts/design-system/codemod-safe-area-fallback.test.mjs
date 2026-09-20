import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { commentMask, planFile, runCodemod, FALLBACK } from './codemod-safe-area-fallback.mjs'

// Fixture repos are created fresh per test under dist/design-system-baseline.
// The parent is created first on purpose: a fresh CI checkout has no dist/,
// and an mkdtempSync under a missing parent throws. No committed test may
// READ anything under dist/ -- everything here is written by the test itself.
const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixture() {
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-safe-area-'))
  fixtureRoots.push(root)
  return root
}

function write(root, rel, content) {
  const abs = resolve(root, rel)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content)
  return abs
}

test('inserts exactly one fallback into a bare safe-area env()', () => {
  const plan = planFile('a.tsx', '<div className="pb-[env(safe-area-inset-bottom)]" />')
  assert.equal(plan.editCount, 1)
  assert.equal(plan.edits[0].replacement, `,${FALLBACK}`)
})

test('leaves an env() that already has a fallback alone', () => {
  const plan = planFile('a.css', 'padding-bottom: env(safe-area-inset-bottom, 8px);')
  assert.equal(plan.editCount, 0)
  assert.equal(plan.refusals.length, 0)
})

test('ignores env() names that are not safe-area insets', () => {
  const plan = planFile('a.css', 'width: env(titlebar-area-width);')
  assert.equal(plan.editCount, 0)
})

test('does not touch the expression around the env() -- a rem term stays a rem term', () => {
  const before = 'padding-top: max(0.75rem, env(safe-area-inset-top));'
  const plan = planFile('a.css', before)
  assert.equal(plan.editCount, 1)
  const after = before.slice(0, plan.edits[0].start) + plan.edits[0].replacement + before.slice(plan.edits[0].end)
  assert.equal(after, `padding-top: max(0.75rem, env(safe-area-inset-top,${FALLBACK}));`)
  assert.match(after, /0\.75rem/, 'the root-dependent term belongs to a different family and a different decision')
})

test('splits on top-level commas only, so a nested call is not read as a second argument', () => {
  const plan = planFile('a.css', 'padding: env(safe-area-inset-bottom);')
  assert.equal(plan.editCount, 1)
  const nested = planFile('a.css', 'padding: env(safe-area-inset-bottom, max(1px, 2px));')
  assert.equal(nested.editCount, 0, 'two arguments, already compliant')
})

test('refuses rather than edits an env() inside a comment, in CSS and in TS', () => {
  const css = planFile('a.css', '/* see pb-[env(safe-area-inset-bottom)] above */\npadding: 0;')
  assert.equal(css.editCount, 0)
  assert.equal(css.refusals.length, 1)
  assert.match(css.refusals[0].reason, /inside a comment/)
  const ts = planFile('a.tsx', '// env(safe-area-inset-top) is documented here\nconst x = 1\n')
  assert.equal(ts.editCount, 0)
  assert.equal(ts.refusals.length, 1)
})

test('refuses an unbalanced env() instead of guessing where it ends', () => {
  const plan = planFile('a.css', 'padding: env(safe-area-inset-bottom;')
  assert.equal(plan.editCount, 0)
  assert.equal(plan.refusals.length, 1)
  assert.match(plan.refusals[0].reason, /unbalanced/)
})

test('commentMask marks block and line comments and nothing else', () => {
  const text = 'a/*b*/c//d\ne'
  const mask = commentMask(text)
  assert.equal(mask[0], 0, 'a')
  assert.equal(mask[1], 1, 'start of block comment')
  assert.equal(mask[6], 0, 'c, after the block comment closes')
  assert.equal(mask[7], 1, 'start of line comment')
  assert.equal(mask[11], 0, 'e, on the next line')
})

test('dry-run reports edits without writing, --apply writes the same bytes', () => {
  const root = fixture()
  const abs = write(root, 'src/screens/A.tsx', '<div className="pb-[env(safe-area-inset-bottom)]" />\n')
  const before = readFileSync(abs, 'utf8')

  const dry = runCodemod({ repoRoot: root, apply: false, roots: ['src'] })
  assert.equal(dry.totalEdits, 1)
  assert.equal(readFileSync(abs, 'utf8'), before, 'dry-run must not write')

  const applied = runCodemod({ repoRoot: root, apply: true, roots: ['src'] })
  assert.equal(applied.totalEdits, 1)
  const after = readFileSync(abs, 'utf8')
  assert.notEqual(after, before, 'the file on disk actually changed')
  assert.equal(after, `<div className="pb-[env(safe-area-inset-bottom,${FALLBACK})]" />\n`)
})

test('re-running after --apply plans zero further edits', () => {
  const root = fixture()
  write(root, 'src/screens/A.tsx', '<div className="pb-[env(safe-area-inset-bottom)]" />\n')
  write(root, 'src/styles/b.css', 'main { padding-left: env(safe-area-inset-left); }\n')
  assert.equal(runCodemod({ repoRoot: root, apply: true, roots: ['src'] }).totalEdits, 2)
  assert.equal(runCodemod({ repoRoot: root, apply: false, roots: ['src'] }).totalEdits, 0, 'idempotent')
})

test('MUTATION PROOF: a compliant file mutated back to a bare env() is repaired again, and a non-safe-area env() mutated in is still left alone', () => {
  const root = fixture()
  const abs = write(root, 'src/styles/b.css', `main { padding-left: env(safe-area-inset-left,${FALLBACK}); }\n`)
  assert.equal(runCodemod({ repoRoot: root, apply: false, roots: ['src'] }).totalEdits, 0, 'already compliant')

  writeFileSync(abs, 'main { padding-left: env(safe-area-inset-left); }\n')
  assert.equal(runCodemod({ repoRoot: root, apply: true, roots: ['src'] }).totalEdits, 1, 'the regression is detected and repaired')
  assert.match(readFileSync(abs, 'utf8'), new RegExp(`env\\(safe-area-inset-left,${FALLBACK.replace(/[()\-]/g, '\\$&')}\\)`))

  writeFileSync(abs, 'main { width: env(titlebar-area-width); }\n')
  assert.equal(runCodemod({ repoRoot: root, apply: true, roots: ['src'] }).totalEdits, 0, 'a different env() is not swept up')
  assert.equal(readFileSync(abs, 'utf8'), 'main { width: env(titlebar-area-width); }\n')
})
