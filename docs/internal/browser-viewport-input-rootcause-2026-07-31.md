# Root cause: adaptive viewport reports success but changes nothing — and breaks input

**Date:** 2026-07-31
**Deployed state when measured:** UAT v23 (`https://uat-omnipus.fly.dev`)
**Status:** root-caused with hard numbers via live Playwright measurement. NOT fixed.

## The measurement (this is the whole finding)

Taken live in the deployed panel, after opening the browser and waiting for a
frame:

| | size | aspect |
|---|---|---|
| Panel container (`browser-live-frame`) | 561 × 587 | 0.96 |
| `<video>` element box | 561 × 587 | fills container correctly ✓ |
| **`<video>` INTRINSIC stream (`videoWidth/videoHeight`)** | **319 × 158** | **2.02** |

`devicePixelRatio` = 1. `readyState` = 4 (real decoded frames).

Three independent faults are visible in that one row.

## Fault 1 — the tab is never actually reshaped (silent failure)

The stream's aspect is **2.02**, i.e. still the old landscape shape. The panel
is **0.96** (portrait). The gateway logged `viewport applied` three times
during the session and returned success.

**Cause:** `LiveViewRegistry.SetViewport` applies CDP
`Emulation.setDeviceMetricsOverride`, but `encoder.js:305-316`
(`captureActiveTabStream`) sizes the capture from `chrome.tabs.get(tabId).width/height`.
**The emulation override is not reflected in what the extension API reports.**

This is exactly the risk recorded in
`docs/internal/browser-panel-adaptive-viewport-plan-2026-07-31.md` under
"Remaining (much narrower) unknown" — now CONFIRMED, not hypothetical:

> whether `Emulation.setDeviceMetricsOverride` is reflected in what
> `chrome.tabs.get()` reports as width/height … If it is not reflected, use
> CDP `Browser.setWindowBounds`

Every layer reports success while nothing changes: `SetViewport` returns
`applied=true`, `Recapture()` runs cleanly, `runCaptureAndOffer()` completes
without error, and the capture silently continues at the old geometry. Textbook
silent failure.

**Fix:** use `Browser.setWindowBounds` for the ASPECT (definitely reflected in
tab dims) and keep `setDeviceMetricsOverride` only for `deviceScaleFactor`
(sharpness) — verifying the two do not fight. Re-measure `videoWidth/videoHeight`
after, since that is the only thing that proves it worked.

## Fault 2 — the encoder downscales the stream to 319 × 158

Far below both the tab viewport and the panel. Cause is almost certainly
`applyVideoSenderConstraints` (`encoder.js:196-244`):
`degradationPreference = 'balanced'` plus `DEFAULT_MAX_VIDEO_BITRATE_BPS`
(2 Mbps) on a CPU-starved `shared-cpu-2x` box. VP8 trades resolution away
under pressure.

Upscaling 319 × 158 into a 561 × 587 box is the reported blur.

**Fix direction:** `degradationPreference: 'maintain-resolution'`, explicit
`scaleResolutionDownBy: 1`, and a higher bitrate ceiling. Machine size may also
need raising — 2 shared vCPUs is thin for software VP8.

## Fault 3 — THIS is why mouse and keyboard do nothing

**The input path is no longer an authorization problem.** Server logs since v23:
`does not hold control` appears exactly **once** (450 before v23), and that one
is correct by design — the first input takes the wheel via
`EnsureControlForInput` and the retry dispatches. Inputs are ACCEPTED.

They land in the wrong place. `activeFrameDims()` resolves to the video's
intrinsic size in video mode, and `mapPointerToDeviceCoords` maps container
coordinates into that space. The whole mapping rests on a documented assumption
(wave-plan key-decision-8, quoted in `encoder.js:295-304`):

> tab capture delivers device pixels of the tab viewport, page_scale 1.0

With the stream at **319 × 158** and the real page around 1280 wide, that
assumption is false by roughly 4×. Every click is delivered to about a quarter
of its intended position — hitting empty page. Not blocked, not rejected, just
useless. Keyboard follows, because focus is never established by the click.

**Fix:** the mapping must stop assuming `videoWidth == page CSS pixels`. The
true capture geometry has to be carried explicitly (or the downscale prevented,
Fault 2) — with adaptive resize, this value now *drifts at runtime*, which it
never did when the capture was a fixed 1280×720.

## Ordering of fixes

1. **Fault 1 first** (`Browser.setWindowBounds`) — it is the root; 2 and 3 are
   both downstream of the capture geometry being wrong.
2. **Fault 2** — prevent the downscale, else the mapping still sees a size that
   does not match the page.
3. **Fault 3** — make the mapping robust to a capture size that differs from
   the page's CSS pixel space, rather than assuming they are equal.

**Exit proof:** re-run the live measurement above. The stream's intrinsic aspect
must match the panel's, its size must be close to the panel's CSS size × DPR,
and a click on a known target must visibly activate that target. Nothing short
of that observation proves this fixed — every layer already reports success
today while doing nothing.

## Related open item (separate, still outstanding)

`src/lib/ws.ts` has a bare `catch {}` around `close(4000, 'ping timeout')`.
Reported by the sibling instance (see `/home/dev/agent-bridge/`), accepted but
NOT yet verified line-by-line or fixed here. If `close()` throws for any reason
other than the reserved-code case already fixed, `onclose` never fires, nothing
is logged, and the connection sits permanently frozen. Their fix: extract the
`onclose` body into a named `_handleClose(event)` and call it from the catch
with a synthetic `{code:4000, reason:'ping timeout (close() threw)'}`.
