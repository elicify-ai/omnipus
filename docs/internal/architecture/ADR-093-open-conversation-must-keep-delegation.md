# ADR-093 — An open conversation must keep the ability to delegate

## Amended 2026-10-07 — session-core founder decisions

**Dated correction of D2/D6 task child admission, D7 restart presentation and #1198 blanket deferral.** A future authorized scheduled MAIN occurrence may create a real child of a stopped main without resuming its model, ordered against Stop; reports hold. Preserve the general arbitrary-helper guard and selected-execution fences. A restart-cut conversation shows Interrupted, not Working/failed because of restart. Unified archive/intake and approved 64 KiB/60-per-sender/200/1 MiB+model limits are now in scope; waiting-input restart reconstruction/live stop retry stay deferred.

Authority and complete clause/reuse map: **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md`::Decision / Earlier ADR clauses amended; founder ledger `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/CONTINUATION-20261005.md` plus the binding October 7 answers through 15:55. This is a design amendment, not a claim that production implements it. Conflicting older prescriptions below are historical within this scope; unrelated decisions and historical evidence remain unchanged.


**Amended 2026-10-06 (founder):** Stop click 1 / Esc 1 / `/stop` stops **this chat's current turn only** and opens a **3 s window**. A second Stop / Esc / `/stop` within that window stops **this chat and its whole helper tree**; `/cancel` does that immediately. **No separate button or offer.** `/stop-redirect <instruction>` stops this chat's turn and continues **this chat** with the instruction, in **any root or helper chat**. Same semantics on web, CLI and channels. Plain Stop leaves background shells running; a second Stop / Esc within 3 s, or `/cancel`, kills them. Agent delegate `stop` / `stop_all` is unchanged (one helper's turn / its tree). Authority: founder decision, 2026-10-06.

## Amended 2026-10-06 — founder decision

[The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](ADR-20260928-sub-agent-control-plane.md)::D2/D8 and [Steering commands: no person question](ADR-20261004-steering-commands-no-person-question.md)::C1 supersede conflicting generation, boot and message rules below. Both call `AgentLoop.StopSession` with polite stop, force at 3 s and detach 3 s later; no public `hard` option or agent `cancel_grace`.

**A finished root chat becomes lifecycle `completed` (done); it is NOT archived or hidden.** A human message continues it. A new scheduled/heartbeat run may revive a completed root as the **system principal**, into a new round; it **never revives a STOPPED root** or an in-flight Stop. This replaces the old blanket “system wakes never revive” and indefinitely-running standing-root wording; it does not turn boot replay, a tool call or an old helper-result wake into permission to undo Stop. Stopped resumes keep the generation and get a fresh execution identity; committed done/failed resumes mint the next generation.

A helper final reaches the parent once (poll OR hand-back wake). A stopped parent holds it unconsumed; resume alone is not consumption. A tree-scope stop supersedes queued hand-back wakes, not saved history. The message-route/record unification, waiting-message restart reconstruction, 256 KiB aggregate cap, ledger compaction and live descendant-stop retry are **deferred to #1198 (founder 2026-10-06)**. See F6 for the later simplification review, not an authorization to simplify now.

- **Status:** Proposed. Founder decisions F890-1 … F890-4 (2026-09-25, recorded in [ADR-093 founder decisions](./ADR-093-founder-decisions.md)) are incorporated in this correction; the ADR now waits for the spec → RED → GREEN lane, not for answers. Nothing in it is open; the one deferred item — group-chat resume authorisation — is tracked as issue #892 (F890-3).
- **Date:** 2026-09-25 (round 1); corrected 2026-09-26 (the single correction round of the ADR-mode grill — review verdict REVISE, founder interviewed, this correction is the one fix).
- **Deciders:** Daniel Piatkowski (founder — F890-1 … F890-4); architect (draft and this correction).
- **Number verification:** this file is ADR-093; the highest other numbered ADR is [ADR-092 — Shell permission modes: Ask / Auto / God Mode; drop the block list; one rule format](./ADR-092-shell-permission-modes.md).
- **Evidence baseline:** `fix/890-terminal-parent-delegation` @ `044c89bea` (round 1 was written against `d81bcb1ec`; the review ran against `c771fc398`). This correction re-verified every symbol it relies on in this worktree on 2026-09-26 — the re-verified symbols are named in the Review disposition table and in the correction's evidence table. The founder's lifecycle files were read from `<instance-data>/session_lifecycle/` on 2026-09-25; that disk is one machine, not a population.
- **Amends:** [ADR-091 — A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md) D1 (when a conversation root is minted), D8 (who counts as a newer instruction on a root chat), and §7 (what a restart may do to a conversation root). It does **not** weaken `pkg/session/lifecycle.go::persistLocked`'s rule that a terminal generation is never rewritten: resuming mints the next generation on top via `resumed_from`; history is never edited.
- **Reviewed:** grill round 1 (ADR mode), verdict REVISE — [ADR-093 open conversation must keep delegation review](./ADR-093-open-conversation-must-keep-delegation-review.md): 6 MAJOR, 6 MINOR, 3 OBSERVATION. Every finding is disposed in the [Review disposition](#review-disposition) section.
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

The shared chat metadata for the same id (`<instance-data>/sessions/session_01M30VSH24C9FG29838N9WZKG8/meta.json`) has `status: interrupted`, `type: chat`, and `updated_at` 14:06Z — after the lifecycle line. The founder kept using the chat. No component under `src/components/sessions/`, `src/components/chat/`, or `src/components/layout/` reads that session status to block the composer or to explain a failed delegation. The "(interrupted)" label in the chat is a **message** status (`src/components/chat/ChatScreen.tsx`), a different field.

Ordinary turns do not consult the lifecycle record. `pkg/agent/steer_cancel.go::reserveDispatch` (refuses a terminal record and a current Stop marker) runs from steered `Dispatch`, not from a human message that starts a root turn. So the UI and the turn loop both treat the conversation as alive while the one write that delegation needs treats it as frozen.

### Answer 1 — what sets `failed_reason=interrupted`, and how often a normal chat gets there

Two production writers assign that reason, both at boot, both the constant `pkg/agent/plan_engine.go::failedReasonInterrupted`:

| Writer | Who it marks | Skips a chat root? |
|---|---|---|
| `pkg/agent/boot_sweep.go::PlanEngine.sweepToFailedInterrupted`, called from `PlanEngine.bootSweep` | Every non-terminal lifecycle record, except a reconstructable `needs_input`, a paused plan-owner waiting for supervision, and a goal-semantics rebaseline | **No.** A chat root left `running` is swept. |
| `pkg/agent/boot_sweep.go::SteerBootRecovery.failInterrupted`, called from `recoverSteered` | A steered session that is still non-terminal and not parked | **Yes.** `SteerBootRecovery.Run` does nothing for `ClassOrdinaryRoot` ("existing root boot recovery remains authoritative") and does not resume the root. |

No other production assignment of `FailedReason` uses the literal `interrupted`. A chat Stop is a different terminal state: `pkg/gateway/websocket_cancel.go::handleCancel` calls `SteerCanceller.CancelSubtree` (stamps a Stop marker) and then `AgentLoop.RequestCancel`, which calls `pkg/session/lifecycle_bridge.go::TransitionSession` toward `cancelled`. `persistLocked` rejects a terminal record that still carries a current-generation Stop marker, so a Stop that stamped first does not itself produce `failed` / `interrupted`. The founder's two-line file (same generation, reason exactly `interrupted`, no Stop) is the sweep's shape, not the Stop's.

**How often.** A chat root is minted `running` on first delegation and is not completed when the turn ends. The next boot that runs `bootSweep` therefore marks it `failed(interrupted)`. That is every restart after the chat has delegated once, not a rare crash in the middle of a reply. On this machine, 2026-09-25: 145 lifecycle files; **2 of 2** chat-origin roots are `failed` / `interrupted` (this session at 13:29:17Z, and `session_01M3BG2D674E8QXFK7780NXJFZ` at 11:33:41Z, the restart the issue already names); **0** chat-origin roots are still non-terminal. Ten older roots with no origin are also `failed` / `interrupted`.

The 13:29:17Z line was **not** matched to a "boot sweep complete" line in `<instance-data>/logs/gateway.log`. That file contains four such lines, all from 2026-08-12. Which process appended the 13:29 line is Unknown. `runBootSweep` logs through `pkg/logger`, which does not have to write to `gateway.log`, so the absence of sweep lines there is not evidence of absence — the "every restart" claim stays **Inferred from code**, which has exactly one writer that produces this record shape for a chat root.

**Correction (2026-09-26, MAJ-001).** Round 1 said a web Stop turns a chat root `cancelled`. That is wrong for the common path. A web Stop goes through `pkg/gateway/websocket_cancel.go::handleCancel`, which runs `cancelSteeredSubtree` **first** — `SteerCanceller.CancelSubtree` stamps a Stop marker for the chat root's **current** generation — and only then `RequestCancel` → `pkg/session/lifecycle_bridge.go::TransitionSession` toward `cancelled`. `persistLocked` rejects that write (`terminal record (state %q) cannot carry a current-generation stop marker`, `pkg/session/lifecycle.go::persistLocked`), so the cancel path only logs a warning and the record stays **`running` with a current Stop marker** — across restarts too, because the sweep's own `failed(interrupted)` write fails the same validation. What a chat root can therefore actually be, and where each state comes from:

| # | State | Written by | Delegation from that state today |
|---|---|---|---|
| 1 | `failed(interrupted)` | `pkg/agent/boot_sweep.go::sweepToFailedInterrupted` (restart sweep) | Launch refuses before the target agent is considered — the #890 error |
| 2 | `running` + current-generation Stop marker | `pkg/gateway/websocket_cancel.go::handleCancel` (web Stop) | Launch publishes a child stamped with the parent's Stop, then `Dispatch` refuses (`pkg/agent/steer_cancel.go::reserveDispatch`) — `delegate: dispatch: <raw error>`, and a stamped child record is left behind that nothing will ever run |
| 3 | `cancelled` | Only cancel paths that do **not** run the cascade first, e.g. `pkg/agent/cancel.go::RequestCancelForSession` | Launch refuses — the #890 error |

State 2 is the common case after a person presses Stop; state 1 is the common case after a restart. This ADR fixes both: D4 makes the next human message mint the next generation from state 1 and state 2 alike; D2 makes the backstop refusal (state 2 or 3, no revival in the call chain) leave no orphan child behind.

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

### Invariants this ADR will not break (historical 2026-09-26 formulations)

**Amended 2026-10-06:** outcome history remains immutable. The stopped-generation increment and blanket automatic-wake prohibition below are superseded by D3/D4 above. The control-plane D2 permits narrow delivery-metadata-only updates for committed finals, never rewriting their outcome.

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

The founder's rule (F890-1, confirmed by F890-2), which every rule below implements:

> Every session — conversation root, child/worker, heartbeat, recurring — is **always resumable**, like Claude Code, until housekeeping deletes it. A restart never makes a session unusable. Stop only ends the current turn; the next message continues it — and a parent can send a follow-up to an existing child a month later. A task created from a stopped or finished chat still runs. Terminal records stay immutable as history: resuming mints a new generation on top (`resumed_from`), never rewrites.

### D1 — The parent lock is for ordering. An unchanged parent is not rewritten

As in round 1: `PublishChildUnderParentLock` keeps holding the parent's lock from the read through the child append, so a concurrent `CancelSubtree` cannot enumerate between the check and the publication (ADR-091 review R01). What is new is the mechanism the review asked for (MIN-001): the store learns "the callback did not change the parent" from **`existed`**, the `bool` the primitive already passes to the callback. The store appends the parent line **only when `existed == false`** — the mint case, which is ADR-091 D1's first-delegation write, unchanged and still inside the lock. A callback that needs to change an existing parent is out of contract for this primitive (it must go through `Mutate`); today's only callback, `pkg/agent/steer_launcher.go::launchSteered`, already obeys that: it mints when there is no record and only reads otherwise. Rollback consequence: the truncate-branch runs only in the mint case, where it removes a parent file this publication created; with no parent append there is nothing on the parent to undo.

### D2 — A stopped or terminal generation does not gain a child: the refusal is explicit and leaves nothing behind

Option (b) stays rejected. The review's ambiguity (MAJ-005) is resolved as **refuse outright, publish nothing**: under the parent lock, inside the launch callback and **before anything is created**, the launch checks the parent state. Parent terminal, or carrying a Stop marker for its current generation, is a typed refusal — new sentinel `steer.ErrSteeringStopped` in `pkg/steer` (name proposed by the review; the name does not exist in the tree today). The refusal precedes `CreateSessionWithID`, the child meta/history write, and the launch goal, so a refused launch produces **no child lifecycle file, no unified session, and no goal record** — nothing the boot sweep would have to clean up and nothing `pruneTerminalOne` would retain a live child under.

With the refusal inside the locked callback, the stamp-at-launch branch (`pkg/agent/steer_launcher.go::launchSteered`, the `stopStamp` copy of the parent's current Stop onto a child) has **no remaining case**: a child is never published under a stopped or terminal parent, so there is nothing to stamp at launch. The branch is removed for this path. The stamp mechanism itself stays for the cascade path — a Stop stamps children that exist at cascade time — so ADR-091 review R01's guarantee is preserved a fortiori: a launch racing a cascade now ends in **either** a refusal **or** a legitimately published child that the cascade stamps (or misses, exactly as today) — never an unstamped runnable child on a dead generation.

A refusal is returned as the D5 tool result, never as `ErrLifecycleTerminalImmutable`'s text.

### D3 — A completed root is still an open conversation; boot does not dispatch

**Amended 2026-10-06 — founder decision:** a root chat's final answer settles its producing execution as lifecycle `completed` (done). Its coarse chat status remains visible/usable, not archived or hidden. “Session exists” and “a turn is working” are different facts; leaving a finished chat `running` is not the current contract.

An already-completed root stays completed across boot until a legitimate new message/run. An already-stopped root stays stopped with its reason. Boot never dispatches from an old message. Interrupted steered work uses the ordinary `stopped(restart)` model of **The sub-agent control plane**::D8; the former `failed(interrupted)` prescription is historical. This does not add a deletion, archive or housekeeping policy.

Human messages continue a completed conversation under D4. New scheduled/heartbeat runs use the system principal for a new round from completed, but cannot revive a stopped root. The earlier blanket standing-root exemption and indefinite-running interpretation are replaced by these explicit state rules.

### D4 — The next message continues the session

The revival preserves the conversation and immutable completed/failed history. **Amended 2026-10-06:** done/failed -> next generation; landed stopped -> same generation with a fresh execution identity and atomic stop-note clear. An in-flight Stop rejects new admission until its owning execution settles. What changes is **who may trigger it, and which entry point runs it**.

**Roots: ordinary admission, one message -> one turn. Amended 2026-10-06.** A new human message into completed/failed or landed stopped revives the record before any tool runs. Completed/failed starts the next generation; landed stopped continues the same generation with a fresh execution identity. An in-flight Stop is refused visibly until its owning execution settles; do not clear its fence during the grace window. Roots use ordinary admission, not a parallel steered redispatch. New scheduled/heartbeat admissions may revive completed as the system principal, never stopped.

**Children: a follow-up to a terminal child continues it.** F890-1's month-later case: a parent's `follow_up`/`steer` to a child whose record is terminal (the boot sweep's `failed(interrupted)`, no Stop marker) must **revive and continue** it — generation + 1, `resumed_from`, redispatch as a steered turn. Today `pkg/tools/delegate_followup.go::executeSteer` refuses exactly that case (`session %s is terminal (%s) and cannot be steered`); that refusal becomes revive-and-redispatch. The child path already revives the Stop case through `ReviveStoppedSession`; the predicate widens to terminal-without-Stop **for the child path only**. `SteerCanceller.Revive` itself already handles terminal records — the two predicates are the only blockers, and both change.

**What may and may not revive — amended 2026-10-06.** A human message continues a completed or landed-stopped root through ordinary admission, once, before tools run. A new scheduled/heartbeat run may revive a **completed** root as the system principal, into a new round. It **never** revives a stopped root or clears an in-flight Stop. Boot replay, old queued re-entry, a tool call (`delegate`, `create_task`, `run_task`), and a hand-back to a stopped parent are not fresh resume actions. A hand-back stays unconsumed until the parent legitimately resumes; poll OR wake then delivers it once. The former blanket rule that every system wake runs without revival is superseded only by the explicitly authorized completed-root scheduled/heartbeat case.

**Who counts as human (MIN-004).** The revival predicate is: an inbound message whose channel is not `system` and that does not carry steer-wake metadata. In a `channel` root any participant's message counts for now — the per-participant authorisation the review wanted ("only people authorised to act for that conversation") is real but **out of scope by F890-3: tracked as #892**. This ADR ships the rule for the sessions the founder's rule names (chat, heartbeat, recurring, child) and #892 owns the group-chat authorisation.

**The record must agree with the screen (MIN-002).** The sweep reconciles `UnifiedMeta.Status` (`pkg/agent/boot_sweep.go::reconcileUnifiedMetaStatus`) and a terminal transition mirrors it to `interrupted` (`pkg/session/lifecycle_bridge.go::TransitionSession`). `SteerCanceller.Revive` does not touch `UnifiedMeta`, so a revived chat would show `interrupted` in the session list while its record says `running`. The revival path resets `UnifiedMeta.Status` to active **in the same revival** (wherever the AgentLoop wraps `Revive` — `SteerCanceller.Revive` has no `UnifiedStore`), so the session list agrees with the record. This is the record following the screen, not new chrome (D7).

**Audit (STRIDE repudiation, review's STRIDE table).** The revival line records the new generation and `resumed_from`; recording *who* revived is deferred to #892's authorisation work (`SteerCanceller.Revive` ignores its `steer.Principal` argument today).

### D5 — The tool result does not quote the store, on either error path

Both raw-error returns in `pkg/tools/delegate_run.go::launchAndDispatch` map to plain text: `delegate: launch: %v` **and** `delegate: dispatch: %v` (MAJ-002). The dispatch-side sentinels are `steer.ErrDispatchCancelled`, `steer.ErrTerminal` and `steer.ErrStaleGeneration` (`pkg/agent/steer_cancel.go::reserveDispatch`); the launch-side sentinel is D2's new `steer.ErrSteeringStopped`. `startTaskNowViaLauncher`'s `launch: %w` wrapper (`pkg/agent/task_executor.go::startTaskNowViaLauncher`) gets the same mapping. The result is written as **an instruction to the agent** (MIN-005), not a store sentence:

> Delegation is unavailable because this conversation is not active right now. Tell the user that sending a new message in this conversation resumes it, and that their request has not been started.

The result must not contain a session id, a generation number, `follow_up`, `Play`, or `resumed_from`. Under F890-1 it never suggests a new chat — that expectation is retired.

### D6 — A task from a stopped or terminal chat still runs

F890-2 answers this A+ (round 1's Q4 A covered only the terminal creator; the review's MAJ-006 added the stopped creator, and F890-2 explicitly covers both: "a task created from a stopped/finished chat still runs"). The gate reads the creator's record: `pkg/agent/task_executor.go::startTaskNowViaLauncher` reads the creating chat's lifecycle record; creator terminal **or** carrying a current-generation Stop marker → empty steering id → `launchOrdinaryRoot`. The task runs, as an ordinary root, with no revival of the chat as a side effect — a task start is not the human's newer instruction in that conversation.

OBS-001 is accepted: the round-1 clause "unless that launch is inside a turn which D4 just revived" is **dropped**. Once D4 has revived the chat, the record is non-terminal, so the plain rule ("terminal or stopped → ordinary root; otherwise steered") already produces the steered outcome with no "just revived" context flag for the executor to track.

### D7 — No new screen element; the record dies with the chat

F890-4 settles both defaults. No new "interrupted" chrome: the composer staying open **is** the revival; chats simply resume. The session-status value `interrupted` may still be written by today's Stop path (`websocket_cancel.go`); no renderer is added. And the lifecycle record is **deleted with the chat** (review Q6 A): when the chat is deleted, its root lifecycle file goes with it; housekeeping removes the rest. That bounds D3's exemption (MIN-003): a standing root's record is removed with the chat instead of living forever. Today there is no lifecycle delete path (no delete function exists in `pkg/session` or `pkg/gateway` — verified by grep sweep), so this default is a named work item for the spec: tie root-file removal to chat deletion.

## Comparison (historical options, 2026-09-26)

**Amended 2026-10-06:** the old standing-root “keeps running” and stopped-resume “new generation” entries in this comparison are no longer current instructions; D3/D4 now govern.

| | Defect 1 — chat looks alive | Defect 2 — raw store text | Defect 3 — no recovery | ADR-091 | After a restart | After Stop, then another message |
|---|---|---|---|---|---|---|
| (a) alone: skip the parent append, still publish the child | Unfixed, but delegation works, so the lie stops mattering for this tool | Gone on the success path | Delegation works, including on a generation the user stopped | Breaks D8: a live child on a dead generation. Pins the parent against prune | Works, by breaking the sweep's meaning | Works, by undoing Stop without a new generation |
| (b): allow a terminal parent to publish | Same as (a) | Same as (a) | Same as (a) | Weakens `persistLocked` or special-cases one caller. Same D8 break | Same as (a) | Same as (a) |
| (c) alone: revive when the user resumes | Fixed once the next human message has started the turn. Until that message, and again after the next restart, the record is terminal | Fixed after revival. Still needed as a backstop (D5) | Fixed for "the user kept typing". Not for a task whose creator was swept and nobody typed | Matches D8 if only a human message revives. The sweep then re-breaks the chat every boot | Broken again until the next message | Fixed, as a new generation |
| **(a)+(c), widened: skip the unchanged append, revive standing sessions on the next message or follow-up, exempt standing roots from the sweep. This ADR.** | The screen and the record agree after the next human turn (UnifiedMeta reset included), and a restart no longer terminalises the chat — or the heartbeat, or the recurring session | D5, on both error paths, even when the launch succeeds | Same chat can delegate; a follow-up reaches a month-old child; a stopped generation stays dead until a human message or a parent's follow-up | Lock and stamp kept. Terminal rule kept. Revival is the generation move D8 already describes, extended to terminal children | Standing roots stay `running`. Workers and work records still swept | New generation, then delegate works |
| (a)+(c) without the sweep exemption | Same as the row above until the next restart | D5 | Recurs every boot — and permanently for heartbeat/recurring, where no human message ever comes | Same | Broken again | Fixed |

(a) without D2 is not recommended. (b) is not recommended in any combination.

### Reachability

| Who | Today | After this ADR lands |
|---|---|---|
| A person in the same chat | Can type. Delegation fails with the store's own sentence (or, after a Stop, `delegate: dispatch:` raw text). Nothing on screen says why. | Can type. The turn revives the record first. Delegation starts the other agent. No new screen. |
| The chatting agent, `delegate` | `launchAndDispatch` fails before `Dispatch`, or `Dispatch` refuses with raw text. | Launch publishes the child. `Dispatch` admits it. |
| The chatting agent, `follow_up` to a month-old swept child | Refused: "terminal and cannot be steered". | The child is revived (new generation) and runs. F890-1's exact case. |
| A task whose creating chat was swept or stopped, started later from the board or a trigger | `startTaskNowViaLauncher` takes the steered path and fails. | Ordinary root (D6). The task runs. It does not wake the dead chat. |
| A heartbeat standing session that delegates once and survives a restart | Root swept to `failed(interrupted)`; every later delegation fails, permanently — no human message can ever revive it. | Root keeps `running` (D3). Delegation keeps working across restarts. |
| A steered worker left `running` at shutdown | Swept to `failed(interrupted)` and reported upward. | Unchanged — and `follow_up` from the parent revives it anyway. |

### Security and audit

| Topic | Effect |
|---|---|
| Stop | Still covers one generation: running work and queued re-entry. A human message (or a parent's follow-up to a child) is what mints the next one. A tool call, a task start, a queued wake, and a boot sweep do not. |
| Revival authorisation | Any participant's message revives a channel root for now; per-participant authorisation is #892 (F890-3). The revival line records the generation and `resumed_from`; recording the reviving principal is #892's work too. |
| Audit | Revival appends one legal line: new generation, `resumed_from` set. D1's skipped append avoids a second line that looks like a state change and is not. |
| `persistLocked` | Unchanged. No terminal generation is rewritten. |
| Prune | A child is never published under a stopped or terminal parent (D2), so `pruneTerminalOne` is never asked to retain a dead chat behind a live child. Standing roots are removed with their chat (D7, F890-4). |
| Permissions | No change to tool policy, grants, or who may call `delegate`. `inheritDelegatePermissions` still runs only after a committed steered launch. |
| Cross-user | No new read of another account's session. Revival is of the session the human just wrote to. |

### Test plan (historical expectations; amended 2026-10-06)

Current oracle: root final -> completed but still visible; stopped resumes same generation/fresh execution; completed resumes next generation; scheduled/heartbeat system-principal admission revives completed but never stopped; in-flight Stop refuses replacement until settlement; no boot dispatch. The older stopped-generation and sweep rows below are superseded where they conflict.

Suggested homes unchanged from round 1: `pkg/session` for the store, `pkg/agent` for sweep and revival, `pkg/tools` for the tool text.

**The RED test on `fix/890-red` must be re-pinned.** `pkg/agent/terminal_parent_delegation_issue890_test.go` (commit `b6e2ea669`) pins two tests. The characterization test `TestLaunch_TerminalParent_CurrentFailureIsImmutableRecord` carries its own comment — "The exact error assertion below flips once the ADR decides" — and now flips. The refusal-contract test `TestDelegateRun_TerminalParentRefusal_HidesInvariantAndAsksForNewChat` is **wrong under F890-1 in both name and assertions**: its `issue890RequiredRefusalText` (`conversation`, `can no longer start delegations`, `new chat`) encodes the "new chat" workaround the founder rejected. What it must assert instead:

1. **The happy path is the contract now.** A human message into a `failed(interrupted)` chat root starts a turn, the turn revives the record (generation 2, `resumed_from` = same id, state `running`), and a `delegate` call in that turn **succeeds** — the child lifecycle file exists.
2. The forbidden-text list stays and tightens: no `terminal record is immutable`, no `resumed_from`, no `follow_up/Play`, no `generation`, no session id — and **never** `new chat`.
3. The backstop refusal (when one fires with no revival in the call chain) says the D5 sentence; required phrases become e.g. `conversation` + `resumes`/`message`. The test's name changes accordingly.

| Case | Expected |
|---|---|
| Chat root `running`, no Stop. Delegate. | Child published. Parent file does not gain a line (D1: append only when `existed == false`). |
| Chat root `failed(interrupted)`. Human message starts a turn, then delegate. | Generation 2, `resumed_from` = same id, state `running`; `UnifiedMeta.Status` back to active; then the child exists. |
| Same record. Delegate with no new human message (a system-wake turn). | No child, no unified session, no goal. Tool text is D5's sentence; no session id, `generation`, `follow_up`, `Play`, `resumed_from`, `new chat`. |
| Web Stop on a chat root that has delegated. | Record **`running` + current Stop marker, not `cancelled`** — characterisation test (MAJ-001). |
| Chat root with a current Stop marker. Delegate, no new human message. | Typed refusal before any artifact: no child lifecycle file, no unified session, no goal record (MAJ-005). |
| Human message into a root chat whose Stop is current (grace-window path, `enqueueSteeringFromMessage`). | Exactly **one** turn and **exactly one** new generation; no steered-turn dispatch for a `ClassOrdinaryRoot` record (MAJ-003). |
| Stop lands after D4's revival, in the same turn; delegate follows. | Refusal — no child on the generation a newer Stop covers. |
| `follow_up`/`steer` to a terminal (swept) child, no Stop marker. | Revive-and-redispatch: generation + 1, `resumed_from`, child runs. The round-1 refusal "terminal and cannot be steered" is gone. |
| Boot sweep: one chat root `running`, one channel root `running`, one heartbeat root `running`, one scheduled root `running`, one steered child `running`, one task root `running`, one nil-origin root `running`. | Standing roots (chat, channel, heartbeat, scheduled, nil-origin) still `running`. Steered child and task root `failed(interrupted)` (MAJ-004; the nil-origin legacy case from the review's unasked questions). |
| Task with `OriginSessionID` of a terminal **or** stopped chat. | `launchOrdinaryRoot`. No write under the chat's lock; no revival of the chat (D6, MAJ-006). |
| Task with `OriginSessionID` of a non-terminal, non-stopped chat. | Steered child, unchanged. |
| `launchOrdinaryRoot` (no steering id). | Unchanged. |
| Concurrent Stop during launch. | Existing cascade guarantee holds: refusal, or the child is stamped — never an unstamped runnable child on a dead generation. |
| New scheduled/heartbeat run into a completed root. | System-principal revival into the next round; delegation is reachable. Separate stopped-root negative case: no revival, no turn, Stop holds. **Amended 2026-10-06.** |

UI: no new component (F890-4). Reachability check is a person (or the UAT lane) in the **same** chat after a restart, asking for a delegation, and a lifecycle file appearing for the target — plus the same from a heartbeat session across a restart. A unit test that only asserts an error string is not that check.

## Consequences

### Positive

- A restart no longer retires any standing session — chat, channel, heartbeat, recurring — and the store rule stops reaching the user through either error path.
- F890-1's exact case works: a parent can follow up to a month-old child; housekeeping, not a defect, is what eventually removes it.
- **Amended 2026-10-06:** Stop ends only this session's current turn, never its helpers or its generation. Resuming landed stopped retains its generation with a fresh execution; only committed done/failed starts the next generation.
- One less no-op append on every successful delegation: the parent JSONL does not grow by a duplicate snapshot.

### Negative

- **Amended 2026-10-06:** the old indefinitely-running standing-root trade is superseded. A finished root lands completed and stays visible; scheduled/heartbeat new-round revival requires completed, never stopped. Historical sweep behavior is not a delivery guarantee.
- Records already swept (two chat roots on the founder's machine today) stay terminal until a human message or a parent's follow-up revives them. D3 does not rewrite history.
- A task started later from a stopped or swept chat runs as an ordinary root: its result does not arrive as a handback into the old conversation — the same shape as a task whose creator session was deleted.

### Neutral

- `ErrLifecycleTerminalImmutable` remains the store's error for every other same-generation write onto a terminal tail. Only the launch refusal, the two tool-result paths, and the revival predicates change.
- The side panel, pill, and session list gain no field; the only visible change after revival is `UnifiedMeta.Status` returning to active (MIN-002).
- GitNexus impact was not run for this document (the worktree is not indexed — `gitnexus status` reports "Repository not indexed"); the caller sweep in Blast radius is the radius the implementer re-checks before editing, by re-indexing and running impact on `PublishChildUnderParentLock`, `SteerCanceller.Revive`, `bootSweep`, `enqueueSteeringFromMessage` and `executeSteer` (OBS-003).

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| (b) Terminal parent may publish children | Either weakens the terminal rule or starts a live child on a dead generation. Both conflict with ADR-091. Does not fix defect 1. |
| (a) without D2's refusal | Removes the error and also removes Stop's guarantee. |
| (c) without D3 | Correct recovery, then the next restart breaks the chat again — and permanently for heartbeat/recurring. |
| Round-1 D2's stamped-child reading | The MAJ-005 ambiguity resolved against it: a published-but-never-run child is an orphan record and an orphan unified session that the sweep cannot terminalise (terminal + current Stop is invalid) and prune will not retain; and the raw `delegate: dispatch:` error survives. Refusal leaves nothing behind. |
| Reviving a root via `ReviveStoppedSession` | It dispatches a steered turn (`pkg/agent/steering.go::ReviveStoppedSession` → `dispatchSteeredSession`): a second turn for one human message, or a failed registration. Roots revive Revive-only (MAJ-003). |
| Keeping `follow_up`'s terminal refusal | Contradicts F890-1 directly: the month-later follow-up is the founder's own example. |
| Sweeping heartbeat/recurring roots | Contradicts F890-2 by name, and the failure is permanent — no human message can ever revive a heartbeat (MAJ-004). |
| Exempting every `ClassOrdinaryRoot` from the sweep | That class includes a task root with no parent (`SteerRecordClassifier.Classify`). A task left `running` at boot would never be failed. The exemption is by origin kind, as D3 defines it. |
| Force a new chat and block the composer | Makes defect 1 honest and leaves defect 3 as a manual workaround — the founder rejected the workaround itself (F890-1). |
| Map the error text only | Fixes defect 2. Defects 1 and 3 remain; every restart still bricks the chat. |

## Affected components (historical plan, 2026-09-26)

**Amended 2026-10-06:** apply the current D3/D4 state rules to root completion/admission, scheduled/heartbeat system-principal revival, and stopped-parent result consumption. The old sweep-exemption and generation-bumping descriptions below are history, not work to reintroduce.

| Component | Change |
|---|---|
| `pkg/session/lifecycle.go::PublishChildUnderParentLock` | Skip the parent append when `existed == true`; append only the mint case. Do not relax `persistLocked`. Contract note: a callback that must change an existing parent is out of contract here. |
| `pkg/agent/steer_launcher.go::launchSteered` | Under the lock, before any artifact: refuse terminal or currently-stopped parent with `steer.ErrSteeringStopped`. Remove the stamp-at-launch branch. |
| `pkg/steer` (new sentinel) | `ErrSteeringStopped`. |
| `pkg/agent/boot_sweep.go::PlanEngine.bootSweep` | Skip standing roots per D3's kind list, nil-origin included. |
| `pkg/agent/steering.go::enqueueSteeringFromMessage` | Classify before reviving: `ClassOrdinaryRoot` → Revive-only and route the message to the ordinary inbound-turn path; `ClassSteered` → today's revive-and-redispatch. |
| `pkg/agent/steering.go::ReviveStoppedSession` | Child path only: the predicate widens to terminal-without-Stop (revive + redispatch). Never the root admission path. |
| `pkg/tools/delegate_followup.go::executeSteer` | Terminal child → revive-and-redispatch, replacing the `terminal and cannot be steered` refusal. |
| Root-turn admission path (ordinary inbound turn) | Human message into a terminal or stopped root revives Revive-only before tools. |
| Revival path | Reset `UnifiedMeta.Status` to active in the same revival (AgentLoop-level; `SteerCanceller.Revive` has no `UnifiedStore`). |
| `pkg/tools/delegate_run.go::launchAndDispatch` | Map both raw returns (`launch:` and `dispatch:`) to D5's text, same idea as the requested-skill branch. |
| `pkg/agent/task_executor.go::startTaskNowViaLauncher` | Terminal or stopped, unrevised creator → empty steering id → `launchOrdinaryRoot`. No "just revived" flag (OBS-001). |
| SPA | None (F890-4). |
| Contracts | None. No new wire field. |
| Implementation pre-step (OBS-003) | Re-index GitNexus (`node .gitnexus/run.cjs analyze`) and run impact on `PublishChildUnderParentLock`, `SteerCanceller.Revive`, `bootSweep`, `enqueueSteeringFromMessage`, `executeSteer` before editing. |

## Decisions

The founder answered round 1's four questions and the review's stopped-root additions in the grill interview (2026-09-25); the record is [ADR-093 founder decisions](./ADR-093-founder-decisions.md). What each decision settles here:

| ID | The founder's decision | What it decides in this ADR |
|---|---|---|
| F890-1 | "Sessions and child sessions are always resumable, like in Claude Code … the parent session could even send a follow-up to the child session" a month later | The headline rule. D4 (next message continues; follow-up revives a terminal child), with D1/D2 keeping terminal records immutable history. Retires the RED test's "new chat" expectation. |
| F890-2 | Confirms F890-1 exactly: a restart never makes a chat, **heartbeat or recurring** session unusable; Stop only ends the current turn; **a task created from a stopped/finished chat still runs**; the "running but stopped" state that blocks delegation is fixed | Q1 **B+** → D3 exempts standing roots (chat, channel, heartbeat, scheduled, nil-origin). Q2 **A** → D4. Q4 **A+** → D6 covers the stopped creator too. MAJ-001's running+Stop state is fixed by D4's revival (and D2's no-orphan refusal as backstop). |
| F890-3 | Only people authorised to act for that conversation may resume one the operator stopped; automatic wake-ups never do — **not part of this work** | MIN-004 is scoped: this ADR ships the channel-agnostic predicate (any participant, no system wakes); per-participant authorisation is **#892**. |
| F890-4 | No new "interrupted" screen element; a chat's lifecycle record is deleted with the chat, housekeeping removes the rest | Q3 **A** → D7. Review Q6 A → D7's record-deletion default, bounding D3's exemption (MIN-003). |

**Historical 2026-09-26 disposition (not current revival instructions; amended 2026-10-06).** The earlier rule settled the review as follows: the review's five unasked questions resolve from it — (1) a delegation in the Stop grace window is covered by the classify-first rule (the message revives the root Revive-only and re-routes to the ordinary path; the dying generation's queue is never fed); (2) stored child results are acknowledged by Revive's existing revival-state write (`pkg/agent/steer_cancel.go::WriteSteerRevivalState`, the "acknowledged at revival" mechanism of `pkg/agent/steer_audience.go`) — delivery stays with the existing terminal-report and boot-recovery paths, no new drain; (3) a system wake into a terminal root starts its turn without revival and a delegate in it meets the D2 backstop — intended; (4) nil-origin legacy roots are exempt by D3's shape rule — a task root cannot be nil-origin (`persistLocked` requires `origin.task_id` for kind `task`); (5) round 1's "parent file does not gain a line" row *is* the parent-line-count assertion, now stated as D1's own rule, and the existing `pkg/session` publication tests (rollback, mint) stay valid — qa-lead adds the explicit no-growth assertion.

## Review disposition

Every finding from [the grill review](./ADR-093-open-conversation-must-keep-delegation-review.md) (verdict REVISE; 6 MAJOR, 6 MINOR, 3 OBSERVATION), disposed once, in this one correction. Evidence cited is this worktree @ `044c89bea`, re-verified 2026-09-26.

| Finding | Disposition | Where it lands / evidence |
|---|---|---|
| MAJ-001 — web Stop leaves `running` + current Stop, not `cancelled` | **Fixed** | Context corrected with the three-state table; D4 revives from that state, D2's refusal there leaves no orphan child. Evidence: `pkg/gateway/websocket_cancel.go::handleCancel`, `pkg/session/lifecycle.go::persistLocked` (terminal + current Stop rejected). |
| MAJ-002 — dispatch error path stays raw | **Fixed** | D5 maps both raw returns; dispatch sentinels named. Evidence: `pkg/tools/delegate_run.go::launchAndDispatch`, `pkg/agent/steer_cancel.go::reserveDispatch`. |
| MAJ-003 — `ReviveStoppedSession` dispatches a steered turn | **Fixed** | D4: roots revive Revive-only via the ordinary admission path; `enqueueSteeringFromMessage` classifies first; one-turn/one-generation test pinned. Evidence: `pkg/agent/steering.go::ReviveStoppedSession`, `pkg/agent/steering.go::enqueueSteeringFromMessage`. |
| MAJ-004 — heartbeat / continue-scheduled roots swept | **Fixed** | D3 exempts standing kinds by name (F890-2 names them); permanent-failure argument recorded. Evidence: `pkg/session/unified.go` (`SessionTypeHeartbeat` "continue it"), `pkg/agent/steer_launcher.go::launchSteered` (origin copied from steerer meta). |
| MAJ-005 — refuse vs publish stamped child | **Fixed** | D2: refuse outright, typed `steer.ErrSteeringStopped`, before any artifact; stamp-at-launch branch removed. Evidence: `pkg/agent/steer_launcher.go::launchSteered` `stopStamp` branch (today's reading (b)). |
| MAJ-006 — stopped creator blocks trigger-started tasks | **Fixed** | D6 covers the stopped creator (F890-2 A+). Evidence: `pkg/agent/task_executor.go::startTaskNowViaLauncher`. |
| MIN-001 — how the store learns "unchanged parent" | **Fixed** | D1: append only when `existed == false`; changing an existing parent is out of contract. Evidence: `pkg/session/lifecycle.go::PublishChildUnderParentLock` (the `existed bool` already exists). |
| MIN-002 — revival does not restore `UnifiedMeta.Status` | **Fixed** | D4: reset to active in the same revival. Evidence: `pkg/session/lifecycle_bridge.go::TransitionSession` (terminal → `interrupted` mirror), `pkg/agent/boot_sweep.go::reconcileUnifiedMetaStatus`. |
| MIN-003 — root files never pruned | **Fixed** | D7/F890-4: lifecycle record deleted with the chat; housekeeping removes the rest; named spec work item (no delete path exists today). |
| MIN-004 — "human message" undefined; group-chat EoP | **Fixed (scoped)** | D4 defines the predicate (not `system`, no steer-wake metadata); per-participant authorisation → **#892** (F890-3). |
| MIN-005 — tool text is read by the model | **Fixed** | D5 gives the literal model-facing sentence as an instruction to the agent; forbidden-word test pinned. |
| MIN-006 — option C offered yet "rejected even if chosen" | **Fixed (dissolved)** | The Questions section is replaced by Decisions citing F890-n; no option tables remain; the follow-up-to-child rule is now decided by F890-1, which is stronger than round 1's "C is rejected". |
| OBS-001 — "just revived" clause redundant | **Fixed** | D6 drops the clause; the plain rule produces the steered outcome after revival. |
| OBS-002 — frequency claim rests on code, not logs | **Accepted** | The claim stays **Inferred from code**; the Context now states `pkg/logger` need not write to `gateway.log`, so log absence is not evidence of absence. |
| OBS-003 — impact analysis is a manual sweep | **Accepted** | Recorded as an implementation pre-step in Affected components: re-index GitNexus, run impact on the five named symbols before editing. |

The review's five unasked questions are answered inside the Decisions section, from the founder's rule — none required a new question to the founder.
