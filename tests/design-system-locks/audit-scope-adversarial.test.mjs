// E1 and enforcement/contract.json require every lock's applicable formats and
// the complete application/public-package source tree. Fixtures replace only
// filesystem scanner modules; the loader, audit and accounting remain real.
// Mutation plan: drop per-lock validation, tolerate one missing format, and
// omit the package root. Unrelated scanner detection is covered by owner tests.
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import test from 'node:test'
import { audit } from '../../scripts/design-system-locks/audit.mjs'

// Expected applicability comes from the six E1 responsibilities, not exports
// read from the scanner implementations (which are the contract under test).
const FORMATS = {
  'css-colors.mjs': ['.css'],
  'ts-colors.mjs': ['.js', '.jsx', '.ts', '.tsx', '.svg'],
  'typography.mjs': ['.css', '.js', '.jsx', '.ts', '.tsx', '.svg'],
  'spacing.mjs': ['.css', '.js', '.jsx', '.ts', '.tsx'],
  'controls.mjs': ['.js', '.jsx', '.ts', '.tsx'],
  'status.mjs': ['.css', '.js', '.jsx', '.ts', '.tsx', '.svg'],
}

function fixture(t, omitted = null) {
  const root = mkdtempSync(resolve('dist/design-system-baseline/cli-lanes/audit-scope-test-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  function write(file, source) {
    const absolute = resolve(root, file)
    mkdirSync(dirname(absolute), { recursive: true })
    writeFileSync(absolute, source)
  }
  write('src/app.tsx', 'export const clean = true')
  write('design-system/catalog.json', JSON.stringify({ version: 1, entries: [] }))
  for (const [name, formats] of Object.entries(FORMATS)) {
    const extensions = omitted?.name === name ? formats.filter((ext) => ext !== omitted.extension) : formats
    write(`scanners/${name}`, `export const extensions = ${JSON.stringify(extensions)};
      export function scan({path,source}) {
        return source.includes('VIOLATION') ? [{ruleId:'fixture/debt',path,syntax:'VIOLATION',message:'Governed violation'}] : [];
      }`)
  }
  const run = () => audit({ root, scannerDir: resolve(root, 'scanners'), checkpoint: 'B',
    baseline: { version: 1, fingerprints: [] }, ledger: { version: 1, entries: [], exceptions: [] },
    policy: { tokenCssNames: [], resolvedTokens: {} } })
  return { write, run, root }
}

test('complete per-lock formats and absent optional package are accepted', async (t) => {
  const { run } = fixture(t)
  const report = await run()
  assert.deepEqual({ ok: report.ok, errors: report.errors, files: report.scannedFileCount },
    { ok: true, errors: [], files: 1 })
})

for (const [name, formats] of Object.entries(FORMATS)) {
  // Leave CSS scanner structurally valid so this tests applicability, not the
  // already-tested nonempty-extensions shape check.
  if (name === 'css-colors.mjs') continue
  const extension = formats.includes('.svg') ? '.svg' : '.tsx'
  test(`another scanner cannot cover for missing ${extension} in ${name}`, async (t) => {
    const { run } = fixture(t, { name, extension })
    const report = await run()
    assert.equal(report.ok, false)
    assert.deepEqual(report.errors.map(({ code, scanner, missingExtensions }) => ({ code, scanner, missingExtensions })),
      [{ code: 'scanner-coverage-gap', scanner: name, missingExtensions: [extension] }])
  })
}

test('public package source participates in ordinary debt accounting', async (t) => {
  const { write, run } = fixture(t)
  write('packages/ui/src/index.ts', 'export const VIOLATION = true')
  const report = await run()
  assert.equal(report.ok, false)
  assert.equal(report.scannedFileCount, 2)
  assert.deepEqual(report.debt.fingerprints.map(({ path, ruleId, syntax, count }) => ({ path, ruleId, syntax, count })),
    [{ path: 'packages/ui/src/index.ts', ruleId: 'fixture/debt', syntax: 'VIOLATION', count: 5 }])
  assert.deepEqual(report.errors.map(({ code, path }) => ({ code, path })),
    [{ code: 'new-debt', path: 'packages/ui/src/index.ts' }])
})

test('an optional package does not replace the required application source root', async (t) => {
  const { root, write, run } = fixture(t)
  rmSync(resolve(root, 'src'), { recursive: true })
  write('packages/ui/src/index.ts', 'export const clean = true')
  const report = await run()
  assert.equal(report.ok, false)
  assert.deepEqual(report.errors.map(({ code }) => code), ['missing-source-tree'])
})
