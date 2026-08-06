# Plan: adaptive browser viewport (aspect + sharpness)

**Date:** 2026-07-31
**Trigger:** operator UAT — the docked panel fills width but not height, and the
page renders blurry.

## Diagnosis (confirmed, not inferred)

Two independent defects, both at the **capture**, not in CSS:

1. **Fixed aspect.** `pkg/tools/browser/exec_resolver.go:131` hardcodes
   `--window-size=1280,720` (16:9). `encoder.js:305-311` sizes the capture from
   `chrome.tabs.get(tabId).width/height`, so the captured surface IS that fixed
   viewport. The operator's docked panel measures ~890x1010 (**0.88:1,
   portrait**). `object-contain` preserves the SOURCE aspect, so it can only
   ever fill one dimension — hence bars top/bottom. No CSS change can fix this;
   the source shape is wrong.
2. **Fixed resolution.** The capture is 1280x720 CSS px at DPR 1 (encoder.js
   comment: "DPR is 1 in the managed headless Chrome"). Displayed larger than
   that, or on a 2x device, it upscales → blur.

`--window-size` sets only the INITIAL window and cannot follow a resize, so a
better constant does not solve (1) — the panel's aspect changes with the
browser window. It must be dynamic.

Confirmed absent today: no panel→backend resize path exists (`rg` for
resize/viewport/SetDeviceMetrics across `browser_ws.go` + `capture_session.go`
returns nothing), and no `setDeviceMetricsOverride` call anywhere in `pkg/`.

## Mechanism

CDP `Emulation.setDeviceMetricsOverride` — sets width, height AND
`deviceScaleFactor` per tab, which addresses both defects with one call.
`deviceScaleFactor: 2` renders at 2x and fixes sharpness.

## Steps (contract-first, Constraint #8)

1. `contracts/components/schemas/BrowserViewportFrame.yaml` — new client→server
   frame: `{type, width, height, device_scale_factor}`.
2. Reference from `contracts/asyncapi.yaml` (client→server, browser channel).
3. `scripts/gen-contracts.sh` → regenerate Go + TS + Zod. Commit generated
   artifacts in the same commit as the spec (never separately).
4. SPA: `ResizeObserver` on the panel container → debounced (~250ms) send of
   CSS size × `devicePixelRatio`, on attach and on every resize.
5. Gateway: handle `browser_viewport` in `browser_ws.go`'s frame switch →
   forward to the browser manager.
6. Browser manager: `Emulation.setDeviceMetricsOverride` on the live tab.
7. Re-capture (see RISK).

## RISK — RESOLVED 2026-07-31 by tracing the recapture path

**The original risk below does not apply. `applyConstraints` is not needed.**

`browser_capture_control{action: recapture}` already exists end-to-end —
encoder handler, Go sender, generated contract type
(`asyncapi_types.gen.go:54`), and tests
(`TestCaptureSession_RecapturePropagatesToRelayAndIngest`). Traced chain:

    recapture
      -> runCaptureAndOffer()            encoder.js:391
      -> teardownCapture()               (drops the old stream + PC entirely)
      -> captureActiveTabStream()
      -> chrome.tabs.get(tabId)          encoder.js:305-311  <-- RE-READS dims
      -> new stream pinned to the NEW geometry
      -> fresh browser_capture_offer     (new PC, fresh SSRCs)

The pinned `minWidth/maxWidth` constraints are fixed per STREAM, and recapture
creates a new stream — so it picks up whatever size the tab is at that moment.
Nothing needs to mutate a running track.

**Revised sequence:** resize the tab viewport, then send `recapture`. Both
halves already exist; the new work is the panel->backend size channel and the
CDP resize call.

**Remaining (much narrower) unknown:** whether `Emulation.setDeviceMetricsOverride`
is reflected in what `chrome.tabs.get()` reports as width/height — the
extension API may report the window's tab strip geometry rather than the
emulated viewport. If it is not reflected, use CDP `Browser.setWindowBounds`
for the ASPECT (definitely reflected in tab dims) and keep
`setDeviceMetricsOverride` only for `deviceScaleFactor` (sharpness), verifying
the two do not fight. Probe this by resizing and reading back
`window.__omnipusState.videoTracks[0].settings` via CDP Runtime.evaluate —
the encoder already records track settings there for exactly this purpose.

## ORIGINAL RISK (superseded — kept for the reasoning)

`encoder.js:322-333` creates the capture stream with **pinned** constraints:

    mandatory: { minWidth: capW, maxWidth: capW, minHeight: capH, maxHeight: capH }

These are fixed at stream-creation time. Resizing the tab viewport mid-stream
will NOT change an already-running capture — the track keeps producing the old
geometry. Options, in order of preference:

- **`videoTrack.applyConstraints({width, height})`** — cheapest if tabCapture
  honours it. Unverified for `chromeMediaSource: 'tab'`; must be tested, not
  assumed.
- **Tear down and re-create the capture stream** on viewport change, then
  renegotiate. Reliable but causes a visible reconnect blip per resize, so it
  must be debounced hard and skipped for sub-threshold deltas.

Do not start step 4 before resolving step 7 — the frontend work is wasted if
the capture cannot follow.

## Sequencing note

Steps 1-3 touch generated code across both languages; leaving them half-applied
breaks `make verify-contracts` for everyone on the branch. Do them as one
atomic unit, not incrementally.

## Interim (does NOT fix aspect)

Raising the hardcoded `--window-size` to something taller and larger (e.g.
1200x1400) would improve BOTH sharpness and, for this particular panel shape,
the aspect mismatch — but it is a constant, so it merely trades which
container shape is wrong. Only worth doing as a stopgap if the full fix is
deferred.
