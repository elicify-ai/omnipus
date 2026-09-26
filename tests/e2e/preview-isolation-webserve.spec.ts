/**
 * preview-isolation-webserve.spec.ts — ADR-094 (#798 preview isolation) TDD
 * Plan orders 24, 30, 31 and 32 — the E2E half of the RED pack.
 *
 * Traces to: docs/internal/specs/adr-094-preview-isolation-spec.md
 *   Order 24 (S-2.1, S-2.2, S-8.1): Mode 1 Chromium+Firefox open the full app
 *     with working storage; the S-2.2 controls hold in a real browser; the
 *     WebKit card falls back to Mode 2 (FR-024).
 *   Order 30 (S-2.13, RED then GREEN): the #798 attack — script inside a
 *     served /preview/ document reads document.cookie for the csrf value and
 *     POSTs to /api/v1 with the echoed X-Csrf-Token. On TODAY's code the state
 *     change LANDS (RED — proving hole and instrument); after implementation
 *     no state change lands (GREEN). Both serving paths: static AND dev-proxy.
 *   Order 31 (S-3.6, S-3.7): Mode 2 renders fully while isolated — HMR over
 *     ws://, form POST inside the prefix, popup and download all work.
 *   Order 32 (S-4.4): planted-cookie recovery in a real browser — a
 *     Domain=localhost omnipus-session plant (including the trailing-slash
 *     /api/ form) leads to exactly one remaining cookie, a successful retry
 *     and no forced logout.
 *
 * ORACLE INDEPENDENCE: every expectation below is transcribed from the spec —
 * the S-2.13 attack shape, the Mode 2 CSP template (spec §"Mode 2 response
 * headers"), FR-024's engine rule, S-4.4's recovery outcome, FR-015's reserved
 * cookie names and Q4's exact toast message. Nothing is derived from running
 * the implementation.
 *
 * RED SEMANTICS (current failure mode, per row — see each test's comment):
 *   Mode 1 rows fail TODAY at the mint step: `isolated_url` does not exist
 *   (FR-001/FR-011 unimplemented) — the failure names the missing spec
 *   element (loud BLOCKED-style failure, never a skip).
 *   Order 30's rows fail TODAY on the security expectation itself: the attack
 *   lands (the state change occurs server-side) — the literal RED the spec
 *   prescribes.
 *   Order 31's rows and the WebKit fallback row are PINS (green today) that
 *   must survive GREEN unchanged; they are labelled as pins in comments.
 *   Order 32 fails TODAY: no planted-cookie detector exists (FR-015), so the
 *   expected recovery toast never appears.
 *
 * MINT MECHANISM (judgment call, flagged to squad-lead): web_serve
 * registrations are tool-only — there is no REST endpoint that creates one
 * (pkg/gateway/gateway_boot.go wires ServedSubdirs/DevServerRegistry into the
 * tool config; pkg/gateway/rest_preview.go only reads them) — so a real
 * served /preview/ document requires a real agent turn through the composer.
 * This introduces an LLM dependency into the LLM-free isolation shard; the
 * spec's own regression table plans Mode 1 minting on this shard's gateway
 * (port 6083, loopback public_url), so the dependency is intended by the
 * spec. Dev-proxy minting additionally requires Linux (Tier3UnsupportedMessage:
 * dev servers are Linux only) — on other platforms the row fails loudly at
 * the mint step, never skips.
 */

import * as fs from 'node:fs'
import * as path from 'node:path'
import { expect, type Page } from '@playwright/test'
import { test } from './fixtures/console-errors'
import { chatInput, startNewChat, waitForConnected } from './fixtures/selectors'
import { seedAndOpenSession } from './fixtures/session-setup'

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060'
/** Q4's exact human message (S-4.2; order 27 pins the SPA side of it). */
const Q4_MESSAGE = 'Omnipus cleared cookies set by a preview — please retry'
/** MINT_TIMEOUT covers one real agent turn (5-25 s typical, GLM flash). */
const MINT_TIMEOUT = 180_000

// ── Auth plumbing (session-setup's proven pattern; not exported there) ───────

function authFilePath(): string {
  return process.env.OMNIPUS_AUTH_FILE
    ? path.resolve(process.env.OMNIPUS_AUTH_FILE)
    : path.join(path.dirname(new URL(import.meta.url).pathname), '.auth/admin.json')
}

function storedAuthToken(): string | null {
  try {
    const raw = fs.readFileSync(authFilePath(), 'utf-8')
    const state = JSON.parse(raw) as {
      origins?: Array<{ localStorage?: Array<{ name: string; value: string }> }>
    }
    for (const origin of state.origins ?? []) {
      for (const item of origin.localStorage ?? []) {
        if (item.name === 'omnipus_auth_token') return item.value
      }
    }
  } catch {
    // Auth file may not exist on first run — the caller's error is louder.
  }
  return null
}

/** Bearer + CSRF headers for Node-context API calls (the SPA's own shape). */
async function authedHeaders(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies()
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  const token = storedAuthToken()
  return {
    'Content-Type': 'application/json',
    ...(token ? { Authorization: `Bearer ${token}` } : {}),
    ...(csrf ? { 'X-CSRF-Token': csrf.value } : {}),
  }
}

/** The SPA's live session id — ChatScreen stamps it on the chat surface. */
async function activeSessionId(page: Page): Promise<string> {
  const el = page.locator('[data-active-session-id]').first()
  await expect(el, 'chat surface must be bound to a session').toBeVisible({
    timeout: 15_000,
  })
  const sid = await el.getAttribute('data-active-session-id')
  if (!sid) throw new Error('data-active-session-id present but empty')
  return sid
}

/** The /api/v1/about mock (web-serve-canonical's pattern): schema realism
 * for the SPA render path; the href assertions do not depend on it. */
async function mockAbout(page: Page): Promise<void> {
  await page.route(`${BASE_URL}/api/v1/about`, async (route) => {
    let base: Record<string, unknown> = {}
    try {
      const real = await route.fetch()
      if (real.ok()) base = (await real.json()) as Record<string, unknown>
    } catch {
      // Gateway not reachable — stub is sufficient for the SPA render path.
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...base, preview_enabled: true }),
    })
  })
}

interface WireToolCall {
  id: string
  tool: string
  status: string
  parameters?: Record<string, unknown>
  result?: Record<string, unknown>
}

interface WireMessage {
  id: string
  role: string
  tool_calls?: WireToolCall[]
}

/**
 * Drive one REAL agent turn through the composer and wait until the
 * transcript (via the sessions API) carries a successful web_serve call for
 * `dirName`. Returns the tool result exactly as the gateway persisted it.
 */
async function mintWebServeViaAgent(
  page: Page,
  dirName: string,
  dev: boolean,
): Promise<Record<string, unknown>> {
  await page.goto('/')
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page, { timeout: 15_000 })
  await startNewChat(page)

  const commandLine = dev
    ? `npm run dev`
    : `web_serve with path "${dirName}" and duration_seconds 1800`
  const prompt = dev
    ? `Do exactly three things with tools, in this order, then stop: ` +
      `(1) Create directory ${dirName} in your workspace. ` +
      `(2) In it create package.json with exactly this content: ` +
      `{"scripts":{"dev":"python3 -m http.server $PORT"}} — and a file ` +
      `index.html whose full content is: <title>${dirName}</title>dev-e2e. ` +
      `(3) Run ${commandLine} on directory ${dirName} (do not pass a port; ` +
      `the tool injects PORT into the environment). Report the URL.`
    : `Do exactly two things with tools, in this order, then stop: ` +
      `(1) Create directory ${dirName} in your workspace with a file ` +
      `index.html whose full content is: <title>${dirName}</title>static-e2e. ` +
      `(2) Run the ${commandLine}. Report the URL.`

  const input = chatInput(page)
  await expect(input).toBeEnabled({ timeout: 15_000 })
  await input.fill(prompt)
  await input.press('Enter')

  const sid = await activeSessionId(page)
  const deadline = Date.now() + MINT_TIMEOUT
  let lastBody = 'never polled'
  while (Date.now() < deadline) {
    const resp = await page.request.get(`${BASE_URL}/api/v1/sessions/${sid}/messages`, {
      headers: await authedHeaders(page),
    })
    if (resp.ok()) {
      const messages = (await resp.json()) as WireMessage[]
      for (const msg of messages) {
        for (const call of msg.tool_calls ?? []) {
          if (
            call.tool === 'web_serve' &&
            call.status === 'success' &&
            (call.parameters?.path === dirName ||
              (call.result && typeof call.result === 'object' &&
                String((call.result as Record<string, unknown>).path ?? '').includes(dirName)))
          ) {
            return call.result as Record<string, unknown>
          }
        }
      }
      lastBody = `polled ${messages.length} messages, no web_serve success for ${dirName}`
    } else {
      lastBody = `messages API ${resp.status()}`
    }
    await page.waitForTimeout(2_000)
  }
  throw new Error(
    `mint failed: no successful web_serve result for "${dirName}" within ` +
      `${MINT_TIMEOUT} ms (last: ${lastBody}). The agent turn is the only ` +
      `product surface that registers a preview — a failure here is loud, ` +
      `never a skip.`,
  )
}

// ── The #798 attack script (S-2.13, verbatim shape) ──────────────────────────
//
// Runs INSIDE the served preview document. Reads document.cookie for the csrf
// value, enumerates workspaces over the same-origin API, and POSTs a mkdir.
// Returns a report the test verifies SERVER-SIDE (ground truth, the harness
// rule): what the script could read and what landed.

interface AttackReport {
  csrfFound: boolean
  csrfName: string
  listed: boolean
  wsId: string
  attackPath: string
  mkdirStatus: number
}

async function runAttackInPreview(page: Page, tag: string): Promise<AttackReport> {
  return page.evaluate(
    async ({ tag }: { tag: string }) => {
      const report: AttackReport = {
        csrfFound: false,
        csrfName: '',
        listed: false,
        wsId: '',
        attackPath: `attack-${tag}`,
        mkdirStatus: 0,
      }
      let csrfValue = ''
      const pairs = document.cookie.split(/;\s*/)
      for (const name of ['__Host-csrf', 'csrf']) {
        const pair = pairs.find((p) => p.startsWith(name + '='))
        if (pair) {
          report.csrfFound = true
          report.csrfName = name
          csrfValue = pair.slice(name.length + 1)
          break
        }
      }
      if (!report.csrfFound) return report
      const wsRes = await fetch('/api/v1/workspaces', { credentials: 'include' })
      if (!wsRes.ok) return report
      const wsList = (await wsRes.json()) as
        | Array<{ id: string }>
        | { workspaces?: Array<{ id: string }> }
      const first = Array.isArray(wsList) ? wsList[0] : wsList.workspaces?.[0]
      if (!first) return report
      report.listed = true
      report.wsId = first.id
      const mkdirRes = await fetch(`/api/v1/library/${first.id}/mkdir`, {
        method: 'POST',
        credentials: 'include',
        headers: {
          'Content-Type': 'application/json',
          'X-Csrf-Token': csrfValue,
        },
        body: JSON.stringify({ path: report.attackPath }),
      })
      report.mkdirStatus = mkdirRes.status
      return report
    },
    { tag },
  )
}

// ── Mode 2 header facts the attack rows assert (spec §"Mode 2 response
//    headers", lines 291/360-365; FR-014) — the attack-relevant subset, not
//    the byte-identical tripwire (that is order 14, the backend pack). ────────

function expectMode2AttackHeaders(csp: string, what: string): void {
  expect(
    csp.includes('frame-ancestors \'none\''),
    what + ': frame-ancestors must be \'none\' (the served doc must not be embeddable)',
  ).toBe(true)
  expect(
    /(^|;)\s*sandbox\b/.test(csp),
    what + ': no sandbox directive may exist in the web_serve model (FR-004)',
  ).toBe(false)
  expect(
    /connect-src[^;]*'self'/.test(csp),
    what + ': connect-src must not carry \'self\' — that is the whole-origin ' +
      'grant that lets the attack fetch reach /api/v1',
  ).toBe(false)
}

test.describe('order 30 — S-2.13 the #798 attack (RED then GREEN)', () => {
  test('static path: the attack from a served /preview/ document must not drive an authenticated state change', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-srv-${Date.now()}`
    const result = await mintWebServeViaAgent(page, dirName, false)
    const mode2URL = String(result.url ?? '')
    expect(mode2URL, 'mint must return the Mode 2 url').toContain('/preview/')

    // The served document: same origin as the gateway — that IS the hole.
    await page.goto(mode2URL)
    await expect(page.locator('body')).not.toBeEmpty()

    // The attack, then the server-side oracle: the mkdir must NOT land.
    const report = await runAttackInPreview(page, dirName)
    expect(
      report.csrfFound,
      'instrument check: the document must see the csrf cookie while the ' +
        'hole exists (S-2.13 prescribes the cookie-read step)',
    ).toBe(true)
    expect(
      report.listed,
      'instrument check: the document must be able to list workspaces via ' +
        'the same-origin API while the hole exists',
    ).toBe(true)
    const entriesRes = await page.request.get(
      `${BASE_URL}/api/v1/library/${report.wsId}/entries?path=`,
      { headers: await authedHeaders(page) },
    )
    expect(entriesRes.ok(), 'library listing must be readable (oracle setup)').toBe(true)
    const entries = (await entriesRes.json()) as Array<{ name?: string; path?: string }>
    const landed = entries.some((e) =>
      (e.path ?? e.name ?? '').includes(report.attackPath),
    )
    expect(
      landed,
      'S-2.13 RED/GREEN: the #798 attack LANDED — script inside the served ' +
        `/preview/ document POSTed /api/v1/library/${report.wsId}/mkdir with ` +
        `the echoed X-Csrf-Token (HTTP ${report.mkdirStatus}) and the state ` +
        'change occurred server-side. ADR-094 requires the state change NOT ' +
        'to land (Mode 1 origin separation / Mode 2 connect-src + form-action ' +
        'confinement).',
    ).toBe(false)

    // Mode 2 browser-level blocks (order 30: CSP-confined fetch/form,
    // frame-ancestors 'none') — asserted on the response the document got.
    const served = await page.request.get(mode2URL)
    const csp = served.headers()['content-security-policy'] ?? ''
    expectMode2AttackHeaders(csp, 'static path')
  })

  test('dev-proxy path: the attack from a dev-served /preview/ document must not drive an authenticated state change', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-dev-${Date.now()}`
    const result = await mintWebServeViaAgent(page, dirName, true)
    const mode2URL = String(result.url ?? '')
    expect(mode2URL, 'dev mint must return the proxied Mode 2 url').toContain('/preview/')

    await page.goto(mode2URL)
    await expect(page.locator('body')).not.toBeEmpty()

    const report = await runAttackInPreview(page, dirName)
    expect(
      report.csrfFound,
      'instrument check: the proxied document must see the csrf cookie while ' +
        'the hole exists (the dev proxy must not leak gateway credentials to ' +
        'the upstream, but the DOCUMENT is same-origin with the gateway)',
    ).toBe(true)
    expect(report.listed, 'instrument check: workspaces listed via the API').toBe(true)
    const entriesRes = await page.request.get(
      `${BASE_URL}/api/v1/library/${report.wsId}/entries?path=`,
      { headers: await authedHeaders(page) },
    )
    expect(entriesRes.ok(), 'library listing must be readable (oracle setup)').toBe(true)
    const entries = (await entriesRes.json()) as Array<{ name?: string; path?: string }>
    const landed = entries.some((e) =>
      (e.path ?? e.name ?? '').includes(report.attackPath),
    )
    expect(
      landed,
      'S-2.13 (dev-proxy path) RED/GREEN: the attack LANDED through the ' +
        `proxied document (HTTP ${report.mkdirStatus}) — the dev-proxy path ` +
        'is exercised, not only static serving.',
    ).toBe(false)

    const served = await page.request.get(mode2URL)
    const csp = served.headers()['content-security-policy'] ?? ''
    expectMode2AttackHeaders(csp, 'dev-proxy path')
  })
})

test.describe('order 31 — S-3.6/S-3.7 Mode 2 renders fully while isolated (pins)', () => {
  test('S-3.6: a hot-reload ws:// connection inside the preview prefix is not CSP-blocked', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-hmr-${Date.now()}`
    const result = await mintWebServeViaAgent(page, dirName, true)
    const mode2URL = String(result.url ?? '')
    expect(mode2URL).toContain('/preview/')

    await page.goto(mode2URL)
    await expect(page.locator('body')).not.toBeEmpty()

    // The connection attempt targets the ws-scheme form of THIS document's own
    // prefix (S-3.6: "connect-src carries the ws-scheme form of the prefix").
    // The oracle is the browser's own CSP enforcement: a blocked connection
    // fires a `securitypolicyviolation` with violatedDirective=connect-src; an
    // allowed-but-failing handshake (python http.server speaks no WS) errors
    // at the protocol level WITHOUT any CSP violation. That distinction is the
    // instrument — it needs no real WS upstream.
    const violations = await page.evaluate(
      () =>
        new Promise<string[]>((resolve) => {
          const seen: string[] = []
          document.addEventListener('securitypolicyviolation', (e) => {
            seen.push(e.violatedDirective + ' ' + e.blockedURI)
          })
          const prefix = location.pathname
          const ws = new WebSocket(
            (location.protocol === 'https:' ? 'wss://' : 'ws://') +
              location.host + prefix,
          )
          ws.onerror = () => {
            /* handshake failure is EXPECTED — python speaks no WS */
          }
          setTimeout(() => resolve(seen), 1_500)
        }),
    )
    expect(
      violations.filter((v) => v.startsWith('connect-src')),
      'S-3.6: the ws-scheme form of the prefix must be allowed by ' +
        'connect-src — got violations: ' + JSON.stringify(violations),
    ).toHaveLength(0)
  })

  test('S-3.7: a form POST inside the preview prefix works (no CSP form-action block)', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-form-${Date.now()}`
    const result = await mintWebServeViaAgent(page, dirName, false)
    const mode2URL = String(result.url ?? '')
    expect(mode2URL).toContain('/preview/')

    await page.goto(mode2URL)
    await expect(page.locator('body')).not.toBeEmpty()

    // Submit a form whose action is INSIDE the prefix (a sibling path — a 404
    // from the file server is fine: what is asserted is that the navigation
    // HAPPENED and was not CSP-blocked; a blocked form-action fires a
    // securitypolicyviolation and the page does not navigate).
    const outcome = await page.evaluate(
      () =>
        new Promise<{ navigated: boolean; violations: string[] }>((resolve) => {
          const seen: string[] = []
          document.addEventListener('securitypolicyviolation', (e) => {
            seen.push(e.violatedDirective + ' ' + e.blockedURI)
          })
          const target = new URL(location.href)
          target.pathname = target.pathname.replace(/\/$/, '') + '-posted/'
          const form = document.createElement('form')
          form.method = 'POST'
          form.action = target.pathname
          document.body.appendChild(form)
          form.submit()
          setTimeout(() => resolve({ navigated: true, violations: seen }), 2_000)
        }),
    )
    expect(
      outcome.violations.filter((v) => v.startsWith('form-action')),
      'S-3.7: form-action must allow posts inside the prefix — got: ' +
        JSON.stringify(outcome.violations),
    ).toHaveLength(0)
    expect(
      page.url().includes('-posted/'),
      'the form navigation must have happened (S-3.7: forms work)',
    ).toBe(true)
  })

  test('S-3.7: a popup and a download work from a static preview (no sandbox directive)', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-pop-${Date.now()}`
    const result = await mintWebServeViaAgent(page, dirName, false)
    const mode2URL = String(result.url ?? '')
    expect(mode2URL).toContain('/preview/')

    await page.goto(mode2URL)
    await expect(page.locator('body')).not.toBeEmpty()

    // Popup (S-3.7: window.open / target=_blank) — a sandboxed document would
    // return null or refuse the navigation; no sandbox means it opens.
    const popup = page.waitForEvent('popup', { timeout: 10_000 })
    const opened = await page.evaluate(
      () => window.open(location.href, '_blank') !== null,
    )
    const popupPage = await popup
    expect(
      opened,
      'S-3.7: window.open must work from a preview document (no sandbox)',
    ).toBe(true)
    await expect(popupPage.locator('body')).not.toBeEmpty()
    await popupPage.close()

    // Download (S-3.7) — an <a download> click must deliver a download event;
    // a sandbox without allow-downloads suppresses it. Point the href at the
    // document itself (any served bytes download fine).
    const downloadPromise = page.waitForEvent('download', { timeout: 10_000 })
    await page.evaluate(() => {
      const a = document.createElement('a')
      a.href = location.href
      a.download = 'e2e-download-probe.html'
      document.body.appendChild(a)
      a.click()
    })
    const download = await downloadPromise
    expect(
      download.suggestedFilename(),
      'S-3.7: the download must start with the suggested filename',
    ).toBe('e2e-download-probe.html')
  })
})

test.describe('order 24 — S-2.1/S-2.2/S-8.1 Mode 1 + WebKit fallback', () => {
  test('S-2.1: Mode 1 (Chromium/Firefox) — the label URL opens the full app with working storage', async ({ page, browserName }) => {
    test.skip(browserName === 'webkit', 'holdout H-2: Linux WebKit *.localhost resolution is system-resolver-dependent (spec E2E wiring note) — a WebKit pass would say nothing about Safari')
    test.setTimeout(300_000)
    const dirName = `e2e-m1-${Date.now()}`
    const result = await mintWebServeViaAgent(page, dirName, false)

    // FR-001/FR-011: the tool result MUST gain isolated_url. Today the field
    // does not exist anywhere — this is the loud BLOCKED-style failure that
    // names the missing spec element (never a skip).
    const isolated = String(result.isolated_url ?? '')
    expect(
      isolated,
      'BLOCKED: the web_serve result carries no isolated_url — FR-001/FR-011 ' +
        '(ADR-094 Mode 1 minting) is not implemented yet; required by order 24 ' +
        '(S-2.1). Minted result keys: ' + Object.keys(result).join(','),
    ).toBeTruthy()

    await page.goto(isolated)
    await expect(
      page.getByRole('banner'),
      'S-2.1: the full app must render on the label origin',
    ).toBeVisible({ timeout: 30_000 })
    const storage = await page.evaluate(() => {
      try {
        localStorage.setItem('e2e-mode1-probe', 'ok')
        const ls = localStorage.getItem('e2e-mode1-probe')
        sessionStorage.setItem('e2e-mode1-probe', 'ok')
        const ss = sessionStorage.getItem('e2e-mode1-probe')
        document.cookie = 'e2e-mode1-probe=ok'
        const ck = document.cookie.includes('e2e-mode1-probe=ok')
        history.pushState({}, '', '?mode1probe=1')
        return { ls, ss, ck, history: location.search.includes('mode1probe') }
      } catch (err) {
        return { ls: 'threw', ss: 'threw', ck: false, history: false, err: String(err) }
      }
    })
    expect(storage.ls, 'S-2.1: localStorage must work on the label origin').toBe('ok')
    expect(storage.ss, 'S-2.1: sessionStorage must work').toBe('ok')
    expect(storage.ck, 'S-2.1: cookies must be writable/readable').toBe(true)
    expect(storage.history, 'S-2.1: the History API must work').toBe(true)
  })

  test('S-2.2: Mode 1 (Chromium/Firefox) — the three real controls hold in a real browser', async ({ page, browserName }) => {
    test.skip(browserName === 'webkit', 'holdout H-2: see the S-2.1 row')
    test.setTimeout(300_000)
    const dirName = `e2e-ctl-${Date.now()}`
    const result = await mintWebServeViaAgent(page, dirName, false)
    const isolated = String(result.isolated_url ?? '')
    expect(
      isolated,
      'BLOCKED: no isolated_url — FR-001/FR-011 (ADR-094 Mode 1 minting) is ' +
        'not implemented yet; required by order 24 (S-2.2). Result keys: ' +
        Object.keys(result).join(','),
    ).toBeTruthy()

    await page.goto(isolated)
    const controls = await page.evaluate(
      (gatewayOrigin) =>
        new Promise<{ fetchReadable: boolean | null; wsOpened: boolean | null }>((resolve) => {
          const out: { fetchReadable: boolean | null; wsOpened: boolean | null } = {
            fetchReadable: null,
            wsOpened: null,
          }
          fetch(gatewayOrigin + '/api/v1/agents', { credentials: 'include' })
            .then(async (r) => {
              try {
                await r.json()
                out.fetchReadable = true
              } catch {
                out.fetchReadable = false
              }
            })
            .catch(() => {
              out.fetchReadable = false
            })
          const ws = new WebSocket(
            gatewayOrigin.replace(/^http/, 'ws') + '/api/v1/ws',
          )
          ws.onopen = () => {
            out.wsOpened = true
            ws.close()
          }
          ws.onerror = () => {
            out.wsOpened = false
          }
          ws.onclose = () => {
            if (out.wsOpened !== true) out.wsOpened = false
          }
          setTimeout(() => resolve(out), 4_000)
        }),
      BASE_URL,
    )
    expect(
      controls.fetchReadable,
      'S-2.2 control 1: the CORS layer must not reflect a label Origin, so ' +
        'the script must not be able to READ the cross-origin response ' +
        '(the gateway may still service it server-side — browser-side claim)',
    ).toBe(false)
    expect(
      controls.wsOpened,
      'S-2.2 control 3: the WS origin check must refuse a label-origin ' +
        'connection attempt',
    ).toBe(false)
  })

  test('S-8.1 (Chromium/Firefox): a transcript result carrying isolated_url renders the Mode 1 link', async ({ page, browserName }) => {
    test.skip(browserName === 'webkit', 'FR-024: this expectation is Chromium-family/Firefox-only; the WebKit fallback is the next row')
    await mockAbout(page)

    const fakeToken = 'iso-card-token-t24a'
    const fakePath = `/preview/mia/${fakeToken}/`
    const fakeUrl = `${BASE_URL}${fakePath}`
    const isolated = 'http://myapp.localhost:5000/'
    const expires = new Date(Date.now() + 3600 * 1000).toISOString()

    await seedAndOpenSession(page, 'preview-iso-card-t24a', [
      {
        id: 'user-t24a-1',
        role: 'user',
        content: 'serve my workspace',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t24a-1',
        role: 'assistant',
        content: 'Served.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: 'mia',
        tool_calls: [
          {
            id: 'tc-t24a-serve',
            tool: 'web_serve',
            status: 'success',
            duration_ms: 80,
            parameters: { path: 'e2e', duration_seconds: 3600 },
            result: {
              path: fakePath,
              url: fakeUrl,
              expires_at: expires,
              isolated_url: isolated,
            },
          },
        ],
      },
    ])

    const previewLink = page.locator('[data-testid="preview-link"]')
    await expect(previewLink, 'the card must render exactly one preview link').toHaveCount(1)
    expect(
      await previewLink.getAttribute('href'),
      'FR-024/S-8.1: on a Mode-1-capable engine the card href must be the ' +
        'isolated_url (RED today: isolated_url is dropped and the card ' +
        'renders the Mode 2 fallback)',
    ).toBe(isolated)
  })

  test('S-8.1 (WebKit pin): the same transcript renders the Mode 2 fallback link', async ({ page, browserName }) => {
    test.skip(browserName !== 'webkit', 'the Mode 2 fallback expectation is WebKit-specific (FR-024)')
    await mockAbout(page)

    const fakeToken = 'iso-card-token-t24b'
    const fakePath = `/preview/mia/${fakeToken}/`
    const fakeUrl = `${BASE_URL}${fakePath}`
    const isolated = 'http://myapp.localhost:5000/'
    const expires = new Date(Date.now() + 3600 * 1000).toISOString()

    await seedAndOpenSession(page, 'preview-iso-card-t24b', [
      {
        id: 'user-t24b-1',
        role: 'user',
        content: 'serve my workspace',
        timestamp: new Date(Date.now() - 5000).toISOString(),
        agent_id: '',
      },
      {
        id: 'asst-t24b-1',
        role: 'assistant',
        content: 'Served.',
        timestamp: new Date(Date.now() - 4000).toISOString(),
        agent_id: 'mia',
        tool_calls: [
          {
            id: 'tc-t24b-serve',
            tool: 'web_serve',
            status: 'success',
            duration_ms: 80,
            parameters: { path: 'e2e', duration_seconds: 3600 },
            result: {
              path: fakePath,
              url: fakeUrl,
              expires_at: expires,
              isolated_url: isolated,
            },
          },
        ],
      },
    ])

    const previewLink = page.locator('[data-testid="preview-link"]')
    await expect(previewLink, 'the card must render exactly one preview link').toHaveCount(1)
    expect(
      await previewLink.getAttribute('href'),
      'FR-024/S-8.1: WebKit must get the Mode 2 fallback href (never both ' +
        'links). PIN: green today and must survive GREEN unchanged.',
    ).toBe(fakeUrl)
  })
})

test.describe('order 32 — S-4.4 planted-cookie recovery in a real browser', () => {
  test('a Domain=localhost omnipus-session plant (incl. the trailing-slash /api/ form) is cleared once and the retry succeeds — no forced logout', async ({ page }) => {
    test.setTimeout(180_000)

    // Logged-in baseline BEFORE the plant (the plant must be the only thing
    // that changes between "app works" and "recovery flow").
    await page.goto('/')
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page, { timeout: 15_000 })

    // The plant (S-4.4: "a served preview app has tossed a Domain=localhost
    // omnipus-session plant (including the trailing-slash /api/ form)") —
    // planted out-of-band here exactly as a Mode 1 toss would land in the jar:
    // Domain-qualified, so never the genuine cookie (FR-015: the genuine
    // omnipus-session is host-only at exactly Path=/ with no Domain, ever).
    await page.context().addCookies([
      {
        name: 'omnipus-session',
        value: 'e2e-planted-domain-root-' + Date.now(),
        domain: 'localhost',
        path: '/',
      },
      {
        name: 'omnipus-session',
        value: 'e2e-planted-domain-api-' + Date.now(),
        domain: 'localhost',
        path: '/api/',
      },
    ])

    // Drive a SPA call with the duplicated Cookie header (a reload makes the
    // SPA's own boot calls — every main-Host /api/v1 request now carries all
    // three omnipus-session values).
    await page.reload()
    await expect(
      page.getByRole('banner'),
      'the app must still be up for the recovery flow to be reachable',
    ).toBeVisible({ timeout: 30_000 })

    // S-4.2/S-4.4: the SPA renders the typed error with a Retry action —
    // ONE toast, the gateway's Q4 message, a Retry button (orders 27/28 own
    // the unit-level pins; this row proves the real-browser flow).
    const toast = page.getByRole('alert').filter({ hasText: Q4_MESSAGE })
    await expect(
      toast,
      'RED today: no planted-cookie detector exists (FR-015) and the SPA has ' +
        'no retry toast (Q4) — the duplicate cookies pass unnoticed and no ' +
        'typed error ever appears',
    ).toBeVisible({ timeout: 30_000 })
    await expect(
      toast,
      'exactly ONE recovery toast (order 27: never a duplicate)',
    ).toHaveCount(1)

    // Retry re-issues the request once; afterwards the recovery has landed:
    // exactly one omnipus-session remains (the plants were cleared), the app
    // is intact (no forced logout — orders 27/28 pin the SPA side).
    await toast.getByRole('button', { name: /retry/i }).click()
    await expect
      .poll(
        async () =>
          (await page.context().cookies()).filter(
            (c) => c.name === 'omnipus-session',
          ).length,
        {
          message: 'exactly one omnipus-session must remain after the clear',
          timeout: 15_000,
        },
      )
      .toBe(1)
    await expect(
      page.getByRole('banner'),
      'S-4.4: no forced logout — the app stays up after recovery',
    ).toBeVisible({ timeout: 15_000 })
    const token = await page.evaluate(() =>
      localStorage.getItem('omnipus_auth_token'),
    )
    expect(
      token,
      'no forced logout: the auth token must survive the recovery (a ' +
        'forceLogout would have cleared it and redirected to login)',
    ).toBeTruthy()
  })
})
