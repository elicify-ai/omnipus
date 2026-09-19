// Design-system lock scanner tests — ts-colors.mjs (TypeScript AST colours).
//
// Specification sources (expected values derive from these, never from the
// implementation under test):
//   - design-system/enforcement/contract.json — scannerApi, finding shape,
//     canonical syntax without position/trivia, failClosed (parse failures and
//     unsupported governed syntax are explicit findings, never empty success),
//     policy.tokenCssNames / resolvedTokens, scanners never apply exceptions.
//   - docs/internal/design/design-system-definition.md E1 — a colour that is
//     not a token fails the lock; D3 — undefined token names are illegal;
//     D4 — default Tailwind hue utilities are unregistered colours.
//   - docs/internal/design/design-system-foundation-policy.md forced-colour
//     system palette (Canvas / Highlight / …) is a permitted CSS keyword
//     boundary, not an invented brand colour.
//
// Fixtures are inline sources. Oracle values are the colours/utilities written
// into those fixtures, canonicalised as parsed declarations (lowercase hex,
// collapsed var() names, Tailwind utility tokens) or printed AST for
// unsupported expressions.

import assert from 'node:assert/strict'
import test from 'node:test'

import { CANONICAL_GENERATED_TOKEN_PATHS, extensions, scan } from '../../scripts/design-system-locks/ts-colors.mjs'
import { CANONICAL_GENERATED_TOKEN_PATHS as AUDIT_CANONICAL_GENERATED_TOKEN_PATHS } from '../../scripts/design-system-locks/audit.mjs'

const DEFAULT_PATH = 'src/features/fixture.tsx'

const POLICY = {
  tokenCssNames: ['--color-status-done', '--primitive-color-gold', 'color-surface'],
  resolvedTokens: {
    'color.status.done': '#10B981',
    'primitive.color.gold': '#D4AF37',
    'color.surface': '#0A0A0B',
  },
}

function scanSource(source, path = DEFAULT_PATH, policy = POLICY, modules) {
  return scan({ path, source, policy, modules })
}

test('resolves governed static imports and pure helper paint returns without name allowlists', () => {
  const moduleSource = `
    export const tones = { quiet: 'text-[var(--color-status-done)]', loud: 'text-red-500' }
    export function choose(active) { return active ? tones.quiet : tones.loud }
  `
  const source = `import { tones as palette, choose as classes } from '@/lib/arbitrary'; export const A = ({ active }) => <><i className={palette.quiet}/><b className={classes(active)}/></>`
  const findings = scanSource(source, DEFAULT_PATH, POLICY, { [DEFAULT_PATH]: source, 'src/lib/arbitrary.ts': moduleSource })
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === 'text-red-500'))
  assert.ok(!findings.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
})

test('missing and cyclic governed module resolution fails closed', () => {
  const missing = `import { tone } from '@/missing'; export const A = () => <i className={tone}/>`
  assert.ok(scanSource(missing, DEFAULT_PATH, POLICY, { [DEFAULT_PATH]: missing }).some((finding) => finding.ruleId === 'ts-colors/unsupported'))
  const source = `import { a } from '@/lib/a'; export const A = () => <i className={a}/>`
  const modules = { [DEFAULT_PATH]: source, 'src/lib/a.ts': `import { b } from './b'; export const a = b`, 'src/lib/b.ts': `import { a } from './a'; export const b = a` }
  assert.ok(scanSource(source, DEFAULT_PATH, POLICY, modules).some((finding) => finding.ruleId === 'ts-colors/unsupported'))
})

test('module caches are isolated by the immutable governed-source snapshot identity', () => {
  const source = `import { tone } from '@/lib/tone'; export const A = () => <i className={tone}/>`
  const cleanModules = Object.freeze({ [DEFAULT_PATH]: source, 'src/lib/tone.ts': `export const tone = 'text-[var(--color-status-done)]'` })
  const rawModules = Object.freeze({ [DEFAULT_PATH]: source, 'src/lib/tone.ts': `export const tone = 'text-red-500'` })
  assert.deepEqual(scanSource(source, DEFAULT_PATH, POLICY, cleanModules), [])
  assert.ok(scanSource(source, DEFAULT_PATH, POLICY, rawModules).some((finding) => finding.ruleId === 'ts-colors/raw-color'))
})

test('class logical guards skip only values proven boolean', () => {
  const booleanGuard = `export const A = ({ enabled }: { enabled: boolean }) => <i className={enabled && 'text-[var(--color-status-done)]'}/>`
  assert.deepEqual(scanSource(booleanGuard), [])
  const stringGuard = `export const A = ({ prefix }: { prefix: string }) => <i className={prefix && 'text-[var(--color-status-done)]'}/>`
  assert.ok(scanSource(stringGuard).some((finding) => finding.ruleId === 'ts-colors/unsupported' && finding.syntax === 'prefix'))
})

test('non-paint boolean class-builder values and conditional geometry styles stay clean', () => {
  const source = `
    const active = value !== undefined
    export const A = () => <i className={cn(active, active && 'text-[var(--color-status-done)]')} style={active ? { paddingLeft: 14 } : undefined}/>
  `
  assert.deepEqual(scanSource(source), [])
})

// FIX-S (2026-09-20): `z` now requires binding proof — zod has no ambient
// global form (unlike vitest's `expect`/`vi`, real under `test.globals =
// true` with no import line at all), so every real call site imports it
// explicitly (matches src/lib/api/generated/schemas.ts's real
// `z.string().regex(/^#[0-9A-Fa-f]{6}$/)` shape). `expect` stays ambient
// here on purpose — real *.test.tsx files commonly use it with no import.
test('schema and assertion APIs that validate colour-shaped strings are not paint', () => {
  const source = `
    import { z } from 'zod'
    export const schema = { color: z.string().regex(/^#[0-9A-Fa-f]{6}$/) }
    expect(value.color).toEqual(expect.stringMatching(/^#[0-9A-Fa-f]{6}$/))
  `
  assert.deepEqual(scanSource(source), [])
})

test('a local cva result call is governed by its statically scanned variant definition', () => {
  const source = `const variants = cva('text-[var(--color-status-done)]', { variants: { tone: { bad: 'text-red-500' } } }); export const A = () => <i className={variants({ tone: 'bad' })}/>`
  const findings = scanSource(source)
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === 'text-red-500'))
  assert.ok(!findings.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
})

test('a dynamic selection from a static class configuration scans every possible branch', () => {
  const source = `const tones = { quiet: { textClass: 'text-[var(--color-status-done)]' }, loud: { textClass: 'text-red-500' } }; export const A = ({ tone }) => { const config = tones[tone]; return <i className={config.textClass}/> }`
  const findings = scanSource(source)
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === 'text-red-500'))
  assert.ok(!findings.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
})

test('direct runtime paint reads become exact blocking boundaries at proven receivers', () => {
  const source = `export function Avatar({ agent }) { return <><i style={{ backgroundColor: agent.color }}/><svg><path fill={agent.color}/></svg></> }`
  assert.deepEqual(
    scanSource(source).filter((finding) => finding.ruleId === 'ts-colors/extension-boundary').map((finding) => finding.syntax),
    ['Avatar#dom-style.backgroundColor<-agent.color', 'Avatar#svg-attribute.fill<-agent.color'],
  )
})

test('direct canvas paint reads use the stable receiving function and canvas property', () => {
  const findings = scanSource(`export function draw(context, palette) { context.fillStyle = palette.ink }`)
  assert.deepEqual(
    findings.filter((finding) => finding.ruleId === 'ts-colors/extension-boundary').map((finding) => finding.syntax),
    ['draw#canvas-paint.fillStyle<-palette.ink'],
  )
})

test('runtime paint boundary identity is trivia-stable and changes with receiver property or expression', () => {
  const a = scanSource(`export function Avatar({ agent }) { return <i style={{ backgroundColor: agent.color }}/> }`).find((finding) => finding.ruleId === 'ts-colors/extension-boundary')
  const formatted = scanSource(`export function Avatar({ agent }) { return <i style={{ backgroundColor: agent /* kept */ . color }}/> }`).find((finding) => finding.ruleId === 'ts-colors/extension-boundary')
  const changed = scanSource(`export function Avatar({ agent }) { return <i style={{ borderColor: agent.accent }}/> }`).find((finding) => finding.ruleId === 'ts-colors/extension-boundary')
  assert.equal(a.syntax, formatted.syntax)
  assert.equal(a.syntax, 'Avatar#dom-style.backgroundColor<-agent.color')
  assert.equal(changed.syntax, 'Avatar#dom-style.borderColor<-agent.accent')
})

test('runtime boundaries retain raw fallbacks while calls and anonymous receivers stay unsupported', () => {
  const fallback = scanSource(`export function Avatar({ agent }) { return <i style={{ backgroundColor: agent.color ?? '#ffffff' }}/> }`)
  assert.ok(fallback.some((finding) => finding.ruleId === 'ts-colors/extension-boundary'))
  assert.ok(fallback.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === '#ffffff'))
  const opaque = scanSource(`export function Avatar({ agent }) { return <i style={{ backgroundColor: normalize(agent.color) }}/> }`)
  assert.ok(opaque.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
  assert.ok(!opaque.some((finding) => finding.ruleId === 'ts-colors/extension-boundary'))
  const anonymous = scanSource(`items.map((agent) => <i style={{ backgroundColor: agent.color }}/>)`)
  assert.ok(anonymous.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
  const reassigned = scanSource(`export function Avatar(agentColor) { agentColor = normalize(agentColor); return <i style={{ backgroundColor: agentColor }}/> }`)
  assert.ok(reassigned.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
  assert.ok(!reassigned.some((finding) => finding.ruleId === 'ts-colors/extension-boundary'))
})

function findingsFor(source, ruleId, path) {
  return scanSource(source, path).filter((finding) => finding.ruleId === ruleId)
}

function syntaxes(source, ruleId, path) {
  return findingsFor(source, ruleId, path).map((finding) => finding.syntax)
}

function ruleIds(source, path) {
  return scanSource(source, path).map((finding) => finding.ruleId)
}

function assertFindingShape(finding, path) {
  assert.equal(finding.path, path)
  assert.equal(typeof finding.ruleId, 'string')
  assert.ok(finding.ruleId.length > 0, 'ruleId must be nonempty')
  assert.equal(typeof finding.syntax, 'string')
  assert.ok(finding.syntax.length > 0, 'syntax must be nonempty')
  assert.notEqual(finding.syntax.includes('\n'), true, 'canonical syntax is not the whole file')
  assert.equal(typeof finding.message, 'string')
  assert.ok(finding.message.length > 0, 'message must be nonempty')
  if (finding.line !== undefined) {
    assert.ok(Number.isInteger(finding.line) && finding.line > 0, 'line is a positive integer')
  }
  if (finding.column !== undefined) {
    assert.ok(Number.isInteger(finding.column) && finding.column > 0, 'column is a positive integer')
  }
}

// ---------------------------------------------------------------------------
// Scanner API surface
// ---------------------------------------------------------------------------

test('extensions declares JS/TS/JSX/SVG surfaces with leading dots', () => {
  assert.deepEqual([...extensions], ['.js', '.jsx', '.ts', '.tsx', '.svg'])
})

test('scan validates its inputs loudly instead of returning empty success', () => {
  assert.throws(() => scan({ source: 'const x = 1' }), /path must be a non-empty string/)
  assert.throws(() => scan({ path: DEFAULT_PATH }), /source must be a string/)
  assert.throws(() => scan({ path: '', source: 'const x = 1' }), /path must be a non-empty string/)
})

test('scan tolerates an absent policy by treating every var() as undefined', () => {
  const findings = scan({
    path: DEFAULT_PATH,
    source: "export const X = () => <div style={{ color: 'var(--color-status-done)' }} />",
  })
  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, 'ts-colors/undefined-token')
  assert.equal(findings[0].syntax, 'var(--color-status-done)')
})

test('findings carry the contract finding shape and echo the given path', () => {
  const path = 'src/features/example/Fixture.tsx'
  const findings = scanSource("export const X = () => <div style={{ color: '#FF0000' }} />", path)
  assert.equal(findings.length, 1)
  assertFindingShape(findings[0], path)
  assert.equal(findings[0].ruleId, 'ts-colors/raw-color')
})

test('empty source is a successful empty scan, not a parse failure', () => {
  assert.deepEqual(scanSource(''), [])
})

// ---------------------------------------------------------------------------
// Permitted
// ---------------------------------------------------------------------------

test('allows a registered CSS token in a style object', () => {
  assert.deepEqual(
    ruleIds("export const X = () => <div style={{ color: 'var(--color-status-done)' }} />"),
    [],
  )
})

test('allows a registered token whose policy name omitted the -- prefix', () => {
  assert.deepEqual(
    ruleIds("export const X = () => <div style={{ backgroundColor: 'var(--color-surface)' }} />"),
    [],
  )
})

test('allows currentColor and transparent SVG/CSS keywords', () => {
  const source = "export const X = () => <svg fill='currentColor'><rect stroke='transparent' /></svg>"
  assert.deepEqual(ruleIds(source), [])
})

test('allows forced-colour system palette keywords', () => {
  const source = "export const X = () => <div style={{ color: 'CanvasText', background: 'Canvas', outlineColor: 'Highlight' }} />"
  assert.deepEqual(ruleIds(source), [])
})

test('allows inherit and none in colour properties', () => {
  const source = "export const X = () => <path fill='none' style={{ color: 'inherit' }} />"
  assert.deepEqual(ruleIds(source), [])
})

test('ignores hex written only in comments', () => {
  const source = [
    '// brand hex #FF0000 is documented here',
    '/* also #00FF00 in a block comment */',
    'export const ok = 1',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

test('ignores hex that only appears inside a URL string', () => {
  const source = "export const href = 'https://example.com/docs#FF0000'"
  assert.deepEqual(ruleIds(source), [])
})

test('ignores hex mentioned in prose copy', () => {
  const source = "export const copy = 'The brand hex #0A0A0B is Deep Space Black.'"
  assert.deepEqual(ruleIds(source), [])
})

test('ignores non-colour Tailwind utilities', () => {
  const source = "export const X = () => <div className='text-sm text-left bg-cover bg-center border-2 shadow-md ring-2' />"
  assert.deepEqual(ruleIds(source), [])
})

test('allows a registered token inside a Tailwind arbitrary value', () => {
  const source = "export const X = () => <div className='bg-[var(--primitive-color-gold)]' />"
  assert.deepEqual(ruleIds(source), [])
})

test('does not treat a resolved-token hex written as a raw literal as a token', () => {
  // D3 hex table value #10B981 is still a raw colour when not referenced via var().
  const source = "export const X = () => <div style={{ color: '#10B981' }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#10b981'])
})

// ---------------------------------------------------------------------------
// Forbidden raw colours
// ---------------------------------------------------------------------------

test('flags a hex colour in a style object', () => {
  assert.deepEqual(
    syntaxes("export const X = () => <div style={{ color: '#FF0000' }} />", 'ts-colors/raw-color'),
    ['#ff0000'],
  )
})

test('flags rgb() in a style object', () => {
  assert.deepEqual(
    syntaxes("export const X = () => <div style={{ backgroundColor: 'rgb(0, 0, 0)' }} />", 'ts-colors/raw-color'),
    ['rgb(0, 0, 0)'],
  )
})

test('flags hsl() in a box-shadow value', () => {
  const source = "export const X = () => <div style={{ boxShadow: '0 4px 16px hsl(0, 0%, 0%)' }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['hsl(0, 0%, 0%)'])
})

test('flags a named CSS colour on an SVG fill attribute', () => {
  assert.deepEqual(
    syntaxes("export const X = () => <svg fill='red' />", 'ts-colors/raw-color'),
    ['red'],
  )
})

test('flags a hex on an SVG stroke attribute', () => {
  assert.deepEqual(
    syntaxes("export const X = () => <path stroke='#00FF00' />", 'ts-colors/raw-color'),
    ['#00ff00'],
  )
})

test('flags a border shorthand that embeds a hex', () => {
  const source = "export const X = () => <div style={{ border: '1px solid #ABCDEF' }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#abcdef'])
})

test('flags a default Tailwind palette utility', () => {
  assert.deepEqual(
    syntaxes("export const X = () => <div className='bg-red-500' />", 'ts-colors/raw-color'),
    ['bg-red-500'],
  )
})

test('flags a variant-prefixed Tailwind palette utility using the full class token', () => {
  assert.deepEqual(
    syntaxes("export const X = () => <div className='hover:bg-blue-600' />", 'ts-colors/raw-color'),
    ['hover:bg-blue-600'],
  )
})

test('flags text-white and fill-black as default palette utilities', () => {
  const source = "export const X = () => <svg className='text-white fill-black' />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color').sort(), ['fill-black', 'text-white'])
})

test('flags a Tailwind arbitrary hex utility', () => {
  assert.deepEqual(
    syntaxes("export const X = () => <div className='bg-[#FF00AA]' />", 'ts-colors/raw-color'),
    ['#ff00aa'],
  )
})

test('flags cn() class-builder palette utilities', () => {
  const source = "import { cn } from '@/lib/utils'\nexport const X = (on) => <div className={cn('text-blue-600', on && 'bg-red-500')} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color').sort(), ['bg-red-500', 'text-blue-600'])
})

test('flags clsx object keys that are palette utilities', () => {
  const source = "import clsx from 'clsx'\nexport const X = (on) => <div className={clsx({ 'text-red-500': on })} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['text-red-500'])
})

test('flags cva variant class lists', () => {
  const source = "import { cva } from 'class-variance-authority'\nexport const variants = cva('base', { variants: { tone: { danger: 'bg-red-500' } } })"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['bg-red-500'])
})

test('flags an undefined CSS variable in a style value', () => {
  const source = "export const X = () => <div style={{ color: 'var(--not-a-token)' }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/undefined-token'), ['var(--not-a-token)'])
})

test('flags an undefined token inside a Tailwind arbitrary value', () => {
  const source = "export const X = () => <div className='text-[var(--missing-token)]' />"
  assert.deepEqual(syntaxes(source, 'ts-colors/undefined-token'), ['var(--missing-token)'])
})

test('flags a registered var fallback that is still a raw hex', () => {
  const source = "export const X = () => <div style={{ color: 'var(--color-status-done, #FFFFFF)' }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff'])
  assert.deepEqual(syntaxes(source, 'ts-colors/undefined-token'), [])
})

// ---------------------------------------------------------------------------
// Unsupported dynamic expressions (fail closed)
// ---------------------------------------------------------------------------

test('classifies an unchanged direct paint parameter as an exact blocking boundary', () => {
  const source = 'export const X = (dynamicColor: string) => <div style={{ color: dynamicColor }} />'
  const findings = findingsFor(source, 'ts-colors/extension-boundary')
  assert.equal(findings.length, 1)
  assert.equal(findings[0].syntax, 'X#dom-style.color<-dynamicColor')
  assert.match(findings[0].message, /centrally reviewed/i)
})

test('reports an unresolved template interpolation in a class position as unsupported', () => {
  const source = 'export const X = (hue: string) => <div className={`bg-${hue}-500`} />'
  const findings = findingsFor(source, 'ts-colors/unsupported')
  assert.equal(findings.length, 1)
  assert.ok(findings[0].syntax.includes('hue'), 'printed AST names the unresolved expression')
  assert.ok(!findings[0].syntax.includes('export const X'), 'syntax is not the whole file')
})

test('reports an unresolved style spread as unsupported', () => {
  const source = 'export const X = (rest: object) => <div style={{ ...rest }} />'
  const findings = findingsFor(source, 'ts-colors/unsupported')
  assert.equal(findings.length, 1)
  assert.equal(findings[0].syntax, '...rest')
})

test('reports an unresolved class-builder argument as unsupported', () => {
  const source = "import { cn } from '@/lib/utils'\nexport const X = (foo: string) => <div className={cn(foo)} />"
  const findings = findingsFor(source, 'ts-colors/unsupported')
  assert.equal(findings.length, 1)
  assert.equal(findings[0].syntax, 'foo')
})

test('className parameters passed through JSX are classified as extension boundaries', () => {
  const source = 'export const Box = ({ className }) => <div className={className} />'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Box#className'])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
})

test('named function parameters use the receiving symbol in canonical boundary syntax', () => {
  const source = 'export function Panel({ className }) { return <section className={className} /> }'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Panel#className'])
})

test('recognized component wrappers use the receiving variable symbol', () => {
  const source = 'export const Button = React.memo(React.forwardRef(({ className }, ref) => <button ref={ref} className={className} />))'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Button#className'])
})

test('anonymous callback className parameters are unsupported rather than registry candidates', () => {
  const source = 'export const nodes = values.map((className) => <div className={className} />)'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), ['className'])
})

test('className parameters passed through class builders retain static sibling findings', () => {
  const source = "export const Box = ({ className }) => <div className={cn('bg-red-500', className)} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['bg-red-500'])
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Box#className'])
})

test('a forbidden className default is scanned before its parameter is classified', () => {
  const source = "export const Box = ({ className = 'bg-red-500' }) => <div className={className} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['bg-red-500'])
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Box#className'])
})

test('reassigned className parameters remain unsupported', () => {
  const source = "export const Box = ({ className }) => { className = 'text-red-500'; return <div className={className} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), ['className'])
})

// P4 review finding: `style` parameters passed through JSX mirror
// className's exact forwardClassName → extension-boundary contract
// (forwardStyle), including the same reassignment/anonymous-owner
// fallbacks to unsupported.
test('style parameters passed through JSX are classified as extension boundaries', () => {
  const source = 'export const Box = ({ style }) => <div style={style} />'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Box#style'])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
})

test('named function style parameters use the receiving symbol in canonical boundary syntax', () => {
  const source = 'export function Panel({ style }) { return <section style={style} /> }'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Panel#style'])
})

test('anonymous callback style parameters are unsupported rather than registry candidates', () => {
  const source = 'export const nodes = values.map((style) => <div style={style} />)'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), ['style'])
})

test('reassigned style parameters remain unsupported', () => {
  const source = "export const Box = ({ style }) => { style = { color: 'red' }; return <div style={style} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), ['style'])
})

test('a raw style object at the same JSX attribute still reports raw findings, unaffected by forwardStyle', () => {
  const source = "export const Box = () => <div style={{ color: '#ffffff' }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff'])
})

test('only a proven parameter member at a class sink becomes unverified governed debt', () => {
  const fixtures = [
    "const className = getClass(); export const Box = () => <div className={className} />",
    'export const Box = (props) => <div className={props.className} />',
    'export const Box = ({ className }) => <div className={`base ${className}`} />',
    'export const Box = () => <div className={className} />',
  ]
  for (const [index, source] of fixtures.entries()) {
    assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
    if (index === 1) {
      assert.deepEqual(syntaxes(source, 'ts-colors/unverified-governed-value'), ['Box#className<-props.className'])
      assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
    } else {
      assert.deepEqual(syntaxes(source, 'ts-colors/unverified-governed-value'), [])
      assert.ok(syntaxes(source, 'ts-colors/unsupported').length > 0)
    }
  }
})

test('nested local className bindings do not inherit parameter provenance', () => {
  const source = [
    'export const Box = ({ className }) => {',
    "  const inner = () => { const className = 'bg-red-500'; return <div className={className} /> }",
    '  return <section className={className}>{inner()}</section>',
    '}',
  ].join('\n')
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['bg-red-500'])
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Box#className'])
})

test('each unchanged className parameter use remains a distinct candidate occurrence', () => {
  const source = 'export const Box = ({ className }) => <><div className={className} /><span className={className} /></>'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Box#className', 'Box#className'])
})

test('caller-supplied className literals remain scanned at their source', () => {
  const source = [
    'const Box = ({ className }) => <div className={className} />',
    "export const Page = () => <Box className='bg-red-500' />",
  ].join('\n')
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Box#className'])
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['bg-red-500'])
})

test('does not pretend a static palette class next to a dynamic class is safe', () => {
  const source = "import { cn } from '@/lib/utils'\nexport const X = (foo: string) => <div className={cn('bg-red-500', foo)} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['bg-red-500'])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), ['foo'])
})

// ---------------------------------------------------------------------------
// Alias, computed properties, spreads, nested arrays, conditionals
// ---------------------------------------------------------------------------

test('follows a const alias into a style colour', () => {
  const source = "const accent = '#CC00CC'\nexport const X = () => <div style={{ color: accent }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#cc00cc'])
})

test('follows a transitive alias through a second binding', () => {
  const source = "const a = '#AABBCC'\nconst b = a\nexport const X = () => <div style={{ color: b }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#aabbcc'])
})

test('reads a computed colour property name', () => {
  const source = "export const X = () => <div style={{ ['backgroundColor']: '#123456' }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#123456'])
})

test('reports a dynamic computed colour key whose value is also unresolved as unsupported', () => {
  const source = 'export const X = (key: string, value: string) => <div style={{ [key]: value }} />'
  const findings = findingsFor(source, 'ts-colors/unsupported')
  assert.ok(findings.length >= 1, 'dynamic governed key/value must not look safe')
  assert.ok(findings.some((finding) => finding.syntax.includes('key') || finding.syntax.includes('value')))
})

test('inlines a same-file object spread that carries a raw colour', () => {
  const source = "const extra = { color: '#010203' }\nexport const X = () => <div style={{ ...extra }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#010203'])
})

test('flags palette utilities inside nested class-builder arrays and conditionals', () => {
  const source = "import { cn } from '@/lib/utils'\nexport const X = (on: boolean) => <div className={cn(['base', on && 'bg-orange-400', on ? 'text-lime-500' : 'border-pink-300'])} />"
  assert.deepEqual(
    syntaxes(source, 'ts-colors/raw-color').sort(),
    ['bg-orange-400', 'border-pink-300', 'text-lime-500'],
  )
})

test('follows member access into a same-file colour object', () => {
  const source = "const theme = { accent: '#998877' }\nexport const X = () => <div style={{ color: theme.accent }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#998877'])
})

// ---------------------------------------------------------------------------
// Parse failure and SVG assets
// ---------------------------------------------------------------------------

test('malformed source yields an explicit parse-error finding, never empty success', () => {
  const source = 'const x = {'
  const findings = scanSource(source)
  assert.ok(findings.length >= 1, 'parse failure must produce at least one finding')
  assert.equal(findings[0].ruleId, 'ts-colors/parse-error')
  assert.equal(typeof findings[0].syntax, 'string')
  assert.ok(findings[0].syntax.length > 0)
  assert.match(findings[0].message, /parse/i)
})

test('covers SVG assets as JSX colour attributes', () => {
  const source = '<svg xmlns="http://www.w3.org/2000/svg" fill="#ABCDEF"><path stroke="blue" /></svg>'
  const findings = scanSource(source, 'src/assets/mark.svg')
  assert.deepEqual(
    findings.filter((finding) => finding.ruleId === 'ts-colors/raw-color').map((finding) => finding.syntax).sort(),
    ['#abcdef', 'blue'],
  )
  for (const finding of findings) {
    assert.equal(finding.path, 'src/assets/mark.svg')
  }
})

test('strips an XML preamble so SVG assets are covered rather than silently ignored', () => {
  const source = '<?xml version="1.0" encoding="UTF-8"?>\n<svg fill="#FEDCBA"><path /></svg>'
  const findings = scanSource(source, 'src/assets/preamble.svg')
  assert.deepEqual(
    findings.filter((finding) => finding.ruleId === 'ts-colors/raw-color').map((finding) => finding.syntax),
    ['#fedcba'],
  )
})

test('unparseable SVG still reports parse-error rather than returning empty success', () => {
  const findings = scanSource('<svg><path fill="</svg>', 'src/assets/broken.svg')
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/parse-error'))
})

// ---------------------------------------------------------------------------
// Canonical syntax
// ---------------------------------------------------------------------------

test('canonical syntax drops comments and does not echo the whole file', () => {
  const source = "export const X = () => <div style={{ color: /* secret-hex */ '#AABB11' }} />"
  const findings = findingsFor(source, 'ts-colors/raw-color')
  assert.equal(findings.length, 1)
  assert.equal(findings[0].syntax, '#aabb11')
  assert.ok(!findings[0].syntax.includes('secret-hex'))
  assert.ok(!findings[0].syntax.includes('export const'))
  assert.ok(findings[0].syntax.length < source.length)
})

test('canonical syntax is stable across whitespace in var()', () => {
  const source = "export const X = () => <div style={{ color: 'var( --not-a-token )' }} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/undefined-token'), ['var(--not-a-token)'])
})

test('JS files with style objects are scanned', () => {
  const findings = scanSource(
    "export const style = { color: '#010101' }",
    'src/features/fixture.js',
  )
  assert.deepEqual(
    findings.filter((finding) => finding.ruleId === 'ts-colors/raw-color').map((finding) => finding.syntax),
    ['#010101'],
  )
})

// ---------------------------------------------------------------------------
// Governed paint: style tags, data-URI SVG, DOM/CSSOM/canvas assignments
// Oracle: E1 (non-token colour fails), contract failClosed, review-ts-colors.json
// ---------------------------------------------------------------------------

test('flags a hex colour inside a JSX style element', () => {
  const source = "export const X = () => <style>{'.x { color: #ffffff }'}</style>"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff'])
})

test('malformed CSS in a JSX style element fails closed instead of swallowing raw paint', () => {
  for (const css of ['.a { color: #ffffff', '.a { color: #ffffff; /*']) {
    const source = `export const X = () => <style>{${JSON.stringify(css)}}</style>`
    const findings = scanSource(source)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, 'ts-colors/unsupported')
  }
})

test('two identical paints in one JSX style literal remain separate occurrences', () => {
  const source = "export const X = () => <style>{'.a { color: #ffffff } .b { color: #ffffff }'}</style>"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff', '#ffffff'])
})

test('two identical paints in one inline declaration literal remain separate occurrences', () => {
  const source = "export const X = () => <div style={'color: #ffffff; background: #ffffff'} />"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff', '#ffffff'])
})

test('allows a registered token inside a JSX style element', () => {
  const source = "export const X = () => <style>{'.x { color: var(--color-status-done) }'}</style>"
  assert.deepEqual(ruleIds(source), [])
})

test('flags SVG paint embedded in a data-URI background image', () => {
  const source = `export const X = () => <div style={{backgroundImage: "url(data:image/svg+xml,%3Csvg%20fill='%23ffffff'%3E%3C/svg%3E)"}} />`
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff'])
})

test('ignores ordinary background-image URL fragments that look like hex', () => {
  const source = "export const X = () => <div style={{ backgroundImage: 'url(/asset.svg#ffffff)' }} />"
  assert.deepEqual(ruleIds(source), [])
})

test('flags a DOM style.color assignment', () => {
  const source = "element.style.color = '#ffffff'"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff'])
})

test('allows a registered token assigned to style.color', () => {
  const source = "element.style.color = 'var(--color-status-done)'"
  assert.deepEqual(ruleIds(source), [])
})

test('flags CSSOM setProperty of a colour', () => {
  const source = "element.style.setProperty('color', '#ffffff')"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff'])
})

test('allows CSSOM setProperty of a registered token colour', () => {
  const source = "element.style.setProperty('color', 'var(--color-status-done)')"
  assert.deepEqual(ruleIds(source), [])
})

test('ignores CSSOM setProperty of a non-colour property', () => {
  const source = "element.style.setProperty('width', '10px')"
  assert.deepEqual(ruleIds(source), [])
})

test('unrelated object spreads and computed map keys are not treated as style boundaries', () => {
  const source = [
    'const actual = { ok: true }',
    'export const response = { ...actual }',
    'export const mergeResponse = (overrides) => ({ ...overrides })',
    "const sessionId = 'abc'",
    'export const sessions = { [sessionId]: { active: true } }',
  ].join('\n')
  assert.deepEqual(ruleIds(source), [])
})

test('an unresolved spread inside a JSX style attribute still fails closed', () => {
  const source = 'export const X = ({ overrides }) => <div style={{ ...overrides }} />'
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), ['...overrides'])
})

test('flags canvas fillStyle assignment', () => {
  const source = "context.fillStyle = '#ffffff'"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff'])
})

test('flags canvas strokeStyle assignment', () => {
  const source = "context.strokeStyle = '#00ff00'"
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#00ff00'])
})

test('allows currentColor as a canvas fillStyle keyword', () => {
  const source = "context.fillStyle = 'currentColor'"
  assert.deepEqual(ruleIds(source), [])
})

test('reports unresolved CSS in a JSX style element as unsupported', () => {
  const source = 'export const X = (css: string) => <style>{css}</style>'
  const findings = findingsFor(source, 'ts-colors/unsupported')
  assert.equal(findings.length, 1)
  assert.equal(findings[0].syntax, 'css')
})

test('reports a dynamic CSSOM property name as unsupported and still sees the raw colour', () => {
  const source = "const prop = name\nelement.style.setProperty(prop, '#ffffff')"
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), ['prop'])
  assert.deepEqual(syntaxes(source, 'ts-colors/raw-color'), ['#ffffff'])
})

test('reports an undecodable SVG data URI as unsupported instead of throwing or passing', () => {
  const source = "export const X = () => <div style={{ backgroundImage: 'url(data:image/svg+xml,%ZZ)' }} />"
  const findings = scanSource(source)
  assert.ok(findings.length >= 1, 'undecodable embedded paint must not return empty success')
  assert.ok(findings.every((finding) => finding.ruleId !== 'ts-colors/parse-error'))
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
})

test('two DOM colour assignments retain two occurrences of the same syntax', () => {
  const source = "element.style.color = '#aabbcc'; other.style.color = '#aabbcc'"
  const findings = findingsFor(source, 'ts-colors/raw-color')
  assert.equal(findings.length, 2)
  assert.equal(findings[0].syntax, '#aabbcc')
  assert.equal(findings[1].syntax, '#aabbcc')
})

// ── W3-colour bullet 1: renamed class-like parameter forwards ──────────────

test('a class-like-named parameter (not literally "className") is an extension boundary', () => {
  const source = "export function SmartSelectTrigger({ triggerClassName, className }) { return <button className={cn(triggerClassName, className)} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['SmartSelectTrigger#triggerClassName', 'SmartSelectTrigger#className'])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
})

test('sheet.tsx-style "widthClass" (Class suffix, not ClassName) is an extension boundary', () => {
  const source = "export function SheetContent({ widthClass, className }) { return <div className={cn(widthClass, className)} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['SheetContent#widthClass', 'SheetContent#className'])
})

test('a destructured rename to a class-like alias reports the SOURCE property name, not the local alias', () => {
  const source = "export function Calendar({ className, classNames }) { return <DayPicker className={cn('p-3', className)} classNames={{ ...classNames, Chevron: ({ orientation, className: chevronClassName }) => <CaretRight className={chevronClassName} /> }} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['Calendar#className', 'Calendar#Chevron.className'])
})

test('a renamed class-like parameter forwarded member access (item.className) is an extension boundary at the enclosing named component', () => {
  const source = "export function SmartSelectContent({ items }) { return <div>{items.map((item) => <span key={item.value} className={item.className} />)}</div> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['SmartSelectContent#item.className'])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
})

test('a plain-parameter member forward (props.className on a named component) keeps its existing unverified-governed-value contract, not extension-boundary', () => {
  const source = 'export const Box = (props) => <div className={props.className} />'
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.deepEqual(syntaxes(source, 'ts-colors/unverified-governed-value'), ['Box#className<-props.className'])
})

test('a transformed renamed class-like parameter stays unsupported', () => {
  const source = "export function SmartSelectTrigger({ triggerClassName }) { return <button className={triggerClassName.toUpperCase()} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.ok(syntaxes(source, 'ts-colors/unsupported').length > 0)
})

test('a reassigned renamed class-like parameter stays unsupported at the reassignment-shadowed read', () => {
  const source = "export function SmartSelectTrigger({ triggerClassName }) { let forwarded = triggerClassName; forwarded = computeSomethingElse(); return <button className={forwarded} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.ok(syntaxes(source, 'ts-colors/unsupported').length > 0)
})

test('table.tsx-style "{ className } = containerProps ?? {}" stays unsupported, pinned in typography too', () => {
  const source = "export function Table({ containerProps }) { const { className: containerClassName, ...rest } = containerProps ?? {}; return <div className={cn('relative', containerClassName)} {...rest} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), [])
  assert.ok(syntaxes(source, 'ts-colors/unsupported').length > 0, 'containerClassName — a `?? {}` fallback on the destructuring source — must stay unsupported')
})

test('icon-button.tsx-style ${sizes[size]} ${className ?? \'\'}.trim() still resolves the forwarded className through .trim()', () => {
  const source = "const iconButtonSizes = { default: 'h-9 w-9', sm: 'h-7 w-7' }\nexport function IconButton({ className, size }) { return <span className={`${iconButtonSizes[size]} ${className ?? ''}`.trim()} /> }"
  assert.deepEqual(syntaxes(source, 'ts-colors/extension-boundary'), ['IconButton#className'])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
})

// ── W3-colour bullet 2: null branches ───────────────────────────────────────

const STRENGTH_HELPER = `
type Strength = { score: number; label: string; color: string }
function evaluateStrength(pw: string): Strength | null {
  if (!pw) return null
  return { score: 1, label: 'Weak', color: 'var(--color-status-done)' }
}
`

test('a null candidate excluded by an `x &&` lexical guard does not void the whole read', () => {
  const source = `${STRENGTH_HELPER}
export function Field({ password }) {
  const strength = evaluateStrength(password)
  return strength && <p style={{ color: strength.color }}>{strength.label}</p>
}`
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
})

test('a null candidate excluded by a ternary `x ? x.prop : …` guard does not void the whole read', () => {
  const source = `
const STATUS_CONFIG: Record<string, { dotColor: string }> = { running: { dotColor: 'var(--color-status-done)' } }
export function Card({ status }: { status: string | null }) {
  const cfg = status ? STATUS_CONFIG[status] : null
  return cfg ? <span style={{ backgroundColor: cfg.dotColor }} /> : null
}`
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
})

test('a null candidate excluded by an `as NonNullable<typeof x>` cast does not void the whole read', () => {
  const source = `
function fileTypeMeta(name: string) { return { color: '#0EA5E9' } }
export function Row({ mount, name }: { mount: { broad: boolean } | null; name: string }) {
  const containerIcon = mount ? { color: 'var(--color-status-done)' } : null
  const fileMeta = containerIcon ? null : fileTypeMeta(name)
  const color = containerIcon?.color ?? (fileMeta as NonNullable<typeof fileMeta>).color
  return <div style={{ color }} />
}`
  const findings = scanSource(source)
  assert.ok(!findings.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === '#0ea5e9'))
})

test('an UNGUARDED possibly-null read stays unsupported (the control every guard fixture above is proven against)', () => {
  const source = `${STRENGTH_HELPER}
export function Unguarded({ password }) {
  const strength = evaluateStrength(password)
  return <p style={{ color: strength.color }}></p>
}`
  assert.ok(syntaxes(source, 'ts-colors/unsupported').length > 0, 'an unguarded read of a possibly-null dispatcher value must stay unsupported')
})

// ── W3-colour bullet 3: finite const arrays ─────────────────────────────────

test('AVATAR_COLORS.map((color) => …) resolves the array-callback parameter to every literal element', () => {
  const source = `
const AVATAR_COLORS = ['#22C55E', '#3B82F6']
export function Picker() {
  return <>{AVATAR_COLORS.map((color) => <button key={color} style={{ backgroundColor: color }} />)}</>
}`
  const findings = findingsFor(source, 'ts-colors/raw-color')
  assert.deepEqual(findings.map((finding) => finding.syntax).sort(), ['#22c55e', '#3b82f6'])
  assert.deepEqual(syntaxes(source, 'ts-colors/unsupported'), [])
})

test('OPTIONS.filter(pred).map((o) => o.color) resolves the array element MEMBER through the .filter() hop', () => {
  const source = `
const STATUS_OPTIONS = [
  { value: 'inbox', color: 'text-[var(--color-status-done)]' },
  { value: 'next', color: 'text-red-500' },
]
export function Select({ current }: { current: string }) {
  const items = STATUS_OPTIONS.filter((o) => o.value === current || true).map((o) => ({ value: o.value, className: cn('text-xs', o.color) }))
  return items
}`
  const findings = scanSource(source)
  assert.ok(!findings.some((finding) => finding.ruleId === 'ts-colors/unsupported'))
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === 'text-red-500'))
})

test('a mutated const array ("push") stays unsupported — the escape/mutation guard applies to arrays like it does to records', () => {
  const source = `
const MUTABLE_COLORS = ['#111111']
MUTABLE_COLORS.push('#222222')
export function Bad() {
  return <>{MUTABLE_COLORS.map((color) => <i style={{ color }} />)}</>
}`
  assert.ok(syntaxes(source, 'ts-colors/unsupported').length > 0)
})

// R1 (2026-09-20 lead correction): this pin previously asserted a runtime
// (prop) array's per-element member read stays `ts-colors/unsupported`
// forever — but "handled by registration, not resolved here" (the comment
// this test was written against, near resolveStableArrayLiteralElements)
// only means anything if there is a REGISTRABLE finding to hand the
// registry: `ts-colors/unsupported` can never be baselined at all (Stage B
// contract). `agents.map((a) => ...a.color...)` is exactly the
// user-authored-colour shape the plan's exception ledger §5 names as a
// permanent governed category (a non-const array's own callback element
// reading a user-chosen colour field) — receivingSymbol's nearest-named-
// ancestor widening (R1) now correctly resolves the anonymous `.map()`
// callback's owner to the enclosing named `Team` component, producing a
// registrable `ts-colors/extension-boundary`, not a dead-end `unsupported`.
test('R1: a runtime (prop) array element member read resolves as a registrable runtime paint boundary, not a dead-end unsupported', () => {
  const source = "export function Team({ agents }) { return <>{agents.map((a) => <i style={{ backgroundColor: a.color }} />)}</> }"
  const findings = scanSource(source)
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
  assert.deepEqual(
    findings.filter((finding) => finding.ruleId === 'ts-colors/extension-boundary').map((finding) => finding.syntax),
    ['Team#dom-style.backgroundColor<-a.color'],
  )
})

// Forbidden control pinning the OTHER half of the same claim: when there is
// truly no reviewable owner anywhere in the enclosing scope chain (a
// genuinely anonymous function assigned to nothing, itself the module's
// default export with no name of its own), the boundary still correctly
// fails closed — receivingSymbol's widening only ever WIDENS which already-
// proven runtime read gets a real owner name; it never manufactures one
// where none exists.
test('R1 forbidden control: a runtime member read inside a wholly anonymous callback with no named ancestor anywhere stays unsupported', () => {
  // An IIFE assigned to nothing, exported as nothing, is functionIdentity's
  // genuine 'anonymous' case at every enclosing level — unlike a default
  // export (functionIdentity resolves an ExportAssignment parent to the
  // stable name 'default'), there is no reviewable name anywhere in the
  // scope chain for nearestNamedAncestorOwner to climb to.
  const source = ";(function (agents) { return agents.map((a) => ({ backgroundColor: a.color })) })(agentsFromSomewhere)"
  assert.ok(syntaxes(source, 'ts-colors/unsupported').length > 0)
  assert.deepEqual(findingsFor(source, 'ts-colors/extension-boundary'), [])
})

// ── W3-colour bullet 4: graph visuals / Object.fromEntries(Object.keys(…).map(…)) records ──

const STATUS_VISUALS_HELPER = `
const STATUS_COLORS = { inbox: 'var(--color-status-done)', next: '#3B82F6' }
const STATUS_VISUALS = Object.fromEntries(
  Object.keys(STATUS_COLORS).map((s) => [s, { color: STATUS_COLORS[s] }]),
) as Record<string, { color: string }>
export function statusVisual(status: string) {
  return STATUS_VISUALS[status] ?? STATUS_VISUALS.inbox
}
`

test('a dynamic-key read off an Object.fromEntries(Object.keys(X).map(…)) record resolves through the templated entry value', () => {
  const source = `${STATUS_VISUALS_HELPER}
export function Node({ status }: { status: string }) {
  const visual = statusVisual(status)
  return <i style={{ color: visual.color }} />
}`
  const findings = scanSource(source)
  // Exact-shape assertion, not just "no unsupported": the object literal
  // STATUS_VISUALS is built from (`{ color: STATUS_COLORS[s] }`) is ALSO
  // independently reached by this scanner's own blanket "any colour-shaped
  // object literal, anywhere" walk at its declaration site — so a merely
  // "no unsupported" check stays green even with THIS capability disabled
  // (proven by the mutation harness: the disabled recognizer falls to a
  // DIFFERENT pre-existing fallback, ts-colors/extension-boundary, for
  // `visual.color` itself, never ts-colors/unsupported). The real signal is
  // that `visual.color`'s OWN read produces NO extra finding at all — it
  // resolves onto the exact same node the declaration-site walk already
  // covers, deduplicated by position, down to exactly one finding total.
  assert.deepEqual(findings.map((finding) => finding.ruleId), ['ts-colors/raw-color'])
  assert.equal(findings[0].syntax, '#3b82f6')
})

test('a literal-key read off the same fromEntries record resolves through the same templated entry value', () => {
  const source = `${STATUS_VISUALS_HELPER}
export function Home() {
  return <i style={{ color: STATUS_VISUALS.inbox.color }} />
}`
  const findings = scanSource(source)
  // Same exact-shape reasoning as the dynamic-key test above — a bare
  // "no unsupported" check also stays green with the recognizer disabled
  // (it falls to ts-colors/extension-boundary instead, proven by the
  // mutation harness); the real signal is that STATUS_VISUALS.inbox.color
  // resolves onto the SAME node the declaration-site blanket walk already
  // covers, producing exactly one finding total, not an extra one for
  // this read site.
  assert.deepEqual(findings.map((finding) => finding.ruleId), ['ts-colors/raw-color'])
})

test('a non-conforming fromEntries shape (opaque key source) stays unsupported', () => {
  const source = `
declare function getPairs(): [string, { color: string }][]
const OPAQUE_RECORD = Object.fromEntries(getPairs())
export function Node({ status }: { status: string }) {
  return <i style={{ color: OPAQUE_RECORD[status].color }} />
}`
  assert.ok(syntaxes(source, 'ts-colors/unsupported').length > 0)
})

// ── R1 — user-authored-colour boundary widening ─────────────────────────────
//
// Lane R1 (2026-09-20): the plan's exception ledger (docs/internal/design/
// design-system-migration-plan.md §5) names user-authored colours (a prop,
// parameter, store/query result, or a non-const array's own callback
// element reading a user-chosen colour field) as a permanent governed
// registrable category — distinct from a value rooted in a finite,
// module-const palette, which must still resolve (raw-color/token), never
// become an exception. These tests pin: (1) the runtime-paint-boundary
// owner resolving through an anonymous render/useMemo callback to its
// nearest NAMED enclosing component/function (receivingSymbol widening);
// (2) style={} of a call to a LOCAL helper resolving through the helper's
// own returned style object(s); (3) Object.keys(<finite record>)[<literal
// index>] resolving to the key at that position; (4) a body-level
// destructured local read off a PROVEN parameter (never an arbitrary call)
// resolving as a runtime paint boundary, including the className-forward
// variant; (5) a css-mode template tint (`${x}22`) resolving a `??`/`||`
// mix, a local call to a pure finite-returning helper, and a nested-template
// return referencing the callee's own parameter under call-frame
// substitution.

test('R1: a runtime member read inside an anonymous .map() render callback resolves to the nearest named enclosing component', () => {
  const source = `
export function AgentList({ agents }) {
  return <div>{agents.map((agent) => <i key={agent.id} style={{ backgroundColor: agent.color }} />)}</div>
}`
  const findings = scanSource(source)
  assert.deepEqual(
    findings.filter((finding) => finding.ruleId === 'ts-colors/extension-boundary').map((finding) => finding.syntax),
    ['AgentList#dom-style.backgroundColor<-agent.color'],
  )
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
})

test('R1: a runtime read inside a useMemo callback body resolves to the enclosing named component, not anonymous', () => {
  const source = `
export function AgentProfile({ selectedColor }) {
  const identity = useMemo(() => ({ color: selectedColor }), [selectedColor])
  return identity
}`
  const findings = scanSource(source)
  assert.deepEqual(
    findings.filter((finding) => finding.ruleId === 'ts-colors/extension-boundary').map((finding) => finding.syntax),
    ['AgentProfile#dom-style.color<-selectedColor'],
  )
})

test("R1: style={} of a call to a local helper resolves through the helper's own returned style object(s)", () => {
  const source = `
function paint(active, tone) {
  if (active) return { backgroundColor: tone }
  return { backgroundColor: 'var(--color-muted)' }
}
export function Chip({ active, tone }) {
  return <i style={paint(active, tone)} />
}`
  const findings = scanSource(source)
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/extension-boundary' && finding.syntax === 'paint#dom-style.backgroundColor<-tone'))
})

test('R1: Object.keys(<finite record>)[<literal index>] resolves to the key at that position', () => {
  const source = `
const PALETTE_BY_NAME = { '#22C55E': 'Verdant', '#3B82F6': 'Azure' }
export function firstHex() {
  const hex = Object.keys(PALETTE_BY_NAME)[0] as string
  return { color: hex }
}`
  const findings = scanSource(source)
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === '#22c55e'))
})

test('R1: a body-level destructured local read off a proven parameter resolves as a runtime paint boundary', () => {
  const source = `
export function TaskNode({ data }) {
  const { agentColor } = data
  return <i style={{ color: agentColor }} />
}`
  const findings = scanSource(source)
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/extension-boundary' && finding.syntax === 'TaskNode#dom-style.color<-agentColor'))
})

test("R1: a body-destructured className read off a parameter resolves as its own distinct boundary, never colliding with the component's own className parameter", () => {
  const source = `
import { cn } from '@/lib/utils'
export const Table = React.forwardRef(({ className, containerProps = {} }, ref) => {
  const { className: containerClassName } = containerProps
  return <div className={cn('base', containerClassName)}><table className={cn('table', className)} /></div>
})`
  const findings = scanSource(source)
  const boundaries = findings.filter((finding) => finding.ruleId === 'ts-colors/extension-boundary').map((finding) => finding.syntax)
  assert.ok(boundaries.includes('Table#containerProps.className'))
  assert.ok(boundaries.includes('Table#className'))
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
})

test('R1: a ?? mix of a runtime member read and a finite-record value in a template tint resolves both branches independently', () => {
  const source = `
const STATUS_COLORS = { done: '#10b981', failed: '#ef4444' }
export function RollupAvatar({ item, agent }) {
  const color = (agent?.color ?? STATUS_COLORS[item.status])
  return <i style={{ backgroundColor: \`\${color}22\` }} />
}`
  const findings = scanSource(source)
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/extension-boundary' && finding.syntax === 'RollupAvatar#dom-style.backgroundColor<-agent?.color'))
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === '#10b981'))
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === '#ef4444'))
})

test('R1: a template tint of a call whose returns are all finite/const-derived resolves without an exception', () => {
  const source = `
const TASK_CANCELLED_COLOR = '#EAB308'
const STATUS_COLORS = { done: '#10b981', failed: '#ef4444' }
function statusColor(status) { return STATUS_COLORS[status] ?? STATUS_COLORS.done }
function taskDisplayColor(task) { return task.cancelled ? TASK_CANCELLED_COLOR : statusColor(task.status) }
export function TaskCard({ task }) {
  return <span style={{ backgroundColor: \`\${taskDisplayColor(task)}1a\` }} />
}`
  const findings = scanSource(source)
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/extension-boundary'), [])
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/raw-color' && finding.syntax === '#eab308'))
})

test("R1: a template tint of a call whose return is itself a template referencing the callee's own parameter resolves through the caller's argument (call-frame substitution)", () => {
  const source = `
function toTint(color) { return color.startsWith('#') ? \`\${color}2a\` : 'var(--color-surface-3)' }
export function TaskNode({ data }) {
  const { agentColor } = data
  const avatarColor = agentColor ?? 'var(--color-muted)'
  return <span style={{ backgroundColor: \`\${toTint(avatarColor)}\` }} />
}`
  const findings = scanSource(source)
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
  assert.ok(findings.some((finding) => finding.ruleId === 'ts-colors/extension-boundary' && finding.syntax === 'TaskNode#dom-style.backgroundColor<-agentColor'))
})

test('R1: AVATAR_COLORS.map((color) => ...) resolves each finite-array element as raw-color, including inside a template tint', () => {
  const source = `
export const AVATAR_COLORS = ['#22C55E', '#3B82F6']
export function AvatarColorPicker({ value, onChange }) {
  return (
    <div>
      {AVATAR_COLORS.map((color) => (
        <button
          key={color}
          onClick={() => onChange(color)}
          style={{ backgroundColor: color, boxShadow: value === color ? \`0 0 0 4px \${color}\` : undefined }}
        />
      ))}
    </div>
  )
}`
  // knownClassExportUsesSafe's whole-codebase escape proof requires
  // ctx.modules to be populated (returns false, unresolved, without it) —
  // pass the fixture as its own single-file module map so the real
  // cross-module safety proof actually runs, matching how the audit always
  // invokes this scanner.
  const findings = scanSource(source, DEFAULT_PATH, POLICY, { [DEFAULT_PATH]: source })
  assert.deepEqual(findings.filter((finding) => finding.ruleId === 'ts-colors/unsupported'), [])
  // Both read sites (the plain `backgroundColor: color` property and the
  // template's `${color}`) resolve the array-callback parameter back to the
  // SAME array-element AST nodes in AVATAR_COLORS' own declaration — findings
  // dedup by node position (emit()'s ctx.seen key), so each of the two hex
  // literals reports exactly once despite two independent consuming sites.
  assert.deepEqual(
    findings.filter((finding) => finding.ruleId === 'ts-colors/raw-color').map((finding) => finding.syntax).sort(),
    ['#22c55e', '#3b82f6'],
  )
})

// ── R4 — generated-token-accessor capability ────────────────────────────────
//
// Closes the last ts-colors/unsupported finding in the tree:
// src/design-system/status.ts's `generatedColor()`. Spec:
// dist/design-system-baseline/cli-lanes/fanout/R3/scanner-spec-generated-
// token-accessor.md; the R4 lane brief (2026-09-20) makes the outcome
// binding: CLEAN, never ts-colors/extension-boundary. Oracle: the spec's own
// stated invariant ("nothing this call can evaluate to is anything other
// than whatever the token generator itself produced for that key" — the
// audit's own CANONICAL_GENERATED_TOKEN_PATHS names exactly which modules
// count as "the token generator's own output"), not the implementation.

test('R4: the audit\'s canonical generated-token path allowlist and this scanner\'s mirror of it never drift', () => {
  // scripts/design-system-locks/audit.mjs::CANONICAL_GENERATED_TOKEN_PATHS is
  // the single authoritative list (also gates the audit's own reviewed-
  // boundary "generated-tokens" kind). This scanner keeps its own copy
  // rather than importing audit.mjs directly (see that constant's doc
  // comment in ts-colors.mjs for why) — this test is what keeps the mirror
  // honest: any future edit to either list without the other fails here.
  assert.deepEqual([...CANONICAL_GENERATED_TOKEN_PATHS].sort(), [...AUDIT_CANONICAL_GENERATED_TOKEN_PATHS].sort())
})

const GENERATED_TOKENS_MODULE_PATH = 'src/design-system/tokens.ts'
const GENERATED_TOKENS_MODULE_SOURCE = `export const resolvedTokens: Record<string, string | number> = {
  'color.a': '#112233',
  'color.b': '#445566',
}
`

test('R4 permitted: a runtime-validated element-access read off an imported canonical generated-token export, returned through .toUpperCase(), reports clean', () => {
  // The R3 spec's own minimal fixture, at a real canonical path so the
  // allowlist gate is exercised for real rather than vacuously.
  const accessorPath = 'src/design-system/accessor.tsx'
  const source = `import { resolvedTokens } from './tokens'
const values = resolvedTokens as Readonly<Record<string, string | number>>
function readToken(id: string): string {
  const value = values[id]
  if (typeof value !== 'string' || !/^#[0-9a-fA-F]{6}$/.test(value)) {
    throw new Error('missing token: ' + id)
  }
  return value.toUpperCase()
}
const A = readToken('color.a')
function Swatch() { return <div style={{ color: A }} /> }
`
  const findings = scan({ path: accessorPath, source, modules: { [GENERATED_TOKENS_MODULE_PATH]: GENERATED_TOKENS_MODULE_SOURCE, [accessorPath]: source } })
  assert.deepEqual(findings, [])
})

test('R4 permitted: status.ts\'s exact shape (module-level generatedColor() feeding a resolvedColor field) reports clean, not unsupported and not extension-boundary', () => {
  // A faithful minimal reproduction of src/design-system/status.ts's actual
  // shape: two named exports imported and aliased through an `as` cast,
  // consumed by a helper whose return feeds a `resolvedColor` property —
  // the exact property name whose "color"-suffix match is what makes
  // isColorPropertyName walk into this in the first place.
  const statusPath = 'src/design-system/status-like.ts'
  const source = `import { resolvedTokens } from './tokens'
const generatedValues = resolvedTokens as Readonly<Record<string, string | number>>
function generatedColor(id: string): string {
  const value = generatedValues[id]
  if (typeof value !== 'string' || !/^#[0-9a-fA-F]{6}([0-9a-fA-F]{2})?$/.test(value)) {
    throw new Error('generated colour token is missing or invalid: ' + id)
  }
  return value.toUpperCase()
}
function status(tokenName: string) {
  const color = generatedColor('color.status.' + tokenName)
  return Object.freeze({ resolvedColor: color })
}
export const statusContract = Object.freeze({ inbox: status('inbox') })
`
  const findings = scan({ path: statusPath, source, modules: { [GENERATED_TOKENS_MODULE_PATH]: GENERATED_TOKENS_MODULE_SOURCE, [statusPath]: source } })
  assert.deepEqual(findings, [])
})

test('R4 permitted: .trim() and .toLowerCase() are governed the same way .toUpperCase() is', () => {
  const accessorPath = 'src/design-system/accessor2.tsx'
  for (const method of ['trim', 'toLowerCase']) {
    const source = `import { resolvedTokens } from './tokens'
const values = resolvedTokens as Readonly<Record<string, string | number>>
function readToken(id: string): string {
  const value = values[id]
  if (typeof value !== 'string') throw new Error('bad')
  return value.${method}()
}
const A = readToken('color.a')
function Swatch() { return <div style={{ color: A }} /> }
`
    const findings = scan({ path: accessorPath, source, modules: { [GENERATED_TOKENS_MODULE_PATH]: GENERATED_TOKENS_MODULE_SOURCE, [accessorPath]: source } })
    assert.deepEqual(findings, [], `.${method}() should be governed the same as .toUpperCase()`)
  }
})

test('R4 permitted: a bare identifier return (no wrapping String method) off a governed element access also reports clean', () => {
  const accessorPath = 'src/design-system/accessor3.tsx'
  const source = `import { resolvedTokens } from './tokens'
const values = resolvedTokens as Readonly<Record<string, string | number>>
function readToken(id: string): string {
  const value = values[id]
  if (typeof value !== 'string') throw new Error('bad')
  return value
}
const A = readToken('color.a')
function Swatch() { return <div style={{ color: A }} /> }
`
  const findings = scan({ path: accessorPath, source, modules: { [GENERATED_TOKENS_MODULE_PATH]: GENERATED_TOKENS_MODULE_SOURCE, [accessorPath]: source } })
  assert.deepEqual(findings, [])
})
