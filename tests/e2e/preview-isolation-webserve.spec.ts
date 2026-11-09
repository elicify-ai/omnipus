/**
 * preview-isolation-webserve.spec.ts — ADR-094 (#798 preview isolation) TDD
 * Plan orders 24, 30, 31 and 32 — the E2E half, reconciled with the current
 * contracts and the real auth model in the #950 fix round (review F4, plus
 * F2's browser positive controls).
 *
 * Traces to: docs/internal/specs/adr-094-preview-isolation-spec.md
 *   Order 24 (S-2.1, S-2.2, S-8.1): Mode 1 Chromium+Firefox open the agent's
 *     app on its label origin with working storage; the S-2.2 controls hold
 *     in a real browser; the WebKit card falls back to Mode 2 (FR-024).
 *   Order 30 (S-2.13, US-2 AC3): the #798 attack from a served /preview/
 *     document — static AND dev-proxy — must not change server state. Mode 2
 *     must BLOCK the forbidden reads and writes; the test never requires a
 *     forbidden read to succeed.
 *   Order 31 (S-3.6, S-3.7) + US-3 AC1: Mode 2 renders a real bundle while
 *     isolated — linked stylesheet, module script, fetch('./data.json'),
 *     a working ws:// connection through the dev proxy, a form POST inside
 *     the prefix, popup and download — and out-of-prefix requests stay
 *     blocked.
 *   Order 32 (S-4.4): planted-cookie recovery in a real browser — one toast,
 *     Retry succeeds, exactly one omnipus-session remains, no forced logout.
 *
 * ORACLES — transcribed from the spec, never from running the code:
 *   - ${PREFIX} is /preview/{agent}/{token}/ WITH the trailing slash (the
 *     "Mode 2 response headers" template), so CHILD resources are in scope
 *     and a sibling token that merely shares the string prefix is not.
 *   - Authentication is the HttpOnly omnipus-session cookie
 *     (src/lib/api/http.ts::buildHeaders; src/store/auth.ts keeps no
 *     JS-visible token). Node-side calls ride the browser context's cookie
 *     jar via page.request, plus the X-CSRF-Token header for state changes.
 *   - History is the generated SessionHistoryResponse envelope, read through
 *     tests/e2e/fixtures/session-history.ts::sessionMessages (generated Zod
 *     parse), never a hand-written wire type.
 *   - Q4's exact toast message; FR-015's reserved cookie name.
 *
 * Server-observed vs browser-observed: the attack rows' decisive oracle is
 * server state (the library directory never appears), with an instrument
 * check that the same oracle DOES see a directory a legitimate call creates.
 * CSP violations and rejected fetches are browser-side corroboration.
 *
 * MINT MECHANISM: serve_web registrations are tool-only (no REST endpoint
 * creates one), so a real agent turn writes the fixture files with the real
 * file tool and calls the real serve_web. The model edge of that turn is
 * scripted (fixtures/preview-isolation/mint-provider.ts), so these rows'
 * minting turns make no live-model call (the suite's global setup still
 * preflights the live keys and onboards the shared shard gateway, which these
 * rows do not use). Each test runs on its own private gateway (canonical-history
 * fixture, loopback public_url so Mode 1 mints), so no row shares a preview
 * registry, dev server or workspace with another. The fixture content lives in
 * tests/e2e/fixtures/preview-isolation/webserve-bundle.ts (unit-checked by
 * its vitest companion). Dev-proxy minting requires Linux; elsewhere the row
 * fails loudly at the mint step, never skips.
 *
 * Browser execution belongs to CI's preview-isolation shard.
 */

import { expect, type Page } from '@playwright/test'
import { z } from 'zod'
import { test } from './fixtures/preview-isolation/mint-history'
import { chatInput, waitForConnected } from './fixtures/selectors'
import { freshChat, seedAndOpenSession } from './fixtures/session-setup'
import { sessionMessages } from './fixtures/session-history'
import { mintDirective } from './fixtures/preview-isolation/mint-provider'
import {
  DATA_OK_VALUE,
  MODULE_RAN_VALUE,
  PROBE_LETTER_SPACING,
  WS_HELLO,
  childOfPrefix,
  devBundleFiles,
  isolatedUrlFor,
  siblingTokenUrl,
  staticBundleFiles,
  type BundleFiles,
} from './fixtures/preview-isolation/webserve-bundle'
import {
  LibraryEntry,
  LibraryWorkspaceNode,
  ValidateTokenResponse,
} from '../../src/lib/api/generated/schemas'

/**
 * The origin of THIS test's private gateway (the canonical-history fixture
 * starts one per test, with only its model edge scripted); set before each
 * test so no row shares a preview registry, dev server or workspace with
 * another.
 */
let BASE_URL = ''
test.beforeEach(({ canonicalGateway }) => {
  BASE_URL = canonicalGateway.baseURL
})
/** Q4's exact human message (S-4.2; order 27 pins the SPA side of it). */
const Q4_MESSAGE = 'Omnipus cleared cookies set by a preview — please retry'
/** MINT_TIMEOUT covers one scripted agent turn: a few file writes, then serve_web. */
const MINT_TIMEOUT = 120_000

// ── Auth plumbing: the HttpOnly session cookie rides page.request ───────────

/** CSRF header for a Node-side state-changing call (the SPA's own shape). */
async function csrfHeader(page: Page): Promise<Record<string, string>> {
  const cookies = await page.context().cookies(BASE_URL)
  const csrf = cookies.find((c) => c.name === '__Host-csrf' || c.name === 'csrf')
  if (!csrf) throw new Error('no csrf cookie in the browser context — the app must be loaded and signed in first')
  return { 'Content-Type': 'application/json', 'X-CSRF-Token': csrf.value }
}

/** The SPA's live session id — ChatScreen stamps it on the chat surface. */
async function activeSessionId(page: Page): Promise<string> {
  const el = page.locator('[data-active-session-id]').first()
  await expect(el, 'chat surface must be bound to a session').toBeVisible({ timeout: 15_000 })
  const sid = await el.getAttribute('data-active-session-id')
  if (!sid) throw new Error('data-active-session-id present but empty')
  return sid
}

/** The /api/v1/about mock (web-serve-canonical's pattern). */
async function mockAbout(page: Page): Promise<void> {
  await page.route(`${BASE_URL}/api/v1/about`, async (route) => {
    let base: Record<string, unknown> = {}
    try {
      const real = await route.fetch()
      if (real.ok()) base = (await real.json()) as Record<string, unknown>
    } catch {
      // Gateway not reachable — the stub alone is sufficient for the render path.
    }
    await route.fulfill({
      status: 200,
      contentType: 'application/json',
      body: JSON.stringify({ ...base, preview_enabled: true }),
    })
  })
}

/**
 * Drive one real agent turn whose model edge is scripted: write `files` under
 * `dirName` with the real file tool and call the real serve_web. Polls the generated SessionHistoryResponse (via sessionMessages)
 * until a successful serve_web call for `dirName` is persisted and returns
 * its result exactly as the gateway stored it.
 */
async function mintWebServeViaAgent(
  page: Page,
  dirName: string,
  files: BundleFiles,
  dev: boolean,
): Promise<Record<string, unknown>> {
  await page.goto('/')
  await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
  await waitForConnected(page, { timeout: 15_000 })
  await freshChat(page)

  const input = chatInput(page)
  await expect(input).toBeEnabled({ timeout: 15_000 })
  await input.fill(mintDirective(dirName, files, dev))
  await input.press('Enter')

  const sid = await activeSessionId(page)
  const deadline = Date.now() + MINT_TIMEOUT
  let last = 'never polled'
  while (Date.now() < deadline) {
    let entries: Awaited<ReturnType<typeof sessionMessages>> | undefined
    try {
      entries = await sessionMessages(page, BASE_URL, sid)
    } catch (err) {
      // A read can race the turn's first persist; the last failure is carried
      // into the final error below if it never recovers.
      last = `history read failed: ${String(err)}`
    }
    if (entries) {
      for (const entry of entries) {
        for (const call of entry.tool_calls ?? []) {
          const params = call.parameters as Record<string, unknown> | undefined
          if (call.tool !== 'serve_web' || call.status !== 'success' || params?.path !== dirName) continue
          return serveWebResultOf(call.result, dirName)
        }
      }
      last = `read ${entries.length} history entries, no serve_web success for ${dirName}`
    }
    await page.waitForTimeout(2_000)
  }
  throw new Error(
    `mint failed: no successful serve_web result for "${dirName}" within ${MINT_TIMEOUT} ms ` +
      `(last: ${last}). The agent turn is the only product surface that registers a preview — ` +
      'a failure here is loud, never a skip.',
  )
}

/**
 * A string-returning tool is persisted as `{text: "<model-visible string>"}`
 * (pkg/agent/tool_result_transcript.go::projectRecordedResult); serve_web's
 * string is a JSON object (url, isolated_url, kind, path, expires_at). Same
 * reader as tests/e2e/fixtures/conformance-task-diagnostic.ts.
 */
function serveWebResultOf(saved: Record<string, unknown> | undefined, dirName: string): Record<string, unknown> {
  const text: unknown = saved?.text
  if (typeof text !== 'string') {
    throw new Error(`serve_web succeeded for ${dirName} but persisted no text result: ${JSON.stringify(saved)}`)
  }
  const parsed: unknown = JSON.parse(text)
  if (parsed === null || typeof parsed !== 'object' || Array.isArray(parsed)) {
    throw new Error(`serve_web result for ${dirName} is not a JSON object: ${text}`)
  }
  return parsed as Record<string, unknown>
}

function mode2UrlOf(result: Record<string, unknown>): string {
  const url = String(result.url ?? '')
  expect(url, 'mint must return the Mode 2 url').toContain('/preview/')
  return url
}

// ── In-page probes (self-contained: page.evaluate serialises them) ──────────

/** Starts recording CSP violations on the current document. */
async function recordViolations(page: Page): Promise<void> {
  await page.evaluate(() => {
    const w = window as unknown as { __e2eViolations?: string[] }
    w.__e2eViolations = []
    document.addEventListener('securitypolicyviolation', (e) => {
      w.__e2eViolations!.push(`${e.effectiveDirective || e.violatedDirective} ${e.blockedURI}`)
    })
  })
}

async function violations(page: Page): Promise<string[]> {
  return page.evaluate(() => (window as unknown as { __e2eViolations?: string[] }).__e2eViolations ?? [])
}

interface BundlePositives {
  letterSpacing: string
  moduleRan: unknown
  data: unknown
}

/**
 * US-3 AC1 positive controls, each a CHILD of the prefix: a linked
 * stylesheet's computed effect, a module script that runs, and a relative
 * data fetch. Fails (loudly, via the returned values) under a slashless,
 * exact-file CSP source.
 */
async function probeBundlePositives(page: Page): Promise<BundlePositives> {
  return page.evaluate(async () => {
    const link = document.createElement('link')
    link.rel = 'stylesheet'
    link.href = './style.css'
    await new Promise<void>((resolve) => {
      link.onload = () => resolve()
      link.onerror = () => resolve()
      document.head.appendChild(link)
    })
    const probe = document.getElementById('probe')
    const letterSpacing = probe ? getComputedStyle(probe).letterSpacing : 'no #probe element'

    const script = document.createElement('script')
    script.type = 'module'
    script.src = './app.js'
    await new Promise<void>((resolve) => {
      script.onload = () => resolve()
      script.onerror = () => resolve()
      document.head.appendChild(script)
    })
    const moduleRan = (window as unknown as { __e2eModuleRan?: unknown }).__e2eModuleRan

    let data: unknown
    try {
      const r = await fetch('./data.json')
      data = r.ok ? ((await r.json()) as { ok?: unknown }).ok : `HTTP ${r.status}`
    } catch (err) {
      data = `threw: ${String(err)}`
    }
    return { letterSpacing, moduleRan, data }
  })
}

function expectBundlePositives(p: BundlePositives, what: string): void {
  expect(p.letterSpacing, `${what}: the linked ./style.css must apply (style-src covers the prefix directory)`).toBe(
    PROBE_LETTER_SPACING,
  )
  expect(p.moduleRan, `${what}: the module ./app.js must run (script-src covers the prefix directory)`).toBe(
    MODULE_RAN_VALUE,
  )
  expect(p.data, `${what}: fetch('./data.json') must succeed (connect-src covers the prefix directory)`).toBe(
    DATA_OK_VALUE,
  )
}

interface BundleNegatives {
  apiFetch: 'blocked' | 'allowed'
  siblingFetch: 'blocked' | 'allowed'
  siblingStyleViolation: boolean
  siblingScriptViolation: boolean
  violations: string[]
}

/**
 * Out-of-prefix negative controls: the gateway API and a sibling token that
 * shares the string prefix. A fetch blocked by CSP REJECTS; a fetch the
 * policy allowed resolves even on a 404, so "allowed" here means the policy
 * let it out. Stylesheet/script attempts are judged by the violation event.
 */
async function probeBundleNegatives(
  page: Page,
  sibling: { data: string; css: string; js: string },
): Promise<BundleNegatives> {
  return page.evaluate(async (s) => {
    const seen: string[] = []
    document.addEventListener('securitypolicyviolation', (e) => {
      seen.push(`${e.effectiveDirective || e.violatedDirective} ${e.blockedURI}`)
    })
    const attempt = async (u: string): Promise<'blocked' | 'allowed'> => {
      try {
        await fetch(u, { credentials: 'include' })
        return 'allowed'
      } catch {
        return 'blocked'
      }
    }
    const apiFetch = await attempt('/api/v1/agents')
    const siblingFetch = await attempt(s.data)
    const link = document.createElement('link')
    link.rel = 'stylesheet'
    link.href = s.css
    document.head.appendChild(link)
    const script = document.createElement('script')
    script.type = 'module'
    script.src = s.js
    document.head.appendChild(script)
    await new Promise((r) => setTimeout(r, 1_000))
    return {
      apiFetch,
      siblingFetch,
      siblingStyleViolation: seen.some((v) => v.startsWith('style-src') && v.includes(s.css)),
      siblingScriptViolation: seen.some((v) => v.startsWith('script-src') && v.includes(s.js)),
      violations: seen,
    }
  }, sibling)
}

function expectBundleNegatives(n: BundleNegatives, what: string): void {
  const detail = ` (violations: ${JSON.stringify(n.violations)})`
  expect(n.apiFetch, `${what}: fetch('/api/v1/agents') must be blocked by connect-src${detail}`).toBe('blocked')
  expect(n.siblingFetch, `${what}: a sibling-token fetch must be blocked — the source is a directory, not a string prefix${detail}`).toBe('blocked')
  expect(n.siblingStyleViolation, `${what}: a sibling-token stylesheet must violate style-src${detail}`).toBe(true)
  expect(n.siblingScriptViolation, `${what}: a sibling-token module must violate script-src${detail}`).toBe(true)
}

function siblingTargets(mode2URL: string): { data: string; css: string; js: string } {
  return {
    data: siblingTokenUrl(mode2URL, 'data.json'),
    css: siblingTokenUrl(mode2URL, 'style.css'),
    js: siblingTokenUrl(mode2URL, 'app.js'),
  }
}

// ── The #798 attack (S-2.13) with independently provisioned target state ────

interface AttackTarget {
  wsId: string
  attackPath: string
}

async function listLibraryRoot(page: Page, wsId: string): Promise<Array<z.infer<typeof LibraryEntry>>> {
  const res = await page.request.get(`${BASE_URL}/api/v1/library/${wsId}/entries?path=`)
  expect(res.ok(), `library listing must be readable (oracle): HTTP ${res.status()}`).toBe(true)
  return z.array(LibraryEntry).parse(await res.json())
}

async function libraryHas(page: Page, wsId: string, name: string): Promise<boolean> {
  return (await listLibraryRoot(page, wsId)).some((e) => e.name === name || e.path === name)
}

/**
 * Provisions the attack target from Node (never from the preview): a
 * workspace id and a fresh directory name that must not exist. Proves the
 * oracle can see a landed mkdir by creating and deleting a control directory
 * through a legitimate call first.
 */
async function provisionAttackTarget(page: Page, tag: string): Promise<AttackTarget> {
  const wsRes = await page.request.get(`${BASE_URL}/api/v1/library/workspaces`)
  expect(wsRes.ok(), `GET /api/v1/library/workspaces: HTTP ${wsRes.status()}`).toBe(true)
  const workspaces = z.array(LibraryWorkspaceNode).parse(await wsRes.json())
  expect(workspaces.length, 'a workspace with a work tree must exist (attack target)').toBeGreaterThan(0)
  const wsId = workspaces[0].id

  const control = `attack-control-${tag}`
  const mk = await page.request.post(`${BASE_URL}/api/v1/library/${wsId}/mkdir`, {
    headers: await csrfHeader(page),
    data: { path: control },
  })
  expect(mk.ok(), `instrument check: a legitimate mkdir must succeed (HTTP ${mk.status()})`).toBe(true)
  expect(await libraryHas(page, wsId, control), 'instrument check: the oracle must see a directory that landed').toBe(true)
  const del = await page.request.delete(
    `${BASE_URL}/api/v1/library/${wsId}/entries?path=${encodeURIComponent(control)}`,
    { headers: await csrfHeader(page) },
  )
  expect(del.ok(), `control cleanup: HTTP ${del.status()}`).toBe(true)

  const attackPath = `attack-${tag}`
  expect(await libraryHas(page, wsId, attackPath), 'baseline: the attack directory must not exist before the attack').toBe(false)
  return { wsId, attackPath }
}

interface AttackReport {
  csrfFound: boolean
  listFetch: 'blocked' | 'allowed'
  mkdirFetch: 'blocked' | 'allowed'
  mkdirStatus: number
  violations: string[]
}

/**
 * Runs INSIDE the served preview document: reads document.cookie for the
 * csrf value (S-2.13's first step), then attempts a workspace read and the
 * mkdir POST with the echoed X-Csrf-Token. Under a correct Mode 2 policy
 * both fetches are BLOCKED; nothing here requires them to succeed.
 */
async function runAttackInPreview(page: Page, target: AttackTarget): Promise<AttackReport> {
  return page.evaluate(async (t) => {
    const seen: string[] = []
    document.addEventListener('securitypolicyviolation', (e) => {
      seen.push(`${e.effectiveDirective || e.violatedDirective} ${e.blockedURI}`)
    })
    let csrfValue = ''
    const pairs = document.cookie.split(/;\s*/)
    for (const name of ['__Host-csrf', 'csrf']) {
      const pair = pairs.find((p) => p.startsWith(`${name}=`))
      if (pair) {
        csrfValue = pair.slice(name.length + 1)
        break
      }
    }
    const report: AttackReport = {
      csrfFound: csrfValue !== '',
      listFetch: 'blocked',
      mkdirFetch: 'blocked',
      mkdirStatus: 0,
      violations: seen,
    }
    try {
      await fetch('/api/v1/library/workspaces', { credentials: 'include' })
      report.listFetch = 'allowed'
    } catch {
      report.listFetch = 'blocked'
    }
    try {
      const res = await fetch(`/api/v1/library/${t.wsId}/mkdir`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'X-Csrf-Token': csrfValue },
        body: JSON.stringify({ path: t.attackPath }),
      })
      report.mkdirFetch = 'allowed'
      report.mkdirStatus = res.status
    } catch {
      report.mkdirFetch = 'blocked'
    }
    await new Promise((r) => setTimeout(r, 300))
    return report
  }, target)
}

/**
 * The form vector (US-2 AC3 row 2): a form POST to /api/v1 must be stopped
 * by form-action. Observed as: the browser never navigates out of the
 * prefix, a form-action violation fires, and (server-side, by the caller) no
 * directory lands.
 */
async function attemptApiFormPost(page: Page, target: AttackTarget): Promise<{ urlAfter: string; violations: string[] }> {
  await recordViolations(page)
  const before = page.url()
  await page.evaluate((t) => {
    const form = document.createElement('form')
    form.method = 'POST'
    form.action = `/api/v1/library/${t.wsId}/mkdir`
    const field = document.createElement('input')
    field.name = 'path'
    field.value = `${t.attackPath}-form`
    form.appendChild(field)
    document.body.appendChild(form)
    // Firefox THROWS from submit() when form-action blocks it (Chromium
    // returns): either way the block is judged below by the violation, the
    // unchanged URL and the server-side oracle, never by this call.
    try {
      form.submit()
    } catch {
      // Blocked submit (Firefox) -- see above.
    }
  }, target)
  await page.waitForTimeout(2_000)
  const urlAfter = page.url()
  return { urlAfter, violations: urlAfter === before ? await violations(page) : ['(navigated away)'] }
}

async function expectAttackDenied(page: Page, mode2URL: string, target: AttackTarget, what: string): Promise<void> {
  await page.goto(mode2URL)
  await expect(page.locator('#probe')).toBeVisible()

  // Positive control in the SAME document: in-prefix fetch works, so a
  // blocked API fetch below is the policy, not a broken page or fetch.
  const positives = await probeBundlePositives(page)
  expect(positives.data, `${what}: in-prefix fetch('./data.json') must work (positive control)`).toBe(DATA_OK_VALUE)

  const report = await runAttackInPreview(page, target)
  expect(
    report.csrfFound,
    `${what}: instrument check — a same-origin Mode 2 document CAN read the csrf cookie, so stopping ` +
      'the attack is the job of the request controls, not of cookie absence',
  ).toBe(true)
  expect(report.listFetch, `${what}: GET /api/v1/library/workspaces must be blocked by connect-src (US-2 AC3)`).toBe('blocked')
  expect(
    report.mkdirFetch,
    `${what}: the mkdir POST must be blocked by connect-src (got HTTP ${report.mkdirStatus}; ` +
      `violations ${JSON.stringify(report.violations)})`,
  ).toBe('blocked')
  expect(
    report.violations.some((v) => v.startsWith('connect-src')),
    `${what}: the browser must report a connect-src violation (corroboration)`,
  ).toBe(true)

  const form = await attemptApiFormPost(page, target)
  expect(form.urlAfter, `${what}: a form POST to /api/v1 must not navigate (form-action)`).not.toContain('/api/v1/')
  expect(
    form.violations.some((v) => v.startsWith('form-action')),
    `${what}: the browser must report a form-action violation (got ${JSON.stringify(form.violations)})`,
  ).toBe(true)

  // Decisive, server-observed oracle.
  expect(
    await libraryHas(page, target.wsId, target.attackPath),
    `S-2.13 (${what}): the #798 attack LANDED — a fetch from the served /preview/ document created ` +
      `${target.attackPath} in workspace ${target.wsId}.`,
  ).toBe(false)
  expect(
    await libraryHas(page, target.wsId, `${target.attackPath}-form`),
    `S-2.13 (${what}): the form-POST variant of the attack LANDED.`,
  ).toBe(false)
}

// ── Mode 2 header facts (attack-relevant subset; order 14 owns the full
//    byte-identical tripwire in the backend pack) ───────────────────────────

function expectMode2AttackHeaders(csp: string, what: string): void {
  expect(csp.includes("frame-ancestors 'none'"), `${what}: frame-ancestors must be 'none'`).toBe(true)
  expect(/(^|;)\s*sandbox\b/.test(csp), `${what}: no sandbox directive may exist in the web_serve model (FR-004)`).toBe(false)
  expect(
    /connect-src[^;]*'self'/.test(csp),
    `${what}: connect-src must not carry 'self' — the whole-origin grant that lets the attack reach /api/v1`,
  ).toBe(false)
}

test.describe('order 30 — S-2.13 the #798 attack is denied (US-2 AC3)', () => {
  test('static path: a served /preview/ document cannot drive an authenticated state change', async ({ page }) => {
    test.setTimeout(360_000)
    const dirName = `e2e-srv-${Date.now()}`
    const mode2URL = mode2UrlOf(await mintWebServeViaAgent(page, dirName, staticBundleFiles(dirName), false))
    const target = await provisionAttackTarget(page, dirName)
    await expectAttackDenied(page, mode2URL, target, 'static path')
    const served = await page.request.get(mode2URL)
    expectMode2AttackHeaders(served.headers()['content-security-policy'] ?? '', 'static path')
  })

  test('dev-proxy path: a dev-served /preview/ document cannot drive an authenticated state change', async ({ page }) => {
    test.setTimeout(360_000)
    const dirName = `e2e-dev-${Date.now()}`
    const mode2URL = mode2UrlOf(await mintWebServeViaAgent(page, dirName, devBundleFiles(dirName), true))
    const target = await provisionAttackTarget(page, dirName)
    await expectAttackDenied(page, mode2URL, target, 'dev-proxy path')
    const served = await page.request.get(mode2URL)
    expectMode2AttackHeaders(served.headers()['content-security-policy'] ?? '', 'dev-proxy path')
  })
})

test.describe('order 31 — US-3 AC1 / S-3.6 / S-3.7 Mode 2 renders a real bundle while isolated', () => {
  test('US-3 AC1 (static): stylesheet, module script and relative data fetch work; out-of-prefix stays blocked', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-bnd-${Date.now()}`
    const mode2URL = mode2UrlOf(await mintWebServeViaAgent(page, dirName, staticBundleFiles(dirName), false))
    await page.goto(mode2URL)
    await expect(page).toHaveTitle(dirName)
    expectBundlePositives(await probeBundlePositives(page), 'static bundle')
    expectBundleNegatives(await probeBundleNegatives(page, siblingTargets(mode2URL)), 'static bundle')
  })

  test('US-3 AC1 (dev proxy): stylesheet, module script and relative data fetch work; out-of-prefix stays blocked', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-dbn-${Date.now()}`
    const mode2URL = mode2UrlOf(await mintWebServeViaAgent(page, dirName, devBundleFiles(dirName), true))
    await page.goto(mode2URL)
    await expect(page).toHaveTitle(dirName)
    expectBundlePositives(await probeBundlePositives(page), 'dev bundle')
    expectBundleNegatives(await probeBundleNegatives(page, siblingTargets(mode2URL)), 'dev bundle')
  })

  test('S-3.6: a hot-reload ws:// connection inside the prefix opens and receives data through the dev proxy', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-hmr-${Date.now()}`
    const mode2URL = mode2UrlOf(await mintWebServeViaAgent(page, dirName, devBundleFiles(dirName), true))
    await page.goto(mode2URL)
    await expect(page.locator('#probe')).toBeVisible()

    const hmrUrl = childOfPrefix(mode2URL, 'hmr').replace(/^http/, 'ws')
    const outcome = await page.evaluate(
      ({ inPrefix }) =>
        new Promise<{ message: string | null; inPrefixViolations: string[]; outOfPrefix: 'blocked' | 'opened' }>(
          (resolve) => {
            const seen: string[] = []
            document.addEventListener('securitypolicyviolation', (e) => {
              seen.push(`${e.effectiveDirective || e.violatedDirective} ${e.blockedURI}`)
            })
            let message: string | null = null
            let outOfPrefix: 'blocked' | 'opened' = 'blocked'
            const ws = new WebSocket(inPrefix)
            ws.onmessage = (ev) => {
              message = String(ev.data)
              ws.close()
            }
            // Negative control: the gateway's own WebSocket, out of prefix.
            try {
              const bad = new WebSocket(`${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/v1/ws`)
              bad.onopen = () => {
                outOfPrefix = 'opened'
                bad.close()
              }
            } catch {
              outOfPrefix = 'blocked'
            }
            setTimeout(
              () => resolve({ message, inPrefixViolations: seen.filter((v) => v.includes('/preview/')), outOfPrefix }),
              5_000,
            )
          },
        ),
      { inPrefix: hmrUrl },
    )
    expect(
      outcome.inPrefixViolations,
      'S-3.6: connect-src must carry the ws-scheme form of the prefix directory',
    ).toHaveLength(0)
    expect(outcome.message, 'S-3.6: the ws:// connection must reach the dev server and receive its frame').toBe(WS_HELLO)
    expect(outcome.outOfPrefix, 'negative control: a WebSocket to /api/v1/ws must be blocked by connect-src').toBe('blocked')
  })

  test('S-3.7: a form POST to a child of the prefix navigates (no form-action block)', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-form-${Date.now()}`
    const mode2URL = mode2UrlOf(await mintWebServeViaAgent(page, dirName, staticBundleFiles(dirName), false))
    await page.goto(mode2URL)
    await expect(page.locator('#probe')).toBeVisible()

    const target = childOfPrefix(mode2URL, 'posted')
    const posted = page.waitForRequest((r) => r.url() === target && r.method() === 'POST', { timeout: 10_000 })
    await page.evaluate((action) => {
      const form = document.createElement('form')
      form.method = 'POST'
      form.action = action
      document.body.appendChild(form)
      form.submit()
    }, target)
    await posted
    await page.waitForURL(target, { timeout: 10_000 })
    expect(page.url(), 'S-3.7: the form navigation inside the prefix happened').toBe(target)
  })

  test('S-3.7: a popup and a download work from a static preview (no sandbox directive)', async ({ page }) => {
    test.setTimeout(300_000)
    const dirName = `e2e-pop-${Date.now()}`
    const mode2URL = mode2UrlOf(await mintWebServeViaAgent(page, dirName, staticBundleFiles(dirName), false))
    await page.goto(mode2URL)
    await expect(page.locator('#probe')).toBeVisible()

    const popup = page.waitForEvent('popup', { timeout: 10_000 })
    const opened = await page.evaluate(() => window.open(location.href, '_blank') !== null)
    const popupPage = await popup
    expect(opened, 'S-3.7: window.open must work from a preview document (no sandbox)').toBe(true)
    await expect(popupPage.locator('#probe')).toBeVisible()
    await popupPage.close()

    const downloadPromise = page.waitForEvent('download', { timeout: 10_000 })
    await page.evaluate(() => {
      const a = document.createElement('a')
      a.href = './data.json'
      a.download = 'e2e-download-probe.json'
      document.body.appendChild(a)
      a.click()
    })
    const download = await downloadPromise
    expect(download.suggestedFilename(), 'S-3.7: the download must start with the suggested filename').toBe(
      'e2e-download-probe.json',
    )
  })
})

test.describe('order 24 — S-2.1/S-2.2/S-8.1 Mode 1 + WebKit fallback', () => {
  test('S-2.1: Mode 1 (Chromium/Firefox) — the label URL opens the agent-built app with working storage', async ({ page, browserName }) => {
    test.skip(browserName === 'webkit', 'holdout H-2: Linux WebKit *.localhost resolution is system-resolver-dependent (spec E2E wiring note) — a WebKit pass would say nothing about Safari')
    test.setTimeout(300_000)
    const dirName = `e2e-m1-${Date.now()}`
    const result = await mintWebServeViaAgent(page, dirName, staticBundleFiles(dirName), false)

    const isolated = String(result.isolated_url ?? '')
    expect(
      isolated,
      'BLOCKED: the serve_web result carries no isolated_url — FR-001/FR-011 (Mode 1 minting) ' +
        `is not in effect for this gateway. Minted result keys: ${Object.keys(result).join(',')}`,
    ).toBeTruthy()

    await page.goto(isolated)
    // The page is the agent's fixture, not the Omnipus shell.
    await expect(page, 'S-2.1: the label origin serves the agent-built app').toHaveTitle(dirName)
    await expect(page.locator('#probe')).toHaveText(`${dirName}-ready`)
    expectBundlePositives(await probeBundlePositives(page), 'Mode 1 label origin')
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
    const result = await mintWebServeViaAgent(page, dirName, staticBundleFiles(dirName), false)
    const isolated = String(result.isolated_url ?? '')
    expect(
      isolated,
      `BLOCKED: no isolated_url — FR-001/FR-011 (Mode 1 minting) not in effect. Result keys: ${Object.keys(result).join(',')}`,
    ).toBeTruthy()

    await page.goto(isolated)
    await expect(page.locator('#probe')).toBeVisible()
    const controls = await page.evaluate(
      (gatewayOrigin) =>
        new Promise<{ fetchReadable: boolean | null; wsOpened: boolean | null }>((resolve) => {
          const out: { fetchReadable: boolean | null; wsOpened: boolean | null } = { fetchReadable: null, wsOpened: null }
          fetch(`${gatewayOrigin}/api/v1/agents`, { credentials: 'include' })
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
          const ws = new WebSocket(`${gatewayOrigin.replace(/^http/, 'ws')}/api/v1/ws`)
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
      'S-2.2 control 1: the CORS layer must not reflect a label Origin, so the script must not be able ' +
        'to READ the cross-origin response (browser-side claim)',
    ).toBe(false)
    expect(controls.wsOpened, 'S-2.2 control 3: the WS origin check must refuse a label-origin connection').toBe(false)
  })

  /**
   * A saved canonical serve_web call (F3's replay path) with a VALID
   * isolated_url for this origin, in the shape the gateway really persists:
   * serve_web returns a string, recorded as {text: "<that JSON string>"}
   * (pkg/agent/tool_result_transcript.go::projectRecordedResult; read back the
   * same way by serveWebResultOf above).
   */
  function servedTranscript(tag: string, isolated: string) {
    const fakePath = `/preview/mia/iso-card-token-${tag}/`
    const fakeUrl = `${BASE_URL}${fakePath}`
    return {
      fakeUrl,
      entries: [
        {
          id: `user-${tag}-1`,
          role: 'user' as const,
          content: 'serve my workspace',
          timestamp: new Date(Date.now() - 5000).toISOString(),
          agent_id: '',
        },
        {
          id: `asst-${tag}-1`,
          role: 'assistant' as const,
          content: 'Served.',
          timestamp: new Date(Date.now() - 4000).toISOString(),
          agent_id: 'mia',
          tool_calls: [
            {
              id: `tc-${tag}-serve`,
              tool: 'serve_web',
              status: 'success',
              duration_ms: 80,
              parameters: { path: 'e2e', duration_seconds: 3600 },
              result: {
                text: JSON.stringify({
                  kind: 'static',
                  path: fakePath,
                  url: fakeUrl,
                  expires_at: new Date(Date.now() + 3600 * 1000).toISOString(),
                  isolated_url: isolated,
                }),
              },
            },
          ],
        },
      ],
    }
  }

  test('S-8.1 (Chromium/Firefox): a saved serve_web result carrying isolated_url renders the Mode 1 link', async ({ page, browserName }) => {
    test.skip(browserName === 'webkit', 'FR-024: this expectation is Chromium-family/Firefox-only; the WebKit fallback is the next row')
    await mockAbout(page)
    const isolated = isolatedUrlFor(BASE_URL, 'e2e-card')
    const { entries } = servedTranscript('t24a', isolated)
    await seedAndOpenSession(page, 'preview-iso-card-t24a', entries)

    const previewLink = page.locator('[data-testid="preview-link"]')
    await expect(previewLink, 'the card must render exactly one preview link').toHaveCount(1)
    await expect(previewLink, 'FR-024/S-8.1: on a Mode-1-capable engine the href is the isolated_url').toHaveAttribute(
      'href',
      isolated,
    )
  })

  test('S-8.1 (WebKit pin): the same saved serve_web result renders the Mode 2 fallback link', async ({ page, browserName }) => {
    test.skip(browserName !== 'webkit', 'the Mode 2 fallback expectation is WebKit-specific (FR-024)')
    await mockAbout(page)
    const isolated = isolatedUrlFor(BASE_URL, 'e2e-card')
    const { entries, fakeUrl } = servedTranscript('t24b', isolated)
    await seedAndOpenSession(page, 'preview-iso-card-t24b', entries)

    const previewLink = page.locator('[data-testid="preview-link"]')
    await expect(previewLink, 'the card must render exactly one preview link').toHaveCount(1)
    await expect(previewLink, 'FR-024/S-8.1: WebKit gets the Mode 2 fallback href (never both links)').toHaveAttribute(
      'href',
      fakeUrl,
    )
  })
})

test.describe('order 32 — S-4.4 planted-cookie recovery in a real browser', () => {
  test('a planted /api/ omnipus-session beside the genuine one is cleared, Retry succeeds, no forced logout', async ({ page, canonicalGateway }) => {
    test.setTimeout(180_000)

    // Signed-in baseline BEFORE the plant: the plant is the only change. The
    // private gateway's storage state carries only its cookies; the SPA's
    // sign-in hint is what a UI login stores (src/store/auth.ts::setUsername),
    // so it is set once here, then the app is reloaded with it.
    await page.goto('/')
    await page.evaluate((name) => localStorage.setItem('omnipus_auth_username', name), canonicalGateway.adminUsername)
    await page.reload()
    await expect(page.getByRole('banner')).toBeVisible({ timeout: 15_000 })
    await waitForConnected(page, { timeout: 15_000 })
    const usernameBefore = await page.evaluate(() => localStorage.getItem('omnipus_auth_username'))
    expect(usernameBefore, 'baseline: the browser is signed in').toBeTruthy()

    // Record which SPA GET met the planted-cookie 401, so the Retry can be
    // checked against that exact request.
    const plantedGets: string[] = []
    page.on('response', async (res) => {
      if (res.status() !== 401 || res.request().method() !== 'GET') return
      try {
        const body = (await res.json()) as { code?: unknown }
        if (body.code === 'planted_cookie_cleared') plantedGets.push(res.url())
      } catch {
        // Not a JSON body — not the typed planted-cookie response.
      }
    })

    // The plant: a second omnipus-session for the /api/ tree. The genuine
    // cookie is host-only at exactly Path=/ (FR-015). On host "localhost" a
    // Domain=localhost cookie is stored HOST-ONLY (RFC 6265 5.3 step 5:
    // "localhost" is a public suffix equal to the request host), so a plant
    // at Path=/ -- with or without Domain -- is the same jar entry as the
    // genuine cookie and REPLACES it (verified in Chromium and Firefox): the
    // request then carries no session and the app signs out for a reason
    // that has nothing to do with the guard. A Domain=localhost toss from a
    // Mode 1 label host is rejected outright by both engines. A /api/ plant
    // is a different jar entry, so the genuine cookie stays and every /api/
    // request carries BOTH values -- the duplicated Cookie header the guard
    // exists to answer.
    await page.context().addCookies([
      { name: 'omnipus-session', value: `e2e-planted-api-${Date.now()}`, domain: 'localhost', path: '/api/' },
    ])
    expect(
      (await page.context().cookies(BASE_URL)).filter((c) => c.name === 'omnipus-session' && c.path === '/').length,
      'the plant must not displace the genuine Path=/ session cookie',
    ).toBe(1)

    // A reload makes the SPA's own boot GETs carry the duplicated Cookie header.
    await page.reload()

    const toast = page.getByRole('alert').filter({ hasText: Q4_MESSAGE })
    await expect(toast, 'S-4.2/S-4.4: the typed planted-cookie error surfaces as the recovery toast').toBeVisible({
      timeout: 30_000,
    })
    await expect(toast, 'exactly ONE recovery toast (order 27: never a duplicate)').toHaveCount(1)
    expect(plantedGets.length, 'the gateway answered a GET with the typed planted-cookie 401').toBeGreaterThan(0)
    const retried = plantedGets[0]

    const retryResponse = page.waitForResponse((r) => r.url() === retried && r.request().method() === 'GET', {
      timeout: 15_000,
    })
    await toast.getByRole('button', { name: /retry/i }).click()
    expect((await retryResponse).status(), `S-4.4: the Retry of ${retried} succeeds`).toBe(200)

    await expect
      .poll(async () => (await page.context().cookies(`${BASE_URL}/api/`)).filter((c) => c.name === 'omnipus-session').length, {
        message: 'exactly one omnipus-session must remain for /api/ after the clear (the plant is gone, the genuine one stays)',
        timeout: 15_000,
      })
      .toBe(1)

    // No forced logout: still on the app, the sign-in hint survives, no logout reason recorded.
    await expect(page.getByRole('banner'), 'S-4.4: the app stays up after recovery').toBeVisible({ timeout: 15_000 })
    expect(page.url(), 'no redirect to the login screen').not.toMatch(/#\/login/)
    expect(await page.evaluate(() => sessionStorage.getItem('omnipus_logout_reason'))).toBeNull()
    expect(await page.evaluate(() => localStorage.getItem('omnipus_auth_username'))).toBe(usernameBefore)

    // The session itself is valid and usable (auth is the HttpOnly cookie).
    const validate = await page.request.get(`${BASE_URL}/api/v1/auth/validate`)
    expect(validate.status(), 'the genuine session validates after recovery').toBe(200)
    ValidateTokenResponse.parse(await validate.json())
    const op = await page.request.get(`${BASE_URL}/api/v1/library/workspaces`)
    expect(op.status(), 'an authenticated operation succeeds after recovery').toBe(200)
    z.array(LibraryWorkspaceNode).parse(await op.json())
  })
})
