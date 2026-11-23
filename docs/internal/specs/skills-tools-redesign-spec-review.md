# Adversarial Review: Skills & Tools redesign

**Document reviewed**: `/workspace/wt/skills-tools-1519f/docs/internal/specs/skills-tools-redesign-spec.md`
**Mode**: SPEC
**Round**: Round 1 of 2
**Review date**: 2026-11-21
**Review commit**: `5028ff7b0162497e653c0eaaf3ae853c94582fda`
**Release baseline**: `4e34afa4160fdb75a42cab57a6fa5628d030c64a`
**Verdict**: **BLOCK**

## Executive Summary

The specification has **4 CRITICAL, 15 MAJOR and 1 MINOR findings**. Its two-layer permission foundation is correct, but the surrounding contracts permit approval bypasses, unintended permission changes during migration or concurrent saves, and a broken first landing before Settings is replaced. Frontend contracts, save states, install journeys, keyboard behavior and component reuse received the same scrutiny as backend authorization and migration; this was a source review, not a rendered-UI or runtime test.

| Severity | Count |
|---|---:|
| CRITICAL | 4 |
| MAJOR | 15 |
| MINOR | 1 |
| OBSERVATION | 0 |
| **Total** | **20** |

### Scope and evidence limits

- GitNexus is unavailable on this pod, as stated in the commission. Navigation used `rg`, direct file reads, pinned Git objects and read-only GitHub issue queries; no graph impact run is claimed.
- The reviewed change adds the specification only. Findings describe contradictions or omissions in that specification and what an implementation following it could do. They are **not claims that the proposed new behavior already runs in the release**.
- **Verified** means the cited requirement, contract or existing code path was inspected. Where the resulting production failure has not been executed, that consequence is explicitly **Inferred**. There were no builds, tests, browser sessions or paid model calls.
- References such as C-2, FR-019 and ST-13 below refer to the document identified above. Existing decisions are cited by title. The historical architecture documents were used as orientation; current code takes precedence.
- Only this report is an authorized output. No specification, production code, contract, test, issue, commit or branch was changed.

## Findings

### CRITICAL Findings

#### CRIT-001 — Full skill instructions enter the model before Ask can authorize them

- **Severity**: CRITICAL.
- **Lens**: Inconsistency; security; testability.
- **Affected section**: C-1, C-3, FR-016, FR-017, FR-030, FR-038, S-43, ST-13.
- **Failure scenario**: Mia has a granted unverified skill, its permission is Allow, trust is Ask, and its visibility is Full. FR-038 includes the instructions in the first request because the skill is “not-denied”; Ask is not Deny. The model receives and can follow those instructions without making a `Skill` call, so the promised approval never gates exposure. The same conflict exists for an ordinary per-skill Ask and for unattended runs, which FR-017 says must refuse an Ask load.
- **Evidence**: C-1 defines Full as “Its whole instructions are in the agent's context from the start of every chat”; FR-038 applies it to “Each granted, not-denied skill.” FR-016 and FR-030 require approval before an Ask load. C-9 only specifies the approval frame for a `Skill` load call, not initial injection.
- **Recommendation**: Define authorization **before every instruction-injection path**, including Full, slash activation and delegated requested skills. Specify the Full × permission × trust × interactive/unattended matrix. Full must not itself confer consent. If approval is required before the first model request, define that user journey and its denied/cancelled states; if Full Ask skills are withheld until authorized, state that exception to “from the first turn.” Add a scripted-provider assertion that a distinctive instruction sentinel is absent from every outbound request before authorization.
- **Certainty**: **Verified, high confidence** — contradictory requirements. The bypass is **Inferred** from implementing the unconditional injection requirement; no runtime exploit was attempted.

#### CRIT-002 — The new skill gate omits existing file-read and helper-execution authority

- **Severity**: CRITICAL.
- **Lens**: Security; incompleteness; ecosystem compatibility.
- **Affected section**: C-0, FR-014–FR-018, Security and User Promises, Integration Boundaries.
- **Failure scenario**: An agent retains a grant to a registry skill, has `edit_skill` denied, and has ordinary file tools allowed. The operator changes that skill to Deny, or trust makes it Ask. Gating only `Skill`, the menu and search leaves the existing folder authorization intact: the agent can read the instructions through `read_file`, independently of the new skill approval. The helper-execution path uses the same folder set. A page showing Deny would therefore not enforce the promised boundary.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority` obtains folders from `skills.LoadableFolders` using grants and grant bindings, without a per-skill policy or trust decision. `/workspace/wt/skills-tools-1519f/pkg/fspolicy/homeguard.go::skillPathDenied` admits those folders. `/workspace/wt/skills-tools-1519f/pkg/tools/shell_skill_operands.go::loadableSkillReadOrRun` also consumes that set. The spec names loader/menu enforcement but provides no corresponding filesystem or helper authority contract. The decision **Skill activation and loading** explicitly permits file-tool reads; they are not a hypothetical alternate entry point.
- **Recommendation**: Inventory and specify instruction read, search, file-tool, shell-helper, Full, slash and delegate entry points. Make ordinary use follow the same authorization outcome, with matching app-level and enforcing-kernel behavior. Preserve the deliberate editor's read-for-authoring exception separately; do not claim that reading for editing authorizes execution or that previously disclosed text can be erased. Add a granted-but-Denied and granted-but-Ask fixture with `edit_skill` denied, testing both `Skill` and direct file/helper paths.
- **Certainty**: **Verified, high confidence** — existing alternate authority and the missing integration are source-verified. The post-change bypass if those paths remain unchanged is **Inferred**, not executed.

#### CRIT-003 — The proposed partial save still requires a replacement tool map and can undo an unrelated Deny

- **Severity**: CRITICAL.
- **Lens**: Contract-first gaps; concurrency; UI states.
- **Affected section**: C-2, Dataset D6, FR-007–FR-009, Edge Cases, Ambiguity Warning 2.
- **Failure scenario**: Two tabs read the same global policies. Tab A denies `send_email`. Tab B changes only a skill or visibility setting. Under the “policies unchanged” contract B must supply its cached complete tool map, which restores `send_email` to its old value while B believes it changed only that skill. Alternatively, B sends D6's skill-only request and the unchanged required field rejects it. This is not the explicitly accepted race of two people changing the **same** value.
- **Evidence**: `/workspace/wt/skills-tools-1519f/contracts/components/schemas/GlobalToolPolicies.yaml::required/properties.policies` requires `policies` and describes the complete map. `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_tool_policies.go::putToolPolicies` replaces `sandbox.tool_policies`. C-2 keeps `policies` unchanged while adding merge-only members; D6 expects a skill-only body to return 200 and change only that key. A write lock serializes replacement but cannot identify stale, untouched values supplied by a client.
- **Recommendation**: Define distinct read and mutation semantics explicitly. A partial skill/level save must not require echoing the tool ceiling. For tool changes, choose per-key deltas or a revision-checked replacement that visibly rejects stale snapshots. Specify omitted, empty and null members, whole-request validation, canonical-key collisions, and the saved/not-applied outcome. Test two tabs changing different keys and prove both survive; separately test the chosen same-key winner/conflict behavior.
- **Certainty**: **Verified, high confidence** — schema/handler versus D6 contradiction. The permission overwrite follows the inspected replacement semantics; it was not reproduced in a running app.

#### CRIT-004 — Missing a server wildcard does not mean its tools were denied

- **Severity**: CRITICAL.
- **Lens**: Inconsistency with current code; security; migration.
- **Affected section**: C-7, FR-045, FQ-6, SC-003.
- **Failure scenario**: C-7 adds an Ask or Deny wildcard whenever the exact server wildcard is absent. Either option changes existing access in cases FQ-6 misclassifies as “never had a permission.” With God Mode off and an assigned service:

  | Existing policy | Before | Added server wildcard | After |
  |---|---|---|---|
  | Global `mcp_*: deny`, no more-specific match | Deny | `mcp_github_*: ask` | Ask: a more-specific wildcard defeats the broad global Deny |
  | No matching global value; agent exact `mcp_github_read: allow` | Allow | `mcp_github_*: deny` | Deny: existing authorized work stops |

- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/tools/compositor.go::resolveFromMap` chooses exact matches, then the most-specific wildcard **within** a layer. `mergeStoredPolicyLayers` uses the agent value when the global value is absent. FQ-6's claim that the absence tested by C-7 means “blocked for every agent” is therefore false. Strictest-wins operates between the two resolved layers, not among every matching key in one map.
- **Recommendation**: Replace “exact server key absent” with an explicit migration matrix covering global exact, server wildcard, broader wildcard, agent-only coverage, genuine absence, assignments and God Mode. Preserve explicit restrictions; name any intended behavioral change rather than calling it preservation. Keep the founder's new-service Ask decision separate from old-install migration. Re-present FQ-6 with the corrected states; neither blanket option is safe as written. Mixed-coverage services need an architect-defined mapping, not a new fallback or blanket per-agent backfill.
- **Certainty**: **Verified, high confidence** — both counterexamples follow directly from the inspected resolver. No new migration implementation was run.

### MAJOR Findings

#### MAJ-001 — “Ask” lacks one coherent consent contract across Auto, installation and content changes

- **Severity**: MAJOR.
- **Lens**: Ambiguity; security; UI journey.
- **Affected section**: C-3, C-9, FR-016, FR-017, FR-030, S-10/S-11, ST-04/ST-10, FQ-2.
- **Failure scenario**: One implementation sends ordinary skill Ask through the existing Auto classifier and skips a card; another always prompts under FR-016. A third treats trust Ask like an always-ask tool but still offers Always Allow, then reuses consent after the same skill name has different content. At installation, “install_skill keeps its own Ask” does not protect trust Ask when that tool has been set to Allow or the global Ask is lifted by God Mode. The direct REST Ask case has no stated proof of review.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/tools/auto_approve.go` classifies `Skill` as `AutoRuns` and `install_skill` as `AutoAsks`; classification is relevant only when the effective tool policy is Ask. `/workspace/wt/skills-tools-1519f/pkg/tools/always_ask.go::AlwaysAskTools` describes fresh, non-remembered consent. `/workspace/wt/skills-tools-1519f/pkg/security/approvalgrants.go::ApprovalGrantStore.IsAllowed` keys standing consent by session, agent, tool and arguments, not skill content revision. FR-016 remembers Always Allow; FQ-2 describes an unverified skill as “always asks.”
- **Recommendation**: Add a single consent table for ordinary skill Ask versus trust Ask, installation versus loading, Auto/God, interactive/unattended, slash requests, delegates and remembered grants. Decide whether trust consent is fresh or reusable; hide/refuse Always Allow when it is not reusable. Bind pending and reusable approval to the actual skill/artifact identity and content/trust state, rechecking before use. Do not substitute the shipped `install_skill: ask` default for an installation trust gate. Define how authenticated REST installation expresses the authorized review.
- **Certainty**: **Verified, high confidence** for incompatible/incomplete rules and current approval mechanics; which wrong interpretation would ship is **Inferred**.

#### MAJ-002 — The install review needs verified facts that no proposed pre-install contract returns

- **Severity**: MAJOR.
- **Lens**: Contract-first gaps; UI states and journey gaps.
- **Affected section**: C-5, FR-042, US-7 AS4–6, Install review state row, S-55/S-56, ST-16.
- **Failure scenario**: The catalog review must display verified status and preselect a category from the file before Install is enabled. The frontend only has search metadata; it either guesses verification, cannot preselect, or installs first to obtain the facts it was supposed to review. Leaving Cancel usable during publication also permits “cancelled” UI while a skill actually installs unless cancellation semantics are defined.
- **Evidence**: `/workspace/wt/skills-tools-1519f/contracts/components/schemas/SkillSearchResult.yaml` has no verification or category property. `/workspace/wt/skills-tools-1519f/contracts/components/schemas/SkillInstallRequest.yaml` supplies a slug/upload reference and optional version, not a reviewed artifact identity. `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_skills.go::installSkill` stages/downloads before `publishSkillInstall`, which returns `Verified` after publication. C-5 only adds category to the install request and installed Skill.
- **Recommendation**: Define the contract that supplies authoritative review metadata before publication, reusing the existing staging mechanism where appropriate. Bind the review to the artifact/version actually installed; define expiration/retry, verification unavailable, hash mismatch, replacement/local edits and changed trust during review. Specify when Cancel can prevent publication and when it merely closes the view while outcome reconciliation continues. Test that a review cannot authorize different downloaded bytes and that cancellation never falsely promises “nothing installed.”
- **Certainty**: **Verified, high confidence** — missing wire data and current staging boundary; resulting frontend workarounds are **Inferred**.

#### MAJ-003 — Project skills have no legal policy identity, and master-policy migration does not cover them

- **Severity**: MAJOR.
- **Lens**: Incompleteness; migration; reachability.
- **Affected section**: C-2, C-4, C-8, FR-019/FR-020, Edge Cases, Ambiguity Warning 6, Assumptions.
- **Failure scenario**: A mounted project exposes a skill that is not installed globally. The edge case says it uses the global permission and C-2 defaults, but C-2 only reconciles installed IDs and rejects unknown IDs. The resolver can therefore find no stored entry and deny it, or the implementer invents a forbidden fallback. A project and registry skill sharing a slug can also receive the wrong policy. Copying the old per-agent `Skill` value only to granted installed skills does not preserve that agent's restriction on mount-authorized skills.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/skills/shelf.go::ResolveSkillName` distinguishes registry/builtin grants from mount-authorized project skills and has explicit same-slug precedence. FR-019 migrates only current granted skills; the project edge case says there is no grant list. C-8's “skills are installation-wide” is not true of the current write-skill workflow's project destination.
- **Recommendation**: Define the policy identity and lifecycle for each shelf, including mount identity, collisions and mount removal, without requiring the deferred project-list UI. State exactly how project skills participate in migration or obtain an explicit, approved exception. Specify one-time/idempotent migration ordering before self-heal, retirement of both old global and per-agent master entries, and restart/failure behavior. Preserve unrelated explicit policies created by old presets. Document that later grants no longer inherit the retired blanket restriction; do not label that future behavior unchanged.
- **Certainty**: **Verified, high confidence** — the identity/default and migration requirements do not cover the existing project path. The chosen failure mode is **Inferred**.

#### MAJ-004 — Visibility migration copies configuration labels, not the actual outbound behavior

- **Severity**: MAJOR.
- **Lens**: Inconsistency; migration; testability.
- **Affected section**: C-6, FR-037, Dataset D7, S-41, SC-003.
- **Failure scenario**: An allowed MCP tool has `deferred: false`, compression is on, and it has not been searched in a fresh session. The ordinary compressed request still classifies its name as lazy and omits its definition. C-6 migrates it to Full, adding that definition to later ordinary requests. The promise that migration changes no request is false. An offline service adds a second gap: after its legacy flag is ignored, the spec does not say how newly discovered tools recover the migrated service default.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/agent/loop_mcp.go::serverIsDeferred` also depends on `Discovery.Enabled`; `registerServerTools` controls registration visibility. `/workspace/wt/skills-tools-1519f/pkg/tools/manifest.go::ToolManifestTier` treats MCP names as lazy. `/workspace/wt/skills-tools-1519f/pkg/agent/tool_manifest.go::buildCompressedToolDefs` sends lazy definitions only when loaded. `/workspace/wt/skills-tools-1519f/pkg/tools/registry.go::RegisterMCP` and `GetAll` show that nondeferred registration is not itself a Full manifest classification.
- **Recommendation**: Derive migration from the complete pre-change request pipeline: compression, discovery enabled, deferred true/false/absent, preview flag, assignment/policy, fresh versus loaded session, and offline discovery. Persist server-level defaults independently of currently connected tools before retiring flags. Provide literal expected outbound fixtures, including the normal request after any goal-narrowing handshake. Narrow SC-003 to unchanged behaviors and list approved exceptions, including the intentional Warn-to-Ask trust change.
- **Certainty**: **Verified, high confidence** — the stated legacy mapping contradicts the inspected pipeline; no request-capture test was run.

#### MAJ-005 — Live visibility edits do not define loaded-definition retention, menu invalidation or search reindexing

- **Severity**: MAJOR.
- **Lens**: Ambiguity; reachability; overcomplexity.
- **Affected section**: C-1, FR-034/FR-036/FR-038, S-38/S-40/S-42, ST-12.
- **Failure scenario**: A person loads a tool, changes it to Hidden, and sees Saved. The current session can keep sending its complete definition, while the new control appears to promise no listing on the next turn. Separately, demoting an old Full builtin to Hidden can remove its initial definition without making it discoverable if the old fixed search classification remains. A frozen skill menu can retain the old level or denied name unless policy/visibility changes invalidate its generation.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/agent/prompt_cache_sessions.go::restoreLoadedToolRetention`, `pruneLoadedTools` and `freezeSessionSkillMenu` retain loaded names and cache the menu by generation. `/workspace/wt/skills-tools-1519f/pkg/tools/registry.go::SnapshotSearchableTools` excludes core tools classified Full/infra by the old classifier. **Tool manifest tier redesign** describes session retention; the spec schedules its correction but does not choose the new transition semantics.
- **Recommendation**: State whether levels govern initial advertising only or also evict already-loaded definitions, and when an existing chat observes a change. Treat permission revocation separately from visibility. Use one visibility authority for manifest building, search membership and cache invalidation rather than parallel old/new classifications. Test promotion and demotion of an originally Full builtin, a loaded lazy tool, a deferred MCP tool and a frozen skill menu, in old and new sessions. Visibility must never manufacture call authority.
- **Certainty**: **Verified, high confidence** — existing retention/index dependencies and the missing transition rule; future stale/unreachable outcomes are **Inferred**.

#### MAJ-006 — Full skill content has no aggregate context-capacity outcome

- **Severity**: MAJOR.
- **Lens**: Infeasibility; denial of service; UI recovery.
- **Affected section**: C-1, FR-038, Performance Bounds, S-43, FQ-1.
- **Failure scenario**: A person grants several individually valid large skills and sets all to Full. Their mandatory instructions plus the fixed prompt and output reserve exceed the chosen model's context capacity before useful conversation starts. An implementation must then violate “whole instructions,” refuse the chat, or silently trim something; no expected result or recovery UI is specified.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/skills/authoring.go::MaxSkillMarkdownBytes` permits 256 KiB per instruction file. `/workspace/wt/skills-tools-1519f/pkg/agent/context_budget.go::contextBudget`, `pinnedCoreOverheadTokens` and `requestTokens` account for finite request capacity. FR-038 has no aggregate-fit exception or failure state. This is not a request to restore the retired per-turn menu cap.
- **Recommendation**: Charge the actual injected instructions to the existing budget exactly once. Define the visible outcome when mandatory Full content cannot fit, including recovery by changing levels or model. Do not silently truncate instructions or falsely show a ready chat. Add just-fits/one-over fixtures, multiple skills, model switching and content growth after selection; use a controlled capacity rather than a paid provider to test it.
- **Certainty**: **Verified, high confidence** for the finite capacity and missing specified outcome; exact provider failure behavior is **Unknown** and is not asserted.

#### MAJ-007 — The required first landing breaks surviving Settings controls and leaves an agent visibility override

- **Severity**: MAJOR.
- **Lens**: Cross-package consistency; UI journey; write-surface parity.
- **Affected section**: C-3, C-6, C-10, FR-049/FR-057/FR-065, Settings integration, Assumptions, FQ-4.
- **Failure scenario**: Skills & Tools lands first, as required, while this package is forbidden to edit Settings. The still-mounted Skill Trust control sends the legacy enum, which the new endpoint rejects; its selected state no longer matches the returned enum. Links to future Settings sections also lack a first-landing contract. Meanwhile an agent holding `set_config` can still change `tools.manifest.compressed`, overriding every tool visibility level, despite C-10 saying these global choices are user-only. This changes visibility, **not** permission Deny.
- **Evidence**: `/workspace/wt/skills-tools-1519f/src/components/settings/SkillTrustSection.tsx::LEVELS/SkillTrustSection` uses `block_unverified`, `warn_unverified`, `allow_all`; `/workspace/wt/skills-tools-1519f/src/components/settings/SecuritySection.tsx::SecuritySection` mounts it. `/workspace/wt/skills-tools-1519f/pkg/sysagent/tools/config.go::validateConfigKey`, `performanceOnlyConfigKeys` and `ConfigSetTool.Execute` leave the existing compressed key writable. The Settings specification at commit `30e105062768c38e362dd29a6f989c71dca56ef5`, `/workspace/wt/skills-tools-1519f/docs/internal/specs/settings-redesign-spec.md::FR-060/C-7/FR-100`, lands later and only then adds that refusal.
- **Recommendation**: Agree a narrow cross-package compatibility change owned in this landing, or an explicitly approved different landing contract. Keep surviving consumers compatible and links valid on each intermediate release. If compressed remains, carry its user-only refusal and corresponding parent-write/effect tests here rather than relying on later Settings. Reconcile the actual later section name: its relevant destination is Limits, whereas this spec repeatedly says Performance. Test the Skills & Tools landing against the old Settings screen, then the combined result.
- **Certainty**: **Verified, high confidence** — source/contract and landing-order conflicts. No compilation failure or live bypass was claimed.

#### MAJ-008 — Agent-tool parity omits object creation, skill authoring and shared-config preservation

- **Severity**: MAJOR.
- **Lens**: Incompleteness; contract-first gaps; reachability.
- **Affected section**: C-4, C-5, C-8, C-10, FR-041/FR-054/FR-056, ST-15, R32.
- **Failure scenario**: The user/API can express new agent skill policies through the expanded shared `AgentToolsCfg`, but `create_agent` has no commissioned matching field or deliberate refusal. An unrelated existing `tools_cfg` update can reconstruct the agent configuration without the new skill map unless preservation is specified. Separately, Write with Omnipus successfully starts W5 but the writing agent cannot set the externally stored category: that workflow creates with `write_file`, not `install_skill`.
- **Evidence**: `/workspace/wt/skills-tools-1519f/contracts/components/schemas/AgentCreateRequestMain.yaml::properties.tools_cfg` references the expanded schema. `/workspace/wt/skills-tools-1519f/pkg/sysagent/tools/agent_apply_args.go::agentCreateArgNames` has only existing permission fields. `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_agents_create.go::agentCreateToolsCfgFromWire` and `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_agents_update.go::updateToolsConfig` normalize/reconstruct existing builtin/MCP fields. `/workspace/wt/skills-tools-1519f/pkg/skills/embedded/write-skill/SKILL.md::Steps` creates files directly. R32 in `/workspace/omnipus-coordination/handoffs/ui-redesign/navigation-menu-wireframe/requirements.md` requires an installing **or writing** agent to choose the category outside the skill file.
- **Recommendation**: Expand C-10 into separate UI, REST-create, REST-update, agent-create and agent-update columns. Cover every shared-schema writer, omission/clear behavior, normalization, descriptions and tests. Either provide the new create path or document and reject the deliberate difference. Specify a reachable category-authoring/publication path for W5 and copies, without writing category into third-party skill content. Extend parity testing to create, ordinary updates that must preserve skill overrides, and a completed authored skill with its chosen category—not merely a workflow launch.
- **Certainty**: **Verified, high confidence** — missing commissioned paths and existing replacement behavior; post-change loss of the new field is **Inferred** if those writers are left unchanged.

#### MAJ-009 — Autosave cannot promise that navigation leaves nothing unsaved or that every network error means “Not saved”

- **Severity**: MAJOR.
- **Lens**: UI states; concurrency; infeasibility.
- **Affected section**: FR-007–FR-009, Performance Bounds, User Journey exit, page save states, rapid Set all edge case.
- **Failure scenario**: A save reaches disk but its response is lost. The page treats the network failure as “Not saved” and restores a stale value even though the opposite value is stored. Or the person navigates while a save is pending and its failure arrives after the page disappears; there is no specified owner for retry/recovery. FR-007 also promises completed saving within 1,000 ms, while Performance Bounds only promises starting the request within that time.
- **Evidence**: The spec states “Exit: through the sidebar; nothing is ever unsaved.” `/workspace/wt/skills-tools-1519f/src/hooks/useAutoSave.ts::useAutoSave` already serializes ordinary saves, snapshots in-flight data, supports persisted-error handling and **flushes pending changes on unmount**. It does not make network completion certain. Its queued rerun coalesces to the latest data, while the spec demands one save for every rapid Set all click. This finding is **not** an allegation that the hook simply drops every debounced edit on unmount.
- **Recommendation**: Define unsent, in-flight, confirmed-saved/applied, confirmed-saved/not-applied, confirmed-refused and outcome-unknown states. Specify who owns pending work and late errors after in-app navigation, and how page exit/reopen reconciles with the server without a leave prompt. Show Saved only for the acknowledged snapshot; do not infer rollback from a lost response. Reconcile coalescing with “one save per click,” define ordering, and measure request-start latency separately from completion. Add response-loss, late-failure, navigation, retry and out-of-order tests.
- **Certainty**: **Verified, high confidence** — contradictory/incomplete state contract and inspected hook behavior. No durability failure was reproduced.

#### MAJ-010 — Set all → Allow includes an always-ask row that the server must reject

- **Severity**: MAJOR.
- **Lens**: UI error prevention; security; inconsistency.
- **Affected section**: C-6, FR-003/FR-011/FR-023, US-1, Dataset D4, regression dataset.
- **Failure scenario**: A group contains `set_channel_outbound`. Set all → Allow promises to change every visible editable row; only ToolSearch and Skill are excluded. The always-ask validator rejects Allow for this tool, so the whole save fails. Removing the validator would instead break the existing security decision. On agent tabs, global locks and Omnipus's per-agent Ask prohibition add action-specific restrictions that cannot be represented by one generic editable-row count.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/tools/always_ask.go::AlwaysAskAllowViolation` and `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_tool_policies.go::putToolPolicies` reject Allow before persistence. `/workspace/wt/skills-tools-1519f/src/lib/toolPolicyPresets.ts::applyRolePreset` already handled this exception in the feature being removed. The spec's regression row preserves always-ask execution but does not define the replacement bulk-edit behavior.
- **Recommendation**: Specify supported values and reasons per row/action, single-edit behavior, and bulk behavior for ineligible rows. The count and accessible label must match what will actually change; do not silently substitute Ask for an advertised Allow or weaken the server guard. Cover global, per-agent, Omnipus, filtered and mixed groups, including Deny under God Mode for an always-ask tool.
- **Certainty**: **Verified, high confidence** — required UI action conflicts with a verified unchanged validator.

#### MAJ-011 — The no-confirm promise contradicts the preserved autonomous-plan confirmation

- **Severity**: MAJOR.
- **Lens**: Inconsistency; UI journey; user promises.
- **Affected section**: FR-007, FR-010, Qualitative Prohibitions, US-1 AS5, S-05, ST-03, T-Q3 B.
- **Failure scenario**: The implementer obeys the categorical “no Set all confirm” requirement and removes the autonomous-plan warning, or obeys FR-010 and ships a dialog that the same spec forbids. The acceptance suite can demand both outcomes for the same click.
- **Evidence**: FR-007 and Qualitative Prohibitions prohibit a Set all confirmation; FR-010 and S-05 require it when `execute_plan` would resolve to Allow. `/workspace/wt/skills-tools-1519f/src/components/shared/ToolPolicyEditor.tsx::resolvesExecutePlanAllow` implements the existing sensitive-grant affordance. Clarifications records the founder's latest answer as “autosave everywhere, no confirm, including Set all.”
- **Recommendation**: Obtain the narrow founder decision listed below: preserve this security-specific exception or explicitly remove it. Then make the requirements, scenarios, bulk atomicity and docs agree. Do not silently reinterpret the latest answer, and do not remove the existing warning merely to make contradictory tests pass. No generic save/bulk confirmation is proposed.
- **Certainty**: **Verified, high confidence** — direct requirement contradiction; the desired exception is not established by the supplied answer.

#### MAJ-012 — Per-key audit does not cover the moved tool-permission edits

- **Severity**: MAJOR.
- **Lens**: Security/repudiation; incompleteness; testability.
- **Affected section**: C-2, FR-040, Set all, Traceability Matrix.
- **Failure scenario**: A person changes a group of tool permissions, or an unrelated permission is overwritten as in CRIT-003. The audit records only the number of submitted policies, so an operator cannot reconstruct which tool changed from Deny to Allow or distinguish it from an unchanged-map retry. The new skill and visibility paths have a stronger audit promise, but the same-looking tool edit does not.
- **Evidence**: `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_tool_policies.go::putToolPolicies` records action `tool_policies_update` and `policy_count`, not per-key old/new tool values. C-2 and FR-040 require per-key records only for skill permission and the two visibility maps. FR-040 traces to S-09 and S-37, whose Then clauses assert denial/menu behavior and menu options, not audit entries.
- **Recommendation**: State a consistent per-key audit contract for the permission edits this page exposes, including tool policies, actual changed keys, old/new values, actor, no-op retries and failed validation. Tie audit deltas to the serialized mutation rather than a stale client snapshot. Add explicit independent audit assertions for one key, filtered bulk, no-op retry, whole-request refusal, persistence failure and saved-not-applied. Define visible handling of audit-write failure rather than implying that a successful map save proves an audit record exists.
- **Certainty**: **Verified, high confidence** — present audit contents and proposed omission. This is an existing limitation carried into the new bulk-edit flow, not a newly observed production incident.

#### MAJ-013 — Saving a filtered row can remove the focused control with no focus destination

- **Severity**: MAJOR.
- **Lens**: Accessibility and keyboard; UI states.
- **Affected section**: FR-005/FR-052, US-6 AS6, S-50, Accessibility and Keyboard.
- **Failure scenario**: A keyboard or screen-reader user filters Permission = Ask, activates Deny on the last matching row, and the acknowledged save removes that row or its entire group. The focused control disappears; the user has no specified next focus target or announcement explaining where the row went. A bulk action can remove every row at once.
- **Evidence**: FR-005 requires removal after save. Accessibility and Keyboard covers the level menu's Escape return and generic focus styling, but not removal of the focused row/group, empty results, or failure restoration.
- **Recommendation**: Specify a deterministic focus destination for next matching row, surviving group header and empty-result/search cases. Announce the saved change and changed result count without moving focus on every background refresh. Test single-row, middle-row, last-row, whole-group and failed-save cases with keyboard and assistive-technology semantics at narrow width as well as desktop.
- **Certainty**: **Verified, high confidence** for the missing transition; the exact browser focus fallback is **Unknown**, not asserted as a tested result.

#### MAJ-014 — Several acceptance checks can pass while their claimed behavior is absent

- **Severity**: MAJOR.
- **Lens**: Testability and false-green risk.
- **Affected section**: ST-06, ST-13, ST-15, FR-040 traceability, SC-001/SC-003/SC-005, Test Hierarchy.
- **Failure scenario**: A model describes a familiar plan skill from prior knowledge without any injected instructions; ST-13 passes because no `Skill` call appears. An upgrade alters outbound requests but ST-06 passes because settings match the saved configuration. W5 opens without creating a categorized skill and ST-15 passes. SC-005 says three install paths were verified, while ST-09 drives only upload and agent installation, not an independently asserted direct REST case.
- **Evidence**: These are the literal steps/expected results of ST-06, ST-13, ST-15 and ST-09. The document `/workspace/wt/skills-tools-1519f/docs/internal/false-green-patterns.md` requires proving the instrument could detect the failure. The spec's scripted-model integration level is available but is not used as the independent oracle for these claims.
- **Recommendation**: Replace model-answer inference with captured outbound requests and unique fixture sentinels. Give migration literal pre/post fixtures independent of the new builder, with approved behavior changes separated. Complete W5 through publication/category/grant behavior, enumerate every install entry point, and add real audit assertions. Require positive controls and mutation probes that remove injection, skip enforcement, reset a migrated value or drop an audit entry. Name `npm run typecheck`, the applicable design-system checks and CI authority in the execution checklist; do not turn this review into a local test run.
- **Certainty**: **Verified, high confidence** — the described checks do not observe the properties they claim. No actual test PASS is being challenged or claimed.

#### MAJ-015 — Unified tool/skill discovery is removed from scope without a traced founder decision

- **Severity**: MAJOR.
- **Lens**: Traceability; reachability; scope consistency.
- **Affected section**: Qualitative Prohibitions, Assumptions, C-1, R31.
- **Failure scenario**: The feature is accepted against R31, but ToolSearch still cannot find a Hidden skill. An agent following the required unified discovery flow finds nothing even though the skill is allowed; it must know to call the separate Skill search. The spec expressly forbids implementing the required path, so passing its own tests cannot demonstrate R31 completion.
- **Evidence**: `/workspace/omnipus-coordination/handoffs/ui-redesign/navigation-menu-wireframe/requirements.md::R31` requires “One search for both: ToolSearch also finds skills,” typed results and a Skill loading handoff. The spec's Assumptions and Qualitative Prohibitions exclude it. The supplied T-Q7 changes global/per-agent scope, and T-Q4b changes levels; neither supplied answer waives unified search.
- **Recommendation**: Either restore the R31 discovery requirement with its result contract and tests, or obtain an explicit founder-approved split and name the owning tracked work plus the accepted user/agent limitation. Do not present an author assumption as a founder scope decision. This does not require merging the two execution tools or changing grants.
- **Certainty**: **Verified, high confidence** for the source/spec conflict; any separate approval not provided to this review is **Unknown**.

### MINOR Findings

#### MIN-001 — The selected view-switch component is a radio group, not a tablist

- **Severity**: MINOR.
- **Lens**: Design-system reuse and brand; accessibility.
- **Affected section**: Accessibility and Keyboard, Design-System Components, FR-052.
- **Failure scenario**: Reusing the nominated component unchanged yields radio-group semantics while accessibility tests and screen-reader expectations are written for tabs and tab panels. Hand-adding roles to an arbitrary wrapper to meet the wording would bypass the catalogued-control rule.
- **Evidence**: `/workspace/wt/skills-tools-1519f/src/components/ui/view-switch.tsx::ViewSwitch` renders `RadioGroup`/`RadioGroupItem`; its props contain no tab-panel relationships. `/workspace/wt/skills-tools-1519f/src/components/ui/CLAUDE.md::No hand-built controls` requires catalogued controls. The spec says “flat view switch with tablist semantics.”
- **Recommendation**: Choose one coherent semantic contract: use the existing radio-group view switch with correctly labelled content, or commission a catalogued tabs-compatible adaptation with its required publication/tests. Update the keyboard and accessibility oracle to the chosen component; do not relabel radio children as tabs by ad hoc markup.
- **Certainty**: **Verified, high confidence** — component/requirement mismatch. No rendered accessibility failure was measured.

## T-Q7 and write-surface assessment

The answer is **not** to invent another policy layer or refuse every stored looser override. The existing runtime clamp is real; the unsafe paths are migration, mutation semantics and incomplete integration.

| Surface | Source-verified behavior | Required disposition |
|---|---|---|
| Runtime merge | `/workspace/wt/skills-tools-1519f/pkg/tools/compositor.go::resolveEffectivePolicyWith/mergeStoredPolicyLayers`: Deny then Ask; a sole matching layer decides; neither matching logs Error and denies | Reuse it. No new default-policy field or per-agent fail-closed backfill |
| God Mode | Same resolver replaces only the ordinary global resolved value with Allow, then merges; per-agent Ask/Deny survive | Show stored and effective values honestly; do not erase the sparse override to make a badge match |
| Always-ask | Same resolver skips the God floor for `set_channel_outbound`; `/workspace/wt/skills-tools-1519f/pkg/tools/always_ask.go` forbids lasting Allow | Preserve Deny and fresh-consent behavior; fix bulk semantics, MAJ-010 |
| Agent editor | `/workspace/wt/skills-tools-1519f/src/components/shared/ToolPolicyEditor.tsx::isPolicyLocked/trueEffectivePolicy` handles the ordinary global floor | Preserve locks while integrating God/trust readouts and action-specific restrictions; tests must distinguish configured from effective values |
| Agent REST/tool writes | `/workspace/wt/skills-tools-1519f/pkg/sysagent/tools/agent_permissions.go::AgentPermissionsTool.Execute` checks self-target, revision, unknown fields, always-ask and Omnipus Ask; C-4 carries the existing REST guard forward | Preserve all checks for new skill inputs; cover every shared-schema writer, MAJ-008 |
| `set_config` sandbox writes | `/workspace/wt/skills-tools-1519f/pkg/sysagent/tools/config.go::blockedConfigKeys/validateConfigKey` refuses the subtree and replacing its ancestors | No sandbox loophole was found in this source review; add the named new-field refusal tests |
| `set_config` compressed override | The separate manifest switch is outside that subtree and the additional refusal belongs to the later Settings package | First-landing visibility-control gap, MAJ-007; it does not defeat a tool Deny |
| Global REST | Complete tool-map replacement, plus proposed merge-only additions | Fix partial-update and cross-tab contract, CRIT-003 |

## Agent-managed object field-path review

This table audits the **changed properties**, not every unchanged agent field. Derived and security-sensitive differences must remain explicit; a single “Agent can set: yes” column is insufficient to prove create/update parity.

| Object / changed field | User UI / API | Agent create path in this spec | Agent update/read path in this spec | Result |
|---|---|---|---|---|
| Agent: `skills.policies` | Agent Tools tab; expanded shared REST configuration | Not defined for `create_agent`; shared REST-create schema gains the field | `set_agent_permissions.skill_policy_changes`; `get_agent.skill_permissions` | MAJ-008: add create parity or clear refusal, and preserve on unrelated updates |
| Agent: skill grants | Existing Skills tab / API | Existing `create_agent.skills` | Existing `update_agent.skills` | Preserve independently of permission migration; project mounts are different authority |
| Skill: category | Install review / `SkillInstallRequest.category` | `install_skill.category` only; W5 authors with file tools | Later category editing deliberately unavailable | MAJ-008: authoring and copy lifecycle not covered by the named install parity test |
| Skill: global permission | Global page / `skill_policies` | Intentionally user-only | Agent config mutation refused | Difference justified; enforce across lifecycle/default initialization |
| Skill: global visibility | Global page / `skill_visibility` | Intentionally user-only | Agent config mutation refused | Difference justified; project identity still missing, MAJ-003 |
| Tool: global permission / visibility | Global page / `policies`, `visibility` | User-only except explicitly commissioned server-default initialization | Sandbox mutation refused, but compressed override is separate | CRIT-003/004 and MAJ-007 |
| Setting: skill trust | Global page / trust API | User-only | Sandbox mutation refused | Difference justified; consent and existing-consumer gaps remain |
| Skill: verified status; agent: effective/global/configured readouts | Derived, not independently user-settable | Must not be caller-asserted | Read-only computation | No writable agent path required; derived status must come from actual provenance/policy |

Keep the named tests `TestSetAgentPermissions_SkillPolicyChangesParity` and `TestInstallSkillCategoryParity`, but broaden their stated coverage and add explicitly named create/authoring/preservation tests in RED. Tool descriptions must change with their schemas. Global/security-sensitive refusals are intentional parity exceptions, not missing features.

## Structural Integrity

PASS below means the section exists with usable structure, **not** that its substantive contents are approved.

| Check | Result | Notes |
|---|---|---|
| Valid `Status:` | PASS | In review |
| ADR linked or no-ADR rationale | PASS for presence; decision closure pending | Header says no open decision, yet FQ-2/FQ-4 remain consequential choices. After interview, architect must apply the existing ADR trigger and dated-correction rules; no automatic new ADR is demanded for settled choices |
| Contract changes first, citing contract sources | PASS for placement; REVISE content | CRIT-003, MAJ-002/008 |
| API/data section | PASS for presence; REVISE content | Project identities, full shared-writer coverage and install review missing |
| Loading/empty/error/partial UI states | PASS for inventory; REVISE transitions | MAJ-002/009/013 |
| End-to-end user journey | REVISE | W5 stops before categorized creation; first-landing Settings and late save errors unresolved |
| Accessibility/keyboard section | REVISE | Disappearing focused rows and view-switch semantics |
| Catalogue-first component choices | PASS for inventory; REVISE one mapping | MIN-001; no ungrounded brand/style objection |
| Security/user promises section | REVISE | CRIT-001–004, MAJ-001/010/012 |
| BDD scenarios with independent oracles | REVISE | Many literal outcomes exist; contradictory and false-green cases remain |
| Requirement → scenario → test traceability | REVISE | FR-040's linked scenarios do not assert its result; R31 requirement excluded; MAJ-014/015 |
| Epic and chief-confirmed riders | WARNING — confirmation incomplete | See coordination warning below; not treated as proof of approval |
| Reachability section | PASS for presence; REVISE completeness | Existing doors named, new create/authoring paths incomplete |
| Specific user-facing documentation TODOs | PASS for presence | DOC-001–009 and the two dated ADR corrections are named; revise for the final consent, migration and first-landing decisions |

### Coordination warning — do not invent epic or rider approval

The spec says the chief still needs to confirm the proposed riders. That confirmation was not supplied. The read-only issue query for #1519 returned OPEN and the title **UI redesign, second round (Settings, Tasks and Team, Chat, Library, Calendar, Onboarding)**; its body explicitly separates first-round Skills & Tools and says “Stage: Requirements. Horizon: Later.” The spec instead says Stage Spec / Horizon Now. **The issue-body mismatch is Verified; current project-board fields and the intended owning epic are Unknown.** Team-lead/chief must reconcile the reference and confirm the rider set before final design approval. This warning alone does not block the review, and no issue was modified.

### Twelve-lens coverage

| Lens | Applied outcome |
|---|---|
| 1. Ambiguity | Ask lifetime, session visibility, autosave timing: MAJ-001/005/009 |
| 2. Incompleteness | Alternate authority, project lifecycle and create/update parity: CRIT-002, MAJ-003/008 |
| 3. Inconsistency / ADR / as-is | Wildcard and visibility migration, confirmation conflict: CRIT-004, MAJ-004/011 |
| 4. Infeasibility | Unbounded mandatory context and guaranteed network-save completion: MAJ-006/009 |
| 5. Contract-first | Mutation shape, pre-install review data and shared DTO writers: CRIT-003, MAJ-002/008 |
| 6. Security | Authorization-before-injection, alternate reads, migration, consent and audit: CRIT-001–004, MAJ-001/010/012 |
| 7. Reachability | Demoted tools, authored categories and unified discovery: MAJ-005/008/015 |
| 8. UI states/journeys | Install review/cancel, old Settings, save recovery: MAJ-002/007/009 |
| 9. Accessibility/keyboard | Filtered-row disappearance, bulk action counts and semantics: MAJ-010/013, MIN-001 |
| 10. Design-system reuse/brand | Existing component contract checked: MIN-001; no invented visual critique without a rendering |
| 11. Testability/false greens | MAJ-014, plus explicit negative oracles throughout |
| 12. Overcomplexity | MAJ-005/007: avoid competing old/new visibility authorities and an ungoverned override. The four founder-chosen levels and two permission maps are not rejected merely for having configuration |

## Test Coverage Assessment

### Missing or inadequate categories

| Category | Required falsifiable observation | Affected scenarios |
|---|---|---|
| Consent before model exposure | Outbound request contains no unique instruction sentinel before approval; does contain it after authorized release | S-10/S-12/S-30/S-31/S-43, ST-10/ST-13 |
| Alternate entry points | With grant present and editor privilege denied, loader, file read and helper path agree on the new policy | S-09/S-10 and new integration cases |
| Concurrency and durability | Different-key edits survive; same-key behavior is explicit; late old response cannot mark a newer draft Saved; unknown outcome is reconciled | S-01/S-07/S-08 and new two-tab/navigation cases |
| Upgrade security | Literal previous and new effective results, not just map presence; restart twice, interrupted migration and later discovered tools | S-16/S-33/S-41/S-59, ST-06/ST-11 |
| Existing-session visibility | Capture both declarations and text after load/demotion/revocation; check search corpus and cache invalidation | S-38/S-40/S-42, ST-12 |
| UI integration across landings | New trust API with surviving old Settings; each cross-link opens a real destination; compressed override governed at the first landing | S-65, FR-065 |
| Installation review | Review metadata belongs to exactly the installed artifact; cancellation and failed/unknown publication are truthful | S-27/S-28/S-55/S-56, ST-09/ST-16 |
| Object parity | Create plus update, authoring plus installation, and preservation during unrelated writes | S-53/S-70/S-71/S-74, ST-15 |
| Audit | Exact changed keys and literal old/new values; no entries implying a refused mutation succeeded; defined persistence/audit failure outcome | FR-040, currently mislinked to S-09/S-37 |
| Keyboard recovery | Focus and announcement after single/bulk filtered removal and after save failure | S-50/S-69 |
| Independent hands-on evidence | Tester and independent validator on the exact cut, with observations that can distinguish missing behavior | ST-01–ST-18; retain the root process, replace weak row oracles |

### Dataset additions

| Dataset | Missing boundaries | Required additions |
|---|---|---|
| D1 permission | Full, Auto, ordinary/trust Ask, remembered consent and pending changes | Full × Allow/Ask/Deny; changed content under same slug; policy/grant change while approval pending; God on/off without mutating stored maps; slash and unattended paths |
| D2 visibility | Existing loaded state and old Full demotion | Fresh/loaded session; each level transition; denied then reallowed; an old Full builtin, not only `serve_web`; skill menu generation |
| D3 trust | Actual legacy behavior and repeat migration | Empty and each legacy value, already-new value, restart twice, audit exactly once, locally modified registry install; explicitly approved Warn-to-Ask difference |
| D4 bulk | Value-specific eligibility and races | Always-ask row; Omnipus Ask restriction; all/none/mixed eligible; two rapid clicks; filter changes before acknowledgement; focused last result |
| D5 install | Review source and authoring lifecycle | Catalog with/without trusted hash; upload metadata; version/artifact change after review; replacement conflict; Cancel during publication; completed W5 and copy |
| D6 mutation | Replacement versus merge and shared writers | Omitted/null/empty maps; valid+invalid keys atomically refused; duplicate canonical skill IDs; two tabs on different keys; agent shared-config omission preservation |
| D7 migration | Effective MCP pipeline and future discovery | Compression × discovery × deferred × preview flag; service offline; future tool; broad wildcard/exact/agent-only policies; preset-authored explicit policies unchanged |
| Context capacity | Aggregate Full payload | Empty, just fits, one over, several individually valid large skills, model switch and content growth |

No proposed expected value should be obtained by calling the new resolver/builder and copying its answer into the assertion. A source fixture can establish old behavior, but the **new** expected result must be independently fixed by the corrected specification. Mutation probes belong to the test-author/CHECK process, not to this read-only review.

## STRIDE Threat Summary

STRIDE covers spoofing, tampering, repudiation, information disclosure, denial of service and elevation of privilege. “Addressed” below means the inspected design retains a relevant control, not that penetration testing passed.

| Component | S | T | R | I | D | E | Evidence / concern |
|---|---|---|---|---|---|---|---|
| Global policy writes | Addressed: signed-in user | Risk | Risk | No new finding | No new finding | Risk | Replacement loses unrelated restriction; per-key tool audit missing, CRIT-003/MAJ-012 |
| Skill activation | Addressed: acting agent/grant identity | Risk: changed artifact/approval | Needs explicit approval audit | Risk: instructions enter before consent | Risk: Full payload capacity | Risk: alternate read/use path | CRIT-001/002, MAJ-001/006 |
| Skill installation | Existing authenticated source path retained | Risk: reviewed versus published artifact | Needs defined review/publication result | No new finding | Existing limits must remain | Risk: trust Ask assumed from tool default | MAJ-001/002 |
| MCP migration | Assignment remains separate | Risk: wrong wildcard precedence | Migration effects need evidence | No new finding | Risk: previously allowed jobs blocked | Risk: broad Deny becomes Ask | CRIT-004 |
| Agent permission mutation | Existing self/revision/locked-agent checks retained | Risk: new field lost by other writer | Existing mutation result must include new field | No new finding | No new finding | Guard preservation required | MAJ-008/010 |
| Save feedback | No new finding | Risk: stale snapshot/retry | Risk: outcome presented as known when it is not | No new finding | No new finding | User can rely on a restriction not actually applied | MAJ-009; retain 503 saved/not-applied distinction |

## Reachability Check

| Question | Answer | Evidence |
|---|---|---|
| Existing tools have actual names and ceiling entries? | Yes, source-verified for the named changed tools; not a new-field execution proof | `/workspace/wt/skills-tools-1519f/pkg/config/defaults.go` contains `Skill`, `install_skill`, `set_agent_permissions`, `get_agent`, `add_mcp_server`; corresponding tool/schema paths were read |
| New skill fields reachable on every promised agent path? | Not yet specified completely | C-10 versus MAJ-008 |
| User-facing screen and entry points named? | Yes; complete journeys still need revision | Spec Reachability names the Skills & Tools route and agent Tools tab; MAJ-002/007/009/013 |
| Test plan describes execution? | Yes | Spec Reachability and Test Implementation Order describe RED/GREEN/CHECK and exact-cut UAT; MAJ-014 corrects what observations prove |
| Implementation tested by this review? | No | Commission is a read-only spec review; no tests/builds/browser runs |

**Code correct and tested:** not assessed as an implementation claim; this is an unimplemented specification review.
**Reachable by a user or agent:** existing entry points were traced; the proposed new behavior is not proven reachable, and specific missing paths are reported.

## Assessment of the author's FQ-1 to FQ-7

A recommendation in the draft is not a founder answer. The instruction that work can continue on recommended choices must not be read as approval to begin implementation before these dependencies are resolved.

| Question | Is this a real open founder decision? | Does option A leave the current spec correct? | Does option B leave the current spec correct? |
|---|---|---|---|
| FQ-1: Full instructions every chat | **Ratification of timing and an earlier decision's reversal**, not a re-vote on four levels. The re-ask already says “full instructions”; whether this means mandatory initial injection needs a precise consent/capacity rule | No, not until CRIT-001 and MAJ-005/006 are fixed and **Skill activation and loading** receives its dated correction | No. Metadata-only changes C-1, FR-038, S-43, ST-13 and DOC-003, and needs to distinguish Full from name+description |
| FQ-2: trust Ask under Auto/God | **Yes**, a consequential exception to ordinary tool behavior | No, not until fresh versus remembered consent, Full, installation and slash behavior agree | No. C-3, FR-030, D1, S-30/S-31, ST-10 and docs currently mandate survival; they must change together |
| FQ-3: new skill Allow or Ask | **Yes**, a shipped default. Its explanation must distinguish installed grant-gated skills from project mounts | A matches written installed defaults but does not fix project identity or Ask/Full interactions | B requires the self-heal rule, new-skill scenarios, UI journey, tests and documentation to change; unattended use may then require an explicit Allow |
| FQ-4: retain compressed override | **Yes**, R30a conflicts with the later Settings decision | Only after the first-landing write guard, correct Settings link, precedence and UI effective-state behavior are specified | Requires coordinated removal/migration and updates to C-6, FR-039, S-44, Settings and docs; simply deleting the switch can change previously uncompressed requests |
| FQ-5: builtin categories | **Product/editorial ratification**, not an unresolved safety mechanism | Category choices can stand; they do not supply the missing authoring metadata path | Safe only after changed mapping, tests and any documentation are updated. No need to reopen architecture for individual category labels |
| FQ-6: formerly unset external tools | **Not a valid binary question as phrased**: exact server-wildcard absence does not prove no effective policy | Blanket Ask can override a broader Deny; corrected coverage/migration matrix required | Blanket Deny can disable existing agent-only Allow; corrected matrix required. A deliberate reversal of R30b needs explicit approval |
| FQ-7: Hidden searchable | **Already specified by R31** absent a later contrary decision. Clarify the wording rather than treating the author recommendation as new authority | A agrees with the cited requirement; fix retention/search integration and MAJ-015 | B is a new product decision and is not implemented by this draft. Skills have a slash path, but the draft does not define a corresponding human-only tool-loading path; contracts, tests and docs must change |

## Unasked Questions for the Author

These are engineering completions, not questions to offload to the founder.

1. Which request schema accepts a skill-only save without replacing unrelated tool policies, and how do existing callers remain compatible?
2. What exact artifact does the install review authorize, and which server result distinguishes cancellation, refusal, persistence and activation?
3. Which identity and stored entries govern each project skill, and when are those entries reconciled or removed?
4. Which generation invalidates each cached menu/search surface, and what survives a visibility edit in an already-running conversation?
5. Which writer owns pending autosaves after the page leaves, and how does an indeterminate response become a truthful recovered state?
6. Which literal request/audit fixtures prove the promises in SC-003, FR-040 and ST-13? Which negative control demonstrates each check can fail?
7. Which existing shared agent-config writers must preserve the new skill-policy map, and how is that proved independently of the dedicated permission endpoint?

## Verdict Rationale

**Verdict: BLOCK.** CRIT-001 and CRIT-003 are direct contract contradictions. CRIT-002 identifies a concrete existing authority path missing from the new enforcement design, and CRIT-004 supplies source-derived counterexamples to the migration premise. These findings stand independently of the unverified epic/board metadata and of any future runtime test result.

Before implementation, settle the founder choices below, then correct the authorization boundaries, mutation/migration contracts and frontend state transitions. Keep the existing strictest-wins resolver, self-target/revision checks, Omnipus Ask prohibition and always-ask exception; do not “fix” this review by replacing them with another permission layer.

## Questions for the Founder

This is the separate interview list for team-lead, **before the correction round**. Suggested answers below are recommendations, not recorded decisions. Answer format: `Q1 A, Q2 A, ...`.

### Q1 — Confirm the always-on implication of Full skills (author FQ-1)

The four levels and “full instructions” were already chosen. The remaining consequence is that selecting Full adds those instructions to each new chat before ordinary conversation, subject to permission and consent, and can consume substantial context.

- **A — Recommended:** Confirm that behavior for Full skills only, with authorization before injection and a visible cannot-fit outcome; correct **Skill activation and loading**.
- **B:** Keep all skill instructions on demand and redefine Full as explicitly named metadata. This changes the draft's Full requirements and tests.

### Q2 — Is unverified-skill consent a fresh safety check or ordinary tool Ask? (author FQ-2)

The current text both says “always asks” and offers remembered Always Allow. It also relies on the installation tool's default Ask, which can be changed. The decision must cover installing and loading, not just one card in one chat.

- **A — Recommended:** A fresh trust check for the artifact being installed or newly loaded, surviving Auto/God and not bypassed by remembered tool consent. Explicit human review can be the consent when it identifies that same artifact; merely requesting an unreviewed name is not automatically equivalent.
- **B:** Ordinary Ask semantics, including applicable Auto/God and remembered-approval behavior, with the reduced guarantee stated in UI and docs.

### Q3 — Default for newly installed skills (author FQ-3)

Registry/global skills start without agent grants, but project skills use mount authority. This default must not silently settle the separate project-identity problem.

- **A — Recommended:** Allow for newly installed grant-gated skills; trust still applies. Require a separately explicit project-shelf rule.
- **B:** Ask for newly installed skills; reflect the extra approval/unattended consequences throughout the spec.

### Q4 — Which control governs tool visibility? (author FQ-4)

R30a removes the compressed switch; the later Settings spec keeps it. Keeping it means a global override can make every allowed tool Full regardless of this page's selections, and its user-only guard must ship with the first landing.

- **A:** Keep the switch, with explicit effective-state UI, a correct Settings destination and the same-landing agent-write refusal.
- **B — Recommended:** Use the four per-tool levels as the single control and coordinate switch removal plus migration with Settings. Do not silently change the outbound behavior of installs that had compression off.

### Q5 — Migration only for genuinely uncovered external-tool access (corrected author FQ-6)

A missing server wildcard is not proof of missing permission. Some installs have broader global restrictions or agent-only values. The architect must first enumerate those states; neither answer authorizes overwriting existing explicit restrictions or treating a mixed service as wholly unset.

- **A — Recommended:** Keep R30b's Ask migration for genuinely uncovered cases, with explicit expected changes and a safe mapping for the other states.
- **B:** Change genuinely uncovered cases to Deny instead, explicitly revising R30b. Existing covered cases still need their own migration mapping.

### Q6 — Include or explicitly defer unified search? (MAJ-015)

R31 requires ToolSearch to find both tools and skills. The draft excludes it without a cited scope decision; Hidden skills would otherwise require a different search tool.

- **A — Recommended:** Keep unified discovery in this package; skills still load through Skill and retain their grants/permissions.
- **B:** Approve a named, tracked follow-up and accept separate discovery in this landing. Record that R31 is only partially delivered.

### Q7 — Does “no confirmation” retain the autonomous-plan safety exception? (MAJ-011)

The existing confirmation warns before granting autonomous plan execution. The latest autosave answer says no confirmation, including Set all, while the draft preserves this exception.

- **A — Recommended:** Preserve only this existing sensitive-grant exception, including Cancel changing nothing; all ordinary autosaves and group edits remain confirmation-free.
- **B:** Remove it too, explicitly superseding that safety affordance and updating the scenarios and user documentation.

**Not new blocking interview decisions:** author FQ-5 can use the proposed category mapping unless the founder changes rows; author FQ-7 should use R31's searchable Hidden meaning unless the founder expressly reverses it. Neither is a reason to postpone the engineering corrections above.

### Recommended Next Actions

1. Team-lead conducts the founder interview above; chief reconciles epic ownership and confirms riders. The author must not treat draft recommendations as answers.
2. The author corrects this one round's findings, updates affected contracts-in-spec, field-path table, documentation TODOs and independent test oracles. Architect records any required dated ADR correction/design decision; implementing leads do not guess it.
3. Run the fixed second specification grill on the corrected artifact. Do not begin implementation on this BLOCK verdict and do not add an extra grill round to evade the fixed process.

### Next step in the process

Verdict: **BLOCK**

Review written to: `/workspace/wt/skills-tools-1519f/docs/internal/specs/skills-tools-redesign-spec-review.md`

This is grill round 1 of 2 (fixed). Next: team-lead interviews the founder on "Questions for the founder", then the spec author fixes round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the corrected spec at `/workspace/wt/skills-tools-1519f/docs/internal/specs/skills-tools-redesign-spec.md` — regardless of this round's verdict.

Skills applied: `grill-spec`, `omnipus-shared-rules`, `ux-heuristics-review`.

## Evidence Table

| Claim | Evidence | Certainty |
|---|---|---|
| Review is pinned to a spec-only change | `git rev-parse HEAD` → exit 0, `5028ff7b0162497e653c0eaaf3ae853c94582fda`; `git diff --stat 4e34afa416..5028ff7b01` → exit 0, one specification file, 1,904 insertions | Verified |
| CRIT-001: Full conflicts with prior consent | Reviewed specification::C-1/C-3/FR-016/FR-017/FR-030/FR-038 | Verified contradiction; inferred runtime consequence |
| CRIT-002: alternate skill-folder authority exists | `/workspace/wt/skills-tools-1519f/pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority`; `/workspace/wt/skills-tools-1519f/pkg/fspolicy/homeguard.go::skillPathDenied`; `/workspace/wt/skills-tools-1519f/pkg/tools/shell_skill_operands.go::loadableSkillReadOrRun` | Verified source; inferred post-change bypass |
| CRIT-003: required replacement conflicts with partial-save promise | `/workspace/wt/skills-tools-1519f/contracts/components/schemas/GlobalToolPolicies.yaml::required`; `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_tool_policies.go::putToolPolicies`; reviewed specification::C-2/D6 | Verified |
| CRIT-004: exact-key absence is not policy absence | `/workspace/wt/skills-tools-1519f/pkg/tools/compositor.go::resolveFromMap/mergeStoredPolicyLayers`; reviewed specification::C-7/FQ-6 | Verified by source-derived counterexamples |
| MAJ-001: consent rules and existing grants do not align | Reviewed specification::C-3/FR-016/FQ-2; `/workspace/wt/skills-tools-1519f/pkg/tools/always_ask.go::AlwaysAskTools`; `/workspace/wt/skills-tools-1519f/pkg/security/approvalgrants.go::ApprovalGrantStore.IsAllowed` | Verified |
| MAJ-002: review facts are unavailable before the specified publication call | `/workspace/wt/skills-tools-1519f/contracts/components/schemas/SkillSearchResult.yaml`; `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_skills.go::installSkill/publishSkillInstall`; reviewed specification::FR-042 | Verified |
| MAJ-003: project identity/default/migration gap | `/workspace/wt/skills-tools-1519f/pkg/skills/shelf.go::ResolveSkillName`; reviewed specification::C-2/C-4/FR-019/Edge Cases | Verified |
| MAJ-004: deferred is not equivalent to Full in compressed requests | `/workspace/wt/skills-tools-1519f/pkg/agent/loop_mcp.go::serverIsDeferred/registerServerTools`; `/workspace/wt/skills-tools-1519f/pkg/tools/manifest.go::ToolManifestTier`; `/workspace/wt/skills-tools-1519f/pkg/agent/tool_manifest.go::buildCompressedToolDefs` | Verified by source, not a captured runtime request |
| MAJ-005: loaded names, frozen menu and search classification require transition rules | `/workspace/wt/skills-tools-1519f/pkg/agent/prompt_cache_sessions.go::restoreLoadedToolRetention/freezeSessionSkillMenu`; `/workspace/wt/skills-tools-1519f/pkg/tools/registry.go::SnapshotSearchableTools` | Verified dependencies; inferred failure if integration omitted |
| MAJ-006: mandatory aggregate content can exceed finite budget | `/workspace/wt/skills-tools-1519f/pkg/skills/authoring.go::MaxSkillMarkdownBytes`; `/workspace/wt/skills-tools-1519f/pkg/agent/context_budget.go::contextBudget`; reviewed specification::FR-038 | Verified missing outcome; no provider behavior asserted |
| MAJ-007: old Settings/late guard remain at first landing | `/workspace/wt/skills-tools-1519f/src/components/settings/SkillTrustSection.tsx::LEVELS`; `/workspace/wt/skills-tools-1519f/pkg/sysagent/tools/config.go::validateConfigKey/performanceOnlyConfigKeys`; Settings specification at `30e105062768c38e362dd29a6f989c71dca56ef5`::FR-060/FR-100 | Verified |
| MAJ-008: create/authoring/preservation paths need coverage | `/workspace/wt/skills-tools-1519f/pkg/sysagent/tools/agent_apply_args.go::agentCreateArgNames`; `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_agents_update.go::updateToolsConfig`; `/workspace/wt/skills-tools-1519f/pkg/skills/embedded/write-skill/SKILL.md::Steps`; reviewed specification::C-10 | Verified |
| MAJ-009: hook flushes, but spec omits uncertain/late outcomes | `/workspace/wt/skills-tools-1519f/src/hooks/useAutoSave.ts::useAutoSave` including serialization, persisted-error and unmount cleanup; reviewed specification::FR-007–FR-009/User Journey | Verified; simplistic unmount-loss allegation rejected |
| MAJ-010/011: bulk actions conflict with retained restrictions | `/workspace/wt/skills-tools-1519f/pkg/tools/always_ask.go::AlwaysAskAllowViolation`; `/workspace/wt/skills-tools-1519f/src/components/shared/ToolPolicyEditor.tsx::resolvesExecutePlanAllow`; reviewed specification::FR-003/FR-007/FR-010 | Verified |
| MAJ-012: tool audit lacks key deltas and mapped scenarios lack audit assertions | `/workspace/wt/skills-tools-1519f/pkg/gateway/rest_tool_policies.go::putToolPolicies`; reviewed specification::FR-040/S-09/S-37 | Verified |
| MAJ-013: focused-row removal has no specified recovery | Reviewed specification::FR-005/Accessibility and Keyboard/S-50 | Verified omission; browser fallback untested |
| MAJ-014: weak observations do not prove their success criteria | Reviewed specification::ST-06/ST-09/ST-13/ST-15/SC-003/SC-005; `/workspace/wt/skills-tools-1519f/docs/internal/false-green-patterns.md` | Verified review of proposed instruments; no test execution |
| MAJ-015: unified search omitted despite R31 | `/workspace/omnipus-coordination/handoffs/ui-redesign/navigation-menu-wireframe/requirements.md::R31`; reviewed specification::Assumptions/Qualitative Prohibitions | Verified conflict; separate waiver Unknown |
| MIN-001: ViewSwitch has radio semantics | `/workspace/wt/skills-tools-1519f/src/components/ui/view-switch.tsx::ViewSwitch`; reviewed specification::Accessibility and Keyboard | Verified |
| Founder-choice assessment uses traced requirements, not recommendation-as-approval | Reviewed specification::FQ-1–FQ-7; `/workspace/omnipus-coordination/handoffs/ui-redesign/skills-tools-wireframe/re-ask-2026-11-20.md::T-Q4b`; requirements source::R30a/R30b/R31/R32; pinned Settings specification | Verified source comparison; answers remain pending |
| Epic body conflicts; chief/board state not established | `gh issue view 1519 --repo elicify-ai/omnipus --json number,title,state,body --jq ...` → exit 0, OPEN, second-round epic, body “Stage: Requirements. Horizon: Later.”; reviewed specification::Riders | Verified body mismatch; intended epic/board/confirmation Unknown |
| No runtime or implementation approval is claimed | Read-only source/spec review; no build, test, browser or production mutation commands used | Verified scope of this review |
| **Self-check** | Re-read the report's findings, verdict and founder choices against the commission and cited source requirements. `python3 -I -` inline report/source checks → exit 0: 20 findings, all 7 required fields, 7 FQ assessments, 7 interview questions, 12 lenses, 95 source references checked; negative controls detected missing fields/findings. Separate whitespace check → exit 0, no trailing whitespace, final newline present. `git status --porcelain=v1 --untracked-files=all` → exit 0, only this report untracked; `git diff --exit-code -- docs/internal/specs/skills-tools-redesign-spec.md` → exit 0, unchanged; HEAD remains `5028ff7b0162497e653c0eaaf3ae853c94582fda`. No product tests run. | Verified — scope and report checks passed; runtime outcomes remain untested |
