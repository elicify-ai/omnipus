# Feature Specification: Parent wake and visibility for helper events

**Created**: 2026-10-15
**Status**: Draft
**Status note**: Final correction round 2 of 2 done, 2026-10-15. No review PASS is claimed. There is no third grill.
**Input**: Founder interview 2026-10-15 (decisions D1–D4 below), root-cause matrix `coordination/designs/parent-wake-matrix-20261015.md`, Jarvis silent-stop investigation `coordination/designs/jarvis-silent-stops-20261015.md`, issue #1317, chief plan section 11 (`PLAN-omnipus-L1-v2-20261013.md`), and the R7 UAT packs. Verified against this branch at `a1eb083a4842886699740f1f2c5b93c6ba6b6974`, not against the matrix's cited tree.

This document is the feature spec. No production code is changed by writing it. No new ADR is commissioned: the 2026-10-15 founder interview already settled the policy. Section 16 is the amendment map for the three existing decisions this spec narrows. Those dated ADR corrections are the architect's follow-up edit, not a second grill.

---

## 0. Decisions log

Source for PW-D1 through PW-D7: founder interview, 2026-10-15, chief plan section 11. PW-D13 through PW-D15 are the same interview, recorded after grill round 1 (Source: Interview, 2026-10-15). They replace the earlier "user Stop only" reading. PW-D16 through PW-D22 are architect rulings on the round-1 author prompts. They are not new founder questions.

| ID | Decision | What it rules |
|---|---|---|
| **PW-D1** | A woken parent's turn streams live in the open web chat. | Today the reconstructed turn keeps the stored channel `main`, and the browser only accepts `webchat`. Fix the turn's stream channel. Do not rewrite the stored channel. Issue #1317 is this gap; do not open a second issue. |
| **PW-D2** | The Judge verdict reaches the parent as input before it is acknowledged, and it is a visible row in the **parent** chat. | "Goal met" uses the existing goal-outcome line. "Not verified" and an unmet round that does not end the goal use a parent-chat notice row. The helper chat keeps the row it already has. |
| **PW-D3** | A helper stop, a restart cut-off, and a historical timeout each write a visible row in the parent chat. Restart and historical timeout also wake an idle parent. | The row does not depend on the parent model mentioning it. |
| **PW-D4** | Only outcomes and questions wake the parent. | Finished / hand-back, failed, historical timeout, gateway-restart cut-off, Judge verdict, question, blocker. Progress, checkpoint, and artifact are stored and shown and do **not** wake. A helper Stop is **not** a wake reason (PW-D13). |
| **PW-D5** | One outcome starts at most one parent turn. | A met verdict plus the hand-back that follows it is one turn, not two. A parent that already consumed the result is not started again. |
| **PW-D6** | A native helper cut off by a gateway restart resumes from saved history, and the parent is woken once for that fresh outcome. | Resume continues at the next model decision. A historical tool invocation is not replayed. Identity is the issuing assistant occurrence plus the call id, not the call id alone (PW-D21). The parent's own interrupted instruction is not replayed (PW-D14). An external command-line helper is not auto-resumed and is not replaced. |
| **PW-D7** | Superseded by PW-D13. | Kept only as history. Do not implement the "person only" reading. |
| **PW-D13** | A helper Stop never wakes and never auto-resumes. | Source: Interview, 2026-10-15. Whether the parent agent or a person pressed Stop, it is not a wake reason: no parent wake, no join into a live parent turn, no boot replay wake, no helper auto-resume. A visible stopped row in the parent chat may stay. Stopping the parent itself does not resume the parent and does not, by itself, cancel a helper the Stop did not select. Q-STOP is closed. |
| **PW-D14** | A restart-stopped parent may be woken by the helper's fresh outcome. | Source: Interview, 2026-10-15, Q1 A. When the same gateway restart cut off both, the resumed helper's fresh outcome wakes that restart-stopped parent. The input is the helper event, not a replay of the parent's interrupted instruction. An intentional Stop is never overridden. |
| **PW-D15** | A parked question card holds the outcome until the person answers. | Source: Interview, 2026-10-15, Q2 A. Show the helper row immediately and keep the report durable. Deliver it together with the person's correlated answer when the parent continues. The card stays valid. No extra outcome-only parent turn while the card is unanswered. A tool approval that is still inside a live turn is different: that outcome joins the live turn. |
| **PW-D16** | Save the input, then place it into model history only at the safe fence. | Round-2 correction. Two facts, one archive. **Save:** a chat-membership record (`archiveBackend` does not accept model membership on a transcript append). **Place:** `PlaceSavedInput` once, at the ordinary consumption fence, after any in-flight tool result. A held, stopped, or already-polled outcome is saved for the person and is not placed again. A bus publish is neither save nor placement. |
| **PW-D17** | Each logical event has its own wake identity. | Round-2 correction. Consumption is per event, on the recipient. A met verdict, its hand-back, and a task completion share one ending id, the existing `<child>:<generation>:final`, which is what `recordPolledFinalConsumed` already consumes. A restart transition, a later finish, a question, a blocker, and a Judge round do not share that id. Generation alone is not an identity. |
| **PW-D18** | Repair keeps its own payload after the inbox is acknowledged. | Round-2 correction. Inbox ack and `compactLocked` must not be the only copy. Each event family has a source that outlives acknowledgement. Repair does not start a second turn. |
| **PW-D19** | Stop authority is the lasting state, not only the current marker. | In-flight Stop, accepted-but-unlanded Stop, and landed intentional Stop all refuse a wake. A cleared marker on a landed Stop is still a Stop. A restart stop is not an intentional Stop (PW-D14). |
| **PW-D20** | Row identity is the source event, not generation alone. | Stop-family rows use the landed stop sequence. Verdict rows use goal id and round. Not-verified uses that conclusion's own id. |
| **PW-D21** | A tool call is an occurrence, not a transcript-wide id. | Resume skips a historical invocation (issuing assistant occurrence plus call id). A later assistant occurrence may reuse the same provider call id. |
| **PW-D22** | No new ADR. | The founder settled the policy. Section 16 names the three decisions this spec narrows and the sentences that stay. |
| **PW-D8** | One reusable wake entry. | Scheduled-task creator notices (that spec's FQ-4) call this entry. They do not grow a second wake implementation. This spec does not implement FQ-4. |
| **PW-D9** | Contract shape. | Ending verdicts reuse `GoalOutcome`. Other parent-chat rows are a new `helper_event` system subtype and frame. No new REST resource. |
| **PW-D10** | Idle-checker boundary. | The goal idle window and the no-goal idle checker are a later unit. This spec only names the wake entry they must call. It does not change their timers. |
| **PW-D11** | Do not edit the human-message steering enqueue. | Chief's rooms unit (WU-15) owns `enqueueSteeringFromMessage`. This unit calls the existing live-turn enqueue from a **new file**. |
| **PW-D12** | No substitute recipient. | A missing, hidden, or removed parent is not replaced by another session or another workspace. |

---

## 1. Current behaviour (verified on this branch)

GitNexus MCP is not available in this session. Callers were found by search. No symbol was edited, so no impact run was required. Certainty is in the evidence table of the author's report.

### 1.1 What already wakes an idle parent

An upward delivery appends the helper's message to the parent's inbox, writes a side-panel frame on the parent transcript, then either joins the parent's live turn or starts a new one.

| Fact | Where | What a person observes today |
|---|---|---|
| Idle completion wakes | `pkg/agent/steer_audience.go::SteerUpwardDeliverer.wakeOwnerOrStore` does not look at `completed`. It refuses a wake only when the parent record is missing or the parent has a Stop marker for its current generation. | The parent model runs a new turn. R7 showed this. |
| The new turn's stream channel is the stored channel | `pkg/agent/steer_reconstruct.go::reconstructSteeredTurn` sets the turn channel from session metadata. It forces `webchat` only when the session is itself a helper (`SteeredBy` set). A main session stays on `main`. | The open web chat, which only accepts `webchat` (`pkg/gateway/websocket_streamer.go::WSHandler.GetStreamer`), shows nothing until reload. |
| The wake event's own channel is not the turn channel | `pkg/agent/async_notifier.go::asyncNotifierImpl.Notify` publishes the bus message on channel `system`. The reporting target's channel is not copied onto the reconstructed turn. | Issue #1317's phrase "the wake uses the reporting-target channel" does not match this code. The matrix's correction does. |
| Reporting target for a main session is already `webchat` | `pkg/agent/steer_launcher.go::reportingTargetFor` returns `webchat` plus the session id when metadata has no peer. The constant is `steerReportingSelfChannel`. | R7's edge said `webchat` and the turn still ran on `main`. |
| "Goal met" is written on the helper only | `pkg/agent/goal_outcome.go::AgentLoop.recordGoalOutcome` appends `system_subtype: goal_outcome` to the session id it is given. The met path passes the helper session (`pkg/agent/goal_triggers.go`, the `clearGoalWithOutcome` call). | The parent chat has no "Goal met" row. Reload cannot invent one. |
| The Judge transcript is a different write | `pkg/agent/goal_loop.go::AgentLoop.writeGoalVerdictTranscript` writes a `judge_verdict` entry on the helper and then calls `deliverGoalVerdictUpward`. It is not the "Goal met" card. | The matrix names both writers as the card. Only `recordGoalOutcome` is the card. |
| A met session-goal verdict does not itself wake | `pkg/agent/steer_frames.go::AgentLoop.deliverGoalVerdictUpward` sets `SuppressWake` when the verdict is met, the goal is session-owned, and the child has a parent edge. | The hand-back is the one wake. |
| That verdict is acknowledged before the parent model can read it | `pkg/agent/goal_triggers.go` calls `pkg/agent/goal_child_completion.go::AgentLoop.ackMetVerdictEntry` once the hand-back wake has been published, not after the parent turn reads the inbox. The wake body is `pkg/agent/steer_audience.go::deliverySummary` of the hand-back alone ("A delegated session handed back…"). | The parent model can honestly say the inbox had no verdict. R7. |
| If the hand-back does not wake, the verdict wakes once as fallback | Same site calls `wakeMetVerdictEntry` when the hand-back did not wake. | Still one wake, but only the short "goal verdict: met" summary. |
| Stop, restart, and historical timeout do not wake an idle parent | `pkg/agent/stopped_notice.go::AgentLoop.parentWorkingForStoppedNotice` returns true only for `running`, `queued`, or no lifecycle record. `completed` returns false. `pkg/agent/steer_completion.go` publishes the notice and not a final hand-back for a stopped commit. | The notice sits in the inbox. The parent chat gains no row. |
| A missing parent record is "do not wake" on the outcome path and "do wake" on the stop-notice path | `wakeOwnerOrStore` treats any failed load as stored-not-woken. `parentWorkingForStoppedNotice` treats not-found as working. | The two gates disagree. |
| Progress, checkpoint, and artifact do wake when they are stored | `pkg/session/message_inbox.go::classifyEnvelope` sets `WakeEligible` for those three kinds. `WakeParentAlways` does not apply the legacy 15-second / 4-per-hour cap (`WakeParent` does; production delivery does not call it). | A noisy helper can start a parent turn per progress line. |
| Those three are still capped at the inbox | The same classifier leaves `CapExempt` false. `MessageInboxStore.Append` then applies the unacked cap and the per-minute rate. A refused append is not stored and does not wake. | The matrix's "no cap" is true only of the wake debounce, not of the inbox. |
| The tool comment that says checkpoint and artifact are not wake-eligible is stale | `pkg/tools/message_parent.go::outcomeForKind` | The classifier above is what delivery actually uses. |
| A restart does not resume the helper | `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered` marks a running or queued helper stopped with cause restart, then replays the parent notice. The file header of `pkg/agent/steer_reconstruct.go` says boot does not reconstruct the turn. | The helper stays stopped until someone resumes it. A helper that died before the process wrote a chat error can show no explanation in its own chat (Jarvis case S3). |
| A new timeout is not produced | `pkg/steer/types.go` documents `OutcomeTimedOut` as historical (issue #1306). | An old timeout stop still on disk is only a stop notice. |
| The open chat draws a goal-outcome row only from a `goal_outcome` frame or the matching stored entry | `src/store/chat/slices/replay-and-status-frames.ts` case `goal_outcome`, `src/lib/goalOutcome.ts::goalOutcomeHeadline` | Copy today is "Goal met — …", "Goal not met after N tries — …", "Goal stopped by you — …", or "Goal not met — …". |
| A side-panel line is not that row | `src/store/chat/slices/frames.ts` case `subagent_message` folds the text onto the worker span. | "A delegated session's goal verdict: met" becomes a finished header, not a verdict line. |
| "Not verified" does not end the goal | `pkg/agent/judge_conclusion.go::notVerifiedConclusion`. User doc `docs/goals.md` says the goal stays active. | The parent can receive the conclusion in its inbox. The parent chat does not get a lasting row from this spec's point of view until this change. |
| A user Stop of the parent already blocks an outcome wake | `wakeOwnerOrStore` returns stored-not-woken when `Stop.Generation` equals the current generation. | That refusal does not exist on the stop-notice path, because that path never wakes an idle parent today. |
| Polling the result first cancels the later completion wake | `pkg/agent/steering.go::AgentLoop.recordPolledFinalConsumed` | Leave this guard in place. |
| A main that has never had a turn has no lifecycle record | `pkg/agent/ordinary_root_record.go::AgentLoop.ensureOrdinaryRootRecord` creates the same record a first turn would, and refuses to invent one for a helper. | The scheduled-task spec already depends on this for a never-turned creator. |

### 1.2 Matrix claims that are wrong or too broad on this branch

| Matrix claim | Verdict on `a1eb083a4` |
|---|---|
| Completion of an idle parent wakes; a met verdict does not, until the hand-back | True |
| The open chat misses the turn because the turn channel is metadata `main`, not the reporting target | True. #1317's mechanism sentence is the part that is wrong. |
| "Goal met" exists only on the helper | True. The writer is `recordGoalOutcome`, not `writeGoalVerdictTranscript`. |
| Stop / restart / historical timeout do not wake `completed` | True |
| Progress wakes with no cap at all | Too broad. The wake debounce is skipped. The inbox rate and unacked caps still refuse the append. |
| Checkpoint and artifact are described as not wake-eligible in `outcomeForKind` | The comment is stale. They are wake-eligible once stored. |
| A reload paints the model's reply | True of the messages API once the turn has persisted. Not evidence that the live socket showed it. |

---

## 2. Actors, problem, scope

**Actors.** The person watching a main chat in the web app. The parent agent (the main that delegated). The helper (a steered session). The Judge. A later scheduled-task run that must wake its creating agent. Gateway boot after a restart.

**Problem.** The parent is often already idle when the helper ends. The product does wake that parent for a normal finish, but the person does not see the turn in the open chat, does not see the Judge's verdict in that chat, and is not told when the helper was stopped, restarted, or timed out. Progress lines, the other way, can wake the parent with no bound.

**In scope.**

- Live streaming of a woken ordinary-root turn into the open web chat, including a second browser on the same session.
- The verdict as parent-turn input, acknowledged only after that input is durable, plus a visible parent-chat row.
- Parent-chat rows for helper stop, restart, and historical timeout.
- Waking an idle parent for outcomes and questions only, one turn per outcome.
- Auto-resume of a native helper cut off by restart, without re-running a completed tool, plus one parent wake.
- The reusable wake entry the scheduled-task creator notice will call.
- Reload showing the same rows the live chat showed.
- User-facing doc updates named in section 14.

**Out of scope.**

- The goal idle window (`pkg/agent/goal_triggers.go` timers and sweep) and the no-goal idle checker, including the 25-second quiet window and the "finished but unread" signal. Later unit. Boundary in section 8.
- Changing who a scheduled task runs as, or the creator-notice wording. That spec owns the wording. This spec owns the entry it calls.
- Rooms (WU-15), except the non-overlap rule PW-D11.
- Auto-resume of the person's own main chat. A restart of that chat stays as `docs/agents.md` describes it today: the next message continues it. Only a helper is auto-resumed.
- Auto-resume of an external command-line helper. Today's rule stands: if the tool's conversation was not retained, resume is refused and a new delegation is required. This unit writes the parent row and wakes the parent; it does not invent a new worker.
- A new Judge, a new goal state, or a rename of the existing "Goal met" / "Goal not met" / "Not verified" sentences.
- Reintroducing a work-time budget or a UI-disconnect watchdog.

---

## Available Reference Patterns

No pattern in `docs/reference/go-implementation/` applies. This feature extends the existing steering wake and chat transcript. It does not add an account, payment, mailbox, or storage service.

---

## Existing Codebase Context

GitNexus was not available. The table is from direct reads. Risk is **Inferred** from the callers found by search, not from a graph impact run.

### Symbols involved

| Symbol | Role | What this feature does with it |
|---|---|---|
| `pkg/agent/steer_reconstruct.go::AgentLoop.reconstructSteeredTurn` | Modify | An ordinary root whose stored channel is `main` or empty streams on `webchat`. A real external channel is left alone. Stored metadata is not rewritten. |
| `pkg/gateway/websocket_streamer.go::WSHandler.GetStreamer` | Call, do not change | Already fans a `webchat` turn out by session id, including when no browser is attached. |
| `pkg/agent/steer_audience.go::SteerUpwardDeliverer.wakeOwnerOrStore` | Call through the new entry | Stop-marker refusal and live-turn join stay. |
| `pkg/agent/async_notifier.go::asyncNotifierImpl.WakeParentAlways` | Call from the new entry | Still the idle wake. Still no debounce. |
| `pkg/agent/steering.go::AgentLoop.EnqueueSteeringWake` | Call | Live parent: join this turn. Do not edit `enqueueSteeringFromMessage`. |
| `pkg/agent/ordinary_root_record.go::AgentLoop.ensureOrdinaryRootRecord` | Call | Never-turned eligible main only. |
| `pkg/session/message_inbox.go::classifyEnvelope` | Modify | Progress, checkpoint, and artifact become not wake-eligible. Inbox caps stay. |
| `pkg/agent/goal_triggers.go` met arm and `ackMetVerdictEntry` | Modify | Verdict text is part of the one wake body before the ack. |
| `pkg/agent/goal_outcome.go::AgentLoop.recordGoalOutcome` | Call twice | Helper transcript as today, plus one parent transcript when a parent exists. |
| `pkg/agent/stopped_notice.go::AgentLoop.deliverLandedStopNotice` | Modify | Idle parent is woken for restart and historical timeout only. An intentional Stop, parent agent or person, is not. A parent-chat row is still written. |
| `pkg/agent/boot_sweep.go::SteerBootRecovery.recoverSteered` | Modify | After the restart stop is durable, a native helper is resumed from history. |
| `pkg/agent/steering.go::AgentLoop.recordPolledFinalConsumed` | Do not change | Still suppresses a second completion turn. |
| `src/lib/goalOutcome.ts::buildGoalOutcomeInsertion` | Reuse | Parent session bucket receives the existing frame. |
| `src/store/chat/slices/frames.ts` case `subagent_message` | Do not treat as the verdict row | Stays the side-panel line. |

### Impact assessment

| Symbol modified | Risk (inferred) | Direct dependents to retest | Indirect |
|---|---|---|---|
| `classifyEnvelope` | Medium | Inbox admission tests, upward delivery, boot re-delivery | Any helper that sends progress |
| `reconstructSteeredTurn` | High | Wake path, helper's own live view, ordinary-root revival | A channel parent that is not web chat |
| `deliverLandedStopNotice` / boot recovery | High | Restart recovery, user Stop, historical timeout | External-CLI helpers |
| `ackMetVerdictEntry` call order | High | Met goal, boot re-wake, `delegate inbox` | Task-owned goals |
| New `helper_event` contract | Medium | Replay, cold message load, SPA reducer | Generated types |

No GitNexus HIGH/CRITICAL number exists for this session. The reconstruct and boot rows are treated as high because a wrong channel hides the turn again, and a wrong resume re-runs work or resurrects a user Stop.

### Relevant execution flows

| Flow | Relevance |
|---|---|
| Helper finish → inbox → wake → reconstructed parent turn → web socket | PW-D1, PW-D5 |
| Judge met → suppressed verdict → hand-back wake → ack | PW-D2, PW-D5 |
| Boot → restart stop → parent notice | PW-D3, PW-D6 |
| `message_parent` progress → inbox → side panel | PW-D4 (wake removed) |

### Cluster placement

Agent turn loop, session inbox, gateway web socket, and the chat store. It crosses the backend/frontend boundary, so the wire shape in section 9 is the contract the SPA must consume. It does not belong to rooms.

---

## User Stories & Acceptance Criteria

### User Story 1 — See the woken reply without reloading (Priority: P0)

A person who left the main chat open after asking an agent to delegate sees that agent's next reply appear in the same chat, while it is being written. Today that reply is saved and shows up only after a reload, because the turn is streamed on a channel the browser ignores.

**Why this priority**: Without this, the verdict row and the stop row can be correct on disk and still invisible in the open chat. Issue #1317.

**Independent Test**: Finish a helper after the parent's turn has ended, with the parent chat open. The parent's new reply renders token by token. No reload.

**Acceptance Scenarios**:

1. **Given** a parent web chat is open and the parent turn has finished, **When** the helper's final result wakes the parent, **Then** the parent's new reply appears in that chat while it is produced, and a reload is not required.
2. **Given** that reply has finished, **When** the person reloads the parent chat, **Then** the same reply is in the history.
3. **Given** two browsers are open on the same parent session, **When** the wake turn streams, **Then** both show it. Neither browser starts its own extra turn.
4. **Given** the parent's stored channel is a real external channel with a peer (not `main`, not empty), **When** a helper wakes that parent, **Then** the turn is streamed on that channel, not forced onto `webchat`.
5. **Given** a main session whose stored channel is `main`, **When** a wake turn streams on `webchat`, **Then** the stored channel is still `main` afterwards.
6. **Given** a wake turn has no session id, **When** streaming is requested, **Then** it is refused and is not attached to whichever chat happens to be open.

### User Story 2 — See the Judge's verdict in the parent chat (Priority: P0)

The person reads the parent chat, not the helper's chat. When the Judge rules, that chat shows the ruling, and the parent agent is actually given the ruling before anyone marks it as already read.

**Why this priority**: R7. The wake happened, the model said it had no verdict, and the only "Goal met" card was on the helper. One lane passed by looking at the helper chat.

**Independent Test**: Delegate a goal, let the Judge rule met, stay on the parent chat. A "Goal met" row is visible there. The parent's reply does not claim the verdict was missing.

**Acceptance Scenarios**:

1. **Given** a helper's session goal ends met, **When** the result is delivered, **Then** the parent chat shows one "Goal met — …" row with the Judge summary, even if the parent model says nothing about it.
2. **Given** that same ending, **When** the parent turn starts, **Then** its input contains the verdict condition and the Judge's reason, and the verdict is marked read only after that exact text is saved as a chat record. Putting it into the model history happens later, once, at the safe point, and is not what marks the verdict read.
3. **Given** the Judge rules unmet and the goal stays active, **When** the round finishes, **Then** the parent chat shows one notice row that the goal is not met yet and the helper continues, the parent is woken once, and no "Goal met" card appears.
4. **Given** the Judge cannot complete the check, **When** the "Not verified — the check could not complete" conclusion is produced, **Then** the parent chat shows that conclusion as a row, the parent is woken once, and the goal stays active.
5. **Given** any of those rulings, **When** the person opens the helper's own chat, **Then** the helper's existing outcome row is still there.
6. **Given** a parent-chat verdict row was written, **When** the person reloads, **Then** the row is still there, once.

### User Story 3 — Hear about a stop, a restart, and a timeout (Priority: P0)

When a helper is cut off, the parent chat says so. A restart or an old timeout also wakes an idle parent. A Stop the person pressed does not start anyone again.

**Why this priority**: Jarvis. Restart and timeout notices reached the inbox of an already-idle parent and never became a chat row or a wake.

**Independent Test**: Stop a helper from the helper's own Stop control while the parent is idle. The parent chat gains one row and does not start a new parent turn. Separately, kill the gateway while a native helper is mid-task. After boot, the helper continues, the parent chat shows a restart row, and the parent wakes once.

**Acceptance Scenarios**:

1. **Given** an idle parent and a native helper cut off by a gateway restart, **When** the gateway is back, **Then** the parent chat shows one restart row and the parent starts one turn.
2. **Given** an idle parent and a helper whose saved stop is a historical timeout, **When** that notice is delivered, **Then** the parent chat shows one timeout row and the parent starts one turn, and the helper is not resumed.
3. **Given** the parent agent or the person stops the helper, **When** that stop lands, **Then** the parent chat shows one stopped row, the parent does not start a turn, the outcome does not join a live parent turn, and the helper does not resume. A later boot replay of that same notice still does not wake.
4. **Given** the person presses Stop on the parent, **When** that stop lands, **Then** no new parent turn starts and neither the parent nor a helper that this Stop cancelled is auto-resumed.
5. **Given** the parent already has a Stop marker for its current generation, **When** a helper outcome or notice arrives, **Then** it is stored and any required row is written, and no parent turn starts.
6. **Given** the parent transcript cannot be written, **When** a restart notice is delivered, **Then** the product does not claim the event row was shown, the parent chat shows a nonfatal line that the event could not be saved and will be retried, and a later retry writes that event row once. The pending line is not the same as an empty chat.

### User Story 4 — Wake only for something the parent must handle (Priority: P0)

Progress chatter is visible and does not spend a parent turn. A real ending, a failure, a question, or a blocker does.

**Why this priority**: Founder rule. Progress wakes today, with no wake debounce, and each wake is a billed model turn.

**Independent Test**: A helper sends three progress lines while the parent is idle. The side panel updates. The parent's turn count does not increase. A later final result increases it by one.

**Acceptance Scenarios**:

1. **Given** an idle parent, **When** the helper sends progress, a checkpoint, or an artifact, **Then** the inbox stores it when the inbox caps allow, the side panel updates, and no parent turn starts.
2. **Given** the inbox rate or unacked cap refuses a progress line, **When** that happens, **Then** nothing is stored, nothing is shown for that line, and no parent turn starts. The refusal is not reported as a successful delivery.
3. **Given** an idle parent, **When** the helper asks a question or reports a blocker, **Then** the parent starts one turn.
4. **Given** an idle parent, **When** the helper fails, **Then** the parent starts one turn and the parent chat shows the failure.
5. **Given** a met goal, **When** the verdict and the hand-back are both produced, **Then** the parent starts one turn, not two.
6. **Given** an ordinary non-fatal error that is not a lifecycle notice, **When** it is delivered, **Then** it is stored and no parent turn starts.

### User Story 5 — Resume a helper a restart cut off, without repeating finished work (Priority: P0)

A native helper that was running when the gateway died continues from the transcript. A tool that already succeeded is not called again by the runtime. The chat row records only that the interruption happened. Whether the helper is now waiting, running, or finished is the existing Activity panel, not a rewrite of that row.

**Why this priority**: Founder rule, and the Jarvis cases where a restart left no chat explanation.

**Independent Test**: A helper's last tool result is already a success. Restart the gateway. The helper's next model request includes that result. The tool executor is not called again for that same call. The parent is woken once.

**Acceptance Scenarios**:

1. **Given** a native helper was running or queued and the gateway restarts, **When** boot recovery finishes, **Then** the helper is dispatched again from its saved transcript and the parent is woken once.
2. **Given** the helper's last tool call already has a successful result, **When** that resume runs, **Then** the runtime does not invoke that tool call again.
3. **Given** that restart, **When** the person opens the helper chat, **Then** it shows one notice that the helper was interrupted by a restart. That notice does not change when the helper later waits, runs, or finishes.
4. **Given** resume cannot be admitted, **When** boot finishes, **Then** the parent is still woken once. The restart notice stays. A second notice says resume did not start. The helper is not left looking running.
5. **Given** the person had pressed Stop on that helper before the restart, **When** boot runs, **Then** the helper is not resumed and the parent is not woken.
6. **Given** the helper is an external command-line worker whose conversation was not retained, **When** the gateway restarts, **Then** it is not auto-resumed and no new worker is created. The parent row and the one wake still happen.

### User Story 6 — One wake entry other features can call (Priority: P1)

The scheduled-task creator notice, and later the idle checker, wake a main session through the same entry this feature uses. They do not copy the channel rules.

**Why this priority**: The scheduled-task spec (FQ-4) already names `WakeParentAlways` and `ensureOrdinaryRootRecord`. Landing that copy beside this fix would fork the channel bug.

**Independent Test**: A test calls the entry with the creator-notice reason and an idle main whose stored channel is `main`. One turn starts on `webchat`. Calling the entry with an unknown reason is refused and starts nothing.

**Acceptance Scenarios**:

1. **Given** the entry is called for a creator notice on an idle eligible main, **When** the main's stored channel is `main`, **Then** one turn streams on `webchat` and the stored channel is unchanged.
2. **Given** the entry is called with a reason this unit does not own, **When** that call is made, **Then** it is refused, nothing is woken, and the refusal is visible to the caller.
3. **Given** a live turn already exists for that session, **When** a second outcome arrives, **Then** it joins that turn and does not start another.

### User Story 7 — Strange parents do not get a guessed chat (Priority: P0)

A parent that is gone, never started, sitting in another workspace, or waiting on an approval is handled without inventing a recipient or dropping the row.

**Why this priority**: These are the cases that silently no-op or double-fire today.

**Independent Test**: Each scenario below stands alone.

**Acceptance Scenarios**:

1. **Given** the parent is an eligible main with session metadata and no lifecycle record, **When** an outcome wake is owed, **Then** the ordinary root record is created and one wake turn starts.
2. **Given** the parent session is gone, hidden, or removed, **When** a helper outcome arrives, **Then** no other session is created or woken. The miss is logged. A row is not written into a different chat.
3. **Given** the person is viewing a different workspace, **When** the parent is woken, **Then** the row and the turn belong to the parent session's own chat only.
4. **Given** the parent is waiting, **When** a helper outcome arrives, **Then** a tool approval still inside a live turn keeps its card and joins that turn, and a parked question card stays valid, shows the outcome row immediately, retains the report, and does not start an outcome-only turn until the person's correlated answer continues the parent.
5. **Given** the process dies after the wake is published and before the verdict text is saved as a chat record, **When** boot runs, **Then** the verdict is not treated as read, the text is saved once, it is placed into model history at most once and only if a turn is still owed, and a turn that already placed it is not repeated.
6. **Given** the parent already pulled the final result, **When** the completion wake arrives later, **Then** no second turn starts, and a verdict row that this spec requires is still present.
7. **Given** two helpers finish while the first wake turn is still running, **When** the second finish arrives, **Then** the second joins that turn. Each finish still has its own row.

---

## Behavioral Contract

Primary flows:

- When a helper finishes, fails, asks, blocks, or receives a Judge ruling, the parent chat gains the required row and, unless a Stop or a parked question card says otherwise, the parent runs one turn, streamed into the open web chat.
- When a native helper is cut off by a restart, it resumes from history, both chats show a notice, and the parent runs one turn.
- When the person reloads the parent chat, the rows and the parent's reply are there.

Error flows:

- When resume cannot start, the restart row stays, a second row says resume did not start, the parent is woken once, and the helper is not shown as running.
- When the parent session is gone, nothing is delivered to a stand-in.
- When a progress line is over the inbox cap, it is refused visibly to the sender and does not wake anyone.

Boundary conditions:

- When the parent turn is already live, a new outcome joins it.
- When the person has Stopped the helper or the parent, nothing auto-wakes and nothing auto-resumes.
- When the stored channel is a real external channel, the wake uses that channel.

---

## Edge Cases

- Two browsers on one session: both see the frames, one turn. Expected: no per-browser wake.
- Parent metadata channel `main` and empty peer: stream `webchat` with the session id. Expected: live tokens in the web chat.
- Parent metadata channel is a connected channel with a peer: keep it. Expected: that channel receives the turn.
- Met verdict and hand-back: one turn, verdict text inside it, goal-outcome row on the parent. Expected: no second turn.
- Unmet round with tries left: notice row, one wake, goal still active. Expected: no goal-outcome "ended" card.
- Judge unavailable: the existing "Not verified" sentence as a parent row, one wake, goal still active.
- User Stop of the helper: parent row, no wake, no resume.
- User Stop of the parent: no wake, no resume of the parent or of helpers that Stop cancelled.
- Restart of a native running or queued helper: resume, one parent wake, completed tool not re-invoked.
- Restart of an external command-line helper: no auto-resume, no new worker, parent row and one wake.
- Historical timeout: parent row and one wake, no resume.
- Progress / checkpoint / artifact: store when caps allow, show on the side panel, no wake.
- Ordinary non-fatal error: store, no wake.
- Inbox cap refuses progress: no store, no show, no wake.
- Parent record missing, eligible main metadata present: create the ordinary record, then one wake.
- Parent gone, hidden, or removed: no substitute, miss logged.
- Person is in another workspace: no copy of the row into that workspace.
- Tool approval still inside a live turn: card stays, row appears, outcome joins that turn. It is not placed into model history ahead of the unfinished tool result.
- Parked question card, no live turn: card stays valid, row appears, report is retained, no outcome-only turn until the person's answer. The saved input is not placed into model history while the card is unanswered.
- Crash after a bus publish and before the chat-membership save: the verdict is not marked read, and boot saves it once. A turn that already placed that input is not started again.
- Parent already polled the final: no second turn; required row still written.
- Second helper finishes during the first wake turn: joins that turn; own row.
- Stop marker on the parent's current generation: store and write the required row; no turn.
- Queued native helper at restart: resumed (it had not finished a tool). A tool that had already succeeded is still not re-invoked.
- Helper in `needs_input`: not treated as a restart cut-off. Not auto-resumed by this unit.
- Task-owned goal met: still one wake for that ending. The task completion notice and the verdict must not start two turns.
- `delegate result` consumed marker: unchanged. It still cancels a duplicate completion turn.

---

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not start a second parent turn for a met verdict and its hand-back, because that bills the person twice for one ending.
- The system must not wake the parent for progress, checkpoint, or artifact, because the founder forbade it and each wake is a model turn.
- The system must not auto-resume or auto-wake after a helper Stop, whether the parent agent or a person pressed it, because Stop would otherwise mean "stop, then immediately continue" (PW-D13).
- The system must not re-invoke a tool call that already succeeded, because a restart would repeat side effects (a second file write, a second send).
- The system must not auto-resume the person's own main chat in this unit, because the founder decision applied to the helper.
- The system must not auto-resume an external command-line helper or create a replacement worker, because a lost tool conversation is already a refusal, not a silent new run.
- The system must not invent a parent in another session or workspace when the real one is gone.
- The system must not rewrite the stored session channel to `webchat`, because a later external delivery would then go to the wrong place.
- The system must not drop a required chat row because the model might mention the event. The row is the record; the model turn is extra.
- The system must not change the goal idle timers or add a no-goal idle nudge here.
- The system must not edit `enqueueSteeringFromMessage`. Rooms work owns that function.
- The system must not implement the scheduled-task creator notice's text, recipients, or run rules. It only provides the wake entry.
- The system must not add a debounce that can swallow a real completion. The unbounded path being removed is progress, not endings.
- The system must not reintroduce a lifetime budget, a UI-disconnect watchdog, a goal confirm gate, or a shell block list.

### Machine-Verifiable Constraints

**Wake reasons the entry accepts:**

- `finished`, `failed`, `timed_out`, `verdict`, `question`, `blocker`, `restart`, `creator_notice`.
- `stopped` is not a reason. An intentional helper Stop, by the parent agent or by a person, is refused on first delivery, on a live-turn join, and on boot replay. The visible stopped row may still be written.
- Restart and historical timeout are recognised from the landed transition's cause (`restart`, historical `timeout`), never from the generic `stopped_child:` text prefix. A tool-iteration ceiling notice (`max_tool_iterations:`) stays a failure wake. It is not a Stop.
- Any other reason, including an idle-checker nudge, is refused. The call returns an error and starts no turn.

**One turn:**

- For one helper ending, the parent session gains at most one new turn.
- A met session goal's verdict inbox entry is not itself a wake when the hand-back wake is published.
- A consumed final marker means the completion wake starts no turn.

**Channel:**

- Stored `channel` `main` or empty, and no peer: the wake turn's stream channel is `webchat` and its chat id is the parent session id.
- Stored channel other than `main` and a non-empty peer: the wake turn uses that pair.
- After either case, metadata `channel` is unchanged.

**Rows:**

- A met ending writes one `goal_outcome` entry on the helper transcript and, when a parent exists, one on the parent transcript. Different message ids. Same goal id and ending.
- An unmet continuing round writes one `helper_event` of kind `verdict_unmet` on the parent transcript and does not write `goal_outcome`.
- A not-verified conclusion writes one `helper_event` of kind `not_verified` on the parent transcript.
- An intentional helper Stop, by the parent agent or by a person, writes one `helper_event` of kind `stopped` and starts no parent turn and no resume.
- A restart of a native helper writes one immutable `helper_event` of kind `restarted` on the helper transcript and one on the parent transcript. Later queued, running, or finished status does not edit that row. A refused resume writes a second `resume_refused` row and does not replace the first.
- A historical timeout writes one `helper_event` of kind `timed_out` on the parent transcript and does not resume the helper.
- Reloading the parent messages returns each of those entries once.

**Resume:**

- Boot dispatches a native helper whose latest landed stop cause is restart and whose state was running or queued, unless an intentional Stop (parent agent or person) owns that generation, including a landed Stop whose marker has already been cleared.
- The resumed turn's history includes every tool result already stored. The runtime does not replay a historical invocation, identified by its issuing assistant occurrence plus call id. A new assistant occurrence that reuses the same provider call id is a new decision and is not blocked.
- A user Stop cause does not dispatch.

**Caps that stay:**

- Progress, checkpoint, and artifact remain subject to the inbox unacked cap and the per-minute rate. They are not cap-exempt.
- Outcome wakes still bypass the legacy 15-second / 4-per-hour debounce. This unit does not add a new numeric cap on endings.

**Scope boundaries:**

- The system MUST NOT change `pkg/agent/goal_triggers.go` idle timers or the quiet-window length.
- The system MUST NOT change `enqueueSteeringFromMessage`.
- The system MUST NOT create a session for a hidden, removed, or missing parent.

### Conservative type design

`HelperEvent` exists because a string cannot say which helper event it is, and stamping these rows as `goal_outcome` would claim the goal had ended. No other new nominal type is required. The wake reason is the closed list above, not a free string the caller invents.

---

## Prerequisites

- **Hardware / OS**: The supported set already in the repo: Linux, macOS, Windows. No new platform.
- **Required runtimes**: The repo's existing Go and Node toolchains. No new runtime.
- **Required services**: A running gateway and the web chat, for the hands-on rows. No new service.
- **Network assumptions**: The same provider access a normal chat turn needs, for the hands-on rows only. Unit tests use fakes.
- **Accounts / credentials**: The UAT lane's own account. No new credential.

---

## Development Setup

No new setup. Use the repo's existing gateway and web build. Do not point tests or hands-on checks at the founder's live instance.

**Expected first-run behaviour**: Not a new install surface.

**Common first-run failures**: A missing SPA embed stub fails the gateway compile. That is the existing trap, not a new one.

---

## Tech Stack

| Category | Choice | Version / Pin | Source |
|---|---|---|---|
| Language | Go and TypeScript | Go 1.26.9 minimum; the repo's current React / Vite | Root `CLAUDE.md` |
| Runtime / container | Existing gateway | — | — |
| Datastore(s) | Existing session transcript and inbox files | — | `pkg/session` |
| External APIs | The parent's configured model, for a real wake turn | Hands-on provider `openrouter` + `deepseek/deepseek-v4.1-flash` | Root `CLAUDE.md` UAT provider |
| Build tool | Existing `make` / contract generator | — | `scripts/gen-contracts.sh` |
| Test framework | `go test` (narrow), Vitest, Playwright | CI is the authority for full Go | Root `CLAUDE.md` |

---

## Deployment / Runtime

- **Target environment**: The existing single gateway binary and embedded web app.
- **Online / offline**: A wake turn needs the parent's model provider. Rows are written locally before that turn starts, so a provider failure does not erase the row.
- **Resource limits**: No new memory budget. Removing progress wakes reduces model spend. Outcome wakes stay uncapped on purpose.
- **Start / stop commands**: Unchanged gateway start. Boot recovery gains the resume step in section 9.
- **Health check**: None new.
- **Logs / telemetry**: A missed wake uses the existing undelivered-wake report. A new info line alone is not enough.

---

## Integration Boundaries

### Web chat socket

- **Data in**: The browser subscribes to `webchat` for a session id.
- **Data out**: Tokens, tool rows, done, `goal_outcome`, and `helper_event` for that session id.
- **Contract**: Section 9. `GetStreamer` already refuses a channel other than `webchat`.
- **On failure**: If no browser is attached, the turn still runs and is stored. Reload shows it. A failed frame publish after a durable transcript write is logged and does not delete the entry. Replay sends it later.
- **Development**: Real gateway in integration tests; no live founder instance.

### Model provider

- **Data in**: The parent turn, whose user content includes the outcome text.
- **Data out**: The parent's reply, streamed.
- **Contract**: Unchanged provider API.
- **On failure**: The row remains. The turn's existing error path applies. A provider error is not a reason to start a second wake for the same outcome.
- **Development**: Fake model in unit and integration tests.

### Scheduled-task creator notice (other spec, not built here)

- **Data in**: That spec calls the wake entry with reason `creator_notice`, the creator's main session id, and its short text.
- **Data out**: One wake of that main, or a join into its live turn, under the channel rules in this spec.
- **Contract**: The entry in section 9. No new wire type for FQ-4.
- **On failure**: That spec's own "do not pick another session" rule. This entry returns the error; it does not substitute.
- **Development**: This unit ships the entry and a test that a `creator_notice` call wakes once on `webchat`. It does not fire a scheduled task.

### Gateway boot

- **Data in**: Lifecycle records left `running` or `queued`.
- **Data out**: Restart stop, resume dispatch for a native helper, parent row, one wake.
- **Contract**: Existing lifecycle journal. No new boot file format.
- **On failure**: A failed resume is visible on both chats and still wakes the parent once.
- **Development**: In-process boot recovery test. No real process kill in unit tests. Hands-on UAT does the real restart.

### Idle checker (later unit, not a runtime dependency)

- **Data in**: None from this unit.
- **Data out**: None. A future caller would use the same entry with a reason this unit refuses until that unit lands.
- **Contract**: Section 8.
- **On failure**: Not applicable.
- **Development**: Not implemented.

---

## BDD Scenarios

### Feature: Parent wake and visibility

#### Scenario: Open parent chat shows the woken reply live

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the person's parent chat is open in the browser
- **And** the parent's own turn has already finished
- **And** a helper is still running
- **When** the helper hands back a final result
- **Then** the parent chat shows the parent's new reply as it is written
- **And** the person does not reload

#### Scenario: Reload shows the reply the live chat showed

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a wake turn has finished and its reply is stored
- **When** the person reloads the parent chat
- **Then** the reply is in the transcript

#### Scenario: Two browsers share one wake turn

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Edge Case

- **Given** two browsers are subscribed to the same parent session
- **When** a helper finish wakes the parent
- **Then** both browsers show the same turn's tokens
- **And** the parent session has one new turn

#### Scenario Outline: Stream channel follows the parent's real destination

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** the parent session's stored channel is `<stored_channel>` and its peer is `<peer>`
- **When** a helper outcome wakes the parent
- **Then** the turn streams on `<stream_channel>` with chat id `<stream_chat>`
- **And** the stored channel is still `<stored_channel>`

**Examples**:

| stored_channel | peer | stream_channel | stream_chat |
|---|---|---|---|
| main | empty | webchat | the parent session id |
| empty | empty | webchat | the parent session id |
| telegram | a real peer id | telegram | that peer id |

#### Scenario: A wake with no session is not attached to another chat

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Error Path

- **Given** a wake turn has no session id
- **When** a stream is requested for it
- **Then** the stream is refused
- **And** no other open chat receives its tokens

#### Scenario: A web wake does not rewrite the stored channel

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Edge Case

- **Given** a main session stored with channel `main`
- **When** its wake turn streams on `webchat`
- **Then** reading the session metadata still returns channel `main`

#### Scenario: Parent chat shows Goal met without the model repeating it

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** a helper has a session goal
- **And** the person is watching the parent chat, not the helper chat
- **When** the Judge rules the goal met
- **Then** the parent chat shows one row whose headline is "Goal met — " plus the goal text
- **And** the row includes the Judge's criteria summary
- **And** the row is visible even when the parent model produces no sentence about the verdict

#### Scenario: The parent is given the verdict before it is marked read

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a helper's session goal has just been ruled met
- **When** the single parent wake is published
- **Then** that wake's input contains the verdict condition and the Judge's reason as well as the hand-back
- **And** the verdict is marked read only after that exact text exists as a chat-membership save
- **And** that save is not yet model history while a tool result is still unfinished, while a question card is unanswered, or while the parent is intentionally stopped
- **And** the parent starts one turn only when a turn is allowed

#### Scenario: An unmet round is a notice, not an ending

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** a helper's goal has tries remaining
- **When** the Judge rules the round unmet
- **Then** the parent chat shows one notice that the goal is not met yet and the helper continues
- **And** the parent starts one turn
- **And** the parent chat does not show a "Goal met" row
- **And** the goal stays active

#### Scenario: Not verified is a parent-chat row and one wake

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Error Path

- **Given** a helper's goal check cannot complete
- **When** the conclusion "Not verified — the check could not complete" is produced
- **Then** the parent chat shows that conclusion as one row
- **And** the parent starts one turn
- **And** the goal stays active

#### Scenario: The helper keeps its own outcome row

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Happy Path

- **Given** a helper goal ends met
- **When** the parent chat receives its "Goal met" row
- **Then** the helper chat still shows its own "Goal met" row

#### Scenario: Reload shows the parent verdict row once

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Edge Case

- **Given** the parent chat has a "Goal met" row
- **When** the person reloads
- **And** the live frame is also replayed
- **Then** the parent chat shows that row once

#### Scenario: Restart wakes an idle parent and shows a row

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the parent is idle
- **And** a native helper was running
- **When** the gateway restarts and comes back
- **Then** the parent chat shows one restart row
- **And** the parent starts one turn

#### Scenario: A historical timeout wakes and does not resume

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** the parent is idle
- **And** a helper has a saved timeout stop from the retired lifetime budget
- **When** that notice is delivered
- **Then** the parent chat shows one timeout row
- **And** the parent starts one turn
- **And** the helper is not dispatched again

#### Scenario: User Stop of the helper writes a row and starts nothing

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the parent is idle
- **When** the parent agent or the person stops the helper and the stop lands
- **Then** the parent chat shows one stopped row
- **And** the parent does not start a turn
- **And** the outcome is not joined into a live parent turn
- **And** the helper does not resume
- **And** a later boot replay of that notice still does not wake

#### Scenario: User Stop of the parent starts nothing

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the person presses Stop on the parent
- **When** that stop lands and cancels a helper under it
- **Then** the parent does not start a turn
- **And** the parent is not auto-resumed
- **And** the cancelled helper is not auto-resumed

#### Scenario: A transcript write failure is not reported as a shown notice

**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Error Path

- **Given** the parent transcript cannot accept a new entry
- **When** a restart notice is delivered
- **Then** the event row is not reported as shown
- **And** the parent chat shows one nonfatal line that the event could not be saved and will be retried
- **And** a later retry writes one restart row
- **And** the parent is not woken twice for that same notice

#### Scenario: A Stop marker on the parent blocks every wake

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the parent's current generation has a Stop marker
- **When** a helper finish, verdict, or restart notice arrives
- **Then** the required row is stored on the parent transcript
- **And** no parent turn starts

#### Scenario Outline: Chatter is stored and shown and does not wake

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the parent is idle
- **When** the helper sends a `<kind>` message within the inbox caps
- **Then** the message is stored
- **And** the parent's side panel updates
- **And** the parent does not start a turn

**Examples**:

| kind |
|---|
| progress |
| checkpoint |
| artifact |

#### Scenario: A progress line over the inbox cap wakes nobody

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Error Path

- **Given** the helper has already hit the inbox rate or unacked cap
- **When** it sends another progress line
- **Then** the line is refused
- **And** it is not shown
- **And** the parent does not start a turn

#### Scenario Outline: Questions, blockers, and failures wake once

**Traces to**: User Story 4, Acceptance Scenario 3; User Story 4, Acceptance Scenario 4
**Category**: Happy Path

- **Given** the parent is idle
- **When** the helper delivers `<event>`
- **Then** the parent starts one turn
- **And** the parent chat shows `<row>`

**Examples**:

| event | row |
|---|---|
| a question | the question text |
| a blocker | the blocker text |
| a fatal failure | the failure text |

#### Scenario: Met verdict and hand-back are one turn

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Edge Case

- **Given** a session goal is ruled met
- **And** the hand-back follows immediately
- **When** delivery finishes
- **Then** the parent session has one new turn
- **And** that turn's input includes both the verdict and the hand-back

#### Scenario: An ordinary non-fatal error does not wake

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Error Path

- **Given** the parent is idle
- **When** the helper delivers a non-fatal error that is not a lifecycle notice
- **Then** the error is stored
- **And** the parent does not start a turn

#### Scenario: A native helper resumes after restart without repeating a finished tool

**Traces to**: User Story 5, Acceptance Scenario 1; User Story 5, Acceptance Scenario 2
**Category**: Happy Path

- **Given** a native helper was running
- **And** its last tool call has a successful result in the transcript
- **When** the gateway restarts
- **Then** the helper is dispatched again
- **And** the next model request includes that tool result
- **And** the runtime does not invoke that tool call again
- **And** the parent starts one turn

#### Scenario: The helper chat says it was interrupted by a restart

**Traces to**: User Story 5, Acceptance Scenario 3
**Category**: Happy Path

- **Given** a native helper was resumed after a restart
- **When** the person opens that helper chat
- **Then** one notice says the work was interrupted by a restart
- **And** that notice does not change when the helper later waits, runs, or finishes

#### Scenario: A failed resume still tells the parent once

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Error Path

- **Given** a native helper was cut off by a restart
- **And** resume cannot be admitted
- **When** boot finishes
- **Then** the restart notice stays unchanged
- **And** a second notice says resume did not start
- **And** the parent starts one turn
- **And** the helper is not shown as running

#### Scenario: A helper the person stopped is not resumed by a later boot

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the person stopped the helper
- **And** the gateway later restarts
- **When** boot recovery runs
- **Then** the helper is not dispatched
- **And** the parent is not woken

#### Scenario: An external helper is not replaced after restart

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** an external command-line helper was running
- **And** its tool conversation is no longer retained
- **When** the gateway restarts
- **Then** no new worker session is created
- **And** the helper is not auto-resumed
- **And** the parent chat shows one restart row
- **And** the parent starts one turn

#### Scenario: A creator notice uses the shared entry and streams on webchat

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** an eligible main is idle and stored with channel `main`
- **When** the wake entry is called with reason `creator_notice` and a short body
- **Then** one turn streams on `webchat`
- **And** the stored channel is unchanged

#### Scenario: An unknown wake reason is refused

**Traces to**: User Story 6, Acceptance Scenario 2
**Category**: Error Path

- **Given** a caller passes a wake reason that is not in the accepted list
- **When** the entry is called
- **Then** the call fails
- **And** no turn starts

#### Scenario: A second outcome joins a live parent turn

**Traces to**: User Story 6, Acceptance Scenario 3; User Story 7, Acceptance Scenario 7
**Category**: Edge Case

- **Given** a parent wake turn is still running
- **When** a second helper finishes
- **Then** the second outcome joins that turn
- **And** no parallel parent turn starts
- **And** the parent chat has a separate row for the second finish

#### Scenario: A never-turned main is recorded and then woken

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** an eligible main has session metadata and no lifecycle record
- **When** an outcome wake is owed to it
- **Then** the ordinary root record is created
- **And** one wake turn starts

#### Scenario: A gone parent is not replaced

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Error Path

- **Given** the parent session is gone, hidden, or removed
- **When** a helper outcome arrives
- **Then** no other session is created
- **And** no other session is woken
- **And** the miss is logged

#### Scenario: Another workspace does not receive the row

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the person is viewing a chat in a different workspace
- **When** a helper wakes its parent
- **Then** the row is in the parent session's chat only
- **And** the other workspace's chat does not gain it

#### Scenario: An open approval stays open

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the parent turn is waiting inside a tool approval
- **When** a helper finishes
- **Then** the approval card stays
- **And** the finish row appears in the parent chat
- **And** the finish joins the live turn
- **And** it is not placed into model history ahead of the unfinished tool result
- **And** a second parent turn does not start

#### Scenario: A parked question card does not start an outcome-only turn

**Traces to**: User Story 7, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** the parent has a pending question card and no live turn
- **When** a helper finishes
- **Then** the question card stays valid with the same correlation
- **And** the finish row appears immediately
- **And** the report is retained
- **And** no outcome-only parent turn starts
- **And** the saved report is not placed into model history while the card is unanswered
- **And** when the person answers, that answer and the retained report are delivered together

#### Scenario: A crash before the archive write does not drop the verdict

**Traces to**: User Story 7, Acceptance Scenario 5
**Category**: Edge Case

- **Given** the combined verdict-and-hand-back text has not yet been appended to the parent archive
- **When** the process dies, even if a bus publish already returned
- **Then** boot does not treat the verdict as read
- **And** boot saves that exact text once as chat membership
- **And** boot places it into model history at most once, and only if a turn is still owed
- **And** at most one parent turn starts
- **And** a turn that already placed that input is not started again

#### Scenario: A result the parent already pulled does not start another turn

**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Edge Case

- **Given** the parent already pulled the helper's final result
- **When** the completion wake arrives
- **Then** no new parent turn starts
- **And** the required verdict row is present if the Judge had ruled

---

## 8. Idle-checker boundary

The later unit owns the goal quiet window and the checker that runs when there is no goal. This unit does not read or write those timers.

The only interface: that unit, when it exists, calls the same wake entry. Until it lands, the entry refuses every reason except the closed list under Machine-Verifiable Constraints. A caller that wants an idle nudge today gets an error, not a silent wake.

This unit also does not define "finished but unread" as a new wake. A finished helper already wakes through the outcome path.

---

## 9. Contract and runtime design

Architect shape. Backend-lead edits `contracts/` and regenerates. Nobody else edits the contract files.

### 9.1 No new REST resource

No new path in `contracts/openapi.yaml`. The parent messages read already returns transcript entries. New fields travel on the existing message object.

### 9.2 Goal outcome, second transcript

`contracts/components/schemas/GoalOutcome.yaml` gains no fields. Its description changes:

- One goal ending may be stored once on the helper transcript and, when the helper has a parent, once on the parent transcript.
- The two entries share `goal_id`, `ending`, rounds, and Judge reason.
- They have different message ids.
- An unmet round that does not end the goal still writes none.

The AsyncAPI `GoalOutcomeFrame` text that duplicates this description is updated in the same change. Replay and the live frame already key off the session id, so the parent copy renders in the parent chat with the existing headline function. No SPA copy change for "Goal met".

Message ids:

- Helper: unchanged (`goal-outcome-<goal id>-<ended-at nanos>`).
- Parent: `goal-outcome-<parent session id>-<goal id>-<ended-at nanos>`.

### 9.3 Helper event

New schema `contracts/components/schemas/HelperEvent.yaml`.

| Field | Required | Rule |
|---|---|---|
| `helper_session_id` | yes | The helper session. Non-empty. |
| `helper_title` | no | The helper's chat title when known. Omitted rather than blank. |
| `kind` | yes | `verdict_unmet`, `not_verified`, `stopped`, `restarted`, `resume_refused`, `timed_out`, `finished`, `question`, `blocker`, `failed` |
| `text` | yes | The sentence the row shows. Non-empty. Sensitive-data filter applied before save, same filter the notifier already uses. |
| `goal_id` | no | Present when a goal is involved. |
| `created_at` | yes | RFC 3339 UTC. |

`contracts/components/schemas/Message.yaml`:

- Add enum value `helper_event` to `system_subtype`.
- Add field `helper_event` referencing the schema, same plain-reference style as `goal_outcome`.

AsyncAPI:

- Frame `helper_event` with `session_id` (the transcript the row belongs to), `message_id`, and the helper-event object.
- Replay emits that frame from a stored `system_subtype: helper_event` entry, discriminating on the subtype, never on the sentence text.
- The live publish uses the same frame.

The row is a lasting system line, not a toast and not a red turn-error. Frontend-lead uses the existing goal-outcome row pattern (one line, expandable text) and the design system. No new color, icon family, or control.

Sentence rules for `text` (the person reads these):

| Kind | Sentence |
|---|---|
| `verdict_unmet` | The Judge's reason, prefixed so it is clear the goal has not ended and the helper continues. |
| `not_verified` | The existing conclusion from `notVerifiedConclusion`, unchanged. |
| `stopped` | Who stopped the helper, and that it will not resume on its own. |
| `restarted` | The helper was interrupted by a restart. This sentence does not change when resume later waits, runs, finishes, or is refused. |
| `resume_refused` | Resume did not start. This is a second event. It does not replace the restart row. |
| `timed_out` | The helper stopped because an old time limit was hit, and it was not resumed. |

Kinds also include the chat rows promised for events that are not a goal ending and not a Stop-family notice: `finished`, `question`, `blocker`, `failed`. A question is not labelled as a goal ending. A goal-less finish is not a `goal_outcome`.

Message id, from the source event (PW-D20), plus the destination transcript id so the helper copy and the parent copy do not collide:

| Kind | Identity |
|---|---|
| `stopped`, `restarted`, `timed_out` | Landed stop sequence, the same sequence `stoppedChildNoticeID` already uses. Generation alone is not enough: resume keeps the generation (`SteerCanceller.reviveWithExecution`). |
| `resume_refused` | `resume-refused:` plus that same stop-sequence id. It is not the restart row's id, so the restart row cannot be overwritten by the refusal. |
| `verdict_unmet` | Goal id and verdict round, the same pair `goalVerdictUpwardMessageID` uses. |
| `not_verified` | That conclusion's own id. |
| `finished`, `question`, `blocker`, `failed` | The inbox message id of that delivery. |

A repeat write of one source event is a no-op. Two events that share a generation or a round number, but not the source identity, are two rows. Live frame, replay, and cold REST load converge on one row per id.

### 9.4 Wake entry

One function, new file under `pkg/agent` (not `steering.go`, not `enqueueSteeringFromMessage`). Name: `WakeSession`.

Inputs: recipient session id, reason (the closed list), the canonical outcome id (PW-D17), and the body text. There is no "row already written" flag. The row writer owns durability and repair (PW-D18). A boolean from the caller is not proof of a durable row.

| Recipient state | Result |
|---|---|
| Live turn waiting on a tool approval | Join that turn. No second turn. |
| Parked question card, no live turn (PW-D15) | Do not start a turn. Keep the report durable. The card stays valid and keeps its correlation. The report is delivered with the person's answer when that parent continues. |
| Live check saw a turn, and enqueue then finds the ordinary root has just completed | Fall through to one idle wake. `completed` is an eligible next round (`admitOrdinaryRootWake`). Do not treat that completion as a reason to drop the outcome. Do not reopen an arbitrary terminal helper or a changed execution. |
| Idle ordinary root, no intentional Stop | One idle wake. |
| Restart-stopped ordinary root, and this wake is the fresh helper outcome from that same restart (PW-D14) | One wake. Input is the helper event only. Do not replay the parent's interrupted instruction. |
| In-flight Stop, accepted-but-unlanded Stop, or landed intentional Stop, including after the marker has been cleared | No wake and no join. The report stays unconsumed for a later legitimate continuation. Do not report a bus publish as a wake that the stopped-root receiver will refuse. Do not relax `processSteeredSystemWake`'s stopped-root refusal. |
| Stopping the parent did not select a helper | That helper is not treated as cancelled. |
| No lifecycle record, metadata exists, not a helper, eligible visible main | Create the ordinary root record, then the idle wake. |
| Hidden, removed, missing metadata, or a load error other than not-found of an eligible main | No wake, no substitute. Return an error the caller logs through the existing undelivered-wake report. |

Channel for the idle wake and for the reconstructed ordinary-root turn:

- `main` or empty, or no peer: `webchat` and the recipient session id.
- Otherwise: the stored channel and peer.
- Do not write the metadata channel.

A helper's own turn is unchanged: it already forces `webchat`.

Idempotency is per logical event, on the recipient. The ids are:

| Logical event | Wake and consumption id | Shares a wake with |
|---|---|---|
| Met ending: verdict plus hand-back, including a task completion for that same ending | The existing final id `<child session>:<generation>:final`. `recordPolledFinalConsumed` already consumes this id. Poll semantics do not change. | The verdict and the task completion. Not a restart, not a later question. |
| Restart cut-off | `stoppedChildNoticeID` for that landed transition (parent, child, generation, stop sequence) | Nothing else. A later finish in the same generation uses the final id above. A `resume_refused` row is not a second wake. |
| Historical timeout | That transition's `stoppedChildNoticeID` | Nothing else. |
| Intentional Stop | That transition's `stoppedChildNoticeID` | No wake. Ack of the notice is not a parent turn. |
| Question, blocker, goal-less finish, fatal failure | That delivery's inbox message id, which already includes the helper | Nothing else. Two questions are two ids. |
| Unmet Judge round that does not end the goal | `goalVerdictUpwardMessageID` (goal id and round) | Nothing else. Round 2 is not round 1. |
| Not verified | That conclusion's own id | Nothing else. |
| Creator notice | That closed run's id, as the scheduled-task spec defines the run | Not a helper event. |

A consumed id starts no second turn. A different row in the table is not consumed by it. Dispositions stay distinct: already consumed, joined a live turn, held for a parked card, deliberately suppressed, saved but not yet placed, or failed. A suppressed or already-consumed event does not fall back under another id.

`creator_notice` is accepted so the scheduled-task spec can call `WakeSession` instead of `WakeParentAlways` directly. The companion is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/sched-tasks/docs/internal/specs/sched-tasks-suspend-resume-spec.md` section 5.7, revision `80559308f` on `feat/sched-tasks-20261015` as read on 2026-10-15. This unit's test calls `WakeSession` with `creator_notice` and asserts one `webchat` turn. That spec still owns when a task sends a notice. `schedules-autonomy-spec.md` is not that companion.

### 9.5 Save, then place, then acknowledge

Two facts, both in the existing archive. They are not the same write.

**Save.** Append one chat-membership record on the parent (`AppendTranscriptStrict` / addressed chat append). View membership is chat, not model. A transcript append rejects model membership (`appendTranscriptLocked`). Id `saved-input-<event id>` from the table above. Content is the exact text the model will later see: verdict condition, Judge reason, and hand-back, or the row text for a non-ending event. This record is the durable address recovery reads. A bus publish (`PublishInbound`) is not this write.

**Place.** Only at the ordinary safe consumption fence, after any assistant tool call that is still waiting on its result, call `archiveBackend.PlaceSavedInput` with that saved record's archive address. Placement appends a body-free model reference and takes its own ordinal. A second placement of the same source returns `ErrAlreadyConsumed`. Do not call `AppendModelMessage` for this input: that would assign model order immediately and can land between an assistant tool call and its later tool result (`TestOrdinals_PlacementOrderNotPhysicalOrder`). Do not describe the chat append as model membership. `PlaceSavedInput` rejects a source that is already a model member.

**Who places.** The consumer that is allowed to run the parent turn: the live-turn join after the approval fence, or the idle wake's turn at its first safe boundary. A parked question card, an intentional Stop, and an already-polled final do not place. The person still has the chat row. Holding or polling must not create a second model copy.

For a met session goal with a parent:

1. Write the helper and parent goal-outcome rows, or the repair payload in section 9.9 if a row write fails. Do not claim a missing row was shown.
2. Save the chat-membership input. If that save fails, do not acknowledge the verdict, do not place, and do not write the consumed marker.
3. After the save succeeds, acknowledge the verdict inbox entry. Acknowledgement is not placement.
4. If a parent turn is allowed, place once at the fence, then write the consumed marker for `<child>:<generation>:final`. If the turn is not allowed, do not place and do not start a turn. The saved record and the chat row remain.

`SuppressWake` stays on the met session-goal verdict. The verdict does not have a second wake id. If the parent already polled the final, that poll has consumed `<child>:<generation>:final`. Acknowledge the verdict. Do not place again. The goal-outcome chat row is still written if it is missing; that row is chat membership, not a model placement.

A task-owned met goal uses the same final id. `deliverTaskCompletionUpward` is the wake owner. The verdict is stored suppressed and correlated to that id.

An unmet round and a not-verified conclusion use their own ids from the table. They do not write `goal_outcome`. Their save/place rules are the same two steps.

### 9.6 Stop, restart, timeout

| Event | Parent row | Parent wake | Helper resume |
|---|---|---|---|
| Intentional Stop of the helper, parent agent or person, first delivery or boot replay | `stopped` | no, and no live-turn join | no |
| Intentional Stop of the parent | no wake of that parent | no | no resume of the parent. A helper is resumed only if this Stop did not select it and its own cause is restart |
| Same gateway restart cut off helper and parent | `restarted` | yes, one, the fresh helper outcome (PW-D14) | native helper only. The parent's interrupted instruction is not replayed |
| Gateway restart, native helper, parent merely idle | `restarted` | yes, one, after resume is admitted or has failed | yes, from history |
| Gateway restart, external command-line helper | `restarted`, text says it was not resumed | yes, one | no, and no new worker |
| Historical timeout, cause read from the landed transition | `timed_out` | yes, one | no |
| Landed intentional Stop whose marker was cleared, or an accepted Stop whose fence has not landed | row if the event is a helper Stop; no turn | no | no |

The stop-notice path does not call `WakeSession` for an intentional Stop. Restart and historical timeout do. Cause comes from the landed transition, not from the `stopped_child:` prefix. Classify must not treat every `stopped_child:` notice as wake-eligible.

A notice for an intentional Stop that is already stored does not wake on replay. A restart or timeout notice not yet consumed may wake once.

### 9.7 Resume without replaying a historical invocation

Resume is the next model decision over the saved transcript. A historical invocation is the issuing assistant occurrence plus its call id (`turnState.recordCallIssuers`). The runtime does not call the tool executor again for that pair. A later assistant occurrence that reuses the provider's call id, including `call_0`, is a new decision and runs. The automatic resume does not invent that later occurrence.

The helper chat and the parent chat each get one immutable `restarted` row: "Interrupted by a restart." It does not change when the helper later waits, runs, or finishes. Current queued, running, or finished state is the existing Activity panel. If resume cannot be admitted, that failure is a second row, kind `resume_refused`, id `resume-refused:` plus the restart notice id, text "Resume did not start." The first row stays. One parent wake, the restart event. The refusal row does not wake again.

A queued native helper that had not run a tool is dispatched. There is no successful tool call to skip.

A helper in `needs_input` is not resumed by this path.

### 9.8 Progress

`classifyEnvelope` sets progress, checkpoint, and artifact to not wake-eligible. Frames and inbox append stay. `CapExempt` stays false. After that change, the comment on `outcomeForKind` that says artifact and checkpoint are not wake-eligible is true again. Do not "correct" it into saying they are not stored. They are stored when the inbox accepts them, and they do not wake.

### 9.9 Crash order and row repair

Save happens before acknowledgement. Placement happens only if a turn is still owed. Boot does not place first and save later.

| Crash point | After boot |
|---|---|
| Chat-membership save missing, verdict not acknowledged | Save once, then acknowledge. Place only if a turn is still owed. Not two turns. |
| Save present, not placed, turn still owed | Place once at the fence. One turn. |
| Save present, already placed (`ErrAlreadyConsumed`) | No second placement and no second turn. |
| Bus publish happened, save never written | Not delivered. The in-process queue is gone. Do not treat the publish as a save. |
| Resume admitted, process dies again | The same restart rule. Do not replay a historical invocation. |
| A required chat row is missing and the wake was consumed | Repair from the family source below. No new turn. |

Repair source, and how long it lives. Inbox acknowledgement and `MessageInboxStore.compactLocked` (acked retention 200, compaction trigger 400) drop acknowledged inbox bodies. They are not a repair source.

| Family | Source that outlives inbox ack | Retired when |
|---|---|---|
| Stop, restart, historical timeout | The landed transition on the control ledger (`stoppedChildNoticeID`) | Never, for the ledger. The chat row is copied from it until the row id exists. |
| Goal ending | The goal record's terminal payload, not the inbox verdict | The goal record stays. The row repair mark is cleared only when both transcripts contain the row id. |
| Question, blocker, goal-less finish, fatal failure, unmet round, not verified | A chat-membership repair record on the child session's existing lifecycle journal, id `repair-<row id>`, holding the row payload. Written before the inbox acknowledgement. `compactLocked` does not read it. | Deleted only after every required destination transcript contains the row id. |

`recordGoalOutcome` returns success or failure. A failed row write still leaves the family source in place. The driver is boot recovery and the next delivery pass, not a test that calls the writer again by hand. The test reopens the stores, forces acknowledged-inbox compaction, runs that pass, and sees one restored row and zero new turns. The question/blocker case is included, not only a stop.

### 9.10 What this does to cost

Each outcome wake is one model turn, on purpose. Several helpers finishing during one live turn share that turn. Progress no longer starts turns. This unit adds no hourly cap on endings.

---

## 10. Frontend behaviour

| Surface | Behaviour |
|---|---|
| Open parent chat | Tokens, tool rows, and done for a wake turn whose stream channel is `webchat`, matched by session id. |
| Goal met / not met ending | Existing goal-outcome row, now also inserted when the frame's session is the parent. |
| Unmet, not verified, stopped, restarted, resume refused, timed out, finished, question, blocker, failed | One `helper_event` row per event. Lasting and immutable. Not a toast. Not the side-panel span header. Visible even when the parent model does not repeat the text. |
| Current helper status | The existing Activity panel. It may move from waiting to running to finished. That movement does not edit a `restarted` row. |
| Row save failed | A nonfatal line, id `repair-pending-<row id>`, in both message renderers: "A helper event could not be saved in this chat. Omnipus will retry. This is not a failure of the helper's work." An empty thread does not show this line. |
| Side panel | Unchanged for progress. Progress still updates the worker line and does not add a verdict card. |
| Reload and replay | Same rows, once each, from the stored subtype. |
| Tool approval | Stays until the person answers. The new row appears without dismissing it. The outcome joins the live turn. |
| Parked question card | Stays valid, including its correlation and focus. The helper row appears immediately. No outcome-only turn until the person answers. Then both are delivered together. |
| Helper chat | Gains the `restarted` row when this unit resumes it. Existing goal-outcome row stays. |
| Second browser | Same frames. No extra turn. |

A `subagent_message` must not be restyled into the verdict row. The verdict row is the goal-outcome or helper-event entry only.

---

## 11. Reachability

This is not a new tool and not a new screen. Reachability means a person with the parent chat open, and a person who reloads, can see the rows and the live reply.

| Check | Done when |
|---|---|
| Live reply | UAT-1 on the commit being landed. Two browsers. A partial token is visible before the turn completes, not only before reload. |
| Verdict row on the parent | UAT-2. The screenshot's composer is the parent agent, not the helper. |
| Stop / restart row | UAT-3 and UAT-4. Historical timeout visibility is Test 13, not those UAT rows. |
| Reload | UAT-5. The rows survive a fresh load. |
| Not only the inbox | A test reads the parent transcript, not only the inbox file, and finds the row. |
| Agent can be woken | The parent turn runs because `WakeSession` published it. No new catalog tool is required. |

Two lines, never merged:

- Code correct and tested: the planned tests in section 12 are green in CI, and the hands-on rows below have tester and validator receipts on the landed commit.
- Reachable by a person: the open parent chat shows the live reply and the rows without a reload, and a reload shows them too.

A green unit test that only reads the inbox is not reachability.

---

## 12. Test-driven development plan

Write the failing tests before the production change. Unit, then integration, then end-to-end. Go tests are narrow and tagged; CI runs the package. One local Go test process at a time when a local run is actually required.

### Test hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | Classifier, channel choice, id idempotency, resume skip | Pure decisions |
| Integration | Delivery, ack order, boot resume, transcript rows, SPA reducers | The seams |
| E2E | One scripted web chat | Live socket without a human |
| Hands-on | A real gateway and browser | The landing check |

### Test implementation order

| Order | Test name | Level | Traces to BDD scenario | Description |
|---|---|---|---|---|
| 1 | TestClassifyEnvelope_ProgressCheckpointArtifactDoNotWake | Unit | Chatter is stored and shown and does not wake; A progress line over the inbox cap wakes nobody | Those three kinds are not wake-eligible and not cap-exempt. A real Append over the rate or unacked cap returns the sender-visible refusal and does not store. Hand-back, question, blocker, fatal error, and goal status stay wake-eligible. A `stopped_child:` prefix does not, by itself, wake. `max_tool_iterations:` still does. |
| 2 | TestWakeChannel_MainBecomesWebchatStoredUntouched | Unit | Stream channel outline; stored channel unchanged | `main` and empty map to `webchat` plus session id. A telegram peer stays telegram. Metadata write is not called. |
| 3 | TestWakeSession_RejectsUnknownReason | Unit | Unknown wake reason | `idle_nudge` and empty reason return an error and do not call the notifier. |
| 4 | TestHelperEventId_RepeatIsOneRow | Unit | Reload shows the parent verdict row once | Same id twice yields one append. |
| 5 | TestResumeSkipsHistoricalInvocationNotCallId | Unit | A native helper resumes after restart without repeating a finished tool | The executor count for the historical occurrence-plus-call-id stays 1. A new assistant occurrence that reuses `call_0` is executed. A side effect outside the transcript is counted separately from a deduplicated row. |
| 6 | TestDeliver_MetVerdictSaveThenPlace | Integration | The parent is given the verdict before it is marked read; Met verdict and hand-back are one turn | Chat-membership save exists before the verdict ack. Model order is user, assistant tool call, tool result, then the placed input. Zero placements while an approval is open, while a question card is unanswered, while the parent is intentionally stopped, or after the final was polled. One placement across a crash. Restart wake then same-generation finish are two ids and two turns. A task-owned met verdict plus completion is one turn. |
| 7 | TestDeliver_UnmetVerdictWakesOnceNoGoalOutcome | Integration | Unmet round is a notice | Parent transcript has `verdict_unmet` and no `goal_outcome`. One wake. |
| 8 | TestDeliver_NotVerifiedRowAndOneWake | Integration | Not verified row | Parent transcript contains the existing conclusion sentence. Goal stays active. One wake. |
| 9 | TestGoalOutcome_WrittenOnHelperAndParent | Integration | Goal met row; helper keeps its row | Two transcripts, two ids, one ending payload. |
| 10 | TestStopNotice_IntentionalStopNoWake | Integration | User Stop of the helper writes a row and starts nothing | Parent-agent Stop and person Stop each write one `stopped` row, call no wake, join no live turn, and do not resume. Boot replay of the unacknowledged notice still does not wake. |
| 11 | TestStopNotice_RestartWakesIdleParent | Integration | Restart wakes an idle parent | Parent state `completed`. One wake. `restarted` row present. |
| 12 | TestStopNotice_LandedStopWithoutMarkerBlocksWake | Integration | A Stop marker on the parent blocks every wake | Landed intentional Stop with the marker already cleared, and an accepted Stop whose fence has not landed, both store the report and start no turn. The stopped-root receiver is not relaxed. A restart-stopped parent is woken once by the fresh helper outcome and is not fed its old instruction. |
| 13 | TestStopNotice_HistoricalTimeoutWakesNoResume | Integration | Historical timeout | One wake. Resume dispatcher not called. |
| 14 | TestBoot_NativeHelperResumesFromHistory | Integration | Native helper resumes | Running helper becomes a new dispatch. Last successful tool is not executed. Parent woken once. |
| 15 | TestBoot_ResumeFailureStillWakesOnce | Integration | Failed resume | The restart row stays "Interrupted by a restart." A second `resume_refused` row says resume did not start. One wake. State is not running. Activity does not rewrite the first row. |
| 16 | TestBoot_UserStopNotResumed | Integration | Person's Stop is not resumed by boot | No dispatch. No wake. |
| 17 | TestBoot_ExternalHelperNotReplaced | Integration | External helper is not replaced | No new session. Parent row and one wake. |
| 18 | TestWakeSession_NeverTurnedMain | Integration | Never-turned main | Ordinary record created, then one wake. A helper id is refused. |
| 19 | TestWakeSession_GoneParentNoSubstitute | Integration | A gone parent is not replaced; Another workspace does not receive the row | No second session id is written or woken. A row addressed to workspace A's parent is absent from workspace B's transcript. |
| 20 | TestWakeSession_ApprovalJoinsAndParkedCardHolds | Integration | An open approval stays open; A parked question card does not start an outcome-only turn; A second outcome joins a live parent turn | Approval: one live turn, card still pending, zero `PlaceSavedInput` calls before the tool result. Parked AskUserQuestion with no active turn: row visible, zero new executions and zero placements until the correlated answer, then one continuation carrying both. Completed-between-check-and-enqueue: one eventual idle wake. |
| 21 | TestBoot_SaveThenPlaceOnce | Integration | A crash before the archive write does not drop the verdict | Reopen stores after bus publish (no save, verdict unread), after chat-membership save (no early placement), and after `PlaceSavedInput`. Exact bytes once. `ErrAlreadyConsumed` on a second placement. No second turn. |
| 22 | TestPolledFinal_NoSecondTurnRowRemains | Integration | Result already pulled | No wake turn. Parent goal-outcome row present. Verdict acknowledged. `PlaceSavedInput` is not called again for that final id. |
| 23 | TestProgress_DoesNotCallWakeParent | Integration | Chatter outline | Side-panel frame written. `WakeParentAlways` not called. |
| 24 | TestChatStore_HelperEventInsertsOneRow | Unit (Vitest) | Reload shows the row once | Live frame and replay of the same id produce one message. Queued, then running, then finished does not change that row and does not add a second restart row. A `repair-pending` line is visible in both `SystemMessage` and `VirtualSystemMessageRow` when the event row is absent, and is distinct from an empty thread. |
| 25 | TestChatStore_ParentGoalOutcomeRendersInParentBucket | Unit (Vitest) | Goal met in the parent chat | Frame session id is the parent. Headline starts with "Goal met". |
| 26 | TestE2E_WokenReplyStreamsWithoutReload | E2E | Open parent chat shows the woken reply live; Reload shows the reply the live chat showed; Two browsers share one wake turn | Two browsers on one parent session. Hold the stream after the first partial token and before completion. Both show that token. One turn. A different workspace's bucket is unchanged. Reload then shows the finished reply. |
| 27 | TestDeliver_QuestionBlockerFailureWakeOnce | Integration | Questions, blockers, and failures wake once | Each event publishes one wake and one parent-chat row. A second delivery of the same id does not. |
| 28 | TestDeliver_OrdinaryNonFatalDoesNotWake | Integration | An ordinary non-fatal error does not wake | Stored. `WakeParentAlways` is not called. A `max_tool_iterations:` notice still wakes. A `stopped_child:` notice does not. |
| 31 | TestWakeIdentity_DistinctEventsDoNotCollapse | Integration | Reload shows the parent verdict row once; Met verdict and hand-back are one turn | Two restart stops are two wake ids. A restart wake then a same-generation finish are two wake ids and two turns. Two questions and two verdict rounds each wake. Redelivery of one id is one turn. Row counts alone are not this proof. |
| 32 | TestWakeSession_CreatorNoticeOneWebchatTurn | Integration | A creator notice uses the shared entry and streams on webchat | The test calls `WakeSession` with `creator_notice`. One turn, stream channel `webchat`, stored channel unchanged. |
| 33 | TestGoalOutcome_ColdRestLiveReplayOneRow | Unit (Vitest) | Reload shows the parent verdict row once | `rawToMessage`, live frame, and replay of one id produce one row in both `SystemMessage` and `VirtualSystemMessageRow`. A parent model that does not echo the event still leaves the row. |
| 34 | TestHelperEvent_RepairSurvivesInboxCompaction | Integration | A transcript write failure is not reported as a shown notice | Fail a question row, consume the wake, acknowledge the inbox message, force `compactLocked` past the acked retention, reopen the stores, run the boot repair pass. The lifecycle repair record restores one row. Zero new turns. A stop-only case is not this test. |
| 29 | TestGetStreamer_MissingSessionDoesNotGuess | Integration | A wake with no session is not attached to another chat | An empty session id is refused. A different session that is open receives none of the turn's tokens. |
| 30 | TestHelperEvent_WriteFailureIsNotShown | Integration | A transcript write failure is not reported as a shown notice | The first write fails and is not reported as shown. The retry writes one row and wakes once. |

### Hands-on UAT (agent-engine landing)

Required on the exact commit being landed, by a tester and an independent validator. Provider `openrouter`, model `deepseek/deepseek-v4.1-flash`. Not the founder's live instance. Screenshots must show the workspace name and the composer identity (parent agent name, not the helper).

| Row | Steps | Pass |
|---|---|---|
| UAT-1 | Open the parent chat. Ask the parent to delegate a short task and not to poll. Wait until the parent's turn ends. Stay on the chat. | The parent's next reply appears live, without reload. |
| UAT-2 | Delegate a goal the Judge can confirm. Stay on the parent chat. | A "Goal met" row is visible in the parent chat. The parent's reply does not say the verdict was missing. Composer is the parent. |
| UAT-3 | Press Stop on the helper only, while the parent is idle. | Parent chat shows a stopped row. No new parent turn. Helper stays stopped. |
| UAT-4 | Start a native helper whose tool appends one line to an append-only fixture file, correlated to that assistant occurrence and call id. Restart the gateway after that line exists. | The fixture file still has one line for that historical occurrence and call. A new model decision may append a different line. The transcript's single success row is supporting history, not the proof. Parent chat shows one immutable restart row and a live parent turn. Tester and validator both read the fixture on this commit. A unit-test receipt does not replace them. |
| UAT-5 | Reload the parent chat after UAT-2 and UAT-4. | The verdict row, the restart row, and the parent replies are present once each. |
| UAT-6 | A helper that emits progress while the parent is idle. | Side panel updates. The parent does not start a turn until a real ending. |

### Test datasets

#### Dataset: Wake reason

| # | Input | Boundary type | Expected | Traces to |
|---|---|---|---|---|
| 1 | `finished` | happy | one wake if idle | Questions, blockers, and failures |
| 2 | `verdict` | happy | one wake | Parent is given the verdict |
| 3 | `restart` | happy | one wake | Restart wakes an idle parent |
| 4 | `creator_notice` | alternate | one wake on webchat | Creator notice |
| 5 | `idle_nudge` | error | refused, no wake | Unknown reason |
| 6 | empty | error | refused, no wake | Unknown reason |
| 7 | intentional Stop, actor parent-agent or person, first delivery and boot replay | edge | row, no wake, no join, no resume | User Stop of the helper |
| 8 | `timed_out` | edge | one wake, no resume | Historical timeout |

#### Dataset: Stream channel

| # | Stored channel | Peer | Expected stream | Traces to |
|---|---|---|---|---|
| 1 | `main` | empty | `webchat` + session id | Stream channel outline |
| 2 | empty | empty | `webchat` + session id | Stream channel outline |
| 3 | `telegram` | set | `telegram` + peer | Stream channel outline |
| 4 | `webchat` | set | `webchat` + peer | Stream channel outline |

#### Dataset: Resume tool history

| # | Last tool record | Expected | Traces to |
|---|---|---|---|
| 1 | success | not invoked again | Native helper resumes |
| 2 | no tool yet (queued) | dispatch, nothing to skip | Native helper resumes |
| 3 | tool started, no result | not treated as success; model may retry as a new decision | Native helper resumes |
| 4 | user Stop marker | no dispatch | Person's Stop is not resumed |

### Regression

| Existing behaviour | Existing test to keep green | New test |
|---|---|---|
| Fatal error wakes, ordinary non-fatal does not | Inbox fatal-vs-non-fatal test (name contains `FatalError`) | Test 1 must not flip that case |
| Helper's own live view is webchat | Reconstruct tests that a steered child streams on webchat | Test 2 must not change the child branch |
| Consumed final does not double-wake | Tests around `recordPolledFinalConsumed` | Test 22 |
| Goal-outcome headline copy | `src/lib/goalOutcome.test.ts` | Test 25 uses the same headline |
| Not-verified sentence | `pkg/agent/judge_conclusion_test.go` | Test 8 asserts the same sentence on the parent transcript |
| User Stop does not auto-continue | Stop-marker tests on `wakeOwnerOrStore` | Tests 10, 12, 16 |

This is a behaviour change, not a new isolated capability. The regression column is the protection.

---

## Functional Requirements

- **FR-001**: The system MUST stream an ordinary-root wake turn on `webchat` when the stored channel is `main` or empty, without rewriting the stored channel. (PW-D1)
- **FR-002**: The system MUST leave a non-`main` stored channel with a peer on that channel. (PW-D1)
- **FR-003**: The system MUST show that live turn to every browser subscribed to the session, as one turn. (PW-D1)
- **FR-004**: The system MUST write one goal-outcome row on the parent transcript when a helper's goal ends, and MUST keep the helper's own row. (PW-D2)
- **FR-005**: The system MUST save the exact combined input as a chat-membership archive record before acknowledging the verdict, and MUST place that record into model history at most once, at the safe consumption fence, never before an unfinished tool result, and never for a held, stopped, or already-polled outcome. A bus publish is not the save. (PW-D2, PW-D16)
- **FR-006**: The system MUST write a `verdict_unmet` helper-event row and wake once when a Judge round is unmet and the goal continues, and MUST NOT write a goal-outcome ending for that round. (PW-D2)
- **FR-007**: The system MUST write a `not_verified` helper-event row containing the existing conclusion and wake once, leaving the goal active. (PW-D2)
- **FR-008**: The system MUST write a parent-chat row for helper user-Stop, restart, and historical timeout. (PW-D3)
- **FR-009**: The system MUST wake an idle parent once for a restart notice and once for a historical timeout, and MUST NOT resume a historically timed-out helper. (PW-D3)
- **FR-010**: The system MUST NOT wake, join, or resume after an intentional helper Stop by the parent agent or by a person, on first delivery or on boot replay, and MUST NOT resume a parent that was intentionally stopped. A restart-stopped parent MAY be woken by the fresh helper outcome from that same restart (PW-D14). (PW-D13)
- **FR-011**: The system MUST NOT wake the parent for progress, checkpoint, or artifact, and MUST still store and side-panel those messages when the inbox accepts them. (PW-D4)
- **FR-012**: The system MUST keep the inbox rate and unacked caps on progress, checkpoint, and artifact. (PW-D4)
- **FR-013**: The system MUST start at most one parent turn for one helper ending, including met verdict plus hand-back, and including a task-owned met goal. (PW-D5)
- **FR-014**: The system MUST resume a native helper cut off by restart from its transcript, and MUST NOT replay a historical tool invocation identified by issuing assistant occurrence plus call id. A new occurrence MAY reuse the provider call id. (PW-D6, PW-D21)
- **FR-015**: The system MUST wake the parent once for that restart after resume has been admitted or has failed, MUST keep the restart row as the immutable sentence "Interrupted by a restart.", and MUST write a separate `resume_refused` row only when admission fails. Current queued, running, or finished status MUST stay on the Activity panel and MUST NOT edit the restart row. (PW-D6)
- **FR-016**: The system MUST NOT auto-resume an external command-line helper and MUST NOT create a replacement worker. (PW-D6)
- **FR-017**: The system MUST expose one wake entry, `WakeSession`, with the closed reason list, the channel rules, the live-turn join, the never-turned-main record, and the Stop-marker refusal. (PW-D8)
- **FR-018**: The system MUST refuse any wake reason outside that list. (PW-D8, PW-D10)
- **FR-019**: The system MUST NOT invent a recipient when the parent is gone, hidden, or removed, and MUST NOT copy the row into a chat in another workspace. (PW-D12)
- **FR-020**: The system MUST keep a tool-approval card up and join that live turn, and MUST keep a parked question card valid, show the helper row immediately, retain the report, and deliver it with the person's correlated answer without an extra outcome-only turn. (PW-D15)
- **FR-021**: The system MUST NOT start a second turn when the final was already consumed or when a wake was already consumed before a crash, MUST start at most one turn when a published wake was never consumed, and MUST still keep a required verdict row. (PW-D5)
- **FR-025**: The system MUST wake an idle parent once for a helper question, a blocker, or a fatal failure, and MUST show that text in the parent chat. (PW-D4)
- **FR-026**: The system MUST NOT wake the parent for an ordinary non-fatal error that is not a lifecycle notice. (PW-D4)
- **FR-022**: The system MUST converge live frame, replay, and reload on one row per deterministic id. (PW-D9)
- **FR-023**: The system MUST NOT change goal idle timers, the no-goal idle checker, or `enqueueSteeringFromMessage`. (PW-D10, PW-D11)
- **FR-024**: The system MUST update the user-facing pages in section 14 in the same change. (definition of done)

---

## Success Criteria

- **SC-001**: On the landing commit, UAT-1 shows a parent reply token in the open chat after the first partial token and before the turn completes. A reply that appears only when the turn is already finished does not pass.
- **SC-002**: On the landing commit, UAT-2's parent chat contains a "Goal met" row and the parent reply does not claim the verdict was absent.
- **SC-003**: A test proves a met goal publishes one parent wake, not two.
- **SC-004**: A test proves three in-cap progress messages publish zero parent wakes.
- **SC-005**: A test proves a historical invocation (occurrence plus call id) is not executed again, counted on the executor or an external side effect, and that a new occurrence reusing that call id does run.
- **SC-006**: A test proves a user Stop of the helper publishes zero parent wakes and zero resume dispatches.
- **SC-007**: UAT-5 shows the parent rows once after reload.
- **SC-008**: Every behavioral FR row in the matrix has at least one BDD scenario and one planned test that exercises that scenario. FR-024 is the documentation review, not a behavioral test, and is not counted as one.

---

## Traceability Matrix

| Requirement | User story | BDD scenario | Test |
|---|---|---|---|
| FR-001 | US-1 | Scenario Outline: Stream channel follows the parent's real destination; Scenario: A web wake does not rewrite the stored channel | Test 2 |
| FR-002 | US-1 | Scenario Outline: Stream channel follows the parent's real destination | Test 2 |
| FR-003 | US-1 | Scenario: Open parent chat shows the woken reply live; Scenario: Reload shows the reply the live chat showed; Scenario: Two browsers share one wake turn; Scenario: A wake with no session is not attached to another chat | UAT-1, Test 26, Test 29 |
| FR-004 | US-2 | Scenario: Parent chat shows Goal met without the model repeating it; Scenario: The helper keeps its own outcome row | Test 9, Test 25 |
| FR-005 | US-2 | Scenario: The parent is given the verdict before it is marked read | Test 6 |
| FR-006 | US-2 | Scenario: An unmet round is a notice, not an ending | Test 7 |
| FR-007 | US-2 | Scenario: Not verified is a parent-chat row and one wake | Test 8 |
| FR-008 | US-3 | Scenario: User Stop of the helper writes a row and starts nothing; Scenario: Restart wakes an idle parent and shows a row; Scenario: A historical timeout wakes and does not resume; Scenario: A transcript write failure is not reported as a shown notice | Tests 10, 11, 13, 30, 34 |
| FR-009 | US-3 | Scenario: Restart wakes an idle parent and shows a row; Scenario: A historical timeout wakes and does not resume | Tests 11, 13 |
| FR-010 | US-3, US-5 | Scenario: User Stop of the helper writes a row and starts nothing; Scenario: User Stop of the parent starts nothing; Scenario: A Stop marker on the parent blocks every wake; Scenario: A helper the person stopped is not resumed by a later boot | Tests 10, 12, 16 |
| FR-011 | US-4 | Scenario Outline: Chatter is stored and shown and does not wake | Tests 1, 23 |
| FR-012 | US-4 | Scenario: A progress line over the inbox cap wakes nobody | Test 1 |
| FR-013 | US-4 | Scenario: Met verdict and hand-back are one turn | Test 6 |
| FR-014 | US-5 | Scenario: A native helper resumes after restart without repeating a finished tool; Scenario: The helper chat says it was interrupted by a restart | Tests 5, 14 |
| FR-015 | US-5 | Scenario: A failed resume still tells the parent once | Test 15 |
| FR-016 | US-5 | Scenario: An external helper is not replaced after restart | Test 17 |
| FR-017 | US-6, US-7 | Scenario: A creator notice uses the shared entry and streams on webchat; Scenario: A never-turned main is recorded and then woken; Scenario: A second outcome joins a live parent turn | Tests 18, 20, 32 |
| FR-018 | US-6 | Scenario: An unknown wake reason is refused | Test 3 |
| FR-019 | US-7 | Scenario: A gone parent is not replaced; Scenario: Another workspace does not receive the row | Test 19 |
| FR-020 | US-7 | Scenario: An open approval stays open; Scenario: A parked question card does not start an outcome-only turn | Test 20 |
| FR-021 | US-7 | Scenario: A result the parent already pulled does not start another turn; Scenario: A crash before the archive write does not drop the verdict | Tests 21, 22 |
| FR-022 | US-2 | Scenario: Reload shows the parent verdict row once | Tests 4, 24, 31, 33 |
| FR-023 | US-6 | Scenario: An unknown wake reason is refused | Test 3 |
| FR-024 | US-1, US-2, US-3 | Section 14 pages, checked by docs-verifier against the landed behaviour | docs review, not a Go test |
| FR-025 | US-4 | Scenario Outline: Questions, blockers, and failures wake once | Test 27 |
| FR-026 | US-4 | Scenario: An ordinary non-fatal error does not wake | Test 28 |

---

## 13. Dependency graph and implementation order

```text
1. Contract (HelperEvent, Message subtype, GoalOutcome description, AsyncAPI frame)
      → regenerate generated types
2. WakeSession + channel rule + classifyEnvelope (progress does not wake)
      → RED tests 1–3, 23
3. Verdict body, parent goal-outcome write, ack order
      → RED tests 6, 7, 8, 9
4. Helper-event writes for stop, restart, timeout
      → RED tests 10–13
5. Boot resume for native helpers, skip completed tools
      → RED tests 5, 14–17
6. Edges: never-turned, gone, live-turn join, crash, polled final
      → RED tests 18–22
7. SPA helper-event row + parent goal-outcome already rendered by existing code
      → Vitest 24, 25
8. User docs (section 14)
9. E2E stream test, then hands-on UAT on the candidate commit
```

Steps 2 and 7 can proceed in parallel after step 1. Step 5 depends on step 4's row writer and step 2's entry. Do not start step 5 by editing `enqueueSteeringFromMessage`.

Owners: backend-lead for steps 1–6, frontend-lead for step 7, the implementing lead drafts step 8, docs-verifier audits it, qa-lead writes the RED tests and does not edit production code.

WU-15 may edit `pkg/agent/steering.go::enqueueSteeringFromMessage` on another branch. This unit does not. Conflict resolution must not take a rooms change into this branch or a wake-channel change into the rooms branch by accident.

---

## 14. User-facing documentation TODOs

Same change as the behaviour. Draft by the implementing lead. `docs-verifier` checks the sentences against the code.

| ID | Page | What to change |
|---|---|---|
| DOC-1 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/agents.md` | In "Workers and delegation": the parent chat shows the helper's finished result live, without a reload, including a question, a blocker, a failure, and a goal-less finish, not only when the model repeats them. A Judge ending also shows in the **parent** chat. Progress does not start a parent turn. A restart of a **native** helper continues it from saved history and does not redo a tool invocation that already finished; the parent chat says it was interrupted by a restart, and that sentence does not change when the helper later waits, runs, or finishes. If resume cannot start, a second line says so. Current status stays in Activity. If that same restart also cut off the parent, the parent is woken with the helper event and is not handed its old interrupted instruction again. A helper Stop, whether the parent agent or a person pressed it, writes a stopped row and does not wake or resume anyone. An intentional Stop of the parent is not overridden by a later helper event. A question card stays until the person answers; a helper result that arrives meanwhile is shown and is handled with that answer, not as a separate turn. Keep the external command-line rule. |
| DOC-2 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/goals.md` | Where it says the parent agent receives the helper's conclusion: the **parent chat** shows that conclusion as a row (Goal met, not met yet, or Not verified). The helper chat still shows its own row. One ending wakes the parent once. |
| DOC-3 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-wt/parent-wake/docs/troubleshooting.md` | Add a short entry: if a delegated helper has finished and the open parent chat is blank, reload once after upgrading; on a current version the reply and the Judge row appear without a reload. If they appear only in the helper's chat, that is the old behaviour. Do not tell the person to restart as the fix. |

No other user-facing page needs a change. Internal docs are not a substitute for these three.

---

## 15. Sequencing note for the scheduled-task spec

That spec's section 5.7 calls `WakeParentAlways` and `reportingTargetFor` itself, and it says not to edit `wakeStoredMessage`. After this unit lands, FQ-4's implementation MUST call `WakeSession` with reason `creator_notice` instead of calling `WakeParentAlways` directly. The notice text, the 500-byte cap, the eligible-main rule, and "do not pick another session" stay in that spec. This unit does not change them.

If that branch merges first, the follow-up is a mechanical switch onto `WakeSession`. Leaving the direct `WakeParentAlways` call in place would stream the creator notice on channel `main` again.

---

## Ambiguity Warnings

| # | What's ambiguous | Normative choice in this spec | Question |
|---|---|---|---|
| 1 | Closed. Q-STOP. | PW-D13. A helper Stop by the parent agent or by a person never wakes and never auto-resumes. The stopped row may stay. | None. |
| 2 | Closed. Q1. | PW-D14. The fresh helper outcome wakes a parent that the same restart stopped. The old instruction is not replayed. An intentional Stop is never overridden. | None. |
| 3 | Closed. Q2. | PW-D15. Parked question card: row now, deliver with the person's answer, no extra turn. | None. |
| 4 | "Not verified" versus "Goal not met". | Keep both existing sentences. | None. |

---

## Evaluation Scenarios (Holdout)

These are for a person evaluating the finished system. They are not part of the test plan and must not be copied into new tests as the oracle.

### Holdout: The reply shows up while you watch

- **Setup**: Parent chat open. Ask for a delegated summary. Do not poll.
- **Action**: Wait, without reloading, after the parent's first reply has ended.
- **Expected outcome**: The next parent reply grows on screen.
- **Category**: Happy Path

### Holdout: The verdict is in the chat you are in

- **Setup**: A goal a helper can finish.
- **Action**: Stay on the parent chat until the Judge has ruled.
- **Expected outcome**: You can point at a Goal met or Not verified row without opening the helper.
- **Category**: Happy Path

### Holdout: Chatter does not start the parent

- **Setup**: A helper that reports several progress lines.
- **Action**: Watch the parent chat.
- **Expected outcome**: The side panel moves. The parent does not start writing a new reply until something actually ends or asks.
- **Category**: Happy Path

### Holdout: Stop means stop

- **Setup**: A running helper.
- **Action**: Press Stop on the helper.
- **Expected outcome**: The parent chat notes it. Nobody starts working again by themselves.
- **Category**: Error

### Holdout: A restart does not redo the file write

- **Setup**: A native helper that has just written a file successfully and is still running.
- **Action**: Restart the gateway. Look at the file and both chats.
- **Expected outcome**: The file was not written a second time by the automatic resume. Both chats mention the interruption. The parent starts once.
- **Category**: Edge Case

### Holdout: The other workspace stays quiet

- **Setup**: Parent lives in workspace A. You are looking at workspace B.
- **Action**: Let the helper finish.
- **Expected outcome**: Workspace B's chat does not gain the row. Workspace A's parent chat does.
- **Category**: Edge Case

### Holdout: Reload matches what you saw

- **Setup**: After a live Goal met row and a live reply.
- **Action**: Reload the parent chat.
- **Expected outcome**: The same row and the same reply, once each.
- **Category**: Happy Path

---

## Assumptions

- The branch under spec is `a1eb083a4`. A later merge that changes `reconstructSteeredTurn` or boot recovery has to be re-checked against section 1.
- "Shown" for progress means the existing side panel, not a new chat bubble.
- The hands-on model is the repo's standing UAT provider, not a model chosen in this spec.
- Task-owned goals already wake on their verdict. This spec only stops that from becoming two turns when a completion notice follows.
- FQ-4 is not implemented here. Only the entry it must call.

## Clarifications

### 2026-10-15

- Q: Does a woken parent turn use the reporting-target channel? → A: No. The bus message is `system`. The turn channel is session metadata, `main` for a main session. PW-D1 changes the turn's stream channel only.
- Q: Is the "Goal met" card `writeGoalVerdictTranscript`? → A: No. That writes `judge_verdict`. The card is `recordGoalOutcome`.
- Q: Do progress messages have no cap? → A: The wake debounce does not apply. The inbox rate and unacked caps do.
- Q: Should this unit auto-resume the person's own chat? → A: No. Helpers only.
- Q: User Stop of the helper — wake or only a row? → A: Closed by PW-D13. No wake and no resume, for a parent-agent Stop as well as a person's Stop. The row may stay.
- Q: Q1, restart-stopped parent? → A: PW-D14. The fresh helper outcome wakes it. The interrupted instruction is not replayed.
- Q: Q2, parked question card? → A: PW-D15. Show and retain the outcome. Deliver it with the person's answer. No extra turn while the card is unanswered.

---

## 16. Governing decisions this spec narrows

No new ADR. The founder interview of 2026-10-15 settled the policy. The architect follows up by dating these three existing decisions. This section is the amendment map. It does not start another grill.

| Decision, by title | What stays | What this spec narrows |
|---|---|---|
| The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume | A landed Stop is retained. Boot does not dispatch a session that was intentionally stopped. Selected-execution Stop still protects that execution. | A native helper whose landed cause is restart is dispatched again from history (PW-D6). That is the only boot dispatch this unit adds. |
| Steering commands: no person question | A new instruction is not a replay of an old report. No helper person-question pause is reintroduced. | A restart-stopped parent's next input is the fresh helper event, not the parent's interrupted instruction (PW-D14). |
| Session core with an agent address book: reuse one standing session, one archive and the existing execution paths | One archive. Tool results stay occurrence-scoped (D2). Placement into model history stays at the safe consumption fence (D2 / U2-ARCHSHAPE). Ordinary admission is unchanged. | Progress, checkpoint, and artifact no longer wake (PW-D4). They remain accepted inbox messages when the caps allow. The helper input is saved as chat membership and placed with `PlaceSavedInput` only at that fence (PW-D16). It is not appended as model membership early. |

Also unchanged: parent-edge ownership, no external-worker replacement, and the two-layer tool policy. Broad viewing by any authenticated operator, as in **A sub-agent is a session steered by another session** (viewing authority), is accepted. This unit does not invent a per-account privacy rule.

## 17. Security and user promises

| Promise | Where it is kept |
|---|---|
| An intentional Stop is not turned into compute | PW-D13, PW-D19. First delivery, live join, and boot replay. |
| A restart does not redo a finished tool invocation | PW-D21. Occurrence plus call id. |
| The parent sees the event without trusting the model to repeat it | The chat row is written before the model turn. |
| Sensitive text is filtered before it is stored or woken | The same filter `asyncNotifierImpl.Notify` already applies, on row text and on the combined input. |
| A missing parent is not replaced | PW-D12. |
| Tool policy is not widened | No new tool. No new allow. Registration of `delegate` and `message_parent` is unchanged. |
| What the public pages will say | Section 14. Docs-verifier checks them. They are not a behavioral test. |

## 18. Screen states and the journey

No new screen. The existing parent chat and the helper chat are the surfaces. Loading, empty, and reconnect stay the chat's existing behaviour. This unit adds only the states below.

| State | What the person sees |
|---|---|
| Nothing owed | No new row and no pending-save line. An empty chat stays empty. |
| Event row | One immutable past-tense line, for example "Interrupted by a restart." A repeat of that event does not edit it and does not add a second copy. |
| Queued, running, or finished after that event | The row text stays. Activity shows waiting, running, or finished. Promotion does not write another restart row. |
| Resume refused | The first restart row stays. A second immutable row says "Resume did not start." Activity does not show the helper as running. |
| Parent row failed, helper row saved | The parent chat shows a nonfatal system line, id `repair-pending-<row id>`: "A helper event could not be saved in this chat. Omnipus will retry. This is not a failure of the helper's work." This is not the same as nothing owed. When repair writes the event row, the pending line stays and the event row appears once. No extra wake. |
| Reconnect / catch-up | Replay draws stored rows once, including a pending line that is still owed. It does not announce them again as new. |
| Parked question | The card stays. The helper row is visible beside it. The saved input is not in model history yet. |
| Very long text | The row truncates on a narrow or touch layout and expands. Expansion is the same control as section 19. |

Journey: the person asks the parent to delegate and stays on that chat. The parent turn ends. The helper works. Progress updates the side panel only. When the helper finishes, fails, asks, or the Judge rules, a row appears in the parent chat and, unless a Stop or a parked card says otherwise, the parent's reply starts in that same chat. Activity still has Open for the helper. A reload shows the same rows once. A gateway restart of a native helper adds the restart row, continues the helper, and wakes the parent with that event.

## 19. Accessibility

The new row uses a native `details` / `summary` control, the same semantics as `GoalOutcomeRow`, not a click-only decoration. Keyboard opens and closes it. The accessible name is the row's sentence. Focus stays in the composer or on the open approval or question card when the row arrives. One live announcement names the new row. Replay and reload do not announce old rows again. Both `SystemMessage` and `VirtualSystemMessageRow` get the same control. Tests cover keyboard toggle, focus staying put, one announcement for a new row, and silence for replay.

## 20. Design-system reuse

| Job | Reuse | Not done |
|---|---|---|
| Lasting outcome line | Share the existing domain row behaviour of `src/components/chat/GoalOutcomeRow.tsx` (`GoalOutcomeRow`) for goal endings. Helper events use the same native disclosure, not a second expander implementation. | No new public catalogue component. No new colour, icon family, or emoji. |
| Empty and error chat states | Inherit the chat's existing empty and error presentation. Catalogue entries Accordion, EmptyState, QueryErrorState, and Skeleton stay available and are not re-skinned. | No new brand. |
| Verification | `npm run typecheck` and the design-system gate that already covers `src/components/`. Component tests for both render paths and the states in section 18. | A reducer-only test is not this verification. |

## 21. Round-1 disposition

Grill round 1, `docs/internal/specs/parent-wake-visibility-spec-review.md`, verdict BLOCK. The rows below are that round's history. Where a row disagrees with section 22, section 22 is the correction. No finding is deferred. No new founder question is open.

| Finding | Disposition |
|---|---|
| CRIT-001 | Fixed, then narrowed by section 22 (NEW-MAJ-001). The durable fact is the chat-membership save, before acknowledgement. Placement into model history is a later step. |
| MAJ-001 | Fixed. PW-D13. `stopped` is not a wake reason. Parent-agent and person Stops, first delivery, live join, and boot replay. Cause is the landed transition, not the `stopped_child:` prefix. Tool-iteration notices still wake. Q-STOP closed. DOC-1 updated. |
| MAJ-002 | Fixed. PW-D19 and the section 9.4 state table. In-flight, accepted-but-unlanded, and landed Stop (marker cleared) refuse a wake. The stopped-root receiver is not relaxed. PW-D14 is the only restart-stopped exception. Self Stop does not cancel an unselected helper. |
| MAJ-003 | Fixed. PW-D20. Stop sequence, goal id plus round, conclusion id. Test 31 covers two events that share a generation or a round. |
| MAJ-004 | Fixed. Section 9.4. A normal completion between the live check and enqueue falls through to one idle wake. Test 20. |
| MAJ-005 | Partially fixed in round 1, closed in section 22. The shared id is only the met ending's existing final id. A restart, a later finish, a question, and a Judge round do not share it. |
| MAJ-006 | Fixed. Section 9.3 kinds `finished`, `question`, `blocker`, `failed`, plus the Stop-family and verdict kinds. Cold REST, live, and replay. Test 33 uses a parent model that does not echo the event. |
| MAJ-007 | Fixed. PW-D21. Occurrence plus call id. Test 5 counts the executor separately and allows a new occurrence to reuse `call_0`. |
| MAJ-008 | Partially fixed in round 1, closed in section 22. The repair source is not the acknowledged inbox body. Test 34 compacts the inbox and still restores a question row. |
| MAJ-009 | Closed as a minor in section 22. The edge-case list no longer says a parked question joins a live turn. |
| MAJ-010 | Partially fixed in round 1, closed in section 22. Event rows are immutable. Activity holds current status. A failed parent save shows a pending line, which is not an empty chat. |
| MAJ-011 | Fixed. Section 19. Native disclosure, one announcement, no replay announcement, focus preserved. |
| MAJ-012 | Partially fixed in round 1, closed in section 22. UAT-4's proof is an append-only fixture counted before and after restart, not a deduplicated transcript row. The earlier test-instrument fixes stay. |
| MAJ-013 | Fixed. The opening states no new ADR, and section 16 is the amendment map by decision title. |
| MIN-001 | Fixed. Section 8 no longer points at a missing section 6. The Stop warning is closed under PW-D13, not PW-D1. Section 9.8 describes the real stale comment: not wake-eligible, which becomes true again. |
| MIN-002 | Fixed. Section 20 names `GoalOutcomeRow` and the catalogue entries, and states that no public component is added. |
| OBS-001 | Fixed. `WakeSession` no longer takes a row-written flag. Section 9.4. |

Author prompts from round 1, decided there and not re-asked: A1 (durable before ack), A2 (one id per met ending only — section 22), A3 (repair that outlives inbox ack — section 22), A4 (companion path and revision `80559308f` in section 9.4).

## 22. Round-2 disposition

Grill round 2, `docs/internal/specs/parent-wake-visibility-spec-review-round2.md` at `a2b10f814`, verdict REVISE. Zero critical, five major, one minor. Every finding is fixed in this document. None is deferred. None needed a new founder choice: each fix is a contract or a test oracle inside the policy the founder already set. No review PASS is claimed. There is no third grill.

| Finding | Disposition |
|---|---|
| MAJ-005 | Fixed. PW-D17 and the section 9.4 identity table. The met ending keeps the existing `<child session>:<generation>:final`, which poll already consumes. A restart uses `stoppedChildNoticeID`. A later finish in that same generation uses the final id, so the restart wake does not eat it. A question, a blocker, a goal-less finish, and a fatal failure use that delivery's inbox message id. An unmet round uses `goalVerdictUpwardMessageID`. Not verified uses the conclusion id. Test 31 asserts two restarts, a restart then a same-generation finish, two questions, and two verdict rounds are distinct wake ids, and one redelivered id is one turn. |
| NEW-MAJ-001 | Fixed. PW-D16 and section 9.5. Save is a chat-membership append, id `saved-input-<event id>`. `appendTranscriptLocked` rejects model membership, so the save is not described as one. Place is `archiveBackend.PlaceSavedInput` once, at the fence, after an unfinished tool result. `AppendModelMessage` is not used. Ack follows the save, not the place. A parked card, an intentional Stop, and an already-polled final do not place. Tests 6, 20, 21, and 22 assert model order, zero early placements, one placement across a crash, and `ErrAlreadyConsumed` on a second place. |
| MAJ-008 | Fixed. PW-D18 and section 9.9. Stop-family repair reads the landed control-ledger transition. A goal ending reads the goal record. A question, blocker, finish, failure, unmet round, or not-verified conclusion has a chat-membership repair record on the child lifecycle journal, id `repair-<row id>`, written before inbox ack. `compactLocked` does not read that journal. The record is deleted only after every destination has the row id. Test 34 fails a question row, acknowledges the inbox, forces compaction past the acked retention, reopens the stores, and runs the boot repair pass: one restored row, zero new turns. A stop-only case is not that test. |
| MAJ-010 | Fixed. Sections 9.3, 9.7, 10, and 18. A `restarted` row is the immutable sentence "Interrupted by a restart." Queued, running, and finished stay on the Activity panel and do not edit the row. A refused resume is a second immutable row, kind `resume_refused`, id `resume-refused:` plus the restart notice id, and it does not wake a second time. A failed parent save shows `repair-pending-<row id>`: "A helper event could not be saved in this chat. Omnipus will retry. This is not a failure of the helper's work." That line is required in both `SystemMessage` and `VirtualSystemMessageRow`, and it is distinct from an empty thread. Test 15 and Test 24. |
| MAJ-012 | Fixed. UAT-4. The helper's tool appends one line to an append-only fixture, correlated to that assistant occurrence and call id. After restart the fixture still has one line for that historical pair. A later new model decision may append a different line (PW-D21). The transcript row is supporting history, not the proof. Tester and validator both read the fixture on the exact commit. Test 5 counts the executor separately from a deduplicated transcript row. |
| MAJ-009 | Fixed. The edge-case list, the behavioral contract, US-7 acceptance scenario 4, and the approval and parked-card scenarios split the two states. A live tool approval joins and is not placed ahead of the unfinished tool result. A parked question card holds, shows the row, and does not place into model history until the person's answer. Test 20. |

No founder question is open.
