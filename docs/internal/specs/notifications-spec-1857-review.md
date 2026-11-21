# Adversarial Review: Notifications — #1857 amendment

**Document reviewed:** `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md`

**Mode:** SPEC

**Round:** Round 1 of 2 — the #1857 amendment only

**Review date:** 2026-11-15

**Verdict:** **REVISE**

**Amendment reviewed:** `5f9d0a2ecf..0a511d4e94`; worktree HEAD `0a511d4e94363f903ae88eefee578ed77c52c204`

**Production reference:** `/workspace/wt/rel-head-1857`, HEAD `5d20e539fa63624189d82f71c45418512b219222`

## Executive summary

**REVISE: 8 MAJOR and 3 MINOR findings.** The amendment preserves the intended fail-closed boundary, but its acceptance tests can certify the old generic behaviour, its surface map misses the actual parent-status path, and several proposed messages claim execution states or causes the producer does not establish. It also transcribes an accepted attribution decision incorrectly.

Frontend contract, journey, accessibility and design-system concerns received source-level review alongside the backend. No browser, build or test was run: accessibility and visual behaviour remain unverified, and no implementation PASS is claimed.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 8 |
| MINOR | 3 |
| OBSERVATION | 0 |
| **Total** | **11** |

### Review boundary and method

- Only the #1857 additions and their fit with the approved specification were grilled. The founder-approved remainder, dated 2026-11-07, is not reopened. The drafter's earlier “correction pass 1” is not this amendment's first grill-and-fix round.
- Section, requirement, scenario and wording references below refer to the document identified above. Production citations refer only to the pinned release worktree, not the older amendment worktree.
- GitNexus is unavailable on this pod. Verification used direct file reads, targeted `rg` searches and read-only Git inspection. No graph-based impact analysis is claimed.
- The Mac team's two reported cases were read in the successful `gh issue view 1857 --repo elicify-ai/omnipus --comments` output. They are evidence, not approved wording. Subsequent GitHub access was rate-limited; no further issue-state claims are made.
- “Verified” below means the document or source mismatch was read directly. Predictions of what an implementation following that text would display are **Inferred**, not runtime reproductions.

## Findings

### CRITICAL findings

None established within this amendment's scope. This is not a security certification.

### MAJOR findings

#### 1857-R1-MAJ-001 — Save-failure wording asserts execution states the shared producer cannot prove

- **Severity:** MAJOR.
- **Lenses:** Inconsistency; UI states and journeys; testability; voice accuracy.
- **Affected sections:** §13.8.2 B7/B11; §13.8.3 W9/W15 and neutral twins; §13.8.4; FR-092.
- **Failure scenario:** A tool-call transcript append fails while a turn is already running. Mapping the shared save error to W9 tells the user that their message “wasn't sent” and asks them to send it again, although the tool still runs. Mapping the same helper to W15 instead says “I answered” before a final answer exists. A late gateway transcript-save failure is a further, separate route that the table does not identify.
- **Evidence:** `/workspace/wt/rel-head-1857/pkg/agent/started_tool_outcome.go::beginStartedTool` calls `reportSessionSaveFailure` with `messageNotSavedError{role: "tool"}` and returns `true`; its execution is not stopped by the failed receipt. `/workspace/wt/rel-head-1857/pkg/agent/turn_exit.go::reportSessionSaveFailure` emits the shared `session_save` error but establishes neither admission refusal nor completed output. `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn_response.go::recordToolCalls` also reports an intermediate assistant-save failure. `/workspace/wt/rel-head-1857/pkg/gateway/websocket_streamer.go::publishSaveFailureNotice` emits `MessageNotSavedReply` in an error frame without a typed `llm_error` payload. W9 says “so it wasn't sent”; W15 says “I answered”; B11 incorrectly treats the shared helper as proof of completion.
- **Recommendation:** Split the amendment's disposition by observed stage: message admission refused before work, in-progress transcript/checkpoint failure, and completed-output persistence failure. Bind each to its actual producer and a safe catalogue variant. Only use “not sent” or “answered” where that state is established. Include the gateway late-save notice. W15 must also supply an evidenced recovery action or an actual system action; its current consequence-only sentence does not meet voice rule 7. Do not invent automatic retries or promise durability. Add stage-specific live/reload tests, including the tool receipt failure that does not prevent execution.
- **Certainty:** **Verified, high confidence** for the producer mismatch; misleading retry and possible repeated work are **Inferred**, not exercised.

#### 1857-R1-MAJ-002 — Resume unavailable is not evidence that the workspace changed

- **Severity:** MAJOR.
- **Lenses:** Inconsistency; UI recovery; voice accuracy.
- **Affected sections:** §13.8.2 B16; §13.8.3 W14 and its neutral twin.
- **Failure scenario:** A continuation has no retained CLI conversation or driver, but the workspace is unchanged. W14 nevertheless tells the user that the workspace changed, sending diagnosis toward the wrong cause.
- **Evidence:** `/workspace/wt/rel-head-1857/pkg/agent/external_run_session.go::errExternalResumeUnavailable` identifies a conversation that is no longer available; `::errExternalWorkspaceChanged` separately identifies a changed workspace relationship. W14 maps both resume refusal and workspace change to the latter explanation.
- **Recommendation:** Use separate cause variants. The resume-unavailable variant must state only that the previous CLI conversation cannot be continued; reserve the workspace explanation for its typed sentinel. Both may retain the supported “Start a new delegation” action. Test both sentinels with an unchanged-workspace control.
- **Certainty:** **Verified, high confidence** for the distinct causes and conflated wording; the predicted incorrect message is **Inferred**.

#### 1857-R1-MAJ-003 — The parent lifecycle line is not the parent's tool-result text

- **Severity:** MAJOR.
- **Lenses:** Reachability; cross-boundary data flow; UI journeys.
- **Affected sections:** §13.8.4 parent row; §30.4 final paragraph; S-136/T-53.
- **Failure scenario:** The implementation fixes CLI classification and `ToolResult.ForLLM`, then declares the Mac team's parent-side “stopped without finishing” case covered. That human-visible line remains unchanged because it is derived from lifecycle status, not the tool-result sentence. The separate asynchronous failure message to the parent agent can also be omitted from the test while the tool-result assertion passes.
- **Evidence:** `/workspace/wt/rel-head-1857/src/lib/delegationEventLine.ts::delegationEventLineText` renders the `stopped` event as `${agent} stopped without finishing`; `::delegationEventShowsOpen` exposes a child-session link. `/workspace/wt/rel-head-1857/src/lib/delegationEvents.ts::spanTerminalEvent` derives the terminal event from the delegation span. The agent-facing completion route is `/workspace/wt/rel-head-1857/pkg/agent/steer_completion.go::completionDisposition` → `::completionMessage`, which constructs a `SessionMessageError` with failure text. The surface table's `/workspace/wt/rel-head-1857/pkg/agent/translate_error.go::ForLLM` is not a producer symbol: the relevant `ForLLM` field is built in `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::drainExternalRun`.
- **Recommendation:** List the human lifecycle summary, parent-agent completion/inbox message, and direct tool result as separate surfaces. Correct the claim that the tool-result row covers the status line. Preserve the approved lifecycle-summary design: specify and test the summary → **Open** → specific child error journey rather than silently adding arbitrary child text to the summary. Verify typed cause preservation and neutral wording on the actual parent completion route as well as the tool result.
- **Certainty:** **Verified, high confidence** for the separate routes; continued failure of a tool-result-only fix is **Inferred**.

#### 1857-R1-MAJ-004 — T-54 and S-138 can pass while the known-reason failures remain generic

- **Severity:** MAJOR.
- **Lenses:** Testability and false-green risk; requirements consistency.
- **Affected sections:** FR-092; S-138; T-54; §23 traceability.
- **Failure scenario:** B5/B6/B7/B10/B14/B15 remain allowed `CodeUnknown` emitters carrying their existing authored messages. The SPA discards those messages and displays generic copy. T-54 explicitly expects that result, so it passes although FR-092's known-reason requirement is still broken. Changing the attached text at an already allowed site also does not create a new unlisted site.
- **Evidence:** T-54 enumerates those known-cause sites and requires each listed frame to render the generic line. S-138 repeats that oracle. FR-092 simultaneously requires every listed failure kind to receive its specific outcome and says `unknown` never carries a non-catalogue message. `/workspace/wt/rel-head-1857/pkg/agent/translate_error_provider_messages.go::WireLLMError` and `/workspace/wt/rel-head-1857/src/lib/llm-error.ts::getLLMErrorDisplay` explain why hiding an attached message is not proof that the producer stopped emitting it.
- **Recommendation:** Separate three assertions: (1) untrusted attached text is never displayed; (2) unassembled `unknown` payloads contain the permitted catalogue fallback rather than arbitrary authored text; (3) real known-cause producers produce their agreed typed/assembled, non-generic outcomes, live and after reload. Restrict the final residual-unknown list to justified cases, not the very emitters this amendment must fix. Mutation checks must cover both a new emitter and a regression inside an existing allowed emitter. Preserve the approved assembly boundary: only the assembly function may set `message_assembled`, never a producer attaching free text.
- **Certainty:** **Verified, high confidence** for the contradictory assertions; the false-green implementation is **Inferred** from those assertions, not tested here.

#### 1857-R1-MAJ-005 — Several newly promised failure kinds have no complete acceptance oracle

- **Severity:** MAJOR.
- **Lenses:** Ambiguity; completeness; contract-first; traceability.
- **Affected sections:** §13.8.2 B6, B10–B17 and C8; §13.8.3; FR-092; S-133–S-138; T-52–T-54.
- **Failure scenario:** The test author reaches B12 session restore, B13 checkpoint failure or C8 program refusal. FR-092 demands that each use “its row's code” and “its §13.8.3 sentence”, but those outcomes are not supplied. The implementer chooses one and the tests copy that choice, so a wrong attribution, remedy or generic fallback can be accepted without an independent specification oracle.
- **Evidence:** B6 defers the mechanism to WP-0; B10 defers attribution; B12/B13 defer their sentence to WP-6; B14/B15 lack a complete code-and-copy mapping; C8 proposes an extra code and defers its sentence. B17 calls `external_cli_failed` a “config variant”, while the accepted six-code map assigns that code `ambiguous`. S-133/T-52 cover selected C rows, not this complete added B inventory. The open own-limiter scope question is explicit and is **not** counted as a defect here.
- **Recommendation:** Add one amendment disposition matrix covering every newly promised cause: triggering fact, terminal/non-terminal state, selected code, per-code attribution, catalogue variant or manifest ID, required/no-fact slots, both voices, action, surfaces and scenario/test. Have the architect close remaining contract-shape gaps before RED; do not ask an implementer to invent an attribution per sentence. Where WP-6 supplies the final copy, identify the exact manifest row and independent acceptance oracle before its tests are written. Alternatively, explicitly narrow the amendment's promise with the appropriate scope decision. This does not reopen the approved rewrite process for the old corpus.
- **Certainty:** **Verified, high confidence** for the missing mappings; implementation divergence is **Inferred**.

#### 1857-R1-MAJ-006 — The new tests bypass adapters that currently lose the required facts

- **Severity:** MAJOR.
- **Lenses:** Testability; reachability; frontend/backend integration.
- **Affected sections:** S-133–S-137; T-52/T-53; §24; §13.8.4.
- **Failure scenario:** A new CLI-only classifier passes a direct `provider.no-route` fixture, but OpenCode's parser still drops the nested field, or Settings still reduces the event to a string and uses the old translator. The helper chat or “Send a test message” then keeps the generic result despite green classifier tests. A sanitizer unit test likewise cannot establish that all real wire, transcript and parent-message builders omit stderr.
- **Evidence:** `/workspace/wt/rel-head-1857/pkg/agent/runner/driver_opencode.go::parseLine` reads only the top-level `message` for an error and otherwise substitutes `unknown opencode error`. `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun` extracts `ev.Err.Message`; `::sanitizeSmokeTestErrorMessage` calls the shared provider translator. S-133/T-52 start at the classifier. T-53 is labelled an agent unit test while claiming wire, parent and logging outcomes. T-45 supplies a useful assembled-payload renderer check, but does not drive these CLI adapters. §24's U1–U26 do not explicitly reproduce either reported CLI case.
- **Recommendation:** Keep the unit tables, and add integration coverage beginning at a fake CLI process/output boundary, with the real parser, dispatcher, Settings handler, error assembly, persistence and relevant renderer path. Assert the two reported cases, direct start failure, actual parent completion, task/centre neutral copy, and live/reload parity. Inspect serialized responses and persisted records, not merely hidden UI text. Include stdout error envelopes as well as stderr. Add exact-cut hands-on rows for duplicate Additional CLI arguments and model unavailable in both helper chat and Settings. Make mutations that discard nested metadata, leave Settings on its old translator, or lose the typed code on completion fail these tests.
- **Certainty:** **Verified, high confidence** for the adapter boundaries and test-plan omissions; an implementation passing only the listed unit tests is **Inferred** to remain vulnerable to those regressions.

#### 1857-R1-MAJ-007 — The upstream-refusal attribution contradicts the accepted ruling

- **Severity:** MAJOR.
- **Lenses:** Decision consistency; contract correctness.
- **Affected sections:** §13.8.2 C9 and contract paragraph; OQ-1857-2.
- **Failure scenario:** Backend-lead follows the amendment's declared “architect-decided” map and generates `external_cli_upstream_refused` with `user` attribution. The shipped contract then assigns a CLI-provider limit or credit refusal to the user, contrary to the accepted `ambiguous` ownership and the associated copy-rule routing.
- **Evidence:** `/workspace/logs/1857/architect-read.md::Rulings — R-2 / OQ-1857-2` explicitly assigns `ambiguous` to both `failed` and `upstream_refused`. C9 and the amendment's contract paragraph instead propose `user`; OQ-1857-2 calls the map decided and points back to that paragraph. `/workspace/wt/rel-head-1857/contracts/components/schemas/LLMError.yaml::x-user-messages` associates attribution with each code, and `/workspace/wt/rel-head-1857/src/lib/llm-error.ts::codeToAttribution` consumes the generated map.
- **Recommendation:** Restore `external_cli_upstream_refused: ambiguous` consistently in the inventory, contract instructions and decision record. Add the accepted attribution to the contract/copy fixture. This is a transcription correction, **not** a new founder question or a reopening of R-2.
- **Certainty:** **Verified, high confidence** for the contradiction and contract consequence.

#### 1857-R1-MAJ-008 — Nonfatal CLI diagnostics can receive wording that says the run stopped

- **Severity:** MAJOR.
- **Lenses:** State completeness; truthful status; testability.
- **Affected sections:** §13.8.1 CLI-only fallback rule; §13.8.2 C preamble; W4; S-133/T-52.
- **Failure scenario:** OpenCode emits a malformed diagnostic line, then valid output and successful completion. The diagnostic is `Fatal: false`, but it passes through the same sanitizer and error emitter. The new residual CLI code uses W4, which says the run “stopped with an error”, even though dispatch continues and can succeed. There is no nonfatal control case in the amendment's classifier scenarios.
- **Evidence:** `/workspace/wt/rel-head-1857/pkg/agent/runner/driver_opencode.go::parseLine` emits malformed input as an error event with `Fatal: false`. `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::drainExternalRun` sanitizes, appends and emits every error event **before** testing `ev.Err.Fatal`; only the fatal branch sets `runErr`. The C preamble acknowledges that distinction, but the classifier fallback and wording table do not specify a nonterminal presentation.
- **Recommendation:** Keep the accepted CLI-only classification design, but specify that classification alone does not declare run termination. Preserve fatality as an independent fact and give nonfatal diagnostics an explicit safe presentation/disposition that does not claim completion or invite a duplicate retry. Add a malformed-line → valid output → successful end control, asserting no terminal-failure sentence or parent failure. Test the Settings path separately because its current drain treats an error event as a failed test result.
- **Certainty:** **Verified, high confidence** for the nonfatal route; the proposed false terminal message is **Inferred** from applying the new fallback wording to it.

### MINOR findings

#### 1857-R1-MIN-001 — Settings model-unavailable points at the credit/limit wording

- **Severity:** MINOR.
- **Lens:** Internal consistency.
- **Affected section:** §13.8.4 Settings row.
- **Failure scenario:** Following the surface table literally shows W13's rate-limit or account-credit advice for a model-unavailable result instead of telling the user to correct the worker model.
- **Evidence:** The row says “W13's neutral form for the model-unavailable case”; §13.8.3 assigns model unavailable to W2 and upstream refusal to W13.
- **Recommendation:** Change the cross-reference to W2's neutral form and bind it to the C2/B18 fixture.
- **Certainty:** **Verified, high confidence** for the erroneous reference; user-facing effect is **Inferred**.

#### 1857-R1-MIN-002 — The reserved code has two contradictory inventory rows

- **Severity:** MINOR.
- **Lens:** Internal consistency.
- **Affected section:** §13.7 `delegated_task_limit` rows.
- **Failure scenario:** A manifest author selects the later, unqualified row and treats the code as having an active producer, despite the amendment's explicit reserved disposition.
- **Evidence:** §13.7 contains both the newly marked reserved row and the original unqualified row for the same code. OQ-1857-6 records the accepted reserved/no-new-emitter decision.
- **Recommendation:** Keep one reserved row. Retain team-lead's issue-filing action; do not add an emitter in this amendment.
- **Certainty:** **Verified, high confidence** for the duplicate; manifest ambiguity is **Inferred**.

#### 1857-R1-MIN-003 — “Generic today” is overstated and its totals are not reliable

- **Severity:** MINOR.
- **Lens:** Source accuracy.
- **Affected sections:** §13.8.1; §13.8.2 C6, B18, C heading and totals.
- **Failure scenario:** RED fixtures are written to expect `unknown` for every stream-read error or every Settings failure. They then fail for the wrong reason, or are narrowed to miss existing typed branches. The claimed total gives a false impression that the inventory has been reconciled by actual surface and input.
- **Evidence:** `/workspace/wt/rel-head-1857/pkg/agent/runner/driver_stream.go::streamParser` wraps the read cause as `stream read error: ...`; `/workspace/wt/rel-head-1857/pkg/providers/error_classifier.go::IsTransportFailure` recognises, among other cases, `unexpected eof` and `broken pipe`. Through `/workspace/wt/rel-head-1857/pkg/agent/translate_error.go::classifyByMessage`, those examples can classify as `network`, not `unknown`. `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::sanitizeSmokeTestErrorMessage` has separate empty-message, permission and timeout paths. B18 is marked “Yes” but omitted from the stated always-generic total. The gateway untyped save route and authored-reply exceptions also mean the §13.8.1 typed-frame mechanism must not be generalized to all user surfaces (MAJ-001/003).
- **Recommendation:** Qualify the current-state claims by exact input and surface, include typed controls, and recompute totals only after that reconciliation. The desired CLI-only codes remain unchanged: this corrects the release baseline, not the accepted future design.
- **Certainty:** **Verified, high confidence** for the branch distinctions and inconsistent count; exact runtime frequency is **Unknown**.

## Accepted decisions and integration checks

These are constraints for the correction, not questions to reopen.

| Decision | Review result |
|---|---|
| Separate CLI-only classifier; six codes; typed cancellation/timeout exceptions | Preserved. MAJ-007 corrects its attribution transcription; MAJ-008 adds the missing nonfatal state distinction without moving CLI matching into the provider classifier. |
| No offending flag names | Preserved. W1 may name the setting, not the flag, its value, or a reconstructed command. |
| Narrow terminal promotion for A4/A5 | Preserved: actual HTTP status ≥400, or unparseable HTTP 200, only after the existing specific classifications/promotions. Pre-send/no-status cases are not promoted. Quoted/no-quote variants follow available facts. |
| Both media checks | Both are now named in FR-092/T-52, including strip-retry-then-residual. **Source precision:** `outcomeFallbackEligible` consumes recovery classification; `outcomeRelabelApplies` consumes the current presentation code after `TranslateTurnError` at the live error path and accepts `unknown`, empty, or `provider_request_rejected`. They do not both directly read the same classifier output as FR-092 currently says. Preserve their behaviour and make promotion ordering explicit; a new `provider_error` must not reach the old relabel predicate early and suppress the media label. Verify live and persisted outcomes, not just unchanged function bodies. |
| Incident eligibility remains independent of display code | Preserved. Terminal promotion must not manufacture a provider-wide incident solely because the displayed code became `provider_error`. |
| Own limiter | Contract shape remains fixed; inclusion remains the founder's OQ-1857-5 decision. |
| Reserved delegated-task code | Preserve the no-emitter ruling; MIN-002 removes only the duplicate documentation. |
| Error-data trust | Preserve **Provider-Capability-Aware Media Handling and User-Facing Error Translation**, especially its fail-closed translation boundary, and the approved notifications assembly/masking decisions. Do not trust arbitrary error text to repair reachability. |

The source for the media distinction is `/workspace/wt/rel-head-1857/pkg/agent/media_downgrade.go::outcomeFallbackEligible`, `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn_response.go::handleProviderResponse`, and `/workspace/wt/rel-head-1857/pkg/agent/turn_transcript.go::outcomeRelabelApplies` / `::persistErrorTranscript`. This is a source-level integration check, **not** proof that future media recovery passes.

## Structural integrity

“Present” below assesses the specification structure, not implementation success.

| Check | Result | Amendment-specific assessment |
|---|---|---|
| Status and scope explicit | Present | Approved base and in-review amendment are distinguishable; D85 remains pending. |
| Architecture grounding | Present | The named media/error-translation ADR and accepted R-2–R-6 rulings supply the boundaries. Production code was checked at the pinned release. |
| Contract-first ordering | Present, correction required | Enum/catalogue/attribution → slot vocabulary → regeneration → use is stated. MAJ-005/007 prevent a stable final contract matrix yet. Backend-lead remains its owner. |
| API and persisted data | Present, incomplete surface map | Approved §11.4 covers trusted assembly and replay. The amendment needs the actual parent and save paths, not a new parallel wire shape. |
| Loading/error/partial/completed states | Incomplete | Save stages and nonfatal CLI diagnostics are conflated (MAJ-001/008). |
| End-to-end journeys | Incomplete | Parent **Open** journey and actual Settings path need explicit coverage (MAJ-003/006). |
| Accessibility and keyboard | Inherited, not exercised | §16A specifies keyboard actions/disclosures, one-time announcements, no history announcement, reduced motion and wrapping. New error variants must use those same carriers and be sampled in the new CLI UAT rows. |
| Design-system reuse and brand | Inherited | No new visual primitive is needed by this amendment. Existing system lines and inline Settings results should retain catalogued controls and disclosures. No new palette/layout decision is proposed. |
| Security and user promises | Stated, path-level proof incomplete | FR-093 prohibits stderr at every client detail level; MAJ-006 requires producer-to-wire evidence. This review does not approve the alternative exposure options. |
| BDD acceptance oracles | Incomplete | Missing B-row dispositions and the generic-positive guard contradiction (MAJ-004/005). |
| Requirement → scenario → test | Links present, proof incomplete | The aggregate §23 row exists, but does not establish per-cause or per-surface coverage. |
| Reachability | Routes exist; fix not demonstrated | Existing chat, Settings, task and parent routes were found. Testing the classifier alone does not prove they use it. |
| User-facing documentation TODO | Present | §25 adds #1857 work to DOC-02 and DOC-08 and retains DOC-12's masking boundary. Extend the TODO detail to the corrected Settings and parent-Open journeys; do not report documentation shipped at spec stage. |
| Agent-tool field parity | Not applicable to this amendment | It changes failure reporting, not a property agents/users can create or update. §17 explicitly states no new agent-managed property; no new tool/policy registration is required. |
| Riders | **WARNING — UNVERIFIED** | §1.5 still records chief confirmation as Unknown. Team-lead must obtain the current same-file rider confirmation before final design readiness. This inherited evidence gap is not an amendment blocker by itself. |

### Twelve-lens accounting

| Lens | Result |
|---|---|
| 1. Ambiguity | MAJ-005: missing per-kind decisions. |
| 2. Incompleteness | MAJ-001/005/008: save stages, disposition matrix and nonfatal state. |
| 3. Inconsistency / ADR / source | MAJ-002/007; MIN-001/002/003; media dataflow precision above. |
| 4. Infeasibility | No new deployment dependency or platform mechanism is required by this amendment. Deterministic fake-process/provider boundary tests fit the existing architecture; they have not been run. |
| 5. Contract-first | Ordering is specified; unresolved code/attribution choices and actual consumer routing remain MAJ-005/007. |
| 6. Security | Closed display-name map, fixed copy and no-stderr rule are retained; real serialization coverage is missing (MAJ-006). |
| 7. Reachability | MAJ-003/006: classifier, parent status, parent-agent completion and Settings are different boundaries. |
| 8. UI states / journeys | MAJ-001/002/003/008; partial output must not be described as unsent or completed. |
| 9. Accessibility / keyboard | Applied to the new text on inherited §16A carriers; no new controls specified. Runtime keyboard, focus and announcements remain unverified. |
| 10. Design-system / brand / voice | Catalogue/control sources checked. W9/W14/W15 make unsupported claims or omit recovery; no “contact support” phrase occurs in the proposed W forms. A lexical guard alone cannot prove truthful copy. |
| 11. Testability / false greens | MAJ-004/005/006; existing allowed-emitter mutation and real-adapter tests are required. |
| 12. Overcomplexity | Keep one CLI classifier, generated catalogue and existing assembly path. Do not add a second frontend parser, arbitrary-text trust path, new service, or stderr viewer to solve this amendment. |

## Test coverage assessment

No tests were authored or executed in this review. These are changes needed in the plan before RED.

| Scenario / test | Assessment | Required proof |
|---|---|---|
| S-133 / T-52 | Selected CLI code oracles are concrete; starting at classification skips adapters | Raw driver envelopes and process exit → actual dispatch/Settings → agreed code, sentence, attribution and action. |
| S-134 / T-53 | Sign-in wording is specified, but the current test-level label underspecifies the path | Structured sign-in result plus separate stderr; actual line/details/replay; `retryable: false` from the agreed contract. |
| S-135 / T-52 | Useful genuine-unknown control | Add dispatched/no-status and pre-send controls separately; neither becomes an API error merely because a call was attempted. |
| S-136 / T-53 | Clear no-stderr rule, insufficient boundary coverage | Inspect actual serialized live frames, Settings responses, parent completion/tool results and stored/replayed error records at all levels. |
| S-137 / T-53 | Lexical/copy-rule checks are feasible, not sufficient for voice truthfulness | Both voice forms, all resolved variants/no-fact forms, accepted attributions, and manual observed-cause/action checks. W15 is not complete merely because banned words are absent. |
| S-138 / T-54 | Wrong positive oracle for known causes | Split untrusted-text rejection from payload correctness and positive known-cause production tests (MAJ-004). |
| A4/A5 media regression | Both gates and a useful residual sequence are named | Real strip retry → subsequent residual → final live and persisted label; control with a distinct later typed failure. Assert no change to retry decisions or incident eligibility. |
| Newly covered B rows / C8 | No complete independent oracle | Resolve the amendment matrix before writing expected values (MAJ-005). |
| §24 hands-on rows | No explicit reproduction of the reported CLI journeys | Add duplicate-argument and nested-model-unavailable cases, Settings and helper chat, parent **Open**, reload, and detail levels on the exact cut commit. |

### Dataset gaps

| Dataset | Missing boundary | Add before RED |
|---|---|---|
| CLI classification | Structured error metadata lost before classification | Nested OpenCode `error.type=provider.no-route`, top-level error control, absent fields, and a deliberately unrecognised type; do not take expected values from the implementation. |
| CLI lifecycle | Recoverable parse diagnostic mistaken for terminal failure | `Fatal:false` malformed line followed by valid output and success; separate fatal sequence and typed cancel/timeout controls. |
| CLI stream read | Old classifier can return typed transport copy | A cause containing `unexpected EOF` versus an unrecognised scanner failure; both must take the agreed new CLI route without modifying provider classification. |
| Save failures | One error type spans different execution stages | User-message admission, running tool receipt, intermediate assistant continuation, final assistant save and gateway late save. |
| Resume | Same remedy, different observed cause | Missing retained conversation with unchanged workspace versus the workspace-change sentinel. |
| Security boundary | Sanitizer output tested without its consumers | Harmless synthetic path, command-line, flag-value and credential-shaped markers in stderr and stdout error messages; assert absence from serialized/persisted error surfaces, including masked-tail fallback. |
| Contract/variant parity | One code can have multiple cause sentences | All `external_cli_failed` cause variants, no-fact fallbacks, both voices, closed CLI display names and per-code attribution. |

## STRIDE threat summary

STRIDE checks identity spoofing (S), tampering (T), repudiation/auditability (R), information disclosure (I), denial of service (D) and privilege escalation (E). **Covered** means a rule addresses it in the document, not that an implementation passed; **proof gap** means verification is incomplete; **—** means no new mechanism in this amendment.

| Boundary | S | T | R | I | D | E | Assessment |
|---|---|---|---|---|---|---|---|
| CLI event → safe classification | Covered | Covered | — | Covered / proof gap | — | — | Driver-keyed closed display names and fixed copy avoid using external text as trusted wording. Parser-to-consumer tests are missing (MAJ-006). |
| Assembled error → live/replay/Settings | — | Covered / proof gap | — | Covered / proof gap | — | — | Preserve the approved trusted-assembly boundary. T-54 must not confuse hiding bad payloads with preventing them. |
| Error → parent agent/task result | — | Covered / proof gap | — | Covered / proof gap | — | — | FR-093 prohibits stderr, including parent results; actual completion route needs coverage, not only `ForLLM`. |
| Server-side diagnostics | — | — | Proof gap | Out of scope for new exposure | — | — | Retention/routing of existing raw diagnostics is not a claim that they are safe for client delivery. Verify the actual log sink when implementing T-53; do not infer it from a helper's return value. |
| Existing authentication/tool policy | — | — | — | — | — | — | No new endpoint authority, tool policy or execution permission is proposed here. |

security-relevant side finding, reported to team-lead

## Reachability check

| Question | Answer | Evidence |
|---|---|---|
| New agent-facing tool or editable object field? | No, not in this amendment | §17; FR-092/093 change error presentation. No field-parity expansion is required. |
| Can a real CLI failure reach chat? | Existing route verified; proposed output not implemented | `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::drainExternalRun` / `::emitExternalCLIErrorEvent`; `/workspace/wt/rel-head-1857/src/lib/llm-error.ts::getLLMErrorDisplay`. |
| Does Settings share the real event/classification route yet? | Not today | `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun` / `::sanitizeSmokeTestErrorMessage`; MAJ-006. |
| Is parent-agent failure delivery the human lifecycle sentence? | No | `/workspace/wt/rel-head-1857/pkg/agent/steer_completion.go::completionMessage` versus `/workspace/wt/rel-head-1857/src/lib/delegationEventLine.ts::delegationEventLineText`; MAJ-003. |
| Is there a supported way from parent status to the child? | An Open affordance is source-verified; journey not exercised | `/workspace/wt/rel-head-1857/src/lib/delegationEventLine.ts::delegationEventShowsOpen`. |
| Does the plan require execution rather than test authorship alone? | Yes in §19.2/§24; added CLI rows are missing | MAJ-006. This report supplies no execution evidence. |

**Code correct and tested:** Not assessed — specification-only review; no build, test or implementation verdict.

**Reachable by a user or agent:** Existing routes are source-verified; the amendment's corrected output is not implemented or demonstrated.

## Unasked questions for the author

These are technical corrections for the author/architect, not new founder decisions.

1. What exact code, attribution and catalogue/manifest variant closes each newly promised B-row and C8, and which independent test asserts it?
2. At which point is residual provider promotion applied relative to live and persisted `outcomeRelabelApplies`, so both recovery checks retain their actual behaviour?
3. Which real producer carries each classified CLI cause to Settings, parent completion, task result and replay, including failures before a run starts and diagnostics after partial output?

## Questions for the founder

Team-lead should conduct the interview before the fix round. The accepted R-2/R-3/R-4 contract and classification rulings, R-5's shape and R-6's reserved-code disposition are **not** asked again.

### Q1 — CLI wording on the main line (OQ-1857-1)

CLI error output can contain paths, arguments and credential material. The proposed fixed sentences solve #1857 without transferring that output to the client.

- **A:** Permit masked/extracted CLI stderr quotations on the main line; this requires an explicitly revised exposure design and security review.
- **B — recommended:** Fixed catalogue sentences using approved safe facts only; no stderr quotation.

### Q2 — Own request limiter in this work package (OQ-1857-5)

The current provider-overloaded wording misidentifies an agent's own request limit. Its separate code and config attribution are already decided; only inclusion is open.

- **A — recommended:** Include the limiter correction in this amendment, with its real Settings destination and test oracle completed.
- **B:** Defer through the named WP-6 disposition and tracked scope, without claiming it fixed by #1857.

### Q3 — CLI stderr in Verbose/Debug (OQ-1857-7)

Showing a masked tail in technical details creates a client data surface that the stated status quo does not have. A detail-level switch is not a confidentiality boundary.

- **A — recommended:** Keep stderr off the wire at every level; expose only approved structured facts such as CLI display name, exit status and classified kind.
- **B:** Permit a masked tail only after security-lead reviews the new exposure and the specification is explicitly revised.

Q1 and Q3 must be answered consistently. Neither question authorizes raw text by default.

### Q4 — Wording pattern for W1–W15

The factual defects in this report must be corrected whichever pattern is selected. The open product choice is whether the proposed short, cause-specific pattern and its per-surface voices are the desired wording direction.

- **A — recommended:** Keep that pattern: observed fact only, specific safe cause where known, one supported recovery path, chat and neutral forms; review the corrected examples against it rather than approving every original sentence.
- **B:** Request a different wording pattern and identify the example that should guide it.

## Verdict rationale and next action

**Verdict: REVISE.** No amendment-specific critical security hole was established, but MAJ-001/002/008 permit false statements about what happened, MAJ-003 leaves the reported parent journey incorrectly accounted for, and MAJ-004/006 allow an apparently successful test plan to miss the reported failure. MAJ-005/007 must be resolved before contract fixtures can serve as an independent oracle.

Priority actions:

1. Repair the positive acceptance oracle and real-path coverage (MAJ-004/005/006).
2. Correct state, cause and surface mapping (MAJ-001/002/003/008).
3. Restore the accepted attribution and reconcile the references/current-state inventory (MAJ-007; MIN-001–003), retaining both media checks and all fail-closed constraints.

Review written to: `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec-1857-review.md`

This is grill round 1 of 2 (fixed). Next: team-lead interviews the founder on “Questions for the founder”, then the spec author fixes round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the corrected spec at `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md` — regardless of this round's verdict. The approved remainder is not reopened.

Skills: `grill-spec`, `omnipus-shared-rules`, `omnipus-voice`, `ux-heuristics-review`.

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Review is pinned to the requested amendment and production revisions | `git rev-parse HEAD` and `git -C /workspace/wt/rel-head-1857 rev-parse HEAD`, exit 0: `0a511d4e94363f903ae88eefee578ed77c52c204` and `5d20e539fa63624189d82f71c45418512b219222`. | Verified |
| Reviewed diff is the amendment, not a reopened base review | `git diff --stat 5f9d0a2ecf..0a511d4e94 -- /workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md`, exit 0: one file, 203 insertions and 2 deletions; exact diff read. | Verified |
| Reported OpenCode cases were checked against the issue comment | `gh issue view 1857 --repo elicify-ai/omnipus --comments`, exit 0; duplicate model argument and nested `provider.no-route` cases in saved output `/workspace/.state/claude/projects/-workspace-wt-notif-1857/8bf60fdc-be36-4e35-a87f-a6b10400f108/tool-results/b2ehibbgv.txt`. | Verified; later issue state Unknown |
| Save-stage claims are not established by the shared helper | `/workspace/wt/rel-head-1857/pkg/agent/started_tool_outcome.go::beginStartedTool`; `/workspace/wt/rel-head-1857/pkg/agent/turn_exit.go::reportSessionSaveFailure`; `/workspace/wt/rel-head-1857/pkg/gateway/websocket_streamer.go::publishSaveFailureNotice`; §13.8.3 W9/W15. | Verified source mismatch; runtime consequence Inferred |
| Resume refusal and workspace change are distinct | `/workspace/wt/rel-head-1857/pkg/agent/external_run_session.go::errExternalResumeUnavailable` / `::errExternalWorkspaceChanged`; W14. | Verified |
| Parent summary, tool result and completion are separate | `/workspace/wt/rel-head-1857/src/lib/delegationEventLine.ts::delegationEventLineText`; `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::drainExternalRun`; `/workspace/wt/rel-head-1857/pkg/agent/steer_completion.go::completionDisposition` / `::completionMessage`. | Verified |
| The unknown guard's positive expectation contradicts the known-reason goal | `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md::FR-092`, `::S-138`, `::T-54`; `/workspace/wt/rel-head-1857/src/lib/llm-error.ts::getLLMErrorDisplay`. | Verified specification mismatch; false green Inferred |
| New B-row/C8 oracles and real-adapter test coverage are incomplete | `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md::§13.8.2`, `::S-133–S-138`, `::T-52–T-54`, `::§24`; `/workspace/wt/rel-head-1857/pkg/agent/runner/driver_opencode.go::parseLine`; `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::drainSmokeTestRun`. | Verified |
| Accepted upstream-refusal attribution is ambiguous, not user | `/workspace/logs/1857/architect-read.md::Rulings — R-2`; compared with `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md::C9` and `::OQ-1857-2`. | Verified |
| Nonfatal diagnostics reach the sanitizer and error emitter | `/workspace/wt/rel-head-1857/pkg/agent/runner/driver_opencode.go::parseLine`; `/workspace/wt/rel-head-1857/pkg/agent/external_dispatch.go::drainExternalRun`. | Verified; future W4 misstatement Inferred |
| W13 Settings reference and duplicated reserved row are incorrect | `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md::§13.8.4`, `::W2`, `::W13`, `::§13.7`. | Verified |
| Current CLI/Settings outcomes are conditional, not uniformly unknown | `/workspace/wt/rel-head-1857/pkg/agent/runner/driver_stream.go::streamParser`; `/workspace/wt/rel-head-1857/pkg/providers/error_classifier.go::IsTransportFailure`; `/workspace/wt/rel-head-1857/pkg/gateway/rest_executor_smoketest.go::sanitizeSmokeTestErrorMessage`. | Verified branches; occurrence frequency Unknown |
| Both media checks need preservation at different stages | `/workspace/wt/rel-head-1857/pkg/agent/media_downgrade.go::outcomeFallbackEligible`; `/workspace/wt/rel-head-1857/pkg/agent/loop_run_turn_response.go::handleProviderResponse`; `/workspace/wt/rel-head-1857/pkg/agent/turn_transcript.go::outcomeRelabelApplies` / `::persistErrorTranscript`. | Verified data flow; future preservation not tested |
| Fail-closed and contract-first are the governing boundaries | `/workspace/wt/rel-head-1857/docs/internal/architecture/ADR-051-media-handling-and-provider-error-translation.md::RD5`; `/workspace/wt/rel-head-1857/contracts/components/schemas/LLMError.yaml::x-user-messages`; specification §11.4, §15, FR-093. | Verified requirements, not a security PASS |
| Frontend and user-document rules were considered | Specification §16A/§25; `/workspace/wt/rel-head-1857/design-system/catalog.json::entries`; `/workspace/wt/rel-head-1857/src/components/ui/CLAUDE.md::No hand-built controls`; `/workspace/wt/rel-head-1857/docs/internal/brand/brand-guidelines.md::Brand Voice & Tone`; `/workspace/wt/rel-head-1857/docs/security.md::How to control agent access`; `/workspace/wt/rel-head-1857/docs/tools.md::What it is`; `/workspace/wt/rel-head-1857/docs/providers-and-models.md::Limits and things to watch`. | Verified source review; UI operation unverified |
| Search instruments were checked rather than trusting absence | Known controls returned `turnErrorUserText` and `userVisibleTurnError` in the same symbol search used to check the claimed `ForLLM` producer. Catalogue search returned `Button`, `DisclosureRow`, `IconButton`, `Tooltip`; exit 0. Review guidance read at `/workspace/wt/rel-head-1857/docs/internal/false-green-patterns.md::A substring scan is not a behavioural test`. | Verified search capability; not behavioural proof |
| Chief rider confirmation is not evidenced here | `/workspace/wt/notif-1857/docs/internal/specs/notifications-spec.md::§1.5` explicitly records confirmation as Unknown. | Unknown; warning only |
| Only the requested report was added; source worktrees stayed unchanged | After report creation, `git status --short`, exit 0: only the untracked review report; `git diff --quiet HEAD`, exit 0: no tracked changes; `git -C /workspace/wt/rel-head-1857 status --short`, exit 0: empty. No tests, builds, commits or pushes performed. | Verified |
| Report formatting check detected and cleared Markdown hard-break whitespace | `git diff --no-index --check /dev/null /workspace/wt/notif-1857/docs/internal/specs/notifications-spec-1857-review.md`: first exit 3 with seven trailing-whitespace diagnostics; after replacing those breaks, exit 1 with no diagnostics (the report is a new-file diff, not an empty diff). This is a formatting check, not a product test. | Verified |
| **Self-check** | Re-read the scoped amendment, accepted rulings and decisive producer/consumer excerpts; checked the final report draft for 8 MAJOR + 3 MINOR findings, evidence/certainty per finding, twelve lenses, separate founder questions, fixed round-2 next step, private-side-finding restraint and one-file write scope. Re-ran source-scope and report-formatting checks above; the specification and release tree remain untouched. Source/test inspection is not presented as a runtime green. | **Verified** |
