import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { extensions, scan } from '../../scripts/design-system-locks/typography.mjs'

// ---------------------------------------------------------------------------
// Stage B typography lock — D1/D2/D9, review follow-up edition.
//
// Oracle independence: every expected value below is derived from
// docs/internal/design/design-system-definition.md (§D1 root clamp 12–20px,
// §D2 hard 12px floor and the arbitrary-utility ban, §D9 role system for
// family/size/weight/leading/tracking, §E1 enforcement list, §D10 320px
// reflow floor), from design-system/enforcement/review-colors-typography.json
// (the required adversarial fixtures for this follow-up), from the installed
// Tailwind 4.3.3 default theme (node_modules/tailwindcss/theme.css:
// --text-xs: 0.75rem, --font-weight-medium: 500, --leading-normal: 1.5,
// --tracking-normal: 0em), from design-system/tokens/foundations.json
// (--font-size-floor: 12px; size tokens all use max(12px, Xrem) guards;
// id→css derivation: type.caption.size → --type-caption-size), and from the
// CSS Values 3 §5.2 absolute keyword table (xx-small 9px … xxx-large 48px at
// the 16px browser default — keywords track the browser default, not the
// html root). Derived arithmetic: 0.75rem × 12px = 9px; 0.875rem × 12px =
// 10.5px; 2vw at the 320px reflow floor = 6.4px; 8pt × 4/3 = 10.667px;
// max(12px, 0.857142857rem) at the 12px root = max(12, 10.29) = 12px.
// Nothing is copied from scanner output. The full case table and mutation
// plan live in dist/design-system-baseline/cli-lanes/b-typography-followup-test-plan.md.
// ---------------------------------------------------------------------------

const POLICY = {
  tokenCssNames: [
    '--type-caption-size',
    '--type-body-size',
    '--type-utility-xs-size',
    '--type-body-family',
    '--font-family-mono',
    '--color-primary',
  ],
  resolvedTokens: {},
}

// Registers the D9 role tokens the adversarial suite uses for weight,
// leading and tracking (mirrors tests/design-system-locks/foundations-adversarial.test.mjs).
const ROLE_POLICY = {
  tokenCssNames: [
    ...POLICY.tokenCssNames,
    '--type-body-weight',
    '--type-body-line-height',
    '--type-body-letter-spacing',
  ],
  resolvedTokens: {},
}

// Registers the Tailwind theme variables that named weight/leading/tracking
// utilities resolve through (theme.css maps font-medium → --font-weight-medium,
// leading-normal → --leading-normal, tracking-normal → --tracking-normal).
const NAMED_UTILITY_POLICY = {
  tokenCssNames: [
    ...POLICY.tokenCssNames,
    '--font-weight-medium',
    '--leading-normal',
    '--tracking-normal',
  ],
  resolvedTokens: {},
}

// resolvedTokens keyed by token id, per the contract; the id→css derivation
// (camelCase → kebab, dots → dashes) is evidenced by foundations.json
// (font.size.bodyCompact → --font-size-body-compact).
const RESOLVED_POLICY = {
  tokenCssNames: ['--type-caption-size', '--font-size-caption'],
  resolvedTokens: {
    'type.caption.size': 'max(12px, 0.857142857rem)',
    'font.size.caption': 'max(12px, 0.857142857rem)',
  },
}

// A registered size token whose resolved value violates the §D2 floor.
const SUBFLOOR_RESOLVED_POLICY = {
  tokenCssNames: ['--type-caption-size'],
  resolvedTokens: { 'type.caption.size': '10px' },
}

// §D1 root font-size tokens (design-system/tokens/foundations.json:
// font.root.minimum/default/maximum), keyed by CSS name via resolvedCssTokens
// exactly as policy.mjs supplies them (contract.json: "canonical policy.mjs
// always supplies exact declared CSS custom property to resolved value map").
// Values match src/styles/tokens.generated.css (--font-root-minimum: 12px;
// --font-root-default: 14px; --font-root-maximum: 20px), the real fixture
// this policy exists to reproduce: src/styles/library.css and globals.css's
// `font-size: clamp(var(--font-root-minimum), var(--user-font-size, var(--font-root-default)), var(--font-root-maximum))`.
const FONT_ROOT_CLAMP_POLICY = {
  tokenCssNames: ['--font-root-minimum', '--font-root-default', '--font-root-maximum'],
  resolvedTokens: {},
  resolvedCssTokens: {
    '--font-root-minimum': '12px',
    '--font-root-default': '14px',
    '--font-root-maximum': '20px',
  },
}

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

describe('scanner API contract (design-system/enforcement/contract.json)', () => {
  it('declares the handled extensions with leading dots, SVG included (review: typography-svg-scope)', () => {
    assert.deepEqual(extensions, ['.ts', '.tsx', '.js', '.jsx', '.mjs', '.cjs', '.css', '.svg'])
  })

  it('returns findings synchronously as an array with the contract field shape', () => {
    const found = scan({ path: 'src/a.tsx', source: 'export const x = <div className="text-xs" />', policy: POLICY })
    assert.ok(Array.isArray(found))
    const first = found[0]
    assert.equal(typeof first.ruleId, 'string')
    assert.equal(first.path, 'src/a.tsx')
    assert.equal(typeof first.syntax, 'string')
    assert.ok(first.syntax.length > 0)
    assert.equal(typeof first.message, 'string')
    assert.ok(first.message.length > 0)
    assert.ok(Number.isInteger(first.line) && first.line >= 1)
    assert.ok(Number.isInteger(first.column) && first.column >= 1)
  })

  it('fails closed on an unhandled extension instead of silently skipping', () => {
    assert.throws(() => scan({ path: 'src/page.html', source: '<p/>', policy: POLICY }), /extension/)
  })

  it('handles governed extensions case-insensitively without weakening their rules', () => {
    const cases = [
      ['src/a.CSS', '.x { font-size: 10px }', 'typography/font-size-below-floor'],
      ['src/a.SVG', '<svg><text font-size="10">x</text></svg>', 'typography/font-size-below-floor'],
      ['src/a.TSX', 'export const X = () => <p className="text-xs">x</p>', 'typography/text-size-below-floor'],
    ]
    for (const [path, source, ruleId] of cases) {
      assert.deepEqual(scan({ path, source, policy: POLICY }).map((finding) => finding.ruleId), [ruleId], path)
    }
  })

  it('treats a missing policy as an empty token registry (every var becomes unregistered)', () => {
    const found = scan({ path: 'src/a.tsx', source: 'export const x = <span style={{ fontSize: \'var(--type-caption-size)\' }} />', policy: undefined })
    assert.equal(found.length, 1)
    assert.equal(found[0].ruleId, 'typography/unregistered-font-size-token')
    assert.equal(found[0].syntax, 'fontSize: var(--type-caption-size)')
  })
})

describe('permitted typography — no findings', () => {
  it('allows default scale utilities at or above 1rem', () => {
    expectClean('export const x = <p className="text-base text-lg text-2xl text-9xl">hi</p>')
  })

  it('does not confuse alignment, colour, or decoration utilities with sizes', () => {
    expectClean('export const x = <p className="text-center text-white text-primary text-muted-foreground underline truncate">hi</p>')
  })

  it('leaves arbitrary colour utilities to the colours lane', () => {
    expectClean('export const x = <p className="text-[#0A0A0B] text-[color:var(--color-primary)]">hi</p>')
  })

  it('leaves the untyped registered colour-token form to the colours lane', () => {
    expectClean('export const x = <p className="text-[var(--color-primary)]">hi</p>')
  })

  it('classifies a well-formed untyped var with unknown kind as blocking type-hint debt', () => {
    expectOne('export const x = <p className="text-[var(--color-text-secondary)]">hi</p>', 'typography/missing-arbitrary-type-hint', 'text-[var(--color-text-secondary)]')
  })

  it('keeps malformed, interpolated, and opaque arbitrary forms unsupported', () => {
    const cases = [
      'export const x = <p className="text-[var(--unknown]">hi</p>',
      'export const x = <p className={`text-[var(--${token})]`}>hi</p>',
      'export const x = <p className="text-[themeValue]">hi</p>',
    ]
    for (const source of cases) assert.deepEqual(findings(source).map((finding) => finding.ruleId), ['typography/unsupported-text-utility'])
  })

  it('allows arbitrary size utilities that reference registered tokens', () => {
    expectClean('export const x = <p className="text-[length:var(--type-caption-size)]">hi</p>')
  })

  it('allows the v4 paren shorthand for a registered size token', () => {
    expectClean('export const x = <p className="text-(length:--type-caption-size)">hi</p>')
  })

  it('allows the v4 bare custom-property shorthand for a registered size token', () => {
    expectClean('export const x = <p className="text-[--type-caption-size]">hi</p>')
  })

  it('allows family utilities remapped in both live builds (font-mono, font-headline)', () => {
    expectClean('export const x = <p className="font-mono font-headline">hi</p>')
  })

  it('allows floor-safe style-object font sizes across the whole 12–20px root range', () => {
    expectClean(`export const x = (
      <>
        <span style={{ fontSize: 12 }}>a</span>
        <span style={{ fontSize: '14px' }}>b</span>
        <span style={{ fontSize: '1rem' }}>c</span>
        <span style={{ fontSize: '1.125rem' }}>d</span>
        <span style={{ fontSize: 'max(12px, 0.875rem)' }}>e</span>
        <span style={{ fontSize: 'clamp(12px, 2vw, 16px)' }}>f</span>
        <span style={{ fontSize: 'calc(1rem + 2px)' }}>g</span>
        <span style={{ fontSize: 'var(--type-caption-size)' }}>h</span>
      </>
    )`)
  })

  it('proves a nested max floor soundly: max(12px, min(2vw, 11px)) cannot compute below 12px', () => {
    // The outer max's known 12px operand bounds the whole expression below at
    // 12px no matter what the inner min contributes (review fixture).
    expectClean(`export const x = <span style={{ fontSize: 'max(12px, min(2vw, 11px))' }} />`)
  })

  it('allows the CSS keyword sizes at or above the floor (13px small at the 16px browser default)', () => {
    expectClean(`export const x = (
      <>
        <span style={{ fontSize: 'small' }}>a</span>
        <span style={{ fontSize: 'medium' }}>b</span>
        <span style={{ fontSize: 'xxx-large' }}>c</span>
        <span style={{ fontSize: 'inherit' }}>d</span>
      </>
    )`)
  })

  it('allows token or keyword font families in style objects', () => {
    expectClean(`export const x = (
      <>
        <span style={{ fontFamily: 'var(--font-family-mono)' }}>a</span>
        <span style={{ fontFamily: 'inherit' }}>b</span>
      </>
    )`)
  })

  it('parses var() fallbacks and proves the clamp floor through the max bound', () => {
    expectClean(`export const x = <span style={{ fontSize: 'clamp(12px, var(--type-caption-size, 14px), 20px)' }} />`)
    expectClean(`export const x = <span style={{ fontSize: 'var(--type-caption-size, 14px)' }} />`)
  })

  it('allows clean classes through the cn builder including conditionals', () => {
    expectClean(`import { cn } from '../lib/utils'
export const x = <p className={cn('text-base', cond && 'text-lg')}>hi</p>`)
  })

  it('allows clean cva base and variant strings', () => {
    expectClean(`import { cva } from 'class-variance-authority'
export const v = cva('text-base', { variants: { tone: { caption: 'text-[length:var(--type-caption-size)] text-primary' } } })`)
  })

  it('allows floor-safe and token-driven CSS declarations', () => {
    expectClean(`.a { font-size: 12px; }
.b { font-size: max(12px, 0.75rem); }
.c { font-family: var(--type-body-family); }
.d { font-size: clamp(12px, 2vw, 16px); }`, { path: 'src/fixture.css' })
  })

  it('does not treat @font-face family names or @theme custom properties as usages', () => {
    expectClean(`@font-face { font-family: 'Outfit'; src: url(x.woff2); }
@theme inline { --text-xs: var(--type-utility-xs-size); }`, { path: 'src/fixture.css' })
  })
})

describe('forbidden Tailwind size utilities', () => {
  it('rejects an arbitrary literal pixel size', () => {
    expectOne('export const x = <p className="text-[11px]">hi</p>', 'typography/arbitrary-text-size', 'text-[11px]')
  })

  it('canonicalizes whitespace in declaration values via the expression printer', () => {
    expectOne('.a { font-size:calc( 1rem - 0.5px ); }', 'typography/font-size-below-floor', 'font-size: calc(1rem - 0.5px)', { path: 'src/fixture.css' })
  })

  it('rejects an arbitrary size even when it clears the 12px floor (E1 bans the form)', () => {
    expectOne('export const x = <p className="text-[13px]">hi</p>', 'typography/arbitrary-text-size', 'text-[13px]')
  })

  it('rejects an explicitly length-typed arbitrary size', () => {
    expectOne('export const x = <p className="text-[length:13px]">hi</p>', 'typography/arbitrary-text-size', 'text-[length:13px]')
  })

  it('rejects an arbitrary rem size', () => {
    expectOne('export const x = <p className="text-[0.9rem]">hi</p>', 'typography/arbitrary-text-size', 'text-[0.9rem]')
  })

  it('rejects text-xs: 0.75rem computes to 9px at the 12px minimum root', () => {
    expectOne('export const x = <p className="text-xs">hi</p>', 'typography/text-size-below-floor', 'text-xs')
  })

  it('rejects text-sm: 0.875rem computes to 10.5px at the 12px minimum root', () => {
    expectOne('export const x = <p className="text-sm">hi</p>', 'typography/text-size-below-floor', 'text-sm')
  })

  it('rejects an arbitrary size referencing an unregistered custom property', () => {
    expectOne('export const x = <p className="text-[length:var(--my-size)]">hi</p>', 'typography/unregistered-font-size-token', 'text-[length:var(--my-size)]')
  })

  it('rejects the bare custom-property shorthand when the token is unregistered', () => {
    expectOne('export const x = <p className="text-[--my-size]">hi</p>', 'typography/unregistered-font-size-token', 'text-[--my-size]')
  })

  it('reports one finding per offending class inside a single className', () => {
    const found = findings('export const x = <p className="text-xs text-[11px] text-base">hi</p>')
    assert.deepEqual(found.map((f) => f.syntax), ['text-xs', 'text-[11px]'])
  })
})

describe('forbidden style-object font sizes', () => {
  it('rejects a numeric literal below the floor', () => {
    expectOne(`export const x = <span style={{ fontSize: 11 }} />`, 'typography/font-size-below-floor', 'fontSize: 11')
  })

  it('rejects the numeric zero value (review fixture)', () => {
    expectOne(`export const x = <span style={{ fontSize: 0 }} />`, 'typography/font-size-below-floor', 'fontSize: 0')
  })

  it('rejects a decimal pixel literal below the floor', () => {
    expectOne(`export const x = <span style={{ fontSize: '10.5px' }} />`, 'typography/font-size-below-floor', 'fontSize: 10.5px')
  })

  it('rejects 11.999px — the boundary just under the 12px floor', () => {
    expectOne(`export const x = <span style={{ fontSize: '11.999px' }} />`, 'typography/font-size-below-floor', 'fontSize: 11.999px')
  })

  it('rejects a negative pixel size (review fixture)', () => {
    expectOne(`export const x = <span style={{ fontSize: '-1px' }} />`, 'typography/font-size-below-floor', 'fontSize: -1px')
  })

  it('rejects 8pt: 8 × 4/3 = 10.667px, below the floor (review fixture)', () => {
    expectOne(`export const x = <span style={{ fontSize: '8pt' }} />`, 'typography/font-size-below-floor', 'fontSize: 8pt')
  })

  it('rejects the sub-floor absolute keyword sizes (9px / 10px at the browser default)', () => {
    expectOne(`export const x = <span style={{ fontSize: 'xx-small' }} />`, 'typography/font-size-below-floor', 'fontSize: xx-small')
    expectOne(`export const x = <span style={{ fontSize: 'x-small' }} />`, 'typography/font-size-below-floor', 'fontSize: x-small')
  })

  it('rejects a bare sub-1rem size (10.5px at the minimum root)', () => {
    expectOne(`export const x = <span style={{ fontSize: '0.875rem' }} />`, 'typography/font-size-below-floor', 'fontSize: 0.875rem')
  })

  it('rejects 0.999rem — the rem boundary just under the floor', () => {
    expectOne(`export const x = <span style={{ fontSize: '0.999rem' }} />`, 'typography/font-size-below-floor', 'fontSize: 0.999rem')
  })

  it('rejects calc(1rem - 0.5px): 11.5px at the minimum root', () => {
    expectOne(`export const x = <span style={{ fontSize: 'calc(1rem - 0.5px)' }} />`, 'typography/font-size-below-floor', 'fontSize: calc(1rem - 0.5px)')
  })

  it('rejects calc(0.75rem - 1px): 8px at the minimum root', () => {
    expectOne(`export const x = <span style={{ fontSize: 'calc(0.75rem - 1px)' }} />`, 'typography/font-size-below-floor', 'fontSize: calc(0.75rem - 1px)')
  })

  it('rejects a clamp whose minimum is under the floor', () => {
    expectOne(`export const x = <span style={{ fontSize: 'clamp(8px, 2vw, 16px)' }} />`, 'typography/font-size-below-floor', 'fontSize: clamp(8px, 2vw, 16px)')
  })

  it('rejects min() that can fall under the floor: min(16px, 0.75rem) = 9px', () => {
    expectOne(`export const x = <span style={{ fontSize: 'min(16px, 0.75rem)' }} />`, 'typography/font-size-below-floor', 'fontSize: min(16px, 0.75rem)')
  })

  it('rejects max() whose operands are both under the floor: max(10px, 0.75rem) = 10px', () => {
    expectOne(`export const x = <span style={{ fontSize: 'max(10px, 0.75rem)' }} />`, 'typography/font-size-below-floor', 'fontSize: max(10px, 0.75rem)')
  })

  it('rejects a bare viewport size: 2vw is 6.4px at the 320px reflow floor', () => {
    expectOne(`export const x = <span style={{ fontSize: '2vw' }} />`, 'typography/font-size-below-floor', 'fontSize: 2vw')
  })

  it('rejects a font size through an unregistered custom property', () => {
    expectOne(`export const x = <span style={{ fontSize: 'var(--my-size)' }} />`, 'typography/unregistered-font-size-token', 'fontSize: var(--my-size)')
  })

  it('matches the kebab-case style key form', () => {
    expectOne(`export const x = <span style={{ 'font-size': '11px' }} />`, 'typography/font-size-below-floor', 'fontSize: 11px')
  })
})

describe('forbidden font families', () => {
  it('rejects a literal family stack in a style object', () => {
    expectOne(`export const x = <span style={{ fontFamily: 'Inter, system-ui' }} />`, 'typography/font-family-literal', 'fontFamily: Inter, system-ui')
  })

  it('rejects a family through an unregistered custom property', () => {
    expectOne(`export const x = <span style={{ fontFamily: 'var(--my-font)' }} />`, 'typography/unregistered-font-size-token', 'fontFamily: var(--my-font)')
  })

  it('rejects font-sans: it resolves to the Tailwind default stack, not the Inter token', () => {
    expectOne('export const x = <p className="font-sans">hi</p>', 'typography/font-family-off-token', 'font-sans')
  })

  it('rejects font-serif: not a Sovereign Deep family at all', () => {
    expectOne('export const x = <p className="font-serif">hi</p>', 'typography/font-family-off-token', 'font-serif')
  })

  it('rejects an arbitrary literal family utility', () => {
    expectOne(`export const x = <p className="font-['Playfair_Display']">hi</p>`, 'typography/font-family-literal', `font-['Playfair_Display']`)
  })

  it('splits the font shorthand into size and family findings', () => {
    const found = findings(`export const x = <span style={{ font: '11px Inter' }} />`)
    assert.deepEqual(
      found.map((f) => `${f.ruleId}|${f.syntax}`),
      ['typography/font-size-below-floor|font: 11px …', 'typography/font-family-literal|font: … Inter'],
    )
  })
})

describe('D9 role properties — weight, leading, tracking (review: typography-role-properties)', () => {
  it('rejects an arbitrary weight utility', () => {
    expectOne('export const x = <p className="font-[350]">x</p>', 'typography/font-weight-arbitrary', 'font-[350]')
  })

  it('rejects an arbitrary leading utility', () => {
    expectOne('export const x = <p className="leading-[13px]">x</p>', 'typography/line-height-arbitrary', 'leading-[13px]')
  })

  it('rejects an arbitrary tracking utility', () => {
    expectOne('export const x = <p className="tracking-[.1em]">x</p>', 'typography/letter-spacing-arbitrary', 'tracking-[.1em]')
  })

  it('rejects literal weight, leading and tracking in one CSS rule as three separate findings', () => {
    const found = findings('.x { font-weight: 350; line-height: 13px; letter-spacing: .1em }', { path: 'src/fixture.css' })
    assert.deepEqual(
      found.map((f) => `${f.ruleId}|${f.syntax}`),
      [
        'typography/font-weight-arbitrary|font-weight: 350',
        'typography/line-height-arbitrary|line-height: 13px',
        'typography/letter-spacing-arbitrary|letter-spacing: .1em',
      ],
    )
  })

  it('rejects literal weight, leading and tracking in style objects', () => {
    expectOne('export const x = <p style={{ fontWeight: 350 }}>x</p>', 'typography/font-weight-arbitrary', 'fontWeight: 350')
    expectOne(`export const x = <p style={{ lineHeight: '13px' }}>x</p>`, 'typography/line-height-arbitrary', 'lineHeight: 13px')
    expectOne(`export const x = <p style={{ letterSpacing: '.1em' }}>x</p>`, 'typography/letter-spacing-arbitrary', 'letterSpacing: .1em')
  })

  it('allows named utilities whose theme variables are registered policy values', () => {
    expectClean('export const x = <p className="font-medium leading-normal tracking-normal">x</p>', { policy: NAMED_UTILITY_POLICY })
  })

  it('rejects named utilities whose theme variables are not registered (neither live build remaps them)', () => {
    expectOne('export const x = <p className="font-medium">x</p>', 'typography/font-weight-arbitrary', 'font-medium')
    expectOne('export const x = <p className="leading-normal">x</p>', 'typography/line-height-arbitrary', 'leading-normal')
    expectOne('export const x = <p className="tracking-normal">x</p>', 'typography/letter-spacing-arbitrary', 'tracking-normal')
  })

  it('allows registered D9 role tokens in style objects', () => {
    expectClean(`export const x = (
      <p style={{
        fontWeight: 'var(--type-body-weight)',
        lineHeight: 'var(--type-body-line-height)',
        letterSpacing: 'var(--type-body-letter-spacing)',
      }}>x</p>
    )`, { policy: ROLE_POLICY })
  })

  it('allows registered D9 role tokens in CSS declarations', () => {
    expectClean(`.x {
      font-weight: var(--type-body-weight);
      line-height: var(--type-body-line-height);
      letter-spacing: var(--type-body-letter-spacing);
    }`, { path: 'src/fixture.css', policy: ROLE_POLICY })
  })

  it('rejects D9 values through unregistered custom properties', () => {
    expectOne(`export const x = <p style={{ fontWeight: 'var(--my-weight)' }}>x</p>`, 'typography/unregistered-typography-token', 'fontWeight: var(--my-weight)')
    expectOne(`export const x = <p style={{ lineHeight: 'var(--my-leading)' }}>x</p>`, 'typography/unregistered-typography-token', 'lineHeight: var(--my-leading)')
    expectOne(`export const x = <p style={{ letterSpacing: 'var(--my-tracking)' }}>x</p>`, 'typography/unregistered-typography-token', 'letterSpacing: var(--my-tracking)')
  })

  it('allows arbitrary D9 utilities that reference registered tokens', () => {
    expectClean('export const x = <p className="leading-[var(--type-body-line-height)]">x</p>', { policy: ROLE_POLICY })
  })

  it('rejects arbitrary D9 utilities that reference unregistered tokens', () => {
    expectOne('export const x = <p className="leading-[var(--my-leading)]">x</p>', 'typography/unregistered-typography-token', 'leading-[var(--my-leading)]')
  })

  it('fails closed on dynamic D9 style values it cannot prove', () => {
    expectOne('export const x = <p style={{ fontWeight: weight }}>x</p>', 'typography/font-weight-arbitrary', 'fontWeight: weight')
    expectOne('export const x = <p style={{ lineHeight: `calc(${lh})` }}>x</p>', 'typography/line-height-arbitrary', 'lineHeight: `calc(${lh})`')
  })

  it('allows CSS-wide keywords and the normal initial value (they override nothing)', () => {
    expectClean(`export const x = (
      <>
        <p style={{ fontWeight: 'inherit' }}>a</p>
        <p style={{ lineHeight: 'normal' }}>b</p>
        <p style={{ letterSpacing: 'unset' }}>c</p>
      </>
    )`)
  })

  it('flags a dynamic leading utility built by template interpolation', () => {
    expectOne('export const x = <p className={`leading-${n}`}>x</p>', 'typography/unsupported-text-utility', 'leading-${…}')
  })
})

describe('SVG typography scope (review: typography-svg-scope)', () => {
  it('rejects sub-floor text in a standalone SVG file (review fixture)', () => {
    expectOne(
      '<svg><text font-size="11px">Label</text></svg>',
      'typography/font-size-below-floor',
      'fontSize: 11px',
      { path: 'src/icon.svg' },
    )
  })

  it('rejects a string fontSize prop on JSX SVG text (review fixture)', () => {
    expectOne('export const X = () => <svg><text fontSize="11px">Label</text></svg>', 'typography/font-size-below-floor', 'fontSize: 11px')
  })

  it('rejects a numeric fontSize prop on JSX SVG text (review fixture)', () => {
    expectOne('export const X = () => <svg><text fontSize={11}>Label</text></svg>', 'typography/font-size-below-floor', 'fontSize: 11')
  })

  it('allows an SVG font-size that clears the floor', () => {
    expectClean('<svg><text font-size="14px">Label</text></svg>', { path: 'src/icon.svg' })
  })

  it('allows an SVG font-size through a registered token that resolves floor-safe', () => {
    expectClean('<svg><text font-size="var(--type-caption-size)">Label</text></svg>', { path: 'src/icon.svg', policy: RESOLVED_POLICY })
  })

  it('rejects sub-floor text inside an SVG <style> element (CSS body is scanned)', () => {
    expectOne(
      '<svg><style>.t { font-size: 10px; }</style><text class="t">L</text></svg>',
      'typography/font-size-below-floor',
      'font-size: 10px',
      { path: 'src/icon.svg' },
    )
  })

  it('rejects a sub-floor size in an SVG inline style attribute', () => {
    expectOne(
      '<svg><text style="font-size: 11px">L</text></svg>',
      'typography/font-size-below-floor',
      'font-size: 11px',
      { path: 'src/icon.svg' },
    )
  })

  it('classifies Tailwind class attributes on SVG text elements', () => {
    expectOne(
      '<svg><text class="text-xs">L</text></svg>',
      'typography/text-size-below-floor',
      'text-xs',
      { path: 'src/icon.svg' },
    )
  })

  it('rejects an SVG font-family attribute stack as a literal family', () => {
    expectOne(
      `<svg><text font-family="Arial">L</text></svg>`,
      'typography/font-family-literal',
      'fontFamily: Arial',
      { path: 'src/icon.svg' },
    )
  })

  it('locates the SVG finding on its real line', () => {
    const source = '<svg>\n  <text font-size="11px">Label</text>\n</svg>'
    const finding = expectOne(source, 'typography/font-size-below-floor', 'fontSize: 11px', { path: 'src/icon.svg' })
    assert.equal(finding.line, 2)
  })

  it('fails closed on malformed SVG markup instead of returning empty success', () => {
    const found = scan({ path: 'src/broken.svg', source: '<svg><text font-size=11px>L</text></svg>', policy: POLICY })
    assert.equal(found.length, 1)
    assert.equal(found[0].ruleId, 'typography/parse-failure')
    assert.ok(found[0].syntax.length > 0)
  })
})

describe('indirection and dynamic values (review: typography-indirection-and-dynamic-values)', () => {
  it('resolves aliased imported typography configuration with the supplied module snapshot', () => {
    const path = 'src/card.tsx'
    const source = `import { typography as type } from './theme'; export const Card = () => <p className={type.caption}>x</p>`
    const modules = {
      [path]: source,
      'src/theme.ts': `export const typography = { caption: 'text-[11px]', body: 'text-base' } as const`,
    }
    assert.deepEqual(findings(source, { path, modules }).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/arbitrary-text-size', syntax: 'text-[11px]' },
    ])
  })

  it('inspects every static return branch of an imported pure typography helper', () => {
    const path = 'src/card.tsx'
    const source = `import { textClass } from './theme'; export const Card = ({ compact }) => <p className={textClass(compact)}>x</p>`
    const modules = {
      [path]: source,
      'src/theme.ts': `export function textClass(compact: boolean) { return compact ? 'text-[10px]' : 'text-base' }`,
    }
    assert.deepEqual(findings(source, { path, modules }).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/arbitrary-text-size', syntax: 'text-[10px]' },
    ])
  })

  it('resolves imported static style values without treating unrelated siblings as typography', () => {
    const path = 'src/card.tsx'
    const source = `import { styles } from './theme'; export const Card = () => <p style={{ fontSize: styles.caption, width: styles.width }}>x</p>`
    const modules = {
      [path]: source,
      'src/theme.ts': `export const styles = Object.freeze({ caption: '10px', width: '8px' })`,
    }
    assert.deepEqual(findings(source, { path, modules }).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/font-size-below-floor', syntax: 'fontSize: 10px' },
    ])
  })

  it('fails closed for missing modules, cyclic exports, and impure imported helpers', () => {
    const missingPath = 'src/missing.tsx'
    const missing = `import { typeClass } from './absent'; export const X = () => <p className={typeClass}>x</p>`
    assert.deepEqual(findings(missing, { path: missingPath, modules: { [missingPath]: missing } }).map((f) => f.ruleId), ['typography/unsupported-text-utility'])

    const cyclePath = 'src/cycle.tsx'
    const cycle = `import { typeClass } from './a'; export const X = () => <p className={typeClass}>x</p>`
    const cycleModules = {
      [cyclePath]: cycle,
      'src/a.ts': `export { typeClass } from './b'`,
      'src/b.ts': `export { typeClass } from './a'`,
    }
    assert.deepEqual(findings(cycle, { path: cyclePath, modules: cycleModules }).map((f) => f.ruleId), ['typography/unsupported-text-utility'])

    const impurePath = 'src/impure.tsx'
    const impure = `import { textClass } from './theme'; export const X = () => <p className={textClass()}>x</p>`
    const impureModules = { [impurePath]: impure, 'src/theme.ts': `export function textClass() { console.log('x'); return 'text-[10px]' }` }
    assert.deepEqual(findings(impure, { path: impurePath, modules: impureModules }).map((f) => f.ruleId), ['typography/unsupported-text-utility'])
  })

  it('resolves local constant class strings instead of reporting them as opaque dynamics', () => {
    expectOne(`const tiny = 'text-xs'
export const X = () => <p className={tiny}>x</p>`, 'typography/text-size-below-floor', 'text-xs')
  })

  it('resolves local constant class arrays element by element', () => {
    expectOne(`const classes = ['text-xs', 'text-base']
export const X = () => <p className={classes}>x</p>`, 'typography/text-size-below-floor', 'text-xs')
  })

  it('scans literal array join and static string concatenation without opaque findings', () => {
    const source = `
      const joined = ['text-xs', ok ? 'font-body' : 'font-mono'].join(' ')
      const concatenated = 'text-' + 'sm'
      export const X = () => <><p className={joined}>a</p><p className={concatenated}>b</p></>
    `
    assert.deepEqual(findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-xs' },
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-sm' },
    ])
  })

  it('scans every static branch of conditionally concatenated class strings', () => {
    const source = `export const X = ({ compact }) => <p className={'font-body ' + (compact ? 'text-xs' : 'text-base')}>x</p>`
    assert.deepEqual(findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-xs' },
    ])
  })

  it('scans static maps and caller extensions inside a trimmed class template', () => {
    const source = `const SIZES = { sm: 'text-xs', lg: 'text-base' } as const; export function Icon({ size, className }) { return <button className={\`${'${SIZES[size]} ${className ?? \'\'}'}\`.trim()} /> }`
    assert.deepEqual(findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-xs' },
      { ruleId: 'typography/extension-boundary', syntax: 'Icon#className' },
    ])
  })

  it('scans every branch of a statically declared map with a runtime key', () => {
    const source = `const TYPES = { compact: 'text-xs', normal: 'text-base' } as const; export const X = ({ kind }) => <p className={TYPES[kind]}>x</p>`
    assert.deepEqual(findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-xs' },
    ])
  })

  it('resolves a nested property after a runtime-key static map lookup', () => {
    const source = `const TYPES = { compact: { className: 'text-xs' }, normal: { className: 'text-base' } } as const; export const X = ({ kind }) => <p className={TYPES[kind]?.className}>x</p>`
    assert.deepEqual(findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-xs' },
    ])
  })

  it('inspects a local pure helper return while leaving impure helpers unsupported', () => {
    const pure = `const typeFor = (compact: boolean) => compact ? 'text-xs' : 'text-base'; export const X = ({ compact }) => <p className={typeFor(compact)}>x</p>`
    assert.deepEqual(findings(pure).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-xs' },
    ])
    const impure = `const typeFor = () => { sideEffect(); return 'text-xs' }; export const X = () => <p className={typeFor()}>x</p>`
    assert.deepEqual(findings(impure).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/unsupported-text-utility', syntax: 'typeFor()' },
    ])
  })

  it('flags a conditional class inside clsx()', () => {
    expectOne(`export const X = () => <p className={clsx('text-base', cond && 'text-xs')}>x</p>`, 'typography/text-size-below-floor', 'text-xs')
  })

  it('scans style-object constants at their declaration site', () => {
    expectOne(`const styles = { fontSize: '11px' }
export const X = () => <p style={styles}>x</p>`, 'typography/font-size-below-floor', 'fontSize: 11px')
  })

  it('flags an unsupported dynamic fontSize alongside a spread style', () => {
    expectOne(`export const X = (props) => <p style={{ ...props.style, fontSize: size }}>x</p>`, 'typography/unsupported-font-size', 'fontSize: size')
  })

  it('resolves computed style keys that fold to a governed property name', () => {
    expectOne(`export const X = () => <p style={{ ['font' + 'Size']: '11px' }}>x</p>`, 'typography/font-size-below-floor', 'fontSize: 11px')
  })

  it('fails closed on unresolvable concatenation indirection', () => {
    expectOne(`const cls = 'text-' + name
export const X = () => <p className={cls}>x</p>`, 'typography/unsupported-text-utility', 'cls')
  })

  it('does not resolve mutable (let) bindings', () => {
    expectOne(`let cls = 'text-xs'
export const X = () => <p className={cls}>x</p>`, 'typography/unsupported-text-utility', 'cls')
  })

  it('does not resolve a name that is declared more than once (ambiguous binding)', () => {
    const source = `export function a() { const cls = 'text-xs'; return <p className={cls}/> }
export function b() { const cls = 'text-base'; return <p className={cls}/> }`
    const found = findings(source)
    assert.equal(found.length, 2)
    assert.deepEqual(found.map((f) => f.ruleId), ['typography/unsupported-text-utility', 'typography/unsupported-text-utility'])
  })
})

describe('token resolution and shadowing (review: typography-token-resolution-and-shadowing)', () => {
  it('rejects a registered size token whose resolved value falls below the floor', () => {
    expectOne(
      `export const X = () => <p style={{ fontSize: 'var(--type-caption-size)' }}>x</p>`,
      'typography/font-size-below-floor',
      'fontSize: var(--type-caption-size)',
      { policy: SUBFLOOR_RESOLVED_POLICY },
    )
  })

  it('allows a registered size token whose resolved value is floor-guarded', () => {
    expectClean(
      `.scope { font-size: var(--type-caption-size); }`,
      { path: 'src/fixture.css', policy: RESOLVED_POLICY },
    )
  })

  it('bounds registered var resolution inside arithmetic: resolved 12px minus 4px is 8px', () => {
    const policy = {
      tokenCssNames: ['--type-body-size'],
      resolvedTokens: { 'type.body.size': 'max(12px, 0.857142857rem)' },
    }
    expectOne(
      `export const X = () => <p style={{ fontSize: 'calc(var(--type-body-size) - 4px)' }}>x</p>`,
      'typography/font-size-below-floor',
      'fontSize: calc(var(--type-body-size) - 4px)',
      { policy },
    )
  })

  it('rejects a local redeclaration of a registered --type-* size token with an unsafe value', () => {
    expectOne(
      '.scope { --type-caption-size: 10px; font-size: var(--type-caption-size); }',
      'typography/token-shadow',
      '--type-caption-size: 10px',
      { path: 'src/fixture.css' },
    )
  })

  it('allows a local redeclaration whose value is provably floor-safe', () => {
    expectClean(
      '.scope { --type-caption-size: 20px; }',
      { path: 'src/fixture.css', policy: RESOLVED_POLICY },
    )
  })

  it('leaves unregistered non-type custom properties to the token-graph lane', () => {
    expectClean('.scope { --my-local: 10px; }', { path: 'src/fixture.css' })
  })

  it('leaves --font-* primitive definitions alone (primitives hold raw values by design)', () => {
    expectClean('.root { --font-weight-medium: 500; }', { path: 'src/fixture.css' })
  })

  it('keeps the canonical var-chain definition shape clean', () => {
    expectClean(
      ':root { --type-body-size: var(--font-size-body); }',
      { path: 'src/fixture.css', policy: { tokenCssNames: ['--type-body-size', '--font-size-body'], resolvedTokens: {} } },
    )
  })
})

describe('class builders route every reachable string through the rules', () => {
  it('scans every static branch of a locally declared cva variant', () => {
    const source = `import { cva as defineVariants } from 'class-variance-authority'; const badge = defineVariants('text-xs', { variants: { tone: { calm: 'text-base', loud: 'text-[11px]' } } }); export const X = ({ tone }) => <p className={badge({ tone })}>x</p>`
    assert.deepEqual(findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-xs' },
      { ruleId: 'typography/arbitrary-text-size', syntax: 'text-[11px]' },
    ])
  })

  it('resolves an imported cva declaration through the supplied module snapshot', () => {
    const path = 'src/view.tsx'
    const source = `import { badge } from './variants'; export const X = ({ tone }) => <p className={badge({ tone })}>x</p>`
    const modules = {
      [path]: source,
      'src/variants.ts': `import { cva } from 'class-variance-authority'; export const badge = cva('text-xs', { variants: { tone: { calm: 'text-base' } } })`,
    }
    assert.deepEqual(findings(source, { path, modules }).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/text-size-below-floor', syntax: 'text-xs' },
    ])
  })

  it('does not trust variant-looking names or dynamic cva definitions', () => {
    const fake = `const buttonVariants = makeVariants('text-xs'); export const X = () => <p className={buttonVariants({})}>x</p>`
    assert.deepEqual(findings(fake).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/unsupported-text-utility', syntax: 'buttonVariants({})' },
    ])
    const dynamic = `import { cva } from 'class-variance-authority'; const badge = cva(runtimeClasses); export const X = () => <p className={badge({})}>x</p>`
    assert.deepEqual(findings(dynamic).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/unsupported-text-utility', syntax: 'runtimeClasses' },
    ])
  })

  // P10 precision fix (dist/design-system-baseline/cli-lanes/claude-codemods/triage.json,
  // pattern P10): `(...inputs) => twMerge(clsx(inputs))` is cn()'s OWN
  // canonical definition shape (src/lib/utils.ts) — its own parameter
  // forwarded unchanged through a chain of CLASS_BUILDER calls, not a live
  // class value. This used to be pinned here as an (incorrect) unsupported
  // finding on `inputs`; the dedup behaviour the test title actually cares
  // about — nested CLASS_BUILDER calls around one unprovable value report
  // ONE finding, not two — is now proven with `OTHER`, a value that is NOT
  // the enclosing function's own transparently-forwarded parameter and so
  // must still fail closed exactly once.
  it('reports one source occurrence when class builders are nested', () => {
    assert.deepEqual(findings(`export const cn = (...inputs) => twMerge(clsx(OTHER))`).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/unsupported-text-utility', syntax: 'OTHER' },
    ])
  })

  // P10 precision fix: cn()'s own definition site is a transparent
  // pass-through of its own parameter, never a class value to prove —
  // src/lib/utils.ts's real shape (`function cn(...inputs) { return
  // twMerge(clsx(inputs)) }`) now scans clean.
  it('does not flag a CLASS_BUILDER function forwarding its own parameter at its definition site (cn()/utils.ts shape)', () => {
    assert.deepEqual(findings(`export function cn(...inputs) { return twMerge(clsx(inputs)) }`), [])
  })

  it('flags a conditional class inside cn()', () => {
    expectOne(`import { cn } from '../lib/utils'
export const x = <p className={cn(cond && 'text-xs', 'text-base')}>hi</p>`, 'typography/text-size-below-floor', 'text-xs')
  })

  it('flags the cva base string and nested variant strings separately', () => {
    const found = findings(`import { cva } from 'class-variance-authority'
export const v = cva('text-sm', { variants: { size: { sm: 'text-[11px]' } } })`)
    assert.deepEqual(
      found.map((f) => `${f.ruleId}|${f.syntax}`),
      ['typography/text-size-below-floor|text-sm', 'typography/arbitrary-text-size|text-[11px]'],
    )
  })

  it('flags a violation inside a template literal className', () => {
    expectOne('export const x = <p className={`p-4 text-xs`}>hi</p>', 'typography/text-size-below-floor', 'text-xs')
  })
})

describe('explicit typography extension boundaries', () => {
  it('classifies unchanged anonymous mock parameters by stable registration key and property path', () => {
    const source = `vi.mock('@router', () => ({ Link: ({ className = 'text-base' }) => <a className={cn('font-body', className)} /> }))`
    assert.deepEqual(findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })), [
      { ruleId: 'typography/extension-boundary', syntax: 'vi.mock(@router):Link#className' },
    ])
  })

  it('keeps anonymous boundary identity stable across trivia and counts repeated registrations separately', () => {
    const compact = `vi.mock('x',()=>({Link:({className})=><a className={className}/>}))`
    const spaced = `vi.mock( 'x', () => ({ Link: ({ className }) => <a className={className} /> }) )`
    assert.deepEqual(findings(compact).map((finding) => finding.syntax), ['vi.mock(x):Link#className'])
    assert.deepEqual(findings(spaced).map((finding) => finding.syntax), ['vi.mock(x):Link#className'])
    const repeated = `${compact}\n${compact}`
    assert.deepEqual(findings(repeated).map((finding) => finding.syntax), ['vi.mock(x):Link#className', 'vi.mock(x):Link#className'])
  })

  it('keeps computed, spread-wrapped, and reassigned anonymous forwarding unsupported', () => {
    const cases = [
      `vi.mock('x', () => ({ [component]: ({ className }) => <a className={className} /> }))`,
      `vi.mock('x', () => ({ ...{ Link: ({ className }) => <a className={className} /> } }))`,
      `vi.mock('x', () => ({ Link: ({ className }) => { className = 'text-base'; return <a className={className} /> } }))`,
    ]
    for (const source of cases) assert.deepEqual(findings(source).map((finding) => finding.ruleId), ['typography/unsupported-text-utility'])
  })

  it('classifies unchanged destructured className forwarding by receiving symbol', () => {
    expectOne('export function Button({ className }) { return <button className={className} /> }', 'typography/extension-boundary', 'Button#className')
  })

  it('classifies forwarding through a recognized builder and retains static sibling checks', () => {
    const found = findings("export const Panel = ({ className }) => <div className={cn('text-xs', className)} />")
    assert.deepEqual(found.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/text-size-below-floor|text-xs',
      'typography/extension-boundary|Panel#className',
    ])
  })

  it('uses the receiving variable through memo and forwardRef wrappers', () => {
    expectOne('export const Card = React.memo(React.forwardRef(({ className }, ref) => <div ref={ref} className={className} />))', 'typography/extension-boundary', 'Card#className')
  })

  it('scans a static default before classifying its forwarded parameter', () => {
    const found = findings("export const Label = ({ className = 'text-xs' }) => <span className={className} />")
    assert.deepEqual(found.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/text-size-below-floor|text-xs',
      'typography/extension-boundary|Label#className',
    ])
  })

  it('classifies body-destructured className forwarding from the props parameter by receiving symbol', () => {
    expectOne('export function Chip(props) { const { className } = props; return <div className={className} /> }', 'typography/extension-boundary', 'Chip#className')
  })

  it('classifies renamed body-destructured forwarding by the receiving prop, not the local alias', () => {
    expectOne('export function Chip(props) { const { className: chipClass } = props; return <div className={chipClass} /> }', 'typography/extension-boundary', 'Chip#className')
  })

  it('scans a body-destructured default before classifying its forwarded alias', () => {
    const found = findings("export const Label = (props) => { const { className = 'text-xs' } = props; return <span className={className} /> }")
    assert.deepEqual(found.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/text-size-below-floor|text-xs',
      'typography/extension-boundary|Label#className',
    ])
  })

  it('classifies the forwardRef body-destructured template shape (icon-button) while keeping static map values scanned', () => {
    const found = findings("export const IconButton = React.forwardRef((props, ref) => { const SIZES = { p: 'text-xs' }; const { className } = props; return <button ref={ref} className={`${SIZES.p} ${className ?? ''}`.trim()} /> })")
    assert.deepEqual(found.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/text-size-below-floor|text-xs',
      'typography/extension-boundary|IconButton#className',
    ])
  })

  it('does not classify through a same-named const shadow closer to the sink than the destructuring', () => {
    const found = findings('export function Chip(props) { const { className } = props; if (props.dark) { const className = "text-xs"; return <div className={className} /> } return <div className={className} /> }')
    assert.deepEqual(found.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/text-size-below-floor|text-xs',
      'typography/extension-boundary|Chip#className',
    ])
  })

  it('does not classify a forwarded parameter when a closer declaration shadows it at the sink', () => {
    expectOne("export function F({ className }) { if (theme) { const className = 'text-[10px]'; return <div className={className} /> } }", 'typography/arbitrary-text-size', 'text-[10px]')
    const found = findings("export function F({ className }) { if (theme) { const className = 'text-[10px]'; return <div className={className} /> } return <div className={className} /> }")
    assert.deepEqual(found.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/arbitrary-text-size|text-[10px]',
      'typography/extension-boundary|F#className',
    ])
  })

  it('keeps reassigned parameters and anonymous callback parameters unsupported', () => {
    expectOne("export const Box = ({ className }) => { className = normalize(className); return <div className={className} /> }", 'typography/unsupported-text-utility', 'className')
    expectOne('export const nodes = values.map((className) => <div className={className} />)', 'typography/unsupported-text-utility', 'className')
  })

  it('classifies a body-destructured className read off a NAMED destructured parameter element with a source-prefixed name, distinct from the SAME function\'s own direct className boundary (table.tsx real shape)', () => {
    const source = [
      'const Table = React.forwardRef(({ className, containerProps = {}, ...props }, ref) => {',
      '  const { className: containerClassName, onKeyDown, ...restContainerProps } = containerProps',
      '  return (',
      "    <div className={cn('relative w-full overflow-auto', containerClassName)}>",
      "      <table ref={ref} className={cn('w-full text-xs', className)} {...props} />",
      '    </div>',
      '  )',
      '})',
    ].join('\n')
    const found = findings(source)
    const boundaries = found.filter((f) => f.ruleId === 'typography/extension-boundary').map((f) => f.syntax)
    assert.deepEqual(boundaries.sort(), ['Table#className', 'Table#containerProps.className'])
    // The naming fix's whole point: two distinct receiving identities never
    // collapse to the same syntax string under one fingerprint.
    assert.equal(new Set(boundaries).size, 2)
  })

  it('keeps body-destructured forwarding with unproven provenance unsupported', () => {
    // helper/store outputs, member-object sources with fallbacks, non-className
    // props, `let` bindings and closures out of the nearest function are not
    // proven unchanged forwarding and must stay unsupported.
    expectOne('export function Pill(state) { const { className } = describeState(state); return <div className={className} /> }', 'typography/unsupported-text-utility', 'className')
    expectOne('const THEME = { className: "text-xs" }; export function Chip() { const { className } = THEME; return <div className={className} /> }', 'typography/unsupported-text-utility', 'className')
    expectOne('export function Wrap(container) { const { className: c } = container ?? {}; return <div className={c} /> }', 'typography/unsupported-text-utility', 'c')
    expectOne('export function Wrap(props) { const { containerClassName } = props; return <div className={containerClassName} /> }', 'typography/unsupported-text-utility', 'containerClassName')
    expectOne('export function Loose(props) { let { className } = props; return <div className={className} /> }', 'typography/unsupported-text-utility', 'className')
    expectOne('export function Chip(props) { const { className } = props; const render = () => <div className={className} />; return render() }', 'typography/unsupported-text-utility', 'className')
  })

  it('keeps body-destructured forwarding unsupported when the alias or the props parameter is mutated', () => {
    expectOne("export function Chip(props) { const { className } = props; className = 'text-xs'; return <div className={className} /> }", 'typography/unsupported-text-utility', 'className')
    expectOne('export function Chip(props) { props = merge(props); const { className } = props; return <div className={className} /> }', 'typography/unsupported-text-utility', 'className')
    expectOne("export function Chip(props) { props.className = 'text-xs'; const { className } = props; return <div className={className} /> }", 'typography/unsupported-text-utility', 'className')
    expectOne('export function Chip(props) { Object.assign(props, overrides); const { className } = props; return <div className={className} /> }', 'typography/unsupported-text-utility', 'className')
    expectOne('export function Chip(props) { delete props.className; const { className } = props; return <div className={className} /> }', 'typography/unsupported-text-utility', 'className')
  })

  it('ignores a boolean guard but inspects both dynamic string possibilities for || and ??', () => {
    expectClean("export const X = ({ active }) => <div className={cn(active && 'text-base')} />")
    expectOne("export const X = ({ className }) => <div className={cn(className || 'text-base')} />", 'typography/extension-boundary', 'X#className')
    expectOne("export const X = ({ className }) => <div className={cn(className ?? 'text-base')} />", 'typography/extension-boundary', 'X#className')
  })

  it('uses the actual unresolved AST expression in unsupported findings', () => {
    expectOne('export const X = (props) => <div className={props.classes.row} />', 'typography/unsupported-text-utility', 'props.classes.row')
  })
})

describe('CSS declarations', () => {
  it('rejects a sub-floor font-size declaration', () => {
    expectOne('.a { font-size: 11px; }', 'typography/font-size-below-floor', 'font-size: 11px', { path: 'src/fixture.css' })
  })

  it('rejects a sub-floor rem declaration', () => {
    expectOne('.a { font-size: 0.875rem; }', 'typography/font-size-below-floor', 'font-size: 0.875rem', { path: 'src/fixture.css' })
  })

  it('splits the CSS font shorthand into size and family findings', () => {
    const found = findings(`.a { font: 400 11px/1.4 'Inter', sans-serif; }`, { path: 'src/fixture.css' })
    assert.deepEqual(
      found.map((f) => f.ruleId).sort(),
      ['typography/font-family-literal', 'typography/font-size-below-floor'],
    )
  })

  it('rejects a literal family stack declaration', () => {
    expectOne(`.a { font-family: 'Inter', system-ui, sans-serif; }`, 'typography/font-family-literal', `font-family: 'Inter', system-ui, sans-serif`, { path: 'src/fixture.css' })
  })

  it('rejects a font size through an unregistered custom property', () => {
    expectOne('.a { font-size: var(--my-size); }', 'typography/unregistered-font-size-token', 'font-size: var(--my-size)', { path: 'src/fixture.css' })
  })

  it('rejects a sub-floor utility pulled in through @apply (review fixture)', () => {
    expectOne('.x { @apply text-xs; }', 'typography/text-size-below-floor', 'text-xs', { path: 'src/fixture.css' })
  })

  it('rejects an arbitrary weight utility pulled in through @apply', () => {
    expectOne('.x { @apply font-[350]; }', 'typography/font-weight-arbitrary', 'font-[350]', { path: 'src/fixture.css' })
  })

  it('allows @apply of named utilities registered in policy', () => {
    expectClean('.x { @apply font-medium; }', { path: 'src/fixture.css', policy: NAMED_UTILITY_POLICY })
  })

  it('rejects the sub-floor keyword font-size declarations', () => {
    expectOne('.a { font-size: xx-small; }', 'typography/font-size-below-floor', 'font-size: xx-small', { path: 'src/fixture.css' })
  })
})

describe('§D1 root clamp() with the runtime font-size preference (--user-font-size)', () => {
  // False positive found in review: a fully-tokenized clamp() whose only
  // non-token leaf is --user-font-size (the runtime, JS-written accessibility
  // preference — never meant to be a registered design token, per
  // ProfileSection.tsx and design-system/enforcement/ledger.json's founder-
  // approved permanent-category entry) was reported as
  // typography/unregistered-font-size-token, the same rule that fires for a
  // genuinely missing/misspelled token. The correct classification mirrors
  // spacing.mjs::analyzeEnvironmentSpacing's safe-area env() carve-out:
  // typography/extension-boundary — still blocking, still requires exact
  // central review, but no longer tells an engineer to go register a token
  // that must never exist.

  it('reclassifies the real library.css/globals.css shape as extension-boundary, not unregistered-font-size-token', () => {
    const found = expectOne(
      '.root { font-size: clamp(var(--font-root-minimum), var(--user-font-size, var(--font-root-default)), var(--font-root-maximum)); }',
      'typography/extension-boundary',
      'font-size: clamp(var(--font-root-minimum), var(--user-font-size), var(--font-root-maximum))',
      { path: 'src/fixture.css', policy: FONT_ROOT_CLAMP_POLICY },
    )
    assert.match(found.message, /--user-font-size/)
    assert.match(found.message, /extension boundary/i)
  })

  it('reclassifies the same shape with no default-fallback var() (var(--user-font-size) alone)', () => {
    expectOne(
      '.root { font-size: clamp(var(--font-root-minimum), var(--user-font-size), var(--font-root-maximum)); }',
      'typography/extension-boundary',
      'font-size: clamp(var(--font-root-minimum), var(--user-font-size), var(--font-root-maximum))',
      { path: 'src/fixture.css', policy: FONT_ROOT_CLAMP_POLICY },
    )
  })

  it('ADVERSARIAL: still reports clamp(12px, var(--user-font-size), 20px) — raw pixel bounds are not "fully tokenized" (the pre-existing §D1 root-clamp fixture, unchanged)', () => {
    expectOne(
      '.root { font-size: clamp(12px, var(--user-font-size, 14px), 20px); }',
      'typography/unregistered-font-size-token',
      'font-size: clamp(12px, var(--user-font-size), 20px)',
      { path: 'src/fixture.css', policy: FONT_ROOT_CLAMP_POLICY },
    )
  })

  it('ADVERSARIAL: a raw pixel bound on only one side still forces the ordinary unregistered path (partial tokenization does not qualify)', () => {
    expectOne(
      '.root { font-size: clamp(var(--font-root-minimum), var(--user-font-size), 20px); }',
      'typography/unregistered-font-size-token',
      'font-size: clamp(var(--font-root-minimum), var(--user-font-size), 20px)',
      { path: 'src/fixture.css', policy: FONT_ROOT_CLAMP_POLICY },
    )
  })

  it('ADVERSARIAL: a genuinely unregistered token alongside --user-font-size still reports unregistered, not extension-boundary', () => {
    expectOne(
      '.root { font-size: clamp(var(--font-root-minimum), var(--user-font-size), var(--not-a-real-token)); }',
      'typography/unregistered-font-size-token',
      'font-size: clamp(var(--font-root-minimum), var(--user-font-size), var(--not-a-real-token))',
      { path: 'src/fixture.css', policy: FONT_ROOT_CLAMP_POLICY },
    )
  })

  it('ADVERSARIAL: a bare font-size: var(--user-font-size) with no clamp/bounds is still unregistered (the carve-out is clamp/min/max-scoped, not a blanket exemption for the name)', () => {
    expectOne(
      '.root { font-size: var(--user-font-size); }',
      'typography/unregistered-font-size-token',
      'font-size: var(--user-font-size)',
      { path: 'src/fixture.css', policy: FONT_ROOT_CLAMP_POLICY },
    )
  })

  it('a sub-floor bound still reports below-floor even with --user-font-size present (the carve-out never masks a real floor violation)', () => {
    const subFloorPolicy = {
      tokenCssNames: ['--font-root-minimum', '--font-root-maximum'],
      resolvedTokens: {},
      resolvedCssTokens: { '--font-root-minimum': '8px', '--font-root-maximum': '20px' },
    }
    expectOne(
      '.root { font-size: clamp(var(--font-root-minimum), var(--user-font-size), var(--font-root-maximum)); }',
      'typography/font-size-below-floor',
      'font-size: clamp(var(--font-root-minimum), var(--user-font-size), var(--font-root-maximum))',
      { path: 'src/fixture.css', policy: subFloorPolicy },
    )
  })
})

describe('fail-closed: unsupported governed expressions', () => {
  it('flags em sizes as unprovable (parent-relative)', () => {
    expectOne(`export const x = <span style={{ fontSize: '0.9em' }} />`, 'typography/unsupported-font-size', 'fontSize: 0.9em')
  })

  it('flags percentage sizes as unprovable (review fixture)', () => {
    expectOne(`export const x = <span style={{ fontSize: '75%' }} />`, 'typography/unsupported-font-size', 'fontSize: 75%')
  })

  it('flags vh sizes as unprovable (no height contract)', () => {
    expectOne(`export const x = <span style={{ fontSize: '2vh' }} />`, 'typography/unsupported-font-size', 'fontSize: 2vh')
  })

  it('flags vmin sizes as unprovable (review fixture)', () => {
    expectOne(`export const x = <span style={{ fontSize: '2vmin' }} />`, 'typography/unsupported-font-size', 'fontSize: 2vmin')
  })

  it('flags lh/rlh/ch sizes as unprovable (review fixtures)', () => {
    expectOne(`export const x = <span style={{ fontSize: '1lh' }} />`, 'typography/unsupported-font-size', 'fontSize: 1lh')
    expectOne(`export const x = <span style={{ fontSize: '1rlh' }} />`, 'typography/unsupported-font-size', 'fontSize: 1rlh')
    expectOne(`export const x = <span style={{ fontSize: '2ch' }} />`, 'typography/unsupported-font-size', 'fontSize: 2ch')
  })

  it('flags the relative keyword sizes (parent-relative)', () => {
    expectOne(`export const x = <span style={{ fontSize: 'smaller' }} />`, 'typography/unsupported-font-size', 'fontSize: smaller')
    expectOne(`export const x = <span style={{ fontSize: 'larger' }} />`, 'typography/unsupported-font-size', 'fontSize: larger')
  })

  it('flags dynamic template font-size expressions', () => {
    expectOne('export const x = <span style={{ fontSize: `calc(${size} - 1px)` }} />', 'typography/unsupported-font-size', 'fontSize: calc(${…} - 1px)')
  })

  it('flags a dynamic text- utility built by template interpolation', () => {
    expectOne('export const x = <p className={`text-${size}`}>hi</p>', 'typography/unsupported-text-utility', 'text-${…}')
  })

  it('inspects whole-value template interpolations after static text instead of silently passing', () => {
    expectOne('function Chip(props){const {className:c}=props;return <div className={`safe ${c}`}/>}', 'typography/extension-boundary', 'Chip#className')
    const found = findings('function Chip(props){const {className:c}=props;return <div className={`text-xs ${c}`}/>}')
    assert.deepEqual(found.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/text-size-below-floor|text-xs',
      'typography/extension-boundary|Chip#className',
    ])
  })

  it('fails closed on unknown whole-value template interpolations after static text', () => {
    const found = findings('export function Chip(props){return <div className={`text-xs ${props.mode}`}/>}')
    assert.deepEqual(found.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/text-size-below-floor|text-xs',
      'typography/unsupported-text-utility|props.mode',
    ])
    expectOne('export function Chip(props){return <div className={`safe ${props.mode} p-4`}/>}', 'typography/unsupported-text-utility', 'props.mode')
    const trailing = findings('export function Chip(props){return <div className={`${props.mode} text-xs`}/>}')
    // static texts classify before span expressions, so text-xs comes first
    assert.deepEqual(trailing.map(({ ruleId, syntax }) => `${ruleId}|${syntax}`), [
      'typography/text-size-below-floor|text-xs',
      'typography/unsupported-text-utility|props.mode',
    ])
  })

  it('fails closed on fragment joins glued to static text or other interpolations', () => {
    expectOne('export function Chip(x){return <div className={`safe${x.a}`}/>}', 'typography/unsupported-text-utility', '`safe${x.a}`')
    expectOne('export function Chip(x){return <div className={`${x.a}`}/>}', 'typography/unsupported-text-utility', 'x.a')
    expectOne('export function Chip(x){return <div className={`safe${x.a} more`}/>}', 'typography/unsupported-text-utility', '`safe${x.a} more`')
    expectOne('export function Chip(x){return <div className={`a ${x.a}${x.b} b`}/>}', 'typography/unsupported-text-utility', '`a ${x.a}${x.b} b`')
    expectOne('export function Chip(x){return <div className={`a ${x.a}${x.b}`}/>}', 'typography/unsupported-text-utility', '`a ${x.a}${x.b}`')
    expectOne('export function Chip(x){return <div className={`text-xs${x.a}`}/>}', 'typography/unsupported-text-utility', '`text-xs${x.a}`')
  })

  // Task 1 regression pin (independent review, nonblocking note #1):
  // "glue-after-with-static" — a static fragment glued directly AFTER an
  // interpolation (no leading-glue involved). GA1/GA2 in the review's own
  // probe battery; verified correct there but unpinned by any test or
  // mutation (M8/M9 both targeted glue-BEFORE/adjacent-empty, not this
  // branch). Whole template must fail closed exactly like every other glued
  // shape — never silently pass, never partially resolve.
  it('keeps a static fragment glued directly after an interpolation failing closed (glue-after-with-static, GA1/GA2)', () => {
    expectOne('export function Chip(x){return <div className={`${x.a}suffix`}/>}', 'typography/unsupported-text-utility', '`${x.a}suffix`')
    expectOne('export function Chip(x){return <div className={`safe ${x.a}suffix`}/>}', 'typography/unsupported-text-utility', '`safe ${x.a}suffix`')
  })

  it('keeps governed fragment prefixes failing closed as fragments, never as whole classes', () => {
    expectOne('export function Chip(x){return <div className={`text-${x.size}`}/>}', 'typography/unsupported-text-utility', 'text-${…}')
    expectOne('export function Chip(x){return <div className={`leading-${x.l}`}/>}', 'typography/unsupported-text-utility', 'leading-${…}')
    expectOne('export function Chip(x){return <div className={`font-[${x.f}`}/>}', 'typography/unsupported-text-utility', 'font-[${…}')
  })

  it('walks nested conditionals and clean whole-value template interpolations', () => {
    expectClean("export function Chip(x){return <div className={`safe ${x.cond ? 'text-base' : 'font-body'}`}/>}")
    expectOne('function Chip(props){const {className:c}=props;return <div className={`safe ${props.on ? c : \'text-base\'}`}/>}', 'typography/extension-boundary', 'Chip#className')
  })

  it('treats escaped template characters as ordinary static text', () => {
    expectOne('function Chip(props){const {className:c}=props;return <div className={`sa\\`fe ${c}`}/>}', 'typography/extension-boundary', 'Chip#className')
  })

  it('flags a dynamic text- utility built by concatenation', () => {
    expectOne(`export const x = <p className={'text-' + size}>hi</p>`, 'typography/unsupported-text-utility', 'text-${…}')
  })

  it('flags an unresolvable dynamic className expression', () => {
    expectOne('export const x = <p className={styles.row}>hi</p>', 'typography/unsupported-text-utility', 'styles.row')
  })

  it('flags an untyped var arbitrary utility even when the var is a registered token', () => {
    expectOne('export const x = <p className="text-[var(--type-caption-size)]">hi</p>', 'typography/unsupported-text-utility', 'text-[var(--type-caption-size)]')
  })

  it('flags a runtime preference channel that is not a design token (the §D1 root-clamp variable)', () => {
    expectOne(`.root { font-size: clamp(12px, var(--user-font-size, 14px), 20px); }`, 'typography/unregistered-font-size-token', 'font-size: clamp(12px, var(--user-font-size), 20px)', { path: 'src/fixture.css' })
  })

  it('reports a cva variant resolver in className as exactly one dynamic finding', () => {
    const found = findings(`const v = cva('text-base')
export const x = <p className={v({ variant })}>hi</p>`)
    assert.deepEqual(
      found.map((f) => f.ruleId),
      ['typography/unsupported-text-utility'],
    )
  })
})

describe('fail-closed: parse failures become explicit findings', () => {
  it('reports TypeScript parse diagnostics instead of trusting a broken tree', () => {
    const found = scan({ path: 'src/broken.tsx', source: 'export const x = <div className=', policy: POLICY })
    assert.equal(found.length, 1)
    assert.equal(found[0].ruleId, 'typography/parse-failure')
    assert.ok(found[0].syntax.length > 0)
  })

  it('reports PostCSS syntax errors instead of returning empty success', () => {
    const found = scan({ path: 'src/broken.css', source: '.a { font-size: 11px ', policy: POLICY })
    assert.equal(found.length, 1)
    assert.equal(found[0].ruleId, 'typography/parse-failure')
    assert.ok(found[0].message.includes('parse'))
  })
})

describe('display positions are 1-based and point at the offending syntax', () => {
  it('locates a class token on its real line', () => {
    const source = "const a = 1\nconst b = <p className=\"text-xs\">hi</p>"
    const finding = expectOne(source, 'typography/text-size-below-floor', 'text-xs')
    assert.equal(finding.line, 2)
    assert.ok(finding.column > 1)
  })

  it('locates a CSS declaration on its real line', () => {
    const finding = expectOne('.root {\n  font-size: 11px;\n}', 'typography/font-size-below-floor', 'font-size: 11px', { path: 'src/fixture.css' })
    assert.equal(finding.line, 2)
  })
})


describe('nested custom-property text utility classification', () => {
  // The enforcement contract requires specific type-hint debt for well-formed
  // ambiguous references, and fail-closed findings for unsupported grammar.
  const cases = [
    ['unknown outer', 'var(--missing,var(--color-primary))', 'typography/missing-arbitrary-type-hint'],
    ['unknown fallback', 'var(--color-primary,var(--missing))', 'typography/missing-arbitrary-type-hint'],
    ['unregistered colour-looking fallback', 'var(--color-primary,var(--color-unknown))', 'typography/missing-arbitrary-type-hint'],
    ['unknown deepest fallback', 'var(--color-primary,var(--color-primary,var(--missing)))', 'typography/missing-arbitrary-type-hint'],
    ['registered colours', 'var(--color-primary,var(--color-primary))', null],
    ['registered nested colours', 'var(--color-primary,var(--color-primary,var(--color-primary)))', null],
    ['typography fallback', 'var(--color-primary,var(--type-body-size))', 'typography/unsupported-text-utility'],
    ['missing closing parenthesis', 'var(--missing,var(--color-primary)', 'typography/unsupported-text-utility'],
    ['empty nested fallback', 'var(--missing,var())', 'typography/unsupported-text-utility'],
    ['trailing tokens', 'var(--missing,var(--color-primary))oops', 'typography/unsupported-text-utility'],
    ['opaque fallback', 'var(--missing,opaque(--color-primary))', 'typography/unsupported-text-utility'],
  ]
  for (const [name, value, expectedRule] of cases) {
    it(name, () => {
      const utility = `text-[${value}]`
      const findings = scan({ path: 'src/example.tsx', source: `export const x = <p className="${utility}" />`, policy: POLICY })
      assert.deepEqual(findings.map(({ ruleId, syntax }) => ({ ruleId, syntax })), expectedRule ? [{ ruleId: expectedRule, syntax: utility }] : [])
    })
  }
})

// ---------------------------------------------------------------------------
// Array-callback property read: `ARRAY.filter(...).map((item) => ... item.prop
// ...)`. Closes TaskDetailPanel.tsx's `STATUS_OPTIONS.filter((o) => ...).map((o)
// => ({ ..., className: cn('text-xs', o.color) }))` — `o.color` is a property
// read off a `.map()` callback's own parameter, whose finite set of possible
// values is exactly the never-mutated const array's own element `.color`
// values (arrayBindingUsesSafe/resolveArrayCallbackPropertyAccess in
// typography.mjs). Clean values use the registered `--color-primary` token
// (parity with the rest of this file); the forbidden value is the same
// sub-floor `text-[10px]` used throughout.
describe('capability: array-callback property read (STATUS_OPTIONS.filter(...).map((o) => o.color))', () => {
  it('PERMITTED: a local never-mutated const array read via .map((item) => item.prop) resolves to its clean element values', () => {
    expectClean(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }, { id: 'b', color: 'text-[var(--color-primary)]' }]\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
    )
  })

  it('PERMITTED: the same read through a leading .filter(...) (the real STATUS_OPTIONS shape) resolves clean', () => {
    expectClean(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }, { id: 'b', color: 'text-[var(--color-primary)]' }]\n" +
      "export function C({ id }) { return OPTIONS.filter((o) => o.id === id).map((o) => <i key={o.id} className={o.color} />) }",
    )
  })

  it('FORBIDDEN: a real sub-floor violation living in one element still surfaces', () => {
    expectOne(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }, { id: 'b', color: 'text-[10px]' }]\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/arbitrary-text-size',
      'text-[10px]',
    )
  })

  it('PERMITTED: an imported never-mutated const array resolves through the same capability', () => {
    const modules = {
      'src/options.ts': "export const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]",
    }
    expectClean(
      "import { OPTIONS } from './options'\nexport function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      { modules },
    )
  })

  it('mutation proof: a .push() anywhere on the array keeps the read unsupported', () => {
    expectOne(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]\n" +
      "OPTIONS.push({ id: 'b', color: 'text-[var(--color-primary)]' })\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/unsupported-text-utility', 'o.color',
    )
  })

  it('mutation proof: .sort() anywhere on the array keeps the read unsupported', () => {
    expectOne(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]\n" +
      "OPTIONS.sort()\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/unsupported-text-utility', 'o.color',
    )
  })

  it('mutation proof: a direct index assignment keeps the read unsupported', () => {
    expectOne(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]\n" +
      "OPTIONS[0] = { id: 'a', color: 'text-[10px]' }\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/unsupported-text-utility', 'o.color',
    )
  })

  it('mutation proof: the .map() callback writing through its own parameter keeps the read unsupported', () => {
    expectOne(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]\n" +
      "export function C() { return OPTIONS.map((o) => { o.color = 'text-[10px]'; return <i key={o.id} className={o.color} /> }) }",
      'typography/unsupported-text-utility', 'o.color',
    )
  })

  it('mutation proof: a .find() result aliased into a const and mutated keeps the array unsupported', () => {
    expectOne(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]\n" +
      "const found = OPTIONS.find((o) => o.id === 'a')\nif (found) found.color = 'text-[10px]'\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/unsupported-text-utility', 'o.color',
    )
  })

  it('mutation proof: an exported array mutated by a downstream importer keeps the read unsupported', () => {
    const modules = {
      'src/options.ts': "export const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]",
      'src/writer.ts': "import { OPTIONS } from './options'\nOPTIONS.push({ id: 'b', color: 'text-[10px]' })",
    }
    expectOne(
      "import { OPTIONS } from './options'\nexport function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/unsupported-text-utility', 'o.color',
      { modules },
    )
  })

  it('mutation proof: a `let` array is not trusted like a const literal', () => {
    expectOne(
      "let OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/unsupported-text-utility', 'o.color',
    )
  })

  it('mutation proof: a spread element in the array keeps the read unsupported', () => {
    expectOne(
      "const BASE = [{ id: 'a', color: 'text-[var(--color-primary)]' }]\n" +
      "const OPTIONS = [...BASE, { id: 'b', color: 'text-[10px]' }]\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/unsupported-text-utility', 'o.color',
    )
  })

  it('mutation proof: a spread property inside an element keeps the read unsupported', () => {
    expectOne(
      "const EXTRA = { color: 'text-[10px]' }\n" +
      "const OPTIONS = [{ id: 'a', ...EXTRA }]\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
      'typography/unsupported-text-utility', 'o.color',
    )
  })

  it('a .find() result read inline (never aliased) does not itself block the array — parity control for taskStatusConfig.ts\'s statusLabel-style read', () => {
    expectClean(
      "const OPTIONS = [{ id: 'a', color: 'text-[var(--color-primary)]' }]\n" +
      "function label(id) { return OPTIONS.find((o) => o.id === id)?.color ?? id }\n" +
      "export function C() { return OPTIONS.map((o) => <i key={o.id} className={o.color} />) }",
    )
  })
})
