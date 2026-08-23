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
 * 95 (`E2E_PreviewFrame_SandboxComposition`, as 11d),
 * 110 (`E2E_PreviewSameOrigin_ReachableButUnauthenticated`),
 * 111 (`E2E_PreviewCannotFrameTheSpa`), and — because the isolation projects
 * run all three engines — the browser-matrix half of 12.
 *
 * ═══════════════════════════════════════════════════════════════════════════
 * WHY THIS FILE MEASURES RENDERING AND CONTAINMENT TOGETHER
 * ═══════════════════════════════════════════════════════════════════════════
 *
 * A rendering defect shipped here behind a green security suite, and the shape
 * of the mistake is worth more than the fix.
 *
 * The original experiment measured the §10.3 HEADER, alone, on three engines:
 * zero egress, opaque origin, cookie unreadable, CSS and JS working. FR-005b
 * then added a `sandbox="allow-scripts"` ATTRIBUTE on the frame, as defence in
 * depth. That COMPOSITION — header AND attribute — was never measured, and
 * under WebKit it stopped `script-src 'self'` matching the serving origin: a
 * previewed bundle's external `<script src>` and `<link rel=stylesheet>` did
 * not load, while every containment assertion in this file went on passing.
 * Chromium and Firefox were unaffected. Full table in 11c's comment.
 *
 * The fix was §10.3 naming the gateway's origins EXPLICITLY **in addition to**
 * `'self'`, never instead of it — and BOTH isolation mechanisms stayed. Each of
 * those three "boths" is load-bearing and none is a belt-and-braces flourish:
 *
 *   `'self'` AND the origins — `'self'` matches whatever spelling the reader
 *     typed, so on Chromium and Firefox no misconfigured origin string can ever
 *     break a preview. The explicit origins are what WebKit matches, because it
 *     does not resolve `'self'` inside an attribute-sandboxed frame at all.
 *     Delete `'self'` and a wrong origin breaks all three engines instead of
 *     one; delete the origins and WebKit is back where it started.
 *   header AND attribute — the header carries twelve rules and the attribute
 *     provides one of them. Measured: the sandbox half alone let five of seven
 *     egress vectors out, the source half alone let `window.open` out.
 *
 * The generalisable lesson: a preview that renders NOTHING satisfies every
 * "nothing escaped" assertion perfectly. So no test here may assert only
 * containment. 11c asserts rendering on its own, and 11d (spec test 95)
 * asserts rendering AND all seven egress vectors in ONE observation of ONE
 * loaded frame — the assertion that, had it existed, would have caught this on
 * the day the attribute was added.
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
 * The last tests in this file are the proof. They serve the SAME fixture bytes
 * through the SAME assertion functions, from a local origin that applies a
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
 *   • sources repointed at  → the bundle's external script and stylesheet must
 *     a dead origin           NOT load, while the inline script still fires all
 *                             twelve probes. This is the mutation proof for the
 *                             RENDER half — 11c and 11d assert `js_ran` and
 *                             `css_applied`, and a claim that a page rendered
 *                             is worth nothing until it has been seen to fail.
 *   • the FR-005b ATTRIBUTE → five of seven must arrive, the same five the
 *     with NO header          `sandbox` DIRECTIVE alone let out. This is why
 *                             the header cannot be dropped "because the frame
 *                             is sandboxed anyway": the attribute provides one
 *                             of the header's twelve rules.
 *
 * The egress rows assert that `assertNoEgress` — the exact function guarding the
 * product tests above — THROWS. The expected vector lists are read off the
 * experiment's measured table (adr-067-preview-isolation-experiment-2026-08-22
 * §1), never off the implementation.
 *
 * ⚠️ THE MUTANTS ARE DERIVED FROM THE LIVE SHIPPED HEADER AND MUST STAY THAT
 * WAY. `splitPolicy` partitions on the DIRECTIVE NAME (`sandbox` vs the rest)
 * and `repointSourcesToDeadOrigin` rewrites any origin-bearing source, keyword
 * or explicit. Neither knows what `'self'` is, which is why both kept mutating
 * unchanged when §10.3 added the gateway's explicit origins alongside `'self'`
 * — a policy shape in which a mutant that rewrote only the keyword, or only the
 * host sources, would have left the other half live and stopped mutating at
 * all. A mutant built by copying the policy into this file would have kept
 * testing the policy we used to have — silently, and with a full row of green.
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
  type MutantPolicyName,
  type RecordedHit,
  type RecordingOrigin,
  DEAD_ORIGIN,
  directiveHostSources,
  directiveNamesOriginExplicitly,
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
    // containment one, and for a while the two disagreed on one engine. This
    // test went RED on WebKit on purpose, for four days, because that is what
    // a real defect looks like. It is here in its original strength — the fix
    // was to the product, not to these two assertions.
    //
    // WHAT WAS MEASURED, one engine at a time, embedded exactly as FR-005b
    // requires (`sandbox="allow-scripts"` on the frame) and top-level:
    //
    //   policy source form        engine    embedded          top-level
    //   'self' alone              Chromium  js ✅  css ✅      js ✅  css ✅
    //   'self' alone              Firefox   js ✅  css ✅      js ✅  css ✅
    //   'self' alone              WebKit    js ❌  css ❌      js ✅  css ✅
    //   'self' + this origin      Chromium  js ✅  css ✅      js ✅  css ✅
    //   'self' + this origin      Firefox   js ✅  css ✅      js ✅  css ✅
    //   'self' + this origin      WebKit    js ✅  css ✅      js ✅  css ✅
    //   'self' + a DIFFERENT      Chromium  js ✅  css ✅      js ✅  css ✅
    //     spelling of this        Firefox   js ✅  css ✅      js ✅  css ✅
    //     origin                  WebKit    js ❌  css ❌      js ✅  css ✅
    //
    // READ THE LAST THREE ROWS BEFORE TOUCHING THE POLICY. They are the reason
    // §10.3 keeps `'self'` AND names the origin, and the reason the precondition
    // in 11d exists. An explicit host source is matched by STRING: a policy
    // naming `http://127.0.0.1:PORT` while the reader opened the identical
    // socket as `http://localhost:PORT` does not match, and because `'self'`
    // still covers Chromium and Firefox, that misconfiguration shows up on ONE
    // engine out of three — as a blank preview, with nothing naming the cause.
    // (§10.3 answers it by naming all three loopback spellings when the bind is
    // loopback; a reverse proxy needs `gateway.public_url` to be right.)
    //
    // THE INLINE SCRIPT RAN IN EVERY ONE OF THOSE CELLS — `attempted` came back
    // complete at twelve in all of them, so no row is the trivial "the page
    // never loaded". What failed was the EXTERNAL `<script src>` and
    // `<link rel=stylesheet>`: under WebKit, adding FR-005b's sandbox
    // ATTRIBUTE on top of §10.3's sandbox DIRECTIVE stopped `'self'` matching
    // the serving origin. Removing the attribute made WebKit pass, so the
    // header alone was never the problem; the COMPOSITION was.
    //
    // THE FIX, and why it is the product's and not this test's: §10.3 now names
    // the gateway's origin EXPLICITLY in its source directives instead of
    // leaning on `'self'`, so the match no longer depends on the document
    // having a usable `self` at all. BOTH isolation mechanisms stayed — the
    // header carries twelve rules and the attribute provides one of them
    // (measured: the sandbox half alone let five of seven vectors out; the
    // source half alone let `window.open` out). Dropping either was never on
    // the table.
    //
    // WHY IT WAS NEVER CAUGHT: the original experiment measured the HEADER
    // ALONE. Containment was unaffected throughout — opaque origin, cookie
    // throws, zero of seven egress; 11a and 11b passed on WebKit the whole
    // time. What broke was US-1 AS-4, the flagship "a complete bundle loads
    // all of its assets" scenario, and a page that renders nothing satisfies
    // every containment assertion ever written. 11d exists so that the two
    // properties can never again be observed apart.
    //
    // CAN THIS STILL FAIL? Yes, and it is proved rather than asserted: the
    // `wrongsource` mutation below serves these exact bytes under this exact
    // policy with its origin-bearing sources repointed at a dead origin, and
    // requires both flags below to come back FALSE while `attempted` stays at
    // twelve.
    await page.goto('/');
    const tokenURL = await tokenURLFor(page, 'index.html');
    await embedPreview(page, tokenURL);

    const report = await readPreviewReport(page, '#e2e-preview-frame');
    expect(report.js_ran, "FR-004 / script-src: the bundle's external script MUST execute").toBe(true);
    expect(report.css_applied, 'style-src: the bundle\'s external stylesheet MUST apply').toBe(true);
  });

  // ───────────────────────────────────────────────────────────────────────────
  // Spec test 95 — E2E_PreviewFrame_SandboxComposition
  // ───────────────────────────────────────────────────────────────────────────
  test('11d — FR-005b composition: the header AND the frame attribute together, rendering and containment in ONE observation', async ({ page, baseURL }) => {
    // THE TEST THIS EPISODE WAS MISSING, and the reason it is one test rather
    // than two.
    //
    // 11a proves containment. 11c proves rendering. Both passed on WebKit for
    // four days while the product was broken — because 11c did not exist yet,
    // and every assertion that did exist was satisfied *better* by a page that
    // rendered nothing at all. The original experiment made the same split:
    // it measured the §10.3 HEADER alone, so the composition FR-005b actually
    // ships — header AND `sandbox="allow-scripts"` on the frame — was never
    // measured in either property, let alone both at once.
    //
    // So this test observes ONE frame, loaded ONCE, the way the product loads
    // it, and requires all of it to hold simultaneously:
    //
    //   the frame carries FR-005b's three attributes;
    //   the response carries a policy that names an origin the frame is on;
    //   the bundle's EXTERNAL script ran and its EXTERNAL stylesheet applied;
    //   all twelve probes fired;
    //   the form was refused rather than skipped;
    //   ZERO of the seven egress vectors reached the second origin;
    //   the origin is opaque and `document.cookie` THREW.
    //
    // Any future change that buys one of those with another — the exact trade
    // that produced this defect — turns this test red on the engine where it
    // happens, and the failure message names which half went.
    const log = recordBrowserRequests(page.context());
    await page.goto('/');
    const tokenURL = await tokenURLFor(page, 'index.html');
    const gatewayOrigin = new URL(tokenURL, page.url()).origin;

    // ── PRECONDITION, and it exists to make ONE failure mode loud ────────────
    //
    // §10.3 names the gateway's origins EXPLICITLY as well as saying `'self'`.
    // The explicit half is what fixed WebKit (see 11c) and it is matched by
    // STRING: it covers the exact spellings named and nothing else. So a
    // deployment reachable by a name the policy does not list renders previews
    // with no CSS and no JS — measured, on a policy naming
    // `http://127.0.0.1:PORT` while the browser opened the identical socket as
    // `http://localhost:PORT`.
    //
    // ⚠️ AND IT HIDES ON TWO ENGINES OUT OF THREE. `'self'` is retained, so
    // Chromium and Firefox keep working through exactly that misconfiguration
    // and only WebKit goes blank — with no error, nothing in the console that
    // names a cause, and a rendering symptom indistinguishable from the browser
    // bug this whole change exists to work around. That is why this is asserted
    // HERE, directly, rather than left to 11c to catch: 11c would go red on one
    // engine and read as "WebKit again".
    //
    // ⚠️ `'self'` DELIBERATELY DOES NOT SATISFY THIS ASSERTION. An earlier
    // version accepted either form, which — with `'self'` on every directive —
    // made it permanently true and therefore incapable of reporting anything.
    // A diagnostic that cannot fail is not a diagnostic.
    const headResponse = await page.request.get(tokenURL);
    expect(headResponse.ok(), `token path → ${headResponse.status()}`).toBeTruthy();
    const livePolicy = headResponse.headers()['content-security-policy'];
    expect(livePolicy, 'FR-005a: every token-path response carries the §10.3 policy').toBeTruthy();
    for (const directive of ['script-src', 'style-src'] as const) {
      const named = directiveHostSources(livePolicy, directive);
      expect(
        directiveNamesOriginExplicitly(livePolicy, directive, gatewayOrigin),
        `${directive} does not name ${gatewayOrigin} — the origin this browser is ` +
        `actually on (baseURL ${baseURL}). It names: ` +
        `${named.length ? named.join(', ') : '(no origin at all — only \'self\')'}.\n` +
        `Consequence: the bundle's own ${directive === 'script-src' ? 'external script' : 'external stylesheet'} ` +
        'will NOT load in Safari/WebKit, while Chromium and Firefox render it correctly via ' +
        "`'self'` — so this misconfiguration is invisible on two engines out of three.\n" +
        'Fix, in order of likelihood:\n' +
        '  • same host under a different name (127.0.0.1 vs localhost vs a LAN IP): the ' +
        'gateway derives its origin from gateway.host/gateway.port, so open the SPA at the ' +
        'origin it derived, or set gateway.public_url to the one you use;\n' +
        '  • behind a reverse proxy: set gateway.public_url to the HTTPS origin the browser ' +
        'reaches;\n' +
        '  • no origin named at all: the bind is a wildcard (0.0.0.0 / ::) so no origin is ' +
        'derivable — set gateway.public_url. The gateway logs this at boot too.\n' +
        `Served policy: ${livePolicy}`,
      ).toBe(true);
    }

    await embedPreview(page, tokenURL);

    // ── FR-005b's three attributes, read off the live DOM ────────────────────
    // Asserted rather than assumed: `embedPreview` sets them, but if a future
    // edit drops one, every containment row below would still pass — the header
    // would be carrying the whole load — and the "defence in depth if a proxy
    // strips the header" property would be gone with no symptom.
    const attrs = await page.evaluate(() => {
      const f = document.querySelector('#e2e-preview-frame');
      return {
        sandbox: f?.getAttribute('sandbox') ?? null,
        referrerpolicy: f?.getAttribute('referrerpolicy') ?? null,
        allow: f?.getAttribute('allow') ?? null,
        srcdoc: f?.hasAttribute('srcdoc') ?? false,
      };
    });
    expect(attrs.sandbox, 'FR-005b: sandbox="allow-scripts", and NOT allow-same-origin').toBe('allow-scripts');
    expect(attrs.referrerpolicy, 'FR-005b: the token must not leave in a Referer').toBe('no-referrer');
    expect(attrs.allow, 'FR-005b: an empty allow="" delegates no permissions').toBe('');
    expect(attrs.srcdoc, '§10.6: never srcdoc — it has no response to carry the policy').toBe(false);

    // ── NON-VACUITY, from the frame's own DOM ────────────────────────────────
    // Not from the network: WebKit surfaces none of a sandboxed frame's
    // requests to the driver, so a request-count oracle is blind there and
    // every negative below would be automatically true.
    const report = await readPreviewReport(page, '#e2e-preview-frame');
    expect(report.attempted.split(','), 'all twelve probes must have been attempted')
      .toHaveLength(PROBES_BEFORE_FORM + 1);

    await clickInsidePreview(page, '#e2e-preview-frame');
    await page.waitForTimeout(EGRESS_SETTLE_MS);

    // ── RENDERING (US-1 AS-4, FR-004) ────────────────────────────────────────
    // The half the header+attribute composition broke on WebKit. `'unsafe-inline'`
    // cannot explain either of these: both flags are set by an EXTERNAL file.
    expect(report.js_ran, "FR-004: under the composition, the bundle's external script MUST run").toBe(true);
    expect(report.css_applied, 'US-1 AS-4: and its external stylesheet MUST apply').toBe(true);

    // ── CONTAINMENT (FR-005, FR-006), the SAME frame, the SAME load ──────────
    const previewFrame = page.frames().find((f) => f.url().includes(tokenURL));
    expect(previewFrame, 'the preview frame navigated away — the form submit was NOT blocked').toBeTruthy();
    assertNoEgress(ext.hits, 'the composed header + attribute embedding');
    expect(vectorsReaching(ext.hits)).toEqual([]);
    expect(report.origin_opaque, 'FR-005: the document must be bound to an opaque origin').toBe(true);
    expect(report.cookie, 'document.cookie must THROW, not return empty').toMatch(/^THREW:/);

    // The browser-side log is deliberately NOT an oracle here — see the module
    // header. It is kept only so a failure report can show what the driver did
    // see, which on WebKit is nothing from inside the frame, by design.
    await log.settle();
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
    // The `wrongsource` mutant must actually BE a mutation. If §10.3 ever
    // reached a shape with no origin-bearing source at all, the rewrite would
    // be the identity function and the render-mutation proof below would pass
    // while proving nothing — the precise false-green this file is built
    // against.
    expect(policies.wrongsource, 'repointing the sources must change the policy — otherwise the render mutation is vacuous')
      .not.toBe(shippedPolicy);
    expect(policies.wrongsource, `the repointed policy must name ${DEAD_ORIGIN}`).toContain(DEAD_ORIGIN);
    mutant = await startMutantOrigin(policies, ext.port);
    return mutant;
  }

  async function driveMutant(page: Page, policy: MutantPolicyName): Promise<void> {
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

  test('mutation — repointing the SOURCES at a dead origin stops the bundle rendering, and the render assertions go red', async ({ page }) => {
    // THE MUTATION PROOF FOR 11c AND 11d's RENDER HALF.
    //
    // Those two require `js_ran` and `css_applied` to be TRUE. Neither claim is
    // worth anything until this oracle has been seen to report FALSE — and
    // "the page rendered" is the single most likely assertion in this file to
    // go quietly, permanently true. (It is also the mirror image of the trap
    // the containment tests guard against: a page that renders nothing passes
    // every egress assertion perfectly.)
    //
    // The mutant is the LIVE shipped policy with EVERY origin-bearing source
    // rewritten to a dead loopback origin — `'self'` and the explicit host
    // sources alike, because §10.3 carries both and rewriting only one of them
    // would leave the other live and stop mutating anything. It is mechanically
    // derived, so it stays a real mutation through any future change to the
    // policy's source form. `sandbox` is left intact so the origin stays opaque
    // and the report is still readable out of the document.
    await driveMutant(page, 'wrongsource');
    const report = await readPreviewReport(page);

    // NON-VACUITY FIRST, and it is the whole reason this mutation is honest:
    // the document DID load and its inline script DID run all twelve probes.
    // Without this, `js_ran: false` would be equally consistent with "the
    // fixture 404'd", which would make the mutation prove nothing about
    // source-directive matching.
    expect(report.attempted.split(','), 'the mutant document must still load and run its inline script')
      .toHaveLength(PROBES_BEFORE_FORM + 1);

    expect(report.js_ran, 'with the sources repointed, the EXTERNAL script must NOT run').toBe(false);
    expect(report.css_applied, 'with the sources repointed, the EXTERNAL stylesheet must NOT apply').toBe(false);
  });

  test('mutation — the FR-005b ATTRIBUTE alone, with no policy at all, lets five of seven out', async ({ page }) => {
    // WHY BOTH MECHANISMS STAY, stated as a measurement rather than as a claim
    // in a comment.
    //
    // The obvious "simplification" whenever this policy causes trouble is to
    // drop the header and rely on the frame's `sandbox="allow-scripts"`
    // attribute, which looks like it does the same job. It does not: the header
    // carries TWELVE rules and the attribute supplies exactly one of them (the
    // sandbox). Everything the source directives do — no fetch, no beacon, no
    // WebSocket, no cross-origin image, no nested frame — is simply absent.
    //
    // Same fixture bytes, same seven-vector oracle, embedded with the same
    // three FR-005b attributes as the product, from a policy-free host page on
    // the mutant origin. (The gateway's own shell cannot host this: §10.7 gives
    // it `frame-src 'self'`, so the frame would be refused before the mutation
    // was exercised.)
    //
    // Expected: the five the `sandbox` DIRECTIVE alone also let out (experiment
    // §1, row `sandboxonly` — image, fetch, beacon, WebSocket, iframe), with
    // popup and form closed by the sandbox. That list is PRE-REGISTERED from
    // the experiment's measured table, not read off this run.
    const origin = await ensureMutantOrigin(page);
    ext.reset();
    await page.goto(origin.embedderURL());
    await embedPreview(page, origin.documentURL('none'));

    const report = await readPreviewReport(page, '#e2e-preview-frame');
    expect(report.attempted.split(','), 'all twelve probes must have been attempted')
      .toHaveLength(PROBES_BEFORE_FORM + 1);
    // The attribute IS doing its own job — this is not a "no isolation at all"
    // control, it is the half that remains. If this ever comes back false the
    // test below is measuring something other than the attribute standing alone.
    expect(report.origin_opaque, 'the attribute alone still seals the origin').toBe(true);

    await clickInsidePreview(page, '#e2e-preview-frame');
    await waitForVectors(5);

    const reached = vectorsReaching(ext.hits);
    for (const vector of ['image', 'fetch', 'beacon', 'websocket', 'iframe'] as EgressVectorName[]) {
      expect(reached, `${vector} escapes when only the frame attribute remains — the header is not optional`)
        .toContain(vector);
    }
    expect(() => assertNoEgress(ext.hits, 'the attribute-only embedding')).toThrow(/egress reached/);
  });
});
