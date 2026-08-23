/**
 * preview-isolation.spec.ts — ADR-067 Stage 1, unit D1: the seven egress vectors.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHY THIS FILE HAS NO RETRIES, AND MUST NEVER GET ANY
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * The three `isolation-*` projects in playwright.config.ts pin `retries: 0` at
 * PROJECT level, which is what these tests run under (SC-012, §13.4). That is
 * not a stylistic choice and it is not negotiable.
 *
 * "Nothing reached the external origin" and "the cookie was not readable" are
 * not properties a fourth attempt establishes. A security assertion allowed to
 * retry reports identically to one that passed first time, so a policy
 * regression failing two runs in three would ship as green. The global
 * `retries: process.env.CI ? 3 : 2` was added for real-LLM latency variance
 * under prolonged suite load; nobody weighing that trade was considering these
 * tests. If a test here is flaky, the flake is the finding.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHAT IS BEING PROVED, AND WITH WHICH ORACLE
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * Requirements: FR-004, FR-005, FR-005a, FR-005b, FR-006, FR-006a, FR-006b.
 * Spec tests: 11 (`E2E_PreviewIsolation_NetworkBlocked`, as 11a/11b),
 * 110 (`E2E_PreviewSameOrigin_ReachableButUnauthenticated`),
 * 111 (`E2E_PreviewCannotFrameTheSpa`), and — because the isolation projects
 * run all three engines — the browser-matrix half of 12.
 *
 * ⚠️ 11c IS EXPECTED RED ON WEBKIT, and that is a finding, not a flake. Under
 * WebKit, adding FR-005b's `sandbox="allow-scripts"` ATTRIBUTE on top of the
 * §10.3 sandbox DIRECTIVE stops `script-src 'self'` / `style-src 'self'`
 * matching the serving origin, so a bundle's own external script and stylesheet
 * do not load. Containment is unaffected — 11a and 11b pass there — but US-1
 * AS-4 does not hold. Full measurement table in 11c's own comment.
 *
 * GROUND TRUTH IS A SECOND ORIGIN'S OWN REQUEST LOG. A page that merely fails
 * to render proves nothing, and neither does a console message: the experiment
 * measured that violation wording differs on every engine (experiment §4.4), so
 * a string match silently stops matching on a new browser version. The suite
 * watches a real HTTP server standing in for the internet and asserts on what
 * arrived there.
 *
 * ⚠️ ONE OBSERVER IS ENGINE-DEPENDENT AND IT IS NOT THE ONE ABOVE. The second
 * origin sees everything on every engine. The BROWSER-side request log — the
 * only thing that can watch requests aimed at the gateway itself, which this
 * suite may not instrument — does not: measured 2026-08-23, WebKit reports NONE
 * of a sandboxed iframe's requests to the driver, while reporting the same page
 * loaded top-level normally. So every negative about gateway-bound traffic is
 * asserted TOP-LEVEL (test 110), and every non-vacuity check on an embedded
 * preview is read out of the frame's own DOM (tests 11a, 111) rather than from
 * the network. A negative asserted through a blind observer is not a negative.
 *
 * THE DOCUMENT IS EMBEDDED THE WAY THE PRODUCT EMBEDS IT (§10.6, FR-005b):
 * `<iframe src="<token URL>">` with `sandbox="allow-scripts"`,
 * `referrerpolicy="no-referrer"` and an empty `allow=""`. Never `srcdoc` —
 * srcdoc resolves relative URLs against the embedder, so no bundle subresource
 * would load, and it has no response to carry the §10.3 policy at all.
 *
 * BOTH LOAD MODES RUN, and the distinction matters more than it looks. With a
 * `sandbox` ATTRIBUTE on the frame and a `sandbox` DIRECTIVE in the header, the
 * effective sandbox is the INTERSECTION (§10.6): embedded, the attribute alone
 * would keep blocking popups even if the header directive were deleted. Only
 * the TOP-LEVEL case — the tab a user opens on the preview URL, H-6's second
 * half — measures the response header on its own. So the top-level test is the
 * one that turns red when the sandbox half is dropped, and it is here for that
 * reason and not for completeness.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * EVERY ASSERTION HERE HAS BEEN SEEN TO FAIL
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * The last four tests in this file are the proof. They serve the SAME fixture
 * bytes through the SAME assertion function, from a local origin that applies a
 * DELIBERATELY BROKEN policy derived from the live shipped one:
 *
 *   • no policy at all      → all seven vectors must arrive. If this control
 *                             ever passes with fewer, the harness is broken and
 *                             no other row in this file means anything.
 *   • sandbox half deleted  → `window.open` must arrive, and the cookie must be
 *                             READABLE. No CSP directive covers popup
 *                             navigation — measured on all three engines.
 *   • source half deleted   → five of seven must arrive (image, fetch, beacon,
 *                             WebSocket, iframe).
 *
 * Each of those asserts that `assertNoEgress` — the exact function guarding the
 * product tests above — THROWS. The expected vector lists are read off the
 * experiment's measured table (adr-067-preview-isolation-experiment-2026-08-22
 * §1), never off the implementation.
 *
 * The mutants are served locally rather than by editing pkg/gateway, for two
 * reasons: this suite does not own that package, and a test that edits the
 * thing it is testing proves nothing about what ships.
 */
import { test, expect, type BrowserContext, type Page } from '@playwright/test';
import fs from 'node:fs';
import path from 'node:path';
import {
  BUNDLE_DIR_NAME,
  EGRESS_VECTORS,
  type EgressVectorName,
  type MutantOrigin,
  type RecordedHit,
  type RecordingOrigin,
  embedPreview,
  installBundle,
  mintPreviewToken,
  mutantPolicies,
  recordBrowserRequests,
  startExternalOrigin,
  startMutantOrigin,
  vectorsReaching,
  workspaceWorkRoot,
} from './fixtures/preview-isolation/harness.js';

/**
 * How many probes the fixture fires before the form vector.
 *
 * Five same-origin (img, api-img, fetch, beacon, ws) + six cross-origin (img,
 * fetch, beacon, ws, iframe, popup); the form makes twelve.
 *
 * This count is the NON-VACUITY oracle, and something has to play that role:
 * without it, "nothing reached the second origin" is equally true of a page
 * whose script never ran — the single most likely way this file could go
 * quietly green with the control gone.
 *
 * It is read TWO ways, because neither works everywhere. Top-level, the
 * fixture's same-origin `probe-done` ping is visible in the driver's request
 * log on all three engines. Embedded, WebKit reports no request from a
 * sandboxed frame at all, so the fixture's own `attempted` list — read out of
 * the frame's DOM, which Playwright reaches through the browser protocol
 * rather than through same-origin script access — is used instead.
 */
const PROBES_BEFORE_FORM = 11;

/**
 * Fixed settle window before asserting a NEGATIVE.
 *
 * There is no event for "a request that will never arrive", so the only honest
 * way to assert absence is to wait. Everything here is loopback, and the
 * positive controls in this same file demonstrate that arrivals land well
 * inside this window.
 */
const EGRESS_SETTLE_MS = 2_000;

/** Throws, loudly and with the evidence, if anything reached the second origin. */
function assertNoEgress(hits: RecordedHit[], where: string): void {
  const reached = vectorsReaching(hits);
  if (reached.length > 0) {
    throw new Error(
      `egress reached the second origin from ${where}: [${reached.join(', ')}]\n` +
      `arrived paths: ${hits.map((h) => `${h.kind}:${h.path}`).join(' | ')}`,
    );
  }
}

function omnipusHome(): string {
  const home = process.env.OMNIPUS_HOME
    || (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '');
  if (!home || !fs.existsSync(home)) {
    throw new Error(
      `[preview-isolation] OMNIPUS_HOME does not exist: ${home || '(unset)'}. ` +
      'This suite writes its hostile fixture into a workspace work tree on disk, ' +
      'so it must run against a gateway on this machine.',
    );
  }
  return home;
}

test.describe('ADR-067 preview isolation — seven egress vectors, retries: 0', () => {
  let ext: RecordingOrigin;
  let mutant: MutantOrigin;
  let workspaceID: string;
  let shippedPolicy: string;
  let spaEntryChunk: string;

  test.beforeAll(async ({ playwright, baseURL }) => {
    ext = await startExternalOrigin();

    const api = await playwright.request.newContext({
      baseURL,
      storageState: process.env.OMNIPUS_AUTH_FILE || './tests/e2e/fixtures/.auth/admin.json',
    });

    // The workspace whose work tree receives the hostile bundle. The list is
    // returned default-first (rest_workspaces.go's sort), so index 0 is the
    // workspace a fresh install actually previews from.
    const wsResponse = await api.get('/api/v1/workspaces');
    expect(wsResponse.ok(), `GET /api/v1/workspaces → ${wsResponse.status()}`).toBeTruthy();
    const workspaces = await wsResponse.json() as Array<{ id: string }>;
    expect(workspaces.length, 'no workspaces exist — cannot preview anything').toBeGreaterThan(0);
    workspaceID = workspaces[0].id;

    const workRoot = workspaceWorkRoot(omnipusHome(), workspaceID);
    fs.mkdirSync(workRoot, { recursive: true });
    installBundle(workRoot, ext.port);

    // The SPA's entry chunk, read out of the shell the gateway is actually
    // serving. Test 111's oracle is "this exact URL was never requested", and
    // a hardcoded or glob-matched name would stop matching on the next build
    // and pass forever.
    const shell = await api.get('/');
    expect(shell.ok(), `GET / → ${shell.status()}`).toBeTruthy();
    const shellHTML = await shell.text();
    const entry = /\/assets\/index-[A-Za-z0-9_-]+\.js/.exec(shellHTML);
    expect(entry, 'no /assets/index-*.js in the SPA shell — is a real SPA embedded?').not.toBeNull();
    spaEntryChunk = entry![0];

    await api.dispose();
  });

  test.afterAll(async () => {
    await ext?.close();
    await mutant?.close();
  });

  test.beforeEach(() => {
    ext.reset();
    mutant?.reset();
  });

  /**
   * Mint a token for the hostile bundle and hand back its serving URL.
   *
   * Minting goes through the real authenticated endpoint on purpose (FR-003b):
   * a token that never widened access is part of what makes the rest of this
   * meaningful, and a hand-forged URL would test a route nobody can reach.
   */
  async function tokenURLFor(page: Page, entry: string): Promise<string> {
    const minted = await mintPreviewToken(page, workspaceID, BUNDLE_DIR_NAME, 'bundle', entry);
    expect(minted.token, 'token must be the 43-char base64url shape (FR-003h)').toHaveLength(43);
    return minted.url;
  }

  /**
   * The fixture's own self-report, read out of the frame.
   *
   * Corroborating evidence, never the egress oracle. It exists for the three
   * things a server log cannot see — whether the external script ran, whether
   * the stylesheet applied, and whether `document.cookie` THREW rather than
   * returned empty — and for the `attempted` list, which is the only
   * non-vacuity oracle that works on every engine (WebKit hides a sandboxed
   * frame's network activity from the driver entirely).
   */
  async function readPreviewReport(
    page: Page,
    frameSelector?: string,
  ): Promise<{ js_ran: boolean; css_applied: boolean; origin_opaque: boolean; cookie: string; attempted: string }> {
    const locator = frameSelector
      ? page.frameLocator(frameSelector).locator('#result')
      : page.locator('#result');
    const raw = await locator.textContent({ timeout: 20_000 });
    expect(raw, 'the preview never produced its report — it did not load or did not run').toBeTruthy();
    return JSON.parse(raw!) as {
      js_ran: boolean; css_applied: boolean; origin_opaque: boolean; cookie: string; attempted: string;
    };
  }

  /** The gesture-driven popup attempt — see the fixture's note 3. */
  async function clickInsidePreview(page: Page, frameSelector?: string): Promise<void> {
    try {
      const target = frameSelector
        ? page.frameLocator(frameSelector).locator('#gesture')
        : page.locator('#gesture');
      await target.click({ timeout: 5_000 });
    } catch {
      // A refused click is not a finding: the load-time popup attempt still
      // stands, and the positive controls below prove the vector is observable.
    }
  }

  // ───────────────────────────────────────────────────────────────────────────
  // Spec test 11 — the product, embedded exactly as §10.6 requires
  // ───────────────────────────────────────────────────────────────────────────
  test('11a — embedded in the product-shaped iframe, zero of seven vectors reach the second origin', async ({ page }) => {
    const log = recordBrowserRequests(page.context());

    // The embedder is a real document on the gateway origin, which is where a
    // preview pane lives. Its own policy carries `frame-src 'self'`, so framing
    // the token path is permitted — as it must be for the product to work.
    await page.goto('/');
    const tokenURL = await tokenURLFor(page, 'index.html');
    await embedPreview(page, tokenURL);

    // NON-VACUITY FIRST, and read from the FRAME rather than from the network.
    //
    // A request-arrival oracle would be blind here on one engine: measured
    // 2026-08-23, WebKit surfaces NONE of a sandboxed iframe's requests to the
    // driver — not the document, not its subresources — while reporting the
    // same page's requests normally when it is loaded top-level. An assertion
    // of the form "no request was seen" is therefore automatically true there,
    // which is the exact false-green this file exists to avoid. The fixture's
    // own `attempted` list is visible on all three engines, because Playwright
    // reaches a frame through the browser protocol rather than through
    // same-origin script access.
    const report = await readPreviewReport(page, '#e2e-preview-frame');
    expect(report.attempted.split(','), 'all twelve probes must have been attempted')
      .toHaveLength(PROBES_BEFORE_FORM + 1);
    expect(report.attempted, 'including the form, which fires last').toContain('form');

    await clickInsidePreview(page, '#e2e-preview-frame');
    await page.waitForTimeout(EGRESS_SETTLE_MS);

    // The form was not merely attempted, it was REFUSED: an unblocked GET
    // submit navigates the document away, and this frame is still on its token
    // URL. That is the observable difference between "blocked" and "skipped".
    const previewFrame = page.frames().find((f) => f.url().includes(tokenURL));
    expect(previewFrame, 'the preview frame navigated away — the form submit was NOT blocked')
      .toBeTruthy();

    // GROUND TRUTH.
    assertNoEgress(ext.hits, 'the embedded preview');
    expect(vectorsReaching(ext.hits)).toEqual([]);

    // Corroborating only — an in-page self-report, never the egress oracle.
    // THREW is the assertion, not "empty": an empty string also comes back
    // from a page that failed to load (§12).
    expect(report.origin_opaque, 'FR-005: the document must be bound to an opaque origin').toBe(true);
    expect(report.cookie, 'document.cookie must THROW, not return empty').toMatch(/^THREW:/);
  });

  // ───────────────────────────────────────────────────────────────────────────
  // FR-004 — kept as its own test, on purpose. See the comment.
  // ───────────────────────────────────────────────────────────────────────────
  test('11c — FR-004: a previewed bundle runs its own script and applies its own stylesheet', async ({ page }) => {
    // WHY THIS IS NOT FOLDED INTO 11a. It is a RENDERING requirement, not a
    // containment one, and on one engine the two currently disagree. Measured
    // 2026-08-23 against this branch's gateway, one engine at a time:
    //
    //   engine    embedding                              js_ran  css_applied
    //   Chromium  top-level                                ✅       ✅
    //   Chromium  iframe + sandbox="allow-scripts"         ✅       ✅
    //   WebKit    top-level                                ✅       ✅
    //   WebKit    iframe + sandbox="allow-scripts"         ❌       ❌
    //
    // The INLINE script runs in every cell (the `attempted` list is complete in
    // all four), so the document loads and executes. What fails on WebKit is
    // the EXTERNAL `<script src>` and `<link rel=stylesheet>`, which are gated
    // by `script-src 'self'` / `style-src 'self'` — i.e. under WebKit, adding
    // FR-005b's sandbox ATTRIBUTE on top of the §10.3 sandbox DIRECTIVE stops
    // `'self'` matching the serving origin. Removing just the attribute makes
    // WebKit pass; the header alone is fine there.
    //
    // The experiment (§2.1) measured `'self'` matching under an opaque origin
    // on all three engines — but it measured the HEADER alone. The attribute is
    // FR-005b's addition and this composition was never measured. Containment
    // is unaffected either way (origin opaque, cookie throws, zero egress —
    // 11a and 11b pass on WebKit): what breaks is US-1 AS-4, the flagship
    // "a complete bundle loads all of its assets" scenario.
    //
    // Kept red rather than skipped, and separate rather than buried inside an
    // isolation test, so the failure names the real defect instead of reading
    // as an isolation regression.
    await page.goto('/');
    const tokenURL = await tokenURLFor(page, 'index.html');
    await embedPreview(page, tokenURL);

    const report = await readPreviewReport(page, '#e2e-preview-frame');
    expect(report.js_ran, "FR-004 / script-src 'self': the bundle's external script MUST execute").toBe(true);
    expect(report.css_applied, "style-src 'self': the bundle's external stylesheet MUST apply").toBe(true);
  });

  // ───────────────────────────────────────────────────────────────────────────
  // Spec test 11 — top level. The mode where the response header stands alone.
  // ───────────────────────────────────────────────────────────────────────────
  test('11b — opened as its own tab, zero of seven vectors reach the second origin', async ({ page }) => {
    const log = recordBrowserRequests(page.context());
    await page.goto('/');
    const tokenURL = await tokenURLFor(page, 'index.html');

    await page.goto(tokenURL);

    // Top level, the driver DOES see the page's requests on every engine, so
    // both oracles are available and both are used: the network ping proves
    // the probes reached the network stack, the in-page list proves all twelve
    // were attempted.
    await expect.poll(
      () => log.matching((r) => r.url.includes('phase=pre-form')).length,
      { message: 'the fixture never reported firing its probes', timeout: 20_000 },
    ).toBeGreaterThan(0);
    expect(log.matching((r) => r.url.includes('phase=pre-form'))[0].url)
      .toContain(`fired=${PROBES_BEFORE_FORM}`);

    await clickInsidePreview(page);
    await page.waitForTimeout(EGRESS_SETTLE_MS);

    const postForm = log.matching((r) => r.url.includes('phase=post-form'));
    expect(postForm.length, 'the form vector must have been attempted').toBeGreaterThan(0);
    expect(postForm[0].url).toContain(`fired=${PROBES_BEFORE_FORM + 1}`);

    assertNoEgress(ext.hits, 'the top-level preview tab');
    expect(vectorsReaching(ext.hits)).toEqual([]);
  });

  // ───────────────────────────────────────────────────────────────────────────
  // Spec test 110 — FR-006a: the accepted residual, and its stated condition
  // ───────────────────────────────────────────────────────────────────────────
  test('110 — same-origin subresources reach the gateway and carry NO session cookie', async ({ page }) => {
    // TOP LEVEL, DELIBERATELY, and for two independent reasons.
    //
    // FIRST, it is the harsher case and the only one with teeth. Embedded, the
    // frame's own `sandbox` ATTRIBUTE keeps the origin opaque even if the
    // header's sandbox directive were deleted, so an embedded-only version of
    // this test stays green through exactly the regression it exists to catch.
    // Measured 2026-08-23: with the sandbox directive removed from the shipped
    // policy the embedded form still passed and this form failed. Whatever
    // holds top-level holds embedded too — embedding only ever intersects the
    // sandbox further.
    //
    // SECOND, it is the only form that can be OBSERVED on all three engines.
    // WebKit surfaces none of a sandboxed iframe's requests to the driver
    // (measured 2026-08-23), so an embedded version could not read the Cookie
    // header there at all — and an unreadable header is not an absent one.
    const log = recordBrowserRequests(page.context());
    await page.goto('/');
    const tokenURL = await tokenURLFor(page, 'index.html');
    await page.goto(tokenURL);

    await expect.poll(
      () => log.matching((r) => r.url.includes('probe=same-api-img')).length,
      { message: 'the preview never issued its same-origin probes', timeout: 20_000 },
    ).toBeGreaterThan(0);
    await page.waitForTimeout(EGRESS_SETTLE_MS);
    await log.settle();

    // (a) The residual, documented rather than denied: an image subresource
    //     really does reach the gateway and is answered. FR-006a accepts this
    //     — the point of the requirement is the condition in (b), not a denial
    //     that the request happens.
    const bundleProbe = log.matching((r) => r.url.includes('probe=same-img'));
    expect(bundleProbe.length, 'a same-origin subresource load must still be possible').toBeGreaterThan(0);
    expect(bundleProbe[0].status, 'and must be answered by the gateway').toBe(200);

    // (b) The condition the whole accepted residual rests on. One edit to the
    //     session cookie's SameSite mode turns untrusted content into an
    //     authenticated API caller with no other symptom, which is why FR-006a
    //     says this MUST be asserted rather than assumed.
    const apiProbes = log.matching((r) => r.url.includes('probe=same-api-img'));
    expect(apiProbes.length).toBeGreaterThan(0);
    for (const probe of apiProbes) {
      // Refuse to conclude from a header set we never read. `cookie` is
      // undefined both when no cookie was attached and when the headers could
      // not be retrieved; only the first of those is evidence.
      expect(probe.headersRead, `headers for ${probe.url} were never read — this proves nothing`).toBe(true);
      expect(probe.cookie ?? '', `preview request ${probe.url} must carry no session cookie`)
        .not.toContain('omnipus-session');
    }

    // (c) FR-006: no fetch / XHR / sendBeacon / WebSocket to ANY origin, the
    //     gateway's own included. `connect-src 'none'` carries no 'self', so
    //     none of these three may be answered.
    const connectProbes = log.matching(
      (r) => r.url.includes('probe=same-fetch') || r.url.includes('probe=same-beacon') || r.url.includes('probe=same-ws'),
    );
    for (const probe of connectProbes) {
      expect(probe.status, `connect-src 'none' must block ${probe.url}`).toBeUndefined();
    }

    // (d) POSITIVE CONTROL, and without it (b) is worthless: "no Cookie header"
    //     is otherwise indistinguishable from "this oracle cannot see cookies
    //     at all". The same path, requested by the authenticated app, must
    //     carry one.
    //
    //     Run on a SECOND page in the same context rather than by navigating
    //     this one: the hostile document leaves a WebSocket attempt and a
    //     cross-origin <iframe> pending, and a `goto` away from it waits for
    //     those to settle and times out. Same context means the same cookie
    //     jar and the same request log, so the comparison still holds.
    const controlPage = await page.context().newPage();
    await controlPage.goto('/');
    await controlPage.evaluate(() => {
      const img = new Image();
      img.src = '/api/v1/state?probe=positive-control&v=' + Date.now();
    });
    await expect.poll(
      () => log.matching((r) => r.url.includes('probe=positive-control')).length,
      { timeout: 10_000 },
    ).toBeGreaterThan(0);
    await log.settle();
    const control = log.matching((r) => r.url.includes('probe=positive-control'))[0];
    expect(control.headersRead, 'positive control: its headers must have been read').toBe(true);
    expect(control.cookie ?? '', 'positive control: the authenticated app DOES send the session cookie')
      .toContain('omnipus-session');
    await controlPage.close();
  });

  // ───────────────────────────────────────────────────────────────────────────
  // Spec test 111 — FR-006b: the SPA refuses to be framed
  // ───────────────────────────────────────────────────────────────────────────
  test('111 — a previewed page may request the SPA shell, but the nested app never renders', async ({ page }) => {
    const log = recordBrowserRequests(page.context());
    await page.goto('/about');
    const tokenURL = await tokenURLFor(page, 'frame-spa.html');

    await log.settle();
    const entryChunkBefore = log.matching((r) => r.url.includes(spaEntryChunk)).length;

    await embedPreview(page, tokenURL);

    // NON-VACUITY: the fixture ran and appended its <iframe src="/">. Read from
    // the frame's own DOM, not from the network — WebKit surfaces none of a
    // sandboxed frame's requests to the driver (measured 2026-08-23), and an
    // assertion of the form "the nested app never requested X" is automatically
    // true when the observer cannot see any nested request at all.
    const marker = await page.frameLocator('#e2e-preview-frame').locator('#frame-added')
      .textContent({ timeout: 20_000 });
    expect(marker, 'the framing fixture never ran — everything below would be vacuous')
      .toBe('spa-frame-appended');
    await page.waitForTimeout(EGRESS_SETTLE_MS);
    await log.settle();

    // THE ORACLE, and it is a DOM one: no frame anywhere in this page holds a
    // rendered SPA. `frame-ancestors 'none'` does not stop the REQUEST — the
    // preview's own `frame-src 'self'` permits it and the shell is fetched and
    // answered — it stops the browser rendering the response it received. A
    // refused document never parses its own <script src>, so the app's entry
    // chunk is present in no frame's DOM.
    //
    // Console text is NOT the oracle: every engine words this refusal
    // differently (experiment §4.4).
    const framesHoldingTheApp: string[] = [];
    for (const frame of page.frames()) {
      if (frame === page.mainFrame()) continue;
      // BOUNDED. A frame whose document the browser refused can leave
      // `evaluate` pending forever rather than rejecting — measured
      // 2026-08-23 on WebKit, where the unbounded form hung the test to its
      // 90 s ceiling. A frame that cannot be evaluated has, by construction,
      // no rendered app in it; a frame that CAN be evaluated is checked.
      const hasEntry = await Promise.race([
        frame.evaluate(
          (chunk) => !!document.querySelector(`script[src*="${chunk}"]`),
          spaEntryChunk,
        ).catch(() => false),
        new Promise<boolean>((resolve) => { setTimeout(() => resolve(false), 3_000).unref?.(); }),
      ]);
      if (hasEntry) framesHoldingTheApp.push(frame.url() || '(opaque)');
    }
    expect(framesHoldingTheApp, 'the nested SPA rendered inside attacker-authored content').toEqual([]);

    // Belt and braces on the engines that DO report nested requests: the entry
    // chunk was never even fetched. Kept as a second, independent oracle rather
    // than the only one, precisely because it is silent on WebKit.
    const entryChunkAfter = log.matching((r) => r.url.includes(spaEntryChunk)).length;
    expect(entryChunkAfter - entryChunkBefore, `the nested SPA must never load ${spaEntryChunk}`).toBe(0);
  });

  test('111 positive control — a normally-rendered SPA IS visible to both oracles', async ({ browser }) => {
    // Without this, test 111 proves nothing: "no frame holds the app" and "the
    // entry chunk was never requested" are both trivially true of an oracle
    // that cannot see the app under any circumstances.
    //
    // A FRESH context, because `immutable` asset caching would otherwise let
    // the request half pass or fail for cache reasons rather than policy ones.
    const context: BrowserContext = await browser.newContext({
      storageState: process.env.OMNIPUS_AUTH_FILE || './tests/e2e/fixtures/.auth/admin.json',
    });
    const log = recordBrowserRequests(context);
    const fresh = await context.newPage();
    await fresh.goto('/about');

    await expect.poll(
      () => log.matching((r) => r.url.includes(spaEntryChunk)).length,
      { message: 'the entry-chunk REQUEST oracle matches nothing even for a normal load — it is blind', timeout: 20_000 },
    ).toBeGreaterThan(0);

    const domSeesTheApp = await fresh.evaluate(
      (chunk) => !!document.querySelector(`script[src*="${chunk}"]`),
      spaEntryChunk,
    );
    expect(domSeesTheApp, 'the entry-chunk DOM oracle cannot see the app even when it renders — it is blind')
      .toBe(true);

    await context.close();
  });

  // ═════════════════════════════════════════════════════════════════════════
  // PROOF OF FAILURE. Same fixture, same assertion, broken policies.
  // ═════════════════════════════════════════════════════════════════════════

  /**
   * Build the mutant origin from the policy the gateway is ACTUALLY sending.
   *
   * Fetched from a live token-path response, then split. A hand-copied mutant
   * would keep testing the policy we used to have; this one follows §10.3
   * wherever it goes.
   */
  async function ensureMutantOrigin(page: Page): Promise<MutantOrigin> {
    if (mutant) return mutant;
    await page.goto('/');
    const tokenURL = await tokenURLFor(page, 'index.html');
    const response = await page.request.get(tokenURL);
    expect(response.ok(), `token path → ${response.status()}`).toBeTruthy();
    const policy = response.headers()['content-security-policy'];
    expect(policy, 'every token-path response must carry the §10.3 policy (FR-005a)').toBeTruthy();
    shippedPolicy = policy;

    const policies = mutantPolicies(shippedPolicy);
    expect(policies.sandboxonly, 'the shipped policy must contain a sandbox directive').toMatch(/^sandbox\b/);
    expect(policies.nosandbox, 'the shipped policy must contain source directives').toContain("default-src 'none'");
    mutant = await startMutantOrigin(policies, ext.port);
    return mutant;
  }

  async function driveMutant(page: Page, policy: 'shipped' | 'nosandbox' | 'sandboxonly' | 'none'): Promise<void> {
    const origin = await ensureMutantOrigin(page);
    ext.reset();
    await page.goto(origin.documentURL(policy));
    await clickInsidePreview(page);
    await page.waitForTimeout(EGRESS_SETTLE_MS);
  }

  async function waitForVectors(expected: number, timeoutMs = 15_000): Promise<void> {
    // Poll on the LIST, not on a count: "6 of 7" without naming the missing one
    // is a failure nobody can act on, and the experiment's measured table is
    // written in vector names.
    await expect.poll(
      () => vectorsReaching(ext.hits).sort(),
      { timeout: timeoutMs },
    ).toHaveLength(expected);
  }

  test('mutation control — with NO policy, all seven vectors reach the second origin', async ({ page }) => {
    // If this ever reports fewer than seven, every "zero" above is meaningless:
    // it would mean the probes do not fire or the observer does not see them.
    // The experiment states the same rule in §6.
    await driveMutant(page, 'none');
    await waitForVectors(EGRESS_VECTORS.length);

    const reached = vectorsReaching(ext.hits).sort();
    expect(reached, 'the harness must be able to SEE every vector it claims to block')
      .toEqual([...EGRESS_VECTORS].map((v) => v.name).sort());
    expect(() => assertNoEgress(ext.hits, 'the no-policy control')).toThrow(/egress reached/);
  });

  test('mutation — dropping the SANDBOX half lets window.open out, and the assertion goes red', async ({ page }) => {
    // Measured on Chromium 151, Firefox 153 and WebKit 26.5 (experiment §1, row
    // `nosandbox`): source directives alone, and the popup still arrives. No CSP
    // directive covers popup navigation; `navigate-to` was dropped from the
    // specification. Only the sandbox's omission of allow-popups closes it.
    await driveMutant(page, 'nosandbox');
    await waitForVectors(1);

    const reached = vectorsReaching(ext.hits);
    expect(reached, 'the vector the source directives alone cannot stop').toContain('popup');
    expect(() => assertNoEgress(ext.hits, 'the sandbox-less mutant')).toThrow(/egress reached/);

    // The other half of what the sandbox directive buys, asserted rather than
    // attempted: without it the origin is not opaque and the page reads its own
    // cookies back. The experiment read `omnipus_probe=SECRET` here on all
    // three engines. This mutant keeps `form-action 'none'`, so the document is
    // still here to be read — no conditional guard, which would let the two
    // assertions below vanish silently.
    const report = await readPreviewReport(page);
    expect(report.origin_opaque, 'without the sandbox directive the origin is NOT opaque').toBe(false);
    expect(report.cookie, 'without the sandbox directive the page reads the cookie back')
      .toContain('omnipus_probe=SECRET');
  });

  test('mutation — dropping the SOURCE half lets five of seven out, and the assertion goes red', async ({ page }) => {
    // Measured (experiment §1, row `sandboxonly`): beacon, fetch, img, iframe
    // and ws all escape. The sandbox seals the origin and stops popups and
    // forms; it controls no subresource fetching at all.
    await driveMutant(page, 'sandboxonly');
    await waitForVectors(5);

    const reached = vectorsReaching(ext.hits);
    for (const vector of ['image', 'fetch', 'beacon', 'websocket', 'iframe'] as EgressVectorName[]) {
      expect(reached, `${vector} escapes when the source directives are gone`).toContain(vector);
    }
    expect(() => assertNoEgress(ext.hits, 'the source-directive-less mutant')).toThrow(/egress reached/);
  });

  test('mutation baseline — the SHIPPED policy, served by the harness itself, blocks all seven', async ({ page }) => {
    // Not a duplicate of test 11. This one isolates the POLICY from the
    // gateway: same fixture, same assertions, a plain static server. If this
    // fails while 11 passes, the difference is something other than the policy
    // string, and that is worth knowing before anyone trusts the mutants above.
    await driveMutant(page, 'shipped');
    assertNoEgress(ext.hits, 'the shipped-policy mutant baseline');
    expect(vectorsReaching(ext.hits)).toEqual([]);
  });
});
