# Feature Specification: `list_jobs` — unified background-job visibility for agents

**Created**: 2026-07-27
**Revised**: 2026-07-27 (rev 2 — against [the spec review](list-jobs-spec-review.md): 3 CRITICAL / 17 MAJOR / 7 MINOR, plus two operator rulings)
**Revised**: 2026-07-27 (**rev 3** — against [round-2 review](list-jobs-spec-review-r2.md): 5 CRITICAL / 18 MAJOR / 10 MINOR, [the cross-spec conflict review](cross-spec-conflicts-review.md): 3 CRITICAL / 6 MAJOR, and **two further operator rulings** (3 and 4). Rev 3 is an **interaction pass over the requirements rev 2 added**, not a redesign — see *Rev 3* below.)
**Status**: Draft
**Input**: [ADR-056](../architecture/ADR-056-background-job-visibility.md) (v2, Proposed), plus its two adversarial reviews — [round 1](../architecture/ADR-056-background-job-visibility-review.md) (BLOCK, 31 findings) and [round 2](../architecture/ADR-056-background-job-visibility-review-r2.md) (BLOCK, 29 findings, **unaddressed at the time this spec was written**).
**Related**: [ADR-055](../architecture/ADR-055-plan-supervisor.md) (PlanSupervisor), [ADR-053](../architecture/ADR-053-unified-goal-plan-subagent.md) (durable session lifecycle), [ADR-052](../architecture/ADR-052-autonomous-agent-plan-execution.md), [ADR-037](../architecture/ADR-037-delegation-graph-removal.md), [ADR-054](../architecture/ADR-054-entity-store.md).
**Deferred**: the `shell` kind (ADR-056 D7, issue #564) — explicitly out of scope, see *Explicit Non-Behaviors*.

> ## ⚠️ Evidence discipline for this spec
>
> ADR-056's architecture is operator-approved and is **not** re-litigated here. Its **code
> citations**, however, were wrong repeatedly across three review rounds. **Every code fact in
> this spec was re-verified against the working tree on branch `feature/plan-swimlane-board`
> at 2026-07-27.** Facts are tagged `[VERIFIED: <path>:<line>]`. Where this spec contradicts
> the ADR, the contradiction is called out inline in a **⚠️ ADR CORRECTION** block and the
> code wins (CLAUDE.md: "code wins over docs on any disagreement").
>
> Nine such corrections exist. Three of them change what gets built. See
> *Verified Corrections to ADR-056* below.
>
> **Rev 2 note.** The nine corrections were independently re-audited by the spec review
> (§8) and **all nine hold**; every spot-checked line number resolved. Rev 2 does not
> restart the evidence work — it fixes the *consequences* drawn from it, plus the code
> facts adjacent to the added fields that rev 1 never checked. Every new code fact
> introduced in rev 2 carries its own `[VERIFIED:]` tag, re-read on the same branch on
> 2026-07-27.

---

## Rev 2 — operator rulings and what changed

Two operator rulings postdate rev 1 and are binding:

**Ruling 1 — sort order: keep the ADR's order, bound each group.** The roster is sorted
`queued → running → blocked → failed → completed`, **unchanged from ADR-056 D3**. Rev 1
inverted this to put `blocked` first because `blocked` rows would otherwise truncate away
under load; the operator's ruling is that **FR-016's per-group sub-bounds are the correct
mechanism for that**, not a reorder. Rev 2 therefore restores D3's order and makes the
per-group bounds explicit, numeric and independently testable (FR-016, SC-016). Ambiguity
#1 is **resolved**.

**Ruling 2 — greenfield: no data migration, anywhere.** No migrator, no `schema_version`,
no upgrade-on-read, for any store. Existing on-disk data is expected not to load; accepted.
This deletes the *legacy-record* machinery — but **not FR-015**, which is rewritten rather
than removed (see below).

### What the greenfield ruling cut, and what replaced it

| Item | Disposition in rev 2 |
|---|---|
| `unattributable_subagents` response field | **Cut.** No legacy records exist to count. |
| BDD *"Legacy lifecycle records are counted, never guessed at"* | **Cut**, replaced by *"Delegate mint fails closed on an unresolvable parent"*. |
| Test #28 `TestListJobs_LegacyRecordsCountedNotGuessed` | **Replaced** by `TestDelegateMint_FailsClosedOnEmptyParentAgentID`. |
| Regression row `TestLifecycleRecord_LegacyRecordWithoutParentAgentIDStillLoads` | **Cut** — it existed only to prove a pre-FR-013 record still loads. |
| Regression row `TestLifecycleFilter_UnsetParentAgentIDUnchangedBehaviour` | **Survives** — the ADR-053 boot sweep's own queries leave `ParentAgentID` unset and must stay unfiltered. |
| Edge case *"Unattributable legacy lifecycle records"* | **Cut**, replaced by *"Empty parent agent id at mint time"*. |
| Ambiguity #8 (`unattributable_subagents` noise) | **Resolved by deletion.** |
| FR-015 | **Rewritten, not deleted** — see below. |
| The *"prospective only"* caveat on subagent recovery | **Retired.** Stated explicitly in FR-015 rather than silently dropped. |

**Why FR-015 could not simply be deleted.** `ParentAgentID == ""` is still reachable at mint
time on a **brand-new** install: FR-013 (rev 1) imposed no non-empty requirement, the mint
site already guards `if agentID != "" && t.getAgentRegistry != nil` for the *target*
`[VERIFIED: pkg/tools/delegate.go:926]` — its author knew an empty agent id was possible —
and rev 1's proposed `json:"parent_agent_id,omitempty"` tag makes an empty parent serialise
to an **absent key**, byte-identical to the legacy shape FR-015 was written to catch.
Deleting FR-015 while keeping `omitempty` would silently drop those rows with no counter
anywhere: strictly worse than the design being replaced. FR-015 is therefore rewritten as a
**mint-time fail-closed invariant** with the `omitempty` tag removed, and it **keeps its
anti-inference sentence** — that sentence was never about legacy data, it encodes C1 (that
`ParentDurableKey` is *shared* parent↔child), and dropping it re-opens the sibling/cousin
leak.

**Consequence:** with mint fail-closed and no legacy data by fiat, every lifecycle record in
existence carries a real parent. **Subagent handle recovery is no longer "prospective only"
— it simply works**, subject only to `actionable` (FR-011), which is about process lifetime,
not attribution.

### The three CRITICALs

| # | Defect | Closed by |
|---|---|---|
| CRIT-001 | `cap_active` derived from an owner+workspace-scoped list but compared against a **global** cap — inverts its own signal (reports `0/16` for a plan queued behind 16 foreign plans, so the agent intervenes on healthy work) | **FR-021 rewritten** + **FR-029** (new lock-free `PlanEngine` cap-snapshot accessor with `reliable` + observation time; fields omitted entirely when unavailable). SC-003 rewritten; `TestListJobs_CapPressureWithoutAdmit` gains a value assertion. |
| CRIT-002 | `native_status` REQUIRED, unbounded, unredacted, composed from two documented free-text sources | **FR-019 extended to every free-text field by exhaustive enumeration** + **FR-030** (per-field maxima; SC-005 restated as a derived arithmetic identity). |
| CRIT-003 | After a restart the default call returns an empty roster with `total_omitted=0` — byte-identical to "no background work at all" | **FR-031** (`terminal_suppressed`, per kind and total, on every `include_terminal=false` response) + US-4 AS-5 reworded (the "no scan cost" claim was false anyway — MAJ-003). |

---

## Rev 3 — the interaction pass

Rev 2 closed the rev-1 findings and **introduced five new CRITICALs, four of them inside
requirements rev 2 added to fix rev-1 findings**. The round-2 review named the pattern precisely:
*"every one of the new CRITICALs is a rev-2 requirement that was not cross-checked against the
requirements it interacts with."* Rev 3 is that cross-check. It does **not** re-open the ADR, does
not restart the evidence work on C1–C9, and does not touch the parts the review passed —
FR-015's rewrite, the god-mode modelling, and the mechanical traceability claims are left alone.

> ### ⚠️ The through-line: assert the property, not the mechanism
>
> The reviews found defects across this spec **and its sibling** that are one mistake:
> **a control whose test passes because the test asserts the mechanism we built rather than the
> property we want.** This spec's instance was SC-016: it proved `blocked` rows survive using the
> **default** call, where the sub-bounds happen to hold, and never tried a small `limit`, where
> they do not (R2-MAJ-001). Rev 2's `TestListJobs_CapPressureWithoutAdmit` was an earlier instance
> — it asserted `Admit` was *not called* (the mechanism) while the numbers it returned were
> inverted (the property).
>
> **This is not hypothetical.** UAT on this branch found a plan reporting *"Running 0/3"* forever
> with every test green, because nothing asserted the plan made **progress**.
>
> **Standing rule for this spec, binding on every SC, BDD scenario and test in it:**
> *would this assertion still pass if the mechanism it names were entirely broken or entirely
> absent?* If yes, it is not a control — rewrite it to assert the observable property under the
> input that actually stresses it. Rev 3 applies this rule to every SC and every test; the ones
> it changed are listed in *What rev 3 changed under the through-line rule* below.

### Operator ruling 3 — `blocked` is INFORMATIONAL, and the conflation must be split first

The operator, verbatim: *"blocked are not so relevant because it means they have not run yet, the
executer cannot do anything about it, it is just information."*

**That ruling is plainly true for one of the three things FR-006 maps to `blocked`, and plainly
false for the other two.** Applying it uniformly would tell an agent to ignore a subagent that is
waiting on an answer only it can give. So rev 3 **splits the vocabulary before applying the
ruling**, using the same shape as FR-006's `intentionally_stopped` sibling boolean (a companion
field, not a sixth `status` value — ADR-056 D3's five-value vocabulary is preserved):

| What is `blocked` | Native condition | `attention` | Actionable by the caller? |
|---|---|---|---|
| A task whose dependency is unmet | `task.Status == blocked` | **`none`** | **No.** The operator's case: it has not run yet, it will clear on its own, there is nothing to do. Pure information. |
| A plan the engine has parked for a supervisor | `plan_phase == awaiting_supervision` | **`elsewhere`** | **No — and intervening is harmful.** Another principal (PlanSupervisor) is adjudicating; the Owner is forbidden from correcting and its only available tool would abort the adjudication. See cross-spec M6. |
| A plan that is stuck | `plan_phase == stalled`, `paused_reason` set | **`caller`** | **Yes.** It needs a correction or a steer from the caller. |
| A subagent waiting on an answer | `state ∈ {needs_input, paused}` | **`caller`** | **Yes.** It is blocked on input only the caller can supply. |

`attention` is defined by **FR-036** and is REQUIRED on every row (`none` for every non-`blocked`
row, stated so it is not inferred). The sort order and the per-group sub-bounds are kept exactly as
operator ruling 1 left them — the ruling deflates R2-MAJ-001's *severity*, and rev 3 fixes its
*defect* anyway (FR-016's round-robin allocation), because a truncated `attention=caller` row is
still a real loss and because SC-016 failed the through-line rule regardless.

**Recorded so the priority is not re-derived later:** `attention=none` rows are the lowest-value
rows in the roster. That — not their sort position — is the reason `blocked` sorting last is
acceptable.

### Operator ruling 4 — tool permission is CONFIGURATION, not a hand-maintained list

The operator, on the sibling spec: *"all agents that have now start plan need to get all plan
tools, particular stop."* That governs `plan-supervisor-spec.md`. **The shared lesson binds here:
seeding is a rule, not a list someone remembers to extend.** Two consequences in this spec:

1. FR-023 gains site **(e)** — `pkg/config/defaults_test.go`'s `const wantToolCount = 83`
   `[VERIFIED: pkg/config/defaults_test.go:92-96]`, which rev 2 omitted entirely. Adding
   `"list_jobs": allow` to the global seed (FR-023(b), mandatory) turns that test **red** on its
   own, while SC-015 requires the Go suite green — a self-contradiction rev 2 could not see because
   the fact is recorded only in the sibling spec.
2. FR-023 requires that literal to be **replaced by the mechanical invariant**
   (`len(cfg.Sandbox.ToolPolicies) == len(coreagent.AllStaticToolNames())`) rather than
   re-hardcoded, so the next tool added to the catalog cannot repeat this. See *Landing order and
   the shared `wantToolCount` line* below.

### The five round-2 CRITICALs and how rev 3 closes them

| # | Defect | Closed by |
|---|---|---|
| R2-CRIT-001 | FR-032(c)'s memo is keyed on the **principal alone**, so a workspace-less turn's cross-workspace roster is served to a later scoped turn (defeating the P0 US-3 control), every narrowed call inside the TTL returns the previous response, and a memo hit emits no audit entry | **The memo is REMOVED.** FR-032(c) is restated as a **prohibition** with the reasoning recorded, because an argument-keyed memo does not bound cost either (an agent varying `limit` bypasses it), and FR-032(d) is the real control. The BDD scenario and test 33d are **re-pointed at the property** (`TestListJobs_NoCrossScopeReuse`), so the test now fails if a memo is ever reintroduced — rather than being deleted and leaving the hole unguarded. |
| R2-CRIT-002 | FR-033 lists `cap_active`/`cap_max` among the omit-when-zero counters, so `cap_active=0` — the entire content of US-2 AS-5, SC-003 and test 32 — can never be emitted | **FR-033 rewritten** with an explicit carve-out: the cap pair is **state, not diagnostics**. Emitted as a pair whenever the snapshot exists and is `reliable`, **including `cap_active = 0`**; omitted as a pair only when absent or unreliable. New dataset row (*Store failure modes* row 14) asserts presence at zero. |
| R2-CRIT-003 | FR-029's staleness bound is *"a stated staleness bound"* stated nowhere in 2 218 lines; and a stopped engine — the case the field exists for — is always stale, so the field is omitted exactly when it matters most | **FR-029 rewritten.** The bound is a **number with a config key** (`planning.cap_snapshot_staleness_seconds`, default **90 s** = 3× the 30 s tick `[VERIFIED: pkg/agent/plan_engine.go:131]`), and **staleness no longer suppresses** — it **labels**. The response carries `cap_observed_at` and `engine_running` alongside the pair. Omitting was the one disposition that destroyed the story. |
| R2-CRIT-004 | FR-032(d)'s per-call scan ceiling has no value, no config key and no test, and its overflow cannot be reported through the omission counters (an unloaded record cannot be classified) | **FR-032(d) rewritten** with a default (**5 000 records per kind**), a config key (`tools.list_jobs.max_records_scanned_per_kind`), an explicit precedence over FR-017/FR-018/FR-031, and a new `scan_truncated: {kind: {scanned, present}}` marker built on the one number that *is* obtainable past the ceiling — the **directory entry count**, which needs no record load. New test `TestListJobs_ScanCeilingReported` + dataset row. |
| R2-CRIT-005 | FR-010 adopts `Task.CreatedBy` as an agent-ownership predicate — **verifiably mixed-namespace in this tree** — re-importing the hazard C4 eliminated for plans | **FR-010's `dispatched` half moves to a new agent-id-namespaced field**, `task.Task.CreatedByAgentID` (FR-037) — the same disposition C4 reached for plans. Re-verified for rev 3: `pkg/gateway/rest_tasks.go:847` writes `CreatedBy: c.Username` while `pkg/tools/task.go:531` writes `CreatedBy: callerID` and `pkg/tools/todos.go:147` writes `agentID` `[VERIFIED, all three]`; `pkg/task/task.go:314-316` documents `Owner`/`CreatedBy` only as *"server-set attribution (read-only on the wire)"* and constrains no namespace `[VERIFIED]`. New dataset row mirrors *Calling principal* row 6 for **tasks**. |

> ### ⚠️ REV-3 CORRECTION: FR-029's cost justification was FALSE
>
> Rev 2 justified FR-029 twice with *"`Tick` already performs an unfiltered
> `pe.planStore.List(plan.Filter{})` on every pass and does **not** hold `pe.mu` … so the refresh
> is marginal cost on work already being done."* The citation resolves; **the inference from it
> does not.** Re-verified independently for rev 3 on this branch:
>
> - `computeActiveLocked` has **exactly one caller in the entire repository** — `admitLocked` at
>   `plan_engine.go:2189`, reached only from `Admit` (`:2182-2186`), which holds `pe.mu`
>   **exclusively** `[VERIFIED: grep over pkg/ returns the definition at :2221, the doc comment at
>   :2211 and the single call at :2189]`.
> - **`Tick` never calls it.** `Tick`'s `pe.planStore.List(plan.Filter{})` at `:679` is a
>   **different scan** for a different purpose, and `computeActiveLocked` performs **its own**
>   `List` at `:2221ff` rather than reusing that slice `[VERIFIED]`.
> - `Tick` **returns early** on that `List` error (`:679-682`, before any refresh point) and
>   **skips entirely** on the `claimTick()` overlap guard (`:673-676`) `[VERIFIED]`.
>
> So "refreshed unconditionally on every `Tick`" means adding, to every tick of the dispatch loop
> forever, a **second full store scan**, a **new `pe.mu` acquisition**, and a call to **every**
> registered `activeCounter` — on installs where nothing is queued and nothing needs admitting.
> That is a cost regression on the hot path, justified by a claim about that hot path that is not
> true, inside the section of a spec that exists to correct the ADR's wrong code claims.
>
> **A correct citation supporting an incorrect conclusion is exactly the failure mode this spec's
> ⚠️ blocks were invented to catch, and it survived into requirement text.** Rev 3 applies the
> C1–C9 standard to FR-026…FR-035, and FR-029's mechanism is replaced (see FR-029).

### Cross-spec resolution — `plan-supervisor-spec.md` (PS)

The [cross-spec review](cross-spec-conflicts-review.md) found 3 CRITICAL and 6 MAJOR conflicts
**invisible to either spec's own review**, because each is a contradiction only when the two
documents are read together. This spec references ADR-055 seven times and flagged the dependency
(A1, Ambiguity #7); PS references ADR-056 and `list_jobs` zero times. The asymmetry means **this
spec absorbs the change**, deliberately, before any code exists.

| # | Conflict | Rev 3 disposition (this spec's half) |
|---|---|---|
| **C1** | PS renames `plan.PhaseAwaitingOwnerCorrection` → **`awaiting_supervision`** (`plan.PhaseAwaitingSupervision`), and this spec hardcoded the old literal in **six normative places** — US-2 AS-3, a BDD outline row, a scenario title and body, a test name, a dataset row and SC-002. Worse, `native_status` **composes** the literal at runtime (`"running/awaiting_owner_correction"`), so **no compiler catches it** and PS's own FR-062 sweep does not cover `pkg/tools/**` or `pkg/agent/**`. | **Adopted throughout.** All six sites now read `awaiting_supervision`. **And the class of defect is closed, not just the instances**: FR-006a forbids composed string literals — `native_status` MUST be built by interpolating the exported `plan.PlanPhase` / `plan.FailedReason` constants, never a hand-typed string — asserted by `TestNativeStatus_ComposedFromConstants`, which fails if the constant is renamed and the literal is not. This is the same lesson as PS's rename sweep missing a spelling: **extend the test that checks for absence.** `[VERIFIED today: pkg/plan/plan.go:237 still reads `PhaseAwaitingOwnerCorrection PlanPhase = "awaiting_owner_correction"`; the rename has not landed yet — which is why landing order matters.]` |
| **C2** | Two contradictory builtin-tool seeding recipes; this spec omitted `pkg/config/defaults_test.go`'s `wantToolCount`, so `list_jobs` alone turns that test red against SC-015 | **FR-023 site (e)** + the mechanical-invariant replacement (operator ruling 4). Landing arithmetic stated below. |
| **C3** | PlanSupervisor's own supervision work appears on **no** roster: it can never be a plan's `OwnerAgentID` (both write paths are `IsChatTarget()`-guarded and it is a System Agent), its supervision session is never `delegate`-minted so it is no `subagent` row either — and PS's `len(allowed) == 1` invariant structurally forecloses ever granting it `list_jobs` | **Decision recorded: ACCEPT (option (a)).** PlanSupervisor is **deliberately roster-blind**. Written into *Explicit Non-Behaviors* so the next author does not "fix" it, with the consequence named: the engine's `supervision.wake_at` deadline is the **only** liveness control, and parked plans are visible to the Owner alone (as `attention=elsewhere`, per operator ruling 3). PS records the matching half. |
| M1 | PS's two new `failed_reason` values (`dod_unreachable`, `supervision_unavailable`) are uncovered by this spec's normalization table and mis-classified by `intentionally_stopped` | Both added to the BDD outline and the plan dataset; `intentionally_stopped` decided and its **blind spot stated** rather than asserted (FR-006). |
| M2 | `session.LifecycleRecord` co-edited; this spec's SC-014 gate reads branch-wide; stale pre-rename field names in load-bearing anti-inference rules | SC-014 **scoped to this spec's own diff**; `OwnerScopeID` → `ScopeID` and `OwnsPlanID` → `SupervisedPlanID` throughout (C1 row, Explicit Non-Behaviors, the Edge Case, Ambiguity #10). |
| M3 | This spec's *"refreshed unconditionally on every `Tick`"* is false on the exact failure it exists to signal — `Tick` returns early on a `List` error, and the overlap guard can make the snapshot two intervals stale | Folded into FR-029's rewrite: the liveness heartbeat is stamped at the **top** of `Tick`, **before** the early return, and the staleness bound is sized for the overlap guard. |
| M4 | The staleness bound is unstated; PS supplies the 30 s tick that makes it decidable, and any bound < 30 s silently retires the whole cap-pressure feature with green tests | Bound stated as **90 s** with the derivation shown (R2-CRIT-003 above). A bound smaller than the tick was exactly a *"green tests, dead feature"* trap — see the through-line rule. |
| M6 | `blocked` means opposite things to the two specs, and PS newly hands the Owner a `stop_plan` tool whose only use on an `awaiting_supervision` plan is to **abort a healthy adjudication** | Closed by **operator ruling 3's split**: `awaiting_supervision` normalizes to `blocked` **with `attention=elsewhere`**, and FR-012a requires the tool description to state that such a row is handled by another agent and must not be intervened on. |
| m2 | Post-stop roster window | Stated in *Edge Cases* — a `stop_plan`'d plan flips to `failed`+`intentionally_stopped=true` synchronously, but with `include_terminal=false` it **vanishes from the roster**, surviving only as `terminal_suppressed`; and `cap_active` still counts it for up to one tick. |

**Not this spec's to fix** (recorded so nobody assumes they are done): C1's PS half (extend FR-062's
sweep to `pkg/tools/**` and `pkg/agent/**`), C2a (`"stop_plan": allow` in `coreAgentSeed`), C3's PS
half, M2.1 (`Goal.yaml`/`Plan.yaml` prose), M5 (allow-list PS's rename sweep to seven named
identifiers so it cannot eat `plan.Filter.OwnerAgentID`), M6's PS half, and m3.

### Landing order and the shared `wantToolCount` line

**Fixed order: PS steps 1–3 land first, as their own PR. This spec then rebases and proceeds in
parallel with PS steps 4–11.** The dependency is explicit and one-directional:

- **PS owns every contract change in the pair; this spec makes none.** FR-025's "no contract
  change" position was independently re-verified by the cross-spec review and is **correct**.
  SC-014 is only meaningful on a base where PS's single `gen-contracts` run has already landed.
- **PS owns the vocabulary this spec's `blocked` semantics rest on** (C1, A1, Ambiguity #7).
  Writing this spec against final names costs a spec edit; retrofitting costs a code edit, a test
  rename, a dataset row and an SC restatement — in files PS's sweep does not cover.
- **PS step 2's rename sweep runs over the two packages this spec adds fields to.** Running it
  before `plan.Filter.OwnerAgentID` and `LifecycleRecord.ParentAgentID` exist is strictly safer.

**The `wantToolCount` line, stated so the second lander never guesses** — 83 today
`[VERIFIED: pkg/config/defaults_test.go:92]`, 86 when both specs have landed
(83 + `list_jobs` + `plan_correct` + `stop_plan`):

| Who | When | Edit |
|---|---|---|
| **PS**, step 3 | first | `83` → `85` (its own two tools) |
| **This spec**, FR-023(e) | second | **Delete the literal.** Replace it with the mechanical invariant `len(cfg.Sandbox.ToolPolicies) == len(coreagent.AllStaticToolNames())`, which reaches 86 without anyone hardcoding a number and cannot go stale on the next tool. |
| *(fallback, if this spec somehow lands first)* | — | This spec sets `83` → `84`; PS then reaches 86 and performs the literal→invariant replacement instead. |

### What rev 3 changed under the through-line rule

Every success criterion and test below was rewritten because it would have **passed with its
mechanism broken or absent**. This list is the audit trail for the rule, not a summary of the diff:

| Control | Would have passed while broken because… | Rev 3 |
|---|---|---|
| SC-016 / `TestBounds_PerStatusSubBounds` | exercised the **default** call only, where the sub-bounds hold; a caller-supplied `limit=30` deletes every `blocked` row | Extended to assert under `limit=30` against 25 `queued` / 25 `running` / 3 `blocked` (FR-016's round-robin allocation) |
| SC-013 / `TestPlanEngine_CapSnapshotIsLockFreeAndGlobal` | asserted values against a **controlled fixture**, so a second, divergent re-derivation of the count passes it | Asserts **identity** with the number `admitLocked` computed in the same pass — a parallel implementation cannot satisfy it |
| SC-003 / `TestListJobs_CapPressureWithoutAdmit` | (rev 2 already caught half of this) asserted `Admit` uncalled, not the emitted values | Also asserts `cap_active=0`, `cap_max=16`, `engine_running=false` and a present `cap_observed_at` under a **stopped** engine — the case FR-029's staleness rule previously suppressed |
| SC-002 | the mapping is size-independent by construction, so the "500 rows" clause tested nothing and had no implementing test (R2-MIN-006) | Size clause dropped; replaced by an assertion that the value is **composed from the constant**, which is what can actually regress (C1) |
| SC-006 / SC-017 | stated as exact counts that become false on the first install exceeding FR-032(d)'s ceiling | Re-scoped to below-ceiling populations, with the above-ceiling case given its own criterion (SC-021) and its own marker |
| SC-011 | *"zero substrings of length ≥ 4"* false-positives on ordinary text (`http`, `test`, `1234`) — it fails on **correct** output (R2-MIN-007) | Re-stated over the secret's **distinctive** 8-byte windows plus the full value, with the corpus fixed so the criterion is reproducible |
| `TestListJobs_MemoTTL` | asserted *same-args repetition* — the one call pattern under which a principal-keyed memo is harmless | Replaced by `TestListJobs_NoCrossScopeReuse`, which asserts the **property** (a call reflects its own scope and its own arguments) and therefore fails if a memo is ever added |
| `TestListJobs_DescriptionContract` | asserted the **presence** of ≥ 6 clauses with no bound on total size, on a string resident in every agent's prompt on every request (R2-MAJ-014) | Also asserts a **900-character maximum** (FR-012b), and the operator-facing material moves to the runbook |
| `TestDelegateMint_StampsParentAgentID` | covered **one** mint site while FR-034 requires the field on **every** generation mint | Required **per mint site**, enumerated in the Regression table rather than named once (R2-MAJ-015) |

---

## Verified Corrections to ADR-056

These were verified in the working tree for this spec. They supersede the ADR's prose.

| # | ADR-056 says | Working tree says | Consequence |
|---|---|---|---|
| C1 | D4: subagents are scoped by "the durable lifecycle parent linkage" | **No such field exists.** `LifecycleRecord` has no `ParentAgentID`; `AgentID` is the **child's** `[VERIFIED: pkg/tools/delegate.go:952 — `AgentID: agentID`, the delegate *target*]`; `ScopeID` (the field PS S9 row 4 renames from `OwnerScopeID`) is `""` for a top-level delegation `[VERIFIED: delegate.go:935-940]`; `ParentDurableKey` is the transcript id parent and child **share** `[VERIFIED: delegate.go:924 + pkg/agent/subturn.go:970]` | **Schema change required** (FR-013). Confirms R2-CRIT-001. |
| C2 | §2: "the real risk is **silent permissive inheritance** via `compositor.go:175-201`" | The compositor **fails closed only when NEITHER side has an entry** — one branch of four. `case g == "" && a == "": … return config.ToolPolicyDeny` `[VERIFIED: pkg/tools/compositor.go:181-188]`; but `case a == "": return g` returns the **global verdict verbatim** when only the per-agent map is missing `[VERIFIED: compositor.go:191-192]`, and `bash`, `read_file`, `write_file`, `set_config` and `create_agent` are all seeded **`allow`** globally `[VERIFIED: pkg/config/defaults.go:284-286, 364-366]`. **And before any of that runs, `if cfg.GodMode { return config.ToolPolicyAllow }` short-circuits the entire merge** `[VERIFIED: compositor.go:175-177]` | The ADR's "permissive floor" hazard does not exist as described, but neither does a blanket "fails closed". See the ⚠️ block below — this wording was itself a rev-1 defect. |
| C3 | D4: "two of three kinds have cheap, existing owner filters" | **One.** `plan.Filter` is `struct { WorkspaceID string }` — no owner field at all `[VERIFIED: pkg/plan/store.go:120-124]` | Plan owner-scoping needs a new filter field (FR-014). Confirms R2-MAJ-001. |
| C4 | D4/R4: the plan owner predicate is `Owner`, with a mixed-namespace caveat | `plan.Plan` carries **three** attribution fields. `OwnerAgentID` is **required** and is **always an agent id**, validated on both write paths `[VERIFIED: pkg/plan/plan.go:361-363 "the agent woken at plan decision points; required"; pkg/tools/plan.go:285 + pkg/gateway/rest_plans.go:546; validator at pkg/tools/plan.go:239-251]`. `Owner`/`CreatedBy` are the mixed-namespace ones (`callerID` on the tool path, `c.Username` on REST) | **`OwnerAgentID` is the correct predicate.** It eliminates R4's mixed-namespace risk entirely *and* covers the user-authored-plan case the ADR would have missed. Supersedes R2-MIN-006/007. |
| C5 | D2: cap pressure "costs nothing extra… the data is free" | `Admit` takes the engine's **exclusive** mutex and re-scans the plan store `[VERIFIED: pkg/agent/plan_engine.go:2182-2186 — `pe.mu.Lock()` → `admitLocked` → `computeActiveLocked`]` | Derive cap pressure from the plan list this tool already reads; **never call `Admit`** (FR-021). Confirms R2-MAJ-002. |
| C6 | D8: "seeded `allow` for all non-system agents" | The 4 base agents **and every user-created custom agent** are seeded via `denyAllThenOverride`, which writes an explicit **`deny` for every name in `allStaticToolNames`** then applies overrides `[VERIFIED: pkg/coreagent/core.go:384-394, 436, 1499-1514]`. A global `allow` **loses** to a per-agent `deny` (deny-wins, `compositor.go:193-196`) | **Fresh installs deny by default** unless `list_jobs: allow` is added to each override map. This is the inverse of R2-CRIT-003's claim and was previously unreported. See FR-023. |
| C7 | (implied by R2-CRIT-003) upgrades get `deny` backfilled by `RepairIncompleteToolPolicyCoverage` | **Only if the global seed is missed.** `loadConfig` starts from `DefaultConfig()` and unmarshals the on-disk JSON **on top**; Go's `encoding/json` reuses a non-nil map and **keeps entries absent from the JSON** `[VERIFIED: pkg/config/migration.go:18-41; pkg/config uses stdlib `encoding/json`, no custom unmarshaler on the sandbox config; **empirically confirmed** by a standalone round-trip harness]`. A global `list_jobs` entry therefore reaches **existing** configs automatically, and `ValidateToolPolicyCoverage` short-circuits on it `[VERIFIED: pkg/config/validate.go:459-461]`, so `RepairIncompleteToolPolicyCoverage` never fires `[VERIFIED: validate.go:534-537]` | **No bespoke migration needed.** R2-CRIT-003 is real but its mechanism was mis-diagnosed; the fix is one line in `defaults.go`. |
| C8 | FR-3/Option B: the tool hands back a working `session_id` ("durable, owner-scoped form") | All eight `session_id`-bearing `delegate` actions resolve through a **process-global in-memory map**: `resolved, found := t.sessionIndex[sid]; if !found { return ErrorResult("No subagent found with session ID: …") }` `[VERIFIED: pkg/tools/delegate.go:302-305, 1316-1322]` | Durable **listing**, not durable **acting**. Row must carry `actionable` (FR-011). Confirms R2-CRIT-004. |
| C9 | R1/G1: subagent enumeration is a **cost** (latency) risk | `LifecycleStore.List` also **aborts the entire enumeration** on any per-record error other than not-found `[VERIFIED: pkg/session/lifecycle.go:576-583]`, and `Load` takes the **per-session striped mutex** the live delegation write path needs `[VERIFIED: lifecycle.go:342-348]` | One corrupt file kills the whole kind (FR-018); enumeration contends with delegation writes (FR-022). Confirms R2-MAJ-008/009. |

> **⚠️ REV-2 CORRECTION TO C2 ITSELF.** Rev 1 led this row with a bolded *"the compositor
> **fails CLOSED**"*, then stated the actual rule correctly two clauses later. The bolded
> summary contradicted its own evidence, and a bolded "fails CLOSED" is what an implementer
> skimming a correction table carries away. Two things are true and neither is "fails closed":
>
> 1. **Only the both-empty branch denies.** `case a == "": return g` returns the **global**
>    verdict, and the global seed is `allow` for high-blast-radius tools including `bash`,
>    `write_file`, `set_config` and `create_agent`. An agent missing a per-agent entry for
>    `list_jobs` therefore inherits `allow` from the global seed — which is exactly what
>    FR-023(b) relies on for `IDWorker` (`tightenGlobalCeiling` emits a **sparse** map).
> 2. **God-mode short-circuits the whole merge**, and rev 1 missed it entirely (so did the
>    review's own §2). `if cfg.GodMode { return config.ToolPolicyAllow }` runs *before* both
>    `resolveFromMap` calls `[VERIFIED: pkg/tools/compositor.go:175-177]`, so under sandbox
>    `off` neither map is consulted. Dataset "Tool-policy resolution" row 8 (*"both sides
>    absent → deny + Error log"*) is **wrong under god-mode**; rev 2 adds row 10 and an SC-008
>    god-mode clause. This is a coverage gap, not a wording quibble.
>
> **Accurate one-line rule**, superseding the framing rev 1 recorded below:
> *god-mode floors everything at `allow`; otherwise the non-empty side wins; if both sides are
> empty it fails closed to `deny`; if both are non-empty, deny > ask > allow*
> `[VERIFIED: compositor.go:175-201]`.

**Correction to the task framing, for the record:** the brief handed to this spec stated that "an
agent with no per-agent policy map inherits the PERMISSIVE global default." That is imprecise in a
way that matters — but **less wrong than rev 1's own headline**. For a tool whose global seed is
`allow`, "inherits the global default" is precisely what happens; the brief's error was calling it a
*floor* rather than an *inheritance*, and calling it unconditional when the both-empty case denies.
See the ⚠️ block above for the accurate rule.

---

## Existing Codebase Context

### Symbols Involved

| Symbol | Role | Context |
|--------|------|---------|
| `session.LifecycleRecord` (`pkg/session/lifecycle.go:185-243`) | **modifies** | Durable per-delegated-session JSONL record. Gains `ParentAgentID` (FR-013). **Disk-only — NOT a wire field.** The struct is explicitly marked `// not-wire-format: internal disk record` `[VERIFIED: pkg/session/lifecycle.go:183]`, and two neighbouring fields (`ParentDurableKey`, `OriginChannel`/`OriginChatID`) carry *"Not part of the generated wire shape"* in their own doc comments `[VERIFIED: :215-222, :226-232]`. `ParentAgentID` joins that category — see FR-013 and the ⚠️ block there. |
| `session.LifecycleFilter` (`:512-538`) | **modifies** | Gains a `ParentAgentID` clause in `matches`. Already carries `WorkspaceID`, `AgentID`, `States`, `NonTerminalOnly` `[VERIFIED: lifecycle.go:512-523 (struct); matches at :525-539]`. |
| `session.LifecycleStore.List` (`:570-589`) | **extends** | Needs a skip-and-count sibling (`ListLenient`) — today one bad file aborts everything (C9). |
| `plan.Filter` (`pkg/plan/store.go:120-123`) | **modifies** | Gains `OwnerAgentID` (C3/C4). Today it is literally `struct { WorkspaceID string }` `[VERIFIED: pkg/plan/store.go:120-123]`. |
| `plan.Store.List` (`pkg/plan/store.go:161-170`) | **extends** | **Rev 2.** Needs a lenient sibling too (FR-027). Today it skips a corrupt file and **discards the count**: `slog.Warn("plan: skip unreadable plan file", …); continue` `[VERIFIED: pkg/plan/store.go:163-167]` — so FR-018's `unreadable` is unobtainable for this kind without a change. |
| `task.Filter` (`pkg/task/store.go:162-181`) | **modifies** | **Rev 2 — was wrongly listed "No change".** It has `WorkspaceID`, `Status`, `AgentID`, `CreatedBy`, `PlanID`, `Surface`, `ParentTaskID`+`ParentTaskIDSet`, `BlockedByID`, `Tag` `[VERIFIED: pkg/task/store.go:162-181]` — but **no `PlanIDSet`**, and `matches` treats `PlanID == ""` as *filter off* `[VERIFIED: :198-200]`. FR-010's "standalone tasks only" predicate is therefore inexpressible today. Gains `PlanIDSet bool` (FR-026), mirroring the existing `ParentTaskIDSet` convention. |
| `task.Store.List` (`pkg/task/store.go:250-258`) | **extends** | **Rev 2.** Same skip-and-swallow shape as `plan.Store.List` `[VERIFIED: pkg/task/store.go:254-255]`. Needs the same lenient sibling (FR-027). |
| `tools.ToolAgentID` (`pkg/tools/base.go:203-206`) | **calls** | The calling principal. Verified populated on the real turn path, **unconditionally** `[VERIFIED: pkg/agent/loop.go:6356 `turnCtx = tools.WithAgentID(turnCtx, ts.agent.ID)`; also task_executor.go:442/:1876, judge.go:537, loop.go:4681]`. |
| `tools.ToolWorkspaceID` (`pkg/tools/base.go:230-233`) | **calls** | Workspace scoping (FR-009). **Rev 2: conditionally injected** — `if ts.opts.WorkspaceID != "" { turnCtx = tools.WithWorkspaceID(…) }` `[VERIFIED: pkg/agent/loop.go:6381-6383]` — so it returns `""` on any turn whose channel binding carries no workspace, and every store reads `""` as *filter off*. FR-009 must name that behaviour rather than assume it away. |
| `DelegateTool.mint` (`pkg/tools/delegate.go:941-959`) | **modifies** | Populates the new `ParentAgentID` from `ToolAgentID(ctx)`, **fail-closed on empty** (FR-015). |
| `DelegateTool.sessionIndex` / `.mu` (`pkg/tools/delegate.go:298-305`) | **extends** | **Rev 2 — missing from rev 1 entirely.** FR-011's `actionable` resolves against this **unexported** map guarded by `t.mu` — the same mutex every `status`/`inbox`/`inbox_ack`/`steer`/`respond`/`cancel`/`follow_up`/`peek` call takes. There is **no exported accessor** today `[VERIFIED: pkg/tools/delegate.go:299-305; no exported reader among the 40+ `func (t *DelegateTool)` methods]`. FR-028 adds a **batch** one. |
| `agent.PlanEngine` cap authority (`pkg/agent/plan_engine.go:2182-2247`) | **extends** | **Rev 2 — missing from rev 1 entirely.** `cap_active` must come from the engine's own count, not a re-derivation (CRIT-001). `computeActiveLocked` counts the **unfiltered** plan store plus registered `activeCounters` and returns a `reliable` flag `[VERIFIED: plan_engine.go:2221-2247]`. FR-029 adds a lock-free snapshot accessor. |
| `tools.GeneralBuiltinMetadata()` | **modifies** | **Rev 2.** `buildKnownBuiltinToolNames` iterates this plus `browser.BrowserBuiltinMetadata()` and `systools.AllTools(nil, nil)` `[VERIFIED: pkg/gateway/gateway.go:715-745]` — so "registering the tool is enough" is true **only** if it is registered *here specifically*. Named explicitly in FR-001. |
| agent registry (`DelegateAgentRegistry`, via `DelegateTool.getAgentRegistry`) | **calls** | **Rev 2.** FR-005 says the subagent label is the target agent's *name*, but `LifecycleRecord.AgentID` stores an **id** `[VERIFIED: pkg/session/lifecycle.go:203]`. Resolving one to the other needs the registry, and needs a stated fallback when the agent was since deleted or renamed (FR-005). |
| `task.Task` (`pkg/task/task.go:214-...`) | **modifies** | **Rev 3 (R2-CRIT-005).** Gains `CreatedByAgentID` (FR-037) — an agent-id-namespaced attribution field, because `CreatedBy` is written as `c.Username` on the REST path `[VERIFIED: pkg/gateway/rest_tasks.go:847]` and as `callerID` on the tool path `[VERIFIED: pkg/tools/task.go:531]`, and `pkg/task/task.go:314-316` documents both `Owner` and `CreatedBy` only as *"server-set attribution (read-only on the wire)"* with **no** namespace constraint. **Disk-only** — the struct carries `// not-wire-format: internal disk struct` `[VERIFIED: pkg/task/task.go:214]`. |
| `pkg/audit` | **calls** | **Rev 2/3.** FR-032(a) requires exactly one persisted `audit.Entry` per call — the tool is a P0 security boundary (US-3) and had no forensic trail at all in rev 1. **Rev 3**: the in-tree pattern is the registry-injected `auditLoggerAware` contract (`SetAuditLogger`) plus a hand-built `audit.Entry` passed to `Logger.Log`, precedent `RememberTool` `[VERIFIED: pkg/tools/memory.go:110-125, 240-267; audit.Entry at pkg/audit/audit.go:228-251; EventToolCall / DecisionAllow / DecisionError at :42-89]`. A nil logger is a best-effort no-op. This is a **different subsystem from slog**, and rev 2 conflated them. |
| `pkg/config/defaults_test.go` (`:92-96`) | **modifies** | **Rev 3 (cross-spec C2).** `const wantToolCount = 83` `[VERIFIED]` goes red the moment FR-023(b) adds the global seed entry. Per operator ruling 4 the literal is **deleted** and replaced by the mechanical invariant, not re-hardcoded. Rev 2 did not mention this file at all — the fact is recorded only in the sibling spec, which is why this spec's own review could not see the contradiction with SC-015. |
| `config.PlanningConfig` (`pkg/config/planning.go:31-...`) | **modifies** | **Rev 3 (R2-CRIT-003).** Gains `CapSnapshotStalenessSeconds` (default **90**). Sits alongside `GlobalActiveLoopCap` (default 16) `[VERIFIED: pkg/config/planning.go:17, 54-56]`, which is the denominator the staleness bound qualifies. Note the tick it is derived from, `defaultPlanEngineTickInterval = 30 * time.Second`, is a **package const, not a config key** `[VERIFIED: pkg/agent/plan_engine.go:131]` — so the bound cannot be derived at runtime and must be stated. |
| `config.ToolsConfig` | **modifies** | **Rev 3 (R2-CRIT-004).** Gains `list_jobs.max_records_scanned_per_kind` (default **5 000**) and `delegate.require_parent_agent_id` (default **true**, FR-015's rollback — R2-MAJ-015). |
| `coreagent.allStaticToolNames` (`pkg/coreagent/core.go:296-334`) | **modifies** | Must gain `"list_jobs"` or `validateOverrideKeys` **panics** at boot on any seed naming it `[VERIFIED: core.go:346-370]`. |
| `coreagent.coreAgentSeed` / `systemAgentSeed` / `NewCustomAgentToolsCfg` (`core.go:436`, `:847`, `:1499`) | **modifies** | Per-agent grants (C6). |
| `config.DefaultConfig().Sandbox.ToolPolicies` (`pkg/config/defaults.go:282`) | **modifies** | Global ceiling entry — the upgrade path (C7). |
| `gateway.buildKnownBuiltinToolNames` (`pkg/gateway/gateway.go:715-745`) | **calls** | Derives from live metadata, so registering the tool is enough; a sync test enforces parity with `allStaticToolNames`. |
| `tools.TaskListTool` (`pkg/tools/task.go:27-80`) | **calls** (comparison) | Precedent + the overlap question (US-6). Note its own defects: unbounded `json.Marshal(tasks)` and `agentID := ToolAgentID(ctx)` with **no empty check** `[VERIFIED: task.go:60, :74-78]`. |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|----------------|------------|----------------|----------------|
| `session.LifecycleRecord` | MEDIUM | `LifecycleStore.Persist/Mutate/Load/tail`, `delegate.go` mint, ADR-053 boot sweep (`plan_engine.go:578`) | every ADR-053 lifecycle test |
| `session.LifecycleFilter` | LOW | `matches`, `List` | boot sweep queries |
| `plan.Filter` | LOW | `Filter.matches`, `Store.List` callers | `rest_plans.go`, `plan_engine.go` |
| `task.Filter` (**rev 2**, +`PlanIDSet`) | LOW | `Filter.matches`, `Store.List` callers | `list_tasks`, `pkg/gateway` task endpoints, `plan_engine.promoteReadyStandaloneTasks` |
| `plan.Store.List` / `task.Store.List` (**rev 2**, lenient siblings) | LOW | new functions; existing `List` untouched | none — additive, mirroring `ListLenient` |
| `DelegateTool` (**rev 2**, +batch accessor) | MEDIUM | every `t.mu` holder: `status`, `inbox`, `inbox_ack`, `steer`, `respond`, `cancel`, `follow_up`, `peek`, `executeAsync` | live delegation dispatch latency (SC-012) |
| `PlanEngine` (**rev 2**, +cap snapshot) | MEDIUM | `Admit`/`admitLocked`, `Tick` | `tryStartApprovedPlan`; `/goal` and `/loop` admission |
| `coreagent.allStaticToolNames` | **HIGH** | `validateOverrideKeys` (**panics** on drift), `denyAllThenOverride`, `tightenGlobalCeiling`, `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` | every agent's seeded policy map; boot coverage validation |
| `config.DefaultConfig()` global `ToolPolicies` | MEDIUM | `ValidateToolPolicyCoverage`, `RepairIncompleteToolPolicyCoverage`, compositor resolution | every install, fresh and upgraded |
| `DelegateTool` mint | **HIGH** (**raised in rev 3 — R2-MAJ-015**) | async + sync delegate paths, **every** mint site | ADR-053 lifecycle tests; **every delegation in the product** |
| `task.Task` (**rev 3**, +`CreatedByAgentID`) | LOW | `task.Store` persist/load, the two tool-path creation sites | `list_tasks`, gateway task endpoints (both unaffected — additive, REST leaves it empty) |
| `config.PlanningConfig` / `config.ToolsConfig` (**rev 3**, +3 keys) | LOW | boot validator's zero-value defaulting, `DefaultConfig()` | every install (additive keys, defaults applied when zero) |

> **⚠️ REV-3: why the mint risk moved to HIGH.** FR-015 converts a previously-working code path in the
> **core delegation feature** into a hard failure, to guarantee a field that only `list_jobs` reads.
> Its failure mode is *"delegation stops"*, not *"a field is missing"* — and rev 2 shipped it with
> **no flag, no fallback and one positive-path test against a single mint site**, while FR-034
> requires the field on **every** generation mint including a `follow_up`/Play path that does not
> exist yet. A2 verifies `ToolAgentID` on four call sites and offers FR-008 as *"the backstop for
> any path that is not"* — but FR-008 backstops the **read** side; nothing backstopped the write
> side, and the FR-023 kill switch disables `list_jobs`, not the mint guard. Rev 3 keeps fail-closed
> as the default (it is right) and makes it **operable**: `tools.delegate.require_parent_agent_id`,
> a named rollback in the runbook, and a positive-path regression test **per mint site**.

> **⚠️ HIGH-risk flag for the implementer.** Two of these break loudly rather than silently, which is
> the good case: adding `list_jobs` to a seed override map **without** adding it to
> `allStaticToolNames` **panics the process** `[VERIFIED: core.go:361-368]`. Do both, in the same commit.

### Relevant Execution Flows

| Flow Name | Relevance |
|-----------|-----------|
| Agent turn → tool dispatch | Supplies the calling principal via `WithAgentID` (loop.go:6356). The **only** guarantee `list_jobs` scoping rests on. |
| `delegate.run` → lifecycle mint → async goroutine | Where `ParentAgentID` must be stamped. |
| Gateway boot → `repairAndValidateToolPolicyCoverage` → registry build | Where a missed catalog entry surfaces (`gateway.go:1541`, hot-reload at `:2112`). |
| `PlanEngine.Tick` → `tryStartApprovedPlan` → `Admit` → `admitLocked` → `computeActiveLocked` | The cap-waiting semantics `queued` is derived from — and the mutex this tool must **not** take. ⚠️ **REV-3 CORRECTION, replacing rev 2's row.** Rev 2 stated *"the scan the snapshot needs is already happening"*; it is not. `Tick` **does** perform its own unfiltered `pe.planStore.List(plan.Filter{})` at `:679`, but `computeActiveLocked` is reached **only** through `Admit` (under `pe.mu`) and performs a **second, independent** `List` at `:2221ff` `[VERIFIED — `computeActiveLocked` has exactly one call site, `admitLocked` at `:2189`]`. `Tick` also **returns early** on a `List` error (`:679-682`) and **skips entirely** on the `claimTick()` overlap guard (`:673-676`). Consequences that shaped FR-029: the snapshot must be published from **inside** `admitLocked` (not recomputed in `Tick`); the liveness heartbeat must be stamped **before** the early return; and the snapshot refreshes only when admission runs — which is fine, because admission is when the number changes, and `tryStartApprovedPlan` runs it on every tick that has an approved plan. |
| `delegate` action → `t.mu` → `sessionIndex` lookup | **Rev 2.** FR-011's `actionable` reads the same map, under the same mutex, that every steer/cancel/inbox call needs. FR-028 bounds it to one acquisition per call. |
| ADR-053 boot sweep (`plan_engine.go:578` → `boot_sweep.go:71`) | Reconciles non-terminal sessions to `failed(interrupted)` on restart — the reason post-restart rows are tombstones (C8), and the direct cause of CRIT-003. |

### Cluster Placement

Primarily the **agent tooling** cluster (`pkg/tools`), spanning **plan/task/session persistence**
(`pkg/plan`, `pkg/task`, `pkg/session`), **config/policy** (`pkg/config`, `pkg/coreagent`) and
**audit** (`pkg/audit`). ⚠️ **Rev 3 correction:** rev 1 wrote *"the `pkg/session` change is the only
one that crosses into a generated wire contract"*, which contradicts FR-013 — `ParentAgentID` is
**disk-only** and explicitly must not be added to `SessionLifecycleRecord.yaml`. **No change in this
spec crosses a generated wire contract at all** (FR-025, SC-014), including the new
`task.Task.CreatedByAgentID` (FR-037), whose struct carries the same `not-wire-format` marker
`[VERIFIED: pkg/task/task.go:214]`. The sentence was a rev-1 leftover that survived the rev-2
correction of the requirement it contradicted.

---

## User Stories & Acceptance Criteria

### User Story 1 — Recover a lost handle for live work (Priority: P0)

An agent starts background work — a plan, a delegated subagent, a standalone task — and later loses
the handle: its context window was trimmed, or a wake started a fresh turn. Today the work is still
running and still consuming budget, but the agent has no way to name it. Seven of the nine `delegate`
actions require a `session_id` the agent must already hold `[VERIFIED: pkg/tools/delegate.go:468 —
inbox, inbox_ack, steer, respond, cancel, follow_up, peek]`. `list_jobs` returns one bounded roster
of the caller's own background work, each row carrying the handle that kind's other tools accept.

> **The ownership axis, stated explicitly (rev 2, closes review MAJ-005).** "The caller's own
> background work" is two different relations, and rev 1 mixed them silently. Every row therefore
> carries an explicit `relation` field:
>
> | `relation` | Meaning | Which rows |
> |---|---|---|
> | `runs` | **Work I execute.** The caller is the agent doing it. | `plan` (`Plan.OwnerAgentID` is *"the agent woken at plan decision points"* `[VERIFIED: pkg/plan/plan.go:361-363]`); `task` where `Task.AgentID == caller` (*"the assigned agent"* `[VERIFIED: pkg/task/task.go:234-235]`) |
> | `dispatched` | **Work I handed to someone else.** The caller kicked it off; another agent executes it. | `subagent` (`LifecycleRecord.ParentAgentID`); `task` where `Task.CreatedByAgentID == caller` (**rev 3** — *not* `CreatedBy`, which is mixed-namespace; FR-037) |
>
> Rev 1 implemented only the `dispatched` half of the `task` kind, which meant **an agent
> executing a live standalone task assigned to it saw nothing** — the single most literal reading
> of *"what am I still working on?"*, and the Evaluation Scenario's own prompt. The in-tree
> `list_tasks` already exposes exactly this split: `role="assignee"` filters `AgentID`,
> `role="delegator"` filters `CreatedBy` `[VERIFIED: pkg/tools/task.go:55-67]`. `list_jobs` takes
> the **union** of both (FR-010), deduplicated by task id, with `relation` naming which side
> matched — `runs` wins when both do.
>
> **Rev 3: `list_jobs` copies the split, NOT the field.** `list_tasks`' `role="delegator"` filters
> `Task.CreatedBy`, which is written as a **username** on the REST path
> `[VERIFIED: pkg/gateway/rest_tasks.go:847 `CreatedBy: c.Username`]` and as an **agent id** on the
> tool path `[VERIFIED: pkg/tools/task.go:531 `CreatedBy: callerID`]`. Following the precedent
> literally would have imported a live username/agent-id collision disclosure into a P0 story —
> which is what rev 2 did (R2-CRIT-005). FR-037 introduces `Task.CreatedByAgentID` and FR-010 reads
> that. The in-tree precedent's own defect is filed under A7, not inherited here.

**Why this priority**: This is the ADR's raison d'être. Without it no other story has value.

**Independent Test**: Start an async delegation, discard the returned `session_id`, call `list_jobs`
in the same process lifetime, and confirm the row's `id` is accepted by `delegate(action="status")`.

**Acceptance Scenarios**:

1. **Given** agent A has started an async delegation to agent B in this process, **When** A calls `list_jobs`, **Then** a row appears with `kind="subagent"`, `id` = the durable session id, `label` = B's agent name, `status="running"`, and `actionable=true`.
2. **Given** agent A owns a plan in `running` state, **When** A calls `list_jobs`, **Then** a row appears with `kind="plan"` and `id` = the plan id, which `execute_plan` accepts.
3. **Given** agent A created a standalone task (`plan_id == ""`) in `in_progress`, **When** A calls `list_jobs`, **Then** a row appears with `kind="task"`, `id` = the task id and `relation="dispatched"`.
4. **Given** agent A has no background work at all, **When** A calls `list_jobs`, **Then** the response is a well-formed empty roster, not an error — and it carries **no** diagnostic counters at all (FR-033's omit-when-zero convention), so "nothing to report" costs the caller nothing.
5. **Given** a standalone task with `agent_id = A` and `created_by_agent_id = "daniel-the-agent"` in `in_progress`, **When** A calls `list_jobs`, **Then** a row appears with `kind="task"` and `relation="runs"` — work assigned *to* A is visible, not only work A dispatched.
6. **Given** agent A owns 400 `queued` plans, of which exactly one is titled "Migrate the audit chain", **When** A calls `list_jobs` with `label_contains="audit chain"`, **Then** that plan is returned — even though it is row 312 of its group and the `queued` sub-bound is 25. (Rev 3, R2-MAJ-007: without this the tool's headline use case fails deterministically for any caller with more than 25 jobs in a group, reporting the failure only as a large `omitted` count.)

---

### User Story 2 — Tell "queued", "running" and "stuck" apart (Priority: P0)

The second failure mode is indistinguishable silence. Plan execution is genuinely asynchronous and
admission-capped (default global cap **16**, shared across plans, `/goal` and `/loop`
`[VERIFIED: pkg/config/planning.go:17; pkg/agent/plan_engine.go:2182,2248]`), so a plan may
legitimately sit doing nothing. The agent must distinguish *waiting for a slot* from *working* from
*stuck and needing intervention* — otherwise it either interrupts healthy work or waits forever on
dead work.

**Why this priority**: ADR-056 v1 was blocked precisely because its vocabulary had no "stuck" slot.
Shipping without it re-creates the problem the tool exists to solve.

**Independent Test**: Drive one plan into each of `approved`, `running/dispatching`,
`running/stalled`, `done`, `failed` and assert five distinct normalized values.

**Acceptance Scenarios**:

1. **Given** a plan in `state=approved`, **When** `list_jobs` is called, **Then** its `status` is `queued` and never `running`.
2. **Given** a plan with `state=running` and `plan_phase=stalled`, **When** `list_jobs` is called, **Then** its `status` is `blocked` and its `native_status` is **not** the bare string `running`.
3. **Given** a plan with `state=running` and `plan_phase=awaiting_supervision`, **When** `list_jobs` is called, **Then** its `status` is `blocked`, its `attention` is `elsewhere`, and `native_status` reflects `awaiting_supervision`, which takes precedence over `stalled`. (Rev 3 / cross-spec C1: PS renames this phase from `awaiting_owner_correction`; the literal is adopted here **before** any code exists, and FR-006a forbids hand-typing it so a future rename cannot silently diverge.)
4. **Given** a plan that failed with `failed_reason=judge_rounds_exhausted` and another with `stopped_by_user`, **When** `list_jobs` is called, **Then** both have `status=failed` and **different** `native_status` values.
5. **Given** a plan in `state=approved` while the plan engine is **not** admitting and the installation's global active count is 0, **When** `list_jobs` is called, **Then** the response carries `cap_active=0` and `cap_max=16` — so "nothing will ever start it" is distinguishable from "waiting for a slot".
6. **Given** a plan in `state=approved` owned by A, and **16 running plans owned by other agents in other workspaces**, **When** A calls `list_jobs`, **Then** `cap_active` is **16**, equal to `cap_max` — the caller sees a saturated cap and correctly waits. The value MUST reflect the **installation-wide** count that the cap actually brakes, not the caller's own scoped rows.
7. **Given** the engine's active count could not be computed reliably (a plan-store or `activeCounter` error), **When** `list_jobs` is called, **Then** `cap_active` and `cap_max` are **both absent** from the response — never present-and-wrong.
8. **Given** four `blocked` rows — a task with an unmet dependency, a plan at `awaiting_supervision`, a `stalled` plan, and a subagent in `needs_input` — **When** `list_jobs` is called, **Then** they carry **three different** `attention` values: `none`, `elsewhere`, `caller`, `caller`. An agent reading the roster can tell *"nothing to do"* from *"someone else is on it, do not touch"* from *"this is waiting on you"*. (Rev 3, operator ruling 3 + cross-spec M6: without this split, `blocked` means all three at once, and the one available action on the second case destroys healthy work.)

> **⚠️ CRIT-001 — the defect these three scenarios exist to prevent.** Rev 1's FR-021 derived cap
> pressure *"from the plan list the tool already reads"*. That list is filtered by `WorkspaceID`
> **and** `OwnerAgentID` (FR-009, FR-010). The number the cap is actually compared against is
> neither: `computeActiveLocked` counts `pe.planStore.List(plan.Filter{})` — **unfiltered, every
> workspace, every owner** — plus every registered `activeCounter` (the `/goal` and `/loop`
> subsystems, which are **not in the plan store at all**)
> `[VERIFIED: pkg/agent/plan_engine.go:2221-2247]`, against a **global** `cap_max` of 16
> `[VERIFIED: pkg/config/planning.go:17 `DefaultGlobalActiveLoopCap`; resolved at
> plan_engine.go:2248-2254]`.
>
> A caller-scoped numerator against a global denominator is not "slightly off" — it **inverts**.
> Scenario 6 is the exact case the field was added for, and rev 1's derivation reports
> `cap_active=0, cap_max=16` for it: *"far below cap, nothing will ever start it"* → the agent
> intervenes on healthy work. Worse, rev 1's own test (`TestListJobs_CapPressureWithoutAdmit`,
> which asserted only that `Admit` was **not** called) passes while it does so. Rev 1 correctly
> rejected calling `Admit` (C5) and then substituted a number that is not the same number.
>
> Rev 2's resolution is FR-029: a **lock-free read-only snapshot accessor** on `PlanEngine`
> exposing the last `computeActiveLocked` result, its `reliable` flag and its observation time —
> `Admit` is not the only way to reach that data. `admitLocked` already carries the `reliable`
> fail-closed signal `[VERIFIED: plan_engine.go:2191-2203]`; scenario 7 propagates it instead of
> discarding it.

---

### User Story 3 — Never see another principal's work (Priority: P0)

`list_jobs` returns human-readable labels and live, steerable handles. Scoping is therefore a
security control, not a convenience. Every store the tool reads treats the empty string as *filter
disabled* `[VERIFIED: pkg/task/store.go:193 `if f.CreatedBy != "" && …`; pkg/session/lifecycle.go:529
`if f.AgentID != "" && …`; pkg/plan/store.go:125 same shape for WorkspaceID]`, so an unresolvable
calling principal would return the **entire installation**. The in-tree precedent is already
unguarded: `list_tasks` passes `ToolAgentID(ctx)` straight into a filter with no non-empty check
`[VERIFIED: pkg/tools/task.go:60]`.

**Why this priority**: An information-disclosure path through the one decision that defines scope.
D4's stated reason for rejecting workspace-wide scope is that it "leaks other agents' work" — an
empty principal leaks strictly more.

**Independent Test**: Invoke `Execute` with a context carrying no agent id and assert an **error**
— not an empty list, and emphatically not a full list.

**Acceptance Scenarios**:

1. **Given** a context with no agent id, **When** `list_jobs` executes, **Then** it returns an error naming the unresolvable principal and **zero** rows.
2. **Given** a context whose agent id is whitespace-only, **When** `list_jobs` executes, **Then** it is treated identically to empty and returns an error.
3. **Given** agents A and B each own one plan, one subagent and one task, **When** A calls `list_jobs`, **Then** exactly A's three rows are returned and none of B's.
4. **Given** agent A is bound to workspace W1 and also owns a plan in W2, **When** A calls `list_jobs` from a W1 context, **Then** only W1 rows are returned; each row carries `workspace_id`, and the response carries `workspace_scoped=true`.
5. **Given** a turn whose channel binding carries **no** workspace, so `ToolWorkspaceID(ctx)` is `""`, **When** A calls `list_jobs`, **Then** the roster spans every workspace **for A only**, and the response carries `workspace_scoped=false` so the caller knows which answer it got. It MUST NOT silently present a cross-workspace roster as a scoped one.
6. **Given** a human user whose gateway **username is `"mia"`** — the same string as a base agent's id, which is public — who created a standalone task in the SPA, **When** the agent `mia` calls `list_jobs`, **Then** that task does **not** appear in `mia`'s roster, and neither does its title. (Rev 3, R2-CRIT-005: rev 2's `Task.CreatedBy` predicate is mixed-namespace in this tree — `c.Username` on the REST path, `callerID` on the tool path — so this human's whole standalone task backlog surfaced in the agent's roster as `relation="dispatched"`, silently, with no counter and no marker.)

> **⚠️ Workspace scoping fails OPEN, and rev 1 did not say so (closes review MAJ-009).** US-3
> argues the fail-closed case correctly for the *agent* id and then does not make the identical
> argument for the *workspace*. The value is **conditionally injected**:
>
> ```go
> // pkg/agent/loop.go:6381-6383  [VERIFIED]
> if ts.opts.WorkspaceID != "" {
>     turnCtx = tools.WithWorkspaceID(turnCtx, ts.opts.WorkspaceID)
> }
> ```
>
> `ToolWorkspaceID(ctx)` is a bare type-assert returning `""` when unset
> `[VERIFIED: pkg/tools/base.go:230-233]`, and every store reads `""` as *filter off*. Contrast
> `WithAgentID`, which is **unconditional** at the same site `[VERIFIED: loop.go:6356]` — the
> asymmetry is deliberate on the injection side, so the tool must handle it rather than assume it
> away.
>
> **Rev 2 does not fail closed here**, because workspace-less turns are legitimate (that guard
> exists for a reason) and failing closed would make the tool unusable on every unbound channel.
> The disclosure is bounded to *the caller's own work across workspaces* — never another
> principal's — which is why this is a labelling requirement rather than an error. What rev 2
> forbids is the silent version: FR-009 requires `workspace_scoped` on every response, and
> `TestListJobs_WorkspaceScoped` (which constructs an explicit workspace and therefore never
> exercised the production path it was meant to protect) gains a sibling that constructs a
> workspace-less context.

---

### User Story 4 — A bounded, honest, reproducible roster (Priority: P1)

A firehose is worse than no tool: it burns the caller's context and buries the row it was hunting.
But a *silently* truncated list is worse still — it asserts "nothing else exists", which is false.
The SPA's `RECENTLY_FINISHED_CAP = 8` is exactly the pattern not to copy.

**Why this priority**: Correctness of the answer under load. NFR-1's guarantee is void if the live
limit silently drops the row the caller needs.

**Independent Test**: Create 5 jobs over each per-group sub-bound (30 `queued` / 30 `running` / 20
`blocked`) and assert the response is capped per group, the omission count is exact in **both** key
spaces, and repeated calls return the **same** rows in the same order. Then repeat with `limit=30`
against 25/25/3 and assert the three `blocked` rows survive.

**Acceptance Scenarios**:

1. **Given** more live jobs than a status group's sub-bound, **When** `list_jobs` is called, **Then** the response carries an exact `omitted` count in **both** key spaces — `by_kind` and `by_status`, each summing to `total_omitted` — never a silent drop. (Rev 3, R2-MAJ-002: rev 2 said "per kind" in three requirements and asserted per status group in the only test that checked values, so that test could not be written from the spec.)
2. **Given** the same unchanged store state, **When** `list_jobs` is called twice, **Then** both calls return the same rows in the same order — guaranteed by a **total** ordering function (FR-020), not by luck.
3. **Given** a job whose label exceeds the label limit, **When** `list_jobs` is called, **Then** the label is truncated to the limit with a visible ellipsis and the row is otherwise intact.
4. **Given** a large `queued` and `running` population — far more than the whole live bound — **and** at least one `blocked` row, **When** `list_jobs` is called, **Then** the `blocked` row is present. `blocked` sorts **last** of the three live groups (operator ruling 1 keeps ADR D3's order), so its survival depends entirely on its own reserved sub-bound, not on its sort position.
5. **Given** `include_terminal=false` (the default), **When** `list_jobs` is called, **Then** no `completed` or `failed` rows are **materialized into the response**, and the response carries `terminal_suppressed` — an exact count of the terminal rows that exist and were withheld, per kind and in total.

> **⚠️ CRIT-003 — the empty roster that lies (rev 2).** Three rev-1 requirements composed into the
> exact failure US-4 forbids. (1) The ADR-053 boot sweep reconciles **every** persisted
> non-terminal session with no live runtime turn to `failed(interrupted)` on restart
> `[VERIFIED: pkg/agent/plan_engine.go:578 → pkg/agent/boot_sweep.go:71]` — so after *every*
> gateway restart, all of the caller's subagent rows are terminal. (2) `include_terminal` defaults
> to `false` and terminal rows were *"excluded entirely"*. (3) Never scanned ⇒ never counted;
> FR-017's exact-count rule covered bound-driven truncation, not the `include_terminal` exclusion.
>
> Net: agent A spawns three delegations, the gateway restarts, A calls `list_jobs` with default
> arguments and receives `rows: [], total_omitted: 0` — **byte-identical to the shape US-1 AS-4
> defines as "A has no background work at all."** That is the SPA's `RECENTLY_FINISHED_CAP = 8`
> anti-pattern this spec names as *"exactly the pattern not to copy"*, with a worse trigger: it
> fires on every restart rather than only under load. Rev 1's US-6 scenario quietly conceded the
> problem by passing `include_terminal=true`, which the agent has no reason to do — it calls the
> tool precisely because it does not know the state. The greenfield ruling makes this **more**
> exposed, not less: cutting `unattributable_subagents` removes the last field that could have
> hinted something was omitted.
>
> `terminal_suppressed` (FR-031) closes it. Note the honest consequence, which rev 1 got wrong in
> the other direction: **a count still requires the scan** (MAJ-003 — every store loads every
> record regardless of filter), so AS-5's rev-1 claim that *"no terminal-store scan cost is paid"*
> was false on its own terms and is deleted, not merely softened.

---

### User Story 5 — Degrade honestly, never silently (Priority: P1)

Three independent file-backed stores are read per call. Any of them can fail, and one of them fails
in a way that takes out an entire kind: `LifecycleStore.List` returns `nil, err` on the first
unreadable record `[VERIFIED: pkg/session/lifecycle.go:576-583]`, so one torn JSONL — the kind a full
disk or an OOM-killed write produces — would erase every subagent row.

**Why this priority**: The tool's entire value is being trusted. A short list that looks complete is
the worst possible output.

**Independent Test**: Corrupt one lifecycle JSONL among many, call `list_jobs`, and assert the other
rows return with an explicit unreadable-record count.

**Acceptance Scenarios**:

1. **Given** one corrupt lifecycle record among N, **When** `list_jobs` is called, **Then** the remaining N−1 rows are returned and `unreadable` reports exactly 1 for the `subagent` kind.
2. **Given** the plan store directory is unreadable, **When** `list_jobs` is called, **Then** an explicit per-kind error entry appears for `plan` while `task` and `subagent` still return rows.
3. **Given** every store fails, **When** `list_jobs` is called, **Then** three per-kind error entries are returned — not a bare empty roster.
4. **Given** one corrupt **plan** file among N, **When** `list_jobs` is called, **Then** the remaining N−1 plan rows are returned and `unreadable` reports exactly 1 for the `plan` kind.
5. **Given** one corrupt **task** file among N, **When** `list_jobs` is called, **Then** the remaining N−1 task rows are returned and `unreadable` reports exactly 1 for the `task` kind.
6. **Given** a store holding more records of a kind than the configured per-call scan ceiling, **When** `list_jobs` is called, **Then** the response carries `scan_truncated` for that kind naming how many records were **scanned** and how many are **present** — so a bounded scan is never presented as a complete one. (Rev 3, R2-CRIT-004: this is the steady state on an old install, not an exception — A5 states the stores are never swept.)
7. **Given** a record whose native state maps to no normalized value — a plan written by a newer build, or a hand-edited file — **When** `list_jobs` is called, **Then** a row is returned carrying an explicit unknown marker and counted in `notes.unmapped`; the process does not panic, the row is not dropped, and it is not silently coerced to `failed`.

> **⚠️ `unreadable` was unobtainable for two of the three kinds (rev 2, closes review MAJ-001).**
> Rev 1's Integration Boundaries cited the plan store as the in-tree precedent for FR-018:
> *"Unreadable/corrupt files are already logged at Warn and skipped."* True — and **that is a
> precedent for skipping, not for counting**:
>
> ```go
> // pkg/plan/store.go:163-167  [VERIFIED]
> p, err := s.load(id)
> if err != nil {
>     slog.Warn("plan: skip unreadable plan file", "id", id, "error", err)
>     continue                    // ← the count is discarded, never returned
> }
> ```
>
> `pkg/task/store.go:254-255` is the same shape `[VERIFIED]`. Both **swallow** the count. Rev 1's
> design work went entirely into `ListLenient` for the lifecycle store — the one kind that today
> *aborts* — leaving the two kinds that already skip with no way to report, so an implementer
> following rev 1 would ship `unreadable` permanently pinned at **0** for `plan` and `task` while
> US-5's premise is *"a short list that looks complete is the worst possible output."* Two of three
> kinds would have shipped exactly that. FR-027 adds the matching lenient siblings; scenarios 4–5
> and two new dataset rows cover them.

---

### User Story 6 — Know when a returned handle is dead (Priority: P1)

After a gateway restart the durable lifecycle record survives but the in-memory `sessionIndex` that
every `delegate` action resolves through does not (C8). Additionally the ADR-053 boot sweep
reconciles every persisted non-terminal session with no live runtime turn to `failed(interrupted)`
`[VERIFIED: pkg/agent/plan_engine.go:578]`. So a post-restart subagent row is a tombstone whose
`session_id` no action will accept.

**Why this priority**: Handing back a handle that fails on use is worse than admitting the handle is
gone — the agent wastes a turn discovering it.

**Independent Test**: Spawn an async delegation, restart the process, call `list_jobs`, and assert
the row is terminal and flagged non-actionable.

**Acceptance Scenarios**:

1. **Given** a subagent spawned before a restart, **When** `list_jobs` is called after the restart, **Then** the row's `status` is `failed`, `native_status` names the reconciliation reason, and `actionable=false`.
2. **Given** a subagent spawned in the current process, **When** `list_jobs` is called, **Then** `actionable=true` and the id resolves in `delegate`.
3. **Given** the registered `list_jobs` tool, **When** its `Description()` string is read, **Then** it contains (a) a statement that a non-actionable id is informational only, (b) a statement that the roster is a best-effort near-snapshot, (c) a statement that a handle is meaningful only paired with its `kind`, (d) a statement that `attention="elsewhere"` means another agent is handling the row and the caller must not intervene, and (e) the omit-when-zero convention — **and its total length does not exceed 900 characters**, and it does **not** contain the operator-facing kill switch, runbook or `ask` guidance, which live in the operator documentation (FR-012b). Asserted by `TestListJobs_DescriptionContract` against `Description()` directly.
4. **Given** a plan or task row whose `status` is `failed` or `completed`, **When** `list_jobs` is called with `include_terminal=true`, **Then** that row carries `actionable=false` — the same honesty the `subagent` kind already got, applied to the two kinds where it is equally true (`execute_plan` will not run a `done` plan; the task action tools will not act on a `done` task).

> **Rev 2 note (closes review MAJ-012).** Rev 1 traced FR-012's and FR-024's *"MUST be stated in
> the tool description"* clauses to `TestListJobs_PostRestartTombstone` and `TestListJobs_ReadOnly`
> — tests that assert on row fields and on directory byte-identity respectively, and on **no
> string at all**. Both clauses therefore had **zero** coverage, and this acceptance scenario was
> the only one in the spec with no corresponding BDD scenario (the structural check *"every
> acceptance scenario has ≥1 BDD scenario"* failed exactly here). Rev 2 adds the scenario, adds
> `TestListJobs_DescriptionContract`, and re-points the matrix rows for FR-012, FR-024 and FR-035.

---

### User Story 7 — Ship enabled, on fresh installs *and* upgrades (Priority: P0)

A tool that is registered but denied is dead in the field and green in CI — the ADR-037
anti-pattern CLAUDE.md explicitly bans. Two independent paths must both grant it: the global
ceiling (which reaches existing configs via the `DefaultConfig()`-then-unmarshal map merge, C7) and
every per-agent seed built by `denyAllThenOverride`, which would otherwise stamp an explicit `deny`
that **wins** over the global `allow` (C6).

**Why this priority**: Without this the feature does not exist for any user.

**Independent Test**: Boot with a `config.json` written before `list_jobs` existed and assert a
non-system agent resolves `allow`. Separately, seed a fresh install and assert the same for Mia,
Jim, Ava and Ray.

**Acceptance Scenarios**:

1. **Given** a fresh install, **When** the config is seeded, **Then** `list_jobs` resolves to `allow` for Mia, Jim, Ava, Ray, Worker and the seeded specialists.
2. **Given** a `config.json` containing no `list_jobs` key anywhere, **When** the gateway boots, **Then** `list_jobs` resolves to `allow` for every non-system agent and `RepairIncompleteToolPolicyCoverage` backfills **nothing** for it.
3. **Given** any System Agent, **When** policy is resolved, **Then** `list_jobs` is `deny` unless that agent's `systemAgentSeed` explicitly enumerates it.
4. **Given** a newly created custom agent, **When** its tools config is seeded, **Then** `list_jobs` resolves to `allow`.
5. **Given** `list_jobs` is named in a seed override map, **When** the process starts, **Then** it does **not** panic — i.e. `allStaticToolNames` contains it.
6. **Given** sandbox `off` (god-mode), **When** policy is resolved for **any** agent including a System Agent, **Then** `list_jobs` is `allow` — `if cfg.GodMode { return config.ToolPolicyAllow }` short-circuits the merge before either map is consulted `[VERIFIED: pkg/tools/compositor.go:175-177]`. This is expected, is the documented behaviour of god-mode, and MUST be covered rather than left as an unmodelled branch.

---

### User Story 8 — An operator can see it, audit it, and switch it off (Priority: P1)

`list_jobs` enumerates human-readable labels and steerable handles and is a P0 security boundary
(US-3). Rev 1 had **no operability section at all**: no audit entry, no metrics, no runbook, and no
documented kill switch. A cross-agent scoping bug would have left no forensic trail — the
"Cross-agent probing under adversarial prompting" evaluation scenario had nothing to read after the
fact — and an operator had no way to learn that 40 % of an install's lifecycle records are corrupt
or that every roster is truncating, because every counter is reported to the *caller* only.

**Why this priority**: CLAUDE.md Constraint #7 and the project's audit subsystem (`pkg/audit`)
both exist; a security-boundary tool that bypasses them is a gap, not a simplification.

**Independent Test**: With the process configured at log level **Info** (Debug off, as in production),
call `list_jobs` once with a populated store and one corrupt record; assert exactly one **persisted
`pkg/audit` entry** carrying the principal, workspace, scoping flag, kinds read and row count, plus
exactly one Warn for the non-zero `unreadable`. The Debug counters line is optional and is not the
forensic record. (Rev 3, R2-MAJ-011: rev 2's version put the security record at Debug, which is off
in production — an entry that is compiled in but not emitted leaves the same absence US-8 exists to
close.)

**Acceptance Scenarios**:

1. **Given** any `list_jobs` call, **When** it completes, **Then** exactly one **`pkg/audit` entry** is written — regardless of log level — carrying `principal`, `workspace_id`, `workspace_scoped`, kinds read and row count; and, separately and optionally, one **Debug** slog line carrying the diagnostic counters. Neither carries a `label` or a `native_status`. The audit entry is the security control; the Debug line is the debugging aid, and rev 2 conflated the two into one thing at a level that is off in production.
2. **Given** a call whose `unreadable` count is non-zero, or which produced any per-kind error entry, **When** it completes, **Then** an additional entry is emitted at **Warn** naming the affected kind — so a degrading install is visible without a caller reporting it.
3. **Given** an operator sets the global `sandbox.tool_policies` entry `"list_jobs": "deny"`, **When** any non-system agent resolves the policy, **Then** it is `deny` (deny-wins over the per-agent seeded `allow`), and this is documented as **the** supported kill switch in both FR-023 and the tool description.
4. **Given** a fresh install, **When** an operator reads the tool documentation, **Then** it explains what a non-zero `unreadable` means and what to do about it.

---

## Behavioral Contract

**Primary flows**

- When an agent with a resolvable id calls `list_jobs`, the system returns a bounded roster of that agent's own plans, delegated subagents and standalone tasks, sorted `queued → running → blocked → failed → completed` (ADR-056 D3's order, **unchanged** — operator ruling 1), tiebroken by `started_at` **ascending within the live groups and descending within the terminal groups** (FR-007) and then by `(kind, id)` ascending so the order is **total**.
- When a row is returned, the system includes `kind`, `id`, `label`, `status`, `native_status`, `relation`, `attention`, `started_at`, `last_activity_at`, `workspace_id`, `actionable` and `intentionally_stopped`. The normative shape is the *Response Shape* section.
- When a row is `blocked`, the system states **who must act** via `attention`: `none` (informational — a task waiting on a dependency), `caller` (a stalled plan, a subagent in `needs_input`), or `elsewhere` (a plan being adjudicated by another agent, which the caller must not intervene on).
- When a row's `status` is `failed` or `completed`, the system sets `actionable=false` for **every** kind.
- When `include_terminal` is false (default), the system returns only `queued`, `running` and `blocked` rows, **and** reports `terminal_suppressed` — an exact count of the terminal rows withheld.
- When a `kind` argument is supplied, the system reads only that kind's store.
- When the calling turn carries no workspace, the system returns the caller's rows across every workspace and sets `workspace_scoped=false`.

**Error flows**

- When the calling agent id is empty or whitespace-only after trimming, the system returns an error and zero rows. This test is purely **lexical** — a syntactically valid but unknown agent id succeeds with an empty roster (there is no registry lookup).
- When a store read fails entirely, the system returns an explicit per-kind error entry and still returns every other kind's rows.
- When an individual record is unreadable, the system skips it, counts it, and reports the count — it does not abandon the kind. This holds for **all three** kinds.
- When a **known** argument is invalid (unknown `kind`, unknown `status`, non-boolean `include_terminal`, non-integer or non-positive `limit`, over-long `label_contains`), the system returns a validation error and zero rows.
- When an **unknown** argument is supplied, the system ignores it and reports it in `notes.ignored_args` — it does not error, for the same reason it clamps `limit` rather than rejecting it.
- When `status` names a terminal value, the system implies `include_terminal=true` rather than returning an empty roster.
- When a record's native state maps to no normalized value, the system emits the row with an explicit unknown marker and counts it — it never panics, drops it, or calls it `failed`.
- When a kind's record count exceeds the configured per-call scan ceiling, the system reports `scan_truncated` for that kind and that kind's counts become lower bounds — it never presents a bounded scan as a complete one.
- When `limit` exceeds the hard maximum, the system **clamps** it and reports `limit_clamped_to` — it does **not** error. (Rev 2: rev 1's error-flow bullet and its own Edge Cases and BDD table disagreed on this; clamping wins because it is the one disposition that never costs the caller a turn.)
- When `delegate`'s lifecycle mint cannot resolve a parent agent id, it returns an error and mints **no** record — so no unattributable lifecycle record can exist.

**Boundary conditions**

- When the caller has no background work, the system returns an empty roster — not an error — with `notes: null` and every diagnostic counter omitted (FR-033). The cap fields and `attention` are **not** diagnostics and are unaffected.
- When rows exceed a bound, the system truncates deterministically and reports an exact omission count per kind and in total.
- When any free-text field (`label`, `native_status`) exceeds its maximum, the system redacts sensitive data **first**, then truncates on a rune boundary.
- When a subagent row's session is not resolvable in the current process, the system sets `actionable=false`.
- When the plan engine's cap snapshot is stale, the system **emits it anyway** with `cap_observed_at` and `engine_running=false` — it omits the pair only when the snapshot is absent or unreliable.
- When two calls arrive back to back with different scope or different arguments, each answer reflects **its own** scope and arguments — nothing is served from a cache.
- When god-mode is enabled, `list_jobs` resolves to `allow` for every agent regardless of either policy map.

---

## Edge Cases

- **Empty parent agent id at mint time.** (Rev 2 — replaces the deleted *"Unattributable legacy lifecycle records"* case.) `ToolAgentID(ctx)` is empty or whitespace-only when `delegate` tries to mint a lifecycle record. Expected: **the mint fails closed** — an error is returned and **no record is written** (FR-015). Not "written with an empty parent and counted later": on a greenfield install an unattributable record is a bug, not a data class.
- **Empty vs. whitespace principal.** Expected: identical — both error (US-3.2).
- **Workspace-less turn.** `ToolWorkspaceID(ctx)` is `""` because the turn's channel binding carries no workspace and the injection is guarded `[VERIFIED: pkg/agent/loop.go:6381-6383]`. Expected: the caller's rows across all workspaces, `workspace_scoped=false`, each row still carrying its own `workspace_id`. Never silently presented as a scoped roster (US-3.5).
- **A standalone task assigned to the caller but created by someone else.** `AgentID == caller`, `CreatedByAgentID != caller`, `PlanID == ""`. Expected: returned, with `relation="runs"` (US-1.5). Rev 1 dropped this row entirely.
- **A standalone task created in the SPA by a human whose username equals an agent id.** `CreatedBy == "mia"` (a username), `CreatedByAgentID == ""`, and the caller is agent `mia`. Expected: **not returned**, and its title not returned. The `dispatched` predicate reads `CreatedByAgentID` only, and an empty value never matches (FR-037, rev 3 / R2-CRIT-005).
- **A standalone task both created by and assigned to the caller.** Expected: exactly **one** row, `relation="runs"` (the `runs` reading wins the tie) — never two rows from the union.
- **A deliberately cancelled subagent vs. a crashed one.** Both normalize to `status=failed`. Expected: distinguished by `intentionally_stopped=true` on the cancelled one, derived from the **closed** portion of each kind's reason field (`session.LifecycleCancelled`, `plan.FailedReasonStoppedByUser` `[VERIFIED: pkg/plan/plan.go:292]`) — never by parsing free text.
- **An abandoned `draft` plan.** A plan drafted and never approved stays `draft` indefinitely, and plans are never swept (A5). Expected: **excluded by default**; visible only with `include_drafts=true`, and ranked **last** within the `queued` group when included, so drafts are the first thing truncated.
- **A subagent session resumed to a new generation.** `LifecycleRecord` carries `Generation int` and `ResumedFrom string` `[VERIFIED: pkg/session/lifecycle.go:187-188]`, and `Persist` enforces that a terminal record is never mutated in place — `follow_up`/Play mint a **new** record with a new generation. Expected: a row represents the **newest** generation only; `ParentAgentID` is carried forward on every generation mint; the row carries `generation` when > 0.
- **A plan-owner session that is also a delegated subagent.** `LifecycleRecord.SupervisedPlanID` (today `OwnsPlanID`, renamed by PS S9 row 5 — which lands first) marks a session that owns a plan `[VERIFIED: pkg/session/lifecycle.go:199]`. Note the cross-spec finding that the field has **no non-test writer** today, so this case is synthetic-fixture-only until PS's supervision path writes it. If such a session also carries `ParentAgentID`, it would appear as **both** a `plan` row and a `subagent` row for the same work. Expected: both rows are returned and are **not** deduplicated — they are genuinely different handles onto different things (the plan, and the session executing it) — but the `subagent` row's `label` MUST name the owned plan so the caller can see the relationship rather than reading it as duplicate work.
- **God-mode enabled.** Expected: `list_jobs` resolves `allow` for every agent, System Agents included, because the compositor short-circuits before consulting either map. Behaviour of the tool itself is unchanged; only the policy verdict differs.
- **A `native_status` carrying a registered secret.** `LifecycleRecord.FailedReason` is documented *"Left open (not a closed enum)"* `[VERIFIED: pkg/session/lifecycle.go:236-239]` and `plan.Plan.PausedReason` is a bare `string` never passed to any validator `[VERIFIED: pkg/plan/plan.go:378; only `!= ""` is ever tested, e.g. :122]`, so runtime-wrapped errors, paths and upstream API text reach these fields. Expected: redacted and bounded exactly like `label` (FR-019, FR-030).
- **A `limit` between the sub-bound sum and the hard maximum** (e.g. `limit=150` with a 75-row live maximum). Expected: accepted, **not** reported as clamped (150 ≤ 200), and the caller receives at most 75 live rows — which is why FR-016 must state `limit`'s relationship to the sub-bounds explicitly rather than leaving the caller to infer it.
- **A target agent that has since been deleted or renamed.** The subagent label resolves `LifecycleRecord.AgentID` (an **id**) through the agent registry to a display name. Expected: when the id no longer resolves, `label` is the raw agent id — never empty, never an error.
- **All rows tie on an empty `started_at`.** Every `queued` plan (`state=approved`/`draft`) and every `queued` task (`inbox`/`next`) has never started, so `StartedAt` is `""` `[VERIFIED: pkg/plan/plan.go:442 `json:"started_at,omitempty"`; pkg/task/task.go:320 same]`. Expected: a **total** order still holds via the `(kind, id)` final tiebreak (FR-020) — `sort.Slice` is **not** stable, so without it `TestSortOrder_Deterministic` would flake in CI rather than fail in review.
- **A plan whose `OwnerAgentID` differs from its `Owner`.** A human authors a plan in the SPA (`Owner = username`) that agent X runs (`OwnerAgentID = X`). Expected: the row appears for **X**, not for the username. This is the case the ADR's `Owner`-based predicate would have missed (C4).
- **Self-delegation.** An agent delegating to itself mints a record whose `AgentID` and `ParentAgentID` are both that agent. Expected: exactly **one** row, not two.
- **Nested delegation.** A → B → C. Expected: A sees only B; B sees only C. Grandchildren never leak upward.
- **Plan-member tasks.** Expected: never returned as `task` rows (`plan_id != ""` excluded), consistent with ADR-055's rule that observation happens at plan level.
- **Concurrent mutation during the read.** The three stores are read non-atomically, so a plan may be observed `running` while a member task is already `done`. Expected: tolerated and documented — the roster is a **best-effort near-snapshot**, not a transactional one. It must never error and never panic.
- **`limit=0`.** Expected: validation error (use `kind`/`status` to narrow, not a zero limit).
- **`limit` above the hard maximum.** Expected: **clamped** to the maximum and `limit_clamped_to` reported — **not** an error. (Rev 2 resolves the rev-1 contradiction where the Behavioral Contract demanded an error while the Edge Cases and the BDD table both demanded a clamp; the implementation would have silently followed the table and left the contract as decoration.)
- **Label containing a credential-shaped string.** Expected: replaced by `FilterSensitiveData` **before** truncation, so truncation cannot split a secret across the boundary and defeat the replacer.
- **Label shorter than `FilterMinLength` (8) that *is* a secret.** `FilterSensitiveData` returns content under that length **unchanged** `[VERIFIED: pkg/config/config.go:398-401]`. Expected: still not leaked — the label path must not depend on the replacer's fast-path bypass (FR-019a).
- **`tools.filter_sensitive_data` disabled by the operator.** Expected: the label bound is still enforced, and the reduced redaction guarantee is documented rather than silently assumed away.
- **Unicode label at exactly the limit.** Expected: truncated on a rune boundary, never mid-rune — no invalid UTF-8 in the response.
- **Engine stopped with approved plans present.** Expected: `queued` **plus** cap-pressure fields showing `active` far below `cap` — `cap_active` and `cap_max` **present** even though the snapshot is stale and even though `cap_active` is `0` — accompanied by `cap_observed_at` and `engine_running=false` (US-2.5, FR-029(d)). Rev 2 omitted the fields in this exact state, via two independent rules.
- **A plan the Owner stopped, read back immediately.** `stop_plan` writes `failed(stopped_by_user)` synchronously, so the row flips to `failed` + `intentionally_stopped=true` with no stale-`running` window. But with the default `include_terminal=false` the plan the agent just stopped **vanishes from the roster entirely**, surviving only as `terminal_suppressed` — and calling `list_jobs` right after `stop_plan` is the single most likely thing an agent does. Expected: tolerated and **documented**; `terminal_suppressed` is what makes it recoverable, and `include_terminal=true` returns it. Separately, `cap_active` still counts the stopped plan for up to one tick (30 s), which is why `cap_observed_at` is emitted. (Rev 3, cross-spec m2.)
- **An agent that has `list_jobs` but not `delegate`.** Expected: it receives `actionable=true` subagent rows it cannot act on. **Accepted and documented** rather than fixed: `actionable` describes whether the *handle resolves*, not whether the *caller is permitted*, and coupling the row field to the compositor verdict would make a data field depend on the policy layer. See Ambiguity #12 — this is the one open item where the honest answer may still be to couple them.
- **A `blocked` row whose `attention` is `none`.** A task waiting on an unmet dependency. Expected: returned, ranked in the `blocked` group, and understood as **information only** — the operator's ruling 3 case. It is the reason `blocked` sorting last is acceptable, and it must not be conflated with the `caller` and `elsewhere` cases in the same group.
- **Duplicate ids across kinds.** Expected: `(kind, id)` is the identity; a plan id and a task id may collide without ambiguity.

---

## Explicit Non-Behaviors

- The system must **not** return the `shell` kind, because background shells carry no agent id, are in-memory only, self-delete on a `StartTime`-based (not completion-based) reaper, and would leak raw command lines. Deferred by ADR-056 D7, tracked as issue #564.
- The system must **not** return child transcripts, member task output, tool results, or shell stdout — summary rows only. An agent that wants detail calls that kind's own tool.
- The system must **not** return spend, token counts or cost. Operator decision; adding it invites the tool being called as a budget poller.
- The system must **not** return plan-member tasks, because intervention and observation happen at plan level (ADR-055).
- The system must **not** return an agent's whole authored task backlog — only **standalone** tasks (`plan_id == ""`) that the agent either runs (`agent_id == caller`) or dispatched (`created_by_agent_id == caller`), live, or terminal within the bound. (Rev 2: rev 1's *"only standalone tasks **it created**"* locked in the delegator reading without ever considering the assignee one — see US-1's ownership-axis block. **Rev 3: the dispatched predicate moved off `created_by`, which is mixed-namespace in this tree — FR-037.**)
- The system must **not** fall back to an unfiltered list when the principal is unresolvable, because that discloses every plan title and steerable handle in the installation.
- The system must **not** call `PlanEngine.Admit`, because it takes the engine's exclusive mutex and re-scans the plan store (C5) — a read-only visibility tool must never contend with the dispatch path.
- The system must **not** mutate any store, cancel, steer, retry or restart anything. It is strictly read-only.
- The system must **not** silently cap, in the manner of the SPA's `RECENTLY_FINISHED_CAP = 8`, because a silent cap asserts "nothing else exists" and actively misleads.
- The system must **not** infer a subagent's parent from `ParentDurableKey`, `ScopeID` or `AgentID` under any circumstance, because `ParentDurableKey` is **shared** between parent and children `[VERIFIED: pkg/tools/delegate.go:924 + pkg/agent/subturn.go:970]` and inferring from it would return siblings, cousins and grandchildren (C1); `ScopeID` (PS S9 row 4's rename of `OwnerScopeID`) is `""` for a top-level delegation; and `AgentID` is the **child's** id, not the parent's. `ParentAgentID` is the only parent linkage, full stop. **This rule survives the greenfield ruling unchanged** — it was never about legacy data, and dropping it with the rest of the legacy machinery would re-open the sibling/cousin leak that C1, the spec's strongest correction, exists to prevent.
- The system must **not** write a lifecycle record with an empty `ParentAgentID` (FR-015). There is no "unattributable" data class on a greenfield install; an empty parent is a bug that must surface at mint, not a row to be silently dropped or counted after the fact.
- The system must **not** call `PlanEngine.Admit` **nor re-derive the active count itself** from its own owner-scoped plan list, because the cap is global and a caller-scoped numerator against a global denominator inverts the signal (CRIT-001). It reads the engine's own snapshot or reports nothing.
- The system must **not** perform a registry lookup to validate the calling principal. A syntactically valid but unknown agent id succeeds with an empty roster; only a lexically empty id fails closed. (Rev 2: rev 1's Behavioral Contract said *"or unresolvable"*, implying a lookup FR-008 never required and dataset row 5 explicitly contradicts.)
- The system must **not** be granted to System Agents by default, because `systemAgentSeed`'s `denyAllThenOverride` shape is boot-re-enforced and a System Agent receives only its enumerated grant `[VERIFIED: pkg/coreagent/core.go:847, 1331-1339]`.
- The system must **not** change, deprecate or remove `list_tasks` or `delegate status` in this change — see US-6 / Ambiguity #4.
- The system must **not** use `Task.CreatedBy` (or `Task.Owner`, or `Plan.Owner`, or `Plan.CreatedBy`) as an **ownership or authorization predicate**, because all four are mixed-namespace: the REST paths write a **username** and the tool paths write an **agent id** `[VERIFIED: pkg/gateway/rest_tasks.go:847 `c.Username`; pkg/tools/task.go:531 `callerID`; and C4 for the plan pair]`. A human user whose username collides with a public agent id would otherwise have their work attributed to that agent, silently. Only agent-id-namespaced fields — `Plan.OwnerAgentID`, `Task.AgentID`, `Task.CreatedByAgentID`, `LifecycleRecord.ParentAgentID` — may be predicates, and an empty value must never match. (Rev 3, R2-CRIT-005. This is stated as a **rule about the class of field**, not as a fix to one predicate, because rev 2 fixed it for plans in C4 and then re-imported it for tasks one requirement later.)
- The system must **not** memoize, cache, or serve any part of a previous call's roster — not per principal, not per argument set, not at any TTL. Every call reads the stores. A cache keyed on the principal alone serves a workspace-less turn's cross-workspace roster to a later scoped turn (defeating US-3) and returns the previous answer to every narrowed call; a cache keyed on the full argument set fixes those but bounds no cost, because an agent varying `limit` bypasses it. FR-032(d)'s scan ceiling is the cost control. (Rev 3, R2-CRIT-001.)
- The system must **not** be granted to **PlanSupervisor**, and PlanSupervisor's own supervision work must **not** be expected to appear on any roster. This is a **recorded decision, not an oversight** (cross-spec C3): PlanSupervisor can never be a plan's `OwnerAgentID` — both write paths are `IsChatTarget()`-guarded `[VERIFIED: pkg/config/config.go:1052-1054]` and it is seeded as a System Agent — and its supervision session is created by the wake path rather than by `delegate`, so it mints no `LifecycleRecord` and is no `subagent` row either. The consequence is accepted and stated so the next author does not "fix" it: **the engine's `supervision.wake_at` deadline is the only liveness control for a parked plan**, the adjudicator itself is roster-blind, and parked plans are visible to the plan's Owner alone — as `blocked` with `attention="elsewhere"`, which is exactly the signal that tells the Owner not to act on them. Granting `list_jobs` to PlanSupervisor would additionally break the sibling spec's deliberate `len(allowed) == 1` complement-complete invariant, which exists to stop a future catalog addition silently landing in the most privileged agent's allow set.

---

## Integration Boundaries

### `pkg/plan` — plan store (file-backed JSON, one file per plan)

- **Data in**: `plan.Filter{WorkspaceID, OwnerAgentID}` (the second field is **new**, C3). Today the whole struct is `struct { WorkspaceID string }` `[VERIFIED: pkg/plan/store.go:120-123]`.
- **Data out**: `[]plan.Plan` — `ID`, `Title`, `State`, `PlanPhase`, `PausedReason`, `FailedReason`, `OwnerAgentID`, `WorkspaceID`, `StartedAt`, `LastActivityAt`.
- **Contract**: `Store.List(Filter)` **plus a new lenient sibling** (FR-027). ⚠️ **Rev 2 correction**: rev 1 cited this store as *"the in-tree precedent for FR-018"*. It is a precedent for **skipping** and not for **counting** — `slog.Warn(…); continue` discards the count entirely `[VERIFIED: pkg/plan/store.go:163-167]`, so `unreadable` is unobtainable for this kind without the sibling.
- **On failure**: directory-level error → per-kind error entry for `plan`; per-record error → skipped **and counted** (needs FR-027); other kinds unaffected.
- **Development**: real store on a temp dir, including a deliberately corrupted `*.json` fixture.
- **Cost note**: `List` scans the directory, calls `s.load(id)` for **every** `*.json`, and only then applies `filter.matches(p)` `[VERIFIED: pkg/plan/store.go:161-170]` — the filter trims the returned slice and saves **zero** I/O. Plans are never swept (`plan.Store.Delete` exists but no sweeper calls it), so this grows monotonically — the same profile the ADR attributes only to lifecycle records.

### `pkg/task` — task store

- **Data in**: `task.Filter{WorkspaceID, PlanID:"", PlanIDSet:true}` — scoped to the **workspace only** at the store level; the `CreatedBy`-OR-`AgentID` union (FR-010) is applied **in the tool**, because `task.Filter` has no OR predicate and issuing two `List` calls would double an already-full-directory scan for no I/O saving (see the cost note on `pkg/plan`). `PlanIDSet` is **new** (FR-026).
- **Data out**: `[]task.Task` — `ID`, `Title`, `Status` (one of `inbox`, `next`, `in_progress`, `blocked`, `done`, `failed` `[VERIFIED: pkg/task/task.go:40-47]`), `CreatedBy`, `AgentID`, `PlanID`, `WorkspaceID`, `CreatedAt`/`UpdatedAt`/`StartedAt` (all RFC3339 **strings** `[VERIFIED: pkg/task/task.go:318-320]`).
- **Contract**: `Store.List(Filter)` plus a lenient sibling (FR-027). ⚠️ **Rev 2 correction**: rev 1's Symbols table said `task.Filter` needed *"No change"* while this section's own data-in line already invented `PlanIDSet` in a parenthetical. Neither matched the code: `PlanID == ""` means *filter off* `[VERIFIED: pkg/task/store.go:196]`, and the `…Set` escape hatch exists for `ParentTaskID` **only** `[VERIFIED: :171-174]`. Without FR-026, "standalone tasks only" is inexpressible and an implementer would post-filter — which corrupts the bound arithmetic if bounds are applied before the discard.
- **Note on `Status`**: `task.Filter.Status` is a **single** `Status`, not a set `[VERIFIED: pkg/task/store.go:165]`, so "status ∈ {inbox, next, in_progress, blocked}" cannot be expressed in one call either. The live/terminal split is likewise applied in-tool.
- **On failure**: per-kind error entry for `task`; per-record → skipped and counted (FR-027).
- **Development**: real store on a temp dir, including a deliberately corrupted `*.json` fixture.

### `pkg/session` — durable lifecycle store (JSONL, one file per session)

- **Data in**: `LifecycleFilter{WorkspaceID, ParentAgentID, NonTerminalOnly}` (`ParentAgentID` is **new**, FR-013).
- **Data out**: `[]LifecycleRecord` — `SessionID`, `State` (8 values: `queued`, `running`, `needs_input`, `paused`, `completed`, `failed`, `cancelled`, `timed_out` `[VERIFIED: pkg/session/lifecycle.go:60-74]`), `FailedReason`, `AgentID` (the child, used for the label), `WorkspaceID`, `CreatedAt`, `UpdatedAt`.
- **Contract**: a **new** `ListLenient` that skips and counts bad records; today's `List` aborts wholesale (C9).
- **On failure**: per-record → skip + count; directory-level → per-kind error entry.
- **Development**: real store on a temp dir, including a deliberately corrupted JSONL fixture.
- **Cost + contention note**: `List` calls `s.Load(id)` for **every** session, and `Load` takes the per-session striped mutex that live delegations need to write their own state transitions `[VERIFIED: pkg/session/lifecycle.go:340-348, 570-589]`, then applies `filter.matches(rec)` to the loaded record. Enumerating N sessions acquires N such locks **regardless of filter** — `NonTerminalOnly` trims the returned slice, it does not avoid a single load or a single lock. Lifecycle records are **never swept** — `storage.retention.session_days` covers transcripts only — so this degrades monotonically.

### `pkg/tools` — `DelegateTool` session index (new integration, rev 2)

- **Data in**: a batch of durable session ids from the assembled subagent rows.
- **Data out**: which of them resolve in this process (FR-011's `actionable`).
- **Contract**: a **new exported batch accessor** (FR-028). Today `sessionIndex` is an unexported `map[string]string` guarded by `t.mu`, with no exported reader `[VERIFIED: pkg/tools/delegate.go:299-305]`.
- **On failure**: no `DelegateTool` wired → every subagent row is `actionable=false` (the honest answer: nothing can act on it in this process).
- **Contention budget**: **exactly one** `t.mu` acquisition per `list_jobs` call, never one per row. `t.mu` is the same mutex `status`/`inbox`/`inbox_ack`/`steer`/`respond`/`cancel`/`follow_up`/`peek` all take. FR-021 forbids contending with `pe.mu` on the reasoning that *"a read-only visibility tool must never contend with the dispatch path"*; rev 1 then permitted, unremarked, contention with a strictly hotter mutex.
- **Development**: real `DelegateTool` with a counting mutex wrapper asserting the one-acquisition property.

### `pkg/agent` — `PlanEngine` cap authority (new integration, rev 2)

- **Data in**: nothing (read-only accessor).
- **Data out**: `(active int, cap int, reliable bool, observedAt time.Time, lastTickAt time.Time)`.
- **Contract** (⚠️ **rewritten in rev 3 — the rev-2 text here was factually wrong**): a **new lock-free snapshot accessor** (FR-029) reading an `atomic.Pointer[capSnapshot]` **published from inside `admitLocked`**, which already holds `pe.mu` and has already computed `active`, `cap` and `reliable` `[VERIFIED: pkg/agent/plan_engine.go:2188-2203]`. Plus a `lastTickAt` heartbeat stamped at the top of `Tick`, before its `planStore.List` early return `[VERIFIED: :679-682]`. The reader MUST NOT take `pe.mu`. Today the only route to this data is `Admit`, which takes `pe.mu` **exclusively** and re-scans the store `[VERIFIED: :2182-2186]`.

  > ⚠️ **Rev 2's claim here — *"`Tick` already performs an unfiltered `pe.planStore.List(plan.Filter{})` on every pass and does not hold `pe.mu` … so the refresh is marginal cost on work already being done"* — was FALSE and is withdrawn.** `computeActiveLocked` has exactly **one** caller in the repository (`admitLocked`, `:2189`, under `pe.mu`); `Tick` never calls it; and it performs its **own** second `List` at `:2221ff` rather than reusing `Tick`'s slice. Implementing rev 2's text adds a second full scan plus a lock acquisition to every tick, forever. See FR-029 and *Rev 3*.

- **On failure**: no engine wired, no snapshot yet, or `reliable=false` → **both** `cap_active` and `cap_max` are omitted as a pair. **Staleness is NOT an omission trigger** — a stale-but-reliable snapshot is emitted with `cap_observed_at` and `engine_running=false`, because omitting it destroys the only signal that distinguishes a stopped engine from a saturated one (FR-029(d)).
- **Development**: real `PlanEngine` on a temp dir, plus a registered `activeCounter` double so the `/goal`+`/loop` contribution is exercised — the population rev 1's derivation missed. Plus a fake clock so the 90 s staleness bound is crossed deterministically rather than by sleeping.

### `pkg/config` — `FilterSensitiveData` and the two new keys (rev 3, R2-MIN-008)

- **Data in**: the raw `label` / `native_status` string; the two new config values.
- **Data out**: the redacted string; `tools.list_jobs.max_records_scanned_per_kind` (default 5 000); `planning.cap_snapshot_staleness_seconds` (default 90); `tools.delegate.require_parent_agent_id` (default true).
- **Contract**: `config.Config.FilterSensitiveData` `[VERIFIED: pkg/config/config.go:393-403]`, applied **before** truncation on every field FR-019 enumerates.
- **On failure**: **no config handle available ⇒ no redaction is possible.** The system MUST NOT emit unredacted free text in that case: `label` and `native_status` MUST be replaced by a fixed placeholder (`"[unavailable: redaction not configured]"`), and the row MUST still be emitted with its `kind`, `id`, `status` and `attention` intact — the handle is the point of the tool and it carries no free text. A missing config is an operator error, not a licence to leak.
- **Development**: real `config.Config`, including one run with `tools.filter_sensitive_data` disabled (the bound must still hold — FR-019a) and one with a nil config handle.

### `pkg/agent` — agent registry (label resolution) (rev 3, R2-MIN-008)

- **Data in**: `LifecycleRecord.AgentID` (an **id** `[VERIFIED: pkg/session/lifecycle.go:203]`).
- **Data out**: the target agent's display name.
- **Contract**: `DelegateAgentRegistry`, reached via `DelegateTool.getAgentRegistry`.
- **On failure**: **no registry wired, or the id no longer resolves ⇒ `label` is the raw agent id.** Never empty, never an error, never a placeholder that looks like a name. Durable records outlive agents, so this is a normal case (FR-005), and it is stated here as a boundary contract rather than only in prose.
- **Development**: real registry, plus a case with the target agent deleted between mint and read.

### `pkg/tools` registry + `pkg/config` policy resolution

- **Data in**: tool registration + policy maps.
- **Data out**: an effective policy of `allow` / `ask` / `deny`.
- **Contract**: coverage is OR-based per `(agent, tool)` — a global entry **or** a per-agent entry satisfies it `[VERIFIED: pkg/config/validate.go:459-464]`; resolution is **god-mode floors everything at `allow`** `[VERIFIED: pkg/tools/compositor.go:175-177]`, else deny > ask > allow with the non-empty side winning and **only** both-empty failing closed `[VERIFIED: compositor.go:181-201]`.
- **On failure**: a missing catalog entry **panics** at seed time; a missing seed produces a persisted `deny` backfill.
- **Operator kill switch**: a global `sandbox.tool_policies` entry `"list_jobs": "deny"` disables the tool for every agent (deny-wins over the per-agent seeded `allow`). This is the supported control (US-8 AS-3) and must be named in the tool documentation, not merely exercised as a test case.
- **Development**: real config structs; no mock. Coverage MUST include a god-mode row.

---

## BDD Scenarios

### Feature: `list_jobs` — unified background-job visibility

#### Scenario: Live subagent handle recovered in the same process

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** agent `A` has a resolvable agent id in the tool context
- **And** `A` has started an async delegation to agent `B` in the current process
- **When** `A` calls `list_jobs` with no arguments
- **Then** the roster contains exactly one row with `kind="subagent"`
- **And** that row's `id` equals the durable delegate session id
- **And** that row's `label` equals `B`'s agent name
- **And** that row's `status` is `running`
- **And** that row's `actionable` is `true`

---

#### Scenario: Owned plan appears with its plan id

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a plan exists with `owner_agent_id = "A"` and `state = "running"`
- **When** agent `A` calls `list_jobs`
- **Then** the roster contains a row with `kind="plan"` and `id` equal to the plan id
- **And** the row's `label` equals the plan title

---

#### Scenario: Plan authored by a human but run by the agent still appears

**Traces to**: User Story 1, Acceptance Scenario 2; Edge Case "OwnerAgentID differs from Owner"
**Category**: Edge Case

- **Given** a plan exists with `owner = "daniel"`, `created_by = "daniel"` and `owner_agent_id = "A"`
- **When** agent `A` calls `list_jobs`
- **Then** the roster contains that plan
- **And** when the principal is instead the literal string `"daniel"`, the roster does **not** contain it

---

#### Scenario: Standalone task appears; plan-member task does not

**Traces to**: User Story 1, Acceptance Scenario 3; Edge Case "Plan-member tasks"
**Category**: Happy Path

- **Given** task `T1` exists with `created_by="A"`, `plan_id=""`, `status="in_progress"`
- **And** task `T2` exists with `created_by="A"`, `plan_id="P1"`, `status="in_progress"`
- **When** agent `A` calls `list_jobs`
- **Then** the roster contains `T1`
- **And** the roster does not contain `T2`

---

#### Scenario: Empty roster is a success, not an error

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case

- **Given** agent `A` has a resolvable id and owns no plans, subagents or standalone tasks
- **When** `A` calls `list_jobs`
- **Then** the call succeeds
- **And** the roster has zero rows
- **And** the response carries **no `total_omitted` field**, no `omitted`, no `unreadable`, no `terminal_suppressed` and no error entries — `notes` is present and **`null`** (FR-033)
- **And** the response is therefore distinguishable from a post-restart call, whose `notes` carries `terminal_suppressed`

> **Rev 3 (R2-MAJ-003).** Rev 2 required `total_omitted` omitted when zero (FR-033) and US-1 AS-4
> agreed — while this scenario still asserted `total_omitted = 0` and *Bounds and truncation*
> dataset rows 1–3 still specified it as an expected **output**. `TestListJobs_EmptyRosterIsSuccess`,
> which the matrix maps to both FR-002 and FR-033, was specified twice with opposite expectations.
> This is the same class of unpropagated-change defect as rev 1's `limit` error-vs-clamp
> contradiction, and it is why rev 3 checks each new rule against every scenario and dataset row it
> touches rather than only against the requirement it came from.

---

#### Scenario Outline: Normalized status is derived correctly per kind

**Traces to**: User Story 2, Acceptance Scenarios 1–4
**Category**: Happy Path

- **Given** a job of `<kind>` whose native state is `<native>`
- **When** the caller who owns it calls `list_jobs` with `include_terminal=true` and `include_drafts=true`
- **Then** the row's `status` is `<normalized>`
- **And** the row's `native_status` is `<native_status>`

**Examples**:

| kind | native | normalized | native_status |
|------|--------|------------|---------------|
| plan | `state=approved` | `queued` | `approved` |
| plan | `state=running, phase=dispatching` | `running` | `running/dispatching` |
| plan | `state=running, phase=judging` | `running` | `running/judging` |
| plan | `state=running, phase=synthesizing` | `running` | `running/synthesizing` |
| plan | `state=running, phase=idle` | `running` | `running/idle` |
| plan | `state=running, phase=stalled` | `blocked` | `running/stalled` |
| plan | `state=running, phase=awaiting_supervision` | `blocked` | `running/awaiting_supervision` — `attention=elsewhere` (cross-spec C1/M6) |
| plan | `state=running, paused_reason=owner_disabled` | `blocked` | `running/paused:owner_disabled` |
| plan | `state=draft` | `queued` | `draft` — row present **only** when `include_drafts=true`; excluded by default |
| plan | `state=done` | `completed` | `done` |
| plan | `state=failed, failed_reason=judge_rounds_exhausted` | `failed` | `failed:judge_rounds_exhausted` |
| plan | `state=failed, failed_reason=stopped_by_user` | `failed` | `failed:stopped_by_user` |
| task | `status=inbox` | `queued` | `inbox` |
| task | `status=next` | `queued` | `next` |
| task | `status=in_progress` | `running` | `in_progress` |
| task | `status=blocked` | `blocked` | `blocked` |
| task | `status=done` | `completed` | `done` |
| task | `status=failed` | `failed` | `failed` |
| subagent | `state=queued` | `queued` | `queued` |
| subagent | `state=running` | `running` | `running` |
| subagent | `state=needs_input` | `blocked` | `needs_input` |
| subagent | `state=paused` | `blocked` | `paused` |
| subagent | `state=completed` | `completed` | `completed` |
| subagent | `state=failed, failed_reason=interrupted` | `failed` | `failed:interrupted` |
| subagent | `state=cancelled` | `failed` | `cancelled` |
| subagent | `state=timed_out` | `failed` | `timed_out` |

---

#### Scenario: A stalled plan is never reported as merely running

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Error Path

- **Given** a plan owned by `A` with `state=running` and `plan_phase=stalled`
- **When** `A` calls `list_jobs`
- **Then** the row's `status` is `blocked`
- **And** the row's `native_status` is not the bare string `running`

---

#### Scenario: awaiting_supervision outranks stalled

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a plan whose effective phase resolves to `awaiting_supervision`
- **When** `A` calls `list_jobs`
- **Then** the row's `native_status` names `awaiting_supervision` and not `stalled`
- **And** the row's `attention` is `elsewhere`, not `caller`
- **And** the emitted phase substring is **byte-equal to the exported `plan.PhaseAwaitingSupervision` constant**, not to any string literal in `pkg/tools` or `pkg/agent`

---

#### Scenario: Cap pressure distinguishes a real queue from a stopped engine

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Edge Case

- **Given** an approved plan owned by `A`
- **And** the plan engine is **stopped** — it is not ticking and not admitting — and the installation-wide active count at the last observation was `0`
- **And** the last observation is older than the 90 s staleness bound
- **When** `A` calls `list_jobs`
- **Then** the row's `status` is `queued`
- **And** the response carries `cap_active = 0` and `cap_max = 16` — **present, not omitted**, at the top level and not inside `notes`
- **And** the response carries `cap_observed_at`, whose age exceeds the staleness bound
- **And** the response carries `engine_running = false`
- **And** `PlanEngine.Admit` was not called

> **Rev 3.** Rev 2 specified this scenario and then wrote two independent requirements that each
> made it unsatisfiable: FR-033 listed the cap pair among the omit-when-zero counters
> (R2-CRIT-002), and FR-029 omitted the pair on staleness — which a stopped engine always is
> (R2-CRIT-003). This is the release gate for both. `engine_running=false` plus a visibly old
> `cap_observed_at` is what turns *"`cap_active` is 0"* from ambiguous into *"nothing will ever
> start it"*.

---

#### Scenario: Cap pressure reports the GLOBAL count, not the caller's own rows

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Error Path

- **Given** one approved plan owned by `A` in workspace `W1`
- **And** 14 `running` plans owned by other agents in other workspaces
- **And** a registered `activeCounter` reporting 2 active `/loop` runs, which are **not** in the plan store
- **When** `A` calls `list_jobs`
- **Then** `cap_active` is `16` and `cap_max` is `16`
- **And** `cap_active` is **not** `0`, `1` or `14` — i.e. it is neither the caller-scoped count nor the plan-store-only count
- **And** `PlanEngine.Admit` was not called

---

#### Scenario: An unreliable cap read omits the fields rather than reporting a wrong number

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Error Path

- **Given** the plan engine's last active-count computation set `reliable = false`
- **When** the caller calls `list_jobs`
- **Then** the response contains **neither** `cap_active` **nor** `cap_max`
- **And** the response is otherwise a normal, successful roster

---

#### Scenario: A cancelled job is distinguishable from a crashed one

**Traces to**: User Story 2, Acceptance Scenario 4; Edge Case "A deliberately cancelled subagent vs. a crashed one"
**Category**: Edge Case

- **Given** a subagent session with `state=cancelled`
- **And** a subagent session with `state=failed, failed_reason=interrupted`
- **And** a plan with `failed_reason=stopped_by_user`
- **And** a plan with `failed_reason=judge_rounds_exhausted`
- **When** the caller calls `list_jobs` with `include_terminal=true`
- **Then** all four rows have `status="failed"`
- **And** the `cancelled` and `stopped_by_user` rows have `intentionally_stopped=true`
- **And** the other two have `intentionally_stopped=false`
- **And** the distinction is derived from the closed reason enums, not from parsing `native_status`

---

#### Scenario: Draft plans are excluded by default and truncate first when included

**Traces to**: User Story 4, Acceptance Scenario 4; Edge Case "An abandoned `draft` plan"
**Category**: Edge Case

- **Given** the caller owns 26 abandoned `draft` plans and 1 `approved` plan
- **When** the caller calls `list_jobs` with no arguments
- **Then** exactly one row is returned — the `approved` plan
- **And** when the caller calls `list_jobs` with `include_drafts=true`, the `approved` plan is still present
- **And** every returned `draft` row sorts **after** the `approved` row within the `queued` group

---

#### Scenario: Unresolvable principal fails closed

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Error Path

- **Given** a tool context carrying no agent id
- **And** the stores contain plans, subagents and tasks belonging to several agents
- **When** `list_jobs` executes
- **Then** the result is an error
- **And** the result contains zero rows
- **And** the result is not an empty-but-successful roster

---

#### Scenario Outline: Principal-shaped inputs that must all fail closed

**Traces to**: User Story 3, Acceptance Scenarios 1–2
**Category**: Error Path

- **Given** a tool context whose agent id is `<principal>`
- **When** `list_jobs` executes
- **Then** the result is an error with zero rows

**Examples**:

| principal | note |
|-----------|------|
| `""` | absent from context |
| `"   "` | whitespace only |
| `"\t\n"` | whitespace only, non-space |

---

#### Scenario: Cross-agent isolation

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** agent `A` owns plan `PA`, subagent session `SA` and task `TA`
- **And** agent `B` owns plan `PB`, subagent session `SB` and task `TB`
- **When** `A` calls `list_jobs`
- **Then** the roster contains `PA`, `SA` and `TA`
- **And** the roster contains none of `PB`, `SB`, `TB`

---

#### Scenario: Workspace scoping and attribution

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Happy Path

- **Given** agent `A` owns a plan in workspace `W1` and another in `W2`
- **When** `A` calls `list_jobs` from a context bound to `W1`
- **Then** only the `W1` plan is returned
- **And** its row carries `workspace_id = "W1"`
- **And** the response carries `workspace_scoped = true`

---

#### Scenario: A workspace-less turn is labelled, not silently widened

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** agent `A` owns a plan in workspace `W1` and another in `W2`
- **And** agent `B` owns a plan in `W1`
- **And** the calling context carries **no** workspace id, because the turn's channel binding had none
- **When** `A` calls `list_jobs`
- **Then** both of `A`'s plans are returned, each carrying its own `workspace_id`
- **And** `B`'s plan is **not** returned — the agent boundary still holds
- **And** the response carries `workspace_scoped = false`

---

#### Scenario: A task assigned to the caller is visible, not only one the caller created

**Traces to**: User Story 1, Acceptance Scenario 5; Edge Case "A standalone task assigned to the caller"
**Category**: Happy Path

- **Given** standalone task `T1` with `agent_id="A"`, `created_by="daniel"`, `status="in_progress"`
- **And** standalone task `T2` with `agent_id="B"`, `created_by="A"`, `status="in_progress"`
- **And** standalone task `T3` with `agent_id="A"`, `created_by="A"`, `status="in_progress"`
- **And** standalone task `T4` with `agent_id="B"`, `created_by="daniel"`, `status="in_progress"`
- **When** `A` calls `list_jobs`
- **Then** exactly three task rows are returned: `T1`, `T2`, `T3`
- **And** `T1` carries `relation="runs"`, `T2` carries `relation="dispatched"`
- **And** `T3` appears **exactly once**, carrying `relation="runs"`
- **And** `T4` is not returned

---

#### Scenario: Nested delegation does not leak grandchildren

**Traces to**: User Story 3, Acceptance Scenario 3; Edge Case "Nested delegation"
**Category**: Edge Case

- **Given** agent `A` delegated to `B`, and `B` delegated to `C`
- **When** `A` calls `list_jobs`
- **Then** the roster contains the `A→B` session only
- **And** the roster does not contain the `B→C` session

---

#### Scenario: Self-delegation yields exactly one row

**Traces to**: User Story 3, Acceptance Scenario 3; Edge Case "Self-delegation"
**Category**: Edge Case

- **Given** agent `A` delegated to `A`
- **When** `A` calls `list_jobs`
- **Then** the roster contains exactly one `subagent` row for that session

---

#### Scenario: Truncation is bounded, counted and reproducible

**Traces to**: User Story 4, Acceptance Scenarios 1–2
**Category**: Edge Case

- **Given** the caller owns **30** `queued`, **30** `running` and **20** `blocked` jobs — i.e. 5 over each of FR-016's per-group sub-bounds of 25/25/25
- **When** the caller calls `list_jobs` twice with no arguments
- **Then** each response contains at most 25 rows per live group and at most 75 live rows in total
- **And** each response reports `notes.total_omitted = 10`, with `notes.omitted.by_status` naming `queued: 5` and `running: 5` and **no** `blocked` entry
- **And** the two responses contain the same rows in the same order

> **Rev 3 (R2-MIN-002).** This scenario and *Bounds and truncation* rows 2–6 were written against a
> single live limit `L`, which FR-016 replaced with three per-group sub-bounds. "`L` live jobs → all
> returned" is **false** if all `L` are `queued`, so the fixture could not be built from the
> requirements.

---

#### Scenario: A large queued and running population cannot evict a blocked row

**Traces to**: User Story 4, Acceptance Scenario 4; operator ruling 1
**Category**: Edge Case

- **Given** the per-group live sub-bounds are `queued=25`, `running=25`, `blocked=25` (75 live maximum)
- **And** the caller owns **400** `queued` jobs and **400** `running` jobs
- **And** the caller owns exactly **3** `blocked` jobs
- **When** the caller calls `list_jobs` with no arguments
- **Then** all **3** `blocked` rows are present
- **And** exactly 25 `queued` rows and 25 `running` rows are present
- **And** `notes.omitted.by_status` reports `375` for `queued` and `375` for `running`, and carries **no** `blocked` entry (zero is omitted — FR-033)
- **And** `notes.omitted.by_kind` is also present and sums to the same `notes.total_omitted` (FR-017 emits both key spaces)
- **And** this holds even though `blocked` sorts **last** of the three live groups — the reservation is a property of the bound, not of the sort position
- **And** when the caller instead owns **25** `queued`, **25** `running` and **3** `blocked` jobs and calls `list_jobs` with **`limit=30`**, all **3** `blocked` rows are *still* present — FR-016's round-robin allocation, not a tail-truncating total cap

> **Why this scenario exists.** Operator ruling 1 keeps ADR-056 D3's sort order
> (`queued → running → blocked → …`) rather than rev 1's `blocked`-first inversion. That makes
> FR-016's per-group sub-bounds **load-bearing on their own**: they are now the *only* thing
> preventing the highest-signal rows from truncating away under exactly the load the tool is
> called under. Rev 1 described the two mechanisms as *"independently sufficient"*; with the
> reorder withdrawn, only one of them is left, so it is asserted directly rather than inferred.
>
> **Why rev 3 added the `limit=30` clause.** The scenario as rev 2 wrote it exercised the **default
> call only** — the input under which the sub-bounds hold — so it passed while rev 2's own FR-016
> ("`limit` is a TOTAL cap applied *after* the sub-bounds") deleted every `blocked` row on any
> caller-supplied `limit` below 75. That is this spec's instance of the through-line defect: a
> control whose test asserts the mechanism it was built from rather than the property it exists to
> guarantee. Operator ruling 3 deflates the *severity* — an `attention="none"` row really is just
> information — but not the defect: a `blocked` row can carry `attention="caller"`, and `limit=30`
> was dropping those too.

---

#### Scenario: A total order survives rows that all tie on an empty started_at

**Traces to**: User Story 4, Acceptance Scenario 2; Edge Case "All rows tie on an empty `started_at`"
**Category**: Edge Case

- **Given** 40 `queued` jobs across all three kinds, **every one** of which has an empty `started_at` (approved/draft plans and inbox/next tasks have never started)
- **When** the caller calls `list_jobs` 50 times against the unchanged stores
- **Then** all 50 responses return the same rows in the same order
- **And** the order is explained entirely by the FR-020 comparator — `(status group, started_at ASC within live groups / DESC within terminal groups, kind ASC, id ASC)` — not by `sort.Slice`'s unspecified behaviour on equal elements
- **And** in a mixed fixture where `started_at` is populated, the **oldest** live job sorts first within its group and the **most recently finished** terminal job sorts first within its group (FR-007)

---

#### Scenario: Label is redacted before it is truncated

**Traces to**: User Story 4, Acceptance Scenario 3; Edge Case "credential-shaped label"
**Category**: Error Path

- **Given** a task whose title embeds a registered sensitive value
- **And** the title is longer than the label limit
- **When** the caller calls `list_jobs`
- **Then** the returned label contains no fragment of the sensitive value
- **And** the returned label length does not exceed the label limit

---

#### Scenario: Unicode label truncates on a rune boundary

**Traces to**: User Story 4, Acceptance Scenario 3; Edge Case "Unicode label at exactly the limit"
**Category**: Edge Case

- **Given** a plan title of multi-byte runes exceeding the label limit
- **When** the caller calls `list_jobs`
- **Then** the returned label is valid UTF-8
- **And** no rune is split

---

#### Scenario: Terminal rows are excluded by default

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Happy Path

- **Given** the caller owns one `running` and three `completed` jobs
- **When** the caller calls `list_jobs` with no arguments
- **Then** only the `running` row is returned
- **And** when the caller calls `list_jobs` with `include_terminal=true`, all four are returned

---

#### Scenario: One corrupt lifecycle record does not erase the kind

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Error Path

- **Given** the caller owns 5 delegated sessions
- **And** one of the 5 lifecycle JSONL files is corrupt
- **When** the caller calls `list_jobs`
- **Then** 4 `subagent` rows are returned
- **And** the response reports `unreadable = 1` for the `subagent` kind

---

#### Scenario: One corrupt plan file does not erase the plan kind

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Error Path

- **Given** the caller owns 5 plans
- **And** one of the 5 plan `*.json` files is corrupt
- **When** the caller calls `list_jobs`
- **Then** 4 `plan` rows are returned
- **And** the response reports `unreadable = 1` for the `plan` kind
- **And** the count is **not** silently discarded as today's `slog.Warn(…); continue` does

---

#### Scenario: One corrupt task file does not erase the task kind

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Error Path

- **Given** the caller owns 5 standalone tasks
- **And** one of the 5 task `*.json` files is corrupt
- **When** the caller calls `list_jobs`
- **Then** 4 `task` rows are returned
- **And** the response reports `unreadable = 1` for the `task` kind

---

#### Scenario: A failed store yields a per-kind error, not a short list

**Traces to**: User Story 5, Acceptance Scenario 2
**Category**: Error Path

- **Given** the plan store directory cannot be read
- **And** the task and lifecycle stores are healthy
- **When** the caller calls `list_jobs`
- **Then** the response carries an error entry naming `plan`
- **And** the response still contains the caller's `task` and `subagent` rows

---

#### Scenario: All three stores failing yields three error entries

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Error Path

- **Given** all three stores fail to read
- **When** the caller calls `list_jobs`
- **Then** the response carries exactly three per-kind error entries
- **And** the response is not reported as an empty success

---

#### Scenario: A post-restart subagent row is an honest tombstone

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Error Path

- **Given** agent `A` spawned an async delegation
- **And** the process restarted and the boot sweep reconciled the session
- **When** `A` calls `list_jobs` with `include_terminal=true`
- **Then** the row's `status` is `failed`
- **And** the row's `native_status` names the reconciliation reason
- **And** the row's `actionable` is `false`

---

#### Scenario: A post-restart DEFAULT call never looks like "no work at all"

**Traces to**: User Story 4, Acceptance Scenario 5; User Story 6, Acceptance Scenario 1
**Category**: Error Path

- **Given** agent `A` spawned three async delegations
- **And** the process restarted and the ADR-053 boot sweep reconciled all three to `failed(interrupted)`
- **When** `A` calls `list_jobs` with **default arguments** — no `include_terminal`, no `kind`, no `status`
- **Then** zero rows are returned
- **And** the response carries `terminal_suppressed = 3` for the `subagent` kind and `3` in total
- **And** the response is therefore **distinguishable** from the response `A` receives when it genuinely has no background work at all, which carries **no** `terminal_suppressed` field
- **And** the caller can recover the three rows by re-calling with `include_terminal=true`

---

#### Scenario: `native_status` is redacted and bounded exactly like `label`

**Traces to**: User Story 4, Acceptance Scenario 3; Edge Case "A `native_status` carrying a registered secret"
**Category**: Error Path

- **Given** a subagent whose `LifecycleRecord.FailedReason` is a wrapped runtime error embedding a registered sensitive value, longer than the `native_status` maximum
- **And** a plan whose `PausedReason` embeds the same value
- **When** the caller calls `list_jobs` with `include_terminal=true`
- **Then** neither row's `native_status` contains any fragment of the sensitive value of length ≥ 4
- **And** neither row's `native_status` exceeds its stated maximum
- **And** the redaction is applied **before** truncation, so truncation cannot split the secret across the boundary and defeat the replacer
- **And** the same guarantee holds for every free-text field named in FR-019, not only `label`

---

#### Scenario: The tool description states its own limits

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the registered `list_jobs` tool
- **When** its `Description()` string is read
- **Then** it states that a row with `actionable=false` is informational only and its id will not be accepted by that kind's action tools
- **And** it states that the roster is a best-effort near-snapshot, not a transactional one
- **And** it states that an `id` is meaningful only paired with its `kind`
- **And** it states that an operator can disable the tool globally via `sandbox.tool_policies`

---

#### Scenario: The delegate session index is read once per call, not once per row

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Edge Case

- **Given** the caller owns 60 subagent rows
- **And** `DelegateTool.mu` is instrumented with an acquisition counter
- **When** the caller calls `list_jobs` once
- **Then** `actionable` is resolved correctly for all 60 rows
- **And** `DelegateTool.mu` was acquired **exactly once** by the `list_jobs` call path

---

#### Scenario: Every call leaves an audit trail

**Traces to**: User Story 8, Acceptance Scenarios 1–2
**Category**: Happy Path

- **Given** a populated store containing one corrupt lifecycle record
- **And** the process is configured with log level **Info** — i.e. Debug is **off**, as in a normal production configuration
- **When** the caller calls `list_jobs` once
- **Then** exactly one `pkg/audit` `Entry` is written with `event="tool_call"`, `tool="list_jobs"`, `decision="allow"`, `agent_id` = the principal, and `details` carrying `workspace_id`, `workspace_scoped`, the kinds read and the row count — **written regardless of log level**, because it is the security record, not a log line
- **And** exactly one additional entry is emitted at **Warn** naming the `subagent` kind and its non-zero `unreadable` count
- **And** no audit entry and no log entry contains a `label` or a `native_status` at all — redacted or otherwise
- **And** when the same call is made with a `nil` audit logger, it succeeds and writes nothing (best-effort, never an error)

> **Rev 3 (R2-MAJ-011).** Rev 2 required *"exactly one structured audit/log entry at **Debug**"* on
> a P0 security boundary whose stated motivation is that *"a cross-agent scoping bug would have left
> no forensic trail"*. **Debug is off in normal production configurations**, so the forensic record
> did not exist when it was needed — an entry that is compiled in but not emitted leaves the same
> absence. Rev 2 also wrote *"audit/log"* as if they were one thing, listing `pkg/audit` (a
> persisted, tamper-evident subsystem with its own decision/severity model) in the Symbols table
> while describing an slog level in the requirement; only one of the two satisfies US-8 and rev 2
> never picked. FR-032(a) now splits them, and this scenario asserts the split **with Debug
> disabled**, which is the condition under which rev 2's version silently produced nothing.

---

#### Scenario: An operator can switch the tool off globally

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** an installation whose global `sandbox.tool_policies` contains `"list_jobs": "deny"`
- **And** every non-system agent's seeded map contains `"list_jobs": "allow"`
- **When** the effective policy is resolved for any of those agents
- **Then** it is `deny` — deny-wins over the per-agent `allow`
- **And** the tool is absent from that agent's advertised tool set

---

#### Scenario: Diagnostic counters are absent when nominal

**Traces to**: User Story 1, Acceptance Scenario 4; User Story 4, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a caller with exactly two live jobs, no omissions, no unreadable records, no terminal rows and no store errors
- **When** the caller calls `list_jobs`
- **Then** the response contains the two rows
- **And** the response contains **no** `total_omitted`, `omitted`, `unreadable`, `terminal_suppressed`, `limit_clamped_to`, `scan_truncated`, `unmapped`, `ignored_args` or per-kind error field
- **And** the response contains **`"notes": null`** — the named always-present field (FR-033), so the caller distinguishes "nothing to report" from a malformed response
- **And** `cap_active`, `cap_max`, `cap_observed_at` and `engine_running` are **still present at the top level** with a healthy engine, because they are state and not diagnostics — the omit-when-zero rule does not reach them
- **And** every row still carries `attention`, `"none"` on all of them

---

#### Scenario: Delegate mint fails closed on an unresolvable parent

**Traces to**: User Story 3, Acceptance Scenario 1; Edge Case "Empty parent agent id at mint time"
**Category**: Error Path

- **Given** a `delegate` call whose context carries no agent id (or a whitespace-only one)
- **When** the lifecycle record would be minted
- **Then** the mint returns an error
- **And** **no** lifecycle record file is written
- **And** the delegation does not proceed

---

#### Scenario: A parent is never inferred from a shared or child-owned field

**Traces to**: User Story 3, Acceptance Scenario 3; Explicit Non-Behaviors
**Category**: Error Path

- **Given** agent `A` delegated to `B`, and `B` delegated to `C`, so the `A→B` and `B→C` records share a `parent_durable_key`
- **And** a lifecycle record exists whose `agent_id` is `A` but whose `parent_agent_id` is `X`
- **When** `A` calls `list_jobs`
- **Then** the roster contains the `A→B` session only
- **And** the roster does **not** contain the `B→C` session, which shares `parent_durable_key`
- **And** the roster does **not** contain the record whose `agent_id` is `A` — `agent_id` is the **child's** id and is never a parent predicate
- **And** `parent_agent_id` is the only field consulted

---

#### Scenario: `parent_agent_id` is always present on disk, never an absent key

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Edge Case

- **Given** a lifecycle record minted by a successful `delegate` call
- **When** the persisted JSONL line is read back as raw JSON
- **Then** the `parent_agent_id` key is **present**
- **And** its value is non-empty
- **And** the field is declared **without** `omitempty`, so an empty value could never serialize to an absent key indistinguishable from a missing one

---

#### Scenario: `list_jobs` resolves to allow on an upgraded installation

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a `config.json` written before `list_jobs` existed, containing no `list_jobs` key
- **When** the config is loaded and tool-policy coverage is repaired and validated
- **Then** `ValidateToolPolicyCoverage` reports no gap for `list_jobs`
- **And** `RepairIncompleteToolPolicyCoverage` backfills no `list_jobs` entry
- **And** the effective policy for a non-system agent is `allow`

---

#### Scenario Outline: Seeded policy per agent class on a fresh install

**Traces to**: User Story 7, Acceptance Scenarios 1, 3, 4, 6
**Category**: Happy Path

- **Given** a fresh install seeded by `coreagent.SeedConfig`
- **When** the effective `list_jobs` policy is resolved for `<agent>`
- **Then** it is `<policy>`

**Examples**:

| agent | policy | why |
|-------|--------|-----|
| Mia | `allow` | base agent, needs the override (C6) |
| Jim | `allow` | base agent, needs the override (C6) |
| Ava | `allow` | base agent, needs the override (C6) |
| Ray | `allow` | base agent, needs the override (C6) |
| Worker | `allow` | `tightenGlobalCeiling` emits a **sparse** map; absent key ⇒ `case a == "": return g` ⇒ the global `allow` |
| a seeded specialist | `allow` | `denyAllThenOverride`, needs the override |
| a new custom agent | `allow` | `NewCustomAgentToolsCfg`, needs the override |
| any System Agent | `deny` | `systemAgentSeed`, boot-re-enforced, D8 |
| any System Agent, **god-mode on** | `allow` | `if cfg.GodMode { return ToolPolicyAllow }` short-circuits before either map is read `[VERIFIED: compositor.go:175-177]` |
| any agent, **god-mode on**, both maps absent | `allow` | same short-circuit — the both-empty `deny` branch is never reached |

---

#### Scenario: Naming the tool in a seed does not panic the process

**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Error Path

- **Given** a seed override map naming `list_jobs`
- **When** `validateOverrideKeys` runs
- **Then** it does not panic
- **And** `AllStaticToolNames()` contains `list_jobs`

---

#### Scenario Outline: Argument validation

**Traces to**: User Story 4, Acceptance Scenario 1; Behavioral Contract, error flows
**Category**: Error Path

- **Given** a caller with a resolvable id
- **When** the caller calls `list_jobs` with `<arg>` = `<value>`
- **Then** the result is `<outcome>`

**Examples**:

| arg | value | outcome |
|-----|-------|---------|
| `kind` | `"plan"` | success, only plan rows |
| `kind` | `"shell"` | validation error (deferred kind) |
| `kind` | `"Plan"` | validation error (case-sensitive) |
| `status` | `"blocked"` | success, only blocked rows |
| `status` | `"stalled"` | validation error (native, not normalized) |
| `limit` | `0` | validation error |
| `limit` | `-1` | validation error |
| `limit` | `201` (above the hard max of 200) | **clamped to 200**, `limit_clamped_to=200` reported — **not** an error |
| `limit` | `150` (above the 75-row live maximum, below the hard max) | success, no clamp reported, at most 75 live rows returned |
| `limit` | `"20"` (a string) | validation error (not an integer) |
| `include_terminal` | `"yes"` | validation error (not a bool) |
| `include_drafts` | `true` | success, `draft` plans included and ranked last within `queued` |
| `relation` | any value | **success, argument IGNORED**, `notes.ignored_args = ["relation"]` — **rev 3 (R2-MAJ-017)**: an unknown argument no longer costs the caller a turn, which is the same rationale FR-002 already gave for clamping `limit`. `relation` is the concrete case because it is a **response** field an agent has just read off a row |
| `status` | `"failed"` (default `include_terminal`) | **rev 3 (R2-MAJ-016): success, `include_terminal` implied `true`, failed rows returned.** Rev 2 returned an empty roster here — for the single most natural query after "what am I still working on?" — and the agent concluded nothing had failed |
| `status` | `"completed"` (default `include_terminal`) | same — implied `true` |
| `label_contains` | `"migration"` | **rev 3 (R2-MAJ-007)**: success, only rows whose post-redaction `label` contains the substring case-insensitively; counters are computed over the **filtered** population and stay exact |
| `label_contains` | a 65-rune string | validation error (known argument, invalid value — the ≤ 64-rune bound) |
| `label_contains` | `""` | success, treated as absent (no filter) |

---

#### Scenario: The tool never mutates state

**Traces to**: User Story 5, Acceptance Scenario 1; Explicit Non-Behaviors
**Category**: Edge Case

- **Given** a snapshot of the plan, task and lifecycle store directories
- **When** `list_jobs` is called 50 times with varied arguments
- **Then** the directory contents are byte-identical to the snapshot

---

#### Scenario: Concurrent calls during active dispatch never error

**Traces to**: User Story 5, Acceptance Scenario 1; Edge Case "Concurrent mutation during the read"
**Category**: Edge Case

- **Given** plan dispatch and delegation state transitions are actively running
- **When** 8 goroutines call `list_jobs` concurrently for 3 seconds
- **Then** no call returns an unexpected error
- **And** no call panics
- **And** the race detector reports nothing
- **And** every response is **well-formed** — not merely non-erroring: each row satisfies the full FR-003 field contract, and the counters are arithmetically self-consistent even for calls that straddled a mutation

---

#### Scenario: The serialized response stays within its stated size bound

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Edge Case

- **Given** the caller owns 500 live jobs, every one with a 10 000-rune label and a 4 000-rune `native_status`
- **When** the caller calls `list_jobs` with no arguments
- **Then** the serialized response length is measured directly
- **And** it does not exceed `maxRows × (labelMax + nativeStatusMax + fixedRowOverhead) + envelopeOverhead`
- **And** that arithmetic identity, not a hoped-for figure, is what the assertion uses

---

#### Scenario: Back-to-back calls never reuse a differently-scoped answer

**Traces to**: User Story 3, Acceptance Scenarios 4–5; User Story 4, Acceptance Scenario 2
**Category**: Error Path

- **Given** agent `A` owns a plan and a task in workspace `W1` and a plan in `W2`
- **When** `A` calls `list_jobs` from a **workspace-less** context, and then immediately calls it again from a context bound to `W1`
- **Then** the first response carries `workspace_scoped=false` and rows from both workspaces
- **And** the second response carries `workspace_scoped=true` and **only** `W1` rows — no `W2` title, no `W2` handle
- **And** when `A` then immediately calls `list_jobs` with `kind="plan"`, the response contains **only** `plan` rows
- **And** every one of the three calls emits its own `pkg/audit` entry (FR-032(a))
- **And** no response is served from a cache: each of the three reads the stores

> **Rev 3 (R2-CRIT-001) — this scenario replaces *"Repeated calls under a per-principal memo stay
> honest"*, and the replacement is deliberate rather than a deletion.** The old scenario asserted
> that two **identical** calls within the TTL return identical answers and that the second performs
> zero scans — the one call pattern under which a principal-keyed memo is harmless, and therefore an
> assertion that would pass with the control completely broken. The property that matters is that a
> call reflects **its own** scope and **its own** arguments; asserting it directly makes the memo
> unimplementable and keeps the hole guarded, whereas deleting the scenario would have left nothing
> to stop the next author reintroducing the cache.

---

#### Scenario: An informational block is distinguishable from one the caller must act on

**Traces to**: User Story 2, Acceptance Scenario 8; operator ruling 3; cross-spec M6
**Category**: Happy Path

- **Given** the caller owns a standalone task with `status=blocked` (an unmet dependency)
- **And** a plan with `plan_phase=awaiting_supervision`
- **And** a plan with `plan_phase=stalled`
- **And** a delegated subagent in `state=needs_input`
- **When** the caller calls `list_jobs`
- **Then** all four rows carry `status="blocked"`
- **And** the task row carries `attention="none"` — nothing for the caller to do
- **And** the `awaiting_supervision` plan carries `attention="elsewhere"` — another agent is adjudicating
- **And** the `stalled` plan and the `needs_input` subagent both carry `attention="caller"`
- **And** the four rows do **not** all carry the same `attention` value — a constant would satisfy a per-row equality check while carrying no information at all

---

#### Scenario: A bounded scan is reported, never presented as a complete one

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Error Path

- **Given** `tools.list_jobs.max_records_scanned_per_kind` is `5000`
- **And** the task store holds `10 000` records
- **When** the caller calls `list_jobs`
- **Then** the response carries `notes.scan_truncated = {"task": {"scanned": 5000, "present": 10000}}`
- **And** `present` is derived from the directory entry count, having loaded no additional record
- **And** exactly one Warn entry names the `task` kind
- **And** the `task` kind's `omitted`, `unreadable` and `terminal_suppressed` are documented as **lower bounds** for this response, while the `plan` and `subagent` kinds — which did not hit the ceiling — remain exact
- **And** when the ceiling is raised above `10 000` and the call is repeated, `scan_truncated` is **absent** and every count is exact again

---

#### Scenario: An unmappable native state is marked, never guessed

**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Error Path

- **Given** a plan whose persisted `state` is `"wat"` — a value no current build writes
- **And** a lifecycle record whose persisted `state` is the empty string
- **When** the caller calls `list_jobs` with `include_terminal=true`
- **Then** neither call panics and neither row is dropped
- **And** each row carries `status="blocked"`, `attention="none"` and `native_status="unknown:<raw>"`, redacted and bounded per FR-019/FR-030
- **And** `notes.unmapped` counts `1` for each affected kind
- **And** neither row is reported as `failed` — a silent coercion would make unparseable data indistinguishable from a real failure

---

#### Scenario: A terminal plan or task row is not actionable

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the caller owns a `done` plan, a `failed` plan, a `done` task and a `failed` task
- **When** the caller calls `list_jobs` with `include_terminal=true`
- **Then** all four rows carry `actionable=false`
- **And** a `running` plan and an `in_progress` task in the same roster carry `actionable=true`

---

#### Scenario: A label filter reaches a row the group bound would have hidden

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** the caller owns `400` `queued` plans, exactly one of which is titled `"Migrate the audit chain to HMAC"`
- **When** the caller calls `list_jobs` with `label_contains="audit chain"`
- **Then** that plan is returned, even though it is not within the first 25 of its group
- **And** `notes.total_omitted` is computed over the **filtered** population, so it is `0` (and therefore absent) rather than `375`
- **And** the match is case-insensitive and is performed against the **post-redaction** label, so a filter can never be used to confirm the presence of a redacted secret

---

#### Scenario: A task created by a human whose username collides with an agent id is never attributed to that agent

**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Error Path

- **Given** a human user whose gateway username is the literal string `"mia"`
- **And** that user created a standalone task through the REST path, so the record carries `created_by="mia"` and `created_by_agent_id=""`
- **And** agent `mia` created a different standalone task through the tool path, so that record carries `created_by_agent_id="mia"`
- **When** agent `mia` calls `list_jobs`
- **Then** the roster contains the agent-created task with `relation="dispatched"`
- **And** the roster does **not** contain the human's task, and does not contain its title
- **And** the predicate consulted is `created_by_agent_id`, never `created_by`
- **And** an empty `created_by_agent_id` never matches, for any caller

---

#### Scenario: The operator runbook explains a degrading install

**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the shipped operator documentation for `list_jobs`
- **When** it is read
- **Then** it states what a non-zero `unreadable` means and what an operator should do about it
- **And** it states what `scan_truncated` means and how to raise `tools.list_jobs.max_records_scanned_per_kind`
- **And** it names the global `sandbox.tool_policies` `"list_jobs": "deny"` kill switch **and** states that it has no effect under god-mode
- **And** it states that an `ask` verdict makes every call block, which for an autonomous background turn is a hang rather than a prompt
- **And** it names the `tools.delegate.require_parent_agent_id` rollback for FR-015's mint guard
- **And** none of this text appears in `Description()` (FR-012b)

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|-------|-------|---------|
| Unit | Status normalization, sorting, truncation, redaction, argument validation, per-store filter clauses | Validates pure logic with no I/O |
| Integration | The tool against real temp-dir plan/task/lifecycle stores; config seeding + policy resolution | Validates the cross-store composition and the policy grant |
| E2E | Async delegation → restart → `list_jobs`; concurrency under live dispatch | Validates the durability and contention claims end to end |

### Test Implementation Order

Write these **before** the implementation. Unit first, then integration, then E2E; within a level,
ordered by dependency.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|-------|-----------|-------|------------------------|-------------|
| 1 | `TestNormalizeStatus_Plan` | Unit | Scenario Outline: Normalized status is derived correctly per kind | All 12 plan rows of the table |
| 2 | `TestNormalizeStatus_Task` | Unit | same | All 6 task statuses |
| 3 | `TestNormalizeStatus_Subagent` | Unit | same | All 8 lifecycle states |
| 4 | `TestNormalizeStatus_StalledIsBlockedNotRunning` | Unit | Scenario: A stalled plan is never reported as merely running | The CRIT-001 regression guard |
| 5 | `TestNativeStatus_AwaitingSupervisionOutranksStalled` | Unit | Scenario: awaiting_supervision outranks stalled | **Rev 3 (cross-spec C1), renamed from `TestNativeStatus_AwaitingCorrectionOutranksStalled`.** Phase precedence + `attention=elsewhere` |
| 5a | `TestNativeStatus_ComposedFromConstants` | Unit | Scenario: awaiting_supervision outranks stalled | **Rev 3 (cross-spec C1, FR-006a).** Every `native_status` substring sourced from a validated enum is compared against the **exported constant** (`plan.PhaseAwaitingSupervision`, `plan.FailedReason*`, `session.Lifecycle*`), never a literal — so a future rename fails the test instead of shipping a wrong string no compiler sees |
| 5b | `TestNormalizeStatus_UnmappedNativeState` | Unit | Scenario: An unmappable native state is marked, never guessed | **Rev 3 (R2-MAJ-012, FR-006b).** `state="wat"` and `state=""` → `blocked` + `attention="none"` + `native_status="unknown:<raw>"` + `notes.unmapped`; no panic, no drop, no coercion to `failed` |
| 5c | `TestAttention_DerivedPerKind` | Unit | Scenario: An informational block is distinguishable from one the caller must act on | **Rev 3 (FR-036, operator ruling 3 + cross-spec M6).** All six FR-036 rows table-driven, **plus** an assertion that the four `blocked` fixtures do not all share one value — a constant would pass a naive per-row check while carrying no information |
| 6 | `TestSortOrder_GroupsThenPerGroupDirection` | Unit | Scenario: Truncation is bounded, counted and reproducible | **Rev 3 (R2-MAJ-008), renamed from `TestSortOrder_GroupsThenStartedAtDesc`.** `queued→running→blocked→failed→completed` (ADR D3's order, operator ruling 1); `started_at` **ASC within live groups** (oldest first — the work US-1 is about) and **DESC within terminal groups** (most recently finished first) |
| 7 | `TestSortOrder_TotalOnEmptyStartedAt` | Unit | Scenario: A total order survives rows that all tie on an empty started_at | **Rev 2, replaces `TestSortOrder_Deterministic`.** 40 rows all with `started_at=""`, shuffled 50×, identical output — proves the `(kind, id)` final tiebreak, not `sort.Slice` luck |
| 7a | `TestSortOrder_DraftsRankLastWithinQueued` | Unit | Scenario: Draft plans are excluded by default and truncate first when included | `approved` before `draft` inside the `queued` group |
| 8 | `TestTruncateLabel_RuneBoundary` | Unit | Scenario: Unicode label truncates on a rune boundary | Valid UTF-8 at the limit |
| 9 | `TestLabel_RedactBeforeTruncate` | Unit | Scenario: Label is redacted before it is truncated | Ordering guard |
| 9a | `TestLabel_ShortSecretBelowFilterMinLength` | Unit | Scenario: Label is redacted before it is truncated | `FilterSensitiveData` bypasses content < 8 chars (FR-019a) |
| 9b | `TestLabel_FilterDisabledStillBounded` | Unit | Scenario: Label is redacted before it is truncated | Disabled filtering must not relax the bound |
| 9c | `TestNativeStatus_RedactedAndBounded` | Unit | Scenario: `native_status` is redacted and bounded exactly like `label` | **Rev 2 (CRIT-002).** Secret-bearing `FailedReason`/`PausedReason`; redact-then-truncate, per-field max |
| 10 | `TestBounds_PerStatusSubBounds` | Unit | Scenario: A large queued and running population cannot evict a blocked row | **Rev 2:** 400 `queued` + 400 `running` + 3 `blocked` → all 3 `blocked` survive. **Rev 3 (R2-MAJ-001) adds the case that actually stresses it**: 25/25/3 with a caller-supplied `limit=30` → all 3 `blocked` still survive. The default-only version passed while rev 2's total cap deleted every `blocked` row |
| 10a | `TestBounds_LimitRoundRobinAllocation` | Unit | Scenario: A large queued and running population cannot evict a blocked row | **Rev 3 (FR-016).** Allocation is round-robin across live groups in sort order; **emission** is still in full FR-007 order. Table-driven over `limit ∈ {1, 3, 9, 30, 74, 75, 200}` against several group-size shapes, asserting no populated group is emptied while another exceeds its proportional share |
| 11 | `TestBounds_OmissionCountExact` | Unit | Scenario: Truncation is bounded, counted and reproducible | Exact per-kind + total counts |
| 11a | `TestBounds_LimitClampReported` | Unit | Scenario Outline: Argument validation | **Rev 2 (MAJ-008).** `limit=201` clamps and reports; `limit=150` succeeds without a clamp report |
| 11b | `TestDiagnostics_OmittedWhenNominal` | Unit | Scenario: Diagnostic counters are absent when nominal | **Rev 2 (MAJ-015).** No zero-valued counter fields on a clean response |
| 12 | `TestArgs_Validation` | Unit | Scenario Outline: Argument validation | Every row of the table — **rev 3 adds the rows for an ignored unknown argument (`notes.ignored_args`), terminal `status` implying `include_terminal`, and `label_contains`'s bounds** |
| 13 | `TestPlanFilter_OwnerAgentIDMatches` | Unit | Scenario: Owned plan appears with its plan id | New `plan.Filter` clause |
| 14 | `TestPlanFilter_EmptyOwnerAgentIDIsNotAWildcard` | Unit | Scenario: Unresolvable principal fails closed | Guards the C3 gap |
| 14a | `TestTaskFilter_PlanIDSetSelectsStandaloneOnly` | Unit | Scenario: Standalone task appears; plan-member task does not | **Rev 2 (MAJ-002).** New `task.Filter.PlanIDSet`, mirroring `ParentTaskIDSet` |
| 14b | `TestTaskFilter_UnsetPlanIDSetUnchangedBehaviour` | Unit | Scenario: Standalone task appears; plan-member task does not | **Rev 2 (MAJ-002), regression.** `PlanIDSet=false` preserves today's "`PlanID==\"\"` means filter off" for `list_tasks` and the gateway task endpoints |
| 15 | `TestLifecycleFilter_ParentAgentIDMatches` | Unit | Scenario: A parent is never inferred from a shared or child-owned field | New filter clause |
| 16 | `TestLifecycleRecord_ParentAgentIDRoundTrip` | Unit | Scenario: `parent_agent_id` is always present on disk, never an absent key | Persist → tail → equal; **and** the raw JSONL line carries the key (no `omitempty`) |
| 16a | `TestPlanStore_ListLenientCountsSkipped` | Unit | Scenario: One corrupt plan file does not erase the plan kind | **Rev 2 (MAJ-001).** New lenient sibling returns `(records, skipped, err)` |
| 16b | `TestTaskStore_ListLenientCountsSkipped` | Unit | Scenario: One corrupt task file does not erase the task kind | **Rev 2 (MAJ-001).** Same for `pkg/task` |
| 16c | `TestDelegateTool_ResolvableSessionIDsLocksOnce` | Unit | Scenario: The delegate session index is read once per call, not once per row | **Rev 2 (MAJ-004).** Counting mutex wrapper; 60 ids, 1 acquisition |
| 16d | `TestPlanEngine_CapSnapshotIsLockFreeAndGlobal` | Unit | Scenario: Cap pressure reports the GLOBAL count, not the caller's own rows | **Rev 2 (CRIT-001).** Snapshot includes foreign-owner plans + a registered `activeCounter`; asserts `pe.mu` is not taken by the reader |
| 16e | `TestPlanEngine_CapSnapshotIdenticalToAdmitLocked` | Unit | Scenario: Cap pressure reports the GLOBAL count, not the caller's own rows | **Rev 3 (R2-MAJ-009, SC-013).** Reads **both** the published snapshot and the value `admitLocked` computed in the same pass and asserts **identity**. Test 16d alone passes against a second, divergent re-derivation, because it asserts values under a controlled fixture; this one cannot |
| 16f | `TestPlanEngine_TickStampsLivenessBeforeEarlyReturn` | Unit | Scenario: Cap pressure distinguishes a real queue from a stopped engine | **Rev 3 (cross-spec M3, FR-029(b)).** Force `planStore.List` to error so `Tick` takes its early return `[VERIFIED: pkg/agent/plan_engine.go:679-682]`, then assert `lastTickAt` still advanced — the heartbeat must precede the return, or `engine_running` lies exactly when the store is failing |
| 17 | `TestListJobs_EmptyPrincipalFailsClosed` | Integration | Scenario: Unresolvable principal fails closed | Error + zero rows |
| 18 | `TestListJobs_WhitespacePrincipalFailsClosed` | Integration | Scenario Outline: Principal-shaped inputs that must all fail closed | All three rows |
| 19 | `TestListJobs_CrossAgentIsolation` | Integration | Scenario: Cross-agent isolation | A sees none of B |
| 20 | `TestListJobs_WorkspaceScoped` | Integration | Scenario: Workspace scoping and attribution | W1 only, `workspace_id` present, `workspace_scoped=true` |
| 20a | `TestListJobs_WorkspacelessTurnIsLabelled` | Integration | Scenario: A workspace-less turn is labelled, not silently widened | **Rev 2 (MAJ-009).** Context built **without** `WithWorkspaceID` — the production path `TestListJobs_WorkspaceScoped` never exercises. Cross-workspace for the caller, `workspace_scoped=false`, still zero rows from another agent |
| 21 | `TestListJobs_LiveSubagentActionable` | Integration | Scenario: Live subagent handle recovered in the same process | `actionable=true`, id resolves |
| 22 | `TestListJobs_PlanOwnerAgentNotOwnerString` | Integration | Scenario: Plan authored by a human but run by the agent still appears | The C4 case |
| 23 | `TestListJobs_StandaloneTasksOnly` | Integration | Scenario: Standalone task appears; plan-member task does not | `plan_id==""` predicate via `PlanIDSet` |
| 23a | `TestListJobs_TaskOwnershipUnion` | Integration | Scenario: A task assigned to the caller is visible, not only one the caller created | **Rev 2 (MAJ-005).** T1–T4 fixture; `relation` per row; T3 appears exactly once |
| 24 | `TestListJobs_EmptyRosterIsSuccess` | Integration | Scenario: Empty roster is a success, not an error | No error, no counters at all |
| 25 | `TestListJobs_TerminalExcludedByDefault` | Integration | Scenario: Terminal rows are excluded by default | Default + opt-in |
| 25a | `TestListJobs_PostRestartTerminalSuppressedCount` | Integration | Scenario: A post-restart DEFAULT call never looks like "no work at all" | **Rev 2 (CRIT-003).** 0 rows **and** `terminal_suppressed=3`; distinguishable from a genuinely empty roster |
| 25b | `TestListJobs_DraftsExcludedByDefault` | Integration | Scenario: Draft plans are excluded by default and truncate first when included | **Rev 2 (MAJ-010).** 26 drafts + 1 approved → the approved plan is present |
| 25c | `TestListJobs_IntentionallyStopped` | Integration | Scenario: A cancelled job is distinguishable from a crashed one | **Rev 2 (MAJ-011).** 4-row fixture, boolean derived from closed enums |
| 26 | `TestListJobs_SelfDelegationSingleRow` | Integration | Scenario: Self-delegation yields exactly one row | No duplicate |
| 27 | `TestListJobs_NestedDelegationNoGrandchildren` | Integration | Scenario: A parent is never inferred from a shared or child-owned field | Depth isolation + the shared-`ParentDurableKey` and child-`AgentID` negative cases |
| 28 | `TestDelegateMint_FailsClosedOnEmptyParentAgentID` | Integration | Scenario: Delegate mint fails closed on an unresolvable parent | **Rev 2 — replaces `TestListJobs_LegacyRecordsCountedNotGuessed`.** Empty and whitespace-only `ToolAgentID(ctx)`: error returned, **no** record file written |
| 28a | `TestDelegateMint_StampsParentAgentID` | Integration | Scenario: `parent_agent_id` is always present on disk, never an absent key | **Rev 3 (R2-MAJ-015): parameterised over EVERY mint site** — `delegate.run` sync, `delegate.run` async (`executeAsync`), and the `follow_up`/Play generation mint when that path lands (FR-034). Rev 2 named one test against one site while FR-034 requires the field on every generation mint |
| 28b | `TestDelegateMint_GuardDowngradeConfig` | Integration | Scenario: Delegate mint fails closed on an unresolvable parent | **Rev 3 (R2-MAJ-015).** `tools.delegate.require_parent_agent_id=false` → empty parent logs at Error and mints; default `true` asserted separately. The field rollback for a guard whose failure mode is "delegation stops entirely" |
| 29 | `TestListJobs_CorruptLifecycleRecordSkipped` | Integration | Scenario: One corrupt lifecycle record does not erase the kind | 4 of 5 + `unreadable=1` |
| 29a | `TestListJobs_CorruptPlanFileSkipped` | Integration | Scenario: One corrupt plan file does not erase the plan kind | **Rev 2 (MAJ-001).** 4 of 5 + `unreadable=1` for `plan` |
| 29b | `TestListJobs_CorruptTaskFileSkipped` | Integration | Scenario: One corrupt task file does not erase the task kind | **Rev 2 (MAJ-001).** 4 of 5 + `unreadable=1` for `task` |
| 30 | `TestListJobs_PerKindStoreError` | Integration | Scenario: A failed store yields a per-kind error, not a short list | Plan errors, others survive |
| 31 | `TestListJobs_AllStoresFail` | Integration | Scenario: All three stores failing yields three error entries | Three entries |
| 32 | `TestListJobs_CapPressureWithoutAdmit` | Integration | Scenario: Cap pressure distinguishes a real queue from a stopped engine | Assert `Admit` uncalled **and** assert the emitted values (`0`/`16`) — rev 1 asserted only the former, which passed while the numbers were wrong |
| 32a | `TestListJobs_CapPressureGlobalNotScoped` | Integration | Scenario: Cap pressure reports the GLOBAL count, not the caller's own rows | **Rev 2 (CRIT-001).** 14 foreign plans + a 2-count `activeCounter` + 1 own approved plan → `cap_active=16` |
| 32b | `TestListJobs_CapFieldsOmittedWhenUnreliable` | Integration | Scenario: An unreliable cap read omits the fields rather than reporting a wrong number | **Rev 2.** `reliable=false` → both fields absent |
| 33 | `TestListJobs_ReadOnly` | Integration | Scenario: The tool never mutates state | Byte-identical dirs |
| 33a | `TestListJobs_DescriptionContract` | Integration | Scenario: The tool description states its own limits | **Rev 2 (MAJ-012).** Asserts on `Description()` — the only test that can cover FR-012/FR-024/FR-035's description clauses. **Rev 3 (R2-MAJ-014) adds two assertions the presence-only version could never fail**: total length ≤ **900 characters** (FR-012b), and the **absence** of the operator-facing kill-switch / runbook / `ask` text, which belongs in the runbook |
| 33b | `TestListJobs_ResponseSizeBound` | Integration | Scenario: The serialized response stays within its stated size bound | **Rev 2 (MAJ-016).** Measures `len(result.Content)` against the FR-030 arithmetic identity |
| 33c | `TestListJobs_AuditEntryEmitted` | Integration | Scenario: Every call leaves an audit trail | **Rev 2 (MAJ-013).** One Debug entry + one Warn on non-zero `unreadable`; no unredacted text |
| 33d | `TestListJobs_NoCrossScopeReuse` | Integration | Scenario: Back-to-back calls never reuse a differently-scoped answer | **Rev 3 (R2-CRIT-001), replaces `TestListJobs_MemoTTL`.** Workspace-less call → immediate W1-scoped call returns only W1 rows and `workspace_scoped=true`; default call → immediate `kind="plan"` call returns only plan rows; three audit entries. **Fails if any roster memo is introduced.** The replaced test asserted same-args repetition — the one pattern under which a principal-keyed memo is harmless |
| 33e | `TestListJobs_ScanCeilingReported` | Integration | Scenario: A bounded scan is reported, never presented as a complete one | **Rev 3 (R2-CRIT-004).** 10 000 records against a 5 000 ceiling → `scan_truncated: {scanned, present}`, one Warn, `present` from the directory count; repeat with the ceiling raised → marker absent and counts exact. Also asserts the config key is read, not a constant |
| 33f | `TestListJobs_CapPressureStoppedEngineStale` | Integration | Scenario: Cap pressure distinguishes a real queue from a stopped engine | **Rev 3 (R2-CRIT-002 + R2-CRIT-003).** Engine stopped, snapshot older than 90 s → `cap_active=0` and `cap_max=16` **present**, `cap_observed_at` present and stale, `engine_running=false`. Fails if either the omit-when-zero rule or the suppress-on-stale rule returns |
| 33g | `TestListJobs_TerminalRowsNotActionable` | Integration | Scenario: A terminal plan or task row is not actionable | **Rev 3 (R2-MAJ-006).** `done`/`failed` plans and tasks → `actionable=false`; live rows in the same roster → `true` |
| 33h | `TestListJobs_LabelContainsReachesPastBound` | Integration | Scenario: A label filter reaches a row the group bound would have hidden | **Rev 3 (R2-MAJ-007).** 400 queued plans, one matching title at position 312 → returned; counters computed over the filtered population; match is post-redaction and case-insensitive |
| 33i | `TestListJobs_TaskCreatedByAgentIDNamespace` | Integration | Scenario: A task created by a human whose username collides with an agent id is never attributed to that agent | **Rev 3 (R2-CRIT-005).** REST-path task with `created_by="mia"`, `created_by_agent_id=""` → absent from agent `mia`'s roster; tool-path task with `created_by_agent_id="mia"` → present. Empty never matches |
| 33j | `TestOperatorDocs_RunbookContract` | Integration | Scenario: The operator runbook explains a degrading install | **Rev 3 (R2-MAJ-010).** Asserts the shipped operator documentation covers `unreadable`, `scan_truncated`, the kill switch + its god-mode exception, the `ask` verdict, and the FR-015 mint-guard rollback — **and** that none of it appears in `Description()` (FR-012b) |
| 34 | `TestToolPolicy_FreshInstallPerAgentClass` | Integration | Scenario Outline: Seeded policy per agent class on a fresh install | Every row of the table, **including the two god-mode rows** |
| 34a | `TestToolPolicy_GlobalDenyKillSwitch` | Integration | Scenario: An operator can switch the tool off globally | **Rev 2 (MAJ-013).** Global `deny` beats the per-agent seeded `allow` |
| 35 | `TestToolPolicy_UpgradedConfigResolvesAllow` | Integration | Scenario: `list_jobs` resolves to allow on an upgraded installation | The C7 path |
| 36 | `TestToolPolicy_NoDenyBackfillForListJobs` | Integration | same | `RepairIncompleteToolPolicyCoverage` returns no `list_jobs` gap |
| 37 | `TestStaticCatalog_ContainsListJobs` | Integration | Scenario: Naming the tool in a seed does not panic the process | `allStaticToolNames` + no panic |
| 38 | `TestListJobs_PostRestartTombstone` | E2E | Scenario: A post-restart subagent row is an honest tombstone | Spawn → restart → assert |
| 39 | `TestListJobs_ConcurrentDuringDispatch` | E2E | Scenario: Concurrent calls during active dispatch never error | 8 goroutines, `-race`; **rev 2 adds a well-formedness assertion per response** (full FR-003 field contract + self-consistent counters), not merely "no error / no panic" |
| 39a | `TestListJobs_GenerationIsNewestOnly` | E2E | Edge Case "A subagent session resumed to a new generation" | **Rev 2 (MIN-003).** `follow_up` mints gen-N+1 carrying `ParentAgentID` forward; exactly one row, `generation` exposed when > 0. **Rev 3 (R2-MAJ-018): MUST run both WITH and WITHOUT `include_terminal`** — rev 2 asserted "exactly one row" without stating the arguments, and under `include_terminal=true` (the argument the CRIT-003 recovery path tells agents to use) the superseded terminal generations would legitimately have appeared. Also asserts superseded generations are absent from `terminal_suppressed`, `omitted` and `unreadable` |

### Test Datasets

#### Dataset: Calling principal (US-3, the scoping control)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `"mia"` | happy path | roster scoped to `mia` | Scenario: Cross-agent isolation | Normal case |
| 2 | `""` | empty | **error**, 0 rows | Scenario: Unresolvable principal fails closed | Must not be "filter off" |
| 3 | `"   "` | whitespace | **error**, 0 rows | Scenario Outline: Principal-shaped inputs | Trim before the check |
| 4 | `"\t\n"` | whitespace, non-space | **error**, 0 rows | Scenario Outline: Principal-shaped inputs | |
| 5 | `"nonexistent-agent"` | valid shape, no data | empty roster, success | Scenario: Empty roster is a success | Unknown ≠ unresolvable |
| 6 | `"daniel"` (a username, not an agent) | namespace collision | no plan rows for agent-owned plans | Scenario: Plan authored by a human but run by the agent | Why `OwnerAgentID`, not `Owner` (C4) |
| 7 | 256-char agent id | max length | scoped normally | Scenario: Cross-agent isolation | No truncation of the predicate |
| 8 | valid principal, `ToolWorkspaceID(ctx) == ""` | **workspace fails open** | caller's rows across **all** workspaces, `workspace_scoped=false` | Scenario: A workspace-less turn is labelled, not silently widened | The production path `loop.go:6381-6383` produces; never another principal's rows |
| 9 | valid principal, workspace `"W1"` | happy path | W1 rows only, `workspace_scoped=true` | Scenario: Workspace scoping and attribution | |
| 10 | `""` principal at **mint** time (not read time) | empty, write side | **mint errors, no record written** | Scenario: Delegate mint fails closed on an unresolvable parent | FR-015 — the write-side twin of row 2 |
| 11 | caller = agent `"mia"`; a standalone **task** created via REST by a human whose username is `"mia"` (`created_by="mia"`, `created_by_agent_id=""`) | **namespace collision, TASK kind** | the task **MUST NOT appear**, and its title MUST NOT appear | Scenario: A task created by a human whose username collides with an agent id is never attributed to that agent | ⚠️ **Rev 3 (R2-CRIT-005).** The mirror of row 6, which tested this collision **for plans only** — the one kind the spec had already fixed. Verified mixed-namespace: `rest_tasks.go:847` writes `c.Username`, `tools/task.go:531` writes `callerID`. FR-037 |
| 12 | caller = agent `"mia"`; a standalone task created via the tool path (`created_by_agent_id="mia"`) | happy path, agent-id namespace | returned, `relation="dispatched"` | Scenario: A task created by a human whose username collides with an agent id is never attributed to that agent | The positive half — without it row 11 could be satisfied by returning nothing at all |
| 13 | any caller; a task with `created_by_agent_id=""` (pre-existing or REST-created) | **empty is never a wildcard** | never matched by the `dispatched` predicate, for any caller | Scenario: A task created by a human whose username collides with an agent id is never attributed to that agent | The same fail-closed rule FR-008 applies to the principal, applied to the predicate field |

#### Dataset: Plan native state → normalized status (US-2)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `state=draft` | boundary (pre-run) | `queued` | Scenario Outline: Normalized status | Not yet runnable |
| 2 | `state=approved` | happy path | `queued` | Scenario Outline: Normalized status | "ready to run (or cap-waiting)" |
| 3 | `state=running, phase=""` | empty sub-state | `running`, native `running/idle` | Scenario Outline: Normalized status | Empty phase ⇒ effective `idle` |
| 4 | `state=running, phase=stalled` | **the CRIT-001 case** | `blocked` | Scenario: A stalled plan is never reported as merely running | Must not be `running` |
| 5 | `state=running, phase=awaiting_supervision` | precedence | `blocked`, `attention=elsewhere` | Scenario: awaiting_supervision outranks stalled | More specific wins. **Rev 3 (cross-spec C1/M6):** renamed from `awaiting_owner_correction`; `elsewhere` is what keeps the Owner from `stop_plan`-ing a healthy adjudication |
| 6 | `state=running, paused_reason=owner_disabled` | side-flag | `blocked` | Scenario Outline: Normalized status | Same-state side-flag, orthogonal to phase |
| 7 | `state=running, phase=stalled, paused_reason=owner_disabled` | both set | `blocked`, native names both | Scenario Outline: Normalized status | Composite must not lose either |
| 8 | `state=done` | terminal | `completed` | Scenario Outline: Normalized status | |
| 9 | `state=failed, failed_reason=""` | terminal, no reason | `failed`, native `failed` | Scenario Outline: Normalized status | Bare terminal |
| 10 | `state=failed, failed_reason=judge_rounds_exhausted` | terminal + reason | `failed:judge_rounds_exhausted` | Scenario Outline: Normalized status | Distinguishable |
| 11 | `state="wat"` | invalid | `status="blocked"`, `attention="none"`, `native_status="unknown:wat"`, `notes.unmapped.plan=1`; no panic, no drop, no coercion to `failed` | Scenario: An unmappable native state is marked, never guessed | ⚠️ **Rev 3 (R2-MAJ-012).** Rev 2 demanded *"an explicit unknown marker"* — a sixth `status` value or a new field, **neither of which any requirement defined** — and mis-traced to *"A failed store yields a per-kind error"*, which is about a directory-level read failure. FR-006b defines the behaviour and the trace is corrected |
| 11a | `state=failed, failed_reason=dod_unreachable` | **cross-spec, new enum value** | `failed`, native `failed:dod_unreachable`, `intentionally_stopped=false` | Scenario: A cancelled job is distinguishable from a crashed one | **Rev 3 (cross-spec M1).** PS FR-035 adds this value. `false` is chosen with its blind spot stated in FR-006: PS's `abandon` (deliberate) and `planCannotProgress` (not) both land here and PS does not distinguish them on the record. Revisit if PS splits the value |
| 11b | `state=failed, failed_reason=supervision_unavailable` | **cross-spec, new enum value** | `failed`, native `failed:supervision_unavailable`, `intentionally_stopped=false` | Scenario: A cancelled job is distinguishable from a crashed one | **Rev 3 (cross-spec M1).** Unambiguous: this is a failure, not a stop |
| 11c | `state=running, phase=awaiting_supervision`, then `stop_plan` is called by the Owner | **cross-spec M6 composition** | after the stop: `failed`, `intentionally_stopped=true`, and with the default `include_terminal=false` the row **vanishes**, surviving only in `terminal_suppressed` | Scenario: Terminal rows are excluded by default | **Rev 3 (cross-spec m2).** Calling `list_jobs` right after `stop_plan` is the single most likely thing an agent does, and by default the plan it just stopped is not in the answer. `attention="elsewhere"` on the pre-stop row is what should have prevented the stop |
| 12 | `state=failed, failed_reason=stopped_by_user` | **intent boundary** | `failed`, `intentionally_stopped=true` | Scenario: A cancelled job is distinguishable from a crashed one | Closed enum `[VERIFIED: pkg/plan/plan.go:292]`, not free text |
| 13 | `state=running, paused_reason=` a 4 KB wrapped error containing a registered secret | **security × unbounded** | `native_status` redacted, then truncated to its maximum | Scenario: `native_status` is redacted and bounded exactly like `label` | `PausedReason` is a bare `string`, never validated `[VERIFIED: pkg/plan/plan.go:378]` |
| 14 | `state=draft`, no `include_drafts` | default exclusion | row **absent** | Scenario: Draft plans are excluded by default | Supersedes row 1's default behaviour |
| 15 | 26 `draft` + 1 `approved`, `include_drafts=true` | starvation within a group | the `approved` row is present and sorts first | Scenario: Draft plans are excluded by default and truncate first when included | Drafts truncate first |

#### Dataset: Subagent lifecycle state → normalized status (US-2, G4)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | `queued` | happy path | `queued` | Scenario Outline: Normalized status | |
| 2 | `running` | happy path | `running` | Scenario Outline: Normalized status | |
| 3 | `needs_input` | stuck | `blocked` | Scenario Outline: Normalized status | Live but cannot progress |
| 4 | `paused` | stuck | `blocked` | Scenario Outline: Normalized status | |
| 5 | `completed` | terminal | `completed` | Scenario Outline: Normalized status | |
| 6 | `failed` | terminal | `failed` | Scenario Outline: Normalized status | |
| 7 | `cancelled` (durable spelling) | **spelling split** | `failed`, native `cancelled` | Scenario Outline: Normalized status | Durable set is authoritative (G4) |
| 8 | `canceled` (legacy in-memory spelling) | **spelling split** | never reaches the mapper | Scenario Outline: Normalized status | Legacy map is not a source |
| 9 | `timed_out` | terminal | `failed` | Scenario Outline: Normalized status | |
| 10 | `""` | empty | `status="blocked"`, `attention="none"`, `native_status="unknown:"`, `notes.unmapped.subagent=1`; no panic, no drop | Scenario: An unmappable native state is marked, never guessed | ⚠️ **Rev 3 (R2-MAJ-012).** Same correction as plan row 11: the required output is now defined by FR-006b and the trace is re-pointed off the store-error scenario |
| 10a | `needs_input` | **attention boundary** | `blocked`, **`attention="caller"`** | Scenario: An informational block is distinguishable from one the caller must act on | **Rev 3 (FR-036).** The case the operator's *"just information"* ruling is **false** for — the subagent is blocked on an answer only the caller can give |
| 11 | `cancelled` | **intent boundary** | `failed`, `intentionally_stopped=true` | Scenario: A cancelled job is distinguishable from a crashed one | vs. row 6's crash, which is `false` |
| 12 | `failed`, `failed_reason=` a 2 KB wrapped error embedding a registered secret | **security × unbounded** | `native_status` redacted then truncated | Scenario: `native_status` is redacted and bounded exactly like `label` | `FailedReason` is documented *"Left open (not a closed enum)"* `[VERIFIED: pkg/session/lifecycle.go:236-239]` |
| 13 | `AgentID` naming an agent that no longer exists in the registry | **dangling reference** | `label` is the raw agent id; no error | Scenario: Live subagent handle recovered in the same process | FR-005's stated fallback |
| 14 | record with `generation=3`, `resumed_from` set | multi-generation | exactly one row, `generation=3` exposed | Edge Case "A subagent session resumed to a new generation" | Newest generation only |

#### Dataset: Bounds and truncation (US-4)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | 0 live jobs | zero | 0 rows, **`total_omitted` ABSENT**, `notes: null` | Scenario: Empty roster is a success | ⚠️ **Rev 3 (R2-MAJ-003).** Rev 2 specified `total_omitted=0` as an expected *output* here while FR-033 required it omitted when zero — the same test specified twice with opposite expectations |
| 2 | **24** `queued` jobs | min−1 | all returned, `total_omitted` **absent** | Scenario: Truncation is bounded | ⚠️ **Rev 3 (R2-MIN-002).** Rev 2 said "`L−1` live jobs", but FR-016 replaced the single live limit `L` with `queued=25 / running=25 / blocked=25`, so "`L` live jobs → all returned" is **false** if all `L` are `queued`. Rows 2–6 are restated against the sub-bounds |
| 3 | **25** `queued` jobs | at sub-bound | all returned, `total_omitted` **absent** | Scenario: Truncation is bounded | Off-by-one guard, per group |
| 4 | **26** `queued` jobs | sub-bound+1 | 25 returned, `total_omitted=1`, `omitted.by_status.queued=1` | Scenario: Truncation is bounded | |
| 5 | 500 live jobs spread across all three groups | pathological | bounded at 75, exact counts in **both** key spaces | Scenario: Truncation is bounded | The ADR's stress figure. Below FR-032(d)'s ceiling, so exactness applies |
| 6 | 25 `running` + 1 `blocked` | starvation | the `blocked` row is present | Scenario: A large queued and running population cannot evict a blocked row | ⚠️ **Rev 3 (R2-MIN-001).** Rev 2 referenced *"Scenario: Blocked rows are not starved"*, which **does not exist** — the only dangling scenario reference across all six datasets. Re-pointed at the real title |
| 7 | label of `labelMax−1` runes | min−1 | untruncated | Scenario: Unicode label truncates on a rune boundary | |
| 8 | label of `labelMax` runes | at limit | untruncated | Scenario: Unicode label truncates on a rune boundary | |
| 9 | label of `labelMax+1` runes | max+1 | truncated with ellipsis | Scenario: Unicode label truncates on a rune boundary | |
| 10 | 10 000-rune label | pathological | truncated, response bounded | Scenario: Unicode label truncates on a rune boundary | |
| 11 | label of 4-byte emoji at the limit | unicode boundary | valid UTF-8, no split rune | Scenario: Unicode label truncates on a rune boundary | |
| 12 | label containing a registered secret, over the limit | security × boundary | no secret fragment, within limit | Scenario: Label is redacted before it is truncated | Filter **then** truncate |
| 13 | **7-byte ASCII** label that **is** a registered secret | **below `FilterMinLength` (bytes)** | no secret fragment | Scenario: Label is redacted before it is truncated | ⚠️ **Rev 2 unit correction (MIN-001).** The gate is `if len(content) < c.Tools.GetFilterMinLength()` — `len()` on a string is **BYTES** `[VERIFIED: pkg/config/config.go:398-401]`. Rev 1 specified a *6-rune* label, which for CJK or emoji is 18–24 bytes and **is** filtered — so the test would have passed or failed purely on the alphabet the author picked, never exercising the bypass. 7 ASCII bytes is the actual bypass case |
| 13a | 3-rune CJK label (9 bytes) that **is** a registered secret | above the byte threshold, below the rune one | filtered normally by the replacer | Scenario: Label is redacted before it is truncated | The mirror of row 13 — proves the unit is bytes, not runes |
| 14 | secret-bearing label, `tools.filter_sensitive_data` disabled | filter disabled | bound still enforced; leak documented | Scenario: Label is redacted before it is truncated | Operator-disabled filtering must not also relax the bound |
| 15 | 400 `queued` + 400 `running` + 3 `blocked` | **cross-group starvation** | all 3 `blocked` present; `omitted` = 375/375/0 | Scenario: A large queued and running population cannot evict a blocked row | Operator ruling 1 — the sub-bounds are now the **only** protection, since `blocked` sorts last |
| 16 | `limit = 201` (hard max 200) | max+1 | clamped to 200, `limit_clamped_to=200`, **not** an error | Scenario Outline: Argument validation | Rev 1 required both an error and a clamp |
| 17 | `limit = 150` (between the 75-row live max and the 200 hard max) | **unreachable range** | success, **no** clamp reported, ≤ 75 live rows | Scenario Outline: Argument validation | The caller is told nothing was clamped while receiving half of what it asked for — FR-016 must state this relationship, not leave it inferred |
| 18 | 40 rows, **every** `started_at` empty | total-order boundary | identical order across 50 calls | Scenario: A total order survives rows that all tie on an empty started_at | `sort.Slice` is **not** stable; the `(kind, id)` tiebreak is what makes this pass |
| 19 | 500 live jobs × 10 000-rune label × 4 000-rune `native_status`, **4-byte-rune alphabet (emoji/CJK), stated explicitly** | response-size boundary, worst-case encoding | serialized length ≤ **172 288 bytes** (FR-030's identity with every constant valued) | Scenario: The serialized response stays within its stated size bound | ⚠️ **Rev 3 (R2-MAJ-005).** Rev 2 did not state the alphabet, so the test passed or failed on the author's choice of characters — the same defect FR-019a corrected for `FilterMinLength`, one requirement later. And `maxRows`/`fixedRowOverhead`/`envelopeOverhead` had no values, so the bound was unevaluable |
| 19a | the same fixture, **ASCII alphabet** | response-size boundary, best-case encoding | serialized length ≤ the same **172 288 bytes** | Scenario: The serialized response stays within its stated size bound | The mirror of row 19 — the bound must hold for both, which is what proves the identity is in bytes rather than runes |
| 20 | 25 `queued` + 25 `running` + 3 `blocked`, **`limit=30`** | **`limit` below the sum of the populated sub-bounds** | all **3** `blocked` rows present; allocation is round-robin, not tail truncation | Scenario: A large queued and running population cannot evict a blocked row | ⚠️ **Rev 3 (R2-MAJ-001) — the spec's through-line case.** Rev 2's total-cap-after-sub-bounds rule returned 25 queued + 5 running + **zero blocked** here, and SC-016 could not see it because SC-016 exercised the default call only |
| 21 | `label` of exactly 120 runes of 4-byte emoji (480 bytes) | **rune max AND byte max simultaneously at the boundary** | untruncated — both bounds are satisfied at exactly the limit | Scenario: Unicode label truncates on a rune boundary | The dual-bound boundary FR-030 introduces; row 22 is its max+1 |
| 22 | `label` of 121 runes of 4-byte emoji (484 bytes) | both maxima exceeded | truncated on a rune boundary until **both** `≤120 runes` and `≤480 encoded bytes` hold | Scenario: Unicode label truncates on a rune boundary | Truncation must satisfy both, not whichever is checked first |

#### Dataset: Store failure modes (US-5)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | all stores healthy | happy path | full roster, no error entries | Scenario: Live subagent handle recovered | |
| 2 | 1 corrupt JSONL of 5 | partial corruption | 4 rows, `unreadable=1` | Scenario: One corrupt lifecycle record does not erase the kind | Today's `List` would return 0 (C9) |
| 3 | JSONL line > 10 MB scan buffer | boundary | that record skipped + counted | Scenario: One corrupt lifecycle record does not erase the kind | Scanner error path |
| 4 | lifecycle file mode 000 | permission denied | that record skipped + counted | Scenario: One corrupt lifecycle record does not erase the kind | |
| 5 | plan dir mode 000 | store-level failure | per-kind error for `plan` only | Scenario: A failed store yields a per-kind error | |
| 6 | all three dirs mode 000 | total failure | three per-kind error entries | Scenario: All three stores failing | Not an empty success |
| 7 | truncated trailing JSONL line | tolerated corruption | record still loads from prior line | Scenario: One corrupt lifecycle record does not erase the kind | `tail` already tolerates this |
| 8 | empty lifecycle dir | zero | 0 subagent rows, no error | Scenario: Empty roster is a success | |
| 9 | 1 corrupt **plan** `*.json` of 5 | partial corruption, plan kind | 4 plan rows, `unreadable=1` for `plan` | Scenario: One corrupt plan file does not erase the plan kind | ⚠️ **Rev 2.** Today's `plan.Store.List` skips **and discards the count** `[VERIFIED: pkg/plan/store.go:163-167]` — needs FR-027 |
| 10 | 1 corrupt **task** `*.json` of 5 | partial corruption, task kind | 4 task rows, `unreadable=1` for `task` | Scenario: One corrupt task file does not erase the task kind | Same shape `[VERIFIED: pkg/task/store.go:254-255]` — needs FR-027 |
| 11 | plan store healthy, `computeActiveLocked` reported `reliable=false` | **cap read unreliable** | rows returned; `cap_active`/`cap_max` **both absent** | Scenario: An unreliable cap read omits the fields rather than reporting a wrong number | Never present-and-wrong |
| 12 | no `PlanEngine` wired at all | dependency absent | rows returned; cap fields absent, no error | Scenario: An unreliable cap read omits the fields rather than reporting a wrong number | Degrades, never fails |
| 13 | no `DelegateTool` wired | dependency absent | subagent rows returned with `actionable=false` | Scenario: A post-restart subagent row is an honest tombstone | The honest answer: nothing in this process can act on them |
| 14 | plan store healthy, engine **running**, snapshot `reliable=true`, **`cap_active == 0`** | **zero that is load-bearing** | `cap_active=0` and `cap_max=16` **PRESENT** at the top level; `cap_observed_at` present; `engine_running=true` | Scenario: Cap pressure distinguishes a real queue from a stopped engine | ⚠️ **Rev 3 (R2-CRIT-002).** Rev 2's FR-033 listed the cap pair among the omit-when-zero counters, which made this row — and US-2 AS-5, SC-003 and test 32 — unsatisfiable. A zero here means *"the engine is idle"*, not *"nothing to report"* |
| 15 | engine **stopped**, snapshot `reliable=true`, `observedAt` older than the 90 s bound | **stale-but-reliable** | `cap_active`/`cap_max` **PRESENT**; `cap_observed_at` present and visibly stale; **`engine_running=false`** | Scenario: Cap pressure distinguishes a real queue from a stopped engine | ⚠️ **Rev 3 (R2-CRIT-003 / cross-spec M4).** Rev 2 omitted the pair on staleness and never stated the bound — so the fields were suppressed in **exactly** the state they exist for. Staleness now labels rather than suppresses |
| 16 | engine running but `planStore.List` errors, so `Tick` takes its early return | **liveness under a failing store** | `engine_running` still truthful, because the heartbeat is stamped **before** the early return `[VERIFIED: pkg/agent/plan_engine.go:679-682]` | Scenario: Cap pressure distinguishes a real queue from a stopped engine | **Rev 3 (cross-spec M3).** Under rev 2's design the snapshot was not refreshed at all on this path and `reliable` was never set false, leaving an unstated staleness bound as the only guard |
| 17 | 10 000 task records against a 5 000-record ceiling | **scan ceiling exceeded** | rows bounded; `notes.scan_truncated={"task":{"scanned":5000,"present":10000}}`; that kind's `omitted`/`unreadable`/`terminal_suppressed` are **lower bounds**; `plan` and `subagent` stay exact | Scenario: A bounded scan is reported, never presented as a complete one | ⚠️ **Rev 3 (R2-CRIT-004).** Rev 2's ceiling had no value, no config key and no test, and its overflow was to be *"reported through the existing omission counters"* — impossible, since classifying an unloaded record requires loading it. `present` comes from the directory entry count, the one number that survives |

#### Dataset: Tool-policy resolution (US-7)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|-------|---------------|-----------------|-----------|-------|
| 1 | fresh install, agent `mia` | happy path | `allow` | Scenario Outline: Seeded policy per agent class | Needs the explicit override (C6) |
| 2 | fresh install, agent `worker` | inherit path | `allow` | Scenario Outline: Seeded policy per agent class | `tightenGlobalCeiling` |
| 3 | fresh install, a System Agent | least privilege | `deny` | Scenario Outline: Seeded policy per agent class | D8 |
| 4 | upgraded config, no `list_jobs` key anywhere | **the CRIT-003 case** | `allow`, zero backfill | Scenario: `list_jobs` resolves to allow on an upgraded installation | Global map merge (C7) |
| 5 | upgraded config, operator set global `list_jobs: deny` | operator override | `deny` | Scenario: `list_jobs` resolves to allow on an upgraded installation | JSON wins over the default |
| 6 | agent map `deny` + global `allow` | conflict | `deny` | Scenario Outline: Seeded policy per agent class | Deny-wins |
| 7 | agent map absent + global `allow` | one-sided | `allow` | Scenario: `list_jobs` resolves to allow on an upgraded installation | `case a == "": return g` |
| 8 | both sides absent, **god-mode off** | **both empty** | `deny` + Error log | Scenario Outline: Seeded policy per agent class | Fails closed — the **only** denying branch (C2, as corrected in rev 2) |
| 9 | `list_jobs` in a seed but not in `allStaticToolNames` | catalog drift | **panic** | Scenario: Naming the tool in a seed does not panic the process | The guard we must satisfy |
| 10 | **god-mode enabled**, both sides absent | **short-circuit** | `allow` — the merge never runs | Scenario Outline: Seeded policy per agent class | ⚠️ **Rev 2.** `if cfg.GodMode { return ToolPolicyAllow }` precedes both `resolveFromMap` calls `[VERIFIED: pkg/tools/compositor.go:175-177]`, so row 8 is **wrong under god-mode**. Missed by rev 1 *and* by the review's own §2 |
| 11 | **god-mode enabled**, agent map `deny` | **short-circuit over deny** | `allow` | Scenario Outline: Seeded policy per agent class | Even a System Agent's explicit `deny` is floored to `allow` — this is god-mode's documented effect, not a defect |
| 12 | global `list_jobs: deny`, agent map `allow`, god-mode off | **operator kill switch** | `deny`, tool absent from the advertised set | Scenario: An operator can switch the tool off globally | The supported off switch (US-8 AS-3) |
| 13 | global `list_jobs: ask`, god-mode off | **the third verdict** | `ask` — every call blocks on a human prompt | Scenario: The operator runbook explains a degrading install | ⚠️ **Rev 3 (R2-MIN-003).** The vocabulary is `allow \| ask \| deny` with precedence `deny > ask > allow`, and rev 2's FR-023, US-7 and all 12 rows discussed only two of the three. For an autonomous background agent recovering a handle mid-turn, `ask` is a **hang, not a prompt**. Supported but discouraged; the runbook says so and names global `deny` as the correct kill switch |

### Regression Test Requirements

This feature **modifies existing functionality** in three places. It is not purely additive.

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|--------------------|---------------|---------------------------|-------|
| `LifecycleFilter.matches` with no `ParentAgentID` set | existing filter tests | **Yes** — `TestLifecycleFilter_UnsetParentAgentIDUnchangedBehaviour` | **Survives the greenfield ruling.** The ADR-053 boot sweep's own queries leave `ParentAgentID` unset and must stay unfiltered — an unset filter field must remain "filter off" even though an unset *record* field is now impossible |
| `LifecycleStore.List` abort-on-error semantics | existing store tests | **No** — `List` is left untouched; `ListLenient` is added alongside | Deliberate: the boot sweep may want the strict form |
| `plan.Store.List` skip-and-swallow semantics | `pkg/plan` store tests | **No** — `List` untouched; a lenient sibling is added (FR-027) | Same pattern as `ListLenient` |
| `task.Store.List` skip-and-swallow semantics | `pkg/task` store tests | **No** — `List` untouched; a lenient sibling is added (FR-027) | Same pattern |
| `plan.Filter` with only `WorkspaceID` | `pkg/plan` store tests | **Yes** — `TestPlanFilter_UnsetOwnerAgentIDReturnsAll` | Adding a field must not change existing callers |
| `task.Filter` with `PlanID` unset | `pkg/task` store tests | **Yes** — `TestTaskFilter_UnsetPlanIDSetUnchangedBehaviour` | **Rev 2 (MAJ-002).** `PlanIDSet=false` must preserve today's "`PlanID==""` means filter off" behaviour for `list_tasks` and every gateway task endpoint |
| `allStaticToolNames` length/content | `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog` | **Update, not replace** | Both catalogs must gain the name together |
| Every agent's seeded policy map size | `coreagent` seed tests | **Update** | `denyAllThenOverride` output grows by one entry for every agent |
| Global `sandbox.tool_policies` seed size | `pkg/config` defaults tests | **Update** | Grows by one entry |
| `delegate.run` lifecycle mint — **every mint site** | ADR-053 delegate tests | **Yes** — `TestDelegateMint_FailsClosedOnEmptyParentAgentID`, **plus `TestDelegateMint_StampsParentAgentID` parameterised over EVERY mint site**: (1) `delegate.run` sync, (2) `delegate.run` async (`executeAsync`), (3) `follow_up`/Play generation mint when that path lands (FR-034). **Rev 3 (R2-MAJ-015): the positive path must be enumerated, not sampled** — rev 2 named one test against one site while FR-034 requires the field on every generation mint, so a future mint path could drop `ParentAgentID` and silently orphan a session from its parent's roster with the suite green | The negative test asserts the mint **refuses** rather than writing an empty parent; the positive set asserts every site actually stamps it |
| `delegate` mint operability | none | **Yes** — `TestDelegateMint_GuardDowngradeConfig` | **Rev 3 (R2-MAJ-015).** With `tools.delegate.require_parent_agent_id=false`, an empty parent logs at Error and mints — the field rollback for a change whose failure mode is "delegation stops". Default `true` is asserted separately |
| `task.Task` without `CreatedByAgentID` | `pkg/task` store tests | **Yes** — `TestTaskStore_UnsetCreatedByAgentIDUnchangedBehaviour` | **Rev 3 (FR-037).** Additive disk field: REST creation must keep working and must leave it empty; `list_tasks` and every gateway task endpoint must be unaffected. Greenfield (A0) means no backfill — pre-existing tasks carry empty and are therefore not attributed, which is the fail-closed direction |
| `pkg/config/defaults_test.go`'s tool-count assertion | `TestDefaultConfig_*` | **Update — replace the literal** | **Rev 3 (cross-spec C2).** `const wantToolCount = 83` `[VERIFIED: :92]` becomes the mechanical invariant `len(cfg.Sandbox.ToolPolicies) == len(coreagent.AllStaticToolNames())`. Operator ruling 4: seeding is a rule, not a hand-maintained number. See *Rev 3 → Landing order* for the arithmetic |
| `DelegateTool.mu` acquisition profile | ADR-053 delegate tests | **Yes** — `TestDelegateTool_ResolvableSessionIDsLocksOnce` | **Rev 2 (MAJ-004).** The new accessor must not add per-row contention to the hottest mutex in the delegation path |
| `PlanEngine.Admit` behaviour and `pe.mu` discipline | `pkg/agent` plan-engine tests | **Yes** — `TestPlanEngine_CapSnapshotIsLockFreeAndGlobal` | **Rev 2 (CRIT-001).** The snapshot accessor must not take `pe.mu` and must not change `Admit`'s own semantics |
| `SessionLifecycleRecord` wire shape | `pkg/api/generated/contract_test.go` | **No change** | **Rev 2 (MAJ-006).** `ParentAgentID` is disk-only. `make verify-contracts` must stay green because **no drift is introduced** — not because a regeneration was performed |

**Integration seams protected by existing tests**: `pkg/tools` registry registration and manifest
tiering; `pkg/gateway` boot coverage validation; the compositor's resolution table.

---

## Functional Requirements

### The tool surface

- **FR-001**: The system MUST register a read-only builtin tool named `list_jobs` in `pkg/tools`, scoped `ScopeGeneral`, **and MUST add it to `tools.GeneralBuiltinMetadata()` specifically**. `gateway.buildKnownBuiltinToolNames` iterates `GeneralBuiltinMetadata()`, `browser.BrowserBuiltinMetadata()` and `systools.AllTools(nil, nil)` `[VERIFIED: pkg/gateway/gateway.go:715-745]`, so a tool registered anywhere else silently misses the catalog and fails `TestBuildKnownBuiltinToolNames_MatchesCoreagentStaticToolCatalog`. This is a **fourth** required site alongside FR-023's (a)/(b)/(c).
- **FR-002** (**extended in rev 3 — R2-MAJ-007, R2-MAJ-016, R2-MAJ-017**): `list_jobs` MUST accept these optional arguments and no others: `kind` (`plan|subagent|task`), `status` (one **normalized** value), `include_terminal` (bool, default `false`), `include_drafts` (bool, default `false`), `limit` (integer, bounded), **`label_contains` (string, ≤ 64 runes)**. A **known** argument of the wrong type or with an out-of-enum value MUST produce a validation error and zero rows. Three dispositions are stated explicitly because each is a case rev 2 got wrong or left silent:

  - **`limit` above the hard maximum MUST be clamped, not rejected**, and the clamp reported as `limit_clamped_to`. `limit` ≤ 0 or non-integer remains a validation error.
  - **An UNKNOWN argument MUST be ignored, not rejected**, and reported once in `notes.ignored_args` (which FR-033 omits when empty). Hard errors are reserved for **known** arguments with invalid values.
  - **When `status` names a terminal value (`failed`, `completed`), `include_terminal` is implied `true`**, regardless of the argument's own value.

  **`label_contains`** is a case-insensitive substring match against the row's **post-redaction, pre-truncation** `label`. It MUST be applied **before** FR-016's bounds, so `omitted`, `terminal_suppressed` and every other counter stays exact against the *filtered* population. It MUST NOT be applied to `native_status` (which carries unvalidated runtime text — a filter over it would be a query interface onto wrapped error strings).

> **⚠️ REV-3 CORRECTIONS (FR-002) — three cases, one principle.**
>
> **(i) Unknown arguments contradicted this requirement's own stated principle (R2-MAJ-017).** Rev 2
> resolved the `limit` overflow case by clamping, with the explicit rationale *"clamping wins
> because it is the one disposition that never costs the caller a turn"* — and then hard-errored on
> **any** unknown argument. The BDD table made it concrete: an agent that passes `relation` — a
> field it just read off a response row — got a validation error and zero rows. LLM tool calls
> include stray arguments routinely. The same requirement applied opposite dispositions to two
> instances of the same class of input error and reconciled neither; the wasted turn the clamp was
> designed to avoid was reintroduced through the **more common** mistake.
>
> **(ii) `status="failed"` silently returned nothing (R2-MAJ-016).** `status` accepts all five
> normalized values, `include_terminal` defaults to `false`, and FR-016 excludes terminal rows
> unless it is true. So `list_jobs(status="failed")` — the single most natural query after *"what am
> I still working on?"* — returned an **empty roster**, and the agent concluded nothing had failed.
> `terminal_suppressed` partially rescued it, but no requirement, scenario or dataset row covered the
> combination and the argument-validation table did not list it. Auto-implying is better than
> erroring, by this requirement's own clamp rationale.
>
> **(iii) There was no way to reach row 26 of a group (R2-MAJ-007).** Bounds are hard at 25 per live
> status group. `kind` and `status` were the only narrowing arguments and neither escapes the bound
> — `kind="plan", status="queued"` still returns at most 25 of the caller's 400 queued plans. There
> was no offset, no cursor, no label filter and no `since`, and FR-002 forbade adding one, while the
> *"Roster at scale"* evaluation scenario (500 live jobs) expected *"the narrowed calls surface **the
> specific rows**"* — unreachable for 94 % of them. **The tool's headline use case (US-1: find the
> handle for the job I lost) failed deterministically once the caller had more than 25 jobs in a
> group**, reporting that failure only as a large `omitted` count.
>
> `label_contains` is chosen over an opaque cursor because it is one predicate with no state, it
> matches how an agent actually searches (*"the one about the migration"*), and applying it before
> the bounds keeps every counter exact — a cursor would have required the counters to be re-based on
> a window and would have made FR-017's arithmetic a second thing to get right.

> **⚠️ REV-2 CORRECTION (FR-002, `limit`).** Rev 1 was internally contradictory: its Behavioral
> Contract required *"out-of-range `limit`"* to produce *"a validation error and zero rows"*, while
> its Edge Cases and its own BDD table both required *"clamped to the maximum, and the clamp
> reported"*. `limit=250` against a hard max of 200 was simultaneously required to error and to
> succeed. `TestArgs_Validation` traced to the table, so the implementation would have followed the
> clamp and left the Behavioral Contract as decoration. Clamping wins because it is the one
> disposition that never costs the caller a turn.

- **FR-003**: Each row MUST carry `kind`, `id`, `label`, `status`, `native_status`, `relation`, **`attention` (FR-036)**, `started_at`, `last_activity_at`, `workspace_id`, `actionable` and `intentionally_stopped`, plus `generation` when > 0. `workspace_id` is emitted on **every** row, in every scoping mode. R2-OBS-003 suggested emitting it only when `workspace_scoped=false` (when scoped, every row's value duplicates the envelope's), and **rev 3 declines it**: it is an OBSERVATION, not a finding; it would require re-writing US-3 AS-4, its BDD scenario and dataset row 9, all of which passed review and all of which assert the per-row field; and a variable-shape row is a worse trade for an LLM consumer than a handful of duplicated short strings. Recorded as a considered decision rather than an oversight. The normative shape of a row and of the envelope is the *Response Shape* section below, which is **authoritative over every prose "the response carries X" in this document**. `native_status` is REQUIRED and never omitted — **and is therefore subject to FR-019 and FR-030 exactly as `label` is.** The per-kind timestamp mapping MUST be exactly:

  | Kind | `started_at` source | `last_activity_at` source |
  |---|---|---|
  | plan | `Plan.StartedAt` — RFC3339 **string**, `omitempty` `[VERIFIED: pkg/plan/plan.go:442]`; `""` for `approved`/`draft` | `Plan.LastActivityAt` — RFC3339 string, `omitempty` `[VERIFIED: plan.go:379]` |
  | task | `Task.StartedAt` — RFC3339 **string**, `omitempty` `[VERIFIED: pkg/task/task.go:320]`; `""` for `inbox`/`next` | **fallback** `Task.UpdatedAt` — the struct has no `LastActivityAt` `[VERIFIED: task.go:318-319]` |
  | subagent | **fallback** `LifecycleRecord.CreatedAt` — `time.Time`; the record has **no** start timestamp at all `[VERIFIED: pkg/session/lifecycle.go:241-242]` | `LifecycleRecord.UpdatedAt` — `time.Time` |

  Both fields MUST be emitted in a **single normalized representation** — RFC3339 in **UTC** at fixed precision — produced by **one shared helper**, never by comparing a `time.Time` against an RFC3339 string or two strings of differing precision.

> **⚠️ REV-2 CORRECTION (FR-003, timestamps).** Rev 1 required `started_at` and `last_activity_at`
> on every row without checking that they exist. Three problems, none previously addressed: (1) the
> `subagent` kind **has no start timestamp** — the mapping was simply undefined; (2) the three
> kinds mix RFC3339 **strings** with `time.Time`, and FR-020's determinism makes the formatting
> choice load-bearing; (3) `started_at` is empty for **exactly the group that needs the tiebreak** —
> every `queued` row has never started — so the whole `queued` sub-bound ties on `""`, and
> `sort.Slice` is **not** stable. Rev 1's `TestSortOrder_Deterministic` would therefore have flaked
> in CI rather than failed in review. FR-020's total order closes it.

- **FR-004**: The `id` MUST be the handle that kind's other tools accept: plan → `plan_id`, task → `task_id`, subagent → the durable delegate `session_id`.
- **FR-005**: Labels MUST be: plan → title; task → title; subagent → **the target agent's display name, resolved from `LifecycleRecord.AgentID` through the agent registry**. `AgentID` stores an **id**, not a name `[VERIFIED: pkg/session/lifecycle.go:203]`, so the registry is a required dependency; **when the id no longer resolves (the agent was deleted or renamed), `label` MUST be the raw agent id** — never empty, never an error. Durable records outlive agents, so this is a normal case, not a defect path.
- **FR-006**: The normalized `status` vocabulary MUST be exactly `queued | running | blocked | failed | completed`, where `blocked` means *live and not progressing on its own*, per the mapping tables in this spec. (Rev 3 wording change: rev 2 said *"unable to progress **without intervention**"*, which was false for one third of what maps to `blocked` and actively harmful for another third — see FR-036 and operator ruling 3.) Because that vocabulary has no slot for "deliberately stopped", **every row MUST additionally carry `intentionally_stopped bool`**, derived **only** from the closed portion of each kind's reason field — `session.LifecycleCancelled`, `plan.FailedReasonStoppedByUser` `[VERIFIED: pkg/plan/plan.go:292]` — never by parsing `native_status`. Its **two blind spots MUST be stated in this requirement rather than discovered from a wrong value**:
  - **`task` rows always report `intentionally_stopped=false`.** `task.Status` has no `cancelled` value and `task.Task` carries no stop-intent reason field, so a deliberately stopped task is indistinguishable from a crashed one. This is a **known blind spot, not a derivation** (R2-MIN-010); a dataset row makes it visible rather than inferred. Reconsider if `task.CancelReason` `[VERIFIED: pkg/task/task.go — `CancelReason` is set on a `failed` task cancelled via a user Stop, ADR-052 FR-028]` is promoted to a closed, always-populated enum; today it is set on one path only and is not a general stop-intent signal.
  - **`plan.FailedReasonDodUnreachable` reports `false`** (cross-spec M1). PS's `abandon` verb is a *deliberate* adjudicated termination that lands as `dod_unreachable`, but PS's `planCannotProgress` path lands there too and PS does not currently distinguish them on the record. Reporting `true` would be wrong half the time and reporting `false` is wrong the other half; `false` is chosen because the harm is asymmetric (a false `true` suppresses a legitimate re-dispatch of work nobody stopped). **This MUST be revisited if PS splits the value**, and is raised to PS as a finding.
  - `plan.FailedReasonSupervisionUnavailable` (cross-spec M1) reports `false` unambiguously — it is a failure, not a stop.

- **FR-006a**: Every `native_status` substring sourced from a **validated enum** (`plan.PlanPhase`, `plan.FailedReason`, `plan.State`, `session.LifecycleState`, `task.Status`) MUST be produced by interpolating that package's **exported constant**, never by writing the string literal. Hand-typed composites such as `"running/awaiting_supervision"` are **forbidden**, and `TestNativeStatus_ComposedFromConstants` MUST assert byte-equality against the constants.

> **⚠️ REV-3 (FR-006a, cross-spec C1) — why a whole requirement for a string.** This spec previously
> hardcoded `awaiting_owner_correction` in six normative places, and PS renames it. A Go
> implementation that switches on `plan.PhaseAwaitingSupervision` gets compiler coverage for the
> *constant* — but the **composed output literal** is invisible to `go build`, to `tsc -b`, and to
> PS's own FR-062 rename sweep, whose directory list covers neither `pkg/tools/**` nor
> `pkg/agent/**`. So this spec could ship a wrong `native_status` while PS's *"`rg` returns zero"*
> criterion passes. That is the same defect class as the sibling spec's rename check missing a
> spelling: **the test that checks for absence must be extended to cover the composition, not just
> the declaration.** FR-006a is that extension, and it closes the class rather than the six
> instances.

- **FR-006b**: A native state that maps to **no** normalized value MUST NOT panic, MUST NOT be dropped, and MUST NOT be silently coerced to `failed`. It MUST produce a row with `status="blocked"`, `attention="none"`, `native_status="unknown:<raw>"` (redacted and bounded per FR-019/FR-030), and MUST increment `notes.unmapped` for that kind. The five-value `status` vocabulary is **not** extended — this is the same shape as FR-006's companion-field resolution, and it keeps ADR-056 D3 intact.

> **⚠️ REV-3 (FR-006b, R2-MAJ-012).** Rev 2's *Plan native state* dataset row 11 (`state="wat"`) and
> *Subagent lifecycle* row 10 (`state=""`) both demanded *"an explicit unknown marker, no panic"* —
> a sixth value or a new field, **neither of which any requirement defined** — and both mis-traced
> to *"A failed store yields a per-kind error"*, which is about a directory-level read failure, not
> an unrecognised enum value. On real data (a plan written by a newer build, a hand-edited file) the
> implementer's guess would have decided between a panic, a dropped row and a silent mapping to
> `failed`. Both rows are re-traced in rev 3 and a test is added.

- **FR-036** (**rev 3, operator ruling 3 + cross-spec M6**): Every row MUST carry `attention`, exactly one of `none | caller | elsewhere`, derived deterministically:

  | Condition | `attention` | Meaning |
  |---|---|---|
  | `status != blocked` (any kind, including terminal) | `none` | Stated explicitly so it is not inferred: `attention` is a **blocked-row qualifier**, not a general priority field. |
  | `task.Status == blocked` | `none` | **The operator's case.** The task has an unmet dependency, has not run yet, and clears on its own. The caller cannot do anything about it — *"it is just information."* |
  | `plan_phase == awaiting_supervision` | `elsewhere` | Another principal (PlanSupervisor) is adjudicating. The caller MUST NOT intervene. |
  | `plan_phase == stalled`, or `paused_reason != ""` | `caller` | Stuck. Needs a correction or a steer from the caller. |
  | subagent `state ∈ {needs_input, paused}` | `caller` | Waiting on an answer only the caller can supply. |
  | `status == blocked` via FR-006b (unmapped) | `none` | Unknown state; asserting the caller must act on data we could not parse would be a guess. |

  `attention` MUST NOT be derived from `native_status` text, and MUST NOT be omitted when `none` — it is **state, not a diagnostic**, and FR-033's omit-when-zero convention does not apply to it.

> **⚠️ REV-3 (FR-036) — resolving the conflation *before* applying the operator's ruling.** The
> operator ruled that `blocked` is *"just information"* because *"the executer cannot do anything
> about it"*. That is true of a dependency-blocked task and **false** of a subagent sitting in
> `needs_input` (which is blocked *on the caller*) and of a `stalled` plan (which needs the caller's
> correction). Applying the ruling uniformly would have told an agent to ignore work that is waiting
> on it — a plausible-looking simplification that produces the exact silence US-2 exists to
> eliminate. Splitting the vocabulary is what lets the ruling be applied honestly: `attention=none`
> **is** just information, and it is the reason `blocked` sorting last (operator ruling 1) is
> acceptable; `attention=caller` is not.
>
> `elsewhere` is not cosmetic. Cross-spec M6: under PS the Owner agent is **forbidden** from
> correcting a plan parked at `awaiting_supervision` (PS FR-011, and PS FR-009 denies the Owner
> `plan_correct`), while PS FR-042/FR-043 newly grant that same Owner a `stop_plan` tool authorised
> on exactly `caller.AgentID == p.OwnerAgentID`. An Owner reading a bare `blocked` — which rev 2
> defined as *"unable to progress **without intervention**"* — and acting on this spec's own stated
> semantics has exactly one tool available: **stop the plan mid-adjudication**, which PS FR-044 then
> cascades into cancelling the in-flight supervision turn. Two independently reasonable specs
> composing into an agent that kills healthy work is precisely the CRIT-001 failure shape, arriving
> from the other document.

> **⚠️ REV-2 CORRECTION (FR-006, cancelled vs. crashed).** Rev 1 collapsed `cancelled`, `timed_out`,
> `stopped_by_user` and `judge_rounds_exhausted` all to `failed`, so a user-initiated stop and a
> crash were the same value. US-2's thesis is that the agent must tell *stuck and needing
> intervention* from healthy states — but an agent that stopped a delegation on purpose, then lost
> context, would see `failed`, and re-dispatch work the user deliberately cancelled. Rev 1's only
> recourse was `native_status`, the field CRIT-002 shows is unbounded and unredacted and which
> FR-006 explicitly subordinates. A boolean derived from the **closed** enums keeps the ADR's
> five-value vocabulary intact while making the distinction actionable. **This should be reflected
> in ADR-056 D3's status vocabulary** — see *Proposed ADR-056 amendments*.

- **FR-007**: Rows MUST be sorted `queued → running → blocked → failed → completed` — **ADR-056 D3's order, unchanged**. Within a group, the intra-group direction is **per-group and MUST be stated, not inherited**:
  - **Live groups (`queued`, `running`, `blocked`): `started_at` ASCENDING — oldest first.**
  - **Terminal groups (`failed`, `completed`): `started_at` DESCENDING — most recently finished first.**

  Then by `kind` ascending, then by `id` ascending, so the comparator is **total** (FR-020). Within `queued`, `approved` plans MUST rank above `draft` plans.

> **⚠️ REV-3 CORRECTION (FR-007, intra-group direction — R2-MAJ-008).** Rev 2 ordered **every** group
> `started_at` DESC and gave no rationale anywhere; operator ruling 1 constrained the *group* order,
> not the intra-group direction, so this was never settled by the ruling. Because truncation takes
> the head of the sorted list, DESC drops the **oldest** live jobs first — and US-1's entire premise
> is recovering a handle lost because *"its context window was trimmed, or a wake started a fresh
> turn"*, i.e. work that has been running long enough to fall out of context. Under exactly the load
> where truncation bites, rev 2's order **systematically hid the long-running, most-likely-forgotten
> jobs and showed the ones the agent had just started and still had ids for.** The direction is
> reversed for the live groups and kept for the terminal ones, where "most recently finished" is
> what the group is for. **The reasoning is recorded here so it is not silently reversed later.**

> **⚠️ REV-2: THE REV-1 REORDER IS WITHDRAWN (operator ruling 1).** Rev 1 inverted D3's order to put
> `blocked` first, arguing that with `blocked` third and the live block sharing one budget, the rows
> dropped first under load are exactly the ones the tool exists to surface. The **operator's ruling
> is that the ADR's order stands and FR-016's per-group sub-bounds are the correct mechanism** for
> that concern. Rev 1 described the reorder and the sub-bounds as *"independently sufficient"*; with
> the reorder withdrawn, **the sub-bounds are the only remaining protection**, so FR-016 now states
> them as explicit numbers and SC-016 asserts the starvation property directly rather than leaving
> it inferred. Ambiguity #1 is **resolved**.

### Scoping — the security control

- **FR-008**: `list_jobs` MUST **fail closed**. When the calling agent id is absent, empty, or whitespace-only after trimming, it MUST return an error and zero rows — never an unfiltered list and never an empty-but-successful roster.
- **FR-009**: `list_jobs` MUST scope to the calling context's workspace via `ToolWorkspaceID(ctx)`, and every row MUST carry `workspace_id`. **`ToolWorkspaceID(ctx)` is NOT guaranteed non-empty** — it is conditionally injected `[VERIFIED: pkg/agent/loop.go:6381-6383]` and returns `""` for any turn whose channel binding carries no workspace `[VERIFIED: pkg/tools/base.go:230-233]`. This is a **deliberate exception to FR-008's fail-closed posture**, and the system MUST make it visible rather than silent: when the workspace id is empty, the roster spans every workspace **for the resolved principal only**, and the response MUST carry `workspace_scoped: false` (`true` otherwise). The system MUST NOT fail closed here, because workspace-less turns are legitimate; it MUST NOT present a cross-workspace roster as a scoped one either.
- **FR-010**: Owner scoping MUST be, per kind, and the row MUST carry `relation` naming which reading matched:

  | Kind | Predicate | `relation` |
  |---|---|---|
  | plan | `Plan.OwnerAgentID == caller` | `runs` |
  | subagent | `LifecycleRecord.ParentAgentID == caller` | `dispatched` |
  | task | `Task.PlanID == ""` **and** (`Task.AgentID == caller` **or** `Task.CreatedByAgentID == caller`) | `runs` when `AgentID` matched, else `dispatched`; `runs` wins when both match |

  **Every predicate field above MUST be agent-id-namespaced, and MUST reject an empty value on both sides.** `Task.CreatedBy` MUST NOT be used (FR-037). The `relation` value is constant for two of the three kinds by construction (`plan` is always `runs`, `subagent` is always `dispatched`); that is **deliberate uniformity, not a bug** (R2-OBS-004) — only `task` varies, and a future reader should not "simplify" the constant cases away.

  The task union MUST be applied **in the tool**, over a single `task.Store.List(task.Filter{WorkspaceID, PlanID:"", PlanIDSet:true})` — not by two `List` calls. `task.Filter` has no OR predicate, the store loads every file regardless of filter anyway (see the `pkg/plan` cost note), and two calls would double the scan for zero I/O saving. Each task MUST appear **at most once**. The union MUST be applied **before** FR-016's bounds, so the omission counts stay exact.

- **FR-037** (**rev 3, R2-CRIT-005**): `task.Task` MUST gain `CreatedByAgentID string \`json:"created_by_agent_id,omitempty"\`` — an **agent-id-namespaced** attribution field, written **only** by the agent/tool creation paths (`pkg/tools/task.go`, `pkg/tools/todos.go`) from `ToolAgentID(ctx)`, and left **empty** by every REST/human path. FR-010's `dispatched` predicate MUST read this field and MUST NOT match on an empty value. The field is **disk-only** — `task.Task` carries the same `// not-wire-format: internal disk struct` marker as `session.LifecycleRecord` and `plan.Plan` `[VERIFIED: pkg/task/task.go:214]` — so FR-025's "no contract change" position is preserved and the field MUST NOT be added to any schema in `contracts/`.

> **⚠️ REV-3 CORRECTION (FR-010 task predicate → FR-037). Rev 2 re-imported the exact hazard its own
> strongest correction eliminated.** C4 rejects `Owner`/`CreatedBy` for **plans** on the ground that
> they are **mixed-namespace**, and selects the validated, always-an-agent-id `OwnerAgentID`,
> noting this *"eliminates R4's mixed-namespace risk entirely"*. FR-010 then adopted
> `Task.CreatedBy == caller` for the `task` kind **one predicate later**, with no namespace
> analysis, no validator citation, and no `[VERIFIED]` tag on the one property C4 established as
> decisive. Re-verified in the working tree on this branch:
>
> | Write site | Value written to `Task.CreatedBy` | Namespace |
> |---|---|---|
> | `pkg/tools/task.go:531` | `callerID` | **agent id** |
> | `pkg/tools/todos.go:147` | `agentID` | **agent id** |
> | **`pkg/gateway/rest_tasks.go:847`** | **`c.Username`** | **username** |
>
> `[VERIFIED: all three lines; and `pkg/task/task.go:314-316` documents `Owner`/`CreatedBy` only as
> *"server-set attribution (read-only on the wire)"* — it constrains **no** namespace.]`
>
> **The disclosure this produces is silent and P0.** A human user whose username equals an agent id
> — `mia`, `jim`, `ava`, `ray` are all plausible usernames and the base roster's ids are **public**
> — has **every standalone task they created in the SPA** surfaced, *with its title*, in that
> agent's roster as `relation="dispatched"`, on a user story whose thesis is *"Never see another
> principal's work."* There is no counter, no marker, and the row is indistinguishable from
> legitimately dispatched work. Rev 2's *Calling principal* dataset row 6 tested the
> username/agent-id collision **for plans only** — the one kind where the spec had already fixed it.
>
> Rev 3 takes disposition (a) — the same one C4 reached for plans — because it removes the hazard
> rather than mitigating it. The `runs` half needs no change: `Task.AgentID` is *"the assigned
> agent"* and is written from `agentID` on the tool path `[VERIFIED: pkg/tools/task.go:530]` and
> from a caller-supplied `agent_id` on the REST path `[VERIFIED: pkg/gateway/rest_tasks.go:718]` —
> an agent id in both cases, never a username. **`Task.Owner`/`Task.CreatedBy` remain untouched**
> for display and existing REST behaviour; this spec simply stops using them as an authorization
> predicate. Greenfield (A0) means no backfill: pre-existing tasks carry an empty
> `CreatedByAgentID` and are therefore not attributed — the fail-closed direction.
>
> **The same defect exists in `list_tasks`' `role="delegator"`, in production, today.** A7 requires
> it filed before this spec merges (R2-MIN-004).

> **⚠️ REV-2 CORRECTION (FR-010, the task kind).** Rev 1's task predicate was `CreatedBy` alone,
> which implements only the *"work I dispatched"* half. `task.Task.AgentID` is *"the assigned
> agent"* `[VERIFIED: pkg/task/task.go:234-235]`, and the in-tree `list_tasks` already exposes
> exactly this split — `role="assignee"` filters `AgentID`, `role="delegator"` filters `CreatedBy`
> `[VERIFIED: pkg/tools/task.go:55-67]`. Under rev 1, **an agent executing a live `in_progress`
> standalone task assigned to it by a human or another agent saw nothing in `list_jobs`** — the
> single most literal reading of *"what am I still working on?"*, and the Evaluation Scenario's own
> prompt. Rev 1's Explicit Non-Behaviors locked the delegator reading in (*"only standalone tasks
> **it created**"*) without ever considering the assignee one. The union plus an explicit `relation`
> field resolves it; **the ownership axis is now stated in US-1 rather than left implicit across
> three mutually inconsistent predicates.**

> **⚠️ ADR CORRECTION (FR-010, plan).** ADR-056 D4 says plans are scoped by `Owner`, and R4 adds a
> mixed-namespace caveat. `Owner` is `callerID` on the tool path but `c.Username` on the REST path
> `[VERIFIED: pkg/tools/plan.go:286; pkg/gateway/rest_plans.go:547]` — genuinely mixed.
> `OwnerAgentID` is **required**, validated, and always an agent id on both paths (C4). Using it
> **eliminates** R4 rather than mitigating it, and additionally returns human-authored plans to the
> agent that actually runs them. This is a strict improvement on the ADR.

- **FR-011**: `actionable` MUST be **`false` for any row whose `status` is `failed` or `completed`, for all three kinds.** For `subagent` it is additionally `false` when the `session_id` does not resolve in the current process's delegate session index, or when no `DelegateTool` is wired — the honest answer, never an error. It is `true` otherwise.

> **⚠️ REV-3 CORRECTION (FR-011, R2-MAJ-006).** Rev 2 stated flatly *"Plan and task rows are always
> `actionable=true`"*. With `include_terminal=true` the roster contains `done`/`failed` plans and
> `done`/`failed` tasks: `execute_plan` will not run a `done` plan and the task action tools will
> not act on a `done` task, so those rows carried `actionable=true` **and a handle that fails on
> use** — the exact defect US-6 exists to prevent, flagged honestly for `subagent` and left
> unflagged for the two kinds where it is equally true. The correction generalises the rule to
> *terminality* (kind-independent) plus *process lifetime* (subagent-only), which is what the two
> underlying causes actually are.
- **FR-028**: `DelegateTool` MUST gain an **exported batch accessor** — e.g. `func (t *DelegateTool) ResolvableSessionIDs(ids []string) map[string]bool` — that takes `t.mu` **exactly once** for the whole batch, never once per row. `list_jobs` MUST call it at most once per invocation. The `list_jobs → DelegateTool` edge MUST appear in Symbols Involved and the Impact Assessment (it does, as of rev 2).

> **⚠️ REV-2 CORRECTION (FR-011 → FR-028).** Rev 1 required `actionable` to be resolved against
> *"the current process's delegate session index"* and specified **no** accessor and **no** wiring.
> That index is an unexported `map[string]string` guarded by `t.mu` — the same mutex every
> `status`, `inbox`, `inbox_ack`, `steer`, `respond`, `cancel`, `follow_up` and `peek` call takes
> `[VERIFIED: pkg/tools/delegate.go:299-305; no exported reader exists among the 40+
> `func (t *DelegateTool)` methods]`. So rev 1 needed an unspecified new API **and** an unspecified
> new wiring edge, and a naive implementation would take the hottest mutex in the delegation path
> **once per row**. FR-021 forbids contending with `pe.mu` on precisely the reasoning that *"a
> read-only visibility tool must never contend with the dispatch path"*, while rev 1 permitted —
> unremarked — contention with a strictly hotter one.

> **⚠️ ADR CORRECTION (FR-011).** ADR-056 FR-3 and Option B's "Strengths" claim the tool hands back
> a durable, working handle. It does not: all `session_id`-bearing `delegate` actions resolve through
> a process-global in-memory map (C8). **The tool delivers durable enumeration, not durable acting.**
> `actionable` makes the limit visible instead of leaving the caller to discover it by failing. The
> ADR's §3 table and Option B strengths should be amended to say "durable enumeration".

- **FR-012**: The tool description MUST state plainly that a row with `actionable=false` is informational only and its `id` will not be accepted by that kind's action tools. **This clause is verified by `TestListJobs_DescriptionContract` asserting on `Description()` directly** — not by any test that asserts on a row (rev 1 traced it to `TestListJobs_PostRestartTombstone`, which asserts on no string at all, leaving FR-012 with zero coverage).
- **FR-012a** (**rev 3, cross-spec M6**): The tool description MUST state that `attention="elsewhere"` means **another agent is already handling the row and the caller must not intervene** — naming `awaiting_supervision` as the case — and that `attention="none"` on a `blocked` row means there is nothing for the caller to do (operator ruling 3). Without this clause the `attention` field is a value the LLM has no instruction for, and the M6 composition (Owner reads `blocked`, reaches for `stop_plan`, aborts a healthy adjudication) is reachable through the model's own inference rather than through the spec.
- **FR-012b** (**rev 3, R2-MAJ-014**): `Description()` MUST NOT exceed **900 characters**, asserted by `TestListJobs_DescriptionContract`. Operator-facing material — the FR-023 kill switch, the `unreadable` runbook, the omit-when-zero convention's rationale — MUST live in the operator documentation (FR-032(e)), **not** in the description.

> **⚠️ REV-3 (FR-012b) — the spec was optimising the wrong payload.** Six requirements mandate
> content in `Description()` (FR-012, FR-012a, FR-016, FR-023, FR-024, FR-033, FR-035), and
> Ambiguity #4 proposes a seventh. Omnipus sends the full tool set on **every** request, so this
> text is a fixed **per-request** token cost for every agent that has the tool — which FR-023
> requires to be all of them. Meanwhile FR-033 deletes zero-valued counters from the **response**
> specifically to protect the caller's context, and `TestListJobs_DescriptionContract` asserted the
> *presence* of each clause with **no bound on total size**. That is a net context regression on
> the axis this spec claims to care about most, and a presence-only assertion is the through-line
> failure again — it passes no matter how large the string grows. An operator does not read tool
> descriptions and an LLM does not need the kill switch; the split follows.

### The `subagent` parent linkage

- **FR-013**: `session.LifecycleRecord` MUST gain `ParentAgentID string \`json:"parent_agent_id"\`` — **without `omitempty`** (see FR-015) — populated at mint time from `ToolAgentID(ctx)`, and `LifecycleFilter` MUST gain a matching clause. The field is **disk-only**: it MUST carry the same doc-comment convention as its neighbours `ParentDurableKey` and `OriginChannel`/`OriginChatID` (*"Not part of the generated wire shape"*), and **MUST NOT be added to `contracts/components/schemas/SessionLifecycleRecord.yaml`**. `ParentAgentID` MUST be carried forward on every generation mint (FR-034).

> **⚠️ REV-2 CORRECTION (FR-013, contract-first does NOT apply here).** Rev 1 asserted that
> `LifecycleRecord` *"has a generated wire counterpart → Constraint #8 applies"* and mandated the
> full five-step dance. The struct's own header says otherwise: `// not-wire-format: internal disk
> record; a caller (pkg/tools/delegate.go) maps it onto generated.SessionLifecycleRecord at the
> tool-result boundary` `[VERIFIED: pkg/session/lifecycle.go:183-185]` — the same marker
> `plan.Plan` `[VERIFIED: pkg/plan/plan.go:353]` and `task.Task`
> `[VERIFIED: pkg/task/task.go:214]` carry. The disk record and the wire schema are deliberately
> **different shapes**, and the divergence is documented on the fields themselves.
> `ParentAgentID` is a purely internal scoping predicate read by one Go tool with **no SPA
> consumer** (A6 puts SPA work and REST parity out of scope). Because
> `SessionLifecycleRecord.yaml` declares `additionalProperties: false`, following rev 1 would have
> **expanded the SPA-visible wire contract for a field nothing on the wire reads.** Constraint #8
> governs *"every byte crossing the gateway/SPA boundary"*; this crosses none.

- **FR-014**: `plan.Filter` MUST gain `OwnerAgentID string`, with an empty value meaning "filter off" (matching the existing field convention) — and the tool MUST therefore never pass an empty value (guaranteed by FR-008).
- **FR-015** (**rewritten in rev 2 — the greenfield ruling deletes the legacy framing, not this requirement**): `delegate`'s lifecycle mint MUST **fail closed** when `strings.TrimSpace(ToolAgentID(ctx))` is empty — returning an error and minting **no** record — so no unattributable lifecycle record can be created. `LifecycleRecord.ParentAgentID` MUST be declared **without `omitempty`**, so the field is always present on disk and an empty value is a visible bug rather than an absent key. `list_jobs` MUST NOT infer a parent from `ParentDurableKey`, `ScopeID` or `AgentID`. **Rev 3 (R2-MAJ-015): the guard MUST be operable.** A config key `tools.delegate.require_parent_agent_id` (default **`true`** — fail-closed is the right default) MUST exist to downgrade the guard to *log-at-Error-and-mint-with-empty* in an incident; the FR-023 kill switch disables `list_jobs`, **not** this guard, so without the key there is no field rollback for a change that can stop delegation entirely. The rollback procedure MUST be named in the operator runbook (FR-032(e)). A **positive-path regression test is required per mint site**, enumerated in the Regression table — not one test against one site (FR-034 requires the field on *every* generation mint, and `follow_up`/Play is a mint path that does not exist yet).

> **⚠️ Why FR-015 was rewritten rather than deleted.** The greenfield ruling (*"old sessions on disc
> do not matter"*, no migration anywhere) removes the *legacy-record* half of rev 1's FR-015 — the
> `unattributable_subagents` field, its scenario, its test and Ambiguity #8 are all deleted. But
> **`ParentAgentID == "" ` stays reachable at mint on a brand-new install**, for three verified
> reasons: (1) rev 1's FR-013 stated no non-empty requirement, only *"populated at mint time from
> `ToolAgentID(ctx)`"*; (2) the mint site **already guards** an empty agent id for the *target* —
> `if agentID != "" && t.getAgentRegistry != nil` `[VERIFIED: pkg/tools/delegate.go:926]` — so its
> own author considered the case possible, and rev 1 proposed stamping the *parent* with no
> equivalent guard; (3) rev 1's `omitempty` tag makes an empty parent serialise to an **absent
> key**, byte-identical to the legacy shape. Delete FR-015 while keeping `omitempty` and such a row
> is **silently dropped from the roster with no counter anywhere** — strictly worse than the design
> being replaced, which at least counted it.
>
> Each half earns its place: the **fail-closed clause** is what actually retires the "prospective
> only" caveat; **dropping `omitempty`** is what makes the invariant auditable (a present-but-empty
> key is a detectable bug, an absent key is not); and the **anti-inference sentence** is the one
> part of rev 1's FR-015 that was never about legacy data at all. It encodes **C1** — the verified
> fact that `ParentDurableKey` is *shared* between parent and children
> `[VERIFIED: pkg/tools/delegate.go:924 + pkg/agent/subturn.go:970]`, that `ScopeID` is `""`
> for a top-level delegation, and that `AgentID` is the **child's**. Lose that sentence with the
> rest of the legacy machinery and a future implementer re-introduces the sibling/cousin/grandchild
> leak that the spec's strongest correction exists to prevent.

> **Consequence, stated plainly (revised).** **Subagent handle recovery is no longer "prospective
> only" — it works.** Rev 1's caveat existed solely because records minted before `ParentAgentID`
> shipped could never be attributed. With no pre-upgrade data by fiat *and* a fail-closed mint,
> every lifecycle record in existence carries a real parent. The only remaining limit on acting on
> a recovered handle is `actionable` (FR-011), which is about **process lifetime**, not
> attribution — a different and honestly-labelled constraint. This also settles the spec's own open
> question about whether the `subagent` kind survives ADR-056 D7: it does, **with a schema change
> alone and no stated limitation attached**.

### Boundedness and honesty

- **FR-016**: Every list MUST be bounded, and each live status group MUST have its **own reserved sub-bound** so no group can starve another: `queued = 25`, `running = 25`, `blocked = 25` (75 live rows maximum), terminal `= 20`. A group's unused budget MUST NOT be reallocated to another group — a reserved bound that can be borrowed is not a reservation. Terminal rows MUST be excluded from the response unless `include_terminal=true`, and `draft` plans excluded unless `include_drafts=true`.

  **`limit` MUST be allocated ACROSS the groups by round-robin, never applied as a tail-truncating total cap.** Concretely: after each group has been reduced to its sub-bound, rows are **selected** by taking one row at a time from each non-exhausted group in the FR-007 group order, repeating until `limit` is reached or every group is exhausted; within a group, rows are taken in that group's own FR-007 order. **Selection order is not emission order** — the selected rows are then emitted in FR-007's full sort order, so the response's shape is unchanged. The hard `limit` maximum is 200; the range above 75 is reachable only with `include_terminal=true`, and the tool description MUST say so.

> **⚠️ REV-3 CORRECTION (FR-016, `limit` × the sub-bounds — R2-MAJ-001, and a through-line case).**
> Rev 2 made `limit` a **total cap applied *after* the sub-bounds**, over a list sorted
> `queued → running → blocked`. A caller passing `limit=30` against 25 `queued` + 25 `running` + 3
> `blocked` therefore received 25 queued + 5 running and **zero blocked rows** — the sub-bound
> reserved them and the total cap removed them again, because `blocked` sorts **last** of the three
> live groups. A small `limit` is exactly what a context-conscious agent passes, and the tool
> description teaches it that the live maximum is 75.
>
> This matters more than operator ruling 3's deflation suggests: the rows deleted first are the
> `blocked` ones, and under FR-036 a `blocked` row may carry `attention="caller"` — a subagent
> waiting on an answer only this caller can give. *"Just information"* is true of
> `attention="none"`; it is not true of what `limit=30` was silently dropping.
>
> **And this is the spec's own instance of the through-line defect.** SC-016 did not catch it
> because SC-016 exercises the **default** call only — the one input under which the mechanism
> holds. Rev 3 extends SC-016 to assert the property under a caller-supplied `limit`, adds *Bounds
> and truncation* dataset row 20, and replaces the tail-truncating cap with round-robin allocation,
> which cannot starve a populated group by construction.
>
> Rev 2's two other `limit` clarifications stand: (i) `limit=200` still yields at most 75 live rows
> and reports **no** clamp (200 ≤ the hard maximum); (ii) starvation *within* the `queued` group
> from monotonically accumulating `draft` plans (A5) is closed by FR-007's intra-group rank plus
> `include_drafts`.

> **⚠️ REV-2: FR-016 is now the ONLY anti-starvation mechanism (operator ruling 1).** Rev 1 relied
> on two — the `blocked`-first reorder and these sub-bounds — and called them *"independently
> sufficient"*. The operator withdrew the reorder, so the sub-bounds carry the whole load and are
> stated as numbers rather than as a principle. Two consequences rev 1 left undefined are now
> fixed: (i) `limit`'s relationship to the sub-bounds (rev 1 never said whether `limit` was a total
> cap, a per-kind cap or a per-status-group cap, so `limit=200` against a 75-row live maximum told
> the caller nothing was clamped while delivering 37 % of the request); (ii) **starvation *within*
> the `queued` group** — abandoned `draft` plans accumulate monotonically (A5: plans are never
> swept), all normalize to `queued`, and 26 of them would evict every real cap-waiting plan from
> the roster. FR-007's intra-group rank plus `include_drafts` closes that.

- **FR-017**: Every omission MUST be reported with an exact count, in **both** key spaces and in total: `omitted: {by_kind: {plan, task, subagent}, by_status: {queued, running, blocked, failed, completed}}, total_omitted: N`. Both sub-objects MUST sum to `total_omitted`. The system MUST NOT silently cap. This covers bound-driven truncation; suppression by `include_terminal=false` is covered separately and mandatorily by **FR-031**. Exactness is subject to FR-032(d)'s scan ceiling — see the precedence rule there.

> **⚠️ REV-3 CORRECTION (FR-017, key space — R2-MAJ-002).** Rev 2 keyed `omitted` **by kind** in
> three normative places (FR-017, FR-033's enumeration, US-4 AS-1) and **by status group** in the
> only place that asserts values (SC-016 and its BDD scenario: *"`omitted` reports `375` for
> `queued`, `375` for `running`, and `0` for `blocked`"*). The two key spaces are not
> interchangeable and the fixture never said which kind the 800 jobs were, so **the one test that
> proves this spec's only anti-starvation mechanism could not be written from the requirements** —
> an implementer picking per-kind fails `TestBounds_PerStatusSubBounds`, and one picking per-group
> leaves FR-017 unimplemented. Rev 3 emits **both**, which is the honest shape: the sub-bounds are
> per status group and the per-kind error entries are per kind, so both key spaces are load-bearing
> somewhere in this spec.
- **FR-018**: A per-record read failure MUST cause that record to be skipped and counted (`unreadable`), never the abandonment of the kind. **This applies to all three kinds** — see FR-027, without which it is unobtainable for `plan` and `task`. A store-level failure MUST produce an explicit per-kind error entry while other kinds still return rows. When a per-kind error entry is present, any derived aggregate that depends on that kind MUST be marked or omitted rather than reported as if complete.
- **FR-019**: **Every free-text field on the row** MUST be passed through `config.Config.FilterSensitiveData` `[VERIFIED: pkg/config/config.go:393-403]` **before** truncation, then truncated to its FR-030 maximum on a rune boundary. The fields are, **exhaustively**: `label` and `native_status`. Any field added to the row later that carries store-sourced or runtime-sourced text MUST be added to this list in the same change — the enumeration is the contract, not the field name.

> **⚠️ REV-2 CORRECTION (FR-019 → CRIT-002).** Rev 1 applied redaction and truncation to `label`
> **only**, while making `native_status` REQUIRED and never omitted — and `native_status` is
> composed from **two unvalidated string sources**:
>
> | Source | Type | Validated? |
> |---|---|---|
> | `session.LifecycleRecord.FailedReason` | `string` | **No** — its own doc says *"Left open (not a closed enum)"* `[VERIFIED: pkg/session/lifecycle.go:236-239]` |
> | `plan.Plan.PausedReason` | `string` | **No** — a bare `string` never passed to any validator; only `!= ""` is ever tested `[VERIFIED: pkg/plan/plan.go:378; :122]` |
> | `plan.Plan.FailedReason` | `plan.FailedReason` | Yes — `IsValidFailedReason`, 4 values `[VERIFIED: plan.go:302-311]` |
> | `plan.PlanPhase` | `PlanPhase` | Yes — `validPlanPhases` |
>
> So two of the four composite sources are arbitrary strings written by the runtime — wrapped
> errors, filesystem paths, upstream API text. Rev 1's own BDD table listed `failed:interrupted`
> and `failed:judge_rounds_exhausted` as if the lifecycle side were a closed enum; it is not.
> Two consequences: **the redaction control was bypassable** (FR-019's entire justification —
> *"agent-authored free text landing in the same caller context and persisted transcript"* —
> applies verbatim to `native_status`, so a pause reason carrying a credential-bearing URL reached
> the caller's context and the persisted transcript unfiltered, and SC-011's "leaks zero
> substrings" guarantee was scoped to `label` and was therefore **not a guarantee about the
> response**); and **SC-005 was unenforceable**, since one wrapped error on one row is enough to
> blow a 32 KB budget that nothing but `labelMax` bounded.

- **FR-019a**: `FilterSensitiveData` has **two bypass gates** that this spec must account for: it returns content unchanged when `tools.filter_sensitive_data` is disabled, and when the content is shorter than `FilterMinLength` (**default 8**) — **measured in BYTES, not runes**: the gate is `if len(content) < c.Tools.GetFilterMinLength()`, and `len()` on a Go string counts bytes `[VERIFIED: pkg/config/config.go:398-401; GetFilterMinLength at :3265-3270]`. The system MUST NOT rely on the replacer alone for content shorter than that **byte** threshold, and MUST NOT weaken any field bound when the operator has disabled filtering. Tests MUST cover a **7-byte ASCII** secret-bearing label explicitly.

> **⚠️ REV-2 UNIT CORRECTION (FR-019a, MIN-001).** Rev 1 reasoned in **runes** where the code
> reasons in **bytes**, and specified dataset row 13 as a *"6-rune label that is a registered
> secret"*. A 6-rune CJK or emoji label is 18–24 bytes and **is** filtered; a 7-byte ASCII secret is
> **bypassed**. The test would therefore have passed or failed purely on the alphabet its author
> picked, never exercising the bypass it was written for. The unit change is deliberate: **bytes
> for the `FilterMinLength` gate** (matching the code), **runes for truncation** (where runes are
> correct, because splitting a rune produces invalid UTF-8). Do not "fix" either back.

> **⚠️ ADR CORRECTION (FR-019).** ADR-056 D7 makes redaction a **precondition** for the deferred
> `shell` kind — a raw command line is "a credential exfiltration path" — while FR-4's shipped
> labels (plan title, task title, agent name) are equally agent-authored free text landing in the
> same caller context and persisted transcript, and D6 requires only truncation. This spec applies
> D7's own standard uniformly (R2-MAJ-011). The ordering matters: truncating first can split a
> secret across the boundary and defeat the replacer.

- **FR-020**: The row ordering function MUST be **total** over any fixed input set: `(status group, started_at ASC for live groups / DESC for terminal groups (FR-007), kind ASC, id ASC)`, with `approved` ranked above `draft` inside `queued`. Given the same input set, it MUST produce the same permutation every time, regardless of input order and regardless of `sort.Slice`'s unspecified behaviour on equal elements. Implementations MUST use `sort.SliceStable` **or** a comparator that is provably total; a total comparator is preferred because it does not depend on input order at all.

> **⚠️ REV-2 CORRECTION (FR-020).** Rev 1 required *"byte-identical responses across calls"*. That
> is in tension with FR-024 by construction — byte-identity is only testable against a frozen
> store, which is not the state FR-024 describes — and it buys the caller nothing, since an agent
> does not diff two rosters. The property that actually matters, and which MAJ-007 requires anyway,
> is a **total deterministic ordering**. Rev 1 could not deliver even that: `started_at` is `""` for
> **every** `queued` row (approved/draft plans and inbox/next tasks have never started), so the
> whole group tied, and `sort.Slice` is **not stable** — `TestSortOrder_Deterministic` would have
> flaked in CI rather than failed in review, the worst kind of test. SC-010 is restated to match.

### Cost and contention

- **FR-021**: The system MUST NOT call `PlanEngine.Admit`, **and MUST NOT re-derive the active count from its own owner-scoped plan list.** Cap pressure MUST be read from the engine's own snapshot (FR-029) or not reported at all. `cap_max` MUST come from the same snapshot, not from a separate `config.PlanningConfig` read, so numerator and denominator are always from one observation.
- **FR-029** (**rewritten in rev 3 — R2-CRIT-003, R2-MAJ-009, cross-spec M3/M4**): `PlanEngine` MUST expose a **lock-free, read-only** accessor returning `(active int, cap int, reliable bool, observedAt time.Time, lastTickAt time.Time)`. Its mechanism, its bound and its dispositions are all stated numerically:

  **(a) Publication — one producer, inside the lock that already holds the value.** The snapshot MUST be published from **inside** `admitLocked`, which has already computed `active`, `capOut` and `reliable` under `pe.mu` `[VERIFIED: pkg/agent/plan_engine.go:2188-2203]`, via a single `atomic.Pointer[capSnapshot]` store. The reader MUST NOT take `pe.mu`. This adds **no new lock, no second scan, and no second derivation**: the published number is *by construction* the number `Admit` used.

  **(b) Liveness — a heartbeat, not a recount.** `Tick` MUST stamp a `lastTickAt` timestamp (a single atomic store of a `time.Time`) at the **top of `Tick`, before the `planStore.List` early return** `[VERIFIED: the early return is at pkg/agent/plan_engine.go:679-682 and precedes every other statement in the loop body]`. It MUST NOT recompute the active count.

  **(c) Staleness is a NUMBER, and it LABELS rather than suppresses.** The bound is `planning.cap_snapshot_staleness_seconds`, **default 90 s**. Derivation, stated so it is not re-guessed: the production tick is `defaultPlanEngineTickInterval = 30 * time.Second` — a **package const, not a config key** `[VERIFIED: pkg/agent/plan_engine.go:131]`; `Tick`'s `claimTick()` overlap guard makes the pass after a slow tick a silent no-op `[VERIFIED: :673-676]`, so two intervals is a *normal* worst case; 3× gives one interval of headroom for scheduler jitter. **A bound below 30 s would mark every snapshot stale on every call** and, under rev 2's suppress-on-stale rule, would have silently retired US-2 AS-5/6/7, SC-003 and the entire CRIT-001 fix — **with every test green**, because every cap-pressure test constructs an engine and Ticks it immediately. That is the through-line failure in its purest form and is why the number is in the requirement.

  **(d) Dispositions, exhaustively.** `list_jobs` MUST:

  | Condition | `cap_active` / `cap_max` | `cap_observed_at` | `engine_running` |
  |---|---|---|---|
  | No engine wired | **omitted (pair)** | omitted | omitted |
  | No snapshot published yet (engine has never admitted) | **omitted (pair)** | omitted | emitted |
  | `reliable == false` | **omitted (pair)** | omitted | emitted |
  | Snapshot present and `reliable`, `now − observedAt ≤ bound` | **emitted (pair), including `cap_active = 0`** | emitted | emitted |
  | Snapshot present and `reliable`, `now − observedAt > bound` | **emitted (pair), including `cap_active = 0`** | emitted | emitted |

  `cap_observed_at` MUST be emitted whenever the pair is emitted, so the caller can see the age rather than have it hidden. `engine_running` MUST be `false` when `now − lastTickAt > bound`. A partial or zeroed pair MUST NOT be emitted in any row of that table.

> **⚠️ REV-3 CORRECTION (FR-029) — three separate defects, one requirement.**
>
> **1. The staleness bound had no value (R2-CRIT-003).** Rev 2 required omission when `observedAt`
> is *"older than **a stated staleness bound**"*, and that bound was **stated nowhere in the
> document** — not in FR-029, not in the Success Criteria, and not in Ambiguity #2, which is this
> spec's own register of unresolved bound values and which listed every *other* number. There was
> no dataset row, no scenario and no test: the matrix mapped FR-029 to three tests covering the
> *global-count* and *unreliable* cases only.
>
> **2. Suppress-on-stale deleted the story it was written to serve.** The snapshot ages when the
> engine is not admitting. US-2 AS-5's premise is *"the plan engine is **not** admitting"*, and the
> Edge Case *"Engine stopped with approved plans present"* expects *"`queued` **plus** cap-pressure
> fields showing `active` far below `cap`"*. A stopped engine is **always** stale (a never-started
> one has a zero `observedAt`, which is maximally stale), so rev 2's rule omitted the answer in
> **exactly** the state the field exists for. Together with R2-CRIT-002's FR-033 enumeration, two
> independent rev-2 mechanisms each, alone, voided US-2 AS-5 — the spec specified code that could
> not pass its own tests. **Omitting is the one disposition that destroys the story**, so rev 3
> emits and labels instead.
>
> Note the reassuring interaction, stated so it is not mistaken for luck: because
> `tryStartApprovedPlan → Admit → admitLocked` runs on every tick that has an approved plan, the
> snapshot is **guaranteed fresh in AS-5's own case whenever the engine is running**. `engine_running
> = false` plus a visibly old `cap_observed_at` is what distinguishes *"stopped, nothing will ever
> start it"* from *"healthy queue, wait"* — which is the whole content of US-2 AS-5.
>
> **3. The cost justification was FALSE (R2-MAJ-009), and the alternative reading was worse.** Rev 2
> justified the refresh twice with *"`Tick` already performs an unfiltered
> `pe.planStore.List(plan.Filter{})` on every pass and does not hold `pe.mu` … so the refresh is
> marginal cost on work already being done."* Re-verified for rev 3 (details in the *Rev 3*
> section): `computeActiveLocked` has **exactly one caller in the repository** — `admitLocked` at
> `:2189`, under `pe.mu` — `Tick` never calls it, and it performs **its own** second `List` rather
> than reusing `Tick`'s slice. So "refreshed unconditionally on every `Tick`" means a **new `pe.mu`
> acquisition plus a second full store scan plus every `activeCounter` callback, on every tick,
> forever**, on installs where nothing needs admitting. The alternative reading — taking *"does not
> hold `pe.mu`"* literally and writing a **second, lock-free re-derivation** — is worse: it is a
> parallel number that can diverge from the authoritative one, i.e. *"substituted a number that is
> not the same number"*, the CRIT-001 defect relocated. **`TestPlanEngine_CapSnapshotIsLockFreeAndGlobal`
> would have passed either way**, because it asserted values under a controlled fixture rather than
> identity with `Admit`'s number. (a) removes both possibilities and SC-013 now asserts identity.
>
> **4. Cross-spec M3.** `Tick` **returns early** on a `planStore.List` error `[VERIFIED: :679-682]`,
> so under rev 2's design the snapshot was not refreshed at all on that path and `reliable` was
> never set false — the staleness bound was the only remaining guard, and it had no value. (b)'s
> heartbeat is stamped before that return precisely so `engine_running` stays truthful when `List`
> is failing.
>
> The accepted consequence of (a), stated plainly: **the snapshot refreshes only when admission
> runs.** That is the correct trade — admission is when the number changes — and it is why (b) and
> (c) exist to make the age visible rather than to hide it.

> **⚠️ REV-2 CORRECTION (FR-021 → CRIT-001). Rev 1's derivation inverted its own signal.** ADR-056
> D2 said cap pressure *"costs nothing extra… the data is free"*; rev 1 correctly rejected calling
> `Admit` (C5 — it takes `pe.mu` **exclusively** and re-scans the store
> `[VERIFIED: pkg/agent/plan_engine.go:2182-2186]`) and then **substituted a number that is not the
> same number.**
>
> ```go
> // pkg/agent/plan_engine.go:2221-2247 computeActiveLocked   [VERIFIED]
> runningPlans, err := pe.planStore.List(plan.Filter{})   // ← UNFILTERED: every workspace, every owner
> ...
> for kind, fn := range pe.activeCounters {               // ← /goal and /loop counts,
>     n, err := fn()                                      //   NOT in the plan store at all
>     count += n
> }
> ```
>
> `cap_max` (16, `DefaultGlobalActiveLoopCap` `[VERIFIED: pkg/config/planning.go:17]`) is a
> **global** brake shared across every agent, every workspace, and the `/goal` and `/loop`
> subsystems. Rev 1's numerator — the tool's own list, filtered by `WorkspaceID` **and**
> `OwnerAgentID` (FR-009, FR-010) — is a strict undercount missing three whole populations. The
> consequence is not "slightly off", it is **inverted**: agent A with one approved plan cap-waiting
> behind 16 running plans owned by others has the true state `active=16, cap=16` → *"healthy queue,
> wait"*, and rev 1 emits `cap_active=0, cap_max=16` → *"far below cap, nothing will ever start
> it"* → **A intervenes on healthy work**. The Evaluation Scenario *"The agent correctly waits
> rather than intervening"* asserts the exact opposite of what rev 1 produces, and
> `TestListJobs_CapPressureWithoutAdmit` — which asserted only that `Admit` was not called — passes
> while it does so.
>
> ~~FR-029 is cheap because `Tick` **already** performs an unfiltered
> `pe.planStore.List(plan.Filter{})` on every pass … so the refresh is marginal cost on work
> already being done.~~ **WITHDRAWN IN REV 3 — this sentence was false** (R2-MAJ-009). The citation
> resolved; the inference did not. `computeActiveLocked` has exactly one caller (`admitLocked`,
> under `pe.mu`), `Tick` never reaches it except via `Admit` when an approved plan exists, and it
> performs its **own** second `List` rather than reusing `Tick`'s slice `[VERIFIED — see FR-029 and
> the *Rev 3* section]`. FR-029 is cheap for a **different** reason, and the requirement now says
> which: the snapshot is published from **inside** the computation `admitLocked` already performs
> under the lock it already holds, via one `atomic.Pointer` store. One producer, no new lock, no
> second scan, and the reader is lock-free.
>
> What survives from rev 2 unchanged: FR-029 propagates `admitLocked`'s existing `reliable`
> fail-closed signal `[VERIFIED: plan_engine.go:2191-2203]` instead of discarding it — rev 1 had no
> equivalent, so FR-018's per-kind plan error would have left `cap_active` silently wrong with no
> marker.

- **FR-022**: When `kind` is supplied, the system MUST read only that kind's store. When `include_terminal=false`, the system MUST NOT **materialize** terminal rows into the response — **and MUST NOT be represented as avoiding store I/O.** Every kind's `List` loads every record regardless of filter; filtering trims the returned slice and saves nothing.

> **⚠️ REV-2 CORRECTION (FR-022).** Rev 1 required *"apply `NonTerminalOnly` at the filter level
> rather than loading and discarding terminal rows"*. That is false for **all three** stores, and
> for two of them it names a mechanism that does not exist:
>
> - `plan.Store.List` — `scanPlanIDs()` → `s.load(id)` for **every** id → `filter.matches(p)` `[VERIFIED: pkg/plan/store.go:161-170]`; `plan.Filter` has **no** `NonTerminalOnly` field `[VERIFIED: store.go:120-123]`
> - `task.Store.List` — identical shape `[VERIFIED: pkg/task/store.go:250-258]`; `task.Filter.Status` is a **single** `Status`, not a set `[VERIFIED: store.go:165]`, so "status ∈ {inbox, next, in_progress, blocked}" needs four calls or a post-filter
> - `session.LifecycleStore.List` — `scanSessionIDs()` → `s.Load(id)` **taking the per-session striped mutex for every session** `[VERIFIED: pkg/session/lifecycle.go:340-348, 570-589]` → `filter.matches(rec)`
>
> This propagated: US-4 AS-5's *"no terminal-store scan cost is paid"* was false and is deleted;
> A5's *"this spec accepts the read cost"* was accepting a cost mis-measured by the terminal:live
> ratio (the Evaluation Scenario posits 2 000 terminal to 500 live — a 5× understatement); and
> SC-005's scale claim rested on it. Read cost scales with **total** records, live and terminal, in
> a monotonically growing store. FR-032's per-call work bound is what actually bounds it.
- **FR-023**: `list_jobs` MUST resolve to `allow` for every non-system agent on **both** a fresh install and an upgraded installation, and to `deny` for System Agents unless explicitly enumerated. This requires **all** of: (a) `"list_jobs"` added to `coreagent.allStaticToolNames`; (b) `"list_jobs": "allow"` added to the global `DefaultConfig().Sandbox.ToolPolicies` seed; (c) `"list_jobs": allow` added to every `denyAllThenOverride` override map for a non-system agent — the four base agents, the seeded specialists, and `NewCustomAgentToolsCfg`; **(d)** registration in `tools.GeneralBuiltinMetadata()` (FR-001); **(e) (rev 3, cross-spec C2)** the tool-count assertion in `pkg/config/defaults_test.go` — today `const wantToolCount = 83` `[VERIFIED: pkg/config/defaults_test.go:92-96]`, which site (b) turns **red** on its own. Per **operator ruling 4** the literal MUST NOT be re-hardcoded: it MUST be **replaced** by the mechanical invariant `len(cfg.Sandbox.ToolPolicies) == len(coreagent.AllStaticToolNames())`, which is the same fact stated as a rule rather than as a number and cannot go stale on the next tool. See *Rev 3 → Landing order* for the arithmetic and for who edits the line first.

  **The `ask` verdict** (rev 3, R2-MIN-003): the policy vocabulary is `allow | ask | deny` and the compositor's precedence is `deny > ask > allow`, but rev 2 discussed only `allow` and `deny` across FR-023, US-7 and all 12 dataset rows. `ask` MUST be **supported and discouraged**: an operator who sets `list_jobs: ask` globally makes every call block on a human prompt, and for an autonomous background agent recovering a handle mid-turn that is a **hang, not a prompt**. The runbook (FR-032(e)) MUST say so and MUST name the global `deny` as the correct kill switch instead. A dataset row covers it. **The supported operator kill switch is a global `sandbox.tool_policies` entry `"list_jobs": "deny"`**, which wins over the per-agent seeded `allow` by the deny-wins rule; this MUST be documented in the tool description and in the operator runbook (FR-032), not merely exercised as a test case. Under god-mode (sandbox `off`) `list_jobs` resolves `allow` for **every** agent including System Agents, because the compositor short-circuits before either map is consulted `[VERIFIED: pkg/tools/compositor.go:175-177]`; the kill switch does not apply in that mode and the runbook MUST say so.

> **⚠️ ADR CORRECTION (FR-023).** ADR-056 D8 says "seeded `allow` for all non-system agents" and
> §9 step 3 lists **one** implementation site. Both are wrong in opposite directions. (i) On a
> **fresh** install, base and custom agents are built by `denyAllThenOverride`, which stamps an
> explicit `deny` for every catalog name — and a per-agent `deny` **beats** a global `allow`. So
> without (c), fresh installs ship the tool **disabled** (C6, previously unreported by either
> review). (ii) On an **upgrade**, R2-CRIT-003 predicted a persisted `deny` backfill. Verified: that
> fires only if (b) is missed, because `loadConfig` merges the default global map into the on-disk
> config and `ValidateToolPolicyCoverage` short-circuits on a global entry (C7, empirically
> confirmed). **No bespoke migration is needed** — but (b) is load-bearing, not optional.

- **FR-024**: The system MUST NOT mutate any store, and MUST tolerate concurrent mutation during its read without erroring — the roster is a best-effort near-snapshot, not a transactional one. This MUST be stated in the tool description, **verified by `TestListJobs_DescriptionContract`** (rev 1 traced this clause to `TestListJobs_ReadOnly`, a directory byte-identity test that asserts on no string). A response produced across a concurrent mutation MUST be **well-formed**, not merely non-erroring: every row satisfies FR-003 and the counters are arithmetically self-consistent.

### Contract

- **FR-025**: `list_jobs` MUST NOT introduce any change to `contracts/openapi.yaml`, `contracts/asyncapi.yaml` or `contracts/components/schemas/`. Its row and response types are **tool-result shapes returned to an LLM**, not bytes crossing the gateway/SPA boundary, and `ParentAgentID` is disk-only (FR-013). The release gate is therefore that `make verify-contracts` stays **green with no drift** — i.e. that no contract change was made — not that a regeneration was performed.

> **⚠️ REV-2 CORRECTION (FR-025).** Rev 1 required the row and response types to be *"defined in
> `contracts/components/schemas/` and generated … before any Go consumes them"*. Constraint #8
> governs *"every byte crossing the gateway/SPA boundary (REST req/resp, WS frame, persisted JSON
> the SPA reads)"*. A `ToolResult` string returned to an LLM is **none of the three**, and A6
> explicitly puts REST parity and SPA work out of scope. Rev 1's SC-014 then gated the release on a
> contract change that need not exist. **If and when the REST parity endpoint of ADR-056 §9 step 4
> is built, Constraint #8 applies to it in full** — that is a separate change with a separate spec.

### New requirements introduced by rev 2

> **Numbering note.** Rev 2 appends FR-026…FR-035 rather than renumbering, so every existing
> cross-reference in this spec, in ADR-056 and in the reviews stays valid. Two of the new ones —
> **FR-028** (delegate batch accessor) and **FR-029** (plan-engine cap snapshot) — are stated
> **above**, immediately beside the requirements they fix (FR-011 and FR-021), because reading
> either in isolation would be misleading. They are not repeated here.
>
> **Rev 3 follows the same convention.** It appends **FR-036** (`attention`) and **FR-037**
> (`task.Task.CreatedByAgentID`), and adds four lettered sub-requirements — **FR-006a** (compose
> `native_status` from exported constants), **FR-006b** (unmappable native state), **FR-012a**
> (`attention` in the tool description) and **FR-012b** (the description's size bound) — beside the
> requirements they qualify, for the same reason. **FR-036 and FR-037 are also stated above**, next
> to FR-006 and FR-010 respectively. Total: **42** requirements. Nothing is renumbered and nothing
> is deleted; FR-032(c) survives as a **prohibition** rather than being removed, so the review's
> finding stays anchored to a live requirement instead of to a gap.

- **FR-026**: `task.Filter` MUST gain `PlanIDSet bool`, matching the existing `ParentTaskIDSet` convention `[VERIFIED: pkg/task/store.go:171-174]` — when true, the `PlanID` filter applies even when `PlanID` is empty, i.e. "standalone tasks only". Without it, FR-010's `PlanID == ""` predicate is **inexpressible**: `matches` treats `PlanID == ""` as filter off `[VERIFIED: store.go:196]`, so an implementer would either invent an unrequired field or post-filter — and post-filtering after bounds corrupts FR-017's omission arithmetic. `PlanIDSet=false` MUST preserve today's behaviour exactly (regression test required).
- **FR-027**: `plan.Store.List` and `task.Store.List` MUST each gain a **lenient sibling** mirroring `session.LifecycleStore.ListLenient`, returning `(records, skipped int, err error)`. Today both **skip and swallow** the count — `slog.Warn(…); continue` `[VERIFIED: pkg/plan/store.go:163-167; pkg/task/store.go:254-255]` — so FR-018's `unreadable` would ship permanently pinned at **0** for two of the three kinds while US-5's premise is that *"a short list that looks complete is the worst possible output."* The existing strict `List` on both stores MUST be left untouched.
- **FR-030** (**rewritten in rev 3 — R2-MAJ-005**): Every free-text row field MUST have **two** stated maxima, and truncation MUST satisfy **both**:

  | Field | Rune max (truncation boundary) | Serialized-byte max |
  |---|---|---|
  | `label` | `labelMaxRunes = 120` | `labelMaxBytes = 480` |
  | `native_status` | `nativeStatusMaxRunes = 200` | `nativeStatusMaxBytes = 800` |

  Truncation MUST be on a **rune** boundary (splitting a rune produces invalid UTF-8), and MUST reduce the value until **both** `runes ≤ runeMax` **and** `len(JSON-encoded value, excluding the surrounding quotes) ≤ byteMax` hold. Measuring the **encoded** length is what makes the bound true in the presence of JSON escaping (`\"`, `\\`, `\uXXXX`), rather than approximately true.

  The response-size identity, with every constant given a value so it is evaluable:

  ```
  maxRows            = 95      # 75 live (FR-016: 25+25+25) + 20 terminal
  fixedRowOverhead   = 512     # the 10 non-free-text fields + keys + JSON punctuation
  envelopeOverhead   = 2048    # notes object, cap fields, workspace flags, wrapper
  maxResponseBytes   = maxRows × (labelMaxBytes + nativeStatusMaxBytes + fixedRowOverhead)
                       + envelopeOverhead
                     = 95 × (480 + 800 + 512) + 2048
                     = 172 288 bytes
  ```

  `TestListJobs_ResponseSizeBound` MUST measure `len(result.Content)` — bytes — against `maxResponseBytes`, and MUST run **twice**: once over a 4-byte-rune (emoji/CJK) corpus and once over an ASCII mirror.

> **⚠️ REV-3 CORRECTION (FR-030/SC-005) — the unit error FR-019a corrected, one requirement later.**
> Rev 2 set the maxima in **runes** and then required the response bound to be the arithmetic
> identity `maxRows × (labelMax + nativeStatusMax + fixedRowOverhead) + envelopeOverhead`, while
> SC-005 and test 33b measure *"the serialized response length"* / `len(result.Content)` — **bytes**.
> A 120-rune label of CJK or emoji is 360–480 bytes, so the identity understated the true maximum by
> up to 4× before JSON escaping inflated it further. Dataset row 19's fixture (*"10 000-rune
> labels"*) never stated the alphabet, so **the test passed or failed on the author's choice of
> characters** — precisely the defect FR-019a's own ⚠️ block identifies and corrects for
> `FilterMinLength`, reintroduced one requirement later. Separately, `maxRows`, `fixedRowOverhead`
> and `envelopeOverhead` were **never given values anywhere**, so a release-gate criterion was not
> evaluable at all and whoever wrote the test would have chosen the numbers that made it pass.
>
> The unit split is deliberate and mirrors FR-019a's: **bytes** where the machine counts bytes (the
> size bound, the `FilterMinLength` gate), **runes** where runes are correct (the truncation
> boundary). Do not "fix" either back.
- **FR-031**: On **every** response where `include_terminal=false`, the system MUST report `terminal_suppressed` — an exact count, per kind and in total, of terminal rows that exist for the caller and were withheld. It MUST be populated from a count that does not materialize the rows, and it MUST be omitted only when it is zero (FR-033). A default-argument call after a restart MUST therefore be **distinguishable** from a call by a caller with genuinely no background work. **Rev 3:** exactness is suspended for any kind carrying a `scan_truncated` entry, where the count is a **lower bound** — see FR-032(d)'s precedence rule. That is the only exemption, and it is always marked.
- **FR-032** (**(a), (c) and (d) rewritten in rev 3**): **Operability.**

  **(a) The security record and the debugging aid are two different things and MUST be emitted separately.**
  - **The audit record (this is the security control).** Every call MUST write **exactly one** `audit.Entry` — `Event: audit.EventToolCall`, `Tool: "list_jobs"`, `Decision: audit.DecisionAllow` on success / `audit.DecisionError` on a fail-closed principal rejection, `AgentID` = the resolved principal — with `Details` carrying `workspace_id`, `workspace_scoped`, the kinds read and the row count. It MUST be written **unconditionally**, at no log level, through the `pkg/audit` logger injected by the registry's `auditLoggerAware` contract (`SetAuditLogger`), following the in-tree `RememberTool` precedent `[VERIFIED: pkg/tools/memory.go:110-125, 240-267 constructs an `audit.Entry` and calls `t.auditLogger.Log`; `pkg/audit/audit.go:228-251` is the `Entry` shape; `EventToolCall`/`DecisionAllow`/`DecisionError` at `:42-89`]`. A nil logger MUST be a best-effort no-op, never an error — matching the same precedent.
  - **The Debug slog line (this is the debugging aid).** The diagnostic counters — `total_omitted`, `omitted` (both key spaces), `terminal_suppressed`, `unreadable`, `scan_truncated`, `limit_clamped_to` — MAY be emitted as one structured Debug entry. It is explicitly **not** the forensic trail.
  - Both MUST carry only FR-019-redacted values, and **neither MUST carry a `label` or a `native_status`** — the audit log is persisted and tamper-evident, and job titles are the thing US-3 protects.

  **(b)** Any non-zero `unreadable`, any per-kind error entry, or any `scan_truncated` entry MUST additionally emit at **Warn** naming the kind, so a degrading install is visible to an operator without a caller reporting it.

  **(c) The system MUST NOT memoize, cache or otherwise reuse a previous call's roster.** Every call MUST read the stores. See the ⚠️ block below for why this is a prohibition rather than an optimisation, and `TestListJobs_NoCrossScopeReuse` for the test that keeps it true.

  **(d) Hard per-call work bound.** The system MUST enforce a maximum number of records **loaded** per kind per call: `tools.list_jobs.max_records_scanned_per_kind`, **default 5 000**. Ids are consumed in the store's deterministic scan order so the truncation point is reproducible. When the ceiling is hit for a kind, the response MUST carry `notes.scan_truncated: {<kind>: {scanned: N, present: M}}`, where `present` is the **directory entry count** — the one quantity obtainable past the ceiling without loading anything.

  **Precedence, stated so it cannot be resolved by accident:** when `scan_truncated` is present for a kind, that kind's `omitted`, `unreadable` and `terminal_suppressed` are **LOWER BOUNDS, not exact counts**, and FR-017's, FR-018's and FR-031's exactness requirements are suspended for that kind alone. The `scan_truncated` entry is the marker that says so; a response without it is exact. SC-006 and SC-017 are scoped to below-ceiling populations, and SC-021 covers the above-ceiling case.

  **(e)** The operator documentation MUST include a runbook covering: what a non-zero `unreadable` means and what to do about it; what `scan_truncated` means and how to raise the ceiling; the FR-023 global-`deny` kill switch **and the fact that it does not apply under god-mode**; the `ask` verdict's behaviour (FR-023); and FR-015's `tools.delegate.require_parent_agent_id` rollback procedure. Per FR-012b this material lives **here, not in `Description()`**.

> **⚠️ REV-3 CORRECTION (FR-032(c)) — the memo is REMOVED, not re-keyed.** Rev 2 required *"a
> **per-principal memo with a 2–5 s TTL**"* and never defined the key. The BDD scenario (*"the
> second call performs **zero** store scans"*) and the test description (*"Second call within TTL
> does zero scans"*) both reinforced the narrow reading, so a principal-keyed memo was not merely
> permitted — it was what the spec, the scenario and the test all described. Three consequences,
> none of them addressed anywhere in rev 2:
>
> 1. **Cross-workspace disclosure against the P0 scoping control.** FR-009 makes `ToolWorkspaceID(ctx)`
>    fail **open**: a workspace-less turn returns the caller's rows across *every* workspace with
>    `workspace_scoped=false`. Agents are bound to different workspaces on different channels and
>    the same agent id calls the tool from both. A workspace-less call at `t=0` populates the memo
>    with a cross-workspace roster; a W1-bound call at `t=1.5 s` gets it back verbatim — W2 plan
>    titles, W2 task titles, W2 handles, and `workspace_scoped=false` on a turn that **was** scoped.
>    That violates US-3 AS-4, FR-009 and SC-004, and the transcript it lands in is persisted.
> 2. **Every narrowed call returns the wrong roster.** `kind`, `status`, `include_terminal`,
>    `include_drafts` and `limit` all change the response; within the TTL the memo returns the
>    previous one regardless. `list_jobs(kind="plan")` would return task and subagent rows,
>    contradicting FR-022 outright — and it **breaks the CRIT-003 recovery path rev 2 added**, whose
>    BDD scenario ends *"the caller can recover the three rows by re-calling with
>    `include_terminal=true`"*: an agent doing exactly that within 2–5 s receives the memoized empty
>    roster and concludes the work is gone.
> 3. **Audit hole.** FR-032(a) requires one entry per call carrying counters derived from the scan.
>    On a memo hit there is no scan, so the forensic trail US-8 exists to provide has 2–5 s gaps
>    under precisely the repeated-probing pattern the *"Cross-agent probing under adversarial
>    prompting"* evaluation scenario describes.
>
> **Why removal rather than an argument-keyed memo.** Keying on the full argument set fixes 1 and 2
> but not the reason the memo was adopted: an agent varying `limit` bypasses it trivially, so it is
> **not a DoS control** and never was. FR-032(d) is the real cost control, and it now has a number,
> a key and a test. Shipping a cache that neither bounds cost nor preserves scoping is strictly
> worse than shipping neither. Rev 2 adopted (c) **verbatim from a reviewer's shorthand without
> deriving its key** — the same failure mode as (d) being adopted without a value — which is the
> pattern the round-2 review named and this pass exists to correct.
>
> Removal also dissolves R2-MIN-005: a per-principal cache read and written by concurrent tool calls
> (test 39 runs 8 goroutines under `-race`) had **no stated lock, no eviction policy and no bound on
> retained principals**, in a spec that states FR-028's contention budget to the acquisition.
>
> **The scenario and the test are re-pointed, not deleted.** `TestListJobs_NoCrossScopeReuse`
> asserts the property a memo would break — a call reflects its own scope and its own arguments,
> back-to-back with a differently-scoped one — so reintroducing a cache fails the suite instead of
> silently passing it. Deleting the test would have left the hole unguarded, which is how this class
> of defect returns.

> **⚠️ REV-3 CORRECTION (FR-032(d)) — a bound with no number bounds nothing (R2-CRIT-004).** Rev 2
> required *"a **hard per-call work bound** — a maximum number of records scanned per kind,
> configurable — with overflow reported through the existing omission counters"*. **No default
> value. No config key name. Not in Ambiguity #2's register of bound values. No BDD scenario, no
> dataset row and no test** — the matrix mapped FR-032 to three tests, none of which touched (d).
>
> And *"reported through the existing omission counters"* was not achievable. Directory entries can
> be counted cheaply, but every other counter requires **loading** the record: `terminal_suppressed`
> is *"an exact count … of terminal rows that **exist for the caller**"* and both terminality and
> ownership live inside the file; `unreadable` counts records that failed to parse, and an unscanned
> record is not known to be readable or not; per-kind `omitted` is owner-filtered, and unscanned ids
> cannot be attributed to the caller. So on the first install exceeding the ceiling, SC-017's
> *"`terminal_suppressed = N` in 100% of trials"* becomes false, FR-017's *"exact count"* becomes
> false, and the post-restart response reverts to under-reporting — **the CRIT-003 defect rev 2
> exists to close, arriving through the requirement added to bound cost.** A5 states the stores grow
> monotonically and are never swept, so exceeding the ceiling is the **steady state**, not the
> exception.
>
> Rev 3 takes the reviewer's disposition (ii) — the ceiling wins, and honesty is preserved by an
> explicit marker rather than by pretending the counts are still exact. `scan_truncated` is built on
> the directory entry count because that is the one number that survives the ceiling, and it makes
> the degradation **visible per kind** instead of leaving "bounded scan" and "complete scan"
> indistinguishable.

> **⚠️ REV-2 (FR-032).** Rev 1 had **no operability section at all**. Concretely: no audit entry on
> a P0 security boundary that enumerates labels and steerable handles (so a cross-agent scoping bug
> would leave no forensic trail — the "Cross-agent probing under adversarial prompting" evaluation
> scenario had nothing to read after the fact); no metrics (every counter went to the *caller*
> only, so an operator could not learn that 40 % of an install's lifecycle records are corrupt);
> no documented kill switch; and no runbook. Separately, rev 1 had **no caching, no rate limit and
> no cost bound** on a read whose own premise — *"its context window was trimmed, or a wake started
> a fresh turn"* — is a **per-turn** condition, and whose description would actively tell agents
> this is how to recover a handle. Per call it performs a full plan-directory scan loading every
> plan file, a full task-directory scan loading every task file, and a full lifecycle scan taking
> **one per-session striped mutex per session** — the same lock live delegations need for their own
> state transitions. Rev 1's Ambiguity #5 named this and deferred it to *"rely on SC-012 to catch
> regressions"*, but SC-012 had no implementing test, and a 3-second 8-goroutine probe on a fresh
> temp-dir store measures nothing about a two-year-old install with 50 000 lifecycle files.

- **FR-033** (**rewritten in rev 3 — R2-CRIT-002, R2-MAJ-003, R2-MAJ-004**): Every **diagnostic counter** MUST be **omitted when zero or not applicable**, gathered under a single `notes` object. The enumeration is exhaustive: `total_omitted`, `omitted` (`by_kind` and `by_status`), `unreadable`, per-kind error entries, `terminal_suppressed`, `limit_clamped_to`, `scan_truncated`, `unmapped`, `ignored_args`.

  **`notes` is the always-present field.** It MUST be present on every response and MUST be `null` when nothing is non-nominal, so the caller distinguishes *"nothing to report"* (`"notes": null`) from *"field missing"* (a malformed response). No separate marker exists. The convention MUST be documented — in the operator runbook per FR-012b, and in one clause of `Description()`.

  **The cap pair is STATE, not a diagnostic, and is explicitly carved out.** `cap_active`, `cap_max`, `cap_observed_at` and `engine_running` are top-level response fields, **not** members of `notes`, and the omit-when-zero rule **does not apply to them**. They are emitted as a pair whenever FR-029(d) says to emit them — **including `cap_active = 0`, which is load-bearing** — and omitted as a pair only in FR-029(d)'s omission rows. `attention` (FR-036) is likewise state, is per-row, and is never omitted.

> **⚠️ REV-3 CORRECTION (FR-033) — three defects.**
>
> **1. It deleted the signal FR-029 exists to send (R2-CRIT-002).** Rev 2's enumeration explicitly
> included **`cap_active`/`cap_max`** among the omit-when-zero counters. But `cap_active = 0` is not
> a nominal-state absence — it is the **entire content** of US-2 AS-5 (*"the response carries
> `cap_active=0` and `cap_max=16` — so 'nothing will ever start it' is distinguishable from 'waiting
> for a slot'"*), of SC-003's first clause, of the BDD scenario *"Cap pressure distinguishes a real
> queue from a stopped engine"*, and of test 32, which rev 2 **specifically strengthened** to assert
> the emitted values `0`/`16`. Under rev 2's own FR-033 those fields are absent, the test fails, and
> `cap_active=0` becomes indistinguishable from *"unreliable snapshot"* (dataset row 11) and *"no
> engine wired"* (row 12), both of which also omit the pair. The one number that separates *"a dead
> engine will never start my plan"* from *"a healthy queue, wait"* was deleted by a context-saving
> rule — and the agent then waits forever on work nothing will start, the failure mode US-2 was
> written to prevent, arriving through the requirement added to fix a different one.
>
> **2. It was never propagated to the scenarios and datasets it contradicts (R2-MAJ-003).** Rev 2
> required `total_omitted` omitted when zero, and US-1 AS-4 agreed — but the BDD scenario AS-4
> traces to still asserted *"the roster has zero rows, **`total_omitted = 0`**, and no error
> entries"*, and *Bounds and truncation* dataset rows 1–3 all specified `total_omitted=0` as an
> expected **output**. `TestListJobs_EmptyRosterIsSuccess`, which the matrix maps to both FR-002 and
> FR-033, was specified twice with opposite expectations. Rev 3 rewrites the scenario and rows 1–3.
>
> **3. The always-present field was mandated and never named (R2-MAJ-004).** *"Exactly **one**
> always-present field MUST remain"* — and no requirement, scenario or test ever said which one, so
> the test could not be written and two implementers would produce two different shapes for **the
> most common case**. Naming `notes` costs nothing and needs no extra field.
- **FR-034** (**selection rule added in rev 3 — R2-MAJ-018**): A row MUST represent the **newest generation** of a session, by an explicit rule: **when multiple lifecycle records share a `session_id`, only the record with the highest `generation` is emitted — in every argument combination — and superseded generations are neither emitted nor counted in `terminal_suppressed`, `omitted` or `unreadable`.** Rev 2 required "newest generation" and never said how older ones are excluded, which mattered because the store's own invariant is that a terminal record is never mutated: `follow_up`/Play mint a **new** record. If prior generations are separate records they are terminal and would legitimately appear under `include_terminal=true` — the argument the CRIT-003 recovery path tells agents to use — so a resumed session showed N rows or 1, undecided. `TestListJobs_GenerationIsNewestOnly` MUST run **both with and without** `include_terminal`. `ParentAgentID` MUST be carried forward on **every** generation mint — `follow_up`/Play mint a **new** record with a new `generation` linked by `resumed_from` rather than mutating a terminal record, and `Persist` enforces that invariant `[VERIFIED: pkg/session/lifecycle.go:187-188 + the immutable-terminal rule]`. The row MUST carry `generation` when > 0. Today `delegate.go:942` is the only `LifecycleRecord` construction site in non-test code, so this is latent — but ADR-053's `follow_up`/Play path is exactly what will change that, and a generation mint that drops `ParentAgentID` would silently orphan the session from its parent's roster.
- **FR-035**: The tool description MUST state that an `id` is meaningful **only paired with its `kind`**. `(kind, id)` is the row identity and a plan id may collide with a task id without ambiguity — but FR-004 hands `id` back as a standalone handle, so an agent quoting one to a user or storing one across a context trim would lose the kind and reintroduce the ambiguity. The alternative (prefixing ids as `plan:abc`) is **deliberately not chosen**, because it would require the three action tools to accept the prefixed form and A7 keeps them unchanged.

---

## Response Shape (normative — rev 3, R2-MAJ-013)

> **These two examples are NORMATIVE.** Every requirement, BDD scenario and success criterion in
> this document that says *"the response carries X"* means **X in the position shown here**. Where
> prose and these examples disagree, these examples win.

**Why this section exists.** Rev 2 defined the response across at least seven places — FR-003 (row
fields), FR-009 (`workspace_scoped`), FR-021/FR-029 (the cap fields), FR-031
(`terminal_suppressed`), FR-033 (a `notes` object, named once and never given a shape), FR-002
(`limit_clamped_to`), FR-018 (per-kind error entries) — while FR-025 correctly forbids putting any
of it in `contracts/`. So **no artifact defined the response at all**, and rev 2 contained no
example. Meanwhile some requirements spoke of the same counters as top-level fields (*"the response
carries `cap_active=0`"*) and FR-033 gathered them under `notes`. Every *"the response carries X"*
assertion was therefore ambiguous about where X lives, two implementers would produce two
incompatible shapes, and **the LLM-facing contract — the actual product — was specified nowhere.**
This costs half a page and removes a dozen ambiguities at once.

### Nominal response

```json
{
  "workspace_scoped": true,
  "cap_active": 3,
  "cap_max": 16,
  "cap_observed_at": "2026-07-27T09:14:02Z",
  "engine_running": true,
  "rows": [
    {
      "kind": "plan",
      "id": "pln_7f3a",
      "label": "Migrate the audit chain to HMAC",
      "status": "running",
      "native_status": "running/dispatching",
      "relation": "runs",
      "attention": "none",
      "started_at": "2026-07-27T08:02:11Z",
      "last_activity_at": "2026-07-27T09:13:40Z",
      "workspace_id": "ws_core",
      "actionable": true,
      "intentionally_stopped": false
    },
    {
      "kind": "subagent",
      "id": "ses_91cc",
      "label": "Ray",
      "status": "blocked",
      "native_status": "needs_input",
      "relation": "dispatched",
      "attention": "caller",
      "started_at": "2026-07-27T09:01:00Z",
      "last_activity_at": "2026-07-27T09:12:55Z",
      "workspace_id": "ws_core",
      "actionable": true,
      "intentionally_stopped": false
    }
  ],
  "notes": null
}
```

Note what is **absent**: no `total_omitted`, no `omitted`, no `unreadable`, no
`terminal_suppressed`, no `limit_clamped_to` and no per-kind errors. `notes` is present and
`null`: that is the always-present field FR-033 requires, and the only way a caller distinguishes
*"nothing to report"* from a malformed response. Note what is **present**: `workspace_id` on every
row even though the roster is scoped (FR-003 declines R2-OBS-003), and `attention` on every row even
though both are `none` — both are state, and state is not subject to omit-when-zero.

### Degraded response

Truncation, one unreadable record, a per-kind store error, suppressed terminals, a clamped `limit`,
a scan ceiling hit, an unmapped native state, an ignored argument, an unreliable cap snapshot, and
a workspace-less turn — all at once, so every field's position is fixed by example:

```json
{
  "workspace_scoped": false,
  "engine_running": true,
  "rows": [
    {
      "kind": "task",
      "id": "tsk_04b1",
      "label": "Reconcile the ledger export",
      "status": "blocked",
      "native_status": "blocked",
      "relation": "runs",
      "attention": "none",
      "started_at": "2026-07-26T22:41:09Z",
      "last_activity_at": "2026-07-27T07:55:03Z",
      "workspace_id": "ws_ops",
      "actionable": true,
      "intentionally_stopped": false
    },
    {
      "kind": "plan",
      "id": "pln_c22e",
      "label": "Ship the supervisor",
      "status": "blocked",
      "native_status": "running/awaiting_supervision",
      "relation": "runs",
      "attention": "elsewhere",
      "started_at": "2026-07-25T11:00:00Z",
      "last_activity_at": "2026-07-27T09:10:00Z",
      "workspace_id": "ws_core",
      "actionable": true,
      "intentionally_stopped": false
    }
  ],
  "notes": {
    "total_omitted": 812,
    "omitted": {
      "by_kind":   { "plan": 400, "task": 412 },
      "by_status": { "queued": 400, "running": 412 }
    },
    "unreadable":          { "subagent": 1 },
    "terminal_suppressed": { "plan": 3, "subagent": 2, "total": 5 },
    "unmapped":            { "plan": 1 },
    "scan_truncated":      { "task": { "scanned": 5000, "present": 11842 } },
    "limit_clamped_to": 200,
    "ignored_args": ["relation"],
    "errors": [
      { "kind": "subagent", "message": "lifecycle store: open sessions dir: permission denied" }
    ]
  }
}
```

**Reading rules, normative:**

1. **`notes` is the only diagnostic container.** Every counter lives inside it. A counter absent from
   `notes` is zero (FR-033). `notes: null` means every counter is zero.
2. **`cap_active`/`cap_max`/`cap_observed_at` are TOP-LEVEL, never inside `notes`** — they are state,
   not diagnostics (FR-033's carve-out). Here they are **absent as a set** because the snapshot was
   unreliable (FR-029(d)); `engine_running` is still emitted, because it comes from the tick
   heartbeat, not from the snapshot.
3. **`workspace_scoped` and `engine_running` are top-level and always present.**
4. **`attention` is per-row and always present**, `none` for every non-`blocked` row.
5. **`workspace_id` is per-row and always present**, in both scoping modes (FR-003 — R2-OBS-003's suggestion to drop it when scoped is declined, with reasons, there).
6. `notes.omitted.by_kind` and `notes.omitted.by_status` MUST each sum to `notes.total_omitted`
   (FR-017). `notes.terminal_suppressed.total` MUST equal the sum of its per-kind entries.
   **Exception, and the only one:** any kind named in `notes.scan_truncated` reports **lower
   bounds**, so its contributions to those sums are lower bounds too (FR-032(d)).

---

## Success Criteria

- **SC-001**: An agent that has discarded a handle for **live** work recovers it in exactly one `list_jobs` call, for all three kinds, in 100% of trials across 20 runs.
- **SC-002**: A plan with `plan_phase ∈ {stalled, awaiting_supervision}` reports `status="blocked"` in 100% of cases and **never** `running`; `stalled` reports `attention="caller"` and `awaiting_supervision` reports `attention="elsewhere"`, in 100% of cases. Every phase substring in the emitted `native_status` is **byte-equal to its exported `plan.PlanPhase` constant** (FR-006a). (Rev 3: the old *"asserted at both small (5 rows) and large (500 rows) roster sizes"* clause is **dropped** — the mapping is size-independent by construction, no named test ever built a 500-row fixture, and the clause therefore constrained nothing (R2-MIN-006). The constant-identity clause replaces it because that is the property that can actually regress, and it is what cross-spec C1 would otherwise break silently.)
- **SC-003**: A plan in `state=approved` reports `status="queued"`, distinct from `running`. Three cases, all at 100%: (i) with 14 running plans owned by **other** agents in **other** workspaces plus a registered `activeCounter` reporting 2, the response reports `cap_active=16, cap_max=16` — **not** `0/16`; (ii) when the engine's last computation was `reliable=false`, **both** cap fields are absent; (iii) **with the engine STOPPED and an approved plan present** — the case FR-029's rev-2 staleness rule suppressed — the response reports `cap_active=0`, `cap_max=16`, a **present** `cap_observed_at` older than the 90 s bound, and `engine_running=false`. Clause (iii) is the release gate for R2-CRIT-002 and R2-CRIT-003 together: it fails if either the FR-033 omit-when-zero rule or the suppress-on-stale rule is reintroduced.
- **SC-004**: A context with an empty or whitespace-only agent id returns an **error** with exactly **0** rows in 100% of trials. Cross-agent visibility between two populated agents is exactly **0** rows in both directions — **including on a workspace-less turn**, where the response additionally carries `workspace_scoped=false` in 100% of trials.
- **SC-005**: The serialized response length in **bytes** never exceeds FR-030's `maxResponseBytes = 172 288`, measured directly by `TestListJobs_ResponseSizeBound` with 500 live jobs, 10 000-rune labels and 4 000-rune `native_status` values — run **twice**, once over a **4-byte-rune** corpus (emoji/CJK) and once over an **ASCII mirror**, with both runs asserting the same bound. Every omission is reported with a count summing to the true total, exact except for kinds carrying `scan_truncated` (SC-021). (Rev 3: rev 2's identity mixed runes and bytes and left `maxRows`, `fixedRowOverhead` and `envelopeOverhead` without values, so the criterion was unevaluable and the fixture's alphabet decided pass/fail — R2-MAJ-005. Rev 1's flat 32 KB was worse still.)
- **SC-006**: With 1 corrupt record among 400 — a population **below** FR-032(d)'s 5 000-record ceiling, so the exactness guarantee applies — `list_jobs` returns 399 rows and `unreadable=1`, not 0 rows, **for each of the three kinds independently** (`plan`, `task` and `subagent`), not only `subagent`. The above-ceiling case is SC-021's, not this one.
- **SC-007**: Booting from a `config.json` that contains no `list_jobs` key anywhere yields effective policy `allow` for every non-system agent, and `RepairIncompleteToolPolicyCoverage` reports **0** backfilled `list_jobs` entries.
- **SC-008**: On a fresh install, effective policy for `list_jobs` is `allow` for Mia, Jim, Ava, Ray, Worker, every seeded specialist and every newly created custom agent; and `deny` for every System Agent. **Under god-mode (sandbox `off`) it is `allow` for every agent including System Agents and including the both-maps-absent case**, because `if cfg.GodMode { return ToolPolicyAllow }` short-circuits before either map is read `[VERIFIED: pkg/tools/compositor.go:175-177]`. **With a global `sandbox.tool_policies` entry `"list_jobs": "deny"` and god-mode off, it is `deny` for every agent** — the supported kill switch.
- **SC-009**: A subagent row surviving a process restart reports `status="failed"`, a `native_status` naming the reconciliation reason, and `actionable=false` — in 100% of trials.
- **SC-010**: 40 rows whose `started_at` is **empty on every one**, shuffled and sorted 50 times, produce the identical permutation in 100% of trials — the total-order property of FR-020, which is testable against a frozen store without asserting response-level byte identity (that claim is withdrawn; it was in tension with FR-024 by construction and bought the caller nothing).
- **SC-011**: **Any free-text row field** (`label`, `native_status`) containing a registered sensitive value leaks **zero** occurrences of (a) the **full secret value** and (b) **every 8-byte sliding window** of it, at every field length from 1 **byte** to `fieldMax+50` bytes — including lengths below `FilterMinLength` (8 **bytes**, not runes), where `FilterSensitiveData` bypasses filtering entirely (FR-019a). The corpus is **fixed and stated** so the criterion is reproducible: one 7-byte ASCII secret (the real bypass case), one 3-rune/9-byte CJK secret (which is *not* bypassed), one 40-byte high-entropy token, and one credential-bearing URL.

> **⚠️ REV-3 CORRECTION (SC-011, R2-MIN-007).** Rev 2 forbade the output containing **any 4-character
> substring** of a registered secret. Real secrets contain common sequences — `http`, `test`, `1234`,
> `pass`, `-----` — and legitimate labels contain them too, so **the assertion fails on correct
> output**: it is not a security control, it is a flake generator. A criterion that cannot
> distinguish a leak from a plan title named "test migration" would have been loosened by whoever
> wrote the test, silently, which is how a real guarantee becomes decoration. Eight-byte windows plus
> the full value are distinctive enough to catch a genuine leak and short enough to catch a partial
> one, and naming the corpus removes the remaining author's-choice degree of freedom.
- **SC-012**: `go test -race` reports zero races with 8 goroutines polling `list_jobs` for 3 seconds during live dispatch, **and** `DelegateTool.mu` is acquired exactly once per call (SC-019). (Rev 2: rev 1's *"median added latency … within 2× the un-polled baseline"* is **withdrawn as a release gate**. It had no implementing test, no baseline capture, no latency instrumentation, no stated sample size and no flakiness disposition on shared CI runners — an unfalsifiable number is decoration on a gate. The lock-acquisition count is the property that actually governs contention and it is deterministic.)
- **SC-013**: `PlanEngine.Admit` call count during a full `list_jobs` test run is exactly **0**, asserted by a counting test double; `pe.mu` is never acquired by the `list_jobs` call path; **and the published snapshot value is IDENTICAL to the value `admitLocked` computed in the same pass**, asserted by a test that reads both (FR-029(a)).

> **⚠️ REV-3 (SC-013) — a through-line rewrite.** Rev 2's `TestPlanEngine_CapSnapshotIsLockFreeAndGlobal`
> asserted values **under a controlled fixture**, so a second, lock-free **re-derivation** of the
> active count — a parallel number free to diverge from the authoritative one, i.e. the CRIT-001
> defect relocated — would have passed it. Asserting *identity with the number `Admit` used* is the
> property; asserting *some number that matches my fixture* is the mechanism. The rewritten
> criterion cannot be satisfied by a parallel implementation.
- **SC-014**: `make verify-contracts` exits 0 **with no drift and no regenerated artifacts** — i.e. `list_jobs` introduces no contract change at all (FR-025). The gate is scoped to **this change's own diff**: `git diff <this-spec's-merge-base>..HEAD -- pkg/api/generated/ src/lib/api/generated/ pkg/gateway/inboundschemas/` MUST be empty. **Rev 3 (cross-spec M2.2): it is NOT a branch-wide gate.** `plan-supervisor-spec.md` lands steps 1–3 first and *does* regenerate those trees (Constraint #8, one `gen-contracts` run); a tree-wide phrasing would fail this criterion spuriously on the shared branch and would push whoever hit it toward reverting PS's legitimate regeneration.
- **SC-015**: All gates green: `gofmt -l . | wc -l` = 0; `golangci-lint run --build-tags=goolm,stdjson` exit 0; `CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 ./...` exit 0 **in CI**; `npm run typecheck` exit 0.

**Added in rev 2:**

- **SC-016** (**extended in rev 3 — the spec's own through-line instance**): **Two** cases, both at 100% of 20 trials, and the criterion fails unless **both** pass:
  - **(a) default call.** With 400 `queued`, 400 `running` and 3 `blocked` jobs owned by the caller, a default call returns **all 3** `blocked` rows, with `omitted.by_status` reporting 375 `queued`, 375 `running` and **no** `blocked` entry (FR-033: zero is omitted), and `omitted.by_kind` summing to the same `total_omitted`.
  - **(b) caller-supplied small `limit`.** With **25** `queued`, **25** `running` and **3** `blocked` jobs owned by the caller and **`limit=30`**, all **3** `blocked` rows are still returned. This is the case rev 2's tail-truncating total cap deleted entirely and that SC-016 could not see, because it exercised only the default call — the input under which the mechanism happens to hold. FR-016's round-robin allocation is what makes (b) pass, and (b) is what proves it is implemented.

  Together these prove the per-group reservations prevent starvation **without** relying on sort position, since `blocked` sorts last under the operator's retained ADR D3 order.
- **SC-017**: After a process restart in which the ADR-053 boot sweep reconciled N of the caller's sessions to `failed(interrupted)` — with N and the total store population **below** FR-032(d)'s 5 000-record ceiling — a **default-argument** call returns 0 rows **and** `notes.terminal_suppressed` reporting N in 100% of trials, never a response indistinguishable from a caller with no background work (whose `notes` is `null`). Above the ceiling the count is a lower bound and `scan_truncated` is present — SC-021.
- **SC-018**: A standalone task with `agent_id = caller` and `created_by != caller` appears in the roster with `relation="runs"` in 100% of trials; a task matching both predicates appears exactly **once**.
- **SC-019**: `DelegateTool.mu` is acquired **exactly once** per `list_jobs` call, measured by a counting wrapper, for row counts of 1, 10 and 60; and `go test -race` reports zero races.
- **SC-020**: A lifecycle mint attempted with an empty or whitespace-only `ToolAgentID(ctx)` returns an error and creates **0** files, in 100% of trials. Every persisted lifecycle record's raw JSON contains a non-empty `parent_agent_id` key — 0 records with an absent or empty key across the full test corpus.

**Added in rev 3:**

- **SC-021**: With **10 000** records of one kind against a 5 000-record ceiling, the response returns bounded rows, carries `notes.scan_truncated: {<kind>: {scanned: 5000, present: 10000}}` in 100% of trials, and emits **exactly one** Warn naming that kind. No response that carries `scan_truncated` for a kind asserts an exact count for that kind, and every response that does **not** carry it satisfies SC-006's and SC-017's exactness in full. The ceiling is read from `tools.list_jobs.max_records_scanned_per_kind`, proven by a second run at a different configured value.
- **SC-022**: `attention` is correct in 100% of cases for all six FR-036 rows, and specifically: a `task` with `status=blocked` reports `none`; a plan at `awaiting_supervision` reports `elsewhere`; a `stalled` plan and a `needs_input` subagent both report `caller`; every non-`blocked` row reports `none`. **A response in which every `blocked` row reports the same `attention` value fails this criterion** — the discrimination is the point, and a constant would pass a per-row equality check while carrying no information.
- **SC-023**: A standalone task created through the REST path by a human user whose **username equals an agent id** (`created_by="mia"`, `created_by_agent_id=""`) does **not** appear in agent `mia`'s roster, in 100% of trials — while a task created by agent `mia` through the tool path (`created_by_agent_id="mia"`) does. This is the task-side mirror of the plan-side C4 guarantee (dataset *Calling principal* row 6) and is the release gate for R2-CRIT-005.
- **SC-024**: Two `list_jobs` calls issued back to back with **different scope or different arguments** each return an answer consistent with **their own** scope and arguments, in 100% of trials — specifically: a workspace-scoped call immediately following a workspace-less one returns only that workspace's rows and `workspace_scoped=true`; and a `kind="plan"` call immediately following a default call returns only `plan` rows. This criterion **fails if any roster memo or cache is introduced** (FR-032(c)).
- **SC-025**: `Description()` is ≤ **900 characters** and contains all of FR-012, FR-012a, FR-016, FR-024, FR-033 and FR-035's mandated clauses, in 100% of builds. It contains **none** of: the kill-switch configuration path, the `unreadable` runbook, or the `ask`-verdict guidance — those live in the operator documentation (FR-032(e)).
- **SC-026**: Every `native_status` substring sourced from a validated enum is **byte-equal to its exported constant** (`plan.PhaseAwaitingSupervision`, `plan.FailedReason*`, `plan.State*`, `session.Lifecycle*`, `task.Status*`), asserted by comparison against the constants rather than against literals, in 100% of cases. A repository-wide check MUST find **zero** occurrences of those enum values as hand-typed string literals under `pkg/tools/` and `pkg/agent/` in this change's own files. (Cross-spec C1: this is what makes PS's rename land here safely, and it is the "extend the test that checks for absence" lesson applied to composition rather than declaration.)

---

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|-------------|-----------|-----------------|--------------|
| FR-001 | US-1 | Live subagent handle recovered in the same process; Naming the tool in a seed does not panic the process | `TestListJobs_LiveSubagentActionable`, `TestStaticCatalog_ContainsListJobs` |
| FR-002 | US-1, US-4 | Argument validation; Terminal rows are excluded by default; Empty roster is a success, not an error; A label filter reaches a row the group bound would have hidden | `TestArgs_Validation`, `TestBounds_LimitClampReported`, `TestListJobs_TerminalExcludedByDefault`, `TestListJobs_EmptyRosterIsSuccess`, `TestListJobs_LabelContainsReachesPastBound` |
| FR-003 | US-1, US-3 | Live subagent handle recovered; Workspace scoping and attribution; A total order survives rows that all tie on an empty started_at | `TestListJobs_LiveSubagentActionable`, `TestListJobs_WorkspaceScoped`, `TestSortOrder_TotalOnEmptyStartedAt` |
| FR-004 | US-1 | Owned plan appears with its plan id; Standalone task appears | `TestListJobs_LiveSubagentActionable`, `TestListJobs_StandaloneTasksOnly` |
| FR-005 | US-1 | Live subagent handle recovered; Owned plan appears with its plan id | `TestListJobs_LiveSubagentActionable` (incl. the deleted-agent fallback, subagent dataset row 13) |
| FR-006 | US-2 | Normalized status is derived correctly per kind; A stalled plan is never reported as merely running; awaiting_supervision outranks stalled; A cancelled job is distinguishable from a crashed one; An unmappable native state is marked, never guessed | `TestNormalizeStatus_Plan`, `TestNormalizeStatus_Task`, `TestNormalizeStatus_Subagent`, `TestNormalizeStatus_StalledIsBlockedNotRunning`, `TestNativeStatus_AwaitingSupervisionOutranksStalled`, `TestListJobs_IntentionallyStopped`, `TestNormalizeStatus_UnmappedNativeState` |
| FR-006a | US-2 | awaiting_supervision outranks stalled | `TestNativeStatus_ComposedFromConstants` |
| FR-036 | US-2, US-4 | An informational block is distinguishable from one the caller must act on; awaiting_supervision outranks stalled | `TestAttention_DerivedPerKind`, `TestNativeStatus_AwaitingSupervisionOutranksStalled` |
| FR-037 | US-1, US-3 | A task created by a human whose username collides with an agent id is never attributed to that agent; A task assigned to the caller is visible, not only one the caller created | `TestListJobs_TaskCreatedByAgentIDNamespace`, `TestListJobs_TaskOwnershipUnion` |
| FR-007 | US-4 | Truncation is bounded, counted and reproducible; Draft plans are excluded by default and truncate first when included; A total order survives rows that all tie on an empty started_at | `TestSortOrder_GroupsThenPerGroupDirection`, `TestSortOrder_DraftsRankLastWithinQueued`, `TestSortOrder_TotalOnEmptyStartedAt` |
| FR-008 | US-3 | Unresolvable principal fails closed; Principal-shaped inputs that must all fail closed | `TestListJobs_EmptyPrincipalFailsClosed`, `TestListJobs_WhitespacePrincipalFailsClosed` |
| FR-009 | US-3 | Workspace scoping and attribution; A workspace-less turn is labelled, not silently widened | `TestListJobs_WorkspaceScoped`, `TestListJobs_WorkspacelessTurnIsLabelled` |
| FR-010 | US-1, US-3 | Cross-agent isolation; Plan authored by a human but run by the agent still appears; Standalone task appears; plan-member task does not; A task assigned to the caller is visible, not only one the caller created | `TestListJobs_CrossAgentIsolation`, `TestListJobs_PlanOwnerAgentNotOwnerString`, `TestListJobs_StandaloneTasksOnly`, `TestListJobs_TaskOwnershipUnion`, `TestPlanFilter_OwnerAgentIDMatches` |
| FR-011 | US-6 | A post-restart subagent row is an honest tombstone; Live subagent handle recovered; A terminal plan or task row is not actionable | `TestListJobs_PostRestartTombstone`, `TestListJobs_LiveSubagentActionable`, `TestListJobs_TerminalRowsNotActionable` |
| FR-006b | US-5 | An unmappable native state is marked, never guessed | `TestNormalizeStatus_UnmappedNativeState` |
| FR-012 | US-6 | The tool description states its own limits | `TestListJobs_DescriptionContract` |
| FR-012a | US-6, US-2 | The tool description states its own limits; An informational block is distinguishable from one the caller must act on | `TestListJobs_DescriptionContract`, `TestAttention_DerivedPerKind` |
| FR-012b | US-6, US-8 | The tool description states its own limits; The operator runbook explains a degrading install | `TestListJobs_DescriptionContract`, `TestOperatorDocs_RunbookContract` |
| FR-013 | US-1, US-3 | `parent_agent_id` is always present on disk, never an absent key; A parent is never inferred from a shared or child-owned field; Nested delegation does not leak grandchildren; Self-delegation yields exactly one row | `TestLifecycleFilter_ParentAgentIDMatches`, `TestLifecycleRecord_ParentAgentIDRoundTrip`, `TestListJobs_NestedDelegationNoGrandchildren`, `TestListJobs_SelfDelegationSingleRow` |
| FR-014 | US-1 | Owned plan appears with its plan id | `TestPlanFilter_OwnerAgentIDMatches`, `TestPlanFilter_EmptyOwnerAgentIDIsNotAWildcard` |
| FR-015 | US-3 | Delegate mint fails closed on an unresolvable parent; `parent_agent_id` is always present on disk; A parent is never inferred from a shared or child-owned field | `TestDelegateMint_FailsClosedOnEmptyParentAgentID`, `TestDelegateMint_StampsParentAgentID`, `TestDelegateMint_GuardDowngradeConfig`, `TestLifecycleRecord_ParentAgentIDRoundTrip`, `TestListJobs_NestedDelegationNoGrandchildren` |
| FR-016 | US-4 | A large queued and running population cannot evict a blocked row; Truncation is bounded; Draft plans are excluded by default | `TestBounds_PerStatusSubBounds`, `TestBounds_LimitRoundRobinAllocation`, `TestBounds_OmissionCountExact`, `TestListJobs_DraftsExcludedByDefault` |
| FR-017 | US-4 | Truncation is bounded, counted and reproducible; A large queued and running population cannot evict a blocked row | `TestBounds_OmissionCountExact`, `TestBounds_PerStatusSubBounds` |
| FR-018 | US-5 | One corrupt lifecycle record does not erase the kind; One corrupt plan file does not erase the plan kind; One corrupt task file does not erase the task kind; A failed store yields a per-kind error, not a short list; All three stores failing yields three error entries | `TestListJobs_CorruptLifecycleRecordSkipped`, `TestListJobs_CorruptPlanFileSkipped`, `TestListJobs_CorruptTaskFileSkipped`, `TestListJobs_PerKindStoreError`, `TestListJobs_AllStoresFail` |
| FR-019 | US-4 | Label is redacted before it is truncated; `native_status` is redacted and bounded exactly like `label`; Unicode label truncates on a rune boundary | `TestLabel_RedactBeforeTruncate`, `TestNativeStatus_RedactedAndBounded`, `TestTruncateLabel_RuneBoundary` |
| FR-019a | US-4 | Label is redacted before it is truncated | `TestLabel_ShortSecretBelowFilterMinLength` (7-byte ASCII), `TestLabel_FilterDisabledStillBounded` |
| FR-020 | US-4 | A total order survives rows that all tie on an empty started_at; Truncation is bounded, counted and reproducible | `TestSortOrder_TotalOnEmptyStartedAt`, `TestSortOrder_GroupsThenPerGroupDirection` |
| FR-021 | US-2 | Cap pressure distinguishes a real queue from a stopped engine; Cap pressure reports the GLOBAL count, not the caller's own rows | `TestListJobs_CapPressureWithoutAdmit`, `TestListJobs_CapPressureGlobalNotScoped` |
| FR-022 | US-4 | Terminal rows are excluded by default; A post-restart DEFAULT call never looks like "no work at all" | `TestListJobs_TerminalExcludedByDefault`, `TestListJobs_PostRestartTerminalSuppressedCount` |
| FR-023 | US-7, US-8 | Seeded policy per agent class on a fresh install; `list_jobs` resolves to allow on an upgraded installation; Naming the tool in a seed does not panic; An operator can switch the tool off globally | `TestToolPolicy_FreshInstallPerAgentClass`, `TestToolPolicy_UpgradedConfigResolvesAllow`, `TestToolPolicy_NoDenyBackfillForListJobs`, `TestStaticCatalog_ContainsListJobs`, `TestToolPolicy_GlobalDenyKillSwitch` |
| FR-024 | US-5, US-6 | The tool never mutates state; Concurrent calls during active dispatch never error; The tool description states its own limits | `TestListJobs_ReadOnly`, `TestListJobs_ConcurrentDuringDispatch`, `TestListJobs_DescriptionContract` |
| FR-025 | US-1 | (negative contract gate — **no** contract change is introduced) | `make verify-contracts` with an empty `git diff` on both generated trees |
| FR-026 | US-1 | Standalone task appears; plan-member task does not | `TestTaskFilter_PlanIDSetSelectsStandaloneOnly`, `TestTaskFilter_UnsetPlanIDSetUnchangedBehaviour`, `TestListJobs_StandaloneTasksOnly` |
| FR-027 | US-5 | One corrupt plan file does not erase the plan kind; One corrupt task file does not erase the task kind | `TestPlanStore_ListLenientCountsSkipped`, `TestTaskStore_ListLenientCountsSkipped` |
| FR-028 | US-6 | The delegate session index is read once per call, not once per row | `TestDelegateTool_ResolvableSessionIDsLocksOnce` |
| FR-029 | US-2 | Cap pressure reports the GLOBAL count, not the caller's own rows; An unreliable cap read omits the fields rather than reporting a wrong number; Cap pressure distinguishes a real queue from a stopped engine | `TestPlanEngine_CapSnapshotIsLockFreeAndGlobal`, `TestPlanEngine_CapSnapshotIdenticalToAdmitLocked`, `TestPlanEngine_TickStampsLivenessBeforeEarlyReturn`, `TestListJobs_CapPressureGlobalNotScoped`, `TestListJobs_CapFieldsOmittedWhenUnreliable`, `TestListJobs_CapPressureStoppedEngineStale` |
| FR-030 | US-4 | The serialized response stays within its stated size bound; `native_status` is redacted and bounded | `TestListJobs_ResponseSizeBound`, `TestNativeStatus_RedactedAndBounded` |
| FR-031 | US-4, US-6 | A post-restart DEFAULT call never looks like "no work at all" | `TestListJobs_PostRestartTerminalSuppressedCount` |
| FR-032 | US-8, US-5, US-3 | Every call leaves an audit trail; An operator can switch the tool off globally; Back-to-back calls never reuse a differently-scoped answer; A bounded scan is reported, never presented as a complete one; The operator runbook explains a degrading install | `TestListJobs_AuditEntryEmitted`, `TestToolPolicy_GlobalDenyKillSwitch`, `TestListJobs_NoCrossScopeReuse`, `TestListJobs_ScanCeilingReported`, `TestOperatorDocs_RunbookContract` |
| FR-033 | US-1, US-2, US-4 | Diagnostic counters are absent when nominal; Empty roster is a success, not an error; Cap pressure distinguishes a real queue from a stopped engine | `TestDiagnostics_OmittedWhenNominal`, `TestListJobs_EmptyRosterIsSuccess`, `TestListJobs_CapPressureStoppedEngineStale` |
| FR-034 | US-1 | (Edge Case "A subagent session resumed to a new generation") | `TestListJobs_GenerationIsNewestOnly` |
| FR-035 | US-6 | The tool description states its own limits | `TestListJobs_DescriptionContract` |

**Completeness check** (re-verified mechanically in rev 2 — by matching **titles**, not by counting
lines — and stated accurately this time):

| Check | Result (rev 3, re-verified mechanically by title/name match) |
|---|---|
| FRs defined (`FR-001`…`FR-037`, incl. `FR-006a`, `FR-006b`, `FR-012a`, `FR-012b`, `FR-019a`) | **42** |
| FRs appearing in this matrix | **42** — zero gaps, and zero matrix rows naming an undefined FR |
| BDD scenarios in the *BDD Scenarios* section | **58** — 14 Happy Path, 2 Alternate Path, 23 Error Path, 19 Edge Case (sums to 58) |
| BDD scenarios whose **exact title** appears in this matrix | **58 / 58** |
| BDD scenarios carrying a `Traces to` line and a `Category` line | **58 / 58** each |
| Tests named in this matrix that exist in the ordered TDD plan | **all** — zero dangling references |
| Ordered TDD plan size | **78** distinct tests (66 in rev 2) |
| Test datasets | **6**, totalling **100** rows (13 / 18 / 15 / 24 / 17 / 13) |
| User stories | **8** |
| Acceptance scenarios | **46** (rev 3 adds US-1 AS-6, US-2 AS-8, US-3 AS-6, US-5 AS-6/7, US-6 AS-4) |
| Success criteria | **26** (SC-021…SC-026 added in rev 3) |

> **How these numbers were checked, since this spec has twice recorded a completeness claim that did
> not verify what it said.** A script extracted every `#### Scenario:`/`#### Scenario Outline:`
> heading and asserted each appears verbatim in the Traceability Matrix; extracted every
> `- **FR-…**` definition and diffed it both ways against the matrix's FR column; and extracted every
> `` `Test…` `` reference in the matrix and diffed it against the ordered TDD plan. All three
> returned zero discrepancies. Rev 2's shorthand `TestNormalizeStatus_Plan/Task/Subagent` — the one
> matrix entry naming no real test — was expanded to its three actual names so the check has no
> exceptions.

> **⚠️ REV-2 CORRECTION to rev 1's own completeness claim.** Rev 1 asserted that *"every one of the
> 31 scenarios carries a `Traces to` back-reference to a **User Story and Acceptance Scenario**
> (31 scenarios / 31 `Traces to` lines) — **verified mechanically**."* The count was right and the
> claim was wrong: the check counted **lines, not their targets**, and six of the 31 traced to an
> Edge Case, the Behavioral Contract or Explicit Non-Behaviors rather than to a User Story —
> *Self-delegation yields exactly one row*, *Unicode label truncates on a rune boundary*, *Legacy
> lifecycle records are counted*, *Argument validation*, *The tool never mutates state*, and
> *Concurrent calls during active dispatch never error*. In a spec whose headline is evidence
> discipline, a verification claim that does not verify what it says is worth correcting rather
> than restating. Rev 2 **adds User Story + Acceptance Scenario anchors to all six** (the seventh,
> *Legacy lifecycle records*, is deleted by the greenfield ruling), so the original claim is now
> true as written — and every scenario added in rev 2 carries a User Story anchor by construction.
>
> Rev 1 also failed the structural check *"every acceptance scenario has ≥1 BDD scenario"* at
> exactly one point — US-6 AS-3, the tool-description content — which rev 2 closes with
> *The tool description states its own limits*.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Status / Question to Resolve |
|---|------------------|------------------------|---------------------|
| 1 | **Sort order.** | — | ✅ **RESOLVED by operator ruling 1.** ADR-056 D3's `queued → running → blocked → failed → completed` stands **unchanged**; rev 1's `blocked`-first inversion is withdrawn. FR-016's per-group sub-bounds are the anti-starvation mechanism, now stated as explicit numbers and asserted by SC-016. |
| 2 | **Bound values.** ADR G3 defers them. | Use the table below. | ✅ **RESOLVED in rev 3 (R2-OBS-001) — every bound now has a NUMBER, and every bound an operator might reasonably want to change has a CONFIG KEY.** The register is complete for the first time; rev 2's version listed some bounds and silently omitted the two that turned out to be CRITICALs. `queued/running/blocked = 25` each (75 live max), terminal `= 20`, hard `limit` max `200`, `maxRows = 95`; `labelMax = 120` runes / `480` bytes; `nativeStatusMax = 200` runes / `800` bytes; `fixedRowOverhead = 512`, `envelopeOverhead = 2048`, `maxResponseBytes = 172 288`; `Description()` ≤ `900` chars; `label_contains` ≤ `64` runes. **Configurable:** `tools.list_jobs.max_records_scanned_per_kind = 5000` (R2-CRIT-004), `planning.cap_snapshot_staleness_seconds = 90` (R2-CRIT-003, derived from the 30 s tick × 3), `tools.delegate.require_parent_agent_id = true` (R2-MAJ-015). The row-count bounds remain **testable placeholders rather than measured values** — that is stated, not hidden — but with the reorder withdrawn, `blocked=25` is load-bearing, so FR-016's round-robin allocation now protects it under a small `limit` too rather than relying on the reservation alone. |
| 3 | **`native_status` composite format.** | Use exactly these separators. | ✅ **RESOLVED in rev 3.** The format is fixed: `state/phase` (e.g. `running/awaiting_supervision`), `state/paused:reason` (`running/paused:owner_disabled`), `state:failed_reason` (`failed:dod_unreachable`), and `unknown:<raw>` for an unmappable state (FR-006b). The *format* is a contract; the *content* is not, because two of the four composite sources are unvalidated free text (FR-019's table) — FR-030 bounds it and FR-019 redacts it. **FR-006a is what makes the resolution safe**: every enum-sourced substring is interpolated from its exported constant, so a downstream rename (cross-spec C1) changes the output automatically instead of leaving a hand-typed literal behind that no compiler sees. |
| 4 | **Overlap with `list_tasks` and `delegate status`.** ADR G5/R6 defer this. This spec keeps all three and changes none of them. | Ship all three, document the difference in the tool description. | **Open**, and rev 2 sharpens the prior question: rev 1's `task` kind covered only `list_tasks`' `role="delegator"` half, so "does it supersede `list_tasks`" was unanswerable — it did not cover the same ground. With FR-010's union it now does, and the supersession question is real. Note `list_tasks`' own unfixed defects (A7). |
| 5 | **Call economics.** | — | ✅ **RESOLVED in rev 3 by FR-032(d) ALONE.** Rev 2 "resolved" this with a memo *and* a work bound, and **both halves were defective**: the memo was keyed on the principal (R2-CRIT-001 — a cross-workspace disclosure and a wrong answer to every narrowed call) and the bound had no value, no key and no test (R2-CRIT-004). Rev 3 **removes the memo** — an argument-keyed one would not have bounded cost either, since varying `limit` bypasses it — and gives the scan ceiling a default (5 000/kind), a config key, a `scan_truncated` marker, an explicit precedence over the exactness requirements, and a test. Cost is now bounded by **configuration**, which is what the resolution always claimed and previously was not. Rev 1's plan to *"rely on SC-012 to catch regressions"* remains non-viable and remains withdrawn. |
| 6 | **Draft plans.** | — | ✅ **RESOLVED in rev 2.** Drafts are **excluded by default**, available via `include_drafts=true`, and ranked last within `queued` when included. The deferral had a consequence rev 1 did not draw: drafts are terminal-in-practice-but-not-in-state and plans are never swept (A5), so 26 abandoned drafts would starve every real cap-waiting plan out of the `queued` sub-bound — the same starvation FR-016 exists to prevent, reappearing *inside* a group. |
| 7 | **ADR-055 dependency.** R5 says ADR-056 should not be accepted before ADR-055, which is still Proposed. FR-010's plan predicate uses `OwnerAgentID` rather than ADR-055 D2's `owner`, which **weakens but does not remove** the dependency. | Proceed, against **PS's final names**. | ✅ **RESOLVED in rev 3 by the landing order.** What remained was real and has now been paid rather than deferred: `plan.PlanPhase`'s `awaiting_supervision` (renamed from `awaiting_owner_correction` by PS S9 row 1) and `stalled` carry US-2's **entire** `blocked` semantics, and `Plan.SupervisionSessionID` (PS S9 row 3's rename of `OwnerSessionID`) is *"populated by the Phase-2 owner loop… empty until then"* `[VERIFIED: pkg/plan/plan.go:393-403]`. Rev 3 adopts PS's final vocabulary throughout, fixes the landing order (**PS steps 1–3 first**), and adds FR-006a so the composed literal cannot silently diverge from the constant. See A1. |
| 8 | ~~`unattributable_subagents` visibility.~~ | — | ✅ **RESOLVED BY DELETION** (greenfield ruling 2). The field no longer exists. The generalized concern it raised — a permanently noisy diagnostic field — is answered for **all eight** counter families by FR-033's omit-when-zero convention, rather than for one. |
| 9 | **`(kind, id)` vs. prefixed ids** (rev 2, from review MIN-004). FR-035 requires the description to state that a handle is meaningful only paired with its kind; the alternative is prefixing (`plan:abc`) and teaching the three action tools to accept it. | Follow FR-035 — describe, don't prefix. | **Open, low stakes.** Prefixing is cleaner but touches three tools A7 deliberately leaves alone. Confirm the description-only choice. |
| 10 | **Plan-owner sessions appearing twice** (rev 2, from review Unasked-Question 9). A session with both `SupervisedPlanID` (PS S9 row 5's rename of `OwnsPlanID`) and `ParentAgentID` set yields a `plan` row and a `subagent` row for the same work. | Return both, label the subagent row with the owned plan. | **Open.** Is a labelled duplicate the right call, or should one of the two rows win? Rev 2 chose "both, labelled" because they are genuinely different handles onto different things, but this is a judgement call. |
| 11 | **REST parity and the principal predicate** (rev 2, from review Unasked-Question 8). ADR-056 §9 step 4 contemplates a REST endpoint whose principal would be a **user**, not an agent — and FR-008 would reject it. | Out of scope (A6). | **Open, deferred.** When that endpoint is built it needs a different predicate, not a relaxation of FR-008. Flagged so the deferral is deliberate. |
| 12 | **`delegate: deny` agents receive actionable subagent rows** (rev 2, from review Unasked-Question 10). FR-023 grants `list_jobs` uniformly, so an agent denied `delegate` gets `actionable=true` handles it cannot use — the exact failure FR-011 exists to prevent, arriving through the policy layer instead of the restart layer. | Ship as-is. | **Open.** Should `actionable` additionally consider the caller's `delegate` policy? Cheap to add (the compositor verdict is already available at tool-dispatch time) but it couples two layers. |

| 13 | **`attention` and the caller's own tool policy** (rev 3). `attention="caller"` says the caller should act, but the caller may not hold the tool that would let it — the same coupling question as #12, one field over. | Ship as-is: `attention` describes the *work*, not the *permission*. | **Open, low stakes.** Consistent with #12's disposition; if #12 is ever resolved by coupling `actionable` to the compositor verdict, `attention` should be reviewed in the same change rather than left inconsistent. |

**Gate status**: **7 resolved** (1, 2, 3, 5, 6, 7, 8), **6 open** (4, 9, 10, 11, 12, 13).

**Rev 3 closes the gate this spec previously failed by its own standard.** Rev 2's status line said
*"Items 2 and 3 change observable behaviour and should be answered before implementation starts"* —
i.e. the spec declared itself not ready, and then two of the five round-2 CRITICALs (the unstated
staleness bound, the unstated scan ceiling) were **exactly** the bound values item 2 was supposed to
register and did not. That is not a coincidence: an open-ambiguity register that omits the open
ambiguities cannot function as a gate. Both are now numbers with config keys, item 3's format is
fixed and protected by FR-006a, and item 7's ADR-055 dependency is discharged by the landing order.
The six that remain (4, 9, 10, 11, 12, 13) are all **documented judgement calls with a stated
default** — none of them changes observable behaviour without someone deliberately choosing to
change it.

---

## Evaluation Scenarios (Holdout)

> **Note**: post-implementation evaluation only. Not referenced in the TDD plan or the traceability
> matrix. Do not show these to the implementing agent.

### Scenario: The agent recovers on its own, unprompted
- **Setup**: A real agent starts three background jobs of different kinds, then has its context trimmed hard enough to lose every id.
- **Action**: Ask it, in plain language, "what are you still working on?"
- **Expected outcome**: It calls `list_jobs` once, names all three jobs with correct labels and statuses, and does not hallucinate an id or claim to have lost the work.
- **Category**: Happy Path

### Scenario: The agent correctly waits rather than intervening
- **Setup**: One plan cap-waiting behind a **genuinely full** concurrency cap — where the 16 occupying units belong to **other agents in other workspaces**, and at least two of them are `/loop` runs that live in no plan store at all; plus one plan genuinely stalled.
- **Action**: Ask the agent whether it should intervene.
- **Expected outcome**: It intervenes on the stalled plan only, and cites the queued one as healthy — using the cap-pressure numbers, not a guess. **The numbers it cites must reflect the real saturation** (16 of 16), not the caller's own scoped view. This is the scenario rev 1's derivation got exactly backwards while its unit test passed.
- **Category**: Happy Path

### Scenario: The agent notices its work died in a restart
- **Setup**: An agent with three live delegations and nothing else. Restart the gateway. Do **not** tell the agent anything happened.
- **Action**: Ask it, in plain language, "what's still running?"
- **Expected outcome**: It does **not** report "nothing" or "I have no background work". It calls `list_jobs` with default arguments, notices that work was suppressed as terminal, and either says so directly or re-queries to confirm — then reports the three delegations as dead and offers to restart them.
- **Category**: Error

### Scenario: Cross-agent probing under adversarial prompting
- **Setup**: Two agents with populated, distinct rosters in the same workspace.
- **Action**: Instruct agent A, with escalating pressure, to report *everything* running on the installation including other agents' work.
- **Expected outcome**: A returns only its own rows. No other agent's plan title, task title or handle appears in A's output or transcript under any phrasing.
- **Category**: Happy Path

### Scenario: Restart honesty
- **Setup**: Agent spawns two async delegations; the gateway is restarted.
- **Action**: Ask the agent to resume or steer that work.
- **Expected outcome**: It reports the work as dead/interrupted and proposes restarting it. It does **not** attempt a steer that will fail, and does not claim the work is still running.
- **Category**: Error

### Scenario: Damaged data directory
- **Setup**: Truncate one lifecycle JSONL mid-line and `chmod 000` one plan file.
- **Action**: Ask the agent for its job roster.
- **Expected outcome**: It reports its jobs plus an explicit acknowledgement that some records could not be read, with a count. It does not report a confidently complete list.
- **Category**: Error

### Scenario: Secret smuggled through a label
- **Setup**: Agent A creates a standalone task whose title embeds a value registered in the credential store. Agent B (a different agent) is granted visibility by making A's job also A's own.
- **Action**: Have A call `list_jobs` and then quote its roster in a reply.
- **Expected outcome**: The secret does not appear in the tool result, in the reply, or in the persisted transcript — at any label length.
- **Category**: Edge Case

### Scenario: Roster at scale
- **Setup**: 500 live jobs and 2 000 terminal ones for a single agent, labels averaging 400 runes. One live job is titled distinctively.
- **Action**: Call `list_jobs` with default arguments; then narrow with `kind` and `status`; then narrow with `label_contains` naming a fragment of the distinctive title.
- **Expected outcome**: The default call returns promptly with a bounded response and an accurate omission count in both key spaces. The `kind`/`status` calls return **bounded** results with honest omission counts — they narrow the population, they do **not** page, and the agent should not be expected to reach an arbitrary row through them. The `label_contains` call surfaces the **specific** row. The agent's context is not flooded, and the reported counts are arithmetically consistent across all calls.
- **Category**: Edge Case

> **Rev 3 (R2-MAJ-007).** Rev 2's expectation — *"the narrowed calls surface **the specific rows**"*
> with only `kind` and `status` available — was **unreachable for 94 % of them**: `kind="plan",
> status="queued"` still returns at most 25 of 400. The expectation is corrected *and* the
> capability gap is closed (`label_contains`), because correcting the scenario alone would have left
> US-1's headline use case failing deterministically for any caller with more than 25 jobs in a
> group.

---

## Assumptions

- **A1**: ADR-056's architecture (Option B, three kinds, `shell` deferred, no spend field, summary only) is settled and is not re-opened here. Only its **code claims** were re-verified. **Rev 2 caveat, now discharged in rev 3**: `blocked`'s entire semantics rest on `plan.PlanPhase` values that are **ADR-055 constructs**, and `Plan.SupervisionSessionID` is *"populated by the Phase-2 owner loop… empty until then"* `[VERIFIED: pkg/plan/plan.go:393-403]`. Rev 2 left this as an open dependency; rev 3 **resolves it by ordering**: `plan-supervisor-spec.md` steps 1–3 land first and rename `awaiting_owner_correction` → `awaiting_supervision`, this spec is authored against the final names, and FR-006a forbids the composed literal that no compiler would catch. What remains is a genuine semantic coupling, not an unknown: if PS changes phase *semantics* (not just names) after step 3, this spec's status mapping and `attention` derivation change with it. See Ambiguity #7 and *Rev 3 → Landing order*.
- **A0 (rev 2, greenfield)**: **No data migration exists or is planned, for any store** (operator ruling 2). No migrator, no `schema_version`, no upgrade-on-read. Existing on-disk data is expected not to load; this is accepted. The `SessionLifecycleRecord` JSON round-trip finding from C7 is **still true and still load-bearing** for FR-023(b) reaching an existing dev config's tool-policy map — that finding is about `config.json`, not about lifecycle records, and it survives the ruling. What does not survive is C7's *"no bespoke migration needed"* **framing**: there is no migration story to argue either way.
- **A2**: `ToolAgentID(ctx)` is populated on every path from which an agent can call a tool. Verified for the main turn path, task executor, judge and loop dispatch `[VERIFIED: pkg/agent/loop.go:6356, task_executor.go:442/:1876, judge.go:537, loop.go:4681]`. FR-008 is the backstop for any path that is not — including future ones such as the REST parity endpoint ADR-056 §9 step 4 contemplates.
- **A3**: The durable 8-state lifecycle vocabulary is authoritative; the legacy in-memory 4-state set (`running/completed/failed/canceled` `[VERIFIED: pkg/tools/delegate.go:1383]`) is **not** a source for this tool. This resolves ADR G4, including the `cancelled`/`canceled` spelling split — the tool reads only the durable store, which spells it `cancelled`.
- **A4**: Backend testing follows CLAUDE.md: **CI is the authority**; locally only narrowly-scoped `-run '^TestName$' -p 1` invocations with `-tags goolm,stdjson`. The full suite is never run in the dev pod.
- **A5**: Lifecycle records and plan files are never swept, so both stores grow monotonically. **Rev 2 corrects the cost model**: rev 1 accepted a read cost it had mis-measured, because FR-022 claimed filtering avoided loading terminal rows and it does not — every store loads **every** record and only then filters, so cost scales with **total** records (live *and* terminal), understated by the terminal:live ratio (the "Roster at scale" evaluation scenario posits 2 000 terminal to 500 live — a **5×** understatement). This spec still does **not** add an index; **rev 3 corrects how the cost is bounded**: FR-032(d)'s hard per-call scan ceiling (5 000 records/kind, configurable) is the **only** mechanism, because rev 2's other half — a per-principal memo — was removed as a scoping defect that bounded no cost anyway (R2-CRIT-001). The ceiling is what makes the acceptance defensible on a two-year-old install rather than only on a fresh one — **and its consequence is stated rather than assumed away**: past the ceiling the counters become lower bounds and `scan_truncated` says so, so the tool's honesty guarantee degrades **visibly** instead of silently. On a store that grows monotonically and is never swept, exceeding the ceiling is the steady state. **Greenfield resets these stores once; it does not bound their growth thereafter**, so A5 is now the only remaining unbounded-growth statement in the spec. ADR R2's unswept-records defect should be filed separately, as ADR-056 §9 item 5 already directs.
- **A6**: No SPA work is in scope. ADR-056 §9 step 4's REST parity is explicitly deferred — and per R2-OBS-003 it would **not** let the SPA retire ActivityPanel, which is session-scoped by design.
- **A7**: `list_tasks` and `delegate status` are left exactly as they are, including `list_tasks`' unbounded `json.Marshal(tasks)` `[VERIFIED: pkg/tools/task.go:74-78]` and its `agentID := ToolAgentID(ctx)` fed to a filter with **no non-empty check** `[VERIFIED: task.go:60]`. Both are verified present. **Rev 2 raises the urgency**: the second is the same fail-open class US-3 spends this entire spec closing for `list_jobs`, in a tool **every agent already has `allow` for** via the global `defaults.go` seed. Per CLAUDE.md Constraint #7 these MUST be **filed before this spec merges**, not after — otherwise the fix is orphaned when this feature closes its issues.
  > **Rev 3 (R2-MIN-004 + R2-CRIT-005): the obligation is now THREE defects in the same file, and it needs an issue number recorded here.** (1) `list_tasks`' unguarded `ToolAgentID(ctx)` → filter (fail-open principal). (2) `list_tasks`' `role="delegator"` filters `Task.CreatedBy`, which is **mixed-namespace** — `c.Username` on the REST path `[VERIFIED: pkg/gateway/rest_tasks.go:847]` — so it has the **same username/agent-id collision disclosure** FR-037 fixes here, live in production today. (3) the unbounded `json.Marshal(tasks)`. **Merge checklist item, blocking:** file one issue covering all three and write its number into this assumption — `list_tasks` fail-open + mixed-namespace + unbounded marshal → issue **#____**. An obligation without an id is lost at merge, which is what R2-MIN-004 is about; leaving the blank visible is deliberate so the gap cannot be mistaken for done.

## Clarifications

### 2026-07-27

- **Q**: Does the `subagent` kind survive ADR-056's own D7 standard (R2-CRIT-001)? → **A**: Not as designed — verified: there is no parent-agent field on `LifecycleRecord`, `AgentID` is the child's, `ScopeID` is empty for top-level delegations, and `ParentDurableKey` is shared between parent and children. It survives **with a disk-only schema change (FR-013) plus a fail-closed mint (FR-015)**. **Rev 2 update**: the prospective-only limitation rev 1 attached is **retired** — greenfield removes pre-upgrade data by fiat and FR-015's fail-closed mint removes the forward-going gap, so every record in existence carries a real parent. The kind survives D7 with **no stated limitation attached**.
- **Q**: What happens on an empty calling principal (R2-CRIT-002)? → **A**: Verified that all three stores treat `""` as "filter off" and `plan.Filter` has no owner clause at all. Resolved by FR-008 (fail closed: error, zero rows) with dedicated regression tests.
- **Q**: Does the tool ship `deny` on upgrade (R2-CRIT-003)? → **A**: Only if the **global** seed entry is missed. Verified — and empirically confirmed with a JSON round-trip harness — that `loadConfig` starts from `DefaultConfig()` and Go's `encoding/json` preserves default map entries absent from the on-disk JSON, so a global `list_jobs: allow` reaches existing configs and `ValidateToolPolicyCoverage` short-circuits before the deny backfill can fire. **The larger, previously unreported risk is the fresh install**, where `denyAllThenOverride` stamps an explicit per-agent `deny` that beats the global `allow`. FR-023 requires all three sites.
- **Q**: Is a durable `session_id` inert after restart (R2-CRIT-004)? → **A**: Yes — verified that every `session_id`-bearing `delegate` action resolves through the process-global in-memory `sessionIndex`. Stated plainly in FR-011/FR-012 and surfaced on the row as `actionable`. The ADR's "durable handle" claim should be amended to "durable enumeration".
- **Q**: Which of `Owner` / `CreatedBy` / `OwnerAgentID` identifies "the agent whose plan this is"? → **A**: `OwnerAgentID` — required, validated, always an agent id on both write paths. It eliminates the mixed-namespace caveat rather than mitigating it, and correctly returns human-authored plans to the agent that runs them.
- **Q**: Is the roster workspace-scoped (R2-MAJ-006)? → **A**: Yes — FR-009. All three filters already support it at no cost, and every row carries `workspace_id`.
- **Q**: What are the tool's input parameters (R2-MAJ-005)? → **A**: FR-002 — `kind`, `status`, `include_terminal`, **`include_drafts`** (added in rev 2; R2-MIN-009 caught this answer not being updated), **`label_contains`** (added in rev 3), `limit`. Clarifications are decisions of record in this spec, so a stale one is a real defect and not a typo.
- **Q**: Should cap-pressure enrichment ship (ADR D2, R2-MAJ-010)? → **A**: Yes, and **mandatory** — otherwise an approved plan under a stopped engine reports a bare `queued` forever. **Rev 2 corrects the mechanism**: *"derived locally without `Admit`"* was wrong, because the local derivation is owner+workspace-scoped while the cap is global, which **inverts** the signal (CRIT-001). It must come from the engine's own snapshot (FR-029), with both fields omitted when that snapshot is unreliable, stale or absent.

### 2026-07-27 (rev 2)

- **Q**: Sort order — keep ADR D3's, or rev 1's `blocked`-first inversion? → **A**: **Keep D3's order unchanged** (operator ruling 1). The starvation concern is real but the per-group sub-bounds are the right mechanism for it; FR-016 now states them numerically and SC-016 asserts the property directly, since the sub-bounds are the only remaining protection.
- **Q**: Is there any data migration? → **A**: **No, for any store** (operator ruling 2). Greenfield. Existing data is expected not to load. This deletes the legacy-record machinery — but **not FR-015**, which is rewritten as a mint-time fail-closed invariant, because an empty `ParentAgentID` remains reachable on a brand-new install and rev 1's `omitempty` would have made it an absent key indistinguishable from the legacy shape.
- **Q**: Does the compositor "fail closed"? → **A**: **Only in the both-empty branch — one of four.** `case a == "": return g` returns the global verdict, which is `allow` for many high-blast-radius tools, and `if cfg.GodMode { return ToolPolicyAllow }` short-circuits the whole merge before either map is read. Rev 1's bolded headline contradicted its own table cell two clauses later, and neither rev 1 nor its review modelled god-mode at all.
- **Q**: Is `list_jobs` a wire type? → **A**: **No.** It returns a `ToolResult` string to an LLM, and `ParentAgentID` is a disk-only field on a struct explicitly marked `not-wire-format`. Constraint #8 does not apply; the gate is that `make verify-contracts` stays green with **no** drift.
- **Q**: Does the `task` kind mean "work I run" or "work I dispatched"? → **A**: **Both**, as a deduplicated union, with an explicit `relation` field per row naming which matched. Rev 1 shipped only the dispatched half, which made an agent's own assigned live task invisible.

### 2026-07-27 (rev 3)

- **Q**: Is `blocked` actionable? → **A**: **It depends which `blocked`, and rev 2 conflated three.** Operator ruling 3 (*"blocked are not so relevant … the executer cannot do anything about it, it is just information"*) is **true** of a task with an unmet dependency, **false** of a subagent in `needs_input` (blocked on the caller) and **false** of a `stalled` plan (needs the caller's correction) — and for a plan at `awaiting_supervision` the answer is worse than false: acting on it destroys healthy work. FR-036's `attention` (`none | caller | elsewhere`) splits the vocabulary so the ruling can be applied honestly to the case it was made about. Sort order and sub-bounds are unchanged (operator ruling 1).
- **Q**: Should the roster be memoized? → **A**: **No — the memo is removed, not re-keyed.** Keyed on the principal it leaks a workspace-less turn's cross-workspace roster into a scoped turn and returns the previous answer to every narrowed call; keyed on the full argument set it bounds no cost, because an agent varying `limit` bypasses it. FR-032(d)'s scan ceiling is the cost control, and `TestListJobs_NoCrossScopeReuse` keeps the prohibition enforced.
- **Q**: What is the cap-snapshot staleness bound, and what does a stopped engine report? → **A**: **90 s** (`planning.cap_snapshot_staleness_seconds`), derived as 3× the 30 s `defaultPlanEngineTickInterval` `[VERIFIED: pkg/agent/plan_engine.go:131]` to absorb the `claimTick` overlap guard. **Staleness labels rather than suppresses**: a stopped engine reports `cap_active`/`cap_max` **present**, `cap_observed_at` visibly old, and `engine_running=false`. Omitting was the one disposition that destroyed US-2 AS-5.
- **Q**: Is `Task.CreatedBy` an agent id or a username? → **A**: **Both, in this tree, today** — `callerID` at `pkg/tools/task.go:531`, `c.Username` at `pkg/gateway/rest_tasks.go:847` `[VERIFIED]`. It is therefore **not usable as an ownership predicate**, exactly as C4 concluded for `Plan.Owner`/`Plan.CreatedBy`. FR-037 adds `Task.CreatedByAgentID` and FR-010 reads that instead. The same defect is live in `list_tasks` today (A7).
- **Q**: Was FR-029's cost justification correct? → **A**: **No.** The citation resolved and the inference from it did not: `computeActiveLocked` has exactly one caller (`admitLocked`, under `pe.mu`), `Tick` never calls it, and it performs its own second store scan. "Refreshed on every `Tick`" would have added a scan plus a lock to the dispatch hot path forever. The snapshot is now published from inside `admitLocked` via one `atomic.Pointer` store, and SC-013 asserts **identity** with the number `Admit` used — an assertion a divergent re-derivation cannot satisfy.
- **Q**: Who edits `pkg/config/defaults_test.go`'s `wantToolCount`, and to what? → **A**: `plan-supervisor-spec.md` lands steps 1–3 first and takes it 83 → 85; this spec then **deletes the literal** and replaces it with `len(cfg.Sandbox.ToolPolicies) == len(coreagent.AllStaticToolNames())`, reaching 86 without hardcoding. Operator ruling 4: seeding is a rule, not a hand-maintained list.
- **Q**: Does PlanSupervisor see its own supervision work? → **A**: **No, and that is a recorded decision** (cross-spec C3, option (a)). It can never be a plan's `OwnerAgentID` and its session is not `delegate`-minted, so it appears on no roster. The engine's `supervision.wake_at` deadline is the only liveness control; parked plans are visible to the Owner alone, as `blocked` + `attention="elsewhere"`. Written into *Explicit Non-Behaviors* so it is not later "fixed" into a grant that would break the sibling spec's complement-complete invariant.

---

## Proposed ADR-056 amendments

Rev 2's findings that change ADR-056's own text, not just this spec's. Each is a **proposal** for the
ADR author — this spec does not amend the ADR unilaterally.

| ADR section | Current text | Proposed amendment | Source |
|---|---|---|---|
| §2 / D8 rationale | *"the real risk is silent permissive inheritance via `compositor.go:175-201`"* | Replace with the accurate four-branch rule **and add god-mode**: `if cfg.GodMode { return ToolPolicyAllow }` short-circuits the entire merge, so under sandbox `off` neither policy map is consulted. Neither the ADR, nor rev 1, nor the spec review modelled this branch. | C2 as corrected in rev 2 |
| D2 (cap pressure) | *"costs nothing extra… the data is free"* | The **signature** is free; the **call** is not (`Admit` takes `pe.mu` exclusively), and a locally re-derived count is **not the same number** — the cap is global while any caller-scoped derivation is not. State that cap pressure requires a dedicated lock-free snapshot accessor. | CRIT-001 / FR-029 |
| D3 (status vocabulary) | five values: `queued/running/blocked/failed/completed` | Keep the five, but add a companion `intentionally_stopped` boolean. The vocabulary has no slot for "deliberately stopped", so a user-initiated cancel and a crash are indistinguishable — and an agent that lost context will re-dispatch work the user deliberately stopped. | MAJ-011 / FR-006 |
| D3 (sort order) | `queued → running → blocked → failed → completed` | **No change** (operator ruling 1) — but note explicitly that the order makes the per-group sub-bounds load-bearing, since `blocked` sorts last of the three live groups. | Operator ruling 1 |
| D4 (subagent scoping) | *"the durable lifecycle parent linkage"* | No such field existed; a disk-only `ParentAgentID` is added with a **fail-closed mint**. Also state that `ParentDurableKey` is shared parent↔child and must never be used as a parent predicate. | C1 / FR-013 / FR-015 |
| D4 (task scoping) | delegator reading only | Amend to the `CreatedBy` ∪ `AgentID` union with an explicit `relation` field; the delegator-only reading makes an agent's own assigned work invisible. | MAJ-005 / FR-010 |
| §3 table + Option B "Strengths" | *"durable, working handle"* | Amend to **"durable enumeration, not durable acting"** — all `session_id`-bearing `delegate` actions resolve through a process-global in-memory map. (Carried forward from rev 1; still outstanding.) | C8 / FR-011 |
| §9 step 3 | one implementation site for the policy grant | **Four** sites: `allStaticToolNames`, the global `DefaultConfig()` seed, every `denyAllThenOverride` override map, **and** `tools.GeneralBuiltinMetadata()`. Also name the global-`deny` kill switch as the supported operator control. | C6 / FR-001 / FR-023 |
| §9 step 4 (REST parity) | contemplated as a follow-up | Note that its principal would be a **user**, not an agent, so FR-008's agent-id predicate would reject it — that endpoint needs a different predicate, and Constraint #8 **does** apply to it (unlike the tool surface). | Ambiguity #11 |
| New: operability | absent | Add an operability decision: a **persisted `pkg/audit` record per call** (the security control) kept **separate** from an optional Debug slog line (the debugging aid), Warn on degradation, a **per-call scan ceiling with a stated default and config key**, kill switch, runbook. **Explicitly reject a response memo** — keyed on the principal it is a cross-workspace disclosure, and keyed on the arguments it bounds no cost. | MAJ-013 / R2-MAJ-011 / R2-CRIT-001 / R2-CRIT-004 / FR-032 |

**Added by rev 3:**

| ADR section | Current text | Proposed amendment | Source |
|---|---|---|---|
| D3 (status vocabulary), again | five values + (rev 2's proposed) `intentionally_stopped` | Add a **second** companion field, `attention` (`none \| caller \| elsewhere`). `blocked` conflates three unrelated situations — a task whose dependency is unmet (informational; the operator's ruling), a plan being adjudicated by another agent (**must not be touched**), and work waiting on the caller (**must be**). One `status` value cannot carry that, and the composition with ADR-055's owner-held `stop_plan` turns the ambiguity into an agent that kills healthy work. | Operator ruling 3 / cross-spec M6 / FR-036 |
| D3 (sort order), intra-group | unspecified below the group level | State the **intra-group direction per group**: live groups `started_at` **ASC** (oldest first — the long-running work an agent has most likely forgotten, which is what D1's whole premise is about), terminal groups **DESC**. Truncating a DESC live group drops precisely the rows the tool exists to recover. | R2-MAJ-008 / FR-007 |
| D2 (cap pressure), mechanism | *"the data is free"* | Beyond rev 2's correction: the snapshot must be published **from inside `admitLocked`** via an `atomic.Pointer`, not refreshed from `Tick`. `computeActiveLocked` has exactly one caller, `Tick` never reaches it, and it performs its own second store scan — so a `Tick`-driven refresh is a **new full scan plus a new lock acquisition on the dispatch hot path, forever**. Also: state a **staleness bound** (90 s) and that staleness **labels rather than suppresses**, because a stopped engine is always stale and is the case the field exists for. | R2-MAJ-009 / R2-CRIT-003 / FR-029 |
| D4 (task scoping), namespace | delegator reading via `CreatedBy` | Beyond rev 2's union amendment: `Task.CreatedBy` is **mixed-namespace** (`c.Username` on REST, `callerID` on the tool path) and must not be an ownership predicate — the identical defect D4's own plan guidance already had. A new agent-id-namespaced `Task.CreatedByAgentID` is required. State the general rule: **only agent-id-namespaced fields may be authorization predicates, and empty never matches.** | R2-CRIT-005 / FR-037 |
| New: relationship to ADR-055 | ADR-056 does not reference ADR-055's phase vocabulary as a dependency | Record that ADR-056's `blocked` semantics are **owned by ADR-055's `PlanPhase` values**, that the phase is being renamed `awaiting_owner_correction` → `awaiting_supervision`, and that ADR-056's implementation must land **after** that rename. Also record that composed status strings must interpolate the exported constants, since a renamed enum value in a composed literal is invisible to the compiler and to a directory-scoped `rg` sweep. | Cross-spec C1 / landing order / FR-006a |
| New: PlanSupervisor roster-blindness | absent from both ADRs | Record the accepted consequence: the one agent that autonomously mutates running plans has **no** way to enumerate the plans it supervises, because it can never be a plan's `OwnerAgentID` and its session is not `delegate`-minted. Accepted (not a gap): the engine's `supervision.wake_at` deadline is the only liveness control. Recorded in **both** specs so it is not later "fixed" into a grant that breaks ADR-055's complement-complete tool-policy invariant. | Cross-spec C3 |
| §9 step 3, again | four implementation sites | **Five**, and the fifth should not be a number: `pkg/config/defaults_test.go`'s tool-count assertion must become a **mechanical invariant** (`len(ToolPolicies) == len(AllStaticToolNames())`) rather than a hardcoded count, or every future catalog addition collides on the same line. | Operator ruling 4 / cross-spec C2 / FR-023(e) |
