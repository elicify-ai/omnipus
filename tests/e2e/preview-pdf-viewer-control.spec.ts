/**
 * preview-pdf-viewer-control.spec.ts — ADR-067 §13.1 test 57 / §13.4 item 4:
 * the BROWSER-VIEWER NEGATIVE CONTROL (FR-014, FR-018, D15.1, D15.3).
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * THE CLAIM THIS FILE EXISTS TO MEASURE
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * D15 revision 3 removed the browser's own PDF viewer from the Library path.
 * FR-018: PDFs are rendered by PDF.js *inside the SPA*, drawing into a
 * `<canvas>`; FR-014: only content the BROWSER executes is sandboxed, and
 * formats Omnipus renders itself "never become browser documents". §10.4 backs
 * that with the disposition — `.pdf` is deliberately absent from the inline
 * allow-list, so no route ever hands PDF bytes to the browser as a document.
 *
 * Every one of those sentences is a claim about a NEGATIVE: *the browser's own
 * PDF viewer is not what rendered this.* And a negative about a browser
 * capability is exactly the assertion that passes for free when the capability
 * is absent.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHY HEADED, AND WHY THAT IS THE WHOLE POINT
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * **Headless Chromium has no PDF viewer at all.** So "no browser PDF viewer
 * appeared" is AUTOMATICALLY TRUE headless — for every build, including one
 * where the SPA had been refactored to hand PDFs straight to the browser. The
 * assertion would report identically whether the product held the property or
 * had abandoned it. preview-pdf.spec.ts says so in its own header and refuses to
 * make the claim: *"Do not 'strengthen' this file by asserting the browser
 * viewer is absent — headless it is absent regardless, which is a pass for the
 * wrong reason."* This file is where that claim is allowed to be made, because
 * here the oracle is proved capable first.
 *
 * §0's derivation earns headed mode for exactly two cases and this is the
 * second: *"Headed is required only where the browser's own PDF handling is
 * what is being measured — the top-level `.pdf` type-confusion case and the
 * browser-viewer negative control."*
 *
 * §0 also records the correction that produced this file. An earlier reading —
 * "PDF fails under sandbox everywhere" — was partly a headless artefact:
 * measured HEADED, a TOP-LEVEL pdf renders even sandboxed, while a FRAMED one
 * is blocked. The Library case is framed, so the conclusion held but the
 * reasoning did not, and a conclusion propped up by the wrong reasoning is one
 * refactor away from being false.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * THE ORACLE, MEASURED RATHER THAN ASSUMED
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * Measured on this branch, 2026-09-10, headed Chromium, top-level
 * `application/pdf`:
 *
 *   `document.querySelector('embed')`  → NOTHING on the top-level document.
 *                                        The `<embed>` lives in a nested
 *                                        same-URL frame.
 *   `page.frames()`                    → a `chrome-extension://…/index.html`
 *                                        document appears. THIS is what "the
 *                                        browser's own PDF viewer mounted"
 *                                        looks like from the driver.
 *   viewer `document.body.innerText`   → EMPTY for a rendered PDF and for a
 *                                        rejected one alike (closed shadow
 *                                        root), so the viewer's own output is
 *                                        not readable from here.
 *
 * An `<embed>`-only oracle would therefore have been BLIND — permanently
 * reporting "no browser viewer" whether or not one was there. That is why the
 * probe was run before the assertion was written, and why the presence of a
 * `chrome-extension://` frame is the predicate used below. Page content cannot
 * fabricate such a document, so it is not forgeable from inside the SPA either.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * EVERY NEGATIVE HERE HAS ITS POSITIVE CONTROL IN THE SAME RUN
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * The control is not an extra; it is the reason the negative means anything.
 * In the SAME browser and the SAME run, the same genuine PDF bytes served
 * INLINE from a plain local origin MUST mount the browser's viewer — and served
 * as an ATTACHMENT must download instead. One pair of observations establishes
 * both halves of the mechanism §10.4 relies on:
 *
 *   the oracle can SEE a browser PDF viewer when there is one   (so the
 *     Library's "none" is a measurement, not a blind spot); and
 *   `Content-Disposition: attachment` is what keeps the viewer away   (so the
 *     Library's disposition is doing the work the spec says it does).
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * RETRIES ARE ZERO, TWICE OVER
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * The `preview-headed` project pins `retries: 0`; this file pins it again at
 * file level so running it under any other project still cannot retry it.
 * "The browser's viewer did not render this" is not a property a fourth attempt
 * establishes.
 */
import {
  expect,
  request as playwrightRequest,
  test,
  type APIRequestContext,
  type Page,
} from '@playwright/test';
import { createServer, type IncomingMessage, type Server, type ServerResponse } from 'node:http';
import type { AddressInfo, Socket } from 'node:net';
import fs from 'node:fs';
import path from 'node:path';

// FILE-level retry pin. See the header.
test.describe.configure({ retries: 0 });

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE || './tests/e2e/fixtures/.auth/admin.json';

const TYPE_PDF = 'application/pdf';

/** Fixture names, seeded into the default workspace's work tree. */
const F_PDF = 'preview-viewer-control.pdf';

/**
 * A token drawn INSIDE the PDF's content stream.
 *
 * Finding it in PDF.js's text layer proves the DOCUMENT was parsed — not that
 * some image happened to load, and not that a canvas exists. It is a single
 * word with no spaces so that text-layer chunking cannot split the match.
 */
const PDF_TEXT_MARKER = 'OMNIPUSVIEWERCTL';

// ─────────────────────────────────────────────────────────────────────────────
// Fixture construction
// ─────────────────────────────────────────────────────────────────────────────

/**
 * Assemble numbered objects into a structurally valid PDF with a real xref
 * table.
 *
 * latin1 throughout, so string length equals byte length and the offsets are
 * exact. A PDF with a wrong xref is still readable by PDF.js — it reconstructs
 * — which would let a broken fixture look like a working one.
 */
function buildPdf(objects: string[]): Buffer {
  const header = '%PDF-1.7\n%\xE2\xE3\xCF\xD3\n';
  let body = '';
  const offsets: number[] = [];
  objects.forEach((obj, i) => {
    offsets.push(header.length + body.length);
    body += `${i + 1} 0 obj\n${obj}\nendobj\n`;
  });
  const xrefOffset = header.length + body.length;
  let xref = `xref\n0 ${objects.length + 1}\n0000000000 65535 f \n`;
  for (const off of offsets) xref += `${String(off).padStart(10, '0')} 00000 n \n`;
  return Buffer.from(
    header +
      body +
      xref +
      `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\nstartxref\n${xrefOffset}\n%%EOF\n`,
    'latin1',
  );
}

function contentStream(content: string): string {
  return `<< /Length ${Buffer.byteLength(content, 'latin1')} >>\nstream\n${content}\nendstream`;
}

/**
 * A genuine single-page PDF carrying a FILLED BLACK RECTANGLE and the text
 * marker.
 *
 * The rectangle is what makes the non-white-pixel oracle independent of which
 * fonts the host has installed: a pixel count of zero then means "nothing
 * rendered", never "this glyph was substituted". Base-14 Helvetica is used for
 * the text so no font has to be embedded.
 */
function genuinePdf(): Buffer {
  const content =
    '0 0 0 rg\n20 150 260 30 re f\n' + `BT /F1 24 Tf 20 60 Td (${PDF_TEXT_MARKER}) Tj ET\n`;
  return buildPdf([
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] ' +
      '/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    contentStream(content),
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]);
}

// ─────────────────────────────────────────────────────────────────────────────
// The control origin: the same genuine PDF, served two ways.
// ─────────────────────────────────────────────────────────────────────────────

interface ControlOrigin {
  origin: string;
  close: () => Promise<void>;
}

/**
 * Serve one PDF twice, differing ONLY in `Content-Disposition`.
 *
 * `/inline.pdf` and `/attach.pdf` are the same bytes from the same origin with
 * the same type, so any difference in what the browser does is attributable to
 * the disposition header and to nothing else. That is the mechanism §10.4 leans
 * on for the `.pdf` row, exercised directly rather than inferred.
 *
 * Served locally rather than by editing `pkg/gateway`: this suite does not own
 * that package, and a test that edits the thing it is testing proves nothing
 * about what ships.
 */
async function startControlOrigin(body: Buffer): Promise<ControlOrigin> {
  const server = createServer((req: IncomingMessage, res: ServerResponse) => {
    const name = (req.url || '').split('?')[0].replace(/^\//, '');
    const disposition =
      name === 'attach.pdf' ? 'attachment; filename="attach.pdf"' : name === 'inline.pdf' ? 'inline' : null;
    if (!disposition) {
      res.writeHead(404, { 'Content-Type': 'text/plain', 'Content-Length': '9' });
      res.end('not found');
      return;
    }
    res.writeHead(200, {
      'Content-Type': TYPE_PDF,
      'X-Content-Type-Options': 'nosniff',
      'Content-Disposition': disposition,
      'Content-Length': String(body.length),
    });
    res.end(body);
  });

  const sockets = new Set<Socket>();
  server.on('connection', (s: Socket) => {
    sockets.add(s);
    s.on('close', () => sockets.delete(s));
  });
  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;
  return { origin: `http://127.0.0.1:${port}`, close: () => closeServer(server, sockets) };
}

function closeServer(server: Server, sockets: Set<Socket>): Promise<void> {
  for (const s of sockets) s.destroy();
  return new Promise<void>((resolve) => server.close(() => resolve()));
}

// ─────────────────────────────────────────────────────────────────────────────
// Oracles
// ─────────────────────────────────────────────────────────────────────────────

/**
 * The browser's own PDF viewer, as it appears to the driver.
 *
 * See the file header for the measurement that settled this. The
 * `chrome-extension://` prefix is matched rather than Chrome's stable viewer id
 * (`mhjfbmdgcfjbbpaeojofohoefgiehjai`), so an id change cannot silently turn the
 * oracle blind. No ordinary gateway page has such a frame, and page script
 * cannot create one, so the predicate is not loose in the direction that
 * matters.
 */
function browserPdfViewerFrames(page: Page): string[] {
  return page.frames().map((f) => f.url()).filter((u) => u.startsWith('chrome-extension://'));
}

/**
 * Non-white pixel count of the first rendered page's canvas.
 *
 * The oracle for "something was actually drawn": `toBeVisible()` on a canvas is
 * equally true of a blank one, and a blank canvas is what a preview that
 * silently failed looks like.
 */
async function firstPageNonWhitePixels(page: Page): Promise<number> {
  return page.evaluate(() => {
    const canvas = document.querySelector<HTMLCanvasElement>(
      '[data-testid="library-pdf-page"] canvas',
    );
    if (!canvas) return -1;
    const ctx = canvas.getContext('2d');
    if (!ctx) return -1;
    const { data } = ctx.getImageData(0, 0, canvas.width, canvas.height);
    let nonWhite = 0;
    for (let i = 0; i < data.length; i += 4) {
      if (data[i] < 200 || data[i + 1] < 200 || data[i + 2] < 200) nonWhite++;
    }
    return nonWhite;
  });
}

/**
 * Wait until PDF.js has drawn pixels AND stopped drawing.
 *
 * Not "the element exists": the component appends the page container and its
 * canvas BEFORE PDF.js paints into them, so proceeding on presence samples a
 * blank canvas. If the viewer errored, ITS message is surfaced rather than a
 * bare selector timeout — a blank pane with no named cause is the failure this
 * whole unit exists to end.
 */
async function waitForPdfPainted(page: Page): Promise<void> {
  await expect(page.getByTestId('library-pdf-preview')).toBeVisible({ timeout: 30_000 });
  const errorPane = page.getByTestId('library-pdf-error');
  await expect
    .poll(
      async () => {
        if ((await page.getByTestId('library-pdf-page').count()) > 0) return 'rendered';
        if ((await errorPane.count()) > 0) return `error: ${await errorPane.innerText()}`;
        return 'pending';
      },
      { timeout: 30_000, intervals: [250], message: 'the PDF must produce a page surface' },
    )
    .toBe('rendered');

  let previousPixels = -1;
  await expect
    .poll(
      async () => {
        const pixels = await firstPageNonWhitePixels(page);
        if (pixels <= 0) return 'canvas still blank';
        const settled = pixels === previousPixels;
        previousPixels = pixels;
        return settled ? 'painted' : 'still drawing';
      },
      { timeout: 30_000, intervals: [250], message: 'the PDF must draw pixels' },
    )
    .toBe('painted');
}

// ─────────────────────────────────────────────────────────────────────────────
// Gateway plumbing
// ─────────────────────────────────────────────────────────────────────────────

let api: APIRequestContext;
let control: ControlOrigin;
let workspaceID: string;

/**
 * The gateway's data directory.
 *
 * This spec writes its fixture into a workspace work tree ON DISK — the same
 * contract preview-pdf.spec.ts and preview-isolation.spec.ts use — because the
 * Library listing's top-level rows are what the SPA is driven through. It
 * therefore must run against a gateway on this machine, and says so loudly
 * rather than failing later with a selector timeout.
 */
function omnipusHome(): string {
  const home =
    process.env.OMNIPUS_HOME || (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '');
  if (!home || !fs.existsSync(home)) {
    throw new Error(
      `[preview-pdf-viewer-control] OMNIPUS_HOME does not exist: ${home || '(unset)'}. ` +
        'This suite writes its PDF fixture into a workspace work tree on disk, so it must run ' +
        'against a gateway on this machine.',
    );
  }
  return home;
}

test.beforeAll(async () => {
  control = await startControlOrigin(genuinePdf());

  api = await playwrightRequest.newContext({ baseURL: BASE_URL, storageState: AUTH_FILE });
  const res = await api.get('/api/v1/workspaces');
  expect(res.status(), 'GET /api/v1/workspaces must succeed').toBe(200);
  const list = (await res.json()) as Array<{ id: string; is_default?: boolean }>;
  expect(
    list.length,
    'the gateway must have at least one workspace to host the Library',
  ).toBeGreaterThan(0);
  workspaceID = (list.find((w) => w.is_default) ?? list[0]).id;

  const workDir = path.join(omnipusHome(), 'workspaces', workspaceID, 'work');
  fs.mkdirSync(workDir, { recursive: true });
  fs.writeFileSync(path.join(workDir, F_PDF), genuinePdf());
});

test.afterAll(async () => {
  if (api) {
    try {
      const workDir = path.join(omnipusHome(), 'workspaces', workspaceID, 'work');
      fs.rmSync(path.join(workDir, F_PDF), { force: true });
    } catch {
      // A fixture left behind is untidy, never a test result.
    }
    await api.dispose();
  }
  await control?.close();
});

/** Open the Library pop-out at the workspace and wait for the fixture's row. */
async function openLibrary(page: Page): Promise<void> {
  await page.goto(`/#/library?workspace=${workspaceID}`);
  await expect(page.getByTestId('library-explorer')).toBeVisible({ timeout: 30_000 });
  await expect(page.getByTestId(`library-row-${F_PDF}`)).toBeVisible({ timeout: 30_000 });
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

test.describe('ADR-067 test 57 / §13.4 item 4 — the browser-viewer negative control', () => {
  /**
   * THE CONTROL, RUN FIRST AND ON PURPOSE.
   *
   * It establishes two things the negative below is worth nothing without:
   *
   *   1. this browser HAS a working PDF viewer, and this file's oracle can SEE
   *      it. Headless it cannot — there is no viewer to see — and the negative
   *      would then be automatically true for every build ever made, including
   *      one that had abandoned PDF.js entirely;
   *   2. `Content-Disposition: attachment` is what keeps that viewer away. Same
   *      bytes, same origin, same type, one header different: inline mounts the
   *      viewer, attachment downloads. That is the mechanism §10.4's `.pdf` row
   *      relies on, measured rather than quoted.
   *
   * Ordering matters. If this fails, everything after it is INCONCLUSIVE rather
   * than passing, and reading it first is what makes that obvious in the report.
   */
  test('57 control — this browser HAS a PDF viewer, the oracle sees it, and `attachment` is what keeps it away', async ({
    page,
  }) => {
    // ── inline: the viewer mounts ────────────────────────────────────────────
    const inlineDownloads: string[] = [];
    page.on('download', (d) => inlineDownloads.push(d.suggestedFilename()));

    const response = await page.goto(`${control.origin}/inline.pdf`);
    expect(
      response,
      'a genuine inline PDF produced no response at top level — this browser has no working PDF path. ' +
        'Is this running headless? Every assertion in this file is then inconclusive (§0, experiment §6A.1).',
    ).not.toBeNull();
    expect(response!.status()).toBe(200);
    expect(
      inlineDownloads,
      'an INLINE pdf was downloaded rather than rendered — the headless artefact §0 warns about',
    ).toEqual([]);

    expect(
      await page.evaluate(() => document.contentType),
      'the PDF must be dispatched as application/pdf',
    ).toBe(TYPE_PDF);

    await expect
      .poll(() => browserPdfViewerFrames(page).length, {
        timeout: 20_000,
        message:
          "the browser's own PDF viewer never mounted for a genuine inline PDF. This file's whole " +
          'negative — "the Library preview is not the browser viewer" — is unmeasurable in this ' +
          'configuration, because the oracle cannot see a viewer even when there is one. ' +
          'Run under the `preview-headed` project.',
      })
      .toBeGreaterThan(0);
    expect(
      browserPdfViewerFrames(page)[0],
      'the viewer must be a browser-provided document, not something page content built',
    ).toMatch(/^chrome-extension:\/\/[a-p]{32}\//);

    // ── attachment: the SAME bytes never reach that viewer ───────────────────
    const attachPage = await page.context().newPage();
    const attachDownloads: string[] = [];
    attachPage.on('download', (d) => attachDownloads.push(d.suggestedFilename()));
    await attachPage.goto(`${control.origin}/attach.pdf`).catch(() => undefined);

    expect(
      attachDownloads,
      'the same PDF bytes with Content-Disposition: attachment must DOWNLOAD, not render — ' +
        'this is the mechanism §10.4 relies on to keep `.pdf` off the inline allow-list',
    ).toContain('attach.pdf');
    expect(
      browserPdfViewerFrames(attachPage),
      'an attachment reached the browser PDF viewer — the disposition is not doing its job',
    ).toEqual([]);
    await attachPage.close();
  });

  /**
   * Spec test 57 / §13.4 item 4 — THE NEGATIVE, now that the oracle is proved.
   *
   * One observation of one loaded preview, requiring RENDERING and
   * NON-DELEGATION to hold simultaneously. They are asserted together
   * deliberately: a preview that renders NOTHING satisfies every "the browser
   * viewer was not involved" assertion perfectly, which is the same false-green
   * shape that let a WebKit rendering defect ship behind a green isolation suite
   * (preview-isolation.spec.ts, 11c/11d).
   *
   *   RENDERED   — canvas pixels drawn AND settled, and the text layer carries a
   *                token that exists only inside the PDF's content stream. The
   *                text layer is what proves the DOCUMENT was parsed rather than
   *                some image having loaded.
   *   BY US      — a `<canvas>` our own component created, under the SPA's own
   *                test id.
   *   NOT BY THE — no `chrome-extension://` viewer frame; the document is still
   *   BROWSER      `text/html` on the gateway origin at the Library route; no
   *                download; no popup; and no `<embed>`/`<object>`/`<iframe>`
   *                anywhere pointing at PDF bytes.
   */
  test('57 — the Library PDF is drawn by PDF.js into our own canvas, and the browser viewer is never involved', async ({
    page,
  }) => {
    const downloads: string[] = [];
    page.on('download', (d) => downloads.push(d.suggestedFilename()));
    const popups: string[] = [];
    page.on('popup', (p) => popups.push(p.url()));
    const documentRequests: string[] = [];
    page.on('request', (r) => {
      if (r.resourceType() === 'document' && /\.pdf(\?|$)|library\/.*\/download/.test(r.url())) {
        documentRequests.push(r.url());
      }
    });

    await openLibrary(page);
    const urlBefore = new URL(page.url());

    await page.getByTestId(`library-row-${F_PDF}`).click();
    await expect(page.getByTestId('library-preview-title')).toHaveText(F_PDF, { timeout: 20_000 });
    await waitForPdfPainted(page);

    // ── RENDERED, and rendered by parsing the document ───────────────────────
    expect(
      await page.locator('[data-testid="library-pdf-page"] canvas').count(),
      'PDF.js must draw into a canvas our own component created',
    ).toBeGreaterThan(0);
    expect(
      await firstPageNonWhitePixels(page),
      'the canvas is blank — a preview that renders nothing satisfies every negative below for free',
    ).toBeGreaterThan(0);
    await expect(
      page.locator('.omnipus-pdf-text-layer').first(),
      'the text layer must carry a token that exists only inside the PDF content stream — ' +
        'that is what proves the DOCUMENT was parsed rather than an image having loaded',
    ).toContainText(PDF_TEXT_MARKER);

    // ── NOT BY THE BROWSER'S OWN VIEWER ──────────────────────────────────────
    // Meaningful only because the control above proved this oracle can see a
    // viewer when one is there.
    expect(
      browserPdfViewerFrames(page),
      "the browser's own PDF viewer rendered the Library preview — FR-014/FR-018 require PDF.js, " +
        'and a browser viewer is a surface Omnipus does not control',
    ).toEqual([]);

    expect(
      await page.evaluate(() => document.contentType),
      'the document must still be the SPA, not a PDF the browser took over',
    ).toBe('text/html');

    const urlAfter = new URL(page.url());
    expect(urlAfter.origin, 'the document must still be the gateway origin').toBe(urlBefore.origin);
    // Deliberately NOT a URL equality check: selecting a file pushes `&path=…`
    // into the hash, which is FR-012's deep linking — an in-app router
    // navigation that would fail an equality check while proving nothing.
    expect(urlAfter.pathname, 'the document must still be the SPA shell, not the PDF').toBe(
      urlBefore.pathname,
    );
    expect(
      urlAfter.hash.startsWith('#/library'),
      `still on the Library route (${urlAfter.hash})`,
    ).toBe(true);

    expect(downloads, 'a previewed PDF must never download').toEqual([]);
    expect(popups, 'a previewed PDF must never open a tab').toEqual([]);
    expect(
      documentRequests,
      'the PDF must never be requested as a browser DOCUMENT — that is the request that would hand it to the browser viewer',
    ).toEqual([]);

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
      );
    expect(
      embeddedSources.filter((u) => /\.pdf(\?|$)|library\/.*\/download/.test(u)),
      'no <embed>/<object>/<iframe> may point at the PDF — each of the three would hand it to the browser viewer',
    ).toEqual([]);
  });

  /**
   * The server-side half of the same mechanism (FR-018, MV-13's second half).
   *
   * PDF.js fetches its bytes over the AUTHENTICATED Library endpoint. That
   * response must keep `attachment` and the extension-derived type, so that even
   * a future SPA change that NAVIGATED to the URL instead of fetching it gets a
   * download rather than a browser document — which the control above proved is
   * the difference between the two.
   *
   * MV-13's negative half rides along: the authenticated path carries NO
   * isolation policy. Asserting that is what stops the two paths quietly
   * becoming one.
   */
  test('57 (server half) — the authenticated Library URL a PDF is fetched from answers with an attachment and no policy', async () => {
    const res = await api.get(
      `/api/v1/library/${workspaceID}/download?path=${encodeURIComponent(F_PDF)}`,
    );
    expect(res.status()).toBe(200);
    const h = res.headers();
    expect(h['content-type'], 'FR-015: the extension decides, never the bytes').toBe(TYPE_PDF);
    expect(
      h['content-disposition'],
      'FR-003g: the authenticated Library path keeps serving attachments unchanged',
    ).toMatch(/^attachment/);
    expect(h['x-content-type-options']).toBe('nosniff');
    expect(
      h['content-security-policy'],
      'the authenticated path must NOT carry the isolation policy (MV-13, second half)',
    ).toBeUndefined();
    // NON-VACUITY: these really are the PDF's bytes, so the header assertions
    // above are about the file under test and not about an error page.
    expect((await res.body()).subarray(0, 5).toString('latin1')).toBe('%PDF-');
  });
});
