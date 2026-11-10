// gateway-process-boot.test.ts — a per-test gateway boot failure FAILS the
// test, with the cause PROVEN, not inferred (CI "E2E — preview-isolation",
// 2026-11-10 attempt 1: "/health never returned 200 after 75 consecutive
// failed probes" with no gateway output anywhere in the job). A failed boot
// must:
//   - put the gateway's own stdout/stderr tail and panic-log tail into the
//     job output and the thrown error, with secrets scrubbed everywhere in
//     the emitted error (no raw output under a `cause`);
//   - really stop the failed process (exit observed, port released, home
//     removed only afterwards), including one that never announced its port
//     or ignores SIGTERM;
//   - never be retried (an unclassified first-boot crash must not pass);
//   - keep the boot failure when cleanup fails too.
// Onboarding and login are never retried either.
//
// The "gateway" here is a small fake executable (a Node script) driven by
// environment variables, so each case is deterministic and needs no build:
//   FAKE_MARKER   a line it writes to stderr (proves the tail is the gateway's)
//   FAKE_MODE     which boot behaviour to play (see the script)
//   FAKE_STATE    a folder where every boot appends its pid

import fs from 'node:fs'
import net from 'node:net'
import os from 'node:os'
import path from 'node:path'
import { inspect } from 'node:util'
import { afterAll, afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { GatewayProcess, REPORT_TAIL_BYTES } from './gateway-process.js'

const FAKE_SOURCE = `#!/usr/bin/env node
const fs = require('node:fs')
const http = require('node:http')
const path = require('node:path')
const home = process.env.OMNIPUS_HOME
const state = process.env.FAKE_STATE
const port = JSON.parse(fs.readFileSync(path.join(home, 'config.json'), 'utf8')).gateway.port
const pidsFile = path.join(state, 'pids')
const boot = (fs.existsSync(pidsFile) ? fs.readFileSync(pidsFile, 'utf8').split('\\n').filter(Boolean).length : 0) + 1
fs.appendFileSync(pidsFile, process.pid + '\\n')
const mode = process.env.FAKE_MODE
const env = process.env
const announce = () => process.stdout.write('fake gateway boot ' + boot + ' announcing localhost:' + port + '\\n')
const ignoreTerm = () => process.on('SIGTERM', () => {})
const panicLog = (text) => {
  fs.mkdirSync(path.join(home, 'logs'), { recursive: true })
  fs.appendFileSync(path.join(home, 'logs', 'gateway_panic.log'), text)
}
const readBody = (req, cb) => { let b = ''; req.on('data', (c) => { b += c }); req.on('end', () => cb(b)) }
const serve = (health) => http.createServer((req, res) => {
  if (req.url === '/health') { res.writeHead(health); res.end('x'); return }
  if (req.url === '/api/v1/onboarding/complete' && mode === 'onboard-refuse-bare') {
    // The provider refuses the key it was sent, echoing it bare.
    readBody(req, (b) => { res.writeHead(500); res.end('provider refused ' + JSON.parse(b).provider.api_key + ' terminal') })
    return
  }
  if (req.url === '/api/v1/onboarding/complete' && mode === 'onboard-huge') {
    readBody(req, () => { res.writeHead(400); res.end('x'.repeat(2 * 1024 * 1024)) })
    return
  }
  if (mode === 'fallback-refuse-bare') {
    // Onboarding and re-auth succeed; saving the fallback key fails, echoing it bare.
    if (req.url === '/api/v1/onboarding/complete') { readBody(req, () => { res.writeHead(200, { 'content-type': 'application/json' }); res.end('{"token":"fake-onboard-token"}') }); return }
    if (req.url === '/api/v1/auth/reauth') { readBody(req, () => { res.writeHead(200, { 'content-type': 'application/json' }); res.end('{"token":"fake-reauth-token"}') }); return }
    if (req.method === 'PUT' && req.url.startsWith('/api/v1/providers/')) {
      readBody(req, (b) => { res.writeHead(500); res.end('provider refused ' + JSON.parse(b).api_key + ' terminal') })
      return
    }
  }
  if (req.url === '/api/v1/onboarding/complete') {
    if (mode === 'onboard-echo') {
      // A 400 that echoes the request (it carries the provider key).
      let body = ''
      req.on('data', (c) => { body += c })
      req.on('end', () => { res.writeHead(400); res.end('bad request: ' + body) })
      return
    }
    res.writeHead(200, { 'content-type': 'application/json' }); res.end('{}'); return
  }
  if (req.url === '/api/v1/auth/login') {
    if (mode === 'login-fails') { res.writeHead(500); res.end('login broken on purpose'); return }
    if (mode === 'login-echo-secret') { res.writeHead(500); res.end('denied for ' + env.FAKE_LOGIN_TOKEN); return }
    res.writeHead(200, { 'content-type': 'application/json', 'set-cookie': 'csrf=fake-csrf; Path=/' })
    res.end('{}')
    return
  }
  res.writeHead(404); res.end()
}).listen(port)
const line = env.FAKE_MARKER + ' boot=' + boot + ' port=' + port + ' pid=' + process.pid + ' home=' + home
if (mode === 'silent-ignore-term') {
  ignoreTerm() // never announces the port, survives SIGTERM
  setInterval(() => {}, 1000)
} else if (mode === 'exit-before-announce') {
  // Raw output only (no announcement), then a crash: the startup error
  // itself carries this output.
  process.stdout.write(line + ' leaked=' + env.FAKE_PROVIDER_API_KEY + '\\n')
  process.exit(7)
} else if (mode === 'leak') {
  announce()
  process.stderr.write(line + '\\n')
  // Not key=value shaped: only the env-value rule can catch these.
  process.stdout.write('short value ' + env.FAKE_SHORT_TOKEN + ' end\\n')
  process.stdout.write('config ' + JSON.stringify({ db: { password: env.FAKE_DB_PASSWORD } }) + '\\n')
  // A known secret on a line that straddles the start of the panic-log read
  // window: the window starts in the middle of the secret.
  const secret = env.FAKE_EDGE_SECRET
  const half = Math.floor(secret.length / 2)
  let rest = Number(env.FAKE_WINDOW) - (secret.length - half) - 1
  let tail = ''
  while (rest > 0) { const n = Math.min(1000, rest); tail += 'f'.repeat(n - 1) + '\\n'; rest -= n }
  panicLog('head ' + 'h'.repeat(5000) + ' edge ' + secret + '\\n' + tail)
  process.exit(5)
} else if (mode === 'ml-edge') {
  // A known THREE-line secret whose first line straddles the start of the
  // last FAKE_WINDOW bytes of the panic log.
  announce()
  const secret = env.FAKE_ML_SECRET
  const half = Math.floor(secret.split('\\n')[0].length / 2)
  let rest = Number(env.FAKE_WINDOW) - (secret.length - half) - 1
  let tail = ''
  while (rest > 0) { const n = Math.min(1000, rest); tail += 'f'.repeat(n - 1) + '\\n'; rest -= n }
  panicLog('head ' + 'h'.repeat(5000) + ' edge ' + secret + '\\n' + tail)
  process.exit(5)
} else if (mode === 'bare-print') {
  // Values printed bare (no key=value shape), then a crash.
  for (const name of (env.FAKE_PRINT_VARS || '').split(',').filter(Boolean)) {
    process.stdout.write('bare ' + env[name] + ' end\\n')
  }
  if (env.FAKE_PRINT_LINE2) process.stdout.write('line2 ' + env[env.FAKE_PRINT_LINE2].split('\\n')[1] + ' end\\n')
  if (env.FAKE_PRINT_TRIMMED) process.stdout.write('trimmed ' + env[env.FAKE_PRINT_TRIMMED].trim() + ' end\\n')
  process.exit(6)
} else if (mode === 'huge-exit') {
  // Megabytes of output, no announcement, then a crash.
  const chunk = ('noise ' + 'n'.repeat(993) + '\\n').repeat(100)
  for (let i = 0; i < 20; i++) process.stdout.write(chunk)
  process.stdout.write('last words of the huge boot\\n', () => process.exit(8))
} else if (mode === 'restart-chain' && boot >= 2) {
  // Boot 2 (first restart) prints its secret and is healthy; boot 3 (second
  // restart) prints its own and crashes.
  announce()
  process.stderr.write(line + ' restart value ' + env.FAKE_RESTART_TOKEN + '\\n')
  if (boot >= 3) process.exit(4)
  serve(200)
} else if (mode === 'restart-leak' && boot >= 2) {
  announce()
  process.stderr.write(line + ' restart value ' + env.FAKE_RESTART_TOKEN + '\\n')
  process.exit(4)
} else {
  announce()
  process.stderr.write(line + '\\n')
  if (mode === 'exit') process.exit(3)
  if (mode === 'panic') {
    panicLog('PANIC OCCURRED: ' + env.FAKE_MARKER + ' boot=' + boot + ' key=' + env.FAKE_PROVIDER_API_KEY + '\\n')
    process.stdout.write('provider said Bearer ' + env.FAKE_PROVIDER_API_KEY + '\\n')
    process.exit(2)
  }
  if (mode === 'listen-503-ignore-term') { ignoreTerm(); serve(503) }
  else if (['login-fails', 'login-echo-secret', 'onboard-echo', 'onboard-refuse-bare', 'onboard-huge', 'fallback-refuse-bare', 'restart-leak', 'restart-chain'].includes(mode)) serve(200)
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

async function startError(
  opts: Parameters<typeof GatewayProcess.start>[0],
  GP: typeof GatewayProcess = GatewayProcess,
): Promise<Error> {
  let started: GatewayProcess | undefined
  const err = await GP.start(opts).then(
    (gw) => {
      started = gw
      return undefined
    },
    (e: unknown) => e as Error,
  )
  // An unexpectedly healthy gateway is stopped before the assertion fails,
  // so a failing case never leaves a process or home behind.
  if (started) await started.stop()
  expect(err, 'start() must fail').toBeInstanceOf(Error)
  return err as Error
}

/**
 * GatewayProcess loaded afresh with the central model pair from the file:
 * OMNIPUS_E2E_PRIMARY_PROVIDER / OMNIPUS_E2E_PRIMARY_MODEL (a CI job moved
 * onto the fallback pair) are cleared first. e2e-model.ts reads them once at
 * load, so the module graph is reloaded. The caller restores the variables
 * with vi.unstubAllEnvs().
 */
async function gatewayProcessWithFilePair(): Promise<typeof GatewayProcess> {
  vi.stubEnv('OMNIPUS_E2E_PRIMARY_PROVIDER', '')
  vi.stubEnv('OMNIPUS_E2E_PRIMARY_MODEL', '')
  vi.resetModules()
  const fresh = await import('./gateway-process.js')
  return fresh.GatewayProcess
}

/** Everything a reporter can print of an error: message, stack, every
 * nested cause and own property (util.inspect walks the cause chain). */
function emitted(err: unknown): string {
  return inspect(err, { depth: 20, showHidden: true }) + '\n' + errors.join('\n')
}

/** The one boot started, failed and is gone: exit observed, home removed. */
async function expectOneBootGone(state: string, message: string, marker: string): Promise<void> {
  expect(pids(state), 'exactly one boot: a failed boot is never retried').toHaveLength(1)
  expect(isAlive(pids(state)[0]), 'the failed boot has exited').toBe(false)
  for (const b of bootLines(message, marker)) {
    expect(await portAnswers(Number(b.port)), `port ${b.port} is released`).toBe(false)
    expect(fs.existsSync(b.home), `the OMNIPUS_HOME ${b.home} is removed`).toBe(false)
  }
}

describe('GatewayProcess boot failure: fails the test with the proven cause', () => {
  it('never listens: fails once, with the gateway output in the error and the job output', async () => {
    const marker = `NEVER-LISTENS-${process.pid}`
    const state = newState('never-listens')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'never-listen', FAKE_STATE: state },
    })
    expect(err.message).toContain('/health never returned 200')
    expect(err.message).toContain('gateway output')
    expect(bootLines(err.message, marker), 'the error carries the boot\'s own stderr line').toHaveLength(1)
    expect(bootLines(errors.join('\n'), marker), 'the job output carries it too').toHaveLength(1)
    await expectOneBootGone(state, err.message, marker)
  }, 60_000)

  it('exits during boot: the failure names the exit code and carries the output tail', async () => {
    const marker = `EXITS-${process.pid}`
    const state = newState('exits')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'exit', FAKE_STATE: state },
    })
    expect(err.message).toMatch(/code 3/)
    expect(bootLines(err.message, marker)).toHaveLength(1)
    await expectOneBootGone(state, err.message, marker)
  }, 60_000)

  it('a failed boot that ignores SIGTERM is killed: exit observed, port released, home removed', async () => {
    const marker = `IGNORES-TERM-${process.pid}`
    const state = newState('ignores-term')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'listen-503-ignore-term', FAKE_STATE: state },
    })
    expect(err.message).toContain('/health never returned 200')
    expect(bootLines(err.message, marker)).toHaveLength(1)
    await expectOneBootGone(state, err.message, marker)
  }, 60_000)

  it('a boot that never announces its port (and ignores SIGTERM) is still owned and killed', async () => {
    const state = newState('pre-announce')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      startupTimeoutMs: 1_000,
      env: { FAKE_MARKER: 'unused', FAKE_MODE: 'silent-ignore-term', FAKE_STATE: state },
    })
    expect(err.message).toContain('did not start within')
    expect(pids(state)).toHaveLength(1)
    expect(isAlive(pids(state)[0]), 'the silent boot has exited').toBe(false)
    const home = /OMNIPUS_HOME ([^,\s)]+)/.exec(err.message)?.[1]
    expect(home, 'the error names the home').toBeTruthy()
    expect(fs.existsSync(home as string), 'its home is removed').toBe(false)
  }, 60_000)

  it('a crashing boot: the report carries the panic-log tail, with secrets scrubbed', async () => {
    const marker = `PANICS-${process.pid}`
    const secret = 'fake-provider-key-for-scrub-test-7f3a9c2e' // gitleaks:allow
    const state = newState('panics')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'panic', FAKE_STATE: state, FAKE_PROVIDER_API_KEY: secret },
    })
    expect(err.message).toMatch(/code 2/)
    expect(err.message).toContain('gateway_panic.log')
    expect(err.message).toContain(`PANIC OCCURRED: ${marker} boot=1`)
    expect(err.message).toContain('[REDACTED')
    expect(emitted(err), 'no secret reaches the error or the job output').not.toContain(secret)
    await expectOneBootGone(state, err.message, marker)
  }, 60_000)

  it('a failed login is not a boot failure: not retried, one process, and it is gone', async () => {
    const marker = `LOGIN-FAILS-${process.pid}`
    const state = newState('login-fails')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'login-fails', FAKE_STATE: state },
    })
    expect(err.message).toContain('login failed 500')
    await expectOneBootGone(state, err.message, marker)
  }, 60_000)

  it('cleanup failing after a boot failure keeps BOTH failures in the error', async () => {
    const marker = `CLEANUP-FAILS-${process.pid}`
    const state = newState('cleanup-fails')
    const realStop = GatewayProcess.prototype.stop
    const stopSpy = vi
      .spyOn(GatewayProcess.prototype, 'stop')
      .mockRejectedValue(new Error('cleanup canary: the process would not die'))
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'never-listen', FAKE_STATE: state },
    })
    const owned = stopSpy.mock.contexts[0] as GatewayProcess
    vi.restoreAllMocks()
    // The mocked stop() left the fake running: clean it up for real.
    expect(owned, 'stop() was called on the failed boot').toBeInstanceOf(GatewayProcess)
    await realStop.call(owned)
    expect(err.message, 'the boot failure is kept').toContain('/health never returned 200')
    expect(bootLines(err.message, marker), 'with its gateway output').toHaveLength(1)
    expect(err.message, 'and the cleanup failure is added').toContain('cleanup canary: the process would not die')
  }, 60_000)
})

describe('GatewayProcess boot failure: no secret in anything it emits: error graph, value forms, tail window', () => {
  it('a crash before the port announcement: the whole error graph (causes included) is scrubbed', async () => {
    const canary = 'canary-cause-graph-6b1d0e9f4a2c' // gitleaks:allow
    const marker = `CAUSE-${process.pid}`
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'exit-before-announce', FAKE_STATE: newState('cause'), FAKE_PROVIDER_API_KEY: canary },
    })
    expect(err.message).toMatch(/code 7/)
    expect(err.message, 'the raw output is still reported, scrubbed').toContain(`${marker} boot=1`)
    expect(emitted(err)).not.toContain(canary)
  }, 60_000)

  const leakEnv = (state: string, extra: Record<string, string>) => ({
    FAKE_MARKER: `LEAK-${process.pid}`,
    FAKE_MODE: 'leak',
    FAKE_STATE: state,
    FAKE_SHORT_TOKEN: 'q7Z',
    FAKE_DB_PASSWORD: 'pw "json" \\ canary-json-58e2',
    FAKE_EDGE_SECRET: 'edgecanaryAAAAAAAAAAAAAAAAAAAAbbbbbbbbbbbbbbbbbbbbbbbbbbb', // gitleaks:allow
    FAKE_WINDOW: String(REPORT_TAIL_BYTES),
    ...extra,
  })

  it('a short secret-named environment value is scrubbed', async () => {
    const err = await startError({ binary: fake, apiBase: 'http://127.0.0.1:9', healthTimeoutMs: 1_000, env: leakEnv(newState('short'), {}) })
    expect(err.message).toContain('short value ')
    expect(emitted(err)).not.toContain('q7Z')
  }, 60_000)

  it('a known secret printed in JSON-escaped form is scrubbed', async () => {
    const err = await startError({ binary: fake, apiBase: 'http://127.0.0.1:9', healthTimeoutMs: 1_000, env: leakEnv(newState('json'), {}) })
    expect(err.message).toContain('"password":')
    expect(emitted(err)).not.toContain('canary-json-58e2')
  }, 60_000)

  it('a known secret cut by the start of the reported panic-log tail (REPORT_TAIL_BYTES) is scrubbed', async () => {
    const err = await startError({ binary: fake, apiBase: 'http://127.0.0.1:9', healthTimeoutMs: 1_000, env: leakEnv(newState('edge'), {}) })
    expect(err.message).toContain('gateway_panic.log')
    expect(err.message, 'the panic tail itself is there').toContain('ffff')
    expect(emitted(err), 'no part of the secret survives').not.toMatch(/bbbbbbbbbb|AAAAAAAAAA|edgecanary/)
  }, 60_000)

  it('a secret passed only to restart({ env }) is scrubbed from the restart failure', async () => {
    const canary = 'canary-restart-env-3c9a71d0' // gitleaks:allow
    const marker = `RESTART-${process.pid}`
    const gw = await GatewayProcess.start({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'restart-leak', FAKE_STATE: newState('restart') },
    })
    try {
      await gw.kill9()
      const err = await gw.restart({ relogin: false, env: { FAKE_RESTART_TOKEN: canary } }).then(
        () => undefined,
        (e: unknown) => e as Error,
      )
      expect(err, 'the restart must fail').toBeInstanceOf(Error)
      expect((err as Error).message).toMatch(/code 4/)
      expect((err as Error).message).toContain('restart value ')
      expect(emitted(err)).not.toContain(canary)
    } finally {
      await gw.stop()
    }
  }, 60_000)

})

describe('GatewayProcess boot failure: no secret in anything it emits: multi-line, compound-name and restart secrets', () => {
  const ML_SECRET = 'mlcanary-one-AAAAAAAAAAAAAAAAAAAA\nmlcanary-two-BBBBBBBBBBBBBBBBBBBB\nmlcanary-three-CCCCCCCCCCCCCCCCCC' // gitleaks:allow

  it('a three-line secret whose first line is cut by the reported tail window is scrubbed, every line', async () => {
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: {
        FAKE_MARKER: 'ml',
        FAKE_MODE: 'ml-edge',
        FAKE_STATE: newState('ml-edge'),
        FAKE_ML_SECRET: ML_SECRET,
        FAKE_WINDOW: String(REPORT_TAIL_BYTES),
      },
    })
    expect(err.message).toContain('gateway_panic.log')
    expect(err.message, 'the panic tail itself is there').toContain('ffff')
    expect(emitted(err), 'no line of the secret survives').not.toMatch(/AAAAAAAAAA|BBBBBBBBBB|CCCCCCCCCC|mlcanary/)
  }, 60_000)

  it('one line of a multi-line secret printed on its own is scrubbed', async () => {
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: 'l2', FAKE_MODE: 'bare-print', FAKE_STATE: newState('line2'), FAKE_ML_SECRET: ML_SECRET, FAKE_PRINT_LINE2: 'FAKE_ML_SECRET' },
    })
    expect(err.message).toContain('line2 ')
    expect(emitted(err)).not.toContain('mlcanary-two')
  }, 60_000)

  it('a compound credential variable name (PGPASSWORD) printed bare is scrubbed', async () => {
    const canary = 'canary-pgpassword-41d7' // gitleaks:allow
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: 'pg', FAKE_MODE: 'bare-print', FAKE_STATE: newState('pgpassword'), PGPASSWORD: canary, FAKE_PRINT_VARS: 'PGPASSWORD' },
    })
    expect(err.message).toContain('bare ')
    expect(emitted(err)).not.toContain(canary)
  }, 60_000)

  it('a secret from an EARLIER restart is scrubbed when a later restart fails', async () => {
    const first = 'canary-restart-first-8e02c1' // gitleaks:allow
    const second = 'canary-restart-second-5f7ab3' // gitleaks:allow
    const marker = `CHAIN-${process.pid}`
    const gw = await GatewayProcess.start({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'restart-chain', FAKE_STATE: newState('restart-chain') },
    })
    try {
      await gw.kill9()
      await gw.restart({ relogin: false, env: { FAKE_RESTART_TOKEN: first } })
      await gw.kill9()
      const err = await gw.restart({ relogin: false, env: { FAKE_RESTART_TOKEN: second } }).then(
        () => undefined,
        (e: unknown) => e as Error,
      )
      expect(err, 'the second restart must fail').toBeInstanceOf(Error)
      expect((err as Error).message).toMatch(/code 4/)
      expect((err as Error).message, 'the retained output of both restarts is reported').toMatch(/restart value [\s\S]*restart value /)
      expect(emitted(err)).not.toContain(first)
      expect(emitted(err)).not.toContain(second)
    } finally {
      await gw.stop()
    }
  }, 60_000)
  it('a credential ending in CRLF, logged with the line break trimmed, is scrubbed', async () => {
    const canary = 'crlf-canary-value-9d04b2' // gitleaks:allow
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: 'crlf', FAKE_MODE: 'bare-print', FAKE_STATE: newState('crlf'), FAKE_CRLF_TOKEN: `${canary}\r\n`, FAKE_PRINT_TRIMMED: 'FAKE_CRLF_TOKEN' },
    })
    expect(err.message).toContain('trimmed ')
    expect(emitted(err)).not.toContain(canary)
  }, 60_000)

  it('the one long line of a multi-line value with short other lines, printed alone, is scrubbed', async () => {
    const middle = 'r4-long-ml-d110e9ff-canary' // gitleaks:allow
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: 'ml1', FAKE_MODE: 'bare-print', FAKE_STATE: newState('ml-one'), FAKE_ML_SECRET: `x\n${middle}\nz`, FAKE_PRINT_LINE2: 'FAKE_ML_SECRET' },
    })
    expect(err.message).toContain('line2 ')
    expect(emitted(err)).not.toContain(middle)
  }, 60_000)

})

describe('GatewayProcess boot failure: no secret in anything it emits: onboarding, provider keys and size', () => {
  it('a login failure whose response carries a secret: the error is scrubbed (cleanup succeeded)', async () => {
    const canary = 'canary-login-body-27fe90c4' // gitleaks:allow
    const state = newState('login-echo')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: 'le', FAKE_MODE: 'login-echo-secret', FAKE_STATE: state, FAKE_LOGIN_TOKEN: canary },
    })
    expect(err.message).toContain('login failed 500')
    expect(err.message).toContain('denied for ')
    expect(emitted(err)).not.toContain(canary)
    expect(isAlive(pids(state)[0]), 'cleanup succeeded').toBe(false)
  }, 60_000)

  it('a live onboarding failure echoing the CI provider keys: the error is scrubbed (cleanup succeeded)', async () => {
    const primary = 'canary-minimax-ci-key-6a02d9e1' // gitleaks:allow
    const fallback = 'canary-openrouter-ci-key-b37c58f0' // gitleaks:allow
    vi.stubEnv('MINIMAX_API_KEY_CI', primary)
    vi.stubEnv('OPENROUTER_API_KEY_CI', fallback)
    try {
      const GP = await gatewayProcessWithFilePair()
      const state = newState('onboard-echo')
      // No apiBase: the real onboardViaAPI path with the CI key variables.
      const err = await startError({
        binary: fake,
        healthTimeoutMs: 1_000,
        env: { FAKE_MARKER: 'oe', FAKE_MODE: 'onboard-echo', FAKE_STATE: state },
      }, GP)
      expect(err.message).toContain('onboarding/complete returned 400')
      expect(err.message, 'the echoed request is reported, scrubbed').toContain('api_key')
      expect(emitted(err)).not.toContain(primary)
      expect(emitted(err)).not.toContain(fallback)
      expect(isAlive(pids(state)[0]), 'cleanup succeeded').toBe(false)
    } finally {
      vi.unstubAllEnvs()
    }
  }, 60_000)

  it('megabytes of boot output: the error stays bounded and still names the cause', async () => {
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: 'huge', FAKE_MODE: 'huge-exit', FAKE_STATE: newState('huge') },
    })
    expect(err.message).toMatch(/code 8/)
    expect(err.message, 'the output tail is still reported').toContain('last words of the huge boot')
    expect(err.message.length, 'the whole error stays under 512 Ki characters').toBeLessThan(512 * 1024)
  }, 60_000)
  // lookupModelKey trims the env value before it is sent, so a padded key
  // reaches the wire (and an echoing error) without its padding.
  for (const [pad, padName] of [[' ', 'spaces'], ['\t', 'tabs']] as const) {
    for (const [mode, where] of [['onboard-refuse-bare', 'primary key (onboarding)'], ['fallback-refuse-bare', 'fallback key (provider PUT)']] as const) {
      it(`a ${where} padded with ${padName} in the env, echoed bare after trimming, is scrubbed`, async () => {
        const primary = `canary-primary-${padName}-0c5e7a19` // gitleaks:allow
        const fallback = `canary-fallback-${padName}-93d4b6e2` // gitleaks:allow
        vi.stubEnv('MINIMAX_API_KEY_CI', `${pad}${pad}${primary}${pad}${pad}`)
        vi.stubEnv('OPENROUTER_API_KEY_CI', `${pad}${pad}${fallback}${pad}${pad}`)
        try {
          // The file pair: with the fallback pair as primary, onboarding
          // skips the fallback seeding this case exercises.
          const GP = await gatewayProcessWithFilePair()
          const state = newState(`${mode}-${padName}`)
          const err = await startError({
            binary: fake,
            healthTimeoutMs: 1_000,
            env: { FAKE_MARKER: 'pad', FAKE_MODE: mode, FAKE_STATE: state },
          }, GP)
          expect(err.message, 'the provider refusal is reported').toContain('provider refused ')
          expect(emitted(err)).not.toContain(primary)
          expect(emitted(err)).not.toContain(fallback)
          expect(isAlive(pids(state)[0]), 'cleanup succeeded').toBe(false)
        } finally {
          vi.unstubAllEnvs()
        }
      }, 60_000)
    }
  }

  it('a huge onboarding error response: the error stays bounded', async () => {
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: 'oh', FAKE_MODE: 'onboard-huge', FAKE_STATE: newState('onboard-huge') },
    })
    expect(err.message).toContain('onboarding failed 400')
    expect(err.message.length, 'the whole error stays under 512 Ki characters').toBeLessThan(512 * 1024)
  }, 60_000)
})

describe('GatewayProcess boot failure: no secret in anything it emits: malformed values and report failures', () => {
  // An unpaired UTF-16 surrogate is a valid JavaScript string but cannot be
  // URL-encoded: building its secret forms must not throw past cleanup.
  const SURROGATE = 'canary-surrogate-\uD800-7a41' // gitleaks:allow

  it('a malformed-Unicode secret on a boot failure: still reported, the child exited and its home is gone', async () => {
    const marker = `SURR-BOOT-${process.pid}`
    const state = newState('surrogate-boot')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'never-listen', FAKE_STATE: state, FAKE_SURROGATE_TOKEN: SURROGATE },
    })
    expect(err.message).toContain('/health never returned 200')
    expect(bootLines(err.message, marker), 'the report is complete').toHaveLength(1)
    await expectOneBootGone(state, err.message, marker)
  }, 60_000)

  it('a malformed-Unicode secret on an onboarding/login failure: still reported, the child exited and its home is gone', async () => {
    const marker = `SURR-LOGIN-${process.pid}`
    const state = newState('surrogate-login')
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'login-fails', FAKE_STATE: state, FAKE_SURROGATE_TOKEN: SURROGATE },
    })
    expect(err.message).toContain('login failed 500')
    await expectOneBootGone(state, err.message, marker)
  }, 60_000)

  it('a report that cannot be built at all: cleanup still runs and the failure says so', async () => {
    const marker = `REPORT-THROWS-${process.pid}`
    const state = newState('report-throws')
    vi.spyOn(GatewayProcess.prototype as unknown as { bootFailureReport: () => string }, 'bootFailureReport').mockImplementation(() => {
      throw new Error('report canary: formatting broke')
    })
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'never-listen', FAKE_STATE: state },
    })
    expect(err.message).toContain('boot failed')
    expect(err.message).toContain('report canary: formatting broke')
    expect(pids(state)).toHaveLength(1)
    expect(isAlive(pids(state)[0]), 'the child exited').toBe(false)
    const home = /OMNIPUS_HOME ([^,\s)]+)/.exec(err.message)?.[1]
    expect(home, 'the failure names the home').toBeTruthy()
    expect(fs.existsSync(home as string), 'its home is removed').toBe(false)
  }, 60_000)

  it('an onboarding failure whose message cannot be scrubbed: cleanup still runs and the failure says so', async () => {
    const marker = `SCRUB-THROWS-${process.pid}`
    const state = newState('scrub-throws')
    vi.spyOn(GatewayProcess.prototype as unknown as { scrub: () => string }, 'scrub').mockImplementation(() => {
      throw new Error('scrub canary: scrubbing broke')
    })
    const err = await startError({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'login-fails', FAKE_STATE: state },
    })
    expect(err.message).toContain('onboarding/login failed')
    expect(err.message).toContain('scrub canary: scrubbing broke')
    expect(err.message, 'the unscrubbable original is withheld').not.toContain('login broken on purpose')
    expect(pids(state)).toHaveLength(1)
    expect(isAlive(pids(state)[0]), 'the child exited').toBe(false)
  }, 60_000)
})
