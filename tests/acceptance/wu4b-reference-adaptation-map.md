# WU-4b reference-only test translation — 2026-10-16

**Scope:** exactly `configure_provider`, `add_mcp_server`, `configure_channel`. The card-request tool, real Registry/Submit and real encrypted store are supporting dependencies, not additional management-tool scope. Tests only; no production implementation or CHECK verdict in this pack.

**Authority:** chief D2-A; Q1-A fault/replacement translation; Q2-A unprescribed error codes; architect CONFIRM-A. Original implementation base: `9c48ab2bc843046277a9c4e025fbb900d67d6268`. Original direct `TestConfigureProvider_RefusesPlaintext` is byte-unchanged. Any new refusal code is the implementer's decision, preferring existing vocabulary; tests require the exact `failed: nothing saved` outcome, error result, meaningful reference/recipient/request-a-secret reason, and exact absence of side effects. Existing specified `CONFLICT`, `INVALID_INPUT`, `UNKNOWN_PROVIDER` and duplicate-collision assertions are retained.

## Retired fault hooks and replaced oracles

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu3_fix_effectful_outcomes_test.go

| Old test | New test | Independent replacement oracle |
|---|---|---|
| `TestWU3MCPAddLaterCredentialFailureIsPartlySaved` | `TestWU4B_MCPLaterRecipientFailureIsFailedNothingSaved` | First valid reference + later recipient-mismatched reference: whole attachment refused; no selected key, config/store byte change, save, reconcile or value echo. A reference-only tool cannot partly save credentials. |
| `TestWU3MCPAddFirstCredentialFailureSavesNothing` | `TestWU4B_MCPFirstMissingReferenceSavesNothing` | First missing reference: exact failed-nothing-saved outcome, meaningful reference reason, zero config/store/runtime effect. |
| `TestWU3MCPAddRollbackFailureNamesDurableCredentials` | `TestWU4B_MCPConfigFailureAfterCardSubmitRetainsValuesWithoutSelection` | A REAL earlier Submit stores a bound value. Valid attachment reaches exactly one injected failing config-save edge; no consumer selection, no config/store rewrite, no runtime call. Prior value/binding/receipt survives. This call reports failure, not credentials partly saved by somebody else's earlier operation. |
| Channel saved/live verdict row, same name | `TestWU3ChannelSuccessfulWritesAreSavedButNotWorking` | Prior bound card reference selected; exact scope/not-tested/reload reason and enabled/disabled assertions preserved. Selection is not live authentication. |

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/export_test.go

| Old QA export | Adaptation | Why no shim remains |
|---|---|---|
| `FailMCPCredentialWriteAfterForTest` | Removed; both callers translated above | D2 deletes the tool's credential-writing path. The new tests fault missing/mismatched references instead of resurrecting writes. |
| `FailMCPCredentialRollbackForTest` | Removed; its caller translated above | No value rollback belongs to reference selection. A prior card Submit is durable and must not be undone by attachment failure. |

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu3_outcomes_test.go

| Old test | New test | Independent replacement oracle |
|---|---|---|
| `TestWU3ProviderConfigFailureAfterCredentialSaveIsPartlySaved` | `TestWU4B_ProviderConfigFailureAfterCardSubmitRetainsValueWithoutSelection` | REAL earlier card Submit; later selection actually attempts the failing filesystem publication exactly once. No selected provider, previous config/store bytes unchanged, stored value readable, no reload/probe, exact failed-nothing-saved outcome. |

All unrelated agent/workspace/MCP-remove outcome tests in these files retain their original oracles. No production hook, alias, migration or compatibility path is added.

## Provider fixture/input translations

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/provider_test.go

| Old test | New test (same name unless shown) | What remains / what D2 changes |
|---|---|---|
| `TestProviderConfigure_WiresAPIKeyRef` | Same | Every provider row points to the exact prior bound version, endpoint unchanged, stored value readable, reload invoked. Retired canonical unversioned key-name oracle becomes explicit selected-reference equality. |
| `TestProviderConfigure_ReusesExistingRef` | `TestProviderConfigure_SelectsNewVersionWithoutOverwritingExistingRef` | Explicit new bound version selected; old reference/value remains byte-for-byte readable and unchanged. No overwrite. |
| `TestProviderConfigure_AppendsEntryForNewProvider` | Same | Exactly one correct new provider row; its reference equals the supplied bound version and resolves to the prior submitted value. |
| `TestProviderConfigure_APIBaseIsStored` | Same | Exact API base persistence; valid prior recipient binding. |
| `TestProviderConfigure_CloudProviderRequiresAPIKey` | `TestProviderConfigure_CloudProviderRequiresAPIKeyReference` | Missing cloud reference remains an `INVALID_INPUT` refusal. |
| `TestProviderConfigure_APIBaseValidation` | Same | All original accepted/rejected URL classes and specific validation codes retained. Valid control references are issued before Execute; malformed-base cases do not use malformed card fixtures. |
| `TestProviderConfigure_WideCatalogProviderConfigures` | Same | Both real catalogue IDs accepted; selected versions remain readable and correct provider row persists. |
| `TestProviderConfigure_LegacyAliasIdsRejected` | Same | Both alias IDs still `UNKNOWN_PROVIDER`; both canonical IDs accepted. Canonical positive controls use an explicit valid destination for the prior card binding. |
| `TestProviderConfigure_UnknownProviderRejected` | Same | Unknown ID still `UNKNOWN_PROVIDER`; no plaintext input masks catalogue validation. |
| `TestProviderConfigure_NoFakeStatusFields` | Same | Fabricated status/models fields remain absent; readable already-stored selected reference is reported, not a new tool value-write claim. |
| `TestProviderConfigure_PublishWarningOnReloadFailure` | Same | Reload error remains visible after persisted reference selection, not swallowed or converted to false live success. |
| `TestProviderConfigureThenList_EndToEnd` | Same | Newly configured provider remains visible through its real read tool. |
| `TestProviderConfigure_NilCredStoreWithAPIKeyNoPanic` | `TestProviderConfigure_NilCredStoreWithReferenceRefusesWithoutPanic` | Unavailable reference store cannot panic OR silently succeed; explicit refusal and no selected provider. |

`newProviderTestDeps` now uses temporary HOME/OMNIPUS_HOME, a real config file/save and closed real encrypted store, rather than a no-op persistence callback. Keyless-local, provider-read, catalogue and description assertions otherwise remain. No real provider is contacted by this fixture.

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu3_provider_test.go

| Adapted test | Preserved oracle |
|---|---|
| `TestWU3ConfigureProviderChecksSavedEndpointNotCatalogue` | Exact saved endpoint/path/auth header; one saved-endpoint call, zero catalogue calls; truthful outcome. Newly selected version used; old stored value preserved. |
| `TestWU3ConfigureProviderUsesSavedNonOpenAIProtocol` | Anthropic path, authentication/version header, absence of Bearer header, exactly one real listing edge and truthful outcome. |
| `TestWU3ProviderWorkingOnlyProvesModelListing` | Listing count one, completion count zero; execution explicitly untested. |
| `TestWU3ConfigureProviderDryRunHasNoSideEffects` | Valid and invalid shapes; zero network calls; config/credential bytes unchanged; exact shape-only scope or validation refusal. Prior card fixture is published BEFORE snapshots. |

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu3_fix_provider_test.go

| Adapted test | Preserved oracle |
|---|---|
| `TestWU3ConfigureProviderChecksTheSavedKeyNotTheInjectedOldOne` | Both good-old/bad-new and bad-old/good-new directions; exact newly selected key in one probe. Stale process value is also planted under the new reference name, preserving the original failure instrument. |
| `TestWU3ConfigureProviderUnreadableSavedKeyIsReported` | Real selected entry deleted at config publication; failure is checked, never swallowed. Neither process environment nor injected listing substitutes for the lost value; exact nonworking explanation and zero probes. |
| `TestWU3ConfigureProviderGuardRefusesForbiddenCloudEndpoint` | Prior bound ref does not bypass outbound guard; no network/listing call; saved-but-not-working and correct persisted endpoint. |
| `TestWU3ConfigureProviderGuardControls` | Allowed cloud destination receives wired guard; keyless local exception unchanged. |

## Channel and MCP positive fixtures

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/channel_impl_test.go

| Adapted test | Preserved / translated oracle |
|---|---|
| `TestConfigure_SetsTokenRef` | Prior bound version selected exactly; the real `test_channel` consumer sees its credentials. No canonical unversioned tool-write key. |
| `TestConfigure_AppSecretRef` | Prior bound Feishu app-secret version selected in AppSecretRef. Existing saved instance makes D1 recipient derivation real. |

`newTestDepsWithCredStore` now isolates HOME/OMNIPUS_HOME and persists config through real SaveConfig; no-op persistence is not evidence of durability. Other channel test functions retain their original assertions.

Source: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/mcp_test.go

| Old test | New test | Preserved / translated oracle |
|---|---|---|
| `TestMCPTools_RoundTrip` | Same | Secret-free descriptor create → real prior bound version → bounded attach, then original persisted descriptor/reference/value, global-create switch, list, collision and remove-consumer assertions. |
| `TestMCPAdd_EnvRequiresCredentialStore` | `TestMCPAdd_PlaintextRefusedEvenWithoutCredentialStore` | Plaintext refused with request-a-secret guidance, no server saved; secret-free create still works without a store. Obsolete credential-write error code replaced by D2/Q2-A semantic refusal, not a QA-invented code. |
| `TestMCPAdd_NameCollisionDoesNotHijackExistingCredentials` | Same | Already selected card version/value unchanged after descriptor-only duplicate (`ALREADY_EXISTS`). Plaintext collision attempt's independent no-write/guidance meaning is covered by the new direct and registered plaintext matrices; wrong-reference hijacking is covered by recipient mismatch rows. |

## Revision and concurrent-winner tests

| Source | Adapted test | Preserved / translated oracle |
|---|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu3_shared_revision_test.go | `TestWU3SharedWritesRejectStaleAndMissingRevisionsBeforeSideEffects` | All six original write verbs still execute. Exactly the three in-scope credential inputs become prior bound refs; MCP tests an existing descriptor's attachment instead of impossible new-server consent. Same specific conflict/missing-precondition assertions, exact disk/store/live snapshots. |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu3_shared_winner_test.go | `TestWU3MCPAbsentTokenRejectsConcurrentAddWithoutTouchingCredentials` | Actual concurrent winner survives; absent token is `CONFLICT`, not merely duplicate refusal; prior submitted credentials remain untouched. |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu3dev_shared_revision_test.go | `TestWU3Dev_SharedWrite_StaleRevisionRefusedNothingSaved` | Real stale endpoint token refuses; current-token positive control still executes. Prior submitted version remains unused/readable, with no new stored versions on refusal. |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu3_fix_provider_token_test.go | `TestWU3ProviderTokenCoversTheRepresentativeRow` | Reordered representative row changes token; old plan still `CONFLICT`; identical-endpoint row addition remains token-stable. |

## New acceptance coverage and fixture boundary

| New test source | Behaviour |
|---|---|
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu4b_reference_contract_test.go | All three actual schemas reference-only; real ToolRegistry dispatch retains card guidance; every current channel plaintext field and representative empty/null/wrong-type plaintext shapes refused without writes. |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu4b_reference_selection_test.go | Matching controls; unknown/unbound/unavailable/wrong-kind/empty/null/wrong-type/absent-store/locked-store refs; exact provider dimensions; current/missing/stale revision, inside-lock recheck; retained channel refs cannot follow destination edits. |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu4b_mcp_attachment_test.go | Every MCP recipient dimension, bounded minimal/effective-descriptor attachment, preserve other refs/fields/disabled/global/policies/assignments, no descriptor upsert/empty patch, all-or-none, identical-map no-op. |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu4b_mcp_journey_test.go | Actual add → request_secret tool/card → real Submit → reference-only resume → bounded approved-tool invocation; two separate config writes, provisional truth, stored replacement not selection, old value unchanged. |
| /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu4b_channel_reference_test.go | Thirteen scalar card-selectable fields plus real Slack paired card (fifteen credential fields total), exact consumer mapping and account/endpoint/instance/field mismatch refusal. |

Supporting fixtures are /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/secret_reference_fixture_test.go and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/acceptance-checkout-9c48ab2bc/pkg/sysagent/tools/wu4b_fixture_test.go. Real CommitCard/recipient metadata/config storage are never mocked. Opaque names plus independently literal mismatch recipients prevent name/existence checks from impersonating exact consent. Runtime/network callbacks alone are doubled, record call counts/arguments where the requirement needs them, and assert mutation locks were released. No elapsed-time oracle, paid provider, real user home, generic deletion framework or new state vocabulary.

## Execution and audit handoff

**Correction:** the first schema attempt had a test-only typed-policy compile error, and an initial bounded-attachment slice hit an uninitialized test agent-tools pointer. Those attempts are not behavioral RED evidence. Two initial config-failure controls returned early before reaching the intended fault; final tests now assert exactly one attempted save, and final runs are RED on that missing prerequisite. No expected result was changed to match observed implementation output.

Final base evidence: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/wu4b-final-base-red/runs.tsv and its manifest bind 26 named rows to the original base and exact source hashes, one guarded slotrun process at a time. Twenty-five named rows fail; the descriptor-refusal row passes. After this run, one redundant type assertion in the unbound-reference setup was replaced with the identical already-assigned string literal; no expectation or tool argument changed. Two author re-runs were memory-gated75, so the initial publication disclosed that final-byte gap. It is now closed by the exact named dispatcher re-run on source archive HEAD8a577a85a (original9c48 production plus final published tests): /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/q4-dispatcher/row3.log, named FAIL/exit1, all27 nested cases executed, no compile/panic/zero-collection error. This author read the raw receipt and compared the executed source with the published file; SHA2560abcf737505ee2dd7727ded68786fd21034a839651c5a71d5585883466827e1f. The missing provider/MCP reference prerequisites are still not claimed as decisive later-guard failability. Pinned lint also compiled/analysed the final source successfully. Missing reference-input/attachment prerequisites are distinguished from decisive proof of every later recipient guard. The latter, author-green acceptance, production mutations and Proof-of-failability are **deferred to a fresh CHECK**. A green-on-base row is explicitly UNVERIFIED for failability.

Pinned 2.10.1 package lint initially refused at its unchanged 4GiB memory precheck (exit75, UNEXECUTED). A later retry admitted at 4,706,226,176 bytes and passed: `0 issues.`, exit0, a fresh GOLANGCI_LINT_CACHE, concurrency1 and no issue caps, one package through slotrun. No full suite, parallel local test or guard bypass. Execution of every adapted legacy positive row against implementation belongs to the implementing lead's narrow GREEN/CI evidence, independently read by fresh CHECK; compile alone is not claimed as behavior verification.

| Claim | Evidence | Certainty |
|---|---|---|
| Translation follows explicit D2/Q1-A/Q2-A/CONFIRM-A, not observed output | Written plan: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/WU4B-RED-PLAN.md; approved old fault/replacement map above; pre-change original test bodies read before translation | Verified instruction/source; runtime semantics only where receipts demonstrate them |
| No production shim or code edit | Tests-only staged scope/diff check; both obsolete QA exports removed with their three callers translated. Original plaintext test untouched. | Verified source scope; broader impact Inferred because GitNexus unavailable |
| RED evidence and remaining gaps are explicit | Final runs/manifest above: 26 named, direct exits, unchanged sources. Initial instrument errors excluded. Pinned-lint final receipt: /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/wu4b-pinned-lint.log, `0 issues.`, exit0; initial guard refusal retained as /Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/l1/wu4/qa-red/wu4b-pinned-lint-attempt1.log, exit75. | Verified receipts; CHECK/mutation and full-suite status Unknown |
| **Self-check** | Re-read translation diff against old test bodies, approved fault map and three-tool boundary; gofmt and whitespace check exit0; final serial receipt manifest source hashes unchanged; zero skipped/deleted tests outside the explicitly superseded hook/oracle translations. | Verified source/receipt check; no independent audit claim |

skills: omnipus-shared-rules, elicify-test-writing, gitnexus-impact-analysis
