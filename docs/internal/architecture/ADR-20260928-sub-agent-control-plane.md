# ADR-20260928 — The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume

- **Status:** Proposed, corrected, **revised to founder rulings 2026-09-29; needs one re-review.** The 2026-09-28 correction round (below) is unchanged history. On 2026-09-29 the founder issued a further, independent set of rulings on session states and the stop model (`coordination/PLAN-2026-09-28.md`, section "2026-09-29 Founder rulings — session states, stop model") that **supersede** several of the 2026-09-28 answers outright — this is not the ADR's one-correction-round budget (that round is already spent, disposed below); it is the founder overriding his own prior rulings before build start, which the architect is obliged to encode without waiting for a further grill round to license it (root `CLAUDE.md`, architect ADR-amendment rule: "flag the contradiction, correct it, date it"). Every normative section below carries a **[2026-09-29]** marker where it changed; superseded rows in [Founder decisions](#founder-decisions) are marked, not deleted. This revision still needs a `grill-spec` pass before build, since it changes D2, D6, D6b, D7 and D8 materially and opens a new gap (a delegate action to clear a helper's goal, §D7b) that has no founder sign-off yet.
- The one `grill-spec` (ADR mode) round returned **BLOCK** (4 critical, 11 major, 10 minor, 3 observations; [review](./ADR-20260928-sub-agent-control-plane-review.md), commit `f746ddb2a`). This is the **one correction round**: every finding is disposed in [Review disposition](#review-disposition); the founder's answers of 2026-09-28 to the review's eight questions are encoded (F1011-Q1 … Q8, [Founder decisions](#founder-decisions)). No second grill ran; the four items the correction left open were answered by the founder the same day ([Founder answers to the open items](#founder-answers-to-the-open-items)).
- **Build gate:** implementation starts only after the #984 follow-up (#1000) and the Q2=B branch have landed on `release/v0.1.1` (founder, 2026-09-28), **and** after this 2026-09-29 revision has its re-review. Restart resume is built **once, here** — Q2=B ships without it (founder Q7 = A, recorded in `coordination/logs/fix890-opus/lc947-defect1-design-note.md`, "Founder ruling Q7 = A"); restart resume's shape itself changes under this revision (D8).
- **Date:** 2026-09-28 (draft `12268d405`; correction the same day); **revised 2026-09-29** (this pass).
- **Deciders:** Daniel Piatkowski (founder); architect (draft, the 2026-09-28 correction, and this 2026-09-29 revision).
- **ID:** minted with `scripts/new-adr-id.sh "Sub-agent control plane"` → `ADR-20260928-sub-agent-control-plane` (date-and-title scheme, PR #996).
- **Evidence baseline:** `origin/release/v0.1.1` @ `ee7640c90` (draft evidence at `da8579715`; the three commits between are e2e-only). Symbols marked **[Q2B]** are on `origin/feat/q2b-on-984` (unlanded); symbols marked **[#1000]** are in commit `f040a2a66` on `origin/fix/984-followup` (merged into the Q2B branch, not on release). GitNexus was not used; symbols were located with `grep` / `git grep` and read in this task.
- **Amends:**
  - [ADR-093 — An open conversation must keep the ability to delegate](./ADR-093-open-conversation-must-keep-delegation.md) **D4**: `steer` no longer revives a finished child; only `follow_up` does (D3).
  - [ADR-091 — A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md) **D5** (action list gains `stop`, `redirect`, `escalate`), **D6** (a parked descendant now holds back its parent's completion — D6b below), **§7** (restart recovery of steered sessions — D8).
  - `docs/internal/specs/cancel-cross-channel-spec.md` **decision 12** and its **FR-5** ("Chat=/cancel only (no `/stop` alias)"): `/stop` and `/stop-redirect` are added as their own commands (D9, founder F1011-Q1).
  - `docs/internal/specs/unified-goal-plan-subagent-spec.md` **R§8.2 / FR-132 / FR-134**: the owner terminus gets a defined answer path; nothing is loosened (D1).
  - **[2026-09-29]** This ADR's own 2026-09-28 text (D2, D3, D6, D6b, D7, D8, and every mention of `paused`/`cancelled`/`interrupted`/`follow_up` as a session state or verb name) is amended by the founder's 2026-09-29 rulings — see Status above and the marked rows in Founder decisions.
- **Related:** #1011 (D1–D5, F1–F4); #1000 (queue ruling); #1020 (steered child has no post-turn steering drain) and #1027 (upward completion wake can repeat after inbox compaction) — D4 must not regress either; the fix-890 squad's Q2=B design note (`coordination/logs/fix890-opus/arch-q2-design.md`) and restart rulings Q5–Q7 (`lc947-defect1-design-note.md`) — both local coordination files, not in the repo.

## Scope

| Item from #1011 | In this ADR? |
|---|---|
| D1 — `artifact` messages rejected | **No.** Fixed separately (`pkg/tools/message_parent.go::outcomeForKind`). |
| D2 — owner-only questions cannot be answered | **Yes** — D1. |
| D3 — oversized child message hangs the child | **No** as a defect (both caps exist: `pkg/agent/loop_run_turn_tools.go::agentLoopRunTurnToolsExecute.applyQuarantineAndHooks`, `pkg/session/message_inbox.go::MessageInboxStore.Append`); verification belongs to qa-lead. D5 changes what the inbox body cap does to completion and question messages. |
| D4 — `steer` on a finished child revives it | **Yes** — D3. |
| D5 — a steer is delivered but not honoured | **Yes** — D2, D4, D5. |
| F1 — `stop`, `redirect`, chat commands | **Yes** — D2, D9. |
| F2 — delivery receipts | **Yes** — D4. |
| F3 — ordering, precedence, resource accounting | **Yes** — D5, D6. |
| F4 — rollback | **Out of scope, recorded as such** — D10. |

## Context

### What exists today (verified)

| Area | Today | Where |
|---|---|---|
| Parent-facing verbs | `run, status, inbox, inbox_ack, steer, respond, cancel, follow_up, peek` | `pkg/tools/delegate.go` (tool schema `action` enum) |
| Who may act | Any ancestor and — by design — the human operator (ADR-091 D5); authority walk | `pkg/tools/delegate.go::verifyCallerPrincipal`. The human-principal carrier `WithDelegatePrincipal` has **no production caller** (only `delegate_adr091_authority_test.go`) |
| `steer` | Queued in an **in-memory** per-scope queue, drained at the child's next tool boundary; advisory | `pkg/tools/delegate_followup.go::executeSteer`; `pkg/agent/steering.go::steeringQueue` |
| `steer` on a finished or Stop-marked child | **Revives it** (new generation, redispatch) and reports success | `executeSteer` → `pkg/agent/steering.go::ReviveStoppedSession` → `pkg/agent/steer_cancel.go::SteerCanceller.Revive` (ADR-093 D4) |
| `follow_up` | Only on a **terminal** record; mints the next generation | `pkg/tools/delegate_followup.go::executeFollowUp` |
| `cancel` | Stop marker on the child and every reachable non-terminal descendant, then interrupt (hard) or cooperative stop (soft); an already-terminal target returns a non-error "no action needed" | `pkg/tools/delegate_run.go::executeCancel`; `SteerCanceller.CancelSubtree` / `::StopSubtree`; `pkg/agent/steer_delegate_cancel.go::cancelDelegatedSubtree` |
| Steering queue cap | Release: `MaxQueueSize = 10` for every scope. **[#1000]** `f040a2a66`: 200, and an item carrying an upward wake bypasses the cap (`item.wake == nil && len(queue) >= MaxQueueSize`) | `pkg/agent/steering.go::MaxQueueSize`, `::pushItemScope` |
| Steer body / rate caps | 16 KiB, 6 per minute per target | `pkg/tools/delegate_followup.go::checkSteerCaps`; `pkg/session/message_inbox.go::DefaultSteerBodyBytes`, `::DefaultSteerRatePerMinute` |
| Inbox caps on upward messages | Depth ≤ 5 and body ≤ 32 KiB refuse **every** kind; the question/blocker per-child ceiling (20) applies to every question/blocker; wake-eligible kinds skip only the unacked cap (200) and the rate cap | `pkg/session/message_inbox.go::MessageInboxStore.Append`; `::DefaultChildSendMaxDepth`, `::DefaultChildSendBodyBytes`, `::DefaultInboxPerTypeCeiling`, `::DefaultInboxUnackedMax` |
| Receipt | `steering_receipt {correlation_id, applied_at}` on `subagent_state`, stamped at injection; no hand-written SPA reader | `pkg/agent/steer_frames.go::deliverSteeringReceiptsForInjection`; `contracts/components/schemas/SubagentStateFrame.yaml`; second copy `pkg/gateway/inboundschemas/SubagentStateFrame.yaml` |
| Owner-only questions | A parent's `respond` to an `owner_required` question is refused; an unverifiable (acked/absent) question is refused (fail-closed) | `pkg/tools/delegate_park.go::verifyQuestionAuthority`; R§8.2 / FR-132 |
| Spec's escalation story | FR-132 "MUST escalate to the owner terminus"; FR-134 "the human MUST answer in normal chat … correlation routing is the parent's job"; FR-135/R§8.7 opt-in shortcut, default off — **not built** (`grep question_escalation pkg` empty) | `docs/internal/specs/unified-goal-plan-subagent-spec.md` |
| Park time limit | `NeedsInput.TTLDeadline = now + 24h` is **written** at park time, but **no production code reads it** (`NeedsInput.Expired` has no caller) | `pkg/tools/message_parent.go` (`TTLDeadline:` in `parkNeedsInput`); `pkg/session/lifecycle.go::NeedsInput.Expired`; `pkg/session/message_inbox.go::DefaultNeedsInputTTL` |
| Session owner identity | A unified session stamps the authenticated user who created it (`Owner`); a web inbound message carries the authenticated gateway user (`GatewayUserID`); channel messages (Telegram, Discord, …) never set it | `pkg/session/unified.go::MetaPatch.Owner`; `pkg/bus/types.go` (`GatewayUserID` doc comment) |
| Lifecycle states | Eight; `paused` is documented for cancel-soft grace and plan owners awaiting supervision but **has no production writer** (only readers) | `pkg/session/lifecycle.go` (`LifecyclePaused` comment); readers `pkg/agent/boot_sweep.go`, `pkg/tools/list_jobs_row.go`, `pkg/session/lifecycle_bridge.go::lifecycleToUnifiedStatus` |
| Turn end after an interrupt | Any `context.Canceled` or aborted turn → terminal `cancelled`, reported upward | `pkg/agent/steer_completion.go::completionDisposition` |
| Lifetime limit | Deadline = `CreatedAt + Limits.TimeoutSeconds`, wall clock | `pkg/agent/steer_launcher.go::steeredTurnRunContext` |
| Restart | Two writers fail steered sessions: `SteerBootRecovery.recoverSteered` fails every steered record that is non-terminal, not `needs_input`, no current Stop (a `paused` record matches), delivers `interrupted` upward and ends its goal (`failInterrupted`); `PlanEngine.bootSweep` → `sweepToFailedInterrupted` fails every non-terminal non-standing record except a reconstructable `needs_input` and a plan owner awaiting supervision | `pkg/agent/boot_sweep.go` |
| Idle-expiry / `/goal clear` tail **[Q2B]** | Sends any steered record that is non-terminal, not `needs_input`, with no live turn through the completion tail → written `cancelled` | `pkg/agent/steer_completion.go::completeSteeredTurnIfDeferredAtGate` |
| "Whole job done" **[Q2B]** | A `met` claim is held while `hasRunningOrQueuedDescendant` is true; counts `queued` and live `running` only | `pkg/agent/steer_completion.go::hasRunningOrQueuedDescendant`; `pkg/agent/goal_child_completion.go::goalCompletionFenceActive`, `::resumeDeferredGoalAfterDescendantTerminal`; `pkg/agent/goal_triggers.go::goalTriggerState` |
| Parked question in the parent's hand-back | ADR-091 D6: a parked descendant does not block; its question travels in `open_questions` | `pkg/agent/steer_completion.go::parkedQuestions` |
| Chat commands | `/cancel` only; "No Aliases per FR-5 — /stop, /abort, /kill … explicitly forbidden"; `stop` is also a `/goal` clear word | `pkg/commands/cmd_cancel.go::cancelCommand`; `pkg/commands/cmd_goal.go::GoalClearAliases` |
| Web Stop button | `CancelSubtree` (cascade) then `RequestCancel` | `pkg/gateway/websocket_cancel.go::handleCancel` |

### What is wrong, in one line each

1. **An owner-only question has no path to the owner, and the owner's answer no path back.**
2. **The only enforced interrupt is `cancel`, which ends the whole subtree.** `steer` is advisory.
3. **`steer` on a finished child silently starts new work** (#1011 D4).
4. **A parent cannot tell "not yet seen" from "seen and ignored"**; the queue is in memory.
5. **A restart throws delegated work away** — failed, goal ended, each level woken separately.

## Founder decisions

Rulings given before the draft (2026-09-28), and the founder's answers to the review's questions (2026-09-28, relayed by team-lead). Where an answer differs from the draft's recommendation, the answer wins. **A further, independent set of rulings landed 2026-09-29** (`coordination/PLAN-2026-09-28.md`, section "2026-09-29 Founder rulings — session states, stop model") — those rows are marked **[2026-09-29]** and appear after the 2026-09-28 table; where a 2026-09-29 row supersedes a 2026-09-28 row, the older row is marked **Superseded 2026-09-29** rather than removed.

| ID | Decision | Lands in |
|---|---|---|
| R-D2 | Owner-only questions end with the human owner; the parent **cascades** them upward (a team lead relaying to its founder) | D1 |
| R-D4 | `steer` = live children only; on a finished child "already finished — use follow_up", nothing starts. Amends ADR-093 D4 | D3 |
| R-F1 | `stop`: non-terminal, pauses the current turn, resumable, **no cascade**. `redirect`: atomic stop + new instruction, **no cascade** | D2 — substance holds; **terminology superseded 2026-09-29** ("pauses" → "stops", state name `paused` → `stopped`, see F0929-2) |
| R-F2 | Receipts `queued / delivered / applied / superseded` | D4 |
| R-F3 | Latest control wins; a stopped session is idle but alive (no compute, no slot, deadline paused) | D5, D6 — substance holds under the new state name |
| R-C | `cancel` terminal, cascades **down only**, never cancels the parent's goal | D7 — **Superseded 2026-09-29.** "Terminal" and "ends the cancelled sessions' own goals (FD1=A)" are both overturned by F0929-3/F0929-5: the cascade now ends in `stopped` (alive, resumable), and it does **not** end any goal — see D7 |
| R-Q2 | Q2=B holds; a stopped/paused child counts as **unfinished** | D6 — **Superseded 2026-09-29 for the stopped case only** (F0929-6/F0929-7): a stopped child no longer silently blocks completion; the parent is told and decides. A working or waiting-for-answer child still counts as unfinished and still blocks — see Vocabulary and D6 |
| R-Q5 / Q6=A | After a restart interrupted children are not thrown away; they **keep their goal (paused)**; the top-level parent is told and can resume | D8 — substance holds; **"interrupted" as its own terminal state is superseded 2026-09-29** (F0929-3/F0929-8): a restart now produces the ordinary `stopped` state, not a distinct terminal-failed one — see D8 |
| Q7=A | Q2=B ships **without** restart resume; restart resume is built once, in this ADR | Build gate, D8 — unaffected by the 2026-09-29 rulings; the *shape* restart resume takes changes (D8) |
| R-1000 | "#1000 Q4: done/wake messages never refused; ordinary steers capped at 200" (`coordination/PLAN-2026-09-28.md`, "Founder rulings 2026-09-28 (late)"); implemented in `f040a2a66` for the steering queue | D5 |
| **F1011-Q1** | **Add `/stop` and a redirect command** (named `/stop-redirect` by the founder's follow-up answer, 2026-09-28) — single sub-agent, non-cascading; `/cancel` and the Stop button keep ending the whole tree. Amends the cross-channel cancel spec's `/stop` ban | D9 — reconfirmed 2026-09-29 (ruling 8: "Redirect (/stop-redirect) stays") |
| **F1011-Q2** | No person at the top (heartbeat / schedule / task): the owner-only question waits until the child's **24-hour limit**, then the child **fails visibly** with "owner could not be reached" | D1.8 — unaffected |
| **F1011-Q3** | Restart: **the child decides.** After a restart C is told its worker D was interrupted and resumes / redirects / drops D itself; the system does **not** auto-resume D (overrides the draft's recommendation). Parent-first boot order | D8 — **Superseded/generalized 2026-09-29** (F0929-6): "the child decides" is no longer restart-specific — it is one instance of the general rule that a parent with *any* stopped helper is told and decides (resume it, do the work itself, or report it open); it never hangs |
| **F1011-Q4** | A child **waiting for an answer holds back** the parent's "done": the parent waits until the question is answered and the child has finished. **Changes ADR-091 D6** (overrides the draft's recommendation) | D6b — reconfirmed 2026-09-29 |
| **F1011-Q5** | Owner identity **strict**: only the signed-in owner; the answer must come after the question was shown; one message answers exactly one question | D1.4 — unaffected |
| **F1011-Q6** | A child a person paused **may be resumed by the parent** agent (founder chose this over the review's recommendation) | D2, D6 — reconfirmed and generalized 2026-09-29 (F0929-6) into the rule for any stopped child, not only one a person stopped |
| **F1011-Q7** | Never refused upward: **done and failed reports and questions**; progress keeps its limits; any refusal is a **visible error to the sender**, never silent | D5 — unaffected |
| **F1011-Q8** | A resumed child continues from its **saved conversation** with a "do not repeat completed actions" note, never by replaying the original instruction | D2, D8 — reconfirmed 2026-09-29 (ruling 8: RESUME) |
| R-F4 | Rollback out of scope | D10 — unaffected |

**2026-09-29 rulings** (`coordination/PLAN-2026-09-28.md`, "2026-09-29 Founder rulings — session states, stop model"; numbered here in source order as F0929-N for citation):

| ID | Decision | Lands in |
|---|---|---|
| **F0929-1** | Follow the Claude Code model: sessions never end and are never deleted; only a **turn** ends. Every session is always resumable. Display/hiding of helper sessions in the UI is issue #1083 (referenced, not designed here) | Vocabulary, Context framing |
| **F0929-2** | Five session states: **working / waiting for answer / done / failed / stopped**. `stopped` **replaces both** `cancelled` and `paused` — one state, session alive, resumable | Vocabulary, D2 |
| **F0929-3** | Restart of Omnipus = automatic stop of working sessions; parent told. (Verbatim ruling; the retired `interrupted` design is this ADR's interpretation, not part of the quotation.) | D8 |
| **F0929-4** | `stop` (button, Esc, `/stop`) ends only **this session's** turn — no cascade. **Stop all** = press Stop/Esc twice to confirm, or `/cancel`, or a menu entry: cascades **down** to every helper, never up/sideways; end state `stopped`, not final | D2, D7 |
| **F0929-5** | `follow_up` / "continue" is renamed **RESUME**; allowed on any session that is not `working` | D3, D8, and every other mention of `follow_up` as the resume verb |
| **F0929-6** | Goals stay open on any stop; only `/goal clear` ends a goal. When a parent's goal is cleared, its helpers' goals are **not** cleared automatically — the runtime injects a prompt into the parent telling it to decide whether to stop its helpers and clear their goals | D7, D7b (new) |
| **F0929-7** | A parent with a **stopped** helper is told and decides itself (resume it, do the work itself, or report it open); it never hangs. Revises R-Q2 for the stopped case only — a working or waiting-for-answer helper still counts as **unfinished** | D6, D6b |
| **F0929-8** | Plans: stopping a plan stops all its steps at once (single press), and a plan can be resumed — this matches today's code (`PlanEngine.StopPlan` fan-out; `POST /plans/{id}/restart` → `PlayPlan` resetting only non-done steps). Kept; naming aligned to `stopped` (today `failed(stopped_by_user)`) | D8, Impact table |
| **F0929-race** | Race rule (not a numbered PLAN bullet; grounded in `tests/adr091/steered_sessions_test.go::TestE2E_StopSurvivesRestart` / `assertStopLandedOn`): a final answer that lands before Stop means `done`, not `stopped`; restart never starts it by itself | D2, D8 |

**[2026-09-30] Later founder answers**, recorded verbatim in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-2026-09-28.md`, section "2026-09-29 Founder rulings — session states, stop model"; these outrank the earlier rows above where they disagree:

| ID | Decision | Lands in |
|---|---|---|
| **F0929-Q19=A** | Timed out = `stopped`, resumable | Vocabulary, D6 timeout budget |
| **F0929-Q20** | `clear_goal` never cascades; a helper whose goal is cleared receives the same injected prompt as a parent and decides recursively about its own helpers | D7b |
| **F0929-Q21=A** | A parent may clear a working helper's goal; the helper is told | D7b |
| **F0929-R2-Q1=C** | A parent may redirect or RESUME a helper waiting for an owner-only answer; record the single-user accepted risk | D1.7, Consequences |
| **F0929-R2-Q2=A** | Stop all keeps an owner question open; answering later resumes the helper | D1.7, D7 |
| **F0929-R2-Q3=A** | Restarting a stopped plan resumes the **same step sessions** with history | D8 Plans |
| **F0929-R2-Q4=B** | A pending redirect/RESUME at restart is kept but awaits a **fresh explicit RESUME**; nothing replays into a running turn | D4, D8 |

## Decision

### Vocabulary

**[2026-09-29, F0929-1/F0929-2]** The founder's ruling replaces the 2026-09-28 draft's five-word vocabulary with the Claude Code model: a session never ends and is never deleted, only a turn ends, and every session is always resumable (RESUME, D3/D8 — the renamed `follow_up`, F0929-5). There are exactly **five session states** — `working`, `waiting for answer`, `done`, `failed`, `stopped` — and `stopped` **replaces both** `paused` and `cancelled`: one alive, resumable state, whatever produced it.

| Word (2026-09-29) | Meaning | Record | Supersedes (2026-09-28 draft) |
|---|---|---|---|
| **working** | a turn executing, `queued` to run, or `running` waiting on its own descendants | `queued`, `running`, no current Stop marker | `live` (renamed only) |
| **waiting for answer** | asked a question and waits for the answer | `needs_input` | `parked` (renamed only) |
| **stopped** **[2026-09-30]** | alive and resumable, no compute/slot; the remaining lifetime budget freezes while stopped except when expiry already spent it (Q19, D6); a question may remain open across Stop (D1.7) | `LifecycleStopped`, separate persisted `stop_note {at, by, seq, cause: stop/redirect_pause/cascade/restart/timeout}`; the old `Stop` marker remains the in-flight dispatch fence only (D2) | merges the prior `paused`, `cancelled`, and restart-only `interrupted` outcomes; Q19 also folds `LifecycleTimedOut` into `stopped` |
| **done** | the turn ended with a final answer | terminal `completed` | the successful half of `finished` |
| **failed** | the turn ended with a real error — never a stop, a cascade, or a restart | terminal `failed`, real error reason only | the erroring half of `finished` |

**[2026-09-30, F0929-Q19=A]** `LifecycleTimedOut` is retired as a distinct outcome: exhausting a session's **lifetime execution budget** writes `LifecycleStopped`, `stop_note.cause: "timeout"`; RESUME grants a fresh budget (D6). This does **not** change the separate 24-hour owner-question park expiry, which still writes `failed(owner_unreachable)` or `failed(answer_timeout)` (F1011-Q2, D1.8). Stop, timeout, question expiry, and terminal `done`/`failed` never clear an active goal as a side effect (F0929-6; D7).

**"Unfinished" for a parent's completion check (D6, D6b), after F0929-7 — restated precisely because the rule changed:**

| Child state | Blocks the parent's "done"? | Why |
|---|---|---|
| `working` | **Yes** — unchanged | The parent naturally waits; nothing to decide |
| `waiting for answer` | **Yes** — unchanged (F1011-Q4, D6b) | The question itself already wakes the parent; the 24-hour limit (D1.8) bounds the wait |
| `stopped` | **No, not by itself.** The transition to `stopped` tells the parent (D6's notice) and the parent **decides**: resume it (it becomes `working` again and blocks until it finishes), do the work itself, or explicitly report the goal open despite the stopped helper. The parent never hangs waiting on a stopped child (F0929-7, revises R-Q2 for this case only) | A stop is a decision point, not a silent block |
| `done` / `failed` | No | Terminal in the ordinary sense (though the session itself is still resumable per F0929-1 — "terminal" here means the turn is over, not that the session is gone) |

### D1 — Owner-only questions: relayed up by each parent, answered only by the signed-in owner

**[2026-09-30; F0929-R2-Q1=C]** The *answer path* retains R§8.2's guarantee: **no agent at any level authors an authenticated answer to an `owner_required` question.** The founder separately permits a parent to withdraw that question through redirect/RESUME and send its own instruction without owner approval; this accepted single-user risk is not represented as an authenticated answer (D1.7, Consequences). This ADR amends R§8.2 only for that withdrawal path.

**1. The parent chooses.** A child's `owner_required` question reaches the parent's inbox as today (wake-eligible, `pkg/agent/async_notifier.go::wakeableSessionMessageKinds`).

| Move | Verb | Effect |
|---|---|---|
| Relay upward | new `delegate(action="escalate", session_id, correlation_id, note?)` | Relay record + one-hop forward (step 2); returns the **new** relayed `correlation_id` |
| Withdraw the question **[2026-09-30]** | `redirect` or RESUME on the waiting child (D2; F0929-R2-Q1=C) | Question and relays close `superseded` **without an owner answer**; the parent's new instruction is parent-authored, not an authenticated owner answer (D1.7) |
| Stop or Stop all **[2026-09-30]** | `stop` on the child, or `stop_all`/`/cancel` (D7) | Session lands `stopped`, **question and relays remain open** until answered or 24-hour expiry (F0929-R2-Q2=A) |
| Try to answer | `respond` | Non-error guidance: "This question needs the owner. Use action=escalate to pass it up, or redirect/RESUME the child without an owner answer." Nothing changes |

Escalating a `self_ok` question is allowed; from then on the **first** answer to arrive wins (a parent `respond` on the original, or the owner's answer through the relay) and the other is closed `superseded` (MIN-004).

**2. Relay one hop at a time.** `escalate` mints a new `correlation_id` (runtime-generated, returned in the tool result) and appends a `question` to the relaying parent's upward inbox with: the child's text **verbatim**; the parent's `note` in a separate labelled `relay_note` field; `authority: owner_required`; `relay_of: {session_id, correlation_id}` (one hop down); `origin: {session_id, correlation_id}` (the first asker, copied unchanged). The relay record is stored **in the relay ledger of the relaying session** (the D4 ledger), not only in the inbox, so it is resolvable regardless of `inbox_ack` (MAJ-002). The relaying parent does **not** park. Relayed entries do **not** increase `Depth` — the hop count lives in the `relay_of` chain — so the inbox depth cap never refuses a relay (MAJ-003). This is R§8.7's strict one-hop default; FR-135 stays unbuilt.

**3. Owner terminus.** The hop stops at the tree's root (`pkg/session/lifecycle_edge.go::SteeredBy.RootSessionID`). When the relayed question lands in the root's inbox, the **runtime** — not the root agent — shows it in the root conversation as a persisted `subagent_message` event (kind `question`, ADR-091 D7's status-line contract) carrying a short **question code** (e.g. `Q-3F2A`). The root agent is woken and may explain it; the runtime entry is the "shown" anchor.

**4. Strict owner answer (F1011-Q5; fixes CRIT-001).** A `respond` whose target question is `owner_required` is accepted only as an **owner answer**, and an owner answer is valid only if **all** hold:

| Check | Rule |
|---|---|
| Who | The message's `GatewayUserID` equals the root session's `Owner` (`pkg/session/unified.go`), both non-empty. Any other participant, bot, integration, or a channel message without a gateway identity fails (fail-closed) |
| When | The message was persisted **after** the runtime's "shown" entry for this question (step 3), not merely after the inbox write |
| Which | The message names the question: it contains the question's code, **or** it is the only owner message after the shown entry while exactly one owner-only question is open at that root |
| Once | The message id is **consumed** in the root's relay ledger; a second use is refused, so one message answers exactly one question |

The answer delivered downward is **that message's text, copied by the runtime** (`answer_source: owner`, `owner_message_id`); the root agent's `respond` names the message and the question code, it never supplies the answer text. The agent's optional `note` travels labelled.

**Two answer paths, both strict.** (a) In the root conversation, as above. (b) **Directly in the parked child** (MAJ-001 — defined here, it did not exist): a message from the signed-in owner (same `Who` check against the child's own `Owner`) typed into a `needs_input` session answers that session's one open question — the ordinary inbound path (`pkg/agent/revive_inbound.go::runInboundTurnWithRevival`) gains this branch: close the question with `answer_source: owner` and resume the parked turn. The `When` anchor is the child's own question entry; `Which` is implicit (one parked question per session); `Once` holds. This path is built through the ordinary inbound path, **not** through `WithDelegatePrincipal`.

**5. Down in one traversal.** The runtime follows `relay_of` from the root to `origin` and resumes the asker with the owner's text through the same native / external split `executeRespond` uses today: `pkg/tools/delegate_park.go::resumeNative` for a native child, `::dispatchThirdParty` for an external command-line child (MIN-005). Intermediate parents run **no** turn for the answer; each gets a non-waking info entry. Each relay record closes `applied`. The `respond` relay branch validates against the relay record (open, not superseded, chain intact), **not** against the target's `needs_input` state (MAJ-002: the relaying parent is never parked).

**6. Worked example (three levels).**

```
Owner (signed in) ─ root chat R
                     └─ A (team lead)
                          └─ B (worker)
1. B asks owner_required Q (corr b1) and parks            B: needs_input
2. B → A inbox: question Q (b1); A wakes
3. A: escalate(B, b1) → runtime mints a1, relay record A:{a1 → (B,b1)}
      R inbox: question Q, relay_of=(B,b1), origin=(B,b1), corr a1
4. runtime shows Q in R as "Q-3F2A"; R's agent explains it to the person
5. owner writes "Q-3F2A: yes, use the staging key" (message m7)
6. R agent: respond(A, a1, owner_message_id=m7)
7. runtime: Who/When/Which/Once pass → copy m7 → a1 applied → b1 applied
      → resume B with m7's text; A gets an info entry; m7 consumed
```

**[2026-09-30; MAJ-005/006] 7. Question state across a stop or an explicit withdrawal.** Stopping a waiting child (single Stop or Stop all) moves it to `stopped` but preserves the **open question, original `TTLDeadline`, shown anchor, and relay chain** in a durable pending-question record separate from `NeedsInput` (which `pkg/session/lifecycle.go::persistLocked` clears outside `needs_input`). The root display marks it *open on a stopped helper*, not withdrawn. A valid signed-in owner answer later follows the same Who/When/Which/Once checks and resumes **that helper** explicitly even if its parent is still stopped; the parent's upward messages wait until that parent resumes. A self-ok answer takes the existing authorized `respond` path. The answer consumes exactly one question and closes each relay `applied`. Expiry still runs while stopped (D1.8). A direct parent's `redirect` **or RESUME** of a `waiting for answer` child **or a stopped child with that question still open** instead closes the pending question and relays `superseded` with reason `parent_redirect`/`parent_resume`, visibly marks the root entry withdrawn, records a `pkg/audit` entry for an `owner_required` withdrawal, and starts the child with a runtime-authored fact: "Question <code> was withdrawn without an answer from the owner; this is a parent instruction, not a verified owner answer." The parent instruction itself is not filtered; a parent may still claim approval in it. **Accepted risk, F0929-R2-Q1=C:** in the supported single-user instance the owner ultimately controls the parent agent, but the child may act on an untrue parent claim. Do not silently treat such a claim as an owner-signed answer. If an answer arrives after withdrawal or after expiry, refuse it as stale, report the reason, and do not consume the owner's message.

**[2026-09-30; MIN-005] 8. The 24-hour question-park limit.** A boot and periodic expiry check uses the original `TTLDeadline` even if the helper was later stopped. Expiry fails the asker visibly (`failed(owner_unreachable)` for `owner_required`, text "owner could not be reached"; `failed(answer_timeout)` otherwise), sends a fatal error upward (D5), and closes open relays `superseded`. This expiry is **not** Q19's session lifetime timeout: Q19 gives a timed-out **working** turn `stopped` and a fresh budget on resume. Expiry does not clear the goal (F0929-6). A failed session can still be explicitly RESUMEd (new generation); escalation does not extend the question TTL.

**[2026-09-30] 9. Audit.** Every owner answer and every parent withdrawal of an `owner_required` question writes a `pkg/audit` entry with question id, relay chain, actor, source action, `owner_message_id` if answered, and outcome; withdrawal has no owner message id.

### D2 — `stop` and `redirect`: runtime-enforced, non-terminal, no cascade

**[2026-09-29, F0929-2/F0929-4]** Every occurrence of `paused` below is the **same mechanism**, renamed `stopped` to match the five-state model (Vocabulary). Nothing about the mechanism, the lock discipline, or the races changes — only the state name and, per the race rule below, one outcome that the 2026-09-28 draft got wrong. This section also now covers **Stop all** (F0929-4's cascading half), which D7 (2026-09-29) folds into the same non-terminal `stopped` outcome instead of D7's old terminal `cancel`.

**[2026-09-30; CRIT-001] Relation to the existing Stop marker.** `LifecycleRecord.Stop` (`json:"stop"`, `pkg/session/lifecycle.go::LifecycleRecord`) and `pkg/session/lifecycle_edge.go::Stop` are **only the current-generation dispatch fence**, `{at, generation, by}`. They are not the lasting cause note. Add a *separate* persisted `stop_note` field `{at, by, seq, cause, boot_seq?, last_activity_at?}`; no second object uses the JSON key `stop`. The `stop` action writes its ledger intent and, in one `LifecycleStore.Mutate`, the current-generation Stop marker **and** `stop_note` before releasing the lock; then it interrupts only this turn (`pkg/agent/steering.go::Interrupt`, hard escalation after the existing grace). `completionDisposition` reads the fence/note rather than inferring a stop from `context.Canceled`. Landing `LifecycleStopped` clears the Stop marker and retains `stop_note` **in the same mutation**. `LifecycleRecord.Stopped()` is redefined as `State == LifecycleStopped || (Stop != nil && Stop.Generation == Generation)`; `persistLocked` accepts a note on `LifecycleStopped`, rejects a terminal state with a current fence as today, and rejects a stopped record lacking a note or still carrying a current fence. Raw current-marker readers, including both boot sweeps and dispatch/delivery guards, must distinguish in-flight stopping from landed stopped state. Terminal `done`/`failed` race writes clear **both** the current marker and note atomically; no terminal write ends a goal (D7). No lock is held over interrupt or a model call.

**[2026-09-30; CRIT-001] Same-generation resume and restart fence.** An explicit RESUME or redirect on a landed stopped session writes its ledger intent first, then atomically clears the current `stop_note`/any current marker, changes the **same-generation** state to `queued`, and records the intended control id and boot epoch. Only an explicit request in the *current* boot can dispatch it. A crash between that mutation and dispatch leaves `queued` (never runnable at boot); boot recovery first converts every interrupted `queued`/`running` record back to `stopped` with a new `cause: restart` note, keeps the pending instruction in the ledger, and requires a **new explicit RESUME** to release it (D8). If the final answer had landed first, its terminal write wins and no stop note survives (F0929-race). This couples clearing the old note to an auditable queued state, not to an unprotected runnable turn (`pkg/session/lifecycle.go::persistLocked`; `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered`). `stopped` gets its first production writer and replaces `LifecyclePaused`/`LifecycleCancelled`; a timeout stop uses the same mechanism (Q19).

**`stop`** — `delegate(action="stop", session_id)`:

| Child is | Effect |
|---|---|
| working, turn executing | as above → `stopped`; the partial turn stays in the transcript |
| working, `queued` | `stopped`; not admitted until resumed; frees its queue position |
| working, `running` waiting on descendants | `stopped`; upward messages from its descendants are stored, and wake it only on resume |
| stopped | "already stopped"; **no ledger line** (MIN-007) |
| waiting for answer **[2026-09-30; MAJ-006]** | Land `stopped` (self-only), retain the pending question, relays, shown anchor and original expiry in the separate durable question record; an authorized answer later resumes this helper (D1.7) |
| done / failed | "already finished — use RESUME"; nothing starts |

A restart-caused stop (D8) reads the same as any other `stopped` child in this table — there is no separate "interrupted" row any more (F0929-3).

**Races (MAJ-010; the first row is restated exactly to the letter of F0929-race, since the draft's `assertStopLandedOn` test — `tests/adr091/steered_sessions_test.go` — currently hard-fails any terminal outcome other than the old `cancelled`, which this ADR now says is wrong).**

| First to land | Result |
|---|---|
| The turn finishes with a **final answer** before the interrupt reaches it | Child `done`, as normal; the stop note is cleared by that terminal write; stop receipt `superseded`, reason `done`. **This is the state, not `stopped`** — F0929-race |
| The turn parks (`message_parent(wait=true)`) first **[2026-09-30]** | A subsequent Stop lands `stopped` and keeps that question/relay open (D1.7). If the asker explicitly redirects/RESUMEs instead, the question closes `superseded` (F0929-R2-Q1=C) |
| Stop all / `cancel` arrives during the stop's grace window | Stop all wins (cascades, D7); stop `superseded`, reason `stopped_all` — **not terminal** (D7, 2026-09-29; the 2026-09-28 draft said "Cancel wins (terminal, D7)", which is superseded) |
| A second `stop` | "already stopped" (no line) |

Either way — final answer or stop — the session must never be `working` again by itself after a restart (F0929-race); D8 restates this for the restart case specifically.

A `stop` reaches only the named session; descendants keep working; background shells are not killed (a parent that wants them gone uses Stop all).

**`redirect`** — `delegate(action="redirect", session_id, text)`, unaffected in mechanism by the 2026-09-29 rulings (ruling 8: "Redirect (/stop-redirect) stays; Steer stays live-only"). **Atomicity by sequence fence, not by a held lock (fixes CRIT-003):**

1. Under the child's record lock: assign `seq = R`, write the ledger line `redirect(R, text)` and the stop note (`cause: "redirect_pause"`); release the lock.
2. Interrupt the live turn exactly as `stop` (no lock held).
3. When the turn has ended (`stopped` by the note), the resume path starts the new turn with `text` as the newest instruction. At that moment every ledger control with `seq < R` still `queued` is marked `superseded`; controls with `seq > R` stay queued behind the instruction, in `seq` order.

Any steer that arrives while the redirect is in flight gets `seq > R` and is therefore delivered **after** the new instruction — never between the halves, and never lost. The turn-completion write takes the record lock on its own and never waits on the redirect (the deadlock the review found cannot arise). `redirect` on a stopped child skips step 2; on a waiting-for-answer child it closes the question `superseded` and resumes with `text`; on a done/failed child: "already finished — use RESUME".

**[2026-09-30; CRIT-001] Resuming a stopped child.** `redirect` (new instruction) or RESUME on **any non-working** session (F0929-5) resumes a stopped child on the **same generation** by the atomic note-clear/queued transition above; only `done`/`failed` mints a next generation. A signed-in owner's new message into its stopped session is also an explicit resume; no boot replay counts as one. Any ancestor may resume an owner-stopped child (F1011-Q6); the last `stop_note.by`/`cause` is copied to the receipt and the D2 resume instruction *before* the active note is cleared, so both the child and parent can see who stopped it. A waiting-for-answer session uses the D1.7 question rule instead of silently discarding its question (D2/D7).

**What a resumed turn runs (F1011-Q8, fixes MAJ-008).** The runtime **never** rebuilds the resumed turn from the last `user` transcript entry. It continues from the persisted conversation and appends one system note before the model call:

> You were stopped at <time> (cause: <stop|redirect|stop-all|restart>). The last completed tool call was <name> at <time>. Continue from here; do not repeat actions that already completed. <new instruction, if any>

If the stopped session had no turn in flight (waiting on descendants), resume flips it to `working` and wakes it only if stored upward messages are pending.

**Who may call them.** Any ancestor and the human operator through `/stop` / `/stop-redirect` (D9). External command-line children (`rec.Is3P`) return a named `not_steerable` result pointing to Stop all / RESUME, like `executeSteer` today.

### D3 — `steer` is for working children only (amends ADR-093 D4)

**Amendment, stated explicitly:** ADR-093 D4's paragraph "Children: a follow-up to a terminal child continues it" applied to `follow_up`/`steer`. From this ADR it applies to **RESUME only** (F0929-5's renamed `follow_up`; ruling 8 keeps "Steer stays live-only" — i.e. working-only). `steer` on a done/failed child returns

> Session <id> has already finished — nothing was started. Use RESUME to give it new work.

as a **non-error** result (the same reasoning as `executeCancel`'s non-error "already terminal — no action needed": an error here drives agents into retry loops) and starts nothing. **[2026-09-29]** `steer` on a **stopped** child (whatever the cause — `stop`, `redirect`, cascade, or restart, D8; the 2026-09-28 draft's separate "interrupted" case collapses into this one, F0929-3) is accepted as `queued` and delivered after the resume instruction (D5); it does not itself resume. F890-1 ("always resumable") is untouched: RESUME is the resume path.

**[2026-09-30]** `pkg/tools/delegate_followup.go::executeSteer` no longer revives finished children; only the explicit RESUME path does. Contract shape decided here (Hard Constraint #8): replace the `delegate.action` value `"follow_up"` with `"resume"` and `"cancel"` with `"stop_all"`; `stop` is separate. Update the tool enum, descriptions, receipts and contract schema together. No alias or automatic upgrade path is introduced on this greenfield branch; backend-lead performs the actual contract edits/regeneration.

### D4 — Every control gets a durable receipt; the ledger is the source of truth

**Controls [2026-09-30; MAJ-009]:** `steer`, `stop`, `stop_all`, `redirect`, `resume`, `respond`, `escalate`, `clear_goal`. Each accepted control gets a per-child `seq` (monotonic, assigned under the child's lifecycle lock) and a line in a **per-session control ledger** — append-only JSONL beside the lifecycle record, atomic appends (file-based storage, root `CLAUDE.md`).

| State | Meaning (runtime-written, deterministic) |
|---|---|
| `queued` | accepted, durable; not yet in front of the child |
| `delivered` | put in front of the model (injected at a tool boundary or turn start) |
| `applied` | the runtime **enforced** the effect: stop → record and notice `stopped`; redirect → new turn started with the instruction; `stop_all` → cascade and notices durable, sessions `stopped` (D7); respond → kept question consumed, asker resumed; escalate → relay persisted (answer closes that relay separately); `resume` → turn dispatched; `clear_goal` → named goal cleared and its notice durable (D7b) |
| `superseded` | replaced before delivery by a newer control (D5), or made moot (reason recorded) |

`steer` has no `applied` state — compliance is the model's, observable through `peek`, never claimed (#1011 F2). That answers #1011 D5: a parent that needs the child to stop now uses `stop`/`redirect`, which are enforced and receipted `applied`. `stop_all`, `respond`, `escalate` and `clear_goal` may be marked `applied` only after their own record/inbox/goal effects are durable; `resume` and `redirect` are `applied` only after dispatch, so a crash between intent and dispatch leaves a visible pending receipt (D4 boot reconciliation).

**Crash semantics (fixes MAJ-011).**

| Rule | Decision |
|---|---|
| Order | Ledger line **first**, then the effect (lifecycle write, interrupt, injection). A crash between the two leaves an intent the next boot finishes |
| Boot reconciliation **[2026-09-30; MAJ-010/F0929-R2-Q4=B]** | First stop interrupted `queued`/`running` sessions and retain their questions/notes (D8); then reconcile pending controls in `seq` order **without dispatching a turn**. Complete unfinished stop/Stop-all fences and rebuild receipts/queues. A pending redirect or RESUME survives as `queued` with `reason: restart_pending` and its original `seq`, text and `control_id`; it is **not** marked `applied` or `superseded`, and boot never injects it into the model. A **fresh explicit RESUME in this boot** releases preserved controls in sequence (the pending redirect instruction first, if any), then dispatches the same session; a second crash requires another explicit RESUME. The fresh control names the released ids in its receipt; an obsolete control that conflicts under D5 gets an explicit `superseded` reason, not silent loss. Injection is idempotent by transcript `control_id`: already-persisted text becomes `delivered`, never injected twice |
| Delivery | At-least-once to the ledger, **at-most-once into the model's context** by that `control_id` check. A `delivered` steer whose injection never reached the persisted transcript reverts to `queued` |
| Retention | Once the session is terminal and has no open relay, lines older than the last `applied`/`superseded` line are compacted into one summary line |

**Queue rebuilt from the ledger.** The in-memory steering queue becomes a cache of the ledger's `queued` steers. **#1020** (a steered child has no post-turn steering drain, so a late steer can strand it until restart) and **#1027** (an upward completion wake can repeat after inbox compaction purges the acked final) are being fixed now; D4 must keep both fixes: the cache refill feeds the same post-turn drain #1020 adds, and ledger replay goes through the wake id-coalescing of #1000's founder Q8 = A (a second enqueue of an already-queued upward-wake id is a no-op) — the ledger never re-enqueues an upward wake whose `message_id` the inbox has already marked consumed. A test pins each.

**Where a parent sees receipts.** (1) The control's tool result: `seq`, `control_id`, state. (2) `status` and `peek` (`pkg/tools/delegate_status.go`) list recent controls. (3) The `subagent_state` frame carries each transition; rendering it in the side panel is **new SPA work** (no reader exists today) — listed in Affected components (MIN-002).

**Wire shape (architect decides the shape; backend-lead edits `contracts/` and regenerates):**

| Schema | Change |
|---|---|
| new `ControlReceipt` | `{seq: int64, control_id, verb: enum[steer, stop, redirect, respond, escalate, follow_up, cancel], state: enum[queued, delivered, applied, superseded], accepted_at, delivered_at?, applied_at?, superseded_at?, superseded_by_seq?, reason?}` |
| `SubagentStateFrame` (`contracts/components/schemas/` **and** `pkg/gateway/inboundschemas/`, MIN-003) | `steering_receipt` replaced by `control_receipt: ControlReceipt` |
| new `DelegateStopAction`, `DelegateStopAllAction`, `DelegateRedirectAction`, `DelegateEscalateAction`, `DelegateResumeAction`, `DelegateClearGoalAction` **[2026-09-30]** | Explicit enum actions; `resume` replaces `follow_up` (D3), `stop_all` replaces delegate `cancel`, both separate from `stop` |
| `CancelFrame` (`contracts/components/schemas/CancelFrame.yaml` **and** inline `contracts/asyncapi.yaml`) **[2026-09-30; MAJ-002]** | Add `scope: enum[session, tree]`, optional with default `session` for a frame with no scope. Single Stop press/Esc sends `session` for root or helper; second confirmed press/Stop-all menu sends `tree`. `/stop` and `/cancel` use the same server behaviours via their command path. Server dispatches on the scope **before** calling `cancelSteeredSubtree`; never cascade an omitted/`session` scope. No client-only interpretation of this safety boundary (Hard Constraint #8) |
| `SessionMessageQuestion`, `SessionMessageDecisionRequest` | optional `relay_of`, `origin` (`{session_id, correlation_id}`), `relay_note` |
| `SessionMessageRespond` | `answer_source: enum[parent, owner]`, `owner_message_id?`, `question_code?` |
| `SubagentMessageFrame` | optional `question_code` (the D1.3 shown entry) |
| lifecycle record (where the SPA reads it) **[2026-09-30]** | Existing `stop {at, generation, by}` remains the **in-flight dispatch fence** (`pkg/session/lifecycle_edge.go::Stop`). New separate `stop_note {at, by, seq, cause: stop/redirect_pause/cascade/restart/timeout, boot_seq?, last_activity_at?}` is the lasting reason on `LifecycleStopped` (D2); `stopped_for_seconds` accounts for idle time. A landed stopped record has no current-generation `stop` marker. No `restart_interrupt` note. Both keys and state must appear in the schema the SPA actually reads; backend-lead defines the persisted-record schema before writing or exposing any new bytes (Hard Constraint #8) |

A dev install replaying old frames that still carry `steering_receipt` must drop the unknown field, not fail validation — the spec checks the replay path (OBS-003).

### D5 — Ordering, precedence and what is never refused

**"Pending"** = accepted, not yet `applied` or `superseded`. For `stop` that is the window between its stop note and the turn's end; for `redirect`, between its fence line and the new turn's start.

**Precedence** (MAJ-005). Rows: the newer control. Cells: what happens to an older pending one. **[2026-09-29]** `paused`/`parked`/`follow_up` renamed to `stopped`/`waiting for answer`/RESUME throughout (Vocabulary, D2, D3); `cancel`'s row is corrected — it no longer wins as a *terminal* outcome (D7, 2026-09-29), only as the *latest* one.

| Newer ↓ / older pending → | `steer` | `stop` | `redirect` | human message into the child |
|---|---|---|---|---|
| `steer` (agent) | both kept, `seq` order | steer waits for the resume | steer queued behind the instruction (`seq > R`) | both kept, `seq` order |
| human message into a working child (ADR-091 D5 — it is a steer) | both kept | waits for resume | queued behind | both kept |
| human message into a stopped child | — | resumes (same as RESUME); earlier queued steers delivered after it | resumes with the redirect's text first | — |
| `stop` | `superseded` | idempotent | `superseded` (the stop keeps the child stopped) | `superseded` |
| `redirect` | `superseded` | the redirect resumes with its text | `superseded` by the newer redirect | `superseded` |
| RESUME on a stopped child | queued steers delivered **after** the resume instruction, in `seq` order | resumes | the redirect wins if older and still pending; the resume queues behind it | delivered after |
| `respond` (waiting-for-answer **or stopped-with-open-question** child) **[2026-09-30; MAJ-006]** | unaffected; delivered after the answer | authorized answer closes the kept question and explicitly resumes that stopped child (D1.7); it is not a boot replay | the redirect already withdrew the question; respond refused as stale without consuming the owner's message | unaffected |
| `escalate` | unaffected (acts on the question, not the turn) | unaffected | unaffected | unaffected |
| Stop all / `cancel` | `superseded` | `superseded` | `superseded` | `superseded` |

`stop_all` (the `/cancel` user command) wins against earlier *steering controls* on every reached session, but it does **not** supersede that session's separate open-question record or relay chain (D1.7, founder Q2=A). It cascades down to `stopped`, not terminal (D7). A later authorized owner answer or explicit RESUME is a **new action**, not replay; its receipt names the earlier stop. Controls from different principals (parent agent, grandparent, owner) are ordered by `seq`, subject to the D1 owner-answer checks; a parent-authored RESUME/redirect withdraws the question visibly rather than pretending to be an owner answer.

**Steering-queue limits (#1000 baseline `f040a2a66`; MAJ-004).** The 200-item cap and the wake bypass are already coded on the #1000 branch; this ADR adds, **for delegate `steer` controls only**, a cap of **256 KiB total pending text per child** in the ledger, refused visibly with the cap named. The chat queue keeps #1000's 200-item cap. Injection per tool boundary follows the configured `SteeringMode` (`one-at-a-time` default). `stop` and `cancel` are exempt from the queue, byte and 6/min rate caps; `redirect` is exempt from the queue and byte caps but keeps the rate and 16 KiB body caps.

**Upward messages that are never refused (F1011-Q7; fixes MAJ-003).** Reconciled with the #1000 ruling and the inbox caps in `MessageInboxStore.Append`:

| Upward kind | Depth cap (5) | Body cap (32 KiB) | Per-child question/blocker ceiling (20) | Unacked cap / rate |
|---|---|---|---|---|
| `handback` (done) | exempt | exempt — over-long text is stored truncated with a visible truncation marker and the full text stays in the child's transcript (ADR-087 — truncation is an outcome, not a silence) | n/a | exempt (as today) |
| fatal `error` (failed), `goal_status` | exempt | exempt, same truncation | n/a | exempt (as today) |
| `question`, `decision_request` | exempt (relays do not add depth, D1.2) | exempt, same truncation | **exempt** | exempt (as today) |
| `blocker` | exempt | exempt, same truncation | **exempt** | exempt (as today) |
| `progress`, `checkpoint`, `artifact`, non-fatal `error` | applies | applies | n/a | applies |

**Any refusal is a visible error to the sender**, returned as the `message_parent` tool result naming the cap — never a silent drop. `blocker` is never refused either (founder answer to O3, 2026-09-28). A child cannot flood questions or blockers without limit in practice: a `wait=true` question parks it, and D1.8 expires parks; `wait=false` questions and blockers are the residual risk (Consequences).

### D6 — A stopped child is idle, alive, costs nothing — and the parent decides (F0929-7 supersedes "counts as unfinished" for the stopped case)

**[2026-09-29]** `paused` renamed `stopped` throughout this section (Vocabulary). More than a rename: F0929-7 reverses the completion effect a stopped descendant has on its parent — see "Q2=B interaction" below, the load-bearing change in this section.

| Aspect | Rule |
|---|---|
| Compute | No turn runs; descendants' upward messages are stored and wake it only on resume |
| Execution slot | None (ADR-091 D6: a slot only while a turn executes) |
| Deadline **[2026-09-30; F0929-Q19=A]** | Measure **remaining active-time budget** durably (excluding time stopped and restart downtime); a voluntary stop/resume uses the remainder. A stop with `stop_note.cause: "timeout"` consumed that budget, so an explicit RESUME resets it to a fresh `Limits.TimeoutSeconds` starting at `resume_at` — not `CreatedAt + TimeoutSeconds + stopped_for` (which would expire immediately). Persist the budget/reset epoch before dispatch; an automatic boot stop does not reset it. The question-park TTL is independent and continues during a stop (D1.8) |
| Addressable | Until resumed: `status`, `peek`, `inbox`, `steer` (queued), `redirect`, RESUME, Stop all/`cancel` (idempotent) |
| Boot **[2026-09-30]** | Both sweeps preserve a landed `stopped` record and its separate `stop_note` (not the `Stop` fence); a pending in-flight fence is completed as a stop, not treated as an already-landed state. An unfinished `queued`/`running` record is stopped before control-ledger replay; no replay dispatches it (D8). Repeated boot never rewrites an already-landed stop note |
| Goal **[2026-09-30; MAJ-003]** | **Every session-owned active goal stays active across stop, timeout, question expiry, restart, `done` and `failed`**. Remove FD1=A's pair-end side effect from **every** session-terminal path, not just Stop (`pkg/agent/goal_child_completion.go::endSessionOwnedGoalOnTerminal`; four call sites in Impact). Natural goal `met`/round-exhaustion adjudication remains independent; a deliberate clear is only the session's `/goal clear` or its authorized parent's `delegate(clear_goal)` (D7b). Idle goal expiry may change a goal only under its separate existing explicit goal policy; it must never change `LifecycleStopped` or close the goal *because the session stopped*. `completeSteeredTurnIfDeferredAtGate` refuses stopped records. A clear does not itself stop a turn or cascade |

**[2026-09-30; CRIT-002/MAJ-007] Q2=B completion frontier.** `pkg/agent/steer_completion.go::hasRunningOrQueuedDescendant` currently counts `queued` and live `running`, **not** `needs_input` (`waiting for answer`); change it to count `needs_input` as blocking. A `stopped` node does **not** block and **cuts the traversal of its subtree**: even a working grandchild under a stopped child does not hold back the stopped child's parent. That parent is told about its direct stopped child and decides about the branch; the grandchild's work and result are stored for its stopped parent and reconciled when that parent explicitly resumes. If the stopped parent resumes while the grandchild remains working or waiting, the branch becomes blocking again. The direct-child goal-claim check gets the same state mapping (D6b). The old "paused blocks" rule is superseded by F0929-7.

**[2026-09-30; CRIT-002] The notice and claim recheck are one durable settlement path, not a conditional nudge.** Every steered child's transition **into `stopped`** (single Stop, timeout, Stop all, plan stop, restart) persists one notice for its **direct parent** before the control is `applied`, whether or not a claim exists. Deterministic inbox id `(parent_id, child_id, child_generation, stop_seq)` deduplicates live retries and boot replay; delivery failure remains pending, visibly reported and retried at boot/periodic delivery, never silently considered applied. A parent `working` is woken once by that notice; a `stopped`, waiting-for-answer, `done` or `failed` parent retains it without a wake, and sees it on explicit resume/answer. The notice says cause/actor/time and offers RESUME, redirect, do the work, report it open, or clear that helper's goal; an owner stop says to consider asking the owner first. A parent whose own goal was cleared receives a **different** child-facing injection (D7b).

**[2026-09-30; CRIT-002] A settled child triggers a deferred-claim recheck.** On the same stop transition (after persisting the state/notice), call the Q2=B post-descendant completion check now named `resumeDeferredGoalAfterDescendantTerminal` (`feat/q2b-one-message-when-done:pkg/agent/goal_child_completion.go`), renamed `…AfterDescendantSettled`, for `stopped` **as well as** terminal `done`/`failed`. Reconstruct the deferred phase from `Goal.LatestClaim`, re-evaluate the completion frontier, and enqueue at most one fresh-claim turn for an eligible working parent once its remaining blockers are gone; never auto-accept its old `met` claim. The durable claim id + settled `(child_id, generation, stop_seq or terminal event id)` is the dedup key across live delivery/boot replay. A parent that is itself stopped is *not* woken: store the recheck and notice until its explicit resume. If a deeper child settles behind a stopped middle session, that middle already cut the ancestor's blocking frontier when **it** stopped; verify any nearest non-stopped ancestor with a deferred claim at that frontier, but do not bypass the direct parent's decision with a second ancestor notice or automatically resume the stopped middle. This closes the ordinary A-claims-`met`, B-stops last-helper hang without inventing a cascading agent decision (F0929-7/Q2=B).

### D6b — A child waiting for an answer holds back its parent's "done" (amends ADR-091 D6; unaffected in substance by the 2026-09-29 rulings)

**Amendment, stated explicitly (F1011-Q4, reconfirmed 2026-09-29 by F0929-7's "waiting for answer... still count as unfinished"):** ADR-091 D6 said "a parked descendant does not block it — its question travels in the parent's `handback` as an open question". From this ADR, a child **waiting for an answer** blocks its ancestors' completion exactly like a working one: `hasRunningOrQueuedDescendant` **[2026-09-30; MAJ-007]** must **gain** a `needs_input` blocking case (it currently checks only `queued` and live `running`, including on `origin/release/v0.1.1`); it must not traverse below a `stopped` node (D6). ADR-091 D6 criteria-free completion ("completes when … a quiet subtree") must use the same frontier. The parent waits until the question is answered **and** the child has finished. No notice turn is needed: the question itself already woke the parent (D1.1), and the 24-hour limit (D1.8) guarantees the block ends. Consequences: `open_questions` in a completion hand-back is always empty on these paths; `parkedQuestions` stays only for a hand-back that is not a completion (e.g. a stopped-child report), and excludes questions with an open relay (MIN-006).

**Verified pre-existing gap in the direct-children path (flagged by team-lead, checked in this task, not part of the founder's 2026-09-29 ruling but directly contradicted by it):** a **second**, narrower blocking check exists — `tools.GoalClaimDirectChildrenAccess.DirectNonTerminalChildren` (`pkg/agent/goal_record_wiring.go::(agentLoopGoalRecordAccess).DirectNonTerminalChildren`, consumed by `pkg/tools/goal_claim.go::executeSetGoal`/claim evaluation for a `met` claim) — that checks only a session's **direct** children, for goal-claim admission rather than turn completion. Read in this task: it switches on `session.LifecycleQueued` and `session.LifecycleRunning` only —

```go
switch child.State {
case session.LifecycleQueued:
    active = append(active, child.SessionID)
case session.LifecycleRunning:
    if a.al.steeredCompletionWriteActive(child.SessionID) { ... } else if ts != nil && ts.IsAlive() { ... }
}
```

— **it has no `session.LifecycleNeedsInput` case**, so a direct child waiting for an answer does not block a `met` claim through this path, contradicting D6b/F1011-Q4/F0929-7 ("waiting for answer... still count as unfinished"). This is a genuine, verified bug against the *already-standing* rule, not something the 2026-09-29 revision introduces — but it must be fixed as part of this ADR's implementation, since the new ruling makes the "stopped does not block" contrast explicit and this gap sits right next to it. **Proposed fix:** add a `session.LifecycleNeedsInput` case to `DirectNonTerminalChildren` that always appends the child (always-blocking, matching D6b — no liveness check needed, since waiting-for-answer is definitionally not running a turn). A `session.LifecycleStopped` (renamed `LifecyclePaused`) case is correctly **absent** — per F0929-7 a stopped child must **not** block here either, so no case should be added for it. See Impact table.

### D7 — Stop all (`cancel`) cascades down only, ends in `stopped`, and never ends a goal by itself

**[2026-09-29, F0929-4/F0929-6] Superseded outright from the 2026-09-28 draft's "`cancel` is terminal ... ends the cancelled sessions' own goals (FD1=A)".** The underlying mechanism is unchanged (`CancelSubtree` / `StopSubtree`, `pkg/agent/steer_cancel.go` — read in this task: `CancelSubtree` cascades with `cancelLiveTurns=true`, `StopSubtree` with `false`, both under the same cascade lock and two-pass enumeration); what changes is the **outcome** the cascade lands sessions in, and what it does to goals:

- **Non-terminal.** The cascade lands every reached session in `stopped` (cause `"cascade"`), same generation, resumable exactly like a single `stop` (D2) — via `redirect` or RESUME. The draft's "terminal for the current generation; `follow_up` may start a next one later" is withdrawn: there is no terminal outcome here to start a next generation *from*.
- Cascades to every reachable non-final descendant, **including stopped and waiting-for-answer**; never upward or sideways ([A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md), D8). **[2026-09-30; MAJ-006]** A waiting session reached by Stop all lands `stopped` but keeps its open question, original 24-hour deadline, relay chain and shown owner entry in the separate durable question record (D1.7). A later authorized answer explicitly resumes that helper even if its parent remains stopped; no boot replay does so. Repeated Stop all leaves the same question open without resetting its clock (F0929-R2-Q2=A).
- **Goals stay open (F0929-6).** Stop all does **not** end any goal — not the cascaded sessions' own, and (unchanged from the draft) never an ancestor's either. Only `/goal clear` (`pkg/agent/goal_outcome.go::clearGoalByUser`, same-session only) or the new `clear_goal` action (D7b) ends a goal. `#1000`'s `pkg/agent/goal_ancestor_guard_984_test.go` (pinning "never an ancestor's goal") still holds; a **new** test is needed pinning "never the cascaded session's own goal either", since that is the opposite of what the 2026-09-28 draft's FD1=A required.
- **The "drop" mechanic for an interrupted child is withdrawn.** The draft gave `cancel` a special "drop" behavior for a restart-interrupted child (ending its kept goal, receipt `applied`/`dropped`). Since a restart-stopped child is now an ordinary `stopped` child whose goal no stop ever touches (F0929-6), there is no special case left: Stop all reaching an already-stopped child is simply idempotent at the record level (D5) — ending its goal, if the parent chooses to, is D7b's job.
- Supersedes every pending control on the sessions it reaches (unchanged).
- `stop`, `redirect`, `/stop`, `/stop-redirect` never cascade (unchanged).

### D7b — Clear exactly one helper goal; tell that helper to decide about its own helpers **[2026-09-30; F0929-Q20/Q21]**

**Context.** `pkg/tools/delegate.go` has no child-goal action; `pkg/agent/goal_outcome.go::clearGoalByUser` operates on the caller's session only. Once Stop ceases to end goals (D6/D7), a parent needs an explicit way to clear a helper goal. The founder ruled that even a **working** helper's goal may be cleared, with notice to that helper, and no clear cascades (`coordination/PLAN-2026-09-28.md`, Q20/Q21).

**Decision.** `delegate(action="clear_goal", session_id)` requires an authorized ancestor and clears the named helper's **own active goal only**, exactly one session per call; it does not stop or restart its turn, withdraw a question, or clear any descendant goal. It has the same goal-ending semantics as the helper's own `/goal clear`. A working helper receives a runtime-authored prompt at its next safe turn boundary: its goal was cleared; decide whether to stop its own helpers or clear *their* goals. A stopped or waiting helper retains that distinct notification durably for its next explicit resume/answer; no notification wakes a stopped helper, no automatic recursive action occurs. Clearing a parent's own goal issues the same prompt to **that parent**. Every helper may separately decide about its own descendants, so a multi-level tree is handled recursively by agent decisions, never an automatic cascade. This goal-clear notification is **distinct** from D6's stopped-child notice: it fires on an explicit goal clear, not on a child stop, and the two can coexist without either acknowledging the other. A missing active goal returns a visible non-error "no active goal"; a successful receipt names the target session and cleared goal id. The `clear_goal` ledger entry becomes `applied` only after goal state and its own notice are durable. Backend-lead owns contract edits/regeneration (root `CLAUDE.md`, Hard Constraint #8).

**Consequence.** A working helper can continue its present turn without a goal; the parent must explicitly Stop it if that is also intended. The accepted distinction between the goal's lifecycle and the session's lifecycle prevents clearing one goal from silently cancelling a subtree (Q20/Q21, D6).

### D8 — Restart is just an automatic stop; the parent is told and decides (amends ADR-091 §7; supersedes the draft's "interrupted" design)

**[2026-09-29, F0929-3] Superseded outright:** "Restart of Omnipus = automatic stop of working sessions (parent told) — replaces the separate 'interrupted' terminal-failed idea where it simplifies." This removes the entire "interrupted" apparatus the 2026-09-28 draft built (a distinct terminal state, a `restart_interrupt` note, a goal-kept special case, a "the drop" cancel variant) and replaces it with: a restart is a `stop`, full stop — the ordinary D2/D6 machinery, with `cause: "restart"`.

**1. Boot order: parents first — unchanged rationale.** `SteerBootRecovery` still processes records in **edge order** — every root, then its children, then theirs (breadth-first over `SteeredBy`) — instead of today's lexical id order (`SteerBootRecovery.sessionIDs`). This did not depend on "interrupted" being its own state, so it carries forward: a parent's own stop/resume should still be settled before its children's notices are meaningful.

**2. Which sessions are stopped by a restart — same test as the draft's "interrupted", renamed.** A native steered session whose state is `working` (`queued` or `running`) and that has no current stop note. **Not** affected: an already-`stopped` record (stays stopped, note unchanged — CRIT-002's fix still holds), a reconstructable `waiting for answer` (preserved, as today), a standing root (ADR-093 D3). External command-line children keep today's handling (their process is gone; RESUME runs a corrective successor, ADR-091 D5).

**3. What an affected session becomes.** `stopped`, with a stop note `{at, by: "restart", seq, cause: "restart", boot_seq}` — an ordinary stop, not a distinct terminal state; no more `failed(interrupted)` record and no more separate `restart_interrupt` note (D4). **Its goal is untouched** — not because a goal-ending step was specially skipped (the draft's "`EndSessionGoal` skipped in `failInterrupted`" framing), but because **no stop of any kind ends a goal** (F0929-6): there is no goal-ending step in the restart-stop path to skip in the first place. `boot_seq` is still a monotonic boot counter persisted in the data dir, now carried on the ordinary stop note. `PlanEngine.bootSweep` still leaves steered records to `SteerBootRecovery` (avoids a second writer racing the same record).

**4. Direct-parent notices at boot [2026-09-30; MAJ-009].** The D6 notice is persisted **eagerly for each child actually stopped** at restart, to its direct parent's inbox before any wake or control receipt says `applied`. This is not one aggregate root message: in root → C → D, the root receives C's stop; C receives D's stop even when C itself is stopped. A working parent receives one coalesced wake for the batch of distinct notices after the stop sweep; a stopped or waiting-for-answer parent retains its notices without a wake, reading them when explicitly resumed/answered. The root does not act in C's place on D. Deduplicate each transition with D6's `(parent_id, child_id, child_generation, stop_seq)` key, and the parent wake with `(parent_id, boot_seq)` for this boot; repeated sweeps neither create new transitions for an already-stopped child nor replay wakes after a consumed inbox id. If storing a notice fails, keep a durable retry item and report the failure: do not silently claim delivery. F0929-3/F0929-7 are satisfied at every directly affected level without automatically starting any stopped parent.

**5. Nothing resumes automatically [2026-09-30; MAJ-010/F0929-R2-Q4=B].** Boot order is: persist a monotonic boot epoch; finish an in-flight stop and stop each interrupted `queued`/`running` steered session; persist its direct-parent notice; then reconcile ledgers and publish receipts **without dispatch**. A `redirect`/RESUME accepted before the crash remains `queued` with the original instruction, identity and sequence (`restart_pending`), visible via `status`/`peek` and the parent's notice. The first **new explicit RESUME in this boot** releases valid pending controls in order (D4); a second restart requires a second new RESUME. Thus neither recovery nor an old accepted control restarts a turn. An explicit owner answer to a retained open question is also a **new** post-boot action, not replay (D1.7). A stopped child's explicit RESUME or redirect continues the same generation (D2), with a note naming the restart; it reconstructs any process-local Q2=B waiting-for-descendants phase from `Goal.LatestClaim`. No prior control is silently marked `applied` or lost.

**6. Unfinished until decided — corrected to F0929-7, not the draft's rule.** The draft: "an interrupted descendant with a kept goal counts as unfinished until it is resumed and finishes, or is dropped with `cancel`." **This is now wrong**, for the same reason as D6's Q2=B correction: a restart-stopped descendant does **not** block a claim by itself (`hasRunningOrQueuedDescendant` must return false for it, D6). What survives: the parent must still be **told** and must still **decide** — deciding is not automatic, but not-blocking and not-deciding are different things. The D6 stopped-child notice (not a claim-specific trigger) is what covers this case now, not a claim-blocked notice specific to interrupted descendants.

**7. Deadline without downtime (MIN-009) — unchanged.** Time between `last_activity_at` (the record's last persisted update before the crash) and the resume is added to the stop-time credit (renamed from `paused_for` — D6); downtime never consumes a session's lifetime budget.

**8. Housekeeping — unchanged, reinforced by F0929-1.** No automatic deletion is introduced; "housekeeping" in the draft referred to nothing that exists and stays withdrawn. A never-resumed stopped tree stays until its goal is cleared (`clear_goal`, D7b, or its own `/goal clear`) and/or its chat is deleted (ADR-093 D7) — F0929-1's "sessions never end and are never deleted" means there is no terminal state a restart could put a tree into that would ever need cleaning up.

**9. The race rule, restated for restart specifically (F0929-race, verified against `tests/adr091/steered_sessions_test.go::TestE2E_StopSurvivesRestart`/`assertStopLandedOn` in this task).** A restart's stop reaches a session either before or after its live turn produced a final answer: before → `stopped` (this section); after → the turn's own terminal write already landed the session at `done` (or `failed`) before the crash, and the restart must leave that alone. **Either way the session must never be `working` by itself after the reboot.** The cited test currently hard-fails (`assertStopLandedOn`, line asserting `rec.State != session.LifecycleCancelled` is a failure) any terminal outcome other than the old `LifecycleCancelled` — that assertion is now too strict and needs updating to accept `LifecycleCompleted` for the race's "final answer already produced" branch (Impact table); the Required acceptance tests table's T4/T6 rows also need their state names updated to `stopped`/`done` before build.

**10. Plan Stop and restart [2026-09-30; MAJ-008/F0929-R2-Q3=A].** `pkg/agent/plan_engine.go::StopPlan` already fans out to in-progress member, owner, verifier and supervision sessions; their session outcome becomes D2 `stopped` with goals kept, and each direct parent gets D6's deduplicated notice. A plan record is a different entity from a session: retain its existing `failed` + `failed_reason: stopped_by_user` and each stopped task's existing `failed` + `cancel_reason: stopped_by_user` **on the wire**; render these as "Stopped" in the plan/task UI, not as a genuine failure. There is **no new Plan/Task enum** (D4 wire table). A later explicit `POST /plans/{id}/restart` counts as an explicit plan-level resume. It may mint a new **plan-record generation** and clear the plan's adjudication signature as `PlayPlan` does today, and it preserves done members, but it must retain each non-done step's `Task.SessionID`, conversation, goal and direct-parent edge. When that step is readmitted, RESUME its **same stopped member session** and history (D2 note), not a newly created step session and not an old instruction replay; only a step that never had a session may create one. An already-working step is not dispatched a second time. The plan owner session uses its existing session id/history when resumable; verifier/supervision sessions are not plan *steps* and can be resumed or recreated for a new adjudication attempt according to their own lifecycle, without replacing a step session. A step/plan state change and session dispatch need recoverable intent + a receipt: if resuming a member fails, keep its session and goal, show the failed member id, and do not report the plan fully resumed. D6 stop notices remain in their inbox as history, but neither plan restart nor member admission emits a **new stop notice** unless that session stops again. Boot alone never performs a plan restart or member resume (D4/D8.5). `pkg/agent/plan_engine_play.go::PlayPlan` currently resets stopped members for redispatch; its member-session reuse/dispatch path must change. Separate stop-plan, restart-plan and crash-between-plan-restart-and-member-dispatch tests pin these boundaries.

### D9 — Chat commands `/stop` and `/stop-redirect` (F1011-Q1; amends the cross-channel cancel spec)

**The founder's model (2026-09-28):** *steer* sends an instruction without stopping; *redirect* stops first, then gives the new instruction. The chat command for redirect is therefore named **`/stop-redirect`** so the difference is obvious in chat; the tool action stays `redirect`. `/stop` remains its own command. There is **no `/steer` alias** (founder answer to O4).

**Amendment, stated explicitly:** `docs/internal/specs/cancel-cross-channel-spec.md` decision 12 ("Chat=/cancel only (no `/stop` alias)") and its FR-5 are amended by the founder's answer: `/stop` and `/stop-redirect <instruction>` are added as **their own commands** — not aliases of `/cancel`. `/cancel` keeps FR-5's "no aliases" rule. The `cmd_cancel.go` comment and the spec's registration test (exact-set assertion, review F-20 of that spec) are updated in the same change.

**[2026-09-30; MAJ-002/MIN-007, F0929-4] Scope is explicit at the server, not inferred from the root/helper relationship (D4 `CancelFrame.scope`).**

| Input | Where | Effect |
|---|---|---|
| `/stop`; **one** Stop-button press; Esc with focus in the active conversation composer/pane (and no dialog, editor or other higher-priority Esc handler) | Root **or** helper | Stop only **that session's current turn** (`scope: session`); its helpers keep working and send results into its durable inbox. If the session waits for an answer, stop its wait but keep the question open (D1.7). No root refusal and no cascade |
| `/stop-redirect <instruction>` | A helper's chat | D2 redirect on that helper only; its subtree keeps working |
| `/stop-redirect <instruction>` | Root chat | Refuse with guidance to target a helper; the root's own `/stop` still works |
| Second Stop/Esc press on **the same session** within a three-second confirmation window; Stop-all menu entry; `/cancel` | Root or helper | `scope: tree`: downward Stop all (D7) of this session and its descendants, state `stopped`, never up or sideways; `/cancel` and the menu entry themselves are explicit confirmation |

The first press visibly offers the Stop-all confirmation for that session; the window expires after three seconds or on focus/session change, and a new first press is again a single-session Stop. Esc first closes an active dialog/editor by that surface's handler and must **not** send a stop frame as well. This specifies the interaction boundary (UX heuristics H3/H5); labels, visual treatment and the display/hide of helper sessions belong to frontend-lead and #1083. `pkg/gateway/websocket_cancel.go::handleCancel` branches on scope before `cancelSteeredSubtree`, including the root case. Chat commands are conversation-scoped (#955), authorized for the signed-in owner, and use the same server actions; `/goal stop` remains the distinct `/goal` clear alias (`pkg/commands/cmd_goal.go::GoalClearAliases`). No `/steer` alias is added (founder O4).

### D10 — Rollback is out of scope

Undo/rewind will not be built (#1011 F4). Resumability covers "continue from here"; irreversible actions are gated **before** the action (the Ask policy, root `CLAUDE.md` Hard Constraint #6), never rewound. D2's resume note is the only related safeguard.

## Required acceptance tests (named here so the spec cannot drop them)

**[2026-09-29]** T4 and T6 are corrected to the new state names and outcomes; T11–T13 are new, covering the 2026-09-29 rulings and the two verified gaps this revision found.

| # | Scenario | Expected |
|---|---|---|
| T1 | Two owner-only questions open at the root; the owner writes one message with no code; the agent cites it for either | Refused (Which) |
| T2 | An owner message cited for a second question | Refused (Once) |
| T3 | A non-owner participant's message, or an owner message sent before the "shown" entry | Refused (Who / When) |
| T4 | `stop` → restart → parent resumed | Stopped child still `stopped`, note unchanged, not resumed (CRIT-002) — **corrected**: was "`paused`" |
| T5 | `redirect` while a steer is being enqueued concurrently | The steer is delivered after the new instruction; no deadlock on the record lock's shard |
| T6 | Restart with root → C → D both working | C and D `stopped` (cause `restart`), goals untouched; root gets exactly one entry; D is **not** resumed until C, once resumed, acts on D | — **corrected**: was "`failed(interrupted)` with goals kept" |
| T7 | Child waiting for an answer (grandchild), parent claims `met` | Claim held until the question is answered and the grandchild finishes (D6b) |
| T8 | Child waiting for an answer past 24 h at a heartbeat root | Child `failed(owner_unreachable)`; parent woken with a fatal error |
| T9 | Resumed child whose partial turn sent a mail | The resumed model input contains the resume note and the persisted transcript, not a replay of the original instruction |
| T10 | #1020 and #1027 regression pins | Late steer drained post-turn; no duplicate upward wake after compaction |
| **T11** *(new)* | A working child's turn produces its final answer in the same instant a `stop`/Stop-all reaches it (the race `TestE2E_StopSurvivesRestart` pins) | Child lands `done`, **not** `stopped` (F0929-race); never `working` again by itself after a restart either way |
| **T12** *(new)* | A `met` claim evaluated while a direct child is `waiting for answer` | Claim held — `DirectNonTerminalChildren` (`pkg/agent/goal_record_wiring.go`) must block on `LifecycleNeedsInput`, which it does not today (verified gap, D6b) |
| **T13** *(new)* | A parent's goal is cleared while it has a `stopped` helper with its own active goal | The helper's goal is **not** cleared automatically; the parent is told and can `clear_goal` it (D7b) — this test cannot be written until D7b's open questions are settled |

## Consequences

**[2026-09-29]** The bullets below are the 2026-09-28 record, corrected in place where the founder's later ruling changed the fact (marked); not rewritten wholesale.

### Positive

- The owner-only round trip works at any depth, and the owner answer is bound to **who** (signed-in owner), **when** (after it was shown), **which** (question code or the only open one) and **once** (consumed) — the four gaps the review found in the draft are closed.
- Enforced `stop` / `redirect` with receipts; `steer` cannot start work by accident.
- Queued controls survive a restart; a restart no longer destroys delegated work or its goal, and never undoes a deliberate stop.
- **[corrected]** One vocabulary (working / waiting for answer / stopped / done / failed) across tools, status, UI and completion — now genuinely one, since `stopped` folds what were three separate ideas (`paused`, the terminal side of `cancel`, `interrupted`) into a single alive, resumable state (Vocabulary).
- **[new]** No stop of any kind — single, cascaded, or restart-caused — ever ends a goal (F0929-6); a goal now ends exactly one way (`/goal clear` or the new `clear_goal`, D7b), which is simpler to reason about than the draft's two paths (explicit clear, or `cancel`'s side effect).

### Negative

- **[2026-09-30; F0929-R2-Q1=C] Accepted owner-question bypass risk, single-user posture.** A parent may RESUME or redirect a helper waiting for an `owner_required` answer without obtaining that answer; this withdraws the question with an audit entry and injects a factual "unanswered" notice (D1.7), **but does not verify or block the parent's new instruction**. A parent can falsely claim approval, and the child may act on it. The earlier assurance that an agent can never bypass the owner-only question is therefore narrowed: only the **owner-answer path** enforces Who/When/Which/Once; owner-only question withdrawal is permitted by the founder. In a supported single-user instance Who normally identifies the one owner; When/Which/Once still prevent wrong-message attribution for actual answers. The root agent also chooses which owner message to cite when the owner wrote a code; only the owner's words are copied, not the agent's note.
- **Owner answers need a gateway identity:** a channel conversation (Telegram, Discord, …) has no `GatewayUserID`, so an owner-only question from a channel-rooted tree can only be answered by the owner in the web app (in the child's session) or it expires at 24 h. A dev install running without authentication has an empty owner identity and **cannot** answer owner-only questions (fail-closed). Both accepted by the founder (2026-09-28): see [Founder answers to the open items](#founder-answers-to-the-open-items).
- A new durable artefact (the ledger) with its own crash rules and compaction.
- `stopped` gets its first writer (**corrected**, was `paused`); readers must check the stop note.
- A stopped child's background shells keep running.
- `wait=false` questions and blockers are exempt from every upward cap and could be used to flood a parent's inbox.
- A child waiting for an answer can now hold back a whole tree's "done" for up to 24 hours (D6b) — **unchanged by 2026-09-29**, still true.
- **[withdrawn]** "Interrupted children keep their goal as terminal records with a note — two meanings of `failed` that readers must separate by the note" — this consequence no longer exists: there is no terminal `failed(interrupted)` record any more (D8, F0929-3); a restart-stopped session is an ordinary `stopped` record.
- **[new]** A stopped child no longer blocks its parent's completion by itself (F0929-7) — a parent that never checks its inbox for the stopped-child notice could, in principle, claim `met` while a helper it should have looked at sits stopped indefinitely; this trades a hang (the old failure mode) for a possible silent-to-the-user-but-not-silent-to-the-agent oversight (the notice fires; whether the agent acts on it is the agent's judgment, same as any other inbox message).
- **[new]** D7b (clearing a helper's goal) is unreviewed and has open policy questions (single-hop vs. cascading, precondition on `stopped`) — see D7b.
- Wire changes in seven-plus schemas (the `stop`/`pause` rename adds field renames on top of the draft's count) plus at least four new action schemas (`stop`, `redirect`, `escalate`, `clear_goal`); new SPA work to render receipts, question codes, and the stopped/decide notice.

### Neutral

- Stop all's mechanism, the Stop marker and ADR-091 D8's cascade are unchanged; only the outcome state changed (D7).
- FR-135's opt-in shortcut stays unbuilt.
- #1000's 200-item steering cap and wake bypass are baseline, not changed.

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Auto-forward every `owner_required` question (FR-135 as default) | The founder ruled the parent decides (R-D2) |
| Accept any message from "a person" as the owner answer (draft) | CRIT-001: unrelated, reused and non-owner messages could approve an owner-only action; founder chose strict (F1011-Q5) |
| Park each relaying parent | Freezes a branch per question; a team lead keeps working |
| LLM turn per hop on the way down | N turns for routing, and each hop could alter the owner's words |
| `redirect` under one held lock (draft) | CRIT-003: deadlocks against the turn's own completion write on the same lock shard |
| Carry the pause as a context cancel cause | Nothing carries a cause today, and a crash loses it; the durable note is written first and survives |
| `stop` as a non-cascading terminal cancel | Founder: non-terminal and resumable (R-F1) |
| A ninth lifecycle state | `paused` exists, is non-terminal in `lifecycleToUnifiedStatus` and in the frame enum, and had no writer |
| Keep `steer`-revives | Overruled (R-D4) |
| Restart: mark interrupted children `paused` on the same generation (draft) | Contradicted the founder's Q6 = A (terminal `failed(interrupted)`, goal kept, resume = next generation) |
| Restart: the runtime resumes interrupted workers automatically (draft's Q3 recommendation) | Founder F1011-Q3: the child decides |
| Resume by replaying the original instruction | Re-executes completed side effects (F1011-Q8) |
| Keep ADR-091 D6 for parked descendants (draft's Q4 recommendation) | Founder F1011-Q4: a parked child holds the parent's "done" |

**[2026-09-29] Historical note, not a further rejection:** the row above, "Restart: mark interrupted children `paused` on the same generation (draft)", records what the 2026-09-28 review rejected in favor of a terminal `failed(interrupted)`. The founder's 2026-09-29 ruling (F0929-3) now **picks that rejected alternative** — a restart-stopped session resumes on the **same** generation, not terminal (D8) — because the whole "interrupted" state it was rejected in service of is itself withdrawn. This table is left as the historical record of the 2026-09-28 round; it is not edited to agree with the later ruling.

## Affected components

**[2026-09-29]** This table is the 2026-09-28 record; every mention of `pause`/`paused`/`interrupted`/`follow_up` in it means what Vocabulary now calls `stop`/`stopped`/(withdrawn)/RESUME. It is **not** rewritten row-by-row — the corrected, current list of what changes, including the two gaps this revision found (`DirectNonTerminalChildren`, the missing `clear_goal` action) and a size estimate per area, is the new [Impact on existing code](#impact-on-existing-code-2026-09-29) table below the Founder answers section. Read that table for build purposes; read this one only for the 2026-09-28 history.

| Component | Change |
|---|---|
| `pkg/tools/delegate.go` | `stop`, `redirect`, `escalate`; `respond` gains `owner_message_id`, `question_code` |
| `pkg/tools/delegate_followup.go::executeSteer` | Finished/interrupted → non-error guidance, no revive; paused → queued; 256 KiB pending cap |
| `pkg/tools/delegate_followup.go::executeFollowUp` | Accept paused (same generation) and interrupted (next generation, goal reactivated) |
| `pkg/tools/delegate_park.go` | Owner-answer checks (D1.4), guidance result, relay branch validated against the relay record, native/3P split for the origin |
| `pkg/tools/delegate_run.go::executeCancel` | Drop of an interrupted child (D7) |
| new `stop`, `redirect`, `escalate` executors | D1, D2 |
| `pkg/tools/message_parent.go` | Refusals always returned to the sender naming the cap |
| `pkg/session/message_inbox.go::MessageInboxStore.Append` | D5 exemptions and truncation for handback / fatal error / goal_status / question / decision_request; relays add no depth |
| `pkg/session` | `pause`, `paused_for`, `restart_interrupt` on the record; control/relay ledger; `LifecyclePaused` doc comment |
| `pkg/agent/steering.go` | Queue as a ledger cache; paused recipients not woken; resume note; keeps #1020's post-turn drain and #1000 Q8's wake id-coalescing |
| `pkg/agent/steer_completion.go` | `completionDisposition` reads the `pause` note; `hasRunningOrQueuedDescendant` **[Q2B]** counts paused, parked, interrupted-with-goal; `completeSteeredTurnIfDeferredAtGate` **[Q2B]** refuses `paused`; `parkedQuestions` excludes relayed questions |
| `pkg/agent/goal_triggers.go` / `goal_child_completion.go` **[Q2B]** | D6 notice (two triggers, single-flight); rebuild the completion phase from `Goal.LatestClaim` on resume |
| `pkg/agent/steer_launcher.go::steeredTurnRunContext` | Deadline plus `paused_for` |
| `pkg/agent/steer_frames.go` | `ControlReceipt` transitions; `question_code` on the shown entry |
| `pkg/agent/boot_sweep.go` | Edge-order recovery; interrupted = `failed(interrupted)` + note + goal kept; skip `paused`-with-note; root notice once per boot per tree; `PlanEngine.bootSweep` leaves steered records to `SteerBootRecovery`; park-expiry check |
| `pkg/agent/revive_inbound.go::runInboundTurnWithRevival` | Owner message into a `needs_input` session answers it (D1.4 b); owner message into a paused session resumes it |
| park-expiry sweep (new, periodic + boot) | D1.8 |
| `pkg/audit` | Owner answers and human `/stop` / `/stop-redirect` / `/cancel` |
| `pkg/commands` | `/stop`, `/stop-redirect`; `cmd_cancel.go` comment; exact-set registration test |
| `contracts/`, `pkg/gateway/inboundschemas/SubagentStateFrame.yaml` | D4 wire table (backend-lead edits and regenerates) |
| SPA | Render control receipts and question codes; `/stop`, `/stop-redirect` in the web command surface |
| `docs/internal/specs/cancel-cross-channel-spec.md` | Decision 12 / FR-5 amendment note |
| User docs (owner-only questions) | State that owner-only questions are answered by the signed-in owner in the web app, and that without sign-in they cannot be answered and expire after 24 hours (O2) |

## Review disposition

Every finding of the [grill review](./ADR-20260928-sub-agent-control-plane-review.md), disposed once.

| Finding | Disposition | Where |
|---|---|---|
| CRIT-001 owner answer binds authorship, not answer | **Fixed** (F1011-Q5 strict) | D1.3, D1.4 (Who/When/Which/Once), T1–T3; residual risk in Negative |
| CRIT-002 restart fails or auto-resumes a stopped child | **Fixed** | D6 "Boot" row (both sweeps skip a paused-with-note record; note never rewritten), D8.2, T4 |
| CRIT-003 redirect lock deadlock | **Fixed** | D2 sequence fence; no lock across the interrupt; T5 |
| CRIT-004 D8 contradicts Q6 = A | **Fixed** | D8 rewritten on Q5 / Q6 = A / Q7 = A / F1011-Q3; auto-resume moved to Alternatives; parent-first order; completion-phase rebuild |
| MAJ-001 direct-in-child path does not exist | **Fixed (defined)** | D1.4 (b) through `runInboundTurnWithRevival`, not `WithDelegatePrincipal` |
| MAJ-002 respond to a non-parked relaying parent; `q1'` undefined | **Fixed** | D1.2 (escalate mints the id), D1.5 (relay branch, ack-independent relay record) |
| MAJ-003 "never refused by any cap" vs inbox caps; ruling source | **Fixed** (F1011-Q7) | D5 per-kind cap table; ruling located (`PLAN-2026-09-28.md`, `f040a2a66`); relays add no depth |
| MAJ-004 200 cap scope and bytes | **Fixed** | D5: #1000's cap is baseline; 256 KiB pending per child for delegate steers; injection per `SteeringMode` |
| MAJ-005 precedence table incomplete | **Fixed** | D5: "pending" defined; rows for `follow_up`, `respond`, `escalate`, human message |
| MAJ-006 idle-expiry cancels a paused child | **Fixed** | D6 "Goal" row; `completeSteeredTurnIfDeferredAtGate` refuses `paused` |
| MAJ-007 nudge undoes a person's stop; no trigger | **Fixed** (F1011-Q6: parent may resume) | D6 notice: two triggers, single-flight key, text shows `pause.by` |
| MAJ-008 resume input undefined | **Fixed** (F1011-Q8) | D2 "What a resumed turn runs"; T9 |
| MAJ-009 orphaned workers, boot-id, repeat notices, "housekeeping" | **Fixed** | D8.4 (non-waking entry to parked/paused parents too), `boot_seq`, notify only this boot, D8.8 withdraws "housekeeping" |
| MAJ-010 pause cause has no carrier; races | **Fixed** | D2 durable note first; race table |
| MAJ-011 ledger crash semantics | **Fixed** | D4 crash-semantics table and retention |
| MIN-001 `paused` has no writer | **Fixed** | Context row; D2 (first writer, doc comment) |
| MIN-002 no receipt consumer in SPA | **Fixed** | D4 (3) and Affected components |
| MIN-003 second frame schema copy | **Fixed** | D4 wire table |
| MIN-004 two answer paths for an escalated `self_ok` | **Fixed** | D1.1 first answer wins |
| MIN-005 3P origin | **Fixed** | D1.5 |
| MIN-006 question arrives twice | **Fixed** | D6b: completion hand-backs carry no open questions; `parkedQuestions` excludes relayed |
| MIN-007 idempotent stop spam | **Fixed** | D2 (no ledger line) |
| MIN-008 audit | **Fixed** | D1.9, Affected components |
| MIN-009 downtime counts against deadline | **Fixed** | D8.7 |
| MIN-010 `/stop` vs `/goal stop` | **Fixed** | D9 |
| OBS-001 receipts for every verb | **Accepted in part** | D4: waiting states only for `steer`/`stop`/`redirect`; the others written in their final state |
| OBS-002 three runtime-started turns | **Accepted** | The automatic worker cascade is gone (F1011-Q3); two remain (D6 notice, D8 root notice) |
| OBS-003 replay of old frames | **Accepted** | D4 last paragraph — spec checks the replay path |

## Founder answers to the open items

The correction round left four items open; the founder answered them on 2026-09-28. Recorded here — this is not a further correction round.

| # | Item | Founder's answer | Where it lands |
|---|---|---|---|
| O1 | Owner answers from channel conversations (Telegram, Discord, …) have no gateway identity | **Accept for now.** Owner-only questions in Telegram/Discord-rooted trees are answered in the web app (D1.4 path (b), in the child's session). Linking channel accounts to the owner is a **follow-up item** (FU-1 below) | D1.4, Consequences |
| O2 | A dev install without sign-in has an empty owner identity | **Accept fail-closed, and document it**: the user docs for owner-only questions must state that without sign-in these questions cannot be answered and expire after 24 hours | D1.4, Affected components (docs) |
| O3 | `blocker` messages were not in F1011-Q7 | **Blockers are also never refused**, like questions | D5 cap table |
| O4 | `/steer` alias for the redirect command | **No `/steer` alias.** Founder's model: steer = instruction without stopping; redirect = stop first, then the new instruction. The chat command is **`/stop-redirect`**; the tool action stays `redirect`; `/stop` stays its own command | D9 |

### Follow-up items

| # | Item | Owner |
|---|---|---|
| FU-1 | Link a channel account (Telegram, Discord, …) to the signed-in owner so owner-only questions can be answered from the channel conversation under the same Who/When/Which/Once rules (D1.4) | team-lead to file an issue; design by architect |

## Impact on existing code (2026-09-29)

Every row below was checked against the code in this task (not the 2026-09-28 draft's evidence baseline alone); **Verified** means read directly, **Inferred** means reasoned from a verified neighbor without reading the exact future diff. Size: **S** = one function/file, low risk; **M** = one package, several call sites, needs a test update; **L** = crosses packages and/or contracts, needs a migration/compat decision.

| Area | What must change | Evidence | Certainty | Size |
|---|---|---|---|---|
| **[2026-09-30; MAJ-003]** `pkg/agent/goal_child_completion.go::goalEndingForTerminalState`, `::endSessionOwnedGoalOnTerminal`, `::EndSessionOwnedGoalOnTerminal` | Remove FD1=A pair-end on **all** session transitions (`done`/`failed` as well as stopped/timeout); keep natural goal adjudication and explicit goal clear. `GoalOutcome.ending: stopped_by_user` remains for `/goal clear` and `clear_goal`, **not** for session Stop; `other` remains for independent goal endings, not session death | Read both functions and `contracts/components/schemas/GoalOutcome.yaml::ending` in this pass | Verified | M |
| **[2026-09-30; MAJ-003]** `pkg/agent/steer_cancel.go::reportSteeredSessionTerminalUpward`; `pkg/agent/steer_completion.go::deliverSteeredCompletion` | Remove their respective `endSessionOwnedGoalOnTerminal` calls; a terminal `done`/`failed` is still delivered upward but never clears its session-owned goal | Both call sites inspected directly in this pass | Verified | M |
| **[2026-09-30; MAJ-003]** `pkg/agent/boot_sweep.go::SteerBootRecovery.failInterrupted`, `::recoverSteered`/`::finishFromFinal`; `::PlanEngine.sweepToFailedInterrupted`; `pkg/gateway/gateway_boot.go::EndSessionGoal` binding | Remove `failInterrupted`'s `EndSessionGoal` callback and its gateway binding. Replace both boot sweep terminal-failed paths with stopped transitions; final-report repair (`finishFromFinal`) stays done/failed **without adding** a goal end. **Code correction to the round-2 finding:** there is one direct goal-ending call in `boot_sweep.go` (`failInterrupted`), not two; the second boot path writes terminal failed but currently has no direct goal-end call. Audit both paths nonetheless | `grep -n EndSessionGoal pkg/agent/boot_sweep.go pkg/gateway/gateway_boot.go` → one boot call + binding; `boot_sweep.go::sweepToFailedInterrupted`, `::finishFromFinal` read | Verified | L |
| **[2026-09-30; MAJ-002]** `pkg/gateway/websocket_cancel.go::handleCancel`, `::cancelSteeredSubtree`; `contracts/components/schemas/CancelFrame.yaml`, `contracts/asyncapi.yaml::CancelFrame` | Parse `scope=session|tree`; default absent to session; call `cancelSteeredSubtree` only for tree. Stop/Esc/`/stop` root and child self-only; second-confirmed press/menu/`/cancel` tree; regenerate both wire types | Code and both schemas read in this pass | Verified | L |
| **[2026-09-30; MAJ-004]** `pkg/agent/steer_launcher.go::steeredTurnRunContext` | Persist remaining active-time budget; after a `timeout` stop an explicit RESUME grants fresh full `TimeoutSeconds` starting at resume, other stops resume remaining budget | D6, F0929-Q19=A; launcher named in Context (reverify exact deadline code at implementation) | Inferred | M |
| `pkg/session/lifecycle.go` | `LifecycleState`'s doc comment ("the eight canonical lifecycle states... the ONLY valid values") and the constant set: `LifecyclePaused` → `LifecycleStopped`; `LifecycleCancelled` is retired and folded into `LifecycleStopped` (F0929-2 — "stopped replaces both cancelled and paused"); `LifecycleTimedOut`'s fate is an **open question** (below), not resolved by the founder's ruling | Read: `const (... LifecycleQueued ... LifecyclePaused ... LifecycleCancelled ... LifecycleTimedOut)`, `validLifecycleStates`, `terminalLifecycleStates` (`LifecycleCompleted`, `LifecycleFailed`, `LifecycleCancelled`, `LifecycleTimedOut`) | Verified | L |
| `pkg/agent/steer_completion.go::completionDisposition` | Currently `errors.Is(runErr, context.Canceled)` → `steer.OutcomeInterrupted, session.LifecycleCancelled, "interrupted: the session was cancelled"`. Must instead read the D2 stop note and return `LifecycleStopped` (never a terminal write) when a stop note is current; the plain-cancel-without-a-note case (today's only path) needs its own outcome name, since `steer.OutcomeInterrupted`'s name now collides with the withdrawn "interrupted" vocabulary | Read: `func completionDisposition(...)`, the `context.Canceled` case | Verified (current code) / Inferred (exact new branch) | M |
| `pkg/agent/steer_cancel.go::CancelSubtree` / `::StopSubtree` | Mechanism (`cascade(ctx, sessionID, by, cancelLiveTurns)`, cascade lock, two-pass enumeration, Stop markers) is confirmed unchanged by D7; only the downstream terminal write these functions lead to (in the delegate executor, next row) changes outcome | Read: both functions' doc comments and bodies in `pkg/agent/steer_cancel.go` | Verified | S (this file) / part of the L below |
| `pkg/tools/delegate_run.go` | Three call sites — `t.transitionLifecycle(sessionID, session.LifecycleCancelled, "stopped_by_user")` — must become `session.LifecycleStopped`; each site's surrounding goal-handling must be checked for an implicit `EndSessionGoal` call that F0929-6 now forbids (not verified in this task — **Inferred** risk, not confirmed absent) | `grep -n "LifecycleCancelled" pkg/tools/delegate_run.go` → 3 hits (approx. lines 666, 820, 883) | Verified (call sites) / Inferred (goal side effects at each site) | M |
| **[2026-09-30; MAJ-008]** `pkg/agent/plan_engine.go::StopPlan`, `pkg/agent/plan_engine_play.go::PlayPlan`, member-session dispatch, `pkg/plan/plan.go`, `pkg/task/task.go`, `pkg/tools/stop_plan.go` | **In this PR.** Keep Plan `failed(stopped_by_user)` and Task `failed(cancel_reason: stopped_by_user)` as existing wire states, display Stopped; stop all in-progress step sessions and retain their goals; restart the plan-record generation but **reuse each stopped step's original `Task.SessionID` and history** on explicit plan restart (D8.10). An undispatched step may create a new session. Show partial member-resume errors, never silently start a replacement worker. Add plan stop/restart/crash tests | `pkg/agent/plan_engine.go::StopPlan`, `pkg/agent/plan_engine_play.go::PlayPlan`, `pkg/task/task.go::StatusFailed`, `pkg/plan/plan.go::StateFailed` read in this task | Verified (today's reset/fan-out) / Inferred (new dispatch) | L |
| **[2026-09-30; CRIT-002/MAJ-007]** `pkg/agent/steer_completion.go::hasRunningOrQueuedDescendant`; Q2=B's `pkg/agent/goal_child_completion.go::resumeDeferredGoalAfterDescendantTerminal` | **In this PR (#984 Q2=B).** `needs_input` blocks, `stopped` cuts traversal; every stopped or terminal child transition rechecks any durable deferred parent claim, but does not auto-accept it or wake a stopped parent (D6/D6b). Function named `resumeDeferredGoalAfterDescendantTerminal` was found on local `feat/q2b-one-message-when-done` @ `4c693d186`, **not** the inspected `feat/q2b-on-984` branch; reverify the integrated name before editing | `pkg/agent/steer_completion.go::hasRunningOrQueuedDescendant` read in this task; local branch inspection reported in correction; implementation-branch integration pending | Verified (present function) / Inferred (branch merge) | L |
| **[2026-09-30; MAJ-010]** control ledger, `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered` | **In this PR.** On boot stop interrupted sessions first, keep pending redirect/RESUME `restart_pending`, no dispatch; next explicit RESUME releases originals in seq order with linked receipts and transcript `control_id` dedup (D4/D8.5) | `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered` and D4 read; ledger is new | Verified (boot location) / Inferred (ledger integration) | L |
| `pkg/agent/goal_record_wiring.go::(agentLoopGoalRecordAccess).DirectNonTerminalChildren` | **Verified pre-existing bug**, independent of 2026-09-29 but exposed by it (D6b): the `switch child.State` has cases for `session.LifecycleQueued` and `session.LifecycleRunning` only — **no** `session.LifecycleNeedsInput` case, so a direct child waiting for an answer does not block a `met` claim through this path, contradicting F1011-Q4/F0929-7. Fix: add a `LifecycleNeedsInput` case that always appends the child (no liveness check needed — waiting-for-answer never runs a turn). Do **not** add a case for `LifecycleStopped` (renamed `LifecyclePaused`) — F0929-7 says it must not block here | Read the full function body in this task (`pkg/agent/goal_record_wiring.go:354-379`, worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/q2b-on-984`, branch `feat/q2b-on-984`) | Verified | S |
| **[2026-09-30; MAJ-009]** `pkg/tools/delegate.go` | **In this PR.** Add `stop`, `stop_all`, `redirect`, `escalate`, `resume`, `clear_goal`; retire delegate action `cancel`/`follow_up` (D3/D4/D7b). `clear_goal` is single-target, allowed on working helpers, never cascades; its own notification is distinct from D6's stopped-child notice (Q20/Q21) | Existing enum read in this task: `run, status, inbox, inbox_ack, steer, respond, cancel, follow_up, peek` | Verified (current enum) / Inferred (new implementation) | M |
| `pkg/agent/goal_outcome.go::clearGoalByUser` | Same-session only today (`func (al *AgentLoop) clearGoalByUser(sessionID string, ...)`, called from `pkg/agent/goal_loop_command.go`'s own-session `/goal clear` handling) — D7b needs a cross-session form (parent ends a named child's goal) | Read: function signature and its one call site | Verified | M |
| `pkg/agent/boot_sweep.go::failInterrupted`, `::sweepToFailedInterrupted`, `SteerBootRecovery.recoverSteered` | The entire "interrupted" terminal apparatus these implement is withdrawn (D8); replaced by an ordinary stop-note write with `cause: "restart"`. The `EndSessionGoal`-skip logic in `failInterrupted` becomes unnecessary (there is no goal-ending step in the new stop path to skip) | Read: function signatures exist at the cited names in `pkg/agent/boot_sweep.go` | Verified (functions exist) / Inferred (exact rewrite) | L |
| `pkg/session/lifecycle_bridge.go::lifecycleToUnifiedStatus` | Today: `LifecycleFailed, LifecycleCancelled, LifecycleTimedOut` all map to `StatusInterrupted`; `LifecyclePaused` falls to the `default` (unmapped, "chat stays Active"). Under the new model `LifecycleFailed` should map to a status meaning `failed`, not `interrupted`; `LifecycleCancelled` is retired (folds into `LifecycleStopped`, which — being non-terminal — plausibly keeps falling to `default`, since the SPA is meant to read the stop note directly per D4, not a mirrored terminal status). **Open question:** does `stopped` need its own non-terminal `SessionStatus` value for the SPA's session list, or does the stop note alone suffice? Not decided here | Read: `func lifecycleToUnifiedStatus` body in full | Verified | M |
| `contracts/components/schemas/Session.yaml` | `status` enum is `active / archived / interrupted` — a **third**, wire-level notion of "interrupted" distinct from both the retired lifecycle state and D8's restart case. Must lose `interrupted` as a terminal-failure synonym and gain whatever the previous row's open question resolves to | Read: the `status` property's `enum` block | Verified | L (contract change, regeneration, SPA consumers) |
| `tests/adr091/steered_sessions_test.go::assertStopLandedOn`, `::TestE2E_StopSurvivesRestart` | Hard-fails today (`case rec.Terminal() && rec.State != session.LifecycleCancelled:`) on any terminal state other than `LifecycleCancelled` when a Stop cascade reaches a session that finished first — this is exactly backwards from F0929-race: that race's "final answer wins" branch must land `LifecycleCompleted` (`done`), and the "stop wins" branch must land the new non-terminal `LifecycleStopped`, not a terminal state at all. This is the test the founder's race-rule ruling was written against | Read the full helper and the test's own commentary in this task | Verified | M |
| `pkg/commands/cmd_cancel.go`, `cmd_goal.go` | Comment/doc-string updates for the renamed states and the "Stop all" framing; `GoalClearAliases` unaffected | Read: `pkg/commands/cmd_cancel.go::cancelCommand`, `pkg/commands/cmd_goal.go::GoalClearAliases` exist as named | Verified (existence) / Inferred (exact wording) | S |
| SPA | Every UI surface naming "paused"/"cancelled"/"interrupted" (session pills, the Stop button, any "cancel" confirm dialog) needs the new three-way Stop/Stop-all/RESUME model and the stopped-child decide notice (D6); the double-press Stop-all confirmation (F0929-4) and hidden/resumable helper sessions (issue #1083, F0929-1) are new UI work | Not read in this task (frontend-lead's tree) | Inferred | L |

**Open questions this table surfaces, not answered here (do not build against a guess):**

1. Does `LifecycleTimedOut` fold into `stopped`, `failed`, or stay its own thing? The founder's ruling names five states and does not mention it.
2. Does the Plan record's own state/reason pairing (`failed` + `stopped_by_user`) get a literal new `stopped` value, or only renamed prose? (F0929-8 says "align naming", not "add a state".)
3. Does `stopped` need its own `SessionStatus` wire value (`Session.yaml`), or does the SPA read the stop note directly and never need a mirrored terminal-style status for a non-terminal state?
4. D7b's three open questions (single-hop vs. cascading `clear_goal`; precondition on `stopped`; whether its parent-notice is the same mechanism as D6's).
5. D8 §4's open question: does a restart need an eager, multi-level notice (unlike a single targeted `stop`), or does the lazy per-parent-resumption design still satisfy "parent told"?
6. D6's stopped-child notice: exact trigger/dedup mechanics beyond "must be unconditional, not claim-only" (architect's derivation, not founder-ruled).

## Evidence

| Claim | Evidence | Certainty |
|---|---|---|
| Baseline | worktree HEAD after ff to `f746ddb2a`; base `ee7640c90` | Verified |
| Owner identity carriers | `pkg/session/unified.go::MetaPatch.Owner`; `pkg/bus/types.go` `GatewayUserID` comment ("set ONLY by the gateway webchat WS path") | Verified |
| Park TTL written, never read | `grep -rn "TTLDeadline\|Expired(" pkg --include='*.go'` → only the writer in `message_parent.go` and the definition | Verified |
| Inbox caps per kind | `pkg/session/message_inbox.go::MessageInboxStore.Append` read (depth, body, per-type ceiling unconditional; wake-eligible skip unacked + rate) | Verified |
| #1000 queue change | `git show f040a2a66 -- pkg/agent/steering.go` (`MaxQueueSize = 200`, `item.wake == nil &&`); not an ancestor of `origin/release/v0.1.1` | Verified |
| Q5/Q6/Q7 rulings | `coordination/logs/fix890-opus/lc947-defect1-design-note.md` sections read | Verified |
| `WithDelegatePrincipal` has no production caller | `grep -rn "func WithDelegatePrincipal" pkg/tools` → definition only; review's architect pass confirmed no non-test caller | Verified (definition) / Inferred (callers, from review) |
| Idle-expiry tail **[Q2B]** | `git grep completeSteeredTurnIfDeferredAtGate origin/feat/q2b-on-984` → `pkg/agent/steer_completion.go` | Verified |
| #1020, #1027 exist and are open | `gh issue view 1020/1027` | Verified |
| Second frame schema copy | `ls pkg/gateway/inboundschemas/SubagentStateFrame.yaml` | Verified |

**2026-09-29 revision evidence** (this task; source ruling text `coordination/PLAN-2026-09-28.md`, section "2026-09-29 Founder rulings — session states, stop model", lines 65-73):

| Claim | Evidence | Certainty |
|---|---|---|
| Founder ruling text | `sed -n '65,73p' /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/PLAN-2026-09-28.md` (8 bullets, read verbatim) | Verified |
| 8-state lifecycle, `LifecyclePaused`/`LifecycleCancelled` both exist as distinct constants today | `pkg/session/lifecycle.go` lines 56-88 read (`const (...)`, `validLifecycleStates`, `terminalLifecycleStates`) | Verified |
| `completionDisposition` maps `context.Canceled` to `LifecycleCancelled`/`OutcomeInterrupted` today | `pkg/agent/steer_completion.go::completionDisposition` read in full (lines ~411-450) | Verified |
| `CancelSubtree`/`StopSubtree` mechanism | `pkg/agent/steer_cancel.go` lines 380-430 read | Verified |
| Plan stop writes `LifecycleCancelled` + reason `stopped_by_user` at 3 call sites | `grep -n "stopped_by_user\|failed(stopped" pkg/` → hits in `pkg/tools/delegate_run.go` (×3), `pkg/plan/plan.go`, `pkg/plan/store.go`, `pkg/tools/stop_plan.go`, plus 4 test files | Verified |
| `delegate` tool action enum has no goal-clearing verb | `pkg/tools/delegate.go` — `grep -n "enum.*run.*status" pkg/tools/delegate.go` and the `switch action` block (lines ~845-861) read | Verified |
| `clearGoalByUser` is same-session only | `pkg/agent/goal_outcome.go::clearGoalByUser` signature and its one call site in `pkg/agent/goal_loop_command.go` read | Verified |
| `DirectNonTerminalChildren` has no `LifecycleNeedsInput` case (verified per team-lead's flagged input) | Full function body read at `pkg/agent/goal_record_wiring.go:354-379` in worktree `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/q2b-on-984` (branch `feat/q2b-on-984`, ahead 4 of `origin/feat/q2b-on-984`); not present by this name in this ADR worktree's own tree (`grep -rn DirectNonTerminalChildren pkg/` here returns nothing — it lives only on the Q2B branch) | Verified |
| `assertStopLandedOn` hard-fails any terminal state but `LifecycleCancelled` | `tests/adr091/steered_sessions_test.go::assertStopLandedOn` and `::TestE2E_StopSurvivesRestart` read in full, including the test's own commentary on the async-unwind race | Verified |
| `lifecycleToUnifiedStatus` maps `Failed`/`Cancelled`/`TimedOut` all to `StatusInterrupted` | `pkg/session/lifecycle_bridge.go::lifecycleToUnifiedStatus` read in full | Verified |
| `Session.yaml`'s wire `status` enum includes `interrupted` | `contracts/components/schemas/Session.yaml` lines 70-76 read | Verified |
| `/stop`, `/stop-redirect` not yet built | `ls pkg/commands/` → only `cmd_cancel.go`, `cmd_goal.go` (plus tests) exist; no `cmd_stop*.go` | Verified |
| **Self-check** | Re-read the full revised ADR top to bottom after editing (Status, Founder decisions, Vocabulary, D2, D3, D4/D5 pointers, D6, D6b, D7, D7b, D8, D9, the Consequences/Alternatives/Affected-components pointers, Required acceptance tests, and this Impact/Evidence section) against the task's done-criteria: every mandated section updated, every renamed-state mention checked for the words `paused`/`cancelled`/`interrupted`/`follow_up` and either fixed or deliberately left as marked history, the two team-lead-flagged findings (`DirectNonTerminalChildren`, missing `clear_goal` action) incorporated with verified evidence, ADR format (Context/Decision/Consequences) intact, ADRs cited by title. Open questions were listed, not answered. `git diff --stat` confirms only this one file changed | Verified (this pass) |
