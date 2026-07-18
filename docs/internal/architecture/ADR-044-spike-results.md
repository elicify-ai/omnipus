# ADR-044 — Phase 0 Spike Results (S-1, S-4 partial; capture finding)

- **Date:** 2026-07-16
- **Environment:** Omnipus dev pod (Fly, Ubuntu 24.04, x86_64, headless container, no display/dbus/audio by default).
- **Binaries tested:** Chrome-for-Testing 150.0.7871.24 — `chrome-headless-shell` (what Omnipus ships today) and full `chrome`.
- **Method:** each binary launched directly (`--headless=new`), a local page probes the WebCodecs pipeline and POSTs results back. Harness in the session scratchpad (`spikes/`).

## Headline

**The WebCodecs video ENCODER works in the binary we already ship — full Chrome is NOT required for encoding. But `getDisplayMedia` (the ADR's primary CAPTURE mechanism) fails headless in BOTH binaries. Capture, not encoding or the binary, is the real open problem.** This empirically kills ADR-044's assumed capture path (getDisplayMedia + auto-accept flags) and independently corroborates the R1 grill-spec finding M-4.

## Results

### S-1a — WebCodecs `VideoEncoder` feasibility: ✅ works in BOTH, incl. headless-shell
- `VideoEncoder`, `MediaStreamTrackProcessor` present; secure context OK.
- A canvas→`VideoEncoder` pipeline produced real encoded chunks (keyframe emitted, no encoder error) in **both** `chrome-headless-shell` and full `chrome`.
- **Implication:** encoding does not justify switching to full Chrome. `chrome-headless-shell` already has the encoder.

### S-1b — Encoder codec matrix (identical in both binaries)
| Codec | `VideoEncoder.isConfigSupported` |
|---|---|
| H.264 **baseline** `avc1.42E01E` | **NOT supported** |
| H.264 **main** `avc1.4D401F` | supported |
| VP8 | supported |
| VP9 | supported |
| AV1 | supported |
- **Implication:** the ADR/spec "H.264 baseline (`avc1.42E01E`) preferred" policy is **wrong for this build** — must be **H.264 main** (`avc1.4D40..`) or VP8. Update ADR §6.4 / spec FR-006 codec policy. (Decoder-side support on the operator's iPad is still S-2, unrun.)

### S-1c — `getDisplayMedia` capture: ❌ fails headless in BOTH binaries
- Result: `NotReadableError: Could not start video source` — headless-shell AND full `chrome`, with `--use-fake-ui-for-media-stream` + `--auto-accept-this-tab-capture`.
- Under **Xvfb** (virtual display): headless-shell still fails (it is headless-only, no render surface — expected); full `chrome` headful under Xvfb did not start cleanly in this container (dbus/headful fragility), inconclusive but itself a deployability red flag.
- **Implication:** the ADR's primary capture mechanism (getDisplayMedia + flags) does not work in Omnipus's headless/container runtime. Full Chrome does not fix it. This is the pivotal finding.

### S-3 (partial) — audio: encoder ready, but no source and no device
| Check (both binaries) | Result |
|---|---|
| `AudioEncoder`/`AudioDecoder` present | yes |
| Opus encode + decode supported | **yes** |
| AAC (`mp4a.40.2`) encode | no |
| `enumerateDevices()` audio outputs | **[] — none** |
| `getDisplayMedia({audio:true})` | `NotReadableError` (same as video) |
- **Implication (initial):** Opus encoding is ready in headless-shell too — audio does not justify full Chrome for encoding.

### S-3 (RUN) — audio capture via a PulseAudio null-sink: ✅ WORKS in headless-shell, and DECOUPLED from video capture
Installed PulseAudio (~27 MB apt), started it headless (no dbus/systemd), created a null-sink + remapped its monitor to a real source, pointed Chrome at it. Result — **non-silent audio captured and Opus-encoded in `chrome-headless-shell`:**
| Check | Result |
|---|---|
| Chrome sees an audio input device | **yes** (`VirtualMic`) after `module-remap-source` (Chrome hides raw `.monitor` sources — the remap is required) |
| `getUserMedia({audio:true})` captures the sink | **yes** — external 440 Hz sine → captured `RMS 0.017, peak 0.235` (real, non-silent) |
| Opus-encode the captured audio | **yes** — 51 `EncodedAudioChunk`s, 8.3 KB / ~1 s |
| Uses `getDisplayMedia`? | **NO** — capture is via `getUserMedia` on the sink monitor, so audio is **INDEPENDENT of the broken video-capture path** |

**Working recipe (headless container, no dbus):**
```
export XDG_RUNTIME_DIR=/tmp/pulse-rt && mkdir -p $XDG_RUNTIME_DIR
pulseaudio -D --exit-idle-time=-1 --disable-shm=1
pactl load-module module-null-sink   sink_name=vspk
pactl load-module module-remap-source master=vspk.monitor source_name=vmic   # Chrome won't use a raw .monitor; remap needed
pactl set-default-sink vspk ; pactl set-default-source vmic
# launch Chrome with env: XDG_RUNTIME_DIR=/tmp/pulse-rt  PULSE_SERVER=unix:/tmp/pulse-rt/pulse/native
# in-page: getUserMedia({audio:true}) -> MediaStreamTrackProcessor -> AudioEncoder({codec:'opus'})
```
- **Implication:** audio is achievable in the binary we already ship, and — importantly — it does **not** depend on solving the video-capture problem (it rides `getUserMedia` on the sink monitor, not `getDisplayMedia`). Codec: Opus only (AAC encode unsupported). Two caveats: (a) the tab's audio must actually route to the null sink (default output — expected, but confirm with real `<video>` audio, not just WebAudio); (b) this is per-deployment audio infra that Go must *orchestrate*, not *be*.

### "Audio sink in Go" — orchestration answer (web research, cited)
- **Cannot be done *in* Go:** no pure-Go PulseAudio *server* exists (native protocol undocumented, maintainers advise against reimplementing); all Go PA libs (`jfreymuth/pulse`, `noisetorch/pulseaudio`, `lawl/pulseaudio`) are *clients*. `snd-aloop` (kernel loopback) needs privileged `modprobe` → ruled out on Fly microVM.
- **Clean Go path (orchestration):** ship `pulseaudio` in the image; `exec` `pulseaudio -D --exit-idle-time=-1 --disable-shm` (no systemd / **no D-Bus** / no root / no Xvfb — Chrome uses the native Unix socket); create the sink from Go by opening the PA native socket with a pure-Go **client** and calling `LoadModule("module-null-sink", …)` + `LoadModule("module-remap-source", …)` — **no `pactl` subprocess**. Precedent: **NoiseTorch** (`noisetorch/pulseaudio` `module.go`) does exactly this in production. Fits Omnipus's existing supervised-sidecar pattern (cf. Signal → `signal-cli`).
- **Licensing:** PulseAudio is LGPL client / GPL server *portions* — run as a **separate process over a socket** (not linked) ⇒ no taint on MIT; it's an OS/image dependency, not embeddable. PipeWire (+`pipewire-pulse`) is MIT-cleaner and lighter but needs **D-Bus** (wireplumber) → more container infra; prefer PulseAudio unless A/V-sync forces the switch.
- **Gotchas (research + empirical):** (1) capture the `.monitor`, remapped to a real source (Chrome hides raw monitors); (2) keep the daemon alive + route Chrome to the sink (`PULSE_SERVER`/default-sink); (3) **A/V clock drift** — null-sink monitors have non-monotonic timestamps; Mux only got stable sync after moving to PipeWire — budget timestamp-normalization if muxing audio with video.
- **Sources:** aerokube/images#403, jitsi/jibri, Mux "headless Chrome as a service" blog, NoiseTorch `module.go`, chromium issues 40155218 / 40176215, ArchWiki PulseAudio.

### S-4 — footprint (on-disk)
| Build | Size |
|---|---|
| `chrome-headless-shell` dir | 262 MB |
| full `chrome` dir | 382 MB |
| **delta** | **+120 MB (~46%)** |
- **Implication:** switching the managed download to full Chrome costs ~120 MB per install (G-5). Given S-1a (encoder works in headless-shell), this cost is only justified if capture or audio *requires* full Chrome — currently unproven.

## What this means for ADR-044

The decision (WebCodecs relay, pixel/video streaming) stands — the encoder works and the transport architecture is unaffected. But the **capture mechanism** is now the key unresolved risk, and the ADR's stated primary path is empirically dead. Remaining candidate capture paths, none yet proven headless here:

1. **Full Chrome headless + extension `chrome.tabCapture`** — the grill-spec "compliant" mechanism (no global flags). `--headless=new` supports extensions (Chrome 112+). Untested. Would justify full Chrome (extensions need it). **Highest-value next spike.**
2. **Headful + Xvfb + getDisplayMedia/tabCapture** — heavy (adds Xvfb + a display + likely dbus/audio infra), flaky in this container, and hurts the deployability constraint (NFR-2). Discouraged.
3. **Feed existing CDP `Page.startScreencast` frames into a browser encoder page** — CDP screencast works headless today, but the frames live server-side; piping them into a browser `VideoEncoder` reintroduces a transfer. Possible but architecturally awkward.

### S-1c′ (RUN) — capture mechanisms all blocked headless; full Chrome does NOT help
Tested the remaining capture paths to decide the full-Chrome question:
| Mechanism | Binary/mode | Result |
|---|---|---|
| `getDisplayMedia` | headless-shell & full, headless | ❌ `NotReadableError` (no display) |
| Extension `chrome.tabCapture` (MV3 + offscreen) | **full Chrome, headless** | ❌ extension loads & runs, but `getMediaStreamId` → **"Extension has not been invoked for the current page (activeTab)… Chrome pages cannot be captured"** — requires a **user gesture** (toolbar click) that headless has no way to produce |
| `getDisplayMedia` headful + auto-accept flags | full Chrome, **Xvfb virtual display** | ❌ full Chrome headful would not start/load the page cleanly in this container (dbus/headful fragility) — no beacons |

**Conclusion: full Chrome does NOT unlock video capture.** All in-browser capture APIs are gated behind user consent / a real display, which a headless server lacks. The extension route (the grill-spec "compliant" mechanism) is blocked by the activeTab **gesture** requirement, not by the binary. The only capture that works headless is **CDP `Page.startScreencast`** — the method Omnipus already ships. → **Do not switch to full Chrome; it costs RAM + attack surface + an extension to maintain and solves nothing here.** Audio is already solved in headless-shell via the PulseAudio sink (S-3), independent of the binary.

### Revised recommendation (post S-1c′)
**Option B is the best working solution:** keep CDP `Page.startScreencast` (works headless, already shipped) as the capture, feed those frames into a WebCodecs `VideoEncoder` running in a headless-shell encoder page to produce real video, and add audio via the PulseAudio-sink + `getUserMedia` path (S-3). All three pieces are empirically proven in the binary we already ship; no full Chrome, no extension, no Xvfb. Cost: an ingest round-trip to hand CDP frames to the encoder page (binary WS, not base64). This supersedes the ADR's getDisplayMedia-based capture design (§6.1) — ADR-044 needs a capture-mechanism amendment.

### S-5 (RUN) — smooth video FROM HEADLESS is achievable (Playwright proof, measured)
Challenge: "even Option B, watching a real video is hard (screencast is repaint-limited)." Tested Playwright's headless video recording as a counter-example:
- Recorded a page animating at 60fps in **fully headless Chromium** (no display, no Xvfb) via Playwright `recordVideo`.
- **ffprobe of the output: VP8, 1280×720, steady 25 fps** (149 frames / 5.96 s), ~836 kbps.
- **Conclusion: headless Chromium CAN produce smooth (~25 fps, film-rate) video with no display.** The earlier "video collapses to a few fps" was the JPEG-*delivery* + per-frame-ack bottleneck, NOT the capture. This substantially de-risks video watching.
- **Mechanism nuance:** Playwright's pipeline is browser screencast frames → **bundled `ffmpeg`** (JPEG→VP8 transcode) → webm file. `ffmpeg` is a C tool (would be an exec'd sidecar, not pure-Go — acceptable per the sidecar precedent, but avoidable). **Our cleaner path: same ~25fps screencast capture → the browser's own WebCodecs `VideoEncoder`** (proven in headless-shell, no C) instead of ffmpeg → live chunks. Either way, smooth headless video is real.
- **CORRECTION (research, from Playwright source):** Playwright's "25 fps" is **ffmpeg frame-DUPLICATION** to a hardcoded constant rate — NOT 25 distinct frames. Underlying capture is CDP screencast (jpeg q90), repaint-driven; Chrome **declined** to add an fps-up control (`everyNthFrame` only throttles *down*). Production data (**Steel**) measured screencast at **4–12 fps, janky** for full-motion. So true-headless screencast is fine for UI/scroll but **NOT smooth for video playback.** My "24-30fps in reach headless" was wrong.

### S-5 VERDICT (definitive, cited): smooth full-motion video REQUIRES a virtual display
Every vendor that ships smooth video runs **headful Chrome on a virtual display (Xvfb/Wayland)** and captures the framebuffer — there is NO supported smooth-video-from-truly-headless path:
| Vendor | Path | Display |
|---|---|---|
| Mux | ffmpeg x11grab framebuffer + PipeWire audio | headful on Xvfb |
| neko (OSS) | Xorg → GStreamer → WebRTC | headful on Xorg |
| Kasm | web-native encoder (4K/60fps) | headful + virtual display |
| **Steel** | **abandoned CDP screencast (4-12fps janky) → headful + WebRTC @ 25fps** | headful |
| Hyperbeam | GPU Chromium → WebRTC | headful, GPU |
| Browserbase | CDP screencast (PNG) | true headless — **explicitly NOT smooth**, observability only |

**Conclusion:** with a virtual display present, `getDisplayMedia`/`tabCapture` WORK (the earlier headless failures were "no display", not the API) → capture the framebuffer at real framerate → encode with the browser's WebCodecs (no ffmpeg needed) or ffmpeg/GStreamer. Cast tab-mirroring (Chrome's real 24-30fps compositor capturer) is a moonshot (needs a fake Cast receiver; unsupported).

## THE DECISION (two real architectures)
| | **A1 — Light (true headless)** | **A2 — Full (headful + virtual display)** |
|---|---|---|
| Browser | headless-shell (already shipped) | **full Chrome** (+120MB) |
| Capture | CDP screencast → WebCodecs encoder | Xvfb virtual display + getDisplayMedia/tabCapture → WebCodecs (or ffmpeg/GStreamer) |
| Extra infra | none | **Xvfb** + more RAM/CPU (SwiftShader on GPU-less pod) |
| Watch agent work (UI/scroll) | ✅ good, big win over today | ✅ |
| Watch full-motion **video** | ❌ janky (4-12fps) | ✅ smooth (25-30fps) — the pro path |
| Audio | ✅ pulseaudio sink (proven) | ✅ pulseaudio sink |
| Complexity / fragility | low | higher (the pros run it in containers; proven but heavier) |
| Full Chrome justified? | no | **yes — this is what justifies it** |

**The choice is a product call: light-but-video-is-janky (A1) vs heavier-but-smooth-video (A2, = the full-Chrome+Xvfb path all the pros use).** A2 vindicates the operator's "ship full Chrome" instinct — full Chrome IS justified, but only together with a virtual display, for smooth video.

### S-A2 (attempted) — headful Chrome on Xvfb: startup PROVEN, fps measurement blocked by the dev-pod sandbox
Operator chose "prove the smooth path first." Progress:
- ✅ **Headful full Chrome launches on Xvfb and loads a page** in this container (verified: a headful Chrome under `Xvfb :99` + `dbus-run-session` fetched a beacon — `HIT /b?ok=1`). The earlier "headful didn't start" was a missing **dbus session** (`dbus-run-session` fixes it); remaining dbus/UPower errors are harmless.
- ❌ **Could NOT complete the integrated getDisplayMedia-capture-of-a-playing-video fps measurement**: every attempt to run the full pipeline (Xvfb + headful Chrome + getDisplayMedia + result POST) — via `run_in_background`, foreground `timeout`, node-spawned children, bash-spawned children, and fully-detached `setsid nohup` — was killed by the **agent dev-pod sandbox** (exit 144 / silent kill of the heavy Xvfb+headful-Chrome process tree). Resources were fine (9 GB RAM free, disk OK); it's a sandbox watchdog on that process pattern, **not** a technology limit and **not** how Omnipus runs in production (there the browser pipeline IS the normal workload, not a sandboxed sub-agent spawn).

**Where that leaves the smooth-path proof (strong, convergent):** every *component* is individually confirmed here — headful Chrome runs on Xvfb (✅), getDisplayMedia fails only for lack of a display (✅ established), WebCodecs `VideoEncoder` works (✅ S-1), audio via the sink works (✅ S-3). The research is definitive that this exact stack yields **25–30 fps smooth video** (Steel measured 25 fps headful+WebRTC; Mux/neko/Kasm/Hyperbeam all run headful-on-virtual-display). The only unverified link — the integrated in-container fps number — is blocked by the sandbox, and should be measured on the **CI worker (`ci-omnipus`) or a real deployment**, where the pipeline runs as normal workload.

## Open spikes
- **S-1c′ (NEW, top priority):** extension `chrome.tabCapture` in full Chrome `--headless=new` — does tab capture work at all headless via the compliant mechanism? This gates whether full Chrome is needed and whether the whole approach is viable headless.
- **S-2:** `VideoDecoder.isConfigSupported` (H.264-main / VP8 / VP9 / AV1) on the operator's iPad Safari — needs the physical device.
- **S-3:** tab audio via a virtual sink (only relevant once capture works; also the other potential full-Chrome justification).

## Recommendation
Do NOT switch the installer to full Chrome yet (S-1a removes the encoder justification; S-4 confirms the cost). Run **S-1c′ (extension tabCapture headless)** next — it is the true go/no-go for the capture mechanism and for the full-Chrome question. Reconcile ADR-044 §6.1/§6.4 (codec policy H.264-baseline→main) and the capture-mechanism assumption after it.
