# Feature Spec — Live-Browser Video Streaming (WebCodecs relay)

- **Source ADR:** `docs/internal/architecture/ADR-044-live-browser-video-streaming.md` (Accepted 2026-07-16; §6.0 "A2" amendment + §6.0.1 "R3 reconciliation" + §6.0.2 "Gate 0 CI-worker results" are authoritative). Spike evidence: `docs/internal/architecture/ADR-044-spike-results.md`.
- **Status:** Draft spec, **Revision R7** (post round-5 grill BLOCK — **final grill round per operator**; both round-5 CRITs resolved). Mechanism = **(b) CDP `Page.startScreencast` → WebCodecs encoder page** (Gate 0). **EC-1 fps 30 on 8-core CI — not PASS until a min-spec re-run (pre-`/taskify` gate); EC-2 isolation PASS (structural); EC-3 CDP-token → pure-Go CDP-over-pipe transport — mechanism decided, but EC-3 is OPEN, a pre-BUILD gate (the transport must be built + proven; CRIT-002); EC-4 iPad DEFERRED (ship gate).** CRIT-001: the pipe transport requires a **significant ADR-043 coordinator rework** (lockfile single-launch + in-process context-sharing), carried as a build task. Stays A2-only (non-video installs lose today's JPEG live view, F-02).
- **Routing:** v0.3 (structural; **fresh-build, no back-compat** — see Constraint alignment below). Amends ADR-038 D3.
- **Handoff:** **No further grilling (operator directive).** Proceed to `/taskify`. **Pre-`/taskify` measurement gates:** SC-016 min-spec + EC-1 re-run at min-spec, SC-001a cold-start, SC-002 glass-to-glass. **Pre-build gates:** EC-3 (build the CDP-over-pipe transport + prove no TCP surface, CRIT-002) and the ADR-043 coordinator rework (CRIT-001); the MAJ-001 browsing-equivalence regression before the CDP-transport swap ships. **Ship gate:** EC-4 (iPad H.264-main decode). Codec-dependent surfaces (`video_caps`, encoder config, DS-1) are sequenced **last** and gated on EC-4 to bound rework if it inverts H.264↔VP8 (F-05).

## Revision R4 — Gate 0 CI-worker results (2026-07-16, AUTHORITATIVE over R3's two-candidate framing)

Gate 0 EC-1/EC-2/EC-3 were run on `ci-omnipus` (16 GB / 8-core, root, no dev-pod agent sandbox). Harness: headful Chromium on Xvfb (`xvfb-run … dbus-run-session -- node`, Playwright-driven) capturing a 720p30 full-motion clip. Full record: ADR-044 **§6.0.2**; memory note `browser-live-responsiveness`.

| EC | Result | Detail |
|----|--------|--------|
| **EC-1** fps ≥ 24 | ✅ **PASS — 30 fps (on 8-core CI)** | Headful Chromium/Xvfb via CDP `Page.startScreencast` = 30 fps (360 frames/12 s, ×5, incl. `--disable-gpu`) vs 4–12 fps headless. WebCodecs VP8 encode keeps up: **30.1 fps, zero drops**. **Caveat (F-04):** measured on `ci-omnipus` (16 GB/8-core); SwiftShader software encode is host-dependent — **must be re-run at the min video-capable spec (SC-016)** before it is asserted to clear the shipping config. |
| **EC-2** capture isolation | ✅ **PASS (structural)** | Mechanism (b) has **no agent-facing capture API** — `Page.startScreencast` is gateway-driven CDP; no `getDisplayMedia` video grant exists. The C-2 process-global-consent risk is **dissolved**, not mitigated. Only audio `getUserMedia` remains consent-scoped. |
| **EC-3** CDP-token | ⏳ **OPEN — pre-BUILD gate (CRIT-002)** | Mechanism decided (pipe transport) but **NOT passed**: the earlier "PASS" was for the reversed keep-9223 mechanism and does not carry over. CDP moves to **`--remote-debugging-pipe`** (inherited fd 3/4, **no TCP port / no `/json` / no HTTP surface**) via a new pure-Go CDP-over-pipe transport → a co-tenant cannot reach CDP on **any** kernel. **EC-3 clears only after that transport is built** and Test 30 proves no TCP/HTTP surface + token-unrecoverable. Kernel-independent (covers Linux < 6.7). |
| **EC-4** iPad decode | ⏳ **DEFERRED** | Operator: not testing iPad yet. Must probe **H.264-main (`avc1.4D40…`)** (VP8-decode doubtful on Safari). Device probe, not a code dependency — does not block spec/taskify; **is the final ship gate before release.** |

**Capture mechanism RESOLVED → (b), (a) rejected.** In-browser `getDisplayMedia` of the browser view is unreliable on bare Xvfb (0-for-3: `NotReadableError` / renderer crash / hang). The shipped path is **CDP `Page.startScreencast` on the headful agent tab → gateway loopback → controlled encoder page (`createImageBitmap`→`VideoFrame`→`VideoEncoder`) → encoded chunks back**. Consequences carried through this spec: the former "capture-page (`getDisplayMedia`)" surface is now an **encoder page** fed by screencast; **EC-1/EC-2 are settled, not open**; the R3 "Gate-0 selects a or b" language is superseded — (b) is selected. Pure-Go is preserved (Go shuffles bytes only). FR-001, FR-016, the Gate 0 table, and US-9 are updated to match; where older R3 phrasing says "the selected mechanism" or "two candidates," read: **mechanism (b), decided.**

## Revision R7 — Round-5 Grill Dispositions (what changed R6 → R7; FINAL grill round per operator)

The round-5 grill (`live-browser-video-streaming-spec-review-round5.md`) returned **BLOCK** (2 CRITICAL, 3 MAJOR, 5 MINOR) — both CRITICALs were flaws in R6's own CRIT-001/CRIT-002 fixes. **Operator directed no further grilling;** R7 resolves every finding and the spec proceeds to `/taskify`. Because there is no re-grill safety net, the CRIT-001 coordinator rework, the MAJ-001 browsing-equivalence gate, and EC-3 are carried as explicit **pre-build / pre-ship gates** into implementation.

| Finding | Sev | R7 disposition |
|---|---|---|
| **CRIT-001** "marker-file-only ownership" (R6) drops the **atomic** single-launch primitive — the `net.Listen(":9223")` bind (`coordinator.go:757`) was the launch-race + grill-M2 foreign-Chrome guard; and a pipe (fd 3/4) means a second process cannot attach at all, breaking ADR-043 cross-process sharing | CRIT | **Coordinator rework specified.** (a) single-launch atomicity: port-bind → **`O_EXCL`/`flock` lockfile** (portable, no port); marker stays the identity layer. (b) CDP sharing: in-process managers use **chromedp child contexts of the launcher `rootCtx`** (one pipe, multiplexed), NOT `RemoteAllocator(ws://9223)`. (c) **cross-OS-process** CDP consumers (CLI) MUST route through the gateway API — the pipe is private to the launcher. Significant rework of a working subsystem; its own build task; ADR-044 §6.0.3-pt3. |
| **CRIT-002** Gate 0 STATUS asserts EC-3 "PASSED" for the pipe transport, which is unbuilt (bespoke pure-Go transport chromedp lacks) | CRIT | **EC-3 marked OPEN, not passed.** The "PASS" referred to the reversed keep-9223 mechanism and does not carry over. EC-3 is a **pre-build gate**: build the pipe transport first, then prove no TCP/HTTP surface + token-unrecoverable (Test 30). Status + EC-3 rows corrected. |
| **MAJ-001** the pipe swap is on `managedExecAllocatorOpts` = shared by EVERY managed launch, so ALL browsing (navigate/drive/screenshot) rides the new transport — under-rated as a token-confidentiality item | MAJ | Re-rated **CRITICAL blast radius**; added a **browsing-equivalence regression gate** (every `browser.*` tool behaves identically over the pipe vs the old port) that MUST pass before the swap ships. FR-009 + impact row updated. |
| **MAJ-002** SC-001a/SC-002 labeled both "pre-`/taskify` gate" and "first-wave task" (the EC-1 contradiction, unfixed for these two) | MAJ | SC-001a and SC-002 made **pre-`/taskify` measurement gates**, consistent with EC-1/SC-016 (AW-4 aligned). |
| **MAJ-003** audio (US-4, P1) has no Gate-0 exit criterion; SC-013 cites a nonexistent "AUDIO Gate-0" → Test 26 untestable | MAJ | SC-013 A/V-skew reworded to a **build-phase measurement** (audio sequenced after video; no Gate-0 citation); Test 26 anchored to that build-phase measurement. |
| **MINORs** 9223 removal misses `BindPortRules` (`sandbox_apply.go:388`) alongside the connect rule (:419); encoder-relaunch doesn't re-grant `audioCapture`; a superseded annotation code-falsely says "9223 is not in the connect allow-list" (it IS, :419) | MIN | Sandbox removal covers **both** `BindPortRules:388` and `ConnectPortRules:419`; the re-mint/relaunch (CRIT-002) MUST re-grant `audioCapture` to the relaunched encoder page; the false annotation corrected. |

**Operator directive (2026-07-16):** round 5 is the **last grill** — no further grill rounds. The CRIT-001 coordinator rework, the MAJ-001 browsing-equivalence gate, and EC-3 (CRIT-002) are carried as explicit **pre-build / pre-ship gates** into `/taskify` and implementation.

## Revision R6 — Round-4 Grill Dispositions (what changed R5 → R6)

The round-4 grill (`live-browser-video-streaming-spec-review-round4.md`) returned **BLOCK** (2 CRITICAL, 5 MAJOR, 5 MINOR, 2 OBSERVATION). Both CRITICALs were flaws in R5's own fixes. R6 resolves all; CRIT-001 carried an **operator decision (2026-07-16)**.

| Finding | Sev | R6 disposition |
|---|---|---|
| **CRIT-001** "keep port 9223 + Landlock isolation" (R5/F-01) only secures on kernel **6.7+** (Landlock ABI v4 `NET_CONNECT_TCP`, `sandbox.go:44-45,70`); below that `ConnectPortRules` silently degrade, so agent `bash` can dial 9223 and steal the token — SC-017's "100%" was false on most installs | CRIT | **Mechanism REVERSED → pure-Go CDP-over-pipe transport (operator decision).** CDP over `--remote-debugging-pipe` (inherited fd 3/4, NUL-delimited JSON) — **no TCP port, no `/json`, no HTTP surface** → a co-tenant cannot reach CDP on **any** kernel (kernel-independent; covers Constraint #4 Linux < 6.7). **New work item:** a small pure-Go CDP-over-pipe transport (chromedp v0.15.1 lacks it — reuse `cdproto` over a pipe `Conn`). **ADR-043 coordinator reworked:** `sharedChromeCDPURL()`/fixed-port ownership removed → marker-file-only ownership. **Landlock 9223 allow-list + `checkDebugPortAvailable` removed** (no port → attack-surface reduction). EC-3 kernel-independent + honest. Supersedes R5/F-01. Details: ADR-044 §6.0.3. |
| **CRIT-002** connection-epoch discriminator (R5/F-06) self-contradictory — gateway-minted ⇒ every connect supersedes (duplicates never rejected); client-carried ⇒ a replayer presents a higher epoch and wins | CRIT | **Epoch dropped; race eliminated.** For the gateway-controlled **loopback** encoder page, a transient ingest drop **re-mints the token + relaunches the encoder page** (cheap, local) — there is **no same-token reconnect**, so no reconnect-vs-duplicate ambiguity. M-5's reconnect-survival concern doesn't apply to a loopback page the gateway owns (a "drop" is an encoder-page crash you relaunch anyway). FR-013, DS-4, US-9/AC-2, Test 29 rewritten. |
| **MAJ-001 / 002** min-spec EC-1 re-run deferred to first-wave (re-opens C-1) + contradicts SC-016 "before /taskify" | MAJ | **Min-spec EC-1 re-run is a PRE-`/taskify` gate**, not a first-wave task. EC-1 is **not asserted PASS for the shipping config** until re-run at the min video-capable spec; SC-016, Handoff, and Gate-0 §STATUS made consistent. |
| **MAJ-003** origin-scoped audio consent defeatable if the agent navigates its tab to the encoder-page origin | MAJ | The encoder-page origin MUST be **unguessable and unreachable by the agent** (random loopback origin + secret path; the `audioCapture` grant bound to the specific encoder-page **target**, not merely its origin). FR-016 tightened. |
| **MAJ-004** NFR-1/2/3 referenced but never defined | MAJ | Added a **Non-Functional Requirements** section (NFR-1 Safari/iPad + Chrome/Edge/Firefox; NFR-2 deployability — WSS-only, no extra ports/UDP/per-install config; NFR-3 footprint — Go-process < 10 MB) from ADR §2. |
| **MAJ-005** GOP LRU eviction of a stream with an attaching viewer unspecified | MAJ | Eviction MUST NOT drop a stream with a live or attaching viewer; on pressure it forces a fresh keyframe rather than starving the viewer. Test 5 extended. |
| **MINORs** stale `displayCapture` grant; zero-length chunk reject w/o FR; unenforced `browser_screencast`-removal sequencing MUST; SC-013 anchors a nonexistent Gate-0 number; misleading "token never in /json" | MIN | `displayCapture`→`audioCapture` (audio-only); zero-length reject backed by an FR; removal-sequencing MUST given a test hook; SC-013 anchored to the pending audio Gate-0 measurement; "/json" framing replaced by "no TCP/HTTP CDP surface at all" (pipe). |

**Operator decision (2026-07-16):** CRIT-001 → **pure-Go CDP-over-pipe transport** (kernel-independent — chosen over narrowing video to kernel 6.7+, or a netns that needs privileges Fly may not grant). Reverses R5/F-01's "keep port 9223."

## Revision R5 — Round-3 Grill Dispositions (what changed R4 → R5)

The round-3 grill (`live-browser-video-streaming-spec-review-round3.md`) returned **BLOCK** (1 CRITICAL, 5 MAJOR, 4 MINOR, 1 OBSERVATION) — code-verified findings, several of them factual errors about the system the spec modifies. R5 resolves all; two carried **operator decisions (2026-07-16)**.

| Finding | Sev | R5 disposition |
|---|---|---|
| **F-01** CDP-token transport infeasible (chromedp v0.15.1 has no `--remote-debugging-pipe`; ephemeral `--remote-debugging-port=0` breaks the Landlock connect allow-list AND the ADR-043 coordinator, both keyed on fixed port 9223) | CRIT | **EC-3 mechanism REPLACED (operator: "keep 9223, isolate access").** The fixed CDP port **9223 is KEPT** — chromedp's ws-URL allocator, the ADR-043 coordinator (`sharedChromeCDPURL()`/ownership marker/`checkDebugPortAvailable`), and the Landlock connect allow-list (`sandbox_apply.go:419`) are all **unchanged**. This **retracts the R3/R4 "replace the fixed port" plan** (and resolves F-03). Confidentiality is closed by **process isolation, not port-hiding**: agent tool processes are Landlock-sandboxed **without** 9223 in their connect allow-list, so agent-driven co-tenant code cannot dial CDP; the ingest token is never in `/json` (delivered via `Page.addScriptToEvaluateOnNewDocument`). **EC-3 becomes an EMPIRICAL pre-build test** (not "by design"): prove a sandboxed agent process can neither dial 9223 nor read the token. Residual (non-sandboxed pod processes) documented; escalation to netns / a custom CDP-over-pipe transport only if zero-trust loopback is later required. FR-013, EC-3, SC-017, Test 30, `browserDebugPort` row rewritten. **(R6/CRIT-001: superseded — port removed; CDP-over-pipe transport.)** |
| **F-02** `browser_screencast` mis-labeled "dead / no runtime path" | MAJ | **Framing corrected; decision unchanged (A2-only — operator reaffirmed).** `browser_screencast` is the **current, sole live-view transport** (emitted `browser_ws.go:546`, consumed `browserLiveWs.ts:147`), NOT dead. Its removal is the **cutover of the only live-view wire path** — re-rated **LOW → MEDIUM**, tied to the SPA cutover. Explicit cost: non-video-capable installs (headless-shell / non-Linux / no-Xvfb) **lose today's working JPEG live view** and land on the unavailable state — the true M-1 cost, not a footnote. Regression note added: removal must not precede the video path being reachable on video-capable installs. |
| **F-03** fixed-port removal breaks ADR-043 coordinator | MAJ | **Resolved by F-01 (keep 9223).** The coordinator, `sharedChromeCDPURL()`, the ownership marker, and `checkDebugPortAvailable` are **unchanged**. **(R6/CRIT-001: superseded — the port IS now removed after all; the coordinator is reworked to marker-file-only ownership, `sharedChromeCDPURL()`/`checkDebugPortAvailable` retired, as a same-wave dependency.)** |
| **F-04** 30 fps measured on 16 GB/8-core, not the deployment min-spec | MAJ | EC-1 "PASS" **scoped to the 8-core CI box**; the SwiftShader software encode is host-dependent. **SC-016 min-spec becomes a first-wave measurement gate**; EC-1 is **re-run at the intended min video-capable spec** before it is asserted to clear for the shipping config. Below-min-spec installs classify not-video-capable. |
| **F-05** EC-4 deferred yet shapes codec priority + ship viability | MAJ | Operator deferred iPad. R5 **sequences the codec-dependent surfaces last** (contract `video_caps`, encoder config, DS-1) and **gates them on EC-4**; the rework cost if EC-4 inverts H.264↔VP8 is stated. Build proceeds; EC-4 is the pre-release ship gate. |
| **F-06** ingest-token single-active-holder race (no discriminator between reconnect and concurrent duplicate) | MAJ | **Discriminator defined:** a monotonic **connection epoch** minted by the gateway per ingest connect. A reconnect carries a **strictly higher** epoch → supersedes the prior holder; a **lower-or-equal** epoch presented while a holder is live → rejected (replay). The exact two-live-holders decision is added to DS-4 and Test 29. **(R6/CRIT-002: superseded — epoch dropped; drop → re-mint + relaunch.)** |
| **F-07** SC placeholders (SC-001a/002/016) unfilled despite the "before /taskify" gate | MIN | Gate 0 measured only fps. SC-001a (cold-start) and SC-002 (glass-to-glass) are **re-scoped as named first-wave measurement tasks**; SC-016 min-spec is a **first-wave gate**. Handoff/AW-4 updated — these are explicit measurement tasks, not silent placeholders. |
| **F-08** installer dual-download understated | MIN | FR-009/US-8/`EnsureChromium` row spell out the **dual-download** design: two CfT download IDs (`chrome` default + `chrome-headless-shell` fallback), two binary-name resolvers, two layouts, per-build integrity, classification; dataset row for both-cached. |
| **F-09** R4 encoder-page rename not fully propagated | MIN | Global pass: "capture page" → **"encoder page"**; every `getDisplayMedia` reference scoped to **audio-only** (Integration Boundaries, US-9/AC-3, the "agent page cannot capture" BDD). |
| **F-10** FR-019 alert thresholds unenforced | MIN | SC-012 definition-of-done extended: alert thresholds + one-line runbook present and reviewed at metrics-land. |
| **F-11** mechanism-(b) round-trip under-justified | OBS | One-line accepted-cost rationale added (pure-Go constraint + isolation; implementers must not "optimize" the loopback hop away and break isolation). |

**Operator decisions (2026-07-16):** (1) F-01 → **keep port 9223, close confidentiality by process isolation, EC-3 empirical** **(R6/CRIT-001: superseded — port removed; CDP-over-pipe transport.)**; (2) F-02 → **stay A2-only** (non-video installs lose today's JPEG live view — accepted, now with the corrected framing).

## Revision R3 — Round-2 Grill Dispositions (what changed R2 → R3)

The round-2 review (`live-browser-video-streaming-spec-review-round2.md`) returned **BLOCK** (3 CRITICAL, 11 MAJOR, 5 MINOR, 4 OBSERVATION). R3 resolves every finding **without introducing an A1 tier** (operator decision: "Stay A2-only, revise R3"). Where a finding's reviewer-suggested fix was "add A1 as the fallback," R3 instead **documents the A2-only scope decision and names its cost honestly** — it does not silently drop the finding.

| Finding | Sev | R3 disposition |
|---|---|---|
| **C-1** fps gate sequenced *after* the architecture it justifies | CRIT | **Re-sequenced.** Test 23 (integrated fps) becomes **Gate 0 / EC-1**, a standalone throwaway-CI proof that gates the whole epic — it runs **before** the installer-default flip (US-8/FR-009) and the `managedExecAllocatorOpts` headful switch. Named fail branch (A2-only): **`fps < 24 ⇒ do NOT ship A2; re-open ADR-044`** — there is no A1 to fall to; the honest consequence of A2-only is that the feature does not ship in this form until the ADR is revisited. §Gate 0, §Validation. |
| **C-2** capture isolation may be infeasible under a process-global auto-accept flag | CRIT | **Mechanism named + proven pre-build.** FR-016 no longer says "isolated context" vaguely: the process-global `--use-fake-ui-for-media-stream` is **FORBIDDEN**; isolation uses **CDP `Browser.grantPermissions({origin, permissions:['displayCapture']})` scoped to the capture-page origin** + source auto-selection bound to the per-stream key. **The capture mechanism is a Gate-0 output, not an assumption:** Gate 0 measures BOTH getDisplayMedia-on-headful AND `Page.startScreencast`-on-headful→encoder, and selects the one that passes fps **and** proves an agent-navigated page (different origin) is denied. **(R4 update: Gate 0 ran — `Page.startScreencast` (b) selected; `getDisplayMedia` (a) rejected as unreliable on bare Xvfb; EC-2 now structural, not a scoped-consent bet. See §Revision R4.)** Named escalation: if neither is both smooth and isolable, **re-open ADR-044** (A2-only ⇒ no A1 escape). §Gate 0 / EC-2, FR-001, FR-016, AW-6 retired. |
| **C-3** CDP-port token confidentiality re-asserted but unclosed; fixed port confirmed in code | CRIT | **Closed in-spec.** The fixed `remote-debugging-port` (9223) is replaced: **CDP over an inherited pipe (`--remote-debugging-pipe`, zero TCP surface) is the target**; ephemeral loopback port (`--remote-debugging-port=0`, read from `DevToolsActivePort`) is the fallback with the residual documented. The ingest token is pushed into the page via `Page.addScriptToEvaluateOnNewDocument` (never in `/json`). **The token-unrecoverable-from-CDP proof is a Gate-0 exit criterion (EC-3)** and gets the missing test (Test 30). §Gate 0 / EC-3, FR-013, AW-5 retired. **(R5/F-01 then R6/CRIT-001: superseded — the port is removed entirely; CDP-over-pipe transport, no TCP surface.)** |
| **M-1** A1 abandoned as the degradation tier | MAJ | **Deliberate A2-only decision, now documented with its cost** (not reversed). Non-A2 installs (no Xvfb / macOS / Windows / Termux / minimal) get the **unavailable state**, not A1. Rationale recorded: a single capture pipeline to maintain; A1's "sometimes-smooth, janky-on-video" UX would be an inconsistent experience the operator declined; the honest unavailable state is accepted as the degradation. **Cost flagged:** a proven-lighter experience is withheld from those installs — this is a scope choice, not a technical necessity. §Overview (A2-only rationale), Non-Behaviors. |
| **M-2** iPad decode gate un-run | MAJ | **Restored as a named exit criterion (Gate 0 / EC-4).** S-2 (`VideoDecoder.isConfigSupported` for H.264-main AND VP8) runs on the operator's actual iPad **before** the epic proceeds. If neither decodes on the primary device ⇒ NFR-1 fails ⇒ re-open ADR-044. No longer an AW footnote. |
| **M-3** non-Linux coverage undefined | MAJ | **Explicit platform matrix** (§Platform Matrix): Linux server = video-capable; macOS / Windows / Termux / no-Xvfb = unavailable state (A2-only ⇒ no A1). GOOS/build-tag guards for the two sidecars (FR-021/022). |
| **M-4** fragmentation has no wire representation | MAJ | **Fragmentation DROPPED (reviewer option a).** The ingest single-message bound is sized to the max plausible keyframe (≥ 2 MB, configurable); an over-bound chunk is **rejected + triggers a bitrate/resolution step-down**, never fragmented. Kills the reassembly-DoS surface and the undefined wire fields. FR-014 rewritten; Test 10 renamed; Test 13 "fragment index" removed; DS-2 updated; Edge case rewritten. |
| **M-5** single-use token kills the stream on a transient ingest drop | MAJ | **Ingest-leg reconnect defined.** Token is **stream-lifecycle-scoped with a single active holder**, not single-use-per-connection: the encoder page may reconnect with the same token while the stream is alive (superseding the prior holder); the token is invalidated when the stream ends. Defeats replay (dead stream ⇒ dead token; concurrent duplicate ⇒ rejected) while allowing reconnect. New Edge case + Test 29; DS-4 updated; US-9/AC-2 + FR-013 rewritten. **(R6/CRIT-002: superseded — epoch dropped; drop → re-mint + relaunch.)** |
| **M-6** viewer-attach-authz has no dedicated test | MAJ | **New Test 27** (`TestViewerAttach_Unauthorized_RejectedBeforeGOPReplay`) + **SC-015** + a BDD scenario. FR-015 re-mapped off Test 1. |
| **M-7** observability untested | MAJ | **New Test 28** (`TestObservability_MetricsEmitted`) — each counter/gauge registered and increments under load. FR-019 re-mapped off Test 12; SC-012 kept and now backed. |
| **M-8** equivalence bar unmeasurable | MAJ | **Corpus enumerated, tolerance defined, excluded-field list complete** (§Equivalence Corpus). Test 16 made concrete: fixed 12-URL fixture; SSIM ≥ 0.95 on non-excluded regions; normalized-DOM equality; enumerated excluded fields. FR-009/SC-007 updated. |
| **M-9** footprint/min-spec ungated; Go budget arithmetic unshown | MAJ | **New SC-016** — a total-footprint acceptance number + documented min video-capable spec as a **ship gate**. The Go < 10 MB budget arithmetic (GOP cache + per-viewer send queues, no reassembly buffer) is shown in §Go Memory Budget. |
| **M-10** no operability escape hatch; JPEG `browser_screencast` on the wire | MAJ | **The currently-live JPEG `browser_screencast` message REMOVED** (v0.3 = no back-compat) — NOT dead: it is today's **sole live-view transport**, so its removal is the cutover of the only live-view path and withdraws working JPEG live view from non-video-capable installs, which land on the unavailable state (F-02, MEDIUM risk). The A2-only operability posture is documented: kill-switch → unavailable state + release rollback is the only lever; **there is no live-view escape hatch under A2-only, and this is an accepted, flagged cost.** FR-020 + m-4 cover in-flight teardown. |
| **M-11** audio-sidecar lifecycle inconsistent/untested | MAJ | **Stable socket path** (`PULSE_SERVER` fixed across restarts) so a daemon restart reuses the same path; **Chrome never blocks on PulseAudio** (best-effort; resolves the AC-1/AC-2 contradiction); the "resumes without restarting Chrome" claim is now **gated on Test 31** (re-enumeration after mid-run restart) — if Chrome can't re-enumerate, audio resumes on the next Chrome launch and the AC is worded accordingly. US-11/FR-022 rewritten. |
| **m-1** US-8/AC-2 "browse on headless-shell boot" has no scenario | MIN | New "Then agent browsing continues" folded into the missing-stack + regression scenarios. |
| **m-2** audit demoted to an assumption | MIN | **New FR-024** (audit stream lifecycle + every ingest-auth rejection) + **Test 32**. |
| **m-3** `ts:u48` non-standard wire width | MIN | **Switched to `ts:u64`** everywhere (envelope, DS-2, ADR §6.3) — cross-language/codegen-safe. |
| **m-4** kill-switch on in-flight streams unspecified | MIN | FR-020: kill-switch **tears down active streams** to the unavailable state (not just new attaches); Edge case added. |
| **m-5** SC-002 span undefined | MIN | Scroll glass-to-glass span defined precisely (agent-tab compositor repaint → viewer canvas paint, common monotonic clock via injected timestamps), independent of the AW-4 numeric placeholder. |
| **O-1** `browser_stream_bitrate` reserved unused | OBS | **Deferred to v1.1** — removed from the R3 contract; lands with the ABR code. |
| **O-2** JPEG `browser_screencast` surface (mislabeled "dead") | OBS | Resolved by M-10 (removed); note (F-02) it was the **currently-live** JPEG transport, not dead — removal withdraws working live view from non-video-capable installs. |
| **O-3** unavailable-hint fingerprints the install | OBS | FR-007: **specific cause → operator logs**; end-user sees a **generic** "needs a video-capable browser" string. |
| **O-4** metrics have no thresholds/runbook | OBS | FR-019: alert thresholds + a one-line runbook pointer specified when the metrics land. |

**KEPT from R2 (still valid):** authenticated loopback ingest leg (FR-012); binary WS transport (FR-017); GOP aggregate memory ceiling (FR-003); unavailable-state degradation / no JPEG (FR-006/007); cold-vs-warm first-paint + bring-up/liveness timeout (FR-018); single-encode-per-source v1; ABR deferred to v1.1; observability (FR-019); kill-switch (FR-020); behavioral-equivalence bar (FR-009); the Xvfb (US-10) and PulseAudio (US-11) sidecar user stories; decoupled audio (FR-023); corrected H.264-main codec policy (FR-006). The R2 A2-pivot dispositions (P-1..P-7) and the R1 grill dispositions are retained as history at the end.

## Constraint alignment (v0.3 fresh-build)

v0.3 is a **fresh build with no back-compat obligation** (CLAUDE.md Release Strategy). R3 uses that latitude twice: (1) it **removes** the currently-live JPEG `browser_screencast` wire message rather than retaining it "for back-compat" (M-10/O-2) — that message is today's sole live-view transport (F-02), so its removal withdraws working JPEG live view from non-video-capable installs (they land on the unavailable state), and is a cutover, not the retirement of a dead surface; (2) it does **not** add an A1 compatibility tier for old/limited installs (M-1) — those installs get the honest unavailable state. Both are consistent with the no-back-compat posture and the operator's A2-only decision.

## Overview

Replace the live browser panel's CDP JPEG-per-frame screencast with a **WebCodecs relay**. The managed browser runs **headful on a virtual display**; a capture surface inside it encodes the agent's tab to real video and audio; the gateway relays the encoded chunks as binary over the existing WebSocket; the SPA decodes with `VideoDecoder`/`AudioDecoder` onto a canvas + audio element. **The Go binary never touches a codec** — all codec work is in the browser (Constraint #2). Go *orchestrates* two supervised sidecars (Xvfb, PulseAudio) — it does not implement them.

**A2 runtime (the shipped video-capable stack):**
- **Full Chrome-for-Testing**, run **headful** (`--window-size`, not `--headless`) under `dbus-run-session`, `DISPLAY` pointed at the Xvfb sidecar. Default for video-capable installs (**after Gate 0 passes**).
- **Xvfb sidecar** — supervised child process (Go `exec`, `DISPLAY=:N`), lifecycle like Signal's `signal-cli`. Provides the framebuffer that makes capture work at framerate.
- **PulseAudio sidecar** — supervised daemon + `module-null-sink` + `module-remap-source`; tab audio routes to the sink, captured via `getUserMedia` on the monitor. Go loads the modules over the native protocol (NoiseTorch pattern), no `pactl` subprocess. **Best-effort — Chrome never blocks on it.**

**Capture mechanism — RESOLVED by Gate 0 to (b) (2026-07-16 on `ci-omnipus`; supersedes the two-candidate framing).** Both candidates were run on headful Chromium/Xvfb:
- **(a) `getDisplayMedia` on headful Chrome — REJECTED.** Empirically unreliable on a bare Xvfb (no window-manager / xdg-desktop-portal / PipeWire): **0-for-3** — `NotReadableError` (tab-capture), renderer **crash / page teardown** (entire-screen, persists with `--disable-gpu` so not a GPU fault), or infinite **hang** (no matching source flag). Not a viable path here; no production vendor relies on it either.
- **(b) `Page.startScreencast` on headful Chrome → WebCodecs `VideoEncoder` — SELECTED.** Measured **30 fps** (reproduced ×5, incl. software `--disable-gpu`); the WebCodecs encoder keeps up (**30.1 fps VP8, zero drops**). Isolation-safe by construction (CDP-driven; an agent page cannot invoke it; no media flag). **This is the shipped mechanism.**

Concretely: the gateway drives `Page.startScreencast` on the agent tab and pushes each JPEG frame over the authenticated loopback to a controlled **encoder page** in the same Chrome (`createImageBitmap`→`VideoFrame`→`VideoEncoder`); the encoder page returns encoded chunks. The Go binary only shuffles bytes — **pure-Go preserved** (Constraint #2). Added cost: one loopback JPEG hop (~2.4 MB/s @ 30 fps 720p on localhost; `createImageBitmap` ~2–5 ms/frame). The only surviving media-consent surface is the encoder page's **audio** `getUserMedia` on the PulseAudio monitor (still CDP origin-scoped). **Accepted cost (F-11):** the Chrome→gateway→encoder-page→gateway loopback round-trip is a deliberate, justified hop — required by the pure-Go constraint (Constraint #2 — Go must not touch a codec) together with capture isolation (the encoder page is a controlled, origin-scoped surface) — so implementers MUST NOT "optimize" it away, as collapsing the hop re-introduces the pure-Go and isolation violations. See ADR-044 §6.0.2.

**Degradation (Constraint #4, A2-only, no JPEG, no A1):** where the full stack isn't present (no full Chrome / no Xvfb / no PulseAudio / non-Linux / a client without `VideoDecoder`) the panel shows an explicit **"Live view needs a video-capable browser"** state — never blank, never JPEG, never silent, and (per the A2-only decision) **never a lighter A1 fallback**. This withholds a proven-lighter experience from those installs; recorded as an accepted operator scope cost (M-1).

**Audio:** proven (S-3) and **decoupled** from video (rides `getUserMedia` on the sink monitor, not the video capture). Sequenced after video in implementation, but a real spec'd capability with its own sidecar — not a blocked phase.

**v1 simplifications:** single active `VideoEncoder` per source; **no ABR** (fixed bitrate + keyframe-on-stall); ABR + its `browser_stream_bitrate` control message are deferred **to v1.1** (contract entry lands with the code — O-1).

**Out of scope (this spec):** WebRTC/Pion transport; ABR feedback loop; a second concurrent encoder for disjoint-codec viewers; GPU/VAAPI acceleration (SwiftShader software rendering is the v1 baseline); an **A1 headless-screencast tier** (explicitly not built — M-1); any change to take-the-wheel input, annotate, or the tab model.

---

## Platform Matrix (M-3)

Video capability is **Linux-only** because Xvfb (X11) and PulseAudio are Linux-only; there is **no A1 tier** to cover the others (A2-only decision, M-1).

| Platform | Live view | Rationale |
|---|---|---|
| Linux x86_64 / arm64 server (Xvfb + PulseAudio present) | **Video-capable** | Full stack runs; the target deployment. |
| Linux, no Xvfb / minimal / Termux | Unavailable state | Sidecars can't run; agent browsing still works (headless-shell). |
| macOS self-host | Unavailable state | Xvfb/PulseAudio are Linux-only; no A1. Agent browsing works. |
| Windows self-host | Unavailable state | Same. Agent browsing works. |

- The two sidecar packages MUST carry **GOOS/build-tag guards** (Linux-only compilation of the spawn/supervise path; a no-op stub elsewhere that classifies the install not-video-capable) — FR-021/022.
- On every non-video-capable platform, agent browsing MUST continue unchanged (headless-shell or full Chrome headless), and live view MUST show the unavailable state (US-5, US-10/AC-3).

---

## Existing Codebase Context

### Symbols Involved
| Symbol | Role | Context |
|--------|------|---------|
| `managedExecAllocatorOpts` (`pkg/tools/browser/exec_resolver.go:31`) | modify | **Shared by every managed launch.** For video-capable installs: **headful** (`--window-size`, drop `--headless`), wrap in `dbus-run-session`, set `DISPLAY=:N`. Capture is mechanism (b) gateway-driven `Page.startScreencast` (**no capture flags**). CDP moves from the fixed `remote-debugging-port` to **`--remote-debugging-pipe`** (C-3/CRIT-001) — no TCP surface; wired to the new pure-Go pipe transport. Highest-risk edit — **built only after Gate 0 passes**. |
| **Xvfb sidecar** (new, e.g. `pkg/tools/browser/display_linux.go`) | new | Supervised `Xvfb :N -screen 0 WxHx24`; health/restart; publishes `DISPLAY`. Linux-only (build-tag). Lifecycle mirrors the channel sidecar pattern (Signal `signal-cli`). |
| **PulseAudio sidecar** (new, e.g. `pkg/tools/browser/audiosink_linux.go`) | new | Supervised `pulseaudio -D --exit-idle-time=-1 --disable-shm`; **stable socket path**; loads `module-null-sink` + `module-remap-source` via a pure-Go PA native-protocol client (`LoadModule`); publishes `PULSE_SERVER`; restart on crash; **never blocks the Chrome launch**. Linux-only (build-tag). |
| `EnsureChromium` / `cftDownloadID` (`pkg/tools/browser/installer.go:23,52`) | modify | **Dual-download (F-08):** two CfT download IDs — `chrome` (full build, default for video-capable) + `chrome-headless-shell` (fallback) — each with its **own binary-name resolver, on-disk layout, and per-build integrity verification**; classification logic picks between them (full + Xvfb + PulseAudio → video-capable; else headless-shell → unavailable state). `cftDownloadID` is today a **single const** driving download key + zip path + extraction subdir + binary name, so it MUST become **build-aware** (one per build). Full-Chrome default flips **only after Gate 0**. `switch runtime.GOOS` already present — extend with the platform matrix. |
| `LiveViewRegistry` (`pkg/tools/browser/live.go:152`) | extend | Stream relay + GOP cache attach here; `Attach`/`Detach`/`Input`/control preserved; **viewer-attach authz gates before any GOP replay (FR-015, Test 27).** |
| `LiveView` (`pkg/tools/browser/live.go:380`), `lastFrame` (`:406`) | extend | GOP cache generalizes the JPEG-era `lastFrame` piggyback. |
| `LiveFrame` (`pkg/tools/browser/live.go:44`) | parallel | Encoded-chunk analog for the video path. |
| `runAckWorker` / `ackCh` (`live.go`) | bypass-for-video | CDP screencast ack loop; unused when a stream is video. |
| `browserWSConn` + `sendFrame`/`sendCritical` (`pkg/gateway/browser_ws.go:54,70,97`) | extend | Per-viewer WS; **write pump emits only `TextMessage` today (`:374`)** — FR-017 adds a binary opcode. `sendCh` is a bare `chan []byte` — becomes opcode-tagged. |
| `browserWSMaxMessageBytes = 64*1024` (`browser_ws.go:49`) | context | Viewer-inbound cap; the **new ingest endpoint** gets its own keyframe-sized bound (FR-014, ≥ 2 MB). |
| `AuthFrame` first-frame token auth (`browser_ws.go:270-339`) | reference | Viewer WS auth model; the encoder page holds no user token, so ingest uses a distinct capability-token model (FR-013). |
| `browserDebugPort` (`= "9223"`, fixed) → **REMOVED (C-3/CRIT-001)** | **remove** | **The fixed CDP TCP port is eliminated.** CDP moves to `--remote-debugging-pipe` (inherited fd 3/4, no TCP surface) via a new **pure-Go CDP-over-pipe transport** (chromedp lacks it). Ripples (same wave, **significant** — round-5 CRIT-001): the ADR-043 coordinator is reworked — **single-launch atomicity via an `O_EXCL`/`flock` lockfile** (the removed `net.Listen(":9223")` bind at `coordinator.go:757`, not the marker, was the atomic guard), **CDP sharing via in-process chromedp child contexts of the launcher `rootCtx`** (not `RemoteAllocator(ws://9223)`; the pipe is private to the launcher, so cross-OS-process consumers route through the gateway API). The Landlock 9223 entries — **both** `BindPortRules` (`sandbox_apply.go:388`) **and** `ConnectPortRules` (`sandbox_apply.go:419`) — plus `checkDebugPortAvailable` are **removed** (attack-surface reduction). |
| `generated.BrowserScreencastFrame` (`browser_ws.go:546`) | **REMOVE (M-10) — MEDIUM risk (F-02)** | **LIVE** wire frame, NOT dead: today's **sole live-view transport** (emitted here, consumed `browserLiveWs.ts:147`). Removal is the **cutover of the only live-view path** — non-video-capable installs lose working JPEG live view → unavailable state. Remove **only after** the video path is reachable on video-capable installs. |
| `browserLiveWs.ts` consumer (`src/lib/browserLiveWs.ts:145,147`) | modify | Parses `event.data as string` today; gains `binaryType='arraybuffer'` + branch on chunk vs JSON; adds audio-chunk path. |
| `BrowserLiveView.tsx` (`imgRef`) | modify | `<img>` → `<canvas>` fed by `VideoDecoder`; `<audio>`/AudioContext fed by `AudioDecoder`; adds the unavailable state. |
| `contracts/asyncapi.yaml` browser channel (`:140-160`) | extend | New viewer messages + a new **ingest** channel (FR-012); **remove `browser_screencast`** (M-10). |

### Impact Assessment
| Symbol Modified | Risk | Direct Dependents (d=1) | Indirect (d=2) |
|----------------|------|-------------------------|----------------|
| `managedExecAllocatorOpts` (headful + display + capture; CDP via --remote-debugging-pipe — **carries ALL browser CDP traffic**, MAJ-001) | **CRITICAL** | Every managed-Chrome launch (all agent browsing — navigate/drive/screenshot — runs on the new transport, not only live view) | Whole `pkg/tools/browser`; agent runs. Guards: (a) behavioral-equivalence golden vs the headless-shell baseline (FR-009, concrete corpus §Equivalence Corpus), (b) **security-regression** proof that agent-browsed pages can't capture (FR-016, Gate 0 / EC-2), (c) **CDP-transport browsing-equivalence** — every `browser.*` tool behaves identically over the pipe transport vs the old port; MUST pass before the swap ships (MAJ-001/round-5). **Built only after Gate 0.** |
| Xvfb sidecar (new, Linux-only) | **HIGH** | Managed-Chrome launch (needs `DISPLAY`) | Live view unavailable if it fails to start (US-10). New supervised process. |
| PulseAudio sidecar (new, Linux-only) | MEDIUM | Audio capture only | Audio silently absent if it fails; video unaffected (US-11, FR-011). Never blocks Chrome. |
| Ingest endpoint (new) | **HIGH (security)** | Gateway routing; encoder page | STRIDE-reviewed (§Integration); reject + reconnect tests (FR-013, Test 1/2/29/30). |
| CDP token confidentiality (CDP-over-pipe, no TCP surface) | **HIGH (security)** | agent sandbox connect-rules; token delivery | Co-tenant token theft closed by removing the TCP CDP surface entirely (--remote-debugging-pipe); kernel-independent (C-3/CRIT-001, EC-3, Test 30). |
| `browser_ws` write pump (Text→Text+Binary) | HIGH | Every viewer frame | SPA consumer; existing status/tabs frames must keep working (regression). |
| `installer.go` `cftDownloadID` (full-Chrome default) | HIGH (one-way-door) | Fresh installs | All platforms; footprint (+120 MB). Mitigated by detect-either-binary + unavailable-state degradation + **flip only after Gate 0 + min-spec ship gate (SC-016)**. |
| Remove `browser_screencast` (M-10) | **MEDIUM (F-02)** | Contract + SPA + Go | It is today's **sole live-view transport** (emitted `browser_ws.go:546`, consumed `browserLiveWs.ts:147`), NOT unconsumed — removal withdraws working JPEG live view from non-video-capable installs (they land on the unavailable state); remove **only after** the video path is reachable on video-capable installs; v0.3 no back-compat. |

### Relevant Execution Flows
| Flow | Relevance |
|------|-----------|
| Attach → screencast stream (ADR-038) | Replaced for video-capable clients; JPEG message removed. |
| Managed-Chrome launch (`coordinator` → `ExecAllocator` via `exec_resolver.go`) | Headful + display + capture introduced here (CDP via --remote-debugging-pipe, no TCP surface); depends on Xvfb sidecar being up; **built post-Gate-0**. |
| Channel sidecar lifecycle (Signal → `signal-cli`) | Pattern for the Xvfb + PulseAudio sidecars (spawn from Start, supervise, restart, teardown). |
| Take-the-wheel input (ADR-040/041) | Preserved unchanged. |
| Multi-viewer piggyback (`lastFrame`) | Generalized to GOP replay; **authz-gated before replay (FR-015).** |

## Available Reference Patterns
- **Channel sidecar pattern** (`pkg/channels/*`, Signal → `signal-cli`): the model for spawning/supervising the Xvfb and PulseAudio sidecars from Go. Do not re-invent process supervision.
- **NoiseTorch PulseAudio native-protocol client pattern**: `LoadModule` over the PA native protocol from Go (no `pactl` subprocess).
- No `docs/reference/` entry covers browser media. Internal reuse: `lastFrame` piggyback, drop-on-full queue, `AuthFrame` (auth reference).

---

## Gate 0 — Pre-Epic Proof (hard, gates the whole epic)

**STATUS (2026-07-16, §Revision R7): EC-2 isolation PASSED (structural) on `ci-omnipus`; the capture mechanism is decided (b). EC-1 fps measured 30 on the 8-core CI box but is NOT asserted PASS until a min-spec re-run (pre-`/taskify` gate). EC-3 CDP-token is OPEN — a pre-BUILD gate (the pure-Go pipe transport must be built + proven, CRIT-002). EC-4 (iPad) is DEFERRED (final ship gate).** The mechanism is chosen and EC-2 clears, so spec → `/taskify` may proceed — but the epic **MUST NOT reach the headful/installer build** until EC-3 (pipe transport) and the ADR-043 coordinator rework (CRIT-001) are in, and **MUST NOT be released** until EC-4 confirms the iPad decodes H.264-main. This section answers C-1 (sequencing), C-2 (isolation), C-3 (CDP token), M-2 (iPad decode); the exit-criteria table below records outcomes and open gates.

| # | Exit criterion | Method | Pass bar | Fail branch (A2-only) |
|---|---|---|---|---|
| **EC-1** ✅ **PASS (30 fps)** | Integrated capture fps | Headful full Chrome on Xvfb captures a playing full-motion video; measure **distinct** fps (not ffmpeg frame-duplication — S-5 caveat). **Result:** mechanism (b) CDP `Page.startScreencast` = **30 fps** (×5); mechanism (a) `getDisplayMedia` rejected (unreliable on bare Xvfb). WebCodecs VP8 encode kept up at 30.1 fps. **Caveat (F-04):** the 30 fps was measured on the 16 GB/8-core `ci-omnipus` box; SwiftShader software encode is host-dependent, so EC-1 MUST be **re-run at the min video-capable spec (SC-016) as a pre-`/taskify` gate (MAJ-001)** before it is asserted to clear the shipping config. | ≥ 24 distinct fps at panel size (sets SC-002/003/013 real numbers). | *(met)* `fps < 24 for both ⇒ do NOT ship A2; re-open ADR-044.` No A1 fallback. |
| **EC-2** ✅ **PASS (structural)** | Capture isolation | Mechanism (b) is CDP-only — an agent-navigated page has no API to start a screencast and no `getDisplayMedia` video grant exists, so the isolation is structural (not a scoped-consent bet). Only the encoder page's **audio** `getUserMedia` remains, CDP origin-scoped. | Agent-origin page cannot obtain a **video** stream by construction; audio consent origin-scoped. | *(met by choosing (b))* If a future mechanism reintroduced `getDisplayMedia` and couldn't isolate it ⇒ re-open ADR-044. |
| **EC-3** ⏳ **OPEN — pre-BUILD gate (CRIT-002)** | CDP-token confidentiality | **CDP over `--remote-debugging-pipe`** (inherited fd 3/4; **no TCP port, no `/json`, no HTTP surface**) via a new pure-Go CDP-over-pipe transport (chromedp lacks it); the ADR-043 coordinator is reworked (lockfile single-launch + in-process context-sharing, CRIT-001) and the Landlock 9223 `BindPortRules`+`ConnectPortRules` entries are removed. **EC-3 is NOT passed** (the R5 "PASS" was for the reversed keep-9223 mechanism): it clears only after the transport is **built** and Test 30 proves no TCP/HTTP surface + token-unrecoverable by any co-tenant on **any** kernel. | Token unrecoverable by any co-tenant/agent process on any supported kernel in 100% of attempts (measured once the transport exists). | If the pipe transport can't be built pure-Go ⇒ escalate (network-namespace the browser, or re-open ADR §6.6). |
| **EC-4** ⏳ **DEFERRED (ship gate)** | iPad decode (primary device, NFR-1) | Run `VideoDecoder.isConfigSupported`/actual decode for **H.264-main** (and VP8) on the operator's actual iPad Safari. **Operator deferred (2026-07-16): not testing iPad yet.** | At least one of {H.264-main, VP8} decodes on the iPad. | Neither decodes ⇒ NFR-1 fails ⇒ re-open ADR-044 (codec / transport revisit). **Must clear before release.** |

Gate 0 outputs, fed into `/taskify`: the **selected capture mechanism = (b) CDP `Page.startScreencast` → encoder page** (decided — R4), the **measured fps = 30** (→ SC-002/003/013), the **CDP transport = `--remote-debugging-pipe` (pure-Go pipe transport, no TCP surface)** (EC-3/CRIT-001), and — still pending EC-4 — the **iPad codec** (→ FR-006 policy, possibly inverting H.264-first↔VP8-first) and the **keyframe-size distribution** (→ GOP `N` + ceilings, §Go Memory Budget, measurable from the encoder-page output during the build). Because EC-4 is deferred (F-05), the codec-dependent surfaces (`video_caps`, encoder config, DS-1) are built **last** and gated on it; an H.264↔VP8 inversion then reworks only the codec-negotiation policy + `video_caps` ordering + DS-1, not the transport/relay.

---

## User Stories & Acceptance Criteria

### US-1 — Watch the agent's browser as smooth video (P0)
An operator sees the agent's tab as fluid video, including full-motion `<video>` playback.
**Why:** the reason the ADR exists. **Independent test:** attach to a session playing a video; observe fluid motion.
1. **Given** a video-capable viewer and a **warm** stream, **When** the viewer attaches, **Then** a decoded frame paints ≤ 1 s (warm — see US-3).
2. **Given** an attached stream, **When** the agent scrolls/animates, **Then** motion updates continuously (no per-step stall).
3. **Given** an attached stream on the headful+display runtime, **When** an in-page `<video>` plays, **Then** it renders at the **Gate-0-confirmed capture framerate** (≥ 24 fps floor) at panel size.

### US-2 — Keep take-the-wheel working over video (P0)
Drive still works; input lands correctly.
1. **Given** an attached stream with control held, **When** the viewer clicks, **Then** CDP `Input.dispatch` fires at the correct CSS pixel (unchanged path).
2. **Given** control held, **When** the viewer types, **Then** keystrokes reach the agent tab exactly as under the JPEG path.
3. **Given** the canvas replaced the `<img>`, **When** coordinates map, **Then** the viewport metadata used is unchanged.

### US-3 — Cold start and warm multi-viewer attach have separate, met budgets (P0)
1. **Given** no stream yet, **When** the first viewer attaches, **Then** capture bring-up completes and a frame paints within the **cold-start budget** (Gate-0-measured; placeholder ≤ 3 s).
2. **Given** a running stream with a cached GOP, **When** a second **authorized** viewer attaches, **Then** the relay replays from the last keyframe and it paints ≤ 1 s.
3. **Given** capture bring-up (or the display sidecar) that hangs, **When** the bring-up timeout expires, **Then** the viewer moves to the unavailable state (never an infinite spinner).
4. **Given** a slow viewer, **When** its queue fills, **Then** its chunks drop in isolation; source and other viewers are unaffected.

### US-4 — Hear in-page audio (P1, proven; requires the PulseAudio sidecar)
The operator hears tab audio, roughly A/V-synced.
**Why:** a raised gap, now proven (S-3). **Independent test:** attach on a page with audio; confirm audible, roughly-synced sound.
1. **Given** the PulseAudio sidecar is up and Opus is negotiated, **When** the tab plays audio, **Then** the viewer decodes/plays `browser_audio_chunk` (captured via `getUserMedia` on the sink monitor).
2. **Given** the PulseAudio sidecar is unavailable or Opus isn't negotiated, **When** a viewer attaches, **Then** video streams normally and audio is silently absent (no error, no block).
3. **Given** audio streaming, **When** measured, **Then** A/V skew ≤ 200 ms (target; final bound from Gate 0).

### US-5 — Honest unavailable state where video can't run (P0)
No blank/frozen panel, no silent failure, no JPEG, no A1.
1. **Given** a viewer whose `video_caps` intersect no offered codec, **When** it attaches, **Then** the panel shows a **generic** "Live view needs a video-capable browser" string and no stream starts.
2. **Given** an install lacking the video-capable stack (headless-shell / no Xvfb / non-Linux), **When** a viewer attaches, **Then** the same state shows; the **specific** cause is logged operator-side only (O-3), the end-user string stays generic.
3. **Given** the unavailable state, **When** shown, **Then** the close/other chrome controls remain operable.

### US-6 — Codec negotiated per viewer; single encode per source (P1)
1. **Given** a viewer advertising H.264, **When** the capture supports it, **Then** the stream is **H.264 main (`avc1.4D40…`)** (or the Gate-0-selected primary if EC-4 inverts the policy).
2. **Given** a running H.264 stream and a second viewer advertising only VP8, **When** it attaches, **Then** that viewer gets the **unavailable state** (v1 = single encode per source; no second concurrent encoder).
3. **Given** no codec intersects, **When** attach completes, **Then** US-5 applies (never JPEG, never A1).

### US-7 — Deploy anywhere the gateway works (P0)
1. **Given** only WSS reaches the gateway (UDP blocked), **When** a viewer attaches, **Then** the video stream works over `/api/v1/browser/ws`.
2. **Given** deployment, **When** it ships, **Then** no new **external** listener port, STUN/TURN, or certificate is required. (Ingest endpoint + Xvfb + PulseAudio are all loopback/local; the image gains those packages, the Go binary stays single.)

### US-8 — Full Chrome + sidecars are the default video-capable runtime; graceful where absent (P1)
Fresh **Linux** video-capable installs get full Chrome + Xvfb + PulseAudio; installs without them keep working with the unavailable state; agent browsing stays behaviorally equivalent.
**Why:** A2 requires the headful+display+audio stack. **Independent test:** provision a video-capable platform and a minimal one; confirm capable vs unavailable + no browse regression.
1. **Given** a fresh video-capable install (Gate 0 passed), **When** the browser is provisioned, **Then** the appropriate CfT build is downloaded via the **dual-download** installer (`chrome` default for video-capable, `chrome-headless-shell` fallback — each integrity-verified per build with its own binary-name resolver and on-disk layout), and (with Xvfb+PulseAudio present) the full-Chrome install is classified video-capable.
2. **Given** an install without full Chrome / Xvfb / PulseAudio / on non-Linux, **When** the gateway boots, **Then** **agents browse normally** (headless-shell or headless full Chrome) and live view shows the unavailable state.
3. **Given** the switch to headful full Chrome as the browsing runtime, **When** an agent runs the **fixed browsing corpus** (§Equivalence Corpus), **Then** behavior is **equivalent** to the headless-shell baseline on the defined bar (navigation succeeds, normalized DOM/tool outputs match, screenshots SSIM ≥ 0.95 on non-excluded regions) — **excluding** the enumerated excluded-field list.

### US-9 — Capture ingest authenticated + reconnectable; capture isolated from agent browsing (P0, security)
1. **Given** the ingest endpoint, **When** a connection presents no or a mis-scoped capability token, **Then** it is rejected before any chunk is relayed.
2. **Given** a valid per-stream token and a **transient ingest-WS drop**, **When** the drop is observed, **Then** the gateway **invalidates the token, re-mints a fresh one, and relaunches the (loopback, gateway-controlled) encoder page** — there is no same-token reconnect, so a second connection presenting the old token is rejected (dead token); the token is invalidated when the stream **ends** (CRIT-002).
3. **Given** the headful runtime with **audio** capture consent granted **only to the encoder-page origin** (CDP origin-scoped grant; no process-global fake-ui), **When** a page the agent autonomously browses calls `getUserMedia` (audio — video capture is CDP `Page.startScreencast`, which has no page-callable API), **Then** no media stream is granted to it.
4. **Given** capture target selection, **When** the capture binds, **Then** it binds by an unguessable per-stream key, never the human tab title.
5. **Given** the CDP transport, **When** a non-gateway loopback process or agent-browsed content probes it, **Then** it cannot recover the ingest token (CDP has no TCP/HTTP surface under --remote-debugging-pipe — EC-3/CRIT-001).

### US-10 — Virtual display sidecar lifecycle (P0)
The Xvfb sidecar is supervised; its absence degrades gracefully, never crashes agent browsing.
**Why:** headful capture depends on it. **Independent test:** kill Xvfb mid-run; observe restart + unavailable state during the gap; confirm agent browsing on the recovered display.
1. **Given** the gateway boots a Linux video-capable install, **When** the browser subsystem starts, **Then** the Xvfb sidecar is spawned, its `DISPLAY` wired into the managed-Chrome launch, and readiness confirmed before Chrome launches.
2. **Given** the Xvfb sidecar exits unexpectedly, **When** the supervisor notices, **Then** it restarts it (bounded backoff) and live view shows the unavailable state until the display is back.
3. **Given** Xvfb cannot start (missing binary / non-Linux), **When** the browser subsystem starts, **Then** the install is classified not-video-capable (US-5) and agent browsing still works (headless path), no crash.

### US-11 — Audio sink sidecar lifecycle (P1)
The PulseAudio sidecar is supervised, optional, and **never blocks the Chrome launch**; audio failure never blocks video.
**Independent test:** kill PulseAudio; observe video continues, audio drops; confirm restart re-enables audio (on the next stream or next Chrome launch per EC/Test 31).
1. **Given** a Linux video-capable install, **When** the browser subsystem starts, **Then** the PulseAudio sidecar is spawned on a **stable socket path** and `module-null-sink` + `module-remap-source` loaded (via the Go native-protocol client), `PULSE_SERVER` (the stable path) wired into the managed-Chrome launch. **Chrome launches regardless** of PulseAudio readiness (best-effort audio).
2. **Given** the PulseAudio sidecar is absent or fails, **When** a stream runs, **Then** video streams normally and audio is silently absent (US-4/AC-2).
3. **Given** PulseAudio restarts on the **same** socket path, **When** Chrome re-enumerates the device, **Then** audio resumes on the next stream **without restarting Chrome** *iff* Test 31 confirms re-enumeration; if Chrome cannot re-enumerate, audio resumes on the next Chrome launch (the AC is satisfied either way — no permanent audio loss).

### Edge Cases
- Capture surface **crashes** → relay marks the stream failed → viewers to unavailable state (not a frozen last frame).
- Capture / display bring-up **hangs** → timeout → unavailable state.
- **Transient ingest-WS drop** (not a crash) → capture reconnects with the same stream-lifecycle token → stream survives (US-9/AC-2, Test 29).
- Agent navigates / tab changes while streaming → capture rebinds; a keyframe is forced to re-sync.
- All viewers detach → encoder stops; GOP cache for that stream cleared.
- Aggregate cache pressure → LRU-evict the least-recently-viewed stream's cache under the total ceiling.
- Rapid reconnect flap → GOP replay each time; bounded goroutines/cache.
- Encoder can't keep up (SwiftShader/slow pod) → keyframe-on-stall; bounded queue (no ABR in v1).
- **Keyframe exceeds the ingest single-message bound** → **rejected + bitrate/resolution step-down** (NOT fragmented — M-4); the relay never assembles partial frames.
- SPA tab backgrounded → `VideoDecoder` may throttle; on refocus, force-keyframe recovery.
- Small panel (phone full-row) → capture dimensions follow the panel/window size; no fixed 1280×720.
- Audio present, video-only negotiated → audio suppressed at source, not sent-and-dropped.
- Xvfb dies mid-stream → stream fails → unavailable until restart (US-10/AC-2).
- **PulseAudio dies mid-stream** → audio drops, video continues; restart re-enables audio per US-11/AC-3.
- **Kill-switch flipped on** → all **new** attaches get the unavailable state **and active streams are torn down** to the unavailable state (m-4), without redeploy.

---

## Behavioral Contract
- When a video-capable viewer attaches to a warm stream, the system paints decoded video ≤ 1 s.
- When the first viewer attaches, the system brings up capture within the cold-start budget, or moves to the unavailable state on timeout.
- When the page changes visually (incl. full-motion video), the system updates the canvas at the Gate-0-confirmed capture framerate.
- When a viewer takes control, the system dispatches input via the unchanged CDP path.
- When a new **authorized** viewer joins a running stream, the system replays the cached GOP; an **unauthorized** attach is rejected before any replay.
- When a viewer is slow, the system drops that viewer's frames only.
- When the video-capable stack is absent (no full Chrome / Xvfb / codec / non-Linux) or the kill-switch is on, the system shows the unavailable state and starts no stream (and never JPEG, never A1).
- When a second viewer needs a codec the single active encoder isn't producing, the system shows that viewer the unavailable state (v1).
- When the PulseAudio sidecar is up and Opus is negotiated, the system streams Opus in sync; otherwise video streams silently; PulseAudio failure never blocks the Chrome launch or video.
- When the encoder page connects to ingest without a valid stream-scoped token, the system rejects it; a transient drop reconnects on the same token; a concurrent duplicate is rejected.
- When an agent-browsed page requests media capture, the system denies it (origin-scoped consent, no global flag).
- When a non-gateway process probes the CDP transport, it cannot recover the ingest token.
- When only WSS is reachable, the system streams over the existing WS with no new external port.
- When agents browse normally on the headful full-Chrome runtime, the system is behaviorally equivalent (defined bar, concrete corpus) to the headless-shell baseline.
- When a stream starts/stops or an ingest auth is rejected, the system writes an audit entry.

## Explicit Non-Behaviors
- Must **not** fall back to JPEG for live view — degradation is the unavailable state.
- Must **not** ship an A1 headless-screencast tier — non-A2 installs get the unavailable state (A2-only decision, M-1). This is a deliberate scope choice, with the withheld-lighter-experience cost accepted.
- Must **not** encode/transcode media in Go — all codec work stays in Chrome (Constraint #2); Go only *orchestrates* the Xvfb/PulseAudio sidecars.
- Must **not** grant media capture to agent-browsed pages — consent is CDP origin-scoped to the encoder page (audio; video capture has no page-callable API); the process-global `--use-fake-ui-for-media-stream` is forbidden (C-2/P-6).
- Must **not** accept ingest chunks without a valid stream-scoped capability token, or carry that token in a URL, or leave it recoverable from the CDP transport (CRIT-001/C-3).
- Must **not** open any new **external** listener port or require UDP/STUN/TURN (NFR-2).
- Must **not** change the take-the-wheel input path, annotate flow, or tab model.
- Must **not** run a second concurrent encoder per source in v1 (single encode; disjoint viewers → unavailable state).
- Must **not** implement ABR in v1 (fixed bitrate + keyframe-on-stall); `browser_stream_bitrate` is not in the v1 contract.
- Must **not** fragment ingest chunks — an over-bound keyframe is rejected and the encoder steps down (M-4).
- Must **not** block or degrade video (or the Chrome launch) on audio-sidecar failure.
- Must **not** send media a viewer did not negotiate.
- Must **not** crash or degrade agent browsing when Xvfb/PulseAudio are missing or on non-Linux — those installs are simply not-video-capable (US-10/AC-3).
- Must **not** retain the currently-live JPEG `browser_screencast` wire message (removed in v0.3, M-10) — it is today's sole live-view transport (F-02), so its removal withdraws working JPEG live view from non-video-capable installs, which land on the unavailable state.

## Integration Boundaries

**Xvfb sidecar** — local process (Linux-only).
- In: launch args (`:N`, screen geom); health checks.
- Out: an X display at `DISPLAY=:N`.
- Failure: won't start → install not-video-capable (US-5), agent browsing unaffected; dies mid-run → restart + unavailable state.
- Dev: real Xvfb; the sidecar supervisor unit-tested with a stub display process.

**PulseAudio sidecar** — local process + native-protocol socket (Linux-only).
- In: daemon launch on a **stable socket path**; `LoadModule` calls (null-sink, remap-source) via a pure-Go PA client; `PULSE_SERVER` handed to Chrome.
- Out: a captureable `VirtualMic` source (the remapped sink monitor).
- Failure: won't start / dies → audio silently absent; video + Chrome launch unaffected. Restart reuses the same socket path.
- Dev: real PulseAudio; supervisor unit-tested with a stub; module-load + restart-re-enumeration integration-tested (Test 31).

**Managed Chrome (headful capture host)** — local + loopback.
- In: headful launch (display, dbus session, Gate-0-selected capture mechanism, origin-scoped consent, `PULSE_SERVER`, **CDP over --remote-debugging-pipe (no TCP surface)**); encoder-page load; per-stream capability token (out-of-band via CDP `addScriptToEvaluateOnNewDocument`).
- Out: `browser_ingest_init` + binary `browser_ingest_chunk` (video + audio) over the authenticated loopback ingest WS.
- Failure: capture/display fails/crashes/hangs → stream failed/timeout → unavailable state.
- STRIDE: token-gated ingest; origin-scoped capture (no leak to agent pages); CDP token confidentiality (EC-3); unguessable target key. Dev: real headful Chrome on Xvfb (CI); scripted fake capture-WS client for relay unit tests.

**SPA viewer (`browserLiveWs`+`BrowserLiveView`)** — existing authed WSS.
- In: `browser_stream_init`, binary `browser_video_chunk`, binary `browser_audio_chunk`, `browser_status`. (No JPEG frame — removed.)
- Out: `browser_attach` (+`video_caps`, `audio_caps`), input/control (unchanged).
- Failure: decode error → force-keyframe or unavailable; WS drop → existing reconnect.
- Dev: real SPA vs gateway; `VideoDecoder` stubbed unsupported for unavailable-state tests; `binaryType='arraybuffer'`.

**Chrome-for-Testing installer** — external HTTPS.
- In: version manifest; full-Chrome build (default for Linux video-capable platforms, **post-Gate-0**).
- Out: none. Integrity: hash/signature-verify before use.
- Failure: no full build for platform / non-Linux → headless-shell + unavailable state.
- Dev: real manifest; fixture manifest for unit tests.

---

## Equivalence Corpus (M-8) — concrete, reproducible

The behavioral-equivalence bar (FR-009 / US-8/AC-3 / SC-007 / Test 16) is the sole guard on the CRITICAL-risk headful switch, so it is defined concretely.

**Corpus (fixed 12-URL fixture, embedded as test data, served from the gateway's loopback preview or a local fixtures dir):**
1. Static HTML document (text + headings).
2. CSS-heavy layout (flex/grid, media queries).
3. JS SPA (a React counter that mutates the DOM on click).
4. Form with text/select/checkbox inputs.
5. Canvas 2D render (deterministic shapes).
6. WebGL triangle (SwiftShader path).
7. HTML5 `<video>` page (poster + first frame).
8. Long scrollable article (scroll-position assertion).
9. Data table (structured DOM).
10. Image gallery (multiple `<img>`).
11. Web-font page (custom `@font-face`).
12. Autoplay-blocked media page (autoplay policy differs headful↔headless).

**Assertions per URL:**
- **Navigation:** `load` event fires; final URL matches (redirects followed identically).
- **DOM/tool output:** normalized `outerHTML` (excluded attributes stripped) and the `read_page`-equivalent text extraction are byte-equal to the headless-shell baseline.
- **Screenshot:** **SSIM ≥ 0.95** on the non-excluded regions (metric = structural similarity; regions listed in "excluded" below are masked before comparison).

**Excluded fields/regions (expected to legitimately differ headful full Chrome ↔ headless-shell):**
- User-Agent (`HeadlessChrome` ↔ `Chrome`), `navigator.webdriver`, `navigator.userAgentData`.
- `window.devicePixelRatio` and any viewport-derived layout metric.
- Sub-pixel font anti-aliasing (SwiftShader software rendering) — masked in screenshots.
- Autoplay-gated media playback state (URL 12) — presence of playback, not the poster.
- Timing fields (`performance.now`, resource timing), GPU renderer string.

Test 16 loads the corpus against both runtimes, applies the masks, and asserts the bar. This makes SC-007 pass/fail and reproducible.

---

## Go Memory Budget (M-9) — the Go-process < 10 MB derivation

NFR-3 bounds the **Go-process** steady-state overhead to < 10 MB (sidecar/Chrome RAM is image-level and covered by the min-spec ship gate SC-016). With fragmentation dropped (M-4) there is **no reassembly buffer**. Worst-case arithmetic (concrete numbers finalized from the Gate-0 keyframe-size distribution):

| Component | Sizing | Worst-case |
|---|---|---|
| Per-stream GOP cache | 1 keyframe (≤ 150 KB) + ≤ 30 deltas (≤ 4 KB) | ≈ 270 KB / stream |
| Aggregate GOP ceiling | ≤ 8 concurrent live streams | ≈ 2.2 MB |
| Per-viewer send queue | bounded depth 4 chunks; worst 1 keyframe + 3 deltas ≈ 162 KB | — |
| Aggregate send queues | ≤ 16 concurrent viewers × 162 KB | ≈ 2.6 MB |
| Reassembly buffer | **none** (fragmentation dropped) | 0 |
| Relay bookkeeping / envelopes | small structs, per-stream/per-viewer | < 0.5 MB |
| **Total (Go process)** | | **≈ 5.3 MB < 10 MB** ✓ |

Caps (max concurrent streams = 8, max viewers = 16, GOP `N` = 30, queue depth = 4) are enforced constants; exceeding them drops the least-recently-viewed cache (LRU) and drops slow-viewer frames. If Gate-0 keyframes run larger than 150 KB, `N` and the ceilings are lowered to hold the budget (AW-3). This shows the per-viewer arithmetic the round-2 review asked for.

---

## BDD Scenarios

```gherkin
Feature: Live-browser video streaming (A2 — headful capture on a virtual display, A2-only)

  # US-1
  Scenario: Warm stream paints within one second               # Happy Path
    Traces to: US-1 / AC-1
    Given a warm video stream with a cached GOP
    And a viewer whose browser decodes the negotiated codec
    When the viewer attaches
    Then a decoded frame paints to the canvas within 1 second

  Scenario: Scrolling updates continuously                     # Happy Path
    Traces to: US-1 / AC-2
    Given an attached video stream
    When the agent scrolls a long page
    Then the canvas updates without a per-step stall

  Scenario Outline: Full-motion video meets the framerate floor # Happy Path
    Traces to: US-1 / AC-3
    Given an attached stream at <panel> on the headful+display runtime
    When an in-page video element plays
    Then the rendered frame rate is at least the Gate-0-confirmed floor (>= 24 fps)
    Examples: | panel | | 45% split | | full row |

  # US-2
  Scenario: Click over video dispatches to the right coordinate # Happy Path
    Traces to: US-2 / AC-1
    Given an attached video stream and the viewer holds control
    When the viewer clicks a known canvas coordinate
    Then CDP Input.dispatch fires at the corresponding CSS pixel

  Scenario: Typing over video reaches the agent tab            # Happy Path
    Traces to: US-2 / AC-2
    Given an attached video stream and the viewer holds control
    When the viewer types a string
    Then the keystrokes are dispatched via the unchanged CDP path

  Scenario: Coordinate mapping is unchanged                    # Alternate Path
    Traces to: US-2 / AC-3
    Given the canvas replaced the img element
    When coordinate mapping runs
    Then it uses the same viewport metadata fields as the JPEG path

  # US-3
  Scenario: Cold start brings up capture within budget         # Happy Path
    Traces to: US-3 / AC-1
    Given no active stream for the session and the display+audio sidecars are up
    When the first viewer attaches
    Then capture bring-up completes and a frame paints within the cold-start budget

  Scenario: Warm authorized second viewer replays the keyframe # Happy Path
    Traces to: US-3 / AC-2
    Given a running stream with a cached GOP
    When a second authorized viewer attaches
    Then the relay sends the last keyframe first and it paints within 1 second

  Scenario: Hung bring-up times out to unavailable             # Error Path
    Traces to: US-3 / AC-3
    Given a capture or display bring-up that never signals ready
    When the bring-up timeout expires
    Then the viewer is shown the unavailable state

  Scenario: One slow viewer does not stall others             # Error Path
    Traces to: US-3 / AC-4
    Given two viewers on one stream
    When one viewer's send queue is full
    Then chunks drop for that viewer only
    And the source and the other viewer continue uninterrupted

  # US-4 (audio, proven)
  Scenario: Audio plays when the sink is up and Opus negotiated # Happy Path
    Traces to: US-4 / AC-1
    Given the PulseAudio sidecar is up and Opus is negotiated
    When the agent tab plays audio
    Then the viewer decodes and plays browser_audio_chunk
    And measured A/V skew is within the documented bound

  Scenario: Audio-sidecar absence never blocks video           # Alternate Path
    Traces to: US-4 / AC-2, US-11 / AC-2
    Given the PulseAudio sidecar is unavailable
    When a viewer attaches
    Then video streams normally and no audio error is surfaced
    And the Chrome launch was not blocked on PulseAudio readiness

  # US-5
  Scenario: No decodable codec shows the unavailable state     # Error Path
    Traces to: US-5 / AC-1
    Given a viewer whose video_caps match no offered codec
    When it attaches
    Then the panel shows the generic video-capable-browser message
    And no stream and no JPEG frame and no A1 fallback is served

  Scenario Outline: Missing stack shows the unavailable state, browsing continues  # Error Path
    Traces to: US-5 / AC-2, US-8 / AC-2, US-10 / AC-3
    Given the install is missing <missing>
    When a viewer attaches
    Then the unavailable state shows with a generic end-user string
    And the specific cause is logged operator-side only
    And agent browsing continues with no crash
    Examples: | missing | | full Chrome (headless-shell) | | the Xvfb display | | a Linux platform (macOS/Windows) |

  # US-6
  Scenario Outline: Codec negotiated; single encode per source # Happy/Error Path
    Traces to: US-6 / AC-1, AC-2, AC-3
    Given a running encode of <active> (if any) and a viewer advertising <caps>
    When it attaches
    Then the result is <outcome>
    Examples:
      | active   | caps        | outcome           |
      | none     | h264m,vp8   | h264-main stream  |
      | none     | vp8         | vp8 stream        |
      | h264main | vp8         | unavailable-state |
      | none     | none        | unavailable-state |

  # US-7
  Scenario: Streams over WSS with UDP blocked                  # Happy Path
    Traces to: US-7 / AC-1
    Given the gateway is reachable only over WSS and UDP is blocked
    When a viewer attaches
    Then the video stream is delivered over /api/v1/browser/ws
    And no additional external port is opened

  # US-8
  Scenario: Full Chrome downloaded, verified, classified capable # Happy Path
    Traces to: US-8 / AC-1
    Given a fresh Linux video-capable install with Gate 0 passed and Xvfb and PulseAudio present
    When the managed browser is provisioned
    Then the full Chrome build is integrity-verified and classified video-capable

  Scenario: Normal browsing is behaviorally equivalent on headful full Chrome # Alternate Path (regression)
    Traces to: US-8 / AC-3
    Given the managed browser runs headful full Chrome
    When an agent runs the fixed 12-URL equivalence corpus
    Then navigation, normalized DOM/tool outputs, and screenshots at SSIM >= 0.95 match the headless-shell baseline
    And the enumerated excluded fields are masked

  # US-9 (security)
  Scenario: Ingest rejects an unauthenticated connection       # Error Path
    Traces to: US-9 / AC-1
    Given the capture ingest endpoint
    When a connection presents no valid stream-scoped token
    Then it is rejected before any chunk is relayed

  Scenario: Ingest reconnect on a transient drop keeps the stream # Alternate Path
    Traces to: US-9 / AC-2
    Given a valid stream-scoped token on a live stream
    When the ingest WS drops transiently and the capture reconnects with the same token
    Then the reconnect is accepted and the stream survives
    And a different concurrent connection presenting the same token is rejected

  Scenario: Ingest token dies with the stream                  # Error Path
    Traces to: US-9 / AC-2
    Given a stream that has ended and its token
    When a connection presents that token
    Then it is rejected

  Scenario: Agent-browsed page cannot capture media            # Error Path
    Traces to: US-9 / AC-3
    Given the headful runtime with audio consent granted only to the encoder-page origin
    When a page the agent browses (different origin) calls getUserMedia for audio
    Then no media stream is granted to it

  Scenario: Capture binds by unguessable key, not tab title    # Error Path
    Traces to: US-9 / AC-4
    Given two tabs with identical titles
    When the capture binds its target
    Then it binds by the per-stream key and never mis-binds by title

  Scenario: Ingest token is not recoverable from the CDP transport # Error Path
    Traces to: US-9 / AC-5
    Given CDP over --remote-debugging-pipe (no TCP/HTTP surface)
    When a non-gateway loopback process probes the CDP endpoint
    Then it cannot enumerate the target or read the ingest token

  # US-10 (Xvfb sidecar)
  Scenario: Xvfb spawned and wired before Chrome launches      # Happy Path
    Traces to: US-10 / AC-1
    Given a Linux video-capable install
    When the browser subsystem starts
    Then the Xvfb sidecar is ready and its DISPLAY is wired into the managed-Chrome launch

  Scenario: Xvfb crash restarts and degrades meanwhile         # Error Path
    Traces to: US-10 / AC-2
    Given a running stream
    When the Xvfb sidecar exits unexpectedly
    Then the supervisor restarts it with bounded backoff
    And live view shows the unavailable state until the display is back

  Scenario: No Xvfb means not-video-capable, browsing still works # Error Path
    Traces to: US-10 / AC-3
    Given Xvfb cannot start on this platform
    When the browser subsystem starts
    Then the install is classified not-video-capable
    And agent browsing still works with no crash

  # US-11 (PulseAudio sidecar)
  Scenario: PulseAudio spawned on a stable socket, Chrome not blocked # Happy Path
    Traces to: US-11 / AC-1
    Given a Linux video-capable install
    When the browser subsystem starts
    Then the PulseAudio sidecar is up on a stable socket path with null-sink + remap-source loaded via the native-protocol client
    And PULSE_SERVER is wired into the managed-Chrome launch
    And the Chrome launch proceeds regardless of PulseAudio readiness

  Scenario: PulseAudio restart re-enables audio                # Alternate Path
    Traces to: US-11 / AC-3
    Given the PulseAudio sidecar restarted on the same socket path
    When the next stream starts
    Then the modules are reloaded and audio resumes
    And Chrome is not restarted if it can re-enumerate the device

  # Edge
  Scenario: Capture crash fails the stream cleanly             # Edge Case
    Traces to: US-3 / AC-3, Edge "capture crash"
    Given an attached video stream
    When the capture surface exits unexpectedly
    Then attached viewers move to the unavailable state and no frozen frame is shown

  Scenario: Oversize keyframe rejected and encoder steps down  # Edge Case
    Traces to: US-1 / AC-1, Edge "keyframe exceeds bound"
    Given a keyframe larger than the ingest single-message bound
    When it is ingested
    Then it is rejected and the encoder is signalled to step bitrate/resolution down
    And no partial frame is assembled or relayed

  Scenario: Aggregate GOP cache stays under the ceiling        # Edge Case
    Traces to: US-3 / AC-2, Edge "aggregate cache pressure"
    Given M concurrent streams near the aggregate memory ceiling
    When another stream caches a keyframe
    Then the least-recently-viewed stream's cache is evicted to stay under the ceiling

  Scenario: Kill-switch tears down active streams              # Edge Case
    Traces to: US-5, FR-020, Edge "kill-switch flipped on"
    Given active video streams and attached viewers
    When the operator flips the kill-switch on
    Then new attaches get the unavailable state
    And active streams are torn down to the unavailable state without redeploy
```

---

## Test-Driven Development Plan

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestIngest_RejectsUnauthenticated` | Unit (Go) | Ingest rejects unauthenticated | No/bad token → rejected pre-relay. |
| 2 | `TestIngest_TokenStreamScoped` | Unit (Go) | Ingest token dies with the stream | Mis-scoped / post-stream-end token rejected. |
| 3 | `TestCapture_OriginScopedConsent_AgentPageDenied` | Integration (Go, scoped) | Agent-browsed page cannot capture | Consent (audio) granted only to the encoder-page origin; a browsed page's `getUserMedia` (audio) is denied — video capture is CDP `Page.startScreencast` with no page-callable API (no global fake-ui). |
| 4 | `TestStreamRelay_GOPCache_ReplaysKeyframeFirst` | Unit (Go) | Warm second viewer replays | Keyframe then deltas to a fresh subscriber. |
| 5 | `TestStreamRelay_AggregateCacheCeiling_Evicts` | Unit (Go) | Aggregate cache under ceiling | LRU eviction keeps total < ceiling with M streams; a stream with a live or **currently-attaching** viewer is **never** evicted (idle streams evicted first — MAJ-005). |
| 6 | `TestStreamRelay_SlowViewerDropsIsolated` | Unit (Go) | One slow viewer no stall | Full queue drops only that viewer. |
| 7 | `TestStreamRelay_CaptureExit_MarksFailed` | Unit (Go) | Capture crash fails cleanly | Ingest close → stream failed → viewers notified. |
| 8 | `TestBringup_Timeout_Unavailable` | Unit (Go) | Hung bring-up times out | No ready within timeout (capture/display) → unavailable. |
| 9 | `TestCodecNegotiation_SingleEncode_Matrix` | Unit (Go) | Codec negotiated; single encode | caps×active → {h264-main,vp8,unavailable}; never JPEG, never A1, never 2nd encoder. |
| 10 | `TestIngest_OversizeKeyframe_RejectedNotFragmented` | Unit (Go) | Oversize keyframe rejected | Chunk > bound → rejected + step-down signalled; no reassembly (M-4). |
| 11 | `TestWSFraming_BinaryChunks_TextControl` | Unit (Go) | (transport, FR-017) | Opcode-tagged send items: Binary for chunks, Text for JSON on one `sendCh`; preserves the nil-ping sentinel. |
| 12 | `TestContracts_AttachCaps_IngestMessages` | Unit (Go, contract) | (all attach/ingest) | `browser_attach` (video_caps/audio_caps) + ingest messages round-trip; **`browser_screencast` removed**; `make verify-contracts` clean. |
| 13 | `TestVideoChunk_BinaryEnvelope_RoundTrip` | Unit (Go, contract) | Warm paint; scrolling | `browser_video_chunk` envelope `{seq:u32, ts:u64, key:u8, len:u32, payload}` parses (no fragment field). |
| 14 | `TestManagedLaunch_Headful_Display_OriginScopedCapture` | Integration (Go, scoped) | Xvfb wired; US-9/AC-3 | Launch is headful with DISPLAY set + origin-scoped consent (not global grant to agent pages) + CDP over --remote-debugging-pipe (no TCP surface). |
| 15 | `TestInstaller_FullChromeDefault_DetectsEither_VerifiesIntegrity` | Unit (Go) | Full Chrome verified; missing-stack unavailable | Full-Chrome default + detection + hash check; no crash on shell; platform-matrix classify. |
| 16 | `TestRegression_NormalBrowse_HeadfulEquivalence` | Integration (Go, scoped) | Normal browsing equivalent | Fixed 12-URL corpus vs headless-shell baseline; SSIM ≥ 0.95; excluded fields masked (§Equivalence Corpus). |
| 17 | `TestXvfbSidecar_SpawnSuperviseRestart` | Unit (Go) | Xvfb spawned/wired; Xvfb crash restarts | Spawn, DISPLAY publish, crash → bounded-backoff restart; no-Xvfb/non-Linux → not-capable. |
| 18 | `TestPulseSidecar_SpawnLoadModules_StableSocket` | Unit (Go) | PulseAudio spawned+modules | Spawn on stable socket + LoadModule(null-sink, remap-source) via native client; Chrome-launch not blocked. |
| 19 | `TestAudioChunk_SuppressedWhenNotNegotiated` | Unit (Go) | Audio-sidecar absence no-block | Audio not sent to video-only viewers; absence never blocks video. |
| 20 | `browserLiveWs.binary-frames.test.ts` | Unit (TS) | Warm paint; scrolling | `binaryType='arraybuffer'`; routes video/audio/init/JSON; feeds mocked `VideoDecoder`/`AudioDecoder`. |
| 21 | `BrowserLiveView.unavailable-state.test.tsx` | Unit (TS) | No codec / missing-stack / kill-switch | Renders generic message; controls operable; no JPEG/A1 path. |
| 22 | `BrowserLiveView.canvas-input-mapping.test.tsx` | Unit (TS) | Click/typing right coord | Canvas→CSS mapping equals prior `<img>`. |
| 23 | **CI fps E2E: headful+Xvfb capture of a playing video, measure distinct fps** | E2E (CI `ci-omnipus`) | US-1/AC-3 (**Gate 0 / EC-1**) | Runs the full pipeline the dev-pod sandbox blocked; asserts distinct fps ≥ floor for the selected mechanism. **Gates the epic.** |
| 24 | E2E: attach → smooth video → drive → detach (Playwright) | E2E | US-1, US-2, US-3 | Against the running binary. |
| 25 | E2E: unavailable state on stubbed `VideoDecoder` | E2E | US-5 | Message shown; no stream. |
| 26 | E2E: audio plays and is roughly synced | E2E (build-phase, audio) | US-4 / AC-1 | Real audio page → audible; A/V skew within the build-phase-measured bound (SC-013; not a Gate-0 criterion — MAJ-003). |
| 27 | `TestViewerAttach_Unauthorized_RejectedBeforeGOPReplay` | Unit (Go) | (M-6, FR-015) | An unauthorized viewer attach is rejected **before** any GOP replay — no cached frame served. |
| 28 | `TestObservability_MetricsEmitted` | Unit (Go) | (M-7, FR-019) | Every FR-019 counter/gauge registered and increments under simulated load. |
| 29 | `TestIngest_DropRemintsAndRelaunches` | Unit (Go) | Ingest drop → re-mint + relaunch (CRIT-002) | On a transient ingest drop the gateway invalidates the token, re-mints, and relaunches the loopback encoder page; a second connection presenting the OLD token is rejected (dead token). No same-token reconnect path exists; token dead post-stream. |
| 30 | `TestIngest_TokenNotRecoverableFromCDP` | Integration (Go, scoped) | (C-3, EC-3, FR-013) | With CDP over `--remote-debugging-pipe` (no TCP/HTTP surface), a non-gateway/co-tenant probe cannot enumerate a CDP target or read the token — on any kernel. |
| 31 | `TestPulseSidecar_RestartReEnumeratesDevice` | Integration (Go, scoped) | PulseAudio restart re-enables audio | After a mid-run daemon restart on the same socket, audio resumes without Chrome restart (or the AC's next-launch path is exercised). |
| 32 | `TestAudit_StreamLifecycle_And_IngestRejections` | Unit (Go) | (m-2, FR-024) | Stream start/stop + every ingest-auth rejection writes an audit entry. |

Order: security + relay + contract units (1–13, 27, 29–30, 32), launch/installer + sidecar units (14–19, 31), observability (28), SPA units (20–22), E2E last (23–26). **Gate 0 / EC-1..EC-4 (incl. Test 23) run before any of this is built.**

### Test Datasets

**DS-1 — Codec negotiation, single encode** (US-6 / Test 9)
| active encode | viewer video_caps | expected | Traces to |
|---|---|---|---|
| none | `[h264-main, vp8]` | h264-main | US-6/AC-1 |
| none | `[vp8]` | vp8 | US-6/AC-1 |
| h264-main | `[vp8]` | unavailable-state | US-6/AC-2 |
| none | `[]` | unavailable-state | US-5/AC-1 |
| none | `[h264-baseline]` (encoder-unsupported) | unavailable-state | US-5/AC-1 |

**DS-2 — Binary chunk / ingest bound** (Test 10, 13; realistic sizes, u32/u64)
| seq (u32) | ts (u64) | key | len (u32) | note | Traces to |
|---|---|---|---|---|---|
| 0 | 0 | 1 | 153600 | 150 KB keyframe (typical, under bound) | US-1/AC-1 |
| 1 | 33 | 0 | 4096 | typical delta | US-1/AC-2 |
| 2 | 66 | 1 | 2097152 | 2 MB keyframe (at the default bound — accepted) | Edge |
| 3 | 99 | 1 | 3145728 | 3 MB keyframe (> bound → **rejected + step-down**, NOT fragmented) | Edge oversize |
| 4294967295 | 18446744073709551615 | 0 | 65535 | seq/ts max, small delta | US-1/AC-2 |
| 5 | 132 | 0 | 0 | empty payload (reject) | Edge |

**DS-3 — Backpressure / aggregate memory** (Test 5, 6)
| streams | condition | expected | Traces to |
|---|---|---|---|
| 2 | one viewer queue full | drop slow only | US-3/AC-4 |
| M (at ceiling) | new keyframe cached | LRU-evict least-recent stream | Edge aggregate |
| 1 | queue full then drains | keyframe-on-recover | Edge |

**DS-4 — Ingest auth + reconnect** (Test 1, 2, 29)
| token | state | expected | Traces to |
|---|---|---|---|
| absent | — | reject | US-9/AC-1 |
| valid | stream A live, first connect | accept (A only) | US-9/AC-2 |
| valid for A | second connection presenting A's token while A live | reject (single connection; no same-token reconnect) | US-9/AC-2 |
| A's token after an ingest drop | gateway re-mints + relaunches encoder page | old token dead; fresh token on the relaunched page | US-9/AC-2 |
| valid for A | stream A ended | reject (token invalidated at stream end) | US-9/AC-2 |
| valid for A | presented for stream B | reject (mis-scoped) | US-9/AC-2 |

**DS-5 — Stack detection / capability** (Test 15, 17, 18)
| platform | full chrome | Xvfb | PulseAudio | classify | live view | audio | Traces to |
|---|---|---|---|---|---|---|---|
| linux | ok (verified) | up | up | video-capable | streams | yes | US-8/AC-1 |
| linux | ok | up | absent | video-capable | streams | no (silent) | US-4/AC-2, US-11/AC-2 |
| linux | ok | absent | — | not capable | unavailable | — | US-10/AC-3, US-5/AC-2 |
| linux | headless-shell | — | — | not capable | unavailable | — | US-5/AC-2 |
| macOS/windows | (headless) | n/a | n/a | not capable | unavailable | — | US-5/AC-2, M-3 |
| linux | both cached (full + headless-shell) | up | up | video-capable (prefer full) | streams | yes | US-8/AC-1, F-08 |
| linux | bad hash | — | — | reject download | (existing error) | — | Edge |

### Regression Test Requirements
Modifies existing functionality (managed-Chrome launch → headful, live-view stream, browser WS write pump, CDP transport, contract).
1. Preserve: agents' normal browsing on the headful full-Chrome runtime (behavioral bar vs headless-shell baseline, concrete corpus); take-the-wheel; annotate; multi-viewer piggyback; tab-change re-bind; existing status/tabs frames still valid; viewer `AuthFrame` auth unchanged; agent browsing on non-video-capable installs (headless).
2. Keep passing: `pkg/tools/browser` live/manager suites; `browser_ws` handler tests; `contracts` `verify-contracts` (with `browser_screencast` removed); SPA `BrowserLiveView`/`browserLiveWs` (updated, not weakened).
3. NEW regression tests: Test 16 (headful behavioral-equivalence corpus); Test 11 (Text control frames still delivered alongside binary + nil-ping sentinel preserved); Test 3/14 (agent browsing cannot capture); Test 17 (no-Xvfb/non-Linux → browsing still works); Test 27 (viewer authz before GOP replay); a seam test that `LiveViewRegistry.Attach` still serves live frame types after the JPEG removal.
4. Regression dataset: DS-5 rows exercising headless-shell + no-Xvfb + non-Linux (agents still browse) alongside the full stack.
5. **Sequencing / live-view continuity (F-02):** `browser_screencast` is today's **sole live-view transport** (removal risk MEDIUM, not LOW). Its removal MUST NOT precede the video path being reachable on video-capable installs; and non-video-capable installs (headless-shell / no-Xvfb / non-Linux) **lose today's working JPEG live view** and land on the unavailable state — an accepted, flagged cost, not a dead-surface cleanup.

---

## Requirements & Success Criteria

### Functional Requirements
- **FR-001**: The system MUST capture the agent tab in a **headful managed Chrome on a virtual display** using the **Gate-0-selected mechanism (b): gateway-driven CDP `Page.startScreencast` on the agent tab → JPEG frames pushed over the authenticated loopback to a controlled encoder page → `createImageBitmap`→`VideoFrame`→ WebCodecs `VideoEncoder`** (R4; mechanism (a) `getDisplayMedia` was rejected as unreliable on bare Xvfb). It MUST NOT encode/transcode media in Go (Go relays bytes only).
- **FR-002**: The gateway MUST relay encoded chunks as binary over `/api/v1/browser/ws` with no new external port and no UDP.
- **FR-003**: The gateway MUST bound each stream's GOP cache (keyframe + ≤ N deltas) AND enforce an aggregate memory ceiling and a max concurrent live-stream count, evicting least-recently-viewed caches. **Eviction MUST NOT drop a stream that has a live or currently-attaching viewer (MAJ-005): under cache pressure it evicts idle (no-viewer) streams first, and on recovery it forces a fresh keyframe rather than starving a viewer of its keyframe.** The Go-process budget MUST stay < 10 MB per the §Go Memory Budget arithmetic; there is **no** fragment-reassembly buffer (M-4).
- **FR-004**: A full-queue viewer MUST have its chunks dropped in isolation.
- **FR-005**: The SPA MUST advertise `VideoDecoder`/`AudioDecoder`-supported codecs in `browser_attach` (`video_caps`, `audio_caps`) and decode the negotiated stream onto a canvas (+ audio element).
- **FR-006**: The system MUST run a single active encoder per source (v1) and negotiate **H.264 main (`avc1.4D40…`)-first, VP8 next** (NOT H.264 baseline `avc1.42E01E`, unsupported) — **the Gate-0 iPad result (EC-4) may invert this to VP8-first**; a viewer needing a codec the active encoder isn't producing MUST get the unavailable state; where no codec intersects it MUST show the unavailable state and MUST NOT serve JPEG or A1. **Sequencing (F-05):** the codec-dependent surfaces (contract `video_caps`, encoder config, DS-1) are built **last** and **gated on EC-4**; if EC-4 inverts the H.264↔VP8 priority, the rework is bounded to the codec-negotiation policy + `video_caps` ordering + DS-1 expected values (re-touching FR-006, US-6, DS-1, and the encoder-config surface) — **not** the transport/relay/GOP path — which is why those surfaces are sequenced last.
- **FR-007**: Where the video-capable stack is absent (headless-shell / no Xvfb / non-Linux / no `VideoDecoder`) or the kill-switch is on, the panel MUST show a **generic** "Live view needs a video-capable browser" string with chrome controls operable; the **specific** cause MUST be logged operator-side only (O-3), never leaked to the end-user string.
- **FR-008**: The take-the-wheel input path (CDP `Input.dispatch`) and coordinate mapping MUST be unchanged.
- **FR-009**: The installer MUST make the integrity-verified **full Chrome build the default** for Linux video-capable platforms **(flipped only after Gate 0 passes)** and MUST implement a **dual-download** design (F-08): two CfT download IDs — `chrome` (default) and `chrome-headless-shell` (fallback) — each with its own binary-name resolver, on-disk layout, and per-build integrity verification, with classification logic selecting between them (so `cftDownloadID` becomes **build-aware** rather than a single const). It MUST detect either binary, and agents' normal browsing on the headful full-Chrome runtime MUST be **behaviorally equivalent** to the headless-shell baseline on the concrete bar (§Equivalence Corpus: navigation, normalized DOM/tool outputs, screenshots SSIM ≥ 0.95; enumerated excluded fields masked). Platforms that cannot run the full stack fall to the unavailable state, never JPEG, never A1.
- **FR-010**: All new wire messages (viewer AND ingest) MUST be defined in `contracts/asyncapi.yaml` and generated before code; the currently-live JPEG `browser_screencast` message MUST be **removed** (M-10; F-02 — it is today's sole live-view transport, not dead); `make verify-contracts` MUST pass. This removal **MUST NOT precede the video path being reachable on video-capable installs** (M-10/F-02 sequencing) — enforced by the seam/regression test in §Regression Test Requirements (#3/#5).
- **FR-011**: The system SHOULD stream Opus via `browser_audio_chunk` (captured via `getUserMedia` on the PulseAudio sink monitor) to negotiating viewers; audio failure MUST NOT block or degrade video, nor block the Chrome launch.
- **FR-012**: The capture→gateway ingest MUST use a distinct **loopback** endpoint (`/api/v1/browser/capture-ingest`) with its own contracted messages (`browser_ingest_init`, binary `browser_ingest_chunk`); it MUST NOT be an external listener, and its loopback-only binding MUST be enforced (reject non-loopback `RemoteAddr`).
- **FR-013**: Ingest MUST authenticate via a per-stream capability token minted by the gateway and delivered to the encoder page **out-of-band** (CDP `addScriptToEvaluateOnNewDocument`), never via URL. The token is **stream-lifecycle-scoped with a single connection** (CRIT-002): a connection without a valid, correctly-scoped token MUST be rejected before any relay, and the token MUST be invalidated when the stream ends. There is **no same-token reconnect** — because the encoder page is a gateway-controlled **loopback** page, a transient ingest-WS drop MUST cause the gateway to **re-mint the token and relaunch the encoder page** (cheap, local) — **re-granting the relaunched page's scoped `audioCapture` consent** (round-5 MINOR) — eliminating the reconnect-vs-duplicate race (the R5 monotonic-epoch discriminator is dropped as self-contradictory; M-5's reconnect-survival concern does not apply to a loopback page the gateway owns). The token MUST NOT be recoverable from the **CDP transport** by a co-tenant/agent process — enforced by **CDP over `--remote-debugging-pipe`** (inherited fd 3/4; **no TCP port, no `/json`, no HTTP surface**; wired via a new **pure-Go CDP-over-pipe transport**, which chromedp v0.15.1 lacks), which is **kernel-independent** (unlike the retracted R5 "keep 9223 + Landlock isolation," which only enforced on kernel 6.7+). Proven empirically by EC-3/Test 30 (C-3/CRIT-001).
- **FR-014**: The ingest endpoint MUST enforce its own max-message bound sized for real keyframes (default configurable, **≥ 2 MB**), independent of the 64 KB viewer-inbound cap; a chunk exceeding the bound MUST be **rejected and trigger an encoder bitrate/resolution step-down** — it MUST NOT be fragmented or partially reassembled (M-4). A **zero-length or otherwise malformed chunk MUST also be rejected** and never relayed (backs DS-2's empty-payload reject row).
- **FR-015**: Viewer attach authorization MUST gate the stream **before any GOP replay** (a viewer cannot pull cached frames from a session it may not view); this MUST have a dedicated test (Test 27) and SC (SC-015).
- **FR-016**: **Video** capture MUST use gateway-driven CDP `Page.startScreencast` (mechanism (b), R4), which exposes **no page-callable capture API** — so an agent-navigated page (different origin) CANNOT obtain a video stream by construction (EC-2 PASS, structural). The process-global `--use-fake-ui-for-media-stream` MUST NOT be used. The one media-consent surface is the encoder page's **audio** `getUserMedia` on the PulseAudio monitor: consent MUST be granted via **CDP `Browser.grantPermissions({origin, permissions:['audioCapture']})` scoped to the encoder-page origin** only, such that an agent-browsed page CANNOT obtain an audio stream. **The encoder-page origin MUST be unguessable AND unreachable/non-navigable by the agent (MAJ-003): a random loopback origin + secret path, with the `audioCapture` grant bound to the specific encoder-page target, not merely its origin — so the agent CANNOT navigate its own tab to the encoder origin to inherit the grant.** Screencast target MUST bind by an unguessable per-stream key, not the tab title. The no-agent-capture posture MUST have its own security-regression test.
- **FR-017**: The browser WS write pump MUST carry binary chunks as WS Binary frames and JSON control frames as Text frames over the one send channel (opcode-tagged queue items, preserving the existing nil-ping sentinel); the SPA MUST set `binaryType='arraybuffer'` and branch on payload type.
- **FR-018**: The system MUST apply capture-, display-, and mid-stream-liveness timeouts; expiry MUST move affected viewers to the unavailable state (no infinite spinner, no frozen frame).
- **FR-019**: The system MUST expose observability: live-stream count, per-stream fps/bitrate, per-viewer drop rate, decode-error rate, capture restart count, **Xvfb/PulseAudio sidecar restart counts**, ingest-auth-reject count. Each metric MUST be emission-tested (Test 28). Alert thresholds + a one-line runbook pointer MUST be documented when the metrics land (O-4).
- **FR-020**: A runtime config flag (`gateway.browser_video_enabled`, default true) MUST disable the video relay without a redeploy; flipping it off MUST both show the unavailable state to new attaches **and tear down active streams** to the unavailable state (m-4).
- **FR-021** (Xvfb sidecar): The system MUST spawn and supervise an Xvfb sidecar for **Linux** video-capable installs (GOOS/build-tag guarded; no-op stub elsewhere → not-video-capable), wire its `DISPLAY` into the managed-Chrome launch, restart it with bounded backoff on crash, and classify the install not-video-capable (unavailable state, agent browsing intact) if it cannot start.
- **FR-022** (PulseAudio sidecar): The system MUST spawn and supervise a PulseAudio sidecar for **Linux** video-capable installs (GOOS/build-tag guarded), on a **stable socket path**, load `module-null-sink` + `module-remap-source` via a pure-Go native-protocol client (no `pactl` subprocess), wire the stable `PULSE_SERVER` into the launch, **never block the Chrome launch on its readiness**, and treat its failure as audio-absent (video unaffected). On restart it MUST reuse the same socket path so audio resumes on the next stream (or next Chrome launch if re-enumeration needs a relaunch — Test 31).
- **FR-023**: The audio capture path MUST use `getUserMedia` on the sink monitor (decoupled from the video capture) → `AudioEncoder` (Opus); it MUST NOT depend on the video capture succeeding.
- **FR-024** (audit, m-2): The system MUST write an audit entry on stream lifecycle (start/stop) and on **every** ingest-auth rejection, on the new privileged ingest entry point; asserted by Test 32.

### Non-Functional Requirements
(source: ADR-044 §2 — the NFR-1/2/3 the FRs reference; defined here per MAJ-004)
- **NFR-1** (compatibility): The live view MUST work in **Safari on iPad** (the operator's primary client) and in **Chrome / Edge / Firefox**. iPad H.264-main/VP8 decode is gated by Gate 0 / EC-4 (the pre-release ship gate).
- **NFR-2** (deployability): The feature MUST work **wherever the gateway works** — behind any HTTPS reverse proxy or tunnel, with **no extra ports, no UDP, and no per-install media configuration** (everything rides the existing authed WSS; Xvfb/PulseAudio/ingest are all loopback-local).
- **NFR-3** (footprint): The **Go-process** steady-state overhead for this feature MUST stay **< 10 MB** (Constraint #3), per the §Go Memory Budget derivation; the browser/sidecar footprint (full Chrome + Xvfb + PulseAudio + SwiftShader) is tracked **separately** by the SC-016 min-spec ship gate.

### Success Criteria
- **SC-001a** (cold): first-viewer first paint ≤ the cold-start budget, p95 over 20 attaches. Gate 0 measured only fps (30) — the cold-start budget is a **pre-`/taskify` measurement gate** (MAJ-002/round-5, consistent with EC-1/SC-016), not a first-wave task; the ≤ 3 s figure is a provisional bound pending that measurement.
- **SC-001b** (warm): second-viewer first paint ≤ 1 s, p95.
- **SC-002**: Scroll glass-to-glass ≤ 150 ms p50 (provisional bound; the real number is a **pre-`/taskify` measurement gate** — MAJ-002/round-5, consistent with EC-1/SC-016 — as Gate 0 measured only fps). **Measured span (m-5):** from the agent-tab compositor repaint (source-side injected monotonic timestamp) to the viewer canvas paint (SPA `requestAnimationFrame` after `VideoDecoder` output), on a single common clock — the definition holds independent of the numeric target.
- **SC-003**: **Full-motion video renders at ≥ 24 distinct fps** at panel size on the headful+display runtime — the number confirmed by **Gate 0 / EC-1 (Test 23)**. **This is the gate SC.** The Gate-0 30 fps was measured on the 8-core `ci-omnipus` box (F-04); the **min-spec fps re-run (EC-1 at the SC-016 min video-capable spec) is a pre-`/taskify` gate (MAJ-001)** before this SC is asserted for the shipping config.
- **SC-004**: With one viewer's queue saturated, the other viewer's fps drops < 10%.
- **SC-005**: Feature works behind a WSS-only proxy with UDP blocked (pass/fail).
- **SC-006**: `govulncheck`/build confirm **zero** new CGo or C-codec dependencies in the Go binary (Xvfb/PulseAudio are separate processes, not linked) (pass/fail).
- **SC-007**: Non-live agent-browse behavioral-equivalence corpus (§Equivalence Corpus, 12 URLs, SSIM ≥ 0.95, masked excluded fields) passes on the headful full-Chrome runtime vs the headless-shell baseline (pass/fail).
- **SC-008**: A non-video-capable install/client shows the generic unavailable state in 100% of attaches, never blank/frozen, never JPEG/A1.
- **SC-009**: Ingest rejects 100% of unauthenticated/mis-scoped/post-stream/non-loopback/concurrent-duplicate connections (pass/fail).
- **SC-010**: An agent-browsed page (different origin) cannot obtain a media stream in 100% of attempts (pass/fail) — proven at Gate 0 / EC-2 and in Test 3.
- **SC-011**: Aggregate GOP cache + per-viewer send queues stay under the ceiling with M concurrent streams (pass/fail); whole-feature steady-state Go-process RAM < 10 MB per §Go Memory Budget (sidecar/Chrome RAM is image-level, covered by SC-016).
- **SC-012**: All FR-019 metrics are emitted and queryable — backed by Test 28 (M-7); **and (F-10) FR-019's alert thresholds + a one-line runbook MUST be present and reviewed at metrics-land time** — definition-of-done: no metric lands without its alert threshold and runbook line.
- **SC-013**: If audio is enabled, A/V skew ≤ 200 ms p95 (**provisional bound**; the real number is a **build-phase measurement taken when the audio phase lands** — audio is sequenced after video (P1), so this is NOT a Gate-0 exit criterion and there is no "audio Gate-0" — MAJ-003/round-5).
- **SC-014**: Xvfb/PulseAudio sidecar crashes are recovered (restart) within a bounded window, and a missing sidecar never crashes agent browsing (pass/fail).
- **SC-015** (M-6): An unauthorized viewer attach is rejected before any GOP replay in 100% of attempts (pass/fail) — Test 27.
- **SC-016** (M-9, **ship gate**): The **total** default footprint (full Chrome + Xvfb + PulseAudio + SwiftShader RAM/CPU) is measured on `ci-omnipus`, and a **documented min video-capable spec** (vCPU / RAM / kernel / packages) is published **before `/taskify` completes**. The min-spec **fps** itself (EC-1 re-run at this spec — F-04) is a **pre-`/taskify` gate** (MAJ-001): those numbers are currently **absent** and MUST be measured **before `/taskify` completes**, before EC-1 is asserted to clear the shipping config. Installs below the min-spec classify not-video-capable (unavailable state). This is a release gate, not a footnote.
- **SC-017** (C-3/CRIT-001): With CDP over `--remote-debugging-pipe` (no TCP port / no `/json` / no HTTP surface), the ingest token is unrecoverable from the CDP transport by any co-tenant/agent process on **any supported kernel** in 100% of attempts (pass/fail) — EC-3 / Test 30 (empirical, pre-build). (The R5 Landlock-isolation claim was kernel-6.7-gated and is retracted.)

### Traceability Matrix
| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|------------------|--------------|
| FR-001 | US-1 | Warm paint; Full-motion floor | 13, 23, 24 |
| FR-002 | US-1, US-7 | Warm paint; WSS UDP-blocked | 13, 24 |
| FR-003 | US-3 | Warm replay; Aggregate ceiling | 4, 5 |
| FR-004 | US-3 | One slow viewer | 6 |
| FR-005 | US-1, US-6 | Warm paint; Codec matrix | 12, 20, 22 |
| FR-006 | US-6, US-5 | Codec/single-encode matrix; No codec | 9 |
| FR-007 | US-5 | No codec; Missing stack; (kill-switch) | 21, 25 |
| FR-008 | US-2 | Click; Typing; Coord unchanged | 22 |
| FR-009 | US-8 | Full-Chrome verified; Headful equivalence | 15, 16 |
| FR-010 | US-1, US-9 | (contract underpins; screencast removed) | 12 |
| FR-011 | US-4 | Audio plays; Audio absence no-block | 19, 26 |
| FR-012 | US-9 | Ingest rejects unauth (+ non-loopback) | 1, 12 |
| FR-013 | US-9 | Ingest rejects unauth; reconnect; token dies; not-from-CDP | 1, 2, 29, 30 |
| FR-014 | US-1 | Oversize keyframe rejected | 10 |
| FR-015 | US-9 | Viewer authz before GOP replay | **27** |
| FR-016 | US-9 | Agent page cannot capture; bind by key | 3, 14 |
| FR-017 | US-1 | (transport) Warm paint; Scrolling | 11, 20 |
| FR-018 | US-3 | Hung bring-up times out; Capture crash | 7, 8 |
| FR-019 | (ops) | (observability) | **28** |
| FR-020 | US-5 | Missing-stack/kill-switch unavailable; kill-switch teardown | 21 |
| FR-021 | US-10 | Xvfb spawned/wired; crash restart; no-Xvfb | 17 |
| FR-022 | US-11 | PulseAudio spawned+stable socket; restart | 18, 31 |
| FR-023 | US-4, US-11 | Audio plays; Audio absence no-block | 19, 26 |
| FR-024 | (audit) | (stream lifecycle + ingest rejections) | **32** |

Every FR appears; every BDD scenario traces to an FR via its `Traces to:` US link. Gate 0 / EC-1..EC-4 gate the epic ahead of all rows.

---

## Ambiguity Warnings (sizing / gate)
| # | Ambiguous | Assumption | Resolution |
|---|---|---|---|
| AW-1 | **Integrated capture fps** in the real runtime | ~25–30 fps (Steel/Mux headful) | **GATE 0 / EC-1 (Test 23): confirm on CI `ci-omnipus`** before ANY headful/installer build — the dev-pod sandbox blocked it. Sets SC-002/003/013. Fail ⇒ re-open ADR (no A1). |
| AW-2 | *(retired — promoted to Gate 0 / EC-4)* | — | iPad H.264-main/VP8 decode is now a hard exit criterion, not a footnote (M-2). |
| AW-3 | GOP `N` + aggregate ceiling values | Sized to keep Go RAM < 10 MB (§Go Memory Budget) | Concrete values from the Gate-0 keyframe-size data at `/taskify`. |
| AW-4 | Cold-start budget (SC-001a) + glass-to-glass (SC-002) numbers | 3 s / 150 ms provisional bounds | **Named first-wave measurement tasks (F-07), not silent placeholders** — Gate 0 measured only fps (30); measure SC-001a cold-start and SC-002 glass-to-glass first-wave. |
| AW-5 | *(retired — resolved by C-3 fix)* | — | CDP pipe/ephemeral-port + `addScriptToEvaluateOnNewDocument` delivery + EC-3/Test 30 close it (was "deferred to /taskify"). **(R5/F-01 then R6/CRIT-001: superseded — the port is removed entirely; CDP-over-pipe transport, no TCP surface.)** |
| AW-6 | *(retired — resolved by C-2 fix + Gate 0 / EC-2)* | — | Mechanism is Gate-0-selected & proven (origin-scoped grant, or screencast escalation), not deferred. |
| AW-7 | *(retired — promoted to SC-016 ship gate)* | — | Total footprint + min video-capable spec is now a documented release gate (M-9), not a footnote. |
| AW-8 | Whether headful `Page.startScreencast` (mechanism b) hits the fps floor | Unknown until Gate 0 | Measured alongside (a) in EC-1; the mechanism that passes fps **and** EC-2 wins. |

**Operator dispositions captured:** A2 chosen and **A2-only** (no A1 tier — M-1); full Chrome + Xvfb + PulseAudio = default video-capable stack (Linux-only — M-3); audio proven + decoupled + best-effort; no JPEG fallback + the currently-live JPEG `browser_screencast` message removed (F-02 — sole live-view transport, not dead); single-encode + no-ABR v1; fps + isolation + CDP-token + iPad decode all confirmed at Gate 0 before build.

## Holdout Evaluation Scenarios (NOT for development; excluded from traceability)
1. **[happy]** The agent plays a full-motion video in the panel — the picture is fluid, not a slideshow.
2. **[happy]** On a real iPad over cellular, a scrolling news site looks like video.
3. **[happy]** Two people open the same session — both see the live view within a second, in sync.
4. **[happy]** A page with sound plays — you hear it, roughly lip-synced to the picture.
5. **[error]** A browser without WebCodecs shows a clear "needs a video-capable browser" message, never a blank box.
6. **[error]** Killing the encoder page mid-session tells you the stream ended; it does not freeze on a stale frame.
7. **[error]** On a minimal / macOS / Windows install (no Xvfb), the agent still browses fine and the panel shows the honest unavailable state.
8. **[edge]** Behind a corporate proxy blocking UDP, the panel still works.
9. **[edge]** A phone-width docked panel fills at the right resolution, not a cropped 720p.
10. **[security]** A local process that opens the ingest socket without a token cannot inject frames into any session.
11. **[security]** A page the agent browses that tries to screen-capture gets nothing.
12. **[security]** A co-tenant process on shared loopback cannot read the ingest token from the CDP transport.

## Assumptions
- The video-capable runtime = **full Chrome (headful) + Xvfb + PulseAudio**, run as supervised sidecars on **Linux**; the Go binary stays single, the deployment image gains these packages.
- Full Chrome-for-Testing publishes a platform build (else headless-shell + unavailable state).
- `VideoEncoder`/`AudioEncoder`/`VideoDecoder`/`AudioDecoder` (WebCodecs) are the primitives; no polyfill.
- Encoder codecs: **H.264-main, VP8** (negotiated); VP9/AV1 available but not v1-negotiated. H.264-baseline unsupported (S-1b). Gate-0 EC-4 may invert H.264/VP8 priority for the iPad.
- The encoder page is embedded (`go:embed`), loopback-only, non-navigable, no remote fetch, audit-logged; the ingest endpoint is loopback-only.
- Single active encoder per source in v1; ABR + `browser_stream_bitrate` deferred to v1.1.
- The integrated fps, capture-isolation mechanism, CDP-token confidentiality, and iPad decode are **all confirmed at Gate 0** before the epic is built — none is assumed into implementation.
- ADR-044 §6 (with the §6.0 A2 amendment and §6.0.1 R3 reconciliation) is the authority; this spec carries the behavior.

## Validation / Gate
- **Hard gate before ANY implementation (Gate 0):** run the four exit criteria on **`ci-omnipus`** (or a throwaway Fly machine):
  - **EC-1** integrated distinct fps ≥ 24 (Test 23) — sets SC-002/003/013; **fail ⇒ do not ship A2, re-open ADR-044 (no A1)**.
  - **EC-2** capture isolation proven (agent-origin page denied) for the selected mechanism.
  - **EC-3** ingest token unrecoverable from the CDP transport.
  - **EC-4** iPad decodes H.264-main or VP8.
  These run **before** the installer-default flip and the `managedExecAllocatorOpts` headful/CDP edits (C-1). The dev-pod agent sandbox cannot run the fps pipeline to completion (documented in `ADR-044-spike-results.md`).
- Then: `/grill-spec` this R3 → Gate 0 on CI → `/taskify`.

---

### Spec summary (R3)
- **User stories:** 11 (P0: US-1,2,3,5,7,9,10; P1: US-4,6,8,11) — US-9 extended (reconnect + CDP-token AC-5).
- **BDD scenarios:** 34 (Happy 12, Alternate 4, Error 13, Edge 4) + 3 outlines — +ingest-reconnect, +token-not-from-CDP, +kill-switch-teardown, +missing-stack-browsing-continues (folds m-1).
- **Test datasets:** 5 (DS-1..5), ~26 rows (DS-4 reconnect rows, DS-5 platform rows added).
- **TDD tests:** 32 (Go unit 16, Go integration 5, TS unit 3, E2E 4 incl. the Gate-0 fps test, contract folded) — +27 viewer-authz, +28 metrics, +29 ingest-reconnect, +30 token-not-from-CDP, +31 pulse-restart, +32 audit.
- **Functional requirements:** 24 (FR-024 audit new; FR-013/014/016/020/022 rewritten).
- **Success criteria:** 17 (SC-015 viewer-authz, SC-016 footprint ship gate, SC-017 CDP-token new).
- **Gate 0:** 4 hard exit criteria (EC-1 fps, EC-2 isolation, EC-3 CDP-token, EC-4 iPad) gate the whole epic.
- **Flagged for follow-up:** AW-1/3/4/8 (sizing/mechanism) — resolved at/before `/taskify`; AW-2/5/6/7 retired into hard gates.

---

## History — Revision R2 Dispositions (A2 pivot, superseded where R3 changed it)

| # | R1 assumption | Spike finding | R2 disposition (R3 refinements in brackets) |
|---|---|---|---|
| P-1 | Capture via `getDisplayMedia`/`tabCapture` headless | Both fail headless | Capture requires a real framebuffer → HEADFUL on Xvfb (US-10). [R3: mechanism selected at Gate 0.] |
| P-2 | WebCodecs / full Chrome justify the switch | Encoder works headless too | Full Chrome justified by CAPTURE, default for video-capable installs. [R3: flip only post-Gate-0.] |
| P-3 | Audio spike-gated, MAY not land | Audio PROVEN (S-3) | Audio a real capability, decoupled (US-11). [R3: best-effort, never blocks Chrome; stable socket.] |
| P-4 | H.264 baseline preferred | Baseline NOT supported | H.264-main-first, VP8 next. [R3: EC-4 may invert for iPad.] |
| P-5 | Full Chrome default deferred | A2 needs it | Full Chrome + two sidecars = default video-capable stack. [R3: Linux-only matrix; min-spec ship gate.] |
| P-6 | CRIT-002 no global media auto-accept | Auto-grant flags are process-global | Isolated capture context. [R3: origin-scoped `Browser.grantPermissions`, global fake-ui FORBIDDEN, proven at EC-2.] |
| P-7 | fps assumed headless | Playwright "25fps" is frame-duplication | Gate on CI fps. [R3: Gate 0 / EC-1, sequenced before the build.] |

## History — Revision R1 Dispositions (grill-spec BLOCK, superseded by the A2 pivot where noted)

| Finding | R1 Disposition |
|---|---|
| CRIT-001 ingest leg unauth/uncontracted | Fixed — authenticated loopback ingest + per-stream token (FR-012..015). **Held** (R3: token stream-lifecycle-scoped, M-5; CDP confidentiality closed, C-3). |
| CRIT-002 global media auto-accept | R1 forbade the flag; **R2/P-6 → R3** origin-scoped consent, no global flag, proven at EC-2. |
| MAJ-001 ADR JPEG-fallback contradictions | Fixed; **R3 removes the JPEG `browser_screencast` message entirely** (M-10; F-02 corrects the "dead" label — it was the currently-live transport). |
| MAJ-002 64 KB cap rejects keyframes | Fixed — ingest keyframe-sized bound (FR-014). **Held** (R3: fragmentation dropped, reject+step-down instead, M-4). |
| MAJ-003 binary transport vs text pump | Fixed — opcode-tagged framing (FR-017). **Held.** |
| MAJ-004 "byte-for-byte" infeasible | Fixed — behavioral-equivalence bar (FR-009). **Held** (R3: concrete corpus + SSIM, M-8). |
| MAJ-005 undecided codec AC | Fixed — single-encode; disjoint viewer → unavailable. **Held** (H.264-main). |
| MAJ-006 cold vs warm first-paint | Fixed — SC-001a/b split + bring-up timeout (FR-018). **Held.** |
| MAJ-007 GOP memory unbounded | Fixed — aggregate ceiling (FR-003). **Held** (R3: arithmetic shown, no reassembly buffer, M-9). |
| MIN-001..009, OBS-001/002 | Held (file ref exec_resolver.go:31; DS realistic sizes; observability FR-019; kill-switch FR-020; single-encode; ABR deferred; etc.). R3: `ts:u48`→`u64` (m-3); audit FR-024 (m-2); metrics tested (M-7). |
