# Effort levels — RED repair traceability, rounds 1–4

Correction: said the three S-71/S-78 wire failures could remain catalog prerequisites, wrong because positive build tests must declare their capability Given independently of production's vendored data, correct is a real parsed catalog fixture plus a separate real-vendored-data no-effort witness. Exact wire oracles are unchanged and no production catalog data is added. Both paths now execute; the earlier missing-Given failures are test-fixture diagnostics, not product RED.

**Round-4 state:** same author; test-only reconciliation in `/workspace/wt-effort` on `landing/effort`. Local test-only commit: `db3b922efdeb3a82f1a77f7dd927f063535bc763` (39 explicit test paths, human author/committer, no AI trailer, no push). Actual twelve-package pinned lint: exit 0, `0 issues.` The final-recap-source agent-only supplement is **PENDING for independent CHECK**, explicitly handed off by the chief; the queued author retry was stopped, no owned job remains, and no result is invented. All older round statuses below are historical. No production change, independent CHECK, mutation proof, cut-commit acceptance or feature-reachability acceptance is claimed. This commissioned document remains unstaged under the direct user's test-only commit authorization; the chief's later separate-document-commit request cannot override that restriction.

Correction: the earlier note tests omitted the mapped/sent value needed by their exact sentences; the S-10 and S-74 no-confirm checks also searched Memory's unrelated Save button. Those were test defects, not product RED. Round 3 supplies the missing record value and scopes the checks to the real switch popover, without changing the expected sentences. Stateful API fixtures, per-test mock reset and acknowledged-autosave ordering are repaired below.

**Current round-3 scope (2026-11-01):** six commissioned test files, with the chief's same-defect S-74 extension. Initial worktree HEAD was `7e167c32fcd29099f8430f33f5bdd710bfe96fd3`; production developers continue working concurrently. Local test-only commit: `82c9391b28d6f8c6c485b776c23f0e8f8141460b`, no push. The older scenario statuses, limitations and receipt tables in this document are **historical observations**, not claims about today's implementation. Read the round-3 receipts for the touched files only. This commissioned document update remains unstaged; the authorized local commit contains test sources only.

Correction: round 1 claimed S-58 tool model-change rows and S-60 Memory presentation coverage that did not exist, and called permissive loop-wire checks exact. Those claims were wrong. Round 2 adds the missing boundaries and exact OpenRouter assertions below; independent CHECK has not yet ruled on this repair.

Correction: the first round-2 S-02 fixture returned the visible answer without invoking ordinary reply publication; the first S-58 fixture asserted the tool input's flat pair shape against the canonical nested agent entity. Those were fixture errors, not product defects. The final receipts use the production session-worker publication path and the declared `AgentModelConfig` nested pair. Earlier mixed-failure runs are retained as diagnostics only; the Off, refusal, no-leak and atomicity expectations were not relaxed.

Correction: the original pack called S-02 a passing pin and described several unexecuted or incomplete paths as covered. That was wrong: the retired keys still have runtime representations, browser collection is not execution, and downstream assertions blocked by a missing implementation have not been verified. This revision distinguishes those states.

- Specification: `/workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md`.
- Independent inputs: `/workspace/omnipus-coordination/designs-20261029/effort-red-check-report-1.md`, CHECK-01 through CHECK-16; `/workspace/omnipus-coordination/designs-20261029/effort-red-check-report-2.md`, all six new findings and the CHECK-03/CHECK-08 carried residuals. This document is the author's repair ledger, **not independent CHECK**.
- Worktree: `/workspace/wt-effort`; branch `landing/effort`; round-1 pre-repair HEAD `e03c89dc3a22ee2dfb351ac24639d21dfd947892`; round-2 base `1d53b345c9775ec27ad6f1b3b85a542240d2d2fd`; assigned release pin `1d3989df0`.
- Process version: `2026-10-29.1`. Tests and test harnesses only, plus this commissioned traceability update. No production repair, push, mutation or full-suite run.
- The older round-2 tables use `SELF` as a historical repair-commit placeholder; consult that round's handoff for its exact test commit. Do not derive the current test commit from this document's Git history: this commissioned document remains unstaged under the test-only commit instruction.

## What the statuses mean

| Status | Meaning |
|---|---|
| RED | A named run failed on a specified missing behavior. It is not an implementation PASS or a claim that every later assertion ran. |
| PIN | The named baseline control passed. It is not red-before-green evidence for a new behavior. |
| BLOCKED / U | The test is loud and failing at a missing production seam; later assertions are coded but **unreached**. No skip replaces that failure. |
| PARTIAL | A specified boundary has a test, but a material additional leg is not established; see the limitations ledger. It is never counted as complete scenario acceptance. |
| N/A | Exactly the four deleted scenario tombstones, not unfinished work. |

GREEN, scratch-clone mutation checks, proof-of-failability checklist completion and an independent CHECK verdict are **deferred to CHECK**. No RED suite here is described as feature delivery.

## Round 6 — chip strings, founder decision 2026-11-07

The visible chip is the level actually used, or "Nothing sent". The longer sentence is the accessible name. Dashed outline means inherited (`data-effort-inherited`); the amber dot (`data-effort-mark=attention`) means a different level was sent, or nothing was sent; the gold dot (`data-effort-mark=command`) means `/effort`. C5 (XHigh versus "Extra high") is unchanged: it is still an open question.

| Place | What the test or spec now requires |
|---|---|
| `tests/e2e/effort.spec.ts` S-41 | Before the other conversation: visible "Max", name "Effort: Max, set for this conversation", gold dot present. After: gold dot absent and that name absent. The old negative text check passed even before the reset. |
| `tests/e2e/effort.spec.ts` S-86 lines that open on the model which offers Off | Visible "Off", dashed, name "Effort: Not set: Agent's setting: Off", no amber or command dot. |
| `model-effort-picker.wireframe.test.tsx` CHECK6 F1 | Inherited High on levels `["none"]`, switch false: visible "Nothing sent", amber, name "Effort: Not set: Agent's setting: High, nothing sent here", and `onChange` is not called. Changing the inherited setting to Off without a model switch shows "Off" and still does not call `onChange`. |
| Same file, CHECK6 F5 | `getByRole('dialog', { name: 'Effort · {model}' })` for the ordinary popover and `getByRole('dialog', { name: 'Finish switching to {model}' })` for the blocked step. |
| Spec of record `effort-levels-spec-r10.md` | S-12, S-41, S-72, S-87, the composer state row, the journey, the stale-level row, and the plain-copy line now say the same. |

C4 keeps one sentence, "Omnipus can't change effort for this model.", as the muted chip's text and its accessible name.

## Round-4 test reconciliation — author observations, not independent CHECK

### Repairs and retained oracles

| Commission / trace | Test-side repair; independently required outcome retained | Saved observation |
|---|---|---|
| TL94 / FR-060, FR-076 | `/workspace/wt-effort/pkg/sysagent/tools/effort_red_boundary_matrix_test.go`: replace the invalid readable sibling `max_tokens` with the declared non-secret fixture `temperature: 0.25`. Direct effort, section/reopen, malformed composite and all length/shape checks remain. No credential exception is added. | `/workspace/logs/effort-red/fix4/qa-author/sysagent-before.log`, exit 1: integer 4096 was actually `[REDACTED]` (fixture diagnostic). `/workspace/logs/effort-red/fix4/qa-author/sysagent-final.log`: TL94 and its nine subcases PASS; the combined command remains exit 1 on eight separate T8 prerequisites. |
| Go notes / S-09, S-72, FR-058 | `/workspace/wt-effort/pkg/agent/effort_red_mapping_d2_test.go`: retain round 3's mapped/sent fourth inputs and all ten exact sentences. Only mechanical test-side lint repairs in this round. | `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`: `TestT_S1_Mapping_S09_NoteTexts` and `TestT_S1_Mapping_TurnNotes` execute and PASS. The historical round-3 compilation gap is resolved; the mixed command is not green. |
| Messenger / S-03, S-04, FR-018 | `/workspace/wt-effort/pkg/agent/effort_red_containment_test.go`: drive the real session worker, observe its actual outbound publication, assert exactly two provider calls, unchanged ordinary sentinel and no reasoning marker in that reply. Do not substitute the loop's return value for messenger publication. This messenger test alone does not assert the done frame's failed-turn flag; no such coverage is claimed for this repair. | Same `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`: messenger name PASS. Earlier missing-publication timeout is fixture diagnostic, not feature RED. |
| Delegated parent / S-04, FR-018 | `/workspace/wt-effort/pkg/agent/effort_red_parent_test.go`: **preserve** the spec's exact sentinel, `The model returned an empty response. This may indicate a provider error or token limit.` Also require the real parent frame's discriminator to be ordinary `handback`, not an error notice. Backend/chief repaired production in `96812d968cb5afb601319fad4a03cc6f53e1e39c`; QA did not. | `/workspace/logs/effort-red/fix4/qa-author/agent-before.log`, exit 1, captures the earlier parent failure; `/workspace/logs/effort-red/fix4/qa-author/agent-final.log` captures the strengthened parent test PASS. This does not make the combined command green. |
| Durable restart / S-26, S-37, S-38, S-39 | `/workspace/wt-effort/pkg/agent/effort_red_containment_test.go::newContainmentLoop` now uses the constructor-owned shared store. `restartContainmentLoop` closes the old runtime and lets the real constructor reopen that same durable archive; require distinct runtime/store objects and the same base directory. Conversation, command and rejection-lifecycle callers keep scope isolation, High/inherited Medium wire bodies, real file-tool marker, exact record, persisted override and config-byte assertions. Do not bypass duplicate-session cutover protection by overwriting the loop's store. | `/workspace/logs/effort-red/fix4/qa-author/fixtures-compile-check.log`, exit 0; later repaired names PASS in `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`. Both the fixture lifecycle defect and its repair are recorded, not reclassified as production RED. |
| Retry accounting / S-28 | `/workspace/wt-effort/pkg/agent/effort_red_request_rejection_test.go`: count extra requests **without effort**, as the spec requires. Require one for 400/422 and zero for the other five statuses. Require each ordinary 429 transport retry to preserve the whole original effort-bearing body and its final error to remain an error. All seven status cases remain. | `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`: rejection-matrix top-level name and all status rows PASS. The previous three-request 429 failure was an incorrect total-call oracle, not an effort-removal defect. |
| Composer session return / existing persistence regression plus effort catalog | `/workspace/wt-effort/tests/session-return/fixtures.ts`: declare `/api/v1/providers/catalog` with the generated `ProvidersCatalog` type, actual fixture models and explicit reasoning capability data. Preserve the unexpected-request guard and every model/session persistence expectation. `satisfies Provider[]` checks the original providers without erasing their concrete string inference. Neither consuming test file is edited. | `/workspace/logs/effort-red/fix4/qa-author/modelpicker-before.log`, exit 1: 14 undeclared-request failures. `/workspace/logs/effort-red/fix4/qa-author/modelpicker-final.log`, exit 0: 14 PASS. `/workspace/logs/effort-red/fix4/qa-author/session-return-neighbour.log`, exit 0: 13 PASS. |
| Command binding / S-37 to S-42 | `/workspace/wt-effort/pkg/commands/effort_red_commands_test.go`: bind the real Runtime callbacks to an isolated real durable session store, fixture agent Medium and declared external capability levels. Reject undeclared conversations, surface handler errors, count zero forbidden model switches. S-39 actually closes/reopens the store and replaces Runtime/Registry. S-42 supplies its distinct S-36 capability premises instead of forcing contradictory replies through one list. | `/workspace/logs/effort-red/fix4/qa-author/commands-before.log`, exit 1: missing binding/obsolete restart placeholder. `/workspace/logs/effort-red/fix4/qa-author/commands-after.log`, exit 0: all eight effort names plus the count test execute and PASS. |
| Command catalog regression | `/workspace/wt-effort/pkg/commands/surface_test.go::TestBuiltinDefinitions_CountsAndSurfaces`: `/effort` adds one canonical/web command, never a CLI command. Counts are canonical 17, web 13, CLI 16 and hidden 0. | Same `/workspace/logs/effort-red/fix4/qa-author/commands-after.log`, exit 0, named count test PASS. |
| File-size repair, no test deletion | Move thirteen `TestResolveMediaRefs_*` functions and `real1x1JPEGBytes` intact from `/workspace/wt-effort/pkg/agent/loop_test.go` to `/workspace/wt-effort/pkg/agent/loop_media_refs_test.go`. Original file 3,137 → 2,714 lines; final new file 435. **Correction:** the initial split receipt's 436 was the pre-format count, not the final file count. `/workspace/logs/effort-red/fix4/qa-author/precommit-integrity.json` rechecks all thirteen exact declaration bodies, the helper and the contiguous test group against the original source. No budget manifest edit. | `/workspace/logs/effort-red/fix4/qa-author/media-split-proof.json`: exact moved-group SHA-256 `7f298d1bbcd4a58c91e770532dee4d8fb7d7ed954753a62b9115c6dd062c68a8`, helper SHA-256 `f1d798f74b70b5c44105cfa636dcc5936a218783c7fde75cc7598f4007ccf598`. All thirteen moved names PASS in `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`. |
| Actual lint defects, not suppressions | Four package-local generic checked-type helpers preserve type premises with a recorded test failure and panic, including HTTP-handler goroutines. Reviewed AST transformation replaces 47 forced assertions in thirteen exact test files. Remaining changes are import grouping, obsolete loop captures, preallocation, parameter names, shadow renames and one unused endpoint-count helper; no test case/oracle is deleted and no linter configuration or suppression is added. | `/workspace/logs/effort-red/fix4/qa-author/lint-resume-1.log`, exit 1: pinned 2.15.0, 72 actual issues. Dry-run/apply receipts exit 0. Final lint result is recorded in the execution ledger below, not inferred from these edits. |
| Recap positive capability Given / S-71, S-78, FR-070 | `/workspace/wt-effort/pkg/agent/effort_red_recap_request_test.go::installRecapEffortCatalog`: parse external data through the production catalog parser, install through the existing provider-catalog seam and restore the prior catalog in cleanup. Keep real recap, mapper, factory and HTTP encoders. Preserve exact level Off `reasoning.effort: none`, switch Off `reasoning.enabled: false`, configured High `reasoning.effort: high`, fallback On `reasoning.enabled: true`, no-Off omission and output cap 2048. | Three pre-repair failures in `/workspace/logs/effort-red/fix4/qa-author/agent-final.log` are missing-Given diagnostics, **not product RED**. The first post-repair attempt ran zero tests because the runner's scratch directory was absent. `/workspace/logs/effort-red/fix4/qa-author/recap-fixture-tmpdir-retry.log`, raw exit 0: all six targeted top-level names PASS, including these three positive tests. Recap source SHA-256 before/after/current is `944e600811f15042657adb278d7873b0a0955d68a51ab13b0719617f2a583cdc`. |
| Real-vendored-data witness / US-8 AS3, S-71 | Separate `TestT_C5_S71_VendoredCatalogWithoutReasoningSendsNothing` parses `catalog.EmbeddedSnapshot`, positively requires all three real model rows, pins their current no-reasoning Given and drives a real recap for each. Require all six effort carriers absent and cap 2048. The original unknown-model no-reasoning test remains. A later vendor refresh invalidates this explicit current-data Given, never the positive encoder oracles. | `/workspace/logs/effort-red/fix4/qa-author/recap-fixture-tmpdir-retry.log`, raw exit 0: the new top-level witness and all three real-model subcases PASS. It is a baseline PIN, never invented red-before-green proof. Current vendor-data evidence: `/workspace/logs/effort-red/fix4/qa-author/recap-catalog-evidence.json`. |

### Commissioned legacy migrations, old → new

| Exact test / file | Retired expectation → new specification expectation | Original intent preserved; execution |
|---|---|---|
| `/workspace/wt-effort/pkg/agent/loop_reasoning_test.go`: `TestProcessMessage_PublishesReasoningContentToReasoningChannel` → `TestProcessMessage_PublishesOnlyOrdinaryReplyWhenReasoningIsPresent` | Retired reasoning-channel publication → FR-017/S-02: exactly one ordinary telegram/chat1 reply `final answer`, with `thinking trace` absent from every observed outbound destination. Real worker publication and the existing bounded wait remain. | Ordinary answer routing and separation from private reasoning remain directly observed. New name PASS in `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`. |
| `/workspace/wt-effort/pkg/agent/session_end_behavioral_test.go::TestRunRecap_HappyPath_PersistsLastSessionAndRetro` | Hardcoded `extended_thinking` / per-call `extra_body` options → FR-070: for this unoffered fixture model, the complete option map is exactly integer `max_tokens: 2048`, with no hardcoded Off hints. Positive wire encoding is separately driven by declared capability data above. | Preserve real recap invocation, one provider call, JSON, recap/carry-forward/retro artifacts and cost cap. PASS in `/workspace/logs/effort-red/fix4/qa-author/legacy-after.log` and `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`; neither mixed command is globally green. |
| `/workspace/wt-effort/pkg/agent/prompt_cache_expanded_thinking_replay_test.go::TestExpandedThinking_SignedBlocksReplayOnToolContinuation_Plain` (name unchanged) | Retired manual-budget arming → FR-012/FR-037a: real capability data, adaptive thinking and Low effort, no temperature/display, exact max output 8192 on both requests. Constructor-registered agent and a config-level context-window override make the fake localhost peer a valid fixture. | Every exact signed thinking text/signature, opaque redacted payload, block order/count, tool-use identity/arguments and paired tool-result assertion remains; two captured requests and one real tool execution. PASS in `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`. |
| Same file, `TestExpandedThinking_SignedBlocksReplayOnToolContinuation_Streamed` (name unchanged) | Same arming change, on the actual streaming response path. The first response has the native signed blocks, so the S-34 missing-own-blocks guard does not apply to the continuation. | All original exact replay assertions remain. PASS in `/workspace/logs/effort-red/fix4/qa-author/agent-final.log`. `/workspace/logs/effort-red/fix4/qa-author/legacy-after.log`'s earlier fake-peer context-window refusal is a fixture diagnostic, not product RED. |

### Round-4 execution ledger and qualifications

All logs in this section are under `/workspace/logs/effort-red/fix4/qa-author`. Completed runs have direct raw exits and JSON receipts with argv, log digest, before/after HEAD and test-source fingerprints. The normal command is `/usr/local/bin/heavy`; only pinned golangci-lint uses `--local`. Go commands use `CGO_ENABLED=0`, `goolm,stdjson`, `-p 1`, `-count=1`, `-v` and an explicit exact-name group in one package. One author-owned test process at a time; the independent linter may overlap it. Frontend commands name one file each. No full suite runs locally.

| Receipt | Raw exit / observed scope | Qualification |
|---|---|---|
| `/workspace/logs/effort-red/fix4/qa-author/agent-final.log` | 1; 68 top-level names: 64 PASS, four FAIL | Three recap capability-Given failures are repaired above; one T8 `get_agent fields` prerequisite remains. Successful named observations are not a green command. |
| `/workspace/logs/effort-red/fix4/qa-author/commands-after.log` | 0; nine top-level names PASS | Command fixture/count repairs, not acceptance of the production agent command path by this boundary alone. |
| `/workspace/logs/effort-red/fix4/qa-author/common-final.log` | 0; six top-level names PASS | Actual provider wire/carrier/tool-continuation boundaries. |
| `/workspace/logs/effort-red/fix4/qa-author/gateway-final.log` | 1; ten top-level names: eight PASS, two FAIL | S-88/S-95 actual workflows remain loud BLOCKED: primary uses plain Chat instead of the required stream. Later workflow assertions remain unreached. |
| `/workspace/logs/effort-red/fix4/qa-author/sysagent-final.log` | 1; eighteen top-level names: ten PASS, eight FAIL | TL94 repair executes; eight failures remain at the accepted T8 `fields` / `model_chain` prerequisite. No downgrade or skip. |
| `/workspace/logs/effort-red/fix4/qa-author/modelpicker-final.log` | 0; one file, fourteen cases PASS | Shared HTTP fixture repair; exact consumer expectations unchanged. |
| `/workspace/logs/effort-red/fix4/qa-author/session-return-neighbour.log` | 0; one file, thirteen cases PASS | Other existing consumer of the same shared HTTP fixture. |
| `/workspace/logs/effort-red/fix4/qa-author/typecheck-final.log` | 0; actual `npm run typecheck` / project-reference build | `NODE_OPTIONS=--max-old-space-size=4096`; never the bare no-op TypeScript command. Default-heap attempt exited 134; the 4-GB attempt found a real optional-field fixture typing error, exit 2, then `satisfies Provider[]` repaired it. Both failed receipts retained. |
| `/workspace/logs/effort-red/fix4/qa-author/recap-fixture-final.log` | 1; zero tests executed | Runner-b lacked explicit `/workspace/.cache/tmp`. Setup failure only, not product RED or an encoder result. |
| `/workspace/logs/effort-red/fix4/qa-author/recap-fixture-tmpdir-retry.log` | 0; six exact recap top-level names PASS | External scratch directory created inside the job; heavy's actual runner-loss/local-slot fallback is recorded rather than bypassed. Same recap source hash before/after and at reconciliation. |
| `/workspace/logs/effort-red/fix4/qa-author/recap-format-preview.log` | 0; pinned formatter, exact recap file, empty `--diff` | `/workspace/logs/effort-red/fix4/qa-author/formatter-instrument-control.log`, exit 1, shows the same configured formatter catches/removes a deliberately unused import in an external scratch probe. Actual formatting check can see the failure; it is still not a final lint verdict. The backend's earlier recap goimports finding remains historical evidence. |
| `/workspace/logs/effort-red/fix4/qa-author/lint-final-3.log` | 0; pinned 2.15.0 built with go1.27.3, `0 issues.` | Twelve explicit effort packages. The recap source changed during this long run; all other captured test hashes are unchanged. `/workspace/logs/effort-red/fix4/qa-author/lint-final-source-reconciliation.json` records the gap rather than silently promoting the result across edits. |
| `/workspace/logs/effort-red/fix4/qa-author/lint-agent-final-source.log` | Harness-interrupted; no completed raw-exit receipt | It emitted no version/result. `/workspace/logs/effort-red/fix4/qa-author/lint-agent-cancelled-process-check.json` confirms no surviving owned log/linter process before the serial retry. Never call this green. |
| `/workspace/logs/effort-red/fix4/qa-author/lint-agent-final-source-retry.log` | **PENDING for independent CHECK; author retry stopped, no raw verdict** | Chief directed the local test commit not to wait on occupied pod slots and assigned this check to the independent auditor. The author stopped its own queued retry and confirmed no surviving owned log/linter processes in `/workspace/logs/effort-red/fix4/qa-author/final-precommit-check.json`. This is an explicit final-source lint gap, not a final-source PASS. |

The first two final-lint attempts were interrupted by the harness after its foreground/background limits; neither has a completed raw-exit receipt. Their silence is **not green**. The third tracked executor is not duplicated. The author keeps the main turn active while the harness tracks a long-running job; no turn is ended with owned work running. Logs and scratch stay outside the worktree. For future runner commands, keep shell `TMPDIR`/`GOTMPDIR` on the pod rather than explicitly forwarding a nonexistent pod-only directory inside the heavy command.

**Residual prerequisites, not deferred bugs silently called PASS:** the chief accepts the T8 reader/schema and S-88/S-95 multi-candidate streaming prerequisites as declared limitations. They still fail loudly; downstream assertions are unverified. The real vendored catalog's missing reasoning descriptors are separately witnessed and are a real reachability qualification, not a reason to omit positive build-test capability data. No formal GREEN/CHECK, mutation proof, cut-commit/CI acceptance or hands-on UAT is awarded by these author observations.

GitNexus tools were unavailable. Controlled direct reference sweeps are saved in `/workspace/logs/effort-red/fix4/qa-author/impact-references.log`, `/workspace/logs/effort-red/fix4/qa-author/impact-follow-up.log`, `/workspace/logs/effort-red/fix4/qa-author/new-helper-control.log`, `/workspace/logs/effort-red/fix4/qa-author/media-native-references.log` and `/workspace/logs/effort-red/fix4/qa-author/recap-addendum-impact.log`. Impact is **Inferred**, not a graph run. Final scoped source/diff inspection is the fallback for unavailable `detect_changes()`; no graph receipt is fabricated. Final report: `/workspace/logs/effort-red/fix4/qa-author/round4-report.md`. `/workspace/logs/effort-red/fix4/qa-author/final-source-scope.json` records 168 → 169 top-level Go test declarations: only the commissioned reasoning-test rename plus one added real-vendor witness, with unchanged skip/parallel/suppression/build-tag markers. `/workspace/logs/effort-red/fix4/qa-author/commit-verification.json` verifies the exact 39 committed files, every final source blob hash, human author/committer, no AI trailer and an empty staging index. The traceability document and unrelated provenance/binary remain outside that commit. No owned work is left running; final-source lint remains a named CHECK handoff, not silence promoted to green.

## Round-3 targeted test repairs — historical author observations, not independent CHECK

| Report / scenario | Corrected test boundary and retained specification oracle |
|---|---|
| 1 / S-09, S-72, FR-058 | `/workspace/wt-effort/src/lib/effort/mapEffort.test.ts` and `/workspace/wt-effort/pkg/agent/effort_red_mapping_d2_test.go`: pass mapped `low` / `high` / `medium` to switch-note rendering and actual sent `low` to the mapped turn note. Every exact expected sentence is unchanged. S-09 names source, result and note separately; source/model alone cannot determine the result. The spec fixes this data dependency, not an API arity. The existing fourth renderer input is appropriate. |
| 2 / S-10 | `/workspace/wt-effort/src/components/ui/model-effort-picker.test.tsx`: no Save/Confirm inside the real switch dialog; High commits exactly one literal pair/effort update, closes the popover/radios and leaves Memory's unrelated Save present. |
| 3 / S-07 | Same picker file: await the actual failed catalog state's Retry button before checking retained High and disabled choices. High's loading presentation no longer masquerades as a completed failure state. |
| 4 / S-93, T-L7 | `/workspace/wt-effort/src/components/settings/effort-red-recap-limits.test.tsx`: file-level `vi.resetAllMocks()` clears call histories and queued one-off behavior before nested API setups; catalog success is restored. Exact one-writer, array-body, counter, draft and failure assertions remain. |
| 5 / S-86 | `/workspace/wt-effort/src/components/agents/AgentProfile.effort-inheritance.test.tsx`: initial valid `updated_at`; PUT updates the acknowledged API record with a newer timestamp, and subsequent GET returns it. All pair/effort, no-autosave, focus and cancellation assertions remain. The stale pre-save GET is no longer injected after a successful save. |
| 6 / H6 and FR-054 | `/workspace/wt-effort/src/components/settings/MemorySection.test.tsx`: hold the primary-slot autosave, require disabled picker and section Save, reject a second dispatch/reopen, then return complete acknowledged settings and wait for re-enable before reopening. The original two-row/second-provider-chosen assertions remain. **Coverage is now provider identity across acknowledged autosave plus one-writer ordering**, not reopening an unsaved draft. |
| Same-defect extension / S-74 | Chief commissioned the additional repair after it was reported: Low/absence of Medium and no Save/Confirm are checked inside the reopened switch dialog; ordinary Memory Save remains present. A previous whole-document assertion failure is not counted as product RED. |

### Round-3 execution receipt ledger

Validation is serial, one frontend file per command through `/usr/local/bin/heavy --local`. The two exact Go note tests also use the chief-requested local slot, tags `goolm,stdjson`, `CGO_ENABLED=0`, `-p 1`, `-count=1` and `-v`. Each completed command has a JSON receipt with raw exit, command, log digest and before/after source fingerprints. After the tool repeatedly killed queued commands at its foreground/background limit, the serial executor used a longer tracked allowance; the main turn remained active and foreground-waited on that sole executor. No second test or heavy job was launched by this author. These are concurrent working-tree observations, **not immutable cut-commit acceptance**.

| Scope | Current observed result | Raw exit | Saved log |
|---|---|---:|---|
| TypeScript mapping and notes | 46 cases PASS, including all ten unchanged exact note sentences | 0 | `/workspace/logs/effort-red/fix3/qa-author/local-final-map.log` |
| Picker | 21 cases PASS, including S-07, S-10 and S-74 | 0 | `/workspace/logs/effort-red/fix3/qa-author/local-retry-2-picker.log` |
| Recap limits / T-L7 | 13 cases PASS, including all five S-93 draft, counter, failure and one-writer cases | 0 | `/workspace/logs/effort-red/fix3/qa-author/local-retry-1-recap.log` |
| AgentProfile inheritance | All five S-86 cases PASS, including exact High save body and focus | 0 | `/workspace/logs/effort-red/fix3/qa-author/local-retry-1-profile.log` |
| Neighbour MemorySection | 17 cases PASS, including both duplicate-provider cases and held primary autosave/reopen | 0 | `/workspace/logs/effort-red/fix3/qa-author/local-retry-1-memory.log` |
| Go `TestT_S1_Mapping_S09_NoteTexts` and `TestT_S1_Mapping_TurnNotes` | **UNVERIFIED**: zero named tests ran. Production compilation fails on `switchCandidates`, `frameProvider`, `pairSwitchNeeded` and `refuseUnresolvedPair` during the backend author's in-flight pair-routing edit. This is not right-reason feature RED. | 1 | `/workspace/logs/effort-red/fix3/qa-author/local-retry-2-go-notes.log` |

The frontend total is **102 executed PASS cases across five individually targeted files**. The first picker execution had 20 PASS and one test-defect failure: the added positive Save role lookup occurred after a modal aria-hid the background. The final test captures the real Save before opening, then checks that same node remains in the DOM while the no-confirm assertion stays scoped inside the popover. `/workspace/logs/effort-red/fix3/qa-author/local-retry-1-picker.log`, raw exit 1, is retained as diagnostic history, not product RED. The only source fingerprint changing during the recap run was this separate picker test file; the recap source and captured production dependencies were unchanged.

The earlier Go attempt reached package setup but failed before either named test because `/tmp` could not create its test home: `/workspace/logs/effort-red/fix3/qa-author/local-retry-1-go-notes.log`, raw exit 1. Its retry uses owned `/workspace/.cache/effort-fix3-tmp` for both `TMPDIR` and `GOTMPDIR`; no shared files were deleted. `/workspace/logs/effort-red/fix3/qa-author/tempdir-preflight.log` records two read-only disk checks, raw exit 0. The chief confirmed that the subsequent undefined production symbols were still being written and commissioned a local round-3 commit with this explicit compile-block qualification; round 4 re-runs the two names once that edit compiles. No test pass or feature RED is invented from either Go failure.

The first two local admission attempts were cancelled without executing tests; the plain-heavy attempt was killed by the harness time limit after a copy-process exit 24, with no raw test exit. A later local batch completed the 46-case mapping file, then was killed while the picker was still queued; its valid completed map receipt is retained separately. The cancelled portions are **not RED proof**. Receipts: `/workspace/logs/effort-red/fix3/qa-author/baseline-cancelled.json`, `/workspace/logs/effort-red/fix3/qa-author/artifact-relocation.json`, `/workspace/logs/effort-red/fix3/qa-author/remote-final-cancelled.json`, and `/workspace/logs/effort-red/fix3/qa-author/local-final-cancelled.json`. Twenty-three owned ignored browser artifacts were moved intact from `/workspace/wt-effort/test-results/effort-fix1` to `/workspace/logs/effort-red/fix3/qa-author/relocated-round1-browser-artifacts`; hashes and paths were preserved. No infrastructure bypass or other owner's process stop was used.

Fresh pre-test-repair RED reproduction was not obtained in those queued attempts. Earlier failing-first logs remain historical; a corrected fixture's current PASS is not retroactively claimed as new RED proof or mutation evidence. The untouched chat composer, `/effort`, answer-note, onboarding, default-model and remove-provider integrations are not accepted by these touched-file runs. Their prior missing-implementation failures remain separate history; concurrent implementation is not silently promoted to PASS. GREEN acceptance, scratch-clone mutations, proof-of-failability completion and independent CHECK remain **deferred to CHECK**.

GitNexus tools were unavailable. `/workspace/logs/effort-red/fix3/qa-author/impact.log` and the S-74 reference sweep in `/workspace/logs/effort-red/fix3/qa-author/remote-final-cancelled.json` record the direct fallback. Impact is **Inferred**, not a graph verdict. Final scoped diff review substitutes for unavailable `detect_changes()`.

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
