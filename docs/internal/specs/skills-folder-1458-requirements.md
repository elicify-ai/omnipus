# Skills as folders — requirements (#1458)

Status: Requirements (founder interview 2026-10-27). Input to the ADR (architect, Opus) and the feature spec (`plan-spec`, Sonnet); both are reviewed by Astra (founder routing, 2026-10-26).
Captured at: release/v0.1.1 `75d48f041`.
Related: #1455 (shared secrets), #1459 (agent details), #1462 (Claude Code / Codex project compatibility, later), #1376, #817.

## Problem (measured 2026-10-27)

Evidence: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/result-skill-probe-1458.md` (real tool registry, fspolicy and macOS Seatbelt) and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-scratch/jarvis-blockers-20261027/REPORT.md`.

| Area | Today (installed skills under `$OMNIPUS_HOME/skills`) |
|---|---|
| Loading | `Skill` returns only the `SKILL.md` body: frontmatter stripped, no folder path, no file list (`pkg/tools/skill.go::execLoad`, `pkg/skills/loader.go::LoadSkillFile`). Document skills are the only exception (`appendDocumentRuntimeGuidance`). |
| Reading other files | `read_file` with a guessed absolute path works for helper files; `SKILL.md` and `list_directory` are refused; `grep` walks the folder and reveals paths. |
| Running scripts | Blocked twice: the shell tool ("never grantable") and the macOS sandbox kernel deny. |
| Editing | `edit_skill` replaces the whole `SKILL.md`; `ValidateSkillMarkdown` accepts a file with no frontmatter. This broke four live skill descriptions on 2026-10-26 (restored by the chief on 2026-10-27). |
| Versions | `.versions/` snapshots of `SKILL.md` only; helper files are not versioned. |
| Audit | Skill loads are audited; writes are not. |

## Goals

A skill installed in Omnipus is a **folder** that agents can fully use and safely change: the whole skill on load, edits through one general permission, every save checked, every change versioned and logged, scripts runnable like any other command.

## Non-goals

- **Project-folder skills** (a workspace mount's `.claude/skills`, as in Claude Code projects) are in the first place **plain files**. They follow the normal file permissions and get none of the new rules. Compatibility with Claude Code and Codex project conventions is a later issue, #1462. (D4)
- No safety scan on agent edits (D6).
- No automatic marketplace updates (D7).
- No change-history screen for the person in this scope (not selected in K8).

## Requirements

### Loading (scope: installed skills)
- R1. Loading a skill returns its instructions, its **description and metadata**, its **absolute folder**, and its **file list** (references, scripts, assets), for every installed skill, not only document skills. (Issue target 1, confirmed by option C)
- R2. Agents can **read** every file of a skill they are granted with the normal file tools (listing included).
- R3. On load, the agent is told **which programs/packages the skill needs and whether they are present**, generalising today's document-skill guidance. (D10)

### Editing
- R4. **One general permission** governs skill editing. An agent that has it may change **any installed skill** — its own or shared; an agent without it may change none. There is no separate own/shared distinction and no per-change approval card. (D1)
- R5. Edits work with the normal file tools on files inside the skill folder; paths may not leave the folder.
- R6. **Save check**: a write that would leave a skill broken (missing name or description, invalid frontmatter, size over limit, path outside the folder) is **refused** with a clear reason; the skill stays unchanged. (D2)
- R7. Replace-only `edit_skill` is retired once the folder path is live (greenfield: no shim).

### Running scripts
- R8. A skill's scripts run **like any other command**, under the agent's normal shell permission (Ask / Auto-approve as today). For running programs the skill folder is **read-only**: a script cannot rewrite its own skill; only the edit permission (R4) can. (D3)

### Versions and audit
- R9. Every change keeps the previous version of the **whole skill folder**.
- R10. Versions are kept according to the **existing data-retention setting** (the founder believes the session default is 90 days; the spec verifies the actual setting and default). (D5)
- R11. An agent with the edit permission can restore an earlier version (logged). Chief default, not contested by the founder; the person's restore path is via an agent or a later UI.
- R12. Every change is **logged**: agent, skill, file, kind of change, version id.

### Safety and updates
- R13. A **safety scan** for harmful instructions runs **on install only** (marketplace and imported skills). Edits by agents are not scanned; versions and the log cover them. (D6)
- R14. There is **no automatic skill update** (verify in the spec). When a person reinstalls a skill that has local changes, Omnipus **warns first** that the local changes would be replaced (they remain in the version history). (D7)

### Freshness
- R15. After a change, agents see the **updated skill list and descriptions immediately**, without a restart. (D11)

## Security considerations

| Risk | Handling |
|---|---|
| A shared skill is a lasting instruction for every agent using it; a bad edit spreads | Founder accepted one general edit permission (D1) and no scan on edits (D6). Mitigations: the save check (R6), versions (R9) and the change log (R12). The ADR must state the residual risk plainly. |
| Skill scripts run with the agent's environment, including shared secrets (#1455) | Scripts follow the normal shell permission (R8); the skill folder is read-only while running; shared secrets reach a script only if that agent has access (#1455 R6/R7). The ADR covers this combined risk explicitly. |
| Opening `$OMNIPUS_HOME/skills` to reading and execution | Today it is app-layer protected and kernel-denied (`pkg/fspolicy/secretset.go::SecretEntriesAlwaysPathOnly`, home guard). The change needs a security-lead review: read and execute allowed, write only through the edit permission, `.versions/` handled deliberately. |
| `grep` reveals paths in a protected area today | Superseded by R2 (reading is allowed); the ADR states the final boundary. |
| Windows has no kernel sandbox | The read-only-while-running rule (R8) is enforced at app level only on Windows; the spec must say so. |

## Edge cases to specify

- Editing a built-in skill: creates an override (today's `builtinMarkerFile` semantics) and is versioned like any edit.
- Two agents editing the same skill at once: revision-based conflict refusal (today `*Reviewed` writers / `SkillRevision`).
- A refused save in the middle of a multi-file edit: whole-change or per-file semantics are decided in the spec.
- Deleting a skill an agent is using: the version history is kept for the retention period.

## Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Who may edit | Skill editing is one general permission: an agent that has it may change any installed skill; no own/shared split and no approval card. The chief explained the spread risk and offered "shared: you approve"; the founder chose the general permission | Simple permission model, consistent with tool permissions | Interview | 2026-10-27 |
| D2 | Save check | A save that would leave a skill broken is refused with a reason | Exactly the failure that broke four descriptions | Interview | 2026-10-27 |
| D3 | Running scripts | Like any command, under the normal shell permission; the skill folder is read-only for running programs | No new permission type; scripts cannot rewrite their skill | Interview | 2026-10-27 |
| D4 | Installed vs project skills | The new rules apply to installed Omnipus skills only; project-folder skills are plain files; Claude Code / Codex compatibility is tracked in #1462 for later | Founder: project skills are "in the first place just files" | Interview | 2026-10-27 |
| D5 | Version retention | Same retention as other data (the retention setting; founder believes the default is 90 days) | One retention policy | Interview | 2026-10-27 |
| D6 | Safety scan | Only on install; edits are not scanned | Founder choice; versions and log cover edits | Interview | 2026-10-27 |
| D7 | Marketplace updates | No automatic updates; warn when a reinstall would replace local changes | Founder: an update happens only when a user installs | Interview | 2026-10-27 |
| D8 | Restore | An agent with the edit permission can restore a version (logged) | Chief default, not contested | Interview | 2026-10-27 |
| D9 | History screen | No change-history screen in this scope | Not selected in K8 | Interview | 2026-10-27 |
| D10 | Dependency readiness | On load the agent is told which programs the skill needs and whether they are present | Founder extra (K8) | Interview | 2026-10-27 |
| D11 | Freshness | The skills menu refreshes immediately after a change | Founder extra (K8) | Interview | 2026-10-27 |

## Dependency Graph & Implementation Order

1. ADR (architect, Opus): the folder boundary in fspolicy and the sandbox (read/execute open, write only via the edit permission), the save-check contract, folder versioning and retention, audit events, the combined skills × secrets risk. One Astra review round, then founder questions, then correction.
2. Feature spec (`plan-spec`, Sonnet) from this document and the ADR; two Astra review rounds, each followed by a founder interview.
3. Build order: fspolicy/sandbox boundary (security-lead) → load contract (R1–R3) → write path with save check and folder versions (R4–R6, R9–R12) → script execution (R8) → install scan and reinstall warning (R13–R14) → freshness (R15) → retire `edit_skill` (R7) → docs and hands-on UAT (an agent loads a skill with a reference and a script, edits its description, a broken save is refused, a version is restored).
