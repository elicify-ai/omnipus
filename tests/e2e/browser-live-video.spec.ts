/**
 * browser-live-video.spec.ts — proves the live-browser WebRTC video/audio/
 * input feature (ADR-038/039/040/041/047) actually works, end-to-end,
 * against a real deployed gateway + a real agent-driven Chrome tab.
 *
 * Why this exists: as of this writing, zero of the other Playwright specs
 * touch the live browser or WebRTC. No test anywhere asserts video pixels,
 * audio content, or input latency — the only prior proof this ever worked
 * was a single manual pass (commit 687c7c6e: "video 1278x576, audio RMS
 * 0.3539 vs 0.3536 theoretical, pixel-exact drive") that was never
 * committed as a repeatable test. This spec is that repeatable test.
 *
 * Operator's acceptance bar: a screenshot of the <video> element at two
 * different moments showing DIFFERENT content. A single screenshot of a
 * frozen or black frame is NOT proof — see the honesty gate below.
 *
 * Strategy for a deterministic, network-independent motion+audio source:
 * browser_navigate only allows http(s) (file/javascript/data/chrome schemes
 * are hard-blocked — pkg/tools/browser/manager.go's blockedSchemes) and SSRF
 * blocks private IPs (pkg/security/ssrf.go) — so navigating to a local
 * data: URI or to an arbitrary localhost port is not an option, and relying
 * on a third-party website for guaranteed continuous motion+audio is
 * unreliable (content drift, consent banners, bot detection). Instead this
 * spec has the agent serve_web a small, deterministic, self-authored HTML
 * fixture from ITS OWN workspace and browser_navigate to the resulting
 * /preview/<agent>/<token>/ URL — that origin is on the SAME allow-listed
 * "gateway origin" exception the browser tool's SSRF checker carries
 * specifically for this purpose (pkg/agent/loop.go: `browserSSRF :=
 * al.ssrfChecker.CloneWithGatewayOrigin("localhost", cfg.Gateway.Port)`;
 * pkg/security/ssrf.go's isAllowedGatewayOrigin treats 127.0.0.1/::1 as
 * "localhost" equivalents). The fixture itself:
 *   - continuously repaints a top-half color block from Date.now() (proves
 *     genuine motion — a static page could never produce this)
 *   - starts a 440Hz sine tone (WebAudio oscillator, gain 0.2) on the first
 *     trusted click (proves genuine audio)
 *   - toggles a bottom-half color block on every click (used for the
 *     input-latency measurement: click via the live view's input path and
 *     time how long the color flip takes to appear in the captured video)
 *
 * Honesty requirement (non-negotiable, per the operator's brief): if this
 * environment cannot produce real WebRTC video (e.g. it falls back to the
 * JPEG <img> picture-mode sink), this spec MUST fail loudly and specifically
 * naming what was missing — never silently pass against the JPEG fallback
 * and call it "video". See the sink-detection block below.
 */

import * as fs from 'fs';
import * as path from 'path';
import { expect, type Page, type Locator } from '@playwright/test';
import { test } from './fixtures/console-errors';
import {
  chatInput,
  assistantMessages,
  selectAgent,
  watchLiveButton,
  browserLivePanel,
  browserLiveFrame,
  browserLiveVideo,
  waitForLiveSink,
} from './fixtures/selectors';

const OMNIPUS_HOME =
  process.env.OMNIPUS_HOME ||
  (process.env.HOME ? path.join(process.env.HOME, '.omnipus') : '/tmp/omnipus-e2e-test');

// ── Deterministic motion+audio fixture (see module doc for the "why") ──────
//
// Design notes:
//   - #motionLayer (top half) cycles a saturated HSL background via a
//     compositor-driven CSS @keyframes animation (omnipusHueShift, 2s linear
//     infinite — see the <style> block below). It deliberately does NOT use
//     requestAnimationFrame: rAF is throttled, and in an occluded tab stopped
//     outright, by Chrome, so a rAF-painted background freezes whenever the
//     captured tab is not foregrounded and the spec then fails as "video is
//     frozen" while the pipeline is actually healthy. HSL(_, 90%, 45%) never
//     degenerates to near-black or near-white regardless of hue, so a "not
//     blank" guard on luminance is meaningful (a hardcoded/frozen-frame bug
//     could not fake this — it requires the video pipeline to actually
//     deliver new frames over time).
//   - #clickLayer (bottom half) starts dark grey (#222) and flips to bright
//     green (#0f0) on the FIRST click, then back to grey on the second, etc.
//     Used as the input-latency detector: a huge, unambiguous color jump
//     that only a real dispatched click can produce.
//   - The oscillator only starts on the first click (a trusted, CDP-
//     dispatched Input.dispatchMouseEvent from browser_click / chromedp.Click
//     — NOT a JS .click() call — Chromium's autoplay-gesture heuristic
//     accepts CDP-dispatched input as trusted) so audio is flowing well
//     before the test ever opens the live view.
const FIXTURE_HTML = `<!doctype html>
<html>
<head>
<meta charset="utf-8">
<title>Omnipus E2E Live Probe</title>
<style>
  html, body { margin: 0; padding: 0; width: 100%; height: 100%; overflow: hidden; }
  /* Motion is driven by a CSS @keyframes animation, NOT requestAnimationFrame.
     rAF is throttled — and in a fully backgrounded/occluded tab, STOPPED — by
     Chrome, so a rAF-painted background stops changing whenever the captured
     tab is not foregrounded. That produced a real false failure on the
     2026-07-28 real-Chrome run: two samples 1.5s apart came back BYTE-IDENTICAL
     ("video is frozen") even though the WebRTC pipeline was healthy and
     delivering frames — the PAGE had simply stopped repainting. A CSS
     animation is driven by the compositor and keeps advancing regardless of
     tab focus, so "the picture changes" tests the video path rather than
     Chrome's background-tab scheduling policy. */
  #motionLayer {
    position: fixed; top: 0; left: 0; width: 100%; height: 50%;
    display: flex; align-items: center; justify-content: center;
    color: #fff; font: bold 40px monospace;
    animation: omnipusHueShift 2s linear infinite;
  }
  @keyframes omnipusHueShift {
    0%   { background-color: hsl(0,   90%, 45%); }
    20%  { background-color: hsl(72,  90%, 45%); }
    40%  { background-color: hsl(144, 90%, 45%); }
    60%  { background-color: hsl(216, 90%, 45%); }
    80%  { background-color: hsl(288, 90%, 45%); }
    100% { background-color: hsl(360, 90%, 45%); }
  }
  #clickLayer {
    position: fixed; top: 50%; left: 0; width: 100%; height: 50%;
    background: #222222;
    display: flex; align-items: center; justify-content: center;
    color: #fff; font: bold 28px monospace;
  }
</style>
</head>
<body>
  <div id="motionLayer">motion</div>
  <div id="clickLayer">clicks:0</div>
  <script>
    (function () {
      // The BACKGROUND animation is pure CSS (see @keyframes omnipusHueShift)
      // so it survives background-tab throttling. This timer only updates the
      // readable timestamp overlay, which is diagnostic sugar for a human
      // looking at the attached frames — no assertion depends on it, so its
      // being throttled in a background tab cannot fail the test.
      function paintClock() {
        var el = document.getElementById('motionLayer');
        if (el) el.textContent = new Date().toISOString().slice(11, 23);
      }
      paintClock();
      setInterval(paintClock, 100);

      var clicks = 0;
      var audioCtx = null, osc = null, gain = null;
      function ensureAudio() {
        if (audioCtx) return;
        var AC = window.AudioContext || window.webkitAudioContext;
        audioCtx = new AC();
        osc = audioCtx.createOscillator();
        gain = audioCtx.createGain();
        osc.type = 'sine';
        osc.frequency.value = 440;
        gain.gain.value = 0.2;
        osc.connect(gain).connect(audioCtx.destination);
        osc.start();
      }
      document.addEventListener('click', function () {
        ensureAudio();
        if (audioCtx.state === 'suspended') { audioCtx.resume(); }
        clicks += 1;
        var layer = document.getElementById('clickLayer');
        layer.style.backgroundColor = (clicks % 2 === 1) ? '#00ff00' : '#222222';
        layer.textContent = 'clicks:' + clicks;
      });
    })();
  </script>
</body>
</html>
`;

/**
 * Resolve the active workspace ID so the fixture can be placed inside its
 * work/ directory (the ADR-046 P1 per-turn filesystem re-root every CoreTeam
 * agent's tools are confined to). Duplicated locally per this suite's
 * established "shared harness pattern, not extracted to a fixture"
 * convention — see media.spec.ts's identically-named/identically-reasoned
 * helper.
 */
async function resolveWorkspaceId(page: Page): Promise<string> {
  try {
    const resp = await page.request.get(`${process.env.OMNIPUS_URL || 'http://localhost:6060'}/api/v1/workspaces`, {
      failOnStatusCode: false,
    });
    if (resp.ok()) {
      const workspaces = (await resp.json()) as Array<{
        id: string;
        is_default?: boolean;
        status?: string;
      }>;
      if (Array.isArray(workspaces) && workspaces.length > 0) {
        const def = workspaces.find((w) => w.is_default === true);
        if (def?.id) return def.id;
        const first = workspaces.find((w) => !w.status || w.status === 'active') ?? workspaces[0];
        if (first?.id) return first.id;
      }
    }
  } catch {
    // Network error — fall through to disk scan.
  }

  const workspacesDir = path.join(OMNIPUS_HOME, 'workspaces');
  if (fs.existsSync(workspacesDir)) {
    const wsDirs = fs
      .readdirSync(workspacesDir, { withFileTypes: true })
      .filter((e) => e.isDirectory())
      .map((e) => e.name);
    if (wsDirs.length > 0) return wsDirs[0];
  }

  throw new Error(
    'BLOCKED: could not resolve an active workspace ID via GET /api/v1/workspaces or a disk ' +
      `scan of ${workspacesDir}. serve_web needs a workspace-scoped work/ directory to serve ` +
      'from — see resolveWorkspaceId in this file.',
  );
}

/**
 * Wait for any follow-up LLM call after the current assistant message
 * settles. Mirrors media.spec.ts / chat.spec.ts's identically-named helper:
 * this prompt forces THREE sequential tool calls (serve_web, browser_navigate,
 * browser_click) and a single LLM completion segment can end (done:true)
 * between any of them, briefly flipping isStreaming false — without this
 * guard a later assertion's budget could start from that premature blip
 * instead of real turn completion.
 */
async function waitForTurnFullyDone(page: Page, gapMs = 8_000): Promise<void> {
  const stopBtn = page.locator('[data-testid="stop-btn"]');
  try {
    await expect(stopBtn).toBeVisible({ timeout: gapMs });
    await expect(stopBtn).not.toBeVisible({ timeout: 180_000 });
  } catch {
    // Stop button did not reappear within gapMs — the turn is fully done.
  }
}

interface RegionSample {
  r: number;
  g: number;
  b: number;
}

interface FrameSample {
  dataUrl: string;
  top: RegionSample;
  bottom: RegionSample;
}

/**
 * Draws the CURRENT decoded video frame to an offscreen canvas and returns
 * the average RGB of a central band of each half (top = motion region,
 * bottom = click-flip region), plus a PNG data URL of the whole frame for
 * human-inspectable evidence attachments. Sampling a central band (not the
 * full half) avoids any edge/letterbox pixels the object-contain fit could
 * introduce.
 */
async function sampleFrame(video: Locator): Promise<FrameSample> {
  return video.evaluate((el) => {
    const v = el as HTMLVideoElement;
    const canvas = document.createElement('canvas');
    canvas.width = v.videoWidth || 1;
    canvas.height = v.videoHeight || 1;
    const ctx = canvas.getContext('2d');
    if (!ctx) throw new Error('2D canvas context unavailable');
    ctx.drawImage(v, 0, 0, canvas.width, canvas.height);
    function avg(x0: number, y0: number, w: number, h: number) {
      const data = ctx!.getImageData(x0, y0, Math.max(1, w), Math.max(1, h)).data;
      let r = 0;
      let g = 0;
      let b = 0;
      let n = 0;
      for (let i = 0; i < data.length; i += 4) {
        r += data[i];
        g += data[i + 1];
        b += data[i + 2];
        n += 1;
      }
      return { r: r / n, g: g / n, b: b / n };
    }
    const w = canvas.width;
    const h = canvas.height;
    const top = avg(Math.floor(w * 0.25), Math.floor(h * 0.1), Math.floor(w * 0.5), Math.floor(h * 0.2));
    const bottom = avg(Math.floor(w * 0.25), Math.floor(h * 0.65), Math.floor(w * 0.5), Math.floor(h * 0.2));
    return { dataUrl: canvas.toDataURL('image/png'), top, bottom };
  });
}

function colorDistance(a: RegionSample, b: RegionSample): number {
  return Math.abs(a.r - b.r) + Math.abs(a.g - b.g) + Math.abs(a.b - b.b);
}

function luminance(c: RegionSample): number {
  return 0.299 * c.r + 0.587 * c.g + 0.114 * c.b;
}

function dataUrlToPngBuffer(dataUrl: string): Buffer {
  const base64 = dataUrl.split(',')[1] ?? '';
  return Buffer.from(base64, 'base64');
}

test.beforeEach(async ({ page }) => {
  await page.goto('/');
});

test(
  'live browser view streams genuinely playing video with real audio and realtime input',
  async ({ page }, testInfo) => {
    // Budget (worst-case ceiling, same accounting discipline as media.spec.ts):
    //   Agent picker + Jim selection + input visibility ...................... ~35s
    //   assistantMessages toHaveCount ceiling ................................ 240s
    //   waitForTurnFullyDone: gapMs(8s) + follow-up-call ceiling(180s) ....... 188s
    //   LIVE_PROBE_READY text assertion ........................................ 15s
    //   Watch-live click + panel visible ....................................... 30s
    //   sink detection poll (video-vs-JPEG-vs-neither honesty gate) ........... 90s
    //   motion/frame-count/rVFC evidence (waits ~1.5s each) .................... 20s
    //   audio RMS sampling (~800ms sampling window) ........................... 15s
    //   input-latency poll ceiling ............................................. 5s
    //   a11y scan ............................................................. 10s
    // Worst-case total: 35+240+188+15+30+90+20+15+5+10 = 648s.
    // Budget: 648s ceiling + ~11% margin for CI scheduling jitter = 720s (12 min).
    // (Was 900s while this spec opened the live browser up front as an
    // issue-#671 mitigation. Removing that step does not make real runs
    // slower — it moves the cold Chrome launch back inside the 240s agent
    // turn, which is where it was always paid before the mitigation existed.)
    test.setTimeout(720_000);

    // ── Setup: seed the deterministic fixture directly on disk (no LLM
    // authoring involved — deterministic content, matching media.spec.ts's
    // send_file fixture pattern) ───────────────────────────────────────────
    const workspaceId = await resolveWorkspaceId(page);
    const relDir = `e2e-live-probe-${Date.now()}`;
    const workDir = path.join(OMNIPUS_HOME, 'workspaces', workspaceId, 'work', relDir);
    fs.mkdirSync(workDir, { recursive: true });
    fs.writeFileSync(path.join(workDir, 'index.html'), FIXTURE_HTML, 'utf-8');

    // Jim — the generalist doer with browser_navigate/browser_click/serve_web
    // all allow-policied (pkg/coreagent/core.go) — same rationale media.spec.ts
    // and subagent.spec.ts already rely on (Mia's "guide" persona routes
    // browser tasks away rather than executing them).
    await selectAgent(page, /Jim/i);

    const input = chatInput(page);
    await expect(input).toBeVisible({ timeout: 10_000 });

    const panel = browserLivePanel(page);
    // The panel's omnibox. Its value is fed from the CAPTURED tab's own
    // metadata (BrowserLiveView's urlInput ← the browser_tabs frame), so it is
    // the honest answer to "which page is this panel actually showing?" — used
    // below both as the "the operator's tab now exists" signal and as the
    // diagnosis when no frame ever paints.
    const addressBar = page.getByRole('textbox', { name: 'Address bar' });

    // ── The operator's tab set is deliberately left EMPTY here ───────────
    //
    // This is the regression guard for issue #671, and the reason it is an
    // absence rather than a step.
    //
    // A browser has two tab sets: the operator's workspace-owned one and each
    // chat's own. The live panel used to be hard-wired to the operator's,
    // while an agent's browser_* tools drive the chat's — so whenever the
    // operator's set was empty, the agent browsed in a tab the panel
    // structurally could not look at, and "Watch live" showed a perfectly
    // painted /browser-start page with no error of any kind. Measured
    // top-band luminance was 22.7 on both a 15s and a 45s budget, identical to
    // the decimal, because nothing was ever starting slowly: it was the wrong
    // tab, rendered correctly.
    //
    // This test used to paper over that by opening the panel once up front to
    // create the operator's set, which made the agent's navigate divert onto a
    // tab the panel already watched. That mitigation is removed: the panel now
    // resolves the tab set from the chat it was opened for
    // (BrowserManager.PanelTabSetID), so panel and agent can no longer
    // diverge. Leaving the set empty means this spec exercises the exact shape
    // that used to fail — a fresh workspace whose first browse is the agent's
    // own — and the video assertions below are what would catch it coming
    // back, including in the capture binding, which has no unit-level proof
    // because it needs a real Chromium.

    const countBefore = await assistantMessages(page).count();
    await test.step('drive the agent to serve+navigate+click a deterministic motion+audio page', async () => {
      await input.fill(
        [
          'Do these three steps in order, using the tools yourself — do not delegate, do not call any other tool:',
          `1) Call serve_web with path "${relDir}" and no command parameter.`,
          '2) Call browser_navigate with the exact url the serve_web tool result returned.',
          '3) Call browser_click with selector "body" exactly once.',
          'After step 3 finishes, reply with exactly: LIVE_PROBE_READY',
        ].join(' '),
      );
      await input.press('Enter');

      await expect(assistantMessages(page)).toHaveCount(countBefore + 1, { timeout: 240_000 });
      await waitForTurnFullyDone(page, 8_000);

      const lastMessage = assistantMessages(page).last();
      await expect(lastMessage).toContainText('LIVE_PROBE_READY', { timeout: 15_000 });
    });

    // ── Open the live view ────────────────────────────────────────────────
    await test.step('open the live view via "Watch live"', async () => {
      const btn = watchLiveButton(page);
      await expect(btn).toBeVisible({ timeout: 15_000 });
      await btn.click();
      await expect(panel).toBeVisible({ timeout: 15_000 });
    });

    // ── HONESTY GATE (non-negotiable) ─────────────────────────────────────
    // WebRTC cold start can legitimately take up to ~30s
    // (BrowserWebRTCSession's firstAnswerTimeoutMs) plus one 15s automatic
    // retry plus a further attempt — 90s is a generous ceiling before
    // concluding this environment genuinely cannot produce live video. If
    // only the JPEG <img> fallback ever appears, or NEITHER sink appears,
    // this test fails loudly and specifically rather than silently treating
    // the JPEG picture-mode path as "video" — see this file's module doc for
    // why that failure mode is exactly what this spec exists to eliminate.
    const sink = await waitForLiveSink(page, 90_000);
    if (sink === 'img') {
      throw new Error(
        'BLOCKED: the live view fell back to the JPEG <img> picture-mode sink ' +
          '(data-testid="browser-live-img") — the WebRTC <video> sink ' +
          '(data-testid="browser-live-video") never appeared within 90s. This ' +
          'environment cannot currently produce live video, so this test CANNOT ' +
          'prove the video/audio feature works (per the operator\'s acceptance bar, a ' +
          'JPEG-fallback screenshot is not proof of video). Likely causes: (a) the ' +
          'agent\'s managed browser is not a full-Chrome build on linux — WebRTC ' +
          'tabCapture requires linux + full "chrome" (not chrome-headless-shell) — see ' +
          'pkg/tools/browser/capability.go ClassifyVideoCapability; (b) ' +
          'tools.browser.webrtc_enabled=false; (c) ' +
          'tools.browser.capture_shared_context=false (ADR-048 condition 3); or (d) a ' +
          'real capture-path regression. Check gateway logs for ' +
          '"browser_webrtc_state"/capability-classification reasons.',
      );
    }
    if (sink === null) {
      throw new Error(
        'BLOCKED: neither the WebRTC <video> sink nor the JPEG <img> fallback ever ' +
          'appeared within 90s of opening the live panel — the live view never received ' +
          'a single frame at all. This is a capture-path failure (screencast/tabCapture ' +
          'attach), not a WebRTC-specific one — check gateway logs for the ' +
          'browser_attach/browser_status round trip for this session.',
      );
    }
    const video = browserLiveVideo(page);
    await expect(video).toBeVisible();

    // Every downstream assertion below now runs against a CONFIRMED live
    // WebRTC <video> sink with a real srcObject — never the JPEG fallback.
    const hasLiveStream = await video.evaluate((el) => (el as HTMLVideoElement).srcObject !== null);
    expect(hasLiveStream, 'browser-live-video must have a real MediaStream srcObject attached').toBe(true);

    // ── Evidence 1: genuine motion — two frames, sampled ~1.5s apart, MUST
    // differ, and neither may be blank/black (guards against "different
    // noise on a black screen"). ──────────────────────────────────────────
    let sampleA!: FrameSample;
    let sampleB!: FrameSample;
    await test.step('evidence: video pixels genuinely change over time (not frozen/black)', async () => {
      // Wait for the FIRST genuinely decoded frame before starting the clock.
      //
      // A non-null srcObject only means the MediaStream is attached — it does
      // NOT mean a frame has been decoded and painted. On a cold capture the
      // first decoded frame lands a little later, so sampling immediately
      // yields a legitimately black frame and fails "must not be
      // near-black/blank" for a reason that has nothing to do with whether
      // video works. That is exactly what happened on the first attempt of
      // the 2026-07-28 real-Chrome run (attempt 1 black at t0, retry green) —
      // a fixed sample point racing an async pipeline, the same anti-pattern
      // CLAUDE.md calls out (wait ON THE CONDITION, never on a fixed delay).
      //
      // Polling here does NOT weaken the assertions below: this only
      // establishes "streaming has actually begun". Every real claim — the
      // frame is not black, not a white wash, and CHANGES between t0 and t1 —
      // is still asserted afterwards, and the poll has a hard budget so a
      // genuinely black stream still fails loudly rather than hanging.
      //
      // The poll must clear the assertion floor by a MARGIN, not equal it.
      //
      // This loop used to exit on the same number the assertion below uses
      // (20), so by construction it stopped at the first sample that cleared
      // the floor by a hair and then asserted that floor with zero headroom —
      // a frame still ramping up exits at ~20.x and any later measurement sits
      // on the boundary. That is how a run failed at 19.2 and passed on retry:
      // not a broken stream, a readiness check calibrated to the exact point
      // it was supposed to move past.
      //
      // 40 is safe as a readiness bar, COMPUTED rather than guessed. The
      // motion layer animates background-color between six HSL(h, 90%, 45%)
      // keyframe stops; CSS interpolates that in RGB, and luminance is a
      // linear function of RGB, so the animation's minimum luminance is the
      // minimum over the STOPS — no interpolated frame can dip below them:
      //
      //   hue    0 → 73.2     hue  216 → 83.5
      //   hue   72 → 182.1    hue  288 → 84.4
      //   hue  144 → 142.1    hue  360 → 73.2
      //
      // So the darkest frame the fixture can produce is 73.2, and the sampled
      // band also contains the white clock text, which only raises the mean.
      // 40 therefore sits far below every real frame while sitting twice the
      // near-black floor: it cannot let a genuinely black stream through, and
      // it stops handing the assertions a frame balanced on their threshold.
      const NEAR_BLACK_FLOOR = 20;
      const FIRST_FRAME_READY_LUMINANCE = 40;
      // 45s, DERIVED — not the guess it originally was.
      //
      // History worth keeping, because the wrong lesson is easy to draw. This
      // was raised 15s → 45s on the theory that a cold start needed longer.
      // That theory was disproven by its own evidence: CI reported top-band
      // luminance 22.7 under BOTH budgets, identical to the decimal. A stream
      // that was slowly starting would have climbed; this one never moved,
      // because the panel was watching the wrong tab entirely (issue #671 —
      // see the empty-operator-set note near the top of this test for the full
      // mechanism). Waiting is not what fixed it and never could have been.
      //
      // 45s survives anyway, for a reason that can be checked rather than
      // guessed: it is exactly BrowserLiveView's own FIRST_FRAME_TIMEOUT_MS
      // (DEFAULT_FIRST_ANSWER_TIMEOUT_MS 30s + 15s, src/components/browser/
      // BrowserLiveView.tsx). That is the deadline the PRODUCT uses before it
      // admits "no video received" and shows its Retry error. Matching it
      // means this poll and the panel reach a verdict at the same moment — so
      // when the poll gives up, the panel's own error text has just become
      // available and is read into the failure message below, instead of the
      // test declaring failure while the panel is still legitimately waiting.
      // Waiting longer for a stream to start can never let a black stream
      // through; the bar stays at 40 and does the judging.
      const FIRST_FRAME_BUDGET_MS = 45_000;
      const firstFrameDeadline = Date.now() + FIRST_FRAME_BUDGET_MS;
      let firstFrame = await sampleFrame(video);
      while (luminance(firstFrame.top) < FIRST_FRAME_READY_LUMINANCE && Date.now() < firstFrameDeadline) {
        await page.waitForTimeout(250);
        firstFrame = await sampleFrame(video);
      }

      // Budget exhausted without a painted frame is its own finding, and it
      // deserves to be reported as itself. Falling through to the generic
      // "frame at t0 must not be near-black" below describes the symptom but
      // hides that the stream had the full budget and never got there.
      //
      // And "no painted frame" is not one failure, it is at least three, which
      // a bare luminance number cannot tell apart: a dead capture, a WebRTC
      // failure the panel has already diagnosed, or a healthy capture of the
      // WRONG TAB. The last one is what actually happened here and it read as
      // an unremarkable dark number for four CI runs. Both of the surfaces
      // that CAN tell them apart are free to read, so read them — the panel's
      // own error text (its waiting overlay promotes to the real reason once
      // FIRST_FRAME_TIMEOUT_MS elapses, which this budget is matched to) and
      // the omnibox, which names the page the capture is actually showing.
      let firstFrameDiagnosis = '';
      if (luminance(firstFrame.top) < FIRST_FRAME_READY_LUMINANCE) {
        const capturedUrl = await addressBar.inputValue().catch(() => '(unavailable)');
        const overlayText = await page
          .locator('[data-testid="browser-live-waiting-overlay"]')
          .textContent()
          .catch(() => null);
        await testInfo.attach('first-frame-never-painted.png', {
          body: dataUrlToPngBuffer(firstFrame.dataUrl),
          contentType: 'image/png',
        });
        firstFrameDiagnosis =
          `. The panel is capturing "${capturedUrl}" — if that is not the /preview/ URL the ` +
          'agent navigated to, the capture is bound to a DIFFERENT TAB and no wait would ever ' +
          'have helped — that is issue #671 regressing; see the empty-operator-set note at the ' +
          'top of this test). Panel state: ' +
          `"${overlayText?.trim() ?? '(no waiting overlay — the panel believes it has frames)'}"`;
      }
      expect(
        luminance(firstFrame.top),
        `the live stream produced no painted frame within ${FIRST_FRAME_BUDGET_MS}ms — ` +
          `top-band luminance stayed at ${luminance(firstFrame.top).toFixed(1)}, below the ` +
          `${FIRST_FRAME_READY_LUMINANCE} readiness bar (the fixture's darkest real frame is 73.2)` +
          firstFrameDiagnosis,
      ).toBeGreaterThanOrEqual(FIRST_FRAME_READY_LUMINANCE);

      sampleA = firstFrame;
      await page.waitForTimeout(1_500);
      sampleB = await sampleFrame(video);

      await testInfo.attach('frame-t0.png', { body: dataUrlToPngBuffer(sampleA.dataUrl), contentType: 'image/png' });
      await testInfo.attach('frame-t1.png', { body: dataUrlToPngBuffer(sampleB.dataUrl), contentType: 'image/png' });

      // Not blank/black: HSL(_, 90%, 45%) is always a vivid, mid-lightness
      // color regardless of hue — luminance well above a near-black floor.
      expect(
        luminance(sampleA.top),
        `frame at t0 must not be near-black/blank (measured ${luminance(sampleA.top).toFixed(1)})`,
      ).toBeGreaterThan(NEAR_BLACK_FLOOR);
      expect(
        luminance(sampleB.top),
        `frame at t1 must not be near-black/blank (measured ${luminance(sampleB.top).toFixed(1)})`,
      ).toBeGreaterThan(NEAR_BLACK_FLOOR);
      // Not a uniform white wash either (defense-in-depth against a
      // different kind of degenerate "frame").
      expect(sampleA.top.r).toBeLessThan(250);
      expect(sampleB.top.r).toBeLessThan(250);

      // Genuinely DIFFERENT content — the operator's core acceptance bar.
      // The CSS animation sweeps a full 360 degrees every 2s (180 deg/s), so
      // the 1.5s wait above shifts hue by ~270 degrees: a large, unmistakable
      // RGB move. (This previously cited ~150 degrees, derived from a
      // requestAnimationFrame formula, hue = Date.now()/10 % 360, that no
      // longer exists — the assertion threshold never depended on the figure,
      // but the stated reasoning was describing deleted code.)
      expect(
        colorDistance(sampleA.top, sampleB.top),
        `top-region color must differ between t0 (${JSON.stringify(sampleA.top)}) and ` +
          `t1 (${JSON.stringify(sampleB.top)}) — identical color across 1.5s means the ` +
          'video is frozen (or a hardcoded/static frame), not genuinely playing',
      ).toBeGreaterThan(30);
    });

    // ── Evidence 2: a SECOND, independent "is it really playing" signal —
    // getVideoPlaybackQuality().totalVideoFrames must increase over time. ──
    await test.step('evidence: totalVideoFrames increases (second independent playing signal)', async () => {
      const framesBefore = await video.evaluate((el) => {
        const v = el as HTMLVideoElement & { getVideoPlaybackQuality?: () => { totalVideoFrames: number } };
        return v.getVideoPlaybackQuality ? v.getVideoPlaybackQuality().totalVideoFrames : -1;
      });
      expect(framesBefore, 'getVideoPlaybackQuality() must be supported by this Chromium build').toBeGreaterThanOrEqual(0);

      // Poll rather than a single 1.5s sample. A recapture / late answer can
      // stall decode for a beat (CI ui-heavy 2026-08-16: evidence 1 color
      // change passed, then totalVideoFrames sat flat at 9–21). If the
      // stream resumes, frames climb inside this budget. A permanently
      // dead ingest still fails.
      const FRAME_BUDGET_MS = 8_000;
      const frameDeadline = Date.now() + FRAME_BUDGET_MS;
      let framesAfter = framesBefore;
      while (framesAfter <= framesBefore && Date.now() < frameDeadline) {
        await page.waitForTimeout(250);
        framesAfter = await video.evaluate((el) => {
          const v = el as HTMLVideoElement & { getVideoPlaybackQuality?: () => { totalVideoFrames: number } };
          return v.getVideoPlaybackQuality ? v.getVideoPlaybackQuality().totalVideoFrames : -1;
        });
      }
      expect(
        framesAfter,
        `totalVideoFrames must increase within ${FRAME_BUDGET_MS}ms of real playback (before=${framesBefore}, after=${framesAfter}) ` +
          '— a stalled/frozen decode would leave this flat',
      ).toBeGreaterThan(framesBefore);
    });

    // ── Evidence 3: audio is flowing — pull the audio track off the SAME
    // srcObject MediaStream, run it through a WebAudio AnalyserNode, and
    // assert a SUSTAINED non-zero signal (not a single lucky sample). Muted
    // <video> playback (autoplay-safe default) still decodes audio — this
    // asserts on the track/analyser, never on speaker output. ─────────────
    await test.step('evidence: audio track carries a sustained non-zero signal', async () => {
      const audio = await video.evaluate(async (el) => {
        const v = el as HTMLVideoElement;
        const stream = v.srcObject as MediaStream | null;
        const audioTracks = stream ? stream.getAudioTracks() : [];
        if (audioTracks.length === 0) return { hasAudioTrack: false, samples: [] as number[] };

        const win = window as unknown as {
          AudioContext: typeof AudioContext;
          webkitAudioContext?: typeof AudioContext;
        };
        const AC = win.AudioContext || win.webkitAudioContext;
        const audioCtx = new AC();
        if (audioCtx.state === 'suspended') {
          try {
            await audioCtx.resume();
          } catch {
            // best-effort — sampling below still reflects reality either way
          }
        }
        const audioOnlyStream = new MediaStream([audioTracks[0]]);
        const source = audioCtx.createMediaStreamSource(audioOnlyStream);
        const analyser = audioCtx.createAnalyser();
        analyser.fftSize = 2048;
        source.connect(analyser);
        const buf = new Float32Array(analyser.fftSize);
        const samples: number[] = [];
        for (let i = 0; i < 8; i += 1) {
          await new Promise((resolve) => setTimeout(resolve, 100));
          analyser.getFloatTimeDomainData(buf);
          let sumSq = 0;
          for (let j = 0; j < buf.length; j += 1) sumSq += buf[j] * buf[j];
          samples.push(Math.sqrt(sumSq / buf.length));
        }
        return { hasAudioTrack: true, samples };
      });

      expect(audio.hasAudioTrack, 'the live MediaStream must carry an audio track').toBe(true);
      expect(audio.samples.length).toBe(8);

      const audible = audio.samples.filter((rms) => rms > 0.01);
      testInfo.annotations.push({
        type: 'audio-rms-samples',
        description: JSON.stringify(audio.samples),
      });
      expect(
        audible.length,
        `audio must show a SUSTAINED signal (>0.01 RMS) across most of the 8 samples taken over 800ms ` +
          `— got samples=${JSON.stringify(audio.samples.map((n) => n.toFixed(4)))}. A single spike would ` +
          'not satisfy this, ruling out transient noise as a false positive.',
      ).toBeGreaterThanOrEqual(6);
    });

    // ── Evidence 4: input is realtime — dispatch a real click through the
    // live panel and measure the round trip to an observable pixel change
    // in the CAPTURED video (agent's tab → CDP/DC input → target page click
    // handler → tabCapture → WebRTC encode/relay/decode → our repaint).
    //
    // Budget reasoning: the in-process relay measures ~275us and the code
    // cites a p95 CDP round trip of ~21ms — both negligible. The rest of
    // this measurement is genuine video-pipeline latency (encode + SFU
    // relay + jitter buffer + decode + our own ~60ms poll granularity),
    // which for a live low-latency WebRTC path is typically well under a
    // second. 3000ms is a generous end-to-end UI ceiling that survives CI/
    // devpod resource contention while still meaningfully bounding
    // "realtime" — a genuinely broken/batched pipeline (e.g. reverting to
    // multi-second JPEG-polling-style latency) would blow well past it. ──
    await test.step('evidence: input round-trips to an observable effect within a realtime budget', async () => {
      const frame = browserLiveFrame(page);
      await expect(frame).toBeVisible({ timeout: 10_000 });

      const before = await sampleFrame(video);

      const t0 = Date.now();
      // Click anywhere in the frame — the fixture's click listener is on
      // `document`, so exact coordinates within the captured content don't
      // matter for triggering the effect (only for which pixels we sample
      // afterward, which sampleFrame already fixes at top/bottom bands).
      await frame.click();

      const pollDeadlineMs = Date.now() + 5_000;
      let detectedAtMs: number | null = null;
      while (Date.now() < pollDeadlineMs) {
        const sample = await sampleFrame(video);
        if (colorDistance(sample.bottom, before.bottom) > 60) {
          detectedAtMs = Date.now();
          break;
        }
        await page.waitForTimeout(60);
      }

      expect(
        detectedAtMs,
        "the click's visual effect (clickLayer color flip) never appeared in the captured " +
          'video within 5s of dispatch — input is not reaching the agent\'s tab in ' +
          'realtime (or at all)',
      ).not.toBeNull();

      const latencyMs = (detectedAtMs as number) - t0;
      testInfo.annotations.push({ type: 'input-latency-ms', description: String(latencyMs) });
       
      console.log(`[browser-live-video] measured end-to-end input latency: ${latencyMs}ms`);
      expect(latencyMs, `end-to-end input latency (${latencyMs}ms) exceeded the realtime budget`).toBeLessThan(3_000);
    });
  },
);
