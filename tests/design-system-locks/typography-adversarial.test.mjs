import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { scan } from '../../scripts/design-system-locks/typography.mjs'

// ---------------------------------------------------------------------------
// Stage B typography lock — scanner-capability follow-up (54-item unsupported
// worklist, dist/design-system-baseline/cli-lanes/claude-lead/worklist-typography-unsupported.json).
//
// Every capability below closes one or more real `unsupported-text-utility`
// findings from that worklist with a PROVEN, precise classification — never
// a blanket exemption. Each capability gets a PERMITTED fixture (the pattern
// now resolves) and at least one FORBIDDEN fixture (a near-identical but
// unprovable variant that must still fail closed as unsupported, or as an
// exact `extension-boundary` when the value crosses a genuine, unproven
// caller boundary). Oracle independence: every expected ruleId/syntax below
// is derived from design-system/enforcement/contract.json's fail-closed and
// runtimeBoundaryClassification clauses and from re-deriving each fixture's
// class tokens against the Tailwind 4.3.3 default scale by hand — never
// copied from a scanner run.
// ---------------------------------------------------------------------------

const POLICY = { tokenCssNames: [], resolvedTokens: {} }

function findings(source, { path = 'src/fixture.tsx', policy = POLICY, modules } = {}) {
  return scan({ path, source, policy, modules })
}

function expectClean(source, options) {
  const found = findings(source, options)
  assert.deepEqual(
    found.map((f) => `${f.ruleId} ${f.syntax}`),
    [],
    `expected no findings, got: ${JSON.stringify(found, null, 2)}`,
  )
}

function expectOne(source, ruleId, syntax, options) {
  const found = findings(source, options)
  assert.equal(found.length, 1, `expected exactly one finding, got: ${JSON.stringify(found, null, 2)}`)
  assert.equal(found[0].ruleId, ruleId)
  assert.equal(found[0].syntax, syntax)
  assert.ok(found[0].message.length > 0, 'finding must carry an actionable message')
  return found[0]
}

describe('capability: class-like parameter forwarding (widthClass/triggerClassName/... style props)', () => {
  it('PERMITTED: a bare same-named non-className parameter ending in Class/ClassName forwards as an extension-boundary (sheet.tsx widthClass)', () => {
    expectOne('export function Sheet({ widthClass }) { return <div className={cn(widthClass, "x")} /> }', 'typography/extension-boundary', 'Sheet#widthClass')
  })

  it('PERMITTED: a renamed object-binding-pattern parameter (`className: alias`) still resolves under the SOURCE name (dialog.tsx overlayClassName)', () => {
    expectOne('export const Dialog = React.forwardRef(({ overlayClassName }, ref) => <div className={overlayClassName} />)', 'typography/extension-boundary', 'Dialog#overlayClassName')
  })

  it('FORBIDDEN: a name that merely CONTAINS "class" without the camelCase Class/ClassName boundary stays unsupported', () => {
    expectOne('export function Box({ subclassify }) { return <div className={subclassify} /> }', 'typography/unsupported-text-utility', 'subclassify')
  })

  it('FORBIDDEN: reassigning the class-like parameter before the read still fails closed (no regression from the naming widening)', () => {
    expectOne("export const Sheet = ({ widthClass }) => { widthClass = normalize(widthClass); return <div className={widthClass} /> }", 'typography/unsupported-text-utility', 'widthClass')
  })

  it('does not widen BODY destructuring beyond literal className/class (pinned narrower than the direct-parameter branches — matches the existing fixture at typography.test.mjs:952)', () => {
    expectOne('export function Wrap(props) { const { widthClass } = props; return <div className={widthClass} /> }', 'typography/unsupported-text-utility', 'widthClass')
  })
})

describe('capability: bare-parameter member access (Capability B — X.className / X?.className)', () => {
  it('PERMITTED: a .map() callback parameter\'s .className member forwards as an extension-boundary named off the nearest NAMED ancestor (smart-select.tsx item.className)', () => {
    expectOne(
      'export function SmartSelect({ items }) { return <>{items.map((item) => <Option key={item.value} className={item.className} />)}</> }',
      'typography/extension-boundary',
      'SmartSelect#item.className',
    )
  })

  it('PERMITTED: an optional-chained member access on a bare (non-destructured) parameter resolves the same way (WhatsAppPairingBody.tsx iconProps?.className)', () => {
    expectOne(
      "function Retry({ iconProps }) { return <div className={`row ${iconProps?.className ?? ''}`} /> }",
      'typography/extension-boundary',
      'Retry#iconProps.className',
    )
  })

  it('FORBIDDEN: a non-className member key carries no naming signal and stays unsupported (TaskDetailPanel-style o.color)', () => {
    expectOne('export function List({ items }) { return <>{items.map((o) => <span className={cn("x", o.color)} />)}</> }', 'typography/unsupported-text-utility', 'o.color')
  })

  it('FORBIDDEN: mutating the .className member before the read breaks the proof', () => {
    expectOne(
      'export function List({ items }) { return <>{items.map((item) => { item.className = "x"; return <span className={item.className} /> })}</> }',
      'typography/unsupported-text-utility',
      'item.className',
    )
  })

  it('FORBIDDEN: a parameter that is itself further destructured is not a bare member-access source', () => {
    expectOne('export function List({ items }) { return <>{items.map(({ className }) => <span className={className} />)}</> }', 'typography/unsupported-text-utility', 'className')
  })
})

describe('capability: finite-return call resolution (Capability C1/C2 — switch/if-chain/IIFE helpers)', () => {
  it('PERMITTED: property access on a switch-based helper call resolves per branch, including a branch that never sets the property (getToolBadgeStatusConfig / statusConfig.textClass style)', () => {
    // bg-* (not text-*/font-*) deliberately: this lane owns typography only,
    // so a clean colour utility proves the PROPERTY resolved without also
    // asserting on the colour lane's own (unrelated) token-registration
    // findings.
    expectClean(`
      function getConfig(status) {
        switch (status) {
          case 'running': return { indicator: 'x' }
          case 'success': return { indicator: 'x', textClass: 'bg-emerald-500' }
          default: return { indicator: 'x' }
        }
      }
      export function Badge({ status }) {
        const config = getConfig(status)
        return <span className={cn('base', config.textClass)} />
      }
    `)
  })

  it('PERMITTED: an if-chain immediately-invoked function expression resolves per branch (BrowserLiveView driveChip style)', () => {
    expectClean(`
      export function Chip({ state }) {
        const chip = (() => {
          if (state === 'a') return { textClass: 'bg-sky-500' }
          if (state === 'b') return { textClass: 'bg-amber-500' }
          return { textClass: 'bg-zinc-500' }
        })()
        return <span className={cn('base', chip.textClass)} />
      }
    `)
  })

  it('PERMITTED: a whole call used directly as the class value resolves the same way (ToolPolicyEditor BULK_BUTTON_CLASS style)', () => {
    expectClean(`
      export function Editor({ active }) {
        const buttonClass = (isActive) => {
          const base = 'border-[var(--color-border)]'
          const activeClass = 'bg-emerald-500/20'
          if (!isActive) return base
          return activeClass
        }
        return <button className={\`btn \${buttonClass(active)}\`} />
      }
    `)
  })

  it('PERMITTED: a body-destructured local bound to a finite-return call resolves the same way (GoalIndicator className style)', () => {
    expectClean(`
      function describeState(state) {
        switch (state) {
          case 'a': return { className: 'bg-amber-500' }
          default: { const x = state; throw new Error(String(x)) }
        }
      }
      export function Indicator({ state }) {
        const { className } = describeState(state)
        return <span className={className} />
      }
    `)
  })

  it('FORBIDDEN: a `let` reassigned across an if/else-if chain (including one branch sourced from an opaque expression) stays unsupported — mutable bindings are never resolved (GenericToolCall style)', () => {
    expectOne(
      `
      export function Tool({ isRunning, sentinels }) {
        let statusConfig
        if (isRunning) statusConfig = getConfig('running')
        else statusConfig = sentinels.statusConfig
        return <span className={cn('base', statusConfig.textClass)} />
      }
    `,
      'typography/unsupported-text-utility',
      'statusConfig.textClass',
    )
  })

  it('FORBIDDEN: a `??` fallback whose left operand is an opaque member read stays unsupported (ToolCallBadge style)', () => {
    expectOne(
      `
      function getConfig(status) { return { textClass: 'text-[var(--color-error)]' } }
      export function Badge({ status, sentinels }) {
        const config = sentinels.statusConfig ?? getConfig(status)
        return <span className={cn('base', config.textClass)} />
      }
    `,
      'typography/unsupported-text-utility',
      'config.textClass',
    )
  })

  it('FORBIDDEN: a switch clause with a non-return/throw/const/void statement is not provably finite and fails the WHOLE call closed', () => {
    expectOne(
      `
      function getConfig(status) {
        switch (status) {
          case 'a': sideEffect(); return { textClass: 'text-xs' }
          default: return { textClass: 'text-xs' }
        }
      }
      export function Badge({ status }) {
        const config = getConfig(status)
        return <span className={cn('base', config.textClass)} />
      }
    `,
      'typography/unsupported-text-utility',
      'config.textClass',
    )
  })

  it('FORBIDDEN: a return that is not the LAST statement in the function body is not provably finite', () => {
    expectOne(
      `
      function getConfig(status) {
        if (status === 'a') return { textClass: 'text-xs' }
        return { textClass: 'text-sm' }
        console.log('unreachable but still a following statement')
      }
      export function Badge({ status }) {
        const config = getConfig(status)
        return <span className={cn('base', config.textClass)} />
      }
    `,
      'typography/unsupported-text-utility',
      'config.textClass',
    )
  })
})

describe('capability: record lookups inside a leaf chain (STATUS_BADGE[status] / PRIORITY_BADGE[p] ?? PRIORITY_BADGE[3] style)', () => {
  it('PERMITTED: a dynamic-key ElementAccess on an IMPORTED const record enumerates every value', () => {
    const source = "import { STATUS_BADGE } from './status'\nexport function Badge({ status }) { return <span className={cn('base', STATUS_BADGE[status])} /> }"
    expectClean(source, { modules: {
      'src/fixture.tsx': source,
      'src/status.ts': "export const STATUS_BADGE = { inbox: 'bg-zinc-500', done: 'bg-emerald-500' }",
    } })
  })

  it('PERMITTED: a `??` chain of two record lookups (one dynamic-keyed, one literal-keyed) resolves as a leaf union, closing on a property read (TaskCard PRIORITY_BADGE style)', () => {
    expectClean(`
      export const PRIORITY_BADGE = {
        1: { label: 'P1', className: 'bg-red-500' },
        3: { label: 'P3', className: 'bg-amber-500' },
      }
      export function Card({ priority }) {
        const badge = PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]
        return <span className={cn('base', badge.className)}>{badge.label}</span>
      }
    `)
  })

  it('FORBIDDEN: a spread inside an IMPORTED record\'s object literal defeats the enumeration proof (importedRecordAllValues\' own spread check)', () => {
    const source = "import { STATUS_BADGE } from './status'\nexport function Badge({ status }) { return <span className={cn('base', STATUS_BADGE[status])} /> }"
    expectOne(source, 'typography/unsupported-text-utility', 'STATUS_BADGE[status]', { modules: {
      'src/fixture.tsx': source,
      'src/status.ts': "export const BASE = { done: 'bg-emerald-500' }\nexport const STATUS_BADGE = { ...BASE, inbox: 'bg-zinc-500' }",
    } })
  })

  it('FORBIDDEN: a spread inside a LOCAL record reached through a `??`-of-record-lookups leaf chain defeats classCarryingLeaves\' own enumeration (distinct code path from importedRecordAllValues above)', () => {
    expectOne(
      `
      export const BASE = { done: { className: 'bg-emerald-500' } }
      export const PRIORITY_BADGE = { ...BASE, 3: { className: 'bg-amber-500' } }
      export function Card({ priority }) {
        const badge = PRIORITY_BADGE[priority] ?? PRIORITY_BADGE[3]
        return <span className={cn('base', badge.className)} />
      }
    `,
      'typography/unsupported-text-utility',
      'badge.className',
    )
  })

  it('FORBIDDEN: an imported record referenced with no supplied module map stays unsupported (documented boundary — cross-module resolution requires the audit\'s frozen module context)', () => {
    expectOne("export function Badge({ status }) { return <span className={cn('base', STATUS_BADGE[status])} /> }", 'typography/unsupported-text-utility', 'STATUS_BADGE[status]')
  })
})

describe('capability: transparent local variadic joiner (ChipListInput.tsx classes() style)', () => {
  it('PERMITTED: a same-file `function name(...parts) { return parts.filter(Boolean).join(\' \') }` is treated as a class builder, walking each argument', () => {
    expectOne(
      `
      function classes(...parts) { return parts.filter(Boolean).join(' ') }
      export function Row({ rowClass }) {
        return <div className={classes('base', rowClass)} />
      }
    `,
      'typography/extension-boundary',
      'Row#rowClass',
    )
  })

  it('PERMITTED: the `.filter(Boolean)` step is optional — a bare `.join()` joiner is recognized too', () => {
    expectClean(`
      function classes(...parts) { return parts.join(' ') }
      export function Row() {
        return <div className={classes('font-mono', 'bg-zinc-500')} />
      }
    `)
  })

  // Not a silent pass: the call is still not treated as a transparent class
  // builder (its extra statement fails transparentJoinerDeclaration's
  // single-statement shape check), but the SEPARATE, more general
  // finite-return-call fallback (Capability C1/C2, tested above) still
  // resolves classes()'s own single return statement down to
  // `cleaned.join(' ')` — which THEN fails closed on its own terms, because
  // `cleaned` doesn't resolve to a provable array literal for the .join()
  // array-spread shape. Two independent fail-closed proofs compose; neither
  // alone decides "opaque call" vs "resolves, but the tail is unprovable".
  it('FORBIDDEN: an extra statement in the joiner body is not a transparent builder — the finite-return fallback still walks in, then fails closed on the unprovable .join() receiver', () => {
    expectOne(
      `
      function classes(...parts) { const cleaned = parts.filter(Boolean); return cleaned.join(' ') }
      export function Row({ rowClass }) {
        return <div className={classes('base', rowClass)} />
      }
    `,
      'typography/unsupported-text-utility',
      "cleaned.join(' ')",
    )
  })

  it('FORBIDDEN: joining a DIFFERENT receiver than the rest parameter is not a transparent joiner (same finite-return-fallback-then-fails-closed composition)', () => {
    expectOne(
      `
      function classes(...parts) { return OTHER.join(' ') }
      export function Row({ rowClass }) {
        return <div className={classes('base', rowClass)} />
      }
    `,
      'typography/unsupported-text-utility',
      "OTHER.join(' ')",
    )
  })
})

describe('capability A: self-separating conditional template glue (independent review capabilities A)', () => {
  it('PERMITTED: a glued-before ternary whose branches are empty or start with a space is separated, not fragment-joined (FullCalendarView.tsx style)', () => {
    expectClean("export function Cell({ isToday }) { return <td className={`fc-day-cell${isToday ? ' fc-day-today' : ''}`} /> }")
  })

  it('PERMITTED: a nested TEMPLATE branch whose own head supplies the missing leading space is separated, and its own interior interpolation is then resolved normally (ChatImage.tsx style)', () => {
    expectOne(
      "export function Img({ className }) { return <div className={`relative inline-block${className ? ` ${className}` : ''}`} /> }",
      'typography/extension-boundary',
      'Img#className',
    )
  })

  it('PERMITTED: multiple glued-before ternary spans in one template all resolve independently (UsageScreen.tsx style)', () => {
    expectClean("export function Row({ hero }) { return <span className={`font-mono uppercase${hero ? ' text-2xl' : ' text-base'}`} /> }")
  })

  it('FORBIDDEN: a glued-before ternary branch that does NOT start with whitespace is a genuine fragment join and stays whole-template-unsupported', () => {
    expectOne(
      "export function Cell({ cond }) { return <td className={`text-s${cond ? 'm' : 'm'}`} /> }",
      'typography/unsupported-text-utility',
      '`text-s${cond ? \'m\' : \'m\'}`',
    )
  })

  it('FORBIDDEN: a self-separating proof on one side does not launder a genuinely glued OTHER side (glued-after with a non-whitespace-trailing branch)', () => {
    expectOne(
      "export function Cell({ cond }) { return <td className={`${cond ? ' a' : ''}suffix`} /> }",
      'typography/unsupported-text-utility',
      '`${cond ? \' a\' : \'\'}suffix`',
    )
  })

  it('regression: the static token adjacent to a self-separating span is still classified on its own, not silently dropped by the boundary-trim fix', () => {
    // 'text-xs' is deliberately a REAL below-floor violation (never clean
    // regardless of policy), so a silent drop of the adjacent static token
    // would flip this from one real finding to zero findings — a clean scan
    // would not distinguish "classified and happens to be clean" from
    // "silently skipped", so a token that MUST fire is required to prove the
    // adjacent-token path is actually exercised (caught mutation M8).
    expectOne(
      "export function Cell({ isToday }) { return <td className={`text-xs${isToday ? ' fc-day-today' : ''}`} /> }",
      'typography/text-size-below-floor',
      'text-xs',
    )
  })
})
