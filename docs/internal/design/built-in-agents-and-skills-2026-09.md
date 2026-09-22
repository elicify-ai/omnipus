# Built-in agents and skills — requirements

**Status:** founder decisions confirmed 2026-09-17; requirements consolidated, implementation pending
**Document role:** consolidated product requirements on the release lineage; detailed implementation tasks follow this document.
**Supersedes:** the five-core README story (Mia, Jim, Ava, Ray, Max) and the compiled seed where it disagrees.
**Related:** vault note `Feature design — Workspace packs`; ADR-082 v10 (worktree isolation, branch `feat/lsp-and-native-worktrees`).

This is the confirmed requirements input. Its decisions are now recorded in [ADR-090](../architecture/ADR-090-built-in-agents-skills-and-visual-reading.md); use that ADR and its implementation specifications where review clarifications extend this historical requirements capture. Packs are extra. Code today does not yet match the target behavior.

**Scope:** built-in agents, their capability assignments, prompts, and skills. **Greenfield only: no migrations or compatibility work for existing installations.** Ray and Max are absent from the new default roster; this work does not migrate, replace, or delete historical agents. Worktree isolation and other coding-engine features are separate work. Section 7 records only future prompt/skill guidance for using isolation once that capability is delivered; it is not a dependency for delivering this roster and its skills.

---

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

### 2.1 Colleagues (chat)

| Agent | Job |
|---|---|
| **Mia** ⭐ | Welcome. Personal assistant: files, library, email, tasks, light look-up, live browser with the user, and document generation using the execution tool. Heavy / multi-step work → Jim. |
| **Jim** | Orchestrator. Interviews (including planning questions). Runs the **plan engine**. Assigns Planner, Researcher, General Purpose. Approves the plan. Does not shell, does not author agents. |
| **Ava** | Team and skill author. Configure agents, models, **tool permissions**, **installed connector assignments**, **skills**, teams, and **workspace delegation relationships**. Author custom-agent instructions; respect built-in field protections (§2.6). Choose type at creation; create a new agent when a different type is needed. Present changes together and obtain one user confirmation (§5.2). Does **not** install connectors or run plans. |

### 2.2 Operator (system, chat-able)

| Agent | Job |
|---|---|
| **Admin** | Harness: connector installation and credentials, providers, channels, doctor, usage, and setup of missing document-generation dependencies. Must have a working setup route, including execution and supporting file tools where needed (§6.5). Not on a workspace team. Kernel `set_config` / sandbox / tokens stay Settings UI. |

### 2.3 Staff (not chat)

| Agent | Job |
|---|---|
| **Planner** | Turn Jim’s brief into a task DAG. **define-goal on every task.** Declares each step’s files. May ask Jim if a hole blocks the DAG. Does not interview the user. |
| **Researcher** | Deep research only (search, fetch, cite, evidence bundle). No shell. Leaf. |
| **General Purpose** | Default **task runner**: files, shell, office files. Does **not** run the plan engine. May create helpers of the same General Purpose role only; no delegation to other roles. This does not mean any agent using the generic worker runtime. |

### 2.4 Engine (hidden)

| Agent | Job |
|---|---|
| **Judge** | Assess each criterion using the existing `met` / `unmet` verdict contract; the engine decides further work. Read the task’s activity record through `inspect_session` and its relevant output files, read-only. No execution or connector access. Instructions are editable; tools and skills are fixed, with **verify** as the only assigned skill. |
| **Plan Supervisor** | One `plan_correct` per wake. Instructions are editable; tool, connector, and skill assignments are fixed. Retain existing `grep` access within its existing file scope, alongside `plan_correct`, `ToolSearch`, and `Skill`. |

### 2.5 Out of the default box

Ray as a chat Scout. Max. Staff for Mia or Ava. Jim holding bash / `serve_web`. Ava holding plan tools or `add_mcp_server`. Inherit-parent-tools when the target is a **different** seat (self-delegate is not that).

---

### 2.6 What users and Ava may edit

| Agent group | Instructions | Tools, installed connector access, and skills |
|---|---|---|
| Ordinary built-ins: Mia, Jim, Ava, Admin, Planner, Researcher, General Purpose | Built-in base prompts/instructions are protected. | User-editable, directly in Settings or through Ava’s confirmed proposal. Role lists are shipped defaults, not permanent locks. |
| Judge and Plan Supervisor | User-editable, directly or through Ava’s confirmed proposal. | Fixed capability sets. No execution or connector access for the Judge. This is the explicit exception to ordinary built-in editing. |
| Custom agents | Ava and the user may author and edit them. | Configurable, within the agent runtime’s supported capabilities and global permissions. External coding agents use their own runtime; do not claim they can load Omnipus skills or tools when they cannot. |

The same field rules apply in the screen, server requests, and agent tools. Reject protected-field changes clearly; do not silently drop them or unlock all fields together. Names and other built-in identity fields retain their existing protection unless expressly changed here. Agent type is chosen at creation and is not editable: a different type means a new agent, with any team changes included in Ava’s proposal.

Changing skill assignments intentionally changes which playbooks an ordinary built-in can use without modifying its base instructions. Capability edits must survive reload and restart, including deliberately empty skill/connector selections and edited Judge/Supervisor instructions. Startup seeding must not restore user-removed defaults. Preserve user choices in future releases as a product principle; implementing upgrade migrations is outside this greenfield scope.

---

## 3. Who talks to whom

```
User → Mia | Jim | Ava | Admin

Mia  → Jim (heavy work) · Ava (new teammate) · Admin (wire the box)
Jim  → Planner · Researcher · General Purpose
Jim  → Ava (handover: missing specialist — user joins)
Jim  → self (fork: same belt, narrower task)
Planner → Researcher (context before the DAG)
Planner → Jim only (questions), never the user
Researcher: no helpers
General Purpose → General Purpose helpers only
```

Simple work: Jim skips Planner and assigns Researcher or General Purpose directly.

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

The matrix below states shipped defaults. “—” means not granted by default. Enumerate concrete tools during implementation, including the supporting operations below. Permission resolution remains the existing global ceiling plus per-agent overrides: no new fallback layer or automatic per-agent deny backfill.

**Send on channels and email is Ask** by default for every working agent who has them (Mia, Jim, Ava, Planner, Researcher, General Purpose). Judge and Plan Supervisor stay mute.

| Group | Mia | Jim | Ava | Admin | Planner | Researcher | GP | Judge | Plan Supervisor |
|---|---|---|---|---|---|---|---|---|---|
| Talk / handoff / ask-user | Allow | Allow | Allow | Allow | send back | send back | send back | — | — |
| Memory | Allow | Allow | Allow | Allow | Allow | Allow | Allow | — | — |
| Files + library read **and write** | Allow | Read | Read | Setup files | Read | Read | Allow | Relevant task outputs, read-only | Existing scoped `grep` only |
| Email + chat channels | Allow; send Ask | same | same | — | same | same | same | — | — |
| Web search / fetch | Allow | Allow | Allow | — | Allow | Allow | Allow | — | — |
| Browser (with the user) | Allow | Allow | Allow | — | — | — | — | — | — |
| Execution (`bash`) | Allow: document workflows | — | — | Allow: dependency setup | — | — | Allow | — | — |
| `serve_web` | — | — | — | — | — | — | Allow | — | — |
| Tasks | Allow | Allow | — | — | Allow | — | list/update/todos | — | — |
| **Plans** | — | **Allow** | **Deny** | — | — | — | **Deny** | — | — |
| Delegate | — | → Planner, Researcher, GP, self | — | — | → Researcher | — | → GP helpers only | — | — |
| Agents, teams, skills, and capability configuration | find_skills | find/list | Proposal then one user confirmation (§5.2) | — | — | — | — | — | — |
| Installed connector discovery and assignment | — | — | Discovery Allow; assignment after confirmed proposal | Allow | — | — | — | — | — |
| Connector installation / provider / channel / doctor | — | — | — | **Allow** (remove/disable Ask) | — | — | — | — | — |
| `inspect_session` / `plan_correct` | — | — | — | — | — | — | — | inspect | correct |
| `set_config` (kernel) | — | — | — | — | — | — | — | — | — |

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

Before applying a confirmed proposal, re-read the affected settings and check that its assumptions still hold. Preserve unrelated concurrent edits. If another edit changes the proposal's meaning or permissions, show the revised proposal and obtain a new confirmation rather than overwrite it. After interruption, read back what succeeded before proposing any remaining work; do not blindly replay creations or deletions.

### 5.3 General Purpose defaults

Give General Purpose explicit, practical task-runner tool defaults, including execution, files, document workflows, and same-type helpers. Express restrictions through the existing per-tool policy model. Do not introduce a separate deny-by-default fallback or automatically overwrite user choices when a tool is added.

### 5.4 Global upfront tool visibility

**Founder decision, 2026-09-17:** keep one global hardcoded classification by tool name. Do not introduce per-agent visibility settings or role-dependent visibility lists. Permissions remain per agent; initial visibility does not grant permission.

The following tools form the global upfront set. A registered, permitted tool in this set has its full callable definition in context from the first ordinary request; it does not require ToolSearch. Existing session applicability and goal-forcing restrictions still apply. `ToolSearch` can retain its infrastructure classification; the table describes the combined upfront surface, not a requirement to move it into the ordinary full-tool map.

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
| **define-goal** | Planner, Jim, Mia — anyone who **creates** a task or plan |
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
| **author-document** | Mia, General Purpose |
| **author-spreadsheet** | Mia, General Purpose |
| **author-presentation** | Mia, General Purpose |
| **author-pdf** | Mia, General Purpose |

The four document-authoring capabilities use the selected Anthropic document skills with their existing Python and Node.js scripts, helper files, and dependencies. Mia and General Purpose run them through the execution tool. A separate native Office engine is not required; see §6.5.

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

### 6.5 Document generation and dependency setup

Use the chosen document skills as supplied rather than rewriting them to Python only. Package their referenced helper scripts and assets, and make the required Python/Node.js runtimes and libraries available in the actual execution environment. Include conversion, rendering, and validation programs required by the chosen workflows; a runtime installed elsewhere on the host is not proof the agent can use it. Record the selected skill versions and dependency requirements during implementation.

Mia can read inputs, write scripts and output files, run the generation and validation commands, inspect the results, and return the actual document to the user. Her instructions must permit this workflow; do not retain a blanket instruction to refuse execution. General Purpose has the same document-generation capability. Both use the existing execution environment and permission model.

If required software is missing, Mia explains what is missing and hands setup to Admin, keeping the document task pending. Admin must have a working setup route with the necessary execution/file access and installation permissions; a `doctor` report alone is not setup. Respect host/environment boundaries and report unsupported installation clearly. Resume generation only after checking the dependency in the agent’s actual environment; do not ask the user to manually complete a task Admin has claimed to handle.

General Purpose reports missing dependencies to its parent/Jim, who requests Admin setup through the existing handoff route. This does not grant General Purpose delegation to Admin or other roles. Its document work remains pending until it verifies the dependency in its own execution environment.

### 6.6 Visual inspection through the read tools

**Founder decision:** extend `read_file` to return supported local images as actual visual input to the model. `library_read` must expose the same behavior through its existing reader wrapper. Do not add a separate image-viewing tool or change the global upfront list. These tools currently return text or reject binary files; generic image handling elsewhere does not already implement this requirement.

Reuse the existing media processing, image normalization, size limits, and model-capability checks. Preserve the readers' filesystem permissions, workspace boundaries, and audit behavior. Reading an image is inspection: it must not automatically send the file to the user, start a web server, or require a browser. File delivery remains a separate `send_file` action. Text pagination and document text extraction retain their existing behavior; reject explicit image pagination with a clear explanation rather than returning partial image bytes.

At minimum, valid PNG and JPEG files must be inspectable. Other formats follow the existing normalizer's supported formats and limits. Invalid, unsupported, inaccessible, or oversized images must produce an accurate explanation without claiming visual inspection. If normalization resizes an image, tell the model it received a resized view so it can use execution to create and read a crop when fine details matter.

Ensure image content survives every provider conversion used for supported vision models, including images returned by tools. The current Responses and Anthropic tool-result paths omit image media and require correction. Apply the existing capability handling consistently to read-tool images and other tool-produced images; do not bypass it by attaching pre-encoded image data directly. For a model without image support, preserve file access and provide the existing guidance to switch models. Do not silently switch models or claim that text extraction completes visual inspection.

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
5. Give Mia document execution and Admin a real dependency-setup route. Verify each in its actual runtime.
6. Set the fixed Judge/Supervisor capabilities, editable instructions, and the Judge’s read-only task-evidence access.
7. Implement the global upfront set and role-prompt discovery instructions in §5.4. Keep the existing global classification model; add no per-agent visibility setting.
8. Keep future isolation guidance gated by §7. Isolation implementation is not part of delivery.
9. Implement image inspection through `read_file` and `library_read`, using existing media/capability handling and correcting provider image delivery (§6.6). No separate image tool or browser dependency.

Completion requires appropriate automated checks **and real workflows**:

| Workflow | Required observed result |
|---|---|
| Tool visibility and discovery | In a fresh session, permitted tools from the 37-name global set are callable without ToolSearch, including Skill, questions, execution, and hidden-agent essentials in their valid scopes. A role-specific deferred tool is named in its prompt, loaded by exact name through ToolSearch, and then successfully used. Denied/unregistered tools are not offered or made usable by a prompt mention. |
| Ava builds a teammate | One combined proposal, one user-question confirmation, then actual creation, model/tool/skill/connector configuration, team membership, and workspace delegation. Jim can assign the resulting teammate a task and it can use its configured capabilities. |
| Ava changes existing settings | Read before editing, preserve unrelated settings, support deliberate removals/empty lists, and accurately report partial failure or unavailable live activation. A cancelled proposal changes nothing. A concurrent material edit triggers a revised proposal; resumption does not duplicate already completed mutations. |
| Built-in editing | Through both Settings and Ava, ordinary built-in prompts stay protected while tools/connectors/skills can change. Judge/Supervisor instructions can change while their capability sets stay fixed. |
| Persistence | Save, reload, and restart preserve permitted edits, edited Judge/Supervisor instructions, and empty selections. No migration or old-version upgrade test is required in this greenfield scope. |
| Global permissions | An agent-level grant cannot override a global denial. A user who removes a necessary capability gets a clear explanation rather than a hidden regrant. |
| Mia’s document outputs | Generate valid, openable Word, spreadsheet, presentation, and PDF files using the actual assigned skills and execution environment; inspect/render/validate as their workflows require and return the files. |
| Visual inspection | Mia and General Purpose read rendered PNG/JPEG pages through the read tools without browser access or automatic user delivery. Images reach each supported vision-provider request. A live model identifies known visual defects and checks corrected output. Unsupported models and corrupt, oversized, or inaccessible files produce accurate limitations; existing text/document reading still works. |
| Missing dependency | Mia identifies the missing dependency, Admin performs supported setup, and Mia verifies availability and resumes. General Purpose follows the parent/Jim-to-Admin route and verifies its own runtime before resuming. Unsupported setup is reported without claiming completion. |
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
| Document skills | Execution environment and skill loader | Package selected skills, scripts/assets, and dependencies; Admin setup route | Mia and GP produce usable files; missing-dependency recovery is demonstrated |
| Image reading | Generic media processing and model-capability handling | Image return in existing readers, inspection-only delivery, provider preservation | Real visual defect detection/correction and negative-path checks (§6.6) |
| Hidden agents | Native Judge/Supervisor execution and evidence tools | Editable instructions with fixed capabilities; relevant Judge output access | Existing verdict/correction contracts and scope restrictions continue to hold |
| Isolation | Separate feature design | Future prompt/skill guidance only | No isolation engine or merge/retry implementation in this delivery |

This table describes planned delivery, not completed runtime functionality. The companion decisions record is the interview history; the main requirements above are the implementation authority. Historical review and tool-analysis recommendations are superseded wherever they differ from these confirmed requirements.
