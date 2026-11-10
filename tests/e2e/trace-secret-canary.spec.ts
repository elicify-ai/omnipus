/**
 * trace-secret-canary.spec.ts — real model keys must never reach a retained
 * Playwright trace (security finding F1, CI MiniMax switch, 2026-10-29).
 *
 * The Playwright test runner records every request a Playwright request
 * context sends, JSON bodies included, into the test's trace, and CI uploads
 * retained traces as artifacts, which GitHub does not secret-mask. The
 * onboarding helper (fixtures/onboard-via-api.ts) carries the primary and the
 * fallback key, so it must send them with Node's own fetch instead.
 *
 * How this proves it, with fake canary keys and a local fake gateway (no
 * network, no real key):
 *   1. The first test runs with trace: 'on' and onboards through the helper
 *      with the canary keys in the key env vars. It also sends one CONTROL
 *      marker through a Playwright request context — the instrument check that
 *      the trace really records request bodies (a canary absent from a trace
 *      that records nothing would prove nothing).
 *   2. The second test (same worker, serial) unzips the first test's
 *      retained trace and requires: the CONTROL marker present, both canaries
 *      absent.
 *
 * No gateway binary, browser or LLM is used, and no external unzip tool: the
 * trace ZIP is read with Node's zlib (readZipText below).
 */
import { randomUUID } from 'node:crypto'
import fs from 'node:fs'
import http from 'node:http'
import type { AddressInfo } from 'node:net'
import path from 'node:path'
import { inflateRawSync } from 'node:zlib'
import { expect, request, test } from '@playwright/test'
import { E2E_FALLBACK } from './fixtures/e2e-model'
import { keyEnvNamesFor } from './fixtures/e2e-model-keys'
import { onboardViaAPI } from './fixtures/onboard-via-api'

test.describe.configure({ mode: 'serial', retries: 0 })
// trace: 'on' is file-wide (Playwright forbids it per describe group): every
// test here is traced, and the second test reads the first one's trace.
test.use({ storageState: { cookies: [], origins: [] }, trace: 'on' })

// Built at run time, never written in this file: a trace also embeds the test
// SOURCE files, so a literal canary would be found in the trace's copy of this
// spec and the check would fail for the wrong reason.
const RUN = randomUUID().replace(/-/g, '')
const PRIMARY_CANARY = ['trace', 'canary', 'primary', RUN].join('-')
const FALLBACK_CANARY = ['trace', 'canary', 'fallback', RUN].join('-')
const CONTROL_MARKER = ['trace', 'canary', 'control', RUN].join('-')
// A fake provider and model for the primary side (see the traced test).
const CANARY_PROVIDER = 'trace-canary-provider'
const CANARY_MODEL = 'trace-canary-model'

let tracedOutputDir = ''

/** Every entry of a ZIP file, decompressed and joined (stored or deflate only). */
function readZipText(file: string): string {
  const buf = fs.readFileSync(file)
  let eocd = -1
  for (let i = buf.length - 22; i >= Math.max(0, buf.length - 65_557); i--) {
    if (buf.readUInt32LE(i) === 0x06054b50) {
      eocd = i
      break
    }
  }
  if (eocd < 0) throw new Error(`${file} is not a ZIP file (no end-of-central-directory record)`)
  const count = buf.readUInt16LE(eocd + 10)
  let p = buf.readUInt32LE(eocd + 16)
  const parts: string[] = []
  for (let n = 0; n < count; n++) {
    if (buf.readUInt32LE(p) !== 0x02014b50) throw new Error(`${file}: bad central-directory entry ${n}`)
    const method = buf.readUInt16LE(p + 10)
    const size = buf.readUInt32LE(p + 20)
    const local = buf.readUInt32LE(p + 42)
    const data = local + 30 + buf.readUInt16LE(local + 26) + buf.readUInt16LE(local + 28)
    const raw = buf.subarray(data, data + size)
    if (method === 0) parts.push(raw.toString('latin1'))
    else if (method === 8) parts.push(inflateRawSync(raw).toString('latin1'))
    else throw new Error(`${file}: entry ${n} uses unsupported compression method ${method}`)
    p += 46 + buf.readUInt16LE(p + 28) + buf.readUInt16LE(p + 30) + buf.readUInt16LE(p + 32)
  }
  return parts.join('\n')
}

function startFakeGateway(): Promise<{ url: string; bodies: string[]; close: () => Promise<void> }> {
  const bodies: string[] = []
  const server = http.createServer((req, res) => {
    const chunks: Buffer[] = []
    req.on('data', (c) => chunks.push(c as Buffer))
    req.on('end', () => {
      bodies.push(Buffer.concat(chunks).toString('utf8'))
      res.setHeader('content-type', 'application/json')
      if (req.method === 'GET' && req.url === '/api/v1/agents') {
        res.end(JSON.stringify([{ id: 'mia', revision: 'rev-mia' }]))
        return
      }
      res.end(JSON.stringify({ token: 'fake-onboarding-token' }))
    })
  })
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address() as AddressInfo
      resolve({
        url: `http://127.0.0.1:${addr.port}`,
        bodies,
        close: () => new Promise((done) => server.close(() => done())),
      })
    })
  })
}

test.describe('credential-bearing requests stay out of traces', () => {
  test('onboarding with canary keys (traced)', async () => {
    tracedOutputDir = test.info().outputDir
    // The primary side is a FAKE provider/model of this test, never the job's
    // E2E_PRIMARY. After a failed job-start check CI points E2E_PRIMARY at the
    // fallback pair (OMNIPUS_E2E_PRIMARY_PROVIDER/_MODEL); both canaries then
    // landed in the same env var, only one key was sent and this test failed
    // in CI while passing locally (train 5, shard stubs-2). A distinct fake
    // primary keeps the two-key path — primary onboarding plus the fallback
    // provider PUT — under test whatever route the job took.
    const names = [...keyEnvNamesFor(CANARY_PROVIDER), ...keyEnvNamesFor(E2E_FALLBACK.provider)]
    const saved = new Map(names.map((n) => [n, process.env[n]]))
    const gw = await startFakeGateway()
    try {
      for (const n of names) delete process.env[n]
      process.env[keyEnvNamesFor(CANARY_PROVIDER)[0]] = PRIMARY_CANARY
      process.env[keyEnvNamesFor(E2E_FALLBACK.provider)[0]] = FALLBACK_CANARY

      await onboardViaAPI({ baseURL: gw.url, providerID: CANARY_PROVIDER, model: CANARY_MODEL })

      // The fake gateway received both canaries, so the helper really sent
      // them (a green that never sent a key would prove nothing).
      const received = gw.bodies.join('\n')
      // Booleans only: expect() arguments are themselves recorded in the trace.
      expect(received.includes(PRIMARY_CANARY), 'the fake gateway got the primary key').toBe(true)
      expect(received.includes(FALLBACK_CANARY), 'the fake gateway got the fallback key').toBe(true)

      // Instrument check: a Playwright request context IS recorded.
      const ctx = await request.newContext()
      try {
        const res = await ctx.post(`${gw.url}/control`, { data: { marker: CONTROL_MARKER } })
        expect(res.ok()).toBe(true)
      } finally {
        await ctx.dispose()
      }
    } finally {
      await gw.close()
      for (const [n, v] of saved) {
        if (v === undefined) delete process.env[n]
        else process.env[n] = v
      }
    }
  })

  test('the retained trace holds the control marker and neither canary', async () => {
    expect(tracedOutputDir, 'the traced test must run first in this worker').not.toBe('')
    const zip = path.join(tracedOutputDir, 'trace.zip')
    expect(fs.existsSync(zip), `no retained trace at ${zip}`).toBe(true)
    const contents = readZipText(zip)
    expect(contents, 'instrument check: the trace must record request bodies').toContain(CONTROL_MARKER)
    expect(contents.includes(PRIMARY_CANARY), 'the primary key reached the trace').toBe(false)
    expect(contents.includes(FALLBACK_CANARY), 'the fallback key reached the trace').toBe(false)
  })
})
