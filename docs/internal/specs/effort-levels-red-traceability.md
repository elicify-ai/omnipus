# Effort levels — RED test traceability (train T7, branch `landing/effort`)

Spec: `/workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md` (grill r9 PASS, r10 minor applied).
Pin: `origin/release/v0.1.1` = `1d3989df0` — matches the spec's code pin; no drift found at the pin.
RED proof logs + exit codes: `/workspace/logs/effort-red/` (`go-*.log`, `vitest-effort.log`, `e2e-collection.log`, `no-reg-*.log`, `go-tools-pins.log`).
RED scaffolding (behaviour stubs, clearly marked): `pkg/agent/reasoning_effort_map.go`, `pkg/providers/effortdialect/effortdialect.go`, `pkg/agent/effort_red_scaffold_timing_seam.go`, `src/lib/effort/mapEffort.ts`, `src/lib/effort/capabilities.ts`, `src/components/ui/model-effort-picker.tsx`, `src/lib/effort/fixtures/effort-mapping-d2.json` (shared Go/TS fixture, SC-002).

Status legend: **RED** = fails today on the missing feature (correct RED); **PIN** = passes at baseline and must keep passing (regression pin); **PARTIAL** = covered at one named seam, the remaining leg needs the feature itself (reason given); **GAP** = not testable at this altitude (reason given).

| Scenario | Test(s) | File | Status | Failing reason / mutation target |
|---|---|---|---|---|
| S-01 | TestT_C1_S01_DebugLogCarriesReasoningLength | pkg/agent/effort_red_containment_test.go | RED | log line lacks `reasoning_chars` (FR-016) |
| S-02 | TestT_C2_S02_ReasoningChannelZeroTrace | pkg/agent/effort_red_containment_test.go | PIN | banned strings absent across repo (sweep) |
| S-03 | TestT_C3_S03_NonTruncatedReasoningOnly | pkg/agent/effort_red_containment_test.go | RED | `reasoning` reaches transcript/log (containment) |
| S-04 | TestT_C3_S04_SameTextOnMessengerAndParent | pkg/agent/effort_red_containment_test.go | RED | reply text differs / marker leaks |
| S-05 | TestT_C1_S05_RecapWarnCarriesLengthOnly | pkg/agent/effort_red_containment_test.go | RED | recap warn carries `response_preview` (FD-22) |
| S-06 | TestT_C4_S06_ReasoningOnlyTaskClassification | pkg/agent/effort_red_containment_test.go | PIN | classification unchanged |
| S-07 | picker component tests (S-07 rows) | src/components/ui/model-effort-picker.test.tsx | RED | stub renders null |
| S-08 | capabilities D3 + picker muted rows | src/lib/effort/capabilities.test.ts, picker | RED | stub throws / null |
| S-09 | TestT_S1_Mapping_S09_NoteTexts + D2 fixture | pkg/agent/effort_red_mapping_d2_test.go | RED | sentinel note texts (D2 r8 rows) |
| S-10 | picker keyboard tests | model-effort-picker.test.tsx | RED | null render |
| S-11 | picker keyboard tests | model-effort-picker.test.tsx | RED | null render (focus-return leg PARTIAL: parent surface) |
| S-12 | picker S-12 row | model-effort-picker.test.tsx | RED | null render |
| S-13 | effort.spec.ts S-13 | tests/e2e/effort.spec.ts | PARTIAL | collection proof only at RED; runtime = GREEN acceptance (CI) |
| S-14 | TestT_S1_Mapping_S09/S14 rows + picker + TS notes | mapping + picker + mapEffort.test.ts | RED | sentinel |
| S-15 | TestT_GW frame metadata (D5) | pkg/gateway/effort_red_gateway_test.go | RED | frame lacks `provider`/`reasoning_effort` |
| S-16 | — | — | GAP | deleted tombstone in spec |
| S-20 | TestT_R0_S20_ExtraBodyOverrideRecorded | pkg/agent/effort_red_request_rejection_test.go | RED | no override record on entry; body pin passes |
| S-21 | TestT_GW history transports + e2e S-21 | gateway + effort.spec.ts | RED/PARTIAL | REST/frame lack effort fields |
| S-22 | capabilities D3 rows + mapping blocked rows | capabilities.test.ts, D2 | PARTIAL | chip half via D3; `/effort` refusal needs the command GREEN |
| S-23 | TestT_P5_S23_EffortClearsTemperature | pkg/providers/anthropic/effort_red_carrier_test.go | RED | temperature stays on the wire |
| S-24 | TestT_P2_S24_RejectedEffortRetriedOnce | rejection file | RED | no retry (adapters send no effort) |
| S-25 | TestT_P2_S25_FailingRetrySurfacesOriginalError | rejection file | RED | no rejection produced |
| S-26 | TestT_P2_S26_RejectionRememberedUntilRestart | rejection file | RED | nothing remembered |
| S-27 | — | — | GAP | deleted tombstone |
| S-28 | TestT_P2_S28_Only400And422TriggerRetry | rejection file | RED (400/422) + PIN (401/403/429/500/503) | |
| S-29 | TestT_P2_S29_EachAttemptAccountedSeparately | rejection file | RED | one accounting entry (no retry) |
| S-30 | TestT_C6_S30_DecoderKeepsReasoningDetailsLosslessly | pkg/providers/common/effort_red_carrier_test.go | RED (capture) | id/signature/data dropped by lossy struct; decode→store→codec→request loop = GREEN obligation once the private carrier exists |
| S-31 | covered by S-30 capture RED + PIN absence in serialisers | common carrier file | PARTIAL | session-codec leg needs the carrier; earliest honest RED is the decoder |
| S-32 | TestT_C6_S33_SerialisersNeverEmitDetailsCrossRoute | common carrier file | PIN | isolation must hold at GREEN (mutation: emit details to every provider → fails) |
| S-33 | TestT_P5_S33_ThinkingBlocksAreTheOnlyAnthropicCarrier + C6 | anthropic + common carrier files | PIN | replay + isolation hold at baseline |
| S-34 | TestT_P5_S34_S90_EffortDrivesTheAnthropicWire | anthropic carrier file | RED (effort legs) + PIN (no-effort leg) | |
| S-35 | shared builder pin via S-34 file + D2 bedrock rows | anthropic carrier + D2 fixture | PARTIAL | bedrock has no separate body builder; guard reason is loop-side |
| S-36..S-42, S-70 | TestT_CMD_Effort_S36..S42/S70 | pkg/commands/effort_red_commands_test.go | RED | Lookup("effort") not found; exact spec texts pinned |
| S-43 | TestT_U1_S43_* (agent loop + promptcache D7 rows 1-6) | effort_red_usage_test.go, effort_red_usage_parse_test.go | RED | no reasoning_tokens anywhere; entry snapshot/cell legs are GREEN writers (T-R2 carries the wire half) |
| S-44 | TestT_U1_S44_UnreportedReasoningStaysAbsent + D7 row 5 | usage files | PIN | absent stays absent |
| S-45/S-46 | served/vendored catalog checks | — | GAP | catalog pipeline (US-8) test item not in the RED scope assigned; documented for the implementer's T-C3 |
| S-47 | — | — | GAP | deleted tombstone |
| S-48/S-49/S-72 | TestT_S1_Mapping rows + TS twins | mapping + mapEffort.test.ts | RED | sentinel |
| S-50/S-55/S-56/S-58/S-59/S-60/S-61/S-62 | TestT_GW agents REST + onboarding | pkg/gateway/effort_red_gateway_test.go | RED | handlers drop effort keys |
| S-51 | TestT_S2_S51_DefaultModelEffortUsed | rejection file | RED | llmOpts key unstorable → nothing sent |
| S-52 | TestT_S2_S52_UnsetFallbackSendsNothing | rejection file | PIN | fallback inherits nothing (mutation: share rt.llmOpts → fails) |
| S-53 | TestT_S2_S53_FallbackUsesOwnSetting | rejection file | RED | fallback effort unstorable |
| S-54/S-57/S-74 | picker T-F1/T-F3 rows | model-effort-picker.test.tsx | RED | null render |
| S-63/S-64/S-66/S-67/S-68/S-76/S-77 | TestT_TL63..TL77 | pkg/sysagent/tools/effort_red_tools_test.go | RED | refusals/drops per log (S-66 refused-not-dropped: spec premise note) |
| S-65 | — | — | GAP | deny-policy dispatch lives in pkg/agent, not the tool registry (agent-loop author's item) |
| S-69 | TestT_D1_S69_ThinkingLevelKeyDeleted + TestT_D1_S69_ThinkingLevelOptionDeleted + config codec | pkg/config + anthropic carrier | RED | legacy key still exists and still drives thinking |
| S-71/S-78/S-97 | recap request tests (S-71 rows, S-78 hand-written config, S-97 deadline rows) | pkg/agent/effort_red_recap_request_test.go | RED + pins (S-71 row 3, no-reason-data, S-97 retry-fits row) | bodies carry no recap effort; deadline not honoured |
| S-79/S-80 | TestT_GW memory settings recap keys + TestT_W2_RecapEffortKeysRoundTrip | gateway + pkg/config | RED | keys dropped; adapter-table effect rows = GREEN obligation (documented) |
| S-81 | TestT_GW memory settings ranges | gateway file | RED | twelve keys unknown |
| S-82/S-93 | effort-red-recap-limits.test.tsx (T-L4/T-L7) | src/components/settings/ | RED | inputs/autosave absent |
| S-84 | BootstrapRecapPass idle window | — | GAP | getter seam doesn't exist at RED (config getter = GREEN); effect asserted indirectly via S-79/S-97 rows |
| S-85/S-06 | TestT_C4_S85_OutputLimitedReasoningOnly / S-06 | containment | PIN | truncated-reasoning rule unchanged |
| S-86/S-87/S-91 | TestT_S1_Mapping_InheritedStates_S86_S87_S91 + TurnNotes rows | mapping file | RED | scaffold sentinel; state assembly = loop T-S2 wiring |
| S-88 | TestT_U1_S88_* (D7 rows 7/9 + carrier) | usage file | RED | failed-attempt/per-call counts lost |
| S-89 | TestT_C5_S89_CodexRecapCarriesEffortAndNoOutputLimit | pkg/providers/effort_red_codex_recap_test.go | RED | reasoning.effort absent, no output limit |
| S-90 | TestT_P5_S34_S90_EffortDrivesTheAnthropicWire (off row) | anthropic carrier | RED | no thinking disabled encoding |
| S-92 | TestT_TL92_CredentialMaskingStillWorks | tools file | PIN | masking unchanged (mutation: prefix match → fails) |
| S-94 | TestT_TL94_* + gateway refuseNullEffort + mapping composite rows | tools + gateway + D2 | RED + PIN (allow_from scalar→list control) | |
| S-95 | TestT_GW transports (T-R2) + e2e scroll-up | gateway + effort.spec.ts | RED/PARTIAL | wire fields absent; read-time-sum mutation pinned via snapshot rows |
| S-96 | — | — | GAP | deleted tombstone |
| S-97 | recap deadline rows | recap file | RED + PIN (retry-fits row) | |

Datasets: D2 (27 rows, shared Go/TS fixture — SC-002), D3 (capabilities), D4 (rejection rows), D5 (frame metadata), D7 (usage rows 1-9 + row 10/11 wire legs via T-R2), D8 (get_agent read-out via S-67/S-76). All implemented as table-driven/parameterised tests in the files above.

## Spec premises that no longer match the code (reported, not adapted)

1. **TD-1/T8 not landed on this branch** — `get_agent` has no `fields`/`model_chain`/`effort_options` (found by the tools RED author). S-67/S-76 stay RED and cannot go green until T8 lands.
2. **S-66 "currently dropped" is actually "currently refused"** — `applyFallbackModels` rejects unknown entry keys; same RED outcome, different message.
3. **S-92** — channels carry `token_ref`, not a plaintext `token`; the FORBIDDEN name-fragment refusal still pins.
4. **S-30 streaming half** — the streaming decoder shares the same lossy detail struct; the non-streaming decoder RED is the recorded proof, the streaming leg is the same struct at GREEN.
5. **Spec's `PUT /memory-settings` vs registered `PUT /settings/memory`** — the r9 correction already renamed this; gateway tests use the registered route.
