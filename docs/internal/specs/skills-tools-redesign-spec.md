Status: In review

ADR: none — no open design decision was routed to an ADR. The founder settled the design in the wireframe answers (T-Q1 to T-Q7, T-Q4b). The contract shapes below are architect decisions recorded in "Contract Changes". Two existing ADRs need a dated correction because of founder answers: [ADR-072 — Skill activation and loading: an on-demand `Skill` tool, the per-agent grant as the real gate, and a project's own skills and instructions](../architecture/ADR-072-skill-activation-and-loading.md) (OQ3 "no always-on skill" is reversed by the T-Q4b "Full" level, see FQ-1) and [ADR-071 — Tool manifest tier redesign](../architecture/ADR-071-tool-manifest-tier-redesign.md) (the fixed tier lists become stored, editable levels). Both corrections are ADR-TODO rows in "User-facing documentation".

# Feature Specification: Skills & Tools redesign (page F)

**Created**: 2026-11-21
**Input**: founder-approved wireframe and notes (`/workspace/omnipus-coordination/handoffs/ui-redesign/skills-tools-wireframe/`: `design-notes.md`, `questions.md`, `re-ask-2026-11-20.md`, `review-response.md`, `index.html`, `app.js`); inventory of today's page (`/workspace/logs/ui-1519/skills-tools/inventory.md`, read at `7faff52915`); UX review (`/workspace/logs/ui-1519/skills-tools/ux-review.md`); requirements R30–R39 (`/workspace/omnipus-coordination/handoffs/ui-redesign/navigation-menu-wireframe/requirements.md`); Settings S-Q8 and `docs/internal/specs/settings-redesign-spec.md` FR-060 on branch `landing/settings-1519` (`30e1050627`). Founder answers, final: T-Q1 A (with the note that Allow / Ask / Deny look and work identically for tools and skills, on this page and in each agent's own settings), T-Q2 A, T-Q3 B, T-Q4b B, T-Q5 (Omnipus chat), T-Q6 A, T-Q7 (this page sets the global default; the agent level is stricter only, reusing today's tool-policy model; no per-agent "what the model sees"). No interview-me output file exists; the wireframe answers take its place.
**Code baseline**: release `4e34afa416`. Every `file::symbol` and line below was read at that commit. GitNexus MCP tools were not available in this session; discovery used `grep`/`Read` (impact rows are therefore Inferred).
**Epic**: #1519 (Stage: Spec; Horizon: Now).
**Riders** (proposed by the architect; the chief confirms before the design is final): IN — #1920, #1297, #1298, #1411, #99, #1570, #1576. FOR THE CHIEF TO DECIDE — #817 item 1, #1063, #708, #1031, #1380. Details: "Riders" near the end.
**Agent tool field table**: see "Agent tool parity" under Contract Changes (C-10). Objects changed: the agent (per-agent skill permission) and the skill (global permission, what the model sees, category). Global settings (skill trust, tool and skill permission ceilings, levels) stay operator-only, refused to `set_config` for the reason already recorded in `pkg/sysagent/tools/config.go::blockedConfigKeys` ("sandbox").

---

## Overview

Today, what an agent may use is decided in three places. The Skills & Tools page only lists skills, connected services and built-in tools; it points to Settings → Security for the global Allow / Ask / Deny of every tool, and Settings also holds the skill trust choice. Each agent's own Tools tab holds that agent's stricter values and three role presets. Skills have no Allow / Ask / Deny of their own: one tool, `Skill`, is a single switch for every skill. How much the model is told about each tool is fixed in code, and the per-skill level does not exist.

This package makes Skills & Tools the one place for the global answer to "may this run, and what does the model see about it", for Omnipus tools, external (MCP) tools and skills. Every tool and every skill gets the same Allow / Ask / Deny control, here and in each agent's Tools tab, where an agent can only be stricter (founder T-Q1, T-Q2, T-Q7). Skill trust becomes the same three words for unverified skills (T-Q1 A). Every tool and skill gets one of four "What the model sees" levels: Hidden, Name only, Name and short description, Full (T-Q4b B), set globally only (T-Q7). Every change saves on its own, with no Save button and no confirm, including "Set all" (T-Q3 B). The three role presets go away on this page and on every agent's Tools tab (T-Q6 A). The New skill menu opens the Omnipus chat to write a skill (T-Q5), and an install asks for a category (R32).

Settings (page G) removes tool permissions, skill trust and web search from Settings because this page carries them; Settings is cut only after this page has landed (`settings-redesign-spec.md` FR-060). This package therefore also moves the Web & Search provider settings here as a side panel (R30a).

## Where this spec departs from the wireframe (answers win)

| Wireframe draws | This spec | Why |
|---|---|---|
| A Save bar (Save changes / Discard, "Leave without saving?") and a confirm before a page-wide or 10+-row Set all (T-Q3 D, mapping N19–N21, N26) | No Save bar, no Discard, no leave prompt, no Set all confirm. Every value saves on its own a moment after the click, with a "Saved" signal | T-Q3 **B**: autosave everywhere, no confirm; a wrong Set all is undone by hand |
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
| `ToolPolicyEditor` (preset removal, Skills group) | MEDIUM | `ToolsAndPermissions.tsx`, `SecuritySection.tsx` (leaves Settings) | agent profile Tools tab |

The four HIGH rows need sign-off before GREEN starts. They touch `pkg/agent` and the tool-policy engine, so hands-on UAT on the cut commit is mandatory (rows ST-01 to ST-18 below).

---

## Contract Changes (contract-first — Hard Constraint #8)

The architect decides the shapes; backend-lead edits `contracts/` and regenerates (`scripts/gen-contracts.sh`). Nothing below is implemented by this spec.

### C-0 How T-Q7 maps onto today's policy layers (the base)

| Founder words (T-Q7) | Today's mechanism, reused unchanged | Evidence |
|---|---|---|
| "The Skills & Tools page sets the GLOBAL default" | Layer 1, the global ceiling `sandbox.tool_policies`, complete for the static catalog, self-healed by `ReconcileToolPolicyCeiling`; written by `PUT /security/tool-policies` | root `CLAUDE.md` line 107 (Hard Constraint #6); `pkg/config/sandbox.go:523`; `pkg/config/validate.go:707`; `pkg/gateway/rest_tool_policies.go:68` |
| "The agent level sets per-agent permissions and can only be STRICTER" | Layer 2, sparse per-agent overrides `tools_cfg.builtin.policies`, merged strictest-wins | `pkg/config/config.go:767`; `pkg/tools/compositor.go:163`, `:203` |
| "If the global policy says Ask, the agent cannot say Allow" | The SPA locks the looser badges (`ToolPolicyEditor.tsx::isPolicyLocked`, `:134`); the server stores a looser value but floors it at run time; `set_agent_permissions` reports the clamp | `ToolPolicyEditor.tsx:126-156`; `compositor.go:203-224`; `agent_permissions.go:204`, `:221` |
| "Reuse it, do not reinvent it" | Skills get the **same two layers and the same merge function**: a global skill map (complete over installed skills) and a sparse per-agent skill map. No third stored layer, no default fallback, no backfill (Hard Constraint #6; `scripts/check-no-fail-closed-backfill.sh`) | this spec C-2, C-4 |
| "No separate per-agent 'what the model sees' setting" | Levels are global only; an agent's Deny (effective) still removes the item from what the model sees, as today | `FilterToolsByPolicy` (`compositor.go:471`) runs before the manifest is built |

Two resolution-time rules sit beside the merge and are **not stored layers**, exactly like today's always-ask rule (`pkg/tools/always_ask.go:18`): God Mode floors the global side to Allow (unchanged), and the skill-trust floor for unverified skills (C-3). This is the same shape as the always-ask exception and is flagged for security-lead (focus area: `pkg/policy`-equivalent code in `pkg/tools`).

### C-1 New schema `ModelVisibility`

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `ModelVisibility` | new | `contracts/components/schemas/ModelVisibility.yaml` | string enum `hidden`, `name_only`, `name_and_description`, `full` |

Meaning (the oracle for every scenario below):

| Value | On screen | A tool | A skill |
|---|---|---|---|
| `full` | Full | Its complete definition is sent every turn; callable at once | Its whole instructions are in the agent's context from the start of every chat (FQ-1) |
| `name_and_description` | Name and short description | Listed as one line: name and the first line of its description (at most 140 characters); loaded through ToolSearch before use | Listed in the skill menu with name and description (today's behaviour) |
| `name_only` | Name only | Listed by name alone; loaded through ToolSearch before use | Listed in the skill menu by name alone |
| `hidden` | Hidden | Not listed; ToolSearch can still find and load it | Not listed; `Skill` search and the person's `/name` command can still find and load it |

An effective Deny for an agent overrides every level for that agent: the item is not listed, not found and not callable (today's rule, `compositor.go:471`).

### C-2 Global permissions and levels: `GlobalToolPolicies` extended

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `GlobalToolPolicies` | changed | `contracts/components/schemas/GlobalToolPolicies.yaml` | adds three optional members, below; `policies` unchanged |

New members:

- `skill_policies`: object, key = installed skill id (case-insensitive identity, as skills already are), value `ToolPolicy` (`allow`/`ask`/`deny`). GET returns the complete map over every installed skill. PUT: an omitted member leaves every stored skill value unchanged; a supplied member sets the listed keys only (a merge, never a delete). An unknown skill id → 400, nothing saved.
- `visibility`: object, key = tool name (static catalog name, or an MCP tool name, or an `mcp_<server>_*` wildcard), value `ModelVisibility`. GET returns the complete effective map over every tool in `GET /tools`. PUT merges like `skill_policies`. A locked tool (C-6) with any value other than its locked value → 400 "`<tool>` is always loaded: agents need it to work, so its level cannot be changed.", nothing saved.
- `skill_visibility`: object, key = installed skill id, value `ModelVisibility`. Same GET/PUT rules.

The existing PUT outcomes stay: 200 = saved and applied; 503 = saved, not applied, exact existing text. Audit: every PUT that changes a skill value, a level or a skill level writes one `EmitSecuritySettingChange` entry per changed key with old and new value (resources `sandbox.skill_policies.<id>`, `sandbox.tool_visibility.<name>`, `sandbox.skill_visibility.<id>`).

Storage (architect decision): `sandbox.skill_policies`, `sandbox.tool_visibility`, `sandbox.skill_visibility` in `config.json`, beside `sandbox.tool_policies`. Reason: the whole `sandbox` subtree is already operator-only and refused to `set_config` (`pkg/sysagent/tools/config.go::blockedConfigKeys`), which is the correct rule for global ceilings and levels (C-10).

Self-heal (same pattern as `ReconcileToolPolicyCeiling`, additive, never overwriting): on every load and after every install, a skill with no stored global permission gets **Allow** (FQ-3) and a skill with no stored level gets `name_and_description`; a tool with no stored level gets its starting level (C-6). Removing a skill removes its two keys. Retired keys are not re-added.

### C-3 Skill trust becomes Allow / Ask / Deny

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `SkillTrustResponse` | changed | `contracts/components/schemas/SkillTrustResponse.yaml` | `level` enum becomes `allow`, `ask`, `deny` |
| `SkillTrustUpdateRequest` | changed | `contracts/components/schemas/SkillTrustUpdateRequest.yaml` | same enum; a legacy value (`block_unverified`, `warn_unverified`, `allow_all`) → 400 "Use allow, ask or deny.", nothing saved |
| `SkillTrustUpdateResponse` | changed | `contracts/components/schemas/SkillTrustUpdateResponse.yaml` | `applied_level` same enum; `warning` present for `allow`: "Allow turns off the check for unverified skills." |

Config: `sandbox.skill_trust` stores `allow`/`ask`/`deny`. On load, a legacy stored value is rewritten once: `block_unverified` → `deny`, `warn_unverified` → `ask`, `allow_all` → `allow`; an empty value is treated as `ask` (today's empty value means `warn_unverified`, `rest_skill_trust.go:47-49`). The rewrite writes one audit entry.

Meaning (applies to **unverified** skills only, C-5):

| Trust | Install of an unverified skill (screen, REST, `install_skill`) | Load of an unverified skill by an agent |
|---|---|---|
| `allow` | proceeds | follows the skill's own permission only |
| `ask` | proceeds after the person confirms in the install review (screen); `install_skill` keeps its own Ask | the effective permission is at least Ask: the normal approval card, also under Auto-approve and in God Mode (FQ-2) |
| `deny` | refused: "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed." | refused: "`<skill>` is unverified, and unverified skills are set to Deny in Skills & Tools." |

The trust floor is a resolution-time rule, not a stored layer (C-0). It survives God Mode, like the always-ask rule (FQ-2).

### C-4 Per-agent skill permissions: `AgentToolsCfg`, `AgentToolsUpdateRequest`, `AgentToolsResponse`

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `AgentToolsCfg` | changed | `contracts/components/schemas/AgentToolsCfg.yaml` | adds `skills: { policies: object<skill id, ToolPolicy> }`, sparse overrides only |
| `AgentToolsUpdateRequest` | changed | `contracts/components/schemas/AgentToolsUpdateRequest.yaml` | adds optional `skills: { policies }`: omitted = unchanged; `{}` = clear every skill override; a key not an installed skill → 400; the Omnipus per-agent Ask rule (`rest_omnipus_guards.go:57`) applies to these values too (422, existing text); locked system agent 403 and external worker 400 apply unchanged |
| `AgentToolsResponse` | changed | `contracts/components/schemas/AgentToolsResponse.yaml` | adds required `skills: AgentSkillEntry[]` |
| `AgentSkillEntry` | new | `contracts/components/schemas/AgentSkillEntry.yaml` | `name` (skill id), `display_name`, `granted` (bool), `configured_policy` (`ToolPolicy` or null = no override), `global_policy` (`ToolPolicy`), `effective_policy` (`ToolPolicy`, after trust floor and God Mode), `unverified` (bool) |

The per-agent value is stored as given (like tools today) and floored at run time; the screen locks looser badges; `set_agent_permissions` reports the clamp (C-0).

### C-5 Skill fields: `Skill`, `SkillInstallRequest`, `SkillCategory`

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `SkillCategory` | new | `contracts/components/schemas/SkillCategory.yaml` | enum `tools`, `business`, `development`, `testing_security`, `data_ai`, `devops`, `documentation`, `content_media`, `research`, `lifestyle`, `databases`, `blockchain`, `uncategorised` (R32's 12 SkillsMP categories) |
| `Skill` | changed | `contracts/components/schemas/Skill.yaml` | adds required `category` (`SkillCategory`); `verified` redefined: true for a built-in skill, or for a skill whose install was checked against a registry hash **and** whose folder has no local changes since; false otherwise |
| `SkillInstallRequest` | changed | `contracts/components/schemas/SkillInstallRequest.yaml` | adds optional `category` (`SkillCategory`). Absent → the file's `metadata.category` when it is one of the values, else `uncategorised` |

The category and the install-time verification result are stored in Omnipus's own data, outside the skill folder (R32: never written into the skill file). A built-in skill takes its category from its own `metadata.category` (Omnipus-owned file; proposed values in Assumptions, FQ-5). The category is set at install only; no later edit control exists (R32, UX review F21).

### C-6 `ToolRegistryEntry` carries the level and the fixed rows

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `ToolRegistryEntry` | changed | `contracts/components/schemas/ToolRegistryEntry.yaml` | adds required `visibility` (`ModelVisibility`, global), `visibility_locked` (bool), `permission_fixed` (bool) |

- `permission_fixed: true` for `ToolSearch` and `Skill`: no Allow / Ask / Deny on screen; every Set all skips them. `ToolSearch` keeps today's sentence; `Skill` shows "Permissions are set per skill on the Skills tab." with a link (T-Q2 A). Their stored global value is `allow` and is not editable (PUT with another value → 400 "`<tool>` has no permission of its own.").
- `visibility_locked: true` for `ToolSearch`, `Skill`, `AskUserQuestion`, `goal_claim`, `set_goal` (R31's list; `switch_agent` in R31 does not exist at `4e34afa416`). Locked value: `full` (ToolSearch shows "Always available for discovery." instead of a level).
- Starting levels (an upgrade changes nothing an agent sees, R31): the 36 tools in `fullManifestToolNames` → `full`; `serve_web` → `name_and_description`; every other static tool → `hidden`; MCP tools of a server whose `deferred` is unset or true → `hidden`, of a server with `deferred: false` → `full`. With `tools.manifest.preview_all_lazy` on, every lazy tool starts `name_and_description`. After the one-time migration, `MCPServerConfig.deferred` and `preview_all_lazy` are no longer read; a stored value is left in place, ignored, with one warning at load.
- `AgentToolEntry.manifest_tier` stays and is derived: `full` → `full`; `name_and_description`, `name_only`, `hidden` → `compressed`; ToolSearch → `infra`.

"Load tools on demand" (`tools.manifest.compressed`, kept in Settings → Performance by the Settings spec) keeps its meaning: when off, every allowed tool is sent in full whatever its level, and the two tool tabs say so (FQ-4).

### C-7 New service tools start at Ask; no "Unset"

No schema change. Behaviour of `POST /mcp-servers` and the `add_mcp_server` agent tool: adding a service writes the global key `mcp_<server>_*` = `ask`, which covers every tool the service has now and adds later (R30b). One-time upgrade: a configured service with no `mcp_<server>_*` key gets one, value per FQ-6. After this, every tool row has a value; the screen has no "Unset" state (R30b).

### C-8 `WorkflowStart` gains the write-skill workflow

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `WorkflowStart` | changed | `contracts/components/schemas/WorkflowStart.yaml` **and** the inline copy in `contracts/asyncapi.yaml` (kept in sync by hand, per the schema's own description) | `workflow` enum adds `W5` (write-skill, `pkg/coreagent/prompts_adr090.go:102`). `target_workspace_id` becomes required for `W1` and `W18` only; for `W5` it is absent (skills are installation-wide) |

### C-9 Approval card names the skill

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `ToolApprovalRequiredFrame` | changed | `contracts/components/schemas/ToolApprovalRequiredFrame.yaml` | adds optional `skill` object: `name`, `display_name`, `unverified` (bool). Present when `tool_name` is `Skill` and the call loads a skill |
| `AgentSkillPermission` | new | `contracts/components/schemas/AgentSkillPermission.yaml` | `skill`, `effective` (`ToolPolicy`), `override` (`ToolPolicy` or null) — the skill twin of `AgentToolPermission` |
| `AgentDetail` | changed | `contracts/components/schemas/AgentDetail.yaml` | adds `skill_permissions: AgentSkillPermission[]` (returned for `fields: ["skill_permissions"]`) |

### C-10 Agent tool parity (field table)

| Object | Property | User can set | Agent can set | Reason if different / refusal |
|---|---|---|---|---|
| Agent | per-agent skill permission | yes — agent's Tools tab | yes — `set_agent_permissions` gains `skill_policy_changes` (`{set, remove}`, same shape as `ToolPolicyChanges`); same clamp report; same Ask rule for Omnipus; read through `get_agent` `skill_permissions` | — |
| Agent | per-agent tool permission | yes | yes (unchanged) | — |
| Agent | skill grants | yes (agent Skills tab) | yes (`create_agent`/`update_agent` `skills`, unchanged) | — |
| Skill | global permission | yes — this page | no | Global ceilings are operator-only; `set_config` refuses `sandbox.*` with its existing reason (`config.go::blockedConfigKeys`). An agent tool must not loosen its own ceiling |
| Skill | what the model sees | yes — this page | no | Same reason and refusal |
| Skill | category | at install (screen, REST) | at install — `install_skill` gains optional `category` (`SkillCategory`); a value outside the enum is refused naming the allowed values | Neither can change it later (R32, F21) |
| Tool | global permission, what the model sees | yes — this page | no | Same as skill global permission |
| Setting | skill trust | yes — this page | no | Same reason |

Parity tests (named in RED by qa-lead): `TestSetAgentPermissions_SkillPolicyChangesParity` (fails when the agent tab can store a skill override the tool cannot), `TestInstallSkillCategoryParity` (fails when the screen can send a category `install_skill` cannot), and a new row in the existing `set_config` sandbox refusal test for `sandbox.skill_policies`, `sandbox.tool_visibility`, `sandbox.skill_visibility`.

### Collisions checked

- `skill_policies` keys are skill ids; they never enter `sandbox.tool_policies`, so the static-catalog validators (`ValidateSubmittedToolPolicyMap`, `config.go`/`validate.go:592`) and the `mcp_` carve-out are unchanged.
- No existing schema named `ModelVisibility`, `SkillCategory`, `AgentSkillEntry` or `AgentSkillPermission` (`ls contracts/components/schemas`).
- The Settings package changes no schema used here (its C-1 to C-7 are retention, restart, Performance and key deletion).

## API and Data

| Endpoint / event | Change |
|---|---|
| `GET/PUT /security/tool-policies` | C-2 members; this page's Omnipus tools, External tools and Skills tabs write through it |
| `GET/PUT /security/skill-trust` | C-3 enum |
| `GET /tools` | C-6 fields |
| `GET /skills` | C-5 `category`, redefined `verified` |
| `POST /skills/install` | C-5 `category`; trust Deny refuses an unverified install (C-3) |
| `GET/PUT /agents/{id}/tools` | C-4 `skills` |
| `POST /mcp-servers` | writes the service's Ask key (C-7) |
| WS `tool_approval_required` | C-9 `skill` |
| WS `message` with `workflow_start` | C-8 `W5` |

Stored data, behaviourally: one global permission and one level per installed skill; one level per tool; per-agent skill overrides inside the agent's tool settings; each skill's category and install-time verification result in Omnipus's own data.

---
## User Stories & Acceptance Criteria

### User Story 1 — Set the global permission of every Omnipus and external tool on one page (Priority: P0)

A person who runs Omnipus wants to decide, in one place, which tools every agent may run, may run after asking, or may never run. Today that grid sits behind "Advanced" in Settings → Security, and the Skills & Tools page only links to it.

**Why this priority**: Settings removes this grid (Settings FR-060); without this story the global ceiling has no screen at all.

**Independent Test**: open Skills & Tools → Omnipus tools, set `send_email` to Deny, reload, see Deny; an agent's call of `send_email` is refused.

**Acceptance Scenarios**:

1. **Given** the Omnipus tools tab, **When** the person clicks Deny on a tool row, **Then** the row shows Deny, a "Saved" signal appears, and after a reload the row still shows Deny.
2. **Given** a group of 12 tools on screen, **When** the person uses that group's Set all → Ask, **Then** all 12 rows show Ask and are saved, with no confirm.
3. **Given** the External tools tab and a service with 5 tools, **When** the person uses the service's Set all → Allow, **Then** all 5 tools show Allow and are saved.
4. **Given** any tool row, **When** the page shows it, **Then** exactly one of Allow, Ask, Deny is selected and "Unset" never appears.
5. **Given** a single click or a Set all that would make `execute_plan` resolve to Allow, **When** the person makes it, **Then** the existing "Confirm autonomous plan execution" dialog appears first, and Cancel changes nothing.
6. **Given** the Tool search row, **When** shown, **Then** it has no Allow / Ask / Deny and reads "Always available for discovery. The tools it finds still follow their own permissions."; every Set all leaves it out.
7. **Given** the save is refused, **When** the server answers with an error, **Then** the page shows the server's reason with Retry, and the rows show the values that are actually saved.
8. **Given** the save is stored but running agents could not be updated, **When** the server says so, **Then** the page shows "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." with Retry.

### User Story 2 — Give every skill its own Allow / Ask / Deny (Priority: P0)

A person wants one skill to run freely, another to ask first, and a third never to run, instead of one switch for all skills (T-Q2 A). It must look and work exactly like a tool row (T-Q1 note).

**Why this priority**: founder decision T-Q2 A; the Skills tab's main column.

**Independent Test**: set skill `deep-research` to Ask; an agent granted it loads it in a chat; an approval card appears naming the skill.

**Acceptance Scenarios**:

1. **Given** the Skills tab, **When** the person clicks Deny on a skill row, **Then** it is saved, and an agent granted that skill can no longer load it and no longer sees it in its skill list.
2. **Given** a skill set to Ask, **When** an agent in a chat tries to load it, **Then** the normal approval card appears naming the skill, with Approve Once, Deny and Always Allow.
3. **Given** a skill set to Ask, **When** a task, plan or scheduled run tries to load it, **Then** the load is refused with "Not run: needs approval, and task and plan runs never ask." and no card appears.
4. **Given** a skill set to Allow and granted to the agent, **When** the agent loads it, **Then** it loads with no card.
5. **Given** a skill not granted to the agent, **When** the agent tries to load it, **Then** it is refused as today, whatever its permission (the grant stays the gate, ADR-072 D4).
6. **Given** the Omnipus tools tab, **When** the `Skill` row is shown, **Then** it has no Allow / Ask / Deny and reads "Permissions are set per skill on the Skills tab." with a link to the Skills tab.
7. **Given** an install upgraded from a version without per-skill permissions, **When** it first starts, **Then** every skill's global permission equals the old global `Skill` value, and each agent's old per-agent `Skill` value is copied to each skill granted to that agent at that moment, so no agent's behaviour changes.
8. **Given** a new skill is installed, **When** it appears, **Then** its global permission is Allow (FQ-3) and its level is "Name and short description".

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
4. **Given** trust = Ask and an unverified skill whose own permission is Allow, **When** an agent in a chat loads it, **Then** an approval card appears naming the skill and saying it is unverified, also with Auto-approve on.
5. **Given** trust = Allow, **When** chosen, **Then** a warning callout reads "Allow turns off the check for unverified skills. Choose Ask to turn it back on."
6. **Given** an upgrade from a stored Block unverified / Warn unverified / Allow all, **When** Omnipus starts, **Then** trust shows Deny / Ask / Allow respectively.
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
7. **Given** an upgrade, **When** Omnipus first starts, **Then** every tool's level matches what agents were sent before (36 Full, `serve_web` Name and short description, the rest Hidden; external services per their old setting) and every skill is Name and short description.
8. **Given** a skill set to Full and granted, **When** a new chat of that agent starts, **Then** the skill's whole instructions are in its context from the first turn (FQ-1).
9. **Given** "Load tools on demand" is off in Settings, **When** a tool tab opens, **Then** a note says "Load tools on demand is off in Settings → Performance, so every allowed tool is sent in full. These levels apply when it is on." with a link.

### User Story 6 — Find any tool or skill and change a group at once (Priority: P1)

A person with 53 skills and 200 tools needs search, filters and group actions.

**Why this priority**: R33, R35, R39; the page is unusable at this size without them.

**Independent Test**: type "browser" on Omnipus tools; only browser rows show; that group's Set all says "Set 17 matching" when 17 rows match.

**Acceptance Scenarios**:

1. **Given** the page, **When** it opens, **Then** it shows three tabs with counts — Skills, External tools (MCP), Omnipus tools — and Skills is selected.
2. **Given** a tab, **When** shown, **Then** one search field and two flat filters (Permission, What the model sees) sit on one line above one grouped list whose groups start collapsed.
3. **Given** a search, **When** text is typed, **Then** groups with matches open and only matching rows show; Escape in the field or the clear button empties it.
4. **Given** a search or filter is on, **When** the person uses a group's Set all, **Then** exactly the rows on screen change, and the control's label and accessible name carry the same count ("Set 3 matching").
5. **Given** no row matches, **When** the list is empty, **Then** "No tools match “zzzz”" shows with "Clear search and filters".
6. **Given** a filter is on and a row's value is changed so it no longer matches, **When** the save completes, **Then** the row leaves the list.
7. **Given** a "What the model sees" filter is on, **When** the list shows, **Then** a note says "Denied items are not loaded, so this filter leaves them out. Use the Permission filter to find them."

### User Story 7 — Add a skill: write it with Omnipus, upload it or install it from the catalog, with a category (Priority: P1)

**Why this priority**: T-Q5, R32; the Skills tab's main action.

**Independent Test**: New skill → Write with Omnipus opens the Omnipus chat with the message "Write a new skill"; Omnipus loads its write-skill workflow.

**Acceptance Scenarios**:

1. **Given** the Skills tab, **When** the person opens New skill, **Then** it offers "Write with Omnipus", "Upload a skill" and "Browse the catalog", and no item names a retired agent.
2. **Given** "Write with Omnipus", **When** chosen, **Then** the Omnipus chat opens and receives "Write a new skill" with the write-skill workflow start.
3. **Given** the connection is down, **When** "Write with Omnipus" is chosen, **Then** nothing is sent and a warning reads "Not connected — “Write a new skill” was not sent. Press the button again once you are reconnected."
4. **Given** a catalog result or an uploaded file, **When** the person chooses Install, **Then** a review shows name, version, whether it is verified, the current trust choice, and a required Category.
5. **Given** no category is picked, **When** Install is pressed, **Then** the field shows "Choose a category." and nothing is installed.
6. **Given** a skill file with `metadata.category: research`, **When** the review opens, **Then** Research is preselected.
7. **Given** skills are installed, **When** the Skills tab lists them, **Then** they are grouped by category; skills without one are under Uncategorised; empty categories are not shown.
8. **Given** a skill row, **When** the person opens More actions, **Then** it offers Make a copy, plus Remove for a skill that is not built-in.

### User Story 8 — Manage external services on the same tab (Priority: P2)

**Why this priority**: today's MCP tab, regrouped (R30, R35).

**Independent Test**: add a service; all its tools show Ask; switch it off; Test; Remove.

**Acceptance Scenarios**:

1. **Given** "Add service", **When** a service is added, **Then** all its tools show Ask, and tools it adds later also show Ask.
2. **Given** a service group header, **When** shown, **Then** it carries the on/off switch, Test, Edit and Remove, and the switch acts at once.
3. **Given** a service that cannot connect, **When** its group is opened, **Then** an error notice shows the server's reason with Retry and Edit.
4. **Given** Remove, **When** pressed, **Then** "Remove this service? Agents that use it lose its tools." asks first.
5. **Given** the local-program choice in Add service, **When** chosen, **Then** "This runs a program on your server" opens with "Use a network address instead" as the main, focused button.

### User Story 9 — Tool-specific settings and links (Priority: P2)

**Why this priority**: Settings FR-060 needs web search here; R30a.

**Independent Test**: Omnipus tools → Web & Search → Settings opens the search-provider panel; changing the default provider works as it did in Settings.

**Acceptance Scenarios**:

1. **Given** the Web & Search group, **When** the person presses its Settings button, **Then** a side panel opens with today's Default search and Fallback cards and their behaviour, unchanged.
2. **Given** the Shell group, **When** shown, **Then** it links "Shell safety rules" to Settings → Sandbox & network.
3. **Given** the Omnipus tools intro, **When** shown, **Then** it reads "Open Settings → Protection to change Auto-approve and God Mode." with the link.
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

1. **Given** `set_agent_permissions` with a skill change, **When** approved, **Then** the agent's override is stored and reported like a tool change, including the clamp sentence when the global value is stricter.
2. **Given** `install_skill` with `category: research`, **When** it installs, **Then** the skill shows under Research.
3. **Given** `set_config` with key `sandbox.skill_policies.plan`, **When** called, **Then** it is refused with the existing sandbox reason.
4. **Given** `get_agent` with `fields: ["skill_permissions"]`, **When** called, **Then** each granted skill is listed with its effective value and override.

### User Story 12 — Rider fixes on the same files (Priority: P2)

See "Riders"; each rider has its own BDD scenario under "Feature: Riders".

### Edge Cases

- A skill removed while its row is on screen: the next save of that row is refused (unknown skill, 400); the page reloads the list and says "This skill was removed."
- Two people change the same value in two browser tabs: the global map has no revision; the last save wins and each tab shows its own last saved value until reload (Ambiguity Warning 2).
- A Set all clicked twice quickly: one save per click; the page ends on the last saved state.
- A skill granted to an agent but denied globally: the agent's row shows Deny, Allow and Ask disabled with "Global setting is Deny; an agent can only be stricter."
- A stored per-agent Allow below a later global Ask: stored as is, floored at run time; the row shows the true effective value (as for tools, `ToolPolicyEditor.tsx::trueEffectivePolicy`).
- An unverified skill with God Mode on: the trust floor still applies (FQ-2).
- A project-folder skill (mount shelf, ADR-072 D4.1): no grant list; it follows trust (unverified unless built-in), its global permission (C-2 default) and its level; it is not listed on the Skills tab (#1380 is not a rider by default), so it cannot be edited on screen (Assumptions).
- A skill and a tool with the same name: separate maps; no collision.
- An external service whose id contains underscores: its Ask key uses the id the registry reports (`ToolRegistryEntry.server_id`), never a prefix parsed from tool names.
- `delegate` with `requested_skill` naming an Ask skill: the child's load raises the card in the root chat (#1263 routing); in an unattended chain it is refused.
- The person's own `/skill` command on an Ask skill: it loads without a card (the person asked by name); Deny and trust Deny still refuse it.

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Skills tab | spinner "Loading skills…", skeleton rows, trust block skeleton | trust block stays; "No skills installed" + "Browse the catalog" (outline) | callout with cause + Retry; no writes | list loads, permissions fail: rows read-only, "Permissions could not be loaded." + Retry | trust block, grouped rows, "Saved" after each change |
| External tools tab | spinner "Loading services…" | "No external services yet" + "Add service" | callout + Retry | a service not connected: "Not connected" in its header and the error notice inside; others normal | one group per service |
| Omnipus tools tab | spinner "Loading tools…" | "No Omnipus tools reported" + Retry | callout + Retry | levels fail, permissions load: level shows "Could not load" + Retry; badges work | one group per category |
| Agent Tools tab | as today | Skills group: "No skills granted. Grant skills on this agent's Skills tab." | as today (three Retry states) | global ceiling missing: read-only, today's text | rows, "Saved" |
| Install review | "Installing…" with Cancel usable | — | server reason; trust Deny refusal | — | toast "Installed <name>. No agent is granted it yet; grant it on an agent's Skills tab." |
| Web & Search panel | today's states | today's | today's | today's | today's |
| Page save signal | "Saving…" | — | "Not saved: <server reason>." + Retry; rows show stored values | "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." + Retry | "Saved" |

## User Journey

1. Sidebar → ASSETS → Skills & Tools; the Skills tab opens (US-6 AS1).
2. The trust block is first (US-4 AS1); a change saves at once.
3. Search or filter, open a category, change a skill's permission or level (US-2 AS1, US-5 AS1); "Saved" appears.
4. New skill → Write with Omnipus / Upload / Browse (US-7); the install review asks for a category; the skill appears with Allow and Name and short description (US-2 AS8).
5. External tools: Add service; its tools show Ask (US-8 AS1); the service's Set all (US-1 AS3).
6. Omnipus tools: a group's Set all (US-1 AS2); Web & Search → Settings panel (US-9 AS1).
7. An agent → Tools tab: the same rows, stricter only, with a Skills group (US-3).
Exit: through the sidebar; nothing is ever unsaved (T-Q3 B).

## Accessibility and Keyboard

- Tabs: the flat view switch with tablist semantics; arrow keys, Home, End.
- Allow / Ask / Deny: toggle buttons with `aria-pressed` in a group named "Permission for <name>"; a locked badge is disabled and its reason is visible text referenced by `aria-describedby`.
- Trust block: a radio group of three badges, one tab stop, arrow keys; all three meanings always visible, each linked by `aria-describedby`.
- "What the model sees": a menu button named "What the model sees for <name>: <level>"; arrow keys, Enter; Escape closes and returns focus.
- Group headers: `aria-expanded`, `aria-controls`. Set all: named "Set <n> matching: permission for the <n> matching <items> in <group>", the same number as its label.
- Save signal: polite status region; errors use `role="alert"`.
- Focus: the design system's one focus ring (focus-visible ownership, `omnipus-design-system`).
- Touch: 44 × 44 px minimum at 390 px; no sideways scroll; every badge carries an icon and a word.

## Design-System Components

Checked against `design-system/catalog.json` first; no new catalogued component is needed.

| Element | Component |
|---|---|
| Page header and the one gold main action per tab | `page-header`, `button` (default variant), `dropdown-menu` (New skill) |
| Tabs | `view-switch` |
| Search and filters | `search-field`, `filter-menu` |
| Grouped list | `list-group` (flat accordion, R35) |
| Permission badges | `PolicyBadge` (product shared component, reused) |
| Level control | `dropdown-menu` |
| States | `collection-state`, `query-error-state`, `skeleton`, `spinner`, `empty`, `callout` |
| Save signal | `AutoSaveIndicator` |
| Dialogs and panels | `dialog`, `confirm-dialog`, `sheet`, `select` (category), `switch`, `segmented-control`, `tooltip` |

The wireframe's Save bar is not built (T-Q3 B), so nothing new is published.

## Security and User Promises

- security-lead reviews before GREEN: the trust floor and its enforcement at every install and load path (supply chain), the skill policy resolution beside `pkg/tools/compositor.go`, the audit entries (`pkg/audit`), and the `set_config` refusal rows.
- Changed user promises (`docs/skills.md`, `docs/tools.md`, `docs/security.md`): trust words and meanings, per-skill permissions, `Skill` no longer a master switch, presets gone, levels, new services at Ask, the global grid's new home.
- The trust default moves from "warn and continue" to Ask: an unverified skill that loaded silently now asks in a chat and is refused in unattended runs (T-Q1 A). The docs and the upgrade note say so plainly (DOC-004).
- Hard Constraint #6 stays intact: two stored layers for skills as for tools, no default fallback, no backfill; trust and God Mode are resolution-time rules, like always-ask (C-0).
- No on-screen text names a retired agent, an issue number, a requirement tag or "contact support".

---

## Behavioral Contract

Primary flows:
- When a person changes a permission, a level or the trust choice, the system saves it with no further action and shows "Saved".
- When a person uses a group's Set all, the system changes exactly the rows shown in that group, in one save, with no confirm — except the existing autonomous-plan confirm when `execute_plan` would resolve to Allow.
- When an agent loads a granted skill, the system applies the strictest of the skill's global permission, the agent's own value, and — for an unverified skill — the trust choice.
- When an agent's turn starts, the system lists each allowed tool and each granted, not-denied skill at its global level.
- When a person opens an agent's Tools tab, the system offers only values at least as strict as the global value.
- When a service is added, the system sets all its tools, now and later, to Ask.

Error flows:
- When a save is refused, the system shows the server's reason with Retry and the stored values.
- When a save is stored but not applied to running agents, the system says so and offers Retry.
- When an unverified install meets trust Deny, the system refuses it and installs nothing.
- When a skill load meets an effective Deny, the system refuses it, naming the skill and which setting refused it.
- When an Ask load happens in an unattended run, the system refuses it with the existing unattended text.

Boundary conditions:
- When a locked tool's level is sent with another value, the system refuses it.
- When a group contains Tool search or Skill, every Set all skips them.
- When "Load tools on demand" is off, every allowed tool is sent in full whatever its level.
- When a filter is on, Set all counts and changes only matching rows.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not show a Save bar, Discard, a leave prompt or a Set all confirm on Skills & Tools, because T-Q3 B chose autosave with no confirm.
- The system must not show Cautious / Balanced / Full access anywhere, because T-Q6 A removes them.
- The system must not offer a per-agent "What the model sees" control, because T-Q7 rules it out.
- The system must not let an agent tool change a global permission, a level or trust, because global ceilings are operator-only (C-10).
- The system must not add a third stored policy layer, a default policy or a fail-closed backfill (Hard Constraint #6).
- The system must not write a category, permission or level into a skill's folder, because that breaks the hash check (R32).
- The system must not show "Unset" for any tool or skill (R30b).
- The system must not name Ava, Admin or any retired agent in menus, toasts or built-in skill text.
- The system must not make ToolSearch search skills or change the grant model; both are out of scope (Assumptions).
- The system must not change Settings screens (page G), Tasks and Board, or signed-in browsers (#1951).

### Machine-Verifiable Constraints

**Error Codes / Messages** (all bodies `ErrorResponse`):
- `PUT /security/tool-policies`, unknown skill id in `skill_policies` or `skill_visibility` → 400, nothing saved.
- `PUT /security/tool-policies`, a locked tool's level changed → 400, "`<tool>` is always loaded: agents need it to work, so its level cannot be changed."
- `PUT /security/tool-policies`, `ToolSearch` or `Skill` in `policies` with a value other than `allow` → 400, "`<tool>` has no permission of its own."
- `PUT /security/skill-trust`, a legacy value → 400, "Use allow, ask or deny."
- `POST /skills/install`, unverified skill with trust `deny` → 403, "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed."
- `POST /skills/install`, `category` outside `SkillCategory` → 400.
- `PUT /agents/omnipus/tools`, `skills.policies` containing `ask` → 422, the existing Omnipus refusal text and code.
- `PUT /agents/{id}/tools`, `skills.policies` key that is not installed → 400.

**Performance Bounds**: a single click MUST start its save request within 1,000 ms; a Set all MUST issue exactly 1 save request.

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

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Question to Resolve |
|---|---|---|---|
| 1 | Whether a Hidden, allowed tool or skill can be found | Not at all (re-ask text "not told at all") | Spec: not listed, but ToolSearch / `Skill` search find it (R31, ADR-071 search-only). Confirm: FQ-7 |
| 2 | Two tabs editing the global map at once | Add a revision field | Out of scope; last write wins, as today. Accepted unless the grill says otherwise |
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

#### Scenario: S-05 Set all that would allow autonomous plans asks first

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

- **Given** `deep-research` at Ask globally
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
- **Then** an approval card appears naming "notes-helper" and marked unverified

#### Scenario: S-31 Trust Ask applies in God Mode

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case

- **Given** God Mode on and the setup of S-30
- **When** Mia loads `notes-helper` in a chat
- **Then** an approval card appears (FQ-2)

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
- **When** a client sends trust `warn_unverified`
- **Then** the answer is 400 with "Use allow, ask or deny." and trust stays Ask

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

- **Given** "Load tools on demand" on, Mia allowed `serve_web`, and `serve_web` at `<level>`
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

#### Scenario: S-41 Upgrade keeps what agents are sent

**Traces to**: User Story 5, Acceptance Scenario 7
**Category**: Edge Case

- **Given** an install from before this release, with service `context7` stored without `deferred` and service `macos` stored with `deferred: false`
- **When** Omnipus starts on this release
- **Then** `read_file` shows Full, `serve_web` Name and short description, `browser_evaluate` Hidden, every `context7` tool Hidden, every `macos` tool Full, and every skill Name and short description

#### Scenario: S-42 A Full skill is in context from the first turn

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Alternate Path

- **Given** `plan` at Full and granted to Mia
- **When** a new chat with Mia starts
- **Then** the first turn's context holds the whole `plan` instructions without a `Skill` call

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

#### Scenario: S-44 Load tools on demand off is explained

**Traces to**: User Story 5, Acceptance Scenario 9
**Category**: Alternate Path

- **Given** "Load tools on demand" off in Settings → Performance
- **When** the Omnipus tools tab opens
- **Then** the note "Load tools on demand is off in Settings → Performance, so every allowed tool is sent in full. These levels apply when it is on." is shown with a link

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
- **When** the person uses "Set 1 matching" → Allow in All skills
- **Then** exactly 1 skill changes to Allow
- **And** the control's accessible name starts "Set 1 matching"

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

- **Given** no service `notion`
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
- **Then** the intro reads "Open Settings → Protection to change Auto-approve and God Mode." with a link, the Shell group links "Shell safety rules" to Settings → Sandbox & network
- **But** no link points to Settings → Security for permissions

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
| ST-06 | US-2 AS7, US-5 AS7 | Upgrade a saved pre-release data folder; open the page and two agents | permissions and levels as in S-16 and S-41 | migration | compare listed values to the saved config | stable with fixture |
| ST-07 | US-3 AS1–3 | Open Mia → Tools | no presets; Skills group with granted skills; Allow disabled where global Ask | agent editor | open Jim and a custom agent too | stable |
| ST-08 | US-3 AS5 | Omnipus → Tools → a skill → Ask | refused with the Omnipus text | Omnipus guard | Deny is accepted | stable |
| ST-09 | US-4 AS2–3 | Trust Deny; upload an unverified zip; ask Omnipus to install an unverified catalog skill | both refused; nothing installed | trust at install | list skills folder afterwards | stable |
| ST-10 | US-4 AS4 | Trust Ask, Auto-approve on; Mia loads an unverified granted skill | card naming the skill as unverified | trust at load | repeat with God Mode on | stable |
| ST-11 | US-4 AS6 | Start with stored `warn_unverified` | trust shows Ask; audit has one entry | trust migration | read audit log | stable with fixture |
| ST-12 | US-5 AS2–4 | Set `serve_web` to each level; inspect the next request Mia sends (debug request log) | matches S-38 | manifest | ToolSearch finds Hidden | needs request capture |
| ST-13 | US-5 AS8 | `plan` Full; new chat with Mia; ask what the plan skill says without loading | answers from the instructions with no `Skill` call in the transcript | skill menu / Full | check transcript for no `Skill` call | model-dependent, keep manual |
| ST-14 | US-6 AS1–5 | Open page; search "browser"; Escape; "zzzz" | as S-45 to S-49 | page | same on phone width | stable |
| ST-15 | US-7 AS2–3 | Write with Omnipus online and offline | chat opens with "Write a new skill" and `write-skill` loads; offline warning, nothing sent | workflow start | offline via disabled network | stable |
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
| 8 | TestPutToolPolicies_SkillAndVisibilityMembers | Integration Go | S-01, S-09, S-39 | Dataset D6 |
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
| 9 | allow, none, ask, no, on | trust survives God Mode | ask | BDD Scenario: S-31 | FQ-2 |
| 10 | allow, ask, ask, yes, on | per-agent survives God Mode | ask | BDD Scenario: S-19 | as for tools |
| 11 | allow, none, deny, yes (built-in), off | built-in verified | allow | BDD Scenario: S-35 | |
| 12 | deny, allow, allow, yes, on | God Mode floors global deny | allow | BDD Scenario: S-76 | same rule as tools today |
| 13 | missing, none, ask, yes, off | no entry either side | deny + Error log | BDD Scenario: S-17 | tripwire, never after self-heal |
| 14 | allow, none, ask, yes, not granted | grant first | refused: not granted | BDD Scenario: S-14 | |
| 15 | upgrade: Skill global ask, agent none | upgrade | skill global ask | BDD Scenario: S-16 | |
| 16 | upgrade: Skill global allow, agent deny | upgrade | skill global allow, agent deny (granted skills) | BDD Scenario: S-16 | |

#### Dataset D2: Tool level → what the agent is sent (Load tools on demand on unless stated)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | full, allowed | top level | definition in tool list | BDD Scenario: S-38 | |
| 2 | name_and_description, description 300 chars | long text | one line, description cut to 140 characters | BDD Scenario: S-38 | C-1 |
| 3 | name_and_description, empty description | empty | line with the name only | BDD Scenario: S-38 | |
| 4 | name_only | middle | name only, no description | BDD Scenario: S-38 | |
| 5 | hidden, ToolSearch by keyword | bottom | not listed; found and loaded | BDD Scenario: S-38 | |
| 6 | full, effective deny | deny wins | not listed, not found | BDD Scenario: S-40 | |
| 7 | hidden, Load tools on demand off | switch off | definition in tool list | BDD Scenario: S-44 | |
| 8 | locked tool, PUT hidden | locked | 400, nothing saved | BDD Scenario: S-39 | |

#### Dataset D3: Skill trust values

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | stored `block_unverified` | legacy | deny | BDD Scenario: S-33 | |
| 2 | stored `warn_unverified` | legacy | ask | BDD Scenario: S-33 | |
| 3 | stored `allow_all` | legacy | allow | BDD Scenario: S-33 | |
| 4 | stored empty | empty | ask | BDD Scenario: S-33 | |
| 5 | PUT `deny` | valid | 200, deny | BDD Scenario: S-27 | |
| 6 | PUT `warn_unverified` | legacy on wire | 400 "Use allow, ask or deny." | BDD Scenario: S-36 | |
| 7 | PUT `Deny` | wrong case | 400 | BDD Scenario: S-36 | case-sensitive as today |
| 8 | PUT `allow` | warning | 200 with warning text | BDD Scenario: S-32 | C-3 |

#### Dataset D4: Set all scope and count

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | no search, no filter, group of 12 | whole group | 12 change, label "Set all 12" | BDD Scenario: S-02 | |
| 2 | Permission filter Deny, 1 match of 53 | filter | 1 changes, "Set 1 matching" | BDD Scenario: S-48 | |
| 3 | search "zzzz", 0 matches | empty | control disabled | BDD Scenario: S-49 | |
| 4 | group with Tool search, Set all Deny | fixed row | all but Tool search change | BDD Scenario: S-06 | |
| 5 | group with execute_plan, Set all Allow | confirm | confirm first; Cancel → 0 change | BDD Scenario: S-05 | |
| 6 | level Set all, group with a locked tool | locked row | locked row unchanged | BDD Scenario: S-39 | |

#### Dataset D5: Install category

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | screen, no choice | missing | "Choose a category.", nothing installed | BDD Scenario: S-55 | |
| 2 | file `metadata.category: research` | from file | Research preselected | BDD Scenario: S-56 | |
| 3 | file `metadata.category: Games` | not in list | no preselection; person must choose | BDD Scenario: S-56 | |
| 4 | REST without category, no metadata | absent | uncategorised | BDD Scenario: S-57 | C-5 |
| 5 | `install_skill` `category: games` | invalid | refused naming allowed values | BDD Scenario: S-72 | |
| 6 | `install_skill` `category: lifestyle` | valid | Lifestyle | BDD Scenario: S-71 | |

#### Dataset D6: `PUT /security/tool-policies` validation

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `skill_policies: {plan: deny}` | valid | 200; only `plan` changed | BDD Scenario: S-09 | merge |
| 2 | `skill_policies: {nope: deny}` | unknown key | 400, nothing saved | BDD Scenario: S-07 | |
| 3 | `skill_policies: {}` | empty | 200, nothing changed | BDD Scenario: S-09 | merge never deletes |
| 4 | no `skill_policies` member | omitted | stored values unchanged | BDD Scenario: S-01 | |
| 5 | `visibility: {AskUserQuestion: hidden}` | locked | 400 lock text | BDD Scenario: S-39 | |
| 6 | `policies: {ToolSearch: deny}` | fixed | 400 "has no permission of its own" | BDD Scenario: S-06 | |
| 7 | `skill_visibility: {plan: full}` | valid | 200 | BDD Scenario: S-42 | |
| 8 | `skill_policies: {PLAN: ask}` | letter case | 200, applies to `plan` | BDD Scenario: S-09 | skill identity ignores case |

#### Dataset D7: Starting levels at upgrade

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `read_file` (in the 36) | full list | full | BDD Scenario: S-41 | |
| 2 | `serve_web` | previewed | name_and_description | BDD Scenario: S-41 | |
| 3 | `browser_evaluate` | search-only | hidden | BDD Scenario: S-41 | |
| 4 | service without `deferred` | default | hidden | BDD Scenario: S-41 | |
| 5 | service `deferred: false` | explicit | full | BDD Scenario: S-41 | |
| 6 | `preview_all_lazy: true`, `browser_evaluate` | old switch | name_and_description | BDD Scenario: S-41 | |
| 7 | every skill | skills | name_and_description | BDD Scenario: S-41 | |

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

---

## Functional Requirements

### Page, search and saving (US-1, US-6)
- **FR-001**: The Skills & Tools page MUST show three tabs with counts — Skills, External tools (MCP), Omnipus tools — opening on Skills.
- **FR-002**: Each tab MUST show one search field and two flat filters (Permission; What the model sees) on one line, above one grouped list whose groups start collapsed; a search MUST open groups with matches; Escape and the clear button MUST empty it.
- **FR-003**: A group's Set all MUST change exactly the rows on screen (search and both filters applied), in one save, with the count in its label and accessible name; Tool search, Skill and locked levels MUST be skipped where they have no value to set.
- **FR-004**: An empty result MUST show "No <items> match “<text>”" with "Clear search and filters".
- **FR-005**: A row whose saved value no longer matches the active filter MUST leave the list.
- **FR-006**: With a "What the model sees" filter on, the page MUST show the denied-items note.
- **FR-007**: Every value on this page (permission, level, trust, a service's switch) MUST save on its own within 1,000 ms of the click and show "Saved"; the page MUST NOT show a Save bar, Discard, a leave prompt or a Set all confirm.
- **FR-008**: A refused save MUST show "Not saved: <server reason>." with Retry, and the rows MUST show the stored values.
- **FR-009**: A 503 save MUST show "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." with Retry.
- **FR-010**: Any change, single or Set all, that would make `execute_plan` resolve to Allow MUST open the existing autonomous-plan confirm first.
- **FR-011**: Every tool row MUST show exactly one stored value; "Unset" MUST NOT appear.
- **FR-012**: Tool search and Skill MUST show no permission badges (C-6 `permission_fixed`), with their fixed texts; the server MUST refuse any value but `allow` for them.

### Per-skill permissions (US-2)
- **FR-013**: The server MUST store one global permission per installed skill and serve and accept it through `GlobalToolPolicies.skill_policies` (C-2), merging on PUT.
- **FR-014**: A skill load MUST resolve to the strictest of the global skill value, the agent's own value and, for an unverified skill, the trust value; God Mode MUST floor only the global skill value (C-0, C-3; Dataset D1).
- **FR-015**: The grant check MUST run before the permission check and keep its current refusal.
- **FR-016**: An Ask load in an interactive chain MUST raise the normal approval card with the C-9 `skill` object; Always Allow MUST remember it for the chat.
- **FR-017**: An Ask load in an unattended chain MUST be refused with "Not run: needs approval, and task and plan runs never ask." and no card.
- **FR-018**: A skill whose effective value is Deny for an agent MUST NOT appear in that agent's skill menu or `Skill` search results.
- **FR-019**: The first start on this release MUST set every skill's global value to the old global `Skill` value and copy each agent's own old `Skill` value to each skill granted to that agent at that moment; then `Skill`'s own value is fixed at `allow`.
- **FR-020**: A skill with no stored global value or level MUST get Allow (FQ-3) and `name_and_description`, additively, never overwriting a stored value.
- **FR-021**: The server MUST store sparse per-agent skill overrides and accept them through `AgentToolsUpdateRequest.skills` (C-4), reporting `AgentToolsResponse.skills`.

### Agent Tools tab (US-3)
- **FR-022**: No agent's Tools tab and no other screen MUST show Cautious / Balanced / Full access; the presets module MUST be removed.
- **FR-023**: In an agent's Tools tab, badges looser than the global value MUST be disabled with the visible reason "Global setting is <value>; an agent can only be stricter."
- **FR-024**: An agent's Tools tab MUST show a Skills group of its granted skills with Allow / Ask / Deny and Inherit, or "No skills granted. Grant skills on this agent's Skills tab."
- **FR-025**: A per-agent Ask on a skill for Omnipus MUST be refused with the existing Omnipus text and code (422).
- **FR-026**: Judge and Plan Supervisor MUST stay read-only and external workers MUST keep their notice, for skills as for tools.

### Skill trust (US-4)
- **FR-027**: Trust MUST use `allow` / `ask` / `deny` on the wire and in storage (C-3); legacy stored values MUST be rewritten once on load (Dataset D3) with one audit entry; legacy values on PUT MUST be refused with 400 "Use allow, ask or deny."
- **FR-028**: The Skills tab MUST open with the "Unverified skills" block: Allow, Ask, Deny badges in that order, all three meanings visible, and the Allow warning callout fully visible after choosing Allow, also at 390 px.
- **FR-029**: With trust Deny, every install path (screen, `POST /skills/install`, `install_skill`) MUST refuse an unverified skill with the C-3 text and leave nothing installed; the install review MUST disable Install with its own text.
- **FR-030**: With trust Ask, an unverified skill's load MUST be at least Ask, including under Auto-approve and God Mode (FQ-2); with trust Deny it MUST be refused with the C-3 load text.
- **FR-031**: `verified` MUST be true only for built-in skills and for registry installs whose hash matched and whose folder has no local changes since (C-5); the install result MUST be stored outside the skill folder.
- **FR-032**: Every trust change MUST keep its existing audit entry with old and new value.

### What the model sees (US-5)
- **FR-033**: Every tool and skill row MUST offer Hidden, Name only, Name and short description, Full, with a one-line explanation per type (C-1).
- **FR-034**: At the start of a turn, each allowed tool MUST be presented per its level (C-1; Dataset D2).
- **FR-035**: Tool search, Skill, Ask the user, goal_claim and set_goal MUST have fixed levels with the visible lock reason; the server MUST refuse a change (400).
- **FR-036**: An item with effective Deny for an agent MUST NOT be listed or found for that agent, whatever its level.
- **FR-037**: The first start on this release MUST set starting levels per C-6 (Dataset D7) so that no agent's request changes; `MCPServerConfig.deferred` and `preview_all_lazy` MUST no longer be read after migration, with one load warning if present.
- **FR-038**: Each granted, not-denied skill MUST be presented per its level (C-1), including Full in context from the first turn (FQ-1).
- **FR-039**: With "Load tools on demand" off, both tool tabs MUST show the C-6 note; every allowed tool is sent in full.
- **FR-040**: Every change of a skill permission, a tool level or a skill level MUST write one audit entry per changed key with old and new value (C-2).

### Adding skills (US-7)
- **FR-041**: New skill MUST offer "Write with Omnipus", "Upload a skill", "Browse the catalog"; "Write with Omnipus" MUST open the Omnipus chat and send "Write a new skill" with `workflow_start {workflow: W5}`; offline it MUST send nothing and show the C-8-pattern warning.
- **FR-042**: Every install (catalog and file) MUST pass a review showing name, version, verified or not, the trust value, and a required Category; `metadata.category` MUST preselect when it is a valid value.
- **FR-043**: Each skill's category MUST be stored outside its folder (C-5) and the Skills tab MUST group by category, hiding empty ones, with Uncategorised for the rest.
- **FR-044**: Each skill row MUST offer More actions: Make a copy; Remove only when not built-in.

### External services (US-8)
- **FR-045**: Adding a service (screen or `add_mcp_server`) MUST store Ask for all its current and future tools (C-7); the one-time upgrade MUST store a value for every service without one (FQ-6).
- **FR-046**: Each service group header MUST carry the switch (acts at once), Test, Edit, Remove; a failing service MUST show its reason with Retry and Edit.
- **FR-047**: Remove MUST confirm with "Remove this service? Agents that use it lose its tools."; the local-program confirm MUST keep the safe choice as the main, focused button.

### Tool settings and links (US-9)
- **FR-048**: The Web & Search group MUST have a Settings button opening the existing search-provider settings in a side panel, behaviour unchanged.
- **FR-049**: The Shell group MUST link to Settings → Sandbox & network; the Omnipus tools intro MUST link to Settings → Protection; no link MUST point to Settings → Security for permissions.

### States, phone, keyboard, wording (US-10)
- **FR-050**: Each tab MUST show the loading, empty, error and partial states in "UI Screens and States"; no write MUST be possible while a tab is loading or failed.
- **FR-051**: At 390 × 844 every control MUST be at least 44 × 44 px and the page MUST NOT scroll sideways; rows MUST become cards.
- **FR-052**: Keyboard and screen-reader behaviour MUST follow "Accessibility and Keyboard".
- **FR-053**: No on-screen text MUST name a retired agent, an issue number, a requirement tag or "contact support".

### Agent tool parity (US-11)
- **FR-054**: `set_agent_permissions` MUST accept `skill_policy_changes` (`set`/`remove`) and report clamps like tool changes.
- **FR-055**: `get_agent` MUST return `skill_permissions` for `fields: ["skill_permissions"]`.
- **FR-056**: `install_skill` MUST accept an optional `category` from `SkillCategory` and refuse other values naming the allowed ones.
- **FR-057**: `set_config` MUST refuse `sandbox.skill_policies`, `sandbox.tool_visibility` and `sandbox.skill_visibility` with the existing sandbox reason.

### Riders (US-12)
- **FR-058**: (#1920) No built-in `SKILL.md` MUST name Ava or Admin as an agent; the retired-agent guard SHOULD also scan embedded skill text.
- **FR-059**: (#1297) Effective values shown in the agent's Tools tab and returned by `get_agent` MUST include God Mode.
- **FR-060**: (#1298) An agent's Tools tab MUST list the tools of services assigned to it.
- **FR-061**: (#1411) Reopening an agent's editor MUST show the saved tool values without a page reload.
- **FR-062**: (#99) `omnipus doctor` MUST warn when trust is Allow.
- **FR-063**: (#1570) One unreadable skill MUST NOT fail the whole skill list; it shows "Cannot load" with its reason.
- **FR-064**: (#1576) A copy MUST show its own name, not the source's display name.

### Dependency
- **FR-065**: This package MUST land before the Settings package is cut (Settings FR-060); at its landing the route MUST render the tool permission grid, web search settings and skill trust.

## Success Criteria

- **SC-001**: All 81 BDD scenarios pass in RED → GREEN, with the RED logs saved.
- **SC-002**: Hands-on rows ST-01 to ST-18 PASS on the cut commit, validated independently.
- **SC-003**: After upgrading a saved pre-release data folder, the request each of 3 sample agents sends on its first turn is identical, tool by tool and skill by skill, to the request before the upgrade (ST-06).
- **SC-004**: `grep -r "Cautious\|toolPolicyPresets" src/` returns 0 matches in production code.
- **SC-005**: Every install path refuses an unverified skill under trust Deny (3 of 3 paths, ST-09).

---

## Reachability

- **Tool registration**: no new tool. Changed tools: `set_agent_permissions` (`skill_policy_changes`), `get_agent` (`skill_permissions`), `install_skill` (`category`), `add_mcp_server` (Ask key). Their policy entries already exist in `pkg/config/defaults.go` and the agent seeds (`pkg/coreagent/seed.go`); `grep -rl '"set_agent_permissions"' pkg/coreagent/ pkg/config/ pkg/tools/` must stay non-zero. The `Skill` tool's global value becomes fixed `allow`; per-skill values live in `sandbox.skill_policies` (global) and each agent's `tools_cfg.skills.policies` (per agent).
- **Screen**: `/skills` route (`src/routes/_app/skills.tsx`), sidebar "Skills & Tools" — the rewritten Skills & Tools screen; each agent's profile → Tools tab (`ToolsAndPermissions`); the Omnipus chat for W5.
- **Test plan execution**: RED (qa-lead) writes and runs the tests in "Test Implementation Order" against the pre-change code and saves the failing logs; GREEN (backend-lead and frontend-lead) makes them pass; CHECK audits integrity; the hands-on rows run on the cut commit by uat-tester and uat-validator.

## User-facing documentation

| TODO | UI / behaviour change | User-facing page | Matching update |
|---|---|---|---|
| DOC-001 | Global tool permissions move to Skills & Tools; autosave; Set all; no presets; no Unset | `docs/tools.md` | Rewrite "How to change an agent's tool access" (drop preset step 3), the "installation-wide setting" paragraph (now Skills & Tools → Omnipus tools / External tools), "Tools from connected servers" (new tab names, Add service, tools start at Ask) |
| DOC-002 | Per-skill Allow / Ask / Deny; Skill tool no longer one switch; grants stay | `docs/skills.md` | New section "Allow, ask or deny a skill"; table row for `Skill` in `docs/tools.md` defaults updated |
| DOC-003 | What the model sees: four levels | `docs/tools.md`, `docs/skills.md` | New section on the levels, what each means for a tool and a skill, locked tools, and the "Load tools on demand" interplay |
| DOC-004 | Skill trust words and enforcement; Warn becomes Ask | `docs/skills.md` (line with Block/Warn/Allow all), `docs/security.md` | New words, where it applies (install and load), what changed on upgrade, unattended runs refuse |
| DOC-005 | New skill menu; install review with category; categories | `docs/skills.md` "How to add a skill and grant it" | New steps and tab names |
| DOC-006 | Agent Tools tab: Skills group, stricter only, no presets | `docs/agents.md`, `docs/tools.md` | Steps and screenshots text |
| DOC-007 | Agent tools gain skill fields | `docs/tools.md` (get_agent paragraph), `docs/skills.md` | `skill_permissions`, `skill_policy_changes`, `install_skill` category |
| DOC-008 | Web & Search settings live on Skills & Tools | `docs/tools.md` ("Web search works without setup" bullet) | Point to Omnipus tools → Web & Search → Settings |
| DOC-009 | Settings no longer holds tool permissions or trust | `docs/settings.md` | Coordinated with the Settings package DOC-007; this package owns the Skills & Tools side |
| ADR-TODO-1 | Full skill level reverses ADR-072 OQ3 | `docs/internal/architecture/ADR-072-skill-activation-and-loading.md` | Dated correction by the architect after FQ-1 is answered |
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
| FR-012 | US-1, US-2 | S-06, S-15 | SkillsToolsPage.setAll.test; TestPutToolPolicies_SkillAndVisibilityMembers | DOC-002 |
| FR-013 | US-2 | S-09 | TestPutToolPolicies_SkillAndVisibilityMembers | DOC-002 |
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
| FR-035 | US-5 | S-39 | SkillsToolsPage.levels.test; TestPutToolPolicies_SkillAndVisibilityMembers | DOC-003 |
| FR-036 | US-5 | S-40 | TestManifest_LevelsShapeTurn | DOC-003 |
| FR-037 | US-5 | S-41 | TestVisibilityStartingLevels_Upgrade | DOC-003 |
| FR-038 | US-5 | S-42, S-43 | TestSkillsSummary_Levels | DOC-003 |
| FR-039 | US-5 | S-44 | SkillsToolsPage.levels.test; TestManifest_LevelsShapeTurn | DOC-003 |
| FR-040 | US-5 | S-09, S-37 | TestPutToolPolicies_SkillAndVisibilityMembers | none — audit log content has no user page beyond `docs/security.md` audit section, covered by DOC-004 |
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
| FR-052 | US-10 | S-69 | skills-tools.spec.ts | none — keyboard follows product-wide conventions |
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
| FR-065 | — | S-64, S-26, S-01 | cut checklist | DOC-009 |

**Completeness check**: every FR appears with at least one scenario and one test; scenarios S-01 to S-81 each appear at least once (S-03 in FR-001's page test set via skills-tools.spec.ts row 31; S-20, S-57, S-58, S-60 to S-65, S-68, S-69 through FR-024 and FR-043 to FR-052).

---

## Riders

Search: `gh issue list --repo elicify-ai/omnipus --search "skills OR tools in:title" --state open` (exit 0, 74 issues), plus the design notes' #1920. The chief confirms before the design is final.

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
- ToolSearch keeps searching tools only; a Hidden skill is found through `Skill` search (ADR-072 D1) and the person's `/name`. R31's "one search for both" is not in this package.
- Skill categories for built-ins (FQ-5), proposal from R32 Appendix B mapped to today's 29 skills: Tools — add-connector, add-provider, capability-plan, change-agent, connect-channel, create-agent, dependency-check, disable-channel, doctor-usage, inbox-triage, install-skill, provider-config, remove-agent, remove-connector, shell-diagnosis, team-delegation, team-setup, welcome-workspace, write-skill; Business — interview, define-goal, plan, orchestrate; Research — deep-research; Testing & Security — verify; Content & Media — elicify-docx, elicify-pdf, elicify-pptx, elicify-xlsx.
- Project-folder (mount) skills are not listed on the Skills tab (#1380 not a rider by default); their permission and level use the C-2 defaults.
- The Settings package removes the controls from Settings; this package does not edit Settings files.
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

## Questions for the founder

Each needs a one-line answer, e.g. "FQ-1 A, FQ-2 A". The spec is written with the recommendation (marked) so work can continue.

**FQ-1 — Does "Full" for a skill mean its whole instructions in every chat?** Context: you chose four levels that work almost the same for tools and skills. For a tool, Full means the agent can use it at once. For a skill, the matching meaning is: its whole instructions are loaded at the start of every chat. In September you decided the opposite (ADR-072: "no always-on skill", because a skill loaded every time costs tokens on every message). Impact: A reverses that decision for skills set to Full only. A **(recommended)** — yes, Full loads the whole instructions every chat; we correct ADR-072. B — Full for a skill means name, description and its extra details (no instructions), so no skill is ever always on.

**FQ-2 — Does trust "Ask" still ask in God Mode and under Auto-approve?** Context: God Mode lifts global Ask for tools; Auto-approve skips safe Ask calls. Trust protects against unreviewed skill files. A **(recommended)** — yes, an unverified skill always asks (like the one tool that always asks today). B — God Mode and Auto-approve treat it like any other Ask.

**FQ-3 — What permission does a newly installed skill start with?** Context: a new skill does nothing until you grant it to an agent; unverified ones are also covered by trust. A **(recommended)** — Allow, like the `Skill` tool today. B — Ask, like new external services.

**FQ-4 — "Load tools on demand" stays in Settings → Performance (the approved Settings spec keeps it), but your requirement R30a said to remove it.** When it is off, every allowed tool is sent in full and the levels have no effect for tools. A **(recommended)** — keep it; the tool tabs show a note when it is off. B — remove it in this build (changes the Settings package).

**FQ-5 — Categories of the 29 built-in skills.** The October proposal named 22 older skills. The mapping in Assumptions is my proposal for today's 29. A **(recommended)** — use it. B — you change rows.

**FQ-6 — External tools that never had a permission.** Today such a tool is blocked for every agent (Omnipus treats "no value" as Deny). Your R30b said to set them to Ask on upgrade, which would let assigned agents use them after asking. A — Ask, as R30b says. B **(recommended)** — Deny, so nothing an agent can do changes on upgrade; you set Ask where you want it.

**FQ-7 — "Hidden": can the agent still find it by searching?** Your re-ask text said "not told at all"; R31 and today's search-only tools mean "not listed, but found by search". A **(recommended)** — not listed, found by search (keeps today's 70+ search-only tools working). B — never found; only you can name it.
