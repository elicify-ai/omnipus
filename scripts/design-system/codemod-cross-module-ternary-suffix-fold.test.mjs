import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { pathToFileURL } from 'node:url'
import { planFileEdits, runCodemod } from './codemod-cross-module-ternary-suffix-fold.mjs'
import { parseSourceFile } from './codemod-lib.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

// Neutral scratch prefix directly under dist/design-system-baseline/, like
// every other codemod-*.test.mjs — a lane-specific evidence path
// (dist/design-system-baseline/cli-lanes/fanout/L13/) only exists on a
// machine that has run that fanout lane; CI runs on a fresh checkout with no
// dist/ at all, so the parent must be created here, not assumed.
function fixtureRepo() {
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-crossmodule-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

function editedText(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start)
  let result = text
  for (const e of sorted) result = result.slice(0, e.start) + e.replacement + result.slice(e.end)
  return result
}

// ── Case 1: the match — the real BasePreview.tsx shape ─────────────────────
test('MATCH: (ternary of template-with-cross-module-substitution : literal) + conditional suffix — folds the suffix into both branches', () => {
  const root = fixtureRepo()
  const before = `import { INLINE_PREVIEW_BOX_CLASS } from './libraryPreviewVariant'

export function BasePreview({ variant, embed }) {
  const containerClass =
    (variant === 'inline'
      ? \`flex \${INLINE_PREVIEW_BOX_CLASS} flex-col overflow-hidden rounded-md border border-[var(--color-border)]\`
      : 'flex h-full min-h-0 flex-col') + (embed ? ' group' : '')
  return <div className={containerClass} />
}
`
  const file = write(root, 'src/components/BasePreview.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.doesNotMatch(after, /\) \+ \(embed/, 'the top-level BinaryExpression(+) must be gone')
  assert.match(after, /variant === 'inline' \? `flex \$\{INLINE_PREVIEW_BOX_CLASS\}.*\$\{\(embed \? ' group' : ''\)\}` : `flex h-full min-h-0 flex-col\$\{\(embed \? ' group' : ''\)\}`/)
  const reparsed = parseSourceFile(file, after)
  assert.equal(reparsed.parseDiagnostics?.length ?? 0, 0, 'rewritten file must still parse')

  // Runtime equivalence over every (variant, embed) combination — the whole
  // point of the rewrite is that it changes nothing observable.
  const INLINE_PREVIEW_BOX_CLASS = 'h-[28rem] max-h-[70vh] min-h-0'
  const oldExpr = "(variant === 'inline' ? `flex ${INLINE_PREVIEW_BOX_CLASS} flex-col overflow-hidden rounded-md border border-[var(--color-border)]` : 'flex h-full min-h-0 flex-col') + (embed ? ' group' : '')"
  const newExpr = edits[0].replacement
  for (const variant of ['inline', 'pane']) {
    for (const embed of [true, false]) {
      // eslint-disable-next-line no-new-func
      const oldValue = new Function('variant', 'embed', 'INLINE_PREVIEW_BOX_CLASS', `return (${oldExpr});`)(variant, embed, INLINE_PREVIEW_BOX_CLASS)
      // eslint-disable-next-line no-new-func
      const newValue = new Function('variant', 'embed', 'INLINE_PREVIEW_BOX_CLASS', `return (${newExpr});`)(variant, embed, INLINE_PREVIEW_BOX_CLASS)
      assert.equal(newValue, oldValue, `variant=${variant} embed=${embed} must produce the identical string`)
    }
  }
})

test('MATCH: plain string leaves on both ternary branches (no cross-module substitution at all) still folds', () => {
  const root = fixtureRepo()
  const before = `export function Row({ active, big }) {
  const cls = (active ? 'text-[var(--color-accent)]' : 'text-[var(--color-muted)]') + (big ? ' text-lg' : '')
  return <span className={cls} />
}
`
  const file = write(root, 'src/components/Row.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.doesNotMatch(after, /\+ \(big/)
  const reparsed = parseSourceFile(file, after)
  assert.equal(reparsed.parseDiagnostics?.length ?? 0, 0)
})

test('MATCH: suffix is an arbitrary non-string expression (purity is not required — it is embedded, not evaluated, by the codemod)', () => {
  const root = fixtureRepo()
  const before = `export function Row({ active, sideEffecting }) {
  const cls = (active ? 'a' : 'b') + sideEffecting()
  return <span className={cls} />
}
`
  const file = write(root, 'src/components/SideEffect.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = editedText(text, edits)
  assert.match(after, /active \? `a\$\{sideEffecting\(\)\}` : `b\$\{sideEffecting\(\)\}`/)
  const reparsed = parseSourceFile(file, after)
  assert.equal(reparsed.parseDiagnostics?.length ?? 0, 0)
})

// ── Case 2: no-match — a `+` whose left is not a ternary-of-leaves at all ──
test('NO-MATCH: a `+` whose left is not a ConditionalExpression at all is left untouched, no refusal noise', () => {
  const root = fixtureRepo()
  const before = `export function Row({ base, suffix }) {
  const cls = base + suffix
  return <span className={cls} />
}
`
  const file = write(root, 'src/components/PlainConcat.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

// ── Case 3: ambiguous — must be refused, not edited ─────────────────────────
test('AMBIGUOUS: ternary branch is not a string/template leaf (numeric literals, browserInputWebRTC.ts shape) — refused', () => {
  const root = fixtureRepo()
  const before = `export function next(hover, hoverSequence, reliableSequence) {
  return (hover ? hoverSequence : reliableSequence) + 1
}
`
  const file = write(root, 'src/lib/webrtc.ts', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /not a plain string\/template-literal class leaf/)
  assert.equal(readFileSync(file, 'utf8'), text, 'file on disk is untouched')
})

test('AMBIGUOUS: a ternary branch contains a literal backtick — refused rather than risk a broken re-fold', () => {
  const root = fixtureRepo()
  const before = 'export function Row({ active, big }) {\n' +
    '  const cls = (active ? "a \\`weird\\`" : "b") + (big ? \' x\' : \'\')\n' +
    '  return <span className={cls} />\n' +
    '}\n'
  const file = write(root, 'src/components/Backtick.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /backtick, backslash, or literal \$\{/)
})

test('AMBIGUOUS: a ternary branch contains a literal ${ sequence — refused rather than risk a broken re-fold', () => {
  const root = fixtureRepo()
  const before = `export function Row({ active, big }) {
  const cls = (active ? 'a \${weird}' : 'b') + (big ? ' x' : '')
  return <span className={cls} />
}
`
  const file = write(root, 'src/components/DollarBrace.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /backtick, backslash, or literal \$\{/)
})

// ── Case 4: idempotency ──────────────────────────────────────────────────
test('IDEMPOTENT: a second --apply over an already-folded tree makes zero further changes', () => {
  const root = fixtureRepo()
  write(root, 'src/components/BasePreview.tsx', `import { INLINE_PREVIEW_BOX_CLASS } from './libraryPreviewVariant'

export function BasePreview({ variant, embed }) {
  const containerClass =
    (variant === 'inline'
      ? \`flex \${INLINE_PREVIEW_BOX_CLASS} flex-col overflow-hidden rounded-md border border-[var(--color-border)]\`
      : 'flex h-full min-h-0 flex-col') + (embed ? ' group' : '')
  return <div className={containerClass} />
}
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 1)
  const afterFirst = readFileSync(resolve(root, 'src/components/BasePreview.tsx'), 'utf8')

  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'idempotent: the rewritten shape has no top-level BinaryExpression(+) left to fold')
  const afterSecond = readFileSync(resolve(root, 'src/components/BasePreview.tsx'), 'utf8')
  assert.equal(afterSecond, afterFirst, 'file is byte-identical after the second apply')
})

test('never edits outside src/ or packages/ui/src/', () => {
  const root = fixtureRepo()
  write(root, 'scripts/other/Row.tsx', `export function Row({ active, big }) {
  const cls = (active ? 'a' : 'b') + (big ? ' c' : '')
  return <span className={cls} />
}
`)
  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.totalFilesTouched, 0, 'scripts/ is outside the allowed roots and must never be scanned/edited')
})

// ── Mutation proof (test-integrity gate) ──────────────────────────────────
// Flips the codemod's own operator check (PlusToken -> MinusToken) in an
// ISOLATED copy of the source and re-imports it under a fresh module URL.
// If the MATCH assertions above are worth anything, this mutant must fail
// to reproduce the match — proving the test suite would actually catch a
// regression in the codemod's own matching logic, not just execute it.
test('MUTATION PROOF: flipping the PlusToken match condition breaks the MATCH case (kills the mutant)', async () => {
  const root = fixtureRepo()
  const realSource = readFileSync(resolve('scripts/design-system/codemod-cross-module-ternary-suffix-fold.mjs'), 'utf8')
  assert.match(realSource, /ts\.SyntaxKind\.PlusToken/, 'sanity: the real source must contain the operator check this mutation flips')
  const mutated = realSource.replace(
    "node.operatorToken.kind === ts.SyntaxKind.PlusToken",
    "node.operatorToken.kind === ts.SyntaxKind.MinusToken",
  )
  assert.notEqual(mutated, realSource, 'sanity: the mutation must actually change the source')
  // codemod-lib.mjs is imported by relative specifier ('./codemod-lib.mjs'),
  // so the mutant copy must sit next to a real codemod-lib.mjs to resolve it.
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const mutantDir = mkdtempSync(resolve('dist/design-system-baseline/codemod-crossmodule-mutant-'))
  fixtureRoots.push(mutantDir)
  writeFileSync(resolve(mutantDir, 'codemod-lib.mjs'), readFileSync(resolve('scripts/design-system/codemod-lib.mjs'), 'utf8'), 'utf8')
  const mutantPath = resolve(mutantDir, 'codemod-cross-module-ternary-suffix-fold.mjs')
  writeFileSync(mutantPath, mutated, 'utf8')
  const mutantModule = await import(pathToFileURL(mutantPath).href)

  const before = `export function Row({ active, big }) {
  const cls = (active ? 'a' : 'b') + (big ? ' c' : '')
  return <span className={cls} />
}
`
  const file = write(root, 'src/components/Row.tsx', before)
  const { edits } = mutantModule.planFileEdits(root, file)
  assert.equal(edits.length, 0, 'mutant must fail to find the match a correct PlusToken check finds — proves the MATCH test is not vacuous')
})
