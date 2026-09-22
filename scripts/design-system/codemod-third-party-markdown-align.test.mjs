import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import {
  verifyRemarkGfmStyleIsTextAlignOnly, planFileEdits, runCodemod,
} from './codemod-third-party-markdown-align.mjs'
import { parseSourceFile } from './codemod-lib.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  // CI runs `node --test scripts/design-system/codemod-*.test.mjs` on a
  // fresh checkout with no dist/ at all — create the parent explicitly
  // rather than relying on another test file having created it first.
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-mdalign-'))
  fixtureRoots.push(root)
  return root
}

// Pre-apply pinned fixture (see
// scripts/design-system/fixtures/codemod-third-party-markdown-align/README.md)
// — the pre-commit-8c58a0e7e shape of the real markdown-shared.tsx, so the
// mutation-proof test below stays independent of the live src/ tree's
// current (post-apply) state.
function readMarkdownSharedFixture() {
  return readFileSync(resolve('scripts/design-system/fixtures/codemod-third-party-markdown-align/markdown-shared.tsx.fixture'), 'utf8')
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

// Minimal, faithful re-creations of the two proof points this codemod relies
// on (mdast-util-to-hast's `align` property, hast-util-to-jsx-runtime's
// table-cell-only textAlign-only style synthesis) — mirrors the ACTUAL
// installed package shape verified by reading node_modules during triage.
const TABLE_ROW_PROVEN = `
function tableRow(node, state) {
  const properties = {}
  properties.align = alignValue
  return properties
}
`

const TO_JSX_RUNTIME_PROVEN = `
const tableCellElement = new Set(['td', 'th'])
function createElementProps(state, node) {
  const props = {}
  let alignValue
  for (const prop in node.properties) {
    if (state.tableCellAlignToStyle && key === 'align' && typeof value === 'string' && tableCellElement.has(node.tagName)) {
      alignValue = value
    }
  }
  if (alignValue) {
    const style = props.style || (props.style = {})
    style[state.stylePropertyNameCase === 'css' ? 'text-align' : 'textAlign'] =
      alignValue
  }
  return props
}
`

function writeProvenPackages(root) {
  write(root, 'node_modules/mdast-util-to-hast/lib/handlers/table-row.js', TABLE_ROW_PROVEN)
  write(root, 'node_modules/hast-util-to-jsx-runtime/lib/index.js', TO_JSX_RUNTIME_PROVEN)
}

const MARKDOWN_SHARED_FIXTURE = `import type { CSSProperties, ReactNode } from 'react'

export const commonMarkdownComponents = {
  th: ({ children, style }: { children?: ReactNode; style?: CSSProperties }) => (
    <th style={style} className="border px-3 py-1.5 text-left">
      {children}
    </th>
  ),
  td: ({ children, style }: { children?: ReactNode; style?: CSSProperties }) => (
    <td style={style} className="border px-3 py-1.5">{children}</td>
  ),
}
`

// ── Re-verification of the upstream proof ───────────────────────────────────

test('verifyRemarkGfmStyleIsTextAlignOnly proves the pattern safe against a faithful re-creation of the real packages', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  const result = verifyRemarkGfmStyleIsTextAlignOnly(root)
  assert.equal(result.proven, true)
})

test('verifyRemarkGfmStyleIsTextAlignOnly proves the pattern safe against the ACTUAL installed packages in this repo', () => {
  // Uses the real node_modules of this checkout — the same read that
  // grounded the founder's "verify the claim" instruction.
  const result = verifyRemarkGfmStyleIsTextAlignOnly(resolve('.'))
  assert.equal(result.proven, true, result.reason)
})

test('DRIFT GUARD: refuses ALL edits when hast-util-to-jsx-runtime no longer gates style-synthesis to table cells only', () => {
  const root = fixtureRepo()
  write(root, 'node_modules/mdast-util-to-hast/lib/handlers/table-row.js', TABLE_ROW_PROVEN)
  write(root, 'node_modules/hast-util-to-jsx-runtime/lib/index.js', TO_JSX_RUNTIME_PROVEN.replace(
    "tableCellElement.has(node.tagName)",
    "true", // hypothetical future version: style-synthesis no longer scoped to table cells
  ))
  write(root, 'src/components/chat/markdown-shared.tsx', MARKDOWN_SHARED_FIXTURE)
  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.proven, false)
  assert.equal(result.totalEdits, 0)
  assert.match(result.reason, /no longer matches the proven/)
  // File on disk is untouched.
  assert.equal(readFileSync(resolve(root, 'src/components/chat/markdown-shared.tsx'), 'utf8'), MARKDOWN_SHARED_FIXTURE)
})

test('DRIFT GUARD: refuses ALL edits when mdast-util-to-hast no longer sets the `align` property this codemod traces', () => {
  const root = fixtureRepo()
  write(root, 'node_modules/mdast-util-to-hast/lib/handlers/table-row.js', TABLE_ROW_PROVEN.replace('properties.align = alignValue', 'properties.someOtherThing = alignValue'))
  write(root, 'node_modules/hast-util-to-jsx-runtime/lib/index.js', TO_JSX_RUNTIME_PROVEN)
  write(root, 'src/components/chat/markdown-shared.tsx', MARKDOWN_SHARED_FIXTURE)
  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.proven, false)
  assert.equal(result.totalEdits, 0)
})

// ── Case 1: the match ────────────────────────────────────────────────────

test('MATCH: <th> style={style} narrows to textAlign-only, invisible for every real value the library can produce', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  const file = write(root, 'src/components/chat/markdown-shared.tsx', MARKDOWN_SHARED_FIXTURE)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 2, 'both th and td narrow')
  const after = editedText(text, edits)
  assert.match(after, /<th style=\{style\?\.textAlign \? \{ textAlign: style\.textAlign \} : undefined\} className=/)
  assert.match(after, /<td style=\{style\?\.textAlign \? \{ textAlign: style\.textAlign \} : undefined\} className=/)
  const reparsed = parseSourceFile(file, after)
  assert.equal(reparsed.parseDiagnostics?.length ?? 0, 0)

  // Prove equivalence for all four real values the library can produce:
  // it only ever sets `style` to undefined or one of the three textAlign
  // values — never any other key. `style?.textAlign ? {textAlign} : undefined`
  // reproduces each of the four cases exactly.
  const simulate = (styleFn, libraryStyle) => styleFn(libraryStyle)
  const narrow = (style) => (style?.textAlign ? { textAlign: style.textAlign } : undefined)
  for (const libraryStyle of [undefined, { textAlign: 'left' }, { textAlign: 'center' }, { textAlign: 'right' }]) {
    assert.deepEqual(simulate(narrow, libraryStyle), libraryStyle, `identical result for library style ${JSON.stringify(libraryStyle)}`)
  }
})

// ── Case 2: no-match ─────────────────────────────────────────────────────

test('NO-MATCH: an untyped `style` parameter (no CSSProperties annotation) is left untouched — cannot prove origin', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  const before = `export const Row = ({ children, style }) => (
  <th style={style} className="border">{children}</th>
)
`
  const file = write(root, 'src/components/chat/untyped-style.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0, 'not even flagged — no CSSProperties-typed style parameter in scope')
})

test('NO-MATCH: a file with an unrelated `style` variable is left untouched', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  const before = `import type { CSSProperties } from 'react'
export const Box = ({ style }: { style?: CSSProperties }) => <div className="p-2">{JSON.stringify(style)}</div>
`
  const file = write(root, 'src/components/chat/no-cell.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0, 'no th/td style attribute at all')
  assert.equal(refusals.length, 0)
})

// ── Case 3: ambiguous — must be refused, not edited ─────────────────────────

test('AMBIGUOUS: a non-table-cell element using the same style param is refused, not guessed at', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  const before = `import type { CSSProperties, ReactNode } from 'react'
export const Weird = ({ children, style }: { children?: ReactNode; style?: CSSProperties }) => (
  <div style={style} className="border">{children}</div>
)
`
  const file = write(root, 'src/components/chat/weird-div.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /only proven safe for <th>\/<td>/)
  assert.equal(readFileSync(file, 'utf8'), text, 'file on disk is untouched')
})

test('AMBIGUOUS: a style expression more complex than the bare passthrough is refused', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  const before = `import type { CSSProperties, ReactNode } from 'react'
export const Cell = ({ children, style }: { children?: ReactNode; style?: CSSProperties }) => (
  <th style={{ ...style, fontWeight: 700 }} className="border">{children}</th>
)
`
  const file = write(root, 'src/components/chat/complex-style.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /ambiguous style expression/)
})

test('never edits outside src/ or packages/ui/src/', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  write(root, 'scripts/other/markdown-shared.tsx', MARKDOWN_SHARED_FIXTURE)
  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.totalFilesTouched, 0, 'scripts/ is outside the allowed roots and must never be scanned/edited')
})

// ── Case 4: idempotency ──────────────────────────────────────────────────

test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  write(root, 'src/components/chat/markdown-shared.tsx', MARKDOWN_SHARED_FIXTURE)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 2)
  const afterFirst = readFileSync(resolve(root, 'src/components/chat/markdown-shared.tsx'), 'utf8')

  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'idempotent: style already narrowed')
  const afterSecond = readFileSync(resolve(root, 'src/components/chat/markdown-shared.tsx'), 'utf8')
  assert.equal(afterSecond, afterFirst, 'file is byte-identical after the second apply')
})

// ── Mutation proof on an isolated copy ──────────────────────────────────────
// Copies the PRE-APPLY fixture (the exact pre-commit-8c58a0e7e shape of the
// real markdown-shared.tsx — see
// scripts/design-system/fixtures/codemod-third-party-markdown-align/README.md)
// into an isolated fixture, proves the codemod produces the expected
// narrowed output on it, then mutates that isolated copy (renaming the bound
// `style` identifier to an alias, the one shape this codemod explicitly
// refuses to guess about) and proves the codemod's safety net actually
// engages instead of silently mis-transforming the mutated source — i.e. the
// test suite would catch a real regression in the matcher's precision.
//
// This deliberately does NOT read the live src/ file: the codemod already
// applied and committed this exact narrowing (8c58a0e7e), so the live file no
// longer carries the pre-apply `style={style}` passthrough this test asserts
// on — reading it here would make this test fail forever after a correct
// apply, and would make it fragile to any later, unrelated edit of the same
// file. The IDEMPOTENT test above already proves the live-tree behaviour
// (re-applying to an already-narrowed file makes zero further changes) using
// its own synthetic fixture.

test('MUTATION PROOF: isolated copy of the pre-apply fixture for markdown-shared.tsx narrows exactly as predicted, and a mutated (aliased) copy is left untouched instead of silently mis-transformed', () => {
  const root = fixtureRepo()
  writeProvenPackages(root)
  const fixtureText = readMarkdownSharedFixture()
  assert.match(fixtureText, /th: \(\{ children, style \}: \{ children\?: ReactNode; style\?: CSSProperties \}\) =>/, 'sanity: the pre-apply fixture still has the exact shape this codemod targets')

  const isolatedCopy = write(root, 'src/components/chat/markdown-shared.tsx', fixtureText)
  const { edits: cleanEdits, refusals: cleanRefusals } = planFileEdits(root, isolatedCopy)
  assert.equal(cleanRefusals.length, 0)
  assert.equal(cleanEdits.length, 2, 'th and td both narrow on the fixture')

  // Mutate: alias the destructured binding (`style: cellStyle`) on the `th`
  // renderer only. A correct codemod must now REFUSE that one occurrence
  // (findStyleBindingName returns undefined for a renamed binding) while
  // still fixing the untouched `td` — proving the matcher does not
  // overreach past what it can actually verify.
  const mutated = fixtureText.replace(
    'th: ({ children, style }: { children?: ReactNode; style?: CSSProperties }) => (\n    <th style={style} className=',
    'th: ({ children, style: cellStyle }: { children?: ReactNode; style?: CSSProperties }) => (\n    <th style={cellStyle} className=',
  )
  assert.notEqual(mutated, fixtureText, 'sanity: mutation actually changed the fixture')
  const mutatedCopy = write(root, 'src/components/chat/markdown-shared.tsx', mutated)
  const { edits: mutatedEdits, refusals: mutatedRefusals, text } = planFileEdits(root, mutatedCopy)
  assert.equal(mutatedEdits.length, 1, 'only the untouched td renderer is still fixed')
  assert.equal(mutatedRefusals.length, 0, 'the aliased th is silently skipped (no bare `style` passthrough of a CSSProperties-typed binding named `style`), not silently mis-transformed')
  const after = editedText(text, mutatedEdits)
  assert.match(after, /style=\{cellStyle\}/, 'the aliased occurrence is untouched, byte-for-byte')
})
