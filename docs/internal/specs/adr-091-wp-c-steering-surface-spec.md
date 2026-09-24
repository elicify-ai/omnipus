# ADR-091 WP-C — Steering surface, the `delegate` front, wait-inline removal, completion

- **Decision record:** [ADR-091](../architecture/ADR-091-steered-sessions-replace-subagents.md) D4, D5, D6, D10 (list_jobs, seeds, prompts)
- **Landing order:** [adr-091-landing-order.md](adr-091-landing-order.md) — consumes I-1, I-2, I-3, I-5
- **Owner files:** landing order §3, row C
- **Status:** Draft rev 2 (consolidated after three grills; supersedes every earlier sentence of rev 1)

## Summary

`delegate` stays the verb agents know, but underneath it becomes a thin front: create a steered session through the injected launcher, ask for dispatch, and hand back a handle — at once, whether the child starts now or waits in the queue. Its steering actions — steer, respond, peek, inbox, follow-up, cancel, status — become capabilities of any steered session, usable by any ancestor and by the human, and available on sessions created by `create_task` too. Waiting inline for a child is removed everywhere it exists, including the places that only *advertise* it. A steered session with no goal is done when it gives a final answer and its subtree is quiet; one launched with a goal is judged like a task; the prompt tells the parent when a goal is worth setting.

## Existing codebase context

*Source inspection at `364290cb5`.*

### Symbols involved

| Symbol | Role today | Change |
|---|---|---|
| `pkg/tools/delegate.go` (actions `run`, `status`, `inbox`, `inbox_ack`, `steer`, `respond`, `cancel`, `follow_up`, `peek`) | the tool; holds `SubTurnSpawner` | holds the injected `steer.SessionLauncher` (I-2), `steer.UpwardDeliverer` (I-5) and `steer.Canceller` (I-6); `run` → `Launch` + `Dispatch` and reports **`DispatchResult`** (the authoritative `running` / `queued`); `steer` / `respond` publish bus events with `Principal` set after `verifyCallerOwnsSession`; `status` answers from the record and inbox, never from streaming (#614) |
| `pkg/tools/delegate.go` `Parameters` — `async`, `allow_blocking_question` ("with wait/async=false only") | wait-inline surface | **both removed** (the contract's field for `async` is `wait`; WP-E removes it from `DelegateRunAction.yaml`) |
| `pkg/tools/delegate_run.go::executeSync` / `executeAsync` / `persistLifecycle` | the two modes; `persistLifecycle` writes `ParentDurableKey` | `executeSync` **deleted**; `executeAsync` becomes the one run path calling I-2; `persistLifecycle` deleted (the launcher writes the record) |
| `pkg/tools/delegate_status.go`, `delegate_park.go`, `delegate_followup.go`, `delegate_run.go` | read `ParentDurableKey` directly (`Drain`, `AckDetailed`, `Peek`, the children `List`) | read the edge's steering session; same calls; the field is deleted (D2) |
| `pkg/tools/delegate.go::verifyCallerOwnsSession` | walks `ParentDurableKey` — ancestor authority | walks the edge; extended with the human principal (the authenticated gateway identity, passed down — never constructed by a tool) |
| `pkg/tools/delegate_followup.go::executeSteer` | rejects external-CLI sessions | kept (limit stated) |
| `pkg/tools/delegate_park.go`, `pkg/tools/message_parent.go::parkNeedsInput` | question parking | delivery via I-5 (WP-B); `prepareMessage` external rejection kept |
| `pkg/tools/task.go::buildTask`, `SetDelegationDenyChecker`, `SetMaxDelegationDepth`, `validateRequest` (requires criteria) | task front | `buildTask` sets the task's existing disk-only `OriginSessionID` **and a new disk-only `OriginCallID`** (the `create_task` tool-call id; `pkg/task/task.go` gains the field, owned here, never on the wire) so the launcher can read both at a delayed start; launcher call; task-only depth ceiling removed (D9); self-target exemption unified (D2) |
| `pkg/tools/run_task.go` (`SetStartTaskNow`) | starts a task | unchanged |
| `pkg/tools/list_jobs_sources.go::collectSubagentRows`, `collectTaskRows`; `list_jobs.go::ListJobsTool.collect` | `collectSubagentRows` filters on `LifecycleRecord.ParentAgentID` (tasks leave it empty) and reads **actionability and custom labels from the live delegate index**; the two collectors' rows are concatenated without de-duplication | three changes, no more: (1) exclude records whose `Origin.Kind` is `task` (the task collector shows those) before any result limit; (2) actionability from the record's lifecycle state (`running` / `needs_input` / `queued` → actionable; terminal → not) instead of the live index; (3) the label from the record's `Title` instead of a live resolver. The `ParentAgentID` filter stays (the launcher stamps it for both fronts). Result: a delegate-backed child appears once as a sub-agent row, a task-backed child once as a task row, and both survive a restart |
| `pkg/agent/loop_delegation.go::buildDelegationDenyChecker` (`selfAssignmentExempt`), `EdgeModeCategory` | the gate | `selfAssignmentExempt=true` for both fronts; `Await` case removed |
| `pkg/agent/loop_wire.go::registerSharedTools` | wires modes and checkers | await wiring removed |
| `pkg/agent/loop_env.go::wireDelegationInjectors` | expands `direct` → `Await`+`Background` for the prompt | `Await` removed |
| `pkg/agent/delegation_context.go::buildDelegationContext` | generated prompt advertising `async=false` | line removed; gains the one-sentence goal guidance (round 8) |
| `pkg/config/config.go::DelegationModeAwait` | the mode | **deleted**; load rejects it |
| `pkg/coreagent/seed.go::coreAgentDelegation` | seeds `Await` on Jim, Planner, Worker | removed from seeds |
| `pkg/agent/task_run_loop.go::noClaimSteeringPrompt`, `finishRunTurn` | claim protocol | only for judged work (D6); gains the completion disposition that maps each outcome to the I-5 row |
| `pkg/agent/task_assignee_readiness.go::TaskAssigneeCannotFinish` | rejects `goal_claim`-denied assignees | not applied to plain steered sessions |

### Impact assessment (manual)

| Symbol | Risk | Dependents |
|---|---|---|
| `delegate` tool contract | **HIGH** — every orchestrating prompt | seeds, skills, docs |
| `buildDelegationDenyChecker` settings | HIGH — authorization | both fronts |
| `task_run_loop.go` disposition | MEDIUM | Judge, plan engine |
| `DelegationModeAwait` removal | MEDIUM | config load, seeds, workspace edge migration (already `direct`/`task`) |
| `collectSubagentRows` filter | LOW | `list_jobs` callers |

## User stories and acceptance criteria

### US-1 — Delegate without waiting (P0)

*As an orchestrating agent, I want `delegate` to start a worker and return at once, and to be woken when it finishes, so that my turn is never blocked.*

1. **Given** an agent calls `delegate(action="run", …)`, **When** `Launch` and `Dispatch` have returned, **Then** the call returns a session handle within the turn — the child may already be running (founder decision, round 9).
2. **Given** an agent passes `async` or `allow_blocking_question`, **When** the call is validated, **Then** it is rejected with a named argument error.
3. **Given** the generated system prompt for an agent with delegation edges, **When** it is rendered, **Then** it contains no advertisement of synchronous delegation.
4. **Given** persisted config or seeds containing the await mode, **When** the process loads, **Then** the value is rejected with a named error (config) or absent (seeds).
5. **Given** the effective concurrency cap is reached, **When** `delegate(run)` is called, **Then** it returns at once with the session id and the **`DispatchResult`** — `state: queued`, the queue position — and a notice that `delegate(cancel)` drops it; the session starts when a slot frees (founder decision, round 8). The state comes from `Dispatch`, never from `Launch`.

### US-2 — Steer any session you are entitled to (P0)

*As an ancestor agent or the human operator, I want to steer, answer, inspect and stop a steered session, so that work can be redirected from wherever it is watched.*

1. **Given** a chain R → A → B → C, **When** R steers C, **Then** the message is queued for C and delivered in arrival order with any others.
2. **Given** the human operator opens C in the chat and writes, **When** C's turn is live, **Then** the text becomes a steering message for C (no new UI path); the principal is the authenticated gateway identity.
3. **Given** a sibling session or an unrelated root, **When** it attempts any action on C, **Then** it is rejected with a named authority error.
4. **Given** C is parked with a question, **When** B responds, **Then** C resumes with the answer; **When** the human answers from C's view, **Then** the same.
5. **Given** a session created by `create_task`, **When** its creator steers it, **Then** the same actions work.
6. **Given** an external command-line worker, **When** steer or a question is attempted, **Then** it is rejected with a named "not steerable" error and the limit is documented.

### US-3 — Self-delegation is allowed and bounded (P1)

1. **Given** agent A delegates to agent A, **When** the gate runs, **Then** it is permitted, a new session with A's profile is created, and depth counts it.
2. **Given** a chain that would exceed the depth ceiling, **When** launched, **Then** it is refused by depth, not by identity.

### US-4 — Done without ceremony; judged when asked (P0)

1. **Given** a steered session launched without a goal, **When** its turn ends with a non-empty final answer and no descendant is `queued` or `running`, **Then** it is `completed` and its parent receives a `handback` (`mode: final`) with the answer.
2. **Given** the same session ends with an empty answer, a parked question, an interruption, a timeout, or with a descendant still `queued` or `running`, **When** the turn ends, **Then** the outcome is persisted and delivered exactly per the I-5 table — **`failed` + `error empty_answer:`** (founder decision, round 10); `needs_input` + `question`; `cancelled` + `error interrupted:`; `timed_out` + `error timed_out:`; stays `running` with no entry until the subtree is quiet — never as done.
3. **Given** a parent that answered while a child still runs, **When** the last such child completes and wakes the parent, **Then** the parent's own `handback` is written then, with any parked descendants' questions in `open_questions`.
4. **Given** a steered session launched with a goal (criteria + DoD in `create_task`'s shape), **When** it claims, **Then** the Judge adjudicates as for a task and a `goal_status` entry (direction `session_to_parent`, condition `met` or `not_met`, `evidence` per criterion — the existing kind, extended by WP-E) reaches the parent.
5. **Given** an agent denied `goal_claim`, **When** it runs plain steered work, **Then** it can complete.
6. **Given** the rendered delegation prompt, **When** an agent reads it, **Then** it finds one sentence saying when to set a goal on a delegate (multi-step or must-verify work) and when to leave it off (quick lookup, single action), and that no goal is the default (founder decision, round 8).
7. **Given** a parent with an active goal delegating without `goal=`, **When** the child's model input is assembled, **Then** it contains neither the parent's goal nor its criteria (Q13).

### US-5 — Jobs are discoverable from the record, once (P1)

1. **Given** a steered session that survived a restart, **When** `list_jobs` runs, **Then** it appears once with correct parentage, label and actionability.
2. **Given** a task-backed session, **When** `list_jobs` runs, **Then** it appears in the task rows only — never also as a sub-agent row — and with a result limit of 1 exactly one correctly classified row is returned.
3. **Given** a restarted steered session, **When** `list_jobs` runs, **Then** its actionability comes from its lifecycle state and its label from its title — no live resolver is consulted.

### US-6 — `status` tells "quiet" from "cannot report" (P1)

*As an orchestrating agent, I want `delegate(status)` to tell me what a child is doing even when its provider does not stream, so I do not mistake a working child for a hung one.* (#614; founder decision, round 7.)

1. **Given** a running child whose provider streams no progress, **When** `status` is called, **Then** it returns the lifecycle state, the last status line and the time of the last inbox entry — from the record and the inbox.
2. **Given** a child that has sent nothing for 40 s, **When** `status` is called, **Then** the answer says "running, last message 40 s ago", not "no progress".
3. **Given** a child that has sent nothing yet, **When** `status` is called, **Then** the answer says "running, no message yet, started N s ago" — never a fabricated timestamp.
4. **Given** an external CLI child, **When** `status` is called, **Then** the same fields are returned.

### Edge cases

| Case | Expected |
|---|---|
| Two steering sources send at once | both delivered, arrival order |
| `respond` to a session that is not parked | named error, no state change |
| `follow_up` on an external session | new external run under the same edge; parentage, limits and cancel scope preserved |
| Goal with criteria the Judge cannot evaluate for this agent | same readiness rejection as `create_task` |
| `delegate` when the caller's own record is not runnable (I-8) | refused; nothing launched |
| `delegate(cancel)` on a `queued` session | record `cancelled`; it never starts; `subagent_state(cancelled)` written |

## Behavioral contract

- When `delegate(run)` is called, the system launches a steered session through the injected launcher and returns at once, reporting `queued` with position when the cap is reached.
- When any steering action is called, the system permits it for an ancestor or the authenticated human and rejects it for anyone else.
- When a plain steered session ends with a final answer and a quiet subtree, the system completes it; otherwise it persists and reports the exact outcome per the I-5 table.
- When a goal-bearing session claims, the system judges it exactly as a task.
- When an external worker is steered or asked, the system refuses with a named limit.
- When `status` is asked, the system answers from the record and the inbox.

## Explicit non-behaviors and safeguards

### Qualitative prohibitions

- The system must not block a parent turn on a child, because wait-inline is removed by founder decision and its argument surface must not survive as a hidden path.
- The system must not gate steering on the tool that created the session, because that would recreate the special case.
- The system must not apply the claim ceremony to goal-less work, because it costs a steering round-trip every time a worker forgets.
- The system must not advertise, in a generated prompt, any capability the tool no longer accepts.
- The system must not ban self-delegation, because recursion is bounded by depth and concurrency, not by identity (founder decision).
- The system must not import `pkg/agent` from `pkg/tools`, because that is the import cycle the injected interfaces exist to avoid.
- The system must not construct a human principal inside a tool, because only the gateway knows who is authenticated.

### Machine-verifiable constraints

Conventions: landing order §5 item 6 (production code only; exact counts; no-match exit is the pass for 0).

| Constraint | Exact check |
|---|---|
| No wait-inline | `grep -rn -e executeSync -e DelegationModeAwait -e allow_blocking_question pkg/ --include='*.go' --exclude='*_test.go'` → 0; `grep -rn 'async=false' pkg/agent/delegation_context.go` → 0; argument validation returns `invalid_argument: async` and `invalid_argument: allow_blocking_question` |
| Prompt clean, guidance present | rendered `buildDelegationContext` output contains no `async=false` and no "synchronously"; contains the goal-guidance sentence exactly once |
| Authority table | for each of 8 actions × {parent, grandparent, human, sibling, unrelated root}: allow, allow, allow, reject, reject |
| Order | two steers within 10 ms arrive in send order |
| Done rule | truth table over (answer non-empty, subtree quiet, parked, interrupted, timed out) yields exactly one persisted state and one I-5 message kind, validated against the generated contract |
| External limit | `steer`/question on an external session returns `not_steerable` |
| Jobs | after restart, `list_jobs` with limit 2 lists a delegate-backed session once (sub-agent row) and a task-backed session once (task row); with limit 1, exactly one correctly classified row; `grep -c 'liveIndex' pkg/tools/list_jobs_sources.go` → 0 after landing (or whichever symbol names the live resolver at implementation time — the implementer records the exact name) |
| Status | `status` on a child with no inbox entries returns state, "no message yet" and started-ago; with entries, the last text and its age |
| Cap | at the cap, `delegate(run)` returns within 50 ms with `state: queued` and a notice naming the limit, the position and `delegate(cancel)` |
| No agent import | the output of `go list -deps ./pkg/tools` contains no line ending in `omnipus/pkg/agent` (count 0) |

## Integration boundaries

| System | Contract | On failure |
|---|---|---|
| Launcher (WP-A) | `steer.SessionLauncher.Launch` then `Dispatch` | error returned to the agent |
| Upward delivery (WP-B) | `steer.UpwardDeliverer.Deliver` for completion, parked, verdict | undeliverable surfaced |
| Steering queue | `steering.go::enqueueSteeringMessage(scope, agentID, msg)` | existing behaviour |
| Judge | task adjudication path, unchanged | existing behaviour |
| Config load | rejects `await` | named error |
| Gateway | passes the authenticated principal into steering actions | unauthenticated → rejected |

## BDD scenarios

```gherkin
Feature: Delegate front and steering surface

  # Happy Path — Traces to: US-1 / AS-1
  Scenario: Delegate returns at once
    Given agent "jim" is in a turn
    When jim calls delegate(action="run", agent_id="worker", task="x")
    Then the call returns a session id as soon as launch and dispatch have returned
    And jim's turn is not blocked

  # Error Path — Traces to: US-1 / AS-2
  Scenario Outline: Removed arguments are rejected
    When an agent calls delegate with "<arg>"
    Then the tool rejects it with an invalid-argument error naming "<arg>"
    Examples:
      | arg                     |
      | async                   |
      | allow_blocking_question |

  # Alternate Path — Traces to: US-1 / AS-5
  Scenario: Delegate at the concurrency cap is queued, not blocked
    Given max_parallel_agents turns are executing
    When Jim calls delegate(run) for a worker
    Then the call returns at once with Dispatch's result: state "queued" and the queue position
    And the result tells Jim the limit is reached and that delegate(cancel) drops it
    When an executing turn ends
    Then the queued worker starts

  # Happy Path — Traces to: US-2 / AS-1, AS-3
  Scenario Outline: Authority per principal
    Given a chain R -> A -> B -> C and a sibling D under B and an unrelated root X
    When <principal> calls <action> on C
    Then the result is <result>
    Examples:
      | principal | action  | result   |
      | B         | steer   | allowed  |
      | R         | cancel  | allowed  |
      | human     | respond | allowed  |
      | D         | steer   | rejected |
      | X         | peek    | rejected |

  # Happy Path — Traces to: US-2 / AS-1, edge case
  Scenario: Two steering sources arrive in order
    Given R and the human both steer C within 10 ms
    Then C's steering queue holds both messages in arrival order

  # Happy Path — Traces to: US-2 / AS-2
  Scenario: The operator steers by writing into the open session
    Given the operator has C open while C's turn is live
    When the operator sends "focus on the checkout page"
    Then C's steering queue holds that message with the authenticated operator as principal

  # Error Path — Traces to: US-2 / AS-6
  Scenario: An external worker cannot be steered
    Given C is a Codex external worker
    When B calls steer on C
    Then the result is not_steerable

  # Alternate Path — Traces to: US-3 / AS-1
  Scenario: Self-delegation is permitted and counted
    When agent "worker" at depth 1 delegates to "worker"
    Then a new session with worker's profile exists at depth 2

  # Happy Path / Error Path — Traces to: US-4 / AS-1, AS-2
  Scenario Outline: Completion disposition
    Given a steered session without a goal
    When its turn ends with answer "<answer>", subtree "<subtree>", parked "<parked>"
    Then its persisted state is "<state>" and the parent's inbox gains "<entry>"
    Examples:
      | answer    | subtree | parked | state       | entry                       |
      | non-empty | quiet   | no     | completed   | handback(final, answer)     |
      | empty     | quiet   | no     | failed      | error(fatal, empty_answer:) |
      | non-empty | running | no     | running     | nothing yet                 |
      | non-empty | queued  | no     | running     | nothing yet                 |
      | any       | any     | yes    | needs_input | question                    |

  # Alternate Path — Traces to: US-4 / AS-3
  Scenario: The last child's completion completes the waiting parent
    Given B answered while C was still running and D is parked with a question
    When C completes and wakes B
    Then B's state becomes completed
    And A's inbox gains B's handback with D's question in open_questions

  # Happy Path — Traces to: US-4 / AS-4
  Scenario: A goal-bearing delegation is judged
    Given a delegation launched with criteria and a DoD
    When the worker claims "met"
    Then the Judge adjudicates and the parent receives a goal_status entry

  # Happy Path — Traces to: US-4 / AS-6
  Scenario: The prompt says when to set a goal
    When the delegation prompt is rendered for Jim
    Then it contains exactly one sentence about setting a goal for multi-step or must-verify work and leaving it off otherwise
    And it says no goal is the default

  # Happy Path — Traces to: US-4 / AS-7
  Scenario: The parent's goal is not in the child's input
    Given Jim's session has an active goal
    When Jim delegates without a goal
    Then the worker's assembled model input contains neither Jim's goal nor its criteria

  # Alternate Path — Traces to: US-5 / AS-1, AS-2, AS-3
  Scenario: Each session is listed once, from the record
    Given a delegate-backed worker and a task-backed worker under Jim, and a restart
    When list_jobs runs with a result limit of 2
    Then the delegate-backed worker appears once as a sub-agent row, actionable, labelled by its title
    And the task-backed worker appears once, in the task rows only
    When list_jobs runs with a result limit of 1
    Then exactly one correctly classified row is returned

  # Happy Path — Traces to: US-6 / AS-1, AS-2, AS-3
  Scenario Outline: Status answers from the record, not from streaming
    Given a worker on a provider that streams nothing
    And its last message_parent was "<last>"
    When Jim calls delegate(status)
    Then the answer says "running" and "<line>"
    Examples:
      | last        | line                            |
      | 40 s ago    | last status line, 40 s ago      |
      | never       | no message yet, started N s ago |
```

## TDD plan

Implementers load the `test-driven-development` skill first.

| Order | Test | Level | Traces to |
|---|---|---|---|
| 1 | `TestDelegateRun_ReturnsAtOnce` | Integration | US-1/AS-1 |
| 2 | `TestDelegate_RejectsRemovedArgs` | Unit | US-1/AS-2 |
| 3 | `TestDelegationContext_NoAwait_GoalGuidanceOnce` | Unit | US-1/AS-3, US-4/AS-6 |
| 4 | `TestConfig_RejectsAwaitMode`, `TestSeeds_NoAwait` | Unit | US-1/AS-4 |
| 5 | `TestDelegateRun_AtCap_QueuedWithNotice` | Integration | US-1/AS-5 |
| 6 | `TestAuthority_Matrix` | Integration | US-2/AS-1,3,5 |
| 7 | `TestSteering_ArrivalOrderTwoSources` | Integration | US-2/AS-1, edge (dataset C-3) |
| 8 | `TestHumanSteer_ViaInbound_AuthenticatedPrincipal` | Integration | US-2/AS-2 |
| 9 | `TestRespond_ResumesParked` | Integration | US-2/AS-4 |
| 10 | `TestExternal_NotSteerable` | Unit | US-2/AS-6 |
| 11 | `TestGate_SelfTargetAllowedBothFronts` | Unit | US-3 |
| 12 | `TestCompletion_Disposition_PersistedAndValidated` | Integration (table) | US-4/AS-1,2 — writes the record and validates the entry against the generated contract |
| 13 | `TestCompletion_LastChildCompletesWaitingParent` | Integration | US-4/AS-3 |
| 14 | `TestGoalDelegation_Judged` | Integration | US-4/AS-4 |
| 15 | `TestPlainWork_GoalClaimDenied_Completes` | Integration | US-4/AS-5 |
| 16 | `TestGoalDelegation_ParentGoalAbsentFromChildInput` | Integration | US-4/AS-7 — asserts on the assembled model input |
| 17 | `TestListJobs_OneRowPerSession_AfterRestart` | Integration | US-5 — under a result limit |
| 18 | `TestDelegateStatus_FromRecordNotStreaming` | Integration | US-6 — includes no-message-yet |
| 19 | `TestDelegateCancel_QueuedNeverStarts` | Integration | edge |
| 20 | `TestTools_NoAgentImport` | Unit (static) | prohibition |
| 21 | `TestSteer_PublishesVerifiedPrincipal` | Unit | FR-C-016 — the event carries the principal `verifyCallerOwnsSession` established; a tool call without a caller session publishes nothing |
| 22 | `TestBuildTask_PersistsOriginCallID` | Unit | FR-C-005 — restart before scheduled start; the launcher reads it |

### Test datasets

| ID | Input | Expected | Traces to |
|---|---|---|---|
| C-1 | `async` ∈ {absent, true, false}; `allow_blocking_question` ∈ {absent, true} | ok / reject / reject / ok / reject | US-1/AS-2 |
| C-2 | 8 actions × 5 principals | matrix | US-2 |
| C-3 | steers at t, t+1 ms, t+10 ms from two sources | delivered in order | US-2 |
| C-4 | answer len 0 / 1 / 10 kB; subtree quiet / running / queued; parked | disposition table; entry validates | US-4 |
| C-5 | depth 1 / ceiling / ceiling+1 self-delegation | ok / ok / refuse | US-3 |
| C-6 | goal: 0 criteria / valid / unevaluable for agent | reject / judged / readiness reject | US-4/AS-4 |
| C-7 | `list_jobs` limit 1 / 2 / 10 with one delegate child + one task child | 1 correctly classified row / each once / each once | US-5 |
| C-8 | last inbox entry: none / 40 s / 0 s | "no message yet" / "40 s ago" / "just now" | US-6 |

### Regression requirements

Preserved: `verifyCallerOwnsSession` ancestor semantics; parked/respond lifecycle transitions; `list_jobs` row shape (`list_jobs_row.go`) and label resolution; `collectTaskRows`. Updated: any test asserting `async=false` or `executeSync`; seeds tests listing `Await`; tests keyed on `ParentDurableKey` in `delegate*_test.go` (WP-G classification: update). Deleted: sync-delegation UAT fixtures (WP-G classification: delete).

## Functional requirements

| ID | Requirement |
|---|---|
| FR-C-001 | `delegate(run)` MUST launch and dispatch through the injected `steer.SessionLauncher`, MUST report `Dispatch`'s result as the session's state, and MUST return as soon as both calls return; it MUST NOT block and MUST NOT wait for the child to start or finish. |
| FR-C-002 | The `async` and `allow_blocking_question` arguments, `DelegationModeAwait`, its seeds, gate wiring, mode conversion, `executeSync`, `persistLifecycle` and the prompt advertisement MUST be removed; persisted `await` MUST be rejected at load. |
| FR-C-003 | Every steering action MUST be permitted for any ancestor and the authenticated human operator (principal supplied by the gateway) and rejected for all others. |
| FR-C-004 | Steering messages from multiple sources MUST be delivered in arrival order. |
| FR-C-005 | Sessions created by `create_task` MUST expose the same steering actions; `buildTask` MUST record the creating session in `task.Task.OriginSessionID` and the creating tool call in the new disk-only `task.Task.OriginCallID`. |
| FR-C-006 | External command-line sessions MUST reject steer and questions with a named error. |
| FR-C-007 | Self-target launches MUST be permitted for both fronts and bounded only by depth and concurrency. |
| FR-C-008 | A goal-less steered session MUST complete only with a non-empty final answer and a quiet subtree (no descendant `queued` or `running`); an empty answer MUST be persisted `failed` with an `error` `empty_answer:`; every outcome MUST be persisted and delivered exactly per the I-5 table; a waiting parent's `handback` MUST be written when its last running descendant completes, with parked descendants' questions in `open_questions`. |
| FR-C-009 | A goal-bearing steered session MUST be adjudicated by the Judge exactly as a task and its verdict delivered upward as the extended `goal_status` kind (direction `session_to_parent`, `met` / `not_met`, `evidence`). |
| FR-C-010 | `collectSubagentRows` MUST exclude records whose `Origin.Kind` is `task` before any result limit, MUST take actionability from the record's state and the label from its title, and MUST NOT read the live delegate index; every session appears exactly once. |
| FR-C-011 | `delegate(status)` MUST answer from the lifecycle record and the inbox (state, last status line, age of the last entry, or "no message yet" with started-ago) and MUST NOT depend on streaming progress. |
| FR-C-012 | At the concurrency cap `delegate(run)` MUST return `Dispatch`'s `queued` result with a notice naming the limit, the queue position and that `delegate(cancel)` drops it. |
| FR-C-013 | A steered session's model input MUST NOT contain the parent's goal or goal context; proven on the actual assembled model input. |
| FR-C-014 | The delegation prompt MUST contain exactly one sentence of guidance on when to set a goal on a delegate — multi-step work or work the parent must verify: set one; a quick lookup or a single action: leave it off — and MUST state that no goal is the default. |
| FR-C-015 | `pkg/tools` MUST NOT import `pkg/agent`; it consumes `pkg/steer` only. |
| FR-C-016 | Every bus event a steering action publishes MUST carry the `Principal` established by `verifyCallerOwnsSession` (or the gateway's authenticated human); the tool MUST NOT construct a human principal. |

## Success criteria

| ID | Criterion |
|---|---|
| SC-C-1 | 0 references to the deletion-manifest symbols in production code after landing. |
| SC-C-2 | Authority matrix: 40 of 40 cells as specified. |
| SC-C-3 | Completion table: 100% of rows yield exactly one persisted state and one contract-valid entry. |
| SC-C-4 | 0 `delegate(run)` calls take longer than 50 ms to return at the cap across 100 runs. |

## Traceability

| Requirement | Story | Scenarios | Tests |
|---|---|---|---|
| FR-C-001 | US-1 | returns at once | 1 |
| FR-C-002 | US-1 | removed arguments | 2, 3, 4 |
| FR-C-003 | US-2 | authority outline; human steer | 6, 8 |
| FR-C-004 | US-2 | two sources | 7 |
| FR-C-005 | US-2 | authority outline (task-created) | 6 |
| FR-C-006 | US-2 | external worker | 10 |
| FR-C-007 | US-3 | self-delegation | 11 |
| FR-C-008 | US-4 | disposition outline; last child | 12, 13, 15 |
| FR-C-009 | US-4 | judged | 14 |
| FR-C-010 | US-5 | listed once | 17 |
| FR-C-011 | US-6 | status outline | 18 |
| FR-C-012 | US-1 | at the cap | 5, 19 |
| FR-C-013 | US-4 | parent's goal absent | 16 |
| FR-C-014 | US-4 | prompt guidance | 3 |
| FR-C-015 | — | static | 20 |
| FR-C-016 | US-2 | authority outline | 21 |
| FR-C-005 (call id) | US-5 | listed once after restart | 22, 17 |

ADR ACs covered: AC-4, AC-5, AC-6, AC-10 (list_jobs, manifest), part of AC-9.

## Ambiguity warnings

| What was ambiguous | Resolution |
|---|---|
| When `delegate(run)` returns | **Founder decision (round 9, superseding round 6):** as soon as launch and dispatch have returned; the child may already be running |
| Human principal identity for the authority check | the authenticated gateway identity, passed into the action by the gateway; a tool never constructs it (accepted, founder Q4) |
| When to set a goal | **Founder decision (round 8):** one sentence of guidance in the prompt; no goal is the default |

## Holdout evaluation scenarios (not for development)

1. Ask Jim to delegate three things; Jim's turn ends at once and three sessions appear.
2. Type into a running worker's session; the worker visibly changes course.
3. As an unrelated agent, try to steer that worker: refused with a clear reason.
4. Let a worker finish plain work: Jim is told "done" with the answer, no Judge involved.
5. Delegate with acceptance criteria: the Judge's verdict shows up for Jim.
6. Delegate to a Codex worker and try to steer it: a clear "cannot be steered" message.
7. Set the cap to 1 and delegate twice from Jim: the second shows "queued" and starts after the first.
8. Ask Jim for `status` on a worker running on a non-streaming provider: "running, last message N s ago".

## Definition of done

1. *Code correct and tested:* TDD plan green per package; deletion-manifest grep at 0.
2. *Reachable:* holdout 1, 2, 4, 5, 7 and 8 executed in the real UI.
