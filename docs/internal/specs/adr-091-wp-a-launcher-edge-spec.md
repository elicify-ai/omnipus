# ADR-091 WP-A — Launcher, steered-by edge, turn reconstruction, admission

- **Decision record:** [ADR-091](../architecture/ADR-091-steered-sessions-replace-subagents.md) D1, D2, D6 (prerequisites), D9
- **Landing order:** [adr-091-landing-order.md](adr-091-landing-order.md) — publishes I-1, I-2, I-3, I-8, I-9; consumes I-6, I-7
- **Owner files:** landing order §3, row A
- **Status:** Draft rev 2 (consolidated after three grills; supersedes every earlier sentence of rev 1)

## Summary

Today a delegated session is built two different ways — one on first run, another when it is woken — and neither gives it a workspace, a title or a memory of its own. This package makes creating and re-entering a steered session **one operation each**: a launcher that writes a complete session in one go (the task launcher, generalised), and a reconstruction step that rebuilds a turn from the session's record on every entry. It also owns the three things every other package reads: the record's classification (I-8), the turn registry's compare-and-set (I-3) and the admission loop that counts executing turns (D9). After this package, a session cannot come back "able to talk to the user" or "unreachable by Stop", because nothing on the entry path decides those things any more.

## Existing codebase context

*Source inspection at `364290cb5`; GitNexus unavailable for this worktree.*

### Symbols involved

| Symbol | Role today | Change |
|---|---|---|
| `pkg/agent/subturn.go::createChildSession` | creates the delegate session; stamps parent meta; no workspace, no title | **deleted** — the launcher replaces it |
| `pkg/agent/subturn.go::prepareProcessOptions` | copies parent `Channel`/`ChatID`/`SenderID`/`SenderDisplayName`; `SendResponse:false` | **deleted** — reconstruction (I-3) replaces it |
| `pkg/agent/subturn.go::buildDelegateAgent` | installs `newEphemeralSession` (50-message ring) as the child's `Sessions` | ring removed; the child uses the agent's real store |
| `pkg/agent/subturn.go::newEphemeralSession`, `truncateLocked`, `maxEphemeralHistorySize`, `ephemeralSessionStore` | the ring | **deleted** |
| `pkg/agent/subturn.go` `SubTurnConfig.Async` (internal field) | always true once `executeSync` (WP-C) is gone | **deleted** with its `false` branches |
| `pkg/agent/subturn.go::getSubTurnConfig`, `defaultConcurrencyTimeout`, the semaphore wait in the spawn path | reads the `SubTurn` block; blocks up to `ConcurrencyTimeoutSec` for a slot | **deleted**; admission is the queue in I-3 |
| `pkg/agent/loop_inbound.go::processSystemMessage` | rebuilds a woken session by hand with `SendResponse:true`; no parent or root restore | **rewritten** to call reconstruction; classifies first (I-8); refuses stale wakes; writes the consumed marker |
| `pkg/agent/turn.go::newTurnState` | defaults `routingSessionID` to the turn's own session | identity taken from the edge |
| `pkg/agent/turn.go::registerActiveTurn` | plain `activeTurnStates.Store` — no duplicate rejection; cancel by session id only | gains internal `registerTurnIfAbsent(ts)` (compare-and-set) and a generation on the registered turn so `RequestCancelForSession(id, gen)` can refuse a cancel aimed at an older generation (I-3, I-6) |
| `pkg/session/lifecycle.go::LifecycleRecord` | has `ParentDurableKey`, `ParentAgentID`, `Generation`, `OriginChannel`, `OriginChatID`, `WorkspaceID`, `GoalRef`, `NeedsInput` | gains `SteeredBy`, `Origin`, `Stop` (I-1); the **existing** `Generation` keeps its terminal-immutability role and gains the revival semantics; `ParentDurableKey` **deleted in the same delivery** once every reader (15 non-test files, by owner in ADR-091 D2) reads the edge; `ParentAgentID` stamped by the launcher for both fronts |
| `pkg/session/lifecycle.go::persistLocked` | rejects same-generation writes after a terminal record | unchanged — this is why a follow-up on a terminal session bumps the generation first (as `delegate_followup.go::spawnCorrectiveFollowUp` does today) and why a Stop skips terminal descendants |
| `pkg/session/lifecycle.go::pruneTerminalOne` | prunes terminal records without checking descendants | refuses while any descendant is non-terminal (I-1 invariant) |
| `pkg/session/lifecycle_index.go::ensureWarm` | skips unreadable records silently | records them; the index gains an exported `Report()` accessor returning `steer.IndexReport` (I-9) |
| `pkg/config/config_defaults_apply.go::EffectiveMaxParallelAgents` | resolves the environment override, a positive configured value, and zero/unset to the existing safety backstop | **reused as is** — the admission loop compares against this value, never against the raw field (R16) |
| `pkg/steer` (new package) | — | every published interface and type (landing order §2); imports nothing from `pkg/agent`; published complete at CP-0 |
| `pkg/session/unified_api.go::CreateSessionWithID` | copies the parent's `Owner` | called by the launcher with workspace + title |
| `pkg/agent/task_executor.go::createTaskSessionSync` | stamps `Title`, `TaskID`, `WorkspaceID`; logs and continues on failure | **becomes the launcher** (extracted and generalised with the edge — not a third path); leniency removed |
| `pkg/agent/task_executor.go::mintTaskLifecycleRecord` | writes the task's lifecycle record without parent fields | folded into the launcher; populates the edge |
| `pkg/agent/task_executor.go::StartTaskNow` | mints the task's session when `Task.SessionID` is empty; returns without dispatch otherwise | calls `Launch` when `Task.SessionID` is empty, then `Dispatch`; calls `Dispatch` alone otherwise (no-op if the session is terminal); reads `task.Task.OriginSessionID` as the steering session when that session still exists |
| `pkg/agent/task_executor_run.go::processTaskDirectExternalCLI` | the task-side external-CLI wrapper | the **one** wrapper both fronts use after D10 |
| `pkg/agent/delegation_depth.go::resolveEffectiveDelegationDepth` | global ∧ per-edge; an explicit per-edge depth wins when the global value is zero | same rule, reading `performance.max_delegation_depth` (D9); precedence carried over unchanged |
| `pkg/agent/admission.go::ResolveRootDelegationCap` | prefers `SubTurn.MaxConcurrent` | reads `EffectiveMaxParallelAgents`; gains the admission lock and loop (counts executing turns; `Dispatch` decides `running` / `queued` atomically under it; dispatches the oldest `queued` session when a turn ends) |
| `pkg/config/config.go::SubTurnConfig` | `MaxDepth`, `MaxConcurrent`, `DefaultTimeoutMinutes`, `ConcurrencyTimeoutSec` | **deleted**; `MaxDepth` → `Performance.MaxDelegationDepth`, `DefaultTimeoutMinutes` → `Performance.DelegationTimeoutMinutes` (same defaults, carried over); the other two have no successor |
| `pkg/sysagent/tools/workspace.go`, `pkg/gateway/rest_workspace_delegation.go` | read `SubTurn.MaxDepth` | read the moved key; owned here for that change only |
| `pkg/agent/empty_in_place.go::eligibleToolResults` | skips results with no archive line | unchanged; now sees every result because the store is real |

### Impact assessment (manual)

| Symbol modified | Risk | Direct dependents that must be re-tested |
|---|---|---|
| `processSystemMessage` | **HIGH** — every async completion, plan-owner wake and goal follow-up enters here | `async_notifier.go`, `goal_triggers.go`, plan engine wakes, `message_parent` wakes |
| `LifecycleRecord` shape | **HIGH** — persisted format | `lifecycle_index.go`, `list_jobs_sources.go`, `boot_sweep.go`, `cancel.go` |
| `registerActiveTurn` | HIGH — every turn | `cancel_prearm.go`, `steering.go` |
| `newTurnState` identity | MEDIUM | `cancel_prearm.go`, `steering.go` predicates |
| `SubTurnConfig` removal | MEDIUM | `getSubTurnConfig` callers incl. external tasks |

### Execution flows touched

Delegate run → launch → dispatch; task start → launch → dispatch; async completion → wake → reconstruction → turn; boot recovery → classification → reconstruction; turn end → admission loop → next queued dispatch.

## User stories and acceptance criteria

### US-1 — A steered session is created whole (P0)

*As the operator, I want every steered session to be findable and complete from the moment it exists, so that nothing about it depends on which tool created it.*

**Why P0:** every other package builds on the record this creates.
**Independent test:** launch through each front door; close and reopen the stores; read the record back from disk.

1. **Given** a steering session in workspace W with agent A, **When** it launches a steered session for agent B, **Then** the new session's record carries workspace W, a non-empty title, an edge whose steering session is the launcher and whose root is the launcher's own root, a record-level `Origin` naming the front door and its tool-call id, `Generation` 1, no Stop marker, and the derived `ParentSessionID`, `Owner` and `ParentAgentID` — the child is published under the parent's record lock — and the same is read back after the stores are closed and reopened.
2. **Given** a steering session that has no lifecycle record of its own (a chat that never delegated), **When** it launches, **Then** the launcher first writes an `ordinary_root` record for it (founder decision, round 9).
3. **Given** a steering session with no workspace, **When** it launches, **Then** the child has no workspace and the launch succeeds.
4. **Given** any mandatory write fails (identity, owner, workspace, title, edge, the steering session's own record), **When** the launch is attempted, **Then** no session directory, no lifecycle record and no transcript exist afterwards, and the caller receives an error naming the failed write.
5. **Given** a launch with a goal, **When** the session is created, **Then** its record carries the goal reference and the goal is readable in the same shape a task's goal has.
6. **Given** a launch from `delegate` and a launch from `create_task` for the same agent, **When** both records are read, **Then** they differ only in `Origin` and in the presence of a task record.
7. **Given** neither a label nor task text, **When** the launch is attempted, **Then** it is refused with `ErrTitleRequired` before any write (founder decision, round 5).

### US-2 — A steered session has one memory (P0)

*As the operator, I want a steered session to remember its whole conversation the way any session does, so that its context handling and history are ordinary.*

**Why P0:** the ring is the proposed cause of #775 and the reason #803 cannot resume.
**Independent test:** run a child through more than fifty messages and read its history.

1. **Given** a steered session that has exchanged sixty messages, **When** its model history is read, **Then** all sixty are present from the same store its transcript is written to.
2. **Given** a steered session whose old tool results are large, **When** the context-emptying pass runs, **Then** those results are eligible and are shrunk.
3. **Given** a steered session interrupted mid-turn, **When** it is re-entered, **Then** it continues from its persisted history, not from an empty one.

### US-3 — Every entry path rebuilds the same identity (P0)

*As the operator, I want a woken or resumed steered session to behave exactly as it did when first launched, so that a wake-up can never grant it powers it did not have.*

**Why P0:** this is the live Jarvis leak and the Stop defect.
**Independent test:** three-level delegation; re-enter the middle session after its child completes; inspect its turn identity.

1. **Given** a steered session B whose child C completes, **When** B is re-entered, **Then** B's turn carries the root, steering session and reporting target from its edge, has no user audience, and its generation is unchanged (generation moves only when a stopped session is revived — D8).
2. **Given** B's record is unreadable, classified `damaged_child` (record missing, **or** record present with its edge lost while B's metadata still names a parent), `legacy_delegate` or `invalid_edge` (I-8), **When** re-entry is attempted, **Then** no turn runs, a diagnostic names the session and the class, and the failure is delivered upward; B is never treated as a root.
3. **Given** a re-entry for B whose own record carries a Stop marker for its current generation, **When** dispatch is attempted, **Then** the reservation is refused and no turn starts.
4. **Given** a wake for B written in generation 1, and B revived to generation 2 in between, **When** that wake is dispatched, **Then** it is refused as stale and acknowledged; B's next turn is the one the revival started.
5. **Given** a wake whose `message_id` B's transcript already records as consumed, **When** it arrives again, **Then** it is acknowledged without a turn.
6. **Given** any future entry path, **When** it starts a steered session's turn, **Then** it can only do so through reconstruction — a turn on a steered session with hand-built options is impossible.

### US-4 — One limit surface; a launch never blocks (P1)

*As the operator, I want depth, concurrency and timeout each set in one place, so that I can reason about them; and I never want a delegating agent's turn to stall on a limit.*

**Independent test:** set each limit once; observe it applied to a delegation and to a task; reach the cap.

1. **Given** the effective concurrency cap N and N turns executing, **When** an (N+1)th steered session or task is launched by either front door, **Then** `Dispatch` — not `Launch` — decides atomically under the admission lock, writes the record as `queued`, returns `DispatchResult{queued, position}` at once, and the session starts in launch order when a turn ends (founder decision, round 8); two launches that both see one free slot cannot both be told `running`.
2. **Given** a parent whose turn has ended and who is waiting for its children, **When** slots are counted, **Then** it holds none (founder decision, round 9): at a cap of 1, a parent's queued child starts as soon as the parent's turn ends.
3. **Given** a per-edge depth tighter than the global ceiling, **When** a chain is built, **Then** the tighter bound applies; **Given** the global ceiling tighter, **Then** it applies; **Given** the global key unset (`0`), **Then** an explicit per-edge depth applies unchanged — today's precedence, carried over.
4. **Given** a creator-set timeout, **When** the session is re-entered by follow-up, **Then** the deadline is not reset; **Given** `Limits.TimeoutSeconds` = 0, **Then** the configured default applies.
5. **Given** a negative limit value in config or on a launch, **When** it is read, **Then** it is refused with a named error; **Given** `max_parallel_agents` zero or unset, **Then** the existing safety backstop applies exactly as `EffectiveMaxParallelAgents` resolves it today, and an environment override still wins — no breaking change.
6. **Given** a launch under a parent that carries a Stop marker for its current generation, **When** the child is published, **Then** it is stamped at launch and never starts.

### Edge cases

| Case | Expected |
|---|---|
| Launch when the steering session itself is at maximum depth | refused by the depth rule before any write |
| Launch with a cycle (target chain leads back to the launcher) | refused; no write |
| Ancestor record pruned while descendant live | pruning refuses; the ancestor stays |
| Two re-entries for the same session race | `registerTurnIfAbsent` admits one; the other is dropped with a diagnostic |
| A launch that lands while a Stop cascade is passing its parent | the launcher reads the parent's record; a marker of the current generation on the parent stamps the child at launch |
| `StartTaskNow` for a task whose session already exists and is terminal | dispatch is a no-op; no second session |
| Delayed or recurring task whose creating session no longer exists | launched as an `ordinary_root` |
| Launch with `Goal` whose criteria fail task-style validation | refused with the same message `create_task` gives |
| Launch with neither a label nor task text | refused with `ErrTitleRequired`; nothing written |

## Behavioral contract

- When a steered session is launched, the system writes identity, owner, workspace, title, edge (and goal) atomically, or nothing — and the steering session's own root record if it had none.
- When a steered session is entered by any path, the system classifies its record, rebuilds its turn from the record, refuses stale or already-consumed wakes, and never trusts the entry path's arguments for identity.
- When a record cannot be validated, the system refuses to run it and reports upward.
- When the session's own record carries a Stop marker for its current generation, the system refuses dispatch.
- When the concurrency cap is reached, the system queues the launch and never blocks or refuses the caller.
- When limits are read, the system reads each from exactly one configuration key.

## Explicit non-behaviors and safeguards

### Qualitative prohibitions

- The system must not build a steered session's turn options anywhere except reconstruction, because per-path construction is how the wake-up leak and the Stop defect arose.
- The system must not keep a second in-memory history for a steered session, because two memories is the mechanism behind lost context and impossible resume.
- The system must not invent a workspace for a child whose creator has none, because inherited-never-invented is the rule that keeps a session inside its creator's boundary.
- The system must not continue after a failed mandatory write, because a half-created session is unfindable and unreachable by Stop.
- The launcher's `Launch` must not start a turn, because dispatch belongs behind the cancel reservation (I-6).
- The system must not block a launch waiting for a slot, because the founder removed the wait (round 8); it queues.
- The system must not count a waiting parent as an executing turn, because that deadlocks a tree at a small cap (round 9).

### Machine-verifiable constraints

| Constraint | Exact check |
|---|---|
| Atomic launch | after an injected failure of each mandatory write, the session directory under the test data dir is absent and the lifecycle store returns not-found |
| Readback across restart | close and reopen the stores in the test; the record read back equals the record written |
| No ring | `grep -rn -e newEphemeralSession -e ephemeralSessionStore -e maxEphemeralHistorySize pkg/ --include='*.go' --exclude='*_test.go'` → 0 (the WP-F conventions apply: production only, exact count, no-match exit is the pass); a 60-message child's `GetHistory` returns 60 |
| Identity from the edge | after re-entry, `turnState.routingSessionID == rec.SteeredBy.RootSessionID` and `processOptions.SendResponse == false` |
| Refusal by class | reconstruction returns `ErrRecordNotRunnable{Class}` for each non-runnable I-8 class; nothing registered in `activeTurnStates` |
| Stale wake | `WakeInput.Generation < rec.Generation` → refused, acknowledged, no turn |
| Consumed marker | the transcript contains exactly one `consumed <id>` entry per processed wake; a repeat wake adds none and starts no turn |
| Reservation honoured | with a Stop marker for the current generation, `Dispatch` returns `ErrDispatchCancelled` and registers nothing |
| Compare-and-set | 100 concurrent `registerTurnIfAbsent` for one key → exactly 1 true |
| Queue, not wait | at cap N, the (N+1)th `Dispatch` returns within 50 ms with `State == queued`; no goroutine blocks on a semaphore; 100 concurrent launches against 1 free slot → exactly 1 `running` |
| Effective cap | `max_parallel_agents` 0 / unset / env-overridden → the admission loop uses `EffectiveMaxParallelAgents`'s value in each case; nothing is queued behind a raw zero |
| Launch under lock | a launch racing a cascade on its parent is either stamped at launch or visible to the cascade's second pass — 1,000 randomized interleavings, 0 unstamped children |
| No agent import | `go list -deps ./pkg/steer` contains no `omnipus/pkg/agent`, `pkg/tools`, `pkg/channels` or `pkg/gateway` |
| Slot = executing turn | with cap 1, parent turn ended, one queued child → the child starts within 1 s |
| One key each | `grep -rn 'SubTurn\.' pkg/ --include='*.go' --exclude='*_test.go'` → 0; depth, concurrency and timeout each resolve from one named config path |
| Goal shape | a `LaunchRequest.Goal` that `create_task` would reject is rejected with an identical message |

## Integration boundaries

| System | In / out | Contract | On failure |
|---|---|---|---|
| Lifecycle store | writes the record with edge; reads on every entry | I-1, I-8, I-9 | unreadable → refuse to run, report upward (WP-B I-5) |
| Session store (`UnifiedStore`) | creates the session directory, meta, transcript | `CreateSessionWithID` + meta stamp | failure → rollback, error |
| Cancel reservation (WP-D) | asked before every dispatch | I-6 | refused → no turn |
| Task executor | calls the launcher for tasks | I-2 | as above |
| Delegate front (WP-C) | calls `Launch` then `Dispatch` | I-2 | as above |
| Fixture (WP-G) | builds trees through the launcher | I-2, I-7 | n/a |

## BDD scenarios

```gherkin
Feature: Steered session launch, reconstruction and admission

  # Happy Path — Traces to: US-1 / AS-1, AS-2
  Scenario: A delegation creates a complete session and a root record
    Given agent "jim" is running in chat session R in workspace W, and R has no lifecycle record
    When R launches a steered session for agent "worker" with task "build the page" from tool call "call-7"
    Then a lifecycle record for R exists with class ordinary_root
    And a session S exists with workspace W and a non-empty title
    And S.SteeredBy.SteeringSessionID is R and S.SteeredBy.RootSessionID is R
    And S.SteeredBy.Origin is {delegate, "call-7"}
    And S.Generation is 1 and S.Stop is nil
    And S.ParentSessionID is R, S.Owner equals R's owner and S.ParentAgentID is "jim"
    When the stores are closed and reopened
    Then the same record is read back

  # Edge Case — Traces to: US-1 / AS-3
  Scenario: A creator without a workspace yields a child without one
    Given session R has no workspace
    When R launches a steered session
    Then the child's workspace is empty and the launch succeeded

  # Error Path — Traces to: US-1 / AS-4
  Scenario Outline: A failed mandatory write leaves nothing behind
    Given the "<write>" write is made to fail
    When R launches a steered session
    Then the launch returns an error naming "<write>"
    And no session directory, lifecycle record or transcript exists for it
    Examples:
      | write       |
      | identity    |
      | owner       |
      | workspace   |
      | title       |
      | edge        |
      | root-record |

  # Happy Path — Traces to: US-1 / AS-5
  Scenario: A goal is stored in create_task's shape
    When R launches a steered session with criteria and a Definition of Done
    Then S's record carries a GoalRef readable as a task goal

  # Happy Path — Traces to: US-1 / AS-6
  Scenario: Both front doors produce the same record
    When R launches worker sessions through delegate and through create_task
    Then the two records differ only in Origin and in the presence of a task record

  # Error Path — Traces to: US-1 / AS-7
  Scenario: No title, no launch
    When R launches with an empty label and empty task text
    Then the launch is refused with ErrTitleRequired and nothing is written

  # Happy Path — Traces to: US-2 / AS-1
  Scenario: History is one store
    Given steered session S has exchanged 60 messages
    When S's model history is loaded for its next turn
    Then it contains all 60 messages and they match S's transcript

  # Happy Path — Traces to: US-3 / AS-1
  Scenario: Re-entry rebuilds identity from the edge
    Given a chain R -> A -> B -> C built by the fixture
    And C has completed
    When B is re-entered to process C's result
    Then B's turn routing root is R
    And B's turn has no user audience
    And B's generation is unchanged

  # Error Path — Traces to: US-3 / AS-2
  Scenario Outline: A non-runnable record refuses to run
    Given B's record is made "<class>"
    When B is re-entered
    Then no turn is registered for B
    And a diagnostic names B and "<class>"
    And an error event is delivered to A
    Examples:
      | class           |
      | invalid_edge    |
      | damaged_child   |
      | legacy_delegate |
      | unreadable      |

  # Error Path — Traces to: US-3 / AS-3
  Scenario: A stamped record refuses re-entry
    Given Stop on R has stamped B's record for its current generation
    When B's queued re-entry is dispatched
    Then dispatch is refused and no turn starts

  # Error Path — Traces to: US-3 / AS-4
  Scenario: A stale wake after revival is refused
    Given a wake for B was written while B was in generation 1
    And B has since been revived to generation 2
    When the generation-1 wake is dispatched
    Then it is refused as stale and acknowledged, and no turn starts

  # Error Path — Traces to: US-3 / AS-5
  Scenario: A consumed wake is not processed twice
    Given B's transcript records "consumed m-1"
    When a wake with message id "m-1" arrives again
    Then it is acknowledged and no turn starts

  # Alternate Path — Traces to: US-4 / AS-1, AS-2
  Scenario: At the cap a launch is queued, and a waiting parent holds no slot
    Given max_parallel_agents is 1 and Jim's turn is executing
    When Jim launches a worker
    Then the launch returns at once with state "queued" and position 1
    When Jim's turn ends
    Then the worker starts within one second

  # Alternate Path — Traces to: US-4 / AS-3
  Scenario Outline: Depth precedence is carried over
    Given the global depth key is "<global>" and the edge depth is "<edge>"
    When a chain is built
    Then the effective depth is "<effective>"
    Examples:
      | global | edge | effective |
      | 3      | 2    | 2         |
      | 2      | 3    | 2         |
      | 0      | 4    | 4         |

  # Alternate Path — Traces to: US-4 / AS-4
  Scenario: Follow-up does not reset the deadline
    Given S was launched with a 10-minute timeout 8 minutes ago
    When S is re-entered by follow-up
    Then S's remaining time is 2 minutes

  # Error Path — Traces to: US-4 / AS-5
  Scenario Outline: Limit boundaries
    When the limit "<key>" is set to "<value>"
    Then the result is "<result>"
    Examples:
      | key                 | value | result           |
      | timeout_seconds     | 0     | default applies  |
      | timeout_seconds     | -1    | refused          |
      | timeout_seconds     | 1     | accepted         |
      | max_delegation_depth| -1    | refused at load  |
      | max_parallel_agents | 0     | existing backstop applies |
      | max_parallel_agents | -1    | refused at load  |

  # Error Path — Traces to: US-4 / AS-1
  Scenario: Two launches, one slot, one running
    Given the effective cap is 1 and no turn is executing
    When two workers are launched concurrently
    Then exactly one Dispatch result is "running" and the other is "queued" at position 1

  # Error Path — Traces to: US-4 / AS-6
  Scenario: A launch under a stamped parent never starts
    Given A carries a Stop marker for its current generation
    When A launches a child
    Then the child is stamped at launch and its dispatch is refused

  # Error Path — Traces to: US-3 / AS-2
  Scenario: A child whose edge was lost is not a root
    Given C's record exists but its SteeredBy was deleted, and C's metadata still names B as parent
    When C is classified
    Then the class is damaged_child and C gets no audience and no turn

  # Error Path — Traces to: edge case (ancestor pruned)
  Scenario: Retention refuses to prune a live subtree's ancestor
    Given B is terminal and its child C is running
    When retention runs pruneTerminalOne on B
    Then B's record is kept

  # Error Path — Traces to: edge case (two re-entries race)
  Scenario: Two re-entries for one session register one turn
    When two re-entries for B race
    Then registerTurnIfAbsent admits exactly one and the other is dropped with a diagnostic

  # Error Path — Traces to: FR-A-014
  Scenario: An unreadable record is reported by the index
    Given one lifecycle file is corrupt
    When the index warms
    Then Report() lists that session id and the error
```

## TDD plan

Implementers load the `test-driven-development` skill first. Order: unit → integration → cross-package (WP-G).

| Order | Test | Level | Traces to | Description |
|---|---|---|---|---|
| 1 | `TestLaunch_WritesCompleteRecord_ReadbackAfterReopen` | Integration | US-1/AS-1 | record fields, derived meta, origin, generation; stores closed and reopened |
| 2 | `TestLaunch_WritesRootRecordForRecordlessSteerer` | Integration | US-1/AS-2 | `ordinary_root` record for the chat that delegates |
| 3 | `TestLaunch_NoWorkspaceInherited` | Unit | US-1/AS-3 | empty stays empty |
| 4 | `TestLaunch_AtomicOnEachWriteFailure` | Unit | US-1/AS-4 | table over the six writes, injected failure |
| 5 | `TestLaunch_GoalMirrorsCreateTask` | Unit | US-1/AS-5 | same validation errors |
| 6 | `TestLaunch_OriginOnlyDifference` | Unit | US-1/AS-6 | diff of two records |
| 7 | `TestLaunch_TitleRequired` | Unit | US-1/AS-7 | empty pair refused |
| 8 | `TestSteered_HistoryIsRealStore` | Integration | US-2/AS-1 | 60 messages |
| 9 | `TestSteered_EmptyingSeesAllResults` | Integration | US-2/AS-2 | eligibility after ring removal |
| 10 | `TestReconstruct_IdentityFromEdge` | Integration | US-3/AS-1 | three-level fixture |
| 11 | `TestReconstruct_RefusesNonRunnableClasses` | Integration | US-3/AS-2 | one case per non-runnable I-8 class |
| 12 | `TestClassifyRecord_AllClasses_RecordMetaConsistency` | Unit | I-8 | one fixture per row of the I-8 table, including both `damaged_child` forms (record deleted; edge deleted with metadata naming a parent) and a genuine root of each session type |
| 13 | `TestDispatch_HonoursReservation` | Integration | US-3/AS-3 | with WP-D's I-6 stub until published |
| 14 | `TestDispatch_RefusesStaleWake` | Integration | US-3/AS-4 | generation 1 wake after revival to 2 |
| 15 | `TestReconstruct_ConsumedMarker_NoSecondTurn` | Integration | US-3/AS-5 | repeat wake acked, no turn |
| 16 | `TestRegisterTurnIfAbsent_OneWins` | Unit | edge | 100 concurrent registrations |
| 16b | `TestDispatch_ResultAuthoritative_ConcurrentLaunches` | Integration | US-4/AS-1 | 100 concurrent launches, 1 slot → 1 `running` |
| 16c | `TestAdmission_UsesEffectiveCap` | Unit | US-4/AS-5 | 0 / unset / env override |
| 16d | `TestLaunch_UnderStampedParent_StampedAtLaunch` | Integration | US-4/AS-6 | replaces the earlier launch-during-cascade test |
| 17 | `TestNoHandBuiltSteeredOptions` | Unit (static) | US-3/AS-6 | a guard test scanning for `processOptions{` on steered paths outside reconstruction |
| 18 | `TestLaunch_AtCap_QueuedNotBlocked` | Integration | US-4/AS-1 | cap reached → `queued`, returns at once; slot frees → starts in order |
| 19 | `TestAdmission_WaitingParentHoldsNoSlot` | Integration | US-4/AS-2 | cap 1; parent turn ends; queued child starts |
| 20 | `TestDepth_PrecedenceCarriedOver` | Unit | US-4/AS-3 | the three-row table |
| 21 | `TestLimits_SingleSource_Boundaries` | Unit | US-4/AS-4,5 | each key once; zero and negative semantics |
| 22 | `TestPrune_RefusesWhileDescendantLive` | Integration | edge | `pruneTerminalOne` on a terminal parent with a running child → refused |
| 23 | `TestTaskFront_UsesLauncher` | Integration | US-1/AS-6, FR-A-010 | `StartTaskNow` with empty / populated / terminal `SessionID`; agent-created vs human-created vs creator-gone parentage |
| 24 | `TestLaunch_RacingCascade_NeverUnstamped` | Integration | edge | 1,000 interleavings of launch vs cascade on the parent → 0 unstamped children |
| 25 | `TestIndexReport_UnreadableListed` | Unit | I-9 | `ensureWarm` returns the unreadable ids |

### Test datasets

| ID | Input | Expected | Traces to |
|---|---|---|---|
| A-1 | creator ws=W, agent=B | child ws=W | US-1/AS-1 |
| A-2 | creator ws="" | child ws="" | US-1/AS-3 |
| A-3 | fail write ∈ {identity, owner, workspace, title, edge, root-record} | nothing on disk | US-1/AS-4 |
| A-4 | goal with 0 criteria / 1 / 25 / duplicate ids | reject / ok / ok / reject, same as `create_task` | US-1/AS-5 |
| A-5 | history length 0 / 49 / 50 / 51 / 500 | all present | US-2/AS-1 |
| A-6 | chain depth 1 / 2 / 3 / ceiling / ceiling+1 | ok ×4, refuse | US-3, US-4 |
| A-7 | class ∈ {invalid_edge, damaged_child, legacy_delegate, unreadable} | refuse each, named | US-3/AS-2 |
| A-8 | concurrent re-entries ×2, ×100 | one runs | edge |
| A-9 | `TimeoutSeconds` 0 / −1 / 1 / 10⁶ | default / reject / ok / ok | US-4/AS-4,5 |
| A-10 | depth (global, edge) ∈ {(3,2), (2,3), (0,4), (−1, any)} | 2 / 2 / 4 / refused at load | US-4/AS-3,5 |
| A-11 | cap N ∈ {1, 2}; launches N+1 | last queued; starts when a turn ends | US-4/AS-1,2 |
| A-12 | wake generation g vs record generation {g, g+1} | run / refused-stale | US-3/AS-4 |

### Regression requirements

Preserved: every existing target-identity test in `subturn_target_identity_test.go` (a child never inherits agent settings — ADR-032); `delegateSessionID == sessionKey` alignment; owner copy on creation; `resolveEffectiveDelegationDepth`'s precedence when the global value is zero. Existing tests that assert the ring (`newEphemeralSession`, 50-message truncation) are **deleted** under WP-G's classification; tests asserting parent-address inheritance are **updated** to assert the edge; `pkg/agent/async_child_publication_test.go::spawnPublicationTestChild` — which constructs `&ephemeralSessionStore{}` — has its **setup moved** to the persisted-store fixture by this package, with every assertion and control unchanged (WP-G classification: update).

## Functional requirements

| ID | Requirement |
|---|---|
| FR-A-001 | The system MUST create a steered session with identity, owner, workspace, title, edge (`Origin` included) and, when a goal is given, `GoalRef`, in one atomic operation, and MUST first write an `ordinary_root` record for a steering session that has none. |
| FR-A-002 | The system MUST inherit the creator's workspace and MUST NOT invent one. |
| FR-A-003 | The system MUST store a steered session's model history in the same persisted store as its transcript. |
| FR-A-004 | The system MUST classify every record (I-8) before running it, MUST rebuild every steered turn from its record, and MUST refuse to run any class but `steered` or `ordinary_root`. |
| FR-A-005 | The system MUST verify the cascade root by walking the chain at launch and MUST reject cycles and root mismatches. |
| FR-A-006 | The system MUST NOT prune an ancestor record while any descendant is non-terminal; proven by a test that actually invokes pruning against a live descendant. |
| FR-A-007 | The system MUST ask the cancel reservation (I-6) before every dispatch, MUST refuse a wake whose generation is older than the record's, and MUST acknowledge without a turn a wake whose `message_id` the transcript already records as consumed. |
| FR-A-008 | The system MUST read concurrency through `EffectiveMaxParallelAgents` (environment override, then a positive `performance.max_parallel_agents`, then the existing backstop for zero/unset — unchanged; negative rejected at load), depth from `performance.max_delegation_depth` (moved from `SubTurn.MaxDepth`; same default; `0` = unset with today's per-edge precedence; negative rejected at load) and the default timeout from `performance.delegation_timeout_minutes` (moved from `SubTurn.DefaultTimeoutMinutes`; same default; negative rejected at load); `Limits.TimeoutSeconds` `0` = that default, negative refused; `SubTurn.ConcurrencyTimeoutSec` and `defaultConcurrencyTimeout` are deleted. |
| FR-A-009 | The system MUST accept an optional goal in the same shape as `create_task` and record it on the session. |
| FR-A-010 | The task front MUST create its session through the launcher: `StartTaskNow` calls `Launch` then `Dispatch` when `Task.SessionID` is empty and `Dispatch` alone otherwise (no-op if terminal); a task created by an agent carries its creating session (`task.Task.OriginSessionID`, set by WP-C) as `SteeringSessionID` when that session still exists, otherwise — and for human-, schedule- or plan-created tasks — it is an `ordinary_root`. |
| FR-A-011 | `Dispatch` MUST decide admission atomically under the admission lock and return the authoritative `DispatchResult`; at the cap it MUST leave the record `queued` with its position, and the admission loop MUST start queued sessions in launch order as executing turns end; a launch MUST NOT block and MUST NOT be refused for the cap. |
| FR-A-012 | The concurrency counter MUST count executing turns only; a session whose turn has ended holds no slot. |
| FR-A-013 | `registerTurnIfAbsent` MUST be a compare-and-set on `activeTurnStates` keyed by the turn's session key, recording the turn's generation; two concurrent registrations for one session MUST resolve to exactly one turn; `RequestCancelForSession(id, gen)` MUST refuse a cancel whose generation differs from the registered turn's. |
| FR-A-014 | The lifecycle index MUST expose `Report()` naming every unreadable record (I-9). |
| FR-A-015 | The launcher MUST publish a child under its parent's record lock and MUST stamp the child at launch when the parent carries a Stop marker for its current generation. |
| FR-A-016 | `pkg/steer` MUST be published complete at CP-0 and MUST import nothing from `pkg/agent`, `pkg/tools`, `pkg/channels` or `pkg/gateway`. |
| FR-A-017 | A record MUST be classified `ordinary_root` only when the record and the session's metadata agree (no `ParentSessionID`, type not `delegate`); an edge lost from a present record MUST classify as `damaged_child`. |

## Success criteria

| ID | Criterion |
|---|---|
| SC-A-1 | 0 delegate sessions created after landing lack `workspace_id` when their creator has one (live-instance query). |
| SC-A-2 | The ring grep (constraints table) returns 0. |
| SC-A-3 | 100% of re-entries in the three-level fixture carry the root from the edge. |
| SC-A-4 | Every injected mandatory-write failure leaves 0 artefacts. |
| SC-A-5 | 0 launches block at the cap across 1,000 randomized launches; 100% of queued launches start in order. |

## Traceability

| Requirement | Story | Scenarios | Tests |
|---|---|---|---|
| FR-A-001 | US-1 | complete session; root record; failed write | 1, 2, 4 |
| FR-A-002 | US-1 | no workspace | 3 |
| FR-A-003 | US-2 | one store | 8, 9 |
| FR-A-004 | US-3 | re-entry; non-runnable classes | 10, 11, 12, 17 |
| FR-A-005 | US-3 | invalid edge | 11 |
| FR-A-006 | edge | pruned ancestor | 22 |
| FR-A-007 | US-3 | stamped record; stale wake; consumed wake | 13, 14, 15 |
| FR-A-008 | US-4 | precedence; boundaries; deadline | 20, 21 |
| FR-A-009 | US-1 | goal | 5 |
| FR-A-010 | US-1 | both front doors | 6, 23 |
| FR-A-011 | US-4 | queued at cap; two launches one slot | 18, 16b |
| FR-A-012 | US-4 | waiting parent holds no slot | 19 |
| FR-A-013 | edge | two re-entries register one turn | 16 |
| FR-A-014 | edge | unreadable reported | 25 |
| FR-A-015 | US-4 | launch under a stamped parent | 16d, 24 |
| FR-A-016 | — | static import check | constraints table |
| FR-A-017 | US-3 | edge lost is not a root | 12 |
| FR-A-008 (cap) | US-4 | limit boundaries | 16c, 21 |

ADR ACs covered here: AC-1, AC-2, AC-9 (with WP-C for the delegate-side keys), AC-6 prerequisites.

## Ambiguity warnings

| What was ambiguous | Resolution |
|---|---|
| Title when both `label` and task text are absent | **Founder decision (round 5): refuse the launch** with `ErrTitleRequired` before any write |
| Whether `ParentDurableKey` is kept as an alias | **Founder decision (round 5): removed in the same delivery**; every reader (15 files, by owner in ADR-091 D2) moves in the one integration |
| Whether a root chat has a lifecycle record | **Founder decision (round 9): yes, written by the launcher at its first delegation** |
| What the concurrency cap counts | **Founder decision (round 9): executing turns**, not sessions in a running state |
| Whether `delegate` returns before the child starts | **Founder decision (round 9, superseding round 6): it returns as soon as the record is written and dispatch has been requested; the child may already be running** |
| `max_parallel_agents = 0` | **not a change:** today's `EffectiveMaxParallelAgents` semantics (backstop) are reused; the earlier "refused" wording was withdrawn (R16) |

## Holdout evaluation scenarios (not for development)

1. Delegate from Jarvis to Jim to a worker; open Sessions; the worker's session is listed under Jim's with a readable title and Jarvis's workspace.
2. Kill the process while a worker is mid-turn; restart; the worker either resumes with its full history or its parent is told it failed — never a silent blank.
3. Set the concurrency key to 2; start three delegations; the third appears as "queued" in the side panel and starts when one finishes; Jim's turn never stalls.
4. Corrupt a child's lifecycle record by hand; wake it; nothing runs and the parent hears why.
5. Launch with a goal identical to a task's; the Judge's verdict format is identical.
6. Delegate under a creator with no workspace; the child has none and still works.
7. Run a child past 60 messages; its history in the UI is complete.

## Definition of done

1. *Code correct and tested:* every test in the TDD plan exists, fails before the change and passes after; scoped `go test` per package; `make verify-contracts` untouched by this WP.
2. *Reachable by a user/agent:* a delegation launched from the real UI produces a session with workspace and title visible in the sidebar; a launch at the cap shows "queued" in the side panel.
