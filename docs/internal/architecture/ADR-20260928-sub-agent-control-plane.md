# ADR-20260928 — The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume

- **Status:** Proposed — awaiting its one `grill-spec` (ADR mode) round. **Build gate:** implementation starts only after the #984 follow-up (#1000) and the Q2=B branch have landed on `release/v0.1.1` (founder, 2026-09-28).
- **Date:** 2026-09-28
- **Deciders:** Daniel Piatkowski (founder — the rulings listed under "Founder rulings encoded"); architect (this draft).
- **ID:** minted with `scripts/new-adr-id.sh "Sub-agent control plane"` → `ADR-20260928-sub-agent-control-plane` (exit 0), the date-and-title scheme of PR #996.
- **Evidence baseline:** `origin/release/v0.1.1` @ `da8579715` for every symbol below unless marked **[Q2B]** (the branch is cut from `ee7640c90`; `git diff da8579715 ee7640c90` touches none of the cited `pkg/`, `contracts/` or spec files — three e2e-only commits), which means `origin/feat/q2b-on-984` @ `a166d2203` (not yet landed). GitNexus was not used; every symbol was located by `grep`/`git grep` and read in this task.
- **Amends:** [ADR-093 — An open conversation must keep the ability to delegate](./ADR-093-open-conversation-must-keep-delegation.md) D4 (the `steer`-revives-a-finished-child clause — see D3 below); [ADR-091 — A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md) §7 (restart recovery of steered sessions — see D8) and D5 (the action list gains `stop`, `redirect`, `escalate`).
- **Related:** issue #1011 (defects D1–D5, proposals F1–F4); #1000 (queue ruling); the fix-890 squad's Q2=B design note (`coordination/logs/fix890-opus/arch-q2-design.md`, local to the founder's machine, not in the repo); the release-tip analysis of #1011 (`coordination/logs/fix890-opus/issue-1011.analysis.md`, same).

## Scope

| Item from #1011 | In this ADR? |
|---|---|
| D1 — `artifact` messages rejected | **No.** Being fixed separately (`pkg/tools/message_parent.go::outcomeForKind` maps `artifact` to the checkpoint outcome). |
| D2 — owner-only questions cannot be answered | **Yes** — D1 below (the relay). |
| D3 — oversized child message hangs the child | **No.** The analysis finds both caps already enforced (`pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.applyQuarantineAndHooks`, `pkg/session/message_inbox.go::MessageInboxStore.Append`); verification belongs to qa-lead. |
| D4 — `steer` on a finished child revives it | **Yes** — D3. |
| D5 — a steer is delivered but not honoured | **Yes** — D2, D4, D5 (enforced verbs plus receipts; a steer itself stays advisory). |
| F1 — `stop`, `redirect`, chat commands | **Yes** — D2 (verbs), Q1 (chat command names). |
| F2 — delivery receipts | **Yes** — D4. |
| F3 — ordering, precedence, resource accounting | **Yes** — D5, D6. |
| F4 — rollback | **Out of scope, recorded as such** (D10). |

## Context

### What exists today (verified)

| Area | Today | Where |
|---|---|---|
| Parent-facing verbs | `run, status, inbox, inbox_ack, steer, respond, cancel, follow_up, peek` | `pkg/tools/delegate.go` (the `action` enum in the tool schema) |
| Who may act | Any ancestor, and the human operator (ADR-091 D5); authority is a walk up the steering chain | `pkg/tools/delegate.go::verifyCallerPrincipal`, `::verifyCallerOwnsSession` |
| `steer` | Queued in an **in-memory** per-session queue, drained at the child's next tool boundary; advisory | `pkg/tools/delegate_followup.go::executeSteer`; `pkg/agent/steering.go::steeringQueue` |
| `steer` on a finished or Stop-marked child | **Revives it** as a new generation and redispatches it, returned as a success | `executeSteer` (the `rec.Terminal() \|\| rec.Stopped()` branch) → `pkg/agent/steering.go::ReviveStoppedSession` → `pkg/agent/steer_cancel.go::SteerCanceller.Revive`. This is ADR-093 D4 ("that refusal becomes revive-and-redispatch") |
| `follow_up` | Only on a **terminal** record; mints the next generation | `pkg/tools/delegate_followup.go::executeFollowUp` (`!rec.Terminal()` → refuse) |
| `cancel` | Stamps a durable Stop marker on the child and every reachable non-terminal descendant, then interrupts live turns (hard) or lets them stop cooperatively (soft) | `pkg/tools/delegate_run.go::executeCancel`; `pkg/agent/steer_cancel.go::SteerCanceller.CancelSubtree` / `::StopSubtree`; `pkg/agent/steer_delegate_cancel.go::cancelDelegatedSubtree` |
| Steering queue limits | Queue length `MaxQueueSize = 10` per scope (a full queue refuses with "steering queue is full"); per-target rate 6/min and body 16 KiB | `pkg/agent/steering.go::MaxQueueSize`, `::pushItemScope`; `pkg/tools/delegate_followup.go::checkSteerCaps`; `pkg/session/message_inbox.go::DefaultSteerRatePerMinute`, `::DefaultSteerBodyBytes` |
| Receipt | One kind only: `steering_receipt {correlation_id, applied_at}` on a `subagent_state` frame, stamped **when the steer is injected** into the child's model context | `pkg/agent/steer_frames.go::deliverSteeringReceiptsForInjection`; `contracts/components/schemas/SubagentStateFrame.yaml` (`steering_receipt`) |
| Owner-only questions | A parent's `respond` to an `owner_required` question is refused; an unverifiable question is refused too (fail-closed) | `pkg/tools/delegate_park.go::verifyQuestionAuthority`; requirement R§8.2 / FR-132 in `docs/internal/specs/unified-goal-plan-subagent-spec.md` |
| The spec's own escalation story | FR-132: an `owner_required` question "MUST escalate to the owner terminus"; FR-134: "the human MUST answer in normal chat … correlation routing is the parent's job"; FR-135 / R§8.7: an opt-in one-traversal shortcut, default off | same spec. `grep question_escalation pkg` finds nothing — FR-135 is not built |
| Lifecycle states | Eight: `queued, running, needs_input, paused, completed, failed, cancelled, timed_out`. `paused` today means cancel-soft grace or a plan owner awaiting supervision | `pkg/session/lifecycle.go` (the `Lifecycle*` constants and the `LifecyclePaused` comment) |
| Turn end after an interrupt | A cancelled turn is mapped to terminal `cancelled` with `interrupted: the session was cancelled` and reported upward | `pkg/agent/steer_completion.go::completionDisposition` |
| Lifetime limit | Deadline = `CreatedAt + Limits.TimeoutSeconds`, wall clock, across re-entries | `pkg/agent/steer_launcher.go::steeredTurnRunContext` |
| Restart | Every in-flight steered session (not parked, no current Stop) gets an `interrupted` error delivered upward and is written `failed(interrupted)`; its session goal is ended | `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered`, `::failInterrupted`; also `PlanEngine.bootSweep` → `::sweepToFailedInterrupted` for every non-terminal non-standing record |
| Boot sweep and `paused` | Only a `paused` **plan owner** awaiting supervision is exempt; any other `paused` record is swept to `failed(interrupted)` | `pkg/agent/boot_sweep.go::PlanEngine.bootSweep` (exemption (b)) |
| Chat commands | `/cancel` only. Its definition states "No Aliases per FR-5 — /stop, /abort, /kill and any other alias are explicitly forbidden". `stop` is also one of the words that clears a goal | `pkg/commands/cmd_cancel.go::cancelCommand`; `docs/internal/specs/cancel-cross-channel-spec.md` decision 12; `pkg/commands/cmd_goal.go::GoalClearAliases` |
| "Whole job done" (Q2=B) **[Q2B]** | A child's `met` claim is held while `hasRunningOrQueuedDescendant` is true; the last descendant's terminal write triggers one re-evaluation. The walk counts `queued` and live `running` only — `paused` and `needs_input` count as quiet | `pkg/agent/steer_completion.go::hasRunningOrQueuedDescendant`, `pkg/agent/goal_child_completion.go::goalCompletionFenceActive`, `::resumeDeferredGoalAfterDescendantTerminal` |

### What is wrong, in one line each

1. **An owner-only question has no path to the owner.** The spec says it must escalate (FR-132) and that routing is the parent's job (FR-134), but no verb escalates, and the owner's answer cannot come back down: at the root, the root agent's `respond` is refused by the same check, because the check cannot tell a relayed human answer from a parent inventing one.
2. **`steer` is the only way to interrupt, and it is advisory.** There is no way to make a child stop now, or stop and do something else, short of `cancel`, which ends the child and its whole subtree.
3. **`steer` on a finished child silently starts new work** (#1011 D4), which a cheap, fire-and-forget call should not do.
4. **A parent cannot tell "not yet seen" from "seen and ignored".** The only receipt is stamped at injection, the queue is in memory (lost on restart) and refuses when full.
5. **A restart throws delegated work away.** Every in-flight child becomes `failed(interrupted)`, its goal ends, and each level is woken separately.

## Founder rulings encoded (not re-decided here)

| ID | Ruling (2026-09-28) | Lands in |
|---|---|---|
| R-D2 | Owner-only questions still end with the human owner, but the parent **cascades** them: it receives the child's question and decides to redirect it upward, the way a team lead relays to its founder — the runtime does not simply refuse the parent | D1 |
| R-D4 | `steer` = live children only. On a finished child it returns a clear "already finished — use follow_up" result and starts nothing. This amends ADR-093 D4 | D3 |
| R-F1 | `stop`: non-terminal, pauses the current turn, child stays resumable, **no cascade**. `redirect`: atomic stop + new instruction, **no cascade** | D2 |
| R-F2 | Delivery receipts `queued / delivered / applied / superseded` | D4 |
| R-F3 | Latest control wins; a stopped session is idle but alive (no compute, no slot, deadline paused) | D5, D6 |
| R-C | `cancel` stays terminal and cascades **down only**; cancelling a child never cancels the parent's goal — the user's tool for that is `/goal clear` (the brief says "/goals clear"; the command in code is `goal`, `pkg/commands/cmd_goal.go`) | D7 |
| R-Q2 | Q2=B ("one message upward when the whole job is done") holds; a stopped/paused child counts as **unfinished** for the parent's re-check | D6 |
| R-Q5 | After a restart, interrupted children are not thrown away; the top-level parent is told and can resume; a resumed child resumes its own interrupted workers | D8 |
| R-1000 | Done/wake messages are never refused; the ordinary steer queue cap is 200 | D5 |
| R-F4 | Rollback is out of scope | D10 |

## Decision

### Vocabulary (used in every section below)

| Word | Meaning | State on the record |
|---|---|---|
| **live** | the child has a turn executing, or is `queued` to run, or is `running` waiting on its own descendants | `queued`, `running` (no current Stop marker) |
| **paused** | stopped by `stop`, or interrupted by a restart; idle but alive; same generation | `paused` + a new `pause` note (D2) |
| **parked** | asked a question and waits for the answer | `needs_input` |
| **finished** | terminal, or carrying a Stop marker for its current generation (cancelled, not yet written terminal) | `completed, failed, cancelled, timed_out`, or `rec.Stopped()` (`pkg/session/lifecycle_edge.go::LifecycleRecord.Stopped`) |

### D1 — Owner-only questions: the parent relays up, the owner's own words come back down

**The rule R§8.2 protects stays exactly as strong:** no agent at any level can author the answer to an `owner_required` question. What changes is that the question now has a way up and the answer a way down.

**1. The parent sees the question and chooses.** A child's `owner_required` question arrives in the parent's inbox as today (wake-eligible, `pkg/agent/async_notifier.go::wakeableSessionMessageKinds`). The parent has three legal moves:

| Move | Verb | Effect |
|---|---|---|
| Relay it upward | new `delegate(action="escalate", session_id, correlation_id, note?)` | Runtime records the relay and forwards the question one hop up (step 2) |
| Make it moot | `redirect` (D2) or `cancel` (D7) | The child's question is closed as `superseded` (D4) |
| Try to answer it | `respond` | **Not an error-shaped refusal any more:** returns "This question needs the owner. Use action=escalate to pass it up, or redirect/cancel the child." and changes nothing |

A parent may also `escalate` a `self_ok` question it does not want to answer. `self_ok` answering is unchanged.

**2. Relay one hop at a time.** `escalate` appends a `question` to the relaying parent's own upward inbox (its steering session's inbox), with:

- the child's text **verbatim** (the parent's optional `note` travels in a separate, labelled field — never merged into the question);
- `authority: owner_required` (a relay can never downgrade);
- `relay_of: {session_id, correlation_id}` — the question one hop below;
- `origin: {session_id, correlation_id}` — the child that first asked, copied unchanged at every hop.

The relaying parent does **not** park: a team lead that passes a question to its founder keeps working on other things. Only the original asker is parked. At each level the next parent makes the same three-way choice. This is the spec's strict one-hop default (R§8.7 "Default (no opt-in) = strict one-hop (each parent forwards manually)"); the opt-in shortcut FR-135 stays unbuilt and is not needed for this ADR.

**3. The owner terminus.** The hop stops at the first session with a human owner: a root session (no `SteeredBy` edge, `pkg/session/lifecycle_edge.go::SteeredBy`) in which a person converses. There the root agent puts the question to the person in normal chat (FR-134 — no reply card).

**4. The answer is the owner's own message.** The owner answers in one of two ways:

| Path | How | Runtime check |
|---|---|---|
| In the root chat (normal) | The person replies; the root agent calls `delegate(action="respond", session_id=<its direct child>, correlation_id=<relayed id>, owner_message_id=<id of the person's message>, note?)` | The message exists in the root session's transcript, came from a person (ADR-093 D4's predicate: channel not `system`, no steer-wake metadata), and arrived **after** the relayed question reached the root inbox. The answer delivered downward is **that message's text, copied by the runtime** — not text the agent typed. The agent's `note` travels labelled as the relay's note. |
| Directly in the child | The person opens the child session and writes (ADR-091 D5: "the human operator may steer directly"); a human principal's `respond` in the parked session | Principal is the authenticated human (`pkg/tools/delegate.go::verifyCallerPrincipal`) |

A `respond` to an `owner_required` question that names no valid `owner_message_id` and is not from a human principal is refused, as today — that is the R§8.2 guard, now with a way to satisfy it.

**5. Down in one traversal.** The runtime follows the `relay_of` chain from the root to `origin` and resumes the original parked child through today's native resume (`pkg/tools/delegate_park.go::resumeNative`). The intermediate parents do **not** each run a turn to pass the answer down; each receives a non-waking info entry ("the owner answered the question you relayed") so its transcript stays complete. Each relay record is closed with receipt `applied` (D4).

**6. Several levels — worked example.**

```
Owner (person) ─ root chat R
                  └─ A (team lead)
                       └─ B (worker) asks owner_required Q, parks (needs_input)
1. B → A inbox: question Q (corr q1)                        B parked
2. A: escalate(B, q1) → R inbox: question Q, relay_of=(B,q1), origin=(B,q1)
3. R agent asks the person in chat; person replies (message m7)
4. R: respond(A, q1', owner_message_id=m7)
5. runtime: verify m7 → copy m7's text → follow relay_of → resume B with it
   A gets an info entry; relay records q1', q1 → applied
```

**7. Staleness.** If the asker is no longer parked on that question when the answer arrives (it was redirected, cancelled or resumed another way), the answer is not delivered; the respond result says so and the relay is marked `superseded`. The parked child's 24-hour park limit (`pkg/session/message_inbox.go` default `needs_input_ttl`, `pkg/tools/message_parent.go::SetNeedsInputTTL`) is unchanged by relaying.

### D2 — `stop` and `redirect`: runtime-enforced, non-terminal, no cascade

**`stop`** — `delegate(action="stop", session_id)`.

| Child is | Effect |
|---|---|
| live, turn executing | The runtime interrupts the turn (the same cooperative-then-hard interrupt as `cancel`, scoped to this session only — `pkg/agent/steering.go::Interrupt` / `::InterruptSessionHard` with a self-only scope) with a **distinct pause cause**. `completionDisposition` maps that cause to `paused`, **not** to `cancelled`: no terminal write, no upward `handback`/`error`, no goal end, same generation. The partial turn stays in the child's transcript. |
| live, `queued` | Moves to `paused`; it is not admitted until resumed; it frees its queue position |
| live, `running` but waiting on its own descendants (no turn executing) | Moves to `paused`; later upward messages from its descendants are stored, not woken on (D5) |
| paused | Idempotent success: "already paused" |
| parked | Nothing changes: "waiting for an answer — answer, redirect or cancel it" |
| finished | "Already finished — use follow_up" (same rule as D3); starts nothing |

The record gains a `pause` note: `{at, by (principal), reason: stopped | interrupted_by_restart, seq}` and an accumulated `paused_for` duration (D6). A stop reaches **only** the named session: its descendants keep running, and its background shells are not killed (they are the child's own resources, like its children — the parent can `cancel` if it wants them gone).

**`redirect`** — `delegate(action="redirect", session_id, text)`: one operation under the child's record lock. If a turn is executing it is interrupted with the pause cause exactly as `stop`; then a new turn starts on the **same generation** with `text` as the newest instruction. Because both halves run under one lock and one sequence number, no steer can land between them (#1011 F1: "not stop+steer as two racy messages"). `redirect` on a paused child resumes it with the instruction. On a parked child it closes the open question as `superseded` and resumes with the instruction. On a finished child: "already finished — use follow_up".

**Resuming a paused child.** `redirect` (with a new instruction) or `follow_up`. `follow_up` widens from "terminal only" (`executeFollowUp`) to "not live": on a **paused** child it resumes the same generation; on a **finished** child it keeps ADR-093 D4's revive-as-next-generation. A human message into a paused child's session also resumes it (ADR-091 D5: a person writing to a session is steering it). Nothing automatic resumes a paused child except D8's restart rule.

**Who may call them.** The same principals as every other action: any ancestor and the human operator (`verifyCallerPrincipal`). External command-line children (`rec.Is3P`) cannot be paused mid-turn — they never drain the steering queue (`executeSteer`'s `Is3P` branch); `stop`/`redirect` on them return a named `not_steerable` result pointing to `cancel` / `follow_up`, exactly like `steer` today.

### D3 — `steer` is for live children only (amends ADR-093 D4)

**Amendment, stated explicitly:** ADR-093 D4, paragraph "Children: a follow-up to a terminal child continues it", made **`follow_up`/`steer`** on a terminal child revive-and-redispatch. From this ADR on, that holds for **`follow_up` only**. `steer` on a finished child (terminal or current Stop marker) returns

> Session <id> has already finished — nothing was started. Use action="follow_up" to give it new work.

as a **non-error** result (same reasoning as `executeCancel`'s "already terminal — no action needed", which deliberately is not `IsError` to avoid retry loops), and starts nothing: no revival, no generation, no dispatch. F890-1 ("sessions are always resumable") is untouched — `follow_up` remains the resume path, and D2 widens it to paused children.

`steer` on a **paused** child is accepted as `queued` and delivered when the child resumes (F3: "addressable until cancelled/resumed"); it does not resume it. `steer` on a parked child keeps today's behaviour.

Affected code: `executeSteer`'s `rec.Terminal() || rec.Stopped()` branch stops calling `ReviveStoppedSession`. `ReviveStoppedSession`'s child-path caller becomes `follow_up` only.

### D4 — Every control gets a durable receipt

**Controls** are `steer`, `stop`, `redirect`, `respond`, `escalate`, `follow_up`, `cancel`. Each accepted control gets a per-child **sequence number** (`seq`, monotonic per session, assigned under the child's lifecycle lock) and a line in a new **per-session control ledger** (append-only JSONL beside the lifecycle record, written with the store's atomic-append rules — file-based storage per root `CLAUDE.md`). The ledger replaces the in-memory queue as the source of truth; the in-memory queue becomes a cache rebuilt from the ledger, so a queued steer survives a restart.

| State | Meaning — deterministic, written by the runtime |
|---|---|
| `queued` | Accepted and durable; the child has not seen it |
| `delivered` | Put in front of the child's model (steer/respond: injected at the tool boundary or turn start — today's `applied_at` moment) |
| `applied` | The runtime **enforced** the effect: stop → turn ended and record `paused`; redirect → new turn started with the instruction; cancel → subtree cascade finished and the record is terminal; respond → parked turn resumed; escalate → relay closed by an answer; follow_up → turn dispatched |
| `superseded` | Replaced before delivery by a newer control (D5), or made moot (a relayed question whose asker moved on) |

`steer` has no `applied` state: whether the model obeyed is best-effort and observable (through `peek`), never claimed by the runtime (#1011 F2: "Compliance stays best-effort (LLM) and observable; delivery is deterministic"). This is the answer to #1011 D5: a steer stays advisory by design; a parent that needs the child to stop **now** uses `stop` or `redirect`, which are enforced and receipted `applied`.

**Where a parent sees receipts.** (1) The tool result of the control call carries `seq`, `control_id` (today's `correlation_id`) and the state at return. (2) `status` and `peek` (`pkg/tools/delegate_status.go`) list the child's recent controls with their current state. (3) The `subagent_state` frame's receipt object carries every transition, so the side panel shows it too.

**Wire shape (architect decides the shape; backend-lead edits `contracts/` and regenerates):**

| Schema | Change |
|---|---|
| new `ControlReceipt` | `{seq: int64, control_id: string, verb: enum[steer, stop, redirect, respond, escalate, follow_up, cancel], state: enum[queued, delivered, applied, superseded], accepted_at, delivered_at?, applied_at?, superseded_at?, superseded_by_seq?, reason?}` |
| `SubagentStateFrame` | `steering_receipt` is **replaced** by `control_receipt: ControlReceipt` (no back-compat shim — the founder's 2026-09-15 greenfield ruling "no migrations, no upgrade backfills", as relayed in the team's working notes; the grill should confirm it covers wire fields) |
| new `DelegateStopAction`, `DelegateRedirectAction`, `DelegateEscalateAction` | in the style of `contracts/components/schemas/DelegateSteerAction.yaml` |
| `SessionMessageQuestion`, `SessionMessageDecisionRequest` | optional `relay_of {session_id, correlation_id}`, `origin {session_id, correlation_id}`, `relay_note` |
| `SessionMessageRespond` | `answer_source: enum[parent, owner]`, `owner_message_id?` |
| lifecycle record (where the SPA reads it) | `pause {at, by, reason, seq}`, `paused_for_seconds` |

### D5 — Ordering: the latest control wins

Controls on one child are ordered by `seq`. Precedence when a newer control arrives while older ones are still `queued`:

| Newer control | Older pending `steer` | Older pending `redirect` | Older pending `stop` |
|---|---|---|---|
| `steer` | both delivered, in order | kept; steer queued behind it | kept; steer waits for resume |
| `stop` | `superseded` | `superseded` | idempotent |
| `redirect` | `superseded` | `superseded` | `superseded` (redirect resumes) |
| `cancel` | `superseded` | `superseded` | `superseded` |

`cancel` always wins and is terminal (D7). A steer that arrives **after** a stop is not superseded by it — it is newer and waits for the resume (D3).

**Queue ruling (#1000, founder).** Upward done/wake messages (`handback`, `question`, `blocker`, fatal `error`, `goal_status`) are **never refused** by any cap: they are always written to the inbox (`MessageInboxStore.Append`), and a paused or stopped recipient simply is not woken until it resumes (ADR-091 D3's existing rule for a Stop-marked recipient, extended to `paused`). Ordinary `steer` messages are capped at **200** pending per child (today `pkg/agent/steering.go::MaxQueueSize = 10`); a steer over the cap is refused **visibly** with the cap named. Control verbs `stop` and `cancel` are exempt from the queue cap and from the 6/min rate cap (a stop must always get through); `redirect` is exempt from the queue cap (it supersedes the queue) but stays under the rate and 16 KiB body caps.

### D6 — A paused child is idle, alive, costs nothing — and counts as unfinished

| Aspect | Rule |
|---|---|
| Compute | No turn runs. Upward messages from its own descendants are stored and wake it only on resume |
| Execution slot | None held (ADR-091 D6: a slot is held only while a turn executes) |
| Deadline | Paused. `steeredTurnRunContext`'s deadline becomes `CreatedAt + TimeoutSeconds + paused_for`, where `paused_for` accumulates each pause interval |
| Addressable | Yes, until cancelled or resumed: `status`, `peek`, `inbox`, `steer` (queued), `redirect`, `follow_up`, `cancel` all work |
| Boot sweep | Not swept. `PlanEngine.bootSweep` gains an exemption for a `paused` record with a `pause` note, next to today's plan-owner exemption (b); without it every paused child would become `failed(interrupted)` on the next restart |
| Goal | A session goal stays active while paused (it is not ended — the session has not ended; #984's FD1=A pairs goal end with a **terminal** write). Idle-expiry applies as to any idle goal |

**Q2=B interaction [Q2B].** For the parent's "is the whole job done?" re-check, a **paused** descendant (either pause reason) counts as **unfinished**: `hasRunningOrQueuedDescendant` returns true for it. Because a paused child produces no events, a held claim that is blocked **only** by paused descendants would wait forever. So the runtime gives the claiming session one deterministic turn — single-flight per (claim, set of paused descendants), under the same `goalTriggerState.mu` the Q2 design uses — saying: "Your completion is held because these delegated sessions are paused: … Resume them (redirect/follow_up) or cancel them." Cancelling them makes the subtree quiet, which fires Q2's existing post-terminal re-evaluation. A parked (`needs_input`) descendant keeps ADR-091 D6's rule (does not block; its question travels in the handback) unless the founder answers Q4 otherwise.

### D7 — `cancel` is terminal, cascades down only, and never touches an ancestor's goal

Unchanged in mechanism (`CancelSubtree` / `StopSubtree`), restated as the control plane's rule:

- Terminal for the child's current generation; F890-1 still lets `follow_up` start a next generation later.
- Cascades to every reachable non-terminal descendant, **including paused ones**, and never upward or sideways (ADR-091 D8: "a Stop on a middle node B stamps B's subtree and leaves A and the root untouched").
- Ends the cancelled sessions' own goals (FD1=A) and **never** an ancestor's goal. #1000's ancestor guard (`goal_ancestor_guard_984_test.go`, root → child → grandchild) is the regression pin. Only the user ends a parent's goal, with `/goal clear` (`pkg/agent/goal_outcome.go::clearGoalByUser`).
- Supersedes every pending control on the cancelled sessions (D5).
- `stop` and `redirect` **never** cascade (D2). The difference is the whole point: "stop this one and let me think" versus "end this whole branch".

### D8 — Restart: interrupted children are paused, the top-level parent is told once, resume cascades down (amends ADR-091 §7)

**Today:** `SteerBootRecovery.recoverSteered` delivers an `interrupted` error to each in-flight session's direct parent and writes `failed(interrupted)`; `failInterrupted` ends its goal.

**New rule.**

1. **Pause, don't fail.** An in-flight native steered session at boot (not parked, no current Stop marker) is written `paused` with `pause.reason = interrupted_by_restart`, same generation; its goal is not ended. `PlanEngine.bootSweep` must leave these records to `SteerBootRecovery` (today both sweeps touch steered records — the spec fixes the order). Pending controls in the ledger survive (D4).
2. **Tell the top-level parent once.** Per boot and per tree, one wake-eligible inbox entry goes to the tree's root (`SteeredBy.RootSessionID`), with a deterministic id (`<root>:boot:<boot-id>:interrupted`) so boot recovery cannot duplicate it. It lists the root's direct children that were interrupted and how many interrupted sessions sit beneath each. Intermediate parents are themselves paused, so nobody else is woken.
3. **The root decides.** Nothing resumes automatically at the top. The root agent tells the person, and resumes a child with `follow_up` or `redirect` when asked (or the person writes into the child directly).
4. **A resumed child resumes its own interrupted workers.** When a session whose pause reason is `interrupted_by_restart` resumes, the runtime resumes each of its direct children whose pause reason is also `interrupted_by_restart`, and they do the same in turn as they resume. Children paused by an explicit `stop`, parked children and finished children are never resumed by this rule. The resuming session gets a note listing the workers it resumed.
5. **Unchanged:** standing roots are not swept (ADR-093 D3); parked sessions are preserved when reconstructable (`isNeedsInputReconstructable`); a current Stop marker stays stopped; external command-line children, whose process died with the gateway, keep today's `failed(interrupted)` and are resumed by `follow_up`'s corrective re-run (ADR-091 D5).

### D9 — Chat commands

#1011 F1 asks for `/stop` and `/redirect <instruction>` (alias `/steer`), scoped to the conversation they are typed in (channel commands are conversation-scoped, #955). They conflict with a standing decision: `docs/internal/specs/cancel-cross-channel-spec.md` decision 12 ("Chat=/cancel only (no `/stop` alias)"), enforced in `pkg/commands/cmd_cancel.go::cancelCommand`. The web Stop button today runs the cascading `CancelSubtree` then `RequestCancel` (`pkg/gateway/websocket_cancel.go::handleCancel`). This ADR does not pick the command names or change the Stop button — **Q1**.

### D10 — Rollback is out of scope

Undo/rewind of what a child did will not be built (#1011 F4, founder). Resumability already covers "continue from here"; irreversible actions (mail, external APIs, payments) must be gated **before** the action (the existing Ask policy, root `CLAUDE.md` Hard Constraint #6), never rewound after it.

## Consequences

### Positive

- The owner-only question round-trip works end to end at any depth, and R§8.2 is **stronger** than before: the answer that reaches the child is the owner's own message, copied by the runtime, instead of text any agent typed.
- A parent can stop a child now, or change its course atomically, with an enforced, receipted result — #1011 D5's "child told to stop at 5 counted to 15" becomes `stop` → `applied`.
- `steer` can no longer start work by accident (#1011 D4).
- Queued controls survive a restart (the ledger); a restart no longer destroys delegated work or burns goals.
- One vocabulary — live / paused / parked / finished — across tools, status, UI and the Q2 re-check.

### Negative

- A new durable artefact (the control ledger) and a new record note (`pause`), with their own crash boundaries: the spec must define the order of "ledger line" versus "lifecycle write" for each verb, and boot repair for a torn pair.
- `paused` now carries three meanings on one state (cancel-soft grace, plan owner awaiting supervision, and this ADR's pause); readers must look at the `pause` note / `OwnsPlanID`. A ninth lifecycle state was considered and rejected (below) but this is a real readability cost.
- A paused child's background shells keep running (no cascade); a forgotten paused tree holds disk and processes until cancelled or housekeeping.
- The root chat gets one extra system turn after a restart in which delegated work was interrupted.
- Wire changes in five schemas plus three new action schemas; the SPA receipt consumer changes with `steering_receipt` removed.

### Neutral

- `cancel`'s mechanism, the Stop marker and ADR-091 D8's cascade are unchanged.
- The 16 KiB body cap and 6/min rate cap are unchanged for `steer`/`respond`.
- FR-135's opt-in one-traversal escalation stays unbuilt; D1 is the default strict one-hop.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Auto-forward every `owner_required` question to the owner (FR-135 as the default) | The founder ruled that the parent **decides** to relay (R-D2); auto-forwarding also removes the parent's option to redirect or cancel instead |
| Let the root agent's `respond` pass once it claims the human said so | That is exactly what R§8.2 forbids: an LLM-authored owner answer. The `owner_message_id` + runtime copy makes the claim checkable |
| Park every relaying parent | A team lead keeps working while the founder thinks; parking every hop freezes the whole branch for one question |
| Pass the answer down hop by hop with an LLM turn per level | N model turns for a deterministic routing step, and each hop could alter the owner's words |
| `stop` as `cancel` without the cascade (terminal) | The founder ruled `stop` non-terminal and resumable (R-F1); a terminal stop would also end goals and send a terminal upward message |
| A ninth lifecycle state `stopped` | `paused` already exists, is already a non-terminal state everywhere (`pkg/session/lifecycle_bridge.go::lifecycleToUnifiedStatus` maps it with the non-terminal states) and is already in the `SubagentStateFrame` enum; a ninth state touches every switch over the eight |
| Keep `steer`-revives (ADR-093 D4 as written) | Overruled by the founder (R-D4) |
| Refuse `steer` on a paused child | F3 keeps a paused child addressable; queuing is harmless because nothing runs until resume |
| Keep receipts in memory | Lost on restart, which is exactly when a parent most needs to know what landed |
| Restart: the resumed child decides whether to resume its workers | Needs an LLM turn and a correct decision at every level for what is mechanical recovery; offered as Q3 option B |
| Restart: keep `failed(interrupted)` (terminal) and rely on `follow_up` | Throws away the goal (`failInterrupted` ends it), wakes every level separately, and cannot tell a restart from a real failure |

## Affected components

| Component | Change |
|---|---|
| `pkg/tools/delegate.go` | Action enum + schema: `stop`, `redirect`, `escalate`; `respond` gains `owner_message_id` |
| `pkg/tools/delegate_followup.go::executeSteer` | Finished → non-error "already finished — use follow_up", no revive; paused → queued |
| `pkg/tools/delegate_followup.go::executeFollowUp` | Accept paused (resume same generation) as well as finished |
| `pkg/tools/delegate_park.go::verifyQuestionAuthority`, `::resumeNative` | Owner-answer path (D1.4), guidance result instead of bare refusal, relay-chain resume (D1.5) |
| new `escalate`, `stop`, `redirect` executors in `pkg/tools` | D1, D2 |
| `pkg/agent/steering.go` | Queue cap 200; queue rebuilt from the ledger; pause-cause interrupt; paused recipients not woken |
| `pkg/agent/steer_completion.go::completionDisposition` | Pause cause → `paused`, no terminal write, no upward outcome |
| `pkg/agent/steer_completion.go::hasRunningOrQueuedDescendant` **[Q2B]** | Paused counts as unfinished; paused-only → one deterministic turn (D6) |
| `pkg/agent/steer_launcher.go::steeredTurnRunContext` | Deadline extended by `paused_for` |
| `pkg/agent/steer_frames.go` | Emit `ControlReceipt` transitions instead of `steering_receipt` |
| `pkg/agent/boot_sweep.go` | `SteerBootRecovery.recoverSteered` pauses instead of failing; one root notice per tree; `PlanEngine.bootSweep` exempts paused-with-note records |
| `pkg/session` | `pause` note, `paused_for`, control ledger |
| `pkg/tools/delegate_status.go` | `status`/`peek` list controls with state |
| `contracts/` | D4's wire table (backend-lead edits and regenerates) |
| `pkg/commands`, SPA | Only after Q1 |
| `pkg/tools/message_parent.go` | None for D1 (D1 of #1011, the artifact kind, is fixed separately) |

## Questions for the founder

**Q1 — Chat commands and the Stop button.** #1011 F1 asks for `/stop` and `/redirect` (alias `/steer`). The cross-channel cancel spec (decision 12) forbids `/stop` and the code enforces it; `stop` is also a word that clears a goal (`/goal stop`). Separately, today's Stop button cascades to the whole tree.
- **A (recommended):** add `/stop` and `/redirect` (+ `/steer`) as the non-cascading pause/redirect of the session the chat shows; amend decision 12; keep `/cancel` and the Stop button cascading as today.
- **B:** as A, and also make the Stop button non-cascading (ending a whole tree then needs `/cancel`).
- **C:** no new chat commands for now; the verbs exist for agents only.

**Q2 — Owner-only questions where no person is in the top conversation** (a heartbeat, a scheduled run, a task started from the board). D1 stops at a root where a person converses; these roots have none.
- **A (recommended):** the question waits at that root until the child's 24-hour park limit, and the child then gets "the owner could not be reached", visibly.
- **B:** also surface it to the owner outside the conversation (a notification or pending-approvals surface — new UI).
- **C:** refuse `owner_required` questions in such trees at the moment they are asked.

**Q3 — After a restart, who resumes a resumed child's workers?**
- **A (recommended):** the runtime, automatically, only workers interrupted by the restart; the child is told which.
- **B:** the child is told and decides for each worker.

**Q4 — Does a parked child (waiting for an answer) count as unfinished for Q2=B?** Your ruling names stopped/paused children; ADR-091 D6 (your round-2 decision) says a parked descendant does not block completion and its question travels in the hand-back.
- **A:** parked also counts as unfinished — the parent's one upward message waits for the answer.
- **B (recommended):** keep ADR-091 D6 for parked children.

## Evidence

| Claim | Evidence | Certainty |
|---|---|---|
| Baseline commit | `git worktree add … origin/release/v0.1.1` → HEAD `da8579715`, exit 0 | Verified |
| Owner-only refusal and its fail-closed branches | `pkg/tools/delegate_park.go::verifyQuestionAuthority` read | Verified |
| FR-132/134/135, R§8.2, R§8.7 text | `docs/internal/specs/unified-goal-plan-subagent-spec.md` grep, lines read | Verified |
| FR-135 not built | `grep -rn question_escalation pkg --include='*.go'` → no output | Verified |
| `steer` revives finished/stopped children | `pkg/tools/delegate_followup.go::executeSteer` read | Verified |
| `follow_up` terminal-only | `pkg/tools/delegate_followup.go::executeFollowUp` read | Verified |
| Queue cap 10, rate 6/min, body 16 KiB | `pkg/agent/steering.go::MaxQueueSize`, `::pushItemScope`; `pkg/session/message_inbox.go` constants | Verified |
| Receipt stamped at injection only | `pkg/agent/steer_frames.go::deliverSteeringReceiptsForInjection`; `SubagentStateFrame.yaml` | Verified |
| `paused` exists with two meanings; boot sweep exempts only plan owners | `pkg/session/lifecycle.go` constants; `pkg/agent/boot_sweep.go::PlanEngine.bootSweep` read | Verified |
| Interrupted turn → terminal `cancelled` | `pkg/agent/steer_completion.go::completionDisposition` read | Verified |
| Deadline anchored at `CreatedAt` | `pkg/agent/steer_launcher.go::steeredTurnRunContext` read | Verified |
| Restart fails steered sessions and ends goals | `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered`, `::failInterrupted` read | Verified |
| `/stop` forbidden; `stop` clears a goal | `pkg/commands/cmd_cancel.go::cancelCommand`; `cmd_goal.go::GoalClearAliases`; cancel-cross-channel spec decision 12 | Verified |
| Q2=B walk ignores paused/needs_input | `git show origin/feat/q2b-on-984:pkg/agent/steer_completion.go` (`hasRunningOrQueuedDescendant`) | Verified |
| `SteeredBy.RootSessionID` exists | `pkg/session/lifecycle_edge.go::SteeredBy` | Verified |
| #1000 queue ruling (never refuse done/wake; cap 200) | Stated in the dispatch brief; not found in #1000's body or the coordination logs | Unknown (source not located) |
| REQ-C1/2/3 named in #1011 F2 | Issue text only; the gist report was not read | Unknown |
