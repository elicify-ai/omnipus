/**
 * uat-browser-panel.spec.ts — UAT Group C, "The live browser panel"
 * (docs/internal/specs/browser-rework-uat-plan.md §4, UAT-13 … UAT-18).
 *
 * These are the plan's own cases, driven exactly as a human tester would drive
 * them: through the real UI of the already-running gateway, never through the
 * REST API and never through the browser tools directly.
 *
 * Why this file does NOT go through an agent to open the panel.
 * browser-live-video.spec.ts (the pre-existing WebRTC spec) asks Jim to
 * serve_web + navigate + click and then clicks "Watch live" on the resulting
 * tool-call row. That is a fine way to prove the agent path, but it makes every
 * panel assertion depend on a live LLM emitting three specific tool calls. The
 * panel has its OWN, agent-independent entry point — ChatControls.tsx's
 * "Open browser" launcher (ADR-039 D-A1) — which creates a session and attaches
 * the live view against a lazily-created blank tab. Group C is about the PANEL,
 * so the panel's own launcher is the honest door to come in through, and a
 * failure here can never be blamed on the model.
 *
 * The honesty bar these cases are written against (plan §1.2, §4 Group C
 * preamble, ADR-061):
 *   - The JPEG screencast fallback was DELETED on purpose. An <img> whose src is
 *     swapped 30x/second is visually indistinguishable from video, so whenever
 *     the fast path failed the panel silently degraded and looked completely
 *     normal. Every case below therefore asserts on the WebRTC <video> sink
 *     specifically, and treats the appearance of data-testid="browser-live-img"
 *     as a hard failure rather than a pass.
 *   - "No error appeared" is never a pass. Each test asserts a positive,
 *     measured observation (decoded frame counts, changed pixels, a landing URL,
 *     a specific chip label, a specific error string).
 */

import * as fs from 'fs';
import * as path from 'path';
import { expect, type Page, type Locator, type TestInfo } from '@playwright/test';
import { test } from './fixtures/console-errors';
import {
  browserLivePanel,
  browserLiveFrame,
  browserLiveVideo,
  assistantMessages,
  browserLiveImgFallback,
  chatInput,
  selectAgent,
  userMessages,
  waitForConnected,
} from './fixtures/selectors';
import { restoreAdminSession } from './fixtures/admin-api';

/**
 * Record an observation. Attached to the Playwright report AND printed to
 * stdout — a UAT run is reported by a human from what was actually observed,
 * so the observations must survive outside the HTML report.
 */
async function note(testInfo: TestInfo, name: string, body: string): Promise<void> {
  console.log(`[UAT ${testInfo.title.split(' ')[0]}] ${name}: ${body.replace(/\n/g, ' | ')}`);
  await testInfo.attach(name, { body, contentType: 'text/plain' });
}

/** the-internet.herokuapp.com — the plan's §3.3/§3.4 fixture host. */
const HEROKU = 'https://the-internet.herokuapp.com';

/**
 * Open a chat that BELONGS TO A WORKSPACE, and return that workspace's id.
 *
 * This is not incidental setup — it is a real finding from the first run of this
 * file, recorded here so the next reader does not rediscover it. Opening the
 * panel from the plain `/` chat (a session with no workspace) is REFUSED, with:
 *
 *   agent "jim" is on more than one workspace's team, so which workspace's
 *   browser — and whose live logins — this panel would show is ambiguous; it is
 *   refused rather than guessed. Open this panel from a chat that belongs to
 *   the workspace you mean
 *
 * That is D1 FR-033's ambiguity refusal doing exactly what it should (and it is
 * a good message — it names the agent, the reason, and the fix). Under the
 * rework a browser belongs to a WORKSPACE, so a workspace-less chat has no
 * browser to show. Every case below therefore enters through a workspace chat.
 *
 * The DEFAULT workspace is used deliberately in preference to the plan's
 * Alpha/Bravo fixture: panel-opened tabs are workspace-scoped and visible to
 * every agent on that workspace, so driving the panel inside Alpha would inject
 * tabs into the very workspace the ownership cases (UAT-01…UAT-12) are counting.
 */
async function openWorkspaceChat(page: Page): Promise<string> {
  await refreshSession(page);
  const res = await page.request.get('/api/v1/workspaces');
  if (!res.ok()) throw new Error(`BLOCKED: GET /api/v1/workspaces returned ${res.status()}`);
  const list = (await res.json()) as Array<{ id: string; name: string; is_default?: boolean }>;
  if (list.length === 0) throw new Error('BLOCKED: no workspaces exist — the panel has no browser to show');
  const ws = list.find((w) => w.is_default) ?? list[0];
  await page.goto(`/#/workspaces/${ws.id}/chat`);
  return ws.id;
}

/**
 * Bind the current chat to its workspace by sending one message, then stop the
 * turn immediately.
 *
 * Why this is necessary, and why it is itself a finding (see the report):
 * pkg/gateway/browser_ws.go's `sessionWorkspaceID` resolves the panel's
 * workspace from the CHAT SESSION'S OWN meta on disk (ADR-075 FR-017) — the
 * client never gets to name a workspace, because a workspace's browser holds
 * that workspace's live logins. But a session only acquires a workspace when a
 * message is sent through it (src/store/chat.ts forwards
 * `metadata.workspace_id`), and ChatControls' "Open browser" launcher creates
 * its session with `POST /sessions {agent_id}` alone (src/lib/api.ts's
 * `createSession`) — no workspace. So on a BRAND-NEW workspace chat the
 * launcher produces a workspace-less session and the panel refuses as
 * ambiguous, telling the operator to "open this panel from a chat that belongs
 * to the workspace you mean" — which is exactly where they already are.
 *
 * Sending a message first is the user-visible workaround, so that is what a
 * human tester would end up doing, and so it is what this fixture does.
 */
async function bindChatToWorkspace(page: Page): Promise<void> {
  const input = chatInput(page);
  await expect(input).toBeVisible({ timeout: 15_000 });
  await input.fill('hi');
  await input.press('Enter');
  await expect(userMessages(page).last()).toBeVisible({ timeout: 60_000 });
  // Stop the turn straight away — a running turn puts the panel's drive chip
  // into "{agent} is browsing…" and would contaminate the control-state case.
  const stop = page.locator('[data-testid="stop-btn"]');
  if (await stop.isVisible().catch(() => false)) {
    await stop.click({ timeout: 5_000 }).catch(() => undefined);
  }
  await expect(stop).toBeHidden({ timeout: 120_000 });
}

/**
 * The gateway keeps ONE session per user (`UserConfig.SessionTokenHash` is a
 * single slot — pkg/gateway/rest_auth.go calls it "the single-slot
 * session_token_hash"), and the harness's globalSetup logs in as `admin` on
 * every invocation. During a UAT with several testers driving the same gateway,
 * any other run starting invalidates this run's cookie mid-test, and the panel
 * then reports, correctly, "Your session expired — reload the page to
 * reconnect."
 *
 * That is an environment collision, not a product defect and not a panel
 * defect, so it must never be reported as either.
 *
 * The repair must not itself be a login. `POST /api/v1/auth/login` re-mints the
 * single slot, so a spec that logs in to save itself evicts the NEXT tester —
 * the exact crosstalk `scripts/check-e2e-login-crosstalk.sh` forbids, and the
 * cause of the 2026-08-28 create-agent incident. `restoreAdminSession` instead
 * copies the shared storageState file forward into this context, minting
 * nothing: it recovers the case a spec legitimately can (this context is stale,
 * the shared session on disk is current) and leaves the case it must not
 * "recover" — the shared session is genuinely gone — to be reported as BLOCKED
 * by the eviction handling at each call site.
 */
async function refreshSession(page: Page): Promise<void> {
  await restoreAdminSession(page);
}

/** Did this failure come from another tester's login evicting ours? */
function isSessionEviction(message: string | null): boolean {
  return message !== null && /session expired/i.test(message);
}

/**
 * Did the product REFUSE to open a browser because the machine is short of
 * memory? That is the admission control working as designed, not a defect, and
 * it must never be reported as one — but it also means the case did not run.
 *
 * It is distinguishable from every other failure by more than its wording: it
 * arrives in well under two seconds, because no browser was ever started.
 */
function isMemoryRefusal(message: string | null): boolean {
  return message !== null && /low on memory|no further browser tab can be opened/i.test(message);
}

/** ChatControls.tsx's agent-independent launcher (ADR-039 D-A1). */
const openBrowserButton = (page: Page) => page.getByRole('button', { name: 'Open browser' });

/** BrowserLiveView.tsx Row B — the omnibox. Reflects the ACTIVE TAB's real url. */
const addressBar = (page: Page) => page.getByLabel('Address bar');

/** The single drive-state signal in the header (BrowserLiveView.tsx driveChip). */
const statusChip = (page: Page) => page.locator('[data-testid="browser-live-status-chip"]');

/** Either Retry affordance — both only render when displayError is non-null. */
const retryControl = (page: Page) =>
  page.locator('[data-testid="browser-live-retry"], [data-testid="browser-live-retry-overlay"]');

/**
 * Open the live panel through the product's own launcher and wait for the
 * WebRTC <video> sink to attach.
 *
 * Returns the sink verdict rather than asserting it, so a caller can report a
 * memory-limit refusal or a capability refusal as the OBSERVATION it is instead
 * of a bare timeout. The distinction the operator asked for — "a browser action
 * that fails in ~1-2 seconds never got a browser at all" — is preserved by
 * returning the elapsed time alongside.
 */
async function openLivePanelOnce(
  page: Page,
  opts: { timeoutMs?: number } = {},
): Promise<{ verdict: 'video' | 'img' | 'error' | 'none'; elapsedMs: number; error: string | null }> {
  const timeoutMs = opts.timeoutMs ?? 90_000;

  await openWorkspaceChat(page);
  await waitForConnected(page);
  await selectAgent(page, /Jim/i);
  await bindChatToWorkspace(page);

  const launcher = openBrowserButton(page);
  await expect(launcher).toBeVisible({ timeout: 15_000 });
  const started = Date.now();
  await launcher.click();

  await expect(browserLivePanel(page)).toBeVisible({ timeout: 15_000 });

  const video = browserLiveVideo(page);
  const img = browserLiveImgFallback(page);
  const retry = retryControl(page);
  const deadline = Date.now() + timeoutMs;

  while (Date.now() < deadline) {
    if (await video.count()) {
      return { verdict: 'video', elapsedMs: Date.now() - started, error: null };
    }
    // ADR-061 deleted this element. If it EVER matches, the deleted fallback is
    // back and the panel is lying about being live — report it, never pass on it.
    if (await img.count()) {
      return { verdict: 'img', elapsedMs: Date.now() - started, error: null };
    }
    if (await retry.first().isVisible().catch(() => false)) {
      // Capture the WHOLE panel body, not just a matched line: a reason we
      // cannot quote is a reason we cannot judge, and this group is about
      // whether the operator is told what actually went wrong.
      const message = (await readDisplayError(page)) ?? (await browserLivePanel(page).innerText().catch(() => ''));
      return { verdict: 'error', elapsedMs: Date.now() - started, error: message };
    }
    await page.waitForTimeout(500);
  }
  return { verdict: 'none', elapsedMs: Date.now() - started, error: await readDisplayError(page) };
}

/**
 * `openLivePanelOnce`, retried when — and only when — the failure was another
 * tester's login evicting ours (see refreshSession). Any other failure is
 * returned unchanged on the first attempt: retrying a real defect until it
 * looks green is exactly the habit this plan exists to break.
 */
async function openLivePanel(
  page: Page,
  opts: { timeoutMs?: number } = {},
): Promise<{ verdict: 'video' | 'img' | 'error' | 'none'; elapsedMs: number; error: string | null }> {
  let last = await openLivePanelOnce(page, opts);
  for (let attempt = 0; attempt < 4; attempt += 1) {
    if (last.verdict !== 'error') break;
    if (isSessionEviction(last.error)) {
      await refreshSession(page);
    } else if (isMemoryRefusal(last.error)) {
      // Wait for the machine to give some memory back rather than declaring the
      // product broken. If it never does, the caller reports BLOCKED with the
      // product's own sentence, which is the honest outcome.
      await page.waitForTimeout(30_000);
    } else {
      break;
    }
    last = await openLivePanelOnce(page, opts);
  }
  return last;
}

/**
 * The visible error text the panel is showing, if any. displayError is rendered
 * in three places (pre-attach block, waiting overlay, post-attach strip); this
 * reads whichever is mounted rather than assuming one.
 */
async function readDisplayError(page: Page): Promise<string | null> {
  const panel = browserLivePanel(page);
  if (!(await panel.count())) return null;
  const text = await panel.innerText().catch(() => '');
  // Take the LONGEST candidate line, not the first: the header's drive chip
  // renders the bare word "Error" above the actual sentence, and reporting
  // "Error" as the reason would hide exactly what this group exists to check.
  const candidates = text
    .split('\n')
    .map((s) => s.trim())
    .filter(
      (s) =>
        s.length > 0 &&
        s.toLowerCase() !== 'retry' &&
        s.toLowerCase() !== 'error' &&
        /video|error|failed|capture|refused|ambiguous|turned off|isn't|no video/i.test(s),
    )
    .sort((a, b) => b.length - a.length);
  return candidates[0] ?? null;
}

/** Drive the live browser from the panel's own omnibox, as a human would. */
async function navigateLiveBrowser(page: Page, url: string): Promise<void> {
  const bar = addressBar(page);
  await bar.click();
  await bar.fill(url);
  await bar.press('Enter');
}

/** Total frames the <video> element has EVER decoded — the honest FPS source. */
async function decodedFrames(video: Locator): Promise<number> {
  return video.evaluate((el) => {
    const v = el as HTMLVideoElement & {
      getVideoPlaybackQuality?: () => { totalVideoFrames: number };
      webkitDecodedFrameCount?: number;
    };
    if (typeof v.getVideoPlaybackQuality === 'function') {
      return v.getVideoPlaybackQuality().totalVideoFrames;
    }
    return v.webkitDecodedFrameCount ?? 0;
  });
}

interface Frame {
  /** Mean RGB of a grid of sample cells, in row-major order. */
  cells: number[];
  width: number;
  height: number;
  png: string;
}

/**
 * Draw the CURRENT decoded frame to an offscreen canvas and reduce it to a
 * coarse grid of per-cell mean luminance. Comparing grids (rather than one
 * average) is what makes "the page scrolled" distinguishable from "the whole
 * picture got slightly brighter".
 */
async function sampleFrame(video: Locator): Promise<Frame> {
  return video.evaluate((el) => {
    const v = el as HTMLVideoElement;
    const canvas = document.createElement('canvas');
    canvas.width = v.videoWidth || 1;
    canvas.height = v.videoHeight || 1;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('2D canvas context unavailable');
    ctx.drawImage(v, 0, 0, canvas.width, canvas.height);
    const COLS = 8;
    const ROWS = 8;
    const cw = Math.max(1, Math.floor(canvas.width / COLS));
    const ch = Math.max(1, Math.floor(canvas.height / ROWS));
    const cells: number[] = [];
    for (let r = 0; r < ROWS; r += 1) {
      for (let c = 0; c < COLS; c += 1) {
        const d = ctx.getImageData(c * cw, r * ch, cw, ch).data;
        let sum = 0;
        let n = 0;
        for (let i = 0; i < d.length; i += 4) {
          sum += 0.299 * d[i] + 0.587 * d[i + 1] + 0.114 * d[i + 2];
          n += 1;
        }
        cells.push(sum / n);
      }
    }
    return { cells, width: canvas.width, height: canvas.height, png: canvas.toDataURL('image/png') };
  });
}

/**
 * Save a captured frame as a PNG. Attached to the report, and ALSO written to
 * UAT_ARTIFACT_DIR when set — `test-results/` is wiped at the start of every
 * Playwright run, and during a UAT several testers drive the same checkout, so
 * an artifact that only lives there can be gone before it is read.
 */
async function saveFrame(testInfo: TestInfo, name: string, frame: Frame): Promise<void> {
  const png = Buffer.from(frame.png.split(',')[1] ?? '', 'base64');
  await testInfo.attach(name, { body: png, contentType: 'image/png' });
  const dir = process.env.UAT_ARTIFACT_DIR;
  if (dir) {
    fs.mkdirSync(dir, { recursive: true });
    fs.writeFileSync(path.join(dir, name), png);
  }
}

/** Number of grid cells whose luminance moved by more than `threshold`. */
function changedCells(a: Frame, b: Frame, threshold = 6): number {
  let n = 0;
  for (let i = 0; i < Math.min(a.cells.length, b.cells.length); i += 1) {
    if (Math.abs(a.cells[i] - b.cells[i]) > threshold) n += 1;
  }
  return n;
}

/** Mean luminance across the whole grid — guards "changed, but it's all black". */
function meanLuminance(f: Frame): number {
  return f.cells.reduce((s, v) => s + v, 0) / f.cells.length;
}

/**
 * Where a given element sits on a page, measured by rendering that SAME page in
 * THIS Playwright browser at the SAME viewport as the remote capture.
 *
 * This is the oracle for "the click landed where I clicked". Without an
 * independent source for the target's coordinates the only alternative is to
 * click somewhere and accept whatever happens, which is precisely the silent
 * failure UAT-14 names: the picture is behind the real page, the click hits
 * whatever is now under that coordinate, and the result gets blamed on the site.
 */
async function coordsOnPage(
  page: Page,
  url: string,
  viewport: { width: number; height: number },
  selector: string,
): Promise<{ x: number; y: number }> {
  const probe = await page.context().newPage();
  try {
    await probe.setViewportSize(viewport);
    await probe.goto(url, { waitUntil: 'domcontentloaded', timeout: 30_000 });
    const box = await probe.locator(selector).first().boundingBox();
    if (!box) throw new Error(`coordsOnPage: ${selector} has no bounding box on ${url}`);
    return { x: Math.round(box.x + box.width / 2), y: Math.round(box.y + box.height / 2) };
  } finally {
    await probe.close();
  }
}

/**
 * Mean luminance of ONE rectangle of the current decoded frame, in the REMOTE
 * page's own pixel coordinates.
 *
 * The 8x8 whole-frame grid is too coarse for a small, local repaint — a few
 * characters appearing in a text field move a full-frame cell by well under a
 * unit. Measuring only the rectangle that is supposed to change makes
 * "the page responded" observable without lowering the threshold to the point
 * where encoder noise would satisfy it.
 */
async function sampleRegion(
  video: Locator,
  rect: { x: number; y: number; width: number; height: number },
): Promise<number> {
  return video.evaluate((el, r) => {
    const v = el as HTMLVideoElement;
    const canvas = document.createElement('canvas');
    canvas.width = v.videoWidth || 1;
    canvas.height = v.videoHeight || 1;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('2D canvas context unavailable');
    ctx.drawImage(v, 0, 0, canvas.width, canvas.height);
    const x = Math.max(0, Math.min(canvas.width - 1, Math.round(r.x)));
    const y = Math.max(0, Math.min(canvas.height - 1, Math.round(r.y)));
    const w = Math.max(1, Math.min(canvas.width - x, Math.round(r.width)));
    const h = Math.max(1, Math.min(canvas.height - y, Math.round(r.height)));
    const d = ctx.getImageData(x, y, w, h).data;
    let sum = 0;
    let n = 0;
    for (let i = 0; i < d.length; i += 4) {
      sum += 0.299 * d[i] + 0.587 * d[i + 1] + 0.114 * d[i + 2];
      n += 1;
    }
    return sum / n;
  }, rect);
}

/** An element's full rectangle on a page rendered at the capture's viewport. */
async function rectOnPage(
  page: Page,
  url: string,
  viewport: { width: number; height: number },
  selector: string,
): Promise<{ x: number; y: number; width: number; height: number }> {
  const probe = await page.context().newPage();
  try {
    await probe.setViewportSize(viewport);
    await probe.goto(url, { waitUntil: 'domcontentloaded', timeout: 30_000 });
    const box = await probe.locator(selector).first().boundingBox();
    if (!box) throw new Error(`rectOnPage: ${selector} has no bounding box on ${url}`);
    return box;
  } finally {
    await probe.close();
  }
}

/**
 * Click a point in the REMOTE page's coordinate space by mapping it onto the
 * live view's capture surface. The surface letterboxes the capture
 * (object-contain), so the mapping is scale + offset, mirroring
 * computeObjectContainRect in BrowserLiveView.tsx.
 */
async function clickRemotePoint(page: Page, remote: { x: number; y: number }): Promise<void> {
  const frame = browserLiveFrame(page);
  const video = browserLiveVideo(page);
  const box = await frame.boundingBox();
  if (!box) throw new Error('clickRemotePoint: the live frame has no bounding box');
  const media = await video.evaluate((el) => {
    const v = el as HTMLVideoElement;
    return { w: v.videoWidth, h: v.videoHeight };
  });
  const scale = Math.min(box.width / media.w, box.height / media.h);
  const drawnW = media.w * scale;
  const drawnH = media.h * scale;
  const offX = box.x + (box.width - drawnW) / 2;
  const offY = box.y + (box.height - drawnH) / 2;
  await page.mouse.click(offX + remote.x * scale, offY + remote.y * scale);
}

// ─────────────────────────────────────────────────────────────────────────────

test.describe('UAT Group C — the live browser panel', () => {
  test('UAT-C0 — with no browser open yet, the panel says so rather than sitting blank', async ({
    page,
  }, testInfo) => {
    test.setTimeout(180_000);

    await openWorkspaceChat(page);
    await waitForConnected(page);
    await selectAgent(page, /Jim/i);
    await bindChatToWorkspace(page);
    await openBrowserButton(page).click();

    const panel = browserLivePanel(page);
    await expect(panel).toBeVisible({ timeout: 15_000 });

    // The moment the panel opens — before any frame can possibly have arrived —
    // it must SAY something. "Blank because nothing is open" and "blank because
    // it broke" must not look the same. The pre-attach branch renders either a
    // spinner with "Connecting to the live browser…" / "Starting live video…"
    // or an error with a Retry; a panel body with neither is the defect.
    const firstText = (await panel.innerText()).trim();
    await note(testInfo, 'panel-text-at-open', firstText);
    expect(
      firstText.length,
      'the live panel rendered NO text at all on open — a user cannot tell "nothing is open yet" from "it broke"',
    ).toBeGreaterThan(0);
    expect(
      /Connecting to the live browser|Starting live video|Waiting for the first frame|Retry|video/i.test(firstText),
      `the panel opened with text that explains nothing about its state: ${JSON.stringify(firstText.slice(0, 300))}`,
    ).toBe(true);

    // And it must not STAY in that state silently: within the first-frame
    // deadline it either shows live video or shows an error with a Retry.
    const video = browserLiveVideo(page);
    const settled = await Promise.race([
      video.waitFor({ state: 'attached', timeout: 120_000 }).then(() => 'video' as const).catch(() => null),
      retryControl(page).first().waitFor({ state: 'visible', timeout: 120_000 }).then(() => 'error' as const).catch(() => null),
    ]);
    const finalText = (await panel.innerText()).trim();
    await note(testInfo, 'panel-text-settled', `verdict=${settled}\n\n${finalText}`);
    expect(
      settled,
      'the panel neither attached live video nor surfaced an error with a Retry — it sat in an ' +
        'indeterminate state, which is the exact silent failure ADR-061 exists to prevent. ' +
        `Panel text was: ${JSON.stringify(finalText.slice(0, 400))}`,
    ).not.toBeNull();

    // Whatever it settled on, the deleted JPEG sink must never be what carried it.
    expect(
      await browserLiveImgFallback(page).count(),
      'the deleted JPEG <img> screencast sink (ADR-061) reappeared',
    ).toBe(0);
  });

  test('UAT-13 — video is smooth, and it is really video', async ({ page }, testInfo) => {
    test.setTimeout(300_000);

    const opened = await openLivePanel(page);
    await note(testInfo, 'open-verdict', `verdict=${opened.verdict} elapsedMs=${opened.elapsedMs} error=${opened.error ?? '(none)'}`);
    if (opened.verdict === 'img') {
      throw new Error(
        'FAIL (P0): the deleted JPEG <img> screencast sink appeared. ADR-061 removed it precisely ' +
          'because a picture swapped 30x/second is indistinguishable from video, so the panel ' +
          'degrades silently and looks completely normal.',
      );
    }
    if (opened.verdict !== 'video') {
      throw new Error(
        `BLOCKED: no live video sink ever attached (verdict=${opened.verdict}, ` +
          `${opened.elapsedMs}ms, panel said: ${opened.error ?? '(nothing)'}). Smoothness cannot ` +
          'be judged without a stream. A refusal in ~1-2s means no browser was ever obtained ' +
          '(memory admission), which is the feature working, not this case failing.',
      );
    }

    const video = browserLiveVideo(page);
    expect(
      await video.evaluate((el) => (el as HTMLVideoElement).srcObject !== null),
      'the <video> sink is mounted but has no MediaStream attached',
    ).toBe(true);

    // A page tall enough to actually scroll at the capture's viewport height —
    // measured on the first run, /dynamic_content does not fill a 684px-tall
    // capture, so "scrolling changed nothing" was the TEST being wrong, not the
    // product. /large is the plan's own host and is several screens deep.
    await navigateLiveBrowser(page, `${HEROKU}/large`);
    await page.waitForTimeout(4_000);
    const loaded = await sampleFrame(video);
    await saveFrame(testInfo, 'frame-loaded.png', loaded);
    const lum = meanLuminance(loaded);
    expect(
      lum,
      `the captured frame is essentially black (mean luminance ${lum.toFixed(1)}) — every ` +
        '"the pixels changed" measurement below would be meaningless noise on a blank picture',
    ).toBeGreaterThan(8);

    // ── Measurement 1: a STATIC page produces few frames, and that is correct. ─
    // Recorded, not asserted. A frame-rate measured on a page that is not moving
    // says nothing about smoothness: an encoder with nothing new to send should
    // send nothing. Reporting it stops the number below being read out of
    // context.
    const idleBefore = await decodedFrames(video);
    const idleT0 = Date.now();
    await page.waitForTimeout(5_000);
    const idleFps = ((await decodedFrames(video)) - idleBefore) / ((Date.now() - idleT0) / 1000);
    await note(testInfo, 'idle-fps', `${idleFps.toFixed(2)} fps while the page is static (recorded for context, not asserted)`);

    // ── Measurement 2: frame rate DURING 10 seconds of continuous scrolling. ─
    // This is the plan's step 3, and it is the only frame-rate number that
    // means anything: the page is changing every frame, so a low count is the
    // pipeline failing to keep up — "recognisably a sequence of stills" — and
    // not simply an encoder with nothing to say.
    const frame = browserLiveFrame(page);
    const box = await frame.boundingBox();
    if (!box) throw new Error('the live frame has no bounding box');
    await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);

    const beforeScroll = await sampleFrame(video);
    const scrollFramesBefore = await decodedFrames(video);
    const scrollT0 = Date.now();
    for (let i = 0; i < 50; i += 1) {
      await page.mouse.wheel(0, 120);
      await page.waitForTimeout(180);
    }
    const scrollSeconds = (Date.now() - scrollT0) / 1000;
    const scrollFps = ((await decodedFrames(video)) - scrollFramesBefore) / scrollSeconds;
    await page.waitForTimeout(700);
    const afterScroll = await sampleFrame(video);

    await saveFrame(testInfo, 'frame-before-scroll.png', beforeScroll);
    await saveFrame(testInfo, 'frame-after-scroll.png', afterScroll);

    const moved = changedCells(beforeScroll, afterScroll);
    await note(
      testInfo,
      'scroll-response',
      `${moved}/64 grid cells changed over ${scrollSeconds.toFixed(1)}s of continuous scrolling; ` +
        `${scrollFps.toFixed(2)} fps decoded while scrolling; mean luminance ${lum.toFixed(1)}`,
    );
    expect(
      moved,
      'scrolling in the panel moved almost nothing in the captured picture — either the wheel ' +
        'input never reached the page, or the capture is not following it',
    ).toBeGreaterThan(4);
    expect(
      scrollFps,
      `the decoder produced ${scrollFps.toFixed(2)} fps while the page was scrolling continuously. ` +
        'Below ~15 fps the panel is recognisably a sequence of stills rather than video — the P0 ' +
        'silent failure this case exists to catch (ADR-061).',
    ).toBeGreaterThan(15);
  });

  test('UAT-14 — a click lands where you clicked, and the page responds', async ({ page }, testInfo) => {
    test.setTimeout(300_000);

    const opened = await openLivePanel(page);
    if (opened.verdict !== 'video') {
      throw new Error(
        `BLOCKED: no live video sink attached (verdict=${opened.verdict}, ${opened.elapsedMs}ms, ` +
          `panel said: ${opened.error ?? '(nothing)'}). Click accuracy cannot be judged without a picture to click on.`,
      );
    }
    const video = browserLiveVideo(page);

    // Anchored to the homepage exactly. A loose /the-internet\.herokuapp\.com/
    // match let a full-file run measure against whatever page the workspace's
    // shared operator tab happened to be left on (observed: /exit_intent), and
    // the click then "missed" for a reason that had nothing to do with the
    // panel. The operator tab persists across chats and across testers.
    await navigateLiveBrowser(page, `${HEROKU}/`);
    await expect(addressBar(page)).toHaveValue(/herokuapp\.com\/?$/, { timeout: 45_000 });
    await page.waitForTimeout(2_000);

    const media = await video.evaluate((el) => {
      const v = el as HTMLVideoElement;
      return { width: v.videoWidth, height: v.videoHeight };
    });
    await note(testInfo, 'remote-viewport', JSON.stringify(media));

    // Step 1 of the case is "take control of the panel". Clicking the frame IS
    // that gesture (BrowserLiveView's takeWheelIfNeeded), so the first click
    // after opening may be spent taking the wheel rather than reaching the
    // page. Take it deliberately, on empty space well below the link list, and
    // confirm the panel agrees before measuring anything.
    const frameBox = await browserLiveFrame(page).boundingBox();
    if (!frameBox) throw new Error('the live frame has no bounding box');
    await page.mouse.click(frameBox.x + frameBox.width / 2, frameBox.y + frameBox.height * 0.92);
    await expect(
      statusChip(page),
      'clicking into the frame did not put the panel into "You\'re driving" — the case cannot ' +
        'measure a click that the panel does not believe it is receiving',
    ).toHaveText(/You're driving/, { timeout: 20_000 });

    // Independent oracle: render the SAME page at the SAME viewport in this
    // browser and read the link's real coordinates (see coordsOnPage's doc).
    const target = await coordsOnPage(page, `${HEROKU}/`, media, 'a[href="/dropdown"]');

    const beforeClick = await sampleFrame(video);
    const clickedAt = Date.now();
    await clickRemotePoint(page, target);

    // The address bar mirrors the ACTIVE TAB's real url (BrowserLiveView's
    // tabState sync), so it is the product's own report of where the click
    // actually took the page — not our guess.
    let landed = true;
    try {
      await expect(addressBar(page)).toHaveValue(/\/dropdown$/, { timeout: 30_000 });
    } catch {
      landed = false;
    }
    if (!landed) {
      // Distinguish the two very different failures the plan cares about:
      // a click that ARRIVED and hit the wrong thing, versus a click that never
      // reached the page at all. Reporting them as one finding would be useless.
      const afterMiss = await sampleFrame(video);
      const reacted = changedCells(beforeClick, afterMiss, 6);
      await saveFrame(testInfo, 'frame-after-missed-click.png', afterMiss);
      throw new Error(
        `the click at remote (${target.x},${target.y}) in a ${media.width}x${media.height} capture ` +
          `did not open /dropdown — the address bar still reads ` +
          `${JSON.stringify(await addressBar(page).inputValue())}. The picture ` +
          `${reacted > 0 ? `DID change (${reacted}/64 cells), so the click reached the page and hit ` +
            'the wrong thing — this is UAT-14\'s named silent failure, the picture sitting behind ' +
            'the real page' : 'did NOT change at all, so the click never reached the page — the input ' +
            'path is broken rather than merely misaligned'}.`,
      );
    }
    const navMs = Date.now() - clickedAt;
    await note(testInfo, 'click-to-navigation', `clicked (${target.x},${target.y}) in a ${media.width}x${media.height} capture; address bar reached /dropdown in ${navMs}ms (includes real network to herokuapp)`);

    // ── Typing latency, measured locally (no network in the loop). ──────────
    // The remote page repaints locally when characters are typed into a field,
    // so the elapsed time is input -> CDP -> repaint -> capture -> WebRTC ->
    // decode, with no site round trip in it. "Typed characters appear in a
    // burst afterwards" is the other silent failure this case names.
    //
    // A native <select> was tried here first and is NOT usable: clicking one
    // opens a popup drawn by the browser's own chrome, which tab capture does
    // not include, so the panel correctly showed no change and the measurement
    // said nothing about latency. Recorded because the same trap will catch the
    // next person.
    await navigateLiveBrowser(page, `${HEROKU}/login`);
    await expect(addressBar(page)).toHaveValue(/\/login$/, { timeout: 45_000 });
    await page.waitForTimeout(3_000);

    const field = await rectOnPage(page, `${HEROKU}/login`, media, '#username');
    await clickRemotePoint(page, { x: Math.round(field.x + field.width / 2), y: Math.round(field.y + field.height / 2) });
    await page.waitForTimeout(1_000);

    const emptyField = await sampleRegion(video, field);
    const typedAt = Date.now();
    await page.keyboard.type('UATUATUAT');

    let respondedMs: number | null = null;
    for (let i = 0; i < 40; i += 1) {
      await page.waitForTimeout(100);
      if (Math.abs((await sampleRegion(video, field)) - emptyField) > 2) {
        respondedMs = Date.now() - typedAt;
        break;
      }
    }
    const typedFrame = await sampleFrame(video);
    await saveFrame(testInfo, 'frame-after-typing.png', typedFrame);
    await note(testInfo, 'local-input-response', respondedMs === null
      ? `no visible change in the username field within 4s of typing (field luminance stayed at ${emptyField.toFixed(1)})`
      : `typed characters appeared in the panel ${respondedMs}ms after the keystrokes`);
    expect(
      respondedMs,
      'typing produced no visible change in the field within 4 seconds — the input is queued ' +
        'somewhere, or is not reaching the page at all, and nothing errors',
    ).not.toBeNull();
    expect(
      respondedMs as number,
      `the panel took ${respondedMs}ms to show a purely local page response. Above ~1s this is the ` +
        '"lags your input by a second or more" failure the plan rates at the same severity as a blank panel.',
    ).toBeLessThan(1_500);
  });

  test('UAT-15 (human half) — taking the wheel and handing it back is visible and matches reality', async ({
    page,
  }, testInfo) => {
    test.setTimeout(300_000);

    const opened = await openLivePanel(page);
    if (opened.verdict !== 'video') {
      throw new Error(
        `BLOCKED: no live video sink attached (verdict=${opened.verdict}, ${opened.elapsedMs}ms, ` +
          `panel said: ${opened.error ?? '(nothing)'}).`,
      );
    }
    const video = browserLiveVideo(page);
    const chip = statusChip(page);
    const hint = page.locator('[data-testid="browser-live-handback-hint"]');

    // Freshly opened, before ANY interaction: the panel advertises how to start.
    await expect(
      chip,
      'a freshly opened panel should tell the operator how to start driving',
    ).toHaveText(/Click to drive|Also viewing/, { timeout: 30_000 });
    const idleLabel = (await chip.innerText()).trim();

    // Driving the omnibox is itself a take-over — worth recording, because the
    // plan's step 1 says "take control" and a tester who types a URL has
    // already done so without clicking anything in the picture.
    await navigateLiveBrowser(page, `${HEROKU}/login`);
    await expect(addressBar(page)).toHaveValue(/\/login$/, { timeout: 45_000 });
    const afterOmniboxLabel = (await chip.innerText()).trim();

    // Hand it back with the advertised escape (Esc — named on screen, which is
    // what satisfies WCAG 2.1.2 No Keyboard Trap).
    //
    // Pressed exactly as it lands after using the omnibox — i.e. with focus
    // still in the address field — and then, if that did nothing, pressed again
    // with the picture focused. Both outcomes are recorded, because "the
    // advertised key works only from one focus position" is a real finding and
    // silently focusing first would hide it.
    await page.keyboard.press('Escape');
    await page.waitForTimeout(1_500);
    const escFromOmniboxReleased = !/You're driving/.test(await chip.innerText());
    if (!escFromOmniboxReleased) {
      await browserLiveFrame(page).focus();
      await page.keyboard.press('Escape');
    }
    await expect(
      chip,
      'after Esc the panel must stop claiming you are driving — neither Esc from the address bar ' +
        'nor Esc with the picture focused released the wheel',
    ).not.toHaveText(/You're driving/, { timeout: 20_000 });
    await expect(hint).toBeHidden();
    const releasedLabel = (await chip.innerText()).trim();
    await note(
      testInfo,
      'esc-release',
      escFromOmniboxReleased
        ? 'Esc released the wheel straight after using the address bar'
        : 'Esc did NOT release the wheel while focus was still in the address bar; it only ' +
          'released after the picture itself was focused. The on-screen hint says "press Esc to ' +
          'stop driving" without saying where focus must be.',
    );

    // Take the wheel again by clicking the picture, and prove the claim is TRUE
    // rather than merely displayed: type, and watch the remote page repaint.
    const media = await video.evaluate((el) => {
      const v = el as HTMLVideoElement;
      return { width: v.videoWidth, height: v.videoHeight };
    });
    const frameBox = await browserLiveFrame(page).boundingBox();
    if (!frameBox) throw new Error('the live frame has no bounding box');
    await page.mouse.click(frameBox.x + frameBox.width / 2, frameBox.y + frameBox.height * 0.92);
    await expect(chip, 'clicking the picture must take the wheel').toHaveText(/You're driving/, {
      timeout: 20_000,
    });
    await expect(hint, 'the handback hint must be visible while you hold the wheel').toBeVisible();
    const drivingHint = (await hint.innerText()).trim();

    const field = await rectOnPage(page, `${HEROKU}/login`, media, '#username');
    await clickRemotePoint(page, {
      x: Math.round(field.x + field.width / 2),
      y: Math.round(field.y + field.height / 2),
    });
    await page.waitForTimeout(800);
    const emptyField = await sampleRegion(video, field);
    await page.keyboard.type('UATUATUAT');
    let typedThrough = false;
    for (let i = 0; i < 40; i += 1) {
      await page.waitForTimeout(100);
      if (Math.abs((await sampleRegion(video, field)) - emptyField) > 2) {
        typedThrough = true;
        break;
      }
    }
    expect(
      typedThrough,
      'the panel says "You\'re driving" but typing changed nothing in the picture — the displayed ' +
        'state and the real state disagree, and the displayed one is the lie',
    ).toBe(true);

    // Release, then take it a third time: the handover must be repeatable.
    await page.keyboard.press('Escape');
    await expect(chip).not.toHaveText(/You're driving/, { timeout: 20_000 });
    await page.mouse.click(frameBox.x + frameBox.width / 2, frameBox.y + frameBox.height * 0.92);
    await expect(chip, 'you must be able to take the wheel back again').toHaveText(/You're driving/, {
      timeout: 20_000,
    });

    await note(
      testInfo,
      'control-states',
      [
        `freshly opened:      ${idleLabel}`,
        `after using omnibox: ${afterOmniboxLabel}`,
        `after Esc:           ${releasedLabel}`,
        `while driving hint:  ${drivingHint}`,
        'after clicking again: You\'re driving (and typing reached the page)',
      ].join(' | '),
    );
  });

  test('UAT-15 (agent half) — after you release, the agent acts with no take-over step and no prompt', async ({
    page,
  }, testInfo) => {
    test.setTimeout(420_000);

    const opened = await openLivePanel(page);
    if (opened.verdict !== 'video') {
      throw new Error(
        `BLOCKED: no live video sink attached (verdict=${opened.verdict}, ${opened.elapsedMs}ms, ` +
          `panel said: ${opened.error ?? '(nothing)'}).`,
      );
    }
    const video = browserLiveVideo(page);
    const chip = statusChip(page);

    await navigateLiveBrowser(page, `${HEROKU}/login`);
    await expect(addressBar(page)).toHaveValue(/\/login$/, { timeout: 45_000 });
    await page.waitForTimeout(3_000);

    // Release the wheel and confirm the panel says so. The picture must be
    // focused first — Esc is handled on the capture surface, so with focus
    // still in the address bar it does nothing (recorded as a finding in the
    // human half of this case).
    await browserLiveFrame(page).focus();
    await page.keyboard.press('Escape');
    await expect(chip).not.toHaveText(/You're driving/, { timeout: 20_000 });

    // A <video> ELEMENT is not a PICTURE. openLivePanel's verdict only means
    // the WebRTC sink attached; `videoWidth` stays 0 until a frame actually
    // DECODES, and the panel then shows "Waiting for the first frame…" over a
    // black rectangle. Every pixel measurement below reduces to
    // `canvas.width = v.videoWidth || 1` — a 1x1 transparent canvas — so with
    // no decoded frame `sampleRegion` returns the SAME number forever and
    // `agentTyped` is mathematically forced to false. The case then fails with
    // "the agent narrated an action it did not perform" about an agent that
    // performed it perfectly, and the real fault (no video ever arrived) is
    // never named.
    //
    // Observed exactly so, 4/4, in CI run 33943602552 (job 101248089931):
    // every failure screenshot shows "Waiting for the first frame…" while the
    // chat shows `browser.type input[name="username"] — "OMNIPUSUAT"  Done`,
    // and the gateway log carries `viewer-14 PLI send failed: the DTLS
    // transport has not started yet` x5 followed by `ingest connection
    // failed — cleared; a fresh capture is required`.
    //
    // No video is a real defect, but it is a defect of the LIVE VIEW, not of
    // the handover this case is about — so it is reported as BLOCKED, with the
    // reason, rather than charged to the agent.
    const media = await video.evaluate((el) => {
      const v = el as HTMLVideoElement;
      const q = (v as HTMLVideoElement & {
        getVideoPlaybackQuality?: () => { totalVideoFrames: number };
      }).getVideoPlaybackQuality?.();
      return { width: v.videoWidth, height: v.videoHeight, frames: q?.totalVideoFrames ?? 0 };
    });
    await note(
      testInfo,
      'capture-before-handover',
      `decoded ${media.frames} frame(s); media ${media.width}x${media.height}`,
    );
    if (media.width === 0 || media.height === 0 || media.frames === 0) {
      const panelText = (await browserLivePanel(page).innerText().catch(() => '')).trim();
      throw new Error(
        'BLOCKED: the live panel never decoded a single frame, so nothing this case measures ' +
          'could ever change — this is a live-view/capture failure, not a handover failure. ' +
          `media=${media.width}x${media.height}, decodedFrames=${media.frames}, ` +
          `panel said: ${JSON.stringify(panelText.slice(0, 300))}`,
      );
    }
    const field = await rectOnPage(page, `${HEROKU}/login`, media, '#username');
    const emptyField = await sampleRegion(video, field);

    // Ask, in ordinary words, without naming a tool. Asking IS the handover:
    // there must be no "take control" step and no permission prompt.
    const input = chatInput(page);
    await input.fill(
      'The page open in your browser is the login form at the-internet.herokuapp.com. ' +
        'Type the word OMNIPUSUAT into the Username field. Do not submit the form.',
    );
    await input.press('Enter');

    // Watch the PANEL, not the chat (the plan is explicit about this), but also
    // record whether the turn ran at all — "the page did not change" means
    // something very different if the agent never got a turn.
    const stop = page.locator('[data-testid="stop-btn"]');
    const turnStarted = await stop.isVisible({ timeout: 30_000 }).catch(() => false);
    let agentTyped = false;
    let turnRunning = turnStarted;
    const deadline = Date.now() + 300_000;
    while (Date.now() < deadline) {
      await page.waitForTimeout(1_000);
      if (Math.abs((await sampleRegion(video, field)) - emptyField) > 2) {
        agentTyped = true;
        break;
      }
      turnRunning = await stop.isVisible().catch(() => false);
      if (turnStarted && !turnRunning) {
        // The turn finished. Give the capture a moment to catch up before
        // concluding the page never moved.
        await page.waitForTimeout(4_000);
        agentTyped = Math.abs((await sampleRegion(video, field)) - emptyField) > 2;
        break;
      }
    }
    const all = await assistantMessages(page).allInnerTexts().catch(() => [] as string[]);
    const transcript = all.slice(-2).join(' /// ').slice(0, 1200);
    await saveFrame(testInfo, 'frame-after-agent-turn.png', await sampleFrame(video));
    await note(
      testInfo,
      'agent-half',
      `turn started: ${turnStarted}; still running at exit: ${turnRunning}; page changed under ` +
        `the agent: ${agentTyped}; last assistant messages: ${JSON.stringify(transcript)}`,
    );
    expect(
      turnStarted,
      'the agent never took a turn at all, so this case did not run — BLOCKED, not failed',
    ).toBe(true);

    // The case is "act on THE CURRENT PAGE — the one the operator is looking at".
    // So the agent finding a different page, or having to navigate to the page
    // the panel is already showing, is a failure of the case even if pixels
    // eventually move: the pixels then moved because the agent went somewhere
    // else, not because it did what was asked. This check exists because the
    // first passing run of this test was exactly that false green.
    const agentSawSomethingElse =
      /isn.t on the login|not on the login|start page|blank page|selector didn.t match/i.test(transcript);
    expect(
      agentSawSomethingElse,
      'the panel was showing the login form, and the agent reported the browser was somewhere ' +
        'else and navigated to reach it. The operator and the agent are not looking at the same ' +
        'tab, so "ask the agent to act on the page in front of you" does not hold. Transcript: ' +
        JSON.stringify(transcript),
    ).toBe(false);

    expect(
      agentTyped,
      'after the operator released the wheel the agent was asked to fill a field and the page ' +
        'never changed. Either the handover did not really happen, or the agent narrated an ' +
        'action it did not perform — the plan says to watch the panel, not the chat.',
    ).toBe(true);

    // The plan names this specific wrong-but-plausible outcome: the agent
    // announcing a transfer of ownership. There is no such thing in this design.
    expect(
      /took control|taking control|take control of the tab|assumed control/i.test(transcript),
      `the agent reported a transfer of control that does not exist in this design: ${JSON.stringify(transcript)}`,
    ).toBe(false);
  });

  test('UAT-16 — a video failure is visible, names a real reason, and offers a Retry', async ({
    page,
  }, testInfo) => {
    test.setTimeout(240_000);

    // There is no sanctioned product control for forcing a video failure (the
    // plan says so in the case's step 3). The closest honest substitute is to
    // break the MEDIA CONNECTION ITSELF in the viewer — WebRTC is simply not
    // available to this page — and see what the panel does. This exercises the
    // real failure path (BrowserWebRTCSession's `pc-create-failed` fallback,
    // src/lib/browserWebRTC.ts), not a test-only branch: the identical thing
    // happens to a real operator whose browser or policy blocks WebRTC.
    await page.addInitScript(() => {
      const boom = function () {
        throw new Error('WebRTC blocked by UAT-16');
      } as unknown as typeof RTCPeerConnection;
      Object.defineProperty(window, 'RTCPeerConnection', { value: boom, configurable: true });
      Object.defineProperty(window, 'webkitRTCPeerConnection', { value: boom, configurable: true });
    });

    const panel = browserLivePanel(page);
    const retry = retryControl(page).first();
    let shown = '';

    // Up to three attempts, but ONLY to step past another tester's login
    // evicting ours (see refreshSession) — never to retry a real outcome.
    for (let attempt = 0; attempt < 3; attempt += 1) {
      await openWorkspaceChat(page);
      await waitForConnected(page);
      await selectAgent(page, /Jim/i);
      await bindChatToWorkspace(page);
      await openBrowserButton(page).click();

      await expect(panel).toBeVisible({ timeout: 15_000 });
      await expect(
        retry,
        'the media connection failed and the panel offered NO Retry. ADR-061 deleted the silent ' +
          'fallback specifically so a failure reaches the operator; a panel with no Retry has ' +
          're-hidden it.',
      ).toBeVisible({ timeout: 90_000 });

      shown = (await panel.innerText()).trim();
      if (!isSessionEviction(shown)) break;
      await note(testInfo, `attempt-${attempt + 1}-evicted`, shown);
      await refreshSession(page);
    }

    await note(testInfo, 'failure-panel-text', shown);
    expect(
      isMemoryRefusal(shown),
      'BLOCKED: the panel never got a browser at all — memory admission refused one before the ' +
        'video path was ever exercised, so this case did not run. That refusal is the feature ' +
        'working; it is not the WebRTC failure surface under test here.',
    ).toBe(false);
    expect(
      isSessionEviction(shown),
      'BLOCKED: every attempt was cut short by another tester\'s login evicting this session, so ' +
        'the WebRTC failure surface was never reached. This is an environment collision, not a result.',
    ).toBe(false);

    // The point of removing the fallback was that the REAL reason reaches the
    // operator. A generic "connection issue" re-hides it.
    expect(
      shown,
      'the panel showed a Retry but no readable explanation of what failed',
    ).toMatch(/video|WebRTC|capture/i);
    expect(
      /connection issue|something went wrong|unknown error/i.test(shown) &&
        !/WebRTC blocked by UAT-16|pc-create-failed|capture|turned off|isn't supported/i.test(shown),
      `the panel reported only a generic reason: ${JSON.stringify(shown.slice(0, 300))}`,
    ).toBe(false);

    // And it must not be showing a picture that is not live.
    expect(
      await browserLiveVideo(page).count(),
      'the panel is reporting a video failure while still mounting a <video> sink',
    ).toBe(0);
    expect(
      await browserLiveImgFallback(page).count(),
      'the panel fell back to the deleted JPEG <img> sink instead of reporting the failure',
    ).toBe(0);

    // Retry must do something honest: recover, or fail again WITH a reason.
    await retry.click();
    await page.waitForTimeout(3_000);
    const afterRetry = (await panel.innerText()).trim();
    await note(testInfo, 'after-retry-panel-text', afterRetry);
    const recovered = (await browserLiveVideo(page).count()) > 0;
    const stillReporting = (await retryControl(page).count()) > 0;
    expect(
      recovered || stillReporting,
      'after Retry the panel neither recovered video nor kept reporting a failure — it went quiet, ' +
        'which is the blank-panel outcome this case exists to rule out',
    ).toBe(true);
  });
});
