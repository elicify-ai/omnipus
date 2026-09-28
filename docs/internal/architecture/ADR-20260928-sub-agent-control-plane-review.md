# Adversarial Review: ADR-20260928 — The sub-agent control plane

**Spec reviewed**: `docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md` (commit `12268d405`)
**Review mode**: ADR mode, one round (`generic-markdown` structure checks)
**Review date**: 2026-09-28
**Evidence base**: worktree `adr-subagent-control-plane` @ `12268d405` (release tip `ee7640c90`), plus `origin/feat/q2b-on-984` for [Q2B] symbols. Every code claim below was read in this review.
**Verdict**: **BLOCK**

## Executive Summary

The ADR identifies the right five problems and the founder rulings are encoded faithfully except R-Q5 (see CRIT-004), but four decisions as written would ship a defect (the fourth, CRIT-004, added by the independent architect pass: D8 contradicts the founder's Q6=A restart ruling as it is being built on the Q2=B branch); the owner-answer check (D1.4) proves *who wrote* a message, not *that it answers this question*, so an agent can still launder an unrelated "ok" into an owner-only approval; the restart rule (D8) cannot tell a child the operator deliberately stopped from one the restart interrupted, and will auto-resume the former; and `redirect`'s "both halves under one lock" (D2) deadlocks against the turn-completion write. Beyond those, several D-sections contradict code that exists today (inbox caps, the idle-expiry completion tail, the unused human-principal path).

| Severity | Count |
|----------|-------|
| CRITICAL | 4 |
| MAJOR | 11 |
| MINOR | 10 |
| OBSERVATION | 3 |
| **Total** | **28** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] The owner-answer check binds authorship, not answer-to-question — R§8.2 is still bypassable

- **Lens**: Insecurity (Spoofing / Elevation of Privilege)
- **Affected section**: D1.4 table, row "In the root chat (normal)"; Consequences → Positive ("R§8.2 is **stronger** than before")
- **Description**: The runtime accepts any `owner_message_id` that (a) exists in the root transcript, (b) passes ADR-093's "came from a person" predicate (channel not `system`, no steer-wake metadata), and (c) arrived after the relayed question reached the root **inbox**. Nothing binds the message to *this* question, nothing makes it single-use, and the root agent — the untrusted LLM R§8.2 exists to distrust — chooses which message to cite. Three concrete gaps:
  1. **Reuse**: one person message ("yes, go ahead") can be cited against two different pending relayed questions.
  2. **Unrelated message**: the person says "ok thanks" about something else; the agent cites it as the answer to "may I delete the production database?". The runtime copies the text faithfully — the check passes.
  3. **Timing anchor is wrong**: "after the question reached the root inbox" is not "after the person saw the question". A message typed while the inbox entry sat unread qualifies.
  4. **"A person" is not "the owner"**: in a group channel (Discord/Slack/Telegram group) any participant, or a bot/integration posting on a non-`system` channel, passes the predicate.
- **Impact**: An `owner_required` question — which FR-139 reserves for credentials, spend, irreversible tools and out-of-scope actions — is "answered by the owner" with words the owner never meant for it. That is exactly the outcome R§8.2 forbids, now with a runtime stamp (`answer_source: owner`) that makes it look verified.
- **Recommendation**: Add to D1.4: (i) an `owner_message_id` is **consumed once** — the ledger records it and a second use is refused; (ii) the message must arrive **after the root turn that displayed the question to the person was delivered** (anchor on the outbound message id carrying the question, not on the inbox write); (iii) with more than one relayed question open at the root, the runtime refuses unless the person's message is a reply-to/quote of the question message where the channel supports it, or the question is the only one open; (iv) the author must be the session's owner identity (the chat's authenticated gateway user), not any non-`system` sender. State the residual risk in Consequences → Negative instead of claiming R§8.2 is stronger.

---

#### [CRIT-002] Restart recovery will fail or auto-resume a child the operator explicitly stopped

- **Lens**: Incorrectness / Incompleteness
- **Affected section**: D6 row "Boot sweep"; D8.1 ("An in-flight native steered session at boot (not parked, no current Stop marker) is written `paused` with `pause.reason = interrupted_by_restart`"); D8.4
- **Description**: There are **two** boot sweeps that fail steered sessions, and D6 exempts only one. `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered` fails **every** steered record that is `!rec.Terminal() && rec.State != LifecycleNeedsInput` and has no current Stop marker — a `paused` record from `stop` matches that predicate today. D8.1's predicate ("not parked, no current Stop marker") also matches a stop-paused record, because `stop` deliberately writes no Stop marker (D2). So one of two things happens on the first restart after a `stop`:
  - implemented per D6 only → `recoverSteered` still writes it `failed(interrupted)` and ends its goal; or
  - implemented per D8.1 literally → its `pause.reason` is overwritten from `stopped` to `interrupted_by_restart`, and D8.4 then **automatically resumes it** as soon as its parent is resumed.
- **Impact**: The founder's "stop this one and let me think" (R-F1) silently becomes "resume on next restart". A child stopped because it was about to do something harmful restarts that work with no human decision.
- **Recommendation**: Rewrite D8.1's predicate as "state `queued` or `running` (a record already `paused` keeps its `pause` note unchanged)", and add to D6's "Boot sweep" row: "**both** `SteerBootRecovery.recoverSteered` and `PlanEngine.bootSweep` skip a `paused` record carrying a `pause` note; `pause.reason` is never rewritten at boot." Add a named acceptance test: stop → restart → parent resumed → stopped child still `paused(stopped)`.

---

#### [CRIT-003] `redirect`'s "both halves under one lock" deadlocks or is not atomic

- **Lens**: Infeasibility
- **Affected section**: D2, `redirect` paragraph ("one operation under the child's record lock … Because both halves run under one lock and one sequence number, no steer can land between them")
- **Description**: Interrupting a live turn is asynchronous: `pkg/agent/steering.go::Interrupt` signals a graceful stop, and the hard abort (`InterruptSessionHard`) follows after the 3-second window. The turn then unwinds through `completeSteeredTurn…`, which writes the lifecycle record through `LifecycleStore.Mutate` — the **same** per-session striped lock (64 shards, non-reentrant `sync.Mutex`; see the deadlock note in `pkg/tools/delegate_followup.go::executeSteer`). If `redirect` holds that lock while waiting for the turn to end, the turn's own completion write blocks forever — and every other session hashing to the same shard blocks with it. If `redirect` does not wait, the "no steer can land between them" guarantee is false.
- **Impact**: Implemented literally: a gateway-wide stall of 1/64 of all sessions. Implemented loosely: the #1011 F1 race ("not stop+steer as two racy messages") the ADR claims to close stays open.
- **Recommendation**: Replace the lock claim with a sequence-number fence: `redirect` writes its ledger line and `seq` under the lock, releases it, interrupts, and the resume path starts the new turn only with controls whose `seq` ≥ the redirect's `seq` (earlier pending steers are `superseded`, later ones are queued behind the instruction). State explicitly that no lock is held across an interrupt.

---

#### [CRIT-004] D8 (restart) contradicts the founder's Q6=A ruling that is being built right now on the Q2=B branch — two restart designs would ship

- **Lens**: Inconsistency (founder rulings)
- **Affected section**: D8.1, D8.4; Founder rulings row R-Q5; Q3; Alternatives row "Restart: the resumed child decides whether to resume its workers"
- **Description**: The founder's Q5 ruling (verbatim, recorded in `coordination/logs/fix890-opus/lc947-defect1-design-note.md`, section "Founder ruling Q5"): "the top level parent must be informed, he must be able to resume c, in that case c resumes d to be able to reach its goal". Section "Founder ruling Q6 = A" in the same note records the build decision, now in qa-lead RED on `feat/q2b-on-984` (brief `coordination/logs/fix890-opus/codex-q6-red.txt`): (a) boot recovers **parents before descendants**; (b) C and D end `failed` with reason `interrupted` (not cancelled) and C's session goal is **kept**, not ended; (c) the parent gets one entry saying C can be resumed; (d) on the parent's `follow_up` of C, a durable entry "your worker D was interrupted; resume it with delegate follow_up" is injected into C's turn — **"C resumes D itself (no automatic revival)"**; (e) C's held met claim and waiting-for-descendants state are reactivated; (f) "resume" means **a new lifecycle generation** plus reactivated durable goal state (code-shape refinement from `q5-feas.report.md`). The ADR instead writes the record `paused` on the **same generation** (D8.1), has the **runtime** resume interrupted workers automatically (D8.4), recommends that as Q3 option A, and lists the founder's actual build choice as a rejected alternative. It is also silent on parent-first boot order (today `SteerBootRecovery.sessionIDs` is lexical, per `q5-feas.report.md` item 4) and on restoring the process-local Q2=B completion phase after a restart (`q5-feas.report.md` items 6-7).
- **Impact**: The Q2=B PR lands one restart behaviour (failed/interrupted + goal kept + the child resumes its workers by instruction, new generation); this ADR's build then replaces it with another (paused, same generation, automatic cascade). Two RED suites with opposite oracles, a rework of freshly landed code, and a Q3 that re-asks the founder a question he answered today.
- **Recommendation**: Rewrite D8 to adopt the Q6=A design as the baseline (cite the design note), and state explicitly what, if anything, this ADR changes on top of it — for example whether the interrupted record becomes `paused` instead of `failed(interrupted)` once `paused` exists as a non-terminal state (that is a genuine new decision and needs the founder, see founder question Q3 below). Add boot order (parents first) and the Q2=B phase restoration to D8. Move "runtime resumes automatically" to Alternatives with the founder's ruling as the reason. CRIT-002's fix (never rewrite a `stopped` pause at boot) still applies under either design.
- **Evidence**: `coordination/PLAN-2026-09-28.md` ("#984 Q5/Q6: restart-interrupted children keep their goal (paused); top parent told; resumed child resumes its workers (Q6 A)"); `lc947-defect1-design-note.md` sections "Founder ruling Q5" and "Founder ruling Q6 = A" (read); `codex-q6-red.txt` RED brief items 1a-1f (read); `origin/feat/q2b-on-984` @ `106a80062` (Q6 not yet in code — RED in progress).
- **Certainty**: Verified (both texts read). Note the PLAN summary word "paused" is loose; the design note's build spec says `failed`/"interrupted" with the goal kept — the correction round must follow the design note or get the founder to pick.

---

### MAJOR Findings

#### [MAJ-001] The "directly in the child" owner-answer path does not exist

- **Lens**: Incorrectness
- **Affected section**: D1.4 table, row "Directly in the child" ("a human principal's `respond` in the parked session … Principal is the authenticated human (`verifyCallerPrincipal`)")
- **Description**: `pkg/tools/delegate.go::WithDelegatePrincipal` — the only way a human principal reaches `verifyCallerPrincipal` — has **no production caller** (`git grep WithDelegatePrincipal` → only `delegate_adr091_authority_test.go`). A person writing into a parked child's chat goes through the ordinary inbound path (`pkg/agent/revive_inbound.go::runInboundTurnWithRevival`), which never checks `needs_input` and never closes the question. Even if a human principal did reach `respond`, `verifyQuestionAuthority` refuses `owner_required` regardless of principal.
- **Impact**: The ADR presents a second, human-only path as existing; the spec will inherit an unreachable feature (root `CLAUDE.md` Definition of Done).
- **Recommendation**: Either define the path (a human message into a `needs_input` session answers the open question: the runtime closes it with `answer_source: owner`, the message text as the answer, and resumes the turn) and list `revive_inbound.go` in Affected components, or drop the row and say the root chat is the only answer path in this ADR.

#### [MAJ-002] `respond` to a relaying parent contradicts today's `respond` contract; the relayed correlation id is never defined

- **Lens**: Inconsistency / Ambiguity
- **Affected section**: D1.2, D1.4, D1.6 step 4 (`respond(A, q1', owner_message_id=m7)`)
- **Description**: `pkg/tools/delegate_park.go::validateAndLoad` refuses a `respond` unless the **target** session is `needs_input` and parked on that exact correlation id. D1.2 says the relaying parent A does **not** park. So `respond(A, q1')` is refused by today's first check. The worked example introduces `q1'` without saying who mints it, or how it relates to `q1`. `verifyQuestionAuthority` also reads the question through `Inbox.Drain`, which excludes acked entries — a root agent that `inbox_ack`s the relayed question before answering is then refused fail-closed.
- **Recommendation**: Specify: `escalate` mints `correlation_id = <new uuid>` for the relayed entry and returns it; `respond` gains a relay branch that validates against the **relay record** (open, not superseded, `relay_of` chain intact) instead of the target's `needs_input`; relay records are resolved independent of inbox ack state.

#### [MAJ-003] "Done/wake messages are never refused by any cap" contradicts the inbox, and its source is unlocated

- **Lens**: Inconsistency / Incorrectness
- **Affected section**: D5 "Queue ruling (#1000, founder)"; Founder rulings R-1000; Evidence row "#1000 queue ruling … Unknown (source not located)"; Scope row D3
- **Description**: `pkg/session/message_inbox.go::MessageInboxStore.Append` already exempts wake-eligible kinds from the unacked cap and the rate cap, but **still refuses** them on hop depth (`DefaultChildSendMaxDepth = 5`), body size (`DefaultChildSendBodyBytes = 32 KiB`), and the per-child question/blocker ceiling (`DefaultInboxPerTypeCeiling = 20`). "Never refused by any cap" contradicts all three — and the Scope table's D3 row relies on the body cap being enforced. D1 makes this worse: a relayed question adds `relay_note` to a child text that may already sit near 32 KiB; its `Depth` at each hop is unspecified (a 6-level relay trips the depth cap); a parent relaying for many children counts every relay against its own 20-question ceiling at the root. Separately, the ruling is listed as "not re-decided here" while the Evidence table admits no source was found; #1000's body (read in this review) contains no queue ruling. `DefaultInboxUnackedMax` is also 200 — the founder's "200" may have referred to the inbox, not the steering queue.
- **Recommendation**: Confirm R-1000's wording with the founder before the correction round. Then replace "never refused by any cap" with an exact list: which of depth, body, per-type ceiling apply to `handback/question/blocker/error/goal_status`, and what a refused one does (visible error to the sender, never a silent drop). Define `Depth` for relayed entries (recommend: relays do not increment depth; hop count lives in `relay_of`).

#### [MAJ-004] Raising `MaxQueueSize` to 200 changes every steering scope and allows 3.2 MB of pending instructions per child

- **Lens**: Insecurity (DoS) / Ambiguity
- **Affected section**: D5 ("Ordinary `steer` messages are capped at **200** pending per child (today `pkg/agent/steering.go::MaxQueueSize = 10`)")
- **Description**: `MaxQueueSize` is one constant used by `pushItemScope` for **every** scope, including the human-message queue of root chats. The ADR says "per child" but names the shared constant. At 16 KiB per steer, 200 pending = 3.2 MB held in memory and in the ledger per child; with the default `one-at-a-time` drain, the child needs 200 tool boundaries to see them all, and in `all` mode they land in one context injection.
- **Recommendation**: State that the 200 cap applies to delegate `steer` controls in the ledger only, the in-memory chat queue keeps 10; add a total-bytes cap on pending steers per child (e.g. 256 KiB) refused visibly; state how many are injected per boundary.

#### [MAJ-005] The precedence table has undefined columns and missing rows

- **Lens**: Ambiguity / Incompleteness
- **Affected section**: D5 precedence table
- **Description**: (a) "Older pending `redirect`" cannot occur — D2 makes `redirect` atomic and immediate. (b) "Older pending `stop`" is undefined — `stop` is enforced at once; is "pending" the grace window before `applied`? (c) No rows or columns for `follow_up`, `respond`, `escalate`, or a **human chat message** into the child (ADR-091 D5: a human writing is steering) — so "latest control wins" across principals is undefined. (d) Steers queued on a paused child (D3) are delivered "when the child resumes" — but resuming with `redirect` supersedes them (table) while resuming with `follow_up` is unspecified.
- **Recommendation**: Define "pending" per verb (queued = accepted, `applied` not yet reached). Remove impossible cells or say why they occur. Add rows/columns for `follow_up`, `respond`, `escalate`, and human messages. State: resume via `follow_up` delivers queued steers after the follow-up instruction, in `seq` order.

#### [MAJ-006] Idle-expiry and `/goal clear` will terminate a paused child

- **Lens**: Inconsistency
- **Affected section**: D6 row "Goal" ("Idle-expiry applies as to any idle goal"); D6 row "Addressable" ("until cancelled or resumed")
- **Description**: On the Q2B branch, `pkg/agent/steer_completion.go::completeSteeredTurnIfDeferredAtGate` (called from `goal_loop.go`'s idle-expiry sweep and `goal_outcome.go`'s `/goal clear`) sends any steered record that is non-terminal, not `needs_input`, and has no live turn through the completion tail — which writes it `cancelled` with an interrupted hand-back. A paused child matches every one of those conditions.
- **Impact**: A paused child silently becomes `cancelled` after the idle-expiry window, contradicting R-F3 ("idle but alive").
- **Recommendation**: Add `paused` to that function's refusal list, and to D6: "a paused child's goal may idle-expire; its record stays `paused`; only `cancel` or resume changes it." Add it to Affected components.

#### [MAJ-007] The D6 nudge turn invites the agent to undo the operator's stop, and has no defined trigger

- **Lens**: Incorrectness / Ambiguity
- **Affected section**: D6 "Q2=B interaction" paragraph
- **Description**: The claiming session is told "Resume them (redirect/follow_up) or cancel them." When a **person** paused a grandchild (the "let me think" case), the parent LLM is now prompted to resume it immediately — overriding the human. The trigger is also undefined: the ADR says "a paused child produces no events", but the `stop` itself is an event, and a claim may be made *after* the pause. Single-flight "per (claim, set of paused descendants)" does not say whether a second stop of another descendant fires another turn.
- **Recommendation**: Name the triggers (a `stop` applied while an ancestor holds a claim; a claim evaluated while only paused descendants block it). Make the text depend on `pause.by`: when a human paused it, tell the agent to wait for the person or ask them, not to resume. Consider Q4-style founder choice: should a human-paused descendant block the claim at all?

#### [MAJ-008] What a resumed session runs is undefined — risk of re-executing side effects

- **Lens**: Incompleteness
- **Affected section**: D2 "Resuming a paused child"; D8.4
- **Description**: A paused session can have (a) a partial turn in its transcript, interrupted mid-tool, or (b) no turn at all (`running` waiting on descendants). D8.4 resumes children automatically with no new instruction. Today's steered-turn rebuild (`steer_reconstruct.go::reconstructSteeredTurn`, as cited in `delegate_park.go::resumeNative`) replays from the last `user` entry — i.e. re-runs the original instruction. Tool calls that completed in the partial turn (mail sent, file written, an Ask already approved) may be executed again. D10 says irreversible actions are gated before the action, but a previously granted approval is exactly what a replay reuses.
- **Recommendation**: Specify the resume input: the runtime appends a system note ("You were paused/interrupted at <time>; the last completed tool call was <name>; do not repeat completed actions") and continues from the transcript as persisted, never from the last `user` entry. For case (b), resume means: flip to `running`, re-wake on stored descendant messages, no new turn unless messages are pending.

#### [MAJ-009] Restart can orphan paused workers beneath a parked or stopped child

- **Lens**: Incompleteness / Inoperability
- **Affected section**: D8.2, D8.4; Consequences → Negative ("until cancelled or housekeeping")
- **Description**: D8.4's cascade passes only through sessions whose own reason is `interrupted_by_restart`. A worker G paused by the restart under a child C that was **parked** (`needs_input`, preserved) or **stop-paused** is never resumed by the cascade: when C resumes via `respond` or `redirect`, it was not `interrupted_by_restart`. D8.2 reports only per-direct-child counts, so the root agent cannot name G. Paused sessions have no TTL (D6), and "housekeeping" in the Negative section refers to nothing that exists. Also, `<boot-id>` is undefined, and each later restart re-notifies the same still-paused set.
- **Recommendation**: Change D8.4 to: when any session resumes, resume its direct children with reason `interrupted_by_restart` (regardless of the resuming session's own pause reason). Define `boot-id` (e.g. the boot's monotonic counter persisted in the data dir), and notify only sessions paused *by this boot*. Either define a housekeeping rule for paused trees or delete the word.

#### [MAJ-010] `stop`'s "distinct pause cause" has no carrier, and its races have no defined outcome

- **Lens**: Infeasibility / Incompleteness
- **Affected section**: D2 `stop` table, row "live, turn executing"; D4 `applied` definition for `stop`
- **Description**: `Interrupt` and `InterruptSessionHard` take `(id, scope, hint)` and cancel contexts; `completionDisposition` maps any `context.Canceled` or `TurnEndStatusAborted` to terminal `cancelled`. Nothing carries a cause today (`git grep WithCancelCause pkg/agent` → none). The ADR does not say how the pause cause travels, or what happens when: (a) the turn finishes with a final answer inside the grace window (the child is `completed` — what is the stop's receipt?); (b) the turn parks (`message_parent(wait=true)`) inside the window; (c) a `cancel` arrives during a `stop`'s grace window (which cause wins?).
- **Recommendation**: Decide the mechanism (a durable `pause` note written **before** the interrupt; `completionDisposition` reads the note, not the context error). Add a race table: finished-first → stop receipt `superseded` with `reason: finished`; parked-first → `superseded`, `reason: parked`; cancel-during-stop → cancel wins, stop `superseded`.

#### [MAJ-011] The ledger's delivery semantics are deferred, although durability is the ADR's headline

- **Lens**: Incompleteness
- **Affected section**: D4 first paragraph; Consequences → Negative, first bullet ("the spec must define the order…")
- **Description**: The ADR makes the ledger the source of truth and the queue a cache, but decides nothing about crash semantics. After a restart, is a steer in state `delivered` (injected into a turn that died before its transcript was persisted) re-delivered (at-least-once, possible duplicate instruction) or not (at-most-once, possible loss)? Is the ledger line written before or after the lifecycle write for `stop`? These are architecture choices, not spec detail.
- **Recommendation**: Decide in the ADR: ledger line first, then the effect; on boot, any control not `applied`/`superseded` is replayed; `delivered` steers whose injection is not in the persisted transcript revert to `queued`. Add ledger retention (e.g. compact lines older than the last `applied` once the session is terminal).

---

### MINOR Findings

#### [MIN-001] `paused` has no production writer today — the "three meanings" cost is overstated

- **Lens**: Incorrectness
- **Affected section**: Context row "Lifecycle states"; Consequences → Negative, second bullet; Alternatives row "A ninth lifecycle state"
- **Description**: `git grep LifecyclePaused pkg` finds only readers (`boot_sweep.go`, `list_jobs_row.go`, `lifecycle_bridge.go`) and the doc comment in `pkg/session/lifecycle.go`. Nothing writes `paused`. Cancel-soft grace and plan-owner supervision are documented meanings, not implemented ones.
- **Recommendation**: Say so. This ADR becomes the first writer; update the `LifecyclePaused` doc comment in the same change, and restate the Negative bullet accordingly.

#### [MIN-002] Receipts in the side panel have no consumer today

- **Lens**: Incompleteness (reachability)
- **Affected section**: D4 "Where a parent sees receipts" (3); Affected components
- **Description**: No hand-written SPA code reads `steering_receipt` (only generated files under `src/lib/api/generated/`). "The side panel shows it too" is new UI work, not a schema swap, and the Affected components table lists the SPA only under Q1.
- **Recommendation**: Add a row for the SPA receipt rendering, or drop claim (3).

#### [MIN-003] A second copy of the frame schema is not listed

- **Lens**: Incompleteness
- **Affected section**: D4 wire table
- **Description**: `pkg/gateway/inboundschemas/SubagentStateFrame.yaml` also defines `steering_receipt`; `pkg/gateway/replay_followup_span_test.go` references it.
- **Recommendation**: List it in the wire table and Affected components.

#### [MIN-004] Escalating a `self_ok` question creates two answer paths

- **Lens**: Ambiguity
- **Affected section**: D1.1 ("A parent may also `escalate` a `self_ok` question")
- **Description**: The relayed entry is `owner_required`, but the original question stays `self_ok`; the relaying parent (or any ancestor) can still `respond` to the original while the owner answers the relay. R§8.7 also says "a parent may still intercept and answer before the forward completes".
- **Recommendation**: State that escalating closes the parent's own answer path for that question (or that the first answer wins and the other becomes `superseded`).

#### [MIN-005] A relayed question's origin may be an external command-line child

- **Lens**: Incompleteness
- **Affected section**: D1.5 ("resumes the original parked child through today's native resume (`resumeNative`)")
- **Description**: For `rec.Is3P`, `executeRespond` uses `dispatchThirdParty` (a corrective successor), not `resumeNative`.
- **Recommendation**: Say D1.5 uses the same native/3P split as `executeRespond`.

#### [MIN-006] The relayed question can arrive twice

- **Lens**: Incompleteness
- **Affected section**: D1.2
- **Description**: Under ADR-091 D6 a parked descendant's question travels in the parent's hand-back (`steer_completion.go::parkedQuestions`). If A relays B's question and then completes, the root receives it once as a relay and again inside A's hand-back.
- **Recommendation**: Exclude questions with an open relay record from `parkedQuestions`.

#### [MIN-007] Rate-cap exemption lets idempotent `stop` spam grow the ledger

- **Lens**: Insecurity (DoS)
- **Affected section**: D5 last sentence; D2 row "paused → idempotent success"
- **Recommendation**: An idempotent `stop` returns "already paused" without writing a ledger line.

#### [MIN-008] No audit-log requirement for owner answers and human controls

- **Lens**: Insecurity (Repudiation)
- **Affected section**: D1.4, D2
- **Recommendation**: Require a `pkg/audit` entry for every owner-sourced answer to an `owner_required` question (question id, `owner_message_id`, principal) and for human `stop`/`redirect`/`cancel`.

#### [MIN-009] Downtime counts against the deadline after a restart

- **Lens**: Ambiguity
- **Affected section**: D6 "Deadline"; D8.1
- **Description**: `pause.at` is written at boot, so the gap between crash and boot is not in `paused_for`.
- **Recommendation**: State whether the downtime counts (recommend: it does not; use the last persisted activity time as `pause.at`).

#### [MIN-010] Q1 options omit the goal-word conflict

- **Lens**: Incompleteness
- **Affected section**: Q1
- **Description**: The question names `/goal stop` but option A does not say how `/stop` coexists with `GoalClearAliases` containing `stop`.
- **Recommendation**: Add one line to option A about which command wins.

---

### Observations

#### [OBS-001] Receipts for every verb may be more than the problem needs

- **Lens**: Overcomplexity
- **Affected section**: D4
- **Suggestion**: `queued`/`superseded` only matter for controls that can wait: `steer`, `stop`, `redirect`. `cancel`, `follow_up`, `respond`, `escalate` already return a definite tool result. A ledger scoped to the three waiting verbs would cut the schema, the table and the crash-order work by roughly half.

#### [OBS-002] Three new runtime-started turns

- **Lens**: Overcomplexity
- **Affected section**: D6 nudge, D8.2 root notice, D8.4 cascade
- **Suggestion**: Each adds a trigger to test for duplication and ordering. Q3 option B (tell the child, let it decide) removes one of the three; weigh that in the Q3 recommendation.

#### [OBS-003] Replay of persisted frames after the wire change

- **Lens**: Inoperability
- **Affected section**: D4 row `SubagentStateFrame`
- **Suggestion**: The greenfield ruling covers upgrades, but replayed frames on a running dev install will carry `steering_receipt`. Confirm the replay path drops unknown fields rather than failing validation.

---

## Structural Integrity Results (generic-markdown, narrative)

| Area | Assessment |
|---|---|
| Scope clarity | Good — the Scope table maps every #1011 item in or out. |
| Actors | Partial — agents, the human owner and the restart are covered; channel participants who are not the owner, integrations/bots, and task/heartbeat roots (Q2) are not fully covered. |
| Success criteria | **Missing** — the ADR has no acceptance criteria; they are left to the spec. At minimum the three CRITICAL scenarios should be named as required tests. |
| Failure modes | Partial — races of `stop` against completion/parking, crash order of ledger vs record, and cap refusals of relays are open. |
| Implementation detail | Sufficient for most sections; D1's `respond` branch and resume input are underspecified. |
| Assumptions | Mostly explicit; R-1000's source is unverified but treated as settled. |
| Constraints | File-based storage and contract-first wire changes are respected. |

## Test Coverage Assessment

| Check | Result |
|---|---|
| Testability | Most decisions are testable; "latest control wins" is not until MAJ-005 is fixed. |
| Negative scenarios | Missing for: reused/unrelated `owner_message_id`, relay over caps, stop racing completion. |
| Boundaries | 200-steer cap, 16 KiB body, depth 5, per-type 20, 24 h park limit — only the first is discussed. |
| Concurrency | redirect vs steer (CRIT-003), stop vs cancel, concurrent relays at one root — not addressed. |
| Test strategy | Absent (expected for an ADR); regression pins named only for D7 (`goal_ancestor_guard_984_test.go`). |
| Regression risk | ADR-093 D4 amendment is explicit; the idle-expiry tail (MAJ-006) and `recoverSteered` (CRIT-002) are unlisted regressions. |

## STRIDE Threat Summary

| Component | S | T | R | I | D | E |
|---|---|---|---|---|---|---|
| D1 owner-answer check | Non-owner participant counts as owner (CRIT-001) | — | No audit entry (MIN-008) | Child text shown verbatim in group chats | — | Unrelated message laundered into approval (CRIT-001) |
| D1 relay chain | — | `relay_of` built by runtime (ok) | — | — | Per-type ceiling refuses relays (MAJ-003) | — |
| D2 stop/redirect | Human path unreachable (MAJ-001) | — | No audit (MIN-008) | — | Idempotent stop spam (MIN-007) | — |
| D4 ledger | — | Crash order undefined (MAJ-011) | Ledger is the receipt record | — | Unbounded growth (MAJ-011) | — |
| D5 queue | — | — | — | — | 3.2 MB pending per child (MAJ-004) | — |
| D8 restart | — | Stop reason overwritten (CRIT-002) | — | — | Repeated notices per boot (MAJ-009) | Stopped child auto-resumed (CRIT-002) |

## Unasked Questions

1. Who is "the owner" of a root chat in a group channel, and how is that identity checked?
2. Can one person message answer more than one relayed question?
3. What exactly does a resumed turn receive as input, and how are completed side-effecting tool calls kept from running twice?
4. What is the founder's actual wording of the #1000 queue ruling — steering queue or inbox?
5. Which inbox caps (depth, body, per-type) apply to relayed questions and to "never refused" wake messages?
6. When a person pauses a grandchild, may the parent agent resume it?
7. Does a stop-paused child survive a restart as stopped — and is there a test that proves it?

## Independent architect pass — corrections to the grill's own findings

The grill ran as a forked read-only pass; the independent architect reviewer re-checked its load-bearing claims and adds these corrections.

| Finding | Correction |
|---|---|
| MAJ-003, MAJ-004, Unasked Question 4 ("#1000 ruling source unlocated; may mean the inbox") | **Located.** `coordination/PLAN-2026-09-28.md`, "Founder rulings 2026-09-28 (late)": "#1000 Q4: done/wake messages never refused; ordinary steers capped at 200." It is implemented in commit `f040a2a66` ("fix(agent): never refuse upward completion wakes", on `origin/fix/984-followup`, merged into `origin/feat/q2b-on-984`, not yet on release): `pkg/agent/steering.go::MaxQueueSize` 10 → 200 and `pushItemScope` exempts items carrying a wake (`item.wake == nil && len(queue) >= MaxQueueSize`). So the ruling is about the **steering queue**, not the inbox. MAJ-003's substance stands (the inbox's depth, body and per-type ceilings still refuse wake-eligible `question`/`blocker` entries, so "never refused by any cap" is over-broad as the ADR words it). MAJ-004's "raising the shared constant also raises the chat queue" is already a landed-on-branch fact, not this ADR's change; the ADR should cite `f040a2a66` as the baseline, and the per-child byte cap recommendation stands. |
| CRIT-002 | Confirmed: `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered` fails every record with `!rec.Terminal() && rec.State != session.LifecycleNeedsInput` (no Stop) — a `paused` record matches. Under the Q6=A design (CRIT-004) the same trap applies: a stop-paused child must be excluded from the restart treatment. |
| MAJ-001 | Confirmed: `pkg/tools/delegate.go::WithDelegatePrincipal` has no non-test caller on `origin/release/v0.1.1`. |
| MAJ-006 | Confirmed on `origin/feat/q2b-on-984`: `completeSteeredTurnIfDeferredAtGate` refuses only `Terminal()` or `needs_input`, and a live turn; a `paused` record would fall through to the cancel tail. |
| The grill's hand-off note ("the architect then fixes the findings and runs `/grill-spec` again on the same ADR file") | **Wrong.** ADR mode is exactly one grill and one correction round (`.claude/skills/grill-spec/SKILL.md`, "Mode and round detection" item 3; architect agent file section 2). After the correction, any still-open blocking finding goes to the founder, not to a second grill. |

## Next action

Verdict: **BLOCK** (4 CRITICAL).

Review written to: `docs/internal/architecture/ADR-20260928-sub-agent-control-plane-review.md`

This is the ADR's one fixed grill round. Next: team-lead interviews the founder on "Questions for the founder" below, then the ADR's author (architect) makes the one correction round. No second grill round runs on this ADR revision; any CRITICAL still open after the correction is escalated to the founder.

## Questions for the founder

One merged list: the ADR's own four questions (kept, some restated) plus four the review raised. Answer in one line, e.g. "Q1 A, Q2 A, …".

**Q1 — Chat commands and the Stop button** (ADR's Q1, kept).
*Context:* the issue asks for `/stop` (pause just this helper, it can continue later) and `/redirect <new instruction>`. An earlier decision forbids `/stop` as a chat command, and the word "stop" already ends a goal (`/goal stop`). Today's Stop button ends the whole tree of helpers.
*Impact:* decides what a person typing in chat, and pressing Stop, actually gets.
- **A (recommended):** add `/stop` and `/redirect` (plus `/steer`) that pause or redirect only the conversation you are looking at; `/goal stop` keeps meaning "end the goal"; `/cancel` and the Stop button keep ending the whole tree.
- **B:** as A, and also make the Stop button pause-only (ending a whole tree then needs `/cancel`).
- **C:** no new chat commands for now; only agents get stop/redirect.

**Q2 — An owner-only question when no person is in the top conversation** (ADR's Q2, kept).
*Context:* a heartbeat, scheduled run or board task has no person chatting at the top. A helper deep inside asks something only you may answer (spend, credentials, irreversible action).
*Impact:* either the question waits quietly, or you get a new kind of notification.
- **A (recommended):** it waits at the top for the helper's 24-hour waiting limit, then the helper is told, visibly, "the owner could not be reached".
- **B:** also show it to you outside the conversation (a notification or pending-approvals list — new screen work).
- **C:** refuse owner-only questions in such runs at the moment they are asked.

**Q3 — After a restart: what state do interrupted helpers get, and who restarts their own sub-helpers?** (ADR's Q3, restated — see CRIT-004.)
*Context:* today you ruled (Q5/Q6 A) that after a restart the top conversation is told, you can resume a helper, and that helper then resumes its own interrupted sub-helpers. The fix-890 team is building that now as: the helper is marked "failed — interrupted" but keeps its goal, and when resumed it is **told** which sub-helpers were interrupted and resumes them itself. The ADR instead proposes marking them "paused" and having the system resume the sub-helpers **automatically**.
*Impact:* choosing the ADR's version means reworking what the fix-890 team is about to land.
- **A (recommended):** keep what is being built (the helper is told and resumes its own sub-helpers); the ADR follows it. Only change the label from "failed — interrupted" to "paused" later if you want restart-interrupted work to look alive in the lists.
- **B:** the ADR's version — the system resumes interrupted sub-helpers automatically once their parent resumes; the Q6 build is changed to match.

**Q4 — Does a helper waiting for an answer count as "not finished" for the one-message-when-done rule?** (ADR's Q4, kept.)
*Context:* you ruled that a paused helper counts as unfinished, so its parent does not report "done" yet. An earlier decision says a helper that is waiting for an answer does not hold the parent back; its question travels up in the parent's report instead.
- **A:** a waiting helper also holds the parent back — the one "done" message waits for the answer.
- **B (recommended):** keep the earlier decision — the parent reports, and the open question travels with the report.

**Q5 — Who counts as "the owner" when answering an owner-only question?** (new — CRIT-001.)
*Context:* the ADR lets the top agent point at any message a person wrote in the chat as "the owner's answer". In a group chat anyone can write; and the agent could point at an unrelated "ok".
*Impact:* this is the safety rule for spend, credentials and irreversible actions.
- **A (recommended):** only the signed-in owner of that conversation can answer; the message must come after the question was shown; one message answers one question only; with several open questions the answer must reply to the specific question.
- **B:** any person in the conversation may answer (simpler, weaker).

**Q6 — May an agent resume a helper that a person paused?** (new — MAJ-007.)
*Context:* if you press "stop" on a helper to think, the parent agent later sees its work blocked by that paused helper and the ADR tells it to resume or cancel the helper.
- **A (recommended):** no — a helper paused by a person stays paused until a person resumes or cancels it; the parent agent is told to wait or ask you.
- **B:** yes — any ancestor agent may resume it, as today's rules allow for all actions.

**Q7 — "Never refused": which upward messages exactly?** (new — MAJ-003.)
*Context:* your #1000 ruling says done/wake messages are never refused. The message store still refuses a question or blocker when a helper already has 20 open ones, when the message is over 32 KB, or when the chain is more than 5 levels deep. Relaying owner-only questions upward adds to those counts.
- **A (recommended):** "never refused" covers completion and failure reports (hand-back, fatal error, goal status); questions and blockers keep their limits, but a refusal is always shown to the sender, never silent; relayed questions do not count as an extra level.
- **B:** nothing travelling upward is ever refused; the limits are removed for all of them.

**Q8 — When a paused or restarted helper continues, how careful must it be about repeating actions?** (new — MAJ-008.)
*Context:* a helper stopped mid-task may have already sent a mail or written a file. If it continues by re-reading its original instruction, it may do those things again.
- **A (recommended):** it continues from its saved conversation with a system note ("you were paused at …; the last completed action was …; do not repeat completed actions"), never by replaying the original instruction.
- **B:** leave it to the spec to decide.
