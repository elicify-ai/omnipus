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

## Gate status

Q2 = YES (done). Q1 = pending one external run. Both YES → amend/supersede ADR-044
(promote Option B / Pion to Accepted, encode the Q2 recipe + Q1 traversal findings)
→ `/plan-spec` → implement (wave pattern + 7-reviewer gate + UAT).
