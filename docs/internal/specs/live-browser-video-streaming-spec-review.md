# Grill-Spec Review — Live-Browser Video Streaming (WebCodecs relay), Revision R1

- **Spec reviewed:** `docs/internal/specs/live-browser-video-streaming-spec.md` (R1, post first grill)
- **Source ADR:** `docs/internal/architecture/ADR-044-live-browser-video-streaming.md`
- **Mode:** `plan-spec` (BDD + FR/SC + traceability present)
- **Reviewer stance:** adversarial, read-only. R1 already dispositioned 2 CRIT + 7 MAJOR + 9 MINOR from the prior pass; this pass verifies those fixes are *real* and hunts for what R1 introduced or still misses.

---

## Executive Summary

R1's structural work is genuinely improved — the security user story (US-9), the cold/warm split, the aggregate GOP ceiling, and the no-JPEG degradation are all now first-class. Several claimed fixes, however, are asserted rather than specified, and two of them reopen the exact security surface R1 says it closed. The keyframe-fragmentation "scheme" (MAJ-002 fix) has no wire fields and no reassembly bound; the ingest capability token (CRIT-001 fix) is delivered over the same loopback the spec's own threat model declares untrusted; and the `< 10 MB` footprint criterion is asserted for a multi-viewer video relay without accounting for per-viewer send buffers.

**Findings:** 2 CRITICAL, 9 MAJOR, 9 MINOR, 4 OBSERVATION.

**Verdict: BLOCK** (two CRITICAL findings).

---

## Findings Table

| ID | Sev | Lens | Section | Finding | Fix |
|----|-----|------|---------|---------|-----|
| C-1 | CRITICAL | Insecurity / Incompleteness | FR-014, Edge "keyframe exceeds bound", DS-2 | Keyframe reassembly buffer is unbounded. FR-003's aggregate ceiling covers the *GOP cache*, not the *in-flight partial keyframe* being reassembled from fragments. A capture page (buggy encoder, or the "encoder can't keep up" path) or a token-bearing co-tenant can stream fragments that never complete → unbounded relay allocation → OOM, violating NFR-3. This is missing error handling for a likely failure. | Add an FR: bound the partial-reassembly buffer per stream (hard cap on total fragmented-keyframe bytes AND max fragments AND a reassembly-completion timeout); on breach, drop the partial keyframe and force a fresh keyframe, count it in FR-019 metrics, and include the buffer in the NFR-3/SC-011 budget. |
| C-2 | CRITICAL | Insecurity (Spoofing/EoP) | FR-012, FR-013, AW-7, §6.6 | The ingest capability token is delivered to the capture page **over CDP on the fixed loopback debug port**, and relayed frames traverse loopback — yet the spec's own premise (ADR §6.6: "loopback is not a trust boundary on the pod … multiple agents, the preview listener, and user-served pages share it") makes that channel untrusted. CDP on the fixed `DebugPort` is unauthenticated by default; a co-tenant process on the shared loopback can enumerate targets, read the injected token before it is consumed, and inject frames — reopening CRIT-001. The token's confidentiality rests on the very boundary it was introduced to replace. | Specify the CDP-port protection explicitly (bind CDP debug port to loopback AND gate it — per-launch ephemeral token / abstract-namespace socket / firewalled port), OR state and justify that co-tenant OS processes are out of scope (only agent-browsed *browser content* is in scope) and cite why browser content cannot reach the CDP `/json` endpoints. Add a test asserting the ingest token cannot be recovered from CDP by a non-capture client. |
| M-1 | MAJOR | Ambiguity / Overcomplexity | FR-014, DS-2, §6.3 wire envelope | The fragmentation "scheme" (MAJ-002 fix) is asserted but has **no wire representation**. The envelope `{seq:u32, ts:u48, key:u8, len:u32, payload}` has no fragment-index, total-fragments, or more-fragments flag; DS-2 shows the 1 MB keyframe as a single row with `len=1048576`, so the relay cannot tell fragment 2-of-4 of `seq N` from a new chunk. Since FR-010 mandates contract-before-code, an unspecified framing blocks implementation. | Either (a) drop fragmentation entirely and set the ingest single-message bound to the max plausible keyframe with a hard cap (WS messages are already multi-MB-capable; the relay then stays a true byte relay, killing C-1's surface too), or (b) define explicit fragment fields in the AsyncAPI envelope (`frag_index`, `frag_count` or `last:u8`, correlation on `seq`) with ordering/dedup/timeout rules. Prefer (a). |
| M-2 | MAJOR | Insecurity / Feasibility | FR-012, US-7/AC-2, §Integration | "Loopback-only, not an external listener" (FR-012) is stated as a hard property with **no enforcement mechanism**, and it conflicts with "no new external listener port" (US-7/AC-2). If `capture-ingest` is registered on the main gateway mux and the gateway binds `0.0.0.0` (operator-configurable `gateway.host`), the endpoint is reachable on the external interface — it is *not* loopback-only. There is no existing "loopback-only endpoint without a new listener" pattern in the gateway (the preview listener is a separate port). | Specify the mechanism: a `RemoteAddr`-is-loopback check in the ingest handler (reject non-loopback before auth), and state it explicitly in FR-012. Add a test that a non-loopback source is rejected. Clarify the interaction with reverse-proxy deployments (the proxy must never forward to this path). |
| M-3 | MAJOR | Infeasibility / Incorrectness | NFR-3, SC-011, FR-003, FR-004 | SC-011 asserts "whole-feature steady-state RAM < 10 MB" but the budget accounts only for the GOP cache. It ignores per-viewer send-queue buffers (`browserWSSendCap` × encoded-chunk size × N viewers — 150 KB keyframes per DS-2), the partial-reassembly buffer (C-1), and multi-stream concurrency. A multi-viewer video relay buffering encoded frames can exceed 10 MB with a handful of viewers. The 10 MB figure is inherited from Constraint #3 (originally security-feature overhead), not derived for this workload. | Re-derive the budget: `GOP_ceiling + Σ(viewer_queue_depth × max_chunk) + reassembly_ceiling`. Either show it fits < 10 MB with the AW-5 sizing, or negotiate a realistic, workload-specific ceiling in the ADR and restate SC-011 against it. |
| M-4 | MAJOR | Inconsistency | FR-016, US-9/AC-4, AW-1, ADR G-1 | The named primary capture mechanism is self-contradictory with FR-016/US-9. The ADR's G-1 flags — `--use-fake-ui-for-media-stream` and `--auto-select-tab-capture-source-by-title` — are **process-global** (FR-016 forbids "process-global media auto-accept") and the second is **title-based** (US-9/AC-4 forbids binding by tab title). So the *only* FR-016/US-9-compliant mechanism is the extension + `chrome.tabCapture` route, which the spec treats as a fallback ("AW-1: use the extension route") and the ADR marks `[ASSUMPTION]` (unproven in headless). The spec/plan lead with a mechanism its own security requirements rule out. | State plainly that the extension `chrome.tabCapture` route is the baseline compliant mechanism (getDisplayMedia + auto-accept flags is non-compliant by FR-016/US-9), and that S-1 must prove the extension route in CfT-headless or the feature is blocked. Adjust effort estimates accordingly. |
| M-5 | MAJOR | Incompleteness | FR-013, US-9/AC-2, Edge "rapid reconnect flap" | Single-use ingest token vs. capture-page ingest reconnect. FR-013 makes the token single-use; the edge cases cover *viewer* reconnect flap but never the *ingest* (capture→gateway) leg. A transient ingest-WS drop (not a crash) spends the single-use token, so the capture page can never reconnect → the stream dies permanently on any loopback WS blip. The spec conflates "capture crash" (terminal, fine) with "ingest WS transient drop" (should recover). | Define ingest-leg reconnect: either the token is single-use *per stream lifecycle* (survives WS reconnect within the same stream, consumed only on stream end), or the gateway re-mints and re-delivers a token on capture-page reconnect. Add an edge case + test. |
| M-6 | MAJOR | Test coverage / Traceability | FR-015, Traceability Matrix, TDD Test 1 | FR-015 (viewer attach authorization MUST gate before any GOP replay — a security requirement) has no dedicated test. The matrix maps it to Test 1 (`TestIngest_RejectsUnauthenticated`), which is the *ingest* leg, not viewer attach. There is no test that an unauthorized viewer cannot pull cached GOP frames from a session it may not view. | Add a dedicated test: unauthorized viewer attach is rejected before GOP replay; add a matching SC. Re-map FR-015 in the matrix off Test 1. |
| M-7 | MAJOR | Incompleteness / Risk not surfaced | NFR-1, US-5, AW-1/AW-2, ADR G-2/G-3 | The originating purpose is smooth video on the operator's iPad Safari (NFR-1). If S-1 shows no H.264 *encode* in CfT-headless and S-2 shows no VP8 *decode* in Safari, the primary device gets the unavailable state — i.e., the person who requested the feature is told "needs a video-capable browser" on their own device, with JPEG now removed. This catastrophic outcome is buried in AW-2 ("if neither → US-5 for Safari") and never surfaced as a spec-level go/no-go gate. | Add an explicit decision gate: if the S-1 ∩ S-2 codec matrix leaves no working codec for iPad Safari, the feature fails NFR-1 for the primary device and the ADR must be revisited (JPEG-removal reconsidered or WebRTC/Option B escalated). Make this a named exit criterion, not a footnote. |
| M-8 | MAJOR | Inoperability | FR-020, §6.4, Non-Behaviors | The kill-switch (`browser_video_enabled=false`) forces the *unavailable* state, not JPEG — and the JPEG `browser_screencast` message is retained "for back-compat only" but is unreachable by any runtime path. So an operator hitting a video regression in production has no lever back to the working-but-laggy JPEG view; live view goes to "nothing." The retained JPEG wire message is dead weight (no code can ever select it). | Decide one way: either wire the kill-switch (or a distinct config) to fall back to the JPEG path as an operability escape hatch, or remove the JPEG message from the contract entirely (it is otherwise unreachable dead surface — see O-2). Do not leave a retained-but-unselectable pipeline. |
| M-9 | MAJOR | Test coverage / Traceability | FR-019, SC-012, Traceability Matrix | Observability is untested. The matrix maps FR-019 to "12 (metrics emit)", but Test 12 is a *contract round-trip* test (`TestAttachFrame_VideoCaps_Contract`) — it does not assert any metric is emitted. SC-012 ("all FR-019 metrics emitted and queryable") has no automated test; the six metrics (fps/bitrate/drop-rate/decode-error/restart/auth-reject) could silently be absent. | Add a metrics-emission test (assert each FR-019 counter/gauge is registered and increments under load), or explicitly downgrade SC-012 to a manual ops check and remove the false Test 12 mapping. |
| m-1 | MINOR | Incompleteness (Repudiation) | §Assumptions, FR-019, ADR §7 | ADR §7 requires "audit-log the stream lifecycle like other browser events," but the spec demotes audit to a bare assumption and provides no FR. Ingest-auth rejections (SC-009) and stream start/stop are security-relevant events with no audit-trail requirement — a repudiation gap on a new privileged entry point. | Add an FR requiring audit-log entries for stream lifecycle and every ingest-auth rejection; assert it in a test. |
| m-2 | MINOR | Ambiguity | FR-020, Edge "kill-switch on" | Kill-switch behavior on *in-flight* streams is unspecified. "All attaches get the unavailable state" covers new attaches; it does not say whether active streams are torn down or continue until detach. | State it: toggling `browser_video_enabled=false` tears down active streams to the unavailable state (or explicitly grandfathers them). Add to the edge case. |
| m-3 | MINOR | Structural (AC without scenario) | US-8/AC-2 | US-8/AC-2 ("headless-shell install: agents browse normally AND live view unavailable") has no BDD scenario; the only headless-shell scenario traces to US-5/AC-2 (unavailable state), not the "browse normally on boot" half. DS-5/regression covers boot+browse, but no BDD scenario asserts it. | Add a scenario for the "agents browse normally on a headless-shell boot" behavior, or fold it explicitly into the regression scenario's Then. |
| m-4 | MINOR | Structural (AC without scenario/test) | US-9/AC-4, FR-016 | US-9/AC-4 ("capture binds by an unguessable per-stream key, never the tab title") has no dedicated BDD scenario and no dedicated test — Test 14 asserts "no global flag," not the key-vs-title binding. Given M-4 (title-based flags exist), this is the assertion that most needs a test. | Add a scenario + test that capture selection uses the unguessable key and fails/refuses title-based selection. |
| m-5 | MINOR | Ambiguity / Feasibility | §6.3 envelope, DS-2 | `ts:u48` is a non-standard wire width. There is no native 48-bit integer; AsyncAPI/codegen and both language sides must agree on a 6-byte encoding, which is error-prone for a 2-byte saving over `u64`. | Use `u64` for the timestamp unless the 2 bytes are load-bearing; if `u48` stays, specify the exact byte layout and endianness in the contract and test the max-value boundary (DS-2 already has the max row). |
| m-6 | MINOR | Incompleteness (transport) | FR-017, browser_ws.go:359-374 | FR-017's opcode-tagged send queue must preserve the existing `nil`-on-`sendCh` ping sentinel (write pump line 371 sends a `PingMessage` for a `nil` item). The spec describes Binary-for-chunks / Text-for-JSON but never mentions the ping sentinel, which a naive opcode wrapper would break (keep-alive regression). | Note in FR-017 that the tagged queue item must still express the ping sentinel; add it to the WS-framing test (Test 11). |
| m-7 | MINOR | Ambiguity | SC-002 | "Scroll glass-to-glass ≤ 150 ms p50" does not define the two glass endpoints (agent-tab repaint → viewer canvas paint?). Two engineers will instrument different spans. It is also a placeholder (AW-6), but the *definition* should not wait for the number. | Define the measured span precisely (start event, end event, clock) independent of the numeric target. |
| m-8 | MINOR | Consistency (upstream doc) | Symbols table vs ADR §6.1 | The spec correctly cites `managedExecAllocatorOpts` at `exec_resolver.go:31` (verified: definition is there; `manager.go:580` and `coordinator.go:617` are call sites). The **ADR §6.1** still cites the wrong location (`manager.go:580`). Not a spec defect, but the authoritative source disagrees with the spec. | Fix the ADR §6.1 reference to `exec_resolver.go:31` so the two agree. |
| m-9 | MINOR | Measurability | SC-001a, SC-002, AW-5, AW-6 | Three of the load-bearing acceptance numbers (cold-start budget, glass-to-glass, GOP `N`/ceiling) are placeholders heading into `/taskify`. Acknowledged (AW-5/6), but a spec whose two headline performance SCs are unmeasured cannot be decomposed into testable tasks yet. | Gate `/taskify` on S-1 landing the three numbers; the spec already says this — make it a hard blocker in the summary, not a soft flag. |
| O-1 | OBSERVATION | Overcomplexity | OBS-001, §6.3 | `browser_stream_bitrate` is reserved in the contract but unimplemented in v1. Reserving an unused wire message is minor speculative surface; fine if truly cheap, but consider adding it in v1.1 alongside the ABR code rather than now. | Optional: defer the contract entry to v1.1. |
| O-2 | OBSERVATION | Overcomplexity | §6.4, M-8 | The retained JPEG `browser_screencast` path is unreachable (nothing selects it). If M-8 does not wire it as a fallback, it is dead wire surface plus dead SPA/Go code kept "for back-compat" with no consumer. | Remove it, or give it a runtime selector (see M-8). |
| O-3 | OBSERVATION | Insecurity (Info-disclosure) | US-5/AC-2 | The "build hint" in the unavailable-state message may fingerprint the install as `chrome-headless-shell` to any viewer. Low impact. | Keep the hint operator-facing (logs) rather than in the end-user panel string, or make it generic. |
| O-4 | OBSERVATION | Inoperability | FR-019, SC-012 | Metrics are listed but no alert thresholds, dashboard, or on-call runbook are specified. Acceptable for a spec, but the 3 AM question ("capture restart count spiking — now what?") is unanswered. | Add a one-line runbook pointer or alert thresholds when the metrics land. |

---

## Structural Integrity Results (plan-spec checks)

| Check | Result |
|-------|--------|
| Every user story has ≥1 acceptance scenario | PASS |
| Every AC has ≥1 BDD scenario | **FAIL** — US-8/AC-2 (m-3) and US-9/AC-4 (m-4) have no dedicated scenario |
| Every BDD scenario has `Traces to:` | PASS |
| Every BDD scenario has a corresponding TDD test | PARTIAL — US-9/AC-4 binding untested (m-4) |
| Every FR in the traceability matrix | PASS (FR-001..020 all present) |
| FR→test mappings are accurate | **FAIL** — FR-015→Test 1 and FR-019→Test 12 are mis-mapped (M-6, M-9) |
| Test datasets cover boundary/edge/error | PASS (DS-2 has u32/u48 max, empty-payload reject; DS-4 covers reuse/mis-scope) |
| Regression impact addressed | PASS (dedicated section + DS-5) |
| Success criteria measurable, no subjective language | PARTIAL — SC-001a/SC-002 are placeholders (m-9); SC-011 asserted without derivation (M-3) |

---

## Test Coverage Assessment

- **Negative paths:** strong on the ingest auth matrix (DS-4, Tests 1-2) and codec negotiation (DS-1, Test 9).
- **Gaps:** viewer-attach authorization before GOP replay (M-6, FR-015 untested); metrics emission (M-9, FR-019 untested); unguessable-key capture binding (m-4, US-9/AC-4 untested); ingest-leg reconnect (M-5, no test); reassembly buffer bound (C-1, no test — because the bound itself is unspecified).
- **Concurrency/DoS:** DS-3 covers per-viewer backpressure and aggregate LRU eviction well; it does **not** cover the partial-reassembly memory path (C-1) or send-queue memory under many viewers (M-3).
- **E2E:** appropriately deferred post-spike; audio E2E (Test 23) correctly gated on S-3.

---

## STRIDE Threat Summary

| Component | Threats identified | Spec coverage |
|-----------|--------------------|---------------|
| Ingest endpoint (`/api/v1/browser/capture-ingest`) | **Spoofing/EoP** — token stolen via unauth CDP on shared loopback (C-2); **Info-disclosure** — token confidentiality on untrusted loopback (C-2); **Tampering** — non-loopback source reaches endpoint if bound `0.0.0.0` (M-2); **DoS** — unbounded fragment reassembly (C-1) | Token model present but channel-confidentiality and loopback-enforcement gaps (C-2, M-2); DoS unbounded (C-1) |
| Capture mechanism | **EoP** — global auto-accept flags would let agent-browsed pages capture (FR-016); named primary mechanism is non-compliant (M-4) | FR-016 + Test 3/14 good; but M-4 mechanism contradiction unresolved |
| Viewer WS fan-out | **EoP** — unauthorized viewer pulls GOP cache (FR-015) | Required by FR-015 but untested (M-6) |
| Stream lifecycle | **Repudiation** — no audit trail for ingest rejections / stream start-stop (m-1) | Demoted to assumption; no FR |
| Managed-Chrome switch | **Tampering** — malicious/corrupt full-Chrome download | Covered — integrity-verify FR-009, DS-5 bad-hash row |

---

## Unasked Questions

1. Is the fixed CDP `DebugPort` reachable by other processes (or agent-browsed content) on the pod's shared loopback, and if so what stops them reading the injected ingest token? (C-2)
2. If keyframes are fragmented, what bounds a never-completed reassembly, and how are fragments correlated/ordered on a wire envelope that has no fragment fields? (C-1, M-1)
3. Why fragment at all instead of sizing the ingest message bound to the max keyframe? What breaks if the relay stays a true single-message byte relay? (M-1)
4. How is `capture-ingest` made loopback-only while adding no new listener and while `gateway.host` may be `0.0.0.0`? (M-2)
5. Show the arithmetic: GOP ceiling + Σ per-viewer send queues + reassembly buffer < 10 MB for the worst supported (streams × viewers). If it doesn't fit, what's the real budget? (M-3)
6. If S-1 finds no headless H.264 encode and S-2 no Safari VP8 decode, does the operator's iPad get *no live view*? Is that an acceptable ship state given it started this ADR? (M-7)
7. Does a transient ingest-WS drop kill the stream permanently under the single-use token? (M-5)
8. When the kill-switch flips, do in-flight streams tear down or continue? (m-2)
9. With JPEG removed as a live tier and the kill-switch producing "unavailable," what is the operator's runtime escape hatch when video regresses in production? (M-8)

---

## Verdict

**BLOCK** — 2 CRITICAL (C-1 unbounded reassembly / NFR-3 DoS; C-2 ingest-token confidentiality reopens CRIT-001), 9 MAJOR.

Review written to: `docs/internal/specs/live-browser-video-streaming-spec-review.md`

To address these findings, run:

```
/plan-spec --revise docs/internal/specs/live-browser-video-streaming-spec.md docs/internal/specs/live-browser-video-streaming-spec-review.md
```

Note: C-1, C-2, M-2, M-4, and M-7 touch the ADR's security/mechanism assumptions — the revision should reconcile ADR-044 §6.3/§6.6 and G-1/G-2 in the same pass (and fix the ADR §6.1 line ref, m-8), not just the spec.
