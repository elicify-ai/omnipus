# Delivery slices — Export / Import packages (#1094)

Read with `ADR.md` (decisions D1–D9). Paths are on `origin/release/v0.1.1` at `7faff5291`; every existing file and function named here was checked with grep. Files marked **new** do not exist yet.

**Sizes** (changed lines including tests): S = under 400 · M = 400–1,200 · L = 1,200–2,500.
**Founder questions that change a slice** are named in the "Waits on" column.

## Overview

```
S1 agent round trip (CLI, demo) ──┬─► S2 skills ──┐
                                  ├─► S3 MCP ─────┤
                                  ├─► S4 agent memory
                                  │               ▼
                                  └─► S5 workspace (team, delegation, shared memory, work files, KB)
                                                  │
                           S6 Replace + Merge + snapshot  ◄─┘
                                                  │
                     S7 whole system + custom scope
                                                  │
                     S8 Settings screen   S9 session history (opt-in)   S10 Claude Code plugin
```

| Slice | One sentence | Size | Depends on | Waits on |
|---|---|---|---|---|
| S1a | Export one custom agent (Main or native worker) to a `.omnpkg` file and import it into a clean install from the command line (founder demo); all-or-nothing, invisible until committed, start-up recovery | above L (~5,570 lines incl. tests, revision 7) | — | — (Q1 decided: B) |
| S1b | External workers in packages, unknown element types skipped | M (~480) | S1a | — |
| S2 | Skills travel with the package | M | S1 | — |
| S3 | MCP server definitions travel, secrets stripped | M | S1 | — |
| S4 | An agent's private memory travels | S–M | S1 | — |
| S5 | A whole workspace travels: record, team, delegation, instructions, shared memory, work files, knowledge bases inside it | L | S1, S4 | — (Q4 decided: C) |
| S6 | Replace (default) and Merge modes, with warning, confirmation and pre-import snapshot | M–L | S1 (S5 for full value) | Q2 |
| S7 | "Whole system" and "Custom" scopes | S–M | S2–S5 | Q3 |
| S8 | Settings → Data screen: export picker, import preview and confirm dialog | M–L | S6, S7 | Q3 |
| S9 | Session history (opt-in) | L | S5 | — |
| S10 | Claude Code plugin import and plugin-shaped export | M | S2, S3 | Q5 |

Recommended order: **S1 → S2 ∥ S3 ∥ S4 → S5 → S6 → S7 → S8**, then S9 and S10 in either order. S2, S3, S4 touch different files and can run in parallel.

---

## S1 — Agent round trip via CLI (founder demo)

**Demo:** on install A, `omnipus export --agent "Writer" -o writer.omnpkg`; copy the file; on a fresh install B, `omnipus package inspect writer.omnpkg` shows the agent name, file list and checksums; `omnipus import writer.omnpkg` adds "Writer" (a Main agent) with a **new ID**; it appears at once in the Agents screen and can chat with the same SOUL prompt and settings. (An imported native worker appears in the Team and is reached by delegation once the owner draws a line to it.)

**Revision 7, 2026-11-19 (founder decisions; review cap reached, nothing else reopened).** **P-QR B:** the import is all-or-nothing. The agent is written as an invisible pending record (`entities/agents/<id>.pending`); the team entry, the self-delegation row and one atomic rename to `<id>.json` (the commit point) run in one workspace-lock hold. Until then no list, chat, session, tool or screen can see or use the agent. Rollback is C1 (leave, inside the lock hold) then C2 (discard record and folder); no rollback reload. Start-up recovery from the journal moves into S1a: the next gateway start finishes or rolls back an unfinished import before any agent is loaded. Withdrawn: the residue from other screens (sessions, chats, main chat, home notes, other workspaces), the final clean-up and its session and workspace recovery steps. Still not guaranteed: lock-free workspace readers can see the ID during the commit step; a power cut is safe only where storage honours flushes (Windows cannot sync a directory). **P-Q1 B:** built-in agents never travel; export refuses them ("cannot be exported"), import never targets them. Size: S1a about 5,570 lines (+190), about +2 to +4 working hours net (🔶 inferred). Spec: revision 7 sections of `export-import-s1-spec.md`; ADR amendments A9–A11.

**Final spec 2026-11-19 after grill round 5 (spec revision 6, review cap reached; ADR A7 revised): build S1a.** The rollback's membership and edge removal covers the import workspace only; references other requests added in other workspaces are repaired by a manual step that uses the existing revision-checked workspace update. Session clean-up is complete only with other session activity paused and a fresh verification scan, otherwise it is reported unverified. The after-deletion home test accepts exactly the real memory writer's note plus its derived index files. Sizes unchanged.

**Revised a fourth time 2026-11-19 after grill round 4 (spec revision 5; ADR A7 revised).** Rollback is defined by the places and records it removes, not by who wrote them: the agent record, its home folder with everything inside it (including content other requests saved there, such as a private memory note), its team entry and every delegation edge involving it. Chats and sessions outside that boundary stay. Work already running can recreate home content after the folder is deleted; the final clean-up (stop the gateway, delete the folder, start, delete every session found over all pages) removes it. A protected main chat cannot be deleted and is not removed by retention, so it can remain indefinitely in S1. The delegation-save fault case now persists a removable edge first (partial C1). Founder acceptance is still pending with the chief.

**Revised a third time 2026-11-19 after grill round 3 (spec revision 4; ADR A7–A8 revised).** (Its rollback wording is superseded by the round-4 revision above.) Rollback promises clean-up of what the import owns (record, home folder, team membership and delegation row) only. What other requests created while the agent was visible (ordinary sessions, accepted chat messages, turns already running, a main chat) is not rolled back; the answer says so and manual recovery step 4 deletes the ordinary sessions. Founder acceptance of this narrower guarantee is pending with the chief. `agent_created` and the main chat only after a confirmed reload, otherwise `activation: "pending"`. A failed membership removal stops the rollback before the record is deleted. Upload consumption is proven with a counting body; limit + 1 is 413 also when chunked. Control and alias names have written decisions in the coverage test. S1b keeps the per-runtime check before the shared field applier.

**Revised again 2026-11-19 after grill round 2 (spec revision 3; ADR A7–A8).** Promises kept small: publication and rollback use the existing full reload; no isolation of a running import (round 3 corrected this: other requests' sessions and messages may remain, see above; S6 brings the clean-up); REST create unchanged; `voice`/`memory_enabled`/`context_window_override` follow the `create_agent` tool; main chat only for the imported Main; S1a refuses external workers on upload too; manual recovery (including freeing the upload slot) and the `unfinished_import_found` warning moved into S1a.

**Revised 2026-11-19 after grill round 1 (spec: `SPEC-S1.md` revision 2; ADR amendments A1–A6).** S1 is split into **S1a** (the founder demo) and **S1b** (hardening the demo does not need). The original estimate of about 1,800 lines was too low: the grill found the shared create function with its field mapping, the workspace-join extraction, the coverage-check extraction, real-store rollback, the staging lifecycle, decoder limits and a small frontend refresh fix.

**Scope S1a**
- `pkg/omnpkg` engine: manifest v1 with relationship checks, tar + zstd writer/reader with bounded decoding, SHA-256 per file, `Inspect` (manifest only), `Verify`, universal path and entry-type checks (yes/no predicates moved from `extractTarGz` / `isProtectedRestoreNamespace`; restore keeps skipping), staging lifecycle under `backups/.staging/`, journal, in-process rollback with a commit point, and start-up recovery (ADR A9).
- Element: **custom agent** (Main or native worker) = record cleaned per the spec's Field mapping, `SOUL.md`, `AGENT.md`. `subagents.allow_agents` and `subagents.model` are removed (retired / unused, ADR A3); connector assignments are dropped and listed.
- Import is a create under the existing create rules (shared function), plus `AGENT.md`; `voice`, `memory_enabled` and `context_window_override` with the `create_agent` tool's rule. Field-coverage test across REST and agent-tool field lists.
- **Revision 7:** invisible pending record (`agentstore` `CreatePendingState` / `PublishPending` / `DiscardPending`, task T-11); join, self-row and the commit rename in one workspace-lock hold (`JoinWorkspaceTeamAndPublish`, T-4); rollback C1 (inside the lock hold) → C2 (record and folder), no rollback reload; start-up recovery before the first roster load.
- Publication by full reload after commit (`agent_created` and main chat only when confirmed, otherwise `activation: "pending"`); main chat only for the imported Main (`ensureMainSession`).
- Manual recovery (short, only when start-up recovery keeps failing) documented and tested; the next upload warns `unfinished_import_found` with the agent ID only after a failed recovery.
- Import mode **New** only (new ID, suffix on name clash). Replace/Merge come in S6.
- REST (contract first): `POST /api/v1/packages/export`, `POST /api/v1/packages/import` (upload → provisional preview + `staging_id`, 64 MiB route limit), `POST /api/v1/packages/import/{staging_id}/apply`. Local mode only, authenticated, refused under developer bypass. **No password re-entry in New mode** (ADR A5).
- CLI: `omnipus export`, `omnipus import`, `omnipus package inspect|verify` (inspect/verify offline). ADR-024 amended (ADR A1).
- Frontend: `agent_created` also refreshes workspace detail and delegation, so an open Team tab shows the new member.
- Core (built-in) agents: never in a package (founder P-Q1 B, ADR A10): export refuses them with a clear message, import never targets them. External workers (on export **and upload**) and unknown element types are refused with a clear message until S1b.

**Scope S1b**
- External workers (Subagent External): `env_overrides`, `cli_args`, `cli_path` removed; the `create_agent` per-runtime check (`agentmutation.ValidateFields`) runs before the shared applier, so `memory_enabled` and `context_window_override` are refused for them; env names listed as secrets to re-enter; the existing "program not set" text surfaced after import.
- Unknown element types pass the universal checks and checksums and are skipped (forward compatibility; **S2 needs this before it ships**).

**Named tasks (from the spec)**: T-1 contracts · T-2 format engine · T-3 shared create function, coverage check without the config write, exported tool field applier, field-coverage test · **T-4 workspace-join extraction from the `create_agent` tool, plus a leave helper and the join-and-publish helper** · T-5 handlers, staging, journal, rollback C1–C2, single-agent main chat, audit rules, unfinished-import warning, start-up recovery · T-6 CLI · T-7 frontend refresh · T-8 docs (with recovery procedure) · T-9 external workers [S1b] · T-10 unknown elements [S1b] · **T-11 pending agent record in `pkg/agentstore` (revision 7)**.

**Files**

| File | Change |
|---|---|
| `pkg/omnpkg/` (**new**): `manifest.go`, `archive.go`, `verify.go`, `remap.go`, `export.go`, `import.go`, `journal.go`, `element_agent.go`, tests | engine |
| `go.mod` | `github.com/klauspost/compress` from indirect to direct (no new download; already linked, ADR F14) |
| `contracts/openapi.yaml` + **new** `contracts/components/schemas/PackageManifest.yaml`, `PackageExportRequest.yaml`, `PackageImportPreview.yaml`, `PackageImportApplyRequest.yaml`, `PackageImportResult.yaml` | wire types; regenerate `pkg/api/generated/`, `src/lib/api/generated/` via `scripts/gen-contracts.sh` |
| `pkg/gateway/rest_packages.go` (**new**) | three handlers, wiring stores into the engine |
| `pkg/gateway/rest.go` (routes near line 936, local-mode block) | register routes |
| `pkg/gateway/rest_agents_create.go` (`validateCreateFields`, `validateAndBuildConfig`, `buildToolConfig`, `persistAgent`, `publishResponse`) | pull the non-HTTP part out into one function the import also calls (no copy); domain input, not the REST request type (T-3) |
| `pkg/gateway/rest_agents.go` (`withToolPolicyCoverageGuard`) and `pkg/gateway/rest_config.go` (`writeConfigJSONLocked`) | split "check coverage under the config lock" from the config-file write, so import never writes `config.json` (T-3) |
| `pkg/sysagent/tools/agent_apply_args.go` (`applySharedConfigArgs`) | exported unchanged, so import stores `voice`, `memory_enabled`, `context_window_override` with the `create_agent` rule (T-3) |
| `pkg/sysagent/tools/agent.go` (`joinWorkspaceTeam`, `seedSelfEdgeLocked`, `cascadeCleanOneWorkspaceLocked`) | extract join (team entry + self-row, one workspace lock) into a shared helper with a matching leave helper, plus `JoinWorkspaceTeamAndPublish` (revision 7); `create_agent` keeps identical behaviour (T-4) |
| `pkg/gateway/main_session.go` (`ensureMainSession`, `mainSessionPairEligible`) | main chat for the imported Main only, after the commit point (not `ensureWorkspaceMains`) |
| `pkg/gateway/rest_auth.go` (`triggerReloadAndWaitOutcome`) | publication after commit (revision 7: no reload after a rollback) |
| `pkg/gateway/gateway.go` (before `seedAndPersistAgentRoster`) | call start-up recovery once (revision 7; moved from S6) |
| `pkg/agentstore/state.go` (`CreateState`, `MutateStateWithAgentMD`, `DeleteState`) | unchanged; **revision 7** adds `CreatePendingState`, `PublishPending`, `DiscardPending` beside them (T-11); export uses its own bounded, link-refusing read instead of `readOptional` |
| `src/lib/api/agents.ts` (`invalidateAfterAgentCreate`) | also invalidate workspace detail and delegation queries (T-7) |
| `pkg/gateway/rest_settings.go` (`extractTarGz`, `isProtectedRestoreNamespace`) | safety checks moved to `pkg/omnpkg`, backup keeps calling them |
| `pkg/app/app.go` (`NewRootCommand`, `AddCommand` list at 503) | add commands |
| `pkg/app/internal/pkgcmd/command.go` (**new**) | CLI, reusing `clitoken.LoadCLIToken` and the auto-start path in `pkg/app/app.go` (`autoStartAndRetry`) |
| `tests/e2e/` or Go integration test (**new**) | two-home round trip |

**Size (re-estimated 2026-11-19, updated in revision 7):** S1a about 5,570 changed lines including tests (about 2,710 production and docs, 2,860 tests; generated contract code excluded); revision 7 added about +190 net (pending record, join-and-publish helper, start-up recovery; minus the withdrawn other-writer cases, rollback reload and long recovery docs), 🔶 about +2 to +4 working hours net. Revision 5 figure: 5,380 (2,580 + 2,800). Revision 4 added about 280 for the round-3 fixes, revision 5 about 170 for the round-4 fixes. Revision 2's "4,050" was an arithmetic error (its own task numbers added up to 4,530); revision 3 added about 400 for the round-2 fixes. S1b about 480 (size M). Do not cut S1a further: a format-only slice cannot be demoed. Per-task numbers are in `SPEC-S1.md` "Size and split". Estimates, not measurements.
**Done when (S1a, revision 7):** the two-home round trip matches the spec's Field mapping, with a new ID; a tampered file fails `verify` and upload with the file name; a higher `format_version` is refused; before the commit point the agent is in no list, reload, agent tool, session or chat, at every hook point; for every fault point before the commit point the rollback oracle holds (no record, pending record, folder, team entry or edge for the agent; every workspace as before plus others' completed edits; `config.json` byte-identical; only the four harmless markers differ; no session or chat names the agent); an incomplete rollback is finished by the next start; a crash at every step is finished or rolled back by the next start, before any agent is loaded, and running recovery twice changes nothing; the exact-64 MiB upload is read in full and limit + 1 is 413 with and without a declared length; an imported Main chats once publication is confirmed and an imported native worker is reachable once a delegation line is drawn; built-in agents are refused at export; an external-worker package is refused at upload; an open Team tab shows the imported agent without reload.
**Done when (S1b):** an external worker round-trips without any runtime value or secret, `memory_enabled` and `context_window_override` on it are refused as `create_agent` refuses them, and a package with an unknown element type imports its agent and skips the rest.

---

## S2 — Skills

**Demo:** export an agent that uses a custom skill; import on a clean install; the skill is installed and the agent can use it.

- Export `skills/<slug>/` for every non-built-in skill the selected agents name (built-ins are skipped: every install has them).
- Import through `skills.ApplyChange` with `KindInstall` (or `KindInstallReplace` in S6), which gives version history and undo for free. Agent `skill_grants` rebuilt by the normal save path.

| File | Change |
|---|---|
| `pkg/omnpkg/element_skill.go` (**new**) + tests | element |
| `pkg/skills/pipeline.go` (`ApplyChange`, `ChangeRequest.StagedDir`), `pkg/skills/identity.go` (`IsBuiltin`, `EnsureGeneration`) | used as is |
| `pkg/agentstore/skill_grants.go` | used as is (grants rebuilt on save) |

**Size:** M. **Depends on:** S1.

---

## S3 — MCP server definitions

**Demo:** export an agent bound to an MCP server that has an API key; the package's `.mcp.json` shows `${KEY_NAME}`, not the key; after import the server exists, switched off, marked "needs secret"; after entering the key in Settings it connects.

- Export global server definitions the selected agents bind to (`tools.mcp.servers[].id`) into root `.mcp.json`. `env_refs` / `header_refs` → placeholder names; literal `env` / `headers` values **removed**; `env_file` dropped. All names listed in manifest `needs_secrets`.
- Import: add server (suffix on name clash in New mode), disabled when a secret is missing; re-bind agents.
- A test scans every exported byte for the test secret values (proof that secrets never leave).

| File | Change |
|---|---|
| `pkg/omnpkg/element_mcp.go` (**new**) + tests | element and secret stripping |
| `pkg/config/config.go` (`MCPServerConfig`, `AgentMCPToolsCfg`) | read only |
| `pkg/gateway/rest_config.go` (`mcpHeaderCredKey`) and the existing MCP-server write path in the gateway | reuse for the import write |
| `pkg/mcp/manager.go` (`ResolveServerEnvRefs`) | unchanged; explains why refs are enough |

**Size:** M. **Depends on:** S1.

---

## S4 — Agent private memory

**Demo:** an agent "remembers" three facts; export; import on a clean install; ask it — it recalls them (search index rebuilt automatically).

- Export `agents/<id>/.omnipus/memories/*.md` and `counters.jsonl` (index `.index/` is excluded; it is rebuilt).
- Rewrite `author` to the new agent ID; clear `born_in` unless sessions are imported (S9).

| File | Change |
|---|---|
| `pkg/omnpkg/element_memory.go` (**new**) + tests | element |
| `pkg/memrooms/rooms.go` (`ResolveAgentPrivateRoom`, `EnsureRoom`), `pkg/memrooms/memory_file.go` (front-matter type) | used as is |
| `pkg/memrooms/index/` | confirm rebuild-on-open works for imported files (test only) |

**Size:** S–M. **Depends on:** S1.

---

## S5 — Whole workspace

**Demo:** export a workspace with three agents, delegation between two of them, shared memory, a knowledge base in `work/`, and project instructions; import into a clean install; the Team tab shows the same team and arrows, the knowledge base is searchable, and all IDs are new.

- Workspace record (`core_team`, `member_configs` remapped; `is_default` forced false), `AGENT.md` instructions, delegation edges (remapped, unknown ends dropped, re-validated), shared memory room, `carry-forward/<agentID>.md` renamed, `work/` files including knowledge-base folders (`.omnipus-vault/` markers). Derived `knowledge/<hash>/` index **not** exported; rebuilt.
- Mounts are not exported (host paths and write permissions). Mounted knowledge bases: **founder P-Q4 C (2026-11-19, ADR A11): ask per mounted folder, default off.** Recorded only; not built before S5.
- Built-in agents in the team (Mia, Jim, Omnipus, …): never exported (founder P-Q1 B, ADR A10); they and their delegation edges are left out and listed, and import never targets the target install's built-ins.
- Agents of the workspace come along automatically (reuses S1, S2, S3, S4 elements).

| File | Change |
|---|---|
| `pkg/omnpkg/element_workspace.go` (**new**) + tests, `remap.go` extended | element |
| `pkg/workspace/workspace.go` (`Workspace`), `pkg/workspace/mount.go` (`SaveRecord`), `pkg/workspace/instructions.go` (`WorkspaceDir`, `WorkDir`, `ReadInstructions`, `WriteInstructions`), `pkg/workspace/delegationstore.go` (`LoadDelegation`, `SaveDelegation`), `pkg/workspace/delegation.go` (`DelegationEdge.Validate`) | used as is |
| `pkg/gateway/rest_workspaces.go` (`handleWorkspacePost`) | pull shared create logic out if needed (same pattern as S1) |
| `pkg/memrooms/rooms.go` (`ResolveWorkspaceSharedRoom`), `pkg/agent/memory.go:986` (carry-forward naming) | used as is |
| `pkg/knowledge/index.go`, `pkg/knowledge/detect.go` | used as is (index rebuild check) |

**Size:** L. **Depends on:** S1, S4 (S2/S3 for full content). **Waits on:** — (Q4 decided: C).

---

## S6 — Replace and Merge, with snapshot

**Demo:** install B already has "Writer"; import a package with "Writer"; the preview warns "1 agent will be replaced; a copy is saved first"; confirm; the new Writer is active; the snapshot file is listed and importing it brings the old Writer back.

- Name-based clash detection per element type; preview lists clashes.
- Replace: snapshot (reuses Export) → add new → retire old (per Q2) → on failure re-import snapshot.
- Merge: add only non-clashing items.
- Core agents: never in a package, never replaced (founder P-Q1 B).
- Crash recovery: S1a's start-up recovery (revision 7) is extended to the Replace and Merge steps (snapshot, retire old).

| File | Change |
|---|---|
| `pkg/omnpkg/import.go`, `journal.go`, `modes.go` (**new**) + tests | modes and recovery |
| `pkg/gateway/rest_packages.go` | apply with mode + confirmation |
| `pkg/omnpkg/journal.go` recovery | extend S1a's start-up recovery to Replace/Merge (the boot call already exists from S1a) |
| `pkg/skills/pipeline.go` (`KindInstallReplace`) | used as is |

**Size:** M–L. **Depends on:** S1 (S5 for workspaces). **Waits on:** Q2.

---

## S7 — Whole system and Custom scopes

**Demo:** `omnipus export --system -o all.omnpkg` and `omnipus export --agent A --workspace W --skill s1 --no-memory`; inspect shows exactly the chosen items.

- Selection model: scope + element toggles + chosen IDs. Whole system = all custom agents, workspaces, registry skills, MCP servers (ADR D9). Custom = any subset. Built-in agents are skipped with a clear message (founder P-Q1 B).

| File | Change |
|---|---|
| `pkg/omnpkg/selection.go` (**new**) + tests | scope resolution |
| `contracts/components/schemas/PackageExportRequest.yaml` | selection fields |
| `pkg/app/internal/pkgcmd/command.go` | flags |

**Size:** S–M. **Depends on:** S2–S5. **Waits on:** Q3 (wording of what "whole system" is not).

---

## S8 — Settings screen

**Demo:** Settings → Data: choose scope, tick elements, pick agents/workspaces, "Export" downloads the file; "Import" uploads a file, shows the preview (what is added, what is replaced, which secrets to re-enter), Replace warning, confirm with password; result summary.

| File | Change |
|---|---|
| `src/components/settings/DataSection.tsx` | new Export / Import panels (old Backup panel kept or removed per Q3) |
| `src/components/settings/PackageExportPanel.tsx`, `PackageImportDialog.tsx` (**new**) + tests | UI |
| `src/lib/api.ts` | client calls using generated types |
| `tests/e2e/packages-export-import.spec.ts` (**new**) | the two E2E criteria from the issue |

**Size:** M–L. **Depends on:** S6, S7. **Waits on:** Q3.

---

## S9 — Session history (opt-in)

**Demo:** export a workspace with "Include chat history" ticked; import; the chats appear under the new agents with all messages; without the tick, no chats.

- Re-survey the session stores first (ADR F11: `sessions/<id>/`, per-agent session folders, `session_lifecycle/`, `session_messages/`, `tool_results/`).
- Session IDs regenerated; `agent_id`, `agent_ids`, `workspace_id`, `parent_session_id` remapped; memory `born_in` relinked.

| File | Change |
|---|---|
| `pkg/omnpkg/element_session.go` (**new**) + tests | element |
| `pkg/session/unified_meta_files.go` (on-disk meta shapes), `pkg/session/daypartition.go`, `pkg/session/lifecycle.go` | read; possibly a small write helper |
| `pkg/gateway/gateway_boot.go:1062,1082` | store locations |

**Size:** L (most uncertain slice). **Depends on:** S5.

---

## S10 — Claude Code plugin interop

**Demo:** point `omnipus import` at a Claude Code plugin folder or zip with `skills/`, `agents/*.md` and `.mcp.json`; skills and agents appear; MCP servers added switched off until secrets are entered. `omnipus export --plugin` writes a package that Claude Code accepts as a plugin.

- First step: check the current plugin layout in the Claude Code docs (context7) — ADR D8 mapping is inferred.
- Hooks and commands skipped with a notice.

| File | Change |
|---|---|
| `pkg/omnpkg/plugin_import.go`, `plugin_export.go` (**new**) + tests | mapping |
| `pkg/skills/project.go` (already reads `.claude/skills`) | reuse SKILL.md parsing |

**Size:** M. **Depends on:** S2, S3. **Waits on:** Q5.

---

## Total

About 8,000–11,000 changed lines across ten slices, roughly half tests. S1 is the largest single risk (it builds the engine and the shared agent-create function); after S1, the others are additions of one element type each.
