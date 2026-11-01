# Effort levels — RED repair traceability, round 2

Correction: round 1 claimed S-58 tool model-change rows and S-60 Memory presentation coverage that did not exist, and called permissive loop-wire checks exact. Those claims were wrong. Round 2 adds the missing boundaries and exact OpenRouter assertions below; independent CHECK has not yet ruled on this repair.

Correction: the first round-2 S-02 fixture returned the visible answer without invoking ordinary reply publication; the first S-58 fixture asserted the tool input's flat pair shape against the canonical nested agent entity. Those were fixture errors, not product defects. The final receipts use the production session-worker publication path and the declared `AgentModelConfig` nested pair. Earlier mixed-failure runs are retained as diagnostics only; the Off, refusal, no-leak and atomicity expectations were not relaxed.

Correction: the original pack called S-02 a passing pin and described several unexecuted or incomplete paths as covered. That was wrong: the retired keys still have runtime representations, browser collection is not execution, and downstream assertions blocked by a missing implementation have not been verified. This revision distinguishes those states.

- Specification: `/workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md`.
- Independent inputs: `/workspace/omnipus-coordination/designs-20261029/effort-red-check-report-1.md`, CHECK-01 through CHECK-16; `/workspace/omnipus-coordination/designs-20261029/effort-red-check-report-2.md`, all six new findings and the CHECK-03/CHECK-08 carried residuals. This document is the author's repair ledger, **not independent CHECK**.
- Worktree: `/workspace/wt-effort`; branch `landing/effort`; round-1 pre-repair HEAD `e03c89dc3a22ee2dfb351ac24639d21dfd947892`; round-2 base `1d53b345c9775ec27ad6f1b3b85a542240d2d2fd`; assigned release pin `1d3989df0`.
- Process version: `2026-10-29.1`. Tests and test harnesses only, plus this commissioned traceability update. No production repair, push, mutation or full-suite run.
- Resolution-table commit `SELF` means the local repair commit containing this document; obtain its exact SHA with `git log -1 --format=%H -- /workspace/wt-effort/docs/internal/specs/effort-levels-red-traceability.md`. The handoff report supplies the actual SHA.

## What the statuses mean

| Status | Meaning |
|---|---|
| RED | A named run failed on a specified missing behavior. It is not an implementation PASS or a claim that every later assertion ran. |
| PIN | The named baseline control passed. It is not red-before-green evidence for a new behavior. |
| BLOCKED / U | The test is loud and failing at a missing production seam; later assertions are coded but **unreached**. No skip replaces that failure. |
| PARTIAL | A specified boundary has a test, but a material additional leg is not established; see the limitations ledger. It is never counted as complete scenario acceptance. |
| N/A | Exactly the four deleted scenario tombstones, not unfinished work. |

GREEN, scratch-clone mutation checks, proof-of-failability checklist completion and an independent CHECK verdict are **deferred to CHECK**. No RED suite here is described as feature delivery.

## Round-1 repair resolutions — historical commit, qualifications corrected below

| Finding | Test-side change and remaining qualification | Commit |
|---|---|---|
| CHECK-01 | Canonical archive fixtures now contain exact effort records and 120/zero/absent turn snapshots; source-patch controls, real close/reopen, REST and WS pages assert values, not nil. Added a production three-turn paging workflow with contributor-cut and incremental-journal checks. Its multi-candidate stream prerequisite is loud BLOCKED, not fabricated accounting. | `1d53b345c` |
| CHECK-02 | Versioned config fixtures; legacy `thinking_level` on the provider row; recap fallback actually forced to serve; reopened codecs checked. | `1d53b345c` |
| CHECK-03 | Seeded the agent's own fallback Max and a genuinely default-riding primary with Medium; removed the contradictory no-effort assertion. The positive body oracle was still permissive, not exact; C2-03 below repairs that residual. | `1d53b345c` |
| CHECK-04 | Removed the blanket rejection of legitimate switch-only Off. Per-route recap bodies distinguish Off, unsupported/default behavior and a configured non-default; fallback has its own value. | `1d53b345c` |
| CHECK-05 | Exact mapping outcomes replace the vacuous OR/discarded call. Correct no-Off S-91 target; actual conversation/inheritance/refresh turns plus chat/profile pending-switch cases. Missing command/UI seams remain loud RED. | `1d53b345c` |
| CHECK-06 | Direct encoder/filter tests, actual served and vendored catalog tests, stream/nonstream serialized route/switch/default matrices, Codex matrix and actual Bedrock-family loop. Bedrock has one Chat adapter path, not invented native streaming. Runtime opaque-profile forwarding additionally fails; see accidental finding below. | `1d53b345c` |
| CHECK-07 | Fragmented stream and nonstream decode → private codec → disk reopen → cross-route/back serialization; populated negative carrier controls. Added actual two-file-tool Router loop, actual native tool loops with real streaming writer, and actual Bedrock tool loop/next turn. No-op streamers are not used as persistence proof. | `1d53b345c` |
| CHECK-08 | Actual failed-stream/fallback, debit/cell/snapshot, later reported-zero and absence workflows replace parser-only claims. They currently stop at absent multi-candidate streaming; later accounting/cell checks are U. Parser probes remain boundary tests, not workflow proof. | `1d53b345c` |
| CHECK-09 | Multiple-radio-safe queries, exact no-Medium reopen, actual previous-pair Memory transaction, keyboard/cancel/focus and rejected-save rollback. No expectation was changed to current scaffold output. | `1d53b345c` |
| CHECK-10 | Real WS request capture, nested metadata, radios, validated `entries` history envelopes and position tokens, correct answering-turn note, older-page scroll/reload. All twelve browser cases executed; baseline envelope control passes. | `1d53b345c` |
| CHECK-11 | Offered On for switch-only positives; post-seed revision; explicit T8 fields; persisting callback and reopened config/entity checks; typed slot and generic-composite matrices; actual dispatch transcript and denied-policy test. Missing T8 schema is an explicit fatal failure. | `1d53b345c` |
| CHECK-12 | Seeded Off/capabilities, asserted the unchanged default 150 after refusal, supplied actual choice/reported usage. Added both onboarding modes/variants, validation off/on, whole-transaction refusal/account controls and exact null/type/length errors. Eight absent-effort onboarding controls pass. | `1d53b345c` |
| CHECK-13 | Runtime prompts/input/fallback/retry/deadline/spacing tests through settings and config tools, all output-cap adapter fields, load/use clamp/file/warning checks, and exact due/young restart-age cases. Missing config getters remain loud RED. | `1d53b345c` |
| CHECK-14 | Medium is established before remapping. Held first fallback autosave disables the second slot; next body must contain `[A-prime, B-prime]`. Deferred Save and failure/draft reconciliation retained. | `1d53b345c` |
| CHECK-15 | Real durable conversation store and runtime replacement; actual serving-model request; terminal outbound observation plus the real web command branch. Missing command registration/capability/storage is not hidden by a fake restart. | `1d53b345c` |
| CHECK-16 | Distinct original/retry error markers; fixed validation-off sign-in decoder sentence; independent empty-answer sentinel literal, not the production constant. | `1d53b345c` |

Correction: said the wrapped error came from the retry, wrong because the isolated capture showed one request, correct is no effort retry exists at baseline. The repaired error test requires the original marker 7731, rejects retry marker 9999 and still requires the two-request retry workflow.

## Round-2 resolutions (2026-11-01)

| Finding | Test-side change and remaining qualification | Commit |
|---|---|---|
| C2-01 MEDIUM | Added T-MC `TestT_TL58_ModelChangeWithoutEffortPreservesOffOrRefusesAtomically`: real registry, canonical durable Off Given independent of the missing effort writer, model-only update to `none`-capable and no-Off targets, exact S-58 refusal, whole entity/config/SOUL/revision/sibling atomicity. Both subcases compile and fail on the missing behavior. | SELF |
| C2-02 MEDIUM | Added three real MemorySection cases: exact recommendation once; absent and explicit Off defaults on both slot chips; model/chip → fallback/chip → recommendation → collapsed limits order, no hydration write. API edges only are mocked. Recommendation fails with zero copies; both slot/order cases fail with zero chips, so their later ordering/collapse assertions remain U. | SELF |
| C2-03 WARN / CHECK-03 residual | Every positive loop-wire use now calls A-O `assertOpenRouterLevelBody`: exact `reasoning:{effort:value}` and no foreign carriers. The broad value collector remains only for the negative primary-Medium leak check. Nine instrument cases include flat-only, duplicate/contradictory and foreign encodings. Actual S-51/S-53/refresh requests fail the exact missing-body oracle; command-dependent continuation checks remain U behind missing `/effort`. | SELF |
| C2-04 WARN | Added A-MSG `TestT_C2_S02_LegacyChannelAndEnvNeverDeliverReasoningToAnyDestination`: its own saved legacy key plus env Given, real session-worker turn/normal reply publication, all-destination outbound observation and visible-answer/provider-call controls. Final run observes ordinary answer **and** forbidden reasoning message. Observation is bounded to the existing 10-second harness budget; no claim about arbitrary later detached work. | SELF |
| INFO-1 | Kept production scaffolds untouched. Re-executed S-97 candidate/deadline test (RED), typecheck (exit 2: unused `served`/`target`) and design scanner (exit 1: unpublished picker). The timing seam discards all three parameters and must be wired/deleted by GREEN, not worked around by QA. | SELF |
| INFO-2 / CHECK-08 residual | Removed the hand-built `StreamProviderError` from A-U's critical S-88 test: actual adapter HTTP SSE creates the typed error and carries billed usage into production attempt/stat writers; actual second HTTP response supplies B's usage. It fails because reported reasoning 120 is dropped. This boundary drives the calls explicitly, not automatic fallback. Both actual gateway workflows remain loud BLOCKED at plain Chat; exact dispatcher evidence and unreached legs are detailed below. | SELF |

No finding is dismissed. Test-side repairs are proposed as addressed, **not** self-awarded independent CHECK closure or feature acceptance. Plan: `/workspace/wt-effort/tests/effort/fix-round-2-plan.md`.

## Source key

The short keys below are references, not relative paths. Each resolves to the absolute test file shown here.

| Key | Absolute file |
|---|---|
| A-C | `/workspace/wt-effort/pkg/agent/effort_red_containment_test.go` |
| A-MSG | `/workspace/wt-effort/pkg/agent/effort_red_messenger_containment_test.go` |
| A-O | `/workspace/wt-effort/pkg/agent/effort_red_openrouter_oracle_test.go` |
| A-M | `/workspace/wt-effort/pkg/agent/effort_red_mapping_d2_test.go` |
| A-R | `/workspace/wt-effort/pkg/agent/effort_red_request_rejection_test.go` |
| A-RL | `/workspace/wt-effort/pkg/agent/effort_red_rejection_lifecycle_test.go` |
| A-S | `/workspace/wt-effort/pkg/agent/effort_red_state_records_test.go` |
| A-CMD | `/workspace/wt-effort/pkg/agent/effort_red_conversation_test.go` |
| A-D | `/workspace/wt-effort/pkg/agent/effort_red_dispatch_commands_test.go` |
| A-P | `/workspace/wt-effort/pkg/agent/effort_red_parent_test.go` |
| A-N | `/workspace/wt-effort/pkg/agent/effort_red_native_tool_loop_test.go` |
| A-B | `/workspace/wt-effort/pkg/agent/effort_red_bedrock_loop_test.go` |
| A-OR | `/workspace/wt-effort/pkg/agent/effort_red_router_tool_loop_test.go` |
| A-Q | `/workspace/wt-effort/pkg/agent/effort_red_recap_request_test.go` |
| A-QE | `/workspace/wt-effort/pkg/agent/effort_red_recap_runtime_test.go` |
| A-QA | `/workspace/wt-effort/pkg/agent/effort_red_recap_adapters_test.go` |
| A-QT | `/workspace/wt-effort/pkg/agent/effort_red_recap_timing_workflow_test.go` |
| A-U | `/workspace/wt-effort/pkg/agent/effort_red_usage_test.go` |
| A-T | `/workspace/wt-effort/pkg/agent/effort_red_tool_transcript_test.go` |
| G | `/workspace/wt-effort/pkg/gateway/effort_red_gateway_test.go` |
| G-H | `/workspace/wt-effort/pkg/gateway/effort_red_history_fixture_test.go` |
| G-P | `/workspace/wt-effort/pkg/gateway/effort_red_production_paging_test.go` |
| G-U | `/workspace/wt-effort/pkg/gateway/effort_red_live_turn_test.go` |
| G-MM | `/workspace/wt-effort/pkg/gateway/effort_red_multimodel_test.go` |
| G-N | `/workspace/wt-effort/pkg/gateway/effort_red_native_tool_loop_test.go` |
| G-OB | `/workspace/wt-effort/pkg/gateway/effort_red_onboarding_matrix_test.go` |
| G-REST | `/workspace/wt-effort/pkg/gateway/effort_red_rest_boundary_matrix_test.go` |
| G-PAIR | `/workspace/wt-effort/pkg/gateway/effort_red_pair_routing_test.go` |
| G-TERM | `/workspace/wt-effort/pkg/gateway/effort_red_terminal_branch_test.go` |
| P-W | `/workspace/wt-effort/pkg/providers/common/effort_red_wire_matrix_test.go` |
| P-C | `/workspace/wt-effort/pkg/providers/common/effort_red_continuation_test.go` |
| P-G | `/workspace/wt-effort/pkg/providers/common/effort_red_tool_guard_test.go` |
| P-A | `/workspace/wt-effort/pkg/providers/anthropic/effort_red_carrier_test.go` |
| P-E | `/workspace/wt-effort/pkg/providers/effortdialect/effortdialect_red_test.go` |
| P-K | `/workspace/wt-effort/pkg/providers/catalog/effort_red_served_reasoning_test.go` |
| P-X | `/workspace/wt-effort/pkg/providers/effort_red_codex_recap_test.go` |
| C | `/workspace/wt-effort/pkg/config/effort_red_config_test.go` |
| CMD | `/workspace/wt-effort/pkg/commands/effort_red_commands_test.go` |
| TERM | `/workspace/wt-effort/pkg/app/internal/run/effort_red_terminal_test.go` |
| T | `/workspace/wt-effort/pkg/sysagent/tools/effort_red_tools_test.go` |
| T-B | `/workspace/wt-effort/pkg/sysagent/tools/effort_red_boundary_matrix_test.go` |
| T-K | `/workspace/wt-effort/pkg/sysagent/tools/effort_red_catalog_test.go` |
| T-MC | `/workspace/wt-effort/pkg/sysagent/tools/effort_red_model_change_test.go` |
| UI-P | `/workspace/wt-effort/src/components/ui/model-effort-picker.test.tsx` |
| UI-M | `/workspace/wt-effort/src/components/settings/effort-red-recap-limits.test.tsx` |
| UI-A | `/workspace/wt-effort/src/components/agents/AgentProfile.effort-inheritance.test.tsx` |
| WEB | `/workspace/wt-effort/tests/e2e/effort.spec.ts` |

## Scenario-by-scenario trace

Each active scenario has an explicit row. A named failing test is not a claim that all of that scenario's legs ran. Where a row uses a shared matrix, its row-labelled subtests carry the specific input and oracle.

| Scenario | Test / source reference | Observed state and scope |
|---|---|---|
| S-01 | A-C `TestT_C1_S01_DebugLogCarriesReasoningLength` | RED: missing length-only log contract; marker assertions retained. |
| S-02 | A-MSG `TestT_C2_S02_LegacyChannelAndEnvNeverDeliverReasoningToAnyDestination`; A-C `TestT_C2_S02_ReasoningChannelZeroTrace`; C `TestT_C2_S02_LeftoverReasoningChannelKeysAreInert` | RED, not inferred behavior: own old-key/env Given; actual session-worker normal reply and forbidden reasoning message observed on the common outbound bus. Separate source/codec legs retain round-1 RED evidence. Bounded observation qualification below. |
| S-03 | A-C `TestT_C3_S03_NonTruncatedReasoningOnly` | RED: actual turn/retry/transcript, independent sentinel literal. |
| S-04 | A-C `TestT_C3_S04_SameTextOnMessengerAndParent`; A-P `TestT_C3_S04_RealDelegatedParentReceivesEmptyAnswerSentinel` | RED: real parent/child path supplements messenger observation. |
| S-05 | A-C `TestT_C1_S05_RecapWarnCarriesLengthOnly` | RED: recap extraction and length-only warning. |
| S-06 | A-C `TestT_C4_S06_ReasoningOnlyTaskClassification` | PIN: classification outcome. |
| S-07 | UI-P S-07 cases; WEB S-07/S-08 | RED: no rendered effort control; full radio-content checks U. |
| S-08 | UI-P no-Medium cases; WEB S-07/S-08; P-K filter | RED: picker/server-filter boundaries, no fabricated Medium. |
| S-09 | A-M `TestT_S1_Mapping_D2_Fixture`, `TestT_S1_Mapping_S09_NoteTexts`; UI-P switching | RED: shared exact D2 and actual Memory transaction. |
| S-10 | UI-P `openBlockedStep` cases | RED: established old model/Off, real selection and atomic save checks U after missing chip. |
| S-11 | UI-P keyboard cases | RED: focus, Escape, arrows, Enter/Space, previous-pair preservation coded. |
| S-12 | UI-P S-12; UI-A inherited profile cases | RED: inheritance presentation/control absent. |
| S-13 | WEB first actual WS message case | RED/U: selected real model; missing chip prevents the later exact metadata/pre-send-refresh assertions. |
| S-14 | A-M `TestT_S1_Mapping_S14_Cleared`; UI-P S-14 | RED: exact cleared outcome and rendered absence contract. |
| S-15 | G `TestT_GW_ChatFrameEffortMetadataBoundaries`; G-PAIR `TestT_GW_S15_S75_RealFrameRoutesExactPairAndMapsToServingModel` | RED: real frame routing, exact serving-pair body/mapping and bounded metadata. |
| S-16 | Deleted scenario | N/A tombstone. |
| S-17 | P-W `TestT_P1_S17_S19_SerializedRouteMatrix`; P-E `TestT_P1_S17_TableKnowsEachRoute`; P-X `TestT_C5_S17_S19_CodexRequestMatrix`; A-B family matrix | RED: serialized route forms, actual stream/nonstream adapters where supported, base-family/profile distinction. |
| S-18 | P-W `TestT_P1_S18_SerializedSwitchMatrix`; A-N/G-N native Off/On; P-E filter | RED: exact switch bodies and explicit native Off through real tool loop. |
| S-19 | P-W route matrix; A-S `TestT_R0_S19_CompletedProviderDefaultAndUnchosenRecords`; A-B; P-X matrix | Wire omission PIN subrows, explicit-choice record RED. PARTIAL: completed records are not independently exercised on every serialized adapter route. |
| S-20 | A-R `TestT_R0_S20_ExtraBodyOverrideRecorded` | RED: completed answering-entry override record; actual override body controlled. |
| S-21 | G `TestT_GW_HistoryTransportsCarryEffortRecord`; G-H fixtures; G-U; WEB saved answers | RED: populated exact records/counts across transports; baseline browser envelope PIN. |
| S-22 | P-E/P-K filtering; A-CMD `TestT_CMD_S36_RealRuntimeCapabilityStatusAndRefusals`; UI-P muted case | RED/BLOCKED: server filter and command/control contracts, not already-filtered mocks alone. |
| S-23 | P-A `TestT_P5_S23_EffortClearsTemperature`; P-W matrix | RED on enabling request; Provider-default restoration tested independently. |
| S-24 | A-R `TestT_P2_S24_RejectedEffortRetriedOnce` | RED: full turn, two exact requests and completed record; missing retry at baseline. |
| S-25 | A-R `TestT_P2_S25_FailingRetrySurfacesOriginalError` | RED: original/retry errors differ and original identity is pinned; missing second attempt is explicit. |
| S-26 | A-R `TestT_P2_S26_RejectionRememberedUntilRestart`; A-RL `TestT_P2_S26_ActualToolRejectionRefreshAndNewProcessState` | RED/U: actual tool executes; no effort retry, so remembered rejection/refresh/restart assertions are downstream. |
| S-27 | Deleted/merged scenario | N/A tombstone. |
| S-28 | A-R `TestT_P2_S28_Only400And422TriggerRetry`; A-RL timeout case | RED on retry/body controls; non-retry statuses preserve one call; actual timeout has an in-flight effort-bearing request. |
| S-29 | A-R `TestT_P2_S29_EachAttemptAccountedSeparately` | RED: real completed-turn accounting requires retry, not fabricated transcript entries. |
| S-30 | P-C fragmented/nonstream two-round codec workflow; A-OR `TestT_C6_S30_S31_S32_ActualRouterToolLoopAndDurableRestart` | RED: actual two-tool nonstream engine projection loses opaque members. Fragmented stream decoder/codec boundary also RED. |
| S-31 | P-C `TestT_C6_S31_ThinkingBlocksSurviveDurableCodecRestart`, two-round workflow; A-OR | Native codec PIN; private-carrier workflow BLOCKED/RED; actual model store close/reopen and routed serialization executed. |
| S-32 | P-C route change/back; A-OR actual loop restart and cross-route/back | RED: populated opaque items lost, not a nil-carrier absence-only test. |
| S-33 | P-C `TestT_C6_S33_PopulatedPrivateCarrierCrossRoute`; P-A native carrier; A-OR | Native own-carrier PIN; populated OpenRouter isolation tests require visible/tool positive controls. |
| S-34 | A-N `TestT_P5_S34_S90_RealNativeToolExecutionRecordsAndNextLoop`; G-N streaming twin; P-G | RED: actual current-loop guard, High/On, final writer and subsequent-turn positive controls. |
| S-35 | A-B `TestT_P5_S17_S19_S35_S90_BedrockBaseFamilyAndActualLoop`; P-G | RED: actual file result, second Bedrock body, continuation record and next turn; opaque-profile issue separately reported. |
| S-36 | CMD `TestT_CMD_Effort_S36_RegisteredWithSpecContract`; A-CMD real runtime status/refusals | BLOCKED: command missing; exact replies/options/length/status cases remain loud. |
| S-37 | A-CMD `TestT_CMD_S37_S38_S39_RealConversationScopeServingModelAndRestart`; CMD S-38 | BLOCKED/U: real two-session scope workflow, not a fake persistence runtime. |
| S-38 | A-CMD same workflow, external serving-model reload; CMD S-38 | BLOCKED/U: real request/body/record assertions follow missing command. |
| S-39 | A-D `TestT_CMD_S39_RealLoopAndStoreRestartKeepsConversationEffort`; A-CMD; CMD S-39 | BLOCKED/U: actual runtime/store replacement, no reused-runtime restart claim. |
| S-40 | CMD `TestT_CMD_Effort_S40_NeverCallsSwitchModel` | BLOCKED: missing command; no agent-wide model switch allowed. |
| S-41 | WEB local command case; CMD `TestT_CMD_Effort_S41_WebDeliveryClientNoServerRecord` | RED/U: no chat chip/local handler, later no-send/new-conversation checks unreached. |
| S-42 | CMD `TestT_CMD_Effort_S42_UnknownUnofferedAndInputForms`; A-CMD | BLOCKED: exact fixed forms/refusals await registered command. |
| S-43 | A-U `TestT_U1_S43_CallEntryCarriesReasoningTokens`; G-U `TestT_GW_RealTurnDisplaysReportedReasoningSnapshot`; G-MM | RED: actual usage booking/writer; multi-model cell checks blocked as below. |
| S-44 | A-U `TestT_U1_S44_UnreportedReasoningStaysAbsent`; WEB saved-answer absent case | PIN at backend absence boundary; UI absence/not-reported checks U after missing new-field rendering. |
| S-45 | P-E `TestT_P1_S45_FilterServed`; P-K `TestT_K1_S45_ServedReasoningFilteredByEncoderTable` | RED: actual server filter matrix, no longer an active declared GAP. |
| S-46 | P-K `TestT_K1_S46_VendoredCatalogCarriesReasoning` | RED: actual vendored data checked, not assigned away. |
| S-47 | Deleted scenario | N/A tombstone. |
| S-48 | A-S `TestT_S3_S48_S49_CatalogRefreshPreservesStoredChoice`; A-O exact oracle; A-M stale rows | RED: actual refreshed request/record and byte-preserved choice; High requires exact nested OpenRouter encoding and no foreign carriers. |
| S-49 | A-S same actual refresh workflow; A-O exact oracle; A-M stale rows | RED: Max remains stored while request/record must map to exact nested High; flat/contradictory encodings cannot satisfy the oracle. |
| S-50 | G `TestT_GW_AgentPrimaryEffortSavedAndReadBack`; C codecs | RED: strict shape still refuses new effort; exact GET/disk/reopen checks coded. |
| S-51 | A-R `TestT_S2_S51_DefaultModelEffortUsed`; A-O exact oracle | RED: explicit Medium default and a genuinely default-riding agent; exact `reasoning:{effort:medium}`, foreign carriers forbidden. |
| S-52 | A-R `TestT_S2_S52_UnsetFallbackSendsNothing` | PIN: actual fallback carries no primary/conversation effort; not a positive-effort implementation claim. |
| S-53 | A-R `TestT_S2_S53_FallbackUsesOwnSetting`; A-O exact oracle | RED: own agent fallback Max seeded; exact `reasoning:{effort:max}`, foreign carriers forbidden. The negative primary-Medium check still searches both flat/nested carriers. |
| S-54 | UI-P contextual fallback-name case | RED: standalone component/control name boundary; all caller integrations not established. |
| S-55 | G `TestT_GW_AgentNameOnlyEditKeepsStoredEffort` | RED: existing stored effort must survive non-effort edits unchanged. |
| S-56 | G `TestT_GW_AgentEffortSaveValidationRows`; A-M/runtime state rows | RED: REST valid/unoffered/length storage boundary; complete REST-save-to-request workflow PARTIAL. |
| S-57 | UI-P rejected mapped Memory autosave case | RED/U: real parent transaction, server rejection, acknowledged pair/value/focus/FormError rollback coded. |
| S-58 | G `TestT_GW_AgentModelChangeWithoutEffortMappedOrBlocked`; T-MC `TestT_TL58_ModelChangeWithoutEffortPreservesOffOrRefusesAtomically`; A-M | RED: direct REST and tool boundaries, stored Off independent of the absent writer; target with none retains Off; no-Off target must return exact refusal and preserve all durable/runtime state. Tool currently drops Off and commits forbidden model/sibling changes. |
| S-59 | G `TestT_GW_DefaultModelEffortStored`; C default codec | RED: exact pair+effort persistence. Default-settings UI caller is not independently exercised here. |
| S-60 | G `TestT_GW_RecapEffortAndProviderStored`; A-Q; UI-M `MemorySection — S-60 recap presentation` (three named cases) | RED: real Memory recommendation count is 0 not 1; absent/explicit-Off slot cases find 0 not 2 chips. Exact ordering/collapsed-area checks are coded but U behind the missing controls. REST persistence and request execution remain separate boundaries. |
| S-61 | G-OB four named matrix tests; G onboarding tests; WEB wizard case | Eight absent-effort transaction controls PIN; valid High/refusal texts RED on both variants/modes. Wizard reaches model selection then missing Change now; final completion-capture assertion U. |
| S-62 | G `TestT_GW_ProviderDeleteNewDefaultCarriesEffort` | RED: replacement pair+effort write through real delete handler. Provider-removal UI caller not independently exercised here. |
| S-63 | T `TestT_TL63_UpdateAgentSavesAndReadsEffort`; A-T `TestT_Tools_S63_ActualDispatchStoresEffortCallAndResult` | RED: valid switch-only value, actual registry/dispatch/durable transcript/result. |
| S-64 | T `TestT_TL64_UpdateAgentRefusesUnofferedAndOversized`; T-B typed matrix | RED: per-primary/fallback refusal options, length and byte-identical entity/sibling atomicity. |
| S-65 | A-D `TestT_Tools_S65_DeniedEffortWriteCannotChangeBytes` | PIN: actual denied compositor dispatch; config/entity bytes identical. No active GAP remains. |
| S-66 | T `TestT_TL66_CreateAgentKeepsFallbackEfforts` | RED: valid offered typed values and fallback preservation; baseline refuses new field rather than silently dropping it. |
| S-67 | T `TestT_TL67_GetAgentEffortOptionsPerSlot`; T-K D8 matrix | BLOCKED/U: missing T8 fields/model_chain, explicit capability/slot rows await that seam. |
| S-68 | T `TestT_TL68_SetConfigWritesDefaultModelEffort`; T-B composites | RED: actual persistence callback, reopened values, no catalog check for generic writes. |
| S-69 | C `TestT_D1_S69_ThinkingLevelKeyDeleted`; P-A option deletion | RED: versioned config puts legacy key on its real provider row and requires it inert. |
| S-70 | TERM `TestT_R1_S70_TerminalSendsNoEffortMetadata`; G-TERM actual web command branch; CMD S-70 | Terminal outbound PIN; shared command branch BLOCKED until command exists. |
| S-71 | A-Q `TestT_C5_S71_RecapDefaultOffPerRoute`, `TestT_C5_S71_RecapNoReasonDataSendsNothing`; P-X default Off | RED on missing disable encodings; no-reason-data absence PIN. Legitimate switch false is allowed. |
| S-72 | A-M `TestT_S1_Mapping_S72_Inherited`; UI-P inherited presentation | RED: exact map/note, broader caller state remains separately qualified. |
| S-73 | A-M `TestT_S1_Mapping_S73_NothingSendable`; UI-P escape/cancel transaction cases; UI-A/WEB inherited cases | RED/U: explicit Provider default escape stores only after choice; Cancel/Escape retain old pair. No omitted trace row. |
| S-74 | UI-P actual Medium→Low Memory transaction with Change link | RED/U: saves once; Low reopens; Medium forbidden; not a pre-painted remap fixture. |
| S-75 | G-PAIR `TestT_GW_S15_S75_RealFrameRoutesExactPairAndMapsToServingModel` | RED: omitted-provider path and exact serving-model capability mapping. |
| S-76 | T `TestT_TL76_GetAgentFieldsProjection`; T-B `TestT_TL76_SchemaEnumAndRuntimeFieldNamesAgree` | BLOCKED/U: missing fields schema; advertised/runtime enum parity and fixed error checks coded downstream. |
| S-77 | T `TestT_TL77_ExternalCLIWorkerRefusesEffort` | BLOCKED/U at dependency where applicable; explicit fields on read, fixed external-worker reason expected. |
| S-78 | A-Q `TestT_C5_S78_ConfiguredRecapEffortReachesBody`, `TestT_C5_S78_RecapFallbackServesWithItsOwnEffort` | RED: versioned loaded config and forced fallback's own value reach actual adapters. |
| S-79 | G defaults/ranges; A-QE default runtime rows | RED: defaults observed at runtime as well as storage; no storage-only completion claim. |
| S-80 | A-QE runtime matrices; A-QA `TestT_L4_S80_RecapCapReachesEverySerializedAdapterField`; A-Q limits | RED: prompt/input/words/items/tail/retries/pause/deadline/chars/spacing paths; output fields independently encoded. |
| S-81 | G ranges both validation modes; A-QE `TestT_L4_S81_S83_ClampOnlyInUseWarnsOnceAndPreservesDisk` | RED/BLOCKED: bounds, loaded use-time clamps, file unchanged and once-per-key warnings; no default-disappears contradiction. |
| S-82 | UI-M T-L4 cases | RED/U: pane/default summary, labels/help/ranges/reset/save; missing controls prevent later assertions. |
| S-83 | T `TestT_TL83_RecapLimitKeysRoundTrip`, `TestT_TL83_RecapSummaryWordsStoresThousand`; A-QE config-tool rows | RED: real get/set/section/reopen plus ensuing runtime effects/clamping; no omitted trace row. |
| S-84 | A-QE `TestT_L4_S84_BootstrapUsesConfiguredIdleAge` | RED: actual catch-up due/young sessions at 12/8 minutes for idle 10 and 31/29 at default 30. No indirect-coverage claim. |
| S-85 | A-C `TestT_C4_S85_OutputLimitedReasoningOnly` | PIN: existing truncation/classification outcome; not new GREEN. |
| S-86 | A-M exact inherited states; UI-A five profile cases; WEB five actual chat cases | RED/U: effective inherited Off established; level/escape/cancel transactions await missing effort chip. |
| S-87 | A-CMD `TestT_S2_S87_InheritedHighChangedToOffAtRuntime`; A-M | BLOCKED/U: actual changed inherited state and next request/record, not discarded mapper result. |
| S-88 | G-MM `TestT_GW_S88_RealFailedStreamFallbackLaterZeroAndHistory`; A-U `TestT_U1_S88_FailedAttemptKeepsItsCount`, reported-zero workflow | Adapter/accounting RED: actual HTTP SSE creates the error; parsed, booked and durable-cell reasoning 120 is missing. Automatic loop fallback/live snapshots remain BLOCKED/U because the multi-candidate callback calls plain Chat; the strengthened boundary test does not claim that automatic workflow. |
| S-89 | P-X `TestT_C5_S89_CodexRecapCarriesEffortAndNoOutputLimit`, default Off; UI-M help | RED: actual Codex request fields; screen help checks are U after missing pane. |
| S-90 | A-N/G-N actual Off/High/On tool loops; A-B Off mapped out | RED: Off disable on both native requests, next user turn; Bedrock Off yields not_supported before continuation, never a fabricated second reason. |
| S-91 | A-S `TestT_S3_S91_OwnOffSurvivesExternalModelChange`; A-M corrected no-Off target | BLOCKED/U for own conversation Off; exact unsupported record and no body effort after external model change, stored Off retained. |
| S-92 | T `TestT_TL92_CredentialMaskingStillWorks` | PIN: exact recap-key exceptions do not open credential fragments, section redaction retained. |
| S-93 | UI-M held fallback array, deferred Save, failure/draft and slot-only autosave cases | RED/U: actual Memory parent/API edge; required [A-prime,B-prime] and one-writer assertions remain behind missing controls. |
| S-94 | T/T-B composite and typed matrices; G-REST `TestT_GW_S94_RESTNullEmptyOmittedAndLengthMatrix`, `TestT_GW_S94_OmissionKeepsAndEmptyClearsStoredOverride`; G-OB | RED: seven REST slot boundaries × both validation modes × seven input classes, plus five stored-High omission/clear workflows. Twelve omitted baseline controls PASS; two Memory validation-on controls are explicit BLOCKED schema failures. Existing-slot seed/change assertions U where the initial High write is not implemented. |
| S-95 | G populated archive pages; G-P `TestT_GW_S95_ProductionThreeTurnsOlderRefreshAndContributorCut`; WEB older/reload | Transport fixture RED; production full-accounting/page workflow BLOCKED/U at absent multi-candidate streaming. Browser reaches older mapped answer, fails its missing exact note. |
| S-96 | Deleted scenario | N/A tombstone. |
| S-97 | A-Q `TestT_L8_S97_OneOverallTimeLimit`; A-QT `TestT_L8_S97_RealCandidateOrderAndHeuristicOutcomes` | RED: real candidates/deadlines/order/heuristic effects; pause-crossing/no-further-call and transient reset cases, not just allocated budget. |

## Round-1 execution receipts — historical, not re-labelled as final round-2 runs

Every listed log records the actual command and raw child exit. Go runs use `CGO_ENABLED=0`, tags `goolm,stdjson`, `-p 1`, `-count=1`, `-v` and an explicit exact-name alternation restricted to this feature in one package. All processes ran serially in the foreground through plain `/usr/local/bin/heavy`. A RED exit 1 is not a gate PASS.

| Scope | Top-level executed result | Raw exit | Saved log |
|---|---|---:|---|
| Agent repair group, including actual Router two-tool/restart | 57 named tests: 7 PASS pins, 50 FAIL. Router calls/results, reopened history and route change/back reached; carrier equality fails. | 1 | `/workspace/logs/effort-red/fix1-authoritative-agent.log` |
| Gateway, including REST input/state matrices | 27 named tests: 3 PASS pins, 24 FAIL; eight absent-effort onboarding and twelve omitted-effort REST controls PASS; two Memory validation-on omitted controls BLOCKED. | 1 | `/workspace/logs/effort-red/fix1-authoritative-gateway-with-state.log` |
| Tools | 17 named tests: 2 PASS pins, 15 FAIL; explicit T8 fatal | 1 | `/workspace/logs/effort-red/fix1-reconciled-tools.log` |
| Config | 4 named tests: 4 FAIL, now on feature fields, not missing version | 1 | `/workspace/logs/effort-red/fix1-final-pkg-config.log` |
| Encoder table/filter | 2 named tests: 2 FAIL | 1 | `/workspace/logs/effort-red/fix1-final-pkg-providers-effortdialect.log` |
| Served/vendored catalog | 2 named tests: 2 FAIL | 1 | `/workspace/logs/effort-red/fix1-final-pkg-providers-catalog.log` |
| Common adapters/continuation | 8 named tests: 2 PASS pins, 6 FAIL | 1 | `/workspace/logs/effort-red/fix1-final-pkg-providers-common.log` |
| Codex | 3 named tests: 3 FAIL | 1 | `/workspace/logs/effort-red/fix1-final-pkg-providers.log` |
| Native adapter | 4 named tests: 1 PASS pin, 3 FAIL | 1 | `/workspace/logs/effort-red/fix1-final-pkg-providers-anthropic.log` |
| Command contract | 8 named tests: 8 FAIL, missing registration | 1 | `/workspace/logs/effort-red/fix1-final-pkg-commands.log` |
| Actual terminal outbound | 1 named test PASS pin | 0 | `/workspace/logs/effort-red/fix1-final-pkg-app-internal-run.log` |
| Picker component file | 21 cases executed, 21 FAIL | 1 | `/workspace/logs/effort-red/fix1-final-ui-picker.log` |
| Memory component file | 10 cases executed, 10 FAIL | 1 | `/workspace/logs/effort-red/fix1-final-ui-memory.log` |
| Agent profile component file | 5 cases executed, 5 FAIL | 1 | `/workspace/logs/effort-red/fix1-profile-final.log` |
| Browser file, actual runtime | 12 cases executed, 11 FAIL, 1 PASS envelope control; zero retries | 1 | `/workspace/logs/effort-red/fix1-reconciled-browser.log` |
| Typecheck, heap 4096 | Test-side option errors fixed; only two production scaffold unused-parameter diagnostics remain | 2 | `/workspace/logs/effort-red/fix1-authoritative-typecheck.log` |
| Scoped ESLint | PASS on the three changed component-test files, browser spec and test-only Playwright config | 0 | `/workspace/logs/effort-red/fix1-eslint-final.log` |
| Targeted Go vet | PASS on the eleven explicit touched feature packages, final Go source | 0 | `/workspace/logs/effort-red/fix1-final-source-go-vet.log` |
| Design-system scanner | Unpublished effort component import is new debt; no exception or suppression added | 1 | `/workspace/logs/effort-red/fix1-current-lint-design-system-locks.log` |
| Function-size budgets | PASS with warnings; no non-grandfathered >240 failure; final test source | 0 | `/workspace/logs/effort-red/fix1-final-source-function-budget.log` |
| File-size budgets | PASS; no effort-file warning/failure; final test source | 0 | `/workspace/logs/effort-red/fix1-final-source-file-budget.log` |
| Mechanical handoff audit | 97 unique scenario rows, exact four tombstones, all 16 resolution rows, 133 named Go results, 44 matching source fingerprints; test-only scope and no removed Go test functions/skips/suppressions | 0 | `/workspace/logs/effort-red/fix1-precommit-scope-and-receipts.log` |

The Go logs above contain **133 distinct top-level named tests: 16 baseline PASS pins and 117 FAIL**, including loud prerequisite failures. Component files execute 36 cases, all FAIL; the browser executes twelve, eleven FAIL and one baseline PASS. These are targeted feature runs, never full-suite results. The machine-readable handoff at `/workspace/wt-effort/tests/effort/fix-round-1-receipts.json` indexes each named Go result, raw-exit receipt and saved-log digest. The source digests in that index are frozen round-1 handoff fingerprints, not run-time measurements. Round-2 modified files intentionally differ; use `/workspace/wt-effort/tests/effort/fix-round-2-receipts.json` for the current handoff.

The sixteen top-level Go pins are:

- Agent: `TestT_C4_S85_OutputLimitedReasoningOnly`, `TestT_C4_S06_ReasoningOnlyTaskClassification`, `TestT_Tools_S65_DeniedEffortWriteCannotChangeBytes`, `TestT_C5_S71_RecapNoReasonDataSendsNothing`, `TestT_S2_S52_UnsetFallbackSendsNothing`, `TestT_R0_S19_ProviderDefaultSendsNothing`, `TestT_U1_S44_UnreportedReasoningStaysAbsent`.
- Gateway: `TestT_GW_OnboardingInvalidPlacementsRefused`, `TestT_GW_S61_InvalidPlacementsKeepWholeTransactionUnchanged`, `TestT_GW_S61_AuthorityPinsWithValidEffortShape`.
- Tools: `TestT_TL92_CredentialMaskingStillWorks`, `TestT_TL94_AllowFromScalarToListControl`.
- Common/native adapters: `TestT_C6_S33_SerialisersNeverEmitDetailsCrossRoute`, `TestT_C6_S31_ThinkingBlocksSurviveDurableCodecRestart`, `TestT_P5_S33_ThinkingBlocksAreTheOnlyAnthropicCarrier`.
- Terminal outbound: `TestT_R1_S70_TerminalSendsNoEffortMetadata`.

The authoritative browser run completed and copied back successfully. Some later plain-heavy commands automatically fell back to a local slot after runner sync/admission failures; their final raw command exits, not the sync warnings, are the results above. No manual `--local` bypass was used. Earlier failed-run/copy-back/OOM receipts remain diagnostic history, not substitute results. A prior default-heap typecheck exited 134; the larger-heap result above is the meaningful compiler diagnostic run. Tests that naturally pass at baseline are pins, not falsely described as new RED proofs.

## Round-2 execution receipts — final source, serial foreground

All ten authoritative heavy commands below were invoked as plain `/usr/local/bin/heavy`; no manual `--local` bypass, background job, subagent, push or full suite. The wrapper itself reported runner loss/admission refusal and selected a local slot; that fallback is disclosed, not relabelled a remote success. Raw child exits are saved directly, never inferred from a pipe.

| Scope | Named result / right-reason evidence | Raw exit | Saved log |
|---|---|---:|---|
| Tools S-58 | 1 top-level FAIL; both subcases FAIL. `off` becomes absent; no-Off update incorrectly succeeds and changes entity/revision. Canonical pair and valid Given controls pass. | 1 | `/workspace/logs/effort-red/fix2-tools-final.log` |
| Agent touched cases plus controls | 12 top-level: 8 FAIL, 4 PASS. Exact nested body missing; actual S-02 answer **and** reasoning delivered; real failed-stream reasoning 120 dropped. Missing `/effort` still blocks its later state/wire steps. S-97 fails on discarded injected deadline/retry values. | 1 | `/workspace/logs/effort-red/fix2-agent-final.log` |
| Gateway S-88/S-95 | 2 top-level FAIL, both explicit `BLOCKED`: real fallback finishes, primary `stream` is absent, not true. All later accounting/live/history assertions U. | 1 | `/workspace/logs/effort-red/fix2-gateway-blocks.log` |
| Memory file | 13 executed FAIL: 3 new S-60 cases (0 recommendation copies, 0 chips); existing 10 S-82/S-93 cases retain their missing-control failures. No collection/type errors. | 1 | `/workspace/logs/effort-red/fix2-memory.log` |
| Targeted Go vet | PASS, only agent/gateway/sysagent-tools packages | 0 | `/workspace/logs/effort-red/fix2-go-vet.log` |
| ESLint | PASS, only the touched Memory test file | 0 | `/workspace/logs/effort-red/fix2-eslint.log` |
| Typecheck | Only production unused scaffold parameters `served` and `target`; no new test diagnostic | 2 | `/workspace/logs/effort-red/fix2-typecheck.log` |
| Required design scanner | Same unpublished effort-picker import debt; no exception added | 1 | `/workspace/logs/effort-red/fix2-design-system.log` |
| Function budget | PASS with warnings retained, including existing long feature tests | 0 | `/workspace/logs/effort-red/fix2-function-budget.log` |
| File budget | PASS with warnings retained | 0 | `/workspace/logs/effort-red/fix2-file-budget.log` |

The authoritative Go set contains **15 unique top-level tests: 11 FAIL and 4 PASS**. Three existing controls remain PASS (`TestT_S2_S52_UnsetFallbackSendsNothing`, `TestT_R0_S19_ProviderDefaultSendsNothing`, `TestT_U1_S44_UnreportedReasoningStaysAbsent`); the fourth is the new instrument PIN `TestT_Instrument_OpenRouterLevelOracleRejectsForeignEncodings` (nine case-labelled inputs). That instrument PIN is not a new-behavior GREEN, production mutation proof or feature acceptance. Every new/changed behavioral test is observed RED; written assertions behind missing commands/controls remain U. This is a narrow no-control-regression observation, **not** an unrun whole-suite regression claim.

The round-2 index `/workspace/wt-effort/tests/effort/fix-round-2-receipts.json` records commands, raw exits, named tests, log SHA-256 values and final-source handoff fingerprints. The earlier `/workspace/logs/effort-red/fix2-tools-s58.log` and `/workspace/logs/effort-red/fix2-agent.log` contain the corrected fixture mistakes and are diagnostic history only. `/workspace/logs/effort-red/fix2-messenger-final.log` is a confirming isolated run of the corrected final messenger source, superseded for aggregate counting by the final agent run. None replaces a final-source receipt or changes a RED into PASS.

### CHECK-08 / INFO-2 — exact production block

Read `/workspace/wt-effort/pkg/agent/loop_run_turn.go::agentLoopRunTurn.callProviderOnce`: inside the fallback-chain callback, **one** active candidate calls `runProviderAttempt`; **multiple** candidates resolve their configured client and call `p.Chat` directly, then `accountProviderAttempt`. Read `/workspace/wt-effort/pkg/agent/loop_run_turn_attempt.go::agentLoopRunTurn.runProviderAttempt`: the `StreamingProvider`/registered-streamer selection and `ChatStream` call live there. The multiple-candidate branch never enters that selection, even though the gateway fixture installs its real stream delegate. Thus A receives no `stream:true` request, its SSE usage/error branch cannot run, and the real fallback completes on the intentionally valid plain-HTTP 402 path. Both gateway tests then fatally require `streamed == true` rather than accepting that plain fallback.

The strengthened A-U test independently forces the real adapter's `ChatStream` against actual SSE and passes the **adapter-produced** error to `accountProviderAttempt`, then books the actual model-B response through the production writer. It removes the old hand-constructed error blind spot, but explicitly drives selection at this adapter/accounting boundary. It does not prove automatic multi-candidate stream dispatch. At RED the actual error retains normal billed output but loses reported reasoning 120; that is a real failing assertion, not missing fixture setup.

Consequently the automatic failed-stream → fallback turn sum, answering-entry snapshots, live done totals, later B zero, model-cell no-double-booking, S-95 turns 2/3 and real tool, contributor-cut older pages, refresh and incremental replay are still **UNREACHED in these production workflows**. Their assertions remain loud and intact. GREEN owns the production dispatch/usage implementation; independent CHECK must re-drive these exact tests after implementation. QA has no authority to change production dispatch or weaken either stream prerequisite.

## Limitations and implementation handoff — do not silently mark complete

1. **Production seams:** `get_agent` still lacks T8 `fields`/`model_chain`/options; the effort command and UI controls are missing; multi-candidate fallback uses nonstream Chat. Loud tests exist. Later T8 enum/refusal, browser transaction, failed-stream accounting/cell/page and command persistence assertions remain U until implementation, then independent CHECK must verify them. Memory validation-on additionally asks for an absent inbound schema name; see item 10.
2. **Additional observed baseline issue:** the real Bedrock factory returns an opaque configured inference-profile ID, but the actual loop requests the catalog base ID. The new test asserts the profile path and fails; it also independently exposes missing effort bodies/records. This is reported for implementation triage, not repaired in production, and it means the opaque-ID family-selection leg is not yet demonstrated end to end.
3. **Residual breadth:** Provider-default wire omission is enumerated across routes, but completed-answer record shape is not independently driven on every route. The six UI call sites are not all exercised as parent integrations: chat, profile, Memory and onboarding have direct paths; default-settings and provider-removal UI paths do not. These are **remaining test-coverage gaps**, not implicit GREEN obligations or an independent whole-feature completeness PASS. S-94 now has the seven-slot REST matrix (98 subcases) and ten seeded state subcases; successful omission/clear after stored High remains U where accepting the seed is missing, not an unwritten-test gap.
4. **Carrier boundary distinction:** the fragmented-stream two-round codec test deliberately supplies tool-result messages at its boundary; the separate actual Router two-tool workflow tests real engine projection and persistence nonstreaming. The actual streaming engine tool/writer workflow is native Anthropic. Do not describe these as an executed full OpenRouter streaming gateway-restart campaign.
5. **Atomic-write instrument:** onboarding's existing self-write registry counts distinct committed config hashes, not identical repeated OS writes. The absent-effort controls establish existing account/session behavior and expected distinct-commit counts. This is not a filesystem syscall-count proof. The wizard also checks no follow-up default-model API mutation, but that assertion remains U at RED.
6. **Gates left red, production ownership unchanged (INFO-1):** `/workspace/wt-effort/pkg/agent/effort_red_scaffold_timing_seam.go::callCandidatesWithTiming` discards `timeLimit`, `streamRetries` and `retryPause` before calling `callCandidates`; the unchanged S-97 workflow remains RED on actual deadline/retry/candidate effects. `/workspace/wt-effort/src/lib/effort/capabilities.ts::classifyEffortPresentation` and `/workspace/wt-effort/src/lib/effort/mapEffort.ts::mapEffort` throw scaffold errors and have unused `served`/`target` parameters (typecheck exit 2). `/workspace/wt-effort/src/components/ui/model-effort-picker.tsx` is not published through the design-system contract (scanner exit 1). GREEN must wire or remove these scaffolds and publish the real picker. QA neither edits those production files nor evades these gates.
7. **External FRs:** FR-068 user-facing documentation must be drafted with implementation and independently audited. FR-026 contract-first commit ordering cannot be proved by schema-presence assertions; it requires implementation history. Neither is met by this RED pack.
8. **Dataset claims:** D2 is a shared exact fixture; D3/D4/D5/D6/D7/D8 now have additional real-boundary cases. No claim of exhaustive dataset acceptance, every possible REST writer, cross-platform certification, UAT, full-suite CI, or measured mutation kills is made.
9. **Impact:** GitNexus tool discovery returned no matching deferred tools. Round-2 pre-edit caller/reference sweeps are saved at `/workspace/logs/effort-red/fix2-impact.log`; historical round-1 sweeps include `/workspace/logs/effort-red/fix1-final-validation-impact.log`, `/workspace/logs/effort-red/fix1-router-caller-impact.log` and `/workspace/logs/effort-red/fix1-rest-matrix-impact.log`. Final scope/diff review substitutes for unavailable `detect_changes`, labelled **Inferred**, never a fabricated graph result.
10. **Additional observed baseline issue — Memory schema:** `/workspace/wt-effort/pkg/gateway/rest_memory_settings.go::putMemorySettings` passes `UpdateMemorySettingsJSONRequestBody` to the inbound validator, while the embedded file is `/workspace/wt-effort/pkg/gateway/inboundschemas/MemorySettings.yaml`. With validation on, even an omitted-effort baseline write returns HTTP 500 `inbound schema unavailable`. `requireEffortMemorySchema` makes this explicitly fatal BLOCKED before the new-field oracle; it affects both Memory slots, including the two otherwise-baseline omitted controls. No production alias/schema or validator bypass was added.

11. **S-02 observation boundary (C2-04):** the real session worker publishes the normal answer and the test observes every message on the common outbound bus, with no channel/chat filter, for the existing 10-second harness budget after turn completion. Connector intake/admission selection is outside this test, and reasoning publication is currently detached rather than joined. The final run proves the present leak within that window; a future absence result does not prove nothing can be scheduled arbitrarily later. Independent CHECK retains that limit; the zero-trace guard remains separate.

Code correct and tested: **not established for the feature**; this is a failing-first test repair with passing baseline controls and explicitly red gates.

Reachable by a user or agent: **not established**; missing controls/command/schema remain visible, and exact-commit UAT plus post-landing in-product testing are separate obligations.

skills: omnipus-shared-rules, elicify-test-writing, omnipus-design-system.

## Evidence and self-check — round 2

| Claim | Evidence | Certainty |
|---|---|---|
| Independent input was used, not self-CHECK | `/workspace/omnipus-coordination/designs-20261029/effort-red-check-report-2.md` read in full; six-row round-2 resolution table, including both carried residuals | Verified |
| S-58 tool model-change cases fail on the missing behavior | `TestT_TL58_ModelChangeWithoutEffortPreservesOffOrRefusesAtomically`; `/workspace/logs/effort-red/fix2-tools-final.log`, raw exit 1: Off absent / forbidden update accepted and committed | Verified RED, not GREEN |
| S-60 Memory cases execute | `/workspace/logs/effort-red/fix2-memory.log`, raw exit 1: 13 executed failures; exact recommendation 0/1, chips 0/2. Later order/collapse assertions U. | Verified RED; downstream assertions Unknown |
| Exact loop oracle and its invalid-encoding instrument controls execute | `/workspace/logs/effort-red/fix2-agent-final.log`, raw exit 1: exact nested-body assertions fail; nine instrument subcases PASS. Three pre-existing controls also PASS. | Verified; instrument PIN is not mutation proof |
| S-02 drives ordinary delivery and catches reasoning on another destination | Same final agent log: `Published outbound response` for `s02-person`; actual outbound list includes both that answer and a separate reasoning marker. Own legacy-config/env Given; bounded observation. | Verified leak in the observed window |
| S-88 uses an actual adapter-produced stream error; automatic workflows remain BLOCKED | Same final agent log: actual SSE error loses reasoning 120. `/workspace/logs/effort-red/fix2-gateway-blocks.log`, raw exit 1: two named `BLOCKED` failures after actual plain fallback. Production `callProviderOnce` / `runProviderAttempt` branches read. | Verified boundary RED and block; later live/history behavior Unknown |
| Static passes and production-owned red gates remain distinct | Final vet/ESLint/budget logs raw exit 0; typecheck raw exit 2; design scanner raw exit 1; exact commands and SHA-256 receipts in `/workspace/wt-effort/tests/effort/fix-round-2-receipts.json` | Verified narrow gate results; no full-suite claim |
| All 97 scenario IDs have explicit rows; exactly four tombstones | `/workspace/logs/effort-red/fix2-precommit-scope-and-receipts.log`, exit 0: `TRACE: 97 unique scenario rows; 93 active; 4 exact tombstones; 6 round-2 resolution rows` | Verified structural trace check, not scenario-completeness acceptance |
| Scope/impact fallback is disclosed | `/workspace/logs/effort-red/fix2-impact.log`; no GitNexus tools at discovery; final source/diff sweep instead of unavailable `detect_changes` | Inferred impact only; no graph verdict |
| Self-check | Re-read final repair diff/artifacts against all six new findings and CHECK-03/CHECK-08 residuals; checked raw exits, named results, log digests, current-source fingerprints, row IDs, no deleted/skipped tests, unchanged production and formatting. `/workspace/logs/effort-red/fix2-precommit-scope-and-receipts.log`, exit 0. Exact staged scope/commit is separately recorded in the handoff. GREEN, scratch-clone mutations/proof-of-failability and independent CHECK remain deferred. | Verified author self-check only |
