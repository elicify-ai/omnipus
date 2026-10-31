# T8 RED pack — traceability (qa-lead, RED phase)

Status: **RED in progress.** Every row below maps a spec §13.1 test to its test
symbol, its file, and its RED proof (the saved failing-run log under
`/workspace/logs/t8-red/`). Rows are filled in as each cluster is committed.

Spec oracle (sole source of every expected value):
`/workspace/omnipus-coordination/designs-20261029/tool-parity-spec-r17.md` (r17, final).

## Test plan (elicify-test-writing step 1)

- **Behaviour under test.** The T8 tool-parity surface: field selection for the
  four read tools; task/plan/goal tool write rules (authority, status, budgets,
  plan membership, goal sync, trigger parity with REST); the scheduling-wait
  invariant and the occurrence-ownership rule across every producer; the agent
  read surface (`get_agent` detail, model chain, permissions, preview); the
  Settings seam (path/preset checks, argv order); the Omnipus policy and
  prompt; the parity guard; the frontend labels/approval card.
- **Specification source.** tool-parity-spec-r17.md — §7 (tables + exact
  texts), §7.13 (field selection), §7.14 (Omnipus), §11 (limits), §12
  (scenarios 1.x–21.x), §13.1 (named tests), §13.2 (datasets D-1…D-12),
  §16 (policy matrix), §20 (traceability). No expectation was taken from
  running code; where the spec and the committed contracts disagree it is
  recorded as a finding, never silently resolved.
- **Unit boundary.** Unit tests exercise the real store/tool/scheduler types;
  process edges faked only where the spec's own fixture schedule says so
  (fake clock `newTriggerFakeClock`, fault-injected cron save, stubbed memory
  signal). The production registry (`ToolRegistry.Execute`),
  `GeneralBuiltinMetadata()`, the generated wire types
  (`pkg/api/generated`) and the real `task.Store` / `TaskTriggerScheduler` /
  plan engine are always real.
- **Case table.** Per test, below; inputs and expected values come from the
  spec datasets (D-1…D-12) and the byte-for-byte error texts (§7.11, §7.13,
  §3.11).
- **What would break this (mutation plan).** Deferred to CHECK (separate
  instance): ≥3 mutations per critical test, scratch clone, revert after.
  In RED only step 4 item 1 is in scope: see it red for the right reason.
- **Known gaps / honesty notes.** Recorded per row as `BLOCKED:` fatals — a
  missing implementation is a loud `t.Fatal`, never a skip. Rows that cannot
  become an automated test (hands-on/UAT-only) are marked explicitly.

## RED convention

- Missing implementation symbols: smallest compile stubs, each in a file whose
  name starts `red_scaffolding_` and whose header says so; stub bodies return
  zero values so tests fail on the spec-derived expectation (never stubbed to
  pass). The implementing lead replaces/deletes scaffolding in GREEN.
- Whole tools/endpoints not yet existing: the test looks the name up through
  the production surface (`GeneralBuiltinMetadata()`, registry) and fails with
  `t.Fatal("BLOCKED: <what> not implemented — required by <spec ref>")`.
- RED proof: one narrow `heavy go test -p 1 -run '<Test>'` per cluster, saved
  log + exit code under `/workspace/logs/t8-red/`. One test process at a time.
  Green/mutation checks are **deferred to CHECK**.

## Traceability table

| §13.1 # | Test | Scenarios / dataset | File | RED proof (log; exit) | Failing reason |
|---|---|---|---|---|---|
| 35 | `TestFieldSelect_ParseAndProject` | 20.1–20.8, 20.8b, 20.11 | `pkg/tools/fieldselect_test.go` | `RED-35-36-fieldselect.log`; exit 1 | stub `ValidateFieldsArg`/`Project` return nil — error/shape expectations unmet (§7.13) |
| 35b | `TestFieldSelect_ThroughRegistry` | 20.10 | `pkg/tools/fieldselect_test.go` (get_agent rows in `pkg/sysagent/tools`) | same log; exit 1 | `BLOCKED: get_task/get_plan/get_goal not implemented` (§7.9/§16) |
| 36 | `TestFieldSelect_EnumsMatchSchemas` | 20.9, 19.4b, FR-185 | `pkg/tools/fieldselect_test.go` (+ `pkg/sysagent/tools`) | same log; exit 1 | `BLOCKED: get_task/get_plan/get_goal not implemented` (FR-184) |
| 3 | `TestUpdateTask_PromptAndGoalSync` | 1.1–1.4, 1.8, 1.12, 1.13 | `pkg/tools/task_parity_test.go` | pending | |
| 4 | `TestUpdateTask_DefinitionAuthority_Matrix` | 1.5–1.7, 1.9–1.11 | `pkg/tools/task_parity_test.go` | pending | |
| 5 | `TestUpdateTask_StatusValuesAndStoreTransitions` | 2.4–2.9, D-11 | `pkg/tools/task_parity_test.go` | pending | |
| 6 | `TestTaskPlanMembership_SharedRestAndTool` | 2.11–2.15 | `pkg/tools/task_parity_test.go` | pending | |
| 7 | `TestUpdateTask_OrgFields_ClearDue_EmptyTitle` | 2.1–2.3, 2.10, 2.16, D-1 | `pkg/tools/task_parity_test.go` | pending | |
| 8 | `TestBudgets_CeilingWins` | 3.1–3.8, D-3 | `pkg/tools/budgets_ceiling_test.go` | pending | |
| 9 | `TestUpdatePlan_StateAndAuthority` | 5.1–5.6 | `pkg/tools/plan_parity_test.go` | pending | |
| 10 | `TestCreatePlan_AliasRemoved_DescriptionBounds` | 5.7, 5.8 | `pkg/tools/plan_parity_test.go` | pending | |
| 11 | `TestReads_VisibilityTable` | 4.1–4.12 | `pkg/tools/read_visibility_test.go` | pending | |
| 12 | `TestTaskTrigger_ToolParityWithRest` | 18.1, 18.3–18.10, D-9 | `pkg/tools/task_trigger_tool_parity_test.go` | pending | |
| 12b | `TestTaskTrigger_ScheduledTaskNotRunEarly_ProductionWiring` | 18.1, 18.11–18.14 | `pkg/agent/task_trigger_tool_wiring_test.go` | pending | |
| 12c | `TestTaskTrigger_ForeignActivationRefused` | 18.2, 18.15–18.18, 18.23 | `pkg/tools/task_trigger_foreign_test.go` | pending | |
| 12d | `TestTaskTrigger_ScheduledWaitSurvivesAutomaticPromotions` | 18.19–18.20, 18.24, 18.27–18.29, 18.31–18.32, 18.34, 18.36, 18.37 | `pkg/agent/task_trigger_wait_promotions_test.go` | pending | |
| 12e | `TestTaskTrigger_ReconciliationOrderingAndPartialFailure` | 1.14–1.15, 18.21–18.22, 18.25–18.26 | `pkg/agent/task_trigger_reconciliation_test.go` | pending | |
| 12f | `TestTaskTrigger_SchedulerDeliveryAuthority` | 18.30, 18.33, 18.35, 18.38, 18.39, 18.40, D-9 row 12 | `pkg/agent/task_trigger_delivery_authority_test.go` | pending | |
| 13 | `TestGetAgent_DetailAndModelChain` | 6.1–6.8 | `pkg/sysagent/tools/get_agent_parity_test.go` | pending | |
| 14 | `TestRestCreate_KeepsFallbackModels` | 7.7 | `pkg/gateway/rest_agent_parity_test.go` | pending | |
| 15 | `TestAgentWrite_RevisionAndTruthfulOutcomes` | 7.1–7.6, D-12 | `pkg/agentmutation/agent_write_outcomes_test.go` | pending | |
| 16 | `TestToolPolicyNames_McpAndStale_400NotStorage500` | 15.2–15.8, D-10 | `pkg/agentmutation/tool_policy_names_test.go` | pending | |
| 17 | `TestSecrets_NeverInAnySink_RealLoop` | 10.1–10.7 | `pkg/agent/secret_sink_test.go` | pending | |
| 18 | `TestEnvOverrides_MergeRemoveMask_RestAndTool` | 8.1–8.6, 8.10–8.12 | `pkg/tools/env_overrides_parity_test.go` | pending | |
| 19 | `TestCliArgs_ArgvAndDroppedAtRun` | 8.7, D-5 | `pkg/tools/cli_args_argv_test.go` | pending | |
| 20 | `TestExternalWorkerSettings_NoExtraPrompt` | 8.8, 8.9, 8.13, 8.14 | `pkg/tools/external_worker_settings_test.go` | pending | |
| 21 | `TestThirdPartySettings_PutWholeSection` + `_PathChecks_Table` + `_PresetLimits` + `_PersonOnly` | 9.1–9.8, 9.20–9.24, 9.27, D-6 | `pkg/gateway/third_party_settings_test.go` | pending | |
| 22 | `TestSpawn_ResolveCheckExec_NoFallback` | 9.9–9.14 | `pkg/agent/spawn_settings_path_test.go` | pending | |
| 23 | `TestSpawn_ArgvOrderPresetUnfiltered` + `TestAgentCannotWritePreset` | 9.17–9.19, 9.25 | `pkg/agent/spawn_argv_order_test.go` | pending | |
| 24 | `TestAgentSurfaces_CliPathRefused_CliLocked` | 9.15, 9.16, 9.28, 11.1–11.3 | `pkg/gateway/agent_surfaces_locks_test.go` | pending | |
| 25 | `TestDetection_LinkPathAndNoSpawn` | 9.1, 9.26 | `pkg/agent/cli_detection_test.go` | pending | |
| 26 | `TestNoSoulForThirdParty` | 12.1–12.3 | `pkg/gateway/no_soul_third_party_test.go` | pending | |
| 27 | `TestSeedConfig_OmnipusSkillsReconciled` | 14.1–14.4 | `pkg/coreagent/seed_config_reconcile_test.go` | pending | |
| 28 | `TestPolicyMatrix_FourTools` + `TestLiveAfterReload_ProductionRegistry` | 16.1–16.4, D-7 | `pkg/tools/policy_matrix_test.go` | pending | |
| 29 | `TestToolSearch_FindsGetTask` | 16.5 | `pkg/tools/toolsearch_finds_get_task_test.go` | pending | |
| 30 | `TestGetAgent_EmptySelectors` + `TestCreateAgent_HeartbeatRefused` + `TestGetAgent_ExactId` + `TestReadSchemas_Bounded` | 15.1, 15.9–15.11 | `pkg/sysagent/tools/get_agent_selectors_test.go` | pending | |
| 1 | `TestParity_RecursivePaths_MappingAndExclusions` | 13.1–13.5 | `pkg/tools/parity_guard_test.go` | pending | |
| 2 | `TestRuleTable_TaskFrozenSetShared` | 13.6 | `pkg/tools/rule_table_frozen_set_test.go` | pending | |
| 37 | `TestGetAgent_FieldsSelectorsAndPreview` + `TestGetAgent_ToolPermissionsLiveCatalog` | 19.1–19.5, 19.4b, 19.18, 15.1 | `pkg/sysagent/tools/get_agent_fields_test.go` | pending | |
| 38 | `TestGetAgent_EveryAgentAllowed` | 19.6 | `pkg/sysagent/tools/get_agent_every_agent_test.go` | pending | |
| 39 | `TestSetAgentPermissions_AskSelfRefusalGrantable` | 19.7–19.12 | `pkg/tools/set_agent_permissions_guardrails_test.go` | pending | |
| 40 | `TestToolPolicyScreens_FreshInstallSave` | 19.13, 19.14 | `pkg/gateway/tool_policy_screens_fresh_install_test.go` | pending | |
| 41 | `TestRetiredToolNames_RealLoopMessage_GuardAndSkillText` | 19.15–19.17 | `pkg/agent/retired_tool_names_test.go` | pending | |
| 42 | `TestOmnipusSelfDelegate_SpawnsChildAndReportsBack` | 21.1, 21.2, 21.6 | `pkg/agent/omnipus_self_delegate_test.go` | pending | |
| 43 | `TestOmnipusSelfDelegate_Refusals` + `TestOmnipusSelfDelegate_QueuedAtExplicitParallelCap` | 21.3–21.5 | `pkg/agent/omnipus_self_delegate_test.go` | pending | |
| 44 | `TestOmnipusTaskTools_PolicyAndSelfTask` (+ `list_jobs`, 21.13) | 21.7–21.9, 21.13, 21.15 | `pkg/coreagent/omnipus_task_tools_test.go` | pending | |
| 45 | `TestPrompt_OmnipusDelegatesRealWork` | 21.10, §7.14.3 | `pkg/coreagent/prompt_omnipus_test.go` | pending | |
| 46 | `TestAgentStore_StrayRateLimitsIgnoredThenDropped` | 21.11 | `pkg/agentstore/stray_rate_limits_test.go` | pending | |
| 47 | `TestUpdateAgent_RefusesPermissionFields_ProductionRegistry` | 7.8 | `pkg/sysagent/tools/update_agent_refusals_test.go` | pending | |
| 48 | `TestSkills_T8Texts_FreshInstallLoad` | 19.19 | `pkg/skills/t8_texts_fresh_install_test.go` | pending | |
| 50 | `TestSelfDelegate_MemoryPressureQueues_ProductionWiring` + `TestSelfDelegate_PromotionRespectsMemoryCap_ProductionWiring` + `TestSteerAdmission_ReleaseLockedRespectsLiveCap` | 21.16–21.19 | `pkg/agent/self_delegate_memory_pressure_test.go` | pending | |
| 49 | `TestRestAgentWrite_ExecutorUnknownFieldTexts` | 9.16, 11.1, 11.2 | `pkg/gateway/rest_agent_write_texts_test.go` | pending | |
| 31 | `approvalPreview.agentChange.test.tsx` | 17.1, 17.3, 17.7 | `src/components/approvals/__tests__/approvalPreview.agentChange.test.tsx` | pending | |
| 32 | `AgentProfile.programLineReadOnly.test.tsx` | 17.6 | `src/components/agents/__tests__/AgentProfile.programLineReadOnly.test.tsx` | pending | |
| 33 | `humanizeToolName.newTools.test.ts` + `autoApproveAskGroups.newTools.test.ts` | 17.4, 17.5 | `src/lib/__tests__/` | pending | |
| 34 | `tests/e2e/agent-parity-approval.spec.ts` | 17.2 | `tests/e2e/agent-parity-approval.spec.ts` | pending | |
| V-2 | receipt (not a test) | FR-163 | `/workspace/logs/t8-red/V2-RECEIPT.txt` | **done** — 4 gates green at `dbdd1da6a` | n/a |

## Findings (spec premise vs code)

- **F-SEL-1 (spec ambiguity, reported not resolved).** §13.1 test 35 says
  "a value of exactly 8,000 characters untouched, 8,001 cut", but §7.13's
  operative algorithm ("candidates are cut … longest first, **until the
  result fits**") can never exhibit an 8,001-character value as cut in a
  success path: longest-first stops as soon as the result fits, and any
  candidate reached after that point does not exist (candidates ≤ 8,000 are
  not candidates; cutting an 8,001 value to 8,000 + marker never makes an
  over-bound result fit). "8,001 cut" is only reachable under a
  cut-all-candidates reading, which contradicts "until the result fits".
  The test pins the operative reading (`project: cutting stops as soon as
  the result fits`) and additionally pins that exactly 8,000 is never a
  candidate. The implementer or chief should confirm the intended reading.
- **F-SEL-2 (spec example premise).** §7.12.1/§7.13 rule 1's example of a
  null a projection may carry — "`max_tool_iterations: null` = rides the
  global limit" — has no corresponding type in the committed contract:
  `AgentDetail.MaxToolIterations` is `*int` with `omitempty`
  (`pkg/api/generated/openapi_types.gen.go::AgentDetail`), so a nil value is
  omitted from the marshalled object and rides `absent_fields`, never null.
  The normative rule itself ("null only where the full object carries null")
  is tested with a synthetic object; the example's premise does not exist in
  the committed types.
