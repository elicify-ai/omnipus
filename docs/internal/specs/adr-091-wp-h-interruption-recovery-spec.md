# ADR-091 WP-H — Interruption recovery

**Decision:** ADR-091 §3 D12 · **Criterion:** AC-14 · **Defect:** issue #857 ·
**UAT:** `docs/internal/testing/adr-091-uat-plan.md` lane I (issue #856)

> Build this on a fresh branch from `release`, after the main ADR-091 work has
> merged. It is a defect fix with a design decision behind it, not part of the
> original thirteen-lane delivery.

---

## 1. Why this exists

A steered session's turn currently decides two things by reading
`turnState.parentTurnState` — a pointer to the steering session's **in-memory**
turn:

| `file::symbol` | Reads it to decide |
|---|---|
| `pkg/agent/loop_provider_retry.go::shouldRetryDelegatedRateLimit` | whether to retry a provider rate limit |
| `pkg/agent/turn_exit.go::IsParentEnded` | whether to exit because the parent turn finished |

That field's only production writer was `pkg/agent/subturn.go:2057`
(`st.childTS.parentTurnState = st.parentTS`), deleted with the sub-turn mechanism in
`d5f5d7c82`. **Nothing assigns it today.** Verified:

```
grep -rnE "\.parentTurnState\s*=[^=]|parentTurnState:" --include="*.go" . | grep -v _test.go
# -> no output
```

So both gates evaluate `false` for every real delegated session, and have since the
deletion. Three tests still cover them and still pass, because each **assigns the
field by hand** in a struct literal — a state production no longer produces. This is
the exact false-green shape the suite is supposed to catch and did not.

D12 states the rule this violated: **a steered turn's decisions come from its durable
record, never from a pointer to another turn's in-memory state.** The in-memory link
cannot survive a restart, a re-entry or a wake — which is why D2 replaced it with a
durable edge in the first place.

## 2. Existing codebase context

| `file::symbol` | Role after this change |
|---|---|
| `pkg/agent/loop_provider_retry.go::shouldRetryDelegatedRateLimit` | **re-gate** on the durable edge |
| `pkg/agent/loop_provider_retry.go` retry loop (~L39) | unchanged in shape; becomes reachable |
| `pkg/agent/turn_exit.go::IsParentEnded` | **delete** |
| `pkg/agent/loop_run_turn.go` (~L1182) parent-ended branch | **delete** |
| `pkg/agent/turn.go::turnState.parentTurnState` | **delete** once both readers are gone |
| `pkg/session/lifecycle_edge.go::LifecycleRecord.SteeredBy` | the durable replacement signal |
| `pkg/agent/steer_completion.go::completeSteeredTurn` | where a permanent provider failure must terminalise and report |
| `pkg/providers::ClassifyError` / `FailoverRateLimit` | unchanged; still classifies transient vs permanent |

## 3. User stories

**H-1 — P0. A delegated worker survives a provider rate limit.**
As an operator running several delegations at once, when one worker's model call is
throttled, I want that worker to wait and retry rather than fail, so a busy moment
does not lose work I have already paid for.

*Independent test:* launch a steered session through the real launcher against a
provider that returns 429 once, then succeeds. The session completes.

**H-2 — P0. A permanently failing provider call does not strand a worker.**
As an operator, when a worker's provider is misconfigured or down, I want the worker
to fail visibly and tell its parent, so nothing waits forever on a dead call.

*Independent test:* same, but the provider always fails. The record reaches a terminal
state and the steering session is woken.

**H-3 — P1. A worker is not bound to my browser.**
As an operator, when my connection drops, I want the work to carry on and the result
to be there when I come back.

*Independent test:* no client-side change is required — this is a guard that no code
path terminates a steered session on transport close.

## 4. Behavioural requirements

| # | Requirement |
|---|---|
| FR-H-001 | The delegated retry gate MUST be satisfied by a session whose lifecycle record carries a non-nil `SteeredBy`, on **every** entry path — first launch, wake, follow-up, boot recovery. |
| FR-H-002 | The gate MUST NOT read `turnState.parentTurnState`, nor any other pointer to another turn's in-memory state. That field is deleted. |
| FR-H-003 | A transient provider failure (`ClassifyError` → `FailoverRateLimit`) inside a steered turn MUST be retried, bounded by the existing max-retry constant, with the existing backoff. |
| FR-H-004 | A permanent provider failure MUST terminalise the record and report upward through D3's single operation. The record MUST NOT be left `running`. |
| FR-H-005 | Exhausting the retry budget is a permanent failure and MUST follow FR-H-004. |
| FR-H-006 | `IsParentEnded`, its call site and the `parentTurnState` field MUST be deleted. A steered session's life is bounded by its own record, its `Stop` marker and D9's timeout — D8's cascade already handles a cancelled parent. |
| FR-H-007 | No code path may terminate, cancel or fail a steered session because a client transport closed. |
| FR-H-008 | A test MUST assert that a production path **assigns** `SteeredBy` on a launched steered session, so that deleting the writer fails the suite rather than silently disabling FR-H-001. |

## 5. BDD scenarios

```gherkin
Scenario: a throttled delegated worker retries and completes
  Given a steered session launched by the real launcher
    And its provider returns a rate-limit error on the first call and succeeds on the second
  When the session runs its turn
  Then the provider is called twice
    And the session reaches "completed"
    And its steering session receives the result
# Traces to: H-1, FR-H-001, FR-H-003, AC-14

Scenario: the retry gate is satisfied from the persisted record, not a handbuilt state
  Given a steered session launched by the real launcher
  When the turn evaluates whether a rate limit is retryable
  Then the decision is derived from the record's SteeredBy edge
    And the turn holds no pointer to the steering session's turn
# Traces to: FR-H-001, FR-H-002, AC-14
# NOTE: this is the scenario whose absence hid #857. It must construct NOTHING by hand.

Scenario: a permanently failing provider terminalises the worker and wakes its parent
  Given a steered session whose provider always fails with a non-retryable error
  When the session runs its turn
  Then the record reaches a terminal state
    And it is not left "running"
    And the steering session is woken exactly once
# Traces to: H-2, FR-H-004, AC-14

Scenario: exhausting the retry budget is a visible failure, not a hang
  Given a steered session whose provider returns a rate limit on every call
  When the retry budget is exhausted
  Then the record reaches a terminal state
    And the steering session is told
# Traces to: FR-H-005, AC-14

Scenario: a client disconnect does not cancel the work
  Given a running steered session
  When the operator's transport closes
  Then the session keeps running
    And its record is unchanged by the disconnect
    And its result is readable when the operator returns
# Traces to: H-3, FR-H-007, AC-14, UAT I2
```

## 6. Test data

| Case | Provider behaviour | Expected | Source |
|---|---|---|---|
| Happy | succeeds first call | completed, no retry | FR-H-003 |
| Transient ×1 | 429 then success | completed, 2 calls | FR-H-003 |
| Transient at budget | 429 exactly `delegatedRateLimitMaxRetries` times, then success | completed | boundary |
| Transient over budget | 429 every call | terminal + parent woken | FR-H-005 |
| Permanent | auth failure | terminal + parent woken, **no retry** | FR-H-004 |
| Not steered | ordinary root session, 429 | unchanged existing behaviour | regression |

The boundary pair (`at budget` / `over budget`) is mandatory: an off-by-one there is
the difference between a worker that recovers and one that fails.

## 7. Non-goals

- Changing retry behaviour for ordinary, non-delegated sessions.
- A new operator setting for retry counts — the existing constant stands until
  someone asks.
- Provider failover between models. `ClassifyError` already distinguishes the cases;
  this spec only decides what a *steered* turn does with the answer.
- Reviving the sub-turn mechanism in any form.

## 8. Definition of done

1. **Correct and tested.** FR-H-001…008 hold; every scenario above has a named test
   observed red before green; the boundary pair is covered; the FR-H-008 guard exists
   and fails if the writer is removed.
2. **Reachable by a user or agent.** A delegated worker throttled by a real provider
   completes instead of failing, and UAT scenario I3 — restored to the plan once this
   lands — passes when run by a person.

## 9. The trap this spec exists to avoid

The obvious implementation is to keep the gate and assign `parentTurnState` again
somewhere. **Do not.** That restores the exact coupling D2 removed: an in-memory link
that is absent on every entry path except the one that built it, so the bug returns
the moment a session is woken or recovered rather than launched.

Equally, do not "fix" the three existing tests by leaving their hand-assigned
literals in place. A test that constructs the authorising state cannot detect that
production stopped producing it — which is precisely how this survived a thirteen-lane
delivery and a seven-reviewer gate.
