# Adversarial Review: Parent wake and visibility for helper events

**Verdict: REVISE — 0 CRITICAL, 5 MAJOR, 1 MINOR, 0 OBSERVATIONS.** Of the 17 round-one findings, 12 are closed in the written design and 5 are partially addressed. One additional defect was introduced by the durability fix. This is the final spec grill, not product acceptance.

**Document reviewed**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/specs/parent-wake-visibility-spec.md`

**Prior review**: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/specs/parent-wake-visibility-spec-review.md`

**Commit reviewed**: `ba890752c33c50290893d0516e0bc83047d66b5e`

**Spec SHA-256**: `e331bba206349d4a42fe17d41dcf026038e592955faacb53e480b97436ef5861`

**Mode**: Spec

**Round**: Round 2 of 2 (final)

**Review date**: 2026-10-15

## Executive Summary

The corrected specification now requires durable verdict bytes before acknowledgement, applies the actor-independent Stop rule, and distinguishes a parked question card from a live approval wait. It is not implementation-ready: the chosen wake key aliases independent events, the new archive carrier confuses saving with safe model placement, and repair/UI/acceptance details remain incomplete. All twelve lenses were applied, including contract, both chat-rendering paths, accessibility, catalogue reuse and public-documentation promises; frontend scrutiny was not reduced to reducer tests.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 5 |
| MINOR | 1 |
| OBSERVATION | 0 |
| **Total surviving findings** | **6** |

| Round-one closure | Count |
|---|---:|
| Closed in written design | 12 |
| Partially addressed | 5 |
| Entirely unaddressed | 0 |
| **Total reviewed** | **17** |

### Authority and limits

| Item | Review treatment |
|---|---|
| PW-D13 | Settled: an intentional helper Stop by its parent agent or a person never wakes/joins the parent and never auto-resumes the helper. The stopped row may remain. |
| PW-D14 | Settled: a restart-stopped parent may handle the resumed helper's fresh outcome, not replay its interrupted instruction; an intentional Stop never loses precedence. |
| PW-D15 | Settled: show the outcome row immediately while a question card is parked; retain the report and deliver it with the person's correlated answer, with no outcome-only turn. |
| Current source | `git pull --ff-only` exited 0, already at the requested author-fix commit. A source diff against the round-one baseline confirmed that production code, contracts and the local grill method are unchanged. Decisive source symbols were nevertheless reopened below. |
| Graph tools | GitNexus MCP is not connected in this harness. Direct source reads and caller searches were used. No graph impact or `detect_changes` result is claimed. Documentation-only impact is inferred from the scoped diff. |
| Meaning of closure | Closure is of a specification defect, not a claim that its future implementation or tests passed. The author's §21 “Every finding is fixed” assertion is not independent closure evidence. |
| Execution | No Go/frontend/browser/CI/UAT suite ran. No gateway was contacted, no live instance/data directory was accessed, and no production file was changed. Failure consequences are source-grounded inferences, not reproduced product incidents. |
| Scope | No idle checker, work timeout, new policy layer, external-worker replacement or account-privacy redesign is added. The withdrawn round-one privacy allegation stays withdrawn: broad authenticated-operator viewing remains an accepted assumption. |

## Round-1 Closure Verification

Original IDs are retained for partially addressed findings. MAJ-009 is downgraded to MINOR because the founder's answer and the main runtime table now settle the behavior; only a contradictory old edge-case sentence remains.

| Original ID | Closure | First-hand verification | Remaining gap |
|---|---|---|---|
| CRIT-001 | **Closed — written durability requirement** | PW-D16, FR-005 and §9.5 require the exact archive carrier before verdict acknowledgement and the consumed marker; Tests 6/21 reopen stores. This replaces the former build/publish/ack-only sequence. | A distinct problem in the **chosen carrier's model-placement semantics** is NEW-MAJ-001 below. Closure does not certify the current receiver, which still has its old ordering. |
| MAJ-001 | **Closed — written Stop policy** | PW-D13; closed reason list excludes `stopped`; §9.6 distinguishes landed causes from `stopped_child:` text; Test 10 covers parent-agent/person, first delivery, live join and boot replay; DOC-1 agrees. | No new policy question. Implementation must change the current classifier/publisher, not only the accepted-reason list. |
| MAJ-002 | **Closed — written stopped-state table** | PW-D19 and §9.4 now cover in-flight, accepted-but-unlanded and landed intentional Stop, including a cleared marker. PW-D14/Test 12 name the restart-stopped exception; self Stop does not cancel an unselected helper. | The source's blanket stopped-root refusal and hand-back admission still exist. Implementation must realize the narrow exception through the existing admission/Stop fence and prove an actual turn, not merely bus publication. This is an implementation obligation, not another founder choice. |
| MAJ-003 | **Closed — row event identity** | PW-D20, §9.3 source-event/destination identity table and Test 31 distinguish repeated delivery from distinct stops/goals. Existing stoppedChildNoticeID and goalVerdictUpwardMessageID were rechecked. | This closes **row** identity. The newly chosen **wake** identity has the separate collision in MAJ-005. Use the complete authoritative source-event identity, not a bare sequence stripped of its helper namespace. |
| MAJ-004 | **Closed — written fallback rule** | §9.4 explicitly admits normal ordinary-root completion between the live check and enqueue; Test 20 requires an eventual idle wake and forbids reopening arbitrary terminal helpers/replaced executions. | The current wakeOwnerOrStore still uses the old terminal guard; future code and its deterministic race test must implement the revised rule. |
| MAJ-005 | **Partial** | PW-D17/§9.4–9.5 now choose one key, dispositions and a completion wake owner, including task-owned met plus completion. | The key identifies a generation, not an event; it suppresses a distinct later result. See MAJ-005 below. |
| MAJ-006 | **Closed — written carriers** | §9.3 adds `finished`, `question`, `blocker`, `failed`; §§10/19 require lasting rows and both renderer paths. Test 33 adds REST/live/replay/component parity and model-independent visibility. | The new wire family still needs backend-owned schemas/generation and real producer wiring; none is already implemented at this HEAD. |
| MAJ-007 | **Closed — occurrence identity** | PW-D21, §9.7, Test 5 and SC-005 use assistant occurrence plus call ID and explicitly allow a new occurrence reusing `call_0`; the current occurrence-based archive fixture was reread. | UAT-4's separate measurement gap remains under MAJ-012. |
| MAJ-008 | **Partial** | PW-D18/§9.9 name boot and delivery repair, independent destinations, write-result propagation and a reopened-store Test 34. | “Retained event” has no lifetime guarantee against acknowledgement/compaction. See MAJ-008. |
| MAJ-009 | **Partial — residual MINOR** | PW-D15, §9.4, FR-020, revised approval BDD and the new parked-card BDD/Test 20 now distinguish the real states. | Edge Cases still says an approval **or question card** joins the live turn. See the downgraded MAJ-009. |
| MAJ-010 | **Partial** | §§18–20 add inherited loading/empty/reconnect behavior, a journey, queued/running/refused states and both renderer obligations. | The table does not make partial-write failure observable to the person or reconcile changing resume text with immutable/no-op rows. See MAJ-010. |
| MAJ-011 | **Closed — written accessibility contract** | §19 specifies native disclosure, keyboard operation, accessible name, focus preservation, one new-event announcement and no old-history announcements, with tests for both render paths. | Accessibility execution remains a future gate, not a demonstrated PASS. |
| MAJ-012 | **Partial** | Test 26 now holds a partial stream open for two browsers; Test 32 invokes `creator_notice`; Test 1 exercises real refusal; Test 6 adds task pairing; SC-008 excludes documentation review; §20 names frontend gates. | UAT-4 still uses a deduplicable transcript success row as its only proof of one executor invocation. See MAJ-012. |
| MAJ-013 | **Closed — written decision mapping** | Opening/PW-D22 explain no new ADR; §16 maps the three governing decisions by full title; §17 names security/user promises and preserves accepted broad viewing. Those ADR clauses were reread. | Dated ADR edits are explicitly a follow-up, not completed by this spec or authorized as edits in this review task. Keep that obligation in the implementation plan. |
| MIN-001 | **Closed** | §8 points to the named closed list; §16 now exists; the Stop warning is closed under PW-D13; §9.8 correctly describes the wake-eligibility comment rather than inventing a not-stored claim. | None surviving. |
| MIN-002 | **Closed** | §20 names the existing domain row/native disclosure, catalogue entries and no new public component. Catalogue entries and GoalOutcomeRow were rechecked. | No new visual-design choice is required. |
| OBS-001 | **Closed** | §9.4 removes the row-written Boolean and leaves durability/repair with the row writer. | None surviving. |

The former A4 warning is also resolved: the companion is identified by absolute path and revision. Its §5.7 was verified from Git object `80559308f`, not assumed from the old scheduled-autonomy document.

## Findings

### CRITICAL Findings

None verified in this final review. The original publish-before-save critical defect is corrected as a written ordering requirement. The remaining findings establish incorrect delivery, unsupported placement or incomplete presentation/acceptance contracts; this report does not claim destruction of the only surviving work artifact or a reproduced security incident.

### MAJOR Findings

#### [MAJ-005] One generation key collapses distinct outcomes

- **Severity**: MAJOR.
- **Origin**: Round-one finding partially addressed; the chosen formula introduces a concrete collision.
- **Certainty**: **Verified (high confidence)** formula and same-generation resume; the suppressed subsequent turn is **Inferred (high confidence)** from the specified consumed-key rule, not runtime-tested.
- **Lens**: Ambiguity; Incompleteness; Reachability; Testability & false-green risk.
- **Affected section**: PW-D17; §9.4 idempotency; §9.5; FR-009, FR-013, FR-015, FR-021; Tests 6/14/21/31.
- **Failure scenario**: A restart notice wakes the parent under `child-A:1:outcome`. The native helper resumes in generation 1 and later finishes. Its genuinely different finish has the same key, so the consumed marker suppresses the required finish wake. Repeated restarts within that generation collide too. §9.5 says continuing unmet/not-verified events have their own canonical IDs, but PW-D17 gives no event-qualified formula for them. Row IDs can be correct while wake/input IDs still collapse.
- **Evidence**: PW-D17 explicitly defines `<child session>:<generation>:outcome`; §9.4 says an already-consumed key starts no turn. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_cancel.go`::SteerCanceller.reviveWithExecution preserves the generation for stopped-session resume. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steering.go`::AgentLoop.writeSteeringConsumedMarker / recordPolledFinalConsumed makes consumption identity-specific; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/stopped_notice.go`::stoppedChildNoticeID already distinguishes actual stop transitions using stop sequence as well as helper/generation/recipient. A read-only formula audit produced `distinct_events=2; keys=1`, with event-qualified keys as a positive control; it was not a product test.
- **Recommendation**: Define an explicit identity table for **logical events**, with recipient-scoped consumption. A final and its met verdict/task completion share one ending identity; a restart transition, later final, separate question/blocker, and separate Judge round do not. Include source stop/goal/message/run identity as applicable, retaining its helper namespace. Specify the mapping to the existing final poll marker without changing poll semantics. Test restart-wake → same-generation finish, two restart transitions, two questions and separate verdict rounds, each with duplicate-redelivery controls. Test 31's row-count assertions alone cannot prove this.

#### [NEW-MAJ-001] The durable carrier is placed in model history before the safe boundary

- **Severity**: MAJOR.
- **Origin**: New defect introduced by the round-one durability correction; not a reopening of its saved-bytes-before-ack requirement.
- **Certainty**: **Verified (high confidence)** existing append/placement contracts and the proposed membership; invalid ordering/refusal is **Inferred (high confidence)** for the specified live-approval case.
- **Lens**: Inconsistency & ADR/AS-IS contradiction; Infeasibility; Incompleteness; Security.
- **Affected section**: PW-D16; §9.4 live/held/polled branches; §9.5 carrier and steps 2–4; §9.9 first crash row; FR-005; Tests 6/20/21/22.
- **Failure scenario**: The parent has issued a tool call and is waiting on approval. A met helper result arrives. §9.5 tells the producer to append a role-user entry with model membership before acknowledgement/wake. A checked model append assigns model order now, potentially between the parent's assistant call and its later tool result, instead of at its safe consumption boundary. Using the ordinary addressed **transcript** append instead is refused because that API only accepts chat membership. The same prescription also appends model input after the result was already polled, although the spec says it was already consumed. The spec must not solve durability by prematurely consuming or duplicating input.
- **Evidence**: §9.5 names “one addressed-archive entry ... role user, view membership model” before WakeSession; the polled branch says to write it even when no new turn runs. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/transcript_indexed.go`::UnifiedStore.appendTranscriptLocked rejects non-chat membership on AppendTranscriptAddressed/Strict/Once. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/archive_backend.go`::archiveBackend.AppendModelMessage / PlaceSavedInput and newPayloadRecord distinguish immediate checked model append from later placement of saved input; PlaceSavedInput explicitly rejects an already-model-member source. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/ordinal_window_test.go`::TestOrdinals_PlacementOrderNotPhysicalOrder demonstrates required model order `user, assistant-call, tool-result, later-user`, even when later input was physically saved before the tool result. **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::D2 / U2-ARCHSHAPE, preserves this distinction.
- **Recommendation**: Specify two separate facts in the same existing archive: durable prepared bytes/source identity, then once-only model placement at the ordinary safe consumption/Stop fence. Name the accepted append/placement contract and the durable address/correlation needed by recovery; do not describe a chat transcript append as accepting model membership. Held/stopped/already-polled outcomes must not gain another model placement merely to retain verdict evidence. Alternatively, if a direct model append is chosen, move it into the actual safe consumer and defer acknowledgement until that write succeeds. Reconcile §9.9's “wake ... then the entry” wording with the chosen order. Extend Tests 6/20/21/22 to assert complete model message order, zero early placement while held, and one placement across polling and crash recovery—not just that bytes exist somewhere.

#### [MAJ-008] Repair depends on a source that acknowledgement can retire

- **Severity**: MAJOR.
- **Origin**: Round-one finding partially addressed.
- **Certainty**: **Verified (high confidence)** bounded inbox retention and missing repair-lifetime rule; inability of the specified driver to reconstruct the required row is **Inferred (high confidence)**. No claim is made that every copy of the work result is destroyed.
- **Lens**: Incompleteness; Testability & false-green risk; UI states & journey gaps.
- **Affected section**: PW-D18; §9.5 step 1; §9.9 repair driver; §9.3 question/blocker/failed/finished row IDs; FR-004, FR-008, FR-022; Test 34.
- **Failure scenario**: A required parent-chat row fails, but the parent receives/consumes the report and acknowledges its inbox message. Repair remains unsuccessful while other reports are acknowledged. Inbox compaction removes the original message before the next successful repair/restart. The named driver now has no guaranteed retained authoritative envelope/row payload to read, even though the missing row is still owed. Repairing a landed stop is different: its history remains on the control ledger. A successful stop-only repair test would miss the inbox-only case.
- **Evidence**: §9.9 says boot/next delivery pass read “retained authoritative events (the goal-outcome payload, the landed stop transition, the inbox message)”, but specifies no hold, durable pending descriptor or retirement condition for row repair. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/message_inbox.go`::DefaultInboxAckedRetentionMax is 200, DefaultInboxCompactionAckedTrigger is 400, and MessageInboxStore.compactLocked drops acknowledged message bodies beyond retention. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/boot_final_delivery.go`::SteerBootRecovery.runFinalDeliveryPass skips already-delivered/retired items; completion facts do not establish that every presentation row was repaired. The new Test 34 reopens stores but does not force source compaction/retirement.
- **Recommendation**: Define the authoritative repair source and its lifetime **for every event family**. A missing required row must retain a discoverable payload or a sufficient immutable reconstruction reference until all required destinations have succeeded; wake acknowledgement alone cannot retire it. Reuse existing owning stores and content/address mechanisms, not another message service. Add a question/blocker or other non-stop event case: fail the row, consume the wake, force real acknowledged-message compaction, reopen stores, invoke the actual repair driver, and prove one restored row with zero new turns. Include the corresponding goal/final payload-retirement boundary if that source is used.

#### [MAJ-010] The new UI state table is not reconciled with immutable rows or visible failure

- **Severity**: MAJOR.
- **Origin**: Round-one finding partially addressed; state names and journey were added, but their observable transitions remain incomplete.
- **Certainty**: **Verified (high confidence)** conflicting written rules/current insertion pattern; stale or indistinguishable UI is **Inferred (high confidence)**, not browser-tested.
- **Lens**: UI states & journey gaps; Contract-first gaps; Reachability.
- **Affected section**: §9.3 deterministic row/no-op rule; §9.6; §10; §18 states; Test 15/24/33/34; DOC-1.
- **Failure scenario**: A restarted helper is admitted but queued, so its one restart row says it is waiting for a slot. Promotion later runs the helper. §18 now requires that restart row to say it is continuing, but §9.3 makes a repeat write of that source event a no-op; no revision/effect/upsert rule exists. The parent/helper can keep an obsolete waiting statement after reload, or an implementation adds another restart row to show the change. Separately, when only the parent row fails, §18 renders no row and provides no user-visible failure signal; that is visually identical to “Nothing owed”, despite calling the absence a missing row.
- **Evidence**: §18 explicitly specifies waiting/running/refused sentences and “Parent row failed, helper row saved ... does not show a row”. §9.3 says “A repeat write of one source event is a no-op”. Its proposed HelperEvent/frame has no status revision/update semantics. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/lib/goalOutcome.ts`::buildGoalOutcomeInsertion—the reused row pattern—returns null for an already-present message ID. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/transcript_indexed.go`::UnifiedStore.AppendTranscriptOnce likewise does not update an existing row. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_outcome.go`::AgentLoop.recordGoalOutcome currently logs/counts a failed save without emitting that outcome frame; simply returning its error does not define a user-facing signal.
- **Recommendation**: Choose an explicit, minimal presentation rule. Either make the lasting row an immutable **past-tense event fact** and use existing Activity state for current queued/running status, or define a contract-first append-only correction/version and client update rule that still converges to one visible row. Do not silently overwrite retained history or treat changed state as duplicate delivery. Name the inherited user-visible nonfatal failure/recovery signal for an owed-but-unsaved row; absence alone cannot distinguish the two cases. Add queued → promoted → finished live/replay/reload parity and partial-parent-save tests in both render paths, proving one restart row and no extra wake. No brand redesign is needed.

#### [MAJ-012] UAT-4 still uses transcript deduplication as an invocation counter

- **Severity**: MAJOR.
- **Origin**: Round-one testability finding partially addressed; most automated-plan mappings are corrected.
- **Certainty**: **Verified (high confidence)** unchanged acceptance instrument; a false hands-on PASS is **Inferred (high confidence)**. No UAT run is claimed.
- **Lens**: Testability & false-green risk; Reachability.
- **Affected section**: §12 / Hands-on UAT row UAT-4; §11 exact-commit reachability; SC-005; Test 5.
- **Failure scenario**: Automatic resume executes the historical tool twice, but repeated writes share the historical transcript identity or the second transcript append fails. UAT-4 sees one success row and passes. The separate unit invocation counter is an improvement, but it does not make that hands-on observation an independent proof about the real candidate execution.
- **Evidence**: UAT-4 still says “The finished tool does not run again (**transcript shows one success, not two**)”. Test 5/SC-005 now correctly demand a separate executor/side-effect count; UAT-4 does not. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/transcript_indexed.go`::UnifiedStore.AppendTranscriptOnce demonstrates why one retained row and one operation are different counts. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/false-green-patterns.md`::Checklist / A stopwatch is not a proof of logic requires observing the actual discrete operation rather than a proxy.
- **Recommendation**: Amend UAT-4 to capture an independent invocation counter or observable append-only side effect from the isolated UAT fixture before and after the restart, correlated to the exact historical assistant occurrence/call. Keep the transcript check as supporting history evidence, not the oracle. Tester and independent validator must inspect that evidence on the exact candidate commit; a unit-test receipt cannot substitute for it. Do not accidentally forbid a genuinely new model decision, which PW-D21 explicitly allows.

### MINOR Findings

#### [MAJ-009] One old edge case still says a parked question joins a live turn

- **Severity**: MINOR — downgraded; original ID retained for traceability.
- **Origin**: Residual round-one inconsistency, not an open founder choice.
- **Certainty**: **Verified (high confidence)** contradiction in the fully read document; implementation confusion is **Inferred (medium confidence)**.
- **Lens**: Inconsistency & ADR/AS-IS contradiction; Ambiguity.
- **Affected section**: Edge Cases / “Approval or question card open on the parent”; PW-D15; §9.4; FR-020.
- **Failure scenario**: A reader derives the edge-case test/implementation from the retained sentence “Approval or question card ... outcome joins the live turn”, although the now-correct PW-D15/FR-020 require the question-card report to be held until the answer. This is a stale contradictory instruction, not a reason to ask the founder again.
- **Evidence**: That exact sentence remains in Edge Cases. The new parked-card BDD states no live turn and zero outcome-only turns. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/ask_user_wire_test.go`::TestRequestCancel_CancelsPendingAskWithoutActiveTurn confirms the source's parked-card/no-active-turn shape; the test was read, not run.
- **Recommendation**: Split the old edge-case sentence into approval-joins and parked-question-holds, matching PW-D15. Apply the same qualification to broad “every outcome starts a turn” summaries where necessary. No new policy interview is required.

### Observations

No new observations counted. The unnecessary row-written Boolean was removed, and the common wake entry remains justified by its named callers.

## Structural Integrity

These are document checks, not passing product gates.

| Check | Result | Evidence / disposition |
|---|---|---|
| Status field | PASS | Draft is valid. |
| Governing decision or explicit no-new-ADR rationale | PASS | Opening, PW-D22 and §16; dated ADR correction remains a declared follow-up. |
| Contract-first changes and ownership | PASS, with NEW-MAJ-001/MAJ-010 consequences | §9.1–9.3 define wire shape before §9.4 runtime behavior; §13 places backend-owned contracts/generation before consumers. Earlier as-is/impact explanation is not authorization to code before contracts. Any row-state update must also be shaped before consumers. |
| API and data section | PARTIAL | §9 exists; canonical event identity, archive placement and repair lifetime remain defective. |
| UI loading/empty/error/partial states | PARTIAL | §18 names states/inheritance; MAJ-010 supplies the remaining observable-state/transition gap. |
| End-to-end user journey | PASS | §18 now describes delegation through outcome/restart, Activity/Open and reload. |
| Accessibility and keyboard | PASS — written requirements | §19; not an executed accessibility acceptance. |
| Catalogue-first reuse | PASS | §20 and direct catalogue/domain-row inspection. No new public component claimed. |
| Security and user promises | PASS — explicit section present | §17 preserves Stop authority, filtering, edge ownership and tool policy. NEW-MAJ-001 still requires safe model consumption. |
| BDD acceptance coverage | PASS structurally | 7 stories, 40 acceptance scenarios, 38 BDD blocks; every acceptance reference represented and every BDD has a trace. |
| Requirement → scenario → test | PARTIAL substantively | 26 FRs and 34 unique planned tests are present; behavioral/documentation distinction is corrected. The counterexamples below and UAT-4 still need coverage. |
| Reachability | PASS section present; acceptance pending | §11 identifies actual parent chat, two browsers, partial stream, cold load and exact-commit tester/validator. No runtime PASS here. |
| Specific user-facing docs TODOs | PASS | DOC-1–3 name public destinations and now reflect PW-D13–15. Internal review is not the future docs-verifier audit. |

## Twelve-Lens Coverage

| Lens | Result / evidence |
|---|---|
| 1 — Ambiguity | MAJ-005 event identity; residual MAJ-009 sentence. |
| 2 — Incompleteness | MAJ-008 source lifetime; NEW-MAJ-001 save/placement distinction. |
| 3 — Inconsistency with ADR/AS-IS/code | NEW-MAJ-001 against the addressed archive and session-core D2; MAJ-009 against PW-D15. Current code still differs from proposed behavior intentionally; that alone is not a finding. |
| 4 — Infeasibility | NEW-MAJ-001's carrier cannot be sent through the named ordinary transcript append with model membership. The checked model alternative has immediate-placement semantics. No new runtime, unsupported platform or CGo requirement is proposed. |
| 5 — Contract-first gaps | Basic nine-kind HelperEvent/Message/frame direction is present. MAJ-010: changing a static row into live status needs an explicit update contract or immutable-event interpretation. |
| 6 — Security | Preserve all intentional Stop fences and selected execution/admission authority; no new tool policy or account boundary. NEW-MAJ-001 must not treat mere storage as authority for consumption. |
| 7 — Reachability | MAJ-005 can swallow the later required final turn; MAJ-010 can leave stale/invisible state; MAJ-012 can overstate hands-on proof. |
| 8 — UI states and journey | §18 closes the missing section; MAJ-010 remains on observable failure and state transitions. |
| 9 — Accessibility and keyboard | MAJ-011 closed by §19. Native disclosure, focus preservation and non-repeated announcements are specified; execution still pending. No additional unsupported accessibility defect is alleged. |
| 10 — Design-system reuse and brand | MIN-002 closed by §20/catalogue check. Existing tokens/icons, domain-row behavior and no-new-public-component choice are retained. No visual redesign is proposed by this review. |
| 11 — Testability and false-green risk | MAJ-012; add same-generation **wake** identity, safe model order, source-compaction and live-state transition checks. More labels alone are not coverage. |
| 12 — Overcomplexity | OBS-001 closed: Boolean removed. Prefer existing archive/addressed placement and Activity status over another queue, event service or speculative general framework. No new complexity defect is counted. |

## Test Coverage Assessment

| Category | What improved | Remaining correction |
|---|---|---|
| Crash durability | Tests 6/21 name real reopened stores and exact combined bytes before ack/marker. | Assert safe model order and once-only placement for live approval, parked/stopped/polled states; bytes existing alone is not consumption. NEW-MAJ-001. |
| Identity/deduplication | Test 31 distinguishes distinct **rows** and duplicate delivery; task-owned pairing is in Test 6. | Consume restart outcome, then same-generation final; consume two independent questions/verdict rounds; every actual event must retain its own wake identity. MAJ-005. |
| Stop/restart admission | Test 10 covers both Stop actors and all delivery modes; Test 12 names cleared-marker/pending Stop and PW-D14. | Execution must demonstrate the real admitted parent turn and a concurrent winning intentional Stop, not simply notifier success. This is an execution obligation for the repaired design. |
| Parked question / approval | Separate BDD/Test 20 states now match the runtime distinction. | Remove the old contradictory edge-case sentence. Held input must not already be model-placed under NEW-MAJ-001. |
| Repair | Test 34 runs a real boot/delivery pass after reopen and consumption. | Force acknowledged-message compaction/retirement before repair succeeds, with a non-stop event; source lifetime is part of the contract. MAJ-008. |
| Streaming / fan-out | Test 26 now uses two browsers and a held partial token before completion; creator_notice has explicit Test 32. | Keep actual execution receipts and positive destination controls; do not claim this review executed them. |
| Frontend / accessibility | Tests 33 and §§19–20 name cold/live/replay, both renderers, keyboard, focus, announcements, typecheck and design-system checks. | Add queued → promoted → finished and parent-row-failure observability, not just initial insertion. MAJ-010. |
| Resume safety | Test 5/SC-005 now count the actual historical invocation separately and allow reused provider IDs in new occurrences. | Apply an independent real-execution oracle to UAT-4 as well. MAJ-012. |
| Documentation | FR-024 no longer pretends to be a behavioral BDD test; DOC-1 reflects new founder answers. | Implementing leads draft and docs-verifier audits with the eventual behavior; those gates have not happened here. |

### Dataset Gaps

| Dataset | Missing boundary | Required evidence |
|---|---|---|
| Wake identity | A stop/restart and a later ending share helper/generation but are not one outcome. | Distinct wake/input keys; duplicate retries of each still coalesce. |
| Model history | Outcome arrives between assistant call and tool result, or is already polled/held. | User-call-result-user model order; no second placement merely for evidence retention. |
| Repair | More than the retained acknowledged tail accumulates while a row stays pending. | After real compaction/reopen, the production repair pass still has an authoritative source and restores one row with zero turns. |
| UI state | Restart resume queued, then promoted/running, then finished; one required destination fails. | One truthful row/current-status presentation through live/replay/reload and a visible failure/recovery signal. |
| UAT tool count | Two invocations, one retained success record. | Independent counter/side effect rejects the false one-row PASS, correlated to the historical occurrence. |

## STRIDE Threat Summary

STRIDE means spoofing, tampering, repudiation, information disclosure, denial of service and elevation of privilege. “ok” records a reviewed boundary or non-applicability, not a penetration-test PASS.

| Component / flow | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Outcome identity | ok | risk | risk | ok | risk | ok | MAJ-005 aliases actual events and can suppress owed compute; it does not prove deletion of the helper's work artifact. |
| Saved input and model placement | ok | risk | risk | ok | risk | risk | NEW-MAJ-001: storing a report must not prematurely consume input across the safe-boundary/Stop fence. Keep private payload/source separate from public Message projection. |
| Repair source and history | ok | risk | risk | ok | ok | ok | MAJ-008: acknowledgement/compaction must not remove the only specified repair source while a row remains owed. |
| Native restart / intentional Stop | ok | ok | ok | ok | accepted | ok | PW-D13/14 distinguish authority; implementation must preserve selected-execution races. Restart and real outcomes intentionally can spend a model turn, without a new hourly cap. |
| Live / REST / replay rows | ok | risk | risk | accepted | ok | ok | MAJ-010: stale/incomplete presentation; accepted broad authenticated-operator viewing is not reopened as a privacy defect. |
| Tool policy / external worker | ok | ok | ok | ok | ok | ok | No new tool permission; external CLI helpers remain non-auto-resumed and non-replaced. These are retained written constraints, not audited implementation results. |

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| New agent-facing tool to register? | N/A | WakeSession is internal. Existing delegate/message_parent front doors remain. No new policy map or fallback is warranted. |
| Named screen/component renders the feature? | Yes, existing chat | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/ChatScreen.tsx`::SystemMessage / VirtualSystemMessageRow and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/GoalOutcomeRow.tsx`::GoalOutcomeRow; §§10/19/20 require parity. New helper-event wiring is future work. |
| Cold-load and replay consumers identified? | Yes, design obligations stated | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/lib/api/sessions.ts`::rawToMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/store/chat/slices/replay-and-status-frames.ts`::handleReplayAndStatusFrame, goal_outcome branch; Tests 24/33 extend the pattern. |
| Real session-bound stream mechanism? | Verified current source | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/gateway/websocket_streamer.go`::WSHandler.GetStreamer refuses a non-webchat channel/empty session ID. Spec still commissions the ordinary-root channel correction. |
| Test plan requires execution? | Yes; not yet executed | §11/12 require CI and exact-commit tester plus independent validator. MAJ-012 repairs one of their proposed instruments. |
| Public docs named? | Yes | DOC-1–3: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/agents.md`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/goals.md`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/troubleshooting.md`. The existing agents/goals/security/tools promises were checked; this internal review does not replace their eventual updates. |

### UX heuristic assessment

**Several issues to address.** This is a written-flow review, not acceptance of a rendered screen.

| Heuristic | Result |
|---|---|
| H1 — Visibility of system status | MAJ-010: distinguish actual current Activity state from a lasting historical event; distinguish missing row from nothing owed. |
| H3 — User control and freedom | PW-D13–15 now settle Stop/card control. Preserve that contract at model placement, not only in the wake producer's precheck. |
| H4 — Consistency and standards | The reuse/accessibility sections now specify the intended shared behavior; no new visual convention is needed. |
| H9 — Error diagnosis and recovery | MAJ-008/010: the repair source must survive, and a logged pending failure is not automatically a user-visible recovery state. |
| H10 — Help and documentation | DOC-1 now reflects the actual founder choices; final queue/failure wording must match the selected presentation rule. |

**Priority Actions**: (1) separate event identity and safe consumption; (2) retain repairable history; (3) make UI transitions and UAT evidence truthful.

## Unasked Questions

These are technical corrections for the author/implementers, not new founder policy choices.

| Prompt | Options and recommendation |
|---|---|
| A1 — How is durable content separated from safe model placement? | A: **recommended**, save prepared content/source once in the existing archive and place it once at the ordinary safe boundary; B: perform the checked direct model append only in that consumer and defer ack until it succeeds. Both must preserve held/polled/Stop semantics. |
| A2 — What remains authoritative until every required row is repaired? | A: **recommended**, retain the existing owning payload/address or sufficient immutable reconstruction reference until all destinations succeed; B: explicitly gate its retirement/compaction on pending row repair. Name that lifetime and discovery rule; do not infer it from a log entry. |

## Questions for the founder

**None. PW-D13, PW-D14 and PW-D15 are settled; the remaining findings require technical specification corrections, not new founder policy choices.**

## Verdict Rationale

**REVISE** applies because five MAJOR findings survive and no CRITICAL finding was verified. The original critical ordering requirement is now explicit, but safe storage/model placement is a separate seam: one correct durable write does not justify injecting input into an unfinished tool group. Similarly, choosing a canonical key is not sufficient if its scope collapses distinct events. The remaining repair, UI and hands-on measurement defects are specific consequences of the revised text, not requests to reopen the founder's choices.

### Escalation to the founder

**Conditional final-round escalation candidates:** the author's correction round 2 has **not** happened yet. The entries below must be corrected before implementation readiness, or, if still open after that one correction, receive explicit founder disposition with the gap stated. This is not an instruction to run another grill or a claim that a post-correction verification already occurred. The residual MINOR MAJ-009 is not blocking alone.

| Finding | What would remain blocking after correction 2 | Founder disposition if unresolved |
|---|---|---|
| MAJ-005 | A valid later helper outcome can be suppressed by an earlier same-generation event. | Require event-scoped identity/correlation, or explicitly accept/defer the missed-outcome behavior with tracked ownership; do not call the guarantee met. |
| NEW-MAJ-001 | The durability carrier still violates existing safe placement/append contracts or adds duplicate consumed input. | Require a concrete save/placement/ack sequence and its assertions; any exception to the preserved session-core invariant needs explicit authority. |
| MAJ-008 | Required rows remain owed after their specified authoritative source can retire. | Require a retained, discoverable repair source/lifetime, or explicitly accept/defer the incomplete-history case. |
| MAJ-010 | Restart/current-state wording cannot converge under the specified row rules, or partial save failure is indistinguishable from nothing owed. | Require a coherent immutable-event/current-status or update rule and observable failure handling, or explicitly accept/defer the UI gap. |
| MAJ-012 | Hands-on acceptance can certify one historical invocation using only a deduplicated transcript row. | Require independent exact-candidate invocation/side-effect evidence, or explicitly record the UAT verification gap rather than claim proof. |

There is no new founder design interview question hidden in this table. It records the fixed process's disposition path if the final technical correction does not close a blocker. Any deferral requires explicit founder approval plus a tracked issue, owner and target date; no deferral is approved by this report.

### Recommended Next Actions

| Order | Action |
|---|---|
| 1 | Team-lead records that no new policy questions were found; the prior Q1/Q2 and no-Stop-wake answer remain settled. |
| 2 | Author makes the one final spec correction for MAJ-005/008/010/012, NEW-MAJ-001 and the residual MAJ-009 sentence; do not change unrelated scope. |
| 3 | If a blocking item remains after that correction, use the escalation table for founder disposition. No third spec grill. |
| 4 | Once disposition is complete, team-lead plans contract-first implementation, RED/GREEN/CHECK, the five-reviewer gate, CI, public docs audit, exact-commit tester/validator and founder landing approval. This review performs or waives none of those gates. |

### Next step in the process

Verdict: **REVISE**

Review written to: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/internal/specs/parent-wake-visibility-spec-review-round2.md`

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews the founder on "Questions for the founder" if a genuinely open question exists (this report lists none), then the spec author fixes round-2 findings. Any CRITICAL/blocking finding still open after that fix is listed under "Escalation to the founder" above for the founder to decide — do not run a third grill round. Once resolved, team-lead plans the implementation (RED / GREEN / CHECK, the 5-reviewer gate).

**Code correct and tested:** Not established; this is a source/specification review with no product test execution.

**Reachable by a user/agent:** Not established for the proposed behavior; actual execution and exact-commit UAT remain required.

skills: omnipus-shared-rules, grill-spec, gitnexus-exploring, ux-heuristics-review, omnipus-design-system, github-cli, jev-use:jev-use, commit-messages

## Evidence Table

| Claim | Evidence | Certainty |
|---|---|---|
| Reviewed the requested author fix after fast-forward | `git pull --ff-only`, exit 0: `Already up to date`; `git rev-parse HEAD`: `ba890752c33c50290893d0516e0bc83047d66b5e`. Initial status was clean. | Verified — high confidence |
| Runtime/method evidence is at the current HEAD, not a changed implementation | Scoped `git diff --quiet` from `bc7dc46ea9ae1486efdd89eeb1458fc23baf3e25` to HEAD over the absolute source/contract/root-guidance/grill-method paths under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/`, exit 0. Author-fix diff from `3f1b489ae` contains only the spec. Decisive source symbols were reopened. | Verified byte identity / direct reads — high confidence |
| Full corrected spec, prior report and fixed final-round method inspected | Target spec read through its final §21, prior report read completely; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/.claude/skills/grill-spec/SKILL.md`::Mode and round detection / Structural integrity / final-round output; report template read. Round-one review exists, round-two absent before writing. | Verified — high confidence |
| Structural census and negative searches used positive controls | Read-only `python3 -I` document census, exit 0: `stories=7; acceptance=40; bdd=38; FR=26; planned_tests=34`; `missing_acceptance_bdd=[]; missing_test_numbers=[]; bdd_without_trace=[]`. Known design/disposition headings and US-1 AS-1 found by the same check. `ack_retention_or_payload_lifetime_specified=False`; complete §9.9 also read. | Verified document checks only — high confidence |
| Reviewed artifact is pinned | Same census: spec SHA-256 `e331bba206349d4a42fe17d41dcf026038e592955faacb53e480b97436ef5861`. | Verified — high confidence |
| MAJ-005 collision is in the proposed formula, not a fabricated runtime observation | Read-only identity audit, exit 0: restart-stop-7 and final-after-resume both map to `child-A:1:outcome`, `distinct_events=2; keys=1`; event-qualified control distinguishes them. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steer_cancel.go`::reviveWithExecution and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/steering.go`::writeSteeringConsumedMarker read. | Verified formula/source; suppressed turn Inferred — high confidence |
| NEW-MAJ-001 storage and placement contracts differ | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/transcript_indexed.go`::appendTranscriptLocked; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/archive_backend.go`::AppendModelMessage / PlaceSavedInput / newPayloadRecord; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/ordinal_window_test.go`::TestOrdinals_PlacementOrderNotPhysicalOrder; session-core ADR::D2 / U2-ARCHSHAPE reread. Test fixture read, not run. | Verified contracts; failure consequence Inferred — high confidence |
| MAJ-008 acknowledged sources have bounded retention | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/message_inbox.go`::DefaultInboxAckedRetentionMax / DefaultInboxCompactionAckedTrigger / compactLocked; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/boot_final_delivery.go`::runFinalDeliveryPass; spec::§9.9 / Test 34. | Verified source and missing lifetime; repair failure Inferred — high confidence |
| MAJ-010 row rules cannot imply current-state updates or a visible error by themselves | Spec::§9.3 / §18; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/lib/goalOutcome.ts`::buildGoalOutcomeInsertion; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/session/transcript_indexed.go`::AppendTranscriptOnce; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/pkg/agent/goal_outcome.go`::recordGoalOutcome. | Verified rules/current pattern; UI consequence Inferred — high confidence |
| MAJ-012 and residual MAJ-009 survived the correction | Census, exit 0: `UAT4_transcript_only_counter=True; old_question_join_edge_case_remains=True`; target spec::UAT-4 / Edge Cases read; parked-card source fixture and false-green guidance reread. | Verified document omissions/contradiction — high confidence |
| Former companion warning resolved at the specified revision | `git show --stat --oneline 80559308f`, exit 0: creator-result-notice spec change; read-only Git-object extraction at `80559308f` of the companion `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec.md`, exit 0: §5.7 contains WakeParentAlways, live enqueue, never-turned initializer and 500-byte bound, with the ordinary-path non-edit boundary. Issue 1317 read via `gh issue view`, exit 0: open, matching live-stream symptom. | Verified source/revision, not FQ-4 execution — high confidence |
| Design/UX/public-doc closure checks were grounded | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/design-system/catalog.json`::entries; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/src/components/chat/GoalOutcomeRow.tsx`::GoalOutcomeRow; design-system definition, brand and UI guidance; agents/goals/security/tools public sections read. Full ADR titles and relevant clauses in §16 were reread; accepted operator viewing remains. | Verified written requirements/current patterns; no browser PASS |
| Judgment tooling did not substitute for source verification | Batched Jev closure triage exited 0; source-aware severity triage exited 3 with `escalated: true`, returning uncertain and higher-severity proposals. Reviewer resolved escalations/retained scope using the actual invariants and distinguishes lost delivery/row guarantees from destruction of all work data. Closure table is first-hand verification, not the preliminary label-only verdict. | Verified tool receipts; final judgments reasoned from cited evidence |
| Final report artifact checks | Read-only `python3 -I` audit, exit 0: `severity=0/5/1/0; closure=12_closed/5_partial/17_total; lenses=12; findings=6`; all 28 absolute path citations exist, input spec hash unchanged. Seeded missing-failure, missing-lens and trailing-whitespace copies rejected. `git diff --cached --name-status`, exit 0: only the final-round review added; `git diff --cached --check`, exit 0. | Verified document checks only — high confidence |
| **Self-check** | Checked all 17 original IDs against the corrected text and source: 12 closed, 5 partial; counted five surviving MAJOR and one downgraded MINOR once, including one newly introduced placement defect. Applied all twelve lenses and structural/test/STRIDE/reachability/UX/public-doc checks; kept PW-D13–15 settled, no founder policy question, conditional post-correction escalation and no-third-grill handoff. Re-read the report diff and reran artifact/scope/whitespace checks against the dispatch criteria before reporting; no product-green or live-instance claim. Commit/push identity checks accompany dispatch. | Verified review self-check; implementation readiness not established |
