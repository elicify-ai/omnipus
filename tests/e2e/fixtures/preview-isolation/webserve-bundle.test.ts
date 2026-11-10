// @vitest-environment node
// (Node's own fetch/WebSocket; jsdom's Event class breaks Node's WebSocket.)
/**
 * webserve-bundle.test.ts — checks the fixture helpers that
 * tests/e2e/preview-isolation-webserve.spec.ts relies on (#950 review F4),
 * so the browser rows cannot fail for a fixture reason before they reach the
 * property under test:
 *
 *   - the card fixture's isolated_url is derived from the real test origin
 *     and passes the SPA's own validator (src/lib/preview-url.ts) at the
 *     preview-isolation shard's port 6083 — the old fixed :5000 URL did not;
 *   - the in-prefix form target is a CHILD of /preview/{agent}/{token}/, not
 *     the sibling token the old `pathname + '-posted/'` produced;
 *   - the dev fixture upstream really serves the bundle with the right
 *     content types and really completes a WebSocket handshake (run here under
 *     Node with Node's own WebSocket client).
 *
 * This is NOT browser acceptance: no gateway, no CSP, no agent turn.
 */

import { afterEach, describe, expect, it } from 'vitest'
import { spawn, type ChildProcess } from 'node:child_process'
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs'
import { tmpdir } from 'node:os'
import { join } from 'node:path'
import { createServer } from 'node:net'
import { resolvePreviewHref } from '../../../../src/lib/preview-url'
import {
  DATA_OK_VALUE,
  MODULE_RAN_VALUE,
  PROBE_LETTER_SPACING,
  WS_HELLO,
  childOfPrefix,
  devBundleFiles,
  isolatedUrlFor,
  previewPrefixUrl,
  siblingTokenUrl,
  staticBundleFiles,
} from './webserve-bundle'

const MODE2 = 'http://localhost:6083/preview/mia/tok_ABC-123/'

describe('isolatedUrlFor — Mode 1 card fixture derived from the test origin', () => {
  const chromiumNav = { userAgentData: { brands: [{ brand: 'Chromium', version: '130' }] }, userAgent: 'x' }
  const originalNavigator = globalThis.navigator

  afterEach(() => {
    Object.defineProperty(globalThis, 'navigator', { value: originalNavigator, configurable: true })
  })

  function resolveOnChromium(isolated: string, port: number, origin: string) {
    Object.defineProperty(globalThis, 'navigator', { value: chromiumNav, configurable: true })
    return resolvePreviewHref({
      path: '/preview/mia/tok_ABC-123/',
      url: MODE2,
      isolated_url: isolated,
      origin,
      hostname: 'localhost',
      port,
    })
  }

  it('at the shard origin :6083 the derived URL is the one the SPA renders (Mode 1 selected)', () => {
    const iso = isolatedUrlFor('http://localhost:6083', 'e2e-card')
    expect(iso).toBe('http://e2e-card.localhost:6083/')
    expect(resolveOnChromium(iso, 6083, 'http://localhost:6083')).toEqual({ href: iso })
  })

  it('control: the old fixed :5000 URL is rejected at :6083 and falls back to Mode 2', () => {
    expect(resolveOnChromium('http://myapp.localhost:5000/', 6083, 'http://localhost:6083')).toEqual({ href: MODE2 })
  })

  it('a portless origin (http :80) yields a portless URL', () => {
    expect(isolatedUrlFor('http://localhost', 'app')).toBe('http://app.localhost/')
  })

  it('refuses a label the gateway could never mint', () => {
    expect(() => isolatedUrlFor('http://localhost:6083', 'Bad.Label')).toThrow()
  })
})

describe('prefix helpers — child vs sibling', () => {
  it('previewPrefixUrl always ends in a slash (the spec ${PREFIX})', () => {
    expect(previewPrefixUrl('http://localhost:6083/preview/mia/tok').href).toBe('http://localhost:6083/preview/mia/tok/')
    expect(previewPrefixUrl(`${MODE2}index.html`).href).toBe(MODE2)
  })

  it('childOfPrefix stays inside /preview/{agent}/{token}/', () => {
    expect(childOfPrefix(MODE2, 'posted')).toBe(`${MODE2}posted`)
    expect(childOfPrefix('http://localhost:6083/preview/mia/tok_ABC-123', 'posted')).toBe(`${MODE2}posted`)
  })

  it('control: the old form target was a different token, not a child', () => {
    const old = new URL(MODE2)
    old.pathname = old.pathname.replace(/\/$/, '') + '-posted/'
    expect(old.pathname.startsWith(new URL(MODE2).pathname)).toBe(false)
  })

  it('siblingTokenUrl shares the string prefix but is outside the directory', () => {
    const sib = siblingTokenUrl(MODE2, 'data.json')
    expect(sib).toBe('http://localhost:6083/preview/mia/tok_ABC-123-sibling/data.json')
    expect(new URL(sib).pathname.startsWith(new URL(MODE2).pathname)).toBe(false)
  })

  it('refuses absolute or traversing children', () => {
    expect(() => childOfPrefix(MODE2, '/api/v1/agents')).toThrow()
    expect(() => childOfPrefix(MODE2, '../x')).toThrow()
  })
})

describe('bundle files', () => {
  it('are plain one-line ASCII with no backslashes and carry the probe values', () => {
    for (const content of Object.values(devBundleFiles('e2e-x'))) {
      expect(content).not.toMatch(/\\/)
      expect(content).toMatch(/^[\x20-\x7e\n]*$/)
    }
    expect(staticBundleFiles('e2e-x')['style.css']).toContain(PROBE_LETTER_SPACING)
    expect(staticBundleFiles('e2e-x')['app.js']).toContain(MODULE_RAN_VALUE)
    expect(JSON.parse(staticBundleFiles('e2e-x')['data.json'])).toEqual({ ok: DATA_OK_VALUE })
  })
})

describe('dev fixture upstream (server.js) under Node', () => {
  let child: ChildProcess | undefined
  let dir: string | undefined

  afterEach(() => {
    child?.kill()
    child = undefined
    if (dir) rmSync(dir, { recursive: true, force: true })
    dir = undefined
  })

  async function freePort(): Promise<number> {
    return new Promise((resolve, reject) => {
      const srv = createServer()
      srv.once('error', reject)
      srv.listen(0, '127.0.0.1', () => {
        const addr = srv.address()
        if (addr === null || typeof addr === 'string') {
          srv.close()
          reject(new Error('no port'))
          return
        }
        srv.close(() => resolve(addr.port))
      })
    })
  }

  async function startFixture(): Promise<string> {
    dir = mkdtempSync(join(tmpdir(), 'webserve-bundle-'))
    for (const [name, content] of Object.entries(devBundleFiles('e2e-dev-node'))) {
      writeFileSync(join(dir, name), content)
    }
    const port = await freePort()
    child = spawn(process.execPath, ['server.js'], { cwd: dir, env: { ...process.env, PORT: String(port) }, stdio: 'pipe' })
    const base = `http://127.0.0.1:${port}`
    const deadline = Date.now() + 10_000
    let lastErr: unknown
    while (Date.now() < deadline) {
      try {
        const r = await fetch(`${base}/`)
        if (r.ok) return base
        lastErr = new Error(`status ${r.status}`)
      } catch (err) {
        lastErr = err
      }
      await new Promise((res) => setTimeout(res, 100))
    }
    throw new Error(`fixture server did not start: ${String(lastErr)}`)
  }

  it('serves index, stylesheet, module and data with the right content types', async () => {
    const base = await startFixture()
    const cases: Array<[string, string, string]> = [
      ['/', 'text/html', 'e2e-dev-node-ready'],
      ['/style.css', 'text/css', PROBE_LETTER_SPACING],
      ['/app.js', 'text/javascript', MODULE_RAN_VALUE],
      ['/data.json', 'application/json', DATA_OK_VALUE],
    ]
    for (const [p, type, body] of cases) {
      const r = await fetch(base + p)
      expect(r.status, p).toBe(200)
      expect(r.headers.get('content-type'), p).toBe(type)
      expect(await r.text(), p).toContain(body)
    }
    const missing = await fetch(`${base}/posted`, { method: 'POST' })
    expect(missing.status).toBe(404)
  }, 30_000)

  it('completes a WebSocket handshake on any path and sends the hello frame', async () => {
    const base = await startFixture()
    const message = await new Promise<string>((resolve, reject) => {
      const ws = new WebSocket(`${base.replace(/^http/, 'ws')}/hmr`)
      const timer = setTimeout(() => reject(new Error('no message within 5 s')), 5_000)
      ws.onmessage = (ev) => {
        clearTimeout(timer)
        resolve(String(ev.data))
        ws.close()
      }
      ws.onerror = () => {
        clearTimeout(timer)
        reject(new Error('websocket error'))
      }
    })
    expect(message).toBe(WS_HELLO)
  }, 30_000)
})
