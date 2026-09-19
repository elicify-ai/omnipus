import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { clsx as clsxImport } from 'clsx'
import { planFileEdits, runCodemod } from './codemod-classname-passthrough.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-classname-passthrough-'))
  fixtureRoots.push(root)
  return root
}

function write(root, relPath, content) {
  const abs = resolve(root, relPath)
  mkdirSync(dirname(abs), { recursive: true })
  writeFileSync(abs, content, 'utf8')
  return abs
}

// ── Transform A: TEMPLATE-JOIN ──────────────────────────────────────────

test('MATCH (template-join): ternary-guarded className span (ChatImage.tsx shape) — emits clsx, not cn', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/Widget.tsx', `
export function Widget({ className }: { className?: string }) {
  return <div className={\`relative inline-block\${className ? \` \${className}\` : ''}\`} />
}
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 2) // rewrite + clsx import
  assert.match(text, /import \{ clsx \} from 'clsx'/)
  assert.match(text, /className=\{clsx\("relative inline-block", className\)\}/)
  assert.doesNotMatch(text, /\bcn\(/, 'must not introduce a twMerge-backed cn() — see lead review point 1')
})

test('MATCH (template-join): bare identifier span + missing clsx import gets one added, no stray whitespace argument', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/Toolbar.tsx', `
export function Toolbar({ className = '' }: { className?: string }) {
  const base = 'flex items-center gap-1'
  return <div className={\`\${base} \${className}\`} />
}
`)
  const { edits, after: text } = planFileEdits(root, path)
  assert.equal(edits.filter((e) => e.transform === 'template-join').length, 2) // rewrite + import
  assert.match(text, /import \{ clsx \} from 'clsx'/)
  assert.match(text, /className=\{clsx\(`\$\{base\}`, className\)\}/)
  assert.doesNotMatch(text, /" "/, 'the inter-span whitespace-only literal must be dropped, not emitted as its own argument')
})

test('MATCH (template-join): `?? \'\'`-guarded span + `.trim()` wrapper (icon-button.tsx shape) — clsx, `.trim()` DROPPED (breaks builder recognition if kept, see codemod header)', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/icon-button.tsx', `
import * as React from 'react'
const sizes = { sm: 'h-8 w-8', default: 'h-9 w-9' } as const
export function IconButton({ size = 'default', className }: { size?: 'sm' | 'default', className?: string }) {
  return <button className={\`\${sizes[size]} \${className ?? ''}\`.trim()} />
}
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.ok(edits.some((e) => e.transform === 'template-join'))
  // Keeping `.trim()` on the OUTSIDE (`clsx(...).trim()`) was tried and
  // reverted: spacing.mjs/ts-colors.mjs's builder-recognition gate only
  // matches a BARE identifier callee (`clsx(...)`), not `(...).trim()` — the
  // whole expression falls back to unsupported the moment `.trim()` wraps
  // it, defeating the transform. Dropping it is provably byte-identical for
  // undefined/''/one class/several classes and DOM/classList-equivalent
  // (whitespace-run-insensitive) for the one bounded exception — a
  // className carrying ITS OWN leading/trailing whitespace — proven in
  // dist/design-system-baseline/cli-lanes/fanout/L6/equality-proof.log.
  assert.match(text, /className=\{clsx\(`\$\{sizes\[size\]\}`, className\)\}/)
  assert.doesNotMatch(text, /" "/)
})

test('MATCH (template-join): a bare conditional "other" span is passed through UNWRAPPED (AutoSaveIndicator.tsx shape) — no needless template shell', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/AutoSaveIndicator.tsx', `
export function AutoSaveIndicator({ status, className = '' }: { status: string, className?: string }) {
  return <span className={\`inline-flex items-center gap-1 \${
    status === 'saved' ? 'opacity-60' : 'opacity-0'
  } \${className}\`} />
}
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.ok(edits.some((e) => e.transform === 'template-join'))
  // the ternary appears bare — never wrapped in its own `${...}` template shell
  assert.match(text, /clsx\("inline-flex items-center gap-1", status === 'saved' \? 'opacity-60' : 'opacity-0', className\)/)
})

test('MATCH (template-join): a site already wrapped in cn()/twMerge keeps cn — never downgraded to clsx', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/AlreadyMerged.tsx', `
import { cn } from '@/lib/utils'
export function AlreadyMerged({ className }: { className?: string }) {
  return <div className={cn('extra-base', \`relative inline-block\${className ? \` \${className}\` : ''}\`)} />
}
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1) // no NEW import needed — cn already imported
  assert.match(text, /className=\{cn\('extra-base', cn\("relative inline-block", className\)\)\}/)
})

test('NO-MATCH (template-join): a template with no className-shaped span is left untouched', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/Plain.tsx', `
export function Plain({ label }: { label: string }) {
  return <div className={\`fixed \${label}\`} />
}
`)
  const before = readFileSync(path, 'utf8')
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
  assert.equal(text, before)
})

test('REFUSED (template-join): two className-shaped spans in one template is ambiguous', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/Dup.tsx', `
export function Dup({ className }: { className?: string }) {
  return <div className={\`\${className} \${className}\`} />
}
`)
  const { edits, refusals } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /multiple.*ambiguous/)
})

test('REFUSED (template-join): className is reassigned within its owner', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/Reassign.tsx', `
export function Reassign({ className }: { className?: string }) {
  if (!className) className = 'fallback'
  return <div className={\`base \${className}\`} />
}
`)
  const { edits, refusals } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /reassigned/)
})

test('REFUSED (template-join): anonymous owner (not bound to any name) is refused', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/Anon.tsx', `
export default function ({ className }: { className?: string }) {
  return <div className={\`base \${className}\`} />
}
`)
  const { edits, refusals } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /anonymous/)
})

test('REFUSED (template-join): a differently-named class-like prop (not literal `className`) never matches', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/Sheet.tsx', `
export function Sheet({ widthClass }: { widthClass?: string }) {
  return <div className={\`base \${widthClass}\`} />
}
`)
  const before = readFileSync(path, 'utf8')
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0) // not even recognized as a candidate — `widthClass` never classifies as the target span
  assert.equal(text, before)
})

// ── Transform B: CVA-ARG-SPLIT ──────────────────────────────────────────

test('MATCH (cva-arg-split): className folded into a cva()-produced call inside cn() (button.tsx shape)', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/button.tsx', `
import { cva } from 'class-variance-authority'
import { cn } from '@/lib/utils'
const buttonVariants = cva('base', { variants: { size: { default: 'h-9' } } })
const Button = ({ className, size }: { className?: string, size?: string }) => (
  <button className={cn(buttonVariants({ size, className }))} />
)
export { Button }
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.equal(edits.length, 1)
  assert.match(text, /cn\(buttonVariants\(\{ size \}\), className\)/)
})

test('NO-MATCH (cva-arg-split): cn() wrapping a non-cva call is untouched', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/Other.tsx', `
import { cn } from '@/lib/utils'
function otherVariants(props) { return 'x' }
const Widget = ({ className }: { className?: string }) => (
  <div className={cn(otherVariants({ className }))} />
)
`)
  const before = readFileSync(path, 'utf8')
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
  assert.equal(text, before)
})

test('REFUSED (cva-arg-split): className property is not a bare forward', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/Weird.tsx', `
import { cva } from 'class-variance-authority'
import { cn } from '@/lib/utils'
const v = cva('base')
const Widget = ({ className }: { className?: string }) => (
  <div className={cn(v({ className: className + ' extra' }))} />
)
`)
  const { edits, refusals } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /not a bare/)
})

// ── Transform C: HOIST-OUT-OF-MAP ───────────────────────────────────────

test('MATCH (hoist-out-of-map): two classes() calls hoisted out of a .map() callback (ChipListInput.tsx shape)', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/workspaces/ChipListInput.tsx', `
function classes(...parts: (string | undefined)[]): string { return parts.filter(Boolean).join(' ') }
const CHIP_BASE = 'inline-flex items-center'
export function ChipListInput({ values, chipClassName, chipRemoveClassName }: { values: string[], chipClassName: string, chipRemoveClassName?: string }) {
  return (
    <div>
      {values.map((value, index) => (
        <span key={index} className={classes(CHIP_BASE, chipClassName)}>
          <button className={classes('shrink-0', chipRemoveClassName)} />
        </span>
      ))}
    </div>
  )
}
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.ok(edits.length >= 3)
  // lead review 2026-09-19: named after the forwarded class-like identifier
  // (`resolvedChipClassName`), not a generated `__hoisted1ClassName` counter.
  assert.match(text, /const resolvedChipClassName = classes\(CHIP_BASE, chipClassName\)/)
  assert.match(text, /const resolvedChipRemoveClassName = classes\('shrink-0', chipRemoveClassName\)/)
  assert.match(text, /className=\{resolvedChipClassName\}/)
  assert.match(text, /className=\{resolvedChipRemoveClassName\}/)
  // hoisted BEFORE the .map(, not inside its callback
  assert.ok(text.indexOf('resolvedChipClassName =') < text.indexOf('values.map'))
})

test('MATCH (hoist-out-of-map): no class-like argument falls back to `resolvedClassName`', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/workspaces/NoClassLikeArg.tsx', `
function classes(...parts: (string | undefined)[]): string { return parts.filter(Boolean).join(' ') }
export function NoClassLikeArg({ values, extra }: { values: string[], extra: string }) {
  return (
    <div>
      {values.map((value, index) => (
        <span key={index} className={classes('base', extra)} />
      ))}
    </div>
  )
}
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.ok(edits.length > 0)
  assert.match(text, /const resolvedClassName = classes\('base', extra\)/)
  assert.match(text, /className=\{resolvedClassName\}/)
})

test('MATCH (hoist-out-of-map): the derived name gets a numeric suffix only on an actual collision with an existing identifier', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/workspaces/NameCollision.tsx', `
function classes(...parts: (string | undefined)[]): string { return parts.filter(Boolean).join(' ') }
export function NameCollision({ values, chipClassName }: { values: string[], chipClassName: string }) {
  const resolvedChipClassName = 'already taken by unrelated code'
  console.log(resolvedChipClassName)
  return (
    <div>
      {values.map((value, index) => (
        <span key={index} className={classes('base', chipClassName)} />
      ))}
    </div>
  )
}
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.ok(edits.length > 0)
  // the pre-existing `resolvedChipClassName` local is left completely alone
  assert.match(text, /const resolvedChipClassName = 'already taken by unrelated code'/)
  // the HOISTED one gets bumped to the first free numeric suffix
  assert.match(text, /const resolvedChipClassName2 = classes\('base', chipClassName\)/)
  assert.match(text, /className=\{resolvedChipClassName2\}/)
})

test('REFUSED (hoist-out-of-map): a call that depends on the map callback\'s own parameter cannot be hoisted', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/workspaces/PerItem.tsx', `
function classes(...parts: (string | undefined)[]): string { return parts.filter(Boolean).join(' ') }
export function PerItem({ values, chipClassName }: { values: string[], chipClassName: string }) {
  return (
    <div>
      {values.map((value, index) => (
        <span key={index} className={classes(chipClassName, value)} />
      ))}
    </div>
  )
}
`)
  const { edits, refusals } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 1)
  assert.match(refusals[0].reason, /own parameter/)
})

test('NO-MATCH (hoist-out-of-map): className call OUTSIDE any .map() callback is untouched', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/workspaces/NotInMap.tsx', `
function classes(...parts: (string | undefined)[]): string { return parts.filter(Boolean).join(' ') }
export function NotInMap({ chipClassName }: { chipClassName: string }) {
  return <span className={classes('base', chipClassName)} />
}
`)
  const before = readFileSync(path, 'utf8')
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(edits.length, 0)
  assert.equal(refusals.length, 0)
  assert.equal(text, before)
})

// ── Transform D: PARAM-DESTRUCTURE-HOIST ────────────────────────────────

test('MATCH (param-destructure-hoist): forwardRef body destructuring moves into the parameter list, unblocking template-join (icon-button.tsx shape)', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/Wrapped.tsx', `
import * as React from 'react'
const sizes = { sm: 'h-8', default: 'h-9' } as const
const Wrapped = React.forwardRef((props, ref) => {
  const { size = 'default', className } = props
  const accessibleName = 'x'
  if (accessibleName.trim().length === 0) throw new Error('boom')
  return <button ref={ref} className={\`\${sizes[size]} \${className ?? ''}\`} />
})
`)
  const { edits, refusals, after: text } = planFileEdits(root, path)
  assert.equal(refusals.length, 0)
  assert.ok(edits.some((e) => e.transform === 'param-destructure-hoist'))
  assert.ok(edits.some((e) => e.transform === 'template-join'))
  assert.match(text, /React\.forwardRef\(\(\{ size = 'default', className \}, ref\) => \{/)
  assert.doesNotMatch(text, /const \{ size = 'default', className \} = props/)
  assert.match(text, /className=\{clsx\(`\$\{sizes\[size\]\}`, className\)\}/)
  assert.doesNotMatch(text, /" "/)
  assert.doesNotMatch(text, /\bcn\(/)
  // lead review point 2: the destructuring line's removal must not leave its
  // own indentation dangling ahead of the NEXT statement's already-correct
  // indentation (that bug doubled "  const accessibleName" to
  // "    const accessibleName" in the earlier icon-button.tsx dry run).
  assert.match(text, /\{\n {2}const accessibleName = 'x'\n {2}if \(accessibleName/, 'the statement AFTER the removed destructuring must keep its original single-level indentation, not double up')
})

test('REFUSED (param-destructure-hoist): `props` referenced elsewhere in the body is not safe to remove', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/StillUsesProps.tsx', `
import * as React from 'react'
const Wrapped = React.forwardRef((props, ref) => {
  const { className } = props
  console.log(props)
  return <button ref={ref} className={\`base \${className}\`} />
})
`)
  const { edits, refusals } = planFileEdits(root, path)
  assert.equal(edits.filter((e) => e.transform === 'param-destructure-hoist').length, 0)
  assert.ok(refusals.some((r) => r.transform === 'param-destructure-hoist' && /referenced 2 times/.test(r.reason)))
})

// ── Idempotency, scope, and coverage of the codemod driver ─────────────

test('IDEMPOTENT: a second --apply over an already-fixed tree makes zero further changes', () => {
  const root = fixtureRepo()
  write(root, 'src/components/chat/ChatImage.tsx', `
import { cn } from '@/lib/utils'
export function ChatImage({ className }: { className?: string }) {
  return <div className={\`relative inline-block\${className ? \` \${className}\` : ''}\`} />
}
`)
  write(root, 'src/components/workspaces/ChipListInput.tsx', `
function classes(...parts: (string | undefined)[]): string { return parts.filter(Boolean).join(' ') }
export function ChipListInput({ values, chipClassName }: { values: string[], chipClassName: string }) {
  return <div>{values.map((v, i) => <span key={i} className={classes('base', chipClassName)} />)}</div>
}
`)
  const first = runCodemod({ repoRoot: root, apply: true })
  assert.ok(first.totalEdits > 0)
  const second = runCodemod({ repoRoot: root, apply: true })
  assert.equal(second.totalEdits, 0, 'idempotent: nothing left to rewrite')
  assert.equal(second.totalRefusals, 0)
})

test('never edits outside src/ or packages/ui/src/', () => {
  const root = fixtureRepo()
  write(root, 'scripts/other/Outside.tsx', `
export function Outside({ className }: { className?: string }) {
  return <div className={\`base \${className}\`} />
}
`)
  const result = runCodemod({ repoRoot: root, apply: true })
  assert.equal(result.totalFilesTouched, 0)
  const untouched = readFileSync(resolve(root, 'scripts/other/Outside.tsx'), 'utf8')
  assert.match(untouched, /\$\{className\}/)
})

test('dry-run by default: runCodemod({ apply: false }) writes nothing to disk', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/DryRun.tsx', `
export function DryRun({ className }: { className?: string }) {
  return <div className={\`base \${className}\`} />
}
`)
  const before = readFileSync(path, 'utf8')
  const result = runCodemod({ repoRoot: root, apply: false })
  assert.ok(result.totalEdits > 0)
  assert.equal(readFileSync(path, 'utf8'), before, 'dry-run must not write')
})

test('caveat surfaces for the cva-arg-split residual opaque call', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/button.tsx', `
import { cva } from 'class-variance-authority'
import { cn } from '@/lib/utils'
const buttonVariants = cva('base', { variants: { size: { default: 'h-9' } } })
const Button = ({ className, size }: { className?: string, size?: string }) => (
  <button className={cn(buttonVariants({ size, className }))} />
)
`)
  const { caveats } = planFileEdits(root, path)
  assert.equal(caveats.length, 1)
  assert.match(caveats[0].note, /P7/)
})

// ── Equality: the emitted expression evaluates to the same string as the
// original, for the className values lead review asked for explicitly —
// undefined, '', one class, several classes, leading/trailing whitespace.
// Full multi-site sweep with the REAL clsx/cn packages:
// dist/design-system-baseline/cli-lanes/fanout/L6/equality-proof.log. This
// test additionally proves it by EVALUATING the codemod's own emitted text
// (not a hand re-derivation), so it fails if the emitted shape ever changes. ─

test('EQUALITY: clsx(...) output matches the original .trim()-wrapped template, byte-for-byte for 4/5 values, DOM/classList-equivalent for the 5th', () => {
  const root = fixtureRepo()
  const path = write(root, 'src/components/ui/icon-button.tsx', `
import * as React from 'react'
const sizes = { sm: 'h-8 w-8', default: 'h-9 w-9' } as const
export function IconButton({ size = 'default', className }: { size?: 'sm' | 'default', className?: string }) {
  return <button className={\`\${sizes[size]} \${className ?? ''}\`.trim()} />
}
`)
  const { after: rewrittenSource } = planFileEdits(root, path)

  function original({ className } = {}) {
    const sizes = { sm: 'h-8 w-8', default: 'h-9 w-9' }
    return `${sizes.default} ${className ?? ''}`.trim()
  }
  // Locate the ACTUAL rewritten expression by evaluating the codemod's own
  // output text — located by balanced-paren scan (a regex with `[^}]*`
  // breaks on the `}` that closes `${sizes[size]}`'s own interpolation).
  const startIdx = rewrittenSource.indexOf('clsx(')
  assert.notEqual(startIdx, -1, 'expected a clsx(...) call in the rewritten output')
  let depth = 0
  let endIdx = -1
  for (let i = startIdx + 'clsx'.length; i < rewrittenSource.length; i++) {
    if (rewrittenSource[i] === '(') depth++
    else if (rewrittenSource[i] === ')') { depth--; if (depth === 0) { endIdx = i; break } }
  }
  assert.notEqual(endIdx, -1, 'unbalanced clsx(...) call')
  assert.notEqual(rewrittenSource.slice(endIdx + 1, endIdx + 8), '.trim()', '.trim() must NOT wrap the clsx(...) call — that breaks scanner builder-recognition, see codemod header')
  const rewrittenExprSource = rewrittenSource.slice(startIdx, endIdx + 1)

  const tokenSequence = (s) => s.trim().split(/\s+/).filter(Boolean).join(' ')
  const byteIdenticalValues = [undefined, '', 'foo', 'foo bar baz']
  for (const className of byteIdenticalValues) {
    const sizes = { sm: 'h-8 w-8', default: 'h-9 w-9' }
    const rewritten = new Function('clsx', 'sizes', 'size', 'className', `return ${rewrittenExprSource}`)(clsxImport, sizes, 'default', className)
    assert.equal(rewritten, original({ className }), `expected byte-identical for className=${JSON.stringify(className)}`)
  }
  // The one bounded, documented exception: className itself carries its own
  // leading/trailing whitespace. Not byte-identical (dropping the outer
  // `.trim()` no longer strips it), but DOM classList / testing-library's
  // toHaveClass both split on \s+ and drop empty tokens, so the SET of
  // classes actually applied is identical — proven here as token-sequence
  // equality, the same normalization those two consumers perform.
  const sizes = { sm: 'h-8 w-8', default: 'h-9 w-9' }
  const paddedClassName = '  foo bar  '
  const rewrittenPadded = new Function('clsx', 'sizes', 'size', 'className', `return ${rewrittenExprSource}`)(clsxImport, sizes, 'default', paddedClassName)
  const originalPadded = original({ className: paddedClassName })
  assert.notEqual(rewrittenPadded, originalPadded, 'this IS expected to differ byte-for-byte — see comment above')
  assert.equal(tokenSequence(rewrittenPadded), tokenSequence(originalPadded), 'must still be identical as a DOM classList / toHaveClass token set')
})
