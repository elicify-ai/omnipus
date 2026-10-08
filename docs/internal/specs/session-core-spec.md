Repository root: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/
Status: Approved (founder, 2026-10-08)
ADR: [Session core with an agent address book: reuse one standing session, one archive and the existing execution paths](../architecture/ADR-20261006-session-core-with-an-agent-address-book.md)

# Feature Specification: Session core

Created: 2026-10-07. Corrected: 2026-10-08, final cut round. Size: Feature.

## Overview and scope

Each eligible agent has one workspace main; its row opens that conversation and row New chat creates an extra. Heartbeats use main, MAIN tasks use real children with task-owned outcomes, and @ requests guest answers without handover. This spec extends existing paths; the ADR's dated log holds founder authority and `CLAUDE.md` holds project constraints, delivery and test rules.

| Scope | Work |
|---|---|
| In scope | #1211 report refusal/retry visibility; #1214 steer/redirect race; #1216 live limit wiring. |
| Separate | #1212 snapshot, #1213 respond correlation, #1215 duplicate notice, #1217 recovery, #1206 per-person channels; #1221 implements parent Auto/no agent off-switch, with no switch-removal notice/log warning. |
| Excluded | #1198 waiting-input restart reconstruction/live descendant-stop retry; outside-payload events, new helper memory/result pipelines, parallel stores/queues/services and CLI server protocol. |

S-FE: **Agent-first navigation and agent identity**, `docs/internal/architecture/ADR-20261007-agent-first-navigation-and-agent-identity.md`, `work/adr-frontend-navigation-20261007` at `554d21ffd`, **Proposed** (other-branch Git object). Layout/identity belongs there; D5/D12 require joint sidebar/main-backend integration.

## Existing Codebase Context

E-keys name existing `file::symbol` seams; target requirements are not current implementation claims. Baseline: `c6837a42d`, true-merged by `250b72cae`. GitNexus was unavailable; direct source reads/caller sweeps ground this map.

| Key | Existing file::symbol | Current seam |
|---|---|---|
| E-MAIN | `pkg/session/unified.go::NewHeartbeatSession`, `GetOrCreateScheduledSession`; `pkg/gateway/rest_workspaces.go::prepareWorkspacePutMembers`; `pkg/gateway/rest_sessions.go::computeSessionProtected` | Standing heartbeat create/reuse and protection; identity/lifecycle mature. |
| E-STORE | `pkg/session/daypartition.go::TranscriptEntry`, `PartitionStore.AppendMessage`; `pkg/session/unified.go::UnifiedStore`; `pkg/memory/window.go::snapshotWindowLocked`, `CommitWindow`, `RollbackWindow` | Day append/shared archive/model window; current separate histories/full reads. |
| E-RETENTION | `pkg/session/retention_sweep.go::RetentionSweep` | File-age sweep/lock order and cursor repair. |
| E-QUEUE | `pkg/agent/steering.go::steeringQueue`, `pushItemScopeChecked`; `pkg/agent/session_worker.go::sessionWorker.processTurn`, `closeSteeringWhenDrained`; `pkg/agent/ordinary_execution_admission.go::prepareOrdinarySessionExecution` | Steering FIFO/session runner/settlement admission; competing paths to remove. |
| E-LIMIT | `pkg/config/session_messaging.go::EffectiveSteerBodyBytes`, `EffectiveSteerRatePerMinute`; `pkg/tools/delegate.go::SetSteerCaps`; `pkg/tools/delegate_followup.go::checkSteerCaps`; `pkg/agent/context_budget.go::contextBudget`, `requestTokens` | Current resolvers/caps/rate window/model estimator; production wiring needed. |
| E-INBOX | `pkg/session/message_inbox.go::MessageInboxStore.Append`, `classifyEnvelope`; `pkg/agent/steer_audience.go::SteerUpwardDeliverer.Deliver`, `wakeOwnerOrStore` | Durable append/ack/dedupe/wake; child edge and wake-cap coupling need adaptation. |
| E-STOP | `pkg/agent/stop_session.go::StopSession`, `StopDelegatedTree`; `pkg/agent/stop_redirect_root.go`; `pkg/agent/cancel.go::RequestCancel`, `CollectDescendantSessionIDs`; `pkg/agent/revive_inbound.go::reviveRecordForHumanTurn`, `lifecycleInFlightStopFence` | Canonical Stop/tree/redirect/revive with selected execution fences. |
| E-DELEGATE | `pkg/tools/delegate_run.go::launchAndDispatch`, `validateRequest`; `pkg/agent/loop_delegation.go::buildDelegationDenyChecker`, `evalUntargetedDelegation`; `pkg/agent/steer_launcher.go::SteerLauncher.Launch`, `launchSteered` | Ordinary launcher/self normalization/workspace gates. |
| E-APPROVAL | `pkg/agent/loop_policy.go::inheritSessionPermissions`; `pkg/agent/steer_launcher.go::inheritDelegatePermissions`; `src/components/agents/ToolApprovalModal.tsx::ToolApprovalModal`; `src/store/toolApproval.ts::ToolApprovalStore` | Parent grants/Auto and one acting approval modal/store. |
| E-TASK | `pkg/agent/task_executor.go::ExecuteTask`, `StartTaskNow`, `startTaskNowViaLauncher`, `activateTaskGoal`, `SpawnTriggeredRun`; `pkg/agent/task_executor_run.go::openRun`, `closeRun`; `pkg/agent/task_executor_judge.go::deliverTaskCompletionUpward` | Task executor/claim/run/goal/authoritative completion. |
| E-SCHEDULE | `pkg/gateway/schedules.go::scheduledRunner.pickSession`, `RunScheduled`; `pkg/agent/task_trigger.go::TaskTriggerScheduler.RunScheduled`, `triggerToCronSchedule`; `pkg/cron/service.go::SessionMode`, `AddJobFull` | Current modes/once-RRULE/cron dispatch; selection/parentage to mature. |
| E-COMMAND | `pkg/commands/builtin.go::BuiltinDefinitions`; `pkg/commands/cmd_clear.go::clearCommand`; `pkg/commands/executor.go::Executor.Execute`; `src/hooks/useSlashMenu.ts::runClientSlashCommand`, `selectMentionAgent` | Registry/dispatch/palette/mention; current clear starts new chat. |
| E-NAV | `src/components/chat/useSelectSession.ts::useSelectSession`; `src/store/session.ts::startNewSession`, `attachToSession`; `src/store/connection.ts::setConnected` | Attach/new/search/connect roster invalidation. |
| E-ACTIVITY | `src/hooks/useRunningActivity.ts::useRunningActivity`; `src/components/chat/ActivityPanel.tsx::partitionRunning`, `ActivityRow` | Current span-only Activity; task/run/usage projection needs extending. |
| E-RECAP | `pkg/agent/loop_idle.go::resetIdleTicker`; `pkg/agent/session_end.go::CloseSession`, `persistResponse`; `pkg/agent/memory.go::MemoryStore.WriteLastSession`; `pkg/tools/memory.go::RetrospectiveTool.Execute` | Idle/close/memory/joined retrospective. |
| E-CLI | `pkg/agent/runner/driver_claude.go::Input`, `Resume`; `pkg/agent/runner/driver_codex.go::Input`, `Resume`; `pkg/agent/runner/driver_opencode.go::Input`, `Resume`; `pkg/agent/external_dispatch.go::policyApproverConsent.RequestConsent` | Three current Input/Resume drivers; no-op/fresh baseline needs real resume. |
| E-DELETE | `pkg/gateway/rest_agents.go::deleteAgent`; `pkg/sysagent/tools/agent.go::cascadeDeleteAgentSessions`, `cascadeUnassignAgentTasks` | Existing REST/tool cascades; baseline entity-first must reverse. |
| E-POLICY | `pkg/config/defaults.go::defaultToolPoliciesGeneral`; `pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata`; `pkg/agent/loop_wire.go::registerDelegationTools` | Catalog/global defaults/runtime registrations and canonical effective policy. |
| E-ADDRESS | `pkg/config/config.go::ChannelInstanceConfig`, `ChannelIdentity`; `pkg/config/config_channels_instance.go::ChannelInstanceConfig.IsWorkspaceBound`; `pkg/gateway/channel_ownership.go::channelOwnershipResolver.OwnerOf`, `OwnedBy`; `pkg/agent/loop_inbound.go::resolveMessageRoute`, `resolveWorkspaceIDForContinuation` | Configured connector pair ownership/inbound routing. |
| E-TASK-TARGET | `pkg/sysagent/tools/task.go::TaskCreateTool.Execute`, `taskCreateToolExecute.resolveWorkspace`, `enforceDelegation`; `pkg/agent/loop_delegation.go::NewSysagentDelegationDeny`; `pkg/tools/task.go::TaskCreateTool.resolveWorkspaceID`, `taskCreateToolExecute.buildTask`; `pkg/coreagent/role_policies_adr090.go::ADR090RolePolicyInventory`, `adr090SparseRolePolicies` | Ordinary and duplicate explicit-workspace validators/callers/role policies. |
| E-ATTENTION | `pkg/gateway/ws_tool_approval.go::sessionStateBytes`; `pkg/gateway/ws_ask_user.go::broadcastAskUserCard`; `pkg/agent/goal_outcome.go::recordGoalOutcome`; `pkg/gateway/goal_outcome_frame.go::goalOutcomeFrame`; `pkg/gateway/websocket_replay.go::handleAttachSession`; `pkg/notifications/store.go::MarkRead` | Pending snapshots/outcome/attach sources; shared seen writer needed. |
| E-PROMPT | `pkg/coreagent/prompts_adr090.go::adr090Prompts`, `exactToolDiscoveryRule`; existing structured question tool/catalog and E-ATTENTION | Current role prompts/structured-question tool rule, prompt-engineer owned. |
| E-MESSAGE | `pkg/tools/message.go::MessageTool.Parameters`, `Execute`, `SendOrigin`; `pkg/agent/loop_wire.go::registerCoreTools`; `pkg/channels/outbound_ownership.go::allowAgentOriginatedSend`; `pkg/channels/manager.go::sendWithRetry`; E-ADDRESS | Tool→outbound bus/worker; publish is not delivery, egress binding needs validation. |
| E-GOAL | `pkg/agent/turn.go::ActiveTurnInfo`, `GetActiveTurnBySession`; `pkg/agent/turn_stream.go::stampStreamerTurnID`; `pkg/agent/goal_loop.go::emitGoalStatusFrameWithCriteriaAndDoD`; `pkg/agent/goal_triggers.go::dispatchGoalAsyncFollowUp`; `src/lib/goalSetupState.ts::isGoalRecordEmpty`; `src/components/chat/ChatScreen.tsx::InlineThinkingIndicator`, `FallbackToolUI`, `VirtualAssistantMessageRow` | Turn/stream/goal producers plus keyed UI; universal run association needs wiring. |

### Data flow

```text
web / connector / authorized parent / addressed peer
    -> trusted intake -> one session FIFO + runner -> safe boundary / next turn
helper / task outcome -> existing report inbox -> same wake / runner boundary
schedule -> existing cron/task executor -> derived mode -> task-owned result
Stop / redirect -> selected-execution fence -> discard undelivered human input / continue
correlated guest reply -> source owner's return adapter -> original source, guest author
```

## Contract Changes

Backend-lead alone edits/regenerates existing OpenAPI/AsyncAPI/shared schemas before consumers; generated boundary types/runtime validation only (`CLAUDE.md`, Constraint 8).

| Key | Schema / source | Shape |
|---|---|---|
| C-MAIN | `contracts/components/schemas/Session.yaml`, `contracts/components/schemas/WorkspaceMemberConfig.yaml`, `contracts/components/schemas/WorkspaceMemberHeartbeat.yaml`; E-MAIN | Current Session.type required; server-only `main` replaces heartbeat, absent from client-create enum. Required immutable agent_id/workspace_id/computed pair id; computed protected. Eligible member main_session_id: readOnly string; absent for workers/other systems. Delete heartbeat.session_id/active_agent_id. Computed-ID bounds derive from existing workspace/agent bounds across REST/WS; Admin uses Session list/detail, no fake membership. |
| C-ARCHIVE | `contracts/components/schemas/Message.yaml`; E-STORE | One entry: view_membership=`chat`/`model`/`both`, full existing model representation, trusted source and message/turn/tool/result identity. File/byte/view marks remain disk-only. REST/replay project this archive. No per-entry author timestamp or deletion-only agent_removed field. |
| C-INPUT | `contracts/components/schemas/MessageFrame.yaml`, `contracts/components/schemas/MessageStatusFrame.yaml`; Message/replay in `contracts/asyncapi.yaml` | Current ordinary first-send/queue correlation required. MessageStatusFrame.state adds `discarded`, reason=`stopped_before_delivery`. Message/REST/replay retain read-only input_disposition: original message_id:string, optional client_message_id, state=`discarded`, reason=`stopped_before_delivery`. Server appends the same chat-only system effect referencing original input, never rewrites bytes. No client release/discard action. |
| C-INBOX | `contracts/openapi.yaml`::SessionMessage; `contracts/components/schemas/SessionMessageSteer.yaml`, `contracts/components/schemas/SessionMessageRespond.yaml`, `contracts/components/schemas/SessionMessageQuestion.yaml` | Existing message_id/sender_identity/correlation_id/generation and real-child parentage. Authenticated peer/task classification uses common append/ack/dedupe/wake, never fake parent IDs. Typed refusal/retry hint and parent rejected-arrival observation use current result/status surfaces. |
| C-TASK | `contracts/components/schemas/TaskRun.yaml`, `contracts/components/schemas/TaskCreateRequest.yaml`, `contracts/components/schemas/TaskUpdateRequest.yaml`, `contracts/components/schemas/ScheduleCreate.yaml`, `contracts/components/schemas/ScheduleUpdate.yaml`; E-TASK/E-SCHEDULE | Run recipients captured at start: session-ID array. Scheduled run_isolated:boolean optional, default false; no mode chooser. Existing run→session link identifies CONTINUE segment. Worker/workspace/external-runtime compatibility capture is private; prior links/native ID remain. No new Task stopped status/segment service. |
| C-TIMING | `contracts/components/schemas/TaskTrigger.yaml` | Keep manual, once/config.at_ms and recurring RRULE/dtstart_ms/tz. Remove every/every_ms/recurring.cron_expr; heartbeat's internal cron shape stays. |
| C-CONTROL | `contracts/asyncapi.yaml`::RedirectFrame/SessionStateFrame; `contracts/components/schemas/SlashCommand.yaml` | Existing selected-execution receipts: queued/applied/superseded/refused. Session-aware command result/capabilities, including clear refusal. Remove session_close/ack, agent_switched/retired aliases; keep session_mode_update. |
| C-LIMIT | E-LIMIT / existing config contracts | Existing steer_body/steer_rate types and zero/effective meanings unchanged; defaults 65,536 UTF-8 bytes and 60/minute/sender+target. Sole new setting session_messaging.steer_aggregate_body: 1,048,576 bytes; ordinary item count 200. Existing model estimator/fit unchanged. |

### C-ADDRESS / C-REPLY

| Field / operation | Shape / validation |
|---|---|
| Recipient | `{workspace_id, agent_id}` pair in current message tool/intake/MessageFrame alongside owning chat/session; UI defaults current workspace, foreign explicit. Invalid pair refuses. |
| Trusted model envelope | request_id=admitted message_id, authenticated sender, source owner pair and return correlation per input. Private connector/credential route stays server-owned; receiver gets request/material only. |
| Reply selector | Existing send_message adds optional reply_to:string. Reply form requires content+reply_to and excludes supplied channel/chat/recipient destination; direct request uses recipient pair, ordinary owner sends keep their checks. |
| Binding | Server resolves request in authenticated responding session/agent; match recipient/source session/correlation/captured instance/chat/thread/owner. Nonexistent, wrong-session, forged, discarded or unreadable selection refuses. |
| Return authorization | Existing source-owner bus/worker returns guest-authored reply. Server-only validated capture separate from author; current binding checked at final send/retry/placeholder-edit boundary. OwnershipChecked alone/system-origin exemption is insufficient; rebound/deleted/unreadable/unauthorized capture refuses, no replacement. |
| Receipt / history | Accepted-for-send differs from actual delivery/error; PublishOutbound success is not delivery. Existing failure observation, persisted request/reply IDs and guest Message.agent_id live/replay; no new receipt/retry service or successful no-op. |

### C-ATTENTION

| Field / operation | Shape / ownership |
|---|---|
| Session.needs_attention | Boolean readOnly; present true/false on valid mains including default Admin, omitted on non-main. FR-047 owns source/mapping; read failure is unknown/error/stale, never false. |
| Goal-seen bound | One bounded shared mark in existing main metadata from saved outcome/entry order; disk-owned, no unbounded set/per-user notification. |
| AttachSessionFrame.ack_attention | Optional boolean, default false; authorized successful foreground open captures observed bound. Failed/background/reconnect attach cannot write seen; no later outcome or ask/approval acknowledged. |
| Live/reconnect | Existing pending snapshots/question/approval/goal frames and Session-list projection for unopened mains. No event family/attention service. |

### C-TASK-TOOLS / C-FOREIGN-TASK

| Surface | Shape |
|---|---|
| Canonical family | create_task/update_task/list_tasks optional workspace_id; absent=own current workspace. Null/empty/invalid/inaccessible explicit choice refuses; no fallback. Existing delete_task approval/ownership stays. Remove all four *_in_workspace callables and their active references. |
| Policy | Shipped canonical defaults and current canonical-name policy only. Old-name saved values in both layers are inert/ignored, not rewritten/converted/merged; existing unknown-tool warnings acceptable. Tests inspect effective access, not removed config keys. |
| Foreign create result | Existing result: task_id, status=`inbox`, resolved workspace_id/agent_id, created/not-started guidance; no run_id/session_id/started claim. |
| Foreign status result | Read-only task identity/status/resolved target pair, scoped by destination plus private CreatedByAgentID/CreatedByAgent. No unrelated content/result/plan/private creator field or human/operator read bypass. FR-048/049 own execution/ownership rules. |

### C-DELETE

Existing deleteAgent/agent-delete tool, E-DELETE, `contracts/components/schemas/ConfigurationMutationState.yaml`: validate current identity/revision/authority/guards before writes; clean owned chats/memory and required cascade parts first, including applicable owned SOUL bytes, then remove the agent record last. If any part fails, the record remains visible and current error/mutation result says **partly deleted**; the same Delete action retries idempotently with current revision, including after restart. No separate scope record, folder-isolation system, pending list, cleanup_only query, AgentDeletionCleanup/deletion_cleanup result or per-entry timestamp machinery. Existing list response remains unchanged; FR-037 defines behaviour.

### C-GOAL

Sources: `contracts/components/schemas/SessionStateActiveTurn.yaml`, `contracts/components/schemas/TokenFrame.yaml`, `contracts/components/schemas/ErrorFrame.yaml`, Message and current replay/done/snapshot carriers; E-GOAL.

| Producer / consumer | Shape / association |
|---|---|
| Task/goal/turn | Capture actual dispatch/activation goal_id into producing run/turn; ordinary work without proven goal is unknown. Validated current-turn activation associates subsequent work, not earlier messages. |
| Live/history/replay | Optional goal_id beside producing turn_id/message identity; run-scoped errors gain missing turn correlation. Stream, non-stream, tool/error, snapshot, persisted entry and replay retain same association; later frames cannot rebind old message. |
| UI | Exact goalPills[goal_id] join with merged criteria. Matching empty active goal may show setup; different/nonempty/unknown uses ordinary/neutral state. FR-039 forbids scalar/latest/_default choice. |

## Functional Requirements

Normative rules below; E/C keys name existing seams and shapes. Merged/retired identifiers resolve through the final ID map.

| ID | Rule |
|---|---|
| FR-002 | Eligible native MAIN membership MUST eagerly create/reuse one `main-session-<workspaceid>-<agentid>` with immutable agent_id/workspace, pinning/protection independent of heartbeat. Admin has default-workspace main only, unchanged standalone/no-team/fixed-tool role; workers/other system agents have none. Validate stored pair/owner, eligibility/access; refuse corrupt/mismatched/unauthorized identity without guessing. |
| FR-003 | Team removal MUST immediately hide main from UI/channel/task addresses and refuse new membership-based work. Authorized live runs settle/retain outcomes without hidden-main wake; same pair re-add reveals retained identity until normal retention. |
| FR-004 | Session MUST use one authoritative append-only content entry format for chat/model/both and full model representation, no duplicate model-content store. |
| FR-005 | UTC day files/file-byte marks MUST give bounded model reads across days, preserve accepted FIFO despite delayed timestamps and complete tool groups, and avoid full-archive reads per append/step. Do not keep every main archive/model runner permanently hot. |
| FR-006 | Clear/correction/rollback/projection MUST append effects or move view/window metadata, never rewrite retained bytes. Preserve provider-valid call/results, pending controls, recall/full result references; no new summary writer. |
| FR-007 | Retention MUST keep 90-day file-modification-age/default/disabled semantics and expire old main content despite protected identity. Expired mark advances to first retained complete group with agent expiry notice, or empty same-ID window when no complete content remains. |
| FR-008 | Acceptance MUST save message and trusted provenance together or neither; failed save/admission cannot claim saved echo/receipt/consumption. Transcript save, intake acceptance and model consumption are distinct. |
| FR-009 | One session FIFO/existing runner MUST own ordinary intake through settlement on every entry path. All ready inputs enter at safe boundaries together as separate ordered messages; natural completion immediately starts next ready turn. Delete competing worker/manual queues, waiting room and bypass dispatch; retain execution/generation/boot/message/source fences. |
| FR-010 | Ordinary admission MUST apply 65,536 UTF-8 bytes/item, 60/minute/trusted sender+target, 200 waiting items, 1,048,576 aggregate bytes AND existing model fit before acceptance. Count media/tool/pinned costs through unchanged estimator; CLI exemption is not a fit guarantee. Refuse visibly, no truncation of accepted text. |
| FR-011 | Existing steer_body/steer_rate and sole aggregate setting MUST govern actual common intake at boot/reload, not just saved/displayed config (#1216); report policies unchanged. |
| FR-012 | All accepted helper report kinds MUST use existing inbox/dedupe/ack/consumption, wake idle non-stopped parent, join live safe boundary or remain in stopped parent's inbox without revival. Expanded wake eligibility cannot widen preserved rate/count exemptions. |
| FR-013 | Refused report arrival MUST give child typed refusal/applicable retry hint and parent rejected/not-delivered observation (#1211). Preserve report body/rate/unacked/type ceilings, exemptions and result delivery; no auto retry/overflow store/service. |
| FR-014 | Omitted target MUST normalize to caller before common authorization. Eligible native MAIN/WORKER self-delegation needs no self-edge; operator Deny wins. |
| FR-015 | Self-helpers MUST use ordinary context/memory/requested-skill/receiver/depth/admission rules and nesting default depth 3; other-agent workspace delegation gates remain. Main/extra chats retain role tools and prioritize delegation by prompt, not ban. No self-only hop/quota/spend/memory pipeline or #1212 side fix. |
| FR-016 | External CLI workers MUST never create Omnipus helpers; Admin/system tool/runtime restrictions stay. Harness-internal work is not tracked delegation. |
| FR-017 | Existing task/schedule dispatch MUST derive MAIN fresh real assignee-main child per run (MAIN beats CONTINUE), worker one-time ISOLATED, recurring worker CONTINUE; scheduled isolation checkbox forces fresh independent chat for either role. Heartbeat uses main itself. Worker/workspace/external-runtime change starts next-run segment; preserve series/prior links/old owner/native identity and active-run capture. No mode chooser/handover. Keep once/at_ms and RRULE, delete task every/cron_expr compatibility, not heartbeat engine. |
| FR-018 | Authorized future scheduled MAIN launch under stopped main MUST be task-origin-only, ordered against existing Stop/cascade fence, without main-model revival. Task retains claim/goal/attempts/limits/plan/outcome; ordinary extra-chat helper parentage and general stopped-parent guard stay. |
| FR-019 | Run recipients MUST be captured at actual start: starter MAIN's main (not creator) plus MAIN assignee's main, deduped. Missing/hidden capture leaves result in task/run view; no substitute. |
| FR-020 | Actual done/failed/stopped runs MUST notify authoritative reason/full stored result plus brief engine header; no skipped/per-retry final notice or start-time model injection. Eligible idle wake once/batch, live safe consumption, stopped report retention. ISOLATED/CONTINUE remain independent despite monitoring. Existing full-result envelope/context limitations remain visible; no defer/pagination/summary/retry pipeline. |
| FR-022 | Existing Stop MUST act on current selected turn first; second within three seconds or /cancel stops its real downward helper/MAIN-child tree, not independent monitored runs/future recurrence/parent/sibling. Native plain Stop retains background shells; stopped-main live-helper label counts actual current activity. |
| FR-023 | Root/helper stop-redirect and steer race MUST retain same chat, existing control precedence and selected execution. Queued/applied/superseded/refused are truthful/curated; refused text never delivered, late force/detach/completion/redirect cannot touch replacement (#1214). Navigation/disconnect never stops work. |
| FR-024 | Stop MUST discard webchat/channel input not yet committed at the safe boundary into selected agent/model input, not merely bus-admitted. Order discard versus consumption under existing fence. Show discarded/not delivered; preserve archived bytes/already-consumed input. Helper reports alone wait for next legitimate active turn, never revive; superseded parent controls never return. No held-state/release/discard actions. |
| FR-025 | Restart-cut conversation MUST show Interrupted, not permanently Working/failed solely from restart; zero boot replay of old queued input. Preserve task interrupted failure/plan recovery; integrate I1/#1217 next-message continuation without competing recovery. |
| FR-027 | Guest request/reply MUST preserve author and admitted request/return IDs live/replay; model selects reply_to and source owner's existing adapter returns it after current egress binding check. Forged/foreign/discarded/rebound selection refuses; no guest connector grant. Owner sees request/answer, idle eligible wake once, stopped retention; silence creates no empty bubble. |
| FR-028 | Mixed-source replies MUST address each original sender/instance/chat/thread/session/correlation. Missing usable correlation refuses visibly, no fallback/broadcast. Connectors share their bound pair's main context/control for now (#1206 later). |
| FR-029 | Row click/switch-agent MUST navigate eligible main; only agent-row New chat creates extra MAIN chat in UI. Preserve current team picker/standalone Admin and existing connect/reconnect roster refresh. No in-chat handover/latest/fresh fallback. |
| FR-030 | /clear MUST be main/extra-only; ALL helper/subagent/delegate sessions, native/external and MAIN task children, refuse with explanation. Allowed clear waits for safe point, changes context/display with marker, preserves session/pending input/archive/search/recall; never says in-flight input forgotten. |
| FR-031 | Command table MUST agree at palette and typed/server execution, including stale-menu direct calls. Delete retired aliases; read-only lists cannot select model/mutate config. |
| FR-032 | Authorized human input in existing worker/task chat MUST steer live run or continue finished conversation without rewriting/rerunning completed task. Explicit Rerun separate; no fresh worker chat/@ target. |
| FR-033 | Existing Activity MUST show relevant agent task/scheduler runs even without current-chat spawn, real parent/starter/assignee scope, one MAIN row, running/queued/waiting/actual available tokens and correct Open link. Unknown usage not zero; no extra dashboard. |
| FR-034 | MAIN child MUST inherit main-parent grants/Auto, not starter extra chat, within target policy/authority and #1221 dependency. Disclose possible loosening; one native approval ID/modal names acting helper/run. Unattended human-required Ask refuses immediately; external harness not native-popup-protected. |
| FR-035 | Existing UI MUST satisfy the normative UI states, command, accessibility and catalog tables below; no new visual-design decisions. |
| FR-036 | Default-on recap MUST use 30-minute actual inactivity including main, reset by any turn/message, never overlap execution/settlement; repeated idle episodes and same-ID resume remain. Keep idle/bootstrap/joined and ordinary helper memory; delete lazy/explicit/session_close/ack. |
| FR-037 | Same authorized delete cascade MUST preserve default/locked/system/active-plan guards; delete owned chats/memory/required cleanup first, record last. Failure leaves visible agent with honest partly-deleted result; same Delete idempotently retries, also after restart. Retain guest history labelled deleted agent after completed deletion; UI warning/two confirmations only, API/tool single existing approval. No separate cleanup record/list/filter/result/timestamp machinery. |
| FR-038 | Every DELETE inventory ID MUST be removed with canonical replacement/nothing, including active registrations/contracts/aliases/policies/defaults/prompts/callers/compatibility-only tests/docs. Preserve current shared-branch output/safety; finish caller sweep/canonical producer before removal, no owner/producer guessing or side fixes. K/B proof matches what can fail. Greenfield: no importer/conversion/upgrade backfill/dual reader. |
| FR-039 | Thinking/error indicators MUST join their own producing run/turn/message goal_id to exact keyed merged criteria; unknown neutral. No latest scalar/arbitrary map/_default selection or old-message rebinding by later goal/delayed frame. |
| FR-043 | Three current CLI drivers MUST deliver live instruction by interrupt plus actual native-conversation resume with runtime/workspace/model/caps preserved. Missing native ID/driver/auth or failed/superseded delivery visibly refuses; no no-op/fabricated ID/fresh fallback. Disclose Stop subprocess-kill exception. |
| FR-045 | Every @/peer/cross-workspace target MUST resolve exact workspace_id+agent_id to computed eligible main: current workspace default, foreign explicit, Admin default-only, workers/other systems excluded. Eligible MAIN peers can message without delegation edge, including foreign pair, under receiver permissions; no source-history copy or transferred Stop/context/grants/ancestor authority/bare ID/fallback. |
| FR-046 | Merge task families into canonical create/update/list optional workspace (absent=own); consolidate delete callers into delete_task. Remove duplicate callables/active callers/catalog/default/role/prompt references. Shipped canonical defaults/current canonical policies and two-layer Deny apply; saved retired settings are inert/ignored, no rewrite/conversion/strict merge. Preserve validators/store/audit/criteria/assignment/owner/plan/run and FR-048/049; no cross-workspace plan/new executor. |
| FR-047 | Main-only read-only needs_attention MUST reflect pending structured ask/approval or unseen met (finished), rounds_exhausted/other (failed), not stopped_by_user/other chats/generic activity/task/plan. Shared bounded metadata seen mark: successful foreground open acknowledges observed goals for all, never pending sources/later race. Existing unopened-main live/query/reconnect paths authoritative; failure unknown/error, not false. User-answer prompt uses structured tool, owned by prompt engineer. |
| FR-048 | Explicit foreign create MUST deliver assigned task to existing target team board Inbox, return created/not-started identity/pair, no execution status/claim/activated goal/dispatch/armed trigger/run/session/mode. Foreign run/start/re-run/assign-for-execution refuses before writes, including wrappers/source-policy bypass. Receiver chooses local pickup under destination policy/normal ownership; optional peer message does not admit execution. Creator not automatically starter/parent/recipient. |
| FR-049 | Foreign creator MAY read only status of tasks it created in explicit destination via CreatedByAgentID/CreatedByAgent+workspace, including direct-ID/alternate-role reads. Empty/unrelated/human-colliding attribution refuses; no private creator field, unrelated disclosure, mutation/execution or operator read bypass. |

## UI Screens, States and User Journey

S-FE owns visual layout/identity. Reuse these existing surfaces; loading/empty/error/partial/success and recovery remain explicit.

| Surface | Loading / empty | Error / partial | Success / recovery |
|---|---|---|---|
| Agent navigation/attach/search | Loading; empty protected main valid | Stale roster/failed attach visible; no guessed replacement | Main/extra/search distinct; current Retry (FR-002/029) |
| Main attention | Authoritative load, no guessed false | Source unknown/stale/error; pending vs seen distinct | Shared foreground seen/resolve state (FR-047) |
| Composer/mention/palette | Discovery gate; empty matching choices | Admission/clear refusal; discarded input observation, no recovery buttons | Guest identified; main/extra clear marker, typed parity |
| Thinking/goal-setup error | Neutral unknown association | Matching run error only | Own-run keyed criteria/state (FR-039) |
| Activity | Loading/empty current work | Lifecycle/usage unknown; rejected arrival visible | One actual run/helper, correct Open/count/usage |
| Approval | Current pending/resolved, no request=no card | Expired/refused; external exception explicit | One ID/acting run label/link |
| Calendar/run details | Existing editor/occurrence states | Timing/dispatch/delivery failure; stored result still accessible | Current timing/isolation, run/segment links |
| Agents/profile Delete | Existing delete pending; row remains on partial failure | Honest partly deleted result; no cleanup sublist | Same Delete retries visible agent before/after restart |

Journey: open main → request/@guest → Activity/real run/approval → Stop/redirect and truthful discard → main/extra clear/search/recall → same-ID return or membership hide/Delete. Cases reside in the corresponding BDD rows.

### Command capabilities

| Main/extra vs helper session action | Capability |
|---|---|
| Help/status/stop/cancel/stop-redirect/sessions/workspace/tasks/recall/navigation; existing read-only model/skill/channel lists | Allowed within current authorization |
| clear | Main/extra only; every helper/native/external/task child refuses |
| remember/retrospective/goal/loop/model switching/config mutation | Refused in helper/worker context |
| new/agents/list/show/switch/check/channel/start/old resume | Deleted everywhere, no alias |

### Accessibility and design-system reuse

Names, readable guest author, focus/restoration and status/error announcements apply individually to New chat, isolation, commands/mention, Stop, Open, approval and Delete confirmations. Escape dismisses an open menu/dialog before chat Stop; never accidental confirmation. No color-only states; preserve central focus, 24×24 pointer/44×44 touch targets, 200% zoom/320px reflow (`docs/internal/design/design-system-definition.md`::D7/D16/D17).

Use `design-system/catalog.json`: Button/IconButton actions/links, Checkbox isolation, ConfirmDialog deletion, Command palette, Skeleton/EmptyState and existing Tooltip/status/identity renderers. No new primitive/dashboard. Frontend-lead applies the design-system skill; no browser certification is claimed here.

## Security and accepted limitations

| Disclosure | Boundary |
|---|---|
| Shared channel context | Correct sender egress is not per-person history/control privacy (#1206). |
| MAIN approval | Main-parent Auto/grants may be looser than starter extra chat; target policy/unattended refusal still apply. |
| Stop discard | Undelivered web/channel input discarded; consumed input/side effects not undone. |
| CLI Stop | May kill subprocesses; external harness is not governed by native popup. |
| Restart / delivery limits | No waiting-input durability (#1198). Existing full-result envelope/context limits remain; no universal delivery-fit promise. |

## User Stories and Acceptance Criteria

All P1; acceptance/test links live in Traceability, not a second rule copy.

| ID | User outcome |
|---|---|
| US1 | Reliable protected workspace main. |
| US2 | Retained history and bounded model window. |
| US3 | Ordered intake and honest limits. |
| US4 | Helper reports without silent loss. |
| US5 | Ordinary native helpers and honest CLI steering. |
| US6 | Task-owned runs/results and compatible continuation. |
| US7 | Truthful Stop/redirect/interruption. |
| US8 | Addressed guest replies and foreign board delivery. |
| US9 | Distinct navigation/clear and worker follow-up. |
| US10 | Truthful activity/approval and accessible controls. |
| US11 | Idle recap versus deliberate retryable deletion. |
| US12 | Canonical code/current output and exact run attribution. |
| US13 | Main attention with explicit sources/read state. |

## BDD Scenarios

Tags: H=happy, A=alternate, E=error, X=edge. Each row defines fixture/action and independent expected observations; cases expand the row. Compare real saved/consumed/outbound/UI identities, not the candidate's own selection helper. Proposed tests are not execution receipts.

| ID / acceptance | Tag | Given / action and cases | Then / oracle |
|---|---|---|---|
| BDD-01.1 / US1.1 | H | Concurrent eligible-main lookups: Mia in W1/W2 with heartbeat off; default/non-default workspace, longest valid pair IDs; Admin default-only positive variant. | Exactly main-session-W1-mia/main-session-W2-mia or default Admin computed ID, one stored identity per authorized pair, immutable owner/workspace. Other pair and existing extra unchanged. Count persisted identities, not calls. |
| BDD-01.2 / US1.2 | H | Disable enabled heartbeat on pinned/protected Mia main. | Same main protected/navigable, no replacement or stored heartbeat address. |
| BDD-01.3 / US1.3 | A | Remove member Mia while authorized child runs with captured completion recipient; later re-add same pair. | Main hidden from UI/connector/task addressing; new membership-based work refused. Child settles/retains result without hidden-main wake. Re-add reveals same retained ID, no replacement. |
| BDD-01.4 / US1.4 | E | Main lookup/attach cases: stored ID wrong owner/workspace; unreadable/corrupt metadata; no workspace access; worker/other-system or Admin outside default. | Visible refusal/error, zero guessed/replacement identity, wrong-owner attach or other-workspace dispatch. |
| BDD-02.1 / US2.1 | H | Archive A/B/C, complete tool group and saved mark; admitted message slides window. | One archive/two views, prior prefixes byte-identical, A recallable, valid groups. Actual file-read offsets begin at mark: window-sized return alone is insufficient. |
| BDD-02.2 / US2.2 | X | Call at 23:59 UTC/result after midnight; ready M1/M2 accepted in order with older source timestamps. | Complete matching group and separate M1/M2 in accepted FIFO; legal day/byte refs, no old-file back-insertion/new execution/late-event archive. Dataset B04 adds repeated tool ID. |
| BDD-02.3 / US2.3 | X | Sweep expires marked day. Remaining: fragment then complete group / fragments only or none. Also empty protected main, disabled retention and younger/equal/older mtime fixtures B01/B05/B06. | First complete retained group onward, no orphan calls/results; or empty same-ID window. Agent expiry notice; identity/protection retained. Disabled means no deletion/manufactured repair, strict older-than cutoff only. |
| BDD-02.4 / US2.4 | E | Inject failure of owner/identity write, provenance save, content append or admission publication while submitting valid message. | No saved/accepted receipt, saved-looking echo or model-consumption success; authenticated sender gets real failure. Empty/null schema cases A02 separate. |
| BDD-03.1 / US3.1 | H | Block tool/model step; accept human, connector and authorized-parent inputs in that order; reach next safe boundary. | All three distinct messages/identities in next model input and chat, same order; zero competing runner/queue dispatch. A01 minimal valid byte and trusted sender. |
| BDD-03.2 / US3.2 | X | New accepted input lands during final settlement; separate same-agent chat also active (A12). | After settlement one immediate next turn receives all ready input; no timer/previous-reply refusal or cross-chat queue/control contamination. Assert dispatch/input identity at forced boundary, not latency. |
| BDD-03.3 / US3.3 | E | Isolate each bound: body 65,535/65,536/65,537 UTF-8 bytes; prior minute sends 0/59/60 by sender+target; different sender first send; waiting 199/200; aggregate 1,048,575/1,048,576/1,048,577 bytes; assembled model estimate B−1/B/B+1 after allowed old-window slide. A03/A05/A07/A11 add media/Unicode/identity cost. | Body/aggregate/model under-or-at bound accepted, +1 refused; first/60th admitted, 61st refused for same trusted sender+target; other sender independent if target bounds fit. Candidate 200 accepted, 201 refused. Bound-specific visible error before admission, no accepted-text shortening. |
| BDD-03.4 / US3.4 | H | Save distinct valid steer_body/steer_rate/aggregate values; reload running config and boot fresh fixture; exercise web/channel/parent/peer adapters. | Actual admission boundaries change under applied config on all ordinary paths; fresh boot same values. Report caps/delivery unchanged. |
| BDD-04.1 / US4.1 | H | Idle authorized non-stopped parent, report below preserved caps. Kinds: progress/checkpoint/artifact/question/blocker/handback-final/engine lifecycle; live/stopped variants C01. | Same inbox stores original identity; accepted kind participates in one eligible wake/consumption; live safe boundary, stopped no revival. Wake expansion does not widen cap exemptions. |
| BDD-04.2 / US4.2 | A | Current-generation stopped parent; live helper publishes valid accepted report, then legitimate human continuation. | Inbox retains report with zero report-triggered dispatch while stopped; next active turn consumes once. Duplicate replay adds no copy. |
| BDD-04.3 / US4.3 | E | Refuse next non-exempt report at preserved rate cap, encoded envelope body boundary or applicable unacked/question-blocker ceiling; C03/C04 isolate limits. | Child typed refusal/applicable retry-backoff hint; parent rejected/not-delivered observation, not accepted content or loss-free has_more inference. No auto retry/second store. |
| BDD-04.4 / US4.4 | X | Retry same accepted report ID; another full result exceeds current delivery limit; C02/C05 include acknowledged duplicate and individually-valid combined overflow. | Accepted report saved/consumed once, no extra acknowledged wake; over-limit has honest failure observation, stored task result remains. No summary/pagination/defer/retry queue or universal-fit claim. |
| BDD-05.1 / US5.1 | H | Native MAIN in main/extra and native WORKER; no self-edge, delegate permitted; test omitted and explicit self in each role. | Ordinary helper launched with identical authorization/context; MAIN delivered prompt prioritizes delegation but role tools remain. Compare ordinary launcher/context, not helper label. |
| BDD-05.2 / US5.2 | A | Helper requests another: below default depth 3/admission available; at 3/admission available; below 3/memory admission unavailable. | Ordinary accept / visible depth refusal / existing admission disposition without bypass; no self-only one-hop or per-main quota. Ordinary memory/recap unchanged. |
| BDD-05.3 / US5.3 | E | Self request from native with explicit delegate Deny, external CLI worker, Admin outside expanded native scope or fixed-tool system agent. | No helper; visible policy/eligibility refusal. |
| BDD-05.4 / US5.4 | E | Other-agent request: no edge; permitted mode/depth/receiver; wrong mode/excess depth; unreadable graph. | Refuse / accept / refuse / refuse under actual workspace gates and target/requested-skill checks. Accepted context/memory exactly ordinary, no special snapshot path. |
| BDD-05.5 / US5.5 | A | Claude Code, Codex and OpenCode each has actual native ID/marker N, live run and known runtime/workspace/model/caps; send S in existing worker chat. | Interrupt/resume same native conversation; S received, N/config/caps retained, same Omnipus chat. Subprocess-kill exception disclosed; no Omnipus self-helper. Real installed lane, not help/mock-only evidence. |
| BDD-05.6 / US5.5 | E | External steering/resume: absent/invalid native ID, absent CLI/bad auth, rejecting/crashing resume, selected execution superseded. | Visible failure, no no-op success/fabricated ID/silent fresh conversation or stale delivery. |
| BDD-06.1 / US6.1 | H | Timing manual/once/RRULE; isolation off/on. MAIN one-time/recurring; native/external worker one-time/recurring; unchanged captured identity first/later occurrence. Heartbeat separate case. | MAIN always fresh real assignee-main child/new run; worker one-time fresh independent ISOLATED; recurring same compatible independent CONTINUE/new run; scheduled isolation fresh independent either role. Task/run/goal and task limits intact. Heartbeat main itself; once/at_ms/RRULE positive timing controls, no old recurring adapter. |
| BDD-06.2 / US6.2 | A | Authorized future scheduled MAIN occurrence under stopped main; starter extra Auto differs, target Deny/Ask and unattended-required-Ask variants C11; force both launch/Stop orders. | Fresh task-origin child admitted only under fence order, zero main-model wake; main-parent grants within target policy/task limits, not starter setting/delegate defaults. General stopped-parent helper guard remains; unattended Ask refuses immediately. |
| BDD-06.3 / US6.3 | H | Creator A, MAIN starter B, MAIN assignee Mia; recipients captured B-main/Mia-main. Equal starter/assignee variant. Outcomes done/failed/winning Stop then late success/skipped fire/retry still within run. | Captured recipients get authoritative brief header/reason+full result for actual terminal run; same recipient once, never creator substitute/late contradictory success/skipped/per-retry final. Eligible idle wakes once, ready notices batched, live safe boundary, stopped retained report. |
| BDD-06.4 / US6.4 | E | Captured session deleted/hidden; recipient reassigned after start C09; full result exceeds current envelope/context; independent monitored ISOLATED/CONTINUE. | Task/run result inspectable, delivery failure differs from consumption. Fixed capture/no guessed recipient/fake edge/summary/overflow queue. Hidden no wake; monitoring confers no chat-tree Stop. |
| BDD-06.5 / US6.5 | X | Complete worker A/W1/runtime R1 in C1; authorize between-run worker/workspace/runtime change. Also edit while active paused run, then next occurrence. | Unchanged identity C1 reused/new run; changed identity C2 next segment with correct new owner/workspace/runtime/native ID. Series/prior run→C1/history/owner intact; active capture unchanged, no foreign-policy dispatch. |
| BDD-07.1 / US7.1 | H | Native main has live helper/current MAIN child/independent task/future recurrence. First Stop/Esc/stop; second <3 s; cancel; Stop after window closes. | Current turn / real downward tree / real tree immediately / current turn. Preserve independent/future/parent/sibling as applicable; native plain Stop keeps background shells, tree kills descendants. Actual liveness/selected identity; no Stop-all button, helper count not historical. |
| BDD-07.2 / US7.2 | E | Competing steer/redirect paused at acceptance/publication; both orders plus replacement turn and late force/completion (B10). | Each authoritative queued/applied/superseded/refused; refused text absent from consumption. Same root/helper chat redirect with new instruction; curated explanation, no internal stale-generation text or stale mutation of replacement. |
| BDD-07.3 / US7.3 | X | Block consumption; accept web H1/channel H2 and helper report R, older parent control P. Stop wins, then human N continues. Inverse: H1 committed before Stop, H2/H3 still waiting. | Waiting human IDs discarded/not delivered live/reload and never later consumed; already-committed H1 not labelled forgotten. Next legitimate turn gets N/R once, never superseded P. Archive prefix intact; no held/recovery state/action/button. |
| BDD-07.4 / US7.4 | X | Gateway killed during in-flight chat with waiting input; I1-owned waiting-for-answer variant, then manual next message (B12). | Interrupted, not permanent Working/failed conversation solely from restart; zero old-input boot dispatch. Manual continuation, task interrupted failure/plan recovery unchanged; #1217 externally owned. |
| BDD-08.1 / US8.1 | H | Mia owns C, Jim eligible MAIN; explicit @request/material X, source-history-only decoy Z. | Jim gets request/X, never whole history/Z. Reply guest-labelled Jim live/replay with exact IDs; Mia sees request/answer, owner unchanged; idle eligible wake once, allowed silence no empty bubble. |
| BDD-08.2 / US8.2 | A | Peer target: eligible same-workspace without edge; explicit foreign pair; worker/other system/Admin non-default; hidden member main. Text spoofs parent/human. | First two delivered under receiver permissions; excluded/hidden refused. Zero transferred Stop/context/grants/ancestor authority or classification-by-text. |
| BDD-08.3 / US8.3 | H | One main consumes web U and same-platform I1/I2 with distinct authenticated principals/chats/threads/request IDs. Address individual replies. | Exactly each original sender/instance/thread/session/correlation gets its reply; negative destinations receive none. No broadcast/last-sender fallback. |
| BDD-08.4 / US8.4 | E | Mixed-source output has missing/null usable return correlation/destination; deleted recipient variant. | Visible refusal asks agent to address sender; zero guessed/default outbound send. Warning plus fallback fails. |
| BDD-08.5 / US8.5 | H | Permitted canonical caller in W1: create/update/list workspace omitted; create explicit W2; update execution-enabling W2; list explicit W2; invalid/inaccessible/null/empty/Deny, unresolved caller and cross-workspace plan/dependency/active-run variants E13/E15. Move Ava/Admin/other callers; challenge retired names. | Own-scope defaults, no inferred foreign workspace. W2 create assigned board Inbox/not-started, zero trigger/run; W2 update-execution refused unchanged; W2 creator status only. Invalid/Deny/prohibited plan state zero unintended writes/disclosures/fallback. Only canonical tools/validation/store/audit; old names refuse, no alias/executor. |
| BDD-08.6 / US8.6 | E | A in W1 has delivered B-assigned W2 Inbox task; attempt run/start, re-run, assign/reassign/status next/in_progress, schedule/arm, update/REST wrapper. Source edge or same agent ID across workspaces variant E17. | Visible created-not-started/must-pick-up-locally explanation. Zero status-for-execution mutation/claim/activated goal/dispatch/armed trigger/run/session. Positive local start; source graph cannot authorize W2. |
| BDD-08.7 / US8.7 | H | W2 has A-created T, other-agent U, human-name A collision and empty creator; explicit W2 list/direct-ID/alternate-list-role query. | Only T's read-only status/resolved metadata; no U/colliding/unattributed record/private creator field. No mutation/execution or human/operator lookup bypass. |
| BDD-08.8 / US8.8 | A | B receives assigned W2 board Inbox and A's optional explicit-pair pickup message; B chooses local start under allowed/denied W2 policy. | Placement/message caused zero prior dispatch. Allowed local normal mode/claim/goal/run/selected identity; denied local refuses regardless W1 graph. A remains creator, not invented starter/parent/recipient; normal captured recipients. |
| BDD-08.9 / US8.9 | E | Mia owns I1/q1→Jim. Reply content+reply_to=q1; repeat forged/wrong-receiver/discarded/unreadable ID; hold outbound then rebind I1 before send/retry/placeholder edit. I2 negative recipient. | Valid one actual Mia/I1 original-thread send authored Jim, request/reply IDs live/replay; no guest grant. Invalid zero I1/I2/guessed sends, visible failure. Observe actual transport owner/author separately; bus acceptance not delivery. |
| BDD-08.10 / US8.10 | A | Global AND agent maps: canonical Allow/Ask/Deny/absent crossed with retired-name Allow/Ask/Deny/absent, seeded/manually saved conflicts. Load/invoke canonical and removed names. | Effective catalog has no old callables; retired saved values grant/restrict nothing and need no rewrite. Canonical explicit policy effective unchanged; absent canonical frozen shipped default/role and strictest-wins. No conversion/old-Deny merge/old-Allow foreign grant/third layer/backfill. Assert effective tools/permissions, not key removal. |
| BDD-09.1 / US9.1 | H | Mia row / row New chat / switch-agent Jim / connect-reconnect / sessions search, existing extra retained. | Mia main / deliberate extra / Jim main / roster refresh without widened eligibility / current search. No mutable handover, generic new bypass/latest/fresh fallback; unavailable attach uses 01.4. |
| BDD-09.2 / US9.2 | H | Main AND extra: live step already consumed A, H pending; request clear, release safe boundary (B08). | In-flight input unchanged. At boundary display/context marker and next request H under same ID; A bytes/search/recall preserved. Reopen preserves clear view, no new session. |
| BDD-09.3 / US9.3 | E | Each command-table row through palette, typed/server and stale-menu direct invocation in native/external helpers/workers/MAIN child; main/extra positive clear. | Allowed within authorization; forbidden writes/clear refuse without display/window/native reset; retired names absent/refused. Read-only lists never select model/config; no alias bypass. |
| BDD-09.4 / US9.4 | A | Human follow-up into live, stopped and finished legitimate worker/task chat. | Steer live run or legitimate same-chat continuation; completed task/run/result unchanged, zero implicit new claim/run. Pre-Stop discarded text never returns; explicit task Rerun separate. No fresh worker/@ eligibility. |
| BDD-09.5 / US9.5 | E | Marker X in native/external worker, self-helper, other-agent delegate, MAIN child, isolated scheduled task; clear palette/typed/server then follow-up. Main/extra controls. | All helper contexts explain refusal, no view/window/native reset/new session; same conversation retains X. Main/extra safe clear works; MAIN role cannot bypass session classification. |
| BDD-10.1 / US10.1 | H | Agent has native helpers, scheduled MAIN child and independent starter-monitored task, plus running/queued/waiting/terminal history. Open Activity. | Relevant actual rows/states/available provider-run tokens and correct links; missing usage not zero. One MAIN row, no task+child duplicate; viewer no independent-tree Stop; historical child_count not live count. |
| BDD-10.2 / US10.2 | H | Native helper/run approval: pending/resolved/expired/out-of-workspace, external contrast (D06); different parent/starter Auto (C11). | One ID/current scoped actions/target restrictions, acting execution name/Open; no duplicate queue/card. Parent inheritance valid, global/per-chat choices/resolution retained, unattended Ask refusal and external exception honest. |
| BDD-10.3 / US10.3 | E | Pending roster/main load, empty retained main, failed roster refresh with cache, unknown lifecycle/usage, rejected report/result, partial Delete. | Loading/no extra / empty same identity / visible stale+Retry / partial unknown not running-zero / not-delivered+task result / partly-deleted visible agent+same Delete retry. No replacement main/history recovery/success fiction. |
| BDD-10.4 / US10.4 | H | Individually keyboard/assistive operate New chat/isolation/Stop/commands/Open/approval/Delete; Escape menu/dialog, pointer/touch, zoom/reflow. | Same authorized effect, readable guest/name/status/refusal, focus/restoration. No dismissal-as-confirmation/accidental Stop; 24×24 pointer/44×44 touch, 200% zoom/320px usable, no hidden/overlap controls. |
| BDD-11.1 / US11.1 | H | Settled main, default recap on: idle 29:59/30:00; late activity reset; still-live settlement; same-ID resume then another idle episode (D09/D10). | One memory recap only at actual 30-minute inactivity after settlement, no archive summary/new chat/live overlap. Any message/turn resets; resumed next episode can recap again. |
| BDD-11.2 / US11.2 | H | X owns chats/memory, guest answer in Mia. UI two confirmations/warning; authorized tool/API existing single approval; membership removal separate. | Same cascade removes only owned data FIRST and record LAST, guest remains labelled deleted agent on completed deletion. Guard/confirmation correct; no tool/API second UI gate. |
| BDD-11.3 / US11.3 | E | UI first-only/dismissed confirmation, default/locked/system/active-plan guard; inject owned-session/memory/required-cleanup or final record-delete failure. | Guarded zero destruction; actual partial cleanup reported honestly, X record/row remains visible, guest history intact. Final failure never full success; existing Delete still available. |
| BDD-11.4 / US11.4 | A | Bootstrap/idle/agent-recorded retrospective and ordinary helper recap eligibility. | Current bootstrap/idle/joined and helper memory unchanged; lazy/explicit/session-close cannot recap. No new compaction-summary workflow. |
| BDD-11.5 / US11.5 | X | Fail session cleanup or memory/SOUL/required cascade step, or record deletion after data cleanup. Restart, reopen still-visible X and press same Delete via UI/tool/API with current authorization/revision; duplicate retry. | Partly-deleted record remains until ALL cleanup succeeds; after restart retry finishes leftovers; already-removed owned data acceptable. Record removed last only on success; guest bytes intact, wrong authority/revision refuses. No separate manifest/list/filter/timestamp recovery. |
| BDD-12.1 / US12.1 | E | Every inventory ID/direct caller/sub-symbol: K compiler/typecheck+controlled absence; B old callable/wire/reader/control challenged with canonical positive case. | K check detects forbidden alias and known symbol, no browser case for erased names. B actual obsolete behavior absent plus canonical scoped behavior/current once-RRULE preserved. Every logical ID accounted; no hidden alias. |
| BDD-12.2 / US12.2 | X | Current correlated first-send/queued delivery, kickoff ack, no-stream final/no prior bubble, tool-owner ring eviction, reconnect offsets, span start/state/end out of order, typed model/replay+control/kickoff errors, keyed goal and plan-global verdict. | Current message/tool/lifecycle/goal/plan visible and truly owned using canonical producer IDs, no fallback/latest guess/output loss. Preserve nonlegacy shared branches before deletion. |
| BDD-12.3 / US12.3 | E | Real generated validator/consumer: valid main/member/message/run/control; absent current Session.type/wrong pair; client main/old handover/close; old every/cron_expr; valid once/RRULE; malformed correlated identity/typed error. | Valid/current timing accepted; invalid/retired server/schema refusal or visible error, no hand-written bypass/guessed producer. Correct baseline control first so unrelated rejection cannot mask intended bound. |
| BDD-12.5 / US12.5 | X | Run A/message a→G1 empty active criteria, B/b→G2 nonempty, c unknown. Shuffle G2 then delayed G1/error, snapshot/replay/non-stream; producer stamps captured. | a matching G1 setup only; b G2/ordinary never relabelled G1; c neutral. Same association live/history/replay, exact criteria merge retained. No goalStatus/_default/latest-map. |
| BDD-13.1 / US13.1 | H | Main, including default Admin: pending ask/approval or unseen met/rounds_exhausted/other; controls stopped_by_user, extra/helper/worker source, generic task/plan activity, no source; non-main response. | Main true only for pending or mapped unseen outcome (met finished, other two failed); negative/no-source false; non-main field omitted. No lifecycle/source-chat mutation or invented non-default Admin main. |
| BDD-13.2 / US13.2 | A | Users A/B share main: unseen G1, pending Q/P; G2 appended beyond A's successful foreground-open captured bound. Replay old acknowledged outcome. | G1 seen for both; Q/P pending, G2 unseen, replay no relight. Resolve clears corresponding Q/P, not open. Bounded mark survives restart without growing ID set. |
| BDD-13.3 / US13.3 | E | Known/unseen attention: background attach/prefetch/reconnect, acknowledged replay, new outcome while unopened, source/metadata read failure, unauthorized/failed open, restart. | Background/failed open no seen mutation; replay no relight; unopened-main existing query/frame path updates. Read failure visible unknown/stale/error not false; restart restores saved shared mark/current pending, no per-user fallback. |
| BDD-13.4 / US13.4 | H | Human-facing agent needs answer; structured tool permitted vs denied/unavailable. Child question remains report to parent. | Real structured card/pending-resolve main attention; delivered prompt-owner rule, no text detector/event system. Unavailable truthful refusal, no permission bypass; parent asks user via structured tool. |

## Proposed test families

QA supplies real names/receipts in RED; each family expands its referenced cases.

| ID | Family | Level | Acceptance |
|---|---|---|---|
| T01 | MainPairIdentityAndProtection | Unit | BDD-01.1, BDD-01.2, BDD-01.4 |
| T02 | SingleArchiveBoundedWindowAndAppendEffects | Unit | BDD-02.1, BDD-02.2 |
| T03 | RetentionCursorRepairAndProtectedIdentity | Unit | BDD-02.3 |
| T04 | OrdinaryIntakeIndependentBounds | Unit | BDD-03.3 |
| T05 | LiveConfigAdmissionSnapshot | Unit | BDD-03.4 |
| T06 | AcceptedReportClassAndPreservedCaps | Unit | BDD-04.1, BDD-04.3, BDD-04.4 |
| T07 | NativeSelfAuthorizationAndOrdinaryLimits | Unit | BDD-05.1, BDD-05.2, BDD-05.3, BDD-05.4 |
| T08 | AutomaticTaskModeAndTiming | Unit | BDD-06.1, BDD-06.5 |
| T09 | SelectedExecutionControlAndDiscard | Unit | BDD-07.1, BDD-07.2, BDD-07.3 |
| T10 | PerSourcePeerReturnAddressing | Unit | BDD-08.2, BDD-08.3, BDD-08.4, BDD-08.9 |
| T11 | CanonicalSessionAwareCommandCapabilities | Unit | BDD-09.1, BDD-09.3, BDD-12.1, BDD-09.5 |
| T12 | CurrentActivityAndCanonicalReducerCases | Unit | BDD-10.1, BDD-10.3, BDD-12.2, BDD-12.5 |
| T13 | IdleEpisodesAndCurrentRecapTriggers | Unit | BDD-11.1, BDD-11.4 |
| T14 | CurrentGeneratedContractControls | Unit | BDD-12.3, BDD-12.5 |
| T15 | AtomicProvenanceFIFOAndSettlement | Integration | BDD-02.4, BDD-03.1, BDD-03.2, BDD-03.4, BDD-07.2 |
| T16 | ReportRetentionRefusalAndDedupe | Integration | BDD-04.1, BDD-04.2, BDD-04.3, BDD-04.4 |
| T17 | TaskParentAdmissionCaptureAndTerminalNotice | Integration | BDD-01.3, BDD-06.1, BDD-06.2, BDD-06.3, BDD-06.4, BDD-06.5 |
| T18 | AddressedGuestAndConnectorReplyRoute | Integration | BDD-08.1, BDD-08.2, BDD-08.3, BDD-08.4, BDD-08.9 |
| T19 | CLINativeResumeAndFailureIdentity | Integration | BDD-05.5, BDD-05.6, BDD-06.5 |
| T20 | RestartInterruptedWithoutWaitingReplay | Integration | BDD-07.4 |
| T21 | CleanupFirstDeletionAndRetry | Integration | BDD-11.2, BDD-11.3, BDD-11.5 |
| T22 | SafeClearAndExistingWorkerContinuation | Integration | BDD-09.2, BDD-09.4, BDD-09.5 |
| T23 | CanonicalDeletionAndContractGeneration | K compile/source; B Integration | BDD-12.1, BDD-12.2, BDD-12.3, BDD-12.5 |
| T24 | JointMainExtraNavigationAndSearch | E2E | BDD-01.1, BDD-01.2, BDD-01.4, BDD-09.1, BDD-09.2 |
| T25 | LiveStopRedirectDiscardedInput | E2E | BDD-07.1, BDD-07.2, BDD-07.3, BDD-09.4 |
| T26 | GroupIdentityAndMixedSources | E2E | BDD-08.1, BDD-08.2, BDD-08.3, BDD-08.4, BDD-08.9 |
| T27 | AgentTaskActivityAndApprovalAttribution | E2E | BDD-10.1, BDD-10.2, BDD-10.3, BDD-06.4 |
| T28 | KeyboardWorkerCommandsAndControlStates | E2E | BDD-09.3, BDD-10.4, BDD-09.5 |
| T29 | IdleResumeDoubleConfirmDeleteRetry | E2E | BDD-11.1, BDD-11.2, BDD-11.3, BDD-11.4, BDD-01.3, BDD-11.5 |
| T30 | ExactCandidateJointReachabilityAndDocs | E2E | BDD-12.2, Reachability |
| T31 | ActualThreeCLILiveSteering | E2E | BDD-05.5, BDD-05.6, BDD-09.5 |
| T32 | MainOnlySharedAttention | Unit/Integration/E2E | BDD-13.1, BDD-13.2, BDD-13.3, BDD-13.4 |
| T34 | CanonicalTaskFamilyWorkspaceMerge | Unit/Integration/E2E | BDD-08.5, BDD-08.6, BDD-08.7, BDD-08.8, BDD-08.10, BDD-12.1, BDD-12.3 |

## Test datasets

Only fixtures adding values beyond BDD cases remain; merged values/IDs are in the final ID map. Isolate competing bounds explicitly.

| ID | Fixture | Independent expected value | BDD |
|---|---|---|---|
| A01 | 1-byte text with ample headroom | accepted with authenticated sender and distinct message | BDD-03.1 |
| A02 | empty content, no media / null content | current message schema rejects; no admission | BDD-02.4, BDD-12.3 |
| A03 | empty caption, valid media reference | accepted if media/model/other bounds fit; reference retained | BDD-03.3 |
| A05 | combining characters, multibyte text, newlines at those byte boundaries | same byte-bound result; not character-only maxLength | BDD-03.3 |
| A07 | sender A reached 60; sender B and same display name on another trusted instance send once | B/distinct principal has own rate scope, subject to target's other bounds | BDD-03.3, BDD-08.3 |
| A11 | tiny text plus high media/tool/pinned overhead | current estimator/model fit, not text-only fit | BDD-03.3 |
| A12 | late instruction during final-settlement barrier plus separate fresh chat of same agent | immediate same-session next turn; no cross-chat queue/control contamination | BDD-03.2, BDD-07.2 |
| A13 | valid non-default settings before reload/after reload/fresh boot | observed intake boundaries change only after current live application; all ordinary adapters agree | BDD-03.4 |
| A14 | control text claims another sender/parent; provenance save denied | trusted identity cannot be replaced by text; failed atomic acceptance not consumed | BDD-02.4, BDD-08.2 |
| B01 | zero retained entries; same eligible main metadata | empty same-ID chat/model view, protection retained | BDD-02.3, BDD-10.3 |
| B02 | marked yesterday's byte offset, today's complete tool group | valid bounded multi-day window; prefix unchanged | BDD-02.1, BDD-02.2 |
| B03 | delayed older timestamp after newer accepted input | accepted FIFO, no earlier-file back-insertion | BDD-02.2 |
| B04 | call/result crosses midnight; repeated tool ID in later turn | matching provider-valid group and correct result reference | BDD-02.2 |
| B05 | file mtime younger than / equal to / older than 90-day cutoff | current sweep's strict older-than deletion only; expired mark repaired | BDD-02.3 |
| B06 | retention disabled; old content present | no deletion or expiry repair manufactured | BDD-02.3 |
| B08 | clear requested while step in flight, human H pending | old in-flight step unchanged; next boundary clears display/window, not H/archive | BDD-09.2 |
| B10 | Stop/redirect/steer before/after publication barrier, then replacement turn | authoritative dispositions; refused text absent; stale force/completion cannot mutate replacement | BDD-07.2 |
| B12 | gateway crash while running / I1-owned waiting-for-answer, queued text present | Interrupted integration, no old-input boot dispatch, manual next-message continuation | BDD-07.4 |
| C01 | each accepted existing helper report kind; idle/live/stopped parent | common wake/safe-boundary/retained-report behavior without cap widening | BDD-04.1, BDD-04.2 |
| C02 | same accepted report identity retried | one saved/consumed content identity, no extra wake after acknowledgement | BDD-04.4 |
| C03 | ordinary non-exempt report reaches current rate cap, then one more | typed rejection/retry information and parent rejected visibility | BDD-04.3 |
| C04 | report encoded envelope just below / at / above existing body cap | preserve current cap behavior; rejected content not claimed delivered | BDD-04.3 |
| C05 | multiple full TaskRun results individually valid but collectively too large | current delivery/context failure visible; task results stay; no new summary/defer path | BDD-06.4 |
| C09 | recipient deleted/hidden/reassigned after run start | capture fixed, stored result retained, no guessed replacement/hidden wake | BDD-01.3, BDD-06.4 |
| C11 | extra-chat Auto off, main Auto on, target tool-policy Deny/Ask; unattended required Ask; #1221 removes agent Auto off-switch | main parent source within target restrictions; no unattended popup wait; loosening disclosure | BDD-06.2, BDD-10.2 |
| D06 | existing approval pending/resolved/expired/out-of-workspace; native/external | existing scope/actions/ID preserved, acting attribution, no external protection claim | BDD-10.2 |
| D09 | idle 29:59 / 30:00 after last real activity; new message/turn reset; live settlement | recap only at actual idle threshold and after settlement; same ID continues | BDD-11.1 |
| D10 | resume same ID, then another full idle episode; joined retrospective/helper fixture | new idle episode recap possible, joined/helper memory unchanged, no archive summary | BDD-11.1, BDD-11.4 |
| E13 | explicit null/empty/invalid/inaccessible workspace; unresolved caller; Operator Deny | existing typed/schema/authorization refusal, zero fallback/write/disclosure | BDD-08.5, BDD-12.3 |
| E15 | explicit other workspace plus foreign plan/dependency/active-run conflict | existing all-or-nothing validation refuses prohibited state; no cross-workspace plan move/copy | BDD-08.5 |
| E17 | foreign run/start/re-run/assign/trigger through tool, update, REST wrapper and source-policy edge | refuse before writes/dispatch; source graph cannot authorize W2 work | BDD-08.6 |

### Regression protection

Existing tests read, not run; port their safety assertions to canonical replacements, never weaken them.

| Preserved behavior | Existing source test | Required regression complement |
|---|---|---|
| Window eviction deletes zero archive bytes; evicted history remains recallable | `pkg/memory/archive_test.go::TestArchive_SkipEvictionDeletesZeroBytes`, `TestReadArchive_ReachesEvictedTurn` | T02/T03/T22 assert day-byte mark/projection/clear and unchanged prior prefixes |
| Inbox message-ID dedupe | `pkg/session/message_inbox_test.go::TestMessageInboxStore_DedupeByMessageID` | T06/T16 preserve dedupe and consumption/wake identity while extending accepted kinds |
| Native plain Stop retains real background process; tree Stop kills it | `pkg/agent/stop_session_background_q13_unix_test.go::TestQ13StopSession_PlainStopEndsTurnLeavesRealBackgroundAlive`, `TestQ13StopSession_StopAllKillsRealBackgroundProcess` | T09/T25 keep selected-execution and downward-tree scope with MAIN task children; external exception is separate |
| Current Calendar one-time authoring and common execution | `src/components/calendar/CalendarEventSlideOver.tsx::buildTriggerForSave`; `pkg/agent/task_trigger.go::triggerToCronSchedule` | T08/T17/T23 keep once/at_ms and RRULE; no every/cron_expr compatibility fixture survives |
| Current first-send/kickoff/non-stream/approval/goal/plan producers | Exact canonical producer/reducer mapping in DELETE inventory and E-NAV/E-APPROVAL/E-ACTIVITY | T12/T23/T24/T27 force current cases, not only old-schema absence or mocked successful render |
| Task claim/goal/attempts/outcome and plan recovery | E-TASK/E-SCHEDULE existing task-origin path; S-ADR::preserved boundaries | T17/T20 preserve one task outcome owner, separate run identity, task-specific limits and Stop-authoritative result |

Preserved cases run baseline/candidate; changed behaviour needs RED. #1211/#1214 need controlled failure receipts; #1216 actual settings/admission mismatch, not source search alone.

## Explicit DELETE Requirements

Single normative inventory; all active callers/catalog/policy/default/prompt/contract and compatibility-only tests/docs included. Historical saved calls remain inert, never aliases. T23 accounts for every logical ID; merged groups preserve all IDs.

K: erased alias removal uses compiler/typecheck/generated-contract and controlled source/caller checks, not browser cases. B: same removal checks plus actual changed behaviour/canonical positive control named in Proof. Known-present and injected-forbidden controls test absence instruments; source text alone is not behaviour. Preserve current producers sharing old branches.

### Backend / shared

| ID | Source / removal target | Canonical replacement / boundary | Proof / tests |
|---|---|---|---|
| DEL-01 | `pkg/gateway/schedules.go::pickSession` — `sched-main-<agent>` standing-session branch; `pkg/session/unified.go::NewHeartbeatSession` heartbeat-only identity/enable-coupled lifecycle | Mature that creation/get-or-create/protection path into the one computed main session. Delete separate stored heartbeat session-address mapping, not heartbeat configuration/origin. | B T01/T08/T17/T24 |
| DEL-02 | `pkg/agent/ordinary_execution_admission.go::awaitPreviousOrdinaryExecution`, `previousExecutionSettleBudget`, `ErrPreviousExecutionPending`; ordinary “still finishing” refusal mapping | session runner owns settlement before next admission; retain internal duplicate-execution tripwire, not user-facing early-arrival refusal. | B T09/T15/T25 |
| DEL-03 | `pkg/agent/session_worker.go::workerInboxCap`, `sessionWorker.inbox`, system-turn buffering/probe and legacy bare-goroutine fallback in `dispatchSystemMessageToSessionWorker` | per-session steering queue/runner and report inbox. No capacity-full bypass dispatch. | B T09/T15/T25 |
| DEL-04 | `pkg/agent/steering.go::manualSteeringScope`, `push`, `dequeue`, manual fallback drain, `SteeringOneAtATime`, `parseSteeringMode`; `SteeringMode` configuration | Resolved session address and the same FIFO, always all-at-once. No manual/unscoped queue or user-selectable dequeue modes. | B T09/T15/T25 |
| DEL-05 | `pkg/commands/cmd_clear.go::clearCommand` old `/new` action/hidden alias; `/resume` old client name; `/agents` old selector/list action | `/clear` main/extra-only safe-point context/display reset; helper refusal, `/sessions` search, `/switch-agent` MAIN-session navigation. Old names do not survive as hidden aliases. | B T11/T22/T24/T28 |
| DEL-06 | `pkg/commands/builtin.go::BuiltinDefinitions` deprecated `/start`, `/show`, `/list`, `/switch`, `/check`; `/channel` alias/support wherever wired; their hidden/deprecated execution/menu guidance | Nothing for removed names. Current noun commands/actions remain as allowed by the final capability table. Remove Telegram compatibility `/start`, not a new greeting workflow. | B T11/T22/T24/T28 |
| DEL-07 | `switch_agent`/handover implementation, catalog, prompt/tool-exclusion entries; `pkg/session/unified_write.go::SwitchAgent`; routing handoff pins and `agent_switched` consumers | Direct MAIN-peer messaging and session navigation. The slash navigation command is not a replacement tool with the same name. | B T01/T18/T23/T26 |
| DEL-08 | `pkg/agent/memory.go::TriggerLazy`, `TriggerExplicit`; explicit session-close WS frame, handler and ack; obsolete lazy/explicit invocation paths | idle/bootstrap recap. **Joined stays**: `RetrospectiveTool.Execute` uses it. | B T13/T29 |
| DEL-09 | `pkg/session/unified.go::migrateLegacy`, `writeUnifiedMetaDirect` migration/runtime missing-type fallback; `pkg/memory/migration.go::MigrateFromJSON` and boot import callers | DELETE importer/readers/conversion/backfill and boot callers. Current single archive only; no cutover exception. | B T02/T03/T15/T23 |
| DEL-10 | `pkg/agent/instance.go::initSessionStore` JSONL/SessionManager fallback; `pkg/session/manager.go::SessionManager`; `pkg/agent/loop_session.go::GetAgentStore`, `getLegacyAgentStore`, legacy scan/merge branches | Shared UnifiedStore only; move scoped task/verifier/delete/read callers then DELETE per-agent/SessionManager fallback/readers. Read errors visible, no alternate history. | B T02/T03/T15/T23 |
| DEL-11 | `pkg/session/daypartition.go::SessionMeta.PostLoad` old multi-agent backfill; legacy owner/active-agent/type/token/truncation/provenance defaults in scoped readers | Explicit current identity/entries only; DELETE old normalization. Keep current optional/zero semantics, user/system zero-token entries and authenticated anonymous/shared modes; contributor is not owner handover. | B T02/T03/T15/T23 |
| DEL-12 | Separate model content history/backend rewrites, `pkg/memory/window.go::RollbackWindow` destructive rewrite; whole-history `SetHistory` hydration/compaction paths; in-place transcript truncation/tool-result updates | One archive, current window/projection metadata and append-only correction effects. Keep provider-valid replay and full-result references; no compatibility second writer. | B T02/T03/T15/T23 |
| DEL-13 | `pkg/agent/steer_classify.go::Classify` pre-ADR-origin leniency/legacy delegate branch; `pkg/agent/boot_sweep.go::SteerBootRecovery.failLegacy`, `failedReasonPreADR091NotResumable`; obsolete legacy class enum/support | Current record/metadata validation and restart-stop settlement. Malformed current children do not become roots merely because an old compatibility branch did so. | B T09/T17/T20/T23 |
| DEL-14 | `pkg/session/message_inbox.go::backfillSeq`; `pkg/workspace/delegation.go::legacyModeDirect`, `DelegationEdge.UnmarshalJSON` old-mode migration | Current persisted inbox sequence and current direct/task edge validation. No legacy sequence/mode rewrite. Preserve normal dedupe/ack and non-self trust checks. | B T09/T17/T20/T23 |
| DEL-15 | `pkg/cron/service.go::migrateOwners`, `migrateOwnersUnsafe`, `AddJob` back-compat creation; boot default-agent migration wiring | `AddJobFull`/JobSpec with explicit authorized owner and derived mode. No owner-less job upgrade backfill. | B T09/T17/T20/T23 |
| DEL-16 | `pkg/gateway/sse.go::SSEHandler`, `newSSEHandler`; `pkg/gateway/gateway_boot.go` backward-compat SSE registration | WebSocket persistent-session transport; remove matching old chat-stream contract/callers, not add another streaming fallback. | B T14/T23 |
| DEL-17 | `pkg/gateway/websocket_cancel.go::u11CollectDescendantSessionIDs` signature shim; stale prose about the earlier u11 name in `pkg/agent/cancel.go` | `CollectDescendantSessionIDs` and one Stop path, preserving returned errors/selected execution. Nothing replaces the gateway wrapper kept solely for an older signature. The current AgentLoop.collectDescendantSessionIDs is a store-bound canonical adapter, not a live u11 compatibility shim; preserve it or route its callers to the same collector without changing behavior. | B T09/T17/T20/T23 |
| DEL-18 | `pkg/agent/task_goal_terminal.go::mintLegacyTaskGoal`; legacy task-goal backfill branches reached from `activateTaskGoal`, task create/edit/wire | fresh task-goal creation/update and task-owned goal activation. Keep intentional scratchpad/non-goal behavior; no upgrade-only goal minting. | B T09/T17/T20/T23 |
| DEL-19 | `pkg/agent/task_trigger.go::triggerToCronSchedule` old every/cron_expr branches; `pkg/gateway/task_occurrences.go::expandCronServerZone`, legacy every projection; `src/components/calendar/CalendarEventSlideOver.tsx::isLegacyTrigger`, old-trigger preservation/preview | DELETE old every_ms/cron_expr adapters and compatibility promises; recurring Calendar work uses RRULE/compileRecurrence/next-occurrence/rearm path. KEEP once/at_ms, the current Calendar one-time authoring format, and remove its misleading legacy label (15:55 Q4=A). Current buildTriggerForSave/triggerToCronSchedule use it through the same cron at-job engine as RRULE; no new one-time scheduler/encoding. | B T08/T17/T23 |
| DEL-20 | E-CLI no-op `Input` success and fresh-run `Resume` implementations presented as steering/resume; old CLI steering exclusion/help | drivers matured for interrupt plus actual native-ID resume. No invented ID, successful no-op or silent fresh-session fallback. | B T19/T31 |
| DEL-21 | `pkg/config/validate.go::MigrateLegacyToolPolicyKeys`, `migrateLegacyToolPolicyMap`, old tool-key rename entries feeding switch_agent; loader wiring for those retired aliases | DELETE legacy key remapping/loader aliases (hand_off/return_to_default→switch_agent). Keep Reconcile/current ceiling+tightening; retired saved settings inert/ignored, no rewrite/conversion/merge/backfill (D2). | B T01/T18/T23/T26 |
| DEL-23 | `pkg/sysagent/tools/task.go::TaskCreateTool`, `TaskUpdateTool`, `TaskDeleteTool`, `TaskListTool` — create_task_in_workspace/update_task_in_workspace/delete_task_in_workspace/list_tasks_in_workspace; their task registrations in `pkg/sysagent/tools/registry.go::AllTools`, category/policy/default/prompt/wire/caller/compatibility-only references | DELETE four duplicate task implementations/callables/active refs. Reuse canonical pkg/tools/task.go::TaskCreateTool/TaskUpdateTool/TaskDeleteTool and pkg/tools/task_query.go::TaskListTool, optional workspace on create/update/list; same validation/store/audit/criteria/owner/plan/approval. FR-046/048/049: foreign assigned board Inbox/not-started, creator status-only, receiver-local execution; no new executor/plan/alias. Policy home DEL-21. | B T34/T23 |
| DEL-22/24 | `pkg/agent/turn.go::getAnyActiveTurnState`, `GetActiveTurn`; `pkg/agent/steering.go::Steer`, `InjectFollowUp`; `pkg/agent/steering.go::InjectSteering`, `EnqueueSteeringMessage`, `InterruptGraceful`, `InterruptHard`, arbitrary-turn enqueue attribution; `pkg/tools/delegate_followup.go` old/basic sink interfaces/callers | DELETE unscoped first-active-turn controls/design alias/status-stripping wrapper/fallback sink. Use canonical session-targeted status-carrying intake, GetActiveTurnBySession/StopSession and actual scoped event attribution; selected execution/queued receipt/error preserved, no manual/global alternative. | B T09/T15/T25 |
| DEL-25 | `pkg/gateway/websocket_streamer.go::WSHandler.GetStreamer` missing-session-ID connection-binding fallback | DELETE guessed binding. Producing callers supply explicit sessionID; missing target refuses, never streams into the currently viewed replacement chat. | B T12/T14/T23 |
| DEL-26 | `pkg/tools/session.go::StatusExited`, `ProcessSession.IsDone`, statusPriority and matching poll/read/test compatibility branches | DELETE legacy exited value/branches and compatibility-only fixtures; preserve current done/killed/timeout/canceled terminal behavior and reasons. Source sweep must show no current writer before removal. | B T09/T23 |

### Frontend

| ID | Source | Old branch | Replacement / boundary | Proof / tests |
|---|---|---|---|---|
| DEL-F01 | `src/hooks/useSlashMenu.ts::runClientSlashCommand`, `executeSlashCommand`, `resolveClientCommand` | `name === 'new' \|\| name === 'clear'`; palette/typed definition resolution also matches `c.aliases`. `/clear` is explicitly legacy and hidden. | startNewSession only via agent-row extra; main/extra safe-point clear, every helper refuses; no new/clear hidden alias. | B T11/T22/T24/T28 |
| DEL-F02 | `src/components/chat/ChatScreen.tsx::commandLabelsWithAliases` | Adds `/${alias}` to built-in labels so old command aliases suppress skill chips. Live UserMessage/virtual user rows consume it. | Canonical command labels already come from `commands`; nothing for retired alias names. | B T11/T22/T24/T28 |
| DEL-F03 | `src/lib/api/agents.ts::isWorker` | Third test `a.type === 'worker'`, for stale payloads. | `Subagent` / `subagent_3p` tests already present. | B T01/T12/T23/T24 |
| DEL-F04 | `src/lib/agentKind.ts::isWorkerType`, `agentKindFlags` | Legacy `worker` recognition and `(type === 'worker' && executor.kind === 'external-cli')` classification. | modern type-based native/external classification. | B T01/T12/T23/T24 |
| DEL-F05 | `src/components/agents/AgentProfile.tsx::AgentProfile` (kind derivation) | Live profile derives worker/native/external gates through the legacy-aware `agentKindFlags`; the legacy case is used for heartbeat eligibility. | Same helper's modern type cases; no replacement worker kind. Profile consumer only, not provider compatibility. | B T01/T12/T23/T24 |
| DEL-F06 | `src/lib/api/sessions.ts::RawSession` | `type RawSession = _RawSessionInternal`, explicitly “Alias for backward-compat within this file”; used in adapters/request casts. | `_RawSessionInternal` already exists; no new wire type. | K T14/T23 |
| DEL-F07 | `src/lib/api/sessions.ts::rawToSession` | Missing legacy type becomes `raw.type ?? 'chat'`. | C-MAIN requires raw.type; missing type fails regenerated validation, never defaults chat. Baseline optional shape must change first. | B T01/T12/T23/T24 |
| DEL-F08 | `src/store/session.ts::useSessionStore.enterWorkspaceChat` | Saved descriptor `id === '__pending'` treated as fresh; comment explicitly describes pointers written by older code. | Current writers already exclude transient `__pending` from saved real descriptors. Nothing for an old saved sentinel. Keep the live first-send sentinel distinct from this old saved-pointer branch. | B T01/T12/T23/T24 |
| DEL-F09–13 | `src/components/chat/useSelectSession.ts::selectSession`; `src/routes/_app/sessions.$sessionId.tsx::SessionRoute`; `src/store/session.ts::resolveRememberedSessionFromServer`; `src/components/search/SearchModal.tsx::bucketByAgent`; `src/components/chat/OmnipusRuntimeProvider.tsx::resolveLastActiveAgentId` | Five active_agent_id ?? agent_id owner readers. | C-MAIN required immutable agent_id in all five readers; DELETE handover/dual-owner fallback. | B T01/T18/T23/T26 |
| DEL-F14–18 | `src/lib/ws.ts::WsSubagentStartFrame`, `WsSubagentEndFrame`, `WsReplayMessageFrame`, `WsRateLimitFrame`, `WsToolApprovalRequiredFrame`, `WsSessionStateFrame`, `WsReceiveFrame`; `src/store/chat/slices/frames.ts::createFrameSlice`; `src/store/chat/slices/replay-and-status-frames.ts::handleReplayMessageFrame`, `handleTurnCanceledReplayEntry`, `handleReplayAndStatusFrame`; `src/store/toolApproval.ts::ToolApprovalStore`; `src/store/chat/types.ts::ChatStore.handleFrame` | Legacy Ws*Frame type aliases plus imports/casts in frame/replay/approval/chat stores. | Use generated SubagentStartFrame/SubagentEndFrame/ReplayMessageFrame/RateLimitFrame/ToolApprovalRequiredFrame/SessionStateFrame/ServerFrame; current one approval store remains. | K T14/T23 |
| DEL-F19 | `src/lib/ws.ts::WsConnectionCallbacks.onFrame`, `WsConnection._flushBatch`; `src/components/chat/OmnipusRuntimeProvider.tsx::WsLifecycle` | Explicitly legacy single-frame callback remains live: provider supplies `onFrame: handleFrame`; flush falls back to looping it. | `onFrames(batch)` callback is already defined and preferred by the transport. This is callback-shape reuse, not a new event schema. | B T12/T14/T23 |
| DEL-F20 | `src/lib/ws.ts::SessionCloseFrame`, `SessionCloseAckFrame` imports/re-exports; `src/store/chat/runtime-state.ts::SESSION_SCOPED_FRAME_TYPES` | Retained explicit-close type surface and live `session_close_ack` classification. No frontend sender found; explicit close retired. | Nothing for person-triggered explicit close; remove generated union/ack consumers via backend-owned specs. | B T13/T29 |
| DEL-F21 | `src/store/chat/slices/first-send-frames.ts::handleFirstSendFrame` | A `session_started` without `client_message_id` binds the known chat but retains unconfirmed first-send state and returns to the old reducer. | correlated `confirmFirstSend` path and `message_status` receipt path; **nothing** establishing a save from an old uncorrelated ack. | B T12/T15/T23/T24 |
| DEL-F22 | `src/store/chat/slices/frames.ts::createFrameSlice` (`session_started`) | Shared tail for “Kickoff and legacy acknowledgements”: old ordinary ack migrates the pending bucket/agent/mode without a correlated receipt. | ordinary `handleFirstSendFrame` / `confirmFirstSend`. Kickoff resolution is a separate current use of this tail, not retired by this compatibility removal. | B T12/T15/T23/T24 |
| DEL-F23 | `src/store/chat/first-send.ts::firstSendBlocksQueue`; `src/store/chat/store.ts::maybeDrainNext` | Explicit legacy exception lets a bound, unconfirmed chat stop blocking queued sends: `pending.status !== 'unconfirmed'` is part of the gate. | correlated save/recovery gating; **nothing** that confirms the legacy ack. | B T12/T15/T23/T24 |
| DEL-F24 | `src/store/chat/types.ts::OutboundQueueItem`; `src/store/chat/store.ts::drainQueuedMessage`; `src/components/chat/ChatScreen.tsx::ChatScreen` (`queuedMessages`) | String queue items retained for old persisted/test state are a live branch: drain sends an uncorrelated string; display filters strings out. | `QueuedOutboundMessage {id, content, timestamp}` branch preserves correlation and display. Nothing supplies the missing identity/time for an old string. | B T12/T15/T23/T24 |
| DEL-F25 | `src/store/chat/slices/frames.ts::createFrameSlice` (`done`, `isReplayTerminatorDone`) | Vestigial replay terminator is identified by `frames_emitted` with no tokens/cost. Still bakes stranded calls and drains. It does **not** currently finish catch-up. | `catch_up_complete` handles catch-up completion; `bakeToolCallsByOwner` is the bake primitive. Nothing in the read completion branch yet duplicates this terminator's stranded-call flush; do not invent it. | B T12/T15/T23/T24 |
| DEL-F26 | `src/hooks/useRunningActivity.ts::resolveSpanAgentId` | Pre-steered-session attribution workaround prefers originating delegate call's `params.agent_id` over `span.agentId`; the source comment says the old emitter gap was superseded. | Use stamped span.agentId from start/end reducer; no originating-params attribution workaround. | B T12/T23/T27 |
| DEL-F27 | `src/hooks/useRunningActivity.ts::activityStatusForSpan` | Explicit legacy fallback `default: return span.status`. | lifecycle-state mapping; unknown pre-state has no guessed span.status fallback. | B T12/T23/T27 |
| DEL-F28 | `src/components/chat/ActivityPanel.tsx::ActivityRow` | Explicit legacy dot/label fallback `getSpanStatusDot(item.status)` when lifecycle is absent. | getLifecycleStatusDot on known lifecycle only; no old dot/label before known state. | B T12/T23/T27 |
| DEL-F29 | `src/store/chat/slices/frames.ts::createFrameSlice` (`subagent_end`, `subagent_message`, `subagent_state`) | Full backwards message scans when span index misses; end handler explicitly labels this the legacy path. Message/state use the same compatibility lookup before their pending-update path. | `spanBySpanId` lookup; message/state already have `pendingSpanUpdatesBySpanId` for updates arriving before start. Nothing reconstructs a corrupt/missing index automatically here. | B T12/T23/T27 |
| DEL-F30 | `src/store/chat/slices/frames.ts::createFrameSlice` (`token`) | Legacy producer-less tokens retain permissive coalescing and guess `agentId` from `activeAgentId`; only stamped IDs split producers. | Actual frame.agent_id/message_id/turn_id routing; no guess for producer-less old frames. | B T12/T14/T23 |
| DEL-F31 | `src/store/chat/slices/frames.ts::createFrameSlice` (`done`, no-turn fallback) | Pre-#823 message-ID/last-message heuristic is retained when `turn_id` is missing or has no matching bubble; comments also identify a current no-stream fallback sharing it. | turn-ID bubble lookup. Nothing supplies absent turn identity; no new handler for the nonlegacy no-stream use is designed here. | B T12/T14/T23 |
| DEL-F32 | `src/store/chat/slices/replay-and-status-frames.ts::handleTurnCanceledReplayEntry` | Explicit legacy/undecorated cancellation entry with no `turn_id` logs and drops without correlation. | findAssistantMessageIdByTurnId for identified current turns; nothing reconstructs an unidentified old entry. | B T12/T14/T23 |
| DEL-F33 | `src/store/chat/session.ts::bakeToolCallsByOwner`; callers in `src/store/chat/slices/frames.ts::createFrameSlice` and `src/store/chat/slices/outbound-lifecycle.ts::clearStreamingState` | Legacy/unmapped owner uses fallback last message. Source also names live ring-buffer eviction as a reason ownership may be absent. | `toolCallOwnerMessageId` mapping; **nothing** for a genuinely absent/evicted owner. No replacement ownership policy is chosen. | B T12/T14/T23 |
| DEL-F34 | `src/lib/omnipus-runtime.ts::buildContentParts` | Live-call filter accepts undefined owner as the pre-tracking legacy path, instead of requiring this message to own it. | toolCallOwnerMessageId[id]===msg.id required for live-call ownership; no unidentified old call inclusion. | B T12/T14/T23 |
| DEL-F35 | `src/lib/omnipus-runtime.ts::pushHistoryParts`; `src/lib/messageParts.ts::splitMessageParts` | Explicit legacy no-offset rendering appends unknown-position calls after text (`Infinity` / `unpositioned`). Comments also cover current reconnects with a missing start snapshot. | recorded `textAtToolCallStart` / `PositionedToolCall.textOffset` interleaving; **nothing** for genuinely unknown position. | B T12/T14/T23 |
| DEL-F36 | `src/lib/truncation.ts::normalizeTruncationReason`; consumers `src/lib/api/sessions.ts::rawToMessage`, `src/store/chat/slices/replay-and-status-frames.ts::handleReplayMessageFrame`, `src/store/chat/slices/frames.ts::createFrameSlice` (`done`) | Explicit legacy default `reason ?? 'cancelled'` when truncated. | Explicit truncation_reason; no default cancelled for unexplained old truncated entry. | B T12/T14/T23 |
| DEL-F37 | `src/store/chat/slices/frames.ts::createFrameSlice` (`error`); `src/lib/llm-error.ts::sanitizeLegacyErrorMessage` | Legacy/no-typed-payload display falls back to sanitized `frame.message`. This shared path also serves current synthesized control/kickoff errors. | typed `payload.llm_error` + `getLLMErrorDisplay` for model errors; **nothing** replacing all current untyped control errors. Remove the old-model-error compatibility, not unrelated security/auth mechanisms by implication. | B T12/T14/T23 |
| DEL-F38 | `src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` (`replay_error`); `src/lib/llm-error.ts::readLLMErrorFromReplayFrame` | Legacy replay shape yields no typed error and displays `frame.message`. | typed replay-error display; no old untyped payload translation. | B T12/T14/T23 |
| DEL-F39–40 | `src/store/chat/types.ts::SessionChatState.goalStatus`, `ChatStore.goalStatus`; `src/store/chat/session.ts::emptySessionState`; `src/store/chat/store.ts::useChatStore`; `src/store/chat/cursor.ts::applySnapshotHistoryWipe`; `src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` (`goal_status`); `src/components/chat/ChatScreen.tsx::InlineThinkingIndicator`, `FallbackToolUI`, `VirtualAssistantMessageRow`; `src/lib/goalSetupState.ts::isGoalRecordEmpty` | Latest goalStatus scalar writers/initializers and thinking/error readers. | C-GOAL producer run/turn/message goal_id→goalPills/mergeGoalPillFrame with exact merged criteria, neutral if unknown; no scalar/latest selection. | B T12/T14/T23 |
| DEL-F41 | `src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` (`goal_status`); `src/lib/goalSetupState.ts::goalPillKey` | Missing/empty legacy `goal_id` goes into `_default`; a keyed frame additionally deletes that compatibility entry. | Explicit goal_id keys; no invented _default ID for unkeyed old frame. | B T12/T14/T23 |
| DEL-F42 | `src/components/chat/GoalPillTray.tsx::GoalPill`, `GoalPillTray`, `describePillState` | Live guards skip the retired goal state `queued`; type narrowing also excludes it. This is the deleted goal-confirm state, **not** an agent helper waiting for an execution slot. | Remove retired queued-goal type/guards, not execution-slot helper waiting. Current live goal branches remain; backend owns enum. | B T12/T14/T23 |
| DEL-F43 | `src/store/chat/slices/replay-and-status-frames.ts::handleReplayAndStatusFrame` (`judge_verdict`) | Legacy task/goal verdict with no session ID remains global/panel-only. The same optional-field branch also intentionally permits current plan-scope global verdicts. | Current session-addressed buildJudgeVerdictInsertion for task/goal; no guessed session for legacy verdict. Preserve current plan-scope global verdict. | B T12/T14/T23 |

## Success Criteria

| ID | Pass/fail evidence |
|---|---|
| SC-001 | Every proposed T family has executed passing evidence on CI at the candidate SHA; missing/unsupported live lane is not a pass. |
| SC-002 | Every logical DEL ID has its K/B compiler/source and applicable behavioral evidence; canonical positive controls pass. |
| SC-003 | Exact-SHA reachability campaign invokes required real user/agent surfaces; joint sidebar/backend integration passes. |

## Reachability

No new tool. Existing global ceiling plus sparse tightening overrides govern actual invocation, not metadata-only registration. Project gates are in `CLAUDE.md`; joint sidebar/main-backend integration must be tested on one candidate before landing. T30 executes this campaign.

| Invocation | Existing entry / observable proof |
|---|---|
| Main/extra/search/attention | E-MAIN/E-NAV: row main, sole row New chat, heartbeat-off protected main, retained search and unopened-main source/read updates. |
| Native helper | `pkg/tools/general_builtin_catalog.go::GeneralBuiltinMetadata`, `pkg/agent/loop_wire.go::registerDelegationTools`, `pkg/config/defaults.go::defaultToolPoliciesGeneral`: effective permitted omitted/explicit self; Deny control and real child link. |
| Report/guest return | Same catalog/defaults; `pkg/agent/session_messaging_wire.go::wireSessionMessagingForAgent`, E-MESSAGE: actual report/peer request, correlated source-owner send with guest author, no fake edge. |
| Task/Calendar/foreign Inbox | E-TASK/E-SCHEDULE/E-TASK-TARGET: canonical local execution, foreign assigned Inbox/creator status, rejected foreign start with zero writes, recipient local policy and correct run/result/segment links. |
| Stop/input/CLI | E-STOP/E-COMMAND/E-CLI: actual redirect/discard, main/extra clear/helper refusal, existing worker follow-up without task rerun and actual native CLI resume. |
| Activity/approval/goal | E-ACTIVITY/E-APPROVAL/E-GOAL: real count/usage/Open, one acting approval, producer-stamped own-run goal or neutral unknown. |
| Delete | E-DELETE: UI/tool/API same cleanup-first/record-last cascade; failure keeps row, ordinary Delete retry after restart. |

## User-facing documentation

Docs land with the behaviour change; docs-verifier audits. Navigation overlap coordinates with S-FE. Existing destinations/topics:

| ID | Page | Update topics |
|---|---|---|
| DOC-001 | `docs/agents.md` | Main/extra/Admin/worker, native self/depth/memory, external exclusion/resume, helper clear refusal, owned/guest deletion and same-action retry; remove handover/migration. |
| DOC-002 | `docs/using-omnipus-ui.md` | Row/mention/pair/search/commands; Stop discard, main/extra clear/history, Interrupted, activity/approval/run-goal labels, shared attention; partly-deleted row/Delete retry. |
| DOC-003 | `docs/workspaces.md` | Computed main/membership hide/re-add, Admin default, peer vs execution authority, shared seen; foreign assigned board Inbox/status-only/local pickup. |
| DOC-004 | `docs/calendar.md` | Modes/isolation/CONTINUE segment edits, recurrence vs current-run Stop, captured recipients/full-result limits, once/RRULE, foreign no-trigger delivery. |
| DOC-005 | `docs/connectors.md` | Shared-context privacy limit, correlated owner return/guest author/instance-thread/refusal, no transfer/fallback; Stop discards waiting input. |
| DOC-006 | `docs/settings.md` | Live ordinary limits/units/reload, model/report bounds, UTC/file-age/expiry/history, repeated idle/joined, Stop/restart loss; remove importer promises. |
| DOC-007 | `docs/reference/built-in-tools.md` | Generator-owned: regenerate existing generator; delegate/report/reply_to/pair/task args/results/limits/clear/native-external; remove handover/duplicate names; delete retry description. |
| DOC-008 | `docs/security.md` | Two-layer/inert retired settings, main-parent approval risk, native vs external/unattended, request-bound connector authority, UI confirmation/partial delete. |
| DOC-009 | `docs/tools.md` | Native self/other boundaries, refusal hint/ordinary limits/reply selector, canonical task scope/defaults/inert policies/foreign pickup, process terminal labels and deletion retry. |

## Traceability Matrix

| FR | Story | BDD | Proposed T |
|---|---|---|---|
| FR-002 | US1 | BDD-01.1, BDD-01.2, BDD-01.4 | T01, T24 |
| FR-003 | US1, US6 | BDD-01.3, BDD-06.4 | T17, T29 |
| FR-004 | US2 | BDD-02.1 | T02 |
| FR-005 | US2, US3 | BDD-02.1, BDD-02.2, BDD-03.1 | T02, T15 |
| FR-006 | US2, US9, US11 | BDD-02.1, BDD-09.2, BDD-11.1 | T02, T22, T13 |
| FR-007 | US2 | BDD-02.3 | T03 |
| FR-008 | US2, US3, US8 | BDD-02.4, BDD-03.1, BDD-08.3 | T15, T18 |
| FR-009 | US3 | BDD-03.1, BDD-03.2 | T15 |
| FR-010 | US3 | BDD-03.3 | T04 |
| FR-011 | US3 | BDD-03.4 | T05, T15 |
| FR-012 | US4 | BDD-04.1, BDD-04.2, BDD-04.4 | T06, T16 |
| FR-013 | US4, US10 | BDD-04.3, BDD-04.4, BDD-10.3 | T06, T16, T27 |
| FR-014 | US5 | BDD-05.1, BDD-05.3 | T07 |
| FR-015 | US5 | BDD-05.1, BDD-05.2, BDD-05.4 | T07 |
| FR-016 | US5 | BDD-05.3, BDD-05.5 | T07, T19, T31 |
| FR-017 | US6 | BDD-06.1, BDD-06.5 | T08, T17 |
| FR-018 | US6 | BDD-06.1, BDD-06.2 | T08, T17 |
| FR-019 | US6 | BDD-06.3, BDD-06.4 | T17 |
| FR-020 | US6, US10 | BDD-06.3, BDD-06.1, BDD-06.4, BDD-10.1 | T17, T27 |
| FR-022 | US7 | BDD-07.1 | T09, T25 |
| FR-023 | US7, US3 | BDD-07.2, BDD-03.2, BDD-05.6, BDD-07.3 | T09, T15, T19, T25 |
| FR-024 | US7, US9 | BDD-07.3, BDD-09.4, BDD-04.2 | T09, T16, T22, T25 |
| FR-025 | US7 | BDD-07.4 | T20 |
| FR-027 | US8 | BDD-08.1, BDD-08.9 | T18, T26 |
| FR-028 | US8 | BDD-08.3, BDD-08.4, BDD-08.9 | T10, T18, T26 |
| FR-029 | US9 | BDD-09.1 | T11, T24 |
| FR-030 | US9 | BDD-09.2, BDD-09.5 | T22, T24, T25 |
| FR-031 | US9, US12 | BDD-09.1, BDD-09.3, BDD-12.1, BDD-09.5 | T11, T23, T28 |
| FR-032 | US9 | BDD-09.4 | T22, T25 |
| FR-033 | US10 | BDD-10.1, BDD-10.3, BDD-07.1 | T12, T27, T25 |
| FR-034 | US10, US6 | BDD-10.2, BDD-06.2 | T17, T27 |
| FR-035 | US1, US10 | BDD-01.4, BDD-10.3, BDD-10.4 | T01, T27, T28 |
| FR-036 | US11 | BDD-11.1, BDD-11.4 | T13, T29 |
| FR-037 | US11 | BDD-11.2, BDD-11.3, BDD-11.5 | T21, T29 |
| FR-038 | US12, US9, US11 | BDD-12.1, BDD-12.3, BDD-09.3, BDD-11.4, BDD-12.2, BDD-12.5 | T11, T23, T14 |
| FR-039 | US12 | BDD-12.5 | T12, T14, T23 |
| FR-043 | US5 | BDD-05.5, BDD-05.6, BDD-09.5 | T19, T31 |
| FR-045 | US8, US9 | BDD-08.1, BDD-08.2, BDD-09.1 | T18, T24, T26, T10 |
| FR-046 | US8, US12 | BDD-08.5, BDD-08.10, BDD-12.1, BDD-12.3 | T34, T23 |
| FR-047 | US13 | BDD-13.1, BDD-13.2, BDD-13.3, BDD-13.4 | T32 |
| FR-048 | US8 | BDD-08.5, BDD-08.6, BDD-08.8 | T34, T17 |
| FR-049 | US8 | BDD-08.7 | T34 |

T30 → Reachability (US12). DOC topics map directly to affected behaviour; deletion proof/test links live in inventory cells.

## Final ID map

This preserves traceability after the mechanical cut; no identifier is silently lost. Behaviour/shape supersession for deletion follows final Q1=A; policy rewriting is removed by D2.

| Original ID(s) | Final home |
|---|---|
| FR-001 | Contract Changes intro; BDD-12.3→FR-038. |
| FR-021 | FR-020; original independence/delivery cases retained. |
| FR-026 | FR-045; original peer/address cases retained. |
| FR-039 shared-branch first sentence | FR-038; own-run goal stays FR-039. |
| FR-040 | Project constraints in CLAUDE.md; no-hot-main clause→FR-005. |
| FR-041 | User-facing documentation intro. |
| FR-042, BDD-12.4, D16 | Reachability/T30; joint candidate test retained, gate boilerplate delegated to project rules. |
| A04/A06/A08/A09/A10 | BDD-03.3: every original under/at/over and zero boundary included. |
| B07/B09/B11 | BDD-02.3/07.3/07.1. |
| C06/C07/C08/C10 | BDD-06.1/06.5/06.3/06.3. |
| C12/C13/C14/C15/C16/C17 | BDD-05.1–05.4/08.2/08.1/08.3/08.4+06.4/05.5+05.6. |
| D01–D05/D07/D08 | BDD-01.1+01.4/09.1/09.3+09.5/09.4/10.1/10.3/10.4. |
| D11/D12/D13/D14/D15 | BDD-11.2+11.3/11.5/12.1/12.2+12.5/12.3; D12 recovery updated to record-last order. |
| E01–E07 | BDD-13.1–13.4; no-source/default Admin, replay, denied-tool cases retained. |
| E12/E14/E16/E18/E19 | BDD-08.5/08.5+12.1/08.5+08.6/08.7/08.8. |
| R01/R02/R03/R04/R05/R06/R07 | BDD-08.10/08.9/06.5/09.5/07.3/11.5/12.5; R06 replaces superseded scope/isolation crash points with existing cascade-step/record failure and restart retry. |
| DEL-22/24 | One row; both IDs/all symbols/callers survive. |
| DEL-F09–13 / DEL-F14–18 / DEL-F39–40 | One row per group; all original IDs/sources preserved. |
| Old SC-001–013 | FR/BDD/T coverage; final SC-001–003 summarizes executed tests/deletion evidence/reachability. |
| HE1–HE7 | Duplicate BDD families 01/06/08/07/03/02/07+01.3; optional QA campaign outlines, not separate requirements. |

## Open questions

None new. Previously retired: FR-044, US14, BDD-14.1–14.4, T33, SC-011. Original/R1/final founder answers live in the ADR decision log.

skills: omnipus-shared-rules, plan-spec, gitnexus-exploring, ux-heuristics-review, omnipus-design-system

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Final corrections follow founder answers | ADR::Dated decision log, final Q1=A/Q2=A/D2/D3; Opus R2 report::Cut plan and defects. | Verified instructions; implementation untested |
| Existing deletion/policy sources contradict the removed machinery | `pkg/sysagent/tools/agent.go::deleteAndCascade`, `pkg/agentstore/state.go::DeleteState`, `pkg/config/validate.go::ReconcileToolPolicyCeiling` direct reads. | Verified source, target adaptation not implemented |
| Cut preserves coverage and portable citations | `python3 -I build/session-core-evidence/final-cut-r2/verify_cut.py`, exit 0: active IDs/deletion expansions/case manifest/source references and injected-negative checks; `git diff --check`, exit 0. | Document evidence only; receipt in final hand-off |
| **Self-check** | Reread final diff against all 34 cut actions, D1–D4 and retained rule/shape/boundary manifests; reran checks at final SHA and verified pushed clean branch. ADR has only contradiction corrections; no production edits, implementation green or third grill claimed. | Document verification; runtime Unknown |
