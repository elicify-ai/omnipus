import assert from 'node:assert/strict'
import test from 'node:test'
import { scan } from '../../scripts/design-system-locks/ts-colors.mjs'

// Plan: E1 and enforcement/contract.json require raw color findings across
// governed TS/JSX/SVG syntax, without path exemptions. The real scanner is the
// unit; no mocks. Raw CSS in style elements, embedded SVG paint and direct
// DOM/canvas paint assignments are negative equivalence classes. Registered
// tokens and ordinary URL fragments are clean controls; duplicate JSX paint
// occurrences must remain countable. Owner implementation is still active:
// these are RED reproductions, not acceptance or mutation-verified evidence.
// Once repaired/frozen, remove each detection branch separately to prove the
// style-tag, embedded-SVG and assignment cases fail, then restore the source.
const policy = { tokenCssNames: ['--color-primary'], resolvedTokens: {} }
const path = 'src/components/ColorAdversarial.tsx'

function paintFindings(source, scanPath = path) {
  return scan({ path: scanPath, source, policy }).map(({ ruleId, path: findingPath, syntax }) => ({
    ruleId,
    path: findingPath,
    syntax,
  }))
}

const forbidden = [
  ['CSS inside a JSX style element', "export const X = () => <style>{'.x { color: #ffffff }'}</style>"],
  ['SVG paint embedded in an inline background image', `export const X = () => <div style={{backgroundImage: "url(data:image/svg+xml,%3Csvg%20fill='%23ffffff'%3E%3C/svg%3E)"}} />`],
  ['a DOM style color assignment', "element.style.color = '#ffffff'"],
  ['a CSSOM property assignment', "element.style.setProperty('color', '#ffffff')"],
  ['canvas paint assignment', "context.fillStyle = '#ffffff'"],
]

for (const [name, source] of forbidden) {
  test(`E1 reports ${name}`, () => {
    const findings = scan({ path, source, policy })
    assert.deepEqual(
      findings.map(({ ruleId, path: findingPath, syntax }) => ({ ruleId, path: findingPath, syntax })),
      [{ ruleId: 'ts-colors/raw-color', path, syntax: '#ffffff' }],
      `${name}: E1 requires the raw colour itself to be reported`,
    )
    for (const finding of findings) {
      assert.match(finding.message, /\S/)
    }
  })
}

test('raw paint findings are invariant across governed feature, UI, story, and test paths', () => {
  const source = "element.style.color = '#ffffff'"
  for (const scanPath of [
    'src/features/ColorAdversarial.tsx',
    'src/components/ui/ColorAdversarial.tsx',
    'src/components/ui/ColorAdversarial.stories.tsx',
    'src/components/ui/ColorAdversarial.test.tsx',
  ]) {
    assert.deepEqual(
      paintFindings(source, scanPath),
      [{ ruleId: 'ts-colors/raw-color', path: scanPath, syntax: '#ffffff' }],
      `${scanPath}: raw scanners must leave boundary decisions to central enforcement`,
    )
  }
})

test('registered token style remains clean', () => {
  assert.deepEqual(scan({ path, source: "export const X = () => <div style={{ color: 'var(--color-primary)' }} />", policy }), [])
})

test('ordinary asset URL fragment remains clean', () => {
  assert.deepEqual(scan({ path, source: "export const X = () => <div style={{ backgroundImage: 'url(/asset.svg#ffffff)' }} />", policy }), [])
})

test('CSSOM non-colour properties and ordinary strings containing hex remain clean', () => {
  const source = [
    "element.style.setProperty('width', '#ffffff')",
    "export const label = '#ffffff'",
  ].join('\n')
  assert.deepEqual(scan({ path, source, policy }), [])
})

test('two distinct raw JSX paints retain two occurrences of the same fingerprint syntax', () => {
  const finding = { ruleId: 'ts-colors/raw-color', path, syntax: '#ffffff' }
  assert.deepEqual(
    paintFindings('export const X = () => <svg><path fill="#ffffff"/><path fill="#ffffff"/></svg>'),
    [finding, finding],
  )
})

test('a parsed dynamic member at an exact class sink is blocking governed debt with stable identity', () => {
  const source = `export function Chip({ config }) { return <i className={config.textClass}/> }`
  const formatted = `export function Chip({ config }) { return <i className={config /* trivia */ . textClass}/> }`
  const changedExpression = `export function Chip({ config }) { return <i className={config.dotClass}/> }`
  const changedSymbol = `export function Badge({ config }) { return <i className={config.textClass}/> }`
  const syntax = (text) => paintFindings(text).filter(({ ruleId }) => ruleId === 'ts-colors/unverified-governed-value').map((finding) => finding.syntax)

  assert.deepEqual(syntax(source), ['Chip#className<-config.textClass'])
  assert.deepEqual(syntax(formatted), ['Chip#className<-config.textClass'])
  assert.deepEqual(syntax(changedExpression), ['Chip#className<-config.dotClass'])
  assert.deepEqual(syntax(changedSymbol), ['Badge#className<-config.textClass'])
})

test('static definitions resolve before an unverified governed class finding is considered', () => {
  const source = `import { config } from '@/config'; export function Chip() { return <i className={config.textClass}/> }`
  const modules = {
    [path]: source,
    'src/config.ts': `export const config = { textClass: 'text-red-500' }`,
  }
  const findings = scan({ path, source, policy, modules })
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/raw-color' && syntax === 'text-red-500'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unverified-governed-value'))
})

test('opaque calls, missing modules and cyclic imports stay unsupported at class sinks', () => {
  const opaque = `export function Chip({ state }) { return <i className={makeClasses(state)}/> }`
  assert.ok(paintFindings(opaque).some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
  assert.ok(!paintFindings(opaque).some(({ ruleId }) => ruleId === 'ts-colors/unverified-governed-value'))

  const missing = `import { config } from '@/missing'; export function Chip() { return <i className={config.textClass}/> }`
  const missingFindings = scan({ path, source: missing, policy, modules: { [path]: missing } })
  assert.ok(missingFindings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
  assert.ok(!missingFindings.some(({ ruleId }) => ruleId === 'ts-colors/unverified-governed-value'))

  const cyclic = `import { config } from '@/a'; export function Chip() { return <i className={config.textClass}/> }`
  const modules = {
    [path]: cyclic,
    'src/a.ts': `import { config } from './b'; export { config }`,
    'src/b.ts': `import { config } from './a'; export { config }`,
  }
  const cyclicFindings = scan({ path, source: cyclic, policy, modules })
  assert.ok(cyclicFindings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
  assert.ok(!cyclicFindings.some(({ ruleId }) => ruleId === 'ts-colors/unverified-governed-value'))
})

test('raw fallbacks and unknown CSSOM properties remain independently visible', () => {
  const fallback = `export function Avatar({ data }) { return <i style={{ color: data.color ?? '#ffffff' }}/> }`
  const fallbackFindings = paintFindings(fallback)
  assert.ok(fallbackFindings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/raw-color' && syntax === '#ffffff'))
  assert.ok(fallbackFindings.some(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'))

  const dynamicProperty = `element.style.setProperty(property, '#ffffff')`
  const dynamicFindings = paintFindings(dynamicProperty)
  assert.ok(dynamicFindings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'property'))
  assert.ok(dynamicFindings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/raw-color' && syntax === '#ffffff'))
})

test('whole style forwarding and transformed paint values are coverage gaps rather than baselineable debt', () => {
  const style = `export function Card({ style }) { return <i style={style}/> }`
  const transformed = `export function Card({ data }) { return <i style={{ color: tint(data.color) }}/> }`
  for (const source of [style, transformed]) {
    const findings = paintFindings(source)
    assert.ok(findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
    assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unverified-governed-value'))
  }
})

test('proven boolean operands and schema objects are non-paint while unknown selectors remain unsupported', () => {
  const boolean = `export function Chip({ active }: { active: boolean }) { return <i className={cn(active, active && 'text-[var(--color-primary)]')}/> }`
  assert.deepEqual(paintFindings(boolean), [])

  const unknown = `export function Chip() { const active = useStore(s => s.active); return <i className={cn(active, 'text-[var(--color-primary)]')}/> }`
  assert.ok(paintFindings(unknown).some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))

  const schema = `const VaultFilterNode = z.lazy(() => z.object({ color: z.string() })); export const View = z.object({ filter: VaultFilterNode.optional() })`
  assert.deepEqual(paintFindings(schema), [])
})

test('proven numeric calculations and numeric custom-property serialization are non-paint', () => {
  const source = `
    function contrastRatio(first: string, second: string): number {
      const light = Math.max(luminance(first), luminance(second))
      const dark = Math.min(luminance(first), luminance(second))
      return (light + 0.05) / (dark + 0.05)
    }
    export function Preview({ scale }: { scale: number }) {
      element.style.setProperty('--scale-factor', String(scale))
      return { border: contrastRatio('#000000', '#ffffff') }
    }
  `
  const findings = paintFindings(source)
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/unsupported'), [])
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/raw-color').map(({ syntax }) => syntax), ['#000000', '#ffffff'])
})

test('finite readonly array selections scan every colour branch before class debt classification', () => {
  const source = `const tones = ['text-[var(--color-primary)]', 'text-red-500'] as const; export function Chip({ index }: { index: number }) { return <i className={tones[index]}/> }`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/raw-color' && syntax === 'text-red-500'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported' || ruleId === 'ts-colors/unverified-governed-value'))
})

test('optional member access over a finite object selection scans every branch', () => {
  const source = `const tones = { ok: { textClass: 'text-[var(--color-primary)]' }, bad: { textClass: 'text-red-500' } } as const; export function Chip({ tone }) { const config = tones[tone]; return <i className={config?.textClass}/> }`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/raw-color' && syntax === 'text-red-500'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
})

test('annotated member booleans are clean only as guards while unknown members fail closed', () => {
  const typed = `export function Chip(props: { active: boolean }) { return <i className={props.active && 'text-[var(--color-primary)]'}/> }`
  assert.deepEqual(paintFindings(typed), [])
  const unknown = `export function Chip(props) { return <i className={props.active && 'text-[var(--color-primary)]'}/> }`
  assert.ok(paintFindings(unknown).some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
})

test('locally derived numeric geometry custom properties are clean but colour transforms remain unsupported', () => {
  const source = `export function apply(top: number, color: string) { const appTop = \`${'${Math.max(0, top)}'}px\`; element.style.setProperty('--app-top', appTop); element.style.backgroundColor = \`${'${color}'}22\` }`
  const findings = paintFindings(source)
  assert.ok(!findings.some(({ syntax }) => syntax === 'appTop'))
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax.includes('color')))
})

test('IIFE class configurations scan every returned branch and boolean field', () => {
  const source = `export function Chip({ state }) { const config = (() => { if (state) return { textClass: 'text-red-500', pulse: true }; return { textClass: 'text-[var(--color-primary)]', pulse: false } })(); return <i className={cn(config.textClass, config.pulse && 'animate-pulse')}/> }`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/raw-color' && syntax === 'text-red-500'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
})

test('nullish fallback over a finite object lookup scans every class branch', () => {
  const source = `const badges = { 1: { className: 'text-red-500' }, 2: { className: 'text-[var(--color-primary)]' } } as const; export function Badge({ priority }) { const badge = badges[priority] ?? badges[2]; return <i className={badge.className}/> }`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/raw-color' && syntax === 'text-red-500'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
})

test('named interfaces and forwardRef generic intersections prove boolean guards', () => {
  const named = `interface Props { active?: boolean }; export function Chip(props: Props) { return <i className={props.active && 'text-[var(--color-primary)]'}/> }`
  assert.deepEqual(paintFindings(named), [])
  const forwarded = `const Item = React.forwardRef<HTMLElement, React.HTMLAttributes<HTMLElement> & { inset?: boolean }>(({ inset }, ref) => <i ref={ref} className={inset && 'pl-8'}/>)`
  assert.deepEqual(paintFindings(forwarded), [])
})

test('unknown named types, lexical shadows and opaque helper results remain unsupported', () => {
  const missing = `export function Chip(props: MissingProps) { return <i className={props.active && 'text-[var(--color-primary)]'}/> }`
  const shadow = `interface Props { active: boolean }; export function Chip(props: Props) { function inner(props) { return <i className={props.active && 'text-[var(--color-primary)]'}/> }; return inner(props) }`
  const opaque = `export function Chip(state) { const config = makeConfig(state); return <i className={config.textClass}/> }`
  for (const source of [missing, shadow, opaque]) assert.ok(paintFindings(source).some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
})

// Numeric named-type proof: only required, explicit numeric fields in unique local
// declarations can remove unsupported debt. Surrounding paint remains governed.
test('named numeric fields prove gradient arithmetic without hiding surrounding paint', () => {
  const gradient = '`linear-gradient(var(--color-primary) ${((value - min) / (max - min)) * 100}%, var(--color-primary))`'
  const declarations = [
    ['','{ value: number; min: number; max: number }'],
    ['interface Props { value: number; min: number; max: number }','Props'],
    ['type Base = { value: number; min: number; max: number }; type Props = Base','Props'],
  ]
  for (const [declaration, type] of declarations) {
    const source = `${declaration}; function Range({ value, min, max }: ${type}) { return <i style={{ background: ${gradient} }}/> }`
    assert.deepEqual(paintFindings(source), [], type)
    assert.deepEqual(paintFindings(source.replaceAll('var(--color-primary)', '#ff0000')).map(f => f.ruleId), ['ts-colors/raw-color', 'ts-colors/raw-color'])
    assert.deepEqual(paintFindings(source.replaceAll('--color-primary', '--missing')).map(f => f.ruleId), ['ts-colors/undefined-token', 'ts-colors/undefined-token'])
  }
  assert.deepEqual(paintFindings('interface Props { amount: number }; function Range({ amount: value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> }'), [])
})

test('named numeric proof rejects unknown, optional, cyclic, ambiguous and shadowed types', () => {
  const cases = [
    'interface Props { value: string }',
    'interface Props { value: unknown }',
    'interface Props { value?: number }',
    'type Props = { value: number } | { value: string }',
    'type Props = Other; type Other = Props',
    'interface Props { value: number }; interface Props { value: string }',
    'import type { Props } from "./missing"',
  ]
  for (const declaration of cases) {
    const findings = paintFindings(`${declaration}; function Range({ value }: Props) { return <i style={{ background: \`linear-gradient(var(--color-primary) \${value}%, transparent)\` }}/> }`)
    assert.deepEqual(findings.map(f => f.ruleId), ['ts-colors/unsupported'], declaration)
  }
  for (const source of [
    'interface Props { value: number }; function Range({ value }: Props) { value = getUnknown(); return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> }',
    'interface Props { value: number }; function Range<Props>({ value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> }',
    'interface Props { value: number }; function outer() { type Props = { value: string }; function Range({ value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> } }',
    'interface Props { value: number }; function Range({ value }: Props) { function inner(value) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> }; return inner(value) }',
    'interface Props { value: number }; function Range({ nested: { value } }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> }',
  ]) assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported').length >= 1, true, source)
})


test('namespace-local types cannot inherit a numeric proof from the file scope', () => {
  const source = 'interface Props { value: number }; namespace Inner { interface Props { value: string }; export function Range({ value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> } }'
  assert.deepEqual(paintFindings(source), [{
    ruleId: 'ts-colors/unsupported', path,
    syntax: '`linear-gradient(var(--color-primary) ${value}%, transparent)`',
  }])
})

test('object rest bindings cannot be mistaken for a numeric named property', () => {
  const source = 'interface Props { value: number }; function Range({ ...value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> }'
  assert.deepEqual(paintFindings(source), [{
    ruleId: 'ts-colors/unsupported', path,
    syntax: '`linear-gradient(var(--color-primary) ${value}%, transparent)`',
  }])
})


for (const [scope, source] of [
  ['switch', 'interface Props { value: number }; switch(flag) { case true: type Props = { value: string }; function Range({ value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> } }'],
  ['block-local class', 'interface Props { value: number }; function outer() { class Props { value: string }; function Range({ value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> } }'],
  ['named class expression', 'interface Props { value: number }; const Outer = class Props { value: string; render({ value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> } }'],
]) test(`named numeric proof rejects ${scope} lexical scope`, () => {
  assert.deepEqual(paintFindings(source), [{
    ruleId: 'ts-colors/unsupported', path,
    syntax: '`linear-gradient(var(--color-primary) ${value}%, transparent)`',
  }])
})


// Absence is a closed-world fact about fresh finite object returns, never an
// inference from an optional type annotation or a partially resolved branch.
test('absent class properties on complete fresh factory returns are non-paint', () => {
  const factory = "function configFor(flag) { if (flag) return { __proto__: null, label: 'yes' }; return flag ? { __proto__: null, indicator: 'a' } : { __proto__: null, label: 'no' } }"
  for (const init of ['configFor(flag)', "flag ? configFor(flag) : { __proto__: null, label: 'other' }"]) {
    assert.deepEqual(paintFindings(`${factory}; function View({ flag }) { const config = ${init}; return <i className={config.textClass}/> }`), [])
  }
  const source = "import { configFor as factory } from '@/config'; function View({ flag }) { const config = factory(flag); return <i className={config.textClass}/> }"
  const modules = { [path]: source, 'src/config.ts': `export ${factory}` }
  assert.deepEqual(scan({ path, source, modules, policy }), [])
})

test('adding a raw class to any returned branch remains a colour violation', () => {
  const source = "function factory(flag) { if (flag) return { __proto__: null, label: 'yes' }; return { textClass: 'text-red-500' } }; function View({ flag }) { const config = factory(flag); return <i className={config.textClass}/> }"
  assert.deepEqual(paintFindings(source).map(f => [f.ruleId, f.syntax]), [['ts-colors/raw-color', 'text-red-500']])
})

for (const [label, factory] of [
  ['opaque return', "function factory(flag) { if (flag) return { __proto__: null, label: 'yes' }; return opaque() }"],
  ['opaque conditional branch', "function factory(flag) { return flag ? { __proto__: null, label: 'yes' } : opaque() }"],
  ['spread', "function factory() { return { __proto__: null, ...external, label: 'yes' } }"],
  ['computed key', "function factory() { return { [key]: 'text-red-500' } }"],
  ['getter', "function factory() { return { get label() { this.textClass = 'text-red-500'; return 'x' } } }"],
  ['method', "function factory() { return { update() { this.textClass = 'text-red-500' } } }"],
  ['prototype setter', "function factory() { return { __proto__: external, label: 'yes' } }"],
  ['aliased return', "function factory() { const result = { __proto__: null, label: 'yes' }; mutate(result); return result }"],
  ['recursive return', "function factory() { return factory() }"],
]) test(`absent class proof rejects ${label}`, () => {
  const source = `${factory}; function View({ flag }) { const config = factory(flag); return <i className={config.textClass}/> }`
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})

for (const [label, alteration] of [
  ['property write', "config.textClass = getClass();"],
  ['computed write', "config[key] = getClass();"],
  ['assign escape', 'Object.assign(config, external);'],
  ['alias escape', 'const alias = config; mutate(alias);'],
  ['callback escape', 'mutate(config);'],
  ['reassignment', 'config = opaque();'],
  ['object destructuring write', '({ value: config.textClass } = external);'],
  ['array destructuring write', '[config.textClass] = external;'],
  ['loop assignment', 'for (config.textClass of external) {}'],
]) test(`absent class proof rejects receiver ${label}`, () => {
  const source = `function factory() { return { __proto__: null, label: 'yes' } }; function View() { const config = factory(); ${alteration} return <i className={config.textClass}/> }`
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})

test('absent class proof rejects missing imports, cycles and lexical helper shadows', () => {
  const source = "import { factory } from '@/config'; function View() { const config = factory(); return <i className={config.textClass}/> }"
  for (const modules of [
    { [path]: source },
    { [path]: source, 'src/config.ts': "import { factory } from './other'; export { factory }", 'src/other.ts': "import { factory } from './config'; export { factory }" },
    { [path]: source, 'src/config.ts': "function factory() { return { __proto__: null, label: 'private' } }" },
  ]) assert.equal(scan({ path, source, modules, policy }).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
  const shadow = "function factory() { return { __proto__: null, label: 'yes' } }; function View({ factory }) { const config = factory(); return <i className={config.textClass}/> }"
  assert.equal(paintFindings(shadow).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})


for (const branch of ['opaque()', 'factory(flag)']) test(`known governed classes do not hide unresolved ${branch} returns`, () => {
  const source = `function factory(flag) { if (flag) return { textClass: 'text-[var(--color-primary)]' }; return ${branch} }; function View({ flag }) { const config = factory(flag); return <i className={config.textClass}/> }`
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
  const raw = source.replace('text-[var(--color-primary)]', 'text-red-500')
  assert.deepEqual(paintFindings(raw).map(f => [f.ruleId, f.syntax]), [
    ['ts-colors/raw-color', 'text-red-500'], ['ts-colors/unsupported', 'config.textClass'],
  ])
})

for (const mutation of [
  'Object.prototype.textClass = external;',
  "Object.defineProperty(Object.prototype, 'textClass', { get() { return external } });",
  "const builtin = Object; const base = builtin.prototype; base.textClass = external;",
]) test(`ordinary object absence remains unsupported with prototype mutation: ${mutation}`, () => {
  const source = `${mutation} function factory() { return { label: 'x' } }; function View() { const config = factory(); return <i className={config.textClass}/> }`
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})

test('ordinary object absence remains unsupported across imported prototype mutation', () => {
  const source = "import '@/mutate'; function factory() { return { label: 'x' } }; function View() { const config = factory(); return <i className={config.textClass}/> }"
  const modules = { [path]: source, 'src/mutate.ts': 'const base = Object.prototype; base.textClass = external' }
  assert.equal(scan({ path, source, modules, policy }).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})


for (const object of [
  "{ textClass: 'text-[var(--color-primary)]', ...external }",
  "{ textClass: 'text-[var(--color-primary)]', [key]: external }",
  "{ textClass: 'text-[var(--color-primary)]', get label() { this.textClass = external; return 'x' } }",
]) test(`known class targets retain unsupported object overrides: ${object}`, () => {
  const source = `function factory() { return ${object} }; function View() { const config = factory(); return <i className={config.textClass}/> }`
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})


test('ordinary own-property absence is unsupported even without visible prototype writes', () => {
  const source = "function factory() { return { label: 'x' } }; function View() { const config = factory(); return <i className={config.textClass}/> }"
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})

test('explicit null-prototype results prove absence independently of imported prototype mutations', () => {
  const source = "import '@/mutate'; function factory() { return { __proto__: null, label: 'x' } }; function View() { const config = factory(); return <i className={config.textClass}/> }"
  const modules = { [path]: source, 'src/mutate.ts': "Object.defineProperty(Object.prototype, 'textClass', { get() { return external } })" }
  assert.deepEqual(scan({ path, source, modules, policy }), [])
})


for (const [label, alteration] of [
  ['property assignment', 'config.textClass = external;'],
  ['dynamic assignment', 'config[key] = external;'],
  ['alias mutation', 'const alias = config; alias.textClass = external;'],
  ['opaque escape', 'mutate(config);'],
  ['reflective write', "Object.defineProperty(config, 'textClass', { value: external });"],
  ['destructuring write', '({ value: config.textClass } = external);'],
  ['loop write', 'for (config.textClass of external) {}'],
]) test(`known member colour targets retain unsupported receiver ${label}`, () => {
  const source = `function factory() { return { textClass: 'text-[var(--color-primary)]' } }; function View() { const config = factory(); ${alteration} return <i className={config.textClass}/> }`
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
  const raw = source.replace('text-[var(--color-primary)]', 'text-red-500')
  const findings = paintFindings(raw)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color' && f.syntax === 'text-red-500').length, 1)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})

test('known member targets reject mutable bindings even when their initializer is finite', () => {
  for (const assignment of ['', 'config = external;']) {
    const source = `function View() { let config = { textClass: 'text-[var(--color-primary)]' }; ${assignment} return <i className={config.textClass}/> }`
    assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
  }
})


for (const alteration of [
  'const alias = tones["good"]; alias.textClass = external;',
  'mutate(tones["good"]);',
  'const alias = tones["good"]; const other = alias; other.textClass = external;',
]) test(`selected configuration aliases cannot invalidate known class targets silently: ${alteration}`, () => {
  const source = `const tones = { good: { textClass: 'text-[var(--color-primary)]' } }; function View() { ${alteration} const config = tones["good"]; return <i className={config.textClass}/> }`
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})


test('known imported class targets reject writes through another module alias', () => {
  const source = "import { config } from '@/config'; function View() { return <i className={config.textClass}/> }"
  const modules = {
    [path]: source,
    'src/config.ts': "export const config = { textClass: 'text-[var(--color-primary)]' }",
    'src/mutate.ts': "import { config as alias } from '@/config'; alias.textClass = external",
  }
  assert.equal(scan({ path, source, modules, policy }).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})


for (const access of [
  "import * as namespace from '@/config'; mutate(namespace)",
  "export { config as exposed } from '@/config'",
  "import(moduleName).then(mutate)",
]) test(`known exported class objects reject unproven module access: ${access}`, () => {
  const source = "import { config } from '@/config'; function View() { return <i className={config.textClass}/> }"
  const modules = {
    [path]: source,
    'src/config.ts': "export const config = { textClass: 'text-[var(--color-primary)]' }",
    'src/access.ts': access,
  }
  assert.equal(scan({ path, source, modules, policy }).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})


test('async factories cannot establish stable synchronous member targets', () => {
  const source = "function View() { const config = (async () => ({ textClass: 'text-[var(--color-primary)]' }))(); return <i className={config.textClass}/> }"
  assert.equal(paintFindings(source).filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass').length, 1)
})


for (const alteration of ['shared.textClass = external;', 'mutate(shared);']) test(`borrowed nested configurations retain unsupported stability after ${alteration}`, () => {
  const source = `const shared = { textClass: 'text-[var(--color-primary)]' }; const config = { nested: shared }; ${alteration} function View() { return <i className={config["nested"].textClass}/> }`
  const findings = paintFindings(source)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config["nested"].textClass').length, 1)
  const raw = paintFindings(source.replace('text-[var(--color-primary)]', 'text-red-500'))
  assert.equal(raw.filter(f => f.ruleId === 'ts-colors/raw-color' && f.syntax === 'text-red-500').length, 1)
  assert.equal(raw.filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config["nested"].textClass').length, 1)
})


test('nested inline literal configuration remains provably stable', () => {
  const source = 'const config = { nested: { textClass: "text-[var(--color-primary)]" } }; function View() { return <i className={config["nested"].textClass}/> }'
  assert.deepEqual(paintFindings(source), [])
})

test('nested dot access preserves a clean governed class', () => {
  const source = 'const config = { nested: { textClass: "text-[var(--color-primary)]" } }; function View() { return <i className={config.nested.textClass}/> }'
  assert.deepEqual(paintFindings(source), [])
})

test('nested dot access reports known raw colour beside borrowed-object uncertainty', () => {
  const source = "const shared = { textClass: 'text-red-500' }; const config = { nested: shared }; shared.textClass = external; function View() { return <i className={config.nested.textClass}/> }"
  assert.deepEqual(paintFindings(source), [
    { ruleId: 'ts-colors/raw-color', path, syntax: 'text-red-500' },
    { ruleId: 'ts-colors/unsupported', path, syntax: 'config.nested.textClass' },
  ])
})

for (const init of [
  'flag ? { textClass: "text-[var(--color-primary)]" } : shared',
  'shared',
]) test(`every selected nested factory branch must retain borrowed-object stability: ${init}`, () => {
  const source = `const shared = { textClass: 'text-red-500' }; function factory() { return { nested: ${init} } }; const config = factory(); mutate(shared); function View() { return <i className={config["nested"].textClass}/> }`
  const findings = paintFindings(source)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config["nested"].textClass').length, 1)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color' && f.syntax === 'text-red-500').length, 1)
})


test('selected nested values require stability in every returned object', () => {
  const source = 'const shared = { textClass: "text-red-500" }; function factory(flag) { if (flag) return { nested: { textClass: "text-[var(--color-primary)]" } }; return { nested: shared } }; const config = factory(flag); mutate(shared); function View() { return <i className={config["nested"].textClass}/> }'
  const findings = paintFindings(source)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config["nested"].textClass').length, 1)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color' && f.syntax === 'text-red-500').length, 1)
})

test('stable nested literals do not excuse replacement of their containing property', () => {
  const source = 'const config = { nested: { textClass: "text-red-500" } }; config["nested"] = external; function View() { return <i className={config["nested"].textClass}/> }'
  const findings = paintFindings(source)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config["nested"].textClass').length, 1)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color' && f.syntax === 'text-red-500').length, 1)
})


// Finite helper-local class returns (ToolPolicyEditor BULK_BUTTON_CLASS shape).
// A component- or module-local helper whose guarded returns are whole-value
// reads of unwritten const string literals must resolve those literals at the
// call site; the literals' classes then carry the findings. Every write-shaped
// use, non-literal initializer, shadowing declaration or borrowed value keeps
// the return unsupported. RED reproductions against the live scanner.
const helperPolicy = { tokenCssNames: ['--color-border', '--color-muted', '--color-secondary'], resolvedTokens: {} }

const bulkSource = `export function ServerPanel({ isActive, policy }) {
  const BULK_BUTTON_CLASS = (active, pol) => {
    const baseInactive = 'border-[var(--color-border)] text-[var(--color-muted)] hover:text-[var(--color-secondary)]'
    const activeAllow = 'bg-emerald-500/20 text-emerald-400 border-emerald-500/40'
    const activeAsk   = 'bg-amber-500/20 text-amber-400 border-amber-500/40'
    const activeDeny  = 'bg-red-500/20 text-red-400 border-red-500/40'
    if (!active) return baseInactive
    if (pol === 'allow') return activeAllow
    if (pol === 'ask')   return activeAsk
    return activeDeny
  }
  return <button className={\`px-2 rounded \${BULK_BUTTON_CLASS(isActive, policy)}\`}>set</button>
}`

test('finite helper-local guarded class returns resolve to their whole literal values', () => {
  const findings = scan({ path, source: bulkSource, policy: helperPolicy })
  const summarized = findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort()
  assert.deepEqual(summarized, [
    'ts-colors/raw-color|bg-amber-500/20',
    'ts-colors/raw-color|bg-emerald-500/20',
    'ts-colors/raw-color|bg-red-500/20',
    'ts-colors/raw-color|border-amber-500/40',
    'ts-colors/raw-color|border-emerald-500/40',
    'ts-colors/raw-color|border-red-500/40',
    'ts-colors/raw-color|text-amber-400',
    'ts-colors/raw-color|text-emerald-400',
    'ts-colors/raw-color|text-red-400',
  ])
})

test('module-level helpers with local literal consts resolve the same way', () => {
  const source = `function toneClass(denied) {
  const calm = 'text-[var(--color-muted)]'
  const alarm = 'text-red-500'
  return denied ? alarm : calm
}
export function View({ denied }) { return <i className={toneClass(denied)}/> }`
  const findings = scan({ path, source, policy: helperPolicy })
  assert.deepEqual(findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(), ['ts-colors/raw-color|text-red-500'])
})

test('repeated call sites do not duplicate a resolved literal occurrence', () => {
  const source = `export function View({ a, b }) {
  const chip = (hot) => { const hot_ = 'text-red-500'; const cold_ = 'text-[var(--color-muted)]'; return hot ? hot_ : cold_ }
  return <i className={\`\${chip(a)} \${chip(b)}\`}/>
}`
  const findings = scan({ path, source, policy: helperPolicy })
  assert.deepEqual(findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(), ['ts-colors/raw-color|text-red-500'])
})

for (const [label, body] of [
  ['a let binding', "let chipClass = 'text-red-500'; chipClass = external; return chipClass"],
  ['a later assignment to the const', "const chipClass = 'text-red-500'; if (flag) { chipClass = external }; return chipClass"],
  ['a member write through the binding', "const chipClass = 'text-red-500'; receiver[chipClass] = external; return chipClass"],
  ['a nested-closure write', "const chipClass = 'text-red-500'; const mutate = () => { chipClass = external }; return chipClass"],
  ['a destructuring assignment', "const chipClass = 'text-red-500'; [chipClass] = external; return chipClass"],
  ['an increment', "const chipClass = 'text-red-500'; chipClass++; return chipClass"],
  ['a destructured object initializer', "const { chipClass } = config; return chipClass"],
  ['an undeclared outer free identifier', 'return chipClass'],
  ['a declaration only in a shadowed inner block', "if (flag) { const chipClass = 'text-red-500' }; return chipClass"],
]) test(`helper-local class returns stay unsupported for ${label}`, () => {
  const source = `export function View({ flag }) {
  const chip = () => { ${body} }
  return <i className={chip()}/>
}`
  const findings = scan({ path, source, policy: helperPolicy })
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'chipClass').length, 1,
    `${label}: the unproven return must stay blocking`)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color').length, 0,
    `${label}: the unproven literal must not be scanned as governed value`)
})

// Positional resolution makes callee-local values follow the SAME semantics an
// in-place expression already has: a write-free let resolves to its stable
// initializer, non-literal initializers resolve as expressions (blocking at
// their own opaque point), and concatenation/template initializers get the
// two-sided in-place treatment. Written bindings still block whole-value use.
test('a write-free let return resolves to its stable initializer like an in-place let', () => {
  const source = `export function View({ flag }) {
  const chip = () => { let chipClass = 'text-red-500'; return chipClass }
  return <i className={chip()}/>
}`
  assert.deepEqual(
    scan({ path, source, policy: helperPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(),
    ['ts-colors/raw-color|text-red-500'],
  )
})

test('a call initializer blocks at the opaque call, not at the return identifier', () => {
  const source = `export function View({ flag }) {
  const chip = () => { const chipClass = computeClass(flag); return chipClass }
  return <i className={chip()}/>
}`
  const findings = scan({ path, source, policy: helperPolicy })
  assert.deepEqual(findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(), ['ts-colors/unsupported|computeClass(flag)'])
})

test('a concatenated initializer keeps in-place two-sided semantics', () => {
  const source = `export function View({ flag }) {
  const chip = () => { const chipClass = 'text-red-500' + suffix; return chipClass }
  return <i className={chip()}/>
}`
  assert.deepEqual(
    scan({ path, source, policy: helperPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(),
    ['ts-colors/raw-color|text-red-500', 'ts-colors/unsupported|suffix'],
  )
})

test('a template-with-spans initializer keeps in-place span semantics', () => {
  const source = `export function View({ flag }) {
  const chip = () => { const chipClass = \`text-red-500 \${flag}\`; return chipClass }
  return <i className={chip()}/>
}`
  assert.deepEqual(
    scan({ path, source, policy: helperPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(),
    ['ts-colors/raw-color|text-red-500', 'ts-colors/unsupported|flag'],
  )
})

test('callee-local resolution does not leak across sibling helpers', () => {
  const source = `export function View() {
  const owned = () => { const chipClass = 'text-red-500'; return chipClass }
  const foreign = () => { return chipClass }
  return <i className={\`\${owned()} \${foreign()}\`}/>
}`
  const findings = scan({ path, source, policy: helperPolicy })
  assert.deepEqual(findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(), [
    'ts-colors/raw-color|text-red-500',
    'ts-colors/unsupported|chipClass',
  ])
})

test('callee-local literal resolution serves style-value sinks without weakening opacity', () => {
  const resolve = `export function View({ hot }) {
  const tone = () => { const hot_ = '#ffffff'; const cold_ = 'var(--color-muted)'; return hot ? hot_ : cold_ }
  return <i style={{ color: tone() }}/>
}`
  assert.deepEqual(
    scan({ path, source: resolve, policy: helperPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(),
    ['ts-colors/raw-color|#ffffff'],
  )
  const opaque = `export function View({ color }) {
  const tone = () => { const chip = color; return chip }
  return <i style={{ color: tone() }}/>
}`
  // The chain resolves through to its terminal provenance — the component
  // parameter at a DOM style paint site — and blocks as the runtime paint
  // boundary, exactly like the in-place `style={{ color }}` equivalent.
  assert.deepEqual(
    scan({ path, source: opaque, policy: helperPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/extension-boundary|View#dom-style.color<-color'],
  )
})


// Lexical binding collisions V1–V4 (independent review glm-colours-independent-
// review.md): name-keyed resolution (module import map, module declaration map,
// walk-time caller scope) must never override the identifier's own lexical
// position. Ground truth = what runtime JS paints. A positional binding that
// cannot be valued must block, never fall through to the stale maps.
const GOV = 'text-[var(--color-muted)]'
const collisionPolicy = helperPolicy

test('V1: helper-local const shadowing a same-named import reports the local raw colour', () => {
  const source = `import { CHIP } from '@/tokens'
function chip() { const CHIP = 'text-red-500'; return CHIP }
export function View() { return <i className={chip()}/> }`
  const modules = { [path]: source, 'src/tokens.ts': `export const CHIP = '${GOV}'` }
  assert.deepEqual(
    scan({ path, source, policy: collisionPolicy, modules }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/raw-color|text-red-500'],
  )
})

test('V1 inverse: governed helper-local const shadowing a raw import stays clean', () => {
  const source = `import { CHIP } from '@/tokens'
function chip() { const CHIP = '${GOV}'; return CHIP }
export function View() { return <i className={chip()}/> }`
  const modules = { [path]: source, 'src/tokens.ts': `export const CHIP = 'text-red-500'` }
  assert.deepEqual(scan({ path, source, policy: collisionPolicy, modules }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [])
})

test('V2: helper-local const shadowing a same-named module-level const reports the local raw colour', () => {
  const source = `const CHIP = '${GOV}'
function chip() { const CHIP = 'text-red-500'; return CHIP }
export function View() { return <i className={chip()}/> }`
  assert.deepEqual(
    scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/raw-color|text-red-500'],
  )
})

test('V2 term collisions: module-level interface or function shadowed by a raw helper-local const still reports raw', () => {
  for (const outer of ['interface chipClass { tone: string }', "function chipClass() { return 'x' }"]) {
    const source = `${outer}
function chip() { const chipClass = 'text-red-500'; return chipClass }
export function View() { return <i className={chip()}/> }`
    assert.deepEqual(
      scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
      ['ts-colors/raw-color|text-red-500'],
      outer,
    )
  }
})

test('V3: helper-local const shadowing a caller-scope const reports the callee raw colour, not the caller value', () => {
  const source = `export function View() {
  const c = '${GOV}'
  const chip = () => { const c = 'text-red-500'; return c }
  return <i className={chip()}/>
}`
  assert.deepEqual(
    scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/raw-color|text-red-500'],
  )
})

test('V3 inverse: governed helper-local const shadowing a raw caller-scope const stays clean', () => {
  const source = `export function View() {
  const c = 'text-red-500'
  const chip = () => { const c = '${GOV}'; return c }
  return <i className={chip()}/>
}`
  assert.deepEqual(scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [])
})

test('V4: parameter colliding with a module const binds to the raw call argument', () => {
  const source = `const c = '${GOV}'
function chip(c) { return c }
export function View() { return <i className={chip('text-red-500')}/> }`
  assert.deepEqual(
    scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/raw-color|text-red-500'],
  )
})

test('raw literal call arguments reaching a returned parameter are reported (non-colliding baseline)', () => {
  const source = `function chip(c) { return c }
export function View() { return <i className={chip('text-red-500')}/> }`
  assert.deepEqual(
    scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/raw-color|text-red-500'],
  )
})

test('parameter default referencing an outer const resolves the default value', () => {
  const source = `const c = 'text-red-500'
function chip(v = c) { return v }
export function View() { return <i className={chip()}/> }`
  assert.deepEqual(
    scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/raw-color|text-red-500'],
  )
})

for (const [label, source, expectation] of [
  ['unresolvable argument expression',
    `function chip(c) { return c }\nexport function View() { return <i className={chip(unknownValue)}/> }`,
    (findings) => {
      assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported').length, 1, 'must block')
      assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color').length, 0, 'must not clean-pass raw')
    }],
  ['destructured parameter colliding with a module const',
    `const c = '${GOV}'\nfunction chip({ c }) { return c }\nexport function View() { return <i className={chip(config)}/> }`,
    (findings) => {
      assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported').length, 1, 'must block')
      assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color').length, 0, 'must not clean-pass')
    }],
  ['rest parameter colliding with a module const',
    `const c = '${GOV}'\nfunction chip(...c) { return c[0] }\nexport function View() { return <i className={chip('text-red-500')}/> }`,
    (findings) => {
      assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported').length >= 1, true, 'must block')
      assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color').length, 0, 'must not clean-pass')
    }],
  ['missing argument for a defaulted-from-collision parameter shape',
    `const c = '${GOV}'\nfunction chip(c) { return c }\nexport function View() { return <i className={chip()}/> }`,
    (findings) => {
      assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported').length, 1, 'undefined argument must block')
      assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color').length, 0, 'must not clean-pass')
    }],
]) test(`V4 fail-closed: ${label} blocks without clean-passing`, () => {
  expectation(scan({ path, source, policy: collisionPolicy }))
})

test('shadow write to an inner let voids the outer const proof conservatively (V7 stability)', () => {
  const source = `export function View({ flag }) {
  const chip = () => { const c = 'text-red-500'; if (flag) { let c = other; c = flag }; return c }
  return <i className={chip()}/>
}`
  const findings = scan({ path, source, policy: collisionPolicy })
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'c').length, 1)
  assert.equal(findings.filter(f => f.ruleId === 'ts-colors/raw-color').length, 0)
})

test('cross-module helper-local const still reports raw at its definition (F18 preservation)', () => {
  const source = `import { chip } from '@/tokens'
export function View() { return <i className={chip()}/> }`
  const modules = { [path]: source, 'src/tokens.ts': `export function chip() { const c = 'text-red-500'; return c }` }
  assert.deepEqual(
    scan({ path, source, policy: collisionPolicy, modules }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/raw-color|text-red-500'],
  )
})

test('a logical-not read of an unwritten derived boolean does not void its proof', () => {
  // Real-tree shape (BrowserNavigate hasDetail): the && guards read the const
  // through `!`, which is a pure read — only ++/--/delete are writes. Voiding
  // on `!` regressed six governed files to unsupported in the bounded diff.
  const source = `import { cn } from '@/lib/utils'
export const X = ({ r }) => {
  const isRunning = a()
  const hasResult = r !== undefined
  const hasDetail = !isRunning && hasResult
  return <i className={cn('x', hasDetail && 'hover:y', !hasDetail && 'z')}/>
}`
  // The boolean derivation is provable end-to-end; nothing class-valued
  // reaches a sink, so the file is clean. Voiding on `!` would regress this
  // to unsupported|hasDetail (the pre-fix behaviour).
  assert.deepEqual(scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [])
})

// Nested-helper parameter-frame propagation (lead probe "nested-frame",
// glm-colours-collision-lead-probes.json, still failing at handover: the
// identifier reentrancy marker in inspectClassExpr/inspectCssValueExpr keyed
// itself on the identifier's bare text, so two distinct parameters that
// happen to share a name (outer(c) forwarding into inner(c)) collided on the
// same marker and the second, correctly-resolvable occurrence was reported
// as a false self-cycle (ts-colors/unsupported|c) instead of resolving
// through to the raw call argument. The module-scope `c` token must never be
// substituted for the call argument either — that would be a silent pass.

test('nested helper parameter frame propagates the raw call argument through two pure levels', () => {
  const source = `const c = '${GOV}'
function inner(c) { return c }
function outer(c) { return inner(c) }
export function View() { return <i className={outer('text-red-500')}/> }`
  const findings = scan({ path, source, policy: collisionPolicy })
  assert.deepEqual(findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), ['ts-colors/raw-color|text-red-500'])
  assert.ok(!findings.some((f) => f.ruleId === 'ts-colors/unsupported'), 'must not fall back to false self-cycle on the shared parameter name')
})

test('nested helper parameter frame propagates the raw call argument through three pure levels', () => {
  const source = `const c = '${GOV}'
function innermost(c) { return c }
function inner(c) { return innermost(c) }
function outer(c) { return inner(c) }
export function View() { return <i className={outer('text-red-500')}/> }`
  assert.deepEqual(
    scan({ path, source, policy: collisionPolicy }).map(({ ruleId, syntax }) => `${ruleId}|${syntax}`),
    ['ts-colors/raw-color|text-red-500'],
  )
})

test('nested helper parameter shadowing a governed module const still reports the raw call argument, never the shadowed token', () => {
  // Same shape as the two-level case, restated to make the fail-closed
  // requirement explicit: the module-scope `c` is governed (GOV); if the
  // nested-frame resolution ever fell back to the name-keyed module map
  // instead of the positional call-argument frame, this would silently pass
  // clean instead of reporting the raw argument actually painted at runtime.
  const source = `const c = '${GOV}'
function inner(c) { return c }
function outer(c) { return inner(c) }
export function View() { return <i className={outer('text-red-500')}/> }`
  const findings = scan({ path, source, policy: collisionPolicy })
  assert.equal(findings.length, 1, 'the shadowed module token must not suppress the raw call-argument finding')
  assert.deepEqual(findings.map((f) => `${f.ruleId}|${f.syntax}`), ['ts-colors/raw-color|text-red-500'])
})

test('nested helper parameter shadowing a raw module const stays clean when the call argument is governed', () => {
  const source = `const c = 'text-red-500'
function inner(c) { return c }
function outer(c) { return inner(c) }
export function View() { return <i className={outer('${GOV}')}/> }`
  assert.deepEqual(scan({ path, source, policy: collisionPolicy }), [])
})

test('nested helper self-recursion blocks fail-closed without hanging', () => {
  const source = `function loop(c) { return loop(c) }
export function View() { return <i className={loop('text-red-500')}/> }`
  const findings = scan({ path, source, policy: collisionPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

test('nested helper mutual recursion blocks fail-closed without hanging', () => {
  const source = `function pingpong(c) { return pong(c) }
function pong(c) { return pingpong(c) }
export function View() { return <i className={pingpong('text-red-500')}/> }`
  const findings = scan({ path, source, policy: collisionPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

test('opaque argument at the inner nested level blocks without leaking the outer call argument', () => {
  // outer forwards an opaque call (unknown()) into inner, not its own
  // parameter; the raw literal reaching outer() must never be substituted
  // for the genuinely unresolved inner argument.
  const source = `function inner(c) { return c }
function outer(x) { return inner(unknown()) }
export function View() { return <i className={outer('text-red-500')}/> }`
  const findings = scan({ path, source, policy: collisionPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

// Destructured-binding member resolution (Task 2 capability, worklist pattern
// `const { color } = fileTypeMeta(...)`; also GoalIndicator.tsx's
// `const { className } = describeNonActiveState(...)`): an object-destructured
// local whose source resolves to a finite set of provably-literal candidate
// objects (a local/imported pure factory's return branches, or a plain
// object literal) is proven per property, the same way a direct
// `config.textClass` member access already is — declarationInit's scalar
// contract can't express this (see destructuredBindingElement's header
// comment), so it is proven separately and only consulted after the ordinary
// LOOKUP_UNBOUND path has already failed.
const destructurePolicy = { tokenCssNames: ['--color-muted', '--color-border'], resolvedTokens: {} }

test('destructured member from a local factory call resolves to its raw literal at a style paint sink', () => {
  const source = [
    "function fileTypeMeta(name) {",
    "  if (name.endsWith('.pdf')) return { icon: 'pdf', color: '#E5484D' }",
    "  return { icon: 'file', color: '#64748B' }",
    "}",
    "export function View({ entry }) {",
    "  const { icon, color } = fileTypeMeta(entry.filename)",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: destructurePolicy })
  assert.deepEqual(
    findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(),
    ['ts-colors/raw-color|#64748b', 'ts-colors/raw-color|#e5484d'],
  )
})

test('destructured member resolving to a governed token in every branch is clean', () => {
  const source = [
    "function describeState(state) {",
    "  if (state === 'a') return { className: 'text-[var(--color-muted)]' }",
    "  return { className: 'text-[var(--color-border)]' }",
    "}",
    "export function View({ state }) {",
    "  const { className } = describeState(state)",
    "  return <span className={className}/>",
    "}",
  ].join('\n')
  assert.deepEqual(scan({ path, source, policy: destructurePolicy }), [])
})

test('a destructured default value stays unsupported, never the default silently substituted', () => {
  const source = [
    "function fileTypeMeta() { return {} }",
    "export function View() {",
    "  const { color = '#E5484D' } = fileTypeMeta()",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: destructurePolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

test('a property missing from at least one candidate branch stays unsupported, never partially proven', () => {
  // Property key `tone`, not `color` (see the mutated/unknown-source
  // template test below for why): isolates this from the independent
  // static-object-literal scan so the assertion verifies only the
  // partial-coverage question.
  const source = [
    "function fileTypeMeta(name) {",
    "  if (name.endsWith('.pdf')) return { tone: '#E5484D' }",
    "  return { icon: 'file' }",
    "}",
    "export function View({ entry }) {",
    "  const { tone: color } = fileTypeMeta(entry.filename)",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: destructurePolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

test('a destructured binding reassigned after declaration voids the proof (mutated variant)', () => {
  // Property key `tone`, not `color`, for the same static-scan-isolation
  // reason as above.
  const source = [
    "function fileTypeMeta() { return { tone: '#E5484D' } }",
    "export function View({ override }) {",
    "  let { tone: color } = fileTypeMeta()",
    "  if (override) { color = override }",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: destructurePolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

test('destructuring from an unresolvable external module call stays unsupported (unknown-source variant)', () => {
  const source = [
    "import { fileTypeMeta } from '@/missing'",
    "export function View({ entry }) {",
    "  const { color } = fileTypeMeta(entry.filename)",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: destructurePolicy, modules: { [path]: source } })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

test('array-destructured bindings stay unsupported — index is not a stable key', () => {
  const source = [
    "function fileTypeMeta() { return ['#E5484D', 'pdf'] }",
    "export function View() {",
    "  const [color] = fileTypeMeta()",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: destructurePolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

test('nested destructured patterns stay unsupported — only a simple top-level binding is proven', () => {
  // Property key `tone`, not `color` (see the mutated/unknown-source
  // template test above for why): isolates this from the independent
  // static-object-literal scan so the assertion verifies only the
  // nested-pattern question.
  const source = [
    "function fileTypeMeta() { return { meta: { tone: '#E5484D' } } }",
    "export function View() {",
    "  const { meta: { tone: color } } = fileTypeMeta()",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: destructurePolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/raw-color').length, 0)
})

// css-value template splicing of a destructured base (Task 2 capability,
// worklist pattern `${color}22`): only a base that resolves to a plain
// string literal in EVERY branch is safe to splice into a compound css-value
// template — see literalDestructuredTemplateTargets's header comment for why
// this is deliberately narrower than class-mode's template fan-out.

test('a template alpha suffix on a destructured literal base reports the proven raw values, not the whole template', () => {
  const source = [
    "function fileTypeMeta(name) {",
    "  if (name.endsWith('.pdf')) return { color: '#E5484D' }",
    "  return { color: '#64748B' }",
    "}",
    "export function View({ entry }) {",
    "  const { color } = fileTypeMeta(entry.filename)",
    "  return <i style={{ backgroundColor: `${color}22` }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: destructurePolicy })
  assert.deepEqual(
    findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(),
    ['ts-colors/raw-color|#64748b', 'ts-colors/raw-color|#e5484d'],
  )
})

test('a template alpha suffix on an unproven (non-destructured) value stays unsupported, never spliced', () => {
  const source = [
    'export function View({ color }) {',
    '  return <i style={{ backgroundColor: `${color}22` }}/>',
    '}',
  ].join('\n')
  assert.deepEqual(scan({ path, source, policy: destructurePolicy }).map((f) => f.ruleId), ['ts-colors/unsupported'])
})

test('a template alpha suffix on a destructured non-literal value stays unsupported (mutated/unknown-source variant)', () => {
  // Property key `tone`, not `color`: keeps this isolated from the
  // independent static-object-literal scan (inspectNode fires
  // inspectStyleObject on EVERY object literal with a colour-shaped key,
  // anywhere it is written, regardless of how it is later read) so the
  // assertion below verifies only the template-splice question — the base
  // resolves via a rename (`tone: color`) to a literal in one branch and an
  // opaque parameter in the other, so it is not provably literal-only and
  // must never be spliced.
  const source = [
    "function fileTypeMeta(name, unresolvedTone) {",
    "  if (name.endsWith('.pdf')) return { tone: '#E5484D' }",
    "  return { tone: unresolvedTone }",
    "}",
    "export function View({ entry, tone }) {",
    "  const { tone: color } = fileTypeMeta(entry.filename, tone)",
    "  return <i style={{ backgroundColor: `${color}22` }}/>",
    "}",
  ].join('\n')
  assert.deepEqual(scan({ path, source, policy: destructurePolicy }).map((f) => f.ruleId), ['ts-colors/unsupported'])
})

test('an unproven-type numeric template is unaffected by the destructured-literal escape hatch (regression pin)', () => {
  // Guards the named-numeric-proof adversarial suite above: `value` here is
  // a destructured PARAMETER (not a destructured local variable), so
  // destructuredBindingElement's function-parameter bail-out means
  // literalDestructuredTemplateTargets must return null and this must fall
  // straight through to the pre-existing numeric-proof/unresolved path,
  // unchanged by either Task 2 capability.
  const policy = { tokenCssNames: ['--color-primary'], resolvedTokens: {} }
  const source = 'interface Props { value: string }; function Range({ value }: Props) { return <i style={{ background: `linear-gradient(var(--color-primary) ${value}%, transparent)` }}/> }'
  const findings = scan({ path, source, policy })
  assert.deepEqual(findings.map((f) => f.ruleId), ['ts-colors/unsupported'])
})


// ── Pass-2 capability suite ─────────────────────────────────────────────
// Six structural fixes to the "is this chained-member receiver stable"
// proof (knownClassReceiverStable / knownClassDerivedUsesSafe /
// absenceBindingUsesSafe / knownClassExportUsesSafe), found by tracing WHY
// real, already-resolvable findings (STATUS_BADGE[run.status],
// MODE_CHIP_CLASS[m], cfg.activeColor, driveChip.textClass, PRIORITY_BADGE
// via Object.keys) were still reported `unsupported` despite their target
// values already resolving to plain literals. Each capability gets a
// permitted case (the real shape that must now resolve) and at least one
// forbidden/mutated case (the same shape with the one detail changed that
// must still block) so the fix cannot be a blanket relaxation.

const pass2Policy = { tokenCssNames: [], resolvedTokens: {} }

function pass2Findings(source, modules) {
  return scan({ path, source, policy: pass2Policy, modules: modules ?? { [path]: source } })
}

// CAP-D1 — a non-JS asset file (.svg/.css) elsewhere in the module set must
// not poison an exported const's cross-module receiver-stability proof: it
// can never contain import/export/require syntax relevant to that proof, so
// trying to TS-parse it (and finding diagnostics, since it isn't JS) must
// not blanket-fail the check the moment such a file exists anywhere.
test('an unparseable non-JS module elsewhere does not block an exported record receiver (permitted)', () => {
  const configSource = "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }"
  const source = "import { REGISTRY } from '@/registry'; export const View = ({ k }) => <i className={REGISTRY[k].textClass}/>"
  const modules = {
    [path]: source,
    'src/registry.ts': configSource,
    'src/assets/logo.svg': '<?xml version="1.0" encoding="UTF-8"?>\n<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>',
  }
  const findings = pass2Findings(source, modules)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/undefined-token' && f.syntax === 'var(--color-primary)'))
})

test('a genuinely unsafe cross-module export use still blocks, even with a non-JS module present (forbidden)', () => {
  const configSource = "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }"
  const source = "import { REGISTRY } from '@/registry'; export const View = ({ k }) => <i className={REGISTRY[k].textClass}/>"
  const modules = {
    [path]: source,
    'src/registry.ts': configSource,
    'src/assets/logo.svg': '<?xml version="1.0" encoding="UTF-8"?>\n<svg xmlns="http://www.w3.org/2000/svg"><path d="M0 0"/></svg>',
    'src/mutate.ts': "import { REGISTRY as alias } from '@/registry'; alias.a.textClass = external",
  }
  const findings = pass2Findings(source, modules)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY[k].textClass'))
})

// CAP-D2 — a template-literal dynamic import()/require() whose literal head
// structurally cannot resolve to `origin` (proven, not guessed, from the
// TemplateExpression's fixed prefix) must not contaminate every OTHER
// exported const's stability proof just for existing somewhere in the tree.
test('a directory-disjoint dynamic import template elsewhere does not block an exported record (permitted)', () => {
  const configSource = "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }"
  const source = "import { REGISTRY } from '@/registry'; export const View = ({ k }) => <i className={REGISTRY[k].textClass}/>"
  const modules = {
    [path]: source,
    'src/registry.ts': configSource,
    'src/lib/lazy.ts': 'export const load = (name) => import(`@/components/tools/${name}.tsx`)',
  }
  const findings = pass2Findings(source, modules)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('an npm-package dynamic import template elsewhere does not block an exported record (permitted)', () => {
  const configSource = "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }"
  const source = "import { REGISTRY } from '@/registry'; export const View = ({ k }) => <i className={REGISTRY[k].textClass}/>"
  const modules = {
    [path]: source,
    'src/registry.ts': configSource,
    'src/lib/lazy.ts': 'export const load = (mode) => import(`@codemirror/legacy-modes/mode/${mode}`)',
  }
  const findings = pass2Findings(source, modules)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('a dynamic import template sharing the origin directory still blocks (forbidden)', () => {
  const configSource = "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }"
  const source = "import { REGISTRY } from '@/registry'; export const View = ({ k }) => <i className={REGISTRY[k].textClass}/>"
  const modules = {
    [path]: source,
    'src/registry.ts': configSource,
    'src/lib/lazy.ts': 'export const load = (mode) => import(`@/registry${mode}`)',
  }
  const findings = pass2Findings(source, modules)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY[k].textClass'))
})

test('a dynamic import with a bare variable specifier still blocks, unchanged (forbidden)', () => {
  const configSource = "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }"
  const source = "import { REGISTRY } from '@/registry'; export const View = ({ k }) => <i className={REGISTRY[k].textClass}/>"
  const modules = {
    [path]: source,
    'src/registry.ts': configSource,
    'src/lib/lazy.ts': 'export const load = (moduleName) => import(moduleName)',
  }
  const findings = pass2Findings(source, modules)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY[k].textClass'))
})

// CAP-E1 — a resolved member consumed only as a JSX element's own tag name
// (directly, or through a `const Alias = cfg.member` indirection) is a
// terminal render read and must not blanket-block an unrelated sibling
// property of the same finite record.
test('an unrelated sibling rendered as a JSX tag (direct member access) does not block a colour sibling (permitted)', () => {
  const source = [
    "const CFG = { a: { Icon: RealIcon, textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { return <div><CFG[k].Icon size={12}/><i className={CFG[k].textClass}/></div> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('an unrelated sibling aliased then rendered as a JSX tag does not block a colour sibling (permitted)', () => {
  const source = [
    "const CFG = { a: { icon: RealIcon, textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const cfg = CFG[k]; const Icon = cfg.icon; return <div><Icon size={12}/><i className={cfg.textClass}/></div> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('an aliased sibling that escapes beyond a JSX tag still blocks (forbidden)', () => {
  const source = [
    "const CFG = { a: { icon: RealIcon, textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const cfg = CFG[k]; const Icon = cfg.icon; mutate(Icon); return <div><Icon size={12}/><i className={cfg.textClass}/></div> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'cfg.textClass'))
})

// CAP-E2 — a simple, non-rest, non-default, non-nested object-destructuring
// read of a stable receiver is safe when every extracted local is itself
// only ever used safely (recursing through the same proof) — must not
// block an unrelated sibling drawn from the SAME receiver via a plain
// member access.
test('destructuring an unrelated icon out of a receiver used only as a JSX tag does not block a colour sibling (permitted)', () => {
  const source = [
    "const CFG = { a: { icon: RealIcon, textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const cfg = CFG[k]; const { icon: Icon } = cfg; return <div><Icon size={12}/><i className={cfg.textClass}/></div> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('destructuring with a rest element still blocks (forbidden)', () => {
  const source = [
    "const CFG = { a: { icon: RealIcon, textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const cfg = CFG[k]; const { icon: Icon, ...rest } = cfg; use(rest); return <div><Icon size={12}/><i className={cfg.textClass}/></div> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'cfg.textClass'))
})

test('a destructured local that itself escapes beyond a JSX tag still blocks (forbidden)', () => {
  const source = [
    "const CFG = { a: { icon: RealIcon, textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const cfg = CFG[k]; const { icon: Icon } = cfg; mutate(Icon); return <div><Icon size={12}/><i className={cfg.textClass}/></div> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'cfg.textClass'))
})

// CAP-F — a ternary/`??` composed entirely of literal leaves is exactly as
// immutable/escape-free as one literal; recognizing this must not treat a
// bare object/array literal sibling the same way (that one CAN still be
// captured by a separate alias and mutated — the alias-escape check below
// must keep catching that).
test('a sibling whose value is a ternary of plain literals does not block a colour sibling (permitted)', () => {
  const source = [
    "const CFG = { a: { label: flag ? 'x' : 'y', textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const cfg = CFG[k]; return <i title={cfg.label} className={cfg.textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('a sibling whose ternary has one opaque literal branch still blocks (forbidden)', () => {
  const source = [
    "const CFG = { a: { label: flag ? opaque() : 'y', textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const cfg = CFG[k]; return <i title={cfg.label} className={cfg.textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'cfg.textClass'))
})

test('a sibling that is a plain nested object literal still requires the alias-escape check, not treated as a leaf (forbidden/mutation-representative)', () => {
  // Same shape as the real 'tones["good"]' regression this decoupling could
  // have reintroduced: the SAME nested object literal is reachable through a
  // second alias that mutates it, so `config.textClass` (read through a
  // THIRD, otherwise-unrelated access of the same source) must still block.
  const source = [
    "const tones = { good: { textClass: 'text-[var(--color-primary)]' } }",
    "export function View() { const alias = tones['good']; alias.textClass = external; const config = tones['good']; return <i className={config.textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass'))
})

// CAP-G — a sibling property's inability to be fully ENUMERATED (e.g. an
// optional field omitted on some branches, blocked by the deliberately
// unrelaxed null-prototype absence rule) must not blanket-block an
// unrelated, fully-resolvable sibling — but the incomplete property itself
// must still report unsupported, and a TOTALLY unresolvable sibling
// (targets.length === 0) must still gate the escape check (the vacuous-
// truth guard).
test('an incomplete-but-primitive sibling does not block a fully resolvable sibling (permitted)', () => {
  const source = [
    "function factory(flag) { if (flag) return { textClass: 'text-[var(--color-primary)]', pulse: true }; return { textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ flag }) { const config = factory(flag); return <i className={cn(config.textClass, config.pulse && 'animate-pulse')}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass'), [])
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.pulse'))
})

test('a totally unresolvable sibling still gates the escape check (forbidden/vacuous-truth guard)', () => {
  const source = [
    "function factory(flag) { if (flag) return { textClass: 'text-[var(--color-primary)]', other: opaque() }; return { textClass: 'text-[var(--color-primary)]', other: opaque() } }",
    "export function View({ flag }) { const config = factory(flag); mutate(config.other); return <i className={config.textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'config.textClass'))
})

// CAP — Object.keys/values/entries/freeze/isFrozen/getOwnPropertyNames(X) is
// a spec-pure static read of X: it can neither mutate X nor hand out a
// mutable reference to X's own nested values. A terminal use this way must
// not blanket-block an unrelated read of the very same record.
test('Object.keys(record) elsewhere does not block a colour read of the same record (permitted)', () => {
  const source = [
    "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' }, b: { textClass: 'text-[var(--color-primary)]' } }",
    'const ORDER = Object.keys(REGISTRY)',
    "export function View({ k }) { return <i className={REGISTRY[k].textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('passing the record to a non-allowlisted function still blocks (forbidden)', () => {
  const source = [
    "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' }, b: { textClass: 'text-[var(--color-primary)]' } }",
    'mutate(REGISTRY)',
    "export function View({ k }) { return <i className={REGISTRY[k].textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY[k].textClass'))
})

test('a same-named "keys" method on a non-Object receiver still blocks (forbidden)', () => {
  const source = [
    "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' }, b: { textClass: 'text-[var(--color-primary)]' } }",
    'NotObject.keys(REGISTRY)',
    "export function View({ k }) { return <i className={REGISTRY[k].textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY[k].textClass'))
})
