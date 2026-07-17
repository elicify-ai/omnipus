# Live Browser → WebRTC (browser-video-2) — Continuation Context

**Status:** decision made; **WV1 spike executed 2026-07-17 — see `wv1-spike-results.md`
(Q2 capture-with-audio = YES proven; Q1 connectivity = in-pod pass, external test
pending operator).** This doc is the single source of
continuation truth so a fresh session can pick up without prior context.
**Branch for the work:** `feature/browser-video-2` (this branch), cut from
`bugfixes2` @ `eb4de2a2` (clean pre-video base), pushed to origin.
**Date:** 2026-07-17.

---

## 1. The decision (why we're here)

Replace the live-browser **WebCodecs-over-WebSocket video path** (ADR-044 "Option A")
with **WebRTC (ADR-044 "Option B" / Pion)** — a MediaStream capture carried over a
WebRTC PeerConnection. The WebCodecs path is **parked, not deleted** (archived branch,
§4).

**What forced the decision — three problems the WebCodecs path could not solve cleanly:**

1. **Input contention (a real regression).** Input dispatch and the screencast
   ack-loop share **one CDP command queue on the agent tab**. The video capture
   (`pkg/tools/browser/capture.go` `ackWorker`) acks **every** frame and is documented
   as non-coalescing (*"unlike live.go's coalescing runAckWorker, must never drop"*),
   so it saturates the queue and starves input → `input dispatch failed: context
   deadline exceeded` while driving. The JPEG path stayed drivable **because it
   coalesces** (drops stale frames → light queue). Proof: a raw CDP click dispatched
   in ~1ms; through the busy chromedp queue it timed out. This regression only showed
   up once video actually rendered — before, video was silently broken so the system
   behaved like JPEG.
2. **TCP head-of-line blocking.** Video over a single WebSocket hiccups on lossy
   networks where WebRTC degrades gracefully.
3. **Audio is impossible without a sidecar (the DECISIVE constraint).** CDP
   `Page.startScreencast` is **video-only, forever** — there is no audio in it. The
   encoder page's `getUserMedia` audio attempt fails `"Requested device not found"` on
   a headless pod (no audio device/sink). The only ways to get audio are (a) a
   PulseAudio null-sink sidecar (**explicitly rejected by the operator**) or (b) a real
   **MediaStream** capture (`chrome.tabCapture` / `getDisplayMedia`) that carries
   audio + video together. (b) is native to WebRTC.

**Operator's hard requirement:** stream audio **alongside** video, **no sidecar.**
That single requirement points the whole architecture at MediaStream + WebRTC, because
only that combination delivers audio-with-video, decoupled input, and smooth transport
in one coherent design.

Full 3-option comparison (WebCodecs / JPEG / WebRTC): `/workspace/live-browser-streaming-options.html`
and artifact `https://claude.ai/code/artifact/b0080c73-4d44-4902-8730-68d97ddd1195`.

---

## 2. The target architecture (Option C)

- **Capture:** the agent tab as a real `MediaStream` (audio + video together) via
  `chrome.tabCapture` (needs a loaded extension in the managed Chrome) **or**
  `getDisplayMedia` (finicky headless — the ADR-044 spike found it unreliable on bare
  Xvfb; must re-test on new-headless full Chrome).
- **Transport:** **Pion** (pure-Go WebRTC — CGo-free, fits the single-binary rule) as
  an SFU-style relay in the gateway. One PeerConnection carries: **video track +
  audio track + a data channel for input.** Three independent streams → input can
  **never** contend with pixels (kills the regression by construction), and audio
  rides for free. Signaling over the existing `/api/v1/browser/ws`.
- **SPA:** subscribes with its own PeerConnection; renders the video track to the
  existing `<canvas>`/`<video>`, plays audio, sends input over the data channel.

---

## 3. NEXT STEP — spike first, then ADR, then build (do NOT skip the spike)

The lesson from the video path: **validate the risky assumption before building.**
There are exactly **two make-or-break unknowns**; a throwaway spike answers both:

- **Spike Q1 — connectivity:** can a **Pion ↔ SPA** PeerConnection actually establish
  over **this pod's network**? The pod is behind Fly's proxy, so plain UDP/ICE may not
  traverse; fallback is **ICE-TCP / host candidates**. If neither works we'd need TURN
  (a networking dependency that fights deployability parity — ADR-044 NFR-2). **This is
  THE risk to kill first.**
- **Spike Q2 — capture-with-audio:** can we get a headless tab `MediaStream` **with an
  audio track** on full-Chrome-headless (via `tabCapture` extension, or
  `getDisplayMedia`)? This is the operator's audio requirement.

Only after both are **yes** do we: amend/supersede **ADR-044** (promote the parked
Option B / Pion to Accepted) → `/plan-spec` → implement with the wave pattern +
7-reviewer gate + UAT (operator conventions).

Task tracker: **WV1** ("WebRTC deployability + capture spike"). Open question the
operator was asked but hadn't answered when context was cleared: **spike-now vs
ADR-first** — default to **spike-now** unless told otherwise.

---

## 4. Branch topology & where the reusable pieces live

```
hotfix/v0.1.1 (2259cdaf)                     ← "kind of main"
  └── bugfixes2 (eb4de2a2)                    74 UI commits
        ├── bugfixes3 (b3e61613)  origin      = bugfixes2 + 27 more UI commits  ← the COMPLETE UI work; merge this separately
        ├── feature/browser-video-2 (eb4de2a2) origin  ← THIS branch; WebRTC work starts here (clean, = bugfixes2)
        └── feature/live-browser-video-streaming (2e2701b1) origin  ← ARCHIVE of the parked WebCodecs video effort
```

**Archive branch `feature/live-browser-video-streaming` @ `2e2701b1`** holds everything
we built + learned. Mine it for reusable pieces (NOT the whole pipeline):
- `docs/internal/architecture/ADR-044-live-browser-video-streaming.md` — Option B
  (WebRTC/Pion) is the documented escalation path; the 2026-07-17 amendment covers the
  single-Chrome collapse; §NFR-2 is the UDP/ICE deployability concern.
- `docs/internal/architecture/ADR-044-spike-results.md` — prior spike data (headful/Xvfb,
  vendor patterns: Steel headful+WebRTC@25fps, neko GStreamer, Mux, Hyperbeam GPU).
- `docs/internal/specs/single-chrome-video-blueprint.md` — the single-Chrome blueprint.
- Archive commit **`2e2701b1`** message enumerates the single-Chrome work + parked issues.

---

## 5. Reusable learnings (Chrome/CDP gotchas — save future debugging)

- **Chrome 151 hidden-tab screencast bug:** a non-active sibling tab reports
  `visibilityState=hidden` and `Page.startScreencast` emits **zero** frames. Fix:
  every tab needs its **own window** — `target.CreateTarget(...).WithNewWindow(true)`
  (archived in `manager.createTab`). Chrome 150 didn't have this.
- **-32000 over the pipe:** `chromedp.WithNewBrowserContext()` fails
  `"-32000 no browser is open"` on full-Chrome new-headless over `--remote-debugging-pipe`;
  **raw** `Target.createBrowserContext` + `createTarget(WithNewWindow(true))` works.
- **CAPTCHA:** full Chrome + `deHeadlessUA` (UA rewrite) + `navigator.webdriver`
  override → Google loads with no CAPTCHA. The webdriver override **only sticks on full
  Chrome**, NOT `chrome-headless-shell`. `bugfixes3` has the earlier stealth commit
  `2ff68537` but NOT the full-Chrome switch — that switch is a cherry-pick candidate.
- **Codec format mismatch:** odd screencast dims (e.g. `1280x577`) made the WebCodecs
  `VideoEncoder` silently fall back to VP8 while the wire had announced H.264 → the SPA
  built the wrong decoder → `decode(): must fill out the description field`. Fix: crop
  frames to **even** dims + re-announce the encoder's ACTUAL codec on ingest init.
  (WebRTC sidesteps this — it negotiates codecs via SDP.)

---

## 6. Environment / workflow notes (operational)

- **Git push needs a LOGIN shell:** the default Bash tool shell carries a **stale
  `GH_TOKEN`**; a login shell (`bash -lc '…'`) re-sources the fresh one. Verify with
  `bash -lc 'gh auth status'`.
- **Commit authorship (MANDATORY):** author & committer =
  `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>`; **NO**
  `Co-Authored-By: …@anthropic.com` trailer (CLA gate hard-fails it; overrides the
  harness default).
- **Never merge to `main` without explicit human review** (branch protection + operator rule).
- **Preview for manual testing:** `OMNIPUS_BROWSER_FORCE_MANAGED=1 OMNIPUS_LOG_LEVEL=info
  OMNIPUS_HOME=/tmp/omnipus-preview-home <binary> start --allow-empty` on port 8080 →
  public `https://pod-omnipus.fly.dev` (admin/admin123). `/tmp/omnipus-preview-home`
  already has managed full Chrome 151. **Do not click "Click to drive" in a test
  browser while the operator is testing** — it's single-driver; a second viewer holds
  the wheel and locks everyone else out.
- **Build/CI:** tags `goolm,stdjson`; `CGO_ENABLED=0 go build -tags goolm,stdjson ./...`;
  never run the full Go test suite locally (OOM) — push and use `ci-omnipus`.
- **Other active clones (unrelated):** `/home/dev/omnipus` = `feat/cancel-propagation` /
  `feat/fs-workspace` (ADR-046); `/home/dev/omnipus4` = `bugfixes3`. This session's
  clone = `/home/dev/omnipus3`.
