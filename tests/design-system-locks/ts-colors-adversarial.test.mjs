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

test('an unchanged whole-style parameter forwarded to JSX is an exact blocking boundary, not a coverage gap', () => {
  // P4 review finding: `style`, forwarded UNCHANGED straight into a
  // `style={…}` JSX attribute, is caller pass-through with the exact same
  // contract as a forwarded `className` parameter (already an
  // extension-boundary a few tests up) — the caller's own argument was
  // already validated at ITS call site. This used to be a coverage gap
  // (permanently unsupported, never registrable); it now gets the same
  // exact, reviewable identity className gets.
  const style = `export function Card({ style }) { return <i style={style}/> }`
  const findings = paintFindings(style)
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax), ['Card#style'])
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unverified-governed-value'))
})

test('a transformed paint value is still an opaque call, not a forwarded parameter — stays a coverage gap', () => {
  // Unlike the bare-forward case above, `tint(data.color)` is a NEW value
  // this scanner cannot see through (an opaque call, not a pass-through
  // parameter) — it must keep failing exactly as before the P4 fix.
  const transformed = `export function Card({ data }) { return <i style={{ color: tint(data.color) }}/> }`
  const findings = paintFindings(transformed)
  assert.ok(findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unverified-governed-value'))
})

// P17 review finding: a React state local (`const [x] = useState(...)`)
// reaching a KNOWN colour sink is a genuinely runtime value — a fixed,
// provable React API, not an arbitrary opaque call — so it gets the same
// registrable extension-boundary treatment as a forwarded parameter. The
// fairness pair matters here: a raw literal reaching the SAME sink the same
// way must still report raw-color (never silently swallowed by the new
// path), and an identically-shaped array destructuring from anything OTHER
// than useState must keep failing exactly as before (the adversarial suite's
// own "array-destructured bindings stay unsupported" case, reused as the
// opaque-call control below).
test('a useState local reaching a dom-style colour property is an exact runtime paint boundary', () => {
  const source = `export function Avatar() { const [selectedColor] = useState(undefined); return <i style={{ backgroundColor: selectedColor }}/> }`
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['Avatar#dom-style.backgroundColor<-selectedColor'],
  )
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
})

test('a useState local reaching an SVG colour attribute is an exact runtime paint boundary', () => {
  const source = `export function Avatar() { const [selectedColor] = useState(undefined); return <svg><path color={selectedColor}/></svg> }`
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['Avatar#svg-attribute.color<-selectedColor'],
  )
})

test('a raw colour literal at the same dom-style sink still reports raw-color, unaffected by the useState boundary', () => {
  const source = `export function Avatar() { return <i style={{ backgroundColor: '#ffffff' }}/> }`
  const findings = paintFindings(source)
  assert.deepEqual(findings, [{ ruleId: 'ts-colors/raw-color', path, syntax: '#ffffff' }])
})

// Lead review, 2026-09-19: a laundering leak — once a useState read site is
// registered as a permanent extension-boundary exception, a raw colour
// literal fed to it through the initial value or a later setter call would
// otherwise pass completely silently (the read-site boundary is the ONLY
// finding, never independently scanned). scanHookStateLaundering closes
// this: whenever hookStateLocal grants the boundary, it ALSO scans the
// useState(...) initializer argument and every direct setter call's
// argument (including functional-update return values) as static values —
// a raw literal there reports ts-colors/raw-color IN ADDITION to the
// unchanged extension-boundary at the read site.
test('a raw colour literal in the useState initial value is independently reported, not laundered by the boundary', () => {
  const source = "import { useState } from 'react'\nexport function Avatar() { const [c] = useState('#ff0000'); return <i style={{ color: c }}/> }"
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['Avatar#dom-style.color<-c'],
  )
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/raw-color').map(({ syntax }) => syntax),
    ['#ff0000'],
  )
})

test('a raw colour literal passed to the paired setter is independently reported, not laundered by the boundary', () => {
  const source = "import { useState } from 'react'\nexport function V() { const [c, setC] = useState(undefined); return <i onClick={() => setC('#00ff00')} style={{ color: c }}/> }"
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['V#dom-style.color<-c'],
  )
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/raw-color').map(({ syntax }) => syntax),
    ['#00ff00'],
  )
})

test('a raw colour literal returned from a functional-update setter call is independently reported', () => {
  const source = "import { useState } from 'react'\nexport function V() { const [c, setC] = useState(undefined); return <i onClick={() => setC((prev) => '#f00')} style={{ color: c }}/> }"
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['V#dom-style.color<-c'],
  )
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/raw-color').map(({ syntax }) => syntax),
    ['#f00'],
  )
})

test('an opaque setter argument (a parameter, a property access) adds nothing extra beyond the boundary', () => {
  const source = "import { useState } from 'react'\nexport function V({ agent }) { const [c, setC] = useState(undefined); return <i onClick={() => setC(agent.color)} style={{ color: c }}/> }"
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['V#dom-style.color<-c'],
  )
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/raw-color'), [])
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/unsupported'), [])
})

test('a setter passed to another component (escapes) voids the boundary — the read stays unsupported', () => {
  // `onSave={setC}` hands the setter to ANOTHER component; this file can
  // never see every future call ColorPicker makes with it, so the whole
  // useState exception must be voided, not just the one visible reference.
  const source = "import { useState } from 'react'\nexport function V() { const [c, setC] = useState(undefined); return <ColorPicker onSave={setC}><i style={{ color: c }}/></ColorPicker> }"
  const findings = paintFindings(source)
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'), [])
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'c'))
})

test('a setter returned from the component (escapes) voids the boundary — the read stays unsupported', () => {
  const source = "import { useState } from 'react'\nexport function useColor() { const [c, setC] = useState(undefined); const el = <i style={{ color: c }}/>; return { el, setC } }"
  const findings = paintFindings(source)
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'), [])
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'c'))
})

test('a setter aliased to another variable (escapes) voids the boundary — the read stays unsupported', () => {
  const source = "import { useState } from 'react'\nexport function V() { const [c, setC] = useState(undefined); const alias = setC; alias('#ff0000'); return <i style={{ color: c }}/> }"
  const findings = paintFindings(source)
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'), [])
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'c'))
})

test('the real AgentProfile shape — undefined initial, opaque setter calls only — still classifies as extension-boundary with no extra raw findings', () => {
  const source = [
    "import { useState } from 'react'",
    "export function AgentProfile({ agent }) {",
    "  const [selectedColor, setSelectedColor] = useState(undefined)",
    "  setSelectedColor(agent.color)",
    "  return (",
    "    <>",
    "      <i style={{ backgroundColor: selectedColor }}/>",
    "      <svg><path color={selectedColor} onChange={(color) => setSelectedColor(color)}/></svg>",
    "    </>",
    "  )",
    "}",
  ].join('\n')
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax).sort(),
    ['AgentProfile#dom-style.backgroundColor<-selectedColor', 'AgentProfile#svg-attribute.color<-selectedColor'].sort(),
  )
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/raw-color'), [])
  assert.deepEqual(findings.filter(({ ruleId }) => ruleId === 'ts-colors/unsupported'), [])
})

test('an array-destructured local from a NON-useState call reaching the same colour sink stays unsupported', () => {
  const source = `export function Avatar() { const [selectedColor] = pickColor(); return <i style={{ backgroundColor: selectedColor }}/> }`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'selectedColor'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'))
})

test('a useState local aliased through an intermediate variable still resolves through the existing alias-following proof', () => {
  // This is NOT a hookStateLocal-specific capability — the ordinary
  // resolveIdentInit chain already follows a plain `const alias = x` alias
  // to its source for EVERY identifier (see "follows a const alias into a
  // style colour" above); it bottoms out at `selectedColor`, which
  // hookStateLocal then recognizes exactly as if it had been used bare.
  const source = `export function Avatar() { const [selectedColor] = useState(undefined); const alias = selectedColor; return <i style={{ backgroundColor: alias }}/> }`
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['Avatar#dom-style.backgroundColor<-selectedColor'],
  )
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
})

test('a useState local inside a genuinely anonymous-owner IIFE stays unsupported — no stable identity to register', () => {
  // Deliberately an IIFE whose CALL RESULT (not the function itself) is
  // assigned to Avatar — functionIdentity walks past a call wrapper only
  // for forwardRef/memo (isComponentWrapper), so an ordinary invoked
  // function expression gets no borrowed name from its call site, unlike a
  // function bound directly to a variable or object property.
  const source = `export const Avatar = (() => { const [selectedColor] = useState(undefined); return <i style={{ backgroundColor: selectedColor }}/> })()`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'selectedColor'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'))
})

// P18/P6 review finding: `.style.setProperty('--custom-prop', value)` never
// constructed a boundary at all, so an unresolved custom-property write
// could only ever be permanently unsupported — even when the property name
// itself proves it is not a colour (a live layout measurement, an
// adjustable root font size). isRuntimeMeasurementBoundary is the exact,
// narrow gate: non-colour custom property name AND a resolvable (non-
// anonymous, or hook-derived) owner.
test('an unresolved non-colour custom property set through a NAMED function is an exact runtime paint boundary', () => {
  const source = `export function AppShell() { function applyMetrics() { const { appTop } = computeAppMetrics(); document.documentElement.style.setProperty('--app-top', appTop) } }`
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['applyMetrics#dom-style.--app-top<-appTop'],
  )
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/unsupported'))
})

test('the same custom-property write inside a NAMED function\'s anonymous useEffect callback derives owner#hook identity', () => {
  const source = `export function ProfileSection() { useEffect(() => { document.documentElement.style.setProperty('--user-font-size', \`\${fontSize}px\`) }, [fontSize]) }`
  const findings = paintFindings(source)
  assert.deepEqual(
    findings.filter(({ ruleId }) => ruleId === 'ts-colors/extension-boundary').map(({ syntax }) => syntax),
    ['ProfileSection#useEffect#dom-style.--user-font-size<-fontSize'],
  )
})

test('an unresolved custom-property write inside a genuinely anonymous callback with NO hook wrapper stays unsupported', () => {
  // The IIFE's inner arrow is never bound to a name or property (same shape
  // as the useState anonymous-owner case above) AND is not a direct
  // useEffect/useLayoutEffect/useInsertionEffect callback argument, so
  // neither functionIdentity nor directHookCallbackName can derive anything
  // — setPropertyOwnerIdentity must fall all the way through to 'anonymous'.
  const source = `export function AppShell() { (() => { document.documentElement.style.setProperty('--app-top', appTop) })() }`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'appTop'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'))
})

test('an anonymous useEffect callback with no NAMED outer function to anchor to stays unsupported', () => {
  const source = `(() => { useEffect(() => { document.documentElement.style.setProperty('--app-top', appTop) }, []) })()`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'appTop'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'))
})

test('a custom property whose OWN name reads as a colour gets no measurement exception and stays unsupported', () => {
  const source = `export function AppShell() { function applyAccent() { document.documentElement.style.setProperty('--accent-color', dynamicAccent) } }`
  const findings = paintFindings(source)
  assert.ok(findings.some(({ ruleId, syntax }) => ruleId === 'ts-colors/unsupported' && syntax === 'dynamicAccent'))
  assert.ok(!findings.some(({ ruleId }) => ruleId === 'ts-colors/extension-boundary'))
})

test('a raw literal set through the same custom-property sink still reports raw-color, unaffected by the boundary fix', () => {
  const source = `function applyMetrics() { document.documentElement.style.setProperty('--app-top', '#ffffff') }`
  const findings = paintFindings(source)
  assert.deepEqual(findings, [{ ruleId: 'ts-colors/raw-color', path, syntax: '#ffffff' }])
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

// ── Round-3 fix: destructuring/member-access incompleteness parity ──────
// Independent adversarial review (dist/design-system-baseline/cli-lanes/
// claude-ts-colors-review/review.md, Finding 1 and Finding 2) found that
// resolveDestructuredTargets did not reproduce resolveMemberTargets's
// spread/computed-key incompleteness guard, and that inspectCssValueExpr's
// plain property-access branch called resolveMemberTargets without a
// resolution tracker at all — both let a real raw-colour override reach a
// paint sink while the scanner reported the file completely clean. Every
// test below is a false green on HEAD b57bd04a3 (confirmed via an isolated
// probe against a frozen pre-fix copy of the scanner:
// dist/design-system-baseline/cli-lanes/claude-ts-colors-fix3/red-check/).
const spreadPolicy = { tokenCssNames: ['--color-safe'], resolvedTokens: {} }

test('destructuring: an opaque spread AFTER a governed literal blocks in CLASS mode (Finding 1)', () => {
  const source = [
    "function fileTypeMeta(overrides) {",
    "  return { color: 'text-[var(--color-safe)]', ...overrides }",
    "}",
    "export function View({ overrides }) {",
    "  const { color } = fileTypeMeta(overrides)",
    "  return <i className={color}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: spreadPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1,
    'a spread sibling could override color at runtime — this must never resolve to a clean class value')
})

test('destructuring: an opaque spread AFTER a governed literal blocks in CSS-VALUE mode (Finding 1)', () => {
  const source = [
    "function fileTypeMeta(overrides) {",
    "  return { color: 'var(--color-safe)', ...overrides }",
    "}",
    "export function View({ overrides }) {",
    "  const { color } = fileTypeMeta(overrides)",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: spreadPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1,
    'a spread sibling could override color at runtime — this must never resolve to a clean style value')
})

test('destructuring: an opaque spread AFTER a governed literal blocks through the CAP-B template-splice escape hatch (Finding 1)', () => {
  const source = [
    "function fileTypeMeta(overrides) {",
    "  return { color: 'var(--color-safe)', ...overrides }",
    "}",
    "export function View({ overrides }) {",
    "  const { color } = fileTypeMeta(overrides)",
    "  return <i style={{ backgroundColor: `${color}22` }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: spreadPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === '`${color}22`').length, 1,
    'the template base must never be trusted enough to splice when its source object carries an opaque spread')
})

test('plain member access on a call-derived object with an opaque spread blocks in CSS-VALUE mode (Finding 2)', () => {
  // Same shape as the destructuring test above, read via `c.color` instead
  // of `const { color } = ...` — this exercises inspectCssValueExpr's plain
  // property-access branch directly, independent of resolveDestructuredTargets.
  const source = [
    "function fileTypeMeta(overrides) {",
    "  return { color: 'var(--color-safe)', ...overrides }",
    "}",
    "export function View({ overrides }) {",
    "  const c = fileTypeMeta(overrides)",
    "  return <i style={{ color: c.color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: spreadPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'c.color').length, 1,
    'CSS-value mode member access must apply the same incompleteness guard class mode already applies')
})

test('destructuring: a spread BEFORE the governed literal still blocks (order-agnostic parity with resolveMemberTargets)', () => {
  // ECMAScript resolves this specific literal object safely (the later
  // explicit `color` always wins over an earlier spread) — but
  // resolveMemberTargets, the sibling function for plain member access,
  // does not attempt order-aware reasoning either: ANY spread or computed
  // key anywhere in a candidate object marks the whole resolution
  // incomplete, regardless of position. Making destructuring order-aware
  // while member access stays order-blind would itself be a soundness
  // asymmetry between the two paths, so this stays unsupported by design.
  const source = [
    "import { dangerousOverrides } from '@/somewhere'",
    "function fileTypeMeta() {",
    "  return { ...dangerousOverrides, color: 'var(--color-safe)' }",
    "}",
    "export function View() {",
    "  const { color } = fileTypeMeta()",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const destructured = scan({ path, source, policy: spreadPolicy })
  const memberSource = source
    .replace('const { color } = fileTypeMeta()', 'const c = fileTypeMeta()')
    .replace('style={{ color }}', 'style={{ color: c.color }}')
  const memberAccess = scan({ path, source: memberSource, policy: spreadPolicy })
  assert.equal(destructured.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
  assert.equal(memberAccess.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'c.color').length, 1)
})

test('destructuring: a nested spread (spread-of-a-spread) still blocks', () => {
  // The immediate object literal's own property list already contains a
  // SpreadAssignment member (`...{ ...dangerousOverrides }`), which is
  // sufficient to trip the incompleteness guard without needing to recurse
  // into what the inner spread itself contains.
  const source = [
    "import { dangerousOverrides } from '@/somewhere'",
    "function fileTypeMeta() {",
    "  return { color: 'var(--color-safe)', ...{ ...dangerousOverrides } }",
    "}",
    "export function View() {",
    "  const { color } = fileTypeMeta()",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: spreadPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
})

test('destructuring: a spread of a provably disjoint-key const literal still blocks (deliberately not proven safe — see comment)', () => {
  // SAFE_EXTRA is a local const object literal with no 'color' key at all,
  // so this specific spread genuinely cannot override `color` at runtime —
  // in principle this case COULD be proven safe. It is deliberately NOT
  // special-cased here: resolveMemberTargets (the sibling function this fix
  // brings resolveDestructuredTargets into parity with) has no such
  // per-key-disjointness proof either, and never inlines a spread's source
  // to check it. Adding that proof only on the destructuring path would
  // reopen exactly the class-of-asymmetry this fix exists to close, for a
  // pattern with no evidence of real-world need (the spot-checked real
  // factories in the tree use no spread at all). Fails closed, not guessed.
  const source = [
    "const SAFE_EXTRA = { label: 'x' }",
    "function fileTypeMeta() {",
    "  return { color: 'var(--color-safe)', ...SAFE_EXTRA }",
    "}",
    "export function View() {",
    "  const { color } = fileTypeMeta()",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: spreadPolicy })
  assert.equal(findings.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
})

test('destructuring: a computed key that could dynamically alias the target property still blocks in CLASS mode, matching plain member access', () => {
  const source = [
    "function fileTypeMeta(dynamicKey) {",
    "  return { color: 'text-[var(--color-safe)]', [dynamicKey]: 'text-red-500' }",
    "}",
    "export function View({ dynamicKey }) {",
    "  const { color } = fileTypeMeta(dynamicKey)",
    "  return <i className={color}/>",
    "}",
  ].join('\n')
  const memberSource = [
    "function fileTypeMeta(dynamicKey) {",
    "  return { color: 'text-[var(--color-safe)]', [dynamicKey]: 'text-red-500' }",
    "}",
    "export function View({ dynamicKey }) {",
    "  const meta = fileTypeMeta(dynamicKey)",
    "  return <i className={meta.color}/>",
    "}",
  ].join('\n')
  const destructured = scan({ path, source, policy: spreadPolicy })
  const memberAccess = scan({ path, source: memberSource, policy: spreadPolicy })
  assert.equal(destructured.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color').length, 1)
  assert.equal(memberAccess.filter((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'meta.color').length, 1)
})

test('destructuring without any spread or computed key still resolves cleanly (regression pin — the fix must not over-block)', () => {
  const source = [
    "function fileTypeMeta(name) {",
    "  if (name.endsWith('.pdf')) return { color: '#E5484D' }",
    "  return { color: '#64748B' }",
    "}",
    "export function View({ entry }) {",
    "  const { color } = fileTypeMeta(entry.filename)",
    "  return <i style={{ color }}/>",
    "}",
  ].join('\n')
  const findings = scan({ path, source, policy: spreadPolicy })
  assert.deepEqual(
    findings.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`).sort(),
    ['ts-colors/raw-color|#64748b', 'ts-colors/raw-color|#e5484d'],
  )
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

// CAP-H — nested-reference escape through `Object.values`/`Object.entries`
// (independent-review Finding 2, round 4). Unlike `Object.keys`, `values`
// and `entries` hand out LIVE references to a record's own nested objects —
// mutating an element mutates the record in place. Every consumption shape
// below must still block: forEach/for-of iteration, `Array.from`, spread,
// and the same escape reached through an importer's own use of the export.
// The one permitted case (`Object.keys(...).map(k => k.length)`) proves the
// unconditionally-safe methods were not swept up by the tightened rule.

test('Object.values(record).forEach mutating a nested value blocks the colour read (forbidden)', () => {
  const source = [
    "const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }",
    "Object.values(REGISTRY).forEach(v => { v.textClass = 'text-red-500' })",
    'export function V(){ return <i className={REGISTRY.a.textClass}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY.a.textClass'))
})

test('for...of over Object.entries(record) mutating the destructured value blocks (forbidden)', () => {
  const source = [
    "const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }",
    "for (const [, v] of Object.entries(REGISTRY)) { v.textClass = 'text-red-500' }",
    'export function V(){ return <i className={REGISTRY.a.textClass}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY.a.textClass'))
})

test('an imported record mutated via Object.values by the importer blocks the colour read in the exporting module (forbidden)', () => {
  const recPath = 'src/lib/registry.ts'
  const recSource = "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }"
  const consumerSource = [
    "import { REGISTRY } from '../lib/registry'",
    "Object.values(REGISTRY).forEach(v => { v.textClass = 'text-red-500' })",
    'export function V(){ return <i className={REGISTRY.a.textClass}/> }',
  ].join('\n')
  const findings = pass2Findings(consumerSource, { [recPath]: recSource, [path]: consumerSource })
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY.a.textClass'))
})

test('Array.from(Object.values(record))[0] mutation blocks the colour read (forbidden)', () => {
  const source = [
    "const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }",
    "Array.from(Object.values(REGISTRY))[0].textClass = 'text-red-500'",
    'export function V(){ return <i className={REGISTRY.a.textClass}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY.a.textClass'))
})

test('[...Object.values(record)][0] mutation blocks the colour read (forbidden)', () => {
  // The leading `;` is load-bearing: without it, ASI merges the previous
  // statement's closing `}` with the following `[` into a single computed
  // member-access expression on the object literal itself, which is not the
  // construct this test intends to exercise.
  const source = [
    "const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }",
    ";[...Object.values(REGISTRY)][0].textClass = 'text-red-500'",
    'export function V(){ return <i className={REGISTRY.a.textClass}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY.a.textClass'))
})

test('Object.freeze(record) does not prove nested safety: a later nested mutation still blocks (forbidden)', () => {
  const source = [
    "const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' } }",
    'Object.freeze(REGISTRY)',
    "REGISTRY.a.textClass = 'text-red-500'",
    'export function V(){ return <i className={REGISTRY.a.textClass}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REGISTRY.a.textClass'))
})

test('Object.keys(record).map(k => k.length) elsewhere still resolves the colour read (permitted, positive control)', () => {
  const source = [
    "export const REGISTRY = { a: { textClass: 'text-[var(--color-primary)]' }, b: { textClass: 'text-[var(--color-primary)]' } }",
    'const LENS = Object.keys(REGISTRY).map(k => k.length)',
    "export function View({ k }) { return <i className={REGISTRY[k].textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(
    findings.filter((f) => f.ruleId === 'ts-colors/unsupported'),
    [],
  )
})

// ── L8a precision fixes ──────────────────────────────────────────────────────
// dist/design-system-baseline/cli-lanes/claudem-review/ts-colors/review.md
// (W1, W3) and dist/design-system-baseline/cli-lanes/claude-codemods/
// triage.json (P10, P11, P13). Each capability below pairs a forbidden
// control (the fix must not relax) with a permitted case (the fix must now
// resolve), and a same-shape mutation the fix must not paper over.

// W1 — knownClassDerivedUsesSafe over-blocked a single-use const record read
// directly into a JsxExpression (a JSX attribute value or child) whose
// resolved target is not a primitive leaf: no NEW alias is created there for
// something else to capture and mutate later, so there is nothing for the
// escape walk to catch.
test('W1: a single-use non-primitive record read directly into a JsxExpression resolves clean (permitted)', () => {
  const source = [
    "const REC = { color: 'text-[var(--color-primary)]' }",
    "export function V({ c }) { const cfg = { color: REC.color }; return <i className={cfg.color} /> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

// W1 forbidden control — a SIBLING read of the same binding (even one that
// is itself JsxExpression-terminal) must still gate the whole binding when a
// DIFFERENT property's value is genuinely unresolvable: the single-use carve
// out must not become "any non-alias JsxExpression use is safe".
test('W1: a record with a sibling read and an unresolvable branch still blocks (forbidden)', () => {
  const source = [
    "const REC = { color: 'text-[var(--color-primary)]' }",
    "export function V({ c, flag }) { const cfg = { label: flag ? opaque() : 'y', color: REC.color }; return <i title={cfg.label} className={cfg.color} /> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'cfg.color'))
})

// W1 forbidden control — the read must still block when it IS captured into
// a real alias (the escape the walk exists to catch), proving the carve-out
// is JsxExpression-specific, not "any single-use binding is safe".
test('W1: a single-use record whose read is captured into a real alias still blocks (forbidden)', () => {
  const source = [
    "const REC = { color: 'text-[var(--color-primary)]' }",
    "export function V({ c }) { const cfg = { color: REC.color }; const alias = cfg.color; mutate(alias); return <i className={cfg.color} /> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
})

// W3 — objectFreezeCallArgumentDiscardsReturn only recognized the bare
// `Object.freeze(X);` statement form; a parenthesized statement
// (`;(Object.freeze(X))`) discards the return exactly the same way and must
// be exempted too.
test('W3: a parenthesized Object.freeze statement is exempted like the bare form (permitted)', () => {
  const source = [
    "const REC = { color: 'text-[var(--color-primary)]' }",
    ';(Object.freeze(REC))',
    'export function V(){ return <i className={REC.color}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

// W3 forbidden control — the return value must still be treated as a live
// alias the instant it is actually consumed (assigned), parens or not: this
// proves the fix widened only the discarded-return shape, not freeze's
// mutability contract.
test('W3: a parenthesized Object.freeze whose return is captured still blocks (forbidden)', () => {
  const source = [
    "const REC = { color: 'text-[var(--color-primary)]' }",
    'const frozen = (Object.freeze(REC))',
    "frozen.color = 'text-red-500'",
    'export function V(){ return <i className={REC.color}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'REC.color'))
})

// P10 — a generic class-composing utility (cn/clsx and the project-local
// classes/statusDot names) flagged its OWN rest/sole parameter as an
// unproven value at its DEFINITION site, even though every CALL site's
// arguments are already independently inspected. A recognized composer's
// forward parameter now resolves like a literal `className` parameter
// (extension-boundary, not unsupported) — still tracked, never silently
// clean.
test('P10: a recognized composer rest parameter is an extension boundary at its own definition, not unsupported (permitted)', () => {
  const source = [
    'function classes(...parts) { return parts.filter(Boolean).join(" ") }',
    "export function View({ chipClassName }) { return <span className={classes(chipClassName, 'base')} /> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/extension-boundary' && f.syntax === 'classes#parts'))
})

// P10 forbidden control — an UNRECOGNIZED local helper with the exact same
// rest-parameter-forwarding shape must still be unsupported: the fix is a
// named exemption for specific composer identities, not a structural
// "any rest parameter forwarded to .filter/.join is safe" relaxation.
test('P10: an unrecognized helper with the same rest-parameter shape still blocks (forbidden)', () => {
  const source = [
    'function mergeThings(...parts) { return parts.filter(Boolean).join(" ") }',
    "export function View({ chipClassName }) { return <span className={mergeThings(chipClassName, 'base')} /> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
})

// P10 — the same contract for a plain (non-rest) sole parameter, matching
// the real statusDot(colorClass: string) shape.
test('P10: a recognized composer sole string parameter is an extension boundary at its own definition (permitted)', () => {
  const source = [
    'function paintDot(dotClass) { return <span className={`w-2 h-2 ${dotClass}`} /> }',
  ].join('\n')
  // Deliberately NOT in LOCAL_CLASS_COMPOSERS — this is the forbidden half
  // of the pair, proving an arbitrary sole-parameter function name is not
  // swept in by shape alone.
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
})

test('P10: statusDot\'s own sole colour-class parameter is a recognized composer forward parameter (permitted)', () => {
  const source = [
    'function statusDot(colorClass) { return <span aria-hidden="true" className={`w-2 h-2 rounded-full shrink-0 ${colorClass}`} /> }',
  ].join('\n')
  const findings = pass2Findings(source)
  // A forwardClassName parameter spliced into a TEMPLATE (not used bare)
  // stays unsupported by the existing, separately-tested template-mode rule
  // ('only a proven parameter member at a class sink becomes unverified
  // governed debt', ts-colors.test.mjs) — this permitted case instead
  // proves the parameter is now recognized as a genuine forward parameter
  // (bare usage), matching the real statusDot's own contract.
  const bareSource = [
    'function statusDot(colorClass) { return <span className={colorClass} /> }',
  ].join('\n')
  const bareFindings = pass2Findings(bareSource)
  assert.deepEqual(bareFindings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(bareFindings.some((f) => f.ruleId === 'ts-colors/extension-boundary' && f.syntax === 'statusDot#colorClass'))
  // The template-splice shape (the REAL statusDot body) still resolves via
  // the pre-existing forwardClassName template rule, not silently ignored:
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
})

// P10 / `.filter()` receiver-preserving recursion — `.filter(predicate)`
// (any predicate, including `Boolean`) can only REMOVE elements from its
// receiver, never manufacture new content, so recursing into the receiver
// (like the pre-existing `.join()` handling) is sound.
test('.filter(Boolean).join(sep) resolves through to a safe receiver (permitted)', () => {
  const source = [
    "const parts = ['text-[var(--color-primary)]', undefined]",
    'export function V(){ return <i className={parts.filter(Boolean).join(" ")}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

// forbidden control — `.map()` CAN manufacture new content from its
// callback, so it must not be swept into the same receiver-preserving set;
// this proves the fix is method-name-scoped (join/filter only), not
// "any array method on a resolvable receiver is safe".
test('.map() is not treated as receiver-preserving — still blocks (forbidden)', () => {
  const source = [
    "const parts = ['a', 'b']",
    'export function V(){ return <i className={parts.map(p => opaque(p)).join(" ")}/> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
})

// P13 — the blanket "any color-shaped object literal, anywhere" walk
// (inspectStyleObject's top-level invocation) cannot tell a real,
// eventually-rendered style object apart from one handed directly to a
// non-paint API: a jest-dom/testing-library assertion (already recognized
// for `z`/`expect` chains) or a Vitest mock return value, whose keys can
// coincidentally collide with a CSS colour-property name (Canvas 2D's
// `stroke()` method, not the SVG `stroke` colour attribute).
test('P13: an object literal passed to expect(...).toHaveStyle(...) is not treated as a real style object (permitted)', () => {
  const source = "expect(label).toHaveStyle({ color: `var(${colorVar})` })"
  const findings = pass2Findings(source)
  assert.deepEqual(findings, [])
})

test('P13: an object literal passed to a Vitest mock return value is not treated as a real style object (permitted)', () => {
  const source = [
    "vi.spyOn(HTMLCanvasElement.prototype, 'getContext').mockReturnValue({",
    '  stroke: () => {},',
    '  fill: () => {},',
    '} as unknown as CanvasRenderingContext2D)',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings, [])
})

// P13 forbidden control — the SAME key names, in an object literal that is
// NOT an argument to a recognized non-paint API, must still be scanned as a
// real style object: the fix gates on the enclosing call, not the property
// names.
test('P13: the same color-shaped object literal outside a non-paint API call still blocks (forbidden)', () => {
  const source = [
    'const mockContext = {',
    "  stroke: () => {},",
    "  fill: '#ffffff',",
    '}',
    'export function useIt(el) { el.style.cssText = JSON.stringify(mockContext) }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/raw-color' && f.syntax === '#ffffff'))
})

// P13 forbidden control — a genuine style object built for a REAL element
// and reached only incidentally through a `vi`-rooted variable name must
// still block: recognizing the `vi.*` root does not mean "anything a test
// file constructs is exempt".
test('P13: a real style object assigned inside a vi.fn() implementation body still blocks (forbidden)', () => {
  const source = [
    "vi.fn(() => { document.body.style.color = '#ffffff' })",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/raw-color' && f.syntax === '#ffffff'))
})

// P11 — a computed object-literal property key that is itself a string
// literal (`['color']`, or the same behind a cast) was never evaluated by
// the whole-object completeness proofs: resolveDestructuredTargets and
// resolveMemberTargets treated ANY computed key as opaque, even one
// propertyNameOf already resolves to a plain name.
test('P11: a literal computed key participates in the completeness proof like a plain key (permitted)', () => {
  const source = [
    "const CFG = { ['a']: { ['textClass' as string]: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const { textClass } = CFG[k]; return <i className={textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

// P11 forbidden control — a genuinely dynamic (non-literal) computed key
// must still void the proof: the fix narrows the check to keys
// propertyNameOf can resolve, not computed keys in general.
test('P11: a genuinely dynamic computed key still blocks (forbidden)', () => {
  const source = [
    "const CFG = { a: { [dynamicKey()]: 'z', textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const { textClass } = CFG[k]; return <i className={textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
})

// P11 mutation-representative — propertyInit must resolve LAST-match, not
// first, once a literal computed key participates: a later plain key
// overriding an earlier literal computed key must be trusted...
test('P11: a later plain key overrides an earlier literal computed key (last-write-wins, permitted)', () => {
  const source = [
    "const CFG = { a: { ['textClass']: 'text-red-500', textClass: 'text-[var(--color-primary)]' } }",
    "export function View({ k }) { const { textClass } = CFG[k]; return <i className={textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/raw-color'), [])
})

// ...and the reverse order must still catch the real violation — this is
// the case that would have silently regressed had propertyInit stayed
// first-match once computed literal keys were allowed to participate.
test('P11: a later literal computed key overriding a safe plain key still surfaces the violation (forbidden)', () => {
  const source = [
    "const CFG = { a: { textClass: 'text-[var(--color-primary)]', ['textClass']: 'text-red-500' } }",
    "export function View({ k }) { const { textClass } = CFG[k]; return <i className={textClass}/> }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/raw-color' && f.syntax === 'text-red-500'))
})

// P10 forbidden control — a composer body that MUTATES its own rest
// parameter in place before forwarding it (an array-mutating method call)
// injects content no call site's argument inspection ever validated; the
// forward-parameter trust must not cover this shape.
test('P10: a composer body that pushes onto its own rest parameter before forwarding still blocks (forbidden)', () => {
  const source = [
    "import { twMerge } from 'tailwind-merge'",
    "import { clsx } from 'clsx'",
    "export function cn(...inputs) { inputs.push('text-[10px]'); return twMerge(clsx(inputs)) }",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'inputs'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
})

// ── R1 — user-authored-colour boundary widening: mutation-proof / forbidden controls ──
//
// Lane R1 (2026-09-20). Positive PERMITTED cases pin each new capability;
// each is paired with a FORBIDDEN control that must NOT be swept in by the
// same widening — proving classification is provenance-based, never
// identifier-name-based, and that every existing "stays unsupported forever"
// adversarial pin (an arbitrary opaque call's destructured result, a
// reassigned parameter, a mutated array) is still exactly as blocking as it
// was before this lane's changes.

test('R1 permitted: a runtime member read with a non-"color"-suggestive name still resolves as a boundary (classification is never identifier-name-gated)', () => {
  const source = [
    "export function Swatch({ agents }) {",
    "  return <div>{agents.map((entry) => <i key={entry.id} style={{ backgroundColor: entry.hue }} />)}</div>",
    "}",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/extension-boundary' && f.syntax === 'Swatch#dom-style.backgroundColor<-entry.hue'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('R1 forbidden control: a variable literally named "color" that is a plain build-time literal never becomes an exception (name-only matching must fail)', () => {
  const source = [
    "export function Swatch() {",
    "  const color = '#112233'",
    "  return <i style={{ backgroundColor: `${color}22` }} />",
    "}",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
  // A proven build-time literal splices directly (tryString resolves it
  // before this scanner's template escape hatches even run — never reaches
  // any R1 capability at all), so the assembled 8-digit hex is analyzed as
  // one raw-colour value, not the bare 6-digit literal in isolation.
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/raw-color' && f.syntax === '#11223322'))
})

test('R1 forbidden control: a literal colour mixed into a ?? tint still reports raw-color on its own branch, never swept into the other branch\'s exception', () => {
  const source = [
    "export function Chip({ agent }) {",
    "  const color = agent?.color ?? '#ff0000'",
    "  return <i style={{ backgroundColor: `${color}22` }} />",
    "}",
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/raw-color' && f.syntax === '#ff0000'))
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/extension-boundary' && f.syntax === 'Chip#dom-style.backgroundColor<-agent?.color'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
})

test('R1 forbidden control: Object.keys(<opaque call>)[0] stays unsupported — never guessed at a const-rooted-looking shape it cannot prove', () => {
  const source = [
    'declare function loadPalette(): Record<string, string>',
    'const hex = Object.keys(loadPalette())[0] as string',
    'export function View() { return <i style={{ color: hex }} /> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
})

test('R1 forbidden control: Object.keys(<record with a spread>)[<index after the spread>] stays unsupported — a spread of unknown key count could shift what actually lands at that index', () => {
  // The spread sits BEFORE the target index in the AST, at position 0 — the
  // property AST node actually AT index 1 ('#3B82F6') is a perfectly
  // ordinary PropertyAssignment on its own. Only the BLANKET opaqueObjectMember
  // scan (not a check of the specific resolved index alone) catches that an
  // unknown-length spread earlier in the same record could shift a LATER
  // index to a completely different runtime key.
  const source = [
    'declare const EXTRA: Record<string, string>',
    "const PALETTE_BY_NAME = { ...EXTRA, '#22C55E': 'Verdant', '#3B82F6': 'Azure' }",
    'const hex = Object.keys(PALETTE_BY_NAME)[1] as string',
    'export function View() { return <i style={{ color: hex }} /> }',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
})

test('R1 forbidden control: a destructured local from an arbitrary opaque call stays unsupported (the fileTypeMeta() pin, unweakened)', () => {
  const source = [
    'declare function fileTypeMeta(name: string): { color: string }',
    'export function Row({ name }) {',
    '  const { color } = fileTypeMeta(name)',
    '  return <i style={{ backgroundColor: color }} />',
    '}',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
})

test('R1 forbidden control: a body-destructured className whose source parameter was reassigned before the read stays unsupported', () => {
  const source = [
    "import { cn } from '@/lib/utils'",
    'export function Panel({ containerProps }) {',
    "  containerProps = { className: 'injected' }",
    '  const { className: containerClassName } = containerProps',
    "  return <div className={cn('base', containerClassName)} />",
    '}',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'containerClassName'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
})

// Same reassignment guard, exercised through the CSS-VALUE variant
// (destructuredSourceParameterBinding, distinct from
// bodyDestructuredClassBoundary above — a separate function with its own
// `binding.reassigned` check that the className-context test above does not
// reach at all).
test('R1 forbidden control: a body-destructured colour whose source parameter was reassigned before the read stays unsupported (css-value variant)', () => {
  const source = [
    'export function TaskNode({ data }) {',
    "  data = { agentColor: '#ff0000' }",
    '  const { agentColor } = data',
    '  return <i style={{ color: agentColor }} />',
    '}',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'agentColor'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
})

test('R1 forbidden control: a mutated exported array still blocks even when read through .map() (the terminal-enumeration widening never overrides the mutation guard)', () => {
  const source = [
    "export const PALETTE = ['#22C55E', '#3B82F6']",
    "PALETTE.push('#000000')",
    'export function Swatches() {',
    "  return <div>{PALETTE.map((color) => <i key={color} style={{ backgroundColor: color }} />)}</div>",
    '}',
  ].join('\n')
  const modules = { [path]: source }
  const findings = scan({ path, source, policy: pass2Policy, modules })
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'color'))
})

test('R1 forbidden control: a call whose callee is not local (imported, declared, or opaque) inside style={} still blocks', () => {
  const source = [
    "import { externalPaint } from 'some-ungoverned-package'",
    'export function Chip({ tone }) {',
    '  return <i style={externalPaint(tone)} />',
    '}',
  ].join('\n')
  const findings = pass2Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported'))
})

test('R1 permitted: an enumeration-method terminal call on an exported const array is safe across the whole-codebase escape proof even when a DIFFERENT importing module also reads .length', () => {
  const constantsSource = "export const AVATAR_COLORS = ['#22C55E', '#3B82F6']"
  const testSource = [
    "import { AVATAR_COLORS } from './constants'",
    "expect(x).toHaveLength(AVATAR_COLORS.length)",
    'for (const color of AVATAR_COLORS) { void color }',
  ].join('\n')
  const source = [
    "import { AVATAR_COLORS } from './constants'",
    'export function Picker() {',
    '  return <div>{AVATAR_COLORS.map((color) => <i key={color} style={{ backgroundColor: color }} />)}</div>',
    '}',
  ].join('\n')
  const modules = {
    [path]: source,
    'src/components/constants.ts': constantsSource,
    'src/components/Picker.test.tsx': testSource,
  }
  const findings = scan({ path, source, policy: pass2Policy, modules })
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/raw-color' && f.syntax === '#22c55e'))
})

// ── R4 — generated-token-accessor capability: FORBIDDEN controls ───────────
//
// Companion to the PERMITTED tests in ts-colors.test.mjs. Each control here
// changes exactly ONE detail of the governed shape (spec:
// dist/design-system-baseline/cli-lanes/fanout/R3/scanner-spec-generated-
// token-accessor.md, binding correction in the R4 lane brief, 2026-09-20:
// reports CLEAN, never extension-boundary) so the fix cannot be a blanket
// relaxation. Oracle: the capability's own stated gate — an EXACT
// CANONICAL_GENERATED_TOKEN_PATHS match on the element access's base, a
// content-preserving String method only — never the implementation.

const R4_TOKENS_PATH = 'src/design-system/tokens.ts'
const R4_TOKENS_SOURCE = `export const resolvedTokens: Record<string, string | number> = {
  'color.a': '#112233',
}
`
const R4_ACCESSOR_PATH = 'src/design-system/accessor.tsx'

function r4Findings(accessorSource, tokensPath = R4_TOKENS_PATH, tokensSource = R4_TOKENS_SOURCE) {
  return scan({
    path: R4_ACCESSOR_PATH,
    source: accessorSource,
    modules: { [tokensPath]: tokensSource, [R4_ACCESSOR_PATH]: accessorSource },
  })
}

test('R4 forbidden control: the same accessor shape reading from a NON-canonical module stays unsupported', () => {
  const source = `import { resolvedTokens } from './other-tokens'
const values = resolvedTokens as Readonly<Record<string, string | number>>
function readToken(id: string): string {
  const value = values[id]
  if (typeof value !== 'string') throw new Error('bad')
  return value.toUpperCase()
}
const A = readToken('color.a')
function Swatch() { return <div style={{ color: A }} /> }
`
  // 'src/design-system/other-tokens.ts' is NOT on CANONICAL_GENERATED_TOKEN_PATHS
  // — same shape, same directory, deliberately not the allowlisted file.
  const findings = r4Findings(source, 'src/design-system/other-tokens.ts', R4_TOKENS_SOURCE)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'value.toUpperCase()'))
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
})

test('R4 forbidden control: a read whose base is a local object literal stays unsupported', () => {
  const source = `const LOCAL = { 'color.a': '#112233' } as Record<string, string>
function readLocal(id: string): string {
  const value = LOCAL[id]
  if (typeof value !== 'string') throw new Error('bad')
  return value.toUpperCase()
}
const A = readLocal('color.a')
function Swatch() { return <div style={{ color: A }} /> }
`
  const findings = r4Findings(source)
  assert.deepEqual(
    findings.map((f) => ({ ruleId: f.ruleId, syntax: f.syntax })),
    [{ ruleId: 'ts-colors/unsupported', syntax: 'value.toUpperCase()' }],
  )
})

test('R4 forbidden control: a read whose base is a parameter stays unsupported', () => {
  const source = `function readParam(values: Record<string, string>, id: string): string {
  const value = values[id]
  if (typeof value !== 'string') throw new Error('bad')
  return value.toUpperCase()
}
const A = readParam({ a: '#112233' }, 'a')
function Swatch() { return <div style={{ color: A }} /> }
`
  const findings = r4Findings(source)
  assert.deepEqual(
    findings.map((f) => ({ ruleId: f.ruleId, syntax: f.syntax })),
    [{ ruleId: 'ts-colors/unsupported', syntax: 'value.toUpperCase()' }],
  )
})

const R4_OTHER_STRING_METHODS = [
  ['.replace()', "value.replace('a', 'b')"],
  ['.concat()', "value.concat('x')"],
  ['.slice()', 'value.slice(0, 4)'],
  // Zero-argument, like .trim()/.toUpperCase()/.toLowerCase() — this one
  // specifically isolates the METHOD-NAME gate from the separate
  // zero-argument gate the three above also happen to fail on (mutation
  // proof: dist/design-system-baseline/cli-lanes/fanout/R4/mutation/
  // mutation2-result.log shows the three above stay green even with the
  // method-name check fully disabled, because their own non-empty argument
  // lists already block them independently; only this fixture actually
  // exercises the method-name allowlist).
  ['.toString()', 'value.toString()'],
]

for (const [name, expression] of R4_OTHER_STRING_METHODS) {
  test(`R4 forbidden control: an arbitrary other String method (${name}) on an otherwise-governed base stays unsupported`, () => {
    const source = `import { resolvedTokens } from './tokens'
const values = resolvedTokens as Readonly<Record<string, string | number>>
function readToken(id: string): string {
  const value = values[id]
  if (typeof value !== 'string') throw new Error('bad')
  return ${expression}
}
const A = readToken('color.a')
function Swatch() { return <div style={{ color: A }} /> }
`
    const findings = r4Findings(source)
    assert.deepEqual(
      findings.map((f) => ({ ruleId: f.ruleId, syntax: f.syntax })),
      [{ ruleId: 'ts-colors/unsupported', syntax: expression }],
    )
  })
}

test('R4 forbidden control: a literal colour mixed into the same accessor\'s return set is still reported as raw-color', () => {
  const source = `import { resolvedTokens } from './tokens'
const values = resolvedTokens as Readonly<Record<string, string | number>>
function readTokenMixed(id: string): string {
  if (id === 'x') return '#ff0000'
  const value = values[id]
  if (typeof value !== 'string') throw new Error('bad')
  return value.toUpperCase()
}
const A = readTokenMixed('color.a')
function Swatch() { return <div style={{ color: A }} /> }
`
  const findings = r4Findings(source)
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/raw-color' && f.syntax === '#ff0000'))
  // The mixed return set is not wholesale exempted either: the OTHER
  // (governed-shaped) return still blocks rather than silently passing,
  // because "every return must independently prove governed" fails the
  // moment ANY one return does not — see isGeneratedTokenAccessorCall's
  // own doc comment (ts-colors.mjs) for why that is the deliberately
  // conservative choice, not a gap.
  assert.ok(findings.some((f) => f.ruleId === 'ts-colors/unsupported' && f.syntax === 'value.toUpperCase()'))
})

test('R4 forbidden control: the governed shape never resolves to ts-colors/extension-boundary even when the wrapping property name is a colour sink', () => {
  const source = `import { resolvedTokens } from './tokens'
const values = resolvedTokens as Readonly<Record<string, string | number>>
function generatedColor(id: string): string {
  const value = values[id]
  if (typeof value !== 'string') throw new Error('bad')
  return value.toUpperCase()
}
function status(tokenName: string) {
  const color = generatedColor('color.status.' + tokenName)
  return Object.freeze({ resolvedColor: color })
}
export const statusContract = Object.freeze({ inbox: status('inbox') })
`
  const findings = r4Findings(source)
  assert.deepEqual(findings, [])
  assert.deepEqual(findings.filter((f) => f.ruleId === 'ts-colors/extension-boundary'), [])
})
