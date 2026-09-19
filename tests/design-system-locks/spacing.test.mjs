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
