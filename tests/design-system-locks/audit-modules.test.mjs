// Cross-module proof must use the same complete, root-isolated source snapshot
// as the audit. The scanner is an injected consumer at the orchestration edge;
// filesystem collection, context construction and accounting stay real.
// Mutations: omit context, omit dependency files, expose a mutable context.
import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'
import { audit } from '../../scripts/design-system-locks/audit.mjs'

function fixture(t, color) {
  const root = mkdtempSync(resolve('dist/design-system-baseline/cli-lanes/audit-modules-'))
  t.after(() => rmSync(root, { recursive: true, force: true }))
  mkdirSync(resolve(root, 'src'), { recursive: true })
  const files = {
    'src/palette.ts': `export const paint = '${color}'`,
    'src/view.tsx': 'import { paint } from "./palette"; export const view = <div style={{color:paint}}/>',
  }
  for (const [path, source] of Object.entries(files)) writeFileSync(resolve(root, path), source)
  return { root, files }
}

function run(root, scan) {
  return audit({ root, checkpoint: 'B', policy: { tokenCssNames: [], resolvedTokens: {} },
    baseline: { version: 1, fingerprints: [] }, ledger: { version: 1, entries: [], exceptions: [] },
    scanners: [{ label: 'module-context-consumer', extensions: ['.ts', '.tsx'], scan }] })
}

test('every scan receives the complete frozen source context and matching current source', async (t) => {
  const { root, files } = fixture(t, '#ff0000')
  const seen = []
  const report = await run(root, ({ path, source, modules }) => {
    seen.push({ path, source, modules, frozen: Object.isFrozen(modules) })
    return []
  })
  assert.deepEqual(report.errors, [])
  assert.deepEqual(seen, Object.entries(files).map(([path, source]) => ({ path, source, modules: files, frozen: true })))
  assert.equal(seen[0].modules, seen[1].modules, 'all files in one audit share the same source snapshot')
})

test('concurrent roots cannot borrow each other\'s same-named dependency', async (t) => {
  const first = fixture(t, '#ff0000')
  const second = fixture(t, '#0000ff')
  function scan({ path, modules }) {
    if (path !== 'src/view.tsx') return []
    if (!modules) return [{ ruleId: 'context/unsupported', path, syntax: 'missing modules', message: 'Source context required' }]
    return modules['src/palette.ts'].includes('#ff0000')
      ? [{ ruleId: 'fixture/raw-color', path, syntax: '#ff0000', message: 'Forbidden fixture paint' }]
      : []
  }
  const [a, b] = await Promise.all([run(first.root, scan), run(second.root, scan)])
  assert.deepEqual(a.errors.map(({ code, path, ruleId, syntax }) => ({ code, path, ruleId, syntax })),
    [{ code: 'new-debt', path: 'src/view.tsx', ruleId: 'fixture/raw-color', syntax: '#ff0000' }])
  assert.deepEqual({ ok: b.ok, errors: b.errors, files: b.scannedFileCount }, { ok: true, errors: [], files: 2 })
})
