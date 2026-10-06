# ADR-20261004 — Steering commands: no person question

## Amended 2026-10-06 — founder decision

Later founder decisions narrow the preserved control-plane requirements. Use [The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-adr-spec-sync-20261006/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md)::Amended 2026-10-06 as the current decision record. This is a founder-requested synchronization, not a second grill/correction round.

| Boundary | Current rule / correction |
|---|---|
| Chat Stop, Stop all and redirect | **Amended 2026-10-06 (founder):** Stop click 1 / Esc 1 / `/stop` stops **this chat's current turn only** and opens a **3 s window**. Stop click 2 / Esc 2 within it stops **this chat and its whole helper tree**; `/cancel` does that immediately. **No separate Stop-all button or offer.** `/stop-redirect <instruction>` stops this chat's turn and continues **this chat** with the instruction, in **any root or helper chat**. Same on web, CLI and channels. Plain Stop leaves background shells; Stop all / cancel kills them. Agent delegate `stop` / `stop_all` is unchanged. Human and agent callers still share `AgentLoop.StopSession`: polite now, forced at 3 s, detached 3 s after force; no public `hard` or `cancel_grace`. Authority: [Stop controls — founder decision 2026-10-06](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/STOP-CONTROLS-DECISION-20261006.md)::Action / Behaviour. |
| Finishing input (R1) | A provider answer is not completion. Input accepted **before the terminal lifecycle/outbox commit** continues the same generation. Input accepted **after that commit** starts the next round; failed publication does not undo the committed outcome or discard its owed final. |
| Delivery (D4) | `delivered` requires exact text/identity durably in the recipient transcript. A denied write leaves it queued and returns a visible error. No model-obedience claim. |
| Upward result | Final consumed **once: poll OR hand-back wake, never both**. A stopped parent holds it unconsumed until resumed. Stop all supersedes queued hand-back wakes for the stopped tree, not saved result/history. |
| Root completion | Finished root chat -> `completed` (done), **not archive/hide**. Human continuation works. A scheduled/heartbeat run may revive completed as the system principal, into a new round, **never stopped**. Old-message boot replay is not such a run. |
| Goal continuation | `/goal <intent>` activates; `set_goal` refuses beforehand (Work-First FR-005). Keeper reminders to helpers use a steered system wake. Refused goal-tail saves are visible to the parent; the tail claims only its producing execution. |
| Deferred scope | One route/record for every sender (transcript as record), rebuild of waiting messages after restart, 256 KiB aggregate cap, ledger compaction, and live failed-descendant-stop retry are **deferred to #1198 (founder 2026-10-06)**. The earlier D4/D6 rulings required these; the later founder decision defers them. C6 must not be read as keeping their release gate. |

F6 is the planned post-landing simplification review in [FOUNDER-OPEN-ITEMS.md](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/FOUNDER-OPEN-ITEMS.md)::F6; it is not another correction round or an already-approved removal. The original locked decisions and historical code pins below keep their dates.

- **Status:** Draft (amendment to [ADR-20260928 — The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-adr-spec-sync-20261006/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md); founder decisions of 2026-10-04)
- **Date:** 2026-10-04
- **Deciders:** Daniel Piatkowski (founder) — the seven locked decisions below are the founder's, as decided on 2026-10-04
- **Amends:** [ADR-20260928 — The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-adr-spec-sync-20261006/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md) — source read at the read-only snapshot `omnipus-investigations/a-u1-runtime-contracts-20261002/assets/ADR-20260928-sub-agent-control-plane@cd20cf8b.md`. Where a locked decision below contradicts that ADR, **the founder's later (2026-10-04) decision wins** and this amendment says so explicitly.
- **Correction (2026-10-04):** this is the **one correction** after this ADR's single grill round ([review](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/internal/architecture/ADR-20261004-steering-commands-no-person-question-review.md), verdict REVISE, no founder questions). The answers to that review's C1–C6 are the "Correction (2026-10-04)" section below. **No second grill runs on this revision**; any blocking finding still open after this correction escalates to the founder. The seven locked decisions are not reopened.

## Context

ADR-20260928 built one special channel for "a helper needs a person": a helper with an `owner_required` question **pauses** (`needs_input`, waiting for answer); every parent relays the question one hop upward (`escalate`, a relay ledger, a shown question code at the root); the signed-in owner answers under the Who/When/Which/Once checks; and if no answer arrives within **24 hours** the asker fails visibly (`failed(owner_unreachable)`). A parent may withdraw the paused question by `redirect`/RESUME without an owner answer (F0929-R2-Q1=C); Stop and Stop all keep the question open with its original deadline (D1.7, F0929-R2-Q2=A).

On 2026-10-04 the founder removed that special mechanism. There is **no person-only question, no special pause, and no special message type**: a helper that has a question asks its parent with a normal message; the parent reaches a human through ordinary chat text. Steering stays with the four commands — Stop, Cancel (Stop all), Redirect, Resume — plus messages in both directions.

**Precedence.** Where a locked decision contradicts ADR-20260928, the founder's 2026-10-04 ruling supersedes it. ADR-20260928's other decisions are **not re-opened** by this amendment: the stop model (D2: fence, note, same-generation resume), receipts and the control ledger (D4), goal rules for stopped sessions (D6/D7b), the non-terminal Stop all cascade (D7), restart-as-automatic-stop for live runs (D8), the chat commands (D9, with the 2026-10-06 chat-control correction above), and rollback-out-of-scope (D10) stand as written, except where D1's removal takes away their object (the pending owner-question record) or where a consequence below names an adjustment.

### What this amendment replaces (decisions of ADR-20260928)

| Source ADR decision (by title + section) | Replaced by | Why |
|---|---|---|
| D1 — the `owner_required` **pause** (helper parks `needs_input` waiting for an owner) | Locked decision 6 | No helper pauses for a person; a question is an ordinary message to its parent |
| D1 — **relay-to-owner** (`escalate` verb, relay ledger, question codes, shown-entry anchors, the Who/When/Which/Once owner-answer acceptor) | Locked decisions 6–7 | Withdrawn with the pause; the acceptor is explicitly out of scope |
| D1.8 — the **24-hour question expiry** (`failed(owner_unreachable)` / `failed(answer_timeout)`) | Locked decision 6 | Removed with the pause; **no replacement expiry is designed** |
| D1.7 (F0929-R2-Q1=C) — parent **withdrawal of the paused question** by redirect/RESUME (superseded closures, withdrawal audit entry, "unanswered" notice) | Locked decision 3 | There is no pause left to withdraw; redirect simply replaces the turn |
| D3 — a **steer into a stopped child is queued and does not itself resume** it | Locked decision 5 | A message into a stopped helper **resumes** it, the same way a chat message does |
| D3 — "`steer` on a done/failed child returns 'Session \<id\> has already finished — nothing was started'" | Locked decision 5, integrated in Correction C1 | A message into a helper whose turn has ended (done or failed) **starts its next round**, the same way a chat message does |
| D5 — ordering cells that park a message behind a later resume ("steer waits for the resume") | Locked decision 5, integrated in Correction C1 | The C1 table governs message effects; the control-precedence rows between Stop/Redirect/Stop all and pending controls are preserved |
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

## Correction (2026-10-04) — answers to the grill (C1–C6)

This section is the ADR's **one correction** after its single grill round ([review](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/internal/architecture/ADR-20261004-steering-commands-no-person-question-review.md), verdict REVISE, "Questions for the founder: none"). It makes the locked decisions' integration unambiguous; it adds no product behavior beyond them, and it does not reopen them. Code evidence was read first-hand in this worktree, with the implementation branch `work/a-lane-combine-notice-epoch-20261004` pinned at `ac7848fb8` and the fence-less re-ring read at `0132e8000`.

### C1 — Normative message/state table (supersedes the contradicting D3/D5/D6 sentences)

"A message" below means a **steering message**: text sent deliberately to a helper as its input — by its parent through the delegate message path (`steer`), by a parent's `respond` reply, or by the signed-in human typing in that session's chat. Two other deliveries are **not** steering messages and never resume anything: a **worker's upward message** (a descendant reporting into its stopped parent — stored per D6, preserved) and a **runtime notice** (a stop-notice ring, a delivery receipt). This distinction is forced by the locked decisions themselves: if every delivered item resumed a stopped helper, Stop would not be durable (locked decision 1) and boot would start runs from stored messages (D8, preserved).

| Recipient state | Steering message arrives | Worker's upward message arrives | Stop-notice ring / runtime notice |
|---|---|---|---|
| `working`, mid-turn | Delivered into the current turn as ordinary steering input; **the turn is not stopped** (locked decision 5) | Delivered/stored under the ordinary upward rules (source D5, preserved) | Never stops or resumes anything |
| `stopped` | **Resumes the helper** on the same conversation, same generation — the same way the Resume command does: ledger intent first, then the atomic note-clear/queued mutation installing a **fresh execution identity** (source D2, preserved); the message is injected as the newest instruction, never a transcript replay (F1011-Q8, preserved) | **Stored** in the helper's durable inbox; **no resume**; read when the helper next runs (source D6, preserved) | Never resumes; a ring is the delivery of a notice, not a steering act |
| `done` / `completed` | Starts the helper's **next round** on the same conversation with the message as input; terminal commit, not provider text, is the boundary | Deliver once under the ordinary upward rules; never duplicate a final through poll and wake | Never resumes |
| `failed` | Same as `done`: next round with the message as input | Preserve upward delivery. Initial descendant Stop results must distinguish landed/pending/incomplete; live retry is deferred to #1198 (founder 2026-10-06) | Never resumes |
| any | A **retry or replay of an already-delivered message is not a new resume** — message-identity dedup applies and changes nothing | — | — |
| boot | **Boot never starts a run from an old message** (source D8.5, kept verbatim). A message arriving after boot is a fresh explicit post-boot action | — | — |

Superseded sentences (source ADR by title, read at the pinned snapshot):

- D3: "`steer` on a **stopped** child … is accepted as `queued` and delivered after the resume instruction (D5); it does not itself resume." → superseded: the message resumes.
- D3: "`steer` on a done/failed child returns 'Session \<id\> has already finished — nothing was started. Use RESUME to give it new work.'" → superseded for messages: the message starts the next round.
- D5: the ordering cells that held a message behind a later resume ("steer waits for the resume") → superseded for messages by the table above. The control-precedence rows between Stop/Redirect/Stop all and pending controls are **preserved**.
- D6: "descendants' upward messages are stored and wake it only on resume" → **preserved** (it is the worker-upward column above and does not contradict locked decision 5).
- D8.5: boot never dispatches, nothing resumes automatically → **preserved verbatim**.

### C2 — Removal boundary: the helper person-question pause and the root clarification cards are different mechanisms

**Removed** (locked decision 6): the helper person-question pause — the `owner_required` park, the `escalate` relay (verb, relay ledger, question codes, shown-entry anchors), the 24-hour expiry (`failed(owner_unreachable)` / `failed(answer_timeout)`, including the `expireQuestion` arms in `pkg/agent/boot_sweep.go`), and `needs_input` as a lifecycle state that existed only for that pause. Verified at the pinned snapshot: `pkg/tools/message_parent.go::parkNeedsInput` — gated on the tool's `wait`/`authority` parameters — is the only production writer of `LifecycleNeedsInput`; deleting the pause leaves that state with no writer, so it is deleted outright (greenfield, no upgrade path, no shim).

**A helper question to its parent is an ordinary message and does not park the helper.** The named surfaces are disposed, not left dangling:

| Surface | Disposition |
|---|---|
| `message_parent` `wait` / `authority` parameters (`owner_required` / `self_ok`) | Deleted with the pause; the text is delivered upward as an ordinary message (upward caps per source D5, preserved — unchanged) |
| `message_parent` park machinery (`ParksTurn`, `parkNeedsInput`, `NeedsInputTTL`, 24-hour expiry) | Deleted; no replacement expiry is designed (locked decision 6) |
| `needs_input` (`LifecycleNeedsInput`) | Deleted outright — its only writer was the pause |
| `respond` | Stays the parent's reply-to-message action. The owner-answer acceptor checks (`pkg/tools/delegate_park.go::verifyQuestionAuthority`, Who/When/Which/Once) are deleted with the acceptor (locked decision 7: out of scope); it delivers its text downward as an ordinary message, and C1's table applies to the recipient |
| Delegated producer inside the card package (`pkg/askuser/registry.go::relaySteeredQuestions`) | Remapped: it currently emits the retired shape (`wait: true`, `authority: self_ok`) for a delegated child; it delivers that content as an ordinary upward message with no wait, no authority, no park |
| `SessionMessageQuestion.yaml::wait` / `::authority` and `ControlReceipt.yaml::verb`'s `escalate` | Deleted at the contracts layer by backend-lead with regeneration (Hard Constraint #8), no alias path (greenfield, same rule as A6) |

**The root user's clarification cards are not the same mechanism and are not removed.** Verified: the two are separated by construction — `pkg/askuser/registry.go::Registry.CreatePending` refuses delegated children (`ErrDelegatedChild`: "a delegated session asks its parent via message_parent…") and only then relays upward; the card path itself (pending set, card UI, answer-dispatch resume) is owner-session-only and serves the founder's own conversation with the root agent. Locked decision 6 removes the helper's person-question; it says nothing about the root conversation's clarification cards, and this amendment does not touch them. No blocked note is needed: the mechanisms are distinct, verified, not shared.

**Security boundary, restated precisely** (correcting the Negative bullet below): removing the acceptor removes **owner-only question-answer authorization**; it does not remove distinction as such. Connection authentication, ordinary session/ancestor steering authority, the untrusted-origin label on agent-authored content, and tool approval policy all stay as they are (review OBS-001).

### C3 — Durable stop identity for every actual stop (closes the residual MAJ-003 hole)

The fence-less re-ring is implemented at `0132e8000` (`84fe206b1`): the notice re-rings on every delivery pass **while the stop note remains**. Two holes remain there — Resume clears the active note, so an untaken notice can be lost, and two fence-less stops in one generation share the generation-derived identity. One rule answers both:

**Every actual stop transition — fenced or fence-less — is appended to the existing per-session control ledger (source D4, preserved) when it lands.** The ledger is the owning store; no new store, no new notice format, no periodic timer, no new control verb.

- **Durable creation boundary:** a fence-less landing (`pkg/agent/steer_completion_commit.go::landSteeredStopLocked`) appends the transition to the ledger in the same landing sequence that writes the stop note. The record carries cause/actor/time as the note does and identifies the landing execution (the completion claim's execution identity) — it **fabricates no accepted Stop control**; the landed-transition writer accepts that execution identity for fence-less stops where it today requires an accepted control id. This implements locked decision 1's own words: the notice rings "including a stop with no saved control history."
- **Identity:** the existing notice id space `(parent_id, child_id, child_generation, stop_seq)` is retained (`pkg/agent/stopped_notice.go::stoppedChildNoticeID`). The fence-less stop's `stop_seq` stops being the generation stand-in (`Seq: uint64(cur.Generation)` at `landSteeredStopLocked`, verified at `0132e8000`): it takes the next monotonic stop sequence under the child's lock, the same discipline the ledger already applies to fenced stops. **Two stops in one generation therefore never share one identity**, and taking the first notice acknowledges only its own id — the second stop's distinct id stays untaken and keeps ringing.
- **Discovery after Resume:** delivery passes discover untaken transitions from ledger history (`pkg/agent/stopped_notice.go::deliverLandedStopNotices` already reads it). Resume's atomic note-clear (source D2) cannot remove a ledgered transition, so **an untaken notice survives Resume clearing the active note**, across restart as well. The note-derived fallback `::stoppedTransitionFromLandedNote` exists only because fence-less transitions were never ledgered; it is retired once the ledger write is in place.
- **Ack/retirement rule:** unchanged — the existing taken rule (`::stopNoticeTaken` by notice id); each untaken id re-rings on the existing event-driven delivery passes (landing, boot pass, later delivery passes). No periodic timer, no second notice format, no new control verb (locked decision 1).

### C4 — Acceptance boundary for "message and sender record together, or neither"

The founder's rule (locked decision 7) is **not weakened**. The accepted-pair boundary: a message is accepted — echoed, acknowledged, or admitted to a turn — **only when both** the transcript line and its sender record are durably written.

- **Returned save error (either write fails with an error):** implemented at `ac7848fb8` via `775250d9f` — `pkg/session/transcript_indexed.go::UnifiedStore.appendTranscript` rolls the transcript line back (`pkg/session/message_provenance.go::rollbackTranscriptAppend`); the person sees an error and no turn starts. Nothing to redo.
- **Abrupt exit between the two writes — one recovery rule:** **on reopen, a transcript line with no sender record from that paired save is not shown and not admitted.** Every reader of the store — transcript render, replay, model-context assembly — skips such a line; the person's client sees neither echo nor a started turn; a retry creates a fresh pair. The line's bytes may remain on disk as inert residue; from every reader's perspective "neither is kept" holds, which is the rule's meaning. This is the authoritative acceptance boundary: no echo, no acknowledgment and no turn admission may precede the completed pair.
- **Alternative considered and not selected:** a single combined internal record (one write instead of two). Rejected: it changes the transcript storage layout and the append-ordinal machinery for no additional user-visible guarantee — the reopen-hide rule already delivers the all-or-nothing behavior locked decision 7 specifies, and the decision fixes behavior, not storage layout.

### C5 — Historical notice replay and current-run recovery are independent obligations

Both are required at boot, and neither gates the other (source D8.1's pass order, restated because today's code couples them):

- **Current-run recovery (pass one):** stop every interrupted `queued`/`running` steered record (cause `restart`), persist its notice, require a fresh explicit resume. No turn starts (source D8, kept).
- **Historical replay (pass two):** re-ring every untaken stop notice from ledger history (C3), whether or not that record's current run was just stopped.

**An old notice — replayed successfully, replay-failed, or not yet replayed — must never skip the current-run stop.** The coupling found in review (`pkg/agent/stopped_notice.go::SteerBootRecovery.recoverStoppedChildNotice` returning `pending || replayErr != nil` as "handled" for a running/queued record, and `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered` returning on that boolean before current-run recovery, verified at `ac7848fb8`) is superseded: a replay outcome is not current-run recovery. A replay failure stays visibly pending and retried; it never suppresses the stop of the interrupted run and never authorizes dispatch. The current run is stopped before any parent wake can publish against half-recovered state. Boot still does not start a run.

Required regression (into qa-lead's pack with the C6 list): stop → untaken notice → a fresh message resumes the helper → crash → boot must land both obligations — the resumed run stopped with no automatic run, and the historical notice still owed and re-ringing — asserting both the notice identity and the stopped state, including when replay fails.

### C6 — Source-test dispositions and user-document TODOs

Source acceptance tests (ADR-20260928, "Required acceptance tests"):

| Source test | Disposition |
|---|---|
| T1, T2, T3 | **Superseded** — the owner-answer acceptor (Who/When/Which/Once) is withdrawn with the person-question (locked decisions 6–7) |
| T8 | **Superseded** — the 24-hour question expiry is removed; no replacement expiry (locked decision 6) |
| T14 | **Superseded** — no open owner question survives Stop/Stop all; no owner-answer path |
| T25 | **Superseded** — there is no `owner_required` question to withdraw; redirect simply replaces the turn (locked decision 3) |
| T19 | **Stays** for its lifetime-timeout half (`stopped(timeout)`, fresh budget on resume); **superseded** for its 24-hour-expiry comparison clause |
| T22 | **Current:** existing body/rate/item caps and durable injection receipts. **deferred to #1198 (founder 2026-10-06):** aggregate 256 KiB cap, waiting-message restart reconstruction and ledger compaction. **Superseded:** open-owner-question clauses. |
| T11 | **Stays** — final-versus-stop race, committed outbox, delivery-once (stop model and receipts preserved) |
| T27 | **Stays** — same-generation stale-effect protection |
| T20, and the goal clauses of T6/T19/T23 | **Stays** — goal preserved: no stop of any kind ends a goal (locked decision 1) |
| T21 | **Stays** — single Stop does not cascade; Stop all goes downward only (locked decisions 1–2) |
| T4, T18, T6 | **Current:** no boot dispatch; an already-stopped helper stays stopped. **T18 waiting-message reconstruction/release: deferred to #1198 (founder 2026-10-06).** Accepted Stop finishing and saved notice/final recovery are not waived. |
| T15, T16 | **Stays** — stop notices and the stopped-node completion frontier (locked decision 1 refines delivery, not the frontier) |
| T17, T23, T26 | **Stays** — plan/task session-goal reuse, failed-parent descendant stops, state display (minus any person-question row) |

New and adjusted tests this correction requires (qa-lead RED; expected values come from the seven locked decisions, not from current code): the C1 message-state matrix (mid-turn no-stop; stopped/done/failed message-resume through the real entry paths; ring-does-not-resume; replay-is-not-a-resume; no boot dispatch); the C3 notice set (two fence-less stops in one generation, take-first/deliver-second; untaken notice survives Resume and restart; a failed ring stays visibly pending); the C5 boot-independence case; the C4 intake cases (the implemented A4 rollback preserved; abrupt-exit reopen-hide exercised at the real write cuts against a **reopened** store — a same-lock assertion is not crash evidence).

**User-document TODOs** (the implementing lead drafts these updates in the implementation diff; docs-verifier audits them against integrated behavior; the source ADR's build-plan unit 4 and its Impact guide row — which instructed owner-only-answer and 24-hour-expiry documentation — are superseded by these TODOs):

- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/docs-stop-controls-20261006/docs/agents.md` — "Workers and delegation": (1) **Amended 2026-10-06 (founder):** first Stop/Esc/`/stop` stops this chat's turn only and opens 3 s; second Stop/Esc within it is the tree-stop confirmation, `/cancel` does it immediately; no extra Stop-all control; `/stop-redirect` stops and continues the current chat, root or helper; plain Stop leaves background shells, Stop all / cancel kills them; (2) a **message resumes a stopped or finished helper** (stopped: continues the same conversation; finished: starts a next round); (3) **no person-only wait and no 24-hour expiry** — a helper asks its parent with an ordinary message and the parent reaches a human by ordinary chat text; (4) the **final verb names** (Stop, Stop all, Redirect, Resume) after the `follow_up`→`resume` and `cancel`→`stop_all` rename.
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/docs-stop-controls-20261006/docs/using-omnipus-ui.md` — "The chat area" / "Stop and redirect commands": replace helper-only redirect and root refusal with current-chat redirect; document the 3 s first/second Stop/Esc sequence and immediate `/cancel`; no separate Stop-all control.
- `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/a-steering-design-20261004/docs/reference/built-in-tools.md` — "General" (`delegate`, `message_parent`, `AskUserQuestion` rows): (1) the delegate action list under the **final verb names**, including the added `redirect` and `resume` actions and no alias path; (2) `message_parent` as an ordinary upward message — no `wait`/`authority`, no park, no expiry; (3) `AskUserQuestion` unchanged for the owner's own session; a delegated session asks its parent with an ordinary message.

## Consequences

### Positive

- One mechanism for questions and steering: ordinary messages in both directions. No second message type, no relay chain, no question codes, no expiry bookkeeping to keep consistent across stops and restarts.
- Stop outcomes stay uniform everywhere: paused-not-failed, work preserved, goals untouched, subtree untouched by a single Stop. A helper never "fails" because a person was unavailable.
- The ring-until-taken stop notice closes the "helper stopped and nobody noticed" hole, and a repeat ring cannot make the parent redo the work.

### Negative

- An agent chain that asks a question and has no human in the loop has **no forced bound on waiting**: the 24-hour visible failure is gone and no replacement expiry is designed (locked decision 6). The ordinary bounds that remain are the parent agent acting on the message and the session's own lifetime budget, which stops any working turn as usual.
- The Who/When/Which owner-answer guarantee disappears with the acceptor: nothing in the message path grants **owner-only question-answer authorization** any more. What stays: connection authentication, ordinary session/ancestor steering authority, the untrusted-origin label on agent-authored content, and tool approval policy (Correction C2). The sender record of locked decision 7 is a persistence guarantee, **not** an authorization check.
- The web intake takes on an atomicity duty: message and sender record must commit together or neither survives (adjustment A4). A torn save is a visible error to the person, never a provenance-less turn.

### Withdrawn ADR-20260928 behavior

- The `owner_required` pause (D1): a helper no longer enters a waiting-for-answer park for a person.
- Relay-to-owner (D1): `escalate`, the relay ledger, question codes, shown-entry anchors, and the Who/When/Which/Once answer acceptor are withdrawn; the acceptor is out of scope (locked decision 7).
- The 24-hour question expiry (D1.8): `failed(owner_unreachable)` / `failed(answer_timeout)` are gone; no replacement expiry.
- Redirect/RESUME withdrawal of the paused question (D1.7, F0929-R2-Q1=C): superseded-question closures, the withdrawal audit entry and the "unanswered" notice lose their object; redirect now only replaces the turn (locked decision 3).
- Steer-on-stopped-does-not-resume (D3): replaced — a message into a stopped helper resumes it like a chat message (locked decision 5).
- The D2/D7 rows that kept an owner question open across Stop/Stop all with its original deadline: the record they kept no longer exists; stopped helpers keep their ordinary conversation history like any stopped session.

### Code that already contradicts this amendment (adjustments to make; no code is written here)

Verified first-hand in this worktree (`work/a-steering-design-20261004`) on 2026-10-04; team-lead's verification matches. **Status pin (Correction, 2026-10-04):** A1 and A4 are **already implemented** on the implementation branch `work/a-lane-combine-notice-epoch-20261004` @ `ac7848fb8` (A1 via `0b071bdb7`, A4 via `775250d9f`; `git merge-base --is-ancestor` exit 0 for both), and the fence-less re-ring of A2 is **implemented** there at `0132e8000` (`84fe206b1`, a descendant of `ac7848fb8`). This design worktree still shows the pre-implementation code below — **do not dispatch A1/A4 as open work or redo them**; their acceptance certification is qa-lead's separate CHECK, not claimed by any ADR. The read-only source snapshot is not amended.

- **A1 — Boot marks an already-stopped helper failed — IMPLEMENTED at `ac7848fb8`.** The design checkout's `pkg/agent/boot_sweep.go::SteerBootRecovery.failInterrupted` had an "already-stopped arm" that set `LifecycleFailed` with reason `interrupted` (or `timeout`) and drove a fatal upward message. `0b071bdb7` replaced it on the implementation branch: a restart leaves an already-stopped helper stopped, keeps its retained stop reason, and sends no fatal interrupted/timeout message — as locked decision 1 requires.
- **A2 — A fence-less stop notice rings once — RE-RING IMPLEMENTED at `0132e8000`; residual hole answered by Correction C3.** The design checkout's re-ring-until-taken loop (`pkg/agent/stopped_notice.go::deliverLandedStopNotices` → `::deliverLandedStopNotice` → `::stopNoticeTaken`) covered only transitions discovered from the control ledger's landed history, so a fence-less notice could not re-ring. `84fe206b1` fixed that: a fence-less notice now re-rings on every delivery pass **while the stop note remains**. The remaining hole (review MAJ-003) is that Resume clears the active note — an untaken notice can then be lost — and two fence-less stops in one generation share the generation-derived identity; Correction C3 answers both.
- **A3 — Question expiry runs at startup for the old person-question.** The boot consumer enforces the 24-hour park limit for `needs_input` **and** stopped askers (the expiry arms in `pkg/agent/boot_sweep.go` calling `expireQuestion`, including the superseded-relay closure landed for it). With the pause removed, this expiry machinery must be removed; no replacement expiry (locked decision 6).
- **A4 — Provenance can leave a transcript line without the sender record — ROLLBACK IMPLEMENTED at `ac7848fb8`; abrupt-exit boundary answered by Correction C4.** The design checkout's `pkg/session/message_provenance.go::AppendTranscriptWithProvenance` documented a torn-write window: on a returned provenance-save error the message was rejected but the already-written transcript line stayed on disk without its sender record. `775250d9f` implemented the rollback (`pkg/session/transcript_indexed.go::UnifiedStore.appendTranscript` capturing pre-state, `::rollbackTranscriptAppend` restoring it): a returned save error now rejects the message and removes the paired line — the person sees an error and no turn starts. The remaining boundary (review MAJ-004) is the abrupt process exit between the two writes; Correction C4 names its recovery rule. Do not redo the rollback.
- **A5 — Delegate has no redirect or resume action.** The delegate action set in `pkg/tools/delegate.go` is `inbox, inbox_ack, steer, respond, cancel, clear_goal, follow_up, peek`. Locked decisions 3–4 require `redirect` and `resume` actions; add them contract-first (schemas → regeneration → handlers, Hard Constraint #8).
- **A6 — `follow_up` and `cancel` still use the old names.** ADR-20260928's D3 rename (`follow_up` → `resume`, `cancel` → `stop_all`) has not landed; the delegate enum above still carries the old names. Locked decisions 2 and 4 name the verbs Cancel/Stop-all and Resume; complete the rename in the same contract change, with no alias path (greenfield).

### Not changed by this amendment

ADR-20260928's stop mechanics (fence + separate stop note), receipts and control ledger, the rule that no stop ends a goal, the non-terminal Stop all cascade downward only, restart-as-automatic-stop for live runs, the distinct chat commands `/stop`, `/cancel` and `/stop-redirect` under the 2026-10-06 current-chat rule above, and rollback-out-of-scope all stand. This amendment removes the person-question layer and requires the two notice/expiry adjustments named above; it adds no behavior beyond the founder's locked decisions.
