# ADR-072 — Region-aware transport for the live browser

**Status:** Proposed
**Date:** 2026-08-30
**Affects:** the live browser panel (ADR-038, ADR-069). Does not change how the
agent drives the browser.

---

## 1. Context

Agent and user share one browser session. The user watches, and can take over,
through a live panel. That sharing is not in question and is retained.

Today the panel **transcodes the screen**: the gateway's Chrome renders the page,
an extension captures the tab, the frames are encoded to H.264, and the result is
relayed over WebRTC. Input returns over a data channel and is replayed via CDP.

### 1.1 The measured defect

Measured 2026-08-29/30, current release build, founder's Mac (Intel
i7-1068NG7, hardware VideoToolbox H.264):

| Capture size | Megapixels | fps | CPU idle at sample |
|---|---|---|---|
| 1098 × 556 | 0.61 | **29.9** | 12.5 % |
| 1798 × 856 | 1.54 | **15.0** | 43.9 % |
| 2558 × 1296 | 3.32 | **8.4** | 27–51 % |

Frame rate tracks pixel count inversely. Contention was eliminated as the cause
of the absolute number: the smallest frame reached the full 30 fps the source
offers **while the machine was at its most loaded** (12.5 % idle).

Nothing is lost downstream: `packetsLost`, `framesDropped`, `nackCount` and
`pliCount` were `0` in every run, on this and the previous binary.

### 1.2 The same defect is far worse on the other target

The live browser must work on macOS **and** on lean Linux with no GPU. That is
not hypothetical: `pkg/tools/browser/exec_resolver.go` disables the GPU on every
non-darwin platform (`runtime.GOOS != "darwin"` → `--disable-gpu`
`--enable-unsafe-swiftshader`), so software encode is the **default posture** for
Linux.

`pkg/tools/browser/captureext/embedded/encoder.js` already records that case
(measured 2026-08-14/15):

```
4x SHARED-cpu box, software H.264, scrolling at 1266x1372 ->  1 fps
  @ ~700kbps, 0% packet loss, machine only 9% busy
the same scroll at 632x684 (a quarter of the pixels)      -> 18 fps
macOS (VideoToolbox HARDWARE H.264, loopback)             -> 13 fps
```

**One frame per second at 9 % machine load.** So the two first-class targets have
structurally different bottlenecks — hardware-assisted on macOS, pure software on
Linux — differing by an order of magnitude. Any remedy must clear both.

### 1.3 Why the existing adaptation does not save us

The step-down loop in `encoder.js` is gated on the encoder's own
`qualityLimitationReason === 'cpu'` self-report. Measured behaviour: it correctly
reports `cpu` at 1798 × 856, and reports `none` at 2558 × 1296 — the largest
frame, where downscaling would help most. `scaleResolutionDownBy` stayed `1` in
every sample at every size.

That is not an unknown bug. The loop's own doc comment states the assumption:
"on a hardware encoder over loopback — which is never CPU-limited — it never
steps at all… A macOS-shaped sample (reason 'none' at 13 fps) is deliberately
NEUTRAL."

### 1.4 The structural observation

When a page plays video, the bytes arriving at the gateway's Chrome are
**already compressed**. The current design decodes them, composites them,
re-encodes the whole screen, and ships that. It is a transcode, and on the Linux
target the re-encode collapses to 1 fps.

---

## 2. Decision

Transport is chosen **per region, by what the region is**:

| Region | Treatment | Encode cost |
|---|---|---|
| Media (`<video>`, MSE) | Forward the source's own compressed segments | **None** |
| Static UI, text, layout | Dirty-rectangle tiles | Near-zero — few pixels change |
| Canvas / WebGL | Encode that rectangle only | Real, but bounded and small |

Audio arrives inside the media stream. Input is unchanged.

### 2.1 This is deliberately a ladder, not a single mechanism

An earlier draft of this ADR claimed one mechanism with no fallback. Two
independent reviews rejected that claim as false, and they were right: canvas and
WebGL cannot be replicated at the DOM or media layer, and a design that cannot
render a CAPTCHA breaks ADR-038's stated purpose for this panel — human takeover
for "login, CAPTCHA, purchase approval".

Region-awareness **is** the fallback ladder, made explicit and chosen by content
type rather than discovered at failure time. Each path is used where it is
strongest:

- video is tiling's worst case (the region is dirty every frame) and segment
  forwarding's best case (the bytes already exist);
- static UI is tiling's best case and needs no codec at all;
- canvas has no source stream, so it must be encoded — but only its own
  rectangle.

---

## 3. What the spikes proved

Two throwaway spikes were built to test the load-bearing claims before
committing to this design. The code and evidence are committed alongside this
ADR; §9 states exactly which arms are automated and which are not.

### 3.1 Media replication works, on YouTube, with audio, in sync

Intercepting `SourceBuffer.appendBuffer` in a source Chrome and replaying the
exact bytes into a second browser:

| | DASH vector (dash.js) | **YouTube** |
|---|---|---|
| Appends captured | 53, 0 missing | **450, 0 missing** |
| Viewer accepted | all | **all 450** |
| Playback rate (media ÷ wall, 8 s) | 1.000 | **1.000** |
| Frames decoded | 243 in 8.05 s | 243 in 8.05 s |
| Audio | present | present (RMS 0.0585) |
| Codecs | H.264 + AAC | **AV1 + Opus** |

Mid-stream ABR switching occurred in both runs and replayed correctly.

### 3.2 Safari is solvable, and the method is specific

YouTube served AV1, which WebKit rejects outright (`NotSupportedError`, 0
appends). Denying the source Chrome AV1 makes YouTube serve VP9, which real
Safari plays.

Measured on real Safari 26.5.2 (not Playwright's WebKit), Intel Mac, no
hardware AV1:

| | AV1 (control) | VP9 (gated) |
|---|---|---|
| `isTypeSupported` | false | **true** |
| Appends accepted | **0 / 450** | **820 / 820** |
| Video size | 0 × 0 | **640 × 360** |
| Playback rate | 0 | **0.971** |
| Frames decoded | 0 | **258**, 0 dropped |

**A Chrome launch flag does not exist for this.** `--disable-features=Av1Decoder`
and variants had no effect; grepping the Chrome 151 framework shows the only AV1
feature names are cast-streaming ones. The working method is patching three
capability APIs in the injected shim — `MediaSource.isTypeSupported`,
`HTMLMediaElement.canPlayType`, and `navigator.mediaCapabilities.decodingInfo`.
**YouTube queries all three**; missing one leaves AV1 in place.

### 3.3 Worker-hosted MSE is capturable, but fails silently if unhandled

Adversarial test material (MediaSource inside a dedicated Worker, handle
transferred via `srcObject`) played perfectly while capture recorded **0 appends,
0 bytes, no error**. That is the dangerous shape: it looks like an idle stream,
not a fault.

It is capturable — `Debugger.enable` plus
`setInstrumentationBreakpoint('beforeScriptExecution')`, then injecting via
`Debugger.evaluateOnCallFrame` inside the resulting pause, produced a capture
a capture the spike reported as byte-identical to the main-thread case.
**Caveat: no hash comparison is committed** — nothing in the spike computes a
digest, so "byte-identical" rests on the spike agent's report, not on a
reproducible check. Treat it as unverified until a comparison is added.
`Runtime.evaluate`
on a worker halted by `waitForDebuggerOnStart` deadlocks.

YouTube did not use worker MSE in these runs. If it adopts it, a naive shim goes
quietly to zero.

### 3.4 Bandwidth is the source's bitrate — not inherently lower

Measured as bytes ÷ media-seconds:

- YouTube 360→480p, AV1: **0.42 Mbps** — 8.6× cheaper than the re-encode
- Same path, VP9 (Safari-compatible): **0.49 Mbps** (+17 %)
- DASH uncapped, ABR ran to 4K: **13.15 Mbps** — 3.7× **more expensive**

**The design is not inherently cheap.** It costs whatever the source player
chose to fetch, and on a fat link ABR will choose 4K. Capping the source
player's ABR is the real lever, and it is ours to set.

---

## 4. Design

### 4.1 Region classification

Regions are derived from the live DOM via CDP: media elements and their
bounding boxes, canvas/WebGL elements and theirs, everything else is UI. The
classification must be re-evaluated on layout change, and a region that cannot be
classified is treated as UI (encode it) rather than assumed replicable — the
failure must be visible, not silent.

### 4.2 Media path

A shim is installed with `Page.addScriptToEvaluateOnNewDocument`, which runs
before any page script. It hooks `MediaSource`, `SourceBuffer.appendBuffer` and
`remove`, records the MIME type from `addSourceBuffer`, and forwards appended
buffers in order.

**Byte-exact ordering is mandatory.** YouTube does not append whole segments: 96 %
of video appends and 94 % of audio appends begin mid-segment, at no container
boundary. One dropped or reordered append corrupts the stream. Any design
assuming "append = segment" works against dash.js and breaks against YouTube.

**Late join.** A viewer attaching mid-playback needs the init segment plus enough
media to reach the playhead. Init segments are identifiable by container box
(`ftyp`/`moov`, or EBML header) and are small (633–707 bytes measured). Each ABR
switch emits a fresh init. Cuts must land on a parsed container boundary, not an
arbitrary append.

**Codec gate.** The shim reports a capability set that is the intersection of what
all attached viewers support (§3.2). This is set at capture start.

### 4.3 UI path

Dirty-rectangle tiles. Cost scales with changed area, so panel size no longer
drives cost for static content — which is the specific coupling that produced the
measured defect.

### 4.4 Canvas path

Encoded as an image sequence, scoped to the element's rectangle. A reCAPTCHA
widget is roughly 300 × 500 ≈ 0.15 Mpx, comfortable even against the ~2 Mpx/s
software floor.

**Cost asymmetry, noted:** on GPU-less Linux, Chrome renders canvas via
SwiftShader anyway, so quality there is already limited. On macOS with a real
GPU, canvas renders crisply today — so this path costs the most on the platform
best equipped to avoid it.

### 4.5 Audio

No separate audio transport. Audio is inside the media stream and arrives with
it. The Opus track and `tabCapture` audio are removed.

**Gap:** page-generated audio that is not from a media element (WebAudio
synthesis, notification sounds) has no path in this design and is not covered by
the spikes. See R6.

### 4.6 Input

Unchanged: replayed into the real page via `Input.dispatchMouseEvent` /
`dispatchKeyEvent`.

**But the correctness property changes and this must be handled.** Today the user
clicks on an image of the real framebuffer, so coordinates map exactly by
construction. Where a region is replicated rather than pixel-copied, the user
clicks on the *viewer's own rendering*, and layout can diverge — different engine,
font metrics, scrollbar widths. Coordinate replay could then land on the wrong
element. Node-based hit-testing is required for replicated regions; coordinate
replay remains correct for encoded ones.

### 4.7 Security of replicated content

Where DOM is replicated, foreign markup is rendered in a **sandboxed iframe with
no `allow-same-origin`**, and script execution is restricted to a
**nonce-pinned first-party renderer bundle**. Page script never executes in the
replica; the real page executes remotely.

The earlier draft stated "no script execution" as an absolute, which was
self-contradictory — the media and EME paths require the replica to call
`appendBuffer` and drive a CDM, which are script. The invariant is *foreign*
script never executes; a trusted, pinned renderer does.

---

## 5. Consequences

**Gains.** The encoder ceiling disappears for media, which is the workload that
collapses it. Cost scales with change rather than window size. Media plays with
hardware decode at native framerate. Text in replicated regions renders as text.
On lean Linux the expensive path is removed entirely.

**Costs.** Three transports to build and maintain instead of one. A subresource
proxy becomes load-bearing for replicated regions and is a new SSRF-shaped egress
surface that must be bound by the same policy as the sandbox (#155). Region
classification is a new failure mode. Cross-origin iframes (OOPIFs) are separate
CDP targets needing `Target.setAutoAttach` and per-target injection.

**Loss of universality.** Today everything is transcoded to H.264, which every
viewer decodes. Under this design the viewer must decode what the source fetched,
which is why §3.2's codec gate exists.

---

## 6. Risks

| # | Risk | Severity | Status |
|---|---|---|---|
| R1 | Worker-hosted MSE silently captures nothing if unhandled | High | **Solved in spike; must be built deliberately, with an evasion detector that logs "blind"** |
| R2 | DRM (Widevine) content | High | **Will not work.** Encrypted bytes are useless without key exchange. Already fails today (protected surfaces capture black), so not a regression |
| R3 | Byte-order loss corrupts the media stream | High | Transport must be ordered and lossless for the media path |
| R4 | Input lands on wrong element in replicated regions | High | Node-based hit-testing required (§4.6) |
| R5 | Source ABR chooses a high bitrate on a fat link | Medium | Cap the source player's ABR |
| R6 | WebAudio-generated audio has no path | Medium | **Open** — not covered, not tested |
| R7 | Subresource proxy egress surface | Medium | Must inherit sandbox egress policy |
| R8 | Region misclassification renders nothing | Medium | Default to encoding when unclassifiable |
| R9 | Viewer-side bitrate adaptation is lost for media | **High** | ADR-069's 27.6 %-loss link was **RESOLVED 2026-08-19** by an AIMD loop (`bitrate.go`). Citing that number as live was wrong. The real point stands and is worse: forwarded byte-exact segments **cannot** adapt down, so this design *removes* a mitigation the shipped system has, with no replacement proposed |
| R10 | Canvas cost is highest on macOS (§4.4) | Low | Accepted |

---

## 7. Cheaper work that should happen first, regardless

This ADR is not a prerequisite for relieving the measured defect. Three changes
are far smaller and should be done and measured before any of this is built:

1. **Cap capture resolution independently of panel size** (and/or capture at
   DPR 1). The repo's own data shows quartering the pixels took the shared-vCPU
   box from 1 fps to 18.
2. **Fix the adaptation gate** so it can fire when the encoder reports `none` but
   framerate is plainly starved (§1.3).
3. **Force VP8 on no-GPU hosts.** VP8 software encode is materially cheaper per
   pixel than VP9 or software H.264.

If those three restore acceptable framerate on both targets, the scope of this
ADR should be reconsidered — possibly narrowed to the media path alone.

---

## 8. What this ADR does not decide

- The transport (WebSocket vs WebRTC data channel).
- Whether the existing pixel pipeline is deleted or retained beneath the region
  logic. **Retention is the safer default**, since it is the universal fallback.
- Whether replicated UI uses DOM replication or tiles — §4.3 assumes tiles, which
  is the lower-risk option; DOM replication remains a possible later refinement.
- Whether DRM is ever supported. R2 says no.

---

## 9. Reproducing the evidence

Spike code and evidence: `docs/internal/spikes/browser-streaming/` (committed
alongside this ADR). See its `README.md` for what is and is not automated.

**`run-all.sh` does not reproduce everything, and this ADR previously claimed it
did.** It drives the Chrome-side capture and the Chrome/Firefox replay. It needs
one external file fetched first (`dash.all.min.js`, see the README), and the
real-Safari arm of §3.2 is **manual by design** — `safaridriver` needs "Allow
Remote Automation" enabled, which the spike deliberately did not do. That arm was
run by opening the harness page in Safari by hand; its self-reported payloads are
in `evidence/safari-reports/`. `crossengine.js` covers Playwright's WebKit, which
is not real Safari.

Captured media chunks (~170 MB) are excluded; the committed manifests carry the
per-append offsets, sizes, codecs, container classification and load averages the
§3 numbers are computed from. `shim.js` is the
scope-agnostic MSE interceptor; `record.js` the CDP recorder including worker
injection; `codecgate.js` the AV1 denial; `pages/viewer.html` the replay and
measurement harness.

Untested, and named so nobody assumes otherwise: live/DVR streams, true ABR
downshifts, long-run drift beyond 8-second windows, Apple Silicon Safari (M3+
supports AV1, so §3.2's constraint may not apply there), DRM, and WebAudio.
