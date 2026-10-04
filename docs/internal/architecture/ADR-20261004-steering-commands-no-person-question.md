# ADR-20261004 — Steering commands: no person question

- **Status:** Draft (amendment to [ADR-20260928 — The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/a-u1-runtime-contracts-20261002/assets/ADR-20260928-sub-agent-control-plane@cd20cf8b.md); founder decisions of 2026-10-04)
- **Date:** 2026-10-04
- **Deciders:** Daniel Piatkowski (founder) — the seven locked decisions below are the founder's, as decided on 2026-10-04
- **Amends:** [ADR-20260928 — The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-investigations/a-u1-runtime-contracts-20261002/assets/ADR-20260928-sub-agent-control-plane@cd20cf8b.md) — source read at the read-only snapshot `omnipus-investigations/a-u1-runtime-contracts-20261002/assets/ADR-20260928-sub-agent-control-plane@cd20cf8b.md`. Where a locked decision below contradicts that ADR, **the founder's later (2026-10-04) decision wins** and this amendment says so explicitly.

## Context

ADR-20260928 built one special channel for "a helper needs a person": a helper with an `owner_required` question **pauses** (`needs_input`, waiting for answer); every parent relays the question one hop upward (`escalate`, a relay ledger, a shown question code at the root); the signed-in owner answers under the Who/When/Which/Once checks; and if no answer arrives within **24 hours** the asker fails visibly (`failed(owner_unreachable)`). A parent may withdraw the paused question by `redirect`/RESUME without an owner answer (F0929-R2-Q1=C); Stop and Stop all keep the question open with its original deadline (D1.7, F0929-R2-Q2=A).

On 2026-10-04 the founder removed that special mechanism. There is **no person-only question, no special pause, and no special message type**: a helper that has a question asks its parent with a normal message; the parent reaches a human through ordinary chat text. Steering stays with the four commands — Stop, Cancel (Stop all), Redirect, Resume — plus messages in both directions.

**Precedence.** Where a locked decision contradicts ADR-20260928, the founder's 2026-10-04 ruling supersedes it. ADR-20260928's other decisions are **not re-opened** by this amendment: the stop model (D2: fence, note, same-generation resume), receipts and the control ledger (D4), goal rules for stopped sessions (D6/D7b), the non-terminal Stop all cascade (D7), restart-as-automatic-stop for live runs (D8), the chat commands (D9), and rollback-out-of-scope (D10) stand as written, except where D1's removal takes away their object (the pending owner-question record) or where a consequence below names an adjustment.

### What this amendment replaces (decisions of ADR-20260928)

| Source ADR decision (by title + section) | Replaced by | Why |
|---|---|---|
| D1 — the `owner_required` **pause** (helper parks `needs_input` waiting for an owner) | Locked decision 6 | No helper pauses for a person; a question is an ordinary message to its parent |
| D1 — **relay-to-owner** (`escalate` verb, relay ledger, question codes, shown-entry anchors, the Who/When/Which/Once owner-answer acceptor) | Locked decisions 6–7 | Withdrawn with the pause; the acceptor is explicitly out of scope |
| D1.8 — the **24-hour question expiry** (`failed(owner_unreachable)` / `failed(answer_timeout)`) | Locked decision 6 | Removed with the pause; **no replacement expiry is designed** |
| D1.7 (F0929-R2-Q1=C) — parent **withdrawal of the paused question** by redirect/RESUME (superseded closures, withdrawal audit entry, "unanswered" notice) | Locked decision 3 | There is no pause left to withdraw; redirect simply replaces the turn |
| D3 — a **steer into a stopped child is queued and does not itself resume** it | Locked decision 5 | A message into a stopped helper **resumes** it, the same way a chat message does |
| D2 stop row and D7 Stop-all row for a "waiting for answer" child (retain the open question, original deadline, relay chain) | Locked decisions 1–2 | The pending owner-question record no longer exists; a stopped helper keeps its ordinary conversation, like any stopped session |

## Decision

The founder's locked decisions of 2026-10-04, recorded in his words:

1. **Stop — one helper, paused, resumable.** Stop shows one helper paused, not failed. Work so far stays. Helpers under it keep going. The goal is not cleared. Stopping an already-stopped helper says it is already stopped and changes nothing. A restart leaves an already-stopped helper stopped, keeps its stop reason, and does not send a fatal interrupted or timeout message. A stop notice rings until the parent takes it, including a stop with no saved control history, and including after a restart. A second ring does not make the parent do the work twice. There is no periodic timer and no second notice format.

2. **Cancel (Stop all) — that helper and every helper under it.** Cancel, also called Stop all, pauses that helper and every helper under it. They show as stopped, not failed. Parents see a stop notice, not a failure.

3. **Redirect — replace the current turn.** Redirect replaces the current turn with the new instruction. It does not withdraw a special person-question, because that pause no longer exists.

4. **Resume — continue, or start a next round.** Resume continues a stopped helper on the same conversation. A finished helper starts a next round.

5. **Messages are a steering mechanism too.** Messages both ways are a steering mechanism besides Stop, Cancel, Redirect and Resume. A message that arrives mid-turn does not stop the turn, the same way chat works. If the helper has ended a turn or was stopped, a message resumes it the same way a chat message does.

6. **No person-only question.** There is no special person-only question, no special pause, and no special message type for it. When a helper has a question it asks its parent with a normal message. The parent can redirect that to a human by ordinary text, for example "please ask the operator". That text is not a pause and not a stop. The 24-hour person-question expiry is removed with the pause. Do not design a replacement expiry.

7. **A web message and its sender record save together, or neither is kept.** A web message and its server-side record of who sent it are saved together, or neither is kept. If the save fails, the person sees an error and the turn does not start. This record is not an owner-answer check. The owner-answer Who/When/Which acceptor is out of scope because the special person-question is gone.

## Consequences

### Positive

- One mechanism for questions and steering: ordinary messages in both directions. No second message type, no relay chain, no question codes, no expiry bookkeeping to keep consistent across stops and restarts.
- Stop outcomes stay uniform everywhere: paused-not-failed, work preserved, goals untouched, subtree untouched by a single Stop. A helper never "fails" because a person was unavailable.
- The ring-until-taken stop notice closes the "helper stopped and nobody noticed" hole, and a repeat ring cannot make the parent redo the work.

### Negative

- An agent chain that asks a question and has no human in the loop has **no forced bound on waiting**: the 24-hour visible failure is gone and no replacement expiry is designed (locked decision 6). The ordinary bounds that remain are the parent agent acting on the message and the session's own lifetime budget, which stops any working turn as usual.
- The Who/When/Which owner-answer guarantee disappears with the acceptor: nothing in the message path distinguishes an owner's text from an agent's text. The sender record of locked decision 7 is a persistence guarantee, **not** an authorization check.
- The web intake takes on an atomicity duty: message and sender record must commit together or neither survives (adjustment A4). A torn save is a visible error to the person, never a provenance-less turn.

### Withdrawn ADR-20260928 behavior

- The `owner_required` pause (D1): a helper no longer enters a waiting-for-answer park for a person.
- Relay-to-owner (D1): `escalate`, the relay ledger, question codes, shown-entry anchors, and the Who/When/Which/Once answer acceptor are withdrawn; the acceptor is out of scope (locked decision 7).
- The 24-hour question expiry (D1.8): `failed(owner_unreachable)` / `failed(answer_timeout)` are gone; no replacement expiry.
- Redirect/RESUME withdrawal of the paused question (D1.7, F0929-R2-Q1=C): superseded-question closures, the withdrawal audit entry and the "unanswered" notice lose their object; redirect now only replaces the turn (locked decision 3).
- Steer-on-stopped-does-not-resume (D3): replaced — a message into a stopped helper resumes it like a chat message (locked decision 5).
- The D2/D7 rows that kept an owner question open across Stop/Stop all with its original deadline: the record they kept no longer exists; stopped helpers keep their ordinary conversation history like any stopped session.

### Code that already contradicts this amendment (adjustments to make; no code is written here)

Verified first-hand in this worktree (`work/a-steering-design-20261004`) on 2026-10-04; team-lead's verification matches.

- **A1 — Boot marks an already-stopped helper failed.** `pkg/agent/boot_sweep.go::SteerBootRecovery.failInterrupted` has an "already-stopped arm" that sets `LifecycleFailed` with reason `interrupted` (or `timeout`) and drives a fatal upward message. Locked decision 1 requires the opposite: a restart leaves an already-stopped helper stopped, keeps its retained stop reason, and sends no fatal interrupted/timeout message.
- **A2 — A fence-less stop notice rings once.** The re-ring-until-taken loop (`pkg/agent/stopped_notice.go::deliverLandedStopNotices` → `::deliverLandedStopNotice` → `::stopNoticeTaken`) covers only transitions discovered from the control ledger's landed history. A fence-less landing's transition is synthesized once at the landing and never written to the ledger (`pkg/agent/stopped_notice.go::stoppedTransitionFromLandedNote`; its single delivery call site is in `pkg/agent/steer_completion.go`), so it cannot re-ring on later delivery passes. Locked decision 1 requires the fence-less notice to ring until taken, on the same delivery passes and the same single notice format, with no periodic timer.
- **A3 — Question expiry runs at startup for the old person-question.** The boot consumer enforces the 24-hour park limit for `needs_input` **and** stopped askers (the expiry arms in `pkg/agent/boot_sweep.go` calling `expireQuestion`, including the superseded-relay closure landed for it). With the pause removed, this expiry machinery must be removed; no replacement expiry (locked decision 6).
- **A4 — Provenance can leave a transcript line without the sender record.** `pkg/session/message_provenance.go::AppendTranscriptWithProvenance` documents a torn-write window: if the provenance write fails after the transcript line landed, the error rejects the message (the person sees an error and no turn starts — that half matches locked decision 7), **but** the already-written transcript line stays on disk without its sender record, violating "or neither is kept". The save must be made all-or-nothing.
- **A5 — Delegate has no redirect or resume action.** The delegate action set in `pkg/tools/delegate.go` is `inbox, inbox_ack, steer, respond, cancel, clear_goal, follow_up, peek`. Locked decisions 3–4 require `redirect` and `resume` actions; add them contract-first (schemas → regeneration → handlers, Hard Constraint #8).
- **A6 — `follow_up` and `cancel` still use the old names.** ADR-20260928's D3 rename (`follow_up` → `resume`, `cancel` → `stop_all`) has not landed; the delegate enum above still carries the old names. Locked decisions 2 and 4 name the verbs Cancel/Stop-all and Resume; complete the rename in the same contract change, with no alias path (greenfield).

### Not changed by this amendment

ADR-20260928's stop mechanics (fence + separate stop note), receipts and control ledger, the rule that no stop ends a goal, the non-terminal Stop all cascade downward only, restart-as-automatic-stop for live runs, the chat commands `/stop` and `/stop-redirect`, and rollback-out-of-scope all stand. This amendment removes the person-question layer and requires the two notice/expiry adjustments named above; it adds no behavior beyond the founder's locked decisions.
