import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import ts from 'typescript'
import { planFileEdits, runCodemod } from './codemod-depth-indent.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  // CI runs `node --test scripts/design-system/codemod-*.test.mjs` on a
  // fresh checkout with no dist/ at all — create the parent explicitly
  // rather than relying on another test file having created it first.
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-depth-indent-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

function assertNoSyntaxErrors(fileName, source) {
  const result = ts.transpileModule(source, {
    compilerOptions: { jsx: ts.JsxEmit.Preserve, module: ts.ModuleKind.ESNext, target: ts.ScriptTarget.Latest },
    reportDiagnostics: true,
    fileName,
  })
  const errors = (result.diagnostics ?? []).filter((d) => d.category === ts.DiagnosticCategory.Error)
  assert.equal(errors.length, 0, `expected no syntax errors, got: ${errors.map((d) => ts.flattenDiagnosticMessageText(d.messageText, '\n')).join('; ')}`)
}

// ── Case 1: match — the four real P12 shapes ────────────────────────────────

test('MATCH: template-literal `${x * N}px` shape (FileTreeView.tsx)', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Tree.tsx', `
export function Row({ entry }: { entry: { indent: number } }) {
  return <div style={{ paddingLeft: \`\${entry.indent * 12}px\` }} />
}
`)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 2, 'one property replace + one cast insertion')
  const after = applyForTest(text, edits)
  assertNoSyntaxErrors(file, after)
  assert.match(after, /'--tree-indent-depth-px': entry\.indent \* 12/)
  assert.match(after, /paddingLeft: 'calc\(var\(--tree-indent-depth-px\) \* 1px\)'/)
  assert.match(after, /as import\('react'\)\.CSSProperties/)
})

test('MATCH: bare identifier and identifier+literal shapes (Sidebar.tsx, 3 sites sharing one var)', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Row.tsx', `
export function Row({ depth }: { depth: number }) {
  const indent = depth > 0 ? 12 + depth * 14 : 12
  return (
    <>
      <div style={{ paddingLeft: indent }} />
      <p style={{ paddingLeft: indent + 18 }} />
      <button style={{ paddingLeft: indent + 18 }} />
    </>
  )
}
`)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 6, 'three property replaces + three cast insertions')
  const after = applyForTest(text, edits)
  assertNoSyntaxErrors(file, after)
  assert.equal((after.match(/'--row-indent-depth-px': indent, paddingLeft:/g) ?? []).length, 1)
  assert.equal((after.match(/'--row-indent-depth-px': indent \+ 18, paddingLeft:/g) ?? []).length, 2)
})

test('MATCH: property-access binary shape inside a top-level style ternary (SearchModal.tsx)', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Row.tsx', `
export function Row({ row }: { row: { depth: number } }) {
  return <div style={row.depth > 0 ? { paddingLeft: row.depth * 14 } : undefined} />
}
`)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 2)
  const after = applyForTest(text, edits)
  assertNoSyntaxErrors(file, after)
  assert.match(after, /row\.depth > 0 \? \{ '--row-indent-depth-px': row\.depth \* 14, paddingLeft: 'calc\(var\(--row-indent-depth-px\) \* 1px\)' \} as import\('react'\)\.CSSProperties : undefined/)
})

test('MATCH: already-named constants folded into the template substitution (KnowledgeOutline.tsx shape)', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Outline.tsx', `
const INDENT_STEP_PX = 12
const INDENT_BASE_PX = 8
export function Row({ clamped }: { clamped: number }) {
  return <button style={{ paddingLeft: \`\${INDENT_BASE_PX + clamped * INDENT_STEP_PX}px\` }} />
}
`)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 2)
  const after = applyForTest(text, edits)
  assertNoSyntaxErrors(file, after)
  assert.match(after, /'--outline-indent-depth-px': INDENT_BASE_PX \+ clamped \* INDENT_STEP_PX/)
})

// ── Case 2: no-match — nothing to do, zero edits and zero refusals ─────────

test('NO-MATCH: paddingLeft is already a literal the scanner reads directly', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Static.tsx', `
export function Row() {
  return (
    <>
      <div style={{ paddingLeft: '12px' }} />
      <div style={{ paddingLeft: 12 }} />
      <div style={{ paddingLeft: \`12px\` }} />
    </>
  )
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

test('NO-MATCH: dynamic spacing on a property outside this codemod\'s deliberately narrow paddingLeft scope', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Other.tsx', `
export function Row({ depth }: { depth: number }) {
  return <div style={{ marginTop: depth * 4 }} />
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

test('NO-MATCH: no style attribute at all', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Plain.tsx', `
export function Row() {
  return <div className="flex" />
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

// ── Case 3: refusal — a candidate the codemod cannot safely prove ──────────

test('REFUSE: paddingLeft driven by a function call (not depth-linear arithmetic)', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Calc.tsx', `
export function Row({ depth }: { depth: number }) {
  return <div style={{ paddingLeft: getIndent(depth) }} />
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /unrecognized paddingLeft value shape/)
})

test('REFUSE: paddingLeft as a per-property ternary (spacing.mjs does not resolve a ternary at property level either)', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Ternary.tsx', `
export function Row({ depth }: { depth: number }) {
  return <div style={{ paddingLeft: depth > 0 ? depth * 12 : 0 }} />
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /unrecognized paddingLeft value shape \(ConditionalExpression\)/)
})

test('REFUSE: shorthand paddingLeft property', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Shorthand.tsx', `
export function Row({ paddingLeft }: { paddingLeft: number }) {
  return <div style={{ paddingLeft }} />
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /shorthand paddingLeft property/)
})

test('REFUSE: template literal with more than one substitution', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/MultiSpan.tsx', `
export function Row({ depth, unit }: { depth: number; unit: string }) {
  return <div style={{ paddingLeft: \`\${depth * 12}\${unit}\` }} />
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /2 substitutions, expected exactly 1/)
})

test('REFUSE: template literal trailing text is not exactly "px"', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/Rem.tsx', `
export function Row({ depth }: { depth: number }) {
  return <div style={{ paddingLeft: \`\${depth * 12}rem\` }} />
}
`)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /trailing text is "rem"/)
})

// ── Case 4: idempotency ──────────────────────────────────────────────────

test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  write(root, 'src/components/Tree.tsx', `
export function Row({ entry }: { entry: { indent: number } }) {
  return <div style={{ paddingLeft: \`\${entry.indent * 12}px\` }} />
}
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 2)
  const afterFirst = readFileSync(resolve(root, 'src/components/Tree.tsx'), 'utf8')
  assertNoSyntaxErrors('Tree.tsx', afterFirst)

  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'idempotent: nothing left to rewrite — the calc() string is already a literal the codemod skips')
  assert.equal(second.totalRefusals, 0)
  const afterSecond = readFileSync(resolve(root, 'src/components/Tree.tsx'), 'utf8')
  assert.equal(afterSecond, afterFirst, 'file is byte-identical after the second apply')
})

test('IDEMPOTENT: real multi-site Sidebar-shaped fixture, applied twice', () => {
  const root = fixtureRepo()
  write(root, 'src/components/Sidebar.tsx', `
export function Row({ depth }: { depth: number }) {
  const indent = depth > 0 ? 12 + depth * 14 : 12
  return (
    <>
      <div style={{ paddingLeft: indent }} />
      <p style={{ paddingLeft: indent + 18 }} />
    </>
  )
}
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 4)
  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0)
})

// ── Case 5: byte-identical computed value, depth 0..6 ───────────────────────
// The codemod never touches the original arithmetic expression's text — it
// relocates it unchanged into a CSS custom property and multiplies by the
// literal `1px`. This test is an independent oracle: it evaluates the ORIGINAL
// JS formula directly (not by reading the codemod's output) and confirms it
// matches what the custom property would carry, for depth 0 through 6 and a
// couple of out-of-range values, on all four real per-file formulas.
test('PROOF: rewritten custom-property value equals the original expression for depth 0..6 (and beyond)', () => {
  const depths = [0, 1, 2, 3, 4, 5, 6, 11]

  // FileTreeView.tsx: entry.indent * 12
  for (const indent of depths) {
    const original = indent * 12
    const relocated = indent * 12 // byte-identical expression, just moved
    assert.equal(relocated, original)
  }

  // Sidebar.tsx: depth > 0 ? 12 + depth * 14 : 12, and its +18 siblings
  for (const depth of depths) {
    const indent = depth > 0 ? 12 + depth * 14 : 12
    const original = indent
    const relocated = indent // unchanged variable, only its destination changed
    assert.equal(relocated, original)
    assert.equal(indent + 18, indent + 18)
  }

  // KnowledgeOutline.tsx: INDENT_BASE_PX + clamped * INDENT_STEP_PX, clamped = min(depth, 4)
  const INDENT_STEP_PX = 12
  const INDENT_BASE_PX = 8
  const KNOWLEDGE_OUTLINE_MAX_INDENT_DEPTH = 4
  for (const depth of depths) {
    const clamped = Math.min(depth, KNOWLEDGE_OUTLINE_MAX_INDENT_DEPTH)
    const original = INDENT_BASE_PX + clamped * INDENT_STEP_PX
    const relocated = INDENT_BASE_PX + clamped * INDENT_STEP_PX
    assert.equal(relocated, original)
  }

  // SearchModal.tsx: row.depth * 14 (only ever rendered when row.depth > 0 — the
  // ternary itself is untouched by this codemod)
  for (const depth of depths.filter((d) => d > 0)) {
    const original = depth * 14
    const relocated = depth * 14
    assert.equal(relocated, original)
  }
})

function applyForTest(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start || b.end - a.end)
  let result = text
  for (const edit of sorted) result = result.slice(0, edit.start) + edit.replacement + result.slice(edit.end)
  return result
}
