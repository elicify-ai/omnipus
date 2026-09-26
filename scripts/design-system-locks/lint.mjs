#!/usr/bin/env node
// Local lock-half design-system audit -- `npm run lint:design-system-locks`.
//
// Runs the same scanners and ledger accounting CI's `audit:design-system` runs,
// minus the coverage/Storybook half (no --coverage, no storybook index or
// evidence files): policy.mjs -> audit.mjs (baseline, ledger, checkpoint from
// current-checkpoint.mjs, report), then on failure prints print-audit-errors
// output for the report. Cross-platform: spawns each step with explicit argv
// (no shell syntax anywhere); every step's exit code is propagated -- a
// policy.mjs failure can never pass silently.
//
// Deliberately NOT equivalent to `audit:design-system` (kept unchanged): CI's
// command adds `--coverage --index dist/storybook/index.json --evidence ...`,
// which needs a Storybook static build and executed-check evidence files; the
// local wrapper checks the lock half only.
import { spawnSync } from 'node:child_process'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { readCurrentCheckpoint } from './current-checkpoint.mjs'
import { summarizeAuditErrors } from './print-audit-errors.mjs'

const scriptDir = dirname(fileURLToPath(import.meta.url))
const defaultRoot = resolve(scriptDir, '../..')

function spawnStep(file, args) {
  const result = spawnSync(process.execPath, [file, ...args], { encoding: 'utf8' })
  if (result.error) {
    process.stderr.write(`lint:design-system-locks: cannot spawn ${file}: ${result.error.message}\n`)
    return { status: 1, failed: true }
  }
  if (result.stderr) process.stderr.write(result.stderr)
  return { status: result.status ?? 1, failed: result.status !== 0, stdout: result.stdout }
}

export function runLint({
  root = defaultRoot,
  stdout = process.stdout,
  stderr = process.stderr,
  spawn = spawnStep,
} = {}) {
  const reportPath = resolve(root, 'test-results/design-system-audit.json')
  const policyPath = resolve(root, 'test-results/design-system-policy.json')

  mkdirSync(resolve(root, 'test-results'), { recursive: true })

  const policy = spawn(resolve(root, 'scripts/design-system-locks/policy.mjs'), [], { stderr })
  if (policy.failed) {
    stderr.write(`lint:design-system-locks: policy.mjs failed (exit ${policy.status})\n`)
    return policy.status || 1
  }
  writeFileSync(policyPath, policy.stdout)

  let checkpoint
  try {
    checkpoint = readCurrentCheckpoint(root)
  } catch (error) {
    stderr.write(`lint:design-system-locks: ${error.message}\n`)
    return 1
  }

  const audit = spawn(resolve(root, 'scripts/design-system-locks/audit.mjs'), [
    '--baseline', resolve(root, 'design-system/enforcement/baseline.json'),
    '--ledger', resolve(root, 'design-system/enforcement/ledger.json'),
    '--checkpoint', checkpoint,
    '--policy', policyPath,
    '--report', reportPath,
  ], { stderr })

  if (audit.failed) {
    stderr.write(`lint:design-system-locks: audit.mjs failed (exit ${audit.status})\n`)
    if (existsSync(reportPath)) {
      try {
        const report = JSON.parse(readFileSync(reportPath, 'utf8'))
        for (const line of summarizeAuditErrors(report)) stdout.write(`${line}\n`)
      } catch (error) {
        stderr.write(`lint:design-system-locks: cannot summarize report ${reportPath}: ${error.message}\n`)
      }
    }
    return audit.status || 1
  }
  return 0
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  process.exit(runLint())
}
