# ADR-20260928 — The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume

- **Status:** Proposed, corrected — the one `grill-spec` (ADR mode) round returned **BLOCK** (4 critical, 11 major, 10 minor, 3 observations; [review](./ADR-20260928-sub-agent-control-plane-review.md), commit `f746ddb2a`). This is the **one correction round**: every finding is disposed in [Review disposition](#review-disposition); the founder's answers of 2026-09-28 to the review's eight questions are encoded (F1011-Q1 … Q8, [Founder decisions](#founder-decisions)). No second grill runs; what could not be settled is listed under [Still open for the founder](#still-open-for-the-founder).
- **Build gate:** implementation starts only after the #984 follow-up (#1000) and the Q2=B branch have landed on `release/v0.1.1` (founder, 2026-09-28). Restart resume is built **once, here** — Q2=B ships without it (founder Q7 = A, recorded in `coordination/logs/fix890-opus/lc947-defect1-design-note.md`, "Founder ruling Q7 = A").
- **Date:** 2026-09-28 (draft `12268d405`; correction the same day).
- **Deciders:** Daniel Piatkowski (founder); architect (draft and this correction).
- **ID:** minted with `scripts/new-adr-id.sh "Sub-agent control plane"` → `ADR-20260928-sub-agent-control-plane` (date-and-title scheme, PR #996).
- **Evidence baseline:** `origin/release/v0.1.1` @ `ee7640c90` (draft evidence at `da8579715`; the three commits between are e2e-only). Symbols marked **[Q2B]** are on `origin/feat/q2b-on-984` (unlanded); symbols marked **[#1000]** are in commit `f040a2a66` on `origin/fix/984-followup` (merged into the Q2B branch, not on release). GitNexus was not used; symbols were located with `grep` / `git grep` and read in this task.
- **Amends:**
  - [ADR-093 — An open conversation must keep the ability to delegate](./ADR-093-open-conversation-must-keep-delegation.md) **D4**: `steer` no longer revives a finished child; only `follow_up` does (D3).
  - [ADR-091 — A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md) **D5** (action list gains `stop`, `redirect`, `escalate`), **D6** (a parked descendant now holds back its parent's completion — D6b below), **§7** (restart recovery of steered sessions — D8).
  - `docs/internal/specs/cancel-cross-channel-spec.md` **decision 12** and its **FR-5** ("Chat=/cancel only (no `/stop` alias)"): `/stop` and `/redirect` are added as their own commands (D9, founder F1011-Q1).
  - `docs/internal/specs/unified-goal-plan-subagent-spec.md` **R§8.2 / FR-132 / FR-134**: the owner terminus gets a defined answer path; nothing is loosened (D1).
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

Rulings given before the draft (2026-09-28), and the founder's answers to the review's questions (2026-09-28, relayed by team-lead). Where an answer differs from the draft's recommendation, the answer wins.

| ID | Decision | Lands in |
|---|---|---|
| R-D2 | Owner-only questions end with the human owner; the parent **cascades** them upward (a team lead relaying to its founder) | D1 |
| R-D4 | `steer` = live children only; on a finished child "already finished — use follow_up", nothing starts. Amends ADR-093 D4 | D3 |
| R-F1 | `stop`: non-terminal, pauses the current turn, resumable, **no cascade**. `redirect`: atomic stop + new instruction, **no cascade** | D2 |
| R-F2 | Receipts `queued / delivered / applied / superseded` | D4 |
| R-F3 | Latest control wins; a stopped session is idle but alive (no compute, no slot, deadline paused) | D5, D6 |
| R-C | `cancel` terminal, cascades **down only**, never cancels the parent's goal; `/goal clear` is the user's tool | D7 |
| R-Q2 | Q2=B holds; a stopped/paused child counts as **unfinished** | D6 |
| R-Q5 / Q6=A | After a restart interrupted children are not thrown away; they **keep their goal (paused)**; the top-level parent is told and can resume | D8 |
| Q7=A | Q2=B ships **without** restart resume; restart resume is built once, in this ADR | Build gate, D8 |
| R-1000 | "#1000 Q4: done/wake messages never refused; ordinary steers capped at 200" (`coordination/PLAN-2026-09-28.md`, "Founder rulings 2026-09-28 (late)"); implemented in `f040a2a66` for the steering queue | D5 |
| **F1011-Q1** | **Add `/stop` and `/redirect`** — single sub-agent, non-cascading; `/cancel` and the Stop button keep ending the whole tree. Amends the cross-channel cancel spec's `/stop` ban | D9 |
| **F1011-Q2** | No person at the top (heartbeat / schedule / task): the owner-only question waits until the child's **24-hour limit**, then the child **fails visibly** with "owner could not be reached" | D1.8 |
| **F1011-Q3** | Restart: **the child decides.** After a restart C is told its worker D was interrupted and resumes / redirects / drops D itself; the system does **not** auto-resume D (overrides the draft's recommendation). Parent-first boot order | D8 |
| **F1011-Q4** | A child **waiting for an answer holds back** the parent's "done": the parent waits until the question is answered and the child has finished. **Changes ADR-091 D6** (overrides the draft's recommendation) | D6b |
| **F1011-Q5** | Owner identity **strict**: only the signed-in owner; the answer must come after the question was shown; one message answers exactly one question | D1.4 |
| **F1011-Q6** | A child a person paused **may be resumed by the parent** agent (founder chose this over the review's recommendation) | D2, D6 |
| **F1011-Q7** | Never refused upward: **done and failed reports and questions**; progress keeps its limits; any refusal is a **visible error to the sender**, never silent | D5 |
| **F1011-Q8** | A resumed child continues from its **saved conversation** with a "do not repeat completed actions" note, never by replaying the original instruction | D2, D8 |
| R-F4 | Rollback out of scope | D10 |

## Decision

### Vocabulary

| Word | Meaning | Record |
|---|---|---|
| **live** | a turn executing, `queued` to run, or `running` waiting on its own descendants | `queued`, `running`, no current Stop marker |
| **paused** | stopped by `stop` / `/stop` / the pause half of `redirect` | `paused` + `pause` note `{at, by, seq}` |
| **parked** | asked a question and waits for the answer | `needs_input` |
| **interrupted** | was live when the gateway restarted | terminal `failed`, `failed_reason = interrupted`, plus a `restart_interrupt` note `{boot_seq, at, last_activity_at}`; **goal kept** (D8) |
| **finished** | ended for good until a `follow_up` | terminal without a `restart_interrupt` note, or a current Stop marker (`pkg/session/lifecycle_edge.go::LifecycleRecord.Stopped`) |

"Unfinished" for a parent's completion check (D6, D6b) = live, paused, parked, or interrupted.

### D1 — Owner-only questions: relayed up by each parent, answered only by the signed-in owner

R§8.2's guarantee is kept: **no agent at any level authors the answer to an `owner_required` question.**

**1. The parent chooses.** A child's `owner_required` question reaches the parent's inbox as today (wake-eligible, `pkg/agent/async_notifier.go::wakeableSessionMessageKinds`).

| Move | Verb | Effect |
|---|---|---|
| Relay upward | new `delegate(action="escalate", session_id, correlation_id, note?)` | Relay record + one-hop forward (step 2); returns the **new** relayed `correlation_id` |
| Make it moot | `redirect` (D2) or `cancel` (D7) | The question closes as `superseded` |
| Try to answer | `respond` | Non-error guidance: "This question needs the owner. Use action=escalate to pass it up, or redirect/cancel the child." Nothing changes |

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

**7. Staleness.** If the asker is no longer parked on that question when the answer arrives, nothing is delivered; the `respond` result says so, the relay closes `superseded`, and `m7` is **not** consumed.

**8. The 24-hour limit is enforced (F1011-Q2).** A new park-expiry check (at every boot and on a periodic sweep) reads `NeedsInput.TTLDeadline`, which today nobody reads. At expiry the parked child **fails visibly**: terminal `failed`, reason `owner_unreachable` for an `owner_required` question ("owner could not be reached"), `answer_timeout` otherwise; a fatal `error` goes upward (never refused, D5); open relays close `superseded`. This is the answer for roots with no person (heartbeat, scheduled, task) and applies equally elsewhere. Relaying does not extend the limit. The failed child stays resumable by `follow_up` (F890-1).

**9. Audit (MIN-008).** Every owner answer to an `owner_required` question writes a `pkg/audit` entry: question id, relay chain, `owner_message_id`, owner identity.

### D2 — `stop` and `redirect`: runtime-enforced, non-terminal, no cascade

**Mechanism (fixes MAJ-010).** The pause is carried by a **durable note, written first**: `stop` writes the `pause` note `{at, by, seq}` on the child's record in one `LifecycleStore.Mutate` and releases the lock; **then** interrupts the live turn, self-only (`pkg/agent/steering.go::Interrupt`, escalating to `::InterruptSessionHard` after the existing grace window). `completionDisposition` reads the note, not the context error: a turn that ends while the record carries a current `pause` note becomes `paused` — no terminal write, no upward `handback`/`error`, no goal end, same generation. `paused` gets its first production writer; the `LifecyclePaused` doc comment is updated in the same change (MIN-001). No lock is held across the interrupt.

**`stop`** — `delegate(action="stop", session_id)`:

| Child is | Effect |
|---|---|
| live, turn executing | as above → `paused`; the partial turn stays in the transcript |
| live, `queued` | `paused`; not admitted until resumed; frees its queue position |
| live, `running` waiting on descendants | `paused`; upward messages from its descendants are stored, and wake it only on resume |
| paused | "already paused"; **no ledger line** (MIN-007) |
| parked | nothing: "waiting for an answer — answer, redirect or cancel it" |
| interrupted | nothing: "interrupted by a restart — follow_up, redirect or cancel it" |
| finished | "already finished — use follow_up"; nothing starts |

**Races (MAJ-010).**

| First to land | Result |
|---|---|
| The turn finishes with a final answer before the interrupt reaches it | Child `completed` as normal; the `pause` note is cleared by that terminal write; stop receipt `superseded`, reason `finished` |
| The turn parks (`message_parent(wait=true)`) first | Child `needs_input`; note cleared; stop `superseded`, reason `parked` |
| `cancel` arrives during the stop's grace window | Cancel wins (terminal, D7); stop `superseded`, reason `cancelled` |
| A second `stop` | "already paused" (no line) |

A `stop` reaches only the named session; descendants keep running; background shells are not killed (a parent that wants them gone uses `cancel`).

**`redirect`** — `delegate(action="redirect", session_id, text)`. **Atomicity by sequence fence, not by a held lock (fixes CRIT-003):**

1. Under the child's record lock: assign `seq = R`, write the ledger line `redirect(R, text)` and the `pause` note; release the lock.
2. Interrupt the live turn exactly as `stop` (no lock held).
3. When the turn has ended (`paused` by the note), the resume path starts the new turn with `text` as the newest instruction. At that moment every ledger control with `seq < R` still `queued` is marked `superseded`; controls with `seq > R` stay queued behind the instruction, in `seq` order.

Any steer that arrives while the redirect is in flight gets `seq > R` and is therefore delivered **after** the new instruction — never between the halves, and never lost. The turn-completion write takes the record lock on its own and never waits on the redirect (the deadlock the review found cannot arise). `redirect` on a paused child skips step 2; on a parked child it closes the question `superseded` and resumes with `text`; on an interrupted child it resumes it (D8) with `text`; on a finished child: "already finished — use follow_up".

**Resuming a paused child.** `redirect` (new instruction) or `follow_up`, which widens from "terminal only" to "not live": on a **paused** child it resumes the **same generation**; on an interrupted or finished child it mints the next generation (ADR-093 D4, Q6=A). A message from the session's signed-in owner into a paused child's session also resumes it. **Any ancestor may resume a child a person paused** (F1011-Q6); the child's `pause.by` is shown to it in the D6 notice so it can choose to ask the person first.

**What a resumed turn runs (F1011-Q8, fixes MAJ-008).** The runtime **never** rebuilds the resumed turn from the last `user` transcript entry. It continues from the persisted conversation and appends one system note before the model call:

> You were paused (or interrupted by a restart) at <time>. The last completed tool call was <name> at <time>. Continue from here; do not repeat actions that already completed. <new instruction, if any>

If the paused session had no turn in flight (waiting on descendants), resume flips it to `running` and wakes it only if stored upward messages are pending.

**Who may call them.** Any ancestor and the human operator through `/stop` / `/redirect` (D9). External command-line children (`rec.Is3P`) return a named `not_steerable` result pointing to `cancel` / `follow_up`, like `executeSteer` today.

### D3 — `steer` is for live children only (amends ADR-093 D4)

**Amendment, stated explicitly:** ADR-093 D4's paragraph "Children: a follow-up to a terminal child continues it" applied to `follow_up`/`steer`. From this ADR it applies to **`follow_up` only.** `steer` on a finished child returns

> Session <id> has already finished — nothing was started. Use action="follow_up" to give it new work.

as a **non-error** result (the same reasoning as `executeCancel`'s non-error "already terminal — no action needed": an error here drives agents into retry loops) and starts nothing. `steer` on an **interrupted** child returns the same shape pointing to `follow_up` / `redirect`. `steer` on a **paused** child is accepted as `queued` and delivered after the resume instruction (D5); it does not resume. F890-1 ("always resumable") is untouched: `follow_up` is the resume path.

Affected code: `executeSteer` no longer calls `ReviveStoppedSession`; `follow_up` becomes its only child-path caller.

### D4 — Every control gets a durable receipt; the ledger is the source of truth

**Controls:** `steer`, `stop`, `redirect`, `respond`, `escalate`, `follow_up`, `cancel`. Each accepted control gets a per-child `seq` (monotonic, assigned under the child's lifecycle lock) and a line in a **per-session control ledger** — append-only JSONL beside the lifecycle record, atomic appends (file-based storage, root `CLAUDE.md`).

| State | Meaning (runtime-written, deterministic) |
|---|---|
| `queued` | accepted, durable; not yet in front of the child |
| `delivered` | put in front of the model (injected at a tool boundary or turn start) |
| `applied` | the runtime **enforced** the effect: stop → record `paused`; redirect → new turn started with the instruction; cancel → cascade finished, record terminal; respond → parked turn resumed; escalate → relay closed by an answer; follow_up → turn dispatched |
| `superseded` | replaced before delivery by a newer control (D5), or made moot (reason recorded) |

`steer` has no `applied` state — compliance is the model's, observable through `peek`, never claimed (#1011 F2). That answers #1011 D5: a parent that needs the child to stop now uses `stop`/`redirect`, which are enforced and receipted `applied`. `cancel`, `follow_up`, `respond` and `escalate` reach a definite state synchronously, so their line is written directly in its final state (OBS-001: the waiting states apply to `steer`, `stop`, `redirect`).

**Crash semantics (fixes MAJ-011).**

| Rule | Decision |
|---|---|
| Order | Ledger line **first**, then the effect (lifecycle write, interrupt, injection). A crash between the two leaves an intent the next boot finishes |
| Boot replay | Every control not `applied`/`superseded` is replayed in `seq` order against the recovered state (D8). Replay is **idempotent by `control_id`**: the injection writes the `control_id` into the transcript entry, and a control whose `control_id` is already in the persisted transcript is marked `delivered` instead of re-injected |
| Delivery | At-least-once to the ledger, **at-most-once into the model's context** by that `control_id` check. A `delivered` steer whose injection never reached the persisted transcript reverts to `queued` |
| Retention | Once the session is terminal and has no open relay, lines older than the last `applied`/`superseded` line are compacted into one summary line |

**Queue rebuilt from the ledger.** The in-memory steering queue becomes a cache of the ledger's `queued` steers. **#1020** (a steered child has no post-turn steering drain, so a late steer can strand it until restart) and **#1027** (an upward completion wake can repeat after inbox compaction purges the acked final) are being fixed now; D4 must keep both fixes: the cache refill feeds the same post-turn drain #1020 adds, and ledger replay goes through the wake id-coalescing of #1000's founder Q8 = A (a second enqueue of an already-queued upward-wake id is a no-op) — the ledger never re-enqueues an upward wake whose `message_id` the inbox has already marked consumed. A test pins each.

**Where a parent sees receipts.** (1) The control's tool result: `seq`, `control_id`, state. (2) `status` and `peek` (`pkg/tools/delegate_status.go`) list recent controls. (3) The `subagent_state` frame carries each transition; rendering it in the side panel is **new SPA work** (no reader exists today) — listed in Affected components (MIN-002).

**Wire shape (architect decides the shape; backend-lead edits `contracts/` and regenerates):**

| Schema | Change |
|---|---|
| new `ControlReceipt` | `{seq: int64, control_id, verb: enum[steer, stop, redirect, respond, escalate, follow_up, cancel], state: enum[queued, delivered, applied, superseded], accepted_at, delivered_at?, applied_at?, superseded_at?, superseded_by_seq?, reason?}` |
| `SubagentStateFrame` (`contracts/components/schemas/` **and** `pkg/gateway/inboundschemas/`, MIN-003) | `steering_receipt` replaced by `control_receipt: ControlReceipt` |
| new `DelegateStopAction`, `DelegateRedirectAction`, `DelegateEscalateAction` | in the style of `DelegateSteerAction.yaml` |
| `SessionMessageQuestion`, `SessionMessageDecisionRequest` | optional `relay_of`, `origin` (`{session_id, correlation_id}`), `relay_note` |
| `SessionMessageRespond` | `answer_source: enum[parent, owner]`, `owner_message_id?`, `question_code?` |
| `SubagentMessageFrame` | optional `question_code` (the D1.3 shown entry) |
| lifecycle record (where the SPA reads it) | `pause {at, by, seq}`, `paused_for_seconds`, `restart_interrupt {boot_seq, at, last_activity_at}` |

A dev install replaying old frames that still carry `steering_receipt` must drop the unknown field, not fail validation — the spec checks the replay path (OBS-003).

### D5 — Ordering, precedence and what is never refused

**"Pending"** = accepted, not yet `applied` or `superseded`. For `stop` that is the window between its `pause` note and the turn's end; for `redirect`, between its fence line and the new turn's start.

**Precedence** (MAJ-005). Rows: the newer control. Cells: what happens to an older pending one.

| Newer ↓ / older pending → | `steer` | `stop` | `redirect` | human message into the child |
|---|---|---|---|---|
| `steer` (agent) | both kept, `seq` order | steer waits for the resume | steer queued behind the instruction (`seq > R`) | both kept, `seq` order |
| human message into a live child (ADR-091 D5 — it is a steer) | both kept | waits for resume | queued behind | both kept |
| human message into a paused child | — | resumes (same as `follow_up`); earlier queued steers delivered after it | resumes with the redirect's text first | — |
| `stop` | `superseded` | idempotent | `superseded` (the stop keeps the child paused) | `superseded` |
| `redirect` | `superseded` | the redirect resumes with its text | `superseded` by the newer redirect | `superseded` |
| `follow_up` on a paused child | queued steers delivered **after** the follow-up instruction, in `seq` order | resumes | the redirect wins if older and still pending; the follow-up queues behind it | delivered after |
| `respond` (parked child) | unaffected; delivered after the answer | not applicable (a stop does nothing to a parked child) | the redirect already closed the question; respond refused as stale | unaffected |
| `escalate` | unaffected (acts on the question, not the turn) | unaffected | unaffected | unaffected |
| `cancel` | `superseded` | `superseded` | `superseded` | `superseded` |

`cancel` always wins and is terminal (D7). Controls from different principals (parent agent, grandparent, the owner) are ordered by `seq` alone — the latest accepted control wins, whoever sent it.

**Steering-queue limits (#1000 baseline `f040a2a66`; MAJ-004).** The 200-item cap and the wake bypass are already coded on the #1000 branch; this ADR adds, **for delegate `steer` controls only**, a cap of **256 KiB total pending text per child** in the ledger, refused visibly with the cap named. The chat queue keeps #1000's 200-item cap. Injection per tool boundary follows the configured `SteeringMode` (`one-at-a-time` default). `stop` and `cancel` are exempt from the queue, byte and 6/min rate caps; `redirect` is exempt from the queue and byte caps but keeps the rate and 16 KiB body caps.

**Upward messages that are never refused (F1011-Q7; fixes MAJ-003).** Reconciled with the #1000 ruling and the inbox caps in `MessageInboxStore.Append`:

| Upward kind | Depth cap (5) | Body cap (32 KiB) | Per-child question/blocker ceiling (20) | Unacked cap / rate |
|---|---|---|---|---|
| `handback` (done) | exempt | exempt — over-long text is stored truncated with a visible truncation marker and the full text stays in the child's transcript (ADR-087 — truncation is an outcome, not a silence) | n/a | exempt (as today) |
| fatal `error` (failed), `goal_status` | exempt | exempt, same truncation | n/a | exempt (as today) |
| `question`, `decision_request` | exempt (relays do not add depth, D1.2) | exempt, same truncation | **exempt** | exempt (as today) |
| `blocker` | applies | applies | applies | exempt (as today) |
| `progress`, `checkpoint`, `artifact`, non-fatal `error` | applies | applies | n/a | applies |

**Any refusal is a visible error to the sender**, returned as the `message_parent` tool result naming the cap — never a silent drop. `blocker` is not in the founder's list and keeps its limits (see Still open). A child cannot flood questions without limit in practice: a `wait=true` question parks it, and D1.8 expires parks; `wait=false` questions are the residual risk (Consequences).

### D6 — A paused child is idle, alive, costs nothing — and counts as unfinished

| Aspect | Rule |
|---|---|
| Compute | No turn runs; descendants' upward messages are stored and wake it only on resume |
| Execution slot | None (ADR-091 D6: a slot only while a turn executes) |
| Deadline | Paused: `steeredTurnRunContext`'s deadline becomes `CreatedAt + TimeoutSeconds + paused_for` |
| Addressable | Until cancelled or resumed: `status`, `peek`, `inbox`, `steer` (queued), `redirect`, `follow_up`, `cancel` |
| Boot | **Neither** `SteerBootRecovery.recoverSteered` **nor** `PlanEngine.bootSweep` touches a `paused` record carrying a `pause` note; the note is never rewritten at boot (fixes CRIT-002). A stopped child is still stopped after any number of restarts |
| Goal | Stays active. The idle-expiry sweep may expire the **goal**; the **record** stays `paused` — `completeSteeredTurnIfDeferredAtGate` **[Q2B]** refuses a `paused` record (fixes MAJ-006). Only `cancel` or a resume changes the record |

**Q2=B interaction [Q2B].** `hasRunningOrQueuedDescendant` returns true for a paused (and, D8, an interrupted-with-goal-kept) descendant.

**The claim-blocked notice (fixes MAJ-007).** A paused or interrupted descendant emits no events, so a held claim blocked only by such descendants gets **one** deterministic turn in the claiming session. Triggers, exactly two: (a) a `met` claim is evaluated while every blocking descendant is paused or interrupted; (b) a `stop` is applied, or a boot marks a session interrupted, while an ancestor already holds a claim that is thereby blocked only by paused/interrupted descendants. Single-flight per (claim id, set of blocking descendant ids) under `goalTriggerState.mu`; a different set (a second descendant paused) fires once more. The text names each blocker and who paused it — "paused by the owner at 14:02" versus "paused by agent X" — and the options (resume with `follow_up`/`redirect`, or `cancel`); for an owner-paused child it adds "the owner paused it — consider asking them first". The parent **may** resume it (F1011-Q6).

### D6b — A parked child holds back its parent's "done" (amends ADR-091 D6)

**Amendment, stated explicitly (F1011-Q4):** ADR-091 D6 said "a parked descendant does not block it — its question travels in the parent's `handback` as an open question". From this ADR, a **parked** descendant blocks its ancestors' completion exactly like a live one: `hasRunningOrQueuedDescendant` **[Q2B]** returns true for `needs_input`, and the ADR-091 D6 criteria-free completion ("completes when … a quiet subtree") counts it too. The parent waits until the question is answered **and** the child has finished. No notice turn is needed: the question itself already woke the parent (D1.1), and the 24-hour limit (D1.8) guarantees the block ends. Consequences: `open_questions` in a completion hand-back is always empty on these paths; `parkedQuestions` stays only for a hand-back that is not a completion (e.g. an interrupted report), and excludes questions with an open relay (MIN-006).

### D7 — `cancel` is terminal, cascades down only, never touches an ancestor's goal

Mechanism unchanged (`CancelSubtree` / `StopSubtree`):

- Terminal for the current generation; `follow_up` may start a next one later (F890-1).
- Cascades to every reachable non-terminal descendant, **paused and parked included**; never upward or sideways (ADR-091 D8).
- Ends the cancelled sessions' own goals (FD1=A), never an ancestor's; pinned by #1000's `pkg/agent/goal_ancestor_guard_984_test.go`. Only `/goal clear` (`pkg/agent/goal_outcome.go::clearGoalByUser`) ends a parent's goal.
- **On an interrupted child** (terminal with a kept goal) `cancel` is the **drop**: it ends the kept goal, removes the `restart_interrupt` note's "unfinished" effect, and cascades the drop to that child's interrupted descendants; receipt `applied`, reason `dropped`. This replaces `executeCancel`'s "already terminal — no action" for that one case.
- Supersedes every pending control on the sessions it reaches.
- `stop`, `redirect`, `/stop`, `/redirect` never cascade.

### D8 — Restart: interrupted children keep their goal, the top-level parent is told, each child decides about its own workers (amends ADR-091 §7)

Built once, here (Q7=A); follows the founder's Q5 / Q6 = A rulings with F1011-Q3's "the child decides" (fixes CRIT-004).

**1. Boot order: parents first.** `SteerBootRecovery` processes records in **edge order** — every root, then its children, then theirs (breadth-first over `SteeredBy`) — instead of today's lexical id order (`SteerBootRecovery.sessionIDs`). A parent's recovery state is settled before any child reports to it.

**2. Which sessions are interrupted.** A native steered session whose state is `queued` or `running` and that has no current Stop marker. **Not** interrupted: a `paused` record with a `pause` note (stays paused, note unchanged — CRIT-002), a reconstructable `needs_input` (preserved, as today), a current Stop marker (stays stopped), a standing root (ADR-093 D3). External command-line children keep today's handling (their process is gone; `follow_up` runs a corrective successor, ADR-091 D5).

**3. What an interrupted session becomes (Q6 = A).** Terminal `failed`, `failed_reason = interrupted`, plus the `restart_interrupt` note `{boot_seq, at, last_activity_at}`. **Its session goal is kept** (the `EndSessionGoal` call in `failInterrupted` is skipped for this case; the goal's durable state, including a held `met` claim, is left as it was). `PlanEngine.bootSweep` leaves steered records to `SteerBootRecovery` so the goal is not ended by the second writer. `boot_seq` is a monotonic boot counter persisted in the data dir.

**4. Who is told.**

| Recipient | Message |
|---|---|
| The tree's root (a standing session) | **One** wake-eligible inbox entry per boot per tree, id `<root>:boot:<boot_seq>:interrupted`, listing the root's direct children interrupted **by this boot**: "C was interrupted by a restart and can be resumed with follow_up". Later boots do not re-list sessions interrupted earlier (MAJ-009) |
| An interrupted parent (C, whose worker D was also interrupted) | A durable, **non-waking** entry "your worker D was interrupted by a restart; resume it with follow_up, redirect it, or drop it with cancel", injected into C's next turn when C is resumed, acknowledged only after that delivery |
| A parked or paused parent with an interrupted worker | The same non-waking entry, injected when that parent next runs (its answer arrives, or it is resumed) — so no worker is orphaned under a parent that was not itself interrupted (MAJ-009) |

**5. The root decides; each resumed child decides for its own workers (F1011-Q3).** Nothing resumes automatically. The root agent tells the person; C is resumed with `follow_up` (or `redirect`). Resume mints the **next generation** (ADR-093 D4) and **reactivates the kept goal**, including a held `met` claim and its Q2=B "waiting for descendants" phase — that phase is process-local today, so it is rebuilt from the durable `Goal.LatestClaim` on resume (the gap the fix-890 feasibility report named). C's resumed turn receives the D2 resume note (F1011-Q8) and the injected "your worker D was interrupted" entry; **C** then resumes, redirects or drops D. When D finishes, Q2=B's re-evaluation runs once and one completion message goes up.

**6. Unfinished until decided.** For Q2=B and D6b, an interrupted descendant with a kept goal counts as **unfinished** until it is resumed and finishes, or is dropped with `cancel` (D7). The D6 notice covers a claim blocked only by interrupted descendants.

**7. Deadline without downtime (MIN-009).** Time between `last_activity_at` (the record's last persisted update before the crash) and the resume is added to `paused_for`; downtime never consumes a session's lifetime budget.

**8. Housekeeping.** No automatic deletion is introduced; "housekeeping" in the draft referred to nothing that exists and is withdrawn. A never-resumed interrupted tree stays until cancelled or deleted with its chat (ADR-093 D7).

### D9 — Chat commands `/stop` and `/redirect` (F1011-Q1; amends the cross-channel cancel spec)

**Amendment, stated explicitly:** `docs/internal/specs/cancel-cross-channel-spec.md` decision 12 ("Chat=/cancel only (no `/stop` alias)") and its FR-5 are amended by the founder's answer: `/stop` and `/redirect <instruction>` are added as **their own commands** — not aliases of `/cancel`. `/cancel` keeps FR-5's "no aliases" rule. The `cmd_cancel.go` comment and the spec's registration test (exact-set assertion, review F-20 of that spec) are updated in the same change.

| Command | Where | Effect |
|---|---|---|
| `/stop` | typed in a **sub-agent's** (steered) session | D2 `stop` on that one session; no cascade |
| `/redirect <instruction>` | same | D2 `redirect` on that one session; no cascade |
| either, typed in a root conversation | — | Reply: "`/stop` and `/redirect` act on one sub-agent — open it first. `/cancel` or the Stop button ends this conversation's whole tree." |
| `/cancel`, the Stop button | anywhere | Unchanged: end the whole tree (`handleCancel`) |

The commands are conversation-scoped (#955) and run with the human principal of the signed-in owner (the same `Who` rule as D1.4), writing to the control plane directly, not through the agent's `delegate` tool. `/goal stop` keeps meaning "clear the goal": it is an argument of `/goal` (`GoalClearAliases`), while `/stop` is a separate top-level command, so the parser never confuses them (MIN-010). No `/steer` alias is added (see Still open).

### D10 — Rollback is out of scope

Undo/rewind will not be built (#1011 F4). Resumability covers "continue from here"; irreversible actions are gated **before** the action (the Ask policy, root `CLAUDE.md` Hard Constraint #6), never rewound. D2's resume note is the only related safeguard.

## Required acceptance tests (named here so the spec cannot drop them)

| # | Scenario | Expected |
|---|---|---|
| T1 | Two owner-only questions open at the root; the owner writes one message with no code; the agent cites it for either | Refused (Which) |
| T2 | An owner message cited for a second question | Refused (Once) |
| T3 | A non-owner participant's message, or an owner message sent before the "shown" entry | Refused (Who / When) |
| T4 | `stop` → restart → parent resumed | Stopped child still `paused`, note unchanged, not resumed (CRIT-002) |
| T5 | `redirect` while a steer is being enqueued concurrently | The steer is delivered after the new instruction; no deadlock on the record lock's shard |
| T6 | Restart with root → C → D both live | C and D `failed(interrupted)` with goals kept; root gets exactly one entry; D is **not** resumed until C, once resumed, acts on D |
| T7 | Parked grandchild, parent claims `met` | Claim held until the question is answered and the grandchild finishes (D6b) |
| T8 | Parked child past 24 h at a heartbeat root | Child `failed(owner_unreachable)`; parent woken with a fatal error |
| T9 | Resumed child whose partial turn sent a mail | The resumed model input contains the resume note and the persisted transcript, not a replay of the original instruction |
| T10 | #1020 and #1027 regression pins | Late steer drained post-turn; no duplicate upward wake after compaction |

## Consequences

### Positive

- The owner-only round trip works at any depth, and the owner answer is bound to **who** (signed-in owner), **when** (after it was shown), **which** (question code or the only open one) and **once** (consumed) — the four gaps the review found in the draft are closed.
- Enforced `stop` / `redirect` with receipts; `steer` cannot start work by accident.
- Queued controls survive a restart; a restart no longer destroys delegated work or its goal, and never undoes a deliberate stop.
- One vocabulary (live / paused / parked / interrupted / finished) across tools, status, UI and completion.

### Negative

- **Residual R§8.2 risk:** the root agent still chooses which owner message to cite when the owner wrote the question code; the runtime copies the owner's words, so the worst case is the owner's own words applied to the question they named. The draft's claim that R§8.2 is "stronger than before" is withdrawn as unqualified.
- **Owner answers need a gateway identity:** a channel conversation (Telegram, Discord, …) has no `GatewayUserID`, so an owner-only question from a channel-rooted tree can only be answered by the owner in the web app (in the child's session) or it expires at 24 h. A dev install running without authentication has an empty owner identity and **cannot** answer owner-only questions (fail-closed). Both listed under Still open.
- A new durable artefact (the ledger) with its own crash rules and compaction.
- `paused` gets its first writer; readers must check the `pause` note.
- A paused child's background shells keep running.
- `wait=false` questions are exempt from every upward cap and could be used to flood a parent's inbox.
- A parked grandchild can now hold a whole tree's "done" for up to 24 hours (D6b).
- Interrupted children keep their goal as terminal records with a note — two meanings of `failed` that readers must separate by the note.
- Wire changes in seven schemas plus three new action schemas; new SPA work to render receipts and question codes.

### Neutral

- `cancel`'s mechanism, the Stop marker and ADR-091 D8's cascade are unchanged.
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

## Affected components

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
| `pkg/audit` | Owner answers and human `/stop` / `/redirect` / `/cancel` |
| `pkg/commands` | `/stop`, `/redirect`; `cmd_cancel.go` comment; exact-set registration test |
| `contracts/`, `pkg/gateway/inboundschemas/SubagentStateFrame.yaml` | D4 wire table (backend-lead edits and regenerates) |
| SPA | Render control receipts and question codes; `/stop`, `/redirect` in the web command surface |
| `docs/internal/specs/cancel-cross-channel-spec.md` | Decision 12 / FR-5 amendment note |

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

## Still open for the founder

| # | Item | Why it is not settled | Recommendation |
|---|---|---|---|
| O1 | Owner answers from channel conversations (Telegram, Discord, …) | Strict identity (F1011-Q5) needs a gateway identity that channel messages do not carry, so owner-only questions from channel-rooted trees can only be answered in the web app | Accept for now; bind channel accounts to the owner later |
| O2 | Dev installs without authentication | Empty owner identity → owner-only questions can never be answered, only expire | Accept (fail-closed); document |
| O3 | `blocker` messages | Not named in F1011-Q7; kept under the limits | Treat like questions (never refused) — needs a yes |
| O4 | A `/steer` alias for `/redirect` (#1011 F1 asked for it) | The founder's answer named only `/stop` and `/redirect`, and "steer" means advisory elsewhere | Do not add |

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
