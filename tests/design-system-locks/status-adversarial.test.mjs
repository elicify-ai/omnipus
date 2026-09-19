// Adversarial D4 status-scanner tests.
//
// Expected outcomes come from design-system-definition.md D4 and the shared
// enforcement contract. The scanner under test remains real; exceptions and
// reviewed path boundaries must be applied later by the audit orchestrator.

import assert from 'node:assert/strict'
import test from 'node:test'

import { scan } from '../../scripts/design-system-locks/status.mjs'

const RULE = {
  mismatch: 'design-system/status-mismatch',
  literal: 'design-system/status-literal',
  unsupported: 'design-system/status-unsupported',
}

const POLICY = {
  tokenCssNames: [
    '--color-status-done',
    '--color-status-failed',
  ],
  resolvedTokens: {
    'color.status.done': '#10B981',
    'color.status.failed': '#EF4444',
  },
}

function run(source, { path = 'src/features/status-adversarial.ts', policy = POLICY } = {}) {
  return scan({ path, source, policy })
}

function compact(findings) {
  return findings.map(({ ruleId, path, syntax }) => ({ ruleId, path, syntax }))
}

test('unrelated next and done properties do not create a D4 status context', () => {
  const source = `
    export const paginationColors = {
      next: '#111111',
      done: '#222222',
    }
  `

  assert.deepEqual(run(source), [])
})

test('a shorthand value in a clearly named status map cannot hide a wrong D4 colour', () => {
  const source = `
    const done = '#EF4444'
    export const STATUS_COLORS = { done }
  `

  const findings = run(source)
  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, RULE.mismatch)
  assert.match(findings[0].message, /done/)
  assert.match(findings[0].message, /#10B981/)
  assert.ok(findings[0].syntax.includes('done') || findings[0].syntax.includes('#EF4444'))
})

test('a local style alias on explicit status chrome is classified instead of silently allowed', () => {
  const source = `
    const doneStyle = { backgroundColor: '#EF4444' }
    export const DoneChip = () => <span status="done" style={doneStyle}>Done</span>
  `

  const findings = run(source, { path: 'src/features/status-adversarial.tsx' })
  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, RULE.mismatch)
  assert.match(findings[0].message, /done/)
  assert.match(findings[0].message, /#10B981/)
})

test('a side-specific status border using another registered status token is a mismatch', () => {
  const source = `[data-status="done"] { border-left-color: var(--color-status-failed); }`
  const findings = run(source, { path: 'src/features/status-adversarial.css' })

  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, RULE.mismatch)
  assert.match(findings[0].message, /done/)
  assert.match(findings[0].message, /--color-status-failed/)
})

test('a matching-value token absent from tokenCssNames is unsupported', () => {
  const source = `export const STATUS_COLORS = { done: 'var(--color-fake)' }`
  const policy = {
    tokenCssNames: ['--color-status-done'],
    resolvedTokens: {
      'color.status.done': '#10B981',
      'color.fake': '#10B981',
    },
  }

  const findings = run(source, { policy })
  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, RULE.unsupported)
  assert.match(findings[0].message, /--color-fake/)
})

test('status paint inside a valid SVG style element is analyzed as CSS', () => {
  const source = `<svg><style>.status-done { fill: #EF4444; }</style><circle class="status-done" /></svg>`
  const findings = run(source, { path: 'src/assets/status-adversarial.svg' })

  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, RULE.mismatch)
  assert.match(findings[0].message, /done/)
  assert.match(findings[0].message, /#10B981/)
  assert.match(findings[0].syntax, /#EF4444/)
})

test('raw status findings are invariant across centrally reviewed-looking paths and fake exclusions', () => {
  const source = `export const STATUS_COLORS = { done: '#10B981' }`
  const fakeExclusionPolicy = {
    ...POLICY,
    exclude: ['src/**'],
    allowedPaths: ['src/features/status-adversarial.ts'],
    exceptions: [{ ruleId: RULE.literal, path: '*', syntax: '*' }],
  }
  const paths = [
    'src/features/status-adversarial.ts',
    'src/components/ui/status-adversarial.ts',
    'src/components/ui/status-adversarial.stories.ts',
    'src/components/ui/status-adversarial.test.ts',
    'src/design-system/tokens.generated.ts',
    'src/components/workspaces/graph/status-adversarial.ts',
  ]

  const results = paths.map((path) => compact(run(source, { path, policy: fakeExclusionPolicy })))
  for (let index = 0; index < paths.length; index += 1) {
    assert.equal(results[index].length, 1)
    assert.equal(results[index][0].ruleId, RULE.literal)
    assert.equal(results[index][0].path, paths[index])
    assert.match(results[index][0].syntax, /#10B981/)
    assert.equal(results[index][0].syntax, results[0][0].syntax, 'canonical syntax must not vary by path or fake exclusion policy')
  }
})

test('status words in non-paint data and messages do not create palette context', () => {
  const source = `
    const message = { error: translatedMessage, done: false }
    const state = { kickoffAttemptStatus: { ...previous, [workspaceId]: 'in-flight' } }
    export const View = ({ error }) => <Panel data-testid={\`probe-${'${error}'}\`} error={error} message={message} state={state} />
  `
  assert.deepEqual(run(source, { path: 'src/features/status-adversarial.tsx' }), [])
})

test('a genuinely dynamic status palette lookup remains fail-closed', () => {
  const source = `export const statusColor = STATUS_COLORS[item.status]`
  const findings = run(source)
  assert.equal(findings.length, 1)
  assert.equal(findings[0].ruleId, RULE.unsupported)
})
