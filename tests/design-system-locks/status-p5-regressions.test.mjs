// FIX-P5 regression tests.
//
// Five confirmed false greens in scripts/design-system-locks/status.mjs, each
// reproduced against the real scan() by a probe lane before this fix landed.
// Every item below pairs a FORBIDDEN case (must produce a finding) with a
// PERMITTED control (must stay clean) so a real finding proves detection,
// not "flag everything".
//
// Expected values are transcribed from docs/internal/design/design-system-
// definition.md §D4 and design-system/enforcement/contract.json, never read
// off the scanner's own output.

import assert from 'node:assert/strict'
import { describe, it } from 'node:test'

import { scan } from '../../scripts/design-system-locks/status.mjs'

const D4 = {
  inbox: '#9CA3AF',
  next: '#3B82F6',
  'in-progress': '#D4AF37',
  blocked: '#F97316',
  done: '#10B981',
  failed: '#EF4444',
  cancelled: '#EAB308',
}

const POLICY = {
  tokenCssNames: [
    '--color-status-inbox',
    '--color-status-next',
    '--color-status-in-progress',
    '--color-status-blocked',
    '--color-status-done',
    '--color-status-failed',
    '--color-status-cancelled',
  ],
  resolvedTokens: {
    'color.status.inbox': D4.inbox,
    'color.status.next': D4.next,
    'color.status.in-progress': D4['in-progress'],
    'color.status.blocked': D4.blocked,
    'color.status.done': D4.done,
    'color.status.failed': D4.failed,
    'color.status.cancelled': D4.cancelled,
  },
}

const RULE = {
  mismatch: 'design-system/status-mismatch',
  literal: 'design-system/status-literal',
  unsupported: 'design-system/status-unsupported',
}

function run(source, extra = {}) {
  return scan({
    path: extra.path ?? 'src/fixture.ts',
    source,
    policy: extra.policy ?? POLICY,
    modules: extra.modules,
  })
}

describe('FIX-P5 item 1: second D4-shaped palette under an innocuous name (shape, not name)', () => {
  it('PERMITTED: an innocuously-named seven-state map that only references status tokens stays clean', () => {
    const source = `
      const THEME_HUES = {
        inbox: 'var(--color-status-inbox)',
        next: 'var(--color-status-next)',
        'in-progress': 'var(--color-status-in-progress)',
        blocked: 'var(--color-status-blocked)',
        done: 'var(--color-status-done)',
        failed: 'var(--color-status-failed)',
        cancelled: 'var(--color-status-cancelled)',
      }
    `
    assert.deepEqual(run(source), [])
  })

  it('FORBIDDEN: THEME_HUES (no status/palette/badge/... in the name) with a wrong failed hex is still a mismatch', () => {
    const source = `
      const THEME_HUES = {
        inbox: 'var(--color-status-inbox)',
        next: 'var(--color-status-next)',
        'in-progress': 'var(--color-status-in-progress)',
        blocked: 'var(--color-status-blocked)',
        done: 'var(--color-status-done)',
        failed: '#000000',
        cancelled: 'var(--color-status-cancelled)',
      }
    `
    const findings = run(source)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch), 'a second D4-shaped palette must be caught regardless of binding name')
    assert.ok(findings.some((finding) => finding.message.includes('failed')))
  })
})

describe('FIX-P5 item 2: a value built by string concatenation must fail closed, never silence', () => {
  it('PERMITTED: a plain (non-concatenated) literal hex is classified normally', () => {
    const findings = run(`const statusPalette = { failed: '#EF4444' }`)
    assert.equal(findings.length, 1)
    assert.equal(findings[0].ruleId, RULE.literal)
  })

  it('FORBIDDEN: a status map value built by "+" concatenation produces an unsupported finding, not zero findings', () => {
    const findings = run(`const statusPalette = { failed: '#0' + '00000' }`)
    assert.ok(findings.length > 0, 'concatenation must never be silently dropped')
    assert.ok(findings.some((finding) => finding.ruleId === RULE.unsupported))
    assert.equal(findings.some((finding) => finding.ruleId === RULE.mismatch || finding.ruleId === RULE.literal), false, 'a concatenated value must never be folded and compared as if it were a literal')
  })
})

describe('FIX-P5 item 3: mutation after definition must be re-classified, not skipped', () => {
  it('PERMITTED: a correct definition mutated to the same correct token stays clean', () => {
    const source = `
      const statusPalette = { failed: 'var(--color-status-failed)' }
      statusPalette.failed = 'var(--color-status-failed)'
    `
    assert.deepEqual(run(source), [])
  })

  it('FORBIDDEN: a correct definition mutated to a wrong hex afterward is caught as a mismatch', () => {
    const source = `
      const statusPalette = { failed: '#EF4444' }
      statusPalette.failed = '#000000'
    `
    const findings = run(source)
    assert.ok(findings.some((finding) => finding.ruleId === RULE.mismatch), 'the mutated value, not just the original definition, must be classified')
  })
})

describe('FIX-P5 item 4: a runtime status paint binding with no literal status key must fail closed', () => {
  it('PERMITTED: an explicit literal data-status attribute paired with a correct token stays clean', () => {
    const source = `
      function Badge({ status }) {
        return <span data-status="failed" style={{ background: 'var(--color-status-failed)' }} />
      }
    `
    assert.deepEqual(run(source, { path: 'src/fixture.tsx' }), [])
  })

  it('FORBIDDEN: style={{ background: getStatusHex(status) }} with no data-status/status/className channel is unsupported, not []', () => {
    const source = `
      import { getStatusHex } from './statusHelpers'
      function Badge({ status }) {
        return <span style={{ background: getStatusHex(status) }} />
      }
    `
    const findings = run(source, { path: 'src/fixture.tsx', modules: {} })
    assert.ok(findings.length > 0, 'a runtime status paint call with no literal status key must never resolve to zero findings')
    assert.ok(findings.some((finding) => finding.ruleId === RULE.unsupported))
  })
})

describe('FIX-P5 item 5: an all-computed-key map under a status-shaped name must fail closed', () => {
  it('PERMITTED: the same shape with a statically resolvable key and a correct token stays clean', () => {
    const source = `
      const cancelledEntries = {
        ['cancelled']: 'var(--color-status-cancelled)',
      }
    `
    assert.deepEqual(run(source), [])
  })

  it('FORBIDDEN: a status-shaped binding whose only key comes from a runtime call is unsupported, not []', () => {
    const source = `
      function getKey(runtimeStatus) { return runtimeStatus }
      const cancelledEntries = {
        [getKey(currentStatus)]: '#000000',
      }
    `
    const findings = run(source)
    assert.ok(findings.length > 0, 'an all-computed-key status-shaped map must never resolve to zero findings')
    assert.ok(findings.some((finding) => finding.ruleId === RULE.unsupported))
  })
})
