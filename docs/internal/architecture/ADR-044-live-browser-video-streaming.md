# ADR-044: Live-browser streaming target architecture — WebCodecs relay over the existing gateway WebSocket

> **SUPERSEDED IN PART by ADR-047 (2026-07-18):** the A2/WebCodecs transport+capture
> decisions (§6.0–6.4) are superseded by WebRTC/Pion; the R3/M-10 `browser_screencast`
> contract removal is CANCELLED — JPEG screencast is retained as the automatic
> fallback tier. Retained as decision history.
>
> **⛔ PLATFORM-SUPPORT WARNING (2026-07-21).** This ADR describes the **A2** design
> (headful Chrome on **Xvfb** + a **PulseAudio** null-sink + ffmpeg encoding) which was
> **never built**. Any statement in this document about video being **Linux-only** —
> notably §6.0.1 item 4 — rests on that dead premise and **must not be cited as the
> current reason**. The shipped architecture (ADR-047 §D2) captures via an in-Chrome
> `chrome.tabCapture` MV3 extension + in-process Pion SFU, uses **no Xvfb, no
> PulseAudio, no ffmpeg**, and is cross-platform Chrome API throughout.
> **For real, code-verified platform support read ADR-047 §13.**

- **Status:** **Accepted 2026-07-16** (operator: Daniel Piatkowski); **§6.0 "A2" amendment + §6.0.1 "R3 reconciliation" + §6.0.2 "Gate 0 CI-worker results" are authoritative** over the original §6.1–6.6 body where they differ (newest wins). **Gate 0 partial (2026-07-16, `ci-omnipus`): EC-1 fps PASS at a measured 30 fps (min-spec re-run pending); capture mechanism RESOLVED to CDP `Page.startScreencast` → WebCodecs encoder page (NOT `getDisplayMedia`); EC-2 isolation structural; EC-4 iPad DEFERRED — see §6.0.2. EC-3 CDP-token confidentiality: after round-4 showed "keep port 9223 + Landlock isolation" is kernel-6.7-gated (insecure on most installs), RESOLVED to a pure-Go CDP-over-pipe transport (no TCP surface, kernel-independent) — see §6.0.3 (authoritative, newest).** Amends ADR-038 D3 (JPEG screencast → default becomes the WebCodecs relay; **the dead JPEG message is REMOVED from the contract in v0.3 — R3/M-10; it is NOT a live fallback tier, and there is NO A1 tier. Degradation is an explicit "video-capable browser required" unavailable state**, operator decision 2026-07-16, "Stay A2-only"). The client-side-rendering family was researched as a first-class candidate at operator request and **rejected** — see the companion comparison (`docs/internal/architecture/browser-display-comparison.html`) and the one-line rejections in §6. Implementation remains **gated on the Phase 0 spikes** (§9); acceptance settles direction, not the open mechanical unknowns (G-1..G-5).
- **Date:** 2026-07-16
- **Deciders:** Daniel Piatkowski (operator)
- **Evidence level (highest used):** 1 (user input + codebase facts), with tagged inference/assumption where noted
- **Routing:** v0.3 scope (structural) per the release strategy; supersedes the interim tuning levers recorded in the `browser-live-responsiveness` research note.
- **Supersedes for this decision:** the reopened client-side-rendering investigation (rewriting-proxy / DOM-mirroring) — resolved against, grounded in the three-pass web research and the comparison document.

## 1. Problem Understanding

The live browser panel (ADR-038/040/041) streams the agent's Chrome viewport to the SPA as **full-page JPEGs**: CDP `Page.startScreencast` → JPEG (Q60, 1280×720, every frame — constants in `pkg/tools/browser/live.go:20-26`) → base64 inside a JSON **text** WS frame (`pkg/gateway/browser_ws.go:78-83` marshals frames via `json.Marshal`) → SPA `<img>` swap (`BrowserLiveView.tsx`, `imgRef`). Each frame also costs a CDP ack round-trip serialized through an ack worker (`live.go` `ackCh`/`runAckWorker`). `[FACT]`

Measured consequence on the reference deployment (dev pod behind the Fly proxy): 100–500 ms per frame round-trip → scroll lags by that much per step, video collapses to a few fps, and there is **no audio at all** — the managed binary is `chrome-headless-shell` (`pkg/tools/browser/installer.go:23`, Chrome-for-Testing download id), which has no audio stack, and the wire format has no audio channel. `[FACT]` The operator has rejected incremental tuning (quality/resolution/frame-skip) as the path: **go straight to the target architecture.** `[FACT — user input]`

Blast radius: the streaming leg only. The attach flow (`/api/v1/browser/ws`, `BrowserAttachFrame`), the take-the-wheel input path (viewer input → CDP `Input.dispatch`, ADR-040/041), multi-viewer piggyback (`LiveViewRegistry`/`FrameSink`), and the annotate flow must keep working unchanged. `[FACT — contracts/asyncapi.yaml:501-530, live.go FrameSink]`

## 2. Extracted Requirements

### Functional
- FR-1: The SPA MUST render the live browser viewport with smooth scrolling and watchable in-page video (target: perceived scroll latency well under the current 100–500 ms/frame; video ≥ 15 fps at panel size). Numbers are targets, not measurements. `[INFERENCE from operator complaints; exact thresholds ASSUMPTION]`
- FR-2: The existing attach / take-the-wheel input / annotate / multi-viewer flows MUST keep working unchanged. `[FACT — user input]`
- FR-3: Audio SHOULD become possible in the same architecture (it is a known gap the operator has raised), but MAY land as a later phase. `[FACT — prior conversation]`
- FR-4: A new viewer attaching mid-stream MUST get a first paint promptly (≤ ~1 s), including on the multi-viewer piggyback path. `[INFERENCE from existing lastFrame-replay behavior in live.go]`

### Non-Functional
- NFR-1 (compat): MUST work in the operator's primary client, **Safari on iPad**, plus Chrome/Edge/Firefox. `[FACT — operator environment established this session]`
- NFR-2 (deployability): MUST work wherever the gateway itself works — i.e. through any HTTPS reverse proxy/tunnel with **no extra ports, no UDP requirement, no per-install media configuration**. Omnipus is community self-hosted software; deployments are heterogeneous. `[FACT — project positioning]`
- NFR-3 (footprint): security/feature RAM overhead minimal (Constraint #3, < 10 MB steady-state in the Go process for this feature). `[FACT — CLAUDE.md]`
- NFR-4 (degradation): MUST degrade gracefully where the new pipeline is unavailable (old managed browser, SPA without WebCodecs, constrained platforms) — Constraint #4. `[FACT — CLAUDE.md]`

### Constraints
- C-1: **Pure Go, no CGo, no external C libraries** — the Go binary can never link ffmpeg/libvpx/x264; Go-side video encoding is off the table. `[FACT — Constraint #2]`
- C-2: **Single Go binary**, SPA embedded. `[FACT — Constraint #1]`
- C-3: **Contract-first wire formats** — any new frame lands in `contracts/asyncapi.yaml` before code (Constraint #8). `[FACT]`
- C-4: The managed-browser installer currently downloads `chrome-headless-shell`; the Chrome-for-Testing manifest also publishes the full `chrome` build under the same versioning scheme, so switching is a download-id change plus size/behavior consequences, not a new install mechanism. `[FACT — installer.go:21-23 + CfT manifest structure]`

## 3. Gaps and Ambiguities

| # | What's missing/ambiguous | Why it matters | Likely assumption if unresolved | Question to resolve |
|---|---|---|---|---|
| G-1 | Do the tab-capture auto-accept flags (`--auto-select-tab-capture-source-by-title` / `--use-fake-ui-for-media-stream`) work in the **Chrome-for-Testing full build running headless (`--headless=new`)**? | Option A's capture mechanism depends on it | Yes — these are standard Chromium flags used by CI capture setups `[ASSUMPTION]` | Spike S-1 (half a day): launch CfT full Chrome headless, open a companion page, `getDisplayMedia` a target tab |
| G-2 | WebCodecs **encoder** codec support in headless full Chrome (H.264/`avc1.*` vs VP8) | Codec choice drives Safari compat | VP8 encode certain; H.264 encode present on desktop builds `[ASSUMPTION]` | Spike S-1 measures `VideoEncoder.isConfigSupported` matrix |
| G-3 | WebCodecs **decoder** support in Safari/iPadOS (VideoDecoder ships 16.4+; H.264 expected, VP8 doubtful) | The operator's device must decode | H.264 decode works in current Safari `[ASSUMPTION — flag: verify against current Safari]` | Spike S-2 (hours): capability probe page on the operator's iPad |
| G-4 | Can a **headless** full Chrome capture **tab audio** via `getDisplayMedia({audio:true})` with no system audio device? | Determines whether audio is phase 2 or needs more (virtual sink) | Unknown — genuinely uncertain `[UNKNOWN]` | Spike S-3 (day): part of the same test harness |
| G-5 | RAM/download delta of full Chrome vs headless-shell on small self-host boxes | NFR-3 neighborly-ness; affects default vs opt-in | Full build is larger on disk and somewhat heavier at runtime; magnitude unmeasured `[UNKNOWN — do not quote figures until measured]` | Spike S-4: measure both binaries idle + one tab on the pod |
| G-6 | Fly-proxy/UDP availability for typical Omnipus installs | Decides Option B's viability as a *default* | Most self-host proxies (nginx/caddy/cloudflared) pass WSS trivially, UDP rarely `[INFERENCE]` | Not needed if Option A chosen; revisit only if B resurfaces |

None of these change *which option wins* under the stated constraints (see §6); they shape the implementation plan and the audio phase. G-1–G-3 are committed pre-implementation spikes.

## 4. Decision Criteria

| Criterion | Weight | Notes |
|---|---|---|
| Deployability parity (works wherever the gateway works, zero new ops) | 30% | NFR-2 — community self-host is the product |
| Responsiveness gain (scroll latency, video fps) | 25% | The reason this ADR exists |
| Constraint fit (pure Go, single binary, contracts) | 20% | Hard gates, not preferences |
| Safari/iPad client compat | 10% | Operator's device (NFR-1) |
| Path to audio | 10% | FR-3 |
| Implementation + operational complexity | 5% | Weighted low because the operator chose "target architecture" over incrementalism |

## 5. Option Analysis

### Option A — WebCodecs relay over the existing gateway WS (recommended)

A companion "capture page" runs inside the managed Chrome (full build, headless). It captures the target tab via a **scoped capture mechanism** (a dedicated capture browser context, or an embedded extension using `chrome.tabCapture` — **never a process-global media auto-accept flag**, which would let any agent-browsed page silently capture the screen; see §6.6 security), encodes with WebCodecs `VideoEncoder` (H.264 preferred, VP8 next — negotiated against the SPA's declared `VideoDecoder` capabilities at attach), and ships `EncodedVideoChunk`s as **binary** messages to the gateway over an **authenticated loopback ingest endpoint** (§6.3, §6.6) — loopback is not itself a trust boundary on a multi-tenant pod. The gateway relays chunks to attached viewers on the existing `/api/v1/browser/ws`, maintaining a **GOP cache** (last keyframe + subsequent deltas) so a newly attached viewer replays from the keyframe — the video-era equivalent of today's `lastFrame` piggyback. Backpressure: the relay watches per-viewer queue depth and sends a control frame to the capture page to step the encoder bitrate/framerate down/up (ABR without WebRTC). The SPA decodes with `VideoDecoder` onto a canvas that replaces the `<img>` — coordinate mapping for take-the-wheel input is unchanged (same viewport metadata fields). Audio (phase 2, gated on Spike S-3): `AudioEncoder` → Opus chunks over the same channel.

| Dimension | Assessment |
|---|---|
| Strengths | All codec work happens in the two Chromes — the Go binary stays a pure byte relay (C-1 satisfied by construction). Transport = the existing authenticated WSS: works through every proxy/tunnel the app already works through (NFR-2 = 100%). One wire, one auth model, contract-first friendly. Keyframe/GOP fan-out preserves multi-viewer semantics. Clear audio path. |
| Weaknesses | TCP transport → head-of-line blocking on lossy WANs (no NACK/FEC); ABR is homegrown (queue-depth signal) rather than transport-cc. Requires switching the managed download to full Chrome (bigger footprint, G-5). Capture-page lifecycle is a new moving part inside the browser manager. |
| Risks | G-1 flags may not behave in CfT-headless (kills the capture mechanism → fallback is an extension + `chrome.tabCapture`, which full Chrome headless supports via `--load-extension` `[ASSUMPTION]`); Safari decode matrix (G-3); silent regression risk to agents' normal browsing when the binary switches (mitigate: same CfT version pinning, run the browser tool suite against full build in CI). |
| Complexity | Moderate: capture page (~200 lines JS, embedded in the binary), relay + GOP cache in Go (~300 lines), SPA decoder path (~200 lines), installer switch, contracts. No new infra. |
| Cost implications | Build: one focused epic. Run: encoding cost moves into Chrome (hardware-accelerated where available); Go relay is cheap. Larger Chrome download/disk per install (G-5). |
| Operational impact | None beyond today — no new ports, no STUN/TURN, no certificates. Monitoring = existing WS metrics + a frames/bitrate gauge. |

### Option B — Full WebRTC via Pion (pure-Go WebRTC stack)

Same browser-side encoding idea, but transported as real WebRTC: the capture page publishes a PeerConnection; the Go gateway runs Pion as an SFU-style relay (pure Go `[FACT — Pion is CGo-free]`); the SPA subscribes with a second PeerConnection. Signaling over the existing WS.

| Dimension | Assessment |
|---|---|
| Strengths | Best-in-class congestion control (transport-cc/GCC), NACK/PLI recovery, jitter buffers — the right answer on lossy, high-RTT WANs. Native audio tracks. Battle-tested media semantics. |
| Weaknesses | ICE/UDP is a per-deployment ops problem: most self-host reverse proxies pass WSS but not UDP; the fallback (TURN/TCP or ICE-TCP) needs per-install configuration and sometimes a public TURN server — exactly the "works only after networking homework" experience Omnipus avoids (NFR-2 hit). Meaningful binary-size and API-surface growth. `[INFERENCE]` |
| Risks | Support burden: every proxy/tunnel/NAT combination becomes a potential media-failure ticket while the rest of the app works fine. Two transports to keep authenticated and audited. |
| Complexity | High: ICE lifecycle, DTLS/SRTP, SDP negotiation on three legs, TURN story, plus everything Option A needs anyway (capture page, installer switch). |
| Cost implications | Build: 2–3× Option A. Run: comparable; TURN relay bandwidth where needed. |
| Operational impact | New ports/UDP or TURN per install; media-specific debugging skills required. |

### Option C — Status-quo-plus (tuned MJPEG) — rejected as target by the operator

Binary WS frames, immediate-ack + latest-frame-wins, panel-size capture, RTT-adaptive JPEG quality. `[FACT — operator explicitly rejected as the target: "we should straight go to the target architecture"]`

| Dimension | Assessment |
|---|---|
| Strengths | Days of work; no installer change; zero compat risk. |
| Weaknesses | Physics ceiling: full-frame JPEG per repaint can never deliver smooth scroll or video on constrained links; no audio path at all. |
| Risks | Investment in a dead-end pipeline that the target architecture then replaces. |
| Complexity | Low. |
| Cost implications | Low build, wasted once A lands. |
| Operational impact | None. |

Documented for the record: elements of C that are *shared groundwork* for A (binary WS frames on this channel, latest-wins queueing — already partially present in `browser_ws.go`'s drop-on-full design `[FACT]`) are folded into A's plan rather than done as a separate phase.

### Option D — Client-side rendering (iframe in the user's browser) — rejected on requirements, recorded per operator question (2026-07-16)

Raised as "why ship pixels at all — render the page in an iframe in the browser the user already runs?" Rejected because the live panel's requirement is to mirror **the agent's server-side browser session**, not to render a URL:

| Blocker | Detail |
|---|---|
| Wrong session | An iframe loads a fresh copy with the USER's cookies/state; the agent's session (logins, form state, CDP-driven navigation) lives in the server-side Chrome. Watching and take-the-wheel (FR-2) are impossible by construction. `[FACT — architecture]` |
| Framing is widely forbidden | `X-Frame-Options`/`frame-ancestors` block iframing on much of the real web. The rewriting-proxy workaround (strip headers via the gateway) breaks logins/CSP/service-workers and creates an SSRF/credential-leak surface. `[FACT — web platform behavior]` |
| Cross-origin opacity | Even where framing is allowed, the SPA cannot read the iframe's URL/DOM or inject input — the ADR-040 interaction model (URL bar, annotate, drive) cannot be built on it. `[FACT — same-origin policy]` |

Note: the iframe pattern is already used where it is sound — the `web_serve` preview listener (port 5001) frames agent-generated pages whose headers we control. `[FACT]` The related **DOM-mirroring** family (rrweb-style replay) was also weighed: low bandwidth and crisp text, but canvas/WebGL/video/cross-origin content does not mirror, scripts must be stripped (faked interactivity), and the input round-trip still needs the server browser — a large surface for lower fidelity. `[EXPERT REASONING]` Pixel/video streaming of the real session remains the only approach that satisfies FR-1/FR-2 as stated; it is also the approach used by comparable products (OpenAI Operator/ChatGPT Agent stream frames). `[FACT — publicly documented behavior; flag: based on training knowledge]`

## 6. Decision & Target Architecture

> ### ⚠️ 6.0 — AMENDED TO "A2" post-spike (2026-07-16, AUTHORITATIVE; operator-confirmed)
> The Phase-0 spikes (`ADR-044-spike-results.md`) disproved the original capture assumption below. **`getDisplayMedia`/`chrome.tabCapture` do NOT work in a truly headless browser** (no display; extension capture needs a user gesture) — full Chrome does not change that. The WebCodecs encoder itself, and Opus audio, DO work in the shipped `chrome-headless-shell`, so the *encoder* never justified full Chrome. **What justifies full Chrome is capture: smooth, framerate-correct video (incl. full-motion playback) requires a real framebuffer, i.e. the full browser run HEADFUL on a VIRTUAL DISPLAY** — the setup every smooth-video vendor uses (Mux/neko/Steel/Kasm). The operator chose this "A2" path (2026-07-16).
>
> **A2 target architecture (supersedes §6.1's headless capture-page assumption; the relay/GOP/ingest/SPA-decode design in §6.1–6.3 otherwise stands):**
> 1. **Managed browser = full Chrome, run HEADFUL** (`--window-size`, not `--headless`) under `dbus-run-session`, pointed at a **virtual display**. (Proven: headful Chrome loads pages on Xvfb here once a dbus session is provided.)
> 2. **Virtual display sidecar (Xvfb)** — a supervised child process (Go `exec`s it, `DISPLAY=:N`), exactly like the Signal channel's `signal-cli` and the audio daemon. Gives Chrome a framebuffer so `getDisplayMedia`/`tabCapture` work at framerate.
> 3. **Audio sidecar (PulseAudio)** — supervised daemon + `module-null-sink` + `module-remap-source`; tab audio routes to the sink, captured via `getUserMedia` on the monitor. **Proven (S-3)**; decoupled from video capture; Go orchestrates it (native-protocol `LoadModule`, NoiseTorch pattern), does not implement it.
> 4. **Capture page (in the headful Chrome):** `getDisplayMedia` (video, now works — there is a display) + `getUserMedia` (audio, sink monitor) → WebCodecs `VideoEncoder` + `AudioEncoder` → binary chunks over the authenticated loopback ingest (§6.3). Both encoders run in the browser (pure browser, no ffmpeg/CGo).
> 5. **Relay + GOP cache, SPA `VideoDecoder`/`AudioDecoder` decode, drive/annotate via CDP — unchanged from §6.1–6.3.**
>
> **Codec policy CORRECTION (spike S-1b):** the encoder supports **H.264 *main* (`avc1.4D40…`)**, VP8, VP9, AV1 — **NOT** H.264 *baseline* (`avc1.42E01E`). §6.4's "H.264 baseline preferred" is wrong; use **H.264-main-first, VP8 next** (decoder-side confirmed per-viewer via `video_caps`).
>
> **Full Chrome is now DECIDED (default for video-capable installs)** — justified by capture, not encoding. Runtime cost (accepted): +120 MB browser download, an Xvfb sidecar, a PulseAudio sidecar, more RAM/CPU (SwiftShader on GPU-less pods). The **Go binary stays single**; the deployment *image* gains Xvfb + PulseAudio as supervised sidecars. Installs without them fall to the unavailable state (Constraint #4), not JPEG.
>
> **Open before build:** (a) confirm the integrated capture **fps on CI/`ci-omnipus`** (the dev-pod sandbox blocked the measurement; expected ~25–30 fps per Steel/Mux); (b) the capture-consent isolation detail — `getDisplayMedia` auto-accept flags are process-global, so the capture must be isolated (dedicated context/instance) so an agent-browsed page cannot also capture (carries CRIT-002 forward). Both are spec/plan items. **→ These, plus CDP-token confidentiality and iPad decode, are now the four Gate-0 exit criteria in §6.0.1.**

> ### ⚠️ 6.0.1 — R3 RECONCILIATION post round-2 grill-spec (2026-07-16, AUTHORITATIVE; operator: "Stay A2-only, revise R3")
> The round-2 grill-spec review (`docs/internal/specs/live-browser-video-streaming-spec-review-round2.md`) returned **BLOCK** (3 CRITICAL, 11 MAJOR). The operator chose to **stay A2-only** (no A1 headless-screencast fallback tier). This block amends §6.0/§6.3/§6.4/§6.6 accordingly; the plan-spec R3 carries the full behavior.
>
> 1. **Sequencing (C-1) — Gate 0 precedes the build.** The four open items become **hard, pre-epic exit criteria** measured on `ci-omnipus` **before** the installer-default flip and the `managedExecAllocatorOpts` headful/CDP edits: **EC-1** integrated distinct fps ≥ 24; **EC-2** capture isolation proven; **EC-3** CDP-token unrecoverable; **EC-4** iPad decodes H.264-main or VP8. **Fail branch is A2-only:** any EC failing ⇒ **do NOT ship A2; re-open this ADR** — there is no A1 to fall back to (the honest cost of A2-only). The 25–30 fps figure is a vendor analogy, not this stack's measurement.
> 2. **Capture isolation (C-2) — mechanism named, no process-global flag.** The process-global `--use-fake-ui-for-media-stream` is **FORBIDDEN**. Consent is granted via **CDP `Browser.grantPermissions({origin, permissions:['displayCapture']})` scoped to the capture-page origin**; source auto-selection is bound to the per-stream key. The **capture mechanism is a Gate-0 output**: EC-1/EC-2 measure BOTH (a) `getDisplayMedia` on headful (origin-scoped consent) AND (b) `Page.startScreencast`-on-headful→WebCodecs (isolation-safe by construction, unproven fps), and select the one that passes fps AND denies an agent-navigated page. Escalation if neither is both smooth and isolable: re-open this ADR (separate pipeline / WebRTC).
> 3. **CDP-token confidentiality (C-3) — FURTHER SUPERSEDED by §6.0.3 (round-5): the keep-9223 mechanism described below is itself reversed — CDP now runs over `--remote-debugging-pipe`. Also note its premise below ("agent processes are sandboxed without 9223 in their connect allow-list") was code-false — 9223 IS in `ConnectPortRules` at `sandbox_apply.go:419`. Retained only as decision history.** (Original R5 text:) The R3 plan to *replace* the fixed port 9223 is **retracted**: chromedp v0.15.1 has no `--remote-debugging-pipe` (it reads a `ws://` URL from Chrome's stdout), and an ephemeral port breaks **both** the Landlock connect allow-list (`sandbox_apply.go:419`, fixed 9223) **and** the ADR-043 shared-Chrome coordinator (`coordinator.go` dials `ws://127.0.0.1:9223` and proves ownership by the fixed-port holder). **The fixed port 9223 is KEPT**; confidentiality is closed by **process isolation** — agent tool processes are Landlock-sandboxed without 9223 in their connect allow-list, and the ingest token is delivered via `Page.addScriptToEvaluateOnNewDocument`, never in `/json`. EC-3 is an **empirical** pre-build test that a sandboxed agent process can neither dial 9223 nor read the token. §6.6 amended. (Operator decision: "keep 9223, isolate access.")
> 4. **A2-only degradation (M-1, M-3).** There is **no A1 tier.** Video is **Linux-only** (Xvfb + PulseAudio are Linux-only); macOS / Windows / Termux / no-Xvfb installs get the **unavailable state**, and agent browsing still works there (headless). This deliberately withholds a proven-lighter experience from those installs — recorded as an accepted operator scope cost, not a technical necessity.
>    > **⛔ PREMISE VOID (2026-07-21) — do not cite this item as the reason video is Linux-only.** The "Xvfb + PulseAudio" mechanism this rationale rests on **was never built**: ADR-047 §D2 replaced A2 with an in-Chrome `chrome.tabCapture` MV3 extension + in-process Pion SFU, and a repo-wide search of `pkg/` finds **zero** references to Xvfb, PulseAudio or ffmpeg. The capture code (`captureext/embedded/encoder.js`) is standard cross-platform Chrome API with no platform branch. The `goosForCapability != "linux"` check in `capability.go` survived the mechanism swap and was never re-derived — for **macOS** no code-level blocker has been found (untested, so not a claim that it works); for **Windows** there IS a real blocker, but it is `cmd.ExtraFiles` in the CDP-pipe transport (`cdppipe/allocator.go:232`, unsupported on Windows per the Go stdlib), which breaks all managed browsing, not just video. **Authoritative record: ADR-047 §13.** This item is retained as decision history only.
> 5. **Wire cleanups (M-10, m-3, O-1).** The `browser_screencast` message is **removed** from the contract (v0.3 = no back-compat). **Correction (R5/F-02): it is NOT "dead"** — it is today's sole live-view transport (emitted `browser_ws.go:546`, consumed `browserLiveWs.ts:147`); its removal is the cutover of the only live-view path and **withdraws working JPEG live view from non-video-capable installs** (they get the unavailable state — the true A2-only cost), so it must not precede the video path being reachable on video-capable installs. The video-chunk envelope timestamp is **`ts:u64`** (not the non-standard `u48`). `browser_stream_bitrate` (ABR) is **deferred to v1.1** and is not in the v1 contract.
> 6. **Ingest token lifecycle (M-5).** The ingest token is **stream-lifecycle-scoped with a single active holder** (not single-use-per-connection): the capture page may reconnect with the same token while the stream is alive; a concurrent duplicate is rejected; the token dies when the stream ends. This survives a transient ingest-WS drop without permanently killing the stream.
> 7. **Footprint gate (M-9).** A **total default-footprint acceptance number + a documented min video-capable spec** is a release gate (measured on `ci-omnipus` before `/taskify`); below-min-spec installs classify not-video-capable. The Go-process < 10 MB budget arithmetic is shown in the plan-spec.
> 8. **Ingest fragmentation dropped (M-4).** No fragmentation/reassembly: the ingest single-message bound is sized to the max keyframe (≥ 2 MB); an over-bound chunk is rejected and the encoder steps down. Audio (M-11): PulseAudio is best-effort on a stable socket path and **never blocks the Chrome launch**.

> ### ✅ 6.0.2 — GATE 0 CI-WORKER RESULTS post-run (2026-07-16, AUTHORITATIVE — newest; capture mechanism RESOLVED)
> Gate 0 EC-1/EC-2/EC-3 were run on the CI worker `ci-omnipus` (16 GB / 8-core, root, **no dev-pod agent sandbox** — the exit-144 that blocked the measurement on the dev pod is absent here). Setup: full Chromium at `/opt/ms-playwright/chromium-1223/chrome-linux64/chrome`; `apt install xvfb dbus-x11 pulseaudio ffmpeg`; harness = **headful Chromium on Xvfb** (`xvfb-run … dbus-run-session -- node`) playing + capturing a 720p30 full-motion test clip, driven by Playwright. These results **supersede the §6.0.1-point-2 "capture mechanism is a Gate-0 output" open question — it is now resolved in favor of mechanism (b).**
>
> 1. **EC-1 (fps ≥ 24) — PASS at 30 fps (measured, not analogy).** Headful Chromium on Xvfb delivers the full-motion clip at a **reproducible 30 fps** (CDP `Page.startScreencast` = 360 frames / 12 s, ×5 runs incl. software `--disable-gpu`), versus 4–12 fps on the *headless* path for the same clip. The **WebCodecs `VideoEncoder` keeps up end-to-end: VP8 720p30 = 30.1 fps, 361 frames, zero drops/errors.** The 25–30 fps vendor figure is now this stack's own number; the virtual display is confirmed as the source of smoothness. **Caveat (R5/F-04):** measured on `ci-omnipus` (16 GB / 8-core); SwiftShader software encode is host-dependent, so EC-1 MUST be **re-run at the intended min video-capable spec** (SC-016) before it is asserted to clear the shipping configuration — the min-spec, not the CI box, sets the shipped fps floor.
> 2. **Capture mechanism RESOLVED — (b) CDP `Page.startScreencast`, NOT (a) `getDisplayMedia`.** In-browser `getDisplayMedia` of the browser view is **empirically unreliable on a bare Xvfb — 0 for 3**: `preferCurrentTab` + tab-source flag → `NotReadableError`; entire-screen auto-select → **renderer crash / page teardown** (persists with `--disable-gpu`, so not a GPU fault); no-matching-flag → **infinite hang** (a picker with nothing to click). A bare Xvfb lacks the window-manager + xdg-desktop-portal/PipeWire a real desktop gives `getDisplayMedia`; no production browser-streaming vendor relies on it either. The reliable path — now **primary and only** — is **server-driven CDP `Page.startScreencast` on the headful agent tab → gateway relays the JPEG frames over the authenticated loopback to a controlled WebCodecs encoder page (`createImageBitmap` → `VideoFrame` → `VideoEncoder`) → encoded chunks back to the relay.** This is exactly the "mechanism (b)" §6.0.1 named as the isolation-safe alternative; Gate 0 promotes it to primary because (a) is fragile *and* (b) is strictly better on isolation and stays pure-Go.
> 3. **EC-2 (capture isolation) — PASS, structurally.** With mechanism (b) there is **no agent-facing video-capture API at all**: `Page.startScreencast` is a privileged CDP command issued by the gateway; an agent-navigated page cannot invoke it and no `getDisplayMedia` **video** grant exists anywhere. The C-2 process-global-consent risk (the R2 BLOCK) is **dissolved, not merely mitigated** — there is no video consent to scope. §6.6/C-2 is amended: `CDP Browser.grantPermissions` origin-scoping is **no longer needed for video**; it remains relevant only for the **audio** `getUserMedia` on the PulseAudio monitor (the one surviving media-consent surface, still origin-scoped to the encoder page).
> 4. **EC-3 (CDP-token confidentiality) — mechanism = KEEP port 9223 + process isolation (revised R5/F-01; empirical, not "by design").** The earlier `--remote-debugging-pipe` plan is **retracted**: chromedp v0.15.1 can't speak it, and an ephemeral port breaks the Landlock allow-list AND the ADR-043 coordinator (both keyed on 9223). Instead **keep the fixed port** (chromedp allocator, coordinator, sandbox all unchanged) and close confidentiality by **process isolation** — agent tool processes are Landlock-sandboxed without 9223 in their connect allow-list; the ingest token is delivered via `addScriptToEvaluateOnNewDocument`, never in `/json`. **EC-3 is an empirical pre-build test:** prove a sandboxed agent process can neither dial 9223 nor read the token. Escalation (netns / a custom CDP-over-pipe transport) only if zero-trust loopback is later required. (Operator decision 2026-07-16: "keep 9223, isolate access.")
> 5. **EC-4 (iPad decode) — DEFERRED (operator, 2026-07-16: "not testing iPad yet, slowly get to building").** Must test **H.264-main (`avc1.4D40…`)**, not VP8 — Safari WebCodecs VP8-decode support is doubtful and H.264 is the safe Apple codec. This is a device probe, not a code dependency; it does not block spec/taskify work up to the build. It remains a hard Gate-0 exit criterion before the epic ships.
> 6. **Consequences for the design body (applied below).** §6.1 component 1 "capture page (`getDisplayMedia`)" is replaced by **(1a) the agent tab, captured via CDP screencast with no injected code**, plus **(1b) a controlled encoder page** (receives JPEG frames → WebCodecs video encode; captures audio via `getUserMedia` on the sink monitor → Opus). §6.2 data-flow, §6.3 ingest, and §6.6/C-2 are amended to match. **Pure-Go is preserved** — the Go binary still only shuffles bytes (CDP JPEG in, encoded chunks out); all encode stays in Chrome. Net added cost vs mechanism (a): one loopback JPEG hop (~2.4 MB/s @ 30 fps 720p on localhost; `createImageBitmap` ~2–5 ms/frame) — negligible. The screencast frame is intra-frame JPEG re-encoded to inter-frame video — acceptable double-lossy for a live view.

> ### ⚙️ 6.0.3 — R6 / ROUND-4 GRILL: CDP CONFIDENTIALITY → PURE-GO PIPE TRANSPORT (2026-07-16, AUTHORITATIVE — newest; supersedes §6.0.2-pt4 and §6.0.1-pt3)
> Round-4 grill-spec verified **in code** that the R5 "keep port 9223 + Landlock process isolation" mechanism (§6.0.2-pt4) is **kernel-gated**: Landlock `NET_CONNECT_TCP` connect-filtering needs **ABI v4 → kernel 6.7+** for the net access rights (`pkg/sandbox/sandbox.go:44-45, 70`); on the common older-kernel install base `ConnectPortRules` "silently degrade (computed but not enforced)," so an agent `bash` process can dial 9223, drive CDP, and steal the ingest token → live-view hijack. The R5 SC-017 "100% isolation" claim was **false below kernel 6.7** (Jan 2024 — most self-hosted installs). **Operator decision (2026-07-16): close it kernel-independently with a pure-Go CDP-over-pipe transport.**
>
> **Resolution (reverses the R3/R5 "keep the fixed port"):**
> 1. **CDP over `--remote-debugging-pipe`** — Chrome speaks CDP over inherited fd 3/4 (NUL-delimited JSON); **no TCP port, no `/json`, no HTTP surface.** A co-tenant process cannot reach CDP (nothing to dial; it can't access the gateway's inherited fds). Kernel-independent — works on any supported platform, incl. Linux < 6.7 and the app-level-fallback sandbox.
> 2. **New work item — pure-Go CDP-over-pipe transport (chromedp lacks it).** chromedp v0.15.1 reads a `ws://` URL from Chrome's stdout and has no pipe transport, so Omnipus implements a small pure-Go transport that wires Chrome's fd 3/4 pair, speaks CDP (reusing `cdproto` message types over a pipe `Conn` in place of the websocket), and feeds chromedp's higher layers. Pure Go, single binary — Constraints #1/#2 hold; it is security-critical surface, scoped and tested as its own task. **Blast radius (MAJ-001/round-5):** `managedExecAllocatorOpts` is shared by EVERY managed launch, so this transport carries **all** browser CDP traffic (navigate, take-the-wheel, screenshots, live view) — not only live view. The swap is gated by a **browsing-equivalence regression** (every `browser.*` tool behaves identically over the pipe vs the old port) before it ships.
> 3. **ADR-043 shared-Chrome coordinator reworked (SIGNIFICANT same-wave dependency — CRIT-001/round-5).** With a pipe there is no `ws://` URL/port, so `sharedChromeCDPURL()` and the port-based `RemoteAllocator` sharing are removed. The fixed-port bind provided **two** things that must be re-provided, because a marker file alone is NOT atomic: **(a) atomic single-launch** — `net.Listen("tcp",":9223")` (`coordinator.go:757`) was the cross-process launch-race guard + grill-M2 foreign-Chrome spoof guard; replace it with an **`O_EXCL`/`flock` lockfile** (portable, atomic, no port), the marker file remaining the identity layer; the "foreign Chrome squatting our port" case largely evaporates (no port to squat). **(b) CDP sharing** — in-process managers must share the launcher's browser via **chromedp child contexts of the launcher `rootCtx`** (one pipe connection, multiplexed across targets) instead of each dialing `ws://9223` through a `RemoteAllocator`. **Constraint:** the pipe (fd 3/4) is private to the launching process, so any **cross-OS-process** CDP consumer (e.g. an `omnipus` CLI subcommand driving the browser while the gateway runs) MUST route through the gateway API — it cannot dial Chrome directly. This is a real rework of a working subsystem, scoped as its own build task; it is the true cost of the pipe choice.
> 4. **Landlock 9223 allow-list removed.** With no CDP TCP port, `sandbox_apply.go:419`'s `browser.DebugPort` connect allow-list entry and `checkDebugPortAvailable`'s port preflight are moot and removed — a net attack-surface reduction.
> 5. **EC-3 kernel-independent — and OPEN, not "passed" (CRIT-002/round-5).** SC-017 no longer claims Landlock-dependent "100%". EC-3 is a **pre-build gate that cannot be marked PASSED until the pipe transport is actually built**: it then proves the pipe exposes no TCP/HTTP surface and the token is unrecoverable by any co-tenant process on any supported kernel (Test 30). The earlier "EC-3 PASS" referred to the now-reversed keep-9223 mechanism and does **not** carry over — EC-3 is currently unproven for the pipe transport.
>
> This is the mechanism §6.0.2-pt4 set aside ("chromedp can't speak pipe → keep the port"); round-4 showed keeping the port is insecure on most installs, so the pipe transport is **built**, not avoided. §6.6/C-3, §6.0.1-pt3, and §6.0.2-pt4 are superseded by this block. **CRIT-002 (ingest token race):** the epoch discriminator is dropped — for a gateway-controlled **loopback** encoder page a transient ingest drop simply **re-mints the token and relaunches the encoder page** (cheap, local), so there is no same-token reconnect and no reconnect-vs-duplicate race (M-5's reconnect-survival concern does not apply to a loopback page the gateway owns).

> ### ⚙️ 6.0.4 — IMPLEMENTATION INCREMENT: "GATE-DORMANT" (2026-07-17, AUTHORITATIVE — newest; scopes what ships in THIS increment)
> The full A2 stack (transport, coordinator, capture, ingest, relay/GOP, SPA decode, contract, installer dual-download, Xvfb/PulseAudio sidecar packages) is **built and unit-tested**, but the two sidecars are **not yet wired into the gateway boot path** — `coordinator.SetDisplaySidecar` / `NewDisplaySidecar` / the PulseAudio equivalent have no boot caller — so `coordinator.videoLaunchMode()` can only ever return headless-shell. A 2-review-round gate (16 reviewer passes total) surfaced this as the sole structural blocker: the classifier + installer must **not** advertise "video-capable" (nor trigger the +120 MB full-Chrome one-way-door download) off a bare `Xvfb`-on-PATH probe while the launch is still headless.
> **Operator decision (2026-07-17, Daniel Piatkowski): Option "gate-dormant now, wire next."** This increment (a) **gates `ClassifyVideoCapability` and `selectDownloadBuild` on the display sidecar being wired-and-`Healthy()`**, not on an `Xvfb`-binary probe — so with no sidecar wired the feature is coherently **dormant** (classify → NotCapable, installer → headless-shell, viewer → JPEG `browser_screencast`, never a headless-grade masquerade or an ungated heavy download); (b) fixes all round-2 correctness/quality findings (recovery-path force-fresh-keyframe + decode-error→unavailable, silent-failure and comment/dedup cleanups). **Wiring the Xvfb + PulseAudio sidecars at boot, the real-headful-Chrome pipeline E2E (spec Tests 23–26), the SSIM equivalence corpus (Test 16), and executing the `[A2]` UAT rows on real headful video are the NEXT increment.** Until then, `browser_screencast` (JPEG) remains the **live default** live-view transport (its removal, §6.4/M-10, is likewise deferred to the sidecar-wiring increment — it must not precede the video path being reachable, per F-02). The A2 end-state in §6.1–6.6 is unchanged; only the wiring has not yet caught up to it.

**Decided: Option A — a WebCodecs relay over the existing gateway WebSocket.** Where video cannot run, the panel shows an explicit unavailable state (NOT a JPEG fallback tier — §6.4). Rejected alternatives, one line each:

- **Option B — WebRTC via Pion:** best media transport, but UDP/ICE/TURN is per-install networking homework that violates the deployability parity Omnipus depends on (NFR-2); kept only as the documented escalation path if TCP head-of-line blocking proves chronic. `[FACT — §4, §5]`
- **Option C — tuned MJPEG (status-quo-plus):** operator rejected as the target; its shared groundwork (binary WS frames, latest-wins queueing) is folded into A. `[FACT — user input]`
- **Option D — client-side rendering (DOM-mirroring / rewriting-proxy):** cannot be the agents' autonomous runtime (no human browser executes the page on a scheduled run), can't show the agent's real session, and the mature proxy engines are AGPL/unlicensed (incompatible with MIT). It is at best an *additional view* of the same server browser, not a replacement — see the comparison doc. `[FACT — three-pass research]`

Why A wins on §4: it takes the 30%-weight deployability criterion outright (WSS-only, works wherever the gateway works), the 20%-weight constraint-fit criterion by construction (all codec work is in the two Chromes, so the Go binary stays a pure byte relay — C-1), and delivers the 25%-weight responsiveness gain; it concedes only the lossy-WAN corner of responsiveness to B.

### 6.1 Component architecture

Five components; four are new or changed, and the drive/annotate paths are deliberately untouched.

| # | Component | Location | Role |
|---|---|---|---|
| 1 | **Encoder page** (was "capture page" — §6.0.2) | new embedded static HTML/JS, `go:embed` in the browser package; served on gateway **loopback only** | Controlled page inside the managed Chrome. Receives the agent tab's **CDP screencast JPEG frames** from the gateway over the loopback, `createImageBitmap`→`VideoFrame`→`VideoEncoder` (+ audio `getUserMedia` on the PulseAudio sink monitor → `AudioEncoder`, phase 2) → emits `EncodedVideoChunk`/`EncodedAudioChunk`s back over the loopback WS. Non-navigable, no remote fetch. **Does not use `getDisplayMedia`** (proved unreliable on bare Xvfb, §6.0.2). |
| 2 | **Chrome launch + CDP screencast capture** | `managedExecAllocatorOpts` (`pkg/tools/browser/exec_resolver.go`) + installer `cftDownloadID` (`installer.go:23`) | Full Chrome-for-Testing build run **HEADFUL on Xvfb** (§6.0) with **CDP over `--remote-debugging-pipe`** (EC-3, zero TCP surface). The gateway drives **`Page.startScreencast` on the agent tab** — no capture flags, no injected code in the agent's browsing context — and forwards each frame to the encoder page (1). `[FACT — launch seam grounded; §6.0.2]` |
| 3 | **Stream relay + GOP cache** | new, alongside `LiveViewRegistry`/`FrameSink` (`pkg/tools/browser/live.go`) | Terminates the capture page's internal WS; holds a **bounded GOP cache** (one keyframe + ≤ N deltas) per live stream; fans encoded chunks out to attached viewers; drives ABR by watching per-viewer queue depth and sending a bitrate control frame upstream. Reuses the existing drop-on-full, latest-wins queue discipline (`browser_ws.go:65`). |
| 4 | **Wire contract** | `contracts/asyncapi.yaml` (Constraint #8) | New messages, §6.3. |
| 5 | **SPA decode path** | `src/lib/browserLiveWs.ts` (frame consumer, `:147`) + `BrowserLiveView.tsx` (`imgRef`) | Declares `VideoDecoder.isConfigSupported` codecs at attach; decodes chunks via `VideoDecoder` onto a `<canvas>` that replaces the `<img>`. Viewport metadata unchanged, so take-the-wheel coordinate mapping is identical. |

### 6.2 Data flow

```
 AGENT TAB (headful Chrome on Xvfb)
        │  CDP Page.startScreencast (gateway-driven; no agent-facing API)
        ▼  JPEG frame
 GATEWAY  ──loopback WS (authed ingest)──►  ENCODER PAGE (same Chrome, controlled origin)
        ▲                                     │  createImageBitmap → VideoFrame → VideoEncoder
        │  EncodedVideoChunk (binary, loopback WS)  ◄──────────┘  (+ getUserMedia sink monitor → AudioEncoder, phase 2)
        ▼
 GATEWAY STREAM RELAY ──► GOP cache (keyframe + deltas)
        │  fan-out over /api/v1/browser/ws (existing authed WSS)
   ┌────┴───────────────┬─────────────────┐
   ▼                    ▼                 ▼
 VIEWER SPA         VIEWER SPA      (new viewer: replay GOP, then live)
 VideoDecoder → canvas

 DRIVE PATH (UNCHANGED): viewer input ──► CDP Input.dispatch on the agent tab
```

The return (video) leg is the only thing that changes. Capture is **server-driven CDP screencast** (§6.0.2) — the agent's browsing context has no capture code and no media grant. All encode happens in the encoder page (Chrome/WebCodecs); the Go binary only shuffles bytes (JPEG in, encoded chunks out — pure-Go, C-1). Input injection stays on the existing low-latency CDP path (ADR-040/041), which is why take-the-wheel keeps working with no contract change.

### 6.3 Wire contract (contract-first, per Constraint #8)

| Message | Direction | Encoding | Payload |
|---|---|---|---|
| `browser_attach` (amended) | SPA → gateway | JSON | add `video_caps`: the SPA's decodable codec list from `VideoDecoder.isConfigSupported` |
| `browser_stream_init` | gateway → SPA | JSON | negotiated `codec`, `width`/`height`, `keyframe_interval`, `has_audio` |
| `browser_video_chunk` | gateway → SPA | **binary** | compact **18-byte** big-endian envelope `{ seq:u32, ts:u64, key:u8, kind:u8, len:u32, payload }` — `kind` at offset 13 (0=video / 1=audio; one ingest connection multiplexes both, so `browser_video_chunk`/`browser_audio_chunk` share the envelope). The contract schema `BrowserChunkEnvelope.yaml` is authoritative (Constraint #8). Binary AsyncAPI message (R3: `u64`, not `u48` — m-3; the earlier 17-byte "no kind byte" text is superseded by the as-built + contract). |
| `browser_audio_chunk` (phase 2) | gateway → SPA | binary | Opus frames, same envelope shape |
| `browser_stream_bitrate` (v1.1) | relay → capture | JSON | target bitrate/framerate (ABR step) — **deferred to v1.1, not in the v1 contract** (R3/O-1) |

JSON control frames (`browser_status`, `browser_tabs`, `browser_control`, `error`) are unchanged. The dead JPEG `browser_screencast` message is **removed** from the contract in v0.3 (R3/M-10 — no back-compat obligation; no runtime path selected it).

**Feed/ingest leg (gateway ↔ encoder page, bidirectional — §6.0.2):** a distinct authenticated loopback endpoint (`/api/v1/browser/capture-ingest`) carries both directions: **downstream** the gateway pushes the agent tab's CDP **screencast JPEG frames** to the encoder page (`browser_frame_feed`, binary); **upstream** the encoder page returns `browser_ingest_init` + binary `browser_ingest_chunk` (encoded video/audio). It is authenticated by a **per-stream capability token, stream-lifecycle-scoped with a single active holder** (R3/M-5 — reconnectable, not single-use-per-connection) that the gateway mints and delivers to the encoder page out-of-band (CDP `Page.addScriptToEvaluateOnNewDocument` — **not** a URL query parameter, and not recoverable from the CDP transport, §6.6/C-3), scoped to one session and one stream lifecycle. All messages are contracted before code (Constraint #8). Detailed in the plan-spec.

### 6.4 Codec & degradation (amended 2026-07-16 by operator — no JPEG fallback)

At attach the gateway intersects the SPA's `video_caps` with the capture page's `VideoEncoder` support: **H.264 main (`avc1.4D40…`) preferred**, VP8 next (S-1b: baseline `avc1.42E01E` is NOT supported by the encoder — the earlier "baseline preferred" text was wrong; corrected in §6.0/§6.0.1). Gate-0 / EC-4 may invert this to VP8-first if the operator's iPad decodes VP8 but not H.264-main. Selection is per-attach, so a Safari viewer and a Chrome viewer on the same stream can be served the negotiated codec off the same single encoder (v1 = single encode per source; a disjoint-codec second viewer gets the unavailable state); no transcoding, ever (C-1).

**Degradation is an explicit unavailable state, NOT a JPEG tier and NOT an A1 tier** (A2-only, R3/M-1). Where no codec intersects, the managed browser is headless-shell, Xvfb/PulseAudio are absent, the platform is non-Linux, or capture bring-up fails, the panel shows **"Live view needs a video-capable browser"** (generic end-user string; specific cause operator-logged only — R3/O-3) and starts no stream. This still satisfies graceful degradation (Constraint #4): degraded clients get an honest state, never a blank/frozen panel and never a silent failure, without maintaining a second rendering pipeline. The `browser_screencast` message is **removed** from the contract in v0.3 (R3/M-10) — but note (R5/F-02) it is **not "dead"**: it is today's sole live-view transport, so its removal **withdraws working JPEG live view from non-video-capable installs** (they get the unavailable state, the accepted A2-only cost), and must not precede the video path being reachable on video-capable installs. The plan-spec (`docs/internal/specs/live-browser-video-streaming-spec.md`) carries the full behavior.

### 6.5 Installer & phasing

- **Installer:** point `cftDownloadID` at the full `chrome` build behind the same version pinning; **detect either binary at runtime** and keep headless-shell installs working (agents browse normally; live view shows the unavailable state on those installs). Flip the *default* download to full Chrome only after the G-5 footprint measurement; Termux-class platforms that can't afford it keep the unavailable state for live view (Constraint #4). Verify the downloaded build's integrity (hash/signature) before it becomes the agent runtime.
- **Phase 0 — spikes (§9), gate implementation.**
- **Phase 1 — video:** capture page + authenticated ingest leg + scoped-capture security, relay + GOP cache (with an aggregate memory ceiling), wire contract, SPA decode, per-attach codec negotiation + unavailable-state degradation, installer detection.

### 6.6 Security (STRIDE-driven; added post-review)

- **Ingest authentication (R3/M-5):** the capture→gateway ingest endpoint uses a per-stream capability token, **stream-lifecycle-scoped with a single active holder** (not single-use-per-connection — that broke reconnect): the capture page may reconnect with the same token while the stream is alive (superseding the prior holder); a concurrent duplicate is rejected; the token dies when the stream ends. An unauthenticated or mis-scoped ingest connection is rejected. Loopback is not a trust boundary on the pod (multiple agents, the preview listener, and user-served pages share it).
- **CDP-token confidentiality (R3/C-3; mechanism now = CDP-over-pipe per §6.0.3):** because loopback is not a trust boundary, the ingest token must not be recoverable from the CDP transport. **CDP runs over `--remote-debugging-pipe`** (inherited fd 3/4; no TCP port, no `/json`, no HTTP surface) via a new pure-Go CDP-over-pipe transport — so a co-tenant process cannot reach CDP at all, on **any** kernel. (The R5 "keep 9223 + Landlock connect-isolation" mechanism is **retracted** — round-4 verified it only enforces on kernel 6.7+ (Landlock ABI v4), and its premise that agent processes lack 9223 in their connect allow-list was itself code-false: 9223 IS added to `ConnectPortRules`/`BindPortRules` at `sandbox_apply.go:419`/`:388`, both now removed with the port.) The token is delivered via `Page.addScriptToEvaluateOnNewDocument`, never in `/json`. **EC-3 is an OPEN pre-build gate** (Test 30), proven once the pipe transport is built. Escalation if the pure-Go pipe transport proves infeasible: network-namespace the browser.
- **No agent-facing video capture (R3/C-2 — resolved structurally by §6.0.2):** video capture is **server-driven CDP `Page.startScreencast`**, which has **no page-callable API** — an agent-navigated page cannot start a screencast, and no `getDisplayMedia` video grant exists anywhere. The process-global-consent risk (the R2 BLOCK) is therefore **dissolved**, not merely mitigated (EC-2 PASS, structural). The process-global `--use-fake-ui-for-media-stream` remains **forbidden**. The one surviving media-consent surface is the encoder page's **audio** `getUserMedia` on the PulseAudio monitor: consent is granted via **CDP `Browser.grantPermissions({permissions:['audioCapture'], origin})` scoped to the encoder-page origin** only, so an agent-browsed page (different origin) **cannot** obtain an audio stream. Hard security-regression requirement with its own test — the "normal browsing unchanged" golden does not catch a posture change.
- **Capture target binding:** the capture selects its target by an unguessable per-stream key, not the human tab title.
- **Viewer authorization:** existing attach authorization gates the stream **before any GOP replay** — now backed by a dedicated test (R3/M-6).
- **Audit (R3/m-2):** stream lifecycle (start/stop) and every ingest-auth rejection write an audit entry on the new privileged ingest entry point.
- **Build integrity:** the full-Chrome download is hash/signature-verified before use.
- **Phase 2 — audio:** `AudioEncoder` → Opus over `browser_audio_chunk` (scope set by spike S-3).
- **Phase 3 — deferred:** WebRTC/Pion transport, only if Phase 1 telemetry shows chronic TCP head-of-line blocking.

```
CONFIDENCE (direction — Option A over B/C/D): High
  Basis         : Decided by the operator; the constraint set (C-1 kills Go-side
                  encoding; NFR-2 penalizes UDP transports) makes A the only option
                  that satisfies every hard gate, corroborated by market convergence
                  and the comparison research.
  Evidence      : User decision; codebase grounding; three-pass web research.
  Missing       : Nothing at the direction level.
  Would improve  : —
```

```
CONFIDENCE (design — capture/encode mechanism, RESOLVED §6.0.2): High
  Basis         : Capture mechanism settled empirically — CDP Page.startScreencast on
                  headful/Xvfb = measured 30 fps, WebCodecs VideoEncoder keeps up
                  (30.1 fps VP8, zero drops). getDisplayMedia is OUT (0-for-3 on bare
                  Xvfb). Relay/GOP/fan-out maps cleanly onto LiveViewRegistry/FrameSink.
  Evidence      : Gate 0 run on ci-omnipus (fps + encode measured x5); grounded launch
                  seam (exec_resolver.go managedExecAllocatorOpts) + SPA consumer.
  Missing       : EC-4 iPad H.264-main decode (deferred device probe) — affects codec
                  order, not the mechanism.
  Would improve : EC-4 on the operator's iPad.
```

```
CONFIDENCE (sub-decision — H.264-first codec policy): Medium
  Basis         : Safari VideoDecoder H.264 support expected; VP8 decode in Safari
                  doubtful — both [ASSUMPTION] pending probe.
  Evidence      : WebCodecs shipping status in Blink; Safari 16.4 notes (training
                  knowledge — flag: verify current).
  Missing       : S-2 capability probe on the operator's actual iPad.
  Would improve : S-2 (hours).
```

```
CONFIDENCE (sub-decision — unavailable-state degradation, no JPEG tier): High
  Basis         : Constraint #4 is non-negotiable; the tier already exists and
                  costs nothing to keep; codec negotiation selects it naturally.
  Evidence      : Existing screencast path is stable and tested.
  Missing       : None material.
  Would improve : —
```

## 7. Risks and Caveats

- **One-way door — installer default switch (sub-decision 2):** once fresh installs pull full Chrome, reverting strands installs on a larger binary. Mitigation: ship the capture feature detecting *either* binary; flip the default only after G-5 numbers are in.
- **G-1 failure mode:** if `getDisplayMedia` auto-accept doesn't work in CfT-headless, the extension + `chrome.tabCapture` route adds an embedded extension to maintain — more surface, same architecture. `[ASSUMPTION that at least one of the two capture routes works; both failing would force revisiting B or CDP-fed encoding, which has no pure-path — flagged as the single architecture-level risk]`
- **TCP head-of-line blocking** on genuinely lossy links: scroll can hiccup where WebRTC would degrade smoothly. Mitigation: aggressive keyframe-on-stall + bitrate step-down; documented escalation path to B.
- **Take-the-wheel latency asymmetry:** input injection stays on the low-latency CDP path while video returns via the encoder pipeline (adds encode+decode latency vs JPEG's single frame). Net perceived driving latency must be validated in the spike (S-1 measures glass-to-glass).
- **Security:** the capture page is a privileged internal page talking to the gateway loopback — its WS must use the existing credential model, and the page must be non-navigable content baked into the binary (no remote fetch). Audit-log the stream lifecycle like other browser events.

## 8. Confidence Assessment

Direction (A over B/C/D): **High** — settled by the operator and by the constraint set, which no spike outcome can flip (B's deployability penalty is structural, not empirical). Design (capture-page mechanism): **Medium-High**, with the single mechanical unknown being G-1 (auto-accept capture flags in CfT-headless); its failure changes the *mechanism* (extension + `chrome.tabCapture`) but not the architecture. H.264-first codec policy: **Medium** pending S-2 on the operator's iPad. Unavailable-state degradation (no JPEG tier): **High**. Two security items are hard requirements, not spike outcomes: the authenticated ingest leg and no-global-media-auto-accept (§6.6). Net: the decision is firm; the remaining risk is mechanical (retired by Phase 0) plus the named security requirements (carried into the plan-spec).

## 9. Validation / Next Steps

1. **Phase 0 — spikes (≈2 days; gate implementation, raise design confidence to High):**
   - S-1: CfT full Chrome `--headless=new` + auto-accept flags → `getDisplayMedia` a tab → `VideoEncoder` matrix + glass-to-glass latency vs today's JPEG path. (Resolves G-1, G-2; if G-1 fails, adopt the extension + `chrome.tabCapture` mechanism.)
   - S-2: `VideoDecoder.isConfigSupported` probe page on the operator's iPad (Safari). (Resolves G-3, sets codec policy.)
   - S-3: tab-audio capture attempt in the same harness. (Sets Phase 2 audio scope; resolves G-4.)
   - S-4: footprint delta full-Chrome vs headless-shell on the pod. (Resolves G-5; sets the default-download flip.)
2. **Red-team this decision record:** `/grill-spec docs/internal/architecture/ADR-044-live-browser-video-streaming.md`
3. **Spec the implementation:** `/plan-spec docs/internal/architecture/ADR-044-live-browser-video-streaming.md` — the capture page, relay + GOP cache, AsyncAPI messages (§6.3), installer detection, per-attach fallback negotiation (§6.4), and SPA decode path, with the FR/NFR targets from §2 as acceptance anchors. Sequence Phase 1 → 2 per §6.5.
4. File the v0.3 tracking epic referencing this ADR, the comparison document, and the `browser-live-responsiveness` note (interim tuning explicitly not pursued per operator direction).

---

## Amendment (2026-07-17): Single full-Chrome, encoder-as-tab — supersedes the two-Chrome split

**Status:** Accepted 2026-07-17 (operator: Daniel Piatkowski). **Authoritative over the "Option A / two-Chrome" shape described in §6.5, in the current code comments (`browser_stream.go`'s `encoderBrowser`, `exec_resolver.go`'s `EncoderChromeCmdline`, `capability.go`, `installer.go`), and in `ADR-044-spike-results.md` wherever they describe two Chrome processes.** The WebCodecs-relay-over-gateway-WS decision (§6.0–6.4, wire contract, GOP cache, ingest token, unavailable-state degradation) is UNCHANGED — only the *process topology that hosts the encoder* changes. Ratifying mode: this records an operator decision already made; it is not a re-litigation of §5/§6.

### Decision

Run **exactly ONE full-Chrome-headless process** (the ADR-043 `BrowserCoordinator`'s shared Chrome), not two. That single process hosts BOTH:

1. **Agent tabs**, each in its own per-agent CDP browser context (ADR-043 isolation, unchanged — it works on full Chrome), and
2. **The WebCodecs encoder tab**, created in the coordinator Chrome's **default** browser context (the one no agent uses), navigated to the gateway's unguessable `http://127.0.0.1:<random>/enc/<secret>` loopback page (127.0.0.1 = secure context, the precondition `VideoEncoder` requires).

The agent binary switches from `chrome-headless-shell` to the **full `chrome` Chrome-for-Testing build**. `chrome-headless-shell` is dropped as the agent binary; the dedicated encoder-Chrome process (`encoderBrowser` and `EncoderChromeCmdline`) is deleted. The encoder becomes a **tab in the coordinator's own Chrome**, reached through the coordinator's existing `RootContext()`.

### Why the two-Chrome split existed, and why it is gone

The split was forced by two beliefs that the operator's probes (below) falsify:
- *"chrome-headless-shell has no WebCodecs `VideoEncoder`, so the encoder needs a separate full-Chrome process."* — True about headless-shell, but the fix is to make the **agent** binary full Chrome, not to run a second process.
- *"A new browser context on full-Chrome `--headless` returns `-32000 'no browser is open'`, so agent-context isolation can't coexist with the encoder."* — This `-32000` is specific to `--headless=new`; **plain `--headless` (old mode) does not have it** (`[FACT-1]`).

### Grounding evidence (operator probes, 2026-07-17 — reproducible in `scratchpad/{one_chrome_probe.mjs,encoder_secure_probe.mjs}`)

- `[FACT-1]` full-chrome-151 **plain `--headless`**: `Target.createBrowserContext` → `Target.createTarget{browserContextId}` → `Page.navigate https://example.com` all succeed. The `-32000 "no browser is open"` that originally forced the split is a `--headless=new`-only defect; plain `--headless` supports per-agent browser contexts AND default-context targets in the same process.
- `[FACT-2]` full-chrome-151 plain `--headless`, page served from `http://127.0.0.1:<port>`: `isSecureContext:true`, `VideoEncoder:function`, `VideoEncoder.isConfigSupported({codec:'avc1.4D4028',…}).supported===true`, and a real encode produced `{type:'key', byteLength:783}`. (`about:blank` reports `VideoEncoder:undefined` because it is not a secure context — a red herring; the real encoder page is loopback-served.)
- `[FACT-3]` The `HeadlessChrome/151…` UA is the CAPTCHA trigger; `manager.go` already de-Headlesses the UA (`deHeadlessUA`) and installs `stealthInitScript`/`applyStealth`. The existing caveat comment notes the `navigator.webdriver` override **lands on full-Chrome `--headless` but NOT on chrome-headless-shell** — so switching the agent to full Chrome strengthens stealth on BOTH the UA and the `navigator.webdriver` axes.

### Rejected alternatives (one line each)

- **(a) Two-Chrome Option A (current code):** a dedicated encoder-Chrome process beside the agent's headless-shell — rejected by the operator ("two chromes can not be the solution"); `[FACT-1/2]` show one full-Chrome process serves both roles, so the second process is pure overhead (a second ~120 MB binary, a second profile, a second crash/idle lifecycle).
- **(b) Pure-Go H.264/VP8 encoder:** rejected — no mature pure-Go H.264/VP8 **encoder** exists, and a CGo binding to x264/libvpx violates Constraint #2 (pure Go, no CGo). Encoding must stay in the browser's WebCodecs.
- **(c) ffmpeg sidecar:** rejected — an external ffmpeg process violates the single-binary Constraint #1 and the "no shelling out for the media path" spirit of Constraint #2 / NFR-2 (zero per-install media configuration).
- **(d) iframe-embed the server-side page in the client:** rejected — the agent's server-side Chrome cannot be framed by the client browser, and cross-origin target sites set `X-Frame-Options`/`frame-ancestors` that block framing; this is Option D (§5), already rejected on requirements.

### What stays identical (explicitly not re-opened)

The gateway relay + GOP cache (`browser_stream.go`), the loopback capture-ingest endpoint + per-stream token (component E), `Page.startScreencast` capture on the agent tab (component L), the `browser_stream_init` / `browser_video_chunk` wire contract (§6.3, `contracts/asyncapi.yaml`), the H.264-first codec policy (`avc1.4D4028`), and the "video-capable browser required" unavailable-state degradation (no JPEG/A1 tier). The encoder-**target-creation** mechanic in `LaunchEncoderPage` is also unchanged (raw `target.CreateTarget` in the default context → `NewContext(rootCtx, WithTargetID(tid))`); only the `rootCtx` it receives changes from "the dedicated encoder browser's root" to "the coordinator's root".

### Security note (FR-016) — re-scoped, not weakened

Under two-Chrome, FR-016 (agent must not navigate to the encoder origin and inherit the audio grant) was "structurally impossible" because no agent tab existed in the encoder's process. Under single-Chrome that structural argument no longer holds, so FR-016 is re-secured by: **(i)** the unguessable loopback origin (OS-random port on 127.0.0.1) + random secret path — an agent cannot guess the URL; **(ii)** phase-1 is video-only (`ClassifyVideoCapability` → `VideoOnlyLevel`, `HasAudio` always false), so there is **no media-consent surface to inherit at all** today; **(iii)** when phase-2 audio ships, the `Browser.setPermission` grant MUST be scoped to the encoder tab's **default** browser context (`WithBrowserContextID` of that context), NOT a browser-level origin grant — the reverse of the current phase-2 note in `encoder_launch.go`, which assumed a dedicated process. Video capture itself (`Page.startScreencast`) has no page-callable API and no consent surface (C-2/P-6 `--use-fake-ui-for-media-stream` stays forbidden).

### Constraint & ADR compliance

Single pure-Go binary (#1/#2) — no new process, one fewer Chrome, no CGo, no ffmpeg. Footprint (#3/NFR-3) — one full Chrome instead of headless-shell + a second full Chrome (net **less** RAM and disk than Option A). Graceful degradation (#4/NFR-4) — non-linux/no-full-build hosts classify not-capable → unavailable state (unchanged); the installer's headless-shell fallback is retained as a safety net (agent still browses, video reports not-capable). Contract-first (#8) — **no `contracts/*.yaml` change**: the SPA decode path and all wire frames are byte-identical. ADR-043 per-agent isolation — preserved (`[FACT-1]`: per-agent browser contexts work on plain `--headless` full Chrome).

### Per-decision confidence

```
CONFIDENCE (single full-Chrome, encoder-as-tab over two-Chrome): High
  Basis         : Operator decision + three direct probes on full-chrome-151.
  Evidence      : [FACT-1] createBrowserContext/createTarget/navigate OK on plain
                  --headless; [FACT-2] real avc1.4D4028 encode from a 127.0.0.1
                  loopback page; [FACT-3] webdriver + UA stealth improve on full Chrome.
  Missing       : One integration UAT that the coordinator's OWN full-Chrome launch
                  hosts N per-agent contexts + a default-context encoder tab
                  concurrently (the probes ran a bespoke Chrome, not the coordinator).
  Would improve : That UAT (wave's final gate).
```

```
CONFIDENCE (agent binary = full Chrome by default; headless-shell retired): High
  Basis         : selectDownloadBuild() is a one-line switch to fullChromeBuild();
                  the dual-build installer + F-08 fallback already exist.
  Evidence      : installer.go EnsureChromiumBuild is already build-aware; full build
                  already downloaded/verified for the encoder under Option A.
  Missing       : Footprint delta re-measure of full-Chrome-as-agent vs prior
                  headless-shell-agent (G-5 was measured for the encoder, not the agent).
  Would improve : One RSS/disk measurement on the pod (non-blocking; net is still
                  less than Option A's two binaries).
```

```
CONFIDENCE (FR-016 re-scoping for phase-2 audio): Medium-High
  Basis         : Unguessable origin + secret path is the primary defense and is
                  unchanged; phase-1 has zero media grant.
  Evidence      : loopbackEncoderServer mints an OS-random port + 24-byte secret path
                  per stream (browser_stream.go).
  Missing       : Phase-2 audio is not built yet; the default-context-scoped grant is a
                  design note to honor when it is.
  Would improve : Implementing + testing the phase-2 grant against a hostile agent tab.
```

**Implementation blueprint:** `docs/internal/specs/single-chrome-video-blueprint.md` (dev-wave execution plan; disjoint file ownership, DoD per unit, review/fix/UAT waves).
