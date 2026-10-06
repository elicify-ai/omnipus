# ADR-20261006 — Session core with an agent address book: one queue per session, default session per agent and workspace

- **Status:** Proposed (design only; no code changes in this ADR). Awaiting its one `grill-spec` ADR-mode review.
- **Date:** 2026-10-06
- **Deciders:** Daniel Piatkowski (founder). The founder decisions of 2026-10-06 (waiting room removed, Q20, the queue model, the inbox refinement, main/worker agents, the address book, Q24 to Q28) are recorded here as decisions. Everything else is marked **Proposed (architect)** and is open to the founder.
- **Author:** architect.
- **ID:** minted with `scripts/new-adr-id.sh` (date-and-title scheme, see `scripts/check-adr-id-scheme.sh`).
- **Relates to (does not reopen):** [A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md) (a helper is an ordinary session); [The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](./ADR-20260928-sub-agent-control-plane.md) (the Stop model); [Steering commands: no person question](./ADR-20261004-steering-commands-no-person-question.md) (messages are steering); [UI-independent turns and session-bound webchat streaming](./ADR-082-ui-independent-turns-and-session-bound-streaming.md) (a turn never depends on a UI connection); [Workspace-scoped heartbeat and global memory UI](./ADR-027-workspace-scoped-heartbeat-and-global-memory-ui.md) (the heartbeat session this ADR generalises).
- **Open pull request this builds on:** PR #1204, branch `work/engine-admission-per-session-20261006` (state at writing: open, not merged). Called **U0** below.

## Plain-English summary

Today the engine decides "which queue does this message go into?" from several different keys. Some use the session id. Some use the agent. Some use the chat id of the connection. Two chats with the same agent can end up sharing a queue, a worker or a history window. That is the cause of the bugs in the audit below.

This ADR makes one rule: **everything about a conversation is keyed by the session id, and nothing else.** To make that possible, a small **address book** turns "agent plus workspace" into a session id **before** a message enters any queue. A main agent has one standing **default session** per workspace. A worker agent gets a new session each time. Each session has one steering queue (messages with authority) and one inbox (reports with no authority). The 5-second waiting room goes away. As a founder proposal (D8, details open), events and tasks for a main agent are worked in a fork of the default session, so the default session stays light.

## Context

### What exists today (verified in the tree at `origin/release/v0.1.1` @ `07679737c`)

| # | Fact | Evidence |
|---|---|---|
| C1 | The turn registry is keyed by `ts.sessionKey`, the routing key, and a second turn on the same key is refused rather than queued. | `pkg/agent/turn.go::registerTurnIfAbsent` (`activeTurnStates.LoadOrStore(ts.sessionKey, ts)`); `pkg/agent/ordinary_execution_admission.go::newTurnStateForAdmission` returns `steer.ErrStaleGeneration` when it is already claimed |
| C2 | The routing key can be agent-level (`agent:<id>:main`) when the message carries no session id. With a session id it is `agent:<id>:session:<sid>`. PR #1204 forces the per-session form whenever `msg.SessionID` is set. | `pkg/agent/loop_inbound.go::agentSessionKey`; `pkg/agent/loop_inbound.go::resolveMessageRoute` (the #1204 hunk) |
| C3 | Today a message can wait in **three** places: the 5 s previous-execution wait, the session worker's own inbox channel (capacity 8), and the in-turn steering queue. | `pkg/agent/ordinary_execution_admission.go::previousExecutionSettleBudget`, `::awaitPreviousOrdinaryExecution`; `pkg/agent/session_worker.go::workerInboxCap`, `sessionWorker.inbox`; `pkg/agent/steering.go::steeringQueue` |
| C4 | `sessionWorker.enqueue` sends a message to the steering queue while a turn runs, and to the worker inbox otherwise. When the inbox is full the message is dropped with "Your message could not be queued". | `pkg/agent/session_worker.go::enqueue`, `::trySteerIntoLiveTurn` |
| C5 | The steering queue is filled under `route.SessionKey` and drained under `ts.sessionKey`. At every step after the first, the turn polls it; each polled item becomes its own message. The default mode takes **one** item per poll. | `pkg/agent/steering.go::enqueueSteeringFromMessage`; `pkg/agent/loop_run_turn.go` (the `iteration > 1` poll of `dequeueSteeringMessagesForScope`); `pkg/agent/steering.go::dequeueItemsLocked`, `parseSteeringMode`; `pkg/config/defaults.go` (`SteeringMode: "one-at-a-time"`) |
| C6 | A helper's report goes to the parent through `message_parent` into a durable inbox. The kinds are `progress`, `checkpoint`, `artifact`, `blocker`, `question`, `handback` (mode `final` or `pause`). The text is described as untrusted. | `pkg/tools/message_parent.go` (kind enum, "Untrusted narration/question text"); `pkg/tools/delegate.go::DelegateInboxStore` |
| C7 | A hand-back wake reaches the parent as a queue item that carries a `wake` pointer, and it is admitted as an ordinary execution under `rec.SessionID`, not the route key. | `pkg/agent/steering.go::steeringQueueItem.wake`; `pkg/agent/loop_inbound.go::admitOrdinaryRootWake` |
| C8 | A heartbeat already creates one eager standing session per (workspace, agent member). Type `heartbeat`, stamped with `workspace_id` and agent, id stored at `member_configs[agent].heartbeat.session_id`, `protected` (delete refused with 409) while the heartbeat is enabled. A worker agent may not have a heartbeat. | `pkg/session/unified.go::NewHeartbeatSession`; `pkg/workspace/member_config.go::MemberHeartbeat`, `ValidateMemberConfigs`; `pkg/gateway/rest_workspaces.go` (FR-010 block in `prepareWorkspacePutMembers`); `pkg/gateway/rest_sessions.go::computeSessionProtected`, delete guard; `contracts/components/schemas/WorkspaceMemberHeartbeat.yaml::session_id` |
| C9 | A schedule picks its session by mode: `isolated` (new session per run), `continue` (job's own session), `main` (id `sched-main-<agent>`, no workspace in the id). | `pkg/gateway/schedules.go::pickSession`; `pkg/cron/service.go::SessionMode` |
| C10 | A task gets its **own** new session (`SessionTypeTask`) every time, whatever the agent. The task, not the session, owns the goal. | `pkg/agent/task_executor.go::createTaskSessionSync`, `::activateTaskGoal` (`GetByOwner(GoalOwnerKindTask, t.ID)`) |
| C11 | An owner (session or task) should have at most one active goal; more is reported as an error. | `pkg/goal/predicate.go::errMultipleActiveGoalsForOwner` |
| C12 | A main versus worker distinction exists. A worker is delegation-only, never a chat target, never the routing default, has no heartbeat. Core and custom agents are chat targets. | `pkg/config/config_agents.go::AgentTypeWorker`, `AgentConfig.IsWorker` (founder Q27: already exists) |
| C13 | A task run dispatched directly (not delegated) has no parent session, and `message_parent` says so. | `pkg/tools/message_parent.go` (the `ToolRunningTaskID` branch) |
| C14 | A person typing into a helper's own chat steers that helper's existing execution. | `pkg/agent/session_worker.go::dispatchSessionWorker` (`deliverHumanHelperInput`); [Steering commands: no person question](./ADR-20261004-steering-commands-no-person-question.md), locked decision 5 |

### The audit (read-only, at `07679737c` against `9e1c79f6f`, backend-lead, 2026-10-06)

The audit summary found **6 Lane A hits** (A1 to A6) and **10 older hits** (O1 to O10) where a conversation structure is keyed by something other than the session id. They share one cause: a routing key or a chat id stands in for the session id. The audit's own severities:

| Group | Hits | What goes wrong |
|---|---|---|
| Turn registry and admission (A1, A2, A5) | registry keyed by routing key; hand-back wake uses `rec.SessionID` while the human turn uses the route key; Stop matching falls back to `ts.sessionKey` | A second turn is refused instead of queued; one conversation has two admission buckets |
| Workers and steering (A6, O1, O2) | workers and in-turn steering keyed by route key | Two conversations sharing a route key share one worker. Message B can be steered into A's live turn. HIGH for SSE/API clients without `agent_id` and channel DMs under `dm_scope=main|per-peer` |
| History window (O3) | history window, recall span and continue guard keyed by routing key | Shared history on agent-level keys |
| Delivery stamps (O5, O7) | web delivery maps keyed by connection chat id; wake debounce keyed by channel and chat id | Two sessions on one connection can get wrong session stamps |
| Cancel latches (A3, A4, O8) | extra and fallback keys in cancel pre-arm; `/stop` may address an agent-level key | Wrong-chat latch possible, bounded by a 5 s TTL |

The audit also lists what is already keyed by session id: steer admission, steering for steered sessions, post-finish revival, session lifecycle ledger and locks, `StopSession`, `StopDelegatedTree`, the `t:`/`s:` cancel latches, the gateway stop/redirect/cancel frames, WebSocket pending-message status, scheduled runs, and tools via `ToolTranscriptSessionID`. The audit labels A4, A5, A6 and O5 as **Inferred** and the others **Verified**; this ADR keeps those labels. I re-verified C1, C2, C5 and the heartbeat code (C8) in the tree myself; I did not re-run the audit's Inferred items.

### Founder decisions of 2026-10-06 that frame this ADR

| Decision | Founder's rule |
|---|---|
| Waiting room | Waiting room is per session, not per agent (20:15). Then **Q20**: remove the 5 s previous-execution wait; "remove the additional queue and use the normal message queue". |
| Queue model | One FIFO queue per session. While a turn runs, take queued messages at the next step boundary, all at once. When a turn ends with messages queued, a new turn starts at once with all of them. No timer, no second waiting room. All queued messages go to the model at once, **but as separate messages**; each is shown as its own message in the chat. |
| Who fills it | The person and the parent session. Parent messages carry the same authority as the person's. |
| Inbox refinement | Keep a per-session **inbox** for messages from child sessions: status reports, finals, questions. Normally **no authority**. An agent-to-agent inbox for non-children is a later feature. |
| Main and worker | Main agents: one ongoing **default session per workspace**; events and tasks for a main agent go into that session's queue. Worker agents: no default session; cloneable; each event or task for a worker starts a **new** worker session. |
| Q24 | Write this ADR. |
| Q25 | Extra parallel chats with a main agent are allowed. |
| Q26 | Channels postponed: separate ADR later, tracked in issue #1206. |
| Q27 | The main/worker distinction already exists (C12). |
| Q28 | Reload the agent list on WebSocket connect and reconnect. The ledger entry records that it does not exist today (only the `agent_created` frame, window focus and cross-tab refresh). |
| Address book key | (agent, workspace): one default session per agent per workspace. |
| Stop | The Stop table of 2026-10-06 (summarised in D6). |

## Decision

### D1 — Session core: only the session id keys a conversation

Every runtime structure about one conversation is keyed by the **session id** and by nothing else. No agent-level key. No chat-id key. No routing key.

| Structure | Today's key (evidence) | Key under D1 |
|---|---|---|
| Steering queue | route key (`steering.go::enqueueSteeringFromMessage`) | session id |
| Turn and turn registry | `ts.sessionKey` (`turn.go::registerTurnIfAbsent`) | session id |
| Session worker / loop | route key plus session suffix (`session_worker.go::dispatchSessionWorker`, `loop_inbound.go::resolveSteeringTarget`) | session id |
| Admission (one turn at a time) | route key (`ordinary_execution_admission.go`), session id after U0 | session id |
| History window, recall span, continue guard | routing key (audit O3; `pkg/agent/loop_window.go`, `Sessions.GetHistory(sessionKey)`) | session id |
| Stop and steering | session id already (audit, "correctly keyed") | unchanged |
| Inbox | owner key plus child session id (`delegate.go::DelegateInboxStore`) | parent session id, child session id |
| Wakes and debounce | channel plus chat id (`async_notifier.go::wakeWindows`) | session id |
| Delivery stamps (turn id, message id) | connection chat id (`webchat_channel.go::streamed`, `::turnIdentity`) | session id |
| Cancel latches | `"t:"+ts.sessionKey` and fallbacks (`cancel_prearm.go`) | `"t:"+session id` only |

The routing key survives only as a **display and routing input** to the address book (D2), never as a state key. A session has one active agent (`UnifiedMeta.ActiveAgentID`), so the agent is a property of the session, not part of its key.

### D2 — Agent address book: (agent, workspace) resolves to a session id before any queue

A small function, the **address book**, runs at the entry of the engine, before a message touches any queue or worker. Its input is the sender intent and the address. Its output is a session id.

| Message is | Address book returns | Evidence or basis |
|---|---|---|
| An explicit chat (web chat with a session id, a person opening a chat) | That chat's own session id | Founder (explicit chat goes to its own session); `loop_inbound.go::agentSessionKey` already uses `msg.SessionID` |
| An event or task for a **main** agent | The agent's **default session** in that workspace (its heavy work may run in a fork, D8) | Founder (main/worker, address book); D8 is a founder proposal, details open |
| An event or task for a **worker** agent (`config.AgentTypeWorker`) | A **new worker session** (a clone) | Founder; already true for tasks today, `task_executor.go::createTaskSessionSync`, and for delegation, `pkg/agent/steer_launcher.go::Launch` |
| An extra parallel chat with a main agent | A new chat session beside the default | Founder Q25 |

"Event" in this ADR means any input that is not typed by a person: a heartbeat fire, a schedule fire, a task trigger or task dispatch, a plan step. The address book never routes a channel message (D7).

**D2.1 — The default session generalises the heartbeat session (Proposed, architect).** The heartbeat session already is the right object: one standing session per (workspace, agent member), stamped with workspace and agent, stored in the workspace's `member_configs`, pinned in the Session panel and protected from deletion (C8). The default session is that object with the heartbeat taken out of its definition.

| Aspect | Heartbeat session today | Default session (proposed) |
|---|---|---|
| Who gets one | Agents whose heartbeat is enabled | Every **main** agent on the workspace team (never a worker; C12) |
| Session type | `heartbeat` | `default` (replaces `heartbeat` in the type enum) |
| Where the id lives | `member_configs[agent].heartbeat.session_id` | `member_configs[agent].default_session_id` (read-only on the wire); `heartbeat.session_id` is removed |
| Created | Eagerly when a heartbeat is enabled | By the address book's get-or-create. The workspace PUT handler calls it for each new main team member, so the session appears in the UI at once. Any other path calls it lazily. One function creates it; a lock per (workspace, agent) makes it idempotent |
| Who writes into it | Heartbeat fires only | Heartbeat fires, schedule fires, tasks, events, and a person who opens it |
| Protected from delete | While heartbeat is enabled | While the agent is on the workspace team |
| Heartbeat disabled | Session is deleted (`rest_workspaces.go` disable path) | Session stays; only the heartbeat cron job goes away |
| Schedule `session_mode` | `isolated`, `continue`, `main` (C9) | Events for a main agent go to the default session (see U4 and the risk R3); a main agent's `sched-main-<agent>` session is replaced by the workspace default session; with no workspace the key is `(agent, "")`, one default session per agent outside any workspace |

**D2.2 — Migration: none (greenfield).** The founder ruled on 2026-09-15 that there is no upgrade path, no backfill, and that upgrade-only code and tests are dropped (greenfield, no upgrade path). So:

- `heartbeat.session_id` and the `heartbeat` session type are deleted outright. No shim, no alias, no "deprecated" comment (founder 2026-09-21, delete superseded code).
- An existing dev home with `heartbeat`-typed sessions is not converted. Fresh installs are the verified path. The implementing unit must prove that a fresh home creates the default session and must not add a conversion.
- The contract change (the session `type` enum in `contracts/components/schemas/Session.yaml` and `SessionLifecycleRecord.yaml`, and its copy in `contracts/openapi.yaml`; `WorkspaceMemberConfig.yaml`; `WorkspaceMemberHeartbeat.yaml`; the `protected` description in `Session.yaml`) follows Hard Constraint 8. The task `surface: heartbeat` value in `Task.yaml` is a different field and is not touched here (**Inferred**: not traced in code). The order is: the schema first, then `scripts/gen-contracts.sh`, then code. Contract shapes are decided here; backend-lead edits the specs.

### D3 — Two queues per session: steering (authority) and inbox (none)

| | Steering queue | Inbox |
|---|---|---|
| Carries | Instructions | Reports from child sessions: `progress`, `checkpoint`, `artifact`, `blocker`, `question`, `handback` (founder's "final" is `handback` with mode `final`; mode `pause` is the other) |
| Authority | Yes. Parent messages carry the same authority as a person's | **None**, normally. Text is untrusted (C6) |
| Order | FIFO | Per child, arrival order (existing durable store) |
| Fed by | The person typing in **this** session (web); the **parent** session (delegate steer or redirect); events and tasks addressed to this session | A child session, through `message_parent` only |
| Existing code | `steering.go::steeringQueue`, to be keyed by session id | `session.MessageInboxStore` through `DelegateInboxStore` |

Consequences of the split:

- A person cannot message a child from the parent's chat. The person opens the child's chat; there the person is that session's steering source (C14).
- A hand-back wake stops being a steering-queue item with a `wake` pointer (C7). It becomes "an inbox item arrived" (see D4 and the open questions).
- An agent-to-agent inbox between non-related agents is **out of scope** (D7).

### D4 — Turn rule: one turn at a time, all waiting steering messages at each step boundary

1. **One turn at a time per session.** The session's own loop is the serialiser: take what is waiting, run a turn, settle it, then look again. A new message cannot start a second turn, so admission never refuses for "early". (This follows from D1 and D5.)
2. **At every step boundary** (after a model response or after a tool call) the turn takes **all** waiting steering messages at once. Each one is injected as **its own** message and shown **separately** in the chat. Evidence for the mechanism: the poll already exists and already makes one message per item (C5). The change is the mode: the `one-at-a-time` default and the `steering_mode` setting go away; "all" is the only behaviour.
3. **When a turn ends with messages waiting**, a new turn starts at once and takes all of them. No timer.
4. **Inbox items (Proposed, architect; open question Q1).**
   - Inside a running turn, an inbox item enters at a step boundary, **marked as a report** (who sent it, which kind), with no authority, never as a person's message.
   - An inbox item arriving while the session is **idle** starts a turn, as today's hand-back wake does (C7), so a finished helper's `handback` is not left unread.
5. **Queue bound (Proposed, architect).** Keep the existing cap, `steering.go::MaxQueueSize` (200). Hitting it is a flood limit, not "early", and the refusal is shown to the sender. The 8-slot worker inbox and its drop message are deleted with D5.
6. **After a Stop (open question Q5).** A turn ended by Stop must not restart at once just because messages wait; that would make Stop look broken. Proposed: waiting messages stay queued, shown as held, until a person resumes the session.

### D5 — No waiting room

Deleted outright, with their tests:

| Deleted | Evidence |
|---|---|
| The 5 s previous-execution wait | `ordinary_execution_admission.go::previousExecutionSettleBudget`, `::awaitPreviousOrdinaryExecution` |
| The "previous execution pending" refusal and its user text | `ErrPreviousExecutionPending`, `translate_error.go::previousReplyStillFinishingMessage` (both added by U0; U0 is a bridge, D5 removes them) |
| The "execution already registered" refusal for a second admission | `ordinary_execution_admission.go::prepareOrdinarySessionExecution` |
| The worker's separate inbox channel and its "could not be queued" drop | `session_worker.go::workerInboxCap`, `sessionWorker.inbox`, `::enqueue` |
| The `steering_mode` setting | `pkg/config/config.go` (`SteeringMode`), `pkg/config/defaults.go` |

Nothing is refused for being early. It queues (founder, Q20).

### D6 — Stop rules unchanged

The founder's Stop decision of 2026-10-06 stands as written and this ADR does not change it. Summary:

| Action | Behaviour |
|---|---|
| Stop click 1, Esc 1, `/stop` | Stops **this** session's current turn only; opens a 3 s window |
| Stop click 2 or Esc 2 within 3 s | Stop all: this session and its whole helper tree |
| `/cancel` | Stop all at once |
| Separate "Stop all" button | Removed |
| `/stop-redirect <instruction>` | Works in any session: stops this session's current turn and continues it with the instruction |
| Channel `/stop`, `/cancel`, `/stop-redirect` | Same meaning |
| Agent `delegate` stop and `stop_all` | Unchanged (one helper's turn; its tree) |
| Plain Stop leaves background shells; Stop all and `/cancel` kill them | Unchanged |

The only interaction with this ADR is D4.6. Stop is already session-id keyed (audit, "correctly keyed": `StopSession`, `StopDelegatedTree`).

### D7 — Out of scope

| Out of scope | Where it goes |
|---|---|
| Channel messages (per-person session or default session) | A separate ADR, issue #1206 (founder Q26) |
| An agent-to-agent inbox outside the parent and child relation | A later feature (founder, inbox refinement) |
| Per-turn publication, context limits, goal and plan semantics | Their own ADRs; D2 only touches where a task's goal lives (D8) |

### D8 — Fork per event or task (founder proposal, details open)

**Status of this section: founder proposal, details open.** The idea is the founder's, passed on by team-lead on 2026-10-06. The direction is recorded; the details below are the architect's analysis and proposals and are **not** decided. Everything in D1 to D7 holds whether or not D8 is accepted.

**The idea.** An event or task addressed to a **main** agent's default session is not worked in the default session itself. It is delegated automatically to a sub-session that is a **fork** of the default session: the same agent, a child of the default session. The heavy work and the tool calls run in the fork. The result is presented back in the default session. Several events and tasks can then run in parallel, while the default session stays light: coordination, memory and results only.

**How it fits the machinery that exists.**

| Piece | What exists today | Use in D8 |
|---|---|---|
| Starting a child | `pkg/steer/types.go::LaunchRequest` (`SteeringSessionID`, `TargetAgentID`, `Task` as the first user message, workspace and owner inherited from the steering session); `pkg/agent/steer_launcher.go::Launch` | The address book calls the same launcher. Parent = the default session; target agent = the same agent |
| Delegating to your own agent | `delegate` with no agent id is "self-delegation" (`pkg/tools/delegate_run.go::requestedSkillNotFoundResult` names that target) | A fork needs no new target kind |
| Child is a session | A helper is an ordinary session steered by another session ([A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md)) | The fork is a helper session of the default session |
| Reporting up | `message_parent` writes to the parent's inbox; the hand-back wake (`pkg/tools/message_parent.go`, C6, C7) | The fork reports progress, blockers and its `handback` the same way |
| Stop | Stop and Stop all through `StopSession` and `StopDelegatedTree` along the durable edge (D6) | Unchanged |
| Worker type | `config.AgentTypeWorker` is a **different** agent profile used as a clone (C12) | Not used for a fork: the fork runs the main agent's own profile |

**What is new.**

1. A **fork of the same main agent**, rather than a worker agent. Self-delegation exists, but only when the agent chooses it in a turn. A fork is a child of the default session running the **same** profile, soul and tools.
2. **Automatic delegation by the address book.** Today an agent decides to delegate. Under D8 the address book makes the decision for every event or task that is addressed to a main agent's default session (subject to Q4: which events fork).

**Analysis and proposals (architect).**

| Question | Analysis | Proposal |
|---|---|---|
| **Context the fork receives** | A delegated child today starts with only the task text as its first message (`LaunchRequest.Task`) and its own empty history. The same agent profile gives it the same soul and agent memory. Full history costs tokens for every event and carries every earlier event's untrusted content into each fork. A summary needs a new summariser | **Task only**, plus a short header written by the address book (source, id, workspace, time). Anything the fork needs from the default session it asks the parent for with `message_parent` (question). Open question Q3 |
| **Does a report start a turn in the default session?** | The inbox rule of D4.4 decides. Idle default session: a `handback` starts a turn (Q1, proposal A) | Yes when idle, same as any helper. Inside a turn: at the next step boundary, as a report |
| **Ordering when several forks finish** | Inbox items keep arrival order per child (D3). If several arrive while the default session is idle or in a turn, D4 takes **all** waiting items at one boundary, each as its own marked report | No new ordering rule. Results are presented in arrival order, one message each. The default session sees them together in one turn |
| **Failure** | A fork that fails ends in its own failed state and the parent is told through the existing notice (control-plane ADR, "Parents see a stop notice, not a failure"; amendment D-E) | The default session receives the failure as a report. It never fails the default session |
| **Stop** | Per the founder's Stop table (D6): Stop on the default session ends only its current turn; **forks keep running**. Stop all and `/cancel` stop the default session and its whole tree of forks (existing `StopDelegatedTree`). Stop inside a fork stops that fork only | No new stop path. The UI must show that forks are still running after a plain Stop (founder's own table makes this visible; wording is a spec item) |
| **Limit on parallel forks** | A global cap exists: `config.PerformanceConfig::EffectiveMaxParallelAgents` sizes the task dispatch semaphore (`pkg/agent/task_executor.go::TryAcquireDispatchSema`) and the admission soft cap (`pkg/agent/admission.go::AdmissionController`). Delegation depth has its own limit (`pkg/agent/delegation_depth.go::resolveEffectiveDelegationDepth`) | Use the **existing global cap**; no new knob. A fork never forks (depth 1 from the default session). A task over the cap stays queued as today (**Inferred**: `CheckQueuedTasks` re-picks queued tasks). An event with no durable record over the cap must queue visibly, never be dropped (risk R9) |
| **Cost** | Every forked event is one more model session with its own tool calls. The default session itself stays small, so its per-turn cost falls | Accept: the cost is the work itself, no longer inflated by a long default history. Report fork count in the existing usage views (spec item) |
| **Light events** | A heartbeat that only checks would pay for a whole fork it does not need | Heartbeat and plain events run in the default session directly; the agent may use `delegate` itself for the heavy part. **Tasks and schedule fires fork.** Open question Q4 |
| **Memory and recap in the default session** | The session-end recap is skipped for delegate-type sessions (`pkg/agent/session_end.go`, the `SessionTypeDelegate` gate), so a fork's work is **not** recapped. The default session's history would hold only coordination and results | The fork's `handback` must carry the durable facts, and the fork writes memory through its own tools. The default session's recap then describes what it coordinated. Whether a never-ending default session keeps working with compaction stays **Unknown** (R4); D8 reduces its growth |

**Effect on the rest of this ADR if D8 is accepted.** D2's row for "an event or task for a main agent" reads "forks from the default session (D8)". Tasks again get their own session (the fork), so each task keeps its own goal and the one-goal-per-owner rule (C10, C11) is no longer a problem. If D8 is **not** accepted, the task and goal conflict returns (risk R2) and a task for a main agent keeps its own task session until a later decision.

## The target picture

```
  person (web chat id)   parent session (delegate)   event / task / heartbeat
          |                        |                        |
          v                        v                        v
   +-----------------------------------------------------------------+
   |  ADDRESS BOOK   (agent, workspace, intent)  ->  session id      |
   |   explicit chat -> its own session                              |
   |   main agent    -> default session of (agent, workspace)        |
   |   worker agent  -> NEW worker session (clone)                    |
   +-----------------------------------------------------------------+
                              | session id
                              v
   +------------------------ one SESSION ---------------------------+
   |  steering queue (FIFO, authority)  <- person, parent, events    |
   |  inbox (no authority)              <- child reports            |
   |  one turn at a time; at each step boundary take ALL steering,   |
   |  each as its own message; turn ends with waiting -> new turn    |
   |  history window, registry, stop, wakes, stamps: keyed by id     |
   +-----------------------------------------------------------------+
```

## Consequences

### Positive

- The audit's whole class of bug (two chats sharing a worker, a queue, a history window, a delivery stamp) cannot occur, because there is no other key left to share (D1; audit A1 to A6, O1 to O8).
- One queue and one turn rule replace three waiting places and a timer (C3, D5). Less code, fewer states.
- Events and tasks for a main agent land where the person can see and steer them: the default session.
- The heartbeat session machinery (eager create, pin, delete guard, rollback) is reused rather than duplicated (C8, D2.1).

### Negative

- It is a broad change: about 99 production references to a `.sessionKey` field in 32 files, 92 production references to `activeTurnStates` in 19 files (48 test files), 26 to `sessionWorkers` in 3 files (counted with `grep` on `pkg/**/*.go`, tests excluded). Most of the cost is in tests that assert today's keys.
- A contract change (session type, member config) reaches the generated Go and TypeScript types and the Session panel.
- A default session grows without end. It needs the existing context compaction to keep working over very long histories (not verified; see R4).

### Neutral

- The agent still decides **what** to do; the address book only decides **where** the input lands.
- Worker sessions, delegation and task sessions that are already new sessions keep working as they do today.

### What changes for users

| For the user | Change |
|---|---|
| Messages typed while the agent works | All waiting messages go to the model at the next step, together, but each shown as its own message |
| A message sent a moment after a reply | Never refused with "previous reply still finishing"; it queues |
| History | Per session, never shared between two chats of the same agent |
| The default session | Visible and pinned for each main agent in each workspace; heartbeat, schedules, tasks and events show up there |
| Stop | Same table as today (D6); messages waiting at Stop are held (Q5) |
| Agent picker | Reloads on WebSocket connect and reconnect (founder Q28; separate small unit, see U6) |

### Implementation units, in order

Each unit is shippable alone and keeps CI green. Counts are `grep` counts in `pkg/` (production files; tests excluded) and are **Verified** as counts, **Inferred** as a measure of risk.

| Unit | What | Blast radius | Evidence |
|---|---|---|---|
| **U0** | PR #1204 (open): per-session admission, and the per-session route key whenever `msg.SessionID` is set; refusal text. Bridge only; D5 later deletes its refusal. | 9 files; `pkg/agent/ordinary_execution_admission.go`, `loop_inbound.go`, `translate_error.go`, `session_end.go`, tests | PR diff `origin/release/v0.1.1...origin/work/engine-admission-per-session-20261006` |
| **U1** | **Address book and session id on arrival.** New resolver; the default session replaces the heartbeat session (type `default`, `default_session_id`, `protected` while on the team); every inbound message carries its session id before any queue. Contract change first. | `pkg/session/unified.go`; `pkg/workspace/member_config.go`; `pkg/gateway/rest_workspaces.go`, `rest_sessions.go`, `heartbeat_schedule.go`, `schedules.go`; contracts (`Session.yaml` `type` and `protected`, `SessionLifecycleRecord.yaml`, `WorkspaceMemberConfig.yaml`, `WorkspaceMemberHeartbeat.yaml`); generated Go and TS; the Session panel pin. 26 production references to heartbeat types in 8 files | `grep NewHeartbeatSession|SessionTypeHeartbeat|OriginKindHeartbeat` |
| **U2** | **Worker scope per session id.** `sessionWorkers` keyed by session id; delete the `":"+sid` suffix match and the probe worker for channel hand-backs (A6). | `pkg/agent/session_worker.go`, `loop.go`, `loop_inbound.go`. 26 references in 3 files; `resolveSteeringTarget` 11 references in 4 files | `grep sessionWorkers`, `grep resolveSteeringTarget` |
| **U3** | **One queue and all-at-once.** Merge the worker inbox into the steering queue; steering keyed by session id; "all" only; remove `steering_mode`; a new turn starts at once with everything waiting; the inbox separated from the steering queue (hand-back wake becomes an inbox arrival). | `pkg/agent/steering.go`, `session_worker.go`, `loop_run_turn*.go`, `async_notifier.go`; `pkg/config` (`SteeringMode`); WebSocket per-message status; `docs/internal/architecture/steering.md`. 38 references to `enqueueSteeringMessage` in 7 files; 14 dequeue references in 5 files | `grep enqueueSteeringMessage`, `grep dequeueSteeringMessagesForScope` |
| **U4** | **Scheduled, heartbeat, task and event input into the default session's queue, and remove the waiting room.** `pickSession` and the task executor ask the address book; delete D5's list. | `pkg/gateway/schedules.go`, `pkg/agent/task_executor*.go`, `ordinary_execution_admission.go`, `translate_error.go`; `ScheduleCreate`/`ScheduleUpdate` `session_mode` in the contract; 25 references in 4 files for the waiting-room symbols | `grep awaitPreviousOrdinaryExecution|activeScopes|attachExecution` |
| **U5** | **Turn registry, history window, cancel latches and delivery stamps by session id.** The composite key becomes the plain session id. Heaviest unit; may split into U5a (registry and cancel) and U5b (history and stamps). | `activeTurnStates` 92 references in 19 files (48 test files); `Sessions.GetHistory` 14 references in 11 files (55 test files); `cancel_prearm.go`; `webchat_channel.go` | `grep activeTurnStates`, `grep 'GetHistory('` |
| **U6** | **Agent list reload on WebSocket connect and reconnect** (founder Q28). Independent; may ship any time. | `src/` agent list store; no engine change | founder Q28 |
| **U7** | **Fork per event or task (D8, only if accepted).** The address book launches a fork through `SteerLauncher.Launch`; results flow through the inbox. Needs U1, U3, U4. | `pkg/agent/steer_launcher.go`, `task_executor*.go`, `pkg/gateway/schedules.go`; the Session panel (forks listed under the default session); usage views | `pkg/steer/types.go::LaunchRequest`; D8 |

Order reason: after U1 every message has a session id, so the composite route key is a pure function of (agent, session id). U2 to U4 can then move queues and workers to the session id without a second key to disagree with. U5 last removes the now-redundant agent part from the registry key, which is the largest test-churn change.

### Risks

| # | Risk | Evidence | Mitigation |
|---|---|---|---|
| R1 | A session can have more than one agent over time (handoff, `AgentIDs`, `ActiveAgentID`). "The key is the session id" must still pick the right agent for the turn. | `pkg/session/unified.go::NewHeartbeatSession` stamps `AgentIDs` and `ActiveAgentID`; audit O6 (handoff pin). **Inferred** for the exact handoff behaviour | U1 spec must define "the agent of a session" as `ActiveAgentID`; test a handoff mid-session |
| R2 | The default session breaks a rule that was easy to keep per task: one goal per owner (C10, C11). Two tasks queued to one default session would share one session goal. | `pkg/goal/predicate.go::errMultipleActiveGoalsForOwner`; `task_executor.go::activateTaskGoal` | Decided by D8 (each task forks, so it keeps its own goal); if D8 is rejected, a task for a main agent keeps its own task session |
| R3 | Schedule modes `isolated` and `continue` were designed for per-run sessions. Sending events to the default session changes what `session_mode` means and is a contract change. | `pkg/gateway/schedules.go::pickSession`; `ScheduleCreate.yaml::session_mode` | U4 spec decides whether `session_mode` is removed (greenfield) or narrowed |
| R4 | A never-ending default session grows its history and its context cost. | Same engine as a long chat. **Unknown** whether compaction covers a session that never ends | U1 spec adds a long-run test; no new compaction here |
| R5 | Lazy creation can race: two events for the same (agent, workspace) at once. | The existing eager path takes a lock for the whole handler (`rest_workspaces.go`, comment at the PUT handler) | Per (workspace, agent) lock in the address book; a concurrency test |
| R6 | Removing the 5 s wait removes a guard that hid a real race: a new turn admitted while the old turn's tail still runs. | `ordinary_execution_admission.go::awaitPreviousOrdinaryExecution` doc | The session loop serialises (D4.1): the next turn starts only after the previous one settled. Test: turn B never starts before A's disposition settles |
| R7 | Hand-back wake semantics move from the steering queue to the inbox (C7). The "poll or wake, never both, exactly once" rule must survive. | [The sub-agent control plane](./ADR-20260928-sub-agent-control-plane.md), amendment D-E | U3 keeps one durable message identity; test exactly-once across the move |
| R8 | A held-after-Stop queue (D4.6) is easy to forget and looks like lost messages. | Founder Stop table; ADR-093 D4 rule that only a person resumes a stopped session ([An open conversation must keep the ability to delegate](./ADR-093-open-conversation-must-keep-delegation.md)) | Show held messages in the chat; decided in Q5 |
| R9 | D8 only: an event over the parallel cap is dropped instead of queued, and a fork that never reports leaves the default session waiting on nothing. | `task_executor.go::TryAcquireDispatchSema` returns a bool (refusal path); **Inferred** for events with no durable record | U7 spec: every fork-bound event is recorded before launch; a fork without a report ends in a visible failed or stopped state |
| R10 | D8 only: the default session shows "idle" while forks work; a plain Stop leaves them running and looks like it did nothing. | Founder Stop table (D6); `StopDelegatedTree` | Show running forks in the default session; the double-Stop already stops them |

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep the agent-level keys and fix each hit by hand (what U0 did for admission) | The audit found 16 hits with one cause. Each fix leaves the next key in place. U0 shows the cost: one hit moved admission to the session id and exposed the registry as the next mismatch. |
| Keep a short wait but make it per session | Founder Q20: no timer, no second waiting room. A timer is a guess; the loop's own ordering is exact. |
| A new "default session" concept beside the heartbeat session | Two standing sessions per (agent, workspace) with the same shape. Founder 2026-10-06, simplest design first: reuse the existing path. |
| One queue that also holds child reports | Child reports carry no authority; mixing them with person and parent instructions invites prompt-injection through a report. Founder inbox refinement rejects it (C6 marks the text untrusted). |
| Tasks always get their own new session (today, C10) | Contradicts the founder's rule that tasks for a main agent go to the default session. D8 (a fork per task) removes the goal conflict; this is the fallback if D8 is rejected. |
| Work every event in the default session itself (no fork) | One long default session does all the tool calls, runs one thing at a time and grows without end. D8 is the founder's answer; kept as the fallback if D8 is rejected |
| Fork with a full copy of the default session history | Pays for the whole history on every event and copies untrusted content between events (D8 analysis) |
| Per-person session for channel messages now | Postponed by founder Q26; tracked in issue #1206. |

## Affected components

| Area | Components |
|---|---|
| Engine | `pkg/agent/turn.go`, `ordinary_execution_admission.go`, `session_worker.go`, `steering.go`, `loop_inbound.go`, `loop_run_turn*.go`, `loop_window.go`, `cancel_prearm.go`, `async_notifier.go`, `task_executor*.go`, `translate_error.go` |
| Sessions and workspaces | `pkg/session/unified.go`, `pkg/workspace/member_config.go` |
| Gateway | `pkg/gateway/rest_workspaces.go`, `rest_sessions.go`, `heartbeat_schedule.go`, `schedules.go`, `webchat_channel.go` |
| Config | `pkg/config/config.go`, `defaults.go` (`SteeringMode`) |
| Contracts | `Session.yaml` (`type`, `protected`), `SessionLifecycleRecord.yaml`, `WorkspaceMemberConfig.yaml`, `WorkspaceMemberHeartbeat.yaml`, `ScheduleCreate.yaml`, `ScheduleUpdate.yaml`, the session-type copy in `openapi.yaml`; generated Go and TS |
| SPA | Session panel (default session pinned), separate display of each queued message, agent list reload (U6) |
| Docs | `docs/internal/architecture/steering.md` and the user docs on delegation and sessions |

## Evidence and verification

Read in this task: the founder's 2026-10-06 ledger entries; the audit summary; the PR #1204 diff; the two control-plane ADRs (headers and decision lists); and the code at `origin/release/v0.1.1` @ `07679737c` for every `file::symbol` in the table C1 to C14. Not read in full: the whole of ADR-091 and ADR-20260928 (cited for their amendment notes and decision titles). The PR #1204 branch state is `OPEN`. For D8 I also read `pkg/steer/types.go::LaunchRequest`, `pkg/tools/delegate_run.go` (self-delegation), `pkg/config/config_defaults_apply.go::EffectiveMaxParallelAgents` and the recap gate in `pkg/agent/session_end.go`. Items R1 and R9 are **Inferred**. Item R4 is **Unknown**. D8's details are a founder proposal, not a decision.

## Open questions for the founder

**Q1 — Report from a helper (inbox item): when does it reach the agent?** (A / B / C). A: while a turn runs, inject it at the next step boundary marked as a report with no authority; while the session is idle, start a turn (today's hand-back wake, C7). B: while a turn runs, hold it until the turn ends; while idle, start a turn. C: never start a turn; the agent reads it only when it next runs. Recommendation: **A**. It keeps the parent informed at once, never confuses a report with an instruction, and leaves no finished helper's result unread. D8 depends on the idle case.

**Q2 — The default session is the heartbeat session, generalised** (A / B / C). A: one default session per main agent and workspace, type `default`, created by the address book (eagerly on joining the team, lazily otherwise), protected while the agent is on the team, kept when the heartbeat is turned off; with no workspace, one default session per agent. B: same, but only for agents with a heartbeat or a task. C: keep `heartbeat` as a separate type and add a second `default` session. Recommendation: **A**. B makes the default session appear and disappear. C is two standing sessions with the same job.

**Q3 — D8: what does a fork receive?** (A / B / C). A: only the task plus a short header (source, id, workspace). B: the task plus a summary of the default session. C: the full default-session history. Recommendation: **A**. It matches how a delegated child starts today, costs the least, and keeps one event's untrusted content out of another event's fork. B needs a new summariser; C pays for the whole history on every event.

**Q4 — D8: which events fork?** (A / B / C). A: tasks and schedule fires fork; heartbeat and plain events run in the default session directly. B: everything forks, including a heartbeat that only checks. C: nothing forks automatically; the agent uses `delegate` when it decides to. Recommendation: **A**. B pays a full session for a check that may do nothing. C does not give the parallelism you asked for. The agent can still delegate the heavy part of a heartbeat itself.

**Q5 — Messages waiting when Stop lands** (A / B / C). A: they stay queued, shown as held, and run with the next message from a person (consistent with "only a person resumes a stopped session"). B: they are dropped and shown as "not sent". C: the next turn starts at once with them, which makes Stop look broken. With D8, plain Stop also leaves forks running; the double-Stop already stops them. Recommendation: **A**. B loses what the person typed. C defeats Stop.

**Q6 — Who steers a **worker** session started by an event, not by a delegation?** (A / B). It has no parent session (C13). A: it is a root session with no parent; the person steers and stops it from its own chat; it ends with its own outcome (as a direct task run does today). B: the workspace's default session of the agent that owns the trigger becomes its parent. Recommendation: **A**. B invents a parent that did not ask for the work. (A main agent's fork, by contrast, always has the default session as its parent, D8.)
