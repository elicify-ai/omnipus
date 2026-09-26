import test from 'node:test'
import assert from 'node:assert/strict'
import { spawnSync } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

const repoRoot = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
const scriptPath = resolve(repoRoot, 'scripts/design-system-locks/print-audit-errors.mjs')

function runSummarizer(reportContent, { exists = true } = {}) {
  const dir = mkdtempSync(join(tmpdir(), 'ds-lock-summarizer-'))
  try {
    let reportPath = join(dir, 'report.json')
    if (!exists) reportPath = join(dir, 'missing-report.json')
    else writeFileSync(reportPath, typeof reportContent === 'string' ? reportContent : JSON.stringify(reportContent))
    return spawnSync(process.execPath, [scriptPath, reportPath], { encoding: 'utf8' })
  } finally {
    rmSync(dir, { recursive: true, force: true })
  }
}

function assertCountLine(stdout, expectedErrors) {
  const lines = stdout.split('\n').filter((line) => line.length > 0)
  const countLine = lines.at(-1)
  assert.match(
    countLine,
    new RegExp(`^design-system-locks: ${expectedErrors} error\\(s\\) — fix these; debt/appliedExceptions/appliedBoundaries are accepted entries and not listed`),
    `last stdout line must be the count line, got: ${countLine}`,
  )
  assert.equal(lines.length - 1, expectedErrors, 'exactly one line per error plus one count line')
  return countLine
}

test('0 errors: prints only the count line and exits 0', () => {
  const result = runSummarizer({
    ok: true,
    mode: 'audit',
    checkpoint: 'C1',
    scannedFileCount: 100,
    errorCount: 0,
    errors: [],
    debt: { fingerprints: [{ fingerprint: 'fp1', ruleId: 'spacing/off-scale', path: 'src/a.tsx', syntax: '9px', count: 1, accepted: true }], perRule: {}, acceptedCount: 1 },
    appliedExceptions: [{ ruleId: 'ts-colors/extension-boundary', path: 'src/b.tsx', syntax: '#fff' }],
    appliedBoundaries: [{ path: 'src/c.test.tsx', ruleId: 'controls/raw-button', syntax: "createElement('button')" }],
  })

  assert.equal(result.status, 0)
  const countLine = assertCountLine(result.stdout, 0)
  assert.match(countLine, /accepted: 1 debt, 1 appliedExceptions, 1 appliedBoundaries/)
  // Accepted entries must not be listed: none of their identifiers may appear.
  assert.ok(!result.stdout.includes('src/a.tsx'), 'debt fingerprints must not be listed')
  assert.ok(!result.stdout.includes('src/b.tsx'), 'appliedExceptions must not be listed')
  assert.ok(!result.stdout.includes('src/c.test.tsx'), 'appliedBoundaries must not be listed')
})

test('N errors: prints one compact line per error (kind, path, message), not debt, exits 1', () => {
  const result = runSummarizer({
    ok: false,
    mode: 'audit',
    checkpoint: 'C1',
    scannedFileCount: 100,
    errors: [
      {
        code: 'new-debt',
        message: 'new debt controls/raw-button src/components/chat/x.tsx button',
        path: 'src/components/chat/x.tsx',
        ruleId: 'controls/raw-button',
        syntax: 'button',
      },
      {
        code: 'expired-ledger',
        message: 'ledger entry expired at C1 (checkpoint C1)',
        fingerprint: 'deadbeef',
        path: 'src/components/chat/y.tsx',
        ruleId: 'spacing/off-scale',
        syntax: '9px',
      },
      {
        // An error without a path (e.g. setup/schema errors) must still render one line.
        code: 'missing-argument',
        message: 'missing --baseline',
      },
    ],
    errorCount: 3,
    debt: { fingerprints: [{ fingerprint: 'fp-debt', ruleId: 'spacing/off-scale', path: 'src/debt-only.tsx', syntax: '3px', count: 4, accepted: true }], perRule: {}, acceptedCount: 1 },
    appliedExceptions: [],
    appliedBoundaries: [],
  })

  assert.equal(result.status, 1)
  const lines = result.stdout.split('\n').filter((line) => line.length > 0)
  assert.equal(lines.length, 4, 'one line per error plus one count line')
  assert.match(lines[0], /new-debt/)
  assert.match(lines[0], /src\/components\/chat\/x\.tsx/)
  assert.match(lines[0], /new debt controls\/raw-button/)
  assert.match(lines[1], /expired-ledger/)
  assert.match(lines[1], /src\/components\/chat\/y\.tsx/)
  assert.match(lines[1], /ledger entry expired at C1/)
  assert.match(lines[2], /missing-argument/)
  assert.match(lines[2], /missing --baseline/)
  assertCountLine(result.stdout, 3)
  assert.ok(!result.stdout.includes('src/debt-only.tsx'), 'debt fingerprints must not be listed')
  assert.ok(!result.stdout.includes('fp-debt'), 'debt fingerprints must not be listed')
})

test('missing report file: clear error and exit 2', () => {
  const result = runSummarizer(null, { exists: false })
  assert.equal(result.status, 2)
  const output = `${result.stderr}${result.stdout}`
  assert.match(output, /print-audit-errors/)
  assert.match(output, /not found/)
  assert.match(output, /missing-report\.json/)
})

test('unparseable report JSON: clear error and exit 2', () => {
  const result = runSummarizer('{ not json')
  assert.equal(result.status, 2)
  const output = `${result.stderr}${result.stdout}`
  assert.match(output, /print-audit-errors/)
  assert.match(output, /not valid JSON|parse/i)
})

test('report without an errors array: clear error and exit 2', () => {
  const result = runSummarizer({ ok: true })
  assert.equal(result.status, 2)
  const output = `${result.stderr}${result.stdout}`
  assert.match(output, /print-audit-errors/)
  assert.match(output, /errors/i)
})
