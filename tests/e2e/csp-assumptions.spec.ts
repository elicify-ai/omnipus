/**
 * csp-assumptions.spec.ts — the SPA's Content-Security-Policy, measured.
 *
 * ## Why this file exists
 *
 * `pkg/gateway/embed.go`'s policy shipped as an explicitly UNMEASURED proposal.
 * ADR-067 §10.7 said so in its own words, and listed six assumptions with the
 * symptom each would produce if wrong. One of them — "Shiki needs no
 * WebAssembly" — turned out to be false in every released build: Shiki's default
 * Oniguruma engine is WebAssembly, `script-src 'self'` refuses
 * `WebAssembly.instantiate`, react-shiki swallowed the CompileError, and EVERY
 * code block rendered as an empty box. It was console-visible the whole time and
 * nobody was looking, because no test opened a browser and read the console.
 *
 * This file is that instrument. One test per assumption, each driving the REAL
 * user journey against the REAL embedded build, with the browser's own CSP
 * reporting as the oracle.
 *
 * ## The oracle, and why the console is part of it
 *
 * Two channels, because neither alone is sufficient:
 *
 *   1. `securitypolicyviolation` DOM events, collected by an init script. This
 *      is the precise channel — it carries the effective directive and the
 *      blocked URI. It sees violations in the DOCUMENT's realm ONLY.
 *   2. Browser console messages. A violation inside a WORKER's realm never
 *      reaches the document's `securitypolicyviolation` listener, so for the
 *      PDF.js worker the console is the ONLY passive channel. This is exactly
 *      how the qcms defect below hid: `[violations] n=0` while the console
 *      carried a CompileError naming the policy verbatim.
 *
 * Neither is trusted on its own. Every test that asserts "no violations" is
 * paired with a positive control in the same file (`A0`) proving both channels
 * fire on a real violation, so "zero" means "measured zero" and not "the
 * listener was never wired".
 *
 * ## What a passing run of this file does NOT establish
 *
 *  * Chromium only. ADR-067 §10.7's freeze condition asks for a headed run on
 *    Chromium, Firefox AND Safari; this file runs on the default project. The
 *    engines genuinely differ on CSP, so a green here is one engine's answer.
 *  * `connect-src`'s `stun:`/`turn:`/`turns:` sources are NOT exercised — that
 *    needs a live WebRTC session against a real ICE server. §10.7's 2026-08-23
 *    amendment is the only measurement those sources have.
 *  * The `frame-src 'self'` embedding surface (the Library's sandboxed HTML
 *    frame) is UNREACHABLE in a production build: `PREVIEW_TOKEN_MINTER` in
 *    `LibraryPreviewPane.tsx` is `null`, so the pane renders "Preview
 *    unavailable" and no iframe is created. A6 below measures what CAN be
 *    measured — that the SPA refuses to be framed — and says so rather than
 *    claiming the embedding path is covered.
 */

import { test, expect, type Page, type Worker as PwWorker } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'

// A CSP regression is not a property a retry establishes: an assertion allowed
// to retry reports identically to one that passed first time, so a policy
// regression failing two runs in three would ship green. Pinned at describe
// level so raising the suite-global count cannot reach it.
test.describe.configure({ retries: 0 })

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

/** The PDF.js worker's URL suffix — the realm whose policy A2b measures. */
const PDFJS_WORKER_SUFFIX = '/pdfjs/pdf.worker.min.mjs'

// ─────────────────────────────────────────────────────────────────────────────
// The instrument
// ─────────────────────────────────────────────────────────────────────────────

interface DocumentViolation {
  directive: string
  blockedURI: string
  source: string
}

interface Recorder {
  /** Violations reported by the document's own realm. */
  documentViolations: () => Promise<DocumentViolation[]>
  /** Every console line that names a CSP refusal or a WebAssembly failure. */
  consoleHits: string[]
}

/**
 * Wire both channels before any page script runs.
 *
 * The console filter deliberately includes `WebAssembly` and `CompileError` as
 * well as the CSP wording: the qcms failure surfaces as
 * `Warning: ICCBased color space: "CompileError: WebAssembly.Module(): ...
 * violates the following Content Security policy directive ..."` — a PDF.js
 * warning that happens to quote the policy, not a browser-issued CSP report.
 * A filter matching only "Refused to" would miss it entirely.
 */
function record(page: Page): Recorder {
  const consoleHits: string[] = []
  void page.addInitScript(() => {
    const w = window as unknown as { __cspViolations: unknown[] }
    w.__cspViolations = []
    document.addEventListener('securitypolicyviolation', (e) => {
      w.__cspViolations.push({
        directive: e.effectiveDirective || e.violatedDirective,
        blockedURI: e.blockedURI,
        source: `${e.sourceFile}:${e.lineNumber}`,
      })
    })
  })
  const interesting =
    /Content Security Policy|Content Security policy|Refused to|CompileError|WebAssembly/i
  page.on('console', (msg) => {
    const text = msg.text()
    if (interesting.test(text)) consoleHits.push(`[${msg.type()}] ${text}`)
  })
  return {
    consoleHits,
    documentViolations: async () =>
      (await page.evaluate(() => {
        const w = window as unknown as { __cspViolations?: DocumentViolation[] }
        const out = w.__cspViolations ?? []
        w.__cspViolations = []
        return out
      })) as DocumentViolation[],
  }
}

/** Wait for the SPA shell to have actually mounted something. */
async function bootApp(page: Page, route = '/'): Promise<void> {
  await page.goto(route)
  await expect(page.locator('#root')).not.toBeEmpty({ timeout: 30_000 })
}

// ─────────────────────────────────────────────────────────────────────────────
// PDF fixtures — built byte by byte so what each exercises is readable
// ─────────────────────────────────────────────────────────────────────────────

function buildPdf(objects: string[]): Buffer {
  const header = '%PDF-1.7\n%\xE2\xE3\xCF\xD3\n'
  let body = ''
  const offsets: number[] = []
  objects.forEach((obj, i) => {
    offsets.push(header.length + body.length)
    body += `${i + 1} 0 obj\n${obj}\nendobj\n`
  })
  const xrefOffset = header.length + body.length
  let xref = `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`
  for (const off of offsets) xref += `${String(off).padStart(10, '0')} 00000 n \n`
  return Buffer.from(
    `${header}${body}${xref}trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\n` +
      `startxref\n${xrefOffset}\n%%EOF\n`,
    'latin1',
  )
}

function contentStream(content: string): string {
  return `<< /Length ${Buffer.byteLength(content, 'latin1')} >>\nstream\n${content}\nendstream`
}

/** A plain Latin document. Draws a filled rectangle, so "pixels appeared" is
 *  independent of which fonts the host happens to have installed. */
function latinPdf(): Buffer {
  return buildPdf([
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] ' +
      '/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    contentStream('0 0 0 rg\n20 150 260 30 re f\nBT /F1 28 Tf 20 60 Td (OMNIPUS PDF) Tj ET\n'),
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ])
}

/**
 * A document whose page paints through an `/ICCBased` colour space.
 *
 * This is the fixture that finds the defect. PDF.js 6 handles ICC profiles with
 * `qcms_bg.wasm` and — unlike `jbig2.wasm` and `openjpeg.wasm` — ships NO
 * JavaScript fallback for it. `IccColorSpace.isUsable` compiles the module,
 * catches whatever it throws, `warn()`s, memoises `false`, and every ICC and
 * DeviceCMYK colour in every document silently uses the crude fallback
 * conversion instead. Nothing is blank; the colours are just wrong.
 *
 * The profile is the one pdfjs-dist itself ships, read out of the embedded SPA
 * tree, so the fixture cannot drift from what the build serves.
 */
function iccPdf(): Buffer {
  const icc = fs.readFileSync(
    path.join(process.cwd(), 'pkg/gateway/spa/pdfjs/iccs/CGATS001Compat-v2-micro.icc'),
  )
  return buildPdf([
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] ' +
      '/Resources << /ColorSpace << /CS0 5 0 R >> >> /Contents 4 0 R >>',
    contentStream('/CS0 cs 0.2 0.3 0.4 sc\n20 20 260 160 re f\n'),
    '[/ICCBased 6 0 R]',
    `<< /N 3 /Length ${icc.length} >>\nstream\n${icc.toString('latin1')}\nendstream`,
  ])
}

const F_LATIN = 'csp-audit-latin.pdf'
const F_ICC = 'csp-audit-icc.pdf'
const F_NOTE = 'csp-audit-note.md'

/** A string that exists nowhere else in the SPA, so finding it proves THIS file
 *  rendered rather than some cached shell. */
const CODE_MARKER = 'cspAuditMarker8f21'

async function seedWorkspace(page: Page): Promise<string> {
  const res = await page.request.get('/api/v1/workspaces')
  expect(res.status(), 'GET /api/v1/workspaces must succeed').toBe(200)
  const list = (await res.json()) as Array<{ id: string; is_default?: boolean }>
  expect(list.length, 'a workspace is required to host the Library').toBeGreaterThan(0)
  const ws = list.find((w) => w.is_default) ?? list[0]
  const workDir = path.join(OMNIPUS_HOME, 'workspaces', ws.id, 'work')
  fs.mkdirSync(workDir, { recursive: true })
  fs.writeFileSync(path.join(workDir, F_LATIN), latinPdf())
  fs.writeFileSync(path.join(workDir, F_ICC), iccPdf())
  fs.writeFileSync(
    path.join(workDir, F_NOTE),
    `# CSP audit note\n\n\`\`\`js\nconst ${CODE_MARKER} = 1\n\`\`\`\n`,
  )
  return ws.id
}

async function openLibraryEntry(page: Page, wsId: string, entry: string): Promise<void> {
  await page.goto(`/#/library?workspace=${wsId}`)
  await expect(page.getByTestId('library-explorer')).toBeVisible({ timeout: 30_000 })
  await page.getByTestId(`library-row-${entry}`).click()
  await expect(page.getByTestId('library-preview-title')).toHaveText(entry, { timeout: 20_000 })
}

/** Wait for the PDF surface to reach a terminal state, surfacing the viewer's
 *  OWN error rather than a bare selector timeout. */
async function pdfOutcome(page: Page): Promise<string> {
  const pages = page.getByTestId('library-pdf-page')
  const errorPane = page.getByTestId('library-pdf-error')
  await expect(page.getByTestId('library-pdf-preview')).toBeVisible({ timeout: 30_000 })
  await expect
    .poll(
      async () => {
        if ((await pages.count()) > 0) return 'rendered'
        if ((await errorPane.count()) > 0) return 'error'
        return 'pending'
      },
      { timeout: 30_000, intervals: [250], message: 'the PDF must reach a terminal state' },
    )
    .not.toBe('pending')
  if ((await pages.count()) > 0) return 'rendered'
  return `error: ${await errorPane.innerText()}`
}

/** The live PDF.js worker handle, or null. Playwright's own CDP-side view of
 *  real dedicated workers — in-page script cannot fabricate this. */
function pdfWorker(page: Page): PwWorker | undefined {
  return page.workers().find((w) => w.url().endsWith(PDFJS_WORKER_SUFFIX))
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

test.describe("ADR-067 §10.7 — the SPA's Content-Security-Policy, measured", () => {
  /**
   * A0 — THE POSITIVE CONTROL, and the reason every "zero violations" below
   * means anything.
   *
   * Three independent things are established here:
   *   1. the policy is actually ON (an inline script does not execute),
   *   2. the DOCUMENT channel fires and carries a directive name,
   *   3. the CONSOLE channel fires.
   *
   * Without this, a broken listener and a clean policy are the same result.
   */
  test('A0 positive control: both violation channels fire on a real violation', async ({
    page,
  }) => {
    const rec = record(page)
    await bootApp(page)

    const inlineRan = await page.evaluate(() => {
      // An inline <script> element: refused by `script-src 'self'`.
      const s = document.createElement('script')
      s.textContent = 'window.__cspAuditInlineRan = true'
      document.body.appendChild(s)
      // An off-origin image: refused by `img-src 'self' data: blob:`.
      const img = document.createElement('img')
      img.src = 'https://csp-audit.invalid/blocked.png'
      document.body.appendChild(img)
      return (window as unknown as { __cspAuditInlineRan?: boolean }).__cspAuditInlineRan === true
    })
    expect(inlineRan, "an inline <script> must not run under script-src 'self'").toBe(false)

    await expect
      .poll(async () => (await rec.documentViolations()).length, { timeout: 10_000 })
      .toBeGreaterThan(0)
    // Chromium words a blocked inline script as "Executing inline script
    // violates the following Content Security Policy directive …" and a blocked
    // subresource as "Refused to load …". Both forms must be accepted, or this
    // control fails for a wording reason rather than a policy one.
    expect(
      rec.consoleHits.join('\n'),
      'the console channel must also carry the refusal — it is the ONLY channel for worker-realm violations',
    ).toMatch(/violates the following Content Security Policy|Refused to/i)
  })

  /**
   * A1 — "no inline bootstrap script → white screen at boot".
   *
   * The oracle is the app itself: the shell boots and mounts real content while
   * `script-src 'self'` is in force, with no script-src violation anywhere. A0
   * has already proved that an inline script under this policy DOES get refused
   * and DOES get reported, so "no script-src violation" here is a measurement
   * rather than a silent listener.
   */
  test('A1 the shell boots with no inline script', async ({ page }) => {
    const rec = record(page)
    await bootApp(page)
    await expect(page.locator('#main-content, main').first()).toBeVisible({ timeout: 30_000 })

    const violations = await rec.documentViolations()
    expect(
      violations.filter((v) => v.directive.startsWith('script-src')),
      'FR-019b: the built shell must need no inline or off-origin script',
    ).toEqual([])
  })

  /**
   * A2 — "worker-src covers the built PDF.js worker URL".
   *
   * PDF.js does NOT fail when its worker is refused; it parses on the main
   * thread and warns, which FR-019c forbids. So the oracle is the WORKER'S
   * EXISTENCE as Playwright sees it, not the absence of an error.
   */
  test('A2 the PDF.js worker is permitted and a PDF renders', async ({ page }) => {
    const rec = record(page)
    const wsId = await seedWorkspace(page)
    await openLibraryEntry(page, wsId, F_LATIN)

    expect(await pdfOutcome(page), 'the Latin PDF must render').toBe('rendered')
    const workerUrls = page.workers().map((w) => w.url())
    expect(
      workerUrls.filter((u) => u.endsWith(PDFJS_WORKER_SUFFIX)),
      'FR-019c: parsing must happen on a real worker, which requires worker-src to permit it. ' +
        `Live workers were: ${JSON.stringify(workerUrls)}`,
    ).toHaveLength(1)

    const violations = await rec.documentViolations()
    expect(
      violations.filter((v) => v.directive.startsWith('worker-src')),
      'worker-src must permit the built worker URL',
    ).toEqual([])
  })

  /**
   * A2b — THE ASSUMPTION THE ORIGINAL LIST NEVER MADE, and the defect this
   * audit found.
   *
   * `vite.config.ts` deliberately ships `pdfjs/wasm/` and the build FAILS if it
   * is missing, because "a scanned PDF (JPEG 2000 / JBIG2) loses images" and
   * "colour profiles are ignored" without it. The SPA's own policy then refused
   * to let any of it compile: the worker script's response carried
   * `script-src 'self'`, and a worker's realm takes its policy from its OWN
   * response headers.
   *
   * TWO oracles, because they fail for different reasons:
   *
   *   1. Inside the worker's realm, `WebAssembly.instantiate` on a minimal
   *      module must SUCCEED. This is a direct read of the realm's policy that
   *      no in-page code can fabricate, and it is independent of any PDF.
   *   2. Opening a real ICC document must produce no `ICCBased color space`
   *      CompileError warning. This is the USER JOURNEY, and it is the channel
   *      the defect actually announced itself on for months.
   */
  test('A2b the PDF.js worker realm can compile the WebAssembly the build ships', async ({
    page,
  }) => {
    const rec = record(page)
    const wsId = await seedWorkspace(page)
    await openLibraryEntry(page, wsId, F_ICC)
    expect(await pdfOutcome(page), 'the ICC PDF must render').toBe('rendered')

    const worker = pdfWorker(page)
    expect(worker, 'the PDF.js worker must be live for its realm to be measurable').toBeTruthy()

    // The 8-byte empty WebAssembly module: valid, and small enough that a
    // failure can only be the policy.
    const wasmVerdict = await (worker as PwWorker).evaluate(async () => {
      const bytes = new Uint8Array([0, 97, 115, 109, 1, 0, 0, 0])
      try {
        await WebAssembly.instantiate(bytes)
        return 'compiled'
      } catch (e) {
        return `refused: ${String(e)}`
      }
    })
    expect(
      wasmVerdict,
      "the worker realm must permit WebAssembly, or pdfjs/wasm/ is shipped and unusable: " +
        'qcms (ICC/DeviceCMYK colour) has NO JavaScript fallback, so every ICC colour ' +
        'silently degrades',
    ).toBe('compiled')

    expect(
      rec.consoleHits.filter((h) => /ICCBased color space/i.test(h)),
      'an ICC document must not report a WebAssembly CompileError — that warning IS the ' +
        'silent-wrong-colour defect, and it is the only symptom the user ever gets',
    ).toEqual([])
  })

  /**
   * A3 — "Tailwind and Radix need inline styles → broken layout".
   *
   * MEASURED NECESSARY, not merely assumed: with `'unsafe-inline'` removed from
   * `style-src`, the Settings screen alone reports 13 `style-src-attr`
   * violations (12 from DOMPurify re-applying sanitised `style=` attributes,
   * one from React). That measurement lives in the audit report; what this test
   * pins is the shipped side of it — zero style violations under the real
   * policy across the screens where they were observed.
   */
  test('A3 inline styles are permitted across the app', async ({ page }) => {
    const rec = record(page)
    await bootApp(page)
    const seen: DocumentViolation[] = []
    for (const route of ['/', '/#/agents', '/#/settings']) {
      await page.goto(route)
      await expect(page.locator('#root')).not.toBeEmpty({ timeout: 30_000 })
      await page.waitForTimeout(2_000)
      seen.push(...(await rec.documentViolations()))
    }
    expect(
      seen.filter((v) => v.directive.startsWith('style-src')),
      "style-src must keep 'unsafe-inline': DOMPurify re-applies sanitised style= attributes",
    ).toEqual([])
  })

  /**
   * A4 — "same-origin WebSocket matches 'self' → the live connection silently
   * fails".
   *
   * The oracle is a socket that reaches OPEN, not merely a `new WebSocket(...)`
   * that threw nothing: a refused connection also constructs without throwing.
   */
  test('A4 the live WebSocket connects under connect-src', async ({ page }) => {
    const rec = record(page)
    const sockets: string[] = []
    page.on('websocket', (ws) => sockets.push(ws.url()))
    // `/` — not `/#/chat`. The router redirects the root to the default
    // workspace's chat, which is where the live socket is opened; `/#/chat` is
    // not a route this build serves, and navigating there produces a shell with
    // no socket at all. Measured 2026-09-05: the first version of this test used
    // `/#/chat` and reported zero sockets — a false RED that said nothing about
    // connect-src.
    await bootApp(page, '/')

    await expect
      .poll(() => sockets.filter((u) => u.includes('/api/v1/chat/ws')).length, {
        timeout: 30_000,
        message: "the SPA's chat WebSocket must open — connect-src 'self' must cover ws://",
      })
      .toBeGreaterThan(0)

    // Independent of the SPA's own socket: open one directly and require OPEN.
    const readyState = await page.evaluate(async () => {
      const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/api/v1/chat/ws`
      const ws = new WebSocket(url)
      return await new Promise<string>((resolve) => {
        ws.onopen = () => {
          ws.close()
          resolve('open')
        }
        ws.onerror = () => resolve('error')
        setTimeout(() => resolve(`timeout(readyState=${ws.readyState})`), 15_000)
      })
    })
    expect(readyState, "a same-origin ws:// URL must be reachable under connect-src 'self'").toBe(
      'open',
    )

    const violations = await rec.documentViolations()
    expect(violations.filter((v) => v.directive.startsWith('connect-src'))).toEqual([])
  })

  /**
   * A5 — "Shiki needs no WebAssembly". MEASURED FALSE on 2026-09-05 and fixed in
   * the library rather than the policy (`createJavaScriptRegexEngine`). This
   * test is the regression guard for that fix.
   *
   * The oracle is the code block's TEXT. "A <pre> exists" is true of the broken
   * state too — react-shiki rendered an empty box, not a missing element — so
   * the marker string is what separates "highlighted" from "disappeared".
   */
  test('A5 code blocks render without WebAssembly', async ({ page }) => {
    const rec = record(page)
    const wsId = await seedWorkspace(page)
    await openLibraryEntry(page, wsId, F_NOTE)

    await expect(page.locator('pre').first()).toContainText(CODE_MARKER, { timeout: 30_000 })
    expect(
      rec.consoleHits.filter((h) => /WebAssembly|CompileError/i.test(h)),
      "Shiki must stay on its pure-JavaScript regex engine — do not re-enable the WASM " +
        'engine and do not widen the policy for it',
    ).toEqual([])
  })

  /**
   * A6 — "nothing embeds the SPA → any embedding surface goes blank".
   *
   * What is measured: `frame-ancestors 'none'` is live and DOES refuse a
   * same-origin embed. What is NOT measured, and is stated here rather than
   * quietly claimed: the app's one embedding surface (the Library's sandboxed
   * HTML frame) never mounts in a production build, because
   * `PREVIEW_TOKEN_MINTER` is `null` — the pane renders "Preview unavailable"
   * instead. So the assumption holds today for a reason that has nothing to do
   * with the policy, and the day that minter lands, this test's second half is
   * the one to extend.
   */
  test('A6 the SPA refuses to be framed', async ({ page }) => {
    const rec = record(page)
    await page.route('**/csp-audit-framer.html', (route) =>
      route.fulfill({
        status: 200,
        contentType: 'text/html',
        body: '<!doctype html><html><body><iframe id="f" src="/"></iframe></body></html>',
      }),
    )
    await page.goto('/csp-audit-framer.html')
    await expect
      .poll(() => rec.consoleHits.filter((h) => /frame-ancestors/i.test(h)).length, {
        timeout: 15_000,
        message: "FR-006b: framing the SPA must be refused by frame-ancestors 'none'",
      })
      .toBeGreaterThan(0)

    const inner = await page.evaluate(() => {
      const f = document.getElementById('f') as HTMLIFrameElement | null
      try {
        return f?.contentDocument?.body?.innerHTML ?? 'NO_CONTENT_DOCUMENT'
      } catch {
        return 'OPAQUE'
      }
    })
    expect(inner, 'the refused frame must expose no SPA chrome').not.toContain('id="root"')

    // The production embedding surface, measured rather than assumed absent.
    const wsId = await seedWorkspace(page)
    fs.writeFileSync(
      path.join(OMNIPUS_HOME, 'workspaces', wsId, 'work', 'csp-audit-page.html'),
      '<!doctype html><html><body><h1>embedded</h1></body></html>',
    )
    await openLibraryEntry(page, wsId, 'csp-audit-page.html')
    await expect(page.getByTestId('library-html-preview-unavailable')).toBeVisible({
      timeout: 20_000,
    })
    expect(
      await page.locator('iframe').count(),
      'no iframe is created today — when PREVIEW_TOKEN_MINTER lands, extend this assertion ' +
        'to check the frame loads from the isolated preview endpoint and not the SPA handler',
    ).toBe(0)
  })
})
