// Design-system lock scanner tests — spacing.mjs (D10 closed 4px/8px scale).
//
// Specification sources (expected values derive from these, never from the
// implementation under test):
//   - design-system/enforcement/contract.json — scannerApi, finding shape,
//     canonical syntax (parsed declaration / utility normalization, not whole
//     file), failClosed (parse failures and unsupported governed syntax are
//     explicit findings, never empty success).
//   - docs/internal/design/design-system-definition.md E1, D1 and D10 — spacing
//     off the 4px/8px scale is forbidden; hairlines and 1px borders are the
//     only built-in exceptions. Spacing and control geometry use pixels so they
//     do not jump when the user changes the 12–20px root; rem is for type.
//     Zero rem remains the dimensionless zero exception. Percentage padding
//     and fr-in-gap are not closed-scale spacing.
//   - docs/internal/design/design-system-foundation-policy.md — closed scale
//     0, 4, 8, 16, 24, 32, 40, 48, 64px; 44px chrome is preserved geometry,
//     not a spacing step; 1px is a border hairline, not padding/gap/margin.
//   - design-system/tokens/foundations.json — machine-readable scale and
//     font.root.default = 14px. Tests load that file through the shared
//     token resolver so the scanner is exercised against the real tokens.
//
// Fixtures are inline sources. The scanner must not be asked to walk the
// application tree from these tests.

import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { describe, it } from 'node:test'
import { fileURLToPath } from 'node:url'

import { validateTokenSources } from '../../scripts/design-system/tokens.mjs'
import { extensions, scan } from '../../scripts/design-system-locks/spacing.mjs'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '../..')

// D10 / foundation policy closed scale (px). Independent of scanner code.
const CLOSED_SCALE_PX = [0, 4, 8, 16, 24, 32, 40, 48, 64]
const ROOT_PX = 14
const OFF_SCALE_PX = 13
const ON_SCALE_PX = 8
const SCALE_MIN = 4
const SCALE_MAX = 64

const RULE = {
  offScale: 'spacing/off-scale',
  rootDependent: 'spacing/root-dependent',
  extensionBoundary: 'spacing/extension-boundary',
  missingSafeAreaFallback: 'spacing/missing-safe-area-fallback',
  invalidVar: 'spacing/invalid-var',
  unsupported: 'spacing/unsupported',
  parseError: 'spacing/parse-error',
}

function loadPolicy() {
  const foundations = JSON.parse(readFileSync(resolve(ROOT, 'design-system/tokens/foundations.json'), 'utf8'))
  const colors = JSON.parse(readFileSync(resolve(ROOT, 'design-system/tokens/colors.json'), 'utf8'))
  const { tokens, resolved } = validateTokenSources([colors, foundations])
  return {
    tokenCssNames: tokens.map((token) => token.css),
    resolvedTokens: resolved,
  }
}

const policy = loadPolicy()

function run(path, source, nextPolicy = policy, modules) {
  return scan({ path, source, policy: nextPolicy, modules })
}

function css(source) {
  return run('src/fixture.css', source)
}

function tsx(source) {
  return run('src/fixture.tsx', source)
}

function ts(source) {
  return run('src/fixture.ts', source)
}

function byRule(findings, ruleId) {
  return findings.filter((finding) => finding.ruleId === ruleId)
}

function syntaxes(findings, ruleId) {
  return byRule(findings, ruleId).map((finding) => finding.syntax)
}

function assertContractShape(finding, path) {
  assert.equal(finding.path, path)
  assert.equal(typeof finding.ruleId, 'string')
  assert.ok(finding.ruleId.length > 0, 'ruleId must be nonempty')
  assert.equal(typeof finding.syntax, 'string')
  assert.ok(finding.syntax.length > 0, 'syntax must be nonempty canonical form, not the whole file')
  assert.notEqual(finding.syntax, sourceIfWholeFile(finding), 'syntax must not be the whole source')
  assert.equal(typeof finding.message, 'string')
  assert.ok(finding.message.length > 0, 'message must be actionable')
  if (finding.line !== undefined) {
    assert.ok(Number.isInteger(finding.line) && finding.line > 0, 'line is display-only and must be a positive integer')
  }
  if (finding.column !== undefined) {
    assert.ok(Number.isInteger(finding.column) && finding.column > 0, 'column is display-only and must be a positive integer')
  }
}

function sourceIfWholeFile() {
  return undefined
}

describe('foundations policy oracle', () => {
  it('loads the closed D10 scale and 14px root from foundations.json', () => {
    assert.equal(policy.resolvedTokens['font.root.default'], `${ROOT_PX}px`)
    for (const [index, px] of CLOSED_SCALE_PX.entries()) {
      assert.equal(policy.resolvedTokens[`space.scale.${index}`], `${px}px`)
    }
    assert.ok(policy.tokenCssNames.includes('--space-2'))
    assert.ok(policy.tokenCssNames.includes('--space-control-gap'))
    assert.equal(policy.resolvedTokens['space.control.gap'], '8px')
    assert.equal(policy.resolvedTokens['chrome.header.height'], '44px')
    assert.equal(policy.resolvedTokens['border.width.hairline'], '1px')
  })
})

describe('scanner API', () => {
  it('declares CSS and script extensions with leading dots', () => {
    assert.deepEqual(extensions, ['.css', '.js', '.jsx', '.ts', '.tsx'])
  })

  it('throws when path is missing instead of returning empty success', () => {
    assert.throws(() => scan({ source: '.a { padding: 4px }' }), /path must be a non-empty string/)
  })

  it('throws when source is not a string instead of returning empty success', () => {
    assert.throws(() => scan({ path: 'src/fixture.css' }), /source must be a string/)
  })

  it('returns no findings for an empty file', () => {
    assert.deepEqual(css(''), [])
    assert.deepEqual(tsx(''), [])
  })

  it('emits the contract finding shape for an off-scale declaration', () => {
    const path = 'src/components/example/Fixture.css'
    const source = `.box {\n  padding: ${OFF_SCALE_PX}px;\n}`
    const findings = run(path, source)
    assert.equal(findings.length, 1)
    assertContractShape(findings[0], path)
    assert.equal(findings[0].ruleId, RULE.offScale)
    assert.equal(findings[0].syntax, `padding: ${OFF_SCALE_PX}px`)
    assert.equal(findings[0].line, 2)
  })
})

describe('CSS spacing declarations', () => {
  it('reports off-scale padding, margin, and gap pixel literals', () => {
    const source = `.box {\n  padding: ${OFF_SCALE_PX}px;\n  margin: ${OFF_SCALE_PX}px;\n  gap: ${OFF_SCALE_PX}px;\n}`
    assert.deepEqual(syntaxes(css(source), RULE.offScale), [
      `padding: ${OFF_SCALE_PX}px`,
      `margin: ${OFF_SCALE_PX}px`,
      `gap: ${OFF_SCALE_PX}px`,
    ])
  })

  it('keeps closed-scale pixel steps silent, including zero and the 64px max', () => {
    const decls = CLOSED_SCALE_PX.map((px, index) => `  --case-${index}: ignore;\n  padding: ${px}px;`).join('\n')
    const source = `.box {\n${decls}\n}`
    assert.deepEqual(byRule(css(source), RULE.offScale), [])
  })

  it('treats 3px and 5px as off-scale around the 4px step, and 63px/65px around 64px', () => {
    const source = `.box {\n  padding: ${SCALE_MIN - 1}px;\n  margin: ${SCALE_MIN + 1}px;\n  gap: ${SCALE_MAX - 1}px;\n  scroll-padding: ${SCALE_MAX + 1}px;\n}`
    assert.deepEqual(syntaxes(css(source), RULE.offScale), [
      `padding: ${SCALE_MIN - 1}px`,
      `margin: ${SCALE_MIN + 1}px`,
      `gap: ${SCALE_MAX - 1}px`,
      `scroll-padding: ${SCALE_MAX + 1}px`,
    ])
  })

  it('does not treat 44px chrome geometry as a spacing step when used as padding', () => {
    const findings = css('.box { padding: 44px; }')
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['padding: 44px'])
  })

  it('permits negative on-scale margin and reports negative off-scale margin', () => {
    const source = '.box { margin-top: -8px; margin-left: -13px; }'
    assert.deepEqual(syntaxes(css(source), RULE.offScale), ['margin-left: -13px'])
  })

  it('reports a mixed shorthand when any side is off-scale', () => {
    const source = `.box { padding: ${ON_SCALE_PX}px ${OFF_SCALE_PX}px; }`
    assert.deepEqual(syntaxes(css(source), RULE.offScale), [`padding: ${ON_SCALE_PX}px ${OFF_SCALE_PX}px`])
  })

  it('permits 1px border hairlines and rejects 1px padding, gap, and margin', () => {
    const source = `.box {\n  border: 1px solid black;\n  border-width: 1px;\n  outline-width: 1px;\n  padding: 1px;\n  gap: 1px;\n  margin: 1px;\n}`
    const findings = css(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), [
      'padding: 1px',
      'gap: 1px',
      'margin: 1px',
    ])
  })

  it('does not treat non-spacing geometry, auto, grid tracks, or flex lengths as spacing', () => {
    const source = `.box {\n  width: ${OFF_SCALE_PX}px;\n  height: 7px;\n  top: ${OFF_SCALE_PX}px;\n  inset: ${OFF_SCALE_PX}px;\n  flex-basis: ${OFF_SCALE_PX}px;\n  grid-template-columns: ${OFF_SCALE_PX}px 1fr;\n  letter-spacing: ${OFF_SCALE_PX}px;\n  margin: auto;\n}`
    assert.deepEqual(css(source), [])
  })

  it('does not silently accept percentage padding or fr used as gap', () => {
    const source = `.box {\n  padding: 50%;\n  padding-inline: 50%;\n  margin: 10%;\n  gap: 1fr;\n}`
    const findings = css(source)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [
      'padding: 50%',
      'padding-inline: 50%',
      'margin: 10%',
      'gap: 1fr',
    ])
  })

  it('rejects rem spacing, including rem that equals a closed step only at the 14px root', () => {
    // D1/D10: rem follows the user root. 0.285714285714rem is 4px at 14px and
    // 3.43px / 5.71px at the 12px / 20px bounds, so it is not pixel-stable.
    const rootDependentRem = '0.285714285714rem'
    assert.equal(Math.abs(Number.parseFloat(rootDependentRem) * ROOT_PX - SCALE_MIN) < 1e-6, true)
    const findings = css(`.box { padding: 0.5rem; margin: 1rem; gap: ${rootDependentRem}; }`)
    assert.deepEqual(syntaxes(findings, RULE.rootDependent), [
      'padding: 0.5rem',
      'margin: 1rem',
      `gap: ${rootDependentRem}`,
    ])
    assert.equal(byRule(findings, RULE.offScale).length, 0)
    assert.match(findings[0].message, /root/i)
  })

  it('treats 0rem as the closed-scale zero step', () => {
    assert.deepEqual(css('.box { padding: 0rem; margin: 0; }'), [])
  })

  it('accepts registered space token var() references including semantic aliases', () => {
    const source = `.box {\n  padding: var(--space-2);\n  gap: var(--space-control-gap);\n  margin: var(--space-page-margin);\n}`
    assert.deepEqual(css(source), [])
  })

  it('accepts calc() whose evaluated result is a closed pixel step', () => {
    // space-2 = 8px, space-3 = 16px (foundations.json). 8*2=16; 16+8=24; 4+4=8.
    const source = `.box {\n  gap: calc(var(--space-2) * 2);\n  margin: calc(var(--space-3) + 8px);\n  padding: calc(4px + 4px);\n}`
    assert.deepEqual(css(source), [])
  })

  it('reports calc() whose evaluated result is off the closed scale', () => {
    // 4*3=12px and 8/3≈2.667px are not closed steps. Token sum 8+4=12px.
    const source = `.box {\n  padding: calc(4px * 3);\n  gap: calc(8px / 3);\n  margin: calc(var(--space-2) + var(--space-1));\n}`
    assert.deepEqual(syntaxes(css(source), RULE.offScale), [
      'padding: calc(4px * 3)',
      'gap: calc(8px / 3)',
      'margin: calc(var(--space-2) + var(--space-1))',
    ])
  })

  it('reports unknown custom properties in spacing as invalid-var', () => {
    const source = '.box { padding: var(--not-a-token); gap: var(--space-missing); }'
    const findings = css(source)
    assert.deepEqual(syntaxes(findings, RULE.invalidVar), [
      'padding: var(--not-a-token)',
      'gap: var(--space-missing)',
    ])
    assert.match(findings[0].message, /not a registered/i)
  })

  it('does not treat a registered colour token as a spacing token', () => {
    const source = '.box { padding: var(--color-accent); }'
    const findings = css(source)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.unsupported)
    assert.equal(findings[0].syntax, 'padding: var(--color-accent)')
  })

  it('reports unsupported spacing expressions instead of accepting them', () => {
    const source = `.box {\n  padding: calc(theme(spacing.4));\n  margin: attr(data-gap);\n  gap: env(safe-area-inset-top);\n}`
    const findings = css(source)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [
      'padding: calc(theme(spacing.4))',
      'margin: attr(data-gap)',
    ])
    assert.deepEqual(syntaxes(findings, RULE.missingSafeAreaFallback), ['gap: env(safe-area-inset-top)'])
    assert.match(findings[0].message, /unsupported/i)
  })

  it('reports CSS parse failures as parse-error findings, never as empty success', () => {
    const findings = css('.box { padding: 8px ')
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.parseError)
    assert.ok(findings[0].syntax.length > 0)
    assert.notEqual(findings[0].syntax.includes('.box { padding: 8px'), true)
    assert.match(findings[0].message, /failed to parse/i)
  })

  it('scans @apply class lists with the same spacing rules as JSX utilities', () => {
    const source = '.box { @apply flex p-[13px] gap-[var(--space-2)]; }'
    const findings = css(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
  })
})

describe('Tailwind spacing utilities', () => {
  it('rejects default rem-based utilities instead of blessing a 14px-root coincidence', () => {
    // Tailwind spacing n → n * 0.25rem. p-2 and m-4 move with the user root.
    // A decimal whose 14px-root product is 4px (n = 4 / 3.5) must also fail.
    const onScaleOnlyAtDefault = 'p-1.142857142857'
    const source = `export const X = () => <div className="p-2 m-4 gap-2 ${onScaleOnlyAtDefault}" />`
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.rootDependent).sort(), ['gap-2', 'm-4', onScaleOnlyAtDefault, 'p-2'].sort())
    assert.equal(byRule(findings, RULE.offScale).length, 0)
  })

  it('permits p-0 and reports p-px because 1px is not a spacing step', () => {
    const source = 'export const X = () => <div className="p-0 p-px" />'
    assert.deepEqual(syntaxes(tsx(source), RULE.offScale), ['p-px'])
  })

  it('does not silently accept arbitrary off-scale gap, margin, or padding', () => {
    const source = 'export const X = () => <div className="gap-[13px] m-[10px] p-[6px]" />'
    assert.deepEqual(syntaxes(tsx(source), RULE.offScale).sort(), [
      'gap-[13px]',
      'm-[10px]',
      'p-[6px]',
    ])
  })

  it('permits arbitrary on-scale pixels and registered token refs in utilities', () => {
    const source = 'export const X = () => <div className="p-[4px] gap-[var(--space-2)] mx-[var(--space-control-gap)]" />'
    assert.deepEqual(tsx(source), [])
  })

  it('reports invalid token names inside arbitrary utilities', () => {
    const source = 'export const X = () => <div className="p-[var(--not-a-token)]" />'
    assert.deepEqual(syntaxes(tsx(source), RULE.invalidVar), ['p-[var(--not-a-token)]'])
  })

  it('covers negative, axis, and responsive utilities', () => {
    const source = 'export const X = () => <div className="-mt-[8px] -mt-[13px] px-[8px] py-[13px] sm:p-[13px] md:gap-[var(--space-2)]" />'
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale).sort(), [
      '-mt-[13px]',
      'py-[13px]',
      'sm:p-[13px]',
    ])
  })

  it('permits mx-auto and non-spacing geometry utilities, but not percent padding', () => {
    const source = 'export const X = () => <div className="mx-auto p-[50%] w-[13px] inset-4" />'
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['p-[50%]'])
    assert.equal(byRule(findings, RULE.offScale).length, 0)
  })

  it('reports arbitrary calc utilities from the evaluated pixel result', () => {
    const source = 'export const X = () => <div className="p-[calc(4px_*_3)] gap-[calc(4px_+_4px)]" />'
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[calc(4px_*_3)]'])
    assert.equal(syntaxes(findings, RULE.unsupported).length, 0)
  })

  it('extracts spacing classes from cn, clsx, and cva builders', () => {
    const source = `
      import { cn } from '@/lib/utils'
      import clsx from 'clsx'
      import { cva } from 'class-variance-authority'
      export const box = cn('flex', 'p-[13px]')
      export const row = clsx({ 'gap-[13px]': true, 'items-center': true })
      export const variants = cva('m-[8px]', { variants: { size: { sm: 'p-[13px]', lg: 'p-[var(--space-2)]' } } })
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale).sort(), ['p-[13px]', 'gap-[13px]', 'p-[13px]'].sort())
  })

  it('reports interpolated spacing utilities as unsupported expressions', () => {
    const source = 'export const X = (n: number) => <div className={`p-${n}`} />'
    const findings = tsx(source)
    assert.equal(byRule(findings, RULE.unsupported).length, 1)
    assert.match(findings[0].syntax, /p-\$/)
    assert.match(findings[0].message, /unsupported/i)
  })
})

describe('JSX style objects', () => {
  it('reports numeric and string padding using canonical CSS declaration syntax', () => {
    const source = `export const X = () => <div style={{ padding: ${OFF_SCALE_PX}, marginTop: '${ON_SCALE_PX}px' }} />`
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), [`padding: ${OFF_SCALE_PX}px`])
  })

  it('rejects rem style values instead of converting them at the 14px root', () => {
    const source = "export const X = () => <div style={{ padding: '0.5rem' }} />"
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.rootDependent), ['padding: 0.5rem'])
  })

  it('accepts token refs and reports unknown vars on style properties', () => {
    const source = "export const X = () => <div style={{ padding: 'var(--space-2)', gap: 'var(--nope)' }} />"
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.invalidVar), ['gap: var(--nope)'])
  })

  it('reports a dynamic spacing style value as unsupported', () => {
    const source = 'export const X = (offset: number) => <div style={{ padding: offset }} />'
    const findings = tsx(source)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.unsupported)
    assert.equal(findings[0].syntax, 'padding: {expr}')
  })
})

describe('fail-closed script parsing', () => {
  it('reports a TypeScript parse failure instead of empty success', () => {
    const findings = ts('export const x = {')
    assert.ok(findings.length >= 1)
    assert.equal(findings[0].ruleId, RULE.parseError)
    assert.ok(findings[0].syntax.length > 0)
    assert.match(findings[0].message, /failed to parse/i)
  })
})

describe('policy fallback', () => {
  it('still reports off-scale pixels when policy is omitted, using the constitutional scale', () => {
    const findings = scan({ path: 'src/fixture.css', source: '.box { padding: 13px; }' })
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['padding: 13px'])
  })

  it('does not let extra policy scale steps legalize an off-scale pixel', () => {
    const mutated = {
      tokenCssNames: policy.tokenCssNames,
      resolvedTokens: { ...policy.resolvedTokens, 'space.scale.99': '13px' },
    }
    const findings = run('src/fixture.css', '.box { padding: 13px; }', mutated)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['padding: 13px'])
  })
})

describe('local class and style aliases', () => {
  it('resolves a local const string className', () => {
    const source = "const spacingClass = 'p-[13px]'; export const X = () => <div className={spacingClass} />"
    assert.deepEqual(syntaxes(tsx(source), RULE.offScale), ['p-[13px]'])
  })

  it('resolves a local const style object', () => {
    const source = 'const spacingStyle = { padding: 13 }; export const X = () => <div style={spacingStyle} />'
    assert.deepEqual(syntaxes(tsx(source), RULE.offScale), ['padding: 13px'])
  })

  it('resolves local arrays and simple conditional class aliases', () => {
    const source = `
      const flagged = true
      const classes = ['flex', 'p-[13px]']
      const maybe = flagged ? 'gap-[13px]' : 'gap-[8px]'
      export const X = () => <div className={classes} />
      export const Y = () => <div className={maybe} />
    `
    assert.deepEqual(syntaxes(tsx(source), RULE.offScale).sort(), ['gap-[13px]', 'p-[13px]'])
  })

  it('does not blanket-reject a CSS-module class identifier', () => {
    const source = "import styles from './fixture.module.css'; export const X = () => <div className={styles.box} />"
    assert.deepEqual(tsx(source), [])
  })
})

describe('governed module and pure helper resolution', () => {
  const fixturePath = 'src/components/Fixture.tsx'

  it('resolves aliased static config imports from the supplied source snapshot', () => {
    const source = "import { spacing as layout } from '@/config/layout'; export const X=()=> <div className={layout.roomy}/>"
    const modules = {
      [fixturePath]: source,
      'src/config/layout.ts': "export const spacing={compact:'p-[8px]',roomy:'p-[13px]'}",
    }
    const findings = run(fixturePath, source, policy, modules)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('reports imported spacing at the consumer use site while parsing the origin AST', () => {
    const source = "import { cls } from './layout';\nexport const X = () => <div className={cls} />"
    const modules = {
      'src/components/fixture.tsx': source,
      'src/components/layout.ts': `${'// origin padding\n'.repeat(30)}export const cls = 'p-[13px]'`,
    }
    const findings = run('src/components/fixture.tsx', source, policy, modules)
    const finding = byRule(findings, RULE.offScale)[0]
    assert.equal(finding.syntax, 'p-[13px]')
    assert.deepEqual({ line: finding.line, column: finding.column }, { line: 2, column: 40 })
  })

  it('scans every static return branch of an imported pure helper without a helper-name allowlist', () => {
    const source = "import { choose as arbitraryName } from '../config/layout'; export const X=({dense}:{dense:boolean})=> <div className={arbitraryName(dense)}/>"
    const modules = {
      [fixturePath]: source,
      'src/config/layout.ts': "export function choose(dense:boolean){return dense?'p-[8px]':'p-[13px]'}",
    }
    const findings = run(fixturePath, source, policy, modules)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('resolves imported style config and pure style-helper return branches', () => {
    const source = "import { styles, makeStyle } from '@/config/layout'; export const X=({dense}:{dense:boolean})=> <><div style={styles.roomy}/><div style={makeStyle(dense)}/></>"
    const modules = {
      [fixturePath]: source,
      'src/config/layout.ts': "export const styles={roomy:{padding:13}}; export function makeStyle(dense:boolean){return dense?{gap:8}:{gap:13}}",
    }
    const findings = run(fixturePath, source, policy, modules)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['padding: 13px', 'gap: 13px'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('fails closed for a missing governed import and an impure helper', () => {
    const missing = "import { spacing } from '@/config/missing'; export const X=()=> <div className={spacing.compact}/>"
    assert.deepEqual(syntaxes(run(fixturePath, missing, policy, { [fixturePath]: missing }), RULE.unsupported), ['className: spacing.compact'])

    const impure = 'function choose(){log();return "p-[8px]"} export const X=()=> <div className={choose()}/>'
    assert.deepEqual(syntaxes(run(fixturePath, impure, policy, { [fixturePath]: impure }), RULE.unsupported), ['className: choose()'])

    const effectInReturn = 'function choose(){return log() ? "p-[8px]" : "p-[13px]"} export const X=()=> <div className={choose()}/>'
    assert.deepEqual(syntaxes(run(fixturePath, effectInReturn, policy, { [fixturePath]: effectInReturn }), RULE.unsupported), ['className: choose()'])

    const malformed = "import { spacing } from '@/config/broken'; export const X=()=> <div className={spacing}/>"
    const malformedModules = { [fixturePath]: malformed, 'src/config/broken.ts': 'export const spacing = {' }
    assert.deepEqual(syntaxes(run(fixturePath, malformed, policy, malformedModules), RULE.unsupported), ['className: spacing'])
  })

  it('treats whole boolean and string class guards as control flow', () => {
    const booleanGuard = "export const X=({enabled}:{enabled:boolean})=> <div className={enabled && 'p-[8px]'}/>"
    assert.deepEqual(run(fixturePath, booleanGuard), [])

    const stringGuard = "export const X=({prefix}:{prefix:string})=> <div className={prefix && 'p-[8px]'}/>"
    // A truthy prefix returns the safe RHS; an empty prefix contributes no class.
    assert.deepEqual(run(fixturePath, stringGuard), [])
  })

  it('resolves a statically imported boolean guard and rejects cyclic imports', () => {
    const guarded = "import { enabled } from '@/config/flags'; export const X=()=> <div className={enabled && 'p-[13px]'}/>"
    const guardedModules = { [fixturePath]: guarded, 'src/config/flags.ts': 'export const enabled=true' }
    const guardedFindings = run(fixturePath, guarded, policy, guardedModules)
    assert.deepEqual(syntaxes(guardedFindings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(guardedFindings, RULE.unsupported), [])

    const cyclic = "import { value } from '@/config/a'; export const X=()=> <div className={value}/>"
    const cyclicModules = {
      [fixturePath]: cyclic,
      'src/config/a.ts': "import { value as next } from './b'; export const value=next",
      'src/config/b.ts': "import { value as next } from './a'; export const value=next",
    }
    assert.deepEqual(syntaxes(run(fixturePath, cyclic, policy, cyclicModules), RULE.unsupported), ['className: value'])
  })
})

describe('lexical alias resolution', () => {
  it('keeps same-named constants isolated between function scopes', () => {
    const source = `
      function A() { const cls = 'p-[13px]'; return <div className={cls} /> }
      function B() { const cls = 'p-[8px]'; return <div className={cls} /> }
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.equal(byRule(findings, RULE.unsupported).length, 0)
  })

  it('does not inherit an outer constant through a shadowing parameter', () => {
    const source = `
      const cls = 'p-[13px]'
      function Box(cls: string) { return <div className={cls} /> }
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), [])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['className: cls'])
  })

  it('reports cyclic aliases as unsupported instead of returning clean', () => {
    const source = `
      const first = second
      const second = first
      export const Box = () => <div className={first} />
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['className: first'])
  })
})

describe('explicit spacing extension boundaries', () => {
  it('classifies a destructured className prop forwarded unchanged to JSX', () => {
    const source = `
      type Props = { className?: string }
      export function Button({ className }: Props) { return <button className={className} /> }
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.extensionBoundary), ['Button#className'])
    assert.equal(byRule(findings, RULE.unsupported).length, 0)
  })

  it('classifies direct className forwarding through a class builder', () => {
    const source = `
      export const Panel = ({ className }: { className?: string }) => <div className={cn('p-[8px]', className)} />
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.extensionBoundary), ['Panel#className'])
    assert.equal(byRule(findings, RULE.offScale).length, 0)
  })

  it('uses the receiving variable for className forwarded through forwardRef', () => {
    const source = `
      const Card = React.forwardRef<HTMLDivElement, Props>(({ className }, ref) => <div ref={ref} className={cn('p-[8px]', className)} />)
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.extensionBoundary), ['Card#className'])
    assert.equal(byRule(findings, RULE.unsupported).length, 0)
  })

  it('still scans a static default while classifying the forwarded prop boundary', () => {
    const source = `
      export function Card({ className = 'p-[13px]' }: { className?: string }) { return <div className={className} /> }
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.extensionBoundary), ['Card#className'])
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
  })

  it('keeps a reassigned className parameter unsupported', () => {
    const source = `
      export function Mutating(className: string) { className = normalize(className); return <div className={className} /> }
    `
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.extensionBoundary), [])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['className: className'])
  })

  it('requires a closed-scale fallback for safe-area environment spacing', () => {
    const valid = css('.x { padding-bottom: env(safe-area-inset-bottom, 0px) }')
    assert.deepEqual(syntaxes(valid, RULE.extensionBoundary), ['padding-bottom: env(safe-area-inset-bottom, 0px)'])
    assert.equal(byRule(valid, RULE.unsupported).length, 0)

    const missing = css('.x { padding-bottom: env(safe-area-inset-bottom) }')
    assert.deepEqual(syntaxes(missing, RULE.missingSafeAreaFallback), ['padding-bottom: env(safe-area-inset-bottom)'])

    const invalid = css('.x { padding-bottom: env(safe-area-inset-bottom, 13px) }')
    assert.deepEqual(syntaxes(invalid, RULE.offScale), ['padding-bottom: env(safe-area-inset-bottom, 13px)'])

    for (const value of ['env(unknown-inset)', 'env()', 'env(safe-area-inset-top, 0px, 4px)', 'calc(env(safe-area-inset-top) + 4px)']) {
      const findings = css(`.x { padding-bottom: ${value} }`)
      assert.equal(syntaxes(findings, RULE.missingSafeAreaFallback).length, 0)
      assert.ok(syntaxes(findings, RULE.unsupported).length > 0)
    }
    assert.equal(byRule(invalid, RULE.extensionBoundary).length, 0)
  })
})

describe('Tailwind v4 registered-token shorthand', () => {
  it('accepts parenthesized shorthand for registered D10 tokens, including responsive variants', () => {
    const source = 'export const X = () => <div className="p-(--space-2) gap-(--space-control-gap) md:gap-(--space-control-gap)" />'
    assert.deepEqual(tsx(source), [])
  })

  it('reports an unknown token in parenthesized shorthand as invalid-var', () => {
    const source = 'export const X = () => <div className="p-(--not-a-token)" />'
    assert.deepEqual(syntaxes(tsx(source), RULE.invalidVar), ['p-(--not-a-token)'])
  })

  it('does not treat a registered non-spacing token shorthand as spacing', () => {
    const source = 'export const X = () => <div className="p-(--color-accent)" />'
    const findings = tsx(source)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.unsupported)
    assert.equal(findings[0].syntax, 'p-(--color-accent)')
  })
})

describe('class array joins and unresolved indexed reads', () => {
  it('checks every branch of a literal class array joined by whitespace', () => {
    const source = `export const X = () => <div className={['p-[8px]', active ? 'gap-[13px]' : 'm-[4px]'].join(' ')} />`
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['gap-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('accepts a literal closed-scale class array with a whitespace separator', () => {
    assert.deepEqual(tsx(`export const X = () => <div className={['p-[8px]', 'gap-[16px]'].join(' ')} />`), [])
  })

  it('rejects token-synthesizing, dynamic, or omitted join separators', () => {
    for (const argument of ["''", 'separator', '']) {
      const findings = tsx(`export const X = () => <div className={['p-', '[13px]'].join(${argument})} />`)
      assert.equal(byRule(findings, RULE.unsupported).length, 1)
    }
  })

  it('does not silently accept a runtime-indexed class map', () => {
    const findings = tsx(`const styles = { bad: 'p-[13px]', good: 'p-[8px]' }; export const X = () => <div className={styles[state]} />`)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['className: styles[state]'])
  })

  it('resolves an in-bounds numeric literal index on a proven class array', () => {
    const findings = tsx(`const styles = ['p-[8px]', 'p-[13px]'] as const; export const X = () => <div className={styles[1]} />`)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('keeps out-of-bounds and reassigned array aliases unsupported', () => {
    const outOfBounds = tsx(`const styles = ['p-[8px]']; export const X = () => <div className={styles[2]} />`)
    const reassigned = tsx(`let styles = ['p-[8px]']; styles = ['p-[13px]']; export const X = () => <div className={styles[0]} />`)
    assert.deepEqual(syntaxes(outOfBounds, RULE.unsupported), ['className: styles[2]'])
    assert.deepEqual(syntaxes(reassigned, RULE.unsupported), ['className: styles[0]'])
  })

  it('does not hide a dynamic spread inside a joined class array', () => {
    const findings = tsx(`export const X = () => <div className={['p-[8px]', ...external].join(' ')} />`)
    assert.equal(byRule(findings, RULE.unsupported).length, 1)
  })
})

describe('unclassified class expressions remain blocking', () => {
  it('ignores proven boolean and undefined class-builder operands', () => {
    assert.deepEqual(tsx(`export const X = ({dirty}:{dirty:boolean}) => <div className={cn(undefined, !dirty, dirty, dirty === false, 'p-[8px]')} />`), [])
  })

  it('classifies unchanged style forwarding as an extension boundary and scans its default', () => {
    const source = `export const Box = ({ style = { padding: '13px' } }) => <div style={style} />`
    const findings = tsx(source)
    assert.deepEqual(syntaxes(findings, RULE.extensionBoundary), ['Box#style'])
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['padding: 13px'])
  })

  it('reports a dynamic spread passed into a class builder', () => {
    const findings = tsx(`export const X = () => <div className={cn('p-[8px]', ...external)} />`)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['className: ...external'])
  })

  it('reports an expression outside the supported class grammar', () => {
    const findings = tsx(`export const X = () => <div className={amount * step} />`)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['className: amount * step'])
  })

  it('keeps inert class-builder literals harmless', () => {
    assert.deepEqual(tsx(`export const X = () => <div className={cn(false, null, 0, 'p-[8px]')} />`), [])
  })
})

describe('authenticated class-builder call aliases', () => {
  // Real-source group resolved-nested-class-builder: a className sink (or
  // builder argument) that is an identifier resolving to a call of an
  // AUTHENTICATED class builder (the verified src/lib/utils.ts cn wrapper, an
  // import alias of it, or clsx/classnames/tailwind-merge). The aliased call's
  // arguments are governed at their definition site — visitNode scans every
  // builder call in the defining file — so the consuming site must neither
  // report unsupported nor re-report the same literals twice.
  //
  // The wrapper source is the real src/lib/utils.ts, not a fixture replica:
  // guardClassBuilder authenticates the exact clsx + extendTailwindMerge
  // shape, and a replica could silently drift from the implementation the
  // application actually runs.
  const fixturePath = 'src/components/Fixture.tsx'
  const utilsSource = readFileSync(resolve(ROOT, 'src/lib/utils.ts'), 'utf8')

  function runWith(source, extraModules = {}) {
    const modules = { 'src/lib/utils.ts': utilsSource, [fixturePath]: source, ...extraModules }
    return run(fixturePath, source, policy, modules)
  }

  it('accepts a function-scope const bound to an authenticated cn call (CalendarToolbar shape)', () => {
    const source = `
      import { cn } from '@/lib/utils'
      export const X = () => {
        const touchTarget = 'pointer-coarse:min-h-[44px]'
        const navBtnClass = cn('flex items-center justify-center shrink-0', 'h-8 w-8 rounded-md', touchTarget)
        return <button className={navBtnClass} />
      }
    `
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
    assert.deepEqual(findings, [])
  })

  it('reports aliased-call debt exactly once at the definition site (date-picker shape)', () => {
    const source = `
      import { cn } from '@/lib/utils'
      export const TRIGGER = cn('flex w-full items-center gap-2', 'px-3 py-1')
      export const X = ({ className }: { className?: string }) => <button className={cn(TRIGGER, className)} />
    `
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
    assert.deepEqual(syntaxes(findings, RULE.extensionBoundary), ['X#className'])
    assert.deepEqual(syntaxes(findings, RULE.rootDependent), ['gap-2', 'px-3', 'py-1'])
  })

  it('does not double-report an aliased off-scale literal between definition and sink', () => {
    const source = `
      import { cn } from '@/lib/utils'
      export const ROOMY = cn('flex', 'p-[13px]')
      export const X = () => <div className={ROOMY} />
    `
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('authenticates a cross-module alias and keeps its debt at the defining path', () => {
    const source = "import { ROOMY } from './room'; export const X = () => <div className={ROOMY} />"
    const roomSource = "import { cn } from '@/lib/utils'; export const ROOMY = cn('flex', 'p-[13px]')"
    const modules = { 'src/lib/utils.ts': utilsSource, [fixturePath]: source, 'src/components/room.tsx': roomSource }
    const consumer = run(fixturePath, source, policy, modules)
    assert.deepEqual(consumer, [])
    const definition = run('src/components/room.tsx', roomSource, policy, modules)
    assert.deepEqual(syntaxes(definition, RULE.offScale), ['p-[13px]'])
  })

  it('authenticates cn imported under an alias name', () => {
    const source = `
      import { cn as cx } from '@/lib/utils'
      export const X = () => {
        const btn = cx('flex', 'p-[13px]')
        return <button className={btn} />
      }
    `
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('authenticates a clsx package call bound through a const', () => {
    const source = `
      import clsx from 'clsx'
      export const X = () => {
        const btn = clsx('flex', 'p-[13px]')
        return <button className={btn} />
      }
    `
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })
})

describe('renamed builder imports stay governed', () => {
  // Independent-review HIGH bypass (frozen 1577aee7): an authenticated builder
  // imported under a name outside CLASS_BUILDERS (e.g. clsx as formatClasses)
  // cleared the alias sink while visitNode never dispatched the definition by
  // local name — the off-scale argument was reported nowhere. The definition
  // dispatch must authenticate renamed builders so their arguments are scanned
  // exactly once at the definition site, for alias and direct uses alike.
  const fixturePath = 'src/components/Fixture.tsx'
  const utilsSource = readFileSync(resolve(ROOT, 'src/lib/utils.ts'), 'utf8')

  function runWith(source, extraModules = {}) {
    const modules = { 'src/lib/utils.ts': utilsSource, [fixturePath]: source, ...extraModules }
    return run(fixturePath, source, policy, modules)
  }

  it('reports the aliased renamed-clsx repro exactly once (lead repro)', () => {
    const source = "import { clsx as formatClasses } from 'clsx'; const styles = formatClasses('p-[7px]'); export const view = () => <div className={styles}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('reports a renamed cn wrapper import exactly once at the definition', () => {
    const source = "import { cn as merge } from '@/lib/utils'; const styles = merge('p-[7px]'); export const view = () => <div className={styles}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('reports a renamed default classnames import exactly once', () => {
    const source = "import combo from 'classnames'; const styles = combo('p-[7px]'); export const view = () => <div className={styles}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('scans a direct renamed-builder call at the sink without unsupported noise', () => {
    const source = "import { clsx as formatClasses } from 'clsx'; export const view = () => <div className={formatClasses('p-[7px]')}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('governs a renamed-builder definition even without a consuming sink', () => {
    const source = "import { clsx as formatClasses } from 'clsx'; const unused = formatClasses('p-[13px]')"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('governs a cross-module renamed-builder alias at the defining path', () => {
    const source = "import { R } from './room'; export const view = () => <div className={R}/>"
    const roomSource = "import { clsx as fmt } from 'clsx'; export const R = fmt('p-[13px]')"
    const modules = { 'src/lib/utils.ts': utilsSource, [fixturePath]: source, 'src/components/room.tsx': roomSource }
    const consumer = run(fixturePath, source, policy, modules)
    assert.deepEqual(consumer, [])
    const definition = run('src/components/room.tsx', roomSource, policy, modules)
    assert.deepEqual(syntaxes(definition, RULE.offScale), ['p-[13px]'])
  })

  it('does not double-report a renamed-builder literal between definition and sink', () => {
    const source = "import { clsx as formatClasses } from 'clsx'; const styles = formatClasses('flex', 'gap-2'); export const view = () => <div className={styles}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.rootDependent), ['gap-2'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })
})

describe('independent-review follow-up repairs', () => {
  // F1 (MEDIUM): visitClassBuilderArg early-returned on the identifier
  // `undefined` with no shadow proof, so `var undefined = 'p-[7px]'` hid an
  // arbitrary literal behind a false-clean scan. F3 (LOW): authenticated
  // renamed builders in conditional/nullish positions gained one unsupported
  // marker per branch even when every argument was statically governed.
  // F4 (LOW): the (ruleId, syntax, line) scanner dedupe merged two distinct
  // same-line occurrences into one finding, under-counting fingerprints whose
  // occurrence counts are explicit Stage B requirements.
  const fixturePath = 'src/components/Fixture.tsx'
  const utilsSource = readFileSync(resolve(ROOT, 'src/lib/utils.ts'), 'utf8')

  function runWith(source, extraModules = {}) {
    const modules = { 'src/lib/utils.ts': utilsSource, [fixturePath]: source, ...extraModules }
    return run(fixturePath, source, policy, modules)
  }

  it('analyzes a shadowed undefined className binding instead of silently passing (F1)', () => {
    const source = "var undefined = 'p-[7px]'; export const X = () => <div className={undefined}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('analyzes a function-scoped shadowed undefined inside a class builder (F1)', () => {
    const source = "export const X = () => { var undefined = 'p-[13px]'; return <div className={cn(undefined)}/> }"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[13px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('keeps unshadowed undefined class operands clean (F1 baseline)', () => {
    const source = "export const X = () => <div className={cn(undefined, 'p-[8px]')}/>"
    assert.deepEqual(runWith(source), [])
  })

  it('drops the marker for an authenticated renamed call in conditional branches when all arguments are governed (F3)', () => {
    const source = "import { clsx as fmt } from 'clsx'; export const X = ({t}:{t:boolean}) => <div className={t ? fmt('p-[7px]') : fmt('p-[8px]')}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('drops the marker for an authenticated renamed call in a nullish position with a resolvable left operand (F3)', () => {
    const source = "import { clsx as fmt } from 'clsx'; const fallback = 'flex'; export const X = () => <div className={fallback ?? fmt('p-[7px]')}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('extends the conditional-position clearance to the authenticated cn wrapper (F3)', () => {
    const source = "import { cn } from '@/lib/utils'; export const X = ({t}:{t:boolean}) => <div className={t ? cn('p-[7px]') : cn('p-[8px]')}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('counts two same-line occurrences of one class as two distinct findings (F4)', () => {
    const source = "import { clsx as fmt } from 'clsx'; const a = fmt('p-[7px]'); const b = fmt('p-[7px]')"
    const findings = runWith(source)
    const occurrences = byRule(findings, RULE.offScale)
    assert.equal(occurrences.length, 2)
    assert.ok(occurrences.every((finding) => finding.syntax === 'p-[7px]'), 'both occurrences carry the same fingerprint syntax')
    assert.notEqual(occurrences[0].column, occurrences[1].column, 'distinct source occurrences are distinguished by position')
  })

  it('still reports one source occurrence exactly once through an alias chain (F4 control)', () => {
    const source = "const s = 'p-[7px]'; const a2 = s; const b2 = a2; export const X = () => <div className={b2}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'])
  })
})

describe('helper-array occurrence undercount repair (Stage B blocker)', () => {
  // A pure local function returning an array assigns ONE forced (point-of-use)
  // source location to every element it visits, so two textually identical
  // off-scale elements collapsed onto the (ruleId, syntax, line, column)
  // dedupe key in pushFinding and were undercounted to one finding. Stage B
  // rejects an increased occurrence count per fingerprint at audit time, so a
  // scanner that undercounts on the way in can never regain the true count
  // later — a genuinely new second occurrence of an already-seen fingerprint
  // would silently vanish into the same undercounted total. originKey (see
  // withOrigin/nodeKey in spacing.mjs) fixes this by keying the in-run dedupe
  // on the actual terminal AST node reached, not the display location.
  const fixturePath = 'src/components/Fixture.tsx'

  function runWith(source) {
    return run(fixturePath, source, policy, { [fixturePath]: source })
  }

  it('counts two distinct elements of a helper-returned array as two findings, not one (repro)', () => {
    const source = "function pick(){return ['p-[7px]','p-[7px]']} export const X = () => <div className={pick()}/>"
    const findings = runWith(source)
    const occurrences = byRule(findings, RULE.offScale)
    assert.equal(occurrences.length, 2, `two distinct array literals must not dedupe to one, got ${JSON.stringify(occurrences)}`)
    assert.ok(occurrences.every((finding) => finding.syntax === 'p-[7px]'))
    // The bug is specifically that both elements display at the SAME forced
    // call-site location — proving the fix cannot rely on (line, column).
    assert.equal(occurrences[0].line, occurrences[1].line)
    assert.equal(occurrences[0].column, occurrences[1].column)
  })

  it('counts two distinct elements inside a nested array return as two findings', () => {
    const source = "function pick(){return [['p-[7px]'],['p-[7px]']]} export const X = () => <div className={pick()}/>"
    const findings = runWith(source)
    const occurrences = byRule(findings, RULE.offScale)
    assert.equal(occurrences.length, 2, `nested array elements must not dedupe to one, got ${JSON.stringify(occurrences)}`)
    assert.equal(occurrences[0].column, occurrences[1].column, 'both still display at the shared call site')
  })

  it('counts two same-line, same-display-location conditional branches with an identical value as two findings', () => {
    const source = "function pick(cond){return cond ? 'p-[7px]' : 'p-[7px]'} export const X = ({t}:{t:boolean}) => <div className={pick(t)}/>"
    const findings = runWith(source)
    const occurrences = byRule(findings, RULE.offScale)
    assert.equal(occurrences.length, 2, `distinct whenTrue/whenFalse literal nodes must not dedupe to one, got ${JSON.stringify(occurrences)}`)
    assert.equal(occurrences[0].column, occurrences[1].column)
  })

  it('reports one source occurrence exactly once when a single-value pure helper is called from two sites (repeat consumption)', () => {
    const source = "function pick(){return 'p-[7px]'} export const X = () => <><div className={pick()}/><div className={pick()}/></>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'], 'both calls trace back to the one literal in pick(); this is repeat consumption, not two occurrences')
  })

  it('keeps an impure array-returning helper unsupported (fail-closed), never exploding into off-scale findings', () => {
    const source = "function pick(){log(); return ['p-[7px]','p-[7px]']} export const X = () => <div className={pick()}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), [], 'an impure helper must never be trusted as an off-scale findings source')
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['className: pick()'])
  })

  it('counts two call sites of an unknown/impure helper as two unsupported findings, not one (fail-closed occurrence tracking)', () => {
    const source = "function pick(){log(); return 'p-[7px]'} export const X = () => <><div className={pick()}/><div className={pick()}/></>"
    const findings = runWith(source)
    const occurrences = byRule(findings, RULE.unsupported)
    assert.equal(occurrences.length, 2, `two distinct impure call sites must not dedupe to one, got ${JSON.stringify(occurrences)}`)
    assert.notEqual(occurrences[0].column, occurrences[1].column, 'each call site is its own node with its own natural position')
  })

  it('regression: a renamed clsx import consumed through a const alias still reports its off-scale literal (prior HIGH bypass)', () => {
    // Prior frozen review confirmed this fixed; kept as a permanent guard so a
    // future change to alias/authentication handling cannot silently regress
    // the HIGH bypass fixed at 1577aee7 (see spacing.mjs visitNode comment).
    const source = "import { clsx as formatClasses } from 'clsx'; const s = formatClasses('p-[7px]'); const styles = s; export const X = () => <button className={styles}/>"
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale), ['p-[7px]'], 'renamed-builder value consumed through a const alias must not scan clean')
  })
})

describe('finite-dispatcher member resolution (closes statusConfig.textClass-shaped unsupported debt)', () => {
  // The Stage B worklist's largest single unsupported group (11 occurrences)
  // was `className: statusConfig.textClass`-shaped member reads on the
  // result of a status→render-config switch helper (getToolBadgeStatusConfig
  // / getSpanStatusDot in src/lib/toolStatusConfig.tsx and local equivalents
  // in GoalIndicator.tsx/GoalPillTray.tsx). resolveExpr never dereferences a
  // CallExpression, so these were unconditionally unsupported regardless of
  // whether the helper could ever actually produce an off-scale value.
  // resolveDispatcherMember (spacing.mjs) proves two outcomes: ABSENCE (the
  // property is omitted from every branch — contributes nothing, same as an
  // unshadowed `undefined`) and PRESENCE (every branch either omits it or
  // sets it to a plain value, each visited like an array literal's
  // elements). Any branch this cannot classify — spread, computed key,
  // non-switch control flow, a non-const or reassigned/mutated binding, a
  // call cycle — aborts the whole proof and falls through to the ordinary
  // unsupported path.
  //
  // LEAD DECISION (frozen review of bb1fd56b2 — see
  // dist/design-system-baseline/cli-lanes/claude-spacing-review/review.md —
  // found two BLOCKING false greens in the original version of this
  // capability): the dispatcher-result binding must be `const`, declared
  // inside a function, and unmutated/unreassigned/unescaped anywhere in
  // that enclosing function (see the "finite-dispatcher binding safety"
  // describe block below for the direct regression tests); and an ABSENT
  // classification additionally requires the object literal to carry an
  // explicit `__proto__: null`, or the read stays unsupported (an ordinary
  // literal can inherit the property from prototype mutations elsewhere).
  // The tests below were rewritten to declare the dispatcher-result binding
  // inside the consuming component (not at module scope, which this fix no
  // longer trusts — see directForwardedParameter's owner search) and to
  // carry `__proto__: null` on every absent-classified branch, so they keep
  // demonstrating the capability actually resolving real code, not just
  // becoming blanket-conservative.
  const fixturePath = 'src/components/Fixture.tsx'

  function runWith(source) {
    return run(fixturePath, source, policy, { [fixturePath]: source })
  }

  it('proves absence when every switch branch (including an exhaustiveness-guard default block) omits the property', () => {
    const source = `
      function describeStatus(status) {
        switch (status) {
          case 'a': return { __proto__: null, label: 'A' }
          case 'b': return { __proto__: null, label: 'B' }
          default: { const x = 0; void x; return { __proto__: null, label: 'C' } }
        }
      }
      export const X = ({status}) => {
        const config = describeStatus(status)
        return <div className={cn('shrink-0', config.textClass)}/>
      }
    `
    assert.deepEqual(runWith(source), [], 'a provably always-undefined member must not block or produce a false finding')
  })

  it('resolves every present branch value when the property is set (not omitted) in every branch', () => {
    const source = `
      function describeStatus(status) {
        switch (status) {
          case 'a': return { label: 'A', textClass: 'p-[7px]' }
          case 'b': return { label: 'B', textClass: 'p-[9px]' }
          default: return { label: 'C', textClass: 'p-[8px]' }
        }
      }
      export const X = ({status}) => {
        const config = describeStatus(status)
        return <div className={cn('shrink-0', config.textClass)}/>
      }
    `
    const findings = runWith(source)
    assert.deepEqual(syntaxes(findings, RULE.offScale).sort(), ['p-[7px]', 'p-[9px]'], 'on-scale p-[8px] must not be reported; both off-scale branches must be')
    assert.deepEqual(syntaxes(findings, RULE.unsupported), [])
  })

  it('follows a ternary chain of dispatcher calls to prove absence', () => {
    const source = `
      function describeStatus(status) {
        switch (status) { case 'a': return { __proto__: null, label: 'A' }; default: return { __proto__: null, label: 'B' } }
      }
      export const X = ({status, isRunning, isDone}) => {
        const config = isRunning ? describeStatus('a') : isDone ? describeStatus('b') : { __proto__: null, label: 'z' }
        return <div className={cn('shrink-0', config.textClass)}/>
      }
    `
    assert.deepEqual(runWith(source), [])
  })

  it('follows one dispatcher delegating to another (nested finite dispatch)', () => {
    const source = `
      function inner(s) {
        switch (s) { case 'x': return { __proto__: null, label: 'X' }; default: return { __proto__: null, label: 'Y' } }
      }
      function outer(status) {
        switch (status) { case 'a': return inner('x'); default: return inner('y') }
      }
      export const X = ({status}) => {
        const config = outer(status)
        return <div className={cn('shrink-0', config.textClass)}/>
      }
    `
    assert.deepEqual(runWith(source), [])
  })

  it('keeps the real getToolBadgeStatusConfig member read unsupported without a null-prototype literal (frozen review fix; LEAD DECISION)', () => {
    // Prior version of this test asserted the read resolved clean (pure
    // absence proof) because no switch branch of getToolBadgeStatusConfig
    // ever sets textClass. The frozen independent review found that
    // trusting an ordinary (non-null-prototype) object literal's *absence*
    // of a property assumes a pure module graph — a prototype mutation
    // anywhere else in the real app could inject the property onto that
    // literal. The scanner cannot prove the real
    // src/lib/toolStatusConfig.tsx literals carry `__proto__: null` (they
    // do not, and adding it there is out of scope for this fix), so per the
    // LEAD DECISION this read correctly returns to spacing/unsupported.
    // This is the intended, correct outcome, not a regression — see the
    // review's "Combined impact" section (dist/design-system-baseline/
    // cli-lanes/claude-spacing-review/review.md) for why the prior green
    // here was unsound.
    const toolStatusConfigSource = readFileSync(resolve(ROOT, 'src/lib/toolStatusConfig.tsx'), 'utf8')
    const source = "import { getToolBadgeStatusConfig } from '@/lib/toolStatusConfig'; export const X = ({status}) => { const statusConfig = getToolBadgeStatusConfig(status); return <span className={cn('text-[var(--color-muted)] shrink-0', statusConfig.textClass)}/> }"
    const findings = run(fixturePath, source, policy, { [fixturePath]: source, 'src/lib/toolStatusConfig.tsx': toolStatusConfigSource })
    assert.deepEqual(syntaxes(findings, RULE.unsupported), ['className: statusConfig.textClass'],
      'without a null-prototype literal, absence cannot be proven; the read must stay unsupported')
  })

  it('keeps a spread branch unsupported (cannot rule out an injected property)', () => {
    const source = `
      function describeStatus(status) {
        const base = { label: 'base' }
        switch (status) { case 'a': return { ...base }; default: return { label: 'C' } }
      }
      const config = describeStatus(status)
      export const X = () => <div className={cn('shrink-0', config.textClass)}/>
    `
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: config.textClass'])
  })

  it('keeps a computed property key unsupported (cannot rule out a match)', () => {
    const source = `
      function describeStatus(status) {
        const key = 'textClass'
        switch (status) { case 'a': return { [key]: 'p-[7px]' }; default: return { label: 'C' } }
      }
      const config = describeStatus(status)
      export const X = () => <div className={cn('shrink-0', config.textClass)}/>
    `
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: config.textClass'])
  })

  it('keeps a reassigned dispatcher-result binding unsupported', () => {
    const source = `
      function describeStatus(status) {
        switch (status) { case 'a': return { label: 'A' }; default: return { label: 'B' } }
      }
      let config = describeStatus(status)
      config = other
      export const X = () => <div className={cn('shrink-0', config.textClass)}/>
    `
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: config.textClass'])
  })

  it('keeps a non-switch dispatcher (if/else control flow) unsupported', () => {
    const source = `
      function describeStatus(status) {
        if (status === 'a') return { label: 'A' }
        return { label: 'B' }
      }
      const config = describeStatus(status)
      export const X = () => <div className={cn('shrink-0', config.textClass)}/>
    `
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: config.textClass'])
  })

  it('keeps a cyclic pair of dispatcher functions unsupported (cycle guard aborts, never infinite-loops)', () => {
    const source = `
      function a(s) { switch (s) { case 'x': return b('y'); default: return { label: 'A' } } }
      function b(s) { switch (s) { case 'y': return a('x'); default: return { label: 'B' } } }
      const config = a('x')
      export const X = () => <div className={cn('shrink-0', config.textClass)}/>
    `
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: config.textClass'])
  })
})

describe('finite-dispatcher binding safety (frozen review fix: mutation/reassignment/escape must never be invisible)', () => {
  // Independent review of bb1fd56b2 found two BLOCKING false greens: a
  // property written onto (or otherwise escaping through) the
  // dispatcher-result binding after the call reached a className with ZERO
  // findings — not even the pre-existing conservative `spacing/unsupported`
  // — on code shapes the review called "ordinary... not exotic". Root
  // cause: resolveDispatcherMember only inspected the call's return-value
  // SHAPE, never whether the caller subsequently wrote to the binding; and
  // its only reassignment guard (isReassignedWithin) was invoked with the
  // whole SourceFile as `owner`, whose traversal stops descending the
  // instant it meets the first function-like node — a near no-op for any
  // component body, since virtually all real consumer code here is a
  // function component or hook. Every case below is reproduced directly
  // from the frozen review (dist/design-system-baseline/cli-lanes/
  // claude-spacing-review/review.md) and the lead's spacing-repro.mjs
  // three-case script (dist/design-system-baseline/cli-lanes/claude-lead/).
  const fixturePath = 'src/components/Fixture.tsx'
  const getConfigHelper = "function getConfig(s) { switch (s) { case 'a': return { label: 'A' }; default: return { label: 'B' } } }\n"

  function runWith(source) {
    return run(fixturePath, source, policy, { [fixturePath]: source })
  }

  it('keeps a property write after the dispatcher call unsupported (review finding 1 — lead repro case 1)', () => {
    const source = `${getConfigHelper}export function V({status}) { const cfg = getConfig(status); cfg.textClass = 'p-[7px]'; return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'],
      'a property written onto the binding after the call must keep the read blocking, never silently pass')
  })

  it('keeps a `let` binding reassigned inside the enclosing function unsupported (review finding 2 — lead repro case 2)', () => {
    const source = `${getConfigHelper}export function V({status}) { let cfg = getConfig(status); cfg = { textClass: 'p-[7px]' }; return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'],
      'reassignment inside the component must be visible, not only at module scope')
  })

  it('keeps an absent member unsupported when its object literal has no null-prototype guard (lead repro case 3)', () => {
    const source = `${getConfigHelper}export function V({status}) { const cfg = getConfig(status); return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'],
      'absence without an explicit __proto__: null assumes a pure module graph, which the contract forbids')
  })

  it('keeps an Object.assign write onto the dispatcher-result binding unsupported', () => {
    const source = `${getConfigHelper}export function V({status}) { const cfg = getConfig(status); Object.assign(cfg, { textClass: 'p-[7px]' }); return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('keeps a delete on the dispatcher-result binding unsupported', () => {
    const source = `${getConfigHelper}export function V({status}) { const cfg = getConfig(status); delete cfg.label; return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('keeps a write through an alias of the dispatcher-result binding unsupported', () => {
    const source = `${getConfigHelper}export function V({status}) { const cfg = getConfig(status); const x = cfg; x.textClass = 'p-[7px]'; return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'],
      'merely aliasing the binding (const x = cfg) is itself an escape this proof cannot see past')
  })

  it('keeps a write performed inside a nested callback unsupported (does not stop descending at the first closure)', () => {
    const source = `${getConfigHelper}export function V({status}) { const cfg = getConfig(status); [1].forEach(() => { cfg.textClass = 'p-[7px]' }); return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'],
      'a mutation inside a nested closure is exactly as real as one at the top of the function')
  })

  it('keeps a property write inside a nested block unsupported', () => {
    const source = `${getConfigHelper}export function V({status}) { const cfg = getConfig(status); if (status === 'a') { cfg.textClass = 'p-[7px]' } return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('keeps a reassignment inside a nested block unsupported', () => {
    const source = `${getConfigHelper}export function V({status}) { let cfg = getConfig(status); if (status === 'a') { cfg = { textClass: 'p-[7px]' } } return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('resolves a null-prototype absence proof scoped to the enclosing function (positive control — must still resolve)', () => {
    const source = `
      function getConfig(s) { switch (s) { case 'a': return { __proto__: null, label: 'A' }; default: return { __proto__: null, label: 'B' } } }
      export function V({status}) { const cfg = getConfig(status); return <div className={cfg.textClass}/> }
    `
    assert.deepEqual(runWith(source), [], 'a genuinely null-prototype, unmutated, const binding must still resolve — the fix must not become blanket-conservative')
  })

  it('resolves a null-prototype presence proof scoped to the enclosing function (positive control — off-scale value still reported)', () => {
    const source = `
      function getConfig(s) { switch (s) { case 'a': return { __proto__: null, textClass: 'p-[7px]' }; default: return { __proto__: null, label: 'B' } } }
      export function V({status}) { const cfg = getConfig(status); return <div className={cfg.textClass}/> }
    `
    assert.deepEqual(syntaxes(runWith(source), RULE.offScale), ['p-[7px]'])
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), [])
  })

  it('control: a direct off-scale literal still reports (sanity check the harness itself works)', () => {
    const source = "export function V() { return <div className='p-[7px]'/> }"
    assert.deepEqual(syntaxes(runWith(source), RULE.offScale), ['p-[7px]'])
  })

  it('rejects a generator dispatcher function (calling it returns a Generator, not the object)', () => {
    const source = "function* getConfig(s) { switch (s) { case 'a': return { label: 'A' }; default: return { label: 'B' } } }\nexport function V({status}) { const cfg = getConfig(status); return <div className={cfg.textClass}/> }"
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('rejects an async dispatcher function (calling it returns a Promise, not the object)', () => {
    const source = "async function getConfig(s) { switch (s) { case 'a': return { label: 'A' }; default: return { label: 'B' } } }\nexport function V({status}) { const cfg = getConfig(status); return <div className={cfg.textClass}/> }"
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('rejects a dispatcher function nested inside the consuming component itself (not top-level)', () => {
    const source = "export function V({status}) { function getConfig(s) { switch (s) { case 'a': return { label: 'A' }; default: return { label: 'B' } } } const cfg = getConfig(status); return <div className={cfg.textClass}/> }"
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })
})

describe('finite-dispatcher binding safety — mutation-isolated variants (each guard is the ONLY thing standing between resolve and unsupported)', () => {
  // Self-verification gate (test-plan-and-write / test-driven-development):
  // every fixture above that models a mutation/reassignment/escape or an
  // unsafe dispatcher function uses a helper whose branches never set
  // `textClass` AND never carry `__proto__: null` — so classifyDispatcherProperty
  // classifies every branch 'absent', and hasNullPrototypeLiteral independently
  // fails regardless of whether the mutation-safety or top-level-function guard
  // is even evaluated. A mutation round that disables ONLY
  // dispatcherBindingUsesSafe, isTopLevelFiniteDispatcherFunction, or (for a
  // never-mutated `let`) isConstVariableDeclaration therefore does not
  // necessarily kill those fixtures — hasNullPrototypeLiteral can mask the
  // disabled guard and still report unsupported for the right output, wrong
  // reason. This block repeats the same shapes with a `__proto__: null`
  // dispatcher (or, for the const-only case, a binding that is never
  // mutated at all) so each fixture would otherwise resolve cleanly, making
  // the specific guard under test the sole reason it stays unsupported —
  // confirmed by the mutation proof in this fix's evidence directory
  // (dist/design-system-baseline/cli-lanes/claude-spacing-fix/mutants/),
  // where disabling any one of these guards on an isolated copy flips the
  // corresponding test here (and only here) to a false green.
  const fixturePath = 'src/components/Fixture.tsx'
  const nullProtoHelper = "function getConfig(s) { switch (s) { case 'a': return { __proto__: null, label: 'A' }; default: return { __proto__: null, label: 'B' } } }\n"

  function runWith(source) {
    return run(fixturePath, source, policy, { [fixturePath]: source })
  }

  it('isolates dispatcherBindingUsesSafe: a property write after the call on an otherwise-resolvable null-prototype dispatcher', () => {
    const source = `${nullProtoHelper}export function V({status}) { const cfg = getConfig(status); cfg.textClass = 'p-[7px]'; return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates dispatcherBindingUsesSafe: Object.assign onto an otherwise-resolvable null-prototype dispatcher', () => {
    const source = `${nullProtoHelper}export function V({status}) { const cfg = getConfig(status); Object.assign(cfg, { textClass: 'p-[7px]' }); return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates dispatcherBindingUsesSafe: delete onto an otherwise-resolvable null-prototype dispatcher', () => {
    const source = `${nullProtoHelper}export function V({status}) { const cfg = getConfig(status); delete cfg.label; return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates dispatcherBindingUsesSafe: an alias write onto an otherwise-resolvable null-prototype dispatcher', () => {
    const source = `${nullProtoHelper}export function V({status}) { const cfg = getConfig(status); const x = cfg; x.textClass = 'p-[7px]'; return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates dispatcherBindingUsesSafe: a nested-callback write onto an otherwise-resolvable null-prototype dispatcher', () => {
    const source = `${nullProtoHelper}export function V({status}) { const cfg = getConfig(status); [1].forEach(() => { cfg.textClass = 'p-[7px]' }); return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates dispatcherBindingUsesSafe: a nested-block property write onto an otherwise-resolvable null-prototype dispatcher', () => {
    const source = `${nullProtoHelper}export function V({status}) { const cfg = getConfig(status); if (status === 'a') { cfg.textClass = 'p-[7px]' } return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates isConstVariableDeclaration: a `let` binding that is never reassigned or mutated must still stay unsupported', () => {
    // No mutation, no reassignment, no escape at all — dispatcherBindingUsesSafe
    // would report this binding safe. Only the const-only trust rule (LEAD
    // DECISION: "the binding is const") keeps this blocking; a `let` binding
    // trusted merely because no mutation was FOUND, rather than because the
    // binding categorically cannot be reassigned, is exactly the gap the
    // frozen review's finding 2 exploited.
    const source = `${nullProtoHelper}export function V({status}) { let cfg = getConfig(status); return <div className={cfg.textClass}/> }`
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates isTopLevelFiniteDispatcherFunction: a generator dispatcher with an otherwise-resolvable null-prototype shape', () => {
    const source = "function* getConfig(s) { switch (s) { case 'a': return { __proto__: null, label: 'A' }; default: return { __proto__: null, label: 'B' } } }\nexport function V({status}) { const cfg = getConfig(status); return <div className={cfg.textClass}/> }"
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates isTopLevelFiniteDispatcherFunction: an async dispatcher with an otherwise-resolvable null-prototype shape', () => {
    const source = "async function getConfig(s) { switch (s) { case 'a': return { __proto__: null, label: 'A' }; default: return { __proto__: null, label: 'B' } } }\nexport function V({status}) { const cfg = getConfig(status); return <div className={cfg.textClass}/> }"
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })

  it('isolates isTopLevelFiniteDispatcherFunction: a dispatcher nested inside the component with an otherwise-resolvable null-prototype shape', () => {
    const source = "export function V({status}) { function getConfig(s) { switch (s) { case 'a': return { __proto__: null, label: 'A' }; default: return { __proto__: null, label: 'B' } } } const cfg = getConfig(status); return <div className={cfg.textClass}/> }"
    assert.deepEqual(syntaxes(runWith(source), RULE.unsupported), ['className: cfg.textClass'])
  })
})
