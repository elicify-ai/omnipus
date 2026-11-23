# Adversarial Review: Settings redesign (page G)

Correction: the preliminary inline review said invalid Save had an unresolved keyboard-access defect, wrong because the approved wireframe review response explicitly specifies `aria-disabled` and activation that focuses the invalid field; correct is to preserve and test that behavior, not report it as an inaccessible native-disabled button. That candidate is withdrawn. This artifact supersedes the preliminary inline findings; this remains one round-1 review, not another grill round.

**Document reviewed:** `/workspace/wt/settings-1519/docs/internal/specs/settings-redesign-spec.md`  
**Mode:** SPEC  
**Round:** Round 1 of 2  
**Review date:** 2026-11-20  
**Reviewed branch:** `landing/settings-1519`  
**Reviewed commit:** `1d8e31042cec126f098c4ab9e53a12fb2ffdb985`  
**Release baseline:** `c2f8f8e735`  
**Verdict:** **BLOCK**

## Executive Summary

The specification has **23 findings: 2 CRITICAL, 18 MAJOR, and 3 MINOR**. Built literally, browser termination leaves independent preview permissions alive, and the specified retention guard allows a parent configuration replacement to remove keep-forever protection. Frontend coverage received the same scrutiny as backend coverage: save contracts, persisted-versus-running state, restart recovery, routing, partial reads, keyboard focus, catalog reuse and test instruments were checked—not just the rendered happy path.

| Severity | Count |
|---|---:|
| CRITICAL | 2 |
| MAJOR | 18 |
| MINOR | 3 |
| OBSERVATION | 0 |
| **Total** | **23** |

**Review limits:** GitNexus is unavailable on this pod; exploration used `rg` and direct file reads. This is a specification review, not execution evidence: no build, test, browser campaign, exploit or performance measurement was run. “Verified” below means the specification contradiction or source mechanism was verified by reading; it does not mean the proposed feature has been implemented or exercised. MAJ-017's proposed concurrency failure is explicitly **Inferred / WARNING**, not an independently verified product failure or a block on its own.

Only this report is authorized to change. The specification and production files remain untouched; there is no commit or push.

### Special-focus conclusions

| Focus | Assessment |
|---|---|
| C-1 / #1096 | The capped set, public-id cookie lookup, server-side 24-hour boundary, six socket-ending causes and privacy-limited list are explicit. Preview revocation, durable legacy recognition, duplicate-cookie verification budget, restart-time eviction semantics, admission races and cross-site read protection still need correction or specification. |
| C-3 retention | The requested write ranges are explicit. The read schema contradicts repair of stored values; the proposed generic-config guard misses ancestor replacement; removal from the memory schema alone does not reject obsolete requests. Sweep and recall must each be proved with the actual consumer, not just the stored switch. |
| C-4 / B4 | One single-use token is feasible with the stated per-section batching; a multi-use token is not required. The named guard must execute the real save plan and observe actual protected requests. Its oracle must distinguish local mode, platform mode, cancellation and a new consent after late 409. |
| Old links and leaving drafts | D-3 covers the principal old/new routes and prefilled model/God Mode destinations. Full-document navigation must not be confused with hash navigation; partial reads and pending-save state also need precise recovery behavior. |
| Independent oracles | 83 FR declarations have 83 traceability rows, but that count does not prove valid expectations. D-10, G-07 and G-14 contain concrete false-oracle or ineffective-instrument problems. |

## Findings

### CRITICAL Findings

#### CRIT-001 — Ending a browser does not revoke its independent preview grants

- **Severity:** CRITICAL.
- **Lens:** Security; incompleteness; user promises.
- **Affected section:** C-1 T1; FR-083–FR-087; integration boundary “Gateway WebSockets”; DOC-009.
- **Failure scenario:** Browser B has opened a private Library document or mail preview. Browser A ends B. B's cookie and sockets stop working, but the already-issued preview URL still authorizes fresh reads until its separate lifetime expires. A lost device can therefore keep reading after the owner has been told it is signed out.
- **Evidence:** The spec limits termination effects to the browser entry and chat/live-browser sockets. `/workspace/wt/settings-1519/pkg/gateway/preview_token.go::PreviewSessionKey` keys grants by a digest of the **whole raw credential**, not the proposed public session id; `PreviewTokenStore.Lookup` checks only presence and grant expiry. `/workspace/wt/settings-1519/pkg/gateway/rest_auth.go::HandleLogout` explicitly invalidates Library, mail and attachment grants. Remote End has the target id, not the target's raw cookie, so copying that request-based logout helper cannot revoke the target's grants. `/workspace/wt/settings-1519/docs/security.md::Mail metadata and temporary previews` promises logout revocation and fresh mail retrieval per request.
- **Recommendation:** Define one authenticated browser identity shared by authentication, socket ownership and all derived preview grants, without storing plaintext credentials. Every termination cause must invalidate that browser's dependent grants, preserve other browsers' grants, and prevent an in-flight mint from publishing a grant after revocation. Add real grant-serving tests for all six causes, including a fresh mail/attachment request after termination. Do not claim revocation can erase content already downloaded.
- **Certainty:** **Verified, high confidence**—the independent grant lifetime, key mismatch and missing specification coverage were read directly; no exploit was run.

#### CRIT-002 — A parent replacement bypasses the proposed retention leaf guard

- **Severity:** CRITICAL.
- **Lens:** Security; contract-first gaps; data integrity.
- **Affected section:** C-3; FR-044, FR-048, FR-049; D-12; SC-005.
- **Failure scenario:** An install has `storage.retention.disabled: true`, or the new memory keep-forever flag. An authenticated generic-config write sends `{"storage":null}`. Merely adding `storage.retention` to `blockedPaths` does not match that request; the generic merge replaces the parent, removing the protection from disk. Defaults can then resume deletion on a later sweep, despite the promised single write path and preservation of the legacy switch.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/blocked_paths.go::collectPaths` records only `storage` for this input; `matchBlockedPath` matches exact collected paths. `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::updateConfig` replaces scalar/null parents, and preserves/fingerprints only protected agent-default fields. `writeConfigJSONLocked` persists the result. `/workspace/wt/settings-1519/pkg/gateway/rest_channels_outbound_guard.go::guardedConfigMutate` guards channels, not retention; `/workspace/wt/settings-1519/pkg/gateway/pairing_config_write.go::orderedConfigJSONLocked` adds no retention protection. `/workspace/wt/settings-1519/pkg/config/migration.go::loadConfig` loads over defaults; `/workspace/wt/settings-1519/pkg/config/config_retention.go::IsDisabled` directly reads the boolean. D-12 tests named leaves, not replacement of their ancestor.
- **Recommendation:** Specify protection of the **effective retention subtree across the entire mutation**, including parent replacement and alternate key casing. Reject an attempted indirect change before persistence, while allowing genuine unrelated sibling edits. Extend D-12 with null/scalar/array parents, mixed benign changes, casing and dotted shapes; assert unchanged saved and effective retention, including both keep-forever flags. Pin the requested 400 response too: the current generic blocked-path branch returns 403, so adding a list entry alone is not the complete change.
- **Certainty:** **Verified, high confidence**—the specified implementation is insufficient on the source-read mutation chain. The future sweep consequence is a static deduction, not an executed deletion.

### MAJOR Findings

#### MAJ-001 — Changed-only saves conflict with the Third-party agents replacement contract

- **Severity:** MAJOR.
- **Lens:** Contract-first gaps; inconsistency; frontend/backend integration.
- **Affected section:** FR-015, FR-019; C-4's unchanged-endpoint premise; Third-party agents UI state.
- **Failure scenario:** The person changes only Codex parameters. Sending only that field fails because its path is required. Sending its complete row but omitting the other programs resets those programs to empty. Sending all rows instead violates the promised preservation of another tab's untouched edits.
- **Evidence:** `/workspace/wt/settings-1519/contracts/components/schemas/ThirdPartyAgentSettings.yaml::description` explicitly defines whole-section replacement and clearing of absent CLI rows. `/workspace/wt/settings-1519/contracts/components/schemas/CliPreset.yaml::required` requires both fields. `/workspace/wt/settings-1519/pkg/gateway/rest_third_party_settings.go::thirdPartyRowsFromBody` initializes omitted rows empty; `putThirdPartyAgentSettings` writes the resulting set. C-1–C-5 do not change this contract.
- **Recommendation:** Add an explicit contract-first partial-update change for this endpoint, including omission versus clearing of each row field and preservation of sibling programs. Alternatively explicitly withdraw the universal partial-save/concurrency promise for this section through the author/founder process; do not silently send stale full snapshots. Test one-field edits and two independent tabs against the real handler.
- **Certainty:** **Verified, high confidence.**

#### MAJ-002 — Partial request bodies do not prove concurrent saves preserve untouched fields

- **Severity:** MAJOR.
- **Lens:** Incompleteness; concurrency; false-green risk.
- **Affected section:** FR-019; “Two browser tabs editing the same section”; TDD hierarchy.
- **Failure scenario:** Two Limits saves read the same old context settings. A changes one cap and B changes another. Although both send partial bodies, B later persists the old value of A's field and silently loses A's change.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/rest_context_settings.go::putContextSettings` builds `next := cfg.Context` before entering `safeUpdateConfigJSON`, then writes all context fields from that snapshot inside the lock. `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::safeUpdateConfigJSON` serializes the write, not that earlier snapshot. FR-019 attributes preservation to the frontend's changed-only request; the plan does not specify this server-side interleaving.
- **Recommendation:** Require merging only requested fields into authoritative saved state under the write lock, and review the other section writers for the same pattern. Add a deterministic real-handler concurrency case: pause both reads, commit disjoint edits in each order, and assert both survive. Also pin same-field last-writer behavior and explicitly define whether list-valued fields conflict as a whole.
- **Certainty:** **Verified, high confidence** for the source mechanism; not race-tested in this review.

#### MAJ-003 — The retention response schema prevents the promised repair UI

- **Severity:** MAJOR.
- **Lens:** Contract-first gaps; frontend error state.
- **Affected section:** C-3 `RetentionSettings`; FR-041–FR-046.
- **Failure scenario:** The stored chat count is 500. FR-046 requires GET to return 500 so the person can see and repair it, but the same schema caps the response at 365. A validating client rejects the entire response before the field can render. The UI shows a load error rather than the repair control.
- **Evidence:** C-3 assigns one bounded schema to GET and PUT, and omits a `required` list despite FR-041 promising all fields. `/workspace/wt/settings-1519/src/lib/api/http.ts::performRequest` throws `ApiSchemaError` when the supplied response schema rejects the body. The conflict also affects a partial PUT echoing an untouched out-of-range sibling.
- **Recommendation:** Separate the complete read/acknowledgement shape from the strict partial update shape. Reads must represent the documented stored/effective state, including repairable legacy values; writes must enforce the new ranges, reject read-only members and require at least one editable field. Define zero/negative legacy handling consistently with the existing accessors. Test the generated response validator and the actual repair screen, not a bypassed/mock API return.
- **Certainty:** **Verified, high confidence.**

#### MAJ-004 — Removing schema members does not reject obsolete memory writes by default

- **Severity:** MAJOR.
- **Lens:** Inconsistency; contract enforcement; false-green risk.
- **Affected section:** C-3 `MemorySettings` row; FR-045; `TestMemorySettings_RefusesRetentionFields`.
- **Failure scenario:** With the default inbound-validation setting, an older client sends a valid recap change plus a retired retention field. A generated struct without that member silently ignores it and applies the recap change instead of rejecting the entire request unchanged.
- **Evidence:** C-3 says `additionalProperties: false` is why the request gets 400. `/workspace/wt/settings-1519/pkg/gateway/rest_memory_settings.go::putMemorySettings` passes `Gateway.ValidateInbound` into the decoder; `/workspace/wt/settings-1519/pkg/gateway/rest_inbound_validate.go::decodeAndValidate` uses ordinary permissive JSON decoding when that flag is false.
- **Recommendation:** Require unconditional boundary rejection of both retired fields before any mutation, independent of optional whole-schema validation. Test each field alone, each mixed with a valid recap edit, and both validation-flag states; assert the saved file and live settings remain unchanged. Apply the same unconditional enforcement standard to the new retention endpoint's ranges, unknown/read-only fields and empty body.
- **Certainty:** **Verified, high confidence.**

#### MAJ-005 — A failed response is incorrectly treated as proof that nothing was saved

- **Severity:** MAJOR.
- **Lens:** UI states and journeys; persistence ownership; ambiguity.
- **Affected section:** FR-012, FR-015; integration boundary “Config file and nightly sweeps”; #995 rider.
- **Failure scenario:** A write reaches disk but refreshing the running configuration fails, or its response is lost. The Save bar labels the value unsaved; Discard restores the old display while the newer durable value remains. A subsequent restart applies a setting the person believes they discarded.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::applyWrittenConfigLocked` returns `configRefreshError` **after** durable persistence. `/workspace/wt/settings-1519/pkg/gateway/rest_third_party_settings.go::putThirdPartyAgentSettings` already distinguishes `saved_not_refreshed`. FR-015 instead classifies failed writes as drafts, and `/workspace/wt/settings-1519/docs/tools.md::How to change an agent's tool access` explicitly warns that saved-but-unapplied changes are not rolled back.
- **Recommendation:** Specify per-write states: not saved, saved/applied, saved/not applied, and acknowledgement unknown. Define authoritative read-back and retry behavior; an unknown result must not assert rollback, and Discard only discards drafts. Preserve current endpoint-specific apply warnings, and add failure-after-write plus response-loss tests on the real server/client seam.
- **Certainty:** **Verified, high confidence** for durable-write/apply failure; response loss is an additional specified test case, not a measured incident.

#### MAJ-006 — Failed hot reload leaves neither the promised banner nor its focus target

- **Severity:** MAJOR.
- **Lens:** UI states; accessibility and keyboard; inconsistency.
- **Affected section:** C-2; FR-034–FR-038; accessibility restart focus rule.
- **Failure scenario:** Defense level saves but its hot reload fails. The response opens Restart now. After Later, the pending-restart list is empty because hot keys are excluded. The required persistent warning disappears, and keyboard focus is directed to a banner button that does not exist; the person can believe the new protection is active.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/rest_prompt_guard.go::putPromptGuard` returns `Saved: true, RequiresRestart: true` on reload error. `/workspace/wt/settings-1519/pkg/gateway/rest_pending_restart.go::RestartGatedKeys` excludes that key. FR-035 requires focus on the banner, FR-036 keeps hot keys out, and FR-038 hides an empty list.
- **Recommendation:** Define a durable/refreshable pending-apply source for failed hot changes, distinct from boot-frozen restart-key comparison, and say how the common UI represents and clears it. Include Later, Escape, section switch, reload and retry paths, with an actually existing focus target and a real failed-apply test. Do not fix this by unconditionally adding every hot key to the static restart list.
- **Certainty:** **Verified, high confidence.**

#### MAJ-007 — Restart after a port change has no truthful completion or recovery journey

- **Severity:** MAJOR.
- **Lens:** UI journey; operability; false-green risk.
- **Affected section:** US-4; FR-033, FR-035; G-10; reuse of `GatewayRestartModal`.
- **Failure scenario:** A directly connected person changes port 8080 to 8081, restarts, and remains on the old origin. The reused dialog polls the old address and cannot prove the new listener is ready. Conversely, its first successful health response can come from the old process before it has stopped and be mistaken for restart completion.
- **Evidence:** `/workspace/wt/settings-1519/src/components/settings/GatewayRestartModal.tsx::pollUntilOnline` polls relative `/health` and returns on the first 2xx. G-10 specifically changes Port but has no new-address step or independent applied-value proof. `/workspace/wt/settings-1519/pkg/gateway/rest_pending_restart.go::RestartGatedKeys` identifies host/port as listener-rebind settings.
- **Recommendation:** Specify direct-listener and stable-proxy cases separately. Before restart, retain an actionable reconnect destination or deployment-specific instruction, keep failure recovery available, and verify the intended running configuration/process before announcing completion. Do not blindly navigate a proxy client to its internal bind address. Add old-process-still-healthy, changed-port, failed-start and reconnect tests.
- **Certainty:** **Verified, high confidence** for the existing poll and missing journey; no restart was executed.

#### MAJ-008 — “Typed address” conflates hash navigation with leaving the document

- **Severity:** MAJOR.
- **Lens:** Ambiguity; infeasibility; user control.
- **Affected section:** FR-014; “Every way out asks first”; G-06.
- **Failure scenario:** The person edits a value and types a full destination URL or reloads the page. The spec demands the app's Stay/Leave dialog for a typed address, but full-document unload uses the browser's native prompt. A test that changes only the hash passes while the claimed exit coverage is not exercised.
- **Evidence:** `/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/review-response.md::BC-D5` describes a **typed hash**; FR-014 does not make that distinction and reserves the native prompt only for tab close. `/workspace/wt/settings-1519/src/components/library/preview/unsavedGuard.ts::beforeunload listener` documents and implements the native-prompt boundary for close/reload/document navigation.
- **Recommendation:** Split same-document router/hash transitions from full-document navigation, reload and close. Use the custom confirm for supported in-app transitions and native `beforeunload` protection for document exits; state browser/platform limitations rather than promising exact app wording there. Test Stay/Leave for Back, Forward, typed hash, reload and full navigation separately, including unchanged history on Stay and dirty phone-back navigation.
- **Certainty:** **Verified, high confidence** for the spec/reference mismatch; native behavior is grounded in the existing guard, not a new browser run.

#### MAJ-009 — Partial-read states contradict the blanket prohibition on saving a section

- **Severity:** MAJOR.
- **Lens:** UI states and journeys; inconsistency.
- **Affected section:** UI Screens and States; FR-066; US-9 AS2.
- **Failure scenario:** Storage counts fail while retention loads successfully. The partial-state table says retention remains editable, but FR-066 prevents anything in that section saving. Likewise a failed God Mode read can disable unrelated Protection changes, and a failed voice read can block the default model.
- **Evidence:** The table explicitly says “retention still editable” and “rest works”; FR-066 says “nothing in that section can be saved.” These are different behaviors for the same partial failure.
- **Recommendation:** Name the read dependencies of each editable group and action. Block only fields whose saved baseline or safety prerequisites are unavailable; preserve loaded unrelated groups and drafts. Give whole-section versus group-only errors separate scenarios, including Retry that does not replace unrelated drafts.
- **Certainty:** **Verified, high confidence.**

#### MAJ-010 — A two-order transitional UI contradicts the approved landing dependency

- **Severity:** MAJOR.
- **Lens:** Inconsistency; reachability; overcomplexity.
- **Affected section:** FR-060; D-9; Edge Cases; Skills & Tools boundary; Assumptions.
- **Failure scenario:** Implementers build a temporary Settings layout retaining controls in “their current places” even though the old Security/Integrations tabs are forbidden by FR-001. They must invent a second layout and migration switch, or make those controls unreachable while the destination is missing.
- **Evidence:** `/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/questions.md::S-Q3 — Order of work with Skills & Tools` records **A: Settings is built after Skills & Tools lands; no temporary moving block**. The spec instead says the packages may land in either order and cites S-Q3 A for that fallback.
- **Recommendation:** Restore the recorded prerequisite, name the destination-route/control readiness checks, and remove the uncommissioned two-order implementation and tests. Preparation may proceed, but cutting/landing must obey the dependency. A different order needs a new explicit founder decision, not an inferred fallback.
- **Certainty:** **Verified, high confidence.**

#### MAJ-011 — The universal one-bcrypt limit cannot preserve duplicate-cookie recovery

- **Severity:** MAJOR.
- **Lens:** Infeasibility; security; inconsistent acceptance.
- **Affected section:** C-1 T3/T4; FR-077, FR-086; Performance Bounds; SC-007.
- **Failure scenario:** A request contains a genuine cookie and a planted cookie with the same public id but a different secret. The id selects the stored hash, not which secret is genuine. Comparing only one candidate can clear the legitimate sign-in depending on header order; trusting the id to resend a candidate can resend the plant.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/middleware/planted_cookie.go::genuineSessionResend` verifies distinct candidates, refuses ambiguous multiple successes and preserves original expiry. `plantedResendMaxComparisons` caps this exceptional path at four. FR-086 demands preserved recovery while Performance Bounds says at most one comparison **per request**, including legacy cookies.
- **Recommendation:** Limit ordinary single-cookie authentication to one hash verification and specify a separate, small, bounded recovery budget. Define clear-only behavior on exhausted budget or multiple valid candidates. Add candidate-order permutations, same-id/wrong-secret, two valid sign-ins, legacy-plus-new, over-budget and expiry-during-verification cases. Never use the public id as authentication evidence.
- **Certainty:** **Verified, high confidence.**

#### MAJ-012 — Several acceptance oracles are false or internally contradictory

- **Severity:** MAJOR.
- **Lens:** Testability and false-green risk.
- **Affected section:** D-10; “One of two writes fails”; “An invalid value blocks saving”; G-07; SC-003.
- **Failure scenario:** A mocked context API rejects 512 and makes the partial-save test pass, but the real handler accepts 512, so the end-to-end test never exercises partial failure. Separately, applying the invalid-input Scenario Outline to all D-10 rows rejects values the dataset says are valid. An unqualified SC-003 also fails correct platform-mode behavior or encourages an unnecessary password request.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/rest_context_settings.go::validateContextSettingsUpdate` accepts positive context windows, including 512; the spec invents a 1024 refusal message. D-10 rows 3 and 8 say “accepted,” while the outline's Then universally requires an error and unavailable Save. SC-003 requires one reauth for every protected Save, contradicting FR-032's zero in platform mode and omitting FR-031's new-consent exception.
- **Recommendation:** Use a declared deterministic server failure seam for partial failure, or a genuinely refused request that the UI can submit under the scenario's setup—without changing production bounds to match the fixture. Split accepted and rejected data rows. Make password/request counts conditional on mode and consent attempt. Keep expected values in independent spec-derived tables, and require a real-handler counterpart to the mocked partial-save test.
- **Certainty:** **Verified, high confidence.**

#### MAJ-013 — Rider acceptance and agent-tool exceptions are not owned or testable

- **Severity:** MAJOR.
- **Lens:** Incompleteness; security; reachability; agent tool parity.
- **Affected section:** Header's agent-tool claim; Riders; FR-044; Reachability; #936, #1265, #1146, #598.
- **Failure scenario:** A team implements the new screens and marks riders complete from their one-line mappings. The generic REST guard changes but `set_config` still reaches Performance fields; ending a sign-in still loses the message or location; “Clear sessions” gets one generic confirmation without the pending visibility/re-confirm decision. All named FR tests can pass because these acceptance conditions have no complete mapping.
- **Evidence:** The spec says no tool changes and lists riders “to be confirmed,” yet #936 explicitly requires both generic REST and `set_config` refusals. `/workspace/wt/settings-1519/pkg/sysagent/tools/config.go::ConfigSetTool` is a real existing tool surface, and `performanceOnlyConfigKeys` presently covers the #904 tool-iteration fields, not a declared full new rider matrix. Issue #1146 requires draft preservation **or an explicit loss explanation**, direct login transition and return to the same place; G-22 only says handling “per #1146.” Issue #1265 contains a founder-deferred visibility/re-confirm decision. Issue #598 allows either instance-local limiters or explicit fixture isolation; the spec names neither mechanism nor an isolation test. All four issue bodies were read from GitHub with exit 0.
- **Recommendation:** Obtain chief-confirmed rider ownership before finalizing the design. Map each carried rider to exact acceptance, test and docs; explicitly retain/defer unresolved scope rather than claim it fixed. Replace “no tool changed” with a field-by-field allow/refuse matrix for the #936 changes, an explicit security reason for user-only fields, clear refusal behavior, and tool description/schema/test disposition. Add a named parity/refusal test that fails if an affected field has neither an allowed agent path nor its documented, tested exception. Do not reopen an unsafe agent path just to satisfy parity.
- **Certainty:** **Verified, high confidence** for missing ownership/acceptance and tool-scope contradiction. Chief confirmation is **Unknown**, not presumed denied or granted.

#### MAJ-014 — G-14 can pass without running the memory sweep

- **Severity:** MAJOR.
- **Lens:** Testability and false-green risk.
- **Affected section:** G-14; FR-049; retention test plan.
- **Failure scenario:** The tester creates an old reflection, invokes the session-sweep endpoint and sees the reflection still there. The row passes even if the memory keep-forever check was never implemented, because no retrospective sweep ran.
- **Evidence:** G-14 acknowledges that the POST covers sessions and leaves the actual retrospective trigger unresolved. `/workspace/wt/settings-1519/pkg/gateway/retention_goroutine.go::executeRetroSweepTick` is a separate path calling `retentionRetroSweepFn`; its loop has its own tick.
- **Recommendation:** Specify a test-only controlled trigger/clock through the real retrospective sweep path and proof it completed. Pair the kept old reflection with an otherwise identical switch-off case that actually deletes it; show chat cleanup still runs when only memory is forever, and neither runs for the legacy all-forever switch. For FQ-1 A, separately query the real recall path with distinct old content. A stored boolean or unchanged file after the wrong sweep is not sufficient evidence.
- **Certainty:** **Verified, high confidence.**

#### MAJ-015 — Legacy-cookie recognition has no durable representation or restart proof

- **Severity:** MAJOR.
- **Lens:** Data lifecycle; ambiguity; migration.
- **Affected section:** C-1 Storage/Migration; FR-076, FR-085; migration test/G-21.
- **Failure scenario:** The old hash is migrated and its legacy fields are removed on a subsequent save. After restart, the loader cannot identify which of the otherwise identical five-field records accepts an unprefixed cookie. It either signs out the preserved browser, scans all hashes, or recreates a new issue time for a missing-time legacy record.
- **Evidence:** C-1 says “marked legacy” but the exclusive stored shape is `{id, hash, issued_at, label, device_kind}`; FR-085 removes the old fields. Neither the marker encoding nor its relationship to stable id/issue time is defined. The migration scenario proves continuing after the update, not persistence, another sign-in, ordinary config save and a second restart.
- **Recommendation:** Define the durable legacy discriminator or unambiguous representation, one-time migration persistence and failure behavior. Preserve stable id, original expiry and legacy verification eligibility across saves/restarts; assigning a missing issue time must not repeat indefinitely. Test old-cookie use after new sign-ins and removal of the old fields, ending/evicting the legacy entry, and migration write failure. Reconcile legacy label enrichment with the “no per-request config writes” rule.
- **Certainty:** **Verified, high confidence** that the required representation/lifecycle is unspecified; the stated failures are alternative literal implementations, not reproduced product defects.

#### MAJ-016 — Restart resets the eviction policy from last use to sign-in age

- **Severity:** MAJOR.
- **Lens:** Inconsistency; user promise; data lifecycle.
- **Affected section:** FR-078, FR-091; S-Q14 A; D-5; DOC-009.
- **Failure scenario:** A signed in at 09:00 and was used at 11:55; B signed in and was last used at 10:00. After a noon restart, both last-use values reset to issue time. Before either makes another request, an 11th sign-in evicts A, although B was used longest ago. The displayed policy and founder's chosen eviction order are no longer true across restart.
- **Evidence:** FR-078 and the exact UI text in FR-090 promise least-recent use. FR-091 and the restart BDD explicitly reset it to issue time. Clarifications identify this as an architect choice to avoid writes, not a recorded founder acceptance of a different eviction policy.
- **Recommendation:** Expose this trade-off to the founder (Q4 below), then align storage, ordering, UI/docs and tests with that answer. Do not silently replace “used longest ago” with “signed in earliest after restart.” Add the crossed issue-time/use-time fixture above, with restart before the next login, plus concurrent use/eviction ordering.
- **Certainty:** **Verified, high confidence**—the contradictory outcome follows directly from the two required rules; no runtime measurement is claimed.

#### MAJ-017 — Revocation is not ordered against in-flight socket admission

- **Severity:** MAJOR; **WARNING, not an independent block** pending implementation verification.
- **Lens:** Concurrency; security; integration boundaries.
- **Affected section:** FR-078–FR-084; six ending-cause scenarios.
- **Failure scenario:** B passes authentication, A revokes B and closes the sockets currently registered for it, then B's paused handshake registers a new socket. A close-current-sockets implementation can satisfy all six already-open-socket tests while admitting a revoked connection.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/websocket.go::WSHandler.ServeHTTP` calls `authenticateWS` before separately adding to `h.sessions`; `authenticateWS` currently binds the account username, not the proposed browser id. The spec's six-cause outline starts with both sockets already open and states no admission/revocation ordering rule.
- **Recommendation:** Specify a per-browser authenticated identity and an admission barrier/revalidation rule so a completed revocation cannot be followed by a valid new registration. Add deterministic auth→pause→revoke→register and expiry-during-auth cases, including live-browser attachment. Define how durable revocation and live-config refresh failure interact. Do not stop the agent's independent turn merely because its UI socket closes.
- **Certainty:** **Inferred, medium confidence**—the current split lifecycle and missing requirement are verified, but the new implementation does not exist. This warning does not drive the BLOCK verdict.

#### MAJ-018 — The new-cookie threat tests omit an existing cross-site read defense

- **Severity:** MAJOR.
- **Lens:** Security; regression test coverage.
- **Affected section:** C-1 T5; FR-077, FR-079, FR-081, FR-086; regression table.
- **Failure scenario:** A replacement resolver passes the new id/secret and CSRF mutation tests but omits the existing Fetch Metadata check. A framed preview that sends a cookie on a cross-site subresource request can regain authenticated reads. SameSite and the mutation-only CSRF test do not prove this defense survived.
- **Evidence:** `/workspace/wt/settings-1519/pkg/gateway/middleware/session_cookie.go::IsCrossSiteSubresourceRequest` documents the measured framed-WebKit case; `ResolveUserFromCookie` refuses it before verification. `/workspace/wt/settings-1519/pkg/gateway/middleware/planted_cookie.go::genuineSessionResend` also refuses recovery on that path. The spec's only explicit cross-site threat row is T5, ending sign-ins through mutations; the regression table names planted cookies but no new-format cross-site read matrix. `/workspace/wt/settings-1519/pkg/gateway/middleware/csrf.go::CSRFMiddleware` also exempts bearer requests, while FR-081 is unqualified and D-7 permits user-bearer deletion.
- **Recommendation:** Require unchanged Fetch Metadata rejection for both new and migrated cookies, on mandatory/optional authentication and duplicate-cookie recovery, alongside positive same-origin/top-level controls. Specify the CSRF rule by the credential actually used: cookie mutations versus bearer-only calls, and mixed/invalid credentials. Assert denied requests have no mutation or cookie resend, and keep the existing planted-cookie middleware ordering.
- **Certainty:** **Verified, high confidence** for the current defense and missing/ambiguous contract coverage; this is a specification regression risk, not a claim that current code bypasses the guard.

### MINOR Findings

#### MIN-001 — The successful Save bar has no defined lifetime

- **Severity:** MINOR.
- **Lens:** Ambiguity; visible system status.
- **Affected section:** FR-011, FR-018; Save bar states.
- **Failure scenario:** One implementation immediately hides the zero-dirty bar, so “Saved at HH:MM” is never visible; another leaves it indefinitely, contradicting the zero-change rule.
- **Evidence:** FR-011 requires hidden at zero changes; FR-018 requires success text in the same bar without a success-state duration or transition.
- **Recommendation:** Define the success state and its exit condition, including a new edit during it, route change and the live-region announcement. Preserve an accessible confirmation without relying on a single-frame render.
- **Certainty:** **Verified, high confidence.**

#### MIN-002 — Browser termination copy contradicts the vocabulary rule

- **Severity:** MINOR.
- **Lens:** Inconsistency; recognition and wording.
- **Affected section:** FR-074 versus FR-090 and End confirmation scenarios.
- **Failure scenario:** The sign-in list says browser but the confirmation says “End session,” reintroducing the ambiguity with chat-session deletion that the spec explicitly forbids.
- **Evidence:** FR-074 forbids “session” alone; FR-090 prescribes the confirm label “End session.”
- **Recommendation:** Pick one browser/sign-in term and use it consistently in the confirm, accessible name, tests and docs; have frontend-lead apply the product voice rules.
- **Certainty:** **Verified, high confidence.**

#### MIN-003 — The frontend plan omits its typecheck and design-system verification gates

- **Severity:** MINOR.
- **Lens:** Design-system reuse; test-plan completeness.
- **Affected section:** Test Hierarchy; Test Implementation Order; Reachability “Where tests run.”
- **Failure scenario:** Mocked component tests pass while generated API use fails project typechecking, or newly composed controls violate catalog/token locks. The feature-specific plan does not name the checks that detect either before hand-back.
- **Evidence:** The plan lists Go, Vitest and Playwright but no typecheck or design-system scanner. `/workspace/wt/settings-1519/package.json::scripts` wires `typecheck` to `tsc -b --noEmit` and `lint:design-system-locks` to the scanner; `/workspace/wt/settings-1519/.claude/skills/omnipus-design-system/SKILL.md::13. Running the gates, in order` requires the latter. The positive catalog check found all 32 named exports, so a missing component is **not** the finding.
- **Recommendation:** Add these gates to the implementation evidence plan, respecting the pod's `heavy` wrapper and targeted-test limits; full-suite/design-system CI evidence remains at the cut. Record generation/drift checks separately from behavioral tests. Do not run these gates merely to review this text-only spec.
- **Certainty:** **Verified, high confidence.**

## Structural Integrity

### Spec-mode checklist

| Check | Result | Notes |
|---|---|---|
| Valid `Status:` | PASS | `In review`. |
| ADR linked or reason for none | PASS, conditional | Explicit reason and accepted credential-vault decision cited; FQ-1/FQ-2 and this review's Q4 remain decisions, not approvals. |
| Contract changes first | PASS structurally | C-1–C-5 precede stories/implementation; MAJ-001/003/004 identify substantive contract gaps. |
| API and data section | FAIL completeness | Present, but dependent grants, legacy representation and mutation preservation are incomplete. |
| Screens, loading/empty/error/partial | FAIL consistency | All 12 sections named; MAJ-005/006/009 remain. |
| End-to-end user journey | FAIL completeness | Present; changed-listener recovery and document exits are incomplete. |
| Accessibility and keyboard | FAIL one journey | Names, live regions, dialog stacking, touch and reduced motion are covered; failed-hot-reload focus target is impossible under the current banner rules. |
| Catalogue-first components | PASS structurally | All 32 proposed exports found; `Button` positive control succeeds; `Item` is absent as stated. No new kit needed. |
| Security/user promises | FAIL completeness | CRIT-001/002, MAJ-011/015/016/018. |
| BDD with independent expected values | FAIL | MAJ-012/014. |
| Requirement → scenario → test | PASS coverage count only | 83 declarations and 83 rows, no missing FR id. This does not validate their oracles or rider coverage. |
| Epic and owned riders | FAIL | Epic named; chief confirmation/complete rider acceptance missing (MAJ-013). |
| Reachability | FAIL completeness | Main screen/routes identified, but dependency order and changed tool restrictions are inconsistent. |
| Named public-documentation TODOs | PASS structurally | DOC-001–011 name user pages and behavior changes; add corrected revocation/apply/restart semantics. DOC-012 is an internal ADR amendment, not a public-doc substitute. |

### Twelve-lens coverage

| Lens | Principal findings / result |
|---|---|
| 1. Ambiguity | MAJ-008/015; MIN-001. |
| 2. Incompleteness | CRIT-001; MAJ-002/013/015. |
| 3. Internal/external inconsistency | MAJ-001/004/006/009/010/016; MIN-002. |
| 4. Infeasibility | MAJ-008/011. |
| 5. Contract-first | CRIT-002; MAJ-001/003/004; actual generated-client validation inspected. |
| 6. Security / user promises | CRIT-001/002; MAJ-011/015/018; MAJ-017 warning. |
| 7. Reachability | MAJ-010/013; screen/route/table inspected, not treated as implemented. |
| 8. UI states and journeys | MAJ-005–009; MIN-001. |
| 9. Accessibility and keyboard | MAJ-006/008; invalid Save candidate withdrawn with evidence. No visual/axe execution claimed. |
| 10. Design-system reuse and brand | MIN-003; catalog and ownership rules checked. No invented missing-kit or visual-brand finding. |
| 11. Testability / false greens | MAJ-002/012/014/018; counters alone do not prove behavior. |
| 12. Overcomplexity | MAJ-010's unapproved transitional layout; C-4's rejection of a multi-use token is justified. |

### Agent-tool scope check

This is an audit of the proposed scope, **not approval of a new agent write path**. Existing private authentication fields and user-only execution/security settings may deliberately differ, but the exceptions and refusal tests must be explicit. #936 makes the blanket “no tool changed” claim false.

| Property / group audited | User UI / API path in scope | Agent path / required disposition |
|---|---|---|
| `session_days` | Data & retention / new retention PUT | `set_config` has no writable `storage.*` prefix; document user-only deletion-policy exception and test clear refusal. |
| `memory_retros_days` | Same | Same; remove old memory writer explicitly. |
| `memory_retros_forever` | New switch / new retention PUT | Same exception must name the new property, not assume coverage from its neighbors. |
| `keep_everything_forever` / persisted `disabled` | Read-only in UI/API; file edit only | No tool mutation; explicit read-only refusal. |
| Browser `id` | Read/list/end target; assigned on sign-in | Server-owned identity, not an agent-settable property. |
| Browser `hash` | Never served or user-entered as a hash | Server-owned credential material; no tool path. |
| Browser `issued_at` | Derived on sign-in/migration | Server-owned expiry anchor; no tool setter. |
| Browser `label` | Derived from client header | Informational derivation, not an editable label in this spec. |
| Browser `device_kind` | Derived from client header | Same. |
| `last_used_at`, `expires_at`, `current` | Read-only derived list values | No tool mutation; last-use persistence policy remains Q4. |
| `max_parallel_agents` | Limits / Performance PUT | #936 requires generic REST and `set_config` refusal; name security exception and RED case. |
| `goal_max_rounds` | Same | Same. |
| `goal_reminder_quiet_seconds` | Same | Same. |
| `goal_stalled_checkin_enabled` | Same | Same. |
| `goal_stalled_checkin_minutes` | Same | Same. |
| `max_tool_iterations` | Same, with lowering consent | Existing #904 exception must remain enforced, including ancestor writes. |
| Legacy `tools_on_demand` | Transition depends on Skills & Tools prerequisite | Explicit disposition required; do not leave the rider's “every Performance field” scope implicit. |

**Proposed RED parity test name:** `TestSettingsAgentWriteParity`—a proposal for the test author, not a claim the test exists. It should enumerate each affected editable field and require either its intended agent setter or an explicit security/user-only exception with a verified refusal through the actual tool. Pair with existing #904 guards and the new #936 cases; update the tool description and parameter-schema documentation where the supported surface changes.

## Test Coverage Assessment

### Missing or ineffective categories

| Category | Required correction | Affected scenarios |
|---|---|---|
| Derived authorization revocation | Mint actual Library/mail/attachment grants, end the parent, attempt new reads; other browser grants remain valid. | C-1 six causes; CRIT-001. |
| Effective-config preservation | Ancestor replacement plus mixed request, saved/live state unchanged. | D-12; CRIT-002. |
| Concurrent partial mutation | Force the stale-snapshot interleaving through real handlers, both orders. | FR-019; MAJ-002. |
| Persisted versus applied versus unknown | Disk write succeeds then refresh fails; response lost after commit; read-back and Discard semantics. | FR-015; MAJ-005. |
| Generated read-contract validation | Out-of-range stored values must reach the real repair UI. | FR-046; MAJ-003. |
| Default-off inbound validation | Retired fields and strict new retention body rejected regardless of flag. | FR-042/045; MAJ-004. |
| Restart and focus | Failed hot apply + Later; direct changed port; stable proxy; stale old health response. | G-10–12; MAJ-006/007. |
| Navigation and partial-read recovery | Router/hash exits separately from document unload; local failed reads do not destroy unrelated drafts. | G-06/G-25; MAJ-008/009. |
| Migration persistence | Migration → normal config save → new sign-in → restart → old cookie; failure path and stable expiry. | G-21; MAJ-015. |
| End-versus-admit concurrency | Paused authentication/registration/mint resumed after revocation; expired idle sockets. | FR-084; MAJ-017 warning. |
| Sweep instrument | Actual retro sweep plus switch-off deletion positive control; independent chat/legacy-switch results. | G-14; MAJ-014. |
| Recall instrument | If FQ-1 A: actual recall returns distinct 400-day/730-day/older-forever content; no test-only stub returning the fixture. | FR-050. |
| Riders and tool parity | Each chief-confirmed rider gets an acceptance/test/docs row; no incidental issue closure from a moved component. | MAJ-013. |

### Dataset gaps and oracle corrections

| Dataset | Add or correct |
|---|---|
| D-1 / D-2 | Distinguish request validation from out-of-range persisted read state; test null/wrong type/unknown/read-only inputs with validation off as well as on. Keep chat 1/365 and memory 1/730 independently asserted. |
| D-5 | Crossed sign-in/use order across restart; use concurrent with eviction; tie after both timestamps match must have an explicit deterministic choice if identity of the evicted browser is asserted. |
| D-6 | Valid-id/wrong-secret and malformed cookie; expiry while authenticating; idle socket deadline with no new HTTP request; legacy save/restart must not renew missing-time migration repeatedly. |
| D-7 / auth matrix | Cookie versus user bearer versus CLI/dev/env; missing/mismatched CSRF; mixed credentials; new and legacy cookies with cross-site Fetch Metadata. Same 404 body for the four specified target-id classes. |
| D-8 / D-11 | Real save planner and network request counts, not a duplicated map tested against itself. Local success: one token/one protected request; platform: zero token requests; Cancel: zero writes; late 409: no other writes before renewed consent. |
| D-10 | Split valid rows from invalid outline; replace the false 512 refusal with a declared failure setup. |
| D-12 | `storage:null`, non-object parents, key casing, dotted spellings, and a benign sibling in the same refusal. Verify both bytes and effective values, not only error status. |
| Planted-cookie fixtures | Same-id different secrets in both orders, multiple valid candidates, bounded budget exhaustion, original expiry after verification, cross-site no-resend. |

**Instrument discipline for implementation:** use a controlled clock for expiry/sweeps and synchronization barriers for races, not sleeps as the sole oracle. Prove the instrument can fail: the switch-off sweep deletes the old fixture; an injected second protected endpoint fails the B4 guard; removing the effective-subtree check fails the parent-replacement case. Save the permitted narrow RED run before GREEN. Full CI and exact-cut UAT remain later gates; this review produces neither.

## STRIDE Threat Summary

S = impersonation; T = unauthorized modification; R = missing accountability; I = information disclosure; D = resource exhaustion; E = excess privilege. `covered` means the spec has an explicit mitigation, **not an executed security PASS**; `gap` means correction is required; `n/a` means no additional finding in this scope.

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Browser identity / expiry / migration | gap | covered | covered | covered | gap | gap | Preserve durable legacy recognition and bounded duplicate recovery; 24-hour server check explicit. |
| Revocation and derived grants | gap | gap | covered | gap | n/a | gap | Cookie/socket revocation alone leaves preview read authority (CRIT-001); admission race warning. |
| New-cookie cross-site handling | gap | gap | n/a | gap | covered | gap | Preserve existing read defense and qualify CSRF by authenticated credential. |
| Retention writers | n/a | gap | n/a | n/a | n/a | gap | Parent replacement can remove retention protection; optional validation is not enforcement. |
| Protected section saves | covered | gap | covered | covered | covered | gap | Single-use token is appropriate; #936 side-door acceptance is incomplete. |
| Credential deletion | covered | decision | covered | n/a | decision | decision | FQ-2 is explicit informed founder acceptance, not approval inferred from this review. |

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| Is there a named user-facing surface? | Yes, specified—not implemented here. | Spec FR-001 and Reachability; `/workspace/wt/settings-1519/src/routes/_app/settings.tsx::SettingsRoute`. |
| Are new API routes and guards named? | Yes, planned. | C-1/C-3 and route-table test requirement. Registration is not claimed present at this spec-only commit. |
| Is a new agent tool needed? | No new tool is commissioned; an existing tool's restrictions do change under #936. | `/workspace/wt/settings-1519/pkg/sysagent/tools/config.go::ConfigSetTool`; MAJ-013. |
| Can moved controls be reached? | Conditional; prerequisite must be corrected. | S-Q3 A versus FR-060, MAJ-010. |
| Does the plan require execution? | Yes; two rows are not yet executable as written. | TDD/UAT table; G-07/G-14; MAJ-012/014. |
| Are public documentation changes planned? | Yes. | DOC-001–011; current public settings, memory, security and tools pages read. Extend them for corrected semantics and chief-confirmed riders. |

**Code correct and tested:** not established—specification-only review; no tests run.  
**Reachable by a user/agent:** future wiring is specified, not demonstrated on a running implementation.

## Unasked Questions for the Author / Chief

These are engineering/scope clarifications to resolve in the correction, not requests for the founder to choose implementation details.

1. What canonical browser identity binds cookie resolution, socket registration, preview grants and audit entries, and what makes revocation authoritative after a saved-config/live-refresh failure?
2. How is legacy-cookie eligibility durably represented under the exclusive five-field record, and when are migration id/issue time persisted exactly once?
3. What is the complete section save plan, including protected-first ordering for a late 409, endpoint-specific persistence acknowledgements and the Third-party agents contract amendment?
4. Which reads are essential to each edit group, and where does failed hot-apply state live so it survives Later, a route change and reload?
5. Which riders are confirmed, who owns each, and which are explicitly deferred? Resolve #936's tool matrix, #1146's message/location behavior, #1265's pending product decision and #598's test-isolation mechanism. Do not count existing partial fixes as complete without evidence.

### Source clarifications that should not become new design work

- Password change's bearer behavior is **not Unknown** at the reviewed commit: `/workspace/wt/settings-1519/pkg/gateway/rest_auth.go::HandleChangePassword` clears `tokens`, `token_hash` and `session_token_hash`. Preserve this separately from the rule that ordinary browser End/sign-out-others does not revoke app tokens.
- Closing the live-browser connection already runs viewer/WebRTC cleanup: `/workspace/wt/settings-1519/pkg/gateway/browser_ws.go::readLoop` unconditionally calls `detachWebRTCViewer`. There is no verified standalone “closing signaling leaves video alive” finding here; test that the new revocation path reaches existing cleanup, including in-flight attachment.
- Invalid Save already has an approved `aria-disabled`, focus-on-activation pattern in `/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/review-response.md::BC-D1`. Preserve it; do not invent a new control.
- `Item` is not in the current catalog, while all 32 components named by the spec are. The Card-row fallback is justified; no new kit component is requested.

## Questions for the Founder

This separate list is for **team-lead's interview before the round-1 correction**, not an answer assumed by the reviewer. Existing S-Q13 A (90/180 defaults) and S-Q14 A (used longest ago) are not reopened without explaining the newly exposed trade-off.

### Q1 — Should kept memory also remain recallable? (spec FQ-1)

The current search reads at most 365 days. Keeping reflections for 730 days or forever does not by itself let an agent find the older ones.

- **A — Recommended:** recall all retained reflections, with an implementation that keeps work bounded without silently excluding old history; verify the actual recall tool path.
- **B:** retain the 365-day recall limit and state that limitation beside retention and in documentation.

**Answer:** `Q1 A` or `Q1 B`. Evidence: `/workspace/wt/settings-1519/pkg/agent/memory_adapter.go::MemoryStoreAdapter.SearchRetros` and `/workspace/wt/settings-1519/pkg/agent/memory.go::MemoryStore.ReadRetros`.

### Q2 — Confirm the security consequence of confirm-only key deletion (spec FQ-2)

S-Q11 B chose confirmation without another password. The current server password gate was deliberately added to prevent someone at an unlocked signed-in screen deleting live provider/channel keys; removing only the UI prompt is insufficient.

- **A — Recommended to preserve your recorded choice, if this consequence is accepted:** confirm-only deletion, authenticated/admin/bypass protections retained and audited; amend the accepted decision and public docs.
- **B:** retain password confirmation for deletion as well as creation/rotation.

**Answer:** `Q2 A` or `Q2 B`. Evidence: `/workspace/wt/settings-1519/docs/internal/architecture/ADR-022-credential-vault-reauth.md::Decision`, **“Re-auth Consent Gate: Credential-Vault Scope Expansion to v0.1.0”**, and spec C-5. No security change has been made by this review.

### Q3 — Keep the 24-hour lifetime here? (spec FQ-3)

The wireframe's multi-day row cannot exist under the current 24-hour lifetime. Lengthening sign-ins changes security and scope; correcting sample data does not.

- **A — Recommended:** keep 24 hours in this package and correct the sample.
- **B:** keep this package's lifetime unchanged, but commission a separately scoped follow-up to consider longer sign-ins.

**Answer:** `Q3 A` or `Q3 B`. Evidence: `/workspace/wt/settings-1519/pkg/gateway/middleware/session_cookie.go::SessionCookieMaxAge`; spec FR-079 and Scope Boundaries. Neither answer authorizes silently extending this package's expiry.

### Q4 — Must “used longest ago” remain true after a gateway restart?

The spec avoids request-time disk writes by forgetting last-use times on restart. That can evict a recently used older sign-in rather than an unused newer one (MAJ-016). This is a behavior trade-off, not just where a timestamp is stored.

- **A — Recommended, consistent with S-Q14 A:** preserve enough durable recency information to honor the selected eviction rule across restart; architect specifies the bounded persistence strategy and its evidence before implementation.
- **B:** explicitly accept that eviction temporarily falls back to sign-in age after restart, and disclose that limit in the UI/docs/tests.

**Answer:** `Q4 A` or `Q4 B`. Evidence: spec FR-078 versus FR-091. Do not treat the current in-memory choice as founder approval of B.

### Q5 — Is the previously deferred “Clear sessions” decision carried now?

Issue #1265 was explicitly deferred and still contains a decision about visibility and re-confirmation. Calling it a rider and keeping one generic confirm does not resolve that decision.

- **A — Recommended for a bounded correction:** keep the current action's behavior unchanged in this redesign, state that #1265 is not resolved, and have the chief retain its explicit deferral/ownership while correcting misleading chat-type wording as authorized.
- **B:** carry #1265 fully now; team-lead interviews the visibility, re-confirmation and exact deletion-message choices and adds their acceptance/tests/docs before implementation.

**Answer:** `Q5 A` or `Q5 B`. Evidence: `gh issue view 1265 --repo elicify-ai/omnipus --json number,title,state,body` returned OPEN and the founder's 2026-10-11 “deal with this later” decision; exit 0. This review does not close or rescope the issue itself.

## Verdict Rationale

**BLOCK.** CRIT-001 leaves active read authority after a promised sign-out; CRIT-002 defeats the single-writer retention boundary and can remove deletion protection. Both are source-verified design omissions, independent of MAJ-017's unverified future race. The save/contract contradictions, failed-apply state, restart recovery and invalid test oracles must also be corrected before the spec is implementation-ready.

The report does not reject the redesign's goals or require a new service, database, multi-use consent token or new UI kit. The shortest correct path is to close the actual boundaries, make the UI reflect persisted and running state truthfully, preserve the approved dependency order, and give the test author independent, executable expectations.

### Recommended Next Actions

1. Team-lead interviews the founder on Q1–Q5, and confirms the rider ownership/dispositions with the chief.
2. The author makes the round-1 correction: revocation closure and retention mutation protection first, then save/read contracts, restart/navigation states, migration/recency semantics and the test oracles. Backend-lead later owns actual contract edits and regeneration; this review changes none.
3. Run the fixed second spec grill on the corrected artifact. Do not treat this review, catalog checks, or a future mock-only green as implementation/UAT evidence.

### Next step in the process

Verdict: **BLOCK**

Review written to: `/workspace/wt/settings-1519/docs/internal/specs/settings-redesign-spec-review.md`

This is grill round 1 of 2 (fixed). Next: team-lead interviews the founder on "Questions for the founder", then the spec author fixes round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the corrected spec at `/workspace/wt/settings-1519/docs/internal/specs/settings-redesign-spec.md` — regardless of this round's verdict.

skills: grill-spec, omnipus-shared-rules, ux-heuristics-review; additional reference read: omnipus-design-system.

## Evidence Table

| Claim | Evidence | Certainty |
|---|---|---|
| Reviewed immutable input | `git rev-parse HEAD` → `1d8e31042cec126f098c4ab9e53a12fb2ffdb985`, exit 0; `git diff --name-status c2f8f8e735..1d8e31042c` → only the added specification, exit 0. | Verified |
| CRIT-001: browser termination must include preview authority | `/workspace/wt/settings-1519/pkg/gateway/preview_token.go::PreviewSessionKey`, `PreviewTokenStore.Lookup`; `/workspace/wt/settings-1519/pkg/gateway/rest_auth.go::HandleLogout`; public logout-revocation promise read. | Verified |
| CRIT-002: a leaf block misses parent replacement | `/workspace/wt/settings-1519/pkg/gateway/blocked_paths.go::collectPaths`; `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::updateConfig`; both common mutation-wrapper paths read. | Verified by source; deletion not executed |
| MAJ-001: Third-party agents is replacement, not partial | `/workspace/wt/settings-1519/contracts/components/schemas/ThirdPartyAgentSettings.yaml::description`; `/workspace/wt/settings-1519/pkg/gateway/rest_third_party_settings.go::thirdPartyRowsFromBody`. | Verified |
| MAJ-002: context writer persists stale untouched fields | `/workspace/wt/settings-1519/pkg/gateway/rest_context_settings.go::putContextSettings`. | Verified by source; race not run |
| MAJ-003: bounded read schema contradicts repair UI | Reviewed spec C-3/FR-046; `/workspace/wt/settings-1519/src/lib/api/http.ts::performRequest`. | Verified |
| MAJ-004: optional validation does not enforce old-field rejection | `/workspace/wt/settings-1519/pkg/gateway/rest_inbound_validate.go::decodeAndValidate`; `/workspace/wt/settings-1519/pkg/gateway/rest_memory_settings.go::putMemorySettings`. | Verified |
| MAJ-005: persistence can precede error | `/workspace/wt/settings-1519/pkg/gateway/rest_config.go::configRefreshError`; `/workspace/wt/settings-1519/pkg/gateway/rest_third_party_settings.go::putThirdPartyAgentSettings`. | Verified |
| MAJ-006: failed hot reload has no static pending entry | `/workspace/wt/settings-1519/pkg/gateway/rest_prompt_guard.go::putPromptGuard`; `/workspace/wt/settings-1519/pkg/gateway/rest_pending_restart.go::RestartGatedKeys`; spec FR-034–038. | Verified |
| MAJ-007: current poll proves only old-origin liveness | `/workspace/wt/settings-1519/src/components/settings/GatewayRestartModal.tsx::pollUntilOnline`; G-10. | Verified |
| MAJ-008: hash and document exits need distinct rules | `/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/review-response.md::BC-D5`; `/workspace/wt/settings-1519/src/components/library/preview/unsavedGuard.ts::beforeunload listener`; FR-014. | Verified |
| MAJ-009: partial-read rules conflict | Reviewed spec UI Screens and States versus FR-066. | Verified |
| MAJ-010: two-order landing contradicts founder answer | `/workspace/omnipus-coordination/handoffs/ui-redesign/settings-wireframe/questions.md::S-Q3 — Order of work with Skills & Tools`; FR-060 and Assumptions. | Verified |
| MAJ-011: recovery verifies multiple candidates within its own budget | `/workspace/wt/settings-1519/pkg/gateway/middleware/planted_cookie.go::genuineSessionResend`, `plantedResendMaxComparisons`. | Verified |
| MAJ-012: test oracles conflict with declared/current behavior | `/workspace/wt/settings-1519/pkg/gateway/rest_context_settings.go::validateContextSettingsUpdate`; spec D-10, SC-003, FR-031/032. | Verified |
| MAJ-013: rider acceptance exceeds one-line mappings | `gh issue view <id> --repo elicify-ai/omnipus --json number,title,state,body`, ids 936, 1265, 1146, 598: each exit 0, each OPEN; `/workspace/wt/settings-1519/pkg/sysagent/tools/config.go::performanceOnlyConfigKeys`. | Verified; chief confirmation Unknown |
| MAJ-014: retrospective sweep is independent | `/workspace/wt/settings-1519/pkg/gateway/retention_goroutine.go::executeRetroSweepTick`; G-14's unresolved trigger. | Verified |
| MAJ-015: durable legacy discrimination is unspecified | Reviewed spec C-1 Storage/Migration and FR-076/085. | Verified specification gap |
| MAJ-016: reset changes eviction order | Reviewed spec FR-078/091 and restart scenario; crossed-timestamp example in finding. | Verified logical contradiction |
| MAJ-017: revoke/admit race needs verification | `/workspace/wt/settings-1519/pkg/gateway/websocket.go::WSHandler.ServeHTTP`, `authenticateWS`; spec tests start with already-open sockets. | Inferred; WARNING |
| MAJ-018: cross-site read guard is an existing requirement to preserve | `/workspace/wt/settings-1519/pkg/gateway/middleware/session_cookie.go::IsCrossSiteSubresourceRequest`, `ResolveUserFromCookie`; `/workspace/wt/settings-1519/pkg/gateway/middleware/csrf.go::CSRFMiddleware`; spec T5. | Verified coverage gap |
| MIN-001/002: success visibility and vocabulary conflict | Reviewed spec FR-011/018 and FR-074/090. | Verified |
| MIN-003: frontend gates omitted from feature plan | Reviewed spec Test-Driven Development Plan; `/workspace/wt/settings-1519/package.json::scripts`; design-system skill §13. | Verified |
| Catalog and traceability instruments could see named entries | Read-only `python3 -I` catalog/FR inspection, exit 0: `positive control Button: True`; `requested catalog exports: 32 missing: []`; `FR declarations: 83 trace rows: 83 missing: []`; `Item present: False`. | Verified structural checks only |
| Current bearer-password and memory-recall facts | `/workspace/wt/settings-1519/pkg/gateway/rest_auth.go::HandleChangePassword`; `/workspace/wt/settings-1519/pkg/agent/memory_adapter.go::MemoryStoreAdapter.SearchRetros`; `/workspace/wt/settings-1519/pkg/agent/memory.go::MemoryStore.ReadRetros`. | Verified |
| No product execution evidence is claimed | Review used source/contract/document reads, read-only git/issue queries and structural text checks; no build/test/browser execution. | Verified scope of this review |
| **Self-check** | Re-read the composed report against the commissioned round-1 format, all twelve lenses, special focus and one-file boundary. Read-only `python3 -I` artifact/scope check: exit 0; `Report structure: 23 unique findings; all seven required fields and mandatory sections present.` Negative control detected a removed Evidence field; corrected `.tsx` matcher retained the full extension; cited path/identifier checks passed. `HEAD and all tracked files unchanged; only authorized report untracked.` The initial check's symbol typo and path-matcher defect were corrected before this passing check; no product test result is claimed. | Verified |
