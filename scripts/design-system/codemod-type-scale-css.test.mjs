import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { runCodemod, planCssFileEdits, planTsxFileEdits } from './codemod-type-scale-css.mjs'
import { isInAllowedRoot } from './codemod-lib.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  // CI runs `node --test scripts/design-system/codemod-*.test.mjs` on a
  // fresh checkout with no dist/ at all — create the parent explicitly
  // rather than relying on another test file having created it first.
  mkdirSync(resolve('dist/design-system-baseline'), { recursive: true })
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-typescale-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

// ── Pattern 1: plain CSS font-size / font-family declarations ─────────────

test('MATCH (CSS): below-floor font-size is raised to its M2-mapped token, preserving !important, a preceding comment, a sibling custom property and a vendor-prefixed property untouched', () => {
  const root = fixtureRepo()
  const before = `/* dense calendar chip label */
.fc-sovereign-chip {
  --fc-small-font-size: 0.8em;
  font-size: 0.75rem !important;
  -webkit-font-smoothing: antialiased;
}
`
  const file = write(root, 'src/styles/fullcalendar-theme.css', before)
  const { edits, refusals, after: afterText } = planCssFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.match(afterText, /font-size: var\(--type-utility-xs-size\) !important;/)
  assert.match(afterText, /\/\* dense calendar chip label \*\//, 'leading comment preserved')
  assert.match(afterText, /--fc-small-font-size: 0\.8em;/, 'unrelated custom property preserved untouched')
  assert.match(afterText, /-webkit-font-smoothing: antialiased;/, 'vendor-prefixed sibling declaration preserved untouched')
})

test('MATCH (CSS): legacy font-family alias with an inline fallback is replaced by the registered token, dropping the now-redundant fallback', () => {
  const root = fixtureRepo()
  const before = `.node-label {
  font-family: var(--font-body, 'Inter', sans-serif);
}
`
  const file = write(root, 'src/components/workspaces/reactflow-theme.css', before)
  const { edits, refusals, after: afterText } = planCssFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.match(afterText, /font-family: var\(--font-family-body\);/)
})

test('REFUSE (CSS): a root font-size clamp() is left untouched — M2 NEEDS-DECISION, no registered semantic token yet', () => {
  const root = fixtureRepo()
  const before = `html {
  font-size: clamp(12px, var(--user-font-size, 14px), 20px);
}
`
  const file = write(root, 'src/styles/globals.css', before)
  const { edits, refusals, text, after: afterText } = planCssFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /NEEDS-DECISION/)
  assert.equal(afterText, text, 'file content is byte-identical when refused')
})

test('NO-MATCH (CSS): a font-size already on a registered token is left untouched (not a violation, not a refusal)', () => {
  const root = fixtureRepo()
  const before = `.already-fine {
  font-size: var(--type-body-family);
}
`
  const file = write(root, 'src/styles/library.css', before)
  const { edits, refusals } = planCssFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

// ── Pattern 2: JSX inline style object font-size / font-family ────────────

test('MATCH (JSX style): fontSize inside a real style={{}} attribute is rewritten; an identically-named field on an unrelated object is left untouched', () => {
  const root = fixtureRepo()
  const before = `export function Foo({ active }: { active: boolean }) {
  const config = { fontSize: '0.875rem' } // not a style prop — must not match
  return (
    <span style={{ fontSize: '0.875rem', color: 'red' }}>{config.fontSize}</span>
  )
}
`
  const file = write(root, 'src/components/calendar/FullCalendarView.tsx', before)
  const { edits, refusals, text } = planTsxFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1, 'only the style-attribute fontSize is touched, not config.fontSize')
  const after = applyEditsFor(text, edits)
  assert.match(after, /style=\{\{ fontSize: 'var\(--type-body-compact-size\)', color: 'red' \}\}/)
  assert.match(after, /const config = \{ fontSize: '0\.875rem' \}/, 'non-style object field is untouched')
})

test('MATCH (JSX style): brand-disclaimer 0.75rem fontSize is raised to the utility-xs token', () => {
  const root = fixtureRepo()
  const before = `export function BrandDisclaimer({ className }: { className?: string }) {
  return (
    <p
      className={className}
      style={{
        fontSize: '0.75rem',
        lineHeight: '1.4',
        color: 'var(--color-secondary, #E2E8F0)',
        opacity: 0.55,
      }}
    >
      Logos are trademarks of their respective owners.
    </p>
  )
}
`
  const file = write(root, 'src/components/ui/brand-disclaimer.tsx', before)
  const { edits, refusals, text } = planTsxFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = applyEditsFor(text, edits)
  assert.match(after, /fontSize: 'var\(--type-utility-xs-size\)'/)
})

test('MATCH (JSX style): fontFamily legacy alias with inline fallback is replaced, dropping the fallback', () => {
  const root = fixtureRepo()
  const before = `function LettermarkChip() {
  return (
    <span style={{ fontFamily: 'var(--font-outfit, Outfit, sans-serif)', fontWeight: 700 }} />
  )
}
`
  const file = write(root, 'src/components/ui/brand-icon.tsx', before)
  const { edits, refusals, text } = planTsxFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = applyEditsFor(text, edits)
  assert.match(after, /fontFamily: 'var\(--font-family-heading\)'/)
})

// ── Pattern 3: SVG fontSize="9" presentation attribute ─────────────────────

test('REFUSE (SVG attribute): fontSize="9" is never rewritten — var() safety in an SVG presentation attribute is not provable from this harness', () => {
  const root = fixtureRepo()
  const before = `export function AxisLabel() {
  return (
    <text x={0} y={0} textAnchor="end" fontSize="9" fill="var(--color-muted)">
      0
    </text>
  )
}
`
  const file = write(root, 'src/components/library/preview/viewparts/ChartPart.tsx', before)
  const { edits, refusals, text } = planTsxFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.equal(refusals[0].syntax, 'fontSize="9"')
  assert.match(refusals[0].reason, /Safari/)
  assert.equal(readFileSync(file, 'utf8'), text)
})

test('REFUSE (SVG attribute): all six literal occurrences in one file are each refused individually, not just the first', () => {
  const root = fixtureRepo()
  const before = `export function Axes() {
  return (
    <g>
      <text fontSize="9">a</text>
      <text fontSize="9">b</text>
      <text fontSize="9">c</text>
      <text fontSize="9">d</text>
      <text fontSize="9">e</text>
      <text fontSize="9">f</text>
    </g>
  )
}
`
  const file = write(root, 'src/components/library/preview/viewparts/ChartPart.tsx', before)
  const { edits, refusals } = planTsxFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 6)
})

// ── Pattern 4: missing arbitrary type hint ─────────────────────────────────

test('MATCH (missing hint): a registered-colour arbitrary value gets its color: hint added, with zero rendered change', () => {
  const root = fixtureRepo()
  const before = `const badgeVariants = cva('base', {
  variants: {
    variant: {
      error: 'border-transparent bg-[var(--color-error)]/20 text-[var(--badge-error-foreground)]',
      destructive: 'border-transparent bg-[var(--color-error)]/20 text-[var(--badge-error-foreground)]',
    },
  },
})
`
  const file = write(root, 'src/components/ui/badge.tsx', before)
  const { edits, refusals, text } = planTsxFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 2, 'both literal occurrences in the file are fixed, not just the first')
  const after = applyEditsFor(text, edits)
  assert.match(after, /text-\[color:var\(--badge-error-foreground\)\]/)
  assert.doesNotMatch(after, /text-\[var\(--badge-error-foreground\)\]/, 'no un-hinted occurrence survives')
})

test('REFUSE (missing hint): an unregistered --color-primary-fg reference is refused, not guessed', () => {
  const root = fixtureRepo()
  const before = `className={
  'font-mono text-[11px] ' +
  (active
    ? 'text-[var(--color-primary-fg,var(--color-secondary))] border-[var(--color-accent)]'
    : 'text-[var(--color-muted)] border-transparent')
}
`
  const file = write(root, 'src/components/chat/AskUserQuestionCard.tsx', before)
  const { edits, refusals } = planTsxFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /not a registered colour token/)
})

test('REFUSE (missing hint): an unregistered --color-text-secondary reference is refused, not guessed', () => {
  const root = fixtureRepo()
  const before = `<div role="status" className="pointer-events-none text-xs text-[var(--color-text-secondary)]" />
`
  const file = write(root, 'src/components/browser/BrowserLiveView.tsx', before)
  const { edits, refusals } = planTsxFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /not a registered colour token/)
})

// ── Idempotence ─────────────────────────────────────────────────────────────

test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  write(root, 'src/styles/fullcalendar-theme.css', `.chip {
  font-size: 0.65rem;
  font-family: var(--font-body);
}
`)
  write(root, 'src/components/ui/badge.tsx', `const cls = 'text-[var(--badge-error-foreground)]'
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 3)
  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'every rewritten shape no longer matches its own find pattern')
})

// ── Allowed-roots guard ───────────────────────────────────────────────────

test('GUARD: isInAllowedRoot rejects a path outside src/ and packages/ui/src/', () => {
  const root = fixtureRepo()
  assert.equal(isInAllowedRoot(root, resolve(root, 'scripts/design-system/other.css')), false)
  assert.equal(isInAllowedRoot(root, resolve(root, 'src/styles/globals.css')), true)
  assert.equal(isInAllowedRoot(root, resolve(root, 'packages/ui/src/foo.tsx')), true)
})

test('GUARD: a violation sitting outside the allowed roots is never scanned or touched', () => {
  const root = fixtureRepo()
  // A below-floor font-size at the repo root, outside src/ and packages/ui/src/.
  write(root, 'scripts/scratch.css', `.x {\n  font-size: 0.75rem;\n}\n`)
  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.totalEdits, 0)
  assert.equal(result.totalFilesScanned, 0)
  assert.equal(readFileSync(resolve(root, 'scripts/scratch.css'), 'utf8'), '.x {\n  font-size: 0.75rem;\n}\n', 'left byte-identical')
})

// ── helper: apply the edits this codemod produced against a source string,
// mirroring what runCodemod does internally via codemod-lib's applyEdits. ──
function applyEditsFor(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start)
  let result = text
  for (const edit of sorted) {
    result = result.slice(0, edit.start) + edit.replacement + result.slice(edit.end)
  }
  return result
}
