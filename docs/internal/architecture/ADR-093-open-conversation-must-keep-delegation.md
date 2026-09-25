# ADR-093 — An open conversation must keep the ability to delegate

- **Status:** Proposed. Not in force until the founder answers the questions at the end. If a question rejects the recommendation, the matching decision letter is withdrawn and the fallback named under that letter applies.
- **Date:** 2026-09-25
- **Deciders:** Daniel Piatkowski (founder, ratifies the questions); architect (draft).
- **Number verification:** no `ADR-093` file under `docs/internal/architecture/` on this branch. Highest numbered ADR present is [ADR-092 — Shell permission modes](./ADR-092-shell-permission-modes.md).
- **Evidence baseline:** `fix/890-terminal-parent-delegation` @ `d81bcb1ec` (cut from `release/v0.1.1`). Every `file::symbol` below was read in this worktree. The founder's lifecycle file was read from `/Users/danielpiatkowski/.omnipus/session_lifecycle/` on 2026-09-25; that disk is one machine, not a population.
- **Amends:** [ADR-091 — A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md) D1 (when a conversation root is minted), D8 (who counts as a newer instruction on a root chat), and §7 (what a restart may do to a conversation root). It does **not** weaken `pkg/session/lifecycle.go::persistLocked`'s rule that a terminal generation is never rewritten.
- **Related:** GitHub issue #890. Not #883.

## Context

### What broke

Delegating from a chat that is still open failed before the target agent was considered. The error was the lifecycle store's own refusal, quoted back to the user:

> terminal record is immutable: session "…" generation 1 is terminal (failed); a follow_up/Play must mint generation 2 via resumed_from

The session id in that sentence is the **parent chat**, not the agent the user asked for. Issue #890 separates three defects:

| # | Defect | What the user sees |
|---|---|---|
| 1 | A terminal lifecycle record does not close the chat | The conversation stays usable, with no explanation |
| 2 | A store rule is shown as the tool result | The text names the wrong session and a worker-revival recipe (`follow_up` / Play / `resumed_from`) the user did not ask for |
| 3 | Nothing brings the conversation back | Every later delegation from that chat fails the same way. The only workaround is a new chat, which the product never says |

### The chain, checked in code

```
delegate tool
  pkg/tools/delegate_run.go::launchAndDispatch
    SteerLauncher.Launch                         (SteeringSessionID = the chatting session)
      pkg/agent/steer_launcher.go::launchSteered
        LifecycleStore.PublishChildUnderParentLock
          persistLocked(parent)                  ← always, even when the parent was not changed
            same generation + terminal tail → ErrLifecycleTerminalImmutable
          child is not written
  launchAndDispatch formats the error as "delegate: launch: %v"
```

`PublishChildUnderParentLock` holds the parent's lock, runs the callback, then **always** appends the parent record and only then the child (`pkg/session/lifecycle.go::PublishChildUnderParentLock`). The callback in `launchSteered` changes the parent only when the parent has no record yet: it mints generation 1, state `running`, owner `human`, origin copied from the chat's session type (`pkg/agent/steer_launcher.go::launchSteered`). When the parent already exists, the callback only reads it (depth, Stop marker) and returns the child. The parent append still happens.

`persistLocked` rejects that append when the tail is terminal and the generation did not increase (`pkg/session/lifecycle.go::persistLocked`, error `pkg/session/lifecycle.go::ErrLifecycleTerminalImmutable`). The child file is never created. That matches the issue: no lifecycle record for the target, no files changed.

The lock itself is required. ADR-091 D8 / review R01: the launcher publishes a child under the parent's record lock and, if that parent carries a Stop marker for its current generation, stamps the child at launch so a Stop cannot land between the check and the publication. The **append of an unchanged parent** is not what closes that race. The lock and the stamp do.

### What the parent's record actually is

ADR-091 D1: every session that steers has a lifecycle record. A chat gets an `ordinary_root` record the first time it delegates, written in that same critical section. On disk, the founder's record is exactly that shape:

| Line | State | Generation | Origin | Steered by | Failed reason | Updated |
|---|---|---|---|---|---|---|
| 1 | `running` | 1 | `chat` | no | — | 2026-09-25 13:07:45Z |
| 2 | `failed` | 1 | `chat` | no | `interrupted` | 2026-09-25 13:29:17Z |

Nothing in the ordinary turn path sets a chat root back to a non-terminal "idle" state when the reply ends. The record stays `running` across later turns. `pkg/agent/steer_classify.go::SteerRecordClassifier.Classify` calls this `ClassOrdinaryRoot` (no steered-by edge, origin present, metadata not a delegate).

The shared chat metadata for the same id (`/Users/danielpiatkowski/.omnipus/sessions/session_01M30VSH24C9FG29838N9WZKG8/meta.json`) has `status: interrupted`, `type: chat`, and `updated_at` 14:06Z — after the lifecycle line. The founder kept using the chat. No component under `src/components/sessions/`, `src/components/chat/`, or `src/components/layout/` reads that session status to block the composer or to explain a failed delegation. The "(interrupted)" label in the chat is a **message** status (`src/components/chat/ChatScreen.tsx`), a different field.

Ordinary turns do not consult the lifecycle record. `pkg/agent/steer_cancel.go::reserveDispatch` (refuses a terminal record and a current Stop marker) runs from steered `Dispatch`, not from a human message that starts a root turn. So the UI and the turn loop both treat the conversation as alive while the one write that delegation needs treats it as frozen.

### Answer 1 — what sets `failed_reason=interrupted`, and how often a normal chat gets there

Two production writers assign that reason, both at boot, both the constant `pkg/agent/plan_engine.go::failedReasonInterrupted`:

| Writer | Who it marks | Skips a chat root? |
|---|---|---|
| `pkg/agent/boot_sweep.go::PlanEngine.sweepToFailedInterrupted`, called from `PlanEngine.bootSweep` | Every non-terminal lifecycle record, except a reconstructable `needs_input`, a paused plan-owner waiting for supervision, and a goal-semantics rebaseline | **No.** A chat root left `running` is swept. |
| `pkg/agent/boot_sweep.go::SteerBootRecovery.failInterrupted`, called from `recoverSteered` | A steered session that is still non-terminal and not parked | **Yes.** `SteerBootRecovery.Run` does nothing for `ClassOrdinaryRoot` ("existing root boot recovery remains authoritative") and does not resume the root. |

No other production assignment of `FailedReason` uses the literal `interrupted`. A chat Stop is a different terminal state: `pkg/gateway/websocket_cancel.go::handleCancel` calls `SteerCanceller.CancelSubtree` (stamps a Stop marker) and then `AgentLoop.RequestCancel`, which calls `pkg/session/lifecycle_bridge.go::TransitionSession` toward `cancelled`. `persistLocked` rejects a terminal record that still carries a current-generation Stop marker, so a Stop that stamped first does not itself produce `failed` / `interrupted`. The founder's two-line file (same generation, reason exactly `interrupted`, no Stop) is the sweep's shape, not the Stop's.

**How often.** A chat root is minted `running` on first delegation and is not completed when the turn ends. The next boot that runs `bootSweep` therefore marks it `failed(interrupted)`. That is every restart after the chat has delegated once, not a rare crash in the middle of a reply. On this machine, 2026-09-25: 145 lifecycle files; **2 of 2** chat-origin roots are `failed` / `interrupted` (this session at 13:29:17Z, and `session_01M3BG2D674E8QXFK7780NXJFZ` at 11:33:41Z, the restart the issue already names); **0** chat-origin roots are still non-terminal. Ten older roots with no origin are also `failed` / `interrupted`.

The 13:29:17Z line was **not** matched to a "boot sweep complete" line in `/Users/danielpiatkowski/.omnipus/logs/gateway.log`. That file contains four such lines, all from 2026-08-12. Which process appended the 13:29 line is Unknown. The code has only one writer that produces this record shape for a chat root.

A second way a chat root becomes terminal, less often: a Stop or other `TransitionSession` to `cancelled` that lands when the record has no current Stop marker (`pkg/agent/cancel.go::RequestCancel`). Delegation then fails the same way. D4 covers that case if the founder accepts it.

### Answer 2 — does any other launch path write under the parent?

No. `PublishChildUnderParentLock` has one production caller: `launchSteered`.

`SteerLauncher.Launch` branches on `SteeringSessionID`:

| Path | When | Touches the parent record? |
|---|---|---|
| `pkg/agent/steer_launcher.go::launchOrdinaryRoot` | No steering session (a task with no live creator, a schedule, a plan) | No. It persists only the new child. |
| `launchSteered` | Steering session set | Yes. The coupling in this ADR. |

Two callers set a steering session, and both go through that one function (`steer_launcher.go::inheritDelegatePermissions`'s own comment):

| Caller | Steering session |
|---|---|
| `pkg/tools/delegate_run.go::launchAndDispatch` | The chatting turn's transcript session. Always set. |
| `pkg/agent/task_executor.go::startTaskNowViaLauncher` | The task's `OriginSessionID`, if that session's **metadata** still loads. A swept chat still has metadata, so a task started from it takes the broken path too. |

`inheritDelegatePermissions` runs after a successful publish and does not write the parent lifecycle record.

### Invariants this ADR will not break

From ADR-091, read against the code:

| Rule | Where | What it forbids here |
|---|---|---|
| A terminal generation is immutable. A new generation is the only legal append, via `resumed_from`. | `persistLocked`; ADR-091 review R05 | Option (b): a special case that appends onto a terminal parent, or that publishes a live child on the dead generation. |
| Generation moves when a stopped session is revived by a newer instruction, and on a terminal follow-up. Not on an ordinary re-entry. | ADR-091 D8; `pkg/agent/steer_cancel.go::SteerCanceller.Revive` | Treating every delegation as the revival. A tool call is not the human's newer instruction. |
| Stop covers the whole generation: running work and queued re-entry. A launch racing the cascade is stamped, not missed. | ADR-091 D8 / R01 | Dropping the parent lock, or publishing a runnable child under a current Stop marker. |
| An ancestor file is kept while any descendant is non-terminal. | `pkg/session/lifecycle.go::pruneTerminalOne` | Leaving a live child under a parent that stays terminal. Prune would then keep the dead parent forever. Revival must make the parent non-terminal **before** the child exists. |
| Creator gone → the task is an ordinary root. | ADR-091 appendix F07, as implemented by the empty-steering branch | Treating "metadata file still exists" as "creator can still steer" after the lifecycle record is terminal. |
| A steered session in flight at restart is re-woken once or terminalised and reported upward. | ADR-091 §7; `SteerBootRecovery` | Exempting steered workers from the sweep. This ADR exempts conversation roots only. |

`SteerCanceller.Revive` already mints `generation+1`, sets `ResumedFrom` to the same session id, clears the failed reason, and leaves an older Stop marker as inert history. The production caller `pkg/agent/steering.go::ReviveStoppedSession` returns without doing that unless a Stop marker names the **current** generation. A `failed` / `interrupted` chat has no Stop marker, so a later human message does not revive it. A fresh root turn never calls `reserveDispatch`.

### Blast radius

GitNexus is not indexed in this worktree (`gitnexus status` → "Repository not indexed"). The radius below is a caller sweep, not a graph run. Certainty: Inferred.

| Symbol | Production callers | Risk if changed |
|---|---|---|
| `PublishChildUnderParentLock` | `launchSteered` only | Low. One call. Tests in `pkg/session` cover the lock and the rollback. |
| `SteerLauncher.Launch` | `delegate_run.go::launchAndDispatch`, `task_executor.go::startTaskNowViaLauncher` | Medium. Both fronts must keep the same rule. |
| `PlanEngine.bootSweep` / `sweepToFailedInterrupted` | Boot only | Medium. An over-broad exemption would leave a stranded task `running` with no turn. |
| `SteerCanceller.Revive` / `ReviveStoppedSession` | Mid-turn human message when a Stop marker is current; delegate follow-up via the reviver | Medium. Extending revival to a root chat must not let a queued wake or a tool call mint a generation. |
| `persistLocked` | Every lifecycle write | Do not change the terminal rule. |

## Decision

### D1 — The parent lock is for ordering. An unchanged parent is not rewritten

Option (a), narrowed so it cannot start work on a dead generation (that narrowing is D2).

`PublishChildUnderParentLock` keeps holding the parent's lock from the read through the child append, so a concurrent `CancelSubtree` cannot enumerate between the check and the publication (ADR-091 R01). If the callback did not change the parent, the store does not append a parent line. The rollback of a failed child write then has nothing to undo on the parent.

The first-delegation mint stays inside the lock: no record yet → write the `ordinary_root` line, then the child. That is ADR-091 D1, unchanged.

### D2 — A dead generation does not gain a live child

Option (b) is rejected.

Under the same lock, if the parent is terminal, or its Stop marker names the current generation, the launch does not publish a child that is allowed to run on that generation. The existing stamp (a current Stop marker copied onto the child at launch) stays for the race ADR-091 already closed. A terminal parent is not given a new exception inside `persistLocked`.

A launch that refuses here returns a mapped tool result (D5), not `ErrLifecycleTerminalImmutable`'s text.

**Why (b) is worse than it sounds.** "Publishing a child is not a rewrite of the parent" is true only if the parent line is skipped (that is D1). Allowing the child anyway, on the dead generation, means work starts after a Stop or a sweep without a new generation. ADR-091 D8 says nothing in that generation starts again. The child would also pin the terminal parent against `pruneTerminalOne` for as long as the child is non-terminal.

### D3 — A conversation root is not a stranded turn

`PlanEngine.bootSweep` must not call `sweepToFailedInterrupted` on a conversation root.

A conversation root is a record with no steered-by edge whose origin kind is `chat` or `channel` — the kinds `launchSteered` copies from the chat's own session type (`pkg/session/lifecycle_edge.go::OriginKind`). Records whose origin is `task`, `delegate`, `scheduled`, `heartbeat`, `verifier`, `plan`, or `human`, and every steered record, keep today's sweep. `SteerBootRecovery` keeps skipping `ClassOrdinaryRoot` and keeps terminalising steered sessions that were in flight.

This is the frequency fix. Without it, D4 repairs the chat on the next message and the following restart breaks delegation again, because the root is `running` between turns and the sweep treats that as a crashed turn.

**Fallback if the founder rejects D3:** the sweep stays as it is. D4 is then the only recovery, and it must run at the start of the next human turn or the first delegation after every restart still fails.

### D4 — The next human message in that chat is the newer instruction

Option (c), scoped to the instruction ADR-091 D8 already names, and applied where the code currently skips it.

When a **human message** starts a turn in a conversation whose lifecycle record is terminal, or stopped for its current generation, the turn revives that record **before any tool runs**: `SteerCanceller.Revive`'s existing shape (generation + 1, `ResumedFrom` = this session id, state `running`, failed reason cleared, an older Stop marker kept as history). Delegation later in that turn then sees a non-terminal parent. D1's skipped append is legal. D2 does not fire.

These are **not** a revival: a boot wake, a queued re-entry, `delegate`, `create_task` / `run_task`, or `Dispatch`. They must not mint a generation. A Stop still means nothing in the old generation starts (ADR-091 D8). `ReviveStoppedSession` today misses the `failed` / `interrupted` case because it returns immediately when there is no current Stop marker; that gap closes for the human-message path only.

**Fallback if the founder rejects D4:** no revival. The chat can still be typed in, but D2 refuses delegation, and D5 tells the user to start a new chat. That is today's workaround, made visible.

### D5 — The tool result does not quote the store

Defect 2, either way.

`pkg/tools/delegate_run.go::launchAndDispatch` already turns a requested-skill failure into a structured result (`requestedSkillDispatchFailureResult`) and turns every other launch error into `delegate: launch: %v`, which is how `ErrLifecycleTerminalImmutable` reached the founder. A refusal from D2, and this sentinel if it still escapes, become a short result:

- If D4 is accepted: the conversation had to be resumed, and sending a message does that. If this result is shown, the resume did not happen before the tool call — say that, and do not invent a button.
- If D4 is rejected: this conversation cannot start another agent; start a new chat with the same agent.

The result must not contain a session id, a generation number, `follow_up`, `Play`, or `resumed_from`. `startTaskNowViaLauncher`'s `launch: %w` wrapper gets the same mapping when the founder has not chosen the ordinary-root fallback (D6).

### D6 — A task whose creating chat is terminal launches as an ordinary root

Unless that launch is inside a turn which D4 just revived.

`startTaskNowViaLauncher` sets `SteeringSessionID` when `OriginSessionID` still has metadata. A swept or cancelled chat still has metadata, so the task takes `launchSteered` and dies with the parent. ADR-091's "creator gone → ordinary root" applies: a terminal creator that this launch did not just revive is gone for steering purposes. The task uses `launchOrdinaryRoot` (no parent append). A creator that is merely stopped for its current generation, with no new human message, is not "gone" — D2 refuses, so a stopped chat cannot sprout a task on the dead generation.

**Fallback if the founder rejects D6:** do not fall back. D2 refuses and D5's message is what the task start returns.

### D7 — No new "this chat is dead" chrome, unless the founder asks

Defect 1 is the record and the screen disagreeing. The recommended repair makes the record follow the screen (D3, D4), not the screen follow the record. The composer staying open is the revival. A badge that says the conversation is finished would be a new lie in the other direction, and blocking the composer is the bug made explicit.

The session-status value `interrupted` may still be written by today's Stop path (`websocket_cancel.go`). This ADR does not add a renderer for it.

## Comparison

| | Defect 1 — chat looks alive | Defect 2 — raw store text | Defect 3 — no recovery | ADR-091 | After a restart | After Stop, then another message |
|---|---|---|---|---|---|---|
| (a) alone: skip the parent append, still publish the child | Unfixed, but delegation works, so the lie stops mattering for this tool | Gone on the success path | Delegation works, including on a generation the user stopped | Breaks D8: a live child on a dead generation. Pins the parent against prune | Works, by breaking the sweep's meaning | Works, by undoing Stop without a new generation |
| (b): allow a terminal parent to publish | Same as (a) | Same as (a) | Same as (a) | Weakens `persistLocked` or special-cases one caller. Same D8 break | Same as (a) | Same as (a) |
| (c) alone: revive when the user resumes | Fixed once the next human message has started the turn. Until that message, and again after the next restart, the record is terminal | Fixed after revival. Still needed as a backstop (D5) | Fixed for "the user kept typing". Not for a task whose creator was swept and nobody typed | Matches D8 if only a human message revives. The sweep then re-breaks the chat every boot | Broken again until the next message | Fixed, as a new generation |
| **(a)+(c), plus not sweeping conversation roots. This ADR.** | The screen and the record agree after the next human turn, and a restart no longer terminals the chat | D5, even when the launch succeeds | Same chat can delegate. A stopped generation stays dead until a human message | Lock and stamp kept. Terminal rule kept. Revival is the generation move D8 already describes | Chat root stays `running`. Workers still swept | New generation, then delegate works |
| (a)+(c) without the sweep exemption | Same as the row above until the next restart | D5 | Recurs every boot | Same | Broken again | Fixed |

(a) without D2 is not recommended. (b) is not recommended in any combination.

### Reachability

| Who | Today | After this ADR, if the founder accepts D3 and D4 |
|---|---|---|
| A person in the same chat | Can type. Delegation fails. Nothing on screen says why. | Can type. The turn revives the record first. Delegation starts the other agent. No new screen. |
| The chatting agent, `delegate` | `launchAndDispatch` fails before `Dispatch`. | Launch publishes the child. `Dispatch` admits it. |
| A task whose creating chat was swept, started later from the board or a trigger | `startTaskNowViaLauncher` takes the steered path and fails. | Ordinary root (D6). The task runs. It does not wake the dead chat. |
| A steered worker left `running` at shutdown | Swept to `failed(interrupted)` and reported upward. | Unchanged. |

### Security and audit

| Topic | Effect |
|---|---|
| Stop | Still one generation. A human message is what mints the next one. A tool call, a queued wake, and a boot sweep do not. |
| Audit | Revival appends one legal line: new generation, `resumed_from` set. Skipping the unchanged parent append avoids a second line that looks like a state change and is not. |
| `persistLocked` | Unchanged. No terminal generation is rewritten. |
| Prune | A live child is not published under a parent that remains terminal, so `pruneTerminalOne` is not asked to retain a dead chat forever. |
| Permissions | No change to tool policy, grants, or who may call `delegate`. `inheritDelegatePermissions` still runs only after a committed steered launch. |
| Cross-user | No new read of another account's session. Revival is of the session the human just wrote to. |

### Test strategy

Tests prove the rule, not the current branches. Suggested homes: `pkg/session` for the lock (parent line count does not grow when the parent is unchanged; a terminal parent is not appended; a missing parent is still minted atomically with the child), `pkg/agent` for sweep and revival, `pkg/tools` for the tool text.

| Case | Expected |
|---|---|
| Chat root `running`, no Stop. Delegate. | Child published. Parent file does not gain a line. |
| Chat root `failed` / `interrupted`. Human message starts a turn, then delegate. | Generation 2, `resumed_from` = same id, state `running`, then the child exists. |
| Same record. Delegate with no new human message (and D4's turn-start revival not in this call). | No child. Tool text has no session id and no `follow_up` / `resumed_from`. |
| Chat root with a current Stop marker. Queued wake or delegate, no new human message. | No runnable child. Generation unchanged. |
| Chat root `cancelled` or stopped. A new human message, then delegate. | New generation, then the child runs. |
| Boot sweep: one chat root `running`, one steered child `running`, one task root `running`. | Chat root still `running`. Child and task root `failed` / `interrupted`. |
| Task with `OriginSessionID` of a terminal chat, not inside a just-revived turn. | `launchOrdinaryRoot`. No write under the chat's lock. |
| Task with `OriginSessionID` of a chat this turn just revived. | Steered child of the new generation. |
| `launchOrdinaryRoot` (no steering id). | Unchanged. |
| Concurrent Stop during launch. | Existing cascade test still holds: the child is stamped or the launch does not leave an unstamped runnable child. |

UI: no new component. Reachability check is a person (or the UAT lane) in the **same** chat after a restart, asking for a delegation, and a lifecycle file appearing for the target. A unit test that only asserts the error string is not that check.

## Consequences

### Positive

- A restart no longer retires every chat that has ever delegated.
- The store rule stops being a user-facing sentence.
- Stop still ends the generation. Continuing to type is an explicit new generation, which is what ADR-091 already says a newer human instruction does.
- One less no-op append on every successful delegation (the parent JSONL does not grow by a duplicate snapshot).

### Negative

- A conversation root left `running` across a restart has no turn behind it. That is already its normal state between replies. Operators who used `failed(interrupted)` on a chat root as a signal that "this chat died at last boot" lose that signal. Steered workers still carry it.
- Records already swept (two chat roots on the founder's machine today) stay terminal until a human message revives them. D3 does not rewrite history.
- D6 means a task started later from a swept chat is not steered by that chat. Its result does not arrive as a handback into the old conversation. That is the same shape as a task whose creator session was deleted.

### Neutral

- `ErrLifecycleTerminalImmutable` remains the store's error for every other same-generation write onto a terminal tail (cancel vs complete, a bad `Mutate`). Only the launch refusal and the tool result change.
- The side panel, pill, and session list do not gain a field.
- GitNexus impact was not run; the caller sweep above is the radius the implementer re-checks before editing.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| (b) Terminal parent may publish children | Either weakens the terminal rule or starts a live child on a dead generation. Both conflict with ADR-091. Does not fix defect 1. |
| (a) without D2 | Removes the error and also removes Stop's guarantee. |
| (c) without D3 | Correct recovery, then the next restart breaks the chat again. Largest ongoing surprise. |
| (c) for every terminal state, including a tool call as the "resume" | A delegate call would revive the generation it is trying to escape, including a queued call after Stop. That is the hole D8 closed. |
| Force a new chat and block the composer | Makes defect 1 honest and leaves defect 3 as a manual workaround. The issue's expected behaviour allows either "delegation succeeds" or "the user is told to restart". Telling them is D5's fallback, not the recommendation: the conversation is the thing they are still using. |
| Map the error text only | Fixes defect 2. Defects 1 and 3 remain. Every restart still bricks the chat. |
| Exempt every `ClassOrdinaryRoot` from the sweep | That class includes a task root with no parent (`SteerRecordClassifier.Classify` row 2). A task left `running` at boot would never be failed. The exemption is origin `chat` / `channel` only. |

## Affected components

| Component | Change |
|---|---|
| `pkg/session/lifecycle.go::PublishChildUnderParentLock` | Skip the parent append when the callback did not change the parent. Still persist a parent the callback minted. Do not relax `persistLocked`. |
| `pkg/agent/steer_launcher.go::launchSteered` | Under the lock: refuse a runnable child on a terminal or currently-stopped parent. |
| `pkg/agent/boot_sweep.go::PlanEngine.bootSweep` | Skip conversation roots. |
| `pkg/agent/steering.go::ReviveStoppedSession` and the root-turn admission path | Human message revives a terminal or currently-stopped conversation root before tools. No other caller of `Revive` grows a new reason to run. |
| `pkg/tools/delegate_run.go::launchAndDispatch` | Mapped result, same idea as the requested-skill branch. |
| `pkg/agent/task_executor.go::startTaskNowViaLauncher` | Terminal unreived creator → empty steering id → `launchOrdinaryRoot`. |
| SPA | None, unless the founder picks a one-line note in Q3. |
| Contracts | None. No new wire field. |

## Questions for the founder

The recommendation is one package: D1, D2, D3, D4, D5, D6, and no new chrome (D7). (b) is not in the package. The letters above are the proposal; the four questions are the parts that change what a person sees or what a restart means. Answer like "Q1 B, Q2 A, Q3 A, Q4 A".

### Q1 — What a restart does to a chat that has delegated before

**Context.** The first time a chat delegates, the product writes a lifecycle record and leaves it `running` forever after, including between ordinary replies. On the next gateway start, the boot sweep marks every such record `failed(interrupted)`. That is the founder's 13:29 line. Steered workers should still be swept: a worker left `running` has no turn behind it and its parent must be told.

**Impact.** If the sweep stays, every restart silently removes delegation from every chat that has ever delegated, until something revives the record. If chat roots are exempt, a crash in the middle of a reply does not leave a "failed" chat. The reply itself is already gone either way: root turns are not resumed at boot.

**Options.** This question is only about the sweep from now on. What happens to a record that is already terminal is Q2.

| | Choice |
|---|---|
| A | Keep sweeping chat roots. Every restart marks them `failed(interrupted)` again. |
| B | Stop sweeping conversation roots (`chat` / `channel`, no steered-by edge). Workers and other work records stay on the sweep. |

**Recommendation: B.** Pair it with Q2 A, or the chats already swept on this machine stay unable to delegate.

### Q2 — Stop, then keep typing in the same chat

**Context.** ADR-091 D8 says a newer instruction from the human revives a stopped session as a new generation. The code does that only when a Stop marker is current and the message arrives while a turn is still in flight. A chat whose record is `failed` or `cancelled` does not revive when the user types. The composer accepts the message anyway.

**Impact.** If the next message revives, Stop still ends the work that was running, and the next thing the user types can delegate. If it does not, that chat can never delegate again; the user must start a new chat, and the product has to say so (D5's fallback).

**Options.**

| | Choice |
|---|---|
| A | The next message the user sends in that chat revives it before any tool runs. A queued or automatic wake does not. |
| B | Stop ends delegation in that chat for good. The tool result tells the user to start a new chat. The composer stays open for ordinary replies. |

**Recommendation: A.** It is the reading of D8 that matches a composer which already accepts the message.

### Q3 — Should the screen say the conversation was interrupted?

**Context.** The chat metadata for the founder's session is already `status: interrupted`, and nothing in the chat screen reads it. Defect 1 is that silence. The recommended repair makes the lifecycle record agree with the open chat, rather than marking the chat closed.

**Impact.** A badge or a block would teach the user that the conversation died. That is misleading if the next message resumes it (Q2 A) and a restart no longer kills it (Q1 B). A block repeats today's failure in clearer words.

**Options.**

| | Choice |
|---|---|
| A | No new chrome. |
| B | One non-blocking line, once, when the record is terminal and a message has not revived it yet. It goes away when the record is `running` again. Wording to be written with the screen, not in this ADR. |
| C | Block the composer until the user starts a new chat. |

**Recommendation: A.** Pick B only if a restart should stay visible even though the conversation still works.

### Q4 — A task created from a chat that has since gone terminal

**Context.** A task remembers the chat that created it (`OriginSessionID`). Starting that task later still tries to steer it from that chat, and today that launch fails with the same store error. ADR-091 already says: if the creator is gone, the task is an ordinary root (nobody to steer it).

**Impact.** Falling back means the task runs, and its result does not come back into the old chat. Refusing means the task does not start until someone types in that chat and revives it (Q2 A), or never, if Q2 is B.

**Options.**

| | Choice |
|---|---|
| A | If the creating chat is terminal and this start is not inside a turn that just revived it, start the task as an ordinary root. |
| B | Fail the task start with the same plain message as a refused delegation. Do not revive the chat as a side effect of starting the task. |
| C | Starting the task revives the old chat, then steers from it. |

**Recommendation: A.** C is rejected by this ADR even if chosen later without a second look: a task start is not the user typing in that conversation, and it would wake a chat nobody opened.
