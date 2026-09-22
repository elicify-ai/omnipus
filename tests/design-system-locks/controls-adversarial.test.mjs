// Adversarial contract tests for the controls lock scanner.
//
// Oracles come from design-system/enforcement/contract.json (raw findings,
// central exemptions, occurrence counting, fail-closed parsing) and E1/D5 of
// docs/internal/design/design-system-definition.md. They intentionally do not
// characterize the scanner's current implementation.

import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'

import { extensions, scan } from '../../scripts/design-system-locks/controls.mjs'
import { audit } from '../../scripts/design-system-locks/audit.mjs'

const FEATURE_PATH = 'src/features/adversarial-fixture.tsx'
const CATALOG = JSON.parse(readFileSync(new URL('../../design-system/catalog.json', import.meta.url), 'utf8'))

function findings(source, path = FEATURE_PATH, policy = {}) {
  return scan({ path, source, policy, catalog: CATALOG })
}

function ruleIds(source, path = FEATURE_PATH, policy = {}) {
  return findings(source, path, policy).map((finding) => finding.ruleId)
}

test('raw violations remain findings in UI, story, and test paths', () => {
  const source = 'export const Fixture = () => <button>Run</button>'
  for (const path of [
    'src/components/ui/adversarial-fixture.tsx',
    'src/components/ui/adversarial-fixture.stories.tsx',
    'src/components/ui/adversarial-fixture.test.tsx',
  ]) {
    assert.deepEqual(ruleIds(source, path), ['controls/raw-button'], `${path} must emit a raw finding for central boundary review`)
  }
})

test('concurrent audit roots classify ui imports from their own immutable catalog', async (t) => {
  const make = (publicExports) => {
    mkdirSync(resolve('dist/design-system-baseline/cli-lanes'), { recursive: true }) // a fresh checkout has no dist/
    const root = mkdtempSync(resolve('dist/design-system-baseline/cli-lanes/controls-catalog-'))
    t.after(() => rmSync(root, { recursive: true, force: true }))
    mkdirSync(resolve(root, 'src/features'), { recursive: true })
    writeFileSync(resolve(root, 'src/features/view.ts'), "import { Button } from '@/components/ui/button'\n")
    return { root, catalog: { version: 1, entries: [{ source: 'src/components/ui/button.tsx', exports: ['Button'], publicExports, publicTypes: [] }] } }
  }
  const allowed = make(['Button']); const blocked = make([])
  const run = ({ root, catalog }) => audit({ root, catalog, checkpoint: 'B', policy: { tokenCssNames: [], resolvedTokens: {} },
    baseline: { version: 1, fingerprints: [] }, ledger: { version: 1, entries: [], exceptions: [] },
    scanners: [{ label: 'controls.mjs', extensions, scan }] })
  const [first, second] = await Promise.all([run(allowed), run(blocked)])
  assert.deepEqual(first.errors, [])
  assert.deepEqual(second.errors.map(({ code, ruleId }) => ({ code, ruleId })), [
    { code: 'new-debt', ruleId: 'controls/shadcn-low-level-import' },
  ])
})

test('caller-supplied fake exclusions cannot suppress raw scanner findings', () => {
  const source = [
    "import { Dialog } from '@radix-ui/react-dialog'",
    'export const Fixture = () => <button>Run</button>',
  ].join('\n')
  const fakeExclusionPolicy = {
    exclude: ['src/**'],
    allowedPaths: ['src/features/adversarial-fixture.tsx'],
    exceptions: [{ path: FEATURE_PATH, ruleId: '*' }],
  }

  assert.deepEqual(ruleIds(source, FEATURE_PATH, fakeExclusionPolicy), [
    'controls/radix-import',
    'controls/raw-button',
  ])
})

test('duplicate violations remain separate occurrences of one fingerprint syntax', () => {
  const source = [
    'export const First = () => <button>First</button>',
    'export const Second = () => <button>Second</button>',
  ].join('\n')

  assert.deepEqual(
    findings(source).map(({ ruleId, path, syntax }) => ({ ruleId, path, syntax })),
    [
      { ruleId: 'controls/raw-button', path: FEATURE_PATH, syntax: '<button>' },
      { ruleId: 'controls/raw-button', path: FEATURE_PATH, syntax: '<button>' },
    ],
  )
})

test('local confirm declarations and local browser-object names shadow globals', () => {
  const source = [
    'function confirm() { return true }',
    'const window = { confirm: () => true }',
    'const globalThis = { confirm: () => true }',
    'export const ask = () => [confirm(), window.confirm(), globalThis.confirm()]',
  ].join('\n')

  assert.deepEqual(findings(source), [])
})

test('a browser confirm captured in one function and called in another cannot pass silently', () => {
  const source = [
    'let capturedConfirm',
    'export function captureConfirm() { capturedConfirm = window.confirm }',
    "export function askLater() { return capturedConfirm('Delete?') }",
  ].join('\n')

  const result = findings(source)
  assert.ok(
    result.some((finding) => finding.ruleId === 'controls/global-confirm'),
    'the global reference or its later alias call must produce a global-confirm finding',
  )
  assert.ok(result.every((finding) => finding.ruleId !== 'controls/parse-error'))
})

test('a hoisted local var named confirm shadows the browser global before its declaration', () => {
  const source = [
    'export function askLocal() {',
    "  confirm('local')",
    '  var confirm = () => true',
    '}',
  ].join('\n')

  assert.deepEqual(findings(source), [])
})

test('function parameters shadow window, document, and React factory globals', () => {
  const source = [
    'export function build(window, document, React) {',
    "  window.confirm('local')",
    "  document.createElement('button')",
    "  return React.createElement('dialog')",
    '}',
  ].join('\n')

  assert.deepEqual(findings(source), [])
})

test('aliased JSX runtime factories still report intrinsic controls', () => {
  const source = [
    "import { jsx as renderOne, jsxs as renderMany } from 'react/jsx-runtime'",
    "import { jsxDEV as renderDev } from 'react/jsx-dev-runtime'",
    "export const ButtonFixture = () => renderOne('button', { children: 'Run' })",
    "export const DialogFixture = () => renderMany('dialog', { children: [] })",
    "export const DevFixture = () => renderDev('button', { children: 'Debug' })",
  ].join('\n')

  assert.deepEqual(
    findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [
      { ruleId: 'controls/raw-button', syntax: 'createElement("button")' },
      { ruleId: 'controls/raw-button', syntax: 'createElement("button")' },
      { ruleId: 'controls/raw-dialog', syntax: 'createElement("dialog")' },
    ],
  )
})

test('static and conditional intrinsic JSX aliases cannot hide raw controls', () => {
  const source = [
    "const DialogAlias = 'dialog'",
    "const ButtonAlias = asChild ? Slot : 'button'",
    'export const Fixture = () => <><DialogAlias /><ButtonAlias /></>',
  ].join('\n')

  assert.deepEqual(
    findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [
      { ruleId: 'controls/raw-button', syntax: '<button>' },
      { ruleId: 'controls/raw-dialog', syntax: '<dialog>' },
    ],
  )
})

test('local component aliases and conditional custom branches remain clean', () => {
  const source = [
    'const LocalOne = () => null',
    'const LocalTwo = () => null',
    'const Component = choose ? LocalOne : LocalTwo',
    'export const Fixture = () => <Component />',
  ].join('\n')
  assert.deepEqual(findings(source), [])
})

test('escaped browser confirm references are reported once at each source reference', () => {
  const source = [
    "window.confirm.call(window, 'Delete?')",
    'consume(window.confirm)',
    'const holder = { confirmLater: window.confirm }',
    "holder.confirmLater('Delete?')",
    'const tracked = window.confirm',
    'consume(tracked)',
  ].join('\n')
  assert.deepEqual(
    findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [
      { ruleId: 'controls/global-confirm', syntax: 'window.confirm' },
      { ruleId: 'controls/global-confirm', syntax: 'window.confirm' },
      { ruleId: 'controls/global-confirm', syntax: 'window.confirm' },
      { ruleId: 'controls/global-confirm', syntax: 'window.confirm' },
    ],
  )
})

test('direct confirm calls remain one finding rather than a call plus reference duplicate', () => {
  assert.deepEqual(
    findings("window.confirm('Delete?')").map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [{ ruleId: 'controls/global-confirm', syntax: 'window.confirm(...)' }],
  )
})

test('locally shadowed browser objects keep confirm references clean', () => {
  const source = [
    'export function useLocal(window) {',
    '  consume(window.confirm)',
    "  return window.confirm.call(window, 'local')",
    '}',
  ].join('\n')
  assert.deepEqual(findings(source), [])
})

test('fully dynamic input type and role fail closed instead of hiding a checkbox switch', () => {
  const source = 'export const Fixture = ({ type, role }) => <input type={type} role={role} />'
  const result = findings(source)

  assert.ok(result.length > 0, 'dynamic type and role must produce an explicit unresolved finding')
  assert.ok(result.every((finding) => finding.ruleId !== 'controls/parse-error'))
  assert.ok(result.every((finding) => finding.path === FEATURE_PATH))
  assert.ok(result.every((finding) => finding.syntax === '<input type={expr} role={expr}>'))
})

test('dynamic Radix imports are governed like static low-level imports', () => {
  const source = "export const loadDialog = () => import('@radix-ui/react-dialog')"
  assert.deepEqual(
    findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [{ ruleId: 'controls/radix-import', syntax: 'import("@radix-ui/react-dialog")' }],
  )
})

test('Radix re-exports remain governed at the module boundary', () => {
  const source = "export { Root as DialogRoot } from '@radix-ui/react-dialog'"
  assert.deepEqual(
    findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [{ ruleId: 'controls/radix-import', syntax: 'export { Root as DialogRoot } from "@radix-ui/react-dialog"' }],
  )
})

test('namespace imports cannot expose non-public exports from a partly public UI module', () => {
  const source = "import * as ButtonModule from '@/components/ui/button'"
  assert.deepEqual(
    findings(source).map(({ ruleId, syntax }) => ({ ruleId, syntax })),
    [{ ruleId: 'controls/shadcn-low-level-import', syntax: 'import * as ButtonModule from "src/components/ui/button"' }],
  )
})

test('parse failures remain blocking in paths that may later receive central boundaries', () => {
  for (const path of [
    'src/components/ui/broken.tsx',
    'src/components/ui/broken.stories.tsx',
    'src/components/ui/broken.test.tsx',
  ]) {
    const result = findings('export const broken = {', path, { exclude: ['src/**'] })
    assert.equal(result.length, 1, `${path} must produce exactly one infrastructure finding`)
    assert.equal(result[0].ruleId, 'controls/parse-error')
    assert.equal(result[0].path, path)
    assert.match(result[0].message, /failed to parse/i)
  }
})
