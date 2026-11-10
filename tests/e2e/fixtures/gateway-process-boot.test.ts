// gateway-process-boot.test.ts — the per-test gateway's boot failure is
// PROVEN, not inferred (CI "E2E — preview-isolation", 2026-11-10 attempt 1:
// "/health never returned 200 after 75 consecutive failed probes" with no
// gateway output anywhere in the job). A failed boot must put the gateway's
// own stdout/stderr tail into the job output and the failure message, retry
// once on a fresh port and home, and fail with every attempt's tail if the
// retry fails too.
//
// The "gateway" here is a small fake executable (a Node script) driven by
// environment variables, so each case is deterministic and needs no build:
//   FAKE_MARKER   a line it writes to stderr (proves the tail is the gateway's)
//   FAKE_MODE     never-listen | exit | listen-on-second
//   FAKE_COUNTER  a file counting its boots (for listen-on-second)

import fs from 'node:fs'
import os from 'node:os'
import path from 'node:path'
import { afterAll, describe, expect, it } from 'vitest'
import { GatewayProcess } from './gateway-process.js'

const FAKE_SOURCE = `#!/usr/bin/env node
const fs = require('node:fs')
const http = require('node:http')
const path = require('node:path')
const home = process.env.OMNIPUS_HOME
const port = JSON.parse(fs.readFileSync(path.join(home, 'config.json'), 'utf8')).gateway.port
let boot = 1
if (process.env.FAKE_COUNTER) {
  boot = (fs.existsSync(process.env.FAKE_COUNTER) ? Number(fs.readFileSync(process.env.FAKE_COUNTER, 'utf8')) : 0) + 1
  fs.writeFileSync(process.env.FAKE_COUNTER, String(boot))
}
process.stdout.write('fake gateway boot ' + boot + ' announcing localhost:' + port + '\\n')
process.stderr.write(process.env.FAKE_MARKER + ' boot=' + boot + ' port=' + port + '\\n')
const mode = process.env.FAKE_MODE
if (mode === 'exit') process.exit(3)
if (mode === 'listen-on-second' && boot >= 2) {
  http.createServer((req, res) => {
    if (req.url === '/health') { res.writeHead(200); res.end('ok'); return }
    if (req.url === '/api/v1/onboarding/complete') { res.writeHead(200, { 'content-type': 'application/json' }); res.end('{}'); return }
    if (req.url === '/api/v1/auth/login') {
      res.writeHead(200, { 'content-type': 'application/json', 'set-cookie': 'csrf=fake-csrf; Path=/' })
      res.end('{}')
      return
    }
    res.writeHead(404); res.end()
  }).listen(port)
} else {
  setInterval(() => {}, 1000) // alive, never listening
}
process.on('SIGTERM', () => process.exit(0))
`

const scratch = fs.mkdtempSync(path.join(os.tmpdir(), 'gwproc-boot-test-'))
const fake = path.join(scratch, 'fake-omnipus.cjs')
fs.writeFileSync(fake, FAKE_SOURCE, { mode: 0o755 })

afterAll(() => {
  fs.rmSync(scratch, { recursive: true, force: true })
})

/** Every OMNIPUS_HOME the fixture made for this port is gone. */
function homesFor(port: string): string[] {
  return fs.readdirSync(os.tmpdir()).filter((d) => d.startsWith(`omnipus-e2e-gwproc-${port}-`))
}

function bootPorts(text: string, marker: string): string[] {
  return [...text.matchAll(new RegExp(`${marker} boot=\\d+ port=(\\d+)`, 'g'))].map((m) => m[1])
}

describe('GatewayProcess boot failure evidence and retry', () => {
  it('never listens: retries once, then fails with BOTH boots\' own output tails', async () => {
    const marker = `NEVER-LISTENS-${process.pid}`
    const retries: string[] = []
    const err = await GatewayProcess.start({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'never-listen' },
      onBootRetry: (report) => retries.push(report),
    }).then(
      () => undefined,
      (e: unknown) => e as Error,
    )
    expect(err, 'a gateway that never answers /health must fail start()').toBeInstanceOf(Error)
    const message = (err as Error).message
    expect(message).toContain('/health never returned 200')
    const ports = bootPorts(message, marker)
    expect(ports, 'the failure message carries each boot\'s own stderr line').toHaveLength(2)
    expect(ports[0], 'the retry runs on a fresh port').not.toBe(ports[1])
    expect(message).toContain('gateway output')
    expect(retries, 'exactly one retry is reported').toHaveLength(1)
    expect(bootPorts(retries[0], marker), 'the retry report carries the failed boot\'s output').toEqual([ports[0]])
    for (const p of ports) expect(homesFor(p), `the OMNIPUS_HOME for port ${p} is removed`).toEqual([])
  }, 30_000)

  it('exits during boot: the failure names the exit code and carries the output tail', async () => {
    const marker = `EXITS-${process.pid}`
    const err = await GatewayProcess.start({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'exit' },
    }).then(
      () => undefined,
      (e: unknown) => e as Error,
    )
    expect(err).toBeInstanceOf(Error)
    const message = (err as Error).message
    expect(message).toMatch(/code 3/)
    expect(bootPorts(message, marker)).toHaveLength(2)
  }, 30_000)

  it('a boot that fails once and then answers: start() succeeds and the first failure is reported', async () => {
    const marker = `SECOND-BOOT-OK-${process.pid}`
    const retries: string[] = []
    const gw = await GatewayProcess.start({
      binary: fake,
      apiBase: 'http://127.0.0.1:9',
      healthTimeoutMs: 1_000,
      env: { FAKE_MARKER: marker, FAKE_MODE: 'listen-on-second', FAKE_COUNTER: path.join(scratch, 'second-boot.count') },
      onBootRetry: (report) => retries.push(report),
    })
    try {
      const health = await fetch(`${gw.baseURL}/health`)
      expect(health.status, 'the returned gateway is the healthy second boot').toBe(200)
      expect(retries).toHaveLength(1)
      expect(retries[0]).toContain('/health never returned 200')
      expect(retries[0]).toContain(`${marker} boot=1`)
    } finally {
      await gw.stop()
    }
  }, 30_000)
})
