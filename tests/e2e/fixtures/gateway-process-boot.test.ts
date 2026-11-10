// gateway-process-boot.test.ts — the per-test gateway's boot failure is
// PROVEN, not inferred (CI "E2E — preview-isolation", 2026-11-10 attempt 1:
// "/health never returned 200 after 75 consecutive failed probes" with no
// gateway output anywhere in the job). A failed boot must put the gateway's
// own stdout/stderr tail and its panic-log tail (scrubbed) into the job output
// and the failure message, really stop the failed process (exit observed, port
// released) BEFORE retrying once on a fresh home, say loudly that the retry
// was used, and fail with every attempt's report if the retry fails too.
// Onboarding and login are never retried.
//
// The "gateway" here is a small fake executable (a Node script) driven by
// environment variables, so each case is deterministic and needs no build:
//   FAKE_MARKER   a line it writes to stderr (proves the tail is the gateway's)
//   FAKE_MODE     which boot behaviour to play (see the script)
//   FAKE_STATE    a folder where every boot appends its pid; a later boot
//                 records which earlier boots were still alive when it started

import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { GatewayProcess } from './gateway-process.js'

const FAKE_SOURCE = `#!/usr/bin/env node
const fs = require('node:fs')
const http = require('node:http')
const path = require('node:path')
const home = process.env.OMNIPUS_HOME
const state = process.env.FAKE_STATE
const port = JSON.parse(fs.readFileSync(path.join(home, 'config.json'), 'utf8')).gateway.port
const pidsFile = path.join(state, 'pids')
const earlier = fs.existsSync(pidsFile) ? fs.readFileSync(pidsFile, 'utf8').split('\\n').filter(Boolean).map(Number) : []
const boot = earlier.length + 1
fs.appendFileSync(pidsFile, process.pid + '\\n')
if (boot >= 2) {
  const alive = earlier.filter((p) => { try { process.kill(p, 0); return true } catch { return false } })
  fs.writeFileSync(path.join(state, 'overlap-boot' + boot), alive.join(',') || 'none')
}
const mode = process.env.FAKE_MODE
const announce = () => process.stdout.write('fake gateway boot ' + boot + ' announcing localhost:' + port + '\\n')
const ignoreTerm = () => process.on('SIGTERM', () => {})
const serve = (health) => http.createServer((req, res) => {
  if (req.url === '/health') { res.writeHead(health); res.end('x'); return }
  if (req.url === '/api/v1/onboarding/complete') { res.writeHead(200, { 'content-type': 'application/json' }); res.end('{}'); return }
  if (req.url === '/api/v1/auth/login') {
    if (mode === 'login-fails') { res.writeHead(500); res.end('login broken on purpose'); return }
    res.writeHead(200, { 'content-type': 'application/json', 'set-cookie': 'csrf=fake-csrf; Path=/' })
    res.end('{}')
    return
  }
  res.writeHead(404); res.end()
}).listen(port)
const line = process.env.FAKE_MARKER + ' boot=' + boot + ' port=' + port + ' pid=' + process.pid + ' home=' + home
if (mode === 'silent-ignore-term-then-ok' && boot === 1) {
  ignoreTerm() // never announces the port, survives SIGTERM
  setInterval(() => {}, 1000)
} else {
  announce()
  process.stderr.write(line + '\\n')
  if (mode === 'exit') process.exit(3)
  if (mode === 'panic') {
    fs.mkdirSync(path.join(home, 'logs'), { recursive: true })
    fs.appendFileSync(path.join(home, 'logs', 'gateway_panic.log'),
      'PANIC OCCURRED: ' + process.env.FAKE_MARKER + ' boot=' + boot + ' key=' + process.env.FAKE_PROVIDER_API_KEY + '\\n')
    process.stdout.write('provider said Bearer ' + process.env.FAKE_PROVIDER_API_KEY + '\\n')
    process.exit(2)
  }
  if (mode === 'listen-503-ignore-term') { ignoreTerm(); serve(503) }
  else if (mode === 'listen-on-second' && boot >= 2) serve(200)
  else if (mode === 'silent-ignore-term-then-ok' || mode === 'login-fails') serve(200)
  else setInterval(() => {}, 1000) // alive, never listening
  if (mode !== 'listen-503-ignore-term') process.on('SIGTERM', () => process.exit(0))
}
`

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'gwproc-boot-test-'))
const fake = path.join(scratch, 'fake-omnipus.cjs')
fs.writeFileSync(fake, FAKE_SOURCE, { mode: 0o755 })

afterAll(() => {
  fs.rmSync(scratch, { recursive: true, force: true })
})

let errors: string[] = []
beforeEach(() => {
  errors = []
  vi.spyOn(console, 'error').mockImplementation((...args: unknown[]) => {
    errors.push(args.map(String).join(' '))
  })
})
afterEach(() => {
  vi.restoreAllMocks()
})

function newState(name: string): string {
  const dir = path.join(scratch, name)
  fs.mkdirSync(dir)
  return dir
}

function pids(state: string): number[] {
  return fs.readFileSync(path.join(state, 'pids'), 'utf8').split('\n').filter(Boolean).map(Number)
}

function isAlive(pid: number): boolean {
  try {
    process.kill(pid, 0)
    return true
  } catch {
    return false
  }
}

/** Whether something accepts a TCP connection on the loopback port. */
function portAnswers(port: number): Promise<boolean> {
  return new Promise((resolve) => {
    const sock = net.connect({ host: '127.0.0.1', port })
    sock.once('connect', () => {
      sock.destroy()
      resolve(true)
    })
    sock.once('error', () => resolve(false))
  })
}

function bootLines(text: string, marker: string): { port: string; pid: string; home: string }[] {
  return [...text.matchAll(new RegExp(`${marker} boot=\\d+ port=(\\d+) pid=(\\d+) home=(\\S+)`, 'g'))].map((m) => ({
    port: m[1],
    pid: m[2],
    home: m[3],
  }))
}

async function startError(opts: Parameters<typeof GatewayProcess.start>[0]): Promise<Error> {
  const err = await GatewayProcess.start(opts).then(
    () => undefined,
    (e: unknown) => e as Error,
  )
  expect(err, 'start() must fail').toBeInstanceOf(Error)
  return err as Error
}

const RETRY_LINE = 'GATEWAY BOOT RETRY USED'

describe('GatewayProcess boot failure evidence and retry', () => {
  it('never listens: retries once, then fails with BOTH boots\' own output tails', async () => {
    const marker = `NEVER-LISTENS-${process.pid}`
    const state = newState('never-listens')
    const retries: string[] = []
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'never-listen', FAKE_STATE: state },
      onBootRetry: (report) => retries.push(report),
    })
    const message = err.message
    expect(message).toContain('/health never returned 200')
    const boots = bootLines(message, marker)
    expect(boots, 'the failure message carries each boot\'s own stderr line').toHaveLength(2)
    expect(boots[0].home, 'the retry runs in a fresh OMNIPUS_HOME').not.toBe(boots[1].home)
    expect(message).toContain('gateway output')
    expect(retries, 'exactly one retry is reported').toHaveLength(1)
    expect(bootLines(retries[0], marker).map((b) => b.pid), 'the retry report carries the failed boot\'s output').toEqual([boots[0].pid])
    expect(errors.filter((l) => l.includes(RETRY_LINE)), 'the retry is announced on a greppable line').toHaveLength(1)
    for (const b of boots) expect(fs.existsSync(b.home), `the OMNIPUS_HOME ${b.home} is removed`).toBe(false)
  }, 60_000)

  it('exits during boot: the failure names the exit code and carries the output tail', async () => {
    const marker = `EXITS-${process.pid}`
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'exit', FAKE_STATE: newState('exits') },
    })
    expect(err.message).toMatch(/code 3/)
    expect(bootLines(err.message, marker)).toHaveLength(2)
  }, 60_000)

  it('a boot that fails once and then answers: start() succeeds, and the retry is reported and announced', async () => {
    const marker = `SECOND-BOOT-OK-${process.pid}`
    const retries: string[] = []
    const gw = await GatewayProcess.start({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'listen-on-second', FAKE_STATE: newState('second-ok') },
      onBootRetry: (report) => retries.push(report),
    })
    try {
      const health = await fetch(`${gw.baseURL}/health`)
      expect(health.status, 'the returned gateway is the healthy second boot').toBe(200)
      expect(retries).toHaveLength(1)
      expect(retries[0]).toContain('/health never returned 200')
      expect(retries[0]).toContain(`${marker} boot=1`)
      const loud = errors.filter((l) => l.includes(RETRY_LINE))
      expect(loud.length, 'the retry and the recovery are both announced').toBe(2)
      expect(loud.some((l) => /recovered on attempt 2\/2/.test(l))).toBe(true)
    } finally {
      await gw.stop()
    }
  }, 60_000)

  it('a failed boot that ignores SIGTERM is killed, its exit observed and its port released before the retry', async () => {
    const marker = `IGNORES-TERM-${process.pid}`
    const state = newState('ignores-term')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'listen-503-ignore-term', FAKE_STATE: state },
    })
    expect(err.message).toContain('/health never returned 200')
    const boots = bootLines(err.message, marker)
    expect(boots).toHaveLength(2)
    expect(fs.readFileSync(path.join(state, 'overlap-boot2'), 'utf8'), 'boot 1 had exited before boot 2 started').toBe('none')
    for (const pid of pids(state)) expect(isAlive(pid), `gateway pid ${pid} has exited`).toBe(false)
    for (const b of boots) {
      expect(await portAnswers(Number(b.port)), `port ${b.port} is released`).toBe(false)
      expect(fs.existsSync(b.home), `the OMNIPUS_HOME ${b.home} is removed`).toBe(false)
    }
  }, 90_000)

  it('a boot that fails before announcing its port (and ignores SIGTERM) is still owned, killed and gone before the retry', async () => {
    const marker = `PRE-ANNOUNCE-${process.pid}`
    const state = newState('pre-announce')
    const retries: string[] = []
    const gw = await GatewayProcess.start({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      startupTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'silent-ignore-term-then-ok', FAKE_STATE: state },
      onBootRetry: (report) => retries.push(report),
    })
    try {
      expect(retries).toHaveLength(1)
      expect(retries[0]).toContain('did not start within')
      expect(fs.readFileSync(path.join(state, 'overlap-boot2'), 'utf8'), 'boot 1 had exited before boot 2 started').toBe('none')
      const [first] = pids(state)
      expect(isAlive(first), `the silent first boot (pid ${first}) has exited`).toBe(false)
    } finally {
      await gw.stop()
    }
    for (const pid of pids(state)) expect(isAlive(pid), `gateway pid ${pid} has exited after stop()`).toBe(false)
  }, 90_000)

  it('a crashing boot: the report carries the panic-log tail, with secrets scrubbed', async () => {
    const marker = `PANICS-${process.pid}`
    const secret = 'fake-provider-key-for-scrub-test-7f3a9c2e' // gitleaks:allow
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'panic', FAKE_STATE: newState('panics'), FAKE_PROVIDER_API_KEY: secret },
    })
    expect(err.message).toMatch(/code 2/)
    expect(err.message).toContain('gateway_panic.log')
    expect(err.message).toContain(`PANIC OCCURRED: ${marker} boot=1`)
    expect(err.message).toContain(`PANIC OCCURRED: ${marker} boot=2`)
    expect(err.message, 'no secret reaches the report').not.toContain(secret)
    expect(err.message).toContain('[REDACTED')
    for (const l of errors) expect(l, 'no secret reaches the job output').not.toContain(secret)
  }, 60_000)

  it('a failed login is not a boot failure: no retry, one process, and it is gone', async () => {
    const marker = `LOGIN-FAILS-${process.pid}`
    const state = newState('login-fails')
    const retries: string[] = []
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'login-fails', FAKE_STATE: state },
      onBootRetry: (report) => retries.push(report),
    })
    expect(err.message).toContain('login failed 500')
    expect(retries).toEqual([])
    expect(errors.filter((l) => l.includes(RETRY_LINE))).toEqual([])
    expect(pids(state)).toHaveLength(1)
    expect(isAlive(pids(state)[0])).toBe(false)
  }, 60_000)
})
