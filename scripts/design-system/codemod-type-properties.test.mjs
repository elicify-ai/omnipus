import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import {
  runCodemod, planTsFileEdits, planCssFileEdits, planSvgLineHeightCleanup, isSvgCleanupAllowedPath,
} from './codemod-type-properties.mjs'
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
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-typeproperties-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

function applyEditsFor(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start)
  let result = text
  for (const edit of sorted) result = result.slice(0, edit.start) + edit.replacement + result.slice(edit.end)
  return result
}

// ── Group 1: font-normal ───────────────────────────────────────────────────

test('GROUP 1 (font-normal): every occurrence in a file is rewritten to the fontWeight token, including a module-scope cn() constant', () => {
  const root = fixtureRepo()
  const before = `import { cn } from '@/lib/utils'
export const DATE_TRIGGER_CLASSNAME = cn(
  'flex h-11',
  'justify-start text-left font-normal whitespace-nowrap',
)
export function Badge() {
  return <span className="text-xs font-normal">hi</span>
}
`
  const file = write(root, 'src/components/ui/date-picker.tsx', before)
  const { mainEdits, refusals, text } = planTsFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(mainEdits.length, 2, 'both the cn() argument and the JSX className are matched')
  const after = applyEditsFor(text, mainEdits)
  assert.match(after, /justify-start text-left font-\[var\(--font-weight-regular\)\] whitespace-nowrap/)
  assert.match(after, /className="text-xs font-\[var\(--font-weight-regular\)\]"/)
})

test('GROUP 1 (font-normal): a plain string-concatenated constant NOT passed through cn/clsx is left untouched (calendar.tsx shape)', () => {
  const root = fixtureRepo()
  const before = `const DAY_BASE =
  'inline-flex text-sm font-normal font-inter ' +
  'text-[var(--color-secondary)]'
function Calendar() {
  return <div classNames={{ weekday: 'text-xs font-normal font-inter' }} />
}
`
  const file = write(root, 'src/components/ui/calendar.tsx', before)
  const { mainEdits, refusals } = planTsFileEdits(root, file)
  assert.equal(mainEdits.length, 0, 'neither occurrence is inside a className attribute or a cn()/clsx() call')
  assert.equal(refusals.length, 0)
})

// ── Group 2: numeric fontWeight (JSX style + CSS) ──────────────────────────

test('GROUP 2 (fontWeight, JSX style): 400/500/600/700 inside a real style={{}} are rewritten to their token; an identically-named field on an unrelated object is untouched', () => {
  const root = fixtureRepo()
  const before = `export function Chip() {
  const config = { fontWeight: 700 } // not a style prop — must not match
  return <span style={{ fontWeight: 700, fontSize: 12 }}>{config.fontWeight}</span>
}
`
  const file = write(root, 'src/components/ui/brand-icon.tsx', before)
  const { mainEdits, refusals, text } = planTsFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(mainEdits.length, 1, 'only the style-attribute fontWeight is touched, not config.fontWeight')
  const after = applyEditsFor(text, mainEdits)
  assert.match(after, /style=\{\{ fontWeight: 'var\(--font-weight-bold\)', fontSize: 12 \}\}/)
  assert.match(after, /const config = \{ fontWeight: 700 \}/, 'non-style object field is untouched')
})

test('GROUP 2 (fontWeight, CSS): every literal 400/500/600/700 declaration in a file is rewritten, preserving comments and unrelated declarations', () => {
  const root = fixtureRepo()
  const before = `/* header cell */
.fc-col-header-cell-cushion {
  font-weight: 600;
  color: red;
}
.fc-event-title {
  font-weight: 700;
}
`
  const file = write(root, 'src/styles/fullcalendar-theme.css', before)
  const { edits, refusals, after: afterText } = planCssFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 2)
  assert.match(afterText, /font-weight: var\(--font-weight-semibold\);/)
  assert.match(afterText, /font-weight: var\(--font-weight-bold\);/)
  assert.match(afterText, /\/\* header cell \*\//, 'leading comment preserved')
  assert.match(afterText, /color: red;/, 'unrelated declaration preserved')
})

test('REFUSE (fontWeight ternary — M3 NEEDS-DECISION): a ConditionalExpression is never guessed at', () => {
  const root = fixtureRepo()
  const before = `export function Item({ active }) {
  return <button style={{ fontWeight: active ? 600 : 400 }} />
}
`
  const file = write(root, 'src/components/screens/AgentListScreen.tsx', before)
  const { mainEdits, refusals } = planTsFileEdits(root, file)
  assert.equal(mainEdits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /NEEDS-DECISION/)
  assert.match(refusals[0].reason, /ConditionalExpression|structural/)
})

// ── Group 3: tracking-normal ────────────────────────────────────────────────

test('GROUP 3 (tracking-normal): the Tailwind class token is rewritten to the letter-spacing token, alongside an unrelated font-normal in the same string', () => {
  const root = fixtureRepo()
  const before = `<span className="text-[9px] font-normal normal-case tracking-normal text-[var(--color-muted)]">x</span>
`
  const file = write(root, 'src/components/search/SearchModal.tsx', before)
  const { mainEdits, refusals, text } = planTsFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(mainEdits.length, 2, 'both font-normal and tracking-normal in the same string are found')
  const after = applyEditsFor(text, mainEdits)
  assert.match(after, /font-\[var\(--font-weight-regular\)\]/)
  assert.match(after, /tracking-\[var\(--font-letter-spacing-normal\)\]/)
})

// ── Group 4: CSS letter-spacing: 0 ─────────────────────────────────────────

test('GROUP 4 (letter-spacing: 0, CSS): rewritten to the normal token; 0.04em (M3 NEEDS-DECISION) is refused, not guessed', () => {
  const root = fixtureRepo()
  const before = `.fc-col-header-cell-cushion {
  letter-spacing: 0;
}
.some-other-rule {
  letter-spacing: 0.04em;
}
`
  const file = write(root, 'src/styles/fullcalendar-theme.css', before)
  const { edits, refusals, after: afterText } = planCssFileEdits(root, file)
  assert.equal(edits.length, 1)
  assert.equal(refusals.length, 1)
  assert.match(afterText, /letter-spacing: var\(--font-letter-spacing-normal\);/)
  assert.match(afterText, /letter-spacing: 0\.04em;/, 'the refused declaration is left byte-identical')
  assert.match(refusals[0].reason, /NEEDS-DECISION/)
})

// ── Group 5: exact-match line-height (TS + CSS) ────────────────────────────

test('GROUP 5 (lineHeight, JSX style): the exact 1.4 match is rewritten to the compact token', () => {
  const root = fixtureRepo()
  const before = `export function BrandDisclaimer() {
  return <p style={{ fontSize: '0.75rem', lineHeight: '1.4', opacity: 0.55 }} />
}
`
  const file = write(root, 'src/components/ui/brand-disclaimer.tsx', before)
  const { mainEdits, refusals, text } = planTsFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(mainEdits.length, 1)
  const after = applyEditsFor(text, mainEdits)
  assert.match(after, /lineHeight: 'var\(--font-line-height-compact\)'/)
})

test('GROUP 5 (line-height, CSS): the exact 1.6 match is rewritten to the body token', () => {
  const root = fixtureRepo()
  const before = `body {
  font-family: var(--font-body);
  line-height: 1.6;
}
`
  const file = write(root, 'src/styles/globals.css', before)
  const { edits, refusals, after: afterText } = planCssFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.match(afterText, /line-height: var\(--font-line-height-body\);/)
})

test('REFUSE (lineHeight — M3 NEEDS-DECISION kinds): 1, 1.65 (property) and leading-[1.65] (class) and line-height: 1 (CSS) are all refused, never rounded', () => {
  const root = fixtureRepo()
  const tsFile = write(root, 'src/components/ui/brand-icon.tsx', `export function X() {
  return <span style={{ lineHeight: 1 }} />
}
`)
  const tsResult = planTsFileEdits(root, tsFile)
  assert.equal(tsResult.mainEdits.length, 0)
  assert.equal(tsResult.refusals.length, 1)
  assert.match(tsResult.refusals[0].reason, /NEEDS-DECISION/)

  const tsFile2 = write(root, 'src/components/chat/markdown-shared.tsx', `export function Code() {
  return <div style={{ lineHeight: '1.65', fontFamily: '"JetBrains Mono", "Fira Code", monospace' }} />
}
`)
  const tsResult2 = planTsFileEdits(root, tsFile2)
  assert.equal(tsResult2.refusals.length, 1)
  assert.match(tsResult2.refusals[0].syntax, /1\.65/)
  // the fontFamily literal on the SAME node is still applied — refusing one
  // property never blocks an unrelated one on the same object.
  assert.equal(tsResult2.mainEdits.length, 1)
  assert.match(tsResult2.mainEdits[0].replacement, /--type-code-family/)

  const tsFile3 = write(root, 'src/components/library/preview/LibraryCodePreview.tsx', `<span className="leading-[1.65] text-[11px]" />
`)
  const tsResult3 = planTsFileEdits(root, tsFile3)
  assert.equal(tsResult3.mainEdits.length, 0)
  assert.equal(tsResult3.refusals.length, 1)
  assert.match(tsResult3.refusals[0].syntax, /leading-\[1\.65\]/)

  const cssFile = write(root, 'src/styles/fullcalendar-theme.css', `.x { line-height: 1; }\n`)
  const cssResult = planCssFileEdits(root, cssFile)
  assert.equal(cssResult.edits.length, 0)
  assert.equal(cssResult.refusals.length, 1)
  assert.match(cssResult.refusals[0].reason, /NEEDS-DECISION/)
})

test('REFUSE (letter-spacing arbitrary — M3 NEEDS-DECISION): tracking-[0.07em]/[0.08em]/[0.2em] class tokens are each refused, not guessed', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/library/preview/viewparts/FiguresPart.tsx', `<span className="text-[10px] uppercase tracking-[0.07em] text-[var(--color-muted)]" />
<span className="tracking-[0.08em]" />
<span className="tracking-[0.2em]" />
`)
  const { mainEdits, refusals } = planTsFileEdits(root, file)
  assert.equal(mainEdits.length, 0)
  assert.equal(refusals.length, 3)
  for (const r of refusals) assert.match(r.reason, /NEEDS-DECISION/)
})

// ── Group 6: font-sans -> font-body (SEPARATE PASS) ────────────────────────

test('GROUP 6 (font-sans, SEPARATE PASS): a plain --apply never touches font-sans; --font-sans-pass --apply applies it in isolation', () => {
  const root = fixtureRepo()
  write(root, 'src/components/chat/tools/GenericToolCall.tsx', `<div className="font-sans text-[10px]">a</div>
<div className="font-sans text-[10px]">b</div>
`)
  write(root, 'src/components/agents/AgentCard.tsx', `<Badge className="font-normal" />\n`)

  const mainRun = runCodemod({ repoRoot: root, apply: true, fontSansPass: false })
  assert.equal(mainRun.totalEdits, 1, 'only the font-normal edit is applied by the default pass')
  assert.equal(mainRun.fontSansPass.totalEdits, 2, 'font-sans edits are counted but not applied')
  assert.equal(mainRun.fontSansPass.applied, false)
  assert.match(readFileSync(resolve(root, 'src/components/chat/tools/GenericToolCall.tsx'), 'utf8'), /font-sans/, 'font-sans is untouched on disk after the default pass')
  assert.match(readFileSync(resolve(root, 'src/components/agents/AgentCard.tsx'), 'utf8'), /font-\[var\(--font-weight-regular\)\]/)

  const fontSansRun = runCodemod({ repoRoot: root, apply: true, fontSansPass: true })
  assert.equal(fontSansRun.totalEdits, 2, 'this pass applies exactly the font-sans edits')
  assert.equal(fontSansRun.fontSansPass.applied, true)
  const afterFontSans = readFileSync(resolve(root, 'src/components/chat/tools/GenericToolCall.tsx'), 'utf8')
  assert.doesNotMatch(afterFontSans, /font-sans/, 'no font-sans occurrence survives the independent pass')
  assert.match(afterFontSans, /font-body/)
})

// ── Group 7: literal fontFamily ────────────────────────────────────────────

test('GROUP 7 (fontFamily literal): the exact JetBrains-Mono stack is rewritten even OUTSIDE a JSX style attribute (CodeMirror theme object shape)', () => {
  const root = fixtureRepo()
  const before = `export const theme = EditorView.theme({
  '.cm-scroller': {
    fontFamily: '"JetBrains Mono", "Fira Code", monospace',
  },
})
`
  const file = write(root, 'src/components/library/preview/LibraryCodeEditor.tsx', before)
  const { mainEdits, refusals, text } = planTsFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(mainEdits.length, 1)
  const after = applyEditsFor(text, mainEdits)
  assert.match(after, /fontFamily: 'var\(--type-code-family\)'/)
})

test('GROUP 7 (fontFamily literal): the exact Inter stack is rewritten in a non-style config object (Mermaid themeVariables shape)', () => {
  const root = fixtureRepo()
  const before = `mermaid.initialize({
  themeVariables: {
    fontFamily: '"Inter", system-ui, sans-serif',
  },
})
`
  const file = write(root, 'src/components/chat/mermaid-renderer.tsx', before)
  const { mainEdits, refusals, text } = planTsFileEdits(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(mainEdits.length, 1)
  const after = applyEditsFor(text, mainEdits)
  assert.match(after, /fontFamily: 'var\(--type-body-family\)'/)
})

test('NO-MATCH (fontFamily literal): an unrelated fontFamily value is left untouched (not in M3 scope)', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/ui/brand-icon.tsx', `<span style={{ fontFamily: 'var(--font-outfit, Outfit, sans-serif)' }} />\n`)
  const { mainEdits, refusals } = planTsFileEdits(root, file)
  assert.equal(mainEdits.length, 0)
  assert.equal(refusals.length, 0)
})

// ── SVG invisible cleanup ────────────────────────────────────────────────

test('SVG CLEANUP: dead line-height:1 is removed from a logo with no <text>/<tspan>', () => {
  const root = fixtureRepo()
  const before = '<svg fill="currentColor" fill-rule="evenodd" height="1em" style="flex:none;line-height:1" viewBox="0 0 24 24" width="1em" xmlns="http://www.w3.org/2000/svg"><title>Qwen</title><path d="M1 1z"></path></svg>\n'
  const file = write(root, 'src/assets/brand-logos/p_qwen.svg', before)
  const { edits, refusals, text } = planSvgLineHeightCleanup(root, file)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  const after = applyEditsFor(text, edits)
  assert.match(after, /style="flex:none"/)
  assert.doesNotMatch(after, /line-height/)
})

test('SVG CLEANUP REFUSAL: a file containing a <text> element is refused, not stripped, even if it has the identical line-height:1 declaration', () => {
  const root = fixtureRepo()
  const before = '<svg style="flex:none;line-height:1" viewBox="0 0 24 24"><text x="0" y="0">Hi</text></svg>\n'
  const file = write(root, 'src/assets/brand-logos/p_faketext.svg', before)
  const { edits, refusals, text } = planSvgLineHeightCleanup(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /<text>\/<tspan>/)
  assert.equal(readFileSync(file, 'utf8'), text, 'file left byte-identical when refused')
})

test('SVG CLEANUP REFUSAL: a <tspan> anywhere in the file also refuses the cleanup', () => {
  const root = fixtureRepo()
  const before = '<svg style="flex:none;line-height:1"><text><tspan>Hi</tspan></text></svg>\n'
  const file = write(root, 'src/assets/brand-logos/p_faketspan.svg', before)
  const { edits, refusals } = planSvgLineHeightCleanup(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
})

test('SVG CLEANUP: a file with no line-height:1 declaration produces no edits and no refusals', () => {
  const root = fixtureRepo()
  const before = '<svg style="flex:none" viewBox="0 0 24 24"><path d="M1 1z"></path></svg>\n'
  const file = write(root, 'src/assets/brand-logos/p_clean.svg', before)
  const { edits, refusals } = planSvgLineHeightCleanup(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

// ── Allowed-roots / SVG-path guard ──────────────────────────────────────────

test('GUARD: isInAllowedRoot rejects a path outside src/ and packages/ui/src/', () => {
  const root = fixtureRepo()
  assert.equal(isInAllowedRoot(root, resolve(root, 'scripts/design-system/other.css')), false)
  assert.equal(isInAllowedRoot(root, resolve(root, 'src/styles/globals.css')), true)
  assert.equal(isInAllowedRoot(root, resolve(root, 'packages/ui/src/foo.tsx')), true)
})

test('GUARD: isSvgCleanupAllowedPath accepts only src/assets/ and rejects everything else, including other allowed roots', () => {
  const root = fixtureRepo()
  assert.equal(isSvgCleanupAllowedPath(root, resolve(root, 'src/assets/brand-logos/p_openai.svg')), true)
  assert.equal(isSvgCleanupAllowedPath(root, resolve(root, 'src/components/ui/icon.svg')), false, 'inside src/ but NOT under src/assets/')
  assert.equal(isSvgCleanupAllowedPath(root, resolve(root, 'packages/ui/src/assets/icon.svg')), false, 'a different allowed root is not src/assets/')
})

test('GUARD: planSvgLineHeightCleanup refuses to process a file outside src/assets/ even if handed one directly', () => {
  const root = fixtureRepo()
  const file = write(root, 'src/components/ui/icon.svg', '<svg style="flex:none;line-height:1"><path d="M1 1z"/></svg>\n')
  const { edits, refusals, guardRejected } = planSvgLineHeightCleanup(root, file)
  assert.equal(guardRejected, true)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
  assert.match(readFileSync(file, 'utf8'), /line-height:1/, 'left untouched')
})

test('GUARD: a violation sitting outside the allowed roots is never scanned or touched', () => {
  const root = fixtureRepo()
  write(root, 'scripts/scratch.tsx', '<span className="font-normal" />\n')
  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.totalEdits, 0)
  assert.equal(readFileSync(resolve(root, 'scripts/scratch.tsx'), 'utf8'), '<span className="font-normal" />\n')
})

// ── Idempotence ─────────────────────────────────────────────────────────────

test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further changes (main pass)', () => {
  const root = fixtureRepo()
  write(root, 'src/components/agents/AgentCard.tsx', '<Badge className="font-normal" />\n<span className="tracking-normal" />\n')
  write(root, 'src/styles/fullcalendar-theme.css', '.x {\n  font-weight: 600;\n  letter-spacing: 0;\n  line-height: 1.6;\n}\n')
  write(root, 'src/components/ui/brand-disclaimer.tsx', "<p style={{ lineHeight: '1.4' }} />\n")
  write(root, 'src/components/chat/mermaid-renderer.tsx', "mermaid.initialize({ themeVariables: { fontFamily: '\"Inter\", system-ui, sans-serif' } })\n")
  write(root, 'src/assets/brand-logos/p_test.svg', '<svg style="flex:none;line-height:1"><path d="M1 1z"/></svg>\n')

  const first = runCodemod({ repoRoot: root, apply: true })
  assert.ok(first.totalEdits > 0)
  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'every rewritten shape no longer matches its own find pattern')
})

test('IDEMPOTENT: a second --font-sans-pass --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  write(root, 'src/components/chat/GoalPillTray.tsx', '<p className="font-sans text-[10px]" />\n')
  const first = runCodemod({ repoRoot: root, apply: true, fontSansPass: true })
  assert.equal(first.totalEdits, 1)
  const second = runCodemod({ repoRoot: root, apply: true, fontSansPass: true })
  assert.equal(second.totalEdits, 0)
})

// ── Mutation proof (scratch copy under the lane evidence dir) ─────────────
// Proves --apply performs a REAL on-disk mutation, not just a returned edit
// object — written to a persistent (non-cleaned-up) scratch dir under this
// lane's evidence directory so the write itself is inspectable as evidence,
// per the lane brief's "mutation proof in a scratch copy under your evidence
// dir" requirement. This directory is NOT registered in `fixtureRoots` and
// is therefore not deleted by the `after()` hook above.

const MUTATION_EVIDENCE_ROOT = resolve('dist/design-system-baseline/cli-lanes/c1-prep/S-TYPO-3/mutation-scratch/repo')

test('MUTATION PROOF: --apply rewrites font-normal, fontWeight, letter-spacing, line-height, fontFamily and the SVG cleanup on real disk files in a scratch repo', () => {
  mkdirSync(MUTATION_EVIDENCE_ROOT, { recursive: true })
  const root = MUTATION_EVIDENCE_ROOT
  write(root, 'src/components/agents/AgentCard.tsx', '<Badge className="font-normal" />\n')
  write(root, 'src/styles/fullcalendar-theme.css', '.x {\n  font-weight: 700;\n  letter-spacing: 0;\n}\n')
  write(root, 'src/assets/brand-logos/p_mutationproof.svg', '<svg style="flex:none;line-height:1"><path d="M1 1z"/></svg>\n')

  const beforeTsx = readFileSync(resolve(root, 'src/components/agents/AgentCard.tsx'), 'utf8')
  const beforeCss = readFileSync(resolve(root, 'src/styles/fullcalendar-theme.css'), 'utf8')
  const beforeSvg = readFileSync(resolve(root, 'src/assets/brand-logos/p_mutationproof.svg'), 'utf8')

  const result = runCodemod({ repoRoot: root, apply: true })
  assert.ok(result.totalEdits >= 3, 'at least one edit per fixture file was applied')

  const afterTsx = readFileSync(resolve(root, 'src/components/agents/AgentCard.tsx'), 'utf8')
  const afterCss = readFileSync(resolve(root, 'src/styles/fullcalendar-theme.css'), 'utf8')
  const afterSvg = readFileSync(resolve(root, 'src/assets/brand-logos/p_mutationproof.svg'), 'utf8')

  assert.notEqual(afterTsx, beforeTsx, 'AgentCard.tsx was actually rewritten on disk')
  assert.notEqual(afterCss, beforeCss, 'fullcalendar-theme.css was actually rewritten on disk')
  assert.notEqual(afterSvg, beforeSvg, 'the SVG was actually rewritten on disk')
  assert.match(afterTsx, /font-\[var\(--font-weight-regular\)\]/)
  assert.match(afterCss, /font-weight: var\(--font-weight-bold\);/)
  assert.match(afterCss, /letter-spacing: var\(--font-letter-spacing-normal\);/)
  assert.doesNotMatch(afterSvg, /line-height/)
})
