# Function-length baseline (120-line rule), 2026-09-15

**Status:** measurement, not a decision. Input to `draft-module-map.md` (the file-budget ratchet and the "one function, one job" rule).
**Tree:** `release/v0.1.1` at `1f996b01d`, after the library-improvements merge `ff11e8249`.
**Rule measured:** a function is over budget when its span (from the `func` keyword or signature to the closing brace, inclusive) exceeds 120 lines. This is the `funlen` line threshold in `.golangci.yaml` line 97. That linter is currently disabled (line 61), so nothing enforces it today.

## How it was measured

- Go: a `go/ast` walk over every `*.go` file, skipping `pkg/api/generated`, `pkg/gateway/spa`, `vendor`. Tool: `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/cmd/funlen`.
- TypeScript: a TypeScript compiler API walk over `src/**/*.ts(x)`, skipping `generated`. Counts function declarations, methods, function expressions, arrow functions, constructors, accessors. Tool: `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/scripts/tsfunlen.cjs`.
- Test files are counted but listed separately; the plan's test budget is looser.
- Raw output: `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/results/funlen-go-120-2026-09-15.txt` and `funlen-ts-120-2026-09-15.txt`.

## Totals

| | Functions scanned | Over 120 lines | Share |
|---|---|---|---|
| Go production | 11330 | 315 | 2.8% |
| Go tests | 19806 | 270 | 1.4% |
| TS production | 6671 | 184 | 2.8% |
| TS tests | 25574 | 212 | 0.8% |

## Size bands (production only)

| Go band | Count |
|---|---|
| over 2,000 | 1 |
| 1,001 to 2,000 | 8 |
| 501 to 1,000 | 7 |
| 251 to 500 | 51 |
| 121 to 250 | 249 |

| TS band | Count |
|---|---|
| over 2,000 | 4 |
| 1,001 to 2,000 | 5 |
| 501 to 1,000 | 16 |
| 251 to 500 | 53 |
| 121 to 250 | 108 |

## Go production, by package

| Count | Package |
|---|---|
| 81 | `pkg/gateway` |
| 57 | `pkg/agent` |
| 30 | `pkg/tools` |
| 22 | `pkg/knowledge` |
| 14 | `pkg/tools/browser` |
| 10 | `pkg/sysagent/tools` |
| 10 | `pkg/sandbox` |
| 7 | `pkg/records/knowledgefind` |
| 5 | `pkg/vaultimport` |
| 4 | `pkg/tools/browser/webrtc` |
| 4 | `pkg/session` |
| 4 | `pkg/coreagent` |
| 3 | `pkg/records` |
| 3 | `pkg/providers` |
| 3 | `pkg/media/library` |
| 3 | `pkg/config` |
| 3 | `pkg/channels/matrix` |
| 3 | `pkg/channels` |
| 3 | `pkg/audit` |
| 3 | `pkg/agent/runner` |
| 2 | `spikes/wv1-webrtc/q4-bidir` |
| 2 | `spikes/wv1-webrtc/q3-e2e` |
| 2 | `pkg/vaultprops` |
| 2 | `pkg/task` |
| 2 | `pkg/providers/anthropic` |
| 2 | `pkg/gitevidence` |
| 2 | `pkg/filegrep` |
| 2 | `pkg/channels/telegram` |
| 2 | `evals/cmd/eval-runner` |
| 2 | `cmd/omnipus-launcher-tui/ui` |
| 1 | `spikes/wv1-webrtc/q1-connectivity` |
| 1 | `scripts/gen-asyncapi-go` |
| 1 | `pkg/utils` |
| 1 | `pkg/routing` |
| 1 | `pkg/providers/openai_compat` |
| 1 | `pkg/providers/catalog` |
| 1 | `pkg/providers/anthropic_messages` |
| 1 | `pkg/plan` |
| 1 | `pkg/migrate/sources/openclaw` |
| 1 | `pkg/mcp/testdata/stub_mcp_server` |
| 1 | `pkg/mcp` |
| 1 | `pkg/gateway/middleware` |
| 1 | `pkg/cron` |
| 1 | `pkg/channels/weixin` |
| 1 | `pkg/channels/wecom` |
| 1 | `pkg/channels/line` |
| 1 | `pkg/channels/discord` |
| 1 | `pkg/agent/testutil` |
| 1 | `cmd/omnipus/internal/run` |
| 1 | `cmd/omnipus/internal/onboard` |
| 1 | `cmd/omnipus/internal/gateway` |
| 1 | `cmd/omnipus/internal/doctor` |
| 1 | `cmd/omnipus` |
| 1 | `` |

## Go production, full list (longest first)

| Lines | Function | Location |
|---|---|---|
| 4364 | `AgentLoop.runTurn` | `pkg/agent/loop.go:9740` |
| 1489 | `spawnSubTurn` | `pkg/agent/subturn.go:632` |
| 1346 | `registerSharedTools` | `pkg/agent/loop.go:2414` |
| 1258 | `setupAndStartServices` | `pkg/gateway/gateway.go:4370` |
| 1231 | `WSHandler.eventForwarder` | `pkg/gateway/websocket.go:3998` |
| 1090 | `RunContextWithOptions` | `pkg/gateway/gateway.go:2134` |
| 1060 | `coreAgentSeed` | `pkg/coreagent/core.go:798` |
| 1041 | `restAPI.HandleProviders` | `pkg/gateway/rest.go:6439` |
| 1003 | `DefaultConfig` | `pkg/config/defaults.go:52` |
| 999 | `restAPI.updateAgent` | `pkg/gateway/rest.go:3435` |
| 687 | `streamReplay` | `pkg/gateway/replay.go:75` |
| 595 | `WSHandler.handleChatMessage` | `pkg/gateway/websocket.go:1709` |
| 587 | `restAPI.handleTaskPatch` | `pkg/gateway/rest_tasks.go:2069` |
| 584 | `AgentLoop.RequestCancel` | `pkg/agent/cancel.go:253` |
| 534 | `restAPI.createAgent` | `pkg/gateway/rest.go:2663` |
| 504 | `applySandbox` | `pkg/gateway/sandbox_apply.go:240` |
| 500 | `restAPI.HandleCompleteOnboarding` | `pkg/gateway/rest_onboarding.go:429` |
| 427 | `NewAgentLoop` | `pkg/agent/loop.go:1451` |
| 409 | `restAPI.handleWorkspacePut` | `pkg/gateway/rest_workspaces.go:989` |
| 397 | `TaskUpdateTool.Execute` | `pkg/tools/task.go:1683` |
| 392 | `TaskUpdateTool.Execute` | `pkg/sysagent/tools/task.go:813` |
| 389 | `restAPI.HandleUpload` | `pkg/gateway/rest.go:10618` |
| 386 | `WSHandler.handleAttachSession` | `pkg/gateway/websocket.go:2905` |
| 383 | `AgentLoop.runRecap` | `pkg/agent/session_end.go:287` |
| 378 | `AgentLoop.runVerifierAdjudication` | `pkg/agent/verifier_adjudication.go:1328` |
| 375 | `restAPI.setChannelRouting` | `pkg/gateway/rest.go:9224` |
| 373 | `restAPI.updateAgentTools` | `pkg/gateway/rest.go:8404` |
| 367 | `AgentLoop.runGoalAdjudication` | `pkg/agent/goal_triggers.go:765` |
| 354 | `NewAgentInstance` | `pkg/agent/instance.go:151` |
| 345 | `MessageParentTool.Execute` | `pkg/tools/message_parent.go:383` |
| 341 | `AgentLoop.processMessage` | `pkg/agent/loop.go:8360` |
| 341 | `DelegateTool.executeAsync` | `pkg/tools/delegate.go:1906` |
| 325 | `findRecords` | `pkg/records/knowledgefind/find.go:1040` |
| 314 | `TaskCreateTool.Execute` | `pkg/tools/task.go:1116` |
| 311 | `Store.updateLocked` | `pkg/plan/store.go:323` |
| 310 | `Store.updateLocked` | `pkg/task/store.go:1154` |
| 310 | `renderSeatbeltProfile` | `pkg/sandbox/seatbelt_profile.go:128` |
| 308 | `SetGoalTool.Execute` | `pkg/tools/set_goal.go:416` |
| 307 | `restAPI.putSandboxConfig` | `pkg/gateway/rest_sandbox_config.go:152` |
| 303 | `RunWithOptions` | `pkg/vaultimport/run.go:272` |
| 302 | `Manager.Reload` | `pkg/channels/manager.go:1745` |
| 301 | `TaskCreateTool.Execute` | `pkg/sysagent/tools/task.go:422` |
| 299 | `RecallConversationTool.Execute` | `pkg/agent/recall_conversation.go:189` |
| 299 | `restAPI.handleWorkspaceDelete` | `pkg/gateway/rest_workspaces.go:1399` |
| 293 | `BrowserWSHandler.handleWebRTCOffer` | `pkg/gateway/browser_webrtc.go:282` |
| 292 | `ValidateViewAgainstSchemas` | `pkg/records/view.go:1200` |
| 289 | `LiveView.applyViewport` | `pkg/tools/browser/live.go:1019` |
| 288 | `validateBootConfig` | `pkg/config/validator.go:274` |
| 285 | `AgentLoop.applyGoalCommandPrompt` | `pkg/agent/goal_loop.go:70` |
| 284 | `wsStreamer.Finalize` | `pkg/gateway/websocket.go:5868` |
| 284 | `Run` | `cmd/omnipus/internal/run/run.go:139` |
| 278 | `WSHandler.readLoop` | `pkg/gateway/websocket.go:1271` |
| 277 | `AgentCreateTool.Execute` | `pkg/sysagent/tools/agent.go:185` |
| 277 | `runExternalCLISubTurn` | `pkg/agent/external_dispatch.go:143` |
| 277 | `AgentLoop.checkGoalLoopAfterTurn` | `pkg/agent/goal_loop.go:1185` |
| 277 | `DelegateTool.executeRun` | `pkg/tools/delegate.go:1424` |
| 271 | `execPathCaches.resolve` | `pkg/tools/browser/exec_resolver.go:475` |
| 271 | `restAPI.runExecutorSmokeTest` | `pkg/gateway/rest_executor_smoketest.go:302` |
| 270 | `ResolvePath` | `pkg/tools/resolvepath.go:902` |
| 270 | `StartTestGateway` | `pkg/agent/testutil/gateway_harness.go:308` |
| 262 | `AgentLoop.JudgeCriteria` | `pkg/agent/judge.go:309` |
| 260 | `restartServices` | `pkg/gateway/gateway.go:5990` |
| 260 | `restAPI.runProviderDelete` | `pkg/gateway/rest_providers_delete.go:170` |
| 257 | `AgentDeleteTool.Execute` | `pkg/sysagent/tools/agent.go:790` |
| 255 | `DelegateTool.executeRespond` | `pkg/tools/delegate.go:3442` |
| 254 | `restAPI.setAgentMailbox` | `pkg/gateway/rest_mailbox.go:212` |
| 254 | `restAPI.HandleOnboardingProbeProvider` | `pkg/gateway/rest_onboarding.go:1231` |
| 250 | `SeedConfig` | `pkg/coreagent/core.go:2533` |
| 249 | `restAPI.registerAdditionalEndpoints` | `pkg/gateway/rest.go:5727` |
| 243 | `AgentLoop.windowTrimForce` | `pkg/agent/loop.go:14716` |
| 241 | `MCPAddTool.Execute` | `pkg/sysagent/tools/mcp.go:92` |
| 240 | `IsValidEventName` | `pkg/audit/audit.go:134` |
| 239 | `AgentLoop.Run` | `pkg/agent/loop.go:4613` |
| 239 | `AgentLoop.processSystemMessage` | `pkg/agent/loop.go:8953` |
| 235 | `Renamer.Plan` | `pkg/knowledge/rename.go:449` |
| 234 | `converter.walk` | `pkg/utils/markdown.go:138` |
| 233 | `restAPI.configureChannel` | `pkg/gateway/rest.go:10167` |
| 232 | `NewOmnipusCommand` | `cmd/omnipus/main.go:387` |
| 229 | `ExecTool.runBackground` | `pkg/tools/shell.go:2137` |
| 228 | `buildV2LeafNode` | `pkg/vaultimport/view_write.go:846` |
| 227 | `parseStreamResponse` | `pkg/providers/openai_compat/provider.go:298` |
| 227 | `loadConfigInternal` | `pkg/config/config.go:4473` |
| 226 | `state.scanFile` | `pkg/filegrep/filegrep.go:1188` |
| 225 | `ExecTool.guardCommand` | `pkg/tools/shell.go:1052` |
| 224 | `syncReconcileBody` | `pkg/vaultprops/sync.go:421` |
| 223 | `evaluation.assemble` | `pkg/records/knowledgefind/assemble.go:251` |
| 223 | `restAPI.HandleChannels` | `pkg/gateway/rest.go:8783` |
| 221 | `sendRawFrameBytes` | `pkg/gateway/websocket.go:3611` |
| 219 | `GeneralBuiltinMetadata` | `pkg/tools/general_builtin_catalog.go:45` |
| 219 | `migrateLegacyMatrixCryptoStore` | `pkg/channels/matrix/init.go:79` |
| 218 | `missingChromeLibsELF` | `cmd/omnipus/internal/doctor/command_libs_linux.go:90` |
| 217 | `EditTool.execEmbed` | `pkg/knowledge/knowledge_edit.go:1488` |
| 216 | `captureIngestWSHandler.serveConn` | `pkg/gateway/browser_webrtc.go:1362` |
| 213 | `WebServeTool.executeDev` | `pkg/tools/web_serve.go:538` |
| 213 | `AgentLoop.Close` | `pkg/agent/loop.go:5038` |
| 211 | `restAPI.handleTaskCreate` | `pkg/gateway/rest_tasks.go:1856` |
| 210 | `restAPI.validateAndStoreOnboardingKey` | `pkg/gateway/rest_onboarding.go:938` |
| 210 | `ReadFileTool.Execute` | `pkg/tools/filesystem.go:518` |
| 209 | `DelegateTool.executeCancel` | `pkg/tools/delegate.go:3725` |
| 209 | `restAPI.handleKnowledgeRecordRelation` | `pkg/gateway/rest_knowledge_relation.go:98` |
| 208 | `resolveMediaRefsWithOffload` | `pkg/agent/loop_media.go:90` |
| 206 | `DefaultPolicyForModel` | `pkg/sandbox/sandbox.go:480` |
| 205 | `SwitchAgentTool.Execute` | `pkg/tools/handoff.go:221` |
| 204 | `WebFetchTool.Execute` | `pkg/tools/web.go:1332` |
| 203 | `AgentLoop.UpsertAgentFast` | `pkg/agent/registry.go:674` |
| 202 | `Manager.ConnectServer` | `pkg/mcp/manager.go:560` |
| 201 | `AgentLoop.ReloadProviderAndConfig` | `pkg/agent/loop.go:5780` |
| 200 | `query.applyColumns` | `pkg/records/knowledgefind/request.go:390` |
| 199 | `TaskExecutor.finishRunTurn` | `pkg/agent/task_run_loop.go:239` |
| 197 | `sessionWorker.processTurn` | `pkg/agent/session_worker.go:379` |
| 195 | `restAPI.addMCPServer` | `pkg/gateway/rest.go:7710` |
| 195 | `WorkspaceUpdateTool.Execute` | `pkg/sysagent/tools/workspace.go:270` |
| 193 | `Report.Render` | `pkg/vaultimport/report.go:119` |
| 193 | `Trasher.Restore` | `pkg/knowledge/knowledge_restructure_trash.go:711` |
| 192 | `LinuxBackend.ApplyWithMode` | `pkg/sandbox/sandbox_linux.go:231` |
| 191 | `AgentUpdateTool.Execute` | `pkg/sysagent/tools/agent.go:533` |
| 191 | `restAPI.toWireTask` | `pkg/gateway/rest_tasks.go:692` |
| 191 | `restAPI.putToolPolicies` | `pkg/gateway/rest_tool_policies.go:68` |
| 189 | `Repo.Commit` | `pkg/gitevidence/commit.go:99` |
| 188 | `findTasks` | `pkg/records/knowledgefind/responses.go:376` |
| 188 | `restAPI.HandleTokenStats` | `pkg/gateway/rest_stats.go:76` |
| 188 | `restAPI.patchMCPServer` | `pkg/gateway/rest.go:8156` |
| 188 | `AgentLoop.HydrateAgentHistoryFromTranscript` | `pkg/agent/attach_hydrate.go:140` |
| 186 | `AgentLoop.resolveMessageRoute` | `pkg/agent/loop.go:8702` |
| 185 | `AgentLoop.reconcileLocked` | `pkg/agent/loop_mcp.go:401` |
| 185 | `ToolRegistry.ExecuteWithContext` | `pkg/tools/registry.go:487` |
| 184 | `restAPI.handlePlanPut` | `pkg/gateway/rest_plans.go:890` |
| 183 | `AggregateUsage` | `pkg/session/usage.go:209` |
| 183 | `startBrowserWarmBoot` | `pkg/gateway/gateway.go:3649` |
| 182 | `EditTool.Parameters` | `pkg/knowledge/knowledge_edit.go:236` |
| 182 | `AgentLoop.admitToolResult` | `pkg/agent/tool_result_admit.go:376` |
| 181 | `TelegramChannel.handleMessage` | `pkg/channels/telegram/telegram.go:603` |
| 181 | `assembleBPFMode` | `pkg/sandbox/seccomp_linux.go:176` |
| 181 | `DelegateTool.executeSync` | `pkg/tools/delegate.go:2250` |
| 181 | `Session.HandleViewerOfferHandle` | `pkg/tools/browser/webrtc/viewer.go:78` |
| 179 | `restAPI.handleKnowledgeGraph` | `pkg/gateway/rest_knowledge.go:616` |
| 179 | `MemoryStore.SearchEntriesInScope` | `pkg/agent/memory.go:633` |
| 178 | `PlanEngine.wakeSupervisor` | `pkg/agent/plan_engine.go:3616` |
| 178 | `executeReload` | `pkg/gateway/gateway.go:4087` |
| 178 | `translateOneView` | `pkg/vaultimport/view_write.go:184` |
| 177 | `App.newUsersPage` | `cmd/omnipus-launcher-tui/ui/users.go:17` |
| 176 | `restAPI.toWirePlan` | `pkg/gateway/rest_plans.go:168` |
| 176 | `Session.HandleIngestOffer` | `pkg/tools/browser/webrtc/ingest.go:78` |
| 175 | `RecallConversationTool.executeToolCallID` | `pkg/agent/recall_conversation.go:822` |
| 175 | `restAPI.handleLibraryFilesSearch` | `pkg/gateway/rest_library_files_search.go:86` |
| 175 | `omnipusGracefulShutdown` | `pkg/gateway/shutdown.go:34` |
| 174 | `restAPI.HandleAgents` | `pkg/gateway/rest.go:1405` |
| 174 | `Index.indexNote` | `pkg/knowledge/index.go:2192` |
| 174 | `AgentLoop.CloseSession` | `pkg/agent/session_end.go:32` |
| 174 | `restAPI.HandleAgentToolsRegistry` | `pkg/gateway/rest_tool_registry.go:172` |
| 173 | `state.walkDir` | `pkg/filegrep/filegrep.go:920` |
| 171 | `Index.SyncWith` | `pkg/knowledge/index.go:1695` |
| 171 | `BrowserWSHandler.readLoop` | `pkg/gateway/browser_ws.go:1134` |
| 171 | `Library.uploadInternal` | `pkg/media/library/library.go:546` |
| 170 | `Watcher.run` | `pkg/knowledge/watch.go:390` |
| 169 | `TaskExecutor.StartTaskNow` | `pkg/agent/task_executor.go:1938` |
| 169 | `ToolsTool.execSearchAndLoad` | `pkg/tools/tools_tool.go:153` |
| 168 | `BrowserWSHandler.handleAttach` | `pkg/gateway/browser_ws.go:1410` |
| 168 | `RegenerateReport` | `evals/cmd/eval-runner/report.go:104` |
| 168 | `AgentLoop.processTaskDirectExternalCLI` | `pkg/agent/loop.go:7598` |
| 168 | `PlanEngine.processPlan` | `pkg/agent/plan_engine.go:1229` |
| 168 | `PlanCorrectTool.buildCorrection` | `pkg/tools/plan_correct.go:401` |
| 167 | `classifyProperty` | `pkg/vaultimport/infer.go:843` |
| 167 | `CronService.executeJobByID` | `pkg/cron/service.go:593` |
| 166 | `AgentLoop.runMachineCheck` | `pkg/agent/judge.go:667` |
| 166 | `seedSystemAgents` | `pkg/coreagent/core.go:3023` |
| 166 | `AgentLoop.maybeSettleGoalIdle` | `pkg/agent/goal_triggers.go:1226` |
| 166 | `Session.attachIngestTrack` | `pkg/tools/browser/webrtc/ingest.go:565` |
| 165 | `InstallSkillTool.Execute` | `pkg/tools/skills_install.go:162` |
| 165 | `drainExternalRun` | `pkg/agent/external_dispatch.go:429` |
| 164 | `ClaudeDriver.Run` | `pkg/agent/runner/driver_claude.go:121` |
| 163 | `App.newSchemesPage` | `cmd/omnipus-launcher-tui/ui/schemes.go:17` |
| 162 | `systemAgentSeed` | `pkg/coreagent/core.go:1880` |
| 162 | `CodexDriver.Run` | `pkg/agent/runner/driver_codex.go:95` |
| 162 | `PlanEngine.AppendCorrection` | `pkg/agent/plan_engine.go:4962` |
| 161 | `AgentLoop.midTurnWindowCheck` | `pkg/agent/midturn_budget.go:194` |
| 160 | `TelegramChannel.SendMedia` | `pkg/channels/telegram/telegram.go:442` |
| 160 | `EnsureChromiumBuild` | `pkg/tools/browser/installer.go:141` |
| 160 | `MatrixChannel.SendMedia` | `pkg/channels/matrix/matrix.go:436` |
| 160 | `SearchTool.Execute` | `pkg/knowledge/tools.go:410` |
| 158 | `wsStreamer.Update` | `pkg/gateway/websocket.go:5709` |
| 157 | `OpencodeDriver.Run` | `pkg/agent/runner/driver_opencode.go:61` |
| 157 | `Manager.StartAll` | `pkg/channels/manager.go:883` |
| 156 | `SpawnBackgroundChild` | `pkg/sandbox/spawn_bg.go:81` |
| 156 | `Library.CascadeDelete` | `pkg/media/library/library.go:1045` |
| 154 | `restAPI.setGodMode` | `pkg/gateway/rest_god_mode.go:116` |
| 154 | `UnifiedStore.RetentionSweep` | `pkg/session/retention_sweep.go:97` |
| 154 | `buildVaultSearchResult` | `pkg/gateway/rest_knowledge_find.go:250` |
| 152 | `parseProvider` | `pkg/providers/catalog/parse.go:185` |
| 152 | `TaskTriggerScheduler.RunScheduled` | `pkg/agent/task_trigger.go:577` |
| 152 | `runScenario` | `evals/cmd/eval-runner/main.go:802` |
| 151 | `NewWebSearchTool` | `pkg/tools/web.go:972` |
| 151 | `BrowserManager.ensureStarted` | `pkg/tools/browser/manager.go:1324` |
| 151 | `NewGatewayCommand` | `cmd/omnipus/internal/gateway/command.go:19` |
| 151 | `TaskExecutor.adjudicateRunClaim` | `pkg/agent/task_run_loop.go:454` |
| 151 | `restAPI.handleLibraryUpload` | `pkg/gateway/rest_library.go:1248` |
| 151 | `DiscordChannel.handleMessage` | `pkg/channels/discord/discord.go:346` |
| 150 | `BrowserManager.adoptTarget` | `pkg/tools/browser/manager.go:2882` |
| 149 | `PlanEngine.applyJudgeRoundOutcome` | `pkg/agent/plan_engine.go:2420` |
| 149 | `CSRFMiddleware` | `pkg/gateway/middleware/csrf.go:340` |
| 149 | `restAPI.handleKnowledgeBaseViews` | `pkg/gateway/rest_knowledge_base_views.go:63` |
| 149 | `ContextBuilder.BuildMessages` | `pkg/agent/context.go:1291` |
| 148 | `restAPI.HandleActivity` | `pkg/gateway/rest.go:6287` |
| 148 | `buildOneOccurrenceSet` | `pkg/gateway/task_occurrences.go:226` |
| 147 | `restAPI.deleteChannelInstance` | `pkg/gateway/rest.go:9766` |
| 147 | `buildFileSearchRoots` | `pkg/gateway/rest_library_files_search.go:603` |
| 147 | `EgressProxy.handleConnect` | `pkg/sandbox/egress_proxy.go:326` |
| 147 | `SSEHandler.ServeHTTP` | `pkg/gateway/sse.go:101` |
| 146 | `LINEChannel.processEvent` | `pkg/channels/line/line.go:278` |
| 146 | `ConfigureTool.execCreateView` | `pkg/knowledge/knowledge_configure_create_view.go:809` |
| 146 | `Provider.streamWithCallbacks` | `pkg/providers/anthropic/provider.go:195` |
| 145 | `DeriveKernelPolicy` | `pkg/sandbox/derive_from_fspolicy.go:80` |
| 145 | `BrowserManager.ReapIdleSessions` | `pkg/tools/browser/manager.go:3708` |
| 145 | `composePartsForKind` | `pkg/knowledge/knowledge_configure_create_view.go:652` |
| 144 | `AgentLoop.compileGoalIntentLLM` | `pkg/agent/goal_compile_llm.go:843` |
| 142 | `restAPI.deleteAgent` | `pkg/gateway/rest.go:3201` |
| 142 | `ClickTool.Execute` | `pkg/tools/browser/tools.go:284` |
| 142 | `restAPI.handleWorkspacePost` | `pkg/gateway/rest_workspaces.go:800` |
| 142 | `Repo.DiffWorkingTree` | `pkg/gitevidence/workingdiff.go:79` |
| 142 | `restAPI.putRateLimits` | `pkg/gateway/rest_rate_limits.go:60` |
| 141 | `applyPostStartHardening` | `pkg/sandbox/hardened_exec_linux.go:168` |
| 141 | `OpenFindEnv` | `pkg/vaultprops/find_env.go:57` |
| 141 | `AgentLoop.wireSessionMessagingForAgent` | `pkg/agent/session_messaging_wire.go:127` |
| 140 | `OpenClawConfig.convertChannels` | `pkg/migrate/sources/openclaw/openclaw_config.go:698` |
| 140 | `EditTool.execSetProperty` | `pkg/knowledge/knowledge_edit.go:719` |
| 140 | `buildBreadcrumb` | `pkg/agent/breadcrumb.go:46` |
| 139 | `CreateBaseTool.Execute` | `pkg/knowledge/knowledge_base_create.go:129` |
| 139 | `AgentLoop.runAgentLoop` | `pkg/agent/loop.go:9195` |
| 139 | `restAPI.HandleLogin` | `pkg/gateway/rest_auth.go:738` |
| 139 | `offerHandler` | `spikes/wv1-webrtc/q1-connectivity/main.go:170` |
| 139 | `libraryPreviewRoutes.handleServeLibraryPreview` | `pkg/gateway/rest_library_preview.go:540` |
| 138 | `BrowserWSHandler.handleViewport` | `pkg/gateway/browser_ws.go:2325` |
| 138 | `resolveGoType` | `scripts/gen-asyncapi-go/main.go:546` |
| 138 | `reduceAggregate` | `pkg/records/knowledgefind/summaries.go:489` |
| 137 | `restAPI.handleProviderEntitlement` | `pkg/gateway/rest_providers_entitlement.go:161` |
| 137 | `Parameters` | `pkg/records/knowledgefind/tool.go:196` |
| 137 | `RouteResolver.ResolveRoute` | `pkg/routing/route.go:66` |
| 137 | `relay.viewerOfferHandler` | `spikes/wv1-webrtc/q4-bidir/viewer.go:19` |
| 137 | `computeProviderDependents` | `pkg/gateway/provider_dependents.go:136` |
| 137 | `ToolsTool.execLoad` | `pkg/tools/tools_tool.go:325` |
| 136 | `sanitizeHistoryIndexed` | `pkg/agent/context.go:1456` |
| 136 | `DescribeTool.gather` | `pkg/knowledge/tools.go:1313` |
| 135 | `NavigateTool.Execute` | `pkg/tools/browser/tools.go:97` |
| 135 | `EditNote` | `pkg/knowledge/author.go:730` |
| 135 | `TaskListTool.Execute` | `pkg/sysagent/tools/task.go:1440` |
| 135 | `encodeImageToDataURLCached` | `pkg/agent/loop_media.go:746` |
| 135 | `BuildLinkGraph` | `pkg/knowledge/graph.go:71` |
| 135 | `toJudgeVerdictFrame` | `pkg/gateway/replay.go:1287` |
| 135 | `buildRequestBody` | `pkg/providers/anthropic_messages/provider.go:176` |
| 135 | `TasksTool.Execute` | `pkg/knowledge/authoring_tools.go:1363` |
| 134 | `ValidateFormulaSet` | `pkg/records/formula_set.go:185` |
| 134 | `Filter.Validate` | `pkg/records/filter.go:442` |
| 134 | `restAPI.handleRecordCreate` | `pkg/gateway/rest_knowledge_record.go:904` |
| 134 | `NewSession` | `pkg/tools/browser/webrtc/session.go:246` |
| 134 | `AgentLoop.dispatchVerifierTurn` | `pkg/agent/verifier_adjudication.go:1862` |
| 134 | `AgentLoop.wirePlanToolsForAgent` | `pkg/agent/loop.go:6728` |
| 133 | `mutateToolCallInTranscript` | `pkg/agent/approval_transcript.go:272` |
| 133 | `VerifyFile` | `pkg/audit/verify.go:146` |
| 132 | `restAPI.serveStaticFile` | `pkg/gateway/rest_preview.go:196` |
| 131 | `Library.OrphanGC` | `pkg/media/library/library.go:1210` |
| 131 | `q1OfferHandler` | `spikes/wv1-webrtc/q3-e2e/q1compat.go:113` |
| 131 | `ProviderConfigureTool.Execute` | `pkg/sysagent/tools/provider.go:108` |
| 131 | `MatrixChannel.handleMessageEvent` | `pkg/channels/matrix/matrix.go:707` |
| 131 | `q1OfferHandler` | `spikes/wv1-webrtc/q4-bidir/q1compat.go:113` |
| 131 | `runOnCurrentThread` | `pkg/sandbox/hardened_exec.go:733` |
| 131 | `buildProviderPool` | `pkg/agent/instance.go:720` |
| 130 | `NewStoreOAuthTokenSource` | `pkg/providers/oauth_token_source.go:440` |
| 130 | `buildEnabledRefMap` | `pkg/gateway/gateway.go:376` |
| 130 | `renderGrepResult` | `pkg/tools/grep.go:1174` |
| 130 | `WeixinChannel.pollLoop` | `pkg/channels/weixin/weixin.go:152` |
| 130 | `toWireCriteria` | `pkg/gateway/rest_tasks.go:1008` |
| 130 | `BrowserCoordinator.launchChrome` | `pkg/tools/browser/coordinator.go:782` |
| 130 | `NewOnboardCommand` | `cmd/omnipus/internal/onboard/onboard.go:124` |
| 130 | `restAPI.HandleToolApprovals` | `pkg/gateway/rest_tool_registry.go:405` |
| 129 | `PlanCorrectTool.Parameters` | `pkg/tools/plan_correct.go:158` |
| 129 | `restAPI.handleKnowledgeInfo` | `pkg/gateway/rest_knowledge.go:416` |
| 129 | `toWirePlanDoD` | `pkg/gateway/rest_plans.go:352` |
| 128 | `buildParams` | `pkg/providers/anthropic/provider.go:389` |
| 128 | `main` | `pkg/mcp/testdata/stub_mcp_server/main.go:65` |
| 128 | `restAPI.HandleLibrary` | `pkg/gateway/rest_library.go:47` |
| 128 | `SplitMessage` | `pkg/channels/split.go:13` |
| 128 | `scheduledRunner.RunScheduled` | `pkg/gateway/schedules.go:104` |
| 127 | `ConfigureTool.Parameters` | `pkg/knowledge/knowledge_configure.go:317` |
| 127 | `toWireJudgeVerdict` | `pkg/gateway/rest_tasks.go:2815` |
| 127 | `ReadTool.gather` | `pkg/knowledge/tools.go:1664` |
| 127 | `TaskUpdateTool.Parameters` | `pkg/tools/task.go:1555` |
| 126 | `restAPI.createSessionHTTP` | `pkg/gateway/rest.go:1274` |
| 126 | `buildIndexMapping` | `pkg/knowledge/index.go:1051` |
| 126 | `ResolveCandidatesWithLookup` | `pkg/providers/fallback.go:183` |
| 126 | `TaskCreateTool.Parameters` | `pkg/tools/task.go:909` |
| 126 | `BrowserManager.createFirstTab` | `pkg/tools/browser/manager.go:1717` |
| 125 | `restAPI.handleLibraryTransfer` | `pkg/gateway/rest_library.go:1924` |
| 124 | `FallbackChain.Execute` | `pkg/providers/fallback.go:324` |
| 124 | `MigrateMilestonesToTags` | `pkg/task/migrate_milestones.go:66` |
| 124 | `OpenTabTool.Execute` | `pkg/tools/browser/tabs.go:447` |
| 124 | `wireDelegationInjectors` | `pkg/agent/loop_env.go:81` |
| 124 | `UploadFileTool.Execute` | `pkg/tools/browser/tools_interact.go:746` |
| 123 | `relay.viewerOfferHandler` | `spikes/wv1-webrtc/q3-e2e/viewer.go:19` |
| 123 | `LiveView.dispatchInput` | `pkg/tools/browser/live.go:2584` |
| 123 | `InspectSessionTool.Execute` | `pkg/tools/inspect_session.go:146` |
| 123 | `parseGoalCompileResponse` | `pkg/agent/goal_compile_llm.go:166` |
| 123 | `AgentLoop.ProcessScheduled` | `pkg/agent/loop.go:8236` |
| 123 | `UsageQueryTool.Execute` | `pkg/sysagent/tools/diag.go:189` |
| 123 | `MessageInboxStore.Append` | `pkg/session/message_inbox.go:443` |
| 123 | `UnifiedStore.listSessionsFiltered` | `pkg/session/unified.go:1763` |
| 122 | `CheckDrift` | `pkg/knowledge/drift.go:182` |
| 122 | `LinuxBackend.RestrictCurrentThreadWithPolicy` | `pkg/sandbox/sandbox_linux.go:671` |
| 122 | `WeComChannel.dispatchIncoming` | `pkg/channels/wecom/wecom.go:554` |
| 122 | `GrepTool.Execute` | `pkg/tools/grep.go:169` |
| 122 | `storeInlineDataURL` | `pkg/tools/normalization.go:193` |
| 121 | `Trasher.Trash` | `pkg/knowledge/knowledge_restructure_trash.go:283` |
| 121 | `renderViewClauses` | `pkg/knowledge/knowledge_describe.go:780` |
| 121 | `Logger.cleanupExpired` | `pkg/audit/audit.go:1197` |
| 121 | `applyView` | `pkg/records/knowledgefind/find.go:542` |
| 121 | `ScreenshotTool.Execute` | `pkg/tools/browser/tools.go:709` |
|  | `` | `` |

## TypeScript production, full list (longest first)

| Lines | Function | Location |
|---|---|---|
| 4568 | `(arg of create)` | `src/store/chat.ts:2097` |
| 2789 | `AgentProfile` | `src/components/agents/AgentProfile.tsx:129` |
| 2775 | `handleFrame` | `src/store/chat.ts:3888` |
| 2602 | `BrowserLiveView` | `src/components/browser/BrowserLiveView.tsx:456` |
| 1447 | `LibraryPdfPreview` | `src/components/library/preview/LibraryPdfPreview.tsx:526` |
| 1300 | `LibraryExplorer` | `src/components/library/LibraryExplorer.tsx:235` |
| 1214 | `OmnipusComposer` | `src/components/chat/ChatScreen.tsx:2092` |
| 1170 | `TaskDetailPanel` | `src/components/workspaces/TaskDetailPanel.tsx:116` |
| 1049 | `SandboxSection` | `src/components/settings/SandboxSection.tsx:293` |
| 925 | `useSlashMenu` | `src/hooks/useSlashMenu.ts:260` |
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
| 594 | `(arg of useEffect)` | `src/components/library/preview/LibraryPdfPreview.tsx:634` |
| 568 | `McpServerModal` | `src/components/skills/McpServerModal.tsx:171` |
| 560 | `CalendarScreen` | `src/components/screens/CalendarScreen.tsx:59` |
| 547 | `PerformanceSection` | `src/components/settings/PerformanceSection.tsx:105` |
| 521 | `EmailMailboxPanel` | `src/components/connectors/EmailMailboxPanel.tsx:305` |
| 498 | `WorkspaceTeamTab` | `src/components/workspaces/WorkspaceTeamTab.tsx:62` |
| 497 | `WorkspaceSettingsTab` | `src/components/workspaces/WorkspaceSettingsTab.tsx:57` |
| 496 | `SkillBrowser` | `src/components/skills/SkillBrowser.tsx:132` |
| 492 | `GatewaySection` | `src/components/settings/GatewaySection.tsx:90` |
| 488 | `useAutoSave` | `src/hooks/useAutoSave.ts:152` |
| 485 | `OnboardingWizard` | `src/routes/onboarding.tsx:252` |
| 475 | `ProviderPicker` | `src/components/providers/ProviderPicker.tsx:160` |
| 461 | `CreatePlanSlideOver` | `src/components/workspaces/CreatePlanSlideOver.tsx:143` |
| 439 | `MemorySection` | `src/components/settings/MemorySection.tsx:144` |
| 438 | `SkillsScreen` | `src/components/screens/SkillsScreen.tsx:55` |
| 433 | `SignInDialog` | `src/components/providers/SignInDialog.tsx:124` |
| 428 | `sendMessage` | `src/store/chat.ts:3080` |
| 416 | `ConnectorsScreen` | `src/components/screens/ConnectorsScreen.tsx:864` |
| 414 | `ProviderConfigSheet` | `src/components/settings/ProvidersSection.tsx:215` |
| 411 | `(anonymous)` | `src/components/library/preview/LibraryPdfPreview.tsx:787` |
| 407 | `ToolApprovalCard` | `src/components/agents/ToolApprovalModal.tsx:157` |
| 405 | `AgentsLibraryView` | `src/components/screens/AgentListScreen.tsx:140` |
| 397 | `(arg of create)` | `src/store/session.ts:304` |
| 395 | `VirtualAssistantMessageRow` | `src/components/chat/ChatScreen.tsx:1236` |
| 393 | `EditableValueCell` | `src/components/library/preview/viewparts/RecordFieldEditor.tsx:303` |
| 381 | `ProviderStep` | `src/routes/onboarding.tsx:1059` |
| 379 | `CreateAgentWizard` | `src/components/agents/CreateAgentWizard.tsx:215` |
| 355 | `useLibraryFileEditor` | `src/components/library/preview/useLibraryFileEditor.ts:173` |
| 353 | `ProfileSection` | `src/components/settings/ProfileSection.tsx:60` |
| 352 | `WorkspaceTasksTab` | `src/components/workspaces/WorkspaceTasksTab.tsx:60` |
| 337 | `(arg of withBucket)` | `src/store/chat.ts:5832` |
| 336 | `AskUserQuestionCard` | `src/components/chat/AskUserQuestionCard.tsx:62` |
| 335 | `(arg of produce)` | `src/store/chat.ts:5833` |
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
| 249 | `(arg of useEffect)` | `src/components/browser/BrowserLiveView.tsx:1059` |
| 249 | `LoginScreen` | `src/routes/login.tsx:15` |
| 242 | `ProviderDetailPanel` | `src/components/providers/ProviderDetailPanel.tsx:189` |
| 242 | `TaskCard` | `src/components/workspaces/TaskCard.tsx:148` |
| 240 | `CalendarPart` | `src/components/library/preview/viewparts/CalendarPart.tsx:23` |
| 235 | `IframePreview` | `src/components/chat/IframePreview.tsx:170` |
| 232 | `IntegrationsSection` | `src/components/settings/IntegrationsSection.tsx:32` |
| 229 | `NewWorkspaceSlideOver` | `src/components/workspaces/NewWorkspaceSlideOver.tsx:38` |
| 226 | `WhatsAppNativeNotice` | `src/components/skills/WhatsAppNativeNotice.tsx:41` |
| 225 | `(arg of withBucket)` | `src/store/chat.ts:4128` |
| 223 | `(arg of produce)` | `src/store/chat.ts:4129` |
| 218 | `ToolPolicyEditor` | `src/components/shared/ToolPolicyEditor.tsx:594` |
| 218 | `PlanFilterTile` | `src/components/workspaces/PlansFilterBand.tsx:238` |
| 217 | `ListView` | `src/components/workspaces/ListView.tsx:72` |
| 216 | `WorkspaceTeamGraphInner` | `src/components/workspaces/team/WorkspaceTeamGraph.tsx:504` |
| 214 | `KbQueryFenceEmbedContent` | `src/components/library/preview/KbQueryFenceEmbed.tsx:98` |
| 211 | `ImageLightbox` | `src/components/chat/image-lightbox.tsx:43` |
| 210 | `ChartPart` | `src/components/library/preview/viewparts/ChartPart.tsx:66` |
| 208 | `LibraryTransferDialog` | `src/components/library/LibraryTransferDialog.tsx:58` |
| 207 | `Step1Identity` | `src/components/agents/wizard/Step1Identity.tsx:55` |
| 205 | `MermaidDiagramImpl` | `src/components/chat/mermaid-renderer.tsx:303` |
| 205 | `(arg of withBucket)` | `src/store/chat.ts:4859` |
| 202 | `(arg of useAutoSave)` | `src/components/agents/AgentProfile.tsx:786` |
| 202 | `LibraryPreviewPane` | `src/components/library/LibraryPreviewPane.tsx:130` |
| 201 | `(arg of useEffect)` | `src/components/browser/BrowserLiveView.tsx:1447` |
| 197 | `ProviderRow` | `src/components/settings/ProviderRow.tsx:364` |
| 195 | `AuthMethodControl` | `src/components/providers/AuthMethodControl.tsx:97` |
| 194 | `SessionRoute` | `src/routes/_app/sessions.$sessionId.tsx:33` |
| 193 | `PasswordStep` | `src/routes/onboarding.tsx:852` |
| 190 | `LibrarySignaturePad` | `src/components/library/preview/LibrarySignaturePad.tsx:52` |
| 188 | `StatusColumnsRow` | `src/components/workspaces/BoardView.tsx:415` |
| 187 | `ExecAllowlistSection` | `src/components/settings/ExecAllowlistSection.tsx:11` |
| 185 | `DevicesSection` | `src/components/settings/DevicesSection.tsx:61` |
| 185 | `RemoveProviderDialog` | `src/components/settings/RemoveProviderDialog.tsx:102` |
| 184 | `MessageItem` | `src/components/chat/MessageItem.tsx:124` |
| 184 | `TaskNodeComponent` | `src/components/workspaces/graph/TaskNode.tsx:30` |
| 179 | `mapToCalendarEvents` | `src/lib/calendar/eventMapping.ts:417` |
| 178 | `AgentListScreen` | `src/components/screens/AgentListScreen.tsx:548` |
| 174 | `ChatControls` | `src/components/chat/ChatControls.tsx:29` |
| 174 | `WorkspaceTabBar` | `src/components/workspaces/WorkspaceTabBar.tsx:118` |
| 173 | `AssistantMessage` | `src/components/chat/ChatScreen.tsx:1870` |
| 173 | `BrowserToolBlock` | `src/components/chat/tools/BrowserTool.tsx:108` |
| 172 | `ToolCallBadge` | `src/components/chat/ToolCallBadge.tsx:44` |
| 170 | `VideoEmbed` | `src/components/library/preview/VideoEmbed.tsx:155` |
| 169 | `KnowledgeOutline` | `src/components/library/knowledge/KnowledgeOutline.tsx:134` |
| 161 | `ExecProxyStatusCard` | `src/components/settings/ExecProxyStatusCard.tsx:46` |
| 159 | `useFileSearch` | `src/components/library/search/useFileSearch.ts:98` |
| 158 | `CommandPreview` | `src/components/agents/CommandPreview.tsx:91` |
| 158 | `BrowserNavigateBlock` | `src/components/chat/tools/BrowserNavigate.tsx:64` |
| 157 | `useCancelState` | `src/hooks/useCancelState.ts:62` |
| 157 | `clearStreamingState` | `src/store/chat.ts:3705` |
| 156 | `ActivityRow` | `src/components/chat/ActivityPanel.tsx:48` |
| 155 | `ChatImage` | `src/components/chat/ChatImage.tsx:31` |
| 153 | `useVaultSearch` | `src/components/library/search/useVaultSearch.ts:261` |
| 152 | `AuditLogViewer` | `src/components/settings/AuditLogViewer.tsx:256` |
| 151 | `CreateAgentModal` | `src/components/agents/CreateAgentModal.tsx:196` |
| 151 | `LibraryHtmlFrame` | `src/components/library/LibraryPreviewPane.tsx:443` |
| 149 | `useRunningActivity` | `src/hooks/useRunningActivity.ts:473` |
| 148 | `(arg of useEffect)` | `src/components/agents/AgentProfile.tsx:485` |
| 148 | `DefaultModelCard` | `src/components/settings/DefaultModelCard.tsx:80` |
| 147 | `getMermaid` | `src/components/chat/mermaid-renderer.tsx:155` |
| 145 | `(arg of slashMenu.slashItems.map)` | `src/components/chat/ChatScreen.tsx:2707` |
| 145 | `ModelPicker` | `src/components/chat/composer/ModelPicker.tsx:20` |
| 145 | `(arg of withBucket)` | `src/store/chat.ts:5138` |
| 144 | `remarkKbWikilinks` | `src/components/library/preview/knowledgeMarkdown.tsx:549` |
| 144 | `KbBaseEmbedContent` | `src/components/library/preview/knowledgeMarkdown.tsx:1649` |
| 143 | `WebServeBlock` | `src/components/chat/tools/WebServeUI.tsx:141` |
| 143 | `TaskChecklistField` | `src/components/workspaces/TaskChecklistField.tsx:39` |
| 143 | `markLastMessageInterrupted` | `src/store/chat.ts:2537` |
| 142 | `(anonymous)` | `src/components/library/preview/knowledgeMarkdown.tsx:550` |
| 142 | `shouldRenderToolCall` | `src/lib/toolVisibility.ts:96` |
| 141 | `SsrfEditor` | `src/components/settings/SsrfEditor.tsx:33` |
| 140 | `SubagentBlock` | `src/components/chat/SubagentBlock.tsx:87` |
| 140 | `KnowledgePanel` | `src/components/library/knowledge/KnowledgePanel.tsx:209` |
| 139 | `(arg of mapTextNodes)` | `src/components/library/preview/knowledgeMarkdown.tsx:551` |
| 138 | `(arg of splitTextNode)` | `src/components/library/preview/knowledgeMarkdown.tsx:552` |
| 136 | `KnowledgeViewsList` | `src/components/library/knowledge/KnowledgeViewsList.tsx:87` |
| 136 | `KbMarkdownImageContent` | `src/components/library/preview/KbMarkdownImage.tsx:102` |
| 134 | `GoalEchoCard` | `src/components/chat/GoalEchoCard.tsx:185` |
| 134 | `ReAuthDialog` | `src/components/settings/ReAuthDialog.tsx:24` |
| 133 | `EnvironmentOverridesEditor` | `src/components/agents/AgentProfile.tsx:3079` |
| 132 | `SkillTrustSection` | `src/components/settings/SkillTrustSection.tsx:57` |
| 132 | `buildTaskGraph` | `src/components/workspaces/graph/taskGraph.ts:231` |
| 131 | `(arg of edges.map)` | `src/components/library/knowledge/KnowledgeBacklinks.tsx:316` |
| 131 | `useSessionForest` | `src/components/sessions/SessionTree.tsx:135` |
| 131 | `useWorkspaceSetupKickoff` | `src/hooks/useWorkspaceSetupKickoff.ts:65` |
| 129 | `ReSignInDialog` | `src/components/providers/ReSignInDialog.tsx:61` |
| 128 | `(arg of messageParts.map)` | `src/components/chat/ChatScreen.tsx:1431` |
| 128 | `(arg of visibleProjects` | `src/components/layout/Sidebar.tsx:487` |
|             .map) | `` | `` |
| 128 | `(arg of withBucket)` | `src/store/chat.ts:4550` |
| 127 | `commit` | `src/components/library/preview/viewparts/RecordFieldEditor.tsx:386` |
| 127 | `CustomEndpointPanel` | `src/components/providers/CustomEndpointPanel.tsx:38` |
| 126 | `GatewayRestartModal` | `src/components/settings/GatewayRestartModal.tsx:64` |
| 126 | `(arg of produce)` | `src/store/chat.ts:4551` |
| 124 | `Step3Tools` | `src/components/agents/wizard/Step3Tools.tsx:23` |
| 124 | `(arg of useCallback)` | `src/components/browser/BrowserLiveView.tsx:2134` |
| 124 | `AddAgentPicker` | `src/components/workspaces/team/AddAgentPicker.tsx:54` |
| 123 | `VirtualizedMessageListInner` | `src/components/chat/ChatScreen.tsx:1746` |
| 123 | `SessionRow` | `src/components/search/SearchModal.tsx:268` |
| 123 | `RiskySettingControl` | `src/components/shared/RiskySettingControl.tsx:108` |
| 123 | `EdgeModeEditor` | `src/components/workspaces/team/EdgeModeEditor.tsx:41` |
| 123 | `(arg of create)` | `src/store/toolApproval.ts:99` |
| 122 | `WebSearchBlock` | `src/components/chat/tools/WebSearchResult.tsx:55` |
| 122 | `NotificationPanel` | `src/components/layout/NotificationPanel.tsx:41` |
| 121 | `ExecutorSelector` | `src/components/agents/ExecutorSelector.tsx:83` |
| 121 | `McpServerSection` | `src/components/shared/ToolPolicyEditor.tsx:461` |
|  | `` | `` |

## Reading the numbers

- One Go function, `AgentLoop.runTurn`, is over 4,000 lines on its own. The next eight Go functions are each over 1,000. Those nine are the function-level giants the ratchet must grandfather by name.
- The TS list is dominated by whole React components written as one function (`AgentProfile`, `BrowserLiveView`, `LibraryExplorer`) and the single Zustand `create` call in `src/store/chat.ts`. Splitting those is component extraction, not helper extraction.
- Roughly four out of five over-budget Go functions sit in the 121 to 250 band. Turning `funlen` on at 120 today would fail hundreds of files at once; the plan's grandfather-and-shrink ratchet is the only workable path.
