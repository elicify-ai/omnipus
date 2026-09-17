# Critical review — Built-in agents and skills

Review date: 2026-09-17. Original baseline verdict: **REVISE**. Findings below are historical; see the final review addendum for their disposition.

Reviewed document: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/docs/internal/design/built-in-agents-and-skills-2026-09.md`.

Evidence baseline: release worktree commit `a0b36050b`. This is a requirements review supported by source inspection, not a runtime test or implementation audit. GitNexus has no index for this worktree; source was inspected directly. A separate read-only reviewer checked document consistency. The requirements themselves were not changed.

**Follow-up, 2026-09-17:** The founder subsequently narrowed this work to agents and skills. The requirements now exclude isolation implementation and the coding-tool implementation list; section 7 retains only future prompt/skill guidance, activated when the separate isolation feature ships. F8 and F11 below describe the reviewed baseline, not outstanding isolation implementation work. The former sections 10 and 11 are now 9 and 10. Other findings have not yet been resolved.

**Office-tooling clarification:** F9 does not require a native Office tool. The intended approach is to use the Anthropic document skills and their scripts. The concrete gap is that the proposed matrix denies Mia shell execution while expecting her to generate Office/PDF files. She needs a permitted script-execution route, the selected skills' runtime dependencies and helper scripts, writable output storage, and a way to validate and return the files. The locally available Word and PowerPoint skill versions use Node.js generators as well as Python helpers; do not assume Python alone runs those skills unchanged. This is a dependency/permission requirement, not a proposal to build a new document engine.

**Interview outcome, 2026-09-17:** The requirements have now been revised against the confirmed decisions recorded in `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/docs/internal/design/built-in-agents-and-skills-2026-09-decisions.md`. The findings below remain the historical baseline review, not a claim that the revised document retains those omissions. The founder explicitly chose greenfield delivery: F4's migration/retired-agent work and old-version upgrade checks are excluded, while reload/restart preservation remains required. Judge/Supervisor editable instructions with fixed capabilities are now an explicit exception, and Mia's script execution plus Admin's dependency setup are required. No implementation or runtime validation has yet occurred.

## Executive assessment

Ava's expanded job is partially described, but the means to complete it are not. The user's requirement that built-in prompts/instructions stay fixed while tool permissions, MCP connector assignments, and skill assignments remain editable is absent. Related existing documentation and implementation still enforce broader locks.

There are **8 major findings, 2 minor findings, and 1 observation**. No demonstrated critical production incident is claimed. Revise these requirements before implementing them; do not interpret missing details as permission to keep the existing locks.

The user's current direction is the review baseline: built-in base prompts and instructions are protected; capability assignments are user-controlled. Editable skill assignments intentionally change available playbooks without editing the built-in base prompt. Custom-agent authoring remains a separate case.

## Findings

| ID | Severity / lens | Sections | Finding and consequence | Required correction |
|---|---|---|---|---|
| F1 | Major / incompleteness | 2.1, 5, 6.2, 11.2 | Ava's tool-policy and delegation expansion is explicit, but skill assignment and installed-MCP assignment are not complete operational requirements. Her current create/update tools lack tool-policy, skills, and MCP fields. `update_agent` refuses all locked-agent changes. A skill named `skill-mapping` cannot supply a missing operation. | Specify read, assign, change, and remove operations for tools, skills, and installed MCPs; model selection; workspace team membership; and delegation. Require Ava to read back the effective result and confirm the agent is usable. Distinguish authoring a skill from granting it to an agent. |
| F2 | Major / ambiguity and insecurity | 2.1, 5, 11 | No field-level distinction separates immutable built-in prompts from editable capabilities. The screen, dedicated tools endpoint, skill-update validation, and Ava's update tool currently impose separate locks. Some hidden system-agent souls are editable today, which also needs reconciliation with the user's instruction. | Define protected prompt/instruction fields and editable tool/MCP/skill fields for each built-in type. Enforce the same rules in the screen and every server/tool write path. Reject prohibited fields explicitly; do not silently discard them. Keep existing global policy limits effective. |
| F3 | Major / ambiguity | 2.1, 5, 6.2 | “Deny MCP” conflates installing/configuring a connector with assigning an already-installed connector to an agent. Ava cannot fully equip a teammate if both are forbidden. | Admin owns installation, credentials, provider/channel setup, and server lifecycle. Ava can inspect installed connectors and grant/revoke agent bindings and permitted connector tools. An unavailable connector produces an Admin handoff, not a fictitious successful assignment. |
| F4 | Major / inoperability | 11 | Reseeding is specified without an upgrade contract. User changes can be overwritten, and removing Ray/Max can leave team, task, or delegation references unresolved. Current system-agent seeding reapplies exact skill lists and tool policies at startup. | Separate first-install defaults from stored user overrides. Preserve permitted overrides, including deliberately empty skill/MCP selections, through restart and upgrade. Define migration of retired-agent references and protected prompt updates. Report changes that cannot be activated live rather than claiming they are effective. |
| F5 | Major / inconsistency | 2.4, 5, 6.1, 11 | The matrix is not labelled as editable defaults versus permanent restrictions. Judge's pinned `verify` list conflicts with blanket built-in skill editability. GP's “not inherit the global ceiling” wording conflicts with the current two-layer policy model if interpreted as new fallback behavior. | Make capability entries shipped defaults under the user's rule. Explicitly resolve hidden-engine-agent treatment rather than inventing an exception. Express GP defaults as concrete per-tool entries within the existing global/per-agent model; do not add a third fallback. Coordinate changes with existing Judge restrictions and policy decisions. |
| F6 | Major / inconsistency | 2.3, 3, 5 | General Purpose is allowed self-delegation in prose, called a leaf elsewhere, and receives no delegation permission in the table. Implementations can reasonably choose opposite behavior. | State consistently that GP may delegate only to itself, if that is intended, and retain the explicit restriction against nested isolated helpers. Define “leaf” as no delegation to other agents, or remove the term. |
| F7 | Major / incorrectness | 2.4, 5, 7.1 | Judge is described as `inspect_session` only, but the table also grants file/library reads. This leaves the evidence boundary unresolved, especially for isolated work. | Name the supported evidence channel and exact read permissions. Ensure a judged isolated task is evaluated against its own artifacts and commit range. Remove contradictory grants or amend the role description. |
| F8 | Major / incorrectness | 7.2–7.4 | “Notes and documents never need” isolation contradicts isolating every concurrent writer to a repository. A README or specification edit can conflict with another repository writer just like code. | Base isolation on writes to the same repository, including documentation and configuration. Exempt read-only research and artifacts outside that repository. |
| F9 | Minor / infeasibility risk | 5, 6.1 | Mia is promised real Office/PDF output while lacking shell access, a native Office tool, and a clearly permitted worker route. The document itself acknowledges the missing native tool; the file-generation path is unproven. | Specify an actual generation capability or an explicit handoff to Jim/GP. Require an openable sample file for each promised format. Do not equate a written skill with a file-generation runtime. This review did not test output generation. |
| F10 | Minor / incompleteness | 1, 11 | No observable acceptance checklist covers the main capabilities, persistence, and protection boundary. Ava can otherwise be declared complete when she only creates a record. | Add the concrete checks below, including a usable configured teammate, forbidden prompt edits, permitted empty selections, and restart preservation. |
| F11 | Observation / overcomplexity | 7, 9 | The roster requirements carry extensive isolation mechanics and language-server implementation detail while the central editing rules are missing. The referenced ADR-082 v10 decisions D11–D13 are not present in this release checkout's ADR-082. | Keep the behavioral dependency, link the exact authoritative design/version, and move implementation detail into its owning spec. Do not assume the branch-only dependency has shipped. |

## Source evidence

All paths below are absolute. Symbol references identify the relevant behavior; comments alone were not treated as runtime proof.

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/sysagent/tools/agent.go` — `AgentCreateTool.Parameters`, `AgentUpdateTool.Parameters`, and `AgentUpdateTool.Execute`: missing capability fields and blanket locked-agent refusal.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/sysagent/tools/workspace.go` — `WorkspaceUpdateTool`: team updates exist, with bounded default delegation seeding; this is not an arbitrary workspace delegation editor. Agent creation and delegation configuration are separate concerns.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/coreagent/seed.go` — `avaSeedPolicies`: Ava already has agent lifecycle, model listing, skill authoring, and workspace tools. More permissions alone will not add missing tool parameters.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/src/components/agents/AgentProfile.tsx` — `AgentProfile`: locked skills checkboxes; system-agent soul exception; passes locked status to the permission editor.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/src/components/agents/ToolsAndPermissions.tsx` — `ToolsAndPermissions`: locked status disables editing and autosave, including the combined permission surface.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/gateway/rest_tools.go` — `restAPIUpdateAgentTools.validateRequest`: dedicated tools update endpoint rejects locked agents.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/gateway/rest_agents_update.go` — locked-agent validation rejects skill mutations, permits a system-agent soul exception, and has a separate tools configuration update path. The server does not have one uniform field policy today.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/pkg/coreagent/seed_system.go` — `systemAgentSkills` and `seedSystemAgents`: system-agent capability lists/policies are re-enforced on startup. Ordinary core-agent override preservation is different; do not generalize the system-agent behavior to every built-in.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/docs/internal/architecture/agent-types-field-matrix.md` — built-in skills documented as read-only. This and the related agent-form requirements must be reconciled with the new direction.
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/worktrees/release-20260917/AGENTS.md` — hard constraint 6 specifies global ceiling plus per-agent overrides and prohibits reintroducing automatic deny backfill.

## Document completeness

The roster and intended responsibilities are clear enough to discuss. Completion conditions are much weaker: “completely” authoring a teammate is not tied to readback, persistence, or runnability. Failure behavior is detailed for isolation but largely missing for capability changes. Dependencies exist, but the referenced worktree design is not self-contained on this release checkout. This is an informal requirements document; a full formal specification is unnecessary to resolve these gaps.

## Acceptance checks to add

1. Through both Settings and Ava, change an existing built-in's tool policy, installed MCP assignment, and skill list; verify the runtime uses the resulting configuration.
2. Attempt to change protected built-in prompts/instructions through each supported write path; reject the edit and leave protected data unchanged. Separately confirm custom-agent authoring still works.
3. Remove every assigned skill or MCP; verify empty remains empty after reload, restart, and upgrade. Preserve deliberate tool overrides as well.
4. Ava creates a specialist, assigns its capabilities, places it on the intended team, configures the workspace delegation relationship, and Jim successfully assigns it a task. Do not count creation alone as completion.
5. Unknown skills/connectors fail clearly. A denied global tool remains denied despite an agent-level allow. Failed live activation or a partially completed team/configuration operation is reported accurately.
6. Exercise the chosen Judge/Plan Supervisor editing rules explicitly, including startup persistence and protected instructions.
7. Run two repository writers where one edits documentation; apply the same isolation/overlap rule as for code. Verify the GP delegation and Judge evidence rules chosen during revision.
8. If Office/PDF authoring remains promised for Mia, demonstrate valid files using her actual permitted execution route.

## Security and authority assessment

| Surface | Specific concern | Required boundary |
|---|---|---|
| Agent update | Protected instructions could become editable if a blanket lock is simply removed. | Field-level server validation, consistent across screen and agent tools. |
| Capability editing | User-authorized capability changes must remain within the global ceiling; Ava's role must distinguish administering a permission from personally using the tool. | Existing policy resolution remains authoritative; no implicit authority expansion through self-edits. Specify how user direction authorizes sensitive capability changes. |
| Connector assignment | Installation/credentials and per-agent access are different operations. | Ava manages assignments; Admin manages installation and secrets. Readback must not expose credentials. |
| Judge evidence | Skill and read permissions can change what influences judgment. | Resolve editable defaults versus engine constraints explicitly and use task-specific evidence. |

No separate authentication, encryption, or availability incident was established by this review; a generic security redesign is not warranted.

## Decisions to make explicit in the revision

- Apply the user's editable-capability rule consistently to all built-ins; do not silently exempt the hidden engine agents. If an engine restriction is retained, surface the conflict for an explicit decision.
- Separate installed MCP assignment from installing/configuring a server.
- Keep delegation relationships workspace-scoped; adding fields to an agent update alone does not define which workspace changes.
- Define how existing installations move to the new roster without erasing user capability choices or orphaning historical work.

Next action: revise the requirements and affected field-matrix documentation against these findings, then review the revision before implementation.


## Final review addendum — consolidated requirements

The independent read-only review returned **PASS: ready for implementation planning** after rereading the corrections. This is a requirements verdict, not runtime delivery or a claim of passing tests.

| Finding | Severity at review | Resolution |
|---|---|---|
| R1: Judge vocabulary conflicted with its existing verdict contract | Major | §2.4 now retains criterion-level `met`/`unmet`; engine behavior remains authoritative. |
| R2: Supervisor table accidentally omitted existing scoped grep | Major | §2.4 and §5 explicitly retain the fixed scoped search capability. |
| R3: Confirmed changes could overwrite concurrent edits or replay successful mutations | Minor | §5.2 requires readback, preservation, revised confirmation for material changes, and safe resumption; §10 covers acceptance. |
| R4: General Purpose dependency setup route was missing | Minor | §6.5 routes through parent/Jim to Admin without cross-role delegation; §10 covers acceptance. |

The review covered ambiguity, completeness, consistency, feasibility, security, operability, correctness, and complexity. No further blocking findings remained. Scope, field protections, default capabilities, global visibility, document dependencies, and image inspection now have acceptance criteria. Image inspection retains filesystem boundaries and input limits, preserves unsupported-model handling, and requires actual provider image delivery and live defect detection. Existing media infrastructure and readers are reused; no new isolation implementation or image tool is prescribed.

Verification for this documentation change: direct source inspection and independent requirements review. GitNexus change detection was attempted but cannot resolve this unindexed worktree; Git diff inspection is the fallback scope check. No runtime tests or live agent workflows were run. Historical findings and alternatives above are superseded by the main requirements and confirmed decisions.
