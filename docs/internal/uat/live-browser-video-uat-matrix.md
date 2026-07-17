# UAT Test Matrix — Live-Browser Video Streaming (ADR-044)

Feature: the live browser panel streams the agent's Chrome as real video (WebCodecs relay over the existing WSS) instead of JPEG-per-frame, with audio, take-the-wheel, and an honest unavailable state on non-video-capable installs.

**Execution model:** each row is a human-tester scenario. UAT impersonation subagents execute them against a running gateway (Playwright MCP for UI/driving; the `ci-omnipus` worker for the full A2 stack — headful full Chrome on Xvfb + PulseAudio — which the dev pod's sandbox can't run). Rows are tagged by where they run: **[UI]** dev-pod/any gateway; **[A2]** needs the full video stack; **[DEV]** device-specific (iPad — deferred, EC-4).

**Persona:** "Dana", a self-hoster who runs Omnipus on a Linux box and watches/drives her agent's browser from a laptop (Chrome) and occasionally an iPad (Safari).

| # | Persona goal | Steps | Expected result | Traces | Tag |
|---|---|---|---|---|---|
| U-1 | Watch the agent browse, smoothly | Attach the live panel while the agent loads a full-motion page/video | Video renders on the canvas at ≥24fps, no JPEG tearing; no frozen frame; tab/URL bar update | US-1, SC-003 | [A2] |
| U-2 | Take the wheel | Click "take control", click a link, scroll, type in a field | Input lands at the correct coordinates (canvas mapping == old `<img>`); the agent's page responds; control lock respected | US-2, FR-008 | [A2] |
| U-3 | Second viewer joins mid-stream | Open the same session's live view in a 2nd tab/browser | 2nd viewer paints within ~1s (GOP keyframe replay), then live; neither viewer stalls the other | US-3, FR-003/004 | [A2] |
| U-4 | Detach / last viewer leaves | Close all viewers, re-open | Stream tears down when the last viewer detaches; re-attach starts a fresh stream cleanly | US-3, FR-018 | [A2] |
| U-5 | Hear the page audio | Attach to a session playing audio | Audio plays, roughly A/V-synced; muting/unmuting works | US-4, FR-011/023 | [A2] |
| U-6 | Audio sidecar absent | Same, on an install with no PulseAudio | Video plays normally; audio is silently absent; no error, no video degradation | US-11, FR-011 | [A2] |
| U-7 | Old/unsupported browser | Open the live view in a browser with no `VideoDecoder` (or stub it) | Panel shows the generic "Live view needs a video-capable browser"; chrome controls (URL bar, tabs) still operable; NO JPEG, no blank, no spinner-forever | US-5, FR-007 | [UI] |
| U-8 | Non-video-capable install (no Xvfb / headless-shell / non-Linux) | Attach live view on such an install; also drive the agent to browse | Live view shows the unavailable state; **agent browsing still works** (navigate/screenshot/tools succeed) | US-10, FR-007, Platform Matrix | [UI]/[A2] |
| U-9 | Operator kill-switch | With a stream live, set `gateway.browser_video_enabled=false` and reload config | New attaches get the unavailable state AND the active stream tears down to unavailable — no redeploy | US-?, FR-020 | [UI] |
| U-10 | iPad viewing | Open the live view on the operator's iPad Safari | Video decodes (H.264-main) and plays; controls usable | NFR-1, EC-4 | [DEV] |
| U-11 | Codec negotiation | Attach from a viewer advertising only VP8, then only H.264 | Server serves the negotiated codec off the single encoder; a disjoint-codec 2nd viewer gets the unavailable state (never a 2nd encode, never JPEG) | US-6, FR-006 | [A2] |
| U-12 | Fresh install provisioning | Fresh Linux video-capable install → first browser use | Full Chrome is downloaded + integrity-verified; classified video-capable; live view streams | US-8, FR-009 | [A2] |
| U-13 | Take-the-wheel latency feel | Drive during a live stream; observe responsiveness | Input feels responsive (glass-to-glass acceptable); no runaway lag or frozen cursor | US-2, SC-002 | [A2] |
| U-14 | Stream failure recovery | Kill the agent tab / capture mid-stream | Viewer is moved to the unavailable state promptly (no frozen last frame, no infinite spinner) | FR-018, Test 7 | [A2] |
| U-15 | Ingest security (adversarial) | From a co-tenant process, try to connect to the capture-ingest endpoint / reach CDP | Non-loopback rejected; no/invalid/mis-scoped token rejected; CDP has no TCP port to reach (pipe); no token recoverable | US-9, FR-012/013, EC-3 | [A2]/[UI] |
| U-16 | Agent-page cannot capture | Have the agent navigate to a page that calls getDisplayMedia/getUserMedia | The agent-browsed page gets NO media stream (video capture is server-side CDP; audio grant is origin-scoped to the encoder page only) | FR-016, EC-2 | [A2] |
| U-17 | Regression: normal browsing unchanged | Run the standard browser tool suite (navigate, screenshot, tabs, annotate) on the headful full-Chrome runtime | Behaviorally equivalent to the headless-shell baseline (Equivalence Corpus, SSIM ≥ 0.95) | FR-009, Test 16 | [A2] |

## Severity + exit criteria
- **Blocker:** any of U-1 (no smooth video on a capable install), U-7/U-8 (broken/blank instead of honest unavailable), U-15/U-16 (security), U-17 (browsing regression).
- **Major:** U-2/U-3/U-5/U-9/U-14 failing.
- UAT passes when all [UI] + [A2] rows pass on the target platforms; U-10 [DEV] is the pre-release iPad gate (EC-4, operator device).

## Notes for the impersonation testers
- Drive the SPA via Playwright MCP against a running gateway; assert on the canvas element, the unavailable-state text, control behavior, and console (zero errors; WS reconnect warnings OK).
- For [A2] rows, run against a gateway with the full stack on `ci-omnipus` (Xvfb + PulseAudio + full Chrome installed as in the Gate-0 setup).
- File every deviation as a finding with repro steps; the lead fixes and re-runs the affected rows.

---

## Run 1 — Option-B "gate-dormant" increment (2026-07-17)

Scope note: per the operator's Option-B decision (ADR-044 §6.0.4), this increment ships the feature **dormant** (sidecars unwired → NotCapable/headless-shell/unavailable-state). Executed against the `754036d6`/`cd91d541` binary on an internal-port gateway via Playwright MCP + curl. The [A2] real-headful-video rows require the sidecar-wiring increment on `ci-omnipus` and are **deferred**, as is U-10 [DEV] (iPad, EC-4).

| Row | Result | Evidence / Notes |
|---|---|---|
| **U-7** unsupported/absent video | **PASS** | Live panel resolves to the exact generic string **"Live view needs a video-capable browser"** (FR-007); chrome controls (address bar, back/refresh, tabs) operable; **no JPEG, no blank, no infinite spinner**; 0 console errors. Screenshot: `uat-U7-U8-unavailable-state.png`. |
| **U-8** non-video-capable (dormant) install | **PASS (UI)** | Dormant classifier → unavailable state renders correctly, no crash. "Agent browsing still works" leg depends on Chrome install, which the **U-12 finding below** was blocking — now fixed. |
| **U-9** operator kill-switch | **PASS (logic)** | FR-020 hermetically covered (`SetVideoEnabled(false)` teardown + reload wiring); no active stream to tear down in a dormant/no-Chrome pod. UI toggle deferred to the video increment. |
| **U-15** ingest/CDP security | **PASS (4/4, adversarial)** | Impersonation security tester ran real WebSocket attacks on `/api/v1/browser/capture-ingest` — garbage/empty/mis-scoped token, chunk-before-init, bad frame, `Origin: evil` → **all closed/403 before any relay, all audited** (`browser.live.ingest_rejected`, HMAC-chained). No CDP TCP port (all Chromes `--remote-debugging-pipe`, 9222/9223 refuse); token is CDP-injected out-of-band, absent from URL/logs/audit; no `--use-fake-ui-for-media-stream`; audio grant origin+context scoped. No findings. |
| General SPA health | **PASS** | Login → chat → open browser panel: **0 console errors** throughout; the round-2 SPA changes (SF-H1 decode-error→unavailable, GC-5 zero-len, comment fixes) integrate cleanly in a real browser. |
| U-1..U-6, U-11, U-13, U-14, U-16, U-17 | **DEFERRED [A2]** | Require real headful Chrome on Xvfb + PulseAudio → next increment on `ci-omnipus`. |
| U-10 | **DEFERRED [DEV]** | iPad Safari, EC-4, operator device. |

### Findings

- **UAT-1 (BLOCKER, FIXED — `cd91d541`): Chrome download broken on every real install.** The round-1 SF1 integrity check read only `header.Get("X-Goog-Hash")` (first value). Real `storage.googleapis.com` sends **two** `X-Goog-Hash` lines — `crc32c=...` **first**, then `md5=...` — so the verifier saw only crc32c, found no md5, and hard-rejected the download (`integrity check failed: X-Goog-Hash header present but carries no md5 checksum: "crc32c=XqmS2Q=="`). This broke Chrome installation on every fresh install (U-12), and with it all agent browsing (U-8) and the whole video feature (U-1..). Missed by 16 reviewer passes + CI because tests set a single md5-only header. Fixed to scan all `X-Goog-Hash` lines; regression test `TestVerifyGoogHashMD5_MultipleHeaderLines` added. This is the headline UAT outcome.
- **UAT-2 (observation, FIXED — `754036d6`): cold bring-up failures were not audited.** Surfaced while adding timeout coverage; `startStreamLocked` unwound manually without an audit record. Added `auditBringupFailed` + regression test.
