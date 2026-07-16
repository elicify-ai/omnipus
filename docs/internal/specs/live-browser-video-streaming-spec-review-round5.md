# Adversarial Review: Live-Browser Video Streaming (WebCodecs relay) — Round 5 (grill of R6)

**Spec reviewed**: `docs/internal/specs/live-browser-video-streaming-spec.md` (Revision R6)
**Review date**: 2026-07-16
**Verdict**: BLOCK

## Executive Summary

R6's two headline fixes both introduce new, code-verifiable defects. The CRIT-001
reversal to a pure-Go CDP-over-pipe transport (a) removes the coordinator's *only*
atomic cross-process single-launch primitive — the `net.Listen` port bind — and
replaces it with a one-line "marker-file-only ownership" that has no atomicity
mechanism and silently drops the grill-M2 foreign-Chrome spoof guard; and (b) is
asserted "PASSED" at Gate 0 while the transport it depends on is unbuilt, unproven,
and rides on a chromedp seam that may not exist. Because Gate 0 is a hard
"before ANY implementation" gate, these are blocking.

| Severity | Count |
|----------|-------|
| CRITICAL | 2 |
| MAJOR | 3 |
| MINOR | 5 |
| OBSERVATION | 2 |
| **Total** | **12** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] "Marker-file-only ownership" removes the atomic single-launch primitive and the spoof guard with no replacement

- **Lens**: Incorrectness (COR-06 race) / Incompleteness (INC-05 concurrency)
- **Affected section**: R6 CRIT-001 disposition (spec lines 27, 150: "`sharedChromeCDPURL()`/fixed-port ownership removed → marker-file-only ownership"); ADR-044 §6.0.3-pt3.
- **Description**: The spec asserts the ADR-043 coordinator can move to "marker-file-only ownership" once the fixed port is gone. This is contradicted by the code the spec is modifying. The coordinator's single-launch guarantee is `preflightPort()` → `net.Listen("tcp", "127.0.0.1:9223")` (`pkg/tools/browser/coordinator.go:601,757-789`). **The successful TCP bind is the atomic cross-process mutual-exclusion primitive** — only one process on the host can hold it, so the "second launcher" deterministically loses and attaches to the winner's Chrome via `sharedChromeCDPURL()`. The ownership marker (`shared-chrome.pid`) is explicitly *not* the mutex — the code comment states the marker is only the identity/attribution layer ("the CDP endpoint carries no identity token, so the marker is the only way to tell our Chrome from an operator's unrelated one", coordinator.go:770-772; and coordinator.go:648-652 "the guarantee is the port-bind preflight, the marker is the identity layer"). There is **no `flock`/`O_EXCL`** on the marker; `c.mu` is a same-process goroutine mutex only. Removing the port removes: (1) the atomic mutex → two processes can both read "no live marker" and both launch (TOCTOU), and both then race on the shared `UserDataDir` → Chromium SingletonLock conflict / profile corruption; (2) the dialable endpoint → with `--remote-debugging-pipe` (inherited fd 3/4) a *second* process **cannot attach** to the first's Chrome at all (it can't inherit another process's fds), so the shared-Chrome-across-processes model that ADR-043 exists to provide is silently broken; (3) the grill-M2 launch-vs-spoof guard, which depends on the port being held + marker identity, has nothing to key on.
- **Impact**: On the *normal* single-gateway-per-host case with a clean start, it works. On process-restart overlap (graceful SIGTERM drain of the old gateway while the new one boots — the standard supervised-restart pattern) or any two-instance topology, the new gateway either wedges its browser subsystem, corrupts the profile dir, or double-launches Chrome + Xvfb. These are recurring production incidents for self-hosters, in a subsystem the team already invested two prior grills (ADR-043 D1, grill M2) to harden.
- **Recommendation**: Specify the atomic replacement explicitly, not "marker-file-only": e.g. an `O_EXCL`/`unix.Flock`-held lockfile (the repo already uses `unix.Flock`) that provides the same atomic check-and-own the port bind gave. State outright that with a pipe the shared Chrome is **per-process** (a second process cannot share it) and define the second-process behavior (refuse-and-error vs. own-Chrome-on-a-distinct-profile). Port the grill-M2 spoof guard to the new primitive or document, with evidence, why it is obsolete under a pipe.

---

#### [CRIT-002] Gate 0 asserts EC-3 "PASSED" while its transport is unbuilt, unproven, and possibly infeasible against chromedp

- **Lens**: Inconsistency (CON-02) / Infeasibility (FEA-01/FEA-04)
- **Affected section**: Gate 0 §STATUS (line 186 "EC-1, EC-2, EC-3 PASSED on `ci-omnipus`"); EC-3 row (line 192, "⚙️ EMPIRICAL … Pre-build empirical proof (Test 30)"); ADR-044 §6.0.3-pt2.
- **Description**: The STATUS banner declares EC-3 (CDP-token confidentiality) PASSED. This is untrue on two counts. First, the R4 "pass" it inherits was for the mechanism R6 *reversed and declared insecure* (keep-port-9223 + Landlock isolation) — so the "pass" certifies a mechanism no longer in the spec. Second, the *current* mechanism (pure-Go CDP-over-pipe) has never run: EC-3's own row calls Test 30 a "pre-build empirical test," and ADR §6.0.3-pt2 admits chromedp v0.15.1 has **no** pipe transport and reads a `ws://` URL from stdout, so Omnipus must build a bespoke transport that "wires Chrome's fd 3/4 pair … reusing `cdproto` … and feeds chromedp's higher layers." chromedp exposes no public seam to inject a custom `Conn` in place of its websocket `Target`/`Browser` layers; this is a fork-or-vendor-internals task, not a "small" one — yet Gate 0 is defined as the hard gate that runs *before* the CRITICAL-risk headful/installer build (line 879, "before ANY implementation").
- **Impact**: A reader — or `/taskify` consuming the Gate-0 outputs — will treat the security foundation as validated and proceed to the headful switch, the installer-default flip, and the `browser_screencast` removal. If the pure-Go pipe transport then proves infeasible against chromedp's internals, the epic's entire confidentiality story collapses *after* the highest-risk, hardest-to-reverse changes are already built. The fail-branch ("escalate to netns / re-open ADR §6.6") exists but is worthless if the gate that would trigger it is pre-marked "PASSED."
- **Recommendation**: Change EC-3's status from PASSED to **OPEN — blocked on the pure-Go CDP-over-pipe transport spike + Test 30**, and correct the STATUS banner to "EC-1/EC-2 passed; EC-3 pending transport build; EC-4 deferred." Make the transport spike a Gate-0 exit criterion in its own right (prove chromedp's higher layers can be driven over the pipe `Conn`) so the gate genuinely precedes the headful build it is supposed to protect.

---

### MAJOR Findings

#### [MAJ-001] The pipe transport becomes the CDP path for 100% of managed browsing, but is rated only as a "token-confidentiality" item

- **Lens**: Inoperability (OPS-06) / Incorrectness (COR-05)
- **Affected section**: Symbols table `managedExecAllocatorOpts` row (line 139) and `browserDebugPort → REMOVED` row (line 150); Impact Assessment "CDP token confidentiality" row (line 163, rated "HIGH (security)").
- **Description**: `managedExecAllocatorOpts` is verified as "Shared by the coordinator's launch path and the manager's no-coordinator fallback" (`exec_resolver.go:27-28`) — i.e., *every* managed Chrome launch. Switching it to `--remote-debugging-pipe` routes **all** CDP traffic — agent `browser.navigate`, take-the-wheel `Input.dispatch`, screenshots, tab management, not just live-view screencast — through the new, unbuilt, security-critical pure-Go transport. The spec files this under "CDP token confidentiality (HIGH security)" and frames the risk as token theft. The actual blast radius is: a bug in the in-house transport breaks *all agent browsing on every video-capable install*, and there is no longer a websocket reconnect path (a broken pipe = relaunch Chrome).
- **Impact**: The risk register under-states the highest-coupling change in the epic. Reviewers and implementers will scope transport testing to security, not to full browsing correctness/stability, and the regression suite (which today targets the headless→headful switch) will not gate the transport swap itself.
- **Recommendation**: Add an explicit row/finding rating the transport swap CRITICAL-for-all-browsing (co-equal with the headful switch), and add a regression gate: the full `pkg/tools/browser` navigate/input/screenshot suites MUST pass over the pipe transport before the port is removed. Note the loss of the ws-reconnect path and specify pipe-break recovery (relaunch).

---

#### [MAJ-002] SC-001a and SC-002 are labeled both "pre-`/taskify` gate" and "first-wave measurement task" — the exact contradiction round-4 raised for EC-1

- **Lens**: Inconsistency (CON-03)
- **Affected section**: Handoff (line 6, "Pre-`/taskify` gates: … SC-001a cold-start, SC-002 glass-to-glass") vs. SC-001a (line 784, "a **named first-wave measurement task**"), SC-002 (line 786, "a **named first-wave measurement task**"), and AW-4 (line 841, "**Named first-wave measurement tasks**").
- **Description**: Round-4's MAJ-001/002 flagged that deferring EC-1's min-spec re-run to "first-wave" contradicted the "before `/taskify`" gate; R6 fixed that *for EC-1* (now unambiguously pre-`/taskify`) but left the identical contradiction for the cold-start (SC-001a) and glass-to-glass (SC-002) measurements. The Handoff says they gate before `/taskify`; their own SC text and AW-4 say they are first-wave tasks (i.e., after `/taskify`). "First wave" is after decomposition; "pre-`/taskify`" is before it. They cannot be both.
- **Impact**: `/taskify` cannot tell whether to block on these numbers or emit them as tasks. Either the epic is decomposed on unmeasured budgets it claimed to gate on, or the gate stalls waiting for tasks that don't exist yet.
- **Recommendation**: Pick one side for SC-001a and SC-002 (recommend: measured *with* the EC-1 min-spec re-run as one pre-`/taskify` measurement pass, since all three want the same min-spec rig) and make Handoff, the SC bodies, and AW-4 agree verbatim.

---

#### [MAJ-003] Audio ships as a P1 capability but has no Gate-0 exit criterion and an unmeasurable acceptance bound

- **Lens**: Incompleteness (INC-01) / Infeasibility (FEA-04)
- **Affected section**: US-4 (P1, "proven"); SC-013 (line 797, "provisional bound; the real number is measured by a **pending AUDIO Gate-0 measurement when audio lands** … no A/V-skew exit criterion exists yet"); Gate 0 table (only EC-1..EC-4, none for audio).
- **Description**: US-4 is a shipped P1 capability with its own sidecar, yet SC-013's A/V-skew bound (≤ 200 ms) is admitted to reference a Gate-0 audio measurement that does not exist (Gate 0 defines fps, isolation, CDP-token, iPad decode — no audio EC). The spec's own governing rule is "integrated fps, isolation, CDP-token, iPad decode are all confirmed at Gate 0 before build — none is assumed into implementation" (line 870); audio's core acceptance number is exactly such an assumption.
- **Impact**: US-4/AC-3 and SC-013 are untestable as written — Test 26 asserts "A/V skew within the documented bound," but the bound is a placeholder pointing at a non-existent gate. Audio can ship "green" against an undefined target.
- **Recommendation**: Either (a) add an audio exit criterion (measure real skew on the full Xvfb+PulseAudio+encoder-page stack) and make SC-013/Test 26 cite it, or (b) explicitly de-scope the v1 audio SC to a qualitative bar ("audible and roughly A/V-synced, no numeric skew target") and remove the phantom "pending AUDIO Gate-0" reference.

---

### MINOR Findings

#### [MIN-001] The 9223 removal is under-enumerated — it misses the `BindPortRules` entry and mis-locates the file

- **Lens**: Incompleteness / Incorrectness (COR-05)
- **Affected section**: Symbols table (line 150) and R6 CRIT-001 disposition (line 27): "the Landlock 9223 connect allow-list (`sandbox_apply.go:419`) + `checkDebugPortAvailable` are removed."
- **Description**: Verified: `browser.DebugPort` is added to `ConnectPortRules` at `pkg/gateway/sandbox_apply.go:419` **and** to `bindPorts`/`BindPortRules` at `pkg/gateway/sandbox_apply.go:388`. Removing the port must drop *both* rules; the spec names only the connect rule. Also the citation is bare `sandbox_apply.go:419` while the file is `pkg/gateway/sandbox_apply.go` (readers will look in `pkg/sandbox/`, where it does not exist).
- **Recommendation**: Enumerate both removals (`bindPorts` append at :388 and `ConnectPortRules` append at :419) and give the full path `pkg/gateway/sandbox_apply.go`.

#### [MIN-002] Encoder-page relaunch (CRIT-002 fix) doesn't re-issue the target-scoped `audioCapture` grant

- **Lens**: Incompleteness (INC-03)
- **Affected section**: FR-013 (relaunch on ingest drop) vs. FR-016 ("the `audioCapture` grant bound to the specific encoder-page **target**, not merely its origin").
- **Description**: FR-016 deliberately binds audio consent to the specific encoder-page *target*. FR-013's fix relaunches that target (fresh page, fresh token) on every transient drop. A fresh target has no grant, so audio dies on each relaunch unless the gateway re-issues `Browser.grantPermissions` to the new target — which FR-013 never says it does.
- **Recommendation**: State in FR-013 that relaunch re-mints the token *and* re-issues the target-scoped `audioCapture` grant to the new encoder-page target; note that audio is briefly interrupted across a relaunch.

#### [MIN-003] FR-003 eviction rule is unsatisfiable in the all-viewed / large-keyframe corner

- **Lens**: Incompleteness (INC-05)
- **Affected section**: FR-003 / MAJ-005 ("Eviction MUST NOT drop a stream that has a live or currently-attaching viewer … evicts idle streams first"); §Go Memory Budget (assumes keyframe ≤ 150 KB, "Gate-0-unknown" distribution).
- **Description**: When all streams at the aggregate ceiling have viewers and keyframes exceed the assumed 150 KB (the distribution is explicitly Gate-0-unknown), "never evict a viewed stream" and "hard aggregate ceiling" cannot both hold — there is nothing idle to evict and no viewed keyframe may be dropped. AW-3 lowers `N` (deltas), which doesn't help a keyframe-dominated overflow.
- **Recommendation**: Define the tie-break for the all-viewed overflow: reject/deny the newest stream to the unavailable state, or force a resolution/bitrate step-down on the largest stream, or raise the ceiling within the ~4.7 MB of headroom under the 10 MB budget. Make it explicit rather than leaving two MUSTs in conflict.

#### [MIN-004] Single pipe `Conn` must multiplex all concurrent CDP sessions; the transport's session model is unspecified

- **Lens**: Incompleteness (INC-05) / Infeasibility (FEA-01)
- **Affected section**: FR-013 / ADR §6.0.3-pt2 ("reuse `cdproto` over a pipe `Conn`").
- **Description**: chromedp today gives each allocator its own websocket. Over one inherited fd pair, the new transport must multiplex every concurrent CDP session — each agent tab, the gateway-driven `Page.startScreencast`, encoder-page control, and take-the-wheel `Input.dispatch` — on a single stream. Session routing, ordering, and head-of-line-blocking behavior are unspecified and are correctness-critical for interactive input latency.
- **Recommendation**: Specify the transport's session-multiplexing contract (per-`sessionId` routing over the single `Conn`, write serialization, backpressure) and add a concurrency test alongside Test 30.

#### [MIN-005] Superseded ADR reasoning that the escalation fallback rests on is code-false

- **Lens**: Incorrectness (COR-05)
- **Affected section**: ADR-044 §6.0.2-pt3/pt4 (cited authoritative), "agent tool processes are Landlock-sandboxed **without 9223 in their connect allow-list**"; escalation branch (EC-3 / spec line 43) "re-open ADR §6.6 / keep 9223 + isolation."
- **Description**: Verified false: `pkg/gateway/sandbox_apply.go:419` explicitly *adds* `browser.DebugPort` to `ConnectPortRules` (and :388 to bind), so on ABI v4+ 9223 **is** in the allow-list — the opposite of the claim. R6's pipe reversal makes this moot for the primary path, but the documented escalation fallback ("keep 9223 + process isolation") is built on this inverted premise, so if the pipe spike fails, the team could fall back to a mechanism that never provided the claimed isolation.
- **Recommendation**: Correct or strike the "9223 not in the allow-list" statements in the superseded ADR sections, and remove "keep 9223 + isolation" from the EC-3 escalation options (leave only netns / mechanism revisit).

---

### Observations

#### [OBS-001] The bespoke pipe transport vs. an ephemeral loopback port is not weighed explicitly

- **Lens**: Overcomplexity (CPX-07) / Infeasibility
- **Affected section**: FR-013; ADR §6.0.3.
- **Suggestion**: `--remote-debugging-port=0` (ephemeral loopback, read from `DevToolsActivePort`) is natively supported by chromedp and needs no new transport; R3 originally proposed it. R6 partly rejects it citing the Landlock-allow-list break — which is being removed anyway (MIN-001/MIN-005). The pipe's *genuine* advantage is kernel-independent zero-TCP-surface against a local co-tenant. State that as the sole justification and weigh it against the cost/risk of building and maintaining security-critical CDP transport code in the single binary; if the spike (CRIT-002) is hard, the ephemeral port is a lower-risk interim that still removes the fixed port.

#### [OBS-002] Revision scar tissue buries the live requirement

- **Lens**: Ambiguity (readability)
- **Affected section**: Whole document (~920 lines; the majority is R1–R6 disposition tables with multi-hop "supersedes X" chains, several of which the current text still parrots — e.g., "keep 9223" reasoning).
- **Suggestion**: Snapshot the R6 live spec (overview, FRs, SCs, Gate 0, BDD, tests) as the primary artifact and move R1–R5 dispositions to a `-history.md` appendix, so `/taskify` and implementers read only current truth and cannot pick up a superseded claim.

---

## Structural Integrity (Plan-Spec Format)

| Check | Result | Notes |
|-------|--------|-------|
| Every user story has acceptance scenarios | PASS | US-1..US-11 all carry ACs. |
| Every acceptance scenario has BDD scenarios | PASS | 34 scenarios + 3 outlines cover the ACs. |
| Every BDD scenario has `Traces to:` reference | PASS | All present. |
| Every BDD scenario has a test in TDD plan | PASS | Tests 1–32 map; E2E 23–26 cover the happy/audio paths. |
| Every FR appears in traceability matrix | PASS | FR-001..FR-024 all present. |
| Every BDD scenario in traceability matrix | PARTIAL | Matrix is FR-keyed; BDD coverage is via `Traces to:` US links, not by scenario name — indirect but complete. |
| Test datasets cover boundaries/edges/errors | PASS | DS-1..DS-5; u32/u64 boundary rows correct (4294967295 / 18446744073709551615); oversize/empty reject rows present. |
| Regression impact addressed | PASS | Extensive §Regression Test Requirements incl. the F-02 live-view-continuity sequencing MUST. |
| Success criteria are measurable | PARTIAL | SC-001a/SC-002/SC-013/SC-016(fps) carry provisional or absent numbers pending measurement; SC-013 references a non-existent audio gate (MAJ-003). |

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Concurrency (cross-process) | No test that two gateway processes / a restart-overlap cannot double-launch Chrome or corrupt the shared profile once the atomic port bind is gone (CRIT-001). | US-8, US-10; coordinator invariant |
| Transport correctness | Test 30 proves token *confidentiality* over the pipe but nothing proves the pipe transport drives full browsing (navigate/input/screenshot) or multiplexes concurrent sessions (MAJ-001, MIN-004). | US-2, US-8 regression |
| Audio acceptance | Test 26 asserts skew "within the documented bound" but no bound/gate exists (MAJ-003). | US-4 |
| Encoder-page relaunch audio | No test that audio consent survives / is re-granted after an ingest-drop relaunch (MIN-002). | US-9/AC-2, US-4 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| §Go Memory Budget | All-viewed / keyframe > 150 KB overflow row | Add the corner where no stream is evictable and keyframes exceed the assumed size (MIN-003). |
| DS-4 | Relaunch re-grant of `audioCapture` | Add a row asserting the relaunched target holds a fresh grant (MIN-002). |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Ingest endpoint (`/api/v1/browser/capture-ingest`) | ok | ok | ok | ok | ok | ok | Loopback-only bind (FR-012), per-stream token (FR-013), ≥2 MB bound + reject-not-fragment (FR-014), audit on every reject (FR-024). Well covered. |
| CDP transport (pipe) | ok | ok | ok | **risk** | risk | ok | Confidentiality claimed via no-TCP-surface, but the transport is unbuilt/unproven (CRIT-002); superseded "9223 not allow-listed" claim is code-false (MIN-005). |
| Shared-Chrome coordinator | **risk** | **risk** | risk | ok | risk | ok | Spoof guard (grill-M2) and atomic single-launch dropped with the port; marker file is not atomic and is forgeable (CRIT-001). |
| Encoder page (audio consent) | ok | ok | ok | risk | ok | risk | Target-scoped grant + unguessable origin (FR-016) good, but relaunch re-grant unspecified (MIN-002). |
| Managed Chrome (headful capture) | ok | ok | ok | ok | risk | ok | All browsing now depends on the new transport; DoS/availability blast radius under-rated (MAJ-001). |
| Viewer WS | ok | ok | ok | ok | ok | ok | AuthFrame unchanged; viewer-attach authz before GOP replay (FR-015, Test 27). |

**Legend**: risk = identified threat not mitigated in spec, ok = adequately addressed or not applicable.

---

## Unasked Questions

1. With `--remote-debugging-pipe`, how do two Omnipus processes on one host coordinate — is shared Chrome now strictly per-process, and if so what happens on supervised restart overlap? (CRIT-001)
2. What is the concrete atomic primitive that replaces the `net.Listen(":9223")` bind for single-launch mutual exclusion — `flock`, `O_EXCL` lockfile, something else? (CRIT-001)
3. Has anyone demonstrated that chromedp's `Browser`/`Target` layers can be driven over a custom pipe `Conn` at all, or is this fork-the-library work? Until that spike exists, on what basis is EC-3 "PASSED"? (CRIT-002)
4. When the encoder page is relaunched on an ingest drop, is `audioCapture` re-granted to the new target, and is the audio gap acceptable? (MIN-002)
5. Are SC-001a (cold-start) and SC-002 (glass-to-glass) measured before `/taskify` or during the first wave — and against which rig? (MAJ-002)
6. Does audio have a measured acceptance bound for v1, or does it ship against a qualitative bar? (MAJ-003)
7. Which of the two `browser.DebugPort` sandbox rules (bind at :388, connect at :419) are removed, and is any residual browser-tool reachability affected? (MIN-001)
8. When all live streams sit at the aggregate ceiling and keyframes exceed the budgeted size, which MUST wins — the no-evict-viewed-stream rule or the hard ceiling? (MIN-003)

---

## Verdict Rationale

BLOCK. R6 was produced to resolve two round-4 CRITICALs, and both of its resolutions
introduce fresh, code-verified defects. CRIT-001: the reversal to a pipe transport
removes the coordinator's atomic single-launch primitive and grill-M2 spoof guard —
guards two prior grills built deliberately — and replaces them with a non-atomic pid
file, breaking the cross-process shared-Chrome invariant on restart overlap. CRIT-002:
the Gate-0 STATUS certifies EC-3 "PASSED" for a mechanism the spec itself reversed,
while the pure-Go pipe transport the current mechanism depends on is unbuilt, unproven,
and may be infeasible against chromedp's internals — yet Gate 0 is the hard gate that is
supposed to clear *before* the epic's highest-risk changes. These must be resolved
before the spec proceeds. The three MAJORs (transport blast-radius mis-rating, the
SC-001a/SC-002 pre-`/taskify` contradiction inherited from the round-4 fix, and audio's
missing gate) and the MINORs are all addressable in the same revision.

### Recommended Next Actions

- [ ] Specify the atomic single-launch replacement and the per-process shared-Chrome model; re-home or retire the grill-M2 spoof guard (CRIT-001).
- [ ] Reset EC-3 to OPEN, add a chromedp-over-pipe feasibility spike as a Gate-0 exit criterion, and correct the STATUS banner (CRIT-002).
- [ ] Re-rate the transport swap as CRITICAL-for-all-browsing and gate it on the full browser regression suite over the pipe (MAJ-001).
- [ ] Resolve the SC-001a/SC-002 pre-`/taskify`-vs-first-wave contradiction consistently across Handoff, the SC bodies, and AW-4 (MAJ-002).
- [ ] Give audio a real gate or a qualitative v1 bar; stop citing a non-existent audio Gate-0 (MAJ-003).
- [ ] Enumerate both DebugPort sandbox-rule removals with the correct path; fix the encoder-relaunch audio re-grant, the eviction corner, the pipe session-multiplexing contract, and the code-false superseded reasoning (MIN-001..005).
