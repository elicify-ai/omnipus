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
  process.exit(6)
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
  else if (mode === 'login-fails' || mode === 'restart-leak' || mode === 'restart-chain') serve(200)
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
    const home = /OMNIPUS_HOME (\S+)/.exec(err.message)?.[1]
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

describe('GatewayProcess boot failure: no secret in anything it emits', () => {
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

  it('a known secret cut by the start of the panic-log read window is scrubbed', async () => {
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
})
