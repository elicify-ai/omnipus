// Violation-proof end-to-end (ds-safety-net round 3, reviewer finding B):
// the REAL audit.mjs CLI, pointed at a scratch root via its --root seam, must
// catch a raw <button> written into the scratch src tree — exit non-zero, the
// scratch file named in the report's errors. Real repo scanners (controls.mjs
// among them), real policy and checkpoint; no injected scanners, no synthetic
// rule. The only design-system file the audit reads from the audited root is
// design-system/catalog.json, so the scratch root copies it from the repo.

import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import test from 'node:test'
import { fileURLToPath } from 'node:url'

import { readCurrentCheckpoint } from '../../scripts/design-system-locks/current-checkpoint.mjs'

const HERE = dirname(fileURLToPath(import.meta.url))
const REPO = resolve(HERE, '../..')

test('violation-proof e2e: the real audit CLI flags a raw <button> in a scratch root', () => {
  const root = mkdtempSync(join(tmpdir(), 'dsl-e2e-'))
  try {
    mkdirSync(resolve(root, 'design-system'), { recursive: true })
    cpSync(resolve(REPO, 'design-system/catalog.json'), resolve(root, 'design-system/catalog.json'))
    mkdirSync(resolve(root, 'src'), { recursive: true })
    writeFileSync(
      resolve(root, 'src/Bad.tsx'),
      'export function Bad() {\n  return <button>raw button</button>\n}\n',
    )

    const policy = spawnSync(process.execPath, [resolve(REPO, 'scripts/design-system-locks/policy.mjs')], { encoding: 'utf8' })
    assert.equal(policy.status, 0, `policy.mjs failed: ${policy.stderr}`)
    const policyPath = resolve(root, 'policy.json')
    writeFileSync(policyPath, policy.stdout)

    const reportPath = resolve(root, 'report.json')
    const result = spawnSync(process.execPath, [
      resolve(REPO, 'scripts/design-system-locks/audit.mjs'),
      '--root', root,
      '--baseline', resolve(REPO, 'design-system/enforcement/baseline.json'),
      '--ledger', resolve(REPO, 'design-system/enforcement/ledger.json'),
      '--checkpoint', readCurrentCheckpoint(REPO),
      '--policy', policyPath,
      '--report', reportPath,
    ], { encoding: 'utf8' })
    assert.equal(result.status, 1, `expected audit exit 1, got ${result.status}; stderr: ${result.stderr}`)
    const report = JSON.parse(readFileSync(reportPath, 'utf8'))
    assert.ok(Array.isArray(report.errors) && report.errors.length > 0, 'report carries no errors')
    const flagged = report.errors.filter((error) => error.path === 'src/Bad.tsx')
    assert.ok(flagged.length > 0, `src/Bad.tsx not in errors; got: ${JSON.stringify(report.errors.map((error) => error.path))}`)
    assert.ok(
      flagged.some((error) => error.code === 'new-debt'),
      `expected a new-debt entry for the raw button; got codes: ${flagged.map((error) => error.code).join(', ')}`,
    )
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})
