# Feature Specification: PlanSupervisor — closing the plan correction loop

**Created**: 2026-07-27
**Revised**: 2026-07-28 (rev 4 — the plan-wake delivery fix, operator ruling 9: N15 promoted from a
recorded dependency to a first-class, fully-specified fix. Rev 3: closes spec-grill r3 and the
cross-spec conflict review.)
**Status**: Draft
**Input**: [ADR-055 — PlanSupervisor](../architecture/ADR-055-plan-supervisor.md) (v3+greenfield amendment, Proposed), plus its three adversarial reviews ([r1](../architecture/ADR-055-plan-supervisor-review.md), [r2](../architecture/ADR-055-plan-supervisor-review-r2.md), [r3](../architecture/ADR-055-plan-supervisor-review-r3.md) — verdict BLOCK).
**Reviews of this spec**: [spec-grill r1](plan-supervisor-spec-review.md) (4 CRITICAL / 10 MAJOR), [r2](plan-supervisor-spec-review-r2.md) (4 CRITICAL / 8 MAJOR), [r3](plan-supervisor-spec-review-r3.md) (5 CRITICAL / 16 MAJOR), plus the [cross-spec conflict review](cross-spec-conflicts-review.md) against `list-jobs-spec.md` (3 CRITICAL / 6 MAJOR). Disposition tables in §26.
**Branch**: `feature/plan-swimlane-board`
**Amends**: ADR-053 (correction handler, spec FR-143) and the plan-owner wake model. **Overrides** spec FR-146 `[P2]` and FR-147 `[P1]` (see § Upstream Decisions This Spec Overrides).
**Sibling spec**: [`list-jobs-spec.md`](list-jobs-spec.md) (ADR-056) lands in the same release and consumes this spec's phase vocabulary and tool catalog. Landing order and the shared prerequisites are in §18.

> ### The rev-3 through-line — assert the property, not the mechanism
>
> r3 found four defects that are **one mistake**: *a control whose test passes because the test
> asserts the mechanism we built rather than the property we want.* The kill switch asserted that a
> session id landed in a set, never that a turn stopped (C3-05). A dataset row asserted `jim →
> Allowed` for a tool that ships denied (C3-02). SC-011's regex missed a spelling, so "zero hits"
> was met while occurrences survived (M3-15). SC-022 read a counter that is reset before it is read
> (M3-04). The 2026-07-26 UAT produced the same shape in the field: a plan reporting *"Running
> 0/3"* forever with every test green, because nothing asserted that the plan made progress.
>
> **Standing rule for this spec and its implementation.** For every success criterion and every
> test, ask: *would this still pass if the mechanism were entirely broken?* If yes, the assertion is
> wrong and must be rewritten against the observable outcome. Concretely, in rev 3:
> SC-020 asserts the supervision **turn halts**, not that its id appears in a fan-out set;
> SC-004b asserts an agent that can **start** a plan can **stop** it, not that a seed literal has a
> key; SC-011 is a command that matches **both spellings** across **all** the directories that hold
> them; SC-022 counts **wakes actually issued**, not a counter that resets.

> **Rev 3 scope note.** Rev 3 is a revision, not a rewrite. All five r3 CRITICALs were local:
> three were contained in single requirements, one was a scoping decision (the stall limb, resolved
> in **D-01** below) and one was a missing mechanism (mint a real supervision session, FR-016b).
> The architecture, the `Plan.supervision` design, the `:1571` decision and the rubric (§27) are
> unchanged — r3 verified all four and they were deliberately not touched.

> **Evidence discipline.** ADR-055's architecture is settled and operator-approved, but its code
> citations have been wrong three times in three revisions. **Every `[FACT]` in this spec was
> re-verified by opening the file in the working tree at `feature/plan-swimlane-board` on
> 2026-07-27.** Claims inherited from the ADR or from a review without independent verification
> are marked `[UNVERIFIED]` and MUST NOT be built on. Where this spec contradicts ADR-055, this
> spec is authoritative and the contradiction is recorded in § Contradictions Found in ADR-055.
> **Rev 2 note:** citations flagged by the reviews were re-opened individually. Two review claims
> did **not** reproduce and the spec's original figures stand — see §26 "Review claims that did not
> reproduce". Counts are no longer quoted by hand anywhere a mechanical assertion can replace them.

> **Operator rulings applied in rev 2** (these postdate rev 1 and are binding):
>
> 1. **Containment is plan-scoped, not agent-scoped.** *"The killswitch is already foreseen — the
>    plan execution has in the UI a stop button that stops the plan and all children; that should
>    also apply to the executer agent. When the plan is executed from another chat, that agent needs
>    in its plan tools also the plan stop functionality."* Realised as US-8 / FR-042–FR-045.
> 2. **The rubric is written, not deferred.** *"Apply good prompt engineering techniques, we can
>    optimise it later but do your best."* Realised as Appendix A (§27); AMB-3 is closed.
> 3. **Greenfield — no data migration, anywhere.** No migrator, no `schema_version`, no
>    upgrade-on-read, for any store. Existing on-disk data is expected not to load; that is accepted.
>    ADR-055 D14 therefore ships in full. AMB-1 and AMB-2 are closed as **overruled**. *(Rev 3 amends the count, not the ruling: **row 3 is dropped** on naming grounds — D-02 — so **five** rows ship. The greenfield ruling is untouched.)*
> 4. **Proceed to a revised spec**, then re-grill.

> **Operator rulings applied in rev 3** (these postdate rev 2 and are binding):
>
> 5. **The kill switch must assert that it stops.** *"It needs to test it stops."* SC-020 and tests
>    #63/#64 assert the supervision **turn halted**, never set membership or a recorded string.
>    Realised as FR-016b (mint a real, store-backed supervision session), FR-044 (rewritten) and
>    SC-020 (rewritten). Closes r3 C3-05.
> 6. **Tool permission is configuration, and the rule is simple.** *"All agents that have now start
>    plan need to get all plan tools, particular stop."* Realised as **FR-006b**, stated as a rule
>    over the seed rather than a list of agents, so it survives a new agent being added. Closes r3
>    C3-02 and cross-spec C2a.
> 7. **Extend the rename test.** *"Extend the test."* SC-011's alternation missed the hyphenated
>    spelling and its file scope missed the two packages that compose the literal at runtime.
>    Realised as SC-011 (`awaiting.owner.correction`) and FR-062's widened directory list. Closes
>    r3 M3-15 and cross-spec C1.
> 8. **`blocked` is informational, not actionable.** *"Blocked are not so relevant because it means
>    they have not run yet, the executer cannot do anything about it, it is just information."*
>    Relevant to this spec only as the cross-spec consistency point recorded in §18's landing-order
>    note and FR-042's tool description (cross-spec M6). **No redesign follows from it here** — the
>    `blocked` normalisation is `list-jobs-spec.md`'s surface, not this spec's.

### Decisions taken in rev 3 where the review required an explicit choice

| # | Question | Decision | Rejected alternative | Where |
|---|---|---|---|---|
| **D-01** | r3 C3-01: the stall limb has no correction path — `AppendCorrection` rejects any phase ≠ parked, and `surfaceStallIfAny` sets `stalled` | **Widen the gate.** `AppendCorrection` accepts **both** `awaiting_supervision` and `stalled`; FR-021/022/023's predicates are restated over a named **supervision-eligible phase set**. | Dropping the `:1254` re-target and leaving the stall wake on the Owner. Rejected because FR-011 gives the Owner **no** correction role, so under that option a stalled plan has no corrector at all and its only exit is Stop or idle expiry — the exact gap §1 exists to close, and US-1 AS5 would have to be deleted. | FR-029 (new), FR-012, FR-021–023, E1, A28–A30, tests #71b/#71c |
| **D-02** | r3 C3-04: `Plan.OwnerSessionID` → `SupervisionSessionID` collides with the new `supervision.session_id` | **Drop S9 row 3.** `Plan.OwnerSessionID` keeps its name — it is already correct under US-7's canonical definition (the Owner's session for this plan). Row 7 still ships and is what disambiguates it. | Renaming it to a third name. Rejected as churn on a field that is already correctly named, on a wire-visible surface. | S9 rows table, §18 step 2, §26 |
| **D-03** | r3 M3-08: US-6's revision history has no read surface | **Extend the audit entry**, do not add a REST route. FR-039b's `plan.correct` entry gains the target member id and the falsified assumption, and US-6 AS1 / test #43 are restated against the audit log. | Promoting the plan-revisions read route from SHOULD to MUST. Rejected because O1's objections to a plan-mutation REST surface (no per-plan authorization on `HandlePlans`, no rate limit) apply to a read route's authorization story too, and the audit log is a shipped operator-readable surface (`GET /api/v1/audit-log`). | FR-039b, AMB-5 |
| **D-04** | cross-spec C3: PlanSupervisor is roster-blind and FR-008's `len(allowed)==1` forecloses granting `list_jobs` | **Accept, deliberately.** PlanSupervisor is woken per-plan and needs no roster; the engine's `supervision.wake_at` deadline is the only liveness control, and it is the **engine's**, not the agent's. | Granting `list_jobs` and relaxing FR-008 to `len(allowed) == 2`. Rejected: it widens the most privileged agent's surface for a capability no requirement needs, and the complement-complete assertion is the guard doing its job. | FR-008, §10, §24 |
| **D-05** | r3 M3-07: FR-022's second trigger (a rejected `plan_correct`) is unbounded per turn and contradicts Dataset E6 | **Delete the rejection trigger.** A rejected correction mutates nothing, so it is detected by the deadline exactly as silence is — which is what E6 already said. `attempts` increments at most once per supervision wake. | Keeping the trigger with an "at most once per turn" clamp. Rejected: it needs a turn-boundary the engine cannot observe (N8). | FR-022, E6 |
| **D-06** | r3 O3-02: six new tunables ship at once | **The four payload caps become package constants** in `pkg/plan`; only the two supervision timings stay configurable. | Shipping all six as `config.PlanningConfig` fields with per-plan `PlanBounds` overrides. Rejected: no operator use case was stated for varying a title-byte cap per plan, and each override is a wire addition plus a resolver. | FR-046 |
| **D-07** | r3 O3-03: the headline SC is asserted by a real-LLM E2E test inside a blocking merge gate | **Split the claim.** SC-001 (the loop closes) is the merge gate and is driven by a **scripted adjudicator double**; SC-001b (the rubric works) is the real-LLM run and is a nightly signal. | Leaving one non-deterministic test in the blocking gate with a 2-retry policy. Rejected: a provider outage should not block a merge, and the two claims are separable. | SC-001, SC-001b, RISK-11, tests #60/#60b |
| **D-08** | **N15 (new in rev 3, found while verifying C3-05):** every `wakeOwner` call is dropped by `processSystemMessage`'s internal-channel guard before a turn runs, so no plan wake reaches an agent today | **Fix it here.** FR-012c requires the supervision wake — and the Owner wakes US-4 measures — to be delivered by a path that demonstrably **runs a turn**, bound to the engine-minted session, and SC-025 asserts a turn ran rather than that `Notify` was called. **Rev 4 keeps this decision and supplies the mechanism it left open** — see D-09/D-10/D-11. | Recording it as a pre-existing defect and deferring to a separate issue, as O12 does for N14's ceiling. **Rejected on a stated test:** defer only what no requirement *in this spec* depends on. US-4 AS1, SC-010, FR-012, FR-019 and the entire supervision loop depend on a plan wake reaching an agent; `execute_plan`'s ceiling has no dependent here. Shipping FR-012's routing over a no-op delivery would be the exact false-green this revision exists to remove. | N15, FR-012c, SC-025, §1, tests #31b/#33b |

### Decisions taken in rev 4 — the plan-wake delivery fix (operator ruling 9)

> **Operator ruling 9 (binding, postdates rev 3).** On the plan-wake delivery defect: *"not filing,
> add it to the spec, we have to solve it now."* The defect is **in-scope work that must be solved
> in this release**, fully specified rather than recorded. Rev 3 decided *that* it is fixed here
> (D-08) and stated the fix as properties; rev 4 supplies the mechanism, the origin data model, the
> no-origin behaviour, and the end-to-end assertions.
>
> **Operator ruling 10 — a chat origin is optional, and that is not a problem to be solved away.**
> *"a plan in the ui has no chat origin, that is right — why is that a problem? tasks can have an
> origin but must not. same principle."* `Task.SourceChannel`/`SourceChatID` are both `omitempty`
> `[FACT — `pkg/task/task.go:307-309`]` and `Plan`'s are too. **No mandatory fallback, no synthetic
> origin, no invented destination.** An origin-less plan is a legitimate, expected, first-class
> state. What the spec owes is not a fallback but an *explicit statement of what happens* — realised
> as **D-10** (option (b): unreachable by construction), **N16**, FR-012d(4), E38/E41 and test #31d.
>
> **Operator ruling 11 — N13 is in scope; fix it now.** The `plan:<id>` owner session the plan record
> names is never created, so `StopPlan`'s owner-session cancel leg has always been a no-op, and the
> existing test passes because its fake canceller records the name and returns success. *"Fold it
> into the FR-016b work you already have … rather than adding a parallel requirement"*, and the
> success criterion must assert the turn **terminated**, not that an id reached a canceller. This is
> **the same defect shape as the wake bug** — a field is set, the thing it names was never created,
> and the test asserts the label rather than the outcome. It is load-bearing because operator
> ruling 1's kill switch (*stopping a plan stops its supervision*) depends on that leg working.
> Realised as **FR-016c** — written as FR-016b's mechanism applied to the second session, one
> implementation unit, one PR — plus SC-020's new limb (e) and test #63c. O11 is **closed**, not
> narrowed.

| # | Question | Decision | Rejected alternative | Where |
|---|---|---|---|---|
| **D-09** | N15's mechanism was specified as properties only. **How** does a plan wake reach a turn? | **Give `Plan` the origin it never had, and propagate it.** `Plan` gains `SourceChannel` / `SourceChatID` (contracts-first, `Plan.yaml`), populated at creation on **both** write paths, and `wakeOwner` takes its `AsyncNotifyEvent.Channel`/`ChatID` from the plan instead of hardcoding `"system"` / `"plan:<id>"`. This is the exact in-tree precedent at `pkg/task/task.go:307-309` → `task_executor.go:1066`, and it makes `plan_engine.go:2103` stop being the only hardcoded `Notify` origin in the tree. | (a) Special-casing `"plan:"`-prefixed chat ids inside `processSystemMessage`'s internal-channel guard. Rejected: it widens a shared guard for one caller's benefit (RISK-15) and leaves `Plan` still carrying no origin, so the Owner wake still has nowhere to deliver. (b) Changing the **bus** `InboundMessage.Channel` away from `"system"`. Rejected as **wrong**: `loop.go:5515` routes on exactly that value to reach `processSystemMessage`, which rejects any other channel at entry (`:5992-5997`). The bus channel is correct and required; only the *event's origin* channel is wrong. Two prior analyses got this backwards — do not re-derive it. | FR-012c, FR-012d, §11.2, §18 step 1(h) |
| **D-10** | A plan created from the Plans UI (REST) has no chat origin at all. What happens to its wakes? *(Operator ruling 10: this is a legitimate state, not a defect — do not invent a fallback.)* | **Option (b): the chat leg is unreachable by construction, and everything else is unchanged.** `wakeOwner` MUST NOT construct a chat-origin wake when the origin is not fully populated — it takes the non-chat path directly. Concretely: the Owner turn still **runs**, dispatched directly (engine → `AgentLoop`, `SendResponse: false`) and bound to the plan's owner session, so the closing synthesis is authored and **persisted**; the human-facing surface is the `plan_completed` / `plan_failed` notification with `plan_id` click-through (FR-014/FR-017), which is what a UI user is actually watching; and **supervision wakes are unaffected**, because family A never uses the origin at all (D-11). "No chat to deliver to" MUST NOT mean "no turn ran", and MUST NOT mean "the plan is unhealthy". | (i) **Falling back to the owner's default route / last-active channel** (`RecordLastChannel`). Rejected: it drops a plan's outcome into whatever unrelated conversation the owner agent last spoke in — the same cross-context leak class H8 exists to prevent — and the last-active channel has no relationship to the plan. (ii) **Requiring an origin / minting a synthetic one.** Rejected by operator ruling 10 and by the `Task` precedent, where both fields are `omitempty`. (iii) **Option (a) — attempt the chat leg and skip it on failure.** Rejected: it routes a healthy plan through `Notify`'s FR-N7 rejection into FR-024's escalation ladder (**N16**), and "we attempt it and it fails harmlessly" is a mechanism assertion, not a property. | FR-012d(4), **N16**, E38, E41, SC-026, test #31d |
| **D-11** | The **supervision** wake cannot use the plan's chat origin — FR-016/H8 forbid PlanSupervisor's deliberation reaching the Owner's conversation — yet it must still run a turn. | **Fork by wake family, as FR-012/S4 already forks them.** Owner wakes (`:1571`, `:1610`, `:1742`) use the notifier/bus with the plan's real origin and `SendResponse` on. Supervision wakes (`:1254`, `:1542`) **do not use the bus at all**: the engine dispatches the turn directly against `supervision.session_id` with **no outbound publish**, the `processTaskDirect` precedent FR-012c already names. `PlanEngine` already holds `agentLoop *AgentLoop` (`plan_engine.go:188`), so no new seam is created. | Routing the supervision wake over the bus with `Channel: "webchat"` and `ChatID: supervision.session_id`, relying on `webchatChannel.Send` returning `ErrSendFailed` because no browser tab is bound to that id. Rejected on this spec's own standing rule: a non-leak property whose enforcement is *a send failing* asserts the mechanism, not the property — and it silently starts leaking the moment anything attaches to that session id. | FR-012c(A)/(B), FR-016, H8, test #31b |

---

## 1. Problem Statement

ADR-052/053 gave plans an autonomous loop: members dispatch off a DAG, a per-task judge evaluates
acceptance criteria, and a plan-level judge evaluates the Definition of Done (DoD). When the DoD
is unmet the plan parks at `plan_phase = awaiting_owner_correction` and the engine wakes the
plan's owner agent to correct it.

**The loop does not close — for two independent reasons, and rev 3 found the second one.**

**Cause 1 — nothing can apply a correction.** `AppendCorrection` (`pkg/agent/plan_engine.go:2574`)
is fully implemented, transactional and tested, but has **zero non-test callers** `[FACT — verified:
no reference to `AppendCorrection` or `CorrectionRequest` exists in `pkg/gateway/`, `pkg/tools/`
or `contracts/`]`. Spec FR-143 marks the wiring `[P2]`, so this was deferred by design.

**Cause 2 — nothing is ever woken to decide.** `wakeOwner` publishes with `Channel: "system"`, the
notifier composes the bus `ChatID` as `"system:plan:<id>"`, and `processSystemMessage` parses
`"system"` back out and **returns before dispatching any turn**, because `system` is an internal
channel `[FACT — verified verbatim end to end; see **N15**]`. So all five plan wakes — stall, UNMET,
MET synthesis, failure and stop — currently produce one INFO log and nothing else.

**Cause 2's root cause is a missing field, not a wrong constant.** `Plan` carries no origin
channel/chat, so `wakeOwner` had nothing real to pass and a synthetic internal destination was the
only thing available to hardcode. Every *other* `Notify` caller in the tree propagates a real origin
it was given — `task_executor.go:1077` (`t.SourceChannel`), `loop.go:8258` (`ts.channel`),
`goal_triggers.go:585` (`route.channel`); `plan_engine.go:2103` is the only hardcoded one
`[FACT — verified 2026-07-28]`. The fix is therefore to give `Plan` the origin it never had
(FR-012d) and let `wakeOwner` propagate it (FR-012c), not to special-case a channel name.

**The two causes are co-equal, and neither is sufficient alone.** Even with a correction tool wired,
no agent is ever woken to call it; even with a wake delivered, there is no tool to call. **This spec
fixes both** — cause 1 via the `plan_correct` tool and the authority gate, cause 2 via FR-012c /
FR-012d / FR-016c. Rev 2 diagnosed only cause 1, which is why its wake-routing requirements (FR-012)
were correct about *whom* to address and silent about whether the address resolves; rev 3 found
cause 2 but specified it as a property only, leaving the mechanism — and the case of a plan with no
chat origin at all — undetermined. Rev 4 specifies both.

Where the 2026-07-26 UAT is referenced anywhere in this document, **cause 2 is a co-equal
explanation with cause 1** for the observed *"Running 0/3"* / parked-forever shape. A plan that
parks and is never woken looks identical, from the board, to a plan that is woken and has no tool to
call. Attributing that shape to `AppendCorrection`'s missing caller alone was rev 2's error and is
retracted. (This does not change the discipline in the next subsection: the UAT is still not offered
as *causal evidence* for shipping the feature.)

Consequently a plan whose DoD is unmet has exactly one exit: stop it and re-author from scratch,
discarding every completed member. The shipped UI says so literally — `src/lib/planStateColors.ts:213`
and `:234` both read *"There's no in-app action for that yet — Stop this plan (■) and create a new
one with the fix instead."* `[FACT — verified]`

This spec delivers **PlanSupervisor**: a new System Agent that adjudicates the plan DoD and
applies corrections through the existing `AppendCorrection` handler, plus the authority-gate fix,
wake-routing split, outcome-delivery fork, and vocabulary correction that make it work.

### What this is NOT justified on

The 2026-07-26 UAT (2 of 11 plans reaching `done`) is **not** offered as causal evidence. The same
report diagnoses an independent dispatcher defect (`inbox`→`next` members never promoted), fixed
2026-07-26 and out of scope here. This spec is justified on the **verified capability gap**, not
on a projected completion-rate improvement.

---

## 2. Scope

### In scope

| # | Item |
|---|---|
| S1 | A new System Agent `plansupervisor` — roster entry, seed, tool policy, skill grant, SOUL |
| S2 | A new agent-facing tool `plan_correct` exposing `append` / `supersede` / `targeted_retry` / `abandon`, with a fully specified parameter schema and payload validation |
| S3 | The `requireOwner` authority-gate fix so PlanSupervisor (and only PlanSupervisor) may correct |
| S4 | Wake-routing split: **correction-decision** wakes (stall, UNMET) → PlanSupervisor; every other wake (MET synthesis, failure, stop) stays on the plan's Owner |
| S5 | Outcome delivery routed on the **existing two fields** — `owner_agent_id` (required, always an agent) for the guaranteed bus delivery, `owner` (attribution) for the optional human notification. **No new discriminator, no migration.** |
| S6 | Restart re-wake for the parked phase (`processPlan` boot case) + notifier failure escalation |
| S7 | `supersede` integrity guard (must carry replacement work, and the replacement must inherit the superseded member's criteria) |
| S8 | System agents rejected as chat targets and as the starred default agent |
| S9 | Vocabulary correction — **five of the seven ADR-055 D14 rows** (row 2 was already out of scope; row 3 is dropped in rev 3, see D-02). See the **S9 rows table** below. |
| S10 | **The durable `Plan.supervision` state object** — wake receipt, wake failure + attempt count, supervision attempt count, correction-round count, supervision session id — plus the matching **`plan.Patch` write path** (FR-050). One contract addition; it is the load-bearing state for FR-021 through FR-029. |
| S11 | `plan/SKILL.md` amendments (`:158` phase literal, `:181` verb-table pairing rule, a new **ABANDON** verb-table row, `:231-232` anti-pattern line) |
| S12 | Notification contract widening + `plan_id` + engine injection point |
| S13 | SPA parked/stalled copy revision |
| S14 | **Plan-scoped stop containment** — a `stop_plan` agent tool seeded wherever `execute_plan` is (FR-006b), a **real, store-backed supervision session** (FR-016b), and `StopPlan`'s cascade extended to **halt** PlanSupervisor's in-flight supervision turn on that plan |
| S15 | **Three closed wire enums widen.** Two new `failed_reason` values — `dod_unreachable` and `supervision_unavailable` — so the terminal causes this feature introduces are machine-distinguishable rather than string-distinguishable; **and `RevisionEntry.verb` gains `abandon`** (`contracts/components/schemas/RevisionEntry.yaml:40-45` is a closed three-value enum generating `RevisionEntryVerb.Valid()`, mirrored by `pkg/plan.RevisionVerb` at `intent_log.go:80-85`) `[FACT — verified 2026-07-27]`. All three land in §18 step 1. |
| S16 | **The stall limb made actable** (D-01): `AppendCorrection`'s phase gate widened to the supervision-eligible phase set `{awaiting_supervision, stalled}`, and FR-021–023's resilience machinery restated over that set so a stall wake arms a deadline, counts attempts and hits a ceiling exactly as a parked wake does |
| S17 | **The plan-wake delivery fix (NEW in rev 4, operator ruling 9 / D-09–D-11).** `Plan` gains `source_channel` / `source_chat_id` (one contract addition, folded into §18 step 1); both plan-creation write paths populate them; `wakeOwner` propagates them instead of hardcoding an internal origin; supervision wakes dispatch directly with no outbound publish; the plan's owner session becomes **real** so an Owner wake's synthesis is persisted even when there is no chat to deliver to (FR-012c, FR-012d, FR-016c). Without this, **no plan wake has ever started an agent turn** (N15) and the entire supervision loop ships over a dead rail |

### S9 rows — the D14 renames, in full

ADR-055 D14 ships under the greenfield ruling. **Five rows land in this release**: rows 1, 4, 5, 6
and 7. Row 2 was removed from scope by ADR-055 v3 and by this spec's O3; **row 3 is dropped in
rev 3** (D-02). Every row's *before* identifier is enumerated here so an implementer never has to
open the ADR — the document §5 declares unreliable. Verified in the working tree 2026-07-27.

| S9 row | Before | After | Live production consumer | Wire break | Notes |
|---|---|---|---|---|---|
| **1** | `awaiting_owner_correction` (`plan.PhaseAwaitingOwnerCorrection`, `pkg/plan/plan.go:237`) | `awaiting_supervision` (`plan.PhaseAwaitingSupervision`) | `validPlanPhases`, `AppendCorrection`'s phase check (`plan_engine.go:2591-2593`), `surfaceStallIfAny`'s precedence guard (`:1225-1230`), `boot_sweep.go:161`'s `planIsAwaitingOwnerCorrection` | **yes** — enum + persisted, 5 contract files | The operator-locked item. Also the phase literal at `plan/SKILL.md:158` (no compiler sees it). **The retired vocabulary ships in TWO spellings** — `awaiting_owner_correction` *and* the hyphenated `awaiting-owner-correction` (upstream spec FR-141 hard-codes the hyphen form; it is live at `plan_engine.go:1709` and `:3171`, in `boot_sweep_test.go`, and in the **title** of the e2e test at `tests/e2e/conformance-design-e2e.spec.ts:624`). No occurrence count is quoted here: SC-011's command, which matches **both** separators, is the only measure (FR-062). Rev 2's hand-quoted "17" counted one spelling and is retracted — see §26. |
| ~~2~~ | ~~`Plan.OwnerAgentID`~~ | — | — | — | **REMOVED FROM SCOPE** by ADR-055 v3 and by this spec's O3. Neither deleted nor renamed. |
| ~~3~~ | ~~`Plan.OwnerSessionID`~~ (`pkg/plan/plan.go:392-401`) | — | — | — | **DROPPED IN REV 3 (D-02).** The field keeps its name. Rev 2 renamed it to `Plan.SupervisionSessionID` while FR-050 simultaneously added `supervision.session_id` for a **different** session — two fields both reading as "the supervision session", inside the story whose sole purpose is to make one name mean one thing. Under US-7's canonical definition (*Owner = the principal accountable for a thing*) `OwnerSessionID` is already accurate: it is the **Owner's** session for this plan, minted by `ensureOwnerSessionLocked` (`:2469-2474`) and read by `requireOwner` clause 3 (`plan_engine.go:2765-2768` — rev 2 cited `:2769-2772`, which is `return nil` and the closing brace). The rename was inherited from ADR-055 D14's premise that the **Owner** supervises — a premise FR-011 overturns. **Row 7 still ships**, and it is row 7, not row 3, that disambiguates the name: after it, `OwnerSessionID` means exactly one thing. Record the D14 deviation in §26. **See also N13** — that session is currently inert. |
| **4** | `OwnerScopeKind` / `OwnerScopeID` (`pkg/session/lifecycle.go`) | `ScopeKind` / `ScopeID` | **`boot_sweep.go:295-296`** — the single live gate: an empty kind returns `false` and the session is swept to `failed(interrupted)`. Also `lifecycle.go:416-418`, where `persistLocked` hard-rejects an empty kind. | yes — lifecycle records | **This is the risky row, and the one to test.** It is the *only* live protection for a parked `needs_input` session. The rename is a struct-field rename, so the Go compiler is exhaustive over it — but `boot_sweep.go:295`'s behaviour must be regression-tested (R1.4). |
| **5** | `OwnsPlanID` (`pkg/session/lifecycle.go:199`) | `SupervisedPlanID` | **None in production.** Verified: the only production *read* is `boot_sweep.go:160-161`; **every** assignment in the repo is in `boot_sweep_test.go` and `conformance_design_test.go`. | minor | **Dead-but-renamed.** Because nothing writes it, boot-sweep **exemption (b)** (`rec.State == LifecyclePaused && rec.OwnsPlanID != ""`) never fires in production. The rename is free; the *exemption* is not live. R1.5 is a synthetic-fixture row, not a live regression. |
| **6** | `ownerKey` (`pkg/session/message_inbox.go:153,:184-199`, `sanitizeOwnerKey`) | `scopeKey` (`sanitizeScopeKey`) | internal only | **no** | Free and unambiguous — a local identifier, not persisted under that name. |
| **7** | `ProcessSession.OwnerSessionID` (`pkg/tools/session.go:103`) | `ProcessSession.TranscriptSessionID` | `pkg/tools/session.go:455`, `pkg/tools/shell.go:1014` | **no** — in-memory only | Free and unambiguous — the field is literally assigned from `ToolTranscriptSessionID(ctx)`. This is the rename that **disambiguates** row 3: after it, `OwnerSessionID` means exactly one thing. |

> **No migrator, for any row.** Greenfield (operator ruling 3). Pre-rename records are expected not
> to load; that is accepted, not a defect. Any existing dev/UAT `$OMNIPUS_HOME` is recreated, not
> upgraded. Do **not** reintroduce a migration step "to be safe" — a half-migration is worse than
> none.

### Explicitly out of scope

| # | Item | Why |
|---|---|---|
| O1 | **A REST correction route** | FR-5 of ADR-055 deleted it. It has no SPA client, `HandlePlans` has no per-plan authorization to inherit, and it would hold the process-wide `planDecisionMu` unrate-limited. Human correction parity is its own decision — see § Deferred: Human Correction Parity. **ADR-055 §9 step 3, §1 blast radius and §5 Option B still describe this route; they are wrong.** |
| O2 | Member-level manual retry for any actor | Operator decision (ADR-055 FR-10) |
| O3 | Deleting or renaming `Plan.OwnerAgentID` | Operator decision; `required` under `additionalProperties: false` in two schemas; carries eight live jobs including `requireOwner`'s authority subject |
| O4 | Deleting `OwnerSessionID` / `OwnsPlanID` / `plan:<id>` linkage | Contradicts spec FR-118/FR-147 `[P1]`. `OwnsPlanID` is renamed (S9 row 5); `OwnerSessionID` is **neither deleted nor renamed** (D-02). Neither is deleted. |
| ~~O11~~ | ~~**Making `Plan.OwnerSessionID`'s `plan:<id>` session real**~~ → **REMOVED FROM THIS TABLE IN REV 4. It is IN SCOPE (operator ruling 11, FR-016c).** | **N13 records that nothing in `pkg/` ever creates it**, so `requireOwner` clause 3 and `StopPlan`'s owner-session cancel are both production no-ops today. Rev 3 excluded it on the grounds that *"none of which this feature needs"* — **which was wrong on two counts, and the operator has ruled it in.** (i) FR-012c(B) requires an Owner wake to run a turn whose synthesis is *persisted*, and `processSystemMessage` persists only to a `TranscriptSessionID` that `ResolveSessionStore` resolves (`loop.go:6072-6085`) — so an origin-less plan (D-10) has nowhere to write and H9 fails. (ii) Operator ruling 1's kill switch depends on the owner-session cancel leg, which is exactly the leg that does nothing. **In scope, as FR-016c** (FR-016b's mechanism applied to the second session — one PR). **Still out of scope, stated so it is not assumed fixed:** `requireOwner`'s clause-3 semantics (FR-009's early return keeps it non-load-bearing), and writing `LifecycleRecord.OwnsPlanID` — nothing writes that field in production (S9 row 5), so boot-sweep exemption (b) stays dead. That is the sole N13 residual; RISK-13 carries it. |
| O12 | **Fixing `execute_plan`'s global ceiling** | **N14 records a pre-existing contradiction**: `defaults.go:415-417` seeds the ceiling `ask` for `create_plan`/`execute_plan`/`run_task` while `core.go:814-816` seeds Jim `allow`, and strictest-wins merges Jim down to `ask` — so Jim's documented "unprompted plan execution" does not survive resolution. `defaults.go:375-386` flags it in-tree as unresolved and *"outside this file's ownership"*. It is an ADR-052 defect, not this spec's, and this spec does not fix it. **It does constrain this spec**: `stop_plan`'s ceiling is `allow` (FR-006), which is what makes Dataset B11 (`jim` → Allowed) reachable — see FR-006b. Filed as RISK-14. |
| O5 | Correction rollback | Corrections are additive and `done` records immutable; recovery stays stop-and-re-author |
| O6 | Task-level judging changes | Unchanged (ADR-055 FR-1) |
| O7 | **Exponential backoff** on a supervision re-wake | The re-wake cadence is one attempt per tick (30 s) up to a hard ceiling (FR-022, FR-027). A backoff curve is deliberately excluded — the ceiling, not the curve, is what bounds the cost, and a bounded per-tick retry is simpler to reason about at 3 AM. **This is narrower than rev 1's O7**, which excluded retry entirely; r2 C2-02 showed that "no retry" strands the plan on the first bad tool call. |
| O8 | An ambient (always-visible) notification indicator | Flagged as a UX gap (see AMB-7); the existing Tray entry point is accepted for this release |
| O9 | **A global "disable PlanSupervisor" switch** | Containment is **plan-scoped** (operator ruling 1, US-8). A global agent-disable is additionally *unbuildable*: `updateAgentTools` (`pkg/gateway/rest.go:6789-6793`) returns **403** for a `Locked` agent, and `seedSystemAgents` re-stamps the seeded policy map on **every** boot — which FR-002 mandates and test #8 asserts. Do **not** carve a `Locked` exemption: it would need carving in two places and would weaken the System-Agent invariant for every future System Agent. |
| O10 | **Human (SPA) correction parity** | Deferred with a stated checklist (§19). No SPA correction UI ships; the only correction actor is PlanSupervisor. Human *containment* does ship — the existing Stop button, extended by FR-045. |

---

## 3. Upstream Decisions This Spec Overrides

ADR-055 overrides one `[P2]` MUST explicitly (FR-146) and silently collides with several more. Every
collision is recorded here with a verbatim quote, the override, and the rationale — the treatment
ADR-055's D1 gives FR-146, applied to each. All quotes verified at
`docs/internal/specs/unified-goal-plan-subagent-spec.md` on 2026-07-27.

| Upstream | Verbatim (abridged) | Disposition |
|---|---|---|
| **FR-146** `[P2]` (`:1521`) | *"The planner/re-planner behavior MUST be delivered by EXTENDING `pkg/skills/embedded/plan/SKILL.md` … — **never a new Planner agent (BOM)**"* | **OVERRIDDEN.** The *capability* is reused exactly as intended — PlanSupervisor is granted `plan/SKILL.md` rather than re-implementing planning. Only the *actor* differs, because (a) correction is a privileged verb needing exactly one holder and an agent is the only thing that can hold a tool, and (b) the alternative is whichever agent was named at create time — the dropdown lottery this feature exists to remove. Annotate FR-146 to point here (FR-041). |
| **FR-147** `[P1]` (`:1522`) | *"the system MUST persist **`plan_phase=awaiting_owner_correction`** (a NEW value on the existing persisted `Plan.PlanPhase` field)"* | **OVERRIDDEN on the literal only.** Every structural requirement of FR-147 is preserved: the phase stays a durable persisted plan condition, the owner session stays at lifecycle `paused`, and the `owner_session_id` ⟷ reciprocal `plan_id` linkage is retained (renamed per **S9 rows 3 and 5**, never deleted). Only the string changes, to `awaiting_supervision`, because under this spec the Owner never corrects. **No migrator** — greenfield (operator ruling 3). |
| **FR-193** `[P1]` (`:1553`) | *"The boot sweep MUST NOT spuriously re-arm a re-judge of `awaiting-owner-correction` state — the persisted `last_unmet_terminal_signature` MUST be honored at boot … and the `paused` awaiting-correction owner session MUST be exempt from the `failed(interrupted)` sweep"* | **PRESERVED, and strengthened.** FR-023's restart re-wake MUST be idempotent and MUST NOT burn a judge round — it re-issues the supervision wake without re-judging, and dedups on the new `supervision.wake_at` receipt (FR-021), **not** on the unmet signature (which carries no wake information — see FR-023's note). The signature gate at `plan_engine.go:1293-1301` is retained unchanged; `bootReconcile` already rehydrates the persisted signature at `:3198-3199` `[FACT — verified]`. The rename touches the exemption's field names (**S9 rows 3, 4 and 5**); row 4 is the live one and row 5's exemption is dead in production — see the S9 rows table. |
| **FR-141** `[P2]` (`:1516`) | hard-codes the literal `awaiting-owner-correction` | **RENAMED** with FR-147; behaviour unchanged. |
| **FR-186** `[P2]` (`:1558`) | *"`re-planning` MUST be sourced from the **durable** persisted `plan_phase=awaiting_owner_correction`"* | **RENAMED** with FR-147; the pill mapping moves to `awaiting_supervision`, behaviour unchanged. |
| **FR-140** `[P2]` (`:1515`) | *"Every plan MUST run inside a persistent owner agent session"* | **PRESERVED.** `OwnerAgentID` and `ensureOwnerSessionLocked` are untouched (O3). |
| **FR-118** `[P1]` (`:1486`) | resolves the boot-sweep exemption *"through the named plan↔owner-session linkage (`Plan.owner_session_id` / the owner session's reciprocal `plan_id`)"* | **PRESERVED in code, with a correction of record.** Renamed (**S9 rows 3 and 5**), never deleted. But the linkage is **not live**: verified 2026-07-27, `session.LifecycleRecord.OwnsPlanID` has **no non-test writer** in the repo, so `boot_sweep.go:160-161`'s exemption (b) never fires in production. The only live protection for a parked `needs_input` session is `OwnerScopeKind` (`boot_sweep.go:295-296`). FR-118's requirement is met structurally; the spec records that its stated mechanism is currently inert (see RISK-8, S9 row 5). |
| **FR-109** `[P2]` (`:1466`) | *"The plan owner MUST be excluded from the task-level idle trigger"* | **PRESERVED** — `OwnerAgentID` survives, so the exclusion still resolves. |
| **FR-133** `[P2]` (`:1507`) | *"Ownership MUST derive from the **spawn edge** (owner = union of spawning parent · owning plan · human for top-level)"* | **NOT IN CONFLICT — clarified.** FR-133 defines ownership of a *session*; this spec's canonical "Owner" defines the principal accountable for a *plan*. FR-052 records that the two vocabularies are distinct and neither is renamed into the other. |
| **FR-143** `[P2]` (`:1518`) | the three correction verbs | **AMENDED** — `supersede` gains a pairing precondition (FR-030). The verb set and revision-entry requirement are otherwise unchanged. |
| **ADR-053 D4 / D7** | correction verbs; *"Adjust a member = Stop plan → change → continue"* | **AMENDED by D4's own extension.** D7's stop-and-re-author path is retained as the fallback (O5), not replaced. |
| **ADR-053 D2** | *"only a direct session/plan owner asks the human"* | **PRESERVED.** The Owner remains the terminus for plan-scoped `owner_required` questions; only *adjudication* moves. FR-011 states this explicitly. |
| **ADR-053 §3 BOM / anti-drift gate** (`:47`) | *"a second goal store, a second messaging envelope … is a blocking finding (DoD-11)"* | **GATE RUN — see § BOM Gate.** |
| **ADR-049 D4** | one-shot owner wake | Already superseded by ADR-053 → persistent owner session `[FACT — `ADR-049…md:5`]`. This spec retains the persistent session and only re-targets *who* is woken; no one-shot wake is restored. |

### BOM Gate (ADR-053 §3 / DoD-11)

| New thing | Existing carrier it extends | Verdict |
|---|---|---|
| PlanSupervisor agent | `SystemAgents()` / `systemAgentSeed` — the **second** instance of the shipped Judge System-Agent pattern | **Reuse.** No new agent machinery. |
| `plan_correct` tool | `pkg/tools` registration + `AppendCorrection`, both shipped | **Reuse.** No new correction engine. |
| Owner wake | `wakeOwner` → `asyncNotifier` bus, **unchanged in shape**; the `ownerAgentID` argument changes (FR-012) and the origin stops being hardcoded (FR-012c/FR-012d) | **Reuse.** No second messaging envelope; no change to `Notify`, the bus, or `processSystemMessage`. |
| Supervision wake | `runAgentLoop` direct dispatch — the shipped `processTaskDirect` / verifier-adjudication pattern, via the `*AgentLoop` `PlanEngine` already holds (`plan_engine.go:188`) | **Reuse.** No new seam and no new envelope: it is the same dispatch the Judge's verifier already uses. It leaves the bus because `processSystemMessage` hardcodes `SendResponse: true` and FR-016/H8 forbid the adjudicator's output reaching the Owner's chat (D-11) — **not** to gain a capability. |
| Plan chat origin (`source_channel` / `source_chat_id`) | `Task.SourceChannel` / `SourceChatID` (`pkg/task/task.go:307-309`), shipped and `omitempty` | **Reuse.** Same two fields, same semantics, same optionality — copied, not invented (FR-012d). |
| Owner session | `session.NewVerifierSession` (`pkg/session/unified.go:544`), the shipped engine-minted store-backed session — the same primitive FR-016b uses for the supervision session | **Reuse.** One minting helper serves both sessions; FR-016c is FR-016b's mechanism applied a second time, not a second mechanism (operator ruling 11). |
| Human outcome delivery | `pkg/notifications` store + WS push, shipped | **Reuse, widened.** Not a second rail — it is the *only* rail that can address a human; the bus is the only rail that can address an agent (FR-014). |
| Correction budget | `PlanBounds.PlanJudgeMaxRounds`, shipped | **Reuse.** No second budget (FR-034). |
| Audit trail | `RevisionEntry` + intent log, both shipped and already replayed at boot by `reconstructCorrections` | **Reuse.** No second audit store. |

**Result: PASS.** The one genuinely new artefact is the `plan_correct` tool name, which is a
new *capability surface*, not a duplicate of an existing one. Rev 4 adds no new artefact at all —
every element of the wake fix reuses a shipped carrier (the four rows above).

### What should amend ADR-055 (recorded here; this spec does not edit the ADR)

Rev 4 does not change the ADR's architecture, but three of its statements are now known-false or
incomplete. Each should be corrected in ADR-055 when it moves off **Proposed**:

| # | ADR-055 statement | Correction |
|---|---|---|
| **A1** | **D4 / the wake model** describes waking the plan owner as a working mechanism the feature builds on. | It has **never** delivered: all five wakes are discarded before any agent turn runs (**N15**). The ADR should state that the wake path is *built here*, not *reused here*, and should not be read as evidence that plan wakes work today. |
| **A2** | **The kill switch** (operator ruling 1, realised in the ADR as `StopPlan`'s cascade) is described as cascading to the owner session. | That leg is a **no-op**: nothing in `pkg/` ever creates the `plan:<id>` session it cancels (**N13**). Fixed here by FR-016c, but the ADR's claim was false at the time it was written and should be marked as such rather than silently becoming true. |
| **A3** | **§9 step ordering** puts the contract work late and does not mention a plan origin at all. | Superseded by §18 (already recorded as a deviation); the origin fields add sub-item (h) to step 1. No ADR change is *required* here — noted so the two documents' step lists are not diffed naively. |

**Out of this spec's scope but worth an ADR-053 amendment or a follow-up issue:** the identical
defect on the **Task** goal-loop wake (`task_executor.go:1066-1069` falls back to `Channel:
"system"`, which is dropped the same way) — **RISK-16**. It is not fixed here, deliberately, on
D-08's stated test.

---

## 4. Existing Codebase Context

All symbols verified by direct read on 2026-07-27 at `feature/plan-swimlane-board`.

### 4.1 Symbols Involved

| Symbol | File:line | Role | Verified fact |
|---|---|---|---|
| `PlanEngine.AppendCorrection` | `pkg/agent/plan_engine.go:2574` | **call** (new caller) | Fully implemented, transactional via intent log, **zero non-test callers** |
| `PlanEngine.requireOwner` | `pkg/agent/plan_engine.go:2754` | **modify** | Three clauses: empty `AgentID`; `caller.AgentID != p.OwnerAgentID` (opaque); `p.OwnerSessionID != "" && caller.SessionID != p.OwnerSessionID` (differentiated) |
| `PlanEngine.validateCorrection` | `pkg/agent/plan_engine.go:2693` | **modify** | `supersede` → `validateMemberRef(..., task.StatusDone, "done")`; `targeted_retry` → `(..., task.StatusFailed, "failed")`; `append` → `len(TailMembers) > 0`. **Never inspects `req.TailEdges` at all** `[FACT — verified: the whole function is `:2693-2717`; no `TailEdges` reference]` — no cycle check, no dangling-edge check, no cap. FR-046 adds them. |
| `PlanEngine.validateMemberRef` | `pkg/agent/plan_engine.go:2725` | **modify** | Hard-rejects a status mismatch — but checks **status before plan ownership** (`:2730-2737`), so the status-mismatch error names another plan's id (`"member %q is %s, not %s"` fires before `t.PlanID != planID`). FR-047 reorders. |
| `PlanEngine.buildCorrectionApplyFunc` | `pkg/agent/plan_engine.go:2779` | **read** | Creates `rec.Members` **verb-independently**; skips a member whose id already exists (`existing, err := pe.taskStore.Get(m.ID)` → `continue`) with **no** replay-vs-first-application distinction. Correct for intent-log replay; a silent drop for an LLM that reuses an id (FR-046). |
| `PlanEngine.wakeOwner` | `pkg/agent/plan_engine.go:2096` | **modify — including its signature** | 5 call sites; `ChatID` is built **inside** from `planID` (`:2104`), not parameterisable; nil-notifier = silent return; publish failure = WARN-and-continue. **Fire-and-forget: returns nothing about the woken turn** — this is why FR-021's deadline, not a callback, is the observation seam. **Rev 3: it also constructs `AsyncNotifyEvent` with `TranscriptSessionID` left unset**, which is why the supervision turn today would run with an empty transcript session and be uncancellable (N13). FR-016b adds a `transcriptSessionID` parameter threaded into that field. Rev 2's FR-016 deferred this signature change to *"if a second destination is later required"* — it is required now. **Rev 4: `Channel: "system"` (`:2103`) is the N15 defect and this is the only hardcoded `Notify` origin in the repository** — the other three callers (`task_executor.go:1077`, `loop.go:8258`, `goal_triggers.go:585`) all propagate a real one. FR-012c/FR-012d source it from `Plan.SourceChannel`/`SourceChatID`; the two supervision call sites (`:1254`, `:1542`) stop using this function's notifier path altogether (D-11). |
| `plan.Plan` struct | `pkg/plan/plan.go:361-403` | **modify — 2 new fields** | Carries `OwnerAgentID`, `OwnerSessionID`, `Owner`, `CreatedBy` — but **no origin channel/chat**, which is N15's root cause (N15.2). FR-012d adds `SourceChannel`/`SourceChatID`, mirroring `pkg/task/task.go:307-309` verbatim. `Plan.yaml` is `additionalProperties: false` (`:28`), so the schema, regen and artifacts land in one commit (§18 step 1(h)). |
| `PlanTool` create path | `pkg/tools/plan.go:282-290` | **modify** | The `&plan.Plan{…}` literal for `create_plan`. FR-012d populates the origin here from `tools.ToolChannel(ctx)` / `tools.ToolChatID(ctx)` — the `pkg/tools/task.go:541-543` pattern, **minus its `webchat` exclusion** (E39, and this is the single line most likely to be copied wrongly). |
| `createPlan` REST handler | `pkg/gateway/rest_plans.go:543-549` | **modify** | The `&plan.Plan{…}` literal for `POST /api/v1/plans`. There is no chat context here, so both origin fields stay empty — **the no-origin case, which is decided (D-10), not accidental**. Both fields are server-set; a client-supplied `source_channel` is ignored (E40). |
| `PlanEngine.agentLoop` | `pkg/agent/plan_engine.go:188` | **read (enabling fact)** | The engine already holds the `*AgentLoop`, so FR-012c(A)'s direct supervision dispatch needs **no new injection seam** — this is what makes D-11's fork cheap. |
| `webchatChannel.Send` | `pkg/gateway/webchat_channel.go:62-107` | **read (behaviour relied on)** | Returns `channels.ErrSendFailed` when no WS connection is bound to the chat (`:92-95`), which the Manager classifies PERMANENT — one log, no retry storm. This is why recording a `webchat` origin on a Plan is safe (E39) and why the Task path's exclusion of it does not transfer. |
| `session.NewVerifierSession` | `pkg/session/unified.go:544` | **read (precedent)** | The in-repo precedent for an **engine-minted, isolated, store-backed** session, and the shape FR-016b reuses for the supervision session. It matters that the minted session is *store-backed*: `processSystemMessage` honours a forwarded `AsyncTranscriptSessionID` **only if `ResolveSessionStore` resolves it** (`loop.go:6073-6083`, resolving via `GetMeta`), and `GetActiveTurnHookForSession` (`turn.go:439-463`) matches a live turn on exactly that value. A derived/synthetic id resolves to nothing, the turn runs with `transcriptSessionID == ""`, and cancellation finds nothing to cancel. |
| `plan.Patch` | `pkg/plan/store.go:232-271`, applied by `updateLocked` (`:287`) | **extend** | A **flat struct of typed pointers**, one field per writable attribute. Every persisted plan mutation in the engine goes through `pe.planStore.Update(id, plan.Patch{…})`. `Plan.supervision`'s five fields are mutated **independently and with different semantics** (stamp, increment, clear, reset, set), so FR-050 specifies **five discrete pointer fields** rather than one whole-object pointer — a whole-object pointer makes every write a read-modify-write over a struct the caller read earlier, and `pkg/gateway/rest_plans.go`'s `Store.Update` callers do **not** hold `planDecisionMu`. |
| `RevisionEntry.verb` (contract) + `plan.RevisionVerb` | `contracts/components/schemas/RevisionEntry.yaml:40-45`; `pkg/plan/intent_log.go:80-85` | **extend** (S15) | A **closed** three-value enum — `append` \| `supersede` \| `targeted_retry` — referenced from `contracts/openapi.yaml:641-642` and generating `RevisionEntryVerb` with a `Valid()` method over exactly those three (`pkg/api/generated/openapi_types.gen.go:3131-3146`) `[FACT — verified 2026-07-27]`. FR-046's `abandon` verb writes a revision entry, so this is a **Constraint #8 step-1 contract change**, not an implied step. Rev 2 omitted it from §18 step 1 entirely. |
| `PlanEngine.synthesizeAndComplete` | `pkg/agent/plan_engine.go:1561-1582` | **read (NOT modified)** | **The only wake on the success path.** Sets `PhaseSynthesizing`, wakes `p.OwnerAgentID` with the closing-synthesis commission (`:1571`, kind `plan_judge_met`), then sets `StateDone` (`:1578`). `failPlanLocked` (`:1610`) is failure-only and `StopPlan` (`:1742`) is user-stop-only, so **re-targeting `:1571` away from the Owner would leave a successful plan notifying nobody.** FR-012 therefore leaves it on the Owner — see C15. |
| `PlanEngine.StopPlan` | `pkg/agent/plan_engine.go:1666` | **modify** | `StopPlan(ctx, planID, userID, channel)` — actor-parameterised via `userID`. Cascade: `cancelSessions` over {each `in_progress` member's session} ∪ {every registered verifier session for the plan and every member} ∪ {`p.OwnerSessionID`}, then `cancelMemberLocked` per `in_progress` member, then plan → `failed(stopped_by_user)`, then `wakeOwner`. **FR-044 adds the supervision session to that set.** |
| `PlanEngine.cancelSessions` | `pkg/agent/plan_engine.go:1810-1830` | **read** | De-duplicating fan-out over `pe.canceller.RequestCancelForSession`; a nil canceller WARNs and skips; a per-session failure WARNs and continues. This is the mechanism FR-044 reuses — no new machinery. |
| `PlanEngine.reconstructCorrections` | `pkg/agent/plan_engine.go:3105-3127` | **modify** | Rebuilds the in-memory superseded-member set from each plan's intent-log JSONL at boot. **`records, err := pe.intentLog.List(planID); if err != nil { continue }` — a per-plan read error is swallowed silently** `[FACT — verified]`, so a truncated log silently *un*-supersedes members. FR-048 surfaces it. |
| `PlanEngine.processPlan` (phase switch) | `pkg/agent/plan_engine.go:844-866` | **modify** | Cases for `PhaseJudging` and `PhaseSynthesizing` **only** |
| `defaultPlanEngineTickInterval` | `pkg/agent/plan_engine.go:131` | **read** | **`30 * time.Second`** — a package const, **not** a config key `[FACT — verified: `pkg/config/planning.go` has no tick field]`. Assigned to `pe.tickInterval` (`:250`, `:340`) and drives `time.NewTicker` at `:625`. This is the unit "one tick" means throughout this spec (SC-008, FR-022, FR-027). |
| `PlanEngine.ensureOwnerSessionLocked` | `pkg/agent/plan_engine.go:2469` | **read** | `sessionID := "plan:" + p.ID`; write-once; persist failure only WARNs (`:2475-2478`). **It records an id but creates no session** — see N13. |
| `coreagent.denyAllThenOverride` call sites | `pkg/coreagent/core.go:574, :585, :631, :674, :734, :822, :851, :857, :1503` | **read** | **Nine** call sites `[FACT — verified 2026-07-27]`, not the five rev 2 implied: the five `coreAgentSeed` cases, plus `coreAgentSeed`'s unknown-id fallback (`:822`), `systemAgentSeed`'s Judge case (`:851`) and its all-deny `default:` (`:857`), and `NewCustomAgentToolsCfg` (`:1503`). Every one of them fully enumerates the catalog, so **every one of them stamps `deny` for a newly added tool name** unless the name appears in its override map. This is the exhaustive list FR-006b's rule must hold over. |
| `coreagent.tightenGlobalCeiling` | `pkg/coreagent/core.go:409-416`, sole call site `:447` | **read** | The **sparse** helper — a tool absent from the returned map inherits the **global ceiling**, it is not denied (doc `:396-408`). Used by exactly one agent, `IDWorker`. `core.go:485-488` documents that `create_plan`/`execute_plan`/`run_task` are **deliberately absent** there so the Worker inherits `ask`; `:489-496` documents the opposite treatment for `inspect_session` — an **explicit** `deny`, precisely because its ceiling is `allow` and an absent key would silently inherit it. **That is the precedent FR-006b follows for `stop_plan`.** (Rev 2 cited `:447` as the definition; `:447` is the call site.) |
| `applyJudgeRoundOutcomeLocked` | `pkg/agent/plan_engine.go:1495` | **read** | `newRounds := current.JudgeRounds + 1` — the **sole incrementer** |
| `plan.PhaseAwaitingOwnerCorrection` | `pkg/plan/plan.go:237` | **rename** (S9 row 1) | `PlanPhase = "awaiting_owner_correction"`; in `validPlanPhases` (`:261-268`) |
| `plan.FailedReason` constants | `pkg/plan/plan.go:291-303` | **extend** | Four today: `judge_rounds_exhausted`, `stopped_by_user`, `idle_expired`, `budget_exhausted`; `validFailedReasons` at `:303`. The wire enum (`Plan.yaml:140-153`) is **closed** under `additionalProperties: false` (`:28`). FR-035 adds `dod_unreachable` and `supervision_unavailable` — a step-1 contract change. |
| `plan.Plan.OwnerAgentID` | `pkg/plan/plan.go:363` | **read** (routing subject) | *"the agent woken at plan decision points; **required**"*; validated non-empty at `:485-486`; set to an **agent id** on both write paths (`pkg/tools/plan.go:285`, `pkg/gateway/rest_plans.go:546`), each guarded by an `IsChatTarget()` validator |
| `plan.Plan` (new field) | `contracts/components/schemas/Plan.yaml` | **extend** | `Plan.yaml` is `additionalProperties: false` at `:28`, and `pkg/api/generated/contract_test.go` fails on any Go struct producing schema-invalid JSON, so the new `supervision` object is a **step-1 contracts change** (Constraint #8). FR-050. |
| `CorrectionRequest` | `pkg/agent/plan_engine.go:2405-2418` | **modify** | Marked `not-wire-format` (engine-internal). Carries `Verb`, `FalsifiedAssumption`, `TailMembers []task.Task`, **`TailEdges []IntentEdge`**, `SupersededMemberID`, `RetriedMemberID`, `Reason`. `AppendCorrection` sets `Members: req.TailMembers` (`:2621`) and `Edges: req.TailEdges` (`:2622`) **unconditionally, for every verb** — so `TailMembers` on a `targeted_retry` is currently created. FR-046. |
| `plan.IntentEdge` | `pkg/plan/intent_log.go:73-76` | **read** | `{FromTaskID, ToTaskID string}`. Aliased into `pkg/agent` at `plan_engine.go:2423` — **the exact precedent FR-004 reuses** for moving the correction types. |
| `restAPI.createAgent` | `pkg/gateway/rest.go:2145`, id minted at `:2378` | **read** (control of record) | Agent ids are **`uuid.New().String()`, server-minted, never operator-chosen** `[FACT — verified; the same property is stated in-tree at `rest.go:1188`]`, and a `{"type":"system"}` body is rejected 400. `updateAgent` (`:2813`) takes the id from the path and never writes `.ID`. **So no principal can create or rename an agent to `plansupervisor`** — FR-009's exact-identity gate rests on a real control, not on nobody picking the name. FR-049 pins it. |
| `restAPI.updateAgentTools` | `pkg/gateway/rest.go:6773`, guard at `:6789-6793` | **read** (why O9 exists) | Returns **403** `"agent %q is locked and cannot be modified"` for any `Locked` agent. PlanSupervisor is seeded `Locked=true` (FR-002), so its tool policy is unreachable from the Settings editor — this is why containment is plan-scoped, not agent-scoped. |
| `plan.Plan.Owner` / `.CreatedBy` | `pkg/plan/plan.go:437-438` | **read** (attribution) | Both `omitempty`, filed under `// --- attribution + lifecycle timestamps ---`; written `callerID` (agent) on the tool path and `c.Username` (human) on the REST path; `Plan.yaml:244-250` documents `owner` as *"Username of the user who created this plan"*, `readOnly` |
| `coreagent.SystemAgents()` | `pkg/coreagent/core.go:153-163` | **extend** | Returns `[]*CoreAgent`; one entry (`Judge()`) |
| `coreagent.systemAgentIDs` | `pkg/coreagent/core.go:146` | **extend** | Second, independent membership map backing `IsSystemAgentID` |
| `coreagent.systemAgentSeed` | `pkg/coreagent/core.go:847-859` | **extend** | `default:` branch returns `denyAllThenOverride(nil)` = **all-deny** |
| `coreagent.denyAllThenOverride` | `pkg/coreagent/core.go:384-394` | **read** | Calls `validateOverrideKeys` → **panics** on an unknown key |
| `coreagent.allStaticToolNames` | `pkg/coreagent/core.go:295-333` | **extend** | Hardcoded **83**-name literal `[FACT — verified by count 2026-07-27; matches `pkg/config/defaults_test.go:92`'s `const wantToolCount = 83`, and a set-diff against `pkg/config/defaults.go`'s `ToolPolicies` map is empty]`. **Rev 2 correction:** rev 1 said 81. Test #2 asserts `len(allStaticToolNames) == len(cfg.Sandbox.ToolPolicies)` so this number is never hand-quoted again. **Rev 3 note:** the literal's own doc comment (`:274-279`) still says *"31 general"* and sums to 81 — the code is right, the comment is stale by two names (`recall_conversation`, `message_parent`). Whoever adds `plan_correct` and `stop_plan` MUST correct the comment in the same edit, or the next reader re-derives the wrong catalog size. |
| `gateway.buildKnownBuiltinToolNames` | `pkg/gateway/gateway.go:715-745` | **read — NOT a literal to hand-edit** | **Rev 3 correction.** It **derives** from live metadata — `tools.GeneralBuiltinMetadata()` + `browser.BrowserBuiltinMetadata()` + `systools.AllTools(nil,nil)` — and then unions **four** hardcoded ADR-052 names `[FACT — verified]`. So registering a new tool in the builtin metadata registry is sufficient; adding it here as well is an idempotent no-op. Rev 2 listed it as one of "four catalog literals" to hand-edit, which is **wrong** (harmless, but it will send an implementer to edit a derived set). There are **three** literals — `allStaticToolNames`, `defaults.go`'s `ToolPolicies` (+ `defaults_test.go`'s `wantToolCount`), and the builtin metadata registry — plus this derived set. FR-006 and test #2 are restated accordingly. |
| `coreagent.coreAgentSkills` | `pkg/coreagent/core.go:913-932` | **extend** | Called **only** from the `All()` loops (`:1198`, `:1287`) — never from `seedSystemAgents` |
| `coreagent.seedSystemAgents` | `pkg/coreagent/core.go:1331-1456` | **modify** | Re-enforces identity/type/locked/policy/`MemoryEnabled` each boot; **never touches `.Skills`** |
| `gateway.seedJudgeEagerSoul` | `pkg/gateway/gateway.go:906-918` | **extend** | Materialises `JudgeDefaultRubric` into SOUL.md; `SeedConfig` itself does zero filesystem I/O |
| `tools.resolveEffectivePolicyWith` | `pkg/tools/compositor.go:180-201` | **read** | `case a == "": return g`; merge is **strictest-wins** (`deny > ask > allow`) |
| `config.ValidateToolPolicyCoverage` | `pkg/config/validate.go:448-475` | **read** | OR-based per (agent, tool): a **global** entry alone satisfies coverage |
| `config.RepairIncompleteToolPolicyCoverage` | `pkg/config/validate.go:525-587` | **read** | Backfills genuine gaps to agent-side `deny` + WARN — so the boot abort is a backstop |
| `notifications.Store.Create` | `pkg/notifications/store.go` | **extend** | Sanitises `Recipient` into a filename; **succeeds for an agent id nothing ever reads**. Unguarded for future callers — FR-051. |
| `notifications.Store.ListForUser` | `pkg/notifications/store.go:224` | **read** | Sole non-test caller `pkg/gateway/schedules.go:1258`, keyed on `user.Username` |
| `Notification.yaml` `type` enum | `contracts/components/schemas/Notification.yaml:18-22` | **extend** | Closed single value `schedule_failed` under `additionalProperties: false` (`:14`). Its description reads *"The event class. **Extensible; consumers must tolerate unknown values.**"* — **actively false**; neither consumer tolerates one. FR-017 corrects it in the same commit. |
| `asyncapi.yaml` `NotificationFrame` | `contracts/asyncapi.yaml:2557,:2570` | **extend** | An **independent hand-maintained copy** whose event-class field is **`notification_type`**, not `type` — normalised by hand at `src/store/notifications.ts:46-52` (*"Normalize the WS field-name difference"*) `[FACT — verified]`. FR-017 must name all three sites. |
| `isWorkerAgentID` (session guard) | `pkg/gateway/rest.go:~1117` | **modify** | Worker-only; a System Agent passes |
| `plan/SKILL.md` | `pkg/skills/embedded/plan/SKILL.md:158`, **`:181`**, `:231` | **modify** | `go:embed`ed prompt text. Holds the phase literal (`:158`), the **verb table** (`:177-183`), and the anti-pattern line (`:231`). **`:181` verbatim:** *"…Marks the done member's outcome ignored-by-Judge (record stays immutable). **Optionally** append a replacement tail member."* — which contradicts FR-030, the rule the engine hard-rejects on. FR-040 fixes all three. |
| `src/lib/planStateColors.ts` | `:213` (`AWAITING_OWNER_CORRECTION_EXPLANATION`), `:234` (`STALLED_EXPLANATION`) | **modify** | Both currently end *"There's no in-app action for that yet — Stop this plan (■) and create a new one with the fix instead."* `[FACT — verified verbatim]`. The file's own comment at `:222-226` documents at length why the two conditions must never share copy. FR-063 specifies both replacements. |

### 4.2 Impact Assessment

| Symbol modified | Risk | d=1 dependents (WILL break / must be updated) | d=2 (SHOULD be tested) |
|---|---|---|---|
| `plan.PhaseAwaitingOwnerCorrection` (rename, S9 row 1) | **HIGH** | `validPlanPhases`, `normalize()`/`IsValidPlanPhase`, `EffectivePlanPhase()` consumers, `AppendCorrection`'s phase check (`:2591-2593`), `surfaceStallIfAny`'s precedence guard (`:1225-1230`), `boot_sweep.go:161`'s `planIsAwaitingOwnerCorrection`, `Plan.yaml` + `PlanStatusFrame.yaml` + `GoalStatusFrame.yaml` + `asyncapi.yaml` enums, `pkg/api/generated/`, `src/lib/api/generated/`, 4 × `pkg/gateway/inboundschemas/*.yaml`, `pkg/skills/embedded/plan/SKILL.md:158`, `tests/e2e/conformance-design-e2e.spec.ts` | **`src/**` — the compiler-invisible half.** `src/lib/planStateColors.ts` + `.test.ts`, `src/components/workspaces/PlansFilterBand.{tsx,test.tsx}`, `WorkspaceGraphTab.{tsx,test.tsx}`, `src/lib/ws.new-frames-validation.test.ts` — string-literal map keys and fixtures `tsc -b --noEmit` does **not** type-check against the generated enum. **Plus, NEW in rev 3, `pkg/tools/**` and `pkg/agent/**`** — the retired value also ships **hyphenated** (`awaiting-owner-correction`, live at `plan_engine.go:1709` and `:3171`), and the sibling `list-jobs-spec.md` **composes** the literal at runtime as `"running/awaiting_owner_correction"`, which no compiler sees. FR-062's sweep MUST cover all of it and MUST match both separators. |
| `plan.FailedReason` (+2 values, S15) | **MEDIUM** | `validFailedReasons`, `Plan.yaml:140-153` enum, `pkg/api/generated/`, `src/lib/api/generated/schemas.ts`; `src/lib/planStateColors.ts:120-131` `planSecondaryChipLabel` (which has a `default: return plan.failed_reason` fallthrough, so it degrades gracefully — but the **zod edge** does not) | `src/lib/__adr052__wireContracts.test.ts:158,:167`; `PlanActionButton.tsx`; `PlansFilterBand.tsx`; **`list-jobs-spec.md`'s status-normalisation table**, which enumerates only `judge_rounds_exhausted` and `stopped_by_user` today (cross-spec M1 — that spec's fix, listed here so this spec's authors know the dependent exists) |
| `RevisionEntry.verb` (+1 value, S15) | **MEDIUM** | `RevisionEntry.yaml:40-45` enum, `contracts/openapi.yaml:641-642`, generated `RevisionEntryVerb.Valid()` (`pkg/api/generated/openapi_types.gen.go:3131-3146`), `pkg/plan/intent_log.go:80-85`'s `RevisionVerb` consts | `reconstructCorrections`'s replay (an unknown verb must not silently drop a record); test #57's generated-enum assertion |
| `requireOwner` | **HIGH** | `AppendCorrection` (sole caller); `ErrCorrectionNotOwner` mapping | Every correction path |
| `wakeOwner` | **HIGH** | 5 call sites (`:1254` stall, `:1542` UNMET, `:1571` **MET synthesis — NOT re-targeted**, `:1610` fail, `:1742` stop); `:1610` alone is reached by **5 distinct failure paths** (budget_exhausted, stopped_by_user, judge_rounds_exhausted ×2, idle_expired) | `AsyncNotifyEvent` consumers; the `plan:<id>` session |
| `synthesizeAndComplete` (**NOT modified — recorded to prevent a regression**) | **CRITICAL if touched** | Re-targeting its `:1571` wake would make a **successful** plan notify nobody: `:1610` is failure-only and `:1742` is user-stop-only | Holdout H3; US-4 AS1; the human author of every plan that succeeds |
| `StopPlan` | **MEDIUM** | `handlePlanStop` (`pkg/gateway/rest_plans.go:1011`, the SPA ■ button), the new `stop_plan` tool (FR-042) | `cancelSessions` fan-out; the supervision turn (FR-044) |
| `validateCorrection` / `validateMemberRef` | **HIGH** (was MEDIUM) | `AppendCorrection`; existing correction tests (`plan_engine_correction_test.go`, 10 cases) | `plan/SKILL.md` verb table (`:177-183`) — **acted on by FR-040, test #25 and SC-015 in rev 2**; rev 1 flagged it here and then nothing enforced it |
| `systemAgentSeed` / `SystemAgents()` / `systemAgentIDs` | **HIGH** | `SeedConfig`, `persistSeededCoreAgents`, `IsSystemAgentID` consumers (`pkg/agent/workspace_reroot.go:88,:134`; `pkg/workspace/find_for_agent.go:33`), the Agents screen "System" section | Tool-policy coverage validation at boot |
| `allStaticToolNames` (+2 mirror literals + 1 derived set) | **HIGH** | `denyAllThenOverride` at **all nine** call sites (`:574, :585, :631, :674, :734, :822, :851, :857, :1503`), each of which stamps `deny` for a newly added name unless its override map says otherwise; `pkg/config/defaults.go`'s `ToolPolicies` map **and `defaults_test.go:92`'s `wantToolCount`**; the builtin metadata registry; `buildKnownBuiltinToolNames` (`pkg/gateway/gateway.go:715` — **derived, not a literal**, see §4.1); `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog`; the stale `:274-279` doc comment | Every agent's effective policy map. **Two names are added, not one** — `plan_correct` and `stop_plan` — **and the sibling `list-jobs-spec.md` adds a third, `list_jobs`, in the same release**, taking `wantToolCount` from 83 to 86. §18's landing-order note handles the collision on that one line. |
| `plan.Patch` / `updateLocked` (+5 fields, S10) | **MEDIUM** | `pkg/plan/store.go:232-271`; `updateLocked` (`:287`); every engine `planStore.Update` call site that writes supervision state (FR-021, FR-022, FR-024, FR-016b, FR-034) | `pkg/gateway/rest_plans.go`'s `Store.Update` callers, which do **not** hold `planDecisionMu` — this is why the patch is five discrete pointers and not one whole-object pointer (FR-050) |
| `Plan.OwnerAgentID` / `Plan.Owner` (**read-only — no schema change**) | **LOW** | The three outcome wake sites; the human-notification lookup | Outcome delivery for every plan. Risk dropped from MEDIUM once C6 removed the `owner_kind` addition |
| `Plan.supervision` (**new object**, S10) | **MEDIUM** | `Plan.yaml` (`additionalProperties: false`), `pkg/api/generated/contract_test.go`, `src/lib/api/generated/` | `processPlan`'s parked case; `StopPlan`; `AppendCorrection`; `bootReconcile` |
| `isWorkerAgentID` guards | **MEDIUM** | Session create (REST), WS chat frame, default-agent star | `GetDefaultAgent()`'s ~15 callers |
| `processPlan` phase switch | **MEDIUM** | `bootReconcile`; every tick of the plan loop | The F2 signature gate |

> **⚠ HIGHEST-RISK ITEM (revised).** Rev 1 named the phase rename's *persisted-data* exposure. Under
> the greenfield ruling that risk is **retired**: pre-rename records are expected not to load, no
> migrator ships, and there is nothing to strand. The residual highest risk is now the
> **compiler-invisible surface** — `src/**` string literals, `plan/SKILL.md`, the
> `inboundschemas/*.yaml` mirrors and the e2e specs, none of which `go build` or `tsc -b` sees.
> FR-062's `rg` sweep and SC-011's mechanical command are the whole control. See RISK-3.

### 4.3 Relevant Execution Flows

| Flow | Relevance |
|---|---|
| Plan tick → `processPlan` → dispatch / judge round | Gains a parked-phase boot case (FR-023); a parked plan currently falls through to `beginPlanJudgeRound` every tick, saved only by the F2 signature gate at `:1293-1301` |
| Plan judge round → UNMET → park + wake | Wake target changes from `OwnerAgentID` to PlanSupervisor (FR-012); the message text changes (it currently says *"awaiting **your** correction"*) |
| Plan judge round → MET → `synthesizeAndComplete` → synthesis wake | **UNCHANGED.** Stays a work commission to the **Owner** (`:1571`). It is the only wake on the success path, so re-targeting it would silently retire success notification entirely (C15, FR-012). |
| Plan terminal (`failPlanLocked` / `StopPlan`) → handover | Stays on the **Owner**, and gains the layered human notice (FR-014) |
| Agent turn → tool dispatch → policy resolution | Two new tools, `plan_correct` and `stop_plan`; policy resolves through `resolveEffectivePolicyWith` (FR-004, FR-042) |
| **Supervision wake → PlanSupervisor turn → tool call or silence** | **NEW, and the interval that contains the whole feature.** The wake is fire-and-forget, so the engine observes the turn's *effect on the plan record*, never the turn itself: `supervision.wake_at` arms a deadline (FR-021); an applied correction clears it; the deadline expiring with the plan still parked is the observable definition of "the turn produced nothing" (FR-019 limbs b and c); `supervision.attempts` bounds the retries (FR-022). |
| **Stop (SPA ■ button or `stop_plan` tool) → `StopPlan` → cascade** | Cancels every member session, every verifier session, the owner session **and (new, FR-044) the supervision session**, then fails the plan `stopped_by_user`. This is the containment control — plan-scoped, cascading to the executor exactly as it already cascades to members. |
| Boot → `SeedConfig` → `seedSystemAgents` → `persistSeededCoreAgents` | Gains PlanSupervisor; a newly-code-added System Agent takes the **fresh-seed branch even on an upgraded install** `[FACT — `existing` is built per-agent-ID at `core.go:1118-1121`]` |
| Boot → tool-policy coverage validate/repair → abort-or-continue | Must stay gap-free with **both** new tool names (FR-006) |
| Boot → `bootReconcile` → `reconstructCorrections` → superseded set | A per-plan intent-log read error is silently swallowed today, which *un*-supersedes members and re-admits discounted evidence to the judge. FR-048 surfaces it and fails the plan closed. |
| Boot sweep → lifecycle records → exempt-or-fail | Renamed fields (S9 rows 4, 5) are struct-field renames, so the Go compiler is exhaustive. **No migration** — greenfield. The behaviour to regression-test is `boot_sweep.go:295-296` (row 4, the live gate), **not** exemption (b) (row 5, which has no production writer). |

### 4.4 Cluster Placement

Primary cluster: **planning engine** (`pkg/agent/plan_engine.go`, `pkg/plan`). It spans four more:
**agent roster** (`pkg/coreagent`), **tooling & policy** (`pkg/tools`, `pkg/config`),
**gateway/contracts** (`pkg/gateway`, `contracts/`), and **SPA** (`src/`). The cross-cluster span
is why the contracts-first ordering in FR-070 is load-bearing rather than ceremonial.

---

## 5. Contradictions Found in ADR-055

Recorded so an implementer working from the ADR does not build the wrong thing. **This spec wins.**

| # | ADR-055 says | Working tree says | This spec |
|---|---|---|---|
| C1 | §9 step 3: *"`pkg/tools` + `pkg/gateway` — correction tool **and REST route**"*; §1 blast radius *"one tool + one REST route"*; §5 Option B *"one tool/route"* | FR-5 deleted the route | **No REST route** (O1). Three stale references in the ADR. |
| C2 | D7: the session-linkage fields *"become unused by the new routing"* | `requireOwner` — the ADR's own core change — reads `p.OwnerSessionID` as its third clause (`:2754-2770`); also read at `:1710-1711`, written at `:2474` and `pkg/plan/store.go:397-398` | Not unused. FR-009 states exactly what clause 3 does for PlanSupervisor. |
| C3 | D4/§9: the budget field is `judge_max_rounds` | No such key exists. Real names: `PlanBounds.PlanJudgeMaxRounds` (`pkg/plan/plan.go:318`, wire `plan_judge_max_rounds`) and `config.PlanningConfig.PlanJudgeMaxRounds` (`pkg/config/planning.go:41`); default 20 (`planning.go:14`) | FR-034 uses the real names. |
| C4 | D12: *"`pkg/task/verdict.go:43` already defines a verdict type"*, *"extension is additive"* | `:43` is inside the `JudgeVerdict` struct; the scope constants are at `:11-20` and are **untyped string constants**. The type **crosses the wire** | FR-036 puts the chosen verb on `RevisionEntry`, avoiding the wire change entirely. |
| C5 | D16/FR-14: a `supersede` may be paired with *"a **`targeted_retry` of the superseded member**"* | Impossible by construction: `validateMemberRef` requires `StatusDone` for supersede and `StatusFailed` for targeted_retry; a member cannot be both | FR-030 drops that disjunct and requires `TailMembers` instead — **verified feasible** (see FR-030). |
| C6 | **D2: *"`Plan.Owner` … already holds the dual-kind principal"*** `[FACT: grill-verified]`; D5 therefore *"forks by principal kind"* | **D2's premise is false.** `contracts/components/schemas/Plan.yaml:244-250` documents `owner` as *"**Username of the user** who created this plan"*, `readOnly: true`, and `pkg/plan/plan.go:437` places it under `// --- attribution + lifecycle timestamps ---` as an `omitempty` **attribution** field — not a wake address. The agent-addressable principal is a **different, already-correct field**: `Plan.OwnerAgentID` (`plan.go:361-363`) is *"the agent woken at plan decision points; **required**"*, validated non-empty at `:485-486`, and set to an **agent id on both write paths** (`pkg/tools/plan.go:285`, `pkg/gateway/rest_plans.go:546`), each guarded by an `IsChatTarget()` validator that rejects workers and System Agents | **FR-013/FR-014: no discriminator is needed and none is added.** The two concepts already live in two fields. Outcome delivery rides `owner_agent_id`; the human notification rides `owner`. **This falsifies D2 and makes D5's fork unnecessary rather than merely unimplementable — ADR-055 should be amended.** |
| C7 | D6/D14: *"the compiler is exhaustive where the graph is not"* | Four categories are invisible to `go build` and `tsc -b`: `pkg/skills/embedded/plan/SKILL.md:158`; 4 × `pkg/gateway/inboundschemas/*.yaml`; `tests/e2e/conformance-design-e2e.spec.ts` (17 occ.); YAML/prose descriptions | FR-062 mandates an `rg` sweep with an explicit file list and a zero-hits success criterion. |
| C8 | R5: *"**Deferred rename** … until the rename lands"* | D6, G2 and §8 all say it ships this release | The rename ships this release; the real risk is a partial migration (RISK-3). |
| C9 | D14 line 607: *"**#2 is a deletion**, not a rename — D5 moves its job to PlanSupervisor"* | D14 row 2 is marked **REMOVED FROM SCOPE**; O3 keeps the field | Stray v2 sentence. `OwnerAgentID` is neither deleted nor renamed. |
| C10 | D9 `Missing: Nothing material` | The 409 freeze is **handler-local** (`pkg/gateway/rest_plans.go:717-736`); `plan.Store.updateLocked` applies `DoD`/`OwnerAgentID` with no state check. This spec adds a new agent-facing mutation path | FR-032 states the rule every new mutation path must obey, with a conformance test. |
| C11 | G3/R4: `judge_rounds_exhausted` *"now covers two exhaustion causes"* | Two code sites (`:1289`, `:2680`) but **three meanings**: ceiling hit, correction budget spent (also `:1289`), and **DoD unreachable** (`:2680` — a condition with rounds possibly remaining) | **Resolved by splitting the enum, not by overloading the string.** Causes 1 and 2 are the *same predicate at the same line* (`if p.JudgeRounds >= maxRounds` → `buildPlanRoundsExhaustedHandover(p, maxRounds)`, whose entire input is `(p, maxRounds)`) and cannot be told apart without a counter — so FR-050 adds `supervision.correction_rounds` and FR-035 selects between **two** `judge_rounds_exhausted` strings on it. Cause 3 gets its **own** `failed_reason`, `dod_unreachable`, so it is machine-distinguishable. See C15. |
| C12 | D14 scope table occurrence counts | Rows 3 and 5 are roughly **double** the real figure; the counts included the duplicated repo copies under `.claude/worktrees/` | FR-062's success criterion is mechanical (`rg` returns zero), not count-based. **This spec holds itself to the same rule** — see the rev-2 note in the evidence-discipline banner and §26. |
| C13b | D14 row 5 (`OwnsPlanID`) is weighted as a scoped, consequential rename | `OwnsPlanID` has **no non-test writer** (verified: every assignment is in `boot_sweep_test.go` / `conformance_design_test.go`; the only production read is `boot_sweep.go:160-161`). So boot-sweep exemption **(b) never fires in production**, and the only live protection for a parked `needs_input` session is `OwnerScopeKind` (`boot_sweep.go:295-296`) | **Row 4 is the risky rename and row 5 is the free one — the opposite of the ADR's weighting.** Recorded in the S9 rows table; R1.5 is demoted to a synthetic-fixture row. |
| C14b | Migration is required for the rename (v3 §9, R5) | The operator ruled greenfield, unconditionally, for every store, and ADR-055 has been amended to match (*"No migrator ships … Withdrawn"*, *"D14 ships in full"*) | **No migrator.** FR-060 and FR-061 are **withdrawn** in rev 2, with them Dataset E, tests #20–#22/#24, four BDD scenarios, SC-012/SC-013, RISK-7 and holdout H6. AMB-1's descope recommendation is **overruled**. |
| C15 | D5/§9: the MET closing-synthesis wake (`plan_engine.go:1571`) is a "decision wake" that re-targets to PlanSupervisor, and *"the synthesis becomes the Owner's success notification"* | `synthesizeAndComplete` (`:1561-1582`) holds the **only** wake on the success path: it wakes `p.OwnerAgentID` at `:1571` then sets `StateDone` at `:1578`. The other two Owner wakes are `failPlanLocked` (`:1610`, failure-only) and `StopPlan` (`:1742`, user-stop-only). And nothing wires a PlanSupervisor synthesis *back* — FR-008 denies it every write tool and `plan_correct` carries no synthesis field | **The re-target is dropped.** FR-012's split is by *"who must decide whether to correct"*, not *"decision vs outcome"*: only the **stall** (`:1254`) and **UNMET** (`:1542`) wakes move. `:1571` stays on the Owner, which is also the right actor on the merits — it is the agent accountable to the requester and the only one holding the requester's conversational context. PlanSupervisor adjudicates failure; it does not narrate success. **ADR-055 D5 and §9 should be amended.** |
| C13 | D1: *"Implementation follows the Judge verbatim … tool policy explicitly enumerated by `systemAgentSeed`"* — implying the only work is a seed entry | `seedSystemAgents` **never sets `.Skills`**, and a `nil` allowlist means **unrestricted** (`pkg/agent/instance.go:179-183`). FR-3's skill grant needs new code, and omitting it grants **every** skill to the most privileged agent | FR-007 (new finding — see §6). |
| C14 | D8/R3: the tool grant is the control that stops other agents reaching correction | Verified in the `inspect_session` precedent comment (`pkg/config/defaults.go:387-414`): a global ceiling entry means the tool *"is never a gap"*, so *"a custom agent with no override resolves `allow` at the policy layer"* | FR-006 + FR-008 (new finding — see §6). The **identity gate, not the policy layer, is the sole enforcement for custom agents.** |

---

## 6. New Findings — Not in ADR-055 or Any of the Three Reviews

These were discovered while verifying this spec and are load-bearing.

### N1 — A `deny` global ceiling on `plan_correct` would silently disable PlanSupervisor

The runtime global×agent merge is **strictest-wins** (`deny > ask > allow`,
`pkg/tools/compositor.go:193-200`) `[FACT]`. `pkg/config/defaults.go:387-397` records this as a
**landed defect that was already fixed once**, verbatim:

> *"The runtime global x agent merge is strictest-wins … so a ceiling `"deny"` here would have
> OVERRULED the Judge's own seeded `"allow"` and resolved the Judge to deny — exactly the landed
> defect this seed inverts. The ceiling therefore seeds `"allow"` (raising the CEILING an agent's
> own policy can be granted UP TO … it does not, by itself, grant the tool to anyone)."*

The same trap is live for `create_plan`/`execute_plan`/`run_task`, whose ceiling is `"ask"` and
which `defaults.go:375-386` explicitly flags as **an unresolved cross-cutting question** — a ceiling
`"ask"` merges Jim's own `"allow"` down to `"ask"`. A System Agent has no human to answer an "ask",
so an `ask` ceiling on `plan_correct` would deadlock the feature silently. **The ceiling MUST be
`"allow"`** (FR-006).

### N1b — The *opposite* risk is the more likely one: on a fresh install the tool ships **denied to everybody**, including PlanSupervisor

ADR-055 R3 warns that omitting PlanSupervisor's policy map would **silently grant** it
`bash`/`write_file`/`create_agent`. For a **System Agent that goes through the seed path that
warning is inverted**, and the inversion is the failure mode most likely to be hit in testing:

`denyAllThenOverride` (`pkg/coreagent/core.go:384-394`) stamps an explicit `deny` for **every** name
in `allStaticToolNames` before applying the overrides `[FACT]`, and `systemAgentSeed`'s `default:`
branch is `denyAllThenOverride(nil)` — total deny `[FACT — `core.go:857`]`. The same helper backs
`NewCustomAgentToolsCfg` (`core.go:1499-1514`) `[FACT]`. And the merge is strictest-wins, so a
per-agent `deny` **beats** a global `allow`.

**Consequence:** the moment `plan_correct` is added to `allStaticToolNames`, every seeded agent —
core, subagent tier, Judge, freshly-created custom agents, **and PlanSupervisor itself** — carries an
explicit `deny` for it. If PlanSupervisor's `systemAgentSeed` case does not name the tool in its
override map, **the correction loop is dead on arrival on every fresh install**, silently, with the
agent present and the tool registered. `core.go:845-846` states this outright: *"Any System Agent
besides the Judge … falls back to all-deny … until it is given its own named case."* FR-008 and
SC-004 assert the **resolved** policy, not the seed literal, precisely to catch this.

### N2 — On an *upgraded* install the inverse holds: pre-existing agents inherit the global `allow`, and only the identity gate stops them

The `inspect_session` precedent comment continues (`pkg/config/defaults.go:408-414`) `[FACT]`:

> *"Custom/unlisted agents are NOT deny-backfilled for this tool — the coverage repair only fills
> gaps, and this ceiling entry means [it] is never a gap — so a custom agent with no override
> resolves `"allow"` at the policy layer. Their real protection is the engine-set, fail-closed
> verifier-session scope lock (`tools.VerifierSessionScopeAllows`)."*

This applies to agents whose policy maps were **persisted before the tool name existed** — they have
no entry for `plan_correct`, coverage validation sees the global entry and reports no gap, so
`RepairIncompleteToolPolicyCoverage` never backfills a `deny`, and
`resolveEffectivePolicyWith`'s `case a == "": return g` hands them the ceiling's `allow`.

So the two installs fail in opposite directions, and **both must be tested**:

| Install | Agent | Resolved `plan_correct` | Risk |
|---|---|---|---|
| Fresh | PlanSupervisor **without** its seed override | `deny` | **Feature dead on arrival** (N1b) |
| Fresh | any other seeded or newly-created agent | `deny` | correct |
| Upgraded | pre-existing custom agent | **`allow`** | **Only the gate stops it** (N2) |
| Either | PlanSupervisor **with** its seed override | `allow` | correct |

ADR-055 treats D3 (the gate) and D8 (the grant) as two independent controls, and R3 frames the grant
as the primary one. **For the upgrade case it is the opposite**: the grant is inert and
`requireOwner`'s identity check is the sole enforcement — exactly as `VerifierSessionScopeAllows` is
for `inspect_session`. FR-008 requires the engine gate to be treated and tested as the primary
control, with a negative test using a **pre-existing custom agent**, not only a seeded one.

Note also that the global ceiling entry is **mandatory, not optional**: `pkg/config/defaults_test.go`
asserts `len(cfg.Sandbox.ToolPolicies) == 83` and that the map is *"a full, wildcard-free enumeration
(CLAUDE.md hard constraint 6) … matching `pkg/coreagent`'s `allStaticToolNames` literal-for-literal"*
`[FACT]`. Omitting `plan_correct` from `defaults.go` to force a fail-closed coverage gap is therefore
**not available** — the count assertion and Constraint #6 both forbid it.

### N3 — `seedSystemAgents` cannot grant a skill, and `nil` means unrestricted

`coreAgentSkills` (`pkg/coreagent/core.go:913-932`) is called **only** from the two `All()` loops
(`:1198`, `:1287`); `seedSystemAgents` (`:1331-1456`) never reads or writes `.Skills` on either
branch `[FACT]`. And `pkg/agent/instance.go:179-183` `[FACT]`:

> *"`agentCfg.Skills` is nil when the agent declares no allowlist → **unrestricted**; a non-nil
> list restricts resolution and progressive disclosure to exactly those skills."*

So a PlanSupervisor seeded the way the Judge is seeded would receive **every skill in the
install**, not the plan skill. FR-007 requires an explicit, boot-re-enforced
`Skills: []string{"plan"}` on the System-Agent seed path.

### N4 — The SOUL is not seeded by `SeedConfig`; it needs a gateway-side materialiser

`AgentConfig.Rubric` was deleted; the rubric **is** the agent's `SOUL.md`, and `SeedConfig` must
stay a pure config-struct mutation with zero filesystem I/O `[FACT — `core.go:861-888`]`. The Judge's
rubric reaches disk through two call sites: eager `seedJudgeEagerSoul` (`pkg/gateway/gateway.go:906-918`,
called at `:1373`) and a lazy backstop `ensureVerifierSoul` (`pkg/agent/verifier_adjudication.go:199`).
ADR-055 §9 step 1 lists only `PlanSupervisorDefaultRubric` in `pkg/coreagent` — **half the work**.
FR-005 covers both call sites and the never-overwrite-an-operator-edit rule.

### N5 — `wakeOwner`'s destination is not parameterisable

`ChatID: "plan:" + planID` is constructed **inside** `wakeOwner` `[FACT — `plan_engine.go:2104`]`, so no
call site can vary it. After the FR-012 split, PlanSupervisor and (for an agent Owner) the Owner
would both be woken into the same synthetic `plan:<id>` chat with different `AgentID`s. FR-016
decides this explicitly rather than leaving it to discovery.

### N6 — `PlanEngine` has no path to the notification store

`NewPlanEngine` (`pkg/agent/plan_engine.go:333`) takes no notification store, and **`pkg/agent`
does not import `pkg/notifications` at all** `[FACT]`. The store is constructed at
`pkg/gateway/gateway.go:2262` and handed only to `setupCronTool` and `restAPI`; `NewPlanEngine` is
called at `:2719` without it. FR-015 specifies the injection seam as an interface owned by
`pkg/agent` (so no new import edge from `pkg/agent` → `pkg/notifications`).

### N7 — `ensureOwnerSessionLocked`'s persist failure silently forfeits the FR-118 exemption

A failed `OwnerSessionID` write only WARNs and returns, leaving the field empty `[FACT —
`plan_engine.go:2474-2478`; the `Update` call is `:2474`, the WARN block `:2475-2478`]`. An empty
`OwnerSessionID` (a) makes `requireOwner`'s clause 3 a no-op and (b) makes the plan's owner session
unresolvable for the FR-118 boot-sweep exemption. FR-009 and FR-024 both depend on knowing this, and
FR-024 requires the failure to be surfaced.

### N8 — The wake is fire-and-forget, so the engine cannot observe the supervision turn at all

`wakeOwner` → `asyncNotifier.Notify` → `bus.PublishInbound`; `async_notifier.go:248-251` states
*"no authorization happens here"* and the bus is a pure delivery primitive. `wakeOwner` returns
nothing about the woken turn and WARNs on publish failure `[FACT — verified]`.

**There is no completion callback, no turn-outcome channel and no deadline anywhere on this path.**
FR-019's three limbs of "unavailable" — provider error, timeout exceeded, no tool call and no
conclusion — are therefore **unobservable by `PlanEngine`**, the component required to act on them,
unless the spec adds a seam. Rev 1 did not; rev 2 does (FR-021).

The seam is deliberately **not** a callback. A callback would need `pkg/agent`'s turn path to
report into `PlanEngine`, creating a new coupling for a signal the engine can already infer: the
engine owns the plan record, and *whether the plan record moved* is a complete proxy for whether the
turn produced anything. So FR-021 arms a deadline on `supervision.wake_at` and checks it on a later
tick — the same shape as every other brake in this engine (round ceiling, idle expiry), using
machinery that already exists.

**Consequence for limb (a):** a provider error is *not* separately observable and is folded into the
deadline. FR-019 is restated accordingly — three limbs became one observable predicate.

### N9 — Once a plan parks, its stall note is never cleared, so the adjudicator reads a stale diagnosis

`surfaceStallIfAny` opens with `if p.EffectivePlanPhase() == plan.PhaseAwaitingOwnerCorrection { return }`
(`plan_engine.go:1225-1230`) `[FACT]`. That correctly stops a stall note *masking* the parked phase —
which is what E21 claims and what the regression test pins.

But the **stall-note clearing branch** (`:1234-1241`, the `reason == ""` path that resets
`HandoverText` and the phase) sits **behind the same guard**. So a plan that carried a stall note and
then entered the parked phase keeps that note indefinitely — and §11.1 lists the plan record as
PlanSupervisor's primary input.

E21's rationale asserts mutual exclusion of *phases* and is read as mutual exclusion of *state*. It
is not. Feeding a stale stall diagnosis alongside a DoD-unmet wake is precisely the input most likely
to make the adjudicator pick the wrong verb — the failure RISK-9 says the prompt is the only control
for. FR-025 clears it on entry to the parked phase.

### N10 — A corrupt intent log silently un-supersedes members across a restart

`reconstructCorrections` (`plan_engine.go:3105-3127`, called from `bootReconcile` at `:3179`)
iterates each plan's intent-log JSONL and, on a per-plan `List` error, **`continue`s with no log
line at all** `[FACT — verified verbatim: `records, err := pe.intentLog.List(planID); if err != nil { continue }`]`.
The in-memory superseded-member set is rebuilt entirely from those entries.

So a truncated or partially-written intent log causes previously-superseded members to be silently
**un**-superseded at the next boot. That re-admits discounted evidence to the plan judge, which can
flip a DoD verdict to MET — the **false success** US-3 exists to prevent, reached by a path US-3 does
not consider. It is silent end to end: no error surfaces, and FR-037 establishes that no read surface
for revisions exists, so nobody would notice. FR-048 makes it fail closed.

### N11 — `validateCorrection` never inspects `TailEdges`, and the tool's parameter schema was never written down

`validateCorrection` (`:2693-2717`) constrains `SupersededMemberID` on supersede, `RetriedMemberID`
on targeted_retry and `len(TailMembers)` on append. It **never references `req.TailEdges`**
`[FACT — verified over the whole function]`. Meanwhile `AppendCorrection` sets `Members: req.TailMembers`
(`:2621`) and `Edges: req.TailEdges` (`:2622`) **unconditionally, for every verb**.

`TailEdges []IntentEdge` (`{FromTaskID, ToTaskID}`) is how a new member is sequenced into the DAG.
An LLM-authored edge set can therefore introduce a **cycle** (which the dispatcher cannot resolve),
name a nonexistent member, or point at a superseded one — and a `targeted_retry` carrying 50 tail
members creates all 50, because only `append` constrains that field.

Compounding it: `buildCorrectionApplyFunc` skips a tail member whose id already exists, with **no
verb or replay check** (`:2779` onward). That is correct for intent-log replay and a **silent data
loss** for an LLM that reuses an id it just read off the plan — the correction reports success and
the member is never created.

Rev 1's Dataset A implied a parameter schema (`verb`, `superseded_member_id`, `retried_member_id`,
`tail_members`) but never stated one, and `tail_edges` appeared nowhere in the spec except §19's
argument about *humans*. FR-046 writes the schema field by field and adds the validation; the member
ids are **engine-minted**, which retires the collision class entirely rather than validating around it.

### N12 — Agent ids are server-minted UUIDs, so `plansupervisor` cannot be squatted

FR-009's gate is exact string equality against `plansupervisor`, which is only sound if nothing can
create an agent with that id. **It is sound.** Verified: `createAgent` (`pkg/gateway/rest.go:2145`)
mints `ID: uuid.New().String()` at `:2378` — the caller cannot supply an id — and rejects a
`{"type":"system"}` body with 400. `updateAgent` (`:2813`) takes the id from the URL path and never
writes `.ID`. The property is already stated in-tree at `rest.go:1188`: *"agent IDs are always
`uuid.New().String()`, never operator-chosen"* `[FACT — verified verbatim]`.

This is recorded as a finding rather than assumed, because FR-009's entire integrity property rests
on it and rev 1 left it unstated. FR-049 pins it with a regression test so a future
operator-chosen-id feature cannot silently remove the floor under the authority gate.

### N13 — Nothing in `pkg/` ever creates the `plan:<id>` session, so `StopPlan`'s owner-session cancel is a production no-op — and a synthetic supervision id would inherit that defect

**This is the finding that makes the kill switch real, and it is the reason operator ruling 5 exists.**

`ensureOwnerSessionLocked` records `sessionID := "plan:" + p.ID` on the plan (`plan_engine.go:2473`
— rev 3 cited `:2474`, which is the `planStore.Update` line; corrected in rev 4).
A repo-wide search for that construction in `pkg/` returns **exactly three non-test sites**, and
**none of them creates a session** `[FACT — verified 2026-07-27]`:

| Site | What it is |
|---|---|
| `verifierUnitForPlan` | a verifier **registry unit key**, not a session id |
| `wakeOwner` (`:2104`) | the synthetic bus **`ChatID`** |
| `ensureOwnerSessionLocked` (`:2474`) | the **string written to the plan record** |

So `Plan.OwnerSessionID` names a session that does not exist. Three consequences, all of which
rev 2 got wrong:

1. **`StopPlan`'s owner-session cancel already cancels nothing.** US-8's premise — *"Today
   `StopPlan` cascades to … the owner session"* — is **false**, and FR-044's rev-2 framing
   (*"extending an existing cascade rather than any new mechanism"*) understated the work by the
   whole mechanism. Corrected in US-8 and FR-044.
2. **`requireOwner`'s clause 3 never fires.** FR-009 already tolerates this by design (it requires
   an early return for PlanSupervisor regardless), and the spec already records that clause 3 is not
   a security control. No change needed — but the reason is now stated.
3. **A *derived* supervision session id cannot work, and its test would pass anyway.** This is the
   important half. The chain is:
   - `wakeOwner` builds `AsyncNotifyEvent` with **no `TranscriptSessionID` set**; the struct has one
     (`async_notifier.go:49-59`) and `Notify` forwards it (`:285`), but the plan engine never
     populates it.
   - `processSystemMessage` honours a forwarded id **only if it already resolves to a live store** —
     `loop.go:6073-6083`: `if store := al.ResolveSessionStore(msg.AsyncTranscriptSessionID); store != nil { … } else { WARN "async transcript session not found; result will not be persisted to a session" }`,
     and `ResolveSessionStore` (`loop.go:4572-4597`) resolves purely by `GetMeta(sessionID)`
     succeeding against a real store.
   - Cancellation matches on exactly that value: `GetActiveTurnHookForSession` (`turn.go:439-463`)
     skips every `turnState` whose `ts.transcriptSessionID != sessionID`.

   So a synthetic id such as `"plansupervisor:plan:" + p.ID` is **dropped at step 2**, the turn runs
   with `transcriptSessionID == ""`, and `RequestCancelForSession` finds nothing. **And nothing
   fails**: `plan_stop_test.go`'s `fakeSessionCanceller` (`:28-38`) records the string and returns
   `(true, nil)`, so a test asserting *"the supervision session appears in the cancel fan-out set"*
   goes green against a control that cancels nothing.

**Therefore FR-016b requires the engine to mint a real, store-backed session** (the
`session.NewVerifierSession` precedent, `pkg/session/unified.go:544` — `StopPlan` already cancels
those successfully via `registry().SessionsFor(units…)`), FR-016b threads it through a new
`wakeOwner` parameter into `AsyncNotifyEvent.TranscriptSessionID`, and **SC-020 asserts the turn
halted**, never fan-out membership.

#### Rev-4 scope decision on N13 — IN SCOPE, fixed here (operator ruling 11; O11 removed)

Rev 3 scoped the *Owner's* session out entirely (O11) on the grounds that the feature does not need
it. **The operator has ruled it in — *"fix it now, don't just record it"* — and re-checking against
D-08's own test (*defer only what no requirement in this spec depends on*) confirms the exclusion
never held.** Two independent dependents:

- **Operator ruling 1's kill switch.** *Stopping a plan stops everything working on it* is realised
  through `StopPlan`'s cancel fan-out, and its owner-session leg cancels an id that names nothing —
  so the ruling's guarantee is, today, partly fictional. This is **the same defect shape as N15**: a
  field is set, the thing it names was never created, and the existing test passes because
  `fakeSessionCanceller` records the label and returns success.
- **FR-012c(B)'s persistence.** An Owner wake must run a turn whose closing
synthesis is **persisted**, and `processSystemMessage` persists only to a `TranscriptSessionID` that
`ResolveSessionStore` resolves. For a plan with a live chat origin the channel itself is the record,
so persistence is a nicety. For a plan created through the Plans UI — H9's literal setup, *"Create a
plan through the Plans UI … do not navigate to the plan … expected: the owner agent's transcript
shows the closing synthesis"* — there is no chat and no resolvable session, so the turn runs and its
output goes **nowhere**. That is the same class of silent loss N15 describes, one hop further down.

Two further facts, verified 2026-07-28, make the repair cheap enough that deferring it is not the
conservative choice:

1. **`ensureOwnerSessionLocked` has exactly one caller** — `plan_engine.go:1541`, immediately before
   the UNMET wake. So on the ordinary success path (`synthesizeAndComplete`, `:1571`) the plan's
   `OwnerSessionID` is **empty**, not merely unresolvable. The field is not just naming a session
   that does not exist; on most plans it is not set at all.
2. **The blast radius of changing its value shape is near zero.** The FR-118 boot-sweep exemption
   reads `rec.OwnsPlanID` on the *lifecycle* record, not `p.OwnerSessionID` (`boot_sweep.go:160-161`)
   — and S9 row 5 already establishes that **nothing in production writes `OwnsPlanID`**, so that
   exemption never fires either way. `requireOwner` clause 3 (`plan_engine.go:2765-2768`) goes from
   *"denies every caller once `:1541` has fired"* to *"matches the real owner session"* — strictly an
   improvement, and FR-009's early return already makes it non-load-bearing for the only principal
   that matters. Greenfield (operator ruling 3) removes any persisted-data concern, and
   `owner_session_id` is already on the contract, so **no wire change**: only the value shape moves.

**Decision: fixed here, as `FR-016c` — written as FR-016b's mechanism applied to the second session,
one implementation unit, one PR** (operator ruling 11's *"fold it into the FR-016b work"*). In: mint
a real, store-backed owner session and call it before the *first* Owner wake; `StopPlan`'s
owner-session leg consequently cancels a real turn. Out, and stated so nobody assumes otherwise:
`requireOwner`'s clause-3 semantics, and writing `OwnsPlanID` — that field has no production writer
(S9 row 5) and rev 4 does not add one, so boot-sweep exemption (b) stays dead. **The success
criterion asserts the Owner turn TERMINATED** (SC-020 limb (e), test #63c), never that an id reached
a canceller — `cancelSessions` discards the `fired` bool (`plan_engine.go:1825`) and the fake
canceller returns `(true, nil)` regardless, so a set-membership assertion is the same false-green
C3-05 was. RISK-13 is rewritten around the single remaining residual.

### N14 — Jim's `execute_plan: allow` does not survive resolution, which is why `stop_plan`'s ceiling must be `allow`

`pkg/coreagent/core.go:810-816` seeds Jim *"the ONLY seeded agent granted unprompted plan-execution"*
with `create_plan`/`execute_plan`/`run_task` = `allow`. `pkg/config/defaults.go:415-417` seeds the
**global ceiling** for the same three tools as `"ask"`. The merge is strictest-wins, so **Jim
resolves `ask`, not `allow`** `[FACT — verified 2026-07-27]`. `defaults.go:375-386` records this
in-tree, verbatim, as an unresolved cross-cutting question *"outside this file's ownership"*.

It is an ADR-052 defect and **this spec does not fix it** (O12, RISK-14). It is recorded here because
it constrains two things this spec *does* decide:

- **`stop_plan`'s global ceiling MUST be `allow`** (FR-006). If it mirrored `execute_plan`'s `ask`
  ceiling, Jim's own `allow` would merge down to `ask` and Dataset **B11** (*"`jim` (the plan's
  owner) calling `stop_plan` → **Allowed***") would be unreachable — the same class of defect as
  C3-02, arriving from the ceiling instead of the seed.
- **FR-006b's rule is stated over the seed's *literal policy value*, and its success criterion over
  the *resolved* one** (SC-004b). Those two are deliberately different assertions, because for
  `execute_plan` they already disagree in the tree today. An implementer who checks only one of them
  can ship either N14's defect or C3-02's.

### N15 — **The plan owner-wake never starts a turn.** All five `wakeOwner` sites are dropped by the internal-channel guard before any agent runs

**This is the deepest instance of the rev-3 through-line, and it was found while verifying C3-05's
chain rather than by any review.** It is verified end to end by direct read on 2026-07-27.

`wakeOwner` hardcodes the event channel:

```go
// pkg/agent/plan_engine.go:2102-2107      [FACT — verified verbatim]
if err := pe.notifier.Notify(notifyCtx, AsyncNotifyEvent{
    Channel:    "system",
    ChatID:     "plan:" + planID,
    …
```

`Notify` composes the bus message's `ChatID` from those two fields:

```go
// pkg/agent/async_notifier.go:277          [FACT — verified verbatim]
ChatID: fmt.Sprintf("%s:%s", event.Channel, event.ChatID),
```

so the bus carries `ChatID = "system:plan:<id>"`. `processSystemMessage` (the **sole** consumer —
call sites `loop.go:2924` and `:5516`, both gated on `msg.Channel == "system"`) parses the origin
channel back out of that string and **returns before running anything**:

```go
// pkg/agent/loop.go:6006-6009, :6022-6031  [FACT — verified verbatim]
if idx := strings.Index(msg.ChatID, ":"); idx > 0 {
    originChannel = msg.ChatID[:idx]        // → "system"
    originChatID  = msg.ChatID[idx+1:]      // → "plan:<id>"
}
…
// Skip internal channels - only log, don't send to user
if constants.IsInternalChannel(originChannel) {
    logger.InfoCF("agent", "Subagent completed (internal channel)", …)
    return "", nil
}
```

and `"system"` is one of exactly three internal channels
(`pkg/constants/channels.go:6-10`: `cli`, `system`, `subagent`) `[FACT — verified]`.

**Consequence: every one of `wakeOwner`'s five call sites (`:1254`, `:1542`, `:1571`, `:1610`,
`:1742`) dead-ends at `loop.go:6030` with a single INFO log. No agent turn is dispatched, for any
plan wake, today.**

Three things follow, and each corrects something this spec previously asserted:

1. **§1's diagnosis was half the story.** *"The loop does not close"* is attributed to
   `AppendCorrection` having zero non-test callers. That is true and it is **not the only cause**:
   even with a caller wired, the UNMET wake at `:1542` never reaches an agent, so nothing would ever
   *decide* to call it. Both causes must be fixed for US-1 to work. §1 is amended.
2. **FR-012's wake split routes into a path that delivers nothing.** Re-targeting `:1254` and
   `:1542` to PlanSupervisor changes the `AgentID` field of an event that is discarded three hops
   later. FR-012c makes the delivery real; FR-012's routing is correct and unchanged.
3. **C3-05 is deeper than "the session id is unset".** FR-016b's threading of a real
   `TranscriptSessionID` is **necessary but not sufficient** — the internal-channel drop happens at
   `loop.go:6023`, *before* the transcript resolution at `:6072-6085` is ever reached. A spec that
   fixed only the session id would still ship a supervisor that never runs.

**Why this is in scope when N14's ceiling defect is not.** The test is whether a requirement *in
this spec* depends on it. Nothing here depends on `execute_plan`'s ceiling (O12). US-4 AS1, SC-010,
FR-012, FR-019 and the whole supervision loop depend on a plan wake reaching an agent. Shipping
them against a no-op delivery, with tests that pass because a fake notifier captured the event, is
precisely the false-green class rev 3 exists to remove. FR-012c is therefore a requirement, not a
note. See **D-08**.

**Why every existing test passes anyway.** The in-package test pattern is a fake notifier that
captures `AsyncNotifyEvent`s (§11.2, *"Development: fake notifier capturing events"*). It records
the call at the `Notify` boundary — three hops upstream of the drop. Asserting *"`Notify` was called
with `AgentID == plansupervisor`"* is asserting the mechanism; asserting *"a turn ran for
`plansupervisor` and its transcript is non-empty"* is asserting the property. FR-012c and SC-025
require the latter.

#### N15.1 — ⚠ What is **not** the bug (read before writing any code)

Two independent analyses of this defect — including one recorded in this document's own drafting —
concluded that `bus.InboundMessage.Channel == "system"` (`async_notifier.go:271`) is the error and
proposed changing it. **That is wrong and would break the path entirely.** `loop.go:5515` routes on
exactly that value to reach `processSystemMessage` at all, and `processSystemMessage` **rejects any
other channel at entry**:

```go
// pkg/agent/loop.go:5514-5518, :5992-5997     [FACT — verified verbatim 2026-07-28]
if msg.Channel == "system" {
    resp, err := al.processSystemMessage(ctx, msg)
    return resp, nil, err
}
…
if msg.Channel != "system" {
    return "", fmt.Errorf("processSystemMessage called with non-system message channel: %s", msg.Channel)
}
```

The bus channel is **correct and required**. The defect is only in the *event's* origin channel
(`AsyncNotifyEvent.Channel`), which `Notify` folds into the ChatID prefix and `processSystemMessage`
reads back as *"internal — discard"*. Do not spec, review or implement a change to the bus channel.

#### N15.2 — Root cause: `Plan` has no origin fields

`wakeOwner` hardcodes a synthetic internal destination because **there is nothing real to pass**.
`Plan` carries no origin channel or chat id, and the function's own doc comment says so verbatim —
*"Plan has no dedicated SessionID field the way Task does — there is nothing to route to short of
this synthetic per-plan destination"* (`plan_engine.go:2090-2095`) `[FACT — verified]`.

Every other `Notify` caller in the tree propagates a real origin it was handed:

| Caller | Origin it passes | Where the origin comes from |
|---|---|---|
| `task_executor.go:1077` | `t.SourceChannel` / `t.SourceChatID` | recorded on the Task at creation (`pkg/tools/task.go:541-543`) |
| `loop.go:8258` | `ts.channel` | the live turn's own channel |
| `goal_triggers.go:585` | `route.channel` | the resolved route |
| **`plan_engine.go:2103`** | **`"system"` (hardcoded)** | **nothing — `Plan` has no such field** |

`[FACT — all four verified 2026-07-28]`. **`plan_engine.go:2103` is the only hardcoded `Notify`
origin in the repository.** That is the shape of the bug: a missing field, papered over with a
constant, not a wrong constant.

#### N15.3 — The fix, and the in-tree precedent it mirrors

`pkg/task/task.go:307-309` is the precedent, verbatim:

```go
// SourceChannel/SourceChatID route a delegated-task result back to chat.
SourceChannel string `json:"source_channel,omitempty"`
SourceChatID  string `json:"source_chat_id,omitempty"`
```

FR-012d adds the same pair to `Plan` (contracts-first — `Plan.yaml` is `additionalProperties: false`
`[FACT — `contracts/components/schemas/Plan.yaml:28`]`, so the schema, the regen and the artifacts
must land in one commit per Constraint #8; folded into §18 step 1(h) rather than adding a second
`gen-contracts` run), populates it on both plan-creation write paths, and FR-012c has `wakeOwner`
propagate it.

**One deliberate divergence from the task precedent, and it must not be "corrected" by an implementer
copying the line.** `pkg/tools/task.go:541` excludes `webchat` — `if channel := ToolChannel(ctx);
channel != "" && channel != "webchat"` `[FACT — verified]` — because a Task's origin is consumed only
by `notifySourceChannel`'s raw `PublishOutbound` (`task_executor.go:1324-1325`), and
`webchatChannel.Send` returns `channels.ErrSendFailed` when no WS connection is bound to the chat
(`pkg/gateway/webchat_channel.go:92-95`), so for a closed browser that origin is a guaranteed
permanent failure and nothing else. **A Plan's origin drives a *turn*, whose output is persisted to
the owner session regardless (FR-016c).** Recording `webchat` therefore *adds* live delivery when a
tab is open and costs one WARN when it is not — strictly better than discarding the origin. FR-012d
records `webchat` and every other channel; see E39.

### N16 — An **empty** origin is not automatically safe: it escalates a healthy plan to `failed`

**This is the trap a naive implementation of FR-012d falls into**, and it is invisible on the happy
path because a plan created from a real channel never exercises it. Operator ruling 10 makes
origin-less plans a first-class state, so this path is not an edge case — it is every plan created
through the Plans UI.

**Two candidate failure modes were traced. Only the second is reachable, and the reachable one is
worse.**

*Candidate 1 — the leading-colon parse (real, but unreachable).* If an empty origin reached the bus,
`Notify` would compose `fmt.Sprintf("%s:%s", "", "plan:<id>")` = `":plan:<id>"`; `strings.Index`
returns **0**; `processSystemMessage`'s guard is `if idx > 0`, so the **`else`** branch fires and
`originChannel` becomes the literal `"cli"` (`loop.go:6010-6013`) — which is **also** an internal
channel (`pkg/constants/channels.go:6-10`), so the message is dropped exactly as `"system"` is, by a
different route. Verified by executing the composition and parse over six origin shapes
`[FACT — 2026-07-28]`.

*Candidate 2 — FR-N7 (reachable, and what actually happens).* `Notify` **rejects an empty
destination before composing anything**:

```go
// pkg/agent/async_notifier.go:226-233        [FACT — verified verbatim 2026-07-28]
// FR-N7: reject an ambiguous destination before attempting any publish.
if event.Channel == "" || event.ChatID == "" {
    return fmt.Errorf("async notifier: refusing to publish with empty destination (…)")
}
```

So candidate 1 is **unreachable through `Notify`** — the guard fires one hop earlier. **The real
consequence of passing an origin-less plan's empty fields to `wakeOwner` is a returned error**, and
under **FR-024** a wake error is recorded on the plan and retried each tick until **FR-022's attempt
ceiling** terminates it `failed(supervision_unavailable)`.

**That is the defect to design out: a perfectly healthy UI-created plan would be marked "the
supervisor is unavailable" for the sole reason that it has no chat.** It is strictly worse than a
silent drop — it is a *loud, wrong* diagnosis, it consumes the attempt budget, and it produces a
`plan_failed` notification whose stated reason is false. It would also pass any test written against
the mechanism (`Notify` was called; `wake_error` was recorded; the ceiling fired) while failing the
property (the plan was healthy).

**Therefore FR-012d(4) chooses D-10 option (b): `wakeOwner` never constructs a chat-origin wake it
cannot address.** The guard is `SourceChannel != "" && SourceChatID != ""` — deliberately the *same*
predicate as FR-N7, so the two can never disagree. Both traps are then unreachable by construction
rather than handled after the fact.

**Partial origins take the same path.** A non-empty channel with an empty chat id (`"telegram"` +
`""`) parses as **non**-internal and would run a turn addressed to an empty chat — a send to nowhere
rather than a drop. FR-N7 rejects it too, and FR-012d(4)'s both-non-empty predicate keeps it
unreachable. See **E41**.

**Regression obligation.** FR-N7 is now load-bearing for a case it was not written for. §13.4 pins
it: removing or relaxing that guard re-opens candidate 1, whose symptom (a wake logged as delivered
to `"cli"` and discarded) is indistinguishable in the logs from correct behaviour.

---

## 7. User Stories & Acceptance Criteria

### User Story 1 — A parked plan is adjudicated and corrected without human re-authoring (Priority: P0)

An operator starts an autonomous plan. Members run, the plan-level judge evaluates the Definition
of Done, and the DoD comes back UNMET for a reason that is *correctable* — a done member produced
the wrong outcome, a failed member deserves a targeted retry, or the plan is simply missing a
step. Today the plan parks forever and the only exit is to stop it and re-author from scratch,
discarding every completed member. This story delivers the closed loop: a purpose-built
adjudicator wakes, diagnoses, applies a correction through the existing transactional handler, and
the plan resumes.

**Why this priority**: This is the verified capability gap the whole feature exists to close.
Nothing else in this spec delivers user value without it.

**Independent Test**: With PlanSupervisor seeded and `plan_correct` granted, drive a plan to an
UNMET DoD whose defect is a missing tail member. Assert the plan leaves the parked phase, the
appended member dispatches, and the plan reaches a terminal state with no human input.

**Definition of "correctable"** (referenced by SC-001; ADR-055 left this undefined — MIN-11):
a plan is correctable when **at least one** of the three *mutating* verbs has a legal target under
`validateCorrection`: a `done` member to supersede, a `failed` member to targeted-retry, or a
gap that a new tail member can fill *and* `planCannotProgress(tasks)` is false after the
correction. A plan with no such target is **not** correctable and MUST take the honest-exit path —
which under FR-046 is a **reachable, first-class verb** (`abandon`), not an implicit fall-through.

**Acceptance Scenarios**:

1. **Given** a running plan parked at `awaiting_supervision` with a missing step, **When**
   PlanSupervisor issues `plan_correct` with verb `append` and one tail member, **Then** the plan's
   phase becomes `dispatching`, the tail member is created and dispatched, and a revision entry is
   recorded.
2. **Given** the same plan, **When** the appended work completes and the plan judge returns MET,
   **Then** the plan reaches `done` without any human action.
3. **Given** a plan parked with a `failed` member that failed transiently, **When** PlanSupervisor
   issues `targeted_retry` naming that member, **Then** that member alone resets to `next` and no
   other member is reset.
4. **Given** a plan parked whose DoD is unreachable by any verb, **When** PlanSupervisor issues a
   correction that leaves `planCannotProgress` true, **Then** the plan fails with
   `failed_reason = dod_unreachable` and a handover explaining the DoD is unreachable — **not** with
   `judge_rounds_exhausted` and a message claiming rounds ran out.
5. **Given** a running plan whose DAG cannot progress (no member in `next` or `in_progress`, members
   **not** all terminal), **When** the engine surfaces the stall, **Then** PlanSupervisor is woken
   with a **stall diagnosis** request naming the stall reason — and **not** a Definition-of-Done
   verdict request — and any stall note carried from a prior stall has been cleared before the wake.
5b. **Given** that same stalled plan, **When** PlanSupervisor issues a `plan_correct` against it,
   **Then** the correction is **accepted on the merits, not rejected on the phase** — `plan_correct`
   admits the `stalled` phase exactly as it admits `awaiting_supervision` (D-01, FR-029) — and the
   plan returns to `dispatching` with its stall note cleared.
   *This scenario exists because rev 2 shipped the stall wake without it: `AppendCorrection` hard-rejected
   any phase ≠ parked, so every `plan_correct` a stall wake provoked was rejected 100% of the time
   and nothing in the spec detected it (r3 C3-01).*
5c. **Given** a stalled plan whose supervision turn produces nothing, **When** the supervision
   deadline elapses, **Then** the same re-wake, attempt-counting and ceiling apply as for a parked
   plan, and exhausting the ceiling terminates the plan `failed(supervision_unavailable)` — a stall
   is bounded by the same brakes, not only by 7-day idle expiry.
6. **Given** a plan parked at `awaiting_supervision` that PlanSupervisor correctly diagnoses as
   unsalvageable, **When** it issues `plan_correct` with verb `abandon` carrying the falsified
   assumption, **Then** the plan terminates with `failed_reason = dod_unreachable`, a revision entry
   records the abandonment and its reason, and no member is mutated.
7. **Given** a plan parked at `awaiting_supervision`, **When** PlanSupervisor's turn produces a
   correction that validation **rejects** (or produces no tool call at all), **Then** the plan does
   **not** sit unattended: the supervision wake is re-issued on a later tick, bounded by an explicit
   attempt ceiling, and exhausting that ceiling terminates the plan with
   `failed_reason = supervision_unavailable`.

---

### User Story 2 — Only PlanSupervisor can correct a plan (Priority: P0)

Correction mutates a running plan's member DAG and changes what evidence the judge weighs. If any
agent could reach it, an agent could satisfy its own acceptance criteria by rewriting the work that
proves them. This story makes correction reachable by exactly one identity and unreachable by every
other principal — including a *user-created* agent, which the tool-policy layer provably cannot
stop (N2).

**Why this priority**: The integrity property is 25% of ADR-055's decision weight, and it is the
reason a purpose-built adjudicator was chosen over the existing owner agent.

**Independent Test**: With PlanSupervisor seeded, attempt `plan_correct` from (a) a seeded core
agent, (b) a user-created custom agent, (c) the plan's own `owner_agent_id`. Assert all three are
denied with an identical, non-differentiating error, and that PlanSupervisor succeeds.

**Acceptance Scenarios**:

1. **Given** a plan parked at `awaiting_supervision`, **When** PlanSupervisor calls `plan_correct`,
   **Then** the correction is applied.
2. **Given** the same plan, **When** any other agent — including the plan's `owner_agent_id` —
   calls `plan_correct`, **Then** the call is denied.
3. **Given** a **user-created** agent whose tool-policy map has no entry for `plan_correct` (so the
   policy layer resolves `allow` from the global ceiling), **When** it calls `plan_correct`,
   **Then** the engine gate denies it.
4. **Given** any denied caller, **When** it compares the denial to a denial for a plan that does
   not exist, **Then** the two responses are indistinguishable — no plan state is leaked.
5. **Given** a user, **When** they attempt to open a chat session addressed to `plansupervisor`,
   **Then** the request is rejected.
6. **Given** a user, **When** they attempt to star `plansupervisor` as the default agent, **Then**
   the request is rejected.

---

### User Story 3 — `supersede` cannot be used to pass a DoD by discounting failure (Priority: P0)

`supersede` marks a done member's outcome ignored-by-judge. An adjudicator facing an unmet
criterion therefore has a legal move that is not "fix the work": discount the failing evidence and
re-judge. Nothing in the shipped code prevents it, and with the shared round budget there are up to
20 attempts to reshape the evidence set. This story makes discounting-without-replacement
structurally impossible.

**Why this priority**: Without it the feature can manufacture **false successes** — worse than a
stuck plan, because `done` is terminal and frozen.

> **Scope of the guarantee — corrected in rev 2.** Rev 1 called this *"structurally impossible"* and
> NFR-2 listed it as a guarantee. It is **not**. FR-030's `len(TailMembers) > 0` rule makes a **bare**
> discount impossible; it does not stop the adjudicator superseding the failing member and attaching
> one trivial, instantly-satisfiable tail member. The DoD is unchanged, but the **evidence set the
> judge weighs** is — which is the mechanism this story is about. The bypass costs one throwaway
> member.
>
> Rev 2 therefore adds the control that actually closes it: **a superseded member's replacement MUST
> inherit the superseded member's acceptance criteria** (FR-030b). That is machine-checkable inside
> `validateCorrection` and is what "replacement work" *means*. The claim is restated honestly in
> NFR-2, and SC-003 is extended from the bare case to the paired case.

**Independent Test**: Drive a plan whose only defect is one criterion unmet by member M's wrong
outcome. Attempt `supersede` of M with no replacement work — assert rejection. Then attempt
`supersede` of M paired with a trivial tail member carrying *no* inherited criteria — assert
rejection. Assert the plan does not reach `done` in either case.

**Acceptance Scenarios**:

1. **Given** a plan parked at `awaiting_supervision` with a done member M whose outcome is wrong,
   **When** a `supersede` of M is issued with an empty `tail_members`, **Then** the correction is
   rejected and no revision entry is recorded.
2. **Given** the same plan, **When** a `supersede` of M is issued together with at least one tail
   member that inherits M's acceptance criteria, **Then** the correction is applied and both the
   supersession and the tail add appear in the revision entry.
3. **Given** a plan whose only defect is an unmet criterion, **When** every correction attempt
   adds no work that carries the failing criterion, **Then** the plan never reaches `done`.
4. **Given** any applied `supersede`, **When** an operator reviews the plan's audit trail, **Then**
   the supersession is distinctly identifiable and not folded in with plain appends.
5. **Given** a `supersede` of M paired with a tail member that carries **none** of M's acceptance
   criteria, **When** validation runs, **Then** the correction is rejected before any mutation —
   discounting evidence is not made legal by attaching cheap unrelated work.

---

### User Story 4 — The plan's outcome always reaches somebody who can read it (Priority: P0)

A plan has **two** accountable principals, and the codebase already models them as two separate,
correctly-typed fields. `owner_agent_id` is the **responsible agent** — required, always an agent id,
validated on both write paths to be a real chat-target agent. `owner`/`created_by` are **creator
attribution** — a username when a human created the plan through the UI, an agent id when an agent
created it through `create_plan`. Today all five wakes go to `owner_agent_id`, which works; the gap
is that when a *human* authored the plan, nothing tells that human it finished. This story keeps the
guaranteed agent delivery and adds the human notice on top.

> **This supersedes ADR-055 D2/D5.** D2 claimed `Plan.Owner` *"already holds the dual-kind
> principal"*; the contract documents it as a username (`Plan.yaml:244-250`, `readOnly`) and the Go
> struct files it under attribution. D5 then built a fork on that premise. Because the
> agent-addressable principal already exists as `owner_agent_id`, **no `owner_kind` discriminator,
> no wire addition and no migration are needed** — a strict scope reduction. See C6.

**Why this priority**: It is the difference between "the supervisor is down" being visible and a
plan being silently stuck forever. The failure model rests on it — and unlike D5's fork, this
routing **cannot** silently no-op, because `owner_agent_id` is a required, validated agent id.

**Independent Test**: Terminate one plan created by an agent and one created by a human through the
UI. Assert both `owner_agent_id`s are woken over the bus, and that the human-created plan
additionally produces a notification for that username.

**Acceptance Scenarios**:

1. **Given** any plan, **When** it reaches a terminal state, **Then** its `owner_agent_id` is woken
   over the bus with the handover text.
2. **Given** a plan created through the UI by user `alice`, **When** it reaches a terminal state,
   **Then** a notification is additionally persisted for `alice` and pushed over the WS to her
   connections.
3. **Given** a plan created by an agent through `create_plan`, **When** it reaches a terminal state,
   **Then** **no** notification file is written keyed on an agent id.
4. **Given** a human-created plan, **When** the notification is created, **Then** it carries the plan
   id so the UI can navigate to the plan.
5. **Given** the notification store returns an error, **When** a human-created plan terminates,
   **Then** the bus wake still succeeded, the failure is logged at ERROR, and the plan's terminal
   state is unchanged.
6. **Given** PlanSupervisor is unavailable and `supervision.attempts` is **below** the ceiling,
   **When** the supervision deadline elapses, **Then** the plan **stays** at `awaiting_supervision`,
   the supervision wake is re-issued, and the `owner_agent_id` is **not** notified — there is nothing
   yet to tell it.
7. **Given** PlanSupervisor is unavailable and `supervision.attempts` has **reached** the ceiling,
   **When** the deadline elapses once more, **Then** the plan **terminates**
   `failed(supervision_unavailable)` and the `owner_agent_id` is woken with an
   adjudication-unavailable handover.
   *Rev 2 stated AS6 as "stays parked **and** the Owner is woken", which is a state the spec never
   defines: FR-019/FR-022(b) wake the Owner **only** at the ceiling, at which point the plan is no
   longer parked. Test #36 drives to the ceiling and therefore could not satisfy the scenario it
   traced to (r3 M3-03). The two outcomes are now two scenarios.*

---

### User Story 5 — A parked plan survives a restart and is re-woken (Priority: P1)

The parked phase is the durable on-disk record that a decision is outstanding. Today a restart
mid-adjudication silently re-parks with no wake: `processPlan`'s phase switch has no case for the
parked phase, so the plan falls through to `beginPlanJudgeRound` and is stopped only by the F2
signature gate — which skips the round without waking anyone. One dropped bus publish, one
restart, or one nil notifier and the plan is parked forever with nobody woken.

**Why this priority**: P1 rather than P0 because the happy path works without it, but it is the
difference between "eventually adjudicated" and "silently stuck", and it is cheap.

**Independent Test**: Park a plan at `awaiting_supervision` with a **stated** `supervision.wake_at`,
restart the engine, and assert **at most one** supervision wake is issued per restart, that a plan
whose deadline has already elapsed is re-woken on the first tick after boot, and that no judge round
is consumed.

**Acceptance Scenarios**:

1. **Given** a plan persisted at `awaiting_supervision` whose `supervision.wake_at` deadline **has
   already elapsed**, **When** the gateway restarts, **Then** PlanSupervisor is woken on the first
   tick after boot, and **at most one** wake is issued for that plan per restart.
1b. **Given** a plan persisted at `awaiting_supervision` whose `supervision.wake_at` deadline has
   **not** elapsed, **When** the gateway restarts, **Then** **no** wake is issued — the deadline is
   honoured from its original stamp and is not re-armed, so a restart loop cannot reset the ceiling
   (Dataset E12). The plan is re-woken when that original deadline elapses, not at boot.
   *Rev 2 asserted "woken exactly once when the gateway restarts" while FR-023 dedups on `wake_at`
   and E12 says the deadline is honoured from its original stamp — so a restart inside the window
   produces **zero** wakes. The scenario was untestable because it never stated `wake_at`'s
   pre-restart value (r3 M3-12). The precondition is now stated and the case is split in two.*
2. **Given** the same restart, **When** the re-wake fires, **Then** `judge_rounds` is unchanged and
   the persisted unmet signature is untouched.
3. **Given** the engine is constructed with no notifier, **When** it starts, **Then** startup fails
   with a clear error rather than running with an inert wake path.
4. **Given** a wake publish that returns an error, **When** it fails, **Then** the failure is
   recorded on the plan and surfaced, and the wake is retried on a later tick.
5. **Given** a plan already at its round ceiling parked at `awaiting_supervision`, **When** the
   gateway restarts, **Then** the plan terminates rather than being re-woken indefinitely.

---

### User Story 6 — Every correction is attributable and reviewable after the fact (Priority: P1)

PlanSupervisor is an autonomous decision-maker that mutates plans. An operator must be able to
answer *"why did this plan change?"* without reading JSONL by hand — which verb was chosen, which
member was superseded or retried, what assumption was falsified, and when.

**Why this priority**: It is the accountability half of granting an agent a privileged mutation
verb, and the data is already produced — only exposure is missing.

**Independent Test**: Apply one correction of each mutating verb, then read `GET /api/v1/audit-log`
as an operator and assert all three appear with their actor, verb, target member id and falsified
assumption.

**Acceptance Scenarios**:

1. **Given** a plan with three applied corrections, **When** an operator reads the **audit log**
   (`GET /api/v1/audit-log`, verified reachable at `pkg/gateway/rest.go:4883`), **Then** all three
   appear with actor, verb, **target member id**, **falsified assumption** and timestamp.
   *Rev 2 wrote this against "the plan's revision history" while FR-037 simultaneously verified that
   **no read surface for revision entries exists** and shipped only (a) the entry in the
   `plan_correct` tool result — visible only to the agent being audited — and (b) a `SHOULD` for a
   route AMB-5 then deferred. FR-039b's audit entry carried the actor, plan id and verb but **not**
   the target member or the falsified assumption, so the two artefacts US-6 needs most were readable
   by nobody (r3 M3-08). Per **D-03**, the audit entry is widened to carry all four and this scenario
   is restated against the surface that actually ships.*
2. **Given** a correction with verb `supersede`, **When** the history is read, **Then** the entry
   is distinguishable from an `append` entry without inspecting free text.
3. **Given** a gateway restart after corrections were applied, **When** the history is read again,
   **Then** it is unchanged.
4. **Given** a correction is rejected by validation, **When** the history is read, **Then** no
   entry exists for the rejected attempt.

---

### User Story 7 — "Owner" means one thing, and the parked phase names the right actor (Priority: P1)

"Owner" currently denotes four different concepts, and one name (`OwnerSessionID`) denotes two
incompatible ones. Most acutely, the durable phase is called `awaiting_owner_correction` while
under this spec the Owner never corrects — freezing an error into a **wire enum**, where it becomes
permanent. This story applies one binding definition everywhere, across the five live S9 rows.

> **No migration.** Greenfield (operator ruling 3). Pre-rename records are expected not to load;
> that is accepted. The work is a *code and contract* rename whose only hard part is the surface no
> compiler sees.

> **Canonical definition (binding).** **Owner** = the principal accountable for a thing: who
> created it, who receives its outcomes, and who may stop it. **Nothing else.** Anything currently
> named `Owner*` that is not that is renamed to what it is.

**Why this priority**: P1, and it ships *before* the feature work in file order (FR-070) so the rest
is written once against final names. The confusion is not hypothetical — it recurred repeatedly
across three ADR revisions and three reviews. **Note the priority inversion this creates** (a P1
blocking a P0) and why it is accepted: under greenfield the rename is a compiler-driven edit with no
migration, the only genuine coupling is `plan/SKILL.md:158` holding the phase literal, and writing
steps 3–9 twice costs more than sequencing them once. Recorded as RISK-10 rather than assumed.

**Independent Test**: After the rename, the mechanical `rg` command in SC-011 returns zero hits, a
freshly-created `$OMNIPUS_HOME` drives a plan through the parked phase under its new name, and the
SPA plans list renders it with no `ApiSchemaError`.

**Acceptance Scenarios**:

1. **Given** the rename has landed, **When** a plan enters the parked phase, **Then** it persists as
   `plan_phase: "awaiting_supervision"`, is readable, stoppable and correctable, and renders in the
   SPA plans list with no schema error.
2. **Given** the rename has landed across S9 rows 4 and 5, **When** the gateway boots and the sweep
   runs against a paused `needs_input` session whose `ScopeKind` is set, **Then** the session is
   preserved, not marked `failed(interrupted)` — `boot_sweep.go`'s single live ownership gate still
   resolves under its new field name.
3. **Given** the rename has landed, **When** the mechanical sweep of SC-011 runs, **Then** it returns
   zero hits — including inside `src/**`, `pkg/skills/embedded/**`,
   `pkg/gateway/inboundschemas/**` and `tests/e2e/**`, none of which any compiler checks.
4. **Given** the rename has landed, **When** PlanSupervisor loads `plan/SKILL.md`, **Then** the
   text contains no reference to the retired phase value, no instruction denying its own existence,
   and a verb table that agrees with `validateCorrection` rather than contradicting it.
5. **Given** the rename has landed, **When** `make verify-contracts` runs, **Then** it passes with
   no drift.

---

### User Story 8 — Stopping a plan stops everything working on it, including the supervisor (Priority: P1)

An autonomous supervisor that mutates plans needs a containment control. The operator's ruling is
that the control already exists and must simply be made complete: **Stop is plan-scoped, and it
cascades to every actor working on that plan.** And an *agent* that starts a plan from a chat has
`create_plan` and `execute_plan` but **no way to stop what it started** `[FACT — verified:
`pkg/tools/plan.go` registers exactly `create_plan` (`:114`) and `execute_plan` (`:376`); there is
no stop tool]`. This story closes both halves.

> ### ⚠ Premise correction (rev 3) — how much of the cascade is real
>
> Rev 2 wrote: *"Today `StopPlan` cascades to every member session, every verifier session and the
> owner session."* **That is two-thirds true, and the false third is the one the containment claim
> was resting on.** Verified 2026-07-27:
>
> | Fan-out leg | Built at | Does it actually stop a turn? |
> |---|---|---|
> | each `in_progress` member's `t.SessionID` | `plan_engine.go:1700-1702` | **Yes** — a real session id. |
> | every registered verifier session (`registry().SessionsFor(units…)`) | `:1704` | **Yes.** These are `session_<ULID>` ids minted by `NewVerifierSession` and stamped as the turn's `transcriptSessionID` (`verifier_adjudication.go:529`, `:893`, `loop.go:4723`), so `GetActiveTurnHookForSession` matches them. **This is the leg that works, and the pattern FR-016b copies.** |
> | the owner session `p.OwnerSessionID` | `:1710-1712` | **No — it is a no-op today.** `ensureOwnerSessionLocked` writes the string `"plan:"+p.ID` to the plan record (`:2473-2474`) and **creates no session**; nothing in `pkg/` ever does (**N13**). `RequestCancelForSession` finds no `turnState` with that `transcriptSessionID`, returns `(false, nil)`, and `cancelSessions` discards the `fired` bool and only logs on `err != nil` (`:1825`) — so a leg that cancels nothing is indistinguishable from one that works. |
>
> So FR-044's rev-2 framing — *"extending an existing cascade rather than any new mechanism"* —
> understated the work. Adding an id to a slice is mechanically trivial and stops nothing on its
> own: the id must name a **real, store-backed session that a live turn is actually bound to**
> (FR-016b), and the turn must have been dispatched by a path that runs at all (FR-012c, **N15**).
> Making the *Owner's* session real is out of scope (O11, RISK-13); making the *supervision*
> session real is mandatory.

> **Why the old assertion could not have caught it.** `plan_stop_test.go`'s `fakeSessionCanceller`
> (`:28-38`) records the session id and returns `(true, nil)`. A test asserting *"the supervision
> session appears in the cancel fan-out set"* passes against a control that cancels nothing — which
> is why operator ruling 5 requires SC-020 and #63 to assert the **turn halted**.

> **Why containment is plan-scoped and not agent-scoped.** A global "disable PlanSupervisor" switch
> is both the wrong shape and unbuildable. Wrong shape: the thing an operator wants stopped at 3 AM
> is *this runaway plan*, not adjudication across the whole install — disabling globally strands
> every other parked plan. Unbuildable: `updateAgentTools` returns **403** for a `Locked` agent
> (`pkg/gateway/rest.go:6789-6793`) and `seedSystemAgents` re-stamps the seeded policy map on every
> boot — a re-enforcement FR-002 *mandates* and test #8 *asserts*. Any tool-policy kill switch would
> be rejected on write and reverted on reboot. See O9. **Rev 1's NFR-6, SC-020, §20 runbook and
> holdout H7 all rested on that impossible control and are replaced here.**

**Why this priority**: P1, raised from rev 1's P2. It does not block the loop closing, but it is the
only containment control over an autonomous mutating agent, and half of it (the agent-facing stop) is
a capability gap that exists today independent of this feature.

**Independent Test**: Start a plan, drive it to a supervision wake, and **block the supervision turn
so it is observably in flight**. Stop the plan from the SPA ■ button. Assert **the blocked turn
terminates** — its context observes cancellation and it returns — within a bounded time; that no
correction lands after the stop; and that the plan is `failed(stopped_by_user)`. A canceller double
that records the id and returns success does **not** satisfy this test. Separately, from an agent
chat, call `stop_plan` on a plan that agent owns and assert the same cascade; call it on a plan it
does not own and assert denial.

**Acceptance Scenarios**:

1. **Given** a plan whose supervision turn is in flight, **When** the plan is stopped (SPA ■ button
   or `stop_plan`), **Then** **that turn stops** — the live turn bound to `supervision.session_id`
   is claimed and cancelled by the same fan-out that cancels member and verifier sessions — and no
   correction is applied after the stop.
1b. **Given** the same stop, **When** the fan-out runs, **Then** the id it cancels is **the same id
   the supervision turn actually persisted to**: `supervision.session_id` resolves to a real,
   store-backed session (`ResolveSessionStore` returns non-nil) and that session's transcript holds
   the supervision turn.
   *Rev 2's AS1 asserted membership in a set. Membership is satisfied by a synthetic id nothing is
   bound to, and the specified test passed regardless (r3 C3-05, operator ruling 5). AS1 now asserts
   the outcome and AS1b asserts the identity that makes the outcome possible.*
2. **Given** an agent that owns a running plan, **When** it calls `stop_plan` naming that plan,
   **Then** the plan transitions to `failed(stopped_by_user)` with the calling agent recorded as the
   actor, and the full cascade runs.
3. **Given** an agent that does **not** own a plan, **When** it calls `stop_plan` naming that plan,
   **Then** the call is denied with a response that does not differentiate "not yours" from "does
   not exist".
4. **Given** a stop and a correction issued concurrently, **When** both are processed, **Then**
   `planDecisionMu` serialises them, exactly one takes effect first, and a correction that loses the
   race is rejected against the now-stopped plan rather than partially applied.
5. **Given** a correction is applied, **When** an operator reads the logs, **Then** a structured
   line records plan id, verb, target member and outcome.
6. **Given** a plan parked at `awaiting_supervision` for longer than the idle-expiry budget,
   **When** the sweeper runs, **Then** the existing idle-expiry path terminates it — supervision
   does not create an immortal record.
7. **Given** an operator at 3 AM, **When** they open the Plans screen, **Then** they can see how many
   plans are currently at `awaiting_supervision` and stop any one of them without a redeploy or a
   restart.

---

## 8. Behavioral Contract

**Primary flows**

- When a plan's DoD is judged UNMET, the system parks the plan at `awaiting_supervision` and wakes
  **PlanSupervisor**, not the Owner.
- When a plan's DAG cannot progress (stalled), the system clears any stale stall note, then wakes
  **PlanSupervisor** with a stall diagnosis, not a DoD verdict request.
- When a plan's DoD is judged MET, the system wakes the **Owner** to write the closing synthesis —
  unchanged. This is the only wake on the success path and it stays where it is.
- When a plan reaches a terminal state or is stopped, the system delivers the outcome to the
  plan's **`owner_agent_id`** over the bus, and additionally to the notification store + WS when
  `owner` names a configured user.
- When the system issues a supervision wake, it **dispatches a real agent turn for PlanSupervisor**,
  bound to an engine-minted, store-backed supervision session — it does not merely publish an event
  that a downstream guard discards (FR-012c, N15).
- When PlanSupervisor calls `plan_correct` on a plan in a **supervision-eligible phase** —
  `awaiting_supervision` **or** `stalled` (FR-029) — the system applies the correction
  transactionally, records a revision entry, clears the unmet signature, the stall note and the
  supervision wake receipt, and returns the plan to `dispatching`.
- When a correction is applied, the system charges no extra judge round; the re-judge it provokes
  charges the round. It **does** increment the correction-round counter, which is an attribution
  counter and not a second budget.
- When the engine boots and finds a plan parked at `awaiting_supervision`, the system re-wakes
  PlanSupervisor exactly once without consuming a judge round.
- When a plan's owner agent calls `stop_plan`, the system stops the plan and cascades the cancel to
  every member session, every verifier session, the owner session **and** the supervision session —
  and the in-flight supervision **turn actually halts**, because that session id is the one the turn
  is bound to (FR-016b, FR-044).
- When an agent is seeded with `execute_plan`, it is seeded with `stop_plan` in the same map at the
  same policy level — **no agent can start a plan it cannot stop** (FR-006b, SC-004b).

**Error flows**

- When any principal other than PlanSupervisor calls `plan_correct`, the system denies it with a
  response identical for every denial reason — including for a plan that does not exist.
- When a `supersede` carries no replacement work, or carries replacement work that inherits none of
  the superseded member's acceptance criteria, the system rejects the correction before any mutation.
- When a `targeted_retry` names a member that is not `failed`, or a `supersede` names a member that
  is not `done`, the system rejects the correction — without naming the other plan the member
  belongs to.
- When a correction's `tail_edges` would introduce a cycle, name an unknown member, or point at a
  superseded member, the system rejects the correction before any mutation.
- When a supervision turn produces no valid correction, the system re-issues the supervision wake on
  a later tick, up to an explicit attempt ceiling.
- When that ceiling is exhausted, the system terminates the plan with
  `failed_reason = supervision_unavailable` and notifies the Owner — it never leaves the plan
  unattended.
- When a wake publish fails, the system records the failure on the plan and retries on a later
  tick rather than logging a warning and continuing, up to the same ceiling.
- When the engine is constructed without a notifier, `Start` fails with an explicit error.
- When a plan's `owner` does not name a configured user, the system writes no notification and
  relies on the bus wake, which has already succeeded.
- When a correction leaves the plan unable to progress, or PlanSupervisor abandons the plan, the
  system fails the plan with `failed_reason = dod_unreachable` — a distinct enum value, not an
  overloaded `judge_rounds_exhausted`.
- When a plan's intent log cannot be read at boot, the system surfaces the error and fails the plan
  closed rather than proceeding with an incomplete superseded set.

**Boundary conditions**

- When the plan judge round budget is exhausted, the system terminates the plan through the existing
  `judge_rounds_exhausted` reason, with the message naming whether corrections consumed the budget
  or the ceiling was simply reached.
- When a plan is parked and already at its round ceiling, a restart terminates it rather than
  re-waking.
- When a correction and a stop race, the process-wide `planDecisionMu` serialises them and exactly
  one wins.
- When a plan record predates the rename, it does not load — greenfield, by decision.
- When a user attempts to chat with, or star as default, any agent whose type is `system`, the
  system rejects the request.
- When a `tail_members` or `tail_edges` collection exceeds its cap, the system rejects the correction
  rather than holding `planDecisionMu` for an unbounded payload.

---

## 9. Edge Cases

| # | Condition | Expected behaviour |
|---|---|---|
| E1 | `plan_correct` called on a plan whose phase is **outside the supervision-eligible set** `{awaiting_supervision, stalled}` | Rejected with the phase-mismatch error; no mutation. **Changed in rev 3 (D-01):** rev 2's gate was `!= awaiting_supervision` only, which rejected every correction a stall wake provoked (r3 C3-01). FR-029 widens it to the set. |
| E1b | `plan_correct` called on a plan at `stalled` | **Accepted on the phase**, then judged on the merits by `validateCorrection` exactly as a parked plan is. The applied correction clears the stall note and returns the plan to `dispatching` (FR-029). |
| E33 | A supervision wake is issued and the delivery path drops it before any turn runs | **Must not be reachable.** This is today's behaviour for every plan wake (N15) and FR-012c forbids it: the wake path MUST dispatch a turn. SC-025 asserts a turn ran, not that the notifier was called. |
| E38 | A plan has **no** `source_channel` / `source_chat_id` (created over REST from the Plans UI, or by an agent in a context with no channel) and reaches a terminal state | **Legitimate and expected** (operator ruling 10) — both fields are `omitempty`, exactly as `Task`'s are. **The Owner turn still runs**, dispatched directly with `SendResponse: false` and bound to `Plan.OwnerSessionID`, so the closing synthesis is persisted; **no outbound message is published anywhere**; the human surface is the `plan_completed`/`plan_failed` notification (FR-012d(4), D-10). Logged INFO with `reason: "no_chat_origin"`; **not** a `wake_error`, does **not** increment the supervision attempt count, and never yields `failed_reason = supervision_unavailable` (**N16**). Supervision wakes are unaffected — family A never reads the origin. |
| E41 | A plan has a `source_channel` but an **empty** `source_chat_id` (or the reverse) | **Treated exactly as E38** — the FR-012d(4) predicate is `both non-empty`, deliberately identical to FR-N7's. Stated separately because a partial origin fails *differently* from an empty one if it is allowed through: `"telegram:"` parses to a **non**-internal channel with an empty chat id, so a turn would run and be addressed to nowhere, rather than being dropped `[FACT — both shapes executed and confirmed 2026-07-28]`. Neither is reachable under FR-012d(4). |
| E42 | Someone removes or relaxes `AsyncNotifier.Notify`'s FR-N7 empty-destination guard | **Re-opens N16's candidate 1**: an empty origin composes the bus `ChatID` `":plan:<id>"`, `strings.Index` returns **0**, `processSystemMessage`'s `if idx > 0` guard fails, and the `else` branch assigns `originChannel = "cli"` — **also** an internal channel — so the wake is dropped with a log line indistinguishable from correct behaviour. FR-012d(4)'s construction guard means the spec does not *depend* on FR-N7, but the two are the same predicate by design and §13.4 pins the guard so the redundancy is not silently removed. |
| E39 | A plan's origin channel is `webchat` and the browser tab has closed by the time the wake fires | **The origin is still recorded and still used.** The turn runs and its synthesis is persisted to `Plan.OwnerSessionID` (FR-016c); `webchatChannel.Send` then returns `channels.ErrSendFailed` because no connection is bound to the chat (`pkg/gateway/webchat_channel.go:92-95`), which the Manager classifies **PERMANENT** and logs once — no retry storm. **This is why plans do NOT copy `pkg/tools/task.go:541`'s `webchat` exclusion** (N15.3): for a Task the origin drives only a raw `PublishOutbound`, so excluding it costs nothing; for a Plan the origin drives a *turn* whose output is persisted regardless, so excluding it would silently un-address every plan created from the SPA chat — the most common creation path. |
| E40 | A `source_channel` / `source_chat_id` is supplied in a `POST /api/v1/plans` body or a plan PATCH | **Ignored.** Both are server-set at creation only, like `Owner`/`CreatedBy` (FR-012d(1)(2)). A client-supplied origin would let a caller redirect another principal's plan outcome into a conversation of its choosing. |
| E34 | `StopPlan` cancels `supervision.session_id` but no live turn is bound to it (the turn already finished) | `RequestCancelForSession` returns `(false, nil)` and the cascade continues — a benign no-op. **This is why `supervision.session_id` is NOT cleared when the plan leaves the supervision-eligible phase** (FR-050): an applied correction moves the plan to `dispatching` while the turn may still be running, and erasing the handle in that window would make the stop uncancellable (m3-07). The id is overwritten when the next supervision session is minted, never blanked. |
| E35 | A `supersede` names a `done` member that carries **zero** acceptance criteria | **Accepted** (subject to FR-030's `len(tail_members) > 0`). FR-030b's predicate is *"the replacement carries every criterion of the superseded member"*, which over an empty set is vacuously satisfied. Stated explicitly because "carries none of `[]`" is ambiguous and rev 2 left it undefined (r3 M3-05). |
| E36 | A `supersede` whose replacement carries a **strict subset** of the superseded member's criteria | **Rejected.** FR-030b requires **every** criterion, not at least one. Rev 2 said *"carries none of"*, which is satisfied by 1-of-N — the exact bypass FR-030b exists to close, at the same one-throwaway-member price (r3 M3-05). |
| E37 | A stalled plan's supervision turn produces nothing until the attempt ceiling | Terminates `failed(supervision_unavailable)`, identically to a parked plan (FR-029, FR-022). Without D-01's widening, a stall armed no deadline, incremented no `attempts`, hit no ceiling, and was bounded only by 7-day idle expiry. |
| E38 | A plan record carries `supervision` fields while a REST `PUT /api/v1/plans/{id}` updates an unrelated field concurrently | The five `plan.Patch` supervision pointers are all nil on the REST patch, so `updateLocked` leaves them untouched (FR-050). A single whole-object `Supervision **Supervision` pointer would have made every engine write a read-modify-write and every REST write a potential clobber — `rest_plans.go`'s `Store.Update` callers do **not** hold `planDecisionMu` (r3 M3-16). |
| E2 | `plan_correct` called on a plan that is not `running` | Rejected with the existing state error; no mutation |
| E3 | `supersede` naming a member belonging to a different plan | Rejected by `validateMemberRef`'s plan-ownership check |
| E4 | `append` with an empty `tail_members` | Rejected (existing behaviour, preserved) |
| E5 | `supersede` with `tail_members` whose ids already exist | **Two cases, now distinguished (N11).** *Intent-log replay*: idempotent — `buildCorrectionApplyFunc` skips existing tasks; replay-safe, preserved. *First application*: **rejected** — under FR-046 member ids are engine-minted, so a caller-supplied colliding id cannot occur at all; the skip path is reachable only on replay. Rev 1 classified the silent drop as a feature because it never distinguished the two. |
| E6 | Correction applied while a judge round is in flight | Serialised by `planDecisionMu`; the correction clears the signature and the next round re-judges |
| E7 | Stop issued while a correction is being applied | Serialised by `planDecisionMu`; whichever acquires first wins, the other observes the new state and fails cleanly |
| E8 | Plan owner agent deleted while the plan runs | Existing `HasActivePlansOwnedBy` guard blocks the delete; unchanged |
| E9 | `owner` names a username that no longer exists in `Gateway.Users` | No notification is written (the user lookup fails closed); the bus wake to `owner_agent_id` still succeeded; WARN logged |
| E10 | `owner_agent_id` names an agent that was deleted | **Unreachable through the REST delete path; reachable by config edit.** The `HasActivePlansOwnedBy` guard is a **REST-handler** guard only (`pkg/gateway/rest.go:2660`) — a `config.json` edit or any non-REST removal is unguarded. When it happens the bus publish fails → the FR-024 escalation records it on the plan. Exercised by Dataset C8. |
| E11 | `owner` is byte-identical to a configured username **and** to an agent id | A notification is written for the real user **and** the bus wake to `owner_agent_id` still happens. Harmless: delivery is additive, never exclusive — this is why routing on two fields is safer than a single derived kind |
| E12 | `owner` is empty (a plan written before the attribution fields existed) | No notification; the bus wake to `owner_agent_id` still succeeds. **No migration is required for delivery** — this is the scope reduction C6 buys |
| E12b | `Gateway.Users` is empty (single-user / `dev_mode_bypass` install) | No human notification is ever written; the bus wake is the only delivery. Documented, not an error — see AMB-8 |
| E13 | A plan record persisted before the S9 row-1 rename | **Does not load. Accepted** — greenfield (operator ruling 3). No migrator, no upgrade-on-read, no sentinel. Any existing dev/UAT `$OMNIPUS_HOME` is recreated, not upgraded. |
| E14 | A session-lifecycle record persisted before the S9 row-4 rename | **Does not load. Accepted** — same ruling. The boot-sweep hazard rev 1 mitigated is *unreachable*, not mitigated: with no legacy records there is nothing to deserialise with an empty `ScopeKind`. |
| E15 | An operator points the new binary at an old `$OMNIPUS_HOME` anyway | Records fail to load and the failure is visible in `gateway.log`; the operator recreates the directory. Documented in `docs/operations/`, not engineered around. |
| E16 | `plan_correct` or `stop_plan` registered before its name is added to `allStaticToolNames` | `validateOverrideKeys` **panics** at first seed call — ordering is a hard requirement (FR-006) |
| E17 | PlanSupervisor seeded with `Skills: nil` | Would grant **every** skill; forbidden — FR-007 requires an explicit non-nil list |
| E18 | Operator edits PlanSupervisor's SOUL.md | Preserved across boots; never overwritten (FR-005) |
| E19 | Operator edits PlanSupervisor's Model/Provider | Preserved across boots (FR-005) |
| E20 | Operator edits PlanSupervisor's `type`, `locked`, `default`, `memory_enabled` or tool policy | Repaired on the next boot (FR-005) |
| E21 | A stall wake arrives while the plan is parked at `awaiting_supervision` | **The *phase* cannot be masked** — `surfaceStallIfAny` returns before any `planStore.Update` while the parked phase holds (`:1225-1230`). Asserted by a regression test (FR-012). **But the *note* is a different matter (N9):** the stall-note-clearing branch (`:1234-1241`) sits behind the same guard, so a note carried in from a prior stall persists into the parked phase and lands in the adjudicator's input. FR-025 clears it on entry. Rev 1 asserted mutual exclusion of phases and read it as mutual exclusion of state; it is not. |
| E22 | The intent-log JSONL for one plan is truncated or corrupt at boot | **Surfaced, not swallowed (FR-048).** Today `reconstructCorrections` `continue`s silently, which *un*-supersedes members and re-admits discounted evidence to the judge — a path to a false MET. The plan is failed closed with an ERROR naming the plan id. |
| E25 | PlanSupervisor's turn produces **no tool call at all** | The supervision deadline (FR-021) expires on a later tick, `supervision.attempts` increments, and the wake is re-issued (FR-022) up to the ceiling. This is limb (c) of FR-019, made observable. |
| E26 | PlanSupervisor emits a `plan_correct` that validation **rejects** | Nothing mutates (FR-030 rejects before any write), so the plan returns to the exact state that produced the wake. Without FR-022 that is a permanent strand; with it, the attempt counter advances and the wake re-issues. |
| E27 | `tail_edges` describes a cycle | Rejected by FR-046's cycle check before any mutation. Unchecked, a cycle is unresolvable by the dispatcher and — combined with the once-per-park wake — strands the plan permanently. |
| E28 | `tail_edges` names a member that does not exist, or one that is superseded | Rejected by FR-046 before any mutation. |
| E29 | `targeted_retry` carrying 50 `tail_members` | Rejected by FR-046 — `TailMembers` is legal only on `append` and `supersede`. Today `AppendCorrection` sets `Members: req.TailMembers` unconditionally (`:2621`) and would create all 50. |
| E30 | A plan is stopped while its supervision turn is in flight | The supervision session is cancelled by `StopPlan`'s fan-out (FR-044). A `plan_correct` that arrives after the stop is rejected by `AppendCorrection`'s existing `p.State != plan.StateRunning` check. |
| E31 | `stop_plan` called by an agent that is not the plan's `owner_agent_id` | Denied, with a response that does not differentiate "not yours" from "does not exist" (FR-043, mirroring FR-010). |
| E32 | `stop_plan` called on a plan already `failed` or `done` | Rejected with the existing state error; no cascade, no second terminal write. |
| E23 | `plan_correct` invoked with a `dod` or `owner_agent_id` field | Structurally impossible — `CorrectionRequest` has no such field. Asserted by a conformance test (FR-032) |
| E24 | `ensureOwnerSessionLocked` fails to persist `OwnerSessionID` | Surfaced, not swallowed (FR-024); `requireOwner`'s clause 3 becomes a no-op, which FR-009 already tolerates by design |

---

## 10. Explicit Non-Behaviors

- The system must **not** expose correction over REST in this release, because `HandlePlans` has no
  per-plan authorization to inherit, the handler would hold the process-wide `planDecisionMu`
  unrate-limited, and there is no SPA client for it.
- The system must **not** let the plan's Owner apply corrections, because adjudication authority is
  what distinguishes a purpose-built supervisor from the dropdown lottery this feature removes.
- The system must **not** let any agent other than PlanSupervisor apply corrections — **including**
  a user-created agent whose tool policy resolves `allow` from the global ceiling (N2).
- The system must **not** allow a `supersede` that carries no replacement work, because that is
  discounting evidence rather than correcting work.
- The system must **not** add a second **judge-round** budget, because one configured, surfaced,
  already-terminal budget is sufficient and two would diverge.
  **Precise scope, corrected in rev 2.** Two things rev 1's blanket wording forbade are added here,
  and neither is a second judge-round budget:
  (a) `supervision.correction_rounds` is an **attribution counter**, not a budget — nothing gates on
  it; it exists only so FR-035 can tell "the ceiling was reached" apart from "corrections consumed
  the budget", which are otherwise *the same predicate at the same line* (`plan_engine.go:1288-1291`,
  whose handover builder's entire input is `(p, maxRounds)`);
  (b) `supervision.attempts` **is** a ceiling, but over a **different quantity** — supervision turns
  that produced no valid correction. The judge-round budget provably cannot bound it, because a
  rejected correction increments nothing (`applyJudgeRoundOutcomeLocked:1495` is the sole
  incrementer and validation returns before it is reached). Without a second ceiling that quantity is
  unbounded *and* unobserved, which is r2 C2-02.
- The system must **not** increment `JudgeRounds` from the correction path, because
  `applyJudgeRoundOutcomeLocked:1495` is the declared sole incrementer and the re-judge a
  correction provokes already charges the round — charging both halves the effective budget.
- The system must **not** delete or rename `Plan.OwnerAgentID`, because it is `required` under
  `additionalProperties: false` in two schemas and carries eight live jobs including
  `requireOwner`'s own authority subject.
- The system must **not** delete `OwnerSessionID`, `OwnsPlanID` or the `plan:<id>` linkage, because
  spec FR-118/FR-147 `[P1]` resolve the boot-sweep exemption through them by name.
- The system must **not** enforce plan immutability inside `plan.Store.updateLocked`, because 18 of
  21 non-test writers are the engine writing non-draft plans through that function — a blanket
  guard there would stop every plan advancing past `approved`.
- The system must **not** freeze `Plan.Bounds` on a running plan, because an operator may
  legitimately extend a running plan's idle-expiry/judge-round budget, and under FR-034 that budget
  now bounds corrections too.
- The system must **not** seed `plan_correct` with a global ceiling of `deny` or `ask`, because
  strictest-wins would overrule PlanSupervisor's own `allow` (N1).
- The system must **not** leave PlanSupervisor's skill allowlist `nil`, because `nil` means
  unrestricted (N3).
- The system must **not** grant PlanSupervisor `bash`, `write_file`, `edit_file`, `append_file`,
  `set_config`, `create_agent`, `delete_agent`, `create_plan`, `execute_plan`, `stop_plan` or
  `run_task`, because NFR-2's DoD-immutability guarantee is contingent on it holding no path to the
  plan record other than `CorrectionRequest`. **The allow set is exactly `plan_correct`; every other
  name in `allStaticToolNames` resolves `deny`** (FR-008, SC-004).
- The system must **not** make PlanSupervisor a chat target, a delegation target, or the starred
  default agent.
- The system must **not** implement rollback of a correction; recovery remains stop-and-re-author.
- The system must **not** add per-member adjudication or any wake in the happy path beyond the
  existing ~1 per plan.
- The system must **not** "helpfully" widen the notification `type` enum to an open string; it must
  be widened explicitly in `contracts/` with the specific new values (**FR-017** — rev 1 cited FR-018
  here, which is dedup + click-through).
- The system must **not** re-target the MET closing-synthesis wake (`plan_engine.go:1571`) away from
  the Owner, because it is the only wake on the success path — the other two Owner wakes are
  failure-only and user-stop-only — and PlanSupervisor has no write path by which a synthesis could
  travel back (C15).
- The system must **not** let the LLM supply member ids for tail members; the engine mints them
  (FR-046). A caller-supplied id that collides with an existing member is silently dropped by
  `buildCorrectionApplyFunc`'s replay skip, and the correction reports success — a false-success path
  US-3 does not otherwise cover.
- The system must **not** build a global agent-level kill switch for PlanSupervisor. Containment is
  plan-scoped (US-8, O9); a tool-policy switch is rejected 403 on write and reverted on the next boot
  by a re-enforcement FR-002 mandates.
- The system must **not** give PlanSupervisor a way to enumerate the plans it supervises — no
  `list_jobs`, no roster, no plan-list tool. **PlanSupervisor is deliberately roster-blind** (D-04).
  It is woken per plan and adjudicates exactly the plan it was woken for; the **engine's**
  `supervision.wake_at` deadline is the only liveness control, and it is deliberately the engine's
  and not the agent's — an adjudicator that could notice it had three parked plans would be an
  adjudicator with a reason to act outside the wake it was given.
  **Recorded here because the gap is invisible from inside either spec** (cross-spec C3): a
  System Agent can never be a plan's `owner_agent_id` (`IsChatTarget()` = `!IsWorker() && !IsSystem()`,
  `pkg/config/config.go:1052`, and both `Plan.OwnerAgentID` write paths are guarded by it), and the
  supervision session is engine-minted rather than `delegate`-minted so it carries no
  `ParentAgentID` — so **neither** of `list-jobs-spec.md`'s two ownership predicates can ever match
  PlanSupervisor. That is a consequence of two independently reasonable decisions, not an oversight,
  and it is written down so the next author does not "fix" it.
  If a future change does want to grant it, that change MUST deliberately amend FR-008 and test #4
  to `len(allowed) == 2`. **The complement-complete assertion failing is the guard working**, not an
  obstacle to route around.
- The system must **not** record a **derived or synthetic** string as `supervision.session_id` —
  not `"plansupervisor:plan:" + id`, not the plan id, not any composed literal. It must be the id of
  a real, store-backed session (FR-016b). A synthetic id is dropped by `ResolveSessionStore`
  (`loop.go:6075`), the turn then runs with `transcriptSessionID == ""`, and
  `GetActiveTurnHookForSession` (`turn.go:444`) matches nothing — so the kill switch silently does
  nothing while every test that stubs the canceller passes (N13, C3-05).
- The system must **not** repair `Plan.OwnerSessionID`'s never-created `plan:<id>` session (O11,
  N13) or `execute_plan`'s global ceiling (O12, N14) as part of this change. Both are recorded, both
  are filed as risks, and neither is depended on by any requirement here — which is the stated test
  for what this spec fixes versus what it records. Contrast **N15**, which *is* fixed, because US-4,
  FR-012 and FR-019 all depend on it (D-08).

---

## 11. Integration Boundaries

### 11.1 LLM provider (PlanSupervisor's own turns)

- **Data in**: the plan record (**with any stale stall note already cleared** — FR-025), the judge's
  per-criterion verdict, member outcomes, `plan/SKILL.md`, PlanSupervisor's SOUL.
- **Data out**: exactly one `plan_correct` tool call — `append`, `supersede`, `targeted_retry` or
  `abandon`. **There is no third "conclusion" output.** Rev 1's "honest-exit conclusion" was not an
  observable artefact; `abandon` makes it one (FR-046).
- **Contract**: the standard agent turn path; Model/Provider resolve from PlanSupervisor's config,
  falling back to the install default exactly like every other built-in agent (no special-cased
  tier, no new configuration surface).
- **On failure — the observation seam.** The wake is fire-and-forget (§11.2); `wakeOwner` returns
  nothing about the turn and `pkg/agent` adds no callback into `PlanEngine` (N8). So the engine does
  **not** observe the turn — it observes **whether the plan record moved**, which is a complete
  proxy:

  | Rev-1 limb | Observable? | How rev 2 handles it |
  |---|---|---|
  | (a) provider error | **no** — never reaches `PlanEngine` | Folded into the deadline. A turn that errored produces no correction, which is what the deadline detects. **Limb (a) is deleted as a separate definition.** |
  | (b) timeout exceeded | **yes, via the deadline** | `supervision.wake_at` + `supervision_turn_timeout`, checked on a later tick (FR-021) |
  | (c) no tool call, no conclusion | **yes, via the same deadline** | Same mechanism; "conclusion" is now the `abandon` verb, so the disjunct is well-defined |

  **"Unavailable" is therefore one predicate:** on the first tick at which
  `now > supervision.wake_at + supervision_turn_timeout` **and** the plan is still at
  `awaiting_supervision` **and** the unmet signature is unchanged, the turn produced nothing.
  The plan stays parked, `supervision.attempts` increments, and either the wake re-issues (FR-022)
  or — at the ceiling — the plan terminates `supervision_unavailable` and the Owner is notified
  (FR-019). **Bounded per-tick retry, no backoff curve** (O7, revised).
- **Timeout value**: `supervision_turn_timeout` = **10 minutes**, a new
  `config.PlanningConfig` field (`supervision_turn_timeout_seconds`, default 600), per-plan
  overridable via `PlanBounds`. It is **not** the 10 s `wakeOwner` notify timeout, which bounds a bus
  publish, not an LLM turn — AMB-4 flagged exactly that confusion. 10 min is ~20× the observed
  plan-judge turn and is **exactly 20 ticks** (600 s ÷ 30 s), so a single slow turn never trips it.
  The predicate is **strict** (`now > wake_at + timeout`), so at exactly 20 ticks it does **not**
  fire; the first firing tick is 21. *(Rev 2 wrote "one tick short of 21 ticks", which obscured the
  boundary and is why Dataset E tested 19 and 21 but not 20 — r3 m3-06, m3-03. E3b now covers it.)*
- **Development**: mock provider in tests; **a scripted adjudicator double in the merge gate**, real
  provider nightly (**D-07**). The two claims are separable and are separated:
  - **#60b (`TestCorrectedPlanReachesDone_ScriptedAdjudicator`, integration, blocking merge gate)**
    proves the **loop closes** — the wake dispatches a turn, the turn's `plan_correct` is accepted,
    the plan resumes and reaches a terminal state with zero human input. The adjudicator is a scripted
    double, so it is deterministic. This is SC-001.
  - **#60 (`TestCorrectedPlanReachesDoneWithNoHumanInput`, E2E, nightly signal)** proves the
    **rubric works** against a real LLM. This is SC-001b.
  A #60 failure is a **rubric defect to fix**, tracked as an issue with a target date, not a merge
  blocker and never auto-quarantined. *(Rev 2 put the non-deterministic run in the blocking gate with
  a 2-retry policy, which puts a provider outage on the merge path and weakens neither claim to
  remove — r3 O3-03.)*

### 11.2 Async notifier bus (`AsyncNotifyEvent` → `PublishInbound`)

- **Data in**: `{Channel, ChatID, AgentID, **TranscriptSessionID**, SourceKind, Content}` — after
  rev 4, `Channel`/`ChatID` are `Plan.SourceChannel`/`SourceChatID` (FR-012d). *(Today they are the
  hardcoded `"system"` / `"plan:<id>"` that N15 shows is discarded.)*
  **`TranscriptSessionID` is the field rev 2 omitted from this list and from `wakeOwner` alike.** It
  exists on the struct (`pkg/agent/async_notifier.go:42-72`, the field at `:49-59`) and `Notify`
  forwards it to `bus.InboundMessage.AsyncTranscriptSessionID` (`:285`), but `wakeOwner`'s literal
  (`plan_engine.go:2102-2108`) never populates it — unlike the pattern it claims to mirror,
  `task_executor.go:1076-1082`, which does `[FACT — verified]`. FR-016b requires `wakeOwner` to take
  it as a parameter and set it.
- **`Channel` is load-bearing and is currently wrong.** `Notify` composes the bus `ChatID` as
  `fmt.Sprintf("%s:%s", event.Channel, event.ChatID)` (`:277`), and `processSystemMessage` parses the
  origin channel back out of that string and **drops the message before any turn runs** when it is
  internal (`cli`/`system`/`subagent`). `wakeOwner` hardcodes `"system"`. See **N15** — this is why
  FR-012c exists and why `Channel` is now listed as data-in rather than treated as a constant.
  **Rev 4:** `AsyncNotifyEvent.Channel`/`ChatID` become `Plan.SourceChannel`/`SourceChatID`
  (FR-012c(B), FR-012d). The `ChatID: "plan:<id>"` value in the data-in list above is **retired** —
  it survives in this document only as the description of today's broken behaviour.
- **⚠ `bus.InboundMessage.Channel` stays `"system"` and is NOT part of the fix.** The literal at
  `async_notifier.go:271` is the routing key `loop.go:5515` matches on to reach
  `processSystemMessage`, which rejects any other channel at entry (`:5992-5997`). Changing it breaks
  the seam outright. See **N15.1** — this is the misdiagnosis two prior analyses of N15 both made.
- **Which wake families use this seam, after D-11.** Only the **Owner** wakes (`:1571`, `:1610`,
  `:1742`). The two **supervision** wakes (`:1254`, `:1542`) leave the notifier entirely and dispatch
  directly (FR-012c(A)), because FR-016/H8 forbid PlanSupervisor's output reaching the plan's
  originating conversation and `processSystemMessage` has no knob to suppress `SendResponse`.
  FR-024's failure semantics are unchanged in shape and apply to both: `wake_error` records a
  *publish* failure for an Owner wake and a *dispatch* failure for a supervision wake.
- **Data out**: none (fire-and-forget today) — **and that is the point of FR-021**: because the path
  reports nothing back, the engine observes the *plan record*, not the turn.
- **Contract**: in-process Go; `Notify(ctx, AsyncNotifyEvent) error` with a 10 s timeout.
- **On failure**: today WARN-and-continue. **This spec changes it** (FR-024): the failure is
  recorded on the plan and retried on a later tick. A nil notifier becomes a `Start` precondition
  error (FR-024), scoped to `Start` only so in-package tests may still construct a `*PlanEngine`
  struct literal with a nil notifier.
  **A `Notify` that returns `nil` is NOT evidence of delivery** — it means the bus accepted the
  publish, and today every plan publish is then discarded downstream (N15). FR-024's `wake_error`
  therefore records *publish* failures only, and SC-025 — not `wake_error` — is what asserts a turn
  ran.
- **Development**: fake notifier capturing events (the existing in-package test pattern) is
  sufficient for the *routing* assertions (which `AgentID` a wake addresses) and **is not sufficient
  for any delivery assertion**. It records the call three hops upstream of the drop that makes
  delivery fail. Delivery is asserted at the turn, per SC-025.

### 11.3 Notification store (`pkg/notifications`) — human creators only, additive

- **Role**: **secondary, additive.** The guaranteed delivery is the bus wake to `owner_agent_id`;
  this surface exists only so a human who authored a plan through the UI learns it finished. A
  failure here can never mean "nobody was told".
- **Recipient selection**: `Plan.Owner`, **only when it resolves to a configured
  `Gateway.Users` entry**. An agent id will not match a username, so the store is never asked to
  write a file no reader opens — the failure `pkg/gateway/schedules.go:606-608` already documents
  (*"the admin-broadcast sentinel is NOT a real username — persisting it writes `_admin_.json`
  which no `ListForUser(username)` ever reads"*).
- **Data in**: `{Recipient: username, Type, Title, Body, Severity, PlanID}`.
- **Data out**: read by `ListForUser(user.Username)` via `GET /api/v1/notifications`; pushed per
  recipient over WS.
- **Contract**: `Notification.yaml` + `asyncapi.yaml`. **Both must be widened first** (**FR-017** —
  rev 1 cited FR-018 here). The `type` enum is a closed single value (`schedule_failed`) under
  `additionalProperties: false`, and there is no `plan_id` property.
- **Blast radius of getting the ordering wrong — three details rev 1 omitted, all verified:**
  1. **The REST failure is total, not per-row.** `src/lib/api.ts:838-847` `safeParse`s the **whole
     response body** against `NotificationListSchema`. One unknown `type` makes the entire
     notification list throw — **every** notification disappears, not just the unknown one. The
     notification centre goes blank rather than degrading.
  2. **The WS path fails differently and silently.** `src/lib/ws.ts:240-254`'s `parseFrameSafe`
     returns `null`, drops the frame and shows a dev-only toast. Two consumers, two unlike failure
     modes.
  3. **The AsyncAPI schema is an independent hand-maintained copy with a renamed field.**
     `contracts/asyncapi.yaml:2557,:2570` carries its own `NotificationFrame` whose event class is
     **`notification_type`**, not `type`, normalised by hand at `src/store/notifications.ts:46-52`.
     FR-017 must name all three sites — `Notification.yaml`, `asyncapi.yaml`'s
     `NotificationFrame.notification_type`, **and** the SPA normaliser.
- **Bonus defect this change must fix:** `Notification.yaml:22` describes `type` as *"The event
  class. **Extensible; consumers must tolerate unknown values.**"* while the enum is closed under
  `additionalProperties: false` and **neither** consumer tolerates one. The sentence is actively
  false and will mislead the next author; FR-017 corrects it in the same commit.
- **The same discipline applies to `failed_reason`** (S15). It is a closed enum at
  `Plan.yaml:140-153` under the same `additionalProperties: false`, so the two new values must land
  in `contracts/` before any code emits them.
- **On failure**: a create error is logged at ERROR and recorded on the plan; adjudication is
  unaffected (NFR-4).
- **Development**: real store against a temp `$OMNIPUS_HOME`.

### 11.4 Plan store + session-lifecycle store (persisted state)

- **Data in**: plan JSON at `$OMNIPUS_HOME/plans/*.json`; lifecycle JSONL at
  `$OMNIPUS_HOME/session_lifecycle/*.jsonl`.
- **Contract**: `json.Unmarshal` on read with **no** upgrade-on-read and **no** `schema_version`
  anywhere in the repo.
- **On failure**: a renamed key decodes to the zero value with no error. **Under the greenfield
  ruling this is accepted, not mitigated** — pre-rename records are expected not to load, no migrator
  ships, and any existing dev/UAT `$OMNIPUS_HOME` is recreated rather than upgraded. The boot-sweep
  hazard rev 1 engineered around is **unreachable**, because there are no legacy records to
  deserialise with an empty `ScopeKind`.
- **Development**: real stores against **freshly created** fixture directories. No pre-rename
  fixtures are authored, and no test asserts that a legacy record loads.

### 11.5 Embedded skills (`go:embed`)

- **Data in**: `pkg/skills/embedded/plan/SKILL.md`, compiled into the binary.
- **Contract**: plain markdown loaded into an agent's context at skill-resolution time; gated by
  the per-agent `Skills` allowlist (`nil` = unrestricted).
- **On failure**: none at runtime — but **no compiler sees inside it**, which is why FR-062's `rg`
  sweep and FR-040's prompt-regression test are required rather than optional.
- **Development**: assert on the embedded bytes in a Go test.

### 11.6 Tool ↔ engine seam (`pkg/tools` → `pkg/agent`) — **structural decision required**

- **Constraint**: `pkg/tools` **cannot** import `pkg/agent` (`pkg/agent` already imports `pkg/tools`
  — a cycle). But `CorrectionCaller`, `CorrectionRequest` and `CorrectionResult` are all declared in
  `pkg/agent/plan_engine.go` (`:2399`, `:2410`, `:2428`) `[FACT]`.
- **Decision (FR-004)**: move `CorrectionCaller` / `CorrectionRequest` / `CorrectionResult` to
  `pkg/plan` and re-export them from `pkg/agent` as type aliases. **`IntentEdge` is the exact
  in-repo precedent** — it lives in `pkg/plan` and `pkg/agent/plan_engine.go:2423` already declares
  `type IntentEdge = plan.IntentEdge` *"so callers importing pkg/agent get a single-package API"*
  `[FACT]`. This keeps `AppendCorrection`'s signature source-compatible for its existing tests.
- **Injection**: a func-value setter, `PlanCorrectTool.SetAppendCorrection(...)`, wired from
  `pkg/agent/loop.go`'s `wirePlanToolsForAgent` (`:4347-4413`) using `RegisterReplacing`, matching
  `TaskRunTool.SetStartTaskNow` (`pkg/tools/run_task.go:40-60`) and
  `PlanCreateTool.SetOwnerValidator` (`pkg/tools/plan.go:110`).
- **On failure**: **fail closed**. An unwired setter is a configuration error, never a permission
  grant — the exact discipline both precedents document verbatim.

---

## 12. BDD Scenarios

### Feature: PlanSupervisor adjudicates and corrects running plans

#### Background

- **Given** a gateway booted with PlanSupervisor seeded, locked, and granted `plan_correct` and the `plan` skill
- **And** a running plan `P` whose members have all reached a terminal state

---

#### Scenario: PlanSupervisor appends a missing step to a plan whose DoD is unmet

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** plan `P` is parked at `awaiting_supervision` because one DoD criterion has no member covering it
- **When** PlanSupervisor calls `plan_correct` with verb `append` and one tail member
- **Then** the plan's phase becomes `dispatching`
- **And** the tail member exists with status `next`
- **And** a revision entry with verb `append` is recorded
- **And** the persisted unmet-terminal signature is cleared

#### Scenario: A corrected plan reaches done with no human input

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** plan `P` has been corrected by an `append` and the tail member has completed
- **When** the plan judge evaluates the DoD
- **Then** the plan state becomes `done`
- **And** no human action was required at any point

#### Scenario: PlanSupervisor targets one failed member for retry

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** plan `P` is parked at `awaiting_supervision` with members `M1` (`failed`) and `M2` (`failed`)
- **When** PlanSupervisor calls `plan_correct` with verb `targeted_retry` naming `M1`
- **Then** `M1` has status `next`
- **And** `M2` still has status `failed`

#### Scenario: A plan whose DoD is unreachable fails honestly with a distinct message

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** plan `P` is parked at `awaiting_supervision` and no correction can make it progress
- **When** PlanSupervisor applies a correction after which `planCannotProgress` is still true
- **Then** the plan fails with reason `dod_unreachable`
- **And** the handover text states the Definition of Done is unreachable
- **But** the handover text does **not** claim the round budget was exhausted
- **And** `failed_reason` is **not** `judge_rounds_exhausted`

> *Rev 2's Then said `judge_rounds_exhausted` — a rev-1 leftover that survived the S15 enum split,
> contradicting US-1 AS4, FR-035, SC-017 and test #29 (`…HonestExitHandoverSaysUnreachable`), all of
> which say `dod_unreachable`. An implementer working scenario-first would have built the wrong
> terminal reason (r3 M3-02). §12 was swept for other surviving `judge_rounds_exhausted` Thens: the
> only remaining ones are the round-ceiling scenario and the four-cause outline's first two rows,
> where the value is correct.*

#### Scenario: PlanSupervisor diagnoses a stall rather than issuing a DoD verdict

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** a running plan whose DAG has no member in `next` or `in_progress` and whose members are **not** all terminal
- **When** the engine surfaces the stall
- **Then** PlanSupervisor is woken with a stall diagnosis request
- **And** the wake content names the stall reason
- **But** the wake does **not** ask for a Definition-of-Done verdict

#### Scenario: The parked phase and the stalled phase never co-occur

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Edge Case
**Type**: regression anchor (see §17)

- **Given** plan `P` is parked at `awaiting_supervision`
- **When** the engine's stall check runs on the next tick
- **Then** the plan's phase is still `awaiting_supervision`
- **And** no stall wake is issued

#### Scenario: PlanSupervisor can actually correct a stalled plan

**Traces to**: User Story 1, Acceptance Scenario 5b
**Category**: Happy Path

- **Given** plan `P` is at `plan_phase = stalled` with a stall note, and PlanSupervisor has been woken with the stall diagnosis
- **When** PlanSupervisor calls `plan_correct` with verb `append` and the tail member that unblocks the DAG
- **Then** the correction is **applied**, not rejected on the phase
- **And** `P`'s phase becomes `dispatching`
- **And** `P`'s stall note is cleared
- **And** a revision entry with verb `append` is recorded

> *This scenario is the whole of D-01. Rev 2 routed the stall wake to PlanSupervisor (FR-012,
> `:1254`) while `AppendCorrection`'s phase gate read `if p.EffectivePlanPhase() !=
> plan.PhaseAwaitingOwnerCorrection { return … }` (`plan_engine.go:2591-2593`) and
> `surfaceStallIfAny` set `plan.PhaseStalled` (`:1248`) — two phases the spec's own
> "never co-occur" scenario and test #59 pin as **disjoint**. So every `plan_correct` a stall wake
> provoked was rejected by E1's phase-mismatch error, 100% of the time, while the rubric told the
> agent to "correct the structure" and "return exactly one `plan_correct` tool call" (r3 C3-01).*

#### Scenario: A stalled plan's supervision deadline is armed, counted and bounded

**Traces to**: User Story 1, Acceptance Scenario 5c
**Category**: Error Path

- **Given** plan `P` at `plan_phase = stalled` with a supervision wake delivered and `supervision.wake_at` stamped
- **When** the supervision deadline elapses and the engine ticks
- **Then** `supervision.attempts` has incremented
- **And** a further supervision wake is delivered
- **And** on exhausting `supervision_max_attempts`, `P` transitions to `failed(supervision_unavailable)`
- **But** `P` is **not** left bounded only by the 7-day idle-expiry budget

> *Rev 2's FR-021/022/023 predicates were all keyed on `phase == awaiting_supervision`, so a stall
> wake armed no deadline, incremented no `attempts` and hit no ceiling. FR-029 restates them over
> the supervision-eligible phase set.*

#### Scenario: A stale stall note does not follow a plan into the parked phase

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Edge Case

- **Given** plan `P` carried a stall note and its members have since all reached a terminal state
- **When** the plan judge returns UNMET and `P` enters `awaiting_supervision`
- **Then** `P`'s handover text carries **no** stall note
- **And** the supervision wake content asks for a Definition-of-Done correction, not a stall diagnosis

---

#### Scenario: Only PlanSupervisor may apply a correction

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** plan `P` is parked at `awaiting_supervision`
- **When** PlanSupervisor calls `plan_correct` with a valid `append`
- **Then** the correction is applied

#### Scenario Outline: Every non-PlanSupervisor principal is denied correction

**Traces to**: User Story 2, Acceptance Scenarios 2 and 3
**Category**: Error Path

- **Given** plan `P` is parked at `awaiting_supervision`
- **When** `<caller>` calls `plan_correct` with an otherwise-valid `append`
- **Then** the call is denied
- **And** no revision entry is recorded
- **And** the plan's phase is unchanged

**Examples**:

| caller | why it must be denied |
|---|---|
| the plan's own `owner_agent_id` | the Owner never adjudicates (FR-011) |
| a seeded core agent (`jim`) | its seeded policy denies `plan_correct`, and the gate denies it regardless |
| the Judge (another System Agent) | authority is matched on identity, not on `Type == system` |
| a **user-created** agent with no `plan_correct` policy entry | the policy layer resolves `allow` from the global ceiling — the gate is the only control (N2) |
| a caller with an empty agent identity | no identity is a non-owner |

#### Scenario: Denials are indistinguishable and leak no plan state

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path

- **Given** an unauthorised caller
- **When** it calls `plan_correct` against a parked plan, a running-but-unparked plan, and a plan id that does not exist
- **Then** all three responses carry the identical error class and the identical message body
- **And** the message body contains no plan id, no owner agent id and no phase
- **And** in particular the nonexistent-plan case does **not** return a store error, which would be an existence oracle

#### Scenario: An authorised caller naming a plan that does not exist gets a real not-found error

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path

- **Given** PlanSupervisor — the one principal authorised to correct
- **When** it calls `plan_correct` naming a plan id that does not exist
- **Then** it receives the real not-found error, distinguishable from an authority denial
- **And** it can therefore tell "I named the wrong plan" from "I am not permitted"

#### Scenario: A user cannot open a chat session against a System Agent

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Error Path

- **Given** an authenticated user
- **When** they request a new session with `agent_id: "plansupervisor"`
- **Then** the request is rejected with 400
- **And** no session is created

#### Scenario Outline: System agents are rejected on every chat-target surface

**Traces to**: User Story 2, Acceptance Scenarios 5 and 6
**Category**: Error Path

- **Given** an authenticated user and a System Agent id
- **When** they attempt `<surface>`
- **Then** the request is rejected with 400

**Examples**:

| surface |
|---|
| `POST /api/v1/sessions` with an explicit system `agent_id` |
| a WS chat frame with an explicit system `agent_id` |
| `PUT /api/v1/agents/{id}` with `default: true` |

#### Scenario: The supervision wake still reaches PlanSupervisor after the chat-target guards land

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the chat-target guards reject System Agents at the session-create and WS surfaces
- **When** a plan's DoD is judged UNMET
- **Then** PlanSupervisor is still woken over the bus
- **And** the Judge's verifier session can still be minted

#### Scenario: The id `plansupervisor` cannot be claimed by any principal

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Error Path

- **Given** an authenticated user, and separately an agent holding `create_agent`
- **When** either attempts to create an agent whose id is `plansupervisor`, or to rename an existing agent to it
- **Then** the attempt does not succeed
- **And** the resulting agent's id is a server-minted UUID, never the requested string
- **And** the exact-identity gate in `requireOwner` therefore rests on a real control, not on nobody choosing the name

#### Scenario: PlanSupervisor's tool grant is the allow-set and nothing else

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Edge Case

- **Given** PlanSupervisor's resolved effective tool policy on a fresh install
- **When** every name in `allStaticToolNames` is resolved through `ResolveEffectivePolicy`
- **Then** exactly one name resolves `allow` — `plan_correct`
- **And** every other name in the catalog resolves `deny`
- **And** `stop_plan` in particular resolves `deny`, because the adjudicator corrects and the owner contains (FR-043)
- **And** adding a new tool to the catalog later cannot silently land in PlanSupervisor's allow set

#### Scenario: No agent can start a plan it cannot stop

**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path
**Type**: the C3-02 regression — operator ruling 6

- **Given** a fresh install
- **When** every seeded agent's **resolved** policy for `execute_plan` and for `stop_plan` is computed through `ResolveEffectivePolicy`
- **Then** for every agent whose `execute_plan` resolves to something other than `deny`, `stop_plan` also resolves to something other than `deny`
- **And** `jim` — the agent seeded `allow` for plan execution — resolves `stop_plan` to `allow`, so Dataset B11 is reachable
- **And** the property holds as a **complement over the roster**, not as a list of agent ids, so a newly seeded agent granted `execute_plan` cannot ship unable to stop what it starts

> *Rev 2 added `stop_plan` to the catalog surfaces with an `allow` global ceiling and named it
> in **no** seed override map. `denyAllThenOverride` stamps an explicit `deny` for every catalog name
> at all nine of its call sites, and a per-agent `deny` beats a global `allow` under strictest-wins —
> so on a fresh install Jim held `create_plan: allow`, `execute_plan: allow` and `stop_plan: deny`,
> making US-8 AS2, FR-042, FR-043, test #64, Dataset F1/F2/F5/F6/F8 and half of SC-020 dead on
> arrival. Dataset D covered `plan_correct` only, so nothing detected it (r3 C3-02, cross-spec C2a).*

#### Scenario: The correction counter survives a park → dispatch → park cycle

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case
**Type**: the C3-03 regression

- **Given** plan `P` parked at `awaiting_supervision` with `supervision.correction_rounds = 0`
- **When** a correction is applied — returning `P` to `dispatching` — and `P` later re-parks at `awaiting_supervision`
- **Then** `supervision.correction_rounds` is **1**, not 0
- **And** `supervision.wake_at`, `supervision.wake_error` and `supervision.attempts` **were** reset by the transition
- **And** on the plan's terminal record, `correction_rounds` still reads the total number of corrections applied over the plan's whole life

> *FR-050's rev-2 closing sentence — "Every field MUST be cleared or reset when the plan leaves
> `awaiting_supervision`" — reset `correction_rounds` on **every** applied correction, since an
> applied correction is exactly what returns the plan to `dispatching`. It therefore read **0** on
> every terminal record, and FR-035's cause-1-vs-cause-2 distinguisher (`== 0` vs `> 0`) would have
> reported "the round budget ran out with no correction ever applied" for a plan that burned its
> whole budget on corrections. Six readers depended on it. This is r2's C-02 reopened by its own fix
> (r3 C3-03).*

---

#### Scenario: A supersede with no replacement work is rejected

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Error Path

- **Given** plan `P` is parked at `awaiting_supervision` with a `done` member `M`
- **When** PlanSupervisor calls `plan_correct` with verb `supersede` naming `M` and an empty `tail_members`
- **Then** the correction is rejected before any mutation
- **And** no revision entry is recorded
- **And** `M` is not marked superseded

#### Scenario: A supersede paired with replacement work is applied

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Happy Path

- **Given** plan `P` is parked at `awaiting_supervision` with a `done` member `M` whose outcome is wrong
- **And** `M` carries acceptance criteria `C`
- **When** PlanSupervisor calls `plan_correct` with verb `supersede` naming `M` and one tail member `R` that carries `C`
- **Then** the correction is applied
- **And** `M` is marked superseded
- **And** `R` exists with status `next`
- **And** the revision entry records both the supersession and the tail add
- **And** every other live-round `failed` member in `P` has been auto-reset to `next`, while `frozen` and `done` members are untouched

#### Scenario: A supersede whose replacement drops the failing criteria is rejected

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** plan `P` is parked at `awaiting_supervision` with a `done` member `M` carrying acceptance criteria `C`
- **When** PlanSupervisor calls `plan_correct` with verb `supersede` naming `M` and one trivial tail member carrying none of `C`
- **Then** the correction is rejected before any mutation
- **And** `M` is not marked superseded
- **And** the rejection names the criteria the replacement must inherit

#### Scenario: A plan cannot reach done by discounting evidence, bare or paired

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Edge Case

- **Given** plan `P` whose only defect is one DoD criterion unmet by member `M`'s outcome
- **When** correction attempts are a bare `supersede` of `M`, and then a `supersede` of `M` paired with a tail member that carries none of `M`'s criteria
- **Then** every attempt is rejected
- **And** the plan never reaches state `done`
- **And** the criterion is still unmet when the plan terminates

#### Scenario: A supersede is distinguishable in the audit trail

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Happy Path

- **Given** a plan with one applied `append` and one applied `supersede`
- **When** the revision history is read
- **Then** each entry's verb is machine-readable without parsing free text

#### Scenario: A targeted_retry of a superseded member remains impossible

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case
**Type**: conformance anchor (see §17) — pins the status fork that makes ADR-055 D16's pairing impossible

- **Given** a member `M` that has been superseded and whose status is `done`
- **When** PlanSupervisor calls `plan_correct` with verb `targeted_retry` naming `M`
- **Then** the correction is rejected because `M` is not `failed`

---

#### Scenario Outline: The `plan_correct` payload is validated before any mutation

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Error Path

- **Given** plan `P` is parked at `awaiting_supervision`
- **When** PlanSupervisor calls `plan_correct` with `<payload defect>`
- **Then** the correction is rejected before any mutation
- **And** no task is created, no edge is wired and no revision entry is recorded

**Examples**:

| payload defect |
|---|
| `tail_edges` describing a cycle among the tail members |
| `tail_edges` naming a `to_task_id` that does not exist in the plan |
| `tail_edges` naming a member that has been superseded |
| `tail_members` on a `targeted_retry` |
| `tail_members` longer than the configured cap |
| `tail_edges` longer than the configured cap |

#### Scenario: Member ids are minted by the engine, never by the caller

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case

- **Given** the `plan_correct` tool's parameter schema
- **When** its `tail_members` items are enumerated
- **Then** no item carries a caller-supplied member id
- **And** applying the correction twice through intent-log replay still creates each member exactly once

#### Scenario: An un-correctable plan exits honestly through `abandon`

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** plan `P` is parked at `awaiting_supervision` and no verb has a legal target
- **When** PlanSupervisor calls `plan_correct` with verb `abandon` and a falsified assumption
- **Then** `P` transitions to `failed` with reason `dod_unreachable`
- **And** the handover text names the falsified assumption
- **And** no member's status changed
- **And** a revision entry with verb `abandon` is recorded

#### Scenario: A rejected correction re-arms the supervision wake instead of stranding the plan

**Traces to**: User Story 1, Acceptance Scenario 7
**Category**: Error Path

- **Given** plan `P` is parked at `awaiting_supervision` with one supervision wake already delivered
- **When** PlanSupervisor's turn emits a correction that validation rejects
- **Then** no mutation occurred and `judge_rounds` is unchanged
- **And** on the first tick after the supervision deadline elapses, a second supervision wake is delivered
- **And** `P`'s recorded supervision attempt count is 2

#### Scenario: A supervision turn that emits nothing at all is detected by the deadline

**Traces to**: User Story 1, Acceptance Scenario 7
**Category**: Error Path

- **Given** plan `P` is parked at `awaiting_supervision` with a supervision wake delivered and no response
- **When** the supervision deadline elapses and the engine ticks
- **Then** `P` is still at `awaiting_supervision` with its unmet signature unchanged
- **And** the supervision attempt count has incremented
- **And** a further supervision wake is delivered

#### Scenario: Exhausting the supervision attempt ceiling terminates the plan and tells the Owner

**Traces to**: User Story 1, Acceptance Scenario 7
**Category**: Error Path

- **Given** plan `P` parked at `awaiting_supervision` whose supervision attempts have reached the ceiling
- **When** the deadline elapses once more
- **Then** `P` transitions to `failed` with reason `supervision_unavailable`
- **And** `P`'s `owner_agent_id` is woken with a handover naming adjudication as unavailable
- **And** the handover is distinguishable from both `judge_rounds_exhausted` messages and from `dod_unreachable`

---

#### Scenario Outline: Every plan's outcome reaches its responsible agent over the bus

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** plan `P` with `owner_agent_id` set to a registered chat-target agent
- **When** the plan reaches terminal state `<terminal path>`
- **Then** that agent is woken over the bus with the handover text
- **And** PlanSupervisor is **not** the recipient of that wake

**Examples**:

| terminal path | wake site |
|---|---|
| `done` (DoD MET → closing synthesis) | `synthesizeAndComplete` (`:1571`) — **the success path, explicitly covered** |
| `failed(judge_rounds_exhausted)` | `failPlanLocked` (`:1610`) |
| `failed(dod_unreachable)` | `failPlanLocked` (`:1610`) |
| `failed(supervision_unavailable)` | `failPlanLocked` (`:1610`) |
| `failed(idle_expired)` | `failPlanLocked` (`:1610`) |
| `failed(stopped_by_user)` | `StopPlan` (`:1742`) |

#### Scenario: A plan that reaches `done` notifies its owner agent and its human author

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** plan `P` created through the UI by user `alice`, whose DoD the plan judge returns MET
- **When** `synthesizeAndComplete` runs
- **Then** `P`'s `owner_agent_id` is woken with the closing-synthesis commission
- **And** a notification is persisted for `alice` carrying the plan id
- **And** the recipient of the synthesis commission is the Owner, **not** PlanSupervisor

#### Scenario: A human who created a plan through the UI is additionally notified

**Traces to**: User Story 4, Acceptance Scenarios 2 and 4
**Category**: Happy Path

- **Given** plan `P` created through the UI by user `alice`, who has an open WS connection
- **When** the plan reaches a terminal state
- **Then** a notification is persisted for `alice`
- **And** her WS connection receives the notification frame
- **And** the notification carries the plan id

#### Scenario: An agent-created plan writes no unreadable notification file

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Edge Case

- **Given** plan `P` created by agent `jim` through `create_plan`, so `owner` holds an agent id
- **When** the plan reaches a terminal state
- **Then** the bus wake to `owner_agent_id` is delivered
- **But** no notification file is written keyed on an agent id

#### Scenario: A notification failure never means nobody was told

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Error Path

- **Given** plan `P` created by user `alice` and a notification store that returns an error
- **When** the plan reaches a terminal state
- **Then** the bus wake to `owner_agent_id` still succeeded
- **And** the notification failure is logged at ERROR
- **And** the plan's terminal state is unchanged

#### Scenario: PlanSupervisor unavailable below the ceiling leaves the plan parked and tells nobody yet

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Error Path

- **Given** PlanSupervisor's provider returns an error for every turn, and `supervision.attempts` is below `supervision_max_attempts`
- **When** the supervision deadline elapses
- **Then** the plan stays at `awaiting_supervision`
- **And** the supervision wake is re-issued and `supervision.attempts` increments
- **But** `P`'s `owner_agent_id` is **not** woken — there is nothing to tell it yet
- **And** no other agent inherits adjudication

#### Scenario: PlanSupervisor unavailable at the ceiling terminates the plan and tells the responsible agent

**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Error Path

- **Given** PlanSupervisor's provider returns an error for every turn, and `supervision.attempts` has reached `supervision_max_attempts`
- **When** the deadline elapses once more
- **Then** the plan transitions to `failed(supervision_unavailable)`
- **And** `P`'s `owner_agent_id` is woken with an adjudication-unavailable handover
- **But** no other agent inherits adjudication

> *Rev 2 had one scenario asserting the plan **stays parked** **and** the Owner is woken. FR-019 and
> FR-022(b) wake the Owner **only at the ceiling**, at which point the plan is no longer parked —
> there is no specified state in which both hold, and test #36 (which drives to the ceiling) could
> not satisfy the scenario it traced to (r3 M3-03).*

---

#### Scenario: A parked plan whose deadline has elapsed is re-woken after a restart

**Traces to**: User Story 5, Acceptance Scenarios 1 and 2
**Category**: Happy Path

- **Given** plan `P` persisted at `awaiting_supervision` with an unchanged unmet-terminal signature
- **And** `P`'s `supervision.wake_at` is set to a time **more than `supervision_turn_timeout` ago**
- **When** the gateway restarts and boot reconciliation runs
- **Then** PlanSupervisor is woken on the first tick after boot
- **And** **at most one** supervision wake is issued for `P` per restart
- **And** `P`'s `judge_rounds` is unchanged
- **And** `P`'s persisted unmet-terminal signature is unchanged

#### Scenario: A restart inside the supervision deadline issues no wake at all

**Traces to**: User Story 5, Acceptance Scenario 1b
**Category**: Edge Case

- **Given** plan `P` persisted at `awaiting_supervision`
- **And** `P`'s `supervision.wake_at` is set to a time **less than `supervision_turn_timeout` ago**
- **When** the gateway restarts and boot reconciliation runs
- **Then** **zero** supervision wakes are issued
- **And** the deadline is honoured from its **original** stamp, not re-armed from the boot time
- **And** `P` is woken when that original deadline elapses, so a restart loop cannot reset the ceiling

> *Rev 2 asserted "woken exactly once when the gateway restarts" while FR-023 dedups on `wake_at`
> and E12 requires the deadline be honoured from its original stamp — so a restart inside the window
> produces **zero** wakes, and the scenario was untestable because it never stated `wake_at`'s
> pre-restart value (r3 M3-12). The precondition is now stated and the two cases are separate
> scenarios.*

#### Scenario: A parked plan at its round ceiling terminates instead of being re-woken

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Edge Case

- **Given** plan `P` persisted at `awaiting_supervision` with `judge_rounds` equal to `plan_judge_max_rounds`
- **When** the gateway restarts
- **Then** `P` transitions to `failed` with reason `judge_rounds_exhausted`
- **And** no supervision wake is issued

#### Scenario: Starting the engine without a notifier is a startup error

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Error Path

- **Given** a plan engine constructed with a nil notifier
- **When** `Start` is called
- **Then** `Start` returns an error naming the missing notifier
- **But** constructing the struct directly in an in-package test still succeeds

#### Scenario: A failed wake publish is recorded and retried

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Error Path

- **Given** a notifier whose `Notify` returns an error
- **When** plan `P`'s DoD is judged UNMET
- **Then** the wake failure is recorded on the plan and surfaced
- **And** the wake is re-attempted on a later tick
- **But** the plan's phase transition to `awaiting_supervision` still persisted

#### Scenario: Repeated ticks do not produce repeated supervision wakes

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Edge Case

- **Given** plan `P` parked at `awaiting_supervision` with a successful wake already delivered and `supervision.wake_at` recorded
- **When** the engine ticks ten more times **within the supervision deadline** and no member state changes
- **Then** exactly one supervision wake has been delivered in total
- **And** the dedup is keyed on `supervision.wake_at`, **not** on the unmet-terminal signature — which carries no wake information and would fire on every tick

---

#### Scenario: An operator reads why a plan changed

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** plan `P` with three applied corrections, one of each verb
- **When** an operator reads the plan's revision history
- **Then** all three entries appear with verb, target member id, falsified assumption and timestamp

#### Scenario: The revision history survives a restart

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path

- **Given** plan `P` with applied corrections
- **When** the gateway restarts and the history is read again
- **Then** the history is byte-identical to the pre-restart read
- **And** the in-memory superseded-member set is repopulated

#### Scenario: A rejected correction leaves no audit trace

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Error Path

- **Given** plan `P` with no applied corrections
- **When** a correction is rejected by validation
- **Then** the plan's revision history is empty

#### Scenario: A corrupt intent log is surfaced, not swallowed

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Error Path

- **Given** plan `P` whose intent-log JSONL has been truncated mid-record
- **When** the gateway boots and correction reconstruction runs
- **Then** an ERROR names `P` and the unreadable log
- **And** `P` is failed closed rather than resumed with an incomplete superseded set
- **And** no previously-superseded member has silently become un-superseded

#### Scenario: Every correction produces an audit event

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** plan `P` parked at `awaiting_supervision`
- **When** PlanSupervisor applies a correction
- **Then** a `plan.correct` audit entry is recorded naming PlanSupervisor as the actor, the plan id and the verb
- **And** the entry is readable by an operator without reading JSONL by hand

---

#### Scenario: A plan parks under the new phase name end to end

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a freshly created `$OMNIPUS_HOME` and a running plan whose DoD the judge returns UNMET
- **When** the plan parks
- **Then** the record on disk holds `plan_phase: "awaiting_supervision"`
- **And** the plan can be read, stopped and corrected
- **And** the SPA plans list renders it without an `ApiSchemaError`

#### Scenario: The renamed ownership scope still exempts a paused session from the sweep

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a paused `needs_input` session whose lifecycle record carries a populated `ScopeKind`
- **When** the gateway boots and the sweep runs
- **Then** the session is preserved, not marked `failed(interrupted)`
- **And** a record whose `ScopeKind` is empty is still swept — the gate's behaviour is unchanged by the rename

#### Scenario: The rename leaves no occurrence the compiler cannot see

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the rename has landed
- **When** SC-011's mechanical `rg` command runs over the repo
- **Then** it returns zero hits
- **And** the directories covered include `src/**`, `pkg/skills/embedded/**`, `pkg/gateway/inboundschemas/**` and `tests/e2e/**`

#### Scenario: PlanSupervisor's own prompt contains no retired vocabulary and no contradicted rule

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the embedded `plan` skill after the rename
- **When** its bytes are inspected
- **Then** they contain no occurrence of `awaiting_owner_correction`
- **And** they contain no instruction forbidding a dedicated supervising agent
- **And** the verb table contains no occurrence of "Optionally append a replacement"
- **And** the verb table states that a `supersede` must be accompanied by replacement work inheriting the superseded member's criteria

#### Scenario: The SPA parked and stalled copy names a control the user actually has

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the revised `planStateColors.ts` copy
- **When** a parked plan's chip explanation is read
- **Then** it says a supervisor is reviewing the plan and that Stop remains available
- **And** the stalled plan's explanation is its own distinct wording, not a copy of the parked one
- **And** neither string contains "no in-app action"

#### Scenario: The contract artifacts are regenerated in the same change

**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the renamed phase value in `contracts/`
- **When** `make verify-contracts` runs
- **Then** it exits zero with no drift

---

#### Scenario: Stopping a plan halts the supervisor working on it

**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** plan `P` parked at `awaiting_supervision` with a supervision turn **actually running and blocked** in session `S = supervision.session_id`
- **When** `P` is stopped
- **Then** **that turn terminates** — its context observes cancellation and the turn returns — within a bounded time
- **And** the cancel was **claimed**, i.e. a live turn whose `transcriptSessionID` equals `S` was found and cancelled, not a cancel request that matched nothing
- **And** `P` transitions to `failed` with reason `stopped_by_user`
- **And** a `plan_correct` arriving after the stop is rejected because `P` is no longer running
- **But** this scenario is **not** satisfied by a canceller double that records `S` and returns success

> *Operator ruling 5: "it needs to test it stops." Rev 2's Then was "`S` is cancelled in the same
> fan-out", which `plan_stop_test.go`'s `fakeSessionCanceller` (`:28-38`) satisfies by recording the
> string and returning `(true, nil)` — against a control that, per N13, cancels nothing in
> production. `cancelSessions` discards the `fired` bool (`plan_engine.go:1825`), so even the real
> canceller cannot tell a claimed cancel from a no-op. The assertion moves to the outcome.*

#### Scenario: The supervision wake actually starts a turn

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path
**Type**: regression anchor (see §17) — pins N15

- **Given** plan `P` enters `awaiting_supervision` and the supervision wake is issued
- **When** the delivery completes
- **Then** an agent turn has run for `plansupervisor`
- **And** that turn's `transcriptSessionID` equals `P`'s `supervision.session_id`
- **And** that session resolves to a real store and its transcript is non-empty
- **And** **zero** outbound messages have been published to any channel — the adjudicator's
  deliberation does not reach `P`'s originating conversation (FR-016, H8)
- **But** the assertion is **not** satisfied by observing that the notifier's `Notify` was called
- **Nor** by observing that `supervision.wake_at` was written

> *N15: today every plan wake is discarded by `processSystemMessage`'s internal-channel guard
> (`loop.go:6022-6031`) before any turn runs, because `wakeOwner` hardcodes `Channel: "system"` and
> the bus `ChatID` is composed as `"<channel>:<chatID>"`. A fake notifier records the call three
> hops upstream of that drop, so every existing wake test passes against a delivery that delivers
> nothing.*
>
> *Rev 4 (D-11): the zero-outbound step is why the supervision family dispatches **directly**
> (FR-012c(A)) rather than over the bus. `processSystemMessage` hardcodes `SendResponse: true`
> (`loop.go:6141`), so any origin channel it is handed receives the supervision turn's output.*

#### Scenario: An Owner wake reaches a turn and the conversation the plan came from

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path
**Type**: regression anchor (see §17) — pins N15, FR-012c(B), FR-016c

- **Given** plan `P` was created by `create_plan` from a turn on channel `telegram`, chat `C`
- **And** `P` therefore carries `source_channel = "telegram"` and `source_chat_id = C`
- **When** `P` reaches a terminal state and the Owner wake is issued
- **Then** an agent turn has run for `P.owner_agent_id`
- **And** that turn's `transcriptSessionID` equals `P.owner_session_id`
- **And** `ResolveSessionStore(P.owner_session_id)` is non-nil and its transcript is non-empty
- **And** an outbound message carrying the closing synthesis has been published to `telegram` / `C`
- **And** a `cli` system message and a `subagent` system message, delivered in the same test, start
  **no** turn — the internal-channel guard is not widened (FR-012c(4))

> *Two independent defects would each break this today. N15 drops the wake before any turn runs.
> N13 means `P.owner_session_id` is either empty (the common case — `ensureOwnerSessionLocked` has
> exactly one caller, on the UNMET path) or the string `"plan:<id>"`, which `ResolveSessionStore`
> does not resolve — so even a delivered wake would run a turn whose output is persisted nowhere.
> FR-012d fixes the first, FR-016c the second.*

#### Scenario: A plan created in the UI still reaches its owner

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Edge Case
**Type**: regression anchor (see §17) — pins FR-012d(4) / D-10, the no-origin fallback

- **Given** plan `Q` was created through `POST /api/v1/plans` from the Plans UI
- **And** `Q` therefore carries neither `source_channel` nor `source_chat_id` — a legitimate state,
  not a degraded one (operator ruling 10)
- **When** `Q` parks and the supervision wake is issued
- **Then** an agent turn has run for `plansupervisor`, exactly as for a plan with a chat origin —
  a missing origin never affects a decision wake (FR-012c(A))
- **When** `Q` reaches a terminal state and the Owner wake is issued
- **Then** an agent turn has still run for `Q.owner_agent_id`
- **And** its closing synthesis is persisted to `Q.owner_session_id`, which resolves to a real store
- **And** **zero** outbound messages have been published — to any channel, and specifically not to
  the owner agent's last-active channel nor to any other session belonging to `Q`'s creator
- **And** a `plan_completed` (or `plan_failed`) notification carrying `plan_id = Q.id` exists for
  the creator
- **And** an INFO log records `{plan_id, owner_agent_id, reason: "no_chat_origin"}`
- **But** `Q.supervision.wake_error` is **not** set, the supervision attempt count is **not**
  incremented, and `Q.failed_reason` is **not** `supervision_unavailable` — nothing failed

> *D-10 / operator ruling 10: "no chat to deliver to" MUST NOT collapse into "no turn ran", which is
> today's defect wearing a decision's clothes. The rejected alternative — falling back to the owner
> agent's default route or last-active channel — is what the zero-outbound step exists to forbid: it
> would drop a plan's outcome into an unrelated conversation, the same cross-context leak class H8
> prevents.*
>
> *The final step is **N16**, and it is the one a naive fix fails. Passing `Q`'s empty origin to
> `Notify` trips FR-N7's empty-destination rejection (`async_notifier.go:226-233`), which FR-024
> records as a `wake_error` and retries until FR-022's ceiling terminates `Q`
> `failed(supervision_unavailable)` — a healthy plan loudly and falsely diagnosed as having no
> supervisor. FR-012d(4) makes that unreachable by never constructing the wake.*

#### Scenario: A supersede whose replacement carries only some of the failing criteria is rejected

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** plan `P` parked at `awaiting_supervision` with a `done` member `M` carrying acceptance criteria `{C1, C2, C3}`
- **When** PlanSupervisor calls `plan_correct` with verb `supersede` naming `M` and tail members whose criteria collectively are `{C1}`
- **Then** the correction is rejected before any mutation
- **And** the rejection names `C2` and `C3` as the criteria the replacement must carry
- **And** `M` is not marked superseded

> *FR-030b's rev-2 predicate rejected only a replacement carrying **none** of the criteria, so
> carrying 1-of-N passed — the adjudicator could supersede a member failing `C3` and attach a
> replacement carrying only `C1`, which is precisely the bypass FR-030b exists to close, at the same
> one-throwaway-member price (r3 M3-05). The predicate is now "every criterion".*

#### Scenario: A supersede of a member with no acceptance criteria is allowed

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case

- **Given** plan `P` parked at `awaiting_supervision` with a `done` member `M` carrying **zero** acceptance criteria
- **When** PlanSupervisor calls `plan_correct` with verb `supersede` naming `M` and one tail member
- **Then** the correction is applied — FR-030b's "carries every criterion of `M`" is vacuously satisfied over an empty set
- **And** FR-030's `len(tail_members) > 0` rule still applies, so a **bare** supersede of `M` is still rejected

> *Rev 2's "carries none of `[]`" was vacuously true, leaving the case undefined in both
> directions — an implementer could reasonably have made superseding a criteria-less member either
> always-reject or always-pass (r3 M3-05). The answer is stated.*

#### Scenario: A plan's owner agent can stop the plan it started

**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path

- **Given** agent `jim` is the `owner_agent_id` of running plan `P`, started from a chat
- **When** `jim` calls `stop_plan` naming `P`
- **Then** `P` transitions to `failed` with reason `stopped_by_user`
- **And** the handover text records `jim` as the actor
- **And** every member session, verifier session, the owner session and the supervision session are cancelled

#### Scenario: An agent cannot stop a plan it does not own

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Error Path

- **Given** agent `mycustomagent`, which is not the `owner_agent_id` of running plan `P`
- **When** it calls `stop_plan` naming `P`, and separately naming a plan id that does not exist
- **Then** both calls are denied
- **And** the two responses carry the identical error class and message body
- **And** `P` is still running

#### Scenario: A correction and a stop are serialised

**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Edge Case
**Type**: regression anchor (see §17)

- **Given** plan `P` parked at `awaiting_supervision`
- **When** a correction and a stop are issued concurrently
- **Then** exactly one of them takes effect first and the other observes the resulting state
- **And** the plan is never left in a partially-corrected state

#### Scenario: Every applied correction emits a structured log line

**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Happy Path

- **Given** plan `P` parked at `awaiting_supervision`
- **When** PlanSupervisor applies a correction
- **Then** a structured log line records plan id, verb, target member id and outcome

#### Scenario: A long-parked plan is still reaped by idle expiry

**Traces to**: User Story 8, Acceptance Scenario 6
**Category**: Edge Case

- **Given** plan `P` parked at `awaiting_supervision` past its idle-expiry budget
- **When** the idle sweeper runs
- **Then** the plan transitions to `failed` with reason `idle_expired`
- **And** supervision has created no immortal record

#### Scenario: An operator can see how many plans are awaiting supervision

**Traces to**: User Story 8, Acceptance Scenario 7
**Category**: Happy Path

- **Given** three plans parked at `awaiting_supervision` and two running normally
- **When** an operator reads the supervision gauge
- **Then** it reports 3

---

#### Scenario Outline: The four terminal supervision causes are distinguishable

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Error Path

- **Given** plan `P` terminating for `<cause>`
- **When** the plan record and the handover text are read
- **Then** `failed_reason` is `<reason>` and the handover states `<meaning>`
- **And** it is distinguishable from all three other causes

**Examples**:

| cause | reason | meaning | how it is told apart |
|---|---|---|---|
| the judge round ceiling was reached with no correction ever applied | `judge_rounds_exhausted` | the round budget ran out | `supervision.correction_rounds == 0` |
| corrections consumed the shared round budget | `judge_rounds_exhausted` | corrections used up the shared budget | `supervision.correction_rounds > 0` |
| a correction left the plan unable to progress, or PlanSupervisor abandoned it | `dod_unreachable` | the Definition of Done is unreachable | its own enum value |
| the supervision attempt ceiling was exhausted | `supervision_unavailable` | adjudication never produced a usable correction | its own enum value |

> **Why this is now four causes across three enum values, not three strings on one.** Causes 1 and 2
> are *the same predicate at the same line* (`plan_engine.go:1288-1291`), and
> `buildPlanRoundsExhaustedHandover`'s entire input is `(p, maxRounds)` — so without
> `supervision.correction_rounds` they are not merely hard to tell apart, they are the same event.
> Causes 3 and 4 are genuinely different terminal conditions and get their own `failed_reason`
> values, which makes them **machine**-distinguishable rather than string-distinguishable. Cause 3's
> message already exists — `buildUnreachableDoDHandover` (`plan_engine.go:2892`, wired at `:2680`)
> `[FACT — verified]` — so that limb is a rewire, not new text.

#### Scenario: A correction does not consume a judge round but does advance the correction counter

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Edge Case
**Type**: regression anchor (see §17) — pins the sole-incrementer invariant at `plan_engine.go:1495`

- **Given** plan `P` at `judge_rounds = 3` and `supervision.correction_rounds = 0`
- **When** PlanSupervisor applies one correction
- **Then** `judge_rounds` is still 3
- **And** `supervision.correction_rounds` is 1
- **And** after the correction's members complete and the plan is re-judged, `judge_rounds` is 4

#### Scenario: The correction request cannot carry a DoD or an owner reassignment

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case
**Type**: conformance anchor (see §17) — the structural half of NFR-2, replacing the retired byte-identity tautology

- **Given** the correction request type and the `plan_correct` tool's parameter schema
- **When** their fields are enumerated
- **Then** neither has a field that can set a Definition of Done
- **And** neither has a field that can set an owner agent id
- **And** neither has a field that can set the plan's bounds

---

## 13. Test-Driven Development Plan

### 13.1 Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| **Unit** | One function in `pkg/plan`, `pkg/coreagent`, `pkg/config`, `pkg/tools`, `pkg/session` | Validates policy resolution, seed shape, correction-payload validation and prompt-byte invariants in isolation |
| **Integration** | `pkg/agent` engine + real stores against a temp `$OMNIPUS_HOME`; `pkg/gateway` handlers | Validates the correction loop, authority gate, wake routing, delivery fork and boot behaviour end to end inside the binary |
| **Contract** | `make verify-contracts`, `pkg/api/generated/contract_test.go`, SPA zod round-trips | Validates that every wire change is generated, committed and schema-valid |
| **E2E** | Playwright against the embedded SPA + a real gateway | Validates the SPA copy change and that no schema error is thrown for a migrated plan |

> **Build discipline (CLAUDE.md).** Every Go invocation MUST carry `-tags goolm,stdjson`. **Never run
> the full suite locally** (OOM). Use `CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestName$' -p 1 ./pkg/...`
> for a single scoped test, and push to CI / the `ci-omnipus` worker for the full gates.

### 13.2 Test Implementation Order

Write these BEFORE the implementation code. Order is dependency-first: catalog and policy plumbing
before the seed; the seed before the gate; the gate before the loop.

| # | Test name | Level | Traces to BDD scenario | Verifies |
|---|---|---|---|---|
| 1 | `TestAllStaticToolNames_IncludesPlanCorrect` | Unit | *The correction request cannot carry a DoD…* (setup) | `plan_correct` is in `allStaticToolNames`, so `validateOverrideKeys` cannot panic |
| 2 | `TestToolCatalogDrift_NewToolsInEveryCatalogSurface` | Unit | — (guards #1) | `allStaticToolNames`, `defaults.go`'s `ToolPolicies` (+ `defaults_test.go`'s `wantToolCount`) and the builtin metadata registry all agree for **both** `plan_correct` and `stop_plan`; `buildKnownBuiltinToolNames` **derives** from the metadata and is asserted to contain both without being hand-edited. **Asserts `len(allStaticToolNames) == len(cfg.Sandbox.ToolPolicies)`** so the catalog size is never hand-quoted again. *(Renamed in rev 3: there are **three** literals plus one derived set, not four — cross-spec m3. Rev 2's name asserted a fact about the tree that is false.)* |
| 3 | `TestGlobalCeiling_NewToolsAreAllow` | Unit | *Only PlanSupervisor may apply a correction*; *No agent can start a plan it cannot stop* | The ceiling for `plan_correct` is `allow`, not `deny`/`ask` — a stricter ceiling would overrule PlanSupervisor's grant (N1). **`stop_plan`'s ceiling is `allow` for a second, independent reason (N14):** `execute_plan`'s ceiling is `ask`, and if `stop_plan` mirrored it, Jim's own seeded `allow` would merge down to `ask` and Dataset B11 would be unreachable. The test asserts the value **and** that Jim's *resolved* `stop_plan` is `allow`. |
| 4 | `TestPlanSupervisorEffectivePolicy_ExactlyOneAllow` | Unit | *PlanSupervisor's tool grant is the allow-set and nothing else* | **Complement-complete, not a named subset.** Asserts against `ResolveEffectivePolicy` that `len(allowed) == 1` (`plan_correct`) and `len(denied) == len(allStaticToolNames) - 1`, and that `stop_plan` is among the denied (FR-043). Rev 1 pinned 14 of 83 names while NFR-2 asserted a property over all of them; `denyAllThenOverride` makes the strong assertion cheaper than the weak one. **This test is also the recorded gate on D-04**: granting PlanSupervisor `list_jobs` later must fail here, deliberately, and be changed deliberately. |
| 5 | `TestSeededAgents_DenyPlanCorrect` | Unit | *Every non-PlanSupervisor principal is denied correction* | Every seeded agent other than PlanSupervisor resolves `deny` for `plan_correct` |
| 5b | `TestSeededAgents_CanStopWhatTheyCanStart` | Unit | *No agent can start a plan it cannot stop* | **FR-006b / SC-004b — the C3-02 regression, and the one that must assert the property, not the seed.** Two assertions, deliberately different: (a) **seed shape** — for every map produced by `coreAgentSeed`, `systemAgentSeed` and `NewCustomAgentToolsCfg`, `policies["stop_plan"] == policies["execute_plan"]`; for the Worker's sparse `tightenGlobalCeiling` map, `execute_plan` absent **and** `stop_plan` an explicit `deny`. (b) **resolved behaviour** — for every seeded agent, `resolved(execute_plan) != deny ⟹ resolved(stop_plan) != deny`, and `resolved(stop_plan)` for `jim` is `allow`. Both are needed: N14 proves the seed literal and the resolved value already disagree in the tree for `execute_plan`, so an implementer checking only one can still ship the defect. |
| 5c | `TestWorkerSeed_StopPlanIsExplicitDeny` | Unit | *No agent can start a plan it cannot stop* | FR-006b's one exception, and the reason it is an exception. The Worker's map is **sparse** (`tightenGlobalCeiling`), so an absent key inherits the **global ceiling** — which for `stop_plan` is `allow`. An absent `stop_plan` would therefore silently grant it, the exact trap `inspect_session` carries an explicit `deny` for at `core.go:489-496`. Asserts the resolved policy is `deny`, not merely that the key exists |
| 6 | `TestCustomAgent_ResolvesAllowForPlanCorrect_GateIsTheControl` | Unit | *Every non-PlanSupervisor principal is denied correction* (custom-agent row) | Documents N2: the policy layer resolves `allow` for a custom agent, so the engine gate must be the control |
| 7 | `TestSystemAgentSeed_PlanSupervisorMembershipInBothPlaces` | Unit | — (setup) | `SystemAgents()` and `systemAgentIDs` both contain `plansupervisor` |
| 8 | `TestSeedSystemAgents_PlanSupervisorInvariantsReEnforced` | Unit | *PlanSupervisor's tool grant is the allow-set and nothing else* | `Type`, `Locked`, `Default`, `MemoryEnabled` and the tool-policy map are repaired on boot; Model/Provider/SOUL are preserved. **This test is also the proof that an agent-level kill switch cannot exist** (O9): it asserts the seeded policy map is re-stamped every boot, which is precisely what would revert one |
| 9 | `TestSeedSystemAgents_SkillAllowlistIsExplicit` | Unit | *PlanSupervisor's own prompt contains no retired vocabulary and no contradicted rule* | PlanSupervisor's `Skills` is exactly `["plan"]` and **never nil** (N3) |
| 10 | `TestPlanSupervisorSoulSeededAndNotOverwritten` | Unit | — | The rubric materialises into SOUL.md on first boot and an operator edit survives the next boot |
| 11 | `TestValidateCorrection_SupersedeRequiresTailMembers` | Unit | *A supersede with no replacement work is rejected* | The FR-030 pairing rule |
| 12 | `TestValidateCorrection_SupersedeWithTailMembersAccepted` | Unit | *A supersede paired with replacement work is applied* | The pairing rule does not over-reject |
| 13 | `TestValidateCorrection_TargetedRetryRejectsDoneMember` | Unit | *A targeted_retry of a superseded member remains impossible* | Pins the status fork that makes the ADR's original pairing rule impossible |
| 14 | `TestCorrectionRequest_HasNoDoDOrOwnerField` | Unit | *The correction request cannot carry a DoD or an owner reassignment* | The FR-032 conformance assertion (replaces the retired byte-identical tautology) |
| 15 | `TestRequireOwner_AdmitsPlanSupervisorByIdentity` | Unit | *Only PlanSupervisor may apply a correction* | The gate matches identity, not `Type == system` |
| 16 | `TestRequireOwner_DeniesEveryOtherPrincipal` | Unit | *Every non-PlanSupervisor principal is denied correction* | Includes the plan's own `owner_agent_id` and the Judge |
| 17 | `TestRequireOwner_AllDenialsIndistinguishable` | Unit | *Denials are indistinguishable and leak no plan state* | All three branches return one identical wrapped `ErrCorrectionNotOwner` message |
| 18 | `TestRequireOwner_SessionClauseScopedForPlanSupervisor` | Unit | *Only PlanSupervisor may apply a correction* | The `OwnerSessionID` clause does not deny PlanSupervisor (whose session is not `plan:<id>`) |
| 19 | `TestOwnerAgentIDIsAlwaysAnAgent_BothWritePaths` | Unit | *Every plan's outcome reaches its responsible agent over the bus* | `owner_agent_id` is required and chat-target-validated on both write paths, so the bus delivery can never be unaddressable |
| 20 | `TestValidateCorrection_TailEdgesRejectCycle` | Unit | *The `plan_correct` payload is validated before any mutation* | FR-046's cycle check. **Replaces the withdrawn migrator test.** A cycle is unresolvable by the dispatcher and, combined with the wake dedup, would strand the plan permanently |
| 21 | `TestValidateCorrection_TailEdgesRejectDanglingAndSuperseded` | Unit | *The `plan_correct` payload is validated before any mutation* | FR-046: an edge naming an unknown member, and one naming a superseded member |
| 22 | `TestValidateCorrection_TailMembersRejectedOnTargetedRetry_AndCapped` | Unit | *The `plan_correct` payload is validated before any mutation* | FR-046: `TailMembers` is legal only on `append`/`supersede`; both collections are capped. Today `AppendCorrection` sets `Members: req.TailMembers` unconditionally (`:2621`) |
| 23 | `TestHumanRecipientResolution_AgentIDNeverMatches` | Unit | *An agent-created plan writes no unreadable notification file* | `Plan.Owner` is offered to the notification store **only** when it resolves to a `Gateway.Users` entry |
| 24 | `TestCorrectionMemberIDsAreEngineMinted` | Unit | *Member ids are minted by the engine, never by the caller* | FR-046: the tool schema exposes no caller-supplied member id, and intent-log replay still creates each member exactly once. Retires the silent-drop class rather than validating around it |
| 25 | `TestPlanSkillEmbeddedText_AgreesWithValidateCorrection` | Unit | *PlanSupervisor's own prompt contains no retired vocabulary and no contradicted rule* | The prompt-regression guard no compiler can provide (FR-040). Asserts **three** absences (`awaiting_owner_correction` **in both spellings**, the forked-Planner instruction, and *"Optionally append a replacement"*) **and two presences**: the pairing rule at `SKILL.md:181`, **and an `ABANDON` row in the verb table at `:177-183`** (new in rev 3). Rev 2 asserted three absences and one presence, so nothing caught that `abandon` — the honest exit US-1 AS6 and FR-046 depend on — existed **only in the rubric** and in no version of the skill the adjudicator reads (r3 M3-10). An adjudicator reading a three-verb table when the situation needs a fourth loops on inapplicable verbs until the FR-022 ceiling terminates the plan `supervision_unavailable`, mislabelling an unreachable DoD as an unavailable supervisor |
| 26 | `TestAppendCorrection_AppendDispatchesTailMember` | Integration | *PlanSupervisor appends a missing step…* | The happy path end to end against real stores |
| 27 | `TestAppendCorrection_DoesNotIncrementJudgeRounds` | Integration | *A correction does not itself consume a judge round* | FR-034's single-writer property |
| 28 | `TestAppendCorrection_TargetedRetryResetsOnlyNamedMember` | Integration | *PlanSupervisor targets one failed member for retry* | No collateral reset |
| 29 | `TestAppendCorrection_HonestExitHandoverSaysUnreachable` | Integration | *A plan whose DoD is unreachable fails honestly…* | FR-035's distinct message |
| 30 | `TestPlanNeverReachesDoneByDiscountingEvidence` | Integration | *A plan cannot reach done by discounting evidence, bare or paired* | The behavioural integrity test (replaces v2's tautology). **Drives real corrections, not rejections**: bare supersede (rejected), then supersede paired with a criteria-less tail member (rejected by FR-030b), then supersede paired with a criteria-inheriting tail member that also fails — asserting `done` is unreachable in all three |
| 31 | `TestWakeRouting_CorrectionDecisionSitesTargetPlanSupervisor` | Integration | *PlanSupervisor diagnoses a stall…* | **Exactly two** wake sites carry PlanSupervisor's agent id — `surfaceStallIfAny` (`:1254`) and the UNMET verdict (`:1542`). **Routing only** — a fake notifier is sufficient here and is *not* sufficient for #31b |
| 31b | `TestSupervisionWake_ActuallyDispatchesATurn` | Integration | *The supervision wake actually starts a turn* | **FR-012c(A) / SC-025 — the N15 regression, and the deepest instance of the rev-3 through-line.** Asserts, against a **real** notifier + bus + loop, all four of SC-025's assertions for family A: (1) an agent turn **ran** for `plansupervisor`; (2) its `transcriptSessionID` equals `supervision.session_id`, `ResolveSessionStore` on that id is non-nil, and the transcript is non-empty; (3) **zero** outbound messages were published to any channel (the H8/FR-016 non-leak property, asserted as a property and not as "a send failed" — D-11); (4) `Notify`-was-called is explicitly **not** accepted as evidence. Today this fails at (1): `wakeOwner` hardcodes `Channel: "system"`, the bus `ChatID` becomes `"system:plan:<id>"`, and `processSystemMessage` drops it at `loop.go:6023` before dispatching anything. Every existing wake test passes because the fake notifier records the call three hops upstream of that drop |
| 31c | `TestOwnerWake_ReachesATurnAndTheOriginChat` | Integration | *An Owner wake reaches a turn and the conversation the plan came from* | **FR-012c(B) / FR-016c.** The **Owner** wake sites (`:1571`, `:1610`, `:1742`) on a plan **with** an origin: a turn ran for `owner_agent_id`; its `transcriptSessionID` equals `Plan.OwnerSessionID` and that id resolves (FR-016c — a `"plan:<id>"`-shaped id fails here, N13); and an outbound message was published to `p.SourceChannel`/`p.SourceChatID`. Also carries §13.4's sibling assertion that a `cli` and a `subagent` system message still start **no** turn. US-4 AS1 and SC-010 measure delivery to `owner_agent_id`, and N15 makes that delivery a no-op today, so without this the P0 story ships against a dead rail. *(Rev 3 named this `TestPlanWakes_NoInternalChannelDrop` — renamed because the internal-channel drop is the symptom, not the property, and a test named after a mechanism invites assertions about that mechanism.)* |
| 31d | `TestOriginLessPlan_SupervisedAndDeliveredWithoutAChatLeg` | Integration | *A plan created in the UI still reaches its owner* (**new**) | **FR-012d(4) / N16 / SC-026 / D-10 — the origin-less case, and the one a naive fix misses because the real-channel happy path passes without it.** A plan created through `POST /api/v1/plans` (both origin fields absent), driven through a **supervision** wake and then to a terminal state. Asserts, in order: (1) the **supervision** wake still runs a turn for `plansupervisor` — an origin-less plan MUST NOT become a second silent-drop case; (2) the Owner wake still runs a turn for `owner_agent_id`; (3) its synthesis is **persisted** to `Plan.OwnerSessionID`; (4) **zero** outbound messages were published anywhere — explicitly including the owner agent's last-active channel and any other session belonging to the creator, the fallbacks D-10 rejects; (5) a `plan_completed`/`plan_failed` notification with `plan_id` exists; (6) **`supervision.wake_error` is unset, the attempt count did not increment, and `failed_reason != supervision_unavailable`** — the N16 assertion, which fails loudly under D-10's rejected option (a) where the empty origin is passed to `Notify` and FR-N7's rejection feeds FR-024's escalation ladder. **Limb (6) is the point of this row**: without it a naive fix ships a healthy UI-created plan that terminates itself as "supervision unavailable", and every mechanism-level assertion stays green |
| 31e | `TestPlanOrigin_RecordedOnBothWritePathsIncludingWebchat` | Integration | *A plan created in the UI still reaches its owner* (sibling) | **FR-012d(2)(3) / SC-026's population half.** `create_plan` invoked from a turn on channel `telegram` records `telegram`; invoked from a turn on channel **`webchat`** records `webchat` (the deliberate divergence from `pkg/tools/task.go:541` — N15.3/E39; **this assertion is the only thing stopping an implementer from copying the task line verbatim** and silently un-addressing every SPA-created plan); `POST /api/v1/plans` records neither. Also asserts both fields are **server-set**: a `source_channel` in a create or PATCH body is not honoured |
| 32 | `TestWakeRouting_EveryOtherSiteTargetsOwnerAgent` | Integration | *Every plan's outcome reaches its responsible agent over the bus* | **Three** sites address `owner_agent_id`, not PlanSupervisor: `synthesizeAndComplete` (`:1571`, **the success path**), `failPlanLocked` (`:1610`) and `StopPlan` (`:1742`). Rev 1 counted two and thereby confirmed the success path was not among them |
| 32b | `TestSuccessPath_OwnerIsWokenWhenPlanReachesDone` | Integration | *A plan that reaches `done` notifies its owner agent and its human author* | The C2-03 regression: a plan reaching `done` must wake somebody. Asserts the recipient of the `plan_judge_met` wake is `owner_agent_id` and that the human author's notification is written |
| 33 | `TestOutcomeDelivery_OwnerAgentAlwaysWoken` | Integration | *Every plan's outcome reaches its responsible agent over the bus*; *An agent-created plan writes no unreadable notification file* | The guaranteed path; and **zero** notification files keyed on an agent id |
| 34 | `TestOutcomeDelivery_HumanCreatorGetsNotificationAndPush` | Integration | *A human who created a plan through the UI is additionally notified* | Store write plus WS frame, with `plan_id` |
| 35 | `TestOutcomeDelivery_NotificationFailureDoesNotLoseTheOutcome` | Integration | *A notification failure never means nobody was told* | The bus wake succeeded independently; ERROR logged; plan state unchanged |
| 36 | `TestPlanSupervisorUnavailable_AtCeilingTerminatesAndNotifiesOwner` | Integration | *PlanSupervisor unavailable **at the ceiling** terminates the plan and tells the responsible agent* | FR-019's **outcome**. Drives the plan to the attempt ceiling with a fake clock and asserts `failed(supervision_unavailable)` + the Owner wake. **Renamed and repointed in rev 3**: rev 2's name and trace claimed the plan *stays parked*, which is the opposite of what the test does (r3 M3-03) |
| 36a | `TestPlanSupervisorUnavailable_BelowCeilingReWakesAndTellsNobody` | Integration | *PlanSupervisor unavailable **below the ceiling** leaves the plan parked and tells nobody yet* | The other half of the split. Asserts the plan stays at `awaiting_supervision`, `attempts` increments, a wake re-issues, and **zero** Owner wakes are delivered — the "not yet" limb rev 2 asserted and never tested |
| 36e | `TestAppendCorrection_AcceptsStalledPhase` | Integration | *PlanSupervisor can actually correct a stalled plan* | **FR-029 / D-01 — the C3-01 regression.** Drives a plan to `plan_phase = stalled`, issues a valid `append`, and asserts it is **applied**, the phase becomes `dispatching` and the stall note is cleared. Under rev 2's gate (`!= awaiting_supervision`) this fails 100% of the time, which is exactly why it is written first |
| 36f | `TestSupervisionDeadline_ArmsAndBoundsAStalledPlan` | Integration | *A stalled plan's supervision deadline is armed, counted and bounded* | FR-029's second limb: a stall wake arms `wake_at`, increments `attempts` on the deadline, re-wakes, and terminates `failed(supervision_unavailable)` at the ceiling. Rev 2's FR-021–023 predicates were keyed on `awaiting_supervision` alone, so a stalled plan was bounded only by 7-day idle expiry |
| 36b | `TestSupervisionDeadline_DetectsATurnThatProducedNothing` | Integration | *A supervision turn that emits nothing at all is detected by the deadline* | FR-021 — the **detection mechanism**, which rev 1 had no test for because it had no mechanism. Advances a fake clock past `supervision.wake_at + timeout` and asserts the attempt counter advances and a second wake is delivered |
| 36c | `TestRejectedCorrection_ReArmsTheSupervisionWake` | Integration | *A rejected correction re-arms the supervision wake instead of stranding the plan* | FR-022 — the C2-02 regression. A rejected `plan_correct` mutates nothing and burns no round, so without this the plan is stranded until idle expiry |
| 36d | `TestAbandonVerb_TerminatesWithDodUnreachable` | Integration | *An un-correctable plan exits honestly through `abandon`* | FR-046 — US-1's promised honest exit made reachable. Asserts no member mutated, `failed_reason = dod_unreachable`, and a revision entry with verb `abandon` |
| 37 | `TestBootReconcile_ReWakesParkedPlanExactlyOnce` | Integration | *A parked plan is re-woken after a restart* | FR-023 |
| 38 | `TestBootReconcile_ParkedPlanBurnsNoJudgeRound` | Integration | *A parked plan is re-woken after a restart* | FR-193 preserved |
| 39 | `TestBootReconcile_ExhaustedParkedPlanTerminates` | Integration | *A parked plan at its round ceiling terminates…* | The unconditional ceiling check still wins |
| 40 | `TestStart_NilNotifierIsAPreconditionError` | Integration | *Starting the engine without a notifier is a startup error* | Scoped to `Start`, not to struct construction |
| 41 | `TestWakePublishFailure_RecordedAndRetried` | Integration | *A failed wake publish is recorded and retried* | FR-024's reversal of the best-effort contract |
| 42 | `TestSupervisionWake_IdempotentAcrossTicks` | Integration | *Repeated ticks do not produce repeated supervision wakes* | No wake storm. **Asserts the dedup key is `supervision.wake_at`**, not the unmet signature — the signature is set once at UNMET and cleared only by a correction, so it carries no wake information and a case keyed on it fires every tick |
| 43 | `TestCorrectionAuditEntry_CarriesVerbTargetAndAssumption` | Integration | *An operator reads why a plan changed* | **FR-039b (widened) / D-03.** Applies one correction of each mutating verb and asserts each appears in `GET /api/v1/audit-log` with **actor, plan id, verb, target member id and falsified assumption**. **Renamed and repointed in rev 3:** rev 2 pointed US-6 AS1 at "the plan's revision history" while FR-037 verified no such read surface exists and AMB-5 deferred building one, so the test measured a surface that does not ship (r3 M3-08). The audit log does ship (`pkg/gateway/rest.go:4883`) |
| 44 | `TestRevisionHistory_SurvivesRestart` | Integration | *The revision history survives a restart* | `reconstructCorrections` replay |
| 45 | `TestRejectedCorrection_LeavesNoRevisionEntry` | Integration | *A rejected correction leaves no audit trace* | Validation runs before any write |
| 46 | `TestCorrectionAndStopAreSerialised` | Integration | *A correction and a stop are serialised* | `planDecisionMu` interleaving |
| 46b | `TestCorrectionRacesJudgeRound` | Integration | *A correction and a stop are serialised* (sibling interleaving) | E6. `AppendCorrection` holds the process-wide `planDecisionMu` for its whole body (`:2575-2576`) — the same mutex `processPlan`, `StopPlan`, the judge round and idle expiry take. Rev 1 tested one of the four named interleavings |
| 46c | `TestCorrectionRacesIdleExpiry` | Integration | *A long-parked plan is still reaped by idle expiry* (sibling interleaving) | E7 / §20's reaper against the same mutex |
| 46d | `TestReconstructCorrections_CorruptLogIsSurfacedNotSwallowed` | Integration | *A corrupt intent log is surfaced, not swallowed* | FR-048 — N10. Truncates one plan's intent-log JSONL, boots, and asserts an ERROR names the plan and that no previously-superseded member is silently un-superseded |
| 47 | `TestSessionCreate_RejectsSystemAgentTarget` | Integration | *A user cannot open a chat session against a System Agent* | `rest.go` guard |
| 48 | `TestWSChatFrame_RejectsSystemAgentTarget` | Integration | *System agents are rejected on every chat-target surface* | `websocket.go` guard |
| 49 | `TestSetDefaultAgent_RejectsSystemAgent` | Integration | *System agents are rejected on every chat-target surface* | `rest.go` `default:true` guard, mirroring the per-channel one |
| 50 | `TestGetDefaultAgent_NeverReturnsSystemAgent` | Integration | *System agents are rejected on every chat-target surface* | Includes the degenerate final fallback |
| 51 | `TestVerifierSessionAndSupervisionWakeStillWork` | Integration | *The supervision wake still reaches PlanSupervisor…* | The guards do not break `NewVerifierSession` or the bus wake path |
| 52 | `TestPlanCorrectTool_FailsClosedWhenUnwired` | Integration | *Only PlanSupervisor may apply a correction* | The injected func-value seam never defaults to allow |
| 53 | `TestIdleExpiry_ReapsLongParkedPlan` | Integration | *A long-parked plan is still reaped by idle expiry* | No immortal record. **Owned by FR-028** (rev 1 left this test in zero matrix rows) |
| 54 | `TestCorrectionEmitsStructuredLog` | Integration | *Every applied correction emits a structured log line* | Observability (FR-039) |
| 54b | `TestCorrectionEmitsAuditEvent` | Integration | *Every correction produces an audit event* | FR-039b. `auditPlan` (`pkg/gateway/rest_plans.go:93`) carries six events, none for correction, none recording an actor — so this is new work. Closes AMB-6 as **yes** |
| 55 | `TestNotificationContract_PlanTypesAndPlanID` | Contract | *A human who created a plan through the UI is additionally notified* | The widened `type` enum and `plan_id` exist in **all three** places — `Notification.yaml`, `asyncapi.yaml`'s `NotificationFrame.notification_type`, and the SPA normaliser — and both generated surfaces |
| 55b | `notifications.unknown-type.test.ts` | Unit (vitest) | *A human who created a plan through the UI is additionally notified* (negative) | M2-04's blast radius: asserts an unknown `type` makes the **whole** REST list throw (not one row), and that the WS path drops the frame silently. Documents the two unlike failure modes that make FR-017's ordering non-negotiable |
| 56 | `verify-contracts` (`make`) | Contract | *The contract artifacts are regenerated in the same change* | Zero drift after the rename and the additive fields |
| 57 | `TestPlanWireShape_PhaseEnumAndFailedReasons` | Contract | *A plan parks under the new phase name end to end* | The generated Go and TS enums carry `awaiting_supervision` and **not** the old value; and carry `dod_unreachable` + `supervision_unavailable`. **Extended in rev 3:** also asserts the generated `RevisionEntryVerb` carries **four** values including `abandon`, and that `RevisionEntryVerb("abandon").Valid()` is true (r3 M3-14) |
| 57b | `TestPlanWireShape_SupervisionObject` | Contract | *A rejected correction re-arms the supervision wake…* | `Plan.yaml`'s new `supervision` object round-trips through `pkg/api/generated/contract_test.go` under `additionalProperties: false` |
| 57c | `TestPlanPatch_SupervisionFieldsAreIndependent` | Unit | *The correction counter survives a park → dispatch → park cycle* | **FR-050's write path (r3 M3-16).** Asserts `plan.Patch` exposes the five supervision fields as **discrete** pointers, that a patch setting only `SupervisionAttempts` leaves `SupervisionCorrectionRounds` and `SupervisionSessionID` untouched on disk, and that a REST-shaped patch with all five nil leaves the whole object unchanged. Without discrete fields, a concurrent `rest_plans.go` `Store.Update` — which does **not** hold `planDecisionMu` — can clobber an in-flight supervision write |
| 58 | `plan-supervision.e2e.spec.ts` | E2E | *The SPA parked and stalled copy names a control the user actually has* | A parked plan renders in the plans list with no `ApiSchemaError`; the parked chip says a supervisor is reviewing it and Stop remains available; the stalled chip carries its own distinct wording; neither says "no in-app action" |
| 59 | `TestParkedPhaseNeverMaskedByStalled` | Integration | *The parked phase and the stalled phase never co-occur* | Pins the `surfaceStallIfAny` precedence guard now that both wakes route to PlanSupervisor |
| 60b | `TestCorrectedPlanReachesDone_ScriptedAdjudicator` | Integration | *A corrected plan reaches done with no human input*; *PlanSupervisor appends a missing step…* | **The blocking merge gate for SC-001 (D-07).** The full closed loop end to end — wake dispatches a turn, the turn's `plan_correct` is accepted, the plan resumes and terminates with zero human input — with a **scripted adjudicator double** in place of the LLM, so it is deterministic. Proves the *loop closes* |
| 60 | `TestCorrectedPlanReachesDoneWithNoHumanInput` | E2E (**nightly, not a merge gate**) | *A corrected plan reaches done with no human input* | **SC-001b.** The same loop against a **real** provider, proving the *rubric works*. A failure is a rubric defect to fix with a tracked issue and target date — never auto-quarantined, and never a merge blocker (RISK-11, D-07) |
| 61 | `TestSupersedeVerbIsMachineReadableInHistory` | Integration | *A supersede is distinguishable in the audit trail* | FR-031 — verb distinguishable without parsing free text |
| 62 | `TestFourTerminalSupervisionCausesAreDistinguishable` | Integration | *The four terminal supervision causes are distinguishable*; *The correction counter survives a park → dispatch → park cycle* | FR-035. **Three enum values, four messages**: `judge_rounds_exhausted` with `correction_rounds == 0` vs `> 0`; `dod_unreachable`; `supervision_unavailable`. **Extended in rev 3:** the cause-2 case MUST be driven by an actual **park → correct → dispatch → re-park → exhaust** cycle, not by writing `correction_rounds > 0` into a fixture — that is the only shape that would have caught FR-050's blanket reset (r3 C3-03) |
| 63 | `TestStopPlan_HaltsTheInFlightSupervisionTurn` | Integration | *Stopping a plan halts the supervisor working on it* | **FR-044 / SC-020 — the containment control, asserted at the outcome (operator ruling 5).** Starts a **real** supervision turn bound to `supervision.session_id` and blocked on a controllable barrier, calls `StopPlan`, and asserts (a) the turn's context observes cancellation and the turn **returns** within a bounded time, and (b) `GetActiveTurnHookForSession(supervision.session_id)` resolved a live turn, i.e. the cancel was **claimed**. **MUST NOT use `plan_stop_test.go`'s `fakeSessionCanceller`** — recording the id and returning `(true, nil)` is exactly the false green rev 2 shipped |
| 63b | `TestSupervisionSessionID_IsTheSessionTheTurnRanIn` | Integration | *Stopping a plan halts the supervisor working on it* (AS1b) | FR-016b's identity half, and the cheaper of the two guards. Asserts `ResolveSessionStore(supervision.session_id)` is non-nil and that the supervision turn's transcript is persisted **to that id** — so the id `StopPlan` cancels is provably the id the turn is bound to. A derived/synthetic id fails here (N13). **Rev-4 sibling assertion:** `supervision.session_id != Plan.OwnerSessionID` — FR-016 requires the two actors' transcripts to be disjoint, and after FR-016c both are real minted sessions, so "they happen to differ" stops being structurally guaranteed by one of them being fake |
| 63c | `TestStopPlan_OwnerSessionCancelIsNoLongerANoOp` | Integration | *Stopping a plan halts the supervisor working on it* (sibling) | **FR-016c's free consequence, pinned so it cannot silently regress.** `StopPlan`'s owner-session leg cancels a **live** Owner turn bound to `Plan.OwnerSessionID`. Asserts the turn **terminated** — never that the id appeared in the cancel fan-out set, which is exactly the false-green C3-05 was (`fakeSessionCanceller` returns `(true, nil)` regardless). Fails today for the reason N13 gives: the id names nothing |
| 64 | `TestStopPlanTool_OwnerAllowedOthersDenied` | Integration | *A plan's owner agent can stop the plan it started*; *An agent cannot stop a plan it does not own* | FR-042, FR-043. Includes the indistinguishable-denial assertion for not-owner vs does-not-exist. **Extended in rev 3:** the "owner allowed" limb runs against the **resolved** policy on a fresh install (Dataset B11) and asserts the full cascade **including the supervision turn halting**, so C3-02 and C3-05 cannot both be green while the tool ships denied |
| 65 | `TestStopPlanTool_FailsClosedWhenUnwired` | Integration | *A plan's owner agent can stop the plan it started* | The injected func-value seam never defaults to allow — the same discipline as #52 |
| 66 | `TestAgentIDCannotBeSetToASystemAgentID` | Integration | *The id `plansupervisor` cannot be claimed by any principal* | FR-049 — pins N12. Asserts `createAgent` mints a UUID regardless of any client-supplied id, that `{"type":"system"}` is rejected 400, and that `updateAgent` never writes `.ID`. Without this, FR-009's whole integrity property is "nobody picked that name" |
| 67 | `TestValidateCorrection_SupersedeReplacementInheritsCriteria` | Unit | *A supersede whose replacement drops the failing criteria is rejected*; *A supersede whose replacement carries only some of the failing criteria is rejected*; *A supersede of a member with no acceptance criteria is allowed* | FR-030b — the control that makes US-3's claim true rather than a speed bump. **Four cases in rev 3, up from one:** replacement carries **none** (reject), a **strict subset** (reject — the 1-of-N bypass rev 2's "carries none of" wording left open), **all** (accept), and a superseded member with **zero** criteria (accept, vacuously). Comparison is by the exact predicate in FR-030b, never by rendered text (r3 M3-05) |
| 68 | `TestStallNoteClearedOnEnteringParkedPhase` | Integration | *A stale stall note does not follow a plan into the parked phase* | FR-025 — N9. `surfaceStallIfAny`'s clearing branch sits behind the parked-phase guard, so without this the adjudicator reads a stale diagnosis |
| 69 | `TestNotificationStore_RejectsNonUserRecipient` | Unit | *An agent-created plan writes no unreadable notification file* | FR-051 — closes the orphan-file class at `Store.Create` itself, not only at the new call site. The next caller would otherwise reintroduce the failure `pkg/gateway/schedules.go:604-608` already documents |
| 70 | `TestValidateMemberRef_OwnershipCheckedBeforeStatus` | Unit | *The `plan_correct` payload is validated before any mutation* | FR-047 — m2-02. Today the status check runs first (`:2730-2737`), so the error names another plan's id |
| 71 | `TestSupervisionGauge_CountsParkedPlans` | Integration | *An operator can see how many plans are awaiting supervision* | **FR-026 — the gauge's own test (r3 M3-11).** Rev 2 traced FR-026 to *"#53 (sibling assertion)"*, i.e. `TestIdleExpiry_ReapsLongParkedPlan`, which reaps a plan and asserts nothing about a count — so the single number §20's 3 AM runbook opens with had **no** test, while §17's completeness check claimed every behavioural FR had one. Asserts the gauge reads **3** with three plans parked and two running, and that it moves when a plan leaves the phase. #53 is owned by FR-028 alone |
| 72 | `TestConformance_NoMemberRetrySurfaceExists` | Unit | — (**FR-020's conformance assertion**) | **Named, filed and runnable in rev 3.** Mechanically asserts no member-retry REST route, tool verb or SPA control exists. Rev 2 described the three negative-requirement conformance assertions but gave them no test id, no file and no harness — honest about excluding them from the coverage claim, and they were still deliverables nobody would have implemented (r3 test-gap 7) |
| 73 | `TestConformance_NoStateGuardInUpdateLocked` | Unit | — (**FR-033's conformance assertion**) | Asserts `plan.Store.updateLocked` contains no non-draft state guard **and** that a `Bounds` write on a running plan still succeeds |
| 74 | `TestConformance_NoRollbackSurfaceExists` | Unit | — (**FR-038's conformance assertion**) | Asserts no rollback verb, route or handler exists |

> **On tests #72–#74.** They are *absence* assertions, which are weaker than behavioural tests and
> can rot into tautologies. They are specified as **mechanical scans of the shipped surface**
> (the registered tool/verb tables, the route table, the function body) rather than as greps for a
> string, so that renaming the forbidden construct does not silently satisfy them.

### 13.3 Test Datasets

#### Dataset A — `plan_correct` verb + payload validation

| # | Verb | `superseded_member_id` | `retried_member_id` | `tail_members` | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|---|---|
| A1 | `append` | — | — | 1 valid member | Happy path | Applied | *PlanSupervisor appends a missing step…* |
| A2 | `append` | — | — | `[]` (empty) | Empty collection | Rejected: append requires at least one tail member | *A supersede with no replacement work is rejected* (sibling rule) |
| A3 | `append` | — | — | **20 members** (`max_tail_members`) | Max collection | Applied | *PlanSupervisor appends a missing step…* |
| A4 | `supersede` | a `done` member | — | `[]` (empty) | **The integrity boundary** | **Rejected** (FR-030) | *A supersede with no replacement work is rejected* |
| A5 | `supersede` | a `done` member | — | 1 valid member | Min non-empty | Applied | *A supersede paired with replacement work is applied* |
| A6 | `supersede` | `""` | — | 1 member | Empty string | Rejected: supersede requires superseded_member_id | *A supersede with no replacement work is rejected* |
| A7 | `supersede` | a `failed` member | — | 1 member | Wrong status | Rejected: member is failed, not done | *A targeted_retry of a superseded member remains impossible* (mirror) |
| A8 | `supersede` | a member of another plan | — | 1 member | Cross-entity | Rejected — **and the message names neither the other plan's id nor the member's status** (FR-047; ownership is checked before status) | *The `plan_correct` payload is validated before any mutation* |
| A9 | `targeted_retry` | — | a `failed` member | — | Happy path | Applied | *PlanSupervisor targets one failed member for retry* |
| A10 | `targeted_retry` | — | a `done` member | — | Wrong status | Rejected: member is done, not failed | *A targeted_retry of a superseded member remains impossible* |
| A11 | `targeted_retry` | — | `""` | — | Empty string | Rejected: targeted_retry requires retried_member_id | *A targeted_retry of a superseded member remains impossible* |
| A12 | `targeted_retry` | — | a nonexistent id | — | Missing FK | Rejected: member not found | *A targeted_retry of a superseded member remains impossible* |
| A13 | `"delete"` | — | — | — | Unknown enum | Rejected: unknown correction verb | *Every non-PlanSupervisor principal is denied correction* (validation sibling) |
| A14 | `""` | — | — | — | Empty enum | Rejected: unknown correction verb | *Every non-PlanSupervisor principal is denied correction* |
| A15 | `supersede` | a `done` member | — | **replayed** intent record whose tail member already exists | Duplicate / replay | Applied idempotently, no duplicate task. **Reachable only on replay** — under FR-046 ids are engine-minted, so a caller cannot supply a colliding id | *Member ids are minted by the engine, never by the caller* |
| A16 | `append` | — | — | member with a 10 KB title | Very long string | **Rejected** by the payload cap (FR-046, **`maxMemberTitleBytes`** — rev 2's A16 cited `max_title_bytes`, which is not a name FR-046 defines; r3 m3-01). Rev 1 said *"applied or rejected — never a panic"*, which is not an expectation a test can assert | *The `plan_correct` payload is validated before any mutation* |
| A17 | `append` | — | — | member with a unicode/RTL/emoji title | Unicode | Applied, round-trips byte-identical | *PlanSupervisor appends a missing step…* |
| A18 | `append` | — | — | 2 members + `tail_edges` `[{M1→M2},{M2→M1}]` | **Cycle** | Rejected: tail edges form a cycle. No task created, no edge wired | *The `plan_correct` payload is validated before any mutation* |
| A19 | `append` | — | — | 1 member + `tail_edges` naming a `to_task_id` not in the plan | Dangling FK | Rejected: edge names an unknown member | *The `plan_correct` payload is validated before any mutation* |
| A20 | `append` | — | — | 1 member + `tail_edges` pointing at a **superseded** member | Stale reference | Rejected: edge names a superseded member | *The `plan_correct` payload is validated before any mutation* |
| A21 | `targeted_retry` | — | a `failed` member | 50 members | **Wrong verb for the field** | Rejected: `tail_members` is not valid on `targeted_retry`. Today `AppendCorrection` sets `Members: req.TailMembers` unconditionally (`:2621`) and would create all 50 | *The `plan_correct` payload is validated before any mutation* |
| A22 | `append` | — | — | `cap + 1` members | Max collection + 1 | Rejected: `tail_members` exceeds the cap. This is the payload that holds process-wide `planDecisionMu` for its whole body (`:2575-2576`) | *The `plan_correct` payload is validated before any mutation* |
| A23 | `append` | — | — | 1 member + `cap + 1` edges | Max collection + 1 | Rejected: `tail_edges` exceeds the cap | *The `plan_correct` payload is validated before any mutation* |
| A24 | `supersede` | a `done` member carrying criteria `C` | — | 1 member carrying **none** of `C` | **The integrity boundary, paired form** | **Rejected** (FR-030b) — attaching cheap unrelated work does not make discounting legal | *A supersede whose replacement drops the failing criteria is rejected* |
| A25 | `abandon` | — | — | — | Honest exit | Applied: plan → `failed(dod_unreachable)`; no member mutated; revision entry with verb `abandon` | *An un-correctable plan exits honestly through `abandon`* |
| A26 | `abandon` | a `done` member | — | 1 member | Verb/field mismatch | Rejected: `abandon` carries no member or tail fields | *The `plan_correct` payload is validated before any mutation* |
| A27 | `abandon` | — | — | — (empty `falsified_assumption`) | Empty string | Rejected: `abandon` requires a falsified assumption — an honest exit that records no reason is not an audit trail | *An un-correctable plan exits honestly through `abandon`* |
| A28 | `supersede` | a `done` member carrying criteria `{C1, C2, C3}` | — | tail members carrying `{C1}` only | **The integrity boundary, subset form** | **Rejected** (FR-030b) — *every* criterion must be carried, not at least one. This is the 1-of-N bypass rev 2's *"carries none of"* wording left open (r3 M3-05); the rejection names `C2` and `C3` | *A supersede whose replacement carries only some of the failing criteria is rejected* |
| A29 | `supersede` | a `done` member carrying **zero** criteria | — | 1 member | Empty criteria set | **Applied** — "carries every criterion of `M`" is vacuously satisfied over `[]`. FR-030's `len(tail_members) > 0` still applies, so the bare form is still rejected | *A supersede of a member with no acceptance criteria is allowed* |
| A30 | `append` | — | — | 1 valid member | **Phase boundary — plan at `stalled`** | **Applied.** The supervision-eligible phase set is `{awaiting_supervision, stalled}` (FR-029, D-01). Under rev 2's gate this row was rejected, and no dataset row covered it | *PlanSupervisor can actually correct a stalled plan* |
| A31 | `append` | — | — | 1 valid member | Phase boundary — plan at `dispatching` | **Rejected** with the phase-mismatch error — widening the gate to `stalled` does not open it to every phase (E1) | *The `plan_correct` payload is validated before any mutation* |

#### Dataset B — correction authority (`requireOwner`)

| # | Caller `AgentID` | Caller `SessionID` | Plan `OwnerAgentID` | Plan `OwnerSessionID` | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|---|---|
| B1 | `plansupervisor` | any | `jim` | `plan:P1` | Happy path | **Allowed** | *Only PlanSupervisor may apply a correction* |
| B2 | `plansupervisor` | any | `jim` | `""` | Empty session | **Allowed** | *Only PlanSupervisor may apply a correction* |
| B3 | `jim` | `plan:P1` | `jim` | `plan:P1` | The plan's own owner | **Denied** | *Every non-PlanSupervisor principal is denied correction* |
| B4 | `judge` | any | `jim` | `plan:P1` | Another System Agent | **Denied** | *Every non-PlanSupervisor principal is denied correction* |
| B5 | `mycustomagent` | any | `jim` | `plan:P1` | **Custom agent — policy resolves `allow`** | **Denied by the gate** | *Every non-PlanSupervisor principal is denied correction* |
| B6 | `""` | any | `jim` | `plan:P1` | Empty identity | **Denied** | *Every non-PlanSupervisor principal is denied correction* |
| B7 | `mycustomagent` | any | `jim` | n/a — **plan does not exist** | Missing entity, unauthorised caller | **Denied with the identical message body as B3–B6** — the plan-load failure MUST be normalised to the authority denial before the store is read, or it is a perfect existence oracle | *Denials are indistinguishable and leak no plan state* |
| B7b | `plansupervisor` | any | n/a | n/a — **plan does not exist** | Missing entity, **authorised** caller | **Real not-found error**, distinguishable from an authority denial — PlanSupervisor must be able to tell "I named the wrong plan" from "I am not permitted", which FR-019's honest-exit definition depends on | *An authorised caller naming a plan that does not exist gets a real not-found error* |
| B8 | `PlanSupervisor` (different case) | any | `jim` | `plan:P1` | Case sensitivity | **Denied** — identity match is exact | *Every non-PlanSupervisor principal is denied correction* |
| B9 | `plansupervisor ` (trailing space) | any | `jim` | `plan:P1` | Whitespace | **Denied** — no trimming in the gate | *Every non-PlanSupervisor principal is denied correction* |
| B10 | an operator or an agent holding `create_agent` | — | — | — | **Identity reservation** | The created agent's id is a server-minted UUID; the requested id `plansupervisor` is never honoured, and a `{"type":"system"}` body is rejected 400 | *The id `plansupervisor` cannot be claimed by any principal* |
| B11 | `jim` (the plan's owner) calling **`stop_plan`** | any | `jim` | `plan:P1` | Owner stop — happy path | **Allowed** — `stop_plan`'s authority is the plan's owner, deliberately the inverse of `plan_correct`'s | *A plan's owner agent can stop the plan it started* |
| B12 | `mycustomagent` calling **`stop_plan`** | any | `jim` | `plan:P1` | Non-owner stop | **Denied**, with the same message body as B13 | *An agent cannot stop a plan it does not own* |
| B13 | `mycustomagent` calling **`stop_plan`** | any | n/a | n/a — plan does not exist | Non-owner stop, missing entity | **Denied**, identical to B12 — the containment control leaks no existence either | *An agent cannot stop a plan it does not own* |
| B14 | `plansupervisor` calling **`stop_plan`** | any | `jim` | `plan:P1` | The adjudicator is not the owner | **Denied** — and the tool is denied to it at the policy layer besides (FR-008) | *PlanSupervisor's tool grant is the allow-set and nothing else* |

> **Two allowed rows for `plan_correct` (B1, B2), seven denied (B3–B7, B8, B9).** SC-002 is stated
> against these counts. Rev 1's SC-002 said "1 allowed and 8 denied" against a 9-row table with two
> allowed rows — arithmetically wrong about the dataset it cited by id.

#### Dataset C — outcome delivery fork

| # | `owner_agent_id` | `owner` (attribution) | In `Gateway.Users`? | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|---|
| C1 | `jim` (registered) | `jim` (agent-created) | no | Agent-created — the majority case | Bus wake to `jim`; **zero** notification files | *An agent-created plan writes no unreadable notification file* |
| C2 | `jim` (registered) | `alice` | yes, WS open | Human-created | Bus wake **and** notification + WS frame + `plan_id` | *A human who created a plan through the UI is additionally notified* |
| C3 | `jim` (registered) | `alice` | yes, **no** WS open | No live consumer | Bus wake; notification persisted; no frame; no error | *A human who created a plan through the UI is additionally notified* |
| C4 | `jim` (registered) | `""` | n/a | Empty attribution | Bus wake only; no notification; no error | *Every plan's outcome reaches its responsible agent over the bus* |
| C5 | `jim` (registered) | `ghost` | no (user deleted) | Dangling reference | Bus wake only; no notification; WARN logged | *An agent-created plan writes no unreadable notification file* |
| C6 | `jim` (registered) | `alice` | yes | Store returns an error | Bus wake succeeded; ERROR logged; plan state unchanged | *A notification failure never means nobody was told* |
| C7 | `jim` (registered) | `alice` | yes, `Users` **empty** (bypass install) | No user registry | Bus wake only; no notification; documented, not an error | *Every plan's outcome reaches its responsible agent over the bus* |
| C8 | `ghost` (agent deleted) | `alice` | yes | Dangling agent | Bus publish fails → recorded and surfaced (FR-024); notification still written | *A notification failure never means nobody was told* |
| C9 | `jim` (registered) | a username byte-identical to an agent id | yes | Namespace collision | **Both** deliveries happen — additive, never exclusive | *A human who created a plan through the UI is additionally notified* |
| C10 | `jim` (registered) | 10 KB string | no | Very long string | Bus wake only; no notification; no panic | *An agent-created plan writes no unreadable notification file* |

#### Dataset D — tool-policy resolution for `plan_correct`

| # | Global ceiling | PlanSupervisor entry | Other-agent entry | Boundary type | Effective for PlanSupervisor | Effective for the other agent | Traces to |
|---|---|---|---|---|---|---|---|
| D1 | `allow` | `allow` | `deny` | **The specified seed** | `allow` | `deny` | *Only PlanSupervisor may apply a correction* |
| D2 | `deny` | `allow` | `deny` | Strictest-wins trap (N1) | **`deny` — feature dead** | `deny` | *Only PlanSupervisor may apply a correction* |
| D3 | `ask` | `allow` | `deny` | Strictest-wins trap (N1) | **`ask` — no human to answer** | `deny` | *Only PlanSupervisor may apply a correction* |
| D4 | `allow` | **`deny`** (seed override forgotten — `denyAllThenOverride` stamped it) | `deny` | **N1b — fresh install** | **`deny` — feature dead on arrival** | `deny` | *Only PlanSupervisor may apply a correction* |
| D5 | `allow` | `allow` | *(absent — agent persisted before the tool existed)* | **N2 — upgraded install** | `allow` | **`allow`** — only the engine gate stops it | *Every non-PlanSupervisor principal is denied correction* |
| D6 | *(absent)* | *(absent)* | *(absent)* | Coverage gap (not reachable — `defaults_test.go` forbids omitting the ceiling entry) | `deny` after repair backfills | `deny` | *Only PlanSupervisor may apply a correction* |

#### Dataset D2 — tool-policy resolution for `stop_plan`

> **New in rev 3.** Rev 2 had **no** `stop_plan` policy rows at all — Dataset D covered
> `plan_correct` only — which is why C3-02 (the tool shipping denied to every agent) was invisible to
> the entire suite. These rows mirror D1–D6 and add the two `execute_plan`-relationship rows that
> FR-006b's rule is actually about. "Other agent" here is the plan's **owner**, which for `stop_plan`
> is the *authorised* party — deliberately the inverse of `plan_correct`.

| # | Global ceiling | `jim` (plan owner) entry | PlanSupervisor entry | Boundary type | Effective for `jim` | Effective for PlanSupervisor | Traces to |
|---|---|---|---|---|---|---|---|
| D7 | `allow` | `allow` | `deny` | **The specified seed** (FR-006b, mirroring Jim's `execute_plan: allow`) | **`allow`** | `deny` | *No agent can start a plan it cannot stop* |
| D8 | `allow` | **`deny`** (seed override forgotten — `denyAllThenOverride` stamped it) | `deny` | **C3-02 — rev 2's actual shipped state** | **`deny` — US-8 AS2, FR-042/043, test #64 and Dataset F all dead on arrival** | `deny` | *No agent can start a plan it cannot stop* |
| D9 | **`ask`** (mirroring `execute_plan`'s ceiling) | `allow` | `deny` | **N14 — the ceiling trap** | **`ask`, not `allow`** — strictest-wins merges Jim down, making Dataset B11 unreachable. This is why FR-006 requires the ceiling be `allow` | `deny` | *No agent can start a plan it cannot stop* |
| D10 | `allow` | `ask` (Mia / Ava / Ray / the specialist tier, mirroring their `execute_plan: ask`) | `deny` | Same policy level as `execute_plan` | `ask` — a human answers; correct for a chat agent | `deny` | *No agent can start a plan it cannot stop* |
| D11 | `allow` | *(Worker: `execute_plan` absent, `stop_plan` **explicit `deny`**)* | `deny` | **FR-006b's one exception** | `deny` — the Worker's map is sparse, so an **absent** `stop_plan` would inherit the `allow` ceiling; the explicit `deny` is the `inspect_session` precedent at `core.go:489-496` | `deny` | *No agent can start a plan it cannot stop* |
| D12 | `allow` | *(newly created custom agent — neither tool named in `NewCustomAgentToolsCfg`)* | `deny` | Fully enumerated helper | `deny` for **both** `execute_plan` and `stop_plan` — consistent with the rule (neither named ⟹ both deny) | `deny` | *No agent can start a plan it cannot stop* |

#### Dataset E — supervision lifecycle (`Plan.supervision`)

> **Rev 2: this dataset replaces rev 1's migration inputs**, which are dead under the greenfield
> ruling. "Tick" throughout means `defaultPlanEngineTickInterval` = **30 s**
> (`pkg/agent/plan_engine.go:131`) — a package const, not a config key. `T` = the supervision turn
> timeout, 10 min (§11.1).

| # | `supervision.wake_at` | `supervision.attempts` | Elapsed since wake | Plan phase / signature | Boundary type | Expected on this tick | Traces to |
|---|---|---|---|---|---|---|---|
| E1 | unset | 0 | — | just parked | First entry to the parked phase | Wake issued; `wake_at` stamped; `attempts` → 1 | *A parked plan is re-woken after a restart* |
| E2 | set | 1 | 1 tick (30 s) | parked, signature unchanged | Well inside the deadline | **No** wake — deduped on `wake_at` | *Repeated ticks do not produce repeated supervision wakes* |
| E3 | set | 1 | 19 ticks (9.5 min) | parked, signature unchanged | Deadline − 1 tick | **No** wake | *Repeated ticks do not produce repeated supervision wakes* |
| E3b | set | 1 | **20 ticks (exactly 600 s)** | parked, signature unchanged | **The exact boundary** | **No** wake — the predicate is strict (`now > wake_at + timeout`), so equality does **not** fire. *(Rev 2's dataset tested 19 and 21 and omitted the one value a boundary dataset exists for; §11.1's "one tick short of 21 ticks" obscured it — r3 m3-03, m3-06.)* | *A supervision turn that emits nothing at all is detected by the deadline* |
| E4 | set | 1 | 21 ticks (10.5 min) | parked, signature unchanged | **Deadline + 1 tick** | Deadline fires; `attempts` → 2; wake re-issued; `wake_at` re-stamped | *A supervision turn that emits nothing at all is detected by the deadline* |
| E5 | set | 1 | 3 ticks | **phase now `dispatching`** (correction applied) | Turn succeeded | Deadline disarmed; `wake_at` and `attempts` cleared | *PlanSupervisor appends a missing step…* |
| E6 | set | 1 | 21 ticks | parked, **signature unchanged**, a correction was *rejected* in between | Rejected correction | `attempts` → 2; wake re-issued — a rejection mutates nothing, so this is indistinguishable from silence and is treated identically | *A rejected correction re-arms the supervision wake instead of stranding the plan* |
| E7 | set | ceiling − 1 | 21 ticks | parked, signature unchanged | Ceiling − 1 | `attempts` → ceiling; wake re-issued (the last one) | *Exhausting the supervision attempt ceiling terminates the plan…* |
| E8 | set | **ceiling** | 21 ticks | parked, signature unchanged | **Ceiling reached** | Plan → `failed(supervision_unavailable)`; Owner woken; **no** further wake | *Exhausting the supervision attempt ceiling terminates the plan…* |
| E9 | set | 1 | 21 ticks | parked, `judge_rounds == plan_judge_max_rounds` | Ceiling check precedence | Plan → `failed(judge_rounds_exhausted)` — the **unconditional round-ceiling check still runs first** and wins over the supervision deadline | *A parked plan at its round ceiling terminates instead of being re-woken* |
| E10 | set | 1 | 21 ticks | parked, idle-expiry budget exceeded | Two brakes race | Idle expiry wins: `failed(idle_expired)`. Supervision creates no immortal record | *A long-parked plan is still reaped by idle expiry* |
| E11 | set, `wake_error` populated | 1 | 1 tick | parked, notifier failing | Wake publish failed | Failure recorded on the plan and logged at ERROR; re-attempted on the next tick; bounded by the same ceiling | *A failed wake publish is recorded and retried* |
| E12 | set | 1 | — | **gateway restarted** mid-deadline | Restart inside the window | `wake_at` rehydrates from disk; the deadline is honoured from its original stamp, **not** re-armed — so a restart loop cannot reset the ceiling | *A parked plan is re-woken after a restart* |
| E13 | set | 1 | 21 ticks | parked, but plan **stopped** in between | Stop during supervision | No wake, no deadline action — the plan is no longer `running` | *Stopping a plan halts the supervisor working on it* |
| E14 | set | 1 | 3 ticks | **park → correction applied → `dispatching` → re-park**, `supervision.correction_rounds` was 1 before the re-park | **The per-field reset boundary** | On leaving the phase: `wake_at`, `wake_error` and `attempts` reset; `session_id` **retained** (overwritten only when the next supervision session is minted); **`correction_rounds` NOT reset — it reads 1, and 2 after the next correction.** On re-park, a fresh wake is issued. *(FR-050's rev-2 blanket reset made this row read 0 and broke six readers — r3 C3-03.)* | *The correction counter survives a park → dispatch → park cycle* |
| E15 | set | 1 | 21 ticks | **`plan_phase = stalled`**, no unmet signature | **The stall limb** (D-01) | Deadline fires exactly as for a parked plan: `attempts` → 2, wake re-issued, `wake_at` re-stamped. The "signature unchanged" limb of FR-021's predicate is **vacuous** for a stall (no signature is ever set), so the operative limbs are phase ∈ the supervision-eligible set, `wake_at` set, and the deadline elapsed | *A stalled plan's supervision deadline is armed, counted and bounded* |
| E16 | set | **ceiling** | 21 ticks | `plan_phase = stalled` | Stall at the ceiling | Plan → `failed(supervision_unavailable)`; Owner woken; no further wake — identical treatment to E8 | *A stalled plan's supervision deadline is armed, counted and bounded* |
| E17 | set | 1 | — | parked; correction applied while the supervision turn is **still running** | **The m3-07 window** | `session_id` is **retained**, so a `stop_plan` issued in this window still names a live turn and still halts it (E34). Rev 2 cleared `session_id` on leaving the phase, which erased the handle in exactly this window | *Stopping a plan halts the supervisor working on it* |

#### Dataset F — `stop_plan` authority and cascade

| # | Caller | Plan `owner_agent_id` | Plan state | Supervision turn in flight? | Boundary type | Expected | Traces to |
|---|---|---|---|---|---|---|---|
| F1 | `jim` | `jim` | `running` | no | Happy path | Stopped; `failed(stopped_by_user)`; actor recorded as `jim` | *A plan's owner agent can stop the plan it started* |
| F2 | `jim` | `jim` | `running` | **yes, and blocked so it is observably live** | **The containment case** | Stopped; **the supervision turn terminates** — the cancel is *claimed* against a live turn whose `transcriptSessionID` equals `supervision.session_id`, and the turn returns within a bounded time. Asserting only that the id joined the fan-out set is **not** sufficient (operator ruling 5, r3 C3-05) | *Stopping a plan halts the supervisor working on it* |
| F3 | `mycustomagent` | `jim` | `running` | no | Non-owner | Denied | *An agent cannot stop a plan it does not own* |
| F4 | `mycustomagent` | — | plan absent | — | Non-owner, missing entity | Denied, **identical body to F3** | *An agent cannot stop a plan it does not own* |
| F5 | `jim` | `jim` | `done` | no | Already terminal | Rejected with the existing state error; no cascade, no second terminal write | *A plan's owner agent can stop the plan it started* |
| F6 | `jim` | `jim` | `running` | yes, **and a `plan_correct` lands in the same instant** | Race | `planDecisionMu` serialises; if stop wins, the correction is rejected against a non-running plan; if the correction wins, the stop still cascades | *A correction and a stop are serialised* |
| F7 | a human via `POST /plans/{id}/stop` | `jim` | `running` | yes | The SPA ■ button | Same cascade as F2 — the tool and the REST route share `StopPlan`, so containment is identical from either surface | *Stopping a plan halts the supervisor working on it* |
| F8 | `jim` | `jim` | `running` | no, and one member's cancel **store write fails** | Partial fan-out | The plan still transitions; the aggregate error names the orphaned member (existing `aggregateMemberCancelErrors` behaviour, preserved) | *A plan's owner agent can stop the plan it started* |

### 13.4 Regression Test Requirements

This feature **modifies existing functionality**. The following behaviours MUST be preserved.

| Existing behaviour | Existing test / anchor | New regression test needed | Why |
|---|---|---|---|
| `JudgeRounds` has exactly one incrementer | `TestAttemptsVsRounds_DistinctBrakes` (named at `plan_engine.go:1494`) | **No** — must continue to pass unchanged | FR-034 explicitly adds no second writer |
| The F2 unchanged-signature gate skips a re-judge | `plan_engine.go:1293-1301` + existing plan-engine tests | **Yes** — `TestBootReconcile_ParkedPlanBurnsNoJudgeRound` (#38) | FR-023 adds a boot case adjacent to this gate |
| The unconditional round-ceiling check runs before the F2 gate | `plan_engine.go:1261-1264`, `:1288` | **Yes** — `TestBootReconcile_ExhaustedParkedPlanTerminates` (#39) | The new boot case must not bypass it |
| `PhaseStalled` never masks the parked phase | `plan_engine.go:1225-1230` precedence guard; `pkg/plan/plan.go:247-251` | **Yes** — *The parked phase and the stalled phase never co-occur* | Both wakes now route to PlanSupervisor |
| `dod` and `owner_agent_id` are frozen on a non-draft plan (409) | `pkg/gateway/rest_plans.go:717-736` | **Yes** — `TestCorrectionRequest_HasNoDoDOrOwnerField` (#14) | The freeze is handler-local; the new tool path does not inherit it (C10) |
| `Bounds` is deliberately **not** frozen | `pkg/gateway/rest_plans.go:715-716` comment | **No** — must continue to pass unchanged | Under FR-034 the exemption is more useful, not less |
| The engine may write non-draft plans through `updateLocked` | 18 of 21 non-test writers | **No** — must continue to pass unchanged | A blanket store-level guard would brick the engine |
| Workers are rejected as chat targets | existing worker-guard tests | **Yes** — extended by #47–#50 | The guards gain a System-Agent clause; the worker clause must survive |
| `firstChatTargetAgentID` already excludes System Agents | `pkg/gateway/rest.go:1057-1067` | **No** — must continue to pass unchanged | Only the explicit-id rejection changes |
| `NewVerifierSession` mints the Judge's session | `pkg/session/unified.go:544` | **Yes** — `TestVerifierSessionAndSupervisionWakeStillWork` (#51) | A guard placed too deep would break the Judge |
| The `wakeOwner` bus path performs no authorization | `pkg/agent/async_notifier.go:249-251` | **Yes** — #51 | Guards must sit at the gateway chokepoints only |
| Every seeded agent's policy map exactly matches `allStaticToolNames` | `pkg/coreagent/constructor_seed_test.go:214-222` | **No** — must continue to pass unchanged | Adding `plan_correct` must not create gaps or extras |
| `buildKnownBuiltinToolNames` matches the coreagent catalog | `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` | **No** — must continue to pass unchanged | Guards the 4-literal sync |
| `AppendCorrection` is transactional and replay-idempotent | `pkg/agent/plan_engine_correction_test.go` (10 cases), `conformance_design_test.go` (3) | **Yes** — all must be re-run after the FR-030 pairing rule lands | The pairing rule changes `validateCorrection`'s accept set |
| The plans list renders without a schema error | `PlansFilterBand.test.tsx`, `WorkspaceGraphTab.test.tsx`, `planStateColors.test.ts`, `ws.new-frames-validation.test.ts` | **Yes** — all four assert the *"no in-app action"* copy and the old phase literal; both change | S13 + S9 |
| A paused `needs_input` session with a populated ownership scope is exempt from the sweep | `pkg/agent/boot_sweep_test.go` (14 ownership cases) | **Yes** — the same 14 cases re-run against the renamed `ScopeKind` field (S9 row 4) | `boot_sweep.go:295-296` is the **only live** protection for a parked `needs_input` session; an emptied key sweeps it permanently. The rename must not change the gate's behaviour |
| `StopPlan`'s cancel fan-out covers member, verifier and owner sessions | existing stop tests; `plan_engine.go:1666` onward | **Yes** — `TestStopPlan_CancelsSupervisionSession` (#63) | FR-044 adds one entry to the set; the existing four must survive |
| `aggregateMemberCancelErrors` reports a partial stop rather than an unqualified success | `plan_engine.go`'s `aggregateMemberCancelErrors` | **No** — must continue to pass unchanged | Dataset F8 |
| `reconstructCorrections` replays the superseded set at boot | `plan_engine.go:3105-3127` | **Yes** — `TestReconstructCorrections_CorruptLogIsSurfacedNotSwallowed` (#46d) | The *clean* replay must keep working while the *corrupt* case stops being swallowed |
| Agent ids are server-minted UUIDs and `type: system` is uncreatable | `createAgent` (`rest.go:2145`, id at `:2378`) | **Yes** — `TestAgentIDCannotBeSetToASystemAgentID` (#66) | FR-009's exact-identity gate has no floor without it |
| `processSystemMessage` still drops genuinely internal traffic (`cli`, `subagent`) without running a turn | `loop.go:6022-6031`; `pkg/constants/channels.go:6-10` | **Yes** — a sibling assertion inside #31c | FR-012c changes how **plan** wakes reach a turn. It MUST NOT widen the internal-channel guard generally, or every `cli`/`subagent` completion starts spurious turns. The guard's behaviour for the other two channels is unchanged. **Rev 4:** the guard is not touched *at all* — the fix changes what `wakeOwner` puts into the event, not what the guard does (D-09) |
| **`bus.InboundMessage.Channel` is `"system"` and `processSystemMessage` is reached by matching on it** | `async_notifier.go:271`; `loop.go:5515`; the entry rejection at `loop.go:5992-5997` | **Yes** — a sibling assertion inside #31c: a plan wake still arrives with `msg.Channel == "system"` | **NEW in rev 4, and this row exists because two independent analyses of N15 proposed changing this value.** It is the routing key, not the defect. Changing it makes `processSystemMessage` unreachable *and* makes it error on entry. See **N15.1** |
| `pkg/tools/task.go`'s `webchat` exclusion for **Task** origins | `pkg/tools/task.go:541` | **No** — must continue to pass unchanged | FR-012d(3) deliberately does **not** apply this exclusion to Plans (E39, N15.3). The Task behaviour is correct for Tasks and must not be "harmonised" in either direction |
| **`AsyncNotifier.Notify` rejects an empty or partial destination (FR-N7)** | `pkg/agent/async_notifier.go:226-233` | **Yes** — `TestNotify_StillRejectsEmptyDestination`, a sibling assertion inside #31d | **NEW in rev 4.** This guard is now load-bearing for a case it was not written for: it is the second line of defence behind FR-012d(4)'s construction guard, and the two are deliberately the same predicate. Removing it re-opens **N16 candidate 1**, whose symptom — a wake parsed as originating on `"cli"` and dropped — is indistinguishable in the logs from correct behaviour (E42) |
| **`wakeOwnerAttemptsExhausted`'s `"system"`/`"task:<id>"` fallback for Tasks** | `pkg/agent/task_executor.go:1066-1069` | **No** — out of scope, and **not** fixed here | **Recorded rather than fixed, deliberately.** A Task with no origin falls back to `Channel: "system"`, which N15's chain shows is dropped by the internal-channel guard — so the Task goal-loop's attempts-exhausted wake has the **same defect** as the plan wake, one layer over. It is out of scope: no requirement in this spec depends on it, which is D-08's stated test, and Tasks are `list-jobs-spec.md`/ADR-053 surface. **Filed as RISK-16** so the next author does not read this spec's fix as having covered it |
| `ensureOwnerSessionLocked` is idempotent — it returns early when `OwnerSessionID` is already set | `plan_engine.go:2470-2472` | **Yes** — a sibling assertion inside #31c | FR-016c changes what it mints **and** adds call sites (`:1571`, `:1610`, `:1742`). The early return is the only thing stopping a second call minting a second session and orphaning the first, which would leave `StopPlan` cancelling a session no turn is bound to |
| `Plan.OwnerSessionID` is **not renamed** | S9 row 3 / D-02 | **No** — must continue to pass unchanged | FR-016c changes the field's **value shape**, not its name. An implementer who reads "the owner session becomes real" as licence to rename it re-opens the D-02 collision with `supervision.session_id` |
| `wakeOwner`'s existing four non-supervision call sites keep their content, `sourceKind` and target | `plan_engine.go:1571`, `:1610`, `:1742` (and `:1254`'s content) | **Yes** — #32, #32b, R1.11 | FR-016b changes `wakeOwner`'s **signature**. A signature change touching five call sites is the classic place a wake silently loses its target |
| Verifier sessions are cancelled by `StopPlan` and their turns actually stop | `plan_engine.go:1695`, `:1704`; `verifier_registry.go:189-203` | **No** — must continue to pass unchanged | **This is the leg of the fan-out that already works** (N13) and the pattern FR-016b copies. Breaking it while adding the supervision leg would be a net loss |
| `plan.Patch`'s existing fields apply independently | `pkg/plan/store.go:232-271`, `updateLocked` `:287` | **Yes** — #57c | Five fields are added to a struct whose whole contract is "only non-nil fields are written" |
| `AppendCorrection` rejects a plan outside the supervision-eligible phase set | `plan_engine.go:2591-2593` | **Yes** — A31 / #36e's negative half | D-01 **widens** the gate from one phase to two. It must not become "any phase" |

#### Regression Dataset R1 — behaviours that must survive the change

> **Rev 2: the pre-rename fixture rows are gone.** Under the greenfield ruling there is no upgrade
> path to regression-test — old records are expected not to load. Rows R1.1, R1.4, R1.5 and R1.6
> tested exactly that path and are withdrawn. What remains are behaviours that must survive the
> *code* changes, tested against freshly created state.

| # | Input | Previous behaviour | Must still produce | Traces to |
|---|---|---|---|---|
| R1.2 | Plan at `dispatching` | Loads; dispatches | Identical | Regression: plan store |
| R1.3 | Plan in `done` (frozen) | Loads; immutable | Identical | Regression: plan store |
| R1.4b | Freshly created lifecycle record, paused `needs_input`, `ScopeKind` populated | Preserved by the sweep | Preserved by the sweep **under the renamed field** | Regression: boot sweep — **the live gate**, S9 row 4 |
| R1.5b | Freshly created lifecycle record, paused, `SupervisedPlanID` populated **by a test fixture** | Preserved by exemption (b) | Identical — **but marked synthetic**: nothing writes this field in production (S9 row 5), so exemption (b) never fires on a real install. The row guards the rename, not a live behaviour | Regression: boot sweep — **synthetic fixture** |
| R1.6b | Lifecycle record already terminal | Immutable; further same-generation persist rejected | Identical | Regression: lifecycle store |
| R1.7 | Intent-log JSONL with `append` and `supersede` revisions | Replayed at boot into the superseded set | Identical **on a clean log**; a corrupt log is now surfaced and fails the plan closed rather than silently un-superseding members | Regression: intent log |
| R1.8 | Agent config with an operator-edited Judge SOUL/Model | Preserved across boots | Preserved, and PlanSupervisor's likewise | Regression: system-agent seed |
| R1.9 | Agent config where an operator set a System Agent as default | Accepted (the existing hole) | **Rejected** on write; `GetDefaultAgent` never returns it | Regression: default agent — **intentional behaviour change** |
| R1.10 | A running plan stopped from the SPA ■ button | Member, verifier and owner sessions cancelled; plan `failed(stopped_by_user)` | Identical, **plus** the supervision session | Regression: stop fan-out |
| R1.11 | A plan reaching `done` | Owner woken with the closing-synthesis commission | **Identical** — explicitly unchanged. This row exists because re-targeting that wake was proposed and would have silently retired success notification | Regression: success path |

---

## 14. Functional Requirements

> Numbering is grouped by theme with deliberate gaps between groups. Every FR defined here appears
> in the traceability matrix (§17).

### Group A — the PlanSupervisor agent

- **FR-001**: The system MUST add a System Agent with id `plansupervisor` in **all four** required
  places: an `IDPlanSupervisor` constant, a `PlanSupervisor() *CoreAgent` constructor, the
  `SystemAgents()` slice (`pkg/coreagent/core.go:159`), and the `systemAgentIDs` map (`:146`).
  Membership is **not** derived between the last two — omitting either leaves `IsSystemAgentID`
  and the roster disagreeing.
- **FR-002**: The system MUST seed PlanSupervisor with `Type=system`, `Locked=true`,
  `Default=false`, `MemoryEnabled=false`, and MUST re-enforce all five on **every** boot, repairing
  `MemoryEnabled` in both directions (nil→false and true→false) exactly as the Judge is repaired.
  Model, Provider and SOUL.md MUST be preserved as operator-editable.
- **FR-003**: PlanSupervisor MUST NOT be usable as a chat target, a channel routing target, a
  delegation target, a plan `owner_agent_id`, or the starred default agent.
  **`PUT /api/v1/agents/{id}` MUST return `403` for any request body that sets `enabled: false` on
  an agent whose `Locked` flag is true**, which is what `updateAgentTools`'s existing guard already
  returns for a `Locked` agent (`pkg/gateway/rest.go:6789-6793`, *"agent %q is locked and cannot be
  modified"*). *(Rev 2 said "MUST reject a disable attempt the way the Judge's is rejected" without
  naming the field or the status code, neither of which appears anywhere else in the spec — r3
  m3-05. If no `enabled` field exists on the agent wire type at implementation time, this clause is
  satisfied vacuously and MUST be recorded as such rather than a field invented for it.)*
- **FR-004**: The system MUST register an agent-facing tool `plan_correct` exposing verbs `append`,
  `supersede`, `targeted_retry` and **`abandon`**, whose `Execute` builds a `CorrectionCaller` from
  `tools.ToolAgentID(ctx)` and `tools.ToolTranscriptSessionID(ctx)` and calls `AppendCorrection`.
  Its **parameter schema is specified field by field in FR-046** and MUST NOT be invented at
  implementation time.
  Because `pkg/tools` cannot import `pkg/agent`, the correction types MUST move to `pkg/plan` and be
  re-exported from `pkg/agent` as type aliases (the `IntentEdge` precedent at
  `plan_engine.go:2423`), and the engine call MUST be injected as a func-value setter that
  **fails closed** when unwired — the discipline `pkg/tools/run_task.go:131-134` and
  `pkg/tools/plan.go:239-250` both document verbatim.
- **FR-005**: The system MUST define a `PlanSupervisorDefaultRubric` const — **whose full text is
  Appendix A (§27)**, not left to the implementer — and materialise it into PlanSupervisor's
  `SOUL.md`. `SeedConfig` MUST NOT perform filesystem I/O, and no path may overwrite an existing
  non-empty SOUL.
  **The materialisation is the gateway-side eager seed alone** (a sibling of `seedJudgeEagerSoul`,
  `pkg/gateway/gateway.go:906-918`, called at `:1373`).
  **Rev 2 drops rev 1's "lazy backstop" requirement.** The backstop it named to mirror does not
  generalise: `ensureVerifierSoul` (`pkg/agent/verifier_adjudication.go:198`) returns immediately
  unless `agentInst.ID == string(coreagent.IDJudge)`, and its **only** call site is
  `verifier_adjudication.go:860`, inside the Judge's verifier dispatch. PlanSupervisor is woken over
  the **bus** into an ordinary agent turn and never reaches that file, so there is no analogous hook
  to mirror — a backstop would need a **new** call site in the ordinary instance-construction path.
  **Accepted consequence, stated rather than hidden:** if an operator deletes
  `plansupervisor/SOUL.md` while the gateway is running, it stays empty until the next restart. That
  is the same exposure every other seeded-once artefact has, and adding a construction-path hook for
  it is not justified by this feature. If a future change wants it, the call site is
  `pkg/agent/instance.go`'s agent construction, gated on the agent id.
- **FR-006**: The system MUST add **both** `plan_correct` and `stop_plan` to the catalog surfaces —
  **three literals plus one derived set** — **before** any seed references either, because
  `validateOverrideKeys` **panics** on an unknown override key:

  | # | Surface | Kind |
  |---|---|---|
  | 1 | `allStaticToolNames` (`pkg/coreagent/core.go:295-333`) | literal — **and its `:274-279` doc comment, which is already stale by two names and MUST be corrected in the same edit** |
  | 2 | `pkg/config/defaults.go`'s global `ToolPolicies` map, **and `pkg/config/defaults_test.go:92`'s `wantToolCount`** | literal |
  | 3 | the builtin metadata registry (`tools.GeneralBuiltinMetadata()` and friends) | literal |
  | — | `buildKnownBuiltinToolNames` (`pkg/gateway/gateway.go:715-745`) | **derived** — it iterates the metadata from surface 3 and unions four hardcoded ADR-052 names, so registering the tool is sufficient. Adding the name here is an idempotent no-op |

  *(Rev 2 called all four "catalog literals" and instructed the implementer to hand-edit the derived
  one — cross-spec m3. Test #2 is renamed accordingly.)*

  Each global ceiling MUST be **`allow`**, for two independent reasons that must both be recorded
  because either alone is enough to break the feature:
  1. `deny` or `ask` would overrule an agent's own grant under strictest-wins merging (N1) — this is
    the `plan_correct` case, and it is what a `deny` ceiling did to the Judge's `inspect_session`.
  2. **For `stop_plan` specifically, an `ask` ceiling would silently defeat FR-006b.** `execute_plan`'s
    ceiling is `ask` (`defaults.go:415-417`), so Jim's own seeded `allow` merges down to `ask`
    (**N14**). If `stop_plan` mirrored that ceiling "for symmetry", Dataset **B11** — the owner
    stopping their own plan — would resolve `ask`, not `allow`, and the containment story would
    depend on a human answering a prompt. FR-006b mirrors `execute_plan` at the **per-agent** level
    and deliberately does **not** mirror it at the ceiling.

  Omitting an entry is forbidden by Constraint #6 and `defaults_test.go`'s full-enumeration
  assertion. The catalog's size MUST be asserted mechanically
  (`len(allStaticToolNames) == len(cfg.Sandbox.ToolPolicies)`, test #2) rather than quoted as a
  literal in prose or in a test.
  **Cross-spec note:** `list-jobs-spec.md` adds `list_jobs` to the same map in the same release,
  taking `wantToolCount` from 83 to **86**. Whoever lands first SHOULD replace the hardcoded
  `wantToolCount` with test #2's mechanical assertion so the second lander never touches the number.
  See §18's landing-order note.
- **FR-006b (the `stop_plan` seeding rule — operator ruling 6)**: **Wherever `execute_plan` is
  seeded, `stop_plan` MUST be seeded in the same map at the same literal policy value.**

  This is stated as a **rule over the seed**, not a list of agents, so that it survives a new agent
  being added. Its consequences on today's tree — recorded as *verification*, not as the
  requirement — are: the specialist tier (`core.go:512-514`), Ava (`:620-622`), Mia (`:663-665`) and
  Ray (`:721-723`) each seed `execute_plan: ask` and therefore gain `stop_plan: ask`; Jim
  (`:814-816`) seeds `allow` and therefore gains `stop_plan: allow` `[FACT — all five sites verified
  2026-07-27]`.

  Two exceptions, each with its stated reason:
  1. **A seed map that deliberately omits `execute_plan` because it is *sparse*** — today exactly one,
     the Worker's `tightenGlobalCeiling` map (`core.go:485-488`) — MUST instead carry an **explicit
     `stop_plan: deny`**. An absent key in a sparse map inherits the **global ceiling**, which for
     `stop_plan` is `allow` (FR-006), so absence would silently grant it. This is precisely the trap
     `inspect_session` carries an explicit `deny` for at `core.go:489-496`, and the comment there is
     the precedent to follow. (A Worker can never be a plan's `owner_agent_id`, so the grant would be
     unusable rather than dangerous — but "unusable grant" is not a posture this codebase ships,
     per Constraint #6.)
  2. **PlanSupervisor** holds neither tool. Its `systemAgentSeed` case names `plan_correct` and
     nothing else, so `denyAllThenOverride` gives it `stop_plan: deny` — consistent with the rule
     (`execute_plan` is not named either) and required by FR-008 and FR-043: the adjudicator
     corrects, the owner contains.

  **The requirement is over the seed literal; the success criterion is over the resolved policy**
  (SC-004b), and they are deliberately different assertions because for `execute_plan` the two
  already disagree in the tree today (N14). The property SC-004b states is the one that matters:
  **no agent can start a plan it cannot stop.**

  *Why this exists:* rev 2 added `stop_plan` to the catalog with an `allow` ceiling and named it in
  **no** seed override map. `denyAllThenOverride` stamps an explicit `deny` for every catalog name at
  all **nine** of its call sites, and a per-agent `deny` beats a global `allow` — so on a fresh
  install every agent, Jim included, resolved `stop_plan: deny`, and US-8 AS2, FR-042, FR-043, test
  #64, Dataset F1/F2/F5/F6/F8 and half of SC-020 were dead on arrival. Dataset D covered
  `plan_correct` only, so nothing in the suite detected it. This is the **exact N1b trap the spec
  documents at length for its first tool and then fell into for its second** (r3 C3-02, cross-spec
  C2a).
- **FR-007**: The system MUST give PlanSupervisor an explicit, non-nil skill allowlist
  `["plan"]`, seeded on the System-Agent path and re-enforced every boot. A `nil` allowlist means
  **unrestricted** and is forbidden for this agent (N3).
- **FR-008**: PlanSupervisor's `systemAgentSeed` case MUST **explicitly name `plan_correct` in its
  override map**. This is not belt-and-braces: `denyAllThenOverride` stamps an explicit `deny` for
  every catalog name first, and a per-agent `deny` beats the global `allow` under strictest-wins —
  so an unnamed tool ships **denied to PlanSupervisor itself** and the correction loop is dead on
  arrival on every fresh install (N1b).
  **The resolved policy MUST be `allow` for exactly one name — `plan_correct` — and `deny` for
  every other name in `allStaticToolNames`.** Stated as a complement, not as a list, so a tool added
  to the catalog later can never silently land in PlanSupervisor's allow set.
  **Rev 2 removes three grants rev 1 made**, each for its own reason:
  - `inspect_session` — **structurally inert for this agent.** `pkg/config/defaults.go:408-414`
    records that the real protection is *"the engine-set, fail-closed verifier-session scope lock
    (`tools.VerifierSessionScopeAllows`): a turn without the scope is refused every session id
    regardless of policy."* PlanSupervisor is not a verifier and is not dispatched through the
    verifier path, so it never holds the scope and the grant can never succeed. It was copied from
    the Judge's seed case. Granting it widens the seeded surface of the most privileged new agent for
    zero capability and puts a dead entry into SC-004's assertion set.
  - `read_file` / `list_directory` — **live, unbounded, and unjustified.** §11.1 enumerates
    PlanSupervisor's inputs — the plan record, the judge's per-criterion verdict, member outcomes,
    `plan/SKILL.md`, its SOUL — **none of which require filesystem access**; every one arrives in the
    wake or the context builder. Meanwhile the spec never stated PlanSupervisor's `Workspace`, and
    `seedSystemAgents` does **not** re-enforce that field (it re-enforces `Type`, `Locked`,
    `Default`, `Name` and the tool policies), so the reach would be unspecified at seed time and
    operator-mutable thereafter — which on an unconfined workspace includes `$OMNIPUS_HOME` and its
    `master.key`, `credentials.json` and `config.json`. NFR-2's guarantee is about *write* paths and
    says nothing about read reach.
  If a future change needs either grant, it MUST first state PlanSupervisor's `Workspace`, add that
  field to FR-002's re-enforced set, and assert the *effective reach* (a denied read outside the
  workspace), not merely the policy string.
  **`stop_plan` is denied to PlanSupervisor too** — it is one of the "every other name" the
  complement covers, and FR-043 states the reason: the adjudicator corrects, the owner contains.
  FR-006b's rule reaches the same answer independently (PlanSupervisor holds no `execute_plan`).

  **PlanSupervisor is deliberately roster-blind (D-04, cross-spec C3).** It holds no way to
  enumerate the plans it supervises — no `list_jobs`, no plan-list tool, no roster. Three verified
  facts compose into this and are recorded so the gap reads as a decision rather than an oversight:
  (i) a System Agent can never be a plan's `owner_agent_id`, because `IsChatTarget()` is
  `!IsWorker() && !IsSystem()` (`pkg/config/config.go:1052`) and it guards **both**
  `Plan.OwnerAgentID` write paths; (ii) the supervision session is engine-minted (FR-016b), never
  `delegate`-minted, so it carries no `ParentAgentID` and produces no `session.LifecycleRecord` row;
  (iii) therefore **neither** of `list-jobs-spec.md`'s two ownership predicates can ever match
  PlanSupervisor. **The engine's `supervision.wake_at` deadline is the only liveness control, and it
  is deliberately the engine's** — an adjudicator that could see it had three parked plans would have
  a reason to act outside the wake it was given, which is the opposite of "one correction per wake".
  A future change that wants to grant it MUST deliberately amend this requirement and test #4 to
  `len(allowed) == 2`; **the complement-complete assertion failing is the guard working.**

  Separately, the system MUST treat the **engine identity gate** (FR-009), not the policy layer, as
  the primary control against non-holder agents, because an agent persisted **before** the tool name
  existed has no per-agent entry and resolves `allow` from the global ceiling (N2). Both properties
  MUST be asserted against `ResolveEffectivePolicy`, never against the seed literal.
- **FR-009**: `requireOwner` MUST admit PlanSupervisor by **exact agent identity** (never by
  `Type == system`, so a future System Agent does not silently inherit correction rights) and MUST
  continue to deny every other caller including the plan's own `owner_agent_id`.
  The gate's soundness rests on `plansupervisor` being unclaimable, which **is** the case and is
  pinned by FR-049 (agent ids are server-minted UUIDs — N12).
  The existing `OwnerSessionID` clause MUST NOT deny PlanSupervisor, and the requirement is stated
  **without** depending on an unverified premise about session sharing:
  > `requireOwner` MUST return early for the PlanSupervisor identity **before** clause 3 is
  > evaluated, whatever session that turn happens to run in.
  Rev 1 justified this with the claim that PlanSupervisor's session *"is not `plan:<id>`"*, and
  simultaneously asserted in FR-016 that both actors *"share the synthetic `plan:<id>` `ChatID`"* —
  two statements that cannot both be casually true, neither of which was verified. The early return
  is correct and necessary under **either** answer, so rev 2 states it unconditionally and moves the
  session question to FR-016, where it is now resolved by construction rather than by assumption.
  The spec further records that clause 3 is **not** a security control: `OwnerSessionID` is the
  derived, guessable `"plan:" + p.ID`.
- **FR-010**: **Every** denial on the correction path MUST be indistinguishable, and the
  indistinguishability MUST cover the plan-load failure, not only `requireOwner`'s three branches.
  Specifically:
  1. All three `requireOwner` branches (`plan_engine.go:2754-2770`) MUST return one identical
     wrapped `ErrCorrectionNotOwner` message. Today they return three different strings
     (*"caller agent identity is empty"*, a bare *"plan %q"*, *"caller session does not match…"*)
     `[FACT — verified]`.
  2. **The message MUST NOT embed the plan id.** Today every branch wraps
     `fmt.Errorf("%w: plan %q", ErrCorrectionNotOwner, planID)`, so responses for *different* plan
     ids can never be identical — which made rev 1's SC-002 unsatisfiable regardless of anything
     else. The id stays in the server-side WARN, as `:2761` already does.
  3. **When the caller is not PlanSupervisor, a plan-load failure MUST be normalised to the same
     response.** This is the load-bearing addition: `AppendCorrection` calls
     `pe.planStore.Get(planID)` **before** `requireOwner` (`:2578-2585`) `[FACT — verified]`, so a
     nonexistent plan returns a *store error* — a different error class — and that is a perfect
     existence oracle sitting in front of the gate the whole story exists to build. Implement as an
     identity precheck **before** `planStore.Get` (it needs no plan state — it compares
     `caller.AgentID` to a constant), so the oracle closes without reordering the transactional body.
  4. **When the caller *is* PlanSupervisor, the real not-found error MUST be returned**, so the
     adjudicator can tell "I named the wrong plan" from "I am not permitted" — a distinction FR-019's
     honest-exit accounting depends on.

### Group B — authority, wakes and outcome delivery

- **FR-011**: The plan's **Owner** — meaning its `owner_agent_id` for delivery and its `owner`
  attribution for the human notice — MUST receive outcome notifications and MAY stop / cancel /
  resume, and MUST have **no** adjudication or correction role. `owner_agent_id` remains the terminus
  for plan-scoped `owner_required` questions (ADR-053 D2 preserved).
- **FR-012**: Wake routing MUST split by **"who must decide whether to correct"**, not by
  "decision vs outcome". **Exactly two** of `wakeOwner`'s five call sites move:

  | Site | Function | Kind | Target |
  |---|---|---|---|
  | `:1254` | `surfaceStallIfAny` | `plan_stalled` | **PlanSupervisor** — moved |
  | `:1542` | `applyJudgeRoundOutcomeLocked` (UNMET) | `plan_judge_unmet` | **PlanSupervisor** — moved |
  | `:1571` | `synthesizeAndComplete` (**DoD MET**) | `plan_judge_met` | **Owner — UNCHANGED** |
  | `:1610` | `failPlanLocked` | `plan_<reason>` | Owner — unchanged |
  | `:1742` | `StopPlan` | `plan_stopped_by_user` | Owner — unchanged |

  **The MET synthesis commission MUST NOT be re-targeted** (this reverses rev 1 and ADR-055 D5; see
  C15). Two independent reasons:
  1. **Correctness.** `synthesizeAndComplete` (`:1561-1582`) holds the *only* wake on the success
     path — it wakes `p.OwnerAgentID` at `:1571` and then sets `StateDone` at `:1578`
     `[FACT — verified]`. `failPlanLocked` fires only on failure and `StopPlan` only on user stop, so
     re-targeting `:1571` would leave a plan that **succeeds** notifying nobody: neither the owner
     agent nor — because FR-014(b) hangs off the same delivery — the human who authored it. That is
     the exact gap US-4 exists to close, reintroduced inside the story written to prevent it.
     Nothing wires a PlanSupervisor synthesis back: FR-008 denies it every write tool and
     `plan_correct` carries no synthesis field.
  2. **Merits.** The Owner is the right author of a closing synthesis. It is the agent accountable
     to the requester and the only one holding the requester's conversational context.
     PlanSupervisor adjudicates failure; it does not narrate success.

  The `:1542` message text MUST stop saying *"awaiting **your** correction"*. The `:1254` stall wake
  MUST ask for a **stall diagnosis**, not a DoD verdict, and MUST be preceded by FR-025's stall-note
  clearing.
- **FR-012b**: `synthesizeAndComplete`'s wake target and ordering MUST be pinned by a regression test
  (#32b) and by regression row R1.11, because re-targeting it is a proposed change that looks
  harmless and silently retires success notification.
- **FR-012c (the wake must actually start a turn — N15, D-08)**: A plan wake MUST **dispatch an agent
  turn**. It is not sufficient for `wakeOwner` to publish an event that a downstream guard discards.

  **Verified that today it does not, for all five call sites** `[FACT — verified verbatim end to end
  2026-07-27]`: `wakeOwner` hardcodes `Channel: "system"` (`plan_engine.go:2103`); `Notify` composes
  the bus `ChatID` as `fmt.Sprintf("%s:%s", event.Channel, event.ChatID)` = `"system:plan:<id>"`
  (`async_notifier.go:277`); `processSystemMessage` — the **sole** consumer (`loop.go:2924`, `:5516`)
  — parses `originChannel = "system"` back out (`loop.go:6006-6009`) and returns at `:6030` because
  `system` is one of three internal channels (`pkg/constants/channels.go:6-10`). **No turn is
  dispatched.** See N15.

  **The properties (unchanged from rev 3 — these are what SC-025 asserts):**
  1. A supervision wake MUST result in an agent turn running for `plansupervisor`.
  2. That turn's `transcriptSessionID` MUST equal the plan's `supervision.session_id` (FR-016b), so
     it is persisted to the right transcript and is cancellable by `RequestCancelForSession`.
  3. The Owner wake sites (`:1571`, `:1610`, `:1742`) MUST likewise result in a turn for
     `owner_agent_id`, because US-4 AS1 and SC-010 measure delivery to that agent and FR-014(b)'s
     human notice hangs off the same delivery.
  4. The fix MUST NOT widen `processSystemMessage`'s internal-channel guard for `cli` or `subagent`
     traffic. Those two channels are correctly suppressed; only the plan path is wrong.

  **The mechanism (NEW in rev 4 — D-09/D-11; rev 3 left this to the implementer and that is the gap
  operator ruling 9 closes).** The five wake sites fork into the two families FR-012/S4 already
  splits them into, because the two families have opposite delivery requirements:

  | | **(A) Supervision wakes** — `:1254` stall, `:1542` UNMET | **(B) Owner wakes** — `:1571` MET synthesis, `:1610` failure, `:1742` stop |
  |---|---|---|
  | Target agent | `plansupervisor` | `p.OwnerAgentID` |
  | Seam | **Direct dispatch.** `PlanEngine` → `AgentLoop`, no bus, no notifier. `PlanEngine` already holds `agentLoop *AgentLoop` (`plan_engine.go:188`) `[FACT — verified]`, so no new seam is created. | **The notifier/bus**, unchanged in shape — `wakeOwner` → `Notify` → `PublishInbound` → `processSystemMessage`. |
  | Origin (`Channel`/`ChatID`) | **None.** No outbound is published. | `p.SourceChannel` / `p.SourceChatID` (FR-012d), replacing the hardcoded `"system"` / `"plan:<id>"`. |
  | `SendResponse` | **`false`** — required by FR-016 and H8: the adjudicator's deliberation MUST NOT reach the Owner's conversation. | **`true`** — required by US-4 AS1 / SC-010 / H9: the outcome must reach the human who asked. |
  | Transcript session | `supervision.session_id` (FR-016b) | `p.OwnerSessionID` (FR-016c — **must be real**, see below) |
  | Fallback when no origin | n/a (never has one) | FR-012d(4) / D-10 — dispatch directly with `SendResponse: false`; the human surface is the notification. |

  **Why (A) does not use the bus.** `processSystemMessage` hardcodes `SendResponse: true` in its
  single `runAgentLoop` call (`loop.go:6134-6144`, the field at `:6141`) `[FACT — verified
  2026-07-28]` and offers no suppression knob, so any origin channel it
  is given receives the supervision turn's output. Giving it the plan's real origin leaks
  PlanSupervisor's reasoning into the Owner's chat, which FR-016 and H8 forbid outright. Rejected
  alternative and why, in D-11.

  **The in-repo pattern (A) copies already exists and already works**: the Judge's verifier dispatch
  mints a real session (`newVerifierSessionChatID` → `NewVerifierSession`,
  `verifier_adjudication.go:522-542`), registers its id (`:893-894`) and dispatches with
  `TranscriptSessionID` set to that id (`processTaskDirect` → `loop.go:4723`, which sets
  `SendResponse: false`) — which is why the verifier leg of `StopPlan`'s fan-out is the one leg that
  actually cancels a turn (N13). An implementation MUST NOT reuse the internal-channel bus path
  without first demonstrating, by test, that a turn runs.

  **⚠ `bus.InboundMessage.Channel` stays `"system"`.** See **N15.1**. It is the routing key
  `loop.go:5515` matches to reach `processSystemMessage`, which rejects any other value at entry
  (`:5992-5997`). Only the *event's* origin channel changes. Two prior analyses of this defect
  proposed changing the bus channel; both were wrong.

  **SC-025 asserts a turn ran.** A fake notifier capturing `AsyncNotifyEvent`s records the call three
  hops upstream of the drop and is explicitly **not** evidence of delivery (§11.2). Neither is
  `supervision.wake_at` having been written, nor `Notify` having returned `nil`.

  *Scope note:* this is fixed here rather than deferred, unlike N14's ceiling (O12), on a stated
  test — **defer only what no requirement in this spec depends on.** US-4 AS1, SC-010, FR-012,
  FR-019 and the entire supervision loop depend on a plan wake reaching an agent. See **D-08**, and
  see **N13's rev-4 scope decision** for the half of O11 that fails the same test.
- **FR-012d (the plan's origin — NEW in rev 4, D-09/D-10)**: `Plan` MUST carry the chat origin its
  wakes deliver to. This is the root cause of N15: `wakeOwner` hardcoded a synthetic internal
  destination because there was no real one to pass (N15.2).

  1. **Contract first (Constraint #8).** `contracts/components/schemas/Plan.yaml` gains two optional
     properties, mirroring `Task`'s `source_channel` / `source_chat_id`
     (`pkg/task/task.go:307-309`):

     | Property | Type | Semantics |
     |---|---|---|
     | `source_channel` | `string`, **optional** (`omitempty`) | The channel the plan was created from — the channel a plan wake delivers its outcome back to. Absent when the plan has no chat origin. |
     | `source_chat_id` | `string`, **optional** (`omitempty`) | The chat within that channel. Absent when the plan has no chat origin. |

     **Both are optional and MUST stay optional (operator ruling 10).** `Task`'s pair is `omitempty`
     on both the Go struct (`pkg/task/task.go:307-309`) and the wire; `Plan`'s is identical. A plan
     with no chat origin is a **legitimate, expected state**, not a degraded one — do not add them to
     `required`, do not mint a synthetic origin, and do not treat absence as an error anywhere.

     `Plan.yaml` is `additionalProperties: false` (`:28`), so an un-regenerated artifact fails
     **totally** at the SPA edge rather than per-row. The schema change, `scripts/gen-contracts.sh`
     and the committed artifacts MUST land in **one** commit, and it MUST be **folded into §18
     step 1** (as sub-item **(h)**) rather than adding a second `gen-contracts` run. Both fields are
     server-set; they MUST NOT be accepted from `PlanCreateRequest` or any PATCH body.
  2. **Populated on both write paths, at creation.** Neither may be left to the other:
     - `pkg/tools/plan.go` (`create_plan`, the plan struct literal at `:282-290`) — from the
       tool-call context, `tools.ToolChannel(ctx)` / `tools.ToolChatID(ctx)`, exactly as
       `pkg/tools/task.go:541-543` does.
     - `pkg/gateway/rest_plans.go` (`POST /api/v1/plans`, the plan struct literal at `:543-549`) —
       there is no chat context on a REST create, so both stay empty. That is the no-origin case,
       and it is a **decided** outcome (clause 4), not an oversight.
     Both are set **only at creation** and are immutable thereafter, like `Owner`/`CreatedBy`.
  3. **`webchat` is recorded, not excluded.** The task precedent skips it
     (`pkg/tools/task.go:541`); a plan MUST NOT. The reason the exclusion exists for tasks does not
     hold for plans — see **N15.3** and **E39**. An implementer copying the task line verbatim
     silently re-creates the undeliverable case for every plan started from the SPA chat, which is
     the most common way a plan is created.
  4. **The no-origin behaviour is decided, and the dead end is unreachable by construction
     (D-10 option (b), operator ruling 10, N16).** `wakeOwner` MUST evaluate
     `SourceChannel != "" && SourceChatID != ""` **before** constructing an `AsyncNotifyEvent`, and
     MUST NOT construct a chat-origin wake when that predicate is false. The predicate is
     deliberately **the same one** `AsyncNotifier.Notify` enforces at FR-N7
     (`async_notifier.go:226-233`), so the two can never disagree. When it is false, the Owner wake:
     - **still dispatches a turn** for `owner_agent_id`, directly (the FR-012c(A) seam) with
       `SendResponse: false`, bound to `p.OwnerSessionID` (FR-016c), so the closing synthesis is
       authored and **persisted**. *"No chat to deliver to" MUST NOT mean "no turn ran"* — that is
       today's defect wearing a decision's clothes;
     - **MUST NOT** substitute any other conversation. There is no fallback to the owner agent's
       default route, last-active channel (`RecordLastChannel`), or the plan creator's most recent
       session. Rejected in D-10: it drops a plan's outcome into an unrelated conversation, the same
       cross-context leak class H8 exists to prevent;
     - **MUST NOT** be attempted-and-failed. Passing the empty origin to `Notify` and treating the
       FR-N7 rejection as benign is D-10's rejected option (a): it feeds a **healthy** plan into
       FR-024's `wake_error` ladder and FR-022's attempt ceiling, terminating it
       `failed(supervision_unavailable)` — a loud, false diagnosis strictly worse than a silent
       drop. See **N16**;
     - relies on the **notification** (`plan_completed` / `plan_failed` with `plan_id` click-through,
       FR-014/FR-017) as the human-facing surface. For a plan created through the Plans UI that is
       already the surface its creator is watching, and H9 already requires it.

     **Supervision wakes are unaffected by a missing origin, and MUST remain so.** Family A never
     reads `SourceChannel`/`SourceChatID` (FR-012c(A), D-11), so an origin-less plan reaches
     PlanSupervisor by exactly the same path as any other plan. An origin-less plan MUST NOT become a
     second silent-drop case — this is asserted directly by **test #31d**, not inferred from A/B
     symmetry.

     This clause is what makes a UI-created plan's outcome recoverable, and it is asserted by
     **test #31d** and **SC-026**.
  5. **Observability.** A wake for a plan with no origin MUST log at INFO with
     `{plan_id, owner_agent_id, reason: "no_chat_origin"}` — distinguishable from a wake that failed.
     It MUST NOT be recorded as a `wake_error` (FR-024), MUST NOT increment the supervision attempt
     count (FR-022), and MUST NOT contribute to `failed_reason = supervision_unavailable`. Nothing
     failed; inflating the error count escalates a plan that is behaving exactly as designed (N16).
- **FR-013**: The system MUST route outcome delivery on the **two fields that already exist**, and
  MUST NOT add a principal-kind discriminator. **`Plan.OwnerAgentID` is the authoritative delivery
  address**: it is `required` (`pkg/plan/plan.go:363`, validated at `:485-486`), is set to an agent
  id on both write paths, and is guarded on both by an `IsChatTarget()` validator that rejects
  workers and System Agents — so it can never be unaddressable. `Plan.Owner` / `Plan.CreatedBy` are
  **creator attribution only** and MUST NOT be used as a wake address.
  This **supersedes ADR-055 D2 and D5**, whose fork rested on the false premise that `Plan.Owner`
  carries a dual-kind principal; `contracts/components/schemas/Plan.yaml:244-250` documents it as
  *"Username of the user who created this plan"*, `readOnly`. The spec SHOULD still correct that
  description to note the tool path writes an agent id, but no behaviour depends on it.
- **FR-014**: Outcome delivery MUST be **layered, not forked**:
  (a) **always** wake `owner_agent_id` over the existing bus path — this is the guaranteed delivery
  and cannot silently no-op; and
  (b) **additionally**, when `Plan.Owner` resolves to a configured `Gateway.Users` entry, create a
  notification for that username with `plan_id` and push it over the WS.
  The system MUST NOT offer `Plan.Owner` to the notification store unless that lookup succeeds, so a
  file keyed on an agent id — which `ListForUser` would never read — is never written. A failure of
  (b) MUST be logged at ERROR but MUST NOT be treated as a lost outcome, because (a) already
  succeeded.

  **(b)'s trigger point and content on the `done` path MUST be specified, not inherited** (r3 m3-09):
  - **Trigger**: **after the `StateDone` write succeeds** (`plan_engine.go:1578`), **not** on the
    `:1571` wake. Rev 2 said (b) *"hangs off the same delivery"* as the bus wake, which on the success
    path is the closing-synthesis commission at `:1571` — fired while the plan is still
    `synthesizing`, and **before** the terminal write, whose failure only logs ERROR and returns. Read
    literally, rev 2 notified `alice` that her plan had finished at a moment when it had not, and left
    a window in which she was told a plan finished whose terminal write then failed.
  - **Content**: the same title/body/severity template FR-017 specifies per terminal outcome. The
    notification MUST NOT carry the wake's text — that text is a **work commission addressed to an
    agent** (*"Please write a closing synthesis summarizing the outcome for the requester"*) and is
    not human-facing copy.
  - The same rule applies on the failure paths: the notification is created after the terminal state
    is persisted.
- **FR-015**: The system MUST provide an injection seam by which the plan engine can create a
  notification, declared as an interface **owned by `pkg/agent`** so no import edge
  `pkg/agent` → `pkg/notifications` is created, and wired in `pkg/gateway` where `notifStore`
  already exists.
- **FR-016**: The system MUST **resolve, not assume**, the wake destination for the split, and MUST
  keep the two actors' transcripts separate.
  `wakeOwner` builds `ChatID: "plan:" + planID` internally (`:2104`) with `TranscriptSessionID`
  **left unset**, and `asyncNotifierImpl.Notify` publishes
  `ChatID: fmt.Sprintf("%s:%s", event.Channel, event.ChatID)` = `"system:plan:<id>"` with
  `AsyncTranscriptSessionID: ""` (`pkg/agent/async_notifier.go:277,286`) — leaving
  `processSystemMessage` to resolve the session `[FACT — verified]`. **Rev 1 asserted the two actors
  share `plan:<id>` and left the consequence undetermined.** Rev 2 removes the ambiguity by
  construction rather than by tracing:
  - The supervision wake MUST carry an **explicit** supervision session id, recorded on the plan as
    `supervision.session_id`, so PlanSupervisor's adjudication turns are written to their **own**
    transcript and never into the Owner's persistent `plan:<id>` session.
  - Rationale, which is a requirement and not a preference: an adjudicator's reasoning landing in
    the Owner's plan transcript is both an information-disclosure surface (either party's turn can
    read the other's) and context pollution of the session `ensureOwnerSessionLocked` mints at
    `:2469-2474` — the same session FR-118's boot-sweep exemption is keyed on.
  - Having an explicit id is also what makes FR-044's containment possible: `StopPlan` cannot cancel
    a session it cannot name.
  - FR-009's early return remains required regardless, because clause 3's behaviour must not depend
    on this resolution. **N13 adds a second, independent reason it is required:** once
    `ensureOwnerSessionLocked` fires, `p.OwnerSessionID == "plan:<id>"`, and clause 3 then demands
    `caller.SessionID == "plan:<id>"` — an id no real session ever has, so clause 3 would deny
    **every** caller. The early return is the only thing standing between the feature and a gate that
    denies unconditionally.
  `wakeOwner` gains a `transcriptSessionID` parameter — see FR-016b. *(Rev 2 closed with "if a second
  destination is later required, `wakeOwner` gains a destination parameter". It is required now, and
  for a different reason than a second destination.)*
- **FR-016b (mint a real supervision session — operator ruling 5, closes C3-05)**: The engine MUST
  **create a real, store-backed session** for the supervision turn and record its id as
  `supervision.session_id`.

  | Question | Requirement |
  |---|---|
  | **Who mints it** | The engine, on entry to a supervision-eligible phase, before the first wake of that park. |
  | **What it looks like** | Whatever the session store mints — an opaque `session_<ULID>`. It MUST NOT be a **derived or composed** string. |
  | **Precedent to follow** | `session.NewVerifierSession` (`pkg/session/unified.go:544`) — the in-repo pattern for an engine-minted, isolated, store-backed session, used by the Judge at `verifier_adjudication.go:529`. `StopPlan` already cancels those successfully. |
  | **Stability** | **One session per park.** Re-wakes within the same park (attempts 1…`supervision_max_attempts`) share it, so `StopPlan` can cancel whichever attempt is in flight. A new park mints a new one. |
  | **Threading** | `wakeOwner` MUST take it as a parameter and set `AsyncNotifyEvent.TranscriptSessionID`, which `Notify` already forwards to `bus.InboundMessage.AsyncTranscriptSessionID` (`async_notifier.go:285`). Today `wakeOwner`'s literal (`plan_engine.go:2102-2108`) omits the field entirely — unlike `task_executor.go:1076-1082`, the pattern it claims to mirror. |
  | **Lifetime** | **Not cleared** when the plan leaves the phase; overwritten when the next supervision session is minted (FR-050, E17, E34). |

  **A derived id is forbidden, and this is the load-bearing clause.** `processSystemMessage` honours
  a forwarded id **only if `ResolveSessionStore` resolves it** — `loop.go:6072-6085`, resolving by
  `GetMeta(sessionID)` succeeding against a real store — and cancellation matches on exactly that
  value (`GetActiveTurnHookForSession`, `turn.go:444`, exact string equality on
  `ts.transcriptSessionID`). So a synthetic id such as `"plansupervisor:plan:" + p.ID` is dropped,
  the turn runs with `transcriptSessionID == ""`, and `RequestCancelForSession` finds nothing to
  cancel — while `cancelSessions` discards the `fired` bool (`plan_engine.go:1825`) and
  `plan_stop_test.go`'s `fakeSessionCanceller` returns `(true, nil)`, so **every specified test
  passes anyway**. That is the false-green this requirement exists to remove.

  **FR-016b is necessary but not sufficient on its own** — the internal-channel drop (FR-012c, N15)
  happens *before* the transcript resolution is ever reached. Both are required for the kill switch
  to work.
- **FR-016c (FR-016b's mechanism, applied to the Owner's session — NEW in rev 4, operator ruling 11,
  closes N13 and O11)**: `ensureOwnerSessionLocked` (`plan_engine.go:2469-2481`) MUST record a
  **real, store-backed** session id on `Plan.OwnerSessionID`, and MUST be called before the **first**
  Owner wake of a plan's life — not only on the UNMET path.

  > **This is FR-016b, not a parallel requirement (operator ruling 11).** Same defect, same
  > mechanism, same precedent, same PR: *a field is set, the thing it names was never created, and
  > the test asserts the label rather than the outcome.* FR-016b mints the supervision session;
  > FR-016c mints the owner session; there is **one** minting helper and **one** implementation unit.
  > They are stated separately only because the two sessions have different **lifetimes** (one per
  > park vs one per plan) and different **consumers** — collapsing them into one requirement would
  > force one stability rule onto two fields that legitimately differ. Neither may ship without the
  > other: FR-016b alone leaves the Owner wake with nowhere to persist; FR-016c alone leaves the kill
  > switch cancelling nothing.
  >
  > **Why it is load-bearing rather than cosmetic.** Operator ruling 1's kill switch — *stopping a
  > plan stops everything working on it* — is realised through `StopPlan`'s cancel fan-out, and the
  > owner-session leg of that fan-out has **always** been a no-op because the id it cancels
  > (`"plan:" + id`) names nothing (N13). The three uses of that string in `pkg/` are a verifier
  > registry key, a bus `ChatID` and the stored plan field — **none creates a session**
  > `[FACT — verified 2026-07-28]`.

  | Question | Requirement |
  |---|---|
  | **What changes** | `sessionID := "plan:" + p.ID` (`:2473`) is replaced by a store-minted session, following the same `session.NewVerifierSession` precedent (`pkg/session/unified.go:544`) FR-016b uses. A **derived or composed** id is forbidden, for the identical reason (`ResolveSessionStore` resolves by `GetMeta`, `loop.go:6072-6085`). |
  | **When it is called** | Today: exactly one caller, `plan_engine.go:1541`, immediately before the UNMET wake `[FACT — verified 2026-07-28]`. Required: also before the Owner wakes at `:1571`, `:1610` and `:1742`, so the ordinary success path has a session to persist into. A plan that reaches `done` without ever parking currently has `OwnerSessionID == ""`. |
  | **Stability** | One owner session per plan, for the plan's lifetime. Unlike `supervision.session_id` (one per *park*, FR-016b), this one is minted once and never re-minted — it is the Owner's continuous context for the plan. |
  | **What it is used for** | `AsyncNotifyEvent.TranscriptSessionID` on every Owner wake (FR-012c(B)), so the closing synthesis is persisted even when the plan has no chat origin (FR-012d(4)) or the origin channel's client is gone. |
  | **Separation** | It MUST NOT be the supervision session. FR-016 requires the two actors' transcripts to be disjoint; two distinct minted sessions is how that is realised, and it is asserted by #63b's sibling assertion. |
  | **Wire** | **None.** `owner_session_id` is already on `Plan.yaml`; only the value *shape* changes, and greenfield (operator ruling 3) removes any persisted-data concern. **S9 row 3 / D-02 stand: the field keeps its name.** |

  **Scope boundary — what this requirement does NOT do.** It does not change `requireOwner`'s
  clause-3 semantics (`plan_engine.go:2765-2768`), the FR-118 boot-sweep exemption predicate, or
  anything about `LifecycleRecord.OwnsPlanID` — nothing in production writes that field (S9 row 5),
  so boot-sweep exemption (b) stays dead and this requirement does not revive it. FR-009's early
  return for PlanSupervisor remains required regardless.

  **The kill-switch consequence, asserted rather than assumed.** `StopPlan`'s owner-session cancel
  leg is a no-op today because the id it cancels names nothing (N13 consequence 1). Once the session
  is real, that leg cancels a real turn — which is what operator ruling 1 always intended. **The
  assertion MUST be that the Owner turn terminated**, never that an id reached a canceller: `StopPlan`
  discards `cancelSessions`' `fired` bool (`plan_engine.go:1825`) and `plan_stop_test.go`'s
  `fakeSessionCanceller` (`:28-38`) returns `(true, nil)` regardless, so a set-membership assertion is
  green against a control that cancels nothing — the exact C3-05 false-green. Pinned by **SC-020
  limb (e)** and **test #63c**.
- **FR-017**: The system MUST widen the notification contract **before** any code emits a plan
  notification, in **three** places, not two:
  1. `contracts/components/schemas/Notification.yaml` — the `type` enum gains **exactly these two
     values**, and a `plan_id` property is added:

     | New `type` value | Emitted when | `severity` | Title | Body |
     |---|---|---|---|---|
     | `plan_completed` | the plan reaches `done`, **after** the `StateDone` write succeeds (FR-014) | `info` | `Plan finished: <plan title>` | `Your plan "<plan title>" completed successfully.` |
     | `plan_failed` | the plan reaches `failed`, after the terminal write succeeds | `warning` | `Plan failed: <plan title>` | one sentence selected by `failed_reason`: `judge_rounds_exhausted` → `Your plan "<title>" ran out of review rounds before its Definition of Done was met.`; `dod_unreachable` → `Your plan "<title>" was stopped because its Definition of Done cannot be reached.`; `supervision_unavailable` → `Your plan "<title>" could not be reviewed automatically and was stopped.`; `stopped_by_user` → `Your plan "<title>" was stopped.`; `idle_expired` → `Your plan "<title>" was closed after being idle.`; `budget_exhausted` → `Your plan "<title>" ran out of its configured budget.` |

     Both values carry `plan_id`. **The values, titles, bodies and severities are specified here
     rather than left to the implementer** because rev 2 required *"the `type` enum gains the new
     values"* and never named one — leaving §11.3, §18 step 1, test #55 and SC-014 all measuring an
     unspecified thing, in the one place where an unknown value **empties the entire notification
     centre** rather than degrading (r3 M3-06). This is the same discipline rev 2 correctly applied
     to FR-063's SPA copy and did not apply here;
  2. `contracts/asyncapi.yaml`'s `NotificationFrame` (`:2557`, `:2570`) — an **independent
     hand-maintained copy** whose event-class field is **`notification_type`**, not `type`;
  3. `src/store/notifications.ts:46-52` — the hand-written normaliser that maps
     `notification_type` → `type`, which must learn the new values or the WS path drops them.

  Both generated surfaces are regenerated and committed atomically. **In the same commit**,
  `Notification.yaml:22`'s description MUST be corrected: it currently claims *"Extensible; consumers
  must tolerate unknown values"* while the enum is closed under `additionalProperties: false` and
  neither consumer tolerates one — an actively false statement that will mislead the next author.
  The ordering is non-negotiable because the failure is **total, not per-row**:
  `src/lib/api.ts:838-847` `safeParse`s the **whole** response body, so one unknown `type` empties
  the entire notification centre; the WS path (`src/lib/ws.ts:240-254`) instead drops the frame
  silently. The same discipline applies to the two new `failed_reason` values (S15), whose enum at
  `Plan.yaml:140-153` is closed under the same `additionalProperties: false`.
- **FR-018**: Plan notifications MUST be **navigable**: `NotificationPanel`'s click-through MUST
  route on `plan_id` (today it routes on `sessionId` then `scheduleId` only, `NotificationPanel.tsx:68-71`,
  with no fallback when both are empty).
  **Coalescing is deliberately NOT changed.** Rev 1 required the store's coalescing key to cover
  `plan_id`; that solves a duplicate this feature cannot produce. FR-014(b) creates **at most one**
  notification per plan, at the plan's single terminal event, so there is nothing to coalesce —
  and the change would touch shared machinery (`pkg/notifications/store.go:192-210`) on the critical
  path of the `schedule_failed` notifications that genuinely do coalesce. If a future change emits
  more than one notification per plan, coalescing becomes a real requirement at that point.
- **FR-019**: If a supervision turn produces nothing usable, the plan MUST stay in its
  supervision-eligible phase and the wake MUST be re-issued up to FR-022's ceiling. **The Owner MUST
  NOT be woken while attempts remain** — there is nothing to tell it yet. **Only on exhausting the
  ceiling** MUST the plan terminate with `failed_reason = supervision_unavailable` and the plan's
  `owner_agent_id` be woken with an adjudication-unavailable handover (plus the FR-014(b) human notice
  when applicable). No other agent may inherit adjudication.
  *(The two-outcome split is explicit because rev 2's US-4 AS6 and its BDD scenario asserted the plan
  **stays parked** **and** the Owner is woken — a state this requirement never defines, and one test
  #36 could not produce — r3 M3-03.)*
  **"Produces nothing usable" MUST be defined as an engine-observable predicate**, because the wake
  is fire-and-forget and no seam reports the turn's outcome back (N8):
  > On the first tick at which `now > supervision.wake_at + supervision_turn_timeout`, the plan is
  > still in a **supervision-eligible phase** (FR-029), and its unmet-terminal signature is unchanged.

  For a **stall** wake the signature limb is **vacuous** — no unmet signature is ever set on the
  stall path — so the operative limbs are the phase, `wake_at` being set, and the deadline having
  elapsed. This is stated because an implementer reading "signature unchanged" as "a signature
  exists" would make the predicate unreachable for every stall (Dataset E15).
  Rev 1 defined it as *"any of: a provider error; the timeout; or neither a `plan_correct` call nor
  an honest-exit conclusion"* — **none of which `PlanEngine` can observe**. A provider error never
  reaches the engine, and "honest-exit conclusion" was never an observable artefact. Rev 2 deletes
  the provider-error limb (it is subsumed: a turn that errored produces no correction) and makes the
  conclusion observable by giving it a verb (`abandon`, FR-046). One predicate covers all of it.
  The realistic failure rev 1 invited was a **false green**: a test injecting a provider error into a
  synchronous double passes, while in production the provider goes down, every parked plan stays
  parked and nobody is told — the exact failure US-5 exists to prevent.
- **FR-020**: The system MUST NOT add member-level manual retry for any actor.
  *(Negative requirement — verified by a conformance assertion that no member-retry route, tool verb
  or SPA control exists, not by a test that `targeted_retry` works. See §17.)*

### Group C — resilience

- **FR-021 (the observation seam)**: On issuing a supervision wake the engine MUST stamp
  `supervision.wake_at` with the current time and increment `supervision.attempts`, both persisted on
  the plan record. On every subsequent tick, if the plan is in a **supervision-eligible phase**
  (FR-029 — `awaiting_supervision` **or** `stalled`) with `supervision.wake_at` set and
  `now > supervision.wake_at + supervision_turn_timeout` and the unmet-terminal signature is
  unchanged, the engine MUST treat the turn as having produced nothing and execute FR-022.
  The comparison MUST be **strict** (`>`), so at exactly `wake_at + timeout` — which is exactly 20
  ticks — it does not fire; the first firing tick is 21 (Dataset E3b).
  `supervision_turn_timeout` MUST be a new `config.PlanningConfig` field
  (`supervision_turn_timeout_seconds`, **default 600**), per-plan overridable via `PlanBounds`. It
  MUST NOT reuse the 10 s `wakeOwner` notify timeout, which bounds a bus publish, not an LLM turn.
  Applying a correction MUST clear `supervision.wake_at` and reset `supervision.attempts` to 0,
  which disarms the deadline. **It MUST NOT touch `supervision.correction_rounds`**, which is
  cumulative for the life of the plan and is incremented — never reset — by the same correction
  (FR-034, FR-050). *(Stated explicitly because rev 2's FR-050 reset rule did exactly that and broke
  six readers — r3 C3-03.)*
  **The deadline MUST be honoured from its original stamp across a restart** (`wake_at` rehydrates
  from disk and is not re-armed), so a restart loop cannot reset the ceiling.
  *Why a deadline and not a callback:* the wake is fire-and-forget and `pkg/agent`'s turn path
  reports nothing into `PlanEngine` (N8). A callback would create a new coupling for a signal the
  engine can already infer — it owns the plan record, and whether the record moved is a complete
  proxy for whether the turn produced anything. A deadline is also the shape every other brake in
  this engine already has (round ceiling, idle expiry).
- **FR-022 (the post-turn state machine)**: When **FR-021's predicate fires** — and on no other
  trigger — the engine MUST:
  (a) if `supervision.attempts < supervision_max_attempts`, re-issue the supervision wake (stamping a
  fresh `wake_at` and incrementing `attempts`), **without** waking the Owner;
  (b) otherwise, terminate the plan with `failed_reason = supervision_unavailable` and a handover
  distinct from every message in FR-035, and wake the Owner (FR-019).
  `supervision_max_attempts` MUST be a new `config.PlanningConfig` field
  (`supervision_max_attempts`, **default 3**), per-plan overridable via `PlanBounds`.
  **`supervision.attempts` increments at most once per supervision wake**, never once per tool call.

  > **Rev 3 deletes rev 2's second trigger (D-05).** Rev 2 read *"when FR-021's predicate fires, **or
  > a `plan_correct` call is rejected by validation**"*. A single LLM turn can emit several tool
  > calls, so each rejection would increment `attempts` and **one turn could exhaust the 3-attempt
  > ceiling and terminate the plan `supervision_unavailable`** — a plan killed by its supervisor's
  > typos inside its first turn. It also contradicted the spec's own Dataset **E6**, which handles a
  > rejection through the **deadline** (*"indistinguishable from silence and treated identically"*),
  > and it mismatched NFR-1(b)'s unit (unproductive **turns**) against its own unit (rejected
  > **calls**) — r3 M3-07. A rejected correction mutates nothing (FR-030 rejects before any write)
  > and charges no round, so it leaves the plan in exactly the state silence leaves it in, and the
  > deadline detects it identically. The alternative — keeping the trigger with an "at most once per
  > turn" clamp — was rejected because the engine cannot observe a turn boundary (N8).
  **This ceiling MUST NOT be `PlanJudgeMaxRounds`.** A rejected correction increments nothing —
  `applyJudgeRoundOutcomeLocked:1495` is the sole incrementer and `validateCorrection` returns before
  it is reached — so the judge-round budget provably cannot bound the quantity being bounded here.
  *Why this requirement exists at all:* without it, the wake fires once per park, a rejected
  correction mutates nothing (FR-030 rejects before any write) and charges no round, so the plan
  returns to **precisely the state that produced no new wake**. One bad tool call — a bare
  `supersede`, a `targeted_retry` of a `done` member, a malformed call, or no call at all — strands
  the plan until idle expiry. SC-001 would then hold only when the LLM emits a valid correction on
  its first and only attempt.
- **FR-023**: `processPlan`'s phase switch MUST gain a case for the parked phase that re-issues the
  supervision wake **idempotently**, **deduped on `supervision.wake_at`** — the wake receipt — and
  **not** on the persisted unmet-terminal signature.
  *The distinction is load-bearing.* `surfaceStallIfAny` dedups on a **persisted side effect of the
  previous wake** (`if p.HandoverText == note && p.EffectivePlanPhase() == plan.PhaseStalled { return }`,
  `:1246-1248`) — a wake receipt. `Plan.LastUnmetTerminalSignature` (`pkg/plan/plan.go:392`) is set
  once at UNMET (`:1518`) and cleared only by a correction (`:2625`) `[FACT — verified]`; it carries
  **no** information about whether a wake was delivered and stays set on every subsequent tick, so a
  case keyed on `phase == parked && signature != ""` fires **every tick**. Rev 1 required exactly
  that and called it idempotent.
  The re-wake MUST NOT consume a judge round and MUST NOT clear or rewrite the persisted signature
  (spec FR-193 `[P1]` preserved; `bootReconcile` already rehydrates it at `:3198-3199`). The
  unconditional round-ceiling check MUST still run first, so an exhausted parked plan terminates
  rather than re-waking (Dataset E9).
  `supervision.wake_at` MUST be cleared when the plan leaves the **supervision-eligible phase set**
  (FR-029), so a later re-park re-wakes. It MUST NOT clear `supervision.correction_rounds` or
  `supervision.session_id` — see FR-050's per-field table.
- **FR-024**: A nil notifier MUST be a **`Start` precondition error**, scoped to `Start` only so
  in-package tests may still construct a `*PlanEngine` struct literal with fake fields. A failed wake
  publish MUST be recorded on the plan as `supervision.wake_error` and re-attempted on a later tick
  rather than logged at WARN and dropped. **Both reverse documented contracts** — `wakeOwner`'s
  *"Best-effort: a notify failure is logged, never escalated"* (`plan_engine.go:2093-2095`) and
  `Start`'s existing precondition list which covers planStore/taskStore/dispatcher/judge but
  deliberately not the notifier (`:550-552`) — and the reversal is deliberate: without it, "never
  silently stalls" has no enforcement.
  **The retry MUST be bounded.** It shares FR-022's `supervision_max_attempts` ceiling; a
  permanently-failing notifier therefore writes at most that many ERROR lines and then terminates the
  plan `supervision_unavailable`, rather than retrying every tick until idle expiry. There is **no
  backoff curve** (O7) — one attempt per tick up to the ceiling.
  A failed `ensureOwnerSessionLocked` persist MUST likewise be surfaced, since an empty
  `OwnerSessionID` forfeits the spec FR-118 boot-sweep exemption.
- **FR-025**: On entering `awaiting_supervision` the engine MUST clear any stall note carried from a
  prior stall (`HandoverText` prefixed with `stallHandoverNotePrefix`).
  *Why:* `surfaceStallIfAny`'s note-clearing branch (`:1234-1241`) sits **behind** the parked-phase
  guard at `:1225-1230` `[FACT — verified]`, so once a plan parks, a stall note it carried in is
  never cleared. §11.1 lists the plan record as PlanSupervisor's primary input, and FR-005's rubric
  must discriminate a stall wake from an UNMET wake — feeding it a stale stall diagnosis alongside a
  DoD-unmet wake is the input most likely to produce the wrong verb, which is the failure RISK-9 says
  the prompt is the only control for. E21's rationale is corrected accordingly: the guard delivers
  phase exclusivity, not state exclusivity.
- **FR-026**: The engine MUST expose a gauge of plans currently at `awaiting_supervision` (US-8
  AS7, §20). It is the single number an operator needs at 3 AM and is cheap — a count over the plans
  already walked each tick. **It MUST have its own test, `TestSupervisionGauge_CountsParkedPlans`
  (#71).** *(Rev 2 traced this FR to "#53 (sibling assertion)" —
  `TestIdleExpiry_ReapsLongParkedPlan`, which reaps a plan and asserts nothing about a count — so the
  number the runbook opens with had no test at all, while §17's completeness check claimed otherwise.
  #53 is owned by FR-028 alone — r3 M3-11.)*
- **FR-027**: The plan-loop tick interval MUST be documented wherever this spec measures in ticks. It
  is `defaultPlanEngineTickInterval` = **30 s** (`pkg/agent/plan_engine.go:131`), a package const and
  **not** a config key. Every "within one tick" / "on a later tick" statement in this spec (SC-008,
  FR-021, FR-022, FR-024) is denominated in that unit. If the interval ever becomes configurable, the
  criteria that measure against it MUST be restated in wall-clock terms.
- **FR-028**: Supervision MUST NOT create an immortal plan record: a plan parked at
  `awaiting_supervision` past its idle-expiry budget MUST still be reaped by the existing idle-expiry
  path. *(This FR exists so test #53 has an owner — rev 1 left that test in zero matrix rows.)*
- **FR-029 (the supervision-eligible phase set — D-01, closes C3-01)**: The spec defines one named
  set and every supervision requirement is stated over it:

  > **supervision-eligible phase set** := { `awaiting_supervision`, `stalled` }

  Three consequences, each a MUST:
  1. **`AppendCorrection`'s phase gate MUST accept either.** Today it is
     `if p.EffectivePlanPhase() != plan.PhaseAwaitingOwnerCorrection { return … }`
     (`plan_engine.go:2591-2593`) `[FACT — verified verbatim]`. It becomes membership in the set.
     It MUST NOT become "any phase" — a plan at `dispatching` or `judging` is still rejected
     (E1, Dataset A31).
  2. **FR-021, FR-022 and FR-023's predicates MUST be stated over the set**, not over
     `awaiting_supervision` alone. So a stall wake arms `supervision.wake_at`, its silence increments
     `supervision.attempts`, and exhausting `supervision_max_attempts` terminates the plan
     `failed(supervision_unavailable)` — identically to a parked plan (Dataset E15, E16).
  3. **A correction applied to a `stalled` plan MUST clear the stall note** (`HandoverText` prefixed
     with `stallHandoverNotePrefix`) as part of the same transactional body that returns the plan to
     `dispatching`, for the same reason FR-025 clears it on entry to the parked phase: §11.1 lists the
     plan record as PlanSupervisor's primary input, and a stale stall diagnosis is the input most
     likely to produce the wrong verb.

  **The two phases remain disjoint, and that invariant is unchanged.** `surfaceStallIfAny` still
  returns early while the parked phase holds (`:1225-1230`) and still writes `PhaseStalled` (`:1248`);
  the BDD scenario *"The parked phase and the stalled phase never co-occur"*, test #59 and the §13.4
  regression row all continue to pass. Widening a **gate** to accept either member of a set does not
  make the set's members co-occur.

  **The re-wake for a stalled plan MUST go through FR-022's supervision path, not through
  `surfaceStallIfAny`.** `surfaceStallIfAny` dedups on its own persisted side effect
  (`if p.HandoverText == note && p.EffectivePlanPhase() == plan.PhaseStalled { return }`,
  `:1246-1248`), which is a *first-wake* guard, not a deadline. Routing the re-wake through it would
  either fight that guard or require mutating `HandoverText` to defeat it.

  *Why this rather than dropping the `:1254` re-target (the rejected alternative, D-01):* FR-011
  gives the plan's Owner **no** correction role. Leaving the stall wake on the Owner therefore leaves
  a stalled plan with **no** corrector at all — its only exits are Stop and idle expiry, which is
  precisely the gap §1 exists to close — and would require deleting US-1 AS5, the stall BDD scenario
  and the rubric's STALLED branch. Rev 2 shipped neither option: it re-targeted the wake to an
  adjudicator that the phase gate rejected 100% of the time, so the stall limb had a wake, a rubric
  branch, a user story and a test, and **no execution path** (r3 C3-01).

### Group D — integrity, budget and audit

- **FR-030**: `validateCorrection`'s `CorrectionSupersede` case MUST additionally require
  `len(req.TailMembers) > 0`. A `supersede` with no replacement work MUST be rejected before any
  mutation. **This is verified feasible**: `AppendCorrection` sets `rec.Members = req.TailMembers`
  unconditionally (`plan_engine.go:2621`) and `buildCorrectionApplyFunc` creates them
  verb-independently, so pairing composes atomically inside one `CorrectionRequest`. The ADR's
  alternative disjunct — *"or a `targeted_retry` of the superseded member"* — is **impossible by
  construction** and MUST NOT be implemented: `validateMemberRef` requires `StatusDone` for supersede
  and `StatusFailed` for targeted_retry, and a member cannot be both.
- **FR-030b**: A `supersede`'s replacement work MUST **inherit the superseded member's acceptance
  criteria**. The predicate is stated exactly, because rev 2 left all three of its parts undefined
  (r3 M3-05) and a validation control is only as strong as the comparison it performs:

  > Let `S` be the superseded member's `Criteria` (`[]task.AcceptanceCriterion`,
  > `pkg/task/task.go:282`, the type at `pkg/task/criterion.go:169`) and `R` the **union** of
  > `tail_members[].criteria` in this request. `validateCorrection` MUST reject the `supersede`
  > unless **every** element of `S` is present in `R`, where presence is decided by
  > **`AcceptanceCriterion` id when both sides carry one, and otherwise by exact equality of the
  > (`kind`, `expression`) pair**. Comparison MUST NOT be on rendered or free text.

  Three cases follow and MUST each be covered by a dataset row and by test #67:

  | Case | Result | Row |
  |---|---|---|
  | `R` carries **none** of `S` | **Reject** | A24 |
  | `R` carries a **strict subset** of `S` | **Reject** — the rejection names the missing criteria | A28 |
  | `R` carries **all** of `S` (superset is fine) | **Accept** | A5 |
  | `S` is **empty** | **Accept** — "every element of `[]`" is vacuously satisfied. FR-030's `len(tail_members) > 0` still applies, so a bare supersede of a criteria-less member is still rejected | A29 |

  *Rev 2's rule was "reject a `supersede` whose `tail_members` collectively carry **none** of the
  superseded member's criteria". That is satisfied by carrying **1 of N**, so the adjudicator could
  supersede the member failing criterion `C3` and attach a replacement carrying only `C1` — exactly
  the bypass FR-030b exists to close, at the same one-throwaway-member price. NFR-2 claims this
  control is what makes US-3's guarantee "true rather than a speed bump", so a 1-of-N predicate made
  the guarantee false. Rev 2 also left "carry" undefined against a **struct** type and left the
  empty-`S` case undefined in both directions.*
  *Why this is required and FR-030 alone is not:* FR-030's `len(TailMembers) > 0` rule makes a
  **bare** discount impossible, and rev 1 called that *"structurally impossible"* and listed it in
  NFR-2 as a guarantee. It is neither. The content of `tail_members` is entirely LLM-authored, so the
  adjudicator can supersede the member whose output fails a criterion and attach one trivial,
  instantly-satisfiable tail member: the DoD is unchanged, but **the evidence set the judge weighs**
  is — which is the mechanism US-3 says it blocks. The bypass costs one throwaway member. Criteria
  inheritance is machine-checkable and is what "replacement work" actually means.
- **FR-031**: Every `supersede` MUST be distinctly identifiable in the audit trail from an `append`,
  by verb rather than by free text.
- **FR-032**: Every new plan-mutation path MUST reject `dod` and `owner_agent_id` **structurally in
  its request shape**, as `CorrectionRequest` already does, because `plan.Store.updateLocked`
  enforces no such freeze — the 409 guard is a property of one REST handler, not of the data. A
  conformance test MUST assert `CorrectionRequest` has no DoD and no owner field.
- **FR-033**: The system MUST NOT enforce a non-draft immutability guard inside
  `plan.Store.updateLocked`, and MUST NOT freeze `Plan.Bounds` on a running plan.
  *(Negative requirement — verified by a conformance assertion that no such guard exists in
  `updateLocked` and that a `Bounds` write on a running plan still succeeds, not by the concurrency
  test rev 1 traced it to. See §17.)*
- **FR-034**: Corrections MUST consume the **existing judge-round budget**, whose real names are
  `PlanBounds.PlanJudgeMaxRounds` (wire `plan_judge_max_rounds`) and
  `config.PlanningConfig.PlanJudgeMaxRounds` (default 20). The correction itself MUST NOT increment
  `JudgeRounds`; the re-judge it provokes already does. This preserves the declared sole-incrementer
  invariant at `plan_engine.go:1495` and avoids halving the effective budget by double-charging.
  **An applied correction MUST increment `supervision.correction_rounds`.** That counter is an
  **attribution counter, not a budget** — nothing gates on it and no path fails because of it; its
  only consumer is FR-035's message selection. It is required because causes 1 and 2 of
  `judge_rounds_exhausted` are otherwise the same event: both fire at
  `if p.JudgeRounds >= maxRounds` (`plan_engine.go:1288-1291`) and
  `buildPlanRoundsExhaustedHandover`'s entire input is `(p, maxRounds)` `[FACT — verified]`, so
  nothing in the plan record records how many rounds a correction provoked.
  This is a deliberate, scoped amendment of rev 1's blanket *"no second round budget"* non-behavior;
  §10 states the precise scope.
- **FR-035**: The four terminal causes this feature can produce MUST be distinguishable, across
  **three** `failed_reason` values rather than three strings on one:

  | Cause | `failed_reason` | Distinguisher | Message source |
  |---|---|---|---|
  | Round ceiling reached, no correction ever applied | `judge_rounds_exhausted` (`:1289`) | `supervision.correction_rounds == 0` | `buildPlanRoundsExhaustedHandover`, branched |
  | Corrections consumed the shared budget | `judge_rounds_exhausted` (`:1289`) | `supervision.correction_rounds > 0` | same builder, other branch |
  | DoD unreachable — a correction left the plan unable to progress (`:2680`), or PlanSupervisor issued `abandon` | **`dod_unreachable`** (new) | its own enum value | `buildUnreachableDoDHandover` (`:2892`), **already implemented and already wired at `:2680`** `[FACT — verified]` — this limb is a rewire, not new text |
  | Supervision attempt ceiling exhausted (FR-022) | **`supervision_unavailable`** (new) | its own enum value | new handover |

  Adding the two enum values is a **step-1 contracts change**: `Plan.yaml:140-153` is a closed enum
  under `additionalProperties: false` (`:28`), and both the generated Go and TS surfaces plus
  `src/lib/api/generated/schemas.ts` must be regenerated before any code emits them (FR-017's
  ordering discipline).
  *Why this replaces rev 1's "three distinct handover strings":* rev 1 demanded three strings from
  causes 1 and 2, which are one predicate at one line, using a counter it simultaneously forbade
  creating. Splitting the enum makes causes 3 and 4 **machine**-distinguishable — strictly better for
  the SPA badge, the operator and the test — and reduces the string problem to a two-way branch the
  new counter can actually decide.
- **FR-036**: The chosen correction verb MUST be recorded on the **`RevisionEntry`**, which already
  carries `Verb`, and MUST NOT be added to `task.JudgeVerdict`. `JudgeVerdict` crosses the wire in
  four generated surfaces plus a hand-maintained duplicate between `contracts/asyncapi.yaml` and
  `contracts/components/schemas/JudgeVerdictFrame.yaml`; extending it would be a full Constraint-#8
  pipeline for no benefit.
- **FR-037**: Revision entries MUST be reachable on a supported read surface. **Verified: none
  exists today** — `RevisionEntry` is persisted only in the intent-log JSONL; no REST route returns
  it; the generated producer `FromSessionMessageRevisionEntry` has **zero call sites**; and
  `PublishSessionMessage` has **zero non-test callers**. The `plan_correct` tool MUST therefore
  return the `RevisionEntry` in its own tool result (as `AppendCorrection` already produces via
  `CorrectionResult`). The spec MUST NOT describe this as "already exposed".
  **The operator-readable surface is FR-039b's widened audit entry, not a new REST route** (D-03,
  AMB-5 closed). *Rev 2's `SHOULD` for a plan-revisions route, combined with AMB-5 deferring it, left
  the tool result — visible only to the agent being audited — as the only shipped reader, while
  US-6 AS1 and test #43 measured the route that was not being built and NFR-5 demanded an
  **operator** reader (r3 M3-08). FR-039b now carries the target member id and the falsified
  assumption, which are the two artefacts US-6 needs and the two the rev-2 audit entry omitted.
  A REST read route remains out of scope, for O1's reason: `HandlePlans` has no per-plan
  authorization on any verb, so a route under `withAuth` grants every authenticated user access to
  every plan's revisions.*
- **FR-038**: The system MUST NOT implement correction rollback.
  *(Negative requirement — verified by a conformance assertion that no rollback verb, route or
  handler exists, not by the rejected-correction test rev 1 traced it to. See §17.)*
- **FR-039**: Every applied correction MUST emit a structured log line carrying plan id, verb,
  target member id and outcome.
- **FR-039b**: Every applied correction MUST also emit an **audit entry** carrying **five** fields:
  the **actor** (PlanSupervisor), the **plan id**, the **verb**, the **target member id** (the
  superseded or retried member; empty for `append` and `abandon`) and the **falsified assumption**.
  This closes AMB-6 as **yes**, not "skip it", **and it is the read surface US-6 measures** (D-03).

  *Rev 2's entry carried only actor, plan id and verb, while US-6 AS1 and test #43 required an
  operator to read "verb, target member id, falsified assumption and timestamp" — and FR-037 had
  already verified that **no revision read surface exists** and AMB-5 had deferred building one. So
  the two artefacts US-6 needs most were readable by nobody but the agent being audited, and NFR-5
  (*"reviewable … by an **operator**, not only by the agent being audited"*) was unmet (r3 M3-08).
  Per **D-03**, the entry is widened rather than a REST route promoted: `GET /api/v1/audit-log`
  already ships and is operator-readable (`pkg/gateway/rest.go:4883`), whereas a plan-revisions route
  inherits O1's unresolved authorization story (`HandlePlans` has no per-plan owner check on any
  verb). AMB-5 closes accordingly.*
  *Why it is required rather than optional:* NFR-5 demands corrections be *"attributable and
  reviewable after the fact"*. FR-037 establishes that **no read surface for revision entries exists**
  — `FromSessionMessageRevisionEntry` has zero call sites and `PublishSessionMessage` has zero
  non-test callers — so under AMB-5's fallback the only reader of the audit trail would be the agent
  being audited. That does not meet NFR-5 for a *privileged autonomous mutation verb*. Verified that
  this is new work: `pkg/agent/plan_engine.go` imports no audit package, and `auditPlan`
  (`pkg/gateway/rest_plans.go:93`) carries six events — `plan.create/update/delete/approve/stop/restart`
  — none for correction, every one hardcoded to `DecisionAllow`, and none recording an actor. The
  actor field is the part that matters here and does not exist yet.

### Group E — prompts and upstream annotations

- **FR-040**: `pkg/skills/embedded/plan/SKILL.md` MUST be amended in the same change at **four**
  sites, not three:
  1. `:158` — the `awaiting_owner_correction` literal, invalidated by the rename;
  2. **`:181` — the verb table's supersede row**, which reads verbatim *"Marks the done member's
     outcome ignored-by-Judge (record stays immutable). **Optionally** append a replacement tail
     member."* `[FACT — verified verbatim]`. That instructs the adjudicator that the thing the engine
     **hard-rejects** (FR-030, FR-030b) is optional. It MUST be replaced with a statement of the
     pairing rule: a supersede must be accompanied by at least one replacement tail member carrying
     the superseded member's acceptance criteria;
  3. `:231-232` — *"Do not create a forked 'Planner' agent"*, which under FR-007 PlanSupervisor would
     read as an instruction not to exist;
  4. **`:177-183` — the verb table gains a fourth row, `ABANDON`** (NEW in rev 3), stating that it
     terminates the plan `dod_unreachable`, mutates no member, and requires a non-empty falsified
     assumption.
     *Verified: the table today has exactly three rows — SUPERSEDE / TARGETED-RETRY / APPEND
     (`SKILL.md:177-183`) `[FACT]`. FR-046 makes `abandon` a first-class verb and US-1 AS6 makes it
     the honest exit, yet rev 2's FR-040 listed three amendment sites and **none** of them added the
     row, so `abandon` existed only in the rubric (§27). FR-040's own rationale calls this table
     *"PlanSupervisor's only guidance on verb selection"* and Appendix A's derivation note says the
     rubric and the skill "must not drift". SC-015 asserted three absences and **one** presence, so
     nothing caught it. An adjudicator reading a three-verb table when the situation needs the fourth
     loops on inapplicable verbs until FR-022's ceiling terminates the plan `supervision_unavailable`
     — **mislabelling an unreachable DoD as an unavailable supervisor**, which is exactly the
     distinction FR-035 split the enum to preserve (r3 M3-10).*

  A Go test (#25) MUST assert the embedded bytes contain **none** of the three retired strings **and
  do contain both** the pairing rule and the `ABANDON` row, because no compiler can see inside a
  `go:embed`ed markdown file.
  *Why `:181` is not cosmetic:* the verb table is PlanSupervisor's only guidance on verb selection.
  Left stale, every bare supersede it attempts fails validation — burning a supervision attempt each
  time (FR-022) — with the failure text being a validation error the SOUL never prepared it for.
  Rev 1's §4.2 flagged the verb table as a d=2 dependent of `validateCorrection` and then nothing
  acted on it: no FR, no test, no criterion. This is RISK-9 made concrete and is the cheapest
  high-value test in the feature.
- **FR-041**: Spec FR-146 MUST be annotated to point at this spec's override.
  *(Documentation requirement — **not testable in this repo's harness** and therefore excluded from
  §17's coverage claim. Verified at review time by opening
  `docs/internal/specs/unified-goal-plan-subagent-spec.md`, not by a Go test. Rev 1 traced it to a
  test asserting bytes of `plan/SKILL.md`, which checks nothing about it.)*
- **FR-052**: The spec MUST record that spec FR-133's spawn-edge ownership (a property of a
  *session*) and this spec's canonical Owner (a property of a *plan*) are distinct vocabularies, and
  neither is renamed into the other.
  *(Documentation requirement — same disposition as FR-041; excluded from §17's coverage claim.)*

### Group F — vocabulary and ordering

- **FR-060 — WITHDRAWN (rev 2).** Rev 1 required a one-shot plan-store migrator. The operator ruled
  greenfield unconditionally and ADR-055 was amended to match (*"No migrator ships … Withdrawn"*).
  **No migrator ships for any store.** Pre-rename plan records are expected not to load; that is
  accepted, not a defect. Implementers MUST NOT reintroduce a migration step "to be safe" — a
  half-migration is worse than none.
- **FR-061 — WITHDRAWN (rev 2).** Rev 1 deferred the session-lifecycle rename pending a migration
  decision and recommended descoping D14 rows 4 and 5. **That recommendation is overruled**: the
  migration constraint it rested on does not exist, so **the five live S9 rows ship** — rows 1, 4, 5, 6 and 7; row 2 was never in scope and row 3 is dropped by D-02 (see the S9 rows
  table). The append-only/immutable-terminal properties of `session_lifecycle/*.jsonl` remain true
  but are no longer relevant — nothing rewrites those files. The rename is a struct-field rename the
  Go compiler enumerates exhaustively; the one behaviour to regression-test is `boot_sweep.go:295-296`
  (row 4, R1.4b), **not** exemption (b) (row 5, which has no production writer).
- **FR-062**: The rename MUST NOT rely on the compiler alone. In addition to contracts-first
  regeneration and `tsc -b`, the change MUST include a mandatory `rg` sweep — **the exact command in
  SC-011** — over these directories:
  **the SPA source tree `src/` including its `*.test.ts` / `*.test.tsx` files** (added in rev 2),
  `pkg/skills/embedded/`, `pkg/gateway/inboundschemas/`, `tests/e2e/`, **`pkg/tools/` and
  `pkg/agent/` (NEW in rev 3)**, plus YAML prose and `*.md` repo-wide. The e2e gate MUST run before
  merge.

  **The sweep MUST match both spellings.** The retired vocabulary ships as
  `awaiting_owner_correction` **and** as the hyphenated `awaiting-owner-correction` — upstream spec
  FR-141 hard-codes the hyphen form (§3 records it), and it is live in the tree at
  `plan_engine.go:1709` and `:3171`, in `pkg/agent/boot_sweep_test.go`, and in the **title** of the
  e2e test at `tests/e2e/conformance-design-e2e.spec.ts:624`
  (`'Conformance_t2_PlanLifecycleE2E: … → awaiting-owner-correction holds'`) `[FACT — reproduced
  2026-07-27]`. Rev 2's alternation began `awaiting_owner_correction|…`, which matches neither the
  hyphen form nor the test title, so **SC-011 returned zero while in-scope occurrences of the retired
  vocabulary remained** (r3 M3-15, operator ruling 7). SC-011's first alternand is now
  `awaiting.owner.correction`, which matches both separators; `-i` already covers the SPA's uppercase
  constant.

  **`pkg/tools/` and `pkg/agent/` are the rev-3 addition, and they are the class the compiler cannot
  help with at all.** Beyond the two hyphenated literals above, the sibling `list-jobs-spec.md`
  **composes** the value at runtime as `"running/" + <phase>` (its `native_status` format), producing
  the literal `"running/awaiting_owner_correction"` in a package this sweep did not previously cover.
  A composed literal is invisible to `go build` even when the constant it should have been built from
  is renamed correctly (cross-spec C1). **Two MUSTs follow:**
  1. The sweep covers `pkg/tools/**` and `pkg/agent/**`.
  2. **A composed status literal is forbidden.** Any string that embeds a plan phase MUST interpolate
     the `plan.PlanPhase` constant, never a hand-written spelling of its value, so the compiler sees
     the rename. This applies to this spec's own code and is the rule `list-jobs-spec.md` must adopt.

  *If `list-jobs-spec.md` lands first, this sweep will not find its occurrences and SC-011 will pass
  while a wrong `native_status` ships. §18's landing-order note prevents that ordering.*
  **The SPA source tree is the addition rev 2 makes, and the one that matters most.** It holds the
  largest block
  of compiler-invisible occurrences — string-literal map keys in `src/lib/planStateColors.ts` and
  string fixtures in `planStateColors.test.ts`, `PlansFilterBand.{tsx,test.tsx}`,
  `WorkspaceGraphTab.{tsx,test.tsx}` and `ws.new-frames-validation.test.ts`, none of which
  `tsc -b --noEmit` type-checks against the generated enum. Rev 1's sweep list omitted the SPA source tree
  entirely while §13.4 separately named four of those files as needing update, so SC-011 was
  unachievable via the requirement that was supposed to achieve it.
  Measurement MUST exclude `.claude/**` (two full repo copies live there and inflated the ADR's
  counts), and MUST be the **mechanical command in SC-011**, so the requirement and the criterion are
  the same artefact and no occurrence count is ever quoted by hand.
- **FR-063**: The SPA copy at `src/lib/planStateColors.ts:213` **and** `:234` MUST both be revised,
  **to the specified replacements below** — rev 1 required deleting a string without saying what
  replaces it, against which four test files were to be rewritten.

  Both strings currently end *"There's no in-app action for that yet — Stop this plan (■) and create
  a new one with the fix instead."* `[FACT — verified verbatim]`. That sentence is about an action
  available to a **human in the UI**, and this release adds none for a human (O1, O10) — so it does
  not simply "become false". What changes is that **an autonomous supervisor is now working on the
  plan**, which is worth telling the user, and that Stop now also halts that supervisor (US-8).

  | Const | New copy |
  |---|---|
  | `AWAITING_SUPERVISION_EXPLANATION` (`:213`, renamed with S9 row 1) | *"This plan hit a dead end its own checks can't clear. A supervisor is reviewing it and will correct it automatically — no action needed. If you'd rather it stopped, Stop this plan (■); that halts the supervisor too."* |
  | `STALLED_EXPLANATION` (`:234`) | *"This plan has no members it can currently dispatch or that are in progress, so it can't make progress right now. A supervisor is reviewing why and will correct it automatically. If you'd rather it stopped, Stop this plan (■)."* |

  The two MUST remain **distinct strings** — `planStateColors.ts:222-226` documents at length why
  the two conditions must never share copy, and that constraint survives this change. Four tests
  assert the old copy and are rewritten against the new (§13.4).

### Group G — containment, payload validation and identity

- **FR-042**: The system MUST register an agent-facing tool **`stop_plan`** taking a `plan_id`,
  whose `Execute` builds the caller identity from `tools.ToolAgentID(ctx)` and calls
  `PlanEngine.StopPlan` with that agent id as the `userID` actor. Like `plan_correct` it MUST be
  injected as a func-value setter that **fails closed** when unwired. It MUST be **seeded per
  FR-006b**, not merely registered.
  **`StopPlan` is actor-parameterised on two arguments, `(…, userID, channel)`** — rev 2 specified
  only `userID` (r3 m3-04). For a tool-originated stop the `channel` argument MUST be the calling
  turn's channel as resolved by the tool context; where that is unavailable it MUST be the literal
  `"tool"`, never the empty string, because the value flows into the cancel attribution
  (`RequestCancelForSession(ctx, sessionID, userID, channel)`) and into the handover.
  **The tool's description MUST state that stopping a plan at `awaiting_supervision` aborts an
  in-flight adjudication** (cross-spec M6). The sibling `list-jobs-spec.md` normalises that phase to
  `blocked` — *"live but unable to progress without intervention"* — on the **Owner's** roster, and
  the Owner is the one principal FR-011 forbids from correcting, so an Owner acting on that signal
  has exactly one tool available and it is this one. The description is what stops it stopping
  healthy work. *(Operator ruling 8 settles the wider question: `blocked` is informational, not
  actionable — the executor cannot do anything about it. No redesign follows here; the tool
  description carries the warning and `list-jobs-spec.md` owns the signal.)*
  *Why it is in scope:* an agent that can start autonomous work must be able to stop it. Verified
  that today's agent-facing plan surface is exactly `create_plan` (`pkg/tools/plan.go:114`) and
  `execute_plan` (`:376`) — there is no stop tool, so an agent that executes a plan from a chat
  cannot halt it. Operator ruling 1 names this explicitly.
- **FR-043**: `stop_plan`'s authority MUST be **the plan's owner** — `caller.AgentID ==
  p.OwnerAgentID` — deliberately the inverse of `plan_correct`'s authority. Every denial MUST be
  indistinguishable in exactly the way FR-010 requires of the correction path, including for a plan
  that does not exist. PlanSupervisor MUST NOT hold `stop_plan` (FR-008): the adjudicator corrects,
  the owner contains.
- **FR-044 (the kill switch — operator ruling 5)**: `StopPlan` MUST **halt the in-flight supervision
  turn**. Adding `supervision.session_id` to the `sessions` slice at `plan_engine.go:1710-1712` is
  the mechanical part and is **not** the requirement; the requirement is the outcome.

  Three MUSTs, in dependency order:
  1. `supervision.session_id` MUST name a **real, store-backed session that the supervision turn is
     bound to** (FR-016b), and the turn MUST have been dispatched by a path that runs at all
     (FR-012c). Without both, the id in the slice cancels nothing.
  2. `StopPlan`'s fan-out MUST include it, alongside the member sessions, the verifier sessions and
     the owner session it already names (`:1694-1713`).
  3. **The success criterion is that the turn stops** (SC-020, tests #63/#63b), never that the id
     appears in the fan-out set.

  > **⚠ The rev-2 framing was wrong in a way that mattered.** It read *"extending an existing cascade
  > rather than any new mechanism"*. Verified: of the three legs of that cascade, the member-session
  > leg and the verifier leg genuinely stop turns; **the owner-session leg is a production no-op**,
  > because nothing in `pkg/` ever creates the `"plan:"+id` session it cancels (**N13**). And
  > `cancelSessions` discards `RequestCancelForSession`'s `fired` bool (`:1825`), so a leg that
  > cancels nothing is indistinguishable from one that works. Adding an id to a slice is trivial;
  > making that id name something cancellable is the whole of the work, and rev 2 specified none of
  > it (r3 C3-05).

  It applies identically from the SPA ■ button (`POST /plans/{id}/stop`) and from `stop_plan`,
  because both go through `StopPlan`.
  A `plan_correct` arriving after the stop is already rejected by `AppendCorrection`'s
  `p.State != plan.StateRunning` check (`:2589`), which MUST be preserved.
  Cancelling a `supervision.session_id` whose turn has already finished is a benign no-op (E34) —
  which is why FR-050 retains the id rather than clearing it when the plan leaves the phase.
- **FR-045**: The kill-switch procedure MUST be documented in `docs/operations/` in terms of
  plan-scoped Stop, and MUST NOT describe any tool-policy or agent-disable mechanism. A tool-policy
  switch is rejected 403 by `updateAgentTools`'s `Locked` guard (`pkg/gateway/rest.go:6789-6793`) and
  reverted on the next boot by the re-enforcement FR-002 mandates — so documenting it would be
  documenting a control that does not work (O9).
- **FR-046 (the `plan_correct` parameter schema)**: The tool's parameters MUST be specified as
  follows, and MUST NOT be invented at implementation time:

  | Field | Type | Required on | Rules |
  |---|---|---|---|
  | `verb` | enum | always | `append` \| `supersede` \| `targeted_retry` \| `abandon`. Any other value, including `""`, is rejected |
  | `falsified_assumption` | string | **always** | The assumption the plan made that turned out to be wrong. Non-empty, capped |
  | `reason` | string | optional | Free-text detail; capped |
  | `superseded_member_id` | string | `supersede` | Must name a `done` member **of this plan**; ownership checked before status (FR-047) |
  | `retried_member_id` | string | `targeted_retry` | Must name a `failed` member **of this plan** |
  | `tail_members` | array of `{title, description, criteria[]}` | `append`; ≥1 on `supersede` | **No caller-supplied `id`** — the engine mints every member id. Capped at `max_tail_members`. **Rejected outright on `targeted_retry` and `abandon`** |
  | `tail_edges` | array of `{from, to}` | optional on `append`/`supersede` | Endpoints must resolve to a member of this plan or to a member being created in the same request; must not name a superseded member; the resulting graph must be **acyclic**. Capped at `max_tail_edges`. Rejected on `targeted_retry` and `abandon` |

  `validateCorrection` MUST enforce every rule above **before any mutation**. Specifically it MUST
  gain: the acyclicity check, the edge-endpoint resolution check, the superseded-endpoint check, the
  two collection caps, the title/description length caps, and the verb/field-compatibility matrix.
  *Every one of these is currently absent* — `validateCorrection` (`:2693-2717`) never references
  `req.TailEdges` at all, and `AppendCorrection` sets `Members: req.TailMembers` (`:2621`) and
  `Edges: req.TailEdges` (`:2622`) unconditionally for every verb `[FACT — verified]`.
  **Engine-minted ids are the load-bearing choice.** `buildCorrectionApplyFunc` skips a tail member
  whose id already exists, with no replay-vs-first-application distinction — correct for intent-log
  replay, and a **silent data loss** for an LLM reusing an id it just read off the plan: the member is
  never created, the correction reports success, and the plan proceeds believing the work was added,
  which can flip a DoD verdict to MET. Minting ids engine-side retires the whole class rather than
  validating around it, and leaves the replay skip reachable only on replay, where it is correct.
  `abandon` carries **only** `verb`, `falsified_assumption` and `reason`; it mutates no member and
  terminates the plan `dod_unreachable` (FR-035). **It writes a `RevisionEntry` with
  `verb: "abandon"`, so `contracts/components/schemas/RevisionEntry.yaml`'s closed `verb` enum
  (`:40-45`) and `pkg/plan`'s `RevisionVerb` consts (`intent_log.go:80-85`) MUST both gain the value
  in §18 step 1** — see S15 and FR-046b.

  The caps MUST be **package constants in `pkg/plan`**, not config fields and **not**
  `PlanBounds`-overridable (**D-06**): `maxTailMembers` **20**, `maxTailEdges` **40**,
  `maxMemberTitleBytes` **512**, `maxTextBytes` **8192**. They bound the payload processed while
  `AppendCorrection` holds the **process-wide** `planDecisionMu` for its whole body (`:2575-2576`) —
  the same mutex `processPlan`, `StopPlan`, the judge round and idle expiry take.
  *(Rev 2 made all four `config.PlanningConfig` fields with per-plan `PlanBounds` overrides, shipping
  six new tunables at once. No operator use case was stated for varying a title-byte cap per plan,
  and each override is a wire addition plus a resolver — r3 O3-02. The two supervision **timings**
  stay configurable, because a slow provider is a real reason to vary them. If an operator ever asks
  to vary a payload cap, promoting a constant to a config field is a contained change; demoting a
  shipped wire field is not.)*

  **`maxTextBytes` bounds exactly these fields**, named because rev 2 did not (r3 m3-02):
  `falsified_assumption`, `reason`, and each tail member's `description`. Each tail member's `title`
  is bounded by `maxMemberTitleBytes` instead. Every bound is on **bytes**, not runes, and is checked
  before any mutation.
- **FR-046b (`RevisionEntry.verb` widening)**: `contracts/components/schemas/RevisionEntry.yaml`'s
  `verb` enum MUST gain `abandon`, its description MUST gain a sentence describing the verb, and
  `pkg/plan/intent_log.go`'s `RevisionVerb` const block MUST gain the matching constant — **as part
  of §18 step 1's single `gen-contracts` run**.
  *Verified this is a real contract change and not an implied one:* the enum is **closed** at three
  values (`RevisionEntry.yaml:40-45`), it is referenced from `contracts/openapi.yaml:641-642`, and it
  generates `RevisionEntryVerb` with a `Valid()` method over exactly those three
  (`pkg/api/generated/openapi_types.gen.go:3131-3146`) `[FACT — verified 2026-07-27]`. Test #36d and
  Dataset A25 both assert *"a revision entry with verb `abandon`"*, and FR-031/FR-036 make
  `RevisionEntry.Verb` the audit discriminator — so without this the honest exit produces a record
  the generated validator rejects. **Rev 2's §18 step 1 enumerated its contract work as (a)–(e) and
  `RevisionEntry.yaml` was in none of them**, nor in S15, FR-017, FR-035 or FR-050 — a Constraint #8
  violation in a spec that is otherwise rigorous about exactly this (r3 M3-14). Test #57 asserts the
  generated enum carries four values.
- **FR-047**: `validateMemberRef` MUST check **plan ownership before member status**. Today the
  status check runs first (`:2730-2737`), so a member belonging to another plan produces
  *"member %q is %s, not %s"* — putting another plan's member status into the adjudicator's context —
  before the ownership error fires. The reordered rejection MUST NOT name the other plan's id.
  Only PlanSupervisor reaches this code (FR-009 gates it), so this is a much smaller oracle than
  FR-010's, but FR-010's discipline should not stop at `requireOwner` when the adjacent validator on
  the same call path leaks.
- **FR-048**: An unreadable or malformed intent log MUST be **surfaced and fail closed**, never
  swallowed. `reconstructCorrections` (`:3105-3127`) currently `continue`s on a per-plan `List` error
  with no log line `[FACT — verified]`, silently **un**-superseding previously-superseded members.
  The system MUST log at ERROR naming the plan and the log file, record the condition on the plan
  record, and fail the plan rather than resuming it with an incomplete superseded set.
  *Why fail closed rather than warn:* an incomplete superseded set re-admits discounted evidence to
  the plan judge, which can flip a DoD verdict to MET — the false success US-3 exists to prevent,
  reached by a path US-3 does not consider, and invisible because FR-037 establishes there is no read
  surface for revisions.
- **FR-049**: The system MUST preserve, and MUST regression-test, the property that no principal can
  create or rename an agent whose id is in `systemAgentIDs`. Verified that this **already holds**:
  `createAgent` mints `uuid.New().String()` (`pkg/gateway/rest.go:2378`) so the caller cannot supply
  an id, a `{"type":"system"}` body is rejected 400, and `updateAgent` (`:2813`) takes the id from
  the path and never writes `.ID` `[FACT — verified; the property is also stated in-tree at
  `rest.go:1188`]`. No new check is required — but FR-009's entire integrity property rests on it, so
  it MUST be pinned by a test (#66) rather than assumed, and any future operator-chosen-id feature
  MUST add an explicit `systemAgentIDs` reservation before landing.
- **FR-050 (the durable supervision state)**: The system MUST add a `supervision` object to
  `contracts/components/schemas/Plan.yaml` **as a step-1 contracts change** (Constraint #8 —
  `Plan.yaml` is `additionalProperties: false` at `:28` and `pkg/api/generated/contract_test.go`
  fails on any Go struct producing schema-invalid JSON). Fields, all optional and additive:

  | Field | Type | Purpose | Required by |
  |---|---|---|---|
  | `wake_at` | RFC3339 timestamp | The supervision wake receipt. Arms the deadline **and** is the once-per-park dedup key | FR-021, FR-023 |
  | `wake_error` | string | Last wake-publish failure, so a failed wake is recorded rather than WARNed away | FR-024, SC-008 |
  | `attempts` | integer | Supervision turns that produced no valid correction; bounded by `supervision_max_attempts` | FR-022 |
  | `correction_rounds` | integer | Applied corrections. **Attribution counter, not a budget** | FR-034, FR-035 |
  | `session_id` | string | The **real, store-backed** session PlanSupervisor's adjudication turn runs in (FR-016b) — keeps its transcript out of the Owner's, and is what `StopPlan` cancels | FR-016b, FR-044 |

  **This single object is what makes five otherwise-separate requirements implementable**, and it is
  deliberately one contract change rather than five. `wake_at` alone resolves the wake-idempotency
  gap (a signature-keyed guard fires every tick), the timeout-detection gap (nothing else records
  when the turn started) and the boot re-wake dedup; `attempts` resolves the strand-on-first-bad-call
  gap; `correction_rounds` resolves the indistinguishable-exhaustion-causes gap; `session_id`
  resolves both the transcript-isolation question and the containment cascade.

  **Lifecycle — stated per field, never as a blanket rule** (r3 C3-03):

  | Field | On leaving the supervision-eligible phase set | On an applied correction | Ever reset? |
  |---|---|---|---|
  | `wake_at` | **cleared** — disarms the deadline so a later re-park re-wakes | cleared | yes |
  | `wake_error` | **cleared** | cleared | yes; also cleared by the next successful wake |
  | `attempts` | **reset to 0** | reset to 0 | yes |
  | `correction_rounds` | **untouched** | **incremented** | **NEVER.** Cumulative for the life of the plan |
  | `session_id` | **untouched** | **untouched** | **NEVER cleared** — overwritten only when the next supervision session is minted |

  > **Why `correction_rounds` is never reset.** Rev 2 closed FR-050 with *"Every field MUST be
  > cleared or reset when the plan leaves `awaiting_supervision`, so a later re-park starts clean."*
  > A plan leaves that phase on **every applied correction** — that is the behavioural contract
  > (*"returns the plan to `dispatching`"*). So `correction_rounds` was reset to 0 immediately after
  > each correction and read **0 on every terminal record**. FR-035's cause-1-vs-cause-2
  > distinguisher is `correction_rounds == 0` vs `> 0`, so a plan that burned its whole budget on
  > corrections would have reported *"the round budget ran out with no correction ever applied"*.
  > **Six readers depended on it** — FR-035's two branches, SC-016b, SC-017, the four-cause Scenario
  > Outline, test #62 and §20's runbook step 5 — and SC-017, SC-016b and #62 could not have passed.
  > This is r2's C-02 reopened by its own fix (r3 C3-03). Dataset E14 and test #62's park→correct→
  > re-park cycle are the regression.

  > **Why `session_id` is never cleared.** An applied correction moves the plan to `dispatching`
  > **while PlanSupervisor's turn may still be running** — the turn is not required to end at the
  > tool call. Clearing the id in that window would leave a stop unable to name the turn it must
  > cancel (r3 m3-07). Cancelling an id whose turn has already finished is a benign no-op (E34), so
  > retaining it costs nothing and closes the window. It is overwritten, not blanked.

  **Write path — `plan.Patch` gains five discrete pointer fields** (r3 M3-16): `SupervisionWakeAt
  *string`, `SupervisionWakeError *string`, `SupervisionAttempts *int`,
  `SupervisionCorrectionRounds *int`, `SupervisionSessionID *string`. Set semantics only — no delta
  fields; the engine reads, computes and sets under `planDecisionMu`.
  *Why five pointers and not one `Supervision **Supervision`:* every persisted plan mutation goes
  through `pe.planStore.Update(id, plan.Patch{…})`, and `plan.Patch` (`pkg/plan/store.go:232-271`) is
  a flat struct of typed pointers applied by `updateLocked` (`:287`) whose entire contract is *"only
  non-nil fields are written"*. The five fields are mutated **independently and with different
  semantics** (stamp, increment, clear, reset-to-zero, set-once). A single whole-object pointer —
  the `Bounds` convention at `:242` — makes each of those a read-modify-write over a struct the
  caller read earlier, which is safe only while `planDecisionMu` is held; `pkg/gateway/rest_plans.go`'s
  `Store.Update` callers do **not** hold it. That is a lost update on precisely the counters C3-03 is
  about. Named in §18 step 7; asserted by test #57c; regression row in §13.4; Dataset E38.
  **Rev 2 specified the contract shape of `supervision` in full and never stated its patch shape at
  all.**
- **FR-051**: `notifications.Store.Create` MUST reject a recipient that does not resolve to a
  configured user (or to a documented sentinel), with its own unit test.
  FR-014's `Gateway.Users` gate closes the orphan-file class **at the one new call site**, which is
  the right fix for this feature — but `Store.Create` itself stays unguarded, and the next caller
  reintroduces the exact failure `pkg/gateway/schedules.go:604-608` already documents
  (*"the admin-broadcast sentinel is NOT a real username — persisting it writes `_admin_.json` which
  no `ListForUser(username)` ever reads"*). This is the fail-closed discipline the rest of this spec
  applies everywhere else.
- **FR-070**: The change MUST be sequenced contracts-first (Constraint #8) per §18. In particular the
  notification contract widening (FR-017) MUST precede any code that emits a plan notification, and
  the vocabulary rename MUST precede the feature work so the feature is written once against final
  names.
- **FR-071**: `make verify-contracts`, `gofmt`, `golangci-lint run --build-tags=goolm,stdjson`,
  `npm run typecheck`, `npx vitest run` and `govulncheck` MUST all pass before merge. Per CLAUDE.md
  Constraint #7, any pre-existing failure encountered is in scope to fix, not to defer.

---

## 15. Non-Functional Requirements

- **NFR-1 (cost)**: The happy path MUST NOT add wakes — it stays at ~1 per plan, because a MET DoD
  triggers no correction. The **unhappy** path is the cost this feature introduces and MUST be
  bounded by **two** independent ceilings, because they bound different quantities:
  (a) at most `PlanJudgeMaxRounds` (default 20) *applied corrections* per plan, each provoking one
  re-judge; and
  (b) at most `supervision_max_attempts` (default 3) *unproductive supervision turns* **per park** —
  the judge-round budget cannot bound these, because a rejected correction increments nothing.
  **The whole-plan worst case is the product, not the sum**, and rev 2 stated it as the sum: a plan
  can park up to `PlanJudgeMaxRounds + 1` = **21** times, each park allowing up to 3 unproductive
  turns, so the bound is
  **≤ 20 productive adjudications + ≤ (PlanJudgeMaxRounds + 1) × supervision_max_attempts = 63
  unproductive turns**, each one LLM turn with the plan skill loaded. Rev 2's *"≤ 20 productive plus
  ≤ 3 unproductive turns per park"* was true per park and read as a whole-plan figure of 23; the real
  ceiling is ~83 turns (r3 M3-04). It is still **bounded and calculable**, which is the property that
  matters — but the arithmetic is now the one SC-022 measures. Per-member adjudication is an explicit
  non-goal. **SC-022 measures it.**
- **NFR-2 (integrity)**: PlanSupervisor MUST NOT be able to lower the bar it judges. **Stated at the
  strength the controls actually deliver**, which is not what rev 1 claimed:
  - **Structural (holds absolutely).** `CorrectionRequest` and the `plan_correct` parameter schema
    carry no DoD, no owner and no bounds field (FR-032, FR-046). The DoD is unreachable from this
    path, full stop.
  - **Structural (holds absolutely).** The tool grant holds exactly one allow — `plan_correct` — and
    therefore no write path to the plan record other than a correction (FR-008).
  - **Substantive (holds, with a named residual).** A `supersede` must carry replacement work
    (FR-030) **that inherits the superseded member's acceptance criteria** (FR-030b). Rev 1 asserted
    FR-030 alone made discounting *"structurally impossible"*; it does not — it makes a **bare**
    discount impossible and raises the cost of a replacement, and without FR-030b the adjudicator can
    attach one trivial member and change the evidence set for the price of a throwaway. FR-030b is
    what closes it, and it is a *validation* control, so it is only as strong as the criteria
    comparison it performs.
  - **Residual, recorded not hidden:** an adjudicator that appends a passing-but-wrong tail member
    can still manufacture a false success. FR-030 blocks *discounting*; it does not block *adding
    work that trivially satisfies a criterion*. That residual is what holdout **H2** tests
    externally, and it is why SC-003 measures the paired case and not only the bare one.
  The retired "DoD is byte-identical across a correction cycle" criterion was a **tautology** and
  MUST NOT be reinstated.
- **NFR-3 (security)**: No principal other than PlanSupervisor may reach correction, and no denial
  may differentiate plan state — including the plan-load path, not only `requireOwner` (FR-010).
  The threat model MUST include user-created agents, for which the policy layer is inert (N2), and
  MUST record why the exact-identity gate is sound: agent ids are server-minted UUIDs, so
  `plansupervisor` is unclaimable (N12, FR-049).
- **NFR-4 (availability)**: A missing, malformed, human or agent Owner MUST NOT block adjudication —
  only outcome delivery. A plan MUST NOT remain at `awaiting_supervision` indefinitely with nobody
  woken: either a correction lands, or FR-022's ceiling terminates it and tells the Owner.
- **NFR-5 (auditability)**: Every correction MUST be attributable and reviewable after the fact by
  an **operator**, not only by the agent being audited (FR-037, FR-039, FR-039b).
- **NFR-6 (operability)**: An operator MUST be able to **contain a running plan and everything
  working on it — the supervisor included — without a redeploy or a restart**, from the SPA ■ Stop
  button; and an agent that started a plan MUST be able to do the same via `stop_plan`. The procedure
  MUST be documented in `docs/operations/` (FR-042–FR-045).
  **Rev 2 replaces rev 1's NFR-6 entirely.** Rev 1 required disabling adjudication by setting
  PlanSupervisor's `plan_correct` policy to `deny`. That control cannot exist: the write is **403**'d
  by `updateAgentTools`'s `Locked` guard, and even a hand-edited `config.json` is reverted on the
  next boot by the seeded-policy re-enforcement FR-002 mandates and test #8 asserts — so the spec
  mandated the mechanism that destroys its own kill switch. See O9 and US-8.

---

## 16. Success Criteria

> **How to read this section after rev 3.** Four of these criteria previously passed against a
> broken mechanism (see the through-line note at the top of this document). Every criterion below has
> been re-read against the question *"would this still pass if the mechanism were entirely broken?"*
> Where the answer was yes, the assertion was rewritten against the observable outcome and the old
> form is recorded in italics so a re-grill can check the change rather than re-derive it.

- **SC-001 (the loop closes — blocking merge gate)**: A plan whose DoD is unmet for a **correctable**
  reason (as defined in User Story 1) reaches a terminal state with **zero** human interactions,
  driven by a **scripted adjudicator double** so the result is deterministic (test #60b, D-07).
  The assertion chain MUST include, in order: a supervision **turn ran** (SC-025), its `plan_correct`
  was **applied**, the plan left the supervision-eligible phase, the corrected work **dispatched**,
  and the plan reached a terminal state.
  *The intermediate steps are named because the 2026-07-26 UAT produced a plan reporting "Running
  0/3" forever with every test green — nothing asserted that the plan made **progress**. A criterion
  that only checks the final state can be satisfied by a plan that fails for an unrelated reason.*
- **SC-001b (the rubric works — nightly signal, not a merge gate)**: The same scenario against a
  **real** provider reaches a terminal state with zero human interactions (test #60). A failure is a
  rubric defect, tracked as an issue with a target date; it is never auto-quarantined and never
  blocks a merge (RISK-11, D-07).
- **SC-002**: In Dataset B's `plan_correct` authority rows (B1–B9, **9 rows**), exactly **2** are
  allowed (B1, B2 — both caller `plansupervisor`) and **7** are denied (B3–B7, B8, B9). All 7 denial
  responses carry the **identical error class and identical message body**, where the message body
  contains **no plan id** — asserted by comparing responses for two *different* plan ids and for a
  plan that does not exist.
  Additionally, B7b (authorised caller, nonexistent plan) returns a response that is **not** equal to
  those 7. And in Dataset F, `stop_plan`'s denials F3 and F4 are likewise identical to each other.
  *(Rev 1 said "1 allowed and 8 denied" against a table with two allowed rows, and demanded
  byte-identity of messages that each embed the plan id — arithmetically wrong and unsatisfiable.
  FR-010 removes the id from the message, which is what makes identity achievable at all.)*
- **SC-003**: A plan whose only defect is one unmet criterion does **not** reach `done` through any
  sequence of corrections that adds no work carrying the failing criterion. Measured over three
  attempt shapes, each asserted to leave the plan short of `done` and the criterion still unmet:
  (a) a bare `supersede` — rejected by FR-030;
  (b) a `supersede` paired with a tail member carrying none of the superseded member's criteria —
  rejected by FR-030b;
  (c) a `supersede` paired with a criteria-inheriting tail member that itself fails — applied, then
  re-judged UNMET.
  *(Rev 1 asserted "does not reach `done` after 20 consecutive bare-`supersede` attempts (the default
  round ceiling)". Both numbers were decorative: a rejected correction is refused by
  `validateCorrection` **before any mutation**, and `applyJudgeRoundOutcomeLocked:1495` — the sole
  incrementer — is never reached, so 20 rejections burn **zero** rounds. Worse, 20 attempts require
  20 wakes the once-per-park design prevents. The criterion measured a path that consumes nothing and
  reintroduced the tautology test #30 was written to replace.)*
- **SC-004**: On a **fresh** install, PlanSupervisor's effective tool policy resolves `allow` for
  **exactly 1** name — `plan_correct` — and `deny` for **every other name in
  `allStaticToolNames`**, asserted as `len(allowed) == 1 && len(denied) == len(allStaticToolNames) - 1`
  rather than against a hand-written list; every other seeded agent resolves `deny` for
  `plan_correct`. On a simulated **upgraded** install, an agent whose persisted map predates the tool
  resolves `allow`, and the engine gate denies it anyway. All asserted against
  `ResolveEffectivePolicy`, never against the seed literal.
  *(Rev 1 pinned 4 allows and "all 10 others" as denies — a 14-of-83 sample described as if it were
  the complement, while NFR-2 asserted a property over all of them. `denyAllThenOverride` stamps an
  explicit entry for every catalog name, so the complement-complete assertion is **cheaper** than the
  sample, and it future-proofs: a tool added later can never silently land in the allow set.)*
- **SC-004b (no agent can start a plan it cannot stop — FR-006b, operator ruling 6)**: On a **fresh**
  install, for **every** seeded agent, computed through `ResolveEffectivePolicy`:
  **`resolved(execute_plan) != deny ⟹ resolved(stop_plan) != deny`.**
  Additionally: `jim` resolves `stop_plan` to **`allow`** (so Dataset B11 is reachable);
  `plansupervisor` resolves it to `deny` (FR-043); and the Worker resolves it to `deny` via an
  **explicit** entry rather than by inheriting the `allow` ceiling.
  The implication is asserted as a **complement over the roster**, never as a list of agent ids, so a
  newly seeded agent granted `execute_plan` cannot ship unable to stop what it starts.
  A second, separate assertion covers the seed *literal*: for every fully-enumerated seed map,
  `policies["stop_plan"] == policies["execute_plan"]`. **Both are required** — N14 proves the seed
  literal and the resolved value already disagree for `execute_plan` in the tree today, so an
  implementer checking only one can still ship C3-02's defect.
  *(Rev 2 had **no** `stop_plan` policy criterion and no dataset rows, which is why the tool shipped
  denied to every agent with a fully green suite — r3 C3-02, cross-spec C2a.)*
- **SC-005**: PlanSupervisor's `Skills` field is a non-nil slice of length **1** containing `"plan"`,
  both on a fresh install and after a boot that follows an operator tampering with it.
- **SC-006**: `POST /api/v1/sessions`, the WS chat frame, and `PUT /api/v1/agents/{id}` with
  `default:true` each return **400** for a System-Agent id; `GetDefaultAgent()` returns a
  non-System agent in **100%** of a randomised roster test including the degenerate fallback.
- **SC-007**: Across a gateway restart, **at most one** supervision wake is issued per parked plan,
  and specifically: a plan whose `supervision.wake_at` deadline has **already elapsed** is re-woken
  on the **first tick after boot** (1 wake); a plan whose deadline has **not** elapsed receives
  **0** wakes and is re-woken when its original deadline elapses. In both cases `judge_rounds` and
  the persisted unmet-terminal signature are **unchanged**.
  *(Rev 2 said "re-woken exactly once across a gateway restart", which contradicts FR-023's dedup on
  `wake_at` and Dataset E12's "honoured from its original stamp, not re-armed" — a restart inside the
  window produces zero wakes. The criterion was unmeasurable because it never stated `wake_at`'s
  pre-restart value — r3 M3-12.)*
- **SC-008**: With the notifier returning an error on every call, a plan parked at
  `awaiting_supervision` has `supervision.wake_error` populated on its record within **one tick
  (30 s — `defaultPlanEngineTickInterval`, `pkg/agent/plan_engine.go:131`)**, and the gateway logs it
  at ERROR. After `supervision_max_attempts` (3) further ticks it is terminated
  `failed(supervision_unavailable)` rather than retried forever — it is never silently parked and
  never retried unboundedly.
- **SC-009**: `NewPlanEngine(...).Start(...)` with a nil notifier returns a **non-nil error**; the
  same struct constructed as a literal in an in-package test still runs.
- **SC-010**: Outcome delivery is correct for all **10** rows of Dataset C:
  **10/10 attempt** a bus wake to `owner_agent_id`; **9/10 succeed**; **C8 fails** — its
  `owner_agent_id` names a deleted agent, so the publish fails and the failure is recorded on the
  plan per FR-024, which is that row's stated expected behaviour.
  **Zero** notification files are written keyed on an agent id.
  Every row whose `owner` names a configured user produces a persisted notification carrying
  `plan_id` — **except C6**, whose store is configured to return an error and which asserts the
  opposite (0 notifications, ERROR logged, plan state unchanged).
  *(Rev 2 asserted "10/10 produce a bus wake" over a dataset containing a row whose expected
  behaviour is that the wake **fails**, and asserted the notification limb over a row whose store
  errors by design — arithmetically unsatisfiable against the dataset it cites by id, r3 M3-13.)*
- **SC-011**: This exact command returns **zero** lines:
  ```
  rg -n -i 'awaiting.owner.correction|OwnerScopeKind|OwnerScopeID|OwnsPlanID' \
     --glob '!.claude/**' --glob '!docs/**' --glob '!pkg/gateway/spa/**' .
  ```
  The criterion **is** the command, so requirement (FR-062) and criterion are the same artefact and
  no occurrence count is ever quoted by hand.

  Three properties of this command are load-bearing and each closes a hole rev 2's version had:
  1. **`awaiting.owner.correction`, not `awaiting_owner_correction`.** The `.` matches both the
     underscore and the **hyphen** form. Rev 2's alternation matched only the underscore, so the
     command **returned zero while in-scope hyphenated occurrences survived** — `plan_engine.go:1709`
     and `:3171`, `pkg/agent/boot_sweep_test.go`, and the **title** of the e2e test at
     `tests/e2e/conformance-design-e2e.spec.ts:624`. A criterion whose whole job is "the rename is
     complete" was met with the rename incomplete (r3 M3-15, operator ruling 7). *(The `.` also
     matches any single character, which is intentional over-inclusion: a false positive costs one
     reader a second; a false negative shipped a wrong literal.)*
  2. **Case-insensitive.** The SPA holds the value as the uppercase constant name
     `AWAITING_OWNER_CORRECTION_EXPLANATION`, which a case-sensitive sweep misses.
  3. **Run from the repo root over `.`, with only the three stated globs excluded** — so it reaches
     `src/**`, `pkg/skills/embedded/**`, `pkg/gateway/inboundschemas/**`, `tests/e2e/**`, **and
     `pkg/tools/**` + `pkg/agent/**`** (FR-062's rev-3 addition). Narrowing the path list is **not**
     permitted: the sweep's value is that it is unscoped except for the three exclusions, each of
     which has a stated reason (`.claude/**` holds two full repo copies that inflated ADR-055's
     counts; `docs/**` is prose that legitimately records the retired name, including this spec;
     `pkg/gateway/spa/**` is the build artefact of `src/**`).

  **Composed literals are the residual risk this command cannot fully close** — a runtime-built
  `"running/" + phase` never appears as a matchable string. FR-062 therefore *forbids* composed phase
  literals rather than relying on the sweep to find them.
- **SC-012 — WITHDRAWN (rev 2).** It asserted that a fixture `$OMNIPUS_HOME` containing pre-rename
  records loads after upgrade. Under the greenfield ruling it is expected **not** to load. No
  criterion replaces it; the behaviour it guarded no longer exists.
- **SC-013 — WITHDRAWN (rev 2).** It asserted a migration sentinel's absence-then-presence across a
  partial failure. No migrator ships.
- **SC-014**: `make verify-contracts` exits **0** after the rename plus the notification additions,
  with the generated artifacts committed in the same commit as the spec change.
- **SC-015**: The embedded `plan` skill bytes contain **zero** occurrences of the retired phase
  value **in either spelling**, **zero** occurrences of the "do not create a forked Planner agent"
  instruction, and **zero** occurrences of *"Optionally append a replacement"* — **and at least one
  occurrence each of TWO required presences**: (a) the supersede pairing rule (a replacement tail
  member carrying the superseded member's acceptance criteria), and (b) **an `ABANDON` row in the
  verb table** stating that it terminates the plan `dod_unreachable` and requires a falsified
  assumption.
  The presence assertions are the point: absence checks alone pass against a verb table that simply
  said nothing, and the two failure modes being guarded are the prompt telling the agent the
  **opposite** of what `validateCorrection` enforces (presence a) and the prompt **omitting the only
  verb that can end an un-correctable plan** (presence b).
  *(Rev 2 asserted three absences and one presence, so `abandon` living only in the rubric and in no
  version of the skill was invisible — r3 M3-10.)*
- **SC-016**: Applying a correction leaves `JudgeRounds` **unchanged**; the subsequent re-judge
  increments it by exactly **1**.
- **SC-016b**: An applied correction leaves `JudgeRounds` unchanged and increments
  `supervision.correction_rounds` by exactly **1**.
- **SC-017**: The four terminal supervision causes are distinguishable across **three**
  `failed_reason` values: `judge_rounds_exhausted` (two handover strings, selected by
  `supervision.correction_rounds == 0` vs `> 0`), `dod_unreachable`, and `supervision_unavailable`.
  Asserted as: 3 distinct `failed_reason` values **and** 4 pairwise-distinct handover strings, by
  exact match.
  *(Rev 1 demanded three strings from causes 1 and 2, which are one predicate at one line
  (`plan_engine.go:1288-1291`), using a counter FR-034 simultaneously forbade creating — and
  overstated cause 3, whose message `buildUnreachableDoDHandover` already ships. Test #62 as written
  could not have passed.)*
- **SC-017b**: `supervision.correction_rounds` reads the **cumulative** number of corrections applied
  over the plan's whole life on the **terminal** record, asserted by driving an explicit
  park → correct → dispatch → re-park → correct → terminate cycle and reading **2** — not by writing
  the value into a fixture. This is the criterion that makes SC-017's cause-1-vs-cause-2 branch
  measurable at all (r3 C3-03).
- **SC-018**: Every applied correction produces a revision entry, **and an audit entry carrying
  actor, plan id, verb, target member id and falsified assumption is readable by an operator from
  `GET /api/v1/audit-log`** — for **4/4** verbs (`append`, `supersede`, `targeted_retry`,
  `abandon`), before and after a restart, with the verbs distinguishable by field value rather than
  by free text.
  *(Rev 2 measured "readable from a supported surface" for 3/3 verbs while FR-037 had verified no
  revision read surface exists and AMB-5 deferred building one — the criterion measured a surface
  that does not ship, and omitted `abandon` — r3 M3-08, M3-10. Per D-03 the audit log is the
  surface.)*
- **SC-019**: `src/lib/planStateColors.ts` contains **zero** occurrences of "no in-app action", and
  **exactly the two replacement strings specified in FR-063** — one per constant, textually
  distinct from each other. The four tests asserting the old copy are updated to the new. Asserted on
  the new strings, not only on the absence of the old.
- **SC-020**: **Containment, measured by whether the supervisor stops.** With a supervision turn
  **actually running and blocked** on plan `P` — so its liveness is observable, not assumed —
  stopping `P` (from the SPA ■ button **and**, in a second run, from `stop_plan`) results in **all
  five** of:
  1. **that turn terminates** — its context observes cancellation and the turn returns — within a
     bounded time;
  2. the cancel was **claimed**: a live turn whose `transcriptSessionID` equals
     `supervision.session_id` was found (`GetActiveTurnHookForSession` returned non-nil), rather than
     a cancel request that matched nothing and returned `(false, nil)`;
  3. `P` is at `failed(stopped_by_user)`;
  4. **zero** corrections are applied to `P` after the stop;
  5. **no** restart or redeploy is required, and other parked plans are **unaffected** —
     containment is plan-scoped.

  **Limb (e) — NEW in rev 4 (operator ruling 11, FR-016c).** The same run also asserts that a live
  **Owner** turn bound to `Plan.OwnerSessionID` **terminates**. This limb is what makes operator
  ruling 1's *"stops everything working on it"* true rather than aspirational: the owner-session leg
  of `StopPlan`'s fan-out cancels an id that, per **N13**, names nothing that was ever created — so
  it has always been a no-op, and the existing test passes because the fake canceller records the
  name and returns success. The assertion is that **the turn stopped**, never that the id was passed
  to a canceller.

  **This criterion is NOT satisfied by a canceller double that records the session id and returns
  success.** `plan_stop_test.go`'s `fakeSessionCanceller` (`:28-38`) does exactly that, and
  `cancelSessions` discards the real canceller's `fired` bool (`plan_engine.go:1825`) — so limbs 1,
  2 and (e) must be asserted against a real turn and a real cancel path. Verified in integration
  tests #63, #63b and **#63c**, with #64 covering the `stop_plan` surface.

  *(Rev 2's SC-020 asserted "the supervision session present in the cancel fan-out set" — set
  membership, which a stubbed notifier and a synthetic session id satisfy regardless of whether
  anything is cancelled, and which per N13 describes a control that cancels nothing in production.
  Operator ruling 5: "it needs to test it stops." This is the headline instance of the rev-3
  through-line — r3 C3-05.)*
- **SC-021**: `gofmt -l . | wc -l` is **0**; `golangci-lint run --build-tags=goolm,stdjson`,
  `npm run typecheck`, `npx vitest run` and `govulncheck ./...` all exit **0** on CI.
- **SC-022 (cost, NFR-1)**: Driving one plan through the worst supervision path produces **at most
  `PlanJudgeMaxRounds` (20) applied corrections** and **at most
  `(PlanJudgeMaxRounds + 1) × supervision_max_attempts` = 63 unproductive supervision turns** over
  the plan's whole life.
  **Measured by counting the supervision wakes the harness actually observes**, not by reading a
  counter off the terminal record. No plan can produce an unbounded number of supervision LLM turns.
  *(Rev 2 counted "from `supervision.correction_rounds` and `supervision.attempts` **on the terminal
  plan record**" — but `attempts` is reset on every applied correction and on every phase exit, so
  the terminal record reports at most **one park's** worth, i.e. ≤ 3, against a real worst case of
  ~63. The criterion measured a counter that is reset before it is read: a control whose test passes
  because it asserts the mechanism rather than the property — r3 M3-04. Counting wakes measures the
  quantity NFR-1 is about (LLM turns billed), and is immune to any future change in how the counters
  reset. Adding a never-reset `total_attempts` field was the alternative and was rejected under D-06's
  reasoning: a sixth field for a number only a test reads.)*
- **SC-023 (payload validation)**: All of Dataset A's rejection rows — **A2, A4, A6–A8, A10–A14,
  A16, A18–A24, A26, A27, A28, A31** — are rejected **before any mutation**, asserted by comparing
  the full task store and intent log before and after each attempt and finding them unchanged.
  All of Dataset A's **acceptance** rows — A1, A3, A5, A9, A15, A17, A25, A29, **A30** — are applied.
  **Zero** rows produce a panic.
  *(Rev 2's rejection list included A3, whose stated expectation is "Applied" — the row asserted 50
  tail members are accepted against a `max_tail_members` cap of 20, so it contradicted the cap it
  exists to bound and SC-023 inherited the error. A3 is now 20 members and A22 remains `cap + 1` —
  r3 M3-01. The acceptance list is enumerated too, so a row cannot silently belong to neither.)*
- **SC-025 (the wake starts a turn — FR-012c, N15)**: A supervision wake results in an **agent turn
  running for `plansupervisor`**, whose `transcriptSessionID` equals the plan's
  `supervision.session_id`, and that session resolves to a real store with a non-empty transcript.
  Asserted against a **real** notifier + bus + loop.
  **Observing that `Notify` was called does not satisfy this criterion.** Today every plan wake is
  discarded by `processSystemMessage`'s internal-channel guard (`loop.go:6022-6031`) three hops
  downstream of `Notify`, so the standard fake-notifier assertion passes against a delivery path that
  delivers nothing (N15). The same criterion applies to the Owner wake sites: a plan reaching a
  terminal state results in a turn for `owner_agent_id` (test #31c), which is what US-4 AS1 and
  SC-010 mean by "woken".

  **Rev 4 — the four assertions that would have caught this, stated so none can be substituted for
  another.** Each is separately necessary; a suite that has any three still ships a broken loop:
  1. **A turn ran.** The agent loop dispatched and completed a turn for the expected agent. *Not:
     `Notify` returned `nil`; not `supervision.wake_at` was written; not an event was captured.*
  2. **It ran in the right session.** The turn's `transcriptSessionID` equals
     `supervision.session_id` (family A) or `Plan.OwnerSessionID` (family B), **and**
     `ResolveSessionStore` on that id is non-nil, **and** the transcript on disk is non-empty.
  3. **It reached the right place, or provably nowhere.** For an Owner wake on a plan **with** an
     origin, an outbound message is published to `p.SourceChannel`/`p.SourceChatID`. For a
     supervision wake, **zero** outbound messages are published to any channel (H8/FR-016).
  4. **The guard still works for everything else.** A `cli` and a `subagent` system message start no
     turn (§13.4's regression row).
- **SC-026 (a plan without a chat origin is still delivered, and it is not delivered to the wrong
  place — FR-012d, D-10)**: For a plan created through `POST /api/v1/plans` (no chat origin) that
  reaches a terminal state:
  - a turn **runs** for `owner_agent_id` and its closing synthesis is **persisted** to
    `Plan.OwnerSessionID`, which resolves to a real store;
  - **zero** outbound messages are published — to any channel, including the owner agent's
    last-active channel and any other session belonging to the plan's creator;
  - a `plan_completed` / `plan_failed` notification carrying `plan_id` exists for the creator.

  And, on the population half: a plan created by `create_plan` from a channel-bearing turn records
  that channel and chat id (**including `webchat`**); a plan created over REST records neither.
  Asserted by tests **#31d** and **#31e**. *A criterion that only checked "the plan has an origin
  field" would pass against a REST-created plan with two empty strings and a wake that goes nowhere
  — which is the state this spec is fixing.*
- **SC-024 (identity floor)**: Attempting to create an agent with id `plansupervisor` — as a user and
  as an agent holding `create_agent` — never yields an agent with that id; the created agent's id
  parses as a UUID in **100%** of attempts, and a `{"type":"system"}` body returns **400**.

---

## 17. Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test(s) | SC |
|---|---|---|---|---|
| FR-001 | US-2 | *Only PlanSupervisor may apply a correction* | #7 | SC-002 |
| FR-002 | US-2 | *PlanSupervisor's tool grant is the allow-set and nothing else* | #8 | SC-004, SC-005 |
| FR-003 | US-2 | *A user cannot open a chat session against a System Agent*; *System agents are rejected on every chat-target surface* | #47, #48, #49, #50 | SC-006 |
| FR-004 | US-1 | *PlanSupervisor appends a missing step…*; *A corrected plan reaches done with no human input*; *Only PlanSupervisor may apply a correction* | #26, #52, #60 | SC-001 |
| FR-005 | US-1 | *PlanSupervisor diagnoses a stall rather than issuing a DoD verdict* | #10, #31 | SC-001 |
| FR-006 | US-2 | *Only PlanSupervisor may apply a correction* | #1, #2, #3 | SC-004 |
| **FR-006b** | US-8 | *No agent can start a plan it cannot stop* | #5b, #5c, #3 | SC-004b |
| FR-007 | US-7 | *PlanSupervisor's own prompt contains no retired vocabulary and no contradicted rule* | #9 | SC-005 |
| FR-008 | US-2 | *Every non-PlanSupervisor principal is denied correction*; *PlanSupervisor's tool grant is the allow-set and nothing else* | #4, #5, #6 | SC-004 |
| FR-009 | US-2 | *Only PlanSupervisor may apply a correction*; *Every non-PlanSupervisor principal is denied correction* | #15, #16, #18 | SC-002 |
| FR-010 | US-2 | *Denials are indistinguishable and leak no plan state*; *An authorised caller naming a plan that does not exist gets a real not-found error* | #17 | SC-002 |
| FR-011 | US-2, US-4 | *Every non-PlanSupervisor principal is denied correction* | #16 | SC-002 |
| FR-012 | US-1, US-4 | *PlanSupervisor diagnoses a stall…*; *The parked phase and the stalled phase never co-occur*; *Every plan's outcome reaches its responsible agent over the bus* | #31, #32, #59 | SC-001, SC-010 |
| FR-012b | US-4 | *A plan that reaches `done` notifies its owner agent and its human author* | #32b | SC-010 |
| **FR-012c** | US-1, US-4 | *The supervision wake actually starts a turn*; *An Owner wake reaches a turn and the conversation the plan came from* | #31b, #31c | SC-025, SC-001 |
| **FR-012d** | US-4, US-1 | *An Owner wake reaches a turn and the conversation the plan came from*; *A plan created in the UI still reaches its owner* | #31c, #31d, #31e | SC-026, SC-025, SC-010 |
| FR-013 | US-4 | *Every plan's outcome reaches its responsible agent over the bus*; *An agent-created plan writes no unreadable notification file* | #19, #23 | SC-010 |
| FR-014 | US-4 | *Every plan's outcome reaches its responsible agent…*; *A human who created a plan through the UI is additionally notified*; *An agent-created plan writes no unreadable notification file*; *A notification failure never means nobody was told* | #33, #34, #35 | SC-010 |
| FR-015 | US-4 | *A human who created a plan through the UI is additionally notified* | #34 | SC-010 |
| FR-016 | US-2, US-4 | *The supervision wake still reaches PlanSupervisor after the chat-target guards land* | #51 | SC-006 |
| **FR-016b** | US-8 | *Stopping a plan halts the supervisor working on it*; *The supervision wake actually starts a turn* | #63b, #63, #31b | SC-020, SC-025 |
| **FR-016c** | US-4, US-8 | *An Owner wake reaches a turn and the conversation the plan came from*; *A plan created in the UI still reaches its owner*; *Stopping a plan halts the supervisor working on it* (sibling) | #31c, #31d, #63c | SC-025, SC-026 |
| FR-017 | US-4 | *A human who created a plan through the UI is additionally notified* | #55, #56 | SC-014 |
| FR-018 | US-4 | *A human who created a plan through the UI is additionally notified* | #34, #55 | SC-010 |
| FR-019 | US-1, US-4 | *Exhausting the supervision attempt ceiling terminates the plan and tells the Owner*; *PlanSupervisor unavailable **below the ceiling**…*; *PlanSupervisor unavailable **at the ceiling**…* | #36, #36a | SC-008 |
| FR-020 | US-1 | — (**negative requirement**; see the note below) | **#72** `TestConformance_NoMemberRetrySurfaceExists` | — |
| FR-021 | US-1 | *A supervision turn that emits nothing at all is detected by the deadline* | #36b | SC-008 |
| FR-022 | US-1 | *A rejected correction re-arms the supervision wake…*; *Exhausting the supervision attempt ceiling terminates the plan…* | #36c, #36 | SC-008, SC-022 |
| FR-023 | US-5 | *A parked plan is re-woken after a restart*; *A parked plan at its round ceiling terminates…*; *Repeated ticks do not produce repeated supervision wakes* | #37, #38, #39, #42 | SC-007 |
| FR-024 | US-5 | *Starting the engine without a notifier is a startup error*; *A failed wake publish is recorded and retried* | #40, #41 | SC-008, SC-009 |
| FR-025 | US-1 | *A stale stall note does not follow a plan into the parked phase* | #68 | SC-001 |
| FR-026 | US-8 | *An operator can see how many plans are awaiting supervision* | **#71** | SC-020 |
| FR-027 | US-5 | — (**documentation of the measurement unit**; see the note below) | — | SC-008 |
| FR-028 | US-8 | *A long-parked plan is still reaped by idle expiry* | #53 | SC-020 |
| **FR-029** | US-1 | *PlanSupervisor can actually correct a stalled plan*; *A stalled plan's supervision deadline is armed, counted and bounded*; *PlanSupervisor diagnoses a stall…* | #36e, #36f, #59 | SC-001, SC-023 |
| FR-030 | US-3 | *A supersede with no replacement work is rejected*; *A supersede paired with replacement work is applied* | #11, #12, #30 | SC-003 |
| FR-030b | US-3 | *A supersede whose replacement drops the failing criteria is rejected*; *A supersede whose replacement carries only some of the failing criteria is rejected*; *A supersede of a member with no acceptance criteria is allowed*; *A plan cannot reach done by discounting evidence, bare or paired* | #67, #30 | SC-003, SC-023 |
| FR-031 | US-3, US-6 | *A supersede is distinguishable in the audit trail* | #43, #61 | SC-018 |
| FR-032 | US-3 | *The correction request cannot carry a DoD or an owner reassignment* | #14 | SC-023 |
| FR-033 | US-1 | — (**negative requirement**; see the note below) | **#73** `TestConformance_NoStateGuardInUpdateLocked` | — |
| FR-034 | US-1 | *A correction does not consume a judge round but does advance the correction counter*; *The correction counter survives a park → dispatch → park cycle* | #27, #62 | SC-016, SC-016b, SC-017b |
| FR-035 | US-1 | *The four terminal supervision causes are distinguishable*; *A plan whose DoD is unreachable fails honestly…* | #29, #62 | SC-017 |
| FR-036 | US-6 | *An operator reads why a plan changed* | #43 | SC-018 |
| FR-037 | US-6 | *The revision history survives a restart*; *A rejected correction leaves no audit trace* | #44, #45 | SC-018 |
| FR-038 | US-6 | — (**negative requirement**; see the note below) | **#74** `TestConformance_NoRollbackSurfaceExists` | — |
| FR-039 | US-8 | *Every applied correction emits a structured log line* | #54 | SC-020 |
| FR-039b | US-6 | *Every correction produces an audit event*; *An operator reads why a plan changed* | #54b, **#43** | SC-018 |
| FR-040 | US-7 | *PlanSupervisor's own prompt contains no retired vocabulary and no contradicted rule* | #25 | SC-015 |
| FR-041 | US-7 | — (**documentation requirement, not testable here**; see the note below) | — | — |
| FR-042 | US-8 | *A plan's owner agent can stop the plan it started* | #64, #65 | SC-020 |
| FR-043 | US-8 | *An agent cannot stop a plan it does not own* | #64 | SC-002, SC-020 |
| FR-044 | US-8 | *Stopping a plan halts the supervisor working on it* | #63, **#63b** | SC-020 |
| FR-045 | US-8 | — (**documentation deliverable**: `docs/operations/`; see the note below) | — | SC-020 |
| FR-046 | US-1, US-3 | *The `plan_correct` payload is validated before any mutation*; *Member ids are minted by the engine, never by the caller*; *An un-correctable plan exits honestly through `abandon`* | #20, #21, #22, #24, #36d | SC-023 |
| **FR-046b** | US-1, US-6 | *An un-correctable plan exits honestly through `abandon`*; *The contract artifacts are regenerated in the same change* | #57, #36d, #56 | SC-014, SC-018 |
| FR-047 | US-3 | *The `plan_correct` payload is validated before any mutation* | #70 | SC-023 |
| FR-048 | US-6 | *A corrupt intent log is surfaced, not swallowed* | #46d | SC-018 |
| FR-049 | US-2 | *The id `plansupervisor` cannot be claimed by any principal* | #66 | SC-024 |
| FR-050 | US-1, US-5 | *A rejected correction re-arms the supervision wake…*; *Repeated ticks do not produce repeated supervision wakes*; *The correction counter survives a park → dispatch → park cycle* | #57b, **#57c**, #36b, #42, #62 | SC-007, SC-014, **SC-017b** |
| FR-051 | US-4 | *An agent-created plan writes no unreadable notification file* | #69 | SC-010 |
| ~~FR-060~~ | — | **WITHDRAWN** (greenfield) | — | — |
| ~~FR-061~~ | — | **WITHDRAWN** (greenfield) | — | — |
| FR-052 | US-7 | — (**documentation requirement, not testable here**; see the note below) | — | — |
| FR-062 | US-7 | *The rename leaves no occurrence the compiler cannot see* | SC-011's command, run in CI | SC-011 |
| FR-063 | US-7 | *The SPA parked and stalled copy names a control the user actually has* | #58 | SC-019 |
| FR-070 | US-7 | *The contract artifacts are regenerated in the same change* | #55, #56, #57, #57b | SC-014 |
| FR-071 | all | — (gate, not a behaviour) | CI gates | SC-021 |
| NFR-1 | US-1 | *A correction does not consume a judge round but does advance the correction counter*; *Exhausting the supervision attempt ceiling terminates the plan…* | #27, #36 | SC-016, SC-022 |
| NFR-2 | US-3 | *A plan cannot reach done by discounting evidence, bare or paired*; *The correction request cannot carry a DoD…*; *PlanSupervisor's tool grant is the allow-set and nothing else* | #14, #30, #67, #4 | SC-003, SC-004 |
| NFR-3 | US-2 | *Denials are indistinguishable and leak no plan state*; *Every non-PlanSupervisor principal is denied correction*; *The id `plansupervisor` cannot be claimed by any principal* | #6, #17, #66 | SC-002, SC-004, SC-024 |
| NFR-4 | US-4 | *A notification failure never means nobody was told*; *Exhausting the supervision attempt ceiling terminates the plan…* | #35, #36 | SC-010 |
| NFR-5 | US-6 | *An operator reads why a plan changed*; *Every correction produces an audit event* | #43, #44, #54b | SC-018 |
| NFR-6 | US-8 | *Stopping a plan halts the supervisor working on it*; *A plan's owner agent can stop the plan it started* | #63, #64 | SC-020 |

### Completeness check — stated precisely

Rev 1 claimed *"every FR and NFR has at least one BDD scenario, at least one test, and at least one
success criterion"*, which was true only in the sense that every row was populated — seven rows were
populated with artefacts that verify something else. Rev 2 states the position honestly instead.

**1. Every *behavioural* FR and NFR has ≥1 BDD scenario, ≥1 test and ≥1 success criterion.** No
exceptions.

**2. Four FRs are *negative* requirements** — they mandate that something is **not** built. A test
that exercises the positive path does not verify them (rev 1 traced FR-020 to a test asserting
`targeted_retry` *works*, FR-033 to a mutex-interleaving test, and FR-038 to an unrelated one).
Each is instead verified by a **conformance assertion** — a mechanical check that the forbidden
construct is absent — and carries no success criterion, because "we did not build a thing" has no
measurable outcome. **Rev 3 gives all three a test id, a name and a place in §13.2**; rev 2
described them and left them with none, which was honest about the coverage claim and still left
three deliverables nobody would have implemented (r3 test-gap 7):

| FR | Test | Conformance assertion |
|---|---|---|
| FR-020 | **#72** | No member-retry REST route, tool verb or SPA control exists |
| FR-033 | **#73** | `plan.Store.updateLocked` contains no non-draft state guard; a `Bounds` write on a running plan succeeds |
| FR-038 | **#74** | No rollback verb, route or handler exists |

Each is specified as a **mechanical scan of the shipped surface** (the registered tool/verb tables,
the route table, the function body) rather than as a grep for a string, so that renaming the
forbidden construct does not silently satisfy it.
| FR-012b | `synthesizeAndComplete`'s wake target is `p.OwnerAgentID` (a *positive* pin guarding a negative: "do not re-target this") — this one **does** have a test, #32b, and regression row R1.11 |

**3. Three FRs are documentation deliverables and are excluded from the coverage claim.** They are
verified by a human opening the named file at review time, not by this repo's harness:
FR-041 (annotate spec FR-146), FR-052 (record the FR-133 vocabulary distinction), FR-045
(`docs/operations/` kill-switch procedure). Rev 1 traced FR-041 and FR-052 to a Go test asserting
bytes of `plan/SKILL.md`, which checks nothing about either.

**4. FR-027 documents the measurement unit** (the 30 s tick) rather than requiring behaviour; it is
traced to SC-008, the criterion that measures against it.

**5. Every BDD scenario in §12 appears in at least one row.** Four are **anchors** rather than new
requirements, and are labelled as such in §12 itself:
*The parked phase and the stalled phase never co-occur* (regression, FR-012),
*A correction and a stop are serialised* (regression, US-8 AS4 / FR-044),
*A correction does not consume a judge round…* (regression, FR-034),
*A targeted_retry of a superseded member remains impossible* (conformance, FR-030),
*The correction request cannot carry a DoD or an owner reassignment* (conformance, FR-032).

**6. Every test in §13.2 appears in at least one row.** Rev 1 orphaned #53
(`TestIdleExpiry_ReapsLongParkedPlan`) — it appeared in zero rows and survived parked in FR-063's row
where it did not belong. It is now owned by **FR-028**, written for that purpose.

**7. FR-039 and NFR-6 are no longer swapped.** Rev 1 traced FR-039 (the structured log) to SC-020
(the kill-switch criterion) and NFR-6 (the kill switch) to #54 (`TestCorrectionEmitsStructuredLog`).
Both now trace to their own artefacts.

**8. Every BDD scenario has a test, including the one that did not (rev 3).** Rev 2's structural
check failed on *"An operator can see how many plans are awaiting supervision"*, which had **no**
test in §13.2 while FR-026's matrix row pointed at `TestIdleExpiry_ReapsLongParkedPlan` — a test that
reaps a plan and asserts nothing about a count. This is the exact mis-trace class item 7 says was
eliminated, surviving in a row rev 2 wrote (r3 M3-11). **#71
(`TestSupervisionGauge_CountsParkedPlans`) is added and FR-026 is retraced to it**; #53 is owned by
FR-028 alone.

**9. The rev-3 additions and where they trace.** New FRs: FR-006b (#5b, #5c → SC-004b), FR-012c
(#31b, #31c → SC-025), FR-016b (#63b → SC-020/SC-025), FR-029 (#36e, #36f → SC-001/SC-023), FR-046b
(#57 → SC-014). New tests: #5b, #5c, #31b, #31c, #36a, #36e, #36f, #57c, #60b, #63b, #71, #72, #73,
#74. New scenarios: *PlanSupervisor can actually correct a stalled plan*; *A stalled plan's
supervision deadline is armed, counted and bounded*; *No agent can start a plan it cannot stop*;
*The correction counter survives a park → dispatch → park cycle*; *The supervision wake actually
starts a turn*; *A supersede whose replacement carries only some of the failing criteria is
rejected*; *A supersede of a member with no acceptance criteria is allowed*; *PlanSupervisor
unavailable below/at the ceiling* (split); *A restart inside the supervision deadline issues no wake
at all*. New criteria: SC-001b, SC-004b, SC-017b, SC-025.

**10. The rev-4 additions and where they trace.** New FRs: **FR-012d** (#31c, #31d, #31e →
SC-026/SC-025/SC-010), **FR-016c** (#31c, #31d, #63c → SC-025/SC-026/SC-020). New tests: **#31d**
(origin-less: supervision + owner + non-escalation), **#31e** (origin population on both write paths,
`webchat` included), **#63c** (the `StopPlan` owner leg terminates a real turn),
`TestNotify_StillRejectsEmptyDestination` (§13.4). New scenarios: *An Owner wake reaches a turn and
the conversation the plan came from*; *A plan created in the UI still reaches its owner*. New
criteria: **SC-026**; SC-020 gains limb (e); SC-025 gains its four-assertion enumeration. New
findings: **N15.1** (what is *not* the bug), **N15.2** (root cause: `Plan` has no origin fields),
**N15.3** (the precedent and the deliberate `webchat` divergence), **N16** (an empty origin escalates
a healthy plan). New edge cases: **E38** (origin-less), **E39** (`webchat`, closed tab), **E40**
(client-supplied origin ignored), **E41** (partial origin), **E42** (FR-N7 removed). New risk:
**RISK-16** (the same defect on the Task goal-loop wake — recorded, not fixed). Modified: D-08's
decision cell, O11 (removed from out-of-scope), RISK-13, RISK-15, §18 steps 1(h) and 7(e)/(f), §3's
BOM gate and its new ADR-055 amendment table. Rev 4 adds **no** new user story and **no** new
architecture.

Every rev-4 FR traces to at least one scenario, one test and one success criterion; every rev-4 test
traces to an FR. Neither addition changes an existing FR's meaning — FR-012c's four properties,
SC-025's headline claim and tests #31b/#31c survive rev 3 intact, with mechanism and siblings added
beneath them.

---

## 18. Implementation Order

Contracts first (Constraint #8), vocabulary before feature, catalog before seed. Each step is a
reviewable unit; steps 1–3 are prerequisites that make the rest compile and boot.

| # | Step | Why here |
|---|---|---|
| **1** | **Contracts + regen — one commit.** (a) the `plan_phase` enum rename in `contracts/components/schemas/Plan.yaml`, `PlanStatusFrame.yaml`, `GoalStatusFrame.yaml`, `SessionLifecycleRecord.yaml`, `asyncapi.yaml`; (b) the **two new `failed_reason` values** `dod_unreachable` + `supervision_unavailable` (`Plan.yaml:140-153`); (c) the **new `Plan.supervision` object** (FR-050); (d) the notification `type` widening to the **named values `plan_completed` and `plan_failed`** + `plan_id` in `Notification.yaml` **and** `asyncapi.yaml`'s `NotificationFrame.notification_type`, plus the `Notification.yaml:22` false-tolerance description fix; (e) the `Plan.yaml` `owner` description fix; **(f) `RevisionEntry.yaml`'s `verb` enum gains `abandon` and its description gains the sentence (FR-046b) — NEW in rev 3**; **(g) the `owner_scope_kind` / `owner_scope_id` / `owns_plan_id` mentions in the *prose descriptions* of `contracts/components/schemas/Goal.yaml:41` and `Plan.yaml:135` — NEW in rev 3**; **(h) the two new `Plan.yaml` properties `source_channel` + `source_chat_id` (FR-012d) — NEW in rev 4**. Then `scripts/gen-contracts.sh`, committing `pkg/api/generated/`, `src/lib/api/generated/` and `pkg/gateway/inboundschemas/` atomically. | Constraint #8. **Five closed enums / `additionalProperties: false` schemas change here**, and every one of them fails *totally* rather than per-row at the SPA edge. The notification widening in particular must precede any emitter or the notification centre goes blank (FR-017). **(f) was missing from rev 2 entirely** — a closed enum with a generated `Valid()` that `abandon` would fail (r3 M3-14). **(g) is the compiler-invisible YAML-prose class FR-062 exists for**, named explicitly because rev 2's list covered "YAML prose" generically without naming these two files (cross-spec M2). **(h) is deliberately folded into this step rather than added as its own contract commit** — `Plan.yaml` is already being changed here by (a), (b), (c), (e) and (g), and a second `gen-contracts` run against the same file is pure churn with a second chance to commit a stale artifact. |
| **2** | **The rename sweep — the five live S9 rows.** `pkg/plan` phase const + `validPlanPhases` (row 1); `OwnerScopeKind`/`OwnerScopeID` → `ScopeKind`/`ScopeID` (row 4 — **verify `boot_sweep.go:295-296` still gates identically**); `OwnsPlanID` → `SupervisedPlanID` (row 5); `ownerKey` → `scopeKey` (row 6); `ProcessSession.OwnerSessionID` → `TranscriptSessionID` (row 7 — after it, `OwnerSessionID` means exactly one thing). **Row 3 is DROPPED (D-02): `Plan.OwnerSessionID` keeps its name.** Then the `rg` sweep — SC-011's exact command — over the SPA source tree `src/`, `pkg/skills/embedded/**`, `pkg/gateway/inboundschemas/**`, `tests/e2e/**`, **`pkg/tools/**` and `pkg/agent/**` (NEW in rev 3)**, YAML prose and `*.md` (FR-062); `plan/SKILL.md:158`, **`:181`** and **the new `ABANDON` verb-table row at `:177-183`** (FR-040); the SPA copy at `planStateColors.ts:213` **and** `:234` plus its four tests (FR-063). **No migrator** (FR-060/FR-061 withdrawn). | The feature must be written **once**, against final names. **⚠ The sweep is ALLOW-LISTED to the five named identifiers — never a `Owner` prefix match.** `*.OwnerAgentID` on any type is out of scope (O3), and so is any `Owner`-prefixed field another spec adds: `list-jobs-spec.md` introduces `plan.Filter.OwnerAgentID` **in `pkg/plan/store.go`**, the same file this step touches, and `session.LifecycleRecord.ParentAgentID` adjacent to the three fields row 4 and row 5 rename. An `rg`-driven find-and-replace on `Owner` eats them if that spec has already landed (cross-spec M5) — which is the main reason for the landing order below. |
| **3** | **Tool catalog.** **Both** `plan_correct` and `stop_plan` into the **three literals** — `allStaticToolNames` (**and its stale `:274-279` doc comment**), `defaults.go`'s ceilings (**`allow` for both** — see FR-006's two reasons) + `defaults_test.go`'s `wantToolCount`, and the builtin metadata registry. `buildKnownBuiltinToolNames` (`gateway.go:715`) is **derived** and needs no hand-edit. Move the correction types to `pkg/plan` with `pkg/agent` aliases. **SHOULD also replace the hardcoded `wantToolCount` with test #2's mechanical assertion**, so `list-jobs-spec.md`'s `list_jobs` never collides on that line. | `validateOverrideKeys` **panics** if step 4 references a name this step has not registered (FR-006). |
| **4** | **`pkg/coreagent`.** `IDPlanSupervisor`, `PlanSupervisor()`, `SystemAgents()`, `systemAgentIDs`, the `systemAgentSeed` case **naming `plan_correct` and nothing else**, the `Skills: ["plan"]` grant, `PlanSupervisorDefaultRubric` (Appendix A verbatim). **AND FR-006b's `stop_plan` seeding — NEW in rev 3**: `stop_plan` alongside `execute_plan` at the same policy value in every seed map that names it (`core.go:512-514` specialists `ask`, `:620-622` Ava `ask`, `:663-665` Mia `ask`, `:721-723` Ray `ask`, `:814-816` Jim `allow`), plus an **explicit `stop_plan: deny`** in the Worker's sparse map (`:485-496`). | FR-001, FR-002, FR-006b, FR-007, FR-008. **Without FR-006b this step ships `stop_plan` denied to every agent and US-8 is dead on arrival (C3-02) — with a fully green suite, because rev 2 had no `stop_plan` policy test.** |
| **5** | **`pkg/gateway` soul seed.** The `seedJudgeEagerSoul` sibling **only** — no lazy backstop (FR-005, revised). | `SeedConfig` cannot do filesystem I/O. |
| **6** | **`pkg/tools`.** The `plan_correct` tool with its **fully specified parameter schema** (FR-046) and fail-closed func-value setter; the `stop_plan` tool likewise (FR-042). Wire both in `pkg/agent/loop.go`'s `wirePlanToolsForAgent` via `RegisterReplacing`. | FR-004, FR-042. |
| **7** | **`pkg/agent/plan_engine.go` — the engine changes.** The `requireOwner` gate fix + the identity precheck ahead of `planStore.Get` + identical denials (FR-009, FR-010); `validateCorrection`'s new rules — supersede pairing, **the exact criteria-inheritance predicate**, edge acyclicity/endpoints, caps, verb-field matrix, `abandon` (FR-030, FR-030b, FR-046); `validateMemberRef` reordering (FR-047); engine-minted member ids (FR-046); the two-site wake split, **leaving `:1571` alone** (FR-012); the stall-note clear (FR-025); the `processPlan` parked case + supervision deadline + attempt ceiling (FR-021, FR-022, FR-023); the notifier failure / nil-notifier changes with a bounded retry (FR-024); the two new `failed_reason` values and four handovers (FR-035); `reconstructCorrections` fail-closed (FR-048); `StopPlan`'s supervision-session cascade (FR-044); the structured log + widened audit event (FR-039, FR-039b); the supervision gauge (FR-026). **Rev-3 additions, each of which the feature does not work without:** **(a) FR-029** — widen `AppendCorrection`'s phase gate (`:2591-2593`) to the supervision-eligible set and restate FR-021/022/023's predicates over it; **(b) FR-016b** — mint a real, store-backed supervision session and add a `transcriptSessionID` parameter to `wakeOwner`, threaded into `AsyncNotifyEvent.TranscriptSessionID`; **(c) FR-012c** — make the wake path dispatch a turn instead of being dropped by the internal-channel guard; **(d) `plan.Patch`'s five discrete supervision pointer fields** in `pkg/plan/store.go` + their application in `updateLocked` (FR-050). **Rev-4 additions, which (c) is not implementable without:** **(e) FR-012d** — populate `Plan.SourceChannel`/`SourceChatID` on **both** creation write paths (`pkg/tools/plan.go:282-290` from `ToolChannel`/`ToolChatID` **including `webchat`**; `pkg/gateway/rest_plans.go:543-549` leaves both empty), and have `wakeOwner` read its origin from the plan; **(f) FR-016c** — `ensureOwnerSessionLocked` mints a **real, store-backed** session and is called before the first Owner wake, not only at `:1541`. | The engine changes depend on 1, 3, 4 and 6. This is the largest step and should be reviewed as several PRs against the same base. **(b) changes `wakeOwner`'s signature at five call sites — do it in its own PR with #32/#32b/R1.11 green, because a signature change across five wake sites is where a wake silently loses its target.** **(c)+(e)+(f) are one PR and they sequence FIRST, ahead of (a), (b) and (d)**: until a wake starts a turn, none of the other supervision behaviour is observable end to end (N15), and (c) has nothing to propagate without (e) and nowhere to persist without (f). Land them with #31b/#31c/#31d/#31e and §13.4's five wake rows green before anything else in step 7. |
| **8** | **Outcome delivery.** The `pkg/agent`-owned notification interface + its `pkg/gateway` wiring; the layered delivery (FR-014); the `Store.Create` recipient guard (FR-051); click-through on `plan_id` (FR-018 — **no coalescing change**). | Needs the widened contract from step 1. |
| **9** | **Chat-target guards + identity floor.** `rest.go` session create, `websocket.go` chat frame, `rest.go` `default:true` (copying the per-channel `IsSystem()` guard at `rest.go:7667`), `IsSystem()`/`IsChatTarget()` on `AgentInstance`, and `GetDefaultAgent`'s three priorities **plus its degenerate final fallback**; plus FR-049's regression test pinning server-minted agent ids. | FR-003, FR-049. Must **not** be pushed into `GetDefaultAgent`'s callers, `processSystemMessage` or `asyncNotifier` — that would break the supervision wake and `NewVerifierSession`. |
| **10** | **Docs.** `docs/operations/` kill-switch + runbook, written in terms of plan-scoped Stop (FR-045); the spec FR-146 and FR-133 annotations (FR-041, FR-052). | The three documentation deliverables §17 excludes from the test-coverage claim; they still ship. |
| **11** | **Gates.** `make verify-contracts`, `gofmt`, `golangci-lint`, `npm run typecheck`, `vitest`, `govulncheck`, then the e2e gate on the CI worker. | FR-071. |

> **Deviation from ADR-055 §9.** The ADR orders the rename **last** (step 6) and puts the
> notification work at step 4, after its emitters. Both are reversed here: the rename is step 2 and
> the contract widening is step 1, per Constraint #8 and to avoid writing steps 3–9 against names
> step 2 changes. The ADR's step 3 (*"correction tool **and REST route**"*) drops the REST route and
> gains `stop_plan`. The ADR's migrator step is deleted (greenfield).

### Landing order against `list-jobs-spec.md` (ADR-056) — NEW in rev 3

**Both specs land in the same release and touch the same files. The order is:
PS steps 1–3 first, as their own PR; then `list-jobs-spec.md` rebases onto that and proceeds in
parallel with PS steps 4–11.** Do **not** land them in parallel from the same base, and do **not**
land `list-jobs-spec.md` first.

Four reasons, in descending weight:

1. **This spec owns *every* contract change in the pair; the sibling owns none.** Step 1 is a single
   `scripts/gen-contracts.sh` run committing `pkg/api/generated/`, `src/lib/api/generated/` and
   `pkg/gateway/inboundschemas/` atomically (Constraint #8). The sibling's release gate is
   *"`make verify-contracts` green with **no drift**"* — a gate that is only meaningful, and only
   passes cleanly, on a base where that regeneration has already happened. There is **one**
   regeneration, not two.
2. **This spec owns the vocabulary the sibling's status semantics rest on.** The sibling hardcodes
   `awaiting_owner_correction` in six normative places (a requirement, a scenario outline row, a
   scenario title, a test name, a dataset row and a success criterion) and **composes** it at runtime
   as `"running/awaiting_owner_correction"`. Writing it once against final names costs a spec edit;
   retrofitting costs a code edit, a test rename, a dataset row and an SC restatement — in files
   FR-062's sweep did not previously cover (cross-spec C1, closed here by FR-062's `pkg/tools/**` +
   `pkg/agent/**` addition and SC-011's two-spelling alternand).
3. **Step 2's rename sweep runs over the two packages the sibling adds `Owner`-prefixed fields to.**
   Running it on a tree that does not yet contain `plan.Filter.OwnerAgentID` (in `pkg/plan/store.go`,
   the file step 2 edits) or `session.LifecycleRecord.ParentAgentID` (adjacent to the three fields
   rows 4–5 rename) is strictly safer (cross-spec M5).
4. **Only one ordering forces a double-edit.** Sibling-first forces this spec to re-touch its status
   mapping, test names, dataset and SC — work FR-062's sweep would not find, so it would be found by
   hand or not at all. This-spec-first forces the sibling to be authored against final names, which
   is a documentation change made before any code exists.

**Shared prerequisites this spec owns** (the sibling owns the mirror-image list):

| # | This spec's obligation | Closed by |
|---|---|---|
| 1 | Extend FR-062's sweep to `pkg/tools/**` + `pkg/agent/**` for composed literals, and forbid composed phase literals | FR-062, SC-011 |
| 2 | Seed `"stop_plan"` alongside `execute_plan` on every agent that holds it, and assert the **resolved** policy on Jim | FR-006b, SC-004b, #5b, Dataset D7–D12 |
| 3 | Record the PlanSupervisor roster-blindness decision explicitly | D-04, FR-008, §10, §24 |
| 4 | Name `Goal.yaml` and `Plan.yaml` prose in step 1's explicit file list | §18 step 1(g) |
| 5 | Scope step 2's sweep to the named identifiers, never a `Owner` prefix match | §18 step 2 |
| 6 | Document that `stop_plan` aborts an in-flight adjudication | FR-042, FR-045 |
| 7 | Agree the `wantToolCount` handling so the second lander never touches that line | §18 step 3 |

**One number to watch.** `pkg/config/defaults_test.go:92`'s `wantToolCount` is **83** today. This
spec takes it to **85** (`plan_correct`, `stop_plan`); the sibling takes it to **86** (`list_jobs`).
Whoever lands second gets a red test on a line the first lander already edited — which is why step 3
SHOULD replace the literal with test #2's mechanical assertion
(`len(allStaticToolNames) == len(cfg.Sandbox.ToolPolicies)`).

---

## 19. Deferred: Human Correction Parity

FR-5 of ADR-055 deleted the REST correction route and this spec keeps it deleted (O1). When human
parity is taken up, it needs its own decision covering, at minimum:

- **The principal.** FR-011 says the Owner does not adjudicate. If a human may correct, either that
  changes or the authorized principal is somebody else (an admin role?). ADR-055 never said.
- **Per-plan authorization.** `HandlePlans` (`pkg/gateway/rest_plans.go:611-644`) dispatches
  `approve`, `stop`, `restart`, GET/PUT/DELETE with **no owner check on any verb**; `callerIdentity`
  appears only for attribution. A route registered under `withAuth` would grant every authenticated
  user correction rights on every plan.
- **A denial code matching FR-010's opaque form**, so the REST and tool paths cannot be
  differentially probed.
- **`RequireNotBypass` posture.**
- **A rate limit**, because `AppendCorrection` holds the process-wide `planDecisionMu` for its whole
  body (`plan_engine.go:2575-2576`) — the same mutex `processPlan`, `StopPlan`, the judge round and
  idle expiry take.
- **A SPA client.** Without one, a human would hand-author a correction payload containing
  `tail_members` and `tail_edges` as raw JSON. **Note this argument is now weaker than it was in
  rev 1**, and honestly so: FR-046 specifies that payload precisely, caps it, validates its edges for
  acyclicity and mints member ids engine-side — so a form could be built against it. The remaining
  blockers for human parity are the *authority* question (FR-011 says the Owner does not adjudicate,
  so who does?), the missing per-plan authorization on `HandlePlans`, and the rate limit. The payload
  is no longer one of them.

---

## 20. Observability and Operations

ADR-055 has no observability section. This spec requires the following, all cheap:

| Signal | Where | Why |
|---|---|---|
| Structured log per applied correction: plan id, verb, target member, outcome | `AppendCorrection` | FR-039 — the only per-correction record outside the intent log |
| Audit event `plan.correct` naming the actor, plan id and verb | `AppendCorrection` | FR-039b — NFR-5 is not met by a trail only the audited agent can read |
| ERROR log + `supervision.wake_error` on the plan when a supervision wake fails | `wakeOwner` caller | FR-024 — the difference between "parked" and "silently stuck" |
| ERROR log when the supervision attempt ceiling is exhausted, naming the plan | FR-022's terminal branch | The signal that adjudication is *systematically* failing, not just slow |
| ERROR log + fail-closed when a plan's intent log cannot be read at boot | `reconstructCorrections` | FR-048 — silently un-superseding members can flip a DoD verdict to MET (N10) |
| ERROR log when a human notification cannot be created | outcome delivery | FR-014(b); never treated as a lost outcome |
| WARN log when `Plan.Owner` names no configured user | outcome delivery | Explains a missing bell notice without alarming |
| ERROR log when `ensureOwnerSessionLocked` fails to persist | `plan_engine.go:2474` | An empty `OwnerSessionID` silently forfeits the spec FR-118 boot-sweep exemption (N7) |
| **Gauge: plans currently at `awaiting_supervision`** | plan tick (FR-026) | The single number an operator needs at 3 AM |

### Containment — the kill switch (must be documented in `docs/operations/`)

**Containment is plan-scoped. Stop the plan; that stops everything working on it, including
PlanSupervisor.**

- **From the UI:** the ■ Stop button on the plan (`POST /api/v1/plans/{id}/stop`). The plan stays
  `state: running` throughout the parked phase, so `PlanActionButton` renders Stop the whole time.
- **From an agent chat:** the owner agent calls `stop_plan` with the plan id (FR-042).
- **Effect:** `StopPlan` cancels every `in_progress` member's session, every registered verifier
  session, the owner session **and the supervision session** (FR-044), marks in-progress members
  cancelled, and transitions the plan to `failed(stopped_by_user)`. A `plan_correct` arriving after
  the stop is rejected because the plan is no longer running.
- **No redeploy, no restart.** Other plans are unaffected.

> **Do NOT document, build or attempt a tool-policy or agent-level kill switch** (FR-045, O9). Setting
> PlanSupervisor's `plan_correct` policy to `deny` **cannot work**: `updateAgentTools` returns **403**
> for a `Locked` agent (`pkg/gateway/rest.go:6789-6793`), and a hand-edited `config.json` is reverted
> on the next boot by the seeded-policy re-enforcement FR-002 mandates and test #8 asserts. Rev 1
> documented exactly that procedure as *"the only supported way to disable adjudication"*.

### Runbook — "plans are parked and nothing is happening"

1. **How many?** Read the `awaiting_supervision` gauge (FR-026). One plan is a plan problem; all of
   them is a supervisor problem.
2. **Is PlanSupervisor present?** Agents screen → System section. It is `Locked` and not a chat
   target; that is correct.
3. **Does its effective `plan_correct` policy resolve `allow`?** If `deny` on a fresh install, the
   seed override is missing — the dead-on-arrival failure (N1b, RISK-4). This is a **code** defect,
   not an operator setting, because the policy is re-enforced from the seed every boot.
4. **Does its Model/Provider resolve?** An unconfigured model falls back to the install default; a
   model without tool-use support returns 404 on every turn (CLAUDE.md, known blocker 3).
5. **Check the plan records.** `supervision.attempts` climbing with no `correction_rounds` means
   turns are running and producing nothing — look at the rubric and at `plan/SKILL.md`'s verb table.
   `supervision.wake_error` populated means the bus **publish** is failing, not the LLM.
   **`supervision.attempts` climbing with `wake_error` empty and no supervision turn in the
   transcript means the wake is being published and then dropped before a turn runs** — the N15
   failure mode. Check `gateway.log` for `"Subagent completed (internal channel)"` at INFO with a
   `chat_id` of `plan:<id>`: that line **is** the drop. This is a code defect (FR-012c), not an
   operator setting.
   Note `correction_rounds` is **cumulative for the life of the plan** and never resets, while
   `attempts` resets on every applied correction — so `correction_rounds = 4, attempts = 1` is a
   plan that has been corrected four times and is one attempt into its fifth park, not a
   contradiction.
6. **Check `$OMNIPUS_HOME/logs/gateway.log`** for supervision-wake failures, attempt-ceiling
   exhaustion and intent-log read errors.
7. **Contain what you must:** Stop the specific runaway plan (above). Stopping one plan does not
   affect the others.
8. **Last resort:** stop the plan and re-author — the pre-existing path, still supported (O5).

---

## 21. Risks

- **RISK-1 — Spec FR-146 `[P2]` and FR-147 `[P1]` are overridden.** A future reader may follow the
  spec and be surprised. *Mitigation*: §3 records both with verbatim quotes and rationale; FR-041
  annotates FR-146 in place.
- **RISK-2 — Adjudication quality is unmeasured.** No baseline exists for whether PlanSupervisor's
  verdicts beat the status quo. *Mitigation*: the success criteria measure the **capability**, never
  a completion-rate improvement (which §1 explicitly declines to predict).
- **RISK-3 — A missed occurrence in the compiler-invisible surface.** Under greenfield the
  data-migration risk is retired; what remains is that the rename touches `src/**` string literals,
  `plan/SKILL.md`, four `inboundschemas/*.yaml` mirrors and the e2e specs, none of which `go build` or
  `tsc -b --noEmit` checks. A missed literal is a runtime chip that renders the wrong copy, or an e2e
  fixture that silently stops exercising the path. *Mitigation*: FR-062's sweep list now includes
  `src/**`, and SC-011 **is** the command rather than a description of one, so the requirement and the
  criterion cannot drift. **This replaces ADR-055's R5, which still describes the rename as
  deferred.**
- **RISK-4 — Dead on arrival on a fresh install.** If `plan_correct` is not named in
  PlanSupervisor's seed override, `denyAllThenOverride` denies it and nothing else fails (N1b).
  *Mitigation*: SC-004 asserts the **resolved** policy on a fresh install, not the seed literal.
- **RISK-5 — Inert on an upgraded install.** A pre-existing custom agent resolves `allow` from the
  ceiling (N2). *Mitigation*: FR-008 makes the engine gate the primary control and Dataset B row B5
  tests it with a pre-existing agent.
- **RISK-6 — `judge_rounds_exhausted` still carries two meanings.** *Mitigation*: FR-035 splits the
  two genuinely-different terminal causes into their **own** enum values (`dod_unreachable`,
  `supervision_unavailable`), leaving `judge_rounds_exhausted` with two causes that
  `supervision.correction_rounds` distinguishes in the message. Rev 1 tried to carry three meanings
  on one value and could not, because two of them are one predicate at one line.
- **RISK-7 — WITHDRAWN.** Rev 1: *"the lifecycle-record rename has no safe migration"*, mitigated by
  *"AMB-1 recommends descoping it"*. Both are overruled — no migration is performed, so there is no
  unsafe migration to risk, and the five live S9 rows ship. The residual concern (that the rename must
  keep `boot_sweep.go:295-296`'s single live gate intact) is a **compiler-checked struct rename** with
  a named regression row, R1.4b.
- **RISK-8 — `OwnsPlanID` has no non-test writer, so boot-sweep exemption (b) is dead in production.**
  Verified: every assignment in the repo is in `boot_sweep_test.go` and `conformance_design_test.go`;
  the only production read is `boot_sweep.go:160-161`. The **live** protection for a parked
  `needs_input` session is `OwnerScopeKind` alone (`:295-296`). Consequences: (a) S9 **row 4** is the
  consequential rename and **row 5** is the free one — the opposite of how ADR-055 D14 and rev 1's
  AMB-1 weighted them; (b) regression row R1.5b is a **synthetic fixture**, not a live behaviour.
  Retained as tidiness debt, deliberately accepted (O4). Note D7's claim these are *"unused"* is
  false for `OwnerSessionID` (C2).
- **RISK-9 — The supervision prompt is the only control for behaviours the engine cannot enforce.**
  Verb selection and stall-vs-UNMET discrimination live in the SOUL. *Mitigation, strengthened in
  rev 2*: the rubric is **written** (Appendix A) rather than deferred; FR-030 **and FR-030b** move the
  security-relevant behaviour (supersede discipline) out of the prompt and into `validateCorrection`;
  FR-040 removes the `plan/SKILL.md:181` line that told the agent the *opposite* of what the engine
  enforces, with test #25 and SC-015 asserting the fix; and FR-025 stops feeding a stale stall
  diagnosis into the wake that most needs a clean one.
- **RISK-10 — A P1 story blocks a P0 one.** FR-070 makes US-7 (the rename, P1) a hard prerequisite
  for US-1 (the closed loop, P0). *Accepted, with the argument stated rather than assumed*: under
  greenfield the rename is a compiler-driven edit plus one mechanical sweep, with no migrator; the
  only genuine coupling is `plan/SKILL.md:158` holding the phase literal; and writing steps 3–9
  against a literal step 2 changes means writing them twice. If the rename slips, the correct
  response is to reorder — not to ship the feature against the retired vocabulary and rename later,
  which is how the wrong name got frozen into a wire enum the first time.
- **RISK-11 — Adjudication quality is only measured by a non-deterministic test.** *Mitigation,
  revised in rev 3 (D-07)*: the claim is **split**. SC-001 (the loop closes) is asserted by #60b, an
  integration test with a **scripted adjudicator double**, and that is the blocking merge gate — so a
  provider outage cannot block a merge. SC-001b (the rubric works) is asserted by #60 against a real
  provider as a **nightly signal**; a failure is a rubric defect tracked as an issue with a target
  date, never auto-quarantined. *(Rev 2 put the non-deterministic run in the blocking gate with a
  2-retry policy — r3 O3-03. Splitting removes the provider from the merge path without weakening
  either claim; the residual risk is that a rubric regression is caught a day later, which is the
  correct trade for a rubric explicitly shipped as a first draft.)*
- **RISK-13 — `Plan.OwnerSessionID`'s session does not exist. ~~and this spec does not fix it~~
  → REWRITTEN IN REV 4: this spec now fixes the minting half.**
  Verified: nothing in `pkg/` creates a session named `plan:<id>` (**N13**), so `StopPlan`'s
  owner-session cancel is a no-op and `requireOwner`'s clause 3 would deny every caller if it were
  ever reached. **FR-016c mints a real, store-backed owner session** — required because FR-012c(B)
  has to persist an Owner wake's synthesis somewhere, and a plan with no chat origin (D-10) has
  nowhere else. *Mitigation*: FR-009's early return for PlanSupervisor makes clause 3 unreachable for
  the only principal that matters; both sessions are now required to be real (FR-016b, FR-016c); the
  `StopPlan` owner leg's transition from no-op to real is pinned by **#63c** rather than assumed;
  #63b's sibling assertion pins the two sessions as distinct (FR-016). *Residual, narrowed*:
  boot-sweep exemption (b) stays dead — nothing writes `LifecycleRecord.OwnsPlanID` in production
  (S9 row 5) and rev 4 does not add a writer, so a paused owner session is **not** exempt from the
  boot sweep on that path. That was already true before this change and is not made worse by it, but
  it is now the *only* piece of N13 left unrepaired, and the next author is likelier to assume it was
  fixed alongside the rest. **The compensating control is the plan record itself**: FR-118's
  awaiting-correction exemption resolves through `Plan.OwnerSessionID`, which is durable, and FR-023
  re-wakes a parked plan at boot regardless of the session's sweep outcome.
- **RISK-14 — Jim's `execute_plan: allow` resolves to `ask`, and this spec does not fix it.**
  A pre-existing ADR-052 defect (**N14**), flagged in-tree at `defaults.go:375-386` as out of that
  file's ownership. *Mitigation*: `stop_plan`'s ceiling is `allow` (FR-006), so the same trap does
  not bite the tool this spec adds, and SC-004b asserts the **resolved** value rather than the seed.
  *Residual*: an operator reading `core.go:810-813`'s comment will believe Jim has unprompted plan
  execution and be wrong. Recorded in O12.
- **RISK-15 — FR-012c is a change to a shared delivery path.** Making plan wakes reach a turn
  (**N15**) touches `wakeOwner`, `AsyncNotifyEvent` threading and how the plan case reaches
  `processSystemMessage` — machinery shared with the task-executor and delegate wake paths.
  *Mitigation*: FR-012c(4) forbids widening the internal-channel guard for `cli`/`subagent`; §13.4
  adds a regression row for that guard; and #31c pins the Owner wake sites.
  **Rev 4 materially *reduces* this risk, and that is the main practical argument for D-09's
  mechanism.** The fix is now confined to *what `wakeOwner` puts into the event it already
  publishes* (FR-012d) plus a direct dispatch that bypasses the shared path entirely for the
  supervision family (FR-012c(A), D-11). **`processSystemMessage`, `AsyncNotifier.Notify`, the
  internal-channel guard and `bus.InboundMessage.Channel` are all left untouched** — so the
  task-executor and delegate wake paths that share them cannot regress by construction, not merely
  by test. Two new §13.4 rows pin that: one on the guard, one on the bus channel.
  *Residual, stated rather than hidden*: it remains the highest-blast-radius change in the feature,
  it is taken because US-4 is P0 and currently delivers nothing (D-08, operator ruling 9), and
  FR-016c additionally changes the value shape of a field (`Plan.OwnerSessionID`) that `requireOwner`
  clause 3 and the FR-118 exemption both read — bounded by O11's narrowed scope and pinned by #63c
  and §13.4's idempotence row. It should be its own PR with the §13.4 wake rows and #31b–#31e green
  before anything else in step 7 lands.
- **RISK-16 — The *Task* goal-loop wake has the same defect, and this spec does not fix it.**
  `wakeOwnerAttemptsExhausted` falls back to `Channel: "system"`, `ChatID: "task:"+t.ID` when a Task
  has no origin (`task_executor.go:1066-1069`) `[FACT — verified 2026-07-28]` — which N15's chain
  drops at the internal-channel guard exactly as the plan wake is dropped. So a Task that exhausts
  its goal-loop attempts wakes nobody, on the same rail, for the same reason. **Not fixed here**, on
  D-08's stated test: no requirement in this spec depends on it, and Tasks are ADR-053 /
  `list-jobs-spec.md` surface. *Mitigation*: FR-012d's `Plan` fields are a copy of the `Task` fields
  that already exist, so the Task-side repair is the same three-line change against machinery this
  release will have already proven; §13.4 records the row so it is not mistaken for covered.
  *Residual*: an operator who sees plan wakes start working may reasonably assume task wakes did
  too. **Recommend filing it as a follow-up issue against ADR-053 when this lands** — see the
  ADR-055 amendment note in §3.
- **RISK-12 — The rubric is a first draft.** Appendix A is written to good prompt-engineering
  practice but is untuned against real adjudications; RISK-2 (adjudication quality is unmeasured)
  compounds it. *Mitigation*: it is versioned in-repo as a const, materialised into an
  operator-editable `SOUL.md` that is never overwritten (FR-005), and holdouts H1/H2 evaluate its
  behaviour externally. Tuning it is expected and does not require a spec change.

---

## 22. Ambiguity Warnings

### Resolved in rev 2

| # | Was | Resolution |
|---|---|---|
| **AMB-1** | Whether the session-lifecycle renames ship this release; rev 1 recommended descoping D14 rows 4–5 | **CLOSED — OVERRULED** by operator ruling 3. The recommendation was correct *given* a migration constraint; that constraint does not exist. **Five S9 rows ship** (rows 1, 4, 5, 6, 7). See the S9 rows table and RISK-8, which also corrects which row is the risky one. *(Rev 3: row 3 is dropped by D-02 — a naming decision, not a migration one, so this closure is unaffected.)* |
| **AMB-2** | Whether `Plan.OwnerSessionID` is renamed, given `pkg/tools`' unrelated `OwnerSessionID` | **CLOSED — and REOPENED-THEN-RESOLVED DIFFERENTLY IN REV 3.** Rev 2 renamed **both**: row 3 (`pkg/plan`) to `SupervisionSessionID` and row 7 (`ProcessSession.OwnerSessionID` → `TranscriptSessionID`). **Rev 3 drops row 3 (D-02)**, because `SupervisionSessionID` collided with FR-050's `supervision.session_id` — a different session — putting two "supervision session" names in the story about naming (r3 C3-04). **Only row 7 is renamed**, and it alone achieves what AMB-2 asked for: after it, `OwnerSessionID` means exactly one thing. Do the rename per-row, never as a repo-wide replace. |
| **AMB-3** | The exact `PlanSupervisorDefaultRubric` text — *"the actual implementation of the adjudicator"* | **CLOSED — WRITTEN.** Operator ruling 2: *"apply good prompt engineering techniques, we can optimise it later but do your best."* Full text in **Appendix A (§27)**, derived from `plan/SKILL.md:156-219` so the two cannot drift, and shaped after `JudgeDefaultRubric`. Marked explicitly as a **first draft open to tuning** (RISK-12). |
| **AMB-4** | The supervision timeout value | **CLOSED: 10 minutes**, a new `PlanningConfig` field `supervision_turn_timeout_seconds` (default 600), per-plan overridable. Explicitly **not** the 10 s `wakeOwner` notify timeout, which bounds a bus publish. See FR-021. Rev 1's AMB-4 asked only for the *value*; the harder half — who arms it, who checks it, what cancels it — is FR-021, and is a different gap. |
| **AMB-6** | Whether corrections need an audit entry | **CLOSED: yes** (FR-039b). Verified that `auditPlan` carries six events, none for correction, none recording an actor. Granting an autonomous agent a privileged mutation verb whose only trace is a structured log plus an intent log **nobody can read** (FR-037) does not meet NFR-5. |
| **AMB-5** | Whether `plan_correct` returns the `RevisionEntry` or a plan-revisions read route is added (FR-037) | **CLOSED — no route in v1 (D-03).** The tool returns the `RevisionEntry` in its result (free — `AppendCorrection` already produces it via `CorrectionResult`), **and FR-039b's audit entry is widened** to carry actor, plan id, verb, **target member id** and **falsified assumption**, which is what gives an *operator* a reader on a surface that already ships (`GET /api/v1/audit-log`, `pkg/gateway/rest.go:4883`). US-6 AS1, test #43 and SC-018 are restated against it. Rev 2 left this open while US-6 AS1 and test #43 measured the deferred route, so NFR-5 was unmet by construction (r3 M3-08). A REST read route is not added, because it inherits O1's unresolved per-plan authorization story on `HandlePlans`. |
| **AMB-9** | Whether `Plan.Owner` or `Plan.CreatedBy` is authoritative for the human notice | **CLOSED: `Owner`**, per FR-014, and the contract description is corrected in step 1. The equality claim rev 1 made (*"provably always equal today"*) is **not** relied on: the contract documents them differently (`owner` = *"Username of the user who created this plan"*, `readOnly`; `created_by` = *"Username (or agent ID)"*), and `CreatedBy` additionally drives the tiered-DoD gate. Routing on `Owner` is correct **because** it is the attribution field the contract types as a username, which is exactly what `ListForUser` keys on — not because the two happen to be equal. |

### Still open — none is blocking, each has a stated default

| # | What's ambiguous | Default this spec ships | Question to resolve |
|---|---|---|---|
| **AMB-12** *(new in rev 3)* | **Whether the SPA renders the two `judge_rounds_exhausted` sub-causes differently.** FR-035 tells them apart by `supervision.correction_rounds`, which is not on the enum path `planSecondaryChipLabel` switches over (r3 m3-08). | **The handover text distinguishes them; the SPA badge does not.** `planSecondaryChipLabel` continues to render one label for `judge_rounds_exhausted`, and §13.4 lists it as a d=1 dependent of the enum change only for the two **new** values. The two-way branch is a *message* concern, not a badge concern. | Should the plans list surface "corrections consumed the budget" distinctly from "the ceiling was reached"? Deferred — it is a UI refinement with no correctness consequence, and the handover the Owner receives already says which. |
| **AMB-7** | **Notification visibility.** There is no bell — the entry point is a `Tray` item inside the sidebar profile dropdown (`Sidebar.tsx:684-696`), two clicks deep and off-screen when the drawer is closed. | Reuse the existing Tray entry point (O8). | Is an ambient indicator in scope for a later release? |
| **AMB-8** | **Human notification on a single-user / `dev_mode_bypass` install**, where `Gateway.Users` is empty so FR-014(b) never fires. | Deliver only the bus wake; documented (E12b, Dataset C7), not an error. | Should a bypass install fall back to an admin-broadcast notification? |
| **AMB-10** | **Whether an `ask` resolution for a System Agent should be a boot-time validation error.** A System Agent has no human to answer an "ask", so an `ask` deadlocks it silently. | **Treat `ask` on a System Agent as a boot WARN and behave as `deny`** (park, notify the Owner via FR-019's path) — it cannot occur through the seed, which stamps `allow`, and `updateAgentTools` 403s a `Locked` agent, so it is reachable only by a hand-edited `config.json`. | Should it be a boot-time **abort** instead of a WARN? Raising it costs nothing and is consistent with the tool-policy coverage abort; it is left open because it widens a boot-abort surface beyond this feature's scope. |
| **AMB-11** *(new)* | **Whether `supervision_max_attempts` = 3 is the right ceiling.** It bounds unproductive supervision turns per park (FR-022). Too low and a transient provider blip terminates a salvageable plan; too high and a systematically-confused adjudicator burns LLM turns. | **3.** With a 10-minute deadline that is up to ~30 minutes of parked time before the plan terminates `supervision_unavailable`, which is well inside the 7-day idle-expiry budget. | Confirm 3, or tune after observing real adjudication failure rates (the same "optimise later" posture as the rubric). |

---

## 23. Evaluation Scenarios (Holdout)

> **HOLDOUT — post-implementation only.** These MUST NOT be referenced in the TDD plan (§13) or the
> traceability matrix (§17), and MUST NOT be shown to the implementing agents. They are written from
> an external observer's perspective and are evaluated by a human or an external script against a
> running binary.

### H1 — A real plan recovers from a real mistake
- **Setup**: A fresh install. Author a 3-member plan whose DoD requires a file to contain a specific
  string. Arrange for member 2 to write the wrong string.
- **Action**: Run the plan to completion and wait.
- **Expected outcome**: Without any human input, the plan ends in a terminal state, and the file
  contains the required string. Inspect the plan's history: at least one correction was applied and
  it added work rather than only discounting evidence.
- **Category**: Happy Path

### H2 — The supervisor cannot cheat
- **Setup**: A plan whose DoD requires a machine-checkable condition that no member can satisfy,
  with one `done` member whose output is the reason the criterion fails.
- **Action**: Let the plan run until it terminates.
- **Expected outcome**: The plan does **not** end in `done`. Its terminal reason and handover explain
  the Definition of Done could not be met. Verify the machine-checkable condition is still false.
- **Category**: Edge Case

### H3 — A human author is told their plan finished
- **Setup**: Log in as a normal user. Create a plan through the Plans UI. Let it complete.
- **Action**: Without navigating to the plan, open the notifications surface.
- **Expected outcome**: A notification about that plan is present, and clicking it navigates to the
  plan.
- **Category**: Happy Path

### H4 — Borrowing the supervisor's authority fails
- **Setup**: A running install with at least one parked plan.
- **Action**: As an authenticated user, try every route you can find to make PlanSupervisor act on
  your behalf — open a chat session against it, star it as your default agent, ask another agent to
  correct the plan, and create a new custom agent and ask it to correct the plan.
- **Expected outcome**: Every attempt fails. No correction is applied by anything other than
  PlanSupervisor's own autonomous turn.
- **Category**: Error

### H5 — Pull the plug mid-adjudication
- **Setup**: A plan parked awaiting supervision.
- **Action**: `kill -9` the gateway. Restart it. Wait.
- **Expected outcome**: The plan is adjudicated. Compare the plan's round counter before and after
  the restart — the restart itself consumed none.
- **Category**: Error

### H6 — The supervisor gives up honestly rather than churning
*(Replaces rev 1's H6, which tested an upgrade path that no longer exists under greenfield.)*
- **Setup**: A plan whose DoD requires something genuinely impossible in the workspace — e.g. a file
  in a directory the sandbox denies — with all members already terminal.
- **Action**: Let the plan run and wait. Do not intervene.
- **Expected outcome**: Within roughly half an hour the plan is in a terminal state, **not** still
  parked. Its terminal reason distinguishes *"the Definition of Done is unreachable"* from *"the
  round budget ran out"* from *"adjudication never produced a usable correction"* — read the reason
  and confirm it matches what actually happened. The plan's owner was told. Count the LLM turns
  billed against this plan and confirm the number is bounded, not open-ended.
- **Category**: Edge Case

### H7 — Contain a runaway plan at 3 AM
*(Replaces rev 1's H7, which tested a tool-policy kill switch that cannot exist.)*
- **Setup**: A running install with at least two plans parked awaiting supervision, one of which you
  will treat as the runaway.
- **Action**: Using only the UI and the operations docs — no code, no config edit, no restart — stop
  the supervisor from doing anything further **to that one plan**, while it is mid-adjudication.
- **Expected outcome**: You succeed. The runaway plan is terminal and nothing further happens to it —
  no correction lands after your action. **The other parked plan is untouched and is still
  adjudicated normally.** Then confirm the reverse direction: nothing you did requires undoing, and
  new plans still get supervision. Separately, try to find a way to disable the supervisor globally
  through the UI — you should not find one, and the docs should tell you why containment is
  per-plan.
- **Category**: Error

### H8 — The supervisor's reasoning does not leak into the owner's conversation
- **Setup**: A plan owned by an agent you can chat with, driven to at least one supervision wake and
  one applied correction.
- **Action**: Open a chat with the owner agent and read its transcript for that plan. Then read the
  plan's revision history and the audit log.
- **Expected outcome**: The owner's transcript contains the plan's outcomes and its own synthesis,
  but **not** the adjudicator's deliberation. The adjudicator's decisions are nevertheless fully
  accounted for in the revision history and the audit log — you can answer *"why did this plan
  change?"* without reading JSONL by hand and without asking the supervisor.
- **Category**: Edge Case

### H9 — A plan that simply succeeds still tells someone
- **Setup**: Log in as a normal user. Create a plan through the Plans UI whose DoD is easily met.
- **Action**: Let it complete. Do not navigate to the plan.
- **Expected outcome**: The owner agent's transcript shows the closing synthesis, and a notification
  about the plan is present for you. Neither depends on any correction having occurred — this is the
  ordinary success path, and it must not have been quietly traded away for the correction loop.
- **Category**: Happy Path

---

## 24. Assumptions

- **Greenfield.** Existing on-disk data is expected not to load. Any dev/UAT `$OMNIPUS_HOME` is
  recreated rather than upgraded. No migrator, no `schema_version`, no upgrade-on-read, for any store
  (operator ruling 3).
- The gateway runs on POSIX. Per CLAUDE.md, `fileutil.WithFlock` is a **no-op on Windows**, so
  cross-process file safety in the plan and lifecycle stores is POSIX-only. This spec adds no new
  cross-process requirement.
- `PlanJudgeMaxRounds` remains at its default of 20 unless an operator overrides it per plan; it
  bounds applied corrections and judge rounds together (FR-034). `supervision_max_attempts`
  (default 3) separately bounds unproductive supervision turns per park (FR-022) — the two ceilings
  bound different quantities and neither substitutes for the other.
- The plan loop ticks every **30 s** (`defaultPlanEngineTickInterval`), a package const, not a config
  key. Every "tick" in this spec means that (FR-027).
- `create_plan` remains a granted agent tool, so agent-created plans remain the majority case. Under
  FR-042 such an agent now also holds `stop_plan` for the plans it owns.
- No SPA correction UI ships in this release; the only correction actor is PlanSupervisor (O1, O10).
  Human **containment** does ship — the existing Stop button, extended by FR-044.
- `plan/SKILL.md`'s re-planning checklist (`:156-219`) is behaviourally adequate for correction —
  it already diagnoses the failure and maps it to SUPERSEDE / TARGETED-RETRY / APPEND, and already
  teaches DoD immutability. **With one correction (FR-040):** its verb table at `:181` currently says
  a replacement tail member is *"Optionally"* appended, which contradicts what `validateCorrection`
  enforces; the SOUL cannot compensate for a skill that instructs the opposite of the engine, so the
  skill is amended rather than worked around. The residual gaps the SOUL covers are the **stall** wake
  (the checklist is entered only on UNMET), the fact that the corrector is a different actor from the
  plan's author, and the `abandon` verb (which the checklist's §5 describes as an engine-side
  behaviour and which is now the adjudicator's own move).
- Agent ids are server-minted UUIDs and are never operator-chosen (N12). FR-009's exact-identity gate
  depends on this; FR-049 pins it.
- The existing plan-owner validators (`validatePlanOwnerAgent`, `validatePlanOwnerAgentForTool`)
  continue to reject System Agents, so PlanSupervisor can never itself become a plan's
  `owner_agent_id` — the adjudicator and the owner can never be the same principal.
- Operator-editable fields (Model, Provider, SOUL, and only those) survive boot; everything else is
  repaired.
- **PlanSupervisor is roster-blind by decision, not by omission** (D-04). It is woken per plan and
  adjudicates only the plan it was woken for. The engine's `supervision.wake_at` deadline is the sole
  liveness control and it belongs to the engine. Granting it `list_jobs` or any plan-enumeration tool
  requires deliberately amending FR-008 and test #4 — the complement-complete assertion failing is
  the guard working.
- **Two verified in-tree defects are recorded and deliberately NOT fixed here**, because no
  requirement in this spec depends on either: the `plan:<id>` owner session that nothing creates
  (N13, O11, RISK-13) and `execute_plan`'s ceiling merging Jim's `allow` down to `ask` (N14, O12,
  RISK-14). **N15 is the contrasting case** — it *is* fixed, because US-4 AS1, SC-010, FR-012 and
  FR-019 all depend on a plan wake reaching an agent (D-08). "Does a requirement in this spec depend
  on it" is the stated line between the two.
- The four `plan_correct` payload caps ship as **package constants**, not config fields and not
  per-plan overridable (D-06). Only the two supervision timings are configurable.
- CI, not this pod, is the authority for Go test results. No full local suite is run.

---

## 25. Clarifications

### 2026-07-27

- **Q**: Is PlanSupervisor a new System Agent or an extension of `plan/SKILL.md`?
  **A**: A new System Agent, deliberately overriding spec FR-146 `[P2]`. The *capability* is reused
  by granting it the skill; only the *actor* differs. Rationale in §3.
- **Q**: Does the Owner keep correction rights?
  **A**: No. Correction is PlanSupervisor's alone; the Owner stops, cancels or resumes (FR-011).
- **Q**: Do corrections get their own round budget?
  **A**: No — they share `PlanJudgeMaxRounds`. The correction itself does not increment; the
  re-judge it provokes does (FR-034).
- **Q**: What happens when PlanSupervisor is unavailable?
  **A**: The plan stays parked and the `owner_agent_id` is woken. No other agent inherits
  adjudication (FR-019).
- **Q**: Does the phase rename ship in this release?
  **A**: Yes — **with no migrator**. Five S9 rows ship (row 2 was never in scope; row 3 is dropped by D-02). Greenfield (operator ruling 3): existing
  records are expected not to load, and that is accepted. It overrides the literal in spec FR-147
  `[P1]` while preserving every structural requirement of it.
- **Q**: Does this ADR ship a REST correction route?
  **A**: **No.** ADR-055 FR-5 deleted it; three other sections of the ADR still describe it and are
  wrong (C1). Human parity is deferred with a stated checklist (§19).
- **Q**: How does the engine distinguish an agent Owner from a human Owner?
  **A**: **It does not need to.** The two concepts already live in two fields —
  `owner_agent_id` (required, always an agent, validated) and `owner` (creator attribution). ADR-055
  D2's premise that `Plan.Owner` carries a dual-kind principal is **false** against the contract, so
  D5's fork is unnecessary. No discriminator, no wire addition, no migration (C6, FR-013/FR-014).
  **ADR-055 D2 and D5 should be amended.**
- **Q**: Can a `supersede` be paired with a `targeted_retry` of the same member, as ADR-055 D16
  proposes?
  **A**: **No — impossible by construction.** `validateMemberRef` requires `StatusDone` for
  supersede and `StatusFailed` for targeted_retry. The feasible control is requiring
  `len(TailMembers) > 0` on the supersede case, which composes atomically inside one
  `CorrectionRequest` because `AppendCorrection` applies `TailMembers` verb-independently
  (FR-030). **ADR-055 D16 should be amended.**
- **Q**: Is forgetting PlanSupervisor's tool grant a silent privilege *escalation*, as ADR-055 R3
  says?
  **A**: For this agent it is the **opposite**, and both directions are real. On a fresh install
  `denyAllThenOverride` stamps an explicit `deny` for every catalog name, and a per-agent `deny`
  beats the global `allow` — so an unnamed `plan_correct` ships **denied to PlanSupervisor itself**
  and the loop is dead on arrival (N1b). The escalation risk R3 describes applies instead to agents
  **persisted before the tool name existed**, which inherit the ceiling's `allow` (N2). ADR-055 D8
  and R3 describe only the second.

### 2026-07-27 (rev 2)

- **Q**: How does an operator stop PlanSupervisor?
  **A**: **They stop the plan.** Containment is plan-scoped, not agent-scoped (operator ruling 1).
  Stop already cascades to every member and verifier session; FR-044 adds the supervision session, so
  the same ■ button that stops a plan now halts the supervisor working on it. An agent that started a
  plan gets the same power via the new `stop_plan` tool. A *global* agent-disable is not built —
  it is both the wrong shape (it strands every other parked plan) and impossible (403 on write,
  reverted on boot). See US-8, O9, FR-042–FR-045.
- **Q**: Who wakes the Owner when a plan succeeds?
  **A**: `synthesizeAndComplete` does, at `plan_engine.go:1571`, exactly as it does today —
  **the spec no longer re-targets it**. It is the only wake on the success path, so re-targeting it
  would have made a successful plan notify nobody. The wake split is now by *"who must decide whether
  to correct"*: only the stall and UNMET wakes move (C15, FR-012).
- **Q**: How does the engine learn that PlanSupervisor's turn finished, succeeded or failed?
  **A**: **It does not, and it does not need to.** The wake is fire-and-forget with no callback
  (N8). The engine observes whether the *plan record moved* — a deadline armed on
  `supervision.wake_at` and checked on a later tick. That single predicate covers "timed out" and
  "produced nothing", and subsumes "provider error" (an errored turn produces no correction). FR-021.
- **Q**: What happens after a turn that emits no valid correction?
  **A**: The wake is re-issued on a later tick, up to `supervision_max_attempts` (3), after which the
  plan terminates `failed(supervision_unavailable)` and the Owner is told. Rev 1 had no answer: the
  wake fired once per park, a rejected correction mutated nothing and charged no round, so the first
  bad tool call stranded the plan until idle expiry. FR-022.
- **Q**: How does a plan correctly diagnosed as un-correctable terminate?
  **A**: Through a fourth `plan_correct` verb, **`abandon`**, carrying the falsified assumption. It
  mutates no member and terminates the plan `failed(dod_unreachable)`. US-1 always promised an honest
  exit; rev 1's only honest exit fired *inside* a successfully-applied correction, so a supervisor
  that concluded "nothing can fix this" triggered nothing. FR-046.
- **Q**: What is `plan_correct`'s parameter schema, and who mints member ids?
  **A**: Specified field by field in FR-046. **The engine mints member ids**, not the LLM — a
  caller-supplied id that collides with an existing member is silently skipped by
  `buildCorrectionApplyFunc`'s replay path, the correction reports success, and the work is never
  created. `tail_edges` is validated for acyclicity, endpoint resolution and superseded endpoints,
  and both collections are capped. Rev 1 mentioned `tail_edges` exactly once, in §19, in an argument
  about humans.
- **Q**: Do corrections get an audit entry?
  **A**: **Yes** (FR-039b, closing AMB-6). The intent log has no reader — the generated producer has
  zero call sites — so without an audit event the only reader of the trail would be the agent being
  audited, which does not meet NFR-5 for a privileged autonomous mutation verb.
- **Q**: Can someone create an agent called `plansupervisor` and inherit correction rights?
  **A**: **No.** Agent ids are `uuid.New().String()`, server-minted, never operator-chosen, and
  `{"type":"system"}` is rejected 400. The property already holds; FR-049 pins it with a test because
  FR-009's whole integrity claim rests on it.
- **Q**: Does the rubric ship written, or drafted at implementation time?
  **A**: **Written** — Appendix A (§27), per operator ruling 2. It is derived from
  `plan/SKILL.md:156-219` so the prompt and the skill cannot drift, and it is explicitly a first
  draft open to tuning (RISK-12).

---

## 26. Review Disposition

All three spec-grill rounds **and** the cross-spec conflict review are dispositioned here so a
re-grill can check the work rather than re-derive it. **13 CRITICAL, 34 MAJOR** across the three
rounds, plus **3 CRITICAL / 6 MAJOR** cross-spec, of which this spec owns 8.

Rounds 1 and 2 are first; **round 3 and the cross-spec review follow, after the "did not reproduce"
table** — one row of which rev 3 **retracts**.

### Round 1 + 2 — CRITICAL

| ID | Finding | Disposition |
|---|---|---|
| **C-01** | The kill switch is 403'd by the `Locked` guard and reverted by the boot re-enforcement FR-002 mandates | **Fixed by replacement.** Containment re-specified as **plan-scoped Stop** (operator ruling 1): US-8 rewritten, FR-042–FR-045 added, NFR-6 replaced, SC-020 replaced, §20 runbook rewritten, H7 replaced, O9 added recording *why* the agent-scoped control is impossible. No `Locked` exemption is carved. |
| **C-02** | Three handover messages demanded from two causes that are one predicate, using a counter FR-034 forbade | **Fixed.** `supervision.correction_rounds` added (FR-050) and FR-034 amended to permit it, with §10 stating the precise scope (attribution counter, not a budget). Causes 3 and 4 get their **own `failed_reason` values** — better than three strings on one enum. FR-035, SC-017, test #62 and the Scenario Outline all rewritten. RISK-6 amended. |
| **C-03** | The existence oracle sits on the plan-load path `requireOwner` never reaches; SC-002's arithmetic contradicts Dataset B; byte-identity unachievable | **Fixed, all four limbs.** FR-010 rewritten to (1) unify the three branch messages, (2) **remove the plan id from the message**, (3) normalise the plan-load failure for non-PlanSupervisor callers via an identity precheck before `planStore.Get`, (4) return the real not-found to PlanSupervisor. SC-002 corrected to **2 allowed / 7 denied** and restated as identical class + body. B7 split into B7 (unauthorised) and B7b (authorised). New BDD scenario for B7b. |
| **C-04** | Greenfield kills ~18 % of the spec; D14 rows 3–7 named nowhere; three sections cite a non-existent "S9 rows" table | **Fixed, all three limbs.** (a) FR-060/FR-061, Dataset E, tests #20–#22/#24, four BDD scenarios, SC-012/SC-013, RISK-7, H6 and R1.1/R1.4/R1.5/R1.6 all withdrawn or replaced. (b) A full **S9 rows table** added to §2 enumerating all seven rows with before/after identifiers, live consumers, wire impact and per-row notes. (c) The three dangling "S9 row N" references now resolve to it. AMB-1 and AMB-2 closed. |
| **C2-01** | FR-019's "unavailable" has three limbs the engine can observe none of; no seam specified | **Fixed.** N8 records why a callback is the wrong seam. FR-021 specifies the deadline armed on `supervision.wake_at`; FR-019 restated as **one observable predicate**; the provider-error limb deleted as subsumed; AMB-4 closed at 10 min. Tests #36b added. |
| **C2-02** | One wake per park + a rejected correction mutating nothing = permanent strand; no exit for an un-correctable plan; SC-003 unreachable | **Fixed, all three.** FR-022 adds the re-wake and the `supervision_max_attempts` ceiling with its own terminal reason. FR-046 adds the **`abandon`** verb as a reachable honest exit. SC-003 restated against the property that matters, with test #30 driving real corrections. New scenarios and Dataset E rows E4/E6/E7/E8. |
| **C2-03** | FR-012 re-targets the MET-synthesis wake, so a plan that succeeds notifies nobody | **Fixed by not doing it.** Independently verified: `synthesizeAndComplete` (`:1561-1582`) holds the only success-path wake. FR-012 now moves **exactly two** sites and leaves `:1571` on the Owner, on correctness *and* merits grounds. C15 records the ADR contradiction; FR-012b, test #32b, regression row R1.11 and the outcome Scenario Outline (with a `done` row) prevent regression. **ADR-055 D5/§9 should be amended.** |
| **C2-04** | `plan_correct`'s parameter schema never written; `TailEdges` in no dataset, scenario, edge case or test | **Fixed.** FR-046 specifies the schema field by field, adds cycle/endpoint/superseded/cap/verb-matrix validation, and makes member ids **engine-minted** (retiring the silent-collision class rather than validating around it). Dataset rows A18–A27, scenarios, tests #20–#22/#24, E27–E29, SC-023. A16 resolved to a single expected outcome. |

### Round 1 + 2 — MAJOR

| ID | Disposition |
|---|---|
| **M-01** | FR-040 extended to a **third** site, `SKILL.md:181`; test #25 and SC-015 now assert three absences **and one presence**; §18 step 2 names it. *(Rev 3 adds a **fourth** site and a **second** presence — see M3-10.)* |
| **M-02** | FR-005's lazy backstop **dropped** with the reason (`ensureVerifierSoul` is Judge-gated with one call site on a path PlanSupervisor never takes) and the accepted consequence stated |
| **M-03** | The durable field exists: `Plan.supervision` (FR-050), in §18 step 1, §4.1, §4.2; FR-023 rewritten to dedup on `wake_at` with the reason the signature cannot serve; reset rule stated |
| **M-04** | FR-016 rewritten to **resolve by construction** — an explicit `supervision.session_id` — rather than assume; FR-009 restated so it holds under either answer; transcript isolation made a requirement |
| **M-05** | `allStaticToolNames` 81→**83** (verified); `buildKnownBuiltinToolNames` `:739`→**`:715`**; `:484-486`→`:485-486`; `:2597`→`:2591-2593`; test #2 now asserts the catalog size mechanically. **Two review claims did not reproduce — see below.** |
| **M-06** | FR-062's sweep list gains the SPA source tree `src/` (NEW in rev 2); SC-011 restated as the mechanical command, case-**insensitive** (the SPA holds the uppercase constant name) |
| **M-07** | All seven mis-traced rows retraced; four negative requirements moved to conformance assertions; three documentation requirements excluded from the coverage claim; **FR-028 created to own orphaned test #53**; FR-039/NFR-6 unswapped; FR-063 given its own scenario; §17's completeness check restated precisely |
| **M-08** | SC-004 and test #4 restated as **complement-complete** (`len(allowed) == 1`, `len(denied) == len(catalog) - 1`) |
| **M-09** | `inspect_session` **dropped** from the allow set, with the reason (structurally inert — no verifier scope) |
| **M-10** | FR-063 now **specifies both replacement strings**; SC-019 asserts the new copy, not only the absence of the old; new BDD scenario; the two strings stay distinct per `planStateColors.ts:222-226` |
| **M2-01** | FR-025 clears the stall note on entry to the parked phase; E21's rationale corrected (phase exclusivity ≠ state exclusivity); test #68; new scenario |
| **M2-02** | FR-048 makes a corrupt intent log ERROR + fail closed; test #46d; scenario; E22 |
| **M2-03** | **Answered by verification, not by new code**: agent ids are server-minted UUIDs (N12). FR-049 pins it with test #66, dataset B10, scenario, SC-024 |
| **M2-04** | FR-017 extended to all **three** sites incl. `asyncapi.yaml`'s `notification_type` and the SPA normaliser; `Notification.yaml:22`'s false tolerance sentence corrected in the same commit; §11.3 states both failure modes; test #55b |
| **M2-05** | `read_file` / `list_directory` **dropped** — §11.1's input list requires neither, and `seedSystemAgents` does not re-enforce `Workspace`. Any future grant must state and re-enforce a workspace first |
| **M2-06** | US-1 AS5 added for the stall re-route and the scenario repointed; three more scenarios labelled **regression/conformance anchors** in §12 itself and named in §17 |
| **M2-07** | US-3 and NFR-2 **downgraded to what FR-030 delivers**, and FR-030b adds the real control (replacement inherits the superseded member's criteria); SC-003 extended to the paired case; residual recorded |
| **M2-08** | S9 rows table annotates **row 4 as the live one** (`boot_sweep.go:295-296`) and **row 5 as dead-but-renamed**; R1.5b marked synthetic; RISK-8 rewritten |

### Round 1 + 2 — MINOR and OBSERVATIONS

`m-01` FR-018→**FR-017** in §10 and §11.3 · `m-02` SC-003 restated (subsumed by C2-02) ·
`m-03` the supersede auto-reset side effect added to the applied-supersede scenario's Then clauses ·
`m-04` AMB-9 closed on the contract's typing, not on an equality claim · `m-05` E10 reworded
(REST-guard only; reachable by config edit) · `m-06` FR-051 guards `Store.Create` itself ·
`m2-01` FR-018's **coalescing change dropped** (nothing to coalesce), click-through kept ·
`m2-02` FR-047 reorders `validateMemberRef` · `m2-03` FR-024's retry bounded by the same ceiling ·
`m2-04` FR-027 documents the 30 s tick · `m2-05` RISK-10 argues the P1-blocks-P0 inversion rather
than assuming it · `O-01` FR-016's speculative-generality sentence retained but subordinated to a
resolved requirement · `O-02` AMB count reduced from 10 open to **5 open, each with a shipped
default**; AMB-3 and AMB-4 (the two blocking ones) closed · `O2-02` AMB-6 closed as **yes** ·
`O2-03` noted; the `.gitignore` entry is not this spec's scope and this spec adds no third sentinel.

### Review claims that did not reproduce

Recorded so a re-grill does not re-apply them. Both are from r1 M-05, whose other four items were
correct and are fixed above.

| Claim | Re-verification 2026-07-27 | Outcome |
|---|---|---|
| *"`tests/e2e/conformance-design-e2e.spec.ts` — **21** occurrences"* (spec said 17) | **RETRACTED in rev 3.** Both figures reproduce, because they count **two different spellings**: `awaiting_owner_correction` = 17, `awaiting-owner-correction` = 4, case-insensitive total = **21**. r1's 21 was reproducible and was pointing at the exact blind spot SC-011's alternation still had. | **Rev 2's disposition ("the spec's 17 was correct, retained") was right on the letter and wrong on the conclusion.** The finding is **accepted** in rev 3: SC-011's first alternand is now `awaiting.owner.correction` (matches both separators) and S9 row 1 quotes **no** figure at all, per the spec's own no-hand-counts rule. See r3 M3-15 and operator ruling 7. *This row is left in place rather than deleted, as the record of a rebuttal that was mechanically correct and substantively wrong — the failure mode is checking the number instead of asking what the number was pointing at.* |
| *"`src/**` currently holds **39** of the occurrences"*, with per-file counts 11/10/7/5/2/2/1 | Case-**insensitive** per-file counts: `planStateColors.test.ts` 11, `planStateColors.ts` 10, `PlansFilterBand.test.tsx` 7, `WorkspaceGraphTab.test.tsx` 5, `PlansFilterBand.tsx` 2, `ws.new-frames-validation.test.ts` 2, `WorkspaceGraphTab.tsx` 1 — **38 non-generated**, plus generated files | Per-file figures reproduce; the total does not. **Immaterial** — the finding (that `src/**` was missing from FR-062's sweep) is correct and is fixed, and SC-011 is now a command rather than a count, so no figure is load-bearing. |

---

### Round 3 — CRITICAL

| ID | Finding | Disposition |
|---|---|---|
| **C3-01** | The stall limb has no correction path: `AppendCorrection` rejects any phase ≠ parked, `surfaceStallIfAny` sets `stalled`, and the spec's own scenario/test #59 pin the two as disjoint — so every `plan_correct` a stall wake provokes is rejected 100% of the time, and FR-021–023's machinery never arms for a stall | **Fixed by choosing option (a) explicitly — D-01.** **FR-029** defines the *supervision-eligible phase set* `{awaiting_supervision, stalled}`, widens `AppendCorrection`'s gate to membership in it, and restates FR-021/022/023's predicates over it. The disjointness invariant is untouched (widening a gate ≠ making phases co-occur), so #59 and its regression row still pass. Option (b) — dropping the `:1254` re-target — was rejected in writing, because FR-011 gives the Owner no correction role, so it would leave a stalled plan with **no** corrector. New: US-1 AS5b/AS5c, two scenarios, E1b/E37, A30/A31, E15/E16, tests #36e/#36f, §13.4 row. |
| **C3-02** | `stop_plan` ships denied to every agent on a fresh install — the exact N1b trap the spec documents for its first tool and falls into for its second. No FR names it in any seed override map, and Dataset D covers `plan_correct` only, so nothing detects it | **Fixed by rule, not by list — operator ruling 6.** **FR-006b**: *wherever `execute_plan` is seeded, `stop_plan` is seeded in the same map at the same policy value*, with one stated exception (the Worker's sparse map gets an explicit `deny`, the `inspect_session` precedent) and one consequence (PlanSupervisor holds neither). Stated as a rule so a new agent cannot ship unable to stop what it starts. New: **SC-004b** asserting the **resolved** implication `execute_plan != deny ⟹ stop_plan != deny`; Dataset **D2** (D7–D12); tests **#5b/#5c**; scenario *No agent can start a plan it cannot stop*; §18 step 4. **N14** records why the ceiling must be `allow` and not mirror `execute_plan`'s `ask`. |
| **C3-03** | FR-050's blanket reset erases `correction_rounds`, the counter added to fix r2 C-02 — six readers break and SC-017/SC-016b/#62 cannot pass | **Fixed per field.** FR-050 now carries an explicit five-row lifecycle table: `wake_at`/`wake_error`/`attempts` reset on leaving the phase set; **`correction_rounds` is cumulative and never resets**; **`session_id` is never cleared**, only overwritten (which also closes m3-07). FR-021 states explicitly that its `attempts` reset does not touch `correction_rounds`. New: **SC-017b**, Dataset **E14**, scenario *The correction counter survives a park → dispatch → park cycle*, and #62 extended to drive a real park→correct→re-park cycle rather than a fixture. |
| **C3-04** | S9 row 3 renames the **Owner's** session field to `SupervisionSessionID` while FR-050 adds `supervision.session_id` for a different session — two fields reading as "the supervision session" inside the story about naming | **Fixed by dropping the rename — D-02.** S9 row 3 is **WITHDRAWN**; `Plan.OwnerSessionID` keeps its name, which is already correct under US-7's canonical definition. Row 7 still ships and is what disambiguates it. Recorded as an ADR-055 D14 deviation. Scope, §18 step 2, O4 and the S9 table all updated to "five live rows". m3-10's citation slip is fixed in the same row (`:2765-2768`, not `:2769-2772`). |
| **C3-05** | The kill switch cannot fire and the specified test passes anyway: `wakeOwner` never sets `TranscriptSessionID`, a synthetic id is dropped by `ResolveSessionStore`, cancellation matches on exactly that value — and `fakeSessionCanceller` returns success regardless. The owner-session limb FR-044 claims to extend is already inert | **Fixed at the mechanism *and* the assertion — operator ruling 5.** **FR-016b** requires a **real, store-backed** session (the `NewVerifierSession` precedent), one per park, threaded through a new `wakeOwner` parameter into `AsyncNotifyEvent.TranscriptSessionID`; a derived/synthetic id is **forbidden** in §10. **FR-044** is rewritten around the outcome and its "extending an existing cascade" framing corrected. **SC-020** now asserts five limbs including *the turn terminates* and *the cancel was claimed*, and explicitly excludes a canceller double. **N13** records the inert owner session; O11/RISK-13 scope its repair out. New: US-8 AS1/AS1b, tests **#63/#63b**, Dataset F2/E17/E34. **And verifying C3-05's chain surfaced N15** — see below. |

### Round 3 — MAJOR and MINOR

| ID | Disposition |
|---|---|
| **M3-01** | A3 changed from 50 members to **20** (`max_tail_members`, applied); A22 remains `cap + 1`. SC-023 restated and now enumerates the **acceptance** rows too, so a row cannot belong to neither list |
| **M3-02** | The *"DoD is unreachable"* scenario's Then corrected `judge_rounds_exhausted` → **`dod_unreachable`**; §12 swept for other survivors (the round-ceiling scenario and the four-cause outline's first two rows are correct as-is) |
| **M3-03** | US-4 AS6 **split** into AS6 (below ceiling: parked, re-woken, Owner **not** notified) and AS7 (at ceiling: terminated, Owner notified); the BDD scenario split to match; #36 renamed + repointed to AS7; **#36a** added for AS6; FR-019 restated |
| **M3-04** | NFR-1's whole-plan bound corrected to `(PlanJudgeMaxRounds + 1) × supervision_max_attempts` = **63**, not 3; **SC-022 now counts wakes the harness observes** rather than reading counters off the terminal record. A never-reset `total_attempts` field was the alternative and was rejected under D-06 |
| **M3-05** | FR-030b's predicate stated exactly: **every** criterion of the superseded member must appear in the union of `tail_members[].criteria`, compared by `AcceptanceCriterion` **id** where present else exact (`kind`, `expression`). Four cases tabulated — none (reject), **strict subset (reject)**, all (accept), **empty `S` (accept, vacuously)**. New: A28, A29, E35, E36, two scenarios, #67 extended to four cases |
| **M3-06** | FR-017 now **names the two values** — `plan_completed`, `plan_failed` — with a per-terminal-reason title/body/severity table. Test #55 asserts the literals |
| **M3-07** | **D-05: the rejection trigger is deleted** from FR-022. A rejected correction is detected by the deadline exactly as silence is, matching Dataset E6 and keeping NFR-1's unit ("turns"). `attempts` increments at most once per wake |
| **M3-08** | **D-03: FR-039b's audit entry is widened** to five fields (actor, plan id, verb, **target member id**, **falsified assumption**); US-6 AS1, test #43 and SC-018 restated against `GET /api/v1/audit-log`; **AMB-5 closed** as "no route in v1". The plan-revisions REST route stays out, because it inherits O1's authorization gap |
| **M3-09** | Subsumed by **FR-016b** (minting, format, stability, threading) and C3-05's disposition. `TranscriptSessionID` added to §11.2's data-in list, with the note that a `nil` return from `Notify` is not evidence of delivery |
| **M3-10** | **FR-040 gains a fourth site**: an `ABANDON` row in `SKILL.md:177-183`'s verb table. SC-015 now asserts **two** presences; #25 extended |
| **M3-11** | **#71 `TestSupervisionGauge_CountsParkedPlans` added**; FR-026 retraced to it; #53 owned by FR-028 alone; §17's completeness check gains item 8 recording the mis-trace |
| **M3-12** | US-5 AS1 **split** into AS1 (deadline elapsed → woken on the first tick after boot) and AS1b (deadline not elapsed → **zero** wakes); the restart scenario split; **SC-007** restated as "at most one per restart" with the `wake_at` precondition stated |
| **M3-13** | **SC-010** restated: 10/10 **attempt**, 9/10 succeed, C8's failure recorded per FR-024; C6 carved out of the notification limb explicitly |
| **M3-14** | **FR-046b added** and §18 step 1 gains item (f): `RevisionEntry.yaml`'s `verb` enum + `pkg/plan`'s `RevisionVerb` consts gain `abandon`. Added to S15's scope row and to §4.1/§4.2. Test #57 asserts the generated enum carries four values |
| **M3-15** | **SC-011's first alternand is now `awaiting.owner.correction`** (matches both separators); FR-062's directory list gains `pkg/tools/**` + `pkg/agent/**` and **forbids composed phase literals**; S9 row 1's hand-quoted "17" is **removed**; §26's "did not reproduce" row is amended to record that the two figures counted two spellings |
| **M3-16** | **FR-050 gains the `plan.Patch` write path**: five discrete pointer fields with set semantics, with the lost-update reasoning stated (`rest_plans.go`'s `Store.Update` callers do not hold `planDecisionMu`). Named in §18 step 7, §4.1, §4.2; test **#57c**; §13.4 row; E38 |
| `m3-01` | A16 cites **`maxMemberTitleBytes`**, the name FR-046 defines |
| `m3-02` | FR-046 names the fields `maxTextBytes` bounds: `falsified_assumption`, `reason`, tail-member `description` (title uses `maxMemberTitleBytes`); bytes, not runes |
| `m3-03` | Dataset **E3b** added — the exact boundary (20 ticks, `now == wake_at + timeout`, **no** fire); FR-021 states the comparison is strict |
| `m3-04` | FR-042 specifies what `channel` receives on a tool-originated stop (the calling turn's channel; else the literal `"tool"`, never empty) |
| `m3-05` | FR-003 names the field and the status code (`enabled: false` → **403** on a `Locked` agent), with an explicit vacuous-satisfaction clause if no such field exists at implementation time |
| `m3-06` | §11.1 says **"exactly 20 ticks"**, and states the predicate is strict so the first firing tick is 21 |
| `m3-07` | Closed by FR-050's per-field rule: **`session_id` is never cleared**, only overwritten. E17 and E34 cover the window |
| `m3-08` | **AMB-12** added and answered: the handover text distinguishes the two `judge_rounds_exhausted` sub-causes; the SPA badge does not |
| `m3-09` | FR-014(b)'s trigger point and content specified for the `done` path: **after** the `StateDone` write, with FR-017's template — never the wake's agent-directed commission text |
| `m3-10` | Moot — S9 row 3 is withdrawn (D-02). The correct citation (`:2765-2768`) is recorded in the row |
| `O3-01` | **Acknowledged, deliberately not acted on.** Moving every "Rev 1 said…" paragraph out of the FRs into §26 is a whole-document restructure, and rev 3's five CRITICALs were all local — the risk of losing a load-bearing sentence in that move exceeds the readability gain in this round. The archaeology *is* load-bearing here: three separate r3 findings (M3-02, M3-07, C3-03) were rev-1/rev-2 leftovers, and the inline record is how a reader tells a current MUST from a retired one. Revisit when the spec is next opened for a non-blocking revision |
| `O3-02` | **D-06: the four payload caps become package constants** in `pkg/plan`; only the two supervision timings stay configurable and per-plan overridable. Four `PlanBounds` wire additions removed |
| `O3-03` | **D-07: the claim is split.** SC-001 (loop closes, scripted double, #60b) is the blocking merge gate; SC-001b (rubric works, real LLM, #60) is a nightly signal. RISK-11 rewritten |

### Cross-spec review (× `list-jobs-spec.md`) — this spec's obligations

| ID | Disposition |
|---|---|
| **C1** | **FR-062's sweep extended to `pkg/tools/**` + `pkg/agent/**`**, SC-011's alternand matches both separators, and **composed phase literals are forbidden** — the sibling composes `"running/awaiting_owner_correction"` at runtime, which no sweep and no compiler can see. §18's landing order prevents the sibling landing first, which is what would make the sweep miss it entirely |
| **C2a** | **FR-006b** adopts the sibling's FR-023(c) recipe verbatim — the per-agent `denyAllThenOverride` override maps — and SC-004b asserts the **resolved** policy on Jim, not the seed literal |
| **C2b/C2c** | §18 step 3 records the `wantToolCount` collision (83 → 85 here, → 86 with the sibling) and SHOULDs the mechanical assertion so the second lander never touches the line |
| **C3** | **D-04: accept, deliberately.** PlanSupervisor is roster-blind; recorded in FR-008, §10 and §24, with the note that a future grant must *deliberately* amend FR-008 and test #4 |
| **M2** | §18 step 1 gains item (g): `Goal.yaml:41` and `Plan.yaml:135` prose descriptions naming `owner_scope_kind`/`owner_scope_id`/`owns_plan_id` |
| **M5** | §18 step 2 states the sweep is **allow-listed to the named identifiers, never a prefix match**, and that `*.OwnerAgentID` on any type is out of scope — naming the sibling's `plan.Filter.OwnerAgentID` and `LifecycleRecord.ParentAgentID` as the specific hazards |
| **M6** | **FR-042** requires the `stop_plan` tool description to state that stopping a plan at `awaiting_supervision` aborts an in-flight adjudication; **FR-045** requires the same in `docs/operations/`. Operator ruling 8 settles the wider question — `blocked` is informational, not actionable — and no redesign follows here |
| **m3** | FR-006, §4.1 and test #2 corrected: `buildKnownBuiltinToolNames` is **derived**, not a literal. Three literals plus one derived set |
| **M1, M3, M4, m1, m2, m4** | The sibling's obligations, not this spec's. Recorded in §18's landing-order table so neither side assumes the other has them |

### New findings surfaced *while* dispositioning round 3

| ID | What | Where |
|---|---|---|
| **N13** | Nothing in `pkg/` ever creates the `plan:<id>` session, so `StopPlan`'s owner-session cancel is a production no-op and US-8's premise was false | US-8's premise-correction block, O11, RISK-13, FR-044 |
| **N14** | Jim's `execute_plan: allow` resolves to `ask` against an `ask` ceiling — a pre-existing ADR-052 defect that constrains `stop_plan`'s ceiling choice | O12, RISK-14, FR-006, FR-006b |
| **N15** | **Every plan wake is dropped by `processSystemMessage`'s internal-channel guard before a turn runs.** Found while verifying C3-05's chain; it makes §1's diagnosis half the story and FR-012's split route into a path that delivers nothing | §1, N15, **FR-012c**, **SC-025**, D-08, RISK-15, tests #31b/#31c |

### Rev 4 — the plan-wake delivery fix (operator ruling 9)

Rev 4 changes **no architecture and no decision**. It promotes N15 from *"a defect recorded because a
requirement depends on it"* to a first-class, fully-specified fix, per the operator's ruling
(*"not filing, add it to the spec, we have to solve it now"*), and closes the three things rev 3 left
undetermined. It is deliberately additive: FR-012c's four properties, SC-025's headline claim, D-08's
decision and tests #31b/#31c all survive with their meaning intact.

| # | What rev 3 left open | What rev 4 does |
|---|---|---|
| **1** | **FR-012c stated properties, not a mechanism** — *"the requirement, stated as properties rather than as an implementation"* — and pointed at the verifier-dispatch pattern without saying which wake family uses it or where the origin comes from. An implementer could satisfy the letter of it by widening the internal-channel guard, which RISK-15 forbids in the same requirement. | FR-012c gains the **(A)/(B) family fork** (D-11) and FR-012d supplies the **origin data model** (D-09). The root cause is named: `Plan` has no origin fields, which is why a constant was hardcoded (N15.2). |
| **2** | **The no-origin case was never mentioned.** A plan created through the Plans UI has no chat to deliver to, and rev 3's FR-012c(3) — *"MUST likewise result in a turn for `owner_agent_id`"* — is unimplementable for it, silently. H9 tests exactly this plan and would have failed. | **D-10 decides it** under operator ruling 10 (*a chat origin is optional; do not invent a fallback*): the turn still runs and is persisted; nothing is published; the notification is the human surface; there is **no** fallback to any other conversation; and the chat leg is **unreachable by construction** rather than attempted-and-skipped. FR-012d(4), **N16**, E38/E41/E42, SC-026, test #31d. |
| **2b** | **The empty-origin path was assumed harmless.** It is not: it takes the same dead end by a different route, and worse. | **N16**, new. Two candidates traced and executed: the leading-colon parse (`":plan:<id>"` → `idx == 0` → `else` branch → `originChannel = "cli"`, also internal → dropped) is **unreachable**, because `Notify` rejects an empty destination one hop earlier (FR-N7, `async_notifier.go:226-233`). The **reachable** consequence is that rejection feeding FR-024's `wake_error` ladder into FR-022's ceiling — terminating a **healthy** UI-created plan as `failed(supervision_unavailable)`. Designed out by FR-012d(4)'s construction guard, which uses FR-N7's exact predicate; pinned by test #31d limb (6) and a §13.4 row on FR-N7 itself. |
| **3** | **N13 was scoped out wholesale** (O11) on the grounds the feature does not need the Owner's session. | **In scope and fixed here (operator ruling 11).** Two dependents: operator ruling 1's kill switch (whose owner-session cancel leg has always been a no-op) and FR-012c(B)'s persistence. **FR-016c** is written as *FR-016b's mechanism applied to the second session* — one helper, one PR — per the ruling's *"fold it into the FR-016b work"*. **O11 is removed from the out-of-scope table**, not narrowed. SC-020 gains limb (e): the Owner turn **terminated**, never that an id reached a canceller. RISK-13 is rewritten around the one remaining residual (`OwnsPlanID` still has no writer). |
| **4** | **The corrected misdiagnosis.** Two independent analyses of N15 — one of them recorded during this document's drafting — concluded the **bus** channel (`async_notifier.go:271`) was the defect and proposed changing it. Rev 3's N15 narrative was right about the mechanism but never said, in terms, *what is not the bug*, so the misreading kept recurring. | **N15.1** is a dedicated block: the bus channel is the routing key `loop.go:5515` matches and `processSystemMessage` rejects any other value at entry. §11.2 and §13.4 each carry a matching guard row. |
| **5** | **Test #31b already asserted "a turn ran"** — rev 3 got this right and it is preserved verbatim in intent. What was missing were the *sibling* properties a green #31b alone does not establish. | SC-025 enumerates **four separately-necessary assertions**; #31b gains the zero-outbound (non-leak) limb; #31c is rewritten around the Owner family and renamed off the mechanism; **#31d** (origin-less: supervision *and* owner *and* the N16 non-escalation limb) and **#31e** (origin population, both write paths, `webchat` included) are new; **#63c** pins the `StopPlan` owner leg on *the turn terminated*. |
| **6** | **A defect found while specifying, deliberately NOT fixed.** | **RISK-16**: `wakeOwnerAttemptsExhausted` (`task_executor.go:1066-1069`) falls back to `Channel: "system"` for an origin-less Task, so the **Task** goal-loop's attempts-exhausted wake is dropped by the identical chain. Out of scope on D-08's stated test — no requirement here depends on it, and Tasks are ADR-053 surface — but recorded in §13.4 and §21 and recommended as an ADR-053 follow-up, rather than left silent for someone to rediscover. |

**Explicitly *not* changed by rev 4**, so a reviewer does not go looking: the `Plan.supervision` design
(FR-050), the `:1571` leave-it-alone decision (FR-012b), Appendix A's rubric, D-01 through D-07, the
S9 rename rows (including D-02 — `Plan.OwnerSessionID` **keeps its name**; only its value shape
changes), `AsyncNotifier.Notify` itself, `processSystemMessage`, the internal-channel guard, and the
`bus.InboundMessage.Channel` literal.

### What was deliberately preserved

All three reviews named things worth keeping. They are intact:

- **The `:1571` decision** — r3 independently confirmed it is coherent in **all 15** places it
  surfaces and named it *"the strongest part of the revision"*. Untouched.
- **The rubric (§27)** — r3 checked it rule-for-rule against `SKILL.md:156-219` and found exactly one
  drift (`abandon`, now fixed at the *skill* end by FR-040's fourth site, not by weakening the
  rubric). Untouched otherwise.
- **The two new `failed_reason` values** — r3: *"contracts-first and complete. No finding."* Untouched.
- **Evidence quality** — 2 defects in ~60 spot-checked citations. Both fixed (m3-10, M3-15).
- **§26 itself** — r3 called the disposition table *"the reason this round could go deeper than the
  last two."* Extended, not replaced.

Rev 2's own preserved list follows and is also intact:

- **The `ResolveEffectivePolicy`-not-seed-literal discipline** in SC-004 and tests #4–#6 — the only
  reason the fresh-install dead-on-arrival failure (N1b) is visible at all. Rev 2 **strengthens** it
  to a complement-complete assertion rather than trimming it.
- **§13.4's regression table** — the most rigorous section in the document. The four migration rows
  are removed as dead, and **six rows are added**, not trimmed.
- **§23's holdout design** — H2 and H4 are externally observable and adversarial. H6 and H7 are
  replaced (their subjects no longer exist); H8 and H9 are added for the two new failure surfaces
  rev 2 introduces requirements for.

---

## 27. Appendix A — `PlanSupervisorDefaultRubric` (first draft)

> **Status: FIRST DRAFT, open to tuning** (operator ruling 2: *"apply good prompt engineering
> techniques, we can optimise it later but do your best"*; RISK-12).
>
> **Derivation.** Every behavioural rule below is derived from
> `pkg/skills/embedded/plan/SKILL.md:156-219`, which already ships a verified re-planning playbook
> (diagnose → classify → SUPERSEDE / TARGETED-RETRY / APPEND → record the falsified assumption →
> honest exit). **The two must not drift.** Where this rubric states a rule the skill also states,
> the skill is the source; where it adds one, the addition is a *role* fact the skill cannot know
> (that the corrector is a different actor from the plan's author) or a *wake kind* the skill does
> not cover (the stall wake). Note FR-040 amends `SKILL.md:181` in the same change — this rubric
> assumes the **amended** verb table, in which a supersede's replacement is mandatory and inherits
> the superseded member's criteria, **and in which `ABANDON` is a fourth row** (FR-040 site 4, added
> in rev 3). Rev 2's FR-040 amended three sites and none of them added that row, so `abandon` existed
> **only here** and in no version of the skill the adjudicator reads — the one real drift r3 found in
> an otherwise rule-for-rule-matching rubric (M3-10). The rubric is unchanged; the skill is what
> moves.
>
> **Rev 3 note on the STALLED branch.** The rubric's *"Your job is to diagnose why it cannot progress
> and correct the structure"* is now backed by an execution path: under **D-01/FR-029**,
> `plan_correct` accepts the `stalled` phase. In rev 2 it did not, so this instruction told the
> adjudicator to do something the engine rejected 100% of the time (r3 C3-01). No rubric text
> changes — the engine caught up to it.
>
> **Shape.** Follows `JudgeDefaultRubric` (`pkg/coreagent/core.go:889+`) — the in-repo precedent for
> a System-Agent rubric: second-person role statement, an explicit statement of what arrives and what
> it is worth, a short numbered rule list, a fail-closed default, and a required structured output.
> Comparable length.
>
> **Materialisation.** Defined as a Go const in `pkg/coreagent`, written to
> `plansupervisor/SOUL.md` by the gateway-side eager seed, never overwriting an operator edit
> (FR-005). An operator may edit it freely; the const is only the default.

```
You are the Plan Supervisor — the sole adjudicator authorised to correct a running plan in the Omnipus Planning & Goals engine.

You are woken for exactly one reason: a plan cannot move on its own. You did not author the plan. The agent that did is still running and is accountable to whoever asked for it — you are not that agent, you do not talk to the requester, and you do not write the plan's closing summary. Your entire job is to decide what single correction, if any, lets this plan reach its Definition of Done.

WHAT YOU RECEIVE

Two kinds of wake. Read which one you got before deciding anything.

- DEFINITION-OF-DONE UNMET. The plan's members have all finished and the plan Judge ruled the DoD not met. You receive the Judge's per-criterion verdict with its reasons. Your job is to correct the plan's execution.
- STALLED. The plan is still live but no member is dispatchable or in flight — the DAG cannot advance. You receive the stall reason. Your job is to diagnose why it cannot progress and correct the structure. Do NOT return a Definition-of-Done verdict for a stall wake; the DoD has not been evaluated and is not the question.

You also receive the plan record and its members' outcomes. Member outcomes are evidence. A member's own claim that it succeeded is a claim, not a verdict — the Judge's per-criterion reasons are what tell you which criterion actually failed and why.

THE ONE RULE THAT IS NOT NEGOTIABLE

The Definition of Done is immutable. You cannot change it, and nothing you can call will let you. You change the plan's execution so it meets the criteria. You never change the criteria, reinterpret them more loosely, or argue that a criterion was unreasonable. If a criterion genuinely cannot be met, say so and abandon — do not quietly work around it.

HOW TO DECIDE

1. Diagnose. For each unmet criterion, identify which member's outcome is responsible. Name it to yourself before choosing a verb. If you cannot name one, the defect is a missing capability, not a bad outcome.

2. Classify the failure.
   - Wrong outcome — the member finished (done) but its result is incorrect → SUPERSEDE.
   - Recoverable failure — the member failed on something transient (timeout, flake, a dependency that now exists) → TARGETED-RETRY.
   - Missing capability — no member addresses this criterion at all → APPEND.
   - Nothing fits — no legal target exists for any verb, or every remaining path depends on a frozen outcome that cannot be produced → ABANDON.

3. Choose one verb and issue one plan_correct call.
   - APPEND adds new tail member(s) and their dependency edges. Use it for work that does not exist yet.
   - SUPERSEDE marks a done member's outcome ignored by the Judge; the record itself stays immutable. It MUST be accompanied by replacement work that carries the superseded member's acceptance criteria. This is enforced — a supersede with no replacement, or with a replacement that drops those criteria, is rejected before anything changes. That is deliberate: discounting failing evidence without producing better evidence is not a correction, it is lowering the bar, and it is the one thing you must never do.
   - TARGETED-RETRY resets exactly one failed member. Use it when the work was right and the run was not.
   - ABANDON ends the plan honestly with your reason. Use it when the DoD is genuinely unreachable.

4. Know the side effects before you act. APPEND and SUPERSEDE auto-reset every other live-round failed member, giving them another attempt under the corrected plan; done members are frozen and are not re-run unless you supersede them. TARGETED-RETRY resets only the member you name. Edges you supply must point at real members and must not create a cycle.

5. Record the falsified assumption. Every correction carries one: the specific assumption the original plan made that turned out to be wrong. "We assumed X; the evidence shows not-X; therefore Y." This is the audit trail an operator reads to answer "why did this plan change?" — write it for that reader, not for yourself. A vague assumption is a failed correction even if the verb was right.

BOUNDARIES

- One correction per wake. Decide, act once, stop. If it was not enough you will be woken again.
- You have no way to satisfy a criterion yourself, and you must not try. Adding a member whose only purpose is to make a check pass without doing the underlying work is manufacturing a false success — worse than a stuck plan, because done is terminal.
- If you are unsure between two verbs, prefer the one that adds work over the one that discounts it.
- If you conclude the plan cannot reach its Definition of Done, abandon it and say why. An honest failure is a correct outcome. Silence is not — a plan you leave untouched is a plan nobody is working on.

Return exactly one plan_correct tool call. Do not narrate, do not ask questions, do not request more information — you will not receive any.
```

**Coverage check against FR-005's required topics:** the UNMET wake (§"What you receive", §"How to
decide" 1–2) · the **stalled** wake, diagnosing why the DAG cannot progress and explicitly *not*
returning a DoD verdict (§"What you receive") · verb selection (§"How to decide" 2–3) · the
FR-030/FR-030b supersede discipline, stated as enforced and with its rationale (§"How to decide" 3) ·
the falsified assumption, with its reader named (§"How to decide" 5) · the honest exit, as a
first-class verb rather than an engine behaviour (§"How to decide" 2–3, §"Boundaries") · DoD
immutability, as its own non-negotiable section · and the role fact the skill cannot supply — that
the corrector is a different actor from the plan's author and does not write the closing synthesis
(opening paragraph), which is what keeps it from wandering into the Owner's job after FR-012 leaves
`:1571` on the Owner.

---
