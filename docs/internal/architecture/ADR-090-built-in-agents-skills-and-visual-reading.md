# ADR-090 — Built-in agent configuration, skills, and visual file reading

- **Status:** Accepted product decisions (founder-confirmed 2026-09-17); grill-spec and independent Claude Code Opus reviews completed and findings corrected; L1 resolved to original Elicify skills; implementation delivered with verification recorded in the [implementation and verification ledger](../specs/adr-090-implementation-status.md) and the [generic environment setup verification record](../specs/adr-090-environment-setup-verification.md); release review pending; native Windows explicitly deferred; Linux Office rendering pending UAT.
- **Date:** 2026-09-17; dependency-setup decision amended 2026-09-18
- **Decider:** Daniel Piatkowski
- **Number verification:** ADR-090 absent from architecture paths in all locally reachable Git history after fetching origin on 2026-09-17; highest observed number 089. Recheck before publication because other branches can allocate concurrently.
- **Input:** [Consolidated requirements](../design/built-in-agents-and-skills-2026-09.md) and [confirmed decisions](../design/built-in-agents-and-skills-2026-09-decisions.md).
- **Evidence baseline:** release worktree at `3463b2d36`; source findings are documented in the [image verification](../design/built-in-agents-and-skills-visual-inspection-verification.md) and [tool analysis](../design/built-in-agents-and-skills-tool-visibility-analysis.md). GitNexus cannot resolve this unindexed worktree; source inspection is the fallback, not graph-derived impact evidence.

## Context and decision boundary

The current built-ins are too broadly locked for users to configure their capabilities, Ava lacks the complete operations her role promises, and document generation lacks a direct visual-reading path. The approved requirements already define the desired roles and workflows. This ADR retains that document's detailed tables and records the architectural choices and consequences alongside them.

This is greenfield work on agents, skills, and the tool integration required for their workflows. No migrations, legacy-agent deletion, worktree isolation engine, new Office engine, or autonomous model switching is included. Existing installed user choices still survive ordinary reload and restart. Source code describes current behavior; this accepted ADR specifies the intended changes, not a claim that they already ship.

Decision identifiers: **D1** roster and role boundaries (§§1–4); **D2** field-level editing (§2.6); **D3** Ava's configuration workflow (§§5.1–5.2); **D4** default permissions and global visibility (§§5, 5.3–5.4); **D5** skills and dependency ownership (§6); **D6** visual file reading (§6.6); **D7** isolation exclusion (§7). Sections 10–11 define acceptance and delivery boundaries.

## 1. Intent

A new user talks to **three colleagues** and one **operator**. Labour is named staff Jim assigns. Hidden engine agents score and correct plans. Nobody else sits in the sidebar.

Success looks like:

- Mia welcomes and acts as personal assistant (files, mail, tasks), including generating Office documents and PDFs with the document skills and execution tool.
- Jim interviews, plans, and runs work — he does not do the labour.
- Ava creates and edits teammates and skills **completely** (tools, skills, team, delegation graph).
- Admin (system, but chat-able) installs MCP, providers, and channels.
- Planner writes the DAG; Researcher does deep research; General Purpose executes tasks.
- Judge and Plan Supervisor stay hidden.

---

## 2. Roster

### 2.0 Stable roster identities

These are fresh-install identities, not migrations or type changes to existing records. General Purpose keeps the existing `worker` identity with a new display name. Admin is an operator role using persisted `core`, so the existing chat-target rules apply; “system operator” is a job description, not the hidden `system` runtime type.

| Display name | ID | Persisted type | API type | Chat target | Locked identity |
|---|---|---|---|---|---|
| Mia | mia | core | core | Yes | Yes |
| Jim | jim | core | core | Yes | Yes |
| Ava | ava | core | core | Yes | Yes |
| Admin | admin | core | core | Yes | Yes |
| Planner | planner | worker | Subagent | No | Yes |
| Researcher | researcher | worker | Subagent | No | Yes |
| General Purpose | worker | worker | Subagent | No | Yes |
| Judge | judge | system | system | No | Yes |
| Plan Supervisor | plansupervisor | system | system | No | Yes |

Ray, Max and Explorer are not seeded. Add Admin's compiled identity/prompt and retarget Worker's display/prompt; every seeded identity must resolve through the existing catalog. Hidden agents remain engine-only. Their exact fixed tools are Judge: `read_file`, `list_directory`, `grep`, `inspect_session`, `ToolSearch`, `Skill`; Supervisor: `plan_correct`, `grep`, `ToolSearch`, `Skill`. Judge's filesystem boundary is the reviewed workspace under existing path/mount policy; relevant-task selection is an instruction, not a new per-task file allowlist. Its session inspection retains the existing verifier-session restrictions.

### 2.1 Colleagues (chat)

| Agent | Job |
|---|---|
| **Mia** | Welcome. Personal assistant: files, library, email, tasks, light look-up, live browser with the user, and document generation using the execution tool. Heavy / multi-step work → Jim. |
| **Jim** | Orchestrator. Interviews (including planning questions). Runs the **plan engine**. Assigns Planner, Researcher, General Purpose. Approves the plan. Does not shell, does not author agents. |
| **Ava** | Team and skill author. Configure agents, models, **tool permissions**, **installed connector assignments**, **skills**, teams, and **workspace delegation relationships**. Author custom-agent instructions; respect built-in field protections (§2.6). Choose type at creation; create a new agent when a different type is needed. Present changes together and obtain one user confirmation (§5.2). Does **not** install connectors or run plans. |

### 2.2 Operator (core runtime, chat-able)

| Agent | Job |
|---|---|
| **Admin** | Harness: connector installation and credentials, providers, channels, doctor, usage, and system diagnostics. Dependency installation uses the user-approved application tool (§6.5), not an Admin-only route. Not on a workspace team. Kernel `set_config` / sandbox / tokens stay Settings UI. |

### 2.3 Staff (not chat)

| Agent | Job |
|---|---|
| **Planner** | Turn Jim’s brief into a task DAG. **define-goal on every task.** Declares each step’s files. May ask Jim if a hole blocks the DAG. Does not interview the user. |
| **Researcher** | Deep research only (search, fetch, cite, evidence bundle). No shell. Leaf. |
| **General Purpose** | Default **task runner**: files, shell, office files. Does **not** run the plan engine. May create helpers of the same General Purpose role only; no delegation to other roles. This does not mean any agent using the generic worker runtime. |

### 2.4 Engine (hidden)

| Agent | Job |
|---|---|
| **Judge** | Assess each criterion using the existing `met` / `unmet` verdict contract; the engine decides further work. Read the task’s activity record through `inspect_session` and seek relevant output files within the reviewed workspace, read-only; no new per-task filesystem allowlist is introduced. No execution or connector access. Instructions are editable; tools and skills are fixed, with **verify** as the only assigned skill. |
| **Plan Supervisor** | One `plan_correct` per wake. Instructions are editable; tool, connector, and skill assignments are fixed. Retain existing `grep` access within its existing file scope, alongside `plan_correct`, `ToolSearch`, and `Skill`. |

### 2.5 Out of the default box

Ray as a chat Scout. Max. Explorer. Staff for Mia or Ava. Jim holding bash / `serve_web`. Ava holding plan tools or `add_mcp_server`. Inherit-parent-tools when the target is a **different** seat (self-delegate is not that).

---

### 2.6 What users and Ava may edit

| Agent group | Instructions | Tools, installed connector access, and skills |
|---|---|---|
| Ordinary built-ins: Mia, Jim, Ava, Admin, Planner, Researcher, General Purpose | Built-in base prompts/instructions are protected. | User-editable, directly in Settings or through Ava’s confirmed proposal. Role lists are shipped defaults, not permanent locks. |
| Judge and Plan Supervisor | User-editable, directly or through Ava’s confirmed proposal. | Fixed capability sets. No execution or connector access for the Judge. This is the explicit exception to ordinary built-in editing. |
| Custom agents | Ava and the user may author and edit them. | Configurable, within the agent runtime’s supported capabilities and global permissions. External coding agents use their own runtime; do not claim they can load Omnipus skills or tools when they cannot. |

The same field rules apply in the screen, server requests, and agent tools. Reject protected-field changes clearly; do not silently drop them or unlock all fields together. Names and other built-in identity fields retain their existing protection unless expressly changed here. Fixed capabilities mean tools (including nested tool access settings), connector access, and skill assignments; this does not freeze existing editable model/provider or runtime-tuning fields. Judge/Supervisor identities are engine-owned and are not available as custom creation types. Agent type is chosen at creation and is not editable: a different type means a new agent, with any team changes included in Ava’s proposal.

Changing skill assignments intentionally changes which playbooks an ordinary built-in can use without modifying its base instructions. Capability edits must survive reload and restart, including deliberately empty skill/connector selections and edited Judge/Supervisor instructions. Startup seeding must not restore user-removed defaults. Preserve user choices in future releases unless the user explicitly chooses to reset them. Never automatically add new capability defaults to existing ordinary built-in agents. Judge/Supervisor capabilities remain engine-fixed, while their edited instructions are preserved. Implementing upgrade migrations is outside this greenfield scope.

---

## 3. Who talks to whom

```
User → Mia | Jim | Ava | Admin

Mia  → Jim (heavy work) · Ava (new teammate) · Admin (wire the box)
Jim  → Planner · Researcher · General Purpose
Jim  → Ava (delegate: configure needed team specialists or skills after conversational confirmation)
Any permitted native agent → environment_setup → actual user approval → application setup
Jim  → self (fork: same belt, narrower task)
Planner → Researcher (context before the DAG)
Planner → Jim only (questions), never the user
Researcher: no helpers
General Purpose → General Purpose helpers only
```

Simple work: Jim skips Planner and assigns Researcher or General Purpose directly.

A same-role helper is a child turn using the same seeded agent ID/configuration, not a new permanent agent. Permit self-delegation only for `jim` and `worker` through an explicit workspace self-edge, with existing allowed modes and the global/per-edge depth caps (global default 3). Seed these self-edges for eligible fresh workspace members; an omitted/removed edge refuses the helper. Exempt only these validated self-edges from graph cycle detection; continue rejecting other cycles, other self-edges, mode violations and depth overflow. This deliberately replaces the current unconditional self-delegation refusal and the corresponding graph validation, as agent orchestration work, not worktree isolation.

Ava can perform permitted configuration work in a direct or delegated session. Confirmation is conversational and prompt-governed: a delegated Ava sends any unconfirmed proposal through `message_parent`, Jim asks the user, and Ava can apply the approved proposal once that answer is relayed. The founder explicitly removed Ava-specific write restrictions based on delegation depth, unattended status or missing user-session identity. The general owner-session applicability of `AskUserQuestion` remains; no approval token or mandatory owner-session handoff is added. Ordinary tool permissions, revisions and protected fields still apply.

---

## 4. Interview vs plan vs execute

| Step | Who |
|---|---|
| Interview the user | **Jim** (open + planning questions), **Mia** (PA, light), **Ava** (agent/skill checklist), **Admin** (MCP/provider/channel checklist) |
| Write the brief | Jim |
| Decompose to a DAG + per-task goals | Planner |
| Declare each step’s files | Planner (Jim when he skips Planner) |
| Check the plan for overlapping work | plan-lint, at approve |
| Run / stop the plan | Jim (`create_plan`, `execute_plan`, `stop_plan`) |
| Do the task | General Purpose (default) or Researcher |
| Score done | Judge |
| Correct a stalled plan | Plan Supervisor |

---

## 5. Tool requirements

**Default capabilities:** give each working role a complete, practical tool list. Include supporting tools needed to inspect inputs, find existing configuration, execute the workflow, verify results, and return outputs. When a tool plausibly belongs to the role and does not violate an explicit boundary, prefer including it over leaving the agent unable to finish. Do not interpret this as granting unrelated administration, bypassing global permissions, or expanding the fixed Judge/Supervisor sets.

**Floor (every seat):** `Skill` (load), `ToolSearch`, included in shipped defaults. Ordinary built-in capability settings remain user-editable under §2.6; removing a needed tool must produce a clear unavailable-capability response, not an automatic hidden regrant.

Ordinary role tool exclusions and descriptions in this ADR describe shipped defaults, not immutable capability bans. Authorized user changes replace those local overrides within the global ceiling. Prompts must not claim a user-enabled supported tool remains denied solely because its role normally does not use it. GP helper-role restrictions and hidden engine scope remain enforced invariants.

The matrix below states shipped defaults. “—” means not granted by default. Enumerate concrete tools during implementation, including the supporting operations below. Permission resolution remains the existing global ceiling plus per-agent overrides: no new fallback layer or automatic per-agent deny backfill. An explicit role exclusion in this table must have a deliberately authored per-tool deny override wherever the global ceiling would otherwise allow it; omission alone is not denial. Keep these chosen overrides sparse rather than generating a deny entry for every tool. Connector eligibility follows §5.0: no binding means no access; ordinary users may subsequently change assignments within the global ceiling.

**External sending is Ask** by default for each working agent assigned it: author explicit Ask overrides for `send_email` and `reply`. Internal agent communication (`send_message`, `message_parent`, `switch_agent`) does not acquire an external-send approval prompt. Judge and Plan Supervisor stay mute.

The approved Admin installation capability requires changing the shipped `add_mcp_server` ceiling from Deny to Allow, with explicit Deny defaults for other built-ins and new custom agents; Admin alone ships with installation enabled. This follows the already confirmed Allow matrix, not a new recommendation to ask per install. User-set global Ask/Deny remains authoritative. Ava's approved one-question workflow similarly requires shipped Allow ceilings/defaults for configuration mutations including `delete_agent`, `install_skill`, `create_skill`, `edit_skill` and `remove_skill`; author other-role restrictions deliberately and preserve operator-set ceilings. Already-present Allow defaults need no change. Destructive-operation flags reflect the confirmed proposal, not a second user question.

| Group | Mia | Jim | Ava | Admin | Planner | Researcher | GP | Judge | Plan Supervisor |
|---|---|---|---|---|---|---|---|---|---|
| Talk / handoff / ask-user | Allow | Allow | Allow | Allow | send back | send back | send back | — | — |
| Memory | Allow | Allow | Allow | Allow | Allow | Allow | Allow | — | — |
| Files + library read **and write** | Allow | Read | Read | Setup files | Read | Read | Allow | Reviewed workspace, read-only; seek relevant outputs | Existing scoped `grep` only |
| Knowledge-base read | Allow | Allow | Allow | Allow | Allow | Allow | Allow | — | — |
| Knowledge-base write | Ask | Ask | — | — | — | — | Ask | — | — |
| Email + chat channels | Allow; send Ask | same | same | — | same | same | same | — | — |
| Web search / fetch | Allow | Allow | Allow | — | Allow | Allow | Allow | — | — |
| Browser (with the user) | Allow | Allow | Allow | — | — | — | — | — | — |
| Execution (`bash`) | Allow: document workflows | — | — | Allow: supported operator work | — | — | Allow | — | — |
| Dependency setup (`environment_setup`) | Ask | — | — | Ask | — | — | Ask | — | — |
| `serve_web` | — | — | — | — | — | — | Allow | — | — |
| Tasks | Allow | Allow | — | — | Allow | — | list/update/todos | — | — |
| **Plans** | — | **Allow** | **Deny** | — | — | — | **Deny** | — | — |
| Delegate | — | → Planner, Researcher, GP, self, Ava for configuration proposals | — | — | → Researcher | — | → GP helpers only | — | — |
| Agents, teams, skills, and capability configuration | find_skills | find/list | Proposal then one user confirmation (§5.2) | — | — | — | — | — | — |
| Installed connector discovery and assignment | — | — | Discovery Allow; assignment after confirmed proposal | Discovery/management only; no agent assignment | — | — | — | — | — |
| Connector installation / provider / channel / doctor | — | — | — | **Allow** (remove/disable Ask) | — | — | — | — | — |
| `inspect_session` / `plan_correct` | — | — | — | — | — | — | — | inspect | correct |
| `set_config` (kernel) | — | — | — | — | — | — | — | — | — |

Founder clarification during implementation: Jim also has knowledge-base write access, and Admin has read access. Read tools are `knowledge_describe`, `knowledge_find`, `knowledge_read` and `knowledge_list`. Write tools are `knowledge_edit`, `knowledge_restructure`, `knowledge_configure` and `knowledge_base_create`; Mia, Jim and General Purpose use the existing Ask policy for these operations. Other roles retain the table's boundaries. These are editable ordinary-role defaults, remain subject to global restrictions, and do not alter the global upfront tool set.

### 5.0 Connector assignment is enforced availability

Installed does not mean assigned. Native agents begin with no MCP execution access unless a concrete binding is explicitly seeded/assigned; new server installation grants none automatically. Admin can manage installed servers through management tools without implicitly executing their connector tools. For each call, effective connector availability is the intersection of target bindings and existing global/per-agent tool policy. Enforce it during discovery/registration and execution, including already loaded/stale definitions after unassignment. This is assignment eligibility like skills, not a third Allow/Ask/Deny resolver.

An omitted assignment field preserves state on update; explicit empty bindings mean no access. Within an assigned server, omitted tool selection means all of that server's policy-permitted tools; an explicit empty selection means none. The current binding persistence/selection collapses these states and does not enforce per-agent server binding at runtime: both must be corrected. No global tool-policy override can make an unassigned connector callable. Test the actual invocation after unbinding, not merely readback.

### 5.1 Ava’s complete configuration workflow

Ava must be able to perform the following through real registered tools, not just describe it in a skill:

| Capability | Required operations |
|---|---|
| Agents and models | List agents, read current configuration and applicable editable instructions, create agents with a chosen type, update supported settings, and select actual available models using `list_models`. A new type requires a new agent; do not change an existing type or silently delete its predecessor. |
| Tool permissions | Discover relevant tools and their current effective permissions; assign, change, and remove per-agent overrides. Show when a global restriction prevents a proposed grant. Configuring a tool for another agent does not require Ava to execute it herself. |
| Skills | Discover installed skills independently of Ava’s own execution allow-list; inspect, author, edit, install, assign, and remove as appropriate. Distinguish removing an assignment from deleting a shared skill. The fixed Judge/Supervisor assignments remain protected. |
| Connectors | List installed connectors and available tools without exposing secrets; inspect, assign, change, and remove agent access. Admin installs/configures missing connectors and handles credentials. |
| Teams | Read current workspace membership; add and remove members without losing unrelated members. |
| Delegation | Read and edit who may delegate to whom in the named workspace. Team membership alone does not prove the required delegation is possible. Do not recreate a global per-agent delegation field. |
| Readback and usability | Read the effective saved configuration and verify the teammate is usable in the intended workspace. If saving succeeds but activation fails, report that distinction and keep the configuration task incomplete. |

Extend `create_agent` / `update_agent` and the appropriate workspace configuration tools to cover these operations, consistently with the server field rules. Merely granting the existing tools is insufficient: their current parameters and blanket locked-agent rejection do not implement this workflow. Read-only discovery may proceed before confirmation.

### 5.2 Ava’s proposal and confirmation

For each configuration request, Ava shows one combined proposal: affected agents/workspace, what changes, tool/connector/skill grants and removals, and any deletion or shared impact. She asks for confirmation **once through the ordinary user-question tool**, with clear options to apply, change, or cancel. After confirmation, apply exactly that proposal and read back the result. A material change to the proposal needs a new user question; do not repeat the same confirmation for each write.

Do not use a separate approval tool for this workflow. Align Ava’s authoring/configuration tool defaults with this one-confirmation experience rather than also defaulting each proposed write to a second approval prompt. Existing operator-configured global restrictions remain authoritative and cannot be bypassed. Interrupted or partially failed operations must report what changed and what remains; do not report the whole proposal as applied.

Before applying a confirmed proposal, re-read the affected settings and check that its assumptions still hold. Preserve unrelated concurrent edits. If another edit changes the proposal's meaning or permissions, show the revised proposal and obtain a new confirmation rather than overwrite it. After interruption, read back what succeeded before proposing any remaining work; do not blindly replay creations or deletions. Validate the expected state atomically with each resource write under the existing entity/workspace lock; a separate pre-read does not close the race. Use the canonical opaque `revision` precondition defined in the configuration specification across agent, tools, skill and workspace mutations, including deletion; `updated_at` remains display metadata. Agent checks cover both entity and applicable instruction-file content under a shared lock. Stage both files before replacement and report any subsequent storage failure as actual partial state without activation; do not promise cross-file rollback or a cross-resource transaction framework.

### 5.3 General Purpose defaults

Give General Purpose explicit, practical task-runner tool defaults, including execution, files, document workflows, and same-type helpers. Express restrictions through the existing per-tool policy model. Do not introduce a separate deny-by-default fallback or automatically overwrite user choices when a tool is added.

### 5.4 Global upfront tool visibility

**Founder decision, 2026-09-17:** keep one global hardcoded classification by tool name. Do not introduce per-agent visibility settings or role-dependent visibility lists. Permissions remain per agent; initial visibility does not grant permission.

The following tools form the global upfront set. A registered, permitted tool in this set has its full callable definition in context from the first ordinary request; it does not require ToolSearch. Existing session applicability and goal-forcing restrictions still apply. `ToolSearch` retains its infrastructure classification; the table describes the combined upfront surface, not a requirement to move it into the ordinary full-tool map.

**User clarification, 2026-09-18:** ToolSearch discovery infrastructure is intentionally always available on compressed turns and cannot be denied. Permissions still govern which target tools discovery may reveal, load, or execute. Goal forcing may still withhold ToolSearch on a narrowed first-move request. This is not an automatic regrant of a denied target tool.

| Purpose | Upfront tools |
|---|---|
| Discovery and clarification | `ToolSearch`, `Skill`, `AskUserQuestion` |
| Files and execution | `bash`, `read_file`, `write_file`, `edit_file`, `append_file`, `list_directory`, `grep` |
| Locate inputs | `list_mounts`, `library_list`, `library_read`, `get_workspace` |
| Web research | `search_web`, `fetch_url` |
| Communication | `list_agents`, `send_message`, `switch_agent`, `message_parent`, `send_file` |
| Memory | `remember`, `recall_memory`, `recall_conversation` |
| Work tracking | `set_todos`, `list_tasks`, `list_jobs`, `create_task`, `update_task` |
| Goals | `set_goal`, `goal_claim` |
| Delegation and plans | `delegate`, `create_plan`, `execute_plan`, `stop_plan` |
| Hidden-agent essentials | `inspect_session`, `plan_correct` |

This is **37 unique tool names**, including ToolSearch. For example, upfront `bash` remains unavailable to an agent whose policy denies execution; `inspect_session` and `plan_correct` do not become available to ordinary agents merely because their names are in the global list.

Other permitted tools remain deferred under the existing discovery mechanism. Each built-in prompt must name that role’s standard tools, explain their purpose and normal workflow, and state:

> If a named tool is not currently callable, load it through ToolSearch using its exact name before using it. A prompt mention does not load or grant a tool. If access is denied or the tool is unavailable, report the limitation rather than claiming to have used it.

Examples: Ava’s prompt names agent, skill, installed-connector, team, and delegation configuration tools; Admin’s names connector installation, provider/channel configuration, and diagnostics; Mia’s names email and document workflows; browser-capable roles name their browser controls. Tools outside the global upfront list still need ToolSearch even when they are central to one role. Use real tool names and supported operations; update the prompt when the corresponding tool ships.

The selected skill’s body remains loaded on demand through the upfront `Skill` tool. Do not preload every skill or every connected server’s tool definitions. Keep definition assembly, text previews, and offered-call validation consistent with this single global classification.

**Calendar** in Omnipus = **scheduled tasks** (`create_task` / `update_task` with start, due, recurrence). No Google/Microsoft write in this requirements set. ICS read is out for now.

---

## 6. Skills

A skill is a playbook loaded with the `Skill` tool. It is **not** a standing rule (those go in the soul) and **not** a tool.

### 6.1 Ship in the default box

| Skill | Default assignment (ordinary built-ins are editable) |
|---|---|
| **interview** | Jim, Mia, Ava, Admin — **same skill, four checklists** |
| **handoff** | Mia |
| **orchestrate** | Jim — future isolation guidance is gated by §7 |
| **plan** | Jim, Planner, Plan Supervisor — future isolation guidance is gated by §7 |
| **define-goal** | Planner, Jim, Mia, Plan Supervisor — authors and correctors of task/plan goals |
| **deep-research** | Researcher |
| **agent-authoring** | Ava |
| **skill-authoring** | Ava |
| **tool-mapping** | Ava |
| **skill-mapping** | Ava |
| **delegation-graph** | Ava |
| **workspace-team** | Ava |
| **mcp-install** | Admin |
| **provider-setup** | Admin |
| **channel-setup** | Admin |
| **doctor** | Admin |
| **verify** | Judge only (pin the allow-list) |
| **inbox-triage** | Mia |
| **elicify-docx** | Mia, General Purpose |
| **elicify-xlsx** | Mia, General Purpose |
| **elicify-pptx** | Mia, General Purpose |
| **elicify-pdf** | Mia, General Purpose |

The four document-authoring capabilities use original Elicify skills from elicify-ai/elicify-Skills, with their packaged helpers and declared dependencies. Their canonical installed skill IDs are the four elicify-* names in the table. Mia and General Purpose run them through the execution tool. A separate native Office engine is not required; see §6.5.

### 6.2 Interview checklists

| Who | Kind | Checklist |
|---|---|---|
| **Jim** | Open + **planning** | Outcome; done; in/out; constraints; **dependencies, parallel work, already done, risks, who should research vs make**; **which repository, and which pieces change its files at the same time**; context. Then write the brief → Planner. |
| **Mia** | Open, PA-scoped | What you need now; inbox vs files vs reminder vs “this is a project” (→ Jim). Few questions, not a spec grill. |
| **Ava** | Specific | Purpose; type for a new agent; editable fields for an existing agent; tools allow/ask/deny; installed connectors; skills; team/workspace; who may delegate to whom; available model via `list_models`; one combined proposal and confirmation. |
| **Admin** | Specific | MCP vs provider vs channel; which server/app; where the secret lives; test vs enable; MCP leaves the sandbox. |

### 6.3 Drop / do not seed

`daily-briefing`, `summarize` (external CLI), `browser-with-user` (browser is a tool), `execute-task` (General Purpose soul), `web-research` as a skill (tools; the playbook is **deep-research**), `untrusted-content` (standing soul/tool rule, not a skill).

### 6.4 Already in the binary (keep, retarget)

`plan`, `define-goal`, `skill-authoring`. Retarget `skill-authoring` at `create_skill` / `edit_skill` (not old `system.skill.*` names). Proposal-first for skill writes. Keep shipped skill text aligned with available tools; future isolation guidance is covered by §7.

---

### 6.5 Document generation and approval-gated environment setup

**Founder amendment, 2026-09-18:** dependency setup belongs to an application tool, not to Admin, an agent handoff, or a delegation graph. This replaces the original D5 setup route. Workspace-local installation is the default; compatible runtimes and native tools can be shared through an Omnipus-managed, versioned installation. Dependency names, versions and installation instructions come from the agent’s task plan or skills; Omnipus must not hardcode a library/application catalogue or package-specific installation recipes. This is a generic installation facility, not a document-specific installer. Actual operating-system and privilege constraints still apply.

**Founder clarification, 2026-09-18 — option A:** the tool is Bash with enhanced, bounded installation permission. Reuse existing execution, background sessions, poll/read/kill and platform process controls. Do not build a separate supervisor to guarantee cleanup of deliberately detached processes. Such processes retain the inherited sandbox restrictions but have the same cleanup limitations as Bash; neither a completed command nor shared publication proves all detached children have stopped. Installation commands should wait for their work to finish, and results must state the actual outcome without promising stronger cleanup.

Add deferred tool `environment_setup` with its normal tool permission set to **Ask**. That existing tool approval is the installation approval. There is no separate approval workflow, immutable approval token, expiry protocol, second confirmation or special God-mode handling. Existing policy resolution and user overrides apply; do not introduce a new authorization layer. The normal tool call describes dependencies, reason, workspace and requested workspace/shared scope so the user knows what they are approving. Denied approval executes no installation. Reusing installed dependencies through a readiness check needs no setup call.

Omnipus provides generic installation execution with existing bounded setup/sandbox access. Founder clarification: no hardcoded libraries or application-specific recipes. The founder selected agent-supplied installation commands or scripts, shown in the existing Ask approval and executed within the selected installation area. Earlier structured-package-only and recipe-specific wording is superseded. No unrestricted host-privilege grant is implied. Project libraries and caches are workspace-local by default. Shared runtimes/native tools are application-managed and read/execute-only for agents. Dependency hooks remain untrusted code; setup does not grant ordinary agents access to other workspaces, gateway secrets, host administrator rights, normal execution network access or agent tools. Unsupported privilege/platform/licence requirements fail visibly rather than retrying unsandboxed.

All permitted native agents, including newly created custom agents without delegation edges, use the same route. Seed environment_setup as Ask for Mia, General Purpose and Admin; Deny for Jim, Ava, Planner and Researcher; and locked Deny for Judge and Plan Supervisor. Founder clarification: both existing levels must be Ask — global Ask plus explicit per-agent Ask for Mia, General Purpose, Admin and new custom native agents, independently of execution or skills. Preserve that explicit posture in sparse-policy reconciliation. Creation and permission previews must agree. It stays discoverable through ToolSearch, outside the unchanged upfront set. Preserve user overrides on update. Skill assignment never grants setup, execution or file-reading permissions.

Package the original Elicify document skills and referenced helpers. The document skills specify compatible Python and authoring libraries, LibreOffice, a PDF-to-image renderer, and fonts; these are skill/workflow requirements and acceptance fixtures, not a tool-side package catalogue; include Node or build tools only where the selected workflow needs them. Generation and visual-check dependencies are separate so an existing authoring environment remains usable if rendering is unavailable. The optional runtime does not become an Omnipus startup dependency.

The requesting agent checks its actual sandbox, requests setup if necessary, waits for user approval and completion, then rechecks its own environment. Render Office files through LibreOffice to temporary PDF and page/sheet/slide images, inspect the images with read_file, correct defects and rerender. PDF is an internal inspection intermediate; the editable Office file remains the deliverable. Successful installation, converter --version, text extraction, or image creation alone never proves visual inspection. Missing rendering or model vision leaves visual validation explicitly incomplete. No document-library preview UI is added.

The [environment setup specification](../specs/adr-090-environment-setup-spec.md) defines the tool contract, existing Ask integration, installation boundaries and acceptance tests. Skills give portable dependency requirements; Omnipus supplies setup guidance without naming privileged agents.

Admin uses its cross-workspace filesystem authority to select a target workspace without team membership; each installation remains confined to its selected destination. Installation runs asynchronously using the existing Bash-style background session pattern (session_id, poll/read/kill). Starting a session is not completion. Unattended Ask requests retain existing automatic denial; no pending-approval queue or new approval workflow is added. See ES-FR-02–05 and ES-BDD-09–11.

### 6.6 Visual inspection through the read tools

**Founder decision:** extend `read_file` to return supported local images as actual visual input to the model. `library_read` must expose the same behavior through its existing reader wrapper. Do not add a separate image-viewing tool or change the global upfront list. These tools currently return text or reject binary files; generic image handling elsewhere does not already implement this requirement.

Reuse the existing media processing, image normalization, size limits, and model-capability checks. Preserve the readers' filesystem permissions, workspace boundaries, and audit behavior. Reading an image is inspection: it must not automatically send the file to the user, start a web server, or require a browser. File delivery remains a separate `send_file` action. Text pagination and document text extraction retain their existing behavior; reject explicit image pagination with a clear explanation rather than returning partial image bytes.

At minimum, valid PNG and JPEG files must be inspectable. Other formats follow the existing normalizer's supported formats and limits. Invalid, unsupported, inaccessible, or oversized images must produce an accurate explanation without claiming visual inspection. If normalization resizes an image, tell the model it received a resized view so it can use execution to create and read a crop when fine details matter.

Ensure image content survives every provider conversion used for supported vision models, including images returned by tools. The current Responses and Anthropic tool-result paths omit image media and require correction. Apply the existing capability handling consistently to read-tool images and other tool-produced images; do not bypass it by attaching pre-encoded image data directly. For a model without image support, preserve file access and provide the existing guidance to switch models. Do not silently switch models or claim that text extraction completes visual inspection.

Inspection images are transient to the active turn and its existing request retries. Durable session/transcript/archive records retain only text metadata (source identity/path, content hash and dimensions), not inspection image bytes or retained snapshot refs. On later replay/compaction, show “image not retained; re-read to view”; a fresh read applies current filesystem permissions. This chooses the existing allowed unavailable-evidence outcome instead of adding a retention/revocation subsystem. Explicit user uploads and file-delivery history retain their existing behavior. All retained native provider adapters, including optional Bedrock, must be inventoried; external CLI transports without a native image-result path return an explicit unsupported-route limitation.

Mia and General Purpose's document skills must render pages or sheets as needed, read the resulting images, check layout and readability, correct detected defects, and inspect the revised output before delivery. If required visual validation cannot be completed, explicitly report it and keep that validation incomplete. Browser permissions are not a prerequisite for this document workflow.

Verify the complete route with actual rendered pages containing known defects such as clipped text or overlapping elements. Automated checks must cover image preservation in provider requests, capability handling, file-access denial, corrupt/oversized images, no automatic user delivery, and unchanged text/document reading. A live vision-model workflow must demonstrate detection and correction of known visual defects; successful image creation or a text-only tool response is not proof.

---

## 7. Future isolation guidance for prompts and skills

Worktree isolation is a **separate feature, outside this work**. Its design reference is ADR-082 v10 on `feat/lsp-and-native-worktrees`; the isolation feature owns its implementation and authoritative usage contract.

This document retains only the following prompt/skill integration notes. Activate isolation-specific instructions **after the separate capability ships**, using its actual supported arguments and behavior. Until then, do not teach agents to request unavailable isolation or claim it happens automatically. Repository and parallel-work questions remain useful for planning today.

| Prompt or skill | Guidance when isolation is available |
|---|---|
| **Jim / interview** | Identify the repository and tasks that may write to it concurrently. |
| **Planner / plan** | Use the delivered isolation capability where appropriate and declare each task’s files. Repository writes include documentation and configuration as well as code. |
| **Jim / orchestrate** | Request supported isolation for parallel repository-writing helpers, following the separate feature’s usage contract. |
| **General Purpose instructions** | Work in the assigned directory. When assigned an isolated copy, leave merge handling to the engine and follow the delivered feature’s helper rules. |

No isolation engine, validation rules, retry policy, merge handling, or Stop behavior is to be implemented as part of this agents-and-skills work.

---

## 8. On-the-fly labour (not Ava)

| Claude Code | Omnipus |
|---|---|
| Spawn general-purpose | `delegate` **General Purpose** |
| Spawn a research/plan preset | `delegate` **Researcher** / **Planner** |
| Fork | `delegate` **to self** (same belt) |

Ava is for a **new kind of seat**, not every task. Day-to-day fan-out does not wait on `create_agent`.

---

## 9. Packs

Default box does **not** include domain packs. See vault **Feature design — Workspace packs**. First-party candidates: Software, Data, Content, Sales, Finance, Legal, Support. Staff + skills + named MCP recipes; no extra chat colleagues.

---

## 10. Implementation scope and acceptance

1. Seed the greenfield roster and role-complete tool defaults. No old-roster migration or compatibility code.
2. Implement the field rules in §2.6 consistently in the screen, server, agent tools, and startup behavior. Replace conflicting older field-matrix requirements and tests; preserve unrelated restrictions.
3. Complete Ava’s tool operations and one-question proposal workflow (§5.1–5.2), including team and workspace delegation configuration.
4. Write/adapt the role skills; package the selected document skills and their dependencies; omit the retired skills in §6.3. Keep built-in prompts consistent with executable capabilities.
5. Give Mia document execution and all permitted native agents the approval-gated environment_setup route (§6.5). Verify custom-agent use without delegation edges and actual sandbox readiness.
6. Set the fixed Judge/Supervisor capabilities, editable instructions, and the Judge’s read-only task-evidence access.
7. Implement the global upfront set and role-prompt discovery instructions in §5.4. Keep the existing global classification model; add no per-agent visibility setting.
8. Keep future isolation guidance gated by §7. Isolation implementation is not part of delivery.
9. Implement image inspection through `read_file` and `library_read`, using existing media/capability handling and correcting provider image delivery (§6.6). No separate image tool or browser dependency.

Completion requires appropriate automated checks **and real workflows**:

| Workflow | Required observed result |
|---|---|
| Tool visibility and discovery | In a fresh session, permitted tools from the 37-name global set are callable without ToolSearch, including Skill, questions, execution, and hidden-agent essentials in their valid scopes. A role-specific deferred tool is named in its prompt, loaded by exact name through ToolSearch, and then successfully used. Denied/unregistered tools are not offered or made usable by a prompt mention. |
| Ava builds a teammate | One combined proposal, one user-question confirmation, then actual creation, model/tool/skill/connector configuration, team membership, and workspace delegation. Jim can assign the resulting teammate a task and it can use its configured capabilities. |
| Ava changes existing settings | Read before editing, preserve unrelated settings, support deliberate removals/empty lists, and accurately report partial failure or unavailable live activation. A cancelled proposal changes nothing. A concurrent material edit, including one inserted after the preliminary read but before the locked write, rejects that resource mutation and triggers a revised proposal; earlier successful resources are reported and resumption does not duplicate completed mutations. |
| Built-in editing | Through both Settings and Ava, ordinary built-in prompts stay protected while tools/connectors/skills can change. Judge/Supervisor instructions can change while their capability sets stay fixed. |
| Persistence | Save, reload, and restart preserve permitted edits, edited Judge/Supervisor instructions, and empty selections. No migration or old-version upgrade test is required in this greenfield scope. |
| Global permissions | An agent-level grant cannot override a global denial. A user who removes a necessary capability gets a clear explanation rather than a hidden regrant. With global bash Allow, fresh Jim still cannot execute because his shipped Deny is explicit; an authorized subsequent user removal of that override follows the existing ceiling. |
| Mia’s document outputs | Generate valid, openable Word, spreadsheet, presentation, and PDF files using the actual assigned skills and execution environment; inspect/render/validate as their workflows require and return the files. |
| Visual inspection | Mia and General Purpose read rendered PNG/JPEG pages through the read tools without browser access or automatic user delivery. Images reach each supported vision-provider request. A live model identifies known visual defects and checks corrected output. Unsupported models and corrupt, oversized, or inaccessible files produce accurate limitations; existing text/document reading still works. |
| Missing dependency | Any permitted native agent requests environment_setup; the user approves the setup tool call through Ask; application setup completes; the requesting agent verifies its sandbox and resumes. Rejection changes no installation state. Unsupported setup remains explicit and incomplete. |
| Agent type and delegation | Ava creates a new agent when another type is needed. General Purpose may create helpers of its own role but cannot delegate to another role; merely sharing the generic worker runtime is insufficient. |
| Judge | Reads the relevant task activity and output files; cannot execute commands or access connectors. Its editable instructions are used after reload. |

Record code-check results separately from proof that a user or agent can actually complete these workflows. Neither a skill file nor a successfully saved agent record alone is evidence of delivery.

## 11. Delivery summary

| Area | Existing foundation | Required change | Completion evidence |
|---|---|---|---|
| Built-in editing | Settings, configuration APIs, seed machinery | Field-level protection: ordinary instructions fixed/capabilities editable; Judge/Supervisor inverse | Settings and Ava both enforce the matrix; reload/restart preserve edits |
| Ava | Agent/skill tools and workspace configuration | Complete discovery and mutation operations; one confirmed proposal; effective readback and conflict handling | A created/configured teammate can receive and complete a task |
| Role defaults | Global ceiling and per-agent overrides | Complete role tool maps and aligned fixed prompts; GP helpers limited to their own role | Permitted workflows succeed; explicit boundaries remain enforced |
| Tool visibility | Global manifest and ToolSearch | Adopt the exact 37-name list; prompts name deferred role tools | Fresh-session availability and exact-name discovery checks |
| Document skills | Execution environment and skill loader | Package selected skills and assets; approval-gated environment_setup and sandbox-compatible dependencies | Mia and GP produce usable files; missing-dependency recovery is demonstrated |
| Image reading | Generic media processing and model-capability handling | Image return in existing readers, inspection-only delivery, provider preservation | Real visual defect detection/correction and negative-path checks (§6.6) |
| Hidden agents | Native Judge/Supervisor execution and evidence tools | Editable instructions with fixed capabilities; relevant Judge output access | Existing verdict/correction contracts and scope restrictions continue to hold |
| Isolation | Separate feature design | Future prompt/skill guidance only | No isolation engine or merge/retry implementation in this delivery |

This table describes planned delivery, not completed runtime functionality. The companion decisions record is the interview history; this ADR is the architectural authority, and its linked specifications define testable implementation contracts. Historical review and tool-analysis recommendations are superseded wherever they differ from these confirmed requirements.


## 12. Alternatives and rationale

| Decision | Chosen approach and reason | Alternatives declined |
|---|---|---|
| D1 | Named working roles and fixed hidden engine roles keep delegation understandable and preserve existing engine contracts. | Retaining retired Ray/Max defaults; unrestricted GP cross-role delegation. |
| D2 | Enforce editability per field at every mutation surface, preserving user configuration across restarts. | Whole-record locks prevent legitimate capability changes; unlocking every field would expose protected prompts. |
| D3 | Extend existing tools and workspace configuration; one normal user-question confirmation covers the exact proposal. Re-read and report partial application. | A separate approval tool or confirmation per write; a new cross-resource transaction framework; silent rollback of user changes. |
| D4 | Keep the two-layer permission model and one global hardcoded visibility list. ToolSearch loads remaining allowed tools. | Per-agent visibility settings or a third policy fallback layer add unnecessary concepts. |
| D5 | Use selected document skills; application-owned environment_setup installs workspace packages/shared runtimes through existing Ask tool approval. | Rewriting all skills to Python only, inventing a native Office engine, or promising dependencies unavailable in the actual runtime. |
| D6 | Add visual images to existing read tools and connect existing media/capability/provider paths. Reading does not deliver a file to the user. | A separate view-image tool expands the surface; browser-serving workarounds require unrelated permissions; base64 text alone is not vision input. |
| D7 | Gate future isolation instructions on the separate feature's delivered contract. | Bundling isolation engine, merge/retry, or Stop changes into the agent roster. |

## 13. Consequences, operation, and recovery

This changes backend validation, configuration tools, startup seeding, Settings controls, prompts, packaged skills, and provider image conversion. A UI-only edit is insufficient. Existing filesystem, authentication, global-permission, and credential boundaries remain authoritative. Protected and malformed fields are rejected rather than silently ignored. A failed read or configuration activation is visible to the agent and user.

Configuration changes take effect through the existing runtime refresh path; saved-but-inactive state must be reported distinctly. Document dependency failures remain pending until verified setup. Image capability failures preserve file access and give guidance, without fabricating successful inspection. Existing audit and tool records must identify the operation and outcome without exposing connector secrets or dumping image base64 into ordinary text logs.

This is a greenfield release change, not a migration or rollback programme for earlier installations. Delivery must distinguish spec review, automated code checks, and actual user/agent reachability. Implementation may be delivered in the three linked specifications below; the feature is not declared delivered until all applicable acceptance criteria pass.

## 14. Relationship to prior decisions

This ADR supersedes older whole-record built-in write locks and seed/prompt defaults only where they conflict with §2.6 and the role tables. It replaces the full/deferred name selection in ADR-071 — Tool manifest tier redesign with §5.4, while retaining its discovery mechanism. It preserves ADR-077 — Two-layer tool policy's global ceiling and per-agent tightening; a local Allow never defeats global Ask or Deny.

It extends ADR-051 revision 4 — Workspace media library and presentation layer with a private read-tool image path and consistent tool-media capability handling. It preserves ADR-084 — Judge as an active reviewer's criterion verdict/workspace evidence scope and ADR-055 — Plan supervisor's correction lifecycle; only instruction editability and explicit capability defaults change as stated here. This intentionally supersedes the self-edge/self-delegation prohibition for only the two bounded helper roles, the shipped `add_mcp_server` Deny for the explicitly scoped Admin workflow, and ADR-084 JUDGE-FR-059's empty skill allowlist with the fixed `verify` assignment. Plan Supervisor retains both `plan` and `define-goal`.

The approved Python/Node document workflows are an explicit exception to the root “no new runtime deps” wording for optional external document-execution prerequisites; the Omnipus Go binary itself remains standalone and gains no mandatory startup dependency. The implementation must provide the workspace package paths, shared runtime paths and actual sandbox probes defined in the environment setup specification, so an approved installation is usable by any permitted native agent, including custom agents. The root operating rules annotate these planned exceptions alongside the current runtime baseline; implementation must update the baseline statements when delivered. Historical ADRs remain unchanged as an audit trail. Where an older editable-field matrix conflicts, this ADR governs these built-in roles; unrelated custom/external runtime restrictions remain.

## 15. Specification split and open decisions

1. **[Agent configuration and skills](../specs/adr-090-agent-configuration-and-skills-spec.md):** roster, field protection, Ava, tools/visibility, prompt/skill packaging, document execution and approval-gated setup.
2. **[Visual file reading](../specs/adr-090-visual-file-reading-spec.md):** reader image content, inspection-only delivery, capability handling, provider conversion, and visual validation.
3. **[Environment setup](../specs/adr-090-environment-setup-spec.md):** workspace packages, shared runtimes, existing Ask tool approval, sandbox readiness and Office dependencies.

The second specification supplies the visual acceptance required by the first; it can be developed independently against the existing read interface. All three reuse existing runtime facilities; environment setup extends the approval and provisioning paths. The founder resolved the package source choice to original Elicify skills (L1 below). Exact supporting tool maps remain engineering inventory work governed by the approved role boundaries, not permission to omit capabilities.

### Resolved decision L1 — original Elicify document skills

During source verification on 2026-09-17, the four upstream `skills/docx`, `skills/xlsx`, `skills/pptx` and `skills/pdf` LICENSE.txt files at Anthropic skills commit `34040c9c568585f6929bedeaad110ad08f079624` all explicitly restrict copying, derivative works and redistribution. Their presence in a public repository is not evidence that Omnipus may bundle them. [Pinned license evidence](https://github.com/anthropics/skills/blob/34040c9c568585f6929bedeaad110ad08f079624/skills/docx/LICENSE.txt).

Daniel resolved L1 on 2026-09-17: author original Elicify document skills after online comparison, add them to [elicify-ai/elicify-Skills](https://github.com/elicify-ai/elicify-Skills), and plan intensive testing. Select `elicify-docx`, `elicify-xlsx`, `elicify-pptx` and `elicify-pdf`; do not copy or adapt Anthropic prompts, scripts or assets. Python is the initial authoring route; Node remains available where a workflow needs it. The source choice is closed. Package pinning, explicit Elicify-approved public distribution terms, dependency provisioning and full Omnipus acceptance remain release requirements. The private skill repository’s existing license is not silently changed by this decision.

The original package implementation, comparative source research and intensive test plan are available in [Elicify Skills PR 1](https://github.com/elicify-ai/elicify-Skills/pull/1). Its isolated document pilot is package evidence, not evidence that Omnipus provisioning or runtime integration has shipped.
