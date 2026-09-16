# Size-budget grandfather lists, seeded 2026-09-15

**Status:** the named seed for the two shrink-only lists the budget gates read (`scripts/budgets/files.txt` and `scripts/budgets/functions.txt`, see `draft-module-map.md`, "How we enforce it"). Measured on `release/v0.1.1` at `d9a0c6941` with the scanners in `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/`. When the gates are built, they are seeded from this file and this file becomes history.

**Rule (founder, 2026-09-15):** file warns over 2,000 lines, fails over 4,000. Function warns over 120, fails over 240. React component warns at both, never fails. Same numbers for production and test code. Entries below may only shrink; a new file or function must be under the fail line.

**Component classification here** is the report heuristic: a `.tsx` file and a name starting with an uppercase letter. The gate's rule is stricter (the body must return JSX, looking through `memo` and `forwardRef`); expect a handful of moves between the component and function lists when the real scanner runs. Anonymous callbacks are named `(arg of X)` after the call they are passed to.

## After a file split, re-key the grandfather rows

Both lists are keyed by exact address — `path<TAB>lines` in `scripts/budgets/files.txt`, `file<TAB>qualified name<TAB>lines` in `scripts/budgets/functions.txt`. The function list has one fallback: a row also matches its function at any sibling file in the same package directory, keeping the listed number. Nothing else follows a move. A split therefore breaks the keying in two directions, and only one of them fails loud:

| What the split did | What the gate does | Ratchet |
|---|---|---|
| Item lands at an address no row matches (function moved to another directory, extracted function over 240, new file over 4,000) | FAIL, on the next run | Tight — the failure is doing its job |
| Row's address no longer exists | Nothing. Dead rows are never reported. | The row guards nothing |
| Item shrank but its row kept the pre-split number | Nothing, until the item grows past that number | Loose — it can grow back to its pre-split size with no FAIL |

So re-keying is a manual step in every split: point each moved row at its new address and lower its number to the re-measured count (for a file now under 4,000, deleting the row is tighter still). Re-pointing a row that moved is not widening. Adding a row for something that was not grandfathered before the split is widening, and is forbidden: an extracted function or a new file must come in under the fail line (240 / 4,000). One extra trap in the function list: the directory fallback keeps the largest number among same-named rows in a directory, so a stale row can inflate the ceiling of a different function with the same name.

The ratchet is shrink-only for the same reason the lists exist: every number is a snapshot of debt already present at seed time. Numbers move down and rows get deleted; they are never raised and never added. That is what makes the lists a retirement plan rather than a license — a split that could re-key numbers upward, or mint rows for its own output, would reset the debt instead of paying it down.

Worked precedents. `scripts/check-greenfield-providers.sh` (the `GENERIC_EXEMPT_LINES` dict) is keyed by path; the 2026-09-15 split of `pkg/config/config.go` moved its exact exempted lines into per-area files, the guard passed before the split and failed after it purely because of the path keying, and the rows were re-keyed — same lines, same count, new paths. On the budget lists themselves, compare this file with `scripts/budgets/functions.txt`: `handleFrame` was seeded here at 2,775 lines in `src/store/chat.ts` and is listed there at 1,773 in `src/store/chat/slices/frames.ts` — address moved, number lowered, which is what a correct re-key looks like.

## Summary

| List | Entries | Effect |
|---|---|---|
| Files over 4,000 (production) | 10 | grandfathered, may only shrink |
| Files over 4,000 (tests) | 0 | grandfathered |
| Go production functions over 240 | 70 | grandfathered |
| Go test functions over 240 | 20 | grandfathered |
| TS production functions over 240, not components | 13 | grandfathered |
| TS test functions over 240 | 36 | grandfathered |
| React components over 240 | 70 | warned on every PR, never fail |
| Files 2,001 to 4,000 (production / tests) | 24 / 16 | warned |

## Files over 4,000 lines (grandfathered)

| Lines | File |
|---|---|
| 16515 | `pkg/agent/loop.go` |
| 11338 | `pkg/gateway/rest.go` |
| 6695 | `src/store/chat.ts` |
| 6645 | `pkg/gateway/gateway.go` |
| 6221 | `pkg/gateway/websocket.go` |
| 6130 | `pkg/agent/plan_engine.go` |
| 5939 | `src/lib/api.ts` |
| 5278 | `pkg/config/config.go` |
| 4167 | `pkg/tools/browser/manager.go` |
| 4145 | `pkg/tools/delegate.go` |

Test files over 4,000: 0 after skipping `pkg/api/generated/`.

## Go production functions over 240 (grandfathered)

| Lines | Function | Location |
|---|---|---|
| 4364 | `AgentLoop.runTurn` | `pkg/agent/loop.go:9740` |
| 1489 | `spawnSubTurn` | `pkg/agent/subturn.go:632` |
| 1346 | `registerSharedTools` | `pkg/agent/loop.go:2414` |
| 1258 | `setupAndStartServices` | `pkg/gateway/gateway.go:4370` |
| 1231 | `WSHandler.eventForwarder` | `pkg/gateway/websocket.go:4000` |
| 1090 | `RunContextWithOptions` | `pkg/gateway/gateway.go:2134` |
| 1060 | `coreAgentSeed` | `pkg/coreagent/core.go:798` |
| 1041 | `restAPI.HandleProviders` | `pkg/gateway/rest.go:6439` |
| 1003 | `DefaultConfig` | `pkg/config/defaults.go:52` |
| 999 | `restAPI.updateAgent` | `pkg/gateway/rest.go:3435` |
| 687 | `streamReplay` | `pkg/gateway/replay.go:75` |
| 595 | `WSHandler.handleChatMessage` | `pkg/gateway/websocket.go:1711` |
| 587 | `restAPI.handleTaskPatch` | `pkg/gateway/rest_tasks.go:2069` |
| 584 | `AgentLoop.RequestCancel` | `pkg/agent/cancel.go:253` |
| 534 | `restAPI.createAgent` | `pkg/gateway/rest.go:2663` |
| 504 | `applySandbox` | `pkg/gateway/sandbox_apply.go:240` |
| 500 | `restAPI.HandleCompleteOnboarding` | `pkg/gateway/rest_onboarding.go:429` |
| 427 | `NewAgentLoop` | `pkg/agent/loop.go:1451` |
| 409 | `restAPI.handleWorkspacePut` | `pkg/gateway/rest_workspaces.go:992` |
| 397 | `TaskUpdateTool.Execute` | `pkg/tools/task.go:1683` |
| 392 | `TaskUpdateTool.Execute` | `pkg/sysagent/tools/task.go:813` |
| 389 | `restAPI.HandleUpload` | `pkg/gateway/rest.go:10618` |
| 386 | `WSHandler.handleAttachSession` | `pkg/gateway/websocket.go:2907` |
| 383 | `AgentLoop.runRecap` | `pkg/agent/session_end.go:287` |
| 378 | `AgentLoop.runVerifierAdjudication` | `pkg/agent/verifier_adjudication.go:1328` |
| 375 | `restAPI.setChannelRouting` | `pkg/gateway/rest.go:9224` |
| 373 | `restAPI.updateAgentTools` | `pkg/gateway/rest.go:8404` |
| 367 | `AgentLoop.runGoalAdjudication` | `pkg/agent/goal_triggers.go:765` |
| 354 | `NewAgentInstance` | `pkg/agent/instance.go:151` |
| 345 | `MessageParentTool.Execute` | `pkg/tools/message_parent.go:383` |
| 341 | `DelegateTool.executeAsync` | `pkg/tools/delegate.go:1906` |
| 341 | `AgentLoop.processMessage` | `pkg/agent/loop.go:8360` |
| 337 | `LiveView.applyViewportAdmitted` | `pkg/tools/browser/live.go:989` |
| 325 | `findRecords` | `pkg/records/knowledgefind/find.go:1040` |
| 314 | `TaskCreateTool.Execute` | `pkg/tools/task.go:1116` |
| 311 | `Store.updateLocked` | `pkg/plan/store.go:330` |
| 310 | `renderSeatbeltProfile` | `pkg/sandbox/seatbelt_profile.go:128` |
| 310 | `Store.updateLocked` | `pkg/task/store.go:1164` |
| 308 | `SetGoalTool.Execute` | `pkg/tools/set_goal.go:416` |
| 307 | `restAPI.putSandboxConfig` | `pkg/gateway/rest_sandbox_config.go:152` |
| 303 | `RunWithOptions` | `pkg/vaultimport/run.go:272` |
| 302 | `Manager.Reload` | `pkg/channels/manager.go:1745` |
| 301 | `TaskCreateTool.Execute` | `pkg/sysagent/tools/task.go:422` |
| 301 | `restAPI.handleWorkspaceDelete` | `pkg/gateway/rest_workspaces.go:1402` |
| 299 | `RecallConversationTool.Execute` | `pkg/agent/recall_conversation.go:189` |
| 292 | `ValidateViewAgainstSchemas` | `pkg/records/view.go:1200` |
| 288 | `validateBootConfig` | `pkg/config/validator.go:274` |
| 285 | `AgentLoop.applyGoalCommandPrompt` | `pkg/agent/goal_loop.go:70` |
| 284 | `Run` | `cmd/omnipus/internal/run/run.go:139` |
| 284 | `wsStreamer.Finalize` | `pkg/gateway/websocket.go:5870` |
| 278 | `WSHandler.readLoop` | `pkg/gateway/websocket.go:1271` |
| 277 | `execPathCaches.resolve` | `pkg/tools/browser/exec_resolver.go:472` |
| 277 | `runExternalCLISubTurn` | `pkg/agent/external_dispatch.go:143` |
| 277 | `AgentLoop.checkGoalLoopAfterTurn` | `pkg/agent/goal_loop.go:1185` |
| 277 | `DelegateTool.executeRun` | `pkg/tools/delegate.go:1424` |
| 277 | `AgentCreateTool.Execute` | `pkg/sysagent/tools/agent.go:185` |
| 271 | `restAPI.runExecutorSmokeTest` | `pkg/gateway/rest_executor_smoketest.go:302` |
| 270 | `ResolvePath` | `pkg/tools/resolvepath.go:902` |
| 270 | `StartTestGateway` | `pkg/agent/testutil/gateway_harness.go:308` |
| 262 | `AgentLoop.JudgeCriteria` | `pkg/agent/judge.go:309` |
| 260 | `restartServices` | `pkg/gateway/gateway.go:5994` |
| 260 | `restAPI.runProviderDelete` | `pkg/gateway/rest_providers_delete.go:170` |
| 257 | `AgentDeleteTool.Execute` | `pkg/sysagent/tools/agent.go:790` |
| 255 | `DelegateTool.executeRespond` | `pkg/tools/delegate.go:3442` |
| 254 | `restAPI.setAgentMailbox` | `pkg/gateway/rest_mailbox.go:212` |
| 254 | `restAPI.HandleOnboardingProbeProvider` | `pkg/gateway/rest_onboarding.go:1231` |
| 250 | `SeedConfig` | `pkg/coreagent/core.go:2533` |
| 249 | `restAPI.registerAdditionalEndpoints` | `pkg/gateway/rest.go:5727` |
| 243 | `AgentLoop.windowTrimForce` | `pkg/agent/loop.go:14716` |
| 241 | `MCPAddTool.Execute` | `pkg/sysagent/tools/mcp.go:92` |

## Go test functions over 240 (grandfathered)

| Lines | Function | Location |
|---|---|---|
| 396 | `TestOccurrences_BucketingAndCaps` | `pkg/gateway/task_occurrences_test.go:187` |
| 396 | `TestExecCommandInjection_WorkspaceRestriction` | `tests/security/command_injection_test.go:344` |
| 386 | `TestTriggerScheduler_RruleRearmAllPaths` | `pkg/agent/task_trigger_rrule_test.go:89` |
| 384 | `TestRestTasks_OccurrencesEndpoint` | `pkg/gateway/rest_tasks_occurrences_test.go:441` |
| 377 | `TestChannelConfig_AllRefsRoundTrip` | `pkg/config/config_refs_test.go:27` |
| 373 | `TestOccurrenceOverlay` | `pkg/gateway/task_occurrences_overlay_test.go:35` |
| 345 | `TestFormula_AbsencePropagatesAndArithmeticIsExact` | `pkg/records/formula_eval_test.go:91` |
| 323 | `TestGoalClarify_WebCardRoundtrip` | `pkg/agent/goal_flow_integration_test.go:209` |
| 309 | `TestRotationBySizeAndDaily` | `pkg/audit/rotation_test.go:36` |
| 293 | `TestVerifierAntiPatterns` | `pkg/agent/verifier_antipatterns_adr052_qa_test.go:43` |
| 290 | `TestWebRTCEndToEndInProcess` | `pkg/gateway/browser_webrtc_e2e_test.go:428` |
| 289 | `TestSSRFMatrix` | `tests/security/ssrf_matrix_test.go:63` |
| 286 | `TestValidateToolArgs` | `pkg/tools/validate_test.go:15` |
| 283 | `TestProbeProvider_SignIn` | `pkg/gateway/rest_onboarding_probe_test.go:718` |
| 270 | `TestAuditLogger_WriteAndRotate` | `pkg/audit/logger_test.go:26` |
| 268 | `TestProbeProviderID_Validation` | `pkg/gateway/rest_onboarding_probe_test.go:269` |
| 264 | `TestLoad2000Sessions` | `tests/perf/load_2000_sessions_test.go:90` |
| 258 | `TestKnowledge_NoLanguageModelInTheGraphPath` | `pkg/knowledge/links_test.go:712` |
| 247 | `TestEnum_ClosedAndLexical` | `pkg/records/enum_test.go:44` |
| 246 | `TestSetGoalTool_ValidatesAndWrites` | `pkg/tools/set_goal_test.go:84` |

## TypeScript production functions over 240, not components (grandfathered)

| Lines | Function | Location |
|---|---|---|
| 4568 | `(arg of create)` | `src/store/chat.ts:2097` |
| 2775 | `handleFrame` | `src/store/chat.ts:3888` |
| 925 | `useSlashMenu` | `src/hooks/useSlashMenu.ts:260` |
| 594 | `(arg of useEffect)` | `src/components/library/preview/LibraryPdfPreview.tsx:634` |
| 488 | `useAutoSave` | `src/hooks/useAutoSave.ts:152` |
| 428 | `sendMessage` | `src/store/chat.ts:3080` |
| 411 | `(anonymous)` | `src/components/library/preview/LibraryPdfPreview.tsx:787` |
| 397 | `(arg of create)` | `src/store/session.ts:304` |
| 365 | `(arg of useEffect)` | `src/components/browser/BrowserLiveView.tsx:985` |
| 355 | `useLibraryFileEditor` | `src/components/library/preview/useLibraryFileEditor.ts:173` |
| 337 | `(arg of withBucket)` | `src/store/chat.ts:5832` |
| 335 | `(arg of produce)` | `src/store/chat.ts:5833` |
| 284 | `(arg of useEffect)` | `src/components/browser/BrowserLiveView.tsx:1578` |

## TypeScript test functions over 240 (grandfathered)

| Lines | Function | Location |
|---|---|---|
| 1300 | `(arg of describe)` | `src/hooks/useAutoSave.test.tsx:13` |
| 857 | `(arg of describe)` | `src/store/chat.tool-call-offset.test.ts:71` |
| 458 | `(arg of describe)` | `src/components/chat/mermaid-renderer.test.tsx:126` |
| 456 | `(arg of describe)` | `src/lib/api.test.ts:1005` |
| 453 | `(arg of describe)` | `src/components/workspaces/WorkspaceTeamTab.test.tsx:202` |
| 441 | `(arg of describe)` | `src/hooks/useSlashMenu.test.ts:1306` |
| 415 | `(arg of describe)` | `src/components/library/preview/BasePreview.test.tsx:159` |
| 413 | `(arg of describe)` | `src/components/library/knowledge/KnowledgeNoteView.test.tsx:426` |
| 402 | `(arg of describe)` | `src/components/agents/AgentProfile.test.tsx:926` |
| 392 | `(arg of describe)` | `src/store/chat.test.ts:349` |
| 376 | `(arg of describe)` | `src/components/providers/provider-picker-model.test.ts:55` |
| 360 | `(arg of describe)` | `src/components/library/knowledge/KnowledgeBacklinks.test.tsx:126` |
| 344 | `(arg of describe)` | `src/components/chat/ChatScreen.virtualization.test.tsx:275` |
| 329 | `(arg of describe)` | `src/store/chat.test.ts:1096` |
| 321 | `(arg of describe)` | `src/components/settings/PerformanceSection.goal.test.tsx:80` |
| 316 | `(arg of describe)` | `src/components/settings/MemorySection.test.tsx:76` |
| 312 | `(arg of describe)` | `src/components/browser/BrowserLiveView.webrtcInputRouting.test.tsx:213` |
| 312 | `(arg of describe)` | `src/lib/api.test.ts:365` |
| 311 | `(arg of describe)` | `src/components/settings/SandboxSection.test.tsx:715` |
| 304 | `(arg of describe)` | `src/components/connectors/EmailMailboxPanel.test.tsx:486` |
| 286 | `(arg of describe)` | `src/components/settings/PerformanceSection.test.tsx:329` |
| 285 | `(arg of describe)` | `src/components/chat/GoalEchoCard.test.tsx:39` |
| 285 | `(arg of describe)` | `src/components/screens/UsageScreen.test.tsx:128` |
| 282 | `(arg of describe)` | `src/components/chat/ChatScreen.tool-order.test.tsx:292` |
| 276 | `(arg of describe)` | `src/test/contract.test.ts:27` |
| 274 | `(arg of describe)` | `src/components/providers/SignInDialog.test.tsx:158` |
| 274 | `(arg of describe)` | `src/routes/_app/-sessions.$sessionId.workspace.test.tsx:147` |
| 257 | `(arg of describe)` | `src/components/library/LibraryExplorer.test.tsx:1131` |
| 257 | `(arg of describe)` | `src/routes/_app/-library.test.tsx:97` |
| 256 | `(arg of describe)` | `src/components/library/LibraryPanel.test.tsx:72` |
| 256 | `(arg of describe)` | `src/components/settings/PerformanceSection.test.tsx:72` |
| 255 | `(arg of describe)` | `src/store/chat.test.ts:1671` |
| 251 | `(arg of describe)` | `src/components/browser/BrowserLiveView.controlToggle.test.tsx:108` |
| 247 | `(arg of describe)` | `src/components/chat/AskUserQuestionCard.test.tsx:39` |
| 247 | `(arg of describe)` | `src/components/ui/model-selector.test.tsx:338` |
| 245 | `(arg of describe)` | `src/components/library/preview/knowledgeMarkdown.test.tsx:458` |

## React components over 240 (warn only, never fail)

| Lines | Component | Location |
|---|---|---|
| 2789 | `AgentProfile` | `src/components/agents/AgentProfile.tsx:129` |
| 2770 | `BrowserLiveView` | `src/components/browser/BrowserLiveView.tsx:360` |
| 1447 | `LibraryPdfPreview` | `src/components/library/preview/LibraryPdfPreview.tsx:526` |
| 1300 | `LibraryExplorer` | `src/components/library/LibraryExplorer.tsx:235` |
| 1214 | `OmnipusComposer` | `src/components/chat/ChatScreen.tsx:2092` |
| 1170 | `TaskDetailPanel` | `src/components/workspaces/TaskDetailPanel.tsx:116` |
| 1049 | `SandboxSection` | `src/components/settings/SandboxSection.tsx:293` |
| 814 | `Sidebar` | `src/components/layout/Sidebar.tsx:68` |
| 759 | `ModelSelector` | `src/components/ui/model-selector.tsx:214` |
| 745 | `ChannelConfigPanel` | `src/components/skills/ChannelConfigPanel.tsx:403` |
| 738 | `CreateTaskSlideOver` | `src/components/workspaces/CreateTaskSlideOver.tsx:134` |
| 730 | `ProvidersSection` | `src/components/settings/ProvidersSection.tsx:634` |
| 699 | `BasePreview` | `src/components/library/preview/BasePreview.tsx:292` |
| 695 | `LibrarySearchBar` | `src/components/library/search/LibrarySearchBar.tsx:616` |
| 618 | `SearchModal` | `src/components/search/SearchModal.tsx:394` |
| 605 | `SecuritySection` | `src/components/settings/SecuritySection.tsx:172` |
| 604 | `CalendarEventSlideOver` | `src/components/calendar/CalendarEventSlideOver.tsx:176` |
| 568 | `McpServerModal` | `src/components/skills/McpServerModal.tsx:171` |
| 560 | `CalendarScreen` | `src/components/screens/CalendarScreen.tsx:59` |
| 547 | `PerformanceSection` | `src/components/settings/PerformanceSection.tsx:105` |
| 521 | `EmailMailboxPanel` | `src/components/connectors/EmailMailboxPanel.tsx:305` |
| 498 | `WorkspaceTeamTab` | `src/components/workspaces/WorkspaceTeamTab.tsx:62` |
| 497 | `WorkspaceSettingsTab` | `src/components/workspaces/WorkspaceSettingsTab.tsx:57` |
| 496 | `SkillBrowser` | `src/components/skills/SkillBrowser.tsx:132` |
| 492 | `GatewaySection` | `src/components/settings/GatewaySection.tsx:90` |
| 485 | `OnboardingWizard` | `src/routes/onboarding.tsx:252` |
| 475 | `ProviderPicker` | `src/components/providers/ProviderPicker.tsx:160` |
| 461 | `CreatePlanSlideOver` | `src/components/workspaces/CreatePlanSlideOver.tsx:143` |
| 439 | `MemorySection` | `src/components/settings/MemorySection.tsx:144` |
| 438 | `SkillsScreen` | `src/components/screens/SkillsScreen.tsx:55` |
| 433 | `SignInDialog` | `src/components/providers/SignInDialog.tsx:124` |
| 416 | `ConnectorsScreen` | `src/components/screens/ConnectorsScreen.tsx:864` |
| 414 | `ProviderConfigSheet` | `src/components/settings/ProvidersSection.tsx:215` |
| 407 | `ToolApprovalCard` | `src/components/agents/ToolApprovalModal.tsx:157` |
| 405 | `AgentsLibraryView` | `src/components/screens/AgentListScreen.tsx:140` |
| 395 | `VirtualAssistantMessageRow` | `src/components/chat/ChatScreen.tsx:1236` |
| 393 | `EditableValueCell` | `src/components/library/preview/viewparts/RecordFieldEditor.tsx:303` |
| 381 | `ProviderStep` | `src/routes/onboarding.tsx:1059` |
| 379 | `CreateAgentWizard` | `src/components/agents/CreateAgentWizard.tsx:215` |
| 353 | `ProfileSection` | `src/components/settings/ProfileSection.tsx:60` |
| 352 | `WorkspaceTasksTab` | `src/components/workspaces/WorkspaceTasksTab.tsx:60` |
| 336 | `AskUserQuestionCard` | `src/components/chat/AskUserQuestionCard.tsx:62` |
| 330 | `CalendarToolbar` | `src/components/calendar/CalendarToolbar.tsx:54` |
| 328 | `KnowledgeNoteView` | `src/components/library/knowledge/KnowledgeNoteView.tsx:444` |
| 327 | `LibraryAddMountDialog` | `src/components/library/LibraryAddMountDialog.tsx:97` |
| 326 | `AgentPicker` | `src/components/chat/composer/AgentPicker.tsx:45` |
| 323 | `ToolsAndPermissions` | `src/components/agents/ToolsAndPermissions.tsx:82` |
| 319 | `KnowledgeBacklinks` | `src/components/library/knowledge/KnowledgeBacklinks.tsx:172` |
| 318 | `WorkspaceGraphTab` | `src/components/workspaces/WorkspaceGraphTab.tsx:63` |
| 312 | `AcceptanceCriteriaEditor` | `src/components/workspaces/AcceptanceCriteriaEditor.tsx:86` |
| 301 | `GenericToolCall` | `src/components/chat/tools/GenericToolCall.tsx:324` |
| 295 | `DataSection` | `src/components/settings/DataSection.tsx:26` |
| 292 | `ContextSection` | `src/components/settings/ContextSection.tsx:269` |
| 283 | `GraphViewInner` | `src/components/workspaces/graph/GraphView.tsx:128` |
| 280 | `GodModeControl` | `src/components/settings/GodModeControl.tsx:47` |
| 277 | `RelationCellEditor` | `src/components/library/preview/viewparts/RecordFieldEditor.tsx:730` |
| 277 | `UsageScreen` | `src/components/screens/UsageScreen.tsx:246` |
| 274 | `ExecutorInputs` | `src/components/agents/wizard/Step1Identity.tsx:442` |
| 273 | `ChatScreen` | `src/components/chat/ChatScreen.tsx:3335` |
| 273 | `LibraryEntryRow` | `src/components/library/LibraryEntryRow.tsx:67` |
| 273 | `AgentNode` | `src/components/workspaces/team/WorkspaceTeamGraph.tsx:115` |
| 269 | `CreateChannelSheet` | `src/components/screens/ConnectorsScreen.tsx:410` |
| 267 | `AppShell` | `src/components/layout/AppShell.tsx:25` |
| 265 | `RecurrenceEditor` | `src/components/calendar/RecurrenceEditor.tsx:149` |
| 260 | `DiagnosticsSection` | `src/components/settings/DiagnosticsSection.tsx:67` |
| 251 | `KnowledgeEmptyState` | `src/components/library/knowledge/KnowledgeEmptyState.tsx:201` |
| 251 | `KnowledgeMarkdownLink` | `src/components/library/preview/knowledgeMarkdown.tsx:1990` |
| 249 | `LoginScreen` | `src/routes/login.tsx:15` |
| 242 | `ProviderDetailPanel` | `src/components/providers/ProviderDetailPanel.tsx:189` |
| 242 | `TaskCard` | `src/components/workspaces/TaskCard.tsx:148` |

## Files between 2,001 and 4,000 lines (warned)

Production:

| Lines | File |
|---|---|
| 3992 | `pkg/coreagent/core.go` |
| 3616 | `pkg/tools/browser/live.go` |
| 3607 | `src/components/chat/ChatScreen.tsx` |
| 3489 | `pkg/gateway/rest_tasks.go` |
| 3279 | `pkg/vaultimport/infer.go` |
| 3211 | `src/components/agents/AgentProfile.tsx` |
| 3177 | `pkg/knowledge/index.go` |
| 3129 | `src/components/browser/BrowserLiveView.tsx` |
| 2736 | `pkg/tools/shell.go` |
| 2718 | `pkg/agent/turn.go` |
| 2696 | `pkg/agent/task_executor.go` |
| 2605 | `pkg/agent/subturn.go` |
| 2487 | `pkg/gateway/browser_ws.go` |
| 2410 | `src/components/library/preview/knowledgeMarkdown.tsx` |
| 2384 | `pkg/session/unified.go` |
| 2328 | `pkg/vaultimport/view_write.go` |
| 2272 | `pkg/agent/verifier_adjudication.go` |
| 2256 | `pkg/tools/task.go` |
| 2216 | `pkg/agent/goal_triggers.go` |
| 2180 | `pkg/channels/manager.go` |
| 2141 | `pkg/gateway/rest_library.go` |
| 2125 | `pkg/gateway/rest_workspaces.go` |
| 2116 | `pkg/records/knowledgefind/find.go` |
| 2051 | `pkg/knowledge/knowledge_edit.go` |

Tests:

| Lines | File |
|---|---|
| 3781 | `pkg/agent/loop_test.go` |
| 3383 | `src/store/chat.test.ts` |
| 3241 | `src/lib/api.test.ts` |
| 3194 | `src/components/agents/AgentProfile.test.tsx` |
| 3040 | `pkg/channels/manager_test.go` |
| 2520 | `pkg/gateway/rest_test.go` |
| 2482 | `pkg/agent/tool_manifest_test.go` |
| 2407 | `pkg/agent/subturn_test.go` |
| 2393 | `src/components/skills/ChannelConfigPanel.test.tsx` |
| 2305 | `pkg/gateway/rest_auth_test.go` |
| 2273 | `pkg/task/coverage_test.go` |
| 2193 | `pkg/config/config_test.go` |
| 2140 | `pkg/tools/task_test.go` |
| 2115 | `src/components/settings/ProvidersSection.test.tsx` |
| 2070 | `pkg/agent/steering_test.go` |
| 2034 | `pkg/gateway/rest_onboarding_test.go` |
