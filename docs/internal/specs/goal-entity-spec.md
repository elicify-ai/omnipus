# Goal as a First-Class Entity — Specification

- **Implements:** [ADR-086 revision 2](../architecture/ADR-086-goal-as-a-first-class-entity.md)
- **Depends on:** ADR-081 (work-first goal flow), ADR-084 **revision 9** (the Judge as an active reviewer; §10 withdraws the three-state outcome), ADR-080 (criterion types and DoD provenance), ADR-057 (session unification and the striped session lock), ADR-054 D3 (`pkg/entity`), ADR-082 (session-bound streaming)
- **Status:** Draft for review — 2026-09-10
- **Branch of record:** `feat/adr-081-work-first-goal`, verified against commit `3ec8a842`
- **Written non-interactively.** The `/plan-spec` confirmation gates (Phase 1, Phase 5.5) were not put to a human. Every question that would have been asked is recorded in §11 with the assumption taken in its place.

---

## 1. Problem & Actors (Phase 1)

### The problem

The same idea — *"here is what I want, here is how we will know it is done, keep going until a judge agrees"* — is built twice in this product. In chat it is a set of fields hung on a session. On a task it is part of the task record. The two do not merely store the idea differently; they **behave** differently at six decision points, and one of those differences destroys the very thing the operator asked to make visible: when a chat goal is judged met, the record of what was met is deleted in the same breath.

The operator's instruction is that after a goal starts running, chat and task must be **identical**, and the only permitted difference is that a task's goal can be seen before it runs.

### Actors

| Actor | Interest |
|---|---|
| **Operator (human)** | Sets a goal in chat or on a task; watches progress; wants to see which criteria are satisfied; occasionally wants to give a goal more room to finish. |
| **Working agent** | Authors or receives a goal record; works; claims completion. |
| **The Judge** | Reads the evidence, forms a view, returns met or unmet per criterion. |
| **The keeper** | The engine-side loop that notices a goal has gone quiet and decides whether to nudge, push, or judge. |
| **Channel user** | Reaches a goal-running agent over Telegram, Discord, Slack, Matrix, IRC, Google Chat or WhatsApp, and must keep receiving the keeper's follow-ups. |

### Scope

**In scope:** the goal entity and its store; the definition and active phases; the two entry points; the six behavioural contradictions; the terminal-status transition; criteria moving off the task; the four routing fields; retention; the settings surface and the human-editable budget; the mandatory-criteria rule; the active-loop cap exemption; and the projection of a verdict onto criterion status.

**Out of scope:** a plan's `DoD` converging on the same entity (ADR-086 D5 defers it); any change to how the Judge reasons (ADR-084 owns that); any migration of in-flight goals (ADR-086 D7).

### Constraints

- Single Go binary, pure Go, file-based storage (`CLAUDE.md` constraints 1–3).
- Contract-first wire formats (constraint 8) — every cross-boundary shape is defined in `contracts/` before any code.
- Two-layer tool policy is untouched (constraint 6).
- Greenfield: no migration path is built.
- **This machine cannot run the full Go gateway test suite.** Verification is by scoped `-run` invocations locally and by CI for anything broader.

### The behaviour a human would use to judge success

An operator sets a goal in chat and one on a task. Both run. Both show ticks appearing against individual criteria as the Judge decides them. Both end with a record that is still there afterwards, saying what was met and why. Neither behaves differently from the other in any respect the operator can name, except that the task's goal was visible on the board before the task started.

---

## 2. Existing Codebase Context (Phase 1.5)

Verified by direct reading at `3ec8a842`. GitNexus was not consulted for this pass; every citation below was read in the file named.

### Symbols involved

| Symbol | Role | Note |
|---|---|---|
| `pkg/session/unified_meta_files.go::u5GoalFile` | modified | The 14 `Goal*` fields plus `PendingAskJSON`, persisted as `goal.json`. Its own doc comments still say "the 9 Goal\* fields" — stale by five. |
| `pkg/agent/goal_loop.go::applyGoalCommandPrompt` | modified | `/goal` entry. Carries the first `opts.IsTaskRun` fail-closed gate. |
| `pkg/agent/goal_loop.go::checkGoalLoopAfterTurn` | modified | The after-turn claim driver. Carries the second `IsTaskRun` gate and the `meta.GoalCondition == ""` fast path. |
| `pkg/agent/goal_loop.go::clearGoal` | **deleted as a terminal mechanism** | Zeroes ten goal fields in one `SetMeta`. Eight call sites. |
| `pkg/agent/goal_triggers.go::goalQuietWindowSettle` | modified | The idle sweep. Skips every session where `GoalCondition == ""`. |
| `pkg/agent/goal_triggers.go::maybeSettleGoalIdle` | modified | The per-goal idle evaluation and all six suppressions. |
| `pkg/agent/goal_triggers.go::runGoalAdjudication` | modified | Calls `clearGoal(goalClearNoteMet)` on a met verdict. |
| `pkg/agent/goal_triggers.go::goalTriggerState` | modified | Six session-keyed maps. |
| `pkg/agent/goal_triggers.go::routeFor` | modified | Reads the four `GoalRoute*` fields; consumes only two. |
| `pkg/agent/goal_triggers.go::dispatchGoalAsyncFollowUp` | modified | Hard-aborts when channel or chat id is empty. |
| `pkg/agent/task_executor.go::adjudicateClaim` | modified | The task's only adjudication driver. Contains the trust-the-claim path. |
| `pkg/agent/task_executor.go::consumeAttemptOrExhaust` | modified | Sole writer of `AttemptCount`; owns the `2 × maxAttempts` hard ceiling. |
| `pkg/agent/judge.go::SoftTierCriterion` / `softTierCriterionID` | referenced | Mints an ephemeral, unpersisted criterion. |
| `pkg/agent/goal_compile.go::compiledGoalCriteriaFor` | modified | Flattens `Criteria ∪ DoD` for the Judge. The de-union point. |
| `pkg/agent/goal_record_wiring.go::WriteRecord` | modified | The `set_goal` write path into session meta. |
| `pkg/task/criterion.go` (`CritPending`/`CritMet`/`CritUnmet`, `IsValidCriterionStatus`) | referenced | Three statuses; only `CritPending` is ever written. |
| `pkg/task/store.go` (`Patch.Criteria`, the normalize-on-write path) | modified | |
| `pkg/gateway/rest_tasks.go::toWireCriteria` (+ the `rest_plans.go` mirror) | modified | |
| `pkg/gateway/goal_status_criteria.go::setGoalStatusCriteria` | referenced | Already passes `src.Status` through — the projection reaches the wire the moment a status is written. |
| `pkg/config/planning.go` (`DefaultGoalMaxRounds`, `EffectiveGoalMaxRounds`, `DefaultTaskMaxAttempts`, `EffectiveTaskMaxAttempts`, `DefaultGlobalActiveLoopCap`) | modified | |
| `pkg/session/retention_sweep.go::RetentionSweep` | extended | Takes all 64 shards in ascending index order. |
| `pkg/entity/store.go` | referenced | The per-entity store precedent; sidecar flock, POSIX-only cross-process. |
| `src/components/workspaces/CreateTaskSlideOver.tsx` | modified | Submits `criteria` only when non-empty; advertises the soft tier in its empty hint. |
| `src/components/workspaces/AcceptanceCriteriaEditor.tsx` | modified | |
| `src/components/workspaces/TaskCard.tsx` / `TaskDetailPanel.tsx` | modified | Render `max_attempts`; no input exists anywhere. |
| `src/components/settings/PerformanceSection.tsx` | extended | |
| `src/components/chat/GoalEchoCard.tsx`, `GoalPillTray.tsx`, `GoalIndicator.tsx` | modified | |

### Impact assessment

| Symbol modified | Risk | Direct dependents | Why |
|---|---|---|---|
| `clearGoal` | **HIGH** | 8 call sites across `goal_loop.go` and `goal_triggers.go` | Every terminal path for a chat goal. Replacing erasure with a transition changes what six of them mean. |
| `checkGoalLoopAfterTurn` / `applyGoalCommandPrompt` | **HIGH** | The whole task-run path, previously fenced out | Removing the `IsTaskRun` gates is the single change that switches the keeper on for tasks. |
| `Task.Criteria` | **HIGH** | 6 consumer sites (see FR-030) | A silently-missed consumer reads an empty list and judges against the soft tier without saying so. |
| `AcceptanceCriterion.status` on the wire | **HIGH** | 4 contract copies | Missing an inline copy blanks the goal card in production with a green `make verify-contracts`. |
| `goalTriggerState`'s six maps | MEDIUM | `goal_triggers.go` only | Re-keying is mechanical but the maps' own comments assert the identity being broken. |
| `u5GoalFile` | MEDIUM | `pkg/session` read/write/compose paths | Orphan keys drop silently. |
| `EffectiveGoalMaxRounds` | LOW | `runGoalAdjudication`, `handleBareGoalClaim` | Signature gains an override argument. |

### Cluster placement

Spans **agent loop / goal**, **task execution**, **session storage**, **gateway wire contracts** and **SPA**. That five-cluster span is itself the argument for the wave plan in §12: the storage and entity waves must land and be green before the behavioural waves touch them.

---

## 3. User Stories (Phase 2)

### US-1 — One goal, whichever way it was set (P0)

An operator sets a goal by typing `/goal` in chat, or by creating a task with a definition of done. From the moment the work starts, the two behave the same way: the same loop, the same budget, the same judge, the same record of what happened.

**Why P0:** it is the operator's stated requirement and the reason the ADR exists.
**Independent test:** run the same goal text through both entry points and diff the observable behaviour; anything but "identical after activation" fails.

1. **Given** a goal set in chat and an equivalent goal set on a task, **When** both reach the active phase, **Then** every observable loop behaviour is the same.
2. **Given** a task's goal, **When** the task has not started, **Then** the goal exists, is visible, and is not running.
3. **Given** a chat goal, **When** it is created, **Then** it is running immediately.

### US-2 — A task's goal is visible before it runs (P0)

Because a task's goal is authored ahead of time, the operator can see and edit what the task will be judged against before anything starts.

**Why P0:** it is the one permitted difference; if it is absent, the ADR delivers nothing the operator asked for.
**Independent test:** open a task that has not started and read its criteria and definition of done.

1. **Given** an unstarted task, **When** the operator opens it, **Then** its criteria and definition of done are shown.
2. **Given** an unstarted task, **When** the operator edits its criteria, **Then** the edit is what the task is later judged against.
3. **Given** a chat goal, **When** the operator looks for it before it was set, **Then** there is nothing to show, and that is not an error.

### US-3 — The keeper watches tasks too (P0)

A task that has gone quiet is noticed, nudged and eventually judged, exactly as a quiet chat goal is. The operator described this as the most important piece of the change.

**Why P0:** operator-ratified; it is the behaviour that turns a task from "dispatched and hoped for" into "kept".
**Independent test:** start a task, let it go quiet, and observe the keeper act without any human intervention.

1. **Given** a running task whose goal is quiet past the quiet window, **When** the keeper sweeps, **Then** it acts on that task.
2. **Given** a running task with a turn in flight, **When** the keeper sweeps, **Then** it does not act.
3. **Given** a running task whose operator was asked a question and has not answered, **When** the keeper sweeps, **Then** it does not act.
4. **Given** a running task producing no observable output, **When** the keeper sweeps twice, **Then** it pushes rather than judging, up to a bound, and then judges.

### US-4 — A finished goal stays on the record (P0)

When a goal ends — met, out of budget, expired, or cleared by the operator — the record remains, saying what happened, which criteria were satisfied and why.

**Why P0:** without it the projection in US-5 writes into something that is deleted a statement later, and the whole visibility change is inert.
**Independent test:** run a goal to a met verdict and read the record afterwards.

1. **Given** a goal that reaches a met verdict, **When** it ends, **Then** the record persists with its criteria, their final statuses and the verdict.
2. **Given** a goal that exhausts its budget, **When** it ends, **Then** the record persists with a failure reason and a handover.
3. **Given** a goal cleared by the operator, **When** it ends, **Then** the record persists and is distinguishable from a met or exhausted one.
4. **Given** any ended goal, **When** the operator looks at the chat or the board, **Then** one terminal signal was emitted and no pill is left open.

### US-5 — Ticks that move (P0)

Each acceptance criterion shows whether it has been met, not met, or not yet judged. Today every one of them shows "not yet judged" forever, whatever the Judge decided.

**Why P0:** it is the operator's "make goal progress visible", and it is a pre-existing defect affecting task and plan as well as chat.
**Independent test:** run any adjudication and watch an individual criterion change state.

1. **Given** an adjudication returning met for one criterion and unmet for another, **When** the verdict is recorded, **Then** the two criteria render differently.
2. **Given** a criterion never yet judged, **When** it renders, **Then** it shows as not yet judged.
3. **Given** a goal whose judged set was the union of its criteria and its definition of done, **When** the verdict is recorded, **Then** each outcome lands on the list it came from.

### US-6 — The Judge judges; it does not run a checklist (P0)

A criterion can be met on the Judge's reading of an artifact alone, with nothing mechanical involved. Most criteria are like this. A deck having the five sections it was asked for, an email's tone suiting its audience, a summary being genuinely useful to a non-specialist — all of these are ordinary passes.

**Why P0:** ADR-084 §1 and §10. An implementation that requires a machine-checkable form has failed the ADR regardless of what else it satisfies.
**Independent test:** write a criterion with no check, no command and no file path, and have it reach met.

1. **Given** a criterion with no check, no diff and no command, **When** the Judge reads the artifact and is persuaded, **Then** the criterion is met.
2. **Given** two criteria, one with a machine check and one purely subjective, **When** both are judged, **Then** neither is preferred, deferred, or handled by a different path.
3. **Given** a criterion the Judge looked for and could not find evidence of, **When** it is judged, **Then** it is unmet, and the reason says what was looked for and where.
4. **Given** a met verdict, **When** it is recorded, **Then** it points at something the Judge actually opened.

### US-7 — No task can be created without a definition of done (P0)

A task created through the interface requires at least one acceptance criterion and at least one definition-of-done item, exactly as an agent-created task already does.

**Why P0:** it is the operator's resolution of the empty-criteria contradiction; it removes the "trust the claim and complete" path by removing its precondition.
**Independent test:** try to create a task with no criteria through the interface.

1. **Given** the create-task form with no criteria, **When** the operator submits, **Then** creation is refused with a reason.
2. **Given** the create-task form with a criterion but no definition-of-done item, **When** the operator submits, **Then** creation is refused with a reason.
3. **Given** a task created before this rule with no criteria, **When** it runs, **Then** it still runs and is judged as it was.

### US-8 — An operator can give a goal more room (P1)

A goal that is close but out of rounds can be given a larger budget by a human, in the interface, without an agent or a script.

**Why P1:** the control exists on the wire today; only the human surface is missing. Valuable but not a correctness blocker.
**Independent test:** change a goal's budget in the interface and observe the loop honour it.

1. **Given** a goal with the default budget, **When** the operator raises it, **Then** the loop runs to the new number.
2. **Given** a budget field, **When** the operator enters a value below one, **Then** it is refused.
3. **Given** a goal with no override, **When** it runs, **Then** it uses the configured default.

### US-9 — The keeper still reaches non-webchat channels (P0)

A goal set from Telegram, Discord, Slack, Matrix, IRC, Google Chat or WhatsApp keeps receiving the keeper's follow-ups after the change.

**Why P0:** removing the wrong two routing fields silently kills every keeper follow-up outside webchat, with a warning log and no user-visible symptom.
**Independent test:** run a goal from a non-webchat channel and confirm an idle follow-up arrives.

1. **Given** a goal set from a non-webchat channel, **When** the keeper dispatches a follow-up, **Then** it arrives on that channel.
2. **Given** a webchat goal after a reconnect, **When** the keeper dispatches a follow-up, **Then** it arrives on the live connection.
3. **Given** a goal whose routing cannot be resolved at all, **When** the keeper tries, **Then** it says so on the record rather than failing silently.

### US-10 — Goals do not pile up forever (P1)

Because records are now kept rather than erased, they are swept on the same rule and the same schedule as sessions.

**Why P1:** an unbounded store is a slow failure, not an immediate one.
**Independent test:** age a goal record past the retention window and sweep.

1. **Given** a goal record older than the retention window, **When** the sweep runs, **Then** it is removed.
2. **Given** a goal record inside the window, **When** the sweep runs, **Then** it is kept.
3. **Given** a goal whose owning task is deleted, **When** the deletion completes, **Then** the goal record does not outlive its owner unreferenced.

### US-11 — A board of running tasks does not starve chat goals (P1)

Task-owned goals do not consume the admission slots that bound chat goals.

**Why P1:** the failure is a starved chat goal, which is visible and recoverable, not data loss.
**Independent test:** run more concurrent tasks than the cap and set a chat goal.

1. **Given** more running tasks than the goal admission cap, **When** the operator sets a chat goal, **Then** it is admitted.
2. **Given** the goal admission cap reached by chat goals alone, **When** another chat goal is set, **Then** it is bounded as before.

### US-12 — An in-flight goal at upgrade ends visibly (P1)

Greenfield means an in-flight goal does not survive the upgrade. It must therefore be seen to **end**, not to vanish.

**Why P1:** it affects one boot per install, but the failure mode is a permanently open pill and a user who believes work is still running.
**Independent test:** upgrade an install with a live goal and watch the chat.

1. **Given** an install with an in-flight goal, **When** it upgrades, **Then** one terminal signal is emitted for that goal.
2. **Given** the same upgrade, **When** it completes, **Then** one operator-visible log line names what was ended.
3. **Given** an install with no in-flight goal, **When** it upgrades, **Then** nothing is emitted and nothing is logged.

### Edge cases

| # | Situation | Expected |
|---|---|---|
| EC-1 | A task is started, stopped, and re-run | §11 A-2: the same goal is reused with its attempts reset; the prior verdict history is retained. |
| EC-2 | A task-owned goal reaches the active phase with an empty record | Impossible under US-7 for new tasks; for a pre-rule task the nudge ladder applies as it does in chat. |
| EC-3 | A task genuinely working but producing no transcript output, no commits and no tool evidence | The zero-output push fires up to its bound, then a real round is spent. Recorded as an accepted cost. |
| EC-4 | A goal's owning task is deleted mid-run | The goal transitions to a terminal state and is removed with its owner. |
| EC-5 | Two criteria, one from the criteria list and one from the definition of done, carrying the same text | Both are judged; outcomes land on their own lists; the id namespace keeps them apart. |
| EC-6 | The Judge is unavailable at a terminal boundary | No round consumed, no terminal transition, the goal re-arms — unchanged from today. |
| EC-7 | A criterion whose id is one of the reserved non-UUID ids | Reserved ids are enumerated and never minted for a persisted criterion. |
| EC-8 | The budget is lowered below the attempts already used | The goal terminates at the next boundary as exhausted; already-used attempts are not rewritten. |
| EC-9 | The gateway runs on Windows | In-process protection only for the goal store; stated, not silently inherited. |
| EC-10 | A goal record exists whose active session was swept by retention | The goal is terminal-expired at the next sweep rather than left pointing at a missing session. |

---

## 4. Behavioral Contract & Boundaries (Phase 2.5)

### Behavioral contract

- When a goal is set in chat, the system creates it and starts it in one step.
- When a task is created, the system creates its goal in a not-yet-running state.
- When a task starts, the system starts its goal against the session that task mints.
- When a goal is running, the system judges it on a completion claim **and** after a quiet period, whichever comes first.
- When a goal is running and quiet and the operator has been asked something, the system does nothing.
- When a goal is running and a turn is in flight, the system does nothing.
- When a running goal produces nothing observable, the system pushes it to continue, up to a bound, then judges it.
- When the Judge returns a per-criterion outcome, the system records that outcome against that criterion.
- When a criterion has never been judged, the system shows it as not yet judged.
- When a goal reaches a met verdict, the system marks it met, keeps the record, and emits one terminal signal.
- When a goal exhausts its budget, expires, or is cleared, the system marks it accordingly, keeps the record, and emits one terminal signal.
- When an operator raises a goal's budget, the system runs to the new number.
- When a task is created without at least one acceptance criterion and one definition-of-done item, the system refuses.
- When a goal record ages past the retention window, the system removes it.
- When the system starts and finds goal state it can no longer interpret, it ends that goal visibly and says so once.

### Explicit non-behaviours

- **The system must not require a criterion to have a machine-checkable form in order to be eligible for met**, because ADR-084 §1 and §10 make the Judge's reasoned conviction the standard; a checklist evaluator fails the ADR whatever else it satisfies.
- **The system must not prefer, prioritise, defer, or route differently** a criterion that has a check over one that does not, for the same reason. There is one path.
- **The system must not introduce a fourth criterion status**, because ADR-084 revision 9 withdraws the three-state outcome; a verdict is met or unmet and `pending` means not yet judged.
- **The system must not delete a goal's criteria as part of ending it**, because the record of what was met is the deliverable.
- **The system must not trust a completion claim because there was nothing to judge**, because US-7 removes the precondition for that path.
- **The system must not carry the parked-question set into the goal entity**, because it belongs to the session and outlives any goal on it.
- **The system must not build a migration**, because the operator's greenfield directive stands — but it must not let an in-flight goal disappear unannounced either.
- **The system must not count task-owned goals against the chat-goal admission cap**, because the cap was set for a different population.
- **The system must not let a `verify-contracts` pass stand in for verifying the two inline contract duplicates**, because that check is green while the goal card is blank.

### Machine-verifiable constraints

| # | Constraint |
|---|---|
| MV-1 | A goal's default budget is **20**. A per-goal override below 1 is rejected. |
| MV-2 | The hard ceiling on attempts is **2 ×** the effective budget. |
| MV-3 | The idle quiet window is **60 s**, evaluated on the 30 s engine tick. |
| MV-4 | The recordless-nudge bound and the zero-output-push bound are both **2**. |
| MV-5 | The unevidenced-claim brake threshold is **2** for both paths. |
| MV-6 | Creating a task through the API or the interface with fewer than 1 acceptance criterion **or** fewer than 1 definition-of-done item returns **HTTP 400** with a message naming which is missing. |
| MV-7 | `AcceptanceCriterion.status` accepts exactly `pending`, `met`, `unmet` in all **four** contract copies. |
| MV-8 | A terminal goal emits exactly **one** terminal frame. |
| MV-9 | Goal records older than the configured session retention window are removed by the sweep. |
| MV-10 | The number of admission slots consumed by task-owned goals is **0**. |

### Integration boundaries

| System | Data in / out | Failure behaviour |
|---|---|---|
| **LLM provider (Judge)** | Criteria + evidence out; per-criterion outcomes in | Unavailable → no round consumed, no terminal transition, re-arm. Unchanged. |
| **Session store** | Active-session binding, transcript reads, retention | A missing session for an active goal is terminal-expired (EC-10), never a silent dangling reference. |
| **Task store** | Owner reference, status, attempts | A deleted owner takes its goal with it (EC-4). |
| **Channels (async notifier)** | Keeper follow-ups out | Unresolvable routing writes a reason onto the record and logs; it does not retry blindly. |
| **SPA (WebSocket)** | Goal status frames with criteria and DoD | A frame failing zod validation is dropped at the edge with a counter; the four contract copies are what prevent that. |

---

## 5. The seventeen differences, resolved row by row

This section is the core of the specification. ADR-086 §2.2 establishes that eleven rows merge and **six are contradictions**. Each contradiction below carries a chosen behaviour and the reason it was chosen over the alternative.

The six contradictions are rows **2, 3, 7, 9, 12, 13**. Row 4's two sub-behaviours — the zero-output push and the quiet-window re-post — belong to row 2's resolution and are called out separately because they are separately observable.

### Row 2 — Adjudication driver → **resolved toward chat** (FR-015…FR-019)

| | |
|---|---|
| **Chat today** | Two drivers: an after-turn claim hook (`goal_loop.go::checkGoalLoopAfterTurn`) and a 60 s quiet-window idle keeper (`goal_triggers.go::goalQuietWindowSettle` → `maybeSettleGoalIdle`). |
| **Task today** | One driver: a completion claim (`task_executor.go::adjudicateClaim`, reached from `finishTaskRun`). No idle path exists. |
| **Chosen** | Both drivers apply to both paths. The two `opts.IsTaskRun` gates are removed. |
| **Why** | Operator-ratified: *"tasks DO get the idle keeper — it is the most important piece."* Resolving the other way would delete the keeper from chat, which is the behaviour that makes a goal a goal rather than a note. A task with only a claim driver is a task that stops silently and is never noticed. |
| **Cost, stated** | The swept set grows by the number of concurrently running tasks. A quiet task can be judged on persisted evidence with no claim, and in the worst case spend rounds while genuinely progressing invisibly (EC-3). The six suppressions in FR-016 are what keep this bounded and are not optional. |

### Row 3 — Recordless nudge ladder → **retained for both, unreachable for a task** (FR-020)

| | |
|---|---|
| **Chat today** | Two nudges (`goal_triggers.go::goalNudgePrompt`, bounded by `goalZeroOutputPushMax`), then an engine-authored fallback compile (`dispatchGoalFallbackCompile`). |
| **Task today** | Nothing. |
| **Chosen** | The ladder is one code path serving both. For a task-owned goal it is unreachable by construction, because FR-047 makes the record non-empty before the task can exist. |
| **Why** | Special-casing it out for tasks would reintroduce exactly the two-implementations problem this ADR removes, and would leave pre-rule criteria-less tasks (FR-048) with no ladder at all. Unreachable-by-construction is stronger than branched-around, and it is testable: a test asserts the ladder is never entered for a task-owned goal. |

### Row 4a (sub-case of row 2) — Zero-output continue-push → **applies to tasks** (FR-017)

Bounded at 2 free re-posts (`settleZeroOutputRecordedGoal`, `goalContinuePushPrompt`) before a real round is spent. Chosen because the alternative — judging a task the first time it looks quiet — is precisely the ADR-084 E1 failure the push exists to avoid.

### Row 4b (sub-case of row 2) — Quiet-window re-post → **applies to tasks** (FR-018)

The re-arm marker (`idleSettling`) means one fire per quiet spell, re-arming only on genuine external activity. Chosen for the same reason as row 4a; without the marker the keeper fires every tick inside one quiet spell.

### Row 7 — Empty-criteria handling → **resolved by making the state impossible** (FR-021…FR-023)

| | |
|---|---|
| **Chat today** | `goal_compile.go::compiledGoalCriteriaFor` synthesises a single prose criterion with the fixed id `goal-condition` from the goal's own condition text. |
| **Task today** | `task_executor.go::adjudicateClaim` mints an **ephemeral, never-persisted** criterion via `judge.go::SoftTierCriterion` (fixed id `soft-tier-implicit`) from title, description or prompt. **If even that is empty, the claim is trusted and the task is completed.** |
| **Chosen** | The empty set stops existing. FR-047 makes at least one acceptance criterion and one definition-of-done item mandatory in the interface, matching the hard rejection `pkg/tools/task.go` already applies to agent-created tasks. The trust-the-claim branch is deleted. |
| **Why** | This is the operator's own resolution. Choosing chat's synthesis instead would mean judging a task against a restatement of its own title, which is the weakest possible criterion and is indistinguishable from trusting the claim; choosing the task's soft tier would mean a chat goal could complete on the agent's say-so. Removing the precondition is the only resolution that does not weaken one side. |
| **Legacy** | Tasks that already exist with no criteria are **not rewritten and not blocked**. They continue to be judged by the soft tier. The rule binds at creation and at edit (FR-048). |

### Row 9 — Budget → **resolved toward chat's number, task's mechanics** (FR-024…FR-026)

| | |
|---|---|
| **Chat today** | `config.DefaultGoalMaxRounds = 20`. `PlanningConfig.EffectiveGoalMaxRounds()` takes **no argument** — there is no per-goal override. No hard ceiling. |
| **Task today** | `config.DefaultTaskMaxAttempts = 3`, per-task `MaxAttempts` override via `EffectiveTaskMaxAttempts`, and a `2 × maxAttempts` hard ceiling in `consumeAttemptOrExhaust`. |
| **Chosen** | One budget: **default 20**, with a per-goal override, plus the task path's `2 ×` hard ceiling. Operator-ratified. |
| **Why** | 3 was chosen for a dispatch-retry loop, not for a goal's round loop; applying it to chat would cut the chat budget by 85 % as a side effect of a storage change. The override and the ceiling are the task path's genuinely better mechanics — the ceiling exists because two independent gates can disagree, and unification does not remove that possibility. |
| **Addition** | The budget becomes **human-editable** (FR-046). Today `max_attempts` is settable on the wire and through the API but is display-only in the interface (`TaskCard.tsx` renders it, `TaskDetailPanel.tsx` passes it, no input field exists), so only an agent or a script can change the one control whose purpose is a human saying "give it more room". |

### Row 12 — Terminal on MET → **resolved toward the task** (FR-027, FR-028)

| | |
|---|---|
| **Chat today** | `goal_triggers.go::runGoalAdjudication` calls `clearGoal(goalClearNoteMet)` on a met verdict. `goal_loop.go::clearGoal` zeroes **ten** fields in one patch, including `GoalCriteriaJSON`. |
| **Task today** | `completeTaskWithResult` sets status `done`; the record, with its criteria, is kept. |
| **Chosen** | Ending a goal is a **status transition on a retained record**. Erasure is deleted as a terminal mechanism. |
| **Why** | This is a shipping precondition, not a preference: the projection in FR-036 would write per-criterion outcomes into a list that `clearGoal` deletes one statement later, so the operator's "make goal progress visible" would produce nothing at all on the met path — the exact path where visibility matters most. Operator-approved. |

### Row 13 — Terminal on exhaustion → **resolved toward the task** (FR-027, FR-028)

| | |
|---|---|
| **Chat today** | `clearGoal` again — from round exhaustion (two call sites in `goal_triggers.go`), token-budget exhaustion (`goal_loop.go` and `goal_triggers.go`), multi-day idle expiry (`goal_loop.go::goalIdleExpirySweep`) and an explicit user clear. Eight call sites in total. |
| **Task today** | `consumeAttemptOrExhaust` sets status `failed` with a `FailedReason` and writes a handover; the record is kept. |
| **Chosen** | Same as row 12. All eight paths become transitions. |
| **Why** | Identical reasoning, plus a second one specific to this row: an exhausted goal is the case where an operator most needs to read what was tried and what the Judge said, and it is the case where today the least survives. |

### The eleven merges

| Row | Merge |
|---|---|
| 1 | Both records live in the goal entity's own store. |
| 5 | One goal id; the owner keeps its own id. |
| 6 | The goal carries the condition and the compiled statement; the owner keeps its title and description. |
| 8 | Typed `[]AcceptanceCriterion`, never a serialised string. |
| 10 | One attempts-used counter. |
| 11 | One owner reference. |
| 14 | Workspace is resolved from the owner, and is required at activation. |
| 15 | See row 15's own treatment in §6 — two of four routing fields survive. |
| 16 | One `JudgeCriteria` call; the scope field distinguishes the two, and its mutual-exclusion rule is relaxed (FR-013). |
| 17 | One unevidenced-claim brake at threshold 2. |
| — | Superseded-criteria history moves onto the goal for both. |

---

## 6. Functional Requirements (Phase 5)

### A. The entity and its storage (ADR-086 D1)

- **FR-001** — A goal MUST be a stored entity in its own store, addressed by its own id, and MUST NOT be represented as fields on a session or on a task. It follows the `pkg/entity` precedent (ADR-054 D3): per-entity JSON file, atomic write, striped in-process mutex, sidecar advisory lock.
- **FR-002** — A goal MUST reference exactly one owner, as an owner kind (`session` or `task`) plus an owner id. Owner kind MUST be part of the persisted record, not inferred.
- **FR-003** — A goal's acceptance criteria and definition of done MUST each be a typed list of the shared criterion type (ADR-080). Neither MAY be persisted as a serialised string.
- **FR-004** — A goal MUST carry the keeper's own durable counters: the recordless-nudge / zero-output-push streak (today `goal_zero_output_pushes`) and the clarification-question door (today `goal_question_rounds_used`).
- **FR-005** — The parked-question set (`PendingAskJSON`, today riding in `goal.json` per `pkg/session/unified_meta_files.go::u5GoalFile`) MUST be relocated to a session-owned file. It MUST NOT be carried into the goal entity.
- **FR-006** — A goal MUST carry: its condition and compiled statement, its budget and attempts used, its status and latest reason, the most recent claim and verdict, the superseded-criteria history, `started_at`, `last_activity_at`, and the id of the session it is active in once it is active.
- **FR-007** — The criterion id namespace MUST be specified. Persisted criteria are minted as UUIDs by `pkg/task/criterion.go::NormalizeCriteria`. Three non-UUID ids are reserved and MUST NOT be minted for a persisted criterion: `soft-tier-implicit` (`pkg/agent/judge.go::softTierCriterionID`), `goal-condition` (`pkg/agent/goal_compile.go::compiledGoalCriteriaFor`), and the `goal-dod-floor-*` prefix.
- **FR-008** — The lock order between the goal store and the ADR-057 session shard MUST be fixed and one-directional, and MUST be documented alongside `pkg/session/unified_lock.go`'s existing `sessionLock(id)` → `cacheMu` order. The specification MUST state that `fileutil.WithFlock` is a no-op on Windows (`pkg/fileutil/flock_windows.go`), so the goal store's cross-process guarantee is **POSIX-only** and Windows has in-process protection alone.

### B. Two phases, two entry points (D2, D3)

- **FR-009** — A goal in the definition phase MUST exist, be readable and editable, and MUST NOT run.
- **FR-010** — Activation MUST bind a goal to exactly one session and start the loop.
- **FR-011** — `/goal` in chat MUST create the definition and activate it in the current session in one step (ADR-081 D1's instant activation is unchanged).
- **FR-012** — A task MUST hold its goal in the definition phase from task creation until the task starts, and MUST activate it against the session the task mints (`pkg/agent/task_executor.go::createTaskSessionSync`).
- **FR-013** — After activation there MUST be exactly one code path for the loop, the claim, the Judge, the budget accounting and the verdict. `JudgeCriteriaInput`'s validation (`pkg/agent/judge.go`, the scope switch rejecting `scope %q must not also carry TaskID/GoalSessionID`) MUST be relaxed to permit a running task's goal carrying both.
- **FR-014** — A test MUST exist that fails if any post-activation behavioural difference between a chat-owned and a task-owned goal appears. It MUST compare observable behaviour, not implementation structure.

### C. The six contradictions

- **FR-015** — Both the after-turn claim driver and the quiet-window idle keeper MUST apply to both owner kinds. The `opts.IsTaskRun` gates in `pkg/agent/goal_loop.go::applyGoalCommandPrompt` and `::checkGoalLoopAfterTurn` MUST be removed, and `pkg/agent/goal_triggers.go::goalQuietWindowSettle`'s goal-bearing test MUST select task-owned goals.
- **FR-016** — All six existing keeper suppressions MUST apply unchanged to a task-owned goal: a parked question card (`goalHasParkedCard`), a `waiting_on_user` marker (`goalIsWaitingOnUser`), the re-arm marker (`goalIsIdleSettling`), an in-flight adjudication (`goalAdjudicationInFlight`), a live turn including any delegated descendant (`goalHasLiveTurn`), and an exhausted token budget.
- **FR-017** — The bounded zero-output continue-push (`pkg/agent/goal_triggers.go::settleZeroOutputRecordedGoal`, bound `goalZeroOutputPushMax = 2`) MUST apply to a task-owned goal.
- **FR-018** — The quiet-window re-arm marker MUST apply to a task-owned goal, so exactly one keeper action fires per quiet spell and re-arms only on genuine external activity.
- **FR-019** — A task producing no transcript output, no scoped diff and no tool evidence MUST receive the bounded push before any round is spent, and MUST then be judged normally. This cost MUST be recorded in the specification rather than discovered (EC-3).
- **FR-020** — The recordless nudge ladder (`goalNudgePrompt`, `dispatchGoalFallbackCompile`) MUST remain one code path serving both owner kinds. A test MUST assert it is never entered for a task-owned goal created under FR-047.
- **FR-021** — Creating a task through the API or the interface MUST require at least one acceptance criterion and at least one definition-of-done item. This matches the hard rejection `pkg/tools/task.go` and `pkg/sysagent/tools/task.go` already apply to agent-created tasks.
- **FR-022** — The trust-the-claim branch in `pkg/agent/task_executor.go::adjudicateClaim` — which completes a task when its criteria set and its soft tier are both empty — MUST be deleted.
- **FR-023** — A task created before FR-021 with no criteria MUST continue to run and MUST continue to be judged by `pkg/agent/judge.go::SoftTierCriterion`. FR-021 binds at creation and at edit only.
- **FR-024** — There MUST be one budget for both owner kinds, defaulting to **20** (`config.DefaultGoalMaxRounds`).
- **FR-025** — The budget MUST accept a per-goal override. `pkg/config/planning.go::EffectiveGoalMaxRounds` MUST take an override argument, mirroring `EffectiveTaskMaxAttempts`. An override below 1 MUST be rejected.
- **FR-026** — The `2 × effective budget` hard ceiling (`pkg/agent/task_executor.go::consumeAttemptOrExhaust`) MUST apply to both owner kinds.
- **FR-027** — Ending a goal MUST be a status transition on a retained record. The record MUST survive with its criteria, their final statuses, the verdict, the reason and any handover.
- **FR-028** — `pkg/agent/goal_loop.go::clearGoal`'s field-zeroing MUST be deleted as a terminal mechanism, and all eight call sites converted to transitions. The terminal vocabulary MUST distinguish at least: met, budget or round exhaustion, idle expiry, and an explicit operator clear — because all four already produce distinct pill states (`done` / `failed` / `cleared`). Exactly one terminal frame MUST be emitted per ending.

### D. Criteria move off the owner (D5)

- **FR-029** — `pkg/task/task.go`'s `Criteria` field MUST become a reference to the task's goal rather than a second list.
- **FR-030** — Every consumer of `Task.Criteria` MUST be re-pointed: `pkg/agent/task_executor.go::adjudicateClaim`, `pkg/tools/task.go::deferDoneClaimToJudge` and its `pkg/sysagent/tools/task.go` twin, `pkg/plan/lint.go::lintJoinlessConvergence`, `pkg/task/store.go`'s `Patch.Criteria` write path, `pkg/gateway/rest_tasks.go::toWireCriteria` and its `pkg/gateway/rest_plans.go` mirror, and `src/components/workspaces/AcceptanceCriteriaEditor.tsx`.
- **FR-031** — `pkg/agent/judge.go::SoftTierCriterion` MUST remain ephemeral and unpersisted. Any verdict projection targeting it MUST be an explicit, logged no-op, never a silent one, because there is no record to write back to.

### E. Routing (D6)

- **FR-032** — `goal_route_session_key` MUST be deleted. It is written by `pkg/agent/goal_triggers.go::recordGoalRouting` and read into `goalRoute.sessionKey` by `::routeFor`, and consumed by nothing.
- **FR-033** — `goal_route_agent_id` MUST fold into the goal's owner reference (FR-002) rather than persisting as a separate routing field.
- **FR-034** — `goal_route_channel` and `goal_route_chat_id` MUST both survive on the goal record. `pkg/agent/goal_triggers.go::dispatchGoalAsyncFollowUp` aborts without either.
- **FR-035** — A keeper follow-up MUST reach a goal set from any non-webchat channel. Webchat delivery remains session-addressed (ADR-082). When routing resolves on neither side, the existing behaviour is preserved: warn once and write a reason onto the record without overwriting a fresher one (`pkg/agent/goal_triggers.go::goalRoutingLostReason`).

### F. Verdict → criterion status (D8)

- **FR-036** — When an adjudication records a verdict, each criterion's status MUST be set from that criterion's own outcome: met → `task.CritMet`, unmet → `task.CritUnmet`.
- **FR-037** — `AcceptanceCriterion.status` MUST keep exactly `pending`, `met`, `unmet`, where **pending means not yet judged**. No fourth value MAY be added anywhere — on the wire, in the store, or in the interface (ADR-084 revision 9).
- **FR-038** — The system MUST NOT require a criterion to have a machine-checkable form in order to be eligible for `met`, and MUST NOT prefer, prioritise, defer or differently route a criterion that has one. A criterion with no check, no diff, no command and no artifact path is the normal case and reaches `met` on the Judge's reasoning alone.
- **FR-039** — The grounding controls (ADR-084 D2b–D2d) are anti-hallucination controls and MUST NOT be implemented as a proof gate. Their contract is that a `met` points at something the Judge actually opened and does not rest on the worker's own say-so; it is not that a mechanical check passed.
- **FR-040** — The projection MUST run on all **three** write paths, because criteria live in three places: the goal record (`pkg/agent/goal_triggers.go::runGoalAdjudication`), the task record (`pkg/agent/task_executor.go::adjudicateClaim`), and the plan member's definition of done (`pkg/agent/plan_engine.go`'s judge round).
- **FR-041** — The goal write path MUST **de-union** the Judge's result before persisting. `pkg/agent/goal_compile.go::compiledGoalCriteriaFor` hands the Judge a flattened `Criteria ∪ DoD` list; the outcomes MUST be split back onto their originating lists by criterion id (FR-007), never written wholesale onto the criteria list.
- **FR-042** — The status enum MUST be updated in **four** contract copies, in one atomic commit with the regenerated artifacts: `contracts/components/schemas/AcceptanceCriterion.yaml`, `contracts/components/schemas/AcceptanceCriterionInput.yaml`, and the two hand-synced inline duplicates in `contracts/asyncapi.yaml` for `GoalStatusFrame.criteria[]` and `.dod[]`, which generate the enums at `src/lib/api/generated/_asyncapi-zod-schemas.generated.ts:837` and `:867`. Missing either inline copy drops every goal-status frame at the SPA edge with a green `make verify-contracts`.

### G. Retention and lifecycle (D14)

- **FR-043** — Goal records MUST be swept under the same retention rule and schedule as sessions (`pkg/session/retention_sweep.go::RetentionSweep`).
- **FR-044** — A goal MUST NOT outlive its owner as an unreferenced record. Deleting a task MUST transition and remove its goal; a goal whose active session has been swept MUST be terminal-expired at the next sweep rather than left dangling (EC-10).

### H. Settings and the human-editable budget (D10, D13)

- **FR-045** — Goal settings MUST live under **Settings → Performance** (`src/components/settings/PerformanceSection.tsx`). No new settings tab MAY be added; the screen already has eleven.
- **FR-046** — A human-editable budget control MUST exist in the interface for a goal's per-goal override, on the surface where a goal's other properties are edited. It MUST reject a value below 1 and MUST show the inherited default when no override is set.

### I. Mandatory criteria in the interface (D11)

- **FR-047** — `src/components/workspaces/CreateTaskSlideOver.tsx` MUST refuse submission with fewer than one acceptance criterion or fewer than one definition-of-done item, and MUST say which is missing. Its current empty-state hint, which advertises the soft-tier fallback, MUST be removed. The API MUST return HTTP 400 for the same condition so the rule is not interface-only.
- **FR-048** — The definition-of-done editor MUST exist on the task surface. `pkg/task/task.go` has no DoD field today; the DoD list lives on the task's goal record (FR-003), which is a further reason criteria belong on the goal. Opening a pre-FR-047 task with no criteria MUST NOT block reading it; saving an edit MUST enforce FR-047.

### I2. The task form — the eight decided changes (design demo)

**Reference design:** `docs/internal/design/task-form-criteria-dod-demo.html`. It reproduces both
shipped surfaces field for field and outlines only what changes: gold solid = new, gold dashed =
changed, red = removed. Everything unmarked in that page MUST be left exactly as it is. An agent
implementing this section MUST open the demo first and treat it as the visual contract; where this
text and the demo disagree, the demo is wrong and this text wins.

**The two surfaces, and which is which.** Creating is
`src/components/workspaces/CreateTaskSlideOver.tsx`, shared by the workspace board *and* the
calendar. Editing is `src/components/workspaces/TaskDetailPanel.tsx` (1061 lines), wrapped by
`TaskDetailSlideOver.tsx`. These are two different components; a change to one is not a change to
the other. Do **not** mistake either for the calendar's own occurrence slide-over.

**The panel autosaves.** `TaskDetailPanel` writes every field change immediately and shows an
`AutoSaveIndicator`. It has no footer buttons. Do not add a Save button to it, and do not batch
edits.

- **FR-053** — **Criteria required, hint removed.** Both surfaces MUST mark acceptance criteria and
  definition of done as required, using the same `<span className="text-[var(--color-error)]">*</span>`
  the Title field already uses. The empty-state hint MUST be removed in **both** places — it exists
  twice with different wording: `CreateTaskSlideOver.tsx`'s `emptyHint` ("No criteria added — this
  task will be judged against its title and description (D5).") and `TaskDetailPanel.tsx`'s
  `emptyHint` ("No criteria — this task will be judged against its title and description (D5)."). A
  grep for `emptyHint` MUST return zero hits carrying that sentence when this is done. The create
  form replaces it with a plain instruction; the panel needs no replacement. Enforcement is FR-047.
- **FR-054** — **Definition of Done renders as its own group.** In `CreateTaskSlideOver` it is a
  second `AcceptanceCriteriaEditor` with identical vocabulary. In `TaskDetailPanel` it MUST be fed
  to the **existing** `dod` prop on `CriteriaVerdictList`, which already renders a distinctly
  labelled "Definition of Done" group (`CriteriaVerdictList.tsx`, the `criteria-verdict-dod`
  block). The panel currently never passes that prop. Do **not** build a new renderer.
- **FR-055** — **Verdicts render per criterion.** The rows, the tick, the reason line and the
  evidence expander all ship today in `CriteriaVerdictList`/`CriterionRow`; the only defect is that
  nothing ever writes `met`/`unmet`, so every criterion shows pending. This FR is satisfied by
  FR-030–FR-034 (verdict → criterion status) landing, not by touching the renderer. No new
  component. The one interface change is that the panel MUST pass `dod` (FR-054).
- **FR-056** — **Prompt is renamed Goal.** `CreateTaskSlideOver`'s label "Prompt" and
  `TaskDetailPanel`'s "Prompt / Instructions" MUST both read **Goal**. The create form's placeholder
  changes from "Describe what the agent should do…" to "What should this task achieve?", and the
  field becomes required. This is a **label** change: the wire field stays `prompt`
  (`TaskCreateRequest`/`TaskUpdateRequest`), and no contract regeneration is implied. The rename is
  the point — this field is what becomes the goal record when the task starts its own session
  (FR-003).
- **FR-057** — **Checklist is relabelled Todos.** `TaskChecklistField`'s section label and the
  create form's Checklist label MUST read **Todos**, and the placeholder "Add a checklist item…"
  becomes "Add a todo…", in both surfaces. The data, the API (`setTaskTodos`) and the component's
  own header comment already say todos; the label was the last place the other word survived. The
  component filename and exported symbol MAY stay `TaskChecklistField` — renaming those is
  out of scope and MUST NOT be bundled in.
- **FR-058** — **Title becomes editable in the panel.** `TaskDetailPanel` renders the title as
  `<p className="text-sm font-medium">{task.title}</p>` with no edit control, and no inline rename
  exists on the board or list either — so a task can be named once and never renamed. The panel MUST
  offer an editable title following the same pattern its Prompt field already uses (click to edit,
  or a direct input, autosaved). No backend work: `TaskUpdateRequest` already carries `title` and
  `pkg/gateway/rest_tasks.go` already applies it (`if req.Title != nil { patch.Title = req.Title }`).
  This is a missing control, not a missing capability.
- **FR-059** — **Plan is added to the create form.** `CreateTaskSlideOver` has no Plan control; it
  takes `planId` as a prop from the board's active plan filter and sends it silently. Creating from
  an unfiltered board therefore yields a task with no plan, whose only remedy is to save it and
  reopen the detail panel. The create form MUST offer the same Plan picker the panel has, defaulting
  to the inherited `planId` when one is passed and to "No plan" otherwise.
- **FR-060** — **Trigger is REMOVED from both forms.** A normal task has no timer. It starts exactly
  three ways — a human presses Start, an agent starts it, or a plan reaches it — and none is a
  schedule. Time-based starts are the calendar's, and `BoardView`/`ListView` **already** exclude
  every schedule-bearing task via their `isScheduledTrigger` filter, so a task reaching either form
  is manual by definition. Today the field is actively harmful on the panel: its own comment records
  that picking "Once" *"hands the task a default at_ms and PATCHes immediately"*, after which the
  task counts as scheduled, disappears from the board and list, and the field degrades to a
  read-only calendar link — a choice that ejects the task from the surface the user is standing on,
  with a time they never picked. Remove the `Trigger` `Field` and its `SmartSelect` from
  `TaskDetailPanel`, and the Trigger `Label`/`Select` plus its conditional `DateTimePicker` from
  `CreateTaskSlideOver`. **Scope limit:** this removes the two CONTROLS only. The `trigger` field on
  the task model, the wire type, the `isScheduledTrigger`/`scheduledTriggerSummary` helpers, the
  calendar's own editor and the cron engine all stay — the calendar keeps the capability. The
  panel's read-only branch for an already-scheduled task (the plain-English summary plus "Edit in
  workspace calendar" link) MUST also stay, since such a task is still reachable here via a
  dependency chip, subtask row, search result or stale cache.

**Deliberately NOT changed**, so no agent "fixes" them: Status and Workspace stay panel-only (a new
task always starts in Inbox, and it is created inside a workspace); the attempt budget is not a task
field (FR-045/FR-046 put it in Settings → Performance, and the panel keeps its existing read-only
`attempt N/M` line); the Judgment selector stays removed per D-TYPES; and the criteria editor's own
optional technical check and action-count check keep their present wording and stay attached to a
criterion rather than standing alone.

### J. The active-loop cap (D12)

- **FR-049** — Task-owned goals MUST NOT consume the `"goal"` admission slot registered via `pkg/agent/plan_engine.go::RegisterActiveCounter` and bounded by `config.DefaultGlobalActiveLoopCap` (16). Chat goals MUST remain bounded by it unchanged.

### K. Greenfield (D7)

- **FR-050** — No migration of existing goals MAY be built. No frozen legacy shape, no shim, no back-compat parse path.
- **FR-051** — At boot, goal state that can no longer be interpreted MUST be detected, MUST produce exactly one terminal frame for the affected goal, and MUST produce one operator-visible log line naming what was ended. Silent disappearance is prohibited: `json.Unmarshal` drops orphan keys without error, and both goal drivers gate on `GoalCondition != ""`, so without this the goal ceases to exist with no terminal frame and a permanently open interface pill.

### L. Trigger state (§5 open question 5)

- **FR-052** — All six maps on `pkg/agent/goal_triggers.go::goalTriggerState` — `bareClaimStreak`, `waitingOnUser`, `routing`, `idleSettling`, `diffBoundaryHash`, `outputWatermarks` — MUST be re-keyed from session id to goal id, and their doc comments' assertion that "session id *is* the goal id" MUST be removed. The behaviour for an entry whose goal is not yet bound to a session MUST be specified.

### Success criteria

- **SC-001** — A chat goal and a task goal built from identical text produce identical observable loop behaviour after activation, verified by FR-014's differential test.
- **SC-002** — After any adjudication, at least one criterion in the interface renders as `met` or `unmet`; zero criteria render as `pending` where a verdict exists for them.
- **SC-003** — A goal that reaches a met verdict has a readable record afterwards containing its criteria, their statuses and the verdict.
- **SC-004** — A purely subjective criterion with no check, no command and no artifact path reaches `met` in at least one automated test and one holdout scenario.
- **SC-005** — Creating a task with zero criteria through the interface or the API returns HTTP 400 in 100 % of attempts.
- **SC-006** — A keeper follow-up dispatched for a goal owned by a non-webchat channel session reaches that channel in 100 % of attempts where routing is resolvable.
- **SC-007** — Task-owned goals contribute 0 to the goal admission counter, measured with more concurrent tasks than the cap.
- **SC-008** — `make verify-contracts` passes **and** a runtime test asserts a `GoalStatusFrame` carrying `met`/`unmet` criteria survives SPA-edge zod validation.
- **SC-009** — A goal record older than the retention window is absent after a sweep; one inside it is present.
- **SC-010** — Upgrading an install with an in-flight goal emits exactly one terminal frame and one log line for it.

---

## 7. BDD Scenarios (Phase 3)

Categories: **HP** happy path, **AP** alternate path, **EP** error path, **EC** edge case.

### The two entry points

**S-01 (HP) — A chat goal starts immediately**
*Traces to: US-1 AS-3, US-1 AS-1*
- **Given** an operator in a chat session with no active goal
- **When** they set a goal
- **Then** the goal exists, is active, and is bound to that session.

**S-02 (HP) — A task's goal exists before the task runs**
*Traces to: US-1 AS-2, US-2 AS-1*
- **Given** a task created with criteria and a definition of done
- **When** the operator opens it before it has started
- **Then** the goal is shown, and it is not running.

**S-03 (HP) — A task's goal activates when the task starts**
*Traces to: US-1 AS-1*
- **Given** an unstarted task with a goal in the definition phase
- **When** the task starts and mints its session
- **Then** the goal becomes active and bound to that session.

**S-04 (AP) — Editing an unstarted task's goal changes what it is judged against**
*Traces to: US-2 AS-2*
- **Given** an unstarted task
- **When** the operator edits a criterion and the task later runs to adjudication
- **Then** the edited criterion is the one judged.

**S-05 (EC) — A chat goal has no pre-run existence**
*Traces to: US-2 AS-3*
- **Given** a chat session with no goal
- **When** the interface renders
- **Then** no goal surface is shown, and no error is raised.

### The keeper on tasks

**S-06 (HP) — A quiet task is acted on**
*Traces to: US-3 AS-1*
- **Given** a running task whose goal has been quiet longer than the quiet window
- **When** the engine tick sweeps
- **Then** the keeper acts on that task's goal.

**S-07 (AP) — A task with a live turn is left alone**
*Traces to: US-3 AS-2*
- **Given** a running task with a turn in flight, including a delegated descendant turn
- **When** the engine tick sweeps
- **Then** the keeper does not act, and the activity clock is re-armed.

**S-08 (AP) — A task waiting on the operator is left alone**
*Traces to: US-3 AS-3*
- **Given** a running task with a parked question card
- **When** the engine tick sweeps
- **Then** the keeper does not act.

**S-09 (AP) — A silent task is pushed before it is judged**
*Traces to: US-3 AS-4*
- **Given** a running task producing no transcript output, no scoped diff and no tool evidence
- **When** the keeper sweeps twice within the push bound
- **Then** it dispatches a continue-push each time and consumes no round.

**S-10 (EC) — The push bound is respected**
*Traces to: US-3 AS-4*
- **Given** a task that has already received the maximum continue-pushes
- **When** the keeper sweeps again
- **Then** it runs a normal adjudication and consumes a round.

**S-11 (EC) — The nudge ladder is unreachable for a task**
*Traces to: US-3 AS-1, US-7 AS-1*
- **Given** a task created under the mandatory-criteria rule
- **When** its goal runs to any keeper action
- **Then** the recordless nudge ladder is never entered.

**S-12 (AP) — One keeper action per quiet spell**
*Traces to: US-3 AS-1*
- **Given** a quiet task goal that has already had one keeper action
- **When** the next tick arrives with no new external activity
- **Then** no second action fires.

### The terminal transition

**S-13 (HP) — A met goal keeps its record**
*Traces to: US-4 AS-1*
- **Given** an active goal
- **When** the Judge returns met for every criterion
- **Then** the goal's status becomes met, the record persists with its criteria and their statuses, and one terminal frame is emitted.

**S-14 (AP) — An exhausted goal keeps its record**
*Traces to: US-4 AS-2*
- **Given** an active goal at its last permitted attempt
- **When** the Judge returns unmet
- **Then** the goal's status becomes exhausted, the record persists with a reason and a handover, and one terminal frame is emitted.

**S-15 (AP) — An operator-cleared goal is distinguishable**
*Traces to: US-4 AS-3*
- **Given** an active goal
- **When** the operator clears it
- **Then** its terminal status is distinct from met and from exhausted, and the record persists.

**S-16 (EC) — Idle expiry is a fourth distinct terminal state**
*Traces to: US-4 AS-3*
- **Given** an active goal untouched past the idle-expiry window
- **When** the expiry sweep runs
- **Then** its terminal status is distinct from the other three.

**S-17 (EP) — No pill is left open**
*Traces to: US-4 AS-4*
- **Given** any goal reaching any terminal state
- **When** the terminal frame is emitted
- **Then** exactly one is emitted and the interface pill closes.

**S-18 (EC) — The Judge unavailable at a boundary is not terminal**
*Traces to: US-4 AS-1, EC-6*
- **Given** an active goal at its last permitted attempt
- **When** the Judge is unavailable
- **Then** no round is consumed, no terminal transition occurs, and the goal re-arms.

### Ticks that move — including the non-coding cases

**S-19 (HP) — A deck criterion is met on the Judge's reading alone**
*Traces to: US-5 AS-1, US-6 AS-1*
- **Given** a goal whose criterion reads "the deck has sections for problem, market, product, traction and ask, and each says what it claims", with no check, no command and no artifact path
- **When** the Judge opens the deck, reads it, and is persuaded
- **Then** the criterion is recorded met, and it renders as met.

**S-20 (HP) — A tone criterion is met on the Judge's reading alone**
*Traces to: US-6 AS-1*
- **Given** a goal whose criterion reads "the drafted email's tone suits a first approach to a prospective enterprise buyer"
- **When** the Judge reads the draft and is persuaded
- **Then** the criterion is recorded met.

**S-21 (HP) — An irreducibly subjective criterion is met**
*Traces to: US-6 AS-1, SC-004*
- **Given** a goal whose only criterion reads "the summary is genuinely useful to a non-specialist reader"
- **When** the Judge reads the summary and is persuaded
- **Then** the criterion is recorded met, no machine check is consulted, and no different code path is taken.

**S-22 (AP) — Subjective and mechanical criteria are handled identically**
*Traces to: US-6 AS-2*
- **Given** a goal with one criterion carrying a machine check and one purely subjective criterion
- **When** both are judged
- **Then** neither is prioritised, deferred, or routed differently, and both statuses are written by the same path.

**S-23 (AP) — Unmet means the Judge looked and was not persuaded**
*Traces to: US-6 AS-3*
- **Given** a criterion whose artifact does not do what the criterion says
- **When** the Judge reads it
- **Then** the criterion is unmet and the reason names what was looked for and where.

**S-24 (EP) — Unreachable evidence is unmet, not a third state**
*Traces to: US-6 AS-3, US-5 AS-2*
- **Given** a criterion whose evidence the Judge tried and failed to reach
- **When** the verdict is recorded
- **Then** the criterion is unmet, and no fourth status value appears anywhere.

**S-25 (HP) — Ticks land on the list they came from**
*Traces to: US-5 AS-3*
- **Given** a goal with two acceptance criteria and two definition-of-done items, judged as one flattened set
- **When** the verdict is recorded
- **Then** each outcome is written to its originating list and neither list receives the other's outcomes.

**S-26 (AP) — An unjudged criterion stays pending**
*Traces to: US-5 AS-2*
- **Given** a goal whose criteria have never been judged
- **When** they render
- **Then** all show as not yet judged.

**S-27 (AP) — The projection runs at task and plan scope too**
*Traces to: US-5 AS-1*
- **Given** a task adjudication and a plan judge round
- **When** each records a verdict
- **Then** the criteria in each render with their outcomes, not as pending.

**S-28 (EC) — The soft-tier criterion has nowhere to write back**
*Traces to: US-5 AS-1, EC-2*
- **Given** a pre-rule task with no criteria, judged against the ephemeral soft-tier criterion
- **When** the verdict is recorded
- **Then** the projection logs an explicit no-op rather than failing or silently succeeding.

### Mandatory criteria

**S-29 (EP) — A task with no criteria is refused**
*Traces to: US-7 AS-1*
- **Given** the create-task form with a title and prompt but no criteria
- **When** the operator submits
- **Then** creation is refused with a message naming the missing criteria, and the API returns HTTP 400.

**S-30 (EP) — A task with no definition-of-done item is refused**
*Traces to: US-7 AS-2*
- **Given** the create-task form with one criterion and no definition-of-done item
- **When** the operator submits
- **Then** creation is refused with a message naming the missing definition of done.

**S-31 (AP) — A pre-rule task keeps running**
*Traces to: US-7 AS-3, EC-2*
- **Given** a task created before the rule with no criteria
- **When** it runs to adjudication
- **Then** it is judged by the soft tier and completes or fails normally.

**S-32 (EP) — The trust-the-claim path is gone**
*Traces to: US-7 AS-1*
- **Given** a task with no criteria and no title, description or prompt text worth judging
- **When** it claims completion
- **Then** it is not completed on the strength of the claim.

### Budget

**S-33 (HP) — The default budget is 20 for both owners**
*Traces to: US-8 AS-3*
- **Given** a chat goal and a task goal, neither with an override
- **When** each runs
- **Then** both run to 20 rounds.

**S-34 (HP) — An operator raises a budget in the interface**
*Traces to: US-8 AS-1*
- **Given** a goal with the default budget
- **When** the operator sets an override of 30 in the interface
- **Then** the loop runs to 30.

**S-35 (EP) — A budget below one is refused**
*Traces to: US-8 AS-2*
- **Given** the budget control
- **When** the operator enters 0
- **Then** it is refused and no write occurs.

**S-36 (EC) — The hard ceiling still brakes a divergent loop**
*Traces to: US-8 AS-1, EC-8*
- **Given** a goal whose attempts have reached twice its effective budget
- **When** the next boundary is reached
- **Then** the goal terminates as exhausted regardless of the primary gate.

### Routing

**S-37 (HP) — A keeper follow-up reaches a non-webchat channel**
*Traces to: US-9 AS-1*
- **Given** a goal owned by a session on a non-webchat channel
- **When** the keeper dispatches an idle follow-up
- **Then** it is delivered on that channel with that chat id.

**S-38 (AP) — A webchat goal survives a reconnect**
*Traces to: US-9 AS-2*
- **Given** a webchat goal whose connection dropped and was re-established
- **When** the keeper dispatches a follow-up
- **Then** it reaches the live connection.

**S-39 (EP) — Unresolvable routing is recorded, not silent**
*Traces to: US-9 AS-3*
- **Given** a goal with no routing resolvable in memory or on the record
- **When** the keeper tries to dispatch
- **Then** it warns once and writes a routing-lost reason without overwriting a fresher one.

### Retention, cap, greenfield

**S-40 (HP) — Aged goal records are swept**
*Traces to: US-10 AS-1, AS-2*
- **Given** one goal record older than the retention window and one inside it
- **When** the sweep runs
- **Then** the first is removed and the second is kept.

**S-41 (EC) — A goal does not outlive its owner**
*Traces to: US-10 AS-3, EC-4*
- **Given** a task with an active goal
- **When** the task is deleted
- **Then** the goal transitions to terminal and is removed with it.

**S-42 (HP) — Task goals do not starve chat goals**
*Traces to: US-11 AS-1*
- **Given** more concurrently running tasks than the goal admission cap
- **When** the operator sets a chat goal
- **Then** it is admitted.

**S-43 (AP) — Chat goals remain capped**
*Traces to: US-11 AS-2*
- **Given** the goal admission cap reached by chat goals alone
- **When** another chat goal is set
- **Then** it is bounded exactly as before.

**S-44 (EP) — An in-flight goal at upgrade ends visibly**
*Traces to: US-12 AS-1, AS-2*
- **Given** an install with a goal in flight in the old shape
- **When** the gateway boots on the new shape
- **Then** exactly one terminal frame is emitted for it and one log line names it.

**S-45 (AP) — A clean upgrade is silent**
*Traces to: US-12 AS-3*
- **Given** an install with no in-flight goal
- **When** the gateway boots on the new shape
- **Then** no terminal frame and no such log line are produced.

### Storage and concurrency

**S-46 (EC) — The parked-question set survives the move**
*Traces to: US-1 AS-1*
- **Given** a session with a parked question card and an active goal
- **When** the goal moves off session metadata
- **Then** the parked card is still present and still resolvable.

**S-47 (EC) — Trigger state follows the goal, not the session**
*Traces to: US-1 AS-1*
- **Given** a goal in the definition phase with no session bound
- **When** the keeper's trigger state is consulted
- **Then** it resolves by goal id and does not require a session.

**S-48 (EC) — Reserved criterion ids are never minted**
*Traces to: US-5 AS-3, EC-7*
- **Given** criteria normalised for persistence
- **When** ids are minted
- **Then** none equals a reserved id and none carries the reserved DoD-floor prefix.

---

## 8. TDD Plan (Phase 4)

**Execution note.** This machine cannot run the full Go test suite, and `pkg/gateway`'s test binary in particular has OOM-killed sessions. Every Go test below is written to be runnable as a single scoped invocation:

```
CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestName$' ./pkg/<pkg>/
```

Anything broader — the package suite, `./...`, the race detector — runs in CI or on the `ci-omnipus` worker, never here.

| # | Test name | Level | File | Traces to |
|---|---|---|---|---|
| 1 | `TestGoalEntityRoundTrip` | Unit | `pkg/goal/store_test.go` | FR-001, S-01 |
| 2 | `TestGoalOwnerKindPersisted` | Unit | `pkg/goal/store_test.go` | FR-002, S-02 |
| 3 | `TestGoalCriteriaAreTypedLists` | Unit | `pkg/goal/goal_test.go` | FR-003, S-25 |
| 4 | `TestGoalCarriesKeeperCounters` | Unit | `pkg/goal/goal_test.go` | FR-004, S-09 |
| 5 | `TestPendingAskDoesNotMoveToGoal` | Unit | `pkg/session/unified_meta_files_test.go` | FR-005, S-46 |
| 6 | `TestGoalRecordFieldCoverage` | Unit | `pkg/goal/goal_test.go` | FR-006, S-13 |
| 7 | `TestReservedCriterionIDsNeverMinted` | Unit | `pkg/task/criterion_test.go` | FR-007, S-48 |
| 8 | `TestGoalSessionLockOrder` | Unit | `pkg/goal/lock_test.go` | FR-008, S-47 |
| 9 | `TestGoalDefinitionPhaseDoesNotRun` | Unit | `pkg/goal/phase_test.go` | FR-009, S-02 |
| 10 | `TestGoalActivationBindsOneSession` | Unit | `pkg/goal/phase_test.go` | FR-010, S-03 |
| 11 | `TestChatGoalCollapsesBothPhases` | Integration | `pkg/agent/goal_loop_test.go` | FR-011, S-01, S-05 |
| 12 | `TestTaskGoalActivatesAtTaskStart` | Integration | `pkg/agent/task_executor_goal_test.go` | FR-012, S-03 |
| 13 | `TestJudgeInputAcceptsTaskAndGoalSession` | Unit | `pkg/agent/judge_test.go` | FR-013, S-27 |
| 14 | `TestChatAndTaskGoalsBehaveIdentically` | Integration | `pkg/agent/goal_parity_test.go` | FR-014, S-01, S-03 |
| 15 | `TestKeeperSweepsTaskOwnedGoals` | Integration | `pkg/agent/goal_triggers_task_test.go` | FR-015, S-06 |
| 16 | `TestKeeperSuppressionsApplyToTasks` | Integration | `pkg/agent/goal_triggers_task_test.go` | FR-016, S-07, S-08 |
| 17 | `TestZeroOutputPushAppliesToTasks` | Integration | `pkg/agent/goal_triggers_task_test.go` | FR-017, S-09 |
| 18 | `TestQuietWindowReArmAppliesToTasks` | Integration | `pkg/agent/goal_triggers_task_test.go` | FR-018, S-12 |
| 19 | `TestInvisibleProgressTaskIsPushedThenJudged` | Integration | `pkg/agent/goal_triggers_task_test.go` | FR-019, S-10 |
| 20 | `TestNudgeLadderUnreachableForTaskGoal` | Integration | `pkg/agent/goal_triggers_task_test.go` | FR-020, S-11 |
| 21 | `TestCreateTaskRejectsEmptyCriteria` | Integration | `pkg/gateway/rest_tasks_criteria_test.go` | FR-021, S-29, S-30 |
| 22 | `TestTrustTheClaimPathRemoved` | Unit | `pkg/agent/task_executor_test.go` | FR-022, S-32 |
| 23 | `TestLegacyCriterialessTaskStillRuns` | Integration | `pkg/agent/task_executor_test.go` | FR-023, S-31 |
| 24 | `TestGoalDefaultBudgetIsTwentyForBothOwners` | Unit | `pkg/config/planning_test.go` | FR-024, S-33 |
| 25 | `TestEffectiveGoalMaxRoundsHonoursOverride` | Unit | `pkg/config/planning_test.go` | FR-025, S-34, S-35 |
| 26 | `TestHardCeilingAppliesToBothOwners` | Unit | `pkg/agent/task_executor_test.go` | FR-026, S-36 |
| 27 | `TestTerminalGoalRetainsRecord` | Integration | `pkg/agent/goal_terminal_test.go` | FR-027, S-13, S-14 |
| 28 | `TestTerminalStatusVocabularyAndSingleFrame` | Integration | `pkg/agent/goal_terminal_test.go` | FR-028, S-15, S-16, S-17, S-18 |
| 29 | `TestTaskCriteriaIsAGoalReference` | Unit | `pkg/task/task_test.go` | FR-029, S-04 |
| 30 | `TestAllTaskCriteriaConsumersRepointed` | Unit | `pkg/task/criteria_consumers_test.go` | FR-030, S-04 |
| 31 | `TestSoftTierProjectionIsALoggedNoOp` | Unit | `pkg/agent/judge_test.go` | FR-031, S-28 |
| 32 | `TestGoalRouteSessionKeyRemoved` | Unit | `pkg/goal/routing_test.go` | FR-032, S-37 |
| 33 | `TestGoalRouteAgentFoldsIntoOwner` | Unit | `pkg/goal/routing_test.go` | FR-033, S-37 |
| 34 | `TestGoalRouteChannelAndChatIDSurvive` | Unit | `pkg/goal/routing_test.go` | FR-034, S-37 |
| 35 | `TestKeeperFollowUpReachesNonWebchat` | Integration | `pkg/agent/goal_triggers_routing_test.go` | FR-035, S-37, S-38, S-39 |
| 36 | `TestVerdictProjectsOntoCriterionStatus` | Unit | `pkg/agent/verdict_projection_test.go` | FR-036, S-19, S-26 |
| 37 | `TestNoFourthCriterionStatusValue` | Unit | `pkg/task/criterion_test.go` | FR-037, S-24 |
| 38 | `TestSubjectiveCriterionReachesMet` | Integration | `pkg/agent/verdict_projection_test.go` | FR-038, S-21, S-22 |
| 39 | `TestGroundingIsNotAProofGate` | Unit | `pkg/agent/judge_test.go` | FR-039, S-20, S-23 |
| 40 | `TestProjectionRunsOnAllThreeScopes` | Integration | `pkg/agent/verdict_projection_test.go` | FR-040, S-27 |
| 41 | `TestGoalProjectionDeUnionsCriteriaAndDoD` | Unit | `pkg/agent/verdict_projection_test.go` | FR-041, S-25 |
| 42 | `TestCriterionStatusEnumInAllFourContractCopies` | Unit | `src/lib/api/generated/asyncapi-criterion-status.test.ts` | FR-042, S-19 |
| 43 | `TestGoalRecordsSweptOnSessionRetention` | Integration | `pkg/goal/retention_test.go` | FR-043, S-40 |
| 44 | `TestGoalDoesNotOutliveOwner` | Integration | `pkg/goal/retention_test.go` | FR-044, S-41 |
| 45 | `PerformanceSection goal settings` | Unit | `src/components/settings/PerformanceSection.goal.test.tsx` | FR-045, S-34 |
| 46 | `budget control accepts and validates an override` | Unit | `src/components/workspaces/GoalBudgetField.test.tsx` | FR-046, S-34, S-35 |
| 47 | `CreateTaskSlideOver blocks submit without criteria and DoD` | Unit | `src/components/workspaces/CreateTaskSlideOver.criteria.test.tsx` | FR-047, S-29, S-30 |
| 48 | `DoD editor renders and enforces on save` | Unit | `src/components/workspaces/DefinitionOfDoneEditor.test.tsx` | FR-048, S-30, S-31 |
| 49 | `TestTaskGoalsExemptFromActiveLoopCap` | Integration | `pkg/agent/plan_engine_cap_test.go` | FR-049, S-42, S-43 |
| 50 | `TestNoMigrationPathExists` | Unit | `pkg/session/unified_meta_files_test.go` | FR-050, S-45 |
| 51 | `TestOrphanGoalStateEndsVisiblyAtBoot` | Integration | `pkg/gateway/goal_orphan_boot_test.go` | FR-051, S-44, S-45 |
| 52 | `TestTriggerStateKeyedByGoalID` | Unit | `pkg/agent/goal_triggers_test.go` | FR-052, S-47 |
| 53 | `both forms mark criteria and DoD required and carry no fallback hint` | Unit | `src/components/workspaces/CreateTaskSlideOver.criteria.test.tsx`, `TaskDetailPanel.criteria.test.tsx` | FR-053 |
| 54 | `panel passes dod to CriteriaVerdictList and the DoD group renders` | Unit | `src/components/workspaces/TaskDetailPanel.dod.test.tsx` | FR-054 |
| 55 | `a met/unmet criterion renders its tick and reason, not pending` | Unit | `src/components/workspaces/CriteriaVerdictList.status.test.tsx` | FR-055 |
| 56 | `both forms label the prompt field Goal` | Unit | `src/components/workspaces/taskFormLabels.test.tsx` | FR-056 |
| 57 | `both forms label the todos field Todos` | Unit | `src/components/workspaces/taskFormLabels.test.tsx` | FR-057 |
| 58 | `panel renames a task and autosaves it` | Unit | `src/components/workspaces/TaskDetailPanel.title.test.tsx` | FR-058 |
| 59 | `create form offers a Plan picker and defaults to the inherited planId` | Unit | `src/components/workspaces/CreateTaskSlideOver.plan.test.tsx` | FR-059 |
| 60 | `neither form renders a Trigger control; the panel keeps its scheduled read-only branch` | Unit | `src/components/workspaces/taskFormNoTrigger.test.tsx` | FR-060 |

### Test datasets

**D-1 — Budget boundaries** *(traces to S-33, S-34, S-35, S-36)*

| Override | Effective budget | Hard ceiling | Expected |
|---|---|---|---|
| absent | 20 | 40 | runs to 20 |
| 1 | 1 | 2 | runs to 1 |
| 0 | — | — | rejected |
| −1 | — | — | rejected |
| 30 | 30 | 60 | runs to 30 |
| 30, attempts already 31 | 30 | 60 | terminates exhausted at next boundary |

**D-2 — Criterion shapes reaching a verdict** *(traces to S-19, S-20, S-21, S-22, S-23, S-24)*

| Criterion | Check | Artifact path | Judge outcome | Expected status |
|---|---|---|---|---|
| "the deck has the five named sections and each says what it claims" | none | none | persuaded | `met` |
| "the drafted email's tone suits a first approach to an enterprise buyer" | none | none | persuaded | `met` |
| "the summary is genuinely useful to a non-specialist reader" | none | none | persuaded | `met` |
| "the interface reads as modern" | none | none | persuaded | `met` |
| "the artifact does what the criterion says" | none | present | not persuaded | `unmet` |
| "evidence unreachable after trying" | none | none | could not reach | `unmet` |
| "`go build ./...` exits 0" | present, exit 0 | none | persuaded | `met` |
| "`go build ./...` exits 0" | present, exit 2 | none | asserts met | rewritten `unmet` (D5a veto) |
| never judged | any | any | — | `pending` |

**D-3 — Keeper suppression matrix** *(traces to S-06, S-07, S-08, S-12)*

| Parked card | waiting_on_user | re-arm marker | adjudication in flight | live turn | budget exhausted | Expected |
|---|---|---|---|---|---|---|
| no | no | no | no | no | no | keeper acts |
| yes | no | no | no | no | no | suppressed |
| no | yes | no | no | no | no | suppressed |
| no | no | yes | no | no | no | suppressed |
| no | no | no | yes | no | no | suppressed |
| no | no | no | no | yes | no | suppressed, clock re-armed |
| no | no | no | no | no | yes | terminal, budget-exhausted |

**D-4 — Task creation criteria** *(traces to S-29, S-30, S-31)*

| Criteria | DoD | Origin | Expected |
|---|---|---|---|
| 0 | 0 | interface | 400, names both |
| 1 | 0 | interface | 400, names DoD |
| 0 | 1 | interface | 400, names criteria |
| 1 | 1 | interface | created |
| 0 | 0 | agent tool | rejected (existing behaviour) |
| 0 | 0 | pre-existing record | runs, soft-tier judged |

**D-5 — Routing resolution** *(traces to S-37, S-38, S-39)*

| Channel | Chat id | In-memory | Persisted | Expected |
|---|---|---|---|---|
| telegram | present | hit | — | delivered |
| telegram | present | miss | hit | delivered, rehydrated |
| webchat | present | miss | hit | delivered to live connection |
| — | — | miss | miss | warn once, routing-lost reason written |
| telegram | empty | miss | partial | warn once, no dispatch |

### Regression requirements

This change modifies existing behaviour heavily. The following existing tests MUST continue to pass unchanged, and any that must change MUST be changed with an explicit note saying which decision required it:

| Existing test file | Protects |
|---|---|
| `pkg/agent/goal_loop_test.go` | The `/goal` entry point, the origin gates that survive (`UserInitiated`, the follow-up sender sentinel), the parked-turn gate. |
| `pkg/agent/goal_triggers_test.go` | The six suppressions, the quiet-window arithmetic, the bare-claim streak economics, the routing fallback and its reason-overwrite rule. |
| `pkg/agent/task_executor_test.go` | Attempt accounting, the sole-writer invariant on `AttemptCount`, the evidence-gate free re-dispatch, the hard ceiling. |
| `pkg/agent/subturn_target_identity_test.go` | ADR-032 delegation identity — untouched by this change and must stay untouched. |
| `pkg/session/unified_lock.go`'s lock-order tests | The ADR-057 FR-050 one-directional order and the two sanctioned two-shard exceptions. |
| `pkg/entity/store_crossprocess_test.go`, `pkg/entity/flock_isolation_test.go` | The POSIX-only cross-process guarantee the goal store inherits. |
| `pkg/task/criterion_test.go` | `IsValidCriterionStatus`'s three values — must still be three. |
| `src/components/workspaces/CriteriaVerdictList.test.tsx` | Attempt rendering and the default-budget fallback. |
| `src/components/workspaces/TaskCard.goalLoopStatus.test.tsx` | The `attempt N/M` label and its default fallback. |
| `src/components/workspaces/CreateTaskSlideOver.test.tsx` | Every create path other than the criteria rule. |
| `pkg/gateway/rest_default_agent_singleton_test.go` | Unrelated but adjacent; a canary that the gateway config path is untouched. |

**New regression tests required:**

- A test asserting no fourth `CriterionStatus` value exists, which fails if one is added (protects FR-037 against a future re-introduction of the withdrawn third outcome).
- A test asserting `clearGoal`-style field-zeroing has no non-test call site (protects FR-028 against a merge from a pre-ADR-086 branch, in the same spirit as `scripts/check-no-goal-confirm-gate.sh`).
- A test asserting the `IsTaskRun` goal gates are absent, so a merge cannot silently re-fence the keeper out of tasks.

---

## 9. Traceability Matrix

| FR | User story | BDD scenario(s) | Test |
|---|---|---|---|
| FR-001 | US-1 | S-01 | `TestGoalEntityRoundTrip` |
| FR-002 | US-1 | S-02 | `TestGoalOwnerKindPersisted` |
| FR-003 | US-5 | S-25 | `TestGoalCriteriaAreTypedLists` |
| FR-004 | US-3 | S-09 | `TestGoalCarriesKeeperCounters` |
| FR-005 | US-1 | S-46 | `TestPendingAskDoesNotMoveToGoal` |
| FR-006 | US-4 | S-13 | `TestGoalRecordFieldCoverage` |
| FR-007 | US-5 | S-48 | `TestReservedCriterionIDsNeverMinted` |
| FR-008 | US-1 | S-47 | `TestGoalSessionLockOrder` |
| FR-009 | US-2 | S-02 | `TestGoalDefinitionPhaseDoesNotRun` |
| FR-010 | US-1 | S-03 | `TestGoalActivationBindsOneSession` |
| FR-011 | US-1, US-2 | S-01, S-05 | `TestChatGoalCollapsesBothPhases` |
| FR-012 | US-1 | S-03 | `TestTaskGoalActivatesAtTaskStart` |
| FR-013 | US-5 | S-27 | `TestJudgeInputAcceptsTaskAndGoalSession` |
| FR-014 | US-1 | S-01, S-03 | `TestChatAndTaskGoalsBehaveIdentically` |
| FR-015 | US-3 | S-06 | `TestKeeperSweepsTaskOwnedGoals` |
| FR-016 | US-3 | S-07, S-08 | `TestKeeperSuppressionsApplyToTasks` |
| FR-017 | US-3 | S-09 | `TestZeroOutputPushAppliesToTasks` |
| FR-018 | US-3 | S-12 | `TestQuietWindowReArmAppliesToTasks` |
| FR-019 | US-3 | S-10 | `TestInvisibleProgressTaskIsPushedThenJudged` |
| FR-020 | US-3 | S-11 | `TestNudgeLadderUnreachableForTaskGoal` |
| FR-021 | US-7 | S-29, S-30 | `TestCreateTaskRejectsEmptyCriteria` |
| FR-022 | US-7 | S-32 | `TestTrustTheClaimPathRemoved` |
| FR-023 | US-7 | S-31 | `TestLegacyCriterialessTaskStillRuns` |
| FR-024 | US-8 | S-33 | `TestGoalDefaultBudgetIsTwentyForBothOwners` |
| FR-025 | US-8 | S-34, S-35 | `TestEffectiveGoalMaxRoundsHonoursOverride` |
| FR-026 | US-8 | S-36 | `TestHardCeilingAppliesToBothOwners` |
| FR-027 | US-4 | S-13, S-14 | `TestTerminalGoalRetainsRecord` |
| FR-028 | US-4 | S-15, S-16, S-17, S-18 | `TestTerminalStatusVocabularyAndSingleFrame` |
| FR-029 | US-2 | S-04 | `TestTaskCriteriaIsAGoalReference` |
| FR-030 | US-2 | S-04 | `TestAllTaskCriteriaConsumersRepointed` |
| FR-031 | US-5 | S-28 | `TestSoftTierProjectionIsALoggedNoOp` |
| FR-032 | US-9 | S-37 | `TestGoalRouteSessionKeyRemoved` |
| FR-033 | US-9 | S-37 | `TestGoalRouteAgentFoldsIntoOwner` |
| FR-034 | US-9 | S-37 | `TestGoalRouteChannelAndChatIDSurvive` |
| FR-035 | US-9 | S-37, S-38, S-39 | `TestKeeperFollowUpReachesNonWebchat` |
| FR-036 | US-5 | S-19, S-26 | `TestVerdictProjectsOntoCriterionStatus` |
| FR-037 | US-6 | S-24 | `TestNoFourthCriterionStatusValue` |
| FR-038 | US-6 | S-21, S-22 | `TestSubjectiveCriterionReachesMet` |
| FR-039 | US-6 | S-20, S-23 | `TestGroundingIsNotAProofGate` |
| FR-040 | US-5 | S-27 | `TestProjectionRunsOnAllThreeScopes` |
| FR-041 | US-5 | S-25 | `TestGoalProjectionDeUnionsCriteriaAndDoD` |
| FR-042 | US-5 | S-19 | `TestCriterionStatusEnumInAllFourContractCopies` |
| FR-043 | US-10 | S-40 | `TestGoalRecordsSweptOnSessionRetention` |
| FR-044 | US-10 | S-41 | `TestGoalDoesNotOutliveOwner` |
| FR-045 | US-8 | S-34 | `PerformanceSection goal settings` |
| FR-046 | US-8 | S-34, S-35 | `budget control accepts and validates an override` |
| FR-047 | US-7 | S-29, S-30 | `CreateTaskSlideOver blocks submit without criteria and DoD` |
| FR-048 | US-7 | S-30, S-31 | `DoD editor renders and enforces on save` |
| FR-049 | US-11 | S-42, S-43 | `TestTaskGoalsExemptFromActiveLoopCap` |
| FR-050 | US-12 | S-45 | `TestNoMigrationPathExists` |
| FR-051 | US-12 | S-44, S-45 | `TestOrphanGoalStateEndsVisiblyAtBoot` |
| FR-052 | US-1 | S-47 | `TestTriggerStateKeyedByGoalID` |

**Counts.** Functional requirements defined in §6: **52** (FR-001…FR-052, contiguous). Rows in this matrix: **52**. Every FR in §6 appears here and every row here resolves to an FR in §6 — verified by comparing the two ranges symbol by symbol. BDD scenarios: **48** (S-01…S-48); every scenario appears in at least one matrix row and in at least one TDD row — both directions verified mechanically. Named tests: **52**. Success criteria: **10** (SC-001…SC-010).

---

## 10. Holdout Evaluation Scenarios (NOT for development use)

These are for post-implementation verification by a human or an external evaluator. They are deliberately **not** derivable from §8's matrix and MUST NOT be referenced by any test.

**H-1 (happy) — The pitch deck.** Ask an agent, in chat, to produce a five-section investor deck for a fictional company, with criteria written entirely in prose and no file paths. Watch until it ends. Expect: every criterion resolves to met or unmet with a reason a human would recognise as a reasonable review, and the finished record is still readable afterwards.

**H-2 (happy) — The board task nobody watches.** Create a task on a workspace board with real criteria, start it, and then leave for twenty minutes without interacting. Expect: the keeper acted at least once without any human input, and the task did not sit silently in progress.

**H-3 (happy) — The subjective-only goal.** Set a goal whose single criterion is a matter of taste — "the tone of this note would not embarrass us if a customer read it". Expect: it reaches a verdict, and the verdict is met when the note is genuinely fine. If the system asks for a command, a diff or a file path in order to decide, it has failed.

**H-4 (error) — The impossible task.** Create a task through the interface with no acceptance criteria. Expect: it cannot be created, and the message says why in words a non-engineer understands.

**H-5 (error) — The dead channel.** Set a goal from a channel, then break that channel's connection. Expect: the keeper's failure to reach it is visible on the goal's record; nothing pretends to have succeeded.

**H-6 (edge) — The budget rescue.** Let a goal run out of rounds. Raise its budget in the interface. Expect: a human, not an agent, was able to do this, and the goal can continue.

**H-7 (edge) — The upgrade with work in flight.** Upgrade an install while a goal is genuinely mid-run. Expect: the chat shows that goal ending, once. It does not silently stop existing, and no pill is left spinning.

---

## 11. Assumptions & Ambiguity Warnings

This spec was written non-interactively. Each row below is a question the `/plan-spec` gates would have put to a human, together with the assumption taken in its place. **Every one is a live risk until confirmed.**

| # | What is ambiguous | Assumption taken | Question to resolve |
|---|---|---|---|
| A-1 | Where the goal store lives — its own `pkg/goal` entity store, or inside `pkg/task` | Its own store, following the `pkg/entity` precedent (ADR-054 D3), because a goal now has two owner kinds and living inside one of them reintroduces the asymmetry the ADR removes | Own store, or `pkg/task`? |
| A-2 | Whether a task may hold more than one goal over its life (ADR-086 §5 Q2) | One goal per task, reused on re-run with attempts reset and verdict history retained (EC-1) | New goal per run, or one goal with fresh attempts? |
| A-3 | The goal id namespace | UUID, distinct from task and session id namespaces, so an owner reference is unambiguous | Confirm the id shape. |
| A-4 | The exact terminal status vocabulary | Four values distinguishing met, exhausted, expired and cleared, because four pill states already exist | Are four enough, and are these the right four names? |
| A-5 | Whether the per-goal budget override is editable while a goal is running | Yes — that is the point of US-8 | Editable mid-run, or definition-phase only? |
| A-6 | Where the human budget control lives for a **chat** goal, which has no detail panel | On the goal card in chat | Confirm the surface. |
| A-7 | Whether FR-047's DoD requirement applies to agent-created tasks too | Criteria are already required for agents; the DoD requirement is specified for the interface path only, since agents have no DoD argument today | Should `create_task` also require a DoD list? |
| A-8 | What "the same retention as sessions" means for a goal whose owner is a task, not a session | The goal's own `last_activity_at` against the session retention window | Owner's clock, or the goal's own? |
| A-9 | Whether the keeper's quiet window should differ for tasks | No — identical, per D3 | Confirm 60 s is right for a task too. |
| A-10 | What bounds the total number of active goals once task goals are exempt (ADR-086 §5 Q7) | Task concurrency bounds them indirectly; no new cap is introduced | Is an explicit global bound needed? |
| A-11 | The behaviour of trigger state for a goal with no session bound (FR-052) | Entries are created lazily at activation; a definition-phase goal has none | Confirm. |
| A-12 | Whether FR-051's boot detection should also repair, or only report | Report and terminate only; repairing is a migration, which D7 forbids | Confirm report-only. |
| A-13 | Whether the `2 ×` hard ceiling should apply to chat goals, which never had one | Yes, for parity | Confirm the chat budget effectively doubles as a ceiling. |
| A-14 | Whether `pkg/plan`'s DoD converges later (ADR-086 §5 Q4) | Out of scope here, per D5 | Confirm the deferral. |
| A-15 | The Windows posture for the goal store | In-process protection only, matching every other file store; stated, not fixed | Accept, or block on a `LockFileEx` compensation? |

**Additional ambiguity flagged from the review, not resolved here:** the review's list of six contradiction rows and the operator's phrase "rows 2/3/4" describe the same set differently. This spec treats the six as adjudication driver, nudge ladder, empty-criteria handling, budget, terminal-met and terminal-exhausted, and treats the zero-output push and quiet-window re-post as separately-testable sub-behaviours of the first (§5, rows 4a and 4b). If the operator meant those two to be counted as contradictions in their own right, the count is eight, not six — but the resolutions are unchanged either way.

---

## 12. Wave Plan (file-disjoint)

Waves are file-disjoint so they can be implemented in parallel without merge conflict. A wave may not begin until every wave it depends on is green.

| Wave | Depends on | Write-set | Delivers |
|---|---|---|---|
| **W1 — Contracts** | — | `contracts/components/schemas/AcceptanceCriterion.yaml`, `AcceptanceCriterionInput.yaml`, `contracts/asyncapi.yaml`, `contracts/components/schemas/Goal*.yaml`, `pkg/api/generated/**`, `src/lib/api/generated/**` | FR-042; the goal wire shape. One atomic commit with regenerated artifacts. |
| **W2 — The goal store** | W1 | `pkg/goal/**` (new package) | FR-001…FR-008. |
| **W3 — Session storage** | W2 | `pkg/session/unified_meta_files.go`, `pkg/session/unified.go`, `pkg/session/daypartition.go`, `pkg/session/retention_sweep.go` | FR-005, FR-043, FR-050. Goal fields leave `goal.json`; `PendingAskJSON` relocates. |
| **W4 — Terminal transition** | W2 | `pkg/agent/goal_loop.go`, `pkg/agent/goal_triggers.go` (terminal paths only) | FR-027, FR-028. **Must land before W7** — the projection has nowhere to write until erasure is gone. |
| **W5 — Task criteria move** | W2 | `pkg/task/task.go`, `pkg/task/store.go`, `pkg/tools/task.go`, `pkg/sysagent/tools/task.go`, `pkg/plan/lint.go`, `pkg/gateway/rest_tasks.go`, `pkg/gateway/rest_plans.go` | FR-029, FR-030, FR-021 (API half). |
| **W6 — Budget and cap** | W2 | `pkg/config/planning.go`, `pkg/config/validator.go`, `pkg/config/defaults.go`, `pkg/agent/plan_engine.go` | FR-024, FR-025, FR-026, FR-049. |
| **W7 — Verdict projection** | W1, W4, W5 | `pkg/agent/verdict_projection.go` (new), `pkg/agent/goal_compile.go`, `pkg/agent/task_executor.go` (projection call only), `pkg/agent/judge.go` | FR-031, FR-036…FR-041. |
| **W8 — Keeper unification** | W2, W3, W4 | `pkg/agent/goal_loop.go` (gates), `pkg/agent/goal_triggers.go` (drivers, maps), `pkg/agent/task_executor.go` (activation, claim path) | FR-009…FR-020, FR-022, FR-023, FR-052. The behavioural heart; last of the backend waves by design. |
| **W9 — Routing** | W2, W8 | `pkg/goal/routing.go`, `pkg/agent/goal_triggers.go` (routing helpers) | FR-032…FR-035. |
| **W10 — Boot detection** | W3 | `pkg/gateway/gateway.go`, `pkg/gateway/goal_orphan_boot.go` (new) | FR-051. |
| **W11 — SPA: the create form** | W1, W5 | `src/components/workspaces/CreateTaskSlideOver.tsx`, `AcceptanceCriteriaEditor.tsx`, `DefinitionOfDoneEditor.tsx` (new) | FR-047, FR-048, and the create-form half of FR-053, FR-056, FR-057, FR-059, FR-060. |
| **W12 — SPA: the detail panel** | W1, W6 | `src/components/workspaces/GoalBudgetField.tsx` (new), `TaskDetailPanel.tsx`, `TaskChecklistField.tsx`, `src/components/settings/PerformanceSection.tsx` | FR-045, FR-046, FR-054, FR-058, and the panel half of FR-053, FR-056, FR-057, FR-060. |
| **W13 — SPA: goal card** | W1, W7 | `src/components/chat/GoalEchoCard.tsx`, `GoalPillTray.tsx`, `GoalIndicator.tsx`, `src/components/workspaces/CriteriaVerdictList.tsx` | Renders the moving ticks. FR-055. |

**The three SPA waves are file-disjoint by construction, and the split cuts across the eight form changes rather than along them** — five of the eight (FR-053, FR-056, FR-057, FR-060, and FR-055's rendering) touch both surfaces. Implement each half in its own wave against the same demo page; do not let one wave edit the other's file to "finish" a change. `TaskChecklistField.tsx` is shared by both surfaces and belongs to **W12 alone**.
| **W14 — Parity and guard tests** | all | `pkg/agent/goal_parity_test.go`, the three new regression guards in §8 | FR-014 and the merge guards. |

**Critical path:** W1 → W2 → W4 → W7 → W13. **The one ordering that is not negotiable** is W4 before W7: writing per-criterion outcomes into a record that is erased one statement later is the defect ADR-086 D9 exists to prevent, and building them in the other order would produce a passing test suite and a blank goal card.
