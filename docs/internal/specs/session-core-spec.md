Status: Draft
ADR: [Session core with an agent address book: reuse one standing session, one archive and the existing execution paths](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md)

# Feature Specification: Session core

Created: 2026-10-07.
Size: Feature. This is a specification, not implementation, testing or landing approval.
Checkpoint: behavior and contract draft; acceptance expansion, datasets and final traceability follow before hand-off.

## Overview

Mia has one main conversation in each workspace. Clicking her opens it. **+ New chat** on her agent row opens an extra conversation. Heartbeats use the main conversation; tasks normally run in real children of it, while remaining tasks with their own goals, runs and results. A message to `@Jim` asks Jim to answer in the group conversation; it does not replace Mia.

Use the existing standing-session creation, session store, instruction queue, report inbox, task executor, scheduler, Stop path and UI. Do not build parallel replacements beside them. This specification translates the recorded decisions into requirements and verification oracles; it makes no new product decision.

### Authority, interview record and scope

| Source key | Source and precedence |
|---|---|
| S-ADR | **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, linked above; original approved-decision rewrite at `b76b412f6`, dated reference/status follow-up at `b037e91f1`. Sections D1–D10, DEL-01–22 and DEL-F01–43 are normative. |
| S-LEDGER | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/CONTINUATION-20261005.md`::all founder entries on 2026-10-06/07. This is the interview record, not a missing interview file to invent. Later answers win, especially October 7 13:40, 13:50, 14:10, 14:35, 14:45, 15:10, 15:20 and 15:55. |
| S-Q | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/session-core-adr-20261007/QUESTIONS.md`::All closed questions. Q1=A, Q2=A, Q3=B, Q4=A, Q5=A, Q6=B. Their former OPEN labels are not pending decisions. |
| S-FE | **ADR-20261007 — Agent-first navigation and agent identity**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-20261007-agent-first-navigation-and-agent-identity.md`, `work/adr-frontend-navigation-20261007` at `554d21ffd`, **Proposed**. Read from that Git object; not present on this branch. D5 consumes the shared contract; D12 requires the sidebar and main-session backend to land together after a joint integration test. Layout, avatars and that ADR's unanswered frontend questions are not decided here. |
| S-RULES | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/CLAUDE.md`::Hard Constraints, Definition of Done, Contract regeneration; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/.claude/skills/plan-spec/SKILL.md` and its knowledge templates. |
| Grounding | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/AS-IS-architecture.md`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/plugin-extensibility-assessment.md`; current ADRs and code. These walkthroughs are dated; code wins on current behavior. |

The sources are already the founder-confirmed requirements. No new interview, upgrade plan, review recommendation or provider research overrides them. Storage-before-queue was a proposed build order, not a ratified delivery sequence; team-lead owns task planning. Contracts must precede their consumers regardless of implementation grouping (S-ADR::Questions answered and D10).

| Disposition | Issues and boundary |
|---|---|
| In scope | [#1211 visible refused child report and machine-readable retry hint](https://github.com/elicify-ai/omnipus/issues/1211); [#1214 steer/redirect acceptance race](https://github.com/elicify-ai/omnipus/issues/1214); [#1216 live settings wiring](https://github.com/elicify-ai/omnipus/issues/1216). Reported QA failures require reproduction in RED; this spec is not a reproduction receipt. |
| Cross-reference only | [#1212 helper snapshot](https://github.com/elicify-ai/omnipus/issues/1212), [#1213 respond correlation](https://github.com/elicify-ai/omnipus/issues/1213), [#1215 duplicate timeout notice](https://github.com/elicify-ai/omnipus/issues/1215): separate fix squad, no side fixes or new helper-context pipeline here. |
| Cross-reference only | [#1217 restart-cut waiting chat](https://github.com/elicify-ai/omnipus/issues/1217): I1 owns its fix; preserve the founder's 16:25 Interrupted/next-message continuation decision when integrating. [#1206 per-person channel sessions](https://github.com/elicify-ai/omnipus/issues/1206): later work; current channel context/control remains shared in main. |
| Excluded | Waiting/held-input restart reconstruction and live descendant-stop retry under the retained #1198 deferral; new outside-payload events; new result overflow/pagination/retry/summary workflows; changed helper memory; a new scheduler, address-book database, queue, dashboard, approval service or CLI server protocol (S-ADR::D10). |

## Existing Codebase Context

GitNexus MCP is unavailable in this session. The following are direct source reads and caller searches, **not graph results**. Impact is **Inferred, high confidence** from those searches, not runtime certification. No production symbol is edited by this document. Implementing leads repeat upstream impact analysis before editing each symbol, or record a controlled caller sweep if the graph is still unavailable.

### Symbols involved — exact reuse keys

A requirement naming a reuse key below names these existing `file::symbol` paths; it is not permission to introduce a similarly named parallel service. All paths are in the verified worktree. Required adaptations are target behavior, not claims that the code already delivers it.

| Key | Existing path extended | Current seam / required adaptation |
|---|---|---|
| E-MAIN | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/unified.go::NewHeartbeatSession`, `GetOrCreateScheduledSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/rest_workspaces.go::prepareWorkspacePutMembers`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/rest_sessions.go::computeSessionProtected` | Mature heartbeat-created random identity into the computed main pair, eager eligible membership creation and independent protection. |
| E-STORE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/daypartition.go::TranscriptEntry`, `PartitionStore.AppendMessage`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/unified.go::UnifiedStore`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/memory/window.go::snapshotWindowLocked`, `CommitWindow`, `RollbackWindow` | Integrate useful day logic into the production store. Current window snapshots read the model archive from zero. Replace that full read and destructive correction writers; do not turn on a second content store. |
| E-RETENTION | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/retention_sweep.go::RetentionSweep` | Preserve file-modification-age sweep, disabled retention and lock order; repair expired window marks and retain eligible main identity. |
| E-QUEUE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/steering.go::steeringQueue`, `pushItemScopeChecked`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/session_worker.go::sessionWorker.processTurn`, `closeSteeringWhenDrained`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/ordinary_execution_admission.go::prepareOrdinarySessionExecution` | One session runner owns acceptance through settlement. Remove competing inbox/wait/manual fallback mechanisms, retaining execution/generation fences. |
| E-LIMIT | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/config/session_messaging.go::EffectiveSteerBodyBytes`, `EffectiveSteerRatePerMinute`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/delegate.go::SetSteerCaps`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/delegate_followup.go::checkSteerCaps`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/context_budget.go::contextBudget`, `requestTokens` | Resolver/setter declarations have no production invocation at this baseline. Wire boot/reload and common ordinary intake; reuse the rate window and unchanged model estimator. |
| E-INBOX | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/message_inbox.go::MessageInboxStore.Append`, `classifyEnvelope`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/steer_audience.go::SteerUpwardDeliverer.Deliver`, `wakeOwnerOrStore` | Existing durable append, acknowledgement, dedupe and wake. Child delivery requires a real edge; peers/independent task notices use authorized adapters into its common machinery, not fake children. |
| E-STOP | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/stop_session.go::StopSession`, `StopDelegatedTree`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/stop_redirect_root.go`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/cancel.go::RequestCancel`, `CollectDescendantSessionIDs`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/revive_inbound.go::reviveRecordForHumanTurn`, `lifecycleInFlightStopFence` | Keep one Stop/redirect and genuine human revival. Session identity does not replace selected execution identity. |
| E-DELEGATE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/delegate_run.go::launchAndDispatch`, `validateRequest`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_delegation.go::buildDelegationDenyChecker`, `evalUntargetedDelegation`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/steer_launcher.go::SteerLauncher.Launch`, `launchSteered` | Normalize omitted target before the common authorization check; mature ordinary native self-delegation without a self-edge or special helper runtime. |
| E-APPROVAL | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_policy.go::inheritSessionPermissions`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/steer_launcher.go::inheritDelegatePermissions`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/agents/ToolApprovalModal.tsx::ToolApprovalModal`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/toolApproval.ts::ToolApprovalStore` | Existing grants, per-chat modifier, receiver restrictions and one approval ID/modal. MAIN children source permissions from their main parent. |
| E-TASK | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_executor.go::ExecuteTask`, `StartTaskNow`, `startTaskNowViaLauncher`, `activateTaskGoal`, `SpawnTriggeredRun`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_executor_run.go::openRun`, `closeRun`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_executor_judge.go::deliverTaskCompletionUpward` | Keep task claim, goal, attempts, limits and authoritative result. Add MAIN parentage and captured notification recipients, not another task outcome owner. |
| E-SCHEDULE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/schedules.go::scheduledRunner.pickSession`, `RunScheduled`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_trigger.go::TaskTriggerScheduler.RunScheduled`, `triggerToCronSchedule`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/cron/service.go::SessionMode`, `AddJobFull` | Existing modes and scheduler. Retain Calendar once/at_ms and RRULE timing; remove old task every/cron_expr adapters, not internal heartbeat cron. |
| E-COMMAND | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/commands/builtin.go::BuiltinDefinitions`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/commands/cmd_clear.go::clearCommand`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/commands/executor.go::Executor.Execute`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/hooks/useSlashMenu.ts::runClientSlashCommand`, `selectMentionAgent` | Change actions and execution eligibility, not just menu labels. Clear currently starts a new chat; mention currently changes its agent. |
| E-NAV | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/useSelectSession.ts::useSelectSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/session.ts::startNewSession`, `attachToSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/connection.ts::setConnected` | Reuse navigation, attach, search and existing connect/reconnect roster refresh. S-FE owns layout, not a new session registry. |
| E-ACTIVITY | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/hooks/useRunningActivity.ts::useRunningActivity`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/ActivityPanel.tsx::partitionRunning`, `ActivityRow` | Extend span-only activity with agent-task/run projection and actual tokens; preserve running/queued/waiting distinctions and existing Open controls. |
| E-RECAP | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_idle.go::resetIdleTicker`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/session_end.go::CloseSession`, `persistResponse`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/memory.go::MemoryStore.WriteLastSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/memory.go::RetrospectiveTool.Execute` | Existing idle recap writes agent memory. Keep joined retrospective stamp; no archive summary writer is added. |
| E-CLI | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/runner/driver_claude.go::Input`, `Resume`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/runner/driver_codex.go::Input`, `Resume`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/runner/driver_opencode.go::Input`, `Resume`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/external_dispatch.go::policyApproverConsent.RequestConsent` | Mature current drivers for interrupt plus actual native-conversation resume, with honest unavailable/error states. External consent is not native-popup enforcement. |
| E-DELETE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/rest_agents.go::deleteAgent`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/sysagent/tools/agent.go::cascadeDeleteAgentSessions`, `cascadeUnassignAgentTasks` | Consolidate existing differing cascades; delete owned history/memory, retain guest answers elsewhere, preserve guards and report partial cleanup. |
| E-POLICY | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/config/defaults.go::defaultToolPoliciesGeneral`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_wire.go::registerDelegationTools` | Existing catalog, global ceiling and per-agent registration. Native eligibility does not override operator Deny; no new messaging or self-helper tool. |

### Impact assessment and execution flows

| Modified seams | Direct dependencies to update/test | Indirect flows to test | Evidence / risk |
|---|---|---|---|
| E-MAIN / E-STORE / E-RETENTION | Workspace membership/heartbeat reconciliation, scheduler session selection, session REST listing/detail, window/recall/replay and deletion readers | Navigation/search, tasks/verifiers, usage and boot recovery | Controlled tracked-source caller sweep; **Inferred broad impact**, not a GitNexus HIGH verdict. |
| E-QUEUE / E-INBOX / E-STOP / E-LIMIT | Web/channel input, parent steer/respond, settlement, report wake, Stop/redirect and receipts | Same-agent parallel chats, task children, peer response and CLI resume | Direct source seams; **Inferred concurrency-sensitive impact**. No five-second workaround is approved. |
| E-DELEGATE / E-TASK / E-SCHEDULE / E-APPROVAL | Ordinary launcher, task start/fire/result, membership gates and grant inheritance | Calendar run history, plan recovery, activity/approval UI and native/external boundary | Caller searches plus source reads; **Inferred cross-tree impact**. Task run ownership must remain independent of its chat parentage. |
| E-NAV / E-COMMAND / E-ACTIVITY / frontend deletions | Existing store reducers, WS callbacks, API adapters, composer, search, activity and approvals | Reconnect, first-send delivery, non-stream answers, kickoff, ring-buffer eviction, goal/plan display | Current source mapping in S-ADR::DEL-F01–43; **Inferred shared-branch risk**. Removing a compatibility branch does not authorize dropping its current producer's behavior. |

Verified mechanisms compose the following flows; these are explanatory source flows, not invented GitNexus process/cluster names:

```text
web / connector / authorized parent / addressed peer
                 -> existing session intake and identity checks
                 -> ONE session instruction FIFO + existing runner
                 -> next safe step / next turn -> same archive and two views
helper / task outcome -> existing report inbox -> same wake/runner boundary
scheduled task -> existing cron/task dispatch -> derived mode -> task-owned result
Stop / redirect -> ONE Stop path -> selected execution fence -> same session
```

No graph cluster placement is asserted. The feature crosses session/storage, engine, tool/config, gateway/contracts and chat/navigation/calendar boundaries already identified by S-ADR.

### Available reference patterns

The optional generic Go reference index named by the user-level skill is absent from this checkout. A full tracked reference-file search found the known built-in-tool reference as a positive control and no Go-implementation or conservative-type-design reference. Do not create an infrastructure library to satisfy a missing template. The repo-specific skill and existing Omnipus code above are the applicable patterns (S-ADR::New things and why nothing existing fits).

## Contract Changes (contract-first — Hard Constraint #8)

**Backend-lead alone edits and regenerates contracts before any Go/TypeScript consumer.** This spec is the behavioral/shape requirement; it contains no parallel hand-written wire struct, guessed response object, new REST route or new WS protocol. Use existing shared schemas, the OpenAPI inline discriminated union and AsyncAPI frame families. Commit specifications and generated artifacts atomically. Current contract descriptions below are baseline facts, not the target behavior.

| Contract key | Existing source read | Required extension/removal and invariant |
|---|---|---|
| C-MAIN | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/Session.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/WorkspaceMemberConfig.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/WorkspaceMemberHeartbeat.yaml` | S-ADR::D1.1 exactly: require current Session.type; replace heartbeat type with server-only main; immutable agent_id; computed pair ID; derived protected; readOnly main_session_id on eligible MAIN member config. Delete heartbeat.session_id and mutable active_agent_id/handover semantics. Main is absent from client-create enum; workers/system/Admin have no main_session_id. |
| C-INPUT | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/MessageFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/Message.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/asyncapi.yaml`::MessageFrame, ReplayMessageFrame, SessionStateFrame, MessageStatusFrame | Carry the decided per-message source/return correlation and held/control status through current intake/status/replay surfaces. Server-authenticated identity, original session, connector instance and chat/thread remain attached to each accepted input. Extend the current entry to express chat/model/both membership and full model representation without duplicate content. Keep real message/turn/producer identities; remove old uncorrelated acknowledgements and guessed-producer branches. |
| C-INBOX | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/openapi.yaml`::SessionMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SessionMessageSteer.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SessionMessageRespond.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SessionMessageQuestion.yaml` | Extend this envelope's addressing for the already-decided peer/independent-task adapters. Preserve message_id, sender_identity, correlation_id, generation and real child parentage where applicable. A peer is not parent_to_child authority; no invented parent_session_id/edge. Typed refusal/retry information and intake outcomes belong to existing response/status surfaces. The logical information is fixed by S-ADR::D3/D7; no new transport or server-chosen reply destination. |
| C-TASK | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskRun.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskCreateRequest.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskUpdateRequest.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/ScheduleCreate.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/ScheduleUpdate.yaml` | Extend existing run records with recipients captured at start and existing scheduled-task records with the single optional isolation checkbox. Modes are derived, not user/agent-selected. Preserve task/run/goal IDs, result and outcome owners; no new stopped Task status. Real MAIN parent_session_id remains separate from task/run origin. |
| C-TIMING | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskTrigger.yaml`; current Calendar and task trigger path E-SCHEDULE | KEEP manual, once/at_ms and current recurring RRULE + anchor/time zone. DELETE task every/every_ms and recurring.cron_expr shapes/adapters/docs/compatibility tests. Remove the misleading legacy label from current once. Do not delete internal cron schedule representations used by heartbeat or invent a new one-time encoding (Q4=A). |
| C-CONTROL | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/asyncapi.yaml`::RedirectFrame, SessionStateFrame, SessionCloseFrame, SessionCloseAckFrame, AgentSwitchedFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SlashCommand.yaml`; E-APPROVAL | Reuse Stop/redirect/control receipt and one approval ID; expose acting helper/run attribution through existing approval/activity data. Remove session_close/ack, agent_switched and retired command names/aliases. Do not remove session_mode_update, which is the current Auto modifier, merely because handover is removed. |
| C-LIMIT | Existing session_messaging settings through E-LIMIT and current configuration contracts | Set steer_body=65,536 bytes and steer_rate=60/minute per authenticated sender+target. Add exactly one key: session_messaging.steer_aggregate_body=1,048,576 bytes. Retain 200 ordinary items and current model-budget fit. Update descriptions, defaults, validators and live wiring together; no settings-tree copy. |

C-INPUT session-ID bounds on all affected REST/WS fields must permit `main-session-<workspaceid>-<agentid>` while retaining path-safety and existing workspace/agent validation. Derive bounds from those existing validated IDs; do not assume UUID/128 characters or introduce hashes/short IDs. UTF-8 byte admission is not a JSON Schema character maxLength: a character-only validator must not be advertised as enforcing the byte limits.

The approved archive/view, peer-addressing, recipient, held/refusal and isolation extensions are the **logical contract shapes already in S-ADR**, not authority for extra fields. Concrete schema declarations and generated names are produced in the contract-first step, reviewed against these invariants before consumers. If those invariants cannot be represented by extending the existing records, stop and put the choice to the founder; do not create a second exchange family.

### Contract-first proof

| Gate | Required evidence before consumers |
|---|---|
| Definition | Shared schema changes referenced from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/openapi.yaml` and/or `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/asyncapi.yaml`. Keep the existing inline oneOf/discriminator wrapper for generated union compatibility. |
| Generation | Backend-lead runs `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/scripts/gen-contracts.sh`, reviews and commits generated Go, TypeScript/Zod and inbound-schema artifacts with the source specs. Nobody hand-edits them. |
| Validation | `make verify-contracts` on the candidate; valid and invalid payload controls through actual gateway/SPA validators, not only a schema text search. Prove current fields validate and removed/missing required identity cannot invoke retired behavior. Existing protocol error envelopes remain the error surface; do not invent HTTP statuses in an unrelated handler. |
| Consumption | Only generated boundary types from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/api/generated/` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/api/generated/`. Run the existing wire-type guard and real project typecheck. |

## API and Data

Use the existing session list/detail/message/history, workspace/member, task/run, schedule, command and approval APIs; their source is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/openapi.yaml`. Webchat uses the existing WebSocket protocol, not the retired SSE endpoint. Explicit session addresses stay explicit. Unreadable, unauthorized or mismatched metadata must not be treated as a missing record and silently recreate or route to a different workspace.

| Data / execution rule | Backend obligation | Frontend obligation | Authority |
|---|---|---|---|
| Main pair | E-MAIN validates both stored owner/pair and current eligibility/membership under existing session locking; main creation is independent of heartbeat enabled. Native core/custom chat targets qualify; workers, system and standalone Admin do not. | E-NAV consumes C-MAIN, never derives another stored map, chooses a latest chat as main, or mutates the owner. | S-ADR::D1/D1.1 |
| Archive and window | E-STORE integrates one content format into UnifiedStore with view membership, UTC day files and a file/byte start mark. Append order follows accepted FIFO, including delayed sources whose original timestamps fall on an earlier day; timestamp-based back-insertion is not a valid implementation. Midnight cannot split a tool group. | Existing message/replay/search projections read the same saved identity. Clear affects current display, not archived search/recall. | S-ADR::D2/D8 |
| Retention | E-RETENTION expires old day content by existing file modification age, default 90 days; disabled retention remains a no-op. Move an expired mark to the first retained complete group; show the agent expired-history information; empty same-ID window if none remains. Do not retain all main content forever. | Eligible main identity remains navigable with empty retained history. Do not label an expired history as a new conversation or a successful clear of stored data. | S-ADR::D2; Q2=A |
| Intake | E-QUEUE accepts ordinary human/parent instructions in one FIFO per actual session and retains each as a separate message. One runner owns settlement; ready batches join at safe steps or immediately after natural turn end. Provenance save and admission are one truthful acceptance boundary. | Show each item and truthful accepted/applied/held/superseded/refused state through current status surfaces; do not call an echo a save or a save model consumption. | S-ADR::D3/D6; #1214 |
| Ordinary limits | E-LIMIT checks body, authenticated sender+target rate, ordinary item count, aggregate waiting bytes and current model fit before acceptance. Joint ready content includes media/tool/pinned costs after allowed old-window sliding; accepted control content is not silently dropped. | Visible refusal identifies the limit. No successful send bubble for refused admission, no silent truncation or optimistic settings claim. | S-ADR::D3; Q1=A; #1216 |
| Reports | E-INBOX keeps current report caps/exemptions/result-delivery limits while all **accepted** helper report kinds become wake-eligible for an idle non-stopped parent. Current code couples cap exemption to wake classification; changing wake eligibility must not accidentally remove the preserved report rate/count checks. This is an adaptation inside the existing classifier/admission, not another inbox. | Surface actual rejection/not-delivered information and acting child; has_more:false alone is not a no-loss proof. No automatic retry, result-defer or overflow queue. | S-ADR::D3.1/D5; Q-R2-7; #1211 |
| Task modes | E-TASK/E-SCHEDULE derive MAIN for any MAIN-assignee task; recurring worker CONTINUE; one-time worker ISOLATED; scheduled isolation override ISOLATED for either. MAIN always beats CONTINUE. Every MAIN run has a fresh real child of the assignee's main; CONTINUE keeps the conversation but gets a new run ID. | One scheduled-task checkbox **run isolated (one session per execution)**; no mode selector. Use existing Calendar/task history views; display real parentage, not a synthetic monitor edge. | S-ADR::D5; 10:13 |
| Task admission/results | Narrow task-origin admission allows a future authorized scheduled MAIN child under a stopped main without waking that main; launch is ordered with Stop. Capture starter-MAIN main plus assignee-MAIN main at start and dedupe. Deliver brief engine header plus full stored result for each actual done/failed/stopped run, with reason. Skipped did not run; no per-retry notice. | Activity distinguishes starter monitoring from assignee/parent authority, shows MAIN once, and links stored results even when a captured recipient disappeared. Never guess a substitute recipient. | S-ADR::D5/D6; Q-R2-1/3; 10:25 |
| Peer/source addressing | E-INBOX plus existing send_message transport address same-workspace MAIN peers without a delegation edge. Message carries only addressed text/explicit material, not whole source history. Receiver keeps permissions; sender gains no steering/Stop/context/approval rights. Each answer names its sender/return correlation; missing destination is visibly refused. | Replace mention-switch action with message addressing; guest icon/name and persisted identity match replay. Owner sees request and answer; answer wakes idle non-stopped owner once, with silence allowed and no empty bubble. | S-ADR::D7; Q3=B |
| Idle/deletion | E-RECAP runs default-on recap after 30 inactive minutes, resetting on any turn/message and excluding active settlement; repeated idle episodes remain possible. E-DELETE consolidates existing entry points. Team removal hides; deletion removes owned sessions/memory while preserving guest answers labelled deleted agent. | UI deletion warns and requires two confirmations; API/tool retain existing single approval. Report partial cleanup, preserve guards, and never expose a deleted identity as a runnable guest. | S-ADR::D9; Q5=A/Q6=B |

MAIN task children inherit their **main parent's** approval state, not the starting extra chat's modifier; target restrictions and unattended immediate refusal still apply. This may be more permissive than the starting chat and must be disclosed. Current full-result delivery can exceed report/context limits; accepted Q-R2-7 does not promise universal delivery or model fit (S-ADR::D10/Consequences).

## User Stories & Acceptance Criteria

Priorities below are verification priorities, not new release routing. All twelve are required feature scope, Priority P1. Independent tests mean a capability can be tested with its real surrounding seam and deterministic inputs; they do not authorize separate landings.

### User Story 1 — A reliable main conversation (Priority: P1)

A workspace member wants one predictable conversation with each main agent, independent of heartbeat settings, so returning to that colleague returns to the same work.

**Why this priority:** addressing every later capability depends on a correct, authorized main identity.
**Independent Test:** provision an eligible agent on two workspace teams; inspect main creation, navigation and protection with heartbeat off.

1. **Given** an eligible main agent joins a workspace, **When** main addressing is requested repeatedly, **Then** one conversation for that pair exists and other workspace pairs remain separate.
2. **Given** the pair's main exists, **When** its heartbeat is turned off, **Then** the same pinned, protected conversation remains and later heartbeat work uses it.
3. **Given** the agent has ongoing authorized work, **When** it is removed from the team, **Then** its main is immediately hidden, new work is refused, ongoing work keeps its outcomes without waking that hidden chat, and re-addition can reveal the retained conversation.
4. **Given** the stored pair is corrupt, mismatched or inaccessible, **When** the main is requested, **Then** an explicit failure is returned, not another workspace's conversation or a fabricated replacement.

### User Story 2 — Retained history and a bounded working window (Priority: P1)

A person wants readable and recallable saved history without making every new instruction reread a lifetime's conversation. Clearing current context must not erase history.

**Why this priority:** truthful retention and archive effects underpin long-lived main chats.
**Independent Test:** retain a known multi-day conversation, clear/slide its working window and compare saved bytes and visible history.

1. **Given** saved messages, **When** a new message is appended and the working window slides, **Then** prior history remains recallable and only the retained working window is read for model input.
2. **Given** a tool interaction crosses UTC midnight and delayed input is waiting, **When** the ready input is consumed, **Then** the interaction remains complete and accepted messages keep their arrival order as separate messages.
3. **Given** old day content expires under retention, **When** the working window is next read, **Then** it starts at the first retained complete group, tells the agent older history expired, or is empty under the same conversation if no content remains.
4. **Given** saving identity, provenance or content fails, **When** input is submitted, **Then** the sender sees a save/admission failure and the system does not claim acceptance or feed unauthenticated unsaved input to the model.

### User Story 3 — One instruction intake and honest limits (Priority: P1)

Humans, connectors and authorized parents want accepted instructions delivered together in order, including messages arriving while an answer settles, without an extra waiting room or invisible cuts.

**Why this priority:** an accepted message must not be lost between entry paths.
**Independent Test:** block one model/tool step, submit several distinct sources, release the boundary and inspect each delivered message and receipt.

1. **Given** a working conversation with several accepted waiting instructions, **When** the next safe step is reached, **Then** all ready instructions enter together in FIFO order, separately visible with their senders.
2. **Given** an answer is settling and more input is accepted, **When** settlement finishes, **Then** the same conversation starts the next ready turn immediately without a still-finishing refusal or timer.
3. **Given** ordinary intake is at a body, sender-rate, item, aggregate or model-fit bound, **When** another message arrives, **Then** it is accepted only if every applicable bound fits, otherwise visibly refused without shortening already accepted text.
4. **Given** the operator changes an existing intake setting, **When** the running configuration reloads, **Then** subsequent admission uses the new value across ordinary sources; report delivery policies remain unchanged.

### User Story 4 — Helpers report without silent loss (Priority: P1)

A parent wants every accepted helper update to reach it and to know when a helper's attempted report was refused. Reporting is information, not authority.

**Why this priority:** a refused question or result cannot appear to have been delivered.
**Independent Test:** send all existing report kinds to idle/live/stopped parents and exceed the preserved report rate/body boundaries.

1. **Given** an idle non-stopped parent, **When** an allowed helper report is accepted, **Then** the parent is woken for that report, regardless of its report kind.
2. **Given** a stopped parent, **When** an accepted report arrives, **Then** it remains held without revival and is available once after legitimate continuation.
3. **Given** a helper exceeds a preserved report admission bound, **When** it attempts another report, **Then** it receives machine-readable refusal/retry information and the parent can see that the input was rejected, not delivered.
4. **Given** several reports include a duplicate delivery identity or an over-size result, **When** they are delivered, **Then** accepted content is not duplicated and over-limit delivery is honestly reported without a new retry, pagination or summary workflow.

### User Story 5 — Ordinary native self-delegation (Priority: P1)

A main agent or native worker wants to hand hands-on work to an ordinary helper of itself, with its ordinary context and limits, rather than requiring a special self-trust configuration.

**Why this priority:** self-delegation is the founder-selected way to prioritize hands-on work in all main chats.
**Independent Test:** delegate with omitted and explicit self-targets, no self-edge, under the same native policy, context and depth fixtures.

1. **Given** an eligible native agent without a self-edge, **When** it self-delegates with an omitted or explicit target, **Then** both forms use the same ordinary helper path and authorization decision.
2. **Given** an ordinary self-helper below the configured depth bound, **When** it delegates onward, **Then** normal depth and memory admission apply with no one-hop exception, per-main quota or special memory behavior.
3. **Given** a Deny or an excluded external/system/Admin caller, **When** self-delegation is attempted, **Then** no Omnipus self-helper starts and the refusal is visible.
4. **Given** delegation targets another agent, **When** it is requested, **Then** existing workspace trust/mode/depth and receiver permissions still apply, with exactly ordinary helper context and no snapshot side fix.

### User Story 6 — Tasks keep their own execution and results (Priority: P1)

The starter and assignee want tasks to be monitorable and to report their own outcome, whether the task is manual, scheduled, isolated or recurring.

**Why this priority:** parentage must not break task ownership, recurrence or completion.
**Independent Test:** start/finalize known tasks across the decided mode matrix and compare conversation, run, goal and recipient identities.

1. **Given** a task's assignee and timing, **When** it runs, **Then** its mode is automatic: MAIN creates a fresh main child each run, one-time worker uses a fresh independent chat, recurring worker continues its chat with a new run, and the scheduled isolation checkbox forces a fresh independent chat.
2. **Given** an authorized future scheduled MAIN run and a stopped main, **When** the run fires, **Then** a child can run without reviving the main, with launch ordered against Stop and with main-parent permissions rather than the starting extra chat's setting.
3. **Given** a run's recipients were captured at start, **When** the run actually finishes, **Then** deduplicated starter/assignee mains receive its final outcome/reason and full stored result; idle eligible mains wake once, stopped mains hold it, skipped fires and retries do not create extra final notices.
4. **Given** a recipient is gone or current result delivery cannot fit, **When** completion is published, **Then** the stored task/run result remains inspectable and the delivery limitation is visible, with no guessed recipient or new overflow mechanism.

### User Story 7 — Stop, redirect and interruption mean what they say (Priority: P1)

A person wants to stop only the selected chat's turn, deliberately stop its helper tree, redirect the same conversation, or continue after a restart without stale controls touching new work.

**Why this priority:** concurrency mistakes here can stop or steer the wrong execution.
**Independent Test:** control a real root/helper tree with selected execution identities and deterministic race barriers.

1. **Given** a live native parent/helper/task tree, **When** one Stop activation is made, **Then** only that chat's current turn stops and the three-second escalation window is available; a second activation there or cancel reaches real descendants, not independent runs or future recurrence.
2. **Given** steer and redirect compete for the same chat, **When** the control race settles, **Then** the caller receives one authoritative queued/applied/superseded/refused disposition, no refused text is delivered, internal stale-generation details are not the explanation, and the replacement turn is untouched by stale callbacks.
3. **Given** pending human input and older parent controls, **When** Stop wins, **Then** human input is visibly held for release/discard and superseded parent controls never return on continuation.
4. **Given** a gateway restart cuts live work, **When** the saved conversation is reopened, **Then** it shows Interrupted rather than permanently Working or restart-failed, and no old queued instruction executes at boot.

### User Story 8 — Addressed group and connector communication (Priority: P1)

Humans and main teammates want to address each other directly in a workspace and receive answers in the right conversation, with real responder identity and no hidden whole-history sharing.

**Why this priority:** mixed-source main intake must not broadcast an answer to the wrong person.
**Independent Test:** address two authorized main teammates from web and two connector instances, using different request markers and return destinations.

1. **Given** a main chat owned by Mia, **When** a person sends an addressed request to Jim, **Then** Jim receives only the request/explicit material, answers as Jim in that same group chat, and Mia sees request and answer without changing ownership.
2. **Given** two same-workspace main teammates with no delegation edge, **When** one messages the other, **Then** communication is allowed within receiver permissions but grants no Stop, context-read or inherited approval authority; a cross-workspace attempt is refused.
3. **Given** a mixed batch from distinct admitted web/connector senders, **When** addressed answers are produced, **Then** each answer returns only to its sender with its original instance, chat/thread and correlation preserved.
4. **Given** an answer has no usable return address, **When** delivery is attempted, **Then** it is visibly refused and the agent must address a sender, not guess a destination or broadcast.

### User Story 9 — Navigation, clear and worker input are real actions (Priority: P1)

A person wants main navigation, extra-chat creation, history search and context clearing to be distinct, and wants to steer an opened worker session without accidentally rerunning its task.

**Why this priority:** renaming menus without changing actions repeats today's wrong behavior.
**Independent Test:** exercise each current entry path with keyboard and typed commands as well as palette selection.

1. **Given** a current workspace's eligible agent row, **When** its main-navigation or extra-chat action is chosen, **Then** the row opens the main while only its New chat action creates an extra conversation; switching navigates and reconnect refreshes the roster without widening eligibility.
2. **Given** a working conversation and pending input, **When** clear is requested, **Then** the next safe point clears current context/display with a marker, retains the same chat and pending input, and keeps old history searchable/recallable.
3. **Given** an opened worker context, **When** a command is typed or selected, **Then** both paths enforce the final capability table: navigation, controls, clear, search and read-only lists work; remembered writes, goal/loop, model switching and configuration do not.
4. **Given** an opened legitimate worker/task chat, **When** the person types a message, **Then** it steers a live run or continues a finished conversation without changing the completed task; workers remain excluded from fresh chat and mention targets.

### User Story 10 — Truthful activity and one approval surface (Priority: P1)

A person wants the existing background-activity panel and approval dialog to say which agent/run is acting, including work started outside the displayed chat, without confusing visibility with control.

**Why this priority:** truthful state, permissions and reachability are part of the feature, not optional UI polish.
**Independent Test:** project known helper/task run identities and one real pending approval into the existing views.

1. **Given** an agent has helpers and scheduled/manual task runs, **When** Activity is opened, **Then** relevant runs are visible once, with running/waiting distinctions, actual token data and a way to open their chats; monitored independent tasks are not shown as tree-owned.
2. **Given** a native helper/run asks for approval, **When** the approval is shown, **Then** the existing dialog labels and links the acting helper/run under the same approval ID and preserves target restrictions; external CLI calls are not falsely presented as protected by this popup.
3. **Given** data is loading, empty, stale or partially unavailable, **When** the view renders, **Then** it exposes that state without fabricating running counts, tokens, delivery success or lost-history recovery.
4. **Given** keyboard or assistive-technology use, **When** a session, mention, held-input control, activity link or deletion confirmation is operated, **Then** the same authorized behavior is reachable with visible focus, accessible names, status announcements and safe dialog dismissal.

### User Story 11 — Idle recap and deliberate deletion remain separate (Priority: P1)

A person wants idle conversations to leave a useful recap and continue under the same identity, while actual deletion truthfully removes an agent's own data rather than hiding a member or erasing guest answers everywhere.

**Why this priority:** recap is not history compaction and membership removal is not data deletion.
**Independent Test:** use an adjustable idle clock and a deletion fixture with owned sessions, memory, guest responses and cleanup failure.

1. **Given** an ordinary human/main chat is inactive, **When** thirty inactive minutes elapse after execution settles, **Then** default-on recap writes agent memory; any intervening turn/message resets activity and another later idle episode can recap again.
2. **Given** an agent has owned data and answers in another chat, **When** confirmed deletion executes, **Then** the one cascade removes owned sessions/memory and retains guest history labelled deleted agent; the UI requires two confirmations but tool/API retain their existing single approval.
3. **Given** deletion is blocked by a protected/system/active-plan guard or cleanup fails partially, **When** deletion is attempted, **Then** the actual refusal/partial result is shown, never complete success or an executable deleted identity.
4. **Given** idle/bootstrap/agent-recorded retrospective behavior, **When** recap is invoked, **Then** those current meanings remain, joined stays, retired lazy/explicit close actions are unavailable, and helper memory/recap behavior is unchanged.

### User Story 12 — One current implementation, genuinely reachable (Priority: P1)

A maintainer and user want the retired paths removed rather than kept behind aliases, while current behavior still reaches the same stores, controls and screens.

**Why this priority:** a green compatibility fixture cannot justify retaining a second system or deleting a current user path.
**Independent Test:** challenge each deletion row's retired entry point and exercise its named canonical replacement through generated contracts and real UI/tools.

1. **Given** any scoped retired path in the deletion inventory, **When** its old command, type, alias, adapter or transport is invoked, **Then** it is absent or refused, and only the listed replacement, or nothing, remains.
2. **Given** a current producer shares a branch with obsolete compatibility handling, **When** its normal, delayed, replayed or non-stream output arrives, **Then** current behavior remains intact with stamped identities, without recreating an old reader or guessing ownership.
3. **Given** the candidate's changed contracts, **When** valid and invalid boundary payloads are submitted, **Then** generated validation and consumer types agree with the published current shapes and reject the retired/malformed variants.
4. **Given** the integrated backend and frontend candidate, **When** a real user and permitted agent exercise the feature, **Then** the intended actions are actually reachable, matching user documentation is present, and exact-commit testing/review evidence exists before landing.

## UI Screens and States

This specifies behavior/state, not sidebar/identity styling. S-FE remains the layout authority. Use existing surfaces rather than introducing another dashboard or task-result card.

| Existing surface | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Agent navigation, chat attach and session search (E-NAV) | Existing load state; no automatic extra chat while main lookup is pending | New protected main with no content is valid; no worker main is invented | Refused/corrupt/unauthorized attach remains visible; do not silently select another session/workspace | Cached roster is identified as stale; retained history may be empty after expiry | Pair's main opens; agent-row New chat deliberately creates an extra; history search reaches retained entries |
| Composer, command/mention palette (E-COMMAND) | Existing command readiness gate prevents accidental submission before command discovery | No matching allowed agent/command is an empty result, not a fallback target | Inline refusal identifies the failed action/limit; disconnected send is not accepted | HELD input stays visible with release/discard; current running step is not claimed cleared before its boundary | Same owner; addressed guest answers carry their own identity; canonical clear/sessions/navigation actions |
| Background Activity (E-ACTIVITY) | No guessed running status or token count | No current activity is empty; retained results remain in existing task/run/history views | Projection/delivery failure stays visible; no loss-free inference from an empty inbox | Running/queued/waiting, stopped-with-live-helpers and missing usage are distinct; absent usage is not zero | One row per actual helper/run; MAIN task not duplicated; own Open link, actual available tokens |
| Existing approval modal (E-APPROVAL) | Existing pending/resolution behavior | No request means no approval card | Existing refusal/expired/resolution errors remain | Label/link acting helper/run; external-CLI exception is disclosed, not a fake native approval | One ID, one queue, existing once/allow/deny actions and target restrictions |
| Calendar task editor and run details (E-SCHEDULE/E-TASK) | Existing occurrence/editor states | One-time or new recurring rule remains current | Existing timing/validation/dispatch failure surfaces; obsolete every/cron_expr is not preserved | Full task result can exist with failed delivery to a main; recurrence remains even when current child stops | Automatic mode; one isolation checkbox; current once and RRULE authoring; run/result/chat links |
| Existing agent deletion/profile (E-DELETE) | Pending delete remains explicit | Deleted agent has no runnable profile; guest history remains | Guard/refused/partial cleanup is honest and actionable through existing result surface | Warning accurately distinguishes owned data from guest history | Two UI confirmations; tool/API one existing approval; one common cascade |

## User Journey

| Step | User-visible action / outcome | Acceptance |
|---|---|---|
| 1 | Open a workspace and choose Mia's row; the same main conversation opens even with heartbeat disabled | US1.1–2, US9.1 |
| 2 | Type a request or `@Jim` request; separate accepted messages and Jim's identified response remain in Mia's conversation | US3.1, US8.1 |
| 3 | Inspect Activity, open the real helper/task chat, and see actual approval attribution; typing there steers or continues that conversation | US9.4, US10.1–2 |
| 4 | Stop once to stop the chat turn; deliberately escalate/cancel for its real tree; held input can be released/discarded | US7.1–3 |
| 5 | Clear at a safe point, then find/recall the retained earlier history; use row New chat only when an extra conversation is wanted | US2.1, US9.1–2 |
| 6 | Inspect a Calendar run/result or come back after idle recap/restart under the same chat identity | US6.3–4, US7.4, US11.1 |
| 7 | Remove a team member to hide its main, or deliberately delete the agent with the stated owned-data warning; these are not the same action | US1.3, US11.2–3 |

## Accessibility and Keyboard

Reuse the existing command/mention keyboard navigation, chat controls, searchable sessions, modal focus behavior and labelled activity Open controls. Palette selection and typed submission must have identical permission/action results. Escape closes an open menu/dialog according to its existing precedence before activating the chat Stop path; do not make an accidental dismissal a destructive confirmation. The final worker capability restrictions apply at execution, not only visually.

Held release/discard, New chat, isolation checkbox and deletion confirmations need accessible names and visible status/error announcements. Guest responses need a readable agent name in addition to the icon. Preserve focus restoration and safe cancellation in the existing confirmation/approval dialogs. Central focus ownership, 24×24 pointer and 44×44 touch hit regions without overlap, 200% zoom, 320px reflow and no color-only state follow `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/design/design-system-definition.md`::D7/D16/D17, not new geometry decisions.

## Design-System Components

The existing catalog `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/design-system/catalog.json` was checked. No new primitive/composite is commissioned by session-core; S-FE owns its separate agent-identity work.

| Job | Existing catalogued component / surface reused |
|---|---|
| Actions, held release/discard, navigation links | Button / IconButton; existing session/composer action surfaces |
| Isolation choice | Checkbox in the existing Calendar editor, not a three-mode chooser |
| Double deletion confirmation | ConfirmDialog using the existing confirmation flow; no browser confirm or second deletion service |
| Loading / empty state | Skeleton / EmptyState where the current screen uses them; preserve existing inline error/status components rather than assembling a one-off |
| Command and mention choice | Existing palette and catalogued Command family; receiver addressing replaces switch behavior |
| Help / labels | Tooltip and existing status/agent-identity renderer; tokens and semantic labels from the current design system |

Frontend-lead loads `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/.claude/skills/omnipus-design-system/SKILL.md` before code changes. Do not decide new colors, avatars, animation or component publication here. Any genuinely missing recurring UI job follows that skill's catalog/upstream/shared-component sequence, with a founder question if its behavior is not decided.

## Security and User Promises

| Boundary | Required preservation / disclosure | Source |
|---|---|---|
| Tool access | Global ceiling plus sparse tightening agent policy only; no new policy layer, self-helper fallback or fail-closed backfill. Explicit Deny still blocks eligible self-delegation. | S-ADR::D4/D10; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/tools.md`::Allow, ask and deny |
| Parent versus peer | Authenticated human/authorized ancestor instructions have steering authority. Helper reports and MAIN-peer messages cannot assert it in text; messaging grants no tree Stop, full context or approval inheritance. Membership/connector ownership and session-scoped recall/browser/tool isolation remain. | S-ADR::D3/D7; **A channel belongs to one (workspace, agent) pair, in both directions** |
| MAIN task approval | Main parent grants/modifier govern within target restrictions; starting extra-chat modifier does not. Disclose possible loosening. Scheduled/unattended Ask needing a person refuses immediately, even with an open chat. | S-ADR::D10; **Shell permission modes: Ask / Auto / God Mode; drop the block list; one rule format**::D10; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/security.md`::Auto-approve; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/tools.md`::Limits and things to watch |
| External CLI | Never Omnipus self-delegates. Interrupt/native resume may kill its background subprocesses; Omnipus tool policy/native popup does not govern its harness. Actual native resume ID is required; success without text delivery or silent fresh-run fallback is forbidden. | S-ADR::D4/D6; Q-R2-4/5; current E-CLI |
| Shared channel context | Every connector message for the bound agent/workspace currently goes to its main. Source-specific return routing does not make participants' history/control private. Per-person sessions remain #1206. | S-ADR::D7; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/connectors.md`::Choosing which agent answers |
| Accepted limitations | Queued/held human input may be lost on gateway restart; saved transcript, acceptance and model consumption are separate. Full stored results retain today's delivery/context limits; no universal-fit/exactly-once side-effect guarantee. | S-ADR::D3/D5/Consequences; Q-R2-7; #1198 |
| Security review | Auth, sender/provenance, gateway admission, permission inheritance and deletion require the security-lead pass; no new mandatory runtime, security-critical shellout or secret copying. | S-RULES::Hard Constraints and feature gate |

## Behavioral Contract

| When | Then |
|---|---|
| An eligible main colleague is added to a workspace | One pinned main conversation is available, independent of heartbeat on/off |
| Ready ordinary messages arrive while work runs or settles | Accepted instructions enter together in order as separate messages at the next safe step or next turn |
| Ordinary input exceeds any approved applicable limit | Refuse visibly before acceptance; never cut accepted text to fit |
| A helper report is accepted for an idle non-stopped parent | Wake that parent for the report; a stopped parent holds it instead |
| A MAIN task runs | A fresh real child of the assignee's main does task-owned work; recurrence remains configured after current-run Stop |
| A recurring worker task runs again | Continue its independent conversation with a distinct run and outcome |
| A peer or connector reply is produced | Send it only to its addressed sender; missing return address is a visible refusal |
| Clear reaches a safe point | Clear current context/display with a marker, retain history and pending input, keep the conversation identity |
| A user types into an opened finished worker task chat | Continue that chat, not the finished task or a new implicit rerun |
| Old retained day content expires | Advance to the first retained complete group and inform the agent, or keep an empty window in the same conversation |
| A team member is removed / an agent is deleted | Hide retained main / delete owned data with truthful confirmation and guest-history preservation, respectively |

## Edge Cases

| Boundary / state | Expected behavior | Acceptance |
|---|---|---|
| Same pair requested concurrently; long valid IDs; corrupt/mismatched existing pair | One computed authorized identity; no UUID-length assumption, guessed owner or duplicate main | US1.1/4 |
| UTF-8 multibyte text; attachment-only input; body/count/aggregate/model bounds | Bytes and model/media cost tested independently; ordinary refusal precedes acceptance; current attachment-only validation remains | US2.4, US3.3 |
| One sender floods; many senders share a target; same name from another instance | Trusted sender+target rate keys, not text name or whole-target pooling | US3.3–4 |
| Input at natural completion; Stop/revive/redirect races; stale forced Stop | One intake/settlement owner and selected-execution outcome; stale callbacks cannot affect replacement | US3.2, US7.2–3 |
| Midnight/tool result; delayed earlier timestamp; oldest marked file expired; no content retained | Append accepted order, keep complete groups, bounded valid mark, expiry notice/empty same-ID | US2.2–3 |
| Duplicate report, unchanged report rate/body rejection, simultaneous full results | Current dedupe/caps/delivery limits, all accepted report wake behavior, visible refusal; no new overflow transport | US4.1–4, US6.3–4 |
| Recipient/team membership disappears during run; scheduled launch races tree Stop | Capture remains fixed; authorized work settles without hidden-main wake; launch/Stop ordered | US1.3, US6.2–4 |
| MAIN assignee versus recurring worker; scheduled isolation checked | MAIN wins by default; CONTINUE worker-only; explicit scheduled isolation forces independent fresh conversation | US6.1 |
| External CLI lacks usable native ID, is unavailable, or Stop kills its background process | Visible failure/exception, no successful no-op or fabricated resume/fresh conversation | US7.2/4, US10.2 |
| Guest agent deleted; delete cleanup partial; guarded active plan | Retain guest history as deleted agent; no runnable identity; guard/partial result accurate | US11.2–3 |
| Current kickoff/no-stream/reconnect/evicted-owner/plan-scope output shares an old branch | Preserve that current path with canonical identity; no hidden current messages or invented goal/owner choice | US12.2 |

## Explicit Non-Behaviors & Safeguards

### Qualitative prohibitions

The system MUST NOT create another session store, address map, queue, task executor, scheduler, report retry/overflow service, approval modal or result dashboard: the relevant mechanisms already exist (S-ADR::Alternatives considered). It MUST NOT copy an originating conversation wholesale to an addressed guest, invent child edges for peers/independent tasks, treat visibility as Stop authority, or broaden workers into fresh chat/mention targets.

It MUST NOT add a summary step, change helper recap/memory, create a self-only nesting rule, per-main spend controller or extra admission quota, resume stopped mains for reports, replay lost waiting input at boot, or keep upgrade-only adapters behind deprecated aliases. S-FE layout questions, #1212/#1213/#1215/#1217 fixes and #1206 privacy separation are not side work in this feature.

### Machine-verifiable constraints

| Category | Normative bound / observable oracle |
|---|---|
| Ordinary text | Default 65,536 UTF-8 bytes per message; 60 admitted ordinary messages per minute per trusted sender+target; 200 waiting ordinary items; 1,048,576 aggregate waiting ordinary text bytes AND existing model-budget fit, whichever is stricter. No silent cut. |
| Model fit | Reuse E-LIMIT's current budget/estimator. Formula remains B = W − max_tokens − ceil(0.05 × W) − pinnedCoreOverhead; real media/tool costs count. External CLI budget exemption is not a fit guarantee. |
| Reports | Preserve existing report body/rate/count exemptions, unacked/type ceilings and delivery semantics while extending wake eligibility for accepted kinds. Stops/cancels and engine final reports do not consume ordinary sender-text rate. |
| Identity | Main ID is exactly main-session-<workspaceid>-<agentid>; stored owner/pair must match. No new-main create enum, handover or missing-current-type adaptation. |
| Control | Stop activation scope and three-second escalation are exact; return queued/applied/superseded/refused through existing typed status/control surfaces. Refused control text is never delivered. Use the existing protocol's refusal envelope, not raw admission/generation text. |
| Retention / idle | Default retention 90 days by file modification age, disabled retention remains respected; idle timeout default 30 minutes, default-on auto recap, any turn/message resets it, no cleanup while executing/settling. |
| Deployment | One pure-Go binary and embedded SPA; Linux/macOS/Windows only, existing degradation retained, no new mandatory runtime/database. Security-feature overhead ceiling remains <10 MB beyond baseline; no new throughput/latency budget is invented. |

## Integration Boundaries

| Boundary | Data in/out and contract | Failure behavior | Development verification |
|---|---|---|---|
| Gateway ↔ SPA | C-MAIN/C-INPUT/C-CONTROL; existing REST/WS schemas and generated validators/types | Visible current-protocol admission/attach/control/validation failure; no optimistic save/owner fallback | Deterministic gateway plus actual generated SPA decoder; real browser on integrated candidate |
| Session ↔ model provider | Saved model-view window, pending separate messages, tool calls/results and existing estimate | Current model/window error is visible; accepted content not silently removed; provider-valid groups and recall remain | Fake deterministic provider records every supplied message; real agreed provider/model only for UAT |
| Helper/task/peer ↔ recipient | C-INBOX/C-TASK, current report inbox and authenticated adapters | Preserve cap/context failure, Stop holds, identity/correlation and missing-recipient task result; no fake edge or retry service | Real stores and common deliverer/runner; fake clock/barriers only at time/model boundary |
| Calendar ↔ cron/task engine | C-TIMING plus derived modes and existing once/RRULE next-occurrence/rearm path | Timing/dispatch validation failure does not become success; no old recurring adapter retention | Real trigger mapper/run store with deterministic clock, current Calendar editor and task/run links |
| Connector ↔ main / reply | Authenticated source/instance/chat/thread, same authorized workspace/agent binding, current send_message ownership | Unknown/missing return correlation visibly refuses; disconnection/send failure is not delivered; no guessed instance | Simulated connector twins for routing matrix; real connector/browser entry lanes in acceptance where available |
| Omnipus ↔ optional CLI worker | Current three E-CLI drivers; interrupt plus actual native conversation resume, task/model/workspace/cap preserved | Missing native ID/CLI/auth or failed input/resume is explicit; Stop may kill CLI background subprocesses; no fake resume ID | Driver/process doubles for deterministic failure/race tests, actual installed supported CLI for live steering proof; no paid-runtime success claim from help alone |
| Membership / deletion ↔ owned data | E-MAIN/E-DELETE and current authorization/confirmation | Hide blocks new work; running authorized work settles; cleanup/guard failure visible; guest history preserved | Real common cascade/store with injected I/O failures; UI and tool/API paths both exercised |

## Ambiguity Warnings

| Area | Decision / boundary | Implementer must not assume |
|---|---|---|
| Session-core questions | All six S-Q answers are closed; this draft does not reopen them | Different limits, deleting current once, guessed reply destination, automatic hidden-main wake or tool/API double confirmation |
| Known accepted risk | Restart waiting-input loss; result delivery/context caps; shared channel privacy; MAIN-parent approval can loosen relative to extra chat; CLI subprocess-kill exception | Extra durability, universal result fit, privacy separation or native-popup protection for external calls |
| Counterpart | S-FE is Proposed and owns its unanswered layout/identity and joint cutover questions | A latest-session main fallback, avatar/storage/migration decision, unsupported saved-history conversion, or separate sidebar landing |
| Scoped frontend branch removal | S-ADR::DEL-F preserves current shared producer behavior; each implementing deletion needs proof of its canonical current replacement | Dropping current non-stream/kickoff/evicted-owner output, choosing a latest goal, or guessing missing producer/position |

There is no new session-core product assumption in these warnings. Any genuinely new unresolved choice goes to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/session-core-adr-20261007/QUESTIONS.md` as the next OPEN-Qn with context/options/recommendation, and the affected document requirement is held. Do not implement a guessed answer. Frontend counterpart questions remain with that ADR's founder interview; referencing it does not mark it Approved.

## Functional Requirements

Each path key expands to the exact source citations above. MUST requirements are normative; no optional scope is introduced. Detailed acceptance, deletion inventory and traceability will be completed before this Draft becomes In review.

| ID | Requirement | Existing path extended | Authority |
|---|---|---|---|
| FR-001 | All boundary changes MUST be contract-first and use committed generated types/runtime validation only; remove obsolete shapes, no parallel wire types. | C-MAIN–C-LIMIT and existing generation/validator path | S-RULES Constraint #8; S-ADR::D1.1/D10 |
| FR-002 | Eligible MAIN membership MUST eagerly create/reuse exactly one computed pinned/protected main pair, heartbeat-independent; workers/system/Admin have none. Pair mismatch/corruption/unauthorized binding MUST refuse without guessing. | E-MAIN; existing role eligibility | S-ADR::D1/D1.1 |
| FR-003 | Team removal MUST immediately hide main from UI/channels/task addressing and reject new work through that membership; authorized live work keeps outcomes without hidden-main wake; re-add reveals retained pair. | E-MAIN/E-DELETE | S-ADR::D1; Q5=A |
| FR-004 | One authoritative saved entry format MUST express chat/model/both and full model content; no duplicate model-content store. | E-STORE | S-ADR::D2 |
| FR-005 | UTC day archives and file/byte marks MUST support bounded window reads across days, FIFO delayed arrival and complete tool groups; no whole-archive read on each append/model step. | E-STORE/E-QUEUE | S-ADR::D2/D3 |
| FR-006 | Clear/correction/rollback/projection MUST append effects or change view/window metadata, never rewrite prior retained content; preserve provider-valid calls/results, pending control identity, recall and result references; no new summary writer. | E-STORE/E-RECAP | S-ADR::D2/D8/D9 |
| FR-007 | Retention MUST preserve current 90-day modification-age/disabled semantics, expire old main content, repair to first retained complete group with agent notice, or empty same-ID window. | E-RETENTION/E-STORE | S-ADR::D2; Q2=A |
| FR-008 | Acceptance MUST save message and trusted provenance together or neither; failed save/admission MUST not claim echo/receipt/consumption success. | E-QUEUE/E-STORE | S-ADR::D3; **Steering commands: no person question** |
| FR-009 | One session FIFO and existing runner MUST own all ordinary instruction intake through settlement; ready separate messages enter together at safe boundaries or immediate next turn; no competing inbox/wait/manual fallback. | E-QUEUE | S-ADR::D3; October 6 Q20 |
| FR-010 | Ordinary admission MUST apply approved body/rate/count/aggregate AND existing model fit before acceptance, with trusted sender+target rate identity and visible refusal, no silent text cut. | E-LIMIT/E-QUEUE | S-ADR::D3; Q1=A |
| FR-011 | Existing steer_body/steer_rate and the one aggregate setting MUST control live ordinary intake at boot/reload, not only saved config/display; report policies unchanged. | E-LIMIT and existing gateway boot/reload wiring | S-ADR::D3.1; #1216 |
| FR-012 | All accepted helper report kinds MUST use existing inbox/dedupe/ack/consumption and wake idle non-stopped parents, join live safe boundaries, or hold without revival when stopped. | E-INBOX/E-QUEUE/E-STOP | S-ADR::D3/D6 |
| FR-013 | Actual refused report arrivals MUST give child typed refusal/retry information and parent rejected/not-delivered visibility; current caps/exemptions/result-delivery remain, no new retry/overflow service. | E-INBOX/E-ACTIVITY | S-ADR::D3.1; #1211; Q-R2-7 |
| FR-014 | Omitted self-target MUST normalize before common authorization; eligible native MAIN/WORKER self-delegation requires no self-edge; explicit operator Deny still wins. | E-DELEGATE/E-POLICY | S-ADR::D4; Q-R2-2 |
| FR-015 | Self-helpers MUST use exactly ordinary context, memory, requested-skill/depth/admission/receiver rules and normal nested depth default 3; all MAIN chats retain role tools and prioritize delegation by instruction. | E-DELEGATE/E-RECAP; existing role prompts | S-ADR::D4/D9; no #1212 fix |
| FR-016 | External CLI workers MUST never create Omnipus self-helpers; Admin/system tool/runtime treatment remains unchanged. | E-DELEGATE/E-CLI/E-POLICY | S-ADR::D4; Q-R2-4 |
| FR-017 | Existing task/schedule dispatch MUST derive the four-row mode matrix, not accept a new mode chooser; MAIN fresh real child per run, worker CONTINUE new run/same chat, scheduled isolation independent fresh chat. | E-TASK/E-SCHEDULE; C-TASK | S-ADR::D5 |
| FR-018 | Future authorized scheduled MAIN launch under stopped main MUST be task-origin-only and ordered with Stop, without main revival; task goal/claim/limits/attempts/plan/outcome remain task-owned. | E-TASK/E-DELEGATE/E-STOP | S-ADR::D5/D6; Q-R2-1 |
| FR-019 | Run recipients MUST be captured at start: starter MAIN's main, not creator, plus assignee MAIN's main, deduped; missing captured recipient retains task/run result, never a substitute. | E-TASK/E-INBOX; C-TASK | S-ADR::D5; 10:25 |
| FR-020 | Actual done/failed/stopped runs MUST notify with authoritative reason and full stored result plus brief engine header; no per-retry/skipped-run final notices or launch-time model injection; eligible idle wake once/batch, stopped hold. | E-TASK/E-INBOX/E-QUEUE | S-ADR::D5 |
| FR-021 | ISOLATED/CONTINUE MUST remain independent despite display/notification; current result delivery limits remain visible, with no new summarizer/defer/pagination/retry queue. | E-TASK/E-ACTIVITY/E-INBOX | S-ADR::D5; Q-R2-7 |
| FR-022 | Existing Stop MUST implement first current-turn activation, second within three seconds/tree cancel, real descendants including current MAIN task children; future schedule and independent runs stay; native plain Stop retains background shells. | E-STOP | S-ADR::D6; October 6 Q13 |
| FR-023 | Same-session root/helper redirect and steer race MUST preserve control precedence and selected-execution identity; queued/applied/superseded/refused must be truthful and curated, no refused-but-delivered text or stale callback touching replacement. | E-STOP/E-QUEUE | S-ADR::D3.1/D6; #1214 |
| FR-024 | Stop MUST visibly hold pending human input for release/discard, never silently replay all held messages; older superseded parent controls never return on revival; report holds remain information. | E-STOP/E-QUEUE | S-ADR::D6; 06:54 |
| FR-025 | Restart-cut conversations MUST show Interrupted without boot replay of old queued input; keep task interrupted-failure/plan recovery and integrate I1/#1217 without a competing recovery path. | Existing E-STOP/E-QUEUE boot/lifecycle readers | S-ADR::D6; #1217 cross-reference only |
| FR-026 | Same-workspace MAIN peers MUST be addressable without delegation edges; workers excluded, no cross-workspace delivery, whole-source-history read or transferred control/permissions. | E-INBOX/E-MAIN; existing send_message path | S-ADR::D7 |
| FR-027 | Group requests/answers MUST preserve responder and return identity live/replay; owner sees both; guest answer wakes idle non-stopped owner once, stopped holds, silence creates no empty bubble. | E-INBOX/E-STORE/E-COMMAND | S-ADR::D7 |
| FR-028 | Mixed-source answers MUST each address their sender with original instance/chat/thread/session/correlation; unaddressed output MUST visibly refuse, with no fallback/broadcast; channel input uses shared bound main for now. | E-INBOX/E-QUEUE; existing send_message ownership | S-ADR::D7; Q3=B; #1206 cross-reference |
| FR-029 | Row click/switch navigation MUST open eligible main; only agent-row New chat creates extra MAIN chat in UI; preserve team picker and existing connect/reconnect roster refresh. | E-NAV/E-COMMAND | S-ADR::D8; S-FE::D3/D5/D12 |
| FR-030 | Clear MUST execute at next safe point, change context/current display with marker, preserve same session, pending input and retained recall/search; no false claim that a live step forgot input. | E-COMMAND/E-QUEUE/E-STORE | S-ADR::D8; Q-R2-9 |
| FR-031 | Canonical sessions search/switch navigation and final worker commands MUST be enforced at palette AND typed/backend execution; retire all named old commands/aliases. Read-only lists do not permit model/config mutation. | E-COMMAND | S-ADR::D8 final 12:35 table |
| FR-032 | Authorized input into existing worker/child sessions MUST steer live task or continue finished chat without mutating/rerunning completed task; fresh worker chats and worker mentions remain forbidden. | E-STOP/E-TASK/E-NAV | S-ADR::D8; Q-R2-10 |
| FR-033 | Existing Activity MUST include relevant agent tasks/scheduler runs not started by this chat, truthful starter/assignee/parent distinctions, one MAIN row, actual running/waiting/token data and Open links; no extra dashboard. | E-ACTIVITY/E-TASK | S-ADR::D4/D5/D6 |
| FR-034 | MAIN children MUST inherit main-parent approval within target restrictions, with possible loosening disclosed; native helper/run labels/links use existing approval ID/modal; unattended Ask immediately refuses, external exception remains explicit. | E-APPROVAL/E-TASK/E-CLI | S-ADR::D10; Q-R2-3/5 |
| FR-035 | Existing UI MUST cover loading/empty/error/partial/success, keyboard/assistive reachability and truthful no-guess state with catalogued components and current focus/touch/zoom rules; no new visual-design decisions. | E-NAV/E-COMMAND/E-ACTIVITY/E-APPROVAL/E-DELETE | S-RULES; S-FE layout boundary |
| FR-036 | Default-on auto recap MUST use thirty-minute activity-based idle episodes including main, reset on any turn/message, not overlap execution/settlement, keep idle/bootstrap/joined and helper behavior, DELETE lazy/explicit session_close/ack. | E-RECAP/E-QUEUE | S-ADR::D9 |
| FR-037 | One agent-delete cascade MUST keep guards, remove owned sessions/memory, retain guest responses labelled deleted agent, warn/twice-confirm UI only, keep tool/API single approval and truthful partial cleanup. | E-DELETE/E-STORE; existing profile confirmation | S-ADR::D9; Q6=B |
| FR-038 | Every scoped DEL-01–22 and DEL-F01–43 MUST be DELETE with the named canonical replacement or nothing, including registration/contract/alias/compatibility-only tests/docs; no migration/backfill/shim. | Exact deletion inventory from S-ADR, to be expanded below | S-LEDGER::14:45; S-ADR::Consequences |
| FR-039 | Scoped removal MUST preserve current behavior sharing old branches and all preserved safety boundaries; finish direct-caller sweep before deletion, no guessed latest goal/owner/producer or side fixes outside scope. | Canonical paths in S-ADR::DEL-F and preserved inventory, E-STOP/E-STORE/E-POLICY | S-ADR::DEL-F boundary; P1–P17 |
| FR-040 | Existing single pure-Go binary/file stores, supported platform degradation, session-scoped tool/browser/recall state, memory-based admission, lock order and security footprint ceiling MUST remain; no permanently hot lifetime main runner/archive. | E-STORE/E-RETENTION/E-POLICY and existing runtime | S-ADR::D10; S-RULES constraints |
| FR-041 | Matching user-facing DOC TODOs MUST land with each behavior/UI change and be audited by docs-verifier; module instruction changes require byte-identical AGENTS twins. | Existing docs pages and module instruction process | S-RULES Definition of Done |
| FR-042 | Real UI/agent reachability, RED/GREEN/CHECK, five-reviewer gate and tester+independent-validator UAT on exact candidate MUST precede landing; main navigation/sidebar land jointly after integration; this author stops before grill. | E-POLICY/E-NAV/E-TASK and existing feature delivery process | S-ADR::D10; S-FE::D12; S-LEDGER::14:50 |

## Draft completion boundary

This checkpoint commits decided behavior, reuse, contract requirements and the first-class frontend surfaces. BDD scenarios, test datasets/regressions, the explicit per-path deletion table, documentation traceability and final success/reachability checks are still to be written. No implementation or review is authorized by this Draft.

skills: omnipus-shared-rules, plan-spec, gitnexus-exploring, ux-heuristics-review, omnipus-design-system, github-cli, jev-use:jev-use

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Requirements are existing founder decisions, not new choices | S-ADR::D1–D10/Questions answered; S-LEDGER::all October 6/7 entries; S-Q::six closed answers | Verified instruction, high confidence |
| Existing mechanisms are reused, contracts lead consumers | Source citations E-MAIN–E-POLICY; C-MAIN–C-LIMIT; direct controlled source/caller scans under `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/build/session-core-evidence/continuation-20261007/` | Verified source presence; proposed behavior untested |
| Frontend counterpart and issue boundaries are explicit | `git show 554d21ffd:docs/internal/architecture/ADR-20261007-agent-first-navigation-and-agent-identity.md`, exit 0: Proposed, D5/D12; eight `gh issue view` receipts, exit 0 each | Verified reference/instruction, high confidence |
| **Self-check** | Draft reviewed against the ADR/ledger and repo-specific template; first-line Draft reflects incomplete acceptance/deletion/traceability work. No production, contracts, tests, grill or release PASS claimed. | Verified document boundary; completion pending |
