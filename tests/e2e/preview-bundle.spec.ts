/**
 * preview-bundle.spec.ts — ADR-067 §13.1 tests 9, 60 and 64.
 *
 *   9   `E2E_PreviewBundle_AllAssetsLoad`  — US-1 AS-4, FR-015c. A real browser
 *                                            loads css + js + font + audio from
 *                                            a bundle through the token path.
 *   60  `E2E_FontAppliesWithCorsHeader`    — AC-15.1, FR-019. Asserted by
 *                                            RENDERED WIDTH. `document.fonts`
 *                                            is NOT the oracle: it reports
 *                                            "loaded" on failure.
 *   64  `E2E_BundleLoadsViaTokenPath`      — FR-003a. Against the REAL
 *                                            AUTHENTICATED gateway, not a
 *                                            static server. That gap is what
 *                                            hid FR-003a.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHY THIS FILE IS NOT A SUBSET OF preview-isolation.spec.ts
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * That file's 11c/11d already prove a previewed bundle's external SCRIPT runs
 * and its external STYLESHEET applies, under the FR-005b composition, with zero
 * egress. This file adds the two subresource classes its bundle does not carry —
 * a WEBFONT and AUDIO — and they are exactly the two that fail differently:
 *
 *   a font is blocked by CORS, not by CSP. §10.3 already says
 *     `font-src 'self' ${GATEWAY_ORIGIN}`, so the policy permits it and it
 *     still does not load without `Access-Control-Allow-Origin` — because the
 *     document's origin is OPAQUE, which makes every request it issues
 *     cross-origin, gateway's own bytes included (FR-019).
 *   audio needs a SPECIFIC Content-Type. FR-015c/MV-14: an extension missing
 *     from the compiled-in table is served `application/octet-stream` with
 *     `nosniff`, and every browser then refuses to play it. Nothing about that
 *     failure is visible in a CSP or in a status code.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * THE ORACLE FOR "THE FONT APPLIED", AND THE ONE THAT IS FORBIDDEN
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * §0, on the measured webfont row: *"`document.fonts.status` reports 'loaded'
 * on failure and MUST NOT be used."* The experiment (§6A.2) recorded it
 * reporting `"loaded"` while two of three faces sat in `status: "error"` —
 * "Confirmed liar."
 *
 * The same measurement also records the fixture's OWN oracle being broken for
 * two independent reasons: the font had no glyph for the characters being
 * measured, and the measured element was block-level, so its width was
 * font-independent. Both are avoided here deliberately — the measured text is
 * plain ASCII (covered by any Latin face) on an `inline-block` element with
 * `white-space: pre`, and the metric is a RENDERED WIDTH read off
 * `getBoundingClientRect()`.
 *
 * The comparison is DIFFERENTIAL, not absolute: the same string is measured in
 * `'E2EBundleFont', monospace` and in `monospace` alone, in the same document at
 * the same size. If the bundle face loaded the two differ; if it did not, the
 * first falls back to the second and they are identical. No hard-coded pixel
 * expectation, so nothing here rots on a font-rendering change.
 *
 * `@font-face` is declared in an INLINE `<style>`, never in the bundle's own
 * stylesheet — otherwise a stylesheet failure and a font failure would be the
 * same observation, and test 9 and test 60 could not fail independently.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * EVERY ASSERTION HERE HAS BEEN GIVEN A WAY TO FAIL
 * ═══════════════════════════════════════════════════════════════════════════
 *
 *   the font width oracle   — TWO mutations, and it needs both. MUTATION A
 *                             repoints `font-src` at a dead origin and requires
 *                             the two widths to become IDENTICAL; that one is
 *                             enforced on all three engines, so the oracle is
 *                             proved capable of reporting failure EVERYWHERE.
 *                             MUTATION B removes `Access-Control-Allow-Origin`
 *                             and asserts the per-engine MEASURED outcome. A
 *                             claim that a font applied is worth nothing until
 *                             the oracle has been seen to report that it did
 *                             not — on the engine making the claim.
 *   the egress oracle       — a positive control fires the same probes from an
 *                             un-policed origin and requires them to ARRIVE.
 *                             "Nothing reached the second origin" is also what
 *                             a broken harness reports.
 *   the containment claims  — measured in the SAME observation as the rendering
 *                             claims, never separately. A preview that renders
 *                             NOTHING satisfies every "nothing escaped"
 *                             assertion perfectly; that is precisely how a
 *                             WebKit rendering defect shipped behind a green
 *                             isolation suite (see 11c/11d's comments).
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * A RESULT THIS FILE PRODUCED, AND THE SPEC DOES NOT YET REFLECT
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * §0 records the webfont CORS row as measured on CHROMIUM ONLY, with Firefox
 * and WebKit explicitly unmeasured. Finishing that measurement (2026-09-10, and
 * reproduced by a standalone probe outside this suite) gave:
 *
 *   Chromium 151  ENFORCES — no header, `net::ERR_FAILED`, face does not render
 *   Firefox 153   ENFORCES — response arrives 200, then REJECTED at the font
 *                            layer; a network-status oracle would call it a pass
 *   WebKit 26.5   DOES NOT ENFORCE — the face renders with no header at all,
 *                            and NOT only under an opaque origin: an ordinary
 *                            cross-origin font with no header renders too
 *
 * FR-019 is unaffected and still asserted unconditionally — on two of three
 * engines the bundle's font does not render without the header, so the gateway
 * must send it. What the result changes is the REVERSE reading: font CORS is not
 * a containment control, because on WebKit it stops nothing. §10.4 already
 * classifies fonts as "Not executable" and rests containment on the §10.3
 * policy, so the design is unaffected — but §0's table currently reads as a
 * universal browser fact and it is not one. See FONT_CORS_ENFORCED_BY_ENGINE.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHY THE MUTATIONS RUN TOP-LEVEL, AND WHY NOT THROUGH `page.route`
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * The product case must be measured in the shape the product ships (§10.6,
 * FR-005b): `<iframe src="<token URL>">` with `sandbox="allow-scripts"`,
 * `referrerpolicy="no-referrer"` and an empty `allow=""`. Never `srcdoc` —
 * relative URLs would resolve against the embedder and no bundle subresource
 * would load at all.
 *
 * The MUTATIONS cannot run there. Measured 2026-08-23 and recorded in
 * `harness.ts`: WebKit surfaces NONE of a sandboxed iframe's requests to the
 * driver, so an interception aimed at a subresource of an embedded preview is
 * not reliable on all three engines — and a mutation that silently never fires
 * reports as a pass. Top-level, every engine reports, and the document still
 * carries the same §10.3-shaped policy with the same opaque origin, so the font
 * request is cross-origin in exactly the same way.
 *
 * ⚠️ AND THEY CANNOT USE `page.route` AT ALL, WHICH IS A MEASURED CORRECTION
 * RATHER THAN A PREFERENCE. The first version of MUTATION B intercepted the
 * gateway's own font response and deleted the header. Chromium does not apply
 * the CORS check to a response delivered by `route.fulfill()`: the font rendered
 * anyway — cold context, opaque origin, interception counter confirming the
 * route had fired. A harness that silently suppresses the very check it is
 * trying to provoke would have "proved" the header unnecessary on the engine
 * that enforces it most strictly. preview-svg.spec.ts's header records the same
 * class of `fulfill()` artefact for a different subresource. Both mutations
 * therefore run over REAL SOCKETS from a local origin, where the only thing that
 * differs between a pair of pages is the one header or the one directive under
 * test.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * ENGINES, AND WHAT THAT FIXES
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * This file is listed in `ISOLATION_SPEC_FILES`, so it runs under
 * `isolation-chromium`, `isolation-firefox` and `isolation-webkit`, each with
 * `retries: 0` at project level. That is deliberate and it closes a stated gap:
 * §0 records the webfont CORS result as **measured on Chromium ONLY** —
 * *"Not measured on Firefox or WebKit — those runs were stopped."* Running the
 * mutation on all three is where that gets fixed.
 *
 * Retries are pinned at zero a second time at file level below, so running this
 * under any other project still cannot retry it.
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
import { createRequire } from 'node:module';
import { embedPreview } from './fixtures/preview-isolation/harness.js';
import { expectedIsolationPolicy } from './fixtures/preview-isolation/policy-oracle.js';

// FILE-level retry pin. See the header.
test.describe.configure({ retries: 0 });

const BASE_URL = process.env.OMNIPUS_URL || 'http://localhost:6060';
const AUTH_FILE = process.env.OMNIPUS_AUTH_FILE || './tests/e2e/fixtures/.auth/admin.json';

/**
 * The §10.3 policy, read from the SPECIFICATION's own markdown and never from
 * `pkg/gateway` — see policy-oracle.ts's header for why the direction is the
 * whole point of an oracle.
 */
const ISOLATION_POLICY = expectedIsolationPolicy();

/**
 * Expected `Content-Type` values for the bundle's four subresource classes.
 *
 * Read off §10.4's table and FR-015c, which enumerates them by name. `.css` and
 * `.js` are on this list because leaving them off was the round-4 defect: inline
 * allowed with no type means `application/octet-stream` plus mandatory
 * `nosniff`, and every browser then refuses a bundle's own stylesheet and
 * script — breaking US-1 AS-4, the flagship scenario.
 */
const TYPES = {
  html: 'text/html; charset=utf-8',
  css: 'text/css; charset=utf-8',
  js: 'text/javascript; charset=utf-8',
  woff2: 'font/woff2',
  wav: 'audio/wav',
} as const;

/** Fixed settle window before a NEGATIVE about egress is believed. */
const SETTLE_MS = 2_500;

/** The vectors the bundle fires at the second origin. */
const VECTORS = ['img', 'fetch', 'beacon', 'ws', 'iframe'] as const;

/**
 * The subset the POSITIVE CONTROL must show arriving.
 *
 * Three of the five, deliberately. These are the ones an un-policed HTML
 * document fires unconditionally on every engine; `ws` and `iframe` depend on
 * engine-specific timing that would trade a proof for a flake. Three of three
 * arriving is already conclusive for the assertion the negatives make, which is
 * that the list is EMPTY — and the negatives are asserted over ALL five.
 */
const CONTROL_VECTORS = ['img', 'fetch', 'beacon'] as const;

/**
 * The measured string and its size.
 *
 * Plain ASCII, so glyph coverage cannot be the reason two widths agree — the
 * exact defect that made the experiment's own font oracle carry no information
 * (§6A.2). Long enough that a per-character advance difference accumulates well
 * past any sub-pixel rounding.
 */
const FONT_SAMPLE_TEXT = 'AVWimlt0O QjgyRZ AVWimlt0O QjgyRZ';
const FONT_SAMPLE_PX = 48;

/**
 * How different two rendered widths must be before the bundle face is called
 * applied.
 *
 * A fraction of the fallback width, not an absolute pixel count, so it does not
 * depend on the platform's default monospace face. The two states this has to
 * separate are "a proportional face rendered this" and "it fell back to the
 * same monospace face the control span uses" — which is EXACT equality, so any
 * threshold above measurement noise works and 5% is chosen to be obviously
 * above it in both directions.
 */
const FONT_APPLIED_MIN_DELTA = 0.05;

// ─────────────────────────────────────────────────────────────────────────────
// The second origin: a request sink that records what ARRIVES.
// ─────────────────────────────────────────────────────────────────────────────

interface Sink {
  origin: string;
  hits: string[];
  close: () => Promise<void>;
}

/**
 * Start the stand-in-for-the-internet origin.
 *
 * Records WebSocket upgrades too — Node routes those to the `upgrade` event and
 * NOT to the request handler, and missing that event is how a WebSocket probe
 * silently becomes "nothing arrived".
 */
async function startSink(): Promise<Sink> {
  const hits: string[] = [];
  const sockets = new Set<Socket>();
  const record = (req: IncomingMessage) => hits.push((req.url || '').split('?')[0]);

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
// The control origin: the same bundle page with the isolation removed.
// ─────────────────────────────────────────────────────────────────────────────

interface ControlFile {
  body: Buffer;
  contentType: string;
  /**
   * `Access-Control-Allow-Origin`, per file.
   *
   * Per-file rather than global because the whole of test 60's proof of failure
   * is two responses that differ in this one header and in nothing else.
   */
  cors?: boolean;
  /** Content-Security-Policy, per file. Absent means no policy at all. */
  policy?: string;
}

interface ControlOrigin {
  origin: string;
  put: (name: string, file: ControlFile) => void;
  close: () => Promise<void>;
}

/**
 * Start the local origin every control and every mutation is served from.
 *
 * Per-file `Content-Type`, per-file `Access-Control-Allow-Origin` and per-file
 * `Content-Security-Policy`, plus a readable probe cookie — the same shape as
 * the experiment's `server.py`. Because all three are per-file, the SAME bytes
 * can be served twice from the SAME origin differing in exactly one header,
 * which is what makes any difference in outcome attributable to that header and
 * to nothing else.
 *
 * Served locally rather than by editing `pkg/gateway`, because this suite does
 * not own that package and a test that edits the thing it is testing proves
 * nothing about what ships.
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
    const headers: Record<string, string> = {
      'Content-Type': file.contentType,
      'X-Content-Type-Options': 'nosniff',
      'Content-Disposition': 'inline',
      'Content-Length': String(file.body.length),
      'Set-Cookie': 'omnipus_probe=SECRET; Path=/; SameSite=Strict',
    };
    if (file.cors) headers['Access-Control-Allow-Origin'] = '*';
    if (file.policy) headers['Content-Security-Policy'] = file.policy;
    res.writeHead(200, headers);
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

/**
 * The §10.3 policy with its host sources repointed at another origin.
 *
 * MECHANICALLY DERIVED FROM THE LIVE TEMPLATE, never hand-copied. §10.3 carries
 * `'self'` AND explicit gateway origins in six directives, and it has already
 * changed shape once (the 2026-08-23 amendment added the explicit origins; the
 * 2026-09-09 one removed the IPv6 spelling). A policy transcribed into this
 * file would keep testing the policy we used to have — silently, and green.
 *
 * Every `http(s)://…` token is rewritten, and `'self'` is deliberately LEFT
 * ALONE: under this policy's own `sandbox` directive the document's origin is
 * opaque, so `'self'` grants nothing, and removing it would change a second
 * variable at the same time as the one under test.
 */
/**
 * The same policy with ONLY its `font-src` directive repointed at a dead origin.
 *
 * Derived from the live policy rather than written out, for the same reason
 * `repointPolicyTo` is: a hand-copied policy keeps testing the policy we used to
 * have. Every other directive is left exactly as it was, so a document served
 * this way differs from the baseline in the font and in nothing else — its
 * stylesheet, its script and its opaque origin are all unchanged, and each of
 * those is asserted alongside the font.
 */
function deadFontPolicy(policy: string): string {
  return policy.replace(/font-src[^;]*/, `font-src ${DEAD_ORIGIN}`);
}

function repointPolicyTo(policy: string, origin: string): string {
  return policy
    .replace(/https?:\/\/[^\s;]+/g, origin)
    // A loopback bind emits two spellings, which both collapse to the same
    // origin here. Duplicates are harmless to a browser but make the string
    // hard to read in a failure message.
    .replace(new RegExp(`(${origin.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')})(\\s+\\1)+`, 'g'), '$1');
}

function closeServer(server: Server, sockets: Set<Socket>): Promise<void> {
  for (const s of sockets) s.destroy();
  return new Promise<void>((resolve) => server.close(() => resolve()));
}

// ─────────────────────────────────────────────────────────────────────────────
// Bundle fixture construction
// ─────────────────────────────────────────────────────────────────────────────

/**
 * A REAL webfont, taken from a package this repository already depends on.
 *
 * A hand-built or truncated font would test the browser's error path rather
 * than FR-019's CORS path. `@fontsource/inter` is a production dependency of the
 * SPA (package.json), so it is present wherever this suite can run at all — and
 * if it ever is not, this throws by name instead of quietly measuring a fallback
 * face and reporting "the font did not apply" as a product failure.
 *
 * Inter is PROPORTIONAL, which is what makes the differential oracle work: its
 * advances cannot coincide with the monospace fallback's the way a monospace
 * bundle face conceivably could.
 */
function bundleFontBytes(): Buffer {
  const require_ = createRequire(import.meta.url);
  let fontPath: string;
  try {
    fontPath = require_.resolve('@fontsource/inter/files/inter-latin-400-normal.woff2');
  } catch (e) {
    throw new Error(
      '[preview-bundle] @fontsource/inter is not installed, so test 60 has no real font to serve. ' +
        'It is a production dependency in package.json — run `npm ci`. Refusing to substitute a ' +
        "synthetic font, which would measure the browser's error path instead of FR-019.",
      { cause: e },
    );
  }
  const bytes = fs.readFileSync(fontPath);
  // A woff2 file starts with the signature 'wOF2'. Asserted so a zero-length or
  // LFS-pointer file fails here, naming the cause, rather than downstream as
  // "the font did not apply".
  expect(bytes.subarray(0, 4).toString('latin1'), 'the fixture font must be real woff2').toBe('wOF2');
  return bytes;
}

/**
 * A real, playable WAV: 16-bit mono PCM, 8 kHz, a quarter second of a sine.
 *
 * Synthesised rather than committed because a WAV is fully specified by its
 * header and needs no codec, so nothing about it can be subtly wrong in a way a
 * committed binary would hide. The oracle downstream is `duration` and
 * `readyState`, both of which require the browser to have PARSED the container —
 * which it refuses to attempt if the Content-Type is `application/octet-stream`
 * under `nosniff`, and that refusal is the FR-015c failure this asset exists to
 * catch.
 */
function wavBytes(): Buffer {
  const rate = 8000;
  const samples = Math.floor(rate * 0.25);
  const data = Buffer.alloc(samples * 2);
  for (let i = 0; i < samples; i++) {
    data.writeInt16LE(Math.round(Math.sin((2 * Math.PI * 440 * i) / rate) * 12000), i * 2);
  }
  const header = Buffer.alloc(44);
  header.write('RIFF', 0, 'latin1');
  header.writeUInt32LE(36 + data.length, 4);
  header.write('WAVE', 8, 'latin1');
  header.write('fmt ', 12, 'latin1');
  header.writeUInt32LE(16, 16); // PCM fmt chunk size
  header.writeUInt16LE(1, 20); // format = PCM
  header.writeUInt16LE(1, 22); // channels
  header.writeUInt32LE(rate, 24);
  header.writeUInt32LE(rate * 2, 28); // byte rate
  header.writeUInt16LE(2, 32); // block align
  header.writeUInt16LE(16, 34); // bits per sample
  header.write('data', 36, 'latin1');
  header.writeUInt32LE(data.length, 40);
  return Buffer.concat([header, data]);
}

/** The bundle's own stylesheet. Its ONLY job is to be observably applied. */
const BUNDLE_CSS = `#css-probe { letter-spacing: 7px; }\n`;

/** The bundle's own external script. Its ONLY job is to be observably run. */
const BUNDLE_JS = `window.__E2E_BUNDLE_JS_RAN = true;\n`;

/**
 * The bundle's entry document.
 *
 * Four separately-observable subresources, plus the egress probes, plus one
 * report written into `#result`.
 *
 * ORDERING RULE, and it is load-bearing: the probes fire on load, and the report
 * is written only after the font and audio have settled or timed out. Nothing
 * navigates the document away (no form submit, no popup) — those two vectors
 * belong to preview-isolation.spec.ts, and firing them here would race the
 * report off the page, which is the experiment's own recorded harness defect
 * (§6, "Known harness defect").
 *
 * The report is read out of the frame's DOM rather than from the network,
 * because WebKit surfaces none of a sandboxed frame's requests to the driver
 * (harness.ts, measured 2026-08-23) — a request-count oracle would be blind
 * there and every negative would be automatically true.
 */
function bundleIndexHtml(sinkOrigin: string, caseTag: string, fontHref = F_FONT): string {
  return `<!doctype html><html><head><meta charset="utf-8"><title>preview bundle fixture</title>
<link rel="stylesheet" href="style.css">
<style>
/* Declared INLINE, never in style.css: a stylesheet failure and a font failure
   must be independently observable, or tests 9 and 60 cannot fail apart. */
@font-face {
  font-family: 'E2EBundleFont';
  src: url('${fontHref}') format('woff2');
  font-display: block;
}
.metric {
  display: inline-block;
  white-space: pre;
  font-size: ${FONT_SAMPLE_PX}px;
  font-weight: 400;
  font-style: normal;
}
</style>
</head><body>
<div id="css-probe">css probe</div>
<span id="w-bundle" class="metric" style="font-family: 'E2EBundleFont', monospace;">${FONT_SAMPLE_TEXT}</span>
<span id="w-fallback" class="metric" style="font-family: monospace;">${FONT_SAMPLE_TEXT}</span>
<audio id="audio" src="tone.wav" preload="auto"></audio>
<div id="host"></div>
<pre id="result"></pre>
<script src="app.js"></script>
<script>
(function () {
  var U = function (v) { return ${JSON.stringify(sinkOrigin)} + "/x/${caseTag}/" + v; };
  var report = { attempted: [], refused: [] };

  try { report.origin_opaque = String(window.origin) === "null"; }
  catch (e) { report.origin_opaque = "THREW:" + e.name; }
  try { report.cookie = document.cookie; }
  catch (e) { report.cookie = "THREW:" + e.name; }

  // Egress probes. Blocked by the SOURCE directives of the §10.3 policy; the
  // sandbox-only vectors (form, popup) are deliberately absent — see this
  // fixture's doc comment.
  //
  // TWO FACTS ARE RECORDED SEPARATELY, and conflating them was a real defect in
  // the first version of this fixture:
  //
  //   attempted — pushed BEFORE the call, unconditionally. It records that the
  //               inline script REACHED this probe site, which is the
  //               non-vacuity fact every negative below rests on. A script that
  //               died halfway leaves a short list; one that never ran leaves
  //               no report at all.
  //   refused   — the API boundary said no, synchronously: 'sendBeacon'
  //               returned false, or the constructor threw.
  //
  // The first version pushed to 'attempted' only when the API ACCEPTED the
  // call, which is a different fact wearing the same name. Measured on Firefox
  // 153 under the §10.3 policy: 'navigator.sendBeacon' returns FALSE and
  // 'new WebSocket(...)' THROWS, so two of five vectors never appeared in the
  // list and the non-vacuity check failed on an engine where containment was in
  // fact stronger than on Chromium.
  //
  // Keeping them apart matters in the direction that protects the result: a
  // vector the engine refuses at the API boundary NEVER REACHES THE NETWORK, so
  // counting it as "fired, and then blocked by the policy" would credit §10.3
  // for something the engine did on its own. 'refused' is therefore carried
  // into the failure message as evidence, and asserted only where its ABSENCE
  // is the point — in the no-policy control, where an open API boundary is what
  // makes the policied run's refusals attributable to the policy.
  function probe(name, fire) {
    report.attempted.push(name);
    try {
      if (fire() === false) report.refused.push(name);
    } catch (e) {
      report.refused.push(name + ":" + (e && e.name ? e.name : "Error"));
    }
  }

  probe("img", function () { new Image().src = U("img"); });
  probe("fetch", function () { fetch(U("fetch"), { mode: "no-cors" }).catch(function () {}); });
  probe("beacon", function () { return navigator.sendBeacon(U("beacon"), "x"); });
  probe("ws", function () { new WebSocket(U("ws").replace("http", "ws")); });
  probe("iframe", function () {
    var fr = document.createElement("iframe");
    fr.setAttribute("src", U("iframe"));
    document.getElementById("host").appendChild(fr);
  });

  function measure(id) {
    var el = document.getElementById(id);
    return el ? el.getBoundingClientRect().width : -1;
  }

  var audio = document.getElementById("audio");
  var audioSettled = false;
  function audioDone() { audioSettled = true; }
  audio.addEventListener("loadedmetadata", audioDone);
  audio.addEventListener("error", audioDone);

  function finish() {
    report.js_ran = window.__E2E_BUNDLE_JS_RAN === true;
    var probe = document.getElementById("css-probe");
    report.css_applied =
      window.getComputedStyle(probe).letterSpacing === "7px";
    report.font_bundle_width = measure("w-bundle");
    report.font_fallback_width = measure("w-fallback");
    report.audio_ready_state = audio.readyState;
    report.audio_duration = audio.duration;
    report.audio_error = audio.error ? audio.error.code : null;
    report.attempted = report.attempted.join(",");
    report.refused = report.refused.join(",");
    document.getElementById("result").textContent = JSON.stringify(report);
  }

  // Settle, then report. The font wait uses document.fonts.ready purely as a
  // TIMING signal — never as the success oracle, which §0 forbids outright
  // ("reports 'loaded' on failure ... Confirmed liar"). The success oracle is
  // the rendered-width difference computed by the test.
  var fontsSettled = false;
  try {
    document.fonts.ready.then(function () { fontsSettled = true; });
  } catch (e) { fontsSettled = true; }
  var waited = 0;
  var timer = setInterval(function () {
    waited += 100;
    if ((fontsSettled && audioSettled) || waited >= 5000) {
      clearInterval(timer);
      finish();
    }
  }, 100);
})();
</script>
</body></html>
`;
}

// ─────────────────────────────────────────────────────────────────────────────
// Gateway plumbing: workspace, bundle upload, one preview token.
// ─────────────────────────────────────────────────────────────────────────────

/** Library-relative directory this spec owns. Fixed, and wiped before use. */
const FIXTURE_DIR = 'e2e-preview-bundle';

const F_INDEX = 'index.html';
const F_CSS = 'style.css';
const F_JS = 'app.js';
const F_FONT = 'font.woff2';
const F_WAV = 'tone.wav';

/**
 * A Library directory OUTSIDE the bundle, holding a file that demonstrably
 * exists. Test 64's scope assertion needs one: a 404 for a path that was never
 * there proves nothing about the token's scope.
 */
const OUTSIDE_DIR = 'e2e-preview-bundle-outside';
const F_OUTSIDE = 'outside.txt';
const OUTSIDE_MARKER = 'omnipus-outside-the-bundle-scope';

/**
 * A loopback port nothing listens on, used to repoint `font-src` at an origin
 * the font is demonstrably not served from.
 */
const DEAD_ORIGIN = 'http://127.0.0.1:1';

/**
 * WHETHER EACH ENGINE ENFORCES CORS ON A WEBFONT — MEASURED, NOT ASSUMED.
 *
 * §0 records the webfont row as *"Measured — Chromium only … Not measured on
 * Firefox or WebKit — those runs were stopped."* This table is the result of
 * finishing that measurement, on 2026-09-10, on the engines this file runs on.
 * Each cell was reproduced twice: once through this spec, and once through a
 * standalone probe that took the product entirely out of the picture (a local
 * origin, a real socket, one font served with the header and one without).
 *
 *   Chromium 151  ENFORCES.  No header → `net::ERR_FAILED`, the FontFace goes
 *                            to `status: "error"`, the face does not render.
 *   Firefox 153   ENFORCES.  The response arrives 200 at the network layer and
 *                            is then REJECTED at the font layer — same
 *                            `status: "error"`, same fallback. Note the
 *                            difference in mechanism: a network-status oracle
 *                            would call this a success.
 *   WebKit 26.5   DOES NOT ENFORCE. The face renders with no header at all.
 *
 * ⚠️ WEBKIT'S RESULT IS NOT SPECIFIC TO OPAQUE ORIGINS, WHICH IS WHY IT IS
 * WRITTEN DOWN HERE RATHER THAN TREATED AS A QUIRK OF THIS FIXTURE. The probe
 * measured the ordinary case too — a normal, non-sandboxed document on origin A
 * loading a font from origin B with no `Access-Control-Allow-Origin` — and
 * WebKit rendered that as well, where Chromium and Firefox both refused it. So
 * on WebKit a webfont's CORS gate is not a control at all.
 *
 * WHAT THAT DOES AND DOES NOT MEAN FOR THIS PRODUCT. FR-019's requirement is
 * unaffected and still asserted unconditionally in test 9 (wire): the gateway
 * MUST send the header, because on two of three engines the bundle's font does
 * not render without it. What changes is the reverse reading — nothing in the
 * isolation design may treat font CORS as a CONTAINMENT control, because on
 * WebKit it stops nothing. §10.4 already classifies fonts as "Not executable"
 * and rests containment on the §10.3 policy, so this costs the design nothing;
 * it is recorded because §0's table currently reads as a universal browser fact
 * and it is not one.
 *
 * BOTH DIRECTIONS ARE ASSERTED, deliberately. The WebKit row is asserted to be
 * TRUE-that-it-renders rather than skipped, so that a future WebKit release
 * which starts enforcing turns this file RED with a message saying so, instead
 * of the table quietly rotting into folklore. A test that "handles" an engine
 * difference by asserting nothing on that engine is how the Chromium-only
 * measurement became a universal claim in the first place.
 */
const FONT_CORS_ENFORCED_BY_ENGINE: Record<string, boolean> = {
  chromium: true,
  firefox: true,
  webkit: false,
};

/** The two font responses on the control origin, one header apart. */
const FONT_WITH_CORS = 'font-cors.woff2';
const FONT_WITHOUT_CORS = 'font-nocors.woff2';

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

/** The double-submit CSRF echo every state-changing call needs. */
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

  const font = bundleFontBytes();
  const wav = wavBytes();

  const upload = await api.post(
    `/api/v1/library/${workspaceID}/upload?path=${encodeURIComponent(FIXTURE_DIR)}`,
    {
      headers: csrf,
      multipart: {
        f0: {
          name: F_INDEX,
          mimeType: 'text/html',
          buffer: Buffer.from(bundleIndexHtml(sink.origin, 'bundle')),
        },
        f1: { name: F_CSS, mimeType: 'text/css', buffer: Buffer.from(BUNDLE_CSS) },
        f2: { name: F_JS, mimeType: 'text/javascript', buffer: Buffer.from(BUNDLE_JS) },
        // The multipart part declares application/octet-stream on purpose: the
        // SERVED type must come from the extension table and from nothing else
        // (FR-015b), so an implementation that remembered the upload's declared
        // type would answer octet-stream here and the font would not load.
        f3: { name: F_FONT, mimeType: 'application/octet-stream', buffer: font },
        f4: { name: F_WAV, mimeType: 'application/octet-stream', buffer: wav },
      },
    },
  );
  expect(upload.status(), `upload bundle: ${await upload.text()}`).toBeLessThan(300);

  // A file OUTSIDE the bundle the token will be scoped to, so test 64's scope
  // assertion is about a file that demonstrably EXISTS. A 404 for a path that
  // was never there proves nothing about scoping.
  await api.delete(
    `/api/v1/library/${workspaceID}/entries?path=${encodeURIComponent(OUTSIDE_DIR)}`,
    { headers: csrf },
  );
  const mkdirOutside = await api.post(`/api/v1/library/${workspaceID}/mkdir`, {
    headers: { ...csrf, 'Content-Type': 'application/json' },
    data: { path: OUTSIDE_DIR },
  });
  expect(mkdirOutside.status(), `mkdir ${OUTSIDE_DIR}: ${await mkdirOutside.text()}`).toBeLessThan(300);
  const uploadOutside = await api.post(
    `/api/v1/library/${workspaceID}/upload?path=${encodeURIComponent(OUTSIDE_DIR)}`,
    {
      headers: csrf,
      multipart: {
        f0: {
          name: F_OUTSIDE,
          mimeType: 'text/plain',
          buffer: Buffer.from(`${OUTSIDE_MARKER}\n`),
        },
      },
    },
  );
  expect(uploadOutside.status(), `upload outside fixture: ${await uploadOutside.text()}`).toBeLessThan(300);

  // ── The control origin ───────────────────────────────────────────────────
  //
  // The same bundle, served three ways. The two policied variants differ from
  // each other in EXACTLY ONE header — `Access-Control-Allow-Origin` on the
  // font — which is what makes test 60's mutation attributable to that header.
  const controlPolicy = repointPolicyTo(ISOLATION_POLICY, control.origin);

  control.put(F_CSS, { body: Buffer.from(BUNDLE_CSS), contentType: TYPES.css });
  control.put(F_JS, { body: Buffer.from(BUNDLE_JS), contentType: TYPES.js });
  control.put(F_WAV, { body: wav, contentType: TYPES.wav });
  // The two font responses: identical bytes, identical type, one header apart.
  control.put(FONT_WITH_CORS, { body: font, contentType: TYPES.woff2, cors: true });
  control.put(FONT_WITHOUT_CORS, { body: font, contentType: TYPES.woff2, cors: false });

  // No policy at all — the egress positive control.
  control.put('no-policy.html', {
    body: Buffer.from(bundleIndexHtml(sink.origin, 'ctrl', FONT_WITH_CORS)),
    contentType: TYPES.html,
  });
  // The §10.3 policy, repointed at this origin. Same document, same opaque
  // origin, differing only in which font it asks for.
  control.put('policy-cors.html', {
    body: Buffer.from(bundleIndexHtml(sink.origin, 'mutcors', FONT_WITH_CORS)),
    contentType: TYPES.html,
    policy: controlPolicy,
  });
  control.put('policy-nocors.html', {
    body: Buffer.from(bundleIndexHtml(sink.origin, 'mutnocors', FONT_WITHOUT_CORS)),
    contentType: TYPES.html,
    policy: controlPolicy,
  });
  // The CSP mutation: the font is served perfectly (header and all), and the
  // POLICY refuses it. Measured 2026-09-10 to be enforced on all three engines,
  // which is what makes it the width oracle's universal proof of failure —
  // including on WebKit, where the CORS mutation cannot serve that role.
  control.put('policy-deadfont.html', {
    body: Buffer.from(bundleIndexHtml(sink.origin, 'mutdeadfont', FONT_WITH_CORS)),
    contentType: TYPES.html,
    policy: deadFontPolicy(controlPolicy),
  });

  const mint = await api.post('/api/v1/library/preview-token', {
    headers: { ...csrf, 'Content-Type': 'application/json' },
    data: {
      workspace_id: workspaceID,
      path: FIXTURE_DIR,
      scope: 'bundle',
      entry_path: F_INDEX,
    },
  });
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
// Oracles
// ─────────────────────────────────────────────────────────────────────────────

interface BundleReport {
  js_ran: boolean;
  css_applied: boolean;
  font_bundle_width: number;
  font_fallback_width: number;
  audio_ready_state: number;
  audio_duration: number;
  audio_error: number | null;
  origin_opaque: boolean | string;
  cookie: string;
  /** Comma-joined names of every probe site the inline script REACHED. */
  attempted: string;
  /**
   * Comma-joined names of the probes the API boundary refused SYNCHRONOUSLY —
   * `sendBeacon` returning false, or a constructor throwing.
   *
   * Deliberately a separate field from `attempted`. A vector refused here never
   * reaches the network, so folding it into "attempted and then blocked" would
   * credit the §10.3 policy for an engine's own refusal. It is engine-dependent
   * (measured: Firefox 153 refuses `beacon` and `ws` where Chromium 151 does
   * not), so it is never asserted as a fixed list under a policy — only its
   * ABSENCE is asserted, in the no-policy control.
   */
  refused: string;
}

/** Read the fixture's own report out of the frame (or the page, top-level). */
async function readReport(page: Page, frameSelector?: string): Promise<BundleReport> {
  const locator = frameSelector
    ? page.frameLocator(frameSelector).locator('#result')
    : page.locator('#result');
  await expect
    .poll(async () => ((await locator.textContent().catch(() => '')) || '').length, {
      timeout: 30_000,
      message: 'the preview never produced its report — it did not load, or its script did not run',
    })
    .toBeGreaterThan(0);
  return JSON.parse((await locator.textContent())!) as BundleReport;
}

/**
 * Did the bundle's own face render, or did the browser fall back?
 *
 * Differential, and deliberately not a pixel expectation. If the face failed to
 * load, `'E2EBundleFont', monospace` resolves to the SAME monospace face as the
 * control span, so the two widths are identical.
 */
function fontApplied(report: BundleReport): boolean {
  if (report.font_fallback_width <= 0) return false;
  const delta = Math.abs(report.font_bundle_width - report.font_fallback_width);
  return delta / report.font_fallback_width > FONT_APPLIED_MIN_DELTA;
}

function fontEvidence(report: BundleReport): string {
  return (
    `bundle-face width ${report.font_bundle_width}px vs monospace-fallback width ` +
    `${report.font_fallback_width}px (identical widths mean the face did not load)`
  );
}

/** Every vector recorded for one case tag since `from`, in arrival order. */
function arrived(caseTag: string, from: number): string[] {
  const prefix = `/x/${caseTag}/`;
  return sink.hits
    .slice(from)
    .filter((h) => h.startsWith(prefix))
    .map((h) => h.slice(prefix.length));
}

function mark(): number {
  return sink.hits.length;
}

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

async function settleThenRead(caseTag: string, from: number): Promise<string[]> {
  await new Promise((r) => setTimeout(r, SETTLE_MS));
  return arrived(caseTag, from);
}

// ─────────────────────────────────────────────────────────────────────────────
// Tests
// ─────────────────────────────────────────────────────────────────────────────

test.describe('ADR-067 tests 9 / 60 / 64 — a bundle through the token path', () => {
  /**
   * Test 64 — FR-003a, the requirement the original experiment could not see.
   *
   * §0 records it as DISPROVED-by-omission: *"A sandboxed preview can
   * authenticate — DISPROVED. The experiment harness was an unauthenticated
   * static server, which hid this."* A sandboxed document has an opaque origin,
   * so it can send neither the `SameSite=Strict` session cookie nor an
   * `Authorization` header on a `<link>` or `<script>`. That is why the bytes
   * come from a token-bearing path at all.
   *
   * Asserted from a FRESH request context carrying NO credentials whatsoever —
   * the closest thing to an opaque-origin document this level can construct:
   *
   *   the token path MUST serve the bundle without any credential; and
   *   that credential-free access MUST still be a real, bounded grant — a
   *     forged token is refused, and a live token cannot reach a file outside
   *     its own scope (§10.5: "one workspace, one path … never a whole
   *     workspace"; FR-003b).
   *
   * The second half is what stops the first from reading as "this path is open
   * to everyone". Its scope case is aimed at a file that DEMONSTRABLY EXISTS
   * and is readable by the authenticated caller in the same test — a 404 for a
   * path that was never there would prove nothing.
   *
   * ⚠️ ONE PROPERTY IS DELIBERATELY NOT ASSERTED HERE, AND THE REASON MATTERS.
   * FR-003a's premise is that `/api/v1/library/` is behind `withUploadAuth`, so
   * an unauthenticated caller cannot reach the bytes that way. That is NOT
   * measurable from this suite: the E2E rig — locally and in
   * `.github/workflows/pr.yml` alike — boots the gateway with
   * `gateway.dev_mode_bypass: true`, and `checkBearerAuth`
   * (pkg/gateway/auth.go) short-circuits to an authenticated dev user for ANY
   * request with no `Bearer ` prefix. Measured on this branch: an anonymous
   * `GET /api/v1/agents` returns 200 with `users_count: 1` in the AUTH-BYPASS
   * log line. Asserting the refusal here would be a permanent false red that
   * says nothing about the shipped default. That property belongs to a Go
   * integration test, which can boot a gateway with the flag off.
   */
  test('64 — the token path serves the bundle with NO credential, and that grant is bounded', async ({
    playwright,
  }) => {
    const anonymous = await playwright.request.newContext({ baseURL: BASE_URL });
    try {
      // (a) The requirement itself: no credential, and the bytes arrive.
      const viaToken = await anonymous.get(tokenURL(F_INDEX));
      expect(
        viaToken.status(),
        'FR-003a: a sandboxed document carries no cookie and no Authorization header, ' +
          'so the token path must answer without one',
      ).toBe(200);
      expect(
        await viaToken.text(),
        'the token path served something other than the bundle entry — every assertion below would be vacuous',
      ).toContain('preview bundle fixture');

      // (b) The token is a real credential: a well-formed forgery is refused.
      //     Same 43-character base64url shape (FR-003h), so this tests the
      //     lookup and not a syntax check.
      const forged = 'A'.repeat(43);
      expect(forged.length, 'the forgery must have the real token shape').toBe(previewToken.length);
      const viaForged = await anonymous.get(
        `/library-preview/${forged}/${FIXTURE_DIR}/${F_INDEX}`,
      );
      expect(
        viaForged.status(),
        'a forged preview token was accepted — the token path would then be an unauthenticated ' +
          'read of the whole Library',
      ).toBeGreaterThanOrEqual(400);

      // (c) The grant is SCOPED. The outside file exists and the authenticated
      //     caller can read it — asserted first, so the refusal below cannot be
      //     explained by the file being absent.
      const outsideRel = `${OUTSIDE_DIR}/${F_OUTSIDE}`;
      const outsideAuthed = await api.get(
        `/api/v1/library/${workspaceID}/download?path=${encodeURIComponent(outsideRel)}`,
      );
      expect(outsideAuthed.status(), 'the out-of-scope fixture must exist').toBe(200);
      expect(await outsideAuthed.text()).toContain(OUTSIDE_MARKER);

      const outsideViaToken = await anonymous.get(`/library-preview/${previewToken}/${outsideRel}`);
      expect(
        outsideViaToken.status(),
        `a live preview token reached ${outsideRel}, outside its own scope root ` +
          `(${FIXTURE_DIR}) — §10.5 requires one workspace and one path, never a whole workspace`,
      ).toBeGreaterThanOrEqual(400);
      expect(
        await outsideViaToken.text(),
        'the out-of-scope response carried the file\'s contents',
      ).not.toContain(OUTSIDE_MARKER);
    } finally {
      await anonymous.dispose();
    }
  });

  /**
   * Test 9's wire half, and FR-015c's whole point.
   *
   * Every subresource class in the bundle, checked on the response the browser
   * will actually receive: the extension-derived type (FR-015/FR-015a/FR-015b),
   * `nosniff` on everything, INLINE disposition for the §10.4 allow-list, and
   * the §10.3 policy byte for byte on every one of them (FR-008b, MV-13).
   *
   * `.css` and `.js` are here by name because omitting them WAS the round-4
   * defect: inline-allowed with no type means `application/octet-stream` plus
   * mandatory `nosniff`, and every browser then refuses a bundle's own
   * stylesheet and script.
   *
   * `Access-Control-Allow-Origin` is asserted PRESENT on the font and ABSENT on
   * everything else. Both halves matter: FR-019 requires it for fonts, and a
   * blanket `*` on every preview response would be a real widening that no
   * other test in this suite would notice.
   */
  test('9 (wire) — every bundle subresource carries its extension-derived type, nosniff, inline and the §10.3 policy', async () => {
    const cases = [
      { file: F_INDEX, type: TYPES.html, cors: false },
      { file: F_CSS, type: TYPES.css, cors: false },
      { file: F_JS, type: TYPES.js, cors: false },
      { file: F_FONT, type: TYPES.woff2, cors: true },
      { file: F_WAV, type: TYPES.wav, cors: false },
    ] as const;

    for (const c of cases) {
      const res = await api.get(tokenURL(c.file));
      expect(res.status(), `GET ${c.file}`).toBe(200);
      const h = res.headers();
      expect(
        h['content-type'],
        `${c.file}: FR-015c — the type comes from the compiled-in table keyed by extension. ` +
          'application/octet-stream here means the extension is missing from it, and every browser ' +
          'then refuses the resource under nosniff.',
      ).toBe(c.type);
      expect(h['x-content-type-options'], `${c.file}: nosniff`).toBe('nosniff');
      expect(
        h['content-disposition'],
        `${c.file}: §10.4 puts this extension on the inline allow-list`,
      ).toMatch(/^inline/);
      expect(
        h['content-security-policy'],
        `${c.file}: FR-008b/MV-13 — every inline response carries the §10.3 policy, byte for byte`,
      ).toBe(ISOLATION_POLICY);

      if (c.cors) {
        expect(
          h['access-control-allow-origin'],
          `${c.file}: FR-019 — a webfont is fetched by a document whose origin is OPAQUE, so the ` +
            'request is cross-origin and the browser discards the response without this header',
        ).toBe('*');
      } else {
        expect(
          h['access-control-allow-origin'],
          `${c.file}: FR-019 scopes this header to webfonts. A blanket * on every preview response ` +
            'would be a real widening, and nothing else in this suite would notice it',
        ).toBeUndefined();
      }
      // NON-VACUITY: the response really carries the fixture's bytes, so the
      // header assertions above are about the file under test.
      expect((await res.body()).length, `${c.file}: served an empty body`).toBeGreaterThan(0);
    }
  });

  /**
   * Tests 9 and 60 and the containment half — ONE observation of ONE loaded
   * preview, embedded exactly as §10.6 and FR-005b require.
   *
   * They are asserted TOGETHER on purpose, and the reason is written into
   * preview-isolation.spec.ts's history: a preview that renders NOTHING
   * satisfies every "nothing escaped" assertion perfectly, and that is exactly
   * how a real WebKit rendering defect shipped for four days behind a fully
   * green isolation suite. Any future change that buys one of these properties
   * with another turns this test red on the engine where it happens, and the
   * message names which half went.
   *
   *   RENDERED (US-1 AS-4, FR-004, FR-015c, FR-019)
   *     external stylesheet applied, external script ran, the bundle's own
   *     webfont rendered (by WIDTH), and the audio container was parsed.
   *   CONTAINED (FR-005, FR-006)
   *     the origin is opaque, `document.cookie` THREW rather than returning
   *     empty, and NONE of the five egress vectors reached the second origin.
   */
  test('9 + 60 — css, js, webfont and audio all load through the token path, and nothing escapes', async ({
    page,
  }) => {
    const from = mark();

    // The embedder is a real document on the gateway origin, which is where a
    // preview pane lives; §10.7 gives the SPA `frame-src 'self'`, so framing the
    // token path is permitted, as it must be for the product to work.
    await page.goto('/');
    await embedPreview(page, tokenHref(F_INDEX));

    const report = await readReport(page, '#e2e-preview-frame');

    // ── NON-VACUITY FIRST, read from the frame's own DOM ─────────────────────
    // Not from the network: WebKit surfaces none of a sandboxed frame's requests
    // to the driver, so a request-count oracle is blind there and every negative
    // below would be automatically true.
    expect(
      report.attempted.split(','),
      'the fixture did not fire all five probes — everything below would be vacuous',
    ).toHaveLength(VECTORS.length);

    // ── RENDERED ─────────────────────────────────────────────────────────────
    expect(
      report.css_applied,
      "US-1 AS-4: the bundle's own external stylesheet MUST apply (text/css from the §10.4 table)",
    ).toBe(true);
    expect(
      report.js_ran,
      "FR-004: the bundle's own external script MUST run (text/javascript from the §10.4 table)",
    ).toBe(true);
    expect(
      fontApplied(report),
      `AC-15.1 / FR-019: the bundle's own webfont MUST render. ${fontEvidence(report)}. ` +
        'Most likely causes: the font response lost Access-Control-Allow-Origin, or .woff2 lost its ' +
        'entry in the compiled-in type table. NOTE document.fonts.status is deliberately NOT ' +
        'consulted — it reports "loaded" on failure (§0, experiment §6A.2).',
    ).toBe(true);
    expect(
      report.audio_error,
      `MV-14 / FR-015c: the audio element reported a media error (code ${report.audio_error}). ` +
        'application/octet-stream under nosniff produces exactly this.',
    ).toBeNull();
    expect(
      report.audio_ready_state,
      'the browser must have parsed the audio container (readyState >= HAVE_METADATA)',
    ).toBeGreaterThanOrEqual(1);
    expect(
      report.audio_duration,
      'a parsed WAV has a real duration; NaN or 0 means the container was never read',
    ).toBeGreaterThan(0);

    // ── CONTAINED, the SAME frame, the SAME load ─────────────────────────────
    // The full settle budget is spent BEFORE reading: there is no event for "a
    // request that will never arrive", so the only honest way to assert absence
    // is to wait. The positive control waits the same budget and must SEE its
    // traffic inside it, which is what stops this number being too small.
    const got = await settleThenRead('bundle', from);
    expect(
      got,
      `a previewed bundle reached the second origin: ${got.join(', ')}. ` +
        `Vectors this engine refused at the API boundary (i.e. never reached the network, and are ` +
        `therefore NOT evidence for the policy): [${report.refused || 'none'}].`,
    ).toEqual([]);
    expect(
      report.origin_opaque,
      'FR-005: the previewed document must be bound to an opaque origin',
    ).toBe(true);
    expect(
      report.cookie,
      'document.cookie must THROW, not return empty — an empty string also comes back from a page that failed to load',
    ).toMatch(/^THREW:/);
  });

  /**
   * The egress positive control.
   *
   * Without it, "nothing reached the second origin" above is indistinguishable
   * from "the probes never fired" or "the sink is unreachable". Same fixture
   * bytes, same probes, same sink — served from an origin with no policy at all.
   *
   * Only the three unconditional vectors are required to arrive; see
   * CONTROL_VECTORS for why that is enough for an assertion whose content is
   * "the list is EMPTY".
   */
  test('9 + 60 positive control — the same page with no policy DOES reach the second origin', async ({
    page,
  }) => {
    const from = mark();
    await page.goto(`${control.origin}/no-policy.html`);
    const report = await readReport(page);

    const got = await waitForVectors('ctrl', from, CONTROL_VECTORS);
    expect(
      got,
      'the probes or the sink are broken — an un-policed page must leak, or every "reached nothing" ' +
        'assertion in this file is vacuous',
    ).toEqual(expect.arrayContaining([...CONTROL_VECTORS]));

    // The API BOUNDARY is open with no policy — and this is what makes the
    // `refused` field a signal rather than noise.
    //
    // Measured on Firefox 153 under the §10.3 policy: `navigator.sendBeacon`
    // returns false and `new WebSocket(...)` throws, so those two vectors are
    // stopped before any request exists. That is a STRONGER outcome than a
    // wire-level block — but only if the same two calls succeed when the policy
    // is gone. If they were refused here too, the refusal would be a property of
    // this fixture (a bad URL, a missing API) rather than a difference the
    // policy made, and the policied run's empty hit list would prove less than
    // it appears to.
    expect(
      report.refused,
      'with NO policy the beacon API must accept the call — otherwise a refusal under the policy ' +
        'is not attributable to the policy',
    ).not.toContain('beacon');
    expect(
      report.refused,
      'with NO policy the WebSocket constructor must not throw — same reason',
    ).not.toContain('ws');

    // The cookie channel is real too, so "the cookie THREW" above is a
    // difference the policy made rather than a property of this fixture.
    expect(report.cookie ?? '').toContain('omnipus_probe=SECRET');
    expect(report.origin_opaque, 'with no sandbox directive the origin is NOT opaque').toBe(false);
  });

  /**
   * ═══════════════════════════════════════════════════════════════════════
   * TEST 60's PROOF OF FAILURE — the CORS mutation, over real sockets.
   * ═══════════════════════════════════════════════════════════════════════
   *
   * The claim "the webfont applied" is worth nothing until the width oracle has
   * been seen to report that it did NOT. This is that measurement, and it is
   * simultaneously the measurement FR-019 rests on.
   *
   * ⚠️ `page.route` CANNOT BE USED FOR THIS, AND THE REASON IS MEASURED. The
   * obvious mechanism — intercept the gateway's own font response and delete
   * the header — was written first and is WRONG. Chromium does not apply the
   * CORS check to a response delivered by `route.fulfill()`: on this branch,
   * 2026-09-10, a font fulfilled with `Access-Control-Allow-Origin` deleted
   * rendered anyway, in a cold context, in an opaque-origin document, with the
   * interception counter confirming the route had fired. A mutation harness
   * that silently suppresses the very check it is trying to provoke would have
   * "proved" the header unnecessary. preview-svg.spec.ts's header records the
   * same class of `fulfill()` artefact for a different subresource; this is a
   * second instance of it, and the same conclusion follows: measure over a real
   * socket.
   *
   * So the pair below is ONE experiment run twice, on the SAME local origin,
   * over real sockets: the SAME entry document, the SAME font BYTES, under the
   * SAME §10.3 policy — mechanically repointed at that origin so `font-src`
   * names it and the `sandbox` directive still makes the document's origin
   * opaque. They differ in exactly one header on the font response.
   *
   * Expected, from experiment §6A.2's measured `active` row:
   *   with the header    → the face renders (widths differ)
   *   without the header → the browser discards the response and falls back
   *                        (widths identical)
   *
   * §0 records that row as measured on CHROMIUM ONLY — *"Not measured on
   * Firefox or WebKit — those runs were stopped."* This file runs on all three
   * engines, which is where that gap closes.
   *
   * WHAT THIS PAIR DOES AND DOES NOT COVER. It establishes that CORS is what
   * gates a webfont in an opaque-origin document, and that the width oracle can
   * report FALSE. That the GATEWAY sends the header is asserted directly on the
   * live response in test 9 (wire), and that the product's own font renders
   * through the real token path is asserted in test 9 + 60. The three together
   * are the chain; no link is assumed.
   */
  test('60 mutation — the derived policies must genuinely differ before either mutation is believed', () => {
    // A derivation that silently produced the original string would turn both
    // mutations below into tests that assert nothing, and they would still be
    // green. If §10.3 ever reached a shape with no origin-bearing source at all,
    // the rewrite would be the identity function, the mutant pages would carry a
    // policy naming the GATEWAY's origin, and the font would fail on EVERY half
    // for a reason that has nothing to do with the thing under test.
    const controlPolicy = repointPolicyTo(ISOLATION_POLICY, control.origin);
    const deadPolicy = deadFontPolicy(controlPolicy);

    expect(
      ISOLATION_POLICY,
      'the shipped policy must carry the sandbox directive — without it the mutant document has a ' +
        'normal origin, the font request is same-origin, and CORS never enters into it',
    ).toContain('sandbox allow-scripts');
    expect(controlPolicy, 'the repointed policy must keep the sandbox directive').toContain(
      'sandbox allow-scripts',
    );
    expect(
      controlPolicy,
      'repointing must CHANGE the policy — otherwise the mutant pages are served a policy that ' +
        'names the gateway rather than the origin serving them, and every half fails for the wrong reason',
    ).not.toBe(ISOLATION_POLICY);
    expect(controlPolicy, 'the repointed policy must name the control origin').toContain(
      control.origin,
    );
    expect(
      controlPolicy,
      "font-src must name the origin actually serving the font, or the CORS half is a CSP " +
        "refusal wearing a CORS refusal's clothes",
    ).toMatch(new RegExp(`font-src[^;]*${control.origin.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}`));

    // …and the CSP mutation must genuinely move font-src, while touching
    // nothing else. Both halves matter: a rewrite that changed more than
    // font-src would stop being attributable to the font.
    expect(deadPolicy, 'the CSP mutation must move font-src').not.toBe(controlPolicy);
    expect(deadPolicy, 'font-src must now name only the dead origin').toMatch(
      new RegExp(`font-src ${DEAD_ORIGIN.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(;|$)`),
    );
    expect(
      deadPolicy.replace(/font-src[^;]*/, ''),
      'the CSP mutation must change font-src and NOTHING else',
    ).toBe(controlPolicy.replace(/font-src[^;]*/, ''));
  });

  /**
   * MUTATION A — CSP. The width oracle's proof of failure, on EVERY engine.
   *
   * The font is served perfectly here — correct type, correct bytes,
   * `Access-Control-Allow-Origin` present — and the POLICY refuses it, because
   * `font-src` names an origin the font does not come from. Measured
   * 2026-09-10: enforced by Chromium 151, Firefox 153 AND WebKit 26.5.
   *
   * That universality is the whole reason this test exists alongside MUTATION B.
   * The CORS mutation cannot serve as the oracle's proof of failure on WebKit,
   * which does not enforce font CORS at all (see
   * FONT_CORS_ENFORCED_BY_ENGINE) — so without this test, the WebKit run of this
   * file would assert "the font rendered" with no evidence that the oracle can
   * ever report otherwise. That is precisely the vacuity every other control in
   * this suite exists to prevent, and one engine quietly exempt from it is how
   * it would come back.
   */
  test('60 mutation A — CSP: with font-src repointed at a dead origin the face stops rendering', async ({
    page,
  }) => {
    // Baseline first, so "it did not render" below is a difference and not a
    // standing condition.
    await page.goto(`${control.origin}/policy-cors.html`);
    const baseline = await readReport(page);
    expect(
      fontApplied(baseline),
      `baseline: under the unmutated policy the face MUST render. ${fontEvidence(baseline)}`,
    ).toBe(true);

    const freshContext = await page.context().browser()!.newContext();
    try {
      const freshPage = await freshContext.newPage();
      await freshPage.goto(`${control.origin}/policy-deadfont.html`);
      const deadFont = await readReport(freshPage);

      // NON-VACUITY: the document loaded, ran, and everything EXCEPT the font is
      // unchanged. Without these, "the font did not apply" is indistinguishable
      // from "nothing loaded".
      expect(
        deadFont.origin_opaque,
        'the mutant document must have the same opaque origin as the baseline',
      ).toBe(true);
      expect(
        deadFont.attempted.split(','),
        'the document must still load and run under the mutated policy',
      ).toHaveLength(VECTORS.length);
      expect(
        deadFont.css_applied,
        'the stylesheet must still apply — this mutation moves font-src and nothing else',
      ).toBe(true);
      expect(deadFont.js_ran, 'the external script must still run').toBe(true);

      // THE MUTATION.
      expect(
        fontApplied(deadFont),
        `with font-src pointed at ${DEAD_ORIGIN} the face MUST NOT render. ` +
          `${fontEvidence(deadFont)}. If it rendered, the rendered-width oracle cannot report ` +
          'failure on this engine, and every "the font applied" assertion in this file is vacuous here.',
      ).toBe(false);
      expect(
        fontApplied(baseline) === fontApplied(deadFont),
        'the mutation changed nothing — one directive was supposed to decide this outcome',
      ).toBe(false);
    } finally {
      await freshContext.close();
    }
  });

  /**
   * MUTATION B — CORS. FR-019's own mechanism, measured per engine.
   *
   * The SAME entry document and the SAME font BYTES from the SAME origin under
   * the SAME policy, differing in exactly one header on the font response. This
   * is the experiment §6A.2 row that §0 records as measured on CHROMIUM ONLY;
   * running it on all three engines is where that gap closes, and closing it
   * produced a result the spec does not currently reflect — see
   * FONT_CORS_ENFORCED_BY_ENGINE for the measurement and its consequences.
   *
   * THE EXPECTATION IS READ FROM THAT TABLE AND ASSERTED IN BOTH DIRECTIONS.
   * On Chromium and Firefox the face must NOT render without the header; on
   * WebKit it must render, because WebKit does not enforce font CORS at all.
   * Asserting the WebKit row rather than skipping it is deliberate: a future
   * WebKit release that starts enforcing turns this red with a message saying
   * exactly that, instead of leaving a stale claim in a comment.
   *
   * ⚠️ `page.route` CANNOT BE USED FOR THIS, AND THE REASON IS MEASURED. The
   * obvious mechanism — intercept the gateway's own font response and delete
   * the header — was written first and is WRONG. Chromium does not apply the
   * CORS check to a response delivered by `route.fulfill()`: on 2026-09-10 a
   * font fulfilled with the header deleted rendered anyway, in a cold context,
   * in an opaque-origin document, with the interception counter confirming the
   * route had fired. A mutation harness that silently suppresses the very check
   * it is trying to provoke would have "proved" the header unnecessary — on the
   * one engine that enforces it most strictly. preview-svg.spec.ts's header
   * records the same class of `fulfill()` artefact for a different subresource;
   * the same conclusion follows: measure over a real socket.
   */
  test('60 mutation B — CORS: whether Access-Control-Allow-Origin decides the face is engine-specific, and measured', async ({
    page,
    browserName,
  }) => {
    const enforces = FONT_CORS_ENFORCED_BY_ENGINE[browserName];
    expect(
      enforces,
      `no measured CORS row for browser "${browserName}". Add it to ` +
        'FONT_CORS_ENFORCED_BY_ENGINE by MEASURING it — never by guessing, which is how §0 ended ' +
        'up with a Chromium-only result read as a universal fact.',
    ).toBeDefined();

    // ── Half one: the header present. The face must render on every engine. ──
    await page.goto(`${control.origin}/policy-cors.html`);
    const withHeader = await readReport(page);
    expect(
      withHeader.origin_opaque,
      'the mutant document must have an OPAQUE origin, or its font request is same-origin and ' +
        'this measures nothing about CORS',
    ).toBe(true);
    expect(
      withHeader.attempted.split(','),
      'the mutant document must load and run for this half to mean anything',
    ).toHaveLength(VECTORS.length);
    expect(
      fontApplied(withHeader),
      'baseline half: with Access-Control-Allow-Origin present the font MUST render on every ' +
        `engine. ${fontEvidence(withHeader)}`,
    ).toBe(true);

    // ── Half two: the SAME bytes, one header removed. ────────────────────────
    // A fresh context, because a webfont fetched once stays in the HTTP cache
    // with its CORS decision attached, and a second navigation in the same
    // context would re-use it — reporting "the font still applied" whatever the
    // second response said. (Measured: that is exactly what the earlier
    // `page.route` version of this test did.) The two font responses have
    // different URLs, so this is belt and braces rather than the only guard.
    const freshContext = await page.context().browser()!.newContext();
    try {
      const freshPage = await freshContext.newPage();
      await freshPage.goto(`${control.origin}/policy-nocors.html`);
      const withoutHeader = await readReport(freshPage);

      // NON-VACUITY, four ways: the document loaded, ran, kept its opaque
      // origin, and everything about it EXCEPT the font is unchanged.
      expect(
        withoutHeader.origin_opaque,
        'the negative half must have the same opaque origin as the baseline',
      ).toBe(true);
      expect(
        withoutHeader.attempted.split(','),
        'the document must still load and run with the header absent',
      ).toHaveLength(VECTORS.length);
      expect(
        withoutHeader.css_applied,
        'the stylesheet must still apply — a stylesheet is a no-cors request, so CORS does not gate ' +
          'it, and this is what proves the mutation changed the FONT and nothing else',
      ).toBe(true);
      expect(
        withoutHeader.js_ran,
        'the external script must still run — also a no-cors request, and also unaffected',
      ).toBe(true);

      // THE MEASURED EXPECTATION, asserted in whichever direction the table says.
      if (enforces) {
        expect(
          fontApplied(withoutHeader),
          `FR-019: ${browserName} enforces CORS on webfonts, so with Access-Control-Allow-Origin ` +
            `absent the face MUST NOT render. ${fontEvidence(withoutHeader)}. If it DID render, ` +
            `then ${browserName} has stopped enforcing font CORS — re-measure and update ` +
            'FONT_CORS_ENFORCED_BY_ENGINE. That is a finding, not a flake.',
        ).toBe(false);
        expect(
          fontApplied(withHeader) === fontApplied(withoutHeader),
          'the mutation changed nothing — one header was supposed to decide this outcome',
        ).toBe(false);
      } else {
        expect(
          fontApplied(withoutHeader),
          `${browserName} was measured on 2026-09-10 as NOT enforcing CORS on webfonts — the face ` +
            `renders with no Access-Control-Allow-Origin at all, in an opaque-origin document AND ` +
            `in an ordinary cross-origin one. ${fontEvidence(withoutHeader)}. This assertion ` +
            'reports the MEASURED behaviour, so if it now fails, this engine has STARTED enforcing: ' +
            'that is good news, and the fix is to flip its row in FONT_CORS_ENFORCED_BY_ENGINE. ' +
            'It is asserted rather than skipped so the table cannot rot into folklore — which is ' +
            "how §0's Chromium-only measurement came to read as a universal browser fact.",
        ).toBe(true);
      }
    } finally {
      await freshContext.close();
    }
  });
});
