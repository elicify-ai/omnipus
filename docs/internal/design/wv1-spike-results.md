# WV1 Spike Results — WebRTC deployability + capture (browser-video-2)

**Date:** 2026-07-17. **Branch:** `feature/browser-video-2`. Spike code (throwaway,
uncommitted binaries): `spikes/wv1-webrtc/`. Background & decision context:
`live-browser-webrtc-context.md` (same directory).

Two make-or-break questions were tested before amending ADR-044 / building anything.

---

## Q2 — headless tab MediaStream WITH audio: **YES (proven, hard numbers)**

**Verdict: a headless pod produces a tab MediaStream with live ~30fps video AND real
audio samples. No PulseAudio, no sidecar, no audio device, no special audio flags.**

Environment: Chrome for Testing **151.0.7922.34**, `--headless=new`, Fly pod with zero
audio devices (Chrome logs `ALSA PcmOpen: … No such file or directory` and falls back
to its internal fake audio backend automatically — capture still gets real samples).

| Test | Result |
|---|---|
| T1 audio baseline | Headless renders audio with NO flags: oscillator RMS 0.1770 vs 0.1768 theoretical; identical under `--disable-audio-output` / `--mute-audio` |
| T2 `getDisplayMedia` | Works cross-tab with `--auto-select-tab-capture-source-by-title=<title>`: 29.7fps video + "Tab audio" track, 32/32 nonzero RMS probes |
| T3 `tabCapture` extension | Works: 29.3fps, flat RMS 0.295 (matches theoretical source mix), stereo 44.1kHz; selection by **tab ID** |
| T4 navigation survival | The SAME stream survives `Page.navigate` of the captured tab — post-nav RMS 0.35359 vs 0.35355 theoretical. No re-capture on nav |

**Recommended path: tabCapture extension** — tab-ID selection (immune to title
changes), AGC/EC/NS default OFF (getDisplayMedia's default constraints decay a
constant tone 0.574→0.038; must explicitly disable), survives navigation.

**Exact recipe (extension path):**

1. Launch flags: `--headless=new --remote-debugging-pipe
   --enable-unsafe-extension-debugging --allowlisted-extension-id=<extId>
   --autoplay-policy=no-user-gesture-required` (+ usual base flags).
   - `--load-extension` is **dead since Chrome 137** (silently ignored in 151). Load
     via CDP **`Extensions.loadUnpacked {path}`**, which only works over the **pipe**
     transport (fd3/fd4, NUL-delimited JSON) + the unsafe-extension-debugging flag.
     The Omnipus launcher must gain pipe-mode for this call (spike's `cdp.js::PipeCDP`
     shows the ~50-line pattern).
   - Extension id is deterministic per unpacked path
     (`gdogapnbcanodbmgbhchhhdnohhlgljf` for the spike dir) → load once, learn id,
     bake into flags.
   - `--allowlisted-extension-id` is **mandatory** — without it `getMediaStreamId`
     fails with the activeTab/user-invocation error; with it, no gesture needed.
     (Chromium's own tabCapture tests use it; flag-removal risk → note in ADR.)
2. Extension: MV3, permissions `["tabCapture","tabs","scripting"]` + an extension
   *page* driven via CDP eval (MV3 service-worker eval was flaky — avoid).
   `chrome.scripting.executeScript` into a consumer page needs `world:'MAIN'`.
3. Consume: `chrome.tabCapture.getMediaStreamId({targetTabId: agentTab,
   consumerTabId: encoderTab})` → in the encoder page
   `getUserMedia({audio:{mandatory:{chromeMediaSource:'tab', chromeMediaSourceId:id}}, video:{…}})`
   → hand the MediaStream to the RTCPeerConnection. One active capture per tab.

**Fallback (no extension):** `--auto-select-tab-capture-source-by-title=<title>` +
`getDisplayMedia({video:true, audio:{echoCancellation:false, autoGainControl:false,
noiseSuppression:false}})` under a `userGesture:true` eval. Title-matching is fragile
for a navigating agent tab. **Never combine with `--use-fake-ui-for-media-stream`**
(breaks tab capture with `NotReadableError`).

Traps catalogued: built-in component extension masquerades as a loaded extension in
`Target.getTargets` (false-positive when checking whether `--load-extension` worked);
`Extensions.loadUnpacked` refuses port-mode debugging; isolated-vs-MAIN script worlds.

---

## Q1 — Pion ↔ SPA connectivity over Fly-proxied network: **in-pod PASS; external test PENDING (operator)**

Standalone Pion v4 test server (`spikes/wv1-webrtc/q1-connectivity/`, own go.mod —
Pion is NOT in the product go.mod yet). HTTP+signaling on 0.0.0.0:8080, passive
ICE-TCP mux on 8081, three modes (STUN+UDP / host-only / ICE-TCP), data-channel echo
with live candidate/state/RTT reporting and a copy-pasteable report.

**Proven in-pod:**
- **Pod outbound UDP + STUN works** — server gathered srflx candidates against
  `stun.l.google.com:19302` on both IPv4 (`138.199.24.240`) and IPv6. This is the
  precondition for TURN-free hole-punching.
- Full wiring: connect ~150ms, 96s echo at ~1ms RTT, no drops; ICE-TCP mux offers
  valid `tcp` host candidates.
- **Pion gotcha for the real build:** `dc.Send()` emits *binary*-typed frames; a
  browser `dc.send(string)` is *text*-typed. Echoing binary broke the browser-side
  handler silently. Use `SendText()`/check `msg.IsString`. The input data channel
  must get this right.

**Not yet proven (needs the operator's browser, from a real external network):**
whether inbound UDP hole-punching to the pod's srflx mapping succeeds — Fly forwards
only TCP:8080, so success depends on Fly's egress NAT mapping being stable and
reachable. Test: open the preview URL, DEFAULT mode, Connect, wait ~90s, paste the
report. Read-out:
- Selected pair `srflx/prflx` over `udp`, state `connected` → **TURN-free works** (best case).
- Stuck at `checking` → failed → inbound UDP blocked; ICE-TCP mode externally will
  also fail (8081 not forwarded) → design needs an extra reachable port or an
  embedded (pure-Go, in-binary) TURN/relay fallback — decide in the ADR amendment.

Restart the test server: `cd spikes/wv1-webrtc/q1-connectivity && CGO_ENABLED=0 go
build -o wv1spike . && nohup ./wv1spike > server.out 2>&1 & disown`

---

## Q3 — end-to-end video + audio TOGETHER through the target pipeline: **YES (proven in-pod)**

**Verdict: the full target architecture works as one flow** — headless Chrome tab
capture (video+audio MediaStream via the Q2 extension recipe) → WebRTC → **Pion
relay in Go (raw RTP forwarding, no transcoding)** → viewer browser rendering both
tracks. Spike code: `spikes/wv1-webrtc/q3-e2e/` (server superset keeps the Q1 test
page at `/q1`; viewer at `/view`; captured content is a `/metronome` page whose
screen flash coincides with a 1Hz beep, so A/V sync is human-verifiable).

Measured (39s sustained run, scripted viewer):
- Codecs negotiated **VP8 + Opus**; RTP counters climbing on ingest AND viewer,
  `packetsLost=0`; **framesDecoded 1172 @ 30.0fps steady**, 800×600.
- **Audio RMS on the received track pulsed at exactly 1.00s intervals** (12
  consecutive peaks, max 0.637) — real tab audio, matching the metronome, in sync
  with the tick counter visible in decoded frames.
- **Encoder kill/restart mid-run recovered cleanly** — new ingest PC with fresh
  SSRCs replaced the old one; viewers resumed.

Build-phase learnings (carry into the ADR/implementation):
1. Pion needs explicit `MediaEngine.RegisterDefaultCodecs()` +
   `RegisterDefaultInterceptors()` for media — the Q1 data-channel-only defaults
   won't negotiate Chrome's VP8/Opus offer.
2. SFU pattern: one shared `TrackLocalStaticRTP` per kind, `AddTrack`ed to every
   viewer PC — N viewers, zero transcoding, worked first try.
3. **PLI goes to the encoder's PC** (`ingestPC.WriteRTCP` with the remote track's
   `MediaSSRC`) on viewer join + a short periodic burst, or late viewers never get
   a keyframe.
4. RTP/RTCP drain goroutines on both `RTPReceiver` and `RTPSender` are load-bearing
   (buffer stalls otherwise).
5. Self-consuming `tabCapture` inside the extension page (no separate consumer tab)
   is simpler than Q2's cross-tab pattern and reliable across launches.

---

## Q4 — bidirectional: responsive input WHILE video+audio stream: **YES (proven in-pod)**

**Verdict: the regression that killed the WebCodecs path cannot occur in this
architecture — proven by measurement, not tuning.** Input rides its own `"input"`
data channel on the same PeerConnection; CDP carries ONLY input (video/audio never
touch it). Spike: `spikes/wv1-webrtc/q4-bidir/` (q3 superset; interactive metronome
page — click-dots, mousemove trail, text echo, wheel — with the 1Hz beep running).

Measured:
- **Input dispatch RTT under 30s stress (60 mousemove/s + 2 clicks/s + 4 keys/s,
  ~2200 events): p50 = 10.9ms, p95 = 21.0ms, max = 88.4ms, 0 errors** — vs the old
  design's 5000ms+ input timeouts. DC ping baseline ~2.1ms → ~8-9ms is the real CDP
  round trip, not transport.
- **Media unaffected during stress:** 29.6fps, `packetsLost=0`, audio pulses still
  ~1.00s apart. Correctness: 22/22 synthetic events arrived in order; clicks within
  1px; "hello" typed exactly; wheel accumulated exactly; all visible in the decoded
  video.

Build learnings (for the ADR/implementation):
1. **CDP printable-key dispatch requires `text`/`unmodifiedText`** on keyDown or no
   character is typed (no input/beforeinput events fire).
2. DC framing gotcha re-confirmed: browser `dc.send(string)` is text-framed — Go
   must reply `SendText`, or the frame is silently dropped by `onmessage`.
3. Letterbox-aware viewer→tab coordinate mapping
   (`min(rect.w/vw, rect.h/vh)` + centering offset) — clicks landed within 1px.
4. Spike-only scaffolding: Go reaches Chrome CDP via a localhost WS bridge held by
   the Node launcher (extension loading forces pipe-mode). Real build: Go owns the
   pipe (port `Extensions.loadUnpacked`-over-pipe into the Omnipus launcher) →
   dispatch RTT should approach the ~2ms ping baseline.
5. **Open design item:** single-driver arbitration across concurrent viewers (the
   product's existing "one driver holds the wheel" doctrine) is NOT exercised by the
   spike — all viewers' input funnels to one shared CDP session.

---

## Gate status

Q2 = YES. Q3 e2e media = YES. Q4 bidirectional input = YES (all in-pod). Q1 =
pending one external run (operator: open `/view`, drive the tab — a connected
`srflx/udp` pair there proves traversal with real media AND input, superseding the
`/q1` data-channel-only test). All YES → amend/supersede ADR-044
(promote Option B / Pion to Accepted, encode the Q2 recipe + Q1 traversal findings)
→ `/plan-spec` → implement (wave pattern + 7-reviewer gate + UAT).

(Q1 external run resolved 2026-07-18 via operator UAT — see ADR-047 §5 OI-1 resolution.)
