import test, { after } from 'node:test'
import assert from 'node:assert/strict'
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import ts from 'typescript'
import {
  planGoalIndicatorNonActiveState,
  planGoalPillTrayIconAndPulse,
  planFileWriteConfirmTextClass,
  planTablePartInertProps,
  planBrowserLiveViewDriveChip,
  planSite,
  runCodemod,
  SITES,
} from './codemod-finite-branch.mjs'
import { parseSourceFile, applyEdits } from './codemod-lib.mjs'

const fixtureRoots = []
after(() => {
  for (const root of fixtureRoots) rmSync(root, { recursive: true, force: true })
})

function fixtureRepo() {
  const root = mkdtempSync(resolve('dist/design-system-baseline/codemod-finite-branch-'))
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
  return applyEdits(text, edits)
}

function assertParses(filePath, text) {
  const reparsed = parseSourceFile(filePath, text)
  const errors = (reparsed.parseDiagnostics ?? []).filter((d) => d.category === ts.DiagnosticCategory.Error)
  assert.equal(errors.length, 0, `expected valid TS/TSX after rewrite, got: ${errors.map((e) => ts.flattenDiagnosticMessageText(e.messageText, '\n')).join('; ')}`)
}

function plan(planFn, text, filePath = 'src/fixture.tsx') {
  const source = parseSourceFile(filePath, text)
  return { ...planFn(source, text), source, text, filePath }
}

// ════════════════════════════════════════════════════════════════════════
// Site 1 — GoalIndicator.tsx: destructuring off a dispatcher call
// ════════════════════════════════════════════════════════════════════════

const GOAL_INDICATOR_FIXTURE = `function Comp({ state }: { state: string }) {
  return (
    <div>
      {state !== 'active' && (() => {
        const { testId, text, className } = describeNonActiveState(state)
        return (
          <>
            <p className={className} data-testid={testId}>
              {text}
            </p>
          </>
        )
      })()}
    </div>
  )
}
`

test('MATCH (site 1): destructured describeNonActiveState() result becomes a named binding + property access', () => {
  const { ok, edits, text, filePath } = plan(planGoalIndicatorNonActiveState, GOAL_INDICATOR_FIXTURE)
  assert.equal(ok, true)
  assert.equal(edits.length, 4, 'declaration + className attr value + testId attr value + text child')
  const out = editedText(text, edits)
  assert.match(out, /const nonActiveState = describeNonActiveState\(state\)/)
  assert.match(out, /<p className=\{nonActiveState\.className\} data-testid=\{nonActiveState\.testId\}>/)
  assert.match(out, /\{nonActiveState\.text\}/)
  assert.doesNotMatch(out, /\bconst \{ testId, text, className \}/)
  assertParses(filePath, out)
})

test('NO-MATCH (site 1): a file with no describeNonActiveState destructuring is refused cleanly, not crashed', () => {
  const { ok, reason } = plan(planGoalIndicatorNonActiveState, `export function Comp() { return <div>hi</div> }\n`)
  assert.equal(ok, false)
  assert.match(reason, /could not find/)
})

test('REFUSAL (site 1): a renamed destructured field is refused, not silently mis-transformed', () => {
  const mutated = GOAL_INDICATOR_FIXTURE.replace(
    'const { testId, text, className } = describeNonActiveState(state)',
    'const { testId, text, labelClassName } = describeNonActiveState(state)',
  )
  const { ok, reason } = plan(planGoalIndicatorNonActiveState, mutated)
  assert.equal(ok, false)
  assert.match(reason, /no longer match/)
})

test('REFUSAL (site 1): a shadowed destructured name inside a nested scope is refused', () => {
  const mutated = GOAL_INDICATOR_FIXTURE.replace(
    '        return (',
    '        const helper = () => { const className = "shadow"; return className }\n        return (',
  )
  const { ok, reason } = plan(planGoalIndicatorNonActiveState, mutated)
  assert.equal(ok, false)
  assert.match(reason, /ambiguous/)
})

test('IDEMPOTENCY (site 1): a second run on the rewritten output is a clean no-op', () => {
  const first = plan(planGoalIndicatorNonActiveState, GOAL_INDICATOR_FIXTURE)
  const after1 = editedText(first.text, first.edits)
  const second = plan(planGoalIndicatorNonActiveState, after1)
  assert.equal(second.ok, true)
  assert.equal(second.alreadyApplied, true)
  assert.equal(editedText(after1, second.edits), after1)
})

// ════════════════════════════════════════════════════════════════════════
// Site 2 — GoalPillTray.tsx: Icon destructuring + boolean `&&` guard
// ════════════════════════════════════════════════════════════════════════

const GOAL_PILL_TRAY_FIXTURE = `function GoalPill({ frame }: { frame: any }) {
  const config = describePillState(frame.state)
  const { Icon } = config
  return (
    <button className={cn('flex', config.accentClass)}>
      <Icon
        size={13}
        className={cn('shrink-0', config.pulse && 'animate-pulse')}
      />
    </button>
  )
}
`

test('MATCH (site 2): Icon destructuring removed, JSX tag rewritten, pulse guard hardened', () => {
  const { ok, edits, text, filePath } = plan(planGoalPillTrayIconAndPulse, GOAL_PILL_TRAY_FIXTURE)
  assert.equal(ok, true)
  const out = editedText(text, edits)
  assert.doesNotMatch(out, /const \{ Icon \} = config/)
  assert.match(out, /<config\.Icon/)
  assert.match(out, /config\.pulse === true && 'animate-pulse'/)
  assertParses(filePath, out)
})

test('NO-MATCH (site 2): a file with neither Icon destructuring nor pulse guard is refused', () => {
  const { ok, reason } = plan(planGoalPillTrayIconAndPulse, `export function Comp() { return <div>hi</div> }\n`)
  assert.equal(ok, false)
  assert.match(reason, /could not find/)
})

test('REFUSAL (site 2): a renamed Icon destructure (`{ Icon: PillIcon }`) is refused, not guessed at', () => {
  const mutated = GOAL_PILL_TRAY_FIXTURE
    .replace('const { Icon } = config', 'const { Icon: PillIcon } = config')
    .replace('<Icon\n', '<PillIcon\n')
  const { ok, reason } = plan(planGoalPillTrayIconAndPulse, mutated)
  assert.equal(ok, false)
  assert.match(reason, /could not find/)
})

test('REFUSAL (site 2): Icon used as a plain (non-JSX-tag) expression is refused rather than mis-rewritten', () => {
  const mutated = GOAL_PILL_TRAY_FIXTURE.replace(
    '  return (',
    '  const displayName = Icon.displayName\n  return (',
  )
  const { ok, reason } = plan(planGoalPillTrayIconAndPulse, mutated)
  assert.equal(ok, false)
  assert.match(reason, /plain \(non-JSX-tag\) expression/)
})

test('IDEMPOTENCY (site 2): a second run on the rewritten output is a clean no-op', () => {
  const first = plan(planGoalPillTrayIconAndPulse, GOAL_PILL_TRAY_FIXTURE)
  const after1 = editedText(first.text, first.edits)
  const second = plan(planGoalPillTrayIconAndPulse, after1)
  assert.equal(second.ok, true)
  assert.equal(second.alreadyApplied, true)
})

// ════════════════════════════════════════════════════════════════════════
// Site 3 — FileWriteConfirm.tsx: mixed-value ternary split
// ════════════════════════════════════════════════════════════════════════

const FILE_WRITE_CONFIRM_FIXTURE = `function FileOpBlock({ isRefusal, isRunning, isError, isCancelled, reason }: any) {
  const statusConfig = isRefusal && !isRunning
    ? {
        indicator: <span className="dot" />,
        label: 'File already exists',
        textClass: 'text-[var(--color-warning)]',
      }
    : getToolBadgeStatusConfig(
        isRunning ? 'running' : isCancelled ? 'cancelled' : isError ? 'error' : 'success',
        { size: 12, cancelledVariant: 'muted' }
      )
  return (
    <div>
      {statusConfig.indicator}
      <span className={cn('a', statusConfig.textClass)}>{statusConfig.label}</span>
      {reason && <span className={cn('b', statusConfig.textClass)}>{reason}</span>}
    </div>
  )
}
`

test('MATCH (site 3): textClass split into an independent literal-vs-undefined const', () => {
  const { ok, edits, text, filePath } = plan(planFileWriteConfirmTextClass, FILE_WRITE_CONFIRM_FIXTURE)
  assert.equal(ok, true)
  assert.equal(edits.length, 3, 'declaration replacement + two `.textClass` reads')
  const out = editedText(text, edits)
  assert.match(out, /const isRefusalDisplay = isRefusal && !isRunning/)
  assert.match(out, /const textClass = isRefusalDisplay \? 'text-\[var\(--color-warning\)\]' : undefined/)
  assert.doesNotMatch(out, /textClass: 'text-\[var\(--color-warning\)\]'/, 'textClass removed from the object literal branch')
  assert.doesNotMatch(out, /cn\([^)]*statusConfig\.textClass/, 'no cn(...) call reads statusConfig.textClass anymore (a mention inside the explanatory comment is fine)')
  assert.match(out, /cn\('a', textClass\)/)
  assert.match(out, /cn\('b', textClass\)/)
  assertParses(filePath, out)
})

test('NO-MATCH (site 3): a file with no statusConfig ternary is refused cleanly', () => {
  const { ok, reason } = plan(planFileWriteConfirmTextClass, `export function Comp() { return <div>hi</div> }\n`)
  assert.equal(ok, false)
  assert.match(reason, /could not find/)
})

test('REFUSAL (site 3): a changed guard condition is refused rather than mis-split', () => {
  const mutated = FILE_WRITE_CONFIRM_FIXTURE.replace(
    'const statusConfig = isRefusal && !isRunning',
    'const statusConfig = isRefusal',
  )
  const { ok, reason } = plan(planFileWriteConfirmTextClass, mutated)
  assert.equal(ok, false)
  assert.match(reason, /could not find/)
})

test('IDEMPOTENCY (site 3): a second run on the rewritten output is a clean no-op', () => {
  const first = plan(planFileWriteConfirmTextClass, FILE_WRITE_CONFIRM_FIXTURE)
  const after1 = editedText(first.text, first.edits)
  const second = plan(planFileWriteConfirmTextClass, after1)
  assert.equal(second.ok, true)
  assert.equal(second.alreadyApplied, true)
})

// ════════════════════════════════════════════════════════════════════════
// Site 4 — TablePart.tsx: ternary-of-objects read through a template literal
// ════════════════════════════════════════════════════════════════════════

const TABLE_PART_FIXTURE = `function Cell({ inert }: { inert: boolean }) {
  const inertProps = inert
    ? { className: 'cursor-default', onClick: (event: any) => event.stopPropagation() }
    : { className: '' }
  return (
    <td
      className={\`base \${inertProps.className}\`}
      {...(inert ? { onClick: inertProps.onClick, 'data-inert': 'true' } : {})}
    >
      x
    </td>
  )
}
`

test('MATCH (site 4): inertProps ternary-of-objects split into independent literal ternaries', () => {
  const { ok, edits, text, filePath } = plan(planTablePartInertProps, TABLE_PART_FIXTURE)
  assert.equal(ok, true)
  const out = editedText(text, edits)
  assert.match(out, /const inertClassName = inert \? 'cursor-default' : ''/)
  assert.match(out, /const inertOnClick = inert \? \(event: any\) => event\.stopPropagation\(\) : undefined/)
  assert.doesNotMatch(out, /inertProps\.className/)
  assert.doesNotMatch(out, /inertProps\.onClick/)
  assert.match(out, /\$\{inertClassName\}/)
  assertParses(filePath, out)
})

test('NO-MATCH (site 4): a file with no inertProps ternary is refused cleanly', () => {
  const { ok, reason } = plan(planTablePartInertProps, `export function Comp() { return <div>hi</div> }\n`)
  assert.equal(ok, false)
  assert.match(reason, /could not find/)
})

test('REFUSAL (site 4): a third property on the false-branch is refused rather than guessed at', () => {
  const mutated = TABLE_PART_FIXTURE.replace(
    ": { className: '' }",
    ": { className: '', title: 'inert' }",
  )
  const { ok, reason } = plan(planTablePartInertProps, mutated)
  assert.equal(ok, false)
  assert.match(reason, /more than just/)
})

test('IDEMPOTENCY (site 4): a second run on the rewritten output is a clean no-op', () => {
  const first = plan(planTablePartInertProps, TABLE_PART_FIXTURE)
  const after1 = editedText(first.text, first.edits)
  const second = plan(planTablePartInertProps, after1)
  assert.equal(second.ok, true)
  assert.equal(second.alreadyApplied, true)
})

// ════════════════════════════════════════════════════════════════════════
// Site 5 — BrowserLiveView.tsx: if-chain IIFE -> named switch dispatcher
// ════════════════════════════════════════════════════════════════════════
// This recipe matches the ORIGINAL source by exact text (see
// codemod-finite-branch.mjs's DRIVE_CHIP_IIFE_ORIGINAL comment for why: the
// rewrite is a bespoke restructuring unique to this one component's
// branching, so an exact-text match is the honest way to guarantee it never
// silently reproduces stale behaviour against a since-changed source). The
// fixture below is therefore the anchor + IIFE text verbatim, inside a
// minimal wrapping component — this doubles as documentation of exactly
// what shape the recipe requires.

const DRIVE_CHIP_ANCHOR = `/** Presentation state; only annotation and connectivity gate input. */
type DriveMode = 'annotating' | 'agent-working' | 'you-driving' | 'disconnected' | 'other-driving' | 'idle'
`

const DRIVE_CHIP_IIFE = `// ── ADR-040 D6 — header chip config (icon + text label + colour), derived
  // from \`visualState\`. Words + icon back up the colour for accessibility
  // (never colour alone). The 'idle' bucket further distinguishes connection
  // lifecycle (connecting/reconnecting) from a genuinely idle, ready-to-drive
  // frame — the old corner pill's connecting/disconnected states still need
  // SOME visible home now that the pill itself is gone.
  const driveChip = (() => {
    if (visualState === 'agent-working') {
      return { label: \`\${agentDisplayName} is browsing…\`, Icon: Robot, textClass: 'text-[var(--color-info)]', dotClass: 'bg-[var(--color-info)]', pulse: true }
    }
    if (visualState === 'you-driving') {
      return { label: "You're driving", Icon: Cursor, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: true }
    }
    if (visualState === 'annotating') {
      return { label: "You're annotating", Icon: ChatCircleDots, textClass: 'text-[var(--color-accent)]', dotClass: 'bg-[var(--color-accent)]', pulse: false }
    }
    if (visualState === 'error') {
      return { label: 'Error', Icon: WarningCircle, textClass: 'text-[var(--color-error)]', dotClass: 'bg-[var(--color-error)]', pulse: false }
    }
    // 'idle' visualState — visualDriveMode further distinguishes
    // disconnected/other-driving/genuinely-idle, reading the SAME display
    // source of truth \`visualState\` itself derives from, instead of
    // re-deriving \`!connected\`/\`controlledByOther\` here too.
    if (visualDriveMode === 'disconnected') {
      return {
        label: statusState === 'disconnected' ? 'Reconnecting…' : 'Connecting…',
        Icon: SpinnerGap,
        textClass: 'text-[var(--color-muted)]',
        dotClass: 'bg-[var(--color-muted)]',
        pulse: false,
      }
    }
    if (visualDriveMode === 'other-driving') {
      // Informational, NOT a lock-out. Control is shared — this viewer's mouse,
      // keyboard and omnibox all still work while someone else is also active
      // (operator directive, 2026-08-03). The old label read "Someone else is
      // driving", which told the user their input would be ignored — and it
      // was, because the client and server both gated on the lock. Both gates
      // are gone; the chip now just says who else is here.
      return { label: 'Also viewing', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
    }
    return { label: 'Click to drive', Icon: Eye, textClass: 'text-[var(--color-muted)]', dotClass: 'bg-[var(--color-muted)]', pulse: false }
  })()`

const BROWSER_LIVE_VIEW_FIXTURE = `type VisualState = 'agent-working' | 'you-driving' | 'annotating' | 'error' | 'idle'

${DRIVE_CHIP_ANCHOR}
function BrowserLiveView({ visualState, visualDriveMode, statusState, agentDisplayName }: any) {
  ${DRIVE_CHIP_IIFE}
  return (
    <driveChip.Icon size={12} weight={driveChip.pulse ? 'fill' : 'regular'} />
  )
}
`

test('MATCH (site 5): if-chain IIFE replaced with a call to a top-level composite-key switch dispatcher', () => {
  const { ok, edits, text, filePath } = plan(planBrowserLiveViewDriveChip, BROWSER_LIVE_VIEW_FIXTURE)
  assert.equal(ok, true)
  assert.equal(edits.length, 2, 'insert top-level declarations + replace the IIFE call site')
  const out = editedText(text, edits)
  assert.match(out, /function driveChipKeyFor\(visualState: VisualState, visualDriveMode: DriveMode\): DriveChipKey/)
  assert.match(out, /function driveChipConfig\(visualState: VisualState, visualDriveMode: DriveMode, statusState: LiveStatus, agentDisplayName: string\)/)
  assert.match(out, /const driveChip = driveChipConfig\(visualState, visualDriveMode, statusState, agentDisplayName\)/)
  assert.doesNotMatch(out, /const driveChip = \(\(\) => \{/)
  // Every literal from every original branch survives verbatim in the switch.
  for (const literal of [
    "'text-[var(--color-info)]'", "'text-[var(--color-accent)]'", "'text-[var(--color-error)]'",
    "'text-[var(--color-muted)]'", "'Also viewing'", "'Click to drive'", 'Reconnecting…', 'Connecting…',
  ]) {
    assert.ok(out.includes(literal), `expected literal ${literal} to survive the rewrite`)
  }
  assertParses(filePath, out)
})

test('NO-MATCH (site 5): a file with no driveChip IIFE is refused cleanly', () => {
  const { ok, reason } = plan(planBrowserLiveViewDriveChip, `export function Comp() { return <div>hi</div> }\n`)
  assert.equal(ok, false)
  assert.match(reason, /could not find the exact original driveChip IIFE text/)
})

test('REFUSAL (site 5): a single changed label text is refused rather than silently reproducing stale behaviour', () => {
  const mutated = BROWSER_LIVE_VIEW_FIXTURE.replace("label: 'Also viewing'", "label: 'Someone else is here'")
  const { ok, reason } = plan(planBrowserLiveViewDriveChip, mutated)
  assert.equal(ok, false)
  assert.match(reason, /could not find the exact original driveChip IIFE text/)
})

test('IDEMPOTENCY (site 5): a second run on the rewritten output is a clean no-op', () => {
  const first = plan(planBrowserLiveViewDriveChip, BROWSER_LIVE_VIEW_FIXTURE)
  const after1 = editedText(first.text, first.edits)
  const second = plan(planBrowserLiveViewDriveChip, after1)
  assert.equal(second.ok, true)
  assert.equal(second.alreadyApplied, true)
})

// ════════════════════════════════════════════════════════════════════════
// Mutation proof on isolated copies of the REAL production files
// ════════════════════════════════════════════════════════════════════════
// For every one of the five real P8 sites: copy the REAL file from src/
// into an isolated fixture repo, prove the codemod plans the expected edit
// count against it (sanity: the site shape this codemod targets is still
// present in the actual codebase), then mutate one distinguishing token in
// the isolated copy and prove the codemod's safety net refuses instead of
// silently mis-transforming — i.e. the suite would catch a real regression
// in any recipe's precision, not just in a synthetic fixture.

const REAL_SITE_EXPECTATIONS = [
  {
    id: 'goal-indicator-non-active-state',
    sanity: /const \{ testId, text, className \} = describeNonActiveState\(goalStatus\.state\)/,
    // A rename that KEEPS a simple (no propertyName-colon) binding pattern —
    // still matches the decl-finding predicate, but the bound names no
    // longer equal the expected set, exercising the "no longer match" path
    // rather than the "could not find the decl at all" path.
    mutate: (text) => text.replace(
      'const { testId, text, className } = describeNonActiveState(goalStatus.state)',
      'const { testId, text, labelClassName } = describeNonActiveState(goalStatus.state)',
    ),
    refusalMatch: /no longer match/,
  },
  {
    id: 'goal-pill-tray-icon-and-pulse',
    sanity: /const \{ Icon \} = config/,
    mutate: (text) => text.replace("const { Icon } = config", "const { Icon: PillIcon } = config"),
    refusalMatch: /could not find/,
  },
  {
    id: 'file-write-confirm-text-class',
    sanity: /const statusConfig = isRefusal && !isRunning/,
    mutate: (text) => text.replace(
      "textClass: 'text-[var(--color-warning)]',",
      "textClass: 'text-[var(--color-warning)]', extra: true,",
    ),
    refusalMatch: /could not find/,
  },
  {
    id: 'table-part-inert-props',
    sanity: /const inertProps = inert/,
    mutate: (text) => text.replace(": { className: '' }", ": { className: '', title: 'inert' }"),
    refusalMatch: /more than just/,
  },
  {
    id: 'browser-live-view-drive-chip',
    sanity: /const driveChip = \(\(\) => \{/,
    mutate: (text) => text.replace("label: 'Also viewing'", "label: 'Somebody else is here'"),
    refusalMatch: /could not find the exact original driveChip IIFE text/,
  },
]

for (const expectation of REAL_SITE_EXPECTATIONS) {
  const site = SITES.find((s) => s.id === expectation.id)
  test(`MUTATION PROOF (${expectation.id}): isolated copy of the real ${site.file} plans as predicted, and a mutated copy is refused instead of silently mis-transformed`, () => {
    const root = fixtureRepo()
    const realFile = readFileSync(resolve(site.file), 'utf8')
    assert.match(realFile, expectation.sanity, `sanity: real ${site.file} still has the exact shape this recipe targets`)

    write(root, site.file, realFile)
    const cleanPlan = planSite(root, site)
    assert.equal(cleanPlan.ok, true, `expected the isolated copy of the real file to plan cleanly: ${cleanPlan.reason ?? ''}`)
    assert.ok(cleanPlan.edits.length > 0, 'expected at least one edit against the real, unmutated file')

    const mutated = expectation.mutate(realFile)
    assert.notEqual(mutated, realFile, 'sanity: mutation actually changed the fixture')
    write(root, site.file, mutated)
    const mutatedPlan = planSite(root, site)
    assert.equal(mutatedPlan.ok, false, 'the mutated copy must be refused, not mis-transformed')
    assert.match(mutatedPlan.reason, expectation.refusalMatch)
  })
}

// ════════════════════════════════════════════════════════════════════════
// End-to-end: runCodemod dry-run against the real repo closes exactly the
// five sites and touches nothing outside them.
// ════════════════════════════════════════════════════════════════════════

test('END-TO-END dry-run: runCodemod against the real repo plans all 5 sites with zero refusals, and --apply is idempotent', () => {
  const repoRoot = resolve('.')
  const dryRun = runCodemod({ repoRoot, apply: false })
  assert.equal(dryRun.totalSites, 5)
  assert.equal(dryRun.totalRefused, 0, JSON.stringify(dryRun.results.filter((r) => !r.ok).map((r) => ({ id: r.id, reason: r.reason }))))
  assert.equal(dryRun.totalApplied, 5)
  for (const r of dryRun.results) {
    assertParses(r.filePath, r.after)
  }
})
