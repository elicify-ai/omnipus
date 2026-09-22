# ADR-091 — A sub-agent is a session steered by another session

- **Status:** Proposed, rev 5 — four grills by Codex `gpt-6-astra`: rev 1 **BLOCK** (3 critical, 17 major, 2 minor; [`review.md`](ADR-091-steered-sessions-replace-subagents-review.md)), Appendix A; rev 2 **BLOCK** (2/16/2; [`review-r2.md`](ADR-091-steered-sessions-replace-subagents-review-r2.md)), Appendix C; rev 3 **BLOCK** (3/19/2; [`review-r3.md`](ADR-091-steered-sessions-replace-subagents-review-r3.md)), Appendix D; rev 4 **BLOCK** (2/16/1; [`review-r4.md`](ADR-091-steered-sessions-replace-subagents-review-r4.md)), Appendix E. Rev 4 was a consolidation of this ADR, the landing order and all seven specs against one interface contract; rev 5 applies the rev-4 findings and the founder's round-10 decisions. **The founder ended the grill cycle after rev 4** (2026-09-22): the trend was converging (22 → 24 → 19 findings; ten of twenty-four rev-3 fixes verified holding) and the remaining findings were applied without a fifth run. Founder decisions taken 2026-09-21/22 in ten rounds (Appendix B). Awaiting founder review.
- **Date:** 2026-09-21
- **Decider:** Daniel Piatkowski
- **Number verification:** ADR-091 absent from `docs/internal/architecture/` on every fetched `origin/*` ref on 2026-09-21; highest observed number 090. Recheck before publication.
- **Evidence baseline:** release worktree at `364290cb5` (`origin/release/v0.1.1`, includes #805 and #806). Every `file::symbol` below was checked to exist at that commit; the original analysis was made at `5d38291f3` and re-verified after the move. The two publication defects in §2.3 were independently re-confirmed by a second session at `f934e6965`. The #772 / PR #805 facts cited in D3 and §12 were verified on `origin/release/v0.1.1` after that merge. Live-instance figures come from `~/.omnipus/sessions` on the founder's machine on 2026-09-21 and are labelled as such.
- **Inputs:** the rev-1 grill report; two earlier `gpt-6-astra` reviews of a withdrawn ADR-057 §11 draft; the second session's publication audit (`omnipus2/.squads/v-chat-leak/SQUAD-V2-LEAK-AUDIT.md`), its #784 decision note and its context-limits proposal; the in-review #772 change (typed error frames gain child identifiers); issues #763, #764, #765, #755, #670, #658, #803, #784, #772, #614 (open) and #766, #781, #782, #783, #775, #769, #605, #659 (closed).
- **Supersedes / amends:** ADR-057 D2(a) (frame routing identity) and D4 (cancel basis), and the ephemeral child store D1 left in place; ADR-053 D1 where it still describes a delegate as a sub-turn of the parent's session. ADR-032 (a child never inherits parent-sourced *agent settings*) is unchanged.

## Context and decision boundary

A "sub-agent" is a session another session created to do part of its work. Today it is a special case in three ways, and every sub-agent defect found this week — output in the wrong chat, sessions the operator cannot find, a sub-agent that cannot be stopped once woken, one that dies at a third of its budget, work that cannot resume — traces to one of them.

Founder direction, 2026-09-21: *"A sub-agent should not be a special case. It is just a session that is steered by another session."* And: *"wait-inline can be removed — we have agent steering and do not need synchronous sub-agents."*

**In scope:** identity, storage, delivery, steering, cancellation, completion and presentation of a steered session; deletion of the mechanism that special-cases it. **Out of scope:** the compiled per-turn publication policy (a later ADR this one feeds, §12); the context-limit policy (#803; enabled here, decided there); the hand-back content contract (#784); live steering of external command-line workers (§3 D5, explicitly excluded).

## 1. The problem, in plain terms

Two mechanisms let one agent hand work to another — **delegation** (`delegate`, `pkg/agent/subturn.go`) and **tasks** (`pkg/agent/task_executor*.go`). Native delegation and native tasks run the same turn engine, `pkg/agent/loop.go::runAgentLoop`, with the same kind of options; external command-line workers use a shared runner reached from both (`pkg/agent/external_dispatch.go::runExternalCLISubTurn`, called by `task_executor_run.go::processTaskDirectExternalCLI` and by `subturn.go`). The difference between the two mechanisms is a thin setup wrapper each, and the delegation wrapper builds the wrong thing.

A **task session** is a normal session: its own id; its own chat address (`Channel: "webchat"`, `ChatID: <its own session id>` — `task_executor.go::processTaskDirect`); a workspace and a title stamped at creation (`task_executor.go::createTaskSessionSync`); history in the agent's real persisted store. Plans already use tasks for their members.

A **delegated session** is three special cases:

| | Special case | Where | Consequence |
|---|---|---|---|
| 1 | **Borrowed identity.** It copies the parent's `Channel`/`ChatID`/`SenderID`/`SenderDisplayName` and carries them unchanged at every depth. Its *runtime* workspace is inherited (`processOptions.WorkspaceID`), but its persisted session record gets neither a workspace nor a title. | `subturn.go::prepareProcessOptions`, `subturn.go::createChildSession` | Anything it publishes lands in the chat that started the chain — across agents and workspaces. Live instance: 80 of 80 delegate sessions have an empty `workspace_id`, so no workspace view can list them (#763). |
| 2 | **Two memories.** Its model history is an in-memory ring of 50 messages, dropped from the front, never written (`subturn.go::buildDelegateAgent` installs `newEphemeralSession`; `maxEphemeralHistorySize`; `truncateLocked`). Its durable transcript is written separately. | `subturn.go` | Tool results that fall out of the ring cannot be shrunk by the context-emptying pass (`empty_in_place.go::eligibleToolResults` skips `line < 0`). This is the *proposed* mechanism for #775 — a hypothesis until reproduced. Model-history continuity is lost when the turn ends; native follow-up exists but recreates an empty ring, which is why #803's resume has nothing to resume from. The ring's own comment gives its reason — *"so child turns never pollute … the source agent's real session history"* — a premise ADR-057 D1 removed. |
| 3 | **Rebuilt differently on wake-up.** A delegated session that itself delegated is re-entered when its child finishes, by `pkg/agent/loop_inbound.go::processSystemMessage`, which rebuilds it with `SendResponse: true` and restores neither the parent link nor the cascade root. | `loop_inbound.go::processSystemMessage`; `turn.go::newTurnState` defaults `routingSessionID` to the turn's own session | Its **final reply** publishes to the originating chat (`loop.go::runAgentLoop` publishes `finalContent` on `SendResponse`) — the founder's live Jarvis leak. Root **Stop no longer reaches it** (ADR-057 D4: the durable walk serves non-turn resources only). Existing cancellation tests cover live descendants; none covers a re-entered one (#670). |

Containment shipped 2026-09-20 (#766, #781, #782, #783) closed four publication sites by hand. It could not close the class: `loop_run_turn_tools.go::deliverToolOutput` still sends **media** gated only on `len(Media) > 0`. Each sending site is a door with its own lock; this ADR replaces the locks with one identity.

## 2. Evidence

### 2.1 Already shared (verified in code)

| Capability | Delegation | Task | Shared? |
|---|---|---|---|
| Turn engine (native) | `runAgentLoop` | `runAgentLoop` | **Yes** |
| External-CLI runner | `external_dispatch.go::runExternalCLISubTurn` | same function, from `processTaskDirectExternalCLI` | **Yes — one runner, two setup wrappers** |
| Steering queue | `steering.go::enqueueSteeringMessage(scope, agentID, msg)` | same | **Yes — engine-level.** Every native turn drains it for its own `sessionKey` (`loop_run_turn.go`, `loop_run_turn_iterations.go`). Only the `delegate` tool *exposes* it. |
| Lifecycle record | `OwnerScopeKind = parent_session` for nested children; `human` for a top-level delegation (`delegate_run.go::persistLifecycle`) | `plan` / `human` | **Yes — one type**, `session/lifecycle.go::LifecycleRecord`, carrying `ParentDurableKey`, `OriginChannel`, `OriginChatID`, `WorkspaceID`, `NeedsInput` |
| Authorization gate | `loop_delegation.go::buildDelegationDenyChecker` | same function | **Same function, different settings** — see D2 |
| Concurrency | `admission.go::ResolveRootDelegationCap` honours `SubTurn.MaxConcurrent`, else `Performance.EffectiveMaxParallelAgents()` | the performance value | Shared only under default settings — see D9 |
| Depth | `delegation_depth.go::resolveEffectiveDelegationDepth` (global ∧ per-edge) | a separate task-mode ceiling, `tools/task.go::SetMaxDelegationDepth` | Two constraints — see D9 |
| Cancel primitive | interrupt by session | `plan_engine.go::StopTask` → `cancelSessions` → `RequestCancelForSession` | **Yes, underneath** |
| Parent link | `ParentDurableKey` (direct parent) + `UnifiedMeta.ParentSessionID` + inherited `Owner` (`session/unified_api.go::CreateSessionWithID`) | `created_by_agent_id`, `parent_task_id`; no owner copy, no `ParentSessionID` | Both exist; shapes differ — see D2 |
| Child → parent inbox | `session/message_inbox.go::MessageInboxStore`, keyed `(ownerKey, childSessionID)`; `tools/message_parent.go::ownerKeyFor` uses `ParentDurableKey` | unused by tasks | Generic storage, delegate-only consumer |
| Immediate start | spawn | `task_executor.go::StartTaskNow(taskID)` — takes a **task id**, creates the task's session, returns without dispatch if `Task.SessionID` is already set | Both exist; not interchangeable — see D1 |

### 2.2 Delegation-only today, and what happens to each

| Capability | Decision |
|---|---|
| **Wait-inline** — `delegate_run.go::executeSync`, `async:false` (default is async) | **Removed** (D4) |
| Steering **tool actions** — `steer`, `respond`, `peek`, `inbox`, `inbox_ack`, `follow_up`, `cancel`, `status` (`tools/delegate.go`) | Generalised to any steered session (D5) |
| Ask the parent a question and pause (`delegate_park.go`; `message_parent.go::parkNeedsInput`) | Kept; carried on the edge (D5) |
| Progress to the parent (`tools/message_parent.go`) | Kept; wake identity corrected (D3) |
| Nested rendering of the child's steps inside the parent's chat (`subagent_start`/`subagent_end`, `parentSpawnCallID`) | **Removed from the chat**; the existing side panel and pill show the child, with a status line and an open control (D7) |
| Second external-CLI **setup wrapper** | One wrapper; the runner is kept (D10) |
| `switch_agent` excluded from the child's tools (`subturn.go::buildDelegateAgent`, `tools.ExcludedSwitchAgent`) | Kept as a property of the edge (D2) |
| The in-memory history ring | **Deleted** (D1) |

### 2.3 Live defects this ADR closes by construction

- Media publishing with no delegate gate — `deliverToolOutput` (verified; confirmed by the second session).
- Re-entered delegate's final reply publishing to the origin chat — `processSystemMessage` + `runAgentLoop` (verified; confirmed).
- Root Stop not reaching a re-entered delegate — `newTurnState` default + no restore in `processSystemMessage` (verified by this session; not disputed by the grill).

### 2.4 Blast radius (measured, with the commands)

Counts at `364290cb5`, re-measured after review (round 7):

| # | Selection | Result |
|---|---|---|
| 1 | Non-test Go referencing the delegate session type or sub-agent frames | **24 files** (3 generated) |
| 2 | Contract files mentioning sub-agents or delegates | **58 files** — component schemas plus `openapi.yaml`, `asyncapi.yaml` |
| 3 | SPA files on the narrow four-token pattern (`parentCallId` is the SPA name; `parentSpawnCallID` is the Go name) | **18 files** |
| 4 | SPA files on the broad words `subagent` / `delegate` | **96 files** — an upper bound, not a work list |
| 5 | Go tests named for the mechanism (the WP-G set) | **47 files / 17,130 lines** |

```bash
# 1
grep -rlE 'SessionTypeDelegate|subagent_start|subagent_end|parentSpawnCallID' pkg --include='*.go' --exclude='*_test.go'
# 2
grep -rliE 'subagent|delegate' contracts
# 3
grep -rlE 'subagent_start|subagent_end|SubagentSpan|parentCallId' src --include='*.ts' --include='*.tsx' --exclude='*.test.*'
# 4
grep -rliE 'subagent|delegate' src --include='*.ts' --include='*.tsx' --exclude='*.test.*'
# 5
wc -l pkg/agent/subturn*_test.go pkg/tools/delegate*_test.go pkg/tools/message_parent*_test.go
```

Rev 1 quoted 60 SPA files and 46 test files without commands, and rev 2 quoted 56 / 22 / 200 / 50 from a different tree; all those figures are withdrawn. Tests are not rewritten wholesale: WP-G classifies each as **retain** (cancellation, isolation, target identity — behaviour this ADR keeps), **update** (identity source changes) or **delete** (wait-inline, the ring).

## 3. Decision

### D1 — One primitive: the session launcher; a task is a steered session plus criteria

There is one way to create a session that another session steers: a **session launcher** beneath both tools. It creates the session exactly as a task session is created today — own id; `Channel`/`ChatID` = its own session; `WorkspaceID` inherited from the *creating session's* workspace (a creator with none yields a child with none — inherited, never invented); a `Title`; history in the agent's real persisted store, trimmed and emptied by the same code as every other session — and it records the steered-by edge (D2). Creation is **atomic for the mandatory fields**: identity, ownership stamp, workspace, title and edge are written before any turn runs; a failed mandatory write is a failed launch that leaves no partially registered session. This is stricter than `createTaskSessionSync`, which logs a failed `SetMeta` and continues; that leniency does not carry over.

- `delegate(action="run")` = launcher + dispatch. A delegation is a plain steered session. It is **not** a task-board record and pays no claim ceremony (D6). The tool returns as soon as launch and dispatch have returned; the child may already be running (*founder decision, round 9, superseding round 6* — an ordering hook between the parent's tool result and the child's start would be new mechanism for no visible benefit, since a child never reaches the parent's chat).
- `create_task` = task record (criteria, Definition of Done, board entry, triggers) + the same launcher at start: `StartTaskNow` calls the launcher when the task has no session yet and dispatches otherwise. A task created by an agent from a session carries that session in the task's existing disk-only `OriginSessionID`, which the launcher reads as the steering session if it still exists; human-, schedule- and plan-created tasks are ordinary roots.
- **Every session that takes part in steering has a lifecycle record** (*founder decision, round 9*): a chat session gets an `ordinary_root` record the first time it delegates, written by the launcher. That is what makes a root's Stop durable (D8) and lets a lost child record be told apart from a root (landing order I-8).

Every published interface — the launcher, the audience resolver, upward delivery, cancel and revival, record classification, the boundary observer — lives in one neutral package, `pkg/steer`, that imports nothing from the agent, tools, channels or gateway packages; `pkg/agent` implements them and the gateway wires them at boot. This is the pattern `SubTurnSpawner` and `MessageParentWaker` use today, generalised to one place, because the tools, channels and gateway packages cannot import the agent package (landing order §2). `Launch` writes; `Dispatch` decides admission and returns the authoritative `running` / `queued` result.

The ephemeral ring is deleted. A steered session has **one** memory. *Founder decision, round 2 Q1.*

### D2 — One steered-by edge, restored identically on every entry

A steered session carries exactly one relationship to the session that steers it, persisted on its `LifecycleRecord`. The edge is the **canonical parent relationship**; `UnifiedMeta.ParentSessionID` and the inherited `Owner` stamp are derived from it at creation and kept in step, because existing readers depend on them (`askuser/registry.go::CreatePending` rejects children by `ParentSessionID`; `loop.go` installs ownership only when `meta.Owner` is set — ADR-057 D1). The edge carries:

| Field | Purpose |
|---|---|
| steering session id | direct parent; the inbox owner key |
| **cascade root**, verified | the root whose Stop must reach it — resolved by walking the edge chain at creation and stored; never the direct parent alone (rev 1's D11 restored the direct parent, which stops the cascade at generation two — corrected) |
| reporting target | the steering session's own address; where completion wakes it (D3) |
| origin | which front door launched it (`delegate` or `task`) and the tool-call id that did — the key the side panel's events use (D7) |
| generation (record level) | moves **only** when a stopped session is revived by a newer instruction, never on an ordinary re-entry; a Stop marker names the generation it stops (D8) |
| Stop marker (record level) | the durable "stopped, when, which generation, by whom" note the cascade writes on every session it reaches (D8) |
| authorization verdict and remaining depth | the gate's decision at launch; `ResolvedMaxDepth` |
| creator-set limits | timeout; anything the creator may bound (D9) |
| tool exclusions | `switch_agent` today |

**`ParentDurableKey` is removed in the same delivery** (*founder decision, round 5*) — no alias period. Its readers — **15 non-test files, not three** (review round 7) — move to the edge as part of the one integration, by owner: WP-B `tools/message_parent.go::ownerKeyFor` and `agent/session_messaging_wire.go::deliverParentToChild` (the forged-target gate); WP-C `tools/delegate.go::verifyCallerOwnsSession`, `delegate_status.go`, `delegate_park.go`, `delegate_followup.go`, `delegate_run.go`; WP-D `agent/cancel.go::CollectDescendantSessionIDs` (its `LifecycleFilter{ParentDurableKey}` becomes a filter on the edge's steering session), `agent/steering.go`, `gateway/websocket_cancel.go`, `gateway/rest_sessions.go`; WP-A `agent/task_executor.go`, `session/lifecycle.go`, `session/lifecycle_index.go`. `tools/list_jobs_sources.go::collectSubagentRows` is **not** a reader of this field — it filters on `LifecycleRecord.ParentAgentID`, which the launcher now stamps for both fronts (tasks left it empty), so that filter stays; its three changes are in D10 (exclude task-origin records; actionability from the record's state; label from its title — no live index). WP-A removes the field once those moves are in; WP-F verifies zero readers.

**Integrity rules** (rev 1 had none): parentage is immutable; a cycle is rejected at launch; the stored root must equal the walked root or the launch fails; an ancestor's record is retained (not pruned by `lifecycle.go::pruneTerminalOne`) while any descendant is non-terminal; an unreadable ancestor makes a Stop **partial and reported** (§10 AC-8), never silently complete; a record whose edge cannot be read or validated **may not run at all** — "no audience" is not enough, execution authority is missing (rev 1's "persist-only" is withdrawn).

**Authorization at launch.** Both tools use `buildDelegationDenyChecker`, today with different settings: delegation bakes in `selfAssignmentExempt=false` and checks edge mode `direct`; tasks bake in `true` and check `task`. Founder decision, round 2 Q2: **self-target is allowed for both.** A delegation to the agent's own profile is a new session with the same profile; it recurses only if its task tells it to, and that is bounded by the depth cap and the concurrency cap, which already exist and are the guards. The self-target ban is removed from the delegate gate; the mode check stays as today (`direct` for delegation, `task` for tasks), so the edge vocabulary is unchanged. Revoking an edge affects future launches only; a running session is stopped by cancel, not by revocation.

**The rule that fixes special case 3:** every path that starts a turn on a steered session — first run, wake-up, resume, boot — derives the turn's identity **from the edge**, never from the entry path's arguments. `processSystemMessage` (and any successor) reconstructs a steered session's turn from its record. A turn cannot come back "able to talk to the user" or "unreachable by Stop", because those are no longer per-turn choices.

### D3 — A steered session has no user audience; it reports upward through one operation

A steered session **never publishes to a user-facing address** — at any depth, on any entry path. This is a property of the session, decided once, read by every delivery boundary through the injected `steer.AudienceResolver`, which classifies the record and its metadata together (landing order I-8) and answers "none" on any doubt; each boundary then calls the `steer.BoundaryObserver` before acting, so a test can prove the boundary was reached. The boundaries are enumerated, because rev 1 listed three and the code has twelve (the canonical list is landing order §6; every spec references it):

| Boundary | Site |
|---|---|
| Synchronous tool text | `loop_run_turn_tools.go::deliverToolOutput` |
| Asynchronous tool feedback | same file, the async callback gate |
| Final reply | `loop.go::runAgentLoop` |
| **Media** | `deliverToolOutput` — `SendMedia` / `PublishOutboundMedia` |
| Retry notices | `loop_run_turn_response.go::retryTimeout`, `retryContextOverflow` |
| Streaming (webchat) | `gateway/websocket_streamer.go` |
| Streaming (external channels) | `channels/manager.go::finalizeHookStreamer` — Telegram/WeCom |
| Agent-requested direct messages | `tools/message.go::denyUnownedTarget` does **not** contain it: it allows any unbound channel, and webchat is unbound and shared by operator decision, so a child could send to the root chat by naming it. Rev 2's "no new rule" (round 5) is withdrawn. **Rule (founder decision, round 8):** a steered session's `message` tool may target only its own session's conversation; every other target — the parent's chat, another webchat id, any external channel — is refused with a named error. It talks to its parent through `message_parent` and the steering surface, nothing else. Tested with real channel ownership (a nil ownership stub refuses everything and proves nothing). Owner: WP-B |
| Task result notifications | `task_executor_judge.go::notifySourceChannel` |
| Typed error frames | the `LLMError` family — #772 (PR #805, merged) added the `code` value `delegated_task_limit`; the identifiers travel inside the existing `message` string. The frame is a publication surface and is covered |
| Delegate-lifecycle notices | `subturn_result.go::emitSubTurnIterationLimitNotice` (#805; called from `subturn.go`) persists an identified max-iterations notice to the **root** transcript — child-origin content reaching the operator. Covered: delivered as an attributed lifecycle notice through the upward operation below and shown as the child's status line in the side panel, never as a bare bubble in the root chat |
| Question cards | `askuser/registry.go` — a steered session's question is relayed to its steering session (D5), never broadcast as the parent's own |

A steered session's `message` tool reaches its own session only (round 8, above); a task session gets the same rule, since it is the same kind of session. Tool **errors** remain visible in the steered session's own view and transcript (D7) and as an `error` line in the parent's side panel — never routed to the inherited chat. This keeps the #782 property.

**One upward-delivery operation** replaces two incorrect paths. Rev 1 claimed the existing wakes already reach the right parent; they do not: `task_executor_judge.go::notifyParentIfAllSiblingsDone` builds its address from the parent *task* id (`"task:" + parent.ID`, never `parent.SessionID`) and fires only when all siblings are terminal; `message_parent.go` wakes with the **child's** `AgentID` as sender identity and `async_notifier.go::WakeParent` forwards it unchanged. The operation defined here (landing order I-5, an injected interface on the tools side like today's `MessageParentWaker`) resolves the **steering session and its agent from the edge**, distinguishes producer (the child) from recipient (the parent), and wakes the parent on the parent's own address. Completion wakes **per child, as each finishes** — *founder decision, round 2 Q5*; `notifyParentIfAllSiblingsDone` is deleted, not kept behind an option.

**No second vocabulary.** The upward event **is** an existing `SessionMessage` written with `session/message_inbox.go::MessageInboxStore.Append` first: a completion is a `handback` (`mode: final`, the answer in `result_so_far`, parked descendants' questions in `open_questions`); **an empty answer is a failure** — the record is `failed` and the parent gets an `error` `empty_answer:` (*founder decision, round 10*); a parked child is a `question`; failure, timeout and interruption are an `error` with `fatal: true` and a prefix naming which; a Judge verdict is the existing `goal_status` kind, **extended minimally by WP-E** with a parent-bound direction, a `not_met` condition and per-criterion evidence (*founder decision, round 10* — the kind today can only say "met" to the UI); progress, checkpoint and blocker are what they are today. Terminal entries carry a **deterministic id** (`<child>:<generation>:final`) and are appended **before** the terminal lifecycle write, so a crash on either side of that pair is repaired at boot into exactly one entry. The `message_id` travels in the wake; the parent writes a `consumed <id>` marker before executing — or, if it already has a live turn, the wake is enqueued into that turn and the steering-queue drain writes the same marker when it dequeues it. The guarantee is exactly this: *each upward event is consumed at most once — by one new turn or one injection into a live turn, never both, never neither*; what the turn then does has today's guarantees for an interrupted turn. **One wake-eligibility table** governs both first delivery and boot: `handback`, `question`, `blocker`, fatal `error` and `goal_status` wake, are always admitted and bypass the notifier's debounce and hourly cap (`allowWake`), so a terminal outcome can never be suppressed; `progress`, `checkpoint` and non-fatal `error` never wake — not at first delivery, not at boot — and remain subject to the unacknowledged cap and the per-minute rate, a withheld wake being reported as *suppressed*, never as delivered (*founder decision, round 8*: today's `async_notifier.go::wakeableSessionMessageKinds` — question, blocker, error, handback — plus `goal_status`, minus non-fatal errors). At boot, every wake-eligible unacknowledged entry without a consumed marker is re-woken once. A recipient that carries a Stop marker for its current generation is not woken; the entry waits for its revival. **Bus authority:** a steering event on the bus carries the principal its publisher verified (`tools/delegate.go::verifyCallerOwnsSession`, or the gateway's authenticated human), and `session_messaging_wire.go::deliverParentToChild` re-verifies it against the target's edge — a valid child naming another valid child is refused.

### D4 — Wait-inline is removed

`delegate` `async:false` and `delegate_run.go::executeSync` are deleted. The parent's turn never blocks on a child; it steers, parks, or ends its turn and is woken by completion. *Founder decision, round 1.*

The removal is wider than one argument. **Deletion manifest** (rev 1 listed two items):

| Where | What goes |
|---|---|
| `pkg/config/config.go` | `DelegationModeAwait`; the mode set becomes `background`, `task` |
| `pkg/coreagent/seed.go::coreAgentDelegation` | the `Await` entries on Jim, Planner and Worker edges |
| `pkg/agent/loop_wire.go::registerSharedTools` | await-specific gate wiring |
| `pkg/agent/loop_delegation.go::EdgeModeCategory` | the `Await` case (maps to `direct`; `Background` remains) |
| `pkg/agent/loop_env.go::wireDelegationInjectors` | the expansion of `direct` into *both* `Await` and `Background` |
| `pkg/agent/delegation_context.go::buildDelegationContext` | the generated system-prompt line advertising `async=false` |
| `pkg/tools/delegate.go` | the `async` argument **and** `allow_blocking_question`, whose description reads "with wait/async=false only" |
| `contracts/components/schemas/WorkspaceDelegationEdge.yaml` | "synchronously (await) or as a background spawn" |
| Persisted config with `await` | rejected at load with a named error (greenfield, §6) |
| Tests asserting synchronous behaviour | deleted (WP-G classification) |

Acceptance is a **real generated system prompt** with no await advertisement and **real argument validation** rejecting `async:false` — not the absence of one function (AC-4).

### D5 — Steering is a capability of a steered session; who may steer is stated per action

`steer`, `respond`, `peek`, `inbox`, `inbox_ack`, `follow_up`, `cancel` and `status` operate on any steered session for which the caller holds authority. *Founder decision, round 2 Q3:* **any ancestor may act, and the human operator may steer directly.** This matches today's ancestor walk (`tools/delegate.go::verifyCallerOwnsSession` walks `ParentDurableKey`) and rev 1's "immediate parent only" is withdrawn. Two steering sources for one child are reconciled by the steering queue itself: messages are delivered in arrival order and all are delivered; the child treats them as sequential instructions. A human who opens a running steered session and writes to it **is steering it**; that does not change the edge. This needs no new UI: an inbound chat message to a session with a live turn already becomes a steering message (`steering.go::enqueueSteeringFromMessage`), exactly as it does for a task session today (*founder decision, round 4*). Siblings and unrelated roots are rejected (AC-5). `status` answers from the record and the inbox — lifecycle state, the last status line, the time of the last upward message — never from streaming progress, so a child on a non-streaming provider or an external CLI reads as "quiet for 40 s", not "hung". This closes #614 (*founder decision, round 7*).

`delegate` survives as the agent-facing verb: `run` = launcher + start; the other actions are the generic ones under the name agents know. Sessions created with `create_task` expose the same actions — one primitive, two front doors (*round 1 Q1*).

**External command-line workers** (Claude Code, Codex, OpenCode) are steered sessions for identity, audience, workspace, cancel and completion wake, and **cannot be live-steered or ask questions** — `delegate_followup.go::executeSteer` and `message_parent.go` reject them because they never drain the steering queue. This ADR states the limit and does not close it (*round 2 Q8*). Corrective follow-up on an external session creates a new external run under the same edge, preserving parentage, limits and cancel scope.

### D6 — Completion: claim and Judge only when there is something to judge

A steered session **with no acceptance criteria** completes when its turn ends with a **non-empty final answer and a quiet subtree** — no descendant `queued` or `running` (*round 2 Q6*). Parked (asked a question), interrupted/Stop, timeout, and "answered while a descendant still runs" are each a distinct outcome, persisted in one of the eight existing lifecycle states and delivered as one of the existing message kinds (landing order I-5's table) — never conflated with done. **An empty final answer is a failure** (*founder decision, round 10*): persisted `failed`, delivered as an `error` `empty_answer:`, so "done" and "gave nothing" can never look alike to the lifecycle or to the parent. A parent that answered while a child still runs stays `running` and is completed by the last such child's completion wake; a parked descendant does not block it — its question travels in the parent's `handback` as an open question. The claim → Judge protocol applies only when criteria or a Definition of Done are present. **A session holds an execution slot only while a turn is executing** (*founder decision, round 9*): a waiting parent holds none, so a parent at a cap of one cannot starve its own queued child.

**A delegation may carry its own goal** (*founder decision, round 3*). `delegate(action="run", goal=…)` attaches acceptance criteria and a Definition of Done to the steered session — recorded as the session's `GoalRef`, a field `LifecycleRecord` already has — and the same claim → Judge loop that governs tasks then applies: the Judge adjudicates exactly as for a task, and the verdict reaches the steering session through D3's completion wake. **No goal is the default.** The delegation prompt tells the parent when a goal is worth setting — multi-step work, or work it must verify before relying on it — and to leave it off for a quick lookup or a single action (*founder decision, round 8*; one sentence in the prompt text WP-C owns, no new mechanism). A parent's own active goal is **not** inherited by a child in any form — neither as a loop nor as injected context; only an explicit `goal=` on the delegate call gives a child a goal. The parent's goal loop judges the parent.

Prerequisites rev 1 missed: the launcher has no criteria requirement (`tools/task.go::validateRequest` requires criteria for *tasks* only); `task_assignee_readiness.go::TaskAssigneeCannotFinish` — which can reject an agent denied `goal_claim` — is not applied to plain steered sessions; the run loop gains an explicit completion disposition alongside its error, terminal, scratchpad and claim paths (`task_run_loop.go`), and `noClaimSteeringPrompt` is reached only for judged work.

### D7 — A steered session is a normal session on screen: the existing side panel, pill and sidebar show it

*Founder decisions, rounds 1 and 6: reuse what exists; the parent's chat shows nothing of the child.* On screen a steered session behaves exactly as a task session does today, and every surface it needs already exists:

| Surface | Exists today | Change |
|---|---|---|
| Parent chat | the one line the `delegate` tool call produces | nothing else — a child's steps, narration and output never render there |
| Side panel (`src/components/chat/ActivityPanel.tsx`, fed by `src/hooks/useRunningActivity.ts`) | lists the active session's child spans from `subagent_start` / `subagent_end` with a status dot and elapsed time | each row gains **one short status line** and **an open control** |
| Status line | the contract exists — `subagent_message` (`kind: progress`, `text`) and `subagent_state` (lifecycle state), defined by ADR-053 "Unified goal / plan / subagent system" — with **no emitter in Go and no consumer in the SPA** today | wire what was designed: emitted when a child calls `message_parent` (progress, checkpoint, blocker, question) or changes lifecycle state; rendered as the row's line. This is #755, delivered without a new frame type |
| Pill | the existing tray grammar; today `useRunningActivity.ts` computes `runningCount` over agent spans **and background shell jobs together** | the grammar stays; the count's source changes (WP-E): a separate selector counting the **open session's direct agent children in `running`**, excluding shell jobs (*founder decision, round 8* on scope); a grandchild counts in its own parent's pill when that parent is opened |
| Sidebar (`src/components/sessions/SessionTree.tsx`) | nests children via `GET /sessions?parent_session_id=` and opens them | none |
| Watching a child | open its session; single-session attach and per-session replay (`websocket_replay.go::loadReplay`) | none — a child streams only in its own open session; nothing streams that nobody is looking at |

**The one removal.** ADR-057 D2(a) stamped a child's frames with the root's session id so the browser would file them under the parent's span — the trick that made a child invisible in its own view and let it leak. Removed: every frame carries **its own** session id; the workaround field `producing_session_id` is deleted from the three Go payloads in `pkg/agent/events.go`, their readers in `gateway/websocket_forward.go`, and all **seven** contract files that carry it (`ToolResultProjectionFrame`, `ToolApprovalRequiredFrame`, `SubagentEndFrame`, `GoalStatusFrame`, `LoopStatusFrame`, `TaskStatusChangedFrame`, the `asyncapi.yaml` copies). `subagent_start` / `subagent_end` stay exactly what they are — the parent's own lifecycle events about its child — and `subagent_start` gains one optional field, `child_session_id`, so the open control knows where to go; its span key is the originating tool call for **both** fronts, so a `create_task` child has a row too. Delegation-specific stream shadowing (`websocket_streamer.go::isShadowStream` on `parentSpawnCallID`) and the nesting of child steps into parent transcripts (`replay.go::emitNestedToolCalls`) become dead and are deleted. No projection, no multi-session subscription, no new frame type.

**Status lines persist** (*founder decision, round 7, closing #755's open question*): all four sub-agent frames — `subagent_start`, `subagent_state`, `subagent_message`, `subagent_end` — are persisted as events in the **parent's transcript** at the moment they happen (today `start`/`end` are only rebuilt from tool calls at replay, and the other two are never produced), keyed by the originating tool call from the edge's `origin`, so the since-cursor replay (`websocket_replay.go::loadReplay`, which reads the transcript and nothing else) returns them after a reload or a restart with no new store. `replay.go::buildSubagentStart` learns `create_task` alongside `delegate`. Owner: WP-B (persist), WP-E (render).

**A child's approval** (*round 7, closing #658's remainder*) already reaches the operator: `gateway/ws_tool_approval.go::broadcastToolApprovalRequired` fans the request out to every browser of the account, and the modal shows it for the active workspace — which the child inherits (founder decision 2026-09-14, commit `0c79af19`); the chat being open is not a condition. The child's side-panel row additionally reads "awaiting approval: <tool>" from the browser's existing approval queue (`src/store/toolApproval.ts`, keyed by session id). No new frame.

**ADR-057 FR-047 is superseded.** That requirement deleted the earlier, emitter-less `subagent_message` / `subagent_state` surface (commit `2337e864b`) and installed a guard, `src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts`, asserting zero references. This ADR brings the surface back **with** its emitter; WP-E retires the guard in the same commit that adds the consumer.

**Viewing authority.** *Founder decision, round 2 Q4:* **any authenticated operator may view any session.** This records the current effective behaviour (`loadReplay` does not check the viewer against session ownership) as an explicit, accepted assumption of single-principal deployment (R7). Workspace-scoped visibility is a future decision with one hook: the open control and the attach path are where a check would go.

### D8 — Cancel cascades along the durable edge, and beats a queued re-entry

**Terms used here.** A *Stop marker* is a note written on a session's own lifecycle record saying "stopped, at this time, in this generation, by whom". A session's *generation* is a counter that moves only when a stopped session is revived by a newer instruction — not on ordinary re-entries. A *reservation* is the check every dispatch makes against the session's own record before it starts a turn: if the record carries a Stop marker for its current generation, or the wake being dispatched was written for an older generation, or a turn is already registered, nothing starts. The *pre-arm latch* is the existing 5-second window in which a Stop pressed just before a turn registers still catches that turn. **The model:** Stop is **one operation under the stopped node's cascade lock** — enumerate the subtree, stamp the marker on the node and on every reachable non-terminal descendant's own record (one atomic write each), cancel each live turn **with the generation just stamped**, then enumerate once more to catch a child published meanwhile. The cancel carries its generation because the turn registry refuses a cancel aimed at a generation it is not running — a revival that landed between the stamp and the cancel keeps its new turn and is reported as skipped. Each session is judged by its own record, so siblings are independent and a Stop on a middle node B stamps B's subtree and leaves A and the root untouched; a terminal descendant is skipped without a write, because terminal records are immutable (`lifecycle.go::persistLocked`). Two concurrent dispatches of the same session are resolved by a compare-and-set turn registration (`registerTurnIfAbsent`, replacing today's plain map store). Every session that steers has a record (D1, round 9), so a root chat's Stop is as durable as a child's. The launcher publishes a child under its parent's record lock and stamps it at launch if the parent already carries a marker, so a launch can never slip between the cascade's check and its enumeration. Revival: a steering message or answer newer than the marker — or a follow-up on a terminal session, which already bumps the generation today — increments the session's generation; the old marker names an older generation and is inert; a wake written before the revival is refused as stale. Stop and revive on one record are serialised by the record's write lock in arrival order — the later instruction wins, which is what the operator asked for. The published interface is landing order I-6 (`steer.Canceller`: `CancelSubtree`, `Revive`; the reservation is internal to the agent package).

Root Stop reaches every descendant by walking steered-by edges in the lifecycle store. *Founder decision, round 2 Q7:* a Stop cancels **running work and any queued re-entry, for the whole generation** — nothing in that tree starts again after the Stop. Mechanism: a re-entry **reserves before dispatch** against its own record, and the pre-arm latch (`cancel_prearm.go::armCancelOrFindActiveTurn`, `turnImminentForIdentity`) treats a queued wake for a descendant as imminent under the **stopped node**, not only under the child's own worker — rev 1 assumed a derived id alone preserved the latch; it does not. Multiple pending descendants are covered; a revived or unrelated session is not cancelled by an old marker. **A Stop is durable** (*founder decision, round 5*): it survives a restart, and a session stopped before a crash stays stopped after boot — unless a steering message or an answer from its parent or the human arrived **after** the Stop, in which case the session is recovered and continues as a new generation. "A stop is a stop"; only a newer instruction revives. The in-memory `routingSessionID` remains as a cache **derived from the edge's verified root on every entry** (D2). Partial walks — an unreadable branch (`cancel.go::CollectDescendantSessionIDs` returns partial results with errors; `lifecycle_index.go::ensureWarm` skips unreadable records) — are reported as partial in logs and UI, never as complete. This amends ADR-057 D4.

### D9 — One limit surface, with a before/after table

| Setting | Today | After |
|---|---|---|
| Concurrency | `SubTurn.MaxConcurrent` if > 0, else `Performance.MaxParallelAgents` (`admission.go::ResolveRootDelegationCap`) | one key: `performance.max_parallel_agents`, read through the **existing** resolver `config_defaults_apply.go::EffectiveMaxParallelAgents` (environment override → positive configured value → the existing safety backstop for zero/unset; unchanged — a raw zero is never compared against); one counter of **executing turns** across steered sessions and tasks — a session whose turn has ended holds no slot (*founder decision, round 9*); `Dispatch` decides admission atomically under one lock; `MaxConcurrent` removed |
| Depth | global `SubTurn.MaxDepth` ∧ per-edge depth (`resolveEffectiveDelegationDepth` — when the global value is zero, an explicit per-edge depth applies as is); a separate task ceiling via `SetMaxDelegationDepth` | one global ceiling key, **moved, not invented**: `performance.max_delegation_depth` (`Performance.MaxDelegationDepth`, int, default = today's `SubTurn.MaxDepth` default carried over unchanged; `0`/unset keeps today's precedence — the per-edge value applies; negative rejected at load) ∧ per-edge depth; applied by the launcher to both fronts; the task-only ceiling removed |
| Turn timeout | `SubTurn.DefaultTimeoutMinutes`; per-call `timeout_seconds` | creator-set `Limits.TimeoutSeconds` on the edge (int seconds, `0` = default); default from one moved key `performance.delegation_timeout_minutes` (`Performance.DelegationTimeoutMinutes`, default = today's `SubTurn.DefaultTimeoutMinutes` value; negative rejected at load); scope is **the session's lifetime across re-entries** (follow-up does not reset it) |
| Concurrency wait | `SubTurn.ConcurrencyTimeoutSec`; `subturn.go::defaultConcurrencyTimeout` — a launch blocks up to that long waiting for a slot | **deleted** (*founder decision, round 8*): a launch at the cap never blocks and is never refused — the launcher writes the record as `queued`, the tool result tells the parent the limit is reached, the position in the queue and that `delegate(cancel)` drops it; queued sessions start in order as slots free; the side panel shows `queued` |

Context handling is the normal path: window per ADR-066 D2; emptying on the real store. #803 is enabled, not decided; #807 (same day) decided to remove the absolute tool-result share cap and is the adjacent decision — not yet landed at this baseline (`midturn_budget.go::absoluteShareTokens` still exists).

### D10 — Deletions (with the correction)

- `subturn.go::newEphemeralSession` and the ring (D1).
- Borrowed `Channel`/`ChatID`/`SenderID`/`SenderDisplayName` in `prepareProcessOptions` (D1).
- Wait-inline, per the D4 manifest.
- The **delegation setup wrapper** around `runExternalCLISubTurn`. **The runner stays** — rev 1 proposed deleting `external_dispatch.go::runExternalCLISubTurn`, which tasks also call; it supplies workspace enforcement, cancellation wiring, deadlines and transcript/event handling and is preserved intact, with one caller.
- The `SubTurn` config block (D9).
- The unused `"subagent"` entry in `pkg/constants/channels.go::internalChannels` — other `subagent` literals are job-category values and are kept.
- Every per-site "is this a delegate?" boolean, once D3's function exists.
- The live delegate index as `list_jobs`'s source of actionability and labels (`list_jobs_sources.go::collectSubagentRows`): actionability comes from the record's lifecycle state and the label from its title; the collector excludes task-origin records before any result limit (the task collector shows those). Its filter on `LifecycleRecord.ParentAgentID` stays; the launcher stamps that field for both fronts, so a restarted steered session stays discoverable and a task-backed one appears once, not as both task and sub-agent (WP-C).
- `subturn.go` `SubTurnConfig.Async` — the internal field, always true once `executeSync` is gone — and every `false` branch (WP-A).
- The SPA's step buffering by parent call in `src/store/chat/slices/frames.ts` (`pendingByParentCallId`, `span.steps`) and its test-mode active-session fallback; no child steps arrive in a parent's bucket any more (WP-E).
- The three D11 containments — the media predicate in `deliverToolOutput`, the `SendResponse` deny and the walked-root restore in `processSystemMessage` — if landed before this ADR, removed by WP-B and WP-A as I-5 and I-3 land; nothing "temporary" survives the delivery.
- `delegate_run.go::persistLifecycle` (the launcher writes the record) and `task_executor_judge.go::notifyParentIfAllSiblingsDone` (WP-C, WP-B).
- `replay.go::emitNestedToolCalls`, and the field `producing_session_id` from all seven contract files, the three Go payloads and their readers (WP-B, WP-E).
- `turn.go::registerActiveTurn`'s plain store, replaced by the compare-and-set registration (WP-A).

### D11 — Interim containment, if any, before this lands

Three interim containments are **proposed, not landed** at the evidence baseline (`364290cb5`: `deliverToolOutput` still sends media whenever it is present; `processSystemMessage` still builds `SendResponse: true` by hand and restores no root): gate media in `deliverToolOutput` by the text path's predicate; deny `SendResponse` for a re-entered `delegate`-type session in `processSystemMessage`; and restore `routingSessionID` in the same function **from the walked root**, not from `ParentDurableKey` (rev 1's direct-parent restore left grandchildren unreachable). Whether to land them ahead of this ADR is the founder's call; a second session has offered the first two. Whatever lands is deleted by this delivery (D10) — audience (D3) and reconstruction (D2) are the permanent form of all three, and AC-3 and AC-8 prove those at depth three.

## 4. Consequences

**Positive.** One session model; one memory; one audience function; one cascade source; one external-CLI wrapper; one limit surface. The #763, #764, #765, #755, #670 class closes by construction. #775's proposed mechanism and #803's blocker are removed. The wake-up Stop defect cannot recur because wake-up no longer constructs identity.

**Negative / accepted.** Wait-inline users and prompts change (D4). The `delegate` and delegation-edge wire contracts change. The side panel gains a status line and an open control (D7). Any operator sees any session (R7). Tests are classified, not rewritten wholesale (§2.4). ADR-057 D2(a) and D4 are partly superseded; D1, D3, D7 and §2b stand.

## 5. What is not changing

ADR-032 (no inheritance of agent settings). The delegation edge vocabulary (`direct`, `task`). The Judge, criteria and plans for tasks. Kernel sandbox and tool policy. Channel adapters. External workers' inability to be live-steered (stated, not fixed).

## 6. Migration

Greenfield (ADR-057 operator decision 1). The 80 existing delegate sessions stay readable as history and are marked **non-resumable**: a pre-ADR-091 record has no edge and cannot satisfy D2, so boot-time recovery (`boot_sweep.go::sweepToFailedInterrupted`) fails any such session that was queued, parked or running at upgrade and reports it to the operator, rather than resuming it under new rules. Persisted `await` is rejected at load. Contracts regenerate per Hard Constraint #8.

## 7. Restart and recovery

Durable history did not imply durable notification in rev 1. Now: a completion or failure is written before the wake as an inbox entry whose `message_id` is the delivery identity, the wake carries that id and the recipient's generation, the parent's turn writes a consumed marker before executing, an entry still unacknowledged and unconsumed at boot is re-woken once, a re-wake for an id already consumed is acknowledged without a turn, and a wake for an older generation is refused as stale (D3, D8); boot classifies every record first (landing order I-8) and never resumes a legacy delegate or a child whose record was lost; a parked session is recoverable from its `NeedsInput` record without requiring a checkpoint (`boot_sweep.go::isNeedsInputReconstructable` today requires one that `parkNeedsInput` does not create — corrected); a session failed by the boot sweep wakes its steering session through D3's operation instead of only logging (`gateway_boot.go` hook); a stopped session stays stopped unless a newer instruction revives it. Stranded deliveries and unreadable records (`lifecycle_index.go::ensureWarm`'s report, I-9) are surfaced to the operator.

## 8. Risks

| # | Risk | Mitigation |
|---|---|---|
| R1 | D3 hides content an operator needed (#782 shape) | errors visible in the session's own view and transcript, and as a status line in the parent's side panel; AC-3 |
| R2 | the side panel's status line goes stale or names the wrong child | sourced only from that child's own `subagent_message` / `subagent_state` frames in the parent's stream, keyed by span; "last update N s ago"; AC-7 |
| R3 | a new entry path re-introduces per-path identity | identity is a function of the record; AC-2 is written against the session type |
| R4 | removing wait-inline changes prompted behaviour | manifest in D4; generated prompt verified; AC-4 |
| R5 | edge unreadable at run time | the turn refuses to run; reported; AC-2 |
| R6 | test volume | WP-G classification; retain/update/delete |
| R7 | any operator sees any session | accepted assumption of single-principal deployment (Q4); one server-side check point reserved for future scoping |
| R8 | two steering sources conflict | queue arrival order, all delivered (D5) |
| R9 | self-target recursion | depth and concurrency caps (D2) |

## 9. Work packages (input to `/plan-spec`; parallel agents, disjoint production ownership)

Production and test ownership are separate. Shared files have one named owner; other packages consume its interface.

| WP | Scope | Owns (production) | Depends on |
|---|---|---|---|
| **A — Launcher, edge, turn reconstruction, admission** | D1, D2, D6 prerequisites, D9; publishes `pkg/steer` and I-1, I-2, I-3, I-8, I-9 | `pkg/steer/**`, `pkg/agent/subturn*.go` (except `subturn_result.go`), `pkg/session/lifecycle*.go` (incl. `lifecycle_index.go` and its `Report()` accessor), `pkg/agent/turn.go` (identity fields, `registerTurnIfAbsent`, generation-aware cancel refusal), **`pkg/agent/loop_inbound.go::processSystemMessage`**, `pkg/agent/task_executor.go` (`StartTaskNow` → launcher), `pkg/agent/task_executor_run.go::processTaskDirectExternalCLI`, `pkg/config` (SubTurn fold), `pkg/agent/delegation_depth.go`, `pkg/agent/admission.go` (admission lock and loop), the wiring section of `pkg/gateway/gateway_boot.go` | — |
| **B — Audience and upward delivery** | D3, D7 (server side); publishes I-4, I-5 | the twelve boundaries (landing order §6) incl. `pkg/tools/message.go` and `pkg/agent/subturn_result.go`, `pkg/agent/async_notifier.go`, `pkg/session/message_inbox.go::Append` (admission, deterministic ids), `pkg/agent/task_executor_judge.go` (wake, `notifySourceChannel`), `pkg/askuser/registry.go`, `pkg/tools/message_parent.go`, `pkg/bus/session_message.go` (principal), `pkg/agent/session_messaging_wire.go` (principal re-verification), `pkg/agent/events.go`, `pkg/gateway/websocket_forward.go`, `pkg/gateway/websocket_streamer.go`, `pkg/gateway/websocket_replay.go`, `pkg/gateway/replay.go`, `pkg/channels/manager.go::finalizeHookStreamer` | A |
| **C — Steering surface and `delegate` front** | D4, D5, D6 | `pkg/tools/delegate*.go` (incl. the verified principal on bus events), `pkg/tools/task.go` (`buildTask` origin session and call id; launcher call), `pkg/task/task.go` (the disk-only `OriginCallID` field only), `pkg/tools/run_task.go`, `pkg/agent/task_run_loop.go`, `pkg/agent/delegation_context.go`, `pkg/agent/loop_delegation.go`, `pkg/agent/loop_env.go`, `pkg/agent/loop_wire.go`, `pkg/coreagent/seed.go`, `pkg/tools/list_jobs_sources.go` | A |
| **D — Cancel cascade, revival, boot recovery** | D8, §7; publishes I-6 | `pkg/agent/steering.go` (incl. the drain's consumed marker), `pkg/agent/cancel.go` (`CancelSubtree`, generation-carrying cancel), `pkg/agent/cancel_prearm.go`, `pkg/agent/plan_engine.go` (cancel), `pkg/agent/boot_sweep.go` (classification, repair, re-nudge), the boot-hook body in `pkg/gateway/gateway_boot.go`, `pkg/gateway/websocket_cancel.go`, `pkg/gateway/rest_sessions.go` | A |
| **E — Contracts and SPA** | D7 (client: consume the existing `subagent_message` / `subagent_state` frames in the side panel row, add the open control; bucket frames by own id), D4 contract | `contracts/`, generated clients, `src/store/chat/**`, `src/components/chat/**` (incl. `ActivityPanel.tsx`), `src/hooks/useRunningActivity.ts`, `src/lib/subagentStatus.ts`, `src/lib/toolVisibility.ts`, `src/lib/__adr057__noSubagentMessageOrStateReferences.test.ts` (retired) | A (edge on the wire), B (frames emitted) |
| **F — Residual audit and closure** | D10 leftovers, AC-13 | `pkg/constants/channels.go`, `pkg/agent/external_dispatch.go` (caller consolidation), docs, the guard script, `scripts/adr091-issue-closure.sh` (post-merge, outside the CI guard pattern) | A–E |
| **G — Test fixtures, classification, cross-package suites** | all ACs | the shared two- and three-level delegation fixtures; the retain/update/delete classification of existing tests; the integration and E2E suites that span packages. **Each WP writes its own unit tests with its code, using the `test-driven-development` skill** (*founder decision, round 4*) — WP-G never gates another WP. | runs alongside each WP |

## 10. Verification requirements

| # | Requirement |
|---|---|
| AC-1 | (D1) A launched steered session's record carries the creator's `workspace_id`, a non-empty `title`, the edge with a verified root and its origin, and the derived `ParentSessionID`, `Owner` and `ParentAgentID`; the steering session gets an `ordinary_root` record if it had none; a creator with no workspace yields a child with none; empty label and task text refuse the launch; a failed mandatory write leaves no session and no lifecycle record. Verified on disk and read back after the stores are closed and reopened. |
| AC-2 | (D2) A steered session re-entered by every path — wake, follow-up, boot — runs with identity from its edge; a record with an unreadable or invalid edge does not run and is reported. Asserted against the session type, with a three-level delegation. |
| AC-3 | (D3) A steered session publishes nothing to any user-facing address at all **twelve** boundaries (landing order §6), in its first turn and after re-entry, and each boundary is proven to have been reached through the injected observer; **control:** the child demonstrably ran, its own transcript and view received its output, and a **real failed tool result** is visible there and as a line in the parent's side panel. Includes an external-channel capture, the `message` tool with real channel ownership, and a bus event whose principal is a sibling being refused. |
| AC-4 | (D4) `async:false` is rejected by argument validation; a real generated system prompt contains no await advertisement; persisted `await` is rejected at load; no code path blocks a parent turn on a child; a parent needing a child's answer is woken by completion. |
| AC-5 | (D5) Each action succeeds for the parent, a grandparent and the human operator; is rejected for a sibling and an unrelated root; two steering sources are delivered in arrival order. `respond` resumes a parked session; external sessions reject `steer` and questions with a named error. |
| AC-6 | (D6) A criteria-free session completes only with a non-empty final answer and a quiet subtree; an empty answer is persisted `failed` and delivered as `error empty_answer:`; parked, interrupted, timeout and answered-with-a-running-descendant are each persisted in the stated lifecycle state and delivered as the stated existing message kind, validated against the generated contract; the last running child's completion completes a waiting parent and a parked descendant's question travels in `open_questions`; judged work still reaches the Judge and a `not_met` verdict with evidence validates against the extended `goal_status`; an agent denied `goal_claim` can complete plain work; a delegation launched with `goal=` runs the claim → Judge loop and its verdict reaches the parent through the wake; a delegation launched under a parent with an active goal, without `goal=`, carries neither a goal nor goal context — proven on the assembled model input; a waiting parent holds no execution slot. |
| AC-7 | (D7) A running steered session opens from the side panel and streams live in its own view; the parent's chat shows only the `delegate` line; the side panel shows each child with state (including `queued`) and a one-line status that updates when the child reports progress or changes state; the pill count equals the number of the open session's direct children in `running`; a grandchild appears in its own parent's side panel; a `create_task` child has a row; reload, restart, a completed child and two competing turns in one session replay correctly from the parent's transcript. |
| AC-8 | (D8) Stop stamps a durable marker on the stopped node and every reachable non-terminal descendant as one operation; it reaches a re-entered child and one whose wake is queued but not started; a cancel carries the stamped generation and a revival that landed in between keeps its new turn (reported as skipped); a stale wake after revival is refused; a launch racing the cascade is never left unstamped; a terminal descendant is skipped without a write; an unreadable branch yields a reported partial Stop with one line on the originating channel; a Stop on a middle node leaves its ancestors alone; every stamped session stays stopped across a restart. `armCancelOrFindActiveTurn` blocks a real enqueue. |
| AC-9 | (D9) Concurrency, depth and timeout each read from exactly one configuration key; concurrency through the existing effective resolver (zero/unset → today's backstop; environment override honoured); two launches against one free slot yield one `running`; the before/after table's precedence and invalid-value behaviour are asserted. |
| AC-10 | (D10) The ring, the borrowed address, the delete manifest items and the delegate index dependency are absent; the external runner is present with one caller; `list_jobs` shows a restarted steered session once. |
| AC-11 | (D11) Any interim containment that landed before this ADR is gone after it (`loop_inbound.go` contains no hand-built `SendResponse` or `routingSessionID`; the media path asks the injected `steer.AudienceResolver`), and the three properties they protected — media contained, re-entered final reply contained, root Stop reaching a re-entered child — hold at depth three through AC-3 and AC-8. |
| AC-12 | Reachability (Definition of Done): an operator finds a running steered session in the side panel, opens it in the real UI and watches it work; a three-level delegation started in one agent's chat adds **nothing to that chat beyond the `delegate` tool line** and the parent's own replies. |
| AC-13 | Closure: #658, #614, #670, #755, #763, #764, #765 are closed by hand with a comment citing the merging PR or commit (release-branch PRs do not auto-close); #784 and #803 stay open, each with a one-line comment naming the ADR section that says why. |

Delivery is stated in two lines, never merged: *code correct and tested*; *reachable by a user/agent*.

## 11. Open questions — resolved

| # | Question | Decision (2026-09-21) |
|---|---|---|
| Q1 | Delegation = board task, or a lower-level launcher? | **Launcher** (D1) |
| Q2 | Self-target; which mode? | **Self-target allowed for both**; modes unchanged (D2) |
| Q3 | Who may steer/respond/cancel? | **Any ancestor and the human** (D5) |
| Q4 | Who may view a child? | **Any authenticated operator** (D7, R7) |
| Q5 | Wake per child or after all? | **Per child** (D3) |
| Q6 | What is done? | **Non-empty final answer + quiet subtree** (D6) |
| Q7 | Stop scope? | **Running + queued re-entry, whole generation** (D8) |
| Q8 | External CLI workers? | **Limits stated; out of scope** (D5) |
| Q9 | Human talking to an open child? | **It is steering; the edge is unchanged** (D5) |
| Q10 | Existing sessions? | **History only, non-resumable** (§6) |
| Q11 | Keep the name `delegate`? | **Yes** (D5) |
| Q12 | May `delegate` carry a goal? Default? | **Yes, optional; default none** (D6) |
| Q13 | Does a child inherit the parent's active goal? | **No — nothing, unless `goal=` is set** (D6) |
| Q14 | Who adjudicates a delegation's goal? | **The Judge, as for tasks; verdict via the completion wake** (D6, D3) |
| Q15 | Title when label and task text are both empty? | **Refuse the launch** (D1, WP-A) |
| Q16 | Keep `ParentDurableKey` as an alias during rollout? | **No — removed in the same delivery** (D2) |
| Q17 | Does a Stop survive a restart? | **Yes; only a newer instruction revives the session, as a new generation** (D8) |
| Q18 | May a steered session use the `message` tool? | ~~Same rule as a task session today; nothing new~~ — **superseded by Q26 (round 8):** own session only |
| Q19 | Where do a child's steps show? | **Only in its own session when opened; the parent chat shows the `delegate` line; the side panel shows children with state, a one-line status and an open control; the pill shows the running count** (D7) |
| Q20 | When does `delegate(run)` return? | ~~After the record is written, before the child starts~~ — **superseded by Q31 (round 9):** as soon as launch and dispatch return; the child may already be running |
| Q21 | A partial Stop on a headless channel? | **One-line notice to the channel the Stop came from** (D8, WP-D) |
| Q22 | #614 — `delegate status` cannot tell "quiet" from "cannot report"? | **In scope: `status` answers from the record and inbox, not from streaming** (D5, WP-C) |
| Q23 | An unattended child's approval request? | **Already covered by the workspace-scoped modal (2026-09-14 decision); the side-panel row also shows it from the existing approval queue** (D7, WP-E) |
| Q24 | Do status lines survive a reload? | **Yes — the four sub-agent frames are persisted as events in the parent's transcript and replayed; no new store** (D7) |
| Q25 | Fill the 47-file test classification before the grill? | **No — it is WP-G's first deliverable at CP-0** (§9) |
| Q26 | Can a steered session `message` the root chat (webchat is shared)? | **No — its `message` tool reaches its own session only; parent contact is `message_parent` and the steering surface** (D3, WP-B) |
| Q27 | Does progress wake the parent? | **No — stored and shown, as today** (D3) |
| Q28 | What happens at the concurrency cap? | **Queued, with a notice to the parent, cancellable; starts as slots free** (D9, WP-A, WP-C) |
| Q29 | What does the pill count? | **Direct children of the open session** (D7, WP-E) |
| Q30 | Does the parent get guidance on when to set a goal on a delegate? | **Yes — one sentence in the delegation prompt; no goal remains the default** (D6, WP-C) |
| Q31 | Must the child wait to start until the parent's tool result is saved? | **No — `delegate(run)` returns as soon as launch and dispatch return; the child may already be running** (D1; supersedes Q20) |
| Q32 | Does a root chat session get a lifecycle record? | **Yes — written by the launcher at its first delegation** (D1, D8, I-8) |
| Q33 | What does the concurrency cap count? | **Executing turns; a parent waiting for children holds no slot** (D6, D9) |
| Q34 | What is an empty final answer? | **A failure — persisted `failed`, delivered as `error empty_answer:`** (D3, D6) |
| Q35 | How does a Judge verdict reach the parent? | **The existing `goal_status` kind, extended minimally: parent-bound direction, `not_met`, evidence** (D3, WP-E) |

## 12. Relationship to other decisions

- **Compiled publication policy** (no ADR yet). D3 makes audience a stated fact of the session and enumerates the boundaries that policy must govern, including typed error frames. Identity first, policy second.
- **#803 / context limits** — D1 removes the ring; decided there.
- **#784 / hand-back content** — transport is fixed by D3; content-level quoting stays separate.
- **#772 / PR #805 (merged)** — the typed timeout frame gains the `code` value `delegated_task_limit` with identifiers inside `message`, and `emitSubTurnIterationLimitNotice` writes an identified notice to the root transcript; both are D3 boundaries and content classes the later policy will classify.
- **ADR-057** — D1, D3, D7, §2b stand; D2(a) and D4 amended (D7, D8); its "one inherited field" is now the derived cache of the edge.

## Appendix A — Disposition of the rev-1 grill findings

| Finding | Landed in |
|---|---|
| **C1** authorization gates not equivalent | D2 — differences stated; self-target allowed for both (founder); mode unchanged; revocation semantics; AC-5 |
| **C2** D10 deletes the shared runner | D10 and §2.1 corrected — one runner, one wrapper; preservation requirement |
| **C3** neither wake establishes the parent identity | D3 — one upward-delivery operation; per-child wake; producer/recipient; AC-3 |
| D5 cannot call `StartTaskNow` | D1 — launcher primitive; `StartTaskNow` stays task-side; atomic mandatory writes |
| audience function lacks identity at boundaries | D3 — the boundaries enumerated (twelve, landing order §6) incl. streaming, direct messages, task notifications, error frames, question cards |
| edge leaves ownership/hierarchy/question guards behind | D2 — edge canonical; `ParentSessionID` and `Owner` derived; question relay in D3/D5 |
| projected viewer not authorized | D7 + R7 — founder chose any-operator visibility; one server-side check point reserved |
| projection needs transport/replay decision | **Moot** — D7 (round 6): no projection, no multi-subscription; ordinary single-session attach and replay; delegation shadowing removed, stream ownership retained |
| derived root does not preserve the pre-arm latch | D8 — reservation before dispatch against the session's own record; latch treats a queued descendant wake as imminent under the stopped node |
| durable cascade lacks integrity rules | D2 integrity rules; D8 partial-Stop reporting; AC-8 |
| D11 restores the direct parent | D11 — walked root |
| direct-parent authority contradicts ancestor walk | D5 — any ancestor + human (founder) |
| "every steered session" over-promises for external CLI | D5 — limits stated; Q8 |
| D6 misses completion prerequisites | D6 — validation, readiness, run-loop disposition; AC-6 |
| restart can strand the parent | §7 — durable wake with ack/dedupe; parked recovery without checkpoint; old sessions non-resumable |
| D9 undefined surviving limits | D9 — before/after table; AC-9 |
| wait-inline leaves prompts and policy | D4 — deletion manifest incl. generated prompt and `allow_blocking_question`; AC-4 |
| `list_jobs` relies on the old index | D10 — edge-based discovery; AC-10 |
| ACs omit decisions / permit false greens | §10 — one AC per decision, controls, AC-12 reworded |
| work packages overlap / omit files | §9 — production/test split; named owners for `processSystemMessage`, task executor, async notifier, question registry, `list_jobs`, replay |
| causal summary overstates | §1 narrowed — runtime workspace inherited; #775 hypothetical; `human` ownership for top-level delegations; follow-up exists |
| blast radius unjustified | §2.4 — commands, counts, retain/update/delete |

## Appendix B — Founder decisions ledger

| Round | Decision | Landed in |
|---|---|---|
| 1 | A sub-agent is a session steered by another session | whole ADR |
| 1 | Wait-inline removed | D4 |
| 1 | Sub-agent sessions belong to the parent's workspace; open in chat like task sessions | D1, D7 |
| 1 | Keep `delegate`; expose steering on tasks too | D5 |
| 2 | Lower-level launcher, not a board task | D1 |
| 2 | Self-target allowed for tasks and delegation; recursion bounded by caps | D2, R9 |
| 2 | Any ancestor and the human may steer | D5, R8 |
| 2 | Any authenticated operator may view any session | D7, R7 |
| 2 | Wake per child | D3 |
| 2 | Done = non-empty final answer + no running descendants | D6 |
| 2 | Stop covers running work and queued re-entry, whole generation | D8 |
| 2 | External CLI limits stated, out of scope | D5 |
| 3 | `delegate` may carry an optional goal; default none | D6 |
| 3 | A child inherits nothing from the parent's goal — no loop, no context | D6 |
| 3 | The Judge adjudicates a delegation's goal; verdict via the completion wake | D6, D3 |
| 4 | Implementers write their own tests with the `test-driven-development` skill; WP-G owns fixtures, classification, cross-package suites | §9 |
| 4 | Human steering of an open child needs no new UI — writing into the open session already steers it | D5 |
| 5 | Empty label and task text refuse the launch | D1 |
| 5 | `ParentDurableKey` deleted in the same delivery; every reader (15 files) moves with it | D2 |
| 5 | A Stop survives a restart; only a newer instruction revives, as a new generation | D8 |
| 5 | `message` tool from a steered session: same rule as a task session; nothing new — **superseded in round 8:** own session only | D3 |
| 5 | Progress wakes coalesced within 500 ms — **superseded in round 8:** progress never wakes the parent; it is stored and shown | D3 |
| 6 | No child steps in the parent chat; side panel lists children with state, a one-line status and an open control; pill shows running count; a child streams only in its own open session | D7 |
| 6 | Reuse, do not reinvent: the status line is the ADR-053 `subagent_message` / `subagent_state` contract, wired at last; the side panel, pill, sidebar nesting and session opening are the existing surfaces; the only additions are one optional field on `subagent_start` and the row's line + open control. Likewise the launcher is the task launcher generalised, upward delivery is `message_parent`'s inbox-then-wake path made the only one, and the cascade is the existing durable walk + pre-arm latch scoped by generation | D7, D1, D3, D8 |
| 6 | `delegate(run)` returns after the record is written, before the child starts — **superseded in round 9:** returns as soon as launch and dispatch return; the child may already be running | D1 |
| 6 | Partial Stop on a headless channel sends a one-line notice to the originating channel | D8 |
| 7 | #614 folded into WP-C: `delegate status` from the record and inbox, never from streaming | D5 |
| 7 | A child's approval: keep the workspace-scoped modal as is; the side-panel row also shows "awaiting approval" from the existing queue | D7 |
| 7 | Status lines persist: the sub-agent frames are persisted as events in the parent's transcript and returned by the since-cursor replay | D7 |
| 7 | ADR-057 FR-047 superseded; its guard test retired by WP-E with the consumer | D7 |
| 7 | The 47-file test classification stays a CP-0 deliverable of WP-G | §9 |
| 7 | The delivery ends by closing every issue it resolves, with a citation, and stating why the others stay open | AC-13 |
| 8 | A steered session's `message` tool reaches its own session only; it talks to its parent through `message_parent` and the steering surface (closes the webchat hole the re-grill found) | D3 |
| 8 | Progress is stored in the inbox and shown; it never wakes the parent (today's rule kept) | D3 |
| 8 | At the concurrency cap (`Performance.MaxParallelAgents`) a launch is written as `queued`, the parent is told in the tool result and may cancel it; queued sessions start in order as slots free — no blocking wait, no refusal | D9, D1 |
| 8 | The pill counts the direct children of the open session; a grandchild counts in its own parent's pill | D7 |
| 8 | The delegation prompt gives one sentence of guidance on when to set a goal on a delegate (multi-step or must-verify work: yes; quick lookup or single action: no); no goal stays the default | D6 |
| 9 | `delegate(run)` returns as soon as launch and dispatch return; no ordering hook between the parent's tool result and the child's start | D1 |
| 9 | A chat session gets an `ordinary_root` lifecycle record at its first delegation, so its Stop is durable and a lost child record is never mistaken for a root | D1, D8 |
| 9 | The concurrency cap counts executing turns; a parent waiting for its children holds no slot | D6, D9 |
| 9 | Consolidate rather than patch: the landing order restated as one contract, every spec rewritten against it, then grill #4 | process |
| 10 | An empty final answer is a failure (`failed`, `error empty_answer:`), never `completed` | D3, D6 |
| 10 | A Judge verdict travels as the existing `goal_status` kind extended minimally (parent-bound direction, `not_met`, evidence); no new kind, no new frame | D3, WP-E |
| 10 | No fifth grill: the rev-4 findings are applied and the cycle ends here | process |

## Appendix C — Rev-2 grill findings and their disposition

Re-grill of rev 2 (Codex `gpt-6-astra`, 2026-09-22; [`review-r2.md`](ADR-091-steered-sessions-replace-subagents-review-r2.md)): BLOCK, 2 critical, 16 major, 2 minor. Every finding is dispositioned below; founder decisions taken in round 8 are in Appendix B.

| # | Finding | Disposition |
|---|---|---|
| C1 | `message` tool bypasses the audience rule (webchat is shared) | **Fixed — founder decision, round 8:** a steered session's `message` tool reaches its own session only; every other target refused; tested with real ownership (D3 row, FR-B-009, test 3b) |
| C2 | `ReserveDispatch(root, generation)` cannot scope siblings, middle-node Stop or automatic re-entries | **Fixed:** the unit is the session's own record; Stop markers stamped by the cascade; generation moves only on revival; root chats without a record handled (D8 "Terms", I-6, FR-D-002/004/009, tests 13–16) |
| M1 | Published interfaces need unpublished dependencies (import cycle; unexported dispatch; fixture) | **Fixed:** I-2 is an injected `steer.SessionLauncher` interface (the `SubTurnSpawner` / `RegisterGatewayRunner` pattern); `Label`, `LaunchResult`, errors, `WakeInput`, `Principal` defined; I-7 takes the launcher; CP-0 publishes every interface compiled, with stubs |
| M2 | Task adoption has no creation-and-start sequence | **Fixed:** FR-A-010 is a MUST; `StartTaskNow` launches when `Task.SessionID` is empty and dispatches otherwise; agent-created tasks carry the steering session, human/schedule/plan tasks are ordinary roots (I-8); test 9d |
| M3 | Completion outcomes do not map to persisted states | **Fixed:** outcome table in I-5 (existing eight states only; answered-with-children-running stays `running` with no upward event until quiet); FR-C-008 |
| M4 | Inbox de-duplication is not exactly-once wake delivery | **Fixed:** `message_id` is the delivery identity, travels in the wake; the parent's transcript records consumed ids; a re-wake for a consumed id is acked without a turn (D3, §7, I-3 `WakeInput`, FR-B-002, test 10b, B-4, SC-B-2) |
| M5 | Inbox and wake limits can suppress completion | **Fixed:** terminal kinds always admitted and always wake; caps and rate apply to `progress` only; suppression reported, never "delivered"; progress never wakes (**founder decision, round 8**) (D3, I-5, FR-B-010, tests 10c/10d) |
| M6 | Persistent status lines have no server replay contract | **Fixed:** `subagent_message` / `subagent_state` persisted as parent-transcript events replayed by `loadReplay`; `child_session_id` written into the `delegate` tool-call result for `buildSubagentStart` (D7, FR-B-011, test 10e) |
| M7 | The pill cannot count running grandchildren | **Fixed — founder decision, round 8:** the pill counts direct children of the open session (D7, FR-E-005, E-3) |
| M8 | Missing-session-id rule would discard global frames | **Fixed:** the rule applies to `SESSION_SCOPED_FRAME_TYPES` only; global frames preserved as controls (FR-E-002, edge cases, BDD) |
| M9 | Missing edges ambiguous (ordinary vs legacy) | **Fixed:** I-8 record classification with six classes consumed by audience, reconstruction and the boot sweep (FR-D-007, WP-B edge case, tests 9e/17) |
| M10 | Ownership and checkpoints need coordination outside the interfaces | **Fixed:** `gateway_boot.go` → D, `events.go` → B, the external wrapper → A, `message.go` rule → B; CP-0 publishes every interface as compiled code with stubs; WP-E lists the lifecycle-edge, Stop-report and run-result wire changes |
| M11 | Unchanged job collector duplicates task rows | **Fixed:** `collectSubagentRows` excludes task-origin records before result limits; contradictory row removed (FR-C-010, US-5/AS-2, test 19) |
| M12 | Surviving configuration unnamed | **Fixed:** D9 names each key, type, default carry-over, zero meaning and rejection; the concurrency wait is deleted — at the cap a launch is queued with a notice (**founder decision, round 8**) (FR-A-008/011, FR-C-012) |
| M13 | A test required "unmodified" instantiates the deleted ring | **Fixed:** `async_child_publication_test.go` classified *update (setup only)*, assertions unchanged, owner WP-A (WP-G appendix, WP-B DoD) |
| M14 | Audit commands count test calls | **Fixed:** every audit grep scans production code only, states an exact count, and treats grep's no-match exit as the expected result (WP-F constraints, FR-F-006) |
| M15 | Mandatory decisions can stay false while mapped tests pass | **Fixed:** parent-goal isolation (FR-C-013, test 17), retention by actual pruning (FR-A-012, test 9b), Q20 ordered start/return (test 16), twelve boundaries each actually invoked (test 3), Q21 notice (FR-D-010, test 16), AC-13 verified by the issue-closure check (FR-F-005), missing BDD scenarios added |
| M16 | Sibling-wait kept as an option | **Fixed:** removed; `notifyParentIfAllSiblingsDone` deleted (D3, FR-B-003) |
| m1 | Inventory and baseline contradictions | **Fixed:** 49-row classification arithmetic; #807 stated as decided-not-landed; header says eight rounds; "nested view" wording removed |
| m2 | Specialist vocabulary unexplained | **Fixed:** "Terms used here" in D8 defines Stop marker, generation, reservation, pre-arm latch; I-5/I-6 restated in plain words |

## Appendix D — Rev-3 grill findings and their disposition

Third grill (Codex `gpt-6-astra`, 2026-09-22; [`review-r3.md`](ADR-091-steered-sessions-replace-subagents-review-r3.md)): BLOCK, 3 critical, 19 major, 2 minor. It judged 17 of the 20 rev-2 dispositions incomplete because patching had left superseded sentences beside their replacements. Rev 4 is a consolidation — the landing order restated as one contract, every spec rewritten against it — with the founder's round-9 decisions. Each rev-3 finding:

| # | Finding | Disposition in rev 4 |
|---|---|---|
| F01 | Old `message` permission still instructed | **Fixed:** Q18 and Appendix B round 5 marked superseded; WP-B's ambiguity row rewritten; FR-B-009 with real-ownership controls |
| F02 | Stop permits stale work and root resurrection | **Fixed — founder decision, round 9:** every session that steers has a record (D1), so a root's Stop is durable; the wake carries the recipient's generation and a stale wake is refused (I-3, I-6); `registerTurnIfAbsent` replaces the plain map store; a launch during a cascade is stamped; Stop/revive serialised per record; a stopped recipient is not woken (D3, D8) |
| F03 | A lost lifecycle record could restore a child's user audience | **Fixed:** I-8 classifies from the lifecycle store **and** session metadata; `damaged_child` is refused, never a root; the launcher writes the root's record at first delegation |
| F04 | I-5 not consumable from the tools side; return type contradictory; human identity | **Fixed:** `steer.UpwardDeliverer` (extending `MessageParentWaker`), `Deliver(...) (Delivery, error)`, audience takes the I-8 class; the human principal is the authenticated gateway identity passed down, never constructed by a tool |
| F05 | Upward event was a second vocabulary | **Fixed:** every outcome maps onto an existing `SessionMessage` kind (`handback`, `question`, `error`, `goal_status`, `progress`, `checkpoint`, `blocker`) — I-5 table; no new kind; oversized answers truncated in `result_so_far` with the full text in the child's transcript |
| F06 | Edits outside ownership; unspecified lock; index errors | **Fixed:** `message_inbox.go::Append` → B; `websocket_forward.go` → B; `lifecycle_index.go` → A publishing I-9; reservation + registration under the record lock via A's `registerTurnIfAbsent`; cross-package integration tests owned by the package whose AC they prove |
| F07 | Delayed tasks lose the creating session; creation timing | **Fixed:** `task.Task.OriginSessionID` (existing, disk-only) set by `buildTask` (WP-C) and read by the launcher at start; one sequence in `StartTaskNow` (`Launch` then `Dispatch` when no session; `Dispatch` otherwise; no-op if terminal); creator gone → ordinary root |
| F08 | Return-before-start unenforceable | **Fixed — founder decision, round 9:** the ordering is dropped; `delegate(run)` returns as soon as launch and dispatch return |
| F09 | "Consumed" undefined; guarantee overstated | **Fixed:** the consumed marker is written before executing; the guarantee is "each upward event starts at most one parent turn"; the parent turn's own effects keep today's interrupted-turn guarantees; ordinary-root recipients covered by the same rule |
| F10 | Terminal wakes could stay suppressed; progress contradictions | **Fixed:** terminal kinds bypass `allowWake`; a live recipient turn gets the wake as a steering message; progress never wakes; the 500 ms coalescing and B-5's "1 coalesced wake" removed |
| F11 | Completion and concurrency could deadlock | **Fixed — founder decision, round 9:** the cap counts executing turns; a waiting parent holds no slot; "quiet subtree" = no descendant `queued` or `running`; parked descendants' questions travel in `open_questions`; WP-C's scenario vocabulary aligned with the persisted states |
| F12 | Status replay lacked a durable association for both fronts | **Fixed:** all four sub-agent frames are persisted as parent-transcript events keyed by the edge's origin call id, for `delegate` and `create_task` alike, including queued launches and revival; `buildSubagentStart` learns `create_task` |
| F13 | Pill had two incompatible counts | **Fixed:** direct running agent children only, via a separate selector excluding shell jobs; BDD and datasets aligned |
| F14 | Field deletion manifest incomplete | **Fixed:** `producing_session_id` inventoried in all seven contract files plus the Go payloads and readers; WP-E FR-E-011; WP-F check |
| F15 | Fixture unusable from in-package tools tests | **Fixed:** tools tests that need I-7 are external-package tests (`package tools_test`); fixture signature and hooks published; sinks enumerated |
| F16 | Zero-valued limits contradictory | **Fixed:** `TimeoutSeconds` 0 = default; global depth 0 keeps today's per-edge precedence; boundary table in WP-A |
| F17 | Job collector both unchanged and changed | **Fixed:** one instruction — `collectSubagentRows` excludes task-origin records before result limits; the contradictory rows deleted from D2, D10 and WP-C |
| F18 | Retained-unchanged test constructs the ring | **Fixed:** every retention sentence now says "assertions and controls unchanged; setup moved by WP-A" |
| F19 | Audit checks not executable or not valid oracles | **Fixed:** conventions in landing order §5; every check scoped to production code with an exact count; containment removal proven by `SendResponse:`/`routingSessionID` absent from `loop_inbound.go` and the injected `steer.AudienceResolver` present in the media path; live paths replaced by test data dirs |
| F20 | Closure check would run pre-merge in CI | **Fixed:** `scripts/adr091-issue-closure.sh` — outside the `check-*.sh` guard pattern — run at CP-7 after merge with `PR=<n>` |
| F21 | Closure verifier accepted unrelated comments | **Fixed:** requires the merging PR number **and** an `ADR-091 §` citation; open issues require the section and "stays open because"; `gh` failures exit 2; self-test with five negative fixtures |
| F22 | Acceptance claims could pass unexercised | **Fixed:** twelve boundaries everywhere with `AssertBoundaryInvoked`; AC-1 readback after reopening stores; AC-6 validated against the generated contract and persisted state; AC-11 mapped to executable checks and AC-3/AC-8; every FR has a scenario and a test in each spec's traceability table |
| F23 | Approvals/questions misdescribed as global | **Fixed:** they are session-scoped and keep their handling; the global examples are `task_updated`, `plan_updated`, `library_changed` |
| F24 | D11 described proposed containment as present | **Fixed:** D11 states the containments are proposed, not landed at the baseline |

## Appendix E — Rev-4 grill findings and their disposition

Fourth grill (Codex `gpt-6-astra`, 2026-09-22; [`review-r4.md`](ADR-091-steered-sessions-replace-subagents-review-r4.md)): BLOCK, 2 critical, 16 major, 1 minor; ten of the twenty-four rev-3 fixes verified as holding. The founder ended the grill cycle here; every finding below is applied in rev 5 without a fifth run. Founder decisions taken in round 10 are in Appendix B.

| # | Finding | Disposition in rev 5 |
|---|---|---|
| R01 | Stop stamped records, then cancelled turns — a revival could slip between; a launch could slip between check and enumeration | **Fixed:** `CancelSubtree` is one operation under the node's cascade lock; the cancel carries the stamped generation and the registry refuses a mismatch (`SkippedNewerGeneration`); a second enumeration catches late children; the launcher publishes a child under its parent's record lock (D8, I-1, I-6, FR-A-015, FR-D-001/004, WP-D tests 20–22, WP-A test 24) |
| R02 | A present record whose edge was lost could become a root | **Fixed:** I-8 requires record and metadata to agree on every branch; `Origin` is a record-level discriminator; edge lost + metadata naming a parent = `damaged_child` (FR-A-017, WP-A test 12) |
| R03 | Audience function in the agent package, unreachable from tools/channels | **Fixed:** `steer.AudienceResolver` in the neutral `pkg/steer`, injected into every boundary host; consumer compile test at CP-0 (I-5, FR-B-014, WP-B test 33) |
| R04 | Hooks concealed interfaces and ownership | **Fixed:** `pkg/steer` holds every interface and type; fixture takes `steer.Deps` and maps each hook to a published operation; `steer.BoundaryObserver` gives the recorder a pre-decision probe; `registerTurnIfAbsent` is internal to the agent package; `Report()` accessor for the index; `subturn_result.go` excluded from A's wildcard; ordinary-root launch inputs named; `gateway_boot.go` split into A's wiring and D's hook body (landing order §2, §3, CP-0) |
| R05 | Terminal follow-up contradicted the generation rule | **Fixed:** generation also moves on a terminal follow-up (today's behaviour carried over); `persistLocked`'s terminal immutability kept; Stop skips terminal descendants (`SkippedTerminal`); `Dispatch` on a terminal record → `ErrTerminal` (I-1, I-2, I-6, FR-D-001) |
| R06 | Queue state returned before admission was decided | **Fixed:** `Launch` returns id and generation only; `Dispatch` decides atomically under the admission lock and returns the authoritative `DispatchResult`; C reports that (I-2, FR-A-011, FR-C-001/012, WP-A test 16b) |
| R07 | Delayed tasks lacked the originating call id | **Fixed:** new disk-only `task.Task.OriginCallID` set by `buildTask` (WP-C owns the field), read by the launcher into `Origin.CallID` (I-2, FR-C-005, WP-C test 22) |
| R08 | `goal_status` could not carry a verdict; `subagent_message` excluded it | **Fixed — founder decision, round 10:** the existing kind extended minimally (parent-bound direction, `not_met`, evidence); the frame's kind enum gains `goal_status`; validated against the regenerated contract (I-5, FR-B-017, FR-E-013, WP-B test 31) |
| R09 | Boot re-nudged progress; wake rules contradicted | **Fixed:** one wake-eligibility table for delivery and boot (`handback`, `question`, `blocker`, fatal `error`, `goal_status` wake; `progress`, `checkpoint`, non-fatal `error` never) (I-5, FR-B-010, FR-D-010, WP-B test 34) |
| R10 | Crash and consumption windows unclosed | **Fixed:** deterministic terminal ids; inbox append before the terminal lifecycle write; boot repairs either half; the steering-queue drain writes the consumed marker for live-turn injection; the guarantee restated as "consumed at most once, by a new turn or a live-turn injection" (I-3, I-5, FR-B-002/011, FR-D-011, WP-B tests 28–29, WP-D test 23) |
| R11 | Empty output both "not done" and `completed` | **Fixed — founder decision, round 10:** an empty answer is `failed` with `error empty_answer:` (D3, D6, FR-B-016, FR-C-008, WP-B test 27) |
| R12 | Job collector "needs no change" survived; limit-1 oracle impossible | **Fixed:** D2 and WP-C name the three changes (task-origin exclusion before limits; actionability from state; label from title); BDD uses limit 2 and a separate limit-1 assertion (D10, FR-C-010, WP-C test 17) |
| R13 | Guards would reject required code | **Fixed:** `ProducingSessionID` check scoped to `events.go` and `websocket_forward.go` (`UpwardEvent` names its field `ChildSessionID`); the media grep dropped — containment proven by WP-B's boundary-4 test; `persistToolResult` / `recordToolCompletion` media checks preserved (WP-F constraints, FR-F-003) |
| R14 | Closure verifier masked API failures and accepted `§` without a section | **Fixed:** every `gh` call's exit status checked explicitly (exit 2), section identifier required, merging PR number required, "stays open because" required; self-test with the listed negative fixtures (WP-F script, FR-F-005) |
| R15 | Traceability rows without scenarios or sufficient assertions | **Fixed:** BDD scenarios added for A-006/013/014/015, B-004/007/008, the empty answer, repair, bus authority, the negative verdict, a real tool failure in both views; E-012's assertions listed explicitly; G-004 has a story and a scenario |
| R16 | `max_parallel_agents = 0` "refused" contradicted the existing resolver | **Fixed:** `EffectiveMaxParallelAgents` reused as is; zero/unset keeps today's backstop; environment override honoured (D9, FR-A-008, WP-A test 16c) |
| R17 | Bus route could not establish caller authority | **Fixed:** `SessionMessageEvent` carries the verified `Principal`; `deliverParentToChild` re-verifies against the target's edge; a sibling naming another valid child is refused (I-5, FR-B-015, FR-C-016, WP-B test 30) |
| R18 | ADR D7 still said "none" for the pill | **Fixed:** D7 states today's mixed count and requires WP-E's separate selector |
| R19 | Source facts inaccurate | **Fixed:** `UnifiedMeta.Type`; `Generation` marked existing with changed semantics; "zero references in production code"; I-8 lists every session type |
