Status: In review

ADR: none — no open design decision was routed to an ADR. The founder settled the design in the wireframe answers (T-Q1 to T-Q7, T-Q4b). The contract shapes below are architect decisions recorded in "Contract Changes". Two existing ADRs need a dated correction because of founder answers: [ADR-072 — Skill activation and loading: an on-demand `Skill` tool, the per-agent grant as the real gate, and a project's own skills and instructions](../architecture/ADR-072-skill-activation-and-loading.md) (OQ3 "no always-on skill" is reversed by the T-Q4b "Full" level if ST-Q1 is answered A [pending ST-Q1]) and [ADR-071 — Tool manifest tier redesign](../architecture/ADR-071-tool-manifest-tier-redesign.md) (the fixed tier lists become stored, editable levels). Both corrections are ADR-TODO rows in "User-facing documentation".

# Feature Specification: Skills & Tools redesign (page F)

**Created**: 2026-11-21
**Input**: founder-approved wireframe and notes (`/workspace/omnipus-coordination/handoffs/ui-redesign/skills-tools-wireframe/`: `design-notes.md`, `questions.md`, `re-ask-2026-11-20.md`, `review-response.md`, `index.html`, `app.js`); inventory of today's page (`/workspace/logs/ui-1519/skills-tools/inventory.md`, read at `7faff52915`); UX review (`/workspace/logs/ui-1519/skills-tools/ux-review.md`); requirements R30–R39 (`/workspace/omnipus-coordination/handoffs/ui-redesign/navigation-menu-wireframe/requirements.md`); Settings S-Q8 and `docs/internal/specs/settings-redesign-spec.md` FR-060 on branch `landing/settings-1519` (`30e1050627`). Founder answers, final: T-Q1 A (with the note that Allow / Ask / Deny look and work identically for tools and skills, on this page and in each agent's own settings), T-Q2 A, T-Q3 B, T-Q4b B, T-Q5 (Omnipus chat), T-Q6 A, T-Q7 (this page sets the global default; the agent level is stricter only, reusing today's tool-policy model; no per-agent "what the model sees"). No interview-me output file exists; the wireframe answers take its place.
**Round 1**: fixed after grill round 1 (BLOCK, 4 CRIT, 15 MAJ, 1 MIN); see "Round-1 fix log" at the end. Points that wait for the founder are marked **[pending ST-Qn]** with the recommended option in place (questions ST-Q1 to ST-Q7).
**Code baseline**: release `4e34afa416`. Every `file::symbol` and line below was read at that commit. GitNexus MCP tools were not available in this session; discovery used `grep`/`Read` (impact rows are therefore Inferred).
**Epic**: #1519 as named in the brief. Round 1 found that #1519's body describes the *second* UI round and lists first-round Skills & Tools separately ("Stage: Requirements. Horizon: Later."); the owning epic, Stage and Horizon are **Unknown** until the chief reconciles them.
**Riders** (proposed by the architect; the chief confirms before the design is final): IN — #1920, #1297, #1298, #1411, #99, #1570, #1576. FOR THE CHIEF TO DECIDE — #817 item 1, #1063, #708, #1031, #1380. Details: "Riders" near the end.
**Agent tool field table**: see "Agent tool parity" under Contract Changes (C-10). Objects changed: the agent (per-agent skill permission) and the skill (global permission, what the model sees, category). Global settings (skill trust, tool and skill permission ceilings, levels) stay operator-only, refused to `set_config` for the reason already recorded in `pkg/sysagent/tools/config.go::blockedConfigKeys` ("sandbox").

---

## Overview

Today, what an agent may use is decided in three places. The Skills & Tools page only lists skills, connected services and built-in tools; it points to Settings → Security for the global Allow / Ask / Deny of every tool, and Settings also holds the skill trust choice. Each agent's own Tools tab holds that agent's stricter values and three role presets. Skills have no Allow / Ask / Deny of their own: one tool, `Skill`, is a single switch for every skill. How much the model is told about each tool is fixed in code, and the per-skill level does not exist.

This package makes Skills & Tools the one place for the global answer to "may this run, and what does the model see about it", for Omnipus tools, external (MCP) tools and skills. Every tool and every skill gets the same Allow / Ask / Deny control, here and in each agent's Tools tab, where an agent can only be stricter (founder T-Q1, T-Q2, T-Q7). Skill trust becomes the same three words for unverified skills (T-Q1 A). Every tool and skill gets one of four "What the model sees" levels: Hidden, Name only, Name and short description, Full (T-Q4b B), set globally only (T-Q7). Every change saves on its own, with no Save button and no confirm, including "Set all" (T-Q3 B) — except the one existing warning before autonomous plan execution is allowed [pending ST-Q7]. The three role presets go away on this page and on every agent's Tools tab (T-Q6 A). The New skill menu opens the Omnipus chat to write a skill (T-Q5), and an install asks for a category (R32).

Settings (page G) removes tool permissions, skill trust and web search from Settings because this page carries them; Settings is cut only after this page has landed (`settings-redesign-spec.md` FR-060). This package therefore also moves the Web & Search provider settings here as a side panel (R30a).

## Where this spec departs from the wireframe (answers win)

| Wireframe draws | This spec | Why |
|---|---|---|
| A Save bar (Save changes / Discard, "Leave without saving?") and a confirm before a page-wide or 10+-row Set all (T-Q3 D, mapping N19–N21, N26) | No Save bar, no Discard, no leave prompt, no Set all confirm. Every value saves on its own a moment after the click, with a "Saved" signal. The only dialog left is the existing autonomous-plan warning (FR-010) [pending ST-Q7] | T-Q3 **B**: autosave everywhere, no confirm; a wrong Set all is undone by hand |
| Skill trust as "Unverified skills" with Block / Warn / Allow all | The same block, with the words **Allow / Ask / Deny** in that order, the same badges as every row | T-Q1 **A** |
| Model context levels Full description / middle / Hidden, with per-type middle labels | Four levels, one set of labels for tools and skills: Hidden, Name only, Name and short description, Full. Option help text says what each means for a tool or a skill | T-Q4b **B** ("works almost the same for tools and skills") |
| "Write it in a chat" — a new chat that first asks which agent writes the skill | "Write with Omnipus" — opens the Omnipus chat and starts the write-skill workflow, the way "Create with Omnipus" starts the create-agent workflow | T-Q5 |
| Model context per-agent override in the agent's Tools tab (T-Q7 A option) | No per-agent level. The agent's Tools tab shows permissions only | T-Q7 founder answer |
| Agent panels keep their presets (question left open) | Presets removed from every agent's Tools tab in this build | T-Q6 **A** |
| "Not saved" badge on the trust row; Save bar text for Allow all | The Allow warning is a callout under the trust row; no Save bar exists to overlap it on a phone | follows from T-Q3 B |

---

## Existing Codebase Context

GitNexus was not available; every row below is from `grep`/`Read` at `4e34afa416`.

### Symbols Involved

| Symbol | Role | Context (file:line at `4e34afa416`) |
|---|---|---|
| `pkg/tools/compositor.go::resolveEffectivePolicyWith` | extends (skill policies reuse it) | `compositor.go:163`. Global verdict × agent verdict, strictest wins (deny > ask > allow) in `mergeStoredPolicyLayers` (`:203`). God Mode floors the global side to allow, except always-ask tools (`:191`). Both sides empty → deny, logged at Error (`:210`) |
| `pkg/tools/compositor.go::FilterToolsByPolicy` | calls / extends | `compositor.go:471`. Drops denied tools and unassigned MCP tools before the tool list and the manifest are built (`pkg/agent/loop_run_turn.go:890`) |
| `pkg/config/validate.go::ReconcileToolPolicyCeiling` | extends (pattern for skills) | `validate.go:707`. Adds the shipped default for every static tool missing from the global map; never overwrites an operator value; called from `pkg/gateway/gateway_sandbox.go:276` |
| `pkg/config/sandbox.go` `Sandbox.ToolPolicies`, `SkillTrust`, `GodMode`, `AutoApprove` | modifies | `sandbox.go:523`, `:501`, `:567`, `:631` |
| `pkg/config/config.go` `AgentToolsCfg` | modifies | `config.go:767` (per-agent `builtin.policies`, `mcp.servers`) |
| `pkg/agentmutation/tool_policy.go::SelectOverrides`, `::ApplyToolPolicyChanges` | reused | `tool_policy.go:72`, `:37` — sparse override intent; patch with `set`/`remove` |
| `pkg/tools/skill.go::SkillTool` | modifies | `skill.go:112`; `Name()` = `"Skill"` (`:184`); `execLoad` (`:240`), `execSearch` (`:270`). The grant list is the only per-skill gate today (`pkg/agent/skill_identity.go:33`, ADR-072 D4) |
| `pkg/skills/loader.go::BuildSkillsSummaryFuncWithProject` | modifies | `loader.go:410` — the "# Skills" menu block: one entry per granted skill, name + description |
| `pkg/tools/manifest.go` `fullManifestToolNames`, `infraManifestToolNames`, `previewedLazyToolNames`, `BuildCompressedManifest` | modifies | `manifest.go:58`, `:80`, `:145`, `:310` — fixed lists: 36 tools sent in full, ToolSearch as infra, `serve_web` previewed as one line, every other tool search-only |
| `pkg/agent/tool_manifest.go::buildCompressedToolDefs`, `::buildToolManifestNote` | modifies | `tool_manifest.go:192`, `:288` |
| `pkg/config/config.go` `ToolsManifest.Compressed`, `PreviewAllLazy`; `MCPServerConfig.Deferred` | reads / retires `Deferred` | `config.go:1970`, `:1991`, `:2427`; `pkg/agent/loop_mcp.go:805` `serverIsDeferred` |
| `pkg/tools/tools_tool.go` ToolSearch | unchanged | `tools_tool.go:94`; BM25 over tools only, permission-filtered |
| `pkg/gateway/rest_tool_policies.go::HandleToolPolicies`, `::putToolPolicies` | modifies | `:41`, `:68`; no re-auth; audit `tool_policies_update` (`:188`) |
| `pkg/gateway/rest_tools.go::updateAgentTools` | modifies | `:104`; locked system agent 403 (`:~141`), external worker 400 (`:~152`), Omnipus per-agent Ask 422 (`rest_omnipus_guards.go:57`) |
| `pkg/gateway/rest_skill_trust.go::HandleSkillTrust`, `::putSkillTrust` | modifies | `:43`, `:66`; audit `EmitSecuritySettingChange("sandbox.skill_trust", old, new)` (`:109`) |
| `pkg/gateway/rest_skills.go` list handler | modifies | `rest_skills.go:90`: the list's `verified` is true only for built-in skills; the registry install result's `Verified` (`:550`) is not stored |
| `pkg/skills/clawhub_registry.go` install | modifies | hash check `:365-377`; a mismatch always fails; `Verified` is set only when the registry supplied a hash |
| `pkg/tools/skills_install.go` `install_skill` | modifies | `:125`; global default Ask (`pkg/config/defaults.go:268`) |
| `pkg/sysagent/tools/agent_permissions.go::AgentPermissionsTool` (`set_agent_permissions`) | modifies | `:38`; `tool_policy_changes` patch; reports clamps (`:204`, `:221`) |
| `pkg/sysagent/tools/agent_get.go` (`get_agent`) | modifies | `:47`; `AgentDetail.tool_permissions` |
| `pkg/sysagent/tools/config.go::blockedConfigKeys` | reused | `config.go:~360`: the whole `sandbox` subtree is refused to `set_config` |
| `src/components/shared/ToolPolicyEditor.tsx::ToolPolicyEditor` | modifies | `:706`; role preset selector `:821-852`; `DISCOVERY_TOOL_NAME` `:70`; the execute_plan confirm `:902-930` |
| `src/lib/toolPolicyPresets.ts` | deletes | 205 lines; only importer is `ToolPolicyEditor.tsx` (and its test) |
| `src/components/agents/ToolsAndPermissions.tsx` | modifies | `presetsDisabled` `:563`; autosave via `useAutoSave` |
| `src/components/screens/SkillsScreen.tsx::SkillsScreen` | rewrites | 687 lines; three tabs, cards, read-only built-in overview, "Settings → Security" link |
| `src/components/settings/SkillTrustSection.tsx`, `SecuritySection.tsx::GlobalToolPoliciesSection`, `WebSearchGroup.tsx` | moves out of Settings | Settings package removes them from Settings (FR-060); this package hosts them |
| `src/store/omnipusChat.ts::openOmnipusChat`, `src/hooks/useOpenOmnipus.ts::useOpenOmnipus` | reused | `omnipusChat.ts:56`; workflow start W1/W18 today; `pkg/coreagent/prompts_adr090.go:102` routes `write-skill` as W5 |
| `pkg/skills/embedded/orchestrate/SKILL.md` | modifies (rider #1920) | line 18 names the retired Ava and Admin agents |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| Skill policy resolution added beside `resolveEffectivePolicyWith` | **HIGH** — every skill load of every agent | `SkillTool.execLoad`, `/skill` command path (`pkg/agent/loop.go::applyExplicitSkillCommand`), `delegate` `requested_skill` (ADR-072 D9) | every chat, task, plan and heartbeat turn that loads a skill |
| `BuildSkillsSummaryFuncWithProject` | **HIGH** — the cached system prompt of every agent | `pkg/agent/context.go:~797` "# Skills" section (3 call sites, 2 files) | prompt-cache prefix of every turn (ADR-072 D1, D8) |
| `manifest.go` tier lists → stored levels | **HIGH** — what every agent is sent each turn | `ToolManifestTier` (13 call sites, 9 files), `buildCompressedToolDefs`, `buildToolManifestNote` | every turn's tool list; prompt cache (#750) |
| `GlobalToolPolicies` PUT | MEDIUM | `putToolPolicies`, SPA editor | running-agent reload (`503` path) |
| `AgentToolsCfg` / PUT `/agents/{id}/tools` | MEDIUM | `updateAgentTools`, `set_agent_permissions`, `get_agent` | agent store, agent reload |
| Skill trust enforcement at install and load | **HIGH** — security-relevant, focus area | REST install (`rest_skills.go`), `install_skill`, `SkillTool.execLoad` | every install path; every unverified skill load |
| Skill folder authority (`pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority`, `pkg/fspolicy/homeguard.go::skillPathDenied`, kernel grant via `turnSkillFolders`) filtered by the skill outcome (C-11) | **HIGH** — security focus area `pkg/fspolicy` | file tools, `bash` helper operands (`pkg/tools/shell_skill_operands.go::loadableSkillReadOrRun`) | every turn's skill-folder access |
| One-time migrations (C-18, C-19, D7) | **HIGH** — changes stored permissions of every install | config load, `ReconcileToolPolicyCeiling` ordering | every agent after upgrade |
| `ToolPolicyEditor` (preset removal, Skills group) | MEDIUM | `ToolsAndPermissions.tsx`, `SecuritySection.tsx` (leaves Settings) | agent profile Tools tab |

The six HIGH rows need sign-off before GREEN starts. They touch `pkg/agent` and the tool-policy engine, so hands-on UAT on the cut commit is mandatory (rows ST-01 to ST-24 below).

---

## Contract Changes (contract-first — Hard Constraint #8)

The architect decides the shapes; backend-lead edits `contracts/` and regenerates (`scripts/gen-contracts.sh`). Nothing below is implemented by this spec.

### C-0 How T-Q7 maps onto today's policy layers (the base)

| Founder words (T-Q7) | Today's mechanism, reused unchanged | Evidence |
|---|---|---|
| "The Skills & Tools page sets the GLOBAL default" | Layer 1, the global ceiling `sandbox.tool_policies`, complete for the static catalog, self-healed by `ReconcileToolPolicyCeiling`; written by `PATCH /security/permissions` (this page, C-2) and, until the Settings package lands, `PUT /security/tool-policies` (today's Settings screen) | root `CLAUDE.md` line 107 (Hard Constraint #6); `pkg/config/sandbox.go:523`; `pkg/config/validate.go:707`; `pkg/gateway/rest_tool_policies.go:68` |
| "The agent level sets per-agent permissions and can only be STRICTER" | Layer 2, sparse per-agent overrides `tools_cfg.builtin.policies`, merged strictest-wins | `pkg/config/config.go:767`; `pkg/tools/compositor.go:163`, `:203` |
| "If the global policy says Ask, the agent cannot say Allow" | The SPA locks the looser badges (`ToolPolicyEditor.tsx::isPolicyLocked`, `:134`); the server stores a looser value but floors it at run time; `set_agent_permissions` reports the clamp | `ToolPolicyEditor.tsx:126-156`; `compositor.go:203-224`; `agent_permissions.go:204`, `:221` |
| "Reuse it, do not reinvent it" | Skills get the **same two layers and the same merge function**: a global skill map (complete over installed skills) and a sparse per-agent skill map. No third stored layer, no default fallback, no backfill (Hard Constraint #6; `scripts/check-no-fail-closed-backfill.sh`) | this spec C-2, C-4 |
| "No separate per-agent 'what the model sees' setting" | Levels are global only; an agent's Deny (effective) still removes the item from what the model sees, as today | `FilterToolsByPolicy` (`compositor.go:471`) runs before the manifest is built |

The full per-skill decision, and every place that must consume it, is C-11; consent is C-12. Two resolution-time rules sit beside the merge and are **not stored layers**, exactly like today's always-ask rule (`pkg/tools/always_ask.go:18`): God Mode floors the global side to Allow (unchanged), and the skill-trust floor for unverified skills (C-3). This is the same shape as the always-ask exception and is flagged for security-lead (focus area: `pkg/policy`-equivalent code in `pkg/tools`).

### C-1 New schema `ModelVisibility`

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `ModelVisibility` | new | `contracts/components/schemas/ModelVisibility.yaml` | string enum `hidden`, `name_only`, `name_and_description`, `full` |

Meaning (the oracle for every scenario below):

| Value | On screen | A tool | A skill |
|---|---|---|---|
| `full` | Full | Its complete definition is sent every turn; callable at once | Its whole instructions are in the agent's context from the start of every chat **only when the skill's outcome for that agent and chain is `allowed`**; otherwise it is listed as Name and short description (C-14) [pending ST-Q1] |
| `name_and_description` | Name and short description | Listed as one line: name and the first line of its description (at most 140 characters); loaded through ToolSearch before use | Listed in the skill menu with name and description (today's behaviour) |
| `name_only` | Name only | Listed by name alone; loaded through ToolSearch before use | Listed in the skill menu by name alone |
| `hidden` | Hidden | Not listed; ToolSearch can still find and load it (founder default, former FQ-7) | Not listed; `Skill` search, ToolSearch (C-17) [pending ST-Q6] and the person's `/name` command can still find it; loading follows C-11 |

An effective Deny for an agent overrides every level for that agent: the item is not listed, not found and not callable (today's rule, `compositor.go:471`).

### C-2 Global permissions and levels: a read model and a per-key change request (rewritten in round 1, CRIT-003, MAJ-012)

Round 0 added merge members to `GlobalToolPolicies`, whose PUT still required and replaced the whole `policies` map (`contracts/components/schemas/GlobalToolPolicies.yaml` `required: [policies]`; `pkg/gateway/rest_tool_policies.go::putToolPolicies`). A tab that changed one skill would have had to send its cached tool map and could undo another tab's Deny. Round 1 gives this page its own resource that never needs the full map.

| Schema / endpoint | New / Changed | File | Notes |
|---|---|---|---|
| `GET /security/permissions` | new | `contracts/openapi.yaml` | returns `PermissionSettings`; signed-in person (same auth as `/security/tool-policies`) |
| `PATCH /security/permissions` | new | `contracts/openapi.yaml` | body `PermissionSettingsPatch`; 200 `PermissionSettings`; 400/403/409 `ErrorResponse`; 503 saved-not-applied (existing text) |
| `PermissionSettings` | new | `contracts/components/schemas/PermissionSettings.yaml` | complete stored maps: `policies` (tool → `ToolPolicy`, as `GlobalToolPolicies.policies`), `skill_policies` (skill key → `ToolPolicy`), `visibility` (tool name or `mcp_<server>_*` → `ModelVisibility`), `skill_visibility` (skill key → `ModelVisibility`), `skill_trust` (`allow`/`ask`/`deny`) |
| `PermissionSettingsPatch` | new | `contracts/components/schemas/PermissionSettingsPatch.yaml` | the same five members, all optional; each map names **only the keys to change** |
| `GlobalToolPolicies`, `GET/PUT /security/tool-policies` | unchanged shape | — | kept for today's Settings screen (C-22). PUT gains the per-key audit below |

`PATCH` rules (the oracle for Dataset D6):

- An omitted member and an empty map change nothing. A request that changes no key at all → 400 "Nothing to change.".
- `null` for a member or a value → 400 naming the key; there is no delete through PATCH (keys disappear only with their skill or service).
- The whole request is validated before anything is written; one bad key → 400 naming the first bad key, nothing saved.
- Tool keys are validated as the PUT validates them today (`ValidateSubmittedToolPolicyMap`, the `mcp_` rule). Skill keys must be a known skill key (C-19), compared case-insensitively; two keys in one request that are the same skill → 400 "`plan` is listed twice.".
- Per-key guards: always-ask tools cannot be `allow` (`pkg/tools/always_ask.go::AlwaysAskAllowViolation`); `ToolSearch` and `Skill` accept only `allow` (C-6); locked levels accept only their locked value (C-6); `skill_trust` accepts only `allow`/`ask`/`deny`.
- Applied under the existing config write lock: read stored config, change only the named keys, write atomically, reload agents. A key the request does not name is never written, so two tabs changing **different** keys both keep their change. Two tabs changing the **same** key: the later acknowledged write wins; every response returns the complete stored `PermissionSettings`, and the page replaces its view with it, and re-reads on window focus.
- Audit: one `EmitSecuritySettingChange` per **changed** key with actor, old and new value, computed from the stored config inside the write lock — resources `sandbox.tool_policies.<key>`, `sandbox.skill_policies.<key>`, `sandbox.tool_visibility.<key>`, `sandbox.skill_visibility.<key>`, `sandbox.skill_trust`. A key sent with its stored value writes no entry. A refused request writes no change entry. The legacy `PUT /security/tool-policies` gets the same per-key entries (today it logs only `policy_count`). An audit-write failure is handled as for every security setting today (logged at Error by the audit package; the save result is unchanged) — this package does not change audit durability, and says so in DOC-004.

Storage (architect decision): `sandbox.skill_policies`, `sandbox.tool_visibility`, `sandbox.skill_visibility` in `config.json`, beside `sandbox.tool_policies`; trust stays `sandbox.skill_trust`, now `allow`/`ask`/`deny`. Reason: the whole `sandbox` subtree is already operator-only and refused to `set_config` (`pkg/sysagent/tools/config.go::blockedConfigKeys`).

Self-heal (same pattern as `ReconcileToolPolicyCeiling`, additive, never overwriting, runs after the one-time migrations of C-19): a skill key with no stored global permission gets **Allow** [pending ST-Q3] and one with no stored level gets `name_and_description`; a tool with no stored level and no matching server key gets its starting level (C-6). Removing a skill removes its two keys. Retired keys are not re-added.

### C-3 Skill trust becomes Allow / Ask / Deny

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `SkillTrustResponse` | unchanged at this landing | `contracts/components/schemas/SkillTrustResponse.yaml` | legacy enum, mapped (C-22); `PermissionSettings.skill_trust` carries the new enum |
| `SkillTrustUpdateRequest` | unchanged at this landing | `contracts/components/schemas/SkillTrustUpdateRequest.yaml` | keeps the legacy enum for today's Settings screen, mapped to the stored value (C-22). The new page writes trust as `PermissionSettingsPatch.skill_trust` (`allow`/`ask`/`deny`; any other value → 400 "Use allow, ask or deny.") |
| `SkillTrustUpdateResponse` | unchanged at this landing | `contracts/components/schemas/SkillTrustUpdateResponse.yaml` | legacy enum (C-22). The Allow warning text "Allow turns off the check for unverified skills." is shown by the new page itself |

Config: `sandbox.skill_trust` stores `allow`/`ask`/`deny`. On load, a legacy stored value is rewritten once: `block_unverified` → `deny`, `warn_unverified` → `ask`, `allow_all` → `allow`; an empty value is treated as `ask` (today's empty value means `warn_unverified`, `rest_skill_trust.go:47-49`). The rewrite writes one audit entry.

Meaning (applies to **unverified** skills only, C-5):

| Trust | Install of an unverified skill (screen, REST, `install_skill`) | Load of an unverified skill by an agent |
|---|---|---|
| `allow` | proceeds | follows the skill's own permission only |
| `ask` | proceeds only with a person's review of that exact revision: the install review (screen, C-13) or a trust card bound to the staged revision (`install_skill`, C-12); `POST /skills/install` without a review → 409 | the trust check of C-12: a trust card unless that revision was approved, also under Auto-approve and in God Mode [pending ST-Q2] |
| `deny` | refused: "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed." | refused: "`<skill>` is unverified, and unverified skills are set to Deny in Skills & Tools." |

The trust floor is a resolution-time rule, not a stored layer (C-0). It survives God Mode, like the always-ask rule [pending ST-Q2]. Its consent rules are C-12; the upgrade order is C-19.

### C-4 Per-agent skill permissions: `AgentToolsCfg`, `AgentToolsUpdateRequest`, `AgentToolsResponse`

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `AgentToolsCfg` | changed | `contracts/components/schemas/AgentToolsCfg.yaml` | adds `skills: { policies: object<skill id, ToolPolicy> }`, sparse overrides only |
| `AgentToolsUpdateRequest` | changed | `contracts/components/schemas/AgentToolsUpdateRequest.yaml` | adds optional `skills: { policies }`: omitted = unchanged; `{}` = clear every skill override; a key not an installed skill → 400; the Omnipus per-agent Ask rule (`rest_omnipus_guards.go:57`) applies to these values too (422, existing text); locked system agent 403 and external worker 400 apply unchanged |
| `AgentToolsResponse` | changed | `contracts/components/schemas/AgentToolsResponse.yaml` | adds required `skills: AgentSkillEntry[]` |
| `AgentSkillEntry` | new | `contracts/components/schemas/AgentSkillEntry.yaml` | `name` (skill id), `display_name`, `granted` (bool), `configured_policy` (`ToolPolicy` or null = no override), `global_policy` (`ToolPolicy`), `effective_policy` (`ToolPolicy`, after trust floor and God Mode), `unverified` (bool) |

The per-agent value is stored as given (like tools today) and floored at run time; the screen locks looser badges; `set_agent_permissions` reports the clamp (C-0). Every other writer of the agent's tool settings keeps `skills.policies` when it does not name it (C-21). Project skills are keyed `project:<workspace>/<slug>` (C-19).

### C-5 Skill fields: `Skill`, `SkillInstallRequest`, `SkillCategory`

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `SkillCategory` | new | `contracts/components/schemas/SkillCategory.yaml` | enum `tools`, `business`, `development`, `testing_security`, `data_ai`, `devops`, `documentation`, `content_media`, `research`, `lifestyle`, `databases`, `blockchain`, `uncategorised` (R32's 12 SkillsMP categories) |
| `Skill` | changed | `contracts/components/schemas/Skill.yaml` | adds required `category` (`SkillCategory`); `verified` redefined: true for a built-in skill, or for a skill whose install was checked against a registry hash **and** whose folder has no local changes since; false otherwise |
| `SkillInstallRequest` | changed | `contracts/components/schemas/SkillInstallRequest.yaml` | adds optional `category` (`SkillCategory`). Absent → the file's `metadata.category` when it is one of the values, else `uncategorised` |

The category and the install-time verification result are stored in Omnipus's own data, outside the skill folder (R32: never written into the skill file). A built-in skill takes its category from its own `metadata.category` (Omnipus-owned file; values in Assumptions, decided by founder default). The category is set at install; a skill stored as Uncategorised can have it chosen once (C-21); after that no edit exists (R32, UX review F21).

### C-6 `ToolRegistryEntry` carries the level and the fixed rows

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `ToolRegistryEntry` | changed | `contracts/components/schemas/ToolRegistryEntry.yaml` | adds required `visibility` (`ModelVisibility`, global), `visibility_locked` (bool), `permission_fixed` (bool) |

- `permission_fixed: true` for `ToolSearch` and `Skill`: no Allow / Ask / Deny on screen; every Set all skips them. `ToolSearch` keeps today's sentence; `Skill` shows "Permissions are set per skill on the Skills tab." with a link (T-Q2 A). Their stored global value is `allow` and is not editable (PUT with another value → 400 "`<tool>` has no permission of its own.").
- `visibility_locked: true` for `ToolSearch`, `Skill`, `AskUserQuestion`, `goal_claim`, `set_goal` (R31's list; `switch_agent` in R31 does not exist at `4e34afa416`). Locked value: `full` (ToolSearch shows "Always available for discovery." instead of a level).
- Starting levels are derived from the **whole** pre-upgrade request pipeline, not from labels (round-1 MAJ-004); Dataset D7 is the literal table. In short: with `tools.manifest.compressed` on, the 36 tools in `fullManifestToolNames` → `full`, `serve_web` → `name_and_description`, every other static tool → `hidden` (`name_and_description` when `preview_all_lazy` is on); every MCP tool → `hidden` (it is lazy and search-only in a compressed request, whatever `deferred` says — `pkg/tools/manifest.go::ToolManifestTier`), except that with `preview_all_lazy` on a tool registered visible → `name_and_description`. With `compressed` off, every static tool → `full`; an MCP server that is effectively deferred (`pkg/agent/loop_mcp.go::serverIsDeferred`: `tools.mcp.discovery.enabled` on and `deferred` unset or true) → `hidden`, otherwise → `full`.
- The MCP starting level is stored per **server** as `visibility["mcp_<server>_*"]`, so a server that is offline at upgrade, and tools a server adds later, get the migrated level; an exact tool key overrides the server key (same exact-then-wildcard rule as policies).
- After the one-time migration (marker in `sandbox.migrations`), `tools.manifest.compressed`, `preview_all_lazy` and `MCPServerConfig.deferred` are no longer read [pending ST-Q4]; stored values stay in place, ignored, with one warning at load; `set_config` refuses them (C-21).
- `AgentToolEntry.manifest_tier` stays and is derived: `full` → `full`; `name_and_description`, `name_only`, `hidden` → `compressed`; ToolSearch → `infra`.

"Load tools on demand" (`tools.manifest.compressed`): [pending ST-Q4] option B in place — the four levels are the only control; an install that had the switch off is migrated so every agent is sent the same as before (the `compressed`-off rows of D7). Option A (switch kept): levels apply only while the switch is on; when it is off every allowed tool except effectively deferred MCP tools is sent in full, the tool tabs show "Load tools on demand is off in Settings, so every allowed tool is sent in full. These levels apply when it is on.", and `deferred` keeps being read. Switch point: this paragraph, D7, FR-037, FR-039, S-44, C-21 last row, C-22.
- Changing a level while chats run: C-15.

### C-7 New service tools start at Ask; no "Unset"

No schema change. Behaviour of `POST /mcp-servers` and the `add_mcp_server` agent tool: adding a service writes the global key `mcp_<server>_*` = `ask` when no global key already covers the service, which covers every tool the service has now and adds later (R30b). The one-time upgrade and the exact meaning of "covered" are C-18 (round-1 CRIT-004 replaced the round-0 "no exact server key" rule). After this, every tool row has a value; the screen has no "Unset" state (R30b).

### C-8 `WorkflowStart` gains the write-skill workflow

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `WorkflowStart` | changed | `contracts/components/schemas/WorkflowStart.yaml` **and** the inline copy in `contracts/asyncapi.yaml` (kept in sync by hand, per the schema's own description) | `workflow` enum adds `W5` (write-skill, `pkg/coreagent/prompts_adr090.go:102`). `target_workspace_id` becomes required for `W1` and `W18` only; for `W5` it is optional: absent from Skills & Tools, where the write-skill card proposes a global skill and still lets the person pick a workspace's project shelf (the workflow already supports both, `pkg/skills/embedded/write-skill/SKILL.md`) |

### C-9 Approval card names the skill

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `ToolApprovalRequiredFrame` | changed | `contracts/components/schemas/ToolApprovalRequiredFrame.yaml` | adds optional `skill` object: `name`, `display_name`, `unverified` (bool). Present when `tool_name` is `Skill` and the call loads a skill |
| `AgentSkillPermission` | new | `contracts/components/schemas/AgentSkillPermission.yaml` | `skill`, `effective` (`ToolPolicy`), `override` (`ToolPolicy` or null) — the skill twin of `AgentToolPermission` |
| `AgentDetail` | changed | `contracts/components/schemas/AgentDetail.yaml` | adds `skill_permissions: AgentSkillPermission[]` (returned for `fields: ["skill_permissions"]`) |

### C-10 Agent tool parity (field table)

Replaced in round 1 by C-21, which lists every create, update and read path separately (MAJ-008). The parity tests named there supersede the round-0 list.

### C-11 Every way a skill's instructions or files reach an agent follows one decision (round-1 fix, CRIT-001, CRIT-002)

The per-skill decision is computed once per agent and turn by one function beside `pkg/tools/compositor.go::resolveEffectivePolicyWith` (C-0): **grant (or project authority) → global skill value × agent override (strictest wins) → God Mode floor on the global side → trust floor for an unverified skill → consent state (C-12)**. Its outcome is one of `deny`, `ask_pending`, `allowed`. Every entry point below consumes that outcome; none computes its own.

| # | Entry point at `4e34afa416` | What it gives the agent | Rule under this spec |
|---|---|---|---|
| E1 | `Skill` load: `pkg/tools/skill.go::SkillTool.execLoad` → `pkg/agent/loop_wire.go` → `pkg/agent/skill_activation.go::loadSkillActivation` | the instructions | `allowed` → loads; `ask_pending` → approval card (interactive) or refusal (unattended); `deny` → refused naming the setting |
| E2 | Skill menu: `pkg/skills/loader.go::BuildSkillsSummaryFuncWithProject` | name / description | listed at its level only when the outcome is not `deny` |
| E3 | `Skill` search: `pkg/tools/skill.go::SkillTool.execSearch` | name / description | found only when not `deny` |
| E4 | Person's `/name`: `pkg/agent/loop_slash.go::applyExplicitSkillCommand` (sets `ForcedSkills`) | the instructions | the person asking by name is the consent for an ordinary skill Ask (unchanged from round 0); `deny` and a trust `ask_pending` are **not** bypassed: trust Ask shows the trust card first [pending ST-Q2] |
| E5 | Delegation: `delegate` `requested_skill` → `pkg/agent/subturn_identity.go::resolveRequestedSkillForChild` | the instructions, in the child | decided for the **child** agent; `ask_pending` raises the card in the root chat (interactive) or refuses (unattended) |
| E6 | File tools on a skill folder: `pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority` → `pkg/skills/observe.go::LoadableFolders` → `pkg/fspolicy/homeguard.go::skillPathDenied`; kernel grant via `turnSkillFolders` | the files, including the instructions | a folder is in the loadable set only when the outcome is `allowed`. `deny` and `ask_pending` folders are left out, exactly as an ungranted skill's folder is today, in the app-level guard **and** in the kernel grant (both read `resolveTurnSkillAuthority`) |
| E7 | Helper execution: `pkg/tools/shell_skill_operands.go::loadableSkillReadOrRun` | runs helper scripts | same folder set as E6, so same rule |
| E8 | Full level (C-1, new) | the instructions, from the first turn | injected only when the outcome is `allowed` for that agent **and** chain (C-14). `ask_pending` → listed as Name and short description; the load then goes through E1 |
| E9 | Unified search (C-17, new) [pending ST-Q6] | name / description | found only when not `deny`; never loads |

**Editor exception, kept separate.** Today `resolveTurnSkillAuthority` sets `readAll` when `edit_skill` is not denied, so an agent allowed to edit skills can read every skill folder for authoring. This stays. It is read-for-editing only: it never injects instructions, never counts as a load, never records consent, and never adds a folder to the E7 helper set. A Deny on a skill therefore does not stop an editor agent from *reading* its files; the skill's own Deny stops it being *used*. The page says so in the `edit_skill` row's help text: "Agents allowed to edit skills can read every skill's files, including denied ones." Text already disclosed to a model earlier in a chat cannot be withdrawn; a later Deny applies from the next turn (C-15).

### C-12 Consent: ordinary skill Ask versus the trust check (round-1 fix, MAJ-001) [pending ST-Q2]

| Situation | Ordinary skill Ask (verified skill, or trust Allow) | Trust check (unverified skill, trust Ask) — option A in place |
|---|---|---|
| Interactive chat, Auto-approve off | approval card: Approve Once, Deny, Always Allow | trust card: "Approve this version", Deny. No Always Allow |
| Interactive chat, Auto-approve on | follows the existing Auto rule for the `Skill` tool (`pkg/tools/auto_approve.go`: `Skill` is `AutoRuns`) — loads with no card | trust card still shown |
| God Mode | global side floored to Allow (as for tools); a per-agent Ask still asks | trust card still shown |
| Remembered "Always Allow" (`pkg/security/approvalgrants.go::ApprovalGrantStore.IsAllowed`, keyed by session, agent, tool, arguments) | the remembered grant's arguments are `{name, revision}` (revision = `pkg/skills/capture.go::ContentRevision` of the folder); a changed skill asks again | never consulted |
| Where consent is stored | the existing session grant store (per chat) | a new installation-wide record "trust approvals": (skill key, revision, who, when) in Omnipus's own data, outside the skill folder; written only by a person's action |
| What counts as consent | Approve Once / Always Allow on the card | (a) "Approve this version" on a trust card; (b) installing through the install review (C-13), which records the reviewed revision; (c) "Approve this version" in the skill's More actions on the Skills tab |
| Revision changes | asks again (new arguments) | the old approval no longer matches; asks again |
| Unattended chain (task, plan, scheduled) | refused: "Not run: needs approval, and task and plan runs never ask." | refused with the same text; no card |
| `install_skill` by an agent, interactive | `install_skill`'s own tool policy applies first, as today | then, for an unverified skill, a trust card bound to the **staged** revision before publishing; the tool policy of `install_skill` (shipped Ask, `pkg/config/defaults.go`) is **not** treated as the trust check |
| `install_skill`, unattended | as today | refused, nothing installed |
| `POST /skills/install` without a review | — | refused for an unverified skill: 409 "Review needed: this skill is unverified, and unverified skills are set to Ask. Review it in Skills & Tools first." |
| Pending card when policy, grant or revision changes before the answer | the answer is re-checked against the current outcome before loading; a stale yes does not load | same, against the current revision |

Option B (if the founder answers ST-Q2 B): the right-hand column collapses into the left-hand one; the trust card becomes the ordinary card with Always Allow, and Auto/God apply. Switch point: the row "Interactive chat, Auto-approve on" and "God Mode" of this table, D1 rows 9–10, S-30, S-31, ST-10, FR-030 and DOC-004.

### C-13 Install review before publishing (round-1 fix, MAJ-002)

| Schema / endpoint | New / Changed | File | Notes |
|---|---|---|---|
| `POST /skills/install/review` | new | `contracts/openapi.yaml` | body `SkillInstallRequest` (catalog slug + version, or upload reference); stages the skill with the existing staging code in `pkg/gateway/rest_skills.go::installSkill` **without** `publishSkillInstall`; returns `SkillInstallReview` |
| `SkillInstallReview` | new | `contracts/components/schemas/SkillInstallReview.yaml` | `review_id`, `name`, `version`, `revision` (`ContentRevision` of the staged folder), `verification` (enum `matched`, `no_registry_hash`, `mismatch`, `unavailable`), `verified` (true only for `matched`), `suggested_category` (`SkillCategory` or null, from `metadata.category` when valid), `replaces_existing` (bool), `has_local_changes_to_replace` (bool), `expires_at` |
| `DELETE /skills/install/review/{review_id}` | new | `contracts/openapi.yaml` | discards the staged folder; 204; unknown or expired id → 404 |
| `SkillInstallRequest` | changed | as C-5 | adds `review_id` and `category`. With `review_id`, the server publishes **exactly** the staged folder of that review: if its current `ContentRevision` differs from the reviewed `revision` → 409 "The skill changed after review. Review it again."; expired → 410 "This review expired. Open the skill again to review it." |

- `mismatch` is refused at review time (existing rule: a hash mismatch always fails, `pkg/skills/clawhub_registry.go`): 422 "The downloaded skill does not match the catalog's checksum. Nothing was installed."
- `unavailable` (registry did not answer the hash lookup) → `verified: false`; trust applies.
- Trust changed between review and Install: re-checked at publish; Deny → 403 (C-3 text).
- A staged review lives 15 minutes and is removed on expiry, on DELETE, and after publish.
- **Cancel**: before Install is pressed, Cancel sends DELETE and closes; "Nothing was installed." is true. After Install is pressed, Cancel is disabled until the server answers. If the answer is lost, the review shows "Could not confirm the install. Checking…", re-reads `GET /skills`, and then shows either "Installed <name>." or "Not installed." — never a guess.
- Agents: `install_skill` uses the same stage-then-publish path internally; for an unverified skill under trust Ask the trust card (C-12) is bound to the staged `revision`.

### C-14 Full skills: when they are injected and what happens when they do not fit (round-1 fix, CRIT-001, MAJ-006) [pending ST-Q1]

Option A in place.

| Effective outcome for this agent and chain (C-11) | Interactive | Unattended |
|---|---|---|
| `allowed` | whole instructions in the context from the first turn | same |
| `ask_pending` (ordinary Ask, or trust Ask without a matching approval) | not injected; listed as Name and short description; a load raises the card; after approval the skill is loaded the normal way (E1) | not injected; listed as Name and short description; a load is refused |
| `deny` | nothing | nothing |

- Full never counts as consent and never bypasses E1's checks.
- **Budget.** The injected instructions are charged once to the existing request budget (`pkg/agent/context_budget.go::contextBudget`, `::pinnedCoreOverheadTokens`), as part of the pinned overhead. If pinned overhead including all Full skills plus the output reserve does not fit the agent's model, the turn is **not sent** and the chat shows: "The skills set to Full for <agent> need about <n> tokens, more than <model> can take with this chat. Set some of them to Name and short description in Skills & Tools, or choose a model with a larger context." with a link to the Skills tab filtered to Full. Nothing is trimmed silently. The check runs every turn, so a model switch or a skill that grew is caught on the next turn.
- **Page warning.** Each Full skill row shows its size ("about 3,200 tokens"); when the Full total for any agent exceeds half of that agent's model context, the Skills tab shows a warning callout naming the agents.

Option B (ST-Q1 B): Full for a skill means name, description and the skill's `metadata` summary, never the instructions; this whole section except "Budget" falls away. Switch point: C-1 row `full` (skill column), E8, this section, FR-038, S-42, ST-13, DOC-003, ADR-TODO-1.

### C-15 Changing a level or a permission while chats are running (round-1 fix, MAJ-005)

- **One visibility authority.** Manifest building (`pkg/agent/tool_manifest.go::buildCompressedToolDefs`, `::buildToolManifestNote`), search membership (`pkg/tools/registry.go::SnapshotSearchableTools`) and the skill menu read the stored levels through one function. The fixed lists in `pkg/tools/manifest.go` are deleted, not kept beside the levels. `SnapshotSearchableTools` includes every tool whose level is not `full` and that is not ToolSearch, so demoting an old Full built-in to Hidden makes it searchable.
- **Levels govern what is advertised at the start of each turn.** A level change takes effect from the next turn of every chat, new or running. Definitions already loaded in a running chat (`pkg/agent/prompt_cache_sessions.go::restoreLoadedToolRetention`, `::pruneLoadedTools`) stay loaded until that chat's existing retention drops them; a level change does not evict them.
- **Permissions are different.** An effective Deny removes the tool from the next turn's tool list even if loaded (`FilterToolsByPolicy` runs every turn; `pruneLoadedTools` receives only allowed tools), as today. For skills, a Deny removes the skill from the menu, from search and from the loadable folders (C-11) from the next turn.
- **Skill menu cache.** `freezeSessionSkillMenu` freezes the menu per session generation. The generation key gains a hash of the agent's skill outcomes and levels, so a permission, trust, grant or level change rebuilds the menu on the next turn (one prompt-cache miss for that chat).
- Visibility never grants the right to call anything.

### C-16 Saving, step by step (round-1 fix, MAJ-009)

| State | Shown on the row / page | How it ends |
|---|---|---|
| queued | the new value, with a small "Saving…" | the request starts within 1,000 ms of the click |
| in flight | same | one of the four below |
| saved and applied (200) | "Saved" | the page takes the stored maps from the response |
| saved, not applied (503) | "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." + Retry | Retry re-sends the same change |
| refused (4xx) | "Not saved: <server reason>." + Retry; the row shows the stored value from a fresh `GET` | — |
| outcome unknown (network error, timeout, no response) | "Couldn't confirm the save. Checking…", then the stored value from a fresh `GET` and "Showing what is stored now." | the page never assumes the old value came back |

- Each click (one badge, one level, one Set all) is its own `PATCH` naming only its own keys. Requests are sent in click order, one at a time, never merged. "Saved" refers to the change whose response arrived; a later response never marks an earlier change Saved.
- **Ownership.** The queue lives in an app-level store, not in the page, so in-app navigation does not stop it. A failure that arrives after the person left the page shows a toast: "A change on Skills & Tools was not saved: <reason>." with "Open Skills & Tools".
- **Closing the browser.** Requests use `fetch` with `keepalive`, so a request already started continues. A change clicked in the last moment before closing may not be sent; reopening the page shows what is stored. No leave prompt (T-Q3 B).
- `useAutoSave` (`src/hooks/useAutoSave.ts`) is not used for this page's permission and level writes because it coalesces queued changes to the latest snapshot; the agent Tools tab keeps it for its own fields, with the C-4 preservation rule.

### C-17 One search for tools and skills (round-1 fix, MAJ-015) [pending ST-Q6]

Option A in place. `ToolSearch` (`pkg/tools/tools_tool.go`) also searches the skills the calling agent may use (granted or project-authorized, outcome not `deny`, any level including Hidden), with the same keyword ranking. Each result line starts with its kind: `[tool]` or `[skill]`. A skill result gives its name and description and the sentence "Load it with the Skill tool." It does not load the skill and does not change grants or permissions. ToolSearch output is tool text, not a wire schema, so no contract file changes; the tool's description changes to say it finds skills too. `Skill` search stays.

Option B (ST-Q6 B): no change to ToolSearch; R31's "one search" is delivered in part and tracked as a follow-up issue named at landing. Switch point: this section, E9, FR-073, S-96, DOC-003.

### C-18 Migration of external tools that have no global value (round-1 fix, CRIT-004) [pending ST-Q5]

"Covered globally" is decided by the resolver itself: for service `S`, the probe name `mcp_<S>_\x00probe` is resolved against the **global** map with `pkg/tools/compositor.go::resolveFromMap` (exact, then most specific wildcard). A match means covered. This sees broader wildcards such as `mcp_*` and prefix overlaps (a `mcp_git_*` key covers service `git_hub`).

| State before upgrade (God Mode off; service assigned to the agent) | Agent before | Migration writes | Agent after | Change? |
|---|---|---|---|---|
| Global key covers `S` (exact server wildcard, or broader such as `mcp_*`) | per that key and any agent override | nothing | same | none |
| Some tools of `S` have exact global keys; the probe has no match | exact-key tools per their key; others: agent override, else deny | `mcp_<S>_*` = ask | exact-key tools unchanged (exact beats wildcard); the rest as the rows below | as below |
| No global match; no agent override for the tool | deny (no entry either side, logged Error) | `mcp_<S>_*` = ask | ask | **yes: deny → ask** (R30b, intended) |
| No global match; agent override `allow` | allow (sole layer) | `mcp_<S>_*` = ask | ask (strictest wins) | **yes: allow → ask** (named; the agent's work now asks once) |
| No global match; agent override `ask` | ask | same | ask | none |
| No global match; agent override `deny` | deny | same | deny | none |
| God Mode on, any of the above | global side floored to allow | same | per-agent value, else allow | none |

No agent entry is ever written (Hard Constraint #6: no per-agent backfill). Exactly the two "yes" rows change behaviour; both are listed in the upgrade note (DOC-004) and in SC-003's exceptions. The migration runs once (a `sandbox.migrations` marker), before the self-heal of C-2, and is idempotent: a second start writes nothing.

New service (after upgrade, screen or `add_mcp_server`): `mcp_<S>_*` = ask is written only when the probe has no global match; under a broader operator rule (for example `mcp_*: deny`) nothing is written and the rows show that rule's value.

Option B (ST-Q5 B): the value written for uncovered services at upgrade is `deny`; then the row "no agent override" is unchanged and the row "agent override allow" changes allow → deny. New services still start at Ask (R30b). Switch point: this table's "Migration writes" column, FR-045, S-59, S-88, D8.

### C-19 Project-folder skills have their own keys (round-1 fix, MAJ-003)

| Shelf (`pkg/skills/shelf.go::ResolveSkillName`) | Permission and level key | Created | Removed |
|---|---|---|---|
| built-in, registry, uploaded, written in Omnipus | the skill id (lower case) | install, copy, first sight at reconcile | when the skill is removed |
| project shelf of workspace `W` | `project:<W>/<slug>` | when Omnipus first reads `W`'s project shelf (turn start or reconcile) | when `W` is deleted, or at reconcile when the folder is gone |

- A project skill and an installed skill with the same slug have different keys; `ResolveSkillName`'s existing precedence decides which skill runs, and that skill's key applies.
- Project skills have no grant list; authority is the workspace mount, as today. Their starting permission is the same as installed skills [pending ST-Q3]; their starting level is Name and short description.
- Project skills are not listed on the Skills tab (#1380 not a rider), so their keys are edited only through `PATCH /security/permissions` and `set_agent_permissions` (C-10). They count as unverified (anyone with write access to the workspace can change them), so trust applies; this is a named behaviour change in DOC-004.
- **Upgrade order** (one-time, marked done in `sandbox.migrations`, idempotent): (1) trust value (C-3); (2) every installed skill's global value = old global `Skill` value; every project skill found at that moment the same; (3) each agent's old per-agent `Skill` value copied to each skill granted to that agent and to each project skill of each workspace the agent is mounted on; (4) the old `Skill` entries (global and per-agent) are removed and `Skill` becomes fixed `allow`; (5) levels (C-6, D7); (6) external tools (C-18); (7) self-heal. If Omnipus stops midway, the marker is not written and the whole migration re-runs from the stored old values, which are removed only in step 4 after steps 2–3 are written in the same atomic config write.
- Explicit per-agent tool values written earlier by role presets are ordinary overrides and are left unchanged.
- **Named change.** A skill granted to an agent after the upgrade follows the global value; it does not inherit the agent's old blanket `Skill` restriction. DOC-004 says so.

### C-20 Bulk changes, row by row (round-1 fix, MAJ-010)

| Row kind | Allowed values (global page) | Allowed values (agent tab) | In a Set all to a value it cannot take |
|---|---|---|---|
| ordinary tool or skill | Allow, Ask, Deny | values at least as strict as the global value | — |
| always-ask tool (`set_channel_outbound`, `pkg/tools/always_ask.go::AlwaysAskTools`) | Ask, Deny (Allow disabled: "Always asks: this tool never runs without your yes.") | Ask, Deny | skipped |
| Tool search, Skill (`permission_fixed`) | none | none | skipped |
| Omnipus agent, any row | — | Allow (if global allows), Deny; Ask disabled with the existing Omnipus text | skipped for Ask |
| locked level | — | — | skipped by level Set all |

- The Set all control counts only rows that will change: "Set 11 of 12 to Allow". Its accessible name carries the same numbers. After the save a note names what was skipped and why ("1 skipped: Send to channel always asks.").
- No row is ever silently given a different value from the one chosen. The server guards (`AlwaysAskAllowViolation`, Omnipus 422) stay; the page never sends a value they would refuse.
- God Mode on: Deny on an always-ask tool is allowed and saved, as today.

### C-21 Agent tool parity, by path (round-1 fix, MAJ-008; replaces the C-10 table)

| Field | Screen | REST create | REST update | Agent create | Agent update / read | Difference and reason |
|---|---|---|---|---|---|---|
| per-agent skill permission | agent Tools tab | `AgentCreateRequestMain.tools_cfg.skills.policies` (C-4) via `pkg/gateway/rest_agents_create.go::agentCreateToolsCfgFromWire` | `PUT /agents/{id}/tools` `skills`; `PUT /agents/{id}` `tools_cfg` | `create_agent` gains `skill_policy_changes` (`{set}` only), listed in `pkg/sysagent/tools/agent_apply_args.go::agentCreateArgNames` | `set_agent_permissions` `skill_policy_changes`; `get_agent` `skill_permissions` | none |
| preservation | — | — | `pkg/gateway/rest_agents_update.go::updateToolsConfig` keeps the stored `skills.policies` when the request's `tools_cfg` omits `skills`, as it already does for `builtin` and `mcp`; `{}` clears | — | `set_agent_permissions` with only tool changes leaves skill overrides untouched | — |
| skill grants | agent Skills tab | `skills` | unchanged | `create_agent.skills` | `update_agent.skills` | unchanged |
| skill category | install review; "Choose category" for an Uncategorised skill (once) | `POST /skills/install` `category`; `POST /skills/{name}/copy` gains `category` (default: the source's) | `PUT /skills/{name}/category` — only while the stored category is `uncategorised` (else 409 "The category is set at install and cannot be changed.") | `install_skill.category`; `edit_skill` `copy` gains `category` | `edit_skill` gains action `set_category`, same once-only rule | none. "Set once" keeps R32/F21 ("no later edit") while giving written and copied skills a reachable path |
| skill global permission, level; tool global permission, level; trust | Skills & Tools | `PATCH /security/permissions` | same | refused | `set_config` refuses `sandbox.*` (existing) | operator-only ceilings |
| retired manifest switches (`tools.manifest.compressed`, `tools.manifest.preview_all_lazy`, `tools.mcp.servers.<id>.deferred`) | — | — | — | — | `set_config` refuses them from this landing: "This setting was replaced by levels in Skills & Tools." [pending ST-Q4; under A only `compressed` is refused, with Settings' text] | prevents an agent from overriding every level |

The write-skill workflow (`pkg/skills/embedded/write-skill/SKILL.md`) gains: the card asks for a category; after writing, Omnipus calls `edit_skill` `set_category`. Category is never written into the skill file.

Parity tests: `TestSetAgentPermissions_SkillPolicyChangesParity`, `TestCreateAgent_SkillPolicyChangesParity`, `TestUpdateAgentToolsCfg_PreservesSkillPolicies` (REST update without `skills` keeps them), `TestInstallSkillCategoryParity`, `TestEditSkill_SetCategoryOnce`, `TestWriteSkillWorkflow_PublishesCategorisedSkill` (scripted model, W5 through to a listed skill with its category), and the `set_config` refusal rows for the `sandbox.*` keys and the retired manifest keys.

### C-22 First landing next to today's Settings (round-1 fix, MAJ-007)

This package lands before the Settings package. Nothing on today's Settings screen may break in between, and this package does not edit Settings files except where the table says so.

| Today's Settings element | What this landing does |
|---|---|
| Skill trust control (`src/components/settings/SkillTrustSection.tsx`, legacy enum) | `GET/PUT /security/skill-trust` **keep their legacy enum** for that screen, mapped to the stored value (`deny`↔`block_unverified`, `ask`↔`warn_unverified`, `allow`↔`allow_all`). The new page reads and writes trust through `PermissionSettings.skill_trust` (C-2). The legacy endpoint is retired by the Settings package. (This replaces the round-0 "legacy value → 400" rule.) |
| Global tool grid (`SecuritySection.tsx`) | `GET/PUT /security/tool-policies` unchanged for it, plus per-key audit (C-2) |
| "Load tools on demand" (`PerformanceSection.tsx`) | [pending ST-Q4] B: the key is retired by migration (C-6) and `set_config` refuses it; the switch is removed by the Settings package, which needs a matching change to its spec (it currently keeps the switch in Limits; team-lead to route). Until then it has no effect. A: kept; this landing adds the `set_config` refusal now (it was planned for the Settings package, `settings-redesign-spec.md` FR-100) |
| Links from this page to Settings | use today's `?tab=` addresses (`security`); the Settings package's old-link redirect (its US-1 AS3) carries them to the new sections. Link text names no section that does not exist yet: "Open Settings to change Auto-approve and God Mode.", "Shell safety rules (Settings)" |

Release rule: the release branch must not ship this package without the Settings package, because between them the old Settings trust control still says "Warn" for what is now Ask. Both are planned for v0.1.1 (Inferred from FR-065 and Settings FR-060).

### Collisions checked

- `skill_policies` keys are skill ids; they never enter `sandbox.tool_policies`, so the static-catalog validators (`ValidateSubmittedToolPolicyMap`, `config.go`/`validate.go:592`) and the `mcp_` carve-out are unchanged.
- No existing schema named `ModelVisibility`, `SkillCategory`, `AgentSkillEntry`, `AgentSkillPermission`, `PermissionSettings`, `PermissionSettingsPatch` or `SkillInstallReview` (`ls contracts/components/schemas`), and no existing path `/security/permissions`, `/skills/install/review` or `/skills/{name}/category` in `contracts/openapi.yaml` (round 1, `grep`).
- The Settings package changes no schema used here (its C-1 to C-7 are retention, restart, Performance and key deletion).

## API and Data

| Endpoint / event | Change |
|---|---|
| `GET/PATCH /security/permissions` | new (C-2): this page reads and writes every permission, level and trust value through it, one per-key change per request |
| `GET/PUT /security/tool-policies` | unchanged for today's Settings screen; PUT gains per-key audit (C-2) |
| `POST /skills/install/review`, `DELETE /skills/install/review/{id}` | new (C-13) |
| `PUT /skills/{name}/category` | new, once only (C-21) |
| `POST /skills/{name}/copy` | gains `category` (C-21) |
| `GET/PUT /security/skill-trust` | legacy enum kept for today's Settings screen (C-22) |
| `GET /tools` | C-6 fields |
| `GET /skills` | C-5 `category`, redefined `verified` |
| `POST /skills/install` | C-5 `category`, C-13 `review_id`; trust Deny refuses an unverified install (C-3); trust Ask needs a review (C-12) |
| `GET/PUT /agents/{id}/tools` | C-4 `skills` |
| `POST /mcp-servers` | writes the service's Ask key (C-7) |
| WS `tool_approval_required` | C-9 `skill` |
| WS `message` with `workflow_start` | C-8 `W5` |

Stored data, behaviourally: one global permission and one level per installed skill and per project skill (C-19); one level per tool and per external server; per-agent skill overrides inside the agent's tool settings; each skill's category, install-time verification result and trust approvals (skill, revision) in Omnipus's own data; a `sandbox.migrations` marker.

---
## User Stories & Acceptance Criteria

### User Story 1 — Set the global permission of every Omnipus and external tool on one page (Priority: P0)

A person who runs Omnipus wants to decide, in one place, which tools every agent may run, may run after asking, or may never run. Today that grid sits behind "Advanced" in Settings → Security, and the Skills & Tools page only links to it.

**Why this priority**: Settings removes this grid (Settings FR-060); without this story the global ceiling has no screen at all.

**Independent Test**: open Skills & Tools → Omnipus tools, set `send_email` to Deny, reload, see Deny; an agent's call of `send_email` is refused.

**Acceptance Scenarios**:

1. **Given** the Omnipus tools tab, **When** the person clicks Deny on a tool row, **Then** the row shows Deny, a "Saved" signal appears, and after a reload the row still shows Deny.
2. **Given** a group of 12 tools on screen, all able to take Ask, **When** the person uses that group's Set all → Ask, **Then** all 12 rows show Ask and are saved in one request, with no confirm. Rows that cannot take the chosen value are skipped and named (C-20).
3. **Given** the External tools tab and a service with 5 tools, **When** the person uses the service's Set all → Allow, **Then** all 5 tools show Allow and are saved.
4. **Given** any tool row, **When** the page shows it, **Then** exactly one of Allow, Ask, Deny is selected and "Unset" never appears.
5. **Given** a single click or a Set all that would make `execute_plan` resolve to Allow, **When** the person makes it, **Then** the existing "Confirm autonomous plan execution" dialog appears first, and Cancel changes nothing [pending ST-Q7; option B removes this scenario].
6. **Given** the Tool search row, **When** shown, **Then** it has no Allow / Ask / Deny and reads "Always available for discovery. The tools it finds still follow their own permissions."; every Set all leaves it out.
7. **Given** the save is refused, **When** the server answers with an error, **Then** the page shows the server's reason with Retry, and the rows show the values that are actually saved.
8. **Given** the save is stored but running agents could not be updated, **When** the server says so, **Then** the page shows "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." with Retry.
9. **Given** two browser tabs on this page, **When** tab A denies `send_email` and tab B then changes skill `plan` to Ask, **Then** both changes are stored (C-2).
10. **Given** a save whose response never arrives, **When** the page cannot confirm it, **Then** it shows "Couldn't confirm the save. Checking…" and then the stored value (C-16).

### User Story 2 — Give every skill its own Allow / Ask / Deny (Priority: P0)

A person wants one skill to run freely, another to ask first, and a third never to run, instead of one switch for all skills (T-Q2 A). It must look and work exactly like a tool row (T-Q1 note).

**Why this priority**: founder decision T-Q2 A; the Skills tab's main column.

**Independent Test**: set skill `deep-research` to Ask; an agent granted it loads it in a chat; an approval card appears naming the skill.

**Acceptance Scenarios**:

1. **Given** the Skills tab, **When** the person clicks Deny on a skill row, **Then** it is saved, and an agent granted that skill can no longer load it and no longer sees it in its skill list.
2. **Given** a verified skill set to Ask and Auto-approve off, **When** an agent in a chat tries to load it, **Then** the normal approval card appears naming the skill, with Approve Once, Deny and Always Allow; an Always Allow is remembered for that skill's current revision only (C-12).
3. **Given** a skill set to Ask, **When** a task, plan or scheduled run tries to load it, **Then** the load is refused with "Not run: needs approval, and task and plan runs never ask." and no card appears.
4. **Given** a skill set to Allow and granted to the agent, **When** the agent loads it, **Then** it loads with no card.
5. **Given** a skill not granted to the agent, **When** the agent tries to load it, **Then** it is refused as today, whatever its permission (the grant stays the gate, ADR-072 D4).
6. **Given** the Omnipus tools tab, **When** the `Skill` row is shown, **Then** it has no Allow / Ask / Deny and reads "Permissions are set per skill on the Skills tab." with a link to the Skills tab.
7. **Given** an install upgraded from a version without per-skill permissions, **When** it first starts, **Then** every skill's global permission equals the old global `Skill` value, and each agent's old per-agent `Skill` value is copied to each skill granted to that agent at that moment, so no agent's behaviour changes.
8. **Given** a new skill is installed, **When** it appears, **Then** its global permission is Allow [pending ST-Q3] and its level is "Name and short description".
9. **Given** a granted skill whose effective value for an agent is Deny (or Ask not yet approved), **When** that agent reads the skill's folder with a file tool or runs its helper script, **Then** the read or run is refused as for an ungranted skill (C-11 E6, E7).

### User Story 3 — An agent's Tools tab is the same editor, stricter only, without presets (Priority: P0)

A person opens Mia → Tools and wants the same rows and badges as on Skills & Tools, for tools and for Mia's skills, where Mia can only be stricter than the global value (T-Q7), with no Cautious / Balanced / Full access buttons (T-Q6 A).

**Why this priority**: founder notes on T-Q1, T-Q6 and T-Q7.

**Independent Test**: global `bash` = Ask; Mia's Tools tab shows Allow disabled for `bash`; Mia's skill `plan` set to Deny refuses her load.

**Acceptance Scenarios**:

1. **Given** any agent's Tools tab, **When** it opens, **Then** no role preset control is shown.
2. **Given** a tool or skill whose global value is Ask, **When** the agent's row is shown, **Then** Allow is disabled with the visible reason "Global setting is Ask; an agent can only be stricter.", and Ask and Deny are available.
3. **Given** the agent's Tools tab, **When** it opens, **Then** it has a Skills group listing the skills granted to that agent, each with Allow / Ask / Deny.
4. **Given** a skill override on the agent, **When** the person uses Inherit on that row, **Then** the override is removed and the row shows the global value.
5. **Given** Omnipus's Tools tab, **When** the person clicks Ask on a skill row, **Then** the save is refused with the existing Omnipus text ("Omnipus takes no per-agent Ask; set Ask globally or use Deny. …") and the row shows the saved value.
6. **Given** Judge or Plan Supervisor, **When** their Tools tab opens, **Then** it is read-only, as today.
7. **Given** an agent with no granted skills, **When** the Skills group is shown, **Then** it reads "No skills granted. Grant skills on this agent's Skills tab."
8. **Given** a per-agent value is changed, **When** the click lands, **Then** it saves on its own, as today.

### User Story 4 — Skill trust uses Allow / Ask / Deny and decides installs and loads (Priority: P1)

A person decides what happens to unverified skills — skills Omnipus cannot confirm against a reviewed version — with the same three words and badges as every row (T-Q1 A).

**Why this priority**: moves from Settings (S-Q8); a supply-chain protection.

**Independent Test**: trust = Deny; uploading an unverified skill is refused in the install review; an installed unverified skill granted to an agent cannot be loaded.

**Acceptance Scenarios**:

1. **Given** the Skills tab, **When** it opens, **Then** "Unverified skills" is the first block, with Allow / Ask / Deny badges and one line per choice always visible.
2. **Given** trust = Deny, **When** the person reviews an unverified install, **Then** "Install skill" is disabled with "Unverified skills are set to Deny above. Choose Ask or Allow to install this skill."
3. **Given** trust = Deny, **When** `install_skill` or the install endpoint installs an unverified skill, **Then** it is refused with "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed."
4. **Given** trust = Ask and an unverified skill whose own permission is Allow and whose current revision has no trust approval, **When** an agent in a chat loads it, **Then** a trust card appears naming the skill, saying it is unverified, with "Approve this version" and Deny and no Always Allow, also with Auto-approve on [pending ST-Q2].
5. **Given** trust = Allow, **When** chosen, **Then** a warning callout reads "Allow turns off the check for unverified skills. Choose Ask to turn it back on."
6. **Given** an upgrade from a stored Block unverified / Warn unverified / Allow all, **When** Omnipus starts, **Then** trust shows Deny / Ask / Allow respectively, and today's Settings trust control keeps working (C-22).
7. **Given** a registry skill whose hash matched at install, **When** its folder is later changed, **Then** it shows as unverified and trust applies to it.
8. **Given** a built-in skill, **When** trust is Deny, **Then** it still loads (built-ins are verified).

### User Story 5 — Choose what the model sees about each tool and skill (Priority: P1)

A person wants rarely used tools hidden from the model to save cost, and important ones fully visible, with the same four levels for tools and skills (T-Q4b B), set once for all agents (T-Q7).

**Why this priority**: founder T-Q4b; R31.

**Independent Test**: set `serve_web` to Name only; a new turn lists `serve_web` by name without its description; set a skill to Hidden; it leaves the skill menu but `Skill` search still finds it.

**Acceptance Scenarios**:

1. **Given** any tool or skill row, **When** shown, **Then** a "What the model sees" control offers Hidden, Name only, Name and short description, Full, each with a one-line explanation for a tool or a skill.
2. **Given** a tool set to Full, **When** an allowed agent starts a turn, **Then** the tool is callable without a search.
3. **Given** a tool set to Name only, **When** an allowed agent starts a turn, **Then** the tool's name is listed without a description and it is callable after a ToolSearch load.
4. **Given** a tool set to Hidden, **When** an agent searches for it with ToolSearch, **Then** it is found and loadable if allowed.
5. **Given** a locked tool (Tool search, Skill, Ask the user, goal_claim, set_goal), **When** shown, **Then** its level is fixed with the visible reason "Always loaded: agents need this to work, so its level cannot be changed."
6. **Given** an item an agent may not run (effective Deny), **When** that agent's turn starts, **Then** the item is not listed and not found, whatever its level.
7. **Given** an upgrade, **When** Omnipus first starts, **Then** every tool's level is the D7 value for that install's old settings, so the first-turn request of every agent is the same as before apart from the named changes (SC-003), and every skill is Name and short description.
8. **Given** a skill set to Full, granted, and Allow for that agent, **When** a new chat of that agent starts, **Then** the skill's whole instructions are in its context from the first turn; if the skill is Ask (not approved) they are not, and it is listed as Name and short description (C-14) [pending ST-Q1].
9. **Given** an install that had "Load tools on demand" off before the upgrade, **When** Omnipus first starts, **Then** every static tool's level is Full and the tool tabs show no note, because the levels are the only control [pending ST-Q4; option A keeps the switch and shows the note "Load tools on demand is off in Settings, so every allowed tool is sent in full. These levels apply when it is on."].
10. **Given** a running chat that already loaded `browser_evaluate`, **When** the person sets it to Hidden, **Then** the chat keeps the loaded definition, and new chats do not list it (C-15).
11. **Given** Full skills for an agent that do not fit its model, **When** a turn starts, **Then** nothing is sent and the chat shows the C-14 text with a link to the Skills tab [pending ST-Q1].

### User Story 6 — Find any tool or skill and change a group at once (Priority: P1)

A person with 53 skills and 200 tools needs search, filters and group actions.

**Why this priority**: R33, R35, R39; the page is unusable at this size without them.

**Independent Test**: type "browser" on Omnipus tools; only browser rows show; that group's Set all says "Set 17 matching" when 17 rows match.

**Acceptance Scenarios**:

1. **Given** the page, **When** it opens, **Then** it shows three tabs with counts — Skills, External tools (MCP), Omnipus tools — and Skills is selected.
2. **Given** a tab, **When** shown, **Then** one search field and two flat filters (Permission, What the model sees) sit on one line above one grouped list whose groups start collapsed.
3. **Given** a search, **When** text is typed, **Then** groups with matches open and only matching rows show; Escape in the field or the clear button empties it.
4. **Given** a search or filter is on, **When** the person uses a group's Set all, **Then** exactly the rows on screen that can take the value change, and the control's label and accessible name carry the same counts ("Set 3 of 3 matching", C-20).
5. **Given** no row matches, **When** the list is empty, **Then** "No tools match “zzzz”" shows with "Clear search and filters".
6. **Given** a filter is on and a row's value is changed so it no longer matches, **When** the save completes, **Then** the row leaves the list, focus moves as "Accessibility and Keyboard" says, and the status region announces the change and the new count.
7. **Given** a "What the model sees" filter is on, **When** the list shows, **Then** a note says "Denied items are not loaded, so this filter leaves them out. Use the Permission filter to find them."

### User Story 7 — Add a skill: write it with Omnipus, upload it or install it from the catalog, with a category (Priority: P1)

**Why this priority**: T-Q5, R32; the Skills tab's main action.

**Independent Test**: New skill → Write with Omnipus opens the Omnipus chat with the message "Write a new skill"; Omnipus loads its write-skill workflow.

**Acceptance Scenarios**:

1. **Given** the Skills tab, **When** the person opens New skill, **Then** it offers "Write with Omnipus", "Upload a skill" and "Browse the catalog", and no item names a retired agent.
2. **Given** "Write with Omnipus", **When** chosen, **Then** the Omnipus chat opens and receives "Write a new skill" with the write-skill workflow start.
3. **Given** the connection is down, **When** "Write with Omnipus" is chosen, **Then** nothing is sent and a warning reads "Not connected — “Write a new skill” was not sent. Press the button again once you are reconnected."
4. **Given** a catalog result or an uploaded file, **When** the person chooses Install, **Then** the server stages it without installing (C-13) and the review shows name, version, whether it is verified (from the staged files), the current trust choice, and a required Category.
5. **Given** no category is picked, **When** Install is pressed, **Then** the field shows "Choose a category." and nothing is installed.
6. **Given** a skill file with `metadata.category: research`, **When** the review opens, **Then** Research is preselected.
7. **Given** skills are installed, **When** the Skills tab lists them, **Then** they are grouped by category; skills without one are under Uncategorised; empty categories are not shown.
8. **Given** a skill row, **When** the person opens More actions, **Then** it offers Make a copy, plus Remove for a skill that is not built-in, plus "Choose category" while the skill is Uncategorised, plus "Approve this version" for an unverified skill under trust Ask.
9. **Given** Write with Omnipus completes, **When** the person approved the write-skill card with a category, **Then** the new skill is listed under that category (C-21).
10. **Given** the person presses Cancel in an install review before Install, **When** it closes, **Then** the staged copy is discarded and nothing is installed; after Install is pressed Cancel is disabled until the answer (C-13).

### User Story 8 — Manage external services on the same tab (Priority: P2)

**Why this priority**: today's MCP tab, regrouped (R30, R35).

**Independent Test**: add a service; all its tools show Ask; switch it off; Test; Remove.

**Acceptance Scenarios**:

1. **Given** "Add service" and no global rule covering it, **When** a service is added, **Then** all its tools show Ask, and tools it adds later also show Ask; under a broader global rule (for example `mcp_*` Deny) they show that rule's value (C-18).
2. **Given** a service group header, **When** shown, **Then** it carries the on/off switch, Test, Edit and Remove, and the switch acts at once.
3. **Given** a service that cannot connect, **When** its group is opened, **Then** an error notice shows the server's reason with Retry and Edit.
4. **Given** Remove, **When** pressed, **Then** "Remove this service? Agents that use it lose its tools." asks first.
5. **Given** the local-program choice in Add service, **When** chosen, **Then** "This runs a program on your server" opens with "Use a network address instead" as the main, focused button.

### User Story 9 — Tool-specific settings and links (Priority: P2)

**Why this priority**: Settings FR-060 needs web search here; R30a.

**Independent Test**: Omnipus tools → Web & Search → Settings opens the search-provider panel; changing the default provider works as it did in Settings.

**Acceptance Scenarios**:

1. **Given** the Web & Search group, **When** the person presses its Settings button, **Then** a side panel opens with today's Default search and Fallback cards and their behaviour, unchanged.
2. **Given** the Shell group, **When** shown, **Then** it links "Shell safety rules (Settings)" to today's Settings address that holds them; the Settings package's old-link redirect carries it to its new section (C-22).
3. **Given** the Omnipus tools intro, **When** shown, **Then** it reads "Open Settings to change Auto-approve and God Mode." with a link to today's Settings address for them (C-22).
4. **Given** the page, **When** any tab is shown, **Then** no link sends the person to Settings → Security for permissions.

### User Story 10 — Every tab loads, fails and reads the same way; phone and keyboard (Priority: P2)

**Independent Test**: stop the server, open each tab: error with Retry; at 390 px no sideways scroll, 44 px targets.

**Acceptance Scenarios**:

1. **Given** a tab is loading, **When** shown, **Then** a spinner "Loading skills…" (or tools, services) and skeleton rows show; tab counts show "…"; search, filters and badges are disabled.
2. **Given** a tab fails to load, **When** shown, **Then** an error callout gives the observed cause and Retry; no control on that tab can write until it loads.
3. **Given** a width of 390 px, **When** any tab is shown, **Then** each row becomes a card with "Permission" and "What the model sees" lines, every control is at least 44 × 44 px, and nothing scrolls sideways.
4. **Given** keyboard use only, **When** the person moves through the page, **Then** tabs, badges, menus and dialogs work with the keys in "Accessibility and Keyboard".

### User Story 11 — Agents can do what the person can (Priority: P1)

Omnipus, asked in its chat, can set one agent's per-skill permission and install a skill with a category; it cannot change the global ceiling, levels or trust (C-10).

**Why this priority**: agent-tool parity rule (root `CLAUDE.md`).

**Independent Test**: `set_agent_permissions` with `skill_policy_changes: {set: {plan: deny}}` on Mia; `get_agent` `skill_permissions` shows `plan` effective deny, override deny.

**Acceptance Scenarios**:

1. **Given** `set_agent_permissions` with a skill change, **When** approved, **Then** the agent's override is stored and reported like a tool change, including the clamp sentence when the global value is stricter; `create_agent` accepts the same `skill_policy_changes` (C-21).
2. **Given** `install_skill` with `category: research`, **When** it installs, **Then** the skill shows under Research.
3. **Given** `set_config` with key `sandbox.skill_policies.plan`, **When** called, **Then** it is refused with the existing sandbox reason.
4. **Given** `get_agent` with `fields: ["skill_permissions"]`, **When** called, **Then** each granted skill is listed with its effective value and override.
5. **Given** an agent with skill overrides, **When** `PUT /agents/{id}` changes only its MCP servers, **Then** the skill overrides are unchanged (C-21).

### User Story 12 — Rider fixes on the same files (Priority: P2)

See "Riders"; each rider has its own BDD scenario under "Feature: Riders".

### Edge Cases

- A skill removed while its row is on screen: the next save of that row is refused (unknown skill, 400); the page reloads the list and says "This skill was removed."
- Two browser tabs: different keys both survive (each request names only its own keys, C-2); the same key — the later acknowledged write wins, and each tab shows the stored maps from its latest response and re-reads on focus.
- A Set all clicked twice quickly: one request per click, sent in order, never merged (C-16); the page ends on the stored state of the last response.
- A skill granted to an agent but denied globally: the agent's row shows Deny, Allow and Ask disabled with "Global setting is Deny; an agent can only be stricter."
- A stored per-agent Allow below a later global Ask: stored as is, floored at run time; the row shows the true effective value (as for tools, `ToolPolicyEditor.tsx::trueEffectivePolicy`).
- An unverified skill with God Mode on: the trust check still applies (C-12) [pending ST-Q2].
- A project-folder skill (mount shelf, ADR-072 D4.1): no grant list; its own key `project:<workspace>/<slug>` (C-19); it follows trust (unverified), its global permission and its level; it is not listed on the Skills tab (#1380 is not a rider by default), so it is changed through `PATCH /security/permissions` or an agent tool, not on screen (Assumptions).
- A skill and a tool with the same name: separate maps; no collision.
- An external service whose id contains underscores: its Ask key uses the id the registry reports (`ToolRegistryEntry.server_id`), never a prefix parsed from tool names.
- `delegate` with `requested_skill` naming an Ask skill: the child's load raises the card in the root chat (#1263 routing); in an unattended chain it is refused.
- The person's own `/skill` command on an ordinary Ask skill: it loads without a card (the person asked by name); Deny and trust Deny still refuse it; an unverified skill under trust Ask still shows the trust card unless that revision was approved (C-11 E4) [pending ST-Q2].
- An agent allowed `edit_skill` can read a denied skill's files for editing (C-11 editor exception); it still cannot load it or run its helpers.
- A skill whose files change while a trust or approval card is open: the answer is checked against the current revision; a yes for the old revision does not load the new one (C-12).

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Skills tab | spinner "Loading skills…", skeleton rows, trust block skeleton | trust block stays; "No skills installed" + "Browse the catalog" (outline) | callout with cause + Retry; no writes | list loads, permissions fail: rows read-only, "Permissions could not be loaded." + Retry | trust block, grouped rows, "Saved" after each change |
| External tools tab | spinner "Loading services…" | "No external services yet" + "Add service" | callout + Retry | a service not connected: "Not connected" in its header and the error notice inside; others normal | one group per service |
| Omnipus tools tab | spinner "Loading tools…" | "No Omnipus tools reported" + Retry | callout + Retry | levels fail, permissions load: level shows "Could not load" + Retry; badges work | one group per category |
| Agent Tools tab | as today | Skills group: "No skills granted. Grant skills on this agent's Skills tab." | as today (three Retry states) | global ceiling missing: read-only, today's text | rows, "Saved" |
| Install review | "Checking the skill…" while staging (Cancel usable, discards); "Installing…" after Install (Cancel disabled) | — | server reason; trust Deny refusal; "This review expired. Open the skill again to review it."; "The skill changed after review. Review it again."; outcome unknown → "Could not confirm the install. Checking…" then the real result | verification unavailable: "Not verified: the catalog did not answer." and trust applies | toast "Installed <name>. No agent is granted it yet; grant it on an agent's Skills tab." |
| Web & Search panel | today's states | today's | today's | today's | today's |
| Page save signal | "Saving…" (queued or in flight, C-16) | — | "Not saved: <server reason>." + Retry; rows show stored values; outcome unknown → "Couldn't confirm the save. Checking…" then "Showing what is stored now." | "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." + Retry | "Saved" |
| Full skills too large | — | — | in the chat: the C-14 text; on the Skills tab: a warning callout naming the agents whose Full total exceeds half of their model's context [pending ST-Q1] | — | each Full row shows its size |
| After leaving the page | — | — | a late failure shows a toast "A change on Skills & Tools was not saved: <reason>." with "Open Skills & Tools" | — | — |

## User Journey

1. Sidebar → ASSETS → Skills & Tools; the Skills tab opens (US-6 AS1).
2. The trust block is first (US-4 AS1); a change saves at once.
3. Search or filter, open a category, change a skill's permission or level (US-2 AS1, US-5 AS1); "Saved" appears.
4. New skill → Write with Omnipus / Upload / Browse (US-7); the install review asks for a category; the skill appears with Allow and Name and short description (US-2 AS8).
5. External tools: Add service; its tools show Ask (US-8 AS1); the service's Set all (US-1 AS3).
6. Omnipus tools: a group's Set all (US-1 AS2); Web & Search → Settings panel (US-9 AS1).
7. An agent → Tools tab: the same rows, stricter only, with a Skills group (US-3).
Exit: through the sidebar; changes already clicked keep saving after the page is left, and a late failure appears as a toast (C-16). A change clicked in the moment the browser closes may not be sent; reopening shows what is stored (T-Q3 B, no leave prompt).

## Accessibility and Keyboard

- Tabs: the catalogued `tabs` component (Radix tabs: `tablist`, `tab`, `tabpanel`); arrow keys, Home, End.
- A row that leaves a filtered list after its save: focus moves to the same control in the next row of the list; if there is none, the previous row; if the group became empty, its header; if the list became empty, the "Clear search and filters" button. The polite status region announces "<name> set to <value>. It no longer matches the filter. <n> shown." A failed save leaves the row and focus where they were. A Set all that empties the group moves focus to the group header. Background refreshes never move focus.
- Allow / Ask / Deny: toggle buttons with `aria-pressed` in a group named "Permission for <name>"; a locked badge is disabled and its reason is visible text referenced by `aria-describedby`.
- Trust block: a radio group of three badges, one tab stop, arrow keys; all three meanings always visible, each linked by `aria-describedby`.
- "What the model sees": a menu button named "What the model sees for <name>: <level>"; arrow keys, Enter; Escape closes and returns focus.
- Group headers: `aria-expanded`, `aria-controls`. Set all: named "Set <k> of <n> matching to <value>: <group>", the same numbers as its label (C-20).
- Save signal: polite status region; errors use `role="alert"`.
- Focus: the design system's one focus ring (focus-visible ownership, `omnipus-design-system`).
- Touch: 44 × 44 px minimum at 390 px; no sideways scroll; every badge carries an icon and a word.

## Design-System Components

Checked against `design-system/catalog.json` first; no new catalogued component is needed.

| Element | Component |
|---|---|
| Page header and the one gold main action per tab | `page-header`, `button` (default variant), `dropdown-menu` (New skill) |
| Tabs | `tabs` (`src/components/ui/tabs.tsx`, catalogued in `design-system/catalog.json`; tablist semantics) — not `view-switch`, which is a radio group (round-1 MIN-001) |
| Search and filters | `search-field`, `filter-menu` |
| Grouped list | `list-group` (flat accordion, R35) |
| Permission badges | `PolicyBadge` (product shared component, reused) |
| Level control | `dropdown-menu` |
| States | `collection-state`, `query-error-state`, `skeleton`, `spinner`, `empty`, `callout` |
| Save signal | `AutoSaveIndicator` |
| Dialogs and panels | `dialog`, `confirm-dialog`, `sheet`, `select` (category), `switch`, `segmented-control`, `tooltip` |

The wireframe's Save bar is not built (T-Q3 B), so nothing new is published.

## Security and User Promises

- security-lead reviews before GREEN: the C-11 entry-point inventory and the skill-folder authority change (`pkg/fspolicy`), the C-12 consent rules, the C-18/C-19 migrations, the trust floor and its enforcement at every install and load path (supply chain), the skill policy resolution beside `pkg/tools/compositor.go`, the audit entries (`pkg/audit`), and the `set_config` refusal rows.
- Changed user promises (`docs/skills.md`, `docs/tools.md`, `docs/security.md`): trust words and meanings, per-skill permissions, `Skill` no longer a master switch, presets gone, levels, new services at Ask, the global grid's new home.
- The trust default moves from "warn and continue" to Ask: an unverified skill that loaded silently now asks in a chat and is refused in unattended runs (T-Q1 A). The docs and the upgrade note say so plainly (DOC-004).
- Hard Constraint #6 stays intact: two stored layers for skills as for tools, no default fallback, no backfill; trust and God Mode are resolution-time rules, like always-ask (C-0).
- No on-screen text names a retired agent, an issue number, a requirement tag or "contact support".

---

## Behavioral Contract

Primary flows:
- When a person changes a permission, a level or the trust choice, the system saves it with no further action and shows "Saved".
- When a person uses a group's Set all, the system changes exactly the rows shown in that group that can take the value, in one request, with no confirm, and names the skipped rows — except the existing autonomous-plan confirm when `execute_plan` would resolve to Allow [pending ST-Q7].
- When an agent loads a granted skill, the system applies the strictest of the skill's global permission, the agent's own value, and — for an unverified skill — the trust choice, and every other path to that skill's instructions or files (menu, search, `/name`, delegation, file tools, helpers, Full) follows the same outcome (C-11).
- When an agent's turn starts, the system lists each allowed tool and each granted, not-denied skill at its global level; a Full skill's instructions are included only when its outcome is `allowed` (C-14).
- When a person opens an agent's Tools tab, the system offers only values at least as strict as the global value.
- When a service is added and no global rule covers it, the system sets all its tools, now and later, to Ask (C-18).

Error flows:
- When a save is refused, the system shows the server's reason with Retry and the stored values.
- When a save is stored but not applied to running agents, the system says so and offers Retry.
- When an unverified install meets trust Deny, the system refuses it and installs nothing.
- When a skill load meets an effective Deny, the system refuses it, naming the skill and which setting refused it.
- When an Ask load happens in an unattended run, the system refuses it with the existing unattended text.

Boundary conditions:
- When a locked tool's level is sent with another value, the system refuses it.
- When a group contains Tool search or Skill, every Set all skips them.
- The levels are the only control of what is sent about tools [pending ST-Q4].
- When Full skills do not fit the agent's model, the turn is not sent and the chat says why (C-14).
- When a save's outcome is unknown, the system re-reads and shows what is stored, never a guess (C-16).
- When a filter is on, Set all counts and changes only matching rows.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not show a Save bar, Discard, a leave prompt or a Set all confirm on Skills & Tools, because T-Q3 B chose autosave with no confirm. The single exception is the existing autonomous-plan warning (FR-010) [pending ST-Q7].
- The system must not show Cautious / Balanced / Full access anywhere, because T-Q6 A removes them.
- The system must not offer a per-agent "What the model sees" control, because T-Q7 rules it out.
- The system must not let an agent tool change a global permission, a level or trust, because global ceilings are operator-only (C-10).
- The system must not add a third stored policy layer, a default policy or a fail-closed backfill (Hard Constraint #6).
- The system must not write a category, permission or level into a skill's folder, because that breaks the hash check (R32).
- The system must not show "Unset" for any tool or skill (R30b).
- The system must not name Ava, Admin or any retired agent in menus, toasts or built-in skill text.
- The system must not change the grant model. ToolSearch finds skills but never loads them (C-17) [pending ST-Q6].
- The system must not inject a skill's instructions, or open its folder to file tools or helpers, before its outcome is `allowed` (C-11).
- The system must not write any per-agent policy entry during migration (Hard Constraint #6; C-18, C-19).
- The system must not change Settings screens (page G), Tasks and Board, or signed-in browsers (#1951), apart from what C-22 lists; it must not break today's Settings trust or tool-grid controls at this landing.

### Machine-Verifiable Constraints

**Error Codes / Messages** (all bodies `ErrorResponse`):
- `PATCH /security/permissions`, unknown skill key in `skill_policies` or `skill_visibility` → 400, nothing saved.
- `PATCH /security/permissions`, a locked tool's level changed → 400, "`<tool>` is always loaded: agents need it to work, so its level cannot be changed."
- `PATCH /security/permissions`, `ToolSearch` or `Skill` in `policies` with a value other than `allow` → 400, "`<tool>` has no permission of its own."
- `PATCH /security/permissions`, `set_channel_outbound: allow` → 400 (existing always-ask text).
- `PATCH /security/permissions`, no key changed → 400 "Nothing to change."; duplicate skill → 400 "`<skill>` is listed twice."; `null` → 400 naming the key.
- `PATCH /security/permissions`, `skill_trust` other than `allow`/`ask`/`deny` → 400, "Use allow, ask or deny."
- `POST /skills/install`, unverified skill, trust `ask`, no `review_id` → 409 "Review needed: this skill is unverified, and unverified skills are set to Ask. Review it in Skills & Tools first."
- `POST /skills/install`, reviewed revision differs → 409 "The skill changed after review. Review it again."; expired review → 410.
- `POST /skills/install/review`, checksum mismatch → 422 "The downloaded skill does not match the catalog's checksum. Nothing was installed."
- `PUT /skills/{name}/category` when a category is already set → 409 "The category is set at install and cannot be changed."
- `POST /skills/install`, unverified skill with trust `deny` → 403, "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed."
- `POST /skills/install`, `category` outside `SkillCategory` → 400.
- `PUT /agents/omnipus/tools`, `skills.policies` containing `ask` → 422, the existing Omnipus refusal text and code.
- `PUT /agents/{id}/tools`, `skills.policies` key that is not installed → 400.

**Performance Bounds**: a single click MUST start its save request within 1,000 ms (start, not completion); a Set all MUST issue exactly 1 save request; requests from separate clicks are never merged.

**Scope Boundaries**: levels MUST change only what is listed to the model, never whether a tool or skill may run.

## Integration Boundaries

### Gateway REST API (SPA ↔ gateway)
- **Data in / out**: the C-2 to C-6 and C-8 payloads.
- **Contract**: the schemas in Contract Changes.
- **On failure**: 4xx → reason + Retry; 503 → saved-not-applied text.
- **Development**: real gateway in handler tests.

### Agent loop (policy resolution, manifest, skill menu)
- **Data in / out**: global and per-agent maps, trust, levels → tool definitions, manifest lines, skill menu, approval frames.
- **Contract**: `ToolApprovalRequiredFrame` (C-9); internal Go otherwise.
- **On failure**: no entry on either side resolves deny with an Error log (existing rule).
- **Development**: pure resolver unit tests; a scripted model (no paid call) for UAT.

### Skill registry (ClawHub) install
- **Data in / out**: archive + hash → installed folder, stored verification result.
- **On failure**: hash mismatch always fails; registry down → 502 (both unchanged).
- **Development**: the existing registry twin.

### Omnipus chat (write-skill workflow)
- **Data in / out**: `workflow_start {workflow: W5}` → Omnipus loads `write-skill`.
- **Contract**: `WorkflowStart` (C-8).
- **On failure**: not connected → warning, nothing sent (existing pattern).

### Settings package (page G)
- **Order**: this package lands before Settings is cut (Settings FR-060). Readiness: the Skills & Tools route renders the tool permission grid, web search settings and skill trust.
- **Compatibility at this landing**: C-22. Today's Settings trust control and tool grid keep working against unchanged endpoints; links use today's Settings addresses. Changes the Settings package needs: retire `/security/skill-trust`'s legacy enum and, under ST-Q4 B, drop "Load tools on demand" from Limits (team-lead routes this to the Settings spec owner).

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Whether a Hidden, allowed tool or skill can be found | Not at all (re-ask text "not told at all") | Decided (founder default, former FQ-7): not listed, but ToolSearch / `Skill` search find it (R31) |
| 2 | Two tabs editing the global map at once | Add a revision field | Resolved in round 1: per-key PATCH (C-2); different keys both survive; same key, later acknowledged write wins |
| 3 | The person's `/skill` command on an Ask skill | It raises a card | Spec: no card (the person asked by name); Deny still refuses |
| 4 | Which skills the agent's Skills group lists | All installed | Spec: granted skills only (the grant is the gate, ADR-072 D4) |
| 5 | Labels of the four levels | Per-type labels | Spec: one set of labels; help text per type |
| 6 | Whether the upgrade copies a per-agent `Skill` value to skills granted later | It does | Spec: copied to skills granted at upgrade time only; a later grant follows the global value |

---

## BDD Scenarios

Oracles come from this spec (Contract Changes C-0 to C-10, Behavioral Contract) and the founder answers, never from today's code.

### Feature: Global tool permissions on one page (User Story 1)

#### Background

- **Given** a person signed in to Omnipus
- **And** the Skills & Tools page is open

#### Scenario: S-01 Person denies one Omnipus tool and it stays denied after reload

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the Omnipus tools tab with `send_email` at Ask
- **When** the person clicks Deny on the `send_email` row
- **Then** the row shows Deny and the page shows "Saved"
- **And** after a reload the row shows Deny
- **And** an agent's call of `send_email` is refused

#### Scenario: S-02 Person sets a whole group with Set all and no confirm appears

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the Files group with 12 tools shown, none being `execute_plan`
- **When** the person chooses Set all → Ask on the Files group header
- **Then** all 12 rows show Ask
- **And** exactly one save request is sent
- **But** no confirm dialog appears

#### Scenario: S-03 Person sets all tools of one external service

**Traces to**: User Story 1, Acceptance Scenario 3
**Category**: Happy Path

- **Given** the External tools tab and service `github` with 5 tools at Ask
- **When** the person chooses the `github` group's Set all → Allow
- **Then** all 5 rows show Allow and "Saved" appears

#### Scenario: S-04 No tool row ever shows Unset

**Traces to**: User Story 1, Acceptance Scenario 4
**Category**: Edge Case

- **Given** a service connected before this release whose tools had no stored value
- **When** the External tools tab opens after the upgrade
- **Then** every row of that service has exactly one of Allow, Ask, Deny selected
- **But** the word "Unset" appears nowhere on the page

#### Scenario: S-05 Set all that would allow autonomous plans asks first [pending ST-Q7]

**Traces to**: User Story 1, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** the Plans group, containing `execute_plan` at Ask
- **When** the person chooses Set all → Allow on that group
- **Then** "Confirm autonomous plan execution" appears before anything is saved
- **And** choosing Cancel leaves every row of the group unchanged

#### Scenario: S-06 Tool search has no permission and Set all skips it

**Traces to**: User Story 1, Acceptance Scenario 6
**Category**: Edge Case

- **Given** the group that contains Tool search
- **When** the person chooses Set all → Deny on that group
- **Then** every other row shows Deny
- **But** Tool search still shows "Always available for discovery. The tools it finds still follow their own permissions." and no badges

#### Scenario: S-07 A refused save shows the reason and the stored value

**Traces to**: User Story 1, Acceptance Scenario 7
**Category**: Error Path

- **Given** the server will refuse the next save with error "invalid policy value"
- **When** the person clicks Allow on `bash`
- **Then** the page shows "Not saved: invalid policy value." with Retry
- **And** the `bash` row shows the value stored before the click

#### Scenario: S-08 Saved but not applied is said plainly

**Traces to**: User Story 1, Acceptance Scenario 8
**Category**: Error Path

- **Given** the server will answer the next save with 503 (saved, reload failed)
- **When** the person clicks Deny on `fetch_url`
- **Then** the page shows "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." with Retry
- **And** the row shows Deny

### Feature: Per-skill permissions (User Story 2)

#### Background

- **Given** agent Mia is granted skills `plan` and `deep-research`
- **And** both skills are verified

#### Scenario: S-09 Denying a skill removes it from the agent's menu and refuses its load

**Traces to**: User Story 2, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `deep-research` at Allow globally and no Mia override
- **When** the person clicks Deny on the `deep-research` row of the Skills tab
- **Then** a new turn of Mia does not list `deep-research` in its skill list
- **And** Mia's load of `deep-research` by name is refused naming the skill setting

#### Scenario: S-10 An Ask skill raises the normal approval card in a chat

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Happy Path

- **Given** `deep-research` at Ask globally and Auto-approve off
- **When** Mia loads `deep-research` in a web chat
- **Then** an approval card appears with tool `Skill`, the skill named "Deep Research", and the buttons Approve Once, Deny and Always Allow

#### Scenario: S-11 Always Allow remembers the skill load for the chat

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** the card of S-10 answered with Always Allow
- **When** Mia loads `deep-research` again in the same chat
- **Then** it loads with no card

#### Scenario: S-12 An Ask skill in an unattended run is refused without a card

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Error Path

- **Given** `deep-research` at Ask
- **When** a scheduled task run of Mia loads `deep-research`
- **Then** the load is refused with "Not run: needs approval, and task and plan runs never ask."
- **But** no approval card appears anywhere

#### Scenario: S-13 An allowed, granted skill loads with no card

**Traces to**: User Story 2, Acceptance Scenario 4
**Category**: Happy Path

- **Given** `plan` at Allow
- **When** Mia loads `plan`
- **Then** the skill's instructions are returned with no card

#### Scenario: S-14 A skill not granted is refused whatever its permission

**Traces to**: User Story 2, Acceptance Scenario 5
**Category**: Error Path

- **Given** skill `verify` at Allow, not granted to Mia
- **When** Mia loads `verify`
- **Then** the load is refused as not granted

#### Scenario: S-15 The Skill tool row points to the Skills tab

**Traces to**: User Story 2, Acceptance Scenario 6
**Category**: Happy Path

- **Given** the Omnipus tools tab
- **When** the Skills group is opened
- **Then** the `Skill` row shows "Permissions are set per skill on the Skills tab." with a link that selects the Skills tab
- **But** it has no Allow / Ask / Deny badges

#### Scenario Outline: S-16 Upgrade keeps every agent's skill behaviour

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Edge Case

- **Given** before the upgrade the global `Skill` value is `<old_global>` and Mia's own `Skill` value is `<old_agent>`
- **When** Omnipus starts on this release
- **Then** every skill's global permission is `<new_global>`
- **And** Mia's override for `plan` and `deep-research` is `<new_agent>`

**Examples**:

| old_global | old_agent | new_global | new_agent |
|---|---|---|---|
| allow | none | allow | none |
| allow | deny | allow | deny |
| ask | allow | ask | allow (floored to ask at run time) |
| deny | none | deny | none |

#### Scenario: S-17 A newly installed skill starts at Allow and Name and short description

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Happy Path

- **Given** no skill named `weather`
- **When** the person installs `weather` from the catalog with category Lifestyle
- **Then** its row shows Allow and "Name and short description"

### Feature: Agent Tools tab, stricter only, no presets (User Story 3)

#### Scenario: S-18 No preset control on any agent's Tools tab

**Traces to**: User Story 3, Acceptance Scenario 1
**Category**: Happy Path

- **Given** agents Mia, Jim and a custom Main agent
- **When** each agent's Tools tab is opened
- **Then** none shows Cautious, Balanced or Full access

#### Scenario Outline: S-19 An agent row offers only values at least as strict as the global value

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Edge Case

- **Given** the global value of `<item>` is `<global>`
- **When** Mia's Tools tab shows the `<item>` row
- **Then** the enabled badges are `<enabled>` and the disabled ones show "Global setting is <global>; an agent can only be stricter."

**Examples**:

| item | global | enabled |
|---|---|---|
| bash (tool) | Allow | Allow, Ask, Deny |
| bash (tool) | Ask | Ask, Deny |
| plan (skill) | Ask | Ask, Deny |
| plan (skill) | Deny | Deny |

#### Scenario: S-20 Skills group lists the agent's granted skills

**Traces to**: User Story 3, Acceptance Scenario 3
**Category**: Happy Path

- **Given** Mia is granted `plan` and `deep-research` out of 10 installed skills
- **When** Mia's Tools tab opens
- **Then** its Skills group lists exactly `plan` and `deep-research`, each with Allow / Ask / Deny

#### Scenario: S-21 Inherit removes a skill override

**Traces to**: User Story 3, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** Mia has a Deny override for `plan` and the global value is Allow
- **When** the person presses Inherit on Mia's `plan` row
- **Then** the row shows Allow and Mia has no override for `plan`

#### Scenario: S-22 Omnipus refuses a per-agent Ask on a skill

**Traces to**: User Story 3, Acceptance Scenario 5
**Category**: Error Path

- **Given** Omnipus's Tools tab
- **When** the person clicks Ask on Omnipus's `write-skill` row
- **Then** the save is refused with "Omnipus takes no per-agent Ask; set Ask globally or use Deny." at the start of the message
- **And** the row shows the stored value

#### Scenario: S-23 System agents stay read-only

**Traces to**: User Story 3, Acceptance Scenario 6
**Category**: Edge Case

- **Given** the Judge agent
- **When** its Tools tab opens
- **Then** every badge, including the Skills group, is disabled with today's read-only notice

#### Scenario: S-24 Agent with no granted skills

**Traces to**: User Story 3, Acceptance Scenario 7
**Category**: Edge Case

- **Given** a custom agent with no granted skills
- **When** its Tools tab opens
- **Then** the Skills group reads "No skills granted. Grant skills on this agent's Skills tab."

#### Scenario: S-25 Per-agent changes save on their own

**Traces to**: User Story 3, Acceptance Scenario 8
**Category**: Happy Path

- **Given** Mia's Tools tab
- **When** the person clicks Deny on Mia's `plan` row
- **Then** the change is stored with no Save button pressed, and reopening the tab shows Deny

### Feature: Skill trust as Allow / Ask / Deny (User Story 4)

#### Scenario: S-26 Trust block is first with all meanings shown

**Traces to**: User Story 4, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the Skills tab
- **When** it opens
- **Then** "Unverified skills" is the first block, with Allow, Ask, Deny in that order
- **And** the lines "Unverified skills install and load without a check.", "Installing or loading an unverified skill asks you first." and "Unverified skills cannot be installed or loaded." are all visible

#### Scenario: S-27 Trust Deny disables installing an unverified skill in the review

**Traces to**: User Story 4, Acceptance Scenario 2
**Category**: Error Path

- **Given** trust Deny and an uploaded `my-skill.zip` that is unverified
- **When** the install review opens
- **Then** "Install skill" is disabled with "Unverified skills are set to Deny above. Choose Ask or Allow to install this skill."

#### Scenario: S-28 Trust Deny refuses an unverified install from an agent tool

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path

- **Given** trust Deny
- **When** `install_skill` installs an unverified skill
- **Then** it is refused with "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed."
- **And** no folder for that skill exists afterwards

#### Scenario: S-29 Trust Deny refuses loading an installed unverified skill

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path

- **Given** unverified skill `notes-helper` installed under trust Allow, granted to Mia, permission Allow
- **And** trust is then set to Deny
- **When** Mia loads `notes-helper`
- **Then** the load is refused with "`notes-helper` is unverified, and unverified skills are set to Deny in Skills & Tools."

#### Scenario: S-30 Trust Ask asks for an unverified skill even under Auto-approve

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Happy Path

- **Given** trust Ask, Auto-approve on, unverified `notes-helper` at Allow, granted to Mia
- **When** Mia loads `notes-helper` in a chat
- **Then** a trust card appears naming "notes-helper", marked unverified, with "Approve this version" and Deny and no Always Allow [pending ST-Q2]

#### Scenario: S-31 Trust Ask applies in God Mode [pending ST-Q2]

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case

- **Given** God Mode on and the setup of S-30
- **When** Mia loads `notes-helper` in a chat
- **Then** a trust card appears (C-12) [pending ST-Q2]

#### Scenario: S-32 Choosing Allow shows the warning

**Traces to**: User Story 4, Acceptance Scenario 5
**Category**: Alternate Path

- **Given** trust Ask
- **When** the person clicks Allow in the trust block
- **Then** a warning callout reads "Allow turns off the check for unverified skills. Choose Ask to turn it back on."
- **And** at 390 px width the whole callout is visible on screen after the click

#### Scenario Outline: S-33 Legacy trust values are carried over

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case

- **Given** `sandbox.skill_trust` stored as `<legacy>`
- **When** Omnipus starts on this release
- **Then** the trust block shows `<new>` and one audit entry records the change

**Examples**:

| legacy | new |
|---|---|
| block_unverified | Deny |
| warn_unverified | Ask |
| allow_all | Allow |
| (empty) | Ask |

#### Scenario: S-34 A locally changed registry skill becomes unverified

**Traces to**: User Story 4, Acceptance Scenario 7
**Category**: Edge Case

- **Given** `web-search` installed from the registry with a matching hash, shown as Verified
- **When** a file in its folder is changed outside Omnipus
- **Then** the Skills tab shows it without Verified, and trust applies to it

#### Scenario: S-35 Built-in skills load under trust Deny

**Traces to**: User Story 4, Acceptance Scenario 8
**Category**: Edge Case

- **Given** trust Deny and built-in `plan` granted to Mia at Allow
- **When** Mia loads `plan`
- **Then** it loads with no card

#### Scenario: S-36 A legacy trust value sent to the endpoint is refused

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Error Path

- **Given** trust Ask
- **When** a client sends `PATCH /security/permissions` with `skill_trust: warn_unverified`
- **Then** the answer is 400 with "Use allow, ask or deny." and trust stays Ask
- **But** today's Settings control sending `warn_unverified` to `PUT /security/skill-trust` still succeeds and stores Ask (C-22)

### Feature: What the model sees (User Story 5)

#### Scenario: S-37 The level control offers four levels with explanations

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the `serve_web` row
- **When** the person opens "What the model sees"
- **Then** the options are Hidden, Name only, Name and short description, Full, each with a one-line explanation

#### Scenario Outline: S-38 Each tool level changes what an allowed agent is sent

**Traces to**: User Story 5, Acceptance Scenarios 2, 3, 4
**Category**: Happy Path

- **Given** Mia allowed `serve_web`, and `serve_web` at `<level>`, in a new chat with nothing loaded
- **When** Mia's next turn starts
- **Then** `<sent>`

**Examples**:

| level | sent |
|---|---|
| Full | the full definition of `serve_web` is in the tool list |
| Name and short description | one line "serve_web — <first line of its description>" is listed; no definition until a ToolSearch load |
| Name only | the name `serve_web` is listed with no description; no definition until a ToolSearch load |
| Hidden | nothing about `serve_web` is listed; a ToolSearch query "serve web preview" finds and loads it |

#### Scenario: S-39 A locked tool's level cannot change

**Traces to**: User Story 5, Acceptance Scenario 5
**Category**: Error Path

- **Given** the Ask the user row
- **When** it is shown
- **Then** its level shows Full with "Always loaded: agents need this to work, so its level cannot be changed." as visible text
- **And** a direct request setting its level to Hidden is refused with 400

#### Scenario: S-40 Deny wins over any level

**Traces to**: User Story 5, Acceptance Scenario 6
**Category**: Edge Case

- **Given** `fetch_url` at Full globally and Mia's own value Deny
- **When** Mia's next turn starts
- **Then** `fetch_url` is not in Mia's tool list and a ToolSearch for it finds nothing

#### Scenario Outline: S-41 Upgrade keeps what agents are sent

**Traces to**: User Story 5, Acceptance Scenarios 7, 9
**Category**: Edge Case

- **Given** an install from before this release with `<old settings>`
- **When** Omnipus starts on this release
- **Then** `<tool>` shows `<level>`
- **And** a request captured from Mia's first turn in a new chat lists `<tool>` exactly as a request captured before the upgrade did (literal fixture, not built by the new code)

**Examples**: every row of Dataset D7.

#### Scenario: S-42 A Full skill is in context from the first turn

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Alternate Path

- **Given** `plan` at Full, granted to Mia and Allow for Mia, with the sentinel sentence "ZEBRA-PLAN-7" in its instructions
- **When** a new chat with Mia starts
- **Then** the captured first request holds the whole `plan` instructions, including "ZEBRA-PLAN-7", without a `Skill` call [pending ST-Q1]

#### Scenario Outline: S-43 Each skill level changes the skill menu

**Traces to**: User Story 5, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `deep-research` granted to Mia and allowed, at `<level>`
- **When** Mia's next turn starts
- **Then** the skill menu `<menu>`

**Examples**:

| level | menu |
|---|---|
| Name and short description | lists `deep-research` with its description |
| Name only | lists `deep-research` without its description |
| Hidden | does not list `deep-research`; `Skill` with query "research sources" returns it |

#### Scenario: S-44 Levels are the only control after the upgrade [pending ST-Q4]

**Traces to**: User Story 5, Acceptance Scenario 9
**Category**: Alternate Path

- **Given** an install that had "Load tools on demand" off and `browser_evaluate` allowed for Mia
- **When** Omnipus starts on this release
- **Then** `browser_evaluate` shows Full and Mia's first-turn request carries its definition, as before
- **And** the tool tabs show no "Load tools on demand" note
- **And** `set_config` with `tools.manifest.compressed: true` is refused with "This setting was replaced by levels in Skills & Tools."

### Feature: Search, filters and Set all scope (User Story 6)

#### Scenario: S-45 Page opens on Skills with counts

**Traces to**: User Story 6, Acceptance Scenario 1
**Category**: Happy Path

- **Given** 53 skills, 3 services with 86 tools, 114 Omnipus tools
- **When** the page opens
- **Then** the tabs read "Skills 53", "External tools (MCP) 86", "Omnipus tools 114" and Skills is selected

#### Scenario: S-46 Groups start collapsed and search opens matches

**Traces to**: User Story 6, Acceptance Scenarios 2, 3
**Category**: Happy Path

- **Given** the Omnipus tools tab with every group collapsed
- **When** the person types "browser" in the search field
- **Then** only the Browser group opens and only rows matching "browser" show

#### Scenario: S-47 Escape clears the search

**Traces to**: User Story 6, Acceptance Scenario 3
**Category**: Alternate Path

- **Given** the search field holds "browser" and has focus
- **When** the person presses Escape
- **Then** the field is empty and all groups show collapsed

#### Scenario: S-48 Set all under a filter changes only visible rows

**Traces to**: User Story 6, Acceptance Scenario 4
**Category**: Edge Case

- **Given** the Skills tab, Permission filter = Deny, and 1 of 53 skills at Deny
- **When** the person uses "Set 1 of 1 matching" → Allow in All skills
- **Then** exactly 1 skill changes to Allow
- **And** the control's accessible name starts "Set 1 of 1 matching"

#### Scenario: S-49 No match shows the empty state

**Traces to**: User Story 6, Acceptance Scenario 5
**Category**: Error Path

- **Given** the Omnipus tools tab
- **When** the person types "zzzz"
- **Then** "No tools match “zzzz”" shows with "Clear search and filters"

#### Scenario: S-50 A changed row leaves a filtered list

**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Edge Case

- **Given** the Permission filter = Ask showing `bash`
- **When** the person clicks Deny on `bash`
- **Then** after the save `bash` is no longer shown, and clearing the filter shows it at Deny

#### Scenario: S-51 Level filter explains denied items

**Traces to**: User Story 6, Acceptance Scenario 7
**Category**: Alternate Path

- **Given** the Omnipus tools tab
- **When** the person sets "What the model sees" = Hidden
- **Then** the note "Denied items are not loaded, so this filter leaves them out. Use the Permission filter to find them." is shown

### Feature: Adding skills (User Story 7)

#### Scenario: S-52 New skill menu has three items and no retired name

**Traces to**: User Story 7, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the Skills tab
- **When** the person opens New skill
- **Then** the items are "Write with Omnipus", "Upload a skill", "Browse the catalog"
- **But** no item contains "Ava" or "Admin"

#### Scenario: S-53 Write with Omnipus starts the write-skill workflow

**Traces to**: User Story 7, Acceptance Scenario 2
**Category**: Happy Path

- **Given** the connection is up
- **When** the person chooses "Write with Omnipus"
- **Then** the Omnipus chat opens, shows the person's message "Write a new skill", and Omnipus loads the `write-skill` skill

#### Scenario: S-54 Write with Omnipus while disconnected sends nothing

**Traces to**: User Story 7, Acceptance Scenario 3
**Category**: Error Path

- **Given** the connection is down
- **When** the person chooses "Write with Omnipus"
- **Then** the warning "Not connected — “Write a new skill” was not sent. Press the button again once you are reconnected." appears and no message is queued

#### Scenario: S-55 Install review requires a category

**Traces to**: User Story 7, Acceptance Scenarios 4, 5
**Category**: Error Path

- **Given** the install review of catalog skill `weather` with no category chosen
- **When** the person presses Install skill
- **Then** the Category field shows "Choose a category." and nothing is installed

#### Scenario: S-56 Category from the skill file is preselected

**Traces to**: User Story 7, Acceptance Scenario 6
**Category**: Alternate Path

- **Given** an uploaded `SKILL.md` with `metadata.category: research`
- **When** the install review opens
- **Then** Category shows Research

#### Scenario: S-57 Skills are grouped by category

**Traces to**: User Story 7, Acceptance Scenario 7
**Category**: Happy Path

- **Given** skills in Research (1), Tools (12) and none in Blockchain, plus 31 without a category
- **When** the Skills tab lists them
- **Then** groups RESEARCH 1, TOOLS 12 and UNCATEGORISED 31 show
- **But** no Blockchain group shows

#### Scenario: S-58 Built-in skill rows offer only Make a copy

**Traces to**: User Story 7, Acceptance Scenario 8
**Category**: Edge Case

- **Given** built-in `plan` and installed `weather`
- **When** More actions opens on each
- **Then** `plan` offers Make a copy only, `weather` offers Make a copy and Remove

### Feature: External services (User Story 8)

#### Scenario: S-59 A new service's tools start at Ask, including later ones

**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Happy Path

- **Given** no service `notion` and no global key covering `mcp_notion_`
- **When** the person adds service `notion` with 4 tools
- **Then** all 4 tool rows show Ask
- **And** when `notion` later reports a 5th tool, that row also shows Ask

#### Scenario: S-60 Service switch acts at once

**Traces to**: User Story 8, Acceptance Scenario 2
**Category**: Happy Path

- **Given** service `github` on
- **When** the person turns its switch off
- **Then** the service is off after the click with no further action, and its tools are not offered to agents

#### Scenario: S-61 A failing service shows its reason with Retry and Edit

**Traces to**: User Story 8, Acceptance Scenario 3
**Category**: Error Path

- **Given** service `context7` cannot connect with reason "missing secret reference CONTEXT7_KEY"
- **When** its group is opened
- **Then** an error notice shows that reason with Retry and Edit

#### Scenario: S-62 Removing a service asks first

**Traces to**: User Story 8, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** service `github`
- **When** the person presses Remove in its header
- **Then** "Remove this service? Agents that use it lose its tools." appears, and Cancel keeps the service

#### Scenario: S-63 The safe choice is the main button on the local-program confirm

**Traces to**: User Story 8, Acceptance Scenario 5
**Category**: Edge Case

- **Given** Add service
- **When** the person picks "A local program"
- **Then** "This runs a program on your server" opens with "Use a network address instead" as the main button and focused

### Feature: Tool settings and links (User Story 9)

#### Scenario: S-64 Web & Search settings open in a side panel

**Traces to**: User Story 9, Acceptance Scenario 1
**Category**: Happy Path

- **Given** the Web & Search group
- **When** the person presses its Settings button
- **Then** a side panel shows Default search and Fallback cards, and choosing a new default saves it as Settings → Integrations did

#### Scenario: S-65 Shell and Protection links

**Traces to**: User Story 9, Acceptance Scenarios 2, 3, 4
**Category**: Happy Path

- **Given** the Omnipus tools tab
- **When** it opens
- **Then** the intro reads "Open Settings to change Auto-approve and God Mode." with a link, and the Shell group shows "Shell safety rules (Settings)"
- **And** following each link opens today's Settings screen at the section that holds that setting
- **But** no link points to Settings for tool permissions

### Feature: States, phone and keyboard (User Story 10)

#### Scenario: S-66 Loading state disables writing

**Traces to**: User Story 10, Acceptance Scenario 1
**Category**: Alternate Path

- **Given** the skills request has not answered
- **When** the Skills tab shows
- **Then** "Loading skills…" and skeleton rows show, the count reads "…", and search, filters and badges are disabled

#### Scenario: S-67 Load failure shows cause and Retry

**Traces to**: User Story 10, Acceptance Scenario 2
**Category**: Error Path

- **Given** the tools request fails with "Omnipus did not respond"
- **When** the Omnipus tools tab shows
- **Then** an error callout shows that cause with Retry and no badge is enabled

#### Scenario: S-68 Phone layout

**Traces to**: User Story 10, Acceptance Scenario 3
**Category**: Edge Case

- **Given** a 390 × 844 viewport
- **When** each tab is shown
- **Then** rows are cards with "Permission" and "What the model sees" lines, every control is at least 44 × 44 px, and the page does not scroll sideways

#### Scenario: S-69 Keyboard operation of a row

**Traces to**: User Story 10, Acceptance Scenario 4
**Category**: Happy Path

- **Given** focus on the Allow badge of `bash`
- **When** the person presses Tab, then Enter
- **Then** focus moves to Ask, Enter selects Ask, and the status region announces "Saved"

### Feature: Agent tool parity (User Story 11)

#### Scenario: S-70 Agent sets one agent's skill permission

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path

- **Given** global `plan` at Ask
- **When** Omnipus calls `set_agent_permissions` for Mia with `skill_policy_changes: {set: {plan: allow}}` and the person approves
- **Then** Mia's override `plan: allow` is stored
- **And** the result carries the clamp sentence saying the global value Ask applies

#### Scenario: S-71 install_skill takes a category

**Traces to**: User Story 11, Acceptance Scenario 2
**Category**: Happy Path

- **Given** trust Allow
- **When** `install_skill` installs `weather` with `category: lifestyle`
- **Then** `weather` shows in the LIFESTYLE group

#### Scenario: S-72 install_skill refuses an unknown category

**Traces to**: User Story 11, Acceptance Scenario 2
**Category**: Error Path

- **Given** any trust value
- **When** `install_skill` is called with `category: games`
- **Then** it is refused naming the allowed categories and nothing is installed

#### Scenario: S-73 set_config cannot change global skill settings

**Traces to**: User Story 11, Acceptance Scenario 3
**Category**: Error Path

- **Given** any agent allowed `set_config`
- **When** it sets `sandbox.skill_policies.plan` to `allow`
- **Then** the call is refused with the sandbox reason and nothing changes

#### Scenario: S-74 get_agent reports skill permissions

**Traces to**: User Story 11, Acceptance Scenario 4
**Category**: Happy Path

- **Given** Mia granted `plan` (global Ask, override deny) and `deep-research` (global Allow, no override)
- **When** `get_agent` is called for Mia with `fields: ["skill_permissions"]`
- **Then** it lists `plan` effective deny override deny, and `deep-research` effective allow override null

### Feature: Riders (User Story 12)

#### Scenario: S-75 Built-in orchestrate skill names no retired agent (#1920)

**Traces to**: User Story 12, Acceptance Scenario — rider #1920
**Category**: Error Path

- **Given** the built-in skills of this release
- **When** their `SKILL.md` text is searched for the words Ava and Admin as agent names
- **Then** none is found

#### Scenario: S-76 Readouts include God Mode (#1297)

**Traces to**: User Story 12, Acceptance Scenario — rider #1297
**Category**: Edge Case

- **Given** God Mode on, global `send_email` Ask, Mia's own value Allow
- **When** Mia's Tools tab and `get_agent` `tool_permissions` are read
- **Then** both show effective Allow for `send_email`, matching what the run does

#### Scenario: S-77 Agent tools view lists assigned service tools (#1298)

**Traces to**: User Story 12, Acceptance Scenario — rider #1298
**Category**: Error Path

- **Given** Mia assigned service `github` with 5 tools
- **When** Mia's Tools tab opens
- **Then** the 5 `github` tools are listed with their values

#### Scenario: S-78 Reopening an agent shows the saved value (#1411)

**Traces to**: User Story 12, Acceptance Scenario — rider #1411
**Category**: Error Path

- **Given** the person set Mia's `send_message` to Allow and closed the agent editor
- **When** the editor is reopened without a page reload
- **Then** `send_message` shows Allow

#### Scenario: S-79 Doctor warns when unverified skills are allowed (#99)

**Traces to**: User Story 12, Acceptance Scenario — rider #99
**Category**: Edge Case

- **Given** trust Allow
- **When** `omnipus doctor` runs
- **Then** it prints a warning that unverified skills are allowed without a check, and how to choose Ask in Skills & Tools

#### Scenario: S-80 One unreadable skill does not empty the list (#1570)

**Traces to**: User Story 12, Acceptance Scenario — rider #1570
**Category**: Error Path

- **Given** 10 skills, one with an unreadable helper file
- **When** the Skills tab loads
- **Then** 10 rows show, and the broken one shows "Cannot load" with the reason

#### Scenario: S-81 A copy shows its own name (#1576)

**Traces to**: User Story 12, Acceptance Scenario — rider #1576
**Category**: Edge Case

- **Given** built-in `plan`
- **When** the person makes a copy named `plan-mine`
- **Then** the new row is labelled `plan-mine`, not "Plan"

### Feature: Round-1 additions (saving, authority, consent, migration, parity)

#### Scenario: S-82 Two tabs changing different keys both keep their change

**Traces to**: User Story 1, Acceptance Scenario 9
**Category**: Edge Case

- **Given** tabs A and B opened on Skills & Tools with `send_email` at Ask and `plan` at Allow
- **When** tab A sets `send_email` to Deny and then tab B sets `plan` to Ask
- **Then** `GET /security/permissions` returns `send_email: deny` and `plan: ask`
- **And** tab B's request body names only `plan`

#### Scenario: S-83 A lost response is never shown as Not saved

**Traces to**: User Story 1, Acceptance Scenario 10
**Category**: Error Path

- **Given** the server stores the next change but its response is dropped
- **When** the person clicks Deny on `fetch_url`
- **Then** the page shows "Couldn't confirm the save. Checking…", then Deny with "Showing what is stored now."
- **But** the row never shows the old value as if the save had failed

#### Scenario: S-84 Set all skips the always-ask tool and says so

**Traces to**: User Story 1, Acceptance Scenario 2
**Category**: Edge Case

- **Given** a group of 12 rows including `set_channel_outbound`
- **When** the person opens Set all
- **Then** the Allow item reads "Set 11 of 12 to Allow"
- **And** after choosing it, 11 rows show Allow, `set_channel_outbound` keeps its value, and a note reads "1 skipped: Send to channel always asks."

#### Scenario: S-85 A denied skill's files cannot be read or run by the agent

**Traces to**: User Story 2, Acceptance Scenario 9
**Category**: Error Path

- **Given** Mia granted `notes-helper`, Mia's own value for it Deny, and `edit_skill` denied for Mia
- **When** Mia reads `notes-helper/SKILL.md` with `read_file` and runs its helper script with `bash`
- **Then** both are refused as for an ungranted skill
- **And** the kernel grant list for Mia's turn does not contain the `notes-helper` folder

#### Scenario: S-86 An Ask skill's files open only after approval

**Traces to**: User Story 2, Acceptance Scenario 9
**Category**: Edge Case

- **Given** `deep-research` at Ask, granted to Mia, not yet approved in this chat
- **When** Mia reads its `SKILL.md` with `read_file`
- **Then** the read is refused
- **And** after the person approves a `Skill` load of it, the same read succeeds in that chat

#### Scenario: S-87 An editor agent can read but not use a denied skill

**Traces to**: User Story 2, Acceptance Scenario 9
**Category**: Edge Case

- **Given** Omnipus allowed `edit_skill` and skill `notes-helper` Deny for Omnipus
- **When** Omnipus reads `notes-helper/SKILL.md` with `read_file` and then loads it with `Skill`
- **Then** the read succeeds and the load is refused naming the skill setting

#### Scenario Outline: S-88 External tools without global coverage are migrated exactly

**Traces to**: User Story 8, Acceptance Scenario 1
**Category**: Edge Case

- **Given** before the upgrade the global map has `<global>`, Mia has `<agent>` for `mcp_github_read`, God Mode off, and `github` assigned to Mia
- **When** Omnipus starts on this release
- **Then** the global map gains `<written>` and Mia's effective value for `mcp_github_read` is `<after>`
- **And** Mia's own entries are unchanged

**Examples**: every row of Dataset D8.

#### Scenario: S-89 A Full skill that needs approval is not injected [pending ST-Q1]

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Error Path

- **Given** `notes-helper` at Full, granted to Mia, unverified, trust Ask, no approval, sentinel "ZEBRA-NOTES-3" in its instructions
- **When** a new chat with Mia starts and Mia's first request is captured
- **Then** the request does not contain "ZEBRA-NOTES-3" and lists `notes-helper` with its description
- **And** a scheduled run of Mia's captured request does not contain it either

#### Scenario: S-90 Full skills that do not fit stop the turn with a reason [pending ST-Q1]

**Traces to**: User Story 5, Acceptance Scenario 11
**Category**: Error Path

- **Given** a scripted model with an 8,000-token context and Mia with Full skills totalling about 9,000 tokens
- **When** the person sends a message to Mia
- **Then** no request is sent to the model
- **And** the chat shows "The skills set to Full for Mia need about 9,000 tokens, more than <model> can take with this chat. Set some of them to Name and short description in Skills & Tools, or choose a model with a larger context."

#### Scenario: S-91 A level change does not unload a tool from a running chat

**Traces to**: User Story 5, Acceptance Scenario 10
**Category**: Edge Case

- **Given** a chat in which Mia loaded `browser_evaluate` through ToolSearch
- **When** the person sets `browser_evaluate` to Hidden and Mia's next turn starts
- **Then** that chat's request still carries the `browser_evaluate` definition
- **And** a new chat's first request does not list `browser_evaluate`

#### Scenario: S-92 A demoted built-in becomes searchable

**Traces to**: User Story 5, Acceptance Scenario 10
**Category**: Edge Case

- **Given** `read_file` at Full (its starting level)
- **When** the person sets it to Hidden
- **Then** a new chat's first request does not list `read_file`
- **And** ToolSearch with query "read file" returns `read_file`

#### Scenario: S-93 A trust approval covers one version only [pending ST-Q2]

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case

- **Given** trust Ask and `notes-helper` approved with "Approve this version"
- **When** one of its files changes and Mia loads it again
- **Then** a trust card appears again
- **And** with God Mode on and Auto-approve on, the trust card still appears

#### Scenario: S-94 The install publishes exactly what was reviewed

**Traces to**: User Story 7, Acceptance Scenarios 4, 10
**Category**: Error Path

- **Given** a review of uploaded `weather.zip` with revision R1
- **When** the staged files change before Install and the person presses Install
- **Then** the answer is "The skill changed after review. Review it again." and nothing is installed
- **And** pressing Cancel before Install discards the staged copy and shows "Nothing was installed."

#### Scenario: S-95 A direct install under trust Ask needs a review

**Traces to**: User Story 4, Acceptance Scenario 3
**Category**: Error Path

- **Given** trust Ask
- **When** a client calls `POST /skills/install` for an unverified skill without `review_id`
- **Then** the answer is 409 "Review needed: this skill is unverified, and unverified skills are set to Ask. Review it in Skills & Tools first." and nothing is installed

#### Scenario: S-96 ToolSearch finds a Hidden skill and labels it [pending ST-Q6]

**Traces to**: User Story 5, Acceptance Scenario 4
**Category**: Happy Path

- **Given** `deep-research` granted to Mia, Allow, at Hidden
- **When** Mia calls ToolSearch with "research sources"
- **Then** a result line starts with "[skill] deep-research" and says "Load it with the Skill tool."
- **And** the skill is not loaded by the search
- **But** a skill Denied for Mia is not returned

#### Scenario: S-97 An unrelated agent update keeps skill overrides

**Traces to**: User Story 11, Acceptance Scenario 5
**Category**: Error Path

- **Given** Mia with skill override `plan: deny`
- **When** `PUT /agents/mia` sends `tools_cfg` with only `mcp`
- **Then** Mia's `plan: deny` override is still stored

#### Scenario: S-98 Write with Omnipus ends with a categorised skill

**Traces to**: User Story 7, Acceptance Scenario 9
**Category**: Happy Path

- **Given** a scripted model driving Omnipus's write-skill workflow
- **When** the person approves the card for skill `trip-notes` with category Lifestyle
- **Then** `GET /skills` lists `trip-notes` with category `lifestyle` and no agent granted
- **And** `trip-notes/SKILL.md` contains no category field added by Omnipus

#### Scenario: S-99 Each changed key is audited once

**Traces to**: User Story 1, Acceptance Scenario 1
**Category**: Happy Path

- **Given** an empty audit log
- **When** the person sets `bash` to Deny, sets `plan` to Ask, runs Set all Allow on a group of 5 that changes 5 rows, re-sends `plan: ask`, and sends one refused request
- **Then** the audit log holds exactly 7 change entries, each with actor, key, old and new value

#### Scenario: S-100 Focus after the focused row leaves a filtered list

**Traces to**: User Story 6, Acceptance Scenario 6
**Category**: Edge Case

- **Given** Permission filter = Ask showing 3 rows, focus on the Deny badge of the last row
- **When** the person presses Enter and the save is acknowledged
- **Then** focus moves to the Deny badge of the previous row
- **And** the status region announces "<name> set to Deny. It no longer matches the filter. 2 shown."

#### Scenario: S-101 Today's Settings trust control still works at this landing

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case

- **Given** this landing without the Settings package
- **When** the person chooses Block unverified in today's Settings trust control
- **Then** it saves, and Skills & Tools shows trust Deny

#### Scenario: S-102 A project skill and an installed skill with one name keep separate values

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Edge Case

- **Given** installed `notes` at Allow and project skill `notes` in workspace W1 at Deny (`project:w1/notes`)
- **When** an agent in W1 loads `notes`
- **Then** the skill that `ResolveSkillName` picks decides which value applies, and the other value is unchanged

#### Scenario: S-103 create_agent sets skill permissions like the screen

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Happy Path

- **Given** global `plan` at Allow
- **When** Omnipus calls `create_agent` with `skills: ["plan"]` and `skill_policy_changes: {set: {plan: ask}}`
- **Then** the new agent's override `plan: ask` is stored and `get_agent` reports it

#### Scenario: S-104 A category can be chosen once for an Uncategorised skill

**Traces to**: User Story 7, Acceptance Scenario 8
**Category**: Alternate Path

- **Given** skill `trip-notes` stored as Uncategorised
- **When** the person chooses "Choose category" → Lifestyle, and later calls `PUT /skills/trip-notes/category` with `research`
- **Then** the first saves Lifestyle and the second is refused with 409 "The category is set at install and cannot be changed."

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit (Go) | skill policy resolution with trust and God Mode; reconcile/self-heal for skills and levels; trust migration; level-to-manifest builders; skill menu builder; contract validators | logic in isolation, table-driven from the datasets below |
| Integration (Go) | REST handlers C-2 to C-6, C-8; `install_skill`, `set_agent_permissions`, `get_agent`, `set_config`; agent loop turn with a scripted model | the server keeps its promises across boundaries |
| Unit (TS, Vitest) | editor rows, locks, Set all scope and count, filters, trust block, level menu, autosave signal, presets absent | the screen follows this spec |
| E2E (Playwright, targeted spec files) | page tabs, autosave + reload, agent Tools tab, install review, Write with Omnipus | the whole journey |

### Hands-on UAT rows (written at spec time)

Mandatory on the cut commit: this package touches `pkg/agent` and the tool-policy engine. Provider/model: a founder-consented model or a scripted model with no paid call (root `CLAUDE.md`).

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| ST-01 | US-1 AS1 | Omnipus tools → `send_email` Deny → reload; ask Mia to send an email | Deny after reload; Mia's call refused | global tool policy | re-drive Deny, reload, call | stable |
| ST-02 | US-1 AS2, US-6 AS4 | Filter Permission = Ask; Set all → Deny on one group | only the matching rows change; count in label; no confirm | Set all scope | count rows before/after | stable |
| ST-03 | US-1 AS5 | Set all → Allow on the Plans group | autonomous-plan confirm; Cancel changes nothing | execute_plan confirm | Cancel then reload | stable |
| ST-04 | US-2 AS1–4 | `deep-research` Ask; Mia loads it in chat; Always Allow; load again | card naming the skill; second load without card | skill load resolution | repeat in a new chat (card again) | stable |
| ST-05 | US-2 AS3 | `deep-research` Ask; scheduled task of Mia loads it | refused with the unattended text; no card | unattended path | check task conversation text | needs scheduler time control |
| ST-06 | US-2 AS7, US-5 AS7 | Capture Mia's and Jim's first-turn requests (scripted model, request log) on a saved pre-release data folder; upgrade; capture again | requests identical tool by tool and skill by skill, except the SC-003 named changes; a mutation that resets one migrated level makes the comparison fail | migration | diff the two captured requests, not the config | stable with fixture |
| ST-07 | US-3 AS1–3 | Open Mia → Tools | no presets; Skills group with granted skills; Allow disabled where global Ask | agent editor | open Jim and a custom agent too | stable |
| ST-08 | US-3 AS5 | Omnipus → Tools → a skill → Ask | refused with the Omnipus text | Omnipus guard | Deny is accepted | stable |
| ST-09 | US-4 AS2–3 | Trust Deny; (a) upload an unverified zip on screen; (b) ask Omnipus to install an unverified catalog skill; (c) `POST /skills/install` directly with a review id; then trust Ask and (d) direct `POST /skills/install` without a review | (a)–(c) refused, nothing installed; (d) 409 | trust at install | list the skills folder after each | stable |
| ST-10 | US-4 AS4 | Trust Ask, Auto-approve on; Mia loads an unverified granted skill | card naming the skill as unverified | trust at load | repeat with God Mode on | stable |
| ST-11 | US-4 AS6 | Start with stored `warn_unverified` | trust shows Ask; audit has one entry | trust migration | read audit log | stable with fixture |
| ST-12 | US-5 AS2–4, AS10 | Set `serve_web` to each level; capture the next request Mia sends; load it, then set Hidden and capture again; demote `read_file` to Hidden | matches S-38, S-91, S-92 | manifest | ToolSearch finds Hidden and demoted tools | needs request capture |
| ST-13 | US-5 AS8, US-5 AS11 | A test skill with sentinel "ZEBRA-PLAN-7": Full + Allow → capture first request; Full + Ask → capture (sentinel absent until approval); several large Full skills → too-large text | sentinel present only when allowed; too-large text shown, nothing sent | Full / consent / budget | the captured request, never the model's answer | stable with scripted model |
| ST-14 | US-6 AS1–5 | Open page; search "browser"; Escape; "zzzz" | as S-45 to S-49 | page | same on phone width | stable |
| ST-15 | US-7 AS2–3, AS9 | Write with Omnipus online: complete the card with category Research, approve; offline: press again | online: a new skill is listed under Research with no agent granted; offline warning, nothing sent | workflow start to published skill | reload Skills tab and check group and category | stable |
| ST-19 | US-2 AS9 | Mia granted `notes-helper` (Deny for Mia), `edit_skill` denied; ask Mia to read its SKILL.md with a file tool and to run its helper | both refused; then set Allow: both work | C-11 E6/E7 | repeat with Ask (refused until approved) | stable |
| ST-20 | US-1 AS9 | Two browser tabs: A denies `send_email`, B sets `plan` Ask; reload both | both stored | per-key save | read `GET /security/permissions` | stable |
| ST-21 | US-4 AS4 | Trust Ask, approve `notes-helper` v1; change one file; load again | second load asks again | trust approvals | God Mode on, repeat | stable |
| ST-22 | US-8 AS1, C-18 | Upgrade fixtures for each C-18 row | each row's "Agent after" holds; no agent entry written | MCP migration | diff agent configs before/after | stable with fixture |
| ST-23 | FR-040 | Change one tool, one skill, a Set all of 5, a no-op, a refused request | audit has exactly 1 + 1 + 5 + 0 + 0 entries with old/new | audit | read the audit log | stable |
| ST-24 | US-4 AS6, C-22 | On the same build, change trust in today's Settings screen, then on Skills & Tools | both screens show the same stored value | first-landing compatibility | reload both | stable |
| ST-16 | US-7 AS4–7 | Install from catalog with and without category; upload a file with `metadata.category` | category required; preselected; grouped | install review | reload and check group | stable |
| ST-17 | US-8 AS1 | Add a service with tools | all tools Ask | MCP add | restart and check still Ask | needs a test service |
| ST-18 | US-10 AS3, US-9 AS1 | 390 × 844: every tab, Web & Search panel | 44 px targets, no sideways scroll; panel works | phone, panel | measure targets | stable |

### Test Implementation Order

| Order | Test Name (placeholder) | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | TestSkillPolicyResolution_Table | Unit Go | S-09, S-13, S-19, S-29, S-30, S-31, S-35 | Dataset D1 |
| 2 | TestSkillPolicyReconcile_NewSkillAllow | Unit Go | S-17 | self-heal additive, never overwrites |
| 3 | TestSkillPolicyUpgrade_FromSkillTool | Unit Go | S-16 | Dataset D1 upgrade rows |
| 4 | TestSkillTrustMigration_LegacyValues | Unit Go | S-33 | Dataset D3 |
| 5 | TestPutSkillTrust_RefusesLegacy | Integration Go | S-36 | 400 text |
| 6 | TestInstall_TrustDenyRefusesUnverified (REST and install_skill) | Integration Go | S-27, S-28 | nothing on disk afterwards |
| 7 | TestSkillVerified_LocalChangeUnverifies | Integration Go | S-34 | stored verification + local change |
| 8 | TestPermissionsPatch_SkillAndVisibilityMembers | Integration Go | S-01, S-09, S-39 | Dataset D6 |
| 9 | TestVisibilityStartingLevels_Upgrade | Unit Go | S-41 | Dataset D7 |
| 10 | TestManifest_LevelsShapeTurn | Unit Go | S-38, S-40, S-44 | Dataset D2 |
| 11 | TestSkillsSummary_Levels | Unit Go | S-42, S-43 | menu per level |
| 12 | TestSkillLoad_AskUnattendedRefused | Integration Go | S-12 | unattended text |
| 13 | TestSkillApprovalFrame_CarriesSkill | Integration Go | S-10, S-11 | C-9 |
| 14 | TestAgentToolsPut_SkillsPolicies | Integration Go | S-21, S-22, S-23, S-25 | C-4 |
| 15 | TestSetAgentPermissions_SkillPolicyChangesParity | Integration Go | S-70 | parity |
| 16 | TestGetAgent_SkillPermissions | Integration Go | S-74 | C-9 |
| 17 | TestInstallSkillCategoryParity | Integration Go | S-71, S-72 | parity |
| 18 | TestSetConfig_RefusesSandboxSkillKeys | Integration Go | S-73 | refusal row |
| 19 | TestMcpAdd_WritesAskWildcard | Integration Go | S-59, S-04 | C-7 |
| 20 | TestWorkflowStart_W5 | Integration Go | S-53 | C-8 |
| 21 | ToolPolicyEditor.noPresets.test | Unit TS | S-18 | no preset text; presets module absent |
| 22 | ToolPolicyEditor.locks.test | Unit TS | S-19, S-24 | Dataset D1 screen rows |
| 23 | SkillsToolsPage.setAll.test | Unit TS | S-02, S-05, S-06, S-48 | Dataset D4 |
| 24 | SkillsToolsPage.autosave.test | Unit TS | S-01, S-07, S-08 | one request ≤ 1,000 ms; error and 503 |
| 25 | SkillsToolsPage.trust.test | Unit TS | S-26, S-32 | meanings, warning |
| 26 | SkillsToolsPage.levels.test | Unit TS | S-37, S-39, S-44, S-51 | menu, lock text, notes |
| 27 | SkillsToolsPage.search.test | Unit TS | S-45 to S-50 | |
| 28 | InstallReview.category.test | Unit TS | S-55, S-56, S-27 | Dataset D5 |
| 29 | NewSkillMenu.test | Unit TS | S-52, S-54 | |
| 30 | SkillsToolsPage.states.test | Unit TS | S-66, S-67 | |
| 31 | skills-tools.spec.ts | E2E | S-03, S-15, S-20, S-57, S-58, S-60 to S-65, S-68, S-69 | targeted spec file |
| 32 | Rider tests (one each) | mixed | S-75 to S-81 | per rider |
| 33 | TestSkillAuthority_FoldersFollowOutcome | Integration Go | S-85, S-86, S-87 | E6/E7 app-level and kernel grant lists |
| 34 | TestSkillConsent_Table | Unit Go | S-10, S-11, S-30, S-31, S-93 | Dataset D10 |
| 35 | TestInstallReview_StageBindsRevision | Integration Go | S-94, S-95 | C-13 |
| 36 | TestFullSkills_SentinelAndBudget | Integration Go (scripted provider, request capture) | S-42, S-89, S-90 | Dataset D9 |
| 37 | TestVisibilityTransitions | Integration Go | S-91, S-92 | Dataset D11 |
| 38 | TestPermissionsPatch_PerKey | Integration Go | S-82, S-99 | Dataset D6 |
| 39 | TestMcpMigration_CoverageMatrix | Unit Go | S-88 | Dataset D8 |
| 40 | TestProjectSkillKeys | Unit Go | S-102 | C-19 |
| 41 | TestToolSearch_FindsSkills | Integration Go | S-96 | C-17 |
| 42 | TestUpdateAgentToolsCfg_PreservesSkillPolicies; TestCreateAgent_SkillPolicyChangesParity | Integration Go | S-97, S-103 | C-21 |
| 43 | TestWriteSkillWorkflow_PublishesCategorisedSkill; TestEditSkill_SetCategoryOnce | Integration Go | S-98, S-104 | C-21 |
| 44 | SkillsToolsPage.saveStates.test | Unit TS | S-83 | Dataset D12 |
| 45 | SkillsToolsPage.focus.test | Unit TS | S-100 | |
| 46 | LegacySkillTrust.compat.test | Integration Go | S-101 | C-22 |

**Positive controls (MAJ-014).** Each of tests 33, 34, 36, 37, 38 and the migration tests ships with a recorded mutation probe run by CHECK: remove the outcome filter from `LoadableFolders`, skip the trust floor, inject Full unconditionally, keep the old tier lists, write the full map instead of the named keys, drop one audit call, reset one migrated value. Each probe must turn its test red. Expected values come from this spec and from literal pre-upgrade fixtures, never from calling the new resolver or builder.

**Execution checklist.** Targeted Go tests with `heavy --local` only when needed; `npm run typecheck` (never bare `tsc`); the design-system gate and the full suites run in CI at the cut, which is the authority.

### Test Datasets

#### Dataset D1: Effective skill permission (global × agent × trust × verified × God Mode)

Oracle: C-0, C-3 (strictest wins; trust floor only for unverified; God Mode floors the global skill value to Allow but not trust).

| # | Input (global, agent override, trust, verified, God Mode) | Boundary Type | Expected Output (effective) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | allow, none, ask, yes, off | trust ignored for verified | allow | BDD Scenario: S-13 | |
| 2 | allow, deny, ask, yes, off | agent stricter | deny | BDD Scenario: S-09 | |
| 3 | ask, allow, ask, yes, off | agent looser | ask | BDD Scenario: S-19 | stored allow, floored |
| 4 | deny, none, allow, yes, off | global deny | deny | BDD Scenario: S-19 | |
| 5 | allow, none, deny, no, off | trust deny | deny | BDD Scenario: S-29 | |
| 6 | allow, none, ask, no, off | trust ask | ask | BDD Scenario: S-30 | |
| 7 | allow, none, allow, no, off | trust allow | allow | BDD Scenario: S-32 | |
| 8 | ask, none, allow, no, on | God Mode floors global | allow | BDD Scenario: S-31 | |
| 9 | allow, none, ask, no, on | trust survives God Mode | ask (trust card) | BDD Scenario: S-31 | [pending ST-Q2] |
| 10 | allow, ask, ask, yes, on | per-agent survives God Mode | ask | BDD Scenario: S-19 | as for tools |
| 11 | allow, none, deny, yes (built-in), off | built-in verified | allow | BDD Scenario: S-35 | |
| 12 | deny, allow, allow, yes, on | God Mode floors global deny | allow | BDD Scenario: S-76 | same rule as tools today |
| 13 | missing, none, ask, yes, off | no entry either side | deny + Error log | BDD Scenario: S-17 | tripwire, never after self-heal |
| 14 | allow, none, ask, yes, not granted | grant first | refused: not granted | BDD Scenario: S-14 | |
| 15 | upgrade: Skill global ask, agent none | upgrade | skill global ask | BDD Scenario: S-16 | |
| 16 | upgrade: Skill global allow, agent deny | upgrade | skill global allow, agent deny (granted skills) | BDD Scenario: S-16 | |

#### Dataset D2: Tool level → what the agent is sent (new chat, nothing loaded, unless stated)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | full, allowed | top level | definition in tool list | BDD Scenario: S-38 | |
| 2 | name_and_description, description 300 chars | long text | one line, description cut to 140 characters | BDD Scenario: S-38 | C-1 |
| 3 | name_and_description, empty description | empty | line with the name only | BDD Scenario: S-38 | |
| 4 | name_only | middle | name only, no description | BDD Scenario: S-38 | |
| 5 | hidden, ToolSearch by keyword | bottom | not listed; found and loaded | BDD Scenario: S-38 | |
| 6 | full, effective deny | deny wins | not listed, not found | BDD Scenario: S-40 | |
| 7 | old install with Load tools on demand off | migrated | static tools Full, definition in tool list | BDD Scenario: S-44 | [pending ST-Q4]; under A: hidden + switch off → definition in tool list |
| 8 | `browser_evaluate` loaded in a running chat, then set Hidden | level change mid-chat | still loaded in that chat; not listed in a new chat | BDD Scenario: S-91 | C-15 |
| 9 | `read_file` (old Full) set Hidden | demoted built-in | not listed; ToolSearch "read file" finds it | BDD Scenario: S-92 | C-15 |
| 10 | locked tool, PATCH hidden | locked | 400, nothing saved | BDD Scenario: S-39 | |

#### Dataset D3: Skill trust values

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | stored `block_unverified` | legacy | deny | BDD Scenario: S-33 | |
| 2 | stored `warn_unverified` | legacy | ask | BDD Scenario: S-33 | |
| 3 | stored `allow_all` | legacy | allow | BDD Scenario: S-33 | |
| 4 | stored empty | empty | ask | BDD Scenario: S-33 | |
| 5 | PATCH `skill_trust: deny` | valid | 200, deny | BDD Scenario: S-27 | |
| 6 | PATCH `skill_trust: warn_unverified` | legacy on new endpoint | 400 "Use allow, ask or deny." | BDD Scenario: S-36 | |
| 7 | PATCH `skill_trust: Deny` | wrong case | 400 | BDD Scenario: S-36 | case-sensitive as today |
| 8 | PATCH `skill_trust: allow` | warning | 200; page shows the warning callout | BDD Scenario: S-32 | |
| 9 | legacy `PUT /security/skill-trust` `warn_unverified` | old Settings screen | 200; stored `ask`; GET legacy returns `warn_unverified` | BDD Scenario: S-101 | C-22 |
| 10 | stored value already `ask`, start twice | repeat migration | no rewrite, no audit entry | BDD Scenario: S-33 | idempotent |

#### Dataset D4: Set all scope and count

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | no search, no filter, group of 12 | whole group | 12 change, label "Set all 12" | BDD Scenario: S-02 | |
| 2 | Permission filter Deny, 1 match of 53 | filter | 1 changes, "Set 1 matching" | BDD Scenario: S-48 | |
| 3 | search "zzzz", 0 matches | empty | control disabled | BDD Scenario: S-49 | |
| 4 | group with Tool search, Set all Deny | fixed row | all but Tool search change | BDD Scenario: S-06 | |
| 5 | group with execute_plan, Set all Allow | confirm | confirm first; Cancel → 0 change | BDD Scenario: S-05 | |
| 6 | level Set all, group with a locked tool | locked row | locked row unchanged | BDD Scenario: S-39 | |
| 7 | group with `set_channel_outbound`, Set all Allow, 12 rows | always-ask row | 11 change; label "Set 11 of 12 to Allow"; note names the skipped row | BDD Scenario: S-84 | C-20 |
| 8 | Omnipus agent tab, Set all Ask | Omnipus rule | control disabled with the Omnipus text; 0 change | BDD Scenario: S-22 | C-20 |
| 9 | two Set all clicks 100 ms apart | race | 2 requests, in order; final rows = second response's stored maps | BDD Scenario: S-82 | C-16 |
| 10 | filter Permission = Ask, Set all Deny empties the group | focused last result | focus on group header; status announces "0 shown" | BDD Scenario: S-100 | |

#### Dataset D5: Install category

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | screen, no choice | missing | "Choose a category.", nothing installed | BDD Scenario: S-55 | |
| 2 | file `metadata.category: research` | from file | Research preselected | BDD Scenario: S-56 | |
| 3 | file `metadata.category: Games` | not in list | no preselection; person must choose | BDD Scenario: S-56 | |
| 4 | REST without category, no metadata | absent | uncategorised | BDD Scenario: S-57 | C-5 |
| 5 | `install_skill` `category: games` | invalid | refused naming allowed values | BDD Scenario: S-72 | |
| 6 | `install_skill` `category: lifestyle` | valid | Lifestyle | BDD Scenario: S-71 | |

#### Dataset D6: `PATCH /security/permissions` validation

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `skill_policies: {plan: deny}` | valid | 200; only `plan` changed; 1 audit entry | BDD Scenario: S-09 | |
| 2 | `skill_policies: {nope: deny}` | unknown key | 400, nothing saved | BDD Scenario: S-07 | |
| 3 | `skill_policies: {}` with `policies: {bash: ask}` | empty member | 200, only `bash` changed | BDD Scenario: S-82 | |
| 4 | `{}` | nothing | 400 "Nothing to change." | BDD Scenario: S-07 | |
| 5 | `visibility: {AskUserQuestion: hidden}` | locked | 400 lock text | BDD Scenario: S-39 | |
| 6 | `policies: {ToolSearch: deny}` | fixed | 400 "has no permission of its own" | BDD Scenario: S-06 | |
| 7 | `skill_visibility: {plan: full}` | valid | 200 | BDD Scenario: S-42 | |
| 8 | `skill_policies: {PLAN: ask}` | letter case | 200, applies to `plan` | BDD Scenario: S-09 | |
| 9 | `skill_policies: {plan: ask, PLAN: deny}` | duplicate | 400 "`plan` is listed twice.", nothing saved | BDD Scenario: S-07 | |
| 10 | `skill_policies: {plan: null}` | null | 400 naming `plan` | BDD Scenario: S-07 | |
| 11 | `{plan: deny}` valid + `{nope: deny}` invalid in one request | atomic | 400, `plan` unchanged | BDD Scenario: S-07 | |
| 12 | `policies: {set_channel_outbound: allow}` | always-ask | 400 | BDD Scenario: S-84 | |
| 13 | tab A `policies: {send_email: deny}`, then tab B `skill_policies: {plan: ask}` | different keys | both stored | BDD Scenario: S-82 | CRIT-003 |
| 14 | tab A `policies: {bash: deny}`, tab B `policies: {bash: allow}` | same key | stored `allow` (later); tab A shows `allow` after its next response or focus | BDD Scenario: S-82 | |
| 15 | `skill_policies: {plan: deny}` when stored `deny` | no-op | 200; no audit entry | BDD Scenario: S-99 | |
| 16 | `skill_policies: {"project:w1/notes": ask}` | project key | 200 | BDD Scenario: S-102 | C-19 |

#### Dataset D7: Starting levels at upgrade (rewritten in round 1, MAJ-004)

Old behaviour comes from `pkg/tools/manifest.go::ToolManifestTier`, `::ToolManifestVisibility`, `pkg/agent/loop_mcp.go::serverIsDeferred` and `pkg/agent/tool_manifest.go::buildCompressedToolDefs` at `4e34afa416`. Each row also needs a literal captured request from the old build as its fixture (ST-06); these levels are the spec's oracle, the captures check them.

| # | Input (compressed, discovery, deferred, preview_all_lazy, tool) | Boundary Type | Expected Output (level) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | on, –, –, off, `read_file` (in the 36) | full list | full | BDD Scenario: S-41 | |
| 2 | on, –, –, off, `serve_web` | previewed | name_and_description | BDD Scenario: S-41 | |
| 3 | on, –, –, off, `browser_evaluate` | search-only | hidden | BDD Scenario: S-41 | |
| 4 | on, –, –, on, `browser_evaluate` | old switch | name_and_description | BDD Scenario: S-41 | |
| 5 | on, on, unset, off, MCP tool | deferred server | hidden (server key) | BDD Scenario: S-41 | |
| 6 | on, on, `false`, off, MCP tool | registered visible but lazy | hidden (server key) | BDD Scenario: S-41 | round 0 said full: wrong |
| 7 | on, on, `false`, on, MCP tool | visible + preview | name_and_description (server key) | BDD Scenario: S-41 | |
| 8 | on, on, unset, on, MCP tool | registered hidden | hidden (server key) | BDD Scenario: S-41 | Inferred from `RegisterMCP` / `GetAll`: hidden-registered tools never reach the preview; the old-build capture is the authority |
| 9 | off, –, –, any, any static tool | uncompressed | full | BDD Scenario: S-44 | [pending ST-Q4] |
| 10 | off, on, unset or `true`, any, MCP tool | effectively deferred | hidden (server key) | BDD Scenario: S-41 | |
| 11 | off, off, `true`, any, MCP tool | discovery off | full (server key) | BDD Scenario: S-41 | `serverIsDeferred` returns false when discovery is off |
| 12 | off, on, `false`, any, MCP tool | not deferred | full (server key) | BDD Scenario: S-41 | |
| 13 | server offline at upgrade, rows 5–12 | offline | server key written from its stored config; its tools get it when they appear | BDD Scenario: S-41 | |
| 14 | every skill | skills | name_and_description | BDD Scenario: S-41 | |
| 15 | any row, second start | repeat | nothing written | BDD Scenario: S-41 | idempotent |

#### Dataset D8: External tools migration (C-18) [pending ST-Q5]

| # | Input (global map, Mia's entry for `mcp_github_read`, God Mode) | Boundary Type | Expected Output (written; Mia after) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `mcp_github_*: allow`, none, off | covered exact server key | nothing; allow | BDD Scenario: S-88 | |
| 2 | `mcp_*: deny`, none, off | covered by broader key | nothing; deny | BDD Scenario: S-88 | round-0 rule would have made it ask |
| 3 | `mcp_github_read: deny` only, none, off | partly covered | `mcp_github_*: ask`; deny (exact wins) | BDD Scenario: S-88 | other github tools: ask |
| 4 | no match, none, off | uncovered | `mcp_github_*: ask`; ask | BDD Scenario: S-88 | named change deny → ask |
| 5 | no match, `allow`, off | agent-only | `mcp_github_*: ask`; ask | BDD Scenario: S-88 | named change allow → ask |
| 6 | no match, `deny`, off | agent-only | `mcp_github_*: ask`; deny | BDD Scenario: S-88 | |
| 7 | no match, `allow`, on | God Mode | `mcp_github_*: ask`; allow | BDD Scenario: S-88 | |
| 8 | `mcp_git_*: deny`, none, off; service `git_hub` | prefix overlap | nothing for `git_hub`; deny | BDD Scenario: S-88 | probe matches the shorter key |
| 9 | row 4, second start | repeat | nothing written | BDD Scenario: S-88 | |
| 10 | new service after upgrade, no match | new | `mcp_<s>_*: ask` | BDD Scenario: S-59 | R30b |
| 11 | new service after upgrade, `mcp_*: deny` | new, covered | nothing; rows show deny | BDD Scenario: S-59 | |

#### Dataset D9: Full skills, consent and budget (C-14) [pending ST-Q1]

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Full, Allow, verified, chat | allowed | sentinel in first request | BDD Scenario: S-42 | |
| 2 | Full, Ask, verified, chat, not approved | ask | sentinel absent; listed with description | BDD Scenario: S-89 | |
| 3 | Full, Allow, unverified, trust Ask, no approval | trust ask | sentinel absent | BDD Scenario: S-89 | |
| 4 | row 3 after "Approve this version" | approved | sentinel present from next turn | BDD Scenario: S-89 | |
| 5 | Full, Ask, unattended run | unattended | sentinel absent; load refused | BDD Scenario: S-89 | |
| 6 | Full, Deny | deny | not listed, sentinel absent | BDD Scenario: S-89 | |
| 7 | Full total exactly fits (context 8,000, pinned+Full+reserve = 8,000) | just fits | request sent | BDD Scenario: S-90 | |
| 8 | one token over | one over | not sent; C-14 text | BDD Scenario: S-90 | |
| 9 | fits on model A, switch to smaller model B | model switch | next turn not sent; C-14 text | BDD Scenario: S-90 | |
| 10 | a Full skill grows past the limit between turns | content growth | next turn not sent | BDD Scenario: S-90 | |

#### Dataset D10: Consent (C-12)

| # | Input (kind, Auto, God, remembered, revision) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | ordinary Ask, off, off, none | card | approval card with Always Allow | BDD Scenario: S-10 | |
| 2 | ordinary Ask, on, off, none | Auto | loads, no card | BDD Scenario: S-10 | `Skill` is AutoRuns |
| 3 | ordinary Ask, off, off, Always Allow same revision | remembered | loads, no card | BDD Scenario: S-11 | |
| 4 | ordinary Ask, off, off, Always Allow older revision | changed | card again | BDD Scenario: S-11 | |
| 5 | trust Ask, on, on, none | trust | trust card, no Always Allow | BDD Scenario: S-31 | [pending ST-Q2] |
| 6 | trust Ask, off, off, approved same revision | approved | loads | BDD Scenario: S-93 | |
| 7 | trust Ask, off, off, approved older revision | changed | trust card | BDD Scenario: S-93 | |
| 8 | trust Ask, person's `/name` | slash | trust card first | BDD Scenario: S-93 | |
| 9 | ordinary Ask, person's `/name` | slash | loads, no card | BDD Scenario: S-13 | |
| 10 | any Ask, unattended | unattended | refused, unattended text | BDD Scenario: S-12 | |
| 11 | card open, then permission set to Deny, then Approve | stale | not loaded; refused naming the setting | BDD Scenario: S-93 | |

#### Dataset D11: Visibility transitions (C-15)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | loaded tool, level → hidden | demote loaded | stays in that chat; not in new chats | BDD Scenario: S-91 | |
| 2 | `read_file` full → hidden | demote built-in | searchable | BDD Scenario: S-92 | |
| 3 | hidden → full | promote | definition from next turn | BDD Scenario: S-38 | |
| 4 | loaded tool, permission → deny | revoke | gone from next turn, also in that chat | BDD Scenario: S-40 | |
| 5 | deny → allow | re-allow | listed per level from next turn | BDD Scenario: S-40 | |
| 6 | skill menu frozen, skill level → name_only | menu cache | next turn's menu shows name only | BDD Scenario: S-43 | |

#### Dataset D12: Save states (C-16)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 200 | saved | "Saved" | BDD Scenario: S-01 | |
| 2 | 503 | saved, not applied | saved-not-applied text + Retry | BDD Scenario: S-08 | |
| 3 | 400 | refused | "Not saved: …" + stored value | BDD Scenario: S-07 | |
| 4 | response dropped | unknown | "Couldn't confirm…" then stored value | BDD Scenario: S-83 | |
| 5 | two clicks, first response arrives after second request starts | ordering | "Saved" shown for each in order; never early | BDD Scenario: S-82 | |
| 6 | failure after navigating away | late failure | toast with "Open Skills & Tools" | BDD Scenario: S-83 | |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Tool strictest-wins merge, God Mode floor, always-ask | compositor tests (`pkg/tools`) | No — must stay green unchanged | skills reuse the merge |
| Omnipus per-agent Ask refused | `rest_omnipus_guards` tests | Yes — skills variant (S-22) | |
| execute_plan confirm in the editor | `ToolPolicyEditor.test.tsx` | Yes — through Set all (S-05) | |
| Grant list is the gate | `TestSkillTool_LoadDeniedAndNotFoundAreDistinct` (ADR-072) | No — must stay green | |
| Global PUT 503 text | tool-policies handler tests | No — must stay green | |
| Presets | `toolPolicyPresets.test.ts`, preset cases in `ToolPolicyEditor.test.tsx` | Deleted with the feature (T-Q6 A) | the deletion is the expected change, not a weakened test |

#### Regression Dataset: tool behaviour unchanged by this package

| # | Input | Previous Behaviour | Must Still Produce | Traces to |
|---|---|---|---|---|
| 1 | global `bash` ask, agent none | ask | ask | Regression: tool merge |
| 2 | global allow, agent deny | deny | deny | Regression: tool merge |
| 3 | God Mode, `set_channel_outbound` | ask | ask | Regression: always-ask |
| 4 | Omnipus per-agent `bash: ask` | 422 | 422 | Regression: Omnipus guard |
| 5 | MCP tool with `mcp_github_*: deny` | deny | deny | Regression: wildcard |
| 6 | ungranted skill load | refused | refused | Regression: grant gate |
| 7 | `PUT /security/tool-policies` full map from today's Settings screen | replaces map | replaces map (plus per-key audit) | Regression: legacy endpoint |
| 8 | ungranted skill folder read by a file tool | refused | refused | Regression: skill authority |
| 9 | agent with `edit_skill` allowed reads any skill folder | allowed | allowed (editor exception) | Regression: editor read |
| 10 | `PUT /agents/{id}` with `tools_cfg.mcp` only | builtin policies kept | builtin and skill policies kept | Regression: update preservation |

---

## Functional Requirements

### Page, search and saving (US-1, US-6)
- **FR-001**: The Skills & Tools page MUST show three tabs with counts — Skills, External tools (MCP), Omnipus tools — opening on Skills.
- **FR-002**: Each tab MUST show one search field and two flat filters (Permission; What the model sees) on one line, above one grouped list whose groups start collapsed; a search MUST open groups with matches; Escape and the clear button MUST empty it.
- **FR-003**: A group's Set all MUST change exactly the rows on screen (search and both filters applied) that can take the chosen value (C-20), in one request, with "<k> of <n>" in its label and accessible name, and MUST name the skipped rows and why.
- **FR-004**: An empty result MUST show "No <items> match “<text>”" with "Clear search and filters".
- **FR-005**: A row whose saved value no longer matches the active filter MUST leave the list.
- **FR-006**: With a "What the model sees" filter on, the page MUST show the denied-items note.
- **FR-007**: Every value on this page (permission, level, trust, a service's switch) MUST start saving on its own within 1,000 ms of the click and show "Saved" only when that change is acknowledged (C-16); the page MUST NOT show a Save bar, Discard, a leave prompt or a Set all confirm, other than FR-010's warning [pending ST-Q7].
- **FR-008**: A refused save MUST show "Not saved: <server reason>." with Retry, and the rows MUST show the stored values.
- **FR-009**: A 503 save MUST show "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." with Retry.
- **FR-010**: Any change, single or Set all, that would make `execute_plan` resolve to Allow MUST open the existing autonomous-plan confirm first; Cancel changes no row of that action [pending ST-Q7; option B deletes this FR and S-05, ST-03].
- **FR-011**: Every tool row MUST show exactly one stored value; "Unset" MUST NOT appear.
- **FR-012**: Tool search and Skill MUST show no permission badges (C-6 `permission_fixed`), with their fixed texts; the server MUST refuse any value but `allow` for them.

### Per-skill permissions (US-2)
- **FR-013**: The server MUST store one global permission per skill key (C-19) and serve and change it through `GET/PATCH /security/permissions` (C-2), changing only the keys a request names.
- **FR-014**: A skill load MUST resolve to the strictest of the global skill value, the agent's own value and, for an unverified skill, the trust value; God Mode MUST floor only the global skill value (C-0, C-3; Dataset D1).
- **FR-015**: The grant check MUST run before the permission check and keep its current refusal.
- **FR-016**: An ordinary Ask load in an interactive chain MUST follow C-12: the normal approval card with the C-9 `skill` object (unless Auto-approve runs it); Always Allow MUST remember it for the chat and the skill's current revision only.
- **FR-017**: An Ask load in an unattended chain MUST be refused with "Not run: needs approval, and task and plan runs never ask." and no card.
- **FR-018**: A skill whose effective value is Deny for an agent MUST NOT appear in that agent's skill menu or `Skill` search results.
- **FR-019**: The first start on this release MUST run the C-19 upgrade order once and idempotently: every installed and project skill's global value = the old global `Skill` value; each agent's own old `Skill` value copied to each skill granted to that agent and each project skill on its mounts; then the old `Skill` entries are removed and `Skill` is fixed at `allow`.
- **FR-020**: A skill key with no stored global value or level MUST get Allow [pending ST-Q3] and `name_and_description`, additively, never overwriting a stored value.
- **FR-021**: The server MUST store sparse per-agent skill overrides and accept them through `AgentToolsUpdateRequest.skills` (C-4), reporting `AgentToolsResponse.skills`.

### Agent Tools tab (US-3)
- **FR-022**: No agent's Tools tab and no other screen MUST show Cautious / Balanced / Full access; the presets module MUST be removed.
- **FR-023**: In an agent's Tools tab, badges looser than the global value MUST be disabled with the visible reason "Global setting is <value>; an agent can only be stricter."
- **FR-024**: An agent's Tools tab MUST show a Skills group of its granted skills with Allow / Ask / Deny and Inherit, or "No skills granted. Grant skills on this agent's Skills tab."
- **FR-025**: A per-agent Ask on a skill for Omnipus MUST be refused with the existing Omnipus text and code (422).
- **FR-026**: Judge and Plan Supervisor MUST stay read-only and external workers MUST keep their notice, for skills as for tools.

### Skill trust (US-4)
- **FR-027**: Trust MUST be stored as `allow` / `ask` / `deny` and served that way by `PermissionSettings.skill_trust` (C-2); legacy stored values MUST be rewritten once on load (Dataset D3) with one audit entry; `PATCH` MUST refuse other values with 400 "Use allow, ask or deny."; the legacy `/security/skill-trust` endpoint MUST keep its enum, mapped, until the Settings package retires it (C-22).
- **FR-028**: The Skills tab MUST open with the "Unverified skills" block: Allow, Ask, Deny badges in that order, all three meanings visible, and the Allow warning callout fully visible after choosing Allow, also at 390 px.
- **FR-029**: With trust Deny, every install path (screen, `POST /skills/install`, `install_skill`) MUST refuse an unverified skill with the C-3 text and leave nothing installed; the install review MUST disable Install with its own text. With trust Ask, an unverified install MUST need a person's review of that exact revision (C-12, C-13); `POST /skills/install` without one → 409.
- **FR-030**: With trust Ask, an unverified skill's load MUST need a trust approval of its current revision per C-12, including under Auto-approve and God Mode and never satisfied by a remembered Always Allow [pending ST-Q2]; with trust Deny it MUST be refused with the C-3 load text.
- **FR-031**: `verified` MUST be true only for built-in skills and for registry installs whose hash matched and whose folder has no local changes since (C-5); the install result MUST be stored outside the skill folder.
- **FR-032**: Every trust change MUST keep its existing audit entry with old and new value.

### What the model sees (US-5)
- **FR-033**: Every tool and skill row MUST offer Hidden, Name only, Name and short description, Full, with a one-line explanation per type (C-1).
- **FR-034**: At the start of a turn, each allowed tool MUST be presented per its level (C-1; Dataset D2).
- **FR-035**: Tool search, Skill, Ask the user, goal_claim and set_goal MUST have fixed levels with the visible lock reason; the server MUST refuse a change (400).
- **FR-036**: An item with effective Deny for an agent MUST NOT be listed or found for that agent, whatever its level.
- **FR-037**: The first start on this release MUST set starting levels per C-6 and Dataset D7, including per-server levels for external services, so that each agent's first-turn request is unchanged apart from SC-003's named changes; after migration `tools.manifest.compressed`, `preview_all_lazy` and `MCPServerConfig.deferred` MUST no longer be read, with one load warning if present [pending ST-Q4].
- **FR-038**: Each granted, not-denied skill MUST be presented per its level (C-1); a Full skill's instructions MUST be injected only when its outcome is `allowed` for that agent and chain, and MUST be charged to the request budget, with the C-14 refusal when they do not fit [pending ST-Q1].
- **FR-039**: The four levels MUST be the only control of what is sent about tools; `set_config` MUST refuse the retired manifest keys (C-21) [pending ST-Q4; under A: with the switch off both tool tabs MUST show the C-6 note].
- **FR-040**: Every change of a tool permission, a skill permission, a tool level, a skill level or trust — through `PATCH /security/permissions` or the legacy PUT endpoints — MUST write one audit entry per changed key with actor, old and new value, and none for unchanged keys or refused requests (C-2).

### Adding skills (US-7)
- **FR-041**: New skill MUST offer "Write with Omnipus", "Upload a skill", "Browse the catalog"; "Write with Omnipus" MUST open the Omnipus chat and send "Write a new skill" with `workflow_start {workflow: W5}`; offline it MUST send nothing and show the C-8-pattern warning.
- **FR-042**: Every install (catalog and file) MUST pass a review of the staged files (C-13) showing name, version, verified or not, the trust value, and a required Category; `metadata.category` MUST preselect when it is a valid value; Install MUST publish exactly the reviewed revision.
- **FR-043**: Each skill's category MUST be stored outside its folder (C-5) and the Skills tab MUST group by category, hiding empty ones, with Uncategorised for the rest.
- **FR-044**: Each skill row MUST offer More actions: Make a copy; Remove only when not built-in; "Choose category" only while Uncategorised; "Approve this version" only for an unverified skill under trust Ask.

### External services (US-8)
- **FR-045**: Adding a service (screen or `add_mcp_server`) MUST store Ask for all its current and future tools when no global key covers it (C-7); the one-time upgrade MUST follow C-18 exactly, writing only global server keys [pending ST-Q5].
- **FR-046**: Each service group header MUST carry the switch (acts at once), Test, Edit, Remove; a failing service MUST show its reason with Retry and Edit.
- **FR-047**: Remove MUST confirm with "Remove this service? Agents that use it lose its tools."; the local-program confirm MUST keep the safe choice as the main, focused button.

### Tool settings and links (US-9)
- **FR-048**: The Web & Search group MUST have a Settings button opening the existing search-provider settings in a side panel, behaviour unchanged.
- **FR-049**: The Shell group and the Omnipus tools intro MUST link to the Settings addresses that hold shell rules and Auto-approve / God Mode at this landing, with text that names no section that does not exist yet (C-22); no link MUST point to Settings for tool permissions.

### States, phone, keyboard, wording (US-10)
- **FR-050**: Each tab MUST show the loading, empty, error and partial states in "UI Screens and States"; no write MUST be possible while a tab is loading or failed.
- **FR-051**: At 390 × 844 every control MUST be at least 44 × 44 px and the page MUST NOT scroll sideways; rows MUST become cards.
- **FR-052**: Keyboard and screen-reader behaviour MUST follow "Accessibility and Keyboard", including the focus rules for rows that leave a filtered list, using the catalogued `tabs` component for the three tabs.
- **FR-053**: No on-screen text MUST name a retired agent, an issue number, a requirement tag or "contact support".

### Agent tool parity (US-11)
- **FR-054**: `set_agent_permissions` MUST accept `skill_policy_changes` (`set`/`remove`) and report clamps like tool changes.
- **FR-055**: `get_agent` MUST return `skill_permissions` for `fields: ["skill_permissions"]`.
- **FR-056**: `install_skill` and `edit_skill` `copy` MUST accept an optional `category` from `SkillCategory` and refuse other values naming the allowed ones; `edit_skill` `set_category` MUST work once, only while the skill is Uncategorised (C-21).
- **FR-057**: `set_config` MUST refuse `sandbox.skill_policies`, `sandbox.tool_visibility` and `sandbox.skill_visibility` with the existing sandbox reason, and the retired manifest keys with "This setting was replaced by levels in Skills & Tools." (C-21).

### Riders (US-12)
- **FR-058**: (#1920) No built-in `SKILL.md` MUST name Ava or Admin as an agent; the retired-agent guard SHOULD also scan embedded skill text.
- **FR-059**: (#1297) Effective values shown in the agent's Tools tab and returned by `get_agent` MUST include God Mode.
- **FR-060**: (#1298) An agent's Tools tab MUST list the tools of services assigned to it.
- **FR-061**: (#1411) Reopening an agent's editor MUST show the saved tool values without a page reload.
- **FR-062**: (#99) `omnipus doctor` MUST warn when trust is Allow.
- **FR-063**: (#1570) One unreadable skill MUST NOT fail the whole skill list; it shows "Cannot load" with its reason.
- **FR-064**: (#1576) A copy MUST show its own name, not the source's display name.

### Round-1 additions
- **FR-066**: Every entry point E1–E9 of C-11 MUST use the single per-skill outcome; a skill whose outcome is `deny` or `ask_pending` MUST NOT be loaded, injected, found by search (`deny` only), readable by file tools or runnable as a helper for that agent, in the app-level guard and in the kernel grant alike; the editor read exception MUST stay read-only and separate.
- **FR-067**: Consent MUST follow C-12: ordinary Ask uses the existing card, Auto rule and session grants bound to the skill revision; the trust check [pending ST-Q2] uses installation-wide approvals of one revision, offers no Always Allow, and a pending answer MUST be re-checked against the current outcome before use.
- **FR-068**: Installs MUST go through the C-13 review: stage without publishing, return verified facts and the revision, publish exactly that revision, expire after 15 minutes, and report Cancel and lost answers truthfully.
- **FR-069**: When an agent's Full skills do not fit its model, the turn MUST NOT be sent and the chat MUST show the C-14 text; nothing is trimmed silently [pending ST-Q1].
- **FR-070**: A level change MUST apply from the next turn without evicting loaded definitions; a permission Deny MUST remove the item from the next turn; manifest, search membership and skill menu MUST read one visibility authority, and the skill menu MUST rebuild when an outcome or level changes (C-15).
- **FR-071**: The page MUST implement the C-16 save states, one request per click in click order, an app-level queue that survives in-app navigation, a toast for late failures, and a re-read for unknown outcomes.
- **FR-072**: `PATCH /security/permissions` MUST change only named keys, validate the whole request first, and follow every rule of C-2; `GET/PUT /security/tool-policies` MUST keep working for today's Settings screen.
- **FR-073**: ToolSearch MUST also return the skills the agent may use, labelled `[skill]`, without loading them (C-17) [pending ST-Q6].
- **FR-074**: The external-tool migration MUST follow C-18 and Dataset D8, decide coverage with the resolver's own matching, write only global server keys, and run once.
- **FR-075**: Project skills MUST have their own keys and lifecycle (C-19); the upgrade MUST run in the C-19 order and be idempotent and restart-safe.
- **FR-076**: Bulk changes MUST follow C-20: only rows that can take the value change; the label counts them; skipped rows are named.
- **FR-077**: Every write path of C-21 MUST carry the agent's skill permissions and the skill category: `create_agent` `skill_policy_changes`; preservation in `updateToolsConfig`; `edit_skill` `set_category` and copy `category`; `PUT /skills/{name}/category` once; the write-skill workflow sets the category.
- **FR-078**: At this landing, today's Settings trust control and tool grid MUST keep working and links MUST resolve (C-22).
- **FR-079**: Each Full skill row MUST show its approximate size, and the Skills tab MUST warn when an agent's Full total exceeds half of its model's context [pending ST-Q1].
- **FR-080**: `GET /skills` and agent turns MUST create missing project-skill keys at first sight and remove them with their workspace or folder (C-19).

### Dependency
- **FR-065**: This package MUST land before the Settings package is cut (Settings FR-060); at its landing the route MUST render the tool permission grid, web search settings and skill trust.

## Success Criteria

- **SC-001**: All 104 BDD scenarios pass in RED → GREEN, with the RED logs saved, except scenarios removed by a founder answer to ST-Q1 to ST-Q7.
- **SC-002**: Hands-on rows ST-01 to ST-24 PASS on the cut commit, validated independently.
- **SC-003**: After upgrading a saved pre-release data folder, the request each of 3 sample agents sends on its first turn, captured with a scripted model, is identical tool by tool and skill by skill to the request captured before the upgrade (ST-06), except these named changes: trust Warn becomes Ask (unverified skills now ask; C-3); external tools with no global coverage go from Deny or agent-only Allow to Ask (C-18) [pending ST-Q5]; project skills now count as unverified (C-19).
- **SC-004**: `grep -r "Cautious\|toolPolicyPresets" src/` returns 0 matches in production code.
- **SC-005**: Every install path refuses an unverified skill under trust Deny: screen, agent tool and direct REST, each driven and asserted separately (3 of 3, ST-09 a–c); a direct REST install under trust Ask without a review is refused (ST-09 d).
- **SC-006**: With a skill Denied (or Ask not approved) for an agent, 0 of 5 paths give that agent its instructions or files: `Skill`, menu/search, Full, file read, helper run (ST-19, ST-13).
- **SC-007**: Two tabs changing different keys keep both changes in 10 of 10 repetitions (ST-20).

---

## Reachability

- **Tool registration**: no new tool. Changed tools: `set_agent_permissions` (`skill_policy_changes`), `create_agent` (`skill_policy_changes`), `get_agent` (`skill_permissions`), `install_skill` (`category`, stage-then-publish, trust card), `edit_skill` (`copy` `category`, `set_category`), `add_mcp_server` (Ask key only when uncovered), `set_config` (refusals), `ToolSearch` (finds skills [pending ST-Q6]). Their policy entries already exist in `pkg/config/defaults.go` and the agent seeds (`pkg/coreagent/seed.go`); `grep -rl '"set_agent_permissions"' pkg/coreagent/ pkg/config/ pkg/tools/` must stay non-zero. The `Skill` tool's global value becomes fixed `allow`; per-skill values live in `sandbox.skill_policies` (global) and each agent's `tools_cfg.skills.policies` (per agent).
- **Screen**: `/skills` route (`src/routes/_app/skills.tsx`), sidebar "Skills & Tools" — the rewritten Skills & Tools screen, writing through `GET/PATCH /security/permissions`; the install review (`/skills/install/review`); each agent's profile → Tools tab (`ToolsAndPermissions`); the Omnipus chat for W5 through to a published, categorised skill; the save-failure toast from any screen.
- **Test plan execution**: RED (qa-lead) writes and runs the tests in "Test Implementation Order" against the pre-change code and saves the failing logs; GREEN (backend-lead and frontend-lead) makes them pass; CHECK audits integrity; the hands-on rows run on the cut commit by uat-tester and uat-validator.

## User-facing documentation

| TODO | UI / behaviour change | User-facing page | Matching update |
|---|---|---|---|
| DOC-001 | Global tool permissions move to Skills & Tools; autosave; Set all; no presets; no Unset | `docs/tools.md` | Rewrite "How to change an agent's tool access" (drop preset step 3), the "installation-wide setting" paragraph (now Skills & Tools → Omnipus tools / External tools), "Tools from connected servers" (new tab names, Add service, tools start at Ask) |
| DOC-002 | Per-skill Allow / Ask / Deny; Skill tool no longer one switch; grants stay | `docs/skills.md` | New section "Allow, ask or deny a skill"; table row for `Skill` in `docs/tools.md` defaults updated |
| DOC-003 | What the model sees: four levels | `docs/tools.md`, `docs/skills.md` | New section on the levels, what each means for a tool and a skill, locked tools, and that the levels replace "Load tools on demand" [pending ST-Q4] |
| DOC-004 | Skill trust words and enforcement; Warn becomes Ask; upgrade changes | `docs/skills.md` (line with Block/Warn/Allow all), `docs/security.md` | New words; where it applies (install and load, every path of C-11); approving a version; unattended runs refuse; the named upgrade changes of SC-003; project skills count as unverified; a later grant no longer inherits the old blanket `Skill` value; audit-write failures are logged, not blocking (unchanged) |
| DOC-010 | Editor exception; Full size limit; level changes in running chats | `docs/skills.md`, `docs/tools.md` | Agents allowed to edit skills can read denied skills' files; Full skills must fit the model; a level change applies from the next turn and does not unload loaded tools |
| DOC-011 | Install review and categories for written skills | `docs/skills.md` | Review step, expiry, "Choose category" once, write-skill asks for a category |
| DOC-005 | New skill menu; install review with category; categories | `docs/skills.md` "How to add a skill and grant it" | New steps and tab names |
| DOC-006 | Agent Tools tab: Skills group, stricter only, no presets | `docs/agents.md`, `docs/tools.md` | Steps and screenshots text |
| DOC-007 | Agent tools gain skill fields | `docs/tools.md` (get_agent paragraph), `docs/skills.md` | `skill_permissions`, `skill_policy_changes`, `install_skill` category |
| DOC-008 | Web & Search settings live on Skills & Tools | `docs/tools.md` ("Web search works without setup" bullet) | Point to Omnipus tools → Web & Search → Settings |
| DOC-012 | One search finds tools and skills [pending ST-Q6] | `docs/tools.md` (ToolSearch paragraph) | ToolSearch also lists skills, typed, and does not load them |
| DOC-009 | Settings no longer holds tool permissions or trust | `docs/settings.md` | Coordinated with the Settings package DOC-007; this package owns the Skills & Tools side |
| ADR-TODO-1 | Full skill level reverses ADR-072 OQ3 | `docs/internal/architecture/ADR-072-skill-activation-and-loading.md` | Dated correction by the architect after ST-Q1 is answered |
| ADR-TODO-2 | Tier lists become stored levels | `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md` | Dated correction |

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | Documentation TODO(s) |
|---|---|---|---|---|
| FR-001 | US-6 | S-45 | SkillsToolsPage.search.test | DOC-001 |
| FR-002 | US-6 | S-46, S-47 | SkillsToolsPage.search.test | DOC-001 |
| FR-003 | US-1, US-6 | S-02, S-06, S-48 | SkillsToolsPage.setAll.test | DOC-001 |
| FR-004 | US-6 | S-49 | SkillsToolsPage.search.test | DOC-001 |
| FR-005 | US-6 | S-50 | SkillsToolsPage.search.test | DOC-001 |
| FR-006 | US-6 | S-51 | SkillsToolsPage.levels.test | DOC-003 |
| FR-007 | US-1 | S-01, S-02, S-69 | SkillsToolsPage.autosave.test | DOC-001 |
| FR-008 | US-1 | S-07 | SkillsToolsPage.autosave.test | DOC-001 |
| FR-009 | US-1 | S-08 | SkillsToolsPage.autosave.test | DOC-001 |
| FR-010 | US-1 | S-05 | SkillsToolsPage.setAll.test | DOC-001 |
| FR-011 | US-1 | S-04 | TestMcpAdd_WritesAskWildcard | DOC-001 |
| FR-012 | US-1, US-2 | S-06, S-15 | SkillsToolsPage.setAll.test; TestPermissionsPatch_SkillAndVisibilityMembers | DOC-002 |
| FR-013 | US-2 | S-09 | TestPermissionsPatch_SkillAndVisibilityMembers | DOC-002 |
| FR-014 | US-2 | S-09, S-13, S-19, S-31 | TestSkillPolicyResolution_Table | DOC-002 |
| FR-015 | US-2 | S-14 | TestSkillPolicyResolution_Table | DOC-002 |
| FR-016 | US-2 | S-10, S-11 | TestSkillApprovalFrame_CarriesSkill | DOC-002 |
| FR-017 | US-2 | S-12 | TestSkillLoad_AskUnattendedRefused | DOC-002 |
| FR-018 | US-2 | S-09 | TestSkillsSummary_Levels | DOC-002 |
| FR-019 | US-2 | S-16 | TestSkillPolicyUpgrade_FromSkillTool | DOC-002 |
| FR-020 | US-2 | S-17 | TestSkillPolicyReconcile_NewSkillAllow | DOC-002 |
| FR-021 | US-3 | S-20, S-21, S-25 | TestAgentToolsPut_SkillsPolicies | DOC-006 |
| FR-022 | US-3 | S-18 | ToolPolicyEditor.noPresets.test | DOC-001, DOC-006 |
| FR-023 | US-3 | S-19 | ToolPolicyEditor.locks.test | DOC-006 |
| FR-024 | US-3 | S-20, S-21, S-24 | ToolPolicyEditor.locks.test; skills-tools.spec.ts | DOC-006 |
| FR-025 | US-3 | S-22 | TestAgentToolsPut_SkillsPolicies | DOC-006 |
| FR-026 | US-3 | S-23 | TestAgentToolsPut_SkillsPolicies | DOC-006 |
| FR-027 | US-4 | S-33, S-36 | TestSkillTrustMigration_LegacyValues; TestPutSkillTrust_RefusesLegacy | DOC-004 |
| FR-028 | US-4 | S-26, S-32 | SkillsToolsPage.trust.test | DOC-004 |
| FR-029 | US-4 | S-27, S-28 | TestInstall_TrustDenyRefusesUnverified; InstallReview.category.test | DOC-004 |
| FR-030 | US-4 | S-29, S-30, S-31 | TestSkillPolicyResolution_Table | DOC-004 |
| FR-031 | US-4 | S-34, S-35 | TestSkillVerified_LocalChangeUnverifies | DOC-004 |
| FR-032 | US-4 | S-33 | TestSkillTrustMigration_LegacyValues | DOC-004 |
| FR-033 | US-5 | S-37 | SkillsToolsPage.levels.test | DOC-003 |
| FR-034 | US-5 | S-38 | TestManifest_LevelsShapeTurn | DOC-003 |
| FR-035 | US-5 | S-39 | SkillsToolsPage.levels.test; TestPermissionsPatch_SkillAndVisibilityMembers | DOC-003 |
| FR-036 | US-5 | S-40 | TestManifest_LevelsShapeTurn | DOC-003 |
| FR-037 | US-5 | S-41 | TestVisibilityStartingLevels_Upgrade | DOC-003 |
| FR-038 | US-5 | S-42, S-43 | TestSkillsSummary_Levels | DOC-003 |
| FR-039 | US-5 | S-44 | SkillsToolsPage.levels.test; TestManifest_LevelsShapeTurn | DOC-003 |
| FR-040 | US-1, US-5 | S-99 | TestPermissionsPatch_PerKey | DOC-004 |
| FR-041 | US-7 | S-52, S-53, S-54 | NewSkillMenu.test; TestWorkflowStart_W5 | DOC-005 |
| FR-042 | US-7 | S-55, S-56 | InstallReview.category.test | DOC-005 |
| FR-043 | US-7 | S-57 | skills-tools.spec.ts | DOC-005 |
| FR-044 | US-7 | S-58 | skills-tools.spec.ts | DOC-005 |
| FR-045 | US-8 | S-59, S-04 | TestMcpAdd_WritesAskWildcard | DOC-001 |
| FR-046 | US-8 | S-60, S-61 | skills-tools.spec.ts | DOC-001 |
| FR-047 | US-8 | S-62, S-63 | skills-tools.spec.ts | DOC-001 |
| FR-048 | US-9 | S-64 | skills-tools.spec.ts | DOC-008 |
| FR-049 | US-9 | S-65 | skills-tools.spec.ts | DOC-008, DOC-009 |
| FR-050 | US-10 | S-66, S-67 | SkillsToolsPage.states.test | DOC-001 |
| FR-051 | US-10 | S-68 | skills-tools.spec.ts | none — layout only, no behaviour to document |
| FR-052 | US-10, US-6 | S-69, S-100 | skills-tools.spec.ts; SkillsToolsPage.focus.test | none — keyboard follows product-wide conventions |
| FR-053 | US-10 | S-52, S-75 | NewSkillMenu.test | none — wording rule, no user page |
| FR-054 | US-11 | S-70 | TestSetAgentPermissions_SkillPolicyChangesParity | DOC-007 |
| FR-055 | US-11 | S-74 | TestGetAgent_SkillPermissions | DOC-007 |
| FR-056 | US-11 | S-71, S-72 | TestInstallSkillCategoryParity | DOC-007 |
| FR-057 | US-11 | S-73 | TestSetConfig_RefusesSandboxSkillKeys | DOC-007 |
| FR-058 | US-12 | S-75 | rider test #1920 | none — skill text only |
| FR-059 | US-12 | S-76 | rider test #1297 | DOC-006 |
| FR-060 | US-12 | S-77 | rider test #1298 | DOC-006 |
| FR-061 | US-12 | S-78 | rider test #1411 | none — bug fix restoring documented behaviour |
| FR-062 | US-12 | S-79 | rider test #99 | DOC-004 |
| FR-063 | US-12 | S-80 | rider test #1570 | none — error state already described |
| FR-064 | US-12 | S-81 | rider test #1576 | none — bug fix |
| FR-065 | — | S-64, S-26, S-01, S-101 | cut checklist | DOC-009 |
| FR-066 | US-2 | S-85, S-86, S-87 | TestSkillAuthority_FoldersFollowOutcome | DOC-010 |
| FR-067 | US-2, US-4 | S-10, S-11, S-30, S-31, S-93 | TestSkillConsent_Table | DOC-004 |
| FR-068 | US-7 | S-94, S-95 | TestInstallReview_StageBindsRevision | DOC-011 |
| FR-069 | US-5 | S-89, S-90 | TestFullSkills_SentinelAndBudget | DOC-010 |
| FR-070 | US-5 | S-91, S-92 | TestVisibilityTransitions | DOC-010 |
| FR-071 | US-1 | S-83 | SkillsToolsPage.saveStates.test | DOC-001 |
| FR-072 | US-1 | S-82 | TestPermissionsPatch_PerKey | none — internal API; the screen behaviour is covered by DOC-001 |
| FR-073 | US-5 | S-96 | TestToolSearch_FindsSkills | DOC-012 |
| FR-074 | US-8 | S-88 | TestMcpMigration_CoverageMatrix | DOC-004 |
| FR-075 | US-2 | S-102, S-16 | TestProjectSkillKeys; TestSkillPolicyUpgrade_FromSkillTool | DOC-004 |
| FR-076 | US-1, US-6 | S-84 | SkillsToolsPage.setAll.test | DOC-001 |
| FR-077 | US-7, US-11 | S-97, S-98, S-103, S-104 | TestUpdateAgentToolsCfg_PreservesSkillPolicies; TestCreateAgent_SkillPolicyChangesParity; TestWriteSkillWorkflow_PublishesCategorisedSkill; TestEditSkill_SetCategoryOnce | DOC-007, DOC-011 |
| FR-078 | US-4, US-9 | S-101, S-65 | LegacySkillTrust.compat.test; skills-tools.spec.ts | DOC-009 |
| FR-079 | US-5 | S-90 | SkillsToolsPage.levels.test | DOC-010 |
| FR-080 | US-2 | S-102 | TestProjectSkillKeys | DOC-004 |

**Completeness check** (round 1: re-run over S-01 to S-104 and FR-001 to FR-080): every FR appears with at least one scenario and one test; scenarios S-01 to S-104 each appear at least once (S-03 in FR-001's page test set via skills-tools.spec.ts row 31; S-20, S-57, S-58, S-60 to S-65, S-68, S-69 through FR-024 and FR-043 to FR-052).

---

## Riders

Search: `gh issue list --repo elicify-ai/omnipus --search "skills OR tools in:title" --state open` (exit 0, 74 issues), plus the design notes' #1920. The chief confirms before the design is final; not confirmed as of round 1. The owning epic is also to be reconciled (header).

| Issue | Why it rides | Proposed |
|---|---|---|
| #1920 orchestrate skill names Ava/Admin | design-notes gap 5; this package touches built-in skill text (categories) | IN (FR-058) |
| #1297 readouts ignore God Mode | this package rebuilds the readouts of the agent tab and `get_agent` | IN (FR-059) |
| #1298 agent tools view lists no service tools | same agent tab and handler | IN (FR-060) |
| #1411 stale tool policy on reopen | same agent tab | IN (FR-061) |
| #99 doctor warns on trust allow | trust values change here | IN (FR-062) |
| #1570 one unreadable skill fails GET /skills | the Skills tab's list | IN (FR-063) |
| #1576 copies show source name | Skills tab rows, Make a copy | IN (FR-064) |
| #817 item 1 agents loosen other agents' permissions | `set_agent_permissions` gains skill changes and widens the same gap | CHIEF — needs a founder design decision; recommend at least the same rule for skills as for tools |
| #1063 MCP tools denied despite allow | External tools tab values | CHIEF — verify it still reproduces first |
| #708 save after stopping a plan shows a network error | agent tab save | CHIEF |
| #1031 skill/tool discovery ranking | Hidden skills rely on `Skill` search | CHIEF — larger, separate |
| #1380 show project-folder skills | Skills tab list | CHIEF — would let mount skills be edited on screen |

---

## Assumptions

- Grants stay the gate (ADR-072 D4): per-skill permission is a second check after the grant, the way a service assignment and a tool permission are two checks for MCP tools (Hard Constraint #6, ADR-090).
- ToolSearch also finds skills (C-17) [pending ST-Q6]; round 0 had left R31's "one search for both" out without a founder decision (round-1 MAJ-015).
- Skill categories for built-ins (decided: founder default, former FQ-5), proposal from R32 Appendix B mapped to today's 29 skills: Tools — add-connector, add-provider, capability-plan, change-agent, connect-channel, create-agent, dependency-check, disable-channel, doctor-usage, inbox-triage, install-skill, provider-config, remove-agent, remove-connector, shell-diagnosis, team-delegation, team-setup, welcome-workspace, write-skill; Business — interview, define-goal, plan, orchestrate; Research — deep-research; Testing & Security — verify; Content & Media — elicify-docx, elicify-pdf, elicify-pptx, elicify-xlsx.
- Project-folder (mount) skills are not listed on the Skills tab (#1380 not a rider by default); they have their own keys, migration and defaults (C-19).
- The Settings package removes the controls from Settings; this package does not edit Settings files (C-22). Two Settings-spec changes follow from this spec and need its owner: retiring the legacy trust endpoint, and under ST-Q4 B removing "Load tools on demand" from Limits (its section name; round 0 wrongly said Performance for the post-Settings layout).
- Removed template sections: none.

## Clarifications

### 2026-11-21

- Q: Save model? → A: autosave everywhere, no confirm, including Set all (T-Q3 B, founder via chief 2026-11-21 03:20Z).
- Q: Trust words? → A: Allow / Ask / Deny; Ask is new behaviour (T-Q1 A).
- Q: Per-skill permissions in this build? → A: yes (T-Q2 A).
- Q: Levels? → A: four — Hidden, Name only, Name and short description, Full (T-Q4b B).
- Q: New skill item? → A: opens the Omnipus chat (T-Q5); W5 is Omnipus's write-skill workflow (`pkg/coreagent/prompts_adr090.go:102`).
- Q: Presets on agent tabs? → A: removed in the same build (T-Q6 A).
- Q: Per-agent levels? → A: none; this page sets the global default; agents only stricter, reusing today's model (T-Q7).
- Q: Categories of the 29 built-in skills (former FQ-5)? → A: the mapping in Assumptions, founder default via team-lead, 2026-11-21.
- Q: Can a Hidden item be found by search (former FQ-7)? → A: yes — not listed, found by search (R31), founder default via team-lead, 2026-11-21.
- Pending, asked as ST-Q1 to ST-Q7 (`/workspace/logs/skills-tools-1519f/founder-questions-r1.md`): see "Questions for the founder".

## Questions for the founder

Round 0's FQ-1 to FQ-7 were reworked by grill round 1. Team-lead sent seven questions to the founder on 2026-11-21 (`/workspace/logs/skills-tools-1519f/founder-questions-r1.md`); FQ-5 (built-in categories) and FQ-7 (Hidden still searchable) take the default and are recorded under Clarifications. Until the answers arrive, the spec carries the recommended option, and every place that depends on an answer is marked **[pending ST-Qn]**. Changing one answer means editing only the switch points listed here.

| Question | Option in place | Switch points if the other option is chosen |
|---|---|---|
| ST-Q1 What "Full" means for a skill | A — whole instructions every new chat, only after the permission allows it; visible cannot-fit outcome | C-1 (Full, skill column), C-11 E8, C-14, FR-038, FR-069, FR-079, US-5 AS8 and AS11, S-42, S-89, S-90, D9, ST-13, DOC-003, DOC-010, ADR-TODO-1 |
| ST-Q2 Trust check under God Mode, Auto-approve and Always Allow | A — separate check per skill version, survives all three; a reviewed install counts | C-3 `ask` row, C-11 E4, C-12 right-hand column, FR-030, FR-067, US-4 AS4, S-30, S-31, S-93, D1 rows 8–9, D10 rows 5–8, ST-10, ST-21, DOC-004 |
| ST-Q3 Starting permission of a new skill | A — Allow (project skills: same rule, stated separately in C-19) | C-2 self-heal, C-19, FR-020, US-2 AS8, S-17, D1 row 13 |
| ST-Q4 Which control decides what the model sees about tools | B — the four levels only; the switch is retired by migration | C-6 (two paragraphs), C-21 last row, C-22, FR-037, FR-039, US-5 AS9, S-44, D2 row 7, D7 rows 9–12, Settings-spec change request |
| ST-Q5 External tools with no permission at upgrade | A — Ask for genuinely uncovered tools; covered tools unchanged | C-18 "Migration writes" column, FR-045, FR-074, S-88, D8 rows 4–7, SC-003, DOC-004 |
| ST-Q6 One search for tools and skills | A — included in this package | C-1 (Hidden, skill column), C-11 E9, C-17, FR-073, S-96, DOC-012, Assumptions |
| ST-Q7 The autonomous-plan warning | A — keep this one warning only | Overview, departure table, FR-007, FR-010, US-1 AS5, Behavioral Contract, Qualitative Prohibitions, S-05, ST-03 |

### Notes for team-lead (not founder questions)

- **Settings spec changes needed** (C-22): retire the legacy `/security/skill-trust` enum after Settings lands; under ST-Q4 B remove "Load tools on demand" from Limits. Route to the Settings spec owner.
- **Project skills count as unverified** (C-19). This makes trust Ask ask once per version of a project skill that loaded silently before. It follows from the round-0 edge case and ST-Q2 A; if the founder wants project skills treated as local and trusted, that is a separate question — recommended to raise only if ST-Q2 comes back A.
- **Epic and riders**: the owning epic and the rider set are still unconfirmed (header, Riders).

---

## Round-1 fix log

Grill round 1 report: `docs/internal/specs/skills-tools-redesign-spec-review.md` (untracked, at review commit `5028ff7b01`).

| Finding | What changed | Where |
|---|---|---|
| CRIT-001 Full before consent | Full injects only when the skill's outcome is `allowed` for that agent and chain; Ask or trust-Ask Full skills are listed as Name and short description until approved; unattended never injects; sentinel tests on captured requests | C-1, C-11 E8, C-14, FR-038, US-5 AS8, S-42, S-89, D9, ST-13 [pending ST-Q1] |
| CRIT-002 File reads and helpers bypass the skill gate | Inventory of nine entry points with one outcome; denied and unapproved skill folders leave the loadable set in the app guard and the kernel grant; editor read exception kept separate and read-only | C-11, FR-066, US-2 AS9, S-85–S-87, ST-19, SC-006, DOC-010 |
| CRIT-003 Partial save needs the whole tool map | New `GET/PATCH /security/permissions` naming only changed keys; whole-request validation; null, empty, duplicate rules; two-tab test; legacy PUT kept for today's Settings | C-2, API and Data, FR-013, FR-072, D6, S-82, ST-20, SC-007 |
| CRIT-004 Missing server key is not missing permission | Coverage decided by the resolver's own matching; full state matrix incl. broader wildcards, partial coverage, agent-only values, God Mode; two named behaviour changes; no agent entries written | C-7, C-18, FR-045, FR-074, D8, S-88, ST-22, SC-003 [pending ST-Q5] |
| MAJ-001 One consent contract | Consent table: ordinary Ask vs trust check, Auto, God, remembered grants bound to revision, installs, REST, unattended, stale answers | C-3, C-12, FR-016, FR-030, FR-067, D10, S-10, S-30, S-93, ST-21 [pending ST-Q2] |
| MAJ-002 Install review lacks facts | Stage-only review endpoint returning verification, revision, suggested category; publish exactly the reviewed revision; expiry; truthful Cancel and lost answers | C-13, FR-042, FR-068, US-7 AS4/AS10, S-94, S-95, UI states |
| MAJ-003 Project skills have no identity | Own keys `project:<workspace>/<slug>`, lifecycle, same-slug rule, migration order (idempotent, restart-safe), named change for later grants | C-19, FR-019, FR-075, FR-080, Edge Cases, S-102, D6 row 16 |
| MAJ-004 Visibility migration copied labels | Starting levels derived from compression, discovery, deferred, preview flag; per-server keys for offline and future tools; literal captured requests as fixtures; SC-003 lists named changes | C-6, D7, FR-037, S-41, ST-06, SC-003 [pending ST-Q4] |
| MAJ-005 Live level changes undefined | One visibility authority; levels apply from next turn without evicting loaded definitions; Deny removes; skill menu rebuilds on change; demoted built-ins searchable | C-15, FR-070, S-91, S-92, D11, ST-12 |
| MAJ-006 Full has no capacity outcome | Charged once to the request budget; turn not sent with a clear text; page shows sizes and a warning | C-14, FR-069, FR-079, S-90, D9 rows 7–10, UI states [pending ST-Q1] |
| MAJ-007 First landing breaks Settings | Legacy trust endpoint kept and mapped; legacy tool grid kept; links use today's addresses and neutral text; `set_config` refusal of the manifest keys lands here; Settings-spec change requests named; Performance vs Limits naming corrected | C-21, C-22, FR-049, FR-078, US-9 AS2/AS3, S-36, S-65, S-101, ST-24, Assumptions [pending ST-Q4] |
| MAJ-008 Parity misses create, updates, authoring | Per-path parity table; `create_agent` `skill_policy_changes`; update preservation; category set once for written and copied skills; write-skill sets category; new parity tests | C-10 (pointer), C-21, FR-056, FR-057, FR-077, S-97, S-98, S-103, S-104, ST-15 |
| MAJ-009 Autosave promises | Six save states; one request per click in order; app-level queue; late-failure toast; keepalive; 1,000 ms is request start | C-16, FR-007, FR-071, US-1 AS10, S-83, D12, UI states, User Journey |
| MAJ-010 Set all vs always-ask | Per-row allowed values; ineligible rows skipped and named; counts in label; server guards unchanged | C-20, FR-003, FR-076, US-1 AS2, US-6 AS4, S-48, S-84, D4 rows 7–10 |
| MAJ-011 No-confirm vs plan warning | Single exception stated everywhere and tied to the founder question | Overview, departures, FR-007, FR-010, prohibitions, S-05 [pending ST-Q7] |
| MAJ-012 Tool edits not audited per key | Per-key audit for every map and trust, on PATCH and the legacy PUT; none for no-ops and refusals; failure handling stated; FR-040 now traces to an audit scenario | C-2, FR-040, S-99, ST-23, D6 row 15, Traceability |
| MAJ-013 Focus lost when a row leaves a filter | Deterministic focus destinations and announcement | Accessibility and Keyboard, FR-052, US-6 AS6, S-100, D4 row 10 |
| MAJ-014 Checks that cannot fail | Captured requests with sentinels instead of model answers; literal migration fixtures; W5 to a published categorised skill; direct REST install path; audit assertions; mutation probes and execution checklist | Hands-on rows ST-06, ST-09, ST-12, ST-13, ST-15, ST-19–ST-24; Test Implementation Order (positive controls); SC-003, SC-005 |
| MAJ-015 Unified search dropped without decision | ToolSearch finds skills, typed, no loading; recorded as pending, not as an assumption | C-17, FR-073, S-96, Assumptions, DOC-012 [pending ST-Q6] |
| MIN-001 view-switch is a radio group | Tabs use the catalogued `tabs` component | Design-System Components, Accessibility and Keyboard, FR-052 |
| Coordination warning (epic, riders) | Header states the epic mismatch as Unknown for the chief; riders still unconfirmed | Header, Riders, Notes for team-lead |
| FQ-5, FQ-7 | Recorded as decisions (founder default) | Clarifications, Ambiguity Warning 1, C-1 |
