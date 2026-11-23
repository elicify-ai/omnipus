# Adversarial Review: Skills & Tools redesign — final round

**Document reviewed**: `/workspace/wt/skills-tools-1519f/docs/internal/specs/skills-tools-redesign-spec.md`
**Mode**: SPEC
**Round**: Round 2 of 2 (fixed, final)
**Review date**: 2026-11-21
**Review commit**: `b304736f72340bb079933facc5ae046af6155b24`
**Production baseline**: `4e34afa4160fdb75a42cab57a6fa5628d030c64a`
**Prior review**: `/workspace/wt/skills-tools-1519f/docs/internal/specs/skills-tools-redesign-spec-review.md`, reviewing `5028ff7b0162497e653c0eaaf3ae853c94582fda`
**Work package**: #1963, Skills & Tools page F, under #1519
**Settled landing order**: Settings first, then Skills & Tools. The current commission and issue #1963 supersede the old order in both specifications.
**Verdict**: **BLOCK**

## Executive Summary

The corrected specification has **2 CRITICAL and 8 MAJOR findings**: its restart procedure can lose the original permission values, and its proposed folder gate does not cover mounted project skills. Eleven round-one findings are closed at specification level; nine are partly open. The new combined approval actions, Settings-first hand-off, save outcomes and acceptance oracles need correction before implementation; frontend contracts, journeys, keyboard behavior and component reuse received the same source-level scrutiny as backend authorization and persistence.

| Severity | Count |
|---|---:|
| CRITICAL | 2 |
| MAJOR | 8 |
| MINOR | 0 |
| OBSERVATION | 0 |
| **Total findings** | **10** |

Coordination warnings are stated separately, not counted as verified product defects. There was no build, product test, browser session, paid model call or runtime exploit.

### Scope and evidence limits

- This is the same independent review continued from round one, not a new review round or an implementation gate.
- GitNexus is unavailable, as stated in the commission. Exploration used `rg`, direct reads and pinned Git objects. Impact conclusions are **Inferred**, not graph results.
- References to C-1–C-22, FR, S, ST, D and DOC below mean sections of the reviewed specification. **Verified** means the cited text or source was inspected; future runtime consequences are explicitly **Inferred** unless tested. No future implementation is represented as existing code.
- The production trees are unchanged between the production baseline and this review commit: the read-only comparison of the backend, contracts and frontend returned exit 0. Between the first and second review commits, only the specification changed.
- Settings was inspected at `8e5848c951f901ba4218ef20b184c4055ecbc8b5`, not assumed to have landed. Its specification was read from that Git object; it is not present in this worktree's HEAD. The hand-off's older pin, `a2739dfccdc5b9edf35fe6fb08c20db16babb2b4`, is not a substitute for the inspected implementation.
- Only this report is an authorized new file. The specification, prior report, production code, contracts, tests, issues and Git history are not edited.

## Round-one disposition — all twenty findings

“Closed” means the original specification gap now has an adequate stated rule, not that its implementation passed. New contradictory tests or integration requirements remain findings below.

| Round-one ID | Disposition | Corrected-spec evidence and checked source facts | Remaining work |
|---|---|---|---|
| CRIT-001 — Full before consent | **Closed** | C-11 E8 and C-14 admit only `allowed`; `ask_pending` gets a description, not instructions. S-89/ST-13 require an outbound sentinel check. | Consent precedence still needs R2-MAJ-001; do not restore unconditional Full injection. |
| CRIT-002 — file/helper bypass | **Partly open** | C-11 E6/E7, S-85–S-87 and ST-19 commission folder gating and preserve the editor exception. `/workspace/wt/skills-tools-1519f/pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority` and `/workspace/wt/skills-tools-1519f/pkg/fspolicy/homeguard.go::inventoryDenies` cover the installed shelf, not every project mount. | R2-CRIT-002. |
| CRIT-003 — partial save replaces ceiling | **Closed** | C-2 creates a distinct per-key PATCH, validates the whole request and preserves unnamed keys; S-82/D6 prove different-key concurrency. The replacement requirement in `/workspace/wt/skills-tools-1519f/contracts/components/schemas/GlobalToolPolicies.yaml::required` is no longer imposed on this page's mutation. | Saved-state and queue details: R2-MAJ-004. |
| CRIT-004 — false “uncovered” MCP test | **Closed** | C-18 checks resolved global coverage, preserves broader wildcards and exact keys, names both changed states, and writes no agent entries. This matches `/workspace/wt/skills-tools-1519f/pkg/tools/compositor.go::resolveFromMap` and `mergeStoredPolicyLayers`. | Repair D8's scenario parameterization under R2-MAJ-008; do not revert to exact-server-key absence. |
| MAJ-001 — incoherent consent | **Partly open** | C-12 defines revision-bound trust and session grants, stale-answer checks and a combined card. The table nevertheless conflicts with itself and with its first-use/unattended promises. `/workspace/wt/skills-tools-1519f/pkg/agent/loop_run_turn_tools.go::resolveAskPolicy` settles Auto/grants before unattended refusal. | R2-MAJ-001 and R2-MAJ-002. |
| MAJ-002 — no pre-install facts | **Partly open** | C-13 adds staging, authoritative verification/category/revision, expiry and exact-content publication. It still derives a terminal install result from a list read without specifying how completion is established. | R2-MAJ-006. |
| MAJ-003 — project identity/migration | **Partly open** | C-19 defines project keys, lifecycle and existing-skill migration; `/workspace/wt/skills-tools-1519f/pkg/skills/shelf.go::ResolveSkillName` confirms the two shelves and grant-sensitive precedence. Restart and writer contracts remain inconsistent. | R2-CRIT-001 and R2-MAJ-007. |
| MAJ-004 — visibility migration | **Partly open** | C-6/D7 now account for compression, preview, discovery, deferred registration and offline server defaults. `/workspace/wt/skills-tools-1519f/pkg/agent/tool_manifest.go::stripInfraToolDefs` still disproves the blanket uncompressed request-equality promise. | R2-MAJ-005. |
| MAJ-005 — live visibility/cache/search | **Closed** | C-15 specifies one authority, next-turn application, loaded-definition retention, Deny revocation and menu-generation invalidation; S-91/S-92/D11 cover demotion. These address the dependencies in `/workspace/wt/skills-tools-1519f/pkg/agent/prompt_cache_sessions.go::restoreLoadedToolRetention`, `pruneLoadedTools`, `freezeSessionSkillMenu`, and `/workspace/wt/skills-tools-1519f/pkg/tools/registry.go::SnapshotSearchableTools`. | Implementation must prove all three consumers use that authority. |
| MAJ-006 — no Full capacity outcome | **Closed** | C-14/FR-069/FR-079 now define charging, no-send, visible recovery, growth/model-change rechecks and a page warning. | The new D9 boundary is wrong under `/workspace/wt/skills-tools-1519f/pkg/agent/context_budget.go::contextBudget`; fix it under R2-MAJ-008. |
| MAJ-007 — Settings compatibility | **Partly open** | C-22's bidirectional legacy trust mapping fixes the enum mismatch; C-21 commissions retired-key refusals. The entire dependency still assumes the now-superseded order. Current Settings C-7/FR-100/FR-101 already own the compressed-key guard. | R2-MAJ-003. |
| MAJ-008 — create/authoring/preservation parity | **Partly open** | C-21 now names REST create/update, `create_agent`, `set_agent_permissions`, preservation, copy/category and completed W5 tests. `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_agents_update.go::updateToolsConfig` is explicitly in scope. C-4's installed-only validator still contradicts project-key support. | R2-MAJ-007; approval-only human mutations also need R2-MAJ-002. |
| MAJ-009 — autosave truthfulness | **Partly open** | C-16 adds a navigation-surviving queue and saved/not-applied/refused/unknown states. It still imposes incompatible dispatch/order requirements and mislabels late failures. | R2-MAJ-004. This is not the disproved allegation that the existing autosave hook drops all unmount edits. |
| MAJ-010 — bulk versus always-ask | **Closed** | C-20 specifies per-action eligibility, skipped rows, accurate counts and retained server guards; S-84/D4 cover it. `/workspace/wt/skills-tools-1519f/pkg/tools/always_ask.go::AlwaysAskAllowViolation` remains required. | Fix the contradictory Omnipus UI test under R2-MAJ-008. |
| MAJ-011 — no-confirm versus plan warning | **Closed** | ST-Q7 A explicitly preserves only the autonomous-plan warning. C-20, FR-010, S-05 and the prohibitions agree on that exception. | No new founder question. |
| MAJ-012 — missing per-key tool audit | **Closed** | C-2/FR-040/S-99/ST-23 specify deltas for every map, actor, no-op/refusal behavior, computation inside the write lock, and the legacy PUT. Audit failure is explicitly nonblocking and documented by DOC-004. | This is a specification closure, not proof an audit entry has been written. |
| MAJ-013 — filtered focus loss | **Closed** | Accessibility and Keyboard, S-100 and D4 define next/previous row, empty-group/list destinations, failed-save behavior and announcements. | Execute keyboard/narrow-width checks at implementation; no rendered-UI result claimed. |
| MAJ-014 — false-green oracles | **Partly open** | ST-06/ST-13 now capture requests; ST-15 completes authoring; ST-09 covers REST separately; audit assertions and mutation probes are named. New circular and contradictory rows remain. | R2-MAJ-008 plus the scenario-specific requirements in the other findings. |
| MAJ-015 — unified discovery omitted | **Closed** | ST-Q6 A, C-17, FR-073, S-96 and DOC-012 restore ToolSearch skill discovery, including Hidden, without loading or granting. | No waiver or scope question remains. |
| MIN-001 — radio group used as tabs | **Closed** | The component and accessibility sections select the catalogued Tabs primitive. `/workspace/wt/skills-tools-1519f/src/components/ui/tabs.tsx::Tabs/TabsList/TabsTrigger/TabsContent` wraps Radix Tabs, not a radio group. | No new component required. |

## Findings

### CRITICAL Findings

#### R2-CRIT-001 — The migration removes its restart source before completion

- **Severity**: CRITICAL.
- **Lens**: Incompleteness; infeasibility; security; persistence.
- **Affected section**: C-19 upgrade order, C-2 self-heal, FR-019/FR-075, S-16, Qualitative Prohibitions.
- **Failure scenario**: An install has global `Skill: deny`. Steps 2–4 write per-skill Deny and replace the old master with fixed Allow. The process stops before the completion marker. The required restart reruns the migration “from the stored old values,” but the original Deny is gone. Repeating the stated assignment can copy the replacement Allow over the migrated Deny. A stop between individual agent-record writes likewise leaves a mixed migration that a single config-file write cannot resolve.
- **Evidence**: C-19 removes old global/per-agent entries in step 4 but marks the whole sequence complete only after later steps. It claims steps 2–3 share an “atomic config write.” `/workspace/wt/skills-tools-1519f/pkg/agentstore/store.go::RecordsDir` stores agents separately under the entity store; `/workspace/wt/skills-tools-1519f/pkg/entity/store.go::write` atomically writes one entity file. **Entity / config separation — per-entity files for agents**, `/workspace/wt/skills-tools-1519f/docs/internal/architecture/ADR-054-entity-config-separation.md`, is the existing storage decision. The general prohibition on writing *any* agent policy entry also contradicts C-19's required translation of explicit old overrides.
- **Recommendation**: Specify a durable recovery protocol over the global file and agent records: retain an immutable original snapshot or equivalent recoverable progress until all migrated records and the completion state are durable; make each replay safe; define when the migrated policy becomes active and what happens on a failed agent write. Do not infer the old value from fixed Allow or overwrite an already migrated key on replay. Narrow the prohibition to synthetic backfill, not translation of existing explicit intent. Add crash/failure fixtures after every persistent boundary, including removal of the master and each agent write, plus repeated-start assertions preserving Deny and Ask. Keep the old/new-skill distinction explicit: migration preserves existing values; later user/agent-added skills start Ask; later built-ins follow ST-Q10 A.
- **Certainty**: **Verified, high confidence** for the destructive-order and multi-file contradiction. Permission weakening on the described replay is **Inferred, high confidence**, not an executed incident.

#### R2-CRIT-002 — Filtering installed folders does not gate mounted project skills

- **Severity**: CRITICAL.
- **Lens**: Security; incompleteness; degradation; ecosystem compatibility.
- **Affected section**: C-11 E6/E7, C-19, FR-066/FR-082, S-85–S-87/S-105, ST-19/ST-25, SC-006.
- **Failure scenario**: Mia has a mounted project containing `.claude/skills/notes/SKILL.md`, no trust approval, and `edit_skill` denied. A `Skill` load is refused or asks, but the ordinary mounted-path read remains allowed if the implementer only removes the folder from the installed-skill loadable set as C-11 directs. The instructions reach the model before the ST-Q8 trust decision; a helper under the same mount presents the corresponding execution gap.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority` calls `LoadableFolders` on the installed root only. `/workspace/wt/skills-tools-1519f/pkg/fspolicy/homeguard.go::homeGuardDeny` has no opinion outside Omnipus home; `inventoryDenies` invokes `skillPathDenied` only for the installed skills root. `/workspace/wt/skills-tools-1519f/pkg/sandbox/derive_from_fspolicy.go::DeriveKernelPolicy` separately grants the working directory and mount roots. `/workspace/wt/skills-tools-1519f/pkg/skills/shelf.go::ResolveSkillName` makes project authority depend on the mount. C-11's stated folder-set mechanism does not explain how it overrides that broader authority.
- **Recommendation**: Extend the authority contract to recognized project-skill paths, not just installed folders. Define path identity, nested-mount/workdir precedence, direct reads/searches and helper execution, and how permission/trust is checked before exposure on app-only and enforcing-kernel paths. Removing a narrow grant is not enough while a broader grant admits the same path. Preserve the explicit editor read exception without granting load/helper consent. Add real installed-versus-project fixtures with `edit_skill` denied, an ordinary file outside the skill as a positive control, and path-alias and next-turn-change cases. State the supported-platform/degradation behavior; do not promise an enforcing kernel boundary where none exists.
- **Certainty**: **Verified, high confidence** for the mismatch between the nominated mechanism and current mount authority. A bypass in an implementation limited to that mechanism is **Inferred, high confidence**; none was attempted.

### MAJOR Findings

#### R2-MAJ-001 — The consent table still gives different answers for the same state

- **Severity**: MAJOR.
- **Lens**: Ambiguity; inconsistency; UI journey; security.
- **Affected section**: C-12, US-2 AS3/AS8, FR-067/FR-081/FR-083, S-106–S-109, D10, ST-05/ST-16/ST-25, DOC-013.
- **Failure scenario**: A newly installed skill has global Ask and its trust revision was approved during installation. With Auto on, one row says its first load runs without a card; S-107 and the installation copy promise a first-use card. For an unattended run, D10 says “any Ask” is refused while FR-083 refuses only a remaining `ask_pending`. With an agent's own Ask and Auto on, the ordinary row runs automatically but the combined row says the full card appears. An agent installation at effective `install_skill: ask` can also produce an ordinary tool card followed by trust, contradicting “never two in a row” and ST-25's one-card expectation.
- **Evidence**: C-12 contains all of those rules. `/workspace/wt/skills-tools-1519f/pkg/tools/auto_approve.go` classifies `Skill` as AutoRuns and `install_skill` as AutoAsks. `/workspace/wt/skills-tools-1519f/pkg/agent/loop_run_turn_tools.go::resolveAskPolicy` checks automatic authorization and standing grants before `AutoDenyAsk`. `/workspace/logs/skills-tools-1519f/founder-questions-r1.md::ST-Q3` promised first-use asking; `/workspace/logs/skills-tools-1519f/st-questions-2/index.html::ST-Q9` corrected waiting, not the Auto distinction.
- **Recommendation**: Replace the overlapping prose with one executable decision table: global value, agent override, trust/verification/revision approval, Auto, God Mode, session grant, interactive/unattended, and entry point. Separate stored Ask from unresolved human consent. Use one meaning everywhere after the R2-Q2 founder clarification. Explicitly settle `install_skill`'s own permission and trust in the specified card count; do not drop a permission check to achieve one card. The existing pre-approval preparation seam in `/workspace/wt/skills-tools-1519f/pkg/agent/loop_run_turn_tools.go::requestAskApproval` can supply server-derived facts; the author must specify its use rather than assume a second prompt disappears. Give each row literal card actions, stored changes and load/refusal outcomes.
- **Certainty**: **Verified, high confidence** for contradictory requirements and existing execution order. Which interpretation would be implemented is **Unknown**; clarification is required, not guessed.

#### R2-MAJ-002 — The new approval choices lack a complete server contract and confirmed global scope

- **Severity**: MAJOR.
- **Lens**: Contract-first; security; UI control; overcomplexity.
- **Affected section**: C-9/C-12, API and Data, US-7 AS8, FR-083, S-106/S-109, test order 47, DOC-013.
- **Failure scenario**: A person selects “Approve this version and allow everywhere,” but the existing request can express only a one-call or session approval. Treating it as generic `allow` leaves scheduled runs blocked. A client-side global PATCH followed by approval can leave global Allow stored when trust persistence fails, or report success despite a stale card/current Deny. The More-actions “Approve this version” has no specified mutation contract at all. Separately, the person may intend one version's future use, while the architect's action permanently changes the installation's ordinary skill ceiling.
- **Evidence**: C-9 adds only name/display name/unverified metadata to `ToolApprovalRequiredFrame`, only for `Skill`. `/workspace/wt/skills-tools-1519f/contracts/components/schemas/ToolApprovalActionRequest.yaml::properties.action` and `/workspace/wt/skills-tools-1519f/contracts/components/schemas/ToolApprovalResponse.yaml::properties.action` enumerate `deny`, `allow_once`, `allow`, `cancel`; `allow` means a session grant. `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_tool_registry.go::HandleToolApprovals` implements those actions using the immutable pending entry, then records the session grant. A full-spec sweep found the frame amendment but no amendment naming either action/response schema. The founder's ST-Q9 option says “allow the skill once in a chat”; C-12's permanent global mutation is explicitly labelled the architect's reading in Notes for team-lead.
- **Recommendation**: Obtain R2-Q1, then specify the contract shape before implementation: distinct server-validated actions/capabilities, canonical skill key and immutable revision binding, acting identity, what the displayed choice authorizes, response fields reporting what was actually persisted/applied, stale/replayed/expired decisions, and retry/partial-failure behavior across trust, global policy, session grant and current load. Define the More-actions mutation too. Keep ordinary Approve Once and Always Allow unchanged in scope; never infer trust or global consent from them. A generated contract and real handler-to-scheduled-run test must cover the action, not just a button/unit resolver. Do not introduce an ad hoc parallel approval system to avoid extending the existing one.
- **Certainty**: **Verified, high confidence** for missing wire decisions and the scope difference. A two-layer bypass is **not established**: C-12 correctly describes a human global mutation and preserves stricter agent overrides and revision trust. Failures from an unspecified client/server composition are **Inferred**.

#### R2-MAJ-003 — The hand-off is written for the opposite landing order

- **Severity**: MAJOR.
- **Lens**: Cross-package inconsistency; reachability; contract ownership; UI journey.
- **Affected section**: Overview, C-22, Integration Boundaries → Settings, FR-065/FR-078, S-101/ST-24, Assumptions, Notes for team-lead rows 1–15.
- **Failure scenario**: Settings lands first with the loading switch and its user-only guard. Applying this specification's hand-off literally can remove Settings' C-7/FR-100/FR-101 coverage before the replacement lands, or have both teams wait for the other. Skills & Tools then targets the old Settings save implementation and tests a trust screen that the new Settings layout removes, while the intended new control may not yet exist. Removing the Performance field without updating its new save/reconciliation consumers leaves broken Settings saves.
- **Evidence**: The current commission and read-only issue #1963 say Skills & Tools lands **after** Settings. C-22 and FR-065 still say before. At the inspected Settings commit, `/workspace/wt/skills-tools-1519f/docs/internal/specs/settings-redesign-spec.md::C-7/FR-060/FR-100/FR-101` (Git object only) still assumes the old prerequisite; `/workspace/wt/skills-tools-1519f/src/components/settings/PerformanceSection.tsx::CONFIG_KEYS/PerformanceSection` still carries `tools_on_demand` in the new save model; `/workspace/wt/skills-tools-1519f/pkg/sysagent/tools/config.go::manifestCompressedConfigKey/validateConfigKey` already carries its refusal. `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_performance.go` still reads/writes the value and includes it in changed-field metadata.
- **Recommendation**: Rewrite the hand-off around the settled Settings-first baseline; do not ask the founder to choose the order again. State the intermediate reachable controls and exact endpoint ownership. Settings retains its compressed-key refusal and parity tests while it exposes the switch. Skills & Tools removes the post-Settings control and all generated/request/reconciliation consumers atomically with replacement levels and retired-key refusals. Update both specifications' dependency text through their owners. Retire or retain legacy endpoints based on actual remaining consumers, with one named owner; do not resurrect a retired API to satisfy S-101. Re-target links and acceptance checks to the post-Settings destinations. The mapping `deny ↔ block_unverified`, `ask ↔ warn_unverified`, `allow ↔ allow_all` is coherent if the legacy endpoint is deliberately retained; it does not solve ownership or order.
- **Certainty**: **Verified, high confidence** for the authority/spec/code mismatch. A guard gap or broken intermediate journey from blindly applying the hand-off is **Inferred**; no landing or live failure is claimed.

#### R2-MAJ-004 — Serial autosave cannot meet its dispatch deadline, and late errors lose their meaning

- **Severity**: MAJOR.
- **Lens**: UI status/recovery; concurrency; infeasibility; testability.
- **Affected section**: C-16, Performance Bounds, UI Screens and States, D6 row 15, D12 rows 5–6, FR-007/FR-071.
- **Failure scenario**: Request A takes five seconds; a second click occurs after 100 ms. The serial queue cannot start B within 1,000 ms while also waiting for A. D12 instead starts B before A's answer. After navigation, a 503 means the value was stored but not applied, yet the mandated toast says “was not saved”; a lost response is likewise not proof of refusal. A same-value retry after 503 can also be rejected as “Nothing to change” if C-2's generic wording is implemented literally, rather than D6's 200/no-audit rule.
- **Evidence**: C-16 requires one request at a time in click order and a start within 1,000 ms for every click. D12 row 5 requires overlap. C-16 and the after-leaving state use one “not saved” toast for late failures despite the separate 503/unknown states. C-2 and Machine-Verifiable Constraints say no changed key → 400, while D6 row 15 says a same-value request → 200. These are specification contradictions; no hook behavior is needed to prove them.
- **Recommendation**: Keep one clear ordering model. For a serial queue, bound admission/visible Saving feedback, not dispatch behind an unresolved predecessor; specify how later changes behave after refusal, failed reconciliation or an unknown prior outcome. Keep queued versus acknowledged values distinct. Carry the classified saved-not-applied/refused/unknown result into global notifications and recovery. Specify persistence failures and failed follow-up GETs, not only 4xx/503. Distinguish an empty delta request from an idempotent same-value retry; the latter must be able to retry application without duplicate change audit. Rewrite D12 to drive the actual queue with controlled responses.
- **Certainty**: **Verified, high confidence** for mutually incompatible timing/order/error requirements. The misleading UI outcomes are **Inferred**, not browser-tested.

#### R2-MAJ-005 — The conversion still promises request equality for a known changed surface

- **Severity**: MAJOR.
- **Lens**: Migration; inconsistency with code; testability.
- **Affected section**: C-6, D7 row 9, S-41/S-44, ST-06, SC-003, DOC-014.
- **Failure scenario**: An existing install has compression off. Its ordinary request omits ToolSearch; after conversion, the new locked-Full ToolSearch appears. SC-003 permits no such difference, so either the implementation violates the equality test or the test silently ignores a change to what the model sees. Capturing only a goal-narrowed first request can hide this and other migration errors entirely.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/agent/tool_manifest.go::stripInfraToolDefs` removes infra tools; `/workspace/wt/skills-tools-1519f/pkg/tools/manifest.go::infraManifestToolNames` contains ToolSearch. `/workspace/wt/skills-tools-1519f/pkg/agent/loop_run_turn.go::prepareToolSurface` uses stripping when compression is off, but bypasses the normal surface entirely for a goal-narrowed request. C-6 locks ToolSearch Full and SC-003 lists other exceptions, not this one.
- **Recommendation**: State the exact preserved contract and enumerate the infrastructure advertisement difference instead of claiming literal equality. Carry the already-required locked discovery affordance transparently; do not silently reset ordinary tool levels or permissions. Add a literal ToolSearch old/new row, capture both any goal-narrowed request and the following normal request, and assert discoverability/callability separately from metadata. Retain the corrected MCP/offline/preview matrix and the approved trust/new-skill exceptions. This is not a reason to reopen ST-Q4 B or keep the removed switch.
- **Certainty**: **Verified, high confidence** for the old pipeline versus new requirement. No provider request was executed during this review.

#### R2-MAJ-006 — A skills-list read cannot prove a lost install request has finished

- **Severity**: MAJOR.
- **Lens**: Contract-first; asynchronous outcomes; UI status/recovery.
- **Affected section**: C-13 Cancel/lost-answer rule, FR-068, install-review UI state, S-94/S-95.
- **Failure scenario**: The install response is lost while publication is still running. An immediate list read finds no skill and the UI says “Not installed,” but publication then succeeds. For a replacement, the list can already contain the old version, so presence alone can falsely report the reviewed version installed. Deleting the staged review after publication does not itself distinguish success from expiration or cancellation on retry.
- **Evidence**: C-13 explicitly says re-read `GET /skills` and then show either Installed or Not installed, “never a guess.” It defines a staged revision and review lifetime but no operation-bound completion/readback/retry result that makes that binary conclusion sound. `SkillInstallReview.replaces_existing` explicitly includes the replacement case.
- **Recommendation**: Define how the client obtains a terminal outcome bound to the reviewed revision, including the still-running state and replacement identity. Reuse the review/operation identity where possible; a new general job system is unnecessary. Keep Unknown while completion cannot be proved, specify retry/idempotence and failed reconciliation reads, and distinguish installed-current-revision from an older existing skill. Add lost-before-publish, lost-after-publish, slow-publication, replacement, expired/consumed-review and repeated-submit fixtures. Cancellation before Install remains a valid separate case.
- **Certainty**: **Verified, high confidence** for the incomplete outcome contract. The race is **Inferred, high confidence**, not reproduced.

#### R2-MAJ-007 — Project skill permissions are supported and forbidden by different writers

- **Severity**: MAJOR.
- **Lens**: Contract-first; agent-tool parity; reachability.
- **Affected section**: C-4, C-19, C-21, Machine-Verifiable Constraints, S-70/S-102/S-103, D6 row 16.
- **Failure scenario**: A mounted project has key `project:w1/notes`. C-19 says its global and per-agent values can be changed through PATCH and `set_agent_permissions`, yet C-4 and the machine constraint reject any per-agent skill key that is not installed. One writer accepts the key and another refuses it, or developers quietly apply an installed-only validator to the only promised per-agent editing path. The C-21 “none” parity claim is then false.
- **Evidence**: C-4 says “a key not an installed skill → 400” and repeats that rule in Machine-Verifiable Constraints, while C-19/C-4's closing sentence define project keys and D6 accepts one. `/workspace/wt/skills-tools-1519f/pkg/skills/shelf.go::ResolveSkillName` confirms that project authority is not an installed-skill grant. The existing `/workspace/wt/skills-tools-1519f/contracts/components/schemas/AgentToolsUpdateRequest.yaml::required` also requires revision/override intent, and `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_tools.go::normalizeBuiltinPolicies` requires a builtin envelope; adding an optional `skills` field does not silently turn that endpoint into a partial PATCH.
- **Recommendation**: Define one canonical known-skill-key validation contract for installed and project shelves across REST create/update and agent create/update/read. State any deliberate difference and its clear refusal. Preserve the existing revision/override envelope unless explicitly redesigning it; make skill-only requests' accepted shape unambiguous. Specify omitted, empty, null, clear/remove, unknown key, workspace deletion and unrelated-write preservation. Extend the named parity tests to project keys and complete create/update/readback, not just an installed `plan` fixture.
- **Certainty**: **Verified, high confidence** for the incompatible key rules. The eventual inconsistent implementation is **Inferred**.

#### R2-MAJ-008 — Several new acceptance rows still lack an independent, executable oracle

- **Severity**: MAJOR.
- **Lens**: Testability; false-green risk; accessibility; contract consistency.
- **Affected section**: S-88/S-102/S-22, D9, test order 5/47, ST-08, S-109.
- **Failure scenario**: A test uses whichever shelf the implementation selects as its expected winner and passes after precedence is reversed. Another forces all D8 cases through God Mode off and the `github` service, never exercising the God/prefix rows it claims to cover. A budget test expects an over-budget request to send. UI acceptance requires clicking a disabled Ask badge, encouraging a test to bypass the real interaction. Unit tests of a new button and resolver can pass while its global write never reaches the server or scheduled run.
- **Evidence**: S-102's Then is “the skill that `ResolveSkillName` picks decides”; it does not specify the agent's installed grant. S-88 hard-codes God Mode off and `github`, but D8 includes God Mode on and service `git_hub`. D9 row 7 calls `pinned + Full + reserve = 8,000` a fit at context 8,000; `/workspace/wt/skills-tools-1519f/pkg/agent/context_budget.go::contextBudget` subtracts another 400 tokens of headroom, and `requestTokens` counts nonpinned messages/tool definitions. C-20 disables Omnipus Ask while S-22/ST-08 click it. Test order 5 still names `TestPutSkillTrust_RefusesLegacy`, although C-22 retains that endpoint's legacy enum and S-36 now targets the new PATCH. Test order 47 assigns S-105–S-109 only unit Go/TS coverage.
- **Recommendation**: Give S-102 literal granted-installed-wins and no-installed-grant-project-wins outcomes; never call the selection function to construct expected results. Parameterize every D8 dimension and assert each row ran. Express D9 fit/one-over against the reused full budget, including headroom and unavoidable request content, charged once. Split disabled-UI checks from direct-API 422 tests. Retarget the legacy-enum rejection test to PATCH while testing retained legacy mapping if applicable. Add real approval persistence/reload/scheduled-run integration and an exact-cut hands-on row for the new actions, including stale cards, stricter other agents, a changed unverified revision and failed persistence. Add mutation controls for these seams, not merely component text or model answers.
- **Certainty**: **Verified, high confidence** for contradictory/circular test definitions and the budget formula. No tests were run; this is not a reported test failure or pass.

## Focused rulings on the commissioned questions

| Topic | Ruling | Evidence / qualification |
|---|---|---|
| Can Allow everywhere make an agent less strict than the global policy? | **Not by its stated mechanism.** It changes the global value itself through a human action, then keeps the normal merge. It must not manufacture an agent Allow that defeats global Ask/Deny. | C-12; `/workspace/wt/skills-tools-1519f/pkg/tools/compositor.go::resolveEffectivePolicyWith/mergeStoredPolicyLayers`. Server enforcement of the new action remains unimplemented and underspecified: R2-MAJ-002. |
| Does it waive trust? | **It must not.** The combined/trust action records the current revision's approval; a later unverified revision needs another approval. A global Allow is not itself a trust approval. | C-12 Limits and ST-Q2 A. A failed trust write cannot be reported as a completed combined action. |
| Does “everywhere” include stricter agents? | **No.** Another agent's own Ask or Deny still applies. The acting agent's Ask hides the action; other agents' restrictions cannot be promised away. | C-12 Limits. The broad “every chat” sentence and UI explanation must state this limit. |
| Is the permanent global action a founder decision already made? | **Unknown.** The founder chose no waiting and future unattended usability, not that specific permanent ceiling mutation. | Supplied commission, Notes for team-lead, and `/workspace/logs/skills-tools-1519f/st-questions-2/index.html::ST-Q9`; R2-Q1 below. |
| ST-Q3 B versus existing skills | **New user/agent-added skills start Ask; existing skills are translated from the old master, not reset to Ask.** Built-ins introduced later start Allow under ST-Q10 A. | C-2/C-19/FR-020/S-108. Recovery must preserve that distinction: R2-CRIT-001. First-use Auto behavior remains R2-Q2. |
| ST-Q4 B conversion | **The corrected MCP mapping is substantially better, but this is not an entirely behavior-preserving upgrade.** | C-18 explicitly changes no-global/no-agent coverage from Deny to Ask, and **agent-only Allow to Ask** where global coverage is absent. The latter already had a permission; “no global coverage” must not be shortened to “never permitted.” Both are named SC-003 exceptions. R2-MAJ-005 identifies the additional, unlisted ToolSearch advertisement change. |
| C-22 legacy trust mapping | **The bidirectional enum mapping is coherent; the claimed lifecycle/consumer is not settled on the right baseline.** | C-3/C-22; existing `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_skill_trust.go::HandleSkillTrust`. Test both GET and PUT response values if retained. R2-MAJ-003 governs retirement ownership. |
| Settings C-7 / FR-100 / FR-101 | **Transfer ownership after Settings lands; do not delete its still-needed refusal coverage in advance.** | Inspected Settings pin, R2-MAJ-003. Settings-first is settled, not a new founder question. |

## Changed-field agent parity check

This table covers the changed properties and their lifecycle, not unrelated unchanged fields of the agent object. Paths/actions below are **commissioned by the spec**, not claims of current implementation. The implementation package must update tool schemas, descriptions and tests together.

| Property / action | User UI | User API | Agent create | Agent update / read | Deliberate difference or remaining gap | Parity test |
|---|---|---|---|---|---|---|
| Agent skill overrides: set / remove / clear | Agent Tools tab | C-4 agent create/update | `create_agent.skill_policy_changes` set | `set_agent_permissions`; `get_agent.skill_permissions` | Project-key validation must agree across paths; R2-MAJ-007 | `TestCreateAgent_SkillPolicyChangesParity`, `TestSetAgentPermissions_SkillPolicyChangesParity` |
| Preserve overrides during unrelated changes | No user action required | C-21 omission preserves | N/A | Tool-only changes preserve skill map | Keep complete existing envelope/revision semantics | `TestUpdateAgentToolsCfg_PreservesSkillPolicies` |
| Skill grants | Existing agent Skills tab | Existing agent create/update | `create_agent.skills` | `update_agent.skills` | Unchanged; project mount authority is not a separate grant | Existing grant behavior plus C-11/creation integration |
| Skill category at install/copy | Review / copy | Install/copy `category` | `install_skill.category`; `edit_skill` copy | Read listed skill | No category inserted into third-party content | `TestInstallSkillCategoryParity` |
| Choose category once after writing | More actions while Uncategorised | C-21 category PUT | W5 completes via `edit_skill` | `edit_skill.set_category`; refused after set | All paths share once-only rule | `TestEditSkill_SetCategoryOnce`, `TestWriteSkillWorkflow_PublishesCategorisedSkill` |
| Global tool permission | Global editor | C-2 PATCH | Refused | `set_config` refusal | Human-only ceiling; deliberate security difference | Existing sandbox refusal plus C-21 refusal rows |
| Global skill permission | Global editor; proposed Allow everywhere | C-2 PATCH; approval action incomplete | Refused | `set_config` refusal | Human-only, including chat action; R2-MAJ-002 | C-21 sandbox refusal; add real action/persistence parity test |
| Tool visibility level | Global editor | C-2 PATCH | Refused | `set_config` refusal | Global presentation setting, not agent override | C-21 refusal rows; D7/D11 |
| Skill visibility level | Global editor | C-2 PATCH | Refused | `set_config` refusal | Same deliberate global-only difference | C-21 refusal rows; D9/D11 |
| Trust mode | Trust block | C-2 PATCH; legacy adapter if retained | Refused | `set_config` refusal | Human-only safety setting | C-21 refusal rows; C-22 mapping tests |
| Revision trust approval | Install review, card, More actions | Install review defined; approval mutation incomplete | Agent can request, never self-approve | No direct agent mutation | Identity/revision/time derived by server, not supplied as agent authority; R2-MAJ-002 | Add handler/replay/stale-revision tests, not only `TestSkillConsent_Table` |
| Verification, content revision, approving person/time | Read-only derived facts | Read-only response | Not settable | Not settable | Security-derived metadata; reject attempted client/agent writes in the completed contracts | C-13 staged revision test plus approval negative tests |
| Retired loading keys | Removed by this package after Settings | Refused after removal | Refused | `set_config` and generic-config effect guards | Preserve Settings guard until hand-over; R2-MAJ-003 | `TestSettingsAgentWriteParity` until removal, then named retired-key refusal tests |

A test called “Parity” must fail when any supported user-editable property lacks the agent path or its explicit tested refusal. Merely testing one installed skill does not establish the table.

## Structural Integrity

| Check | Result | Notes |
|---|---|---|
| Valid Status | PASS | `In review`; no implementation/approval claim. |
| ADR linked or absence explained | PASS with required follow-through | The header explains no new ADR; dated corrections are commissioned for **Skill activation and loading** and **Tool manifest tier redesign**. Do not treat “no founder question open” as settled for the new action. |
| Contracts before implementation detail | PARTIAL | C-2 and C-13 now have real shapes. New approval decisions and project-key validation remain R2-MAJ-002/R2-MAJ-007. |
| API/data and lifecycle | PARTIAL | Migration/restart and terminal install outcomes remain R2-CRIT-001/R2-MAJ-006. |
| UI loading/empty/error/partial states | PARTIAL | Present across tabs and review; late-save classification and install outcome still conflict. |
| End-to-end user journey | PARTIAL | Main page/W5 journey specified; approval, Settings-first and recovery gaps remain. |
| Accessibility and keyboard | PASS at specification level | Filtered focus restoration and announcements added; disabled control test needs correction. No visual/browser acceptance performed. |
| Catalogue-first/design system | PASS at specification level | Existing primitives named, Tabs source verified; no new component required. |
| Security/user promises | PARTIAL | Project authority and migration blockers; consent ambiguity. User-doc TODOs are present. |
| BDD with independent oracles | FAIL | 109 unique scenarios counted, but R2-MAJ-008 identifies invalid expectations. A scenario count is not coverage proof. |
| Requirement → scenario → test traceability | PARTIAL | 83 unique FRs and 12 datasets counted; stale targets and under-scoped approval tests remain. |
| Epic / riders | WARNING | #1963 under #1519 is established by the current commission and issue. Header still calls ownership Unknown; chief confirmation of riders/Stage/Horizon is not evidenced. |
| Reachability | PARTIAL | Page and existing tool entry points named; project mutations and new approval actions are not yet fully specified. |
| Documentation TODOs | PASS with corrections required | DOC-001–DOC-014 name the affected user pages. Update DOC-013's first-use/each-use claim after consent decisions and align Settings/migration docs; no user-doc implementation is expected in this spec-only review. |

### Twelve-lens coverage

| Lens | Result / evidence |
|---|---|
| 1. Ambiguity | Consent precedence, R2-MAJ-001; global action scope, R2-MAJ-002. |
| 2. Incompleteness | Multi-file restart, project paths and install completion, R2-CRIT-001/R2-CRIT-002/R2-MAJ-006. |
| 3. Inconsistency with sources/ADRs | Storage, Settings order and uncompressed surface verified against code; R2-CRIT-001/R2-MAJ-003/R2-MAJ-005. |
| 4. Infeasibility | Atomic-file claim across entities and serial dispatch deadline, R2-CRIT-001/R2-MAJ-004. No new runtime dependency proposed. |
| 5. Contract-first | Approval action/response and per-agent writer shapes inspected as closely as backend logic; R2-MAJ-002/R2-MAJ-007. |
| 6. Security | Strictest-wins and trust assessed separately; project read/helper and migration blockers. |
| 7. Reachability | Existing tools/page located; Settings ownership and project editing paths checked, not assumed. |
| 8. UI states/journeys | Install staging/cancel/replacement, combined cards, app-level save queue and late errors examined; R2-MAJ-001–004/R2-MAJ-006. |
| 9. Accessibility/keyboard | Row removal, empty filtered results, disabled badge behavior, status announcements and tab semantics reviewed. Original focus/Tabs gaps closed; R2-MAJ-008 retains the contradictory interaction test. |
| 10. Design-system/brand | Catalogue and Tabs implementation checked; named reused controls, dark-first tokens, 44-pixel target requirement and no new component. No additional verified defect found; no rendered conformity claim. |
| 11. Testability/false greens | Literal expected values, request capture, context boundary and cross-boundary approval tests checked; R2-MAJ-008. |
| 12. Overcomplexity | Reuse existing approval/staging machinery; define recovery without adding a generic job/approval framework. R2-MAJ-002/R2-MAJ-006 request the smallest sufficient completion contract. |

### UX priority actions

1. **Status and recovery (H1/H9):** preserve saved/not-applied/refused/unknown meaning after navigation and during install reconciliation; never claim rollback from a missing answer.
2. **Control and error prevention (H3/H5):** make the permanent scope of Allow everywhere explicit, keep one truthful card for each decision, and preserve stricter-agent/trust checks.
3. **Consistency (H4):** align actual disabled controls, tab semantics, queue behavior and Settings destinations with their acceptance tests.

## Test Coverage Assessment

These are required plan corrections, not executed tests or new product scope.

| Category | Missing or defective boundary | Required evidence in implementation |
|---|---|---|
| Migration recovery | Crash after master deletion; failure midway through agent records | Saved RED log for the narrow recovery tests, then GREEN; repeat startup must preserve old Deny/Ask. |
| Project authority | Installed-only fixture misses mounted `.claude/skills` / `.omnipus/skills` | Actual file/helper requests on project paths, outside-file positive control, editor exception separated, platform/degradation evidence. |
| Consent | Auto plus new skill, own Ask, unattended, effective install Ask plus trust | Complete literal table and real card counts; no conflation of stored Ask with pending human consent. |
| New global action | Unit button/resolver tests miss persistence and continuation | Handler → durable readback/reload → a new scheduled run; combined trust, stale/replayed card, failure, stricter second agent, changed unverified revision. |
| Saved state | Slow first request, queued second click, late 503, unknown then failed GET | Controlled transport tests through the real queue owner; correct notification class after page exit. |
| Install outcome | Lost response while still publishing; old installed revision | Operation-bound terminal/unknown evidence, not list presence alone. |
| Settings hand-off | First package retains guard; second removes switch and transfers guard | Tests on the actual post-Settings base and combined cut; both field mutation and parent-object effect checks. |
| Visibility conversion | Compression off ToolSearch and first-request goal narrowing | Literal pre/post normal request, separate discovery/call checks, offline server return. |
| Accessibility | Disabled Ask cannot be activated through the UI | Keyboard/component assertion for disabled reason; separate API refusal test. |

| Dataset/scenario | Correction |
|---|---|
| D6 / C-2 | Empty request versus same-value retry; include project keys in every relevant writer, not only global PATCH. |
| D7 / ST-06 | Add the infra ToolSearch exception and a non-narrowed request; do not infer permission from visibility. |
| D8 / S-88 | Parameterize service, tool, God Mode, first/repeat/new-service state; assert all intended rows ran. |
| D9 | Account for headroom, pinned Full exactly once, output reserve and remaining request content; literal fit and one-over values. |
| D10 | Add explicit Auto/God/own-override/attendance defaults to every row; separate ordinary consent and trust; settle install card count. |
| D12 | No second network request before the first resolves if serial; cover late saved-not-applied/unknown and failed reconciliation. |
| S-102 | Two literal shelf/grant fixtures with opposite winners and unchanged nonselected key. |
| Test order 5 / S-36 | Reject legacy values on the new PATCH, not a deliberately retained legacy PUT. |
| S-109 / test order 47 | Add cross-boundary and hands-on proof; verified/trust-Allow only is not a combined-trust test. |

**Execution authority:** retain the specification's exact-cut tester plus independent validator and full CI at the cut. Extend those rows for the new action and project authority. Test authors retain narrow pre-change RED logs; a green unit test alone cannot certify UI reachability or migration behavior. This review runs no product tests and makes no red/green claim.

## STRIDE Threat Summary

S = spoofing; T = tampering; R = repudiation; I = information disclosure; D = denial of service; E = elevation of privilege. “Addressed” means specified or inherited for this review, not penetration-tested. “Gap” points to a finding; it is not an executed exploit.

| Component | S | T | R | I | D | E | Notes |
|---|---|---|---|---|---|---|---|
| Permission migration | Addressed | Gap | Addressed | Gap | Gap | Gap | Lost old restrictions or mixed entity state, R2-CRIT-001. |
| Project-skill paths | Addressed | Addressed | Addressed | Gap | Addressed | Gap | Mount authority can admit instructions/helper paths outside nominated gate, R2-CRIT-002. |
| Combined/global approval | Gap | Gap | Gap | Addressed | Gap | Gap | Identity/revision/action capability, durable result and stale decisions require a contract, R2-MAJ-002. Existing immutable approval identity must be retained. |
| Global PATCH | Addressed | Addressed | Addressed with caveat | Addressed | Gap | Addressed | Per-key validation/locking/audit specified; audit durability remains explicitly nonblocking. Queue/retry semantics R2-MAJ-004. |
| Staged installation | Addressed | Addressed | Addressed | Addressed | Gap | Addressed | Exact reviewed revision specified; terminal outcome/retry remains R2-MAJ-006. |
| Visibility/Full | Addressed | Addressed | Addressed | Addressed subject to consent | Gap | Addressed | Full is gated; budget fixture/consent precedence still need R2-MAJ-001/R2-MAJ-008. |

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| New tools requiring catalog entries? | No new tool is proposed. Existing tools gain fields/actions. | C-21/Reachability; positive source search found existing `Skill`, `ToolSearch`, agent-management and skill-install/edit entries in `/workspace/wt/skills-tools-1519f/pkg/config/defaults.go` and `/workspace/wt/skills-tools-1519f/pkg/coreagent/seed.go`. This establishes presence, not every proposed field. |
| Does a user screen invoke the feature? | Named, not implemented here. | Reachability names the existing Skills route, agent Tools tab, install review and chat W5. R2-MAJ-003 must align Settings; R2-MAJ-002 must make new actions callable. |
| Can agents set changed user-editable object fields? | Commissioned but incomplete for project keys. | Field table above; R2-MAJ-007. Global ceilings/trust deliberately remain human-only with clear refusals. |
| Does the test plan require execution? | Yes. | RED/GREEN/CHECK, ST-01–ST-25 on exact cut with independent validation; new action/project rows still needed. |

**Code correct and tested:** not established; this is a specification review, and no implementation tests were run.
**Reachable by a user/agent:** the existing entry points are verified, but the proposed feature's complete reachability is not established.

## Coordination warnings and author questions

- **Epic metadata:** the current commission and issue #1963 establish the child package under #1519. Correct the stale Unknown ownership sentence; Stage/Horizon values were not independently verified here.
- **Riders:** the specification explicitly says chief confirmation is outstanding. Record the chief's accepted set and disposition of the listed same-file candidates before final design approval. This report does not silently accept or defer them. That missing receipt is **UNVERIFIED / WARNING**, not an independently blocking finding.
- **Author, not founder:** identify the durable migration boundaries, project-path enforcement rule, approval transaction/result, install terminal receipt, agent request envelope, and post-Settings endpoint owner. These are engineering obligations once the two product questions below are answered.
- **User-facing docs:** retain the named TODOs in `/workspace/wt/skills-tools-1519f/docs/skills.md`, `/workspace/wt/skills-tools-1519f/docs/tools.md`, `/workspace/wt/skills-tools-1519f/docs/security.md`, `/workspace/wt/skills-tools-1519f/docs/settings.md` and `/workspace/wt/skills-tools-1519f/docs/agents.md`. They must match the chosen Auto/first-use scope, permanent versus session consent, editor exception, migration exceptions and Settings-first sequence. This spec-only review does not require those implementation updates now.

## Questions for the founder

These are for team-lead's required interview **before the final correction**. They do not reopen answered ST-Q1–ST-Q10, the no-wait decision, or the settled landing order. Answers can be recorded as “R2-Q1 A, R2-Q2 A.”

### R2-Q1 — Does “works everywhere” authorize a permanent global permission change?

**Context and impact:** The architect's button changes the skill's ordinary global permission from Ask to Allow, not just this call or chat. Other agents without stricter overrides benefit too. The global Allow persists across revisions; a changed unverified revision still needs trust approval, while a verified revision need not. The founder's answer did not name this global mutation.

| Option | Meaning |
|---|---|
| **A — recommended if the intended outcome is unattended use without another settings step** | Approve the explicitly global action. Its explanation must state all agents without stricter overrides, continued trust checks, and how to reverse it in Skills & Tools. Ordinary Approve Once and Always Allow do not gain this scope. |
| B | Keep chat approvals one-call/session/revision scoped; require the person to set global Allow on Skills & Tools before ordinary Ask stops blocking unattended use. This narrows the current reading of “approve once, works everywhere” and must be recorded as such. |

### R2-Q2 — Must a newly added skill get a human first-use yes even with Auto on?

**Context and impact:** ST-Q3 B chose Ask and described first-use asking. C-12 reuses ordinary Auto behavior, so after installation already answered trust, Auto can load a new skill without any first-use card. The later correction established skipping rather than waiting in unattended runs; it did not settle this distinction. The stored starting value remains **Ask under either option**; this is not a request to change ST-Q3 B to Allow.

| Option | Meaning |
|---|---|
| **A — recommended to preserve the stated first-use promise** | A new user/agent-added skill needs an explicit human first-use yes even with Auto on; install trust review alone is not that yes. The author must specify how consent is represented without adding a third policy ceiling. No unattended waiting is introduced. |
| B | Ask is ordinary policy and Auto can run the skill immediately once trust is satisfied. Remove unconditional first-use/every-use card promises, and state that unattended runs refuse only unresolved consent. |

Built-ins introduced later still follow ST-Q10 A, and unverified revision trust still follows ST-Q2/ST-Q8. Neither question permits a per-agent Allow to override the global ceiling.

## Verdict Rationale

**BLOCK** because R2-CRIT-001 and R2-CRIT-002 leave the preservation and enforcement of restrictions undefined at critical boundaries. The round-one fixes substantially improve the design, but implementation cannot safely infer a multi-file recovery protocol, project mount protection or the permanent approval action from these conflicting requirements. The eight MAJOR findings also require correction; a green component test or a rewritten label cannot close a server/state-contract gap.

### Exactly what must change before implementation

| Owner | Required correction | Finding |
|---|---|---|
| Architect/spec author | Recoverable multi-file migration; no restart-source deletion before safe completion; explicit override translation versus forbidden backfill | R2-CRIT-001 |
| Architect with security reviewer | Project-skill path enforcement overriding broader mount grants where required, supported-platform degradation and real-path tests | R2-CRIT-002 |
| Founder interview, then author | One consent truth table and consistent installation/first-use/unattended card count | R2-MAJ-001, R2-Q2 |
| Founder interview, then architect | Explicit global-action scope; generated action/frame/result and More-actions contract; stale/retry/persistence rules | R2-MAJ-002, R2-Q1 |
| Both package leads, chief coordinates | Settings-first dependency, intermediate reachability and exact C-7/FR-100/FR-101 guard transfer; no protection gap or stale consumer | R2-MAJ-003 |
| Spec author/frontend lead | Feasible serial-save timing, truthful global status, failed reconciliation handling and same-value retry semantics | R2-MAJ-004 |
| Spec author/test author | Exact conversion invariants and named infra exception, normal-request capture | R2-MAJ-005 |
| Architect/frontend lead | Revision/operation-bound install outcome and retry contract | R2-MAJ-006 |
| Architect/backend lead | Canonical project/installed key validation and create/update/read parity with preserved request envelope | R2-MAJ-007 |
| Spec author/test author | Literal oracles, corrected parameterization/budget/disabled-UI/legacy targets, integration and exact-cut UAT for new actions | R2-MAJ-008 |

Update the affected FRs, scenarios, datasets, traceability and documentation TODOs together in the **one final correction**. Chief rider confirmation remains a separate coordination receipt, not a speculative new feature or automatic deferral.

### Escalation to the founder

The final correction has **not happened yet**; this report cannot claim findings survived it. If any blocking finding remains after that correction, team-lead escalates it with the actual unresolved state. No third grill is authorized.

| Conditional escalation | Why it cannot silently pass | Decision needed if unresolved after correction |
|---|---|---|
| R2-CRIT-001 | Upgrade can no longer prove preservation of existing restrictions | Keep implementation blocked, or explicitly change the delivery scope to exclude the unsafe migration; no implicit acceptance of weaker permissions. |
| R2-CRIT-002 | Project skills lack the promised direct-path trust/permission boundary | Keep blocked, or explicitly revise the promise/scope with the security impact stated. |
| R2-MAJ-001/R2-MAJ-002 | Consent and global scope cannot be chosen by an implementer | Founder records the intended product outcome and any remaining accepted limitation. |
| Any other unresolved blocking item | Unverified fixes cannot be reported as resolved | Chief presents the specific residual defect and options, rather than requesting another grill. |

### Next step in the process

This was grill round 2 of 2 (fixed, final). Next: team-lead interviews the founder on “Questions for the founder”, then the spec author fixes round-2 findings. Any CRITICAL finding still open after that fix is listed under “Escalation to the founder” above for the founder to decide — do not run a third grill round. Once resolved, team-lead plans the implementation (RED / GREEN / CHECK, the 5-reviewer gate).

skills: grill-spec, omnipus-shared-rules, ux-heuristics-review.

## Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| Correct review pin and isolated scope | `git rev-parse HEAD`, exit 0: `b304736f72340bb079933facc5ae046af6155b24`; `git branch --show-current`, exit 0: `landing/skills-tools-1519f`; initial status contained only the prior untracked review | **Verified** |
| No production implementation changed in reviewed interval | `git diff --quiet 4e34afa4160fdb75a42cab57a6fa5628d030c64a HEAD -- pkg contracts src`, exit 0; prewrite `git diff --quiet` and `git diff --cached --quiet`, both exit 0 | **Verified** |
| All original findings considered | `/workspace/wt/skills-tools-1519f/docs/internal/specs/skills-tools-redesign-spec-review.md::Findings`; twenty-row disposition matrix above | **Verified** |
| Multi-file migration/restart contradiction | Specification C-19; `/workspace/wt/skills-tools-1519f/pkg/agentstore/store.go::RecordsDir`; `/workspace/wt/skills-tools-1519f/pkg/entity/store.go::write` | **Verified** defect; incident **Inferred** |
| Project path enforcement gap in proposed mechanism | Specification C-11/C-19; `/workspace/wt/skills-tools-1519f/pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority`; `/workspace/wt/skills-tools-1519f/pkg/fspolicy/homeguard.go::homeGuardDeny/inventoryDenies`; `/workspace/wt/skills-tools-1519f/pkg/sandbox/derive_from_fspolicy.go::DeriveKernelPolicy` | **Verified** source distinction; future bypass **Inferred** |
| Consent contradictions versus current ordering | Specification C-12/D10; `/workspace/wt/skills-tools-1519f/pkg/tools/auto_approve.go`; `/workspace/wt/skills-tools-1519f/pkg/agent/loop_run_turn_tools.go::resolveAskPolicy` | **Verified** |
| New action contracts incomplete | Existing action/response schemas and `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_tool_registry.go::HandleToolApprovals`; read-only `python3 -I` source check, exit 0: frame positive control present, action/response schema names each 0 occurrences in spec, `REVIEW_PREWRITE_SOURCE_CHECK=PASS` | **Verified** |
| Settings-first ownership conflict | `gh issue view 1963 --repo elicify-ai/omnipus --json number,title,state,body`, exit 0: OPEN, “lands after Settings”; pinned `git show` reads all exit 0 at `8e5848c951f901ba4218ef20b184c4055ecbc8b5`: C-7/FR-100/FR-101 guard and live loading switch remain | **Verified**; landed state not claimed |
| Serial-save and install-result contradictions | Specification C-16/D6/D12 and C-13 Cancel/lost-answer rule | **Verified** text; race outcomes **Inferred** |
| Visibility equality exception | `/workspace/wt/skills-tools-1519f/pkg/agent/tool_manifest.go::stripInfraToolDefs`; `/workspace/wt/skills-tools-1519f/pkg/agent/loop_run_turn.go::prepareToolSurface`; specification C-6/D7/SC-003 | **Verified** |
| Project-key parity and test-oracle gaps | Specification C-4/C-19/S-88/S-102/D9; `/workspace/wt/skills-tools-1519f/contracts/components/schemas/AgentToolsUpdateRequest.yaml::required`; `/workspace/wt/skills-tools-1519f/pkg/agent/context_budget.go::contextBudget/requestTokens` | **Verified** |
| Structural counts are not test passes | Read-only `python3 -I` source count, exit 0: `BDD: 109 entries, 109 unique`; `FR: 83 entries, 83 unique`; `Dataset: 12 entries, 12 unique`; `SOURCE_TABLE_CHECK=PASS` | **Verified** |
| Founder scope questions use actual option text | `/workspace/logs/skills-tools-1519f/founder-questions-r1.md::ST-Q3`; `/workspace/logs/skills-tools-1519f/st-questions-2/index.html::ST-Q9`; current commission and spec Clarifications | **Verified** wording; permanent-global-action approval **Unknown** |
| No graph/runtime/test verdict claimed | Commission states GitNexus unavailable; this review used source/Git/issue reads and report checks only | **Verified** method; runtime behavior of proposed implementation **Unknown** |
| **Self-check** | Re-read the report against the commission and reran the read-only `python3 -I` report validator, exit 0: `REPORT_STRUCTURE=PASS` (2 CRITICAL, 8 MAJOR; all 20 dispositions and seven finding fields); three deliberately damaged report variants were detected; `SPEC_REFERENCES=PASS`, `CITED_PATHS=PASS`; specification/prior-report SHA-256 values unchanged; HEAD unchanged; tracked/staged diffs empty; status contains only the pre-existing round-one report and this authorized report. No product test result is inferred from these document checks. | **Verified** |
