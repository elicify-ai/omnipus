# Adversarial Review: Settings redesign (page G)

**Document reviewed:** `/workspace/wt/settings-1519/docs/internal/specs/settings-redesign-spec.md`  
**Mode:** SPEC  
**Round:** Round 2 of 2 (final)  
**Review date:** 2026-11-20  
**Reviewed branch:** `landing/settings-1519`  
**Reviewed commit:** `5afdc08883983cbaf60ea5349a7ce606f21d3e05`  
**Release baseline:** `c2f8f8e735e8241b1e1fab7fadf738ecda011eae`  
**Round-1 report:** `/workspace/wt/settings-1519/docs/internal/specs/settings-redesign-spec-review.md`  
**Verdict:** **REVISE**

## Executive Summary

There are **6 MAJOR findings and 1 MINOR finding**. CRIT-002's original leaf-only guard has been replaced with an effective-value guard, but the scenario, D-12 and G-26 still contain invalid expectations: the requested proof is **not complete**, and a red result from those cases would not necessarily demonstrate the retention defect. Frontend and backend received equal scrutiny: persisted-versus-running state, response contracts, changed-origin restart, partial-read recovery, field validation, navigation, accessibility and component reuse were checked alongside the retention mutation path.

| Severity | Count |
|---|---:|
| CRITICAL | 0 |
| MAJOR | 6 |
| MINOR | 1 |
| OBSERVATION | 0 |
| **Total** | **7** |

**Review boundary.** This round checks the commissioned round-1 Settings findings and defects introduced or left contradictory by their corrections. The signed-in browsers draft at `/workspace/wt/settings-1519/docs/internal/specs/signed-in-browsers-spec.md` was **not reviewed**. Its removal from Settings was checked; the fix log's claims that findings are fixed in that other document are not endorsed here.

**Evidence qualification.** Verified means a specification contradiction or source mechanism was checked directly. It does not mean the proposed feature ran. No builds, product tests, browser campaign, deletion experiment, commit or push were performed. GitNexus tools were unavailable; code exploration used direct reads and `rg`. The only authorized output is this report. Throughout this report, an unqualified section, FR, dataset or scenario citation refers to the reviewed specification at the absolute path above.

## Round-1 Disposition

“Addressed” means addressed **in the specification**, not implemented, tested or accepted for landing. Fixed findings are not re-raised as defects.

| Round-1 ID | Disposition | Evidence / remaining issue |
|---|---|---|
| CRIT-002 | Original mechanism addressed; proof incomplete | C-3 and FR-044 now compare effective values before persistence. The acceptance matrix still fails scrutiny: R2-MAJ-001. |
| MAJ-001 | Addressed | C-6, FR-015/019/022 explicitly exempt Third-party agents from partial-write and per-field concurrency promises; whole-section last-save-wins is documented. Its failed-save reread is a separate issue under R2-MAJ-002. |
| MAJ-002 | Addressed | C-6 and FR-021 require requested-field merging against stored values inside the lock; orders 11–12 name deterministic, both-order concurrency tests and writer checks. |
| MAJ-003 | Addressed | C-3 separates the complete, unbounded read shape from the bounded partial-write shape; FR-041/046 and order 3 exercise the generated validator and repair screen. |
| MAJ-004 | Addressed | C-3, FR-042/045 and order 4 require unconditional handler enforcement, validation on/off, and rejection of mixed obsolete-memory writes without side effects. |
| MAJ-005 | Not fully addressed | FR-020 adds four outcomes but reconciles against live rather than durable state and contradicts the existing 500 acknowledgement: R2-MAJ-002. |
| MAJ-006 | Original banner/focus gap addressed | C-2 addition and FR-035/062 specify failed-hot-apply entries, survival across page reloads, a focus target and clearing. The new #995 response contract is incomplete: R2-MAJ-006. |
| MAJ-007 | Not fully addressed | C-2/FR-063 replace the first-health-response oracle, but the new changed-port journey conflicts with browser origin restrictions: R2-MAJ-003. |
| MAJ-008 | Addressed | FR-014 and the two exit outlines separate same-document navigation from document unload; G-06 distinguishes automated in-app checks from browser-dependent manual evidence. |
| MAJ-009 | Not fully addressed | Read dependencies and FR-066 are corrected, but the old whole-section failure scenario remains contradictory: R2-MAJ-004. |
| MAJ-010 | Dependency/order defect addressed | FR-060 restores Skills & Tools as a landing prerequisite and prohibits a transitional layout. One changed-link oracle was not updated: R2-MIN-001. |
| MAJ-012 | Partly addressed; replacement fixture defective | The false 512 refusal is removed and SC-003 distinguishes modes/consent attempts. D-10 row 11 and G-07 introduce another false UI assumption: R2-MAJ-005. |
| MAJ-013 | Addressed for commissioned Settings scope | C-7, FR-100/101, D-16 and the Riders table state restrictions, exceptions, ownership/dispositions and tests. The current brief independently confirms the rider list. #1265 remains open, wording only; #1146/#598 moved out. |
| MAJ-014 | Addressed | Real retrospective and session sweep ticks, completion and switch-off deletion controls replace the ineffective test. G-14 explicitly does not claim hands-on deletion proof. |
| MIN-001 | Addressed | FR-018 gives an eight-second success state, edit/section transitions and one announcement. |
| MIN-003 | Addressed | Test Hierarchy names meaningful typecheck, design-system locks, contract drift and touched-package lint, separate from behavior evidence. |

CRIT-001, MAJ-011, MAJ-015–018 and MIN-002 moved with #1096. Their disposition in that draft is outside this review.

## Findings

### CRITICAL Findings

None newly verified in the corrected Settings design. This does **not** close CRIT-002's required proof: the proposed by-effect rule addresses the original data-loss mechanism, while the defective acceptance proof is R2-MAJ-001 below.

### MAJOR Findings

#### R2-MAJ-001 — The retention proof mixes blocked requests, ineffective mutations and incompatible expectations

- **Severity:** MAJOR.
- **Lens:** Testability & false-green risk; inconsistency & AS-IS contradiction; security-boundary preservation.
- **Affected section:** C-3; FR-044/048; “Keep forever survives any replace of its parent block”; D-12; G-26; SC-005; related D-16 row 9.
- **Failure scenario:** A test author translates D-12 literally. Whole-config cases fail at an existing credential/protected-field fence rather than the retention guard; accepted sibling/port writes fail the outline's byte-identical assertion. The team can mistake these unrelated red results for proof of CRIT-002, or relax an existing security fence to make the promised whole-config 200 pass.
- **Evidence:**
  1. D-12 row 5/G-26(c) send the whole `GET /config` object with retention changed; row 9/G-26(d), and D-16 row 9, demand 200 for that whole object unchanged. `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::getConfig` serializes `config.Config`, whose `Providers` field is not omitted, and does not strip `providers`. `updateConfig` rejects that top-level field with **403 before entering the mutation callback**. `/workspace/wt/settings-1519/pkg/gateway/blocked_paths.go::blockedPaths` additionally refuses protected sections. These are not retention-guard exercises, and “rows 1–6 store the change on today's code” is false for row 5.
  2. D-12 row 6 adds uppercase `Storage` alongside the fixture's lowercase `storage`. `updateConfig` merges by the literal map key; `/workspace/wt/settings-1519/pkg/gateway/rest_config_max_tool_iterations.go::protectedAgentDefaultsFingerprint` documents the sorted serialization and folded-key ordering that C-3 proposes to reuse. In the proposed schema, the later canonical subtree supplies its explicit `memory_retros_forever: true`; this fixture does not demonstrate an effective-value change. Requiring 400 solely because the request spells a field differently contradicts the by-effect rule. This outcome is a source-derived deduction, not an executed probe.
  3. The outline applies to **all D-12 rows** and unconditionally requires byte-identical configuration, although rows 10–11 are accepted mutations; row 11 changes Port to 8081. D-12's introduction correctly limits byte equality to refusals, but its consuming scenario does not. G-26 also applies its checksum statement without distinguishing accepted writes from refusals.
  4. The new `memory_retros_forever` member is absent from `/workspace/wt/settings-1519/pkg/config/config.go::OmnipusRetentionConfig` at the baseline. A pre-change test referencing that new Go field cannot provide behavioral RED evidence; a raw unknown JSON member alone is not proof of the old sweep's keep-forever behavior.
- **Recommendation:** Split refusal and accepted-mutation outlines. Preserve all existing generic-config security restrictions; use a precisely enumerated payload of permitted fields, not an unfiltered GET echo, to test a larger retention mutation. Keep raw whole-GET echo as an existing-security refusal control with its correct status. Make the casing case actually change the decoded effective value, and separately test harmless alternate spelling. On refusals assert status/message, unchanged saved bytes and unchanged fresh-load effective values; on accepted writes assert the intended sibling change and preserved retention, not universal byte equality. Correct SC-005's baseline assertion. Require a saved narrow RED receipt later using an existing-field fixture, such as `disabled: true` with `{"storage":null}`; add the new memory-forever matrix after its representation exists, and mutation-test the guard. Do not weaken production validation to satisfy a fixture.
- **Certainty:** **Verified, high confidence** for blocked-GET and contradictory-oracle defects; **Inferred, high confidence** for the unexecuted mixed-case outcome. The finding stands independently of that deduction.

**CRIT-002 proof acceptance checklist for the correction:**

| Proof obligation | Required disposition |
|---|---|
| A real pre-change failure | Use a valid existing configuration with `disabled: true` and nondefault day counts. An authenticated `{"storage":null}` request passes the current leaf/protected-agent guards and can durably replace the parent; assert refusal and preservation against that mechanism, not compilation against a future field. |
| Both forever flags | Use separate memory-forever and legacy-disabled fixtures; do not let one true flag conceal loss of the other. Check all four effective values after fresh loading. |
| Every replacement family | Explicitly cover retention object replacement, null/scalar/array ancestors, permitted multi-section writes, casing that changes effective state, mixed benign changes and harmless/dotted controls. Pin decode-failure handling before persistence. |
| Existing security fences | An unrelated 403 is evidence only for its existing fence, not the new retention guard. No full-config 200 requirement may authorize dropping that fence. |
| Instrument strength | Removing the by-effect guard must fail the focused preservation assertion. Accepted sibling/port controls must still persist their intended changes. |
| Honest evidence | The spec names a future saved RED log; this review supplies source analysis only. Neither a current behavioral failure nor a passing implementation has been run here. |

The baseline path was checked through `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::updateConfig`, `writeConfigJSONLocked`, `/workspace/wt/settings-1519/pkg/gateway/pairing_config_write.go::orderedConfigJSONLocked` and `prepareConfigJSONLocked`, `/workspace/wt/settings-1519/pkg/gateway/rest_channels_outbound_guard.go::guardedConfigMutate`, and `/workspace/wt/settings-1519/pkg/config/migration.go::loadConfig`. The channel guard does not protect retention; fresh loading and `/workspace/wt/settings-1519/pkg/config/config_retention.go::IsDisabled` make loss of the legacy boolean observable. This is why the null-parent fixture targets the actual defect, unlike the full-GET echo.

#### R2-MAJ-002 — Read-back still treats running settings as proof of what was saved

- **Severity:** MAJOR.
- **Lens:** Incompleteness; UI states & journey gaps; ambiguity; persistence ownership.
- **Affected section:** C-6; FR-012/020/022; “An unknown result is checked by reading back”; “A write that was stored but not applied says so”.
- **Failure scenario:** Saving Log level reaches disk, live configuration refresh fails, and the response is lost. The mandated GET returns the old live value; FR-020 therefore declares the change **not saved**. Discard can show the old value even though restart applies the newer durable value. In Third-party agents, the mandatory post-Save reread can likewise replace a known saved-but-unapplied value with stale data.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::applyWrittenConfigLocked` returns `configRefreshError` after durable persistence; `getConfig` reads `agentLoop.GetConfig()`. `/workspace/wt/settings-1519/pkg/gateway/rest_context_settings.go::getContextSettings` and `/workspace/wt/settings-1519/pkg/gateway/rest_third_party_settings.go::thirdPartySettingsView` also read live configuration. FR-020 explicitly turns a reread equal to the old value into “not saved”; C-6 proposes no authoritative saved-state read. FR-020 also places `saved_not_refreshed` under **2xx**, while `putThirdPartyAgentSettings` and `/workspace/wt/settings-1519/contracts/components/schemas/ErrorResponse.yaml::code` use **500** for that known saved result; the same FR classifies 5xx as unknown.
- **Recommendation:** Specify authoritative persisted-state reconciliation independently from running/applied state. Recognized saved-but-unapplied codes must take precedence over generic HTTP-status handling. A live GET equal to the old value must not prove non-persistence or overwrite a known saved acknowledgement. Add a combined **commit → failed refresh → lost response → reread** case, a Third-party agents saved-error/reread case, and a defined outcome when concurrent editing makes the reread equal to neither the previous value nor the draft. Keep Discard limited to actual drafts.
- **Certainty:** **Verified, high confidence**—source/state mismatch and status contradiction; no runtime failure was executed.

#### R2-MAJ-003 — Changed-port restart checks require a cross-origin read the shipped page cannot make

- **Severity:** MAJOR.
- **Lens:** Infeasibility; UI states & journey gaps; security compatibility; testability.
- **Affected section:** C-2 restart completion; FR-063; changed-port/proxy scenarios; G-10; DOC-003.
- **Failure scenario:** A person opens the production-served page at `http://127.0.0.1:8080`, changes Port to 8081 and restarts. The spec waits for authenticated `/about` at the new address **before** navigating there. The old page's connection policy blocks that fetch, so a successfully restarted gateway is reported as unavailable.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/embed.go::spaBaseContentSecurityPolicy` allows `connect-src 'self' stun: turn: turns:`; its comment explicitly keeps HTTP egress pinned to the gateway. A different port is a different origin. `/workspace/wt/settings-1519/pkg/gateway/rest.go::setCORSHeaders` grants credentialed access only to the configured origin, not its loopback fallback; the same file registers `/about` with authentication. `/workspace/wt/settings-1519/src/lib/api/http.ts::performRequest` sends credentials. FR-063 and “A changed port shows where the gateway will answer” specify no compatible handoff mechanism. FR-063's port-equality test also assumes equal ports prove a direct connection; a reverse proxy can expose the same numeric port as its backend.
- **Recommendation:** Define a destination-origin reconnect journey, for example an explicit top-level handoff followed by same-origin readiness/configuration checks, with sign-in handling and a usable recovery link/instruction. Do not broaden connection or cross-origin credential policies merely to make polling succeed. Do not treat port equality alone as proof that an internal listener address is the public destination. Include a production-served browser test with real response headers, changed-port/direct and stable-proxy cases; mocked fetch success cannot prove this journey.
- **Certainty:** **Verified, high confidence** about the specified policy conflict; no browser run performed.

#### R2-MAJ-004 — The old Limits failure scenario still forbids the saves the correction permits

- **Severity:** MAJOR.
- **Lens:** Inconsistency; UI states & journey gaps; testability.
- **Affected section:** “A section that cannot load”; “One failed read blocks only its group”; UI Screens and States/read dependencies; FR-066.
- **Failure scenario:** Only the Context read fails. One implementation disables every Limits save; another leaves Agents and goals usable. Each can cite an explicit acceptance scenario, so the correct partial-failure UI cannot satisfy the full test plan.
- **Evidence:** “A section that cannot load” makes only `GET /settings/context` return 500, then requires that **no value in Limits can be saved**. The corrected read-dependency table, Limits state row, FR-066 and the new group-only scenario require the independent Performance group to save. The new scenario also says Agents and goals “saved normally” while its Tries per goal draft “stayed”, without a separate unsaved edit or save step.
- **Recommendation:** Make the whole-section scenario fail both Limits reads, or constrain its assertions to the Context-dependent groups. Keep an independent partial-read scenario. Separate (a) Retry preserving an unsaved Performance draft and (b) explicitly saving that draft successfully; do not require one value to be both saved and still unsaved.
- **Certainty:** **Verified, high confidence**—directly contradictory specification expectations.

#### R2-MAJ-005 — The replacement partial-failure fixture requires weakening existing field validation

- **Severity:** MAJOR.
- **Lens:** Testability & false-green risk; inconsistency; error prevention.
- **Affected section:** D-10 row 11; “A real server refusal is shown under its field”; “A value inside its range can be saved”; G-07; FR-013.
- **Failure scenario:** The test author tries to obtain the promised partial failure by entering override context length 0. Existing UI validation prevents the request. To satisfy G-07, the implementer removes that validation; the test passes only because product behavior was weakened to manufacture its setup.
- **Evidence:** `/workspace/wt/settings-1519/src/components/settings/ContextSection.tsx::validate` rejects override context lengths below 1. `/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/app.js::overrideRow` declares `min="1"`, and `validate` enforces that minimum. The new scenario and D-10 row 11 instead assert that the screen checks only whole-number syntax. `/workspace/wt/settings-1519/pkg/gateway/rest_context_settings.go::validateContextSettingsUpdate` confirms that the server refuses 0, but that does not make 0 reachable through the approved UI. D-10 also classifies the row as “Accepted by the screen”, while its Accepted-row outline requires no field error and successful storage.
- **Recommendation:** Preserve the minimum and the approved invalid-Save focus behavior. Exercise partial outcomes with valid screen input and the already-declared response-failure seam; test the real handler's zero refusal separately, or identify a genuine server-only rejection reachable without weakening validation. Give UI-accepted, UI-rejected and server-refused datasets distinct consumers; do not feed row 11 to the successful-storage outline. Correct G-07's “no seam needed” claim.
- **Certainty:** **Verified, high confidence**—both current code and the approved prototype contradict the replacement fixture.

#### R2-MAJ-006 — The new #995 behavior lacks a concrete response contract

- **Severity:** MAJOR.
- **Lens:** Contract-first gaps; ambiguity; frontend/backend integration.
- **Affected section:** C-2 addition; C-6; API and Data; FR-062/102; “Settings saves report a failed rebuild”.
- **Failure scenario:** Backend reports a committed-but-failed refresh with a 500 error code while frontend looks for `requires_restart` on a 2xx response; alternatively, backend adds warning fields to a closed Context success shape. Both sides can claim to implement “report saved-not-applied”, yet the warning is lost or the response violates the generated contract.
- **Evidence:** C-2 expands Config and Context writers to “report and record it the same way”; C-6 explicitly says **“No contract change”** and relies on existing signals. `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::updateConfig` returns a generic server failure for refresh errors; `/workspace/wt/settings-1519/pkg/gateway/rest_context_settings.go::putContextSettings` returns plain errors for write/refresh and later reload failures. `/workspace/wt/settings-1519/contracts/components/schemas/ContextSettings.yaml` is a closed settings-only response, referenced by the Context PUT in `/workspace/wt/settings-1519/contracts/openapi.yaml::/settings/context`. `/workspace/wt/settings-1519/contracts/components/schemas/ErrorResponse.yaml::code` describes `saved_not_refreshed` specifically for Third-party agents. The new writers have no defined status/code/envelope mapping in the spec.
- **Recommendation:** Add an endpoint outcome table specifying exact status, generated envelope and machine-readable code for pre-commit refusal/failure, durable-write/refresh failure, subsequent reload failure and success. State how affected fields and pending-apply entries are identified. Reuse `ErrorResponse` if appropriate—a new success schema is not inherently required—but explicitly document the added endpoint semantics and responses before implementation, then require generated-client integration coverage. Align FR-020 with that table. This contract correction is distinct from R2-MAJ-002's authoritative reread requirement.
- **Certainty:** **Verified, high confidence**—missing inter-layer agreement for newly commissioned behavior.

### MINOR Findings

#### R2-MIN-001 — The Skills permissions link has two different destinations

- **Severity:** MINOR.
- **Lens:** Inconsistency; reachability.
- **Affected section:** FR-009 versus “Links from other screens keep working”; FR-060.
- **Failure scenario:** The link test routes a person seeking permissions to Protection, where the grid has been removed; the person must follow another moved-items link. An implementation routing directly to Skills & Tools instead fails that test.
- **Evidence:** FR-009 sends the Skills permissions link to **Skills & Tools**. The scenario's Skills-screen row still sends it to **Protection**. FR-060 removes the grid from Settings and requires the destination page first.
- **Recommendation:** Update the live-link scenario to Skills & Tools. Keep the separate historical `tab=security` bookmark mapping to Protection in D-3; those are different cases.
- **Certainty:** **Verified, high confidence**.

### Observations

None added as findings. Fixed round-1 findings and unreviewed browser work are not counted again.

## Structural Integrity

### Spec-mode checklist

| Check | Result | Notes |
|---|---|---|
| Valid `Status:` field | PASS | `In review`; no premature implementation claim. |
| ADR link or explicit reason for none | PASS structurally | Reason given; accepted **“Re-auth Consent Gate: Credential-Vault Scope Expansion to v0.1.0”** is cited for the proposed deletion change. FQ-1/FQ-2 remain open. |
| Contract changes before implementation | FAIL completeness | C-2/C-3 are explicit; the added #995 acknowledgement is not (R2-MAJ-006). |
| API and data section | FAIL completeness | Present, but authoritative reconciliation is missing (R2-MAJ-002). |
| Screens: loading, empty, error, partial | FAIL consistency | Eleven sections and dependency groups are specified; Limits still has conflicting error oracles (R2-MAJ-004). |
| End-to-end user journey | FAIL feasibility | The changed-origin restart handoff remains unresolved (R2-MAJ-003). |
| Accessibility and keyboard | PASS for correction scope | Invalid Save remains focusable; dialog fallback, failed-apply focus, exit boundaries and announcements are explicit. No rendered accessibility result is claimed. |
| Design-system components, catalogue-first | PASS structurally | All 24 explicitly listed catalog exports found; existing product dialogs are reused separately. No missing-component finding. |
| Security and user promises | FAIL proof completeness | Retention by-effect rule is present, but its proof must preserve existing generic-config fences. Credential deletion remains a founder decision. |
| BDD scenarios with usable independent oracles | FAIL | R2-MAJ-001/004/005 and R2-MIN-001. |
| Requirement → scenario → test | PASS coverage count only | 81 active FR declarations and 81 traceability rows, no missing/extra IDs. Counts do not validate the scenarios. |
| Epic and riders | PASS for current commission | Epic stated; rider dispositions match the chief-approved list supplied with this round. No issue closure or complete fix claim is inferred. |
| Agent-tool parity/exception table | PASS structurally | C-7 plus retention exceptions and named `TestSettingsAgentWriteParity`; execution and inventory-derived coverage remain RED/CHECK obligations. |
| Reachability section | PASS structurally | Screen/route, retention route/middleware and prerequisite checks named; R2-MAJ-003 still affects one journey. |
| User-facing documentation TODOs | PASS structurally | DOC-001–008, DOC-010/011/013 name public pages. DOC-003 must follow corrected save/reconnect semantics; DOC-012 is a separate internal ADR correction. |

### Twelve-lens coverage

| Lens | Result in this fixed round |
|---|---|
| 1. Ambiguity | R2-MAJ-002/006: saved-state classification and endpoint outcomes. |
| 2. Incompleteness | R2-MAJ-002: durable reconciliation; R2-MAJ-006: newly added response behavior. |
| 3. Inconsistency / ADR / AS-IS | R2-MAJ-001/004/005, R2-MIN-001. Existing security fences and field validation were read, not assumed. |
| 4. Infeasibility | R2-MAJ-003: pre-navigation authenticated cross-origin polling. |
| 5. Contract-first | R2-MAJ-006; corrected retention read/write separation checked against generated-client validation requirements. |
| 6. Security / user promises | R2-MAJ-001/003: do not weaken protected-field or browser-origin fences to satisfy the plan. FQ-2 remains explicit. |
| 7. Reachability | R2-MIN-001; Skills & Tools prerequisite restored; signed-in-browser cut checked below. |
| 8. UI states / journeys | R2-MAJ-002/003/004; success-state and document-navigation corrections checked. |
| 9. Accessibility / keyboard | No additional independent defect verified: corrected focusable invalid Save, actual banner focus target, dialog fallback and announcement rules retained. |
| 10. Design-system reuse / brand | No additional independent defect verified: catalog choices, token/control ownership and corrected gate plan checked; no new kit is proposed. |
| 11. Testability / false-green risk | R2-MAJ-001/004/005; proof must exercise the real mutation, real sweep and real browser policies. |
| 12. Overcomplexity | No additional defect verified: the unapproved transitional layout is removed and single-use consent tokens remain. No new service or generic abstraction is requested. |

No finding is invented merely to fill a lens. UX emphasis was H1 (truthful saved/running status), H3 (Discard and reconnect recovery), H4 (consistent partial errors) and H5 (preserve input validation).

### Agent-tool field check

This is a review of the specified user-only exceptions, not authorization to add agent write access. No agent-managed object gains an editable property in this package. `set_config` remains a generic key/value tool; its supported key behavior and description must change together, and the named parity test must exercise actual tool refusals rather than a duplicate static table.

| Field | User UI / API in the spec | Agent create/update path | Reason / required proof |
|---|---|---|---|
| `session_days` | Data & retention / dedicated PUT | Refused through `set_config`; no create tool | Scheduled deletion policy; FR-101. |
| `memory_retros_days` | Same | Refused | Same. |
| `memory_retros_forever` | Same, new switch | Refused | Same; include the new field explicitly. |
| `keep_everything_forever` / stored `disabled` | Read-only; file edit only | Refused | Legacy deletion protection; FR-044/048. |
| `performance.max_parallel_agents` | Limits / password-gated Performance PUT | Refused | Agents must not raise their own run limits; C-7/D-16. |
| `planning.goal_max_rounds` | Same | Refused | Same. |
| `planning.goal_reminder_quiet_seconds` | Same | Refused | Same. |
| `planning.goal_stalled_checkin_enabled` | Same | Refused | Same. |
| `planning.goal_stalled_checkin_minutes` | Same | Refused | Same. |
| `tools.manifest.compressed` | Same while the control remains | Newly refused | C-7 requires the description change and parent/leaf tests. |
| `agents.defaults.max_tool_iterations` | Same, with lowering consent | Existing refusal retained | Preserve the existing dedicated-write boundary. |

Source check: `/workspace/wt/settings-1519/pkg/sysagent/tools/config.go::ConfigSetTool.Parameters` exposes generic `key`/`value`; `knownConfigPrefixes`, `validateConfigKey`, `performanceOnlyConfigKeys` and `validatePerformanceOnlyConfigKey` confirm the present refusal surfaces. This report does not claim the proposed expanded parity test exists or has passed.

### Signed-in browsers cut

| Cut obligation | Result | Specification evidence |
|---|---|---|
| Sign-in hidden | Verified | Overview; US-1 AS1; FR-001/055; 11-section screen table; explicit hidden-section scenario. |
| Old links have a destination | Verified | FR-003 and D-3 rows 9/17 send both `devices` and `signin` to Providers without an error. |
| Search explains the hidden surface | Verified | FR-007, UI Screens and States, D-13 row 10. |
| No active requirement depends on the moved block | Verified within the reviewed text | C-1, FR-074/076–091, D-5–7, G-19–24, SC-007/008 and DOC-009 are moved stubs; active FRs trace within Settings. No browser-auth or WebSocket-handler change is commissioned. |
| Riders match the current cut | Verified against dispatch | IN #1154, #346, #1153, #1596, #1940, #1265 wording-only, #1560, #1641, #1742, #853, #936, #995 Settings-only. #689/#1595/#1146/#598 moved; #1145/#1917 cut. |
| Separate draft approved | Not assessed | No review of `/workspace/wt/settings-1519/docs/internal/specs/signed-in-browsers-spec.md`; no endorsement of its fix claims. |

## Test Coverage Assessment

### Missing or ineffective categories

| Category | Required correction | Affected scenarios |
|---|---|---|
| Baseline-representable retention failure | Real generic handler, existing `disabled` fixture, pre-persistence refusal, fresh-load values, accepted sibling controls and guard mutation proof. | Parent replacement; D-12/G-26; R2-MAJ-001. |
| Combined persistence failure | Commit, failed live refresh and response loss in the same case; reread must use saved truth. Include stale Third-party agents reread. | Unknown result / saved-not-applied; R2-MAJ-002. |
| Browser-policy integration | Test production headers and real origin transition, not just a mocked successful `/about`. | Changed port / proxy / G-10; R2-MAJ-003. |
| Whole versus partial read failure | Separate fixtures and separate draft-preservation/save assertions. | Limits error scenarios; R2-MAJ-004. |
| Realistic partial-write failure | Valid UI inputs plus the declared seam; separate actual-handler negative test. | G-07; R2-MAJ-005. |
| Generated outcome contract | Each endpoint and failure stage exercises the agreed status/code/envelope through the real client boundary. | FR-102 and save outcome tests; R2-MAJ-006. |

### Dataset gaps

| Dataset | Gap | Recommendation |
|---|---|---|
| D-12 | Full-GET echo hits unrelated fences; mixed-case example need not change effective state; refusal outline consumes accepted rows. | Correct fixtures, status precedence and separate accepted/refused oracles. |
| D-16 | Whole-GET echo is not a valid generic-config 200 control. | Use a permitted unchanged payload; retain unrelated security refusal coverage. |
| D-10 | Override 0 is labelled accepted, contradicts current/prototype validation and the successful-storage outline. | Separate UI validation and server refusal cases without loosening validation. |
| D-3 / live-link table | Bookmark mapping and rewritten in-app permissions link are conflated. | Preserve the old bookmark fallback but fix the live-link destination. |

The concurrency barrier, real-sweep completion and deletion positive control are now specified. Those fixed omissions are not repeated as new findings. Full CI, exact-cut hands-on work and future saved RED receipts remain later gates; text inspection is not a substitute.

## STRIDE Threat Summary

S = impersonation; T = unauthorized modification; R = missing accountability; I = disclosure; D = resource exhaustion; E = excess privilege. `covered` means specified or preserved for this correction scope, not an executed security PASS; `gap` means the reported correction is needed; `decision` means founder disposition is still open; `n/a` means no additional threat established here.

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Retention mutation boundary | n/a | gap | n/a | n/a | n/a | gap | By-effect rule now exists; its test fixtures must not exercise or weaken unrelated fences instead. |
| Performance user-only writes | covered | covered | n/a | n/a | covered | covered | C-7 defines REST/tool refusal and dedicated password path; future tests must enforce it. |
| Protected section Saves | covered | covered | covered | covered | covered | covered | Single-use consent retained; truthful persistence/apply reporting still has R2-MAJ-002/006 gaps. |
| Restart/reconnection | n/a | n/a | n/a | covered | gap | n/a | Existing origin policies must remain; the proposed changed-port journey cannot complete as written. |
| Credential deletion | covered | decision | covered | n/a | decision | decision | FQ-2 is informed acceptance, not approval supplied by this review. |
| Signed-in browsers | n/a | n/a | n/a | n/a | n/a | n/a | Cut; no threat review of the separate draft. |

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| New agent tool needs registration/policy? | N/A: no new tool commissioned | Scope Boundaries, C-7 and Reachability; existing tool refusal changes are tested through FR-101. |
| Named user-facing screen/component? | Yes, specified | Reachability names `/workspace/wt/settings-1519/src/routes/_app/settings.tsx` and Data & retention's dedicated endpoint. |
| Moved controls have a prerequisite? | Yes | FR-060; Skills & Tools must already render the three moved controls at the cut. |
| Test plan requires execution? | Yes, but several oracles need correction | Test Hierarchy, Test Implementation Order and UAT rows; findings above. |
| Public documentation changes planned? | Yes | DOC-001–008/010/011/013; current Settings, memory, security and tools pages read. |

**Code correct and tested:** not established—specification review only; no product tests run.  
**Reachable by a user/agent:** planned wiring assessed; no running implementation demonstrated.

## Unasked Questions for the Author / Chief

These are engineering corrections, not new product decisions for the founder:

1. What authoritative persisted-state source reconciles a lost response when the live-config refresh failed, and how is concurrent third-value read-back represented? Resolve in R2-MAJ-002's contract, not by assuming the existing GET reads disk.
2. What exact status/code/envelope does each Settings writer return at each commit/apply stage? Resolve R2-MAJ-006 before the frontend and backend implement different meanings.
3. What origin-safe handoff lets the person reach and verify a changed listener without expanding browser permissions or guessing a proxy destination? Resolve R2-MAJ-003 and DOC-003 together.

## Verdict Rationale

**REVISE.** The updated by-effect guard addresses CRIT-002's original architectural omission, so that old critical finding is not reasserted as an unchanged design defect. Its proof is nevertheless incomplete (R2-MAJ-001), and the saved-state, restart, partial-read and test-oracle defects can still produce incorrect behavior or misleading evidence. None of those is waived by the structural traceability count.

### Escalation to the founder

This is the final grill, **before** the second and final correction; that correction has not happened yet. No remaining CRITICAL finding is established here. R2-MAJ-001–006 prevent an unqualified PASS; any blocking finding still unresolved after the final correction must go to the founder with its evidence and explicit proposed disposition. Do not resolve that uncertainty by commissioning a third grill. FQ-1/FQ-2 below are the outstanding product/security decisions, separate from those engineering findings.

### Recommended Next Actions

1. Team-lead obtains answers to FQ-1/FQ-2 before the final correction. Preserve the confirmed browser cut and rider dispositions.
2. The author corrects R2-MAJ-001 first, then R2-MAJ-002/006 together, R2-MAJ-003, R2-MAJ-004/005 and R2-MIN-001 in `/workspace/wt/settings-1519/docs/internal/specs/settings-redesign-spec.md`. This review edits none of that document.
3. After the correction, resolve or explicitly escalate any remaining blocker. Later RED/GREEN/CHECK must save a real failing run for retention preservation before implementation; no new grill round is authorized.

### Next step in the process

Verdict: **REVISE**

Review written to: `/workspace/wt/settings-1519/docs/internal/specs/settings-redesign-spec-review-round2.md`

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews the founder on "Questions for the founder", then the spec author fixes round-2 findings. Any CRITICAL finding still open after that fix is listed under "Escalation to the founder" above for the founder to decide — do not run a third grill round. Once resolved, team-lead plans the implementation (RED / GREEN / CHECK, the 5-reviewer gate).

## Questions for the founder

Only the two already-open Settings decisions remain. The browser questions moved out; #1265 is already scoped to wording only by this commission. No new product decision is needed to repair the verified contradictions above.

### FQ-1 — Should retained memory also remain searchable by agents?

Today recall reads at most 365 days. Keeping reflections for 730 days or forever does not by itself make those older reflections discoverable.

- **A — Recommended:** make recall reach all retained reflections, with bounded work and actual recall-path evidence; keep FR-050 and its exact-cut hands-on requirement.
- **B:** retain the 365-day recall limit, disclose it beside retention and in `/workspace/wt/settings-1519/docs/memory.md`, and remove the conditional FR-050 acceptance.

Answer **`FQ-1 A`** or **`FQ-1 B`**. Evidence: `/workspace/wt/settings-1519/pkg/agent/memory_adapter.go::MemoryStoreAdapter.SearchRetros` and `/workspace/wt/settings-1519/pkg/agent/memory.go::MemoryStore.ReadRetros`; spec Questions for the founder.

### FQ-2 — Confirm the security consequence of deleting keys without another password

The recorded confirm-only choice removes an intentional server password gate: someone at an unlocked signed-in screen could delete a live provider/channel key. The existing decision is **“Re-auth Consent Gate: Credential-Vault Scope Expansion to v0.1.0”**; this review does not override it.

- **A — Recommended to preserve the recorded choice, only with that consequence accepted:** confirm-only deletion, remaining authentication/admin/bypass protections and audit preserved; make the dated ADR correction and public documentation update.
- **B:** keep password confirmation for deletion as well as creation/rotation.

Answer **`FQ-2 A`** or **`FQ-2 B`**. Evidence: `/workspace/wt/settings-1519/docs/internal/architecture/ADR-022-credential-vault-reauth.md::Decision`; `/workspace/wt/settings-1519/pkg/gateway/rest_settings.go::deleteCredential`; spec C-5/FQ-2.

skills: grill-spec, omnipus-shared-rules, ux-heuristics-review; additional references read: gitnexus-exploring and omnipus-design-system.

## Evidence Table

| Claim | Evidence | Certainty |
|---|---|---|
| Correct input and baseline | `git rev-parse HEAD` → `5afdc08883983cbaf60ea5349a7ce606f21d3e05`, exit 0; `git rev-parse c2f8f8e735` → `c2f8f8e735e8241b1e1fab7fadf738ecda011eae`, exit 0. | Verified |
| Round and required format grounded | `/workspace/wt/settings-1519/.claude/skills/grill-spec/SKILL.md::Mode and round detection`, `::Output`; `/workspace/wt/settings-1519/.claude/skills/grill-spec/review-constitution.md::Review completeness check`; `/workspace/wt/settings-1519/.claude/skills/grill-spec/report-template.md`; full round-1 report and corrected specification read. | Verified |
| All 16 commissioned Settings findings reconciled | Round-1 Disposition table against `/workspace/wt/settings-1519/docs/internal/specs/settings-redesign-spec.md::Round-1 fix log` and the original review's corresponding IDs. | Verified specification assessment |
| R2-MAJ-001: full GET echo does not exercise retention guard | `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::getConfig`, `updateConfig`; `/workspace/wt/settings-1519/pkg/config/config.go::Config`; spec D-12/G-26/SC-005. | Verified |
| R2-MAJ-001: mixed-case fixture need not alter effective state | `/workspace/wt/settings-1519/pkg/gateway/rest_config_max_tool_iterations.go::protectedAgentDefaultsFingerprint`; `/workspace/.cache/go-mod/golang.org/toolchain@v0.0.1-go1.27.4.linux-amd64/src/encoding/json/decode.go::Unmarshal` documents case-insensitive binding and ordered member processing. No probe executed. | Inferred; source-derived |
| Existing-field null-parent case targets the baseline defect | `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::updateConfig`, `writeConfigJSONLocked`; `/workspace/wt/settings-1519/pkg/gateway/pairing_config_write.go::prepareConfigJSONLocked`, `orderedConfigJSONLocked`; `/workspace/wt/settings-1519/pkg/config/config_retention.go::IsDisabled`. Actual RED/green runs remain future evidence. | Verified source mechanism; execution Unknown |
| R2-MAJ-002: live read cannot prove non-persistence | `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::configRefreshError`, `getConfig`; `/workspace/wt/settings-1519/pkg/gateway/rest_context_settings.go::getContextSettings`; `/workspace/wt/settings-1519/pkg/gateway/rest_third_party_settings.go::thirdPartySettingsView`, `putThirdPartyAgentSettings`; FR-020/022. | Verified |
| R2-MAJ-003: specified poll conflicts with browser policy | `/workspace/wt/settings-1519/pkg/gateway/embed.go::spaBaseContentSecurityPolicy`; `/workspace/wt/settings-1519/pkg/gateway/rest.go::setCORSHeaders`; `/workspace/wt/settings-1519/src/lib/api/http.ts::performRequest`; FR-063/G-10. | Verified source/policy conflict; browser not run |
| R2-MAJ-004: Limits error oracles conflict | Specification “A section that cannot load”, “One failed read blocks only its group”, Read dependencies and FR-066. | Verified |
| R2-MAJ-005: fixture weakens current and prototype validation | `/workspace/wt/settings-1519/src/components/settings/ContextSection.tsx::validate`; `/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/app.js::overrideRow`, `validate`; D-10/G-07. | Verified |
| R2-MAJ-006: new failure acknowledgement not defined | `/workspace/wt/settings-1519/contracts/openapi.yaml::/settings/context`; `/workspace/wt/settings-1519/contracts/components/schemas/ContextSettings.yaml`, `/workspace/wt/settings-1519/contracts/components/schemas/ErrorResponse.yaml::code`; handlers and C-2/C-6/FR-102. | Verified |
| R2-MIN-001: live-link destination conflicts | Specification FR-009 versus “Links from other screens keep working”; S-Q3 A read in `/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/questions.md::Answered 2026-11-19`. | Verified |
| Browser cut and rider list match commission | Full Settings text, moved stubs, FR-001/003/055, D-3, Riders; user's round-2 scope/rider instruction. Separate browser draft not read. | Verified within reviewed scope |
| Traceability/catalog checks could detect omissions | Read-only `python3 -I` structural inspection, exit 0: `active=81 trace=81 missing/extra=none`; removed-FR-044 negative control detected; `Catalog: 24 requested exports found` with present/absent controls. Initial unanchored heading matcher exited 1; corrected to exact level-two boundaries before counting. | Verified structural checks only |
| Tool differences and sweep instrument are specified | C-7/FR-101; `/workspace/wt/settings-1519/pkg/sysagent/tools/config.go::ConfigSetTool.Parameters`, `knownConfigPrefixes`, `performanceOnlyConfigKeys`; spec order 7/G-14; `/workspace/wt/settings-1519/pkg/gateway/retention_goroutine.go::executeRetroSweepTick`. | Verified specification/source checks; no test execution |
| Existing founder questions remain open | Spec FQ-1/FQ-2; recall functions and credential gate cited above. No founder answer inferred. | Verified open status in input |
| **Self-check** | Re-read the complete specification and round-1 report; checked all commissioned IDs, retention scenario/D-12/G-26 against the mutation path, all twelve lenses, template sections and browser-cut boundary. Final read-only `python3 -I` report/scope check, exit 0: `7 unique findings, all required fields/sections; removed-Evidence negative control detected; REVISE matches 6 MAJOR/1 MINOR; final Self-check present`. `git diff --exit-code` and `git diff --cached --exit-code`: each exit 0, empty; HEAD unchanged; status contains only this report and the pre-existing round-1 report. No product tests, builds, browser runs, spec/code/contract edits, commits or pushes. | Verified review scope and content |
