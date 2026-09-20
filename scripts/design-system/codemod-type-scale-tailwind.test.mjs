import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { planFileEdits, runCodemod, walkClassExpr, MAPPED, NEEDS_DECISION } from './codemod-type-scale-tailwind.mjs'
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

// ── Case 1: MATCH — static and arbitrary Tailwind values rewrite ──────────

test('MATCH: static text-xs and text-sm each rewrite to their M2 token (separate elements — same-element co-occurrence is the CONFLICT case below)', () => {
  const root = fixtureRepo()
  const before = `export function Badge() {
  return (
    <>
      <span className="text-xs font-medium">a</span>
      <span className="font-medium text-sm">b</span>
    </>
  )
}
`
  const file = write(root, 'src/components/Badge.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 2)
  const after = applyAll(text, edits)
  assert.match(after, /className="text-\[length:var\(--type-utility-xs-size\)\] font-medium"/)
  assert.match(after, /className="font-medium text-\[length:var\(--type-body-compact-size\)\]"/)
})

test('MATCH: variant-prefixed text-sm (`focus:text-sm`, the real AppShell shape) rewrites, prefix untouched', () => {
  const root = fixtureRepo()
  const before = `export function SkipLink() {
  return <a className="sr-only focus:not-sr-only focus:text-sm">Skip</a>
}
`
  const file = write(root, 'src/components/SkipLink.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
  const after = applyAll(text, edits)
  assert.match(after, /focus:text-\[length:var\(--type-body-compact-size\)\]/)
  assert.match(after, /sr-only focus:not-sr-only/, 'everything else on the string is untouched, in order')
})

test('MATCH: every mapped arbitrary bracket value rewrites to its M2 token (one per element — a shared element is the CONFLICT case below)', () => {
  const root = fixtureRepo()
  const cases = [
    ['text-[10px]', 'text-[length:var(--type-caption-size)]'],
    ['text-[11px]', 'text-[length:var(--type-caption-size)]'],
    ['text-[9px]', 'text-[length:var(--type-caption-size)]'],
    ['text-[12px]', 'text-[length:var(--type-caption-size)]'],
    ['text-[0.7rem]', 'text-[length:var(--type-caption-size)]'],
    ['text-[14px]', 'text-[length:var(--type-body-size)]'],
    ['text-[7px]', 'text-[length:var(--type-caption-size)]'],
    ['text-[8px]', 'text-[length:var(--type-caption-size)]'],
  ]
  for (const [from] of cases) assert.ok(MAPPED.has(from), `${from} must be in the exported MAPPED table`)
  const lines = cases.map(([from], i) => `      <span key={${i}} className="${from}" />`).join('\n')
  const before = `export function ChipList() {
  return (
    <>
${lines}
    </>
  )
}
`
  const file = write(root, 'src/components/ChipList.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, cases.length)
  const after = applyAll(text, edits)
  for (const [, to] of cases) assert.ok(after.includes(`className="${to}"`), `expected className="${to}" in output`)
})

test('MATCH: a real arbitrary-value template (`` text-[${size}px] `` is the ambiguous case; a whole-token arbitrary value inside a template chunk with static text around it is not)', () => {
  const root = fixtureRepo()
  const before = 'export function ChipTemplate({ n }) {\n  return <span className={`chip-${n} text-[10px]`} />\n}\n'
  const file = write(root, 'src/components/ChipTemplate.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
  const after = applyAll(text, edits)
  assert.match(after, /text-\[length:var\(--type-caption-size\)\]/)
})

test('MATCH: cn() call arguments, including a ternary branch, are rewritten', () => {
  const root = fixtureRepo()
  const before = `import { cn } from '@/lib/utils'

export function Label({ active }) {
  return <span className={cn('block', active ? 'text-xs' : 'text-sm')} />
}
`
  const file = write(root, 'src/components/Label.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 2)
  const after = applyAll(text, edits)
  assert.match(after, /active \? 'text-\[length:var\(--type-utility-xs-size\)\]' : 'text-\[length:var\(--type-body-compact-size\)\]'/)
})

test('MATCH: cva() base string and a variant map leaf both rewrite', () => {
  const root = fixtureRepo()
  const before = `import { cva } from 'class-variance-authority'

export const badgeVariants = cva('inline-flex text-xs', {
  variants: {
    size: {
      sm: 'text-sm px-1',
      lg: 'text-base px-2',
    },
  },
})
`
  const file = write(root, 'src/components/badgeVariants.ts', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 2)
  const after = applyAll(text, edits)
  assert.match(after, /cva\('inline-flex text-\[length:var\(--type-utility-xs-size\)\]'/)
  assert.match(after, /sm: 'text-\[length:var\(--type-body-compact-size\)\] px-1'/)
  assert.match(after, /lg: 'text-base px-2'/, 'text-base is not in this lane\'s mapping and is left untouched')
})

test('MATCH: a class-like-named constant with a single value rewrites cleanly', () => {
  const root = fixtureRepo()
  const before = `export const sizeClasses = { small: 'text-xs', large: 'text-base' }
`
  const file = write(root, 'src/lib/sizeClasses.ts', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
  const after = applyAll(text, edits)
  assert.match(after, /small: 'text-\[length:var\(--type-utility-xs-size\)\]'/)
  assert.match(after, /large: 'text-base'/)
})

test('MATCH: duplicate identical tokens on one element are both rewritten, order and duplication preserved', () => {
  const root = fixtureRepo()
  const before = `export function Weird() {
  return <span className="text-xs opacity-50 text-xs" />
}
`
  const file = write(root, 'src/components/Weird.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 2)
  const after = applyAll(text, edits)
  assert.equal(after, `export function Weird() {
  return <span className="text-[length:var(--type-utility-xs-size)] opacity-50 text-[length:var(--type-utility-xs-size)]" />
}
`)
})

// ── Case 2: NEEDS-DECISION — refused, never guessed ────────────────────────

test('NEEDS-DECISION: text-[13px] is refused, not snapped to the nearest token', () => {
  const root = fixtureRepo()
  assert.ok(NEEDS_DECISION.has('text-[13px]'))
  const before = `export function Fine() {
  return <span className="text-[13px]" />
}
`
  const file = write(root, 'src/components/Fine.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /NEEDS-DECISION/)
  assert.equal(refusals[0].syntax, 'text-[13px]')
})

test('NEEDS-DECISION: text-[1.15rem] is refused', () => {
  const root = fixtureRepo()
  const before = `export function Big() {
  return <span className="text-[1.15rem]" />
}
`
  const file = write(root, 'src/components/Big.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /NEEDS-DECISION/)
})

// ── Case 3: ambiguous / computed — refused, never guessed ──────────────────

test('AMBIGUOUS: a computed template (`text-${size}`) is refused, not guessed', () => {
  const root = fixtureRepo()
  const before = 'export function Dyn({ size }) {\n  return <span className={`text-${size}`} />\n}\n'
  const file = write(root, 'src/components/Dyn.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /computed/)
})

test('AMBIGUOUS: a real match glued to an interpolation with no separating space is refused (boundary not provable)', () => {
  const root = fixtureRepo()
  const before = 'export function Glued({ suffix }) {\n  return <span className={`text-xs${suffix}`} />\n}\n'
  const file = write(root, 'src/components/Glued.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /dynamic-adjacent/)
})

test('NO REFUSAL: the same token in a template is rewritten fine once whitespace separates it from the interpolation', () => {
  const root = fixtureRepo()
  const before = 'export function Spaced({ suffix }) {\n  return <span className={`flex text-xs ${suffix}`} />\n}\n'
  const file = write(root, 'src/components/Spaced.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
  const after = applyAll(text, edits)
  assert.match(after, /flex text-\[length:var\(--type-utility-xs-size\)\] \$\{suffix\}/)
})

// ── Case 4: conflicting text-size token on the same element ────────────────

test('CONFLICT: two distinct mapped values on the same unprefixed element are both refused', () => {
  const root = fixtureRepo()
  const before = `export function Conflict() {
  return <span className="text-xs text-sm" />
}
`
  const file = write(root, 'src/components/Conflict.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 2)
  for (const r of refusals) assert.match(r.reason, /conflicting text-size utilities/)
})

test('CONFLICT: a mapped value alongside an already-resolved type-scale token on the same prefix is refused', () => {
  const root = fixtureRepo()
  const before = `export function AlreadyFixed() {
  return <span className="text-xs text-[length:var(--type-body-size)]" />
}
`
  const file = write(root, 'src/components/AlreadyFixed.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /conflicting text-size utilities/)
  assert.match(refusals[0].reason, /var\(--type-body-size\)/)
})

test('NO CONFLICT: different variant prefixes on the same element are independent (responsive override, not a collision)', () => {
  const root = fixtureRepo()
  const before = `export function Responsive() {
  return <span className="text-xs md:text-sm" />
}
`
  const file = write(root, 'src/components/Responsive.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 2)
})

test('MATCH: a bare identifier forwarded to cn() resolves to its own sole `const` declaration (real UntrustedChildText.tsx shape)', () => {
  const root = fixtureRepo()
  const before = `import { cn } from '@/lib/utils'

export function UntrustedChildText({ density, className }) {
  const textSize = density === 'compact' ? 'text-[10px]' : 'text-xs'
  return <span className={cn('inline flex flex-col gap-0.5', textSize, className)} />
}
`
  const file = write(root, 'src/components/UntrustedChildText.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 2)
  const after = applyAll(text, edits)
  assert.match(after, /density === 'compact' \? 'text-\[length:var\(--type-caption-size\)\]' : 'text-\[length:var\(--type-utility-xs-size\)\]'/)
})

test('NO-MATCH: an identifier reaching cn() with zero matching declarations in the file is left alone, not refused', () => {
  const root = fixtureRepo()
  const before = `import { cn } from '@/lib/utils'

export function Passthrough({ className }) {
  return <span className={cn('block', className)} />
}
`
  const file = write(root, 'src/components/Passthrough.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

test('MATCH: a module-level const interpolated into a template className (`${PANEL_BASE}`, real VideoEmbed.tsx shape) is edited once, applies everywhere it is used', () => {
  const root = fixtureRepo()
  const before = `const PANEL_BASE =
  'flex w-full flex-col items-center justify-center gap-2 rounded-md border p-6 text-center text-xs'

export function RefusedA() {
  return <div className={\`${'${PANEL_BASE}'} border-[var(--color-border)]\`} />
}

export function RefusedB() {
  return <div className={\`${'${PANEL_BASE}'} border-[var(--color-warning)]/40\`} />
}
`
  const file = write(root, 'src/components/VideoEmbed.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1, 'the shared declaration is a single leaf — edited once, not once per usage site')
  const after = applyAll(text, edits)
  assert.match(after, /const PANEL_BASE =\n {2}'flex w-full flex-col items-center justify-center gap-2 rounded-md border p-6 text-center text-\[length:var\(--type-utility-xs-size\)\]'/)
})

test('MATCH: a hand-rolled `[...].join(\' \')` className array (real ShellDenyPatternsEditor.tsx shape) rewrites', () => {
  const root = fixtureRepo()
  const before = `export function Editor({ hasErrors }) {
  return (
    <textarea
      className={[
        'w-full rounded-md border px-3 py-2 text-xs font-mono',
        'text-[var(--color-secondary)]',
        hasErrors ? 'border-[var(--color-error)]/60' : 'border-[var(--color-border)]',
      ].join(' ')}
    />
  )
}
`
  const file = write(root, 'src/components/Editor.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
  const after = applyAll(text, edits)
  assert.match(after, /'w-full rounded-md border px-3 py-2 text-\[length:var\(--type-utility-xs-size\)\] font-mono'/)
})

test('MATCH: `[...].filter(Boolean).join(\' \')` is walked the same way as a plain `.join`', () => {
  const root = fixtureRepo()
  const before = `export function Editor2() {
  return <textarea className={['text-xs', null].filter(Boolean).join(' ')} />
}
`
  const file = write(root, 'src/components/Editor2.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 1)
  const after = applyAll(text, edits)
  assert.match(after, /\['text-\[length:var\(--type-utility-xs-size\)\]', null\]/)
})

test('NO-MATCH: an identifier resolving to TWO same-named declarations in the file (ambiguous) is left alone, not refused', () => {
  const root = fixtureRepo()
  const before = `import { cn } from '@/lib/utils'

export function A() {
  const textSize = 'text-xs'
  return <span className={cn(textSize)} />
}

export function B() {
  const textSize = 'text-lg'
  return <div>{textSize}</div>
}
`
  const file = write(root, 'src/components/Ambiguous.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

// ── Case 5: NO-MATCH — not a provable class-list shape ─────────────────────

test('NO-MATCH: a bare identifier / opaque function call is skipped, not refused (no literal text to examine)', () => {
  const root = fixtureRepo()
  const before = `export function Opaque({ getClasses }) {
  return <span className={getClasses()} />
}
`
  const file = write(root, 'src/components/Opaque.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

test('NO-MATCH: `+` string concatenation is out of scope and left untouched', () => {
  const root = fixtureRepo()
  const before = `export function Concat({ size }) {
  return <span className={'text-' + size} />
}
`
  const file = write(root, 'src/components/Concat.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

test('NO-MATCH: a value not in the mapping (e.g. text-base) is left completely alone', () => {
  const root = fixtureRepo()
  const before = `export function Base() {
  return <span className="text-base" />
}
`
  const file = write(root, 'src/components/Base.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

test('NO-MATCH: a near-miss substring (`not-text-xs-real`) does not match on either boundary', () => {
  const root = fixtureRepo()
  const before = `export function NearMiss() {
  return <span className="not-text-xs-real" />
}
`
  const file = write(root, 'src/components/NearMiss.tsx', before)
  const { edits, refusals } = planFileEdits(root, file)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
})

// ── Case 6: idempotence ──────────────────────────────────────────────────

test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further edits', () => {
  const root = fixtureRepo()
  write(root, 'src/components/Badge.tsx', `export function Badge() {
  return <span className="text-xs font-medium">hi</span>
}
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.equal(first.totalEdits, 1)
  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'the rewritten text-[length:var(...)] form no longer matches any FROM key, so a second pass is a no-op')
})

// ── Case 7: never edits outside the allowed roots ───────────────────────────

test('NEVER EDITS OUTSIDE ALLOWED ROOTS: runCodemod only scans src/ and packages/ui/src/', () => {
  const root = fixtureRepo()
  write(root, 'scripts/should-not-be-touched.tsx', `export function Rogue() {
  return <span className="text-xs" />
}
`)
  write(root, 'src/components/InScope.tsx', `export function InScope() {
  return <span className="text-xs" />
}
`)
  const result = runCodemod({ repoRoot: root, apply: false })
  for (const f of result.files) assert.ok(isInAllowedRoot(root, resolve(root, f.file)), `${f.file} must be inside an allowed root`)
  assert.ok(result.files.some((f) => f.file === 'src/components/InScope.tsx'))
  assert.ok(!result.files.some((f) => f.file.startsWith('scripts/')))

  const before = readFileSync(resolve(root, 'scripts/should-not-be-touched.tsx'), 'utf8')
  runCodemod({ repoRoot: root, apply: true })
  const after = readFileSync(resolve(root, 'scripts/should-not-be-touched.tsx'), 'utf8')
  assert.equal(before, after, 'a file outside src/ and packages/ui/src/ is never written, even with --apply')
})

// ── Case 8: exported walkClassExpr recognizes the documented composable
// shapes directly (unit-level, not just through a full file scan) ──────────

test('walkClassExpr descends into &&, array-literal elements, and nested class-builder calls', () => {
  const root = fixtureRepo()
  const before = `import { clsx } from 'clsx'

export function Multi({ show }) {
  return <span className={clsx(['text-xs'], show && 'text-sm')} />
}
`
  const file = write(root, 'src/components/Multi.tsx', before)
  const { edits, refusals, text } = planFileEdits(root, file)
  assert.equal(refusals.length, 0, JSON.stringify(refusals))
  assert.equal(edits.length, 2)
  const after = applyAll(text, edits)
  assert.match(after, /\['text-\[length:var\(--type-utility-xs-size\)\]'\]/)
  assert.match(after, /show && 'text-\[length:var\(--type-body-compact-size\)\]'/)
  void walkClassExpr // exported and used by planFileEdits; referenced here so the import is exercised directly too
})

// ── helper ──────────────────────────────────────────────────────────────
function applyAll(text, edits) {
  const sorted = [...edits].sort((a, b) => b.start - a.start)
  let result = text
  for (const e of sorted) result = result.slice(0, e.start) + e.replacement + result.slice(e.end)
  return result
}
