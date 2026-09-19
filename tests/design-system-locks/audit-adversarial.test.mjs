// Adversarial orchestration tests derived from
// design-system/enforcement/review-audit.json and the shared enforcement
// contract. Scratch repositories live under the checked worktree's evidence
// directory; the real source tree and worker-owned audit tests stay untouched.

import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { mkdirSync, rmSync, symlinkSync, writeFileSync } from 'node:fs'
import { resolve } from 'node:path'
import test from 'node:test'

import { audit } from '../../scripts/design-system-locks/audit.mjs'

const SCRATCH_PARENT = resolve('dist/design-system-baseline/cli-lanes/audit-adversarial')
const EMPTY_BASELINE = Object.freeze({ version: 1, fingerprints: [] })
const EMPTY_LEDGER = Object.freeze({ version: 1, entries: [], exceptions: [] })
const EMPTY_POLICY = Object.freeze({ tokenCssNames: [], resolvedTokens: {} })

function scratchRoot(name) {
  const root = resolve(SCRATCH_PARENT, name)
  rmSync(root, { recursive: true, force: true })
  mkdirSync(root, { recursive: true })
  return root
}

function writeFixture(root, path, source = 'export const fixture = true\n') {
  const absolute = resolve(root, path)
  mkdirSync(resolve(absolute, '..'), { recursive: true })
  writeFileSync(absolute, source)
}

function markerScanner({ ruleId = 'controls/raw-button', syntax = '<button>', message = 'governed violation' } = {}) {
  return {
    label: 'adversarial-marker',
    extensions: ['.tsx'],
    scan({ path, source }) {
      if (!source.includes('VIOLATION')) return []
      return [{ ruleId, path, syntax, message }]
    },
  }
}

function scannerForFinding(finding) {
  return {
    label: 'adversarial-infrastructure',
    extensions: ['.tsx'],
    scan({ path }) {
      return [{ ...finding, path }]
    },
  }
}

async function runAudit(root, overrides = {}) {
  return audit({
    root,
    checkpoint: 'B',
    baseline: EMPTY_BASELINE,
    ledger: EMPTY_LEDGER,
    policy: EMPTY_POLICY,
    scanners: [markerScanner()],
    ...overrides,
  })
}

test('route filenames containing dollar segments are scanned and accounted', async () => {
  const root = scratchRoot('dollar-route')
  writeFixture(root, 'src/routes/$workspaceId.tsx', 'export const VIOLATION = () => null\n')

  const report = await runAudit(root)

  assert.equal(report.scannedFileCount, 1)
  assert.deepEqual(
    report.errors.map(({ code, path, ruleId, syntax }) => ({ code, path, ruleId, syntax })),
    [{
      code: 'new-debt',
      path: 'src/routes/$workspaceId.tsx',
      ruleId: 'controls/raw-button',
      syntax: '<button>',
    }],
  )
})

test('unregistered story and test paths receive no implicit exemption', async () => {
  const root = scratchRoot('unregistered-boundaries')
  writeFixture(root, 'src/widgets/Card.stories.tsx', 'export const VIOLATION = true\n')
  writeFixture(root, 'src/widgets/Card.test.tsx', 'export const VIOLATION = true\n')

  const report = await runAudit(root)

  assert.equal(report.scannedFileCount, 2)
  assert.deepEqual(
    report.errors.map(({ code, path, ruleId }) => ({ code, path, ruleId })),
    [
      { code: 'new-debt', path: 'src/widgets/Card.stories.tsx', ruleId: 'controls/raw-button' },
      { code: 'new-debt', path: 'src/widgets/Card.test.tsx', ruleId: 'controls/raw-button' },
    ],
  )
})

test('an exact permanent exception cannot suppress a parse failure', async () => {
  const root = scratchRoot('excepted-parse')
  const path = 'src/widgets/Broken.tsx'
  const finding = {
    ruleId: 'controls/parse-error',
    syntax: 'Unexpected token',
    message: 'failed to parse governed source',
  }
  writeFixture(root, path)

  const report = await runAudit(root, {
    scanners: [scannerForFinding(finding)],
    ledger: {
      version: 1,
      entries: [],
      exceptions: [{ ruleId: finding.ruleId, path, syntax: finding.syntax, reason: 'must not suppress infrastructure', owner: 'test' }],
    },
  })

  assert.deepEqual(
    report.errors.map(({ code, path: errorPath, ruleId }) => ({ code, path: errorPath, ruleId })),
    [{ code: 'parse-failure', path, ruleId: 'controls/parse-error' }],
  )
})

test('an exact permanent exception cannot suppress unsupported syntax', async () => {
  const root = scratchRoot('excepted-unsupported')
  const path = 'src/widgets/Unsupported.tsx'
  const finding = {
    ruleId: 'enforcement/unsupported-syntax',
    syntax: 'decorator-expression',
    message: 'unsupported governed syntax',
  }
  writeFixture(root, path)

  const report = await runAudit(root, {
    scanners: [scannerForFinding(finding)],
    ledger: {
      version: 1,
      entries: [],
      exceptions: [{ ruleId: finding.ruleId, path, syntax: finding.syntax, reason: 'must not suppress infrastructure', owner: 'test' }],
    },
  })

  assert.deepEqual(
    report.errors.map(({ code, path: errorPath, ruleId }) => ({ code, path: errorPath, ruleId })),
    [{ code: 'unsupported', path, ruleId: 'enforcement/unsupported-syntax' }],
  )
})

test('an exact reviewed boundary cannot suppress a parse failure', async () => {
  const root = scratchRoot('reviewed-parse')
  const path = 'src/widgets/Broken.stories.tsx'
  writeFixture(root, path)

  const report = await runAudit(root, {
    scanners: [scannerForFinding({
      ruleId: 'controls/parse-error',
      syntax: 'Unexpected token',
      message: 'failed to parse governed story source',
    })],
    ledger: {
      version: 1,
      entries: [],
      exceptions: [],
      reviewedBoundaries: [{ path, kind: 'story', reason: 'reviewed test boundary' }],
    },
  })

  assert.deepEqual(
    report.errors.map(({ code, path: errorPath, ruleId }) => ({ code, path: errorPath, ruleId })),
    [{ code: 'parse-failure', path, ruleId: 'controls/parse-error' }],
  )
})

test('default scanning fails when canonical src is missing even if an optional package src exists', async () => {
  const root = scratchRoot('missing-canonical-src')
  writeFixture(root, 'packages/ui/src/index.tsx')

  const report = await runAudit(root)

  assert.equal(report.ok, false)
  assert.equal(report.scannedFileCount, 0)
  assert.deepEqual(
    report.errors.map(({ code }) => code),
    ['missing-source-tree'],
  )
})

test('a missing live token policy is a fatal setup error while an explicit empty policy remains valid', async () => {
  const root = scratchRoot('missing-policy')
  writeFixture(root, 'src/clean.tsx')

  const missing = await audit({
    root,
    checkpoint: 'B',
    baseline: EMPTY_BASELINE,
    ledger: EMPTY_LEDGER,
    scanners: [markerScanner()],
  })
  const explicitEmpty = await runAudit(root)

  assert.deepEqual(missing.errors.map(({ code }) => code), ['invalid-policy'])
  assert.equal(missing.scannedFileCount, 0)
  assert.deepEqual(explicitEmpty.errors, [])
})

test('governed source extensions are matched case-insensitively', async () => {
  const root = scratchRoot('uppercase-extension')
  writeFixture(root, 'src/View.TSX', 'export const VIOLATION = true\n')

  const report = await runAudit(root)

  assert.equal(report.scannedFileCount, 1)
  assert.deepEqual(report.errors.map(({ code, path }) => ({ code, path })), [
    { code: 'new-debt', path: 'src/View.TSX' },
  ])
})

test('source symlinks are rejected without reading outside the audit root', async (t) => {
  const root = scratchRoot('external-symlink')
  mkdirSync(resolve(root, 'src'), { recursive: true })
  const outside = resolve(SCRATCH_PARENT, 'outside-source.tsx')
  t.after(() => rmSync(outside, { force: true }))
  writeFixture(SCRATCH_PARENT, 'outside-source.tsx', 'export const VIOLATION = true\n')
  symlinkSync(outside, resolve(root, 'src/linked.tsx'))

  const report = await runAudit(root)

  assert.equal(report.scannedFileCount, 0)
  assert.deepEqual(report.errors.map(({ code, path }) => ({ code, path })), [
    { code: 'source-filesystem', path: 'src/linked.tsx' },
  ])
})

test('broken source links become structured blocking errors instead of rejected audit promises', async () => {
  const root = scratchRoot('broken-symlink')
  mkdirSync(resolve(root, 'src'), { recursive: true })
  symlinkSync(resolve(root, 'absent.tsx'), resolve(root, 'src/broken.tsx'))

  let report
  await assert.doesNotReject(async () => { report = await runAudit(root) })
  assert.equal(report.ok, false)
  assert.deepEqual(report.errors.map(({ code, path }) => ({ code, path })), [
    { code: 'source-filesystem', path: 'src/broken.tsx' },
  ])
})

test('a symbolic-link canonical source root is a structured source error', async () => {
  const root = scratchRoot('symlink-source-root')
  const target = resolve(root, 'real-src')
  mkdirSync(target, { recursive: true })
  symlinkSync(target, resolve(root, 'src'))

  const report = await runAudit(root)

  assert.deepEqual(report.errors.map(({ code, path }) => ({ code, path })), [
    { code: 'source-filesystem', path: 'src' },
  ])
})

test('a broken optional package source root is not silently treated as absent', async () => {
  const root = scratchRoot('broken-optional-root')
  mkdirSync(resolve(root, 'src'), { recursive: true })
  mkdirSync(resolve(root, 'packages/ui'), { recursive: true })
  symlinkSync(resolve(root, 'absent-package-src'), resolve(root, 'packages/ui/src'))

  const report = await runAudit(root)

  assert.deepEqual(report.errors.map(({ code, path }) => ({ code, path })), [
    { code: 'source-filesystem', path: 'packages/ui/src' },
  ])
})

// The 1.5s deadlock watchdog starts after module loading. Otherwise CPU-heavy
// parallel suites measure interpreter/bootstrap latency instead of FIFO safety.
async function runFifoProbe(root, unsafeRead = false) {
  const moduleUrl = new URL('../../scripts/design-system-locks/audit.mjs', import.meta.url).href
  const script = `
    import { audit } from ${JSON.stringify(moduleUrl)};
    import { readFileSync } from 'node:fs';
    import { once } from 'node:events';
    process.send('ready');
    await once(process, 'message');
    if (${JSON.stringify(unsafeRead)}) readFileSync(${JSON.stringify(resolve(root, 'src/stream.tsx'))});
    const report = await audit({
      root: ${JSON.stringify(root)}, checkpoint: 'B',
      baseline: {version:1,fingerprints:[]}, ledger: {version:1,entries:[],exceptions:[]},
      policy: {tokenCssNames:[],resolvedTokens:{}},
      scanners: [{label:'fifo-probe',extensions:['.tsx'],scan(){return []}}],
    });
    process.stdout.write(JSON.stringify(report));
    process.disconnect();
  `
  return new Promise((resolveResult, reject) => {
    const child = spawn(process.execPath, ['--input-type=module', '-e', script], {
      stdio: ['ignore', 'pipe', 'pipe', 'ipc'],
    })
    let stdout = ''; let stderr = ''; let timeoutPhase = null
    const expire = (phase) => { timeoutPhase = phase; child.kill('SIGKILL') }
    let timer = setTimeout(() => expire('startup'), 10_000)
    child.stdout.on('data', (data) => { stdout += data })
    child.stderr.on('data', (data) => { stderr += data })
    child.once('message', (message) => {
      assert.equal(message, 'ready')
      clearTimeout(timer)
      timer = setTimeout(() => expire('operation'), 1500)
      child.send('start')
    })
    child.once('error', (error) => { clearTimeout(timer); reject(error) })
    child.once('close', (status, signal) => {
      clearTimeout(timer)
      resolveResult({ status, signal, stdout, stderr, timeoutPhase })
    })
  })
}

function fifoFixture(name) {
  const root = scratchRoot(name)
  mkdirSync(resolve(root, 'src'), { recursive: true })
  const made = spawnSync('mkfifo', [resolve(root, 'src/stream.tsx')], { encoding: 'utf8' })
  assert.equal(made.status, 0, made.stderr)
  return root
}

test('a governed FIFO is rejected as non-regular before the audit can read it', async () => {
  const result = await runFifoProbe(fifoFixture('fifo-source'))
  assert.equal(result.timeoutPhase, null, result.stderr)
  assert.equal(result.status, 0, result.stderr)
  const report = JSON.parse(result.stdout)
  assert.deepEqual(report.errors.map(({ code, path }) => ({ code, path })), [
    { code: 'source-filesystem', path: 'src/stream.tsx' },
  ])
})

test('the FIFO watchdog kills an actual blocking read after readiness', async () => {
  const result = await runFifoProbe(fifoFixture('fifo-watchdog-control'), true)
  assert.deepEqual({ status: result.status, signal: result.signal, phase: result.timeoutPhase },
    { status: null, signal: 'SIGKILL', phase: 'operation' })
})
