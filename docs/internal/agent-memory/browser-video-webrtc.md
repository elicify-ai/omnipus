---
name: browser-video-webrtc
description: Live-browser view is moving to WebRTC (Pion); the WebCodecs video path is parked. Work starts on branch feature/browser-video-2. Full details in a repo doc.
metadata: 
  node_type: memory
  type: project
  originSessionId: 45eb163f-c6f7-4e88-8de3-bcc8667cefc7
---

The live-browser video effort pivoted to **WebRTC** (ADR-044's parked "Option B" /
Pion). The WebCodecs-over-WebSocket path was made to render but is **parked**, not
shipped.

**Decisive reason:** the operator requires **audio streamed WITH video, NO PulseAudio
sidecar**. CDP `Page.startScreencast` is video-only forever, so audio-without-sidecar
requires a real **MediaStream** capture (`chrome.tabCapture` / `getDisplayMedia`) —
native to WebRTC. WebRTC also fixes the input-vs-video CDP-pipe contention (data
channel decouples input) and TCP head-of-line blocking, all in one design.

**Branches (all on origin):**
- `feature/browser-video-2` @ eb4de2a2 — the WebRTC work starts here (cut from
  `bugfixes2`, clean pre-video base).
- `feature/live-browser-video-streaming` @ 2e2701b1 — ARCHIVE of the parked WebCodecs
  effort + single-Chrome work + full-Chrome CAPTCHA switch. Mine for reusable pieces.
- `bugfixes3` @ b3e61613 — the COMPLETE UI work (bugfixes2 + 27 commits); merged separately.
- `hotfix/v0.1.1` = "kind of main".

**WV1 spike EXECUTED 2026-07-17** (results committed:
`docs/internal/design/wv1-spike-results.md`, code in `spikes/wv1-webrtc/`):
- **Q2 capture-with-audio = YES, proven.** Headless full Chrome 151 delivers tab
  MediaStream with ~30fps video + REAL audio samples, no sidecar/flags. Recommended
  path: tabCapture MV3 extension via CDP `Extensions.loadUnpacked` (pipe transport
  required; `--load-extension` dead since Chrome 137) + `--allowlisted-extension-id`.
- **Q1 connectivity = in-pod PASS, external test PENDING** — operator must open the
  preview URL from their machine (DEFAULT mode, ~90s, paste report). Pod outbound
  UDP+STUN confirmed (srflx v4+v6). Pion gotcha: `dc.Send()` is binary-typed — use
  `SendText()` for browser text frames.
- **Q3 e2e = YES (in-pod).** Full pipeline tabCapture→WebRTC→Pion relay→viewer:
  VP8+Opus, 30fps, 0 loss, audio RMS pulsing exactly 1Hz (metronome page = human
  A/V-sync proof). Pion learnings (PLI to encoder PC, RegisterDefaultCodecs,
  drain goroutines) in wv1-spike-results.md.
- **Q4 bidirectional = YES (in-pod).** Input DC + CDP dispatch while 30fps A/V:
  stress p50=10.9ms p95=21ms max=88ms (old bug: 5000ms timeouts), media unaffected,
  22/22 events 1px-accurate. Gotcha: CDP keyDown needs text/unmodifiedText.
  Open design item: single-driver arbitration.
- **Spike /view demo RETIRED 2026-07-18** (port 8080 taken by a SIBLING Claude
  session's omnipus-preview gateway via socat→127.0.0.1:5000; full-Chrome 151
  binary evicted from /tmp/omnipus-preview-home during disk cleanup; encoder
  killed by a stray pkill). Do NOT tell the operator to test /view. The Q1
  external-traversal data point now comes from the real-feature UAT preview —
  coordinate port 8080 with the sibling session when deploying it.

**FEATURE BUILT 2026-07-18** (3 waves, branch @ 687c7c6e pushed): ADR-047 accepted;
foundations (cdppipe port, pipe coordinator, installer dual-download, Pion relay pkg,
captureext ext, contracts) + vertical slice (gateway signaling/capture-ingest/input-DC,
SPA PC state machine + video sink) + in-process Go e2e + Playwright e2e ALL GREEN
(video, audio RMS exact, pixel-exact drive, recapture, resilience, JPEG fallback).
**OPEN STRUCTURAL (needs operator/ADR decision):** tabCapture can't capture tabs in
CDP-created contexts → env-gated OMNIPUS_BROWSER_CAPTURE_DEFAULT_CONTEXT=1 hosts agent
sessions in the default context (loses per-agent cookie isolation); architect drafting
ADR options. **UAT live at preview URL** (socat 8080→127.0.0.1:18790; e2e gateway +
home under the SESSION SCRATCHPAD — ephemeral, rebuild recipe in commit 687c7c6e /
e2e report; login admin/e2e-test-pass-1; revert bridge to sibling preview:
kill socat then `socat TCP-LISTEN:8080,fork,reuseaddr,bind=0.0.0.0 TCP:127.0.0.1:5000`).
**MERGED 2026-07-19: PR #519 → hotfix/v0.1.1 @ 0decaa22** (operator-directed merge;
#514 stays open until hotfix→main carries "Closes #514"). Post-merge CI rerun =
operator's plan; ARM-matrix pkg/tools failures + e2e stale specs (#517) are inherited
lineage debt, evidence on the PR/issues. Final 14-reviewer pass done, all
blockers fixed in-branch (viewer-PC leak, orphaned-capture teardown, fence TOCTOU +
supersede-snipe, RTP-progress watchdog, ingest write deadline, encoder freeze recovery,
cold-start toast, focus restore). go-test gate ALL GREEN @ e6aebea9; e2e gate debt =
pre-existing lineage (#517, control-run proven). Operator: no CI reruns until after
merge. Issues: #514 epic (closes via main merge), follow-ups #509-#513, #516-#518.
UAT site on the PR head (socat 8080→18790; sibling session keeps re-grabbing 8080 —
re-check `ss -tlnp | grep 8080` whenever login breaks; operator standing-authorized
reclaim). Teardown/password-harden the public UAT instance when testing ends.

**FULL DETAILS (single source of truth, on feature/browser-video-2):**
`docs/internal/design/live-browser-webrtc-context.md` — decision, architecture, spike
plan, branch topology, and reusable Chrome/CDP learnings (Chrome-151 hidden-tab
zero-frames bug, -32000-over-pipe, codec even-dims/re-announce, full-Chrome CAPTCHA).

Related: [[browser-live-responsiveness]]. Note: git push needs a login shell
(`bash -lc`) — the default Bash shell has a stale GH_TOKEN.

## 2026-07-18 — "black when agent drives" UAT bug: root-caused + fixed (uncommitted on feature/browser-video-2)
- NOT the screenshot/captureBeyondViewport/constraint-pin path — all CDP primitives (navigate/FullScreenshot/viewshot/metrics-override/opentab/switchtab) probed CLEAN against a live captured tab, and a real agent turn driving its OWN captured tab streams fine (viewer follows navigation live).
- Root cause = ADR-048 condition-2 fence (pkg/gateway/browser_webrtc.go): denied ANY new capture when another agent merely had a live browser session -> after "human tests agent A's panel, then agent B's chat tools open B's session", EVERY panel attach (either agent) = webrtc_state 'error' -> black/JPEG forever. Second latent bug: encoder findActiveTargetTab falls back to FIRST non-extension tab (encoder's own window is last-focused), so a later agent's capture would bind the OLDEST agent's tab.
- Fix (verified live, tests green): (1) CaptureSession.Start does Page.bringToFront on the requesting agent's tab post-encoder-create (deterministic binding; pkg/tools/browser/capture_session.go); (2) fence re-scoped to deny only when another agent's capture has ViewerCount()>0, viewerless (grace) captures are superseded via Stop() (browser_webrtc.go + captureRegistry.otherSessions). 3 regression tests in browser_webrtc_test.go.
- Gotchas: managed Chrome can OUTLIVE gateway restarts (launch-lock adoption) — restart does NOT reset browser sessions; slog goes to gateway_panic.log, zerolog to stdout log; gpt-4o 400s any follow-up turn in a session whose history has a browser_screenshot image in a tool-role message (filed #510).
- **Port-8080 contention is ongoing**: the sibling session's omnipus-preview re-bound 8080 DIRECTLY (no socat) mid-UAT, hijacking the public URL → operator "can't login"/429 against the WRONG instance. Operator authorized takeover 2026-07-18 ("take the port back"): killed sibling gateway, socat 8080→18790 restored. If login breaks again with unknown-credentials/429, check `ss -tlnp | grep 8080` FIRST. Login limiter: 5 fails/15min keyed ip+username; behind socat all clients = 127.0.0.1 (gateway.trust_xff exists but off in UAT config; #511 filed, needs correcting to reference trust_xff). Reset = gateway restart.
