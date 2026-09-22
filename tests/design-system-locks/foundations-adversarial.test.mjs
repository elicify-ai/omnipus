import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { scan as scanCssColors } from '../../scripts/design-system-locks/css-colors.mjs'
import { scan as scanTypography } from '../../scripts/design-system-locks/typography.mjs'

// Contract-derived adversarial suite for E1/D2/D3/D9.
// The oracle is deliberately rule-name agnostic: each governed violation must
// produce at least one finding or a deliberate scanner error. Empty success is
// always a failure. Registered token references provide the clean controls.
const POLICY = Object.freeze({
  tokenCssNames: Object.freeze([
    '--color-primary',
    '--type-caption-size',
    '--type-body-family',
    '--type-body-weight',
    '--type-body-line-height',
    '--type-body-letter-spacing',
  ]),
  resolvedTokens: Object.freeze({
    'color.text.primary': '#E2E8F0',
    'type.caption.size': 'max(12px, 0.857142rem)',
    'type.body.family': 'Inter, system-ui, sans-serif',
    'type.body.weight': 400,
    'type.body.lineHeight': 1.5,
    'type.body.letterSpacing': '0em',
  }),
})

function assertFinding(scan, input, label) {
  const result = scan({ ...input, policy: POLICY })
  assert.ok(Array.isArray(result), `${label}: scanner must return a findings array`)
  assert.ok(result.length > 0, `${label}: governed violation returned empty success`)
  for (const finding of result) {
    assert.equal(finding.path, input.path, `${label}: finding must retain the exact input path`)
    assert.ok(typeof finding.ruleId === 'string' && finding.ruleId.length > 0, `${label}: finding needs a stable ruleId`)
    assert.ok(typeof finding.syntax === 'string' && finding.syntax.length > 0, `${label}: finding needs canonical offending syntax`)
    assert.ok(typeof finding.message === 'string' && finding.message.length > 0, `${label}: finding needs an actionable message`)
  }
}

function assertClean(scan, input, label) {
  const result = scan({ ...input, policy: POLICY })
  assert.deepEqual(result, [], `${label}: registered-token control must be clean, got ${JSON.stringify(result)}`)
}

describe('E1/D3 CSS colors fail closed at token and embedded-asset boundaries', () => {
  it('rejects an unregistered color variable in a direct color declaration', () => {
    assertFinding(scanCssColors, {
      path: 'src/styles/adversarial.css',
      source: '.x { color: var(--local-foreground) }',
    }, 'direct unregistered CSS color variable')
  })

  it('rejects an unregistered color variable nested in a registered fallback', () => {
    assertFinding(scanCssColors, {
      path: 'src/styles/adversarial.css',
      source: '.x { color: var(--color-primary, var(--local-foreground)) }',
    }, 'nested unregistered CSS color fallback')
  })

  it('rejects raw SVG paint embedded in a CSS data URI', () => {
    assertFinding(scanCssColors, {
      path: 'src/styles/adversarial.css',
      source: `.x { background-image: url("data:image/svg+xml,<svg xmlns='http://www.w3.org/2000/svg'><path fill='%23ffffff'/></svg>") }`,
    }, 'data-URI SVG raw fill')
  })

  it('keeps a registered color token reference clean', () => {
    assertClean(scanCssColors, {
      path: 'src/styles/adversarial.css',
      source: '.x { color: var(--color-primary) }',
    }, 'registered CSS color token')
  })
})

describe('D2 typography covers SVG text in files and JSX', () => {
  it('rejects sub-floor text in a standalone SVG file', () => {
    assertFinding(scanTypography, {
      path: 'src/assets/adversarial.svg',
      source: '<svg xmlns="http://www.w3.org/2000/svg"><text font-size="11px">Label</text></svg>',
    }, 'standalone SVG text floor')
  })

  it('rejects a string fontSize prop on JSX SVG text', () => {
    assertFinding(scanTypography, {
      path: 'src/components/Adversarial.tsx',
      source: 'export const Adversarial = () => <svg><text fontSize="11px">Label</text></svg>',
    }, 'JSX SVG string fontSize')
  })

  it('rejects a numeric fontSize prop on JSX SVG text', () => {
    assertFinding(scanTypography, {
      path: 'src/components/Adversarial.tsx',
      source: 'export const Adversarial = () => <svg><text fontSize={11}>Label</text></svg>',
    }, 'JSX SVG numeric fontSize')
  })
})

describe('D9 governs weight, leading and tracking across supported syntaxes', () => {
  const cases = [
    ['arbitrary weight utility', 'src/components/Adversarial.tsx', 'export const X = () => <p className="font-[350]">x</p>'],
    ['arbitrary leading utility', 'src/components/Adversarial.tsx', 'export const X = () => <p className="leading-[13px]">x</p>'],
    ['arbitrary tracking utility', 'src/components/Adversarial.tsx', 'export const X = () => <p className="tracking-[.1em]">x</p>'],
    ['literal style weight', 'src/components/Adversarial.tsx', 'export const X = () => <p style={{ fontWeight: 350 }}>x</p>'],
    ['literal style leading', 'src/components/Adversarial.tsx', "export const X = () => <p style={{ lineHeight: '13px' }}>x</p>"],
    ['literal style tracking', 'src/components/Adversarial.tsx', "export const X = () => <p style={{ letterSpacing: '.1em' }}>x</p>"],
    ['literal CSS weight', 'src/styles/adversarial.css', '.x { font-weight: 350 }'],
    ['literal CSS leading', 'src/styles/adversarial.css', '.x { line-height: 13px }'],
    ['literal CSS tracking', 'src/styles/adversarial.css', '.x { letter-spacing: .1em }'],
  ]

  for (const [label, path, source] of cases) {
    it(`rejects ${label}`, () => {
      assertFinding(scanTypography, { path, source }, label)
    })
  }

  it('allows registered D9 token references in JSX styles', () => {
    assertClean(scanTypography, {
      path: 'src/components/Adversarial.tsx',
      source: `export const X = () => <p style={{
        fontWeight: 'var(--type-body-weight)',
        lineHeight: 'var(--type-body-line-height)',
        letterSpacing: 'var(--type-body-letter-spacing)',
      }}>x</p>`,
    }, 'registered JSX D9 role tokens')
  })

  it('allows registered D9 token references in CSS', () => {
    assertClean(scanTypography, {
      path: 'src/styles/adversarial.css',
      source: `.x {
        font-weight: var(--type-body-weight);
        line-height: var(--type-body-line-height);
        letter-spacing: var(--type-body-letter-spacing);
      }`,
    }, 'registered CSS D9 role tokens')
  })
})

describe('scanners do not hide path or caller-supplied policy exemptions', () => {
  const paths = [
    'src/components/library/preview/ExceptionCandidate.tsx',
    'src/design-system/generated/adversarial.tsx',
    'src/styles/exception-candidate.css',
  ]

  for (const path of paths) {
    it(`still scans governed syntax at ${path}`, () => {
      const isCss = path.endsWith('.css')
      const scan = isCss ? scanCssColors : scanTypography
      const source = isCss ? '.x { color: #ffffff }' : 'export const X = () => <p className="text-[11px]">x</p>'
      assertFinding(scan, { path, source, exemptions: [{ path, reason: 'must be ignored by scanner' }] }, `path-independent scan for ${path}`)
    })
  }

  it('does not honor an undeclared policy exemption field', () => {
    const policyWithHiddenExemption = {
      ...POLICY,
      exemptions: [{ path: 'src/styles/adversarial.css', ruleId: 'all' }],
    }
    const result = scanCssColors({
      path: 'src/styles/adversarial.css',
      source: '.x { color: #ffffff }',
      policy: policyWithHiddenExemption,
    })
    assert.ok(result.length > 0, 'scanner must report raw color despite caller-supplied exemption data')
  })
})
