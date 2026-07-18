# Live-browser WebRTC — wave implementation plan (2026-07-18)

Goal: full browser control + streaming + **audio** end to end, delivered fast with parallel
subagents, excellent tests, early Playwright verification, operator UAT at first working e2e.
Grounding: `webrtc-build/recon-digest.md`, `wv1-spike-results.md`, the WebRTC ADR
(supersedes ADR-044 transport/capture). Branch: `feature/browser-video-2`.

## Architecture recap (what we're building)

- Capture: gateway-owned **tabCapture MV3 extension** in the managed **full Chrome,
  `--headless=new`** — audio+video MediaStream of the agent's active tab. No sidecars.
- Transport: **Pion SFU relay in the gateway** — encoder-page PC (ingest) → shared
  `TrackLocalStaticRTP` per kind → N viewer PCs. Input rides an `"input"` data channel
  (byte-identical `BrowserInputFrame` payloads) → existing `LiveInput` dispatch (controller
  lock, SSRF gate, rate limit unchanged).
- Signaling: existing `/api/v1/browser/ws`, contract-first, **non-trickle** offer/answer.
- Degradation: **JPEG screencast stays running as today** (v1 keeps both paths active while a
  live view is attached — instant fallback, annotate keeps working, zero regression risk;
  ~~consolidation is a later optimization~~). WebRTC is progressive enhancement; lite/non-Linux/
  ICE-fail → JPEG (no audio).

  **Amended 2026-07-18 (commit 41022b69, pod-CPU UAT finding):** consolidation SHIPPED
  in v1 — a guarded pause reconciler: JPEG pauses when WebRTC covers every attached
  viewer; resumes on a JPEG-only viewer, WebRTC death, or stop.
- Foundations ported from archive FETCH_HEAD: `cdppipe` (CDP-over-pipe; prerequisite for
  `Extensions.loadUnpacked`), coordinator lockfile rework, installer full-Chrome
  dual-download + capability classify.

## Key implementation decisions (fixed here so agents don't re-litigate)

1. **Extension ID pinned** via manifest `"key"` (public key) → deterministic ID on every
   install; no two-phase learn-then-relaunch. Extension shipped `go:embed` →
   `pkg/tools/browser/captureext`, seeded atomically to `$OMNIPUS_HOME/browser/captureext/<ver>/`
   (skills `SeedDefaults` pattern).
2. **Encoder page = the extension's own page** (`chrome-extension://<id>/encoder.html`),
   self-consuming tabCapture (spike-proven simplest). Captures "the active tab" via
   `chrome.tabs.query({active:true})`; gateway signals **recapture** on active-tab switch
   (hook: the `onTabsChanged` → `rebindScreencast` analog).
3. **Ingest leg**: loopback-only WS endpoint `/api/v1/browser/capture-ingest`, authorized by a
   per-stream capability token minted by the gateway and injected via
   `Page.addScriptToEvaluateOnNewDocument` (never in URL/`/json`). Non-trickle offer/answer +
   control messages (recapture, shutdown, health). Loopback is not a trust boundary → token
   mandatory, audit rejections.
4. **Stream lifecycle**: capture session per agent, started on first WebRTC-capable viewer
   offer, stopped on last detach (grace ~30s). ~~One active stream per agent (v1).~~
   **Corrected 2026-07-18 (as-built):** one actively-viewed capture across ALL agents
   in v1 — not one-per-agent.
5. **Lite gating**: `pkg/tools/browser/webrtc` real files `//go:build !lite`, stub keeps the
   same API returning ErrUnavailable; JPEG is the lite behavior (whatsmeow pattern).
6. **Data-channel framing**: browser text frames ⇒ Go replies `SendText` (spike gotcha).
   Non-trickle ICE both legs. PLI to ingest PC (remote MediaSSRC) on viewer join + burst.
   Drain RTP/RTCP on every receiver/sender.
7. **Config**: `Tools.Browser.WebRTCEnabled` (default true; post-auth gate like
   `live_view_enabled`), `Tools.Browser.WebRTCStunServer` (default `stun:stun.l.google.com:19302`,
   empty string = host-candidates only).
8. **Coordinate mapping**: viewer maps against `video.videoWidth/Height`; `page_scale` for the
   video path is 1.0 at capture resolution — tab capture delivers device pixels of the tab
   viewport (verify in e2e; JPEG path mapping unchanged).

## Waves

### Wave 1 — foundations (6 parallel agents)

| ID | Agent | Isolation | Scope (files) | Definition of done |
|---|---|---|---|---|
| W1-A | backend-lead | worktree | Port `cdppipe/` from FETCH_HEAD verbatim (+tests); rework `coordinator.go`/`exec_resolver.go` to pipe launch (lockfile pair, child contexts, drop port 9223 + preflight; keep THIS branch's per-agent `WithNewBrowserContext` model); remove 9223 entries in `pkg/gateway/sandbox_apply.go`; add `Extensions.loadUnpacked` + `--allowlisted-extension-id` launch support (ext dir + id params on BrowserConfig) | package builds; cdppipe tests green; existing coordinator/manager scoped tests green; managed launch verified against real Chrome on the pod (open a page over pipe) |
| W1-B | backend-lead | worktree | Port `installer.go` dual-download + `capability.go` (+tests) from FETCH_HEAD; reword A2 semantics → WebRTC (`VideoAndAudioLevel` when full Chrome present on linux — **corrected 2026-07-18 (as-built):** collapsed to `VideoCapability{Capable,Reason}`; the gateway capability gate is `CaptureVideoCapability`); keep current `EnsureChromium` callers compiling | scoped installer/capability tests green |
| W1-C | backend-lead | main tree | NEW `pkg/tools/browser/webrtc/`: Pion relay library adapted from spike q4 (`session.go`, `ingest.go`, `viewer.go`, `tracks.go`, `inputdc.go`, lite stub) — API: `NewSession(cfg, InputSink, logf)`, `HandleIngestOffer/HandleViewerOffer(sdp) (answer, err)` **(corrected 2026-07-18 (as-built): `HandleViewerOffer` takes a `viewerID` param)**, `SignalRecapture()`, `Close()`; no gateway imports | unit tests green incl. a **Go↔Go PC test** (pion as fake encoder/viewer in-process — full media+DC path without Chrome); `-tags lite` builds |
| W1-D | backend-lead | main tree | Contracts: `BrowserWebRTCOfferFrame`/`BrowserWebRTCAnswerFrame`/`BrowserWebRTCStateFrame` schemas + messages + browser-channel wiring + ops; capture-ingest leg frames (`BrowserCaptureHelloFrame`, `BrowserCaptureOfferFrame`, `BrowserCaptureAnswerFrame`, `BrowserCaptureControlFrame`); run `make gen-contracts`; add `WebRTCEnabled`/`WebRTCStunServer` to `BrowserToolConfig` + defaults | gen-contracts idempotent; `make verify-contracts` green; generated Go+TS committed together |
| W1-E | frontend-lead | main tree | NEW `pkg/tools/browser/captureext/`: MV3 extension (manifest w/ generated pinned `key`, sw.js, encoder.html/encoder.js: tabCapture self-consume → PC non-trickle → ingest WS w/ injected token; recapture/shutdown control; reconnect watchdog; health beacon) + `captureext.go` embed+seed (atomic, versioned dir) + computed extension ID constant + Go test for seed idempotency | seed test green; extension loads in real Chrome on the pod via W1-A's launch path if available, else via spike-style pipe harness; ID matches pinned constant |
| W1-F | frontend-lead | main tree | SPA groundwork in `BrowserLiveView.tsx` + `browserLiveCoords.ts`: `<video>` sink alongside `<img>` (render video when a MediaStream is set, else JPEG), coords-from-video variant, canvas-capture crop for annotate when in video mode, mute/unmute control (gesture-gated autoplay), all behind a nullable stream prop — JPEG behavior byte-identical when null | `npm run typecheck` + vitest green (new tests for coords/crop/mode-switch); no signaling yet |

Merge order after wave: W1-A worktree → W1-B worktree → main tree (C/D/E/F already in).
Conflict surface: none by construction (disjoint files; go.mod pre-committed).

### Wave 2 — vertical slice (3 parallel agents)

| ID | Agent | Scope | DoD |
|---|---|---|---|
| W2-A | backend-lead | Gateway wiring: `pkg/gateway/browser_webrtc.go` — signaling handlers on browser WS (offer → ensure capture session → viewer PC → answer; state frame w/ fallback reason + has_audio), capability+flag gates, per-agent stream lifecycle, encoder orchestration (seed ext, launch encoder page via coordinator, token mint+inject, `/api/v1/browser/capture-ingest` loopback WS), recapture on tab switch, input DC → `Live().Input`, audit events, feature-gate 503-free degradation | compiles + handler unit tests w/ fake webrtc.Session; scoped gateway tests green |
| W2-B | frontend-lead | `browserLiveWs.ts` + `BrowserLiveView`: send offer on attach when state=available, apply answer, attach tracks to W1-F sink, input over DC (WS fallback while DC not open / JPEG mode), ICE-fail → JPEG fallback state machine (5s timeout), ~~Settings toggle (Browser section w/ Switch)~~ **(corrected 2026-07-18: not built; deferred follow-up)**, pop-out parity | typecheck + vitest green (signaling state machine tests w/ mocked RTCPeerConnection) |
| W2-C | qa-lead | Cross-cutting tests: Go integration test — full signaling over a real in-process gateway WS + pion fake encoder + pion fake viewer (media flows, input DC → dispatched LiveInput recorded); vitest e2e-ish signaling tests; contract round-trip tests for new frames | all green locally (scoped), CI-ready |

### Wave 3 — e2e + review gate

1. Build embed pipeline (SPA → `pkg/gateway/spa/` → binary), run gateway on the pod with
   managed full Chrome, **Playwright drives the real SPA**: open panel → video renders
   (`framesDecoded` grows, ~30fps), **audio RMS non-zero** on a sound-playing page, drive
   (click/type/scroll round-trip visible), tab switch → recapture, kill encoder → auto-recover,
   disable flag → JPEG fallback, lite build → JPEG. Fix loop until green.
2. **7 parallel PR reviewers** (operator gate) over the full diff → fix wave → quality gates
   (gofmt, golangci-lint, typecheck, vitest, verify-contracts locally; Go suite via ci-omnipus).
3. **Operator UAT**: preview URL + test script; collect feedback. (14-reviewer final pass
   before the eventual PR to main, per repo convention.)

## Test plan summary (excellent-tests requirement)

- Unit: cdppipe (ported), relay (Go↔Go PC), captureext seed, coords/crop/mode vitest,
  signaling state machines both sides, contract round-trips.
- Integration: in-process gateway WS + fake pion peers (no Chrome needed — runs in CI).
- E2E (pod + Playwright, real Chrome + real SPA): the wave-3 checklist above; audio verified
  by measured RMS, input by round-trip observation, fallback by fault injection.
- CI: push after each merged wave; ci-omnipus for the Go suite; never full local suite.

## Commit conventions

Author/committer `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>`,
NO agent Co-Authored-By trailers, push via `bash -lc`. Contract changes: spec + generated
artifacts in one atomic commit. One commit per wave-task (or logical unit), lead merges.
