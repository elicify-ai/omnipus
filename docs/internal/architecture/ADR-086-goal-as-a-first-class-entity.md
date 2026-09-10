# ADR-086 — A goal is its own entity, with a definition phase and an active phase

- **Status:** Proposed (revision 2 — corrected against an adversarial review, 2026-09-10; five blockers and seven majors addressed, §7 records what changed and why. Revision 1 — 2026-09-09.)
- **Relates to:** ADR-081 (work-first goal flow), ADR-084 **revision 9** (the Judge as an active reviewer; revision 9 withdraws the three-state outcome), ADR-049/ADR-052/ADR-055 (task and plan adjudication), ADR-080 (criterion types and DoD provenance), ADR-057 (session unification, the striped session lock), ADR-054 D3 (`pkg/entity`, the per-entity store precedent)
- **Changes the ground under:** `docs/internal/specs/judge-active-reviewer-spec.md`, which currently assumes a chat goal lives on the session
- **Spec:** `docs/internal/specs/goal-entity-spec.md`

## 1. Operator direction (verbatim, 2026-09-09)

> plan might be slightly different but task and chat must be identical — the only difference is that the task makes the goal visible in the ui, the chat not. that must be the only difference

> no the chat does not become a task, that has more implications. we need only to separate the goal. what they have in common is from the time the goal is set until end of judgement — that part is the same

> that is right, but on task level when the task is started it has its own session, so here it becomes again the same. the task holds / references the goal definition until the session is started, so it is really identical — only how the goal is set is different

## 2. Evidence: today there are two implementations of one concept

A chat goal is **fourteen fields hung on a session** (`pkg/session/unified_meta_files.go::u5GoalFile`, plus `PendingAskJSON` riding in the same file for an unrelated reason); a task is a stored entity with its own record (`pkg/task/task.go`). They express the same ideas twice:

| Concept | Task | Chat goal |
|---|---|---|
| Identity | `id` | `goal_id` |
| What is wanted | `title`, `description`, `prompt` | `goal_condition` |
| Criteria | `criteria []AcceptanceCriterion` | `goal_criteria` — **a JSON string** |
| Attempt budget | `attempt_count` / `max_attempts` | `goal_rounds_used` / `goal_max_rounds` |
| Status | `status` (6-state) | derived from field presence, plus `goal_latest_reason` |
| Owner | `agent_id` | `goal_route_agent_id` |
| Workspace | `workspace_id` (required) | absent; resolved from the session |
| Where it lives | its own record | session metadata |
| Reachability | `notifySourceChannel` reads the task's own source fields | `goal_route_channel`, `goal_route_chat_id`, `goal_route_session_key` |

Three of today's defects are consequences of that split, not independent bugs:

- **The routing fields exist only because a goal has no identity.** It must remember which chat connection to address, which is precisely what failed on reconnect (ADR-082 §2): the stored `goal_route_chat_id` is a per-connection ephemeral id, so every keeper follow-up after a reconnect targeted a dead connection.
- **The criteria are a serialised blob**, which is why superseded-criteria history had to be added by hand (ADR-084 §6, fix-wave GX-B "fix 5b") and why a verdict has nowhere natural to attach.
- **The judging engine already treats the two as one.** `pkg/agent/judge.go::JudgeCriteria` takes a scope of `task` / `plan` / `goal` and one criteria list; `runGoalAdjudication`, `TaskExecutor.adjudicateClaim` and the plan engine all call it. Only the *storage* diverges — and the input type's own validation (`judge.go`, the scope switch that rejects `scope %q must not also carry TaskID/GoalSessionID`) forbids carrying a `TaskID` and a `GoalSessionID` together, which under this ADR becomes wrong: a running task's goal legitimately has both.

### 2.1 What is NOT true today — the correction that matters most

Revision 1 closed this section by claiming the operator's model was *"already half-true in the code"*, on the grounds that a task run mints a session. **That claim is false in the only sense that matters, and it made this ADR read as an act of naming rather than an act of building.** The verified position:

- `pkg/agent/goal_loop.go::applyGoalCommandPrompt` refuses to match on any turn where `opts.IsTaskRun` is set.
- `pkg/agent/goal_loop.go::checkGoalLoopAfterTurn` — the after-turn keeper hook — returns immediately when `opts.IsTaskRun` is set.
- Both gates were added deliberately, by prior review rounds, as **fail-closed origin gates**; their own comments say so.
- Nothing outside `goal_loop.go` and its immediate helpers ever writes `GoalCondition`. `pkg/agent/task_executor.go` contains the string **zero times**. Both goal drivers — the after-turn hook and the idle sweep in `goal_triggers.go::goalQuietWindowSettle` — gate on `GoalCondition != ""`, so a task session, which never has it set, is invisible to both.

**The goal keeper has never once run against a task.** Unification does not express an identity that already exists in the code; it **creates** one, by removing two guards that were installed on purpose. That is a larger and riskier change than revision 1 described, and D2a below states exactly what it switches on.

### 2.2 The seventeen differences, and which six are contradictions

Revision 1 described the two paths as "the same thing stored twice". Eleven of the seventeen observable differences are indeed duplication and merge cleanly. **Six are genuine behavioural contradictions** — the two paths do incompatible things, and unification must *choose*, not merge. The spec resolves each row individually; this table is the authority for that work.

| # | Concept | Chat goal (today) | Task (today) | Kind |
|---|---|---|---|---|
| 1 | Where the record lives | session meta, `goal.json` | the task record | merge |
| 2 | **Adjudication driver** | **two**: a claim hook after every turn, **and** a 60 s quiet-window idle keeper (`goalQuietWindowSettle` → `maybeSettleGoalIdle`) | **one**: a completion claim only (`adjudicateClaim`, from `finishTaskRun`) | **CONTRADICTION** |
| 3 | **Nudge ladder for a recordless goal** | 2 nudges (`goalNudgePrompt`), then an engine-authored fallback compile (`dispatchGoalFallbackCompile`) | none | **CONTRADICTION** |
| 4 | Zero-output continue-push | bounded at 2 (`goalContinuePushPrompt`, `GoalZeroOutputPushes`) | none | sub-case of #2 |
| 5 | Identity | `goal_id` | task `id` | merge |
| 6 | What is wanted | `goal_condition` (one string) | `title` + `description` + `prompt` | merge |
| 7 | **Empty-criteria handling** | synthesises a `goal-condition` prose criterion from the condition (`compiledGoalCriteriaFor`) | mints an **ephemeral, unpersisted** `soft-tier-implicit` criterion (`SoftTierCriterion`); if even that is empty, **trusts the claim and completes the task** | **CONTRADICTION** |
| 8 | Criteria representation | a JSON string | a typed `[]AcceptanceCriterion` | merge |
| 9 | **Budget** | 20 rounds; **no** per-goal override (`EffectiveGoalMaxRounds` takes no argument); **no** hard ceiling | 3 attempts; per-task `MaxAttempts` override; `2 × maxAttempts` hard ceiling (`consumeAttemptOrExhaust`) | **CONTRADICTION** |
| 10 | Attempts-used counter | `goal_rounds_used` | `attempt_count` | merge |
| 11 | Owner reference | `goal_route_agent_id` | `agent_id` | merge |
| 12 | **Terminal on MET** | `clearGoal(goalClearNoteMet)` — **erases the whole record**, criteria included | `completeTaskWithResult` → status `done`, **record kept** | **CONTRADICTION** |
| 13 | **Terminal on exhaustion** | `clearGoal("round bound reached …")` — **erases** | `consumeAttemptOrExhaust` → status `failed` + `FailedReason` + handover, **record kept** | **CONTRADICTION** |
| 14 | Workspace | absent on the goal; read from the session | required on the task | merge |
| 15 | Reachability | four `goal_route_*` fields | the task's own source-channel fields, via `notifySourceChannel` | merge (see D6) |
| 16 | Judge scope | `VerdictScopeGoal` | `VerdictScopeTask` | merge (same `JudgeCriteria` call) |
| 17 | Bare/unevidenced-claim brake | `goalBareClaimCostThreshold = 2`, in-memory streak map | `evidenceGateMaxConsecutiveRejections = 2`, in-memory streak map | merge (same number, two implementations) |

## 3. Decisions

### D1 — A goal is an entity, owned by exactly one owner

A `Goal` is stored in its own right, not as fields on a session and not as fields on a task. It carries **everything that is identical from the moment the goal is set until judgement ends**:

- identity, and the owner reference (owner kind + owner id)
- the condition (what is wanted), and the compiled definition/statement
- `criteria []AcceptanceCriterion` and `dod []AcceptanceCriterion` as **real lists**, using the existing shared type (ADR-080), never a serialised string
- the attempt budget and attempts used — **one pair of counters** (D10), replacing today's `attempt_count`/`max_attempts` on a task and `goal_rounds_used`/`goal_max_rounds` on a session
- **the keeper's own counters**: the recordless-nudge / zero-output-push streak (today `goal_zero_output_pushes`) and the clarification-question door (today `goal_question_rounds_used`). Revision 1's field list omitted both. They are durable goal state, not session state, and a goal that loses them on a re-home loses its loop bounds.
- status (D9), the latest reason, the claim, the verdict, and the superseded-criteria history
- `started_at`, `last_activity_at`
- the session it is active in, once it is active
- the surviving routing fields (D6)

**`PendingAskJSON` does NOT move with the goal.** It rides in `goal.json` today for storage-layout convenience only — it is the AskUserQuestion durable pending set, which belongs to the *session*, not to the goal, and outlives any goal on that session. When `goal.json` stops being the goal's home it must be relocated to a session-owned file, not carried into the goal entity. Revision 1 did not mention it; moving it silently would delete every parked question card on upgrade.

### D2 — Two phases: definition, then active

- **Definition.** The goal exists with its criteria and budget, and is not running. A task holds a goal in this phase from creation until the task starts.
- **Active.** The goal is bound to a session and the loop runs: work → claim → judgement (ADR-084 revision 9).

**In chat, the two phases collapse:** `/goal` creates the definition and activates it in the current session in one step (ADR-081 D1's instant activation is unchanged). **On a task, they are separated in time**: the definition is authored up front, dormant, and activates when the task starts and mints its session.

After activation the two are **identical** — same loop, same claim tool, same Judge, same budget accounting, same verdict. This is the operator's stated invariant and it is the point of the ADR.

### D2a — What unification switches on for tasks, stated before it is built

Because §2.1 establishes that the keeper has never run against a task, D2's "identical after activation" is a **new capability for the task path**, not a refactor of one. The operator has ratified it — *"tasks DO get the idle keeper; it is the most important piece"* — and it must be adopted with its cost visible. Removing the two `IsTaskRun` gates turns all of the following on for every running task:

1. **A 60-second quiet-window sweep over every task session.** `goalQuietWindowSettle` walks `ListSessions()` on each PlanEngine tick (30 s) and evaluates every goal-bearing session. Task sessions become goal-bearing, so the swept set grows by the number of concurrently running tasks.
2. **Claimless adjudication.** A quiet task is judged on persisted evidence with no claim text, where today it is judged only when it claims. Cost: one Judge turn per quiet window per task, subject to the suppressions in (5).
3. **The recordless nudge ladder** (row 3) — reachable only if a goal reaches the active phase with an empty record. Under D11 a task's goal cannot, so for a task-owned goal the ladder exists but is unreachable by construction. It is retained rather than special-cased, so the two paths stay one code path.
4. **The bounded zero-output continue-push** (row 4) — up to 2 free re-posts before a real round is spent.
5. **All of the keeper's existing suppressions come with it**, and they are what makes the cost bounded: a parked AskUserQuestion card, a `waiting_on_user` marker, an in-flight adjudication, a live turn (including any delegated descendant), the re-arm marker, and an exhausted token budget each suppress a fire. These are not optional extras — without them the keeper judges work that is visibly in progress.
6. **A global token-budget brake on a second path.** `maybeSettleGoalIdle` transitions a goal to failed on `TokenBudget().Exhausted()`. Task sessions gain that terminal transition.

**The honest cost:** a long-running, deliberately quiet task (one waiting on a slow external call that produces no transcript output and no commits) can now be adjudicated and, in the worst case, spend rounds against its budget while genuinely making progress. Suppression (5) covers the in-flight-turn case; it does not cover a task whose work is genuinely invisible to all three zero-output terms. The spec MUST carry a scenario for that case and state the chosen behaviour.

### D3 — The only difference is how the goal is set, and therefore who can see it before it runs

A task's goal is authored ahead of time and is therefore visible in the UI as part of the task. A chat goal is created at the moment of use and has no pre-run existence to display. **Visibility is a property of the owner, not of the goal**, and no other behavioural difference is permitted. Any future divergence between the two paths after activation is a defect, and the spec MUST carry a test that fails if one appears.

### D4 — The owner keeps what is genuinely its own

A task keeps its dependencies, assignee, board placement, plan membership and lifecycle. A chat session keeps its transcript and its connections. Neither becomes the other; the operator ruled that out explicitly.

### D5 — Criteria live on the goal, not duplicated on the owner

A task's `Criteria` field becomes a reference to its goal rather than a second list. Two lists of criteria on one task is exactly the duplication this ADR removes. **Plan is deliberately out of scope**: a plan's `DoD` sits above tasks and may stay as it is (operator: "plan might be slightly different").

`Task.Criteria` is not a leaf field. Its consumers must each be re-pointed, and the spec MUST enumerate them: `task_executor.go::adjudicateClaim`, `tools/task.go::deferDoneClaimToJudge` (and its `sysagent/tools/task.go` twin), `plan/lint.go::lintJoinlessConvergence`, `task.Patch.Criteria` and the write path through `pkg/task/store.go`, the wire converter `gateway/rest_tasks.go::toWireCriteria` (and its `rest_plans.go` mirror), and the SPA editor `AcceptanceCriteriaEditor.tsx`.

One consumer is special and must not be treated as a criteria store: `judge.go::SoftTierCriterion` mints an **ephemeral, never-persisted** criterion with the fixed id `soft-tier-implicit`. Any verdict projection (D8) writing back onto it is a silent no-op, because there is no record to write to.

### D6 — Routing, field by field

Revision 1 said "most routing fields dissolve" and left the reader to guess. The verified position, with each of the four decided:

| Field | Verdict | Evidence |
|---|---|---|
| `goal_route_session_key` | **DELETE — dead.** | Written by `recordGoalRouting` and read into `goalRoute.sessionKey` by `routeFor`, and then **never consumed by anything**. No dispatch reads it. |
| `goal_route_agent_id` | **FOLDS INTO THE OWNER.** | It is the goal's owning agent under another name; D1 carries an owner reference. |
| `goal_route_channel` | **SURVIVES.** | `dispatchGoalAsyncFollowUp` hard-aborts (`route.channel == "" \|\| route.chatID == ""` → return) without it. Every keeper follow-up on every non-webchat channel dies silently. |
| `goal_route_chat_id` | **SURVIVES.** | Same abort. ADR-082 made *webchat* delivery session-addressed; it did nothing for Telegram, Discord, Slack, Matrix, IRC, Google Chat or WhatsApp, where the chat id is the only address there is. |

**Revision 1's premise here was wrong in a second way**, and the spec must not inherit it: it implied a `GoalRouteSessionID` existed as the modern replacement. It does not — the identifier appears **nowhere** in the repository. It was never implemented. Two fields survive, and the reconnect defect ADR-082 §2 describes is a *webchat* defect that the surviving pair does not reintroduce for other channels.

### D7 — Greenfield, and the failure mode greenfield produces here

Consistent with the operator's standing directive (ADR-084 D6, "assume greenfield, remove migrations"): no migration of existing goals is built. Goals in flight at upgrade are not carried across.

**Stated as a behaviour, not a footnote:** the goal fields are read by `encoding/json` into a struct. Remove them from that struct and `json.Unmarshal` **drops the orphan keys silently** — no error, no log. Both goal drivers gate on `GoalCondition != ""`. So an in-flight goal at upgrade does not fail, does not terminate, and is not reported: it **ceases to exist mid-run**, with no terminal frame emitted and a goal pill left open in any connected SPA forever. The spec MUST specify the deliberate behaviour for this — at minimum a boot-time detection of orphaned goal state that emits one terminal frame and one operator-visible log line, so "greenfield" reads as *ended*, not as *vanished*.

### D8 — Goal progress becomes visible: the verdict is projected onto criterion status (operator-approved)

> *"yes to make goal progress visible"* — operator, 2026-09-09

`pkg/task/criterion.go` defines and validates three statuses — `CritPending`, `CritMet`, `CritUnmet` — and **only the first is ever written**. A repository-wide search finds 14 non-test writers of `CritPending` and **zero** non-test assignments of `CritMet` or `CritUnmet`. Every criterion therefore renders as pending in the UI regardless of what the Judge decided, at **task and plan level as well as chat**. The verdict is produced, persisted and logged; it simply never reaches the tick marks anyone looks at.

This is a pre-existing defect, not one this ADR introduces, and it is the reason goal work has felt opaque: even a correct verdict was invisible.

- When an adjudication records a verdict, each criterion's `status` MUST be set from that criterion's outcome — `met` → `CritMet`, `unmet` → `CritUnmet`.
- **This is one undivided piece of work.** ADR-084 revision 9 withdraws D2a in full: a verdict is met or unmet, `AcceptanceCriterion.status` keeps exactly `pending | met | unmet` where **pending means not yet judged**, and no fourth value is added anywhere. Revision 1's planned D8.1/D8.2 split existed only to stage a third value that no longer exists; it is deleted.
- **`met` does not mean "a machine check passed".** The standard is the Judge's reasoned conviction from evidence it examined — a competent human reviewer's standard (ADR-084 §10). A criterion with no check, no diff and no command is the normal case and is decided by the Judge reading the artifact and forming a view. The projection is a faithful copy of that judgement; it introduces no verifiability precondition of its own.
- There are **three write paths**, not one, because the criteria live in three different places: the goal record, the task record, and the plan member's `DoD`. The spec MUST enumerate all three. The goal path additionally has to **de-union** its input: `compiledGoalCriteriaFor` hands the Judge a flattened `Criteria ∪ DoD` list, so writing the verdict back requires splitting the result on the id namespace before persisting, or the DoD outcomes land on the criteria list.
- Constraint #8 applies: `AcceptanceCriterion.status` is a wire type with **four** copies — `contracts/components/schemas/AcceptanceCriterion.yaml`, `contracts/components/schemas/AcceptanceCriterionInput.yaml`, and two hand-synced inline duplicates inside `contracts/asyncapi.yaml` for `GoalStatusFrame.criteria[]` and `.dod[]`, which generate `status: z.enum(["pending","met","unmet"])` under `.strict()` (verified at `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts:837` and `:867`). `GoalStatusFrame.yaml` warns in its own description that edits must be mirrored there. **Missing either inline copy drops every goal-status frame at the SPA edge — the goal card blanks in production with a green `make verify-contracts`.**

### D9 — A terminal goal is a status transition, never an erasure (shipping precondition for D8)

`pkg/agent/goal_loop.go::clearGoal` zeroes **ten** goal fields in one `SetMeta` patch — id, condition, **criteria**, rounds used, max rounds, latest reason, started-at, last-activity-at, question-rounds-used and zero-output-pushes. `runGoalAdjudication` calls it **on a MET verdict** (`goalClearNoteMet`, `goal_triggers.go`), and `clearGoal` is also the terminal path for round exhaustion (two call sites), token-budget exhaustion (two call sites), multi-day idle expiry, and an explicit user clear — eight call sites in total.

So D8 as revision 1 wrote it would compute a verdict, write the ticks into the criteria list, and **delete that list one statement later**. The user sees nothing.

**Decision:** ending a goal is a **status transition on a retained record**, matching what the task path already does (`done` / `failed` + `FailedReason` + handover, record kept). The record survives the transition, carrying its criteria, their final statuses, the verdict and the reason. Erasure is deleted as a terminal mechanism; the only thing that removes a goal record is retention (D14) or the deletion of its owner. This is a shipping precondition for D8, not a companion improvement, and the operator has approved keeping goal records.

Terminal statuses must at minimum distinguish met, budget/round exhaustion, idle expiry, and an explicit user clear, because all four already produce distinct pill states today (`done` / `failed` / `cleared`) and the spec must not collapse them.

### D10 — One budget, overridable, and human-editable (operator-ratified)

Row 9 is resolved to a single budget for both paths: **default 20**, with a **per-goal override**. This is the chat number, not the task number; a task's 3 attempts was never a considered choice against the goal loop's 20 rounds.

The task path's `2 × maxAttempts` hard ceiling survives as a divergence brake — it exists because two independent gates can disagree, and unification does not remove that possibility.

**The missing human control is part of this decision.** `max_attempts` is settable on the wire and through the API today, but it is **display-only in the UI**: `TaskCard.tsx` renders it, `TaskDetailPanel.tsx` passes it down, and no input field exists anywhere in the SPA. Only an agent or a script can change a task's budget, which is backwards for a control whose whole purpose is an operator saying "give it more room". The spec MUST add the human-editable control.

### D11 — At least one acceptance criterion and one DoD item are mandatory (operator-ratified)

This is the operator's resolution of row 7. **A task cannot be created with an empty criteria set**, so the "trust an empty claim and complete the task" path stops existing rather than being defended.

The agent-facing path already enforces this: `pkg/tools/task.go` (and its `pkg/sysagent/tools/task.go` twin) hard-rejects `create_task` with zero criteria — *"criteria is required: an agent-created task must supply at least one acceptance criterion"*. The **human/UI path is the open one**: `CreateTaskSlideOver.tsx` submits `criteria` only when non-empty and its empty-state hint actively advertises the soft-tier fallback. D11 closes that path to match the agent path (SD-A7), and extends it to the DoD.

Note that `task.Task` has **no DoD field at all** today — only `Criteria`. The DoD half of this rule is satisfied by the goal record (which carries both lists, per ADR-080), which is another reason criteria belong on the goal and not on the owner.

**Tasks that already exist with no criteria** are not rewritten and are not blocked from running. They remain judged by the soft tier for the remainder of their life, and the mandatory rule applies at creation and at edit. The spec MUST state this explicitly, and MUST state what happens when such a task is opened for editing.

### D12 — A task-owned goal is exempt from the global active-loop cap (operator-ratified)

`PlanEngine.RegisterActiveCounter("goal", …)` counts active goals against `GlobalActiveLoopCap` (default 16, `pkg/config/planning.go`). Once every running task carries a goal (D2), that counter would be measuring something entirely different from what the cap was set for, and a modest board of running tasks would starve chat goals of admission.

Task-owned goals do not consume the "goal" admission slot. Task concurrency is already governed by the task executor's own dispatch limits; the goal cap continues to bound chat goals only.

### D13 — Goal settings live under Settings → Performance (operator-ratified)

No new settings tab. The Settings screen has eleven tabs and `PerformanceSection` already holds the operating limits; the goal budget default and the keeper's timing controls belong beside them.

### D14 — Goal records fall under session retention (operator-ratified)

A goal record is swept on the same retention rule and the same schedule as sessions. This closes the hazard that moving a goal off session metadata into its own store removes it from `UnifiedStore.RetentionSweep` and lets goal records accumulate without bound — which D9's "records are never erased" would otherwise guarantee.

## 4. Consequences

- One counter pair, one criteria list, one status vocabulary across chat and task.
- A verdict has somewhere to live, and a terminal goal keeps it (D9).
- `JudgeCriteriaInput`'s rule that `TaskID` and `GoalSessionID` are mutually exclusive becomes wrong and must be revisited.
- ADR-084's spec must be re-based: it currently assumes the chat goal's session-meta storage, including where the claim, the verdict and the superseded-criteria history are written.
- **Ordering: this work lands before ADR-084's remaining pieces.** Blast radius is the whole goal surface, not just chat, and ADR-084 revision 9 explicitly removes D8's dependency on it — the projection needs only `met` and `unmet`, both of which exist today.
- **ADR-084 D4's residual-risk acceptance must be re-derived, and it is this ADR's job to say so.** That acceptance rests on an asymmetry it states in plain terms: a false `met` at *task* scope dispatches dependent work holding `bash` and `write_file` and notifies the source channel, whereas *goal* scope is "comparatively benign (`runGoalAdjudication` → `clearGoal`, terminal)". D2 removes that asymmetry — under this ADR a goal is what a running task is judged against, so a false `met` on a goal *is* a false `met` on a task, with the full task-scope blast radius. The sentence D4 relies on stops being true the day this ships. ADR-084's acceptance is not void, but its stated basis is, and it must be re-argued on the strength of D2b–D2d and D10's closures alone.
- The keeper's per-session in-memory state becomes per-goal state (§5 Q6).

## 5. Open questions for the spec

1. Where a goal is stored — its own entity store (the `pkg/entity` precedent, ADR-054 D3) or alongside tasks — and what its id namespace is.
2. Whether a task may hold more than one goal over its lifetime (a re-run after failure: new goal, or same goal with a fresh attempt count?).
3. What happens to a goal whose owning task is deleted, and to one whose session is swept by retention (D14 sets the rule; the spec sets the mechanism).
4. Whether the plan's `DoD` should later converge on the same entity, or stay as it is (D5 defers this deliberately).
5. **Cardinality, and the six session-keyed maps.** `goalTriggerState` holds six maps keyed by session id — `bareClaimStreak`, `waitingOnUser`, `routing`, `idleSettling`, `diffBoundaryHash`, `outputWatermarks`. Their comments assert "session id *is* the goal id". Under D1 that stops being true (a task's goal exists before any session, and Q2 may allow a second goal on one owner). Every one of the six must be re-keyed to the goal id, and the spec must say what happens to an entry whose goal has not yet been bound to a session.
6. **The new lock order.** A goal write will need the goal's own store lock and, for anything that also touches session meta, the ADR-057 session shard. `pkg/session` uses a 64-shard striped lock with a one-directional order (`sessionLock(id)` → `cacheMu`) and forbids holding two session shards at once outside two named exceptions. `pkg/entity` layers a striped mutex over a sidecar `flock` whose **cross-process guarantee is POSIX-only** — `fileutil.WithFlock` is a documented no-op on Windows. The spec must fix the order between the goal lock and the session shard, and must state the Windows posture rather than inheriting it silently.
7. **The global active-loop cap.** D12 exempts task-owned goals. The spec must say what, if anything, then bounds the number of simultaneously active goals across the install, and whether the "goal" counter's meaning changes.
8. **The criterion id namespace.** Criterion ids are minted as UUIDs by `NormalizeCriteria`, with three reserved non-UUID ids already in use: `soft-tier-implicit` (`judge.go::softTierCriterionID`), `goal-condition` (`goal_compile.go`'s condition fallback), and the `goal-dod-floor-*` prefix. D8's de-union depends on being able to tell a criterion from a DoD item by id, so the namespace must be specified rather than assumed.

## 6. Rejected alternatives

- **Make the chat session a task.** Ruled out by the operator explicitly ("that has more implications").
- **Keep the two implementations and add a shared interface.** This is what exists today via `JudgeCriteria`, and it is precisely what leaves the six contradictions in §2.2 unresolved: a shared judging call over two divergent stores and two divergent terminal behaviours.
- **Resolve row 9 toward the task's 3 attempts.** Rejected: 3 was chosen for a task's dispatch retry loop, not for a goal's round loop, and applying it to chat would cut the chat budget by 85 % as a side effect of a storage change.

## 7. What revision 2 corrected

| # | Revision 1 claimed | Corrected to |
|---|---|---|
| B1 | The operator's model is "already half-true in the code" | §2.1 — false. Two `IsTaskRun` fail-closed gates fence the keeper out of task runs, and `task_executor.go` never writes `GoalCondition`. Unification **creates** the identity. D2a enumerates what it switches on. |
| B2 | The two paths are "the same thing stored twice" | §2.2 — seventeen differences, of which **six are contradictions** requiring a chosen behaviour, not a merge. |
| B3 | D8 writes verdicts onto criterion status | D9 — `clearGoal` deletes the criteria on a MET verdict one statement after the verdict is recorded. Terminal state becomes a **status transition on a retained record**. |
| B4 | D8 splits into D8.1/D8.2 pending a third status value | Deleted. ADR-084 revision 9 withdraws the three-state outcome; D8 is one undivided piece of work. |
| B5 | (silent) | §4 — ADR-084 D4's risk acceptance rests on goal scope being "comparatively benign … terminal". D2 removes that asymmetry; the basis must be re-derived. |
| M1 | "Most routing fields dissolve"; `GoalRouteSessionID` implied to exist | D6 — field by field. One dead, one folds into the owner, **two survive**; `GoalRouteSessionID` appears nowhere in the repository. |
| M2 | `Task.Criteria` becomes a reference | D5 — six consumer sites enumerated, plus the ephemeral `SoftTierCriterion` no-op hazard. |
| M3 | D1's field list | D1 — adds the two keeper counters; relocates `PendingAskJSON` explicitly rather than carrying it along. |
| M4 | (silent on the budget conflict) | D10 — one budget, default 20, per-goal override, hard ceiling retained, **human-editable control added**. |
| M5 | Five open questions | §5 — four added: map cardinality/re-keying, lock order incl. the POSIX-only flock, the active-loop cap, the criterion id namespace. |
| M6 | "The writer runs where the verdict is recorded" | D8 — **three** write paths; the goal path must de-union `compiledGoalCriteriaFor`'s flattened `Criteria ∪ DoD`. |
| M7 | D7 "greenfield" | D7 — states the actual failure: orphan keys are dropped silently, both drivers gate on `GoalCondition != ""`, so an in-flight goal **ceases to exist** with no terminal frame. |
| m1 | Cites ADR-084 revision 7 | Cites **revision 9** throughout. |
