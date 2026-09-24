# ADR-091 WP-D — Cancel cascade, dispatch reservation, revival, boot recovery

- **Decision record:** [ADR-091](../architecture/ADR-091-steered-sessions-replace-subagents.md) D8, §7
- **Landing order:** [adr-091-landing-order.md](adr-091-landing-order.md) — consumes I-1, I-3, I-8, I-9; publishes I-6; WP-A's dispatch calls I-6
- **Owner files:** landing order §3, row D
- **Status:** Draft rev 2 (consolidated after three grills; supersedes every earlier sentence of rev 1)

## Summary

Stop must reach everything under the session it is pressed on — including a sub-agent that was woken after its own child finished, and one whose wake is queued but has not started — and it must still hold after a restart. Today Stop follows an in-memory link that a woken session loses. This package makes Stop stamp a durable marker on every session it reaches, makes every dispatch check its own session's marker and generation first, defines how a stopped session is revived by a newer instruction, reports partial results honestly when part of the tree cannot be read, and makes boot recovery classify every record, deliver failures to the parent, and re-wake entries whose wake was lost.

## Existing codebase context

*Source inspection at `364290cb5`.*

| Symbol | Role today | Change |
|---|---|---|
| `pkg/agent/steering.go::collectDescendantTurnIDs` | in-memory subtree by `routingSessionID` | reads the cache derived from the edge; falls back to the durable walk |
| `pkg/agent/cancel.go::CollectDescendantSessionIDs(lifecycleStore, rootSessionID)` | durable walk over `LifecycleFilter{ParentDurableKey}`; returns partial + error | the cascade source; its filter moves to the edge's steering session (the field is deleted, D2); partial results reported. Its thin callers `pkg/gateway/websocket_cancel.go` and `pkg/gateway/rest_sessions.go` are owned here for that change |
| `pkg/agent/cancel.go::RequestCancelForSession` | the in-memory cancel entry; takes a session id only | gains a generation parameter; called by `CancelSubtree` for each live turn with the generation it stamped; the registry refuses it when the registered turn's generation differs (R01) |
| `pkg/bus/session_message.go` / `pkg/agent/session_messaging_wire.go` | — | owned by WP-B (principal); this package's `Revive` is reached through the same verified path |
| `pkg/agent/cancel_prearm.go::armCancelOrFindActiveTurn`, `turnImminentForIdentity`, `preArmKeysForTurn` | the pre-arm latch (5 s, consumed once) | a queued descendant wake counts as imminent under the stopped node (root or middle) |
| `registerTurnIfAbsent` (new, internal to `pkg/agent`) (published by WP-A, I-3) | compare-and-set turn registration | used by `ReserveDispatch` under the record lock |
| `pkg/agent/plan_engine.go::StopTask` → `cancelSessions` | task Stop | unchanged entry; cascade via `CancelSubtree` |
| `pkg/session/lifecycle_index.go::ensureWarm` → `IndexReport` (published by WP-A, I-9) | today skips unreadable records silently | this package consumes the report and surfaces it |
| `pkg/agent/boot_sweep.go::sweepToFailedInterrupted`, `isNeedsInputReconstructable` | boot recovery; requires a checkpoint for parked sessions | classifies every record (I-8); delivers failure upward; parked recovery from `NeedsInput` without a checkpoint; stays stopped unless revived |
| `pkg/gateway/gateway_boot.go` | production failure hook logs | calls I-5 `Deliver`; re-wakes unacknowledged inbox entries without a consumed marker; surfaces the index report |

### Impact assessment (manual)

| Symbol | Risk | Dependents |
|---|---|---|
| Cancel cascade source | **HIGH** — safety | every Stop surface (`/goal clear`, StopPlan, StopTask, UI Stop) |
| Pre-arm latch | HIGH — race-sensitive | `subturn` spawn, `processSystemMessage` |
| Boot sweep | MEDIUM | plan engine, lifecycle index |

## User stories and acceptance criteria

### US-1 — Stop reaches the whole tree, durably (P0)

1. **Given** R → A → B → C where B was re-entered after C completed, **When** Stop is pressed on R, **Then** R's, A's, B's and C's records carry a Stop marker for their current generation, and A, B and C's live turns are cancelled.
2. **Given** C's completion has queued a wake for B that has not started, **When** Stop is pressed on R before it starts, **Then** the wake is refused at reservation and B does not run.
3. **Given** the same, **When** Stop is pressed on R after B's re-entry registered, **Then** B is cancelled.
4. **Given** B's record is unreadable, **When** Stop is pressed on R, **Then** A and C (if reachable) are stamped and cancelled, the report names B as unreachable, the Stop response carries `partial: true`, and one line reaches the channel the Stop came from (founder decision, round 6).
5. **Given** Stop is pressed on middle node B, **When** the cascade runs, **Then** B's and C's records are stamped; A's and R's are not; A's next re-entry runs.
6. **Given** Stop on R stamped B in generation g, **When** B is revived by a newer instruction as generation g+1, **Then** the old marker does not cancel it; an unrelated session on the same agent was never stamped and is unaffected.
7. **Given** a Stop cascade in flight, **When** a launch under A lands after A was stamped, **Then** the child is stamped at launch (WP-A, FR-A-015) and never starts.
8. **Given** a Stop on R, **When** the process restarts, **Then** every stamped session stays stopped.
9. **Given** the cascade stamped B in generation 1 and, before it reached the cancel step, B was revived and registered in generation 2, **When** the cascade cancels B with generation 1, **Then** the registry refuses it, B's generation-2 turn keeps running, and the report lists B under `SkippedNewerGeneration`.
10. **Given** a descendant that is already terminal, **When** the cascade reaches it, **Then** nothing is written (terminal records are immutable) and it is listed under `SkippedTerminal`.
11. **Given** the cascade is between enumerating A's children and stamping them, **When** A publishes a new child under its record lock, **Then** the cascade's second enumeration stamps it — no child is left unstamped.

### US-2 — Dispatch honours the reservation (P0)

1. **Given** any dispatch of a steered session, **When** it runs, **Then** it reserves against the session's own record first — Stop marker for the current generation, wake generation not older than the record's, no turn already registered — and a refused reservation dispatches nothing.
2. **Given** two dispatches race for the same session, **When** both reserve, **Then** exactly one proceeds (`registerTurnIfAbsent`).
3. **Given** a wake queued for B in generation 1 and B revived to generation 2, **When** the old wake is dispatched, **Then** it is refused as stale and acknowledged.

### US-3 — Boot recovery classifies, tells the parent, and re-wakes (P1)

1. **Given** a steered session that was running at crash, **When** the process boots, **Then** it is failed as interrupted and its steering session receives an `error` entry (`fatal: true`, `interrupted:`).
2. **Given** a steered session parked with a question at crash, **When** the process boots, **Then** it is recoverable from its `NeedsInput` record without a checkpoint, and its parent's pending question is intact.
3. **Given** every record at boot, **When** the sweep runs, **Then** each is classified per I-8: `ordinary_root` and `steered` resume as today; `legacy_delegate` is set `failed` with reason `pre-adr-091-not-resumable` and reported once; `damaged_child`, `invalid_edge` and `unreadable` are refused and surfaced.
4. **Given** an unreadable lifecycle record at boot, **When** the index warms, **Then** the I-9 report lists it and the operator sees it.
5. **Given** a steered session that was stopped before the crash, **When** the process boots, **Then** it stays stopped — unless a steering message or answer newer than the marker is queued for it, in which case it is revived as a new generation and continues (founder decision, round 5).
6. **Given** inbox entries whose wake never happened or was never consumed, **When** the process boots, **Then** each **wake-eligible** entry (I-5 table) is re-woken once; progress and checkpoint entries are never woken; entries already consumed are acknowledged without a turn (I-3).
7. **Given** a child whose `handback` was appended but whose record was never marked terminal, or whose record is terminal but whose parent's inbox lacks the `<child>:<gen>:final` entry, **When** the process boots, **Then** the missing half is repaired — the record finished from the entry, or the entry recreated with the same id — and the parent is woken once.

### Edge cases

| Case | Expected |
|---|---|
| Stop on a leaf | only the leaf |
| Ancestor pruned by retention while a descendant lives | prevented by WP-A's invariant; if encountered, reported as unreachable |
| Stop issued twice | idempotent; one report |
| Root record itself unreadable | Stop still cancels live turns found in memory; reported partial |
| Revive then Stop within the same second | both applied in arrival order under the record lock; the later one wins |
| Stop on a root whose only descendant is `queued` | the descendant is stamped and never starts; `subagent_state(cancelled)` written |

## Behavioral contract

- When Stop is pressed on a session, the system stamps that session and every descendant it can reach by the durable edge, cancels their live turns, refuses their queued wakes, and reports what it could not reach.
- When a steered session is dispatched, the system first checks its own record — marker, generation, registration.
- When a newer instruction arrives for a stopped session, the system revives it as a new generation.
- When the process boots, the system classifies every record, fails interrupted steered sessions and tells their parents, recovers parked sessions from their record, keeps stopped sessions stopped, re-wakes lost wakes, and surfaces unreadable records.

## Explicit non-behaviors and safeguards

### Qualitative prohibitions

- The system must not report a Stop as complete when part of the tree was unreadable, because a silent partial Stop is the "cannot stop it" symptom in disguise.
- The system must not let a queued re-entry run after a Stop for its generation, because that resurrects a stopped tree one turn later.
- The system must not cancel a revived or unrelated session with an old Stop, because a Stop marker names the generation it stops and sits only on the records the cascade stamped.
- The system must not resume a `legacy_delegate` or `damaged_child` record, because its identity cannot be reconstructed.
- The system must not treat a stale wake as current, because a wake written before a revival carries the old generation.

### Machine-verifiable constraints

Conventions: landing order §5 item 6.

| Constraint | Exact check |
|---|---|
| Full cascade | in the 3-level fixture with B re-entered, Stop on R → every record of R, A, B, C has `Stop.Generation == Generation`; `activeTurnStates` contains none of A, B, C within 2 s |
| Queued re-entry refused | `ReserveDispatch(B, gen)` returns false after Stop stamped B's record for its current generation; `Dispatch` registers nothing |
| Stale wake refused | `ReserveDispatch(B, 1)` false when `B.Generation == 2`; the wake is acknowledged |
| Siblings independent | Stop on B; `ReserveDispatch(C', gen)` for sibling C' returns true |
| Middle node | Stop on B; A's and R's records carry no marker for their generation; A's next re-entry runs |
| Durable | Stop on R; `Reboot()`; `ReserveDispatch` false for every stamped session |
| Revival | newer instruction → `Generation` incremented; `ReserveDispatch(B, 2)` true; `subagent_state(running)` written |
| Order under lock | 100 randomized Stop/Revive interleavings → the record reflects the last write, never a torn state |
| Cancel carries generation | stamp B (gen 1); revive + register B (gen 2); cancel with gen 1 → refused; B's gen-2 turn alive; `SkippedNewerGeneration == [B]` |
| Terminal skipped | a completed descendant → no write, `SkippedTerminal` lists it |
| Second pass | 1,000 randomized launch-during-cascade interleavings → 0 unstamped children |
| Re-nudge eligibility | 3 unacknowledged entries at boot: handback, progress, question → 2 wakes, progress untouched |
| Repair | both half-written orders → one entry with the deterministic id, one wake |
| Partial reported | with B's record unreadable, `CancelReport.Unreachable == [B]`, the Stop response frame carries `partial: true`, exactly one line on the originating channel: "stopped 3 of 5; 2 unreachable" |
| Boot failure delivered | after a simulated crash, 1 `error` entry (`interrupted:`) to the parent; 0 with only a log line |
| Boot classification | one fixture per I-8 class → the stated consequence |
| Parked recovery | parked session with `NeedsInput` and no checkpoint → resumable |
| Re-nudge | 3 unacknowledged entries at boot, 1 already consumed → 2 turns, 1 ack |
| Index report | `ensureWarm` on a directory with one corrupt file → `IndexReport.Unreadable` has one entry; the operator notice names it |

## Integration boundaries

| System | Contract | On failure |
|---|---|---|
| Lifecycle store | durable walk; per-record write lock | unreadable → partial report |
| WP-A dispatch / registration | I-6 reservation; `registerTurnIfAbsent` | refused → no turn |
| WP-A classification / index | I-8, I-9 | refused classes surfaced |
| WP-B delivery | I-5 for boot failures and re-nudge | undeliverable surfaced |
| Gateway WS / REST | Stop response carries `reached`, `unreachable`, `partial` (WP-E schema) | n/a |
| Originating channel | one-line partial notice | n/a |

## BDD scenarios

```gherkin
Feature: Cancel cascade, reservation, revival and boot recovery

  # Happy Path — Traces to: US-1 / AS-1
  Scenario: Stop stamps and cancels the whole tree
    Given a chain R -> A -> B -> C and B has been re-entered after C completed
    When the operator presses Stop on R
    Then R, A, B and C carry a Stop marker for their current generation
    And A, B and C are cancelled

  # Error Path — Traces to: US-1 / AS-2
  Scenario: Stop beats a queued wake
    Given C has completed and B's wake is queued but not started
    When the operator presses Stop on R
    Then B's dispatch is refused at reservation
    And B never runs

  # Error Path — Traces to: US-1 / AS-4
  Scenario: Partial cascade is reported everywhere it should be
    Given B's lifecycle record is unreadable
    When the operator presses Stop on R from Telegram
    Then A and C are cancelled
    And the Stop report lists B as unreachable and is marked partial
    And Telegram receives one line: "stopped 2 of 3; 1 unreachable"

  # Alternate Path — Traces to: US-1 / AS-5
  Scenario: Stop on a middle node leaves its ancestors alone
    When the operator presses Stop on B
    Then B and C are stamped and cancelled
    And A and R carry no marker
    And A's next re-entry runs

  # Alternate Path — Traces to: US-1 / AS-6
  Scenario: A newer instruction revives a stopped session
    Given Stop on R stamped B's record in generation 2
    When the operator writes into B
    Then B is revived as generation 3 and its turn runs

  # Alternate Path — Traces to: US-1 / AS-7
  Scenario: A launch during a cascade is stamped at launch
    Given the cascade has stamped A
    When A launches a new child
    Then the child is stamped and never starts

  # Error Path — Traces to: US-1 / AS-8
  Scenario: Stop survives a restart
    Given Stop on R stamped A, B and C
    When the process restarts
    Then A, B and C stay stopped

  # Error Path — Traces to: US-2 / AS-2
  Scenario: Two dispatches for one session
    When two dispatches for B race
    Then exactly one turn for B is registered

  # Error Path — Traces to: US-2 / AS-3
  Scenario: A stale wake is refused
    Given a wake for B carries generation 1 and B is in generation 2
    When the wake is dispatched
    Then it is refused as stale and acknowledged

  # Happy Path — Traces to: US-3 / AS-1
  Scenario: Boot failure reaches the parent
    Given C was running when the process died
    When the process boots
    Then C is failed as interrupted
    And B's inbox holds an error entry for C starting "interrupted:"

  # Happy Path — Traces to: US-3 / AS-2
  Scenario: A parked session recovers without a checkpoint
    Given C was parked with a question and no checkpoint when the process died
    When the process boots
    Then C is resumable and B's pending question is intact

  # Error Path — Traces to: US-3 / AS-3
  Scenario Outline: Boot classifies every record
    Given a record of class "<class>"
    When the process boots
    Then the consequence is "<consequence>"
    Examples:
      | class           | consequence                                   |
      | ordinary_root   | resumes as today                              |
      | steered         | resumes as today                              |
      | legacy_delegate | failed, pre-adr-091-not-resumable, reported   |
      | damaged_child   | refused, reported                             |
      | invalid_edge    | refused, reported                             |
      | unreadable      | refused, listed in the index report           |

  # Error Path — Traces to: US-3 / AS-4
  Scenario: An unreadable record is surfaced
    Given one lifecycle file is corrupt
    When the index warms
    Then the index report lists it and the operator is told

  # Alternate Path — Traces to: US-3 / AS-5
  Scenario: A stopped session stays stopped after boot unless revived
    Given B was stopped before the crash
    When the process boots
    Then B does not run
    When an answer newer than the Stop is queued for B
    Then B is revived as a new generation and continues

  # Alternate Path — Traces to: US-3 / AS-6
  Scenario: Lost wakes are re-nudged once, and only wake-eligible ones
    Given four unacknowledged inbox entries for B: a handback, a question, a progress, and a consumed handback
    When the process boots
    Then B's turn starts twice, the progress entry is untouched, and the consumed entry is acknowledged

  # Error Path — Traces to: US-1 / AS-9
  Scenario: A cancel aimed at an older generation is refused
    Given the cascade stamped B in generation 1
    And B was revived and registered in generation 2 before the cancel step
    When the cascade cancels B with generation 1
    Then the cancel is refused and B's generation-2 turn keeps running
    And the report lists B under SkippedNewerGeneration

  # Alternate Path — Traces to: US-1 / AS-10
  Scenario: A terminal descendant is skipped
    Given C completed before Stop was pressed on R
    When the cascade runs
    Then C's record is not written and C is listed under SkippedTerminal

  # Error Path — Traces to: US-1 / AS-11
  Scenario: A child published mid-cascade is stamped by the second pass
    Given the cascade has enumerated A's children but not yet stamped them
    When A publishes a new child under its record lock
    Then the cascade's second pass stamps the new child

  # Error Path — Traces to: US-3 / AS-7
  Scenario Outline: A half-written completion is repaired at boot
    Given C's completion crashed "<when>"
    When the process boots
    Then C's record is completed and B's inbox holds one entry "C:1:final"
    And B is woken once
    Examples:
      | when                                              |
      | after the inbox append, before the terminal write |
      | after the terminal write, before the inbox append |
```

## TDD plan

Implementers load the `test-driven-development` skill first.

| Order | Test | Level | Traces to |
|---|---|---|---|
| 1 | `TestReserveDispatch_RefusedAfterStop` | Unit | US-2/AS-1 |
| 2 | `TestReserveDispatch_RaceOneWins` | Unit | US-2/AS-2 |
| 3 | `TestReserveDispatch_StaleWakeRefused` | Unit | US-2/AS-3 |
| 4 | `TestCascade_StampsAndCancelsReenteredChild` | Integration | US-1/AS-1 |
| 5 | `TestCascade_BeatsQueuedWake` | Integration | US-1/AS-2 |
| 6 | `TestCascade_AfterRegistration` | Integration | US-1/AS-3 |
| 7 | `TestCascade_PartialReported_FrameAndChannel` | Integration | US-1/AS-4 |
| 8 | `TestCascade_MiddleNodeScoped` | Integration | US-1/AS-5 |
| 9 | `TestRevive_NewGeneration_OldMarkerInert` | Integration | US-1/AS-6 |
| 10 | `TestCascade_LaunchDuringCascadeStamped` | Integration | US-1/AS-7 |
| 11 | `TestStop_SurvivesRestart` | Integration | US-1/AS-8 |
| 12 | `TestStopRevive_OrderUnderLock` | Unit | edge |
| 13 | `TestPrearm_QueuedDescendantImminentUnderStoppedNode` | Unit | US-1/AS-2 |
| 14 | `TestBoot_FailureDeliveredUpward` | Integration | US-3/AS-1 |
| 15 | `TestBoot_ParkedRecoverableWithoutCheckpoint` | Integration | US-3/AS-2 |
| 16 | `TestBoot_ClassifiesAllClasses` | Integration | US-3/AS-3 |
| 17 | `TestIndexReport_SurfacedToOperator` | Unit | US-3/AS-4 |
| 18 | `TestBoot_StoppedStaysStoppedUnlessNewerInstruction` | Integration | US-3/AS-5 |
| 19 | `TestBoot_RenudgesUnconsumedEntriesOnce_EligibleOnly` | Integration | US-3/AS-6 |
| 20 | `TestCascade_CancelCarriesGeneration_RevivedSkipped` | Integration | US-1/AS-9 |
| 21 | `TestCascade_TerminalDescendantSkipped` | Integration | US-1/AS-10 |
| 22 | `TestCascade_SecondPassStampsLateChild` | Integration | US-1/AS-11 |
| 23 | `TestBoot_RepairsHalfWrittenCompletion` | Integration | US-3/AS-7 |

### Test datasets

| ID | Input | Expected | Traces to |
|---|---|---|---|
| D-1 | Stop on R / A / B / C in the fixture | stamps and cancels {R,A,B,C} / {A,B,C} / {B,C} / {C} | US-1 |
| D-2 | queued wake at t−1 ms / t+1 ms relative to Stop | refused / cancelled after registration | US-1/AS-2,3 |
| D-3 | unreadable ∈ {B, C, root} | partial report naming it; one channel line | US-1/AS-4 |
| D-4 | B stamped in g / B revived as g+1 / sibling never stamped / wake with g after revival | cancelled / not / not / stale-refused | US-1/AS-6, US-2/AS-3 |
| D-5 | crash states: running, parked (with/without checkpoint), queued, stopped, each I-8 class | per the classification outline | US-3 |
| D-6 | 100 concurrent reservations for one session | exactly 1 true | US-2/AS-2 |
| D-7 | unacknowledged entries 0 / 3 (1 consumed) | 0 / 2 turns + 1 ack | US-3/AS-6 |

### Regression requirements

Preserved: existing cancellation tests for live descendants (`subturn_*cancel*_test.go`, per WP-G classification **retain**); `/goal clear`, StopPlan, StopTask entry behaviour; the pre-arm latch's 5-second consume-once semantics for root turns. #670's missing coverage is closed by tests 4–6 and 11.

## Functional requirements

| ID | Requirement |
|---|---|
| FR-D-001 | Stop MUST be one operation under the stopped node's cascade lock: enumerate, stamp every reachable non-terminal descendant, cancel each live turn **with the stamped generation**, enumerate once more for late children; MUST skip terminal descendants without writing (`SkippedTerminal`); and MUST report unreachable ones as partial — in the report, on the Stop response frame, and as one line on the originating channel. |
| FR-D-002 | Every steered dispatch MUST reserve against the session's own record (I-6): refused when it carries a Stop marker for its current generation, when the wake's generation is older than the record's, when the record is terminal without a follow-up, or when a turn is already registered; exactly one of two concurrent dispatches wins; siblings are independent. |
| FR-D-003 | The pre-arm latch MUST treat a queued descendant wake as imminent under the stopped node. |
| FR-D-004 | A Stop marker MUST name the generation it stops; a cancel MUST carry that generation and MUST be refused by the registry when the registered turn's generation differs (`SkippedNewerGeneration`); a session revived by a newer instruction or given a terminal follow-up MUST get a new generation to which the old marker no longer applies; a Stop on a middle node MUST stamp only that node's subtree; Stop and revive on one record MUST be serialised by the record's lock in arrival order. |
| FR-D-005 | Boot recovery MUST deliver an `error` entry (`fatal: true`, `interrupted:`) to the parent of an interrupted steered session. |
| FR-D-006 | Boot recovery MUST recover a parked session from its record without requiring a checkpoint. |
| FR-D-007 | Boot recovery MUST classify every record per I-8 and apply the stated consequence; `legacy_delegate` and `damaged_child` MUST never be resumed. |
| FR-D-008 | Boot MUST consume the I-9 index report and surface every unreadable record to the operator. |
| FR-D-009 | A Stop MUST survive a restart: stamped sessions stay stopped; only a newer instruction revives. |
| FR-D-010 | Boot MUST re-wake every **wake-eligible** (I-5 table) unacknowledged inbox entry without a consumed marker, once, and MUST NOT wake progress or checkpoint entries. |
| FR-D-011 | Boot MUST repair a half-written terminal outcome in either order: finish a non-terminal record whose `<child>:<gen>:final` entry exists, or recreate the entry (same id) for a terminal record whose parent inbox lacks it; then wake once. |

## Success criteria

| ID | Criterion |
|---|---|
| SC-D-1 | 100% of fixture Stops stamp and reach every readable descendant within 2 s, and hold after `Reboot()`. |
| SC-D-2 | 0 queued or stale re-entries run after a Stop in 1,000 randomized timings. |
| SC-D-3 | 100% of simulated crash states produce exactly one parent notification. |

## Traceability

| Requirement | Story | Scenarios | Tests |
|---|---|---|---|
| FR-D-001 | US-1 | whole tree; partial; middle node | 4, 6, 7, 8 |
| FR-D-002 | US-2 | queued wake; race; stale | 1, 2, 3, 5 |
| FR-D-003 | US-1 | queued wake | 13 |
| FR-D-004 | US-1 | middle node; revive; order | 8, 9, 12 |
| FR-D-005 | US-3 | boot failure | 14 |
| FR-D-006 | US-3 | parked recovery | 15 |
| FR-D-007 | US-3 | classification outline | 16 |
| FR-D-008 | US-3 | unreadable surfaced | 17 |
| FR-D-009 | US-1, US-3 | survives restart; stays stopped | 11, 18 |
| FR-D-010 | US-3 | re-nudge, eligible only | 19 |
| FR-D-011 | US-3 | half-written repair | 23 |
| FR-D-001 (generation, terminal, second pass) | US-1 | older generation refused; terminal skipped; late child | 20, 21, 22 |

ADR ACs covered: AC-8, AC-11 (the walked-root containment becomes reconstruction + reservation), §7.

## Ambiguity warnings

| What was ambiguous | Resolution |
|---|---|
| Stop authority duration for a generation with no live turn | **Founder decision (round 5): durable; survives a restart; only a newer instruction revives** |
| Who is shown a partial-Stop report on a headless channel | **Founder decision (round 6): one line to the channel the Stop came from** |
| Whether a root chat's Stop can be durable | **Founder decision (round 9): yes — the launcher gives it a record at its first delegation** |

## Holdout evaluation scenarios (not for development)

1. Delegate three levels deep, let the bottom finish, press Stop at the top: everything stops, no orphan keeps running.
2. Press Stop at the exact moment a middle worker is about to be woken: it never wakes.
3. Corrupt one middle record; press Stop: the others stop and the UI says the Stop was partial and why.
4. Kill the process mid-delegation; restart: the parent is told the child failed.
5. Kill the process while a worker is waiting for an answer; restart; answer it: the worker continues.
6. Upgrade an instance with an old running delegation: it is failed and reported, not resumed.
7. Press Stop, then write into the stopped worker: it continues as a new generation.
8. Press Stop, restart the process: nothing under that session runs.

## Definition of done

1. *Code correct and tested:* TDD plan green; #670 closed by tests 4–6 and 11.
2. *Reachable:* holdout 1–4, 7 and 8 executed in the real UI.
