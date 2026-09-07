/**
 * preview-pdf.spec.ts — ADR-067 D3: PDF rendering, laziness, and the worker.
 *
 * Covers FR-018, FR-018a, FR-018b, FR-019a, FR-019c and NB-17; spec tests 61,
 * 75, 80, 82, 96, and the runtime half of 121/84.
 *
 * ── Why this file is headless, and what that costs ──────────────────────────
 * PDF.js draws into a <canvas>, and a canvas renders identically headless, so
 * §0's narrowed derivation leaves this file on the DEFAULT Playwright project.
 * That has one honest consequence, stated here rather than papered over:
 * headless Chromium HAS NO BUILT-IN PDF VIEWER, so "the browser's own viewer
 * did not render this" cannot be MEASURED here. What this file can and does
 * assert is the structural form of that claim — no <embed>/<object>/<iframe>
 * points at the PDF, no download fired, no navigation happened, and the pixels
 * come from a canvas our own component created. The measurement proper is
 * preview-pdf-viewer-control.spec.ts, which runs HEADED for exactly that
 * reason (§13.4 item 4). Do not "strengthen" this file by asserting the
 * browser viewer is absent — headless it is absent regardless, which is a
 * pass for the wrong reason.
 *
 * ── retries: 0, at describe level, deliberately ─────────────────────────────
 * The suite's global `retries: process.env.CI ? 3 : 2` exists for real-LLM
 * latency variance, and none of the tests it was written for are in this file.
 * "Parsing ran on a worker thread" and "no eval path is shipped" are not
 * properties a fourth attempt establishes: a security assertion allowed to
 * retry reports identically to one that passed first time, so a regression
 * that fails two runs in three would ship green (SC-012, §13.4).
 *
 * playwright.config.ts pins `retries: 0` at PROJECT level for the isolation and
 * headed projects. This file is matched by neither — it runs on `default`,
 * which keeps the global count — so the pin is made here instead, at describe
 * level, where raising the global number cannot reach it. Verified by
 * observation, not by reading the docs: during the proof-of-failure runs
 * recorded below, every deliberately-broken assertion failed exactly ONCE and
 * Playwright's summary reported no retry line.
 *
 * ── Oracles, and the ones deliberately NOT used ─────────────────────────────
 *  * Laziness (test 61) is measured as REQUESTS OBSERVED IN ONE SESSION, in two
 *    ordered phases. Phase 1 opens a `.md` and requires ZERO PDF.js requests;
 *    it also asserts the markdown actually rendered, which is what stops phase
 *    1 passing because the app never booted. Phase 2 opens the `.pdf` in the
 *    SAME session and requires the chunk to arrive.
 *  * "Named chunk" is asserted on the URL the browser actually requested
 *    (`/assets/pdfjs-<hash>.js`), plus a PDF.js identity marker fetched back
 *    out of that exact URL. A lazy import alone produces a hash-named chunk, so
 *    a name-matching test with no name to match would match nothing and pass —
 *    the trap FR-018 names explicitly. Zero matches is a FAILURE here, never a
 *    pass.
 *  * The thread (FR-019c, test 96) is asserted THREE independent ways, because
 *    PDF.js does not fail when its worker cannot load — it falls back to
 *    main-thread parsing with a console warning as the only symptom:
 *      1. Playwright's own `page.workers()` — a CDP-side view of real dedicated
 *         workers that in-page code cannot fabricate;
 *      2. code EVALUATED INSIDE that worker's realm, reading its own
 *         `self.location.href` — an observation that can only succeed on a real
 *         separate thread;
 *      3. the message traffic on the port (a wrapped `window.Worker` records
 *         actions), requiring the parse request itself — `GetDocRequest` — to
 *         have crossed to it and replies to have come back. The fake-worker
 *         fallback uses a `LoopbackPort`, which is NOT a Worker, so it records
 *         nothing here.
 *    Plus zero fake-worker console warnings, with a positive control proving
 *    that predicate can fire.
 *  * NOT used as oracles: `getDocument`'s `isEvalSupported` / `enableScripting`
 *    (neither is a `getDocument` parameter in 6.2.108 — asserting them would
 *    pass forever while proving nothing, per FR-019a's correction), and "no
 *    error was thrown" anywhere.
 *
 * ── Fixtures are GENERATED, not committed blobs ─────────────────────────────
 * Every PDF below is built byte by byte in this file, so what each one exercises
 * is readable rather than opaque. The CJK document in particular is the ONLY
 * way FR-018a's cmaps failure is visible: it uses a Type0 font with an EXTERNAL
 * `/UniJIS-UCS2-H` encoding and no `/ToUnicode`, so PDF.js must fetch character
 * maps over HTTP to map its bytes at all. Without `cmaps/` it renders blank
 * with nothing naming the cause — which is the whole point.
 *
 * ── Proof of failure — every test here has been SEEN to fail ────────────────
 * A security test nobody has watched go red is an assertion about nothing. Each
 * of these was run against a deliberately broken system on 2026-08-23,
 * Chromium, and failed exactly once (no retry line, which is also how the
 * describe-level `retries: 0` above was verified):
 *
 *   worker-src 'none' injected into the SPA policy   → test 96 RED
 *     (the FR-019b × FR-019c interaction, made real)
 *   the worker script blocked at the network         → test 96 RED
 *   ditto, with the render gate removed so the        → test 96's oracle 1 RED:
 *     worker oracles report directly                    `page.workers()` holds
 *                                                       no PDF.js worker
 *   PDF.js opened BEFORE phase 1 is sampled          → test 61 RED (9 PDF.js
 *     (what a static import would produce)              requests where 0 are
 *                                                       allowed)
 *   `new Function(…)` appended to the shipped chunk   → FR-019a RED, reporting
 *     and the gateway REBUILT around it                 `new Function(` and
 *                                                       `Function("…")` in the
 *                                                       served bytes
 *   the gateway's PDF.js 404 branch DELETED and       → FR-018b (404) RED: the
 *     rebuilt — missing assets fall back to             missing asset came back
 *     index.html with HTTP 200 again                    200 text/html
 *   the CSP header stripped from the document        → FR-019b RED (the inline
 *                                                       script executed)
 *
 * One negative result worth keeping: an earlier attempt to mutate the 404 case
 * with `page.route('**\/pdfjs/**')` PASSED — `page.route` does not intercept
 * `page.request` calls, so the "mutation" never reached the assertion under
 * test. A proof-of-failure run that comes back green is not evidence the test
 * is strict; it is evidence the mutation missed. That is why the two artefact
 * mutations above are real rebuilds of the gateway rather than interceptions.
 *
 * ── Engines actually observed ───────────────────────────────────────────────
 * Chromium (default project). This file is not in the three-engine isolation
 * matrix by design — see the headless note above.
 */

import { test, expect, type Page, type Request } from '@playwright/test'
import * as fs from 'fs'
import * as path from 'path'
import * as crypto from 'crypto'

// Not a preference — see the header. Applies to every test in this file.
test.describe.configure({ retries: 0 })

// ─────────────────────────────────────────────────────────────────────────────
// Environment
// ─────────────────────────────────────────────────────────────────────────────

/** Mirrors tests/e2e/fixtures/session-setup.ts — the suite's existing contract
 *  for locating the gateway's data directory from a spec. */
const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test')

/** The SPA-relative prefix PDF.js's runtime assets and worker are served from.
 *  Stated by FR-018a/FR-018b and mirrored by both vite.config.ts
 *  (`PDFJS_ASSET_PREFIX`) and pkg/gateway/embed.go (`pdfJSAssetPathPrefix`). */
const PDFJS_PREFIX = '/pdfjs/'

/** The worker file PDF.js parses on. FR-018/FR-019c: copied verbatim from the
 *  package rather than run through Vite's worker pipeline, whose default output
 *  format differs from what PDF.js ships — and getting that wrong produces no
 *  build error, only the silent main-thread fallback. */
const WORKER_URL_SUFFIX = `${PDFJS_PREFIX}pdf.worker.min.mjs`

/** The NAMED chunk. FR-018 requires a name, not merely a lazily-loaded chunk:
 *  a bare dynamic import yields a hash-named file, and a laziness test written
 *  against a name that does not exist matches nothing and passes. */
const PDFJS_CHUNK_RE = /\/assets\/pdfjs-[A-Za-z0-9_-]+\.js(\?.*)?$/

/** Any request belonging to PDF.js — the chunk, the worker, or a runtime asset.
 *  Phase 1 of test 61 requires ZERO of these. */
const PDFJS_ANY_RE = /\/assets\/pdfjs-[A-Za-z0-9_-]+\.js|\/pdfjs\//

/** The four runtime asset directories, exactly as FR-018a enumerates them, with
 *  what each one being absent does. The failure column is the reason this list
 *  is asserted at all: every one of them degrades SILENTLY. */
const RUNTIME_ASSET_DIRS = [
  { dir: 'cmaps', silentFailure: 'a Japanese, Chinese or Korean PDF renders blank' },
  { dir: 'standard_fonts', silentFailure: 'a PDF that embeds no fonts renders with wrong metrics' },
  { dir: 'wasm', silentFailure: 'a scanned PDF loses its images' },
  { dir: 'iccs', silentFailure: 'colour profiles are ignored' },
] as const

// Fixture file names, seeded into the default workspace's work/ tree.
const F_NOTE = 'preview-pdf-note.md'
const F_LATIN = 'preview-pdf-latin.pdf'
const F_CJK = 'preview-pdf-cjk.pdf'
const F_FORM = 'preview-pdf-form.pdf'

/** A string that exists nowhere else in the SPA, so finding it in the rendered
 *  markdown proves THIS file rendered rather than some cached shell. */
const NOTE_MARKER = 'phase-one-markdown-marker-8f21'

/** The three characters the CJK fixture draws: 日本語 (U+65E5 U+672C U+8A9E).
 *  With `/UniJIS-UCS2-H` and no `/ToUnicode`, these reach the text layer ONLY
 *  via the character maps fetched from cmaps/. */
const CJK_TEXT = '日本語'

// ─────────────────────────────────────────────────────────────────────────────
// PDF fixture construction
// ─────────────────────────────────────────────────────────────────────────────

/** Assemble numbered objects into a structurally valid PDF with a real xref
 *  table. Everything is latin1, so string length equals byte length and the
 *  offsets are exact — a PDF whose xref is wrong is still readable by PDF.js
 *  (it reconstructs), which would make a broken fixture look like a working
 *  one. */
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
  const trailer =
    `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\n` +
    `startxref\n${xrefOffset}\n%%EOF\n`
  return Buffer.from(header + body + xref + trailer, 'latin1')
}

function contentStream(content: string): string {
  return `<< /Length ${Buffer.byteLength(content, 'latin1')} >>\nstream\n${content}\nendstream`
}

/**
 * A Latin document using base-14 Helvetica (non-embedded, so it exercises
 * `standard_fonts/`). It draws a FILLED BLACK RECTANGLE as well as text: the
 * rectangle is what makes the non-blank-pixels oracle independent of whether
 * any particular font resolved, so a pixel count of zero means "nothing
 * rendered", never "this glyph was substituted".
 */
function latinPdf(): Buffer {
  const content =
    '0 0 0 rg\n20 150 260 30 re f\n' + 'BT /F1 28 Tf 20 60 Td (OMNIPUS PDF) Tj ET\n'
  return buildPdf([
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] ' +
      '/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    contentStream(content),
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ])
}

/**
 * A Japanese document that CANNOT be read without `cmaps/`.
 *
 * The font is a Type0 with an EXTERNAL predefined encoding (`/UniJIS-UCS2-H`)
 * and a CIDFontType0 descendant in the Adobe-Japan1 ordering, and it carries no
 * `/ToUnicode`. PDF.js therefore has to fetch `cmaps/UniJIS-UCS2-H.bcmap` to
 * turn the string's bytes into CIDs, and `cmaps/Adobe-Japan1-UCS2.bcmap` to turn
 * those CIDs back into the text layer's characters. Remove `cmaps/` and this
 * document renders blank — which is precisely FR-018a's silent failure, and the
 * only shape in which it is visible to a test.
 */
function cjkPdf(): Buffer {
  const content = 'BT /F1 28 Tf 20 100 Td <65E5672C8A9E> Tj ET\n'
  return buildPdf([
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] ' +
      '/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    contentStream(content),
    '<< /Type /Font /Subtype /Type0 /BaseFont /KozMinPr6N-Regular ' +
      '/Encoding /UniJIS-UCS2-H /DescendantFonts [6 0 R] >>',
    '<< /Type /Font /Subtype /CIDFontType0 /BaseFont /KozMinPr6N-Regular ' +
      '/CIDSystemInfo << /Registry (Adobe) /Ordering (Japan1) /Supplement 6 >> ' +
      '/FontDescriptor 7 0 R /DW 1000 >>',
    '<< /Type /FontDescriptor /FontName /KozMinPr6N-Regular /Flags 4 ' +
      '/FontBBox [-437 -340 1147 1317] /ItalicAngle 0 /Ascent 1317 /Descent -349 ' +
      '/CapHeight 742 /StemV 80 >>',
  ])
}

/** A document with a real AcroForm text field (NB-17 / test 75). The field is a
 *  genuine `/FT /Tx` widget annotation with a `/Rect`, so a viewer configured
 *  for form entry WOULD mount an editable HTML widget over it. */
function formPdf(): Buffer {
  const content = 'BT /Helv 12 Tf 20 160 Td (Name:) Tj ET\n0 0 0 RG\n20 100 260 30 re S\n'
  return buildPdf([
    '<< /Type /Catalog /Pages 2 0 R /AcroForm << /Fields [5 0 R] /DA (/Helv 0 Tf 0 g) ' +
      '/DR << /Font << /Helv 6 0 R >> >> >> >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] /Annots [5 0 R] ' +
      '/Resources << /Font << /Helv 6 0 R >> >> /Contents 4 0 R >>',
    contentStream(content),
    '<< /Type /Annot /Subtype /Widget /FT /Tx /T (name) /V () /Rect [20 100 280 130] ' +
      '/F 4 /DA (/Helv 12 Tf 0 g) /P 3 0 R >>',
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ])
}

// ─────────────────────────────────────────────────────────────────────────────
// Pure predicates — each with a positive control below, because a checker
// nobody has seen fire is a grep wearing a gate's uniform (test 121's note).
// ─────────────────────────────────────────────────────────────────────────────

/**
 * PDF.js's own main-thread-fallback warnings. These are the ONLY symptom of the
 * failure FR-019c forbids: `PDFWorker` catches every worker-setup error and
 * calls `_setupFakeWorker()`, which parses on the main thread and warns.
 *
 * The strings are PDF.js's, not ours — matched loosely enough that a wording
 * change still trips it, and narrowly enough that unrelated warnings do not.
 */
function isMainThreadFallbackWarning(text: string): boolean {
  return /fake\s+worker/i.test(text) || /falling back to main[- ]thread/i.test(text)
}

interface EvalHit {
  pattern: string
  count: number
}

/**
 * FR-019a's FIRST obligation, and the one nothing tested: no eval path in the
 * shipped bundle. Scanned against the ARTEFACT rather than a call-site option,
 * because the option FR-019a used to name (`isEvalSupported`) no longer exists
 * in 6.2.108 — asserting it would pass forever while proving nothing.
 */
function scanForEvalPaths(source: string): EvalHit[] {
  const patterns: Array<[string, RegExp]> = [
    ['eval(', /(?<![.\w$])eval\s*\(/g],
    ['.eval(', /\.\s*eval\s*\(/g],
    ['new Function(', /new\s+Function\s*\(/g],
    ['Function("…")', /\bFunction\s*\(\s*["'`]/g],
    ['setTimeout("…")', /setTimeout\s*\(\s*["'`]/g],
  ]
  const hits: EvalHit[] = []
  for (const [name, re] of patterns) {
    const m = source.match(re)
    if (m && m.length > 0) hits.push({ pattern: name, count: m.length })
  }
  return hits
}

// ─────────────────────────────────────────────────────────────────────────────
// Harness helpers
// ─────────────────────────────────────────────────────────────────────────────

interface WorkspaceFixture {
  id: string
  workDir: string
}

/** Resolve the workspace whose Library these tests drive, and seed its work/
 *  tree with the four fixtures. Idempotent: every test calls it. */
async function seedWorkspace(page: Page): Promise<WorkspaceFixture> {
  const res = await page.request.get('/api/v1/workspaces')
  expect(res.status(), 'GET /api/v1/workspaces must succeed').toBe(200)
  const list = (await res.json()) as Array<{ id: string; is_default?: boolean }>
  expect(list.length, 'the gateway must have at least one workspace to host the Library').toBeGreaterThan(0)
  const ws = list.find((w) => w.is_default) ?? list[0]

  const workDir = path.join(OMNIPUS_HOME, 'workspaces', ws.id, 'work')
  fs.mkdirSync(workDir, { recursive: true })
  fs.writeFileSync(
    path.join(workDir, F_NOTE),
    `# Preview PDF phase-1 note\n\n${NOTE_MARKER}\n`,
  )
  fs.writeFileSync(path.join(workDir, F_LATIN), latinPdf())
  fs.writeFileSync(path.join(workDir, F_CJK), cjkPdf())
  fs.writeFileSync(path.join(workDir, F_FORM), formPdf())

  return { id: ws.id, workDir }
}

/**
 * Install the Worker observer BEFORE any page script runs.
 *
 * It records, per constructed Worker: the URL, the module type, the distinct
 * message ACTIONS posted to it, and how many messages came back. A main-thread
 * fallback constructs no Worker at all (PDF.js uses a `LoopbackPort`), so an
 * empty record set is itself the failure signal.
 *
 * This is the WEAKEST of the three thread oracles on purpose — in-page code
 * could lie about it. It is cross-checked against `page.workers()` and against
 * an evaluation inside the worker's own realm, neither of which page script can
 * fabricate.
 */
async function installWorkerObserver(page: Page): Promise<void> {
  await page.addInitScript(() => {
    const NativeWorker = window.Worker
    interface Rec {
      url: string
      type: string
      posted: string[]
      receivedActions: string[]
      received: number
    }
    const records: Rec[] = []
    ;(window as unknown as { __omnipusWorkerObservations: Rec[] }).__omnipusWorkerObservations =
      records

    class ObservedWorker extends NativeWorker {
      private __rec: Rec
      constructor(url: string | URL, options?: WorkerOptions) {
        super(url, options)
        const rec: Rec = {
          url: String(url),
          type: options?.type ?? 'classic',
          posted: [],
          receivedActions: [],
          received: 0,
        }
        records.push(rec)
        this.__rec = rec
        this.addEventListener('message', (event: MessageEvent) => {
          rec.received++
          const action = (event?.data as { action?: unknown } | undefined)?.action
          if (typeof action === 'string' && !rec.receivedActions.includes(action)) {
            rec.receivedActions.push(action)
          }
        })
      }
      postMessage(message: unknown, ...rest: unknown[]): void {
        const action = (message as { action?: unknown } | undefined)?.action
        if (typeof action === 'string' && !this.__rec.posted.includes(action)) {
          this.__rec.posted.push(action)
        }
        // `any` here is deliberate: postMessage has two incompatible
        // overloads and narrowing to either one would change what a caller may
        // pass through this wrapper.
        return (super.postMessage as (...a: unknown[]) => void)(message, ...rest)
      }
    }
    window.Worker = ObservedWorker as unknown as typeof Worker
  })
}

interface WorkerObservation {
  url: string
  type: string
  posted: string[]
  receivedActions: string[]
  received: number
}

async function readWorkerObservations(page: Page): Promise<WorkerObservation[]> {
  return page.evaluate(
    () =>
      (window as unknown as { __omnipusWorkerObservations?: WorkerObservation[] })
        .__omnipusWorkerObservations ?? [],
  )
}

/** Open the Library pop-out at a workspace and wait for its listing. */
async function openLibrary(page: Page, workspaceId: string): Promise<void> {
  await page.goto(`/#/library?workspace=${workspaceId}`)
  await expect(page.getByTestId('library-explorer')).toBeVisible({ timeout: 30_000 })
  await expect(page.getByTestId(`library-row-${F_NOTE}`)).toBeVisible({ timeout: 30_000 })
}

/** Select one file in the listing — the same click an operator makes. */
async function selectEntry(page: Page, entryPath: string): Promise<void> {
  await page.getByTestId(`library-row-${entryPath}`).click()
  await expect(page.getByTestId('library-preview-title')).toHaveText(entryPath, {
    timeout: 20_000,
  })
}

/**
 * Wait for the SPA's own PDF surface to finish DRAWING a page.
 *
 * Not "the page element exists": the component appends the page container and
 * its canvas BEFORE PDF.js paints into them, so a test that proceeds on the
 * element's presence samples a blank canvas and fails intermittently. Observed
 * exactly that while writing this file — a pixel count of 0 on a document that
 * renders perfectly a beat later. The oracle is therefore drawn PIXELS, held
 * stable across two samples, which is also the point at which a canvas
 * snapshot can be compared against a later one (test 75).
 *
 * If the viewer errored, ITS message is surfaced rather than a bare selector
 * timeout — a blank pane with no named cause is the failure this unit exists
 * to end.
 */
async function waitForPdfSurface(page: Page): Promise<void> {
  await expect(page.getByTestId('library-pdf-preview')).toBeVisible({ timeout: 30_000 })
  const errorPane = page.getByTestId('library-pdf-error')
  await expect
    .poll(
      async () => {
        if ((await page.getByTestId('library-pdf-page').count()) > 0) return 'rendered'
        if ((await errorPane.count()) > 0) return `error: ${await errorPane.innerText()}`
        return 'pending'
      },
      { timeout: 30_000, intervals: [250], message: 'the PDF must produce a page surface' },
    )
    .toBe('rendered')
}

/**
 * The stronger wait: pixels are actually on the canvas and have stopped
 * changing.
 *
 * Used for the Latin fixture only, which draws a filled rectangle and so paints
 * regardless of which fonts the host has. It is deliberately NOT used for the
 * CJK fixture: that document embeds no font, so whether real glyphs, .notdef
 * boxes or nothing at all get painted depends on the host's installed fonts,
 * which differ between a developer's macOS and CI's Linux. Making a pixel count
 * the gate there would be a host-dependent flake wearing a security test's
 * uniform.
 */
async function waitForPdfPainted(page: Page): Promise<void> {
  await waitForPdfSurface(page)
  let previousPixels = -1
  await expect
    .poll(
      async () => {
        const pixels = await firstPageNonWhitePixels(page)
        if (pixels <= 0) return 'canvas still blank'
        const settled = pixels === previousPixels
        previousPixels = pixels
        return settled ? 'painted' : 'still drawing'
      },
      { timeout: 30_000, intervals: [250], message: 'the PDF must draw pixels' },
    )
    .toBe('painted')
}

/** Non-white pixel count of the first rendered page's canvas. The oracle for
 *  "something was actually drawn" — `toBeVisible()` on a canvas is true of a
 *  blank one. */
async function firstPageNonWhitePixels(page: Page): Promise<number> {
  return page.evaluate(() => {
    const canvas = document.querySelector<HTMLCanvasElement>(
      '[data-testid="library-pdf-page"] canvas',
    )
    if (!canvas) return -1
    const ctx = canvas.getContext('2d')
    if (!ctx) return -1
    const { data } = ctx.getImageData(0, 0, canvas.width, canvas.height)
    let nonWhite = 0
    for (let i = 0; i < data.length; i += 4) {
      if (data[i] < 200 || data[i + 1] < 200 || data[i + 2] < 200) nonWhite++
    }
    return nonWhite
  })
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

test.describe('ADR-067 D3 — PDF rendering, laziness and the worker', () => {
  /**
   * Spec test 61 — AC-15.6, FR-018.
   *
   * Two ORDERED phases in ONE session. Phase 1's zero-requests claim is only
   * meaningful if the app is alive, so phase 1 also proves a preview rendered;
   * phase 2 then proves the chunk arrives when — and only when — a PDF is
   * opened. Catches converting the lazy import to a static one, and catches the
   * measured rolldown trap where the preload helper got folded into the pdfjs
   * chunk and the ENTRY then imported all 428 kB on first paint while every
   * name-based check still passed.
   */
  test('61 — PDF.js loads only when a PDF is opened, and arrives in a named chunk', async ({
    page,
  }) => {
    const ws = await seedWorkspace(page)

    const requested: string[] = []
    page.on('request', (r: Request) => requested.push(r.url()))

    await openLibrary(page, ws.id)

    // ── Phase 1: a markdown file. Nothing PDF.js may load. ──────────────────
    await selectEntry(page, F_NOTE)
    await expect(page.getByTestId('library-preview-pane')).toContainText(NOTE_MARKER, {
      timeout: 20_000,
    })

    const phase1 = requested.filter((u) => PDFJS_ANY_RE.test(u))
    expect(
      phase1,
      'no PDF.js chunk, worker or runtime asset may be requested before a PDF is opened',
    ).toEqual([])

    // The initial payload itself must not reference it either — a
    // `<link rel=modulepreload>` in index.html would load the parser on first
    // paint without any of the above firing at the moment we sampled.
    const indexHtml = await (await page.request.get('/')).text()
    expect(indexHtml.toLowerCase(), 'index.html must not preload or reference PDF.js').not.toContain(
      'pdfjs',
    )
    const entryMatch = indexHtml.match(/<script[^>]+src="(\/assets\/index-[^"]+\.js)"/)
    expect(entryMatch, 'index.html must carry an entry module script').not.toBeNull()
    const entrySource = await (await page.request.get(entryMatch![1])).text()
    expect(entrySource.length, 'the entry chunk must actually have been fetched').toBeGreaterThan(0)
    expect(
      entrySource,
      'the entry chunk must not statically import the PDF.js chunk (the measured rolldown preload-helper trap)',
    ).not.toMatch(/pdfjs-[A-Za-z0-9_-]+\.js/)

    // ── Phase 2: the same session opens a PDF. The chunk must arrive. ───────
    await selectEntry(page, F_LATIN)
    await waitForPdfPainted(page)

    const chunkUrls = [...new Set(requested.filter((u) => PDFJS_CHUNK_RE.test(u)))]
    expect(
      chunkUrls.length,
      'opening a PDF must fetch exactly one NAMED PDF.js chunk; zero is a failure, not a pass',
    ).toBe(1)
    expect(path.basename(new URL(chunkUrls[0]).pathname)).toMatch(/^pdfjs-[A-Za-z0-9_-]+\.js$/)

    // …and that chunk must really be PDF.js. A file merely NAMED `pdfjs-*`
    // would satisfy every check above.
    const chunkSource = await (await page.request.get(chunkUrls[0])).text()
    expect(chunkSource, 'the named chunk must contain PDF.js').toContain('GetDocRequest')
    expect(chunkSource).toContain('AbortException')

    expect(await firstPageNonWhitePixels(page), 'the PDF must have drawn pixels').toBeGreaterThan(0)
  })

  /**
   * FR-018 — the PDF renders in the SPA's own viewer: not downloaded, not
   * navigated to, not handed to an <embed>/<object>/<iframe>.
   *
   * See the file header for what headless can and cannot measure here.
   */
  test('FR-018 — a PDF renders in the SPA viewer, is not downloaded and does not navigate', async ({
    page,
  }) => {
    const ws = await seedWorkspace(page)

    const downloads: string[] = []
    page.on('download', (d) => downloads.push(d.suggestedFilename()))
    const popups: string[] = []
    page.on('popup', (p) => popups.push(p.url()))
    const docNavigations: string[] = []
    page.on('request', (r) => {
      if (r.resourceType() === 'document' && /\.pdf(\?|$)|library\/.*\/download/.test(r.url())) {
        docNavigations.push(r.url())
      }
    })

    await openLibrary(page, ws.id)
    const urlBefore = page.url()
    await selectEntry(page, F_LATIN)
    await waitForPdfPainted(page)

    // Our own surface drew it.
    await expect(page.getByTestId('library-pdf-preview')).toBeVisible()
    expect(await page.locator('[data-testid="library-pdf-page"] canvas').count()).toBeGreaterThan(0)
    expect(await firstPageNonWhitePixels(page)).toBeGreaterThan(0)
    // The text layer proves the DOCUMENT was parsed, not that some image loaded.
    await expect(page.locator('.omnipus-pdf-text-layer').first()).toContainText('OMNIPUS PDF')

    expect(downloads, 'a previewed PDF must never download').toEqual([])
    expect(popups, 'a previewed PDF must never open a tab').toEqual([])
    expect(docNavigations, 'the PDF must never become a browser DOCUMENT').toEqual([])

    // The DOCUMENT must still be the SPA. Note what is deliberately NOT
    // asserted: URL equality. Selecting a file pushes `&path=…` into the hash
    // — that is FR-012's deep-linking, an in-app router navigation, and an
    // equality check here fails on it while proving nothing about the PDF. The
    // property that matters is that the browser's document is still the app's
    // own page, which is the pathname plus the route.
    const before = new URL(urlBefore)
    const after = new URL(page.url())
    expect(after.origin, 'the document must still be the gateway origin').toBe(before.origin)
    expect(after.pathname, 'the document must still be the SPA shell, not the PDF').toBe(
      before.pathname,
    )
    expect(after.hash.startsWith('#/library'), `still on the Library route (${after.hash})`).toBe(
      true,
    )

    // No browser-viewer-shaped surface anywhere in the page.
    const embeddedSources = await page
      .locator('embed, object, iframe')
      .evaluateAll((els) =>
        els
          .map(
            (el) =>
              (el as HTMLIFrameElement).src ||
              (el as HTMLEmbedElement).src ||
              (el as HTMLObjectElement).data ||
              '',
          )
          .filter(Boolean),
      )
    expect(
      embeddedSources.filter((u) => /\.pdf(\?|$)|library\/.*\/download/.test(u)),
      'no <embed>/<object>/<iframe> may point at the PDF',
    ).toEqual([])
  })

  /**
   * Spec test 96 — FR-019c. THE ONE MOST LIKELY TO BE SILENTLY WRONG.
   *
   * PDF.js does not fail when its worker cannot load; it falls back to
   * main-thread parsing with a console warning as the only symptom. So this
   * asserts the THREAD, three independent ways, not the configuration — and
   * runs with the SPA's real Content-Security-Policy applied, which is the
   * point: FR-019b and FR-019c can silently defeat each other, because a
   * `worker-src` that does not cover the built worker URL produces exactly this
   * fallback.
   */
  test('96 — PDF parsing happens on a real worker thread, never the main thread', async ({
    page,
  }) => {
    const ws = await seedWorkspace(page)
    await installWorkerObserver(page)

    const consoleText: string[] = []
    page.on('console', (m) => consoleText.push(m.text()))

    await openLibrary(page, ws.id)
    await selectEntry(page, F_LATIN)
    await waitForPdfPainted(page)

    // Oracle 1 — Playwright's own view of real dedicated workers. This comes
    // from the browser, not from page script, so nothing in the page can
    // fabricate it.
    const pwWorkerUrls = page.workers().map((w) => w.url())
    const pdfWorkers = pwWorkerUrls.filter((u) => u.endsWith(WORKER_URL_SUFFIX))
    expect(
      pdfWorkers.length,
      `a real dedicated worker at ${WORKER_URL_SUFFIX} must exist; seen: ${JSON.stringify(pwWorkerUrls)}`,
    ).toBe(1)

    // Oracle 2 — evaluate INSIDE the worker's own realm. Only a real, separate
    // thread can answer this at all.
    const pdfWorker = page.workers().find((w) => w.url().endsWith(WORKER_URL_SUFFIX))!
    const insideWorker = await pdfWorker.evaluate(() => ({
      href: self.location.href,
      isWindow: typeof (globalThis as { window?: unknown }).window !== 'undefined',
      hasDocument: typeof (globalThis as { document?: unknown }).document !== 'undefined',
    }))
    expect(insideWorker.href).toContain(WORKER_URL_SUFFIX)
    expect(insideWorker.isWindow, 'a worker realm has no window').toBe(false)
    expect(insideWorker.hasDocument, 'a worker realm has no document').toBe(false)

    // Oracle 3 — the parse itself crossed the thread boundary. The fake-worker
    // fallback uses a LoopbackPort, which is not a Worker and records nothing.
    const observations = await readWorkerObservations(page)
    const rec = observations.find((o) => o.url.endsWith(WORKER_URL_SUFFIX))
    expect(rec, `the PDF.js worker must have been constructed; saw ${JSON.stringify(observations.map((o) => o.url))}`).toBeDefined()
    expect(rec!.type, 'PDF.js ships an ES module worker').toBe('module')
    expect(
      rec!.posted,
      'the document-parse request must have been sent TO the worker thread',
    ).toContain('GetDocRequest')
    expect(
      rec!.received,
      'the worker must have answered — a constructed-but-dead worker parses nothing',
    ).toBeGreaterThan(0)

    // Oracle 4 — the only symptom the fallback ever produces.
    const fallbackWarnings = consoleText.filter(isMainThreadFallbackWarning)
    expect(
      fallbackWarnings,
      'PDF.js must not have set up a fake (main-thread) worker',
    ).toEqual([])
  })

  /**
   * Positive control for test 96's fourth oracle. Without this, "zero
   * fake-worker warnings" is indistinguishable from a predicate that can never
   * match — the exact false-green shape §13 keeps naming.
   */
  test('96 control — the main-thread-fallback predicate can actually fire', () => {
    // PDF.js's own strings, verbatim.
    expect(isMainThreadFallbackWarning('Setting up fake worker.')).toBe(true)
    expect(isMainThreadFallbackWarning('Setting up fake worker failed: "…".')).toBe(true)
    expect(isMainThreadFallbackWarning('falling back to main-thread parsing')).toBe(true)
    // And it must not fire on unrelated noise, or "zero warnings" would be
    // unachievable for reasons that have nothing to do with the thread.
    expect(
      isMainThreadFallbackWarning(
        'Warning: Cannot load system font: KozMinPr6N-Regular, installing it could help to improve PDF rendering.',
      ),
    ).toBe(false)
    expect(isMainThreadFallbackWarning('[vite] connected.')).toBe(false)
  })

  /**
   * FR-018a — the four runtime asset directories ship and are actually used.
   *
   * The CJK document is the oracle. Its font has an external `/UniJIS-UCS2-H`
   * encoding and no `/ToUnicode`, so the three characters can only reach the
   * text layer by way of character maps fetched over HTTP. Asserting "no error"
   * would pass on a blank page; asserting the CHARACTERS cannot.
   *
   * Pixel counts are deliberately NOT asserted for this document: with no
   * embedded CJK font, whether glyphs or .notdef boxes get drawn depends on the
   * host's fonts, which differ between a developer's macOS and CI's Linux. The
   * text layer and the cmap fetches are host-independent.
   */
  test('FR-018a — the runtime asset directories are served, and a CJK PDF renders through cmaps', async ({
    page,
  }) => {
    const ws = await seedWorkspace(page)

    // The manifest enumerates what the build emitted (FR-018c). Every directory
    // must be present AND non-empty — an empty one is the silent failure.
    const manifestRes = await page.request.get(`${PDFJS_PREFIX}asset-manifest.json`)
    expect(manifestRes.status()).toBe(200)
    const manifest = (await manifestRes.json()) as Record<string, string[]>
    for (const { dir, silentFailure } of RUNTIME_ASSET_DIRS) {
      expect(
        manifest[dir]?.length ?? 0,
        `PDF.js runtime directory "${dir}" must ship — without it, ${silentFailure}`,
      ).toBeGreaterThan(0)
      // And a real file from it must actually be fetchable, not merely listed.
      const probe = await page.request.get(`${PDFJS_PREFIX}${dir}/${manifest[dir][0]}`)
      expect(probe.status(), `${dir}/${manifest[dir][0]} must be served`).toBe(200)
      expect(
        (probe.headers()['content-type'] ?? '').toLowerCase(),
        `${dir} must not be answered with the app's HTML shell`,
      ).not.toContain('text/html')
    }

    const cmapRequests: string[] = []
    page.on('request', (r) => {
      if (r.url().includes(`${PDFJS_PREFIX}cmaps/`)) cmapRequests.push(r.url())
    })

    await openLibrary(page, ws.id)
    await selectEntry(page, F_CJK)
    await waitForPdfSurface(page)

    await expect(
      page.locator('.omnipus-pdf-text-layer').first(),
      'the Japanese characters must reach the text layer, which requires the character maps',
    ).toContainText(CJK_TEXT)

    expect(
      cmapRequests.some((u) => /UniJIS-UCS2-H\.bcmap$/.test(u)),
      `the document's external encoding must have been fetched from cmaps/; saw ${JSON.stringify(cmapRequests)}`,
    ).toBe(true)
  })

  /**
   * FR-018b, half one — a missing PDF.js asset must be a REAL 404.
   *
   * The sharp edge FR-018b names: `newSPAHandler` answers any unmatched path
   * with index.html and HTTP 200, which is correct for /library and every other
   * client-side route and catastrophic for an asset request — PDF.js checks the
   * status, so a 200 carrying HTML reads as success and the document renders
   * blank with nothing naming the cause. This asserts against the real gateway,
   * with no interception.
   */
  test('FR-018b — a missing asset under the PDF.js prefix 404s instead of returning index.html', async ({
    page,
  }) => {
    for (const { dir } of RUNTIME_ASSET_DIRS) {
      const missing = `${PDFJS_PREFIX}${dir}/omnipus-e2e-definitely-missing.bin`
      const res = await page.request.get(missing)
      expect(res.status(), `${missing} must 404`).toBe(404)
      const body = await res.text()
      expect(
        body.toLowerCase(),
        `${missing} must not be answered with the SPA shell`,
      ).not.toContain('<!doctype html')
    }

    // The control that stops the above passing for the wrong reason: an
    // unmatched path OUTSIDE the PDF.js prefix must still get the SPA fallback,
    // or the assertion would also pass on a gateway that 404s everything.
    const spaRoute = await page.request.get('/library')
    expect(spaRoute.status(), 'a client-side route must still resolve to the SPA').toBe(200)
    expect((await spaRoute.text()).toLowerCase()).toContain('<!doctype html')
  })

  /**
   * FR-018b, half two — the viewer surfaces the failure VISIBLY.
   *
   * The gateway's 404 branch is asserted above; this asserts what the viewer
   * does when an asset nevertheless arrives as the app's HTML shell with HTTP
   * 200 — the precise shape of the un-guarded fallback. The pane must name the
   * directory. "It rendered blank" must not be reachable.
   */
  test('FR-018b — an asset served as the HTML shell fails visibly and names the directory', async ({
    page,
  }) => {
    const ws = await seedWorkspace(page)

    await page.route(`**${PDFJS_PREFIX}cmaps/**`, async (route) => {
      await route.fulfill({
        status: 200,
        contentType: 'text/html; charset=utf-8',
        body: '<!doctype html><html><body>app shell</body></html>',
      })
    })

    await openLibrary(page, ws.id)
    await selectEntry(page, F_CJK)

    const errorPane = page.getByTestId('library-pdf-error')
    await expect(errorPane, 'a missing runtime asset must produce a visible error').toBeVisible({
      timeout: 30_000,
    })
    await expect(errorPane).toContainText('cmaps')
    // …and it must explain the consequence, not merely echo a URL.
    await expect(errorPane).toContainText(/character maps/i)
    // Nothing may have rendered — a half-rendered blank page next to an error
    // is the ambiguity this requirement exists to remove.
    expect(await page.getByTestId('library-pdf-page').count()).toBe(0)
  })

  /**
   * FR-019a — asserted against the SHIPPED ARTEFACT, as served.
   *
   * Not against `getDocument` options: `isEvalSupported` does not exist in
   * 6.2.108 and `enableScripting` is not one of its parameters, so asserting
   * either would pass forever while proving nothing. What IS assertable is that
   * the bytes the browser received contain no eval path, and that the scripting
   * interpreter is not shipped at all.
   */
  test('FR-019a — no eval path in the served PDF.js chunk or worker, and pdf.sandbox*.mjs is not shipped', async ({
    page,
  }) => {
    const ws = await seedWorkspace(page)

    const requested: string[] = []
    page.on('request', (r) => requested.push(r.url()))

    await openLibrary(page, ws.id)
    await selectEntry(page, F_LATIN)
    await waitForPdfPainted(page)

    // Scan the exact URLs this session loaded — not a path on disk, which could
    // be a stale build the running gateway never served.
    const chunkUrl = requested.find((u) => PDFJS_CHUNK_RE.test(u))
    expect(chunkUrl, 'the PDF.js chunk must have been requested').toBeDefined()
    const toScan = [chunkUrl!, WORKER_URL_SUFFIX]

    let scanned = 0
    for (const url of toScan) {
      const res = await page.request.get(url)
      expect(res.status(), `${url} must be served`).toBe(200)
      const source = await res.text()
      expect(source.length, `${url} must not be empty`).toBeGreaterThan(1000)
      scanned++
      expect(
        scanForEvalPaths(source),
        `${url} must contain no eval path (FR-019a)`,
      ).toEqual([])
    }
    // Zero files scanned is a FAILURE, never a pass — a glob that stops
    // matching is how this class of gate dies quietly.
    expect(scanned, 'both the chunk and the worker must have been scanned').toBe(toScan.length)

    // The scripting interpreter is absent, not disabled. Absence beats a flag
    // someone can flip back.
    for (const candidate of ['pdf.sandbox.mjs', 'pdf.sandbox.min.mjs']) {
      const res = await page.request.get(`${PDFJS_PREFIX}${candidate}`)
      expect(res.status(), `${candidate} must not be shipped`).toBe(404)
    }
    const manifest = (await (
      await page.request.get(`${PDFJS_PREFIX}asset-manifest.json`)
    ).json()) as Record<string, string[]>
    const sandboxEntries = Object.entries(manifest).flatMap(([dir, files]) =>
      files.filter((f) => /sandbox/i.test(f)).map((f) => `${dir}/${f}`),
    )
    expect(sandboxEntries, 'no sandbox artefact may appear in the shipped asset set').toEqual([])
  })

  /**
   * Positive control for the eval scanner. Test 121's note is explicit: without
   * this, a scanner whose patterns stopped matching would report zero hits and
   * pass forever.
   */
  test('FR-019a control — the eval scanner reports the patterns it claims to find', () => {
    const hostile = [
      'var a = eval("1+1");',
      'const f = new Function("return 1");',
      'obj.eval("x");',
      'Function("return this")();',
      'setTimeout("doThing()", 10);',
    ].join('\n')
    const hits = scanForEvalPaths(hostile).map((h) => h.pattern).sort()
    expect(hits).toEqual(
      ['.eval(', 'Function("…")', 'eval(', 'new Function(', 'setTimeout("…")'].sort(),
    )
    // And it must not fire on benign code, or the real assertion could never be
    // satisfied by any bundle.
    expect(
      scanForEvalPaths('const evaluation = compute(); medieval(x); this.evaluate(y);'),
    ).toEqual([])
  })

  /**
   * FR-019b × FR-019a — the runtime policy, enforced rather than merely present.
   *
   * Two halves. The header, because that is the requirement's wording and it is
   * what carries `no 'unsafe-eval'`. And enforcement, because a policy that is
   * sent but not applied is the failure that matters.
   *
   * ── A false green found while writing this, recorded so it is not re-added ──
   * The obvious enforcement probe is `page.evaluate(() => eval('1+1'))`,
   * expecting an `EvalError`. MEASURED: it returns `2`. Playwright evaluates
   * through the Chrome DevTools Protocol, and CDP evaluation is EXEMPT from the
   * page's CSP — so that probe reports "eval works" on a correctly-locked-down
   * page, and the mirror-image version of it (asserting eval succeeds) would
   * have passed forever while proving nothing about the policy. `eval`
   * enforcement is therefore not measurable from a Playwright evaluate at all.
   *
   * What IS measurable is whether the browser enforces this policy at all, via
   * a violation the PAGE's own DOM triggers: an inline <script> element the
   * page inserts is compiled by the page, not by CDP, and `script-src 'self'`
   * (no `'unsafe-inline'`) must block it and fire `securitypolicyviolation`.
   * That plus the header's `no 'unsafe-eval'` is the honest form of the claim.
   */
  test('FR-019b — the SPA is served with an enforced CSP that has no unsafe-eval', async ({
    page,
  }) => {
    const res = await page.request.get('/')
    const csp = res.headers()['content-security-policy']
    expect(csp, 'the SPA must be served with a Content-Security-Policy').toBeTruthy()
    expect(csp, "no 'unsafe-eval' — non-negotiable (FR-019a, §10.7)").not.toContain('unsafe-eval')
    // worker-src must permit the worker, or FR-019b silently defeats FR-019c
    // by pushing PDF.js onto the main thread with only a warning.
    expect(csp).toMatch(/worker-src[^;]*'self'/)
    // script-src must not be widened to inline, or the enforcement probe below
    // would be measuring a policy that permits what it claims to forbid.
    const scriptSrc = (csp ?? '').split(';').map((d) => d.trim()).find((d) => d.startsWith('script-src'))
    expect(scriptSrc, 'the policy must declare script-src').toBeDefined()
    expect(scriptSrc).toContain("'self'")
    expect(scriptSrc, 'script-src must not permit inline scripts').not.toContain('unsafe-inline')

    await page.goto('/#/library')
    const verdict = await page.evaluate(
      () =>
        new Promise<{ blocked: boolean; directive?: string; ran?: string }>((resolve) => {
          document.addEventListener(
            'securitypolicyviolation',
            (e) => resolve({ blocked: true, directive: e.violatedDirective }),
            { once: true },
          )
          const el = document.createElement('script')
          el.textContent =
            'window.__omnipusCspProbe = "an inline script executed despite the policy"'
          document.head.appendChild(el)
          setTimeout(
            () =>
              resolve({
                blocked: false,
                ran: (window as unknown as { __omnipusCspProbe?: string }).__omnipusCspProbe,
              }),
            2_000,
          )
        }),
    )
    expect(
      verdict.blocked,
      `the browser must be ENFORCING the policy, not merely reporting it (${JSON.stringify(verdict)})`,
    ).toBe(true)
    expect(verdict.directive).toContain('script-src')
  })

  /**
   * Spec test 75 — NB-17. Read-only means read-only.
   *
   * Three halves, because each alone is passable by a broken implementation:
   * nothing appears in the UI, the file is BYTE-IDENTICAL on disk, and no write
   * request reached the gateway. A UI-only fix passes the first and fails the
   * other two.
   */
  test('75 — form fields are inert: nothing appears, nothing is written, nothing is sent', async ({
    page,
  }) => {
    const ws = await seedWorkspace(page)
    const formPath = path.join(ws.workDir, F_FORM)
    const hashBefore = crypto.createHash('sha256').update(fs.readFileSync(formPath)).digest('hex')

    const writeRequests: string[] = []
    page.on('request', (r) => {
      if (r.method() !== 'GET' && r.method() !== 'HEAD' && r.url().includes('/api/v1/library')) {
        writeRequests.push(`${r.method()} ${r.url()}`)
      }
    })

    await openLibrary(page, ws.id)
    await selectEntry(page, F_FORM)
    await waitForPdfPainted(page)

    // No interactive widget was ever mounted. `annotationMode: ENABLE` draws
    // appearance streams as static graphics; ENABLE_FORMS / ENABLE_STORAGE are
    // the modes that turn fields into live HTML controls.
    expect(
      await page
        .locator(
          '[data-testid="library-pdf-preview"] input, [data-testid="library-pdf-preview"] textarea, [data-testid="library-pdf-preview"] select, [data-testid="library-pdf-preview"] [contenteditable="true"]',
        )
        .count(),
      'a read-only viewer mounts no form controls',
    ).toBe(0)

    // Type where the field is. Nothing may change on screen.
    const canvasBefore = await page.evaluate(
      () =>
        document
          .querySelector<HTMLCanvasElement>('[data-testid="library-pdf-page"] canvas')!
          .toDataURL(),
    )
    await page.locator('[data-testid="library-pdf-page"]').first().click({ position: { x: 120, y: 90 } })
    await page.keyboard.type('OMNIPUS-INJECTED-VALUE')
    await page.waitForTimeout(500)
    const canvasAfter = await page.evaluate(
      () =>
        document
          .querySelector<HTMLCanvasElement>('[data-testid="library-pdf-page"] canvas')!
          .toDataURL(),
    )
    expect(canvasAfter, 'typing must not change a single rendered pixel').toBe(canvasBefore)
    await expect(page.getByTestId('library-pdf-preview')).not.toContainText('OMNIPUS-INJECTED-VALUE')

    // The server-side and disk halves.
    expect(writeRequests, 'no write may reach the Library API').toEqual([])
    const hashAfter = crypto.createHash('sha256').update(fs.readFileSync(formPath)).digest('hex')
    expect(hashAfter, 'the file on disk must be byte-identical').toBe(hashBefore)
  })
})
