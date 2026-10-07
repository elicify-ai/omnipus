Status: Draft
ADR: [Session core with an agent address book: reuse one standing session, one archive and the existing execution paths](/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-20261006-session-core-with-an-agent-address-book.md)

# Feature Specification: Session core

Created: 2026-10-07.
Size: Feature. This is a specification, not implementation, testing or landing approval.
Checkpoint: all eight founder questions are closed; 17:45 waiting/task-tool merge and separate 18:05 #1221 dependency are recorded. Final coverage/deletion/document checks precede In review hand-off.

## Overview

Mia has one main conversation in each workspace. Clicking her opens it. **+ New chat** on her agent row opens an extra conversation. Heartbeats use the main conversation; tasks normally run in real children of it, while remaining tasks with their own goals, runs and results. A message to `@Jim` asks Jim to answer in the group conversation; it does not replace Mia.

Use the existing standing-session creation, session store, instruction queue, report inbox, task executor, scheduler, Stop path and UI. Do not build parallel replacements beside them. This specification translates the recorded decisions into requirements and verification oracles; it makes no new product decision.

### Authority, interview record and scope

| Source key | Source and precedence |
|---|---|
| S-ADR | **Session core with an agent address book: reuse one standing session, one archive and the existing execution paths**, linked above; original approved-decision rewrite at `b76b412f6`, dated reference/status follow-up at `b037e91f1`. Sections D1–D11, including the later 17:15/17:25 overrides, DEL-01–23 and DEL-F01–43 are normative; 17:45 closes Q7/Q8; their earlier proposals are historical, not held requirements. |
| S-LEDGER | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/CONTINUATION-20261005.md`::all founder entries on 2026-10-06/07. This is the interview record, not a missing interview file to invent. Later answers win, especially October 7 13:40, 13:50, 14:10, 14:35, 14:45, 15:10, 15:20, 15:55, **17:15, 17:25, 17:45 and 18:05**. |
| S-Q | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/session-core-adr-20261007/QUESTIONS.md`::All closed questions. Q1=A, Q2=A, Q3=B, Q4=A, Q5=A, Q6=B. Their former OPEN labels are not pending decisions. Q7=A and Q8=MERGE close those later choices at 17:45: shared main seen state/goal mapping and the merged canonical task family. No open question remains. |
| S-FE | **ADR-20261007 — Agent-first navigation and agent identity**, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/ADR-20261007-agent-first-navigation-and-agent-identity.md`, `work/adr-frontend-navigation-20261007` at `554d21ffd`, **Proposed**. Read from that Git object; not present on this branch. D5 consumes the shared contract; D12 requires the sidebar and main-session backend to land together after a joint integration test. Layout, avatars and that ADR's unanswered frontend questions are not decided here. |
| S-RULES | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/CLAUDE.md`::Hard Constraints, Definition of Done, Contract regeneration; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/.claude/skills/plan-spec/SKILL.md` and its knowledge templates. |
| Grounding | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/AS-IS-architecture.md`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/internal/architecture/plugin-extensibility-assessment.md`; current ADRs and code. These walkthroughs are dated; code wins on current behavior. |

The sources are already the founder-confirmed requirements. No review recommendation or provider research overrides them. The binding 17:15 answer authorizes the narrow install/upgrade saved-chat migration described below; greenfield remains the rule outside that exception. Storage-before-queue was a proposed build order, not a ratified delivery sequence; team-lead owns task planning. Contracts must precede their consumers regardless of implementation grouping (S-ADR::Questions answered and D10).

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
| E-APPROVAL | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_policy.go::inheritSessionPermissions`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/steer_launcher.go::inheritDelegatePermissions`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/agents/ToolApprovalModal.tsx::ToolApprovalModal`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/toolApproval.ts::ToolApprovalStore` | Reuse existing grants, parent per-chat modifier, target tool-policy/authority checks and one approval ID/modal. MAIN children source their main parent. The baseline per-agent Auto off-switch is DELETE under separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), implemented outside this spec; helpers inherit the parent with no agent off-switch. |
| E-TASK | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_executor.go::ExecuteTask`, `StartTaskNow`, `startTaskNowViaLauncher`, `activateTaskGoal`, `SpawnTriggeredRun`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_executor_run.go::openRun`, `closeRun`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_executor_judge.go::deliverTaskCompletionUpward` | Keep task claim, goal, attempts, limits and authoritative result. Add MAIN parentage and captured notification recipients, not another task outcome owner. |
| E-SCHEDULE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/schedules.go::scheduledRunner.pickSession`, `RunScheduled`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_trigger.go::TaskTriggerScheduler.RunScheduled`, `triggerToCronSchedule`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/cron/service.go::SessionMode`, `AddJobFull` | Existing modes and scheduler. Retain Calendar once/at_ms and RRULE timing; remove old task every/cron_expr adapters, not internal heartbeat cron. |
| E-COMMAND | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/commands/builtin.go::BuiltinDefinitions`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/commands/cmd_clear.go::clearCommand`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/commands/executor.go::Executor.Execute`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/hooks/useSlashMenu.ts::runClientSlashCommand`, `selectMentionAgent` | Change actions and execution eligibility, not just menu labels. Clear currently starts a new chat; mention currently changes its agent. |
| E-NAV | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/useSelectSession.ts::useSelectSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/session.ts::startNewSession`, `attachToSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/connection.ts::setConnected` | Reuse navigation, attach, search and existing connect/reconnect roster refresh. S-FE owns layout, not a new session registry. |
| E-ACTIVITY | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/hooks/useRunningActivity.ts::useRunningActivity`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/ActivityPanel.tsx::partitionRunning`, `ActivityRow` | Extend span-only activity with agent-task/run projection and actual tokens; preserve running/queued/waiting distinctions and existing Open controls. |
| E-RECAP | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_idle.go::resetIdleTicker`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/session_end.go::CloseSession`, `persistResponse`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/memory.go::MemoryStore.WriteLastSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/memory.go::RetrospectiveTool.Execute` | Existing idle recap writes agent memory. Keep joined retrospective stamp; no archive summary writer is added. |
| E-CLI | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/runner/driver_claude.go::Input`, `Resume`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/runner/driver_codex.go::Input`, `Resume`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/runner/driver_opencode.go::Input`, `Resume`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/external_dispatch.go::policyApproverConsent.RequestConsent` | Mature current drivers for interrupt plus actual native-conversation resume, with honest unavailable/error states. External consent is not native-popup enforcement. |
| E-DELETE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/rest_agents.go::deleteAgent`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/sysagent/tools/agent.go::cascadeDeleteAgentSessions`, `cascadeUnassignAgentTasks` | Consolidate existing differing cascades; delete owned history/memory, retain guest answers elsewhere, preserve guards and report partial cleanup. |
| E-POLICY | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/config/defaults.go::defaultToolPoliciesGeneral`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_wire.go::registerDelegationTools` | Existing catalog, global ceiling and per-agent registration. Native eligibility does not override operator Deny; no new messaging or self-helper tool. Founder Q8=MERGE makes the existing canonical task tools the reachable explicit-workspace path; remove the duplicate family and preserve Operator Deny. |

### Later founder amendments — additional existing seams

| Key | Existing path read | Extension / evidence limit |
|---|---|---|
| E-ADDRESS | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/config/config.go::ChannelInstanceConfig`, `ChannelIdentity`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/config/config_channels_instance.go::ChannelInstanceConfig.IsWorkspaceBound`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/channel_ownership.go::channelOwnershipResolver.OwnerOf`, `OwnedBy`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_inbound.go::resolveMessageRoute`, `resolveWorkspaceIDForContinuation` | Channels already bind WorkspaceID plus agent-kind Identity.ID, and OwnerOf/OwnedBy resolve/compare both. Extend that pair semantics to @/peers and the computed main lookup, including explicit cross-workspace targets; no new addressing namespace or bare agent ID. |
| E-MIGRATE | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/unified.go::NewUnifiedStoreWithHome`, `migrateLegacy`, `writeUnifiedMetaDirect`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/memory/migration.go::MigrateFromJSON`; E-STORE current saved-chat readers | Reuse/mature current import/read/staged atomic-write patterns for this cutover only. Current imports can skip failures or synthesize incomplete identity; they cannot be declared sufficient unchanged. One new-format steady-state store remains. |
| E-TASK-TARGET | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/sysagent/tools/task.go::TaskCreateTool.Execute`, `taskCreateToolExecute.resolveWorkspace`, `enforceDelegation`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_delegation.go::NewSysagentDelegationDeny`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/task.go::TaskCreateTool.resolveWorkspaceID`, `taskCreateToolExecute.buildTask`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory`, `adr090SparseRolePolicies` | Explicit cross-workspace task creation already exists for permitted callers; ordinary create_task uses caller/default only and shipped ordinary roles do not receive create_task_in_workspace. 17:45 Q8=MERGE supplies the correction: optional workspace_id on existing create/update/list and deletion of the duplicate family, not a blanket privileged-tool grant. Plans and task assignment/criteria/permission gates are not broadened. |
| E-ATTENTION | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/ws_tool_approval.go::sessionStateBytes`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/ws_ask_user.go::broadcastAskUserCard`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/goal_outcome.go::recordGoalOutcome`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/goal_outcome_frame.go::goalOutcomeFrame`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/websocket_replay.go::handleAttachSession`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/notifications/store.go::MarkRead` | Real pending snapshots and live question/approval/goal updates exist. Goal outcomes save then emit under stable entry identity; no implemented goal-seen/read writer exists at the code baseline. Per-user notification MarkRead was an assessed candidate, not the chosen shared main metadata or proof that goal attention already works. 17:45 Q7=A supplies shared pair-wide seen metadata and exact met/rounds_exhausted/other mapping; do not introduce per-user goal notifications. |
| E-PROMPT | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/coreagent/prompts_adr090.go::adr090Prompts`, `exactToolDiscoveryRule`; existing structured question tool/catalog and E-ATTENTION | Prometheus-prompt-engineer authors the instruction to use the structured question tool whenever a user answer is needed; backend-lead wires it. Preserve denied/unavailable-tool honesty, child report versus user-answer authority and unattended approval limits; architect writes no product prompt here. |

### Impact assessment and execution flows

| Modified seams | Direct dependencies to update/test | Indirect flows to test | Evidence / risk |
|---|---|---|---|
| E-MAIN / E-STORE / E-RETENTION | Workspace membership/heartbeat reconciliation, scheduler session selection, session REST listing/detail, window/recall/replay and deletion readers | Navigation/search, tasks/verifiers, usage and boot recovery | Controlled tracked-source caller sweep; **Inferred broad impact**, not a GitNexus HIGH verdict. |
| E-QUEUE / E-INBOX / E-STOP / E-LIMIT | Web/channel input, parent steer/respond, settlement, report wake, Stop/redirect and receipts | Same-agent parallel chats, task children, peer response and CLI resume | Direct source seams; **Inferred concurrency-sensitive impact**. No five-second workaround is approved. |
| E-DELEGATE / E-TASK / E-SCHEDULE / E-APPROVAL | Ordinary launcher, task start/fire/result, membership gates and grant inheritance | Calendar run history, plan recovery, activity/approval UI and native/external boundary | Caller searches plus source reads; **Inferred cross-tree impact**. Task run ownership must remain independent of its chat parentage; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
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
| C-MAIN | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/Session.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/WorkspaceMemberConfig.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/WorkspaceMemberHeartbeat.yaml` | S-ADR::D1.1 exactly: require current Session.type; replace heartbeat type with server-only main; immutable agent_id; computed pair ID; derived protected; readOnly main_session_id on eligible MAIN member config. Delete heartbeat.session_id and mutable active_agent_id/handover semantics. Main is absent from client-create enum; workers/other system agents have no main_session_id. Admin has the default-workspace main; publish it through existing Session responses/list and any existing Admin config surface without fake team membership. |
| C-INPUT | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/MessageFrame.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/Message.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/asyncapi.yaml`::MessageFrame, ReplayMessageFrame, SessionStateFrame, MessageStatusFrame | Carry the decided per-message source/return correlation and held/control status through current intake/status/replay surfaces. Server-authenticated identity, original session, connector instance and chat/thread remain attached to each accepted input. Extend the current entry to express chat/model/both membership and full model representation without duplicate content. Keep real message/turn/producer identities; remove old uncorrelated acknowledgements and guessed-producer branches. |
| C-INBOX | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/openapi.yaml`::SessionMessage; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SessionMessageSteer.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SessionMessageRespond.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SessionMessageQuestion.yaml` | Extend this envelope's addressing for the already-decided peer/independent-task adapters. Preserve message_id, sender_identity, correlation_id, generation and real child parentage where applicable. A peer is not parent_to_child authority; no invented parent_session_id/edge. Typed refusal/retry information and intake outcomes belong to existing response/status surfaces. The logical information is fixed by S-ADR::D3/D7; no new transport or server-chosen reply destination. |
| C-TASK | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskRun.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskCreateRequest.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskUpdateRequest.yaml`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/ScheduleCreate.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/ScheduleUpdate.yaml` | Extend existing run records with recipients captured at start and existing scheduled-task records with the single optional isolation checkbox. Modes are derived, not user/agent-selected. Preserve task/run/goal IDs, result and outcome owners; no new stopped Task status. Real MAIN parent_session_id remains separate from task/run origin. |
| C-TIMING | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskTrigger.yaml`; current Calendar and task trigger path E-SCHEDULE | KEEP manual, once/at_ms and current recurring RRULE + anchor/time zone. DELETE task every/every_ms and recurring.cron_expr shapes/adapters/docs/compatibility tests. Remove the misleading legacy label from current once. Do not delete internal cron schedule representations used by heartbeat or invent a new one-time encoding (Q4=A). |
| C-CONTROL | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/asyncapi.yaml`::RedirectFrame, SessionStateFrame, SessionCloseFrame, SessionCloseAckFrame, AgentSwitchedFrame; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SlashCommand.yaml`; E-APPROVAL | Reuse Stop/redirect/control receipt and one approval ID; expose acting helper/run attribution through existing approval/activity data. Remove session_close/ack, agent_switched and retired command names/aliases. Do not remove session_mode_update, which is the current Auto modifier, merely because handover is removed; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
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

### C-ADDRESS — the existing workspace+agent pair, not a new address format

An addressed agent recipient always supplies **workspace_id + agent_id**, both nonempty existing IDs, and resolves to `main-session-<workspaceid>-<agentid>`. Preserve the source/return pair independently; a source workspace is not an implicit replacement destination. Reuse E-ADDRESS: channel binding already requires WorkspaceID and agent-kind Identity.ID, OwnerOf returns the pair, and OwnedBy matches both. Do not introduce a bare-agent resolver, special Admin address, hash, global agent-address map or alternate channel identity format.

For @ selection, the UI uses the current workspace by default and records the selected agent ID from that workspace's eligible roster. Another workspace is selected explicitly; the request carries that chosen pair through intake/replay/return correlation. Admin is addressable by the default-workspace/admin pair, not admin in every workspace. Invalid/inaccessible/hidden pairs visibly refuse rather than falling back to a different workspace. Existing SessionMessage/MessageFrame target/provenance extensions carry the two required address identifiers; backend-lead defines/regenerates them before consumers. Display syntax/layout belongs to S-FE; this specifies neither a new @ text grammar nor in-chat handover.

### C-ATTENTION — approved main-only waiting/read shape

| Existing schema / property | Shape and semantics | Existing source extended |
|---|---|---|
| Session.needs_attention | **boolean; readOnly: true**. Optional in the general Session shape because non-main responses omit it; **present true/false for valid main responses**, including Admin's default main. Computed, never a client-editable/persisted truth flag. Source failure is a visible error/unknown, not fabricated false. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/Session.yaml`; existing session list/detail and E-ATTENTION |
| Existing main session metadata | One **bounded, pair-wide goal-seen mark** using stable saved outcome/archive identity; no per-user notification record or unbounded seen-ID set. The mark is private storage metadata, not a second main-address/attention store. | E-STORE existing metadata/identity writer and E-ATTENTION::recordGoalOutcome |
| Existing attach operation | Explicit successful foreground open acknowledges only the observed saved outcome bound for that main; normal background prefetch/reconnect must not acknowledge new outcomes. Extend `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/AttachSessionFrame.yaml` with **ack_attention: boolean, optional, default false** to identify that explicit open using the existing attach path. False/absent keeps seen unchanged. Failed/unauthorized attach never acknowledges. | E-ATTENTION::handleAttachSession; generated attach type and existing foreground-open action |
| Existing snapshot/live updates | Reuse SessionStateFrame.pending_asks / pending_approvals, AskUserQuestionFrame card/status updates, ToolApprovalRequiredFrame / ToolApprovalResolvedFrame and GoalOutcomeFrame with existing session/message identity. On relevant updates, refresh/reconcile the existing main list projection; reconnect rereads authoritative sources. Ensure the existing publication/query path updates sidebar-visible mains even when their chat is not open; do not claim the current active-chat-only forwarding already supplies this. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/SessionStateFrame.yaml`; E-ATTENTION/E-APPROVAL and existing list/query invalidation; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221) |

The exact computation for that **main session only** is:

```text
needs_attention = pending structured question card
               OR pending tool approval
               OR unseen saved goal outcome whose ending is
                    met                  (finished)
                    rounds_exhausted     (failed)
                    other                (failed)
```

`stopped_by_user` never lights the dot. Extra chats, children/workers, generic unread messages, task completion notices and plan/global verdicts are not additional waiting sources. If a task/goal outcome truly belongs to this main according to its existing saved session identity it is evaluated there, not guessed in from the displaying agent's other sessions. Pending asks/approvals clear **only on resolution**, never on open/acknowledgement. The user needing an answer is reached with the existing structured question tool; prometheus-prompt-engineer owns that prompt rule, backend-lead wires it, and denied/unavailable tool access remains truthful.

Goal seen state is shared for the pair: one user's successful explicit open clears the acknowledged finished/failed attention **for everyone**. Keep acknowledgement ordered against outcome append under current session identity/locking; an outcome beyond the captured open bound remains unseen. Replay/deduped copies of an acknowledged outcome do not relight it. Recompute from saved identity and bounded metadata after restart; do not create another attention event bus, read-state service or goal notification inbox.

### C-TASK-TOOLS — approved family merge and optional workspace argument

| Canonical surface | Contract/tool-schema change | Preservation / deletion |
|---|---|---|
| create_task | Add **workspace_id: string, optional** to its existing Parameters/argument contract. Absence resolves the calling agent's own current workspace; an explicit nonempty valid authorized ID chooses another workspace. No implicit cross-workspace selection or fallback from an invalid supplied ID. | Extend `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/task.go::TaskCreateTool.Parameters`, `taskCreateToolExecute.buildTask`, reusing E-TASK-TARGET validation/store logic. DELETE the duplicate create_task_in_workspace callable. |
| update_task | Same optional explicit workspace argument on the existing update surface; absence retains own-workspace context. Preserve task identity, owner/assignee/criteria/active-run and same-workspace plan/dependency checks using the existing cross-workspace update validation before any write. An explicit workspace change must be all-or-nothing, never silently move a plan or an execution's selected identity. | Existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/task.go::TaskUpdateTool`; move relevant E-TASK-TARGET validation, DELETE update_task_in_workspace. |
| list_tasks | Add optional explicit workspace argument to existing caller-scoped read query; absence uses own workspace, not an all-workspaces list. Preserve principal/role filters, bounded rows and truthful matched/returned/truncated metadata; a destination is not a cross-principal disclosure grant. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/task_query.go::TaskListTool.Parameters`, `Execute`; consolidate list_tasks_in_workspace into this single list family. |
| delete_task | Keep the existing canonical deletion surface/approval and task-ID authorization. Move duplicate delete_task_in_workspace callers into it; no second delete executor or alias. | Existing TaskDeleteTool in E-TASK-TARGET; delete duplicate sysagent registration/implementation. No extra optional workspace mutation is invented for delete. |
| Gateway/SPA boundary and published tool descriptors | Keep resolved Task.workspace_id and existing task request/list/update response contracts authoritative; define the optional **tool argument** in the contract/tool-schema before implementation, regenerate any changed boundary types and use them only. A standalone REST creation request still supplies its explicit workspace under its existing contract; optional agent-tool targeting is not a guessed missing REST workspace. | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskCreateRequest.yaml`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/components/schemas/TaskUpdateRequest.yaml`, existing Task/ToolCall/ToolRegistryEntry schema surfaces; backend-lead owns actual declarations/regeneration. |

Move Ava/Admin and other callers, role/default policy and catalog/prompt references to the canonical family. Operator Deny remains effective; no new grant/fallback layer, no implicit widening from caller/default workspace, no new task executor, and **no cross-workspace plans**. Preserve current assignment trust/mode/depth, criteria, ownership, task-goal/run/outcome and audit behavior while consolidating their implementations. Old tool names cannot execute as hidden aliases; saved historical tool-call evidence remains readable as inert history under the cutover preservation rule, not a callable compatibility path.

## API and Data

Use the existing session list/detail/message/history, workspace/member, task/run, schedule, command and approval APIs; their source is `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/contracts/openapi.yaml`. Webchat uses the existing WebSocket protocol, not the retired SSE endpoint. Explicit session addresses stay explicit. Unreadable, unauthorized or mismatched metadata must not be treated as a missing record and silently recreate or route to a different workspace.

| Data / execution rule | Backend obligation | Frontend obligation | Authority |
|---|---|---|---|
| Main pair | E-MAIN validates both stored owner/pair and current eligibility/membership under existing session locking; main creation is independent of heartbeat enabled. Native core/custom chat targets qualify; workers and other system agents do not. Admin is the explicit exception: one main in the default workspace only, while his standalone/no-team operator role remains. | E-NAV consumes C-MAIN, never derives another stored map, chooses a latest chat as main, or mutates the owner. | S-ADR::D1/D1.1 |
| Archive and window | E-STORE integrates one content format into UnifiedStore with view membership, UTC day files and a file/byte start mark. Append order follows accepted FIFO, including delayed sources whose original timestamps fall on an earlier day; timestamp-based back-insertion is not a valid implementation. Midnight cannot split a tool group. | Existing message/replay/search projections read the same saved identity. Clear affects current display, not archived search/recall. | S-ADR::D2/D8 |
| Retention | E-RETENTION expires old day content by existing file modification age, default 90 days; disabled retention remains a no-op. Move an expired mark to the first retained complete group; show the agent expired-history information; empty same-ID window if none remains. Do not retain all main content forever. | Eligible main identity remains navigable with empty retained history. Do not label an expired history as a new conversation or a successful clear of stored data. | S-ADR::D2; Q2=A |
| Intake | E-QUEUE accepts ordinary human/parent instructions in one FIFO per actual session and retains each as a separate message. One runner owns settlement; ready batches join at safe steps or immediately after natural turn end. Provenance save and admission are one truthful acceptance boundary. | Show each item and truthful accepted/applied/held/superseded/refused state through current status surfaces; do not call an echo a save or a save model consumption. | S-ADR::D3/D6; #1214 |
| Ordinary limits | E-LIMIT checks body, authenticated sender+target rate, ordinary item count, aggregate waiting bytes and current model fit before acceptance. Joint ready content includes media/tool/pinned costs after allowed old-window sliding; accepted control content is not silently dropped. | Visible refusal identifies the limit. No successful send bubble for refused admission, no silent truncation or optimistic settings claim. | S-ADR::D3; Q1=A; #1216 |
| Reports | E-INBOX keeps current report caps/exemptions/result-delivery limits while all **accepted** helper report kinds become wake-eligible for an idle non-stopped parent. Current code couples cap exemption to wake classification; changing wake eligibility must not accidentally remove the preserved report rate/count checks. This is an adaptation inside the existing classifier/admission, not another inbox. | Surface actual rejection/not-delivered information and acting child; has_more:false alone is not a no-loss proof. No automatic retry, result-defer or overflow queue. | S-ADR::D3.1/D5; Q-R2-7; #1211 |
| Task modes | E-TASK/E-SCHEDULE derive MAIN for any MAIN-assignee task; recurring worker CONTINUE; one-time worker ISOLATED; scheduled isolation override ISOLATED for either. MAIN always beats CONTINUE. Every MAIN run has a fresh real child of the assignee's main; CONTINUE keeps the conversation but gets a new run ID. | One scheduled-task checkbox **run isolated (one session per execution)**; no mode selector. Use existing Calendar/task history views; display real parentage, not a synthetic monitor edge. | S-ADR::D5; 10:13 |
| Task admission/results | Narrow task-origin admission allows a future authorized scheduled MAIN child under a stopped main without waking that main; launch is ordered with Stop. Capture starter-MAIN main plus assignee-MAIN main at start and dedupe. Deliver brief engine header plus full stored result for each actual done/failed/stopped run, with reason. Skipped did not run; no per-retry notice. | Activity distinguishes starter monitoring from assignee/parent authority, shows MAIN once, and links stored results even when a captured recipient disappeared. Never guess a substitute recipient. | S-ADR::D5/D6; Q-R2-1/3; 10:25 |
| Peer/source addressing | E-INBOX plus existing send_message transport address explicit workspace+MAIN-agent pairs, including other workspaces when needed, without a delegation edge. Message carries only addressed text/explicit material, not whole source history. Receiver keeps permissions; sender gains no steering/Stop/context/approval rights. Each answer names its sender/return correlation; missing destination is visibly refused. | Replace mention-switch action with message addressing; guest icon/name and persisted identity match replay. Owner sees request and answer; answer wakes idle non-stopped owner once, with silence allowed and no empty bubble. | S-ADR::D7; Q3=B |
| Idle/deletion | E-RECAP runs default-on recap after 30 inactive minutes, resetting on any turn/message and excluding active settlement; repeated idle episodes remain possible. E-DELETE consolidates existing entry points. Team removal hides; deletion removes owned sessions/memory while preserving guest answers labelled deleted agent. | UI deletion warns and requires two confirmations; API/tool retain existing single approval. Report partial cleanup, preserve guards, and never expose a deleted identity as a runnable guest. | S-ADR::D9; Q5=A/Q6=B |

MAIN task children inherit their **main parent's** approval state, not the starting extra chat's modifier; target restrictions and unattended immediate refusal still apply. This may be more permissive than the starting chat and must be disclosed. Current full-result delivery can exceed report/context limits; accepted Q-R2-7 does not promise universal delivery or model fit (S-ADR::D10/Consequences). (Approval inheritance: separate #1221 removes the per-agent Auto off-switch; helper Auto follows the parent.)

### Narrow install/upgrade cutover — required, not an ongoing compatibility system

Run the existing E-MIGRATE/store initialization path's bounded saved-chat conversion before normal new-format writes/main creation can publish an empty replacement over existing data. Reuse the current readers for formats this installation already supports and the current atomic identity/archive writers. Preserve saved content, per-entry author identity, effective agent/workspace binding, existing model-view data/window semantics and recall/result references. Existing heartbeat history becomes the computed pair's main; ordinary saved chat/task/worker/extra-session identities remain reachable and continuable. Re-key affected existing heartbeat/session references through the current owners/writers, not a second address map or new execution.

Stage and validate each converted archive/metadata before recording that session's completed conversion. Repeating the import or crashing around publication must not duplicate, truncate or overwrite history, lose owner/workspace or reset continuation into a fresh chat. Keep source data until successful verified conversion; an unreadable/mismatched source or failed stage/publish is a visible migration failure identifying the affected chat, not log-only skipped success. A different pair already at the computed target is a conflict, never an overwrite. Cutover normalization can remove old owner/type/entry compatibility from **runtime** readers only after importing their supported saved data. Steady state has one store/format and no legacy fallback/dual writer. Unrelated cron/tool aliases/old task triggers and queued-input restart recovery are not authorized by this exception.

### New things and why nothing existing fits

| Extension to existing path | Existing candidate | Why the decided behavior needs this extension |
|---|---|---|
| Entry view membership and file/byte mark | E-STORE current transcript/window | Current transcript lacks complete provider/view representation and full archive reads cannot express bounded single-format windows |
| Authenticated pair/return correlation | E-INBOX/E-ADDRESS current child adapter and channel pair | Peer is not a child; mixed senders need preserved original pair/return correlation, not one chat-level fallback |
| Captured run recipients and scheduled isolation flag | E-TASK/C-TASK | Creator/late assignee lookup and selected mode fields cannot express fixed starter/assignee capture plus one checkbox |
| Held/refusal/aggregate intake state | E-QUEUE/E-LIMIT/E-INBOX | Count alone cannot bound bytes/model fit or show held/refused delivery; extend current intake/status, one aggregate setting |
| Saved-chat cutover importer | E-MIGRATE current readers/import hooks | Existing helpers do not convert the decided archive/main identity preserving full binding unchanged; mature them for this cutover only |
| Shared bounded goal-seen mark and computed needs_attention | E-ATTENTION and existing main metadata/attach | Pending states exist, but saved goal outcomes lack a seen writer; no new notification/attention/event service |
| Optional explicit workspace argument on merged task tools | E-TASK-TARGET ordinary tools and duplicate cross-workspace validation | Ordinary args cannot choose another workspace; reuse validators/store in canonical tools and delete the duplicate family |

## User Stories & Acceptance Criteria

Priorities below are verification priorities, not new release routing. All fourteen are required feature scope, Priority P1. Independent tests mean a capability can be tested with its real surrounding seam and deterministic inputs; they do not authorize separate landings.

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
5. **Given** an opened external worker has a valid native conversation, **When** live instruction text is sent, **Then** the existing driver interrupts and resumes that actual native conversation with the instruction; missing native identity or failed resume is visible, never successful no-op or a silent fresh conversation.

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
2. **Given** two explicit workspace+main-agent pairs with no delegation edge, **When** one messages the other, **Then** same- or cross-workspace communication is allowed within receiver permissions but grants no Stop, context-read or inherited approval authority; invalid/unauthorized pairs are refused. (Approval inheritance: separate #1221 removes the per-agent Auto off-switch; helper Auto follows the parent.)
3. **Given** a mixed batch from distinct admitted web/connector senders, **When** addressed answers are produced, **Then** each answer returns only to its sender with its original instance, chat/thread and correlation preserved.
4. **Given** an answer has no usable return address, **When** delivery is attempted, **Then** it is visibly refused and the agent must address a sender, not guess a destination or broadcast.
5. **Given** a permitted agent uses the merged task family, **When** it creates, updates or lists tasks with or without an explicitly chosen workspace, **Then** absent choice uses its own workspace and another workspace is used only when actively chosen; duplicate tool names are gone, Operator Deny and existing task/plan/ownership checks remain, and no cross-workspace plan is created or moved.

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

### User Story 13 — The main's waiting signal has a clear source (Priority: P1)

A person wants an agent row to flag the main conversation's outstanding question, approval or unseen finished/failed goal, not unrelated activity in extra chats or helpers.

**Why this priority:** attention must be truthful and clearing it must not answer a question or approve a tool.
**Independent Test:** seed the four main-only sources plus unrelated extra/helper sources; use two users, reconnect and an explicit main open.

1. **Given** an eligible main including Admin's default main, **When** one of the four decided sources becomes outstanding, **Then** that pair needs attention; unrelated extra/helper activity and a user-stopped goal do not light it.
2. **Given** unseen finished/failed main-goal attention, **When** one user explicitly opens that main successfully, **Then** its observed goal attention clears for everyone, while any pending question/approval stays until resolved and a later racing goal outcome remains unseen.
3. **Given** a reconnect, replay or failed background lookup, **When** waiting state is refreshed, **Then** current authoritative pending/outcome/seen state is restored without acknowledging from prefetch, relighting an already seen replay, or reporting missing source data as false.
4. **Given** an agent needs the user's answer, **When** it requests that answer, **Then** it uses the existing structured question tool and the main signal follows that card's pending/resolved state; prompt text is delivered by the prompt owner, not a new question/event system.

### User Story 14 — The cutover does not lose saved conversations (Priority: P1)

A person upgrading a live install wants saved chats to keep their history and agent/workspace and still accept follow-up, with existing heartbeat conversations becoming main rather than disappearing.

**Why this priority:** the founder explicitly commissions this exception because the previous upgrade broke his live instance.
**Independent Test:** use a frozen pre-cutover saved-chat fixture, then install/upgrade twice and compare history, binding, main conversion and actual follow-up.

1. **Given** supported saved chats and heartbeat history, **When** the install/upgrade cutover completes, **Then** saved content/agent/workspace and ordinary chat identity remain reachable and continuable, and each existing heartbeat becomes the computed main with its history.
2. **Given** a prior successful or partially published import, **When** the cutover is repeated after a crash, **Then** it completes idempotently without duplicate or truncated history, a different owner or a fresh replacement chat.
3. **Given** a source is unreadable/mismatched or conversion publication fails, **When** migration is attempted, **Then** affected chat failure is visible, source data is retained and no successful empty/incorrect conversion is claimed.
4. **Given** a fresh install or an already converted install, **When** normal chat runs, **Then** one current store/format is used, no legacy runtime fallback is kept and no new execution or old queued input is boot-dispatched by migration.

## UI Screens and States

This specifies behavior/state, not sidebar/identity styling. S-FE remains the layout authority. Use existing surfaces rather than introducing another dashboard or task-result card.

| Existing surface | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Agent navigation, chat attach and session search (E-NAV) | Existing load state; no automatic extra chat while main lookup is pending | New protected main with no content is valid; no worker main is invented | Refused/corrupt/unauthorized attach remains visible; do not silently select another session/workspace | Cached roster is identified as stale; retained history may be empty after expiry | Pair's main opens; agent-row New chat deliberately creates an extra; history search reaches retained entries |
| Main attention in existing agent navigation | Current main-list state loads; no guessed false | False only when all authoritative main-only sources are clear | Source/read failure is visible or stale/unknown, not cleared | Goal attention and pending ask/approval sources clear differently; two users share the main seen mark | Computed true/false matches exact main-owned sources; explicit open acknowledges observed goals only |
| Composer, command/mention palette (E-COMMAND) | Existing command readiness gate prevents accidental submission before command discovery | No matching allowed agent/command is an empty result, not a fallback target | Inline refusal identifies the failed action/limit; disconnected send is not accepted | HELD input stays visible with release/discard; current running step is not claimed cleared before its boundary | Same owner; addressed guest answers carry their own identity; canonical clear/sessions/navigation actions |
| Background Activity (E-ACTIVITY) | No guessed running status or token count | No current activity is empty; retained results remain in existing task/run/history views | Projection/delivery failure stays visible; no loss-free inference from an empty inbox | Running/queued/waiting, stopped-with-live-helpers and missing usage are distinct; absent usage is not zero | One row per actual helper/run; MAIN task not duplicated; own Open link, actual available tokens |
| Existing approval modal (E-APPROVAL) | Existing pending/resolution behavior | No request means no approval card | Existing refusal/expired/resolution errors remain | Label/link acting helper/run; external-CLI exception is disclosed, not a fake native approval | One ID, one queue, existing once/allow/deny actions and target restrictions; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
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
| Parent versus peer | Authenticated human/authorized ancestor instructions have steering authority. Helper reports and MAIN-peer messages cannot assert it in text; messaging grants no tree Stop, full context or approval inheritance. Membership/connector ownership and session-scoped recall/browser/tool isolation remain. | S-ADR::D3/D7; **A channel belongs to one (workspace, agent) pair, in both directions**; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
| MAIN task approval | Main parent grants/modifier govern within target restrictions; starting extra-chat modifier does not. Disclose possible loosening. Scheduled/unattended Ask needing a person refuses immediately, even with an open chat. | S-ADR::D10; **Shell permission modes: Ask / Auto / God Mode; drop the block list; one rule format**::D10; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/security.md`::Auto-approve; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/tools.md`::Limits and things to watch; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
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
| A main has pending structured question/approval or unseen mapped goal outcome | Show attention for that main only; one explicit main open shares seen-goal acknowledgement, while unresolved asks/approvals remain |
| Install/upgrade performs the saved-chat cutover | Preserve supported saved history/agent/workspace and actual continuation, turn heartbeat history into main, repeat safely and report source/conversion failure visibly |
| A permitted task tool has no workspace choice / an explicit choice | Use own workspace / explicitly chosen valid workspace through the one canonical family, with no cross-workspace plans |

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
| One user opens main while another watches; pending ask/approval and new goal outcome race open | Shared bounded goal seen only; unanswered/pending sources and later outcomes remain | US13.1–3 |
| Saved heartbeat and ordinary chats, interrupted/repeated migration or I/O/conflicting target | Preserve supported data/binding/continuation or visible affected-chat failure with source retained; no new runtime/empty overwrite | US14.1–4 |
| Task workspace omitted/explicit/null/invalid and foreign plan/dependency/Operator Deny | Own workspace by default; explicit valid target only; existing validation/permission refusal, no implicit fallback or plan move | US8.5 |

## Explicit Non-Behaviors & Safeguards

### Qualitative prohibitions

The system MUST NOT create another session store, address map, queue, task executor, scheduler, report retry/overflow service, approval modal or result dashboard: the relevant mechanisms already exist (S-ADR::Alternatives considered). It MUST NOT copy an originating conversation wholesale to an addressed guest, invent child edges for peers/independent tasks, treat visibility as Stop authority, or broaden workers into fresh chat/mention targets.

It MUST NOT add a summary step, change helper recap/memory, create a self-only nesting rule, per-main spend controller or extra admission quota, resume stopped mains for reports, replay lost waiting input at boot, or keep ongoing upgrade adapters behind deprecated aliases. The 17:15 saved-chat cutover import is the only authorized compatibility exception. S-FE layout questions, #1212/#1213/#1215/#1217 fixes and #1206 privacy separation are not side work in this feature.

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

## Prerequisites, Tech Stack and Deployment

| Topic | Existing requirement / source; no new platform or dependency choice |
|---|---|
| Runtime | Single pure-Go binary with embedded SPA, current file-backed session/config/run data; Linux, macOS and Windows only. No new mandatory database, sidecar, service or C dependency (S-RULES Hard Constraints; S-ADR::D10). |
| Build toolchain | Go minimum 1.26.6 from `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/go.mod`::go directive. Build tags remain goolm,stdjson and CGO_ENABLED=0 per S-RULES; no whole local Go suite/build is requested. Node is build/development tooling for the existing embedded UI, not a new runtime requirement. |
| Frontend/testing | Existing React 19/TypeScript/Vite/design-system/AssistantUI stack and existing Go, component and browser tests. `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/package.json`::scripts.typecheck is tsc -b --noEmit; source pins/scripts, not recalled newer library behavior, are authority. |
| Hardware/resources | No new hardware minimum is specified. Preserve existing memory-based admission and <10 MB security-feature overhead constraint; independently measure, never claim it from document size or mock tests. |
| Services/network | Existing configured model provider for model work and optional configured connectors/installed external CLI workers for their respective features. Storage/metadata do not acquire a new online service; provider/connector/CLI failure remains visible. |
| Accounts/credentials | Existing authentication and configured provider/connector/CLI credentials. UAT uses isolated homes/accounts and openrouter + deepseek/deepseek-v4.1-flash. No credential is copied into this spec or scratch receipts. |
| Startup/cutover | E-MIGRATE's bounded saved-chat conversion precedes normal new-format/main publication on install/upgrade; visible failure/source preservation rather than an empty fresh instance. Fresh/converted normal boot uses the existing store/recovery; no queued-input replay or permanently hot main runner is added. |
| Shutdown/health/observability | Existing gateway startup/shutdown and diagnostic/log/UI error surfaces remain; no new health endpoint or telemetry. Migration/admission/receipt/delivery/partial-delete errors are not successful readiness/delivery. Existing explicit Stop remains independent of viewer disconnection. |

### Development Setup

No new developer service, package installation, container or deployment command is needed for this feature. Use the existing project setup in S-RULES; do not add a second session-core bootstrap. In the implementation flow, backend-lead runs the existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/scripts/gen-contracts.sh` and commits its schemas/artifacts before consumers. The verified frontend check is the existing `npm run typecheck`, not bare tsc --noEmit; heavy build/test/race/platform gates stay in CI. Team-lead supplies the exact candidate binary and real isolated UAT instance; this architect does not run a gateway/local product build here.

A missing embedded SPA in a fresh worktree, omitted required build tags, unconfigured provider or unavailable CLI is the existing setup/instrument boundary, not a feature acceptance pass/fail to manufacture with a mock. Actual deployment/upgrade acceptance uses supported saved fixtures and the joint candidate, not a local dev-server preview or a bare source scan.

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
| Session-core questions | All eight are closed, including 17:45 shared seen/mapping and task-tool MERGE | Different limits, deleting current once, guessed reply destination, automatic hidden-main wake or tool/API double confirmation |
| Known accepted risk | Restart waiting-input loss; result delivery/context caps; shared channel privacy; MAIN-parent approval can loosen relative to extra chat; CLI subprocess-kill exception | Extra durability, universal result fit, privacy separation or native-popup protection for external calls |
| Counterpart | S-FE is Proposed; founder 17:15/17:25 decide migration/Admin/cross-workspace/pair addresses and waiting sources, while unrelated layout/identity questions remain there | A latest-session main fallback, avatar/storage/migration decision, unsupported saved-history conversion, or separate sidebar landing |
| Scoped frontend branch removal | S-ADR::DEL-F preserves current shared producer behavior; each implementing deletion needs proof of its canonical current replacement | Dropping current non-stream/kickoff/evicted-owner output, choosing a latest goal, or guessing missing producer/position |

There is no new session-core product assumption in these warnings. Any genuinely new unresolved choice goes to `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/squads/session-core-adr-20261007/QUESTIONS.md` as the next OPEN-Qn with context/options/recommendation, and the affected document requirement is held. Do not implement a guessed answer. Frontend counterpart questions remain with that ADR's founder interview; referencing it does not mark it Approved.

## Explicit DELETE Requirements

Every row below is part of FR-038/039 and names the existing path and its replacement or nothing. This is an implementation obligation, not a claim deletion already happened. **DELETE means remove the runtime implementation, registration, callable alias, contract support and compatibility-only tests/docs, while preserving the named current behavior and safety assertions.** The 17:15 saved-chat importer can use current readers only within the bounded cutover before runtime removal. #1221's per-agent Auto off-switch deletion is **outside this inventory/spec implementation scope** and remains an explicit integration dependency.

DEL-01–DEL-23 and DEL-F01–DEL-F43 below are copied from the amended S-ADR so the caller/replacement boundaries are identical. The implementing lead finishes the controlled direct-caller sweep and reports any additional scoped live compatibility path rather than retaining it or silently widening scope. Shared current kickoff/non-stream/reconnect/evicted-owner/control-error/plan behavior is not removed merely because an old branch shares it. A missing current canonical producer is a reported implementation gap, not a guessed identity/goal rule.

### Backend / shared paths

| Delete ID | Existing scoped path (`file::symbol`) | Replacement or nothing |
|---|---|---|
| DEL-01 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/schedules.go::pickSession` — `sched-main-<agent>` standing-session branch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/unified.go::NewHeartbeatSession` heartbeat-only identity/enable-coupled lifecycle | Mature that creation/get-or-create/protection path into the one computed main session. Delete separate stored heartbeat session-address mapping, not heartbeat configuration/origin. |
| DEL-02 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/ordinary_execution_admission.go::awaitPreviousOrdinaryExecution`, `previousExecutionSettleBudget`, `ErrPreviousExecutionPending`; ordinary “still finishing” refusal mapping | Existing session runner owns settlement before next admission; retain internal duplicate-execution tripwire, not user-facing early-arrival refusal. |
| DEL-03 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/session_worker.go::workerInboxCap`, `sessionWorker.inbox`, system-turn buffering/probe and legacy bare-goroutine fallback in `dispatchSystemMessageToSessionWorker` | Existing per-session steering queue/runner and report inbox. No capacity-full bypass dispatch. |
| DEL-04 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/steering.go::manualSteeringScope`, `push`, `dequeue`, manual fallback drain, `SteeringOneAtATime`, `parseSteeringMode`; `SteeringMode` configuration | Resolved session address and the same FIFO, always all-at-once. No manual/unscoped queue or user-selectable dequeue modes. |
| DEL-05 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/commands/cmd_clear.go::clearCommand` old `/new` action/hidden alias; `/resume` old client name; `/agents` old selector/list action | `/clear` safe-point context/display reset, `/sessions` search, `/switch-agent` MAIN-session navigation. Old names do not survive as hidden aliases. |
| DEL-06 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/commands/builtin.go::BuiltinDefinitions` deprecated `/start`, `/show`, `/list`, `/switch`, `/check`; `/channel` alias/support wherever wired; their hidden/deprecated execution/menu guidance | Nothing for removed names. Current noun commands/actions remain as allowed by the final capability table. Remove Telegram compatibility `/start`, not a new greeting workflow. |
| DEL-07 | Existing `switch_agent`/handover implementation, catalog, prompt/tool-exclusion entries; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/unified_write.go::SwitchAgent`; routing handoff pins and `agent_switched` consumers | Direct MAIN-peer messaging and session navigation. The slash navigation command is not a replacement tool with the same name. |
| DEL-08 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/memory.go::TriggerLazy`, `TriggerExplicit`; explicit session-close WS frame, handler and ack; obsolete lazy/explicit invocation paths | Existing idle/bootstrap recap. **Joined stays**: `RetrospectiveTool.Execute` uses it. |
| DEL-09 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/unified.go::migrateLegacy`, `writeUnifiedMetaDirect` unrestricted migration behavior and runtime missing-type fallback; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/memory/migration.go::MigrateFromJSON` old ongoing boot import behavior | **DELETE ongoing/unrestricted compatibility, not the source readers needed for the 17:15 cutover.** Mature existing readers/import entry points into one install/upgrade-only idempotent saved-chat conversion before current-store publication. Preserve history/agent/workspace and heartbeat→main, retain source on failure and report failure visibly. After that cutover, only the new format reads/writes; no dual production reader/backfill. D11 defines the narrow exception. |
| DEL-10 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/instance.go::initSessionStore` JSONL/SessionManager fallback; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/manager.go::SessionManager`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_session.go::GetAgentStore`, `getLegacyAgentStore`, legacy scan/merge branches | Existing shared UnifiedStore after the 17:15 cutover importer has consumed supported saved chats through their existing readers. DELETE runtime per-agent/SessionManager fallback, not a reader while it is still required solely by that bounded import. Source errors remain visible; no fallback to a different history. Move all scoped callers, including task/verifier/delete/read paths, before runtime removal. |
| DEL-11 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/daypartition.go::SessionMeta.PostLoad` old multi-agent backfill; legacy owner/active-agent/type/token/truncation/provenance defaults in scoped storage/readers | Explicit current identity/entry fields after the cutover; old-format normalization is confined to its importer, never a current-reader fallback. Preserve normal current zero/optional semantics, legitimate user/system zero-token entries and authenticated anonymous/shared modes. Contributor identity stays per entry, not handover state. |
| DEL-12 | Separate model content history/backend rewrites, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/memory/window.go::RollbackWindow` destructive rewrite; whole-history `SetHistory` hydration/compaction paths; in-place transcript truncation/tool-result updates | One archive, current window/projection metadata and append-only correction effects. Keep provider-valid replay and full-result references; no compatibility second writer. |
| DEL-13 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/steer_classify.go::Classify` pre-ADR-origin leniency/legacy delegate branch; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/boot_sweep.go::SteerBootRecovery.failLegacy`, `failedReasonPreADR091NotResumable`; obsolete legacy class enum/support | Current record/metadata validation and existing restart-stop settlement. Malformed current children do not become roots merely because an old compatibility branch did so. |
| DEL-14 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/message_inbox.go::backfillSeq`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/workspace/delegation.go::legacyModeDirect`, `DelegationEdge.UnmarshalJSON` old-mode migration | Current persisted inbox sequence and current direct/task edge validation. No legacy sequence/mode rewrite. Preserve normal dedupe/ack and non-self trust checks. |
| DEL-15 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/cron/service.go::migrateOwners`, `migrateOwnersUnsafe`, `AddJob` back-compat creation; boot default-agent migration wiring | Existing `AddJobFull`/JobSpec with explicit authorized owner and derived mode. No owner-less job upgrade backfill. |
| DEL-16 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/sse.go::SSEHandler`, `newSSEHandler`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/gateway_boot.go` backward-compat SSE registration | Existing WebSocket persistent-session transport; remove matching old chat-stream contract/callers, not add another streaming fallback. |
| DEL-17 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/websocket_cancel.go::u11CollectDescendantSessionIDs` signature shim; stale prose about the earlier u11 name in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/cancel.go` | Existing `CollectDescendantSessionIDs` and one Stop path, preserving returned errors/selected execution. Nothing replaces the gateway wrapper kept solely for an older signature. The current AgentLoop.collectDescendantSessionIDs is a store-bound canonical adapter, not a live u11 compatibility shim; preserve it or route its callers to the same collector without changing behavior. |
| DEL-18 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_goal_terminal.go::mintLegacyTaskGoal`; legacy task-goal backfill branches reached from `activateTaskGoal`, task create/edit/wire | Existing fresh task-goal creation/update and task-owned goal activation. Keep intentional scratchpad/non-goal behavior; no upgrade-only goal minting. |
| DEL-19 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_trigger.go::triggerToCronSchedule` old every/cron_expr branches; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/gateway/task_occurrences.go::expandCronServerZone`, legacy every projection; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/calendar/CalendarEventSlideOver.tsx::isLegacyTrigger`, old-trigger preservation/preview | **DELETE** old every_ms/cron_expr adapters and compatibility promises; recurring Calendar work uses existing RRULE/compileRecurrence/next-occurrence/rearm path. **KEEP once/at_ms**, the current Calendar one-time authoring format, and remove its misleading legacy label (15:55 Q4=A). Current buildTriggerForSave/triggerToCronSchedule use it through the same cron at-job engine as RRULE; no replacement one-time scheduler/encoding. |
| DEL-20 | R-CLI no-op `Input` success and fresh-run `Resume` implementations presented as steering/resume; old CLI steering exclusion/help | Existing drivers matured for interrupt plus actual native-ID resume. No invented ID, successful no-op or silent fresh-session fallback. |
| DEL-21 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/config/validate.go::MigrateLegacyToolPolicyKeys`, `migrateLegacyToolPolicyMap`, old tool-key rename entries feeding switch_agent; loader wiring for those retired aliases | DELETE upgrade-only tool-key remapping, especially hand_off/return_to_default → switch_agent. Current canonical policy names/global ceiling/per-agent tightening remain; do not delete ReconcileToolPolicyCeiling or resurrect fail-closed backfill. |
| DEL-22 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/steering.go::InjectSteering` design-name alias and `EnqueueSteeringMessage` status-stripping compatibility wrapper; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/delegate_followup.go` old/basic steering-sink fallback interfaces | DELETE aliases/signature compatibility; use the existing canonical session-targeted, status-carrying steering sink. Preserve post-finish queued status, receipt/control identity and visible failures; no second sink implementation. |
| DEL-23 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/sysagent/tools/task.go::TaskCreateTool`, `TaskUpdateTool`, `TaskDeleteTool`, `TaskListTool` — create_task_in_workspace/update_task_in_workspace/delete_task_in_workspace/list_tasks_in_workspace; their task registrations in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/sysagent/tools/registry.go::AllTools`, category/policy/default/prompt/wire/caller/compatibility-only references | **DELETE the duplicate callable family and its implementations/registrations/aliases.** Move shared validation/store behavior and callers into existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/task.go::TaskCreateTool`, `TaskUpdateTool`, `TaskDeleteTool` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/task_query.go::TaskListTool`. Canonical create/update/list take optional workspace_id, unset = own workspace; explicit other only. Preserve role/Operator Deny, assignment/criteria/owner/plan invariants and existing delete approval. No new executor or cross-workspace plans; historical saved tool-call evidence is not a live alias. |

### Frontend paths

| Delete ID | Existing source path/symbol | Old live branch | Existing replacement / boundary |
|---|---|---|---|
| DEL-F01 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/hooks/useSlashMenu.ts::runClientSlashCommand`, `executeSlashCommand`, `resolveClientCommand` | `name === 'new' || name === 'clear'`; palette/typed definition resolution also matches `c.aliases`. `/clear` is explicitly legacy and hidden. | Existing startNewSession only behind the founder-decided agent-row extra-chat action; canonical /clear is the new safe-point window/display action, not /new or a hidden alias. |
| DEL-F02 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/ChatScreen.tsx::commandLabelsWithAliases` | Adds `/${alias}` to built-in labels so old command aliases suppress skill chips. Live UserMessage/virtual user rows consume it. | Canonical command labels already come from `commands`; nothing for retired alias names. |
| DEL-F03 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/api/agents.ts::isWorker` | Third test `a.type === 'worker'`, explicitly retained for stale payloads. | `Subagent` / `subagent_3p` tests already present. |
| DEL-F04 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/agentKind.ts::isWorkerType`, `agentKindFlags` | Legacy `worker` recognition and `(type === 'worker' && executor.kind === 'external-cli')` classification. | Existing modern type-based native/external classification. |
| DEL-F05 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/agents/AgentProfile.tsx::AgentProfile` (kind derivation) | Live profile derives worker/native/external gates through the legacy-aware `agentKindFlags`; the legacy case is used for heartbeat eligibility. | Same helper's modern type cases; no replacement worker kind. This row maps the profile consumer, not unrelated provider compatibility. |
| DEL-F06 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/api/sessions.ts::RawSession` | `type RawSession = _RawSessionInternal`, explicitly “Alias for backward-compat within this file”; used in adapters/request casts. | `_RawSessionInternal` already exists; no new wire type. |
| DEL-F07 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/api/sessions.ts::rawToSession` | Missing legacy type becomes `raw.type ?? 'chat'`. | Existing `raw.type`; **nothing** when the input lacks it. Current schema still makes type optional, so this map does not invent a new required-field contract. |
| DEL-F08 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/session.ts::useSessionStore.enterWorkspaceChat` | Saved descriptor `id === '__pending'` treated as fresh; comment explicitly describes pointers written by older code. | Current writers already exclude transient `__pending` from saved real descriptors. **Nothing** for an old saved sentinel. Keep the live first-send sentinel distinct from this old saved-pointer branch. |
| DEL-F09 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/useSelectSession.ts::selectSession` | Joined-session agent resolution falls back `active_agent_id ?? agent_id`, supporting the single-agent shape described by the adapter. | Target D1.1 requires existing **agent_id** as immutable owner. Delete handover active-agent fallback, update all current readers to that one field; no dual owner-field chain. |
| DEL-F10 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/routes/_app/sessions.$sessionId.tsx::SessionRoute` | Same single-agent fallback in `headerAgentId`. | Target D1.1 requires existing **agent_id** as immutable owner. Delete handover active-agent fallback, update all current readers to that one field; no dual owner-field chain. |
| DEL-F11 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/session.ts::resolveRememberedSessionFromServer` | Same fallback while restoring a saved session. | Target D1.1 requires existing **agent_id** as immutable owner. Delete handover active-agent fallback, update all current readers to that one field; no dual owner-field chain. |
| DEL-F12 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/search/SearchModal.tsx::bucketByAgent` | Same fallback in agent grouping, then `unknown`. | Target D1.1 requires existing **agent_id** as immutable owner. Delete handover active-agent fallback, update all current readers to that one field; no dual owner-field chain. |
| DEL-F13 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/OmnipusRuntimeProvider.tsx::resolveLastActiveAgentId` | Two cached-session fallback reads use `active_agent_id ?? agent_id`. | Target D1.1 requires existing **agent_id** as immutable owner. Delete handover active-agent fallback, update all current readers to that one field; no dual owner-field chain. |
| DEL-F14 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/ws.ts::WsSubagentStartFrame`, `WsSubagentEndFrame`, `WsReplayMessageFrame`, `WsRateLimitFrame`, `WsToolApprovalRequiredFrame`, `WsSessionStateFrame`, `WsReceiveFrame` | Live legacy type aliases, not historical mentions. | Existing generated `SubagentStartFrame`, `SubagentEndFrame`, `ReplayMessageFrame`, `RateLimitFrame`, `ToolApprovalRequiredFrame`, `SessionStateFrame`, `ServerFrame`. |
| DEL-F15 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` | Imports/casts `WsSubagentStartFrame` / `WsSubagentEndFrame`. | Corresponding existing generated canonical types. |
| DEL-F16 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/replay-and-status-frames.ts::handleReplayMessageFrame`, `handleTurnCanceledReplayEntry`, `handleReplayAndStatusFrame` | Imports/uses `WsReplayMessageFrame` / `WsRateLimitFrame`. | Corresponding existing generated canonical types. |
| DEL-F17 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/toolApproval.ts::ToolApprovalStore` | `enqueue` / `reconcileWithSessionState` signatures use the legacy WS aliases. | Generated `ToolApprovalRequiredFrame` / `SessionStateFrame`; retain the one current approval store. |
| DEL-F18 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/types.ts::ChatStore.handleFrame` | Uses legacy union alias `WsReceiveFrame`. | Existing generated `ServerFrame`. |
| DEL-F19 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/ws.ts::WsConnectionCallbacks.onFrame`, `WsConnection._flushBatch`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/OmnipusRuntimeProvider.tsx::WsLifecycle` | Explicitly legacy single-frame callback remains live: provider supplies `onFrame: handleFrame`; flush falls back to looping it. | Existing `onFrames(batch)` callback is already defined and preferred by the transport. This is callback-shape reuse, not a new event schema. |
| DEL-F20 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/ws.ts::SessionCloseFrame`, `SessionCloseAckFrame` imports/re-exports; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/runtime-state.ts::SESSION_SCOPED_FRAME_TYPES` | Retained explicit-close type surface and live `session_close_ack` classification. No frontend sender was found. Explicit close is among the retired triggers recorded in the session-core brief. | **Nothing**: no replacement person-triggered close action is present. Generated union/spec changes remain backend-owned; this report edits none. |
| DEL-F21 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/first-send-frames.ts::handleFirstSendFrame` | A `session_started` without `client_message_id` binds the known chat but retains unconfirmed first-send state and returns to the old reducer. | Existing correlated `confirmFirstSend` path and `message_status` receipt path; **nothing** establishing a save from an old uncorrelated ack. |
| DEL-F22 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` (`session_started`) | Shared tail explicitly retained for “Kickoff and legacy acknowledgements”: old ordinary ack migrates the pending bucket/agent/mode without a correlated receipt. | Existing ordinary `handleFirstSendFrame` / `confirmFirstSend`. Kickoff resolution is a separate current use of this tail, not retired by this compatibility removal. |
| DEL-F23 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/first-send.ts::firstSendBlocksQueue`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/store.ts::maybeDrainNext` | Explicit legacy exception lets a bound, unconfirmed chat stop blocking queued sends: `pending.status !== 'unconfirmed'` is part of the gate. | Existing correlated save/recovery gating; **nothing** that confirms the legacy ack. |
| DEL-F24 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/types.ts::OutboundQueueItem`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/store.ts::drainQueuedMessage`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/ChatScreen.tsx::ChatScreen` (`queuedMessages`) | String queue items retained for old persisted/test state are a live branch: drain sends an uncorrelated string; display filters strings out. | Existing `QueuedOutboundMessage {id, content, timestamp}` branch preserves correlation and display. **Nothing** supplies the missing identity/time for an old string. |
| DEL-F25 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` (`done`, `isReplayTerminatorDone`) | Vestigial replay terminator is identified by `frames_emitted` with no tokens/cost. Still bakes stranded calls and drains. It does **not** currently finish catch-up. | Existing `catch_up_complete` handles catch-up completion; existing `bakeToolCallsByOwner` is the bake primitive. **Nothing** in the read completion branch yet duplicates this terminator's stranded-call flush; do not invent it. |
| DEL-F26 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/hooks/useRunningActivity.ts::resolveSpanAgentId` | Pre-steered-session attribution workaround prefers originating delegate call's `params.agent_id` over `span.agentId`; the source comment says the old emitter gap was superseded. | Existing `span.agentId`, populated by the start/end reducer. Backend universality is not re-audited in this frontend map. |
| DEL-F27 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/hooks/useRunningActivity.ts::activityStatusForSpan` | Explicit legacy fallback `default: return span.status`. | Existing lifecycle-state mapping; **nothing** for a span that has not received lifecycle data. The source also allows a live pre-state interval; no new display choice is made. |
| DEL-F28 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/ActivityPanel.tsx::ActivityRow` | Explicit legacy dot/label fallback `getSpanStatusDot(item.status)` when lifecycle is absent. | Existing `getLifecycleStatusDot(lifecycleState)`; **nothing** before a lifecycle is known. |
| DEL-F29 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` (`subagent_end`, `subagent_message`, `subagent_state`) | Full backwards message scans when span index misses; end handler explicitly labels this the legacy path. Message/state use the same compatibility lookup before their pending-update path. | Existing `spanBySpanId` lookup; message/state already have `pendingSpanUpdatesBySpanId` for updates arriving before start. **Nothing** reconstructs a corrupt/missing index automatically here. |
| DEL-F30 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` (`token`) | Legacy producer-less tokens retain permissive coalescing and guess `agentId` from `activeAgentId`; only stamped IDs split producers. | Existing incoming `frame.agent_id`, message-ID and turn-ID routing paths. **Nothing** identifies a producer when the old frame omits it. |
| DEL-F31 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` (`done`, no-turn fallback) | Pre-#823 message-ID/last-message heuristic is retained when `turn_id` is missing or has no matching bubble; comments also identify a current no-stream fallback sharing it. | Existing turn-ID bubble lookup. **Nothing** supplies absent turn identity; no new handler for the nonlegacy no-stream use is designed here. |
| DEL-F32 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/replay-and-status-frames.ts::handleTurnCanceledReplayEntry` | Explicit legacy/undecorated cancellation entry with no `turn_id` logs and drops without correlation. | Existing `findAssistantMessageIdByTurnId` path for identified turns; **nothing** for the unidentified old entry. |
| DEL-F33 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/session.ts::bakeToolCallsByOwner`; callers in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/outbound-lifecycle.ts::clearStreamingState` | Legacy/unmapped owner uses fallback last message. Source also names live ring-buffer eviction as a reason ownership may be absent. | Existing `toolCallOwnerMessageId` mapping; **nothing** for a genuinely absent/evicted owner. No replacement ownership policy is chosen. |
| DEL-F34 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/omnipus-runtime.ts::buildContentParts` | Live-call filter accepts undefined owner as the pre-tracking legacy path, instead of requiring this message to own it. | Existing `toolCallOwnerMessageId[id] === msg.id`; **nothing** for old unmapped calls. |
| DEL-F35 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/omnipus-runtime.ts::pushHistoryParts`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/messageParts.ts::splitMessageParts` | Explicit legacy no-offset rendering appends unknown-position calls after text (`Infinity` / `unpositioned`). Comments also cover current reconnects with a missing start snapshot. | Existing recorded `textAtToolCallStart` / `PositionedToolCall.textOffset` interleaving; **nothing** for genuinely unknown position. |
| DEL-F36 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/truncation.ts::normalizeTruncationReason`; consumers `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/api/sessions.ts::rawToMessage`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/replay-and-status-frames.ts::handleReplayMessageFrame`, `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` (`done`) | Explicit legacy default `reason ?? 'cancelled'` when truncated. | Existing explicit `truncation_reason`; **nothing** establishing why an old truncated entry ended. |
| DEL-F37 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/frames.ts::createFrameSlice` (`error`); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/llm-error.ts::sanitizeLegacyErrorMessage` | Legacy/no-typed-payload display falls back to sanitized `frame.message`. This shared path also serves current synthesized control/kickoff errors. | Existing typed `payload.llm_error` + `getLLMErrorDisplay` for model errors; **nothing** replacing all current untyped control errors. Remove the old-model-error compatibility, not unrelated security/auth mechanisms by implication. |
| DEL-F38 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` (`replay_error`); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/llm-error.ts::readLLMErrorFromReplayFrame` | Legacy replay shape yields no typed error and displays `frame.message`. | Existing typed replay-error display; **nothing** translating an old untyped payload. |
| DEL-F39 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/types.ts::SessionChatState.goalStatus`, `ChatStore.goalStatus`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/session.ts::emptySessionState`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/store.ts::useChatStore`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/cursor.ts::applySnapshotHistoryWipe`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` (`goal_status`) | Live single latest-goal scalar maintained/initialized/preserved beside the explicitly canonical per-goal map for back-compat. | Existing `goalPills` map and `mergeGoalPillFrame`; **nothing** is defined here as a replacement single “latest” selection rule. |
| DEL-F40 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/ChatScreen.tsx::InlineThinkingIndicator`, `FallbackToolUI`, `VirtualAssistantMessageRow`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/goalSetupState.ts::isGoalRecordEmpty` | These are actual scalar `goalStatus` readers, not just fixture comments. Predicate then looks up merged criteria in `goalPills`. | Existing per-goal `goalPills` data; **nothing** deciding which goal these single-context indicators should select. No new goal-selection product choice is made. |
| DEL-F41 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` (`goal_status`); `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/lib/goalSetupState.ts::goalPillKey` | Missing/empty legacy `goal_id` goes into `_default`; a keyed frame additionally deletes that compatibility entry. | Existing explicit `goal_id` keys; **nothing** supplies an ID for the old unkeyed frame. |
| DEL-F42 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/chat/GoalPillTray.tsx::GoalPill`, `GoalPillTray`, `describePillState` | Live guards skip the retired goal state `queued`; type narrowing also excludes it. This is the deleted goal-confirm state, **not** an agent helper waiting for an execution slot. | **Nothing** for retired queued goals; the eight-plus-current live goal-state branches already exist. Generated enum retirement is backend-owned. |
| DEL-F43 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` (`judge_verdict`) | Legacy task/goal verdict with no session ID remains global/panel-only. The same optional-field branch also intentionally permits current plan-scope global verdicts. | Existing session-addressed `buildJudgeVerdictInsertion` for task/goal; **nothing** identifies the session for an old task/goal verdict. Current plan-scope global behavior is not a legacy path to remove. |

## BDD Scenarios

BDD means behavior-driven acceptance testing. Oracles below come from S-ADR, the founder answers and this spec, not a recorded output of the old implementation. Expected counters, identity markers and failed-delivery observations must be asserted through real store/runner/UI seams. Each scenario has one action; outlines expand into individual cases. Proposed test IDs appear in the TDD plan, not claims of tests already written or run.

### Feature: Main identity and retained history

#### Scenario Outline: BDD-01.1 — Repeated pair lookup has one main
**Traces to:** User Story 1, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** eligible native agent Mia belongs to workspaces W1 and W2, both with heartbeat off
- **When** main lookup for `<pair>` is requested concurrently
- **Then** the returned ID is exactly `<id>` with the correct immutable owner/workspace and one stored identity for that pair
- **And** the other pair and any extra chat are unchanged
**Examples:**

| pair | id |
|---|---|
| W1, mia | main-session-W1-mia |
| W2, mia | main-session-W2-mia |

**Oracle:** FR-002; S-ADR::D1/D1.1. Count actual persisted identities, not lookup calls alone; parameterize with existing valid maximum-bound IDs too.

#### Scenario: BDD-01.2 — Heartbeat-off retains protection and identity
**Traces to:** User Story 1, Acceptance Scenario 2.
**Category:** Happy Path
- **Given** Mia's main is pinned/protected and heartbeat is enabled
- **When** heartbeat is disabled
- **Then** the same main remains protected and navigable, with no replacement chat or heartbeat address mapping
**Oracle:** FR-002; S-ADR::D1. The existing protected-delete behavior remains effective; a protection flag with an unguarded delete path is not a pass.

#### Scenario: BDD-01.3 — Team removal hides without stealing a run's outcome
**Traces to:** User Story 1, Acceptance Scenario 3.
**Category:** Alternate Path
- **Given** Mia's retained main has an authorized running child and a captured completion recipient
- **When** Mia is removed from that workspace team
- **Then** main disappears from UI, connector and task addressing and new work through removed membership is refused
- **And** the authorized child may settle and retain its task result without waking the hidden main
**Oracle:** FR-003; Q5=A. Re-add uses a separate test case beginning with this retained hidden pair and asserts the same ID is revealed, not a replacement.

#### Scenario Outline: BDD-01.4 — Invalid main identity cannot become another chat
**Traces to:** User Story 1, Acceptance Scenario 4.
**Category:** Error Path
- **Given** the requested pair has `<invalid_state>`
- **When** main lookup or attachment is attempted
- **Then** a visible refusal/error is returned with no replacement identity, wrong-owner attach or other-workspace dispatch
**Examples:**

| invalid_state |
|---|
| computed ID exists with the wrong persisted owner/workspace |
| metadata is unreadable or corrupt |
| authenticated caller cannot access that workspace |
| worker/other system agent, or Admin outside his default workspace, is requested as a main |

**Oracle:** FR-002/035; S-ADR::D1/D1.1. Include a valid pair control so a broken lookup cannot pass by rejecting every case.

#### Scenario: BDD-02.1 — Window movement does not rewrite retained history
**Traces to:** User Story 2, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** retained archive messages A/B/C, a complete tool call/result group and a saved working start mark
- **When** a new admitted message causes window sliding
- **Then** prior archive byte prefixes are unchanged, retained A remains recallable, and chat/model views come from one content archive
- **And** model-window reads start at the mark rather than reading the archive prefix
**Oracle:** FR-004/005/006; S-ADR::D2. Instrument actual file reads and compare saved byte prefixes; window-sized returned data alone cannot prove bounded reading.

#### Scenario: BDD-02.2 — Midnight and delayed sources retain FIFO and tool validity
**Traces to:** User Story 2, Acceptance Scenario 2.
**Category:** Edge Case
- **Given** a tool call at 23:59 UTC awaits its result after midnight, and accepted instructions M1/M2 have timestamps from different prior days
- **When** the next safe step consumes the ready batch
- **Then** model input has the complete matching tool group and separate M1/M2 in accepted order
- **And** day/byte references resolve without back-inserting older timestamps, creating another execution or copying content into a late-event archive
**Oracle:** FR-005/009; S-ADR::D2/D3. Use non-ASCII/newline content and a result ID reused in another turn to challenge structural identity.

#### Scenario Outline: BDD-02.3 — Expired marks repair to retained complete content
**Traces to:** User Story 2, Acceptance Scenario 3.
**Category:** Edge Case
- **Given** the old marked day is expired and `<remaining>` content remains
- **When** the working window is read after the retention sweep
- **Then** the same session has `<window>` and the agent is told older history expired
**Examples:**

| remaining | window |
|---|---|
| a retained complete group preceded by a fragment | first retained complete group onward, no orphan call/result |
| no retained content | empty window, same protected eligible main identity |

**Oracle:** FR-007; Q2=A. A disabled-retention fixture leaves old content/mark unchanged; normal file-age equality and just-older fixtures preserve existing sweep semantics.

#### Scenario Outline: BDD-02.4 — Save failure cannot manufacture acceptance
**Traces to:** User Story 2, Acceptance Scenario 4.
**Category:** Error Path
- **Given** `<save_part>` fails during a message's acceptance
- **When** that message is submitted
- **Then** no accepted/saved receipt, live echo presented as saved, or model consumption claims success
- **And** the authenticated sender gets the current save/admission failure surface
**Examples:**

| save_part |
|---|
| persisted owner/identity |
| authenticated provenance |
| content append |
| acceptance/admission publication |

**Oracle:** FR-008; S-ADR::D3 and **Steering commands: no person question**. A real saved entry and a consumed message are counted separately; failure injection is at the write/commit seam, not a mock successful store.

### Feature: Intake and report delivery

#### Scenario: BDD-03.1 — Safe-boundary batch contains every separate accepted input
**Traces to:** User Story 3, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** one tool/model step is blocked and three authenticated inputs from human/connector/authorized parent have been accepted in that order
- **When** the step reaches its next safe boundary
- **Then** the next model input contains all three separate messages in that order and chat shows their distinct identities
- **And** no second runner or waiting mechanism executes any item
**Oracle:** FR-009/008; S-LEDGER::October 6 Q20/queue clarification. Record the full input list and execution-owner count; concatenated text is not the oracle.

#### Scenario: BDD-03.2 — Input during settlement starts the immediate next turn
**Traces to:** User Story 3, Acceptance Scenario 2.
**Category:** Edge Case
- **Given** a natural reply is in final settlement and the same session has newly accepted ready input
- **When** settlement completes
- **Then** the existing runner starts one next turn with all ready input without a timer or previous-reply-still-finishing refusal
**Oracle:** FR-009/023; S-ADR::D3. Use barriers and dispatch counts, not an elapsed-time assertion or a widened timeout.

#### Scenario Outline: BDD-03.3 — Ordinary admission checks every applicable bound
**Traces to:** User Story 3, Acceptance Scenario 3.
**Category:** Error Path
- **Given** an otherwise admissible batch reaches `<bound>`
- **When** the candidate ordinary message arrives
- **Then** admission is `<outcome>` with an explicit bound-specific refusal if rejected, and no already accepted text is shortened
**Examples:**

| bound | outcome |
|---|---|
| text 65,536 UTF-8 bytes, enough other headroom | accepted |
| text 65,537 UTF-8 bytes | refused |
| 59 prior admitted messages by the same sender+target inside the minute | candidate 60 accepted |
| 60 prior admitted messages by that sender+target inside the minute | candidate 61 refused |
| a different trusted sender makes its first message to that target | accepted if other bounds fit |
| 199 waiting ordinary items | candidate 200 accepted if other bounds fit |
| 200 waiting ordinary items | candidate 201 refused |
| joint waiting text 1,048,576 bytes and model fit | accepted |
| joint waiting text 1,048,577 bytes | refused |
| joint request estimate exactly current B | accepted if other bounds fit |
| joint request estimate B+1 even below aggregate bytes | refused |

**Oracle:** FR-010; Q1=A. Per-message, sender, count, bytes and model limits are independent controlled fixtures, not all exercised by one oversized text. Trusted principal/connector identity, not displayed name, keys rate.

#### Scenario: BDD-03.4 — Config save becomes real live admission at reload
**Traces to:** User Story 3, Acceptance Scenario 4.
**Category:** Happy Path
- **Given** common intake is using configured ordinary limits and a second fixture saves different valid steer_body/steer_rate/aggregate values
- **When** the running configuration reloads
- **Then** real post-reload ordinary intake changes its acceptance boundary across web, channel, parent and peer adapters
- **And** fresh boot reads the same values, while report caps/delivery remain unchanged
**Oracle:** FR-011/013; #1216 and Q1=A. Include pre-reload, post-reload and fresh-boot probes; direct resolver outputs or stored JSON alone are insufficient.

#### Scenario Outline: BDD-04.1 — Every accepted helper report can wake
**Traces to:** User Story 4, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** an idle non-stopped authorized parent with available existing wake delivery and a helper's valid `<kind>` report below preserved admission limits
- **When** the report is accepted
- **Then** it is saved to the common report inbox and participates in one parent wake/consumption with its original message identity
**Examples:**

| kind |
|---|
| progress |
| checkpoint |
| artifact |
| question |
| blocker |
| handback/final |
| engine lifecycle report |

**Oracle:** FR-012; S-ADR::D3. Include all existing accepted report variants in QA's expansion. Report wake eligibility must not change preserved rate/count exemptions.

#### Scenario: BDD-04.2 — A stopped parent holds a report without reviving
**Traces to:** User Story 4, Acceptance Scenario 2.
**Category:** Alternate Path
- **Given** a parent is stopped in its current generation and its live helper has a valid accepted report
- **When** the report is delivered
- **Then** it remains in the existing inbox and the parent has zero report-triggered dispatches
**Oracle:** FR-012/024; S-ADR::D6. A separate continuation fixture starts from this held state and proves one consumption; duplicate replay must not add another copy.

#### Scenario Outline: BDD-04.3 — Refused arrival is visible to child and parent
**Traces to:** User Story 4, Acceptance Scenario 3.
**Category:** Error Path
- **Given** a child has reached a preserved report `<limit>`
- **When** another report is attempted
- **Then** the child receives typed refusal and applicable retry/backoff information and the parent's existing status/inbox/activity surfaces record rejected/not delivered, not accepted content
- **And** no automatic retry or second report store is created
**Examples:**

| limit |
|---|
| report rate cap for a non-exempt kind |
| body envelope cap |
| applicable unacked/question-blocker ceiling |

**Oracle:** FR-013; #1211 and S-ADR::D3.1. Reproduce the reported over-rate case before GREEN; an empty drain/has_more:false cannot certify that no refusal occurred.

#### Scenario: BDD-04.4 — Duplicates and full-result limits do not create another protocol
**Traces to:** User Story 4, Acceptance Scenario 4.
**Category:** Edge Case
- **Given** one accepted report identity is retried and another full result exceeds existing delivery limits
- **When** that delivery batch is processed
- **Then** the accepted report is stored/consumed once and the over-limit result has an honest failure/limit observation
- **And** no summary, pagination, deferred-result or retry queue makes the test appear universally successful
**Oracle:** FR-012/013/021; S-ADR::D3/D5 and Q-R2-7. Payload identity and current cap exemptions are preserved; this does not repair #1213 or #1215.

### Feature: Delegation and tasks

#### Scenario Outline: BDD-05.1 — Native omitted and explicit self-target share authorization
**Traces to:** User Story 5, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** eligible native `<role>` has delegate permitted and no self-edge
- **When** it delegates with `<target_form>`
- **Then** one ordinary helper of that caller starts under the same policy/authorization and ordinary context as explicit self-target
**Examples:**

| role | target_form |
|---|---|
| MAIN in its main chat | omitted target |
| MAIN in an extra chat | explicit self |
| native WORKER | omitted target |
| native WORKER | explicit self |

**Oracle:** FR-014/015; S-ADR::D4. Compare launcher requests/context with the ordinary helper path; the MAIN role's delivered prompt prioritizes delegation while role tools remain available. No guarantee that a model will always obey that priority is claimed.

#### Scenario Outline: BDD-05.2 — Nested self-helpers use ordinary limits
**Traces to:** User Story 5, Acceptance Scenario 2.
**Category:** Alternate Path
- **Given** a self-helper is at `<depth>` under default depth 3 and `<admission>`
- **When** it requests one more ordinary helper
- **Then** the result is `<result>` without a self-only one-hop rule or a new per-main quota
**Examples:**

| depth | admission | result |
|---|---|---|
| below 3 | ordinary memory/operator admission available | ordinary helper accepted |
| at 3 | ordinary admission otherwise available | visible depth refusal |
| below 3 | ordinary memory admission cannot admit | existing admission disposition, no cap bypass |

**Oracle:** FR-015; S-ADR::D4 and Q-R2-2. Helper memory/recap and the configured operator limits stay ordinary; no task/delegate default timeout conflation.

#### Scenario Outline: BDD-05.3 — Policy and excluded runtimes remain gates
**Traces to:** User Story 5, Acceptance Scenario 3.
**Category:** Error Path
- **Given** `<caller_state>`
- **When** Omnipus self-delegation is attempted
- **Then** no helper is launched and a visible policy/eligibility refusal is returned
**Examples:**

| caller_state |
|---|
| eligible native caller with explicit delegate Deny |
| external CLI worker |
| standalone Admin outside the expanded native role scope |
| system agent with fixed tool surface |

**Oracle:** FR-014/016; S-ADR::D4/D10. Eligibility is not an override of the global or per-agent Deny.

#### Scenario Outline: BDD-05.4 — Other-agent trust remains separate from self
**Traces to:** User Story 5, Acceptance Scenario 4.
**Category:** Error Path
- **Given** another-agent target has `<edge_state>` in the actual workspace
- **When** ordinary delegation is requested
- **Then** it is `<outcome>` under the existing edge/mode/depth, target policy and requested-skill checks
- **And** accepted helpers have exactly ordinary context/memory, not a new snapshot mechanism
**Examples:**

| edge_state | outcome |
|---|---|
| no authorizing edge | refused |
| authorized edge, allowed mode/depth/receiver | accepted |
| wrong mode or exceeded edge depth | refused |
| workspace graph unreadable | refused |

**Oracle:** FR-015/039; S-ADR::D4/P1–P17. #1212 is a coordinated external fix, not an expectation to silently repair its snapshot here.

#### Scenario Outline: BDD-05.5 — External live steering resumes the real conversation
**Traces to:** User Story 5, Acceptance Scenario 5.
**Category:** Alternate Path
- **Given** `<driver>` has an actual native conversation containing marker N, a live run, and known runtime/workspace/model/caps
- **When** instruction S is sent through its opened worker chat
- **Then** the current driver interrupts and resumes that native conversation, receives S, preserves N and the configured runtime/workspace/model/caps, and remains the same Omnipus chat
- **And** the UI/docs disclose that CLI background subprocesses may be killed; no Omnipus self-helper is created
**Examples:**

| driver |
|---|
| Claude Code |
| Codex |
| OpenCode |

**Oracle:** FR-043/016/034; S-ADR::D6/DEL-20 and 10:35. Real driver/model acceptance is required; help output and a mock successful Input are not this proof.

#### Scenario Outline: BDD-05.6 — Unavailable native resume is not successful steering
**Traces to:** User Story 5, Acceptance Scenario 5.
**Category:** Error Path
- **Given** an external worker has `<problem>`
- **When** live instruction/resume is attempted
- **Then** the action fails visibly with no successful no-op, fabricated native ID or silently fresh conversation
**Examples:**

| problem |
|---|
| native conversation ID absent/invalid |
| CLI unavailable or authentication invalid |
| native resume rejects or crashes |
| selected execution was superseded before delivery |

**Oracle:** FR-043/023; S-ADR::D6/DEL-20. Preserve actual process/control failure and the selected-execution fence.

#### Scenario Outline: BDD-06.1 — Automatic modes preserve chat and task-run ownership
**Traces to:** User Story 6, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** `<assignee>` task has `<timing>` and `<isolation>`
- **When** the task's next actual execution starts
- **Then** it uses `<mode>` and `<conversation>` with its own task/run/goal identity and normal task limits
**Examples:**

| assignee | timing | isolation | mode | conversation |
|---|---|---|---|---|
| MAIN | manual/one-time | not applicable or unchecked | MAIN | fresh real child of assignee main |
| MAIN | recurring scheduled | unchecked | MAIN | fresh real child for each run |
| native/external worker | one-time/manual | unchecked | ISOLATED | fresh independent chat |
| native/external worker | recurring scheduled | unchecked | CONTINUE | same independent chat after first run, new run ID each occurrence |
| MAIN or worker | one-time/recurring scheduled | checked | ISOLATED | fresh independent chat each execution |

**Oracle:** FR-017/018/021; S-ADR::D5. Heartbeat is a separate fixture using main itself. Preserved Calendar once/RRULE timing and current overlap/skipped behavior use the existing engine.

#### Scenario: BDD-06.2 — A scheduled MAIN child may run under stopped main
**Traces to:** User Story 6, Acceptance Scenario 2.
**Category:** Alternate Path
- **Given** a future authorized scheduled MAIN task belongs to a stopped main, with a different starting-extra-chat Auto setting
- **When** its occurrence fires
- **Then** task-origin admission can launch a fresh child under the main without dispatching the main model, ordered against the parent Stop fence
- **And** the child uses main-parent approval within target restrictions and task-owned limits, not the extra chat's setting or delegate defaults
**Oracle:** FR-018/034; Q-R2-1=A/Q-R2-3=A. Race both orderings with Stop barriers; general stopped-parent helper launch guards remain, and unattended human-required Ask still refuses immediately.

#### Scenario Outline: BDD-06.3 — Only actual terminal runs notify captured recipients
**Traces to:** User Story 6, Acceptance Scenario 3.
**Category:** Happy Path
- **Given** creator A differs from MAIN starter B and MAIN assignee Mia, recipients B-main/Mia-main were captured at start, and the run has `<outcome>`
- **When** the authoritative run outcome is published
- **Then** `<notice>` goes to the captured deduplicated recipients, never creator A solely because it created the task
- **And** an eligible idle recipient wakes once, live recipient consumes at a safe boundary, and stopped recipient holds without revival
**Examples:**

| outcome | notice |
|---|---|
| done | brief engine header plus full stored result |
| failed | terminal reason plus full stored result |
| stopped | winning Stop reason plus full stored result, no later success contradiction |
| skipped fire | no actual-run completion notice |
| retry attempt still within one run | no per-retry final notice |

**Oracle:** FR-019/020; S-ADR::D5/10:25. Starter=assignee dedupes to one recipient; simultaneous ready notices are batched, not concatenated into a new result format.

#### Scenario Outline: BDD-06.4 — Missing recipient or delivery overflow leaves truthful task result
**Traces to:** User Story 6, Acceptance Scenario 4.
**Category:** Error Path
- **Given** an actual completed run has `<problem>`
- **When** its captured-recipient completion is delivered
- **Then** the authoritative task/run result remains inspectable and no substitute recipient, synthetic parent edge, summary or new overflow queue is used
- **And** rejected delivery/limit is distinguishable from successful consumption
**Examples:**

| problem |
|---|
| captured recipient session deleted |
| captured main hidden by membership removal |
| full result exceeds current report envelope/context limits |
| independent ISOLATED/CONTINUE run is displayed by an unrelated monitoring chat |

**Oracle:** FR-003/019/021; S-ADR::D5 and Q-R2-7. A displayed independent run is not stopped by that viewer's chat-tree Stop.

### Feature: Controls and communication

#### Scenario Outline: BDD-07.1 — Stop scope follows the selected activation
**Traces to:** User Story 7, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** a native main has a live helper, a current MAIN task child, an independent monitored task and a future recurring schedule
- **When** `<activation>` is performed in that main chat
- **Then** `<scope>` is stopped under selected-execution identity and `<preserved>` is untouched
**Examples:**

| activation | scope | preserved |
|---|---|---|
| first Stop/Esc/stop command | main's current turn only | helpers, MAIN task child, independent task, future recurrence, native background shells |
| second Stop activation within 3 seconds | main and real downward helper/MAIN-child tree | independent task and future recurrence |
| cancel command | same real downward tree immediately | parent/sibling conversations, independent task, future recurrence |
| Stop after escalation window has closed | current turn only | helpers and other conversations |

**Oracle:** FR-022/033; S-ADR::D6. UI has no separate Stop-all button; label uses actual live helpers, not historical child_count. Assert native shell process liveness/termination via existing tests, not a control-result boolean alone.

#### Scenario: BDD-07.2 — Redirect/steer race has one honest disposition
**Traces to:** User Story 7, Acceptance Scenario 2.
**Category:** Error Path
- **Given** redirect and steer target the same selected execution and one is paused at its acceptance/publication fence
- **When** the competing control is admitted
- **Then** each caller receives its authoritative queued/applied/superseded/refused outcome, and text reported refused is never consumed
- **And** root/helper redirect continues the same chat with the new instruction, without internal stale-generation explanation or replacement-turn mutation by a late callback
**Oracle:** FR-023/009; #1214 and S-ADR::D6. Enumerate both ordering outcomes using barriers and receipt/consumed-message identity; control precedence stays existing, not arrival guessed from sleep timing.

#### Scenario Outline: BDD-07.3 — Held human input is released or discarded deliberately
**Traces to:** User Story 7, Acceptance Scenario 3.
**Category:** Alternate Path
- **Given** Stop won with human H1/H2 held visibly and older parent control P superseded
- **When** `<action>` occurs
- **Then** `<effect>` and P never returns
**Examples:**

| action | effect |
|---|---|
| a new ordinary human message N | N can continue the chat without silently replaying all H1/H2 |
| explicit release of held input | authorized released input joins the same intake with visible disposition |
| explicit discard of held input | discarded input does not reach the model and the display reports discard |

**Oracle:** FR-024/008; S-ADR::D6. Clear also retains pending input. This does not add restart reconstruction for held input.

#### Scenario: BDD-07.4 — Restart-cut execution is Interrupted, not replayed
**Traces to:** User Story 7, Acceptance Scenario 4.
**Category:** Edge Case
- **Given** the gateway was killed while a conversation had an in-flight execution and old waiting input
- **When** that saved chat is opened after restart
- **Then** it shows Interrupted, not permanently Working or failed because of restart, and old waiting input has zero boot dispatches
- **And** task interrupted-failure/plan recovery remains its existing behavior
**Oracle:** FR-025; S-ADR::D6. Integrate the I1-owned #1217 waiting-for-answer variant and its next-message continuation decision; do not claim that external fix is delivered by this spec. Manual Generate again remains the existing separate action, not boot replay.

#### Scenario: BDD-08.1 — A guest answers with its own identity without handover
**Traces to:** User Story 8, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** Mia owns chat C and Jim is an authorized MAIN teammate
- **When** the person submits an addressed Jim request with explicit material X
- **Then** Jim's main receives only that request/X, not C's whole history, and his answer returns to C labelled Jim live and on replay
- **And** Mia's context includes request and answer; idle non-stopped Mia wakes once for the answer, may remain silent without an empty bubble, and remains immutable owner
**Oracle:** FR-026/027/029; S-ADR::D7. Use a source-history-only secret marker that Jim must not receive; preserve exact responder/message/correlation IDs.

#### Scenario Outline: BDD-08.2 — Peer messaging is not delegated control
**Traces to:** User Story 8, Acceptance Scenario 2.
**Category:** Alternate Path
- **Given** sender/receiver are `<relationship>`
- **When** a direct peer request is sent
- **Then** the request is `<outcome>` without transferring Stop, context-read, grant inheritance or ancestor steering authority
**Examples:**

| relationship | outcome |
|---|---|
| eligible MAIN teammates in same authorized workspace, no delegation edge | accepted under receiver permissions |
| eligible explicit destination pair in another workspace | accepted under receiver permissions, no control transfer |
| target is a worker/other system agent or Admin at a non-default-workspace pair | visibly refused |
| target main is hidden after team removal | visibly refused |

**Oracle:** FR-026/003/034; S-ADR::D7. Text claiming to be a parent/human must not alter trusted classification.

#### Scenario: BDD-08.3 — Mixed-source answers keep their own return address
**Traces to:** User Story 8, Acceptance Scenario 3.
**Category:** Happy Path
- **Given** one main consumed requests from web sender U and two same-platform connector instances I1/I2 with distinct chats/threads/correlations
- **When** the agent produces addressed answers to those requests
- **Then** each answer goes only to its own sender's original instance/chat/thread/session/correlation, with no broadcast or conversation-level last-sender fallback
**Oracle:** FR-028/008; S-ADR::D7. Capture outbound destinations and negative recipients. Correct egress is not a privacy boundary: the main's shared context still contains admitted inputs.

#### Scenario: BDD-08.4 — Unaddressed output visibly refuses
**Traces to:** User Story 8, Acceptance Scenario 4.
**Category:** Error Path
- **Given** a mixed-source response has no usable return correlation/destination
- **When** that output is submitted for delivery
- **Then** delivery visibly refuses and asks the agent to address a sender, with zero outbound sends to any guessed/default destination
**Oracle:** FR-028; Q3=B. A warning followed by fallback delivery is a failure.

#### Scenario Outline: BDD-08.5 — One task family uses own workspace unless explicitly targeted
**Traces to:** User Story 8, Acceptance Scenario 5.
**Category:** Happy Path
- **Given** a permitted calling agent's own workspace is W1, W2 is a valid explicit target, and `<operation>` uses `<workspace_choice>`
- **When** the merged canonical task operation is invoked
- **Then** `<effect>` follows the current ownership/assignment/criteria/plan/Operator Deny rules and no duplicate *_in_workspace tool can execute
**Examples:**

| operation | workspace_choice | effect |
|---|---|---|
| create_task | absent | creates in W1, not an inferred other workspace |
| create_task | explicit W2 | task uses W2 with existing cross-workspace validation/store |
| update_task | absent | own-workspace scope; no implicit cross-workspace move |
| update_task | explicit W2 | explicit authorized operation uses current all-or-nothing update validation, no moved/copied plan |
| list_tasks | absent | caller/principal-scoped W1 list, not all-workspaces fallback |
| list_tasks | explicit W2 | authorized W2 scope, preserved principal/role/bounded-result metadata |
| any canonical operation | invalid/inaccessible supplied ID or applicable Operator Deny | visible refusal, zero unintended task writes/disclosures, no own/default fallback for invalid explicit choice |
| any canonical operation | tries cross-workspace plan membership/move | existing same-workspace plan constraint refuses; no new plan behavior |

**Oracle:** FR-046; Q8=MERGE and E-TASK-TARGET. Expand Ava/Admin and other moved callers with explicit allowed and denied fixtures. Catalog/role/default/prompt entries expose only canonical task names; not a second executor or a hidden name adapter.

### Feature: Frontend actions, activity and deliberate data removal

#### Scenario Outline: BDD-09.1 — Navigation is distinct from extra-chat creation
**Traces to:** User Story 9, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** the current workspace has eligible Mia/Jim mains and a retained extra chat
- **When** `<action>` is used
- **Then** `<outcome>` occurs without mutable in-chat handover or a new generic chat-creation bypass
**Examples:**

| action | outcome |
|---|---|
| Mia agent row click | Mia main opens |
| Mia row + New chat | one deliberate extra Mia chat opens |
| switch-agent navigation to Jim | Jim main opens |
| WebSocket connect/reconnect | existing roster refresh occurs; eligibility remains team-only with standalone Admin treatment unchanged |
| sessions command | existing session search opens under its canonical name |

**Oracle:** FR-029/031; S-ADR::D8; S-FE::D3/D5/D12. Unavailable attach is covered by BDD-01.4; never fall back to latest session or a fresh chat.

#### Scenario: BDD-09.2 — Clear waits for the safe point and keeps history
**Traces to:** User Story 9, Acceptance Scenario 2.
**Category:** Happy Path
- **Given** a live step holds old context, saved history A, and waiting input H
- **When** clear is requested
- **Then** the current step is not claimed to have forgotten its input; at the next safe point context/current display clear with a marker under the same session
- **And** H remains pending and A stays searchable/recallable without archive-byte deletion
**Oracle:** FR-030/006; Q-R2-9=A. Reopening history must preserve the clear projection; search/recall must still reach retained prior content.

#### Scenario Outline: BDD-09.3 — Typed worker commands and palette agree
**Traces to:** User Story 9, Acceptance Scenario 3.
**Category:** Error Path
- **Given** a human has an opened worker chat and invokes `<capability>` through `<entry>`
- **When** that command/action is executed
- **Then** the result is `<outcome>`, with no forbidden mutation hidden behind a read-only list or stale alias
**Examples:**

| capability | entry | outcome |
|---|---|---|
| help/status/stop/cancel/stop-redirect/clear/sessions/workspace/tasks/recall/navigation | typed and palette, each case | permitted within current authorization |
| existing read-only model/skill/channel lists | typed and palette, each case | permitted as read-only, no selection/mutation |
| remember/retrospective/goal/loop/model switching/config | typed and palette, each case | forbidden in worker context |
| new/agents/list/show/switch/check/channel/start/old resume | any old command or alias | absent/refused everywhere, not hidden compatibility execution |

**Oracle:** FR-031/038; S-ADR::D8 final 12:35 table. The singular retired channel alias is not a ban on current read-only channel lists.

#### Scenario Outline: BDD-09.4 — Existing worker input does not rerun a completed task
**Traces to:** User Story 9, Acceptance Scenario 4.
**Category:** Alternate Path
- **Given** a legitimate worker/task session has `<state>` and existing task/run history
- **When** the authenticated human sends a message into that opened chat
- **Then** `<effect>` occurs under the same conversation and current authorization
- **And** the worker still cannot be selected for fresh chat or mention
**Examples:**

| state | effect |
|---|---|
| live task run | steers that current run |
| task finished | conversation continues; old task/run outcome stays unchanged and no rerun is dispatched |
| stopped conversation | legitimate human continuation respects Stop fence and held-input policy |

**Oracle:** FR-032/024; Q-R2-10=A. Explicit task Rerun remains a different existing action.

#### Scenario: BDD-10.1 — Existing Activity shows tasks once and distinguishes authority
**Traces to:** User Story 10, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** the agent has native helpers, a scheduled MAIN task child and an independent task monitored by its starter
- **When** Activity is opened
- **Then** the relevant run/helper rows and their actual running/queued/waiting state, available usage and own chat links are visible
- **And** the MAIN task is one row rather than task-plus-helper duplicate; independent monitoring grants no tree Stop and historical child_count is not a live running count
**Oracle:** FR-033/021; S-ADR::D4/D5/D6. Token values must match stamped provider/run usage where supplied; missing usage is not fabricated zero.

#### Scenario: BDD-10.2 — One native approval names the acting helper/run
**Traces to:** User Story 10, Acceptance Scenario 2.
**Category:** Happy Path
- **Given** a native helper/task run has one pending tool approval in the current workspace
- **When** the existing approval modal renders
- **Then** its acting helper/run label and Open link resolve to that execution under the same approval ID, actions and receiver restrictions
- **And** no extra approval queue/card exists and external CLI calls are not described as natively popup-protected
**Oracle:** FR-034/035; S-ADR::D10 and E-APPROVAL. Changing attribution must preserve scope, global/per-chat Auto choices, pending resolution and unattended refusal. The per-agent Auto off-switch is removed by separate #1221; helpers inherit the parent.

#### Scenario Outline: BDD-10.3 — Missing or partial view data stays truthful
**Traces to:** User Story 10, Acceptance Scenario 3.
**Category:** Error Path
- **Given** the existing surface is in `<state>`
- **When** it renders
- **Then** it shows `<truthful_state>` rather than fabricated activity/tokens, successful delivery, replacement main or lost-history recovery
**Examples:**

| state | truthful_state |
|---|---|
| roster/main lookup pending | loading, no auto-created extra |
| protected main has no retained content | empty conversation, same identity |
| roster refresh failed with cached choices | visible stale/error notice and current retry path |
| activity lifecycle/usage not yet known | partial/unknown data, not guessed running/zero tokens |
| report/result delivery rejected | not-delivered observation, stored task result remains accessible |
| deletion cleanup incomplete | partial result, not full success |

**Oracle:** FR-035/033/013/037; UI Screens and States. This does not choose a new status-label/product rule for a missing canonical producer.

#### Scenario: BDD-10.4 — Keyboard and assistive controls reach the same behavior
**Traces to:** User Story 10, Acceptance Scenario 4.
**Category:** Happy Path
- **Given** keyboard/assistive use in a workspace with main, worker, held input, activity and a deletion confirmation
- **When** one authorized control is operated through its keyboard interaction
- **Then** the same action/permission is applied with an accessible name, status/error announcement, visible focus and appropriate focus restoration
- **And** escape/dismissal precedence remains safe; reflow/zoom/touch targets do not hide or overlap that control
**Oracle:** FR-035; S-RULES/design-system D7/D16/D17. Parameterize individual controls and pointer/touch, 200% zoom and 320px width; do not assert a whole journey with one click.

#### Scenario: BDD-11.1 — Repeated idle episodes recap the same main identity
**Traces to:** User Story 11, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** a settled main human chat has auto recap on by default and a last real message/turn activity time
- **When** thirty inactive minutes elapse
- **Then** one idle recap for that episode writes agent memory without an archive summary, new conversation or overlapping live execution
**Oracle:** FR-036/006; S-ADR::D9. Separate fixtures add activity just before the boundary, keep a live settlement past it, or resume the same ID before another later idle episode; fake-clock/timer claims, not wall-clock sleeps.

#### Scenario Outline: BDD-11.2 — Actual deletion uses one owned-data cascade
**Traces to:** User Story 11, Acceptance Scenario 2.
**Category:** Happy Path
- **Given** deletable agent X has owned sessions/memory and a guest answer in Mia's chat, with `<confirmation>`
- **When** X is deleted through `<entry>`
- **Then** the one cascade removes X's owned sessions/memory and preserves the guest answer labelled deleted agent without a runnable X identity
**Examples:**

| entry | confirmation |
|---|---|
| UI | owned-data warning and both explicit confirmations |
| existing authorized agent tool | existing single approval |
| existing authorized API | existing single-approval requirements |

**Oracle:** FR-037; Q6=B and Q-R2-11=A. Removal from Team is not this deletion action; default protection/locked/system/plan guards remain.

#### Scenario Outline: BDD-11.3 — Guarded or partial delete is not reported complete
**Traces to:** User Story 11, Acceptance Scenario 3.
**Category:** Error Path
- **Given** deletion has `<problem>`
- **When** it is attempted
- **Then** the current result reports the guard/refusal/partial cleanup accurately, retaining guest history and not claiming complete owned-data deletion
**Examples:**

| problem |
|---|
| UI first confirmation only or dismissed second confirmation |
| locked/system/active-plan guard applies |
| owned-session cleanup fails |
| owned-memory cleanup fails after another cleanup part succeeded |

**Oracle:** FR-037/035; S-ADR::D9. API/tool do not acquire the UI's second confirmation gate as a side effect.

#### Scenario: BDD-11.4 — Current recap meanings remain and explicit close is gone
**Traces to:** User Story 11, Acceptance Scenario 4.
**Category:** Alternate Path
- **Given** current bootstrap/idle/agent-recorded retrospective inputs and an ordinary helper's existing recap eligibility
- **When** a recap action is requested
- **Then** bootstrap/idle remain, retrospective retains joined, helper memory behavior is unchanged, and lazy/explicit/session-close actions cannot invoke a recap
**Oracle:** FR-036/038; S-ADR::D9/DEL-08. No new compaction-summary or helper-memory workflow.

### Feature: Canonical deletion, contracts and joint reachability

#### Scenario Outline: BDD-12.1 — Every scoped retired path is removed, not aliased
**Traces to:** User Story 12, Acceptance Scenario 1.
**Category:** Error Path
- **Given** `<deletion_id>` names the exact retired path and canonical replacement/nothing in the DELETE inventory
- **When** its old callable/wire/config/reader/alias branch is challenged
- **Then** the retired behavior is absent/refused and only its listed current replacement, or nothing, is reachable
**Examples:** every DEL-01–DEL-23 and DEL-F01–DEL-F43 inventory row is an independent case; group aliases within a row into explicit subcases. Current once/at_ms is a positive control, not a deletion case.
**Oracle:** FR-038/031/036; S-ADR::scoped deletion ruling and Q4=A. Static absence checks require positive recall controls and real callable/reader behavior tests; a comment containing a name is neither preserved behavior nor a second implementation.

#### Scenario Outline: BDD-12.2 — Canonical current output survives shared-branch deletion
**Traces to:** User Story 12, Acceptance Scenario 2.
**Category:** Edge Case
- **Given** a current `<producer_case>` has a valid canonical identity/state and shares handling with a retired branch
- **When** that output is delivered or replayed
- **Then** its current message/tool/lifecycle/goal/plan behavior remains visible and correctly owned without back-compat heuristics, hidden output or guessed latest entity
**Examples:**

| producer_case |
|---|
| ordinary correlated first-send and queued delivery |
| current workspace kickoff acknowledgement |
| current non-stream final answer with no prior streamed bubble |
| tool owner evicted from the UI ring buffer |
| reconnect with actual recorded text/tool offsets |
| start/state/end out of order with valid span IDs |
| typed model/replay error and current control/kickoff error |
| current keyed goal state and current plan-scope global verdict |

**Oracle:** FR-039/006/035; S-ADR::DEL-F preservation boundary/P1–P17. If the current producer lacks a required canonical identity, report the gap before deletion; do not fabricate a field, owner or goal-selection rule.

#### Scenario Outline: BDD-12.3 — Generated boundary validation matches current contracts
**Traces to:** User Story 12, Acceptance Scenario 3.
**Category:** Error Path
- **Given** `<payload>` is submitted through the real changed contract seam
- **When** its generated runtime validator/consumer processes it
- **Then** `<verdict>` holds with no hand-written wire-type bypass
**Examples:**

| payload | verdict |
|---|---|
| valid main/member/message/run/control response | accepted by current generated types/validators |
| missing required current Session.type or wrong main owner/pair | schema or authoritative server refusal; no chat fallback |
| client-created main, retired handover or session-close action | refused/not supported |
| task every/every_ms/recurring.cron_expr | refused/not supported, not preserved |
| current once/at_ms and RRULE task | accepted through the existing timing path |
| malformed current correlated identity/typed error | refusal/visible validation failure, no producer guessing |

**Oracle:** FR-001/038; Contract Changes. Validate a correct control first; a wrong payload rejected for an unrelated field is not proof of the intended constraint.

#### Scenario: BDD-12.4 — Integrated real user and agent can invoke the feature
**Traces to:** User Story 12, Acceptance Scenario 4.
**Category:** Happy Path
- **Given** the exact joint backend/sidebar candidate includes committed generated contracts and matching user-facing documentation
- **When** its real user/agent reachability campaign is executed
- **Then** the required main/extra/worker/group/task/control/approval/history paths are invoked through the actual UI and permitted tools
- **And** RED/GREEN/CHECK, five-reviewer findings dispositions and exact-commit tester plus independent validator evidence are recorded separately from merely written plans
**Oracle:** FR-041/042; Reachability and S-FE::D12. S-FE is still Proposed; successful static frontend preparation is not joint acceptance or landing approval.

### Feature: Shared main attention and preserved saved-chat cutover

#### Scenario Outline: BDD-13.1 — Only the four main-owned sources light attention
**Traces to:** User Story 13, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** a valid main response with no previously outstanding source, including the Admin-default-main variant
- **When** `<source>` becomes present
- **Then** computed read-only needs_attention is `<value>` for that pair without changing the session's lifecycle state or reading another chat into this main
**Examples:**

| source | value |
|---|---|
| pending structured question in the main | true |
| pending tool approval in the main | true |
| unseen main goal ending met | true (finished) |
| unseen main goal ending rounds_exhausted | true (failed) |
| unseen main goal ending other | true (failed) |
| main goal ending stopped_by_user only | false |
| pending ask/approval or goal outcome owned by an extra/child/worker chat | false |
| generic unread activity/task notice/plan verdict only | false |

**Oracle:** FR-047; C-ATTENTION and Q7=A's amended mapping. Non-main responses omit the field, not a fabricated main false/identity; actual pending IDs/session provenance are checked.

#### Scenario: BDD-13.2 — One explicit main open clears shared seen goals, not asks or approvals
**Traces to:** User Story 13, Acceptance Scenario 2.
**Category:** Alternate Path
- **Given** users A and B can view the same main, it has unseen mapped goal outcome G1 plus pending ask Q and approval P, and G2 races after the captured open bound
- **When** A explicitly foreground-opens that main successfully with attention acknowledgement
- **Then** G1 is seen for both A and B, Q/P remain pending and G2 stays unseen
- **And** resolving Q or P clears that source through the existing state update, not through the open acknowledgement
**Oracle:** FR-047; Q7=A. Use append/open barriers and a bounded saved identity mark; metadata restart/replay must preserve the shared seen bound, not grow a seen-ID set.

#### Scenario Outline: BDD-13.3 — Reconnect and source failure cannot manufacture a read
**Traces to:** User Story 13, Acceptance Scenario 3.
**Category:** Error Path
- **Given** main attention is known/unseen and `<refresh_case>` occurs
- **When** its existing list/snapshot/live state is refreshed
- **Then** `<effect>` holds using authoritative main-owned pending/outcome/seen sources
**Examples:**

| refresh_case | effect |
|---|---|
| background attach/prefetch/reconnect without explicit acknowledgement | seen mark unchanged; new unseen outcomes remain attention |
| replay of an acknowledged goal outcome ID | no relight |
| new outcome while main chat is not open | existing frame/query path updates the sidebar main projection; no new event family |
| source or metadata read fails | visible error/unknown or stale state, never fabricated false/cleared attention |
| unauthorized/failed foreground attach | no seen write |
| gateway restart | shared saved mark and current pending snapshots restore; no per-user notification fallback |

**Oracle:** FR-047/001/035; C-ATTENTION. Valid baseline source control is required; a main flag with no live unopened-main writer/query path is not delivered.

#### Scenario: BDD-13.4 — A needed user answer uses the existing structured tool
**Traces to:** User Story 13, Acceptance Scenario 4.
**Category:** Happy Path
- **Given** an eligible human-facing agent needs a user answer and the current structured question tool is permitted
- **When** the agent requests that answer
- **Then** the current question card/tool path is used and its pending/resolved main-owned source updates needs_attention through existing frames
- **And** the runtime-delivered prompt contains the prompt-owner-authored structured-question requirement, with no new raw-text question detector/event system
**Oracle:** FR-047; founder 17:15; E-PROMPT/E-ATTENTION. A denied/unavailable tool is reported truthfully, not treated as a successful question or a new permission bypass. Child reports remain reports; the parent asking the user uses the structured tool.

#### Scenario: BDD-14.1 — Saved chats and heartbeat history survive the cutover
**Traces to:** User Story 14, Acceptance Scenario 1.
**Category:** Happy Path
- **Given** a frozen supported pre-cutover fixture has ordinary, extra, worker/task and heartbeat saved chats with known full history, effective agent/workspace, window/recall references and original IDs
- **When** install/upgrade conversion completes
- **Then** those saved contents/bindings remain reachable and accept a real follow-up under the appropriate same ordinary ID or computed heartbeat-to-main identity
- **And** heartbeat main preserves history, eligible protection and required existing references; no empty replacement or guessed owner/workspace appears
**Oracle:** FR-044/002/004/039; 17:15 migration exception. Expected fixture content/binding are frozen from the stated preservation contract, not read off the candidate; a fresh-install-only test cannot certify migration.

#### Scenario: BDD-14.2 — Repeat/crash at conversion boundaries does not duplicate or truncate
**Traces to:** User Story 14, Acceptance Scenario 2.
**Category:** Edge Case
- **Given** a conversion is interrupted before/after staged archive, identity publication or completion marking for a known saved chat
- **When** the same cutover is resumed/repeated
- **Then** one valid converted chat is reachable with the same complete content/binding and no duplicate, missing bytes, stale index or overwritten conflicting pair
**Oracle:** FR-044; E-MIGRATE and 17:15. Force each real write/publication boundary, including failure after content publish before marking completion; source stays available until verified completion.

#### Scenario Outline: BDD-14.3 — Failed conversion stays visible and keeps its source
**Traces to:** User Story 14, Acceptance Scenario 3.
**Category:** Error Path
- **Given** a saved chat has `<failure>`
- **When** its cutover migration is attempted
- **Then** an explicit migration failure identifies the affected chat, source history remains and no completed empty/wrong-owner conversion or default fallback is claimed
**Examples:**

| failure |
|---|
| unreadable/corrupt source metadata or content |
| missing/conflicting effective agent/workspace binding |
| stage/write/atomic publication failure |
| computed main target already belongs to a different pair |
| completion/source-finalization failure after a staged successful write |

**Oracle:** FR-044/008; 17:15. A log-only skip followed by normal full-success status fails; repeating after recoverable I/O repair must use the idempotent conversion path.

#### Scenario: BDD-14.4 — Steady state has one store, not another migration/runtime path
**Traces to:** User Story 14, Acceptance Scenario 4.
**Category:** Alternate Path
- **Given** a fresh install or a successfully converted fixture with no unfinished import
- **When** normal chat input is processed
- **Then** only the current single archive/store/runtime reader is used and no legacy compatibility writer/alias or boot input replay executes
**Oracle:** FR-044/038/040; E-MIGRATE bounded exception. #1221's Auto off-switch removal and unrelated old task triggers/tool aliases are not migration behaviors added to this scope.

## Test-Driven Development Plan

### Test hierarchy and execution discipline

| Level | Real system under test | Allowed doubles / oracle |
|---|---|---|
| Unit | Existing identity/window/admission/classification/mode/reducer logic | Fixed fixture inputs, existing estimator arithmetic and controllable clock; do not substitute a mock successful store for the operation being proved |
| Integration | Real session/inbox/lifecycle/run stores, runner/intake, controls, generated validators, task dispatch and common deletion | Model/connector/process boundary doubles and explicit write-failure/barrier hooks; compare actual saved/consumed/outbound identities |
| E2E | Integrated candidate browser, actual permitted agent tools, task/calendar/approval/search/worker chat links | Isolated homes/accounts and controlled input; actual supported CLI lane needed for its real native-resume claim |

QA authors RED before production edits and derives expected values from these oracles. Backend/frontend leads implement GREEN; a different qa-lead audits CHECK/test integrity. Existing behavioral safety assertions cannot be weakened, skipped or deleted because a path is renamed. Compatibility-only fixtures are removed only as the scoped DELETE requirements require, and replacement behavior/safety tests accompany that removal.

CI is the authority for heavy Go/build/race/cross-platform/frontend gates. No full local Go or frontend suite is requested by this document; the one-at-a-time narrow local exception remains under S-RULES. Concurrency tests use the actual race gate and forced interleavings; compilation on the supported platforms and generated-runtime validation are separate checks. Every claimed future run requires saved log, direct exit code, exact SHA and named executed tests. RED-before-GREEN is shown from the tests-only pre-change commit or the permitted narrow run, not inferred from writing a test.

### Proposed test implementation order

Names are **proposed** test families for QA, not claims that files/tests exist. Each family expands all referenced scenario outline rows. Unit families precede integration, then E2E; this is test dependency order, not a new founder-approved implementation/landing sequence.

| Order / ID | Proposed test family | Level | BDD scenarios | What must distinguish red from green |
|---|---|---|---|---|
| 1 / T01 | MainPairIdentityAndProtection | Unit | BDD-01.1, BDD-01.2, BDD-01.4 | Pair match/eligibility, concurrency/collision, computed long ID and protection rather than merely nonempty ID |
| 2 / T02 | SingleArchiveBoundedWindowAndAppendEffects | Unit | BDD-02.1, BDD-02.2 | One archive, unchanged prefix, real read offset and complete call/result groups |
| 3 / T03 | RetentionCursorRepairAndProtectedIdentity | Unit | BDD-02.3 | File age/disabled state, oldest complete group, agent expiry notice and same-ID empty window |
| 4 / T04 | OrdinaryIntakeIndependentBounds | Unit | BDD-03.3 | Every body/rate/count/aggregate/model bound; bytes versus characters and trusted sender identity |
| 5 / T05 | LiveConfigAdmissionSnapshot | Unit | BDD-03.4 | Settings actually wired to live check; integration complement below verifies gateway boot/reload |
| 6 / T06 | AcceptedReportClassAndPreservedCaps | Unit | BDD-04.1, BDD-04.3, BDD-04.4 | Expanded wake eligibility without widening preserved cap exemptions; dedupe/rejection data |
| 7 / T07 | NativeSelfAuthorizationAndOrdinaryLimits | Unit | BDD-05.1, BDD-05.2, BDD-05.3, BDD-05.4 | Omitted=explicit self, no self-edge, Deny/other-agent trust/depth/memory/runtime gates |
| 8 / T08 | AutomaticTaskModeAndTiming | Unit | BDD-06.1 | MAIN precedence, fresh MAIN IDs, worker CONTINUE/new run, isolation checkbox, once/RRULE positive controls |
| 9 / T09 | SelectedExecutionControlAndHeldState | Unit | BDD-07.1, BDD-07.2, BDD-07.3 | Correct scope/disposition and no stale turn mutation/held auto-replay |
| 10 / T10 | PerSourcePeerReturnAddressing | Unit | BDD-08.2, BDD-08.3, BDD-08.4 | Permission/target scope and exact destination/correlation, zero guessed sends |
| 11 / T11 | CanonicalSessionAwareCommandCapabilities | Unit | BDD-09.1, BDD-09.3, BDD-12.1 | Typed and menu actions agree; old aliases do not execute; no clear-as-new |
| 12 / T12 | CurrentActivityAndCanonicalReducerCases | Unit | BDD-10.1, BDD-10.3, BDD-12.2 | Real run/usage identity, no duplicate MAIN row or missing-current-output heuristic |
| 13 / T13 | IdleEpisodesAndCurrentRecapTriggers | Unit | BDD-11.1, BDD-11.4 | Activity resets, no settling overlap, repeat episode and joined retained |
| 14 / T14 | CurrentGeneratedContractControls | Unit | BDD-12.3 | Passing valid payload plus targeted invalid field/action, byte-limit runtime check distinct from character schema |
| 15 / T15 | AtomicProvenanceFIFOAndSettlement | Integration | BDD-02.4, BDD-03.1, BDD-03.2, BDD-03.4, BDD-07.2 | Actual save/intake/model inputs and boot/reload behavior; force settlement/control races |
| 16 / T16 | ReportWakeHoldRefusalAndDedupe | Integration | BDD-04.1, BDD-04.2, BDD-04.3, BDD-04.4 | Real inbox/runner delivery, stopped zero dispatch, refusal visibility and unchanged full-result limitation |
| 17 / T17 | TaskParentAdmissionCaptureAndTerminalNotice | Integration | BDD-01.3, BDD-06.1, BDD-06.2, BDD-06.3, BDD-06.4 | Real task/run/goal identity, stopped-main launch/Stop order, captured recipients and authoritative result |
| 18 / T18 | AddressedGuestAndConnectorReplyRoute | Integration | BDD-08.1, BDD-08.2, BDD-08.3, BDD-08.4 | Minimal addressed context, own/guest awareness, replay identity and connector-instance egress |
| 19 / T19 | CLINativeResumeAndFailureIdentity | Integration | BDD-05.5, BDD-05.6 | Actual process/driver request carries native ID and instruction; no fresh/no-op success |
| 20 / T20 | RestartInterruptedWithoutWaitingReplay | Integration | BDD-07.4 | Source-integrated recovery, zero old-input boot dispatch, current manual continuation; #1217 lane remains independently owned |
| 21 / T21 | CommonAgentDeletionAndPartialResult | Integration | BDD-11.2, BDD-11.3 | One cascade through UI/tool/API, guarded/partial result, owned data removal and guest-history retention |
| 22 / T22 | SafeClearAndExistingWorkerContinuation | Integration | BDD-09.2, BDD-09.4 | Boundary-installed window/display, retained input/history and no completed-task rerun |
| 23 / T23 | CanonicalDeletionAndContractGeneration | Integration | BDD-12.1, BDD-12.2, BDD-12.3 | Enumerated retired callers/shapes gone and current kickoff/no-stream/evicted-owner/plan-scope behavior still works |
| 24 / T24 | JointMainExtraNavigationAndSearch | E2E | BDD-01.1, BDD-01.2, BDD-01.4, BDD-09.1, BDD-09.2 | Real sidebar/backend pair attach, sole row New chat, clear/search and protected delete refusal |
| 25 / T25 | LiveStopRedirectHeldInput | E2E | BDD-07.1, BDD-07.2, BDD-07.3, BDD-09.4 | Real generated redirect path, UI live/reload consistency, correct held/tree scope |
| 26 / T26 | GroupIdentityAndMixedSources | E2E | BDD-08.1, BDD-08.2, BDD-08.3, BDD-08.4 | Guest name/icon no handover, source-specific replies, visible unaddressed refusal |
| 27 / T27 | AgentTaskActivityAndApprovalAttribution | E2E | BDD-10.1, BDD-10.2, BDD-10.3, BDD-06.4 | Existing Activity/approval/result links, real counts/usage, native/external distinction |
| 28 / T28 | KeyboardWorkerCommandsAndControlStates | E2E | BDD-09.3, BDD-10.4 | Keyboard/menu/typed parity, names/focus/announcements, touch/zoom/reflow and safe dismissal |
| 29 / T29 | IdleResumeAndDoubleConfirmOwnedDeletion | E2E | BDD-11.1, BDD-11.2, BDD-11.3, BDD-11.4, BDD-01.3 | Same-ID idle resume, UI-only double confirm, one cascade and hidden-versus-deleted distinction |
| 30 / T30 | ExactCandidateJointReachabilityAndDocs | E2E | BDD-12.4, BDD-12.2 | Real tool/UI paths, joint generated contract, matching audited docs, complete tester+validator evidence |
| 31 / T31 | ActualThreeCLILiveSteering | E2E | BDD-05.5, BDD-05.6 | Supported installed CLI preserves prior native marker and receives new instruction; unavailable/auth/resume failure remains explicit |

### Later-decision test families

These extend the same plan; unit cases precede integration/E2E execution within each family. They introduce no second test or runtime system.

| ID | Proposed family / level | BDD scenarios | Required oracle and execution |
|---|---|---|---|
| T32 | MainOnlySharedAttention — Unit, Integration, E2E | BDD-13.1, BDD-13.2, BDD-13.3, BDD-13.4 | Exact source mapping including other; optional-on-general/present-on-main field; two users/shared bounded seen, explicit open/resolve/reconnect/race and unopened-main live update. Verify runtime-delivered structured-question prompt and real card path. |
| T33 | SavedChatCutoverPreservation — Unit, Integration, E2E | BDD-14.1, BDD-14.2, BDD-14.3, BDD-14.4 | Frozen supported old fixtures, heartbeat→main, full binding/history/real continuation, stage/publish/crash/idempotence, visible failures/source retention and one-format steady state. |
| T34 | CanonicalTaskFamilyWorkspaceMerge — Unit, Integration, E2E | BDD-08.5, BDD-12.1, BDD-12.3 | Actual canonical Parameters/contract optional workspace, own-versus-explicit scope, moved Ava/Admin/other callers, Operator Deny, current validator/store and no duplicate tool/plan behavior. Audit reachability on effective tools, not metadata names alone. |

### Test datasets

Each table's rows expand rather than replace the scenario outlines. Null/empty/zero values use the current schema/effective-setting meanings; no new config-validation or media policy is invented. A fixture that disables a competing bound does so explicitly to isolate the intended failure.

#### Dataset A — Ordinary intake and identity

| Row | Input / state | Boundary type | Expected outcome | Traces to |
|---|---|---|---|---|
| A01 | 1-byte text with ample headroom | minimum valid | accepted with authenticated sender and distinct message | BDD-03.1 |
| A02 | empty content, no media / null content | empty/null | current message schema rejects; no admission | BDD-02.4, BDD-12.3 |
| A03 | empty caption, valid media reference | alternate valid | accepted if media/model/other bounds fit; reference retained | BDD-03.3 |
| A04 | text 65,535 / 65,536 / 65,537 UTF-8 bytes | max−1/max/max+1 | accept/accept/refuse with other bounds isolated | BDD-03.3 |
| A05 | combining characters, multibyte text, newlines at those byte boundaries | Unicode | same byte-bound result; not character-only maxLength | BDD-03.3 |
| A06 | 0 / 59 / 60 previous admitted sends in the minute | zero/rate boundary | first / 60th accepted; 61st refused for same trusted sender+target | BDD-03.3 |
| A07 | sender A reached 60; sender B and same display name on another trusted instance send once | fairness/provenance | B/distinct principal has own rate scope, subject to target's other bounds | BDD-03.3, BDD-08.3 |
| A08 | 199 / 200 waiting ordinary items | count max−1/max | candidate 200 admitted if fits; candidate 201 refused | BDD-03.3 |
| A09 | resulting aggregate 1,048,575 / 1,048,576 / 1,048,577 bytes | aggregate max−1/max/max+1 | accept/accept/refuse if model fits | BDD-03.3 |
| A10 | real assembled estimate B−1 / B / B+1 after permitted old-window slide | model-fit boundary | accept/accept/refuse with byte/count/rate room | BDD-03.3 |
| A11 | tiny text plus high media/tool/pinned overhead | non-text cost | current estimator/model fit, not text-only fit | BDD-03.3 |
| A12 | late instruction during final-settlement barrier plus separate fresh chat of same agent | concurrency | immediate same-session next turn; no cross-chat queue/control contamination | BDD-03.2, BDD-07.2 |
| A13 | valid non-default settings before reload/after reload/fresh boot | wiring | observed intake boundaries change only after current live application; all ordinary adapters agree | BDD-03.4 |
| A14 | control text claims another sender/parent; provenance save denied | spoof/I/O failure | trusted identity cannot be replaced by text; failed atomic acceptance not consumed | BDD-02.4, BDD-08.2 |

#### Dataset B — Window, retention and control order

| Row | Input / state | Boundary type | Expected outcome | Traces to |
|---|---|---|---|---|
| B01 | zero retained entries; same eligible main metadata | empty | empty same-ID chat/model view, protection retained | BDD-02.3, BDD-10.3 |
| B02 | marked yesterday's byte offset, today's complete tool group | day boundary | valid bounded multi-day window; prefix unchanged | BDD-02.1, BDD-02.2 |
| B03 | delayed older timestamp after newer accepted input | ordering | accepted FIFO, no earlier-file back-insertion | BDD-02.2 |
| B04 | call/result crosses midnight; repeated tool ID in later turn | structural identity | matching provider-valid group and correct result reference | BDD-02.2 |
| B05 | file mtime younger than / equal to / older than 90-day cutoff | age boundary | current sweep's strict older-than deletion only; expired mark repaired | BDD-02.3 |
| B06 | retention disabled; old content present | disabled | no deletion or expiry repair manufactured | BDD-02.3 |
| B07 | retained fragment then complete group; no complete group/content | incomplete/empty | first retained complete group onward or empty same-ID; agent expiry notice | BDD-02.3 |
| B08 | clear requested while step in flight, human H pending | safe point | old in-flight step unchanged; next boundary clears display/window, not H/archive | BDD-09.2 |
| B09 | Stop with H1/H2 and older parent control P | held/superseded | new input doesn't release all H; explicit release/discard honored; P never returns | BDD-07.3 |
| B10 | Stop/redirect/steer before/after publication barrier, then replacement turn | both race orderings | authoritative dispositions; refused text absent; stale force/completion cannot mutate replacement | BDD-07.2 |
| B11 | first Stop, second at <3 seconds, after window expiry, cancel | activation boundary | current-turn/tree/current-turn/tree scope; no separate Stop-all UI | BDD-07.1 |
| B12 | gateway crash while running / I1-owned waiting-for-answer, queued text present | restart | Interrupted integration, no old-input boot dispatch, manual next-message continuation | BDD-07.4 |

#### Dataset C — Report, task, peer and runtime matrix

| Row | Input / state | Boundary type | Expected outcome | Traces to |
|---|---|---|---|---|
| C01 | each accepted existing helper report kind; idle/live/stopped parent | delivery states | common wake/safe-boundary/held behavior without cap widening | BDD-04.1, BDD-04.2 |
| C02 | same accepted report identity retried | duplicate | one saved/consumed content identity, no extra wake after acknowledgement | BDD-04.4 |
| C03 | ordinary non-exempt report reaches current rate cap, then one more | preserved cap+1 | typed rejection/retry information and parent rejected visibility | BDD-04.3 |
| C04 | report encoded envelope just below / at / above existing body cap | envelope boundary | preserve current cap behavior; rejected content not claimed delivered | BDD-04.3 |
| C05 | multiple full TaskRun results individually valid but collectively too large | accepted limitation | current delivery/context failure visible; task results stay; no new summary/defer path | BDD-06.4 |
| C06 | MAIN manual/once/RRULE / worker once/RRULE / scheduled isolate on/off | mode matrix | exact BDD-06.1 identities/modes; once/at_ms positive control, no old every/cron_expr | BDD-06.1, BDD-12.3 |
| C07 | recurring worker first/later run; recurring MAIN first/later run | identity across runs | worker same chat/new run; MAIN fresh real child/new run each time | BDD-06.1 |
| C08 | creator A, starter B, assignee Mia; starter=assignee variant | recipient identity/dedupe | captured B-main/Mia-main; one recipient when equal; no creator-only delivery | BDD-06.3 |
| C09 | recipient deleted/hidden/reassigned after run start | lifecycle change | capture fixed, stored result retained, no guessed replacement/hidden wake | BDD-01.3, BDD-06.4 |
| C10 | done/failed/winning Stop/late success/skipped/per-retry | outcome matrix | authoritative actual-run final/reason only; no contradictory late success, skipped/retry final notice | BDD-06.3 |
| C11 | extra-chat Auto off, main Auto on, target tool-policy Deny/Ask; unattended required Ask; #1221 removes agent Auto off-switch | permission source/risk | main parent source within target restrictions; no unattended popup wait; loosening disclosure | BDD-06.2, BDD-10.2 |
| C12 | omitted/explicit native self, depth below/at default 3, denied delegate, other-agent no edge | authorization boundaries | ordinary self parity; normal depth/policy/memory gates; other-agent edge unchanged | BDD-05.1, BDD-05.2, BDD-05.3, BDD-05.4 |
| C13 | explicit same-/cross-workspace MAIN pairs without edge / Admin default pair / invalid or worker target | address eligibility | eligible pair communication permitted without control grant; invalid/unauthorized pair refused | BDD-08.2 |
| C14 | source-history-only marker Z; addressed request carries X | minimal context | receiver gets request/X, not Z; owner sees request/guest answer | BDD-08.1 |
| C15 | same-platform I1/I2, distinct principals/threads/correlations + web sender | return routing | exact per-answer source route; no broadcast or last-sender fallback | BDD-08.3 |
| C16 | missing return address/null correlation; deleted recipient | error | visible refusal, no guessed outbound destination | BDD-08.4, BDD-06.4 |
| C17 | each installed CLI with actual native ID/marker; missing ID/CLI/auth/resume failure | external runtime | true native resume with text/caps or explicit failure; never no-op/fresh fallback | BDD-05.5, BDD-05.6 |

#### Dataset D — UI, canonical branches, recap and deletion

| Row | Input / state | Boundary type | Expected outcome | Traces to |
|---|---|---|---|---|
| D01 | native MAIN in default/second workspace; Admin default-only; worker/other system agents; longest valid pair IDs | identity/eligibility | correct computed eligible mains only; invalid pairing refuses | BDD-01.1, BDD-01.4 |
| D02 | row click/New chat/switch/sessions; roster connect/reconnect | entry-point matrix | main/extra/navigation/search distinct; existing roster refresh, no widening | BDD-09.1 |
| D03 | every allowed/forbidden/retired worker command through typed and palette entry | capability matrix | final table parity at execution, no alias bypass | BDD-09.3 |
| D04 | live / stopped / finished existing worker task chat | continuation | steer/legitimate continue/conversation-only follow-up, no task rerun | BDD-09.4 |
| D05 | one MAIN run appears as task and child; independent monitored task; queued/waiting/terminal helper | projection | one MAIN row, truthful scope/count, real available usage and own Open link | BDD-10.1 |
| D06 | existing approval pending/resolved/expired/out-of-workspace; native/external | approval boundary | existing scope/actions/ID preserved, acting attribution, no external protection claim | BDD-10.2 |
| D07 | loading/no retained content/failed roster refresh/missing lifecycle or usage | view states | loading/empty/error/partial, no false main/running/token/success fallback | BDD-10.3 |
| D08 | keyboard/screen-reader, menu/dialog Escape, touch versus pointer, 200% zoom/320px reflow | accessibility | named reachable controls, safe dismissal, focus ownership/restoration, no clipped/overlapping targets | BDD-10.4 |
| D09 | idle 29:59 / 30:00 after last real activity; new message/turn reset; live settlement | idle boundary | recap only at actual idle threshold and after settlement; same ID continues | BDD-11.1 |
| D10 | resume same ID, then another full idle episode; joined retrospective/helper fixture | repeated/current behavior | new idle episode recap possible, joined/helper memory unchanged, no archive summary | BDD-11.1, BDD-11.4 |
| D11 | UI first-only/two confirmations; tool/API single approval | destructive-action gate | only authorized correctly confirmed invocation reaches common delete; no API/tool second gate | BDD-11.2, BDD-11.3 |
| D12 | owned sessions/memory plus guest responses elsewhere; locked/system/active-plan; failed cleanup | data/partial failure | guards preserved; owned-only removal; guest deleted-agent label; accurate partial result | BDD-11.2, BDD-11.3 |
| D13 | each of the 66 explicit deletion inventory rows | removal matrix | retired behavior absent; canonical replacement/nothing as specified | BDD-12.1 |
| D14 | current kickoff, no-stream, evicted-owner, delayed spans, reconnect offsets, typed/control errors, keyed goals, plan verdict | preserved current producers | no lost/current hidden output or guessed owner/latest goal; real canonical identity path | BDD-12.2 |
| D15 | valid and targeted-invalid current/generated boundary payloads | contract controls | validator/runtime agreement; old shapes/actions refused and current once/RRULE accepted | BDD-12.3 |
| D16 | exact joint candidate and audited matching docs; all real tool/UI paths | delivery gate | reachability actually executed, exact SHA tester/validator and five-reviewer receipts | BDD-12.4 |

#### Dataset E — Approved attention, migration and tool merge

| Row | Input / state | Boundary type | Expected outcome | Traces to |
|---|---|---|---|---|
| E01 | main pending ask / pending approval / met / rounds_exhausted / other | four-source matrix | true for the main's corresponding outstanding/unseen source | BDD-13.1 |
| E02 | stopped_by_user only; extra/helper/worker sources; generic task notice/plan verdict | negative sources | no dot from these; non-main needs_attention omitted | BDD-13.1 |
| E03 | no source; Admin default main versus Admin at another workspace | empty/identity | false for valid default main; no other-Admin main invented | BDD-13.1, BDD-01.4 |
| E04 | user A explicit foreground open; user B view same pair | shared read | observed goal attention cleared for both; pending asks/approvals untouched | BDD-13.2 |
| E05 | new goal outcome arrives past captured open bound; acknowledged replay ID repeats | concurrency/dedupe | new remains unseen; old replay does not relight | BDD-13.2, BDD-13.3 |
| E06 | background prefetch/reconnect; missing source/error; failed/unauthorized attach | failure/read intent | no seen mutation or false/cleared fallback; visible unknown/error | BDD-13.3 |
| E07 | permitted structured answer request versus unavailable/denied tool | prompt/card | current real structured card/tool and delivered rule, or honest refusal; no raw-text detector/bypass | BDD-13.4 |
| E08 | frozen ordinary/extra/task/worker and heartbeat saved fixtures, known full views/bindings/references | migration happy | retained searchable/recallable content and real continuation; heartbeat computed main with history | BDD-14.1 |
| E09 | repeat upgrade; crash before/after stage, identity publication, completion/source finalization | idempotence boundaries | one complete correct converted identity, no duplicate/truncation; resumable conversion | BDD-14.2 |
| E10 | malformed/unreadable source, wrong pair at target, disk/write/publish failure | migration error | affected-ID failure visible; source retained; no empty/default/incorrect success | BDD-14.3 |
| E11 | fresh install / already converted normal input | steady state | one current format/store, no compatibility reader or boot queued-input replay | BDD-14.4 |
| E12 | canonical create/update/list, workspace argument absent / explicit W2 | optional choice | own workspace / chosen valid workspace through current gates; no implicit elsewhere | BDD-08.5 |
| E13 | explicit null/empty/invalid/inaccessible workspace; unresolved caller; Operator Deny | error boundary | existing typed/schema/authorization refusal, zero fallback/write/disclosure | BDD-08.5, BDD-12.3 |
| E14 | moved Ava/Admin caller/policy/catalog references; all four retired task names invoked | caller/deletion | canonical family reachable when permitted; old family absent/refused, no alias | BDD-08.5, BDD-12.1 |
| E15 | explicit other workspace plus foreign plan/dependency/active-run conflict | invariant | existing all-or-nothing validation refuses prohibited state; no cross-workspace plan move/copy | BDD-08.5 |

### Regression test requirements

This modifies substantial existing behavior. Preserve safety assertions; remove only obsolete compatibility expectations explicitly named for DELETE. The following **existing tests were read**, not run. QA ports their behavioral assertions to the matured canonical surfaces if a deleted fixture/store type prevents retaining the old test body; weakening the assertion is not a valid port.

| Preserved behavior | Existing source test | Required regression complement |
|---|---|---|
| Window eviction deletes zero archive bytes; evicted history remains recallable | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/memory/archive_test.go::TestArchive_SkipEvictionDeletesZeroBytes`, `TestReadArchive_ReachesEvictedTurn` | T02/T03/T22 assert day-byte mark/projection/clear and unchanged prior prefixes |
| Inbox message-ID dedupe | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/message_inbox_test.go::TestMessageInboxStore_DedupeByMessageID` | T06/T16 preserve dedupe and consumption/wake identity while extending accepted kinds |
| Native plain Stop retains real background process; tree Stop kills it | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/stop_session_background_q13_unix_test.go::TestQ13StopSession_PlainStopEndsTurnLeavesRealBackgroundAlive`, `TestQ13StopSession_StopAllKillsRealBackgroundProcess` | T09/T25 keep selected-execution and downward-tree scope with MAIN task children; external exception is separate |
| Current Calendar one-time authoring and common execution | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/src/components/calendar/CalendarEventSlideOver.tsx::buildTriggerForSave`; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/task_trigger.go::triggerToCronSchedule` | T08/T17/T23 keep once/at_ms and RRULE; no every/cron_expr compatibility fixture survives |
| Current first-send/kickoff/non-stream/approval/goal/plan producers | Exact canonical producer/reducer mapping in DELETE inventory and E-NAV/E-APPROVAL/E-ACTIVITY | T12/T23/T24/T27 force current cases, not only old-schema absence or mocked successful render; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
| Task claim/goal/attempts/outcome and plan recovery | E-TASK/E-SCHEDULE existing task-origin path; S-ADR::P1–P17 | T17/T20 preserve one task outcome owner, separate run identity, task-specific limits and Stop-authoritative result |

Regression dataset rows B02/B04/B10/B11, C01/C02/C06/C10/C11 and D06/D14 run against baseline and candidate where behavior is intentionally preserved. New decided behavior is expected RED on baseline, not mislabelled regression. Bugs #1211/#1214 need controlled baseline failure receipts; #1216 needs an actual setting/admission mismatch, not only a symbol-search receipt.

## Functional Requirements

Each path key expands to the exact source citations above. MUST requirements are normative; no optional scope is introduced. The traceability matrix maps every requirement, scenario and documentation TODO.

| ID | Requirement | Existing path extended | Authority |
|---|---|---|---|
| FR-001 | All boundary changes MUST be contract-first and use committed generated types/runtime validation only; remove obsolete shapes, no parallel wire types. | C-MAIN–C-LIMIT and existing generation/validator path | S-RULES Constraint #8; S-ADR::D1.1/D10 |
| FR-002 | Eligible MAIN membership MUST eagerly create/reuse exactly one computed pinned/protected main pair, heartbeat-independent; workers/other system agents have none, except Admin has a default-workspace main only. Pair mismatch/corruption/unauthorized binding MUST refuse without guessing. | E-MAIN; existing role eligibility | S-ADR::D1/D1.1 |
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
| FR-026 | Same-workspace MAIN peers MUST be addressable without delegation edges; workers excluded, no bare-agent/unauthorized pair delivery, whole-source-history read or transferred control/permissions; explicitly addressed cross-workspace messaging is permitted. | E-INBOX/E-MAIN; existing send_message path | S-ADR::D7 |
| FR-027 | Group requests/answers MUST preserve responder and return identity live/replay; owner sees both; guest answer wakes idle non-stopped owner once, stopped holds, silence creates no empty bubble. | E-INBOX/E-STORE/E-COMMAND | S-ADR::D7 |
| FR-028 | Mixed-source answers MUST each address their sender with original instance/chat/thread/session/correlation; unaddressed output MUST visibly refuse, with no fallback/broadcast; channel input uses shared bound main for now. | E-INBOX/E-QUEUE; existing send_message ownership | S-ADR::D7; Q3=B; #1206 cross-reference |
| FR-029 | Row click/switch navigation MUST open eligible main; only agent-row New chat creates extra MAIN chat in UI; preserve team picker and existing connect/reconnect roster refresh. | E-NAV/E-COMMAND | S-ADR::D8; S-FE::D3/D5/D12 |
| FR-030 | Clear MUST execute at next safe point, change context/current display with marker, preserve same session, pending input and retained recall/search; no false claim that a live step forgot input. | E-COMMAND/E-QUEUE/E-STORE | S-ADR::D8; Q-R2-9 |
| FR-031 | Canonical sessions search/switch navigation and final worker commands MUST be enforced at palette AND typed/backend execution; retire all named old commands/aliases. Read-only lists do not permit model/config mutation. | E-COMMAND | S-ADR::D8 final 12:35 table |
| FR-032 | Authorized input into existing worker/child sessions MUST steer live task or continue finished chat without mutating/rerunning completed task; fresh worker chats and worker mentions remain forbidden. | E-STOP/E-TASK/E-NAV | S-ADR::D8; Q-R2-10 |
| FR-033 | Existing Activity MUST include relevant agent tasks/scheduler runs not started by this chat, truthful starter/assignee/parent distinctions, one MAIN row, actual running/waiting/token data and Open links; no extra dashboard. | E-ACTIVITY/E-TASK | S-ADR::D4/D5/D6 |
| FR-034 | MAIN children MUST inherit main-parent approval within target restrictions, with possible loosening disclosed; native helper/run labels/links use existing approval ID/modal; unattended Ask immediately refuses, external exception remains explicit. | E-APPROVAL/E-TASK/E-CLI | S-ADR::D10; Q-R2-3/5; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
| FR-035 | Existing UI MUST cover loading/empty/error/partial/success, keyboard/assistive reachability and truthful no-guess state with catalogued components and current focus/touch/zoom rules; no new visual-design decisions. | E-NAV/E-COMMAND/E-ACTIVITY/E-APPROVAL/E-DELETE | S-RULES; S-FE layout boundary; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
| FR-036 | Default-on auto recap MUST use thirty-minute activity-based idle episodes including main, reset on any turn/message, not overlap execution/settlement, keep idle/bootstrap/joined and helper behavior, DELETE lazy/explicit session_close/ack. | E-RECAP/E-QUEUE | S-ADR::D9 |
| FR-037 | One agent-delete cascade MUST keep guards, remove owned sessions/memory, retain guest responses labelled deleted agent, warn/twice-confirm UI only, keep tool/API single approval and truthful partial cleanup. | E-DELETE/E-STORE; existing profile confirmation | S-ADR::D9; Q6=B |
| FR-038 | Every scoped DEL-01–23 and DEL-F01–43 MUST be DELETE with the named canonical replacement or nothing, including registration/contract/alias/compatibility-only tests/docs; no ongoing migration/backfill/shim outside the bounded 17:15 saved-chat import exception. | Exact DELETE inventory from S-ADR including 17:45 DEL-23 | S-LEDGER::14:45; S-ADR::Consequences |
| FR-039 | Scoped removal MUST preserve current behavior sharing old branches and all preserved safety boundaries; finish direct-caller sweep before deletion, no guessed latest goal/owner/producer or side fixes outside scope. | Canonical paths in S-ADR::DEL-F and preserved inventory, E-STOP/E-STORE/E-POLICY | S-ADR::DEL-F boundary; P1–P17 |
| FR-040 | Existing single pure-Go binary/file stores, supported platform degradation, session-scoped tool/browser/recall state, memory-based admission, lock order and security footprint ceiling MUST remain; no permanently hot lifetime main runner/archive. | E-STORE/E-RETENTION/E-POLICY and existing runtime | S-ADR::D10; S-RULES constraints |
| FR-041 | Matching user-facing DOC TODOs MUST land with each behavior/UI change and be audited by docs-verifier; module instruction changes require byte-identical AGENTS twins. | Existing docs pages and module instruction process | S-RULES Definition of Done |
| FR-042 | Real UI/agent reachability, RED/GREEN/CHECK, five-reviewer gate and tester+independent-validator UAT on exact candidate MUST precede landing; main navigation/sidebar land jointly after integration; this author stops before grill. | E-POLICY/E-NAV/E-TASK and existing feature delivery process | S-ADR::D10; S-FE::D12; S-LEDGER::14:50 |
| FR-043 | All three existing external worker drivers MUST deliver live instruction through interrupt plus actual native-conversation resume with original runtime/workspace/model/caps preserved; missing native identity or failed delivery/resume MUST refuse visibly, not return successful no-op or silently start fresh. Disclose subprocess-kill exception. | E-CLI/E-STOP/E-QUEUE | S-ADR::D6; DEL-20; 10:35; Q-R2-5 |
| FR-044 | This install/upgrade cutover MUST idempotently preserve supported saved-chat full history/effective agent/workspace/continuation and turn existing heartbeat history into the computed main. Stage/validate before completing conversion, keep source on visible failure, re-key affected existing references, no guessed identity/empty replacement, duplicate/truncation, new execution or steady-state dual reader. Greenfield/DELETE remains elsewhere. | E-MIGRATE/E-STORE/E-MAIN and current reference owners/writers | S-ADR::D11; founder 17:15 |
| FR-045 | Every @/peer/cross-workspace recipient MUST resolve the explicit workspace_id+agent_id pair using existing channel pair semantics to that computed main; default @ chooses current workspace, another is explicit; Admin default-only address, no bare-agent or invalid-target fallback. | E-ADDRESS/E-MAIN/E-INBOX/E-NAV; C-ADDRESS | S-ADR::D11; founder 17:25 |
| FR-046 | MERGE task families into canonical create_task/update_task/list_tasks with optional workspace_id, absent = own workspace, explicit other only; consolidate delete callers into existing delete_task. DELETE duplicate *_in_workspace family/aliases/registrations and move Ava/Admin/other callers/policies/defaults/prompts, reusing existing validation/store/audit and preserving Operator Deny, assignment/criteria/owner/plan/run invariants; no cross-workspace plans or new executor. | E-TASK-TARGET and canonical TaskListTool in the exact DEL-23 mapping; C-TASK-TOOLS | S-ADR::D11/DEL-23; founder 17:45 Q8=MERGE |
| FR-047 | Existing main list/detail MUST compute read-only needs_attention from main-only pending structured asks/approvals or unseen met/rounds_exhausted/other goal outcomes; stopped_by_user excluded, non-main omitted. Shared bounded main metadata mark and explicit successful foreground open acknowledge observed goal attention for all users, never unresolved asks/approvals or newer racing outcomes; existing attach/frames/snapshots/query refresh restore truthful live state, no new service. Structured user-answer prompt rule belongs to prometheus-prompt-engineer. | E-ATTENTION/E-STORE/E-NAV/E-PROMPT and existing pending approval surface (separate #1221); C-ATTENTION | S-ADR::D11; founder 17:15/17:45 Q7=A |

## Success Criteria

| ID | Pass/fail outcome | Proof required |
|---|---|---|
| SC-001 | One correct immutable protected main per eligible pair including Admin’s default-only main; zero worker/other-system mains, extra Admin-workspace mains or wrong-pair fallback | T01/T24 plus actual stored identity and contract/UI attach |
| SC-002 | One archive/two views; zero prior retained-byte deletion from clear/correction/sliding; model-window reads begin at the file/byte mark and complete tool groups remain valid | T02/T03/T22 with read instrumentation, archive-prefix comparison and recall/replay |
| SC-003 | Every applicable approved ordinary bound is enforced before admission; every accepted ready instruction is consumed in separate FIFO order; no extra wait/queue/runner | T04/T15 with boundary negatives, sender identity controls and forced settlement interleavings |
| SC-004 | Live settings change admission at boot/reload; accepted helper-kind wake works while preserved report caps remain; rejected report is visible to child and parent | T05/T06/T16 and RED receipts for #1211/#1216 |
| SC-005 | All mode/parent/recipient/outcome rows match this spec; task/run/goal authority stays task-owned; Stop/redirect never touch a replacement execution or deliver refused text | T08/T09/T17/T25 and RED #1214 race reproduction |
| SC-006 | Every addressed guest/mixed-source answer retains its intended return destination and responder live/replay; zero whole-source-history transfers or guessed/unaddressed sends | T10/T18/T26 |
| SC-007 | Navigation/clear/worker commands/input/activity/approval/idle/deletion behavior is reachable through actual UI and existing tools, including keyboard/error/partial states | T11/T12/T13/T21/T22/T24–T29; real screen evidence |
| SC-008 | All 66 scoped DELETE rows removed with no runtime alias/shim or unrelated migration; the bounded saved-chat cutover remains required; corresponding current producer/canonical behavior still passes; generated contracts and real validators agree | T14/T23 plus controlled caller/deletion inventory, positive absence controls, regeneration and runtime payload controls |
| SC-009 | All three existing CLI drivers demonstrate real native-conversation steering or honestly refuse unsupported/unavailable identity; zero successful no-op/fresh-run fallbacks | T19/T31 with actual native marker/instruction evidence and documented Stop subprocess exception |
| SC-010 | Matching DOC-001–DOC-009 updates audited, five-reviewer findings closed/deferred explicitly, exact candidate UAT independently validated, and joint navigation/main-backend integration completed before founder-approved landing | T30 and delivery evidence tied to exact candidate SHA; plans alone fail |
| SC-011 | Saved-chat cutover preserves known content/binding and real continuation, heartbeat→main, with repeat/crash/source failure proofs and no dual runtime | T33, frozen supported fixtures, actual follow-up and visible failure/source-retention evidence |
| SC-012 | Main-only needs_attention exactly matches four sources/mapping and shared bounded open/resolve semantics, with actual unopened-main updates/reconnect and source-error honesty | T32, two-user and append/open race evidence, generated response/attach validation |
| SC-013 | Canonical create/update/list optional-workspace behavior and moved callers work under current permissions; all duplicate task-family calls are absent, no implicit elsewhere or cross-workspace plans | T34/T23, effective tool catalog/policies and actual task/store/query operations |

No measured latency/throughput claim, full-suite green, supported-platform certification or live CLI result is produced by this specification task. Existing platform/build/security footprint constraints remain separate release evidence, not a green inferred from the spec.

## Reachability

**Check reachability before correctness claims.** No new tool is added. Use the existing tool families and real screen entry points below. Registration alone grants no access: the reconciled global ceiling is the complete first policy layer; sparse per-agent overrides can only tighten. Do not add a third layer or per-agent deny backfill to make a reachability checklist appear complete.

| User/agent invocation | Existing production registration/policy and surface | Required reachable proof |
|---|---|---|
| Main and extra chat | E-MAIN + E-NAV; existing agent-row/session attachment and generated C-MAIN | Real eligible row opens the computed main; only row New chat creates extra; heartbeat-off still navigable; no latest/fresh fallback |
| Native self/helper work | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata` registers delegate metadata; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/loop_wire.go::registerDelegationTools` wires real runtime; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/config/defaults.go::defaultToolPoliciesGeneral` carries delegate in the global ceiling | Permitted native MAIN/WORKER can actually call delegate with omitted/explicit self; Deny still blocks; ordinary helper's real chat/activity link opens |
| Reporting/addressed communication | Existing message_parent and send_message metadata/global policy in the same catalog/defaults; existing `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/agent/session_messaging_wire.go::wireSessionMessagingForAgent` and E-INBOX/send_message wiring | Real child report, authorized MAIN-peer send and human mention reach the same common transport; status/reply returns with identity/correlation, no fake parent edge |
| Existing operator config | Global tool-policy data remains in `/Users/danielpiatkowski/.omnipus/config.json` under sandbox.tool_policies and existing per-agent policies; do not edit this live installation for this task. Source defaults/config loader remain the policy reference | Fresh isolated fixture has complete ceiling entries for delegate/message_parent/send_message and target Deny/Ask behavior; sparse per-agent coverage is normal |
| Task/scheduled execution | E-TASK/E-SCHEDULE and current task tool/API/Calendar paths, existing workspace/task/run UI | Manual/Calendar firing actually dispatches assignee under derived mode; current task/run result and activity Open link reach the correct chat |
| Controls and worker follow-up | E-STOP/E-COMMAND and actual generated WS/REST/command path; opened worker/task session route | Real Stop/redirect/held release/discard/clear and typed/menu capabilities operate; completed-task chat input does not rerun the task |
| Approval/usage/deletion | E-APPROVAL/E-ACTIVITY/E-DELETE; existing profile, modal, background panel and task/run views | Real native approval names/opens acting run; known usage is actual; UI-only double confirmation and tool/API common cascade are exercised; approval inheritance follows separate [#1221](https://github.com/elicify-ai/omnipus/issues/1221), no per-agent Auto off-switch. |
| External worker steering | E-CLI through existing opened worker chat, not a new direct CLI product | Native prior marker survives interrupt/resume and new text arrives in all supported installed CLI lanes; absent capability fails visibly |
| Main attention | C-ATTENTION through existing Session list/detail, attach and pending/question/approval/goal frames | Real sidebar main including Admin default reflects exact source/seen state; foreground open clears only shared observed goals; structured user question is a real card/tool, not prompt text alone |
| Saved-chat upgrade | E-MIGRATE before normal store/main publication, current install/upgrade/read/write path | Use pre-cutover saved fixtures, search/open/recall and real follow-up after migration; repeat/crash/failure tests, not a fresh-install demo |
| Merged task tools | E-TASK-TARGET canonical create/update/list and existing delete; normal catalog/role/default policies, no duplicate sysagent task registration | Real Ava/Admin/other permitted callers invoke own/default and explicit other workspace; Deny remains; no old tool aliases or cross-workspace plan behavior |

Tool registration/policy verification searches the **whole tracked catalog/default/config/core-agent/loop wiring**, with a known tool positive control, then invokes the real effective agent tool surface. A zero match on one guessed file or metadata-only registry is not proof of unreachability or readiness. Remove switch_agent from registration, policy and prompts as required by DEL-07/21; that does not add a similarly named tool for slash navigation.

### Execution and hand-off gates

| Stage / owner | Required action; no action in this document task is claimed complete |
|---|---|
| Contract step — backend-lead | Commit schemas and regeneration first; concrete exchange names/fields must implement C-MAIN–C-LIMIT and no extra design |
| RED — qa-lead | Implement scenario/dataset families, reproduce scoped bugs and record failing pre-change evidence |
| GREEN — backend/frontend leads; prompt engineer | Existing-path implementation, scoped deletions, matching docs; source-appropriate prompt authorship; no test weakening/side fixes |
| CHECK — different qa-lead | Independent integrity/mutation checks prove assertions detect intended failure; executed test plan, not merely tests on disk |
| Joint navigation integration | Include S-FE's reviewed/answered frontend work and working main-session backend on the same candidate; actual main/extra/history/failed-attach/identity integration before joint landing. Frontend kit preparation alone does not satisfy it. |
| Feature gate | Three mandatory plugin reviewers plus architect cross-cutting and security-lead; clean findings or explicitly approved tracked deferrals. Review is read-only; authors do not adjudicate disputes over their own design. |
| Exact-commit hands-on UAT | uat-tester plus independent uat-validator, exact SHA being landed. Agent-driven onboarding/UAT uses openrouter + deepseek/deepseek-v4.1-flash. Isolated account/home evidence; each capability/runtime success is separate and named. Missing CLI/platform/connector live evidence stays Unknown, never certified by mocks. |
| Founder landing | Founder approval remains required. This branch is documentation only and never lands into release/main by this architect. |
| This hand-off | Stop before grill-spec. Team-lead owns round 1; latest S-LEDGER::14:50 names Astra then Opus rounds, founder interview before each correction; a third only by founder's say within the ceiling. No grill/review file is authored or invoked here. |

**Code correct and tested:** not claimed; this task writes/validates a specification, not production or test implementations.

**Reachable by a user/agent:** required paths/proofs above are specified, not delivered; real joint candidate acceptance is pending.

## User-facing documentation

The destination pages were read before these TODOs were named. Implementing leads draft matching updates in the **same behavior/UI change**; docs-verifier audits against the actual candidate before landing. Coordinate overlap with S-FE rather than publishing contradictory navigation instructions. Internal spec/ADR changes alone do not discharge these TODOs.

| TODO | User-facing page | Specific matching update |
|---|---|---|
| DOC-001 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/agents.md` | Main/extra/existing worker chats; heartbeat main home; native self-delegation without self-edge, normal context/depth/memory and role prompt priority; permanent external self-helper exclusion, real CLI steering and subprocess/approval exception; remove in-chat handover and blanket worker-no-input language; owned deletion versus guest history. Add Admin default-main/pair addressing, shared main-only attention with structured questions, the saved-chat/heartbeat cutover, and canonical merged task tools with own/explicit workspace semantics. |
| DOC-002 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/using-omnipus-ui.md` | Row click/New chat/mention/navigation/search differences; final command and worker capability table; held release/discard; safe-point clear/display marker with retained search/recall; Interrupted and actual live-helper label; task activity/approval links; remove old uncorrelated-ack and old-command guidance without erasing current first-send recovery limits. Document main-only attention: pending asks/approvals versus shared unseen met/rounds_exhausted/other, stopped_by_user excluded, explicit foreground open versus reconnect, plus successful/failed saved-chat cutover and explicit @ workspace choice. |
| DOC-003 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/workspaces.md` | One computed main per eligible pair; immediate membership hide/unhide/no new work and running-work settlement; same team picker/standalone Admin; self/peer communication versus other-agent delegation edges, no transferred control. Document explicit workspace+agent targets including cross-workspace peers/tasks, default-only Admin main and shared pair-wide goal-seen behavior; tasks do not introduce cross-workspace plans. |
| DOC-004 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/calendar.md` | Automatic MAIN/CONTINUE/ISOLATED behavior, scheduled isolation checkbox, fresh MAIN child each run, current-run tree Stop not recurrence removal, captured starter/assignee recipients and full result/delivery limitation. Remove old recurring-format keep-firing/preservation promise; current one-time authoring and RRULE remain. Agent task creation/update/listing uses the merged optional-workspace family; no duplicate *_in_workspace interface or implicit outside-workspace task. |
| DOC-005 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/connectors.md` | Shared bound main context/control for now and accepted privacy limit; source/instance/chat/thread-specific addressed replies, unaddressed visible refusal and no destination fallback; no per-person privacy claim (#1206 later). |
| DOC-006 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/settings.md` | Actual wired ordinary defaults/units/sender scope, sole aggregate key and model-fit limit, boot/reload effect and visible refusals; report caps unchanged. UTC day/file-age retention, expired-window notice, model window versus retained searchable content, 30-minute repeated idle recap/joined and accepted restart waiting-input loss. Document the narrow idempotent saved-chat/heartbeat migration, visible failure/source retention, one-format steady state, and persisted shared attention mark; no general migration promise. #1221 owns the separate Auto switch deletion docs. |
| DOC-007 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/reference/built-in-tools.md` | Generator-owned page: regenerate via the existing docs-reference generator; do not hand-edit table text. Remove switch/handover entries and old guidance; reflect ordinary delegate/report/addressed messaging behavior, actual refusal/limits and native/external capabilities. Regenerate canonical merged task names/descriptions/optional workspace arguments and remove all four duplicate *_in_workspace entries, preserving historical saved evidence as inert history. |
| DOC-008 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/security.md` | MAIN task main-parent approval source can be more permissive than starter extra chat; target tool-policy/global ceiling remain; per-agent Auto off-switch is DELETE under separate #1221 and helper Auto follows its parent. Native helper attribution under one popup versus external exception; unattended Ask refusal, authenticated peer no authority transfer, UI-only double deletion confirmation. |
| DOC-009 | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/docs/tools.md` | Keep two-layer policy and unattended behavior accurate; native self/other-agent delegation boundaries; current report refusal/retry observability and ordinary sender limits; send_message direct return-address requirement, no guest-context/control inheritance, external policy limitation. Document create_task/update_task/list_tasks optional workspace selection, own workspace when omitted, existing permission/criteria/ownership limits and no cross-workspace plans; remove duplicate-family guidance. #1221 separately updates parent Auto inheritance/no per-agent off-switch. |

Module instructions that describe replaced mechanisms must be updated by their implementing lead with byte-identical AGENTS.md twins. S-ADR's dated older-ADR amendment map remains authority; do not retain its superseded compatibility promises as implementation requirements.

## Traceability Matrix

Every FR has scenario/test/doc coverage. Every BDD identifier and DOC TODO appears here; deletion rows inherit FR-038/039 and their explicit deletion trace table. Txx are the proposed families above; exact executed test names/logs are supplied by QA later. Holdout evaluation is deliberately excluded.

| Requirement | User Story | BDD scenarios | Proposed tests | Documentation TODOs / no-effect reason |
|---|---|---|---|---|
| FR-001 | US12 | BDD-12.3, BDD-12.4 | T14, T23, T30 | Internal generated-type plumbing has no separate user action; changed behavior documented by DOC-001–DOC-009 |
| FR-002 | US1 | BDD-01.1, BDD-01.2, BDD-01.4 | T01, T24 | DOC-001, DOC-002, DOC-003 |
| FR-003 | US1, US6 | BDD-01.3, BDD-06.4 | T17, T29 | DOC-001, DOC-003, DOC-004, DOC-005 |
| FR-004 | US2 | BDD-02.1 | T02 | DOC-006 |
| FR-005 | US2, US3 | BDD-02.1, BDD-02.2, BDD-03.1 | T02, T15 | DOC-002, DOC-006 |
| FR-006 | US2, US9, US11 | BDD-02.1, BDD-09.2, BDD-11.1 | T02, T22, T13 | DOC-002, DOC-006 |
| FR-007 | US2 | BDD-02.3 | T03 | DOC-006 |
| FR-008 | US2, US3, US8 | BDD-02.4, BDD-03.1, BDD-08.3 | T15, T18 | DOC-002, DOC-005, DOC-006 |
| FR-009 | US3 | BDD-03.1, BDD-03.2 | T15 | DOC-002, DOC-006 |
| FR-010 | US3 | BDD-03.3 | T04 | DOC-006, DOC-009 |
| FR-011 | US3 | BDD-03.4 | T05, T15 | DOC-006 |
| FR-012 | US4 | BDD-04.1, BDD-04.2, BDD-04.4 | T06, T16 | DOC-001, DOC-002, DOC-007 |
| FR-013 | US4, US10 | BDD-04.3, BDD-04.4, BDD-10.3 | T06, T16, T27 | DOC-002, DOC-007, DOC-009 |
| FR-014 | US5 | BDD-05.1, BDD-05.3 | T07 | DOC-001, DOC-003, DOC-007, DOC-009 |
| FR-015 | US5 | BDD-05.1, BDD-05.2, BDD-05.4 | T07 | DOC-001, DOC-007, DOC-009 |
| FR-016 | US5 | BDD-05.3, BDD-05.5 | T07, T19, T31 | DOC-001, DOC-007 |
| FR-017 | US6 | BDD-06.1 | T08, T17 | DOC-001, DOC-004 |
| FR-018 | US6 | BDD-06.1, BDD-06.2 | T08, T17 | DOC-004, DOC-008 |
| FR-019 | US6 | BDD-06.3, BDD-06.4 | T17 | DOC-004 |
| FR-020 | US6 | BDD-06.3 | T17 | DOC-004, DOC-007 |
| FR-021 | US6, US10 | BDD-06.1, BDD-06.4, BDD-10.1 | T17, T27 | DOC-002, DOC-004 |
| FR-022 | US7 | BDD-07.1 | T09, T25 | DOC-001, DOC-002, DOC-004 |
| FR-023 | US7, US3 | BDD-07.2, BDD-03.2, BDD-05.6 | T09, T15, T19, T25 | DOC-002, DOC-007 |
| FR-024 | US7, US9 | BDD-07.3, BDD-09.4, BDD-04.2 | T09, T16, T22, T25 | DOC-002, DOC-006 |
| FR-025 | US7 | BDD-07.4 | T20 | DOC-002, DOC-006; #1217 independently owned |
| FR-026 | US8 | BDD-08.1, BDD-08.2 | T10, T18, T26 | DOC-001, DOC-003, DOC-005, DOC-009 |
| FR-027 | US8 | BDD-08.1 | T18, T26 | DOC-001, DOC-002 |
| FR-028 | US8 | BDD-08.3, BDD-08.4 | T10, T18, T26 | DOC-005, DOC-009 |
| FR-029 | US9 | BDD-09.1 | T11, T24 | DOC-001, DOC-002, DOC-003 |
| FR-030 | US9 | BDD-09.2 | T22, T24, T25 | DOC-002, DOC-006 |
| FR-031 | US9, US12 | BDD-09.1, BDD-09.3, BDD-12.1 | T11, T23, T28 | DOC-002 |
| FR-032 | US9 | BDD-09.4 | T22, T25 | DOC-001, DOC-002 |
| FR-033 | US10 | BDD-10.1, BDD-10.3, BDD-07.1 | T12, T27, T25 | DOC-002, DOC-004 |
| FR-034 | US10, US6 | BDD-10.2, BDD-06.2 | T17, T27 | DOC-001, DOC-002, DOC-008, DOC-009 |
| FR-035 | US1, US10 | BDD-01.4, BDD-10.3, BDD-10.4 | T01, T27, T28 | DOC-002 |
| FR-036 | US11 | BDD-11.1, BDD-11.4 | T13, T29 | DOC-001, DOC-006 |
| FR-037 | US11 | BDD-11.2, BDD-11.3 | T21, T29 | DOC-001, DOC-008 |
| FR-038 | US12, US9, US11 | BDD-12.1, BDD-12.3, BDD-09.3, BDD-11.4 | T11, T23, T14 | DOC-002, DOC-004, DOC-007; internal-only aliases/readers have no distinct new user action |
| FR-039 | US12, US5 | BDD-12.2, BDD-05.4 | T12, T23, T07 | DOC-002; safety/canonical plumbing remains unchanged user behavior |
| FR-040 | US2, US5, US12 | BDD-02.1, BDD-05.2, BDD-12.2 | T02, T07, T23 plus existing platform/build/race/footprint gates | No new runtime/security setting or platform; existing user promises remain; DOC-006 describes changed retention/intake behavior |
| FR-041 | US12 | BDD-12.4 | T30 | DOC-001–DOC-009 |
| FR-042 | US12 | BDD-12.4 | T30 | DOC-001–DOC-009 |
| FR-043 | US5 | BDD-05.5, BDD-05.6 | T19, T31 | DOC-001, DOC-007, DOC-008 |
| FR-044 | US14 | BDD-14.1, BDD-14.2, BDD-14.3, BDD-14.4 | T33 | DOC-001, DOC-002, DOC-006 |
| FR-045 | US8, US9 | BDD-08.1, BDD-08.2, BDD-09.1 | T18, T24, T26 | DOC-001, DOC-002, DOC-003, DOC-005 |
| FR-046 | US8, US12 | BDD-08.5, BDD-12.1, BDD-12.3 | T34, T23 | DOC-001, DOC-004, DOC-007, DOC-009 |
| FR-047 | US13 | BDD-13.1, BDD-13.2, BDD-13.3, BDD-13.4 | T32 | DOC-001, DOC-002, DOC-003, DOC-006 |

### DELETE requirement traceability

All inventory rows are independently enumerated in BDD-12.1/T23 and dataset D13; current-behavior preservation additionally uses BDD-12.2/T12/T23/D14. This table makes shared safety/current-branch coverage explicit instead of pretending absence is behavioral proof.

| DELETE rows | Requirement | Additional behavior scenarios / test families | Documentation |
|---|---|---|---|
| DEL-01 | FR-002/017/038 | BDD-01.1/01.2/06.1; T01/T08/T17/T24 | DOC-001/003/004 |
| DEL-02–04, DEL-22 | FR-009/023/038/039 | BDD-03.1/03.2/07.2/07.3; T09/T15/T25 | DOC-002/006 |
| DEL-05–06, DEL-F01–02 | FR-029/030/031/038 | BDD-09.1/09.2/09.3; T11/T22/T24/T28 | DOC-002 |
| DEL-07, DEL-21, DEL-F09–13 | FR-002/026/027/038/039 | BDD-01.4/08.1/08.2/12.2; T01/T18/T23/T26 | DOC-001/002/003/007 |
| DEL-08, DEL-F20 | FR-036/038 | BDD-11.1/11.4; T13/T29 | DOC-001/006 |
| DEL-09–12 | FR-004/005/006/007/038/039 | BDD-02.1/02.2/02.3/02.4/12.2; T02/T03/T15/T23 | DOC-006; internal migration/store aliases have no separate user action |
| DEL-13–15, DEL-17–18 | FR-018/022/023/025/038/039 | BDD-06.2/06.3/07.1/07.2/07.4; T09/T17/T20/T23 | DOC-001/002/004; signature/backfill internals no new action |
| DEL-16 | FR-001/038 | BDD-12.1/12.3; T14/T23 | DOC-002/007; current webchat remains WebSocket |
| DEL-19 | FR-017/038 | BDD-06.1/12.3; T08/T17/T23 | DOC-004 |
| DEL-20 | FR-043/038 | BDD-05.5/05.6; T19/T31 | DOC-001/007/008 |
| DEL-F03–08 | FR-002/029/038/039 | BDD-01.4/09.1/12.2/12.3; T01/T12/T23/T24 | DOC-001/002 |
| DEL-F14–19 | FR-001/038/039 | BDD-12.2/12.3; T12/T14/T23 | Internal aliases/callback signature only; unchanged current UI behavior |
| DEL-F21–25 | FR-008/029/038/039 | BDD-02.4/03.2/12.2; T12/T15/T23/T24 | DOC-002 |
| DEL-F26–29 | FR-033/035/038/039 | BDD-10.1/10.3/12.2; T12/T23/T27 | DOC-002 |
| DEL-F30–38 | FR-006/035/038/039 | BDD-12.2/12.3; T12/T14/T23 | DOC-002; retain legitimate current control/errors/non-stream/reconnect output |
| DEL-F39–43 | FR-038/039 | BDD-12.2/12.3; T12/T14/T23 | DOC-002; no new goal/plan selection behavior |
| DEL-23 | FR-046/038/039 | BDD-08.5/12.1/12.3; T34/T23 | DOC-001/004/007/009; one canonical task family, no hidden aliases |

## Evaluation Scenarios (Holdout)

These are post-implementation **evaluation outlines**, not developer test fixtures. The independent evaluator creates unseen actual inputs/nonces and chooses interleavings after implementation; do not share those concrete holdout values with implementers or add them to the TDD/traceability plan. Publicly reading these outlines does not make an ordinary scripted test a private holdout.

| Outline | Category | External action / independent outcome |
|---|---|---|
| HE1 | Happy Path | On a fresh isolated home, create an unfamiliar workspace/team and return to a main after heartbeat-off. The same protected conversation opens; deliberately created extra chat stays distinct. |
| HE2 | Happy Path | Start a task from an extra chat with a different creator and MAIN assignee; inspect real result/chat/activity links. Starter and assignee receive one truthful result; creator is not substituted. |
| HE3 | Happy Path | Address a teammate with evaluator-chosen material and a source-history-only decoy. Guest answer bears guest identity; receiver never sees whole originating history; owner stays the same. |
| HE4 | Error Path | Interleave a steer with redirect at an evaluator-chosen step in an opened real helper. Compare caller receipts, visible text, persisted history and final execution; no refused-but-delivered instruction or stale replacement Stop. |
| HE5 | Error Path | Attempt an over-limit ordinary message and a refused helper report while watching the sender and recipient separately. Refusals remain visible; no false successful send/no-loss report. |
| HE6 | Edge Case | Run across midnight/retention with evaluator-chosen history and a tool group, then clear/search/recall. Retained bytes and matching results survive, model mark stays valid, old retained history is recallable. |
| HE7 | Edge Case | Stop a main with real task/helper descendants and an independent monitored run, then trigger a future schedule and remove membership. Scope, held reports, future recurrence and hidden-main behavior match the decided rules without guessing new recipients. |

## Assumptions and Clarifications

### No new product assumptions

| Item | Recorded boundary |
|---|---|
| Founder confirmation | S-ADR/S-LEDGER/S-Q provide the confirmed interview/eight answers and binding 17:15/17:25 overrides; Q7=A/Q8=MERGE close the later choices at 17:45; no separate interview JSON is invented. |
| Frontend | S-FE is Proposed with its own remaining questions/review. Binding later founder cutover/Admin/addressing/waiting-source decisions are recorded here; no unrelated avatar/layout choice or frontend-only landing is assumed. |
| Reference/graph availability | Generic Go reference library was not found with positive control; GitNexus MCP is unavailable. Existing source patterns/caller sweeps used; no graph impact/cluster output fabricated. |
| Test and deployment state | This is a spec-only change. Production, contract edits/regeneration, RED/GREEN/CHECK, actual CLI/connector/platform tests and exact-commit UAT are future implementation/delivery work. |
| Greenfield / cutover exception | One install/upgrade migration preserves existing supported saved-chat history/binding and turns heartbeat into main, idempotently and visibly on failure. Outside that cutover, scoped runtime compatibility remains DELETE; no guessed identity or generic backfill. |
| New ambiguity | Stop and record the next OPEN-Qn in S-Q with the affected requirement held. Do not use this section as an assumption to answer it. |

### 2026-10-07 closed answers

| Question | Binding answer | Normative location |
|---|---|---|
| OPEN-Q1 | 15:55 A: 64 KiB body, 60/min trusted sender+target, keep 200, exactly one 1 MiB aggregate setting AND existing model fit; reports/estimator unchanged | C-LIMIT; FR-010/011/013; dataset A |
| OPEN-Q2 | 15:20 A: first retained complete group, agent expiry notice, empty same-ID when none | FR-007; BDD-02.3 |
| OPEN-Q3 | 15:10 B: unaddressed answer visibly refused; agent must address sender, no fallback | FR-028; BDD-08.4 |
| OPEN-Q4 | 15:55 A: KEEP once/at_ms; DELETE every/every_ms and recurring.cron_expr paths; internal heartbeat cron unchanged | C-TIMING; DEL-19; FR-017/038 |
| OPEN-Q5 | 15:20 A: hide immediately/no new work; authorized live work settles/no hidden-main wake; re-add reveals | FR-003; BDD-01.3 |
| OPEN-Q6 | 15:10 B: double confirmation UI only; API/tool single existing approval, one common cascade | FR-037; BDD-11.2/11.3 |
| Q7 (formerly OPEN-Q7) | 17:45 A: shared pair-wide bounded seen mark; met finished, rounds_exhausted+other failed; stopped_by_user excluded; opening never resolves asks/approvals | C-ATTENTION; FR-047; BDD-13.1–13.4 |
| Q8 (formerly OPEN-Q8) | 17:45 MERGE canonical task tools with optional explicit workspace, own workspace if absent, duplicate family DELETE; Operator Deny; tasks not cross-workspace plans | C-TASK-TOOLS; FR-046; DEL-23; BDD-08.5 |

All eight questions are closed by recorded founder answers, including 17:45 Q7=A with other included as failed and Q8=MERGE. No proposal-dependent product choice remains held. No new OPEN question is answered by this author. Any additional question discovered during grill or implementation follows the founder interview boundary. Current shared-producer preservation requirements are proof obligations, not permission to invent a new owner/goal selection rule.

skills: omnipus-shared-rules, plan-spec (repo-specific instructions and templates), gitnexus-exploring, ux-heuristics-review, omnipus-design-system, github-cli, jev-use:jev-use

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Requirements translate confirmed decisions, including all six closed answers | S-ADR::D1–D10/Questions answered; S-LEDGER::all October 6/7 entries; S-Q::All closed questions, 15:10/15:20/15:55 answers | Verified instruction, high confidence |
| Existing code/contract/UI surfaces are named instead of parallel systems | Exact E-MAIN–E-POLICY and C-MAIN–C-LIMIT source reads; controlled source/caller excerpts in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/build/session-core-evidence/continuation-20261007/` | Verified source presence; impact Inferred and target behavior untested |
| Intake config gap and report wake/cap coupling require the specified adaptation | `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/pkg/session/message_inbox.go::Append/classifyEnvelope`; complete production caller scan in `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/build/session-core-evidence/continuation-20261007/intake-callers.txt`: resolver/setter declarations only, with live child-cap wiring as positive control | Verified source, high confidence; no runtime fix/reproduction claimed |
| CLI current no-op Input and fresh Resume are not certified steering | E-CLI::Input/Resume read directly in all three drivers; they return nil/discard text or call fresh Run; BDD-05.5/05.6 and T19/T31 require actual native identity/live proof | Verified source, high confidence; actual future native resume Unknown |
| Frontend counterpart and all eight issue references are traced | `git show 554d21ffd:docs/internal/architecture/ADR-20261007-agent-first-navigation-and-agent-identity.md`, exit 0: Proposed, D5/D12; eight gh issue view JSON receipts, exit 0 each, in evidence directory | Verified reference/instruction; reported issues not reproduced here |
| User-doc destinations and catalog reuse were checked | DOC-001–DOC-009 pages read; `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-session-core-20261006/design-system/catalog.json`::entries; source generator-owned header of built-in reference | Verified source, high confidence; actual public-doc updates pending implementation |
| Document checks / coverage | Final structural/citation/traceability checks and mutation controls are required before hand-off; receipts recorded with the final documentation commit | Pending final document check, not a product test claim |
| **Self-check** | Recheck artifact/diff against confirmed scope, repo template, all FR/scenario/test/doc/deletion links, oracle independence, current-producer safeguards, absolute citations and no production/contracts/scratch changes. Hand-off stops before grill and asserts neither code correctness nor reachability delivered. | Pending final self-check; implementation untested |
