# Adversarial Review: Live-Browser Video Streaming (WebCodecs relay) — Round 4 (grilling R5)

**Spec reviewed**: `docs/internal/specs/live-browser-video-streaming-spec.md` (Revision R5)
**Review date**: 2026-07-16
**Verdict**: **BLOCK**

## Executive Summary

R5 resolved the round-3 findings but two of its own new dispositions (F-01 process-isolation and F-06 connection-epoch) introduce security controls that do not hold as written, and F-04 quietly re-opens the C-1 sequencing defect R3 claimed to close. Total 12 findings: **2 CRITICAL, 5 MAJOR, 5 MINOR, 2 OBSERVATION**. Both CRITICALs are code-verified against `pkg/sandbox` and the spec's own text. Verdict is BLOCK.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 5 |
| MINOR | 5 |
| OBSERVATION | 2 |
| **Total** | **14** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] CDP-token confidentiality (EC-3/FR-013/SC-017) rests on a kernel feature that most video-capable installs won't have

- **Lens**: Insecurity (Elevation of Privilege / Information Disclosure), Incompleteness
- **Affected section**: F-01 disposition (lines 27, 39); EC-3 (line 176); FR-013 (line 748); SC-017 (line 779); Platform Matrix (lines 106-112); `browserDebugPort` symbol row (line 134).
- **Description**: The entire R5 confidentiality argument for the ingest token is "keep port 9223, isolate access — agent processes Landlock-sandboxed **without 9223 in their connect allow-list**." Code verification shows the connect allow-list is enforced **only** by Landlock `NET_CONNECT_TCP`, which requires **Landlock ABI v4 (kernel 5.19+)** — `pkg/sandbox/sandbox.go:153` ("installed by DefaultPolicy via the Landlock NET_CONNECT_TCP rule type (kernel ABI v4+)") and `pkg/sandbox/sandbox_linux.go:157` ("Landlock ABI v4+ adds NET_BIND_TCP and NET_CONNECT_TCP"). Below that, `pkg/sandbox/sandbox.go:599` documents the fallback: "kernel sandboxing is unavailable (non-Linux platforms, Linux < 5.13, Termux…)" → app-level enforcement, which has **no** connect-port filtering. The spec's Platform Matrix classifies "Linux x86_64/arm64 server (Xvfb + PulseAudio present)" as video-capable with **no stated kernel floor**. Xvfb, PulseAudio, and full Chrome run fine on Linux 5.4 / 5.10 / 5.15. So on the entire band of Linux < 5.19 (and especially < 5.13), a video-capable install runs the encoder page holding the ingest token on 9223, while the agent's own tool processes (e.g. `bash`, ADR-036) are **not** connect-confined and can dial loopback 9223 and read the token via CDP `Runtime.evaluate` on the encoder target. F-01's "residual (non-sandboxed pod processes) documented" waves this away as an edge — it is a whole platform class the product explicitly supports (Constraint #4: graceful degradation on < 5.13).
- **Impact**: SC-017's claim ("ingest token unrecoverable … by **any** co-tenant/agent process in **100%** of attempts") is **false** on any video-capable Linux kernel below 5.19. A motivated agent (running attacker-authored code through the `bash` tool) reads the token, connects to the loopback ingest WS, and injects arbitrary frames into a live operator's video stream (Tampering) or reads the stream key (Info Disclosure) — the exact threat EC-3 is supposed to close. EC-3/Test 30 as written ("prove a **sandboxed** agent process can neither dial 9223 nor read the token") passes on a 5.19+ CI box and never exercises the < 5.19 case, so the gate gives false green.
- **Recommendation**: Either (a) add a hard **kernel/Landlock-ABI floor to the video-capable classification** — an install is video-capable only if Landlock ABI ≥ 4 is present, else it falls to the unavailable state — and state it in the Platform Matrix, FR-021, and SC-016; or (b) close confidentiality by a mechanism that does not depend on Landlock ABI v4 (the netns / CDP-over-pipe escalation F-01 mentions "only if later required" — it is required now for < 5.19 installs). Rewrite SC-017 to scope its "100%" claim to the exact enforcement condition, and rewrite EC-3/Test 30 to run on a below-ABI-v4 kernel and assert the token is still unrecoverable (which, under the current design, it is not — so the test would fail, which is the point).

---

#### [CRIT-002] The connection-epoch discriminator (F-06/FR-013/DS-4/Test 29) is self-contradictory and does not reject concurrent duplicates

- **Lens**: Inconsistency, Insecurity (Spoofing/Tampering)
- **Affected section**: F-06 (line 32); FR-013 (line 748); DS-4 (lines 702-710); Test 29 (line 667); US-9/AC-2 (line 235).
- **Description**: FR-013 says, in one sentence: "**the gateway mints a strictly increasing epoch per ingest connect**; … a **connect carrying a strictly higher epoch** supersedes the prior holder (legitimate reconnect); a connect carrying a **lower-or-equal epoch** while a holder is live MUST be rejected (replay/concurrent duplicate)." These two clauses cannot both be true. If the **gateway** mints the epoch on each connect (a monotonic server counter), the client never "carries" one, and **every** new connect — including a malicious concurrent duplicate — is assigned a fresh, strictly-higher epoch and therefore **always supersedes**; the "lower-or-equal → reject" branch can never fire, so concurrent-duplicate rejection (the whole point of M-5/F-06) never happens. If instead the **client** carries the epoch, a replay/duplicate attacker simply presents `current+1` and steals the holder slot. Either reading defeats the stated goal. DS-4 row "reconnect, strictly-higher epoch → accept (supersede prior holder)" and "lower-or-equal → reject" only make sense if the epoch is client-supplied AND bound to something an attacker can't reproduce — but the spec binds it to nothing; it is just "a monotonic epoch."
- **Impact**: The control the spec advertises as defeating "replay/concurrent duplicate" provides neither property. Given the token is (attempted-to-be) confined, a "concurrent duplicate" can only be a self-inflicted double-connect, for which the epoch is unnecessary; if the token is **not** confined (see CRIT-001, on < 5.19), an attacker with the token presents a higher epoch and hijacks the live stream — the epoch actively helps the attacker win the race deterministically.
- **Recommendation**: Decide and state precisely **who generates the epoch and what binds it to the one legitimate holder**. A workable minimal design without an epoch protocol: the gateway keys the single-active-holder slot to the stream; a **new** authenticated connect atomically **closes and replaces** the current holder (newest-wins), and there is no client-presented ordinal at all — reconnect and duplicate are indistinguishable and both simply become "newest connection is the holder," which is safe iff the token is confined. If you keep an ordinal, the gateway must issue a **per-reconnect nonce to the encoder page out-of-band** (same channel as the token) and reject any connect not presenting the current nonce — then define exactly that in FR-013, DS-4, and Test 29. Remove the "gateway mints per connect" clause or the "connect carrying a … epoch" clause; they contradict.

---

### MAJOR Findings

#### [MAJ-001] F-04 re-opens the C-1 defect: the real (min-spec) fps gate is deferred to after `/taskify` and the build

- **Lens**: Incorrectness, Infeasibility
- **Affected section**: F-04 (line 30); EC-1 caveat (lines 14, 174); SC-003 (line 765); SC-016 (line 778); Gate 0 §STATUS (line 170); Handoff (line 6).
- **Description**: R3's C-1 fix was explicit: sequence the fps gate **before** the headful/installer build so the architecture isn't built on an unproven number, with fail branch "fps < 24 ⇒ do NOT ship A2, re-open ADR-044." R5's F-04 concedes the measured 30 fps was on an 8-core CI box and that "SwiftShader software encode is host-dependent," so the number that actually gates shipping — fps at the min video-capable spec — is **re-scoped to a first-wave measurement task**, i.e. run **after** `/taskify` and inside the build. The spec then declares EC-1 "PASS" (green check, lines 4, 14, 174) and says "the downstream build (spec → taskify → implement) may proceed." So the true gate now sits downstream of the architecture it justifies — the exact shape of C-1 — just relocated from "CI box" to "min-spec."
- **Impact**: If the min-spec fps comes in < 24 (plausible: software SwiftShader encode on a 2-core deployment box vs 8-core CI), the fail branch is "do NOT ship A2, re-open ADR-044" — but by then taskify and implementation of the headful switch, sidecars, installer flip, and relay have already been done. This is throwaway of the same magnitude C-1 was raised to prevent.
- **Recommendation**: Make the **min-spec EC-1 re-run a hard Gate-0 blocker that runs before `/taskify` completes**, not a first-wave task — provision (or name) the actual min video-capable spec box and re-run Test 23 there. Only then mark EC-1 PASS "for the shipping config." Until measured, EC-1 should read "PASS (8-core CI only) / min-spec PENDING — blocks taskify," not an unqualified ✅.

#### [MAJ-002] Contradiction: is the min-spec a `/taskify` blocker (SC-016) or a post-taskify task (Handoff/Gate-0)?

- **Lens**: Inconsistency
- **Affected section**: SC-016 (line 778) vs Handoff (line 6) and Gate 0 §STATUS (line 170) and AW-4 (line 819).
- **Description**: SC-016 states the documented min video-capable spec must be "published **before `/taskify` completes**." The Handoff line lists "Pre-`/taskify` measurement tasks (F-07): SC-016 min-spec + EC-1 re-run" — but Gate-0 §STATUS says "the downstream build (spec → taskify → implement) **may proceed**" and F-07/SC-016 also call these "**first-wave** measurement tasks" (i.e. after taskify). The spec simultaneously asserts the min-spec is a pre-taskify gate and a first-wave (post-taskify) task.
- **Impact**: A reader cannot tell whether `/taskify` is allowed to run before the min-spec/fps numbers exist. Given MAJ-001, this ambiguity is load-bearing — it decides whether the architecture can be built on an unproven fps.
- **Recommendation**: Pick one. Recommended: SC-016 min-spec publication **and** the min-spec EC-1 re-run are both **Gate-0 completion criteria that block `/taskify`**; delete "first-wave" from F-07/SC-016/AW-4 or restrict "first-wave" to numbers that genuinely don't gate the architecture (cold-start SC-001a, glass-to-glass SC-002).

#### [MAJ-003] Origin-scoped audio consent (FR-016) is defeatable if the agent can navigate its tab to the encoder-page origin

- **Lens**: Insecurity (Elevation of Privilege), Incompleteness
- **Affected section**: FR-016 (line 751); US-9/AC-3 (line 236); EC-2 (line 175); Assumptions (line 846, "encoder page is … non-navigable").
- **Description**: FR-016 grants audio capture via `Browser.grantPermissions({origin, permissions:['audioCapture']})` **scoped to the encoder-page origin**. Origin-scoping only holds if no attacker-controlled context can **become** that origin. The spec says the encoder page is "non-navigable, loopback-only" (Assumptions) — but that describes the encoder page's own navigation, not whether **the agent's tab** can be navigated (by agent-authored code) to the encoder page's loopback origin. If the agent can point its tab at `http://127.0.0.1:<gateway>/<encoder-path>` (same origin as the encoder), that tab inherits the `audioCapture` grant and can call `getUserMedia`. EC-2 is only "structural" for **video** (no page-callable screencast API); audio isolation still rests entirely on origin-scoping, and this hole is unaddressed.
- **Impact**: An agent-browsed context captures tab/monitor audio it was never meant to reach — the isolation FR-016 and SC-010 promise ("agent-browsed page cannot obtain a media stream in 100% of attempts") fails for audio.
- **Recommendation**: Bind the grant to an **unguessable per-boot origin/path the agent cannot assume** (e.g. the encoder page served on a random loopback path/port the agent tool set never learns), and/or explicitly forbid navigating any agent-controlled tab to the encoder origin (enforced, tested). Add an AC + test: "agent navigates its tab to the encoder-page origin and calls getUserMedia(audio) → denied."

#### [MAJ-004] NFR-1 / NFR-2 / NFR-3 are referenced but never defined

- **Lens**: Incompleteness (dangling requirement IDs)
- **Affected section**: EC-4 "NFR-1" (lines 51, 177); US-7 "NFR-2" (line 296); §Go Memory Budget "NFR-3" (line 376); SC-006/NFR references.
- **Description**: The spec cites NFR-1 (iPad decode / primary device), NFR-2 (no new external port), and NFR-3 (Go-process < 10 MB) as if they were numbered non-functional requirements, but there is **no Non-Functional Requirements section** anywhere — only Functional Requirements (FR-001..024) and Success Criteria (SC-*). The NFR IDs are orphan references.
- **Impact**: An implementer or taskify pass cannot look up what NFR-1 normatively requires (e.g. is iPad support a MUST or a SHOULD? Which is "the primary device"?). Traceability is broken for three requirements the spec treats as hard gates.
- **Recommendation**: Add an explicit `### Non-Functional Requirements` section defining NFR-1/2/3 with RFC-2119 language and their measurable bars, and map them into the Traceability Matrix, or replace every NFR-n reference with the corresponding SC-/FR-/Gate-0 ID.

#### [MAJ-005] GOP LRU eviction of a stream that still has an attaching/attached viewer is unspecified

- **Lens**: Incompleteness, Inoperability
- **Affected section**: Edge "Aggregate cache pressure" (line 260); FR-003 (line 738); Test 5 (line 643); BDD "Aggregate GOP cache stays under the ceiling" (line 619).
- **Description**: The eviction rule is "LRU-evict the **least-recently-viewed** stream's cache." Least-recently-viewed is not the same as **no-viewers-attached**: a stream can be least-recently-viewed yet still have an attached viewer, or a viewer that just attached and is mid-GOP-replay waiting for the cached keyframe. The spec never says what happens to that viewer when its cache is yanked. Test 5 only asserts "total < ceiling."
- **Impact**: A viewer attached to the evicted stream is starved of the keyframe it needs to start decoding (black/garbled canvas) until the next natural keyframe — with no forced-keyframe recovery specified. Under memory pressure this hits exactly when the system is busiest.
- **Recommendation**: Specify eviction precedence (evict caches of streams with **zero attached viewers** first; only then force-evict a viewed stream **and force an immediate keyframe** so the surviving viewer re-syncs), add an AC, and extend Test 5 to assert a keyframe is forced when a viewed stream's cache is evicted.

---

### MINOR Findings

#### [MIN-001] Stale `displayCapture` grant in the C-2 disposition row contradicts FR-016's `audioCapture`-only grant

- **Lens**: Inconsistency
- **Affected section**: C-2 disposition (line 48) vs FR-016 (line 751), F-09 (line 35).
- **Description**: The C-2 row still specifies `Browser.grantPermissions({origin, permissions:['displayCapture']})` (a **video** grant). After R4, video capture is CDP `Page.startScreencast` with no `getUserMedia`, and FR-016 now grants only `audioCapture`. F-09 claimed "every `getDisplayMedia` reference scoped to audio-only," but the `displayCapture` permission grant survived in the R3 history row.
- **Recommendation**: Update line 48 to `audioCapture` (or annotate the row as superseded by R4/FR-016) so no surface still says the encoder page is granted display capture.

#### [MIN-002] Zero-length ingest chunk rejection appears in DS-2 but no FR mandates it

- **Lens**: Incompleteness
- **Affected section**: DS-2 row `len=0 empty payload (reject)` (line 693); FR-014 (line 749).
- **Description**: FR-014 covers only the **oversize** bound (reject + step-down). The empty/zero-length chunk case is asserted in the dataset ("reject") but backed by no requirement, so an implementer could legitimately accept a 0-byte chunk and still satisfy every FR.
- **Recommendation**: Extend FR-014 (or add an AC) to require rejecting zero-length / malformed-envelope ingest chunks, and point Test 10/13 at that row.

#### [MIN-003] The F-02 sequencing constraint (don't remove `browser_screencast` before the video path is reachable) has no enforcing test

- **Lens**: Inoperability, Incompleteness
- **Affected section**: Regression req #5 (line 729); FR-010 (line 745); `BrowserScreencastFrame` REMOVE row (line 135).
- **Description**: The spec states as a MUST that removing the sole live-view transport must not precede video reachability — but only in prose. Nothing in the TDD plan or CI gate asserts the ordering, so a wave could delete `browser_screencast` (and the SPA consumer at `browserLiveWs.ts:147`) before the video path lands, leaving **every** install (video-capable included, until the video path is complete) with no live view.
- **Recommendation**: Add a taskify sequencing dependency (video-path-reachable task precedes the `browser_screencast`-removal task) and/or a seam test asserting live frames are served on video-capable installs at the moment of removal.

#### [MIN-004] SC-013 A/V-skew bound cites "final bound from Gate 0," but Gate 0 measured only fps

- **Lens**: Ambiguity
- **Affected section**: SC-013 (line 775); US-4/AC-3 (line 209).
- **Description**: F-04/F-07 established Gate 0 measured **only** fps (30). SC-013's "≤ 200 ms p95 (final bound from Gate 0)" anchors to a Gate-0 number that does not exist, the same unfilled-measurement problem F-07 flagged for SC-001a/SC-002 but did **not** carry to SC-013.
- **Recommendation**: Re-scope SC-013 like SC-001a/002 — mark 200 ms provisional and name the A/V-skew measurement as a first-wave task, or drop the "from Gate 0" anchor.

#### [MIN-005] "Token never in `/json`" is presented as confidentiality but is not the actual protection

- **Lens**: Ambiguity, Incorrectness
- **Affected section**: FR-013 (line 748); EC-3 (line 176); F-01 (line 27).
- **Description**: The spec repeatedly cites "token never in `/json`" as part of the confidentiality story. `/json` only lists **targets**; it never contained page-script variables, so "not in /json" is trivially true regardless of design. Any CDP client that can dial 9223 reads the token via `Runtime.evaluate` on the encoder target **whether or not** it is in /json. The real (and only) protection is "cannot reach 9223" — which is precisely what CRIT-001 shows is absent on < 5.19.
- **Recommendation**: Drop "not in /json" as a confidentiality claim (keep it only as a hardening note) and state the sole protection is unreachability of 9223 by non-gateway processes — then confront CRIT-001 honestly.

---

### Observations

#### [OBS-001] The epoch protocol is complexity in search of a problem

- **Lens**: Overcomplexity (CPX-09)
- **Affected section**: FR-013 epoch mechanism.
- **Suggestion**: If the token is confined, a "concurrent duplicate" can only be a self-inflicted double-connect, for which "newest connection wins, close the older" is sufficient and needs no monotonic-ordinal concept. Removing the epoch (per CRIT-002's recommended minimal design) deletes a distributed-ordering concept and the reasoning burden it carries.

#### [OBS-002] The spec is now dominated by revision-disposition history; the current normative requirements are hard to extract

- **Lens**: Overcomplexity
- **Affected section**: whole document (R1–R5, C-1..C-3, M-1..M-11, P-1..P-7, F-01..F-11, EC/SC/AW cross-refs).
- **Suggestion**: Move the R1–R4 disposition tables to an appendix and lead with a clean, self-contained normative section (FRs, SCs, Gate-0 status, Platform Matrix). An implementer reading cold currently has to reconstruct "what is true now" from five layers of "what changed."

---

## Structural Integrity (Plan-Spec Format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1..US-11 each carry ACs. |
| Every acceptance scenario has BDD scenarios | PASS | Outlines + scenarios cover the ACs; US-7/AC-2 folded into the WSS scenario. |
| Every BDD scenario has `Traces to:` reference | PASS | All 34 scenarios + 3 outlines carry `Traces to:`. |
| Every BDD scenario has a test in TDD plan | PASS | Mapped via Traceability Matrix + TDD plan (Tests 1-32). |
| Every FR appears in traceability matrix | PASS | FR-001..FR-024 all present. |
| Every BDD scenario in traceability matrix | PARTIAL | Matrix is FR→BDD→Test; FR-019/FR-024 have "(ops)"/"(audit)" instead of a user story — acceptable but leaves two FRs without a US anchor. |
| Test datasets cover boundaries/edges/errors | PASS | DS-1..5 include seq/ts max, oversize, empty, mis-scoped, LRU, platform matrix. |
| Regression impact addressed | PASS | Dedicated Regression Test Requirements section. |
| Success criteria are measurable | **FAIL** | SC-001a, SC-002, SC-013, and the SC-016 min-spec **fps** are explicitly unmeasured/provisional; SC-003's shipping-config number is pending (MAJ-001). Several "final bound from Gate 0" anchors reference numbers Gate 0 did not produce (MIN-004). |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Kernel-degradation security | No test runs EC-3/Test 30 on a below-Landlock-ABI-v4 kernel; the token-confinement claim is only exercised where it happens to hold. | CRIT-001, EC-3, SC-017 |
| Concurrency (ingest race) | Test 29 asserts epoch ordering but cannot distinguish legitimate-reconnect from attacker-duplicate because the mechanism doesn't (CRIT-002). | US-9/AC-2 |
| Origin isolation (audio) | No test drives an agent-controlled tab to the encoder-page origin and attempts getUserMedia(audio). | MAJ-003, SC-010 |
| Cache eviction under load | Test 5 asserts "total < ceiling" but not "viewer of an evicted stream re-syncs via forced keyframe." | MAJ-005 |
| Removal sequencing | No test asserts live view stays served on video-capable installs at the moment `browser_screencast` is removed. | MIN-003 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| DS-4 (ingest auth) | Row where two connections race with the **same** gateway-minted epoch state, and a below-ABI-v4-kernel co-tenant read attempt | Add rows once CRIT-001/CRIT-002 mechanisms are re-specified. |
| DS-2 (chunks) | Malformed envelope (truncated header, `len` > payload) beyond the empty/oversize rows | Add a malformed-header reject row backing the new FR-014 clause (MIN-002). |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Ingest endpoint (loopback WS + capability token) | risk | risk | ok | risk | ok | risk | Token confinement fails on Linux < 5.19 (CRIT-001); epoch discriminator doesn't reject duplicates (CRIT-002) → frame injection / stream hijack. Audit on rejection (FR-024) ok. |
| CDP transport (fixed port 9223 + "process isolation") | risk | ok | ok | risk | ok | risk | Confidentiality = Landlock NET_CONNECT_TCP (ABI v4 / kernel 5.19+ only); absent below that. |
| Encoder page (audio getUserMedia, origin-scoped grant) | risk | ok | ok | risk | ok | risk | Origin-scoping defeatable if agent tab reaches the encoder origin (MAJ-003). Video path structurally isolated (EC-2) — ok. |
| Xvfb sidecar | ok | ok | ok | ok | risk | ok | Failure → unavailable state; supervised restart. DoS bound by supervisor backoff. |
| PulseAudio sidecar | ok | ok | ok | ok | ok | ok | Best-effort; never blocks Chrome/video. Adequately handled. |
| Viewer WS (attach authz, GOP replay) | ok | ok | ok | ok | risk | ok | FR-015/Test 27 gate authz before replay — ok. Slow-viewer drop isolated (FR-004). LRU eviction of a viewed stream unhandled (MAJ-005). |
| CfT installer (dual-download, integrity-verified) | ok | ok | ok | ok | ok | ok | Per-build hash/signature verify; one-way-door mitigated by post-Gate-0 flip. |

**Legend**: risk = identified threat not fully mitigated in spec; ok = adequately addressed or not applicable.

---

## Unasked Questions

1. **What is the minimum Landlock ABI / kernel version for a video-capable classification?** The token-confinement story requires ABI v4 (5.19+); nothing in the spec states this, and video-capability is decided purely on Xvfb/PulseAudio/full-Chrome presence.
2. **Who generates the ingest connection epoch, and what binds it to the one legitimate encoder page** such that an attacker holding the token cannot present a higher epoch and win?
3. **Can an agent-controlled tab be navigated to the encoder-page origin?** If yes, the origin-scoped audio grant is inherited by the agent.
4. **Is `/taskify` permitted to run before the min-spec EC-1 re-run and min-spec publication?** SC-016 and the Handoff/Gate-0 text disagree.
5. **What happens to a viewer whose stream's GOP cache is LRU-evicted mid-replay** — forced keyframe, or silent starvation?
6. **Where are NFR-1/2/3 defined,** and is iPad decode (NFR-1) a MUST or a SHOULD for release?
7. **On a below-ABI-v4 kernel, does a rejected/failed video-capability classification also disable the encoder page and 9223-delivered token entirely,** or does the token still get minted with no confinement?
8. **Is there a bound on kill-switch teardown latency** (FR-020 tears down active streams — within what window, observably)?

---

## Verdict Rationale

**BLOCK.** Two of R5's own new dispositions fail on their own terms. CRIT-001: the sole confidentiality mechanism for the ingest token (F-01/EC-3/FR-013/SC-017) is the Landlock connect allow-list, which the code shows requires kernel 5.19+ (Landlock ABI v4), while the spec gates video-capability on Xvfb/PulseAudio presence with no kernel floor — so SC-017's "100% / any co-tenant/agent process" is false across the entire Linux < 5.19 band the product supports, exposing live streams to frame injection and token theft by agent-run code. CRIT-002: the connection-epoch discriminator is internally contradictory ("gateway mints per connect" vs "connect carrying a … epoch") and under either reading fails to reject the concurrent duplicate it was added (M-5/F-06) to defeat. On top of these, MAJ-001/MAJ-002 show the real fps gate has been quietly relocated to after `/taskify`, re-opening the C-1 sequencing defect R3 claimed to close, and MAJ-003 leaves audio isolation defeatable. These must be resolved before taskify.

### Recommended Next Actions

- [ ] CRIT-001: add a Landlock-ABI-v4 / kernel-5.19 floor to video-capability **or** adopt a confinement mechanism independent of Landlock ABI v4; rewrite SC-017/EC-3/Test 30 to test the < 5.19 case.
- [ ] CRIT-002: re-specify the ingest single-active-holder rule (who issues the ordinal/nonce, what binds it); remove the contradictory "gateway mints per connect" clause; rewrite FR-013/DS-4/Test 29.
- [ ] MAJ-001 / MAJ-002: make the min-spec EC-1 re-run + min-spec publication hard Gate-0 blockers before `/taskify`; reconcile the SC-016 vs Handoff/Gate-0 wording.
- [ ] MAJ-003: bind the audio grant to an unguessable per-boot origin/path or forbid+test agent navigation to the encoder origin; add the getUserMedia-from-agent-origin denial test.
- [ ] MAJ-004: add a Non-Functional Requirements section defining NFR-1/2/3 (or replace the references).
- [ ] MAJ-005: specify eviction precedence + forced-keyframe re-sync for a viewed stream; extend Test 5.
- [ ] MIN-001..005: fix the stale `displayCapture` grant, add the zero-length reject FR, add a removal-sequencing gate, re-scope SC-013, and drop the "/json" confidentiality claim.
