# Adversarial Review: ADR-093 — An open conversation must keep the ability to delegate

**Spec reviewed**: `docs/internal/architecture/ADR-093-open-conversation-must-keep-delegation.md` (commit `c771fc398`)
**Review date**: 2026-09-25
**Review mode**: ADR (generic-markdown), grill round 1
**Verdict**: REVISE

## Executive Summary

The ADR's core diagnosis of #890 holds up against the code: the unconditional parent append in `PublishChildUnderParentLock` is the failing write, and the boot sweep is the writer that makes it common. But the ADR misdescribes what a web Stop leaves on a chat root. The code leaves `running` with a current Stop marker, not `cancelled`. That puts Q2 on a false premise and leaves a second raw-error path (`delegate: dispatch: …`) outside D5. It also names a revival entry point (`ReviveStoppedSession`) that would start a steered turn for a root chat. And D3 leaves the same defect in place for heartbeat standing sessions.

| Severity | Count |
|----------|-------|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 6 |
| OBSERVATION | 3 |
| **Total** | **15** |

Certainty key: **Verified** = read in this worktree at the cited symbol (code read, nothing executed). **Inferred** = follows from the code read, not run.

---

## Findings

### MAJOR Findings

#### [MAJ-001] A web Stop leaves the chat root `running` with a current Stop marker, not `cancelled`. Q2 and "Answer 1" rest on the wrong state

- **Lens**: Incorrectness
- **Affected section**: Context, "Answer 1" last paragraph ("A second way a chat root becomes terminal … a Stop or other `TransitionSession` to `cancelled`"). Q2 Context ("A chat whose record is `failed` or `cancelled` does not revive when the user types").
- **Description**: `pkg/gateway/websocket_cancel.go::handleCancel` calls `cancelSteeredSubtree` first. For any chat that has a lifecycle record, that path goes through `SteerCanceller.CancelSubtree` → `stampStop`, which stamps a Stop marker for the **current** generation on the root. Only after that does `RequestCancel` → `interruptGracefully` → `session.TransitionSession(..., LifecycleCancelled)` run. That write goes through `Mutate` → `persistLocked`, which rejects it: `terminal record (state "cancelled") cannot carry a current-generation stop marker` (`pkg/session/lifecycle.go::persistLocked`). The error is not `ErrLifecycleNotFound`, so the cancel path only logs a warning (`pkg/agent/cancel.go`, "could not transition session to cancelled"). The root stays `running` with a current Stop. The boot sweep's `failed(interrupted)` write fails the same validation, so that record stays `running` + stopped across every restart and logs a warning each boot. Verified (code read).
- **Impact**: After a web Stop, the most common "stopped chat" state is non-terminal with a current Stop marker. Today, a later delegation from that chat does **not** fail at launch. `launchSteered` publishes a child stamped with the Stop, then `launchAndDispatch` calls `Dispatch`, `reserveDispatch` refuses with `ErrDispatchCancelled`, and the tool returns `delegate: dispatch: <raw error>`. A queued child record is left behind, stamped and never run. The founder is asked Q2 about `failed`/`cancelled` records, which is the rarer case. Inferred for the orphan child; Verified for the ordering and the rejection.
- **Recommendation**: Rewrite "Answer 1" and the Q2 Context to name three root states: (1) `failed(interrupted)` from the sweep, (2) `running` + current Stop from a web Stop, (3) `cancelled`, only from cancel paths that do not cascade first (name them, for example `pkg/commands` `RequestCancelForSession`). Add a test-strategy row: "web Stop on a chat root that has delegated → record `running` + current Stop, not `cancelled`" as a characterisation test, and state what D2/D4 do from that exact state.

---

#### [MAJ-002] D5 maps only the launch error. The dispatch error path stays raw

- **Lens**: Incompleteness
- **Affected section**: D5 ("A refusal from D2, and this sentinel if it still escapes, become a short result").
- **Description**: `pkg/tools/delegate_run.go::launchAndDispatch` has two raw-error returns: `delegate: launch: %v` and `delegate: dispatch: %v`. MAJ-001's path (stopped root → stamped child → `ErrDispatchCancelled` / `ErrTerminal` / `ErrStaleGeneration`) comes back through the second one. D5 names only the first. Verified.
- **Impact**: Defect 2 (store text shown to the user) survives for the Stop case. It is the same class of leak, with a different sentinel.
- **Recommendation**: Extend D5 so that `steer.ErrDispatchCancelled`, `steer.ErrTerminal`, `steer.ErrStaleGeneration` from `Dispatch` also map to the plain result. If D2 refuses before publication, state that the dispatch branch should then be unreachable for a stopped parent, and add a test that asserts no stamped orphan child is left behind (lifecycle file count and unified session count unchanged).

---

#### [MAJ-003] D4 names `ReviveStoppedSession` as the revival entry point, but that function dispatches a steered turn

- **Lens**: Incorrectness / Ambiguity
- **Affected section**: D4; Affected components row "`pkg/agent/steering.go::ReviveStoppedSession` and the root-turn admission path".
- **Description**: `AgentLoop.ReviveStoppedSession` does three things: it appends the instruction, calls `SteerCanceller.Revive`, then calls `al.dispatchSteeredSession(ctx, sessionID, newGeneration)`. That last call reconstructs and runs a **steered** turn (`reconstructSteeredTurn`, admission slot, `registerTurnIfAbsent`). A root chat has no steered-by edge, and the human's message is about to start an ordinary root turn anyway. Reusing this function for a root would either start two turns for one message or fail at registration. Separately, the existing mid-turn path (`steering.go::enqueueSteeringFromMessage`) already calls `ReviveStoppedSession` for **any** session with a current Stop, root chats included. A human who types during a Stop's grace window already reaches this path today. D4 does not say which of the two paths owns the root case. Verified (code read); the double-turn outcome is Inferred.
- **Impact**: An implementer who follows the Affected-components row literally ships a root chat that runs a steered-turn reconstruction of the human message, next to, or instead of, the normal turn.
- **Recommendation**: State that for a conversation root, D4 calls `SteerCanceller.Revive` **only**, with no dispatch, from the ordinary inbound-turn path before tool execution. State explicitly that `enqueueSteeringFromMessage`'s revive branch must not run `dispatchSteeredSession` for a record classified `ClassOrdinaryRoot`. Either route it to the same Revive-only call or leave it to the normal turn. Add a test: a human message into a stopped root chat produces exactly one turn and exactly one new generation.

---

#### [MAJ-004] D3 leaves the same defect in place for heartbeat standing sessions (and `continue`-mode scheduled sessions)

- **Lens**: Incompleteness
- **Affected section**: D3 ("Records whose origin is `task`, `delegate`, `scheduled`, `heartbeat` … keep today's sweep"); Q1 options.
- **Description**: `launchSteered` copies the steering session's `UnifiedMeta.Type` into the new root's `Origin.Kind`. A heartbeat session is described as "the eager standing session … so the cron job can continue it … rather than minting a fresh session each run" (`pkg/session/unified.go`, `SessionTypeHeartbeat`). A heartbeat that delegates once therefore gets a `running` root with origin `heartbeat`. Under D3 the next boot sweeps it to `failed(interrupted)`. D4 revives only on a human message, and nobody types into a heartbeat session. From then on, every delegation from that agent's heartbeat fails the #890 way, permanently. `scheduled` sessions run in continue mode have the same shape. Verified for heartbeat reuse and origin copying; Inferred for the permanent failure.
- **Impact**: #890 closes for chats and recurs silently for scheduled and heartbeat automation, with no human to notice or revive it.
- **Recommendation**: Define "conversation root" by whether the session is **long-lived and re-entered**, not by whether it is human-facing: include `heartbeat` (and `scheduled` when it continues a session) in D3's exemption. Alternatively, have D2/D6 treat a terminal long-lived non-human root the way D6 treats a terminal task creator. Add a Q1 option or a sentence that says which one. Add a sweep test row for a heartbeat root.

---

#### [MAJ-005] D2 is ambiguous: refuse, or publish a stamped child, when the parent's Stop is current?

- **Lens**: Ambiguity / Inconsistency
- **Affected section**: D2 ("the launch does not publish a child that is allowed to run on that generation. The existing stamp … stays"); Test strategy row 4 ("No runnable child").
- **Description**: "Does not publish a child that is allowed to run" can be read two ways. (a) Refuse outright, with no child. Then the stamp branch in `launchSteered` becomes dead code for this case. (b) Publish a stamped child that never runs, which is today's behaviour. That leaves a non-terminal orphan record plus a unified session, and the tool's `Dispatch` then fails (MAJ-002). Both readings satisfy "no runnable child". Two engineers would build different things. D5 assumes (a) ("A launch that refuses here returns a mapped tool result").
- **Impact**: Under reading (b), each refused delegation from a stopped chat leaves a queued, stamped child record. Nothing starts it. The boot sweep cannot terminalise it, because persistLocked rejects terminal + current Stop. The record is never pruned (non-terminal). Inferred.
- **Recommendation**: State it plainly. Under the parent lock, if the parent is terminal **or** carries a current-generation Stop, return a typed refusal (for example `steer.ErrSteeringStopped`) before `CreateSessionWithID`, `writeChildMetaAndHistory` or `createLaunchGoal` run. Keep the stamp only for the race it was written for, if one still exists once the refusal is under the same lock. Otherwise say the stamp branch is removed. Test: after a refused launch, no new lifecycle file, no new unified session, no new goal record.

---

#### [MAJ-006] D6 makes tasks from any once-stopped chat unstartable by triggers

- **Lens**: Incompleteness / Inconsistency
- **Affected section**: D6 ("A creator that is merely stopped for its current generation … is not 'gone' — D2 refuses"); Q4.
- **Description**: After MAJ-001, the usual post-Stop state is `running` + current Stop. D3 then keeps that state across restarts, and it stays until a human types in that chat. Under D6, a task whose `OriginSessionID` is that chat is refused on every board start or trigger. A **terminal** creator gets the ordinary-root fallback. So the stricter outcome lands on the milder state. Q4 describes only the "terminal" creator and never mentions this case.
- **Impact**: A user stops one reply in a chat. Later, every scheduled or triggered task created from that chat fails to start, with D5's message, until someone reopens that old chat and types.
- **Recommendation**: Add the stopped-creator case to Q4 as its own row. The ADR can argue either way: D8's "nothing in that tree starts" supports refusing; the "creator gone" rule supports an ordinary root. But the founder must see the choice. If refusal stays, the D5 text for a task start must name the chat to reopen, without an id: for example the chat title.

---

### MINOR Findings

#### [MIN-001] D1 does not say how the store learns "the callback did not change the parent"

- **Lens**: Ambiguity
- **Affected section**: D1; Affected components row for `PublishChildUnderParentLock`.
- **Description**: The callback gets a copy of the parent and returns only the child. `Mutate` uses "set the pointer to nil" as its no-write signal. `PublishChildUnderParentLock` has no signal at all. The store could compare structs (`LifecycleRecord` has pointer fields, so a shallow `==` is wrong and `reflect.DeepEqual` is fragile), or use `existed`, or take an explicit return value.
- **Recommendation**: Specify one mechanism. The simplest one that matches today's only mutation is: append the parent **only when `existed == false`**, and state that a callback which needs to change an existing parent is out of contract. Otherwise, add an explicit `parentChanged bool` return. Name the rollback consequence: with no parent append, the truncate branch runs only in the mint case.

#### [MIN-002] Revival does not restore `UnifiedMeta.Status`

- **Lens**: Incompleteness
- **Affected section**: D4, D7, Consequences.
- **Description**: The sweep calls `reconcileUnifiedMetaStatus` (`boot_sweep.go`), and `TransitionSession` mirrors a terminal state to `StatusInterrupted`. That is the value `GET /api/v1/sessions` returns. `SteerCanceller.Revive` does not touch `UnifiedMeta`. After D4, the lifecycle record is `running` and the session list still says `interrupted`. That contradicts D7's "the record follows the screen" argument.
- **Recommendation**: D4 either resets `UnifiedMeta.Status` to active in the same revival, or states that the stale `interrupted` is accepted and why. Add a test row for it.

#### [MIN-003] Chat-root lifecycle files are never pruned under D3

- **Lens**: Inoperability
- **Affected section**: D3, Consequences / Negative.
- **Description**: `PruneTerminal` / `pruneTerminalOne` remove only **terminal** records past 90 days. Under D3, a chat root is never terminal, so its JSONL file lives forever, even after the chat is deleted (no lifecycle delete path exists; `grep` finds none in `pkg/session`/`pkg/gateway`). `bootSweep` also lists every non-terminal record on each boot, within a time budget. Today the sweep accidentally keeps this bounded.
- **Recommendation**: Name this in Consequences / Negative. Either tie root-file removal to chat deletion, or accept unbounded growth, citing ADR-053's "nothing grows unbounded by omission" and giving the reason.

#### [MIN-004] "Human message" is not defined in code terms

- **Lens**: Ambiguity / Insecurity (Elevation of Privilege)
- **Affected section**: D4, Security and audit ("A human message is what mints the next one").
- **Description**: Root turns start from WebSocket chat, REST, channel adapters (Telegram, Matrix, WhatsApp …) and system wakes (`processSystemMessage`). D4 does not say which inbound discriminator counts. In a `channel` root, such as a group chat, **any** participant's message would revive a generation that the operator stopped. ADR-091 D8 describes revival by the steering human or parent, not by any sender.
- **Recommendation**: Define the predicate. For example: an inbound message whose channel is not `system` and that does not carry steer wake metadata. Decide whether a channel root's revival requires the sender to be an allowed principal for that chat. Add a test: a system wake into a stopped root does not revive it.

#### [MIN-005] D5's wording speaks to the user, but the tool result is read by the model

- **Lens**: Ambiguity
- **Affected section**: D5 bullets.
- **Description**: `ToolResult` text goes to the LLM, which then paraphrases it. "Say that, and do not invent a button" mixes an instruction to the implementer with the text the model receives.
- **Recommendation**: Give the literal text the model should get, written as instructions to the agent. For example: "Delegation is unavailable because this conversation was stopped. Tell the user that sending a new message resumes it." Add a `pkg/tools` test that asserts the absence of a session id, `generation`, `follow_up`, `Play` and `resumed_from`.

#### [MIN-006] Q4 offers option C while the ADR says C is rejected "even if chosen"

- **Lens**: Inconsistency
- **Affected section**: Q4 Recommendation.
- **Description**: "C is rejected by this ADR even if chosen later without a second look" contradicts presenting C as an answer the founder can give.
- **Recommendation**: Remove C from the options table and list it under Alternatives considered, or keep it and say what the ADR does if the founder picks it (for example, reopen D8).

---

### Observations

#### [OBS-001] D6's "unless inside a turn which D4 just revived" clause is redundant

- **Lens**: Overcomplexity
- **Affected section**: D6, test row "Task with `OriginSessionID` of a chat this turn just revived".
- **Suggestion**: Once D4 has revived the record, it is non-terminal, so the rule "terminal creator → ordinary root; otherwise steered, subject to D2" already produces the same outcome. Drop the "just revived" state from the rule. That removes a context flag the executor would otherwise need.

#### [OBS-002] The "every restart" frequency claim rests on code, not logs

- **Lens**: Incorrectness (evidence)
- **Affected section**: Answer 1, "How often".
- **Suggestion**: `runBootSweep` logs through `pkg/logger`, which may not write to `gateway.log`. The four matching lines from 2026-08-12 do not show that no sweep ran on 2026-09-25. Check where the logger actually writes on this machine, or keep the claim labelled as Inferred from code (which the ADR already half-does).

#### [OBS-003] The impact analysis is a manual caller sweep

- **Lens**: Inoperability
- **Affected section**: Blast radius.
- **Suggestion**: The implementer should re-index GitNexus (`node .gitnexus/run.cjs analyze`) and run `impact` on `PublishChildUnderParentLock`, `SteerCanceller.Revive`, `bootSweep` and `enqueueSteeringFromMessage` before editing, as the root CLAUDE.md requires. MAJ-003 shows the manual sweep missed one caller path, the mid-turn revive.

---

## Structural Integrity (ADR mode — narrative)

| Aspect | Assessment |
|---|---|
| Scope clarity | Good. Three defects, explicit non-goals (#883, no persistLocked change). |
| Actors | Chat root, steered worker, task executor, boot sweep are named. Heartbeat and scheduled standing sessions are missing (MAJ-004). System wakes are not distinguished from human messages (MIN-004). |
| Success criteria | The Reachability table and the UAT check are concrete. No criterion covers "no orphan child left behind" (MAJ-005). |
| Failure modes | Sweep and terminal paths are well covered. The web-Stop state is misdescribed (MAJ-001). |
| Implementation detail | Enough to start, apart from D1's mechanism (MIN-001) and D4's entry point (MAJ-003). |
| Assumptions | "Stop → cancelled" is an implicit, wrong assumption (MAJ-001). |
| Founder questions | Four questions map cleanly onto D3/D4/D7/D6, with fallbacks. Q2 and Q4 need the stopped-root case (MAJ-001, MAJ-006). |

## Test Coverage Assessment

| Area | Gap |
|---|---|
| Stop state | No characterisation test for "web Stop leaves `running` + current Stop" (MAJ-001). |
| Orphans | No assertion that a refused launch creates no lifecycle file, unified session or goal (MAJ-005). |
| Dispatch-path text | No test for `delegate: dispatch:` mapping (MAJ-002). |
| Revival turn count | No test that one human message yields one turn and one generation, including the grace-window path (MAJ-003). |
| Heartbeat root | No sweep test row (MAJ-004). |
| Meta status | No assertion on `UnifiedMeta.Status` after revival (MIN-002). |
| Revival principal | No negative test that a system wake does not revive (MIN-004). |
| Concurrency | "Concurrent Stop during launch" is kept. Missing: Stop landing between D4's revival and the delegate call in the same turn. The outcome should be refusal, not a child on the new generation after a newer Stop. |

## STRIDE Threat Summary

| Component | Threat | Note |
|---|---|---|
| D4 revival (channel roots) | Elevation of Privilege | Any group participant can undo the operator's Stop (MIN-004). |
| D4 revival | Repudiation | The revival line records no principal. `Revive` ignores its `steer.Principal` argument (`_ steer.Principal`). Consider recording who revived. |
| D5 tool text | Information disclosure | Today session ids and generation numbers leak to the model/user. D5 fixes the launch path only (MAJ-002). |
| D3 sweep exemption | Denial of service (resource) | Unbounded root-file growth; longer sweep scans (MIN-003). |
| D1 skipped append | Tampering | None: `persistLocked` is unchanged. |

## Unasked Questions

1. What should a delegation do from a chat that is **mid-Stop grace window** (Stop stamped, turn still unwinding)? Refuse, or wait?
2. Stored-not-woken child results for a stopped root are "acknowledged at revival" (`steer_audience.go`, FR-B-013). Does the Revive-only D4 path drain them, or does that need the steered dispatch the ADR is avoiding?
3. After D4, a system wake (a child's result) into a still-terminal root starts a turn with no revival. If the agent then delegates, D2 refuses. Is that intended, and what should the model tell the user in that wake turn?
4. What does D3 do with non-terminal records whose `Origin` is nil (legacy roots)? Sweep or exempt?
5. Is any existing test asserting the parent JSONL line count after a delegation? If so, D1 changes it, and it should be listed under regression impact.
