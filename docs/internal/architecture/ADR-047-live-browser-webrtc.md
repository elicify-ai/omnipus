# ADR-047: Live-browser streaming — WebRTC/Pion promoted to the accepted transport and capture (supersedes ADR-044's transport + capture)

- **Status:** **Accepted 2026-07-18** (operator: Daniel Piatkowski). Operator decision chain: **2026-07-17** — "stream audio *alongside* video, **no PulseAudio sidecar**" (recorded in `docs/internal/design/live-browser-webrtc-context.md`); **2026-07-18** — directive to build the full WebRTC feature end-to-end. This ADR **supersedes ADR-044's transport and capture decisions** (§6.0–§6.4: WebCodecs-over-WS "A2", CDP `Page.startScreencast` capture, the encoder-page-fed-JPEG topology, and the A2-only "unavailable state" degradation with its M-10 contract removal). **ADR-044 remains valid** as decision history and for its **non-superseded** parts: the full-Chrome installer rationale (§6.5 / 2026-07-17 amendment), the CDP-over-pipe motivation (§6.0.3), the single-shared-Chrome coordinator topology (ADR-043 + §6.0.3 pt-3), the STRIDE trust framing (§6.6), and the rejection of client-side rendering / DOM-mirroring (Option D). ADR-044's Option B ("Full WebRTC via Pion", kept there only as a documented escalation path) is the decision now **promoted to Accepted**.
- **Date:** 2026-07-18
- **Deciders:** Daniel Piatkowski (operator); architect (this record)
- **Evidence level (highest used):** 1 (user input + codebase facts + in-pod spike measurements), with tagged inference/assumption where noted
- **Routing:** v0.3 scope (structural), per the release strategy. Branch `feature/browser-video-2` (cut from `bugfixes2` @ `eb4de2a2`).
- **Supersedes for this decision:** ADR-044 §6.0–§6.4 (transport, capture mechanism, degradation-to-unavailable). Amends ADR-038 D3 again: the JPEG `browser_screencast` live view is **retained** (ADR-044/M-10's removal is **cancelled**).

---

## 1. Problem Understanding

ADR-044 accepted "A2": full Chrome run headful on Xvfb + PulseAudio null-sink sidecar, CDP `Page.startScreencast` (video only) → a loopback WebCodecs **encoder page** → binary encoded chunks fanned out over the existing gateway WSS, with degradation being an explicit **"video-capable browser required" unavailable state** (no JPEG fallback tier). Between acceptance and build, three problems surfaced that A2 cannot solve within the project's constraints. `[FACT — live-browser-webrtc-context.md §1]`

1. **Audio is impossible without a sidecar — the decisive constraint.** CDP `Page.startScreencast` is video-only forever; there is no audio in it. The encoder page's `getUserMedia` audio attempt fails `"Requested device not found"` on a headless pod (no audio device/sink). The only ways to get audio in A2 are (a) a PulseAudio null-sink sidecar — **explicitly rejected by the operator** — or (b) a real `MediaStream` capture (`chrome.tabCapture` / `getDisplayMedia`) that carries audio **and** video together, which is WebRTC-native. The operator's hard requirement is audio **with** video, **no sidecar**. `[FACT — user input, 2026-07-17]`
2. **Input contention — a real regression discovered only once video actually rendered.** Input dispatch and the video capture's ack-loop share **one CDP command queue** on the agent tab. The non-coalescing per-frame ack worker (`pkg/tools/browser/capture.go`, documented "*must never drop*") saturates the queue and starves input → `input dispatch failed: context deadline exceeded` while a human drives. The JPEG live path survived only because its ack worker *coalesces* (drops stale frames). Proof: a raw CDP click dispatched in ~1 ms but timed out through the busy chromedp queue. `[FACT — live-browser-webrtc-context.md §1]`
3. **TCP head-of-line blocking.** Video over a single WebSocket hiccups on lossy WANs where WebRTC's congestion control / NACK / jitter buffer degrade gracefully. `[FACT — ADR-044 §5 Option A "Weaknesses"]`

A single architecture resolves all three: capture the agent tab as a real `MediaStream` (audio+video) and transport it over **WebRTC (Pion, pure Go)**, with viewer input on an independent **data channel**. Audio rides for free; input can never contend with pixels (separate transport, never the CDP command queue); congestion control is native. `[FACT — context.md §2; proven by WV1, §5 below]`

Blast radius is the streaming leg plus the capture mechanism. The attach flow, the take-the-wheel controller lock, the SSRF gate, the rate limiter, annotate, and multi-viewer fan-out must keep working. `[FACT — recon-digest.md "Input" / "Contracts"]`

## 2. What ADR-044 Decided, and Why It Reverses Now

ADR-044 chose Option A (WebCodecs-over-WS) over Option B (WebRTC/Pion) primarily on **NFR-2 deployability** (weight 30%): "UDP/ICE/TURN is per-install networking homework that violates the deployability parity Omnipus depends on." That reasoning was sound **under ADR-044's own premise that A2 was the only path** — with no fallback tier, the transport had to be 100 % deployable, and B's UDP requirement failed that bar.

Three things changed, and together they invert the decision:

1. **The decisive requirement changed.** Audio-with-video-no-sidecar is now a hard requirement. Option A **cannot satisfy it at all** without the rejected PulseAudio sidecar (§1.1). This is a requirement change that *eliminates* A, not merely a re-weighting. `[FACT — user input]`
2. **A regression was discovered in A.** The shared-CDP-queue input starvation (§1.2) is structural to the screencast+ack model; WebRTC kills it by construction. `[FACT — spike Q4]`
3. **The NFR-2 objection to B is neutralized by keeping the fallback.** This ADR **retains today's JPEG `browser_screencast` live view as the automatic fallback tier** (D3). WebRTC becomes **progressive enhancement layered over a path that already works everywhere**. A WebRTC ICE failure degrades to *today's* experience — no regression, no "unavailable" state. TURN is therefore **not required in v1**: the worst case is exactly the current product, not worse. The 30%-weight deployability criterion that sank B in ADR-044 is satisfied by the fallback, without UDP homework. `[INFERENCE — grounded in D3 + ADR-044 §4 weights; the load-bearing move of this ADR]`

The honest limit of point 3: keeping the fallback removes the *deployability* objection but does **not** prove that WebRTC will *establish* for external users — see the external-traversal open item (§8, OI-1). It bounds the downside (never worse than today); it does not guarantee the upside is realized for every network.

## 3. Extracted Requirements

Carried forward from ADR-044 §2 (still binding): FR-2 (attach / take-the-wheel / annotate / multi-viewer keep working), FR-4 (new viewer gets prompt first paint), NFR-1 (Safari-on-iPad + Chrome/Edge/Firefox), NFR-2 (works wherever the gateway works), NFR-3 (< 10 MB steady-state Go-process overhead), NFR-4 (graceful degradation), C-1..C-4 (pure Go / single binary / contract-first / Chrome-for-Testing full build available). New or promoted:

- **FR-A1 (decisive):** Live view MUST be able to stream **audio together with video, with no audio sidecar** (no PulseAudio, no separate audio daemon). `[FACT — user input, 2026-07-17]`
- **FR-A2:** Human take-the-wheel input MUST stay responsive **while** audio+video stream (no input starvation). Target: p95 dispatch RTT in tens of ms, not seconds. `[FACT — regression §1.2; validated by spike Q4]`
- **NFR-A3:** No new *mandatory* per-install networking configuration in v1. WebRTC establishment MAY require good NAT/egress, but its failure MUST degrade to the existing JPEG path, never to a broken/blank panel. `[FACT — D3]`

## 4. Decision Criteria (re-anchored)

The ADR-044 criteria table still applies. The change is not a re-weighting of B against A on equal footing — it is that **A now fails a hard gate (FR-A1) outright**, and the fallback tier (D3) **removes B's only losing criterion (NFR-2 deployability)**. With FR-A1 as a gate and NFR-2 satisfied by construction, B wins responsiveness (25%), constraint-fit for audio (the 10% "path to audio" becomes "audio, now, in the base architecture"), and the input-contention correctness that A regressed on — while conceding nothing on deployability that the fallback does not cover. `[INFERENCE — §2, §3]`

## 5. Evidence Base — WV1 spike (in-pod, 2026-07-17/18)

All four make-or-break questions were tested on this pod before this ADR. Full detail: `docs/internal/design/wv1-spike-results.md`. `[FACT — spike measurements]`

- **Q2 — headless tab MediaStream WITH audio: YES.** Chrome-for-Testing 151, `--headless=new`, zero audio devices (Chrome falls back to its internal fake audio backend and still captures real samples). `tabCapture` MV3 extension: 29.3 fps, real stereo 44.1 kHz audio, selection by **tab ID**, **survives `Page.navigate`** (T4: post-nav RMS matches theoretical). No PulseAudio, no sidecar, no audio device, no audio flags. `[FACT]`
- **Q3 — end-to-end video+audio through the target pipeline: YES.** tabCapture MediaStream → WebRTC → **Pion relay in Go (raw RTP forwarding, no transcode)** → viewer browser. VP8+Opus negotiated, `packetsLost=0`, **framesDecoded 1172 @ 30.0 fps steady**, audio pulses at exact 1 Hz matching the source. Encoder kill/restart mid-run recovered with fresh SSRCs. `[FACT]`
- **Q4 — responsive input WHILE video+audio stream: YES.** Input on its own `"input"` data channel; CDP carries only input. Under 30 s stress (~2200 events): **p50 10.9 ms, p95 21.0 ms, max 88.4 ms, 0 errors** — vs the WebCodecs design's 5000 ms+ timeouts. Media unaffected (29.6 fps, `packetsLost=0`). `[FACT]`
- **Q1 — Pion↔SPA connectivity over the Fly-proxied network: IN-POD PASS; external test PENDING.** Pod outbound UDP + STUN works (srflx candidates gathered on IPv4+IPv6 against `stun.l.google.com:19302`); in-pod connect ~150 ms, 96 s echo at ~1 ms RTT, 0 drops; ICE-TCP mux offers valid TCP host candidates. **Not yet proven:** inbound UDP hole-punch to the pod's srflx mapping from a real external network (Fly forwards only TCP:8080). `[FACT — the one unproven leg → OI-1]`
  **→ OI-1 RESOLVED 2026-07-18 (operator UAT):** the operator viewed live video+audio and drove the browser from their own machine through the Fly-proxied preview — the WebRTC PeerConnection established externally without TURN. TURN-free traversal confirmed for this deployment class; the JPEG fallback remains the safety net for networks where it does not. `[FACT — operator UAT]`

Spike build-learnings folded into the decisions below: `RegisterDefaultCodecs()`+`RegisterDefaultInterceptors()` are required for media; one shared `TrackLocalStaticRTP` per kind, `AddTrack`ed to every viewer PC; PLI → the ingest PC on viewer join + periodic burst or late viewers get no keyframe; drain RTP/RTCP on both receiver and sender; **data-channel framing: a browser `dc.send(string)` is text-typed → Go MUST reply `SendText()`** (binary echo is silently dropped). `[FACT — Q1/Q3/Q4]`

## 6. Decision & Target Architecture

**Decided: promote ADR-044 Option B — WebRTC via Pion — to the Accepted live-browser transport and capture, with the existing JPEG `browser_screencast` path retained as the automatic fallback.** Eight sub-decisions (D1–D8).

### D1 — Transport: WebRTC via Pion (pure Go), gateway as an SFU-style relay

One `RTCPeerConnection` per peer. Per active stream there are **two roles**: a **publisher PC** (the gateway-owned capture/extension page in the managed Chrome → gateway Pion "ingest" PC) and **N viewer PCs** (each SPA viewer → gateway Pion "egress" PC). The gateway relays media as an **SFU**: **one shared `TrackLocalStaticRTP` per kind** (video, audio), `AddTrack`ed to every viewer PC — **no transcoding, ever** (C-1: all codec work stays in Chrome and in the browsers' native RTP stacks). Pion (CGo-free, pure Go) satisfies Constraint #2. `[FACT — spike Q3 SFU pattern proven first-try; ADR-044 "Pion is CGo-free"]`

Grounds: the three forcing problems (§1) — audio-with-video is native to WebRTC; input contention is killed **by construction** (D4: input on its own data channel, media never touches the CDP command queue — Q4 p95 21 ms vs the 5000 ms regression); TCP head-of-line blocking is replaced by WebRTC congestion control.

```
CONFIDENCE (D1 — WebRTC/Pion transport, SFU relay): High
  Basis   : Full target pipeline proven end-to-end in-pod (Q3 media, Q4 input);
            SFU shared-TrackLocalStaticRTP + PLI-to-ingest pattern worked first try.
  Evidence: Q3 (1172 frames @ 30 fps, packetsLost=0, VP8+Opus, encoder-restart
            recovery); Q4 (p95 21 ms input under stress, media unaffected).
  Missing : External ICE traversal (OI-1) — bounds benefit reach, not the design
            (fallback covers it). Single-driver arbitration not exercised (OI-3).
  Would improve: OI-1 external run; wiring the controller lock in front of the DC.
```

### D2 — Capture: `chrome.tabCapture` MV3 extension in `--headless=new` FULL Chrome

The agent tab is captured as a real `MediaStream` (audio+video together) by a **gateway-owned MV3 extension** whose page **self-consumes** the capture (`chrome.tabCapture.getMediaStreamId` → `getUserMedia({audio:{mandatory:{chromeMediaSource:'tab',chromeMediaSourceId:id}},video})`) and hands the stream to the publisher `RTCPeerConnection`. No Xvfb, no PulseAudio, no `getDisplayMedia`. Launch flags: `--headless=new --remote-debugging-pipe --enable-unsafe-extension-debugging --allowlisted-extension-id=<id> --autoplay-policy=no-user-gesture-required`. AGC/EC/NS default **off** for a faithful tone. `[FACT — spike Q2 recipe, T3/T4]`

This **empirically corrects ADR-044 §6.0's claim** that headless tab capture is impossible ("*extension capture needs a user gesture*"). That claim predated (a) the `--allowlisted-extension-id` flag, which removes the user-gesture requirement, and (b) testing on Chrome 151 `--headless=new`. With the allowlist flag the capture works headless, no gesture, and survives navigation. `[FACT — spike Q2; the flag is what Chromium's own tabCapture tests use]`

Build-critical mechanics:
- `--load-extension` is **dead since Chrome 137** (silently ignored in 151). The extension is loaded via CDP **`Extensions.loadUnpacked`, which works over the `--remote-debugging-pipe` transport only** — so D5's pipe transport is **load-bearing for capture**, not merely a security nicety. `[FACT — spike Q2]`
- Extension shipped via `go:embed` + atomic seed to `$OMNIPUS_HOME` following the skills `SeedDefaults` pattern (`pkg/skills/embed.go` `//go:embed all:embedded` + `SeedDefaults:61`, staged-tmpdir + atomic rename, idempotent; `OmnipusHomeDir` `home.go:34`). `[FACT — recon-digest]`
- **Extension ID pinned via the manifest `key` field** to get a deterministic ID computable ahead of launch, so `--allowlisted-extension-id` can be passed at launch **without a two-phase launch**. The spike used the two-phase "load, learn ID, relaunch with the ID baked in" dance; the manifest-`key` approach is the recon-recommended production refinement (a well-documented Chrome behavior). `[FACT that manifest key → deterministic ID; INFERENCE that it removes the two-phase launch — recon-digest recommendation, not yet spike-proven end-to-end]`
- The `-32000 "no browser is open"` on `--headless=new` over the pipe means the build MUST use **raw `Target.createBrowserContext` + `createTarget(WithNewWindow(true))`**, not chromedp's `WithNewBrowserContext`. The Chrome-151 hidden-tab screencast bug (a background sibling reports `visibilityState=hidden`) is why each tab needs its **own window** (`WithNewWindow(true)`); whether tabCapture of a background tab is affected the same way is an implementation detail (→ OI-2). `[FACT — context.md §5 learnings]`

```
CONFIDENCE (D2 — tabCapture MV3 extension, headless full Chrome): High
  Basis   : Directly measured — 29-30 fps + real audio, survives navigation, no
            sidecar (Q2 T1-T4); consumed end-to-end in Q3.
  Evidence: Q2 recipe reproduced; --allowlisted-extension-id removes the gesture.
  Missing : Manifest-key single-phase launch (recon refinement over the spike's
            two-phase learn); background/non-active-tab capture behavior (OI-2).
  Risk    : --allowlisted-extension-id / --enable-unsafe-extension-debugging are
            capture-critical flags; a future Chrome removal breaks WebRTC capture
            (not the product — falls back to D3 JPEG). Pin the CfT version; add a
            capture smoke test to the browser-tool CI suite.
```

### D3 — Degradation: JPEG `browser_screencast` REMAINS the automatic fallback tier

This **reverses ADR-044's A2-only "unavailable state"** and **cancels the M-10 contract removal** of `browser_screencast`. WebRTC is progressive enhancement; the existing JPEG live view (video-only, no audio) is the graceful-degradation floor. The degradation ladder is evaluated at **two levels**:

1. **Install/build level (classifier).** WebRTC requires: the non-`lite` build (Pion compiled in, D7) **and** the full Chrome build present (extension capture needs full Chrome, not `chrome-headless-shell`) **and** a supported platform. `ClassifyVideoCapability` (D5) gates this. Otherwise → JPEG.
2. **Per-viewer level (ICE).** Even on a video-capable install, a given viewer whose network cannot establish ICE (no TURN in v1) falls back to JPEG **for that viewer**, signalled by `browser_webrtc_state{fallback_reason}` (D4). A LAN viewer may get WebRTC while an external viewer on the same stream gets JPEG.

This is the **NFR-2 answer without TURN**: worst case = today's experience, for every install and every viewer. `[FACT — recon-digest "fallback posture = JPEG screencast remains"; context.md]`

- **No TURN in v1.** STUN server is **configurable**; default is a public STUN (`stun.l.google.com:19302` in the spike). Document the egress dependency (the install must reach the STUN server over UDP — unfiltered today, D7) **and** the privacy note that a public default STUN discloses the install's IP to that provider; operators may set their own. `[FACT — Q1; INFERENCE on the privacy note]`
- The still-pending operator **external traversal test** (OI-1) is recorded as an **ops-guidance / benefit-reach item that cannot change the architecture** — because the fallback exists, its outcome only decides *how many* users get WebRTC vs JPEG, not whether the feature ships or regresses anything.

```
CONFIDENCE (D3 — JPEG fallback retained, no TURN in v1): High
  Basis   : The JPEG path is the current shipping live view (browser_screencast,
            browser_ws.go:546 → browserLiveWs.ts:147) — stable, tested, free to keep.
  Evidence: Existing code path; ADR-044's own R5/F-02 note that browser_screencast
            is "not dead" — it is the sole live-view transport today.
  Missing : Nothing at the architecture level. OI-1 governs how often WebRTC wins.
  Would improve: An embedded pure-Go TURN or ICE-TCP-over-forwarded-port fallback
            (deferred; escalation path if OI-1 shows external UDP is chronically blocked).
```

### D4 — Signaling and input: contract-first over the existing `/api/v1/browser/ws`

Signaling rides the existing authenticated WS endpoint (`/api/v1/browser/ws`, `gateway.go:2091`) with its existing first-frame bearer auth (`browser_ws.go:283`), origin check (`wsCheckOrigin:189`), and inbound schema validation (`:450-466`). **Non-trickle** offer/answer (spike-proven copy-pasteable SDP; simpler than trickle at the cost of waiting for ICE-gathering-complete before the offer/answer is emitted). New contract-first frames (Constraint #8; each a **per-frame `const`-discriminated schema file with `additionalProperties:false`**, never a `oneOf` union over external file refs — the **ADR-034 trap avoided**):

| Frame | Direction | Payload |
|---|---|---|
| `browser_webrtc_offer` | SPA → gateway | viewer SDP offer (non-trickle, all candidates gathered) |
| `browser_webrtc_answer` | gateway → SPA | Pion SDP answer |
| `browser_webrtc_state` | gateway → SPA | `state` (connecting/connected/failed/fallback), **`fallback_reason`**, **`has_audio`** |

**Viewer input rides an `"input"` WebRTC data channel** carrying **byte-identical `BrowserInputFrame` payloads** (generated types `asyncapi-types.ts:383-450`); the carrier changes, the payload does not (`recon-digest` — "*input/control/tab-action can stay IDENTICAL over a data channel*"). The **WS `browser_input` path remains** for the JPEG fallback tier. Coordinate mapping is unchanged (`browserLiveCoords.ts mapClientToDevice:56-76`, reading `video.videoWidth/Height` instead of `<img>.naturalWidth/Height`).

The **existing single-driver controller lock, SSRF gate, and rate limiter are reused unchanged** because they sit **below the transport**: input funnels through `handleInput` (`browser_ws.go:654`) → `LiveInput` (`live.go:59`) → `dispatchInput` (`:1167`), where `takeControl:1252`/`releaseControl:1271`/reject-non-controller:1169, `ValidateURL:1211` (SSRF), and rate-limit `:1231` all gate at the CDP-dispatch layer. The data-channel input path MUST be wired **in front of that same gate**, exactly as the WS path is today. `[FACT — recon-digest "Input"]`

```
CONFIDENCE (D4 — signaling + input over existing WS/DC, contract-first): High
  Basis   : Non-trickle SDP proven (Q1 copy-paste); input-over-DC proven (Q4);
            payloads and the controller-lock/SSRF/rate-limit stack are transport-below.
  Evidence: Q1/Q4; recon-digest file:line for the input gate and identical payloads.
  Missing : Single-driver arbitration across concurrent DC viewers unexercised (OI-3);
            DC text-vs-binary framing must use SendText (spike gotcha).
```

### D5 — Inherited foundations ported from the archive branch (recon-verified)

Three archive assets from `feature/live-browser-video-streaming @ 2e2701b1` are portable and load-bearing here:

- **`pkg/tools/browser/cdppipe/`** — pure-Go CDP-over-pipe chromedp transport (`allocator.go`, `pipeconn.go`, `dialer.go`, `frame.go` + tests; zero omnipus-internal imports → **clean cherry-pick**). It is **required** for capture (D2: `Extensions.loadUnpacked` works over the pipe only) **and** it closes ADR-044 §6.0.3's EC-3 security intent — no TCP debug surface, no `/json`, so a co-tenant agent process cannot reach CDP on **any** kernel. The fixed **debug port 9223 is removed**, and with it the Landlock connect/bind allow-list entries at `sandbox_apply.go:374-388/419`. `[FACT — recon-digest "Archive portable assets 1"]`
- **Coordinator rework** — `coordinator_lock_unix.go` (flock) + `coordinator_lock_other.go` replace the port-`net.Listen`/preflight/`RemoteAllocator` model with an atomic lockfile + `takeLaunchLock` + child contexts of the pipe `rootCtx` (**port-with-edits**). This is a real rework of a working subsystem — its true cost, carried from ADR-044 §6.0.3 pt-3. `[FACT — recon-digest "Archive portable assets 2"]`
- **Installer dual-download + capability** — `installer.go` (`fullChromeBuild()`, `EnsureChromiumBuild`, `selectDownloadBuild`, per-build subdirs, GoogHash verify) + `capability.go` (`ClassifyVideoCapability`) — **near cherry-pick, reworded** from A2 semantics to WebRTC: "video-capable" now means "full Chrome + Pion-compiled + supported platform → WebRTC-eligible", and the `chrome-headless-shell` fallback keeps agents browsing (with JPEG live view) where full Chrome is absent. `[FACT — recon-digest "Archive portable assets 3"]`

Do **not** port A2's WebCodecs-specific assets (`stream_relay.go`/GOP, `encoder_launch.go`, `encoderpage/`, `browser_ingest`/`browser_ws_video`/`browser_stream` gateway code, `BrowserChunkEnvelope`-family contracts, the SPA `VideoDecoder` path); Xvfb/PulseAudio were already removed on the archive itself (`effb14d3`). `[FACT — recon-digest "Archive portable assets 4"]`

```
CONFIDENCE (D5 — cdppipe / coordinator / installer inheritance): High
  Basis   : Recon-verified as portable; cdppipe is a clean-import cherry-pick and is
            also mandatory for the D2 extension-load path.
  Evidence: recon-digest file:line inventory of the archive branch.
  Missing : Coordinator rework is edits-not-copy (its own build task + browsing-
            equivalence regression gate, per ADR-044 §6.0.3).
```

### D6 — Encoder/publisher-page trust model: gateway-owned, token-authorized, no agent-facing capture API

The capture/publisher page is the **gateway-owned MV3 extension page** inside the managed Chrome, created and driven entirely via CDP (the gateway owns its target). Its ingest (publisher-PC signaling back to the gateway) is authorized by a **per-stream capability token delivered out-of-band via CDP `Page.addScriptToEvaluateOnNewDocument`** — never a URL query param, never recoverable from the CDP transport (loopback is **not** a trust boundary on a multi-tenant pod). `[FACT — ADR-044 §6.6 pattern, carried]`

Structural isolation is **stronger** than A2's EC-2 argument: `chrome.tabCapture` is an **extension-only API** *and* requires `--allowlisted-extension-id` matching the gateway-owned extension's pinned ID. An agent-navigated page is not an extension and cannot call it — there is **no agent-facing capture API at all**, on either the video or audio axis (audio arrives in the same `tabCapture` MediaStream, so there is no `getUserMedia`-on-a-monitor consent surface either). The process-global `--use-fake-ui-for-media-stream` stays **forbidden**. Signaling is behind the existing first-frame bearer auth + origin check + post-auth feature gate (`tools.browser.live_view_enabled`, `browser_ws.go:246`). Stream lifecycle (start/stop) and any ingest-auth rejection are audited. `[FACT — recon-digest; analogous to ADR-044 §6.0.2 EC-2]`

```
CONFIDENCE (D6 — publisher-page trust model): High
  Basis   : tabCapture is extension-API-only + allowlist-gated → no page-callable
            capture, structurally (stronger than A2's "no getDisplayMedia grant").
  Evidence: Q2 (--allowlisted-extension-id mandatory); ADR-044 §6.6 token pattern.
  Missing : Nothing structural. Audit wiring + a hostile-agent-tab regression test
            are build items (a "normal browsing unchanged" golden won't catch a
            posture change).
```

### D7 — Dependency policy: Pion in `go.mod`, lite-gated; UDP sandbox invariant documented

Pion (~15 pure-Go modules `[figure ASSUMPTION — verify at `go mod` time]`) lands in the **main `go.mod`**. The WebRTC feature is **lite-gated with the whatsmeow real+stub build-tag pattern** (real+stub file pair + blank-import init registration, precedent `gateway.go:55`) so `-tags lite` compiles Pion **out** and keeps the binary lean; **the JPEG path is the `lite` behavior**. `[FACT — recon-digest "Lite-build pattern"]`

Sandbox invariant (must be **preserved**, and documented as such): **Landlock net filtering is TCP-only** (`sandbox_linux.go:69-70`), so Pion's UDP binds and outbound STUN are **unfiltered today** and work without change. This is a *fragile* invariant — if Landlock ever gains UDP filtering (or a future ABI enforces it), Pion egress must be explicitly allow-listed. **Caveat:** a future TURN-over-TCP on a non-`{53,80,443}` port **would** be blocked by the connect allow-list (`sandbox.go:171`) — relevant only if the deferred TURN escalation (D3) is ever taken. Enforcement is gated on Landlock ABI v4 / kernel 6.7+ anyway (`sandbox.go:44-45`). `[FACT — recon-digest "sandbox"]`

Honest negative: ~15 new pure-Go modules is a non-trivial dependency/supply-chain surface (each a maintenance + `govulncheck` surface). The lite gate contains the *footprint* cost but not the *dependency-surface* cost on default builds.

```
CONFIDENCE (D7 — Pion in go.mod, lite-gated, UDP invariant): High
  Basis   : Lite real+stub pattern is established (whatsmeow); Landlock-TCP-only is
            code-verified; Pion is CGo-free.
  Evidence: recon-digest sandbox + lite-pattern file:line.
  Missing : Exact Pion module count/binary-size delta (measure at integration);
            supply-chain review of the Pion module set (route to supply-chain audit).
```

### D8 — Codecs negotiated via SDP; the WebCodecs iPad probe is dissolved

Codecs are negotiated over **SDP** — **VP8 + Opus proven** in the spike (Q3); Chrome also offers H.264. This **structurally dissolves ADR-044's EC-4** (whether Safari's *WebCodecs* `VideoDecoder` supports VP8/H.264-main): WebRTC does **not** use WebCodecs on the client — Safari's **native** `RTCPeerConnection` depacketizes and decodes VP8/H.264 as negotiated. Safari's WebRTC VP8+H.264 support is well established. `[FACT that Safari WebRTC supports VP8/H.264; the codec-mismatch class of bug in the WebCodecs path — context.md §5 "codec format mismatch" — is gone because SDP negotiates the actual codec]`

This is **not** a claim that "the iPad works": the operator's specific iPad, the negotiated codec on it, and **audio autoplay** (the panel already opens on an explicit user click — `ChatControls.tsx:69/77` — to satisfy autoplay policy) still belong in **UAT**. What is dissolved is the *WebCodecs decoder capability probe*, not device validation. `[INFERENCE — clearly separated]`

```
CONFIDENCE (D8 — SDP codec negotiation dissolves the WebCodecs probe): High (mechanism) / Medium (iPad UAT)
  Basis   : Q3 negotiated VP8+Opus; Safari WebRTC decodes VP8/H.264 natively.
  Evidence: Q3; Safari WebRTC codec support (training knowledge — flag: verify current).
  Missing : Operator-iPad UAT (codec + audio autoplay). Not a code dependency.
```

### 6.1 Component architecture

| # | Component | Location | Role |
|---|---|---|---|
| 1 | **Capture/publisher extension** | new `go:embed`ed MV3 extension, seeded to `$OMNIPUS_HOME` (skills `SeedDefaults` pattern) | Self-consumes the agent tab via `chrome.tabCapture`; publishes the audio+video MediaStream on a publisher `RTCPeerConnection` to gateway Pion. Gateway-driven via CDP; token-authorized. |
| 2 | **CDP-over-pipe transport** | ported `pkg/tools/browser/cdppipe/` (D5) | Carries **all** managed-Chrome CDP; **required** to load the extension (`Extensions.loadUnpacked`). No TCP surface. |
| 3 | **Coordinator (reworked)** | `coordinator.go` + `coordinator_lock_*.go` (D5) | One shared full Chrome; lockfile launch guard; child contexts of the pipe `rootCtx`. |
| 4 | **Pion SFU relay** | new, in `pkg/tools/browser` (lite-gated, D7) | Ingest PC (from component 1) + N egress viewer PCs; shared `TrackLocalStaticRTP` per kind; PLI→ingest on viewer join; input DC → CDP dispatch. |
| 5 | **Wire contract** | `contracts/asyncapi.yaml` (D4) | `browser_webrtc_offer/answer/state`; `browser_screencast` **retained**. |
| 6 | **SPA WebRTC path** | `browserLiveWs.ts` (signaling), `BrowserLiveView.tsx` (`<video>`+`<audio>`, new surface), `browserLiveCoords.ts` (unchanged math) | Viewer PC; renders video track, plays audio; sends input over the DC; falls back to the existing `<img>` JPEG path on `fallback` state. |

### 6.2 Data flow

```
 AGENT TAB (full Chrome --headless=new)
   │ chrome.tabCapture (extension-only API; --allowlisted-extension-id)
   ▼ MediaStream (video + audio TOGETHER)
 CAPTURE/PUBLISHER EXTENSION PAGE ──publisher PC (WebRTC, token-auth)──►  GATEWAY PION (ingest PC)
                                                                              │  raw RTP, no transcode
                                                                              ▼
                                                     SHARED TrackLocalStaticRTP (video, audio)
                                                          │ AddTrack → each viewer PC
        ┌─────────────────────────────┬───────────────────────────────────┐
        ▼                             ▼                                    ▼
   VIEWER SPA (egress PC)       VIEWER SPA                         (new viewer: PLI→ingest → keyframe)
   <video>+<audio>; input ──"input" DC──► GATEWAY ── controller lock / SSRF / rate-limit ──► CDP Input.dispatch on agent tab

 FALLBACK (per-viewer, on ICE fail / no full Chrome / lite / non-Linux):
   CDP Page.startScreencast (JPEG) ── browser_screencast (WS) ──► SPA <img>   [no audio]
```

## 7. Consequences

### Positive
- Audio+video together, no sidecar (FR-A1) — the decisive requirement, met in the base architecture.
- Input starvation regression eliminated by construction (FR-A2; Q4 p95 21 ms).
- Native congestion control / NACK / jitter buffer — smooth degradation on lossy WANs.
- Deployability preserved: worst case = today's JPEG experience, no TURN required in v1 (D3).
- Pure-Go, single-binary, CGo-free preserved (Pion); contract-first preserved (D4).
- Net attack-surface reduction: no CDP TCP port (cdppipe), no page-callable capture API (D6).

### Negative
- ~15 new pure-Go dependency modules (Pion) — supply-chain + `govulncheck` surface (D7).
- Capture depends on `--allowlisted-extension-id` / `--enable-unsafe-extension-debugging` — Chrome-flag-removal risk; mitigated by the JPEG fallback + CfT version pinning + a capture CI smoke test.
- A new SPA media surface (`<video>`/`<audio>`/RTC) where none exists today (`src/` has no `<video>`/RTC) — genuinely new code, new UAT surface.
- Coordinator rework and the pipe transport are real reworks of working subsystems (D5), gated by a browsing-equivalence regression.
- Two live-view rendering paths to maintain (WebRTC + JPEG) — the honest cost of keeping the fallback.

### Neutral
- The full-Chrome installer default and the CDP-over-pipe motivation were already ADR-044 decisions; this ADR inherits, not re-decides, them.
- Annotate crop must read `canvas.drawImage(video)` on the WebRTC path (vs `<img>.naturalWidth` on JPEG) — a mechanical adaptation, not a design change.

## 8. Open Items (not blockers)

- **OI-1 — External UDP traversal (operator test pending).** Whether inbound UDP hole-punch to the pod's srflx mapping succeeds from a real external network is unproven (Fly forwards only TCP:8080). **Cannot change the architecture** — fallback (D3) means failure = today's JPEG experience — but it **determines the reach of the WebRTC benefit**: if external UDP is chronically blocked and no TURN ships, external users effectively get JPEG while LAN/good-NAT users get WebRTC. Escalation if chronic: embedded pure-Go TURN, or ICE-TCP over a reachable forwarded port (deferred, decide post-measurement). Ops-guidance, not a gate.
- **OI-2 — Capture-follow-active-tab on tab switch.** The spike proved a stream **survives navigation** of the captured tab (Q2 T4), but **tab-switch re-capture** (re-`getMediaStreamId` for a new target tab ID) is unproven, and the Chrome-151 hidden-tab-needs-own-window learning may bear on capturing a backgrounded agent tab. Implementation detail with spike evidence on one side only.
- **OI-3 — Multi-agent concurrency / single-driver arbitration.** v1 scope: one shared Chrome, per-agent managers, capture the **attached agent's active tab**, **one active stream per agent**. The product's existing single-driver controller lock is NOT exercised by the throwaway spike (all viewers funnelled to one shared CDP session); D4 requires wiring it in front of the data-channel input path — architecturally sound, integration-unproven.

## 9. Risks and Caveats

- **Chrome-flag dependency (D2):** capture-critical flags could be removed by a future Chrome. Mitigation: JPEG fallback + CfT version pinning + capture smoke test in the browser-tool CI suite.
- **External traversal unknown (OI-1):** benefit-reach risk, bounded by fallback.
- **Supply-chain (D7):** route the Pion module set through the supply-chain risk auditor before merge; `govulncheck` must stay green (Constraint #7).
- **Sandbox UDP invariant (D7):** Landlock-TCP-only is what lets Pion UDP work unfiltered today; a future Landlock UDP capability would silently break egress unless allow-listed — document as an invariant with a guard test.
- **Two-path maintenance (D3):** WebRTC and JPEG rendering paths both live in the SPA and the gateway; accept as the cost of graceful degradation.

## 10. Confidence Assessment

Direction (WebRTC/Pion over WebCodecs-A2): **High** — the full target pipeline (capture→Pion SFU→viewer, media + input + audio) was proven end-to-end in-pod (Q2/Q3/Q4), the decisive requirement (FR-A1) eliminates A2, and the fallback (D3) neutralizes B's only ADR-044 losing criterion (NFR-2). Capture mechanism: **High** (measured). Codec/iPad: **High mechanism / Medium device-UAT**. The single genuinely open empirical question — external UDP traversal (OI-1) — cannot flip the architecture, only the reach of its benefit, precisely because the JPEG fallback is retained.

## 11. Validation / Next Steps

1. **Operator external traversal run (OI-1):** open the preview `/view`, DEFAULT mode, drive the tab; a connected `srflx/udp` selected pair proves TURN-free traversal with real media + input. Read-out per `wv1-spike-results.md` Q1.
2. **`/plan-spec`** this ADR — capture extension + `go:embed` seed, Pion SFU relay (lite-gated), cdppipe port + coordinator rework, installer/`ClassifyVideoCapability` reword, contract-first `browser_webrtc_*` frames, SPA WebRTC render + input DC + JPEG-fallback switch, with FR-A1/FR-A2/NFR-A3 + the degradation ladder as acceptance anchors.
3. **`/grill-spec`** the resulting spec (must PASS), then **`/taskify`**, then implement under the wave pattern + 7-reviewer gate + UAT (operator conventions).
4. **Supply-chain review** of the Pion module set before merge; capture CI smoke test; browsing-equivalence regression gating the cdppipe/coordinator swap.

## 12. Relationship to ADR-044

- **Superseded by this ADR:** ADR-044 §6.0–§6.4 (WebCodecs-over-WS "A2" transport; CDP `Page.startScreencast` as the capture mechanism; the encoder-page-fed topology; the A2-only "unavailable state" degradation; the M-10 removal of `browser_screencast`, now **cancelled**).
- **Still valid (decision history + inherited):** ADR-044's rejection of Options C/D (§5); the full-Chrome installer rationale and dual-download (§6.5 + 2026-07-17 amendment); the CDP-over-pipe motivation and the coordinator/9223 rework (§6.0.3); the single-shared-Chrome topology (ADR-043); the STRIDE trust framing (§6.6). ADR-044's **Option B is the decision promoted here**.
- **Prior ADRs untouched:** ADR-038/040/041 interaction model, ADR-043 per-agent Chrome contexts — all preserved (input/attach/annotate/multi-viewer unchanged, D4).
