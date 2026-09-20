import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'

import { scan } from '../../scripts/design-system-locks/spacing.mjs'

// Contract-derived policy fixture: the canonical D10 closed scale, 14px root,
// and registered primitive/semantic spacing tokens. Expectations come from
// design-system-definition D1/D10 rather than scanner output.
const POLICY = Object.freeze({
  tokenCssNames: Object.freeze([
    '--space-0',
    '--space-1',
    '--space-2',
    '--space-3',
    '--space-4',
    '--space-5',
    '--space-6',
    '--space-7',
    '--space-8',
    '--space-control-gap',
  ]),
  resolvedTokens: Object.freeze({
    'font.root.default': '14px',
    'space.scale.0': '0px',
    'space.scale.1': '4px',
    'space.scale.2': '8px',
    'space.scale.3': '16px',
    'space.scale.4': '24px',
    'space.scale.5': '32px',
    'space.scale.6': '40px',
    'space.scale.7': '48px',
    'space.scale.8': '64px',
    'space.control.gap': '8px',
  }),
})

function findings(path, source) {
  const result = scan({ path, source, policy: POLICY })
  assert.ok(Array.isArray(result), 'spacing scanner must return a findings array')
  return result
}

function expectBlocked(path, source, label) {
  const result = findings(path, source)
  assert.ok(result.length > 0, `${label}: governed D10 violation returned empty success`)
  for (const finding of result) {
    assert.equal(finding.path, path)
    assert.ok(typeof finding.ruleId === 'string' && finding.ruleId.length > 0)
    assert.ok(typeof finding.syntax === 'string' && finding.syntax.length > 0)
    assert.ok(typeof finding.message === 'string' && finding.message.length > 0)
  }
}

function expectClean(path, source, label) {
  assert.deepEqual(findings(path, source), [], `${label}: canonical spacing/geometry control must be clean`)
}

describe('D10 spacing remains pixel-stable across the adjustable root', () => {
  it('rejects rem spacing that equals 4px only at the 14px default root', () => {
    expectBlocked(
      'src/styles/adversarial.css',
      '.x { padding: 0.285714285714rem }',
      'root-dependent rem padding',
    )
  })

  it('keeps canonical pixel steps and registered pixel tokens clean', () => {
    expectClean(
      'src/styles/adversarial.css',
      '.x { padding: 4px 8px; gap: var(--space-control-gap); margin: var(--space-2) }',
      'canonical pixel spacing',
    )
  })

  it('does not confuse non-spacing geometry with the spacing scale', () => {
    expectClean(
      'src/styles/adversarial.css',
      '.x { width: 13px; min-height: 44px; inset: 7px }',
      'caller-controlled geometry',
    )
  })
})

describe('D10 judges the resulting spacing expression', () => {
  it('rejects calc whose on-scale operand produces an off-scale result', () => {
    expectBlocked(
      'src/styles/adversarial.css',
      '.x { gap: calc(8px / 3) }',
      'off-scale calc result',
    )
  })

  it('does not silently exempt percentage padding', () => {
    expectBlocked(
      'src/styles/adversarial.css',
      '.x { padding-inline: 50% }',
      'percentage padding outside the closed pixel scale',
    )
  })
})

describe('D10 follows locally resolvable JSX class and style values', () => {
  it('rejects an off-scale class held in a local constant', () => {
    expectBlocked(
      'src/components/Adversarial.tsx',
      "const spacingClass = 'p-[13px]'; export const X = () => <div className={spacingClass} />",
      'local class constant',
    )
  })

  it('rejects an off-scale style object held in a local constant', () => {
    expectBlocked(
      'src/components/Adversarial.tsx',
      'const spacingStyle = { padding: 13 }; export const X = () => <div style={spacingStyle} />',
      'local style constant',
    )
  })

  it('resolves same-named aliases within their own function scopes', () => {
    const result = findings(
      'src/components/Adversarial.tsx',
      "function A(){const cls='p-[13px]';return <div className={cls}/>} function B(){const cls='p-[8px]';return <div className={cls}/>}",
    )
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'spacing/off-scale', syntax: 'p-[13px]' },
    ])
  })

  it('treats a shadowing parameter as dynamic instead of inheriting an outer alias', () => {
    const result = findings(
      'src/components/Adversarial.tsx',
      "const cls='p-[13px]'; function Box(cls:string){return <div className={cls}/>}",
    )
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'spacing/unsupported', syntax: 'className: cls' },
    ])
  })

  it('reports cyclic aliases as unsupported governed syntax', () => {
    const result = findings(
      'src/components/Adversarial.tsx',
      'const a=b; const b=a; export const X=()=> <div className={a}/>',
    )
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'spacing/unsupported', syntax: 'className: a' },
    ])
  })
})

describe('known root-dependent spacing is ordinary migration debt', () => {
  it('classifies parsed Tailwind rem utilities separately from unsupported syntax', () => {
    const result = findings(
      'src/components/Adversarial.tsx',
      'export const X=()=> <div className="p-2 gap-4" />',
    )
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'spacing/root-dependent', syntax: 'p-2' },
      { ruleId: 'spacing/root-dependent', syntax: 'gap-4' },
    ])
  })
})

describe('reviewable spacing extension boundaries', () => {
  it('uses receiving-symbol identity for direct className prop forwarding', () => {
    const result = findings(
      'src/components/Adversarial.tsx',
      'export const Panel=({className}:{className?:string}) => <div className={className}/>',
    )
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'spacing/extension-boundary', syntax: 'Panel#className' },
    ])
  })

  it('requires safe-area runtime insets to declare an on-scale fallback', () => {
    const valid = findings(
      'src/styles/adversarial.css',
      '.x { padding-bottom: env(safe-area-inset-bottom, 0px) }',
    )
    assert.deepEqual(valid.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'spacing/extension-boundary', syntax: 'padding-bottom: env(safe-area-inset-bottom, 0px)' },
    ])

    expectBlocked(
      'src/styles/adversarial.css',
      '.x { padding-bottom: env(safe-area-inset-bottom) }',
      'safe-area spacing without fallback',
    )
  })
})

describe('Tailwind v4 registered-token shorthand', () => {
  it('accepts parenthesized shorthand for a registered D10 token', () => {
    expectClean(
      'src/components/Adversarial.tsx',
      'export const X = () => <div className="p-(--space-2) md:gap-(--space-control-gap)" />',
      'registered Tailwind v4 spacing shorthand',
    )
  })
})

describe('D10 absent inline style requires an unshadowed undefined', () => {
  for (const expression of ['undefined', '(undefined)', '(undefined as CSSProperties)', 'flag ? undefined : { padding: 8 }', 'flag ? { gap: 16 } : undefined']) {
    it(`accepts the absent style branch in ${expression}`, () => {
      assert.deepEqual(findings('src/absent.tsx', `const view = <div style={${expression}} />`), [])
    })
  }

  it('still inspects off-scale spacing in the other conditional branch', () => {
    const result = findings('src/absent.tsx', 'const view = <div style={flag ? undefined : { padding: 7 }} />')
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/off-scale', syntax: 'padding: 7px' }])
  })

  it('still rejects dynamic spacing in the other conditional branch', () => {
    const result = findings('src/absent.tsx', 'const view = <div style={flag ? { paddingLeft: depth * 14 } : undefined} />')
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'padding-left: {expr}' }])
  })

  for (const [label, source] of [
    ['parameter', 'function view(undefined) { return <div style={undefined} /> }'],
    ['destructured parameter', 'function view({ value: undefined }) { return <div style={undefined} /> }'],
    ['array parameter', 'function view([undefined]) { return <div style={undefined} /> }'],
    ['rest binding', 'const { ...undefined } = input; const view = <div style={undefined} />'],
    ['local variable', 'const undefined = { padding: 8 }; const view = <div style={undefined} />'],
    ['nested destructuring', 'const { x: [undefined] } = input; const view = <div style={undefined} />'],
    ['named import', 'import { value as undefined } from "./value"; const view = <div style={undefined} />'],
    ['default import', 'import undefined from "./value"; const view = <div style={undefined} />'],
    ['namespace import', 'import * as undefined from "./value"; const view = <div style={undefined} />'],
    ['import equals', 'import undefined = require("./value"); const view = <div style={undefined} />'],
    ['catch binding', 'try {} catch (undefined) { const view = <div style={undefined} /> }'],
    ['destructured catch', 'try {} catch ({ value: undefined }) { const view = <div style={undefined} /> }'],
    ['function declaration', 'function undefined() {} const view = <div style={undefined} />'],
    ['named function expression', 'const view = function undefined() { return <div style={undefined} /> }'],
    ['class declaration', 'class undefined {} const view = <div style={undefined} />'],
    ['named class expression', 'const X = class undefined { render() { return <div style={undefined} /> } }'],
    ['namespace declaration', 'namespace undefined {} const view = <div style={undefined} />'],
    ['enum declaration', 'enum undefined { X } const view = <div style={undefined} />'],
    ['with scope', 'with (scope) { const view = <div style={undefined} /> }'],
    ['direct eval', 'eval(code); const view = <div style={undefined} />'],
  ]) {
    it(`does not mistake ${label} for absent style`, () => {
      const result = findings('src/shadow.tsx', source)
      assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'style: undefined' }])
    })
  }

  it('keeps unresolved and cyclic aliases unsupported', () => {
    for (const source of ['const view = <div style={missing} />', 'const a = b; const b = a; const view = <div style={a} />']) {
      const result = findings('src/aliases.tsx', source)
      assert.equal(result.length, 1)
      assert.equal(result[0].ruleId, 'spacing/unsupported')
      assert.match(result[0].syntax, /^style: (missing|a)$/)
    }
  })

  it('does not treat an ordinary property name as a lexical binding', () => {
    assert.deepEqual(findings('src/property.tsx', 'const config = { undefined: 1 }; const view = <div style={undefined} />'), [])
  })
})


// JavaScript && returns its right operand or a falsy left value. Only a whole
// class value may use this proof; fragments and opaque builders stay unresolved.
describe('D10 logical guards contribute only complete right-hand class values', () => {
  const shape = result => result.map(({ ruleId, syntax }) => ({ ruleId, syntax }))
  for (const expression of ["guard && 'p-[8px]'", "first && second && 'gap-[16px]'", "0 && 'p-[8px]'", "false && 'p-[8px]'"]) {
    it(`accepts whole JSX class value ${expression}`, () => {
      assert.deepEqual(findings('src/guard.tsx', `const view = <div className={${expression}} />`), [])
    })
  }
  it('checks off-scale and root-dependent classes on the right of an unknown guard', () => {
    assert.deepEqual(shape(findings('src/guard.tsx', "const view = <div className={guard && 'p-[7px] m-2'} />")), [
      { ruleId: 'spacing/off-scale', syntax: 'p-[7px]' },
      { ruleId: 'spacing/root-dependent', syntax: 'm-2' },
    ])
  })
  it('keeps an unknown right operand unsupported', () => {
    assert.deepEqual(shape(findings('src/guard.tsx', 'const view = <div className={guard && unknownClass} />')), [
      { ruleId: 'spacing/unsupported', syntax: 'className: unknownClass' },
    ])
  })
  for (const operator of ['||', '??']) it(`does not discard a possible left class from ${operator}`, () => {
    assert.deepEqual(shape(findings('src/guard.tsx', `const view = <div className={unknownClass ${operator} 'p-[7px]'} />`)), [
      { ruleId: 'spacing/unsupported', syntax: 'className: unknownClass' },
      { ruleId: 'spacing/off-scale', syntax: 'p-[7px]' },
    ])
  })
  it('continues scanning a fallback after nested guards', () => {
    assert.deepEqual(shape(findings('src/guard.tsx', "const view = <div className={(guard && 'p-[8px]') || 'p-[7px]'} />")), [
      { ruleId: 'spacing/off-scale', syntax: 'p-[7px]' },
    ])
  })
  for (const [context, source] of [
    ['concatenation', "const view = <div className={'prefix ' + (guard && 'p-[8px]')} />"],
    ['template', 'const view = <div className={`p-${guard && 7}`} />'],
    ['style value', 'const view = <div style={{ padding: guard && 7 }} />'],
    ['opaque builder', "function cn(value) { return value ? value : external }; const view = <div className={cn(guard && 'p-[8px]')} />"],
    ['missing builder', "const view = <div className={cn(guard && 'p-[8px]')} />"],
    ['shadowed builder', "import { clsx } from 'clsx'; function view(clsx) { return <div className={clsx(guard && 'p-[8px]')} /> }"],
  ]) it(`does not use whole-value proof in ${context}`, () => {
    const result = findings('src/guard.tsx', source)
    assert.equal(result.some(finding => finding.ruleId === 'spacing/unsupported'), true, context)
  })
  for (const binding of ["import { clsx } from 'clsx'", "import clsx from 'clsx'"]) it(`accepts an authenticated class builder: ${binding}`, () => {
    assert.deepEqual(findings('src/guard.tsx', `${binding}; const view = <div className={clsx(guard && 'p-[8px]')} />`), [])
  })
  it('accepts the verified local cn composition but rejects changed wrapper behavior', () => {
    const source = "import { cn } from '@/lib/utils'; const view = <div className={cn(guard && 'p-[8px]')} />"
    const wrapper = "import { clsx } from 'clsx'; import { twMerge } from 'tailwind-merge'; export function cn(...inputs) { return twMerge(clsx(inputs)) }"
    const result = scan({ path: 'src/guard.tsx', source, policy: POLICY, modules: { 'src/guard.tsx': source, 'src/lib/utils.ts': wrapper } })
    assert.deepEqual(result, [])
    const changed = scan({ path: 'src/guard.tsx', source, policy: POLICY, modules: { 'src/guard.tsx': source, 'src/lib/utils.ts': wrapper.replace('twMerge(clsx(inputs))', 'opaque(inputs)') } })
    assert.equal(changed.some(finding => finding.ruleId === 'spacing/unsupported'), true)
  })
})


it('preserves fragment guards while authentic configured merge wrappers accept whole guards', () => {
  const source = "import { cn } from '@/lib/utils'; const view = <div className={cn(guard && 'p-[8px]')} />"
  const wrapper = "import { clsx } from 'clsx'; import { extendTailwindMerge } from 'tailwind-merge'; const merge = extendTailwindMerge({}); export function cn(...inputs) { return merge(clsx(inputs)) }"
  assert.deepEqual(scan({ path: 'src/guard.tsx', source, policy: POLICY, modules: { 'src/guard.tsx': source, 'src/lib/utils.ts': wrapper } }), [])
  for (const fragment of ["'prefix ' + (guard && 'p-[8px]')", '`p-${guard && 7}`']) {
    const findings = scan({ path: 'src/guard.tsx', source: `const view = <div className={${fragment}} />`, policy: POLICY })
    assert.equal(findings.some(finding => finding.ruleId === 'spacing/unsupported'), true)
  }
})


describe('D10 verified cn wrappers require stable defining bindings', () => {
  const definition = "import { clsx } from 'clsx'; import { twMerge } from 'tailwind-merge'; export function cn(...inputs) { return twMerge(clsx(inputs)) }"
  for (const [name, alteration] of [
    ['direct assignment', "cn = () => 'p-[7px]'"],
    ['nested assignment', "function replace() { cn = () => 'p-[7px]' }; replace()"],
    ['destructuring assignment', "[cn] = external"],
    ['loop assignment', "for (cn of external) {}"],
    ['setter-mediated assignment', "const alias = { set value(value) { cn = value } }; alias.value = external"],
  ]) it(`rejects ${name} in the defining module`, () => {
    const source = "import { cn } from './utils.js'; const view = <div className={cn(guard && 'p-[8px]')} />"
    const result = scan({ path: 'src/guard.jsx', source, policy: POLICY, modules: { 'src/guard.jsx': source, 'src/utils.js': `${definition}; ${alteration}` } })
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: guard' }])
  })
  it('checks the defining binding behind a consumer import alias', () => {
    const source = "import { cn as clsx } from './utils.js'; const view = <div className={clsx(guard && 'p-[8px]')} />"
    const result = scan({ path: 'src/guard.jsx', source, policy: POLICY, modules: { 'src/guard.jsx': source, 'src/utils.js': `${definition}; cn = external` } })
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: guard' }])
  })
  it('keeps pristine re-exported wrappers usable but rejects a reassigned underlying binding', () => {
    const source = "import { cn } from './bridge.js'; const view = <div className={cn(guard && 'p-[8px]')} />"
    const modules = { 'src/guard.jsx': source, 'src/utils.js': definition, 'src/bridge.js': "import { cn } from './utils.js'; export { cn }" }
    assert.deepEqual(scan({ path: 'src/guard.jsx', source, policy: POLICY, modules }), [])
    const result = scan({ path: 'src/guard.jsx', source, policy: POLICY, modules: { ...modules, 'src/utils.js': `${definition}; function replace() { cn = external }; replace()` } })
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: guard' }])
  })
})

describe('authenticated class-builder aliases fail closed', () => {
  // Real-source group resolved-nested-class-builder, negative side: only the
  // verified wrapper implementations may resolve an aliased builder call.
  // Fake builders, shadowed or ambiguous names, missing module snapshots,
  // cva factories and mutated aliases keep their blocking finding.
  const definition = "import { clsx } from 'clsx'; import { twMerge } from 'tailwind-merge'; export function cn(...inputs) { return twMerge(clsx(inputs)) }"
  const consumerBody = (call) => `export const X = () => { const btn = ${call}; return <button className={btn} /> }`

  const aliased = (utilsModule, extraModules = {}) => {
    const source = `import { cn } from '@/lib/utils'\n${consumerBody("cn('flex', 'p-[8px]')")}`
    return scan({
      path: 'src/x/consumer.tsx',
      source,
      policy: POLICY,
      modules: { 'src/x/consumer.tsx': source, ...extraModules, ...(utilsModule === null ? {} : { 'src/lib/utils.ts': utilsModule }) },
    })
  }

  it('keeps a fake same-name wrapper unsupported (shape verification)', () => {
    const fake = "export function cn(...inputs) { return inputs.filter(Boolean).join(' ') }"
    const result = aliased(fake)
    assert.ok(result.some((finding) => finding.ruleId === 'spacing/unsupported' && /^className: cn\(/.test(finding.syntax)),
      'a non-wrapper cn must not resolve the alias')
  })

  it('keeps the alias unsupported when the wrapper module is not in the snapshot', () => {
    const result = aliased(null)
    assert.ok(result.some((finding) => finding.ruleId === 'spacing/unsupported' && /^className: cn\(/.test(finding.syntax)),
      'authentication requires the real wrapper source, not just the import statement')
  })

  it('keeps the alias unsupported when the consumer shadows the cn binding', () => {
    const source = `import { cn } from '@/lib/utils'\nexport function helper(cn: string) { return cn.length }\n${consumerBody("cn('flex', 'p-[8px]')")}`
    const result = scan({
      path: 'src/x/consumer.tsx',
      source,
      policy: POLICY,
      modules: { 'src/x/consumer.tsx': source, 'src/lib/utils.ts': definition },
    })
    assert.ok(result.some((finding) => finding.ruleId === 'spacing/unsupported' && /^className: cn\(/.test(finding.syntax)),
      'a shadowed name must not authenticate')
  })

  it('keeps the alias unsupported for ambiguous duplicate cn imports', () => {
    const source = `import { cn } from '@/lib/utils'\nimport { cn } from './other-utils'\n${consumerBody("cn('flex', 'p-[8px]')")}`
    const result = scan({
      path: 'src/x/consumer.tsx',
      source,
      policy: POLICY,
      modules: { 'src/x/consumer.tsx': source, 'src/lib/utils.ts': definition, 'src/x/other-utils.ts': definition },
    })
    assert.ok(result.some((finding) => finding.ruleId === 'spacing/unsupported' && /^className: cn\(/.test(finding.syntax)),
      'two matching imports must not authenticate')
  })

  it('resolves cva factory results in both direct and aliased positions (item 4 port closes this)', () => {
    // Item 4 (lead-assigned wave-3 follow-up), ported from ts-colors.mjs's
    // own cva-factory call-site resolution (inspectClassExpr, ~line 1539) --
    // see cvaFactoryDefinition's own header comment in spacing.mjs. This was
    // previously pinned unsupported in BOTH shapes; the port re-inspects the
    // cva definition's own config at each call site (sound: every string the
    // call could ever produce is already a member of that config), so both
    // resolve to the SAME real off-scale violation in the `bad` variant, not
    // an unsupported marker.
    const source = `
      import { cva } from 'class-variance-authority'
      const badgeVariants = cva('p-[8px]', { variants: { tone: { bad: 'p-[13px]' } } })
      export const A = () => <span className={badgeVariants({ tone: 'bad' })} />
      export const B = () => { const v = badgeVariants({ tone: 'bad' }); return <span className={v} /> }
    `
    const result = scan({ path: 'src/x/badge.tsx', source, policy: POLICY, modules: { 'src/x/badge.tsx': source } })
    assert.deepEqual(result.filter((finding) => finding.ruleId === 'spacing/unsupported'), [])
    assert.ok(result.some((finding) => finding.ruleId === 'spacing/off-scale' && finding.syntax === 'p-[13px]'))
  })

  it('keeps a reassigned alias unsupported', () => {
    // `let` alias whose initializer is an authenticated cn call: the sink must
    // stay blocked because the binding can be (and is) reassigned after the
    // initializer runs. File-scope reassignment detection cannot see inside
    // the component body, so constness is the deciding guarantee.
    const source = `import { cn } from '@/lib/utils'\nexport const X = () => { let btn = cn('flex', 'p-[8px]'); btn = 'extra'; return <button className={btn} /> }`
    const result = scan({
      path: 'src/x/consumer.tsx',
      source,
      policy: POLICY,
      modules: { 'src/x/consumer.tsx': source, 'src/lib/utils.ts': definition },
    })
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: "className: cn('flex', 'p-[8px]')" }])
  })

  it('no longer reports the generic pass-through inputs inside the real wrapper module (P10 port closes this)', () => {
    // P10 precision fix (ported from typography.mjs::classBuilderOwnParameterForward
    // — see classBuilderOwnParameterForward's own header comment): cn()'s own
    // definition (`export function cn(...inputs) { return twMerge(clsx(inputs)) }`)
    // is a transparent pass-through of its own parameter, never a live class
    // value to prove — every REAL class argument is already proven at each
    // actual call site elsewhere. This used to be pinned here as an
    // (intentionally accepted) unsupported finding, deferred to "central
    // contract registration"; the P10 port now resolves it directly, so the
    // real wrapper module scans clean of its own rest-parameter findings.
    const utilsSource = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), '../../src/lib/utils.ts'), 'utf8')
    const result = scan({ path: 'src/lib/utils.ts', source: utilsSource, policy: POLICY })
    const unsupported = result.filter((finding) => finding.ruleId === 'spacing/unsupported').map((finding) => finding.syntax)
    assert.ok(!unsupported.includes('className: clsx(inputs)'), 'clsx(inputs) pass-through no longer blocks')
    assert.ok(!unsupported.includes('className: inputs'), 'rest-parameter input no longer blocks')
  })
})

// P10 precision fix (ported from typography.mjs::classBuilderOwnParameterForward,
// see typography-adversarial.test.mjs's own "P10 precision fix" describe
// block for the reference suite this mirrors). Fairness controls mirror
// dist/design-system-baseline/cli-lanes/fanout/COMMON-RULES.md: the clean
// value alone gives [], the bad value written directly gives a finding.
describe('P10 precision fix: a CLASS_BUILDER function definition forwarding its OWN parameter is not a live class value', () => {
  const path = 'src/lib/utils.ts'
  const jsxPath = 'src/lib/utils.tsx'
  // spacing's guardClassBuilder authenticates a CLASS_BUILDER callee by its
  // real import (clsx/tailwind-merge), unlike typography's simple name
  // match -- every fixture below that CALLS clsx()/twMerge() needs their
  // real imports present, or the inner call itself (not the P10 proof under
  // test) would independently report unsupported for an unrelated reason.
  const IMPORTS = "import { clsx } from 'clsx'\nimport { twMerge } from 'tailwind-merge'\n"

  it('fairness control: the clean baseline (a registered spacing token, no class builder involved) gives no findings', () => {
    expectClean(jsxPath, "export const x = <p className=\"p-[var(--space-2)]\" />")
  })

  it('fairness control: a plain off-scale violation written directly still gives a finding', () => {
    const result = findings(jsxPath, "export const x = <p className=\"p-[7px]\" />")
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'))
  })

  it("PERMITTED: cn()'s real definition shape (src/lib/utils.ts) -- a rest parameter forwarded through a chain of two CLASS_BUILDER calls -- scans clean", () => {
    expectClean(path, IMPORTS + 'export function cn(...inputs) { return twMerge(clsx(inputs)) }')
  })

  it('PERMITTED: the arrow-function form of the same shape scans clean', () => {
    expectClean(path, IMPORTS + 'export const cn = (...inputs) => twMerge(clsx(inputs))')
  })

  it('PERMITTED: a plain (non-rest) single parameter forwarded through one CLASS_BUILDER call scans clean', () => {
    expectClean(path, IMPORTS + 'export function cn(input) { return clsx(input) }')
  })

  it('PERMITTED: a REAL class argument alongside the definition-site pass-through is still classified normally at its own call site', () => {
    const result = findings(jsxPath, IMPORTS + "export function cn(...inputs) { return twMerge(clsx(inputs)) }\nexport const x = <p className={cn('p-[7px]')} />")
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'))
    assert.ok(!result.some((f) => f.ruleId === 'spacing/unsupported'))
  })

  it("FORBIDDEN: the enclosing function's own name is not a CLASS_BUILDER -- the transparent-definition proof is scoped to the trusted CLASS_BUILDERS set, not any rest-forwarding function", () => {
    const result = findings(path, IMPORTS + 'export function wrap(...inputs) { return twMerge(clsx(inputs)) }')
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported' && f.syntax === 'className: inputs'))
  })

  it('FORBIDDEN: an extra statement before the return breaks the single-statement transparency proof', () => {
    const result = findings(path, IMPORTS + 'export function cn(...inputs) { logIt(inputs); return twMerge(clsx(inputs)) }')
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported' && f.syntax === 'className: inputs'))
  })

  it("FORBIDDEN: forwarding a DIFFERENT identifier than the function's own parameter is not a pass-through", () => {
    const result = findings(path, IMPORTS + 'export const cn = (...inputs) => twMerge(clsx(OTHER))')
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported' && f.syntax === 'className: OTHER'))
  })

  it('FORBIDDEN: more than one parameter is not the cn()/clsx() rest-forwarding shape', () => {
    const result = findings(path, IMPORTS + 'export function cn(inputs, extra) { return twMerge(clsx(inputs)) }')
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported' && f.syntax === 'className: inputs'))
  })

  it("FORBIDDEN (mutation proof): reverting the fix's name-gate to accept ANY enclosing function name would silently pass this -- pinning `wrap` (not a CLASS_BUILDER) as still-unsupported is the sentinel that would catch that mutation", () => {
    const result = findings(path, IMPORTS + 'export function classNamesHelper(...inputs) { return twMerge(clsx(inputs)) }')
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported' && f.syntax === 'className: inputs'))
  })
})

describe('renamed builder imports fail closed', () => {
  // Negative side of the HIGH-bypass repair: only VERIFIED builders may be
  // dispatched or alias-cleared under a renamed local binding. Renamed fake
  // wrappers, shadowed names, ambiguous imports, reassigned aliases,
  // non-builders and cva factories keep blocking.
  const scanConsumer = (source, extraModules = {}) => scan({
    path: 'src/x/consumer.tsx',
    source,
    policy: POLICY,
    modules: { 'src/x/consumer.tsx': source, ...extraModules },
  })
  const summary = (result) => result.map(({ ruleId, syntax }) => ({ ruleId, syntax }))
  const hasUnsupportedCall = (result, name) => result.some((f) => f.ruleId === 'spacing/unsupported' && f.syntax.startsWith(`className: ${name}(`))

  it('keeps a renamed fake wrapper blocking without trusting its arguments', () => {
    const source = "import { cn as merge } from './fake-utils'; const s = merge('p-[13px]'); export const X = () => <button className={s}/>"
    const result = scanConsumer(source, { 'src/x/fake-utils.ts': "export function cn(...inputs) { return inputs.filter(Boolean).join(' ') }" })
    assert.ok(hasUnsupportedCall(result, 'merge'), 'renamed fake wrapper must stay unsupported')
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/off-scale'), [], 'unverified renamed builder arguments must not be trusted as findings source')
  })

  it('keeps a shadowed renamed import blocking at both definition and sink', () => {
    const source = "import { clsx as formatClasses } from 'clsx'; export function helper(formatClasses: string) { return formatClasses.length }; const s = formatClasses('p-[13px]'); export const X = () => <button className={s}/>"
    const result = scanConsumer(source)
    assert.ok(hasUnsupportedCall(result, 'formatClasses'), 'shadowed renamed import must not authenticate')
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/off-scale'), [])
  })

  it('reports definition debt once while a reassigned renamed alias stays blocking at the sink', () => {
    const source = "import { clsx as formatClasses } from 'clsx'; export const X = () => { let s = formatClasses('p-[13px]'); s = 'extra'; return <button className={s}/> }"
    const result = scanConsumer(source)
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/off-scale').map((f) => f.syntax), ['p-[13px]'], 'definition dispatch is name-blind and reports exactly once')
    assert.ok(hasUnsupportedCall(result, 'formatClasses'), 'reassigned alias must stay blocking at the sink')
  })

  it('keeps ambiguous duplicate renamed imports blocking', () => {
    const source = "import { clsx as fmt } from 'clsx'; import { clsx as fmt } from './other-clsx'; const s = fmt('p-[8px]'); export const X = () => <button className={s}/>"
    const result = scanConsumer(source, { 'src/x/other-clsx.ts': 'export const clsx = () => ""' })
    assert.ok(hasUnsupportedCall(result, 'fmt'), 'ambiguous renamed imports must not authenticate')
  })

  it('does not dispatch an arbitrary renamed import as a builder', () => {
    const source = "import { notABuilder } from './other'; const s = notABuilder('p-[13px]'); export const X = () => <button className={s}/>"
    const result = scanConsumer(source)
    assert.ok(hasUnsupportedCall(result, 'notABuilder'), 'renamed non-builder must stay unsupported')
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/off-scale'), [])
  })

  it('keeps a renamed cva factory blocking in alias and direct positions', () => {
    const source = [
      "import { cva as variants } from 'class-variance-authority'",
      "const badge = variants('p-[8px]')",
      "export const A = () => <span className={badge}/>",
      "export const B = () => <span className={variants('p-[8px]')}/>",
    ].join('\n')
    const result = scanConsumer(source)
    assert.equal(result.filter((f) => f.ruleId === 'spacing/unsupported').length, 2, `both renamed cva shapes stay unsupported, got ${JSON.stringify(summary(result))}`)
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/off-scale'), [])
  })

  it('requires the wrapper module snapshot for a renamed cn import', () => {
    const source = "import { cn as merge } from '@/lib/utils'; const s = merge('p-[13px]'); export const X = () => <button className={s}/>"
    const result = scanConsumer(source)
    assert.ok(hasUnsupportedCall(result, 'merge'), 'renamed cn must not authenticate without the wrapper snapshot')
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/off-scale'), [])
  })

  it('blocks undefined class operands when a dynamic eval scope defeats the absence proof (F1)', () => {
    const source = "export const X = (code: string) => { eval(code); return <button className={undefined}/> }"
    const result = scanConsumer(source)
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: undefined' }])
  })

  it('keeps blocking when a conditional branch argument cannot be statically governed (F3)', () => {
    const source = "import { clsx as fmt } from 'clsx'; export const X = ({t, o}:{t:boolean, o:string}) => <button className={t ? fmt('p-[13px]') : fmt(o)}/>"
    const result = scanConsumer(source)
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/off-scale').map((f) => f.syntax), ['p-[13px]'], 'governed branch still reports its debt once')
    // The dispatch reports the opaque argument itself; the call marker stays
    // because the dry run could not prove that branch governed. Both are
    // pre-existing blocking semantics for opaque builder arguments.
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/unsupported').map((f) => f.syntax).sort(),
      ['className: fmt(o)', 'className: o'])
  })
})

describe('finite-dispatcher binding safety — additional escape shapes (frozen review fix)', () => {
  // Companion to the direct regression tests in spacing.test.mjs (same
  // frozen review: dist/design-system-baseline/cli-lanes/
  // claude-spacing-review/review.md). Covers two further escape shapes of
  // dispatcherBindingUsesSafe (spacing.mjs) that the direct tests do not
  // already exercise: spreading the binding into another object literal,
  // and passing it as an ordinary call argument. Either would let a caller
  // read or capture the live object and mutate it through that second
  // reference, which the binding-safety proof cannot see past — so both
  // must keep the member read blocking exactly like a direct property
  // write does.
  const getConfigHelper = "function getConfig(s) { switch (s) { case 'a': return { label: 'A' }; default: return { label: 'B' } } }\n"

  it('keeps a spread-into-object escape of the dispatcher-result binding unsupported', () => {
    const source = `${getConfigHelper}export function V({status}) { const cfg = getConfig(status); const merged = { ...cfg }; void merged; return <div className={cfg.textClass}/> }`
    const result = findings('src/components/Adversarial.tsx', source)
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/unsupported').map((f) => f.syntax), ['className: cfg.textClass'],
      'spreading the binding into another literal is an escape the proof cannot see past')
  })

  it('keeps the dispatcher-result binding unsupported once it is passed as an ordinary call argument', () => {
    const source = `${getConfigHelper}function poison(o) { o.textClass = 'p-[7px]' }\nexport function V({status}) { const cfg = getConfig(status); poison(cfg); return <div className={cfg.textClass}/> }`
    const result = findings('src/components/Adversarial.tsx', source)
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/unsupported').map((f) => f.syntax), ['className: cfg.textClass'],
      'passing the live binding to an arbitrary function is an escape the proof cannot see past')
  })
})

describe('record-binding escape guard (round 4 — closes the SIZES/REC-shaped false greens)', () => {
  // LEAD DECISION (round 4, cross-scanner-false-green.test.mjs, committed
  // ee0551dc3): resolveExpr's identifier branch trusted a `const` record's
  // ORIGINAL literal forever, regardless of any later mutation or escape,
  // because its only guard (isReassignedWithin(sourceFile, name)) matched
  // nothing but a bare `name = ...` reassignment and never descended past a
  // function boundary. recordBindingEscapes (spacing.mjs) replaces that for
  // record-shaped (object/array literal) bindings only -- see
  // recordLiteralRoot's own describe block below for why a CallExpression
  // alias is deliberately excluded. Each test here is a red/green regression
  // guard for exactly one shape recordBindingEscapes must catch; the shared
  // dist/design-system-baseline/cli-lanes/claude-spacing-fix4/parity.md
  // records the mutation-proof kill for each.
  const scanConsumer = (source, extraModules = {}) => scan({
    path: 'src/components/Adversarial.tsx',
    source,
    policy: POLICY,
    modules: { 'src/components/Adversarial.tsx': source, ...extraModules },
  })

  it('keeps a property write onto a local const record unsupported after the write', () => {
    const source = "const SIZES = { small: 'p-[var(--space-2)]' }\nSIZES.small = 'p-[7px]'\nexport function V(){ return <i className={SIZES.small}/> }"
    const result = scanConsumer(source)
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: SIZES.small' }])
  })

  it('keeps Object.assign onto a local const record unsupported', () => {
    const source = "const SIZES = { small: 'p-[var(--space-2)]' }\nObject.assign(SIZES, { small: 'p-[7px]' })\nexport function V(){ return <i className={SIZES.small}/> }"
    const result = scanConsumer(source)
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: SIZES.small' }])
  })

  it('keeps Reflect.set and Object.defineProperty onto a local const record unsupported', () => {
    for (const mutator of ["Reflect.set(SIZES, 'small', 'p-[7px]')", "Object.defineProperty(SIZES, 'small', { value: 'p-[7px]' })"]) {
      const source = `const SIZES = { small: 'p-[var(--space-2)]' }\n${mutator}\nexport function V(){ return <i className={SIZES.small}/> }`
      const result = scanConsumer(source)
      assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: SIZES.small' }], mutator)
    }
  })

  it('keeps a delete-then-reassign onto a local const record unsupported', () => {
    const source = "const SIZES = { small: 'p-[var(--space-2)]' }\ndelete SIZES.small\nSIZES.small = 'p-[7px]'\nexport function V(){ return <i className={SIZES.small}/> }"
    const result = scanConsumer(source)
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: SIZES.small' }])
  })

  it('keeps an imported record directly mutated by the importer unsupported', () => {
    const result = scanConsumer("import { SIZES } from '../lib/sizes'\nSIZES.small = 'p-[7px]'\nexport function V(){ return <i className={SIZES.small}/> }", {
      'src/lib/sizes.ts': "export const SIZES = { small: 'p-[var(--space-2)]' }",
    })
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: SIZES.small' }])
  })

  it('keeps an export mutated by a helper inside its OWN exporting module unsupported (never touching the importer)', () => {
    // The importer's local `REC2` binding is never written to at all here --
    // only the exporting module's own declaration is. The escape guard must
    // still catch this (see resolveExpr's exportingScope check), matching
    // ts-colors.mjs::absenceFactory's re-application of absenceBindingUsesSafe
    // to the resolved EXPORTING declaration.
    const result = scanConsumer("import { REC2 } from '../lib/rec2'\nexport function V(){ return <i className={REC2.small}/> }", {
      'src/lib/rec2.ts': "export const REC2 = { small: 'p-[var(--space-2)]' }\nfunction poison(){ REC2.small = 'p-[7px]' }\npoison()",
    })
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: REC2.small' }])
  })

  it('keeps a nested object mutated through Object.values/Object.entries live references unsupported (closes the colour-scanner nested-escape hole for spacing)', () => {
    // Object.values/Object.entries return LIVE references to REC's own
    // nested objects -- ts-colors.mjs::READONLY_OBJECT_STATIC_METHODS
    // whitelists these call names as safe arguments, which is exactly the
    // hole this closes: no call name is trusted here for a container-typed
    // (non-primitive) argument, only a structurally-proven primitive leaf.
    for (const mutator of [
      "Object.values(REC).forEach(v => { v.cls = 'p-[7px]' })",
      "for (const [, v] of Object.entries(REC)) v.cls = 'p-[7px]'",
    ]) {
      const source = `const REC = { a: { cls: 'p-[var(--space-2)]' } }\n${mutator}\nexport function V(){ return <i className={REC.a.cls}/> }`
      const result = scanConsumer(source)
      assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: REC.a.cls' }], mutator)
    }
  })

  it('keeps an array-literal alias of a nested object unsupported even though the alias itself is never renamed back', () => {
    const source = "const REC = { a: { cls: 'p-[var(--space-2)]' } }\nconst list = [REC.a]; list[0].cls = 'p-[7px]'\nexport function V(){ return <i className={REC.a.cls}/> }"
    const result = scanConsumer(source)
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: REC.a.cls' }])
  })

  it('keeps an imported nested object mutated via Object.values by the importer unsupported', () => {
    const result = scanConsumer("import { REC } from '../lib/rec'\nObject.values(REC).forEach(v => { v.cls = 'p-[7px]' })\nexport function V(){ return <i className={REC.a.cls}/> }", {
      'src/lib/rec.ts': "export const REC = { a: { cls: 'p-[var(--space-2)]' } }",
    })
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: REC.a.cls' }])
  })

  it('still resolves a plain, unmutated local const record cleanly (no over-blocking)', () => {
    const source = "const SIZES = { small: 'p-[var(--space-2)]' }\nexport function V(){ return <i className={SIZES.small}/> }"
    expectClean('src/components/Adversarial.tsx', source, 'unmutated local record')
  })

  it('still resolves a plain, unmutated imported record cleanly (no over-blocking)', () => {
    const result = scanConsumer("import { SIZES } from '../lib/sizes'\nexport function V(){ return <i className={SIZES.small}/> }", {
      'src/lib/sizes.ts': "export const SIZES = { small: 'p-[var(--space-2)]' }",
    })
    assert.deepEqual(result, [])
  })

  it('does not treat embedding a resolved STRING property (a primitive leaf) in another literal or call as an escape', () => {
    // The container-escape guard only fires for a NON-primitive (object/
    // array) embedded value -- a plain string copied into a new array
    // literal or passed to an arbitrary helper cannot carry a live
    // reference back into the record, so a LATER, direct read of the same
    // record property must stay trusted (not collapse to unsupported just
    // because an earlier statement also read it into a literal/call).
    const source =
      "const SIZES = { small: 'p-[var(--space-2)]', large: 'p-[var(--space-4)]' }\n" +
      "const list = [SIZES.small, SIZES.large]\n" +
      'void list\n' +
      "function widths(strings){ return strings }\n" +
      'widths(SIZES.small)\n' +
      "export function V(){ return <i className={SIZES.small}/> }"
    expectClean('src/components/Adversarial.tsx', source, 'string properties copied by value are not an escape')
  })

  it('still resolves numeric-key array-record element access cleanly after the escape guard (Record<number,string> shape)', () => {
    const source = "const styles = ['p-[var(--space-2)]', 'p-[var(--space-3)]'] as const\nexport const X = () => <div className={styles[1]} />"
    expectClean('src/components/Adversarial.tsx', source, 'static numeric element access on an unmutated array record')
  })
})

describe('record-binding escape guard is scoped to record-shaped initializers only (round 4 non-regression)', () => {
  // recordLiteralRoot gates recordBindingEscapes to object/array-literal
  // (optionally Object.freeze/seal/preventExtensions-wrapped) initializers
  // only. A CallExpression-initialized alias keeps using the pre-existing,
  // narrower isReassignedWithin gate so authenticatedBuilderAlias's own
  // const-ness check remains the deciding guarantee for those (see spacing.
  // mjs's comment at the resolveExpr identifier branch) -- these three
  // shapes are the ones the round-4 change could have silently regressed by
  // applying the stricter guard too broadly.
  const definition = "import { clsx } from 'clsx'; import { twMerge } from 'tailwind-merge'; export function cn(...inputs) { return twMerge(clsx(inputs)) }"

  it('still drills into a reassigned `let` call-expression alias at its definition site rather than collapsing to a generic unsupported', () => {
    const source = "import { cn } from '@/lib/utils'\nexport const X = () => { let btn = cn('flex', 'p-[8px]'); btn = 'extra'; return <button className={btn} /> }"
    const result = scan({ path: 'src/x/consumer.tsx', source, policy: POLICY, modules: { 'src/x/consumer.tsx': source, 'src/lib/utils.ts': definition } })
    assert.deepEqual(result.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: "className: cn('flex', 'p-[8px]')" }])
  })

  it('still reports an aliased call-expression export exactly once at its definition site (date-picker shape)', () => {
    const source = "import { cn } from '@/lib/utils'\nexport const TRIGGER = cn('flex items-center gap-2', 'px-3 py-1')\nexport const X = ({ className }) => <button className={cn(TRIGGER, className)} />"
    const result = scan({ path: 'src/x/consumer.tsx', source, policy: POLICY, modules: { 'src/x/consumer.tsx': source, 'src/lib/utils.ts': definition } })
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/unsupported'), [], 'TRIGGER passed as an ordinary cn() argument must not be treated as a record-binding escape')
  })

  it('still trusts an Object.freeze-wrapped const record exactly like a plain literal one', () => {
    const source = "const SIZES = Object.freeze({ small: 'p-[var(--space-2)]' })\nexport function V(){ return <i className={SIZES.small}/> }"
    expectClean('src/components/Adversarial.tsx', source, 'Object.freeze-wrapped record initializer')
  })
})

describe('dynamic-key record fan-out — adversarial shapes (closes STATUS_BADGE[run.status]-shaped unsupported debt)', () => {
  const scanConsumer = (source, extraModules = {}) => scan({
    path: 'src/components/Adversarial.tsx',
    source,
    policy: POLICY,
    modules: { 'src/components/Adversarial.tsx': source, ...extraModules },
  })

  it('aborts the whole fan-out when one branch is a spread (cannot rule out a shadowed key)', () => {
    const source = "const base = { a: 'p-[var(--space-2)]' }\nconst REC = { ...base, b: 'p-[var(--space-2)]' }\nexport function V({k}){ return <i className={REC[k]}/> }"
    assert.deepEqual(scanConsumer(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: REC[k]' }])
  })

  it('aborts the whole fan-out when one branch has a computed key (cannot rule out a match)', () => {
    const source = "const key = 'b'\nconst REC = { a: 'p-[var(--space-2)]', [key]: 'p-[var(--space-2)]' }\nexport function V({k}){ return <i className={REC[k]}/> }"
    assert.deepEqual(scanConsumer(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: REC[k]' }])
  })

  it('aborts the whole fan-out when one branch is a method (not a plain data property)', () => {
    const source = "const REC = { a: 'p-[var(--space-2)]', b() { return '' } }\nexport function V({k}){ return <i className={REC[k]}/> }"
    assert.deepEqual(scanConsumer(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: REC[k]' }])
  })

  it('still catches a mutation of the record after its declaration even though the read uses a dynamic key (round 4 guard composes with the new fan-out)', () => {
    const source = "const REC = { a: 'p-[var(--space-2)]', b: 'p-[var(--space-3)]' }\nREC.a = 'p-[7px]'\nexport function V({k}){ return <i className={REC[k]}/> }"
    assert.deepEqual(scanConsumer(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: 'className: REC[k]' }])
  })

  it('resolves a shorthand-property branch through the dynamic-key fan-out', () => {
    const a = 'p-[var(--space-2)]'
    void a
    const source = "const a = 'p-[var(--space-2)]'\nconst REC = { a, b: 'p-[var(--space-3)]' }\nexport function V({k}){ return <i className={REC[k]}/> }"
    expectClean('src/components/Adversarial.tsx', source, 'shorthand branch resolves like an ordinary property')
  })

  it('resolves a cross-module dynamic-key record cleanly when every branch is on-scale', () => {
    const result = scanConsumer("import { REC } from '../lib/rec'\nexport function V({k}){ return <i className={REC[k]}/> }", {
      'src/lib/rec.ts': "export const REC = { a: 'p-[var(--space-2)]', b: 'p-[var(--space-3)]' }",
    })
    assert.deepEqual(result, [])
  })
})

describe('destructured-member resolution — adversarial shapes (CAP-A port scope limits)', () => {
  const fixturePath = 'src/components/Fixture.tsx'
  const helper = "function describeState(state) {\n  switch (state) {\n    case 'a': return { className: 'p-[7px]' }\n    default: return { className: 'p-[8px]' }\n  }\n}\n"

  const runWith = (source) => scan({ path: fixturePath, source: helper + source, policy: POLICY, modules: { [fixturePath]: helper + source } })

  it('keeps a rest-destructured local unsupported (cannot bound what the rest captures)', () => {
    const source = "export const X = ({state}) => { const { ...rest } = describeState(state); return <p className={rest.className}/> }"
    const findings = runWith(source)
    assert.ok(findings.some((f) => f.ruleId === 'spacing/unsupported'), 'a rest element must not be resolved by the destructuring capability')
  })

  it('keeps a defaulted destructured local unsupported (the default introduces its own unproven expression)', () => {
    const source = "export const X = ({state}) => { const { className = 'p-[7px]' } = describeState(state); return <p className={className}/> }"
    assert.deepEqual(runWith(source).filter((f) => f.ruleId === 'spacing/unsupported').map((f) => f.syntax), ['className: className'])
  })

  it('keeps a nested-pattern destructured local unsupported', () => {
    const helperNested = "function describeState(state) {\n  switch (state) {\n    case 'a': return { style: { className: 'p-[7px]' } }\n    default: return { style: { className: 'p-[8px]' } }\n  }\n}\n"
    const source = "export const X = ({state}) => { const { style: { className } } = describeState(state); return <p className={className}/> }"
    const result = scan({ path: fixturePath, source: helperNested + source, policy: POLICY, modules: { [fixturePath]: helperNested + source } })
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/unsupported').map((f) => f.syntax), ['className: className'])
  })

  it('keeps a shadowing function parameter of the same name from being mistaken for the destructured local', () => {
    const source = "export const X = ({state}) => { const { className } = describeState(state); return <Inner className={className}/> }\nfunction Inner({className}) { return <p className={className}/> }"
    const result = runWith(source)
    // Inner's OWN `className` is a forwarded parameter (extension-boundary), not a dispatcher destructure — must not collapse the two proofs.
    assert.ok(result.some((f) => f.ruleId === 'spacing/extension-boundary'), 'Inner must still be classified as a forwarded className parameter')
  })
})

describe('dispatcherBindingUsesSafe destructuring exemption — adversarial shapes (CAP-E2 port scope limits)', () => {
  const fixturePath = 'src/components/Fixture.tsx'
  const getConfigHelper = "function getConfig(s) { switch (s) { case 'a': return { accentClass: 'p-[7px]', Icon: 1 }; default: return { accentClass: 'p-[8px]', Icon: 2 } } }\n"
  const runWith = (source) => scan({ path: fixturePath, source: getConfigHelper + source, policy: POLICY, modules: { [fixturePath]: getConfigHelper + source } })

  it('resolves a sibling property once an unrelated property is consumed only via simple destructuring', () => {
    const source = "export function V({status}) { const cfg = getConfig(status); const { Icon } = cfg; void Icon; return <div className={cfg.accentClass}/> }"
    const findings = runWith(source)
    assert.deepEqual(findings.filter((f) => f.ruleId === 'spacing/unsupported'), [])
    assert.deepEqual(findings.filter((f) => f.ruleId === 'spacing/off-scale').map((f) => f.syntax), ['p-[7px]'])
  })

  it('keeps the receiver blocking when the destructuring pattern itself is a rest capture', () => {
    const source = "export function V({status}) { const cfg = getConfig(status); const { ...rest } = cfg; void rest; return <div className={cfg.accentClass}/> }"
    assert.deepEqual(runWith(source).filter((f) => f.ruleId === 'spacing/unsupported').map((f) => f.syntax), ['className: cfg.accentClass'])
  })

  it('keeps the receiver blocking when the destructuring pattern has a default value', () => {
    const source = "export function V({status}) { const cfg = getConfig(status); const { Icon = 1 } = cfg; void Icon; return <div className={cfg.accentClass}/> }"
    assert.deepEqual(runWith(source).filter((f) => f.ruleId === 'spacing/unsupported').map((f) => f.syntax), ['className: cfg.accentClass'])
  })
})

describe('computed property name resolution — adversarial shapes (staticPropertyName port scope limits)', () => {
  it('keeps a non-literal computed style key unsupported (identifier expression, not a string literal)', () => {
    const findings = scan({
      path: 'src/x.tsx',
      source: "const key = 'padding'\nexport const X = () => <div style={{ [key]: '13px' }}/>",
      policy: POLICY,
    })
    assert.deepEqual(findings.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: '[computed]' }])
  })

  it('keeps a template-literal-with-substitution computed key unsupported', () => {
    const findings = scan({
      path: 'src/x.tsx',
      source: "const side = 'Top'\nexport const X = () => <div style={{ [`padding${side}`]: '13px' }}/>",
      policy: POLICY,
    })
    assert.deepEqual(findings.map(({ ruleId, syntax }) => ({ ruleId, syntax })), [{ ruleId: 'spacing/unsupported', syntax: '[computed]' }])
  })

  it('resolves a computed key that is a plain quoted string literal (no assertion needed)', () => {
    const findings = scan({
      path: 'src/x.tsx',
      source: "export const X = () => <div style={{ ['padding']: '13px' }}/>",
      policy: POLICY,
    })
    assert.deepEqual(syntaxes(findings, 'spacing/off-scale'), ['padding: 13px'])
  })
})

describe('exported record readable from another module — false-green fix (ported ts-colors.mjs::knownClassExportUsesSafe)', () => {
  // SP-FALSE-GREEN (lead review of SP-RECOVER): a record DECLARED, EXPORTED
  // and READ in the SAME file only ever had its OWN file's scope walked by
  // recordBindingEscapes (same-file case) or the reading file's scope walked
  // (already-imported case) -- neither walk ever considered a DIFFERENT
  // module in `modules` that imports the export by name and mutates it
  // there. Reproduces with `export let` and `export const`, and with both a
  // fixed-key (`M.a`) and a dynamic-key (`M[k]`) read -- HEAD already had
  // the fixed-key hole; CAP1's dynamic-key fan-out (this session) extended
  // it to dynamic keys too, since both funnel through the SAME resolveExpr
  // identifier branch for a module-level record. ts-colors.mjs blocks every
  // one of these variants via knownClassExportUsesSafe, checking every
  // importer in the modules context for a write; this ports that check.
  const good = 'p-[var(--space-2)]'
  const bad = 'p-[7px]'
  const pPath = 'src/components/P.tsx'
  const qPath = 'src/components/Q.tsx'

  const declareAndRead = (kind, key) => {
    const read = key === 'fixed' ? 'M.a' : 'M[k]'
    const params = key === 'fixed' ? '' : '{k}'
    return `export ${kind} M = { a: '${good}' }\nexport function V(${params}){ return <i className={${read}}/> }`
  }

  for (const kind of ['let', 'const']) {
    for (const key of ['fixed', 'dynamic']) {
      it(`keeps a same-file ${kind}, ${key}-key read of an exported record unsupported when a DIFFERENT module imports it by name and writes to it`, () => {
        const source = declareAndRead(kind, key)
        const q = `import { M } from './P'\nexport function hack(){ M.a = '${bad}' }`
        const result = scan({ path: pPath, source, policy: POLICY, modules: { [pPath]: source, [qPath]: q } })
        assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported'), `${kind}/${key}: a cross-module write through a named import must block`)
      })
    }
  }

  it('keeps the read unsupported when a different module imports it via a NAMESPACE import (untraceable -- blocks even though it only reads through the namespace)', () => {
    const source = declareAndRead('const', 'fixed')
    const q = `import * as NS from './P'\nexport function readViaNamespace(){ return NS.M.a }`
    const result = scan({ path: pPath, source, policy: POLICY, modules: { [pPath]: source, [qPath]: q } })
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported'), 'a namespace import cannot be traced by name and must block unconditionally, matching ts-colors')
  })

  it('keeps the read unsupported when a different module RE-EXPORTS it (untraceable -- blocks even with no explicit write anywhere)', () => {
    const source = declareAndRead('const', 'fixed')
    const rPath = 'src/components/R.tsx'
    const reExport = `export { M } from './P'`
    const result = scan({ path: pPath, source, policy: POLICY, modules: { [pPath]: source, [rPath]: reExport } })
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported'), 're-exporting the declaring module hands the record arbitrarily far downstream and must block unconditionally')
  })

  it('keeps the read unsupported when no modules context is supplied at all (cannot rule out any importer of an exported record)', () => {
    const source = declareAndRead('const', 'fixed')
    const result = scan({ path: pPath, source, policy: POLICY })
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported'), 'a missing modules context cannot prove no importer mutates the export')
  })

  it('stays clean when the only other module that imports it by name only ever READS it (no write, no namespace import, no re-export)', () => {
    const source = declareAndRead('const', 'fixed')
    const q = `import { M } from './P'\nexport function readIt(){ return M.a }`
    const result = scan({ path: pPath, source, policy: POLICY, modules: { [pPath]: source, [qPath]: q } })
    assert.deepEqual(result, [], 'a read-only importer must not block resolution')
  })

  it('CONTROL: an UNEXPORTED same-file record needs no cross-module check at all, even with an unrelated other module present', () => {
    const source = `const M = { a: '${good}' }\nexport function V(){ return <i className={M.a}/> }`
    const q = `export function unrelated(){ return 1 }`
    const result = scan({ path: pPath, source, policy: POLICY, modules: { [pPath]: source, [qPath]: q } })
    assert.deepEqual(result, [], 'an unexported record cannot be imported anywhere, so no importer check applies')
  })
})

// CAP-D1/CAP-D2 (lead-review narrowing, ported from ts-colors.mjs's b57bd04a3
// -- see spacing.mjs's own comments ahead of computeRecordExportedUsesSafe):
// an unrelated non-JS asset, an unrelated unparseable JS/TS module that does
// not reference the origin, and an unrelated opaque dynamic import that
// structurally cannot target the origin must none of them poison an exported
// record's cross-module proof. Each "forbidden" pair pins that a genuinely
// relevant instance of the SAME shape still blocks, unchanged.
describe('exported record cross-module proof — CAP-D1/D2 narrowing (ported from ts-colors.mjs b57bd04a3)', () => {
  const good = 'p-[var(--space-2)]'
  const originPath = 'src/registry.ts'
  const originSource = `export const REGISTRY = { a: '${good}' }\n`
  const readerPath = 'src/components/Reader.tsx'
  const readerSource = "import { REGISTRY } from '../registry'\nexport function V({k}){ return <i className={REGISTRY[k]}/> }\n"

  function scanWith(extraModules) {
    return scan({
      path: readerPath,
      source: readerSource,
      policy: POLICY,
      modules: { [originPath]: originSource, [readerPath]: readerSource, ...extraModules },
    })
  }

  it('CONTROL: resolves cleanly with no unrelated modules present', () => {
    assert.deepEqual(scanWith({}), [])
  })

  it('does not block when an unrelated module fails to parse and never references the origin (permitted)', () => {
    const result = scanWith({
      'src/components/Broken.tsx': "export function Broken( { return <div className='p-[var(--space-2",
    })
    assert.deepEqual(result, [], 'a parse-error module irrelevant to the origin must not poison every other export\'s proof')
  })

  it('still blocks when an unrelated module fails to parse but DOES import the origin (forbidden)', () => {
    const result = scanWith({
      'src/components/BrokenImporter.tsx': "import { REGISTRY } from '../registry'\nexport function Broken( { REGISTRY.a = 'p-[7px]'",
    })
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported'), 'a parse-error module that imports the origin cannot be ruled irrelevant and must still block')
  })

  it('still blocks when an unrelated module fails to parse and its dynamic import()/require() argument is ambiguous (forbidden)', () => {
    const result = scanWith({
      'src/components/BrokenDynamic.tsx': "export function Broken( { return import(name",
    })
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported'), 'an ambiguous dynamic import inside an unparseable module stays conservative, unchanged')
  })

  it('does not block on an unrelated .svg module present anywhere in the modules context (permitted)', () => {
    const result = scanWith({
      'src/assets/logo.svg': '<?xml version="1.0" encoding="UTF-8"?>\n<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>',
    })
    assert.deepEqual(result, [], 'a non-JS asset can never contain import/export syntax and must not poison the proof')
  })

  it('does not block on an unrelated .css module present anywhere in the modules context (permitted)', () => {
    const result = scanWith({
      'src/styles/broken.css': '.foo { color: red; } @broken-at-rule {{{',
    })
    assert.deepEqual(result, [], 'a non-JS asset (even one that would fail to parse as CSS) must not poison the proof')
  })

  it('does not block on a directory-disjoint dynamic import template elsewhere (permitted)', () => {
    const result = scanWith({
      'src/lib/lazy.ts': 'export const load = (name) => import(`@/components/tools/${name}.tsx`)',
    })
    assert.deepEqual(result, [], 'a template head that structurally cannot resolve into the origin\'s directory must not poison the proof')
  })

  it('does not block on an npm-package dynamic import template elsewhere (permitted)', () => {
    const result = scanWith({
      'src/lib/lazy.ts': 'export const load = (mode) => import(`@codemirror/legacy-modes/mode/${mode}`)',
    })
    assert.deepEqual(result, [], 'a template head shaped like an npm package name can never grow into a local specifier and must not poison the proof')
  })

  it('still blocks on a dynamic import template sharing the origin directory (forbidden)', () => {
    const result = scanWith({
      'src/lib/lazy.ts': 'export const load = (mode) => import(`@/registry${mode}`)',
    })
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported'), 'a template head sharing the origin\'s directory could still target it and must block')
  })

  it('still blocks on a dynamic import with a bare variable specifier, unchanged (forbidden)', () => {
    const result = scanWith({
      'src/lib/lazy.ts': 'export const load = (moduleName) => import(moduleName)',
    })
    assert.ok(result.some((f) => f.ruleId === 'spacing/unsupported'), 'a non-template opaque dynamic import argument stays exactly as conservative as before')
  })
})

// Item 1 (lead-assigned wave-3 follow-up): renamed class-like parameter
// forwards (ported from typography.mjs::forwardedClassBoundary/
// parameterMemberBoundary/isClassLikeParameterName -- see
// forwardedClassLikeBoundary/parameterMemberBoundary's own header comments
// in spacing.mjs for exactly what was and was not ported). Fairness controls
// mirror dist/design-system-baseline/cli-lanes/fanout/COMMON-RULES.md.
describe('renamed class-like parameter forwards (ported from typography.mjs)', () => {
  const jsxPath = 'src/components/Widget.tsx'

  it('fairness control: the clean baseline (a registered spacing token, no class builder involved) gives no findings', () => {
    const result = scan({ path: jsxPath, source: "export const x = <p className=\"p-[var(--space-2)]\" />", policy: POLICY })
    assert.deepEqual(result, [])
  })

  it('fairness control: a plain off-scale violation written directly still gives a finding', () => {
    const result = scan({ path: jsxPath, source: "export const x = <p className=\"p-[7px]\" />", policy: POLICY })
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'))
  })

  it("PERMITTED: a class-like-suffix-named parameter (widthClass, sheet.tsx's real shape) forwarded unchanged becomes an extension-boundary, not unsupported", () => {
    const source = "export function SheetContent({ widthClass }) { return <div className={cn('base', widthClass)} /> }"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.ok(syntaxes(result, 'spacing/extension-boundary').includes('SheetContent#widthClass'))
  })

  it("PERMITTED: a `...ClassName`-suffix-named parameter (overlayClassName, dialog.tsx's real shape) forwarded unchanged becomes an extension-boundary", () => {
    const source = "const DialogContent = React.forwardRef(({ overlayClassName }, ref) => <Overlay className={overlayClassName} />)"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.ok(syntaxes(result, 'spacing/extension-boundary').includes('DialogContent#overlayClassName'))
  })

  it("PERMITTED: a render-prop's own renamed destructured className (calendar.tsx's Chevron shape) resolves through its render-prop owner, not unsupported", () => {
    const source = [
      "function Calendar({ className }) {",
      '  return <DayPicker components={{ Chevron: ({ className: chevronClassName }) => <Icon className={chevronClassName} /> }} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.ok(result.some((f) => f.ruleId === 'spacing/extension-boundary' && f.syntax.endsWith('#chevronClassName')))
  })

  it("PERMITTED: a bare `.map()` callback parameter's own .className member read (item.className, smart-select.tsx's real shape) becomes an extension-boundary attributed to the nearest named ancestor", () => {
    const source = "export function SmartSelect({ items }) { return <List>{items.map((item) => <Row key={item.value} className={item.className} />)}</List> }"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.ok(syntaxes(result, 'spacing/extension-boundary').includes('SmartSelect#item.className'))
  })

  it('FORBIDDEN: a TRANSFORMED forward (a method call on the parameter, not passed unchanged) stays unsupported', () => {
    const source = "export function SheetContent({ widthClass }) { return <div className={cn('base', widthClass.trim())} /> }"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: widthClass.trim()'))
  })

  it('FORBIDDEN: a REASSIGNED class-like parameter stays unsupported', () => {
    const source = "export function SheetContent({ widthClass }) { widthClass = 'extra'; return <div className={cn('base', widthClass)} /> }"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: widthClass'))
  })

  it('FORBIDDEN: an UNNAMABLE owner (an anonymous callback with no named ancestor at all, e.g. a bare top-level IIFE) stays unsupported', () => {
    const source = "(function ({ widthClass }) { return <div className={cn('base', widthClass)} /> })(props)"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: widthClass'))
  })

  it("FORBIDDEN: a `containerProps ?? {}` fallback destructured to a class-like name stays unsupported (table.tsx's containerClassName real shape -- body destructuring of a DIFFERENT parameter's nullish-coalesced value, not a direct parameter forward)", () => {
    const source = [
      "export function Table({ className, containerProps }) {",
      '  const { className: containerClassName } = containerProps ?? {}',
      "  return <div className={cn('relative', containerClassName)}><table className={className} /></div>",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: containerClassName'))
  })

  it('FORBIDDEN: a member read of a NON-className property carries no class-name signal and stays unsupported (parameterMemberBoundary is restricted to the literal className/class key)', () => {
    const source = "export function Row({ items }) { return items.map((item) => <span className={item.iconClass} />) }"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: item.iconClass'))
  })

  it('mutation proof: reverting isClassLikeParameterName to require an EXACT literal "className" match would make the widthClass PERMITTED case above fail closed again -- pinning it PERMITTED is the sentinel', () => {
    const source = "export function SheetContent({ widthClass }) { return <div className={cn('base', widthClass)} /> }"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.equal(syntaxes(result, 'spacing/unsupported').length, 0)
    assert.ok(syntaxes(result, 'spacing/extension-boundary').includes('SheetContent#widthClass'))
  })
})

// Item 1 (lane R2, remaining-47.tsv #16 -- table.tsx's REAL shape, distinct
// from the `containerProps ?? {}` fallback pinned unsupported just above):
// `({ className, containerProps = {} }, ref) => { const { className:
// containerClassName } = containerProps; ... }` -- the parameter DEFAULT
// lives on the outer destructure, and the body destructure's own source is a
// BARE identifier. Ported from typography.mjs::forwardedClassBoundary's body-
// destructuring branch (bodyDestructuredClassBoundary in spacing.mjs).
describe('body-destructured className read off a named destructured parameter element (ported from typography.mjs, table.tsx real shape)', () => {
  const jsxPath = 'src/components/ui/table.tsx'

  it('PERMITTED: containerProps.className is prefixed with its source parameter and does not collide with the same function\'s own direct className boundary', () => {
    const source = [
      'const Table = React.forwardRef(({ className, containerProps = {}, ...props }, ref) => {',
      "  const { className: containerClassName, onKeyDown, ...restContainerProps } = containerProps",
      '  return (',
      "    <div className={cn('relative w-full overflow-auto', containerClassName)}>",
      "      <table ref={ref} className={cn('w-full', className)} {...props} />",
      '    </div>',
      '  )',
      '})',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.deepEqual(syntaxes(result, 'spacing/extension-boundary').sort(), ['Table#className', 'Table#containerProps.className'])
  })

  it('PERMITTED (parity with typography.mjs): a body-destructured source bound as a PLAIN identifier parameter (not a destructured element) keeps the bare property name, no prefix', () => {
    const source = "export function Chip(props) { const { className } = props; return <div className={cn('p-[8px]', className)} /> }"
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.deepEqual(syntaxes(result, 'spacing/extension-boundary'), ['Chip#className'])
  })

  it('FORBIDDEN: mutating the source parameter\'s own className member before the read breaks unchanged provenance', () => {
    const source = [
      'function Table({ containerProps }) {',
      "  containerProps.className = 'text-[10px]'",
      '  const { className: containerClassName } = containerProps',
      "  return <div className={cn('relative', containerClassName)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: containerClassName'))
  })

  it('FORBIDDEN: a renamed alias of a NON-className source property carries no naming signal and stays unsupported', () => {
    const source = [
      'function Table({ containerProps }) {',
      '  const { widthHint: containerClassName } = containerProps',
      "  return <div className={cn('relative', containerClassName)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: containerClassName'))
  })

  it('mutation proof: dropping the destructuredParameterNames prefix distinction would collapse Table#containerProps.className back to Table#className, silently colliding with the outer direct forward -- pinning the two distinct strings is the sentinel', () => {
    const source = [
      'const Table = React.forwardRef(({ className, containerProps = {} }, ref) => {',
      '  const { className: containerClassName } = containerProps',
      "  return <div className={cn('relative', containerClassName)}><table className={className} /></div>",
      '})',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    const boundaries = syntaxes(result, 'spacing/extension-boundary')
    assert.equal(boundaries.length, 2)
    assert.notEqual(boundaries[0], boundaries[1])
  })
})

// Item 2 (lane R2, remaining-47.tsv #18-20 -- ChipListInput.tsx's `classes()`
// local variadic joiner). Ported from
// typography.mjs::transparentJoinerDeclaration; proven structurally, never
// trusted by name.
describe('transparent local variadic joiner (ported from typography.mjs, ChipListInput.tsx classes() style)', () => {
  const jsxPath = 'src/components/workspaces/ChipListInput.tsx'

  it("PERMITTED: a same-file `function name(...parts) { return parts.filter(Boolean).join(' ') }` is treated as a class builder, walking each argument", () => {
    const source = [
      "function classes(...parts) { return parts.filter(Boolean).join(' ') }",
      'export function Row({ rowClass }) {',
      "  return <div className={classes('p-[8px]', rowClass)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.deepEqual(syntaxes(result, 'spacing/extension-boundary'), ['Row#rowClass'])
  })

  it("PERMITTED: the .filter(Boolean) step is optional -- a bare .join() joiner is recognized too, and an off-scale literal argument is still checked", () => {
    const source = [
      "function classes(...parts) { return parts.join(' ') }",
      'export function Row() {',
      "  return <div className={classes('p-[7px]', 'shrink-0')} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), ['p-[7px]'])
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
  })

  it('FORBIDDEN: an extra statement in the joiner body is not a transparent builder and the whole call stays unsupported', () => {
    const source = [
      "function classes(...parts) { const cleaned = parts.filter(Boolean); return cleaned.join(' ') }",
      'export function Row({ rowClass }) {',
      "  return <div className={classes('p-[8px]', rowClass)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes("className: classes('p-[8px]', rowClass)"))
  })

  it('FORBIDDEN: joining a DIFFERENT receiver than the rest parameter is not a transparent joiner', () => {
    const source = [
      "function classes(...parts) { return OTHER.join(' ') }",
      'export function Row({ rowClass }) {',
      "  return <div className={classes('p-[8px]', rowClass)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes("className: classes('p-[8px]', rowClass)"))
  })

  it('mutation proof: requiring the callee to be a CLASS_BUILDERS name (not structural detection) would make the classes() PERMITTED case above fail closed -- pinning it PERMITTED is the sentinel', () => {
    const source = [
      "function classes(...parts) { return parts.filter(Boolean).join(' ') }",
      'export function Row({ rowClass }) {',
      "  return <div className={classes('p-[8px]', rowClass)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.equal(syntaxes(result, 'spacing/unsupported').length, 0)
  })
})

// Item 3a (lane R2, remaining-47.tsv #37 -- TaskDetailPanel.tsx's
// `STATUS_OPTIONS.filter(...).map((o) => o.color)`). Ported from
// typography.mjs::resolveArrayCallbackPropertyAccess.
describe('array-callback property read (ported from typography.mjs, TaskDetailPanel.tsx STATUS_OPTIONS.map((o) => o.color) style)', () => {
  const jsxPath = 'src/components/workspaces/TaskDetailPanel.tsx'

  it('PERMITTED: a finite, never-mutated local const array, filtered then mapped, resolves each element\'s property', () => {
    const source = [
      "const STATUS_OPTIONS = [{ value: 'inbox', color: 'p-[8px]' }, { value: 'next', color: 'p-[4px]' }]",
      'export function Row({ status }) {',
      '  return STATUS_OPTIONS.filter((o) => o.value === status).map((o) => <i key={o.value} className={o.color} />)',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), [])
  })

  it('PERMITTED: an off-scale value in one array element is still caught, not laundered by the array-callback proof', () => {
    const source = [
      "const STATUS_OPTIONS = [{ value: 'inbox', color: 'p-[7px]' }]",
      'export function Row() {',
      '  return STATUS_OPTIONS.map((o) => <i key={o.value} className={o.color} />)',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), ['p-[7px]'])
  })

  it('FORBIDDEN: the .map() callback parameter reassigned in its own body keeps the read unsupported', () => {
    const source = [
      "const STATUS_OPTIONS = [{ value: 'inbox', color: 'p-[8px]' }]",
      'export function Row() {',
      "  return STATUS_OPTIONS.map((o) => { o = { value: o.value, color: o.color }; return <i key={o.value} className={o.color} /> })",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: o.color'))
  })

  it('FORBIDDEN: o.color read from a closure NESTED one function deeper than the .map() callback itself stays unsupported', () => {
    const source = [
      "const STATUS_OPTIONS = [{ value: 'inbox', color: 'p-[8px]' }]",
      'export function Row() {',
      '  return STATUS_OPTIONS.map((o) => (() => <i key={o.value} className={o.color} />)())',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: o.color'))
  })

  it('mutation proof: dropping the .map()-callee-is-a-direct-argument check would let the nested-closure FORBIDDEN case above resolve -- pinning it FORBIDDEN is the sentinel', () => {
    const source = [
      "const STATUS_OPTIONS = [{ value: 'inbox', color: 'p-[8px]' }]",
      'export function Row() {',
      '  return STATUS_OPTIONS.map((o) => (() => <i key={o.value} className={o.color} />)())',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.equal(syntaxes(result, 'spacing/extension-boundary').length, 0)
  })
})

// Item 3b (lane R2, remaining-47.tsv #36 -- TaskDetailPanel.tsx's
// `PRIORITY_CONFIG[p]?.color`). Widens dynamicTailEmbedIsSafe so a dynamic
// key need not be the FINAL hop, only followed by a further STATIC path to
// the actual embedded value.
describe('dynamic-key record read followed by a further static hop (PRIORITY_CONFIG[p]?.color widening of dynamicTailEmbedIsSafe)', () => {
  const jsxPath = 'src/components/workspaces/TaskDetailPanel.tsx'

  it('PERMITTED: every branch\'s value at the static tail is a primitive leaf', () => {
    const source = [
      "const PRIORITY_CONFIG = { 1: { label: 'P1', color: 'p-[8px]' }, 2: { label: 'P2', color: 'p-[4px]' } }",
      'export function Row({ p }) {',
      "  return <i className={cn('text-xs', PRIORITY_CONFIG[p]?.color)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
  })

  it('PERMITTED: an off-scale value at the static tail of ONE branch is still caught, not laundered by the dynamic-key widening', () => {
    const source = [
      "const PRIORITY_CONFIG = { 1: { label: 'P1', color: 'p-[7px]' }, 2: { label: 'P2', color: 'p-[4px]' } }",
      'export function Row({ p }) {',
      "  return <i className={cn('text-xs', PRIORITY_CONFIG[p]?.color)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), ['p-[7px]'])
  })

  it('FORBIDDEN: a SECOND dynamic hop in the tail is not provable this way', () => {
    const source = [
      "const PRIORITY_CONFIG = { 1: { sub: { a: 'p-[8px]' } } }",
      'export function Row({ p, q }) {',
      "  return <i className={cn('text-xs', PRIORITY_CONFIG[p]?.sub[q])} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').some((s) => s.includes('PRIORITY_CONFIG')))
  })

  it('FORBIDDEN: a non-primitive branch value at the tail (a nested object, not a class string) stays unsupported', () => {
    const source = [
      "const PRIORITY_CONFIG = { 1: { color: { nested: true } }, 2: { color: 'p-[8px]' } }",
      'export function Row({ p }) {',
      "  return <i className={cn('text-xs', PRIORITY_CONFIG[p]?.color)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').some((s) => s.includes('PRIORITY_CONFIG')))
  })

  it('mutation proof: reverting to the original "dynamic key must be the FINAL hop" rule would make the PERMITTED tail case above fail closed again -- pinning it PERMITTED is the sentinel', () => {
    const source = [
      "const PRIORITY_CONFIG = { 1: { label: 'P1', color: 'p-[8px]' }, 2: { label: 'P2', color: 'p-[4px]' } }",
      'export function Row({ p }) {',
      "  return <i className={cn('text-xs', PRIORITY_CONFIG[p]?.color)} />",
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.equal(syntaxes(result, 'spacing/unsupported').length, 0)
  })
})

// Item 5 (lane R2, remaining-47.tsv #10 -- MessageItem.tsx's
// `avatarStyle(isUser, agent?.color)`). Style-helper counterpart of this
// file's own switch-based finite dispatcher: a top-level if-chain of
// single-statement returns.
describe('finite if-chain style dispatcher (MessageItem.tsx avatarStyle style)', () => {
  const jsxPath = 'src/components/chat/MessageItem.tsx'

  it('PERMITTED: every branch of a top-level if-chain returning a style object literal is walked, and non-spacing properties are silently skipped', () => {
    const source = [
      'function avatarStyle(isUser, agentColor) {',
      "  if (isUser) { return { backgroundColor: 'red', color: 'blue' } }",
      "  if (agentColor) { return { backgroundColor: agentColor, color: 'green' } }",
      "  return { backgroundColor: 'grey', color: 'black' }",
      '}',
      'export function Avatar({ isUser, agent }) {',
      '  return <div style={avatarStyle(isUser, agent?.color)} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(result, [])
  })

  it('PERMITTED: an off-scale SPACING property in one if-chain branch is still caught', () => {
    const source = [
      'function avatarStyle(isUser) {',
      "  if (isUser) { return { padding: '7px' } }",
      "  return { padding: '8px' }",
      '}',
      'export function Avatar({ isUser }) {',
      '  return <div style={avatarStyle(isUser)} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/off-scale').includes('padding: 7px'))
  })

  it('FORBIDDEN: an `else` branch is not the recognized shape and the call stays unsupported', () => {
    const source = [
      'function avatarStyle(isUser) {',
      "  if (isUser) { return { padding: '8px' } } else { return { padding: '4px' } }",
      '}',
      'export function Avatar({ isUser }) {',
      '  return <div style={avatarStyle(isUser)} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('style: avatarStyle(isUser)'))
  })

  it('FORBIDDEN: a NESTED (non-top-level) if-chain function stays unsupported', () => {
    const source = [
      'export function Avatar({ isUser }) {',
      '  function avatarStyle(u) {',
      "    if (u) { return { padding: '8px' } }",
      "    return { padding: '4px' }",
      '  }',
      '  return <div style={avatarStyle(isUser)} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('style: avatarStyle(isUser)'))
  })

  it('mutation proof: requiring a switch statement (the pre-existing dispatcher shape) instead of an if-chain would make the avatarStyle PERMITTED case above fail closed -- pinning it PERMITTED is the sentinel', () => {
    const source = [
      'function avatarStyle(isUser, agentColor) {',
      "  if (isUser) { return { backgroundColor: 'red' } }",
      "  if (agentColor) { return { backgroundColor: agentColor } }",
      "  return { backgroundColor: 'grey' }",
      '}',
      'export function Avatar({ isUser, agent }) {',
      '  return <div style={avatarStyle(isUser, agent?.color)} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.equal(syntaxes(result, 'spacing/unsupported').length, 0)
  })
})

// Item 6 (lane R2, remaining-47.tsv #12 -- KbMarkdownImage.tsx's `style={
// Object.keys(style).length > 0 ? style : undefined}`). A locally-declared,
// never-escaping style accumulator built via imperative property assignment
// rather than an object literal.
describe('locally-assigned style accumulator (KbMarkdownImage.tsx style={Object.keys(style).length > 0 ? style : undefined} style)', () => {
  const jsxPath = 'src/components/library/preview/KbMarkdownImage.tsx'

  it('PERMITTED: an empty-initialized const, assigned only non-spacing properties across conditional branches, reads back clean', () => {
    const source = [
      'function Picture({ widthHint, intrinsicless }) {',
      '  const style = {}',
      "  if (widthHint !== undefined) { style.width = `${widthHint}px`; style.maxWidth = '100%' }",
      "  if (intrinsicless) { style.width = `${widthHint ?? 320}px`; style.height = '240px'; style.objectFit = 'contain' }",
      '  return <img style={Object.keys(style).length > 0 ? style : undefined} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.deepEqual(result, [])
  })

  it('PERMITTED: an off-scale SPACING property assigned into the accumulator is still caught', () => {
    const source = [
      'function Picture({ cond }) {',
      '  const style = {}',
      "  if (cond) { style.padding = '7px' }",
      '  return <img style={Object.keys(style).length > 0 ? style : undefined} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/off-scale').includes('padding: 7px'))
  })

  it('FORBIDDEN: a non-empty initial object literal is not this proof\'s target and stays unsupported', () => {
    const source = [
      'function Picture({ cond }) {',
      "  const style = { display: 'block' }",
      "  if (cond) { style.padding = '8px' }",
      '  return <img style={Object.keys(style).length > 0 ? style : undefined} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('style: style'))
  })

  it('FORBIDDEN: the accumulator escaping into another function call stays unsupported', () => {
    const source = [
      'function Picture({ cond }) {',
      '  const style = {}',
      "  if (cond) { style.padding = '8px' }",
      '  merge(style)',
      '  return <img style={Object.keys(style).length > 0 ? style : undefined} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('style: style'))
  })

  it('mutation proof: dropping the Object.keys/values/entries read-only allowance would make the PERMITTED presence-check case above fail closed -- pinning it PERMITTED is the sentinel', () => {
    const source = [
      'function Picture({ widthHint }) {',
      '  const style = {}',
      "  if (widthHint !== undefined) { style.width = `${widthHint}px` }",
      '  return <img style={Object.keys(style).length > 0 ? style : undefined} />',
      '}',
    ].join('\n')
    const result = scan({ path: jsxPath, source, policy: POLICY })
    assert.equal(syntaxes(result, 'spacing/unsupported').length, 0)
  })
})

// Item 3 (lead-assigned wave-3 follow-up): a candidate set containing a
// proven null/undefined branch must not poison the whole resolution when the
// read is optional-chained (`x?.prop`); an unguarded read of the same chain
// stays unsupported. See memberAccessIsNullGuarded's own header comment in
// spacing.mjs for why this port is scoped to `?.` only, not the `x &&
// x.prop` truthiness-guard form also named in the task (a separate,
// unproven capability this pass deliberately does not ship).
describe('null branches: a null/undefined candidate contributes nothing to a guarded read', () => {
  const path = 'src/components/Widget.tsx'

  it('fairness control: the clean baseline (a registered spacing token, no class builder involved) gives no findings', () => {
    const result = scan({ path, source: "export const x = <p className=\"p-[var(--space-2)]\" />", policy: POLICY })
    assert.deepEqual(result, [])
  })

  it('fairness control: a plain off-scale violation written directly still gives a finding', () => {
    const result = scan({ path, source: "export const x = <p className=\"p-[7px]\" />", policy: POLICY })
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'))
  })

  it("PERMITTED: a ternary's null branch contributes nothing to an optional-chained read of the non-null branch", () => {
    const source = "export function V({flag}){ const cfg = flag ? { color: 'p-[7px]' } : null; return <i className={cn('base', cfg?.color)}/> }"
    const result = scan({ path, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), ['p-[7px]'])
  })

  it("PERMITTED: a ternary's undefined branch is treated identically to a null branch", () => {
    const source = "export function V({flag}){ const cfg = flag ? { color: 'p-[7px]' } : undefined; return <i className={cn('base', cfg?.color)}/> }"
    const result = scan({ path, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), ['p-[7px]'])
  })

  it('FORBIDDEN: the SAME null-branch chain read WITHOUT optional chaining stays unsupported (unguarded, could throw or read off null at runtime)', () => {
    const source = "export function V({flag}){ const cfg = flag ? { color: 'p-[7px]' } : null; return <i className={cn('base', cfg.color)}/> }"
    const result = scan({ path, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes('className: cfg.color'))
  })

  it('FORBIDDEN: a purely-null binding (no non-null branch at all) has nothing to resolve even when guarded -- stays unsupported, never crashes', () => {
    const source = "export function V(){ const cfg = null; return <i className={cn('base', cfg?.color)}/> }"
    const result = scan({ path, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').length > 0)
  })

  it('control: the pre-existing bare `undefined` class operand (unrelated to a proven-null record chain) is unaffected by this port', () => {
    const source = 'export const X = (code) => { eval(code); return <button className={undefined}/> }'
    const result = scan({ path, source, policy: POLICY })
    assert.deepEqual(result, [{ ruleId: 'spacing/unsupported', path, syntax: 'className: undefined', message: 'Unsupported spacing expression; dynamic or cyclic class aliases cannot be verified against the D10 scale.', line: 1, column: 68 }])
  })

  it('mutation proof: reverting memberAccessIsNullGuarded to always return false would make the optional-chained PERMITTED case above fail closed again -- pinning it PERMITTED is the sentinel', () => {
    const source = "export function V({flag}){ const cfg = flag ? { color: 'p-[7px]' } : null; return <i className={cn('base', cfg?.color)}/> }"
    const result = scan({ path, source, policy: POLICY })
    assert.equal(syntaxes(result, 'spacing/unsupported').length, 0)
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), ['p-[7px]'])
  })
})

// Item 4 (lead-assigned wave-3 follow-up): cva variant-function call-site
// resolution, ported from ts-colors.mjs's own cva-factory handling (see
// cvaFactoryDefinition/isCvaCallee's header comments in spacing.mjs).
// Fairness controls mirror
// dist/design-system-baseline/cli-lanes/fanout/COMMON-RULES.md.
describe('cva variant function call-site resolution (ported from ts-colors.mjs)', () => {
  const path = 'src/components/ui/badge.tsx'
  const good = "cva('p-[var(--space-2)]', { variants: { tone: { ok: 'p-[var(--space-1)]' } } })"
  const bad = "cva('p-[var(--space-2)]', { variants: { tone: { bad: 'p-[7px]' } } })"

  it('fairness control: the clean baseline (a registered spacing token, no class builder involved) gives no findings', () => {
    const result = scan({ path, source: "export const x = <p className=\"p-[var(--space-2)]\" />", policy: POLICY })
    assert.deepEqual(result, [])
  })

  it('fairness control: a plain off-scale violation written directly still gives a finding', () => {
    const result = scan({ path, source: "export const x = <p className=\"p-[7px]\" />", policy: POLICY })
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'))
  })

  it("PERMITTED: a badgeVariants({...}) call resolves through to the cva definition's own violation, not unsupported (badge.tsx's real shape)", () => {
    const source = `import { cva } from 'class-variance-authority'\nconst badgeVariants = ${bad}\nexport const A = () => <span className={badgeVariants({ tone: 'bad' })} />`
    const result = scan({ path, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.ok(syntaxes(result, 'spacing/off-scale').includes('p-[7px]'))
  })

  it('PERMITTED: an all-clean cva factory call resolves with no findings at all', () => {
    const source = `import { cva } from 'class-variance-authority'\nconst badgeVariants = ${good}\nexport const A = () => <span className={badgeVariants({ tone: 'ok' })} />`
    const result = scan({ path, source, policy: POLICY })
    assert.deepEqual(result, [])
  })

  it('PERMITTED: a renamed cva import is authenticated the same way as guardClassBuilder authenticates a renamed clsx/cn', () => {
    const source = `import { cva as variants } from 'class-variance-authority'\nconst badgeVariants = ${bad.replace('cva(', 'variants(')}\nexport const A = () => <span className={badgeVariants({ tone: 'bad' })} />`
    const result = scan({ path, source, policy: POLICY })
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
    assert.ok(syntaxes(result, 'spacing/off-scale').includes('p-[7px]'))
  })

  it("FORBIDDEN: a `let`-bound cva factory is not authenticated -- reassignment cannot be ruled out, stays unsupported", () => {
    const source = `import { cva } from 'class-variance-authority'\nlet badgeVariants = ${bad}\nexport const A = () => <span className={badgeVariants({ tone: 'bad' })} />`
    const result = scan({ path, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes("className: badgeVariants({ tone: 'bad' })"))
  })

  it('FORBIDDEN: a same-named local function that is NOT actually cva (no cva import at all) is not authenticated by name alone', () => {
    const source = `function cva() { return () => 'p-[7px]' }\nconst badgeVariants = cva()\nexport const A = () => <span className={badgeVariants({ tone: 'bad' })} />`
    const result = scan({ path, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').length > 0, 'a local function merely NAMED cva must still be authenticated, not trusted by name alone once bound to a variable')
  })

  it("FORBIDDEN: a RENAMED import from the WRONG package is not authenticated (control -- proves the renamed path checks the import specifier, not just the local alias name)", () => {
    const source = `import { cva as v } from 'not-the-real-cva-package'\nconst badgeVariants = v('p-[var(--space-2)]', { variants: { tone: { bad: 'p-[7px]' } } })\nexport const A = () => <span className={badgeVariants({ tone: 'bad' })} />`
    const result = scan({ path, source, policy: POLICY })
    assert.ok(syntaxes(result, 'spacing/unsupported').includes("className: badgeVariants({ tone: 'bad' })"))
  })

  it('mutation proof: reverting isCvaCallee to always return false would make the badge.tsx-shape PERMITTED case above fail closed again -- pinning it PERMITTED is the sentinel', () => {
    const source = `import { cva } from 'class-variance-authority'\nconst badgeVariants = ${bad}\nexport const A = () => <span className={badgeVariants({ tone: 'bad' })} />`
    const result = scan({ path, source, policy: POLICY })
    assert.equal(syntaxes(result, 'spacing/unsupported').length, 0)
    assert.ok(syntaxes(result, 'spacing/off-scale').includes('p-[7px]'))
  })
})

function syntaxes(findings, ruleId) {
  return findings.filter((finding) => finding.ruleId === ruleId).map((finding) => finding.syntax)
}

// FIX-P1 finding 1 (dist/design-system-baseline/cli-lanes/c1-prep/FIX-P1):
// visitNode/visitClassLike/directClassBuilderCallEmbed used to dispatch a
// CLASS_BUILDERS-named callee (cn/clsx/classNames/classnames/twMerge/twJoin/
// cva/cx/tv) purely by bare name, so a same-file lookalike sharing one of
// those 9 names was walked as though its ARGUMENTS were the class content --
// when its real, unproven behavior is its RETURN VALUE. Fairness controls
// mirror dist/design-system-baseline/cli-lanes/fanout/COMMON-RULES.md: the
// clean value alone gives [], the bad value written directly gives a finding.
describe('FIX-P1 finding 1: a CLASS_BUILDERS-named local lookalike is authenticated, not trusted by bare name', () => {
  const path = 'src/components/Lookalike.tsx'

  it('fairness control: the clean baseline (a registered spacing token, no class builder involved) gives no findings', () => {
    assert.deepEqual(findings(path, "export const x = <p className={'p-[var(--space-2)]'} />"), [])
  })

  it('fairness control: a plain off-scale violation written directly still gives a finding', () => {
    const result = findings(path, "export const x = <p className={'p-[7px]'} />")
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'))
  })

  it('FORBIDDEN: a local `cx` lookalike that ignores its arguments and returns a bad literal is caught through its RETURN VALUE (the exact false green reproduced against this lane)', () => {
    const source = "function cx(...a) { return 'p-[7px]' }\nexport const X = () => <div className={cx('safe')} />"
    const result = findings(path, source)
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'),
      'the lookalike\'s real returned literal must be caught, not hidden behind the bare CLASS_BUILDERS name')
    assert.deepEqual(result.filter((f) => f.ruleId === 'spacing/unsupported'), [])
  })

  it('FORBIDDEN: the same lookalike shape under a different CLASS_BUILDERS name (`tv`) is caught the same way', () => {
    const source = "function tv(...a) { return 'p-[13px]' }\nexport const X = () => <div className={tv('safe')} />"
    const result = findings(path, source)
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[13px]'))
  })

  it('FORBIDDEN: a local `clsx` lookalike returning a bad literal is caught, even though `clsx` is one of the 9 trusted names', () => {
    const source = "function clsx(...a) { return 'gap-[13px]' }\nexport const X = () => <div className={clsx('safe')} />"
    const result = findings(path, source)
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'gap-[13px]'))
  })

  it('PERMITTED: an undeclared, unimported bare `cx(...)` call (no local declaration to collide with) is still trusted by name, so its own arguments are checked directly (back-compat: real source can never reach a bare name with no import and no local declaration except the real ambient builder)', () => {
    const source = "export const X = () => <div className={cx('p-[7px]', 'shrink-0')} />"
    const result = findings(path, source)
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), ['p-[7px]'])
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
  })

  it('PERMITTED: a genuinely authenticated cn() wrapper declared AND used in the SAME file (no import at all) still authenticates and reports its real off-scale argument', () => {
    const source = "import { clsx } from 'clsx'\nimport { twMerge } from 'tailwind-merge'\nexport function cn(...inputs) { return twMerge(clsx(inputs)) }\nexport const X = () => <div className={cn('p-[7px]')} />"
    const result = findings(path, source)
    assert.deepEqual(syntaxes(result, 'spacing/off-scale'), ['p-[7px]'])
    assert.deepEqual(syntaxes(result, 'spacing/unsupported'), [])
  })

  it('mutation proof: reverting the dispatch condition to bare `CLASS_BUILDERS.has(name)` alone (dropping the hasLocalNameCollision gate and guardClassBuilder) would make the `cx` lookalike case above resolve to [] instead of the real off-scale finding -- pinning the finding non-empty is the sentinel', () => {
    const source = "function cx(...a) { return 'p-[7px]' }\nexport const X = () => <div className={cx('safe')} />"
    const result = findings(path, source)
    assert.ok(result.length > 0)
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'))
  })
})

// FIX-P1 finding 2: a MODULE-PRIVATE component's class-like prop, read back
// unchanged inside its own body, is registrable as an extension-boundary even
// when its one same-file call site's literal is never inspected. Fairness
// controls mirror dist/design-system-baseline/cli-lanes/fanout/COMMON-RULES.md.
describe('FIX-P1 finding 2: a private component whose class-like prop is always a same-file literal is inspected directly, not boundary-registered', () => {
  const path = 'src/components/PrivateBoundary.tsx'

  it('fairness control: the clean baseline (a registered spacing token, no class builder involved) gives no findings', () => {
    assert.deepEqual(findings(path, "export const x = <p className={'p-[var(--space-2)]'} />"), [])
  })

  it('fairness control: a plain off-scale violation written directly still gives a finding', () => {
    const result = findings(path, "export const x = <p className={'p-[7px]'} />")
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'))
  })

  it('FORBIDDEN: the exact reproduced shape -- a private Inner component whose only call site passes a literal boxClassName -- reports the literal directly and never registers Inner#boxClassName', () => {
    const source = [
      'function Inner({ boxClassName }) { return <div className={boxClassName} /> }',
      'export function W() { return <Inner boxClassName="p-[7px]" /> }',
    ].join('\n')
    const result = findings(path, source)
    assert.ok(result.some((f) => f.ruleId === 'spacing/off-scale' && f.syntax === 'p-[7px]'),
      'the single literal call-site value must be inspected as an ordinary spacing value')
    assert.deepEqual(syntaxes(result, 'spacing/extension-boundary'), [])
  })

  it('PERMITTED: the same shape stays boundary-registered when Inner is EXPORTED (an outside caller is possible)', () => {
    const source = [
      'export function Inner({ boxClassName }) { return <div className={boxClassName} /> }',
      'export function W() { return <Inner boxClassName="p-[7px]" /> }',
    ].join('\n')
    const result = findings(path, source)
    assert.ok(syntaxes(result, 'spacing/extension-boundary').includes('Inner#boxClassName'))
  })

  it('PERMITTED: the same shape stays boundary-registered when the one call site passes a DYNAMIC value', () => {
    const source = [
      'function Inner({ boxClassName }) { return <div className={boxClassName} /> }',
      "export function W({ dynamic }) { return <Inner boxClassName={dynamic} /> }",
    ].join('\n')
    const result = findings(path, source)
    assert.ok(syntaxes(result, 'spacing/extension-boundary').includes('Inner#boxClassName'))
  })

  it('PERMITTED: the same shape stays boundary-registered when ONE of two call sites is non-literal (a single dynamic site aborts the whole recovery)', () => {
    const source = [
      'function Inner({ boxClassName }) { return <div className={boxClassName} /> }',
      'export function A() { return <Inner boxClassName="p-[7px]" /> }',
      "export function B({ dynamic }) { return <Inner boxClassName={dynamic} /> }",
    ].join('\n')
    const result = findings(path, source)
    assert.ok(syntaxes(result, 'spacing/extension-boundary').includes('Inner#boxClassName'))
  })

  it('PERMITTED: the same shape stays boundary-registered when the destructured element is RENAMED (the outward-facing prop name callers use is not the local read-site name)', () => {
    const source = [
      'function Inner({ className: chevronClassName }) { return <div className={chevronClassName} /> }',
      'export function W() { return <Inner className="p-[7px]" /> }',
    ].join('\n')
    const result = findings(path, source)
    assert.ok(result.some((f) => f.ruleId === 'spacing/extension-boundary' && f.syntax.endsWith('#chevronClassName')))
  })

  it('PERMITTED: two literal call sites for the same private component both get inspected', () => {
    const source = [
      'function Inner({ boxClassName }) { return <div className={boxClassName} /> }',
      'export function A() { return <Inner boxClassName="p-[7px]" /> }',
      'export function B() { return <Inner boxClassName="gap-[13px]" /> }',
    ].join('\n')
    const result = findings(path, source)
    assert.deepEqual(syntaxes(result, 'spacing/extension-boundary'), [])
    assert.deepEqual(syntaxes(result, 'spacing/off-scale').sort(), ['gap-[13px]', 'p-[7px]'].sort())
  })

  it('mutation proof: reverting sameFileLiteralClassProp to always return null (i.e. never recovering) would make the FORBIDDEN case above register Inner#boxClassName and never see p-[7px] -- pinning both the off-scale finding and the empty boundary list is the sentinel', () => {
    const source = [
      'function Inner({ boxClassName }) { return <div className={boxClassName} /> }',
      'export function W() { return <Inner boxClassName="p-[7px]" /> }',
    ].join('\n')
    const result = findings(path, source)
    assert.deepEqual(syntaxes(result, 'spacing/extension-boundary'), [])
    assert.ok(syntaxes(result, 'spacing/off-scale').includes('p-[7px]'))
  })
})
