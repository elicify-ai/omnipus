// Behavioural tests for scripts/design-system-locks/lint.mjs::runLint, driven
// through its dependency-injection seams (spawn / stdout / stderr), plus unit
// tests for spawnStep (spawnSync injected).
//
// RED evidence (ds-safety-net round 3): against the PRE-FIX lint.mjs, the
// signal case, the missing-report case and the malformed-report case fail —
// the pre-fix code silently coerced a signal kill to exit 1 with no signal
// message, printed nothing extra for a missing report, and let
// summarizeAuditErrors print a false "0 error(s)" count line for a report
// lacking an errors array. The lint.mjs fixes turn exactly those cases green.

import assert from 'node:assert/strict'
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join, resolve } from 'node:path'
import test from 'node:test'

import { CHECKPOINTS } from '../../scripts/design-system-locks/audit.mjs'
import { runLint, spawnStep } from '../../scripts/design-system-locks/lint.mjs'

const POLICY_OK = Object.freeze({ status: 0, failed: false, stdout: '{"tokenCssNames":[],"resolvedTokens":{}}' })

function fakeStream() {
  const chunks = []
  return {
    write(text) { chunks.push(String(text)); return true },
    text: () => chunks.join(''),
  }
}

// A minimal valid lint root: the only file runLint itself reads outside the
// two spawned steps is design-system/enforcement/contract.json (checkpoint
// source of truth).
function makeRoot() {
  const root = mkdtempSync(join(tmpdir(), 'dsl-lint-'))
  mkdirSync(resolve(root, 'design-system/enforcement'), { recursive: true })
  writeFileSync(
    resolve(root, 'design-system/enforcement/contract.json'),
    JSON.stringify({ currentCheckpoint: CHECKPOINTS[0] }),
  )
  return root
}

function writeAuditReport(root, report) {
  mkdirSync(resolve(root, 'test-results'), { recursive: true })
  writeFileSync(resolve(root, 'test-results/design-system-audit.json'), JSON.stringify(report))
}

// Steps are consumed in spawn order: steps[0] = policy.mjs, steps[1] = audit.mjs.
function fakeSpawn(steps) {
  const calls = []
  return {
    calls,
    spawn(file, args, opts) {
      calls.push({ file, args, opts })
      const index = calls.length - 1
      if (index >= steps.length) throw new Error(`unexpected spawn #${index + 1}: ${file}`)
      return steps[index]
    },
  }
}

test('runLint: policy failure -> non-zero exit, no audit summary on stdout', () => {
  const root = makeRoot()
  try {
    const stdout = fakeStream()
    const stderr = fakeStream()
    const { spawn } = fakeSpawn([{ status: 3, failed: true }])
    const exit = runLint({ root, stdout, stderr, spawn })
    assert.equal(exit, 3)
    assert.match(stderr.text(), /policy\.mjs failed \(exit 3\)/)
    assert.equal(stdout.text(), '')
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('runLint: audit failure with a well-formed report -> summary lines plus count line, non-zero', () => {
  const root = makeRoot()
  try {
    const stdout = fakeStream()
    const stderr = fakeStream()
    writeAuditReport(root, {
      ok: false, mode: 'audit', checkpoint: CHECKPOINTS[0], scannedFileCount: 1, errorCount: 1,
      errors: [{ code: 'new-debt', path: 'src/Bad.tsx', message: 'raw button' }],
    })
    const { spawn } = fakeSpawn([POLICY_OK, { status: 1, failed: true, stdout: '' }])
    const exit = runLint({ root, stdout, stderr, spawn })
    assert.equal(exit, 1)
    assert.match(stderr.text(), /audit\.mjs failed \(exit 1\)/)
    assert.match(stdout.text(), /\[new-debt\] src\/Bad\.tsx: raw button/)
    assert.match(stdout.text(), /1 error\(s\)/)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('runLint: audit failure with NO report file -> non-zero, explicit missing-report message, no count line', () => {
  const root = makeRoot()
  try {
    const stdout = fakeStream()
    const stderr = fakeStream()
    const { spawn } = fakeSpawn([POLICY_OK, { status: 1, failed: true, stdout: '' }])
    const exit = runLint({ root, stdout, stderr, spawn })
    assert.equal(exit, 1)
    assert.match(stderr.text(), /audit failed but its report is missing or malformed/)
    assert.doesNotMatch(stdout.text(), /error\(s\)/)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('runLint: audit failure with a report lacking an errors array -> non-zero, malformed message, no false "0 error(s)" line', () => {
  const root = makeRoot()
  try {
    const stdout = fakeStream()
    const stderr = fakeStream()
    writeAuditReport(root, { ok: false, mode: 'audit', errorCount: 2 })
    const { spawn } = fakeSpawn([POLICY_OK, { status: 2, failed: true, stdout: '' }])
    const exit = runLint({ root, stdout, stderr, spawn })
    assert.equal(exit, 2)
    assert.match(stderr.text(), /audit failed but its report is missing or malformed/)
    assert.doesNotMatch(stdout.text(), /error\(s\)/)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('runLint: child killed by signal -> non-zero, signal message, no misleading generic failure line', () => {
  const root = makeRoot()
  try {
    const stdout = fakeStream()
    const stderr = fakeStream()
    const fakeSync = () => ({ status: null, signal: 'SIGKILL', stdout: '', stderr: '' })
    const spawn = (file, args, opts) => spawnStep(file, args, { ...opts, spawnSyncImpl: fakeSync })
    const exit = runLint({ root, stdout, stderr, spawn })
    assert.equal(exit, 1)
    assert.match(stderr.text(), /was killed by signal SIGKILL — the step did not complete; this is not an audit finding/)
    assert.doesNotMatch(stderr.text(), /failed \(exit/)
    assert.doesNotMatch(stdout.text(), /error\(s\)/)
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('runLint: success -> exit 0, no output on either stream', () => {
  const root = makeRoot()
  try {
    const stdout = fakeStream()
    const stderr = fakeStream()
    const { spawn } = fakeSpawn([POLICY_OK, { status: 0, failed: false, stdout: '' }])
    const exit = runLint({ root, stdout, stderr, spawn })
    assert.equal(exit, 0)
    assert.equal(stderr.text(), '')
    assert.equal(stdout.text(), '')
  } finally {
    rmSync(root, { recursive: true, force: true })
  }
})

test('spawnStep: signal kill -> killed message on the given stderr, failure shape carrying the signal', () => {
  const stderr = fakeStream()
  const fakeSync = () => ({ status: null, signal: 'SIGTERM', stdout: 'partial out', stderr: 'partial err' })
  const step = spawnStep('/tmp/policy.mjs', [], { stderr, spawnSyncImpl: fakeSync })
  assert.equal(step.status, 1)
  assert.equal(step.failed, true)
  assert.equal(step.signal, 'SIGTERM')
  assert.equal(step.stdout, 'partial out')
  assert.match(stderr.text(), /\/tmp\/policy\.mjs was killed by signal SIGTERM — the step did not complete; this is not an audit finding/)
})

test('spawnStep: spawn error (result.error) -> non-zero failure shape, cannot-spawn message', () => {
  const stderr = fakeStream()
  const fakeSync = () => ({ error: new Error('spawn ENOENT'), status: null, signal: null })
  const step = spawnStep('/tmp/absent.mjs', [], { stderr, spawnSyncImpl: fakeSync })
  assert.equal(step.status, 1)
  assert.equal(step.failed, true)
  assert.equal(step.signal, undefined)
  assert.match(stderr.text(), /cannot spawn \/tmp\/absent\.mjs: spawn ENOENT/)
})

test('spawnStep: normal exit 0 -> clean shape, nothing written to stderr', () => {
  const stderr = fakeStream()
  const fakeSync = () => ({ status: 0, signal: null, stdout: '{"ok":true}', stderr: '' })
  const step = spawnStep('/tmp/policy.mjs', [], { stderr, spawnSyncImpl: fakeSync })
  assert.deepEqual(step, { status: 0, failed: false, stdout: '{"ok":true}' })
  assert.equal(stderr.text(), '')
})

