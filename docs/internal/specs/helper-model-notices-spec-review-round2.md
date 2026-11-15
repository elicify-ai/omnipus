# Adversarial Review: Helper model notices — round 2

**Document reviewed**: `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md`
**Mode**: Spec
**Round**: Round 2 of 2 (final)
**Review date**: 2026-11-15
**Reviewed commit**: `b354a031019ee93e176a61cf1a970635433a96b2`
**Branch**: `landing/1537-spec`
**Release base**: `f408d81c746b94a30824537628ab7e03016a093b`
**Prior review**: `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec-review.md`
**Architect ruling**: `/workspace/logs/1537/architect/ruling.md`
**Verdict**: **REVISE**

## Executive Summary

The revised spec still needs correction: **3 MAJOR and 4 MINOR findings** remain in this round's scope. The fixes now cover the original routing, clock, schema and accessibility omissions, but their state transitions can erase a newly scheduled retry, retain a finished helper after non-incremental catch-up, or leave a timeout/empty-reply retry unrecovered while healthy tool work continues. Frontend coverage was as thorough as backend coverage: the review traced producer order through gateway recovery and browser reduction, and checked announcement ownership, overflow, narrow-screen requirements and component reuse.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 3 |
| MINOR | 4 |
| OBSERVATION | 0 new |
| **Total in-scope findings** | **7** |

### Scope and evidence limits

- All 17 round-1 items are dispositioned below. Addressed findings are not raised again; the remaining findings identify incomplete fixes or newly introduced requirements/test defects.
- The additive wire shape, existing-projection reload design, top-chat-only nested-helper scope and strict old-tab behavior are settled. None is reopened. Exact new wording remains the chief's copy review.
- GitNexus is unavailable on this pod. Verification used direct reads, `rg`, git inspection and read-only issue retrieval; no graph impact run is claimed.
- This is a specification review, not implementation acceptance. No builds, tests, generators, browser runs or screen-reader runs were performed. Prospective UI failures are reasoned from verified source mechanisms, not presented as reproduced incidents. No runtime-only unverified claim independently determines the verdict.
- Historical probe reports under `/workspace/logs/1537/` describe the earlier `8f5a95ba7` build. They do not prove this spec or a future implementation passes. Only this round-2 report is authorized to change; no commit or push is authorized.

## Round-1 Disposition

“Addressed” means the revised text specifies the correction and its test obligation, not that implementation or execution has passed. References in the third column are sections of the document reviewed above.

| Round-1 item | Disposition | Verified revised-spec evidence |
|---|---|---|
| MAJ-001 — root completion clears helper projection | Addressed | API and Data / reload rule 1; FR-008; test order 1c explicitly covers root `done`, main Stop and a new main turn. |
| MAJ-002 — replay restarts the clock | Addressed | Connection-wide server-time estimate, fresh `session_state.emitted_at`, test 5b and the bounded 0:24–0:26 oracle replace the original single-frame assumption. |
| MAJ-003 — omitted timeout/empty producer | Addressed for producer coverage | Symbols Involved identifies `retryTimeout` and `hubLLMRetry`; API and Data, both BDD outlines, test 1a and UAT-H11 include both producers. The newly widened success path is incomplete: R2-MAJ-003. |
| MAJ-004 — inline schemas/generation omitted | Addressed | Contract Changes names both standalone and inline definitions, generation, atomic generated artifacts, both-or-neither fields and malformed-frame rejection. |
| MAJ-005 — old-tab compatibility claim false | Addressed | Edge Cases and the old-tab BDD now require strict rejection, ordinary catch-up and refresh for named rows; DOC-003 records the limitation. The accepted trade-off is not reopened. |
| MAJ-006 — no recoverable nested end | Partially addressed | Root-sequenced `ended`, per-turn matching, grandchild removal and incremental reconnect are explicit in FR-007/012 and test 1b. The fix leaves snapshot and `live_only` reconciliation unspecified: R2-MAJ-002. The original missing-grandchild-signal finding is not repeated. |
| MAJ-007 — immediate fallback success leaves retry | Partially addressed | API and Data adds a root `responding` clear, with BDD, test 1e and UAT-H10. The generalized clear conflicts with an existing producer order: R2-MAJ-001. The new fallback fixture also needs R2-MIN-001. |
| MAJ-008 — nested/ticking live regions | Addressed | Accessibility specifies one announcement owner, frozen named text, ticking spans outside it, suppression of the indicators' inner regions, and actual screen-reader evidence separately from DOM/axe. |
| MIN-001 — misleading overflow/order | Addressed | Edge Cases, FR-009 and the overflow/promotion BDDs define first-notice order, truthful mixed/recovered summaries and promotion. |
| MIN-002 — Epic/rider record missing | Partially addressed | Rider #1850 has Team B ownership and recorded chief confirmation; #1851 remains excluded. The Epic is still a placeholder: R2-MIN-003. |
| MIN-003 — security re-review exemption | Addressed | Security and User Promises now uses the combined-cut gate and affected-findings-only re-review. |
| MIN-004 — non-persistence false green | Addressed | Completed-run BDD, test 3a and channel test 7a supply ordinary-history/fallback positive controls and a root-channel positive control. |
| MIN-005 — execution/gate/regression gaps | Partially addressed | Gates, exact-cut CI/UAT, independent CHECK and failing-log evidence are specified; regression controls need not fail. The named routing-consumer guard remains omitted: R2-MIN-004. |
| MIN-006 — raw-ID name fallback | Addressed | Symbols Involved forbids reusing the resolver as-is; unknown/loading/failed names map to “Helper”; recovered wording is reused without an extra unnamed root system message. |
| OBS-001 — domain-component reuse | Preserved | Design-System Components retains the existing domain indicators, token styling and no new public notification component. |
| OBS-002 — bounded recovery, no durable subsystem | Adopted | API and Data uses the existing projection; safeguards forbid a new snapshot field, persisted notices, polling and feature flags. |
| OBS-003 — replay order | Adopted at mechanism level | Last-publication ordering and test 1d's live/replay equivalence are explicit. R2-MIN-002 narrows the new overbroad acceptance wording without changing that mechanism. |

The Clarifications statement “All findings fixed in this revision” overstates this disposition; the correction should record the remaining items rather than retaining that blanket claim.

## Findings

### CRITICAL Findings

None verified in this review.

### MAJOR Findings

#### [R2-MAJ-001] The new `responding` clear can erase the retry just scheduled

- **Severity**: MAJOR.
- **Lens**: Inconsistency & ADR/AS-IS contradiction; UI states & journey gaps; Testability & false-green risk.
- **Affected section**: Contract Changes / retry clearing and `turn_id` paragraph; API and Data / clearing; FR-006; test orders 1d and 1e.
- **Failure scenario**: A helper has already shown a silence notice. Its call then fails retryably. The producer emits `provider_retry`, followed by `provider_status responding` to clear the old silence state. Applying the revised main-chat rule removes the newly scheduled retry during its backoff, while the helper's own chat retains it. The two views disagree precisely when the user needs the countdown.
- **Evidence**: Contract Changes now says “a main-chat copy is cleared by the helper's `provider_status` `ended` or `responding` frame.” `/workspace/wt/1537-spec/pkg/agent/provider_retry_plan.go::agentLoopRunTurn.providerRetryObserver` emits the retry **before** calling `retryScheduled`. `/workspace/wt/1537-spec/pkg/agent/provider_status.go::providerSilenceWatch.retryScheduled` emits `responding` when a waiting notice was active. `/workspace/wt/1537-spec/src/store/chat/slices/provider-frames.ts::handleProviderFrame` deliberately clears only `providerStatusEvent` on responding; its comment identifies this exact scheduling order as the reason not to clear `providerRetryEvent`.
- **Recommendation**: Specify the transition between old silence, new retry and real success. Keep the chosen wire shape, but ensure a scheduling-related silence clear cannot clear the replacement retry—for example, publish that clear before the new retry, with an explicit producer-order guarantee. Retain the separate immediate-fallback-success clear. Add a real producer-to-gateway/reducer case for **waiting → retry scheduled → silence cleared**, asserting that the countdown survives in both live and projected replay state; an isolated injected fallback `responding` frame cannot cover this interaction.
- **Certainty**: **Verified, high confidence** for the specification/source contradiction and event order. The prospective named-row disappearance is **Inferred, high confidence**, not runtime-reproduced.

#### [R2-MAJ-002] The `ended` fix covers journal replay but not replacement catch-up

- **Severity**: MAJOR.
- **Lens**: Incompleteness; UI states & journey gaps; Testability & false-green risk.
- **Affected section**: API and Data / reload and reconnect; Integration Boundaries / WebSocket failure; BDD “Missed end during a disconnect”; FR-006, FR-008, FR-012.
- **Failure scenario**: A still-open browser retains a named helper row during a disconnection. The helper ends, but by reconnect the browser's cursor is too old, or the gateway boot has changed. Catch-up sends current projection rather than the lost `ended` journal event. The finished helper is absent from that projection, but no specified browser transition treats that absence as removal. The retained row can therefore survive a successful catch-up. Unavailable history takes the same current-projection path without even sending `session_snapshot`.
- **Evidence**: The spec explains only that root-sequenced `ended` is replayed by **incremental** catch-up. `/workspace/wt/1537-spec/pkg/gateway/ws_session_hub.go::sessionHub.bind` returns the current projection, not the journal tail, for nonservable cursors. `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::activeTurnProjection.forgetTurn` deletes the helper's items. `/workspace/wt/1537-spec/pkg/gateway/history_page_websocket.go::wsHandlerHandleAttachSession.sendPreparedHistorySnapshot` sends current state/projection and `catch_up_complete(live_only)` on unavailable history, with no snapshot. `/workspace/wt/1537-spec/src/store/chat/slices/catchup-frames.ts::handleSessionSnapshotFrame` preserves the existing bucket, and `handleCatchUpCompleteFrame` contains no provider-row reconciliation. These are not implicit bucket-reset paths.
- **Recommendation**: Define replacement/reconciliation of **transient named rows only** at a successfully completed non-incremental attach, covering both `snapshot` and `live_only`. Preserve loaded history, current projection rows and the held live frames released after the completion boundary; do not wipe on a failed or superseded attach. No new snapshot field, persisted tombstone or polling service is required by this recommendation. Add a retained-browser-state test containing one ended helper and one still-active helper for retention loss, boot change and unavailable history; assert the stale row leaves and the current row survives. Keep the existing incremental `ended` test as a separate case.
- **Certainty**: **Verified, high confidence** for the missing mode-specific specification and existing attach semantics. The future stale named row is **Inferred, high confidence**, not a claimed executed failure.

#### [R2-MAJ-003] Timeout and empty-reply recovery have no specified successful producer

- **Severity**: MAJOR.
- **Lens**: Incompleteness; Inconsistency & ADR/AS-IS contradiction; UI states & journey gaps.
- **Affected section**: API and Data / “Retry then success with the same model”; US1-3, US2-4; FR-004/006; timeout/empty outlines, test 1a and UAT-H11.
- **Failure scenario**: A helper times out before its first byte, or returns an empty stream. Its next outer-loop attempt answers promptly with a tool call, and the tool keeps running. The new retry copies appear, but that answering invocation needed zero fallback-chain retries and never showed a silence notice. There is neither a `recovered` event to copy nor a waiting state to clear. The named row can remain “Retrying now…” throughout healthy tool work; a test that waits only for helper completion hides it.
- **Evidence**: The spec promises recovery for a helper's retried call but says only “the `recovered` frame is copied.” `/workspace/wt/1537-spec/pkg/agent/loop_run_turn_response.go::agentLoopRunTurnResponseCallLLMWithRetries.retryTimeout` owns these outer retries; `handleAttemptFailure` exits the retry loop on success without emitting recovery. `/workspace/wt/1537-spec/pkg/providers/fallback.go::FallbackChain.Execute` sets `Retries = attemptNo - 1` for that invocation's answering candidate. `/workspace/wt/1537-spec/pkg/agent/loop_run_turn.go::agentLoopRunTurn.callProviderOnce` passes that number to `noteProviderRecovered`; `/workspace/wt/1537-spec/pkg/agent/provider_status.go::agentLoopRunTurn.noteProviderRecovered` emits only for a positive number, and `providerSilenceWatch.stop` responds only if waiting was active. The new timeout/empty tests assert notice appearance, not successful recovery while the helper remains active.
- **Recommendation**: Specify who emits/counts recovery after the eligible outer timeout/empty retry sequence succeeds. Define how that count combines with any retries inside the answering fallback invocation so the browser receives one truthful recovery, not duplicates or an invented count. Respect FR-003's unchanged main-agent live behavior; do not silently broaden this helper correction. Add timeout→successful tool response and empty stream→successful tool response cases that keep the helper active, assert the recovered transition in both chats, and check reattach before turn end.
- **Certainty**: **Verified, high confidence** for the producer/accounting gap and untested success path. The composed future UI failure is **Inferred, high confidence**, not runtime-reproduced.

### MINOR Findings

#### [R2-MIN-001] The new fallback fixture does not exhaust the primary candidate

- **Severity**: MINOR.
- **Lens**: Testability & false-green risk; Inconsistency & ADR/AS-IS contradiction.
- **Affected section**: BDD “Named retry row clears when a fallback model answers at once”; UAT-H10; fake-model dataset 10.
- **Failure scenario**: A test scripts two primary 429 responses and then expects the fallback to answer. The unchanged retry policy calls the primary a third time first. An unspecified default response can keep the run on the primary, or the fake must bypass production retry behavior to reach the expected fallback. The test can then fail for the fixture or pass without proving the intended transition.
- **Evidence**: The new BDD says “first model gets 429 ... twice”; UAT-H10 and dataset 10 repeat it. `/workspace/wt/1537-spec/pkg/providers/fallback_retry.go::maxAttemptsPerCandidate` is **3 total calls**. `/workspace/wt/1537-spec/pkg/providers/retry_plan.go::FallbackChain.inPlaceRetry` permits the third call after the second rate-limit failure; `/workspace/wt/1537-spec/pkg/providers/fallback.go::FallbackChain.Execute` advances only after that policy concludes or skips the candidate.
- **Recommendation**: With an available fallback and otherwise normal limits, script **three primary 429 failures**, then a first-call fallback success carrying the tool request. Assert the candidate/request trace as a positive control before asserting the row clears during the long tool operation. Correct the fixture, not the retry policy.
- **Certainty**: **Verified, high confidence** — new fixture and unchanged policy disagree; no test run is claimed.

#### [R2-MIN-002] “Never a retry after recovery” incorrectly spans the whole turn

- **Severity**: MINOR.
- **Lens**: Ambiguity; Inconsistency & ADR/AS-IS contradiction; Testability & false-green risk.
- **Affected section**: FR-008; API and Data / last-publication ordering; test order 1d.
- **Failure scenario**: A helper retries, recovers, executes a tool, then makes another model call in the **same turn** that needs a new retry. A literal implementation of “never a retry line after that turn's recovered line” suppresses the legitimate later retry on reattach, even though FR-006 requires the next notice to update that row.
- **Evidence**: FR-008 contains that unqualified prohibition. `/workspace/wt/1537-spec/pkg/agent/loop_run_turn_iterations.go::agentLoopRunTurnConductor.runIterations` runs provider and tool rounds within one turn; `/workspace/wt/1537-spec/pkg/agent/loop_run_turn.go::agentLoopRunTurn.callProviderOnce` can emit recovery per provider round. The architect's purpose is to prevent an **older, superseded** retry from being replayed after newer recovery, not to make recovery permanent for the entire turn.
- **Recommendation**: Say “An older retry superseded by recovery must not reappear; a later retry in the same turn remains current.” Keep last-publication ordering. Add **retry → recovered → tool → new retry**, under one `turn_id`, to live/replay equivalence tests.
- **Certainty**: **Verified, high confidence** for the wording contradiction; suppression by a future implementation is **Inferred**.

#### [R2-MIN-003] The required feature Epic is still missing from the spec

- **Severity**: MINOR.
- **Lens**: Incompleteness; structural integrity.
- **Affected section**: Epic header; Clarifications / round-1 findings fixed.
- **Failure scenario**: The spec is approved and implementation is planned from it while its feature container and board linkage are still unspecified. The recorded rider owner does not supply the missing Epic, so the required feature-level scope/stage record remains incomplete.
- **Evidence**: The header says “to be supplied by the chief before the spec is approved” and “no Epic is linked yet.” `/workspace/wt/1537-spec/docs/internal/issue-and-board-conventions.md::Feature epics, Stage and Horizon` requires an Epic for every product feature and its spec. This is the unresolved Epic portion of round-1 MIN-002, not a renewed finding about the now-recorded rider owner.
- **Recommendation**: Have the chief supply the actual Epic link and keep its spec/branch and Stage linkage current before approval. Do not invent an Epic number or treat #1850, a milestone or the parked label as the feature Epic.
- **Certainty**: **Verified, high confidence** documentation gap. Whether an unlinked external Epic already exists is **Unknown**.

#### [R2-MIN-004] The affected routing-consumer guard is still outside the regression plan

- **Severity**: MINOR.
- **Lens**: Testability & false-green risk; structural integrity.
- **Affected section**: Regression Test Requirements; Test Implementation Order; round-1 MIN-005 correction.
- **Failure scenario**: The implementer adds or relocates `routingSessionID` reads for the two-destination rule, runs only the listed feature checks and discovers the closed-consumer regression at the cut. An unplanned expectation update can then be mistaken for an obsolete test to remove rather than an invariant to preserve.
- **Evidence**: `/workspace/wt/1537-spec/pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed` checks exact consumer buckets, including 25 WS-stamping reads. Its classifier explicitly names `providerRetryObserver` and `emitProviderStatus`. Round-1 MIN-005 requested this guard; the revised regression inventory still omits it. An exact-name search returned exit 1, while the same search instrument found the proposed retry test in the spec with exit 0.
- **Recommendation**: Add this guard to the affected regression inventory. Preserve it unchanged where possible; if legitimate read ownership/count changes require an update, identify and justify each changed consumer and retain the closed-set/positive-control assertions. Never delete, skip or indiscriminately broaden it to make the feature pass. This does not authorize a reviewer-side local run.
- **Certainty**: **Verified, high confidence** omission and guard relevance. No present or future test failure is asserted as executed.

### Observations

No new in-scope observations. The three round-1 observations are dispositioned above.

### Separate inherited dataset note — not part of this round's verdict

While checking the new fallback fixture, an unchanged earlier error was also found: fake-model dataset 3 expects `Retry-After: 300` to produce a countdown capped at 2:00. The same expectation exists in the round-1 spec commit `ddc5578088006e955248a52ec749d7c6cf83a755`; it was not introduced by this correction and is not counted as a new round-2 finding. `/workspace/wt/1537-spec/pkg/providers/retry_plan.go::FallbackChain.inPlaceRetry` instead skips an above-120-second candidate before scheduling or announcing its retry. **Verified, high confidence** source/spec mismatch; dispatcher note only, no side fix made. The chief should have the author correct the oracle or track its disposition: distinguish exactly 120 seconds from above-ceiling skip, rather than changing the retry policy.

## Structural Integrity

### Spec mode — plan-spec output

Structural PASS below means the section/obligation is present, not that its behavior is accepted or tested.

| Check | Result | Notes |
|---|---|---|
| Valid `Status:` field | PASS | `In review`. |
| ADR linked or explicitly unnecessary | PASS | No open design choice; founder option A and architect ruling are explicitly recorded. |
| Contract changes precede implementation detail | PASS | Both schema representations, generator and generated consumers are named. Semantic clearing defect: R2-MAJ-001. |
| API and data section | PASS, defects | Present; recovery-mode and successful outer-retry gaps are R2-MAJ-002/003. |
| UI loading/empty/error/partial/success | PASS, defects | Both destinations, idle/active root, name-lookup failure, overflow and recovery named. Transition defects remain. |
| End-to-end user journey | PASS | Main delegation → notice → open helper → recovery → completion. |
| Accessibility and keyboard | PASS in spec | One live-region owner; no new focusable control; no ticking announcements; real screen reader required separately. |
| Catalogue-first design-system reuse | PASS in spec | Existing domain indicators, token styling, no new public component. |
| Security and user promises | PASS in scope | No policy/approval change; named fields only; channel containment; combined security review retained. Not a security acceptance result. |
| BDD and independently specified oracles | Present, correction needed | R2-MIN-001/002 and the missing transition cases below. |
| Requirement → scenario → test traceability | Present, incomplete coverage | FR-001–012 have rows; nominal mapping does not cover all lifecycle branches. |
| Epic and confirmed riders | FAIL / partial | Rider record present; Epic missing, R2-MIN-003. |
| Reachability | PASS in spec | Existing chat screen and execution plan named; not executed here. |
| User-facing documentation TODOs | PASS in spec | DOC-001–003 name `/workspace/wt/1537-spec/docs/troubleshooting.md` and `/workspace/wt/1537-spec/docs/agents.md`. Implementation must include the updates and docs verification. |
| Agent tool parity table | N/A, justified | No user-editable agent-managed object/property/state is added; these are backend-authored transient display frames, not create/update fields. |

### Twelve-lens coverage

| Lens | Application and result |
|---|---|
| 1 — Ambiguity | Traced per-turn versus per-request recovery; R2-MIN-002. Exact new copy intentionally left to chief. |
| 2 — Incompleteness | Lifecycle emission and recovery modes checked through gateway and browser; R2-MAJ-002/003 and Epic residue. |
| 3 — Inconsistency / architecture | Verified current producer/reducer semantics, three-call fallback cap and iteration lifetime; R2-MAJ-001/003, R2-MIN-001/002. No proposal reopens the selected architecture. |
| 4 — Infeasibility | Existing bounded in-process projection and connection clock require no new runtime/storage. No additional verified feasibility defect beyond the erroneous fallback fixture. No benchmark or capacity proof claimed. |
| 5 — Contract-first | Standalone and inline definitions, generated strict validators, paired identifiers, `ended`, per-session sequence and copy exclusions examined. Original generation/compatibility gaps fixed; clear semantics still fail under R2-MAJ-001. |
| 6 — Security | Checked output-only scope, address boundaries, field-based rendering, no permission change, channel exclusion and required review. No new security defect verified; implementation audit pending. |
| 7 — Reachability | Screen location, pre-first-byte helper behavior, idle root and nested row are explicit. Appearance-only coverage does not prove successful outer-retry recovery: R2-MAJ-003. |
| 8 — UI states/journey | Reviewed live/reload/reconnect, inactive root, multiple helpers, missing names, end/Stop, overflow and continued tool work. All three MAJOR findings have user-visible consequences. |
| 9 — Accessibility/keyboard | Existing indicators' inner polite regions inspected against the revised one-owner composition; focus, 320 CSS px wrapping and separate screen-reader evidence required. No new verified spec defect; actual announcements remain untested. |
| 10 — Design-system/brand | Catalogue source entries, domain-component policy, tokens and calm chat-first presentation checked. No new component/control/visual design is recommended. Render quality remains untested. |
| 11 — Testability/false greens | Checked actual producer ordering versus injected-frame tests, live versus projected state, positive controls, recovery before turn end and regression inventory; R2-MIN-001/004 plus major test additions. |
| 12 — Overcomplexity | Preserve the chosen two-frame extension and existing bounded projection. No durable notice store, extra snapshot field, polling, third destination or feature flag is warranted by these findings. |

### Architecture qualification

The review treats the founder's option A and the architect ruling as authority for the new named status surface. It does not resurrect nested helper narration or make agent execution depend on a viewer. The existing **“UI-independent turns and session-bound webchat streaming”** decision at `/workspace/wt/1537-spec/docs/internal/architecture/ADR-082-ui-independent-turns-and-session-bound-streaming.md` remains intact: reconnect changes presentation recovery, not turn lifetime. The accepted strict validation behavior is consistent with **“additionalProperties Policy (Requests Closed, Responses Per-Case)”** at `/workspace/wt/1537-spec/docs/internal/architecture/ADR-014-additional-properties.md`.

## Test Coverage Assessment

### Missing or incomplete test categories

| Category | Required correction | Trace |
|---|---|---|
| Real producer transition | Begin in waiting, schedule retry through the real observer/watch order, then compare both live views and projected replay. An injected retry alone cannot expose its subsequent clear. | R2-MAJ-001; FR-006 |
| Replacement catch-up | Prepopulate browser rows; end one helper offline; exercise nonservable-cursor snapshot and unavailable-history `live_only`; retain a second current helper and ordinary history. Include a later held live update. | R2-MAJ-002; FR-006/008/012 |
| Successful outer retries | Both timeout and empty-stream retries must recover before turn end while a successful tool call keeps the turn active; verify truthful count and no duplicate recovery if the answering call also retries. | R2-MAJ-003; US1-3, US2-4 |
| Same-turn later request | Recovered first request, tool execution, then a new retry in the same turn; live and replay must agree that the later retry is current. | R2-MIN-002; FR-006/008 |
| Routing regression | Name the closed routing-consumer guard and retain its assertions or justify a precise ownership/count update. | R2-MIN-004; addressing changes |

### Dataset gaps

| Dataset | Missing or incorrect boundary | Recommendation |
|---|---|---|
| New two-candidate fallback | Two primary failures do not exhaust three total calls. | Three primary failures, assert actual candidate trace, then fallback success and ongoing tool work. |
| Timeout/empty scripts | Only notice appearance is asserted. | Fast next success with tools, before any new 30-second silence, and a mixed outer/inner-retry case. |
| Reconnect scripts | Brief disconnect with servable journal only. | Cursor retention loss, changed boot and unavailable history with retained browser state. |
| Provider ordering | Recovery treated as final for the turn. | Add a newer retry after recovery in the same turn; distinguish it from a stale retained retry. |

The revised plan now distinguishes failing-first new behavior from already-passing controls, names author checks versus exact-cut CI, and separates DOM/axe from real screen-reader evidence. Those are plan corrections, not execution receipts. The saved failing log must precede any future passing result; historical probe reports do not supply that RED evidence.

## STRIDE Threat Summary

S = impersonation; T = tampering; R = missing accountability; I = information disclosure; D = resource exhaustion; E = unauthorized privilege. “Addressed” below means the proposed scope retains the relevant boundary, not that security testing passed; “N/A” means this change adds no such operation.

| Component/data flow | S | T | R | I | D | E | Evidence and limits |
|---|---|---|---|---|---|---|---|
| Gateway notice copies and browser attach | Addressed | Addressed | N/A | Addressed | Existing bound | Addressed | No new entry point; selected session-hub delivery and generated validation remain. `/workspace/wt/1537-spec/pkg/gateway/websocket_replay.go::wsHandlerHandleAttachSession.resolveSession` retains addressed-session access resolution. The future copy implementation still needs the feature security pass. |
| Display name and notice text | N/A | Addressed | N/A | Addressed | Addressed in spec | N/A | Named fields, browser roster, no raw provider error/IDs, wrapping and four-row render cap. No claim of executed sanitization or render tests. |
| Channels and tools | Addressed in scope | N/A | N/A | Addressed in scope | N/A | Addressed in scope | Channel behavior is excluded from change; `/workspace/wt/1537-spec/pkg/agent/provider_retry_plan.go::agentLoopRunTurn.publishRetryNotice` retains its audience check. No new tool or permission bypass. |
| Transient projection/lifetime | N/A | Correctness findings | N/A | No new persistence | Existing bound, lifetime fixes needed | N/A | Existing projection has a 2 MiB budget; R2-MAJ-001/002 are presentation/lifecycle defects, not demonstrated security exploits. No durable store or new service. |

No verified product vulnerability was found in this bounded spec pass. That statement does not replace the security seat at the cut or claim a completed authentication, load or penetration test.

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| New agent tool: registered and policy-covered? | N/A | Reachability and Contract Changes add only transient frames and chat rendering; no tool. |
| User-facing component named? | Yes, in the plan | `/workspace/wt/1537-spec/src/components/chat/ChatScreen.tsx`; helper notice area plus independent named rows above the main composer. Existing indicators and store are identified. |
| Helper before first byte and root while idle covered? | Yes, specified | Test orders 6 and 6b, UAT-H1/H2/H3; actual implementation remains pending. |
| Nested helper path in scope? | Yes, specified | Architect ruling point 3; grandchild BDD and UAT-H7 explicitly include top/middle/grandchild behavior and removal. Not newly runtime-tested here. |
| Execution rather than just authorship planned? | Yes | RED/GREEN/CHECK and exact-cut independent UAT/CI are named. Missing branches are listed above. |

**Code correct and tested:** Not established. This commit contains the spec and round-1 review, not the feature implementation; no tests were run in this review.

**Reachable by a user/agent:** The intended user route is specified, but the new behavior is not delivered or runtime-verified by this review.

## Unasked Questions for the Spec Author

These are implementation/specification clarifications, not requests to reopen product decisions.

1. Which transition/order prevents a scheduling-related `responding` from clearing the new retry while still allowing immediate fallback success to clear it? Resolve R2-MAJ-001 in the state-transition rules and tests.
2. At which successful non-incremental attach boundary are retained transient rows reconciled, including `live_only`, without touching loaded history or discarding newer live frames? Resolve R2-MAJ-002.
3. Which producer owns recovery/counting after an outer timeout/empty retry, including an answering call that itself retries? Resolve R2-MAJ-003 without changing the main-agent live contract silently.

## Verdict Rationale

**REVISE.** R2-MAJ-001 contradicts an existing producer order; R2-MAJ-002 omits a browser transition for real catch-up modes; R2-MAJ-003 assumes a recovery emission that the newly included producer paths do not provide. These are verified specification defects with concrete prospective user failures, not copy preferences or demands for a different architecture. No CRITICAL finding is asserted.

### Escalation to the founder

This is the final grill, **before correction round 2**. That correction has not happened, so this report does not claim a post-correction verdict. There is no CRITICAL item to escalate now. Under the fixed-round rule, any blocking finding still unresolved after the correction goes to the founder; no third grill is authorized.

| Finding to carry through correction | Why currently open | Founder disposition needed only if still unresolved |
|---|---|---|
| R2-MAJ-001 | The contract still clears the scheduling-related retry. | Decide disposition of the unresolved correctness defect; do not call it fixed from fallback-only tests. |
| R2-MAJ-002 | Non-incremental transient-row reconciliation is not specified. | Decide disposition of the unresolved reconnect defect; do not claim every disconnect mode is covered. |
| R2-MAJ-003 | Outer-retry recovery ownership/counting is absent. | Decide disposition of the unresolved success-state gap; do not silently remove the recovery promise. |

### Recommended Next Actions

- [ ] Spec author resolves R2-MAJ-001–003 together as state transitions and adds the missing live/replay/success-before-end acceptance cases, preserving the settled wire and reload design.
- [ ] Correct the new fixture and same-turn wording (R2-MIN-001/002); chief supplies the Epic (R2-MIN-003); name the regression guard (R2-MIN-004). Record the inherited dataset note's disposition and replace the blanket round-1 completion claim.
- [ ] Team-lead completes the fixed second correction process, escalates unresolved blockers, then plans implementation and its existing gates. Do not commission another grill or treat this review as runtime acceptance.

### Next step in the process

Verdict: **REVISE**

Review written to: `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec-review-round2.md`

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews
the founder on "Questions for the founder", then the spec author fixes
round-2 findings. Any CRITICAL finding still open after that fix is
listed under "Escalation to the founder" above for the founder to
decide — do not run a third grill round. Once resolved, team-lead plans
the implementation (RED / GREEN / CHECK, the 5-reviewer gate).

The founder-question list below is empty; do not invent a new product decision to fill it. The remaining-blocker escalation rule still applies after correction.

## Questions for the founder

**None newly required by this review.** The findings concern making the selected behavior correct and testable. Wire shape, reload mechanism, nested-helper destination and exact-copy ownership remain settled. If correction round 2 leaves a blocking finding unresolved, team-lead takes that concrete disposition to the founder instead of starting a third grill.

Skills: grill-spec; omnipus-shared-rules; ux-heuristics-review.

## Evidence and Final Self-check

| Claim | Evidence | Certainty |
|---|---|---|
| Correct version and text-only implementation status | `git rev-parse HEAD` → exit 0, `b354a031019ee93e176a61cf1a970635433a96b2`; `git diff --name-status f408d81c746b94a30824537628ab7e03016a093b..HEAD` → exit 0, only the spec and round-1 review added. | Verified |
| Every prior item was assessed against the revision and ruling | `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec-review.md::Findings`; `/workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md::Contract Changes`, `API and Data`, `Accessibility and Keyboard`, `Test-Driven Development Plan`, `Functional Requirements`, `Clarifications`; `/workspace/logs/1537/architect/ruling.md` (points 1–3). | Verified — spec disposition, not implementation success |
| R2-MAJ-001 producer/consumer contradiction | `/workspace/wt/1537-spec/pkg/agent/provider_retry_plan.go::providerRetryObserver`; `/workspace/wt/1537-spec/pkg/agent/provider_status.go::providerSilenceWatch.retryScheduled`; `/workspace/wt/1537-spec/src/store/chat/slices/provider-frames.ts::handleProviderFrame`. Retry precedes responding; own reducer intentionally retains retry. | Verified mechanism; Inferred future UI consequence |
| R2-MAJ-002 snapshot/live-only gap | `/workspace/wt/1537-spec/pkg/gateway/ws_session_hub.go::sessionHub.bind`; `/workspace/wt/1537-spec/pkg/gateway/history_page_websocket.go::sendPreparedHistorySnapshot`; `/workspace/wt/1537-spec/pkg/gateway/ws_hub_projection.go::activeTurnProjection.forgetTurn`; `/workspace/wt/1537-spec/src/store/chat/slices/catchup-frames.ts::handleSessionSnapshotFrame`, `handleCatchUpCompleteFrame`. | Verified mechanism and omitted rule; Inferred future stale row |
| R2-MAJ-003 outer success does not supply the assumed recovery | `/workspace/wt/1537-spec/pkg/agent/loop_run_turn_response.go::handleAttemptFailure`, `retryTimeout`; `/workspace/wt/1537-spec/pkg/providers/fallback.go::FallbackChain.Execute`; `/workspace/wt/1537-spec/pkg/agent/loop_run_turn.go::callProviderOnce`; `/workspace/wt/1537-spec/pkg/agent/provider_status.go::noteProviderRecovered`, `providerSilenceWatch.stop`. | Verified mechanism; Inferred future UI consequence |
| New fallback fixture requires a third primary failure | `/workspace/wt/1537-spec/pkg/providers/fallback_retry.go::maxAttemptsPerCandidate` = 3; `/workspace/wt/1537-spec/pkg/providers/retry_plan.go::FallbackChain.inPlaceRetry`; spec BDD/UAT-H10/dataset 10 say two. | Verified |
| A recovered turn can legitimately retry a later model round | `/workspace/wt/1537-spec/pkg/agent/loop_run_turn_iterations.go::agentLoopRunTurnConductor.runIterations`; spec FR-006 versus FR-008. | Verified |
| Epic is absent from the document, not proven absent everywhere | Spec Epic header; `/workspace/wt/1537-spec/docs/internal/issue-and-board-conventions.md::Feature epics, Stage and Horizon`. | Verified document gap; external linkage Unknown |
| Guard omission search could detect a present test | `rg -n -F 'TestRoutingSessionID_ConsumerSetIsClosed' /workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md` → exit 1; positive control `rg -n -F 'TestHelperProviderRetryPublishesToHelperAndNamedRootCopy' /workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec.md` → exit 0, order-1 test row. Guard source read at `/workspace/wt/1537-spec/pkg/agent/routing_session_id_consumer_set_adr057_test.go::TestRoutingSessionID_ConsumerSetIsClosed`. | Verified; no test failure claimed |
| Above-ceiling dataset issue predates this fix round | `git show ddc5578088006e955248a52ec749d7c6cf83a755:docs/internal/specs/helper-model-notices-spec.md` piped to the targeted context search → individual exits 0 and 0, same `Retry-After 300` expectation; `/workspace/wt/1537-spec/pkg/providers/retry_plan.go::FallbackChain.inPlaceRetry` returns `skip`. | Verified; outside in-scope finding count |
| Contract generation and strict old-tab treatment are grounded | `/workspace/wt/1537-spec/scripts/_gen-asyncapi-types.mjs::schemas` reads `doc.components.schemas`; `/workspace/wt/1537-spec/src/lib/api/generated/ws-schemas.ts::ProviderRetryFrame`, `ProviderStatusFrame` use `.strict()`; spec now names inline edits and refresh behavior. | Verified source/spec; complete stale-tab recovery not executed |
| Accessibility/design/doc obligations were checked, not runtime-certified | `/workspace/wt/1537-spec/src/components/chat/ProviderRetryIndicator.tsx::ProviderRetryIndicator`; `/workspace/wt/1537-spec/src/components/chat/ProviderWaitingIndicator.tsx::ProviderWaitingIndicator`; `/workspace/wt/1537-spec/design-system/catalog.json::entries`; spec Accessibility, Design-System Components, DOC-001–003; `/workspace/wt/1537-spec/docs/troubleshooting.md::The model provider can't be reached or is busy`, `The model has gone quiet`; `/workspace/wt/1537-spec/docs/agents.md::Workers and delegation`. | Verified source review; actual visual/assistive behavior Unknown |
| No test/build/CI/UAT success is claimed | This read-only review has no execution receipt; the historical probe reports concern `8f5a95ba7`, not a delivered implementation of this spec. All proposed additional tests remain future work. | Verified review scope; product acceptance Unknown |
| **Self-check** | Re-read the complete report diff in two excerpts against the spec, prior findings, ruling and retained source evidence. `git diff --no-index -- /dev/null /workspace/wt/1537-spec/docs/internal/specs/helper-model-notices-spec-review-round2.md` → exit 1, expected new-file diff. Inline `python3 -I` artifact/scope check → exit 0: `7 complete findings, 17 prior dispositions, 12 lenses`; removing one required scenario field in memory was detected. `git status --porcelain=v1 --untracked-files=all` → exit 0, only the authorized report untracked; `git diff --exit-code` and `git diff --cached --exit-code` → exits 0/0, no changes; HEAD unchanged. Certainty limits and final-round/no-third-grill process checked. | Verified — report/source self-check only, not product tests |
