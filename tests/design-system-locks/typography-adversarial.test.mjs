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
  // LEAD DECISION (Stage B typography false-green fix, cross-scanner
  // consistency with ts-colors.mjs's absenceValue standard): a branch that
  // never sets the read property is NOT provably absent unless every
  // candidate object literal carries an explicit `__proto__: null` — an
  // ordinary object literal can gain the property later through application
  // code or a prototype mutation the static lock cannot see. This fixture's
  // object literals carry no null-prototype marker, so `config.textClass`
  // must fail closed as unsupported. This REPLACES the previous PERMITTED
  // expectation below it (dist/design-system-baseline/cli-lanes/claude-typography/),
  // which was a false green: it treated "no leaf has this key" as
  // unconditionally safe. See the two capabilities immediately after this
  // one for the corrected, PROVEN absence and mixed-branch positive cases.
  it('FORBIDDEN: property access on a switch-based helper call whose branches never set the property, without a null-prototype proof, fails closed (getToolBadgeStatusConfig / statusConfig.textClass style — corrected false green)', () => {
    expectOne(
      `
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
    `,
      'typography/unsupported-text-utility',
      'config.textClass',
    )
  })

  it('FORBIDDEN: a MIXED leaf set (one branch sets the property, others prove it absent with `__proto__: null`) still fails closed — absenceValue proves absence for the WHOLE call, not per leaf, matching ts-colors\' own scoping (absentClassProperty only applies when NO leaf has the property at all; a branch that DOES set it trivially fails the "lacks property" check for the whole call)', () => {
    expectOne(
      `
      function getConfig(status) {
        switch (status) {
          case 'running': return { __proto__: null, indicator: 'x' }
          case 'success': return { __proto__: null, indicator: 'x', textClass: 'bg-emerald-500' }
          default: return { __proto__: null, indicator: 'x' }
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

  it('PERMITTED: a helper whose branches ALL prove the read property absent resolves as a safe no-op (pure absence proof, no found leaf at all)', () => {
    expectClean(`
      function getConfig(status) {
        switch (status) {
          case 'a': return { __proto__: null, label: 'A' }
          default: return { __proto__: null, label: 'B' }
        }
      }
      export function V({ status }) {
        const cfg = getConfig(status)
        return <div className={cfg.textClass} />
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

// ---------------------------------------------------------------------------
// LEAD DECISION (Stage B typography false-green fix): a member is provably
// ABSENT only under the same conditions as ts-colors.mjs's absenceValue —
// every candidate object literal has an explicit `__proto__: null`, no
// computed keys, no spreads; the call chain resolves through a safe,
// non-async/generator TOP-LEVEL factory; and any identifier binding an owner
// was reached through is used SAFELY everywhere in its enclosing function —
// read only via property/element access, never reassigned, aliased,
// deleted, Object.assign-mutated, or called as a method receiver. This
// standard gates PRESENT branch enumeration too, not only absence. The lead
// reproduced these as false greens against the prior pass; every FORBIDDEN
// case below returned `[]` (zero findings) before this fix — see
// dist/design-system-baseline/cli-lanes/claude-typography-fix/probe-red.log.
// ---------------------------------------------------------------------------

describe('capability: absence-of-member proof requires the ts-colors absenceValue standard (LEAD DECISION)', () => {
  const helper = "function getConfig(s){ switch(s){ case 'a': return { label: 'A' }; default: return { label: 'B' } } }\n"
  const helperNullProto = "function getConfig(s){ switch(s){ case 'a': return { __proto__: null, label: 'A' }; default: return { __proto__: null, label: 'B' } } }\n"

  it('FORBIDDEN: a property written onto the binding AFTER the call, before the read, fails closed (was a false green: the write itself proves the object escapes the leaf enumeration)', () => {
    expectOne(
      helper + "export function V({status}){ const cfg = getConfig(status); cfg.textClass = 'text-[10px]'; return <div className={cfg.textClass}/> }",
      'typography/unsupported-text-utility',
      'cfg.textClass',
    )
  })

  it('FORBIDDEN: a property genuinely absent from every branch, but without an explicit `__proto__: null` proof, fails closed (was a false green)', () => {
    expectOne(
      helper + "export function V({status}){ const cfg = getConfig(status); return <div className={cfg.textClass}/> }",
      'typography/unsupported-text-utility',
      'cfg.textClass',
    )
  })

  it('PERMITTED (positive control): the same absence, with every branch proving `__proto__: null`, resolves as a safe no-op', () => {
    expectClean(helperNullProto + "export function V({status}){ const cfg = getConfig(status); return <div className={cfg.textClass}/> }")
  })

  it('FORBIDDEN: Object.assign onto the binding after the call fails closed', () => {
    expectOne(
      helper + "export function V({status}){ const cfg = getConfig(status); Object.assign(cfg, { textClass: 'text-[10px]' }); return <div className={cfg.textClass}/> }",
      'typography/unsupported-text-utility',
      'cfg.textClass',
    )
  })

  it('FORBIDDEN: a delete on ANY member of the binding fails closed (not just a delete of the read property itself)', () => {
    expectOne(
      helper + "export function V({status}){ const cfg = getConfig(status); delete cfg.other; return <div className={cfg.textClass}/> }",
      'typography/unsupported-text-utility',
      'cfg.textClass',
    )
  })

  it('FORBIDDEN: aliasing the binding and writing through the alias fails closed (the alias write is invisible to a check scoped only to the original name)', () => {
    expectOne(
      helper + "export function V({status}){ const cfg = getConfig(status); const other = cfg; other.textClass = 'text-[10px]'; return <div className={cfg.textClass}/> }",
      'typography/unsupported-text-utility',
      'cfg.textClass',
    )
  })

  it('FORBIDDEN: a write inside a nested callback fails closed (the safety scan must not stop at a nested function boundary)', () => {
    expectOne(
      helper + "export function V({status}){ const cfg = getConfig(status); [1].forEach(() => { cfg.textClass = 'text-[10px]' }); return <div className={cfg.textClass}/> }",
      'typography/unsupported-text-utility',
      'cfg.textClass',
    )
  })

  it('FORBIDDEN: PRESENT branch enumeration is gated by the same binding-safety proof — a write to an UNRELATED property still disqualifies the whole binding', () => {
    expectOne(
      "function getConfig(s){ switch(s){ case 'a': return { textClass: 'text-sm' }; default: return { textClass: 'text-lg' } } }\nexport function V({status}) { const cfg = getConfig(status); cfg.other = 'z'; return <div className={cfg.textClass}/> }",
      'typography/unsupported-text-utility',
      'cfg.textClass',
    )
  })

  it('FORBIDDEN: a body-destructured local reached directly off the call is gated by the SAME absence standard (parallel code path, distinct from the property-access proof above)', () => {
    expectOne(
      helper + "export function V({status}){ const { textClass } = getConfig(status); return <div className={textClass}/> }",
      'typography/unsupported-text-utility',
      'textClass',
    )
  })

  it('PERMITTED: the same destructured read resolves cleanly once every branch proves `__proto__: null`', () => {
    expectClean(helperNullProto + "export function V({status}){ const { textClass } = getConfig(status); return <div className={textClass}/> }")
  })

  it('regression: a class-like parameter name never turns an unknown value into a silent pass — it only ever chooses between blocking kinds (extension-boundary vs unsupported), matching the capability above; an unknown LOCAL (not a parameter) with a class-like name still reaches unsupported off its own opaque call', () => {
    expectOne(
      'export function Comp() { const widthClass = getWidth(); return <div className={widthClass} /> }',
      'typography/unsupported-text-utility',
      'getWidth()',
    )
  })

  it('FORBIDDEN: a whole-file name reused by an unrelated inner `let` shadow must not resolve the OUTER const binding at a shadowed read site (hasMultipleVariableDeclarations gap: the top-level `bindings` Map fallback previously bypassed this guard)', () => {
    // Top-level `cfg` is a genuinely SAFE value ('text-base', never below
    // floor); the shadowed inner `cfg` is a genuine violation
    // ('text-[8px]'). Before the fix this resolved to the WRONG (outer, safe)
    // binding and returned zero findings — a false green via wrong-scope
    // resolution, not merely an unprovable case.
    expectOne(
      "const cfg = 'text-base'\nexport function Outer() {\n  function Inner() {\n    let cfg = 'text-[8px]'\n    return <div className={cfg} />\n  }\n  return Inner()\n}",
      'typography/unsupported-text-utility',
      'cfg',
    )
  })

  it('FORBIDDEN: an exported `let` (mutable) imported record\'s dynamic-key enumeration fails closed (moduleRecord\'s export collection does not itself filter by const, so importedRecordAllValues must)', () => {
    const source = "import { STATUS_BADGE } from './status'\nexport function Badge({ status }) { return <span className={cn('base', STATUS_BADGE[status])} /> }"
    expectOne(source, 'typography/unsupported-text-utility', 'STATUS_BADGE[status]', { modules: {
      'src/fixture.tsx': source,
      'src/status.ts': "export let STATUS_BADGE = { inbox: 'bg-zinc-500', done: 'bg-emerald-500' }",
    } })
  })

  it('FORBIDDEN: an exported const imported record that IS mutated in its own module (never-mutated requirement) fails closed', () => {
    const source = "import { STATUS_BADGE } from './status'\nexport function Badge({ status }) { return <span className={cn('base', STATUS_BADGE[status])} /> }"
    expectOne(source, 'typography/unsupported-text-utility', 'STATUS_BADGE[status]', { modules: {
      'src/fixture.tsx': source,
      'src/status.ts': "export const STATUS_BADGE = { inbox: 'bg-zinc-500', done: 'bg-emerald-500' }\nSTATUS_BADGE.extra = 'bg-amber-500'",
    } })
  })

  it('PERMITTED (positive control): an exported const, never-mutated imported record still enumerates cleanly (no regression from the never-mutated check)', () => {
    const source = "import { STATUS_BADGE } from './status'\nexport function Badge({ status }) { return <span className={cn('base', STATUS_BADGE[status])} /> }"
    expectClean(source, { modules: {
      'src/fixture.tsx': source,
      'src/status.ts': "export const STATUS_BADGE = { inbox: 'bg-zinc-500', done: 'bg-emerald-500' }",
    } })
  })
})
