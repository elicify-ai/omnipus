// Contract: scanners receive the canonical token graph, never a guessed
// token-id-to-CSS spelling. Real graph validation and filesystem reads remain
// inside the unit boundary. Mutations: lose a CSS name, derive its name from
// the id, and drop the resolved CSS map in audit composition.
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import { createPolicy, loadPolicy } from '../../scripts/design-system-locks/policy.mjs'
import { audit } from '../../scripts/design-system-locks/audit.mjs'

const PRIMITIVE = { id: 'primitive.ink', css: '--ink', layer: 'primitive', kind: 'color', value: '#010203' }
const SEMANTIC = { id: 'color.text', css: '--foreground', layer: 'semantic', kind: 'color', ref: 'primitive.ink' }
const SOURCES = [{ version: 1, tokens: [PRIMITIVE] }, { version: 1, tokens: [SEMANTIC] }]
const EXPECTED = {
  tokenCssNames: ['--foreground', '--ink'],
  resolvedTokens: { 'color.text': '#010203', 'primitive.ink': '#010203' },
  resolvedCssTokens: { '--foreground': '#010203', '--ink': '#010203' },
}

function fixture(t) {
  const root = mkdtempSync(resolve('dist/design-system-baseline/cli-lanes/token-policy-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  mkdirSync(resolve(root, 'design-system/tokens'), { recursive: true })
  mkdirSync(resolve(root, 'src'), { recursive: true })
  writeFileSync(resolve(root, 'src/code.ts'), 'export const value = true')
  return root
}

test('policy preserves declared CSS names and resolves semantic references', () => {
  assert.deepEqual(createPolicy(SOURCES), EXPECTED)
  assert.equal(JSON.stringify(createPolicy([...SOURCES].reverse())), JSON.stringify(EXPECTED),
    'source order must not change generated policy bytes')
})

test('invalid canonical graph cannot produce a policy', () => {
  assert.throws(() => createPolicy([{ version: 1, tokens: [{ ...SEMANTIC, ref: 'primitive.absent' }] }]),
    /references undefined token "primitive.absent"/)
})

test('loader reads both real canonical files and fails on a missing source', (t) => {
  const root = fixture(t)
  writeFileSync(resolve(root, 'design-system/tokens/colors.json'), JSON.stringify(SOURCES[0]))
  assert.throws(() => loadPolicy(root), { code: 'ENOENT' })
  writeFileSync(resolve(root, 'design-system/tokens/foundations.json'), JSON.stringify(SOURCES[1]))
  assert.deepEqual(loadPolicy(root), EXPECTED)
})

test('audit forwards the actual CSS map to scanners without rewriting names', async (t) => {
  const root = fixture(t)
  const seen = []
  const report = await audit({ root, checkpoint: 'B', policy: EXPECTED,
    baseline: { version: 1, fingerprints: [] }, ledger: { version: 1, entries: [], exceptions: [] },
    scanners: [{ label: 'policy-observer', extensions: ['.ts'], scan({ policy }) { seen.push(policy); return [] } }] })
  assert.deepEqual(report.errors, [])
  assert.deepEqual(seen, [EXPECTED])
  assert.equal(Object.isFrozen(seen[0].resolvedCssTokens), true)
})

test('audit rejects duplicate or malformed CSS names and requires exact resolved CSS keys', async (t) => {
  const root = fixture(t)
  const invalidPolicies = [
    { tokenCssNames: ['--ink', '--ink'], resolvedTokens: {}, resolvedCssTokens: { '--ink': '#010203', '--invented': '#ffffff' } },
    { tokenCssNames: ['ink'], resolvedTokens: {}, resolvedCssTokens: { ink: '#010203' } },
    { tokenCssNames: ['--ink', 42], resolvedTokens: {}, resolvedCssTokens: { '--ink': '#010203', 42: '#ffffff' } },
  ]

  for (const policy of invalidPolicies) {
    const report = await audit({ root, checkpoint: 'B', policy,
      baseline: { version: 1, fingerprints: [] }, ledger: { version: 1, entries: [], exceptions: [] },
      scanners: [{ label: 'clean', extensions: ['.ts'], scan() { return [] } }] })
    assert.deepEqual(report.errors.map(({ code }) => code), ['invalid-policy'])
  }
})

for (const [name, map] of Object.entries({ null: null, array: [], missing: { '--ink': '#010203' },
  unregistered: { ...EXPECTED.resolvedCssTokens, '--invented': '#ffffff' },
  nonfinite: { ...EXPECTED.resolvedCssTokens, '--ink': Infinity } })) {
  test(`audit rejects an invalid resolved CSS map: ${name}`, async (t) => {
    const root = fixture(t)
    const report = await audit({ root, checkpoint: 'B', policy: { ...EXPECTED, resolvedCssTokens: map },
      baseline: { version: 1, fingerprints: [] }, ledger: { version: 1, entries: [], exceptions: [] },
      scanners: [{ label: 'clean', extensions: ['.ts'], scan() { return [] } }] })
    assert.equal(report.ok, false)
    assert.deepEqual(report.errors.map(({ code }) => code), ['invalid-policy'])
  })
}
