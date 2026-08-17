# ADR-061: Remove the JPEG CDP-screencast live-browser fallback — reverses ADR-047 D3

- **Status:** **Accepted — 2026-08-12** (operator: Daniel Piatkowski — explicit directive:
  "the JPEG-screencast live-browser path removed once and for all, not deprecated or
  feature-flagged. WebRTC becomes the ONLY live-browser video path. If WebRTC cannot
  connect, the panel must fail visibly and honestly — it must NOT silently degrade to
  JPEG.").
- **Reverses:** ADR-047 D3 ("Degradation: JPEG `browser_screencast` REMAINS the
  automatic fallback tier") — the accepted decision to keep the pre-existing ADR-038
  CDP screencast live view running underneath WebRTC as an automatic degrade path.
  ADR-047 D3 itself was already an amendment of ADR-038 D3 (which this reverts a
  second and final time) and a cancellation of ADR-044 §6.0–§6.4's original plan to
  remove the JPEG path — this ADR completes that removal.
- **Deciders:** Daniel Piatkowski (operator, directive-level).
- **Evidence level:** 1 — direct code inspection of the removed mechanism
  (`pkg/tools/browser/live.go`, `pkg/tools/browser/capture_session.go`,
  `pkg/gateway/browser_ws.go`, `pkg/gateway/browser_webrtc.go`) plus the wire contract
  (`contracts/asyncapi.yaml`, `contracts/components/schemas/`).

## Context

ADR-038 introduced the live interactive browser panel over a CDP `Page.startScreencast`
JPEG stream. ADR-044 planned to replace it with WebRTC and remove the JPEG path
outright (§6.0–§6.4, "M-10"). ADR-047 reversed that removal (D3): WebRTC shipped as
*progressive enhancement layered over* the JPEG screencast, which stayed running
underneath as the automatic fallback tier — any WebRTC failure (ICE, capability
gate, lite build, runtime error) silently degraded the panel back to JPEG with "no
regression, no unavailable state." `CaptureSession.ReconcileScreencast`
(`pkg/tools/browser/capture_session.go`) paused the JPEG screencast only while
*every* JPEG-attached viewer was also covered by an active WebRTC stream, and resumed
it the instant that stopped being true — a dynamic, per-viewer-coverage guarantee, not
a literal "always on" one, but one that meant the JPEG CDP subscription, its ack
worker, and its `browser_screencast` wire frame never fully went away.

Operator directive (2026-08-12): remove the JPEG fallback entirely, not deprecate or
gate it behind a flag. WebRTC (ADR-047) becomes the sole live-browser video path. A
WebRTC failure must now be a visible, honest failure state in the panel — never a
silent substitution of another video source.

## Decision

**Delete the JPEG CDP-screencast live-view path in full**, on both the Go backend and
the wire contract:

1. **`pkg/tools/browser/live.go`** — removed `page.StartScreencast`/`StopScreencast`/
   `ScreencastFrameAck` CDP calls, the `EventScreencastFrame` listener, the ack-worker
   goroutine (`runAckWorker`/`queueAck`/`ackCh`), frame delivery (`deliver`,
   `handleScreencastEvent`), the `LiveFrame`/`FrameSink` types, and the screencast
   tuning consts (`screencastQuality`/`screencastMaxWidth`/`screencastMaxHeight`/
   `screencastEveryNthFrame`/`screencastAckTimeout`).
2. **`pkg/tools/browser/capture_session.go`** — removed `CaptureSession.
   ReconcileScreencast` and every call site (`AddViewer`, `RemoveViewer`,
   `RemoveViewerIfCurrent`, `HandleViewerOffer`, `Stop`) — there is no JPEG tier left
   to pause/resume.
3. **`pkg/gateway/browser_ws.go`** / **`browser_webrtc.go`** — `handleAttach` no
   longer registers a frame sink or emits `browser_screencast` frames;
   `sendFrame`/`sendFrameGen` (the lossy, non-blocking per-frame send path) are
   deleted as dead code. ADR-047 D3's own text ("WebRTC failing must never take the
   JPEG fallback down with it") is now void — documented in place rather than left
   asserting a contract that no longer exists.
4. **Wire contract** — `contracts/components/schemas/BrowserScreencastFrame.yaml`
   deleted; `BrowserScreencastFrame` (message, schema, `WsFrameType` enum entry,
   `oneOf`/union membership) removed from `contracts/asyncapi.yaml`;
   `pkg/api/generated/` and `src/lib/api/generated/` regenerated
   (`scripts/gen-contracts.sh`); `make verify-contracts` passes.

### What a WebRTC failure now looks like to the user

`browser_webrtc_state{available:false, reason:...}` or `{active:false}` (after
previously being `true`, e.g. an ICE failure) now means **the panel genuinely has no
video** — there is nothing left to fall back to. The SPA side (owned by a sibling
change, not this backend PR) is expected to render that as an honest "no video" /
retry state rather than silently degrading to another stream, per the operator's
explicit instruction. `webrtcUnavailableReason`'s gate ladder (disabled → lite build →
not-capable) is unchanged — only the consequence of landing on it changed.

### The "is a live session attached" signal

The JPEG screencast frame used to double as an implicit "the panel is genuinely
live" signal on the SPA side, since a frame only ever arrived once `page.
startScreencast` had actually taken effect. With no frames of any kind on this path,
the authoritative signal is the **`browser_status{state:"attached"}`** frame
`handleAttach` already sends synchronously once `LiveViewRegistry.Attach` succeeds
(this always fires — it does not depend on WebRTC negotiating) — combined with
`browser_webrtc_state{available, active}` for whether *video* is currently flowing.
The SPA-side reconciliation of that replacement signal is the sibling frontend
change's responsibility; this ADR records the decision so both sides can be
reconciled against the same contract.

### Structural simplification this enabled

Removing the CDP screencast subscription also removed every CDP round trip from
`LiveView.attach()`/the tab-follow rebind path — those methods used to require
releasing `lv.mu` around a blocking `chromedp.Run` call (the ADR-038 deadlock
postmortem this file's remaining CDP call sites — `SetViewport`, `dispatchInput`,
`rescaleToCSSViewport` — still document), plus a self-correcting retry loop and two
documented interleaving fixes (ADR-041 "Finding A"/"Finding B") to stay correct
across concurrent attach/detach/rebind. With no CDP call in the tab-follow path,
`attach()` and the renamed `rebindWatch` (was `rebindScreencast`/
`rebindScreencastOnce`) now run start-to-finish under a single mutex acquisition;
the retry loop and both interleaving fixes are gone because the race windows they
existed to close are no longer reachable. `Page.bringToFront` — which the removed
`attach()`/`rebindScreencastOnce` called before every `StartScreencast` (a real
full-Chrome finding: a CDP-created target that's hidden to the compositor emits zero
screencast frames) — is unaffected for WebRTC: `CaptureSession`'s own
`bringAgentTabToFront` (`capture_session.go`, `bringToFrontTimeout`) already does
this independently for the WebRTC capture path, and `BrowserManager.SwitchTab`'s
`activateTabInChrome` already does it independently on tab switch. Neither depended
on the JPEG-path call; both survive unchanged.

## Consequences

### Positive

- One live-browser video path instead of two — less code, no dual-maintenance
  burden, no "which path is actually showing" ambiguity for support/debugging.
- Structural race-condition surface removed (see above) — not merely dead code
  deleted, but a whole class of interleaving fix that existed only to protect a
  now-nonexistent CDP subscription.
- Matches the operator's stated product posture: an honest failure is better than a
  silent, lower-quality substitution the user never asked for and cannot detect.

### Negative

- **No graceful degradation.** A WebRTC failure (ICE, capability gate, lite build,
  transient runtime error) now means no video at all until the next successful
  offer — there is no safety net. This is the explicit, accepted tradeoff of this
  ADR, not an oversight.
- Any deployment environment where WebRTC (UDP/STUN) traversal is blocked and JPEG
  was the only thing that ever worked now has **no live-browser video** in that
  environment. ADR-047 §8 OI-1 recorded that TURN-free traversal was confirmed for
  this project's primary deployment class (Fly) but is not guaranteed universally;
  this ADR accepts that gap rather than re-opening the JPEG fallback to cover it.

### Neutral

- The `browser_screencast` wire frame type is gone from the AsyncAPI contract;
  any external client coded against it will see a schema validation failure on
  `WsFrameType`, not a silent parse. This is the intended contract-first behavior
  (Constraint #8).

## Do not reintroduce

**A merge from another branch must not resurrect the JPEG screencast path.** If a
merge conflict reintroduces `page.StartScreencast`/`EventScreencastFrame`/
`ScreencastFrameAck` calls in `pkg/tools/browser/live.go`, a
`CaptureSession.ReconcileScreencast` method, a `BrowserScreencastFrame` wire type, or
the `browser_screencast` `WsFrameType` enum value, resolve the conflict by keeping
the deletion — re-adding any of them is a regression of this ADR, not a legitimate
conflict resolution. This mirrors the existing "Retired surfaces — do NOT
reintroduce" convention documented in the project's `CLAUDE.md`.

## Validation

- `gofmt -l pkg contracts` — 0.
- `CGO_ENABLED=0 go build -tags goolm,stdjson ./...` — clean.
- `CGO_ENABLED=0 go vet -tags goolm,stdjson ./...` — clean.
- `make verify-contracts` (gen-contracts + `check-no-handwritten-wire-types.sh` +
  `tsc -b --noEmit`) — clean (0 hand-written wire-type findings; TypeScript compiles
  against the regenerated types).
- `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 ./pkg/tools/browser` (scoped to
  every `LiveView`/`LiveViewRegistry`/`CaptureSession` test, excluding the
  real-Chromium-gated suite which is CI's responsibility per this project's
  documented "don't trust local Chromium E2E runs" convention) — all pass.
- `golangci-lint run --build-tags=goolm,stdjson ./pkg/tools/browser/... ./pkg/gateway/...`
  — clean.
