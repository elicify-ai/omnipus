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

  it('keeps cva factory results unsupported in both direct and aliased positions', () => {
    const source = `
      import { cva } from 'class-variance-authority'
      const badgeVariants = cva('p-[8px]', { variants: { tone: { bad: 'p-[13px]' } } })
      export const A = () => <span className={badgeVariants({ tone: 'bad' })} />
      export const B = () => { const v = badgeVariants({ tone: 'bad' }); return <span className={v} /> }
    `
    const result = scan({ path: 'src/x/badge.tsx', source, policy: POLICY, modules: { 'src/x/badge.tsx': source } })
    const unsupported = result.filter((finding) => finding.ruleId === 'spacing/unsupported').map((finding) => finding.syntax)
    assert.equal(unsupported.length, 2, `both cva shapes stay unsupported, got ${JSON.stringify(unsupported)}`)
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

  it('still reports the generic pass-through inputs inside the real wrapper module', () => {
    // Guardrail for group class-builder-infrastructure (owned by central
    // contract registration, not this analyzer capability): resolving aliased
    // calls must not clear the wrapper's own rest-parameter findings.
    const utilsSource = readFileSync(resolve(dirname(fileURLToPath(import.meta.url)), '../../src/lib/utils.ts'), 'utf8')
    const result = scan({ path: 'src/lib/utils.ts', source: utilsSource, policy: POLICY })
    const unsupported = result.filter((finding) => finding.ruleId === 'spacing/unsupported').map((finding) => finding.syntax)
    assert.ok(unsupported.includes('className: clsx(inputs)'), 'clsx(inputs) pass-through stays blocking')
    assert.ok(unsupported.includes('className: inputs'), 'rest-parameter input stays blocking')
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
