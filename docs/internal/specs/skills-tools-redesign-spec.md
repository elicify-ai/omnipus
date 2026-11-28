Status: In review

ADR: none — no open design decision was routed to an ADR. The founder settled the design in the wireframe answers (T-Q1 to T-Q7, T-Q4b). The contract shapes below are architect decisions recorded in "Contract Changes". Two existing ADRs need a dated correction because of founder answers: [ADR-072 — Skill activation and loading: an on-demand `Skill` tool, the per-agent grant as the real gate, and a project's own skills and instructions](../architecture/ADR-072-skill-activation-and-loading.md) (OQ3 "no always-on skill" is reversed by the T-Q4b "Full" level, confirmed by ST-Q1 A) and [ADR-071 — Tool manifest tier redesign](../architecture/ADR-071-tool-manifest-tier-redesign.md) (the fixed tier lists become stored, editable levels). Both corrections are ADR-TODO rows in "User-facing documentation".

# Feature Specification: Skills & Tools redesign (page F)

**Created**: 2026-11-21
**Input**: founder-approved wireframe and notes (`/workspace/omnipus-coordination/handoffs/ui-redesign/skills-tools-wireframe/`: `design-notes.md`, `questions.md`, `re-ask-2026-11-20.md`, `review-response.md`, `index.html`, `app.js`); inventory of today's page (`/workspace/logs/ui-1519/skills-tools/inventory.md`, read at `7faff52915`); UX review (`/workspace/logs/ui-1519/skills-tools/ux-review.md`); requirements R30–R39 (`/workspace/omnipus-coordination/handoffs/ui-redesign/navigation-menu-wireframe/requirements.md`); Settings S-Q8 and `docs/internal/specs/settings-redesign-spec.md` FR-060 on branch `landing/settings-1519` (`30e1050627`). Founder answers, final: T-Q1 A (with the note that Allow / Ask / Deny look and work identically for tools and skills, on this page and in each agent's own settings), T-Q2 A, T-Q3 B, T-Q4b B, T-Q5 (Omnipus chat), T-Q6 A, T-Q7 (this page sets the global default; the agent level is stricter only, reusing today's tool-policy model; no per-agent "what the model sees"). No interview-me output file exists; the wireframe answers take its place.
**Round 1**: fixed after grill round 1 (BLOCK, 4 CRIT, 15 MAJ, 1 MIN); see "Round-1 fix log" at the end. The founder answered ST-Q1 to ST-Q7 on 2026-11-21 ("ST-Q1 A, ST-Q2 A, ST-Q3 B, ST-Q4 B, ST-Q5 A, ST-Q6 A, ST-Q7 A", Discord #team-b, 05:56Z, relayed by team-lead); every answer is written into the spec below. The founder answered ST-Q8 to ST-Q10 the same day ("ST-Q8 A, ST-Q9 A, ST-Q10 A", Discord #team-b, 13:19Z, relayed by team-lead); they are written in too.
**Round 2**: corrected after grill round 2 (BLOCK, 2 CRITICAL, 8 MAJOR; report `docs/internal/specs/skills-tools-redesign-spec-review-round2.md`, untracked); see "Round-2 fix log". The two founder questions of round 2 are answered (2026-11-22, Discord #team-b, 03:34–03:55Z, relayed by team-lead): **R2-Q1** — no card offers "Always Allow" or "Allow everywhere" any more, for tools and skills alike; cards offer Approve Once, Allow for 1 hour / 3 hours / 1 day, and Deny; a timed allowance belongs to the agent, lives in memory only and ends at its time or at a restart (C-26, the founder page's Q3 B). **R2-Q2 A** — a new, already-checked skill at Ask is settled by Auto-approve like any Ask; the trust check still asks for unchecked skills (C-12). There was no third grill round.
**Code baseline**: release `4e34afa416`. Every `file::symbol` and line below was read at that commit, except C-22, which reads the Settings landing branch `landing/settings-1519` at `8e5848c951` (Settings lands first; this package is rebased on top of it). GitNexus MCP tools were not available in this session; discovery used `grep`/`Read` (impact rows are therefore Inferred).
**Epic**: work package #1963 (Skills & Tools page F) under epic #1519, as the current commission and issue #1963 state (grill round 2 read the issue with `gh issue view 1963`: open, "lands after Settings"). Its Stage and Horizon values were not checked in this round: **Unknown**.
**Riders** (proposed by the architect; the chief confirms before the design is final): IN — #1920, #1297, #1298, #1411, #99, #1570, #1576. FOR THE CHIEF TO DECIDE — #817 item 1, #1063, #708, #1031, #1380. Details: "Riders" near the end.
**Agent tool field table**: see "Agent tool parity" under Contract Changes (C-10). Objects changed: the agent (per-agent skill permission) and the skill (global permission, what the model sees, category). Global settings (skill trust, tool and skill permission ceilings, levels) stay operator-only, refused to `set_config` for the reason already recorded in `pkg/sysagent/tools/config.go::blockedConfigKeys` ("sandbox").

---

## Overview

Today, what an agent may use is decided in three places. The Skills & Tools page only lists skills, connected services and built-in tools; it points to Settings → Security for the global Allow / Ask / Deny of every tool, and Settings also holds the skill trust choice. Each agent's own Tools tab holds that agent's stricter values and three role presets. Skills have no Allow / Ask / Deny of their own: one tool, `Skill`, is a single switch for every skill. How much the model is told about each tool is fixed in code, and the per-skill level does not exist.

This package makes Skills & Tools the one place for the global answer to "may this run, and what does the model see about it", for Omnipus tools, external (MCP) tools and skills. Every tool and every skill gets the same Allow / Ask / Deny control, here and in each agent's Tools tab, where an agent can only be stricter (founder T-Q1, T-Q2, T-Q7). Skill trust becomes the same three words for unverified skills (T-Q1 A). Every tool and skill gets one of four "What the model sees" levels: Hidden, Name only, Name and short description, Full (T-Q4b B), set globally only (T-Q7). Every change saves on its own, with no Save button and no confirm, including "Set all" (T-Q3 B) — except the one existing warning before autonomous plan execution is allowed (ST-Q7 A). The three role presets go away on this page and on every agent's Tools tab (T-Q6 A). The New skill menu opens the Omnipus chat to write a skill (T-Q5), and an install asks for a category (R32).

Settings (page G) lands first; this package lands after it, rebased on top (issue #1963). On the Settings branch (`8e5848c951`) the new layout no longer shows the global tool grid or skill trust (Protection shows a "moved to Skills & Tools" notice), still shows web search under Providers, and still has "Load tools on demand" in Limits. This package brings the grid and trust back on this page, moves the Web & Search provider settings here as a side panel (R30a), and removes the loading switch (ST-Q4 B), all in one landing (C-22). Both packages ship in the same release.

## Where this spec departs from the wireframe (answers win)

| Wireframe draws | This spec | Why |
|---|---|---|
| A Save bar (Save changes / Discard, "Leave without saving?") and a confirm before a page-wide or 10+-row Set all (T-Q3 D, mapping N19–N21, N26) | No Save bar, no Discard, no leave prompt, no Set all confirm. Every value saves on its own a moment after the click, with a "Saved" signal. The only dialog left is the existing autonomous-plan warning (FR-010) (ST-Q7 A) | T-Q3 **B**: autosave everywhere, no confirm; a wrong Set all is undone by hand |
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
| `src/components/settings/SkillTrustSection.tsx`, `SecuritySection.tsx::GlobalToolPoliciesSection`, `WebSearchGroup.tsx`, `PerformanceSection.tsx` | modifies or deletes on the Settings base (C-22) | at `8e5848c951` the first two render only in `SecuritySection`'s unused legacy layout; this package deletes them with the legacy endpoints, moves `WebSearchGroup` to this page and removes the loading switch from `PerformanceSection` |
| `src/store/omnipusChat.ts::openOmnipusChat`, `src/hooks/useOpenOmnipus.ts::useOpenOmnipus` | reused | `omnipusChat.ts:56`; workflow start W1/W18 today; `pkg/coreagent/prompts_adr090.go:102` routes `write-skill` as W5 |
| `pkg/skills/embedded/orchestrate/SKILL.md` | modifies (rider #1920) | line 18 names the retired Ava and Admin agents |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| Skill policy resolution added beside `resolveEffectivePolicyWith` | **HIGH** — every skill load of every agent | `SkillTool.execLoad`, `/skill` command path (`pkg/agent/loop_slash.go::applyExplicitSkillCommand`), `delegate` `requested_skill` (`pkg/agent/subturn_identity.go::resolveRequestedSkillForChild`), file tools and helpers (C-11 E6, E7) | every chat, task, plan and heartbeat turn that loads a skill |
| `BuildSkillsSummaryFuncWithProject` | **HIGH** — the cached system prompt of every agent | `pkg/agent/context.go:~797` "# Skills" section (3 call sites, 2 files) | prompt-cache prefix of every turn (ADR-072 D1, D8) |
| `manifest.go` tier lists → stored levels | **HIGH** — what every agent is sent each turn | `ToolManifestTier` (13 call sites, 9 files), `buildCompressedToolDefs`, `buildToolManifestNote` | every turn's tool list; prompt cache (#750) |
| `GlobalToolPolicies` PUT | MEDIUM | `putToolPolicies`, SPA editor | running-agent reload (`503` path) |
| `AgentToolsCfg` / PUT `/agents/{id}/tools` | MEDIUM | `updateAgentTools`, `set_agent_permissions`, `get_agent` | agent store, agent reload |
| Skill trust enforcement at install and load | **HIGH** — security-relevant, focus area | REST install (`rest_skills.go`), `install_skill`, `SkillTool.execLoad` | every install path; every unverified skill load |
| Skill folder authority (`pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority`, `pkg/fspolicy/homeguard.go::skillPathDenied`, kernel grant via `turnSkillFolders`) filtered by the skill outcome (C-11) | **HIGH** — security focus area `pkg/fspolicy` | file tools, `bash` helper operands (`pkg/tools/shell_skill_operands.go::loadableSkillReadOrRun`) | every turn's skill-folder access |
| One-time migrations (C-18, C-19, D7) | **HIGH** — changes stored permissions of every install | config load, `ReconcileToolPolicyCeiling` ordering | every agent after upgrade |
| Approval grant store re-keyed by agent with end times; every approval card (C-26) | **HIGH** — security focus areas `pkg/security`, `pkg/sandbox`, `pkg/audit`; every Ask tool of every agent | `checkStandingGrant`, `HandleToolApprovals`, `approvalGrantRecorder`, shell D7/D8 pre-flights, `inheritSessionPermissions`, `session_end.go`, approval card components | every Ask decision in chats and unattended runs; egress proxy tokens |
| `ToolPolicyEditor` (preset removal, Skills group) | MEDIUM | `ToolsAndPermissions.tsx`, `SecuritySection.tsx` (leaves Settings) | agent profile Tools tab |

The seven HIGH rows need sign-off before GREEN starts. They touch `pkg/agent` and the tool-policy engine, so hands-on UAT on the cut commit is mandatory (rows ST-01 to ST-33 below).

---

## Contract Changes (contract-first — Hard Constraint #8)

The architect decides the shapes; backend-lead edits `contracts/` and regenerates (`scripts/gen-contracts.sh`). Nothing below is implemented by this spec.

### C-0 How T-Q7 maps onto today's policy layers (the base)

| Founder words (T-Q7) | Today's mechanism, reused unchanged | Evidence |
|---|---|---|
| "The Skills & Tools page sets the GLOBAL default" | Layer 1, the global ceiling `sandbox.tool_policies`, complete for the static catalog, self-healed by `ReconcileToolPolicyCeiling`; written by `PATCH /security/permissions` (this page, C-2); the legacy writer `PUT /security/tool-policies` is retired in this landing (C-22) | root `CLAUDE.md` line 107 (Hard Constraint #6); `pkg/config/sandbox.go:523`; `pkg/config/validate.go:707`; `pkg/gateway/rest_tool_policies.go:68` |
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
| `full` | Full | Its complete definition is sent every turn; callable at once | Its whole instructions are in the agent's context from the start of every chat **only when the skill's outcome for that agent and chain is `allowed`**; otherwise it is listed as Name and short description (C-14) (ST-Q1 A) |
| `name_and_description` | Name and short description | Listed as one line: name and the first line of its description (at most 140 characters); loaded through ToolSearch before use | Listed in the skill menu with name and description (today's behaviour) |
| `name_only` | Name only | Listed by name alone; loaded through ToolSearch before use | Listed in the skill menu by name alone |
| `hidden` | Hidden | Not listed; ToolSearch can still find and load it (founder default, former FQ-7) | Not listed; `Skill` search, ToolSearch (C-17) (ST-Q6 A) and the person's `/name` command can still find it; loading follows C-11 |

An effective Deny for an agent overrides every level for that agent: the item is not listed, not found and not callable (today's rule, `compositor.go:471`).

### C-2 Global permissions and levels: a read model and a per-key change request (rewritten in round 1, CRIT-003, MAJ-012)

Round 0 added merge members to `GlobalToolPolicies`, whose PUT still required and replaced the whole `policies` map (`contracts/components/schemas/GlobalToolPolicies.yaml` `required: [policies]`; `pkg/gateway/rest_tool_policies.go::putToolPolicies`). A tab that changed one skill would have had to send its cached tool map and could undo another tab's Deny. Round 1 gives this page its own resource that never needs the full map.

| Schema / endpoint | New / Changed | File | Notes |
|---|---|---|---|
| `GET /security/permissions` | new | `contracts/openapi.yaml` | returns `PermissionSettings`; signed-in person (the same authentication the retired `/security/tool-policies` used) |
| `PATCH /security/permissions` | new | `contracts/openapi.yaml` | body `PermissionSettingsPatch`; 200 `PermissionSettings`; 400/403/409 `ErrorResponse`; 503 saved-not-applied (existing text) |
| `PermissionSettings` | new | `contracts/components/schemas/PermissionSettings.yaml` | complete stored maps: `policies` (tool → `ToolPolicy`, as `GlobalToolPolicies.policies`), `skill_policies` (skill key → `ToolPolicy`), `visibility` (tool name or `mcp_<server>_*` → `ModelVisibility`), `skill_visibility` (skill key → `ModelVisibility`), `skill_trust` (`allow`/`ask`/`deny`) |
| `PermissionSettings.upgrade_unfinished` | **removed** (founder rule 2026-11-28; it was added by ruling spec-gaps-1, 2026-11-25) | `contracts/components/schemas/PermissionSettings.yaml` and its copy `pkg/gateway/inboundschemas/PermissionSettings.yaml` lose the member; `contracts/components/schemas/SkillUpgradeUnfinishedAgent.yaml` and `pkg/gateway/inboundschemas/SkillUpgradeUnfinishedAgent.yaml` are deleted; the `SkillUpgradeUnfinishedAgent` component entry leaves `contracts/openapi.yaml`; regenerated `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/openapi-types.ts`, `src/lib/api/generated/schemas.ts` (one atomic commit, backend-lead) | the upgrade keeps no record of its progress, so there is nothing to serve (C-19). `PermissionSettings` keeps `additionalProperties` as it is; a PATCH body naming `upgrade_unfinished` still gets the existing 400 for an unknown member. No screen reads it (the Skills-tab notice is removed, UI Screens and States) |
| `PermissionSettingsPatch` | new | `contracts/components/schemas/PermissionSettingsPatch.yaml` | the same five members, all optional; each map names **only the keys to change** |
| `GlobalToolPolicies`, `GET/PUT /security/tool-policies` | retired by this package | `contracts/openapi.yaml`, `pkg/gateway/rest_tool_policies.go`, `pkg/gateway/inboundschemas/GlobalToolPolicies.yaml` | on the Settings base no screen writes them; the agent tab, their last reader, moves to `GET /security/permissions` (C-22 row 6) |

`PATCH` rules (the oracle for Dataset D6):

- An omitted member and an empty map change nothing. A request that names no key at all (no member, or only empty maps) → 400 "Nothing to change.". A request that names keys whose values equal the stored values is valid (round-2 fix, R2-MAJ-004): 200, nothing written, no audit entry, and the running agents are reloaded again; this is how "Retry applying" re-applies a saved-but-not-applied change (C-16).
- `null` for a member or a value → 400 naming the key; there is no delete through PATCH (keys disappear only with their skill or service).
- The whole request is validated before anything is written; one bad key → 400 naming the first bad key, nothing saved.
- Tool keys are validated as the PUT validates them today (`ValidateSubmittedToolPolicyMap`, the `mcp_` rule). Skill keys follow the one known-skill-key rule of C-23 (installed ids case-insensitive, `project:<W>/<slug>` keys); two keys in one request that are the same skill → 400 "`plan` is listed twice.".
- Per-key guards: always-ask tools cannot be `allow` (`pkg/tools/always_ask.go::AlwaysAskAllowViolation`); `ToolSearch` and `Skill` accept only `allow` (C-6); locked levels accept only their locked value (C-6); `skill_trust` accepts only `allow`/`ask`/`deny`.
- Applied under the config writer lock of C-19 (`restAPI.configMu` in the gateway, then, inside `config.UpdateConfigFile`, the path-keyed in-process mutex and the sidecar `config.json.lock` across processes; founder rule 2026-11-28, review NM-01): read the stored config inside the lock, convert it (C-19 step B), change only the named keys, write atomically, reload agents. The values the request is compared against are the converted view (`config.ReadConfigView`), the same values `GET` returned (NM-04). A key the request does not name is never written, so two tabs changing **different** keys both keep their change. Two tabs changing the **same** key: the write that commits later under the writer lock wins (commit order, not the order the responses arrive; revision 4); every response returns the complete stored `PermissionSettings`, and the page replaces its view with it, and re-reads on window focus. Result codes: 200 stored and applied; 503 stored, not applied to running agents (existing text); 500 only when nothing was stored (`ConfigWrite.Committed` false: the atomic write did not happen), body "Not saved: the settings file could not be written."; an error after the commit (the lock release, or a fault injected after the rename; a failed directory sync stays a Warn with no error, C-19 "Commit point") is logged at Error and the result stays 200 or 503 (C-19 "Commit point", revision 3 W-01); any 4xx: nothing stored.
- Audit: one `EmitSecuritySettingChange` per **changed** key with actor, old and new value, computed from the stored config inside the write lock — resources `sandbox.tool_policies.<key>`, `sandbox.skill_policies.<key>`, `sandbox.tool_visibility.<key>`, `sandbox.skill_visibility.<key>`, `sandbox.skill_trust`. A key sent with its stored value writes no entry. A refused request writes no change entry. The legacy `PUT /security/tool-policies` is retired by this package (C-22), so `PATCH` is the only writer of these maps. An audit-write failure is handled as for every security setting today (logged at Error by the audit package; the save result is unchanged) — this package does not change the audit durability of ordinary setting writes, and says so in DOC-004. The one-time trust-word conversion has its own, narrower audit rule (C-3).

Storage (architect decision): `sandbox.skill_policies`, `sandbox.tool_visibility`, `sandbox.skill_visibility` in `config.json`, beside `sandbox.tool_policies`; trust stays `sandbox.skill_trust`, now `allow`/`ask`/`deny`. Reason: the whole `sandbox` subtree is already operator-only and refused to `set_config` (`pkg/sysagent/tools/config.go::blockedConfigKeys`).

Self-heal (same pattern as `ReconcileToolPolicyCeiling`, additive, never overwriting; it runs inside the same `config.UpdateConfigFile` call as the C-19 conversion, after it, on the converted content, and is skipped while that content still holds an old-shape value (an old `Skill` value kept by the C-19 inventory rule); when the file cannot be written the heal's result exists in memory only — no done flag decides any of this (founder rule 2026-11-28, review NM-02, NM-04)): a skill key with no stored global permission gets **Ask** (ST-Q3 B) — except a built-in skill shipped by a newer Omnipus release, which gets **Allow**, its shipped default (ST-Q10 A), the way `ReconcileToolPolicyCeiling` adds a new built-in tool's shipped default — and one with no stored level gets `name_and_description`; a tool with no stored level and no matching server key gets its starting level (C-6). Removing a skill removes its two keys. Retired keys are not re-added.

### C-3 Skill trust becomes Allow / Ask / Deny

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `SkillTrustResponse`, `SkillTrustUpdateRequest`, `SkillTrustUpdateResponse`, `GET/PUT /security/skill-trust` | retired by this package | `contracts/components/schemas/SkillTrust*.yaml`, `pkg/gateway/rest_skill_trust.go`, `pkg/gateway/inboundschemas/SkillTrust*.yaml` | on the Settings base their only screen, `SkillTrustSection.tsx`, renders only in `SecuritySection`'s unused legacy layout (C-22). Trust is read and written as `PermissionSettings.skill_trust` (`allow`/`ask`/`deny`; any other value → 400 "Use allow, ask or deny."). The page itself shows the Allow warning "Allow turns off the check for unverified skills." |

Config: `sandbox.skill_trust` stores `allow`/`ask`/`deny`. A stored old word is an old-shape value: the C-19 `config.json` conversion replaces it in the same write as every other old global value: `block_unverified` → `deny`, `warn_unverified` → `ask`, `allow_all` → `allow`. An empty or absent value is not an old-shape value (a fresh install stores none) and is read as `ask` (today's empty value means `warn_unverified`, `rest_skill_trust.go:47-49`; the change is named in DOC-004). When the gateway's own start converts an old word, it writes one audit entry (`EmitSecuritySettingChange`, resource `sandbox.skill_trust`, old word, new word, actor `system`) in that start, from memory: the boot load keeps the conversion's `ConfigConversion.TrustFrom`/`TrustTo` in memory (`(*config.Config).TrustConversion() (from, to string, ok bool)`, never serialized), and `(*runContextWithOptions).auditOldTrustConversion()` (new, `pkg/gateway/gateway.go`) writes the entry at the end of `initializeAgentLoop`, once `AgentLoop.AuditLogger()` is open; a second start finds no old word and writes none. Nothing is stored to remember a pending entry, so there are **two named cases with no entry**: (1) the old word was converted by another command that loads or saves the config (for example `omnipus doctor`) before the first gateway start; (2) the gateway stopped after the conversion write and before the entry. In both, the trust value is still the exact translation of the old word (never looser), and DOC-004 names both cases. Accepted by the founder (Q-AUD1 A, 2026-11-28 14:11Z): exactly these two cases may leave no entry, and nothing else changes — every ordinary setting write keeps its audit entry as before (C-2). A durable "audit pending" record is not used, because it would be upgrade state.

Meaning (applies to **unverified** skills only, C-5):

| Trust | Install of an unverified skill (screen, REST, `install_skill`) | Load of an unverified skill by an agent |
|---|---|---|
| `allow` | proceeds | follows the skill's own permission only |
| `ask` | proceeds only with a person's review of that exact revision: the install review (screen, C-13) or a trust card bound to the staged revision (`install_skill`, C-12); `POST /skills/install` without a review → 409 | the trust check of C-12: a trust card unless that revision was approved, also under Auto-approve and in God Mode (ST-Q2 A) |
| `deny` | refused: "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed." | refused: "`<skill>` is unverified, and unverified skills are set to Deny in Skills & Tools." |
| not available (`install_skill` has no trust setting wired, `pkg/tools/skills_install.go::InstallSkillTool.SetSkillTrust` never called; spec sync, team-lead ruling R2 2026-11-28) | `install_skill` fails closed for an unverified skill only, once staged, before publication and before any card: "Skill trust is not available, so this unverified skill was not installed." (the Deny sentence would be untrue here); a verified skill still installs | — (the load path always reads trust from the loaded config) |

The trust floor is a resolution-time rule, not a stored layer (C-0). It survives God Mode, like the always-ask rule (ST-Q2 A). Its consent rules are C-12; the upgrade order is C-19.

### C-4 Per-agent skill permissions: `AgentToolsCfg`, `AgentToolsUpdateRequest`, `AgentToolsResponse`

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `AgentToolsCfg` | changed | `contracts/components/schemas/AgentToolsCfg.yaml` | adds `skills: { policies: object<skill id, ToolPolicy> }`, sparse overrides only |
| `AgentToolsUpdateRequest` | changed | `contracts/components/schemas/AgentToolsUpdateRequest.yaml` | adds optional `skills: { policies }`. The existing envelope stays (round-2 fix, R2-MAJ-007): `revision` and `override_names` remain required and keep their meaning for tools; a stale `revision` gets the existing 409. A skills-only request is `{revision, override_names: [], skills: {policies: …}}` with no `builtin`; `override_names` naming a tool the request does not supply → 400 "override_names names tools that are not in this request.". `skills` omitted = skill overrides unchanged; `skills.policies: {}` = clear every skill override; otherwise `skills.policies` is the agent's complete set of skill overrides (a key not listed is removed); `null` for `skills`, `policies` or a value → 400 naming it. Keys follow C-23 for an agent. The Omnipus per-agent Ask rule (`rest_omnipus_guards.go:57`) applies (422, existing text); locked system agent 403 and external worker 400 unchanged |
| `AgentToolsResponse` | changed | `contracts/components/schemas/AgentToolsResponse.yaml` | adds required `skills: AgentSkillEntry[]` |
| `AgentSkillEntry` | new | `contracts/components/schemas/AgentSkillEntry.yaml` | `key` (C-23: skill id or `project:<W>/<slug>`), `name` (slug), `display_name`, `granted` (bool; project skills: false, their authority is the mount), `project_workspace` (string or null), `configured_policy` (`ToolPolicy` or null = no override), `global_policy` (`ToolPolicy`), `effective_policy` (`ToolPolicy`, after trust floor and God Mode), `unverified` (bool) |

The per-agent value is stored as given (like tools today) and floored at run time; the screen locks looser badges; `set_agent_permissions` reports the clamp (C-0). Every other writer of the agent's tool settings keeps `skills.policies` when it does not name it (C-21). Project skills are keyed `project:<workspace>/<slug>` (C-19); every writer validates keys with C-23.

### C-5 Skill fields: `Skill`, `SkillInstallRequest`, `SkillCategory`

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `SkillCategory` | new | `contracts/components/schemas/SkillCategory.yaml` | enum `tools`, `business`, `development`, `testing_security`, `data_ai`, `devops`, `documentation`, `content_media`, `research`, `lifestyle`, `databases`, `blockchain`, `uncategorised` (R32's 12 SkillsMP categories) |
| `Skill` | changed | `contracts/components/schemas/Skill.yaml` | adds required `category` (`SkillCategory`); `verified` redefined: true for a built-in skill, or for a skill whose install was checked against a registry hash **and** whose folder has no local changes since; false otherwise. Adds read-only `revision` (`pkg/skills/capture.go::ContentRevision` of the installed folder) and `trust_approved` (bool: the current revision has a trust approval, C-24); both server-derived, ignored if a client sends them |
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
- `tools.manifest.compressed`, `preview_all_lazy` and `MCPServerConfig.deferred` are old-shape values: the C-19 `config.json` conversion reads them and **removes them in the same write** that stores the levels; their struct fields leave `pkg/config`, nothing reads them afterwards (ST-Q4 B), and no load warning exists; `set_config` and `PUT /config` refuse a request naming them (C-21, C-22). There is no marker (founder rule 2026-11-28). A tool or server with no stored level gets its fresh-install starting level from the self-heal (C-2): the compression-on rows of Dataset D7 (the 36 tools Full, `serve_web` Name and short description, every other static tool and every MCP server Hidden).
- `AgentToolEntry.manifest_tier` stays and is derived: `full` → `full`; `name_and_description`, `name_only`, `hidden` → `compressed`; ToolSearch → `infra`.

"Load tools on demand" (`tools.manifest.compressed`): removed in the same release (ST-Q4 B). The four levels are the only control. **Conversion, not reset** — the one-time "tool-loading conversion" (step 3 of the C-19 upgrade) reads the old `tools.manifest.compressed`, `tools.manifest.preview_all_lazy`, `tools.mcp.discovery.enabled` and each server's `deferred`, and writes the starting levels of Dataset D7: with the switch **on**, the old tier lists become levels (36 tools Full, `serve_web` Name and short description, the rest Hidden, external servers Hidden or Name and short description); with the switch **off**, every static tool becomes Full and each external server Full or Hidden as its old `deferred` behaviour was. What each agent is sent after the conversion equals what it was sent before, tool by tool, except the SC-003 named changes and one more (round-2 fix, R2-MAJ-005): with the switch **off**, the old request stripped the infrastructure tools (`pkg/agent/tool_manifest.go::stripInfraToolDefs`; ToolSearch is in `pkg/tools/manifest.go::infraManifestToolNames`), so ToolSearch was not offered; after the conversion ToolSearch is locked Full and is offered. This is intended (one level set for every install, ST-Q4 B) and is named in SC-003 and DOC-014. It changes no permission: ToolSearch finds only what the agent may use, and each tool it finds keeps its own permission. A goal-narrowed first request bypasses the normal tool surface (`pkg/agent/loop_run_turn.go::prepareToolSurface`), so the comparison captures the goal-narrowed request when there is one **and** the next normal request (ST-06, Dataset D7 row 16). The old keys are removed from `config.json` in the same write (founder rule 2026-11-28; Dataset D7 row 17). What leaves where: C-22.
- Changing a level while chats run: C-15.

### C-7 New service tools start at Ask; no "Unset"

No schema change. Behaviour of `POST /mcp-servers` and the `add_mcp_server` agent tool: adding a service writes the global key `mcp_<server>_*` = `ask` when no global key already covers the service, which covers every tool the service has now and adds later (R30b). The one-time upgrade and the exact meaning of "covered" are C-18 (round-1 CRIT-004 replaced the round-0 "no exact server key" rule). After this, every tool row has a value; the screen has no "Unset" state (R30b).

### C-8 `WorkflowStart` gains the write-skill workflow

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `WorkflowStart` | changed | `contracts/components/schemas/WorkflowStart.yaml` **and** the inline copy in `contracts/asyncapi.yaml` (kept in sync by hand, per the schema's own description) | `workflow` enum adds `W5` (write-skill, `pkg/coreagent/prompts_adr090.go:102`). `target_workspace_id` stays optional in the schema; the server requires it for `W1` and `W18` and refuses a start without one, or with a blank one, with "This workflow needs a target workspace." (`pkg/gateway/websocket.go::workflowStartRefusal`, checked whether or not inbound schema validation is on; any other workflow → "Unknown workflow."; spec sync 2026-11-28); for `W5` it is optional: absent from Skills & Tools, where the write-skill card proposes a global skill and still lets the person pick a workspace's project shelf (the workflow already supports both, `pkg/skills/embedded/write-skill/SKILL.md`) |

### C-9 Approval card names the skill

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `ToolApprovalRequiredFrame` | changed | `contracts/components/schemas/ToolApprovalRequiredFrame.yaml` | adds optional `skill` object: `key` (C-23), `name`, `display_name`, `revision`, `unverified` (bool), `open_checks` (array of `permission`, `trust`). The card's choices are the frame's top-level `offered_actions` and `allow_durations` (C-26). Changed in all three copies kept in sync by hand: `contracts/components/schemas/ToolApprovalRequiredFrame.yaml`, the inline schema in `contracts/asyncapi.yaml`, and `pkg/gateway/inboundschemas/ToolApprovalRequiredFrame.yaml`; `SessionStatePendingApproval` (same three places) gains the same `skill` object, so a card rebuilt after a reload is complete. Present when the call loads a skill (`Skill`, the person's `/name` trust card, delegation) or installs one (`install_skill`, with the staged revision and its verification). Every value is server-derived from the pending approval; the client sends none of them back |
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
| E4 | Person's `/name`: `pkg/agent/loop_slash.go::applyExplicitSkillCommand` (sets `ForcedSkills`) | the instructions | the person asking by name is the consent for an ordinary skill Ask (unchanged from round 0); `deny` and a trust `ask_pending` are **not** bypassed: trust Ask shows the trust card first (ST-Q2 A) |
| E5 | Delegation: `delegate` `requested_skill` → `pkg/agent/subturn_identity.go::resolveRequestedSkillForChild` | the instructions, in the child | decided for the **child** agent; `ask_pending` raises the card in the root chat (interactive) or refuses (unattended) |
| E6 | File tools on a skill folder: `pkg/tools/skill_turn_authority.go::resolveTurnSkillAuthority` → `pkg/skills/observe.go::LoadableFolders` → `pkg/fspolicy/homeguard.go::skillPathDenied`; kernel grant via `turnSkillFolders`; **project-skill folders under mounts: C-25** | the files, including the instructions | installed shelf: a folder is in the loadable set only when the outcome is `allowed`; `deny` and `ask_pending` folders are left out, exactly as an ungranted skill's folder is today, in the app-level guard **and** in the kernel grant (both read `resolveTurnSkillAuthority`). Project shelf: the C-25 gate, because a mount grant would otherwise admit the folder whatever the loadable set says (round-2 fix, R2-CRIT-002) |
| E7 | Helper execution: `pkg/tools/shell_skill_operands.go::loadableSkillReadOrRun` | runs helper scripts | the same installed and project folder sets as E6, so the same rule |
| E8 | Full level (C-1, new) | the instructions, from the first turn | injected only when the outcome is `allowed` for that agent **and** chain (C-14). `ask_pending` → listed as Name and short description; the load then goes through E1 |
| E9 | Unified search (C-17, new) (ST-Q6 A) | name / description | found only when not `deny`; never loads |

**Editor exception, kept separate.** Today `resolveTurnSkillAuthority` sets `readAll` when `edit_skill` is not denied, so an agent allowed to edit skills can read every skill folder for authoring. This stays. It is read-for-editing only: it never injects instructions, never counts as a load, never records consent, and never adds a folder to the E7 helper set. A Deny on a skill therefore does not stop an editor agent from *reading* its files; the skill's own Deny stops it being *used*. The page says so in the `edit_skill` row's help text: "Agents allowed to edit skills can read every skill's files, including denied ones." Text already disclosed to a model earlier in a chat cannot be withdrawn; a later Deny applies from the next turn (C-15).

### C-12 Consent: one decision table (round-1 fix MAJ-001; rewritten in round 2, R2-MAJ-001) (ST-Q2 A, ST-Q3 B, ST-Q8 A, ST-Q9 A, R2-Q1 timed allowances, R2-Q2 A)

Two checks can be open on one skill load. They stay two stored decisions: "may this version of the files be on this system at all" and "may an agent use it without asking".

| Check | Open when | Settled by | Never settled by |
|---|---|---|---|
| **Permission check** (ordinary Ask) | the effective permission for this agent (global value × the agent's own value, strictest wins; God Mode floors only the global side) is Ask | Auto-approve where it is active for the run's session (R2-Q2 A); a running timed allowance for this agent and this skill (C-26); the person's own `/name` (E4); Approve Once | a trust approval; the install review |
| **Trust check** | the skill is unverified (C-5; a project skill always, ST-Q8 A), trust is Ask, and its current revision has no trust approval | a person's trust approval of that revision: the install review (C-13), a card action that includes it (C-24), or More actions → "Approve this version" (C-24) | Auto-approve, God Mode, a timed allowance, `/name`, Approve Once |

A Deny anywhere (global, the agent's own value, trust Deny for an unverified skill) refuses before either check: no card, and the refusal names the setting.

"Auto-approve active" means exactly today's rule, unchanged by this package: `pkg/agent/auto_approve_gate.go::autoApproveActive` (the session's Auto-approve; never in God Mode; an agent's "Never auto-approve" switch turns it off for that agent). `pkg/agent/loop_run_turn_tools.go::resolveAskPolicy` consults it, and standing allowances (C-26), **before** the unattended refusal, and `Skill` is `AutoRuns` (`pkg/tools/auto_approve.go`). So wherever Auto-approve is active, an ordinary Ask is settled the same way in a chat and in a task, plan or scheduled run (✅ Verified order in `resolveAskPolicy`; whether Auto-approve is active for a given unattended session follows `SessionAutoApprove` as today).

**Decision table** (the oracle for Dataset D10; P = permission check still open after its settlers, T = trust check open):

| # | Entry | P | T | Interactive (chat; `/name`; delegation, card in the root chat) | Unattended (task, plan, scheduled) | Stored change |
|---|---|---|---|---|---|---|
| 1 | load | no | no | loads, no card | loads | none |
| 2 | load | yes | no | one **permission card**: Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny (C-26) | skipped: "Not run: needs approval, and task and plan runs never ask." | per the answer (C-24) |
| 3 | load | no | yes | one **trust card** naming the skill as unverified: "Approve this version", Deny; no time choices (the permission part is not open) | skipped, same text | per the answer |
| 4 | load | yes | yes | one **combined card**: "Approve this version" (trust approval and this load), "Approve this version and allow for" 1 hour / 3 hours / 1 day (trust approval and a timed allowance of the permission part), Deny | skipped, same text | per the answer |
| 5 | the person's `/name` | settled by `/name` | yes | trust card (row 3) | — | per the answer |
| 6 | `install_skill` by an agent; its effective permission Ask; the skill unverified; trust Ask | — | yes (staged revision) | one **install card** for the `install_skill` call that also names the skill as unverified, with its staged revision and verification result: "Install and approve this version" (Approve Once) and Deny; no time choices. Approving it is both the tool approval and the trust approval of the staged revision | refused, nothing installed | trust approval of the staged revision, then publish |
| 7 | `install_skill`; effective Ask; verified, or trust Allow | — | no | today's `install_skill` card, with the C-26 choices (Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny) in place of Always Allow | refused | as today |
| 8 | `install_skill`; effective Allow; unverified; trust Ask | — | yes (staged revision) | one trust card bound to the staged revision: "Approve this version", Deny | refused, nothing installed | trust approval, then publish |
| 9 | install through the screen's review (C-13) | — | answered by the review | no card | — | trust approval of the reviewed revision |

- Rows 6 and 8 stage the skill (download and unpack into the existing staging area; never published) before the card is raised, through the existing pre-approval seam (`pkg/agent/loop_run_turn_tools.go::requestAskApproval` → `prepareApprovalRequest`), so the card shows the real revision and verification. A staged copy that is denied, times out or is refused is discarded. `install_skill` stays `AutoAsks`; its own permission is never dropped to save a card.
- God Mode lifts a global Ask; an agent's own Ask still counts, and Auto-approve is never active in God Mode, so that Ask gives row 2 (card) or a skip.
- A card answered after its facts changed is re-checked before anything is stored (C-24).
- The five exact-action tools (C-26 point 9: `environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config`) are not skills and never appear in this table; `install_skill` is not one of them and keeps its row 7 time choices (security point 7).
- A change of the skill's permission (global or this agent's own) ends this agent's running allowance for it, so the next load follows the table from the top (C-26 point 7). A trust change ends nothing: an allowance never covers the trust part.

Stored consent:

| Kind | Where | Lifetime |
|---|---|---|
| Timed allowance | the existing in-memory grant store (`pkg/security/approvalgrants.go::ApprovalGrantStore`), re-keyed by C-26: agent and skill key (C-23); never written to disk | until its end time (1 hour, 3 hours or 1 day after the answer), a gateway restart, or a change of that skill's permission for that agent or globally (C-26 point 7); covers the permission part only, so an unverified skill's changed revision still gets a trust card |
| Trust approval | `$OMNIPUS_HOME/system/skill-trust-approvals.json` (C-24): skill key, revision, who, when; written only by a person's action | installation-wide; that revision only |
| Global permission | `sandbox.skill_policies` (C-2) | until a person changes it |

**New skills (ST-Q3 B, R2-Q2 A, founder 2026-11-22).** A skill a person or an agent adds is stored at Ask. That Ask is an ordinary permission: where Auto-approve is active it is settled like any other Ask, so once trust is answered (by the install review, or because the skill is verified) the first load runs with no card; where Auto-approve is off, the first load shows the permission card. The trust check, not a first-use card, is the protection against unchecked files. The install toast says this (UI Screens and States).

**Unattended runs (ST-Q9 A).** Task, plan and scheduled runs never ask and never pause (existing rule, kept): when P or T is still open, the skill is skipped with "Not run: needs approval, and task and plan runs never ask." and the run carries on or ends as its plan decides. No waiting step exists or is added. A person makes the skill work in such runs in one of two ways (R2-Q1, founder 2026-11-22): for a while, by answering one of that agent's cards in a chat with a time choice — the timed allowance covers that agent's task, plan and scheduled runs until it ends (C-26), and other agents are not affected; for good, only by stored policy — the skill's global value set to Allow on Skills & Tools, with that agent not set stricter. A version approval settles the trust part for that revision, as before. There is no card action that changes a stored permission.

### C-13 Install review before publishing (round-1 fix, MAJ-002)

| Schema / endpoint | New / Changed | File | Notes |
|---|---|---|---|
| `POST /skills/install/review` | new | `contracts/openapi.yaml` | body `SkillInstallRequest` (catalog slug + version, or upload reference); stages the skill with the existing staging code in `pkg/gateway/rest_skills.go::installSkill` **without** `publishSkillInstall`; returns `SkillInstallReview` |
| `SkillInstallReview` | new | `contracts/components/schemas/SkillInstallReview.yaml` | `review_id`, `name`, `version`, `revision` (`ContentRevision` of the staged folder), `verification` (enum `matched`, `no_registry_hash`, `mismatch`, `unavailable`), `verified` (true only for `matched`), `suggested_category` (`SkillCategory` or null, from `metadata.category` when valid), `replaces_existing` (bool), `has_local_changes_to_replace` (bool), `expires_at` |
| `DELETE /skills/install/review/{review_id}` | new | `contracts/openapi.yaml` | discards the staged folder; 204; unknown or expired id → 404 |
| `GET /skills/install/review/{review_id}` | new (round-2 fix, R2-MAJ-006) | `contracts/openapi.yaml` | returns `SkillInstallReviewStatus`; 404 when the record no longer exists |
| `SkillInstallReviewStatus` | new | `contracts/components/schemas/SkillInstallReviewStatus.yaml` | `review_id`, `state` (`staged`, `publishing`, `installed`, `failed`, `expired`, `cancelled`), `revision` (the reviewed one), `installed_revision` (set when `installed`), `replaced_revision` (the revision it replaced, or null), `reason` (for `failed`) |
| `SkillInstallRequest` | changed | as C-5 | adds `review_id` and `category`. With `review_id`, the server publishes **exactly** the staged folder of that review: if its current `ContentRevision` differs from the reviewed `revision` → 409 "The skill changed after review. Review it again."; expired → 410 "This review expired. Open the skill again to review it." |

- `mismatch` is refused at review time (existing rule: a hash mismatch always fails, `pkg/skills/clawhub_registry.go`): 422 "The downloaded skill does not match the catalog's checksum. Nothing was installed."
- `unavailable` (registry did not answer the hash lookup) → `verified: false`; trust applies.
- Trust changed between review and Install: re-checked at publish; Deny → 403 (C-3 text).
- A staged review lives 15 minutes and is removed on expiry, on DELETE, and after publish.
- **Cancel**: before Install is pressed, Cancel sends DELETE and closes; "Nothing was installed." is true. After Install is pressed, Cancel is disabled until a terminal state is known.
- **One operation per review (round-2 fix, R2-MAJ-006).** The review id is also the install operation's id. A review record moves to a terminal state (`installed`, `failed`, `expired`, `cancelled`) exactly once and is kept for 15 minutes after that, then removed. `POST /skills/install` with a `review_id` is idempotent: while `publishing` → 202 with the status; after `installed` → 200 with the same result and no second publish; after `failed`, `expired` or `cancelled` → the same terminal answer again (409 or 410).
- **Lost answer.** If the Install response is lost, the review shows "Could not confirm the install. Checking…" and reads `GET /skills/install/review/{review_id}` every second while the state is `publishing`, for up to 60 seconds. `installed` with `installed_revision` equal to the reviewed revision → "Installed <name>."; `failed` → "Not installed: <reason>."; `expired` or `cancelled` → "Not installed."; still `publishing` after 60 seconds, a failed read, or 404 → "Couldn't confirm the install. Open Skills to see what is installed." with Retry, which reads the status again. The skills list alone never decides: a replacement leaves the old version listed. `Skill.revision` (C-5) lets the Skills tab show which revision is installed.
- Agents: `install_skill` uses the same stage-then-publish path internally; for an unverified skill under trust Ask the trust card (C-12) is bound to the staged `revision`.

### C-14 Full skills: when they are injected and what happens when they do not fit (round-1 fix, CRIT-001, MAJ-006) (ST-Q1 A)


| Effective outcome for this agent and chain (C-11) | Interactive | Unattended |
|---|---|---|
| `allowed` | whole instructions in the context from the first turn | same |
| `ask_pending` (ordinary Ask, or trust Ask without a matching approval) | not injected; listed as Name and short description; a load raises the card; after approval the skill is loaded the normal way (E1) | not injected; listed as Name and short description; a load is refused |
| `deny` | nothing | nothing |

- Full never counts as consent and never bypasses E1's checks.
- **Budget.** The injected instructions are charged once to the existing request budget (`pkg/agent/context_budget.go::contextBudget`, `::pinnedCoreOverheadTokens`), as part of the pinned overhead. If pinned overhead including all Full skills plus the output reserve does not fit the agent's model, the turn is **not sent** and the chat shows: "The skills set to Full for <agent> need about <n> tokens, more than <model> can take with this chat. Set some of them to Name and short description in Skills & Tools, or choose a model with a larger context." with a link to the Skills tab filtered to Full. Nothing is trimmed silently. The check runs every turn, so a model switch or a skill that grew is caught on the next turn.
- **Page warning** (data sources settled by ruling spec-gaps-1, 2026-11-25). Each Full skill row shows its size ("about 3,200 tokens"); when the Full total for any agent exceeds half of that agent's model context, the Skills tab shows a warning callout naming the agents. Both numbers are computed by the server; the page never adds up sizes or decides outcomes itself:
  - `Skill.full_tokens` (`contracts/components/schemas/Skill.yaml`, `GET /skills`; integer ≥ 0, read-only, optional): the estimate the turn charges for this skill's block `### Skill: <id>\n\n<body>`, made with the turn's estimator (`pkg/agent/context_budget.go::estimateMessageTokens`, as `pkg/agent/full_skills.go::fullSkillsTokens` applies it). Served for every installed skill whatever its level, so a row can show it as soon as it is set to Full. Absent when the skill file cannot be read (the skill then has `load_error`).
  - `Agent.full_skills_tokens` (`contracts/components/schemas/Agent.yaml`, `GET /agents` and `GET /agents/{id}`; integer ≥ 0, read-only, optional): what this agent's next turn would charge for Full skills, computed by **the same selection the turn uses** (`pkg/agent/full_skills.go::ContextBuilder.fullSkillsContext`: granted, eligible, level Full, outcome `allowed` for this agent, then `fullSkillsTokens`) over installed skills only. Project skills are left out: they depend on the chat's workspace and are not listed on this tab. 0 when no Full skill would be injected; absent when it cannot be computed (for example the agent's record is unreadable). The gateway calls the turn's selection; it never keeps a second copy of the outcome rules (C-11, C-14).
  - Row text: `full_tokens` rounded to the nearest 100, "about <n> tokens" (for example 3,240 → "about 3,200 tokens"); below 50 → "under 100 tokens"; absent → no size. Only rows at Full show it.
  - Warning rule: the Skills tab reads `GET /agents` and names every agent with `full_skills_tokens × 2 > context_window_effective` (the existing ADR-066 field on `Agent`). Exactly half is not a warning. An agent whose `full_skills_tokens` or `context_window_effective` is absent, or whose window is 0 (exempt subprocess agents), is never named. Callout (warning): "The skills set to Full take more than half of the model's context for <agent names, comma-separated>. Set some of them to Name and short description, or choose a model with a larger context."
  - Freshness: the page re-reads `GET /agents` once after each saved skill level or skill permission change and on window focus. If `GET /agents` fails, no warning is shown and the rest of the tab is unaffected (no extra error callout); the chat refusal above stays the guarantee.


### C-15 Changing a level or a permission while chats are running (round-1 fix, MAJ-005)

- **One visibility authority.** Manifest building (`pkg/agent/tool_manifest.go::buildCompressedToolDefs`, `::buildToolManifestNote`), search membership (`pkg/tools/registry.go::SnapshotSearchableTools`) and the skill menu read the stored levels through one function. The fixed lists in `pkg/tools/manifest.go` are deleted, not kept beside the levels. `SnapshotSearchableTools` includes every tool whose level is not `full` and that is not ToolSearch, so demoting an old Full built-in to Hidden makes it searchable.
- **Levels govern what is advertised at the start of each turn.** A level change takes effect from the next turn of every chat, new or running. Definitions already loaded in a running chat (`pkg/agent/prompt_cache_sessions.go::restoreLoadedToolRetention`, `::pruneLoadedTools`) stay loaded until that chat's existing retention drops them; a level change does not evict them.
- **Permissions are different.** An effective Deny removes the tool from the next turn's tool list even if loaded (`FilterToolsByPolicy` runs every turn; `pruneLoadedTools` receives only allowed tools), as today. For skills, a Deny removes the skill from the menu, from search and from the loadable folders (C-11) from the next turn.
- **Skill menu cache.** `freezeSessionSkillMenu` freezes the menu per session generation. The generation key gains a hash of the agent's skill outcomes and levels, so a permission, trust, grant or level change rebuilds the menu on the next turn (one prompt-cache miss for that chat).
- Visibility never grants the right to call anything.

### C-16 Saving, step by step (round-1 fix, MAJ-009; rewritten in round 2, R2-MAJ-004)

| State | Shown on the row / page | How it ends |
|---|---|---|
| queued | the clicked value, marked "Saving…" within 100 ms of the click | sent as soon as no earlier change is in flight or being checked |
| in flight | same | one of the outcomes below |
| saved and applied (200) | "Saved" | the page takes the stored maps from the response |
| saved, not applied (503) | "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." + Retry | Retry re-sends the same keys and values; the server answers 200, writes nothing and writes no audit entry (C-2) |
| refused (4xx) | "Not saved: <server reason>." + Retry; the row shows the stored value from a fresh `GET` | — |
| not stored (500) | "Not saved: the settings file could not be written." + Retry; the stored value from a fresh `GET` | — |
| outcome unknown (network error, timeout, no response) | "Couldn't confirm the save. Checking…", then the stored value from a fresh `GET` and "Showing what is stored now." | the page never assumes the old value came back |
| check failed (the fresh `GET` fails too) | "Couldn't load what is stored." + Retry on the row; the clicked value is not shown as stored | Retry reads again |

- **One queue, one request at a time, in click order.** Each click (one badge, one level, one Set all) is its own `PATCH` naming only its own keys, never merged with another click. The next request starts only after the previous one has an outcome and, for refused, not-stored and unknown outcomes, after its fresh `GET` has answered or failed. Timing rule: the "Saving…" mark appears within 100 ms of the click; the request is sent within 1,000 ms of the click **when nothing earlier is pending**; behind a slower earlier request it waits, still marked "Saving…".
- **Later changes after a failure** are still sent, in order: each names its own keys, so one refusal does not cancel another change. A later click on the **same** key replaces a queued, not yet sent change of that key (the row shows the latest click); a request already sent is never recalled.
- **Queued versus stored.** A row shows the clicked value with "Saving…" only while that change is queued or in flight; otherwise it shows the stored value from the latest response or `GET`. "Saved" refers to the change whose response arrived; a later response never marks an earlier change Saved.
- **Ownership.** The queue lives in an app-level store, not in the page, so in-app navigation does not stop it.
- **After leaving the page**, a late outcome shows one toast with "Open Skills & Tools", worded by its class: refused or not stored → "A change on Skills & Tools was not saved: <reason>."; saved, not applied → "A change on Skills & Tools was saved but is not applied to running agents yet."; unknown or check failed → "Couldn't confirm a change on Skills & Tools. Open it to see what is stored."
- **Closing the browser.** Requests use `fetch` with `keepalive`, so a request already started continues. A change still queued when the browser closes is not sent; reopening the page shows what is stored. No leave prompt (T-Q3 B).
- `useAutoSave` (`src/hooks/useAutoSave.ts`) is not used for this page's permission and level writes because it coalesces queued changes to the latest snapshot; the agent Tools tab keeps it for its own fields, with the C-4 preservation rule.

### C-17 One search for tools and skills (round-1 fix, MAJ-015) (ST-Q6 A)

`ToolSearch` (`pkg/tools/tools_tool.go`) also searches the skills the calling agent may use (granted or project-authorized, outcome not `deny`, any level including Hidden), with the same keyword ranking. Each result line starts with its kind: `[tool]` or `[skill]`. A skill result gives its name and description and the sentence "Load it with the Skill tool." It does not load the skill and does not change grants or permissions. ToolSearch output is tool text, not a wire schema, so no contract file changes; the tool's description changes to say it finds skills too. `Skill` search stays.


### C-18 Migration of external tools that have no global value (round-1 fix, CRIT-004) (ST-Q5 A)

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

No agent entry is ever written (Hard Constraint #6: no per-agent backfill). Exactly the two "yes" rows change behaviour; both are listed in the upgrade note (DOC-004) and in SC-003's exceptions. It is part of the C-19 `config.json` conversion (computed from the old-shape `config.json` it converts, in the same write), before the self-heal of C-2; a second start finds no old-shape value and writes nothing (founder rule 2026-11-28).

New service (after upgrade, screen or `add_mcp_server`): `mcp_<S>_*` = ask is written only when the probe has no global match; under a broader operator rule (for example `mcp_*: deny`) nothing is written and the rows show that rule's value.


### C-19 Project-folder skills have their own keys (round-1 fix, MAJ-003)

| Shelf (`pkg/skills/shelf.go::ResolveSkillName`) | Permission and level key | Created | Removed |
|---|---|---|---|
| built-in, registry, uploaded, written in Omnipus | the skill id (lower case) | install, copy, first sight at reconcile | when the skill is removed |
| project shelf of workspace `W` | `project:<W>/<slug>` | when Omnipus first reads `W`'s project shelf (turn start or reconcile) | when `W` is deleted, or at reconcile when the folder is gone |

- A project skill and an installed skill with the same slug have different keys; `ResolveSkillName`'s existing precedence decides which skill runs, and that skill's key applies.
- Project skills have no grant list; authority is the workspace mount, as today. Their starting permission is Ask, like every newly added skill (ST-Q3 B); a project skill already present at the upgrade gets the converted value (step B of the upgrade below). Their starting level is Name and short description.
- Project skills are not listed on the Skills tab (#1380 not a rider), so their keys are edited only through `PATCH /security/permissions` and the agent writers (C-21), all validated by C-23. Their folders are gated for file tools and helpers by C-25. They count as unverified (anyone with write access to the workspace can change them), so trust applies and each new version asks once, even in God Mode (ST-Q8 A); this is a named behaviour change in DOC-004.
- **Upgrade: each file converts itself, and nothing marks the upgrade (founder rule 2026-11-28; replaces the round-2 write-ahead design of R2-CRIT-001; revised after the on-demand review of 106f63ef9, NM-01 to NM-06).** The founder's rule: after an upgrade the data folder looks like a fresh install that carries the old settings and skills; nothing on disk names or marks the upgrade; nothing checks whether it is done. The old values live in two kinds of file, `config.json` and one record per agent under `$OMNIPUS_HOME/entities/agents/` (`pkg/agentstore/store.go::RecordsDir`; ADR-054 — Entity / config separation), each written atomically (`fileutil.WriteFileAtomic`: a `.tmp-*` file in the same folder, then a rename). **Neither kind needs a value from the other:** every old global value (the old global `Skill` value, the trust word, the three tool-loading switches, the MCP map) is in `config.json`, and the translation of an agent needs only that agent's own old `Skill` value, its grants and its mounts' project skills (`pkg/config/skill_permissions.go::PlanAgentSkillOverrides` takes no global value). Each file does need the **skill inventory** (the installed skills and the project skills on the mounts) to translate an old `Skill` value of `ask` or `deny`; the inventory rule below covers that (NM-02). Each file is converted in **one atomic write of that same file** that adds the new values and removes the old ones.

| Old-shape value (the only trigger) | File | Never in a new-shape file because |
|---|---|---|
| `tools.manifest.compressed` present | `config.json` | every config written by an older Omnipus carries it (`ManifestConfig.Compressed` had no `omitempty` at `4e34afa416`); the field leaves the struct |
| `tools.manifest.preview_all_lazy` present | `config.json` | the field leaves the struct |
| `tools.mcp.servers.<id>.deferred` present | `config.json` | the field leaves the struct; no wire schema carries it |
| `sandbox.skill_trust` = `block_unverified`, `warn_unverified` or `allow_all` | `config.json` | every writer accepts only `allow`/`ask`/`deny` (C-3) |
| `sandbox.tool_policies.Skill` with a value other than `allow` | `config.json` | `Skill` is fixed at `allow` (C-6) |
| `tools.builtin.policies.Skill` present (any value) | an agent record | seeds no longer write a per-agent `Skill` value and no writer stores one (C-21) |

`Skill: allow` alone, an empty or absent trust value, and `tools.mcp.discovery.enabled` (still read for search registration) are not old-shape values. A `config.json` written by hand without any old-shape value is treated as new shape; the result is never looser than before (its skills get Ask from the self-heal, its tools the fresh-install starting levels, an uncovered service keeps its old Deny or agent value).

**One writer rule for both kinds of file (NM-01; revision 3: R2-01, R2-03, W-01).** Every write of `config.json` or of an agent record — the conversion, every ordinary save, the self-heal and every load-time repair of other features — is one read–convert–change–replace cycle held under that file's **ordinary writer lock**, which every writer of that file takes, whatever the reason for the write. Each writer hands in only **its own intended change**, applied to the document read inside the lock; no writer hands in a whole document prepared before the lock. The lock is not an upgrade lock: fresh installs take the same lock for every save, and its lock file holds no data.

| File | Writer lock: in one process, then across processes | The one entry every writer goes through |
|---|---|---|
| agent record `entities/agents/<id>.json` | the entity store's striped in-process mutex, then the existing sidecar `entities/agents/<id>.lock` (`pkg/entity/store.go::Store.WithLock`, the lock `Store.Update` already holds; ADR-054 "Concurrency contract") | `func (s *agentstore.Store) UpdateRaw(id string, change func(converted []byte) (next []byte, err error)) (written bool, err error)` (new, built on `entity.Store.WithLock`). Every other record writer keeps the same lock and converts the record it reads first: `agentstore.Store.Update`, `skill_grants.go::RemoveSkillGrant`/`AddSkillBindings`, the state and pending writers, and the self-heal's removal of gone project keys; `Create` and `PublishPending` write only new-shape records. The existing order stays: skills-mutation lock, then entity lock, never the reverse, never one of them twice |
| `config.json` | a process-wide mutex keyed by the cleaned absolute path (as `entity.Store.WithLock` does for records), then the generic sidecar `config.json.lock` (`fileutil.WithFlock(fileutil.SidecarLockPath(path), …)`, the pattern of `pkg/task/store.go`, `pkg/plan/store.go`, `pkg/credentials/store.go`); both are taken **inside** `UpdateConfigFile`, so every caller is covered, including the reload loader (`pkg/gateway/gateway.go::newReloadConfigLoader`) and the self-heal callers (`pkg/agent/project_skill_keys.go::storeNewProjectSkillKeys`), which do not hold `restAPI.configMu` | `func config.UpdateConfigFile(path string, change func(m map[string]any) error) (ConfigWrite, error)` with `type ConfigWrite struct { Bytes []byte; Committed bool; Deferred bool }` (new): takes both locks, reads the current bytes inside them, converts them (step B, or leaves them as stored when the conversion is deferred, below), runs `change` on that document, runs the heal (step C), writes with `fileutil.WriteFileAtomic` only when the bytes differ. All writers go through it: `restAPI.writeConfigJSONLocked`; `pairing_config_write.go::orderedConfigJSONLocked` (its authoritative read, preparation and channel-order check move inside `change`; the channel commit still follows persistence, after the lock is released); the load and reload; `pkg/config/{cli_token_migration,legacy_agents_list,max_tool_iterations,searxng_cleanup,web_search_roles}.go`; `pkg/app/internal/clitoken/clitoken.go::upsertCLIToken`; `pkg/app/internal/onboard/onboard.go::mutateConfigFile`; `pkg/gateway/gateway_boot_roster.go::persistConfigMarkerList`. First creation uses `func config.CreateConfigFileIfAbsent(path string, data []byte) (created bool, err error)` under the same locks, which re-checks absence inside them (`pkg/datamodel/init.go::Init`). `pkg/config/mail_identity.go` and `pkg/onboarding/onboarding.go` write other files and are not `config.json` writers |

**System-agent saves are named operations (revision 8; withdraws `SaveConfigChanges` and its value rules, the list conflict check, the list correspondence rule, the persisted baseline and its element-count guard of revisions 3 to 7).** `config.SaveConfig(path, cfg)` has one runtime caller, the system agent (`pkg/gateway/gateway.go::wireSystemTools`, `Deps.SaveConfigLocked`). It is removed and **not** replaced by any comparison of two structs. Instead every system-agent writer passes **its own named operation** as the `change` function of `UpdateConfigFile`, as the REST writers already do (`restAPI.writeConfigJSONLocked`). The operation is applied to the document read inside the lock and touches only the paths it names:

| Writer | Its operation on the document read inside the lock | Another writer's change meanwhile |
|---|---|---|
| `configure_provider` (`pkg/sysagent/tools/provider.go::ProviderConfigureTool`, today `wireProviderEntry`) | for **every** row of `providers` whose `provider` value (trimmed, as the load compares it) equals the tool's provider name: set `api_key_ref` and/or `api_base` when the call gives them. Every other member of every row stays as stored, unknown members and stored spelling included. No row with that name: append one row `{provider, model: <name>, …the given members}`, as `wireProviderEntry` does today. Changing all rows of one provider together is the tool's existing meaning (it has no row selector), so it is one operation, never refused as several edits | changes to other rows, or to other members of the same rows, stay; the same member set by both: the later commit wins (commit order under the lock) |
| `add_mcp_server` (`pkg/sysagent/tools/mcp.go::MCPAddTool`; adds or updates) | on `tools.mcp.servers.<name>`: writes every member that `config.MCPServerConfig` marshals, removes the members of that type that now marshal absent, and keeps the members the type does not know (so an old `deferred` member of an updated server stays); creates the entry and its parent objects when absent | other servers stay |
| `remove_mcp_server` (`MCPRemoveTool`) | deletes `tools.mcp.servers.<name>`; a `servers` object left empty is removed, as a fresh install serializes it | other servers stay |
| `set_config` (`pkg/sysagent/tools/config.go::ConfigSetTool`) | sets its one key to its value (`setDot` on the document; `set_config` has no list-index paths, so a list is set as a whole) | other keys stay; the same key set by both: the later commit wins |
| channel writes through `channelWrite.withConfig` (`pkg/sysagent/tools/channel_ordering.go`) | edit only the `channels.<instance>` entries they name; `channels.PreparePairingConfig` compares the typed views decoded from the document before and after the operation, both inside `change`; channel orders are still taken before the lock (work list B14b) | other instances stay |

Each operation's existing checks (`validateProviderSelection`, `validateRetiredConfigKey`, `validatePerformanceOnlyConfigKey`, `validateConfigKey`, `validateConfigKeyLands`, the shared-resource revision check of `withSharedResourceWrite`) run inside `change`. They run on the typed view decoded from that same document by the pure `func config.DecodeDocument(m map[string]any) (*Config, error)` (new; no file access, no lock), before and after the operation, so a check never reads another version than the one written. `Deps.WithConfig(fn func(*config.Config) error)` becomes `Deps.WithConfigOperation(op func(m map[string]any) error) error`, and `Deps.SaveConfigLocked` becomes `Deps.UpdateConfigLocked`, wired to `UpdateConfigFile`. Nothing compares two typed structs, so nothing has to guess which list element is which: there is no persisted baseline, no list conflict check, no correspondence rule and no element-count guard. A file the load normalizes (for example trimmed provider fields, `pkg/config/config_models.go::normalizeProviderRows`) needs no special rule, because the stored document is edited, not compared.

**Defaults are written only by the operation that changes them.** When the stored file has no `providers`, or an empty list, the load fills in the default providers in memory (`pkg/config/migration.go::loadConfig`, `pkg/config/defaults.go::defaultProviders`), and any stored non-empty list replaces them all. So `configure_provider` on such a file writes the default list with its one change, because that is the only stored form of "the defaults, with this provider changed"; today's whole-struct save writes the same rows. No other operation, no load and no other save writes default values.

**One refusal remains, for one operation.** `set_config` with a key whose value is a list of objects (today `providers`) replaces the whole list with the caller's value, which the system agent built from the typed rows it can see. When a stored element of that list holds a member that the element's Go type does not know (for `providers`, a member `config.ModelConfig` does not marshal), the replacement would delete it, and the operation refuses. The decision uses only the stored list read inside the lock. It returns `*config.UnshownFieldsError{Key}` (new), and the tool text is: **"Not saved: some saved entries in <key> hold settings this tool can't show, so replacing the whole list would delete them. Use configure_provider to change one provider, or edit config.json while Omnipus is stopped."** A list of plain values (strings, numbers) hides nothing and is replaced as given. Nothing is stored for this rule.

**Rejection and publication (revision 6, W4-01; revision 8: operations).** Rejecting the caller's change and installing the newer stored view are separate steps. Every result of `UpdateConfigFile` carries in `ConfigWrite.Bytes` the stored document as it is after the call (written, unchanged, or untouched because refused). Committed save, with or without an after-commit error, **or no-op**: `WithConfigOperation`, inside `MutateConfig`'s callback under the already-held loop lock, replaces the clone with the config decoded from the **converted view** of `ConfigWrite.Bytes` (`ConvertConfigJSON` with the deferred-file projection, so a deferred file keeps its old fallback and the fixed `Skill` tool), with the clone's runtime-only state carried over as `config.Clone` does (agent roster, sensitive-value sources, channel-order state); the callback returns nil and `mutateConfigLocked` publishes it. Live state therefore equals the stored file even when the file already held newer unrelated values. Refused (an operation's own check, the `set_config` unshown-fields refusal, or the Q-UPG1 refusal) or not saved: the callback returns the error and `mutateConfigLocked` discards the clone, so nothing of the rejected change is published. Then, **after `mutateConfigLocked` returned** and the loop lock is free, `WithConfigOperation` installs the stored view with `AgentLoop.SwapWrittenConfig` from `ConfigWrite.Bytes`, same projection (it takes the loop lock itself; order `configMu` → loop lock holds). The runtime-only state carried over is the **live config's at that moment**, read by `SwapWrittenConfig` under the loop lock it re-acquires, never the rejected clone's (another writer may have changed the agent roster meanwhile). It never calls the normal reload (`pkg/gateway/gateway_reload.go::handleConfigReload`, which takes `configMu`) while `configMu` is held, and never calls `GetConfig`, `SwapWrittenConfig` or `MutateConfig` inside the callback. A retry starts from that view. The load's own repairs already edit the document inside `UpdateConfigFile`. So the promise holds without exception: **every write applies only its own operation to the content read inside the lock, or writes nothing.**

**Lock order (R2-01; revision 4: R3-03, R3-04).** `restAPI.configMu` (REST and the system agent's `LockConfigWrites`), then the agent-loop lock (`AgentLoop.mu`), then the `config.json` lock. The `config.json` lock is a leaf: while it is held, nothing takes `configMu`, the agent-loop lock, an entity lock or the `config.json` lock again, and a `change` callback never loads, reloads or saves the config and never calls an agent-loop accessor. A load or reload takes only the `config.json` lock; the gateway's refresh after a REST write runs after `UpdateConfigFile` returned, so it never re-enters it. **The pairing writer is split** (`pkg/gateway/pairing_config_write.go`): the part of `prepareConfigJSONLocked` that reads the agent loop (`a.agentLoop.GetConfig()` for `CopyChannelOrderFrom`) runs first, under `configMu` and before the `config.json` lock, and hands a copied channel order to the `change` callback; only the authoritative disk read, the channel-order check against that disk document and the change run inside `UpdateConfigFile`; the channel commit follows persistence, after the lock is released. The heal's side effects outside `config.json` — growing `system/known-builtin-skills.json` and removing gone project keys from agent records — are idempotent and run outside the `config.json` lock after `UpdateConfigFile` returned **without an error**, whether it committed or found nothing to change (so a stop between the `config.json` replace and either effect is finished at the next reconcile, which finds `config.json` already right); they never run after a failed `config.json` write, and neither is described as rolled back. **Each effect is fresh when it runs (revision 6, W4-02); nothing computed before is replayed.** Agent-record cleanup goes record by record through `agentstore.Store.UpdateRaw`: inside that record's own lock it reads the record as stored and the project skills of the record's workspaces through the checked path (`workspaceProjectKeys`, complete), and removes only the project keys that are **still** gone at that moment; with an incomplete list it removes nothing; a key whose skill came back, or an override another writer stored meanwhile, stays. The known-builtins record `system/known-builtin-skills.json` grows only by a locked read and union: under its own path-keyed mutex and sidecar `known-builtin-skills.json.lock` (the same pattern as `config.json`), it re-reads the stored set, writes the union of that set and the built-ins **installed in this run and healed by it** (`installedBuiltins` as `pkg/skillperm/selfheal.go::SelfHeal` computes it today — never every built-in this process ships, so a shipped built-in whose folder is installed later still counts as new and gets the new-release Allow of C-2, revision 7, R5-02), and writes nothing when that adds nothing; it never writes a set smaller than or older than the stored one. Both locks are leaves, never held together with the `config.json` lock or with each other. One lock may be taken inside the `config.json` lock: the credential store's (`pkg/credentials/store.go`, its mutex and sidecar flock), and only by a `BeforeRename` step (W5-01 below); it is a leaf and never takes the `config.json` lock.

**Commit point (W-01; revision 4: test seams).** `Committed` is true once the rename has replaced `config.json`. Results: `Committed` false and no error — nothing changed; `Committed` false with an error (the locks could not be taken, the read, `change` (including an operation's own refusal), the Q-UPG1 check, a `BeforeRename` step, the temp file or the rename failed) — **not saved**: `PATCH` answers 500 "Not saved: the settings file could not be written.", `Deps.WithConfigOperation` rolls back; `Committed` true with an error (the unlock or the close of the lock file failed after the rename, or a fault injected after the rename) — **saved**: the error is logged at Error, the caller applies the change as saved (200, or 503 saved-not-applied, C-2), and nothing is rolled back. A failed directory sync after the rename stays as today (`fileutil.WriteFileAtomic` logs it at Warn and returns no error), so it is a committed write without an error. If the locks cannot be taken during a load, the load reads `config.ReadConfigView(path)` without them, decodes that, writes nothing, and logs an Error naming the file; it never decodes bytes it did not read. `CreateConfigFileIfAbsent` reports `created` true whenever its rename happened, even if releasing the lock then fails. Test seam: `func fileutil.SetAfterRenameFaultForTest(f func(path string) error) (restore func())`, consulted by `WriteFileAtomic` right after the rename; its error is returned wrapped so that `errors.Is(err, fileutil.ErrAfterCommit)` holds, and `UpdateConfigFile` and `CreateConfigFileIfAbsent` classify such an error as committed; `func fileutil.SetUnlockFaultForTest(f func(lockPath string) error) (restore func())` makes `WithFlock`'s unlock fail (test 74).

Because the input is read inside the lock, a converter that was delayed cannot write a result computed from an earlier read: it waits, then reads the file as the earlier writer left it, finds no old-shape value and writes nothing. A person's change acknowledged in between is kept, and so is every unrelated setting. Because every ordinary save starts from the converted content, a save never drops an old value that was not yet converted, and a successful save also finishes the conversion of that file (unless it is deferred, below). A hand edit is not covered by any lock: edit these files only while every Omnipus process is stopped (ADR-054 "Concurrency contract"; DOC-004). On Windows `fileutil.WithFlock` takes no lock (`pkg/fileutil/flock_windows.go`); the in-process mutexes still exclude every writer inside one Omnipus process, and, as for every other store, only one Omnipus process at a time is supported there.

**A file whose skill list is incomplete is not converted at all (NM-02; revision 3: R2-02).** The skill inventory is read inside the file's writer lock, just before the change:

| Input | Complete when | Incomplete when |
|---|---|---|
| installed skills | `$OMNIPUS_HOME/skills` lists without error; a missing folder counts as empty and complete | the folder or a skill's `SKILL.md` cannot be read for any reason other than "does not exist" |
| project skills of workspace `W` | `pkg/skillperm/project_keys_gone.go::workspaceProjectKeys(home, W)` returns complete (its checked path: `workspace.LoadMountsWithDropStatus`, `skills.MergeProjectSkillsChecked`), and `workspace.ListWorkspaceIDs` succeeded | a mount record cannot be read, a record was dropped, or a mount folder is unavailable or cannot be read in full |

An old-shape `config.json` needs the installed skills and every workspace when it holds `sandbox.tool_policies.Skill` with **any** value, `allow` included (step B turns it into one value per skill). An agent record needs the workspaces of its own mounts when it holds `Skill: ask` or `deny`; an old `Skill: allow` alone needs no list and is always removed. When a file needs the list and the list is incomplete, **that file's conversion is deferred as a whole**: its stored bytes are left exactly as they are, so all its old-shape values — the old `Skill` value and, in `config.json`, every other old key — stay on disk as the trigger for the next start, and nothing is added beside them. Ordinary saves still apply their own change to the stored document and nothing more (`ConfigWrite.Deferred`), except a save that would remove the file's last old value, which is refused (next paragraph); the self-heal writes nothing to it. Every reader uses the converted view computed in memory from the stored bytes and the part of the list that could be read, with one projection (W-02): the old `Skill` value — global or the agent's — is taken out of the tool-policy maps (the `Skill` tool itself is fixed Allow at run time) and becomes the value of every skill that has no value of its own in that file, whatever that old value is (`allow`, `ask` or `deny`), exactly as the previous release applied it. An explicit per-skill value already in the file wins over it. So a skill whose folder comes back during the run keeps its old Allow, Ask or Deny. Omnipus logs an Error naming the file and the cause at each start ("Skill permissions in <file> keep their old form because <the mount W1 could not be read>; they apply as before until every skill can be listed."). The next start that can list every skill converts the whole file in one write. Nothing records that a file is waiting: its own old values are the only trace, and they are real data, not a mark. A skill installed by another process while a conversion runs counts as newly added (Ask), the same as one installed a moment later.

**An ordinary save that would remove the last old value of a deferred file is refused (revision 4: R3-02; founder answer Q-UPG1 A, 2026-11-28 14:11Z).** This can only happen to a deferred `config.json` whose kept global value is `Skill: allow`: a kept `ask` or `deny` is itself an old-shape value, and no ordinary write changes it (`PATCH` compares `Skill` against the view, where it is the fixed `allow`, so it writes nothing; `set_config` refuses `sandbox.*`; no system-agent operation names a `sandbox` path). An agent record is never affected: its old `Skill` value is its only old-shape value, and no writer removes it (C-21). The rule, decided inside `UpdateConfigFile` from the document and the skill list it read inside the lock, never from anything stored: when the file's conversion is deferred, the document as read held at least one old-shape value, and the document after the caller's `change` would hold none, the write is **refused** — the check is computed in memory under the ordinary lock, on the document after `change`, before any temp file is created or anything is renamed; nothing is written, `ConfigWrite.Committed` is false, and `UpdateConfigFile` returns `*config.SkillListIncompleteError` (new; `Reason` one of `mount_unavailable`, `mount_record_unreadable`, `skills_folder_unreadable`, `workspace_list_unreadable` (revision 7, N5-01: `workspace.ListWorkspaceIDs` failed, so no workspace name is known); `WorkspaceID` when one workspace is the cause; `Place`, the workspace's display name, "the installed skills", or "your workspaces" for `workspace_list_unreadable`, for the sentence). Every caller maps this error with `errors.As` before its generic error mapping, so it is always the typed 409 below, never the generic 500 "Not saved: the settings file could not be written." (revision 6). Because the rule sits in the one entry, every writer that can make such a change gets it the same way:

| Writer that can remove the last old value | Example | Answer |
|---|---|---|
| `PATCH /security/permissions` (`pkg/gateway/rest_permissions.go::patchPermissionSettings`) | trust from the old `warn_unverified` to `allow` | 409 `ErrorResponse` (`pkg/api/generated::ErrorResponse`), `code: "skills_not_listable"`, `details: {reason, workspace_id?}`, `error` = the sentence below |
| `DELETE /mcp-servers/{id}` (`pkg/gateway/rest_mcp.go::deleteMCPServer`) | removing the last server that still has `deferred: true` | same 409 |
| `PATCH /mcp-servers/{id}` (`pkg/gateway/rest_mcp.go`) | rewriting that server without its `deferred` field | same 409 |
| the system agent's `remove_mcp_server` (`pkg/sysagent/tools/mcp.go::MCPRemoveTool`; revision 8) | removing the last server that still has `deferred: true` (`add_mcp_server` keeps the members its type does not know, `deferred` included, so an update never removes one) | the tool fails with the same sentence; nothing is published |
| any other writer through `UpdateConfigFile` (`PUT /config`, command-line writers, load-time repairs of other features) | none known that can touch these keys today (`set_config` and `PUT /config` refuse the retired keys and `sandbox.*` by name) | the same error; a command-line writer prints the sentence and exits non-zero; a load-time repair is skipped with an Error naming the file and the load goes on with the converted view |

The sentence (neutral voice, omnipus-voice: what happened, observed cause, what to do): **"Not saved: the skills in <Place> can't be read right now (<cause>), and saving this change would lose the permission they carried over from before. Save again once they can be read."** with `<cause>` one of "its folder isn't available", "its folder list can't be read", "the skills folder can't be read", and for `workspace_list_unreadable` (Place "your workspaces") "the list of workspaces can't be read" — never an empty name, an internal identifier, or a cause that was not observed. The screen shows it in the place where that save's errors already show (the trust block of Skills & Tools; the MCP server row); the value on screen goes back to the stored one. The file stays byte-identical; its old values keep applying as before. Once the skills can be listed, the next start converts the whole file and the same save succeeds. Contract change: `contracts/components/schemas/ErrorResponse.yaml` documents the new code `skills_not_listable` in `code`'s description (codes are listed there, not an enum) and its `details` in `details`' description (the four `reason` values above); `contracts/openapi.yaml` adds a `409` `ErrorResponse` response to `DELETE /mcp-servers/{id}` and `PATCH /mcp-servers/{id}` (`PATCH /security/permissions` already declares 409); regenerated `pkg/api/generated/openapi_types.gen.go`, `src/lib/api/generated/openapi-types.ts`, `src/lib/api/generated/schemas.ts` in the same commit (backend-lead).

**Effects outside `config.json` come after every refusal (revision 7, W5-01; lifecycle revision 8, R6-02, W6-01).** A `change` callback is pure: it edits the document it is given and has no other effect (no credential store write, no audit, no channel commit). A writer that must also write elsewhere before the new `config.json` exists passes the option `config.BeforeRename(step func(final map[string]any) (undo func() error, err error))` to `UpdateConfigFile`. Its lifecycle:
1. `step` runs inside the lock only after `change` and the Q-UPG1 check have passed, and before the temp file. It also runs when the document is unchanged (a secret-only change keeps the same `*_ref`): no rename follows, the effect stands, and the result is a `config.json` no-op without error, answered with the same success as today.
2. When `step` fails part-way, it undoes its own earlier writes before returning; its error means not saved.
3. `UpdateConfigFile` calls `undo` **only when the result is not committed** (the temp file or the rename failed). After the rename, including an after-commit error (unlock, close, `fileutil.ErrAfterCommit`), `undo` is never called: the change is saved.
4. A failed undo, in `undo` or inside `step`, is **never reported as restored**. `UpdateConfigFile` returns the save error joined with the undo error, the writer logs an Error naming each credential reference that may now differ (names, never values), and the answer says so: REST 500 **"Not saved: the settings file could not be written, and these stored secrets could not be put back, so they may now hold the new value: <names>."**; the tool answers the same sentence.
5. A process stop between the credential write and the rename leaves the new credential value with the old `config.json`, as today. Nothing records it: a recovery record would be stored state.

The MCP writers adopt it. `PATCH /mcp-servers/{id}` (`pkg/gateway/rest_mcp.go::patchMCPServer`) plans in `change` the candidate server and the list of credential values to store, without calling `routeEnvCredentialsThroughStore` or `storeCredential` there. Its `step` snapshots the current values (`peekStoredCredential`), stores the new ones (`storeCredential`), and returns an `undo` built on the existing `rest_config.go::rollbackCredValueSnapshots`: header and environment values alike, and an entry that did not exist before is deleted again. So a refused save leaves `config.json` and every credential value as they were, and the write step is never reached. The same split applies to `POST /mcp-servers` and to the system agent's MCP tools wherever they store credentials.

**Known pre-existing limit, unchanged by this package (R6-02).** The undo restores its snapshot without checking whether another request stored the same credential name meanwhile. A credential-only writer (`pkg/gateway/rest_settings.go::setCredential`) does not take the config lock, so between this step's write and a failed rename it can store a newer value that the undo then overwrites. Today's code has the same window: `patchMCPServer` writes credentials inside its config callback and restores them with `rollbackCredValueSnapshots` when the config write fails. The `step` keeps that window exactly as it is ("credential written, config write then failed") and adds none. Also pre-existing: `credentials.Store.Set` reads the file before taking its sidecar flock, and `Store.mu` is per instance, so two store instances can overwrite each other's updates of different names. Follow-up issue proposed (team-lead files it): "Credential store: make read-modify-write atomic across store instances, and restore a rollback snapshot only if the stored value is still the one this request wrote." This package does not redesign credential transactions.

| Step | What is written | Runs | If Omnipus stops right before or during it |
|---|---|---|---|
| A Agent records | `func skillperm.ConvertAgentRecords(home string, store *agentstore.Store) (converted []string, err error)`: for each record that holds a per-agent `Skill` value, one `UpdateRaw` of that record: when the value is `ask` or `deny` and the inventory of its mounts is complete, it is added as the agent's override for each skill granted to it and each project skill of its mounts, unless that skill already holds a value, and the `Skill` entry is removed; with an incomplete inventory the record is left as it is (deferred); an old `Skill: allow` is removed and not copied (an `allow` override never changes a strictest-wins outcome while the skill has a global value; S-16). The pure part is `func config.ConvertAgentRecordJSON(raw []byte, projectSkills []string, inventoryComplete bool) (out []byte, changed bool, err error)` | called by `(*runContextWithOptions).loadConfigAndProvider` (`pkg/gateway/gateway.go`), where `skillperm.Run` is called today, before the first config load and before the agent loop starts any turn | that record is still old, and the next start converts it; records already converted hold no old-shape value and are left alone; a stray `.tmp-*` file of an interrupted write is not read by anything |
| B `config.json` | inside `UpdateConfigFile`, unless deferred: each installed and project skill's global value = the old global `Skill` value (none when it was absent; then the self-heal gives Ask); trust (C-3); the tool and server levels of Dataset D7 (C-6); `mcp_<S>_* = ask` for each uncovered service (C-18); `Skill` = `allow`; `tools.manifest.compressed`, `tools.manifest.preview_all_lazy`, every `deferred` and the old trust word removed. Each new key is added only when absent: a key that already holds a value is a person's choice and is kept (logged at Warn). The pure part is `func config.ConvertConfigJSON(raw []byte, inv config.SkillInventory) (out []byte, change config.ConfigConversion, err error)` with `type SkillInventory struct { Installed []string; ProjectByWorkspace map[string][]string; Complete bool; Incomplete string }` (filled by `func skillperm.ReadSkillInventory(home string) config.SkillInventory`, registered with `config.RegisterSkillInventory`, like the skill-map heal) and `type ConfigConversion struct { Changed bool; Deferred bool; TrustFrom, TrustTo string; KeptKeys []string }`; with `Deferred` true, `out` is the in-memory view only and is never written. `ConfigConversion` is never serialized and never consulted by a later start | in every config load before the file is decoded, and at the start of every other `config.json` write (the same function), so every command that loads or saves the config sees the new shape | `config.json` is still old, and the next load converts it |
| C Self-heal (C-2) | additive keys only: `func skillperm.HealSkillMaps(home string, m map[string]any) (changed bool, err error)` (today's `HealIfUpgradedIn`, renamed; no done gate); its writes outside `config.json` follow the lock-order paragraph above | in the same `UpdateConfigFile` call, after B; skipped while the content still holds an old-shape value (a deferred file) | the next load heals; the heal never overwrites |

- **Why every stop is safe.** No file needs another file's old value; each write replaces one whole file; no turn runs before steps A and B; every writer reads inside the file's writer lock, so no write is computed from a stale read (NM-01). The order of A and B is not needed for safety (the files are independent); it keeps today's call site. The full argument is in the ruling `/workspace/logs/skills-tools-1519f/arch/ruling-upgrade-no-marker.md`.
- **A file that cannot be written** (the atomic write fails, for example a folder without write permission or a full disk; a read-only file alone does not stop a rename, NM-06): Omnipus logs an Error naming the file at each start and tries again at the next start; the stored bytes stay old. Meanwhile every reader uses the **converted view**, the output of the same conversion function computed from the stored bytes, and never the raw old maps (NM-04): the config load decodes the converted bytes; `ReadPersistedToolCeiling`, `ReadPersistedSkillSettings`, `ReadPersistedVisibility` (`pkg/config/persisted_tool_ceiling.go`) and `pkg/gateway/rest_permissions.go::readSandboxSection` (behind `GET /security/permissions` and the compare step of `PATCH`) read `config.ReadConfigView(path string) ([]byte, error)`, which returns the converted bytes; the agent-store read overlay (`pkg/agentstore/read_overlay.go`) gives a record that still holds a per-agent `Skill` value its converted view. So the screen, the API and the turns show and apply the same values. A save in that state still reports honestly: `PATCH` answers 500 "Not saved: the settings file could not be written." only when the rename did not happen ("Commit point" above), and nothing claims a stored value that was not stored. The self-heal runs on the converted view in memory only and is written with it when the file can be written. An unreadable or malformed agent record (bad JSON, or valid JSON whose `tools`, `tools.builtin.policies` or `tools.skills.policies` is not an object) is left untouched, skipped with a Warn naming the file at each start, and not partly converted; such an agent cannot run today either (`resolveTurnSkillAuthority` fails closed). Nothing is listed on any screen (the former notice is removed).
- **Nothing is left behind.** No snapshot, ledger, progress record, done marker, version marker or upgrade lock exists; no old-shape key is kept "ignored" (an old `Skill` value kept by the inventory rule is still in force, not ignored); the only files the upgrade can leave are the generic `.tmp-*` of an atomic write interrupted by a hard crash, which does not name the upgrade and is never read, and the ordinary writer-lock files every install has. SC-010 checks this.
- **Nothing is inferred from replaced values or other signals.** An old value is read only from the file that holds it, in the same write that replaces it; `Skill: allow` is never read as an old global value on its own; a key that already holds a value is never overwritten; whether a file is converted is never decided from a log line, a file's age, another key or another file.
- **Translation, not backfill.** Step A turns an agent's explicit `Skill` value of `ask` or `deny` into per-skill values; that keeps what the operator said and is required. Nothing is written for an agent that had no `Skill` value or had `allow`, and no tool entry is ever written (C-18): that would be the forbidden backfill (Hard Constraint #6). When the old `config.json` held no global `Skill` value, the skills get Ask from the self-heal and an agent's old `Skill: allow` is not copied, so the result is never looser than Ask (S-16 row 6).
- **Existing versus new skills.** The upgrade keeps existing values. Skills added later start at Ask (ST-Q3 B); built-in skills added by a later release start at Allow (ST-Q10 A).
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
| per-agent skill permission | agent Tools tab | `AgentCreateRequestMain.tools_cfg.skills.policies` (C-4) via `pkg/gateway/rest_agents_create.go::agentCreateToolsCfgFromWire` | `PUT /agents/{id}/tools` `skills`; `PUT /agents/{id}` `tools_cfg` | `create_agent` gains `skill_policy_changes` (`{set}` only), listed in `pkg/sysagent/tools/agent_apply_args.go::agentCreateArgNames` | `set_agent_permissions` `skill_policy_changes` (`{set}`, `{remove}`, `{clear}`); `get_agent` `skill_permissions` | none: installed and project keys are accepted and refused identically on every path (C-23) |
| preservation | — | — | `pkg/gateway/rest_agents_update.go::updateToolsConfig` keeps the stored `skills.policies` when the request's `tools_cfg` omits `skills`, as it already does for `builtin` and `mcp`; `{}` clears | — | `set_agent_permissions` with only tool changes leaves skill overrides untouched | — |
| skill grants | agent Skills tab | `skills` | unchanged | `create_agent.skills` | `update_agent.skills` | unchanged |
| skill category | install review; "Choose category" for an Uncategorised skill (once) | `POST /skills/install` `category`; `POST /skills/{name}/copy` gains `category` (default: the source's) | `PUT /skills/{name}/category` — only while the stored category is `uncategorised` (else 409 "The category is set at install and cannot be changed.") | `install_skill.category`; `edit_skill` `copy` gains `category` | `edit_skill` gains action `set_category`, same once-only rule | none. "Set once" keeps R32/F21 ("no later edit") while giving written and copied skills a reachable path |
| skill global permission, level; tool global permission, level; trust | Skills & Tools | `PATCH /security/permissions` | same | refused | `set_config` refuses `sandbox.*` (existing) | operator-only ceilings |
| retired manifest switches (`tools.manifest.compressed`, `tools.manifest.preview_all_lazy`, `tools.mcp.servers.<id>.deferred`) | — | — | — | — | `set_config` and `PUT /config` refuse a change to them with "This setting was replaced by levels in Skills & Tools."; this replaces, in the same change, the Settings guard for `tools.manifest.compressed` (Settings C-7 `performanceOnlyConfigKeys` and its FR-100 effect guard, which name Settings → Limits), so the key is never unguarded; `PUT /performance` drops `tools_on_demand` (C-22). The keys no longer exist in the stored config after the C-19 conversion; the refusal is by key name and stores nothing (founder rule 2026-11-28) | prevents an agent from overriding every level |
| per-agent `Skill` tool value (founder rule 2026-11-28; contract settled after review NM-03) | a fixed row, `permission_fixed` (C-20): no control | `tools_cfg.builtin.policies.Skill`: `allow` accepted and not stored; `ask`/`deny` refused | `GET /agents/{id}/tools` keeps reporting `Skill: allow` in the complete `builtin.policies` map (a reported value, never a stored override); `PUT /agents/{id}/tools` and `PUT /agents/{id}`: a submitted `Skill: allow` is accepted and not stored, whether or not `override_names` lists it, and a map that leaves `Skill` out is complete too (the complete-map validator `pkg/gateway/submitted_builtin_policy.go::validateSubmittedBuiltinToolPolicyMap` drops `Skill` from its required set when it is not supplied, as it already does for an always-ask tool), so GET → edit another tool → PUT works unchanged and so does a client that omits it; `ask`/`deny` refused | `create_agent` with a `Skill` tool change: `allow` is a no-op, `ask`/`deny` refused | `set_agent_permissions` `tool_policy_changes` naming `Skill`: same as create | deliberate: `Skill` has no permission of its own (C-6), so a per-agent value would mean nothing; its presence is the only old-shape mark of an agent record (C-19). A refused value answers 400 "`Skill` has no permission of its own. Set permissions per skill on the Skills tab." and stores nothing; no writer ever stores a `Skill` entry, and a stored old one is removed only by the C-19 conversion (writers start from the converted record). The core-agent seeds no longer write it. **Package import** (`pkg/omnpkg/import_map.go::mapPolicies`, `pkg/gateway/rest_packages_import.go::planPackageAgent`): a package agent's `Skill: ask` or `deny` is translated by the step A rule over the skills the import keeps (`mapSkills`): each kept skill without its own override in the package gets that value; `Skill: allow` is dropped; the imported record never holds `Skill`; an Ask or Deny is never dropped. A new-shape package without `Skill` imports unchanged: the shared create builder accepts a map without `Skill` by the same validator rule |

The write-skill workflow (`pkg/skills/embedded/write-skill/SKILL.md`) gains: the card asks for a category; after writing, Omnipus calls `edit_skill` `set_category`. Category is never written into the skill file.

Parity tests: `TestSetAgentPermissions_SkillPolicyChangesParity`, `TestCreateAgent_SkillPolicyChangesParity`, `TestUpdateAgentToolsCfg_PreservesSkillPolicies` (REST update without `skills` keeps them), `TestSkillKeyValidation_AllWriters` (Dataset D16: every writer with an installed key, a project key, an unknown key, a key of a workspace the agent is not mounted on, `null`, empty and clear, then a read-back through `get_agent` and `GET /agents/{id}/tools`), `TestInstallSkillCategoryParity`, `TestEditSkill_SetCategoryOnce`, `TestWriteSkillWorkflow_PublishesCategorisedSkill` (scripted model, W5 through to a listed skill with its category), and the `set_config` refusal rows for the `sandbox.*` keys and the retired manifest keys. A parity test fails when any user-editable property lacks the agent path or its tested refusal.

### C-22 Landing on top of Settings (round-1 fix MAJ-007; rewritten in round 2, R2-MAJ-003)

**Order (settled, not a founder question).** Settings lands first; this package lands after it (issue #1963). This package's landing branch is rebased onto the Settings landing at its landed commit; until then the reference is `landing/settings-1519` at `8e5848c951`, read for this section. Both packages ship in the same release.

**The Settings base at `8e5848c951`** (✅ Verified by reading the tree with `git show` / `git grep`):

| Element | State on the base | Remaining users |
|---|---|---|
| Global tool grid (`SecuritySection.tsx::GlobalToolPoliciesSection`) | rendered only in `SecuritySection`'s legacy layout (no `view`); `SettingsSectionBody.tsx` always passes `view="protection"` or `view="keys"`, so it is not shown | `GET /security/tool-policies` is still read by the agent tab (`src/components/agents/ToolsAndPermissions.tsx` via `fetchGlobalToolPolicies`) |
| Skill trust control (`SkillTrustSection.tsx`) | rendered only in the legacy layout; not shown | none on screen |
| Protection notice | "Tool and skill permissions moved" with "Open Skills & Tools" (`/skills`) | — |
| Web search (`WebSearchGroup` inside `IntegrationsSection`) | shown under Providers → "Voice input and web search" | — |
| "Load tools on demand" | shown in Limits (`PerformanceSection.tsx`: `CONFIG_KEYS`, label, default, field-path map to `tools.manifest.compressed`); `tools_on_demand` in `PerformanceSettings`, `PerformanceSettingsUpdate`, `PerformancePendingApply.changed_fields`, `PerformanceReloadFailedDetails.changed_fields`; label in `src/lib/api/config.ts`; read, written and reported by `pkg/gateway/rest_performance.go` | Settings' tests and `TestSettingsAgentWriteParity` |
| `set_config` / `PUT /config` on `tools.manifest.compressed` | refused (Settings C-7 `performanceOnlyConfigKeys`; FR-100 effect guard), message naming Settings → Limits | — |

**Between the two landings** (release branch only): the global tool grid and skill trust have no screen and can be changed only through the API; web search stays under Providers; the Protection notice points to `/skills`, whose old screen does not hold them yet. The release must not be cut in that state (release rule below). Whether the Settings package should keep a stopgap is team-lead's call (Notes for team-lead).

**What this landing does, as one change with its tests:**

| # | Change | Owner |
|---|---|---|
| 1 | The Skills & Tools page holds the global tool grid, skill trust and Web & Search (this spec) | this package |
| 2 | Removes the loading switch from Limits: the `PerformanceSection.tsx` row, `CONFIG_KEYS` entry, label, default and field-path entry; the `src/lib/api/config.ts` label; `tools_on_demand` from `PerformanceSettings`, `PerformanceSettingsUpdate` and both `changed_fields` enums, with regenerated types; `rest_performance.go` stops reading, writing and reporting it. A request still carrying `tools_on_demand` gets the schema's 400 (`additionalProperties: false`). Saving the other Limits values keeps working | this package |
| 3 | Moves the guard: `set_config` and `PUT /config` refuse `tools.manifest.compressed`, `tools.manifest.preview_all_lazy` and `tools.mcp.servers.<id>.deferred` with "This setting was replaced by levels in Skills & Tools."; the Settings entries for `tools.manifest.compressed` (`performanceOnlyConfigKeys`, the FR-100 guard list) are removed in the same change, so the key is never unguarded | this package |
| 4 | `TestSettingsAgentWriteParity` loses "Load tools on demand" in the same change that removes the control; the retired keys get their own refusal tests (C-21) | this package |
| 5 | Removes `WebSearchGroup` from Providers (the group title becomes "Voice input"); Settings search answers "web search" with "moved to Skills & Tools" and "load tools on demand" with "replaced by levels in Skills & Tools", each with a link | this package, as named edits in Settings files |
| 6 | Retires `GET/PUT /security/skill-trust` (handler, three schemas, inbound schemas, `fetchSkillTrust`/`updateSkillTrust`, `SkillTrustSection.tsx`) and `GET/PUT /security/tool-policies` (handler, `GlobalToolPolicies`, inbound schema, `fetchGlobalToolPolicies`/`updateGlobalToolPolicies`), after moving the agent tab to `GET /security/permissions`; deletes `SecuritySection`'s legacy layout. A guard test fails if any file in `src/`, `pkg/`, `cmd/` or `tests/` still calls either path | this package |
| 7 | Links from this page use the post-Settings section ids: Auto-approve → `/settings?tab=protection`; God Mode → `/settings?tab=protection&focus=god-mode`; Shell safety rules → `/settings?tab=sandbox` | this package |
| 8 | The Protection notice stays as Settings built it; it is true from this landing on | — |

Release rule: the release must not be cut with Settings landed and this package not landed, because the grid and trust would have no screen. Both are planned for v0.1.1; the chief checks it at the release cut.

A stored old trust word is converted by the C-19 `config.json` conversion, in the same write as the other old values (C-3, founder rule 2026-11-28); the round-1 two-way mapping of the legacy trust endpoint is no longer needed, because that endpoint is retired (row 6).

### C-23 One known-skill-key rule for every writer (round-2 fix, R2-MAJ-007)

One function beside `pkg/skills/shelf.go::ResolveSkillName` decides every skill key on every path (global PATCH, REST agent create and update, `create_agent`, `set_agent_permissions`):

| Key form | Global writer (`PATCH /security/permissions`) | Per-agent writers |
|---|---|---|
| installed skill id (built-in, registry, uploaded, written), any letter case | valid; stored lower case | valid when the skill is installed, granted or not (an override may be set before the grant, as for tools today) |
| `project:<W>/<slug>` | valid when workspace `W` exists and `<slug>` is on its project shelf, or a stored key for it already exists | valid when the agent is mounted on `W` and the key is valid globally |
| anything else | 400 "`<key>` is not a known skill." | 400 "`<key>` is not a skill this agent can use." |

| Request part | REST `PUT /agents/{id}/tools`; `tools_cfg` on create and update | `create_agent` | `set_agent_permissions` |
|---|---|---|---|
| omitted | unchanged (update); none (create) | none | unchanged |
| set | `skills.policies` is the complete set of overrides | `skill_policy_changes: {set: {key: value}}` | `skill_policy_changes: {set: {key: value}}` adds or changes |
| remove one | leave it out of the complete set | — | `{remove: [key]}` |
| clear | `skills.policies: {}` | — | `{clear: true}` |
| `null` anywhere | 400 naming it | 400 naming it | 400 naming it |
| unknown key, or a project key of a workspace the agent is not mounted on | 400 (table above), nothing stored | same | same |
| the same skill twice (letter case) | 400 "`<key>` is listed twice." | same | same |

- A deleted workspace or a removed project folder: its `project:` keys leave `sandbox.skill_policies`, `sandbox.skill_visibility` and every agent's `skills.policies` at the next reconcile (C-19); a later request naming such a key gets the unknown-key 400.
- Unrelated writes keep skill overrides on every path (C-21).
- Reads (`get_agent` `skill_permissions`, `AgentToolsResponse.skills`) list the granted installed skills and the project skills of the agent's mounts, each with its key.

### C-24 Skill approval actions: the wire contract (round-2 fix, R2-MAJ-002; timed choices per R2-Q1, 2026-11-22)

| Schema / endpoint | New / Changed | File | Notes |
|---|---|---|---|
| `ToolApprovalActionRequest.action` | changed | `contracts/components/schemas/ToolApprovalActionRequest.yaml` (and its copy in `pkg/gateway/inboundschemas/`) | the enum gets C-26's `allow_for` (in place of `allow`) and the two skill actions `approve_version` and `approve_version_allow_for`. `approve_version_allow_for` needs C-26's `duration`. The body still carries only the action, `duration` and the existing bash `scope`; the client never sends a skill key or revision |
| `ToolApprovalResponse` | changed | `contracts/components/schemas/ToolApprovalResponse.yaml` | `action` enum as the request; C-26's `expires_at`; adds optional `applied` `{call_released, timed_allowance, trust_approved}` (booleans) and optional `not_applied_reason` (string). `applied` is always present for a skill approval; it reports what was stored, never what was asked |
| `POST /skills/{name}/trust-approval` | new | `contracts/openapi.yaml` | More actions → "Approve this version". Body `SkillTrustApprovalRequest` `{revision}` (the revision the screen showed); 200 `SkillTrustApproval`; 409 "This skill changed since the page loaded. Review it again." when the current revision differs; 404 unknown skill; 400 "This skill is verified; it needs no approval." for a verified skill. A person's action only: no agent tool reaches it |
| `SkillTrustApprovalRequest`, `SkillTrustApproval` | new | `contracts/components/schemas/` | `SkillTrustApproval`: `key`, `revision`, `approved_by`, `approved_at`, all server-derived |

Server rules — one handler path, `pkg/gateway/rest_tool_registry.go::HandleToolApprovals`, extended; not a second approval system:

| Rule | Detail |
|---|---|
| Offered actions | The server computes `offered_actions` and `allow_durations` (C-26) when it raises the card, from the C-12 decision table, and accepts only those. Permission part open alone: `allow_once`, `allow_for`, `deny`. Trust part open alone: `approve_version`, `deny`. Both open: `approve_version`, `approve_version_allow_for`, `deny`. Install card (C-12 row 6): `allow_once`, `deny`. A card for an exact-action tool (C-26 point 9) or an always-ask tool never offers `allow_for`; an `allow_for` answer to one → 409 "This choice is not available for this request." (exact-action tools) or today's 400 (always-ask tools). `allow_durations` is `[1h, 3h, 1d]` whenever a `*_allow_for` action is offered and empty otherwise. Any other action → 409 "This choice is not available for this request.", nothing stored |
| Binding | The pending approval's stored entry (agent, session, tool, skill key, revision) is immutable, and every effect uses it. The timed allowance is recorded for the entry's agent, never for the agent of the chat the card is shown in. The acting person is the signed-in identity of the request (`approved_by`). Agents cannot answer approvals (existing rule) |
| Re-check before any effect | Under the lock that serialises trust-approval writes: the skill's current revision equals the entry's revision; its current outcome for that agent is not `deny`. A failed check → 409 with the reason ("The skill changed after this request." / "This skill is now set to Deny."), nothing stored, and the waiting call is refused with the same reason |
| Order of effects | (1) the trust approval, when the action includes it (durable write); (2) the timed allowance, for `allow_for` and `approve_version_allow_for` (in memory, C-26); (3) release the waiting call. Each step starts only after the previous one has finished |
| Partial failure | (1) fails → nothing else happens; 500 "Could not save the approval. Nothing was changed."; the card stays open for another answer until the approval times out. (2) fails after (1) → the call is still released (its consent is complete: trust approved, this call approved); `applied.timed_allowance` is false and `not_applied_reason` reads "Approved for this request only. The time limit was not saved: <reason>."; the card shows that line |
| Replay and expiry | An answered, timed-out or unknown approval id → the existing 404 "approval … not found"; nothing happens twice. A retry after a 500 uses the same id while it is still pending |
| Audit | every trust approval writes `skill.trust_approved` with key, revision and actor; a timed allowance writes the C-26 audit entry (agent, subject, duration, end time, actor), and a permission change that ends it writes `approval.allowance_ended`. No stored permission changes, so no `sandbox.skill_policies` entry is written by a card |
| Scope | A timed allowance from a skill card covers that agent's use of that skill — the permission part only — in all its chats, its task, plan and scheduled runs and its own helpers, until it ends or the skill's permission changes (C-26). It never counts as a trust approval, of this or a later revision, and never changes a stored permission. The card shows under the time choices: "Covers <agent> in all its chats, tasks and its own helpers until the time ends or Omnipus restarts. Other agents still ask." |

Approve Once keeps today's meaning. Neither Approve Once nor a timed allowance records a trust approval or changes a stored permission.

Trust approvals are stored in `$OMNIPUS_HOME/system/skill-trust-approvals.json`, inside the protected inventory name `system` (`pkg/fspolicy/inventory.go::ProtectedInventoryNames`), so agents cannot read or change it on either layer; written atomically; one record per (key, revision).

### C-25 Project-skill paths under mounts (round-2 fix, R2-CRIT-002)

Today a mounted project's `.omnipus/skills/<slug>/` and `.claude/skills/<slug>/` (`pkg/skills/project.go::projectSkillsDirs`) are ordinary mount files: the home guard has no opinion outside `$OMNIPUS_HOME` (`pkg/fspolicy/homeguard.go::homeGuardDeny`); the installed-shelf rule covers only `$OMNIPUS_HOME/skills` (`::inventoryDenies` → `::skillPathDenied`); and reads under a mount need no kernel grant (open read model, `pkg/sandbox/derive_from_fspolicy.go::DeriveKernelPolicy`). Filtering the installed loadable set does not stop a project skill being read. This package adds a project-skill gate.

| Topic | Rule |
|---|---|
| Path identity | A project-skill folder is the real path of `<mount root>/.omnipus/skills/<slug>` or `<mount root>/.claude/skills/<slug>`, resolved inside the mount root the way discovery resolves it (`pkg/skills/project.go::realWithinRoot`). Containment uses the existing identity functions (`pkg/fspolicy` `CoversForDeny`, `CoversForGrant`), so case, Unicode-normalisation and symlink spellings of one folder get one answer; a symlink inside a skill folder is judged at its target by the existing resolution |
| Which folders | For the acting agent and turn: the skill folders of every mount the turn can reach — the agent's workspace mounts, the turn's work dir when it is a mount root or inside one, and nested mounts (each mount's own `.omnipus/skills` and `.claude/skills`; an outer mount's skill folders stay gated when an inner mount is reached). Computed once per turn by `resolveTurnSkillAuthority`, beside the installed folders, from the outcome (C-11) of key `project:<W>/<slug>` |
| What is gated | Reading a file (every path-taking tool, `grep` and search, directory listings through `fspolicy.VisibleDirectoryEntries`) and running a helper from the folder (`pkg/tools/shell_skill_operands.go::loadableSkillReadOrRun` and the bash operand checks). Outcome `allowed` → open, as today. `ask_pending` → refused with "`<slug>` is a project skill this agent may not use yet. Approve it when it is loaded, or change its permission in Skills & Tools."; `deny` → refused naming the setting, as for installed skills. Omnipus reads `SKILL.md` frontmatter for the menu itself, never through the agent's file tools |
| Precedence | The gate runs before every allowance that could re-admit the path — workspace mounts, the work dir and its own-tree exception, path grants (ADR-092 D7) and the God Mode floor's work-tree widening — the same position the installed-shelf rule has today ("inventoryDenies runs before every mount, path grant, setup root and work dir"). A broader grant never re-opens a gated folder |
| Writes | Unchanged: writes follow the mount's existing write rules and the `edit_skill` composition for skill writes (`pkg/tools/shell_skill_operands.go::SkillWriteTarget`). A write makes a new revision, so the trust check asks again (ST-Q8 A) |
| Editor exception | An agent whose `edit_skill` is not Deny may read project-skill folders for editing, as for installed folders (C-11): read only, never a load, never consent, never a helper run |
| Next turn | A permission, trust, approval or mount change applies from the next turn (C-15). A file already read in an earlier turn cannot be withdrawn |
| Kernel layer | The app-level gate is the contract on every platform. Where a kernel sandbox is enforcing, the gate is also rendered into the kernel policy with the existing deny mechanisms: on macOS as explicit Seatbelt denies of the gated folders (the existing denied-paths rendering); on Linux by extending the existing deny enumeration (`pkg/fspolicy/secretset_kernel.go::KernelDenyEnumeration`, today limited to branches under `$OMNIPUS_HOME`) to mount branches, so a gated folder is left out of the read grants. A case the Linux extension cannot express is app-level only, logged at Warn once per turn with the folder, and never reported as kernel-enforced. Windows, Linux before 5.13 and a sandbox that failed to start are app-level only (Hard Constraint #4). On an app-level-only path, `bash` can reach a gated folder through command text the operand checks cannot see (for example a generated path); this is the same accepted limit as every other app-level-only protection and DOC-004 states it |

The Linux kernel extension is 🔶 Inferred (medium confidence): the enumeration already denies by leaving siblings out of the grant under the home; doing the same under a mount root is the same technique but has not been built or measured. ST-28 records, per platform, whether the gate is kernel-enforced or app-level only.

### C-26 Timed allowances replace "Always Allow" on every approval card (founder R2-Q1, 2026-11-22)

**The founder's rule** (Discord #team-b, 2026-11-22, 03:34–03:55Z, relayed by team-lead; points 1–4 the founder's, point 5 team-lead's defaults, told to the founder and not contradicted):

1. No approval card offers "Always allow", "Always Allow" or "Allow everywhere" any more, for tools and for skills. Cards offer Approve Once; Allow for 1 hour / 3 hours / 1 day; and Deny. Tools that today always ask with no remember option stay exactly as they are, and so does the skill trust check for unchecked revisions.
2. A timed allowance is bound to the **agent**, not the chat. It covers all of that agent's chats and the helpers the agent starts for itself (self-delegation). Other agents are unaffected. On the narrow cards it covers exactly the same narrow thing as today, now per agent and timed: one file path (with its access), one network address, or one shell-command family. On a skill card it covers that skill's permission part only, never a trust approval of a later revision.
3. A timed allowance ends at its time, or when the gateway restarts, and then the next matching call asks again. It is not saved as a setting; there is no new storage object.
4. A lasting Allow exists only as stored policy: the agent's own policy for the tool or skill (its Tools tab) under a global value of Allow (Skills & Tools). The global policy always wins, as today: an agent cannot be Allow where the global value is Ask or Deny. Where the screen offers to set an agent to Allow while the global value is Ask, that control is disabled, with a tooltip saying the global policy must be Allow first and a link to it.
5. A timed allowance is a person's answer to an Ask, so it works wherever Approve Once works — under a global Ask and under the agent's own Ask; under Deny no card appears. Unattended task, plan and scheduled runs honour a timed allowance that is still running (replacing today's per-chat "Always Allow grant" wording), and they still never ask or wait.

**Follow-up rulings, 2026-11-22** (founder Q4 B at 05:07Z and team-lead's calls, relayed by team-lead):

6. On an **ordinary tool card** a time choice covers the **whole tool** for that agent until it ends — like a timed agent-level Allow, whatever the arguments (founder Q4 B). The narrow cards stay narrow: one path with its access, the named network address(es), one shell command or command family. A skill card's time choice covers that skill's permission part. The global value still wins: an allowance answers an Ask and never lifts a Deny.
7. Any change of a tool's or a skill's stored permission — global or for one agent, including setting it back to Ask — ends that tool's or skill's running allowances for every agent the change affects, and each ended allowance is audited (team-lead).
8. The server keeps today's behaviour for a looser agent value: it may be stored, the stricter global value wins at run time, and only the screen blocks it (team-lead).
9. **Exact-action tools** (founder Q5 A, 2026-11-22 06:20Z): `environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config` keep a card for every action. Their cards offer Approve Once and Deny only, with no time choices, so the person always sees the exact action (the ADR-090 promise that the exact command is shown). A timed allowance can never cover them, and the server refuses `allow_for` for them through the offered-actions rule (C-24).

Unchanged: strictest wins (C-0); God Mode lifts a global Ask and never an agent's own Ask or Deny; Auto-approve and its order (checked before the unattended refusal, C-12); Approve Once; Deny; Cancel where a card has it today; operator `command_rules`; `omnipus run --yes` (sends `allow_once`, `pkg/app/internal/run/run.go::resolveApproval`).

**Which card offers what**

| Card | Today | After this package | What a time choice covers, for that agent until it ends |
|---|---|---|---|
| Ordinary tool card (any tool at Ask) | Approve Once, Deny, Always Allow, Cancel | Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny, Cancel | the **whole tool**, any arguments (founder Q4 B; wider than today's exact-call grant) |
| Shell card | the same, with the exact / prefix choice for Always Allow | the same, with the exact / prefix choice applied to the time choice | the exact command, or the command family (`ShellPrefixGrant`: resolved program, argument prefix, foreground or background) |
| Shell file-access pre-flight card (D7) | Approve Once, Always Allow (one path) | Approve Once, Allow for …, Deny | that one path with the access the command needed |
| Shell network pre-flight card naming addresses (D8) | Approve Once, Always Allow (those addresses) | Approve Once, Allow for …, Deny | exactly the named addresses |
| Shell network pre-flight card with no address readable | Approve Once, Always Allow (ports 80, 443, 53 to any address) | Approve Once, Allow for …, Deny | the same as today: the usual web and name-lookup ports to any address, for that agent, until the time ends (flagged for security review below) |
| Skill permission card (C-12 row 2) | — (new in this package) | Approve Once, Allow for …, Deny | that skill's permission part for that agent |
| Skill trust card (C-12 rows 3, 8) | — | "Approve this version", Deny | — (no time choice) |
| Skill combined card (C-12 row 4) | — | "Approve this version", "Approve this version and allow for" 1 hour / 3 hours / 1 day, Deny | the permission part, as the skill permission card; the trust approval is stored per revision as before |
| `install_skill` card for an unverified skill (C-12 row 6) | — | "Install and approve this version", Deny | — |
| Exact-action tools (`environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config`; founder Q5 A) | Approve Once, Deny, Always Allow, Cancel | Approve Once, Deny (and Cancel where the card has it today); no time choice; every action shows a card when the tool resolves to Ask | — (never covered) |
| Always-ask tools (`pkg/tools/always_ask.go::AlwaysAskTools`, today exactly `set_channel_outbound`) | Approve Once, Deny, Cancel; "This approval applies to this one request." | unchanged | — |

**Lifetime and scope**

| Topic | Rule |
|---|---|
| Key | (agent id, kind, subject). Subject: the tool name for an ordinary tool card; the command or command family, the path with its access, or the address set for the shell cards; the skill key for a skill card. The session id is no longer part of the key. The agent is the pending approval's `entry.AgentID` — the agent named on the card — also when the card is shown in another agent's chat (a helper's card in the root chat) |
| Covers | every session of that agent: its chats in every workspace, group-chat steps, channel conversations, task, plan and scheduled runs, and helpers it starts for itself (same agent id). A helper of another agent, or a delegation to another agent, is not covered and asks as usual |
| Length | `1h`, `3h` or `1d` (24 hours), measured on the server's monotonic clock from the moment the allowance is recorded. `expires_at` reports the end in wall-clock time |
| A second answer for the same subject | the later end time wins; an answer never shortens a running allowance |
| End | at the end time; at a gateway stop or restart (the store is in memory); when the agent is deleted (`pkg/gateway/rest_agents.go::deleteAgent` and the `pkg/sysagent/tools/agent.go` delete paths drop its allowances); or when the permission it answers changes (next row). There is no list to review running allowances, as today |
| A permission change | Any write that changes the stored permission of a tool or skill ends that tool's or skill's running allowances (every kind: for `bash` that includes its command, command-family, path and address allowances) for every agent the change affects: a global change → every agent; a change of one agent's own value, including clearing it (Inherit) → that agent; an external server's wildcard `mcp_<server>_*` → every tool of that server. Setting the same value again is not a change and ends nothing. Writers: `PATCH /security/permissions` (C-2), `PUT /agents/{id}/tools` (`pkg/gateway/rest_tools.go::updateAgentTools`), `set_agent_permissions` (`pkg/sysagent/tools/agent_permissions.go`), and a config reload that changes a stored value. The ending happens after the write is durable and before the response; each ended allowance writes one audit entry. Ending a `bash` allowance also revokes that agent's live egress tokens. Trust (`sandbox.skill_trust`), levels, Auto-approve and God Mode are not permissions and end nothing |
| Deny | a Deny anywhere (global, the agent's own) refuses before any allowance is looked at, as today; an allowance never lifts a Deny. Setting the Deny ends the allowance (previous row), so lifting it back to Ask brings a card again |
| Decision time | an allowance is checked when a call is decided. A call released before the end keeps running. A shell command started before the end keeps the kernel policy it started with; at the end Omnipus revokes the agent's live egress-proxy tokens (`pkg/sandbox/egress_proxy.go::EgressProxy.RevokeRunToken`, which exists today; 🔶 Inferred that this is enough), so a running command opens no new connection to an expired address; the agent's next command gets a fresh token with the addresses still allowed |
| Unattended | `resolveAskPolicy` consults Auto-approve and standing allowances before the unattended refusal, as it does today with grants; a running allowance lets the call run; nothing asks or waits |
| Storage | none. No file, no config key, no agent field. The founder ruled out a new storage object for a lasting allow |

**How a timed allowance reaches a helper run.** Because the key is the agent, not the session, a helper the agent starts for itself looks up the same allowances with no copy at spawn. `pkg/agent/loop_policy.go::inheritSessionPermissions` therefore stops copying grants (`ApprovalGrantStore.InheritFrom` is removed or reduced to egress-token bookkeeping) and keeps copying only the per-chat Auto-approve modifier (`SessionModes().InheritFrom`, unchanged). The extra record under the parent's session in `pkg/gateway/rest_tool_registry.go` (the "record it under the acting session's own immediate parent's session id" fix) is no longer needed and is removed. A helper's shell command reads the agent's path allowances and allowed addresses at spawn (`PathGrantsFor`, `NetworkHostsFor`, now keyed by agent) and registers the addresses on its own session's egress token through the existing idempotent re-push in `enforceNetworkPreflight`.

**Contract changes (Hard Constraint #8: written into `contracts/` before any Go or TypeScript, then `scripts/gen-contracts.sh`, generated files committed with the schema change)**

| Schema | New / Changed | File(s) | Change |
|---|---|---|---|
| `ApprovalAllowDuration` | new | `contracts/components/schemas/ApprovalAllowDuration.yaml` | string enum `1h`, `3h`, `1d` |
| `ToolApprovalActionRequest` | changed | `contracts/components/schemas/ToolApprovalActionRequest.yaml`, `pkg/gateway/inboundschemas/ToolApprovalActionRequest.yaml` | `action` enum: `deny`, `allow_once`, `allow_for`, `cancel`, `approve_version`, `approve_version_allow_for` (C-24). `allow` is removed. New `duration` (`ApprovalAllowDuration`), required when `action` is `allow_for` or `approve_version_allow_for` and refused otherwise. `scope` (`exact`, `prefix`) keeps its meaning and now applies to `allow_for` on a bash call. The description names the tools that never take `allow_for`: always-ask tools (400) and the exact-action tools (409 through `offered_actions`) |
| `ToolApprovalResponse` | changed | `contracts/components/schemas/ToolApprovalResponse.yaml`, `pkg/gateway/inboundschemas/ToolApprovalResponse.yaml` | `action` enum as above; `grant_recorded` and `scope` now describe the timed allowance (present only for a `*_allow_for` action); new `expires_at` (date-time, server-derived) when an allowance was recorded; C-24's `applied` and `not_applied_reason` |
| `ToolApprovalRequiredFrame` | changed | `contracts/components/schemas/ToolApprovalRequiredFrame.yaml`, the inline copy in `contracts/asyncapi.yaml`, `pkg/gateway/inboundschemas/ToolApprovalRequiredFrame.yaml` | new required `offered_actions` (array of the action enum) and `allow_durations` (array of `ApprovalAllowDuration`, empty when no time choice is offered); both server-computed. Plus the C-9 `skill` object |
| `SessionStatePendingApproval` | changed | `contracts/components/schemas/SessionStatePendingApproval.yaml`, the inline copy in `contracts/asyncapi.yaml`, `pkg/gateway/inboundschemas/SessionStatePendingApproval.yaml` | the same `offered_actions`, `allow_durations` and `skill`, so a card rebuilt after a reload offers exactly what the server accepts |
| `ToolApprovalResolvedFrame` | description only | its two copies | the description's "(approve, deny, cancel, always)" becomes "(approve once, allow for a time, deny, cancel)"; no field change |

Refusals on `POST /tool-approvals/{approval_id}`: `action: allow` (a screen from before the upgrade) → 400 "This choice is no longer offered. Choose Approve Once or a time limit."; a `*_allow_for` action without `duration`, or with another value → 400 "Choose 1 hour, 3 hours or 1 day."; `duration` with any other action → 400 "A time limit applies only to Allow for."; an action not in the card's `offered_actions` → 409 "This choice is not available for this request." (C-24). An always-ask tool keeps today's 400 for a remembering action, now for `allow_for` (checked before the offered-actions rule): its approvals are never remembered. An exact-action tool answered with `allow_for` → 409 "This choice is not available for this request." (offered-actions rule, C-24), nothing stored.

**Audit.** `shell.grant_recorded` (`pkg/audit/shell_permission_events.go`) gains `duration` and `expires_at`; its scope taxonomy (exact, prefix, path widening, network widening) is unchanged. A non-shell timed allowance writes the existing approval-decision entry with `action: allow_for`, `duration`, `expires_at`, the agent and the tool or skill key. An allowance that reaches its end time writes no entry (the record entry already states the end time). An allowance ended by a permission change writes `approval.allowance_ended` with agent, subject, the changed key, old and new value, the actor of the change and reason `permission_changed`.

**Code paths that change (impact analysis; 🔶 Inferred from `grep`/`Read` at `4e34afa416`, GitNexus not available)**

| File::symbol | Today | Change |
|---|---|---|
| `pkg/security/approvalgrants.go::ApprovalGrantStore` (`grantKey`, `Record`, `IsAllowed`, `RecordPrefixGrant`, `IsPrefixAllowed`, `RecordPathGrant`, `PathGrantsFor`, `RecordNetworkGrant`, `RecordNetworkGrantWithHosts`, `HasNetworkGrant`, `NetworkHostsFor`, `RecordNetworkHosts`) | keyed by (session, agent); no expiry; "in-memory-only, cleared on session close" | keyed by agent; every entry carries its end time; every lookup ignores ended entries; every `Record*` takes the duration and returns the end time; for a non-bash tool `Record`/`IsAllowed` key by tool name and no longer use `fingerprintArgs` (the exact fingerprint stays only for the bash exact scope) |
| new `pkg/tools/exact_action.go::ExactActionTools` and `IsExactAction` (beside `always_ask.go`) | — | the five names; read by the offered-actions computation, by `approvalGrantRecorder` (records nothing for them, as for always-ask tools) and by `checkStandingGrant` (never settled by an allowance), so no path can cover them |
| new `ApprovalGrantStore.EndFor(agentIDs, key)`, called by every permission writer (`PATCH /security/permissions`, `updateAgentTools`, `set_agent_permissions`, config reload) | — | ends the matching allowances, returns them for the audit, and (for `bash`) the caller revokes the egress tokens |
| `ApprovalGrantStore.ClearSession`, `pkg/agent/session_end.go` | clears every grant of the session | no longer clears allowances (they belong to the agent); still revokes the session's egress tokens (`NetworkTokensForSession` → `RevokeRunToken`) |
| `ApprovalGrantStore.InheritFrom`, `pkg/agent/loop_policy.go::inheritSessionPermissions` | copies grants parent → child, also to a different agent | copies no grants (self-helpers share the key; other agents get nothing); Auto-approve modifier copy unchanged |
| new `ApprovalGrantStore.ClearAgent`, called from the agent delete paths | — | drops a deleted agent's allowances and revokes its tokens |
| expiry sweep (new, in `pkg/agent`, which can reach both the store and the proxy) | — | at each end time, revokes that agent's live egress tokens |
| `pkg/agent/loop_policy.go::checkStandingGrant` | `IsAllowed(session, agent, …)` and prefix check | the same checks keyed by agent; always-ask guard unchanged |
| `pkg/gateway/rest_tool_registry.go::approvalGrantRecorder`, `::HandleToolApprovals` | records an "allow" under the acting session and its parent session | records one timed allowance for `entry.AgentID` with the requested duration; computes and enforces `offered_actions`; returns `expires_at` |
| `pkg/gateway/approvals.go` (the "action was literally allow" flag, `ApprovalActionApprove` mapping) | `allow` vs `allow_once` share one state | `allow_for` vs `allow_once`; the flag carries the duration |
| `pkg/agent/tool_approver.go` | records on `allow` | records on `allow_for` with the duration |
| `pkg/tools/shell_permission_mode.go` (`requestPreflightApproval`, the D7 and D8 pre-flights, `grantEgressHosts`, prefix checks) and `pkg/tools/shell_no_sandbox_gate.go` | `recordGrant` bool; lookups by session | duration instead of the bool; lookups by agent; the egress token stays per (session, agent) and is seeded from the agent's allowed addresses |
| `pkg/audit/shell_permission_events.go` | grant-recorded event without time | adds `duration`, `expires_at` |
| `src/components/agents/ToolApprovalPanel.tsx`, `ToolApprovalModal.tsx`, `approvalPreviews/types.ts` | "Always Allow" ghost button, exact / prefix select, "Always allowed — … for this session." | renders the server's `offered_actions` and `allow_durations`; the three time choices; the scope line; the result line "Allowed for <agent> until <time>." |
| `src/lib/alwaysAskTools.ts` | comment says "never Always Allow" | comment wording only; behaviour unchanged |
| `src/components/shared/ToolPolicyEditor.tsx` (`lockTitle`, `isPolicyLocked`) | "Locked by a global … policy in Settings → Security …" | the FR-023 tooltip and link (US-3 AS2) |

**Migration of grants that exist at upgrade.** Today's grants live only in memory (`ApprovalGrantStore`), so the upgrade's restart ends every per-chat grant; nothing is converted and nothing is written. The first matching call after the upgrade asks again and offers the time choices. Approvals pending at the restart end as `denied_restart`, as today. (Team-lead's default "they simply end"; this is also the only possible outcome, because nothing is stored.)

**Security-relevant change — for the security-lead's one review** (founder rule "functionality first"; focus areas `pkg/security`, `pkg/sandbox`, `pkg/audit`). Points to review, none of them a finding yet:

| # | Point |
|---|---|
| 1 | The scope grows from one chat to every session of one agent — every workspace it works in, its channel conversations and its unattended runs — for up to a day. |
| 2 | There is still no list of running allowances. One ends early only through a restart, deleting the agent, or a change of that tool's or skill's permission (audited). |
| 3 | A time choice on a network card with no readable address opens ports 80, 443 and 53 to any address for that agent until the end (today: for one chat). |
| 4 | A path allowance from one workspace's chat also covers the agent's sessions in other workspaces. |
| 5 | Narrowing: a delegation to another agent no longer inherits the delegating chat's grants. |
| 6 | Ending a network allowance relies on revoking egress tokens of running commands (🔶 Inferred sufficient). |
| 7 | **One timed click on an ordinary tool card allows the whole tool for that agent, with any arguments, for up to a day** (founder Q4 B), in every session of the agent including unattended runs. Today the same click allows one exact call in one chat. It does **not** apply to the five exact-action tools (`environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config`; founder Q5 A, point 9), which never get a time choice. It still includes other tools that always ask under Auto-approve (`pkg/tools/auto_approve.go`, class `AutoAsks`), for example `delete_task` (any task), `set_agent_permissions` and `install_skill` (any skill that needs no trust card; kept as written, team-lead 2026-11-22). |
| 8 | A permission change ends allowances only on the writers listed in C-26; a write path not on that list would leave an allowance running (test 63 covers each writer). |
| 9 | The exact-action list is a fixed list in code (`ExactActionTools`); a new tool with the same risk is not on it unless added (test 64 guards the five). |

### Collisions checked

- `skill_policies` keys are skill ids; they never enter `sandbox.tool_policies`, so the static-catalog validators (`ValidateSubmittedToolPolicyMap`, `config.go`/`validate.go:592`) and the `mcp_` carve-out are unchanged.
- No existing schema named `ModelVisibility`, `SkillCategory`, `AgentSkillEntry`, `AgentSkillPermission`, `PermissionSettings`, `PermissionSettingsPatch`, `SkillInstallReview`, `SkillInstallReviewStatus`, `ApprovalAllowDuration`, `SkillTrustApproval` or `SkillTrustApprovalRequest` (`ls contracts/components/schemas`, round 2), and no existing path `/security/permissions`, `/skills/install/review`, `/skills/install/review/{review_id}`, `/skills/{name}/trust-approval` or `/skills/{name}/category` in `contracts/openapi.yaml` (round 1, `grep`).
- The Settings package uses the four Performance schemas this package edits (C-22 row 2); this package edits them only after Settings has landed, on top of its version.

## API and Data

| Endpoint / event | Change |
|---|---|
| `GET/PATCH /security/permissions` | new (C-2): this page reads and writes every permission, level and trust value through it, one per-key change per request (C-2); `upgrade_unfinished` removed (founder rule 2026-11-28) |
| `GET/PUT /security/tool-policies` | retired (C-22 row 6) |
| `POST /skills/install/review`, `DELETE /skills/install/review/{id}`, `GET /skills/install/review/{id}` | new (C-13) |
| `POST /tool-approvals/{approval_id}` | `allow` replaced by `allow_for` with `duration` (C-26); two skill actions and the `applied` result (C-24); `expires_at` |
| `POST /skills/{name}/trust-approval` | new (C-24) |
| `PUT /skills/{name}/category` | new, once only (C-21) |
| `POST /skills/{name}/copy` | gains `category` (C-21) |
| `GET/PUT /security/skill-trust` | retired (C-22 row 6) |
| `GET /tools` | C-6 fields |
| `GET /skills` | C-5 `category`, redefined `verified`; C-14 `full_tokens` |
| `GET /agents`, `GET /agents/{id}` | C-14 `full_skills_tokens` (read-only; compared with the existing `context_window_effective`) |
| `POST /skills/install` | C-5 `category`, C-13 `review_id`; trust Deny refuses an unverified install (C-3); trust Ask needs a review (C-12) |
| `GET/PUT /agents/{id}/tools` | C-4 `skills` |
| `POST /mcp-servers` | writes the service's Ask key (C-7) |
| WS `tool_approval_required`, `session_state.pending_approvals` | C-9 `skill` (key, revision, open checks); C-26 `offered_actions`, `allow_durations` |
| WS `message` with `workflow_start` | C-8 `W5` |

Stored data, behaviourally: one global permission and one level per installed skill and per project skill (C-19); one level per tool and per external server; per-agent skill overrides inside the agent's tool settings; each skill's category, install-time verification result and trust approvals (skill, revision) in Omnipus's own data. Nothing records the upgrade: no `sandbox.migrations` marker, no snapshot, ledger or lock file (founder rule 2026-11-28, C-19).

---
## User Stories & Acceptance Criteria

### User Story 1 — Set the global permission of every Omnipus and external tool on one page (Priority: P0)

A person who runs Omnipus wants to decide, in one place, which tools every agent may run, may run after asking, or may never run. Today that grid sits behind "Advanced" in Settings → Security, and the Skills & Tools page only links to it.

**Why this priority**: the Settings base no longer shows this grid (C-22); without this story the global ceiling has no screen at all.

**Independent Test**: open Skills & Tools → Omnipus tools, set `send_email` to Deny, reload, see Deny; an agent's call of `send_email` is refused.

**Acceptance Scenarios**:

1. **Given** the Omnipus tools tab, **When** the person clicks Deny on a tool row, **Then** the row shows Deny, a "Saved" signal appears, and after a reload the row still shows Deny.
2. **Given** a group of 12 tools on screen, all able to take Ask, **When** the person uses that group's Set all → Ask, **Then** all 12 rows show Ask and are saved in one request, with no confirm. Rows that cannot take the chosen value are skipped and named (C-20).
3. **Given** the External tools tab and a service with 5 tools, **When** the person uses the service's Set all → Allow, **Then** all 5 tools show Allow and are saved.
4. **Given** any tool row, **When** the page shows it, **Then** exactly one of Allow, Ask, Deny is selected and "Unset" never appears.
5. **Given** a single click or a Set all that would make `execute_plan` resolve to Allow, **When** the person makes it, **Then** the existing "Confirm autonomous plan execution" dialog appears first, and Cancel changes nothing (ST-Q7 A).
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
2. **Given** a verified skill set to Ask and Auto-approve off, **When** an agent in a chat tries to load it, **Then** the normal approval card appears naming the skill, with Approve Once, Allow for 1 hour / 3 hours / 1 day, and Deny; a time choice lets that agent load the skill without a card in all its chats, its task, plan and scheduled runs and its own helpers until the time ends or Omnipus restarts, and other agents still ask (C-12, C-26).
3. **Given** a skill set to Ask and Auto-approve not active for the run, **When** a task, plan or scheduled run tries to load it, **Then** the load is skipped with "Not run: needs approval, and task and plan runs never ask.", no card appears and the run does not wait (C-12 decision table row 2).
4. **Given** a skill set to Allow and granted to the agent, **When** the agent loads it, **Then** it loads with no card.
5. **Given** a skill not granted to the agent, **When** the agent tries to load it, **Then** it is refused as today, whatever its permission (the grant stays the gate, ADR-072 D4).
6. **Given** the Omnipus tools tab, **When** the `Skill` row is shown, **Then** it has no Allow / Ask / Deny and reads "Permissions are set per skill on the Skills tab." with a link to the Skills tab.
7. **Given** an install upgraded from a version without per-skill permissions, **When** it first starts, **Then** every skill's global permission equals the old global `Skill` value, and each agent's old per-agent `Skill` value of Ask or Deny is copied to each skill granted to that agent at that moment (an old Allow is removed, not copied, because it never changes the outcome), so no agent's behaviour changes; a file whose skills cannot all be listed yet keeps its old value in force until a start can list them; afterwards nothing on disk marks the upgrade (founder rule 2026-11-28, C-19).
8. **Given** a new skill is installed, copied, written with Omnipus or first found in a project folder, **When** it appears, **Then** its global permission is Ask (ST-Q3 B) and its level is "Name and short description"; with Auto-approve off a granted agent's first load in a chat asks, and an unattended run's load is refused with the unattended text (C-12).
9. **Given** a granted skill whose effective value for an agent is Deny (or Ask not yet approved), **When** that agent reads the skill's folder with a file tool or runs its helper script, **Then** the read or run is refused as for an ungranted skill (C-11 E6, E7).
10. **Given** a skill at global Ask, Auto-approve off, that Mia's scheduled task skipped with "Not run: needs approval", **When** the person answers Mia's card for that skill in a chat with "Allow for 1 day", **Then** Mia's scheduled runs within that day load it with no card, another agent's run still skips it, no stored permission changes, and after the day ends (or a restart) Mia's run skips it again; no run ever waits for an answer (C-12, C-26, ST-Q9 A as narrowed by R2-Q1). To make it work for good, the person sets the skill to Allow on Skills & Tools.

### User Story 3 — An agent's Tools tab is the same editor, stricter only, without presets (Priority: P0)

A person opens Mia → Tools and wants the same rows and badges as on Skills & Tools, for tools and for Mia's skills, where Mia can only be stricter than the global value (T-Q7), with no Cautious / Balanced / Full access buttons (T-Q6 A).

**Why this priority**: founder notes on T-Q1, T-Q6 and T-Q7.

**Independent Test**: global `bash` = Ask; Mia's Tools tab shows Allow disabled for `bash`; Mia's skill `plan` set to Deny refuses her load.

**Acceptance Scenarios**:

1. **Given** any agent's Tools tab, **When** it opens, **Then** no role preset control is shown.
2. **Given** a tool or skill whose global value is Ask, **When** the agent's row is shown, **Then** Allow is disabled; its tooltip reads "The global setting is Ask. Set it to Allow on Skills & Tools first."; the row shows the link "Change the global setting", which opens Skills & Tools at that row; Ask and Deny are available. Under a global Deny, Allow and Ask are disabled with the tooltip "The global setting is Deny. Change it on Skills & Tools first." and the same link (R2-Q1 point 4).
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
4. **Given** trust = Ask and an unverified skill whose own permission is Allow and whose current revision has no trust approval, **When** an agent in a chat loads it, **Then** a trust card appears naming the skill, saying it is unverified, with "Approve this version" and Deny and no time choice, also with Auto-approve on (ST-Q2 A).
5. **Given** trust = Allow, **When** chosen, **Then** a warning callout reads "Allow turns off the check for unverified skills. Choose Ask to turn it back on."
6. **Given** an upgrade from a stored Block unverified / Warn unverified / Allow all, **When** Omnipus starts, **Then** trust shows Deny / Ask / Allow respectively; the retired legacy trust endpoint is gone (C-22).
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
8. **Given** a skill set to Full, granted, and Allow for that agent, **When** a new chat of that agent starts, **Then** the skill's whole instructions are in its context from the first turn; if the skill is Ask (not approved) they are not, and it is listed as Name and short description (C-14) (ST-Q1 A).
9. **Given** an install that had "Load tools on demand" off before the upgrade, **When** Omnipus first starts, **Then** every static tool's level is Full, the switch is gone from Settings, and the tool tabs show no note, because the levels are the only control (ST-Q4 B).
10. **Given** a running chat that already loaded `browser_evaluate`, **When** the person sets it to Hidden, **Then** the chat keeps the loaded definition, and new chats do not list it (C-15).
11. **Given** Full skills for an agent that do not fit its model, **When** a turn starts, **Then** nothing is sent and the chat shows the C-14 text with a link to the Skills tab (ST-Q1 A).
12. **Given** skills set to Full, **When** the Skills tab shows, **Then** each Full row shows "about <n> tokens", and a warning callout names every agent whose Full skills take more than half of its model's context (C-14 "Page warning").

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

**Why this priority**: web search leaves Settings → Providers at this landing (C-22 row 5); R30a.

**Independent Test**: Omnipus tools → Web & Search → Settings opens the search-provider panel; changing the default provider works as it did in Settings.

**Acceptance Scenarios**:

1. **Given** the Web & Search group, **When** the person presses its Settings button, **Then** a side panel opens with today's Default search and Fallback cards and their behaviour, unchanged.
2. **Given** the Shell group, **When** shown, **Then** it links "Shell safety rules (Settings)" to `/settings?tab=sandbox` (C-22 row 7).
3. **Given** the Omnipus tools intro, **When** shown, **Then** it reads "Open Settings to change Auto-approve and God Mode." with a link to `/settings?tab=protection` (C-22 row 7).
4. **Given** the page, **When** any tab is shown, **Then** no link sends the person to Settings → Security for permissions.

### User Story 10 — Every tab loads, fails and reads the same way; phone and keyboard (Priority: P2)

**Independent Test**: stop the server, open each tab: error with Retry; at 390 px no sideways scroll, 44 px targets.

**Acceptance Scenarios**:

1. **Given** a tab is loading, **When** shown, **Then** a spinner "Loading skills…" (or tools, services) and skeleton rows show; tab counts show "…"; search, filters and badges are disabled.
2. **Given** a tab fails to load, **When** shown, **Then** an error callout gives the observed cause and Retry; no control on that tab can write until it loads.
3. **Given** a width of 390 px, **When** any tab is shown, **Then** each row becomes a card with "Permission" and "What the model sees" lines, every control is at least 44 × 44 px, and nothing scrolls sideways.
4. **Given** keyboard use only, **When** the person moves through the page, **Then** tabs, badges, menus and dialogs work with the keys in "Accessibility and Keyboard".
5. **Given** the rows of a tab load and the permissions request fails, **When** the tab shows, **Then** the rows show read-only with "Permissions could not be loaded." and Retry, and no badge can write.

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

### User Story 13 — Approval cards allow for a limited time, per agent, instead of "Always Allow" (Priority: P0)

A person answering an approval card wants to let one agent do the same thing again for a while without being asked each time, without that permission quietly lasting for ever or reaching other agents (founder R2-Q1, 2026-11-22). Anything lasting is set as a policy on Skills & Tools or the agent's Tools tab.

**Why this priority**: founder decision R2-Q1 (timed allowances only); it changes every approval card in the product and is security-relevant.

**Independent Test**: `delete_task` at global Ask, Auto-approve off; Mia's card answered with "Allow for 1 hour"; a `delete_task` call with other arguments in Mia's other chat runs with no card; Jim's same call asks; after the hour (test clock) Mia asks again.

**Acceptance Scenarios**:

1. **Given** any tool or skill card that today offers Always Allow, except the five exact-action tools (AS9), **When** it appears, **Then** it offers Approve Once, Allow for 1 hour, Allow for 3 hours, Allow for 1 day, and Deny (and Cancel where it has it today), and no card anywhere offers "Always Allow", "Always allow" or "Allow everywhere" (C-26).
2. **Given** a time choice on a card raised for agent Mia, **When** Mia makes the same call in another chat, in another workspace, in a task, plan or scheduled run, or in a helper she started for herself, **Then** it runs with no card until the time ends; **And** the same call by Jim, or by a helper Mia delegated to Jim, still asks (C-26).
3. **Given** a shell card, a file-access card or a network card, **When** the person picks a time, **Then** the allowance covers exactly what today's Always Allow covered — the exact command or the command family chosen, the one path with its access, or the named addresses — for Mia until the time ends; **And given** an ordinary tool card, the allowance covers the whole tool for Mia, any arguments (founder Q4 B) (C-26).
4. **Given** a running allowance, **When** its time ends or Omnipus restarts, **Then** the next matching call asks again (or, in an unattended run, is skipped with the unattended text without waiting), and nothing about it was ever written to disk.
5. **Given** `set_channel_outbound`, **When** its card appears, **Then** it is unchanged: Approve Once, Deny and Cancel, with "This approval applies to this one request." and no time choice.
6. **Given** a time choice is answered, **When** the card closes, **Then** its result line reads "Allowed for <agent> until <time>." (with the day when it is not today).
7. **Given** a screen or client that still sends the old `allow` action, **When** it answers a card, **Then** the answer is refused with "This choice is no longer offered. Choose Approve Once or a time limit." and the card stays answerable.
8. **Given** a running allowance of Mia for a tool or skill, **When** a person or an agent changes that tool's or skill's permission, globally or for Mia (also back to Ask), **Then** the allowance ends, the next call asks (or is refused under Deny), and the audit log records the ended allowance; a change for Jim alone does not end Mia's (C-26).
9. **Given** a card for `environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config`, **When** it appears, **Then** it offers Approve Once and Deny (and Cancel where it has it today) and no time choice; an `allow_for` answer is refused with 409; the next action of that tool shows a card again (founder Q5 A).

### Edge Cases

- A skill removed while its row is on screen: the next save of that row is refused (unknown skill, 400); the page reloads the list and says "This skill was removed."
- Two browser tabs: different keys both survive (each request names only its own keys, C-2); the same key — the write that commits later wins (commit order under the writer lock, not response order), and each tab shows the stored maps from its latest response and re-reads on focus.
- A Set all clicked twice quickly: one request per click, sent in order, never merged (C-16); the page ends on the stored state of the last response.
- A skill granted to an agent but denied globally: the agent's row shows Deny, Allow and Ask disabled with the tooltip "The global setting is Deny. Change it on Skills & Tools first." and the link "Change the global setting".
- A stored per-agent Allow below a later global Ask: stored as is, floored at run time; the row shows the true effective value (as for tools, `ToolPolicyEditor.tsx::trueEffectivePolicy`).
- An unverified skill with God Mode on: the trust check still applies (C-12) (ST-Q2 A).
- A project-folder skill (mount shelf, ADR-072 D4.1): no grant list; its own key `project:<workspace>/<slug>` (C-19); it follows trust (unverified), its global permission and its level; it is not listed on the Skills tab (#1380 is not a rider by default), so it is changed through `PATCH /security/permissions` or an agent tool, not on screen (Assumptions); its folder is open to file tools and helpers only when its outcome is `allowed` (C-25).
- A skill and a tool with the same name: separate maps; no collision.
- An external service whose id contains underscores: its Ask key uses the id the registry reports (`ToolRegistryEntry.server_id`), never a prefix parsed from tool names.
- `delegate` with `requested_skill` naming an Ask skill: the child's load raises the card in the root chat (#1263 routing); in an unattended chain it is refused.
- The person's own `/skill` command on an ordinary Ask skill: it loads without a card (the person asked by name); Deny and trust Deny still refuse it; an unverified skill under trust Ask still shows the trust card unless that revision was approved (C-11 E4) (ST-Q2 A).
- An agent allowed `edit_skill` can read a denied skill's files for editing (C-11 editor exception); it still cannot load it or run its helpers.
- A skill whose files change while a trust or approval card is open: the answer is checked against the current revision; a yes for the old revision does not load the new one (C-12).
- An upgrade stopped midway (power loss, crash): each file is either still old or already converted; the next start finds the old-shape values that remain and converts those files; no agent ever runs with the replacement `Skill: allow` instead of its old limit (C-19, founder rule 2026-11-28).
- A file that cannot be written (the atomic write fails): an Error naming the file at each start; its stored bytes stay old; every reader — turns, `GET/PATCH /security/permissions`, the agent tab — uses the same converted view, so its old limits still apply and the screen shows them; a save reports that nothing was stored; no screen lists the file (C-19, founder rule 2026-11-28, review NM-04).
- A mount folder, a mount record or the installed-skills folder cannot be read at start: a file whose conversion needs that list is not converted at all; its old values, including an old global `Skill: allow`, stay on disk and keep applying to every skill without its own value, including one whose folder comes back during the run; an Error names the file and the cause; the next start that can list every skill converts the whole file (C-19, review NM-02, R2-02).
- Two writers of the same file at once — a command and the gateway, or two parts of one gateway such as a reload and a REST save, on any platform: every writer takes the file's in-process lock and, where the platform has one, its file lock, inside the one entry, and reads inside them, so the later writer sees the earlier one's result; a system-agent save applies only its named operation; a person's acknowledged change is never overwritten (C-19, review NM-01, R2-01, R2-03). Hand edits are safe only while every Omnipus process is stopped.
- A card answered twice, or after it timed out: the second answer gets the existing 404 and changes nothing (C-24).
- A helper's card shown in the root chat of another agent: a time choice is recorded for the helper's agent (the agent named on the card), never for the chat's agent (C-26).
- Two time choices for the same subject: the later end time wins; a shorter choice never cuts a running allowance short (C-26).
- A Deny set while an allowance runs: the Deny ends the allowance (any permission change does) and the call is refused; lifting it back to Ask brings a card (C-26).
- A permission change for one agent: only that agent's allowances for that tool or skill end; a global change ends them for every agent (C-26).
- An allowance that ends while a shell command runs: the command keeps the access it started with, except that new network connections to an address that is no longer allowed are refused (C-26).
- An install whose answer is lost while the files are still being published: the review keeps checking the install status and never reports "Not installed" from the skills list alone (C-13).

---

## UI Screens and States

| Screen / Component | Loading | Empty | Error | Partial | Success |
|---|---|---|---|---|---|
| Skills tab | spinner "Loading skills…", skeleton rows, trust block skeleton | trust block stays; "No skills installed" + "Browse the catalog" (outline) | callout with cause + Retry; no writes | list loads, permissions fail: rows read-only, "Permissions could not be loaded." + Retry. No upgrade state exists: the former "The upgrade has not finished" callout is removed (founder rule 2026-11-28, C-19) | trust block, grouped rows, "Saved" after each change |
| External tools tab | spinner "Loading services…" | "No external services yet" + "Add service" | callout + Retry | a service not connected: "Not connected" in its header and the error notice inside; others normal | one group per service |
| Omnipus tools tab | spinner "Loading tools…" | "No Omnipus tools reported" + Retry | callout + Retry | tools load, permissions fail: rows and their levels (from `ToolRegistryEntry.visibility`, C-6) shown read-only, no badge enabled, "Permissions could not be loaded." + Retry (the Skills tab's text). Levels cannot fail on their own: they arrive with the rows (ruling spec-gaps-1, 2026-11-25) | one group per category |
| Agent Tools tab | as today | Skills group: "No skills granted. Grant skills on this agent's Skills tab." | as today (three Retry states) | global ceiling missing: read-only, today's text | rows, "Saved" |
| Install review (success toast: "Installed <name>. It is set to Ask: an agent needs your yes to use it unless Auto-approve is on. No agent is granted it yet; grant it on an agent's Skills tab.") | "Checking the skill…" while staging (Cancel usable, discards); "Installing…" after Install (Cancel disabled) | — | server reason; trust Deny refusal; "This review expired. Open the skill again to review it."; "The skill changed after review. Review it again."; outcome unknown → "Could not confirm the install. Checking…", then the result from the install status (C-13), or "Couldn't confirm the install. Open Skills to see what is installed." with Retry | verification unavailable: "Not verified: the catalog did not answer." and trust applies | the toast in the first column |
| Web & Search panel | today's states | today's | today's | today's | today's |
| Page save signal | "Saving…" (queued or in flight, C-16) | — | "Not saved: <server reason>." + Retry; "Not saved: the settings file could not be written." + Retry; rows show stored values; outcome unknown → "Couldn't confirm the save. Checking…" then "Showing what is stored now."; check failed → "Couldn't load what is stored." + Retry | "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." + Retry | "Saved" |
| Full skills too large | — | — | in the chat: the C-14 text; on the Skills tab: a warning callout naming the agents whose `full_skills_tokens` × 2 exceeds their `context_window_effective` (C-14 "Page warning", ST-Q1 A) | `GET /agents` fails: no warning, the tab is otherwise normal | each Full row shows "about <n> tokens" from `Skill.full_tokens` |
| Approval card (C-12, C-24, C-26) | the card itself; buttons Approve Once, "Allow for" with 1 hour / 3 hours / 1 day, Deny (Cancel where it has it today); under the time choices: "Covers <agent> in all its chats, tasks and its own helpers until the time ends or Omnipus restarts. Other agents still ask."; always-ask tools unchanged; the five exact-action tools offer Approve Once and Deny only, with no time choices | — | 409 reasons shown on the card ("The skill changed after this request." and the others in C-24); 400 "This choice is no longer offered. Choose Approve Once or a time limit."; 500 "Could not save the approval. Nothing was changed." with the card still answerable | "Approved for this request only. The time limit was not saved: <reason>." | the card closes; the result line says what was stored, e.g. "Allowed for Mia until 14:05." or "Approved this version of notes-helper." |
| Agent Tools tab, locked badge (US-3 AS2) | — | — | — | — | Allow (and Ask under a global Deny) disabled; tooltip "The global setting is Ask. Set it to Allow on Skills & Tools first." (or the Deny text); link "Change the global setting" next to the badges opens `/skills` at that row |
| After leaving the page | — | — | a late outcome shows one toast with "Open Skills & Tools", worded by class: "A change on Skills & Tools was not saved: <reason>." / "A change on Skills & Tools was saved but is not applied to running agents yet." / "Couldn't confirm a change on Skills & Tools. Open it to see what is stored." (C-16) | — | — |

## User Journey

1. Sidebar → ASSETS → Skills & Tools; the Skills tab opens (US-6 AS1).
2. The trust block is first (US-4 AS1); a change saves at once.
3. Search or filter, open a category, change a skill's permission or level (US-2 AS1, US-5 AS1); "Saved" appears.
4. New skill → Write with Omnipus / Upload / Browse (US-7); the install review asks for a category; the skill appears with Ask and Name and short description (US-2 AS8); the person sets it to Allow when they trust it to run without asking.
5. External tools: Add service; its tools show Ask (US-8 AS1); the service's Set all (US-1 AS3).
6. Omnipus tools: a group's Set all (US-1 AS2); Web & Search → Settings panel (US-9 AS1).
7. An agent → Tools tab: the same rows, stricter only, with a Skills group (US-3).
Exit: through the sidebar; changes already clicked keep saving after the page is left, and a late outcome appears as a toast worded by its class (C-16). A change clicked in the moment the browser closes may not be sent; reopening shows what is stored (T-Q3 B, no leave prompt).

## Accessibility and Keyboard

- Tabs: the catalogued `tabs` component (Radix tabs: `tablist`, `tab`, `tabpanel`); arrow keys, Home, End.
- A row that leaves a filtered list after its save: focus moves to the same control in the next row of the list; if there is none, the previous row; if the group became empty, its header; if the list became empty, the "Clear search and filters" button. The polite status region announces "<name> set to <value>. It no longer matches the filter. <n> shown." A failed save leaves the row and focus where they were. A Set all that empties the group moves focus to the group header. Background refreshes never move focus.
- Allow / Ask / Deny: toggle buttons with `aria-pressed` in a group named "Permission for <name>"; a locked badge is `aria-disabled` and stays focusable; its tooltip reason opens on hover, keyboard focus and tap and is referenced by `aria-describedby`; the "Change the global setting" link is an ordinary link beside the badges, never inside the tooltip.
- Approval card time choices: one group named "Allow <agent> for a limited time" with three buttons named "Allow for 1 hour", "Allow for 3 hours", "Allow for 1 day"; the scope line is referenced by `aria-describedby`; the result line is announced in the polite status region.
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

- security-lead reviews before GREEN: the C-11 entry-point inventory, the skill-folder authority change and the project-skill path gate with its kernel rendering and degradation (`pkg/fspolicy`, `pkg/sandbox`, C-25), the upgrade's one-write-per-file conversion and its read-time translation of a file that could not be rewritten (C-19, founder rule 2026-11-28), the trust-conversion audit gap and its two named no-entry cases (C-3; accepted by the founder, Q-AUD1 A, review NM-05), the one writer rule and the `config.json.lock` sidecar (C-19, review NM-01), the approval action contract (C-24), the timed allowances that replace Always Allow on every card (C-26, the six review points listed there), the C-12 consent rules, the C-18/C-19 migrations, the trust floor and its enforcement at every install and load path (supply chain), the skill policy resolution beside `pkg/tools/compositor.go`, the audit entries (`pkg/audit`), and the `set_config` refusal rows.
- Changed user promises (`docs/skills.md`, `docs/tools.md`, `docs/security.md`, `docs/tasks.md`, `docs/using-omnipus-ui.md`, `docs/group-chats.md`): no more Always Allow; time-limited allowances per agent (C-26); trust words and meanings, per-skill permissions, `Skill` no longer a master switch, presets gone, levels, new services at Ask, the global grid's new home.
- The trust default moves from "warn and continue" to Ask: an unverified skill that loaded silently now asks in a chat and is skipped in unattended runs until a version is approved (T-Q1 A). The docs and the upgrade note say so plainly (DOC-004).
- Hard Constraint #6 stays intact: two stored layers for skills as for tools, no default fallback, no backfill; trust and God Mode are resolution-time rules, like always-ask (C-0).
- No on-screen text names a retired agent, an issue number, a requirement tag or "contact support".

---

## Behavioral Contract

Primary flows:
- When a person changes a permission, a level or the trust choice, the system saves it with no further action and shows "Saved".
- When a person uses a group's Set all, the system changes exactly the rows shown in that group that can take the value, in one request, with no confirm, and names the skipped rows — except the existing autonomous-plan confirm when `execute_plan` would resolve to Allow (ST-Q7 A).
- When an agent loads a granted skill, the system applies the strictest of the skill's global permission, the agent's own value, and — for an unverified skill — the trust choice, and every other path to that skill's instructions or files (menu, search, `/name`, delegation, file tools, helpers, Full) follows the same outcome (C-11).
- When an agent's turn starts, the system lists each allowed tool and each granted, not-denied skill at its global level; a Full skill's instructions are included only when its outcome is `allowed` (C-14).
- When a person opens an agent's Tools tab, the system offers only values at least as strict as the global value; a looser badge is disabled with a tooltip and a link to the global setting.
- When a service is added and no global rule covers it, the system sets all its tools, now and later, to Ask (C-18).
- When a person answers a card with a time choice, the system lets that agent use the whole tool (ordinary card), the approved command, path or addresses (shell cards) or the skill (skill card) without a card, in every session of that agent and its own helpers, until the time ends, the gateway restarts or that permission changes, and stores nothing (C-26).

Error flows:
- When a save is refused, the system shows the server's reason with Retry and the stored values.
- When a save is stored but not applied to running agents, the system says so and offers Retry.
- When an unverified install meets trust Deny, the system refuses it and installs nothing.
- When a skill load meets an effective Deny, the system refuses it, naming the skill and which setting refused it.
- When a load in an unattended run still needs a person's answer (C-12 decision table), the system skips it with the existing unattended text and never waits.
- When a person answers an approval card after the skill changed or was denied, the system stores nothing and refuses the call with the reason (C-24).
- When a client sends the retired `allow` action or a bad duration, the system refuses it with 400 and the card stays answerable (C-26).

Boundary conditions:
- When a locked tool's level is sent with another value, the system refuses it.
- When a group contains Tool search or Skill, every Set all skips them.
- The levels are the only control of what is sent about tools (ST-Q4 B).
- When Full skills do not fit the agent's model, the turn is not sent and the chat says why (C-14).
- When a save's outcome is unknown, the system re-reads and shows what is stored, never a guess (C-16).
- When an install's answer is lost, the system reads the install status bound to the review, never the skills list alone (C-13).
- When a filter is on, Set all counts and changes only matching rows.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not show a Save bar, Discard, a leave prompt or a Set all confirm on Skills & Tools, because T-Q3 B chose autosave with no confirm. The single exception is the existing autonomous-plan warning (FR-010) (ST-Q7 A).
- The system must not show Cautious / Balanced / Full access anywhere, because T-Q6 A removes them.
- The system must not offer a per-agent "What the model sees" control, because T-Q7 rules it out.
- The system must not let an agent tool change a global permission, a level or trust, because global ceilings are operator-only (C-10).
- The system must not add a third stored policy layer, a default policy or a fail-closed backfill (Hard Constraint #6).
- The system must not write a category, permission or level into a skill's folder, because that breaks the hash check (R32).
- The system must not show "Unset" for any tool or skill (R30b).
- The system must not name Ava, Admin or any retired agent in menus, toasts or built-in skill text.
- The system must not change the grant model. ToolSearch finds skills but never loads them (C-17) (ST-Q6 A).
- The system must not inject a skill's instructions, or open its folder to file tools or helpers, before its outcome is `allowed` (C-11).
- The system must not write a per-agent entry during the upgrade that the agent did not already express: no tool entry at all (C-18), and per-skill values only as the translation of the agent's existing, explicit `Skill` value (C-19) (Hard Constraint #6: no backfill).
- The system must not read an old permission value during the upgrade from anywhere but the file that holds it, in the same write that replaces it (C-19, founder rule 2026-11-28).
- The system must not leave on disk anything that exists only because of the upgrade — a lock, snapshot, ledger, progress record, done or version marker, an old-shape key kept "ignored" or a per-agent `Skill` value — and must not decide whether to convert from anything but the presence of an old-shape value (C-19, founder rule 2026-11-28).
- The system must not write `config.json` or an agent record from bytes or a whole document read or prepared outside that file's writer lock, must not let any writer bypass `config.UpdateConfigFile` or the entity store's lock, must not save the system agent's changes by comparing two structs, and must not take the `config.json` lock while holding it already or take `configMu`, the agent-loop lock or an entity lock while holding it (C-19, review NM-01, R2-01, R2-03).
- The system must not convert any part of a file whose conversion needs a skill list that is incomplete, and must not treat "could not be read" as "empty" (C-19, review NM-02, R2-02).
- The system must not report a project-skill folder as kernel-protected on a platform where only the app-level gate applies (C-25).
- The system must not offer "Always Allow", "Always allow" or "Allow everywhere" on any card, and must not let any card action change a stored permission (R2-Q1).
- The system must not offer a time choice for, or let any allowance cover, `environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config` (founder Q5 A).
- The system must not write a timed allowance to disk or keep it across a restart, and must not let it cover another agent (R2-Q1, C-26).
- The system must not change Settings screens (page G), Tasks and Board, or signed-in browsers (#1951), apart from the named edits of C-22; it must not leave `tools.manifest.compressed` unguarded at any commit, and must not keep a caller of a retired endpoint.

### Machine-Verifiable Constraints

**Error Codes / Messages** (all bodies `ErrorResponse`):
- `PATCH /security/permissions`, unknown skill key in `skill_policies` or `skill_visibility` → 400, nothing saved.
- `PATCH /security/permissions`, a locked tool's level changed → 400, "`<tool>` is always loaded: agents need it to work, so its level cannot be changed."
- `PATCH /security/permissions`, `ToolSearch` or `Skill` in `policies` with a value other than `allow` → 400, "`<tool>` has no permission of its own."
- `PATCH /security/permissions`, `set_channel_outbound: allow` → 400 (existing always-ask text).
- `PATCH /security/permissions`, no key named → 400 "Nothing to change."; keys named with their stored values → 200, nothing written, no audit entry; duplicate skill → 400 "`<skill>` is listed twice."; `null` → 400 naming the key; nothing stored → 500 "Not saved: the settings file could not be written.".
- `PATCH /security/permissions`, `skill_trust` other than `allow`/`ask`/`deny` → 400, "Use allow, ask or deny."
- `POST /skills/install`, unverified skill, trust `ask`, no `review_id` → 409 "Review needed: this skill is unverified, and unverified skills are set to Ask. Review it in Skills & Tools first."
- `POST /skills/install`, reviewed revision differs → 409 "The skill changed after review. Review it again."; expired review → 410.
- `POST /skills/install/review`, checksum mismatch → 422 "The downloaded skill does not match the catalog's checksum. Nothing was installed."
- `PUT /skills/{name}/category` when a category is already set → 409 "The category is set at install and cannot be changed."
- `POST /skills/install`, unverified skill with trust `deny` → 403, "This skill is unverified, and unverified skills are set to Deny in Skills & Tools. Nothing was installed."
- `POST /skills/install`, `category` outside `SkillCategory` → 400.
- `PUT /agents/omnipus/tools`, `skills.policies` containing `ask` → 422, the existing Omnipus refusal text and code.
- Any agent writer (C-23), a key the agent cannot use → 400 "`<key>` is not a skill this agent can use."; `PATCH /security/permissions`, an unknown skill key → 400 "`<key>` is not a known skill.".
- `POST /tool-approvals/{id}`, an action not in `offered_actions` → 409 "This choice is not available for this request."; facts changed → 409 with the C-24 reason; trust write failed → 500 "Could not save the approval. Nothing was changed."; answered, timed-out or unknown id → 404.
- `POST /tool-approvals/{id}`, `allow_for` on a card of an exact-action tool → 409 "This choice is not available for this request.", nothing stored (C-24, C-26 point 9).
- `POST /tool-approvals/{id}`, `action: allow` → 400 "This choice is no longer offered. Choose Approve Once or a time limit."; `allow_for` or `approve_version_allow_for` without `duration` or with another value → 400 "Choose 1 hour, 3 hours or 1 day."; `duration` with another action → 400 "A time limit applies only to Allow for." (C-26).
- `POST /skills/{name}/trust-approval`, revision differs → 409 "This skill changed since the page loaded. Review it again."; verified skill → 400 "This skill is verified; it needs no approval.".
- `POST /skills/install` with a `review_id` already `installed` → 200 with the same result, no second publish; while `publishing` → 202.
- `GET/PUT /security/skill-trust`, `GET/PUT /security/tool-policies` → 404 (retired, C-22).
- `set_config` or `PUT /config` changing `tools.manifest.compressed`, `tools.manifest.preview_all_lazy` or `tools.mcp.servers.<id>.deferred` → refused with "This setting was replaced by levels in Skills & Tools.".

**Performance Bounds**: a click MUST show "Saving…" within 100 ms and MUST start its save request within 1,000 ms when no earlier change is pending (C-16); a Set all MUST issue exactly 1 save request; requests from separate clicks are never merged and never overlap.

**Scope Boundaries**: levels MUST change only what is listed to the model, never whether a tool or skill may run. A timed allowance MUST last at most 24 hours and MUST cover one agent only.

**String bounds**: production code under `src/` and `pkg/` MUST contain no card label "Always Allow", "Always allow" or "Allow everywhere" (a repository guard in test 62; comments that describe the removal are allowed).

## Integration Boundaries

### Gateway REST API (SPA ↔ gateway)
- **Data in / out**: the C-2 to C-6 and C-8 payloads.
- **Contract**: the schemas in Contract Changes.
- **On failure**: 4xx → reason + Retry; 503 → saved-not-applied text.
- **Development**: real gateway in handler tests.

### Agent loop (policy resolution, manifest, skill menu)
- **Data in / out**: global and per-agent maps, trust, levels → tool definitions, manifest lines, skill menu, approval frames.
- **Contract**: `ToolApprovalRequiredFrame` and `SessionStatePendingApproval` (C-9, C-26), `ToolApprovalActionRequest` and `ToolApprovalResponse` (C-24, C-26); internal Go otherwise. Timed allowances live in the in-memory grant store, keyed by agent (C-26); nothing about them crosses to disk.
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
- **Order**: Settings lands first; this package lands after it, rebased on the Settings landing (issue #1963; C-22). Readiness: on the rebased branch the Skills & Tools route renders the tool permission grid, web search settings and skill trust, and every C-22 row is done.
- **Compatibility**: C-22 lists the base as read at `8e5848c951`, the gap between the two landings, and the named edits this package makes in Settings files (Limits switch, the moved guard, web search, retired endpoints, search answers). The Settings-spec edits that follow are listed in "Notes for team-lead" for team-lead to make.

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

#### Scenario: S-05 Set all that would allow autonomous plans asks first (ST-Q7 A)

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
- **Then** an approval card appears with tool `Skill`, the skill named "Deep Research", and the buttons Approve Once, Allow for 1 hour, Allow for 3 hours, Allow for 1 day and Deny, and no "Always Allow" or "Allow everywhere"

#### Scenario: S-11 A time choice covers the skill for Mia in all her chats, not for Jim

**Traces to**: User Story 2, Acceptance Scenario 2
**Category**: Alternate Path

- **Given** the card of S-10 answered with "Allow for 1 hour", and Jim also granted `deep-research`
- **When** Mia loads `deep-research` again in the same chat and in a new chat, and Jim loads it in his chat
- **Then** both of Mia's loads run with no card, and Jim gets a card
- **And** with the test clock moved past the hour, Mia's next load shows the card again

#### Scenario: S-12 An Ask skill in an unattended run is refused without a card

**Traces to**: User Story 2, Acceptance Scenario 3
**Category**: Error Path

- **Given** `deep-research` at Ask and Auto-approve off
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
- **And** Mia's record holds no `Skill` tool value and `config.json` holds `Skill: allow` (C-19, founder rule 2026-11-28)

**Examples**:

| old_global | old_agent | new_global | new_agent |
|---|---|---|---|
| allow | none | allow | none |
| allow | deny | allow | deny |
| ask | allow | ask | none (the old Allow is removed, not copied; Mia's effective value is Ask, as before) (founder rule 2026-11-28) |
| deny | none | deny | none |
| allow | ask | allow | ask |
| absent | allow | ask (from the self-heal; never looser than Ask) | none (the old Allow is removed, not copied) (review of 106f63ef9: Allow removal is checked where no global value exists) |

#### Scenario: S-17 A newly installed skill starts at Ask and Name and short description

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Happy Path

- **Given** no skill named `weather`
- **When** the person installs `weather` from the catalog with category Lifestyle
- **Then** its row shows Ask and "Name and short description"
- **And** the toast says it is set to Ask and needs a yes unless Auto-approve is on (R2-Q2 A)

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
- **When** the person looks at Omnipus's `write-skill` row
- **Then** its Ask badge is disabled and its visible reason, linked with `aria-describedby`, reads "Omnipus takes no per-agent Ask; set Ask globally or use Deny."
- **And** a direct `PUT /agents/omnipus/tools` with `skills.policies: {write-skill: ask}` is refused with 422 and that text at the start of the message, and the stored value is unchanged

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
- **Then** a trust card appears naming "notes-helper", marked unverified, with "Approve this version" and Deny and no time choice (ST-Q2 A)

#### Scenario: S-31 Trust Ask applies in God Mode (ST-Q2 A)

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case

- **Given** God Mode on and the setup of S-30
- **When** Mia loads `notes-helper` in a chat
- **Then** a trust card appears (C-12) (ST-Q2 A)

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
- **Then** the trust block shows `<new>` and `config.json` holds no old trust word afterwards (founder rule 2026-11-28)
- **And** when the gateway's own start converted the word, exactly one audit entry (resource `sandbox.skill_trust`, old word, new word, actor `system`) is written by `auditOldTrustConversion` in that start, and a second start writes none
- **And** when another command converted the word before the first gateway start, or the gateway stopped after the conversion write and before the entry, no entry is written, at that start or any later one (the two named cases of C-3, review NM-05)
- **And** for an empty value nothing is rewritten and no entry is written (it is not an old-shape value, C-3)

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
- **And** `PUT /security/skill-trust` no longer exists: 404 (retired, C-22)

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
- **And** the requests captured from Mia's new chat — the goal-narrowed first request when Mia has a goal, and the next normal request — list `<tool>` exactly as the requests captured before the upgrade did (literal fixtures, not built by the new code), except ToolSearch in row 16

**Examples**: every row of Dataset D7.

#### Scenario: S-42 A Full skill is in context from the first turn

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Alternate Path

- **Given** `plan` at Full, granted to Mia and Allow for Mia, with the sentinel sentence "ZEBRA-PLAN-7" in its instructions
- **When** a new chat with Mia starts
- **Then** the captured first request holds the whole `plan` instructions, including "ZEBRA-PLAN-7", without a `Skill` call (ST-Q1 A)

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

#### Scenario: S-44 Levels are the only control after the upgrade (ST-Q4 B)

**Traces to**: User Story 5, Acceptance Scenario 9
**Category**: Alternate Path

- **Given** an install that had "Load tools on demand" off and `browser_evaluate` allowed for Mia
- **When** Omnipus starts on this release
- **Then** `browser_evaluate` shows Full and Mia's first-turn request carries its definition, as before
- **And** the tool tabs show no "Load tools on demand" note
- **And** `set_config` with `tools.manifest.compressed: true` is refused with "This setting was replaced by levels in Skills & Tools."
- **And** Settings → Limits shows no "Load tools on demand" switch, and saving Max parallel agents there still succeeds
- **And** Mia's normal request now offers ToolSearch, the one named difference for an install that had the switch off (C-6)

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
- **And** the first link opens Settings → Protection and the second Settings → Sandbox & network (`/settings?tab=protection`, `/settings?tab=sandbox`)
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

- **Given** before the upgrade the global map has `<global>`, Mia has `<agent>` for tool `<tool>` of service `<service>`, God Mode is `<god>`, `<service>` is assigned to Mia, and the start is `<phase>` (first start, repeat start, or a service added after the upgrade)
- **When** Omnipus starts on this release
- **Then** the global map gains exactly `<written>` and Mia's effective value for `<tool>` is `<after>`
- **And** Mia's own entries are unchanged
- **And** the test asserts that every row of Dataset D8 ran (11 rows), so no dimension is fixed by the test itself

**Examples**: every row of Dataset D8.

#### Scenario: S-89 A Full skill that needs approval is not injected (ST-Q1 A)

**Traces to**: User Story 5, Acceptance Scenario 8
**Category**: Error Path

- **Given** `notes-helper` at Full, granted to Mia, unverified, trust Ask, no approval, sentinel "ZEBRA-NOTES-3" in its instructions
- **When** a new chat with Mia starts and Mia's first request is captured
- **Then** the request does not contain "ZEBRA-NOTES-3" and lists `notes-helper` with its description
- **And** a scheduled run of Mia's captured request does not contain it either

#### Scenario: S-90 Full skills that do not fit stop the turn with a reason (ST-Q1 A)

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

#### Scenario: S-93 A trust approval covers one version only (ST-Q2 A)

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

#### Scenario: S-96 ToolSearch finds a Hidden skill and labels it (ST-Q6 A)

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
- **Then** `GET /skills` lists `trip-notes` with category `lifestyle`, no agent granted, and `GET /security/permissions` shows `trip-notes: ask`
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

#### Scenario: S-101 The legacy trust and tool-grid endpoints are retired on the Settings base

**Traces to**: User Story 4, Acceptance Scenario 6
**Category**: Edge Case

- **Given** this package landed on top of Settings, and a data folder whose stored trust is `block_unverified`
- **When** a client calls `GET /security/skill-trust`, `PUT /security/skill-trust`, `GET /security/tool-policies` and `PUT /security/tool-policies`
- **Then** each answers 404 and nothing is stored
- **And** Skills & Tools shows trust Deny (the stored value was rewritten once on load, C-3)
- **And** the guard test finds no caller of either path in `src/`, `pkg/`, `cmd/` or `tests/`

#### Scenario: S-102 A project skill and an installed skill with one name keep separate values

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Edge Case

- **Given** installed `notes` at Allow, project skill `notes` in workspace W1 at Deny (`project:w1/notes`), Mia mounted on W1 **and granted** installed `notes`, and Ray mounted on W1 and **not granted** installed `notes`
- **When** Mia loads `notes`, then Ray loads `notes`
- **Then** Mia gets the installed skill's instructions (a granted installed skill wins over a same-slug project skill)
- **And** Ray's load is refused naming the Deny of the project skill `notes` in W1
- **And** afterwards installed `notes` is still Allow and `project:w1/notes` is still Deny
- **And** the expected winners are literal in the test; the test never calls `ResolveSkillName` to build them

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

### Feature: Founder answers ST-Q1 to ST-Q10

#### Scenario: S-105 A project skill asks once per version (ST-Q8 A)

**Traces to**: User Story 4, Acceptance Scenario 4
**Category**: Edge Case

- **Given** trust Ask, workspace W1 with project skill `notes` at Allow for Mia, never approved
- **When** Mia loads `notes` in a chat, the person approves it, Mia loads it again, then a file of `notes` changes and Mia loads it a third time
- **Then** the first load shows a trust card, the second loads with no card, and the third shows a trust card again

#### Scenario: S-106 Two open checks give one combined card

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Edge Case

- **Given** trust Ask, unverified skill `notes-helper` at Ask (new skill), granted to Mia, its current version not approved, Auto-approve off
- **When** Mia loads `notes-helper` in a chat
- **Then** exactly one card appears, naming the skill as unverified, with "Approve this version", "Approve this version and allow for" 1 hour / 3 hours / 1 day, and Deny
- **And** after "Approve this version" the skill loads and no second card appears
- **And** with Auto-approve on, the card is a trust card offering only "Approve this version" and Deny (the permission part is settled by Auto-approve, so there is no time choice)

#### Scenario: S-107 A new skill installed through the review asks at first use when Auto-approve is off, and unattended runs skip it

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Happy Path

- **Given** trust Ask and unverified `weather` installed through the install review, granted to Mia, at Ask (new skill), Auto-approve off
- **When** Mia loads `weather` in a chat
- **Then** one ordinary approval card appears (Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny) and no trust card
- **And** when a scheduled task of Mia loads `weather`, the load is refused with "Not run: needs approval, and task and plan runs never ask." and no card appears
- **And** with Auto-approve on instead, the first chat load runs with no card (R2-Q2 A), because the review already answered trust (C-12 decision table row 1)

#### Scenario: S-108 A built-in skill added by an Omnipus update starts at Allow (ST-Q10 A)

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Edge Case

- **Given** an install whose stored skill permissions lack built-in skill `capability-plan` (added by the update)
- **When** Omnipus starts
- **Then** `capability-plan` is stored at Allow, its shipped default
- **But** a skill the person installs afterwards is stored at Ask

#### Scenario: S-109 A time choice in a chat lets the same agent's scheduled runs load the skill until it ends (ST-Q9 A, R2-Q1)

**Traces to**: User Story 2, Acceptance Scenario 10
**Category**: Happy Path

- **Given** trust Allow, verified skill `weather` at global Ask, granted to Mia (no own value) and to Ray (no own value), Auto-approve off, and scheduled tasks of Mia and Ray that load `weather`
- **When** the tasks run, then the person, in a chat with Mia, answers the `weather` card with "Allow for 1 day", then both tasks run again, then the test clock passes the day and Mia's task runs once more
- **Then** the first runs skip the skill with "Not run: needs approval, and task and plan runs never ask." and do not wait
- **And** after the answer Mia's task loads `weather` with no card, Ray's task still skips it, and the global permission of `weather` is still Ask with no `sandbox.skill_policies` audit entry
- **And** after the day Mia's task skips it again
- **But** if Mia's own value for `weather` is Ask, the card offers the same time choices and the allowance works the same way (C-26 point 5)

### Feature: Round-2 corrections (upgrade recovery, project paths, approval contract, install outcome, keys, saving)

#### Scenario Outline: S-110 The upgrade survives a stop at every step and keeps Deny and Ask (rewritten, founder rule 2026-11-28; row-specific expectations after review NM-06)

**Traces to**: User Story 2, Acceptance Scenario 7
**Category**: Edge Case

- **Given** a pre-release data folder (written by the release before this one) with global `Skill: deny`, installed skills `plan` and `deep-research`, three agents — Mia with her own `Skill: ask`, granted `plan` and mounted on W1 with project skill `notes`; Jim and Ray with the seeded `Skill: allow`, Jim granted `deep-research` — and the on-disk state `<state>`
- **When** Omnipus starts on this release, `<repair>` is done, and Omnipus starts again
- **Then** after the first start the stored files are as `<stored>`, every turn, `GET /security/permissions` and the agent tab show and apply `<effective>`, and the logs hold `<diagnostic>`
- **And** after the second start the result is `<after_repair>`
- **And** at no point does a turn of Mia, Jim or Ray load a skill with less than its old limit, or before step A and the config load have run (C-19)
- **And** once every file of the row is converted, the clean end state holds: `config.json` holds none of `tools.manifest.compressed`, `tools.manifest.preview_all_lazy`, a server's `deferred`, an old trust word, `sandbox.migrations` or a `Skill` value other than `allow`; no agent record holds a `Skill` tool value; no file named after the upgrade exists anywhere under the data folder; and a further start makes no conversion or repair write (logs are not counted)

"The S-110 end state" below means: `plan`, `deep-research` and `project:w1/notes` globally Deny; Mia's overrides for `plan` and `project:w1/notes` Ask; Jim and Ray with no skill override; `Skill` fixed Allow; the clean end state above.

**Examples**: Dataset D13 (all rows; the columns `<state>`, `<stored>`, `<effective>`, `<diagnostic>`, `<repair>` and `<after_repair>` are the dataset's columns).

#### Scenario Outline: S-111 Project-skill folders follow the skill's outcome on every path

**Traces to**: User Story 2, Acceptance Scenario 9
**Category**: Error Path

- **Given** Mia mounted on W1, which holds `<folder>` with `SKILL.md` and `helper.sh`, and an ordinary file `W1/README.md`; Mia's `edit_skill` is Deny; the skill's outcome for Mia is `<outcome>`
- **When** Mia `<action>`
- **Then** the result is `<result>`
- **And** reading `W1/README.md` works in every row (positive control)

**Examples**: Dataset D14 (all rows).

#### Scenario: S-112 A card answered after the facts changed stores nothing

**Traces to**: User Story 2, Acceptance Scenario 10
**Category**: Error Path

- **Given** a combined card for unverified `notes-helper` (revision R1) in Mia's chat, offering "Approve this version and allow for" 1 hour / 3 hours / 1 day
- **When** a file of `notes-helper` changes (revision R2) and the person then chooses "Allow for 3 hours"
- **Then** the answer is 409 "The skill changed after this request.", no trust approval and no allowance is stored, and Mia's waiting call is refused with that text
- **And** sending the same answer again gets 404 and changes nothing
- **And** an action the card did not offer (`approve_version_allow_for` on an install card) gets 409 "This choice is not available for this request."

#### Scenario: S-113 A restart ends the time allowance but keeps the version approval

**Traces to**: User Story 2, Acceptance Scenario 10
**Category**: Happy Path

- **Given** trust Ask, unverified `notes-helper` at global Ask with revision R1 not approved, granted to Mia (no own value) and to Ray (own value Deny), Auto-approve off
- **When** Mia's chat card is answered with "Approve this version and allow for 1 day", a scheduled task of Mia loads `notes-helper`, Omnipus restarts, and scheduled tasks of Mia and Ray load `notes-helper`
- **Then** the response's `applied` shows `trust_approved: true`, `timed_allowance: true`, `call_released: true` and `expires_at` one day after the answer; one `skill.trust_approved` (R1) audit entry and one allowance audit entry (Mia, `notes-helper`, 1d) exist
- **And** before the restart Mia's task loads it with no card
- **And** after the restart Mia's task skips it with the unattended text (the allowance ended; the R1 trust approval is still stored), and Ray's task is refused naming his own Deny
- **And** in a chat after the restart Mia gets a permission card only, not a trust card, until a file of `notes-helper` changes (R2), after which she gets a combined card again

#### Scenario: S-114 A half-saved approval is reported as half-saved

**Traces to**: User Story 2, Acceptance Scenario 10
**Category**: Error Path

- **Given** a combined card for `notes-helper` R1, and a test seam that makes recording the timed allowance fail
- **When** the person chooses "Approve this version and allow for 3 hours"
- **Then** the trust approval of R1 is stored, no allowance exists, the call is released, and the card reads "Approved for this request only. The time limit was not saved: <reason>."
- **And** with the seam failing the trust-approval write instead, the answer is 500 "Could not save the approval. Nothing was changed.", nothing is stored and the card can still be answered

#### Scenario Outline: S-115 The install review reports what really happened to a lost install

**Traces to**: User Story 7, Acceptance Scenario 10
**Category**: Edge Case

- **Given** a review of `weather` with reviewed revision R2, `<situation>`
- **When** the Install response is lost and the review checks the install status
- **Then** the review shows `<shown>`
- **And** it never decides from the skills list alone

**Examples**: Dataset D15 (all rows).

#### Scenario Outline: S-116 Every writer accepts and refuses the same skill keys

**Traces to**: User Story 11, Acceptance Scenario 1
**Category**: Edge Case

- **Given** Mia mounted on W1 (project skill `notes`) and not on W2 (project skill `todo`), and installed skill `plan` not granted to Mia
- **When** `<writer>` is called for Mia with `<input>`
- **Then** the answer is `<result>` and a read-back through `get_agent` and `GET /agents/{id}/tools` shows `<stored>`

**Examples**: Dataset D16 (all rows).

#### Scenario: S-117 A slow save holds the next request but not its Saving mark

**Traces to**: User Story 1, Acceptance Scenario 10
**Category**: Edge Case

- **Given** the save queue with a controlled transport, where the first response takes 5 seconds
- **When** the person clicks Deny on `send_email`, then 100 ms later Ask on `plan`
- **Then** `plan` shows "Saving…" within 100 ms of its click, and its request starts only after `send_email`'s response arrives
- **And** if `send_email` is answered 503, the person leaves the page, and the next response arrives, the toast reads "A change on Skills & Tools was saved but is not applied to running agents yet."
- **And** "Retry applying" sends `send_email: deny` again and gets 200 with no new audit entry

#### Scenario: S-118 An agent installing an unverified skill under Ask gets one card

**Traces to**: User Story 2, Acceptance Scenario 8
**Category**: Happy Path

- **Given** trust Ask, `install_skill` at Ask for Omnipus, and an unverified catalog skill `weather`
- **When** Omnipus calls `install_skill` for `weather` in a chat
- **Then** exactly one card appears: the `install_skill` card naming `weather` as unverified with its staged revision, offering "Install and approve this version" and Deny, with no time choice
- **And** approving it installs exactly that revision and stores its trust approval; no second card appears
- **And** denying it installs nothing and removes the staged copy

### Feature: Timed allowances on approval cards (User Story 13)

#### Scenario: S-119 A tool card offers time choices and no Always Allow

**Traces to**: User Story 13, Acceptance Scenario 1
**Category**: Happy Path

- **Given** `delete_task` at global Ask, Auto-approve off
- **When** Mia calls `delete_task` in a chat
- **Then** the card offers Approve Once, Allow for 1 hour, Allow for 3 hours, Allow for 1 day, Deny and Cancel, and the frame carries `offered_actions` `[allow_once, allow_for, deny, cancel]` and `allow_durations` `[1h, 3h, 1d]`
- **And** no text "Always Allow", "Always allow" or "Allow everywhere" appears on the card

#### Scenario: S-120 A time allowance belongs to the agent: other chats and own helpers yes, other agents no

**Traces to**: User Story 13, Acceptance Scenario 2
**Category**: Happy Path

- **Given** `delete_task` at global Ask, Auto-approve off, and Mia's card for `delete_task` with arguments A answered with "Allow for 3 hours"
- **When** Mia calls `delete_task` with arguments A in a second chat in another workspace, in a helper she starts for herself, and in a scheduled task; and Jim, and a helper Mia delegates to Jim, call it with arguments A
- **Then** Mia's three calls run with no card
- **And** Jim's call and the delegated helper's call each raise a card (the helper's in Mia's root chat)
- **And** Mia's call with different arguments B also runs with no card (an ordinary card covers the whole tool, founder Q4 B)

#### Scenario Outline: S-121 Narrow cards keep their narrow subject, now per agent and timed

**Traces to**: User Story 13, Acceptance Scenario 3
**Category**: Edge Case

- **Given** the row's setup (Auto-approve on, an enforcing sandbox for the shell rows) and a running allowance for Mia from `<card>` answered with "Allow for 1 hour"
- **When** Mia runs `<next command>` in another chat
- **Then** the result is `<result>`

**Examples**: Dataset D17 rows 1–9, 12 and 13.

#### Scenario: S-122 A restart ends every allowance and nothing is written

**Traces to**: User Story 13, Acceptance Scenario 4
**Category**: Edge Case

- **Given** running allowances for Mia (a tool call, a shell family, a path, an address) and checksums of `config.json`, every agent file and the `system` folder
- **When** Omnipus restarts
- **Then** each of those calls asks again in a chat
- **And** the checksums are unchanged before and after the answers and the restart (no storage object, R2-Q1 point 3)

#### Scenario: S-123 Unattended runs honour a running allowance and never wait after it ends

**Traces to**: User Story 13, Acceptance Scenario 4
**Category**: Alternate Path

- **Given** `delete_task` at global Ask, Auto-approve off, a scheduled task of Mia that calls `delete_task` with arguments A, and Mia's card for that call answered with "Allow for 1 hour"
- **When** the task runs within the hour, and again after the test clock passes the hour
- **Then** the first run deletes the task with no card
- **And** the second run is refused with "Not run: needs approval, and task and plan runs never ask.", no card appears and the run does not wait

#### Scenario: S-124 Retired and malformed card answers are refused

**Traces to**: User Story 13, Acceptance Scenario 7
**Category**: Error Path

- **Given** a pending `delete_task` card for Mia
- **When** a client answers `{action: allow}`, then `{action: allow_for}`, then `{action: allow_for, duration: 2h}`, then `{action: allow_once, duration: 1h}`
- **Then** the answers get 400 "This choice is no longer offered. Choose Approve Once or a time limit.", 400 "Choose 1 hour, 3 hours or 1 day.", 400 "Choose 1 hour, 3 hours or 1 day." and 400 "A time limit applies only to Allow for."
- **And** nothing is stored and the card can still be answered with `{action: allow_for, duration: 1h}`, which gets 200 with `expires_at` one hour later

#### Scenario: S-125 Always-ask tools keep their one-request card

**Traces to**: User Story 13, Acceptance Scenario 5
**Category**: Edge Case

- **Given** `set_channel_outbound` at Ask
- **When** Omnipus calls it in a chat, and a client then answers that card with `{action: allow_for, duration: 1h}`
- **Then** the card offers Approve Once, Deny and Cancel with "This approval applies to this one request." and `allow_durations` is empty
- **And** the `allow_for` answer is refused with 400 and nothing is remembered

#### Scenario: S-126 The card says what it stored

**Traces to**: User Story 13, Acceptance Scenario 6
**Category**: Happy Path

- **Given** a `delete_task` card for Mia at 13:05 local time
- **When** the person chooses "Allow for 1 hour"
- **Then** the card closes with "Allowed for Mia until 14:05."
- **And** choosing "Allow for 1 day" on another card at 13:05 shows "Allowed for Mia until tomorrow 13:05."

#### Scenario Outline: S-128 A permission change ends the running allowances it affects

**Traces to**: User Story 13, Acceptance Scenario 8
**Category**: Edge Case

- **Given** running allowances of Mia and Jim from `<card>` answered with "Allow for 3 hours"
- **When** `<change>` is saved by `<writer>`
- **Then** Mia's next matching call `<Mia result>` and Jim's `<Jim result>`
- **And** one `approval.allowance_ended` audit entry is written per ended allowance, naming the changed key and the actor

**Examples**: Dataset D17 rows 10, 11 and 14–20.

#### Scenario Outline: S-129 Exact-action tools never get a time choice and every action asks

**Traces to**: User Story 13, Acceptance Scenario 9
**Category**: Error Path

- **Given** `<tool>` at global Ask, Auto-approve on, and Mia
- **When** Mia calls `<tool>` with arguments A in a chat, a client answers the card with `{action: allow_for, duration: 1d}`, then the person answers it with Approve Once, and Mia calls `<tool>` with arguments A again
- **Then** the card offers Approve Once and Deny only (the frame's `allow_durations` is empty and `offered_actions` holds no `allow_for`)
- **And** the `allow_for` answer gets 409 "This choice is not available for this request." and nothing is stored
- **And** the second call shows a new card with its exact action

**Examples**: Dataset D17 rows 21–25.

#### Scenario: S-127 An agent cannot be set to Allow under a global Ask; the control says why and links to the global setting

**Traces to**: User Story 3, Acceptance Scenario 2
**Category**: Error Path

- **Given** global `bash` at Ask and global `send_email` at Deny
- **When** the person opens Mia → Tools and focuses the `bash` Allow badge, then the `send_email` Ask badge
- **Then** the `bash` Allow badge is disabled with the tooltip "The global setting is Ask. Set it to Allow on Skills & Tools first."; the `send_email` Allow and Ask badges are disabled with "The global setting is Deny. Change it on Skills & Tools first."
- **And** each row's "Change the global setting" link opens Skills & Tools with that row in view and focused

#### Scenario Outline: S-130 Full rows show their size and the page warns before a chat would stop (ruling spec-gaps-1, 2026-11-25)

**Traces to**: User Story 5, Acceptance Scenario 12
**Category**: Edge Case

- **Given** `<setup>` (fixed token estimator in the Go test; literal `GET /skills` and `GET /agents` answers in the screen test)
- **When** the server answers `GET /skills` and `GET /agents`, and the Skills tab shows
- **Then** `<expected>`

**Examples**: Dataset D18 (all rows).

#### Scenario: S-131 Retired (founder rule 2026-11-28)

The Skills-tab notice "The upgrade has not finished for <n> agents" and `PermissionSettings.upgrade_unfinished` are removed: the upgrade keeps no record of its progress (C-19). The ID is not reused. Its replacement checks are S-110 (a file that cannot be written or converted keeps its old limits; Dataset D13 rows 9, 10 and 14–16) and S-133 (no upgrade state on disk). `GET /security/permissions` never returns `upgrade_unfinished`, and a `PATCH` body naming it gets the existing 400 for an unknown member (Dataset D6).

#### Scenario: S-132 Omnipus tools load but permissions fail (ruling spec-gaps-1, 2026-11-25)

**Traces to**: User Story 10, Acceptance Scenario 5
**Category**: Error Path

- **Given** `GET /tools` answers and `GET /security/permissions` fails with "Omnipus did not respond"
- **When** the Omnipus tools tab shows
- **Then** the rows show with their levels from `GET /tools`, read-only, and no badge or level control can write
- **And** the tab shows "Permissions could not be loaded." with Retry; after a successful Retry the badges work

#### Scenario: S-133 An upgraded data folder looks like a fresh install that carries the old settings (founder rule 2026-11-28)

**Traces to**: User Story 2, Acceptance Scenario 7; User Story 5, Acceptance Scenario 7
**Category**: Edge Case

- **Given** two saved pre-release data folders (the loading switch on and off; old trust `warn_unverified`; global `Skill: deny`; Mia `Skill: ask`, Omnipus and Jim `Skill: allow` as seeded; one MCP server with `deferred: true` and no global key), and a fresh install of this release with the same skills, workspaces, agents and server added through the screens
- **When** each pre-release folder starts on this release, and the person then sets on the fresh install, through the screens and in this order (review NM-06; C-20 offers an agent only values at least as strict as the global value): first Mia's skill Ask overrides while every skill's global value is still Allow, then each skill's global value Deny, then the server's level and Ask key, then trust Ask
- **Then** every file under the upgraded folder exists in the fresh install too, apart from user content (agents, skills, workspaces, sessions, logs) and the generic artifacts every install can have (an interrupted atomic write's `.tmp-*`, the writer-lock files `config.json.lock` and `entities/agents/<id>.lock`), and every key path of the upgraded `config.json` and agent records also exists in the fresh install's files; values may differ only where the old install carried them over
- **And** the upgraded folder holds none of: `entities/.skill-permissions-upgrade*`, `sandbox.migrations`, `tools.manifest.compressed`, `tools.manifest.preview_all_lazy`, a server's `deferred`, an old trust word, a per-agent `Skill` tool value
- **And** `GET /security/permissions` on the upgraded install has no `upgrade_unfinished` member

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
| ST-04 | US-2 AS1–4 | `deep-research` Ask; Mia loads it in chat; answer "Allow for 1 hour"; load again in the same chat and in a new chat; Jim loads it | card naming the skill with the time choices and no Always Allow; Mia's later loads without card; Jim gets a card | skill load resolution | restart, then Mia loads again (card again) | stable |
| ST-05 | US-2 AS3 | `deep-research` Ask, Auto-approve off; a scheduled task of Mia loads it; then Auto-approve on and run again | off: skipped with the unattended text, no card, no wait; on: loads (C-12 row 1) | unattended path | check task conversation text | needs scheduler time control |
| ST-06 | US-2 AS7, US-5 AS7 | Capture Mia's (with a goal) and Jim's requests (scripted model, request log) on two saved pre-release data folders, one with the loading switch on and one off: the goal-narrowed first request and the next normal request; upgrade; capture again | requests identical tool by tool and skill by skill, except the SC-003 named changes, including ToolSearch now offered for the switch-off folder; a mutation that resets one migrated level makes the comparison fail | migration | diff the captured requests, not the config | stable with fixture |
| ST-07 | US-3 AS1–3 | Open Mia → Tools; hover, focus and tap a locked Allow; follow "Change the global setting" | no presets; Skills group with granted skills; Allow disabled where global Ask, with the tooltip; the link opens that row on Skills & Tools | agent editor | open Jim and a custom agent too | stable |
| ST-08 | US-3 AS5 | Omnipus → Tools → a skill row; then a direct `PUT /agents/omnipus/tools` with that skill at Ask | the Ask badge is disabled with the Omnipus text as its reason; the direct call is refused with 422 and the same text | Omnipus guard | Deny is accepted | stable |
| ST-09 | US-4 AS2–3 | Trust Deny; (a) upload an unverified zip on screen; (b) ask Omnipus to install an unverified catalog skill; (c) `POST /skills/install` directly with a review id; then trust Ask and (d) direct `POST /skills/install` without a review | (a)–(c) refused, nothing installed; (d) 409 | trust at install | list the skills folder after each | stable |
| ST-10 | US-4 AS4 | Trust Ask, Auto-approve on; Mia loads an unverified granted skill | card naming the skill as unverified | trust at load | repeat with God Mode on | stable |
| ST-11 | US-4 AS6 | Start with stored `warn_unverified` | trust shows Ask; audit has one entry | trust migration | read audit log | stable with fixture |
| ST-12 | US-5 AS2–4, AS10 | Set `serve_web` to each level; capture the next request Mia sends; load it, then set Hidden and capture again; demote `read_file` to Hidden | matches S-38, S-91, S-92 | manifest | ToolSearch finds Hidden and demoted tools | needs request capture |
| ST-13 | US-5 AS8, US-5 AS11 | A test skill with sentinel "ZEBRA-PLAN-7": Full + Allow → capture first request; Full + Ask → capture (sentinel absent until approval); several large Full skills → too-large text | sentinel present only when allowed; too-large text shown, nothing sent | Full / consent / budget | the captured request, never the model's answer | stable with scripted model |
| ST-14 | US-6 AS1–5 | Open page; search "browser"; Escape; "zzzz" | as S-45 to S-49 | page | same on phone width | stable |
| ST-15 | US-7 AS2–3, AS9 | Write with Omnipus online: complete the card with category Research, approve; offline: press again | online: a new skill is listed under Research at Ask with no agent granted; offline warning, nothing sent | workflow start to published skill | reload Skills tab and check group and category | stable |
| ST-19 | US-2 AS9 | Mia granted `notes-helper` (Deny for Mia), `edit_skill` denied; ask Mia to read its SKILL.md with a file tool and to run its helper; repeat for a project skill in W1 (`.claude/skills/notes`) with `project:w1/notes` Deny; read `W1/README.md` each time | skill reads and helper runs refused, README read works; then set Allow: both work | C-11 E6/E7, C-25 | repeat with Ask (refused until approved) | stable |
| ST-20 | US-1 AS9 | Two browser tabs: A denies `send_email`, B sets `plan` Ask; reload both | both stored | per-key save | read `GET /security/permissions` | stable |
| ST-21 | US-4 AS4 | Trust Ask, approve `notes-helper` v1; change one file; load again | second load asks again | trust approvals | God Mode on, repeat | stable |
| ST-22 | US-8 AS1, C-18 | Upgrade fixtures for each C-18 row | each row's "Agent after" holds; no agent entry written | MCP migration | diff agent configs before/after | stable with fixture |
| ST-23 | FR-040 | Change one tool, one skill, a Set all of 5, a no-op, a refused request | audit has exactly 1 + 1 + 5 + 0 + 0 entries with old/new | audit | read the audit log | stable |
| ST-25 | US-2 AS8, C-12 | Omnipus installs an unverified skill with `install_skill` at Ask (approve its one card), grant to Mia, load in a chat with Auto-approve off; then a project skill: create it, load, change a file, load again | install: one card (C-12 row 6); first load: one permission card; project skill: one combined card per version | card count, ST-Q3 B, ST-Q8 | count cards per step | stable |
| ST-24 | US-4 AS6, C-22 | On the cut (Settings base plus this package): open Settings → Protection and follow the moved notice; open Limits; open Providers; call the four retired endpoints | the notice opens Skills & Tools with the grid and trust; Limits has no loading switch and saves Max parallel agents; Providers has no web search; the endpoints answer 404 | landing on top of Settings | reload each screen | stable |
| ST-16 | US-7 AS4–7, US-2 AS8 | Install from catalog with and without category; upload a file with `metadata.category`; grant the new skill to Mia and load it in a chat and in a scheduled task | category required; preselected; grouped; the skill shows Ask; the chat load asks once (one card even when unverified, C-12); the task load is refused with the unattended text | install review, new-skill default | reload and check group and permission | stable |
| ST-17 | US-8 AS1 | Add a service with tools | all tools Ask | MCP add | restart and check still Ask | needs a test service |
| ST-18 | US-10 AS3, US-9 AS1 | 390 × 844: every tab, Web & Search panel | 44 px targets, no sideways scroll; panel works | phone, panel | measure targets | stable |
| ST-26 | US-2 AS10, C-24, C-26 | Unverified `notes-helper` at global Ask, granted to Mia and to Ray (own Deny); Auto-approve off; answer Mia's combined card with "Approve this version and allow for 1 day"; run scheduled tasks of Mia and Ray; restart Omnipus; run them again; open a chat with Mia and load it; answer a stale card | before the restart Mia's task loads and Ray's is refused; after it Mia's task skips (allowance ended) while R1 stays approved (the chat shows a permission card only); the stale card stores nothing (409); the global value is still Ask and no policy audit entry exists | approval actions end to end | read audit log and `GET /security/permissions` | stable |
| ST-27 | US-2 AS7, C-19 | On copies of a saved pre-release data folder (never one that ran a build of this package before 2026-11-28), set up each Dataset D13 state (the test build's `fileutil.SetWriteFaultForTest` seam, or the file states placed by hand with Omnipus stopped), start, apply the row's repair, start again; list every file under the data folder and every key of `config.json` and the agent records; compare with a fresh install of the same build (S-133) | each row matches its own `<stored>`, `<effective>`, `<diagnostic>` and `<after_repair>` columns; no Deny or Ask is lost; the clean end state (SC-010) holds where the row says so | upgrade recovery, no upgrade state (founder rule 2026-11-28; row-specific after review NM-06) | diff agent records and `config.json` against the expected files and the fresh install's key paths | stable with fixture |
| ST-28 | US-2 AS9, C-25 | On Linux with an enforcing kernel sandbox, on macOS, and on Windows (or a failed sandbox): Mia reads a gated project-skill file with a file tool and with `bash cat`, and runs its helper | file tool and helper refused everywhere; `bash cat` refused where the kernel layer enforces it; the run log says which platform paths are app-level only | project-skill gate per platform | record the platform table | per platform |
| ST-29 | US-13 AS1–3, AS5–6 | `delete_task` Ask, Auto-approve off: answer Mia's card with "Allow for 3 hours"; repeat it with other arguments in another chat, another workspace, a helper Mia starts for herself, and by Jim; then shell cards: a command family, a file-access card and a network card naming one address, each with "Allow for 1 hour", repeated from another chat; `set_channel_outbound` card | no card anywhere offers Always Allow; Mia's repeats run without a card (whole tool), Jim's asks; each narrow card covers only its subject (Dataset D17); the result line names the end time; `set_channel_outbound` unchanged | every approval card | re-drive one repeat as Jim and one with other arguments | stable |
| ST-30 | US-13 AS4, US-2 AS10 | With running allowances for Mia (a tool call used by her scheduled task, a path, an address): run the task; record checksums of `config.json`, the agent files and `system`; restart; run the task again; answer one card with a stale client sending `allow` | task runs before the restart and is skipped with the unattended text after it, without waiting; checksums unchanged; the stale answer gets the 400 text | allowance lifetime | compare checksums; read the task conversation | stable; the end-of-time case needs the test clock (automated tests 57, 61) |
| ST-31 | US-3 AS2 | On the phone width (390 × 844) and with the keyboard: Mia → Tools, global Ask and global Deny rows | the tooltip opens on tap and on focus; the link is reachable by Tab and is 44 px; screen reader reads the reason | locked badge accessibility | re-drive with a screen reader | stable |
| ST-32 | US-13 AS8 | Mia and Jim each hold a `delete_task` allowance and Mia a `weather` skill allowance: set `delete_task` for Mia to Ask on her Tools tab; set global `delete_task` Ask → Allow → Ask on Skills & Tools; ask Omnipus to set Mia's `weather` to Deny, then clear it; save a same-value change | after Mia's change only Mia asks; after the global change both ask; the skill change ends Mia's skill allowance; the same-value save ends nothing; the audit log lists each ended allowance with the actor | allowance ending on permission change | read the audit log | stable |
| ST-33 | US-13 AS9 | With Auto-approve on: ask Mia to run `environment_setup` for one package, mount a folder with `request_mount`, send an email, run a `browser_evaluate` script and change one setting with `set_config`; answer each card with Approve Once; ask for each again; send `allow_for` to one card through the API | every card offers Approve Once and Deny only, showing the exact action; every repeat shows a new card; the API answer gets 409 | exact-action tools | re-drive `environment_setup` twice and read both cards | stable |

### Test Implementation Order

| Order | Test Name (placeholder) | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | TestSkillPolicyResolution_Table | Unit Go | S-09, S-13, S-19, S-29, S-30, S-31, S-35 | Dataset D1 |
| 2 | TestSkillPolicyReconcile_NewSkillAsk | Unit Go | S-17 | self-heal additive, never overwrites |
| 3 | TestSkillPolicyUpgrade_FromSkillTool | Unit Go | S-16 | Dataset D1 upgrade rows |
| 4 | TestSkillTrustMigration_LegacyValues | Unit Go | S-33 | Dataset D3 |
| 5 | TestPermissionsPatch_RefusesLegacyTrustValues | Integration Go | S-36 | 400 "Use allow, ask or deny." on `PATCH`; the legacy endpoint is retired (test 46) |
| 6 | TestInstall_TrustDenyRefusesUnverified (REST and install_skill) | Integration Go | S-27, S-28 | nothing on disk afterwards |
| 7 | TestSkillVerified_LocalChangeUnverifies | Integration Go | S-34 | stored verification + local change |
| 8 | TestPermissionsPatch_SkillAndVisibilityMembers | Integration Go | S-01, S-09, S-39 | Dataset D6 |
| 9 | TestVisibilityStartingLevels_Upgrade | Unit Go | S-41 | Dataset D7 |
| 10 | TestManifest_LevelsShapeTurn | Unit Go | S-38, S-40, S-44 | Dataset D2 |
| 11 | TestSkillsSummary_Levels | Unit Go | S-42, S-43 | menu per level |
| 12 | TestSkillLoad_AskUnattendedRefused | Integration Go | S-12 | unattended text with Auto-approve off; loads with Auto-approve on |
| 13 | TestSkillApprovalFrame_CarriesSkill | Integration Go | S-10, S-11 | C-9 |
| 14 | TestAgentToolsPut_SkillsPolicies | Integration Go | S-21, S-22, S-23, S-25 | C-4 envelope; S-22 as a direct API call (422) |
| 15 | TestSetAgentPermissions_SkillPolicyChangesParity | Integration Go | S-70 | parity |
| 16 | TestGetAgent_SkillPermissions | Integration Go | S-74 | C-9 |
| 17 | TestInstallSkillCategoryParity | Integration Go | S-71, S-72 | parity |
| 18 | TestSetConfig_RefusesSandboxSkillKeys | Integration Go | S-73 | refusal row |
| 19 | TestMcpAdd_WritesAskWildcard | Integration Go | S-59, S-04 | C-7 |
| 20 | TestWorkflowStart_W5 | Integration Go | S-53 | C-8 |
| 21 | ToolPolicyEditor.noPresets.test | Unit TS | S-18 | no preset text; presets module absent |
| 22 | ToolPolicyEditor.locks.test | Unit TS | S-19, S-22, S-24 | Dataset D1 screen rows; Omnipus Ask disabled with its reason (no click on a disabled control) |
| 23 | SkillsToolsPage.setAll.test | Unit TS | S-02, S-05, S-06, S-48 | Dataset D4 |
| 24 | SkillsToolsPage.autosave.test | Unit TS | S-01, S-07, S-08 | "Saving…" within 100 ms; request within 1,000 ms when nothing is pending; error and 503 |
| 25 | SkillsToolsPage.trust.test | Unit TS | S-26, S-32 | meanings, warning |
| 26 | SkillsToolsPage.levels.test | Unit TS | S-37, S-39, S-44, S-51 | menu, lock text, notes |
| 27 | SkillsToolsPage.search.test | Unit TS | S-45 to S-50 | |
| 28 | InstallReview.category.test | Unit TS | S-55, S-56, S-27 | Dataset D5 |
| 29 | NewSkillMenu.test | Unit TS | S-52, S-54 | |
| 30 | SkillsToolsPage.states.test | Unit TS | S-66, S-67, S-132 | S-132 rows read-only with Retry; the S-131 callout cases are retired with S-131 (founder rule 2026-11-28) |
| 31 | skills-tools.spec.ts | E2E | S-03, S-15, S-20, S-57, S-58, S-60 to S-65, S-68, S-69 | targeted spec file |
| 32 | Rider tests (one each) | mixed | S-75 to S-81 | per rider |
| 33 | TestSkillAuthority_FoldersFollowOutcome | Integration Go | S-85, S-86, S-87 | E6/E7 app-level and kernel grant lists |
| 34 | TestSkillConsent_Table | Unit Go | S-10, S-11, S-30, S-31, S-93 | Dataset D10 |
| 35 | TestInstallReview_StageBindsRevision | Integration Go | S-94, S-95 | C-13 |
| 36 | TestFullSkills_SentinelAndBudget | Integration Go (scripted provider, request capture) | S-42, S-89, S-90 | Dataset D9 |
| 37 | TestVisibilityTransitions | Integration Go | S-91, S-92 | Dataset D11 |
| 38 | TestPermissionsPatch_PerKey | Integration Go | S-82, S-99 | Dataset D6 |
| 39 | TestMcpMigration_CoverageMatrix | Unit Go | S-88 | Dataset D8, every dimension from the row; asserts all 11 rows ran |
| 40 | TestProjectSkillKeys | Unit Go | S-102 | C-19; literal winners for the granted and the not-granted agent |
| 41 | TestToolSearch_FindsSkills | Integration Go | S-96 | C-17 |
| 42 | TestUpdateAgentToolsCfg_PreservesSkillPolicies; TestCreateAgent_SkillPolicyChangesParity | Integration Go | S-97, S-103 | C-21 |
| 43 | TestWriteSkillWorkflow_PublishesCategorisedSkill; TestEditSkill_SetCategoryOnce | Integration Go | S-98, S-104 | C-21 |
| 44 | SaveQueue.states.test | Unit TS (controlled transport through the app-level queue) | S-83, S-117 | Dataset D12 |
| 45 | SkillsToolsPage.focus.test | Unit TS | S-100 | |
| 46 | TestLegacySecurityEndpoints_Retired | Integration Go (plus a repository guard over `src/`, `pkg/`, `cmd/`, `tests/`) | S-101 | C-22 row 6 |
| 47 | TestSkillConsent_Table (every Dataset D10 row); ApprovalCard.offeredActions.test | Unit Go; Unit TS | S-105 to S-109 | decision table, new-skill default, ST-Q8 A, ST-Q9 A, ST-Q10 A |
| 48 | PerformanceSection.noToolsOnDemand.test; TestPerformancePut_RejectsToolsOnDemand; TestSetConfig_RefusesRetiredManifestKeys; TestSettingsAgentWriteParity (updated) | Unit TS; Integration Go | S-44 | ST-Q4 B removal on the Settings base; the guard moves in the same change (C-22 rows 2–4) |
| 49 | TestSkillPermissionsUpgrade_StopAtEveryStep | Integration Go through the real entries (`skillperm.ConvertAgentRecords`, `config.LoadConfig`) on files; seam `func fileutil.SetWriteFaultForTest(f func(path string, data []byte) error) (restore func())` (new, `pkg/fileutil/file.go`, the pattern of `pkg/fileutil/atomic_sync_dir.go::SetDirSyncFaultForTest`), consulted by `fileutil.WriteFileAtomic` before the temp file is created; returning an error fails that write with nothing created; it may also block (park) | S-110, S-16 | Dataset D13 rows 1–8, 12, 13, 17, 18 (rewritten, founder rule 2026-11-28): stop before the n-th write for every n; asserts each write is one whole file, each row's own columns, and that a further start makes no conversion or repair write |
| 50 | TestProjectSkillPathGate; TestProjectSkillPathGate_Kernel (Linux, macOS) | Integration Go | S-111 | Dataset D14 |
| 51 | TestSkillApprovalActions_Handler | Integration Go | S-112, S-114 | C-24 offered actions with time choices, re-check, replay, partial failure, audit |
| 52 | TestSkillApproval_TimedAllowanceReachesScheduledRun | Integration Go (handler → in-memory allowance → scheduled run → restart, scripted model, test clock) | S-109, S-113 | C-24, C-26 |
| 53 | TestInstallReview_OutcomeStatus; InstallReview.lostAnswer.test | Integration Go; Unit TS | S-115 | Dataset D15 |
| 54 | TestSkillKeyValidation_AllWriters | Integration Go | S-116 | Dataset D16 |
| 55 | TestInstallSkill_OneCard | Integration Go | S-118 | C-12 rows 6–8 |
| 56 | TestToolSurface_ConversionToolSearch | Integration Go (request capture) | S-41, S-44 | Dataset D7 row 16; goal-narrowed and normal requests |
| 57 | TestApprovalGrantStore_AgentScopedTimed | Unit Go (controlled clock) | S-11, S-120, S-122 | key by agent; whole-tool key for ordinary tools, narrow keys for shell; end times; later end wins; a new store after restart holds nothing; `ClearAgent` |
| 58 | TestToolApprovals_TimedActions | Integration Go | S-119, S-124, S-125 | `offered_actions`, `allow_durations`, the four refusals, always-ask unchanged, `expires_at`, audit fields |
| 59 | TestShellTimedAllowances_NarrowSubjects | Integration Go (enforcing sandbox on Linux; egress proxy) | S-121 | Dataset D17, including token revocation at the end |
| 60 | TestTimedAllowance_HelpersAndOtherAgents | Integration Go | S-120 | a self-helper shares the allowance; a delegation to another agent asks; no grant copy at spawn |
| 61 | TestTimedAllowance_Unattended | Integration Go (scripted model, test clock) | S-123 | runs within the time; refused after it with the unattended text; never waits |
| 62 | ApprovalCard.timeChoices.test; ToolPolicyEditor.lockTooltip.test; repository guard for card labels | Unit TS; guard over `src/` and `pkg/` | S-119, S-126, S-127 | labels, scope line, result line, tooltip and link; no "Always Allow" or "Allow everywhere" label in production code |
| 63 | TestTimedAllowance_EndsOnPermissionChange | Integration Go (each writer: PATCH, PUT agent tools, `set_agent_permissions`, config reload) | S-128 | Dataset D17 rows 10, 11, 14–20; audit entries; egress token revoked for `bash` |
| 64 | TestToolApprovals_ExactActionToolsNoTimeChoice | Integration Go | S-129 | Dataset D17 rows 21–25: `offered_actions`, empty `allow_durations`, 409, no allowance recorded, second call asks; `ExactActionTools` holds exactly the five names |
| 65 | TestFullSkills_PageSizeFields | Integration Go (fixed token estimator; `GET /skills`, `GET /agents`; request capture) | S-130 | Dataset D18 rows 1–3, 6–10 server side: literal `full_tokens` and `full_skills_tokens`; plus, for Mia, `full_skills_tokens` equals the Full-skills charge captured from Mia's next turn (same selection as C-14) |
| 66 | SkillsToolsPage.fullSize.test | Unit TS | S-130 | Dataset D18 rows 1–5, 11–14 screen side: row text, rounding, warning names, exactly half, absent or 0 window, `GET /agents` failing, re-read after a saved level |
| 67 | TestPermissionsGet_UpgradeUnfinished — **retired** (founder rule 2026-11-28) | — | S-131 (retired) | the member no longer exists; the ID 67 is not reused |
| 68 | TestUpgrade_LeavesNoUpgradeState | Integration Go (two pre-release fixtures, one fresh-install fixture of the same build) | S-133, S-110 | SC-010: file list and key paths compared with the fresh install; none of the named old keys or upgrade files; `GET /security/permissions` has no `upgrade_unfinished` |
| 69 | TestUpgrade_DecidesFromDataOnly | Unit Go on the pure entries `config.ConvertConfigJSON(raw []byte, inv config.SkillInventory) ([]byte, config.ConfigConversion, error)` and `config.ConvertAgentRecordJSON(raw []byte, projectSkills []string, inventoryComplete bool) ([]byte, bool, error)`, plus one integration case each through `config.LoadConfig` and `skillperm.ConvertAgentRecords` on files | S-110, S-16 | each old-shape trigger of C-19 alone starts the conversion of its file; `Skill: allow` alone, an empty trust and `tools.mcp.discovery.enabled` alone do not; a new-shape file is returned byte-identical; a key that already holds a value is never overwritten; with `inventoryComplete`/`inv.Complete` false an old `Skill` value is returned unchanged; S-16 rows 1–6 |
| 70 | TestUpgrade_UnwritableFileKeepsOldLimits | Integration Go (the write fault of `fileutil.SetWriteFaultForTest` for one agent record or `config.json`, observed to fire; real unavailable mount, unreadable mount record and unreadable skills folder) | S-110 | Dataset D13 rows 9, 10, 11, 14, 15, 16, 19, 20, 21, 22: per row, the stored bytes, the loaded config, the outcome of a real turn, `GET /security/permissions`, a `PATCH` and its read-back all match the row's columns; the Error or Warn named in `<diagnostic>` at each start; no surface reports a saved result that was not stored; after the repair the row's `<after_repair>`; a stored Deny never becomes Ask and a stored Allow never becomes Ask; with a kept old master, an explicit per-skill value still decides and the `Skill` tool is not removed (NM-02, NM-04, R2-02, W-02); row 22 (founder answer Q-UPG1 A) through every writer of C-19's refusal table — `PATCH /security/permissions`, `DELETE /mcp-servers/{id}`, `PATCH /mcp-servers/{id}`, the system agent's MCP removal — each answers 409 `skills_not_listable` with the exact sentence (the tool: the same sentence), `config.json` stays byte-identical, nothing is audited for the refused change, the live config is unchanged; a mutation that refuses from anything but the document and skill list read inside the lock, or that saves and drops the old value, turns it red; revision 7 adds: `remove_mcp_server` on the last `deferred` server (the same sentence; nothing published), and `add_mcp_server` updating that server keeps its `deferred` member and saves (revision 8); a command-line writer (prints the sentence on stderr, exits non-zero, file byte-identical); a load-time repair of another feature that would remove the last old value (skipped, one Error naming the file, the load goes on with the converted view); each 409 body validated against the canonical `contracts/components/schemas/ErrorResponse.yaml` (through `pkg/gateway/inboundschemas/ErrorResponse.yaml` as synced by `scripts/gen-contracts.sh`), not only field by field; (W5-01) a `PATCH /mcp-servers/{id}` that changes an environment secret and a header secret of the last `deferred` server: 409, `config.json` byte-identical, both credential values read back unchanged from `credentials.Store`, and an armed `fileutil.SetWriteFaultForTest` hook records zero calls; (N5-01) `workspace.ListWorkspaceIDs` failing: `reason: workspace_list_unreadable`, the sentence with "your workspaces" and "the list of workspaces can't be read", no `workspace_id` |
| 71 | TestAgentWriters_RefuseSkillToolValue | Integration Go (each writer: REST create, `PUT /agents/{id}/tools`, `PUT /agents/{id}`, `create_agent`, `set_agent_permissions`, package import) plus a seed check | S-133 | C-21 row "per-agent `Skill` tool value": `ask`/`deny` → 400 with the fixed sentence, nothing stored; `allow` accepted and not stored; on an upgraded agent GET → change another tool → PUT succeeds with both request envelopes (with and without `Skill` in the map); a fresh agent is created and a new-shape package imported without a `Skill` value; an old package with `Skill: ask` or `deny` imports with per-skill overrides for the kept skills and no `Skill` entry, never dropped; no seed writes a per-agent `Skill` value (NM-03) |
| 72 | TestUpgrade_DelayedWriterKeepsLaterChange | Integration Go, two writers with separate store instances and file handles (the sidecar flock excludes separate open files in one process as across processes), the delayed one parked by `fileutil.SetWriteFaultForTest` | S-110 | NM-01: (a) `config.json`: writer 1 is a command-line config load converting an old file, parked; the gateway's `PATCH` of `plan` to Deny must wait for it and, once writer 1 resumes, lands on writer 1's converted file; the result holds `plan: deny`, writer 1's conversion and every unrelated setting; (b) the same with writer 1 already finished and a second, stale converter started from the old bytes: it re-reads inside the lock and writes nothing; (c) an agent record: `ConvertAgentRecords` parked on Mia's record while `agentstore.Store.Update` changes her `plan` override; both results survive; (d) a mutation that reads the file before taking the lock turns (a)–(c) red; (e) one process, with `fileutil.SetFlockNoopForTest` (new; makes `WithFlock` call straight through, as on Windows): a reload (`newReloadConfigLoader`) or self-heal writer parked inside `UpdateConfigFile` while a REST `PATCH` runs; the PATCH cannot acknowledge until the reload is released, and its value survives; a mutation that moves the in-process mutex out of `UpdateConfigFile` turns (e) red (R2-01); (f) system-agent operations (revision 8): the gateway's live config still holds the kept old `Skill: deny` and an old value of an unrelated key; another writer saves a new value on disk; the system agent then runs `configure_provider` for provider A: the result holds A's change, the newer value and the converted skill values, and no other key changed; (f1) stored `providers = [A + unknown member x, B + unknown member y]`; another writer changes B while `configure_provider` changes A's endpoint: both changes kept, x and y kept; (f2) the live config knows only MCP server S1, another writer added S2 on disk, `remove_mcp_server` S1: only S1 removed, S2 kept; (f3) `set_config` of `tools.web.x` while another writer sets `tools.web.y`: both kept; (f4) the same key set by both, and set by one while deleted by the other: the later commit wins, asserted by commit order under the lock, not by response order; (f5) a provider with two model rows, one of them stored with surrounding spaces and an unknown member: `configure_provider` changes its endpoint on the first try, both rows change, stored spelling of untouched members and the unknown member stay (R6-03, R4-01); (f6) a file without `providers`: `configure_provider` writes the default list with that one change; a `set_config` of an unrelated key on the same file writes no `providers` (R6-03); (f7) `set_config providers` while a stored row holds an unknown member: refused with the unshown-fields sentence, file byte-identical; with no unknown member: replaced as given; (f8) the R6-01 and R5-01 shapes through the real tools — a reorder, an edit that makes A equal B, an edit plus a removal, a removal plus an addition: every stored member of every row that the operation did not name is still there afterwards, or the save was refused; a mutation that saves by comparing two structs (any `before`/`after` diff) turns (f1), (f5) and (f8) red; (g) the real pairing writer (`orderedConfigJSONLocked` through `updateConfigJSONLocked`) against a turn-start heal (`storeNewProjectSkillKeys` inside `MutateConfig`), run concurrently many times with a deadlock detector: both finish, both changes kept (R3-03); (h) a system-agent save during a deferred file: the published live config keeps the converted view (old `Skill` fallback, fixed `Skill` tool), and a real turn reads it back (R3-03, revision 4); revision 6 adds, kept in revision 8: (i) a refusal through the system agent's tool (`remove_mcp_server` hitting the Q-UPG1 refusal; W4-01): nothing of it is published, the stored view is installed after `mutateConfigLocked` returned, no normal reload runs under `configMu` (an observed barrier proves the contention happened; a mutation that reloads under `configMu` deadlocks the test and turns it red), and the retry after repair succeeds; (j) a no-op save whose stored document holds newer unrelated values: live state equals the stored file afterwards, not the caller's clone; (s) a rejection while another writer added an agent: the installed view keeps the live roster, not the rejected clone's; cases (k)–(r) of revisions 6 and 7 are withdrawn with the struct comparison they tested |
| 73 | TestUpgrade_TrustAuditEntry | Integration Go observing the audit log itself (not a return flag) | S-33 | NM-05: (1) gateway start converts `warn_unverified` → exactly one entry from `auditOldTrustConversion`; a second start → none; (2) `config.LoadConfig` from a command converts first, then the gateway starts → no entry; (3) the gateway stops after the conversion write and before `initializeAgentLoop` ends → no entry at that or the next start; (4) empty trust → no rewrite, no entry |
| 74 | TestConfigWrite_CommitPoint | Integration Go (faults injected before the rename with `fileutil.SetWriteFaultForTest`, after the rename with `fileutil.SetAfterRenameFaultForTest`, at lock release with `fileutil.SetUnlockFaultForTest`; `SetDirSyncFaultForTest` does not reach `WriteFileAtomic` and is not used; plus the no-op result and `CreateConfigFileIfAbsent` with two creators and creator versus updater) | S-110 | W-01: before the rename → `Committed` false, `PATCH` 500 "Not saved…", `Deps.WithConfigOperation` rolls back, stored bytes unchanged; after the rename → `Committed` true, the error logged at Error, `PATCH` 200 (or 503), no rollback, the new bytes stored and live; the locks cannot be taken during a load → the load decodes `ReadConfigView`, writes nothing, logs an Error; revision 8 (W6-01), each through a real `PATCH /mcp-servers/{id}` that changes an environment and a header secret: success (both stored, config written); `step` failing after the first credential write (credential store write fault through `pkg/credentials/store.go::writeFileAtomicFn`): both values back, not saved; a fault before the rename: `undo` called, both values back, 500; an after-commit fault (`ErrAfterCommit`): `undo` not called, new values and new config kept, saved; `undo` itself failing: the answer names the credential reference and never says restored; a secret-only change with byte-identical config: no rename, new value stored, success |
| 75 | TestSelfHeal_AuxiliaryWorkAfterNoOp | Integration Go (stop via `fileutil.SetWriteFaultForTest` parking, then process restart, between the `config.json` replace and each auxiliary effect) | S-110 | R3-04: a stop after `config.json` lost a gone project key but before the agent-record cleanup, and a stop before `known-builtin-skills.json` grew: the next reconcile finds `config.json` already right (no replace) and still finishes the auxiliary effect; after a failed `config.json` write neither effect runs Revision 6 (W4-02) interleavings, each with an observed barrier between the config completion and the effect: (a) reconcile sees project key K gone and parks before the cleanup; the folder returns and another writer stores Mia's override for K; the cleanup then keeps K; (b) reconcile A will add {K, X} and parks; reconcile B writes {K, X, Y}; A resumes: the stored set is a superset of {K, X, Y}; (c) two no-op recoveries at once both finish, and the result equals one recovery; (d) revision 7 (R5-02): the binary ships built-in Y whose folder is not installed; a no-op heal runs and `known-builtin-skills.json` does not gain Y; Y is then installed and the next heal gives Y the new-release Allow and records it; a mutation that replays a precomputed gone list or writes a supplied set without the locked re-read turns it red |

**Positive controls (MAJ-014, R2-MAJ-008).** Each of tests 33, 34, 36, 37, 38, 49–65, 68–75 and the migration tests ships with a recorded mutation probe run by CHECK: remove the outcome filter from `LoadableFolders`, skip the trust floor, inject Full unconditionally, keep the old tier lists, write the full map instead of the named keys, drop one audit call, reset one migrated value, read an old value from a file other than the one being converted, split one file's conversion into two writes (new values first, old keys removed later), keep one old-shape key after the conversion, write any file or key that names the upgrade, run the self-heal before the `config.json` conversion, overwrite a key that already holds a value, drop the project-skill gate leg, let a broader mount grant re-admit a gated folder, accept an action that was not offered, skip the revision re-check, decide an install from the skills list, start the next save before the previous one resolves, reverse the shelf precedence, key an allowance by session again, ignore an allowance's end time, copy allowances to another agent at delegation, write an allowance to disk, offer a time choice on an always-ask tool, key an ordinary tool's allowance by its arguments, skip ending allowances on one permission writer, offer `allow_for` for one exact-action tool, count an Ask or not-granted skill in `full_skills_tokens`, drop the read-time translation of an agent record that could not be rewritten (founder rule 2026-11-28 replaces the probe that listed a skipped agent), and (review of 106f63ef9) read a file before taking its writer lock, let one writer bypass `config.UpdateConfigFile`, remove an old `Skill` value with an incomplete inventory, treat an unreadable skills folder or mount as empty, let the self-heal add skill keys beside a kept old `Skill` value, serve `GET`/`PATCH /security/permissions` from the raw old maps, refuse `Skill: allow` in a complete agent map, drop an old package's `Skill: ask`/`deny`, decide a conversion from a log line or any signal other than the file's own old-shape values, write the trust audit entry from a converter other than the gateway's start, and (revision 3) take the `config.json` in-process mutex outside `UpdateConfigFile`, write a whole struct instead of its changed keys, convert the rest of a file whose conversion is deferred, let a kept old `Skill` master remove the `Skill` tool, and report "Not saved" for an error after the rename, and (revision 4) replace a list another writer changed, delete a container instead of its known members, call an agent-loop accessor inside the `config.json` lock, publish raw saved bytes of a deferred file, gate the heal's auxiliary work on `Committed` alone, (revision 8) save a system-agent change by comparing two structs instead of applying its named operation, write default providers from any operation but the one that changes providers, call `undo` after a committed rename, report a failed undo as restored, (revision 6) publish the caller's clone after a no-op, reload under `configMu` after a refusal, map the Q-UPG1 refusal to the generic 500, replay a precomputed gone-key list, write a known-builtins set without the locked re-read, and let an ordinary save remove the last old value of a deferred file instead of refusing it (Q-UPG1 A). Each probe must turn its test red. Expected values come from this spec and from literal pre-upgrade fixtures, never from calling the new resolver or builder.

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
| 9 | allow, none, ask, no, on | trust survives God Mode | ask (trust card) | BDD Scenario: S-31 | (ST-Q2 A) |
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
| 7 | old install with Load tools on demand off | migrated | static tools Full, definition in tool list | BDD Scenario: S-44 | ST-Q4 B |
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
| 9 | `PUT /security/skill-trust` `warn_unverified` | retired endpoint | 404; nothing stored | BDD Scenario: S-101 | C-22 |
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
| 15 | `skill_policies: {plan: deny}` when stored `deny` | same-value retry | 200; nothing written; no audit entry; agents reloaded | BDD Scenario: S-99 | R2-MAJ-004 |
| 18 | `policies: {}, skill_policies: {}` | only empty maps | 400 "Nothing to change." | BDD Scenario: S-07 | differs from row 15 |
| 19 | `skill_policies: {"project:w9/notes": ask}`, no workspace W9 | unknown project key | 400 "`project:w9/notes` is not a known skill." | BDD Scenario: S-116 | C-23 |
| 20 | valid change; the atomic write fails (test seam) | not stored | 500 "Not saved: the settings file could not be written."; stored file unchanged | BDD Scenario: S-117 | |
| 16 | `skill_policies: {"project:w1/notes": ask}` | project key | 200 | BDD Scenario: S-102 | C-19 |
| 17 | new skill installed, then GET | new-skill default | its key present with `ask` | BDD Scenario: S-17 | ST-Q3 B |

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
| 9 | off, –, –, any, any static tool | uncompressed | full | BDD Scenario: S-44 | ST-Q4 B |
| 10 | off, on, unset or `true`, any, MCP tool | effectively deferred | hidden (server key) | BDD Scenario: S-41 | |
| 11 | off, off, `true`, any, MCP tool | discovery off | full (server key) | BDD Scenario: S-41 | `serverIsDeferred` returns false when discovery is off |
| 12 | off, on, `false`, any, MCP tool | not deferred | full (server key) | BDD Scenario: S-41 | |
| 13 | server offline at upgrade, rows 5–12 | offline | server key written from its stored config; its tools get it when they appear | BDD Scenario: S-41 | |
| 14 | every skill | skills | name_and_description | BDD Scenario: S-41 | |
| 15 | any row, second start | repeat | nothing written | BDD Scenario: S-41 | idempotent |
| 16 | off, –, –, any, `ToolSearch` | infra tool | locked full; offered in the normal request after the conversion, absent before | BDD Scenario: S-44 | named change (C-6, SC-003) |
| 17 | any of rows 1–13, after the first start | old keys removed | `config.json` holds no `tools.manifest.compressed`, no `tools.manifest.preview_all_lazy` and no server `deferred`; levels as rows 1–13 | BDD Scenario: S-110, S-133 | founder rule 2026-11-28 (C-6, C-19) |
| 18 | fresh install, no stored level | starting level | the compression-on rows (1–3, 5): the 36 tools full, `serve_web` name_and_description, every other static tool and every MCP server hidden | BDD Scenario: S-133 | self-heal (C-2); the same table a default old install converts to |

#### Dataset D8: External tools migration (C-18) (ST-Q5 A)

S-88 takes every column from the row (global map, agent entry, tool, service, God Mode, start phase); rows without a named service use `github` and tool `mcp_github_read`; the test asserts that all 11 rows ran.

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

#### Dataset D9: Full skills, consent and budget (C-14) (ST-Q1 A)

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | Full, Allow, verified, chat | allowed | sentinel in first request | BDD Scenario: S-42 | |
| 2 | Full, Ask, verified, chat, not approved | ask | sentinel absent; listed with description | BDD Scenario: S-89 | |
| 3 | Full, Allow, unverified, trust Ask, no approval | trust ask | sentinel absent | BDD Scenario: S-89 | |
| 4 | row 3 after "Approve this version" | approved | sentinel present from next turn | BDD Scenario: S-89 | |
| 5 | Full, Ask, unattended run | unattended | sentinel absent; load refused | BDD Scenario: S-89 | |
| 6 | Full, Deny | deny | not listed, sentinel absent | BDD Scenario: S-89 | |
| 7 | context 8,000; output reserve (`maxTokens`) 1,000; headroom ceil(8,000 / 20) = 400 (`contextBudget`); pinned core 2,000; Full skills 3,000 (charged once, inside the pinned overhead); tool definitions 1,400; current message 200 (older history is trimmed as today) | just fits | budget B = 8,000 − 1,000 − 400 − 5,000 = 1,600 = 1,400 + 200: request sent | BDD Scenario: S-90 | fixed token estimators in the test |
| 8 | row 7 with Full skills 3,001 | one over | B = 1,599 < 1,600: not sent; C-14 text | BDD Scenario: S-90 | |
| 9 | fits on model A, switch to smaller model B | model switch | next turn not sent; C-14 text | BDD Scenario: S-90 | |
| 10 | a Full skill grows past the limit between turns | content growth | next turn not sent | BDD Scenario: S-90 | |

#### Dataset D10: Consent (C-12 decision table)

Every row states all inputs; "–" means the input does not matter for that row. Defaults are never implied.

| # | Input (entry; global; agent's own; verified/trust; revision approved; Auto; God; timed allowance; run) | Boundary Type | Expected Output (card or result; stored) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | load; Ask; none; verified; –; off; off; none; chat | permission open | permission card: Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny | BDD Scenario: S-10 | row 2 |
| 2 | load; Ask; none; verified; –; on; off; none; chat | Auto settles | loads, no card | BDD Scenario: S-10 | `Skill` AutoRuns |
| 3 | load; Ask; none; verified; –; off; off; running for this agent; chat | allowance settles | loads, no card | BDD Scenario: S-11 | C-26 |
| 4 | load; Ask; none; verified; –; off; off; ended, or running for another agent only; chat | no allowance for this agent | permission card again | BDD Scenario: S-11 | C-26 |
| 5 | load; Allow; none; unverified, trust Ask; no; on; on; none; chat | trust open | trust card: "Approve this version", Deny; no time choice | BDD Scenario: S-31 | ST-Q2 A |
| 6 | load; Allow; none; unverified, trust Ask; same revision; off; off; none; chat | approved | loads | BDD Scenario: S-93 | |
| 7 | load; Allow; none; unverified, trust Ask; older revision; off; off; none; chat | changed | trust card | BDD Scenario: S-93 | |
| 8 | `/name`; Allow; none; unverified, trust Ask; no; off; off; none; chat | slash | trust card | BDD Scenario: S-93 | row 5 |
| 9 | `/name`; Ask; none; verified; –; off; off; none; chat | slash settles | loads, no card | BDD Scenario: S-13 | |
| 10 | load; Ask; none; verified; –; off; off; none; unattended | unattended | skipped with the unattended text; no card; no wait | BDD Scenario: S-12 | |
| 11 | load; Ask; none; verified; –; on; off; none; unattended | Auto in a run | loads | BDD Scenario: S-12 | Auto checked before the refusal |
| 12 | card open; then the global value set to Deny; then Approve Once | stale | 409 "This skill is now set to Deny."; nothing stored; call refused | BDD Scenario: S-93 | C-24 |
| 13 | load; Ask (new); none; unverified, trust Ask; no; off; off; none; chat | both open | combined card: "Approve this version", "Approve this version and allow for" 1 hour / 3 hours / 1 day, Deny | BDD Scenario: S-106 | row 4 |
| 14 | row 13 with Auto on | Auto settles the permission part | trust card with "Approve this version", Deny | BDD Scenario: S-106 | |
| 15 | load; Ask (new); none; unverified, trust Ask; approved at review; off; off; none; chat | trust answered | permission card | BDD Scenario: S-107 | |
| 16 | row 15 with Auto on | Auto | loads, no card | BDD Scenario: S-107 | R2-Q2 A |
| 17 | load; Ask (new); none; verified; –; off; off; none; unattended | unattended | skipped; no card | BDD Scenario: S-107 | ST-Q3 B |
| 18 | built-in skill added by an update | shipped default | stored Allow; loads, no card | BDD Scenario: S-108 | ST-Q10 A |
| 19 | load; Ask; none; project skill (unverified), trust Ask; new revision; off; off; none; chat | ST-Q8 A | combined card once per revision | BDD Scenario: S-105 | |
| 20 | load; Ask; none; verified; –; off; off; none; chat → "Allow for 1 day" → a later unattended run of the same agent within the day | person action | no stored change; that run loads; another agent's run skips | BDD Scenario: S-109 | R2-Q1 |
| 21 | load; Ask; **Ask**; verified; –; off; off; none; chat | agent's own Ask | permission card with the same time choices; a time choice settles the agent's own Ask like Approve Once | BDD Scenario: S-109 | |
| 22 | load; Allow; **Ask**; verified; –; off; **on**; none; chat | God Mode, agent's own Ask | permission card (Auto is never active in God Mode) | BDD Scenario: S-10 | |
| 23 | `install_skill` Ask; –; –; unverified, trust Ask; staged; on; off; none; chat | install, both | one install card: "Install and approve this version", Deny; no time choice | BDD Scenario: S-118 | row 6 |
| 24 | `install_skill` Ask; –; –; verified; –; on; off; none; chat | install, permission only | today's `install_skill` card | BDD Scenario: S-118 | row 7; `install_skill` AutoAsks |
| 25 | `install_skill` Allow; –; –; unverified, trust Ask; staged; –; off; none; chat | install, trust only | one trust card bound to the staged revision | BDD Scenario: S-118 | row 8 |
| 26 | any `install_skill` row; unattended | unattended | refused, nothing installed, staged copy removed | BDD Scenario: S-118 | |
| 27 | combined card answered after the revision changed | stale | 409 "The skill changed after this request."; nothing stored | BDD Scenario: S-112 | C-24 |
| 28 | the same answer sent twice | replay | second answer 404; nothing happens twice | BDD Scenario: S-112 | |
| 29 | load; Ask; none; verified; –; off; off; running for this agent; unattended | allowance in a run | loads | BDD Scenario: S-109 | C-26 point 5 |
| 30 | load; Ask; none; verified; –; off; off; running; chat, after a restart | restart | permission card (the restart ended it) | BDD Scenario: S-113 | C-26 |
| 31 | load; Ask; none; verified; –; off; off; running, then the agent's own value set to Ask; chat | permission change | permission card (the change ended it); 1 `approval.allowance_ended` | BDD Scenario: S-128 | C-26 point 7 |

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

Driven through the app-level queue with a controlled transport; never by calling the row component directly.

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | 200 | saved | "Saved" | BDD Scenario: S-01 | |
| 2 | 503 | saved, not applied | saved-not-applied text + Retry; Retry sends the same keys and gets 200 with no audit entry | BDD Scenario: S-08 | |
| 3 | 400 | refused | "Not saved: …" + the stored value from a fresh `GET` | BDD Scenario: S-07 | |
| 4 | response dropped | unknown | "Couldn't confirm…" then the stored value | BDD Scenario: S-83 | |
| 5 | first response takes 5 s; second click 100 ms after the first | serial | second row "Saving…" within 100 ms; second request starts only after the first response; no overlap | BDD Scenario: S-117 | |
| 6 | first request unknown; second click queued | unknown before next | second request starts only after the first's fresh `GET` answers or fails | BDD Scenario: S-117 | |
| 7 | 503 after leaving the page | late saved-not-applied | toast "A change on Skills & Tools was saved but is not applied to running agents yet." | BDD Scenario: S-117 | |
| 8 | 400 after leaving the page | late refusal | toast "A change on Skills & Tools was not saved: <reason>." | BDD Scenario: S-83 | |
| 9 | response dropped and the fresh `GET` fails | check failed | "Couldn't load what is stored." + Retry; the clicked value is not shown as stored | BDD Scenario: S-83 | |
| 10 | 500 | not stored | "Not saved: the settings file could not be written." + Retry | BDD Scenario: S-117 | |
| 11 | two queued clicks on the same key, the first not yet sent | same key | only the later value is sent | BDD Scenario: S-117 | |

#### Dataset D13: Upgrade stop points, files that cannot be written, incomplete skill lists (C-19) (rewritten, founder rule 2026-11-28; columns split after review NM-02, NM-06; rows 14–16 deferred and rows 19–21 added in revision 3, R2-02, W-02)

Fixture of S-110 (agents Mia, Jim, Ray as S-110's Given). A state is set up either by the test seam `fileutil.SetWriteFaultForTest` (stop or fail the n-th file write, for every n; test 49) or by placing the files directly. "Old" means the file as the release before this one wrote it; "new" means the file as this release's conversion writes it. "Cannot be written" means the atomic write fails, injected with `fileutil.SetWriteFaultForTest` returning an error for that path; a file mode of 0444 alone does not stop a rename, and a folder of 0555 fails every record in it. "Converted view" is the output of the C-19 conversion computed in memory from the stored bytes. The clean end state of S-110 is asserted only where a row says so: at the end of the first start for rows 1–8, 12, 13, 17 and 18, and only after the repair for rows 9, 10, 14–16 and 19–22; Ray's malformed file in row 11 is excluded from it while malformed. "No repair" means the second start runs on unchanged files.

| # | `<state>` (before the first start) | Boundary Type | `<stored>` after the first start | `<effective>` during the run | `<diagnostic>` | `<repair>` | `<after_repair>` | Traces to | Notes |
|---|---|---|---|---|---|---|---|---|---|
| 1 | everything old (stop before the first write) | nothing written | all files new | S-110 end state values | none | none | S-110 end state; no write | BDD Scenario: S-110 | |
| 2 | Mia's record old plus a stray `.tmp-*` beside it (stop inside Mia's write) | interrupted record write | all files new; the stray file still there, unread | S-110 end state values | none | none | S-110 end state; no write | BDD Scenario: S-110 | the generic atomic-write temp, not named after the upgrade |
| 3 | Mia new, Jim old, Ray old, `config.json` old | between agent records | all files new; Mia's record not written again | S-110 end state values | none | none | S-110 end state; no write | BDD Scenario: S-110 | Jim's and Ray's `Skill: allow` removed, not copied |
| 4 | every agent new, `config.json` old | agents done, config not | all files new; global values Deny, read from the old `config.json` itself | S-110 end state values | none | none | S-110 end state; no write | BDD Scenario: S-110 | the consult's crash case: no file needs another file's old value |
| 5 | every agent old, `config.json` new | config done, agents not (for example a restored backup of the agent folder) | all files new; Mia's overrides Ask from her own record; Jim and Ray lose `Skill: allow` | S-110 end state values | none | none | S-110 end state; no write | BDD Scenario: S-110 | |
| 6 | `config.json` old plus a stray `.tmp-*` beside it | interrupted config write | all files new; the stray file unread | S-110 end state values | none | none | S-110 end state; no write | BDD Scenario: S-110 | |
| 7 | everything new, self-heal not yet run | heal pending | the heal adds only absent keys | S-110 end state values | none | none | S-110 end state; no write | BDD Scenario: S-110 | |
| 8 | row 1, and before the start a person added `sandbox.skill_policies.plan: ask` to the old `config.json` and `tools.skills.policies.plan: deny` to Mia's old record | person's values | all files new; `plan` globally Ask, Mia's `plan` Deny | as S-110 end state except those two values | both kept values logged at Warn | none | same as stored; no write | BDD Scenario: S-110 | a key that already holds a value is never overwritten |
| 9 | Jim's old record holds `Skill: deny` (granted `deep-research`) and cannot be written; other files old | agent record cannot be written | Jim's record byte-identical to before; every other file new | Jim's `deep-research` Deny (converted view; the agent tab shows the override); everything else S-110 end state | Error naming Jim's file at each start | remove the write fault | Jim's record holds `deep-research: deny` and no `Skill`; clean end state | BDD Scenario: S-110 | no notice on any screen |
| 10 | `config.json` old and cannot be written; agent records old | config cannot be written | `config.json` byte-identical to before; agent records new | converted view: skills Deny, `Skill` Allow, levels per D7, trust Ask; `GET /security/permissions` shows exactly these; a `PATCH` answers 500 "Not saved: the settings file could not be written." and the next `GET` still shows the converted view | Error naming `config.json` at each start | remove the write fault | `config.json` new; clean end state | BDD Scenario: S-110 | no screen claims a value that was not stored |
| 11 | Ray's record malformed: (a) invalid JSON, (b) valid JSON with `tools.builtin.policies` an array, (c) valid with an unknown unrelated field | unreadable or wrongly shaped record | (a), (b) Ray's file byte-identical, not partly converted; (c) converted, the unknown field kept; every other file new | (a), (b) Ray cannot run (fails closed, as today); everything else S-110 end state | (a), (b) Warn naming Ray's file at each start | a person replaces (a)/(b) by a valid old record with `Skill: allow` | Ray's record new, no `Skill`; clean end state | BDD Scenario: S-110 | changing file permissions does not repair a parse failure |
| 12 | everything new, then a person writes `tools.manifest.compressed: true` back into `config.json` by hand (Omnipus stopped) | old key re-added | the key removed; no stored level changes (every level is present) | S-110 end state values | none | none | no write | BDD Scenario: S-110 | data decides, not a flag |
| 13 | a new-shape `config.json` with `Skill: allow`, no old key and no `skill_policies` (written by hand) | no trigger | not converted; the self-heal gives each skill Ask (built-ins of a newer release Allow) | those values; never looser than before | none | none | no write | BDD Scenario: S-110 | C-19 "treated as new shape" |
| 14 | row 1, and W1's mount folder is unavailable | mount unavailable | `config.json` byte-identical to before (every old key stays: the deferred conversion, R2-02); Mia's record byte-identical; Jim's and Ray's records new | converted view with the projection of C-19: the `Skill` tool Allow; every skill without its own value Deny (the old global value), Mia Ask on top; `notes` Deny if its folder comes back during the run; trust and levels as converted | Error naming `config.json` and Mia's record, cause "the mount W1 could not be read", at each start | make the folder available | S-110 end state (`project:w1/notes` globally Deny, never Ask from the heal); clean end state | BDD Scenario: S-110 | old values kept are real data, not a mark (NM-02) |
| 15 | row 1, and W1's mount record cannot be read | mount record unreadable | as row 14 | as row 14 | as row 14, cause names the mount record | repair the mount record | as row 14 | BDD Scenario: S-110 | |
| 16 | row 1, and `$OMNIPUS_HOME/skills` cannot be listed (permission error) | installed list unreadable | `config.json` byte-identical to before; agent records new (they need only their mounts) | every skill without its own value Deny; Mia's overrides Ask; the `Skill` tool Allow | Error naming `config.json`, cause the skills folder | make the folder readable | S-110 end state; clean end state | BDD Scenario: S-110 | |
| 17 | row 1, but `$OMNIPUS_HOME/skills` does not exist (no installed skills) | genuinely empty | all files new; only `project:w1/notes` gets a global value (Deny) | those values | none | none | no write | BDD Scenario: S-110 | missing folder = empty and complete; unreadable ≠ empty |
| 18 | stop after step A (agents new, Mia's `notes` Ask), then before the next start `notes` is removed from W1 and project skill `todo` is added; `config.json` old | stop between agents and config, inventory changed | `config.json` new: `project:w1/todo` globally Deny, no `notes` key; Mia's gone `notes` override removed by the heal | `todo` Deny for every agent; S-110 end state for the rest | none | none | no write | BDD Scenario: S-110 | each file reads the inventory of its own write |
| 19 | rows 14, 15 and 16 each again with the old global `Skill: allow` (and `tools.manifest.compressed` present, no per-skill keys); Mia keeps her own `Skill: ask` | incomplete list, old global Allow (R2-02) | `config.json` byte-identical to before, so `Skill: allow` and `compressed` stay as the trigger. Causes of rows 14 and 15 (W1 unreadable): Mia's record byte-identical (her `Skill: ask` needs W1); Jim's and Ray's records new. Cause of row 16 (installed list unreadable): every agent record new, Mia's `plan` and `notes` overrides Ask | global value of every skill without its own: Allow (kept). Agent overrides: Mia Ask on `plan` and `notes` (kept or translated); Jim, Ray none. Outcome: Mia's `plan` and `notes` Ask; every other agent and skill Allow; a returning `notes` Allow globally | Error naming `config.json` (and Mia's record for the W1 causes) and the cause, at each start | make the folder or record readable | global `plan`, `deep-research`, `project:w1/notes` Allow (never Ask from the heal); Mia's overrides `plan`, `notes` Ask; Jim, Ray none; clean end state | BDD Scenario: S-110 | the kept Allow is old data, not a pending flag; compared against the pre-upgrade fixture's behaviour (revision 4: R3-05) |
| 20 | row 19 with the row 14 cause (mount unavailable), started twice before the folder returns, then once after | restart before and after recovery | after starts 1 and 2: `config.json` and Mia's record byte-identical; start 2 makes no conversion write | at both starts: global Allow, Mia's overrides Ask, Mia's outcome Ask, everyone else Allow | the Error at each start | make the folder available before start 3 | after start 3: as row 19's result; start 4 makes no write | BDD Scenario: S-110 | |
| 21 | row 14 (global `Skill: deny`, W1 unavailable), plus `sandbox.skill_policies.plan: allow` in the old `config.json` and `tools.skills.policies.plan: allow` in Mia's old record | kept master beside explicit values (W-02) | as row 14 | global values: `plan` Allow (explicit), `deep-research` and `notes` Deny (kept). Mia's overrides: `plan` Allow (explicit), `notes` Ask (kept). Outcome: `plan` runs for Mia (Allow on both layers; the kept Deny does not remove the `Skill` tool); `deep-research` Deny; Mia's `notes` Deny (strictest of global Deny and her Ask) | as row 14 | as row 14 | global `plan` Allow (kept), `deep-research` and `project:w1/notes` Deny; Mia `plan` Allow, `notes` Ask; Mia's `notes` outcome Deny; clean end state | BDD Scenario: S-110 | the view equals the later conversion (add-only keeps `plan`) (revision 4: R3-05) |
| 22 | a minimal old `config.json`: global `Skill: allow`, `sandbox.skill_trust: warn_unverified`, no `compressed`, no `preview_all_lazy`, no `deferred`, no per-skill keys; W1 unavailable; then, during the run, a `PATCH` of `skill_trust` to `allow`; variants: the last remaining old key is a server's `deferred: true`, and it is removed by `DELETE /mcp-servers/{id}`, rewritten by `PATCH /mcp-servers/{id}`, or removed by the system agent | ordinary save would remove the last old value (R3-02; founder answer Q-UPG1 A) | `config.json` byte-identical to before, also after the refused save: the trust value is **not** saved (still `warn_unverified`), the server is still there | every skill Allow (kept global value); trust shows Ask (the converted view of `warn_unverified`), the screen returns to it after the refusal | the Error at each start; the save answers 409 `code: "skills_not_listable"`, `details: {reason: "mount_unavailable", workspace_id: "w1"}`, `error`: "Not saved: the skills in W1 can't be read right now (its folder isn't available), and saving this change would lose the permission they carried over from before. Save again once they can be read."; the system agent's tool fails with the same sentence; no audit entry for the refused change | make the folder available, restart, repeat the save | after the restart: `plan`, `deep-research`, `project:w1/notes` globally Allow, trust Ask (converted), clean end state; the repeated save then succeeds (trust `allow`, one ordinary audit entry) | BDD Scenario: S-110 | the refusal is decided from the file and skill list read inside the lock, never from stored state |

#### Dataset D14: Project-skill path gate (C-25)

| # | Input (folder; outcome; action) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `W1/.claude/skills/notes`; `deny`; `read_file` of `SKILL.md` | deny | refused naming the setting | BDD Scenario: S-111 | |
| 2 | same; `ask_pending`; `read_file` | ask | refused with the C-25 text | BDD Scenario: S-111 | |
| 3 | same; `allowed`; `read_file` | allowed | works | BDD Scenario: S-111 | |
| 4 | `W1/.omnipus/skills/notes`; `deny`; `grep` over `W1` | search | no line from the skill folder in the result | BDD Scenario: S-111 | |
| 5 | same; `deny`; list `W1/.omnipus/skills` | listing | `notes` not listed | BDD Scenario: S-111 | |
| 6 | same; `deny`; run `helper.sh` | helper | refused | BDD Scenario: S-111 | |
| 7 | same; `deny`; `read_file` through a different case or a symlink spelling of the folder | alias | refused | BDD Scenario: S-111 | path identity |
| 8 | same; `deny`; the work dir is `W1` itself | work dir | refused (gate runs before the own-tree exception) | BDD Scenario: S-111 | precedence |
| 9 | inner mount `W1/sub` with its own `.claude/skills/x` at `deny`; read through the outer mount | nested mount | refused | BDD Scenario: S-111 | |
| 10 | row 1 with `edit_skill` Allow | editor exception | read works; helper still refused; nothing loads | BDD Scenario: S-111 | |
| 11 | row 1, then Allow set; next turn | next turn | refused this turn, works next turn | BDD Scenario: S-111 | |
| 12 | row 1; `bash cat` on Linux with an enforcing kernel sandbox | kernel | refused by the kernel layer | BDD Scenario: S-111 | 🔶 Inferred feasible; ST-28 records it |
| 13 | row 1; `bash cat` on an app-level-only path | degradation | refused when the operand is visible; the platform is logged as app-level only | BDD Scenario: S-111 | DOC-004 limit |

#### Dataset D15: Install outcome after a lost answer (C-13)

| # | Input (situation) | Boundary Type | Expected Output (shown) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | lost before publishing started; status `staged`, then `expired` | lost before publish | "Not installed." | BDD Scenario: S-115 | |
| 2 | lost after publishing finished; `installed`, `installed_revision` R2 | lost after publish | "Installed weather." | BDD Scenario: S-115 | |
| 3 | `publishing` for 3 s, then `installed` | slow publish | "Checking…", then "Installed weather." | BDD Scenario: S-115 | |
| 4 | `publishing` for more than 60 s | still running | "Couldn't confirm the install. Open Skills to see what is installed." + Retry | BDD Scenario: S-115 | |
| 5 | replacement: old `weather` R1 listed; status `failed` | replacement | "Not installed: <reason>."; the list still shows R1 and is not used to decide | BDD Scenario: S-115 | |
| 6 | status read fails or 404 | unknown | "Couldn't confirm the install. …" + Retry | BDD Scenario: S-115 | |
| 7 | Install pressed twice with one review id | repeated submit | one publish; second answer 202 or 200 with the same result | BDD Scenario: S-115 | |
| 8 | Install after Cancel | consumed review | "Not installed."; 409 or 410 again on retry | BDD Scenario: S-115 | |

#### Dataset D16: Skill keys across writers (C-23)

| # | Input (writer; input) | Boundary Type | Expected Output (result; stored) | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | `PUT /agents/{id}/tools`; `skills.policies: {"project:w1/notes": deny}` | project key, mounted | 200; Mia's override present | BDD Scenario: S-116 | |
| 2 | same writer; `{"project:w2/todo": deny}` | not mounted | 400 "`project:w2/todo` is not a skill this agent can use."; nothing stored | BDD Scenario: S-116 | |
| 3 | `set_agent_permissions`; `{set: {"project:w1/notes": deny}}` | project key | stored, as row 1 | BDD Scenario: S-116 | |
| 4 | `set_agent_permissions`; `{set: {"project:w2/todo": deny}}` | not mounted | refused with the row 2 text | BDD Scenario: S-116 | |
| 5 | `create_agent` mounted on W1; `skill_policy_changes: {set: {"project:w1/notes": ask}}` | create | stored | BDD Scenario: S-116 | |
| 6 | any writer; `{plan: deny}` (installed, not granted) | installed, not granted | stored | BDD Scenario: S-116 | |
| 7 | any writer; `{nope: deny}` | unknown | 400; nothing stored | BDD Scenario: S-116 | |
| 8 | `PUT /agents/{id}/tools`; `skills: null` | null | 400 naming `skills` | BDD Scenario: S-116 | |
| 9 | `PUT /agents/{id}/tools`; `{revision, override_names: [], skills: {policies: {}}}` | clear | 200; no skill overrides; tool overrides unchanged | BDD Scenario: S-116 | |
| 10 | `PUT /agents/{id}/tools`; `override_names: ["bash"]` without `builtin` | envelope | 400 "override_names names tools that are not in this request." | BDD Scenario: S-116 | |
| 11 | `set_agent_permissions`; `{clear: true}` | clear | no skill overrides | BDD Scenario: S-116 | |
| 12 | `set_agent_permissions`; `{remove: ["project:w1/notes"]}` | remove | that override gone, others kept | BDD Scenario: S-116 | |
| 13 | W1 deleted; reconcile; then row 1 again | workspace gone | keys removed everywhere; row 1 now 400 | BDD Scenario: S-116 | |
| 14 | `PUT /agents/{id}` with `tools_cfg.mcp` only | unrelated write | skill overrides unchanged | BDD Scenario: S-97 | |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test Needed | Notes |
|---|---|---|---|
| Tool strictest-wins merge, God Mode floor, always-ask | compositor tests (`pkg/tools`) | No — must stay green unchanged | skills reuse the merge |
| Omnipus per-agent Ask refused | `rest_omnipus_guards` tests | Yes — skills variant (S-22) | |
| execute_plan confirm in the editor | `ToolPolicyEditor.test.tsx` | Yes — through Set all (S-05) | |
| Grant list is the gate | `TestSkillTool_LoadDeniedAndNotFoundAreDistinct` (ADR-072) | No — must stay green | |
| Saved-not-applied 503 text | tool-policies handler tests | Yes — moved to `PATCH /security/permissions` tests (the legacy endpoint is retired) | C-2, C-22 |
| Presets | `toolPolicyPresets.test.ts`, preset cases in `ToolPolicyEditor.test.tsx` | Deleted with the feature (T-Q6 A) | the deletion is the expected change, not a weakened test |
| Always Allow on approval cards (bash exact/prefix scope, grant-recorded toasts) | the R6 "Always Allow" cases in `src/components/agents/ToolApprovalModal.test.tsx` and `src/components/agents/ToolApprovalPanel.l1.test.tsx` | Yes, rewritten to C-26: each case becomes its timed-choice equivalent (`allow_for` with the chosen duration; a narrow bash card keeps its exact or prefix scope; the toast names the recorded timed allowance). A case with no C-26 equivalent is deleted, citing R2-Q1 | R2-Q1 removed Always Allow from every card; the rewrite is the expected change, not a weakened test (team-lead ruling 2026-11-22, filling a gap in this table) |

#### Regression Dataset: tool behaviour unchanged by this package

| # | Input | Previous Behaviour | Must Still Produce | Traces to |
|---|---|---|---|---|
| 1 | global `bash` ask, agent none | ask | ask | Regression: tool merge |
| 2 | global allow, agent deny | deny | deny | Regression: tool merge |
| 3 | God Mode, `set_channel_outbound` | ask | ask | Regression: always-ask |
| 4 | Omnipus per-agent `bash: ask` | 422 | 422 | Regression: Omnipus guard |
| 5 | MCP tool with `mcp_github_*: deny` | deny | deny | Regression: wildcard |
| 6 | ungranted skill load | refused | refused | Regression: grant gate |
| 7 | `PUT /security/tool-policies` full map | replaces map | 404 (retired; the page writes per key through `PATCH`) | Regression: legacy endpoint retired (C-22) |
| 8 | ungranted skill folder read by a file tool | refused | refused | Regression: skill authority |
| 9 | agent with `edit_skill` allowed reads any skill folder | allowed | allowed (editor exception) | Regression: editor read |
| 10 | `PUT /agents/{id}` with `tools_cfg.mcp` only | builtin policies kept | builtin and skill policies kept | Regression: update preservation |

---

#### Dataset D17: Timed allowance subjects and lifetime (C-26)

Oracle: C-26 "Which card offers what" and "Lifetime and scope" (narrow cards: today's grant subject; ordinary card: the whole tool; ended by a permission change). Every row: Mia, the allowance answered with "Allow for 1 hour" in chat 1, the next call in chat 2 within the hour unless stated.

| # | Input (card answered; next call) | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | shell card for `npm run test`, scope exact; `npm run test` | exact command | runs, no card | BDD Scenario: S-121 | |
| 2 | shell card for `npm run test`, scope prefix; `npm run test -- --watch` | command family | runs, no card | BDD Scenario: S-121 | `ShellPrefixGrant` |
| 3 | as row 2; `npm run testfoo` | token boundary | card | BDD Scenario: S-121 | |
| 4 | file-access card for writing `/home/p/Desktop/out.pdf`; `cp a.pdf /home/p/Desktop/out.pdf` | same path and access | runs, no card | BDD Scenario: S-121 | |
| 5 | as row 4; `cp a.pdf /home/p/Desktop/other.pdf` | other path | card | BDD Scenario: S-121 | |
| 6 | network card naming `example.com`; `curl https://example.com`, then `curl https://example.org` | named address | first runs; second asks | BDD Scenario: S-121 | |
| 7 | network card with no readable address (`npm install`); `npm install` | no address | runs; ports 80, 443 and 53 to any address for Mia until the end | BDD Scenario: S-121 | same as today; security review point 3 |
| 8 | any of rows 1, 4, 6; the same call by Jim | other agent | card | BDD Scenario: S-121 | |
| 9 | as row 6; the test clock passes the hour during a running `curl` loop that opens a new connection to `example.com` | end during a command | the new connection is refused; the command is not killed | BDD Scenario: S-121 | token revoked |
| 10 | `delete_task` card with arguments A; global `delete_task` then set to Deny; Mia calls A | Deny ends it | refused naming the global Deny; no card; 1 `approval.allowance_ended` | BDD Scenario: S-128 | |
| 11 | as row 10, then global set back to Ask within the hour; Mia calls A | policy lifted | card (the allowance ended at the Deny) | BDD Scenario: S-128 | team-lead 2026-11-22 |
| 12 | `delete_task` card A answered "Allow for 3 hours", then a second card answered "Allow for 1 hour" | two answers | end stays at 3 hours | BDD Scenario: S-121 | later end wins |
| 13 | `delete_task` card with arguments A; Mia calls `delete_task` with arguments B | ordinary card, other arguments | runs, no card | BDD Scenario: S-121 | founder Q4 B: whole tool |
| 14 | Mia and Jim each hold a `delete_task` allowance; global `delete_task` Ask → Allow by `PATCH /security/permissions` | global change | Mia's and Jim's allowances end (calls run anyway under Allow); 2 audit entries | BDD Scenario: S-128 | |
| 15 | as row 14, then global back to Ask | global change back to Ask | Mia and Jim get a card | BDD Scenario: S-128 | |
| 16 | Mia and Jim each hold a `delete_task` allowance; Mia's own value set to Ask by `PUT /agents/mia/tools` | one agent's change | Mia gets a card; Jim runs with no card; 1 audit entry | BDD Scenario: S-128 | |
| 17 | Mia holds a `weather` skill allowance; Omnipus sets Mia's `weather` override to Deny with `set_agent_permissions`, then clears it (Inherit) | agent writer, skill | the Deny ends it (refused); after clearing, a card | BDD Scenario: S-128 | |
| 18 | Mia holds a `delete_task` allowance; `PATCH /security/permissions` sends `delete_task: ask` (the stored value) | same value | 200, nothing ended, no audit entry; Mia runs with no card | BDD Scenario: S-128 | |
| 19 | Mia holds a `bash` address allowance for `example.com` and a command-family allowance; global `bash` Ask → Deny → Ask | bash change | both end; Mia's live egress token is revoked; next `curl https://example.com` asks | BDD Scenario: S-128 | FR-094 |
| 20 | Mia holds a `weather` skill allowance; trust changed Ask → Allow | not a permission | allowance kept | BDD Scenario: S-128 | C-26 |
| 21 | `environment_setup` card for `apt-get install -y poppler-utils`; `allow_for 1d`; then the same command again | exact-action | 409; card again showing the command | BDD Scenario: S-129 | founder Q5 A; ADR-090 |
| 22 | `request_mount` card for `/home/p/Projects`; `allow_for 1h`; then the same folder again | exact-action | 409; card again | BDD Scenario: S-129 | |
| 23 | `send_email` card to one recipient; `allow_for 3h`; then the same email again | exact-action | 409; card again | BDD Scenario: S-129 | |
| 24 | `browser_evaluate` card with one script; `allow_for 1h`; then the same script again | exact-action | 409; card again | BDD Scenario: S-129 | |
| 25 | `set_config` card for one key; `allow_for 1d`; then the same change again | exact-action | 409; card again | BDD Scenario: S-129 | |

#### Dataset D18: Full skill size and page warning (C-14 "Page warning"; ruling spec-gaps-1, 2026-11-25)

Fixed token estimator in the Go test; every expected number is literal, never computed by calling the new code.

| # | Input | Boundary Type | Expected Output | Traces to | Notes |
|---|---|---|---|---|---|
| 1 | skill `plan` at Full; estimator returns 3,240 for its block | normal | `full_tokens` 3,240; row shows "about 3,200 tokens" | BDD Scenario: S-130 | nearest 100 |
| 2 | `plan` at Name and short description; `full_tokens` 3,240 | not Full | no size on the row | BDD Scenario: S-130 | |
| 3 | `notes-helper` with `load_error` | unreadable | `full_tokens` absent; no size on the row | BDD Scenario: S-130 | |
| 4 | a Full skill with `full_tokens` 40 | tiny | row shows "under 100 tokens" | BDD Scenario: S-130 | |
| 5 | a Full skill with `full_tokens` 3,250 | rounding half | row shows "about 3,300 tokens" | BDD Scenario: S-130 | half rounds up |
| 6 | Mia granted `plan` (Full, Allow, verified, 4,001 tokens); window 8,000 | one over half | `full_skills_tokens` 4,001; callout names Mia | BDD Scenario: S-130 | |
| 7 | as row 6 with 4,000 tokens | exactly half | `full_skills_tokens` 4,000; no callout | BDD Scenario: S-130 | |
| 8 | Mia granted `plan` (Full, Allow, 3,000) and `deep-research` (Full, Ask, not approved, 3,000); window 8,000 | ask not counted | `full_skills_tokens` 3,000; no callout | BDD Scenario: S-130 | same outcome rule as D9 row 2 |
| 9 | Mia not granted `deep-research` (Full, Allow, 5,000); Mia's own value for `plan` Deny | not granted, denied | `full_skills_tokens` 0 | BDD Scenario: S-130 | D9 row 6 |
| 10 | Mia granted `plan` (Full, Allow, unverified, trust Ask, no approval) | trust ask | `full_skills_tokens` 0; after "Approve this version" 3,000 | BDD Scenario: S-130 | D9 rows 3–4 |
| 11 | `full_skills_tokens` 5,000; `context_window_effective` absent | unknown window | no callout | BDD Scenario: S-130 | |
| 12 | `full_skills_tokens` 5,000; `context_window_effective` 0 | exempt agent | no callout | BDD Scenario: S-130 | |
| 13 | `GET /agents` fails | load failure | no callout; rows and sizes normal; no error callout | BDD Scenario: S-130 | |
| 14 | Mia fits (3,000 of 8,000); the person sets a second 2,000-token skill to Full and it is saved; `GET /agents` re-read returns 5,000 | change on the page | callout names Mia after the re-read | BDD Scenario: S-130 | |

## Functional Requirements

### Page, search and saving (US-1, US-6)
- **FR-001**: The Skills & Tools page MUST show three tabs with counts — Skills, External tools (MCP), Omnipus tools — opening on Skills.
- **FR-002**: Each tab MUST show one search field and two flat filters (Permission; What the model sees) on one line, above one grouped list whose groups start collapsed; a search MUST open groups with matches; Escape and the clear button MUST empty it.
- **FR-003**: A group's Set all MUST change exactly the rows on screen (search and both filters applied) that can take the chosen value (C-20), in one request, with "<k> of <n>" in its label and accessible name, and MUST name the skipped rows and why.
- **FR-004**: An empty result MUST show "No <items> match “<text>”" with "Clear search and filters".
- **FR-005**: A row whose saved value no longer matches the active filter MUST leave the list.
- **FR-006**: With a "What the model sees" filter on, the page MUST show the denied-items note.
- **FR-007**: Every value on this page (permission, level, trust, a service's switch) MUST save on its own: "Saving…" within 100 ms of the click, the request within 1,000 ms when no earlier change is pending, and "Saved" only when that change is acknowledged (C-16); the page MUST NOT show a Save bar, Discard, a leave prompt or a Set all confirm, other than FR-010's warning (ST-Q7 A).
- **FR-008**: A refused save MUST show "Not saved: <server reason>." with Retry, and the rows MUST show the stored values.
- **FR-009**: A 503 save MUST show "Saved, but not applied to running agents yet. Retry applying the change, or restart Omnipus." with Retry.
- **FR-010**: Any change, single or Set all, that would make `execute_plan` resolve to Allow MUST open the existing autonomous-plan confirm first; Cancel changes no row of that action (ST-Q7 A).
- **FR-011**: Every tool row MUST show exactly one stored value; "Unset" MUST NOT appear.
- **FR-012**: Tool search and Skill MUST show no permission badges (C-6 `permission_fixed`), with their fixed texts; the server MUST refuse any value but `allow` for them.

### Per-skill permissions (US-2)
- **FR-013**: The server MUST store one global permission per skill key (C-19) and serve and change it through `GET/PATCH /security/permissions` (C-2), changing only the keys a request names.
- **FR-014**: A skill load MUST resolve to the strictest of the global skill value, the agent's own value and, for an unverified skill, the trust value; God Mode MUST floor only the global skill value (C-0, C-3; Dataset D1).
- **FR-015**: The grant check MUST run before the permission check and keep its current refusal.
- **FR-016**: An ordinary Ask load in an interactive chain MUST follow the C-12 decision table: the permission card with the C-9 `skill` object unless Auto-approve, a running timed allowance for this agent or `/name` settles it; a time choice MUST cover that agent's loads of the skill per C-26.
- **FR-017**: A load in an unattended chain that still needs a person's answer (C-12: the permission check not settled by Auto-approve or a running timed allowance of that agent, or the trust check open) MUST be skipped with "Not run: needs approval, and task and plan runs never ask.", with no card and no wait.
- **FR-018**: A skill whose effective value is Deny for an agent MUST NOT appear in that agent's skill menu or `Skill` search results.
- **FR-019**: The first start on this release MUST run the C-19 upgrade: every installed and project skill's global value = the old global `Skill` value; each agent's own old `Skill` value of `ask` or `deny` translated to each skill granted to that agent and each project skill on its mounts (an old `allow` removed, not copied); the old `Skill` entries removed and `Skill` fixed at `allow`; every old value read only from the file that holds it, in the same write that replaces it (FR-084) (founder rule 2026-11-28).
- **FR-020**: A skill key with no stored global value MUST get Ask (ST-Q3 B), or Allow for a built-in skill shipped by Omnipus, and one with no stored level `name_and_description`, additively, never overwriting a stored value.
- **FR-021**: The server MUST store sparse per-agent skill overrides and accept them through `AgentToolsUpdateRequest.skills` (C-4), reporting `AgentToolsResponse.skills`.

### Agent Tools tab (US-3)
- **FR-022**: No agent's Tools tab and no other screen MUST show Cautious / Balanced / Full access; the presets module MUST be removed.
- **FR-023**: In an agent's Tools tab, badges looser than the global value MUST be disabled, with the tooltip "The global setting is Ask. Set it to Allow on Skills & Tools first." (or "The global setting is Deny. Change it on Skills & Tools first.") and a "Change the global setting" link that opens that row on Skills & Tools (R2-Q1 point 4).
- **FR-024**: An agent's Tools tab MUST show a Skills group of its granted skills with Allow / Ask / Deny and Inherit, or "No skills granted. Grant skills on this agent's Skills tab."
- **FR-025**: A per-agent Ask on a skill for Omnipus MUST be refused with the existing Omnipus text and code (422).
- **FR-026**: Judge and Plan Supervisor MUST stay read-only and external workers MUST keep their notice, for skills as for tools.

### Skill trust (US-4)
- **FR-027**: Trust MUST be stored as `allow` / `ask` / `deny` and served that way by `PermissionSettings.skill_trust` (C-2); legacy stored values MUST be rewritten once on load (Dataset D3) with one audit entry; `PATCH` MUST refuse other values with 400 "Use allow, ask or deny."; the legacy `/security/skill-trust` endpoint MUST be retired by this package (C-22).
- **FR-028**: The Skills tab MUST open with the "Unverified skills" block: Allow, Ask, Deny badges in that order, all three meanings visible, and the Allow warning callout fully visible after choosing Allow, also at 390 px.
- **FR-029**: With trust Deny, every install path (screen, `POST /skills/install`, `install_skill`) MUST refuse an unverified skill with the C-3 text and leave nothing installed; the install review MUST disable Install with its own text. With trust Ask, an unverified install MUST need a person's review of that exact revision (C-12, C-13); `POST /skills/install` without one → 409.
- **FR-030**: With trust Ask, an unverified skill's load MUST need a trust approval of its current revision per C-12, including under Auto-approve and God Mode and never satisfied by a timed allowance (ST-Q2 A); with trust Deny it MUST be refused with the C-3 load text.
- **FR-031**: `verified` MUST be true only for built-in skills and for registry installs whose hash matched and whose folder has no local changes since (C-5); the install result MUST be stored outside the skill folder.
- **FR-032**: Every trust change MUST keep its existing audit entry with old and new value.

### What the model sees (US-5)
- **FR-033**: Every tool and skill row MUST offer Hidden, Name only, Name and short description, Full, with a one-line explanation per type (C-1).
- **FR-034**: At the start of a turn, each allowed tool MUST be presented per its level (C-1; Dataset D2).
- **FR-035**: Tool search, Skill, Ask the user, goal_claim and set_goal MUST have fixed levels with the visible lock reason; the server MUST refuse a change (400).
- **FR-036**: An item with effective Deny for an agent MUST NOT be listed or found for that agent, whatever its level.
- **FR-037**: The first start on this release MUST set starting levels per C-6 and Dataset D7, including per-server levels for external services, so that what each agent is sent is unchanged apart from SC-003's named changes, including ToolSearch for installs that had the switch off (C-6, D7 row 16); the same `config.json` write MUST remove `tools.manifest.compressed`, `preview_all_lazy` and every `MCPServerConfig.deferred`, and nothing MUST read them afterwards (ST-Q4 B; founder rule 2026-11-28); a tool or server with no stored level MUST get its fresh-install starting level (Dataset D7 row 18).
- **FR-038**: Each granted, not-denied skill MUST be presented per its level (C-1); a Full skill's instructions MUST be injected only when its outcome is `allowed` for that agent and chain, and MUST be charged to the request budget, with the C-14 refusal when they do not fit (ST-Q1 A).
- **FR-039**: The four levels MUST be the only control of what is sent about tools (ST-Q4 B): the "Load tools on demand" switch MUST be gone from Settings → Limits at this landing, on the Settings base (C-22 rows 2–4), `tools_on_demand` MUST be gone from the Performance contract, and `set_config` and `PUT /config` MUST refuse a change to the retired manifest keys (C-21, C-22).
- **FR-040**: Every change of a tool permission, a skill permission, a tool level, a skill level or trust — through `PATCH /security/permissions` (the only writer; the legacy PUT endpoints are retired, C-22) — MUST write one audit entry per changed key with actor, old and new value, and none for unchanged keys or refused requests (C-2).

### Adding skills (US-7)
- **FR-041**: New skill MUST offer "Write with Omnipus", "Upload a skill", "Browse the catalog"; "Write with Omnipus" MUST open the Omnipus chat and send "Write a new skill" with `workflow_start {workflow: W5}`; offline it MUST send nothing and show the C-8-pattern warning.
- **FR-042**: Every install (catalog and file) MUST pass a review of the staged files (C-13) showing name, version, verified or not, the trust value, and a required Category; `metadata.category` MUST preselect when it is a valid value; Install MUST publish exactly the reviewed revision.
- **FR-043**: Each skill's category MUST be stored outside its folder (C-5) and the Skills tab MUST group by category, hiding empty ones, with Uncategorised for the rest.
- **FR-044**: Each skill row MUST offer More actions: Make a copy; Remove only when not built-in; "Choose category" only while Uncategorised; "Approve this version" only for an unverified skill under trust Ask.

### External services (US-8)
- **FR-045**: Adding a service (screen or `add_mcp_server`) MUST store Ask for all its current and future tools when no global key covers it (C-7); the one-time upgrade MUST follow C-18 exactly, writing only global server keys (ST-Q5 A).
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
- **FR-067**: Consent MUST follow the C-12 decision table: the permission check is settled by Auto-approve where active, a running timed allowance for this agent (C-26), `/name` or Approve Once; the trust check only by a person's approval of that revision; one card per moment; every answer MUST be re-checked against the current facts before anything is stored (C-24).
- **FR-068**: Installs MUST go through the C-13 review: stage without publishing, return verified facts and the revision, publish exactly that revision, expire after 15 minutes, keep one terminal state per review readable for 15 minutes, answer repeated submits idempotently, and report Cancel and lost answers from that state, never from the skills list (FR-087).
- **FR-069**: When an agent's Full skills do not fit its model, the turn MUST NOT be sent and the chat MUST show the C-14 text; nothing is trimmed silently (ST-Q1 A).
- **FR-070**: A level change MUST apply from the next turn without evicting loaded definitions; a permission Deny MUST remove the item from the next turn; manifest, search membership and skill menu MUST read one visibility authority, and the skill menu MUST rebuild when an outcome or level changes (C-15).
- **FR-071**: The page MUST implement the C-16 save states: one request at a time in click order, never overlapping or merged, an app-level queue that survives in-app navigation, a re-read for refused, not-stored and unknown outcomes, a check-failed state, and a late toast worded by outcome class.
- **FR-072**: `PATCH /security/permissions` MUST change only named keys, validate the whole request first, tell an empty request (400) from a same-value request (200, nothing written, no audit, agents reloaded), return 500 only when nothing was stored, and follow every rule of C-2.
- **FR-073**: ToolSearch MUST also return the skills the agent may use, labelled `[skill]`, without loading them (C-17) (ST-Q6 A).
- **FR-074**: The external-tool migration MUST follow C-18 and Dataset D8, decide coverage with the resolver's own matching, write only global server keys, and run once.
- **FR-075**: Project skills MUST have their own keys and lifecycle (C-19, C-23); the upgrade MUST follow the C-19 steps (FR-084).
- **FR-076**: Bulk changes MUST follow C-20: only rows that can take the value change; the label counts them; skipped rows are named.
- **FR-077**: Every write path of C-21 MUST carry the agent's skill permissions and the skill category: `create_agent` `skill_policy_changes`; preservation in `updateToolsConfig`; `edit_skill` `set_category` and copy `category`; `PUT /skills/{name}/category` once; the write-skill workflow sets the category; every agent writer validates skill keys with C-23 (FR-088).
- **FR-078**: At this landing the C-22 rows MUST be done together: no commit leaves `tools.manifest.compressed` unguarded, no retired endpoint keeps a caller, and links use the post-Settings section ids.
- **FR-079**: Each Full skill row MUST show its approximate size from `Skill.full_tokens`, and the Skills tab MUST warn when an agent's `full_skills_tokens` exceeds half of its `context_window_effective`; the server MUST compute both numbers with the turn's estimator and the turn's Full-skill selection (C-14 "Page warning"; ST-Q1 A; ruling spec-gaps-1).
- **FR-081**: The person MUST see at most one card per moment (C-12 decision table): one combined card when the permission and trust checks are both open, one install card when `install_skill` needs approval and the staged skill needs trust, and no card at a screen install whose review answered trust.
- **FR-082**: A project-folder skill MUST count as unverified, so trust applies to each new version, also under God Mode (ST-Q8 A).
- **FR-083**: Unattended runs MUST skip a load that still needs a person's answer, with the unattended text, and MUST NOT wait; a running timed allowance of that agent MUST settle the permission part there; no card action MUST change a stored permission (C-12, C-26, ST-Q9 A as narrowed by R2-Q1).
- **FR-084** (rewritten, founder rule 2026-11-28; revised after review NM-01, NM-02, NM-04): The upgrade MUST decide whether to convert a file only from the old-shape values it holds (C-19 trigger table), with no done flag, marker, snapshot, ledger, upgrade lock or progress record; MUST convert each file in one atomic write that adds the new values and removes the old ones; MUST read every file it writes inside that file's ordinary writer lock, which every writer of the file takes (C-19 one writer rule: `config.UpdateConfigFile`, `agentstore.Store.UpdateRaw`/`Update`), so no write is computed from a stale read; MUST leave a file wholly unconverted while the skill inventory its conversion needs is incomplete, and MUST apply its kept old `Skill` value (Allow, Ask or Deny) to every skill without its own value until it is converted; MUST take the in-process and file writer locks inside the one entry for every writer, in the lock order of C-19; MUST write only each writer's own named operation on the document read inside the lock, system-agent writers included (C-19, revision 8), never a comparison of two structs; MUST refuse an ordinary save that would remove the last old value of a deferred file, with 409 `skills_not_listable` and nothing written — the only exception to ordinary saves applying their own change to a deferred file (founder answer Q-UPG1 A; C-19); MUST tie "not saved" to the commit point; MUST serve every reader, including `GET/PATCH /security/permissions`, from the converted view; MUST convert every agent record before the agent loop starts and `config.json` inside the config load before decoding; MUST add a new key only when absent (a person's value is kept); MUST, when a file cannot be written, log an Error naming it at each start, read it through the same conversion so its old limits apply, and retry at the next start; MUST leave nothing on disk that exists only because of the upgrade (SC-010); and MUST write per-agent values only as the translation of an existing explicit `Skill` value of `ask` or `deny` (C-19).
- **FR-085**: A project-skill folder under any mount the turn can reach MUST be readable by file tools, search and listings, and runnable as a helper, only when its outcome is `allowed`, ahead of every mount, work-dir, path-grant and God Mode allowance, with path identity per C-25; where a kernel sandbox enforces, the gate MUST be rendered into it where the platform can express it, and the platforms where it is app-level only MUST be logged and documented (C-25).
- **FR-086**: Skill approvals MUST use the C-24 contract: server-computed offered actions, an immutable binding to skill key and revision, a re-check before any effect, the stated order of effects, an `applied` result that reports only what was stored, no second effect on replay, and audit entries for trust approvals and timed allowances.
- **FR-087**: `GET /skills/install/review/{review_id}` MUST report the review's state bound to its revision (C-13); `POST /skills/install` with a `review_id` MUST be idempotent.
- **FR-088**: One known-skill-key rule (C-23) MUST decide installed and project keys on the global PATCH and on every agent writer, with the same refusals, the same empty, clear, remove and null semantics, and read-back of project keys.

### Timed allowances (R2-Q1, founder 2026-11-22)
- **FR-089**: No approval card MUST offer "Always Allow", "Always allow" or "Allow everywhere"; every card that offered Always Allow MUST offer Approve Once, Allow for 1 hour / 3 hours / 1 day, and Deny (and Cancel where it had it); always-ask tools, the five exact-action tools, trust cards and the unverified-install card MUST offer no time choice (C-26).
- **FR-090**: A timed allowance MUST be keyed by the agent named on the card and a subject: the whole tool for an ordinary tool card (founder Q4 B); today's grant subject for the narrow cards (exact command or command family, path with its access, named addresses or the no-address ports); a skill's permission part for a skill card; and MUST cover every session of that agent and the helpers it starts for itself, and no other agent (C-26).
- **FR-091**: A timed allowance MUST end 1 hour, 3 hours or 24 hours after it was recorded, at a gateway restart, when its agent is deleted, or when the permission it answers changes (FR-096); it MUST never be written to disk; a later answer for the same subject MUST keep the later end time (C-26).
- **FR-092**: A Deny anywhere MUST refuse before any allowance is consulted; Auto-approve and running allowances MUST be consulted before the unattended refusal; unattended runs MUST honour a running allowance and MUST NOT ask or wait (C-12, C-26).
- **FR-093**: The approval contract MUST carry server-computed `offered_actions` and `allow_durations` on `tool_approval_required` and on pending-approval state, MUST accept `allow_for` and `approve_version_allow_for` only with a `duration` of `1h`, `3h` or `1d`, MUST refuse the retired `allow` with 400, and MUST return `expires_at` for a recorded allowance (C-26).
- **FR-094**: When an allowance ends, the system MUST revoke the agent's live egress-proxy tokens so that no new connection reaches an address no longer allowed; a call or command already released MUST keep running (C-26).
- **FR-095**: Every recorded allowance MUST be audited with agent, subject, duration, end time and actor; `shell.grant_recorded` MUST carry `duration` and `expires_at` (C-26).
- **FR-096**: Every write that changes a tool's or a skill's stored permission, globally or for one agent (including back to Ask and clearing an override), MUST end that tool's or skill's running allowances for every agent the change affects before its response, MUST write one `approval.allowance_ended` audit entry per ended allowance, and MUST end nothing on a same-value write or on a trust, level, Auto-approve or God Mode change (C-26).
- **FR-097**: Cards for `environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config` MUST offer Approve Once and Deny (and Cancel where they have it today) with no time choice; no allowance MUST ever cover these tools; the server MUST refuse `allow_for` for them with 409 through the offered-actions rule (founder Q5 A; C-24, C-26 point 9).
- **FR-080**: `GET /skills` and agent turns MUST create missing project-skill keys at first sight and remove them with their workspace or folder (C-19).

### Dependency
- **FR-065**: This package MUST land after the Settings package, rebased on its landed commit (issue #1963); at its landing the route MUST render the tool permission grid, web search settings and skill trust, and every row of the C-22 table MUST be done in the same landing. The release MUST NOT be cut with Settings landed and this package not landed.

## Success Criteria

- **SC-001**: All 127 BDD scenarios pass in RED → GREEN, with the RED logs saved.
- **SC-002**: Hands-on rows ST-01 to ST-33 PASS on the cut commit, validated independently (ST-28 as a per-platform record).
- **SC-003**: After upgrading a saved pre-release data folder, the request each of 3 sample agents sends on its first turn, captured with a scripted model, is identical tool by tool and skill by skill to the request captured before the upgrade (ST-06), except these named changes: trust Warn becomes Ask (unverified skills now ask; C-3); external tools with no global coverage go from Deny or agent-only Allow to Ask (C-18) (ST-Q5 A); project skills now count as unverified (C-19, ST-Q8 A); skills added after the upgrade start at Ask (ST-Q3 B); ToolSearch is offered to installs that had "Load tools on demand" off (C-6). For an agent with a goal, both the goal-narrowed first request and the next normal request are compared. The founder rule of 2026-11-28 (C-19: one write per file, old keys removed, an old per-agent `Skill: allow` not copied) adds no named change: the captured requests stay identical.
- **SC-004**: `grep -r "Cautious\|toolPolicyPresets" src/` returns 0 matches in production code.
- **SC-005**: Every install path refuses an unverified skill under trust Deny: screen, agent tool and direct REST, each driven and asserted separately (3 of 3, ST-09 a–c); a direct REST install under trust Ask without a review is refused (ST-09 d).
- **SC-006**: With a skill Denied (or Ask not approved) for an agent, 0 of 5 paths give that agent its instructions or files, for installed skills and for project skills under a mount: `Skill`, menu/search, Full, file read, helper run (ST-13, ST-19, ST-28).
- **SC-007**: Two tabs changing different keys keep both changes in 10 of 10 repetitions (ST-20).
- **SC-008** (rewritten, founder rule 2026-11-28; row-specific after review NM-06): For each of the 22 rows of Dataset D13, the stored files, the effective values, the diagnostics and the result after the repair match that row's columns; no old Deny or Ask is ever lost or applied less strictly (rows 9, 10, 14–16, 19–22: applied from the converted view, with a kept old `Skill` value as the value of every skill without its own, while the file cannot be written or its conversion is deferred); and once every file of the row is converted, no old-shape value is left and a further start makes no conversion or repair write (ST-27).
- **SC-010** (new, founder rule 2026-11-28; comparison classes after review NM-06): After upgrading each of the two saved pre-release data folders, 0 files and 0 keys exist only because of the upgrade: every product file (excluding user content — agents, skills, workspaces, sessions, logs — and the generic artifacts every install can have: an interrupted atomic write's `.tmp-*` and the writer-lock files `config.json.lock`, `entities/agents/<id>.lock`) and every key path of `config.json` and the agent records also exists in a fresh install of the same build once the carried values are set there by hand in the order of S-133; none of `entities/.skill-permissions-upgrade*`, `sandbox.migrations`, `tools.manifest.compressed`, `tools.manifest.preview_all_lazy`, a server's `deferred`, an old trust word or a per-agent `Skill` value is present (S-133, ST-27, test 68).
- **SC-009**: 0 approval cards offer "Always Allow" or "Allow everywhere" (repository guard of test 62 and ST-29); a timed allowance covers 0 other agents and survives 0 restarts (S-120, S-122, ST-29, ST-30).

---

## Reachability

- **Tool registration**: no new tool. Changed tools: `set_agent_permissions` (`skill_policy_changes`), `create_agent` (`skill_policy_changes`), `get_agent` (`skill_permissions`), `install_skill` (`category`, stage-then-publish, trust card), `edit_skill` (`copy` `category`, `set_category`), `add_mcp_server` (Ask key only when uncovered), `set_config` (refusals), `ToolSearch` (finds skills (ST-Q6 A)). Their policy entries already exist in `pkg/config/defaults.go` and the agent seeds (`pkg/coreagent/seed.go`); `grep -rl '"set_agent_permissions"' pkg/coreagent/ pkg/config/ pkg/tools/` must stay non-zero. The `Skill` tool's global value becomes fixed `allow`; per-skill values live in `sandbox.skill_policies` (global) and each agent's `tools_cfg.skills.policies` (per agent).
- **Screen**: `/skills` route (`src/routes/_app/skills.tsx`), sidebar "Skills & Tools" — the rewritten Skills & Tools screen, writing through `GET/PATCH /security/permissions`; the install review (`/skills/install/review`, with its status read after a lost answer); the approval card's skill actions and the C-26 time choices on every approval card in every chat (`ToolApprovalPanel.tsx`, `ToolApprovalModal.tsx`) (C-24, C-26); More actions → "Approve this version" (C-24); each agent's profile → Tools tab (`ToolsAndPermissions`); the Omnipus chat for W5 through to a published, categorised skill; the save-failure toast from any screen.
- **Test plan execution**: RED (qa-lead) writes and runs the tests in "Test Implementation Order" against the pre-change code and saves the failing logs; GREEN (backend-lead and frontend-lead) makes them pass; CHECK audits integrity; the hands-on rows run on the cut commit by uat-tester and uat-validator.

## User-facing documentation

| TODO | UI / behaviour change | User-facing page | Matching update |
|---|---|---|---|
| DOC-001 | Global tool permissions move to Skills & Tools; autosave; Set all; no presets; no Unset | `docs/tools.md` | Rewrite "How to change an agent's tool access" (drop preset step 3), the "installation-wide setting" paragraph (now Skills & Tools → Omnipus tools / External tools), "Tools from connected servers" (new tab names, Add service, tools start at Ask) |
| DOC-002 | Per-skill Allow / Ask / Deny; Skill tool no longer one switch; grants stay | `docs/skills.md` | New section "Allow, ask or deny a skill"; table row for `Skill` in `docs/tools.md` defaults updated |
| DOC-003 | What the model sees: four levels | `docs/tools.md`, `docs/skills.md` | New section on the levels, what each means for a tool and a skill, locked tools, and that the levels replace "Load tools on demand" (ST-Q4 B) |
| DOC-004 | Skill trust words and enforcement; Warn becomes Ask; upgrade changes | `docs/skills.md` (line with Block/Warn/Allow all), `docs/security.md` | New words; where it applies (install and load, every path of C-11); approving a version; unattended runs refuse; the named upgrade changes of SC-003; project skills count as unverified; a later grant no longer inherits the old blanket `Skill` value; audit-write failures of ordinary setting writes are logged, not blocking (unchanged); the one-time trust-word conversion writes its audit entry only when the gateway's own start converts the word, so there is no entry when another command (for example `omnipus doctor`) converted it first or when Omnipus stopped between the conversion and the entry — in both cases the new trust value is the exact translation of the old word; these two cases are accepted (founder answer Q-AUD1 A) and every ordinary setting change is audited as before (C-3, review NM-05); the upgrade (founder rule 2026-11-28): it converts the old settings once and leaves the data folder like a fresh install — the "Load tools on demand" setting and the old trust words are gone from `config.json`, and no upgrade file or marker remains; if Omnipus cannot write a settings file (for example its folder is not writable or the disk is full), it logs an error naming the file at each start, keeps and shows that file's old skill limits, reports any save in that state as not saved, and finishes when the file can be written; if a mount or the skills folder cannot be read at start, the old blanket skill permission stays in force for every skill without its own value, an error names the cause, and the conversion finishes at the next start that can read every skill; edit `config.json` or an agent file by hand only while every Omnipus process is stopped; if a person changes the last old setting while some skills cannot be read, Omnipus does not save that change and says why; it can be saved once the skills can be read (founder answer Q-UPG1 A); no screen shows an upgrade notice any more; an empty trust value, which used to mean Warn, now means Ask; project-skill folders are closed to agents until their skill is allowed, and on which platforms that is app-level only (C-25) |
| DOC-013 | New skills start at Ask; one card per moment; approving from a chat | `docs/skills.md` | A new skill is set to Ask: an agent needs a yes unless Auto-approve is on (R2-Q2 A); the install review answers the unverified check; a time choice on a skill card lets that agent use the skill in all its chats, tasks and own helpers until the time ends or Omnipus restarts; to make it work for every agent for good, set it to Allow on Skills & Tools; unattended runs skip a skill that still needs an answer and never wait |
| DOC-014 | "Load tools on demand" is gone | `docs/settings.md`, `docs/tools.md` | The switch in Limits was replaced by levels; old installs were converted; installs that had it off now see ToolSearch offered to agents |
| DOC-010 | Editor exception; Full size limit; level changes in running chats | `docs/skills.md`, `docs/tools.md` | Agents allowed to edit skills can read denied skills' files; Full skills must fit the model; a level change applies from the next turn and does not unload loaded tools |
| DOC-011 | Install review and categories for written skills | `docs/skills.md` | Review step, expiry, "Choose category" once, write-skill asks for a category |
| DOC-005 | New skill menu; install review with category; categories | `docs/skills.md` "How to add a skill and grant it" | New steps and tab names |
| DOC-006 | Agent Tools tab: Skills group, stricter only, no presets; locked badge tooltip and link | `docs/agents.md`, `docs/tools.md` | Steps and screenshots text; the tooltip "The global setting is Ask. Set it to Allow on Skills & Tools first." and the "Change the global setting" link |
| DOC-007 | Agent tools gain skill fields | `docs/tools.md` (get_agent paragraph), `docs/skills.md` | `skill_permissions`, `skill_policy_changes`, `install_skill` category |
| DOC-008 | Web & Search settings live on Skills & Tools | `docs/tools.md` ("Web search works without setup" bullet) | Point to Omnipus tools → Web & Search → Settings |
| DOC-012 | One search finds tools and skills (ST-Q6 A) | `docs/tools.md` (ToolSearch paragraph) | ToolSearch also lists skills, typed, and does not load them |
| DOC-009 | Settings no longer holds tool permissions, trust or web search | `docs/settings.md` | Web search leaves Providers; the Protection notice; coordinated with the Settings package DOC-007; this package owns the Skills & Tools side |
| ADR-TODO-1 | Full skill level reverses ADR-072 OQ3 | `docs/internal/architecture/ADR-072-skill-activation-and-loading.md` | Dated correction by the architect after ST-Q1 is answered |
| ADR-TODO-2 | Tier lists become stored levels | `docs/internal/architecture/ADR-071-tool-manifest-tier-redesign.md` | Dated correction |
| DOC-015 | No Always Allow; time choices per agent (C-26) | `docs/tools.md`: the Allow / ask / deny table ("What you see" for Ask), the paragraph on Approve Once and Always Allow, the `set_channel_outbound` bullet ("There is no **Always Allow**"), the task-and-plan bullet ("an \"Always Allow\" grant") | The card offers Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny; a time choice on an ordinary tool card allows the whole tool for that agent (shell cards stay narrow) in all its chats, tasks and own helpers until it ends, Omnipus restarts or that tool's permission changes, other agents still ask, nothing is saved; a lasting Allow is set on Skills & Tools (and an agent can only be stricter); `set_channel_outbound` offers no time choice; `environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config` offer Approve Once and Deny only, so every action shows its card (the `environment_setup` paragraph in `docs/tools.md` keeps "the exact command is shown"); unattended runs honour a running allowance |
| DOC-016 | same | `docs/security.md`: the shell file-prompt paragraph (Always Allow opens one path for the chat), the network-prompt and proxy paragraphs ("for the rest of that chat", "remembered for the rest of the chat only"), "Task, plan, scheduled and other unattended runs" ("an existing \"Always Allow\" grant"), "Approve a request" (button sentence, the **Always Allow** table row, the reload sentence "or always allow", the group-chat sentence "**Always Allow** still lasts for the agent's own chat", the paragraph "What **Always Allow** remembers … Agents that this chat hands work to inherit it"), and the shell sections on Always Allow's exact / prefix choice | Rewrite each promise for timed, per-agent allowances: helpers of the same agent are covered, a delegation to another agent asks; ends at its time, at a restart or when that tool's or skill's permission changes (audited); never written to `config.json`; the no-address network choice opens the usual ports for that agent until it ends |
| DOC-017 | same | `docs/tasks.md` (task-run paragraph naming an "Always Allow" grant), `docs/using-omnipus-ui.md` (chat paragraph: "approve it once, deny it, or always allow it") | "a running time allowance"; the new button names |
| ADR-TODO-3 | Grants become timed and per agent; no copy to other agents (R2-Q1) | `docs/internal/architecture/ADR-092-shell-permission-modes.md` (D4, D7, D8 grant lifetime "ends with the chat"), the ADR-057 delegation decision (FR-031, grant inheritance) | Dated corrections by the architect |

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | Documentation TODO(s) |
|---|---|---|---|---|
| FR-001 | US-6 | S-45 | SkillsToolsPage.search.test | DOC-001 |
| FR-002 | US-6 | S-46, S-47 | SkillsToolsPage.search.test | DOC-001 |
| FR-003 | US-1, US-6 | S-02, S-06, S-48 | SkillsToolsPage.setAll.test | DOC-001 |
| FR-004 | US-6 | S-49 | SkillsToolsPage.search.test | DOC-001 |
| FR-005 | US-6 | S-50 | SkillsToolsPage.search.test | DOC-001 |
| FR-006 | US-6 | S-51 | SkillsToolsPage.levels.test | DOC-003 |
| FR-007 | US-1 | S-01, S-02, S-69, S-117 | SkillsToolsPage.autosave.test; SaveQueue.states.test | DOC-001 |
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
| FR-019 | US-2 | S-16, S-110 | TestSkillPolicyUpgrade_FromSkillTool; TestSkillPermissionsUpgrade_StopAtEveryStep | DOC-002 |
| FR-020 | US-2 | S-17, S-108 | TestSkillPolicyReconcile_NewSkillAsk | DOC-002 |
| FR-021 | US-3 | S-20, S-21, S-25 | TestAgentToolsPut_SkillsPolicies | DOC-006 |
| FR-022 | US-3 | S-18 | ToolPolicyEditor.noPresets.test | DOC-001, DOC-006 |
| FR-023 | US-3 | S-19, S-127 | ToolPolicyEditor.locks.test; ToolPolicyEditor.lockTooltip.test | DOC-006 |
| FR-024 | US-3 | S-20, S-21, S-24 | ToolPolicyEditor.locks.test; skills-tools.spec.ts | DOC-006 |
| FR-025 | US-3 | S-22 | TestAgentToolsPut_SkillsPolicies | DOC-006 |
| FR-026 | US-3 | S-23 | TestAgentToolsPut_SkillsPolicies | DOC-006 |
| FR-027 | US-4 | S-33, S-36, S-101 | TestSkillTrustMigration_LegacyValues; TestUpgrade_TrustAuditEntry; TestPermissionsPatch_RefusesLegacyTrustValues; TestLegacySecurityEndpoints_Retired | DOC-004 |
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
| FR-050 | US-10 | S-66, S-67, S-132 | SkillsToolsPage.states.test | DOC-001 |
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
| FR-065 | — | S-64, S-26, S-01, S-101 | cut checklist; TestLegacySecurityEndpoints_Retired | DOC-009 |
| FR-066 | US-2 | S-85, S-86, S-87 | TestSkillAuthority_FoldersFollowOutcome | DOC-010 |
| FR-067 | US-2, US-4 | S-10, S-11, S-30, S-31, S-93, S-112 | TestSkillConsent_Table; TestSkillApprovalActions_Handler | DOC-004 |
| FR-068 | US-7 | S-94, S-95, S-115 | TestInstallReview_StageBindsRevision; TestInstallReview_OutcomeStatus | DOC-011 |
| FR-069 | US-5 | S-89, S-90 | TestFullSkills_SentinelAndBudget | DOC-010 |
| FR-070 | US-5 | S-91, S-92 | TestVisibilityTransitions | DOC-010 |
| FR-071 | US-1 | S-83, S-117 | SaveQueue.states.test | DOC-001 |
| FR-072 | US-1 | S-82, S-99 | TestPermissionsPatch_PerKey | none — internal API; the screen behaviour is covered by DOC-001 |
| FR-073 | US-5 | S-96 | TestToolSearch_FindsSkills | DOC-012 |
| FR-074 | US-8 | S-88 | TestMcpMigration_CoverageMatrix | DOC-004 |
| FR-075 | US-2 | S-102, S-16, S-110 | TestProjectSkillKeys; TestSkillPolicyUpgrade_FromSkillTool; TestSkillPermissionsUpgrade_StopAtEveryStep | DOC-004 |
| FR-076 | US-1, US-6 | S-84 | SkillsToolsPage.setAll.test | DOC-001 |
| FR-077 | US-7, US-11 | S-97, S-98, S-103, S-104 | TestUpdateAgentToolsCfg_PreservesSkillPolicies; TestCreateAgent_SkillPolicyChangesParity; TestWriteSkillWorkflow_PublishesCategorisedSkill; TestEditSkill_SetCategoryOnce | DOC-007, DOC-011 |
| FR-078 | US-4, US-9 | S-101, S-65, S-44 | TestLegacySecurityEndpoints_Retired; TestSetConfig_RefusesRetiredManifestKeys; skills-tools.spec.ts | DOC-009 |
| FR-079 | US-5 | S-130 | TestFullSkills_PageSizeFields; SkillsToolsPage.fullSize.test | DOC-010 |
| FR-080 | US-2 | S-102 | TestProjectSkillKeys | DOC-004 |
| FR-081 | US-2, US-4 | S-106, S-107, S-118 | TestSkillConsent_Table; TestInstallSkill_OneCard | DOC-013 |
| FR-082 | US-4 | S-105 | TestSkillConsent_Table | DOC-004 |
| FR-083 | US-2 | S-109, S-113 | TestSkillConsent_Table; ApprovalCard.offeredActions.test; TestSkillApproval_TimedAllowanceReachesScheduledRun | DOC-013 |
| FR-084 | US-2 | S-110, S-133 (S-131 retired) | TestSkillPermissionsUpgrade_StopAtEveryStep; TestUpgrade_LeavesNoUpgradeState; TestUpgrade_DecidesFromDataOnly; TestUpgrade_UnwritableFileKeepsOldLimits; TestAgentWriters_RefuseSkillToolValue; TestUpgrade_DelayedWriterKeepsLaterChange; TestConfigWrite_CommitPoint; TestSelfHeal_AuxiliaryWorkAfterNoOp | DOC-004 |
| FR-085 | US-2 | S-111 | TestProjectSkillPathGate; TestProjectSkillPathGate_Kernel | DOC-004 |
| FR-086 | US-2 | S-112, S-113, S-114 | TestSkillApprovalActions_Handler; TestSkillApproval_TimedAllowanceReachesScheduledRun | DOC-013 |
| FR-087 | US-7 | S-115 | TestInstallReview_OutcomeStatus; InstallReview.lostAnswer.test | DOC-011 |
| FR-088 | US-11 | S-116, S-102 | TestSkillKeyValidation_AllWriters | DOC-007 |
| FR-089 | US-13 | S-10, S-106, S-119, S-125, S-129 | TestToolApprovals_TimedActions; ApprovalCard.timeChoices.test | DOC-015, DOC-016, DOC-017 |
| FR-090 | US-13, US-2 | S-11, S-120, S-121, S-128 | TestApprovalGrantStore_AgentScopedTimed; TestTimedAllowance_HelpersAndOtherAgents; TestShellTimedAllowances_NarrowSubjects | DOC-015, DOC-016 |
| FR-091 | US-13 | S-11, S-113, S-122 | TestApprovalGrantStore_AgentScopedTimed | DOC-015, DOC-016 |
| FR-092 | US-13, US-2 | S-109, S-123 | TestTimedAllowance_Unattended; TestSkillConsent_Table | DOC-015, DOC-016, DOC-017 |
| FR-093 | US-13 | S-119, S-124, S-126 | TestToolApprovals_TimedActions; ApprovalCard.timeChoices.test | DOC-016 |
| FR-094 | US-13 | S-121 | TestShellTimedAllowances_NarrowSubjects | DOC-016 |
| FR-095 | US-13 | S-113, S-121 | TestToolApprovals_TimedActions; TestShellTimedAllowances_NarrowSubjects | DOC-016 |
| FR-096 | US-13 | S-128 | TestTimedAllowance_EndsOnPermissionChange | DOC-015, DOC-016 |
| FR-097 | US-13 | S-129 | TestToolApprovals_ExactActionToolsNoTimeChoice; ApprovalCard.timeChoices.test | DOC-015, DOC-016 |

**Completeness check** (round 1, the founder-answer passes and round 2: re-run over S-01 to S-118 and FR-001 to FR-088): every FR appears with at least one scenario and one test; scenarios S-01 to S-118 each appear at least once (S-03 in FR-001's page test set via skills-tools.spec.ts row 31; S-20, S-57, S-58, S-60 to S-65, S-68, S-69 through FR-024 and FR-043 to FR-052).

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
- ToolSearch also finds skills (C-17) (ST-Q6 A); round 0 had left R31's "one search for both" out without a founder decision (round-1 MAJ-015).
- Skill categories for built-ins (decided: founder default, former FQ-5), proposal from R32 Appendix B mapped to today's 29 skills: Tools — add-connector, add-provider, capability-plan, change-agent, connect-channel, create-agent, dependency-check, disable-channel, doctor-usage, inbox-triage, install-skill, provider-config, remove-agent, remove-connector, shell-diagnosis, team-delegation, team-setup, welcome-workspace, write-skill; Business — interview, define-goal, plan, orchestrate; Research — deep-research; Testing & Security — verify; Content & Media — elicify-docx, elicify-pdf, elicify-pptx, elicify-xlsx.
- Project-folder (mount) skills are not listed on the Skills tab (#1380 not a rider by default); they have their own keys, migration and defaults (C-19).
- Settings lands first (issue #1963); this package edits named Settings files on the Settings base (C-22) and lists the Settings-spec edits for team-lead to make ("Notes for team-lead").
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
- Founder answers to the round-1 questions, 2026-11-21, source: founder on Discord #team-b at 05:56Z, verbatim "ST-Q1 A, ST-Q2 A, ST-Q3 B, ST-Q4 B, ST-Q5 A, ST-Q6 A, ST-Q7 A", relayed by team-lead (questions: `/workspace/logs/skills-tools-1519f/founder-questions-r1.md`):
  - Q: What does "Full" mean for a skill (ST-Q1)? → A: whole instructions in each new chat, only after the permission allows it; a visible cannot-fit outcome (C-14).
  - Q: Does the trust check still ask under God Mode, Auto-approve and Always Allow (ST-Q2)? → A: yes, a separate check per version; a reviewed install counts (C-12).
  - Q: What does a newly installed skill start at (ST-Q3)? → **B: Ask** (not the recommended A). Built-in skills shipped by Omnipus keep Allow as their shipped default (confirmed by ST-Q10 A). How it combines with ST-Q2: C-12 "When the two checks meet".
  - Q: Which control decides what the model sees about tools (ST-Q4)? → B: the four levels only; "Load tools on demand" leaves Settings in the same release; existing installs are converted, not reset (C-6, C-22).
  - Q: External tools that never had a permission (ST-Q5)? → A: Ask for genuinely uncovered tools; covered tools unchanged (C-18).
  - Q: One search for tools and skills (ST-Q6)? → A: included in this package (C-17).
  - Q: Does "no confirmation" remove the plan-run warning (ST-Q7)? → A: keep this one warning only (FR-010).
- Founder answers to the second questions, 2026-11-21, source: founder, Discord #team-b, 13:19Z, verbatim "ST-Q8 A, ST-Q9 A, ST-Q10 A", relayed by team-lead (questions: `/workspace/logs/skills-tools-1519f/st-questions-2/index.html`):
  - Q: Do project-folder skills count as unverified (ST-Q8)? → A: yes; they ask once for each new or changed version, even in God Mode (C-19, FR-082, S-105).
  - Q: What happens when a task or plan run needs a skill that asks (ST-Q9)? → A: today's behaviour: the skill is skipped with "Not run: needs approval"; approving once in a chat makes it work everywhere, also in tasks and plans; no new wait mechanism (C-12, FR-083, S-109). The earlier ST-Q3 option text was wrong on this point; the founder saw the correction before answering.
  - Q: What do built-in skills added by a later Omnipus release start at (ST-Q10)? → A: Allow, their shipped default (C-2, FR-020, S-108).

### 2026-11-22

- Founder answers to the round-2 questions, Discord #team-b, 2026-11-22 03:34–03:55Z, relayed by team-lead:
  - Q: Does "Allow everywhere" change a skill's global permission for good (R2-Q1)? → A: no; neither "Allow everywhere" nor "Always Allow" exists any more, for tools and skills (the founder page's Q3 B, "timed only"; team-lead's recommended A was not taken). Cards offer Approve Once, Allow for 1 hour / 3 hours / 1 day, and Deny; a timed allowance belongs to the agent (all its chats and the helpers it starts for itself), covers the same narrow thing as today's grant, ends at its time or at a restart, and is never saved; a lasting Allow exists only as stored policy, the global value always wins, and the screen disables an agent Allow under a global Ask with a tooltip and a link. Team-lead's defaults, told to the founder and not contradicted: a timed allowance works wherever Approve Once works, never under Deny; unattended runs honour a running one and never ask or wait (C-12, C-24, C-26).
  - Q: Does a newly added skill need a person's first yes even with Auto-approve on (R2-Q2)? → A: no; a new, already-checked skill at Ask is settled by Auto-approve like any Ask; the trust check still asks for unchecked skills (C-12).
- Follow-up rulings on the architect's R2-Q1 points, 2026-11-22, relayed by team-lead:
  - Q: What does a time choice on an ordinary tool card cover? → A: the whole tool for that agent until it ends, like a timed agent-level Allow; narrow cards stay narrow; a skill card covers the skill's permission part; it answers an Ask and never lifts a Deny (founder, Q4 B, Discord #team-b, 05:07Z) (C-26 point 6, FR-090, S-120, D17 row 13).
  - Q: Does a permission change end a running allowance? → A: yes; any change of that tool's or skill's permission, globally or for that agent, including back to Ask, ends its allowances for the affected agents, audited (team-lead's call, "the safe side") (C-26 point 7, FR-096, S-128, ST-32). Architect's reading, stated in C-26: trust, levels, Auto-approve and God Mode are not permissions and end nothing; a same-value save ends nothing.
  - Q: May the server store an agent value looser than the global one? → A: keep as today: stored, floored at run time, blocked only on the screen (team-lead's call) (C-0, C-26 point 8).
  - Q: Does a delegation to another agent still inherit the chat's grants? → A: no (founder's "other agents are unaffected"); kept as written (team-lead).
  - Q: Does Cancel stay on cards that have it today? → A: yes; kept as written (team-lead).
  - Q: ST-Q9 A narrowed by R2-Q1? → kept as written (team-lead), see the next item.
- Founder answers, Discord #team-b, 2026-11-22, relayed by team-lead:
  - Q5 (06:20Z): Should the risky tools get the whole-tool time choice? → **A**: `environment_setup`, `request_mount`, `send_email`, `browser_evaluate` and `set_config` keep a card for every action, offering Approve Once and Deny only, no time choice; no allowance can cover them; the server refuses `allow_for` for them (C-24, C-26 point 9, FR-097, S-129, D17 rows 21–25, ST-33).
  - Q6 (06:23Z): Does a permission change end running allowances? → **A**: yes (confirms team-lead's call; C-26 point 7, FR-096).
  - Q7 (06:23Z): May the server store a looser agent value? → **A**: keep as today: stored, the stricter global value wins at run time, only the screen blocks it (confirms team-lead's call; C-26 point 8).
  - `install_skill` keeps the whole-tool time choice, listed in the security points (team-lead, same day).
  - Q1 (architect, after Q5 A): Do the five exact-action tools keep Cancel on their cards where the card has it today? → **A, keep Cancel** (team-lead's call, 2026-11-22, consistent with the earlier "Cancel stays" ruling). Cancel is not an allowance and stores nothing. In the contract, `cancel` stays in the action enum and is listed in a card's `offered_actions` whenever the card shows Cancel (C-24, C-26 point 9).
  - Effect on ST-Q9 A ("approving once in a chat makes it work everywhere, also in tasks and plans"): narrowed by R2-Q1. A chat answer now makes the skill work for that agent's tasks and plans until the time ends; for every agent and for good, the person sets Allow on Skills & Tools.

## Questions for the founder

ST-Q1 to ST-Q10 are answered (Clarifications, 2026-11-21). The two grill-round-2 questions are answered too (Clarifications, 2026-11-22):

- **R2-Q1** — answered 2026-11-22 (Discord #team-b, 03:34–03:55Z, relayed by team-lead): timed allowances only, the founder page's Q3 B. Written into C-12, C-24, C-26, US-2, US-3, US-13, FR-016, FR-023, FR-067, FR-083, FR-089 to FR-095, Datasets D10 and D17, ST-04, ST-26, ST-29 to ST-31, DOC-013, DOC-015 to DOC-017.
- **R2-Q2** — answered 2026-11-22, same message: A (Auto-approve settles a checked new skill's Ask; the trust check is the protection). The reading already in place stays; its markers are removed.

No question is open for the founder or for team-lead. The architect's R2-Q1 points were settled on 2026-11-22, and the founder answered Q5, Q6 and Q7 the same day (Clarifications).

### Notes for team-lead

**Settings spec edits (Settings first, then Skills & Tools).** Edits for team-lead to make in `docs/internal/specs/settings-redesign-spec.md`, as read at `landing/settings-1519` `8e5848c951` (line numbers approximate):

| # | Where in the Settings spec | Change |
|---|---|---|
| 1 | FR-060 (around line 1976) | reverse the order: Settings lands first; Skills & Tools lands after it on top (issue #1963). The readiness check moves to Skills & Tools FR-065 |
| 2 | Integration Boundaries, "Skills & Tools page (prerequisite, S-Q3 A)" (around lines 640–643) | becomes "Skills & Tools page (follows this package)"; Order: Settings lands first; Skills & Tools restores the grid, trust and web search on its page |
| 3 | Security focus-area note "Tool permissions and skill trust leave Settings because Skills & Tools lands first" (around line 555) | "leave Settings' screens at this landing; between the two landings they have no screen (API only); the release is not cut in between" |
| 4 | Qualitative prohibition "must not ship a temporary layout … cut only after Skills & Tools has landed" (around line 594) | "the release must not be cut with Settings landed and Skills & Tools not landed"; or, if team-lead wants no gap on the release branch, a stopgap that keeps the grid and trust visible until Skills & Tools lands (team-lead's call; S-Q3 A said no temporary moving block) |
| 5 | Reachability row "Prerequisite: Skills & Tools landed on the landing base before the cut" (around line 2056) | delete; add "Follow-up: Skills & Tools (issue #1963)" |
| 6 | Scenario "Moved items are announced in Protection" (Given "Skills & Tools has landed", around line 1354) | Given "Settings landed"; note that the notice becomes true when Skills & Tools lands |
| 7 | C-7 row "Load tools on demand (`tools.manifest.compressed`) … newly refused" (around line 259) | keep; add "until Skills & Tools lands, which removes the switch and moves this refusal to its retired-key refusal (Skills & Tools C-22 rows 2–4)" |
| 8 | Agent-write row "`set_config` tool — refuses `tools.manifest.compressed`" (around line 282) and machine-verifiable line (around line 612) | keep; same note as row 7 |
| 9 | FR-100 (around line 1999) | keep `tools.manifest.compressed` in the list; note that Skills & Tools removes it from this guard in the same change that adds its own refusal |
| 10 | FR-101 / `TestSettingsAgentWriteParity` (around line 2000) | keep "Load tools on demand" in the enumeration; Skills & Tools removes it together with the control |
| 11 | Dataset D-9 row 12 "79 Tool loading switch — Removed later — stays in Limits → Agents and goals until R31 ships" (around line 1774) | "stays in Limits until Skills & Tools lands (ST-Q4 B), which removes it" |
| 12 | Dataset D-16 rows 4, 7, 8 (around lines 1872–1876) | keep for Settings; Skills & Tools adds its own rows with the new text at its landing |
| 13 | Dataset D-9 rows 4 and 6 ("27–31 Web search — Moved out", "38, 39 Skill trust, tool grid — Moved out", around lines 1766–1768) and FR-007's "web search … moved to Skills & Tools" answer (around line 1908) | the code at `8e5848c951` still shows web search under Providers; either Settings keeps it there and Skills & Tools moves it at its landing (Skills & Tools C-22 row 5, the reading this spec uses), or Settings removes it now; FR-007's web-search answer then belongs to Skills & Tools' landing |
| 14 | D-9 row 6 and the retired endpoints | `GET/PUT /security/skill-trust` and `GET/PUT /security/tool-policies` are retired by Skills & Tools (its C-22 row 6), not by Settings; the legacy layout of `SecuritySection` is deleted there |
| 15 | DOC-007 (`docs/security.md`) and the Settings user page | Settings' docs say tool permissions and skill trust "are in Skills & Tools"; that is true only after Skills & Tools lands, so either word it as "move to Skills & Tools" or let Skills & Tools update those sentences at its landing (its DOC-009, DOC-014) |

**Other notes.**
- This is a product-wide change of every approval card, in `pkg/security`, `pkg/sandbox`, `pkg/audit` and `pkg/agent`: the Opus sizing rule and the agent-engine hands-on UAT apply, and the security-lead's one review covers C-26's nine points.
- Between the two landings the release branch has no screen for the global tool grid and skill trust (C-22). The spec makes "do not cut the release in between" a rule (FR-065); a stopgap in Settings is the alternative (row 4). Team-lead decides; not a founder question unless the gap is unacceptable.
- This package edits files the Settings team owns (`PerformanceSection.tsx`, `IntegrationsSection.tsx`, `SecuritySection.tsx`, the Settings search answers, `src/lib/api/config.ts`). It lands after Settings, so there are no two writers at once, but the Settings owners should know.
- The Linux kernel rendering of the project-skill gate (C-25) is 🔶 Inferred feasible, not built. If it proves infeasible, Linux falls back to the app-level gate for project skills, as on Windows, and DOC-004 says so; that is a security trade-off for security-lead and, if needed, the founder.
- The rider set is still unconfirmed by the chief (header, Riders); Stage and Horizon of #1963 were not checked (header).
- Upgrade test homes (founder rule 2026-11-28, C-19): UAT lanes and test instances must start from a saved pre-release data folder. A folder that already ran a build of this package from before 2026-11-28 carries upgrade files and a marker that no released install can have; this spec adds no clean-up code for them.
- Open dispositions after the on-demand review of 106f63ef9 (2026-11-28): (1) the founder answered on 2026-11-28 14:11Z (Discord #team-b, message 1576239077403922534): Q-AUD1 A — the two no-entry cases of the trust-conversion audit are accepted and named in DOC-004 (C-3); Q-UPG1 A — an ordinary save that would remove the last old value of a deferred `config.json` is refused with 409 `skills_not_listable` (C-19, Dataset D13 row 22); security-lead still reviews both before GREEN; (2) the one writer rule (C-19) changes every writer of `config.json` to take the sidecar `config.json.lock`; it is a general store change inside this package's files, not a new product behaviour; chief confirmation of riders stays outstanding as before.

---

## Round-1 fix log

Grill round 1 report: `docs/internal/specs/skills-tools-redesign-spec-review.md` (untracked, at review commit `5028ff7b01`).

| Finding | What changed | Where |
|---|---|---|
| CRIT-001 Full before consent | Full injects only when the skill's outcome is `allowed` for that agent and chain; Ask or trust-Ask Full skills are listed as Name and short description until approved; unattended never injects; sentinel tests on captured requests | C-1, C-11 E8, C-14, FR-038, US-5 AS8, S-42, S-89, D9, ST-13 (ST-Q1 A) |
| CRIT-002 File reads and helpers bypass the skill gate | Inventory of nine entry points with one outcome; denied and unapproved skill folders leave the loadable set in the app guard and the kernel grant; editor read exception kept separate and read-only | C-11, FR-066, US-2 AS9, S-85–S-87, ST-19, SC-006, DOC-010 |
| CRIT-003 Partial save needs the whole tool map | New `GET/PATCH /security/permissions` naming only changed keys; whole-request validation; null, empty, duplicate rules; two-tab test; legacy PUT kept for today's Settings | C-2, API and Data, FR-013, FR-072, D6, S-82, ST-20, SC-007 |
| CRIT-004 Missing server key is not missing permission | Coverage decided by the resolver's own matching; full state matrix incl. broader wildcards, partial coverage, agent-only values, God Mode; two named behaviour changes; no agent entries written | C-7, C-18, FR-045, FR-074, D8, S-88, ST-22, SC-003 (ST-Q5 A) |
| MAJ-001 One consent contract | Consent table: ordinary Ask vs trust check, Auto, God, remembered grants bound to revision, installs, REST, unattended, stale answers | C-3, C-12, FR-016, FR-030, FR-067, D10, S-10, S-30, S-93, ST-21 (ST-Q2 A) |
| MAJ-002 Install review lacks facts | Stage-only review endpoint returning verification, revision, suggested category; publish exactly the reviewed revision; expiry; truthful Cancel and lost answers | C-13, FR-042, FR-068, US-7 AS4/AS10, S-94, S-95, UI states |
| MAJ-003 Project skills have no identity | Own keys `project:<workspace>/<slug>`, lifecycle, same-slug rule, migration order (idempotent, restart-safe), named change for later grants | C-19, FR-019, FR-075, FR-080, Edge Cases, S-102, D6 row 16 |
| MAJ-004 Visibility migration copied labels | Starting levels derived from compression, discovery, deferred, preview flag; per-server keys for offline and future tools; literal captured requests as fixtures; SC-003 lists named changes | C-6, D7, FR-037, S-41, ST-06, SC-003 (ST-Q4 B) |
| MAJ-005 Live level changes undefined | One visibility authority; levels apply from next turn without evicting loaded definitions; Deny removes; skill menu rebuilds on change; demoted built-ins searchable | C-15, FR-070, S-91, S-92, D11, ST-12 |
| MAJ-006 Full has no capacity outcome | Charged once to the request budget; turn not sent with a clear text; page shows sizes and a warning | C-14, FR-069, FR-079, S-90, D9 rows 7–10, UI states (ST-Q1 A) |
| MAJ-007 First landing breaks Settings | Legacy trust endpoint kept and mapped; legacy tool grid kept; links use today's addresses and neutral text; `set_config` refusal of the manifest keys lands here; Settings-spec change requests named; Performance vs Limits naming corrected | C-21, C-22, FR-049, FR-078, US-9 AS2/AS3, S-36, S-65, S-101, ST-24, Assumptions (ST-Q4 B) |
| MAJ-008 Parity misses create, updates, authoring | Per-path parity table; `create_agent` `skill_policy_changes`; update preservation; category set once for written and copied skills; write-skill sets category; new parity tests | C-10 (pointer), C-21, FR-056, FR-057, FR-077, S-97, S-98, S-103, S-104, ST-15 |
| MAJ-009 Autosave promises | Six save states; one request per click in order; app-level queue; late-failure toast; keepalive; 1,000 ms is request start | C-16, FR-007, FR-071, US-1 AS10, S-83, D12, UI states, User Journey |
| MAJ-010 Set all vs always-ask | Per-row allowed values; ineligible rows skipped and named; counts in label; server guards unchanged | C-20, FR-003, FR-076, US-1 AS2, US-6 AS4, S-48, S-84, D4 rows 7–10 |
| MAJ-011 No-confirm vs plan warning | Single exception stated everywhere and tied to the founder question | Overview, departures, FR-007, FR-010, prohibitions, S-05 (ST-Q7 A) |
| MAJ-012 Tool edits not audited per key | Per-key audit for every map and trust, on PATCH and the legacy PUT; none for no-ops and refusals; failure handling stated; FR-040 now traces to an audit scenario | C-2, FR-040, S-99, ST-23, D6 row 15, Traceability |
| MAJ-013 Focus lost when a row leaves a filter | Deterministic focus destinations and announcement | Accessibility and Keyboard, FR-052, US-6 AS6, S-100, D4 row 10 |
| MAJ-014 Checks that cannot fail | Captured requests with sentinels instead of model answers; literal migration fixtures; W5 to a published categorised skill; direct REST install path; audit assertions; mutation probes and execution checklist | Hands-on rows ST-06, ST-09, ST-12, ST-13, ST-15, ST-19–ST-24; Test Implementation Order (positive controls); SC-003, SC-005 |
| MAJ-015 Unified search dropped without decision | ToolSearch finds skills, typed, no loading; recorded as pending, not as an assumption | C-17, FR-073, S-96, Assumptions, DOC-012 (ST-Q6 A) |
| MIN-001 view-switch is a radio group | Tabs use the catalogued `tabs` component | Design-System Components, Accessibility and Keyboard, FR-052 |
| Coordination warning (epic, riders) | Header states the epic mismatch as Unknown for the chief; riders still unconfirmed | Header, Riders, Notes for team-lead |
| FQ-5, FQ-7 | Recorded as decisions (founder default) | Clarifications, Ambiguity Warning 1, C-1 |
| Founder answers ST-Q1–ST-Q7 (2026-11-21) | Every pending marker resolved to the chosen option; the unchosen options and switch-point lists removed | whole spec; Clarifications |
| ST-Q3 B (new skills start at Ask) | Self-heal default Ask (built-ins keep their shipped Allow); new skills, copies, written and project skills start at Ask; combined-card rule with the trust check; toast text; unattended refusal kept | C-2, C-12, C-19, FR-020, FR-081, US-2 AS8, User Journey, UI states, S-17, S-98, S-106–S-108, D6 row 17, D10 rows 12–16, ST-15, ST-16, ST-25, DOC-013 |
| ST-Q4 B (switch leaves Settings) | Named "tool-loading conversion"; this package removes the switch from today's Performance screen and `tools_on_demand` from the Performance contract, refuses the retired keys; Settings hand-off list | C-6, C-21, C-22, FR-039, US-5 AS9, S-44, D2, D7, DOC-014, Notes for team-lead |
| ST-Q8 opened | Option A in place with its switch points | C-19, FR-082, S-105, D10 row 17, ST-25, SC-003, Questions for the founder |
| Founder answers ST-Q8–ST-Q10 (2026-11-21, 13:19Z) | ST-Q8 A: markers resolved, switch-point list removed. ST-Q9 A: no waiting step anywhere; "Allow everywhere" card action added. ST-Q10 A: built-ins added by a later release start at Allow, confirmed | header, C-2, C-12, C-19, US-2 AS10, S-105, S-108, S-109, FR-082, FR-083, D10 rows 16–18, test row 47, SC-001, SC-003, DOC-013, Traceability, Clarifications, Questions for the founder, Notes for team-lead |

## Round-2 fix log

Grill round 2 report: `docs/internal/specs/skills-tools-redesign-spec-review-round2.md` (untracked, at review commit `b304736f72`).

| Finding | What changed | Where |
|---|---|---|
| R2-CRIT-001 Upgrade removes its restart source | Snapshot written once before any change; ledger with one in-flight agent and a cross-process lock (the pattern of `delegation_depth_migration.go`); policy resolved from the snapshot plan until done; replay never reads replaced values and never overwrites a person's later change; failed and unreadable agent records handled and shown; the prohibition narrowed to backfill, translation allowed; stop-point fixtures at every boundary | C-19, C-18, C-6, Qualitative Prohibitions, FR-019, FR-075, FR-084, S-110, D13, ST-27, SC-008, test 49 |
| R2-CRIT-002 Project skills under mounts not gated | Project-skill gate: path identity, which mounts, reads, search, listings and helpers, precedence before every mount and work-dir grant, editor exception, next turn; kernel rendering on macOS and (Inferred) Linux; app-level-only platforms named and logged, never claimed as kernel-enforced | C-11 E6/E7, C-25, Edge Cases, FR-085, S-111, D14, ST-19, ST-28, SC-006, test 50, DOC-004 |
| R2-MAJ-001 Consent table contradicts itself | One decision table: which settlers close each check, Auto-approve per today's rule (checked before the unattended refusal), entry points, interactive and unattended outcomes, stored changes; `install_skill` card count settled (one install card, its own permission kept); D10 rebuilt with every input stated | C-12, US-2 AS3/AS8/AS10, S-12, S-107, S-109, S-118, D10, FR-016, FR-017, FR-067, FR-081, ST-05, ST-25, DOC-013 |
| R2-MAJ-002 Approval actions lack a contract | Action and response schemas extended, offered actions computed by the server, immutable binding, re-check, order of effects, partial failure, replay, audit, scope text; More-actions endpoint defined; trust approvals stored in the protected `system` area | C-9, C-24, C-5, API and Data, FR-083, FR-086, S-112, S-113, S-114, ST-26, tests 51, 52 |
| R2-MAJ-003 Hand-off written for the wrong order | Settings first: the Settings base read at `8e5848c951`; the gap between landings stated; this landing's atomic changes listed with one owner (switch removal, guard transfer, parity test, web search, retired endpoints, links); release rule; Settings-spec edit list rewritten for team-lead | Overview, C-22, C-2, C-3, C-21, Integration Boundaries, FR-039, FR-065, FR-078, US-9, S-36, S-44, S-65, S-101, ST-24, Assumptions, Notes for team-lead |
| R2-MAJ-004 Serial save timing and late errors | "Saving…" within 100 ms; request within 1,000 ms only when nothing earlier is pending; next request after the previous outcome and its re-read; same-key replacement while queued; 500 and check-failed states; late toasts by class; empty versus same-value PATCH | C-16, C-2, UI States, Machine-Verifiable Constraints, FR-007, FR-071, FR-072, S-117, D6 rows 15, 18, 20, D12 |
| R2-MAJ-005 Conversion claims equality it cannot keep | ToolSearch named as the one extra difference for switch-off installs; goal-narrowed and next normal requests both captured; permissions unaffected | C-6, D7 row 16, S-41, S-44, ST-06, SC-003, FR-037, DOC-014, test 56 |
| R2-MAJ-006 Skills list cannot prove an install | Review id is the operation id; status endpoint with terminal states kept 15 minutes; idempotent submit; lost-answer polling and Unknown result; `Skill.revision` | C-13, C-5, UI States, FR-068, FR-087, S-115, D15, test 53 |
| R2-MAJ-007 Writers disagree on project keys | One known-skill-key rule for every writer; request envelope kept; omitted, set, remove, clear, null, unknown, duplicate and workspace-deleted cases; read-back of project keys | C-4, C-23, C-21, Machine-Verifiable Constraints, FR-077, FR-088, S-116, D16, test 54 |
| R2-MAJ-008 Oracles not independent | S-102 literal winners for a granted and an ungranted agent; S-88 takes every D8 dimension and asserts all rows ran; D9 fit and one-over with headroom, reserve and unavoidable content; disabled Omnipus Ask split from the API 422; test 5 retargeted to PATCH; consent and approval actions get handler, restart and scheduled-run tests plus a hands-on row; new mutation probes | S-22, S-88, S-102, D8, D9, tests 5, 14, 22, 39, 40, 46, 47, 51–56, Positive controls, ST-08, ST-26 |
| Coordination: epic | Header names #1963 under #1519; Stage and Horizon Unknown | Header |
| Founder answers R2-Q1 (timed only) and R2-Q2 A, 2026-11-22 | No Always Allow or Allow everywhere on any card; Approve Once, Allow for 1 hour / 3 hours / 1 day, Deny; per-agent, in-memory, ending at its time or a restart; contract, impact analysis, migration, security-review points; locked agent badge tooltip and link; every pending marker resolved | Header, C-9, C-12, C-24, C-26 (new), API and Data, Impact Assessment, US-2, US-3, US-4, US-13 (new), Edge Cases, UI States, Accessibility, Security and User Promises, Behavioral Contract, Prohibitions, Machine-Verifiable Constraints, S-10, S-11, S-30, S-106, S-107, S-109, S-112 to S-114, S-118, S-119 to S-127 (new), D10, D17 (new), ST-04, ST-07, ST-26, ST-29 to ST-31 (new), tests 51, 52, 57 to 62, FR-016, FR-017, FR-023, FR-030, FR-067, FR-083, FR-086, FR-089 to FR-095 (new), SC-001, SC-002, SC-009 (new), DOC-006, DOC-013, DOC-015 to DOC-017, ADR-TODO-3, Clarifications, Questions, Notes for team-lead |
| Follow-up rulings 2026-11-22 (founder Q4 B; team-lead on the architect's points) | Ordinary card covers the whole tool; a permission change ends running allowances, audited; looser agent values kept as today; delegation, Cancel and ST-Q9 narrowing kept; notes moved to Clarifications | C-24, C-26 (points 6–8, Key, End, A permission change, Deny, Audit, impact rows, security points 2, 7, 8), US-13 (AS3, new AS8), Edge Cases, Behavioral Contract, S-120, S-121, S-128 (new), D17 rows 10–20, ST-29, ST-32 (new), tests 57, 63 (new), FR-090, FR-091, FR-096 (new), SC-002, DOC-015, DOC-016, Clarifications, Questions, Notes for team-lead |
| Founder answers Q5 A, Q6 A, Q7 A (2026-11-22) | Five exact-action tools get Approve Once and Deny only, never an allowance; server refuses `allow_for` for them; timed-allowance examples moved from `send_email` to `delete_task`; Q6 and Q7 recorded | C-12, C-24, C-26 (point 9, card table, impact, refusals, security points 7 and 9), US-13 (Independent Test, new AS9), UI States, Prohibitions, Machine-Verifiable Constraints, S-119, S-120, S-124, S-126, S-129 (new), D17 rows 10, 12–14, 16, 18, 21–25 (new), ST-29, ST-32, ST-33 (new), test 64 (new), FR-089, FR-097 (new), SC-002, DOC-015, Clarifications, Questions |

## Change log

| Date | Change | Decided by |
|---|---|---|
| 2026-11-25 | Spec gaps (a), (b), (c) found by the test author settled: (a) `PermissionSettings.upgrade_unfinished` with fixed reason codes, from new ledger fields `failures` and `skipped`; a skipped agent is listed only while its record file exists; (b) the Omnipus tools tab's impossible "levels fail" partial state replaced by "tools load, permissions fail"; (c) `Skill.full_tokens` and `Agent.full_skills_tokens` computed by the server with the turn's estimator and Full-skill selection, warning against `context_window_effective`. New S-130 to S-132, Dataset D18, tests 65 to 67, US-5 AS12, US-10 AS5; FR-079 and FR-084 reworded; traceability updated. Contract files are named here and changed by backend-lead. Sections: C-2, C-14, C-19, API and Data, US-5, US-10, UI Screens and States, BDD Scenarios, TDD plan, Dataset D18, FR-079, FR-084, Traceability Matrix | team-lead ruling, 2026-11-25, on the architect's `/workspace/logs/skills-tools-1519f/arch/spec-gaps-1.md` (options A2, B2, C3) |
| 2026-11-28 | Founder rule 2026-11-28 (no upgrade state, no done check, fresh-install-like result; Q2 A: named ADR-067 SC-009 exemption allowed): the one-time upgrade no longer uses a lock, snapshot, ledger or `sandbox.migrations` marker. Each file converts itself in one atomic write, triggered only by the old-shape values it holds: agent records at gateway start, `config.json` inside every config load before decoding. Old keys are removed in the same write. A file that cannot be written is read through the same conversion and retried. `PermissionSettings.upgrade_unfinished` and the Skills-tab notice are removed (contract change; backend-lead edits the files named in C-2). Per-agent `Skill` values are no longer seeded and are refused by every writer. An old per-agent `Skill: allow` is not copied. Sections: C-2 (contract row, self-heal), C-3, C-6, C-18, C-19, C-21, C-22, API and Data, US-2 AS7, Edge Cases, UI Screens and States, Security, Prohibitions, S-16, S-33, S-110 (rewritten), S-131 (retired), S-133 (new), ST-27, tests 30, 49, 67 (retired), 68–71 (new), Positive controls, Dataset D7 rows 17–18, Dataset D13 (rewritten), FR-019, FR-037, FR-084, SC-003 (note), SC-008 (rewritten), SC-010 (new), DOC-004, Traceability FR-084. Dataset D17 and SC-009 of this spec are unaffected | Team B Skills & Tools architect, on the founder's instruction 2026-11-28 11:15Z; ruling `/workspace/logs/skills-tools-1519f/arch/ruling-upgrade-no-marker.md` |
| 2026-11-28 | On-demand review of 106f63ef9 (BLOCK; NM-01 to NM-06) and the test author's seven items, founder rule unchanged: one writer rule for `config.json` (`config.UpdateConfigFile` under `restAPI.configMu` and the generic sidecar `config.json.lock`) and agent records (`agentstore.Store.UpdateRaw` on the entity lock), every read inside the lock (NM-01); no old `Skill` value removed while the skill inventory is incomplete, the kept value applies to every skill without its own value, Error names the cause (NM-02); per-agent `Skill: allow` accepted and not stored, `Skill` optional in complete maps, `ask`/`deny` refused, old package values translated (NM-03); every reader uses the converted view, including `GET/PATCH /security/permissions` (NM-04); the two no-entry audit cases named in C-3, S-33 and DOC-004, disposition pending (NM-05); D13 split into stored/effective/diagnostic/repair columns with rows 14–18, SC-010 comparison classes, S-133 setup order (NM-06); Jim and Ray defined, "cannot be written", seam `fileutil.SetWriteFaultForTest`, Go signatures of the entries, `auditOldTrustConversion`, C-3 trust-unavailable row, C-8 server refusal. Sections: C-2, C-3, C-8, C-19, C-21, Edge Cases, Security, Prohibitions, S-16, S-33, S-110, S-133, ST-27, tests 49, 69–73, Positive controls, Dataset D13, FR-084, SC-008, SC-010, DOC-004, Traceability, Notes for team-lead. IDs kept; tests 72 and 73 new | Team B Skills & Tools architect; ruling `/workspace/logs/skills-tools-1519f/arch/ruling-upgrade-no-marker.md` (revised) |
| 2026-11-28 | Re-check of 114b3fe8a (BLOCK; R2-01 to R2-03, W-01, W-02), founder rule unchanged: the `config.json` in-process mutex (path-keyed) and sidecar both taken inside `config.UpdateConfigFile`, so the reload and self-heal writers are covered on every platform; lock order `configMu` → agent-loop lock → `config.json` lock (leaf), heal side effects after the commit (R2-01); a file whose conversion needs an incomplete skill list is deferred as a whole, so an old global `Skill: allow` stays as the trigger and the kept value (any of Allow, Ask, Deny) fills every skill without its own value (R2-02); `SaveConfig` replaced by `SaveConfigChanges(path, before, after)`, which writes only the changed keys, so the promise "every write applies only its own change to the content read inside the lock" has no exception (R2-03); `ConfigWrite.Committed` decides "not saved" versus saved with a logged follow-up error; a load that cannot take the locks reads `ReadConfigView` and writes nothing (W-01); the kept master is projected out of the tool maps into the per-skill fallback, an explicit per-skill value wins (W-02); writer list corrected (`mail_identity.go`, `onboarding.go` are not `config.json` writers; `CreateConfigFileIfAbsent` for `datamodel.Init`). Sections: C-2, C-19, Edge Cases, Prohibitions, Dataset D13 (rows 14–16 rewritten, 19–21 new), tests 70, 72 (cases e, f), 74 (new), Positive controls, FR-084, SC-008, Traceability, Notes for team-lead (Q-AUD1 neutral). IDs kept | Team B Skills & Tools architect; ruling `/workspace/logs/skills-tools-1519f/arch/ruling-upgrade-no-marker.md` (revision 3) |
| 2026-11-28 | Review 3 of 46eddd624 (BLOCK; R3-01 to R3-05 and nits), founder rule unchanged: `SaveConfigChanges` compares objects member by member, deletes members not containers, keeps a later-committed scalar, and refuses as a conflict a list another writer changed (R3-01); an ordinary save that would remove the last old value of a deferred `config.json` (possible only with a kept global `Skill: allow`) put to the founder as Q-UPG1 (R3-02; answered A, see the next row); the pairing writer split (agent-loop read before the file lock), system-agent publication through the held loop lock from the converted view (R3-03); the heal's auxiliary work runs after any error-free result, committed or no-op (R3-04); Dataset D13 rows 19–21 oracles corrected, row 22 new (R3-05); test seams `SetAfterRenameFaultForTest` and `SetUnlockFaultForTest`; commit order is the oracle for "the later write wins". Sections: C-2, C-19, Edge Cases, Dataset D13, tests 70, 72 (f1–f4, g, h), 74, 75 (new), Positive controls, FR-084, DOC-004, Traceability, Notes for team-lead. IDs kept | Team B Skills & Tools architect; ruling `/workspace/logs/skills-tools-1519f/arch/ruling-upgrade-no-marker.md` (revision 4) |
| 2026-11-28 | Founder answers (Discord #team-b, 2026-11-28 14:11Z, message 1576239077403922534): Q-UPG1 A — an ordinary save that would remove the last old value of a deferred `config.json` is refused with 409 `skills_not_listable` and the sentence of C-19 by every writer that can make that change (`PATCH /security/permissions`, `DELETE`/`PATCH /mcp-servers/{id}`, the system agent's MCP tools, any other `UpdateConfigFile` caller), decided inside the lock from the file and skill list read there; contract: new code documented in `ErrorResponse.yaml`, 409 added to `DELETE`/`PATCH /mcp-servers/{id}`. Q-AUD1 A — the two no-entry cases of the trust-conversion audit are accepted, narrowly; ordinary-write audit unchanged. Sections: C-3, C-19, Security, Dataset D13 row 22, test 70, Positive controls, FR-084, DOC-004, Notes for team-lead | Team B Skills & Tools architect; ruling `/workspace/logs/skills-tools-1519f/arch/ruling-upgrade-no-marker.md` (revision 5) |
| 2026-11-28 | Review 4 of 69e5d3f6a (PASS WITH NOTES; R4-01, W4-01, W4-02, N4-01; R4-02 and R4-03 fell away with option B): list conflicts are checked against the caller's in-memory persisted baseline (`(*Config).PersistedRaw`, never written) and a non-conflicting list edit is applied by position, member by member, so load normalization never causes a conflict and unknown fields stay (R4-01); rejection and installation of the newer stored view are separate, a no-op publishes the stored view, a conflict installs it with `SwapWrittenConfig` after the callback, never a reload under `configMu` (W4-01); auxiliary effects re-check inside their own locks (still-gone keys only; known-builtins by locked re-read and union) (W4-02); the Q-UPG1 refusal is computed in memory under the lock before any temp file and always maps to the typed 409; C-2's post-commit error list matches "Commit point" (N4-01). Sections: C-2, C-19, tests 72 (i–l), 75, Positive controls | Team B Skills & Tools architect; ruling `/workspace/logs/skills-tools-1519f/arch/ruling-upgrade-no-marker.md` (revision 6) |
| 2026-11-28 | Review 5 of db06d577d (BLOCK on R5-01; R5-02, W5-01, N5-01, notes): the longest-common-subsequence list alignment is replaced by a correspondence rule — equal elements keep their stored element when moved, one edit or only inserts or only deletes per save, anything unclear (several edits, ambiguous duplicates, a load that changed the element count) is refused with `ListChangeUnclearError`; the baseline is deep-copied and replaced only together with its view; a rejection keeps the live runtime-only state; known-builtins grows only from built-ins installed and healed in that run; `change` callbacks are pure and credential writes move to a `BeforeRename` step after every refusal; a place and cause for an unreadable workspace list; test 70, 72 (m)–(s) and 75 (d) extended (revision 7, #1963) |
| 2026-11-28 | Review 6 of 361e2d772 (BLOCK; R6-01, R6-02, R6-03, W6-01) and team-lead's direction: system-agent saves become named operations applied by `UpdateConfigFile` to the document read inside the lock (`configure_provider` by provider name, MCP add and remove by server name, `set_config` on its own key, channel writes on their instances). Withdrawn: `SaveConfigChanges`, the list conflict check, the persisted baseline, the list correspondence rule and its count guard, `ListChangeUnclearError`. One refusal remains: `set_config` replacing a list whose stored entries hold members the tool can't show. Credential effects get a full lifecycle (undo only when not committed, a failed undo reported by name, secret-only no-op); the concurrent-writer rollback window is stated as a pre-existing limit with a follow-up issue (revision 8, #1963) |
