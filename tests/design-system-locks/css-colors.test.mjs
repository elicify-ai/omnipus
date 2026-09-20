import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { extensions, scan } from '../../scripts/design-system-locks/css-colors.mjs'

const POLICY = Object.freeze({
  tokenCssNames: Object.freeze([
    '--color-primary',
    '--color-error',
    '--color-status-done',
    '--primitive-color-gold',
    '--spacing-4',
  ]),
  resolvedTokens: Object.freeze({ 'color.accent.default': '#D4AF37' }),
})

function run(source, { path = 'src/styles/fixture.css', policy = POLICY } = {}) {
  return scan({ path, source, policy })
}

/** Findings reduced to the fingerprint-relevant parts plus order. */
function syntaxes(findings) {
  return findings.map((finding) => finding.syntax)
}

function ruleIds(findings) {
  return findings.map((finding) => finding.ruleId)
}

/** One raw-color finding asserted in full contract shape. */
function assertRawColor(finding, syntax) {
  assert.equal(finding.ruleId, 'css-colors/raw-color')
  assert.equal(finding.syntax, syntax)
  assert.equal(typeof finding.message, 'string')
  assert.ok(finding.message.length > 0)
  assert.ok(Number.isInteger(finding.line) && finding.line > 0)
  assert.ok(Number.isInteger(finding.column) && finding.column > 0)
}

/** One unregistered-token finding asserted in full contract shape. */
function assertUnregisteredToken(finding, syntax) {
  assert.equal(finding.ruleId, 'css-colors/unregistered-token')
  assert.equal(finding.syntax, syntax)
  assert.equal(typeof finding.message, 'string')
  assert.ok(finding.message.length > 0)
  assert.ok(Number.isInteger(finding.line) && finding.line > 0)
  assert.ok(Number.isInteger(finding.column) && finding.column > 0)
}

/** One fail-closed embedded-SVG parser finding asserted in full contract shape. */
function assertMalformedSvg(finding) {
  assert.deepEqual(Object.keys(finding).sort(), ['column', 'line', 'message', 'path', 'ruleId', 'syntax'])
  assert.equal(finding.ruleId, 'css-colors/unsupported')
  assert.equal(finding.syntax, 'malformed data-URI SVG')
  assert.match(finding.message, /malformed decoded SVG/i)
  assert.ok(Number.isInteger(finding.line) && finding.line > 0)
  assert.ok(Number.isInteger(finding.column) && finding.column > 0)
}

describe('scanner contract', () => {
  it('handles exactly .css extensions', () => {
    assert.deepEqual(extensions, ['.css'])
  })

  it('returns findings synchronously as an array', () => {
    const result = run('a { color: red }')
    assert.ok(Array.isArray(result))
  })

  it('echoes the repository-relative path it was given', () => {
    const [finding] = run('a { color: red }', { path: 'src/styles/deep/nested.css' })
    assert.equal(finding.path, 'src/styles/deep/nested.css')
  })

  it('gives every finding exactly the contract keys', () => {
    const [finding] = run('a { color: red }')
    assert.deepEqual(Object.keys(finding).sort(), ['column', 'line', 'message', 'path', 'ruleId', 'syntax'])
  })

  it('returns an empty array for a stylesheet with no raw colors', () => {
    assert.deepEqual(run('a { color: var(--color-primary); padding: 4px }'), [])
  })

  it('returns an empty array for empty and whitespace-only sources', () => {
    assert.deepEqual(run(''), [])
    assert.deepEqual(run('\n\n  \n'), [])
  })

  it('does not mutate the policy it is given', () => {
    const policy = { tokenCssNames: ['--color-primary'] }
    Object.freeze(policy)
    Object.freeze(policy.tokenCssNames)
    run('a { color: var(--color-primary, #fff) }', { policy })
    assert.deepEqual(policy, { tokenCssNames: ['--color-primary'] })
  })

  it('is deterministic across repeated runs', () => {
    const source = 'a { background: linear-gradient(to right, var(--color-primary), #fff) }'
    assert.deepEqual(run(source), run(source))
  })
})

describe('policy validation (fail closed)', () => {
  const scanWith = (policy) => scan({ path: 'a.css', source: 'a { color: red }', policy })

  it('throws when policy is missing', () => {
    assert.throws(() => scan({ path: 'a.css', source: 'a { color: red }' }), /policy is required/)
  })

  it('throws when tokenCssNames is not a non-empty string array', () => {
    assert.throws(() => scanWith({}), /tokenCssNames/)
    assert.throws(() => scanWith({ tokenCssNames: [] }), /tokenCssNames/)
    assert.throws(() => scanWith({ tokenCssNames: '--color-primary' }), /tokenCssNames/)
    assert.throws(() => scanWith({ tokenCssNames: ['--ok', ''] }), /tokenCssNames/)
  })

  it('throws when path or source have the wrong type', () => {
    assert.throws(() => scan({ source: 'a{}', policy: POLICY }), /path/)
    assert.throws(() => scan({ path: 'a.css', source: null, policy: POLICY }), /source/)
  })
})

describe('permitted colors (no findings)', () => {
  const permitted = [
    ['registered token reference', 'a { color: var(--color-primary) }'],
    ['registered token inside a gradient', 'a { background: linear-gradient(to right, var(--color-primary), var(--color-error)) }'],
    ['currentColor keyword', 'a { color: currentColor }'],
    ['currentColor in any casing', 'a { color: CURRENTCOLOR }'],
    ['transparent keyword', 'a { background: transparent }'],
    ['inherit keyword', 'a { color: inherit }'],
    ['css-wide keywords', 'a { color: unset }'],
    ['token fallback inside var()', 'a { color: var(--color-primary, var(--color-error)) }'],
    ['relative color derived from a token', 'a { color: rgb(from var(--color-primary) r g b / 50%) }'],
    ['color-mix over tokens only', 'a { color: color-mix(in oklab, var(--color-primary), var(--color-error) 40%) }'],
    ['hex inside a string value', 'a { content: "#fff is just copy" }'],
    ['named color inside a string value', 'a[title="red herring"]::after { content: "red" }'],
    ['hex inside a comment in the value', 'a { color: /* #ffffff */ var(--color-primary) }'],
    ['unquoted url with slashes and colons', 'a { background: url(data:image/svg+xml;utf8,<svg/>) }'],
    ['external asset url is clean at this scanner boundary', "a { background-image: url('/assets/registered.svg') }"],
    ['data-URI svg paint with none is clean', 'a { background: url("data:image/svg+xml,<svg><path fill=\'none\'/></svg>") }'],
    ['data-URI svg paint with currentColor is clean', 'a { background: url("data:image/svg+xml,<svg><path fill=\'currentColor\'/></svg>") }'],
    ['data-URI svg paint reference is clean', 'a { background: url("data:image/svg+xml,<svg><linearGradient id=\'g\'/><path fill=\'url(#g)\'/></svg>") }'],
    ['registered token inside data-URI svg paint is clean', 'a { background: url("data:image/svg+xml,<svg><path fill=\'var(--color-primary)\'/></svg>") }'],
    ['non-image data URI (font) is not a paint surface here', "a { src: url(data:font/woff2;base64,d09GMg==) format('woff2') }"],
    ['unregistered var in a non-color property is the token-graph lane, not this rule', 'a { padding: var(--local-spacing) }'],
    ['registered non-color token in a color position is a namespace edge, not this rule', 'a { color: var(--spacing-4) }'],
    ['color-scheme keywords are not named colors', ':root { color-scheme: dark light }'],
    ['animation name that collides with a named color', 'a { animation: red 2s ease-in-out infinite }'],
    ['animation-name shorthand variant', 'a { animation-name: red }'],
    ['transition property words are not colors', 'a { transition: color 0.2s ease, background-color 0.2s }'],
    ['will-change identifiers', 'a { will-change: transform, opacity }'],
    ['grid template area names', '.grid { grid-template-areas: "red blue" }'],
    ['font family names', 'a { font-family: Red, serif }'],
    ['nested var with mixed fallbacks over tokens', 'a { color: var(--color-primary, var(--color-error, var(--color-status-done))) }'],
    ['light-dark over tokens', 'a { color: light-dark(var(--color-primary), var(--color-error)) }'],
    ['drop-shadow over a token', 'a { filter: drop-shadow(0 0 2px var(--color-primary)) }'],
    ['non-color geometry only', 'a { padding: 4px 8px; border: 1px solid; z-index: 10 }'],
  ]

  for (const [name, source] of permitted) {
    it(name, () => {
      assert.deepEqual(run(source), [], JSON.stringify(run(source)))
    })
  }
})

describe('raw colors are detected', () => {
  const cases = [
    ['six-digit hex', 'a { background: #ffffff }', '#ffffff'],
    ['uppercase hex canonicalizes to lowercase', 'a { background: #FFFFFF }', '#ffffff'],
    ['three-digit hex', 'a { background: #fff }', '#fff'],
    ['four-digit hex', 'a { background: #fff8 }', '#fff8'],
    ['eight-digit hex', 'a { background: #ffffffff }', '#ffffffff'],
    ['named color', 'a { border-color: red }', 'red'],
    ['named color in mixed case', 'a { border-color: ReD }', 'red'],
    ['system color keyword', 'a { background: Canvas }', 'canvas'],
    ['legacy rgb commas', 'a { color: rgb(1, 2, 3) }', 'rgb(1,2,3)'],
    ['rgba canonicalizes to rgb', 'a { color: rgba(0, 0, 0, 0.5) }', 'rgb(0,0,0,0.5)'],
    ['modern space-separated rgb', 'a { color: rgb(0 0 0 / 50%) }', 'rgb(0 0 0 / 50%)'],
    ['uppercase function name canonicalizes', 'a { color: RGB(0 0 0) }', 'rgb(0 0 0)'],
    ['hsl modern syntax', 'a { color: hsl(120deg 50% 50%) }', 'hsl(120deg 50% 50%)'],
    ['hsla canonicalizes to hsl', 'a { color: hsla(120, 50%, 50%, 0.5) }', 'hsl(120,50%,50%,0.5)'],
    ['hwb function', 'a { color: hwb(90 10% 5%) }', 'hwb(90 10% 5%)'],
    ['oklch function', 'a { color: oklch(62.8% 0.25 29) }', 'oklch(62.8% 0.25 29)'],
    ['color() function', 'a { color: color(srgb 1 0 0) }', 'color(srgb 1 0 0)'],
    ['raw stop inside a gradient', 'a { background: linear-gradient(to right, var(--color-primary), #fff) }', '#fff'],
    ['named stop inside a gradient', 'a { background: linear-gradient(to right, var(--color-primary), red) }', 'red'],
    ['raw color inside var() fallback', 'a { color: var(--color-primary, #fff) }', '#fff'],
    ['custom property declaration', ':root { --local-red: #f00 }', '#f00'],
    ['color-mix with a raw component', 'a { color: color-mix(in oklab, var(--color-primary), #fff 40%) }', '#fff'],
    ['relative color derived from a raw color', 'a { color: rgb(from #ffffff r g b / 50%) }', '#ffffff'],
    ['drop-shadow with raw rgba', 'a { filter: drop-shadow(0 0 2px rgba(0,0,0,.5)) }', 'rgb(0,0,0,.5)'],
    ['hex inside nested calc-free border shorthand', 'a { border: 1px solid #d4af37 }', '#d4af37'],
    ['color in a declaration carrying !important', 'a { color: red !important }', 'red'],
    ['raw color in a @theme block is reported (boundary handled centrally)', '@theme { --color-brand-x: #123456 }', '#123456'],
    ['raw color in an @utility body', '@utility card-fill { color: rebeccapurple }', 'rebeccapurple'],
    ['raw color in an @property initial-value', "@property --x { syntax: '<color>'; initial-value: #fff; inherits: false }", '#fff'],
  ]

  for (const [name, source, syntax] of cases) {
    it(name, () => {
      const findings = run(source)
      if (syntax === null) {
        assert.deepEqual(findings, [])
        return
      }
      assert.equal(findings.length, 1, `expected exactly one finding, got: ${JSON.stringify(findings)}`)
      assertRawColor(findings[0], syntax)
    })
  }

  it('reports both colors of light-dark separately and in document order', () => {
    const findings = run('a { color: light-dark(#fff, #000) }')
    assert.deepEqual(syntaxes(findings), ['#fff', '#000'])
  })

  it('reports multiple raw colors in one declaration as separate findings', () => {
    const findings = run('a { border-color: red blue }')
    assert.deepEqual(syntaxes(findings), ['red', 'blue'])
    assert.deepEqual(ruleIds(findings), ['css-colors/raw-color', 'css-colors/raw-color'])
  })

  it('names the property in the message', () => {
    const [finding] = run('a { border-color: red }')
    assert.match(finding.message, /border-color/)
    assert.match(finding.message, /red/)
  })
})

describe('unregistered color-position token references are detected', () => {
  it('reports an unregistered var() reference in a direct color declaration', () => {
    const findings = run('a { color: var(--unregistered-local) }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertUnregisteredToken(findings[0], 'var(--unregistered-local)')
  })

  it('reports one unregistered stop inside a gradient over a registered token', () => {
    const findings = run('a { background: linear-gradient(var(--color-primary), var(--unregistered-local)) }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertUnregisteredToken(findings[0], 'var(--unregistered-local)')
  })

  it('reports an unregistered var nested as a registered token\'s fallback', () => {
    const findings = run('a { color: var(--color-primary, var(--unregistered-local)) }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertUnregisteredToken(findings[0], 'var(--unregistered-local)')
  })

  it('keeps a registered color token reference clean', () => {
    assert.deepEqual(run('a { color: var(--color-primary) }'), [])
  })

  it('reports both the unregistered reference and the raw fallback color beneath it', () => {
    const findings = run('a { color: var(--unregistered-local, red) }')
    assert.deepEqual(ruleIds(findings), ['css-colors/unregistered-token', 'css-colors/raw-color'])
    assertUnregisteredToken(findings[0], 'var(--unregistered-local)')
    assertRawColor(findings[1], 'red')
  })

  it('reports each unregistered stop of a gradient separately', () => {
    const findings = run('a { background: linear-gradient(var(--local-a), var(--local-b)) }')
    assert.deepEqual(syntaxes(findings), ['var(--local-a)', 'var(--local-b)'])
  })

  it('checks gradient stops even in an image-only property', () => {
    const findings = run('a { mask-image: linear-gradient(var(--local-stop), transparent) }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertUnregisteredToken(findings[0], 'var(--local-stop)')
  })

  it('does not check var names in non-color declarations', () => {
    assert.deepEqual(run('a { font-family: var(--local-font-stack) }'), [])
  })

  it('treats custom property name case as significant', () => {
    const findings = run('a { color: var(--Spacing-4) }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertUnregisteredToken(findings[0], 'var(--Spacing-4)')
  })

  it('walks unregistered references out of nested at-rule blocks', () => {
    const findings = run('@media (min-width: 640px) { .a { color: var(--mobile-only-color) } }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertUnregisteredToken(findings[0], 'var(--mobile-only-color)')
  })

  it('gives every finding exactly the contract keys', () => {
    const [finding] = run('a { color: var(--unregistered-local) }')
    assert.deepEqual(Object.keys(finding).sort(), ['column', 'line', 'message', 'path', 'ruleId', 'syntax'])
  })

  it('names the declaration and the reference in the message', () => {
    const [finding] = run('a { border-color: var(--unregistered-local) }')
    assert.match(finding.message, /border-color/)
    assert.match(finding.message, /--unregistered-local/)
  })
})

describe('data URI SVG paint is decoded and scanned', () => {
  const paintUri = (paint) => `url("data:image/svg+xml,<svg><path fill='${paint}'/></svg>")`

  it('reports a raw paint hex embedded in a background data URI', () => {
    const findings = run(`a { background-image: url("data:image/svg+xml,<svg><path fill='%23ffffff'/></svg>") }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertRawColor(findings[0], '#ffffff')
  })

  it('reports a raw stroke paint in a mask-image data URI', () => {
    const findings = run(`a { mask-image: url("data:image/svg+xml,<svg><path stroke='%23d4af37'/></svg>") }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertRawColor(findings[0], '#d4af37')
  })

  it('reports a named paint color embedded in a data URI', () => {
    const [finding] = run(`a { background: ${paintUri('red')} }`)
    assertRawColor(finding, 'red')
  })

  it('canonicalizes an uppercase embedded hex to lowercase', () => {
    const [finding] = run(`a { background: ${paintUri('%23FFFFFF')} }`)
    assertRawColor(finding, '#ffffff')
  })

  it('reports a three-digit embedded hex', () => {
    const [finding] = run(`a { background: ${paintUri('%23fff')} }`)
    assertRawColor(finding, '#fff')
  })

  it('reports an embedded color function', () => {
    const [finding] = run(`a { background: ${paintUri('rgb(255,255,255)')} }`)
    assertRawColor(finding, 'rgb(255,255,255)')
  })

  it('reports every paint attribute of the embedded SVG in document order', () => {
    const findings = run(`a { background: url("data:image/svg+xml,<svg><path fill='%23fff' stroke='%23000'/></svg>") }`)
    assert.deepEqual(syntaxes(findings), ['#fff', '#000'])
  })

  it('reports stop-color attributes on gradient stops', () => {
    const [finding] = run(`a { background: url("data:image/svg+xml,<svg><linearGradient><stop stop-color='%23123456'/></linearGradient></svg>") }`)
    assertRawColor(finding, '#123456')
  })

  it('reports paint declared inside an embedded style attribute', () => {
    const [finding] = run(`a { background: url("data:image/svg+xml,<svg><rect style='fill:%23abc'/></svg>") }`)
    assertRawColor(finding, '#abc')
  })

  it('reports paint in a base64-encoded SVG data URI', () => {
    const payload = Buffer.from("<svg><path fill='#ffffff'/></svg>", 'utf8').toString('base64')
    const [finding] = run(`a { background: url("data:image/svg+xml;base64,${payload}") }`)
    assertRawColor(finding, '#ffffff')
  })

  it('fingerprints the decoded color, not the encoding', () => {
    const encoded = run(`a { background: ${paintUri('%23ffffff')} }`)
    const b64 = Buffer.from("<svg><path fill='#ffffff'/></svg>", 'utf8').toString('base64')
    const decoded = run(`a { background: url("data:image/svg+xml;base64,${b64}") }`)
    assert.equal(encoded[0].ruleId, decoded[0].ruleId)
    assert.equal(encoded[0].syntax, decoded[0].syntax)
  })

  it('fails closed on an invalid percent escape in the payload', () => {
    const findings = run(`a { background: url("data:image/svg+xml,<svg><path fill='%zz'/></svg>") }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assert.equal(findings[0].ruleId, 'css-colors/unsupported')
    assert.ok(findings[0].message.length > 0)
  })

  it('fails closed on a corrupt base64 payload', () => {
    const findings = run(`a { background: url("data:image/svg+xml;base64,!!!not-base64!!!") }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assert.equal(findings[0].ruleId, 'css-colors/unsupported')
  })

  it('fails closed on an unterminated decoded SVG tag', () => {
    const findings = run(`a { background: url("data:image/svg+xml,<svg><path fill='%23fff'") }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertMalformedSvg(findings[0])
  })

  it('fails closed on an unterminated decoded SVG comment', () => {
    const findings = run(`a { background: url("data:image/svg+xml,<svg><!-- <path fill='%23fff'/></svg>") }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertMalformedSvg(findings[0])
  })

  it('fails closed on an unterminated decoded SVG quoted attribute', () => {
    const findings = run(`a { background: url("data:image/svg+xml,<svg><path fill='%23fff></path></svg>") }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertMalformedSvg(findings[0])
  })

  it('fails closed on a binary image data URI whose paint cannot be verified', () => {
    const png = Buffer.from('89504e470d0a1a0a0000000d49484452', 'hex').toString('base64')
    const findings = run(`a { background: url("data:image/png;base64,${png}") }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assert.equal(findings[0].ruleId, 'css-colors/unsupported')
  })

  it('reports an invalid embedded hex length as unsupported', () => {
    const findings = run(`a { background: ${paintUri('%23fffff')} }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assert.equal(findings[0].ruleId, 'css-colors/unsupported')
    assert.equal(findings[0].syntax, '#fffff')
  })

  it('reports an unregistered token reference used as embedded paint', () => {
    const findings = run(`a { background: ${paintUri('var(--unregistered-paint)')} }`)
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertUnregisteredToken(findings[0], 'var(--unregistered-paint)')
  })
})

describe('canonical syntax is position- and formatting-independent', () => {
  it('same hex at a different line and case fingerprints identically', () => {
    const early = run('a {\n  color: #FFF;\n}')
    const late = run(`.pad {}\n\n\n\n.b {\n  color: #fff;\n}`)
    assert.equal(early[0].syntax, late[0].syntax)
    assert.equal(early[0].line, 2)
    assert.equal(late[0].line, 6)
  })

  it('whitespace inside color functions canonicalizes', () => {
    const spaced = run('a { color: rgba(0, 0, 0, 0.5) }')
    const tight = run('a { color: rgba(0,0,0,0.5) }')
    assert.equal(spaced[0].syntax, tight[0].syntax)
  })

  it('syntax never carries line or column information', () => {
    const findings = run('a { color: red }')
    assert.ok(!/\d+\s*:\s*\d+/.test(findings[0].syntax))
  })

  it('computes display-only line and column against a multi-line fixture', () => {
    const findings = run('a {\n  background: #fff;\n}')
    assert.equal(findings[0].line, 2)
    assert.equal(findings[0].column, 15)
  })

  it('computes columns for colors inside multi-line values', () => {
    const source = 'a {\n  background: linear-gradient(\n    to right,\n    #fff\n  );\n}'
    const findings = run(source)
    assert.equal(findings[0].line, 4)
    assert.equal(findings[0].column, 5)
  })
})

describe('nested contexts are scanned', () => {
  it('walks declarations inside @media', () => {
    const findings = run('@media (min-width: 640px) { .a { color: red } }')
    assert.equal(syntaxes(findings).length, 1)
    assert.equal(findings[0].syntax, 'red')
  })

  it('walks declarations inside @supports', () => {
    const findings = run('@supports (color: oklch(0% 0 0)) { .a { color: oklch(0% 0 0) } }')
    assert.equal(findings[0].syntax, 'oklch(0% 0 0)')
  })

  it('walks declarations inside native CSS nesting', () => {
    const findings = run('.parent { color: var(--color-primary); .child { & { background: red } } }')
    assert.equal(findings[0].syntax, 'red')
  })

  it('walks declarations inside @keyframes', () => {
    const findings = run('@keyframes fade { from { color: red } to { color: var(--color-primary) } }')
    assert.deepEqual(syntaxes(findings), ['red'])
  })
})

describe('at-rule condition text is scanned for colors', () => {
  it('reports a raw color in an @supports condition alongside a control declaration color', () => {
    const findings = run('@supports (color: #ff0000) { .a { color: #00ff00 } }')
    assert.deepEqual(ruleIds(findings), ['css-colors/raw-color', 'css-colors/raw-color'])
    assert.deepEqual(syntaxes(findings), ['#ff0000', '#00ff00'])
    assertRawColor(findings[0], '#ff0000')
    assertRawColor(findings[1], '#00ff00')
    assert.match(findings[0].message, /at-rule condition/)
  })

  it('reports a raw color in an @container style() condition alongside a control declaration color', () => {
    const findings = run('@container style(--brand: #ff0000) { .a { color: #00ff00 } }')
    assert.deepEqual(ruleIds(findings), ['css-colors/raw-color', 'css-colors/raw-color'])
    assert.deepEqual(syntaxes(findings), ['#ff0000', '#00ff00'])
  })

  it('reports a raw color inside an @supports not() wrapper', () => {
    const findings = run('@supports not (color: #ff0000) { .a { color: var(--color-primary) } }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertRawColor(findings[0], '#ff0000')
  })

  it('reports an unregistered token reference in an @container style() condition', () => {
    const findings = run('@container style(color: var(--unregistered-local)) { .a { color: var(--color-primary) } }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertUnregisteredToken(findings[0], 'var(--unregistered-local)')
  })

  it('keeps a custom-property style() condition with a raw literal reported the same as a custom-property declaration', () => {
    const findings = run('@container style(--brand: #ff0000) { .a { color: var(--color-primary) } }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assertRawColor(findings[0], '#ff0000')
  })

  it('keeps a token-based @supports condition clean', () => {
    assert.deepEqual(
      run('@supports (color: var(--color-primary)) { .a { color: var(--color-primary) } }'),
      [],
    )
  })

  it('does not turn an ordinary media query into noise', () => {
    assert.deepEqual(run('@media (min-width: 640px) { .a { color: var(--color-primary) } }'), [])
    assert.deepEqual(run('@media screen and (min-width: 640px) { .a { color: var(--color-primary) } }'), [])
    assert.deepEqual(run('@media (prefers-color-scheme: dark) { .a { color: var(--color-primary) } }'), [])
  })

  it('does not turn an ordinary @container size query into noise', () => {
    assert.deepEqual(run('@container sidebar (min-width: 400px) { .a { color: var(--color-primary) } }'), [])
  })

  it('does not walk into a @supports selector() argument', () => {
    assert.deepEqual(run('@supports selector(a > b) { .a { color: var(--color-primary) } }'), [])
  })

  it('does not walk non-color-bearing at-rule preludes (@keyframes, @theme, @property names)', () => {
    assert.deepEqual(run("@keyframes red-fade { from { color: var(--color-primary) } }"), [])
    assert.deepEqual(run('@theme { --color-brand: var(--color-primary); }'), [])
  })

  it('fails closed on a malformed @supports condition (unbalanced parentheses)', () => {
    const findings = run('@supports ((color: #ff0000) { .a { color: #00ff00 } }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assert.equal(findings[0].ruleId, 'css-colors/unsupported')
    assert.match(findings[0].syntax, /unbalanced parentheses/)
    assert.ok(findings[0].message.length > 0)
    assert.ok(Number.isInteger(findings[0].line) && findings[0].line > 0)
    assert.ok(Number.isInteger(findings[0].column) && findings[0].column > 0)
  })

  it('fails closed on a malformed @container condition with an ambiguous declaration shape', () => {
    const findings = run('@container style(too many words: #ff0000) { .a { color: var(--color-primary) } }')
    assert.equal(findings.length, 1, JSON.stringify(findings))
    assert.equal(findings[0].ruleId, 'css-colors/unsupported')
  })
})

describe('unsupported syntax fails closed', () => {
  it('reports a five-digit hex as unsupported, not silently clean', () => {
    const findings = run('a { color: #fffff }')
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, 'css-colors/unsupported')
    assert.equal(findings[0].syntax, '#fffff')
    assert.ok(findings[0].message.length > 0)
  })

  it('reports a two-digit hex as unsupported', () => {
    const findings = run('a { color: #ff }')
    assert.equal(findings[0].ruleId, 'css-colors/unsupported')
    assert.equal(findings[0].syntax, '#ff')
  })

  it('still reports raw colors elsewhere in the same stylesheet', () => {
    const findings = run('a { color: #fffff }\nb { color: red }')
    assert.deepEqual(ruleIds(findings), ['css-colors/unsupported', 'css-colors/raw-color'])
  })
})

describe('parse failures fail closed', () => {
  const broken = [
    ['unclosed block', 'a { color: red'],
    ['unclosed string', 'a { content: "abc; }'],
    ['unclosed bracket in a value', 'a { color: rgb(; }'],
    ['stray closing brace', 'a { color: red } }'],
  ]

  for (const [name, source] of broken) {
    it(name, () => {
      const findings = run(source)
      assert.equal(findings.length, 1, JSON.stringify(findings))
      assert.equal(findings[0].ruleId, 'css-colors/parse-error')
      assert.ok(findings[0].syntax.startsWith('parse error:'))
      assert.ok(findings[0].syntax.length > 'parse error:'.length)
      assert.ok(findings[0].message.length > 0)
    })
  }

  it('keeps position out of the parse-error syntax (fingerprint stays stable)', () => {
    const first = run('a { color: red')
    const second = run('\n\n\n.b { color: red')
    assert.equal(first[0].syntax, second[0].syntax)
  })
})

describe('ident-context properties still report literals', () => {
  it('a hex in an animation shorthand is still a raw color', () => {
    const findings = run('a { animation: #fff 2s }')
    assert.equal(findings[0].ruleId, 'css-colors/raw-color')
  })

  it('a color function in a transition is still a raw color', () => {
    const findings = run('a { transition: rgb(0 0 0) 0.2s }')
    assert.equal(findings[0].ruleId, 'css-colors/raw-color')
  })
})

describe('real stylesheet shapes', () => {
  it('scans a tailwind-v4 style sheet with @import, @source and @theme', () => {
    const source = [
      "@import 'tailwindcss' source(none);",
      "@source '../';",
      '@theme {',
      '  --color-primary: #0a0a0b;',
      '  --font-body: Inter, system-ui, sans-serif;',
      '}',
      ':root { color-scheme: dark; }',
      '.card {',
      '  background: linear-gradient(to bottom, var(--color-primary), #141416);',
      '  border: 1px solid var(--color-error);',
      '}',
    ].join('\n')
    const findings = run(source)
    assert.deepEqual(syntaxes(findings), ['#0a0a0b', '#141416'])
    assert.deepEqual(ruleIds(findings), ['css-colors/raw-color', 'css-colors/raw-color'])
  })
})
