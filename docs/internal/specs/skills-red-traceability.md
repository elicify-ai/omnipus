# skills-spec-r13 — RED traceability (landing/skills)

Test author: qa-lead (RED phase). Branch `landing/skills` off `origin/release/v0.1.1`
(`1d3989df`). Spec: `skills-spec-r13.md` (grill r11 PASS; r12 minor fix; r13 = SD-28
addition, pending grill — its rows are flagged below).

**Evidence classification (SC-001).** Each row declares its class:
- **RED** — asserts new or corrected behaviour; fails on the baseline with a
  meaningful behavioural failure (log + exit code under `/workspace/logs/skills-red/`).
- **REGRESSION** — asserts unchanged behaviour; must pass on baseline and candidate.
  Named pairs from the spec: row 8 (S-2.9), row 9's S-1.4, row 24's S-3.13; further
  pairs are declared below and marked "declared pair (test author)".
- **PLATFORM-GATED** — Windows CI only (S-12.1); compile-verified via
  `GOOS=windows go vet` (exit 0), runtime claim deferred to CI.
- **NOT RUN (written only)** — the S-6.4 Playwright spec (needs a live gateway +
  built SPA; row 21's REST half is covered in pkg/gateway).

## RED-run receipts

Counts are the failing top-level test (or subtest) lines in each log as it
exists on disk; some logs are narrower re-runs of an area after a filter or
seam change, so an area's full test count is larger than its last log's
failing-line count (the §17.1 map above names every test).

| Log | Package(s) | Failing on baseline | Result |
|---|---|---|---|
| `01-identity-sd28.log` | pkg/skills (identity, SD-28) | 10 | EXIT=1 |
| `02-builtin-sync.log` | pkg/skills (built-ins, retirement) | 9 | EXIT=1 |
| `03-pipeline-savecheck.log` | pkg/skills (pipeline, save check) | 5 | EXIT=1 |
| `04-undo-faults.log` | pkg/skills (undo anchors, persistence faults) | 16 | EXIT=1 |
| `05-retention-versions-recovery.log` | pkg/skills (retention, versions, recovery) | 15 | EXIT=1 |
| `06-snapshot-restore.log` | pkg/skills (snapshot domain, bounded capture, D-9) | 6 | EXIT=1 |
| `07-copy-lifecycle.log` | pkg/skills (S-5.21 legs, provenance, interference) | 8 | EXIT=1 |
| `08-sysagent-skill-read.log` + `08b` | pkg/sysagent/tools (get_skill, list_skills) | 7 FAIL + 1 baseline-green pair | EXIT=1 / EXIT=0 |
| `09-tools-gate.log`, `09-coreagent-gate.log`, `09-config-sysagent-gate.log`, `09b-tools-textgate.log`, `09-sysagent-gate.log` | pkg/tools, pkg/coreagent, pkg/config, pkg/sysagent/tools (T-E9b) | 14 FAIL + feasibility suite baseline-green | EXIT=1 |
| `10-fspolicy-boundary.log` + `10b` | pkg/fspolicy (§6.1 boundary) | 5 FAIL + 3 baseline-green | EXIT=1 / EXIT=0 |
| `11-skills-historyseam-recheck.log` | pkg/skills (re-run after SkillHistoryRoot seam) | 12 | EXIT=1 |
| `12-load-header.log` + `12b` | pkg/skills (§5.7 activation header) | 5 FAIL + S-1.4 pair baseline-green | EXIT=1 / EXIT=0 |
| `13-gateway-rest.log` | pkg/gateway (REST surface) | 4 FAIL (8 subtests) | EXIT=1 |
| `14-gateway-boot.log` | pkg/gateway (boot step) | 3 FAIL | EXIT=1 |
| `15-credentials.log` | pkg/credentials (SensitiveNamedValues) | 5 FAIL | EXIT=1 |
| `16-secret-check.log` | pkg/skills (§7.4 secret scan) | 6 FAIL | EXIT=1 |
| `17-documentruntime.log` | pkg/documentruntime (S-1.5 app part) | 2 FAIL | EXIT=1 |
| `18-spa-tests.log` | ui: envelope parser + SkillsScreen | 6 FAIL / 6 baseline-green across both files | EXIT=1 (vitest) |

All runs: `CGO_ENABLED=0 heavy go test -p 1 -tags goolm,stdjson -count=1 -run '<targets>'
<packages>` (build runners), full output in the named log, real exit codes captured.

**No existing test regressed:** every targeted run matched only the new test names
(verified by grep over each log: zero pre-existing test names appear); the baseline-green
pairs listed above were run individually (`-v`) and passed (EXIT=0).

## Scenario → test map

### US-1 Load (§5.7)

| Scenario | Test | File | Class | Failing reason (baseline) |
|---|---|---|---|---|
| S-1.1 | TestLoadHeader_RegistrySkillFullFormat, TestLoadHeader_EditDeniedClause | pkg/skills/load_header_red_test.go | RED | LoadForActivation scaffold refuses |
| S-1.2 | TestLoadHeader_BuiltinShelf | same | RED | same |
| S-1.3 | TestLoadHeader_FileListCap | same | RED | same |
| S-1.4 | TestLoadHeader_AllowedToolsSpellings_Regression | same | REGRESSION (named pair) | passes baseline (EXIT=0, log 12b) |
| S-1.5 (app part) | TestChildEnvironment_NoDocumentSkillsEnv, TestProvision_NoEmbeddedSkillsCopy | pkg/documentruntime/doc_skills_red_test.go | RED | env still carries OMNIPUS_DOCUMENT_SKILLS; provision still copies |
| S-1.5 (child/kernel) | rows 7/10 kernel scope | — | NOT COVERED HERE | kernel/child legs need Linux+macOS sandbox CI; declared gap for the implementing worker |
| S-1.6 | TestLoadHeader_OutsideChange | pkg/skills/load_header_red_test.go | RED | scaffold refuses |

### US-2 Read and run (§6.1 boundary)

| Scenario | Test | File | Class | Failing reason |
|---|---|---|---|---|
| S-2.1 | TestSkillsBoundary_LoadableSetGrantFiltered/granted | pkg/fspolicy/skills_boundary_red_test.go | RED | A7 leaf rule denies SKILL.md |
| S-2.2 | …/S-2.2 ungranted legs; TestListSkills_UsableRowsGrantFiltered_Regression (grant predicate) | fspolicy + pkg/sysagent/tools/skill_read_red_test.go | RED / REGRESSION | no grant filter yet |
| S-2.3 | …/S-2.3 editor legs (SkillsReadAll) | fspolicy boundary | RED | SkillsReadAll missing |
| S-2.4 | TestSkillsBoundary_RegistryControlEntriesRefused | fspolicy boundary | RED | control entries escape the inventory |
| S-2.5–S-2.7 | rows 7 (kernel, Linux+macOS) | — | NOT COVERED HERE | kernel-sandbox CI scope |
| S-2.8 | covered indirectly via §6.1 item 2's inventory-first order (TestSkillsBoundary_InventoryFirstOrder — mount/allow/setup-root re-admission refused); the operator-path-grant scenario itself rides the same rule | fspolicy | RED (order leg) | allowance wins today |
| S-2.9 | TestSkillsBoundary_SystemSubtreeProtected_Regression, TestSkillsBoundary_InventoryRulesCarveOuts | fspolicy | REGRESSION (named pair) | passes baseline (log 10b) |
| S-2.10 (read half) | TestSkillsBoundary_DotFilesInsideSkillArePartOfIt; write half → S-4.1 D-2 rows | fspolicy | REGRESSION pair declared by test author (baseline-consistent: dot-files readable today, must stay readable under the grant rule) | passes baseline (log 10b) |
| S-2.11 | row 37 T-W2 (protection levels) | — | NOT COVERED HERE | sandbox-mode matrix, kernel CI |

### US-3 Edit (§5.5, §7.5)

| Scenario | Test | File | Class |
|---|---|---|---|
| S-3.1–S-3.6 | covered inside pkg/skills pipeline tests (TestCreate_Commits…, TestEdit_StaleExpectation…, multi-file sequences, delete/restore commits) | pkg/skills/{apply_change,undo_anchor}_red_test.go | RED |
| S-3.7 | row 24 T-P1/T-P2 "through the real agent loop" | — | NOT COVERED HERE (agent-loop integration; approval-card rendering) |
| S-3.8 | TestMultiFileSequence_RecoveryFromOwnWrite1Anchor (+leg 2) | pkg/skills/undo_anchor_red_test.go | RED |
| S-3.9 | refusal-shape legs inside apply_change tests | pkg/skills | RED |
| S-3.10 | T-E9b gate (registration/policy/text) | pkg/{tools,coreagent,config,sysagent/tools}/retired_tools_gate_red_test.go | RED |
| S-3.11–S-3.12 | Auto-card write paths — row 24 scope (real agent loop) | — | NOT COVERED HERE (loop integration) |
| S-3.13 | row 24 named regression pair (V-46: approval skip exists at pin) | — | NOT WRITTEN as a separate test; pinned by spec row 24 for the loop-integration worker (declared, not silently dropped) |
| S-3.14–S-3.17 | TestRecreate_AfterDelete_UndoVariantNamesDeletion, TestRecreate_AfterBrokenPredecessor_UndoNamesRepair, TestUndoVariant_KeyedToCompleteRestoreGate, TestRecreate_AfterOutsideRemoval_ContentHeadVariant | pkg/skills/undo_anchor_red_test.go | RED |

### US-4 Save check (§7.2, §7.4)

| Scenario | Test | File | Class |
|---|---|---|---|
| S-4.1 (D-2) | TestSaveCheck_Dataset (full D-2: boundaries, symlink/hardlink/CON.txt/folder-total) | pkg/skills/apply_change_red_test.go | RED |
| S-4.2 | TestSecretCheck_ValueInContent_RefusedNamedNotEchoed | pkg/skills/secret_check_red_test.go | RED |
| S-4.3 | TestSecretCheck_ThreeByteValue_Exempt_Committed | same | RED |
| S-4.4 | TestSecretCheck_OAuthSubValue_RefusedNamed | same | RED |
| S-4.5 | TestSecretCheck_StoreUnavailable_FailsClosed (+ TestSensitiveNamedValues_LockedStore, …DamagedEntryScopes — store side) | pkg/skills + pkg/credentials | RED |
| S-4.6 | TestSecretCheck_NoStore_Committed (+ …MissingStoreFile) | same | RED |
| S-4.7 | TestSecretCheck_RestoreRefused_WhenVersionGainsAStoredValue | pkg/skills | RED |

### US-5 Versions, retention, snapshot (§9)

| Scenario | Test | File | Class |
|---|---|---|---|
| S-5.1 | TestRestore_PutsEveryFileBack_AndRoundTrips | pkg/skills/versions_restore_red_test.go | RED |
| S-5.2 | versions-page legs inside apply_change/undo tests (version ids from §5.5 result texts) | pkg/skills | RED |
| S-5.3 | E-3 broken-folder legs (T-E14/T-E15) | pkg/skills/versions_restore_red_test.go (TestE3_BrokenFolder_PresentAndRepairable) | RED |
| S-5.4 | TestRestore_DeletedSkill_ThenRecreateByOtherSession_Refused | pkg/skills/versions_restore_red_test.go | RED |
| S-5.5 | T-E13 row 11 (restore refusals after delete) | covered inside restore/deleted legs | RED |
| S-5.6 | TestRetention_SupersessionClock | pkg/skills/retention_red_test.go | RED |
| S-5.7 | E-13 paging matrix (T-V13) — tool-side validation in TestGetSkill_UnknownVersion; full 250-version matrix with the history fixtures | pkg/sysagent/tools + pkg/skills | RED (tool side) |
| S-5.8 | TestRetention_Disabled_KeepsCommittedHistory_NeverTouchesCaptureTemporaries | pkg/skills/retention_red_test.go | RED |
| S-5.9 | TestOutsideChange_DetectionAndRestore | pkg/skills/versions_restore_red_test.go | RED |
| S-5.10–S-5.12 | TestGetSkill_UnknownVersion + S-5.11/S-5.12 refusal legs (§5.10 texts) | pkg/sysagent/tools/skill_read_red_test.go + pkg/skills | RED |
| S-5.13 | TestRetention_DeleteHeadKeepsPreDeletionState_FullWindow (+ 91/89-day boundaries) | pkg/skills/retention_red_test.go | RED |
| S-5.14 | TestRetention_NewestSupersededToday_Kept | same | RED |
| S-5.15 | durable changed-path provenance: last_change.paths assertions inside retention tests | same | RED |
| S-5.16 | TestRestore_ReproducesNestedDotContent | pkg/skills/snapshot_restore_red_test.go | RED |
| S-5.17 | TestSnapshot_SymlinkCanary | same | RED |
| S-5.18 | TestSnapshot_RetainedNotRestorable | same | RED |
| S-5.19 | TestDeleteFile_ResultingInstructionValidity (legs 1–2) | same | RED |
| S-5.20 | TestBoundedCapture_MutationRefusedBeforeAnythingChanges (mutation+delete legs) + load legs → S-1.1 header load degradation | pkg/skills/snapshot_restore_red_test.go | RED (mutation legs); load-degradation legs declared for the loading-path worker |
| S-5.21 | TestS521_LegsABCD…, LegsEF, LegG, LegH, LegI | pkg/skills/copy_lifecycle_red_test.go | RED |
| S-5.22 | instruction-read bound: SKILL_INSTRUCTION_LIMIT — declared for the loading-path worker (menu scan + Skill load + get_skill drive it) | — | DECLARED GAP (see below) |
| S-5.23 | captured-domain instruction resolution: SKILL_INSTRUCTION_UNRESOLVED — refusal scaffolds exist (ErrSkillInstructionUnresolved); full probe-driven legs declared for the loading-path worker | — | DECLARED GAP (see below) |

### US-6 Copy, identity, grants (§5.4, §5.13, §5.13a)

| Scenario | Test | File | Class |
|---|---|---|---|
| S-6.1 | TestBuiltinNameReserved_CaseInsensitive_AllEntryPoints, TestWrite_IntoBuiltinFolder_BUILTIN_READ_ONLY | pkg/skills/skill_identity_red_test.go | RED |
| S-6.2 | install replace legs (KindInstallReplace) in pkg/skills tests | pkg/skills | RED |
| S-6.3 | uninstall legs (delete by person/agent) | pkg/gateway (S-6.10) + pkg/skills | RED |
| S-6.4 | SkillsScreen.copy-dialog.test.tsx (4 legs) + tests/e2e/skills-builtin-copy.spec.ts | ui/src + tests/e2e | 2 RED + 2 baseline-green; e2e NOT RUN (written) |
| S-6.5 | TestRestCopy_Route (201/400/404/409) + copy pipeline legs | pkg/gateway + pkg/skills | RED |
| S-6.6 | install refusal legs (D-1 names) | pkg/skills (identity + save-check datasets) | RED |
| S-6.7 | TestSkillKey_IsTheLowercasedSlug | pkg/skills/skill_identity_red_test.go | RED |
| S-6.8 | TestCreate_CaseTwin_OfUserSkill_Refused_SKILL_EXISTS | same | RED |
| S-6.9 | recreation-inherits-nothing legs (grant lists empty after delete/recreate) | pkg/skills/skill_grants_sd28_red_test.go | RED (pending grill) |
| S-6.10 | TestRestDelete_RevisionEnvelope | pkg/gateway/rest_skills_t9_red_test.go | RED |
| S-6.11 | install-replace REST legs — row 36 T-R2; the REST refusals ride the delete/copy envelopes + ApplyChange replace tests | partially RED (envelopes); full T-R2 matrix declared for the implementing worker |
| S-6.12 | TestDelete_RemovesGrants_AtDeletionTime, TestCopy_AfterDelete_CommitsUngranted_TextOffersGrant, TestCreateCopyInstall_OntoStillGrantedName_Refused_SKILL_NAME_GRANTED, TestDelete_GrantRemovalFails…, TestInstallReplace_KeepsGrants | pkg/skills/skill_grants_sd28_red_test.go | RED (**pending grill** — SD-28) |
| S-6.13 | TestCopyProvenance (baseline source) | pkg/skills/copy_lifecycle_red_test.go | RED |
| S-6.14 | TestCopyProvenance (external_change source + transform) | same | RED |
| S-6.15 | TestCopyProvenance (built-in mismatch byte-exact) | same | RED |
| S-6.16 | TestCopyInterference_CaptureThenStage (legs 1/3/4 + negative control) | same | RED |
| S-6.17 | TestS617_OversizedCopySource (+leg c twin) | same | RED |

### US-7 Start-up sync (§8)

| Scenario | Test | File | Class |
|---|---|---|---|
| S-7.1–S-7.5, S-7.10 | TestSyncBuiltins_StartupMatrix, …_EditedUnstampedOverwritten_NoCustomCopy, …_SecondStartIsIdempotent, …_UnstampedEqual_GetsStampOnly, …_RemovesStaleSeedStaging | pkg/skills/builtin_sync_red_test.go | RED |
| S-7.6–S-7.9 | TestRetireBuiltins_StampedFolderDeleted_GrantRemoved, …_DanglingNameRemoved, …_UnstampedFolderKept, …_SkippedWhenABuiltinInstallFailed | same | RED |
| S-7.13 | TestBootSkills_RetiresStampedRetiredBuiltin | pkg/gateway/skills_boot_red_test.go | RED |
| S-7.14 | TestBootSkills_AuditReplayRealLogger (+ TestBootReport_EventNamesMatchSpec) | same | RED |

### US-8 get_skill (§5.2–§5.3)

| Scenario | Test | File | Class |
|---|---|---|---|
| S-8.1 | TestGetSkill_FieldsShape | pkg/sysagent/tools/skill_read_red_test.go | RED |
| S-8.2 (D-5) | TestGetSkill_FieldsValidation_D5 (8 legs incl. all-14-names) | same | RED |
| S-8.3 | truncation legs — declared inside the D-5 projection contract (files 200-cap mirrors §5.2); full truncation matrix declared for the implementing worker with real oversized fixtures | partially covered |
| S-8.4 | TestGetSkill_AuthorizationMatrix/S-8.4 | same | RED |
| S-8.5 | …/S-8.5 | same | RED |
| S-8.6 | …/S-8.6 (deleted-skill precedence) | same | RED |
| S-8.7 | TestListSkills_NameRemoved | same | RED |
| S-8.8 | TestGetSkill_DeletedSkillShape | same | RED |
| S-8.9 | 200-file cap — asserted in the files contract of S-8.1 (§5.2 items rule); full 201-file fixture declared for the implementing worker | partially covered |
| S-8.10 | TestGetSkill_LockedReadCoherence (lock hold + three-way observation) | same | RED |
| E-9 | TestGetSkill_BuiltinVersionsEmptyPage | same | RED |
| NOT_FOUND | TestGetSkill_AuthorizationMatrix/authorised-no-folder | same | RED |

### §15 T-E9b gate

| Surface | Test | File | Class |
|---|---|---|---|
| registered tool set | TestRetiredTools_GoneFromRegisteredSet | pkg/sysagent/tools/retired_tools_gate_red_test.go | RED |
| allStaticToolNames | TestRetiredTools_GoneFromAllStaticToolNames | pkg/coreagent | RED |
| managementTools | TestRetiredTools_GoneFromManagementTools | pkg/coreagent | RED |
| role policies | TestRetiredTools_GoneFromRolePolicies | pkg/coreagent | RED |
| custom-agent baseline | TestRetiredTools_GoneFromCustomAgentBaseline | pkg/coreagent | RED |
| defaults ceiling | TestRetiredTools_GoneFromDefaultsCeiling | pkg/config | RED |
| autoApproveClasses | TestRetiredTools_GoneFromAutoApprove | pkg/tools | RED |
| refusal answers present | TestRetiredTools_RefusalAnswersPresent | pkg/tools | RED |
| provider_defs.golden.json | TestRetiredTools_GoldenProviderDefs | pkg/tools | RED |
| Name() implementation scan | TestRetiredTools_NoToolTypeImplementsRetiredName | pkg/tools | RED |
| §15 item 2 writers | TestRetiredTools_NoOriginMetaWriter | pkg/tools | RED |
| §15 item 3 text gate | TestRetiredTools_TextGate | pkg/tools | RED |
| §15 item 3 feasibility | TestRetiredTools_TextGateFeasibility | pkg/tools | REGRESSION by design (matcher self-check; passes baseline) |

### Persistence faults (§7.6) and recovery (§9.3)

| Scenario | Test | File | Class |
|---|---|---|---|
| S-11.1–S-11.5, S-11.10, S-11.12 | TestRecovery_* | pkg/skills/recovery_red_test.go | RED |
| S-11.6–S-11.9, S-11.13–S-11.18 | TestClass2_…, TestClass3_…, TestClass1_…, TestDelete_ManifestFails…, TestCreateFails_AfterParentDirCreated…, TestRestoreIntoAbsence… | pkg/skills/persistence_faults_red_test.go | RED |

### REST (§5.8), SPA (T-R3), documents, store

| Scenario | Test | File | Class |
|---|---|---|---|
| S-9.1–S-9.2 | TestRestList_HasLocalChanges (fresh false) | pkg/gateway/rest_skills_t9_red_test.go | RED |
| S-9.3 | installSkillBySlug forwards replace_local_changes (SPA leg) | ui/src/lib/api/skills-mutation-envelope.test.ts | RED; backend gate legs ride S-6.11/ApplyChange replace tests |
| S-9.4 | TestRestList_HasLocalChanges (read-time true, no version written) | pkg/gateway | RED |
| S-11.19 | TestRestS1119_FailureEnvelopes (REST legs a–e shapes) + SPA parser legs | pkg/gateway + ui | RED |
| S-12.1 | TestWindowsPipeline_SwapSemantics | pkg/skills/builtin_swap_windows_red_test.go | PLATFORM-GATED (GOOS=windows vet exit 0) |
| E-10/E-11 | dangling-grant sweep + non-loadability after recreation | pkg/skills/skill_grants_sd28_red_test.go (SweepDanglingGrants legs) | RED (pending grill) |
| E-12 | delete envelopes | TestRestDelete_RevisionEnvelope | RED |
| E-13 | version paging | S-5.7 row above | RED (tool side) |
| T-S5 (fields enum = SkillDetail props) | the D-5 legs pin the 14 names; the contract-side equivalence awaits the SkillDetail contract schema (Constraint #8 five-step process adds it with the feature) | pkg/sysagent/tools | DECLARED GAP (contract artifact does not exist yet) |

## Declared gaps (scenarios NOT turned into tests here, and why)

1. **Kernel/child legs** (S-1.5 child part, S-2.5–S-2.7, S-2.11, row 7/37): they need the
   Linux/macOS kernel-sandbox harness (Landlock/Seatbelt ruleset inspection) — out of this
   RED slice's Linux-CI scope; row 7 and row 37 name their own environment.
2. **Real-agent-loop legs** (S-3.7, S-3.11–S-3.13, row 24 T-P1/T-P2): the approval card and
   the Auto classifier verdicts are observable only through the real agent loop; row 24
   assigns that harness. S-3.13 is the named regression pair (V-46) — it belongs to the
   loop-integration worker's candidate run, never to RED evidence.
3. **S-5.22 / S-5.23 loading-path legs**: the menu-scan/Skill-load/get_skill drive needs the
   implemented loading path; the refusal scaffolds (`ErrSkillInstructionLimit`,
   `ErrSkillInstructionUnresolved`) and the bounded-consumption probe exist for the worker.
4. **S-8.3/S-8.9 full fixtures** (truncation matrices with real oversized payloads): the
   projection contracts (§5.2 caps, 200-file rule) are pinned in the D-5/S-8.1 tests; the
   full-size fixtures belong with the implemented reader.
5. **T-S5 contract-side equivalence**: `SkillDetail`'s contract schema does not exist yet
   (Constraint #8: the five-step wire-type process lands it with the feature).
6. **S-6.11's full T-R2 matrix**: the REST refusal envelopes are covered (S-6.10/S-11.19);
   the install-replace REST rows need the install route's ApplyChange migration.
7. **S-1.5 e2e child leg** (script runs; child `cat` fails): needs a live gateway (row 10).

## Spec-premise drift (pin check)

Spec pin `4cad412bf`; release head `1d3989df` — 91 commits between them. The spec's r3/r4
notes state r3 re-verified the premises on `1d3989df` (V-25–V-32) and the head has not
moved since. All pinned symbols verified present at the head: `SeedDefaults`
(pkg/skills/embed.go:62), 29 built-ins incl. `plan` (no `handoff`/`summarize`),
`MaxSkillMarkdownBytes=262144` (authoring.go:22), `writeOriginMeta` (skills_install.go:390),
`retiredToolCanonicalNames` (registry.go:506). **No new drift found; no spec premise
silently adapted.** One worktree-only note: `pkg/gateway/spa/` is a gitignored build
artifact absent in a fresh worktree — a placeholder was created locally (never committed)
so the gateway package compiles, mirroring CI's build step.

## SD-28 status

All §5.13a tests live in `pkg/skills/skill_grants_sd28_red_test.go` — flagged **pending
grill** in this table per the chief's instruction; the chief reports grill changes.
