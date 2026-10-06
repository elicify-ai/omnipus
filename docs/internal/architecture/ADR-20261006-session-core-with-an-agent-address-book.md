# ADR-20261006 — Session core with an agent address book: one queue per session, default session per agent and workspace

- **Status:** Draft — founder decisions recorded; Astra review pending. Design only; no code changes in this ADR. Open questions are listed in "Still open" at the end.
- **Date:** 2026-10-06 (updated the same day to record the founder decisions made after the first draft).
- **Deciders:** Daniel Piatkowski (founder). Every founder decision of 2026-10-06 in `coordination/CONTINUATION-20261005.md` is recorded below and marked **Founder decision**. Opus defaults that no decision touches are marked **default, founder may overrule**. Everything else is **Proposed (architect)**.
- **Author:** architect.
- **ID:** minted with `scripts/new-adr-id.sh` (date-and-title scheme, see `scripts/check-adr-id-scheme.sh`).
- **Relates to (does not reopen):** [A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md) (a helper is an ordinary session); [The sub-agent control plane: stop, redirect, receipts, owner-question relay, restart resume](./ADR-20260928-sub-agent-control-plane.md) (the Stop model); [Steering commands: no person question](./ADR-20261004-steering-commands-no-person-question.md) (messages are steering); [UI-independent turns and session-bound webchat streaming](./ADR-082-ui-independent-turns-and-session-bound-streaming.md) (a turn never depends on a UI connection); [Workspace-scoped heartbeat and global memory UI](./ADR-027-workspace-scoped-heartbeat-and-global-memory-ui.md) (the heartbeat session this ADR generalises).
- **Open pull request this builds on:** PR #1204, branch `work/engine-admission-per-session-20261006` (state at writing: open, not merged). Called **U0** below.

## Plain-English summary

Today the engine decides "which queue does this message go into?" from several different keys. Some use the session id. Some use the agent. Some use the chat id of the connection. Two chats with the same agent can end up sharing a queue, a worker or a history window. That is the cause of the bugs in the audit below.

This ADR makes one rule: **everything about a conversation is keyed by the session id, and nothing else.** To make that possible, a small **address book** turns "agent plus workspace" into a session id **before** a message enters any queue.

The founder decisions of 2026-10-06 shape the rest:

| Topic | What the ADR now says |
|---|---|
| Default session | A main agent always has one standing **default session** per workspace. It is the heartbeat session, generalised. It is always protected while the agent is on the team. |
| Workers | A worker agent has no default session. Each task or event starts a new worker session. |
| Self-delegation | Every agent may delegate to itself. This is the existing `delegate` tool, not a fork. |
| Main agent behaviour | A main agent keeps all the tools its role allows, but is told to **prioritise delegation**. This holds in every chat with a main agent. |
| Events and tasks | They keep running as their own session, as today. New: the run is **registered in the main agent's default session** as if that session had started it. The default session can see it, steer it and receives its result. |
| Queues | One steering queue per session (authority) and one inbox (no authority). All waiting messages are taken at once, each as its own message. The waiting room is gone. |
| Channels | For now, every channel message goes to the agent's default session. No per-person session yet (issue #1206). |
| Session end | A session ends after 30 idle minutes and a recap is written. Typing again resumes the same session. |

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
| C13 | A task run dispatched directly (not delegated) has no parent session, and `message_parent` says so. **Under this ADR this changes for a main agent**: its event and task runs are registered as children of the default session (D8). | `pkg/tools/message_parent.go` (the `ToolRunningTaskID` branch) |
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
| Q26 | Channels were postponed (separate ADR later). **Superseded by the 22:50 decision below.** Per-person channel sessions stay a later idea, issue #1206. |
| Q27 | The main/worker distinction already exists (C12). |
| Q28 | Reload the agent list on WebSocket connect and reconnect. The ledger entry records that it does not exist today (only the `agent_created` frame, window focus and cross-tab refresh). |
| Address book key | (agent, workspace): one default session per agent per workspace. |
| Stop | The Stop table of 2026-10-06 (summarised in D6). |
| Self-delegation (22:09, round 1 Q-ADR-4) | The existing self-delegation is the default for all hands-on work a main agent does. It is not a new fork mechanism. **Every agent may self-delegate.** |
| Events and tasks (round 1 Q-ADR-1 "mixed", Q-ADR-2) | They keep running as their **own session**, as today. The run is **registered in the main agent's default session** as if that session had started it: injected into its context, monitorable and steerable from it, and its result returns into it. They do not target the main session. |
| Main agent tools (round 1 Q-ADR-2) | The main agent keeps **all** tools its role allows. It must **prioritise** delegation. This is an instruction, not a tool ban. |
| Scope (round 1 Q-ADR-3) | The rule applies to **all chats** with a main agent. |
| Session end (22:38, 22:41, 22:43) | A session ends after `idle_timeout_minutes` (default 30) of inactivity and the recap is written. Auto recap is **on by default**. The default session follows the same rule. Typing again resumes the same session. The unused recap triggers "lazy", "joined" and "explicit" (the `session_close` frame and its handler) are **deleted**; this is in progress on another branch. |
| Channels (22:50) | For now **all channel messages go to the agent's default session** (per agent and workspace). No per-person session yet. |
| Queue model (Q20 and the later clarifications) | One steering queue per session with authority, fed by the person and the parent session. The inbox carries no authority. The waiting room is removed entirely. The internal worker inbox merges into the steering queue. |

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

A small function, the **address book**, runs at the entry of the engine, before a message touches any queue or worker. Its input is the sender intent and the address. Its output is a session id. The address book is keyed by **(agent, workspace)** (**Founder decision**).

| Message is | Address book returns | Evidence or basis |
|---|---|---|
| An explicit chat (web chat with a session id, a person opening a chat) | That chat's own session id | Founder; `loop_inbound.go::agentSessionKey` already uses `msg.SessionID` |
| A channel message for a main agent | The agent's **default session** in that workspace (D10) | **Founder decision** 22:50 |
| An event or task for a **main** agent | The event or task runs as its **own session**, as today. The run is **registered** in the agent's default session (D8) | **Founder decision** (round 1 Q-ADR-1, Q-ADR-2) |
| An event or task for a **worker** agent (`config.AgentTypeWorker`) | A **new worker session** (a clone). A worker has no default session | Founder; already true for tasks today, `task_executor.go::createTaskSessionSync`, and for delegation, `pkg/agent/steer_launcher.go::Launch` |
| An extra parallel chat with a main agent | A new chat session beside the default | Founder Q25 |

"Event" in this ADR means any input that is not typed by a person: a heartbeat fire, a schedule fire, a task trigger or task dispatch, a plan step.

**D2.1 — The default session generalises the heartbeat session (Founder decision; the field details are Proposed, architect).** The heartbeat session is the right object: one standing session per (workspace, agent member), stamped with workspace and agent, stored in the workspace's `member_configs`, pinned in the Session panel and protected from deletion (C8). The default session is that object with the heartbeat taken out of its definition. No new machinery: the same create, protect and release code is reused.

| Aspect | Heartbeat session today | Default session (target) |
|---|---|---|
| Who gets one | Core-team members of a workspace with the heartbeat switched on. Created in `pkg/gateway/rest_workspaces.go` when the heartbeat is enabled (`NewHeartbeatSession`) | **Always** exists for every **main** agent on the workspace team (never a worker; C12) |
| Session type | `heartbeat` | `default` (replaces `heartbeat` in the type enum) |
| Where the id lives | `member_configs[agent].heartbeat.session_id` | `member_configs[agent].default_session_id` (read-only on the wire); `heartbeat.session_id` is removed |
| Created | Eagerly when a heartbeat is enabled | By the address book's get-or-create. The workspace PUT handler calls it for each new main team member, so the session appears in the UI at once. Any other path calls it lazily. One function creates it; a lock per (workspace, agent) makes it idempotent |
| Who writes into it | Heartbeat fires only | Heartbeat fires, channel messages, a person who opens it, and the engine's notices about registered runs (D8) |
| Protected from delete | While the heartbeat is enabled (`pkg/gateway/rest_sessions.go::computeSessionProtected`) | **Always, while the agent is on the workspace team** |
| Released | When the heartbeat is disabled, and when the workspace is deleted | When the agent leaves the team and when the workspace is deleted. Turning the heartbeat off only removes the heartbeat cron job; the session stays |
| Schedule `session_mode` | `isolated`, `continue`, `main` (C9) | See T7 below |

**D2.2 — Migration: none (greenfield).** The founder ruled on 2026-09-15 that there is no upgrade path, no backfill, and that upgrade-only code and tests are dropped (greenfield, no upgrade path). So:

- `heartbeat.session_id` and the `heartbeat` session type are deleted outright. No shim, no alias, no "deprecated" comment (founder 2026-09-21, delete superseded code).
- An existing dev home with `heartbeat`-typed sessions is not converted. Fresh installs are the verified path. The implementing unit must prove that a fresh home creates the default session and must not add a conversion.
- The contract change (the session `type` enum in `contracts/components/schemas/Session.yaml` and `SessionLifecycleRecord.yaml`, and its copy in `contracts/openapi.yaml`; `WorkspaceMemberConfig.yaml`; `WorkspaceMemberHeartbeat.yaml`; the `protected` description in `Session.yaml`) follows Hard Constraint 8. The task `surface: heartbeat` value in `Task.yaml` is a different field and is not touched here (**Inferred**: not traced in code). The order is: the schema first, then `scripts/gen-contracts.sh`, then code. Contract shapes are decided here; backend-lead edits the specs.

**D2.3 — No workspace: My Workspace (default, founder may overrule, T3).** A message with no workspace uses **My Workspace**, the default workspace, not a separate agent-only session. Delegation already falls back this way (`pkg/agent/loop_delegation.go::resolveEffectiveWorkspaceID`), and heartbeat sessions always have a workspace. This replaces the earlier draft's `(agent, "")` key.

**D2.4 — Other defaults on the default session (default, founder may overrule, T4).** The default session cannot be deleted while the agent is on the team. There is no "clear history" for now.

### D3 — Two queues per session: steering (authority) and inbox (none)

| | Steering queue | Inbox |
|---|---|---|
| Carries | Instructions | Reports from child sessions: `progress`, `checkpoint`, `artifact`, `blocker`, `question`, `handback` (founder's "final" is `handback` with mode `final`; mode `pause` is the other) |
| Authority | Yes. Parent messages carry the same authority as a person's | **None**, normally. Text is untrusted (C6) |
| Order | FIFO | Per child, arrival order (existing durable store) |
| Fed by | The person typing in **this** session (web); the **parent** session (delegate steer or redirect); channel messages and heartbeat fires addressed to this session | A child session, through `message_parent`; and the engine's notices about registered runs (D8.3). No authority |
| Existing code | `steering.go::steeringQueue`, to be keyed by session id | `session.MessageInboxStore` through `DelegateInboxStore` |

Consequences of the split:

- A person cannot message a child from the parent's chat. The person opens the child's chat; there the person is that session's steering source (C14).
- A hand-back wake stops being a steering-queue item with a `wake` pointer (C7). It becomes "an inbox item arrived" (see D4 and the open questions).
- An agent-to-agent inbox between non-related agents is **out of scope** (D7).

### D4 — Turn rule: one turn at a time, all waiting steering messages at each step boundary

1. **One turn at a time per session.** The session's own loop is the serialiser: take what is waiting, run a turn, settle it, then look again. A new message cannot start a second turn, so admission never refuses for "early". (This follows from D1 and D5.)
2. **At every step boundary** (after a model response or after a tool call) the turn takes **all** waiting steering messages at once. Each one is injected as **its own** message and shown **separately** in the chat. Evidence for the mechanism: the poll already exists and already makes one message per item (C5). The change is the mode: the `one-at-a-time` default and the `steering_mode` setting go away; "all" is the only behaviour.
3. **When a turn ends with messages waiting**, a new turn starts at once and takes all of them. No timer.
4. **Inbox items (Proposed, architect; open question Q-ADR-7).**
   - Inside a running turn, an inbox item enters at a step boundary, **marked as a report** (who sent it, which kind), with no authority, never as a person's message.
   - Which reports wake an **idle** session is open (Q-ADR-7). The recommendation is that final reports, pauses, questions and blockers wake it, so a finished helper's `handback` is not left unread (C7).
5. **Queue bound (Proposed, architect).** Keep the existing cap, `steering.go::MaxQueueSize` (200). Hitting it is a flood limit, not "early", and the refusal is shown to the sender. The 8-slot worker inbox and its drop message are deleted with D5.
6. **After a Stop (open question Q-ADR-12).** A turn ended by Stop must not restart at once just because messages wait; that would make Stop look broken. Recommended: waiting messages stay queued, shown as held, until a person resumes the session.

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

The interactions with this ADR are D4.6 (held messages) and D8.3 (a Stop all on a default session also reaches the registered runs; risk R10). Stop is already session-id keyed (audit, "correctly keyed": `StopSession`, `StopDelegatedTree`).

### D7 — Out of scope

| Out of scope | Where it goes |
|---|---|
| Per-person sessions for channel messages | Later idea, issue #1206. Until then every channel message goes to the default session (D10) |
| An agent-to-agent inbox outside the parent and child relation | A later feature (founder, inbox refinement) |
| Per-turn publication, context limits, goal and plan semantics | Their own ADRs; D8 only touches where a task's goal lives |

### D8 — Self-delegation, and runs registered in the default session (Founder decision)

This section replaces the earlier "fork per event or task" proposal. The founder ruled on 2026-10-06 (22:09) that this is **not a new fork mechanism**: the existing self-delegation is used.

**D8.1 — Every agent may self-delegate (Founder decision, round 1 Q-ADR-4).** A helper is an ordinary session of the same agent with the same profile, started through `pkg/agent/steer_launcher.go::Launch` and `pkg/steer/types.go::LaunchRequest`, as a child of the session that delegated ([A sub-agent is a session steered by another session](./ADR-091-steered-sessions-replace-subagents.md)).

| Point | Today (verified) | Under this ADR |
|---|---|---|
| Who may target themselves | Only Jim and General Purpose, and only with an explicit self entry in the workspace's delegation settings | **Every agent** |
| Evidence | `pkg/agent/loop_delegation.go::buildDelegationDenyChecker`; `pkg/workspace/delegation.go::PermittedSelfDelegationID`; `DelegationEdge.Validate` | These rules change; this is a **permission change**, not only routing |
| Mechanism (Proposed, architect) | | Always allowed for an agent's own helpers, with no workspace setting. The helper gains no extra power: same agent, same tools. Depth and parallel limits still apply. **Security-lead must review** |
| Self-helper delegating again (default, founder may overrule, T6) | | A self-helper may not self-delegate again. It may still delegate to other agents under the workspace rules and the depth limit (default 3, `pkg/agent/delegation_runtime.go::defaultMaxSubTurnDepth`) |

**D8.2 — A main agent prioritises delegation (Founder decision, round 1 Q-ADR-2 and Q-ADR-3).** The main agent keeps **all** tools its role allows. It is told to prioritise delegation: the default session coordinates, and heavy or hands-on work runs in self-delegated helpers. This is an **instruction in the agent's prompt text, not a tool ban**. It applies to **all** chats with a main agent: the default session and every extra chat (Q25). There is no engine rule and no per-session tool removal, so Hard Constraint 6 ("two layers, no third") is untouched. Because it is an instruction, a model may ignore it; that is the accepted trade of the founder's choice.

**D8.3 — Events and tasks keep their own session and are registered in the default session (Founder decision, round 1 Q-ADR-1 "mixed", Q-ADR-2).** An event or task for a **main** agent does **not** target the default session. It runs straight as its **own session** as today. The only change: the run is **registered in the main agent's default session as if that session had started it**.

| Part | What happens | Basis |
|---|---|---|
| Parent link | The run is recorded as a child of the default session, using the same durable parent link a delegated helper has | Founder. The exact record is a spec item (**Inferred**: the link used by `StopDelegatedTree`) |
| Injection | At the moment the run starts, the default session receives an engine-written notice. It enters the session's context. It carries no authority | Founder. Proposed (architect): the notice travels through the inbox (D3) |
| Monitor and steer | The default session can read the run's reports and steer it, like any child (`delegate` steer, redirect and stop) | Founder; existing delegate tools |
| Result | The run's final result returns into the default session, through the inbox | Founder |
| Own limits and goal | The run keeps the task's own goal and time limits. The task, not the session, owns the goal, so the one-goal-per-owner rule is unaffected (C10, C11) | Default, founder may overrule (T5). Removes the earlier risk R2 |
| Recap | A task or event session is not a `SessionTypeDelegate` session, so its session-end recap runs as today | Inferred from `pkg/agent/session_end.go` (`skipped_delegate_session`) |
| Worker agents | No default session. Each task or event starts a new worker session. Who steers it is open (Q-ADR-14) | Founder; Q-ADR-14 |

**What the default session sees, and how Stop acts on it.**

| Question | Answer under the founder's rules |
|---|---|
| Several runs finish near each other | Results arrive in arrival order, one per run, no merging (default, founder may overrule, T8). D4 takes all waiting items at one boundary, each as its own marked report |
| A run fails | The default session receives the failure as a report. It never fails the default session (control-plane ADR, "Parents see a stop notice, not a failure") |
| Reports while the default session is stopped | Wait until it resumes (default, founder may overrule, T9; control-plane ADR, D1.7) |
| Plain Stop on the default session | Ends only its current turn. Registered runs and helpers keep running (D6) |
| Stop all and `/cancel` on the default session | Stops the default session and its **whole tree**, which now includes registered task and event runs. See risk R10 |

**D8.4 — Schedules (default, founder may overrule, T7).** A schedule fire for a main agent runs as its own session and is registered in the default session (D8.3). The `main` session mode and its `sched-main-<agent>` session are replaced by the default session. `isolated` and `continue` stay as they are. A schedule fire for a worker agent gets a new session. Evidence: `pkg/gateway/schedules.go::pickSession`. This is a contract change to `session_mode` (see U4, risk R3).

**D8.5 — Parallel limit and cost.** The existing global cap is 2,000 when nothing is configured (`pkg/config/config_defaults_apply.go::EffectiveMaxParallelAgents`, `physicalConcurrencySafetyCeiling`). That is a crash guard, not a real limit. Whether to add a smaller limit per default session is open (Q-ADR-9).

### D9 — Session end: idle timeout and recap (ADR requirement, Founder decision)

| Rule | Detail | Evidence |
|---|---|---|
| A session ends after `idle_timeout_minutes` of inactivity | Default 30. The recap is written when it ends | `pkg/config/config.go` (`IdleTimeoutMinutes`), `pkg/config/config_agents.go::GetIdleTimeoutMinutes` |
| Auto recap is on by default | Today it is a setting that must be switched on | `pkg/config/config.go` (`AutoRecapEnabled`); `pkg/agent/session_end.go::CloseSession` |
| The default session follows the same rule | It is not treated differently. Ending is not deleting: the session record stays and stays protected | Founder 22:38 |
| Typing again resumes the same session | Same session id, same history | Founder 22:38 |
| No unused triggers remain | `lazy`, `joined` and `explicit` (the `session_close` frame and its handler) are deleted. `idle` (and the separate boot-time `bootstrap`) remain | `pkg/agent/memory.go` (`TriggerLazy`, `TriggerJoined`, `TriggerExplicit`); `pkg/api/generated/asyncapi_types.gen.go` (`WsFrameTypeSessionClose`). Deletion is in progress on another branch |
| There is no UI way to end a chat | Ending happens only by idle | Founder 22:38 |

This ADR does not do that deletion. It records the target so the units that follow can rely on it.

### D10 — Channels: all channel messages go to the default session (Founder decision, 22:50)

For now, **every channel message goes to the agent's default session**, per agent and workspace. There is no per-person session yet. Per-person sessions are a later idea (issue #1206). This replaces the earlier "channels out of scope" text.

| Consequence | Detail |
|---|---|
| One shared conversation | Different people on a channel share one session and one history |
| Reply routing | A reply must go back to the person and chat that sent the message. With one shared session this needs the channel and chat stamped on each message. How is a spec item (**Unknown**: not traced in code) |
| Which workspace | How a channel message picks its workspace is a spec item (**Unknown**) |
| `dm_scope` | The per-peer routing setting no longer decides the session. Its fate is a spec item (**Inferred**; the setting is named in the audit hit O1) |
| Delivery stamps | D1 keys stamps by session id. Channel and chat identity must still travel with each message, so replies reach the right chat |

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
- Events and tasks for a main agent are visible in, and steerable from, the default session, while still running as their own session.
- Channel messages have one clear home: the default session (D10).
- The heartbeat session machinery (eager create, pin, delete guard, release) is reused rather than duplicated (C8, D2.1).
- Self-delegation uses the existing `delegate` tool and launcher; no fork machinery is built.

### Negative

- It is a broad change: about 99 production references to a `.sessionKey` field in 32 files, 92 production references to `activeTurnStates` in 19 files (48 test files), 26 to `sessionWorkers` in 3 files (counted with `grep` on `pkg/**/*.go`, tests excluded). Most of the cost is in tests that assert today's keys.
- A contract change (session type, member config) reaches the generated Go and TypeScript types and the Session panel.
- The default session lives across resumes, so its history keeps growing. It needs the existing context compaction to keep working over very long histories (not verified; see R4).
- Self-delegation becomes a permission change for every agent, so security-lead must review it (D8.1).
- Channel users share one session until per-person sessions exist (D10).

### Neutral

- The agent still decides **what** to do; the address book only decides **where** the input lands.
- Worker sessions, delegation and task sessions that are already new sessions keep working as they do today.

### What changes for users

| For the user | Change |
|---|---|
| Messages typed while the agent works | All waiting messages go to the model at the next step, together, but each shown as its own message |
| A message sent a moment after a reply | Never refused with "previous reply still finishing"; it queues |
| History | Per session, never shared between two chats of the same agent |
| The default session | Visible and pinned for each main agent in each workspace; heartbeat, channel messages and notices about task and event runs show up there |
| Main agent behaviour | In every chat the agent is told to hand heavy work to its own helpers; it keeps all its tools |
| Channels | Everyone on a channel talks to the same default session |
| Session end | A chat ends after 30 idle minutes and a recap is written; typing again resumes it |
| Stop | Same table as today (D6); messages waiting at Stop are held (Q-ADR-12, recommended) |
| Agent picker | Reloads on WebSocket connect and reconnect (founder Q28; separate small unit, see U6) |

### Implementation units, in order

Each unit is shippable alone and keeps CI green. Counts are `grep` counts in `pkg/` (production files; tests excluded) and are **Verified** as counts, **Inferred** as a measure of risk.

| Unit | What | Blast radius | Evidence |
|---|---|---|---|
| **U0** | PR #1204 (open): per-session admission, and the per-session route key whenever `msg.SessionID` is set; refusal text. Bridge only; D5 later deletes its refusal. | 9 files; `pkg/agent/ordinary_execution_admission.go`, `loop_inbound.go`, `translate_error.go`, `session_end.go`, tests | PR diff `origin/release/v0.1.1...origin/work/engine-admission-per-session-20261006` |
| **U1** | **Address book and session id on arrival.** New resolver; the default session replaces the heartbeat session (type `default`, `default_session_id`, `protected` while on the team); every inbound message carries its session id before any queue. Contract change first. | `pkg/session/unified.go`; `pkg/workspace/member_config.go`; `pkg/gateway/rest_workspaces.go`, `rest_sessions.go`, `heartbeat_schedule.go`, `schedules.go`; contracts (`Session.yaml` `type` and `protected`, `SessionLifecycleRecord.yaml`, `WorkspaceMemberConfig.yaml`, `WorkspaceMemberHeartbeat.yaml`); generated Go and TS; the Session panel pin. 26 production references to heartbeat types in 8 files | `grep NewHeartbeatSession|SessionTypeHeartbeat|OriginKindHeartbeat` |
| **U2** | **Worker scope per session id.** `sessionWorkers` keyed by session id; delete the `":"+sid` suffix match and the probe worker for channel hand-backs (A6). | `pkg/agent/session_worker.go`, `loop.go`, `loop_inbound.go`. 26 references in 3 files; `resolveSteeringTarget` 11 references in 4 files | `grep sessionWorkers`, `grep resolveSteeringTarget` |
| **U3** | **One queue and all-at-once.** Merge the worker inbox into the steering queue; steering keyed by session id; "all" only; remove `steering_mode`; a new turn starts at once with everything waiting; the inbox separated from the steering queue (hand-back wake becomes an inbox arrival). | `pkg/agent/steering.go`, `session_worker.go`, `loop_run_turn*.go`, `async_notifier.go`; `pkg/config` (`SteeringMode`); WebSocket per-message status; `docs/internal/architecture/steering.md`. 38 references to `enqueueSteeringMessage` in 7 files; 14 dequeue references in 5 files | `grep enqueueSteeringMessage`, `grep dequeueSteeringMessagesForScope` |
| **U4** | **Heartbeat and schedule input through the address book, run registration, and remove the waiting room.** `pickSession` and the task executor ask the address book; a main agent's event and task runs are registered in its default session (D8.3); delete D5's list. | `pkg/gateway/schedules.go`, `pkg/agent/task_executor*.go`, `ordinary_execution_admission.go`, `translate_error.go`; `ScheduleCreate`/`ScheduleUpdate` `session_mode` in the contract; 25 references in 4 files for the waiting-room symbols | `grep awaitPreviousOrdinaryExecution|activeScopes|attachExecution` |
| **U5** | **Turn registry, history window, cancel latches and delivery stamps by session id.** The composite key becomes the plain session id. Heaviest unit; may split into U5a (registry and cancel) and U5b (history and stamps). | `activeTurnStates` 92 references in 19 files (48 test files); `Sessions.GetHistory` 14 references in 11 files (55 test files); `cancel_prearm.go`; `webchat_channel.go` | `grep activeTurnStates`, `grep 'GetHistory('` |
| **U6** | **Agent list reload on WebSocket connect and reconnect** (founder Q28). Independent; may ship any time. | `src/` agent list store; no engine change | founder Q28 |
| **U7** | **Self-delegation for every agent, and registered runs (D8).** Permit self-delegation for every agent (security-lead review); register event and task runs as children of the default session with an injected notice and a returned result; add the delegation-priority instruction to the main agents' prompt text (prometheus-prompt-engineer; backend-lead wires it). Needs U1, U3, U4. | `pkg/agent/loop_delegation.go`, `pkg/workspace/delegation.go`, `pkg/agent/steer_launcher.go`, `task_executor*.go`, `pkg/gateway/schedules.go`; agent prompt text in `pkg/coreagent`; the Session panel (runs listed under the default session) | D8; `pkg/steer/types.go::LaunchRequest` |
| **U8** | **Channel messages to the default session (D10).** The channel inbound path asks the address book; reply routing keeps channel and chat per message. | `pkg/agent/loop_inbound.go`, `pkg/channels/*`, `pkg/agent/async_notifier.go` | D10; audit hit O1 |
| **U9** | **Session end (D9).** Not built here: deletion of `lazy`, `joined`, `explicit` and auto recap on by default are in progress on another branch. This unit only checks that the default session follows the idle rule. | `pkg/agent/session_end.go`, `pkg/agent/memory.go`, `pkg/config/config.go` | D9 |

Order reason: after U1 every message has a session id, so the composite route key is a pure function of (agent, session id). U2 to U4 can then move queues and workers to the session id without a second key to disagree with. U5 last removes the now-redundant agent part from the registry key, which is the largest test-churn change.

### Risks

| # | Risk | Evidence | Mitigation |
|---|---|---|---|
| R1 | A session can have more than one agent over time (handoff, `AgentIDs`, `ActiveAgentID`). "The key is the session id" must still pick the right agent for the turn. | `pkg/session/unified.go::NewHeartbeatSession` stamps `AgentIDs` and `ActiveAgentID`; audit O6 (handoff pin). **Inferred** for the exact handoff behaviour | U1 spec must define "the agent of a session" as `ActiveAgentID`; test a handoff mid-session |
| R2 | The default session breaks a rule that was easy to keep per task: one goal per owner (C10, C11). | `pkg/goal/predicate.go::errMultipleActiveGoalsForOwner`; `task_executor.go::activateTaskGoal` | **Resolved by D8.3.** Tasks keep their own session, so each keeps its own goal |
| R3 | Schedule modes `isolated` and `continue` were designed for per-run sessions. Replacing the `main` mode with the default session (D8.4) changes what `session_mode` means and is a contract change. | `pkg/gateway/schedules.go::pickSession`; `ScheduleCreate.yaml::session_mode` | U4 spec confirms D8.4: `main` is replaced, `isolated` and `continue` stay |
| R4 | The default session keeps its history across idle ends and resumes (D9), so it grows and its context cost grows. | Same engine as a long chat. **Unknown** whether compaction covers a session that is resumed for months | U1 spec adds a long-run test; no new compaction here |
| R5 | Lazy creation can race: two events for the same (agent, workspace) at once. | The existing eager path takes a lock for the whole handler (`rest_workspaces.go`, comment at the PUT handler) | Per (workspace, agent) lock in the address book; a concurrency test |
| R6 | Removing the 5 s wait removes a guard that hid a real race: a new turn admitted while the old turn's tail still runs. | `ordinary_execution_admission.go::awaitPreviousOrdinaryExecution` doc | The session loop serialises (D4.1): the next turn starts only after the previous one settled. Test: turn B never starts before A's disposition settles |
| R7 | Hand-back wake semantics move from the steering queue to the inbox (C7). The "poll or wake, never both, exactly once" rule must survive. | [The sub-agent control plane](./ADR-20260928-sub-agent-control-plane.md), amendment D-E | U3 keeps one durable message identity; test exactly-once across the move |
| R8 | A held-after-Stop queue (D4.6) is easy to forget and looks like lost messages. | Founder Stop table; ADR-093 D4 rule that only a person resumes a stopped session ([An open conversation must keep the ability to delegate](./ADR-093-open-conversation-must-keep-delegation.md)) | Show held messages in the chat; open as Q-ADR-12 |
| R9 | An event run over the parallel cap is dropped instead of queued, and a registered run that never reports leaves the default session waiting on nothing. | `task_executor.go::TryAcquireDispatchSema` returns a bool (refusal path); **Inferred** for events with no durable record | U7 spec: every registered run is recorded before launch; a run without a report ends in a visible failed or stopped state |
| R10 | Stop all or `/cancel` on a default session now also stops every registered task and event run, including scheduled work, because they are children of it. A plain Stop leaves them running and looks like it did nothing. **Consequence of the founder's decisions, to be confirmed by the founder.** | Founder Stop table (D6); `StopDelegatedTree` | Show running registered runs in the default session. Recommended: confirm that Stop all should also stop scheduled runs, or exempt registered runs from the tree stop |
| R11 | Self-delegation for every agent is a permission change, and the delegation-priority rule is only an instruction, so a model may ignore it. | `pkg/agent/loop_delegation.go::buildDelegationDenyChecker` | Security-lead reviews D8.1; the instruction text is tested in UAT, not assumed |
| R12 | Channel users share one session. Replies could go to the wrong person if the channel and chat stamp is lost. | D10; audit hits O5, O7 | U8 spec: a test with two channel chats on one default session |

## Alternatives considered

| Alternative | Why rejected |
|---|---|
| Keep the agent-level keys and fix each hit by hand (what U0 did for admission) | The audit found 16 hits with one cause. Each fix leaves the next key in place. U0 shows the cost: one hit moved admission to the session id and exposed the registry as the next mismatch. |
| Keep a short wait but make it per session | Founder Q20: no timer, no second waiting room. A timer is a guess; the loop's own ordering is exact. |
| A new "default session" concept beside the heartbeat session | Two standing sessions per (agent, workspace) with the same shape. Founder 2026-10-06, simplest design first: reuse the existing path. |
| One queue that also holds child reports | Child reports carry no authority; mixing them with person and parent instructions invites prompt-injection through a report. Founder inbox refinement rejects it (C6 marks the text untrusted). |
| Tasks and events for a main agent run inside the default session | Founder round 1 (Q-ADR-2): they keep their own session as today and are only registered in the default session. |
| A new fork mechanism of the default session | Founder 22:09: use the existing self-delegation. A fork would also be a second kind of child session. |
| Remove hands-on tools from the main agent's default session | Founder round 1 (Q-ADR-2): the main agent keeps all its tools and is told to prioritise delegation. It would also add a per-session tool layer next to Hard Constraint 6. |
| Per-person session for channel messages now | Founder 22:50: for now all channel messages go to the default session. Per-person sessions are a later idea, issue #1206. |

## Affected components

| Area | Components |
|---|---|
| Engine | `pkg/agent/turn.go`, `ordinary_execution_admission.go`, `session_worker.go`, `steering.go`, `loop_inbound.go`, `loop_run_turn*.go`, `loop_window.go`, `cancel_prearm.go`, `async_notifier.go`, `task_executor*.go`, `translate_error.go` |
| Sessions and workspaces | `pkg/session/unified.go`, `pkg/workspace/member_config.go` |
| Gateway | `pkg/gateway/rest_workspaces.go`, `rest_sessions.go`, `heartbeat_schedule.go`, `schedules.go`, `webchat_channel.go` |
| Config | `pkg/config/config.go`, `defaults.go` (`SteeringMode`) |
| Contracts | `Session.yaml` (`type`, `protected`), `SessionLifecycleRecord.yaml`, `WorkspaceMemberConfig.yaml`, `WorkspaceMemberHeartbeat.yaml`, `ScheduleCreate.yaml`, `ScheduleUpdate.yaml`, the session-type copy in `openapi.yaml`; generated Go and TS |
| SPA | Session panel (default session pinned), separate display of each queued message, agent list reload (U6) |
| Product prompt text | The main agents' delegation-priority instruction in `pkg/coreagent` (prometheus-prompt-engineer) |
| Docs | `docs/internal/architecture/steering.md` and the user docs on delegation and sessions |

## Defaults kept (default, founder may overrule)

These are the Opus defaults (T1 to T11) that no founder decision contradicts. Each is **default, founder may overrule**.

| # | Default | Status in this ADR |
|---|---|---|
| T1 | Self-delegation, not a fork. A helper is an ordinary session with the same agent and profile, started through `SteerLauncher.Launch` | Kept; now also a founder decision (D8.1) |
| T2 | The default session is the heartbeat session generalised: one per main agent per workspace, kept when the heartbeat is off | Kept and **widened by the founder**: it always exists and is always protected while the agent is on the team (D2.1) |
| T3 | No workspace means My Workspace | Kept (D2.3) |
| T4 | The default session cannot be deleted while the agent is on the team; no "clear history" for now | Kept (D2.4) |
| T5 | A task keeps its own task session and its own goal and "one goal per owner" rule | Kept, **adjusted**: the task session is registered as a child of the default session instead of becoming a helper of it (D8.3) |
| T6 | A self-helper may not self-delegate again; it may delegate to other agents under the depth limit (default 3) | Kept (D8.1) |
| T7 | Schedule modes | **Adjusted**: a schedule fire is its own session registered in the default session; `main` is replaced by the default session; `isolated` and `continue` stay (D8.4) |
| T8 | Results are shown in arrival order, one per run, no merging | Kept (D8.3) |
| T9 | Reports sent while the default session is stopped wait until it resumes | Kept (D8.3) |
| T10 | Existing sessions: no conversion, no migration; only fresh installs are verified | Kept (D2.2) |
| T11 | Channels stay out of scope | **Replaced** by the founder's 22:50 decision (D10) |

## Contradictions in the first draft, and how they are resolved

| # | The first draft said | Resolved by |
|---|---|---|
| X1 | D8 was a founder proposal with details open and introduced a "fork" as new machinery | Founder 22:09 and round 1. D8 is rewritten as self-delegation, decided. The word "fork" is gone |
| X2 | Heartbeat and plain events ran in the default session directly; only tasks and schedule fires forked | Round 1 Q-ADR-1: events and tasks keep their own session and are registered in the default session (D8.3) |
| X3 | Self-delegation was described as existing and needing no new target kind | Corrected. Today only Jim and General Purpose may do it, with a workspace entry. The founder decided every agent may (D8.1) |
| X4 | D2 said "heavy work may run in a fork" in the default session | D2's table now follows D8.3 |
| X5 | The draft was silent on extra chats and on typing in the default session | Round 1 Q-ADR-3: the delegation-priority rule applies to all chats with a main agent (D8.2) |
| X7 | "A fork never forks (depth 1)" with no basis in the code's depth default of 3 | Kept as an explicit default (T6), not presented as existing behaviour |
| X9 | No workspace meant the key `(agent, "")` | My Workspace (D2.3, T3) |
| X10 | The old Q3 compared options without noting that the snapshot parameter is dropped | Defect N1 below; Q-ADR-8 states it |
| X11 | Status said "Awaiting its one grill-spec ADR-mode review" | Status line changed. The Astra review waits until the founder says the design talk is over (founder 22:12) |
| X6, X8 | "Use the existing global cap" (which is 2,000, no real limit); tasks in helpers would be force-stopped at 30 minutes | X8 is settled by round 1: event and task runs keep their own limits (D8.3). X6 stays open as Q-ADR-9 |
| X12 (new) | Channels were out of scope (D7, T11) | Founder 22:50 (D10) |
| X13 (new) | The default session never ended and the heartbeat disable path deleted it | D2.1 and D9 |

## Known defects found while writing

These were found in the code while preparing this ADR. They are reported, not fixed here.

| # | Defect | Where | Failure scenario | Certainty |
|---|---|---|---|---|
| N1 | **The delegate snapshot is silently dropped.** The `delegate` tool parses and size-checks a snapshot of references and notes, then never passes it on. `LaunchRequest` has no field for it | `pkg/tools/delegate_run.go` (`dt.snap` is set and validated; `launchAndDispatch` builds the `LaunchRequest` without it); `pkg/steer/types.go::LaunchRequest` | The model believes it handed the helper named files and notes. The helper never sees them and works without that context | Verified that it is dropped; Inferred as unintended |
| N2 | **An untargeted self-delegation slips past the block.** `delegate` with no `agent_id` launches the caller's own agent once the caller has an edge to any other agent. The same request with `agent_id` set to itself is refused for every agent except Jim and General Purpose | `pkg/tools/delegate_run.go::launchAndDispatch` (an empty id becomes `ToolAgentID`); `pkg/agent/loop_delegation.go::evalUntargetedDelegation` | An agent that is not allowed to self-delegate does so anyway by leaving the id out. Under D8.1 the rule changes, but the two paths must agree | Verified in the code path; Inferred as unintended. Security-lead should look |

## Evidence and verification

Read in this task: every 2026-10-06 entry in `coordination/CONTINUATION-20261005.md`; the Opus open-questions file (Q-ADR-1 to 14, T1 to T11, X1 to X11, N1, N2); the first draft of this ADR; and, in the tree at this branch, `pkg/config/config.go` (`IdleTimeoutMinutes`, `AutoRecapEnabled`), `pkg/config/config_agents.go::GetIdleTimeoutMinutes`, `pkg/agent/memory.go` (recap triggers), `pkg/api/generated/asyncapi_types.gen.go` (`WsFrameTypeSessionClose`), `pkg/gateway/rest_workspaces.go` (`NewHeartbeatSession` on enable, disable-path release), `pkg/gateway/rest_sessions.go::computeSessionProtected`, `pkg/tools/delegate_run.go` (`dt.snap`), `pkg/agent/loop_delegation.go` and `pkg/workspace/delegation.go`. The code claims about the audit, C1 to C14 and the 2,000 parallel cap were verified by the earlier passes and not re-run here. Items marked **Inferred** or **Unknown** are labelled where they appear. The deletion of the recap triggers is on another branch and is not verified here.

## Still open

These are the Opus questions the founder has not answered. The ones the decisions above settle are removed: Q-ADR-1, 2, 3 and 4 are answered, and Q-ADR-10 is settled by "events and tasks keep their own session" (they keep their own limits and goal). Each question below states what it affects, the options and a recommendation.

### Q-ADR-5 — Where do a helper's approval requests appear? (A / B)

On a fresh install, shell commands are set to "ask" (ADR-092). If hands-on work runs in helpers, most approval requests come from a helper. The approval message already carries the helper's session id (`pkg/gateway/approvals.go`, the `SessionID` field). Whether the web app shows it in the default chat today is **Unknown**.

| Option | Where the person approves |
|---|---|
| A | In the default session (and the chat that started the helper), labelled with which helper asks. Also visible in the helper's own chat |
| B | Only in the helper's own chat. The default session shows "helper waiting for approval" |

**Recommendation: A.** Otherwise approvals are hidden in helper chats and work stalls unnoticed.

### Q-ADR-6 — How are results shown in the default session? (A / B / C)

| Option | Presentation |
|---|---|
| A | The agent relays it: the report starts a turn and the agent writes the summary |
| B | A result card only, no model turn |
| C | Card plus turn: a card always appears and the agent also gets a turn |

**Recommendation: C.** The card guarantees nothing is lost; the turn lets the agent coordinate the next step. Founder decided that the result returns into the default session (D8.3); how it is shown is still open.

### Q-ADR-7 — Which reports wake an idle default session? (A / B / C)

Helpers send six kinds of report: progress, checkpoint, artifact, blocker, question and handback (`pkg/tools/message_parent.go`). In every option a report arriving during a turn enters at the next step boundary, marked as a report with no authority.

| Option | Rule |
|---|---|
| A | Every report wakes an idle session |
| B | Only final reports, pauses, questions and blockers wake it. Progress, checkpoint and artifact wait |
| C | Nothing wakes it |

**Recommendation: B.** It also decides whether the "run started" notice (D8.3) wakes the session; recommended: it does not.

### Q-ADR-8 — What does a self-delegated helper receive? (A / B / C)

Today a helper gets only the task text plus its own profile and memory. The snapshot is dropped (defect N1). For event and task runs, the task text is the input as today.

| Option | Receives |
|---|---|
| A | Task text plus a short header (source, id, workspace, time). Fix N1 so the agent can pass named files and notes |
| B | A plus the last few messages of the default session |
| C | The full history of the default session |

**Recommendation: A.** Cheapest, and keeps one event's untrusted content out of another run.

### Q-ADR-9 — How many helpers at once, and how is cost shown? (A / B / C)

The unconfigured cap is 2,000, which is no real limit (`pkg/config/config_defaults_apply.go::EffectiveMaxParallelAgents`).

| Option | Limit |
|---|---|
| A | No new limit |
| B | A small limit per default session (for example 4). Extra helpers wait in a visible queue, never dropped |
| C | A small global default for the install |

**Recommendation: B, with the founder choosing the number.** Show helper count and token use per default session in the existing usage views.

### Q-ADR-11 — How does the default session keep its memory when work happens in helpers? (A / B / C)

The session-end recap is skipped for delegate-type sessions (`pkg/agent/session_end.go`, `skipped_delegate_session`). Event and task runs are not delegate-type, so they recap as today (D8.3). The question is about helpers an agent starts itself.

| Option | How facts reach memory |
|---|---|
| A | The helper's final report must carry the facts; the helper may write memory itself |
| B | Run the recap for self-delegated helpers too, into the same agent's memory (one summary call per helper) |
| C | The default session writes key facts to memory when a result arrives |

**Recommendation: B.** It reuses existing code and does not rely on the model remembering.

### Q-ADR-12 — Messages waiting when Stop is pressed (A / B / C)

| Option | Behaviour |
|---|---|
| A | Held: they stay queued, shown as held, and run when the person next sends or resumes |
| B | Dropped, shown as "not sent" |
| C | They start a new turn at once, which makes Stop look broken |

**Recommendation: A.** The person's text is not lost and Stop means stop. The default session should show "N helpers or runs still running" after a plain Stop.

### Q-ADR-13 — How does a person see and steer helpers? (A / B)

| Option | Display |
|---|---|
| A | Helpers and registered runs are grouped under their default session in the session panel, collapsed by default. The default chat shows a live strip of running children. Steering happens in the child's own chat or through the default session |
| B | Helpers hidden; the person steers only through the default session and the agent relays |

**Recommendation: A.** It is what exists (issue #1083 tracks show and hide) plus grouping. B makes the person's instruction depend on the agent relaying it.

### Q-ADR-14 — Who steers a worker session started by an event? (A / B)

The founder decided a worker has no default session and each task or event starts a new worker session. Who is its parent is not decided.

| Option | Rule |
|---|---|
| A | A standalone session with no parent. The person steers it from its own chat and it ends with its own result |
| B | The default session of the agent that owns the trigger becomes its parent |

**Recommendation: A.** B invents a parent that never asked for the work.
