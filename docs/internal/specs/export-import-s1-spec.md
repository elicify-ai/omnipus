`Status: Final (review cap reached, revision 6), build S1a`

`ADR: [ADR — Export / Import packages (.omnpkg), epic elicify-ai/omnipus#1094](ADR.md)` (status Proposed, 2026-11-19, with the 2026-11-19 amendments for S1; slice plan in [SLICES.md](SLICES.md), founder questions in [QUESTIONS.md](QUESTIONS.md))

# Feature Specification: S1 — Move one custom agent to another install (command line)

**Created**: 2026-11-19. **Revised**: 2026-11-19 (revision 2) after grill round 1 (`GRILL-S1-r1.md`: FAIL, 2 blockers, 11 should-fixes, 3 polish), and 2026-11-19 (revision 3) after grill round 2 (`GRILL-S1-r2.md`: FAIL, 2 blockers, 7 should-fixes, 2 polish). Revision 6 (final), 2026-11-19, after grill round 5 (`GRILL-S1-r5.md`: PASS, 3 should-fixes; review cap reached). Revision 5, 2026-11-19, after grill round 4 (`GRILL-S1-r4.md`: FAIL, 1 blocker, 3 should-fixes). Revision 4, 2026-11-19, after grill round 3 (`GRILL-S1-r3.md`: FAIL, 1 blocker, 5 should-fixes; plus the S1b runtime-validation point from a second, overwritten round-3 report the chief relayed). Every finding is either fixed or declined, with its reason, in "Grill r1 resolution", "Grill r2 resolution", "Grill r3 resolution", "Grill r4 resolution" and "Grill r5 resolution" at the end. Revision 3 follows the team lead's direction: keep the promises small and testable, and move what the code cannot keep into Known Limits with the slice that will fix it, rather than adding machinery.
**Input**: `ADR.md`, `SLICES.md` (section S1), `QUESTIONS.md` and `GRILL-S1-r1.md` in `/workspace/omnipus-coordination/designs-20261119-export-import/`, plus the team lead's rulings of 2026-11-19 (section "Decided assumptions"). There is no interview-me output for this epic, and the founder's answers to Q1–Q5 are still open; S1 does not need them.
**Code base read**: `origin/release/v0.1.1` at `7faff5291` (the same commit the ADR and the grill used). Every existing file and symbol named below was checked at that commit with `git show` / `git grep`. Files marked **new** do not exist yet. GitNexus was not available, so the codebase context comes from direct reads (an allowed fallback).
**Output location note**: the repo's plan-spec skill puts specs in `docs/internal/specs/`. This draft lives in the coordination repo as the chief asked, and moves to `docs/internal/specs/export-import-s1-spec.md` when the epic's specs are committed.
**Epic**: elicify-ai/omnipus#1094 (Stage: Spec)
**Riders (candidates; the chief confirms the complete set before design approval)**: #1440 "Agent create (POST /api/v1/agents) silently drops fallback_models". It touches the file S1 refactors. The release code assigns the fallback list in `pkg/gateway/rest_agents_create.go::restAPICreateAgentPrepareAgent.validateAndBuildConfig`, but reading the assignment is not proof. S1 adds a create-then-GET test (`TestCreateExtraction_RESTUnchanged`), and the issue is closed only when that test passes. Not a rider: #1295 (removing `mcp_*` keys on update); S1 never removes keys from an existing agent.
**Agent tool field table**: S1 adds no agent field and no agent tool, and does not change any tool schema. It does change the shared create path, so "Field mapping" below covers every callable surface: REST create, REST update (which the app's agent editor uses), the `create_agent` tool, the `update_agent` tool and `set_agent_permissions`. A field-coverage test fails when a user-editable field has no import decision. The `create_agent` and `update_agent` tools keep their behaviour; only `create_agent`'s workspace-join code moves into a shared helper, and the tools' shared field applier is exported for import (regression tests listed).

---

## Overview

**What this is.** Today an agent you have built (its name, its prompt, its model and tool settings) is stuck on the machine where you made it. S1 lets the owner of an Omnipus install pack **one custom agent** into a single file (`writer.omnpkg`), copy it to another computer, look inside it without installing anything, and add it to the second install. The added agent gets a brand-new internal ID and joins the default workspace (or one you name). A **Main** agent gets its main chat and you can talk to it straight away. A **worker** (Subagent) is not a chat partner: it appears in the Team, and it becomes reachable the usual way, when you draw a delegation line to it from a Main agent in the Team graph.

**What is promised, plainly (small on purpose).**
- Import is a normal agent *create*. The same rules apply as when you create an agent today, so what arrives is the source agent **as the create rules would store it**. For example, spaces at the start and end of the prompt are trimmed, and settings that do not fit the new install are left out and listed. The section "Field mapping" says, field by field, what is kept, what changes and what is left out.
- If the import fails while the gateway is running, the rollback removes a fixed set of **places and records**, whoever wrote into them: the new agent's record, its home folder with everything inside it, its team entry and the delegation lines to or from it in the import's workspace. Then it asks the gateway to reload the live agent list; the answer says whether that reload was confirmed. Every other agent, workspace team and delegation rule is as before. A few harmless internal markers may differ (listed in "Rollback contract").
- The import is **not hidden** from the rest of the gateway while it runs. For a short time, another screen, script or reload can already see the new agent and use it: open a chat or a session with it, send it a message, or start work that saves a note in its home folder. **Rollback is not an undo for those requests.** A note saved in the agent's home folder is deleted with that folder. Chats and sessions live outside the folder and stay. Work that was already running can write into the home folder again after it was deleted. The docs give the final clean-up (stop the gateway, delete what is left, start it, delete the leftover chats) and say which chat cannot be deleted. That is Known Limit L16; S6 brings the automatic clean-up.
- If the gateway itself stops in the middle of an import, S1 does **not** clean up automatically (that comes in S6). The next upload shows a warning naming the half-imported agent, and the docs give a complete manual clean-up that also frees the upload slot.

**Why it matters.** This is the first, smallest slice of epic #1094 and the founder demo. It builds the shared engine every later slice adds to: the file format, the safety and integrity checks, the step-by-step import that can be undone, and the new-ID rule. Skills, connectors (MCP servers), memory, workspaces, Replace and Merge, and the Settings screen come in S2–S10.

**Who uses it.** The owner of a local Omnipus install: from the terminal (`omnipus export`, `omnipus import`, `omnipus package inspect|verify`), or from scripts through three new REST endpoints.

**The demo (from SLICES.md).**

```
Install A                                   Install B (fresh)
---------                                   -----------------
omnipus export --agent "Writer" \
   -o writer.omnpkg          ──copy file──►  omnipus package inspect writer.omnpkg
                                               (name, files, checksums; no server needed)
                                             omnipus import writer.omnpkg
                                               preview: "1 agent will be added: Writer"
                                               confirm → "Writer" added, new ID,
                                               member of the default workspace,
                                               (Main) chat works with the same prompt
```

The founder demo uses a **Main** agent. A native worker is part of S1a too, with its own acceptance criterion (US-3 AS-11).

---

## Decided assumptions (team-lead rulings, 2026-11-19)

| # | Decision | Reason | Where it shows up |
|---|---|---|---|
| DA-1 | **New-mode import does not ask for the password again.** No separate security sign-off is needed for S1. The re-entry step comes with Replace in S6. | The founder's rule is functionality first, and New mode only adds items: it never overwrites or removes anything. Creating an agent today also needs no password re-entry. The CLI signs in as a synthetic user `cli` with no password (`pkg/gateway/auth.go::resolveBearerIdentity`), so the current check (`pkg/gateway/rest_integrations_auth.go::requireReAuth`) could never pass from the CLI. | FR-046; Security and User Promises |
| DA-2 | **Extracting the workspace-join code from the `create_agent` tool is part of S1**, as named task T-4. S1 is re-estimated and split into S1a (the founder demo) and S1b (hardening the demo does not need). | Without team membership an agent cannot chat (`pkg/sysagent/tools/agent.go` create flow). | "Size and split"; FR-035 |
| DA-3 | **ADR.md gets a dated amendment** to CLI Minimization ("a thin one-shot task-runner over the engine"): the CLI may also run `export`, `import` and `package` commands, as gateway clients (`inspect`/`verify` read only the package file). No founder decision is needed. | The `records` command is the precedent for an owner one-shot command. | ADR.md "Amendments 2026-11-19" |
| DA-4 | **Smaller, testable guarantees for the two grill blockers.** Import follows the create rules (B-01). Rollback promises the logical state, with a listed set of allowed internal differences, and covers in-process failures only (B-02, S-04). | A promise the code cannot keep is worse than a smaller promise that is tested. | "Field mapping"; "Rollback contract"; Known Limits L4 |
| DA-5 | **Round 2 direction: no new machinery.** No isolation of an in-flight import from other readers, no new notifier, no change to REST create's existing behaviour. What the code cannot keep is a Known Limit with the slice that fixes it (L16, L17). | The team lead's direction after grill round 2. | "Rollback contract"; "Field mapping"; Known Limits |

## Dependence on founder questions (Q1–Q5)

| Question | Could it change S1? | What S1 does now |
|---|---|---|
| Q1 Built-in agents | No. S1 refuses to export built-in agents whatever the answer. | Refused with a clear message (FR-011). **[ASSUMED-Q1 = A]** affects only the word "yet" in that message. |
| Q2 What Replace does | No. S1 has New mode only. | Replace and Merge are refused (FR-030). |
| Q3 Keep Backup & Restore | No. | **[ASSUMED-Q3 = A]** Backup & Restore is unchanged (regression tests). |
| Q4 Mounted folders | No. | Not applicable. |
| Q5 Claude Code plugins | Only the layout, which ADR D2 already fixes. | **[ASSUMED-Q5 = A]** The S1 layout is ADR D2's; nothing plugin-shaped is written. |

---

## Size and split (re-estimate, DA-2)

The original estimate of about 1,800 changed lines was too low. The grill found work that was not in the file list: the shared create function with its field mapping, the workspace-join extraction, the coverage-check extraction, real-store rollback, the staging lifecycle, decoder limits and a small frontend refresh fix. The new estimate (changed lines, tests included, generated contract code excluded):

| Task | What | Production | Tests | Sub-slice |
|---|---|---|---|---|
| T-1 | Contracts: five schemas, three operations, error codes, regenerate | ~250 (YAML) | — | S1a |
| T-2 | `pkg/omnpkg` format: manifest, tar+zstd writer/reader, decoder limits, universal checks, agent layout checks, external-worker refusal (S1a), verify, inspect | ~670 | ~740 | S1a |
| T-3 | Shared create function: split HTTP input handling from validation and persistence; coverage check without the config-file rewrite; field mapping; control and alias table; export the tools' shared field applier; field-coverage test | ~350 | ~470 | S1a |
| T-4 | Workspace-join extraction from `create_agent` (join + self-row under one workspace lock), plus a matching leave helper for rollback | ~150 | ~200 | S1a |
| T-5 | Gateway handlers, staging lifecycle (including manual-recovery slot release), journal, rollback with live-list reload, single-agent main chat, audit rules, "unfinished import" warning, route with 64 MiB limit, reload outcomes (confirmed/pending), C1-failure branch (including partial C1), neutral C4 line, synchronised rollback cases (including the two home-writer cases) | ~630 | ~1,080 | S1a |
| T-6 | CLI: `export`, `import`, `package inspect|verify`, shared auto-start step | ~300 | ~250 | S1a |
| T-7 | Frontend: refresh workspace detail and delegation on `agent_created` | ~20 | ~60 | S1a |
| T-8 | Docs DOC-001…003, including the manual recovery procedure (with the final clean-up and complete session enumeration) | ~210 | — | S1a |
| T-9 | External workers (Subagent External): strip runtime values, `needs_secrets`, program-not-set warning; per-runtime field check before the shared applier; replaces the S1a refusal | ~130 | ~190 | **S1b** |
| T-10 | Unknown element types skipped (forward compatibility) | ~60 | ~100 | **S1b** |
| | **Total** | **~2,770** | **~3,090** | |

- **S1a ≈ 5,380 lines** (about 2,580 production and docs plus 2,800 tests). Revision 5 adds about 170 lines for the round-4 fixes (partial-C1 setup, the two real home-writer cases, the paged session-recovery test and its documentation). Revision 4 adds about 280 lines for the round-3 fixes (reload outcomes, the C1-failure branch, the alias table, upload-consumption tests, D7 rows 12–18, recovery step 4). Revision 2 said 4,050; that was an arithmetic error (its own numbers added up to 4,530, as grill round 2 noted), and revision 3 adds about 400 lines for the round-2 fixes. That is about twice size L (1,200–2,500). It is not split further, because every task in it is needed for the demo or for safe failure. It delivers the full founder demo for Main agents plus native workers. Until S1b lands, S1a refuses external workers on export **and on upload**, and refuses unknown element types on upload, each with a clear message.
- **S1b ≈ 480 lines (size M)**: external workers (including the per-runtime field check) and forward-compatible skipping. It can land in the same train or the next, and S2 needs T-10 before it ships. The "unfinished import" warning moved from S1b into S1a (T-5), so S1a's manual recovery works without S1b.
- These are estimates, not measurements.
- Do not cut further. A "format-only" slice cannot be used, and the demo needs T-1…T-8.

Requirements and scenarios that belong to S1b are tagged **[S1b]**. Everything else is S1a.

---

## Existing Codebase Context

### Symbols Involved

| Symbol (file::symbol) | Role | Context |
|---|---|---|
| `pkg/gateway/rest_agents_create.go::restAPI.createAgent`, `restAPICreateAgentPrepareAgent.validateCreateFields`, `.validateAndBuildConfig`, `restAPICreateAgent.buildToolConfig`, `.persistAgent`, `.publishResponse` | **modifies (extract)** | All agent-create rules live in this HTTP handler today: unknown skills are refused (`validateSkillIDs`, 400); workers need a description; a native agent needs a non-blank prompt; the prompt and description are trimmed; at most two fallback models, paired; a new UUID; the tool-policy seed. T-3 splits HTTP decoding from shared validation and persistence. |
| `pkg/gateway/rest_agents_update.go` (context-window rule: `context_window_override must be ≥ 1`) | calls (rule reused) | `memory_enabled` and `context_window_override` cannot be set at create, only on update; import applies them with the update path's rule (Field mapping). |
| `pkg/gateway/rest_agents.go::restAPI.withToolPolicyCoverageGuard` | **modifies (extract)** | Runs the coverage check under `configMu`, but goes through `pkg/gateway/rest_config.go::restAPI.writeConfigJSONLocked`, which rewrites `config.json` even when nothing changes, and `applyWrittenConfigLocked`, which refreshes the live config. T-3 extracts "check coverage under the config lock" so import never rewrites `config.json`. |
| `pkg/gateway/rest_omnipus_guards.go::refuseReservedAgentName`, `refuseAlwaysAskAllow`; `pkg/gateway/submitted_builtin_policy.go::validateSubmittedBuiltinToolPolicyMap`; `pkg/agentmutation/tool_policy.go::ToolPolicyChanges`; `pkg/gateway/gateway_sandbox.go::buildKnownBuiltinToolNames`; `pkg/coreagent/seed.go::NewCustomAgentToolsCfg`, `RestrictForWorkerTier` | calls | Name and tool-policy rules used through the shared function. |
| `pkg/agentstore/state.go::Store.CreateState`, `Store.MutateStateWithAgentMD`, `Store.DeleteState`, `ValidateAgentID`, `readOptional` | calls | Create writes the record and `SOUL.md`; `AGENT.md` is written separately; delete removes the record and `SOUL.md` but **not** `AGENT.md`. `readOptional` uses `os.ReadFile`, with no size limit and following links, so export needs its own bounded read (FR-018). |
| `pkg/skills/identity.go::BindGrants`, `EnsureGeneration`, `IsBuiltin`; `pkg/skills/loader.go::ValidSlug` | calls (through `CreateState`) | Binding a skill can stamp a generation marker on an installed skill that has none. This is harmless metadata, and it is an allowed difference after rollback. |
| `pkg/entity/store.go::Store.lockPath` | reads | Permanent lock files beside entity records; never deleted (allowed difference). |
| `pkg/sysagent/tools/agent.go::AgentCreateTool.joinWorkspaceTeam`, `AgentCreateTool.seedSelfEdgeLocked`, `cascadeCleanOneWorkspaceLocked`, `RunAgentDeleteCascade` | **modifies (extract)** / calls | Join and self-row in one workspace lock; `cascadeCleanOneWorkspaceLocked` removes one agent from one workspace's team and edges, re-reading fresh state. Both change the workspace's `updated_at`. `RunAgentDeleteCascade` cleans sessions and tasks for agents with history and is what Delete agent runs (Manual recovery step 1). Rollback does not use it (it keeps home files and stops before record deletion on its own warnings); rollback uses the boundary-based steps C1–C4 in "Rollback contract". `cascadeCleanOneWorkspaceLocked` can fail removing membership or saving delegation, which is why a C1 failure stops the rollback. |
| `pkg/gateway/rest_sessions.go::restAPI.createSessionHTTP`, `deleteSession`; `pkg/gateway/websocket_chat.go::persistUserMessage` | reads | Other requests can create ordinary sessions and messages for any registered agent, including a provisionally visible import. They live in the shared session store, outside the agent's home, so rollback does not remove them (L16). Ordinary sessions can be deleted by hand; a main chat is protected (409). Note for the chief: the 409 message says the main is "kept until normal retention", which does not match the retention code (next rows); that pre-existing text is not changed in S1. |
| `pkg/tools/memory.go::RememberTool.Execute`; `pkg/agent/memory_adapter.go::MemoryStoreAdapter.AppendLongTermToRoom`; `pkg/agent/memory.go::NewMemoryStore`, `MemoryStore.AppendLongTermToScope`; `pkg/memrooms/rooms.go::ResolveAgentPrivateRoom`; `pkg/memrooms/memory_file.go::WriteMemoryFile`; `pkg/fileutil/file.go::WriteFileAtomic`; `pkg/agent/loop_config.go::AgentLoop.ReloadProviderAndConfig` | reads (real writer in tests) | A turn's `remember` with room `private` saves a note under the agent's home `agents/X/`. `WriteFileAtomic` creates missing parent folders, so a write after C3 recreates the home. A reload keeps the old registry open for turns in flight, so a confirmed reload does not stop them. Basis for the location-based rollback boundary, AD-6 and D7 rows 19–20. |
| `pkg/gateway/rest_sessions.go::restAPI.listSessions`, `u18DefaultSessionPageLimit`; `pkg/agent/loop_session.go::AgentLoop.ListAllSessions`; `pkg/session/retention_sweep.go::UnifiedStore.RetentionSweep`, `retentionProtected` | reads | `GET /api/v1/sessions` returns 50 rows by default, applies the `agent_id` filter **after** paging, lists child sessions only with `flat=true`, and returns `next_cursor` while more rows exist. Retention never deletes a protected main chat's identity or newest content; it only expires older rolled content. Basis for Manual recovery step 4. |
| `pkg/gateway/main_session.go::restAPI.ensureMainSession`, `mainSessionPairEligible` | calls | Creates **one** agent's main chat in one workspace, only when the pair is eligible. Import does **not** use `ensureWorkspaceMains`, which provisions every member of the team. |
| `pkg/gateway/rest_auth.go::restAPI.triggerReloadAndWaitOutcome`; `pkg/gateway/rest_agents.go::restAPI.deleteAgent` (uses the same reload after a delete); `pkg/gateway/agent_created_broadcast.go::restAPI.emitAgentCreated` | calls | The live agent list is rebuilt from the store by a full reload. Import uses that reload to publish after the commit point, and again after a rollback. `fastAgentUpsert` is not used: it expects the config write that import skips, and would fall back to the same reload anyway. |
| `pkg/agentstore/store.go::Store.SetNotifier` | reads | The store's change notifier is optional and **not wired** in production at `7faff5291`, so writing or deleting a record does not change the live list by itself. |
| `pkg/sysagent/tools/agent_apply_args.go::applySharedConfigArgs` (and `agentCreateArgNames`, `agentUpdateArgNames`) | **modifies (export only)** | The agent tools' rules for `memory_enabled`, `voice`, `context_window_override` and `model_params`. Import calls it (exported, unchanged) for the three fields REST create does not carry. |
| `pkg/config/config_agents.go::AgentConfig.IsChatTarget`, `IsExternalCLIWorker` | calls | Only Main agents are chat targets; external workers are refused on upload in S1a. |
| `pkg/gateway/rest_agent_executor.go::refuseForbiddenExecutorFields`; `contracts/components/schemas/AgentCreateRequestSubagent3p.yaml`, `ExecutorConfigCreate.yaml` | calls / reads **[S1b]** | An external worker's program is set in Settings → Third-party agents, never per agent. Per agent it has only `cli`, `env_overrides` and `cli_args`. |
| `pkg/agent/loop_wire.go` (ADR-037 comment); Remove the Global Per-Agent Delegation Policy (`docs/internal/architecture/ADR-037-remove-global-delegation-policy.md`) | reads | `subagents.allow_agents` is retired and has no effect; delegation is the per-workspace graph. |
| `pkg/gateway/rest.go::restAPI.withAuth`, `withAuthAndBodyLimit`, `adminWrap`; `pkg/gateway/middleware/bypass_gate.go::RequireNotBypass` | calls | `withAuth` caps every body at 1 MiB. The upload route uses `withAuthAndBodyLimit` with 64 MiB plus `RequireNotBypass` (FR-045). |
| `pkg/gateway/rest_settings.go::extractTarGz`, `isProtectedRestoreNamespace`, `maxRestoreFileSize`, `createTarGz` | **modifies (move predicates)** | Restore **skips** some unsafe or protected entries, whereas package upload **refuses** them. Only the yes/no checks are moved; each caller keeps its own policy (skip versus refuse). |
| `pkg/gateway/gateway_boot.go` (`ensureDefaultWorkspace` call) | reads | If creating the default workspace fails, boot logs it and continues, so the default workspace may be missing (FR-035). |
| `src/lib/api/agents.ts::invalidateAfterAgentCreate`; `src/lib/api/workspaces.ts::workspacesQueryKeys`; `src/store/chat/slices/frames.ts` (`agent_created`); `src/components/workspaces/team/TeamPanel.tsx::TeamPanel`; `src/components/workspaces/WorkspaceTeamTab.tsx::WorkspaceTeamTab` | **modifies (T-7)** | On `agent_created` the app refreshes the agent list and the workspace **list** only; an open Team tab's workspace detail and delegation graph stay stale. |
| `pkg/app/app.go::NewRootCommand`, `autoStartAndRetry`, `autoStartAndRetryWith`; `pkg/app/internal/clitoken/clitoken.go::LoadCLIToken`; `pkg/app/app_test.go::TestNewRootCommand_KeptCommandsPresent` | modifies / calls | Commands, the auto-start steps (generalised to any retry step), the token, and the exact command-list test. |
| `github.com/klauspost/compress@v1.20.2/zstd` `WithDecoderMaxWindow`, `WithDecoderMaxMemory`, `WithDecoderConcurrency`, `WithWindowSize` | calls | The defaults allow a 512 MiB window and up to 64 GiB of output; S1 sets limits (FR-008). |
| `pkg/fspolicy/inventory.go::ProtectedInventoryNames` | reads | `backups` is protected, so agents cannot touch staging. |

### Impact Assessment

| Symbol Modified | Risk Level | d=1 Dependents | d=2 Dependents |
|---|---|---|---|
| Create pipeline in `rest_agents_create.go` (T-3) | **HIGH** ⚠️ | `restAPI.createAgent` (route `POST /api/v1/agents`); `rest_agents_create_test.go`, `rest_agents_adr090_test.go`, `rest_max_tool_iterations_api_test.go`, `rest_tool_policy_write_guard_test.go`, `judge_system_agent_test.go`, `reload_coalescing_test.go` | Omnipus-driven agent creation; scripts |
| Coverage check split from `withToolPolicyCoverageGuard` (T-3) | **MEDIUM-HIGH** ⚠️ | All callers of `withToolPolicyCoverageGuard` (create and update paths) | Every agent create and update |
| Workspace join extraction (T-4) | **MEDIUM** ⚠️ | `create_agent` tool; `agent_create_mains_gate_test.go` and the other `create_agent` tests | Omnipus "Add agent" flow |
| `applySharedConfigArgs` exported (T-3) | LOW | `create_agent`, `update_agent`; `TestWU25EditableFields_MatchCallableUpdateContract`, `TestUpdateAgent_StoresAllParityFieldsAndRevision` | Agent create/update through tools (no behaviour change) |
| `extractTarGz` / `isProtectedRestoreNamespace` predicates (T-2) | LOW-MEDIUM | `HandleRestore`, `createTarGz`; `rest_backup_test.go`, `mail_backup_exclusion_red_test.go` | Settings → Data backup |
| `invalidateAfterAgentCreate` (T-7) | LOW | `agent_created` frame handler | Agent picker, Team tab |
| `autoStartAndRetryWith`, `NewRootCommand` (T-6) | LOW | Root run path; command-list test | Agents named `export`/`import`/`package` (Known Limits L9) |

⚠️ The architect signs off the T-3 and T-4 shapes (inputs, partial results, lock ownership) before GREEN on those tasks. RED work on T-1, T-2 and T-6 does not wait for that.

### Relevant Execution Flows

| Flow | Relevance |
|---|---|
| REST agent create | Import runs the same rules through the shared function |
| `create_agent` tool (validate → persist → join → publish → main chat) | Import follows the same order |
| Workspace membership removal (`cascadeCleanOneWorkspaceLocked`) | Rollback of membership and self-row |
| Backup/Restore | Shares the moved predicates; behaviour unchanged |
| CLI run with auto-start | Reused by export/import |
| `agent_created` → app cache refresh | Extended to workspace detail and delegation |

---

## Contract Changes (contract-first — Hard Constraint #8)

Written and regenerated (`scripts/gen-contracts.sh`) before any Go or TypeScript code, in one commit with the generated files. The architect fixes the exact YAML.

| Schema | New / Changed | File | Notes |
|---|---|---|---|
| `PackageManifest` | new | `contracts/components/schemas/PackageManifest.yaml` | `format` (const `omnpkg`), `format_version` (integer ≥ 1), `scope` (enum `workspace`, `system`, `custom`; S1 writes `custom`), `elements[]` (`type` open string, `schema` integer ≥ 1, `source_id`, `name`, `files[]`), `counts` (type → integer), `files` (path → `{sha256, size}`), `source_build`, `created_at`, `includes_sessions`, `needs_secrets[]` (`name`, `used_by`, `kind`; S1b writes `executor_env`). Extra unknown properties allowed. |
| `PackageExportRequest` | new | `contracts/components/schemas/PackageExportRequest.yaml` | `agent_ids[]` (S1 accepts exactly one). |
| `PackageImportPreview` | new | `contracts/components/schemas/PackageImportPreview.yaml` | `staging_id`, `expires_at`, `manifest`, `will_add[]` (`type`, `source_id`, `source_name`, `proposed_name`, `renamed`), `skipped_elements[]`, `warnings[]` (`code`, `element_source_id`, `message`, `items[]`), `needs_secrets[]`. Everything in it is **provisional** (FR-021). |
| `PackageImportApplyRequest` | new | `contracts/components/schemas/PackageImportApplyRequest.yaml` | `mode` (enum `replace`, `merge`, `new`; required; S1 accepts `new` only), `workspace_id` (optional). |
| `PackageImportResult` | new | `contracts/components/schemas/PackageImportResult.yaml` | `mode`, `created[]` (`type`, `source_id`, `new_id`, `name`, `workspace_id`), `warnings[]` (same shape; includes `changed_since_preview`), `needs_secrets[]`, `activation` (`active`/`pending` + message). |

Errors use the existing `contracts/components/schemas/ErrorResponse.yaml`; its `code` description is extended with the new codes listed under Machine-Verifiable Constraints.

`contracts/openapi.yaml` gains:

| Operation | Request | Success |
|---|---|---|
| `POST /api/v1/packages/export` | `application/json` `PackageExportRequest` | `200` `application/octet-stream` (`format: binary`), `Content-Disposition: attachment; filename="<safe-name>.omnpkg"` |
| `POST /api/v1/packages/import` | `application/octet-stream` | `201` `PackageImportPreview` |
| `POST /api/v1/packages/import/{staging_id}/apply` | `application/json` `PackageImportApplyRequest` | `200` `PackageImportResult` |

No existing route starts with `/api/v1/packages`. The backup schemas and routes are not changed.

## API and Data

- **Package file**: tar + zstd. `manifest.json` comes first. In S1 the agent element owns `omnipus/agents/<source-id>/agent.json`, plus `SOUL.md` (native agents) and, when present, `AGENT.md` (ADR D2).
- **What an import stores** (the complete list, with its compensation in "Rollback contract"): one agent record, its `SOUL.md` and `AGENT.md`, its agent folder, one `core_team` entry and one self-delegation row in one workspace, (Main only) its main chat, an audit entry, and internal markers (a lock file, possibly a skill generation stamp, the workspace's `updated_at`).
- **What an import never stores**: anything in `config.json`, credentials, connectors, other agents, other workspaces' records.
- **Temporary data**: `$OMNIPUS_HOME/backups/.staging/<staging_id>/` (package payload plus `journal.json`). Agents cannot read or write it.

---

## Field mapping (import versus the create rules) — resolves B-01, R2-B-01, R2-S-07

Import is a create. The shared create function takes a domain input (not the REST request type) built from the package record. The REST handler keeps its own decoding and normalisation and calls the same function, so **REST create's behaviour does not change** in S1. The table is the oracle for `TestFieldMapping_Export`, `TestFieldMapping_Import`, the round-trip test and the field-coverage test.

Columns (all checked at `7faff5291`):
- **REST create**: `AgentCreateRequestMain/Subagent/Subagent3p.yaml` and what `rest_agents_create.go` stores. "accepted, not stored" means the request may carry it but the record does not keep it.
- **REST update**: `AgentUpdateRequest.yaml`. The app's agent editor uses REST create and update, so these two columns also describe the screen.
- **`create_agent`** / **`update_agent`** (agent tools): `pkg/sysagent/tools/agent_apply_args.go::agentCreateArgNames`, `agentUpdateArgNames`. On an existing agent, tool policies and connector assignments change only through `set_agent_permissions` ("perm" below), by design.
- "Listed" means the item appears in the preview and result warnings.

| Field (stored JSON name) | Export | Import outcome | REST create | REST update | `create_agent` | `update_agent` | Notes |
|---|---|---|---|---|---|---|---|
| `id` | kept (source ID) | **new UUID** | server-made | — | server-made | — (`id` selects) | ADR D5 |
| `name` | kept | create rule (trim, ≤ 100 characters) + clash suffix (FR-032) | yes | yes | yes | yes | |
| `description` | kept | **trimmed** (create rule); a worker with an empty description is refused at export and upload (`element_not_creatable`) | yes | yes | yes | yes | |
| `type` | kept | custom → Main; worker, native → Subagent; worker, external → Subagent (External) **[S1b]**, refused in S1a (`external_worker_not_supported_yet`); `core`/`system` refused | yes (`type`) | no | yes (`agent_type`) | no | Type cannot change after create |
| `SOUL.md` | packaged byte-identical | stored **with leading and trailing whitespace removed** (create rule). A native agent whose prompt is blank is refused at export (`agent_prompt_missing`) and at upload (`element_not_creatable`). External workers have no prompt; a `SOUL.md` in their element is refused (`invalid_element`). | yes (`soul`) | yes | yes | yes | The inner text is never changed |
| `AGENT.md` | packaged byte-identical | stored **byte-identical**, written right after the create | no | no | no | yes (`agent_md`) | Import-only at create time |
| `model.primary`, `model.provider` | kept | kept (provider name only); not configured on target → listed `provider_not_configured` | yes | yes | yes | yes | |
| `model.fallbacks` (legacy strings) | kept | **left out**, listed `field_not_carried` | no | no | no (refused as retired) | no | Legacy; `fallback_models` is current |
| `fallback_models[].model`, `.provider` | kept | create rule (≤ 2 entries, paired); a violation is refused at upload | yes | yes | yes | yes | Rider #1440 |
| `fallback_models[].reasoning_effort` | kept | kept (create rule) | yes | yes | yes | yes | |
| `reasoning_effort` | kept | kept | yes (native) | yes | yes | yes | |
| `max_tool_iterations` | kept | create rule (1–1000 and ≤ the target's global limit); refused at upload or apply with `element_not_creatable`, `field: max_tool_iterations` | yes | yes | yes | yes | |
| `model_params.temperature`, `.max_tokens` | kept | kept (create rule; other keys refused) | yes (native) | yes | yes | yes | |
| `skills` | kept | names installed on the target kept and bound (listed `skill_bound_local`); others dropped (listed `skill_dropped`) **before** the create rule runs, because that rule refuses unknown skills | yes | yes | yes | yes | **Import transformation** |
| `skill_grants` | **removed** | rebuilt by the store | no | no | no | no | |
| `tools.builtin.policies` | kept | sparse changes over the type's seed: unknown tool names dropped (`tool_policy_dropped`); `allow` on an always-ask tool becomes `ask` (`tool_policy_tightened`); missing tools get the seed value | yes | yes | yes (`tool_policy_changes`) | perm | **Import transformation** |
| `tools.mcp.servers` | kept (so the preview can list them) | **always dropped** (`mcp_binding_dropped`) | yes | yes | yes (`mcp_servers`) | perm | **Import transformation**; ADR D5 deviation until S3 |
| `figure`, `role`, `color` | kept | create rule: canonical values kept; any other non-empty value is **refused at upload** (`element_not_creatable`, `field`), as REST create refuses it. Export only writes values the source install accepted, so this only affects hand-made or damaged packages | yes | yes | yes | yes | No import-only normalisation |
| `voice` | kept | **Main only**: applied with the `create_agent` tool's rule (`applySharedConfigArgs`) in the same create write. A worker with a non-empty voice is refused at upload (`element_not_creatable`, `field: voice`) | **accepted, not stored** (existing behaviour, unchanged; Known Limit L17) | yes | yes | yes | Import matches `create_agent`, not REST create |
| `memory_enabled` | kept | **native agents**: applied with the `create_agent` tool's rule in the same create write. **External workers [S1b]**: refused at upload (`element_not_creatable`, `field: memory_enabled`), because `create_agent` runs `agentmutation.ValidateFields` first and its `externalUnsupported` list forbids this field for external runtimes | no | yes | yes (native) | yes (native) | Import matches `create_agent`, including its runtime check |
| `context_window_override` | kept | **native agents**: applied with the `create_agent` tool's rule (positive integer) in the same create write; a violation is refused at upload. **External workers [S1b]**: refused at upload, as above | no | yes | yes (native) | yes (native) | Import matches `create_agent`, including its runtime check |
| `subagents.allow_agents` | **removed** | not carried | no | no | no | no | Retired by "Remove the Global Per-Agent Delegation Policy"; no effect |
| `subagents.model` | **removed** | not carried | no | no | no | no | Not read by any production code at `7faff5291` |
| `subagents.executor.kind`, `.cli` | kept | kept **[S1b]** | yes (3p) | `.cli` locked | yes (`cli`) | no | |
| `subagents.executor.env_overrides`, `.cli_args` | **removed**; env names go into `needs_secrets` **[S1b]** | not carried; listed `runtime_settings_removed`, pointing to Edit → Runtime **[S1b]** | yes (3p) | yes | yes | yes | Possible secrets |
| `subagents.executor.cli_path` | **removed** **[S1b]** | never carried; the program comes from Settings → Third-party agents | refused | refused | no | no | Host-level setting |
| `workspace` (home path) | **removed** | new default home | no | no | no | no | ADR F2 |
| `created_at`, `updated_at`, `locked` | **removed** | set by the store; `locked` false | no | no | no | no | |

**Control and alias names (not agent properties; never stored from the package).** These appear in the callable or schema field lists but are selectors, version checks, destinations, removal commands or request-side names for rows above:

| Request name | Where it appears | Kind | Import decision |
|---|---|---|---|
| `revision` | `update_agent`, `delete_agent`, `AgentUpdateRequest` | version check of an existing record | **Not transported.** The new agent gets its own revision from the store; the source revision is never used |
| `workspace_id` (`workspaceIDParam`) | `create_agent` | destination | **Not taken from the package.** The destination comes from the apply request (`workspace_id`) or the default workspace |
| `env_overrides_remove` | `update_agent` | removal command on an existing worker | **Not transported** (there is nothing to remove on a new agent) |
| `id` | `update_agent` | selector | Not transported (row `id`: new UUID) |
| `soul` | REST create/update, tools | alias | → row `SOUL.md` |
| `agent_type` (tools), `type` (REST) | create | alias | → row `type` |
| `tools_cfg` | REST create/update | alias | → rows `tools.builtin.policies` and `tools.mcp.servers` (the existing capability rows) |
| `tool_policy_changes` | REST, `create_agent` | alias (sparse change form) | → row `tools.builtin.policies` |
| `mcp_servers` | REST, `create_agent` | alias | → row `tools.mcp.servers` |
| `executor` (REST), `cli` (tools) | create/update | alias | → rows `subagents.executor.*` **[S1b]** |
| `agent_md` | `update_agent` | alias | → row `AGENT.md` |
| `model`, `provider` | all | alias | → row `model.primary`, `model.provider` |

**Parity rule (narrowed in revision 3).** Two checks, both on the *normalised* input, never on raw package values:
1. **Import versus REST create** (`TestImportCreateParity`): for every field REST create stores, the same normalised value gives the same stored value through both paths. Excluded on purpose: the three import transformations (skills dropped, connector assignments dropped, tool policies dropped or tightened), `voice`, `memory_enabled`, `context_window_override`, `AGENT.md`, `id`, the name suffix and timestamps.
2. **Import versus the `create_agent` tool** (same test, second table): `voice`, `memory_enabled` and `context_window_override` are stored exactly as `create_agent` stores them.

**Field-coverage rule** (`TestImportFieldCoverage`, new): the test holds two lists: the stored-field rows of the main table, and the control/alias table above (an alias resolves to its stored-field rows; a control name carries its reasoned decision). For every name in `agentCreateArgNames`, `agentUpdateArgNames`, the three REST create schemas and `AgentUpdateRequest`, it resolves the name through the alias table and checks that each resolved leaf (including nested leaves such as `fallback_models[].reasoning_effort` and `model_params.temperature`) has a row, or that the name is a listed control name. It fails on any unresolved name, so a new user-editable field cannot be added without an import decision, and there is no unlisted "ignore" set. Where `pkg/agentmutation/policy.go::MappingRows` and `ExcludedRows` already describe a REST/tool mapping or a deliberate exclusion, the test reuses that vocabulary instead of a second spelling. Starting state: at `7faff5291` every current name resolves (checked by hand against the lists above, including `revision`, `workspace_id`, `env_overrides_remove` and `tools_cfg`). The existing `TestWU25EditableFields_MatchCallableUpdateContract` and `TestUpdateAgent_StoresAllParityFieldsAndRevision` stay unchanged as regression tests for the tool side.

---

## Rollback contract — resolves B-02, S-04, R2-B-02, R2-S-01

**How the live agent list works today (the basis for this contract).** The list the app and `GET /api/v1/agents` show is the gateway's in-memory roster. It changes only when a reload rebuilds it from the store (`triggerReloadAndWaitOutcome`, the same reload `deleteAgent` uses). The store's own change notifier is not wired in production, so writing a record does **not** add it to the list, and deleting a record does not remove it.

**Commit point.** An import is *committed* once the self-delegation row is written. Before that, any failure is rolled back. After it, the steps that remain can only add warnings; they never undo the import.

**Effect inventory and compensation** (in the order of the writes):

| # | Durable effect | Compensation on rollback (run in this order) | After rollback |
|---|---|---|---|
| E1 | Journal entry "started, new ID X" | marked "rolled back" (or "incomplete" with leftovers) | Kept until the staging is retired |
| E2 | Coverage check under `configMu` (no file write) | none needed | `config.json` byte-identical |
| E3 | Skill generation stamp on an installed skill that had none (through `BindGrants`) | **not undone** | Allowed difference AD-1 |
| E4 | Agent record `entities/agents/X.json` + `agents/X/SOUL.md` (`CreateState`). Not in the live list. | step C2: `DeleteState` | Absent |
| E5 | `agents/X/AGENT.md` | step C3: the folder is deleted | Absent |
| E6 | Agent folder `agents/X/` | step C3: deleted **with everything inside it, whoever wrote it**: the import's files and anything another request wrote there while X was visible (for example a private memory note). The rollback boundary is the folder, not the writer | Absent; a request already running for X can recreate it after C3 (AD-6) |
| E7 | Entity lock file for X | **never deleted** (it keeps locking correct) | Allowed difference AD-2 |
| E8 | X added to the workspace's `core_team` | step C1: removed by the shared leave helper (T-4), re-reading the workspace under its lock, so other people's edits are kept | Team identical except AD-3 |
| E9 | X's self-delegation row | step C1: the same helper removes **every** delegation edge involving X in that workspace, including one another request added while X was visible | Edges identical, except that no edge involves X |
| — | **Commit point** | — | — |
| P1 | Live publish: `triggerReloadAndWaitOutcome` | not rolled back. Three outcomes: **confirmed** → go on to P2; **error** or **timeout** (not confirmed) → `activation: "pending"`, **no** `agent_created`, and the result tells the caller to restart the gateway (or wait and check the agent list) before using the agent | — |
| P2 | `agent_created` (`emitAgentCreated`) | sent **only after P1 is confirmed**; not rolled back | — |
| P3 | Main chat for X only (`ensureMainSession`, when `mainSessionPairEligible`; Main agents only; only after P1 is confirmed) | not rolled back; failure → warning `main_chat_pending` | — |
| P4 | Audit entry | failure → warning `audit_failed` | — |

**Compensation steps and their failure rules** (no new recovery framework; each rule just says when to stop):
- **C1**: remove X's team entry and every delegation edge involving X in the workspace, with the shared leave helper (membership first, then the delegation save, which only happens when an edge involving X exists). **If C1 fails**, the rollback **stops here**: C2 and C3 are skipped, so the record and folder stay. C1 can fail in two places: the membership removal fails (X is still a member), or the membership is removed and then the delegation save fails (**partial C1**: X is no longer a member, but an edge involving X remains). Either way X stays deletable by the normal Delete agent, which cleans team entries and edges naming X in every workspace (`cascadeCleanAgentWorkspaceReferences`). The answer is `import_rollback_incomplete` with `details.failed_step: "C1"`.
- **C2**: `DeleteState` for the record and `SOUL.md`. `DeleteState` removes the entity first and `SOUL.md` second, so a **partial C2** leaves at most `SOUL.md` in the folder and no readable record. In that case the rollback continues with C3 (the folder is inside the rollback boundary), and only reports incomplete if C3 also fails.
- **C3**: delete the folder `agents/X/` with everything in it, whoever wrote it. If it fails, the leftover is folder-only.
- **C4**: always attempted, whatever happened in C1–C3: a full reload (`triggerReloadAndWaitOutcome`). Confirmed → the live list matches the disk. **Error or timeout** → the answer says so (`details.activation: "pending"`), and the CLI adds, after the outcome line (complete or incomplete rollback), the neutral line "The gateway did not confirm that it reloaded its agent list. Restart the gateway, then check the agent list." It never says "nothing was imported" by itself, because after a C1 failure the record is still there.

So a leftover inside the rollback boundary is always one of these shapes, each covered by Manual recovery: **(a)** record and folder present, X still a member (C1 membership removal failed) or not (C2 failed); **(a2)** record and folder present, X not a member, an edge involving X remains (partial C1); **(b)** only the folder `agents/X/`, possibly with `SOUL.md` (partial C2, or C3 failed); **(c)** nothing; **(d)** after a complete rollback, `agents/X/` recreated by a request that was already running for X (AD-6).

The broad `RunAgentDeleteCascade` is not used during rollback. It keeps unrelated agent-home files (leaving E5/E6 behind) and stops before record deletion when its own clean-up warns. The absence of `agent_created` is **not** proof that X has no history: other requests may have used X while it was visible (next paragraph).

**What rollback covers, and what it does not (Known Limit L16).** The rollback is defined by the **places and records it removes, not by who wrote them**. An import is not hidden from other requests while it runs (DA-5: no isolation layer). Between E4 and C4, any reload can put X in the live list, and then other requests can use X like any agent. So the rule is:
- **Inside the boundary, removed whoever wrote it:** X's record (`entities/agents/X.json`, C2), X's home folder `agents/X/` **with everything inside it** (C3; that includes content other requests wrote there while X was visible, for example a private memory note saved by a turn's `remember` tool), X's team entry and every delegation edge involving X in the import's workspace (C1).
- **Outside the boundary, kept:** everything other requests created for X elsewhere. At `7faff5291` that includes at least:
  - **ordinary sessions** for X created through the sessions API (`POST /api/v1/sessions`, `createSessionHTTP`; it needs a registered agent, not workspace membership), with their folders, metadata and history, in the shared session store;
  - **new chats with messages**: a chat message sent to X over the WebSocket starts a new session in the shared store and saves the message before the agent is asked to answer (`websocket_chat.go`: `resolveTargetAgent`, `mintSession`, `persistUserMessage`), so the leftover can be a **populated** chat;
  - **X's main chat** in a workspace, if an open workspace view prepared it after E8 (`handleWorkspaceGet` → main-session preparation), plus any messages sent to it;
  - anything else such a request writes for X outside `agents/X/` (for example membership or edges for X in a workspace other than the import's, task or audit entries, or files a tool call wrote elsewhere).
- **Writers already running can come back after C3:** a turn admitted before compensation keeps running with X's in-memory settings (a reload, even a confirmed one, keeps the old registry for turns in flight and does not stop them). If it writes into X's home after C3, for example a private memory note, the writer recreates `agents/X/` (it creates missing parent folders). That content is AD-6. It is removed by the **final clean-up** (Manual recovery), which first stops the gateway so that no writer for X is left.

What the owner can do: once C4 is confirmed (or after a restart) X is no longer in the live list, so **no new work** can start for X; work already running ends on its own or with the gateway stop of the final clean-up; then the leftover folder (if any) and the ordinary sessions are deleted (Manual recovery steps 2 and 4). A protected main chat cannot be deleted in S1 and is **not** removed by normal retention either: its identity and newest content can remain indefinitely (L16). S6 adds the automatic clean-up. These limits apply only when another request used X during the short window; the normal CLI import with nobody else using the install leaves no such residue. **Founder acceptance of this narrower promise is still open** (Clarifications, open founder question; the chief owns it).

**Allowed differences after a rollback** (the complete list):
- **AD-1**: a skill generation stamp added to an installed skill that had none.
- **AD-2**: an entity lock file for the new ID.
- **AD-3**: the workspace's `updated_at` timestamp.
- **AD-4**: the audit log, the gateway log, and the staging folder's journal.
- **AD-5**: **residue outside the rollback boundary** that requests other than the import wrote for X while X was visible (L16): sessions in the shared session store (ordinary sessions and chats, with or without messages), transcripts, main chats, membership or delegation edges for X in workspaces other than W, task or audit entries, files written outside `agents/X/`. Not removed by the rollback; ordinary sessions are removed by Manual recovery step 4 and other workspaces' references by step 5; a protected main chat stays (L16).
- **AD-6**: **home content recreated after C3** by a request already running for X (for example a private memory note written after the folder was deleted). Only possible when such a request was admitted while X was visible. Removed by the final clean-up (Manual recovery).

Content another request wrote **inside** `agents/X/` **before** C3 is not a difference: C3 deletes it, by design.

**What the rollback oracle checks** (`TestPackagesApply_Rollback`, real store, **wired** reload; a test setup where the reload pipeline is not configured counts as "confirmed" by design and cannot prove roster synchronisation, so it is not used for checks 1 and 7):
1. The set of agent IDs in the store equals the set before. **If** C4 was confirmed, `GET /api/v1/agents` after the answer equals the set before; if it was not confirmed, the answer carries `details.activation: "pending"` and the oracle checks that instead.
2. Every pre-existing agent's record, `SOUL.md` and `AGENT.md` are byte-identical to before.
3. The import workspace W's `core_team` and delegation edges equal what they were before, plus any edit another writer completed during the import, **minus** X's team entry and every edge involving X. **Every other workspace** equals what it was before plus any edit another writer completed during the import; X references another request added there (membership or edges) are AD-5 residue, not removed by the rollback (D7 row 21).
4. `config.json` is byte-identical.
5. `entities/agents/X.json` does not exist, and `agents/X/` does not exist, **including anything another request wrote there before C3** (D7 row 19). Only exception: when the test itself lets an already-admitted writer write after C3 (D7 row 20), `agents/X/` holds exactly the note file with the expected content in X's private room `agents/X/.omnipus/`, plus the derived artifacts that this real write creates: the persistent search index under `agents/X/.omnipus/.index/bleve/` (cold index, opened on this write) and any signature bookkeeping file the writer keeps in the room. The test identifies that exact list from a control run of the same writer into an empty folder; it does not accept any other file. In particular the record, `SOUL.md`, `AGENT.md` and any other home content must be absent (AD-6). After the final clean-up, the whole `agents/X/` does not exist.
6. Every other file under the data folder is byte-identical, except AD-1…AD-4, the AD-5 residue that the test itself created through other requests outside the boundary, and AD-6 in D7 row 20 (and nothing else for X).
7. No `agent_created` frame for X was sent.

**Synchronised cases for the non-isolated window** (lock-compatible; test hooks, no sleeps, the production lock never bypassed):
- **D7 row 10 (reader)**: E4 done; an unrelated reload is forced and confirmed (X is now listed); the self-row write fails; the join helper releases W's lock; a hook holds C1; the test then sends the real workspace-detail request for W and **waits for its HTTP response** (it may prepare X's main chat); only then is C1 released. Expected: `import_rolled_back`; checks 1–7; the residue is exactly X's main chat in W (if the read prepared one), reported by the test as AD-5. The test asserts the read actually completed before C1 (response received, hook order recorded), so it cannot pass without exercising the race.
- **D7 row 12 (populated session)**: same start (E4, confirmed reload, self-row fails, join lock released, C1 held). The test then (a) creates an **ordinary** session for X through `POST /api/v1/sessions`, and (b) sends one real chat message to X over the WebSocket and waits until that message is saved in the transcript; then C1 is released. Expected: `import_rolled_back`; checks 1–7 pass; both sessions and the saved message **still exist** (AD-5), and the answer's note says other requests' sessions are not removed. After C4 is confirmed, a new chat message to X is refused because X is no longer a registered agent. Following Manual recovery step 4 deletes both sessions with `DELETE /api/v1/sessions/<id>`, after which no session for X except a protected main chat remains.
- **D7 row 14 (partial C1, reachable delegation-save fault)**: E4 and E8 done; the self-row write fails; the join helper releases W's lock; a hook holds C1. The test then makes a **separate valid graph edit** through `PUT /api/v1/workspaces/W/delegation` with the current graph plus an edge A → X (A an existing member of W; the architect confirms the exact valid edit when fixing the T-4 shapes) and waits for its 200, so an edge the leave helper must remove is persisted. A fault is injected into C1's own delegation save; C1 is released. Expected: `import_rollback_incomplete`, `failed_step: "C1"`; the test asserts that the save fault was actually reached (hook counter), that the membership removal had already succeeded (X not in W's team), that the edge A → X is still stored, that C2 and C3 did not run (record and folder present), and that C4 was attempted. This is shape (a2); D15 row 8 recovers it.
- **D7 row 19 (home write before C3)**: E4, unrelated reload confirmed, self-row fails, join lock released, C1 held. The test constructs X's writer while the home exists and saves a private memory note for X with the **real writer chain a turn uses** (`RememberTool.Execute` with room `private`, over `MemoryStoreAdapter` and `NewMemoryStore` for X's home; no model call) and asserts that the tool reported success and the note file exists under `agents/X/`. Then C1 is released. Expected: `import_rolled_back`; checks 1–7; the note was deleted with `agents/X/` (check 5); the answer's note says content in the agent's home folder is deleted.
- **D7 row 20 (home write after C3)**: same start. The test **constructs and keeps** X's writer (`NewMemoryStore` for X's home, `MemoryStoreAdapter`, `RememberTool`) **before C3**, with no index opened yet (cold index; constructing it after C3 would itself recreate folders and prove nothing). A hook holds the rollback after C3 and before C4; the test asserts `agents/X/` is absent at that moment, then makes the private `remember` call through the kept writer and asserts the tool reported success; the write recreates `agents/X/`. Then C4 runs (confirmed). Expected: `import_rolled_back`; checks 1–7 with the check-5 exception: `agents/X/` holds exactly the note file with the expected content in X's private room `agents/X/.omnipus/`, plus the derived artifacts that this real write creates: the persistent search index under `agents/X/.omnipus/.index/bleve/` (cold index, opened on this write) and any signature bookkeeping file the writer keeps in the room. The test identifies that exact list from a control run of the same writer into an empty folder; it does not accept any other file. In particular the record, `SOUL.md`, `AGENT.md` and any other home content must be absent (AD-6). Final clean-up: the writer's work and index handles are test-owned, so the test stops its work and closes those handles itself when it simulates the gateway stop (a real gateway stop is not claimed to close a test-owned store); then Manual recovery step 2 deletes the folder, and the test asserts the whole `agents/X/` is absent. No fake or file-only writer is used, and no production memory-writer change is needed.

**Incomplete rollback.** If a compensation step fails, the response is 500 `import_rollback_incomplete` with `details.failed_step`, `details.leftovers`, `details.agent_id`, `details.staging_id` and (when C4 was not confirmed) `details.activation`. The CLI prints "Import failed and could not be fully undone. Agent <id>, staging <staging_id>. Other screens may also have used this agent. See 'Recover from a failed import' in the CLI guide." The staging moves to `needs-cleanup`; it is kept, never expires, and holds an upload slot until it is retired (below).

**Gateway stop during apply (S1 limit, L4).** No automatic recovery; start-up recovery belongs to S6. The journal stays in the staging folder. On every upload, the gateway checks the staging folders under its registry lock. A folder whose journal is unfinished produces the preview warning `unfinished_import_found`, with the agent ID and the staging folder name (S1a). Such a staging is never cleaned up automatically.

### Manual recovery (S1a; documented in DOC-001)

Where to find the agent ID: the `import_rollback_incomplete` answer, the `unfinished_import_found` warning on the next upload, the audit log, or `new_agent_id` in `$OMNIPUS_HOME/backups/.staging/<staging_id>/journal.json`.

1. **Record present** (shapes a and a2; the agent shows in the agent list; if not, restart the gateway, which reloads the list from the store): delete it with **Delete agent** on the Team tab. From a script: `GET /api/v1/agents/<id>` to read its `revision`, then `DELETE /api/v1/agents/<id>?revision=<revision>`. The normal delete also removes team entries and delegation edges naming X in every workspace, and the sessions it owns (its usual cascade). It keeps unrelated home files, so continue with step 2 if `agents/<id>/` is still there.
2. **Only the folder is left** (shape b; shape d, a folder recreated after a complete rollback; or the folder left after step 1): with the gateway **stopped**, delete the folder `$OMNIPUS_HOME/agents/<id>/`, then start the gateway. Stopping the gateway ends every running turn, so no writer for X can recreate the folder afterwards; X is not loaded again because its record is gone. Because C2 only runs after C1 succeeded, no membership is left to clean in shape b.
3. **Retire the journal and free the slot**: delete the staging folder `$OMNIPUS_HOME/backups/.staging/<staging_id>/`. On the next upload, the registry drops every `needs-cleanup` or orphan entry whose folder is gone, so the slot is free without a restart.
4. **Sessions outside the rollback boundary** (AD-5; only after a rollback that removed the record, so step 1 cannot run). Run it after step 2's gateway stop and start (or, if step 2 was not needed, once C4 is confirmed and no work for X is still running). **Precondition: a stable session list for the whole enumeration.** The list is paged by numeric offset over the current, recency-ordered set, so any session created, deleted or updated by anyone (other screens, scripts, background work), not only by X, can shift rows between pages and hide one of X's sessions while every answer still succeeds. So first pause other session activity on the install (close other screens and scripts; do not chat). Then enumerate **every** session for X with the existing API and **collect the IDs before deleting anything**: request `GET /api/v1/sessions?flat=true&agent_id=<id>&offset=0`; keep the listed IDs; if the answer has `next_cursor`, request again with `offset=<next_cursor>`; continue until an answer has no `next_cursor`. `flat=true` is needed so child sessions are listed too. Do **not** stop because a page lists nothing for X: the agent filter is applied after paging. If any request fails, or an answer reports degradation, stop and report that the clean-up is not complete. Then delete each collected session with `DELETE /api/v1/sessions/<session_id>` (or its Delete in the app); a 404 for a child already removed with its parent counts as deleted. Then run a **fresh verification scan** the same way from `offset=0`. If it finds a session for X, repeat the pass. The clean-up is **complete only when a verification scan under the stable-list condition finds none**; if other activity could not be paused, report it as **incomplete/unverified**, however many answers succeeded. A **main chat** for X is protected: it is hidden from the list once X is no longer a member, the sessions API refuses to delete it (409), and normal retention does **not** remove it: retention only expires older rolled content, so its identity and newest messages can remain **indefinitely** in S1. It belongs to an agent that no longer exists and cannot be chatted with. This is the remaining residue S1 accepts (L16).
5. **Workspace references outside the import workspace** (AD-5; only when the record is gone, because `DELETE /api/v1/agents/<id>` answers 404 for an agent that is no longer loaded, so step 1 cannot clean them). Another request may have added X to a second workspace, or drawn an edge to or from X there, while X was visible. With the existing workspace API: list every workspace with `GET /api/v1/workspaces?status=all`; for each one, read it and its current graph and revision; where X is in `core_team` or in an edge, save it with `PUT /api/v1/workspaces/<id>` carrying that `revision`, the `core_team` without X and the `delegation` without the edges involving X, everything else unchanged. Unrelated members and edges stay. An error, a partial save or a revision conflict means the clean-up is not complete: read the workspace again and retry. Never delete or replace the workspace, and never recreate X's record. No new endpoint is used.

**Final clean-up** (when the answer says other screens may have used X, or `agents/<id>/` is there after a rollback): step 1 if the record is present, then step 2 (gateway stopped), then step 5 (if the record was already gone), then step 4 (stable list, verification scan), then step 3. Its end state covers only the named places: no record, no home folder, no team entry or edge for X in any workspace, no ordinary session for X (verified by a scan under the stable-list condition, otherwise reported unverified), the slot free; a protected main chat (if one was prepared) remains. It is **not** an all-data guarantee: task, audit or tool-output effects other requests made for X elsewhere (AD-5) are not cleaned in S1.

Test `TestPackages_ManualRecovery` runs every shape of Dataset D15 (record present after a C1 failure, after a partial C1, after a `DeleteState` failure; folder-only after a partial C2; folder recreated after a complete rollback; retained staging after a restart; ordinary-session residue, including a session beyond the first page and a child session; an unrelated session deleted between page requests; X references in a second workspace after the record is gone). It follows the documented steps through the REST API (not direct store enumeration), then asserts that a new upload is admitted (201) with all 5 slots available, and that only a protected main chat (if any) remains for X.

---

## User Stories & Acceptance Criteria

### User Story 1 — Export one custom agent to a file (Priority: P0)

The owner of install A wants their agent "Writer" on another computer. One command produces one file. The file holds Writer's settings, prompt and instructions, and none of the install's stored credentials, keys, or host-specific paths and runtime values.

**Why this priority**: Without a file there is nothing to import. This is half of the demo.

**Independent Test**: Export on a test install. Open the file with standard tar and zstd tools. Check the entries, the manifest and the cleaned record against "Field mapping", and run the structured-secret scan (FR-013).

**Acceptance Scenarios**:
1. **Given** a custom agent "Writer", **When** the owner exports it, **Then** the file holds the manifest followed by Writer's record, prompt and (if present) instructions, and the command reports success.
2. **Given** Writer's record has a home path, timestamps, skill grants and retired delegation fields, **When** it is exported, **Then** those are absent, and every field marked "kept" in Field mapping is present.
3. **[S1b]** **Given** Writer is an external worker with environment overrides, extra arguments and a program path, **When** it is exported, **Then** none of those values are in the file, and the manifest lists the override names as secrets to re-enter.
4. **Given** a built-in, system or shipped subagent agent, **When** the owner exports it, **Then** export is refused, saying built-in agents cannot be exported yet, and no file is written.
5. **Given** an agent that does not exist, a name two agents share, or a native agent with a blank prompt, **When** the owner exports, **Then** export is refused with a message naming the problem, and no file is written.
6. **Given** the output file exists, **When** the owner exports without `--force`, **Then** export is refused and the existing file is unchanged. With `--force`, the old file is replaced only when the new one is complete.
7. **Given** an agent's prompt or instructions file is a link, unreadable, or larger than 1 MiB, **When** the owner exports, **Then** export is refused naming the file, and no package is produced that the import would refuse anyway.

### User Story 2 — Look inside a package without installing it (Priority: P1)

The owner wants to see what a file contains, and whether it was damaged or changed, without a running Omnipus.

**Why this priority**: Part of the demo and the basis of trust; P1 because the import checks everything again.

**Independent Test**: With no gateway and an empty data folder, run `inspect` and `verify` on good, changed and broken files.

**Acceptance Scenarios**:
1. **Given** a package, **When** the owner runs `inspect`, **Then** they see the format version, source build, creation time, elements, files with sizes and checksums, and secrets to re-enter, plus the line "Checksums not verified". Nothing is written, and only the manifest is read.
2. **Given** an unchanged package, **When** the owner runs `verify`, **Then** every file is confirmed and the exit code is 0.
3. **Given** a package with one file changed, **When** the owner runs `verify`, **Then** it fails naming that file.
4. **Given** a package made by a newer Omnipus, **When** the owner runs `inspect` or `verify`, **Then** it says to update first.

### User Story 3 — Import the agent as a new agent (Priority: P0)

On install B the owner imports `writer.omnpkg`, sees what will be added, confirms, and gets Writer with a new ID, in the default workspace (or a named one). A Main agent is ready to chat; a native worker is a team member that a Main can delegate to once the owner draws that line. If the import fails while the gateway is running, the import's own writes are removed and the agent list matches the disk again (Rollback contract, with its limits).

**Why this priority**: The other half of the demo, and where data could be damaged.

**Independent Test**: Two data folders. Export from one and import into the other. Compare against "Field mapping". Inject a fault after each write E4–E9 and apply the rollback oracle.

**Acceptance Scenarios**:
1. **Given** a valid package, **When** the owner uploads it, **Then** they see a provisional preview (agent to add, proposed name, references that will be dropped, secrets to re-enter), and nothing on B has changed.
2. **Given** the preview, **When** the owner confirms in New mode, **Then** Writer exists on B with a new ID, a record matching "Field mapping", the default workspace's team membership and its own delegation row. **When the live-publish reload is confirmed**, it is in the agent list at once; when it is not (error or timeout), the result says `activation: pending` and tells the owner to restart the gateway or wait and check the list (the agent is stored and is not reported as absent).
3. **Given** B already has a "Writer" (any letter case), **When** the owner imports, **Then** the new agent is "Writer (imported)" and the existing Writer is untouched.
4. **Given** the owner names a workspace, **When** they confirm, **Then** the agent joins that workspace.
5. **Given** a write fails after the import has started writing, **When** the gateway is still running, **Then** the rollback removes the new agent's record, its home folder with everything inside it (whoever wrote it), its team entry and the delegation lines to or from it in the import workspace, a reload is requested, and the owner is told nothing was imported, whether the reload was confirmed, and that chats or sessions other screens may have created for the agent stay and that work already running may write into the home folder again (Rollback contract, L16, final clean-up).
6. **Given** the package refers to skills, connectors or tools B does not have, **When** the owner imports, **Then** those are left out, the agent is still imported, and each left-out item is listed.
7. **Given** a damaged, changed, too large, unsafe, newer or non-creatable package, **When** the owner uploads it, **Then** it is refused with a reason, and nothing is kept or changed.
8. **Given** the owner asks for Replace or Merge, **When** they confirm, **Then** the import is refused: only New mode is available.
9. **Given** something changed between preview and apply (a new agent took the name, a skill was removed), **When** the owner applies, **Then** the import goes ahead with the current state and the result lists each difference from the preview.
10. **Given** the owner has the workspace's Team tab open, **When** a CLI import into that workspace completes, **Then**, when the live-publish reload is confirmed, the Team list and graph show the new member without a page reload (the refresh is triggered by `agent_created`, which is only sent after confirmation; with `activation: pending` there is no refresh and the owner follows the pending guidance).
11. **Given** a package with a native worker (Subagent), **When** the owner imports it, **Then** the worker is in the workspace team with only its own delegation row, it gets **no** main chat, no other agent gains a delegation line to it, and the result says "This worker is reached by delegation: draw a line to it from a Main agent in the Team graph." **And when** the owner then adds that line with the existing Team graph edit, **Then** that Main's delegation directory lists the worker.
12. **Given** a Main agent is imported into a workspace where another member Y has no main chat, **When** the import completes, **Then** the imported agent has exactly one main chat in that workspace, and Y's chat state is unchanged. A failure to create the main chat is a warning (`main_chat_pending`); the chat is created when the owner opens it.
13. **[S1a only]** **Given** a valid, checksummed package whose agent is an external worker (with or without environment overrides, extra arguments or a program path), **When** it is uploaded to S1a, **Then** it is refused before the preview with 422 `external_worker_not_supported_yet`, and nothing is kept.
14. **Given** an import left something behind (incomplete rollback or a gateway stop), **When** the owner follows the manual recovery in the docs, **Then** the agent and its folder are gone, the staging is retired, and a new upload is admitted.

### User Story 4 — Same steps through the REST API (Priority: P1)

Scripts, and later the Settings screen (S8), use the same three endpoints as the CLI (ADR D1).

**Why this priority**: The CLI depends on these endpoints, and S8 builds on them.

**Independent Test**: Call the endpoints directly through the **registered routes** with admin, CLI and missing tokens, in local and platform mode, and with developer bypass mode on.

**Acceptance Scenarios**:
1. **Given** an authenticated admin in local mode, **When** they call export, upload and apply, **Then** they get the bytes, a preview, and a result with the new ID.
2. **Given** no valid authentication, **When** any endpoint is called, **Then** it is refused as unauthenticated.
3. **Given** platform mode, **When** any endpoint is called, **Then** it is not found.
4. **Given** an apply is running, **When** a second apply starts (any staging), **Then** the second is refused as busy.
5. **Given** a staging that expired, was used, or never existed, **When** apply is called, **Then** it is refused as not found.
6. **Given** a valid package larger than 1 MiB, **When** it is uploaded through the real route, **Then** it is accepted (the general 1 MiB request limit does not apply to this route). **And given** a body of exactly 64 MiB, **Then** the route reads all of it into the staging folder before judging its content (it is not refused for size); a body of 64 MiB + 1 byte, also when sent without a declared length, is refused as too large.

---

## UI Screens and States

No new screen in S1 (Settings → Data is S8). There is **one small change to an existing screen** (T-7): when `agent_created` arrives, the app also refreshes every cached workspace detail and delegation graph, not only the workspace list, so an open Team tab shows the new member. The backend sends `agent_created` only after the commit point and the live-publish reload (FR-036), so the refreshed views already see the membership.

| Command / view | In progress | Error | Partial | Success |
|---|---|---|---|---|
| `omnipus export` | "Exporting Writer…" | `error: <reason>`, exit 1/2; an existing output file is untouched | n/a (atomic) | `Exported "Writer" (1 agent, 3 files, 4.2 KB) to writer.omnpkg` + the notice "SOUL.md and AGENT.md are copied as they are; check them for pasted secrets before sharing" |
| `omnipus package inspect` | — | `error:` line, exit 1 | — | Elements and files; "Checksums not verified; run `omnipus package verify`" |
| `omnipus package verify` | — | `FAILED: <file>: <reason>`, exit 1 | — | `OK: 3 files verified` |
| `omnipus import` | "Uploading…", provisional preview (including `unfinished_import_found` with agent ID and staging folder), `Import 1 agent as new? [y/N]` | `error:` line; for a rollback "Import failed; nothing was imported: <reason>. If another screen used this agent meanwhile, its chats are not removed and anything saved in the agent's folder was deleted; see 'Recover from a failed import' for the final clean-up."; for an incomplete rollback the line below; in both cases, when the reload was not confirmed, the neutral C4 line "The gateway did not confirm that it reloaded its agent list. Restart the gateway, then check the agent list."; for an incomplete rollback "Import failed and could not be fully undone. Agent <id>, staging <staging_id>. Other screens may also have used this agent. See 'Recover from a failed import' in the CLI guide." | Warnings (dropped items, changed since preview, main chat pending, program not set) with exit 0. **Activation pending** (reload error or timeout): exit 0 with "Imported "<name>" as <id>, but the gateway did not confirm that it is live yet. Restart the gateway, or wait and check the agent list, before chatting with it." | `Imported "Writer (imported)" as <new-id> into workspace "<name>"`; for a worker, plus the "reached by delegation" line |
| Lost connection during apply | — | "The import may or may not have completed (staging <staging_id>). Check the agent list for "<name>" before importing again; if it is there and incomplete, follow 'Recover from a failed import'." | — | — |
| Team tab (existing) | unchanged | unchanged | — | New member appears without reload |

## User Journey

1. On A: `omnipus export --agent Writer -o writer.omnpkg` (US-1). A stopped gateway is started the same way as for `omnipus <agent> <prompt>`.
2. Copy the file.
3. On B: optionally `omnipus package inspect` / `verify` (US-2).
4. `omnipus import writer.omnpkg`: upload, provisional preview, confirm (`--yes` skips the prompt; `--workspace` picks the workspace).
5. Apply; the result shows the final name and new ID (US-3).
6. In the app, Writer is in the Team tab (live, even if it was already open). If Writer is a Main agent, its main chat answers using the same prompt. If it is a worker, the owner draws a delegation line to it from a Main agent in the Team graph, and that Main can then hand it work.

## Accessibility and Keyboard

The CLI output is plain text: errors go to stderr, meaning is never shown by colour alone, and there is no emoji. The prompt accepts `y`/`yes` in any letter case; anything else, including Enter alone, cancels. The T-7 change adds no control or focus change.

## Design-System Components

None. T-7 changes cache refresh only.

## Security and User Promises

- **Structured secrets never leave (locked rule, ADR D6).** Export reads only the selected agent's record, `SOUL.md` and `AGENT.md`, plus the build version. It never opens `credentials.json`, `master.key`, `cli.token`, `auth.json` or `config.json`. It removes the runtime fields listed in Field mapping. **It does not promise that free text is secret-free**: `SOUL.md` and `AGENT.md` are copied as they are, and a key someone pasted into them travels with them. The CLI shows a notice on every export, and the docs say so (L6).
- **Safe reading.** Export reads source files with bounded, link-refusing, regular-file reads (FR-018). Upload applies universal entry checks plus the agent layout checks, with bounded decoding (FR-007, FR-008).
- **No implicit new permissions.** Connector assignments are always dropped. Tool policies can only end up equal to or tighter than what create allows.
- **Who may do it.** Local mode only, the same scope as Backup & Restore. The middleware is stricter than Backup's: authenticated, refused while developer bypass mode is on (`RequireNotBypass`), with route-specific body limits.
- **Password re-entry.** Not in New mode (DA-1).
- **Audit.** One audit entry per export, per safety refusal at upload, and per apply outcome (FR-016, FR-040). When the audit write itself fails: an export is refused (500 `audit_failed`, no bytes sent), because an unrecorded copy of an agent leaving the install is the case audit exists for. An upload refusal or a rollback keeps its true answer (the refusal or `import_rolled_back`) and the audit failure goes to the gateway log. After the commit point it is a warning (`audit_failed`). The answer never says "nothing imported" once the import is committed.

---

## Behavioral Contract

Primary flows:
- When the owner exports a custom agent, the system writes one package file whose contents follow Field mapping.
- When the owner inspects a package, the system shows its manifest without a gateway and without writing anything.
- When the owner verifies a package, the system checks every file.
- When the owner uploads a package, the system checks it fully, shows a provisional preview, and changes no agent, workspace, skill, connector or configuration (only its staging folder, staging maintenance and the audit log).
- When the owner confirms New mode, the system creates the agent through the create rules, adds it to one workspace team, then reloads the live list, announces it and (Main only) creates its own main chat.

Error flows:
- When an upload check fails, the system refuses with a named reason and keeps no staging for it.
- When an apply fails before the commit point, the system removes the new agent's record, its home folder with everything inside it, its team entry and every delegation edge involving it in the import workspace, requests a reload, and reports that nothing was imported, whether the reload was confirmed, and that chats and sessions other requests created for the agent stay and that work already running may write into the home folder again (final clean-up).
- When undoing fails, the system reports what is left, with the agent ID, and keeps the staging until the owner retires it.
- When the gateway stops during an apply, the system does not undo anything automatically; the next upload warns about it with the agent ID.

Boundary conditions:
- When the name is taken at apply time, the system adds " (imported)", " (imported 2)" … " (imported 99)", keeping the name within 100 characters.
- When state changed after the preview, the system applies to the current state and lists the differences.
- When the package is from a newer Omnipus, the system refuses it.

## Edge Cases

- Package without `AGENT.md`: imports without one.
- A prompt with spaces or newlines at either end: stored trimmed (create rule); the inner text is unchanged.
- A native agent with a blank prompt: export refused (`agent_prompt_missing`); a hand-made package with one is refused at upload.
- A 100-character name that clashes: the base is shortened so the result is exactly 100 characters.
- Non-Latin names: kept; clash matching ignores letter case and surrounding spaces only.
- The name "Omnipus": treated as a clash, becomes "Omnipus (imported)".
- The same file uploaded twice and both applied: the second becomes "Writer (imported)" (`changed_since_preview` listed).
- The default workspace is missing and no workspace is named: 409 `default_workspace_missing`, asking for `--workspace`.
- A `--workspace` name matching two workspaces: refused, listing both IDs.
- The target workspace is deleted between preview and apply: 404 before any write.
- Gateway restart between upload and apply: the staging ID is unknown (404); the folder is removed by the next upload's cleanup unless its journal is unfinished.
- A native worker import: no main chat; reached by delegation once the owner draws the line (US-3 AS-11).
- **[S1a only]** A package whose agent is an external worker: refused at upload (`external_worker_not_supported_yet`), whether or not it carries runtime values.
- **[S1b]** An external worker whose tool has no program set on B: imports, and the result shows the existing "cannot run: no program path is set for …" text.

## Explicit Non-Behaviors & Safeguards

### Qualitative Prohibitions

- The system must not put any stored credential, key, token, `config.json` content, host path, external-worker environment value or extra argument into a package, because of the locked rule and because host paths mean nothing elsewhere.
- The system must not claim free text is secret-free, because it cannot be proven (ADR D6).
- The system must not reuse a package's agent ID, because of the locked rule.
- The system must not bypass the create rules to keep a value the rules would change, because import must not be a way around them. Differences are listed instead.
- The system must not copy the create, join or coverage logic, because copies drift (ADR R1). It calls the shared functions.
- The system must not write `config.json` during an import, because nothing in it changes and a rewrite is a needless side effect.
- The system must not delete entity lock files, because that would break locking.
- The system must not change, rename or remove any pre-existing agent, workspace record (apart from team membership, AD-3), skill (apart from AD-1) or connector in New mode.
- The system must not create or change any other agent's main chat during an import (only the imported Main's own, through `ensureMainSession`).
- The system must not add a delegation line from any other agent to the imported agent; the owner draws it.
- The system must not change REST create's behaviour (including that it does not store `voice` today; L17).
- The system must not add an isolation layer, a new store notifier or a whole-install transaction for S1 (DA-5).
- The system must not assign connectors by matching names, because ADR-090 ("Built-in agent configuration, skills, and visual file reading") requires explicit assignment.
- The system must not export or import built-in, system or shipped subagent agents in S1.
- The system must not add skills, connectors, memory, workspaces, sessions, Replace, Merge or start-up recovery in S1.
- The system must not add an agent tool for export or import, because moving data off an install is the owner's action. The precedent is FR-100/FR-103 of the Vault records specification (`docs/internal/specs/vault-records-spec-2026-08-25.md`).
- The system must not change Backup & Restore behaviour (it keeps skipping where it skips today).
- The CLI must not touch the data folder for export or import (ADR-024 amendment, DA-3).

### Machine-Verifiable Constraints

**Error codes** (`ErrorResponse`; new unless noted):

| Situation | Endpoint | HTTP | `code` |
|---|---|---|---|
| No or invalid auth | all | 401 | (existing) |
| Developer bypass mode on | all | 503 | (existing) |
| Platform mode | all | 404 | — |
| JSON body over 1 MiB | export, apply | 413 | (existing) |
| `agent_ids` not exactly one | export | 400 | `export_selection_invalid` |
| Agent unknown | export | 404 | `agent_not_found` |
| Built-in / system / shipped subagent | export | 422 | `core_agent_not_exportable` |
| Native agent with a blank prompt; worker without a description | export | 422 | `agent_prompt_missing` / `element_not_creatable` |
| Source file is a link, not a regular file, unreadable, or over 1 MiB | export | 422 | `agent_file_unreadable` (`details.file`, `details.check`) |
| **[S1a only]** External worker | export | 422 | `external_worker_not_supported_yet` |
| Audit write fails | export | 500 | `audit_failed` (no bytes sent) |
| Upload body over 64 MiB | upload | 413 | `package_too_large` |
| **[S1a only]** Agent record is an external worker (`IsExternalCLIWorker`), with or without runtime values | upload | 422 | `external_worker_not_supported_yet` |
| Not zstd, not tar, manifest not first, `format` wrong, `format_version` missing or 0 | upload | 422 | `not_a_package` |
| `format_version` too high | upload | 422 | `package_too_new` |
| Agent `schema` too high | upload | 422 | `element_schema_too_new` |
| Decoder window over 8 MiB | upload | 422 | `package_too_large` (`details.limit: window`) |
| Decoded stream over 256 MiB, entries over 10,000, manifest over 1 MiB, agent file over 1 MiB | upload | 413 | `package_too_large` (`details.limit`) |
| Checksum or size mismatch | upload | 422 | `checksum_mismatch` |
| Unlisted entry, missing listed entry, file owned by two elements, `counts` wrong, element name ≠ record name, source ID ≠ folder ≠ record `id`, duplicate element ID | upload | 422 | `manifest_mismatch` |
| Unsafe path, or an agent-owned path outside the agent layout | upload | 422 | `unsafe_path` |
| Link, device, FIFO, duplicate path | upload | 422 | `unsafe_entry_type` |
| No supported element | upload | 422 | `no_supported_element` |
| More than one agent element | upload | 422 | `too_many_agents` |
| **[S1a only]** Unknown element type | upload | 422 | `element_type_not_supported` |
| Record invalid (bad JSON, type `core`/`system`, `SOUL.md` on an external worker) | upload | 422 | `invalid_element` |
| Record breaks a create rule (blank prompt, description, fallback pairing, `max_tool_iterations`, `context_window_override`, voice on a worker, non-canonical figure/role/colour, name over 100 characters) | upload or apply | 422 | `element_not_creatable` (`field`, `details.reason`) |
| Upload slots full (5) | upload | 409 | `too_many_staged_imports` |
| Mode `replace`/`merge` | apply | 422 | `import_mode_not_available` |
| Mode missing or invalid | apply | 400 | (existing validation, `field: mode`) |
| Staging unknown, expired, consumed | apply | 404 | `staging_not_found` |
| Another apply running | apply | 409 | `import_in_progress` |
| Workspace ID unknown | apply | 404 | `workspace_not_found` |
| No workspace named and no default workspace | apply | 409 | `default_workspace_missing` |
| Suffixes exhausted | apply | 409 | `name_suffix_exhausted` |
| Failure before the commit point, rollback complete (everything inside the rollback boundary removed) | apply | 500 | `import_rolled_back` (`details.activation: "pending"` when the C4 reload errored or timed out; `details.note`: the agent's record, home folder (with anything saved in it), team entry and delegation lines are removed; chats and sessions other screens created stay; work already running can write into the home folder again, which the final clean-up removes) |
| Rollback incomplete | apply | 500 | `import_rollback_incomplete` (`details.failed_step`, `details.leftovers`, `details.agent_id`, `details.staging_id`, `details.activation` when C4 was not confirmed, `details.note` as above) |

An audit failure never changes an upload refusal or a rollback answer (gateway log only); after the commit point it is the warning `audit_failed`.

`element_not_creatable` is 422, not the 400 the REST create path returns, on purpose: the problem is in the package, not in the shape of the request.

**CLI exit codes**: 0 success (warnings included); 1 refused or failed (any code above, verify failure, cancelled, gateway unreachable, unknown or ambiguous agent or workspace, output file exists); 2 usage error (unknown flag, missing argument or `-o`, input file missing, unreadable or a folder).

**Performance targets** (targets, not CI gates; measured by `BenchmarkPackageS1` on a 4-core runner with one warm-up run, the median of 5 runs, fixture: one Main agent with a 100 KB `SOUL.md`): export ≤ 2 s; upload with preview ≤ 2 s; apply ≤ 5 s, excluding the live-publish wait; `inspect` ≤ 200 ms on a 64 MiB package. A miss is reported to the architect, not treated as a test failure.

## Staging lifecycle — resolves S-03, R2-S-01

States: `receiving` → `ready` → `applying` → (`done` | `needs-cleanup`). One in-memory registry guarded by one lock. The import lock serialises applies.

| Rule | Detail |
|---|---|
| Admission | Upload reserves a slot (counting `receiving` + `ready` + `applying` + `needs-cleanup` + kept orphans; max 5) **before** decoding, atomically. A failed upload releases its slot and deletes its folder. |
| Expiry | The 30-minute clock starts when the staging becomes `ready`. At 30:00 exactly or later, a `ready` staging is expired. `applying` and `needs-cleanup` never expire. |
| Cleanup | Each upload, under the registry lock, first drops every `needs-cleanup` entry whose folder no longer exists (the owner retired it, Manual recovery step 3), then deletes expired `ready` stagings and orphan folders (left by a restart) whose journal is absent or finished. Orphans with an unfinished journal are kept, take a slot, and produce the `unfinished_import_found` warning with the agent ID and staging folder name (S1a). |
| Retirement | The only way to retire a `needs-cleanup` staging or a kept orphan is to delete its folder (Manual recovery). Nothing else releases its slot. |
| Apply | `ready` → `applying` atomically under the registry lock. Any other state gives 404 `staging_not_found`, or 409 `import_in_progress` when an apply is already running anywhere. |
| End | Success or complete rollback → `done`: folder deleted, ID consumed. Incomplete rollback → `needs-cleanup`: folder kept. A refusal that wrote nothing (404 workspace, 409 default missing, 409 suffix, 422 not creatable at apply) → back to `ready`, with the clock unchanged. |

## Preview freshness — resolves S-05

The preview is **provisional**. Apply re-runs the name-clash, reference, workspace and create-rule checks on the current state before its first write. If the outcome differs from the preview and is still additive (another name suffix, a skill now missing or present, a provider now configured), apply goes ahead and lists each difference as `changed_since_preview`. If a check now refuses (workspace gone, create rule now broken), apply refuses before writing.

## Integration Boundaries

### Agent store
- **In/out**: `CreateState` (record + SOUL), `MutateStateWithAgentMD` (AGENT.md), `DeleteState` on rollback.
- **On failure**: before the commit point → rollback; partial persistence (`PersistencePartial`) → rollback of whatever exists.
- **Development**: real store on a temporary home; faults injected through a store interface in `pkg/omnpkg`, plus real-filesystem faults (a read-only folder) for at least E4 and E8.

### Workspace membership (T-4 helpers)
- **In/out**: join (team entry + self-row, one lock); leave (remove X's entry and every edge involving X in the import workspace, fresh re-read, one lock).
- **On failure**: join failure → rollback; leave failure → `import_rollback_incomplete`.
- **Development**: real workspace files. The concurrency test respects the production lock: it fails the self-row, lets the join helper release the workspace lock, lets a competing writer finish its own edit of the same workspace, then runs compensation, and asserts the competing edit survives. It never bypasses the lock.

### Config lock (T-3)
- **In/out**: coverage check under `configMu` against a candidate snapshot; no write.
- **Development**: a test asserts `config.json` is byte-identical and its modification time unchanged after an import.

### Live list, `agent_created`, main chat
- Publication is a full reload (`triggerReloadAndWaitOutcome`, outcomes confirmed / error / timeout) after the commit point. Only **confirmed** leads to `agent_created` and the main chat; error or timeout → `activation: pending`, no event, restart guidance, the agent stays. The rollback runs the same reload as step C4, with the same three outcomes reported.
- Tests use a **wired** reload pipeline: an unwired one reports "confirmed" by design (`ErrReloadNotConfigured`) and cannot prove publication or removal.
- Main chat: `ensureMainSession` for the imported Main and its workspace only, when `mainSessionPairEligible` allows it. Failure → warning `main_chat_pending`; the chat is created when the owner opens it.
- No isolation from other readers during the import (L16).

### CLI ↔ gateway
- `cli.token` bearer; generated client types; auto-start through the shared steps; a lost apply connection gives the "may or may not have completed" message.

### App cache (T-7)
- `agent_created` additionally refreshes `workspacesQueryKeys.detail(*)` and `workspacesQueryKeys.delegation(*)` (all workspaces, because the frame carries no workspace ID and its contract says it is coarse on purpose). No contract change.

---

## Ambiguity Warnings

| # | What's Ambiguous | Likely Agent Assumption | Resolution |
|---|---|---|---|
| 1 | Step-up password for New mode | Add it and break the CLI | Settled by DA-1 |
| 2 | Shape of the join, leave and coverage helpers | Copy the code | The architect fixes the inputs, partial results and lock ownership before GREEN on T-3 and T-4; copying is forbidden |
| 3 | Same-named connectors and skills | Bind connectors by name | Connectors never; skills by installed name, listed |
| 4 | CLI default mode | Replace | The API has no default; S1's CLI sends `new`; S6 changes the CLI default (documented) |
| 5 | Size caps and decoder limits | Library defaults | FR-008 values |
| 6 | ADR-024 scope | Ignore it | DA-3 amendment |
| 7 | How to publish without the config write | Call `fastAgentUpsert` | Full reload after commit and after rollback (Rollback contract); `agent_created` and the main chat only when that reload is confirmed, otherwise `activation: "pending"` |
| 8 | Main chat for the import | Call `ensureWorkspaceMains` | `ensureMainSession` for the imported Main only |
| 9 | `voice` at import | Follow REST create (drop it) | Follow `create_agent` (keep it for Main); REST create unchanged |
| 10 | What a rollback removes | Everything about X, or only what the import itself wrote | Defined by places and records: the record, the home folder with everything inside it (whoever wrote it), the team entry and every edge involving X in the import workspace. Chats, sessions and other records outside that boundary stay (AD-5); a writer already running can recreate home content after C3 (AD-6), removed by the final clean-up |

---

## BDD Scenarios

### Feature: Export

#### Scenario: Export a Main agent with prompt and instructions
**Traces to**: User Story 1, Acceptance Scenario 1 — **Category**: Happy Path
- **Given** a Main agent "Writer" with `SOUL.md` and `AGENT.md`
- **When** the owner exports it
- **Then** the first entry is `manifest.json`, and the others are exactly `agent.json`, `SOUL.md` and `AGENT.md` under `omnipus/agents/<id>/`
- **And** `SOUL.md`/`AGENT.md` in the package are byte-identical to the source
- **And** the manifest has `format` "omnpkg", `format_version` 1, `scope` "custom", one `agent` element with `schema` 1, `counts.agent` 1, and a SHA-256 and size per file
- **And** exactly one audit entry records the actor, the agent ID and the package SHA-256

#### Scenario: Export is refused when it cannot be audited
**Traces to**: User Story 1, Acceptance Scenario 1 — **Category**: Error Path
- **Given** the audit write fails (test hook)
- **When** the owner exports Writer
- **Then** 500 `audit_failed`, no package bytes are sent, and the CLI leaves no output file

#### Scenario: Export an agent with no AGENT.md
**Traces to**: User Story 1, Acceptance Scenario 1 — **Category**: Alternate Path
- **Given** Writer has no `AGENT.md`
- **When** the owner exports it
- **Then** the package has two agent files and no `AGENT.md`

#### Scenario Outline: The exported record follows Field mapping
**Traces to**: User Story 1, Acceptance Scenario 2 — **Category**: Happy Path
- **Given** Writer's stored `<field>` is a non-default `<value>`
- **When** the owner exports Writer
- **Then** the packaged record shows the "Export" column's outcome for `<field>`

Examples: Dataset D3.

#### Scenario: [S1b] External worker runtime values are stripped
**Traces to**: User Story 1, Acceptance Scenario 3 — **Category**: Edge Case
- **Given** an external worker "Coder" (`cli` codex, `cli_args` "--key sk-TEST-123", `env_overrides` {"OPENAI_API_KEY": "sk-TEST-456"})
- **When** the owner exports Coder
- **Then** the record keeps `executor.kind` and `.cli`, and has no `cli_path`, `cli_args` or `env_overrides`
- **And** `needs_secrets` = [{name "OPENAI_API_KEY", used_by Coder's ID, kind "executor_env"}]
- **And** neither "sk-TEST-123" nor "sk-TEST-456" appears in the package bytes, compressed or decompressed

#### Scenario: Structured secrets from the data folder do not appear
**Traces to**: User Story 1, Acceptance Scenario 2 — **Category**: Error Path
- **Given** known test values in `credentials.json`, `master.key`, `cli.token`, and a plain-text MCP header in `config.json`, **none of which** appear in Writer's `SOUL.md` or `AGENT.md`
- **When** the owner exports Writer
- **Then** none of those values appear in the package, compressed or decompressed

#### Scenario: A secret pasted into the prompt travels (documented limit)
**Traces to**: User Story 1, Acceptance Scenario 1 — **Category**: Edge Case
- **Given** Writer's `SOUL.md` contains the text "sk-PASTED-789"
- **When** the owner exports Writer
- **Then** the package's `SOUL.md` contains "sk-PASTED-789", and the CLI printed the "copied as they are" notice

#### Scenario Outline: Built-in agents are refused
**Traces to**: User Story 1, Acceptance Scenario 4 — **Category**: Error Path
- **Given** the shipped agents
- **When** the owner exports `<id>`
- **Then** 422 `core_agent_not_exportable`, and no file is written

Examples: Dataset D4.

#### Scenario Outline: Export refusals leave no file
**Traces to**: User Story 1, Acceptance Scenario 5 and 6 — **Category**: Error Path
- **Given** `<situation>`
- **When** the owner runs `<command>`
- **Then** the exit code is `<exit>`, the message names the problem, and the output path is unchanged (absent, or its old bytes)

Examples: Dataset D10 rows 1–7, 11–13.

#### Scenario: Overwrite with --force is atomic
**Traces to**: User Story 1, Acceptance Scenario 6 — **Category**: Alternate Path
- **Given** `writer.omnpkg` exists
- **When** the owner exports with `--force` and the download is cut mid-way
- **Then** `writer.omnpkg` keeps its old bytes, and no temporary file is left

#### Scenario Outline: Unsafe or oversized source files are refused at export
**Traces to**: User Story 1, Acceptance Scenario 7 — **Category**: Error Path
- **Given** Writer's `<file>` is `<state>`
- **When** the owner exports Writer
- **Then** 422 `agent_file_unreadable` naming `<file>`, and no file is produced

Examples: Dataset D11.

#### Scenario: Export via REST with wrong selection size
**Traces to**: User Story 4, Acceptance Scenario 1 — **Category**: Error Path
- **Given** an admin
- **When** export is called with `agent_ids` `[]` or two IDs
- **Then** 400 `export_selection_invalid`

### Feature: Inspect and verify offline

#### Scenario: Inspect reads only the manifest
**Traces to**: User Story 2, Acceptance Scenario 1 — **Category**: Happy Path
- **Given** no gateway, an empty `OMNIPUS_HOME`, and a package whose payload files are corrupted but whose manifest is intact
- **When** the owner runs `inspect`
- **Then** it shows the manifest details and "Checksums not verified", exits 0, and `OMNIPUS_HOME` stays empty

#### Scenario: Verify an unchanged package
**Traces to**: User Story 2, Acceptance Scenario 2 — **Category**: Happy Path
- **Given** an unchanged package
- **When** the owner runs `verify`
- **Then** `OK: 3 files verified`, exit 0

#### Scenario: Verify a changed package
**Traces to**: User Story 2, Acceptance Scenario 3 — **Category**: Error Path
- **Given** one byte of `SOUL.md` changed, repacked with the old manifest
- **When** the owner runs `verify`
- **Then** it fails naming `omnipus/agents/<id>/SOUL.md`, exit 1

#### Scenario: Newer package refused offline
**Traces to**: User Story 2, Acceptance Scenario 4 — **Category**: Error Path
- **Given** `format_version` 2
- **When** the owner runs `inspect`
- **Then** "made by a newer Omnipus … update", exit 1

#### Scenario: Oversized decoder window refused offline
**Traces to**: User Story 2, Acceptance Scenario 1 — **Category**: Error Path
- **Given** a small file whose zstd frame asks for a 512 MiB window
- **When** the owner runs `inspect`
- **Then** it is refused as too large without allocating the window (exit 1)

### Feature: Upload and preview

#### Scenario: Upload gives a provisional preview and changes nothing
**Traces to**: User Story 3, Acceptance Scenario 1 — **Category**: Happy Path
- **Given** B running, with a recorded snapshot of its data folder
- **When** the owner uploads `writer.omnpkg`
- **Then** 201 with `staging_id`, `expires_at` = ready time + 30 min, and `will_add` = [Writer, `proposed_name` "Writer", `renamed` false]
- **And** compared with the snapshot, the only differences are the new staging folder, any staging maintenance (expired or retired stagings removed) and the audit log; no agent, workspace, skill, connector or configuration file changed

#### Scenario Outline: Damaged, unsafe or non-creatable packages are refused
**Traces to**: User Story 3, Acceptance Scenario 7 — **Category**: Error Path
- **Given** a package built as `<package>`
- **When** it is uploaded through the registered route
- **Then** `<status>` `<code>`, the slot is released, and no staging folder remains for it
- **And** apart from staging maintenance and the audit log, the data folder is unchanged
- **And** for safety refusals, exactly one audit entry is written; if that audit write fails, the answer is still `<status>` `<code>`

Examples: Datasets D1, D2, D12.

#### Scenario Outline: Upload size through the real route
**Traces to**: User Story 4, Acceptance Scenario 6 — **Category**: Edge Case
- **Given** `<body>`
- **When** it is posted to `/api/v1/packages/import` with an admin token
- **Then** `<status>`

Examples (Dataset D1 rows 11, 12, 25, 32):
- a **valid** package of about 2 MiB (one Main agent with a 1 MiB `SOUL.md` and a 1 MiB `AGENT.md` of random text, so it does not compress) → 201 (positive control for the content limits);
- a body of **exactly 64 MiB** of random bytes, sent through a **counting request body** → 422 `not_a_package`, **and** the counter shows all 67,108,864 bytes were read. The upload handler first copies the whole body into the staging folder (bounded by the route limit) and only then decodes, so a 422 here proves the body passed the size limit; a 422 alone would not prove it;
- 64 MiB + 1 byte with a declared `Content-Length` → 413 `package_too_large`;
- 64 MiB + 1 byte sent **chunked, with no declared length** → 413 `package_too_large` (the limit is enforced while reading).

The transport limit and content validity are tested separately; no test-only layout or cap is used.

#### Scenario Outline: [S1a only] External-worker package refused at upload
**Traces to**: User Story 3, Acceptance Scenario 13 — **Category**: Error Path
- **Given** a valid, checksummed package whose agent record is an external worker `<variant>`
- **When** it is uploaded to S1a
- **Then** 422 `external_worker_not_supported_yet`, before any preview, and no staging remains

Examples: with `env_overrides`, `cli_args` and `cli_path`; with none of them. (When S1b lands, these become acceptance cases for T-9.)

#### Scenario: [S1b] Unknown element types are skipped
**Traces to**: User Story 3, Acceptance Scenario 6 — **Category**: Edge Case
- **Given** a version-1 package with one agent and one `skill` element (`skills/writing/SKILL.md`, listed and checksummed)
- **When** it is uploaded and applied
- **Then** `skipped_elements` lists the skill, only the agent is created, and no skill file leaves staging

#### Scenario: [S1b] A damaged file in an unknown element is still refused
**Traces to**: User Story 3, Acceptance Scenario 7 — **Category**: Error Path
- **Given** the package above with `SKILL.md` changed after export
- **When** it is uploaded
- **Then** 422 `checksum_mismatch` naming `skills/writing/SKILL.md`

### Feature: Apply in New mode

#### Scenario: Confirmed import creates the agent
**Traces to**: User Story 3, Acceptance Scenario 2 — **Category**: Happy Path
- **Given** a `ready` staging
- **When** the owner applies with mode `new`
- **Then** one created agent whose `new_id` is a UUID different from the source ID and from every ID present before
- **And** its stored record equals the expected record of Dataset D3 (export column, then import column)
- **And** `SOUL.md` equals the packaged one with leading and trailing whitespace removed; `AGENT.md` is byte-identical
- **And** it is in the default workspace's `core_team` with its own self-delegation row
- **And** (wired reload, confirmed) `GET /api/v1/agents` lists it after the live-publish reload, and an `agent_created` frame was sent only after that reload was confirmed
- **And** (Main) it has exactly one main chat in that workspace
- **And** exactly one audit entry records the operation "import", the outcome "success", the actor, the new ID and the package SHA-256
- **And** the staging folder is gone

#### Scenario: The main chat is created for the imported Main only
**Traces to**: User Story 3, Acceptance Scenario 12 — **Category**: Edge Case
- **Given** workspace W where an existing Main member Y has no main chat, and a second member V whose main chat record is damaged
- **When** a Main agent is imported into W
- **Then** the imported agent has exactly one main chat in W
- **And** Y still has no main chat, V's chat state is byte-identical, and the result carries no warning caused by Y or V

#### Scenario: A native worker is imported and reached by delegation
**Traces to**: User Story 3, Acceptance Scenario 11 — **Category**: Happy Path
- **Given** a package with a native worker "Researcher-2" and workspace W with a Main "Lead"
- **When** it is imported into W
- **Then** the worker is in W's team with only its self-delegation row, it has no main chat, no edge from Lead (or any other agent) to it exists, and the result has the "reached by delegation" line
- **And when** the owner adds an edge Lead → worker through the existing workspace delegation edit
- **Then** Lead's delegation directory for W lists the worker

#### Scenario: Two-install round trip
**Traces to**: User Story 3, Acceptance Scenario 2 — **Category**: Happy Path
- **Given** homes A and B, each with a gateway
- **When** Writer is exported on A and imported on B
- **Then** B's record equals D3's expected record, except `id` (new) and the listed warnings
- **And** (hands-on and live-model lanes only, Main agent) a chat message to the new Writer gets a reply

#### Scenario: Import and the create paths give the same result for shared fields
**Traces to**: User Story 3, Acceptance Scenario 2 — **Category**: Happy Path
- **Given** one normalised input with non-default values, including `voice` "alloy", `memory_enabled` false and `context_window_override` 64000
- **When** one Main is created by REST create, one by the `create_agent` tool, and one by import
- **Then** for every field REST create stores, import and REST create store the same value (the three import transformations, `voice`, `memory_enabled`, `context_window_override`, `AGENT.md`, `id`, name suffix and timestamps excluded)
- **And** for `voice`, `memory_enabled` and `context_window_override`, import and `create_agent` store the same value
- **And** REST create still stores an empty `voice` (unchanged behaviour, L17)

#### Scenario: Every editable field has an import decision
**Traces to**: User Story 3, Acceptance Scenario 2 — **Category**: Edge Case
- **Given** the field names of `agentCreateArgNames`, `agentUpdateArgNames`, the three REST create schemas and `AgentUpdateRequest`
- **When** `TestImportFieldCoverage` runs
- **Then** every name has a row in Field mapping; a name without a row fails the test

#### Scenario Outline: Name clash gets a suffix
**Traces to**: User Story 3, Acceptance Scenario 3 — **Category**: Edge Case
- **Given** existing names `<existing>`
- **When** `<incoming>` is imported
- **Then** the new name is `<final>`, and existing agents are unchanged

Examples: Dataset D6.

#### Scenario: Import into a named workspace
**Traces to**: User Story 3, Acceptance Scenario 4 — **Category**: Alternate Path
- **Given** a workspace "Research"
- **When** apply names Research
- **Then** the agent is in Research's team only

#### Scenario Outline: Workspace selection problems
**Traces to**: User Story 3, Acceptance Scenario 4 — **Category**: Error Path
- **Given** `<state>`
- **When** apply runs
- **Then** `<status>` `<code>`, nothing is written, and the staging returns to `ready`

Examples: unknown ID → 404 `workspace_not_found`; no workspace named and no default → 409 `default_workspace_missing`; workspace deleted after preview → 404 `workspace_not_found` (Dataset D14).

#### Scenario Outline: Failure before the commit point is rolled back
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Error Path
- **Given** a fault injected right after write `<step>`
- **When** the owner applies
- **Then** 500 `import_rolled_back` with "nothing was imported"
- **And** the rollback oracle (Rollback contract, checks 1–7, with a wired reload) passes
- **And** exactly one audit entry with the outcome "rolled_back"; if the audit write fails, the answer is still `import_rolled_back`

Examples: Dataset D7 rows 1–4, 10, 11, 12, 21.

#### Scenario: A competing workspace edit survives a rollback
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Edge Case
- **Given** the import has joined workspace W, and the self-row write fails
- **And** the join helper has released W's lock, and another writer then adds agent Z to W and finishes (synchronised with test hooks; the production lock is not bypassed)
- **When** compensation runs
- **Then** W's team contains Z and not the imported agent

#### Scenario: Another reader sees the import while it runs (documented limit)
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Edge Case
- **Given** a Main agent import into W; E4 is done and an unrelated reload is forced and confirmed, so X is listed
- **And** the self-row write fails, the join helper releases W's lock, and a hook holds C1
- **When** a real workspace-detail request for W is sent and its HTTP response has been received, and only then C1 is released (all steps ordered by test hooks; the production lock is never bypassed; no sleeps)
- **Then** `import_rolled_back`, checks 1–7 of the rollback oracle pass, the recorded order shows the read completed before C1, and the only state left for X is the main chat the read prepared (if any), reported as AD-5 residue (L16)

#### Scenario: Sessions and messages created during the import are not rolled back (documented limit)
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Edge Case
- **Given** a Main agent import; E4 is done and an unrelated reload is forced and confirmed, so X is listed; the self-row write fails, the join lock is released and a hook holds C1
- **And** the test creates an ordinary session for X with `POST /api/v1/sessions`, and sends one real chat message to X over the WebSocket and waits until it is saved in the transcript
- **When** C1 is released and the rollback finishes with C4 confirmed
- **Then** `import_rolled_back`, and checks 1–7 pass
- **And** both sessions and the saved message still exist (AD-5 residue), and the answer's note says chats and sessions other screens created stay
- **And** a new chat message to X is now refused (X is no longer a registered agent)
- **And when** the owner follows Manual recovery step 4 (all pages, IDs collected first), **Then** both sessions are deleted with `DELETE /api/v1/sessions/<id>`, and no session for X remains except a protected main chat (if one was created), which stays (it is not removed by retention)

#### Scenario Outline: Content in the agent's home folder follows the folder, not its writer (documented limit)
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Edge Case
- **Given** a Main agent import; E4 is done and an unrelated reload is forced and confirmed, so X is listed; the self-row write fails and the join lock is released
- **And** a hook holds the rollback `<when>`, and the test saves a private memory note for X with the real writer a turn uses (`RememberTool.Execute`, room `private`, over `MemoryStoreAdapter` for X's home; no model call), and asserts the tool reported success and the note file exists under `agents/X/`
- **When** the hook is released and the rollback finishes with C4 confirmed
- **Then** `import_rolled_back`, and checks 1–7 of the rollback oracle pass
- **And** `<home>`
- **And when** the owner follows the final clean-up (stop the gateway, Manual recovery step 2, start), **Then** `agents/X/` does not exist

Examples (Dataset D7 rows 19–20): `<when>` before C3 (C1 held) → `<home>` the note (and its index files) were deleted together with `agents/X/`, which does not exist (check 5); `<when>` after C3 and before C4, writer constructed and kept before C3, home asserted absent before the write → `<home>` `agents/X/` exists again and holds exactly the note with its expected content plus the derived index and bookkeeping artifacts of that write, identified by a control run, and no record, `SOUL.md`, `AGENT.md` or other file (AD-6); the answer's note mentions that running work can write into the home folder again; the test closes its own writer before the final clean-up.

#### Scenario: A failed membership removal stops the rollback before the record is deleted
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Error Path
- **Given** the self-row write fails, and then C1 fails `<how>`
- **When** the rollback runs
- **Then** 500 `import_rollback_incomplete` with `details.failed_step: "C1"`; C2 and C3 did **not** run, so X's record and folder still exist; C4 was attempted
- **And** for the delegation-save case, the test asserts that the save fault was actually reached, that X is already out of W's team, and that the edge A → X another request added is still stored (partial C1)
- **And** following Manual recovery step 1 (Delete agent) removes X, its team entries and every edge naming X, step 2 removes a remaining folder, step 3 frees the slot, and a new upload is admitted

Examples: membership removal fails; membership removed, then the delegation save fails while an edge A → X added by a separate valid graph edit is stored (Dataset D7 rows 13–14).

#### Scenario: Incomplete rollback is reported and kept
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Error Path
- **Given** a fault after E4 and a second fault that makes `DeleteState` fail
- **When** the owner applies
- **Then** 500 `import_rollback_incomplete`, with `details.failed_step`, `details.leftovers`, `details.agent_id` and `details.staging_id`
- **And** the staging is in `needs-cleanup`, kept past 30 minutes, and counted in the 5 slots

#### Scenario Outline: The rollback reload is not confirmed
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Error Path
- **Given** X is in the live list (an unrelated reload loaded it after E4), the self-row write fails, and the C4 reload `<outcome>` (wired reload pipeline)
- **When** the rollback finishes
- **Then** `import_rolled_back` with `details.activation: "pending"`; the store has no X; no `agent_created` was sent
- **And** the CLI prints the "did not confirm that it reloaded … Restart the gateway" neutral line after the rollback line, exit 1; after a C1 failure (D7 row 14 combined with a C4 error) the CLI prints the incomplete-rollback line and the neutral C4 line, never "nothing was imported"

Examples: returns an error; times out (Dataset D7 rows 15–16).

#### Scenario Outline: Manual recovery frees everything
**Traces to**: User Story 3, Acceptance Scenario 14 — **Category**: Alternate Path
- **Given** `<leftover>`
- **When** the owner follows the Manual recovery steps
- **Then** no record or folder for X remains, no workspace lists X and no delegation edge names X (step 5 for workspaces outside W when the record is gone), no ordinary session for X is found by a verification scan under the stable-list condition (or, when the list changed during enumeration, the procedure re-lists and still finds X, or reports the clean-up as unverified instead of complete), only a protected main chat (if one was prepared) remains, and the next upload is admitted (201) with all 5 slots available

Examples: Dataset D15.

#### Scenario Outline: Failures after the commit point are warnings
**Traces to**: User Story 3, Acceptance Scenario 2 — **Category**: Alternate Path
- **Given** `<post-commit fault>`
- **When** the owner applies
- **Then** 200, the agent and membership are kept, and the result carries `<warning>`

Examples: live-publish reload returns an error or times out → `activation: pending`, **no** `agent_created` frame, no main chat attempted, and the CLI prints the "not confirmed that it is live yet … restart" line (Dataset D7 rows 7, 17); main chat fails → warning `main_chat_pending`; audit write fails → warning `audit_failed` (never "nothing imported") (Dataset D7 rows 8–9).

#### Scenario Outline: References the target lacks
**Traces to**: User Story 3, Acceptance Scenario 6 — **Category**: Edge Case
- **Given** the packaged record has `<reference>`
- **When** the owner previews and applies
- **Then** the preview and the result list `<warning>`, and the stored record has `<outcome>`

Examples: Dataset D5.

#### Scenario: Changes between preview and apply are applied and listed
**Traces to**: User Story 3, Acceptance Scenario 9 — **Category**: Edge Case
- **Given** a preview proposing "Writer", and then an agent "Writer" is created on B
- **When** the owner applies
- **Then** the agent is "Writer (imported)", and the result lists `changed_since_preview` for the name

Examples also in Dataset D14 (a skill removed after preview; the same file previewed twice and both applied).

#### Scenario Outline: Replace and Merge are not available
**Traces to**: User Story 3, Acceptance Scenario 8 — **Category**: Error Path
- **Given** a `ready` staging
- **When** apply with `<mode>`
- **Then** 422 `import_mode_not_available`; the staging stays `ready`

Examples: `replace`, `merge`.

#### Scenario: Package declares a core agent
**Traces to**: User Story 3, Acceptance Scenario 7 — **Category**: Error Path
- **Given** a hand-made `agent.json` with `type` "core"
- **When** uploaded
- **Then** 422 `invalid_element`

#### Scenario: [S1b] External worker without a program on the target
**Traces to**: User Story 3, Acceptance Scenario 6 — **Category**: Edge Case
- **Given** B has no program set for Codex in Settings → Third-party agents
- **When** an external Codex worker is imported
- **Then** it is created; the result lists `runtime_settings_removed` ("re-enter environment overrides and additional CLI arguments in Edit → Runtime") and the existing text "This agent cannot run: no program path is set for Codex. A person can set it in Settings → Third-party agents → Codex."

#### Scenario: [S1b] Executor values in a hand-made package are ignored
**Traces to**: User Story 3, Acceptance Scenario 6 — **Category**: Edge Case
- **Given** a hand-made external record with `env_overrides`, `cli_path` and `cli_args`
- **When** imported
- **Then** the stored record has none of them, and `runtime_settings_removed` is listed

#### Scenario Outline: [S1b] External-worker fields the runtime does not support
**Traces to**: User Story 3, Acceptance Scenario 7 — **Category**: Error Path
- **Given** a hand-made external-worker record with `<field>` set to `<value>`
- **When** it is uploaded to S1b
- **Then** 422 `element_not_creatable`, `field: <field>`, exactly as `create_agent` refuses it (the runtime check `agentmutation.ValidateFields` runs before the shared field applier)

Examples: `memory_enabled` false; `context_window_override` 64000 (Dataset D1 rows 33–34).

#### Scenario: [S1a only] External worker export and unknown element refused until S1b
**Traces to**: User Story 1, Acceptance Scenario 5 — **Category**: Error Path
- **Given** S1a without S1b
- **When** an external worker is exported, or a package with an unknown element type is uploaded
- **Then** 422 `external_worker_not_supported_yet` or 422 `element_type_not_supported`

(External-worker **upload** refusal: see "[S1a only] External-worker package refused at upload".)

#### Scenario: Team tab refreshes while open
**Traces to**: User Story 3, Acceptance Scenario 10 — **Category**: Happy Path
- **Given** the app shows workspace W's Team tab (list and graph) in a browser
- **When** a CLI import into W completes
- **Then** (wired reload, publication confirmed) within 5 seconds, the list and the graph show the new member without a reload

### Feature: Access and staging

#### Scenario Outline: Access rules on registered routes
**Traces to**: User Story 4, Acceptance Scenario 2 and 3 — **Category**: Error Path
- **Given** `<condition>`
- **When** any endpoint is called
- **Then** `<status>`

Examples: Dataset D8.

#### Scenario: CLI token works without a password prompt
**Traces to**: User Story 4, Acceptance Scenario 1 — **Category**: Happy Path
- **Given** the `cli.token` bearer
- **When** the CLI exports, uploads and applies
- **Then** all three succeed

#### Scenario: One apply at a time
**Traces to**: User Story 4, Acceptance Scenario 4 — **Category**: Error Path
- **Given** apply A paused after E4 (test hook)
- **When** apply B starts on another staging
- **Then** B gets 409 `import_in_progress`; A completes with 200 once released

#### Scenario Outline: Staging that is not usable
**Traces to**: User Story 4, Acceptance Scenario 5 — **Category**: Error Path
- **Given** a staging that `<state>`
- **When** apply is called
- **Then** `<expected>`

Examples: Dataset D9.

#### Scenario Outline: Staging races
**Traces to**: User Story 4, Acceptance Scenario 5 — **Category**: Edge Case
- **Given** `<race>` (synchronised with test hooks)
- **When** both operations finish
- **Then** `<outcome>`

Examples: Dataset D13.

### Feature: Command line

#### Scenario: Interactive confirm
**Traces to**: User Story 3, Acceptance Scenario 2 — **Category**: Happy Path
- **Given** a terminal
- **When** the owner runs `omnipus import writer.omnpkg` and types `y`
- **Then** the preview is printed before the prompt, the result names the new ID and workspace, exit 0

#### Scenario: Declined or non-interactive without --yes
**Traces to**: User Story 3, Acceptance Scenario 1 — **Category**: Alternate Path
- **Given** a valid file
- **When** the owner types `n` or Enter, or stdin is not a terminal and `--yes` is absent
- **Then** "Import cancelled", exit 1, nothing imported

#### Scenario: Gateway not running
**Traces to**: User Story 4, Acceptance Scenario 1 — **Category**: Alternate Path
- **Given** no gateway, with a non-interactive key mode set up
- **When** `omnipus import writer.omnpkg --yes`
- **Then** the gateway is started by the shared steps and the import completes

#### Scenario: Lost connection during apply
**Traces to**: User Story 3, Acceptance Scenario 5 — **Category**: Error Path
- **Given** the apply request was sent and the connection drops before the answer
- **When** the CLI handles the error
- **Then** it prints the "may or may not have completed … check the agent list" message, exit 1

#### Scenario Outline: CLI usage and lookup errors
**Traces to**: User Story 1, Acceptance Scenario 5 — **Category**: Error Path
- **Given** `<input>`
- **When** the command runs
- **Then** exit `<exit>` with an `error:` line, and the output-path state is preserved

Examples: Dataset D10.

### Feature: Existing behaviour preserved

#### Scenario: REST create unchanged, and #1440 checked
**Traces to**: User Story 4, Acceptance Scenario 1 — **Category**: Happy Path
- **Given** the shared create function is in place
- **When** the existing create tests run, plus a create-then-GET with two fallback models and a `voice` value
- **Then** they pass unchanged, GET returns both fallback models, and `voice` is still empty (REST create's behaviour is unchanged; L17)

#### Scenario: Agent tools unchanged after the join extraction and the applier export
**Traces to**: User Story 3, Acceptance Scenario 2 — **Category**: Happy Path
- **Given** the join helper is extracted and `applySharedConfigArgs` is exported
- **When** the existing `create_agent` and `update_agent` tests run, including `TestWU25EditableFields_MatchCallableUpdateContract` and `TestUpdateAgent_StoresAllParityFieldsAndRevision`
- **Then** they pass unchanged, and no tool schema changed

#### Scenario: Backup and Restore unchanged
**Traces to**: User Story 4, Acceptance Scenario 1 — **Category**: Happy Path
- **Given** the moved predicates
- **When** the backup and restore tests run, plus a restore of an archive with an entry restore skips today
- **Then** they pass, and restore still skips (does not refuse) that entry

---

## Test-Driven Development Plan

### Test Hierarchy

| Level | Scope | Purpose |
|---|---|---|
| Unit | `pkg/omnpkg` (manifest, archive, decoder limits, universal and agent checks, verify, field mapping, name suffix, journal and compensation order with fake stores, staging state machine); `pkg/app/internal/pkgcmd` (flags, lookups, exit codes, prompt, lost connection, with a fake server); `src/lib/api/agents.ts` invalidation | Logic in isolation |
| Integration | `pkg/gateway` handlers on registered routes with a real store and workspace files; rollback with real-store faults; staging races; parity; two-home round trip | All-or-nothing and the contracts |
| E2E | Built binary on two homes; one Playwright row with the Team tab open | The demo, and the open-view refresh |

Run rules: targeted packages only, through `heavy` (for example `heavy go test -p 1 -run '<Name>' ./pkg/omnpkg/`), tags `goolm,stdjson`. Full suites run at the cut.

### Hands-on UAT rows (written at spec time)

S1 does not touch `pkg/agent` or `pkg/session`, so exact-commit hands-on UAT is not mandatory. These rows serve the train campaign and the demo. UAT model: `openrouter` + `deepseek/deepseek-v4.1-flash`.

| Row | Acceptance criterion | Steps | Expected result | Affected area | Validator critical path | Automation candidate |
|---|---|---|---|---|---|---|
| UAT-S1-01 | US-1 AS-1, US-3 AS-2, AS-10 | On A, create "Writer" (SOUL "Always answer in rhyme"). Export. On fresh B, **open the default workspace's Team tab first**, then import with `--yes` from a terminal | Writer appears in the open Team list and graph without reload; its chat answers in rhyme; Edit shows the same model, colour and role | export, import, T-3, T-4, T-7 | Second fresh B; compare `GET /api/v1/agents/<id>` with D3's expected record | Yes |
| UAT-S1-02 | US-3 AS-3 | Import the same file three times | "Writer", "Writer (imported)", "Writer (imported 2)"; three different IDs | name suffix | Validator's own file | Yes |
| UAT-S1-03 | US-2 AS-3 | Change one character in SOUL.md and repack with the old manifest; run `verify`, then `import` | `verify` names SOUL.md; import refused `checksum_mismatch`; B unchanged | verify | Validator's own changed file | Yes |
| UAT-S1-04 | US-1 AS-4 | `omnipus export --agent mia -o mia.omnpkg` | Refused; no file | export refusal | Repeat with `omnipus`, `worker` | Yes |
| UAT-S1-05 | US-1 AS-3 **[S1b]** | External worker with a fake key in an env override; export; search the file | Key absent; `inspect` lists the name under secrets | secrets | Validator's own key | Yes |
| UAT-S1-06 | US-3 AS-6 | Agent with an MCP server and a custom skill; import on fresh B | Preview and result list both as dropped; agent works | references | No MCP binding in B's record | Partly |
| UAT-S1-07 | US-3 AS-11 | On A, create a native worker "Summariser". Export; import on fresh B. Open Team, draw a line from the default Main to it, then ask the Main to have Summariser summarise a short text | The worker has no main chat; after the line is drawn the Main delegates to it and the worker's session shows the summary | worker reachability | Validator draws the line and checks the worker session | Partly |

### Test Implementation Order

Test names are placeholders; qa-lead names the real tests in RED.

| Order | Test Name | Level | Traces to BDD Scenario | Description |
|---|---|---|---|---|
| 1 | TestManifest_VersionRules | Unit | Newer package refused offline; Damaged… (D1 7–9) | Version and unknown-field rules |
| 2 | TestManifest_Relationships | Unit | Damaged… (D12) | Cardinality, ownership, IDs, counts, names |
| 3 | TestArchive_ManifestFirstAndUniversalChecks | Unit | Damaged… (D1, D2) | Order, paths, types, duplicates |
| 4 | TestArchive_DecoderLimits | Unit | Oversized decoder window…; Damaged… (D1 13, 26) | Window, memory, concurrency, decoded-stream count |
| 5 | TestArchive_SizeCaps | Unit | Damaged… (D1 11–16) | At limit, limit + 1 |
| 6 | TestVerify_Checksums | Unit | Verify a changed package; Damaged… (D1 3–6) | Names the file |
| 7 | TestInspect_ManifestOnly | Unit | Inspect reads only the manifest | Corrupt payload, inspect still OK |
| 8 | TestExportSourceRead | Unit | Unsafe or oversized source files… | D11 rows |
| 9 | TestFieldMapping_Export | Unit | The exported record follows Field mapping | D3 export column |
| 10 | TestFieldMapping_Import | Unit | Confirmed import creates…; References the target lacks | D3 import column, D5 |
| 11 | TestExportSelection_Refusals | Unit | Built-in agents are refused; Export refusals… | D4 |
| 12 | TestRemap_NewIDNeverCollides (property) | Unit | Confirmed import creates… | New ID ∉ existing ∪ {source} |
| 13 | TestNameSuffix | Unit | Name clash gets a suffix | D6 |
| 14 | TestJournal_CompensationOrder | Unit | Failure before the commit point…; A failed membership removal stops the rollback… | Order C1 → C2 → C3 → C4; C1 failure (membership removal, or partial C1 after the membership was removed) skips C2/C3 and still attempts C4; partial C2 continues to C3; leftovers and `failed_step` reported |
| 15 | TestStaging_StateMachine | Unit | Staging that is not usable; Staging races | D9, D13 with fake clock |
| 16 | TestPkgCmd_UsageLookupsExitCodes | Unit | CLI usage and lookup errors; Export refusals… | D10 |
| 17 | TestPkgCmd_PromptAndLostConnection | Unit | Interactive confirm; Declined…; Lost connection… | Prompt and lost connection |
| 18 | TestAgentCreatedInvalidatesTeamViews | Unit (vitest) | Team tab refreshes while open | Detail and delegation keys invalidated |
| 19 | TestCreateExtraction_RESTUnchanged | Integration | REST create unchanged, and #1440 checked | Existing tests + fallback GET |
| 20 | TestCreateAgentTool_JoinUnchanged | Integration | create_agent tool unchanged… | Existing tool tests |
| 21 | TestImportCreateParity | Integration | Import and the create paths give the same result… | Two tables: versus REST create, versus `create_agent` |
| 21a | TestImportFieldCoverage | Unit | Every editable field has an import decision | Resolves aliases and nested leaves; control names need a listed decision; fails on any unresolved name; reuses `MappingRows`/`ExcludedRows` vocabulary |
| 21b | TestPackagesApply_MainChatScope | Integration | The main chat is created for the imported Main only | Y and V untouched |
| 21c | TestPackagesApply_NativeWorkerReachable | Integration | A native worker is imported and reached by delegation | No main chat, no edge, then directory lists it after the edge |
| 21d | TestPackages_ManualRecovery | Integration | Manual recovery frees everything; A failed membership removal stops the rollback… | D15 rows 1–12 (unstable list with verification scan, second-workspace repair, C1 failure, partial C1, recreated folder, AD-5 residue with a session beyond the first page and a child session); steps run through the REST API as documented, IDs collected before deleting; slot released |
| 21e | TestPackagesExport_AuditFailure | Integration | Export is refused when it cannot be audited | No bytes |
| 22 | TestBackupRestore_UnchangedAfterMove | Integration | Backup and Restore unchanged | Skip-not-refuse kept |
| 23 | TestPackagesExport_Route | Integration | Export a Main agent…; Structured secrets…; A secret pasted…; Export via REST… | Headers, bytes, scan, audit |
| 24 | TestPackagesImport_Route | Integration | Upload gives a provisional preview…; Damaged…; Upload size through the real route; Package declares a core agent | Registered route; valid ~2 MiB package → 201; exactly 64 MiB random body through a counting body → 422 (not 413) with all bytes consumed; 64 MiB + 1 with declared length and chunked → 413; snapshot oracle allows only staging, staging maintenance and audit; audit failure keeps the refusal |
| 25 | TestPackagesApply_NewMode | Integration | Confirmed import creates…; Import into a named workspace; Workspace selection problems; Changes between preview and apply… | D14 |
| 26 | TestPackagesApply_Rollback | Integration | Failure before the commit point…; A competing workspace edit survives…; Another reader sees the import…; Sessions and messages created during the import…; Content in the agent's home folder follows the folder…; A failed membership removal stops the rollback…; Incomplete rollback…; The rollback reload is not confirmed | D7 rows 1–6, 10–16, 18–21; oracle 1–7; real store and **wired** reload; every race ordered by hooks after the join lock is released, with completion recorded; row 12 asserts the session residue; row 14 persists a removable edge and asserts the save fault was reached (partial C1); rows 19–20 use the real private-memory writer before and after C3 (row 20: writer kept from before C3, cold index, home absent asserted before the write, recreated home = note + identified derived index artifacts only, test closes its writer before the final clean-up); row 21 adds X to a second workspace V during the window and asserts the V residue is allowed (scoped check 3) |
| 27 | TestPackagesApply_PostCommitWarnings | Integration | Failures after the commit point are warnings | D7 rows 7–9, 17; no `agent_created` and no main chat on pending publication; wired reload |
| 28 | TestPackagesApply_ModesConcurrency | Integration | Replace and Merge…; One apply at a time | |
| 29 | TestPackages_AccessRules | Integration | Access rules…; CLI token works… | D8 |
| 30 | TestPackages_ConfigUntouched | Integration | Confirmed import creates… | `config.json` bytes and modification time unchanged |
| 31 | TestPackages_TwoHomeRoundTrip | Integration | Two-install round trip | |
| 32 | TestPackages_S1bExternalAndUnknown **[S1b]** | Integration | [S1b] scenarios, including External-worker fields the runtime does not support | D1 rows 33–34: the runtime check runs before the shared applier, same refusal as `create_agent` |
| 33 | TestPackages_S1aRefusals **[S1a only]** | Integration | [S1a only] External worker export and unknown element…; [S1a only] External-worker package refused at upload | Export and upload refusals, with and without runtime values; replaced by S1b acceptance cases when T-9/T-10 land |
| 34 | TestCLI_ExportImportE2E | E2E | Two-install round trip; Gateway not running; Overwrite with --force… | Built binary |
| 35 | team-tab-import.spec.ts | E2E (Playwright, targeted) | Team tab refreshes while open | Team tab open; import through the API |
| — | BenchmarkPackageS1 | Benchmark (target only) | — | Performance targets; not a gate |

### Test Datasets

#### Dataset D1: Package-level checks at upload (and verify)

| # | Input | Boundary Type | Expected | Traces to |
|---|---|---|---|---|
| 1 | Valid S1 package | Happy | 201 | BDD: Upload gives a provisional preview… |
| 2 | Native agent, `SOUL.md` blank | Create rule | 422 `element_not_creatable`, `field: soul` | BDD: Damaged, unsafe or non-creatable… |
| 3 | One byte of `SOUL.md` changed | Tamper | 422 `checksum_mismatch`; verify exit 1 | same; BDD: Verify a changed package |
| 4 | Manifest size wrong, hash right | Tamper | 422 `checksum_mismatch` | same |
| 5 | Entry not listed | Tamper | 422 `manifest_mismatch` | same |
| 6 | Listed `AGENT.md` missing | Tamper | 422 `manifest_mismatch` | same |
| 7 | `format_version` 2 | Max + 1 | 422 `package_too_new` | same; BDD: Newer package… |
| 8 | `format_version` 0 / missing | Below min / null | 422 `not_a_package` | same |
| 9 | Unknown manifest field | Forward compatible | 201 | BDD: Upload gives… |
| 10 | Agent `schema` 2 | Max + 1 | 422 `element_schema_too_new` | BDD: Damaged… |
| 11 | Body of exactly 64 MiB of random bytes through a counting request body (transport boundary only; not a package) | At limit | 422 `not_a_package` (not 413) **and** 67,108,864 bytes consumed | BDD: Upload size through the real route |
| 12 | 64 MiB + 1 byte, declared length | Limit + 1 | 413 `package_too_large` | same |
| 13 | Decoded stream 256 MiB + 1 byte (high-ratio bomb) | Limit + 1 | 413; reading stops at the limit | BDD: Damaged… |
| 14 | 10,001 entries | Limit + 1 | 413 | same |
| 15 | `agent.json` 1 MiB + 1 byte (1 MiB exactly accepted) | Limit + 1 | 413 | same |
| 16 | Manifest 1 MiB + 1 byte | Limit + 1 | 413 | same |
| 17 | gzip instead of zstd | Wrong format | 422 `not_a_package` | same |
| 18 | Random bytes / empty body | Wrong / empty | 422 `not_a_package` | same |
| 19 | Manifest second | Order | 422 `not_a_package` | same |
| 20 | `agent.json` not JSON | Invalid | 422 `invalid_element` | same |
| 21 | Name `""` / `"   "` | Empty | 422 `element_not_creatable`, `field: name` | same |
| 22 | `type` core / system | Forbidden | 422 `invalid_element` | BDD: Package declares a core agent |
| 23 | Worker with empty description | Create rule | 422 `element_not_creatable`, `field: description` | BDD: Damaged… |
| 24 | `fallback_models` with 3 entries | Create rule | 422 `element_not_creatable`, `field: fallback_models` | same |
| 25 | Valid package of about 2 MiB: one Main, 1 MiB random-text `SOUL.md`, 1 MiB random-text `AGENT.md` (does not compress) | Above the general request limit, within all content limits | 201 | BDD: Upload size through the real route |
| 26 | 2 KB file requesting a 512 MiB window | Decoder window | 422 `package_too_large`, `details.limit: window` | BDD: Damaged…; BDD: Oversized decoder window… |
| 27 | `context_window_override` 0 | `create_agent` rule | 422 `element_not_creatable`, `field: context_window_override` | BDD: Damaged… |
| 28 | Worker with `voice` "alloy" | `create_agent` rule | 422 `element_not_creatable`, `field: voice` | same |
| 29 | `color` "not-a-colour" (or unknown figure/role) | Create rule | 422 `element_not_creatable`, `field: color` | same |
| 30 | **[S1a only]** External worker record, with runtime values | Split boundary | 422 `external_worker_not_supported_yet` | BDD: [S1a only] External-worker package refused at upload |
| 31 | **[S1a only]** External worker record, without runtime values | Split boundary | 422 `external_worker_not_supported_yet` | same |
| 32 | 64 MiB + 1 byte sent chunked, no declared length | Limit + 1, unknown length | 413 `package_too_large` | BDD: Upload size through the real route |
| 33 | **[S1b]** External worker with `memory_enabled` false | Runtime rule (`agentmutation.ValidateFields`) | 422 `element_not_creatable`, `field: memory_enabled` | BDD: [S1b] External-worker fields the runtime does not support |
| 34 | **[S1b]** External worker with `context_window_override` 64000 | Runtime rule | 422 `element_not_creatable`, `field: context_window_override` | same |

#### Dataset D2: Entry paths and types (universal checks)

| # | Entry | Type | Expected | Traces to |
|---|---|---|---|---|
| 1 | `/etc/passwd` | Absolute | 422 `unsafe_path` | BDD: Damaged… |
| 2 | `omnipus/agents/x/../../../evil` | Traversal | 422 `unsafe_path` | same |
| 3 | `omnipus\agents\x\SOUL.md` | Backslash | 422 `unsafe_path` | same |
| 4 | `C:/x` | Drive letter | 422 `unsafe_path` | same |
| 5 | `omnipus/agents/x/extra.txt` **owned by the agent element** | Outside the agent layout | 422 `unsafe_path` | same |
| 6 | Symlink entry | Link | 422 `unsafe_entry_type` | same |
| 7 | Hard link | Link | 422 `unsafe_entry_type` | same |
| 8 | Device / FIFO | Special | 422 `unsafe_entry_type` | same |
| 9 | Same path twice | Duplicate | 422 `unsafe_entry_type` | same |
| 10 | `omnipus/agents/作家-1/SOUL.md` | Non-ASCII ID | 422 `invalid_element` | same |
| 11 | `skills/writing/SKILL.md` owned by an unknown element | Unknown layout | universal checks only; skipped **[S1b]** / 422 `element_type_not_supported` (S1a) | BDD: [S1b] Unknown element types… |

#### Dataset D3: Field mapping (export column, then import column)

The rows are exactly the rows of the "Field mapping" table. Each row is tested with a **non-default** value. Additional rows:

| # | Case | Expected | Traces to |
|---|---|---|---|
| 1 | `SOUL.md` = "\n  Answer in rhyme.\n\n" | Package: byte-identical. Stored after import: "Answer in rhyme." | BDD: Confirmed import creates… |
| 2 | `AGENT.md` = "  rules\n" | Byte-identical in the package and after import | same |
| 3 | `description` = "  Drafts  " | Stored "Drafts" | same |
| 4 | Main with `memory_enabled` false, `context_window_override` 64000, `voice` "alloy" | Stored unchanged (as `create_agent` would store them) | same |
| 5 | `subagents.allow_agents` ["mia"], `subagents.model` set | Absent in the package; absent after import | BDD: The exported record follows Field mapping |
| 6 | `model.fallbacks` ["x"] | In the package; left out at import, listed `field_not_carried` | same |
| 7 | `color` "not-a-colour" (hand-made record) | Refused at upload, `element_not_creatable`, `field: color` (no normalisation) | same |
| 8 | All three kinds (Main, Subagent, Subagent External **[S1b]**) with all their non-default fields | Per table | BDD: Two-install round trip |

#### Dataset D4: Export refusals for shipped agents

| # | ID | Expected | Traces to |
|---|---|---|---|
| 1–8 | `mia`, `jim`, `omnipus`, `worker`, `planner`, `researcher`, `judge`, `plansupervisor` | 422 `core_agent_not_exportable` | BDD: Built-in agents are refused |
| 9 | A user-created Subagent (UUID) | 200 | BDD: Export a Main agent… |

#### Dataset D5: Reference rules on import (target = fresh install unless stated)

| # | Packaged | Target | Stored | Warning | Traces to |
|---|---|---|---|---|---|
| 1 | `skills: ["writing"]` | "writing" installed | kept, bound | `skill_bound_local` | BDD: References the target lacks |
| 2 | `skills: ["my-custom"]` | absent | dropped | `skill_dropped` | same |
| 3 | `skills: ["Bad Slug!"]` | — | dropped | `skill_dropped` | same |
| 4 | `tools.mcp.servers: [{id: "github"}]` | "github" configured | none | `mcp_binding_dropped` | same |
| 5 | `{"bash": "deny"}` | known tool | `deny` | — | same |
| 6 | `{"retired_tool_x": "allow"}` | unknown | absent | `tool_policy_dropped` | same |
| 7 | `{"mcp_github_*": "deny"}` | not a static tool | absent | `tool_policy_dropped` | same |
| 8 | `allow` on an always-ask tool | — | `ask` | `tool_policy_tightened` | same |
| 9 | a tool missing from the map | — | seed value for the type | — | same |
| 10 | `model.provider: "openrouter"` | not configured | kept | `provider_not_configured` | same |
| 11 | `max_tool_iterations: 900` | global 200 | refused | 422 `element_not_creatable`, `field: max_tool_iterations` (at upload; at apply if the global is lowered after the preview) | same |
| 12 | **[S1b]** hand-made `env_overrides` | — | absent | `runtime_settings_removed` | BDD: [S1b] Executor values… |

#### Dataset D6: Name suffixes

| # | Existing | Incoming | Final | Traces to |
|---|---|---|---|---|
| 1 | none | "Writer" | "Writer" | BDD: Name clash gets a suffix |
| 2 | "Writer" | "Writer" | "Writer (imported)" | same |
| 3 | "writer " | "Writer" | "Writer (imported)" | same |
| 4 | "Writer", "Writer (imported)" | "Writer" | "Writer (imported 2)" | same |
| 5 | "Writer" … "(imported 98)" | "Writer" | "Writer (imported 99)" | same |
| 6 | "Writer" … "(imported 99)" | "Writer" | 409 `name_suffix_exhausted` | same |
| 7 | "A"×100 | "A"×100 | "A"×89 + " (imported)" (100 characters) | same |
| 8 | none | "A"×100 | "A"×100 | same |
| 9 | none | "A"×101 | 422 `element_not_creatable`, `field: name` | same |
| 10 | "Omnipus" | "omnipus" | "omnipus (imported)" | same |
| 11 | "Mia" | "Mia" | "Mia (imported)" | same |
| 12 | "作家" | "作家" | "作家 (imported)" | same |

#### Dataset D7: Fault points (real store unless stated)

| # | Fault after | Expected | Oracle | Traces to |
|---|---|---|---|---|
| 1 | E4 partly written (record written, `SOUL.md` replace fails: `PersistencePartial`) | 500 `import_rolled_back` | Rollback checks 1–6 | BDD: Failure before the commit point… |
| 2 | E4 complete | same | same | same |
| 3 | E5 (`AGENT.md`) | same | same, `AGENT.md` gone | same |
| 4 | E8 (team entry) | same | same | same |
| 5 | E8, self-row fails; the join helper releases the lock; a competing writer adds Z to the same workspace and finishes; then compensation runs | same | Z kept, X gone | BDD: A competing workspace edit… |
| 6 | E4 + `DeleteState` fails | 500 `import_rollback_incomplete` | leftover named with agent ID and staging ID; C4 reload ran; `needs-cleanup` | BDD: Incomplete rollback… |
| 7 | Commit, then the live-publish reload returns an **error** (wired pipeline) | 200 `activation: pending` | agent kept; **no** `agent_created`; no main chat attempted; CLI restart line | BDD: Failures after the commit point… |
| 8 | Commit, then main chat fails | 200, `main_chat_pending` | agent kept | same |
| 9 | Commit, then audit fails | 200, `audit_failed` | agent kept | same |
| 10 | Reader race, lock-compatible: E4; unrelated reload forced and confirmed; the self-row fails; the join releases W's lock; C1 held by a hook; a real workspace-detail request for W is sent and its response received; then C1 released | 500 `import_rolled_back` | checks 1–7; recorded order proves the read completed before C1; residue exactly X's main chat in W if the read prepared one (AD-5) | BDD: Another reader sees the import… |
| 11 | E8 fails and the rollback audit write also fails | 500 `import_rolled_back` (unchanged) | checks 1–7; audit failure in the gateway log | BDD: Failure before the commit point… |
| 12 | **Populated sessions**: E4; unrelated reload forced and confirmed; the self-row fails; join lock released; C1 held; `POST /api/v1/sessions` for X, plus one real WebSocket chat message to X saved in its transcript; then C1 released | 500 `import_rolled_back` with the note | checks 1–7; both sessions and the saved message **remain** (AD-5); a new message to X is refused after C4; after Manual recovery step 4 only a protected main chat (if any) remains | BDD: Sessions and messages created during the import… |
| 13 | Self-row fails, then C1 **membership removal** fails | 500 `import_rollback_incomplete`, `failed_step: "C1"` | record and folder kept (C2, C3 skipped); C4 attempted; `needs-cleanup` | BDD: A failed membership removal stops the rollback… |
| 14 | **Partial C1**: E8 done; self-row fails; join lock released; C1 held; a separate valid graph edit persists an edge A → X in W (200); C1 released; C1's delegation save fails | 500 `import_rollback_incomplete`, `failed_step: "C1"` | save fault reached (hook counter); X no longer in W's team; edge A → X still stored; record and folder kept (C2, C3 skipped); C4 attempted; `needs-cleanup` | BDD: A failed membership removal stops the rollback… |
| 15 | X listed by an earlier reload; self-row fails; C4 reload returns an **error** | 500 `import_rolled_back`, `activation: "pending"` | store has no X; no `agent_created`; CLI restart line | BDD: The rollback reload is not confirmed |
| 16 | Same, C4 reload **times out** | same | same | same |
| 17 | Commit, then the live-publish reload **times out** | 200 `activation: pending` | as row 7 | BDD: Failures after the commit point… |
| 18 | **Partial C2**: `DeleteState` removes the entity, then fails on `SOUL.md`; C3 then deletes the folder | 500 `import_rolled_back` | checks 1–7 | BDD: Failure before the commit point… |
| 19 | **Home write before C3**: E4; unrelated reload confirmed; self-row fails; join lock released; C1 held; a private memory note saved for X with the real writer (success asserted, file present under `agents/X/`); C1 released | 500 `import_rolled_back` with the note | checks 1–7; the note is gone with `agents/X/` (check 5) | BDD: Content in the agent's home folder… |
| 20 | **Home write after C3**: same start; hook after C3, before C4; writer kept from before C3 (cold index); home absent asserted; a private memory note saved for X through it (success asserted); C4 confirmed | 500 `import_rolled_back` with the note | checks 1–7 with the check-5 exception: `agents/X/` holds exactly the note plus the derived index and bookkeeping artifacts of that write (identified by a control run; no record, `SOUL.md`, `AGENT.md` or other file) (AD-6); after the test closes its writer and runs the final clean-up, the whole folder is absent | same |
| 21 | **Second workspace**: E4, E8 in W; unrelated reload confirmed; self-row fails; join lock released; C1 held; a valid `PUT /api/v1/workspaces/V` adds X to V's team with an edge involving X (200); C1 released; C4 confirmed | 500 `import_rolled_back` with the note | checks 1–7 with check 3 scoped: W has no X entry or edge; V keeps X's entry and edge (AD-5); D15 row 12 repairs V | BDD: Failure before the commit point… |

(A fault after E9 is a post-commit fault by definition. All rows run with a wired reload pipeline.)

#### Dataset D15: Manual recovery

| # | Leftover | Steps | Expected | Traces to |
|---|---|---|---|---|
| 1 | Incomplete rollback, record present (D7 row 6) | Delete agent (Team tab, or GET for `revision` then `DELETE …?revision=`), delete `agents/<id>/` if it is still there, delete the staging folder | No record, folder or membership for X; next upload 201; 5 slots free | BDD: Manual recovery frees everything |
| 2 | Folder-only leftover (`DeleteState` done, folder removal failed) | With the gateway stopped, delete `agents/<id>/`; delete the staging folder | same | same |
| 3 | Gateway stopped after E8 (simulated restart), unfinished journal kept as an orphan | Next upload shows `unfinished_import_found` with the ID; Delete agent; delete the staging folder | same; the warning no longer appears | same |
| 4 | Five retained stagings fill every slot | Retire each by deleting its folder | The next upload is admitted without a restart | same |
| 5 | C1 membership removal failed (D7 row 13): record, folder and membership present | Restart if X is not listed; Delete agent (removes team entries and edges naming X in every workspace); delete `agents/<id>/` if left (gateway stopped); delete the staging folder | No record, folder, membership or edge; next upload 201 | same |
| 6 | Folder with only `SOUL.md` left after a partial C2 whose C3 also failed | With the gateway stopped, delete `agents/<id>/`; delete the staging folder | same | same |
| 7 | AD-5 residue: ordinary session and populated chat for X from D7 row 12 | Manual recovery step 4: all pages with `flat=true`, IDs collected first, then `DELETE /api/v1/sessions/<id>` for each; delete the staging folder if still there | No ordinary session for X; only a protected main chat may remain | same |
| 8 | Partial C1 (D7 row 14): record and folder present, X not a member, edge A → X stored | Delete agent (its cascade removes edges naming X in every workspace); delete `agents/<id>/` if left (gateway stopped); delete the staging folder | No record, folder or edge naming X; next upload 201 | same |
| 9 | AD-5 residue spread over pages: 60 other sessions newer than X's ordinary session, plus a child session for X under another agent's surviving parent session | Manual recovery step 4 as documented: `flat=true`, follow every `next_cursor` as `offset`, do not stop on a page with no X rows, collect IDs, then delete | Both X sessions found and deleted; no session for X remains; the other agent's parent session untouched | same |
| 10 | Folder recreated after a complete rollback (D7 row 20) | Test closes its kept writer (simulated gateway stop); delete `agents/<id>/`; start; step 4; delete the staging folder | No `agents/<id>/`; next upload 201 | same |
| 11 | Unstable list: as row 9, and the test deletes one unrelated session on the first page between the first and second page requests | Step 4 as documented: the verification scan from `offset=0` after the deletions | The procedure finds X's session on a re-scan and deletes it; or, if the list keeps changing, it reports the clean-up as unverified, never complete | same |
| 12 | X added to a second workspace V during the window (D7 row 21), record already gone | Step 5: `GET /api/v1/workspaces?status=all`; read V with its revision; `PUT /api/v1/workspaces/V` with `revision`, `core_team` without X, `delegation` without X's edges | V has no X entry or edge; V's other members and edges unchanged; no new endpoint used | same |

#### Dataset D8: Access (through registered routes)

| # | Condition | Expected | Traces to |
|---|---|---|---|
| 1 | No header | 401 | BDD: Access rules… |
| 2 | Wrong bearer | 401 | same |
| 3 | Human admin bearer | 200/201 | same |
| 4 | `cli.token` | 200/201, no password prompt | BDD: CLI token works… |
| 5 | Developer bypass mode on | 503 | BDD: Access rules… |
| 6 | Platform mode | 404 | same |
| 7 | Export or apply JSON body over 1 MiB | 413 | same |

#### Dataset D9: Staging IDs (fake clock; the clock starts at `ready`)

| # | State | Expected | Traces to |
|---|---|---|---|
| 1 | `ready`, 29 min 59 s | 200 | BDD: Staging that is not usable |
| 2 | `ready`, exactly 30 min | 404 `staging_not_found` | same |
| 3 | `ready`, 30 min 1 s | 404 | same |
| 4 | Never existed | 404 | same |
| 5 | `done` (consumed) | 404 | same |
| 6 | `needs-cleanup` | 404 | same |
| 7 | `..%2F..%2Fentities` | 404, no file access | same |
| 8 | After a gateway restart | 404 | same |
| 9 | Back to `ready` after a 404 `workspace_not_found` | 200 on retry with a valid workspace | BDD: Workspace selection problems |

#### Dataset D10: CLI usage and lookups

| # | Input | Exit | Output path after | Traces to |
|---|---|---|---|---|
| 1 | `export -o w.omnpkg` (no `--agent`) | 2 | absent | BDD: CLI usage and lookup errors |
| 2 | `export --agent Writer` (no `-o`) | 2 | — | same |
| 3 | `export --agent nope -o w.omnpkg` | 1, names "nope" | absent | BDD: Export refusals leave no file |
| 4 | `export --agent Writer -o w.omnpkg` with two "Writer" agents | 1, lists both IDs | absent | same |
| 5 | `export --agent Writer -o w.omnpkg`, file exists | 1 | old bytes | same |
| 6 | `export --agent Writer -o /readonly/w.omnpkg` | 1 | absent | same |
| 7 | `export --agent <blank-prompt agent> -o w.omnpkg` | 1 (`agent_prompt_missing`) | absent | same |
| 8 | `import` (no file) | 2 | — | BDD: CLI usage… |
| 9 | `import missing.omnpkg` | 2 | — | same |
| 10 | `package inspect dir/` | 2 | — | same |
| 11 | `import w.omnpkg --workspace Research` (unique name) | 0, joins Research | — | BDD: Export refusals… / Import into a named workspace |
| 12 | `import w.omnpkg --workspace Team` (two match) | 1, lists both IDs | — | same |
| 13 | `import w.omnpkg --workspace Nope` | 1 (`workspace_not_found`) | — | same |
| 14 | `import w.omnpkg --mode replace` | 1 (`import_mode_not_available`) | — | BDD: Replace and Merge… |
| 15 | `package unknown x` | 2 | — | BDD: CLI usage… |

#### Dataset D11: Export source reads

| # | Source state | Expected | Traces to |
|---|---|---|---|
| 1 | `AGENT.md` absent | Package without it | BDD: Export an agent with no AGENT.md |
| 2 | `AGENT.md` is a symlink to `credentials.json` | 422 `agent_file_unreadable`, `check: link`; link not followed | BDD: Unsafe or oversized source files… |
| 3 | `SOUL.md` is a folder | 422, `check: not_regular` | same |
| 4 | `AGENT.md` unreadable (mode 000) | 422, `check: unreadable` (never silently omitted) | same |
| 5 | `SOUL.md` exactly 1 MiB | 200 | same |
| 6 | `SOUL.md` 1 MiB + 1 byte | 422, `check: too_large` | same |
| 7 | `SOUL.md` replaced during export | The package is consistent: the hash matches the captured bytes | same |

#### Dataset D12: Manifest relationships

| # | Package | Expected | Traces to |
|---|---|---|---|
| 1 | Zero elements | 422 `no_supported_element` | BDD: Damaged… |
| 2 | Only an unknown element | 422 `no_supported_element` | same |
| 3 | Two agent elements | 422 `too_many_agents` | same |
| 4 | Two elements with the same `source_id` | 422 `manifest_mismatch` | same |
| 5 | A file listed in two elements | 422 `manifest_mismatch` | same |
| 6 | `elements[].files` lists a path missing from `files` | 422 `manifest_mismatch` | same |
| 7 | `counts.agent` = 2 with one element | 422 `manifest_mismatch` | same |
| 8 | Element `name` "Writer", record `name` "Editor" | 422 `manifest_mismatch` | same |
| 9 | `source_id` ≠ folder name, or ≠ record `id` | 422 `manifest_mismatch` | same |
| 10 | Agent element without `agent.json` | 422 `manifest_mismatch` | same |

#### Dataset D13: Staging races

| # | Race | Outcome | Traces to |
|---|---|---|---|
| 1 | Apply moves X to `applying` at 29:59; an upload cleans up at 30:01 | X not deleted; apply completes | BDD: Staging races |
| 2 | Two applies of the same X at once | One 200; the other 409 `import_in_progress` or 404 | same |
| 3 | Two uploads compete for the last free slot | One 201; the other 409 `too_many_staged_imports` | same |
| 4 | Upload fails while decoding | Slot released; the next upload is admitted | same |
| 5 | Restart leaves an orphan folder with a finished journal, and one with an unfinished journal | The first is deleted on the next upload; the second is kept, holds a slot and produces `unfinished_import_found` (S1a) | same |
| 6 | A `needs-cleanup` staging whose folder the owner deleted | The next upload drops the entry and admits the new upload | same |

#### Dataset D14: Preview freshness and workspaces

| # | Change between preview and apply | Expected | Traces to |
|---|---|---|---|
| 1 | Agent "Writer" created | "Writer (imported)" + `changed_since_preview` | BDD: Changes between preview and apply… |
| 2 | Same file previewed twice, both applied | The second gets a suffix + `changed_since_preview` | same |
| 3 | Skill "writing" uninstalled | Dropped + `changed_since_preview` | same |
| 4 | Target workspace deleted | 404 `workspace_not_found`, staging `ready` | BDD: Workspace selection problems |
| 5 | No default workspace, none named | 409 `default_workspace_missing` | same |
| 6 | Global `max_tool_iterations` lowered below the agent's value | 422 `element_not_creatable`, staging `ready` | same |

### Regression Test Requirements

| Existing Behaviour | Existing Test | New Regression Test | Notes |
|---|---|---|---|
| REST create rules | `rest_agents_create_test.go`, `rest_agents_adr090_test.go`, `rest_max_tool_iterations_api_test.go`, `rest_tool_policy_write_guard_test.go` | TestCreateExtraction_RESTUnchanged (+ #1440 create-then-GET) | Unchanged |
| Agent update and coverage guard | existing update tests that call `withToolPolicyCoverageGuard` | No | Unchanged after the coverage split |
| `create_agent` join and main chat | `agent_create_mains_gate_test.go` and the other `create_agent` tests | TestCreateAgentTool_JoinUnchanged | Unchanged |
| Agent tool field parity | `TestWU25EditableFields_MatchCallableUpdateContract`, `TestUpdateAgent_StoresAllParityFieldsAndRevision` | TestImportFieldCoverage (new invariant) | Tool schemas unchanged; `applySharedConfigArgs` exported only |
| REST create does not store `voice` | — (no existing test) | TestCreateExtraction_RESTUnchanged asserts it stays empty | Existing behaviour pinned on purpose (L17), so the refactor cannot change it silently |
| Backup / Restore (skip policy) | `rest_backup_test.go`, `mail_backup_exclusion_red_test.go` | TestBackupRestore_UnchangedAfterMove | Restore still skips |
| Command list | `TestNewRootCommand_KeptCommandsPresent` | Updated (three names) | The only intended expectation change |
| Removed verbs | `TestNewRootCommand_RemovedVerbsAreUnknown`; CI `cli-verb-guard` | No | |
| Auto-start for `omnipus <agent> <prompt>` | existing `autoStartAndRetryWith` tests | No | |
| `agent_created` refresh | existing tests of `invalidateAfterAgentCreate` | TestAgentCreatedInvalidatesTeamViews | The list refresh still happens |

---

## Functional Requirements

**Format and reading**
- **FR-001**: A package MUST be a zstd-compressed tar whose first entry is `manifest.json`.
- **FR-002**: An S1 export MUST write `format` "omnpkg", `format_version` 1, `scope` "custom", `includes_sessions` false, and `counts` matching the elements.
- **FR-003**: Every non-manifest entry MUST be listed in `files` with its SHA-256 and size, and MUST belong to exactly one element. Upload and `verify` MUST refuse unlisted entries, missing listed entries, mismatches and double ownership, naming the file.
- **FR-004**: Readers MUST refuse a higher `format_version`, accept the same or lower, and ignore unknown manifest fields.
- **FR-005**: Readers MUST refuse an `agent` element `schema` higher than they know.
- **FR-006** **[S1b]**: An unknown element type MUST pass the universal checks and checksum verification, MUST be listed as skipped, and its files MUST NOT leave staging. **[S1a]**: refused with `element_type_not_supported`.
- **FR-007**: Universal checks (all entries): no absolute path, no `..`, no backslash, no drive letter, regular files and folders only, no duplicate path. Agent layout checks (agent-owned entries only): exactly `agent.json`, plus `SOUL.md` (native) and optionally `AGENT.md`, under `omnipus/agents/<source-id>/`. The moved predicates MUST keep restore's skip policy unchanged.
- **FR-008**: Limits: upload ≤ 64 MiB; decoded stream (all tar bytes, headers and padding included) ≤ 256 MiB; ≤ 10,000 entries; manifest ≤ 1 MiB; each agent file ≤ 1 MiB; zstd decoder max window 8 MiB, max memory 256 MiB, concurrency 1, for upload, `verify` and `inspect`. The writer MUST use a window ≤ 8 MiB.
- **FR-009**: The relationships in Dataset D12 MUST hold: exactly one agent element in S1, unique element IDs, `source_id` = folder = record `id`, element name = record name, `counts` match the elements.

**Export**
- **FR-010**: Export MUST accept exactly one user-created agent (Main or Subagent; Subagent External in **[S1b]**; refused with `external_worker_not_supported_yet` in S1a) and return the package bytes with a safe filename.
- **FR-011**: Export MUST refuse the agents in Dataset D4 with `core_agent_not_exportable`.
- **FR-012**: The packaged record MUST follow the "Export" column of Field mapping.
- **FR-013**: Export MUST NOT open `credentials.json`, `master.key`, `cli.token`, `auth.json` or `config.json`. Values from those files that are not also present in the agent's free text MUST NOT appear in the package. It does not promise that free text is secret-free.
- **FR-014** **[S1b]**: Removed environment-override names MUST be listed in `needs_secrets`.
- **FR-015**: `SOUL.md` and `AGENT.md` MUST be packaged byte-identical to the captured source bytes. `AGENT.md` is included only when present.
- **FR-016**: Each export MUST write exactly one audit entry (actor, agent ID, package SHA-256). If that write fails, the export MUST be refused (500 `audit_failed`) and no package bytes sent.
- **FR-017**: Export MUST leave no file in the data folder, and the CLI MUST write its output atomically.
- **FR-018**: Export MUST read source files without following links, as regular files only, bounded to 1 MiB, and MUST refuse with `agent_file_unreadable` otherwise. An unreadable existing file MUST be refused, not omitted. Hashes MUST be computed from the same captured bytes that are packaged.
- **FR-019**: Export MUST refuse a record that the import would refuse under the create rules (blank native prompt, worker without a description), so it never produces a package it would refuse itself.

**Upload and preview**
- **FR-020**: Upload MUST run every format check plus a dry run of the create rules before answering. On failure it MUST release its slot and delete its folder. **[S1a only]** An agent record that is an external worker MUST be refused with 422 `external_worker_not_supported_yet` before the staging becomes `ready`, with or without runtime values.
- **FR-021**: The preview MUST list the elements to add with their *proposed* names, skipped elements, warnings and `needs_secrets`, and MUST be marked provisional.
- **FR-022**: Staging MUST follow "Staging lifecycle": at most 5 slots, reserved before decoding; a 30-minute expiry from `ready`, expired at exactly 30:00; `applying` and `needs-cleanup` never expire; a retained staging is retired only by deleting its folder, which the next upload notices and which releases its slot.
- **FR-023**: Upload MUST NOT change any agent, workspace, skill, connector or configuration file. The only allowed writes are its own staging folder, staging maintenance (removing expired, finished-orphan or retired stagings) and the audit log.

**Apply**
- **FR-030**: `mode` is required; only `new` is accepted in S1; `replace`/`merge` give 422 `import_mode_not_available`.
- **FR-031**: The new agent ID MUST be a fresh UUID, different from the source ID and from every existing ID.
- **FR-032**: Name clash handling as in Dataset D6, decided at apply time.
- **FR-033**: Stored values MUST follow the "Import" column of Field mapping and Dataset D5; each drop, tightening, default or local binding MUST be listed.
- **FR-034**: Apply MUST create the agent through the shared create function (the one REST create uses), with the coverage check under the config lock and **without writing `config.json`**.
- **FR-035**: The agent MUST join the named workspace, or the default one, through the shared join helper (team entry + self-row in one workspace lock). An unknown workspace gives 404, and no named workspace with no default gives 409, both before any write. No other agent gains a delegation line to it.
- **FR-036**: After the commit point, the agent MUST be published by a full reload (`triggerReloadAndWaitOutcome`). Only when that reload is **confirmed** MUST `agent_created` be sent and P3 run. On **error or timeout**, the result MUST say `activation: "pending"`, MUST NOT send `agent_created`, MUST keep the stored agent (never report it absent), and the CLI MUST tell the owner to restart the gateway or wait and check the agent list.
- **FR-036a**: After confirmed publication, a Main agent MUST get its own main chat through `ensureMainSession` for that agent and workspace only, when `mainSessionPairEligible` allows it. No other member's main chat is created or changed. A failure is the warning `main_chat_pending`.
- **FR-036b**: A native worker MUST get no main chat. The result MUST say that it is reached by delegation once the owner draws a line to it from a Main agent.
- **FR-037**: A failure before the commit point MUST remove everything inside the **rollback boundary**, defined by places and records, not by who wrote them: X's record, X's home folder `agents/X/` with everything inside it (including content other requests wrote there before C3), X's team entry and every delegation edge involving X in the import's workspace. It does so with compensations C1 → C2 → C3, then always attempts the C4 reload, and answers `import_rolled_back`. **If C1 fails (including a partial C1, after the membership was removed), C2 and C3 MUST NOT run** (the record and folder stay as the recovery handle). A partial C2 continues with C3. The rollback oracle checks 1–7 MUST hold, with the allowed differences AD-1…AD-6; the live-list part of check 1 applies only when C4 is confirmed, otherwise the answer MUST carry `details.activation: "pending"` and the CLI MUST print the neutral C4 line after the outcome line. Rollback MUST NOT claim to remove anything outside the boundary that other requests created while X was visible (AD-5: sessions with or without messages, main chats, task or audit entries, files outside `agents/X/`), and MUST NOT claim that `agents/X/` stays absent while work admitted for X may still run (AD-6). The answer MUST carry a note saying so and pointing to the final clean-up. A failed compensation answers `import_rollback_incomplete` with `failed_step`, leftovers, agent ID and staging ID; C4 is still attempted, and the staging moves to `needs-cleanup`.
- **FR-037a**: The manual recovery procedure in "Rollback contract" MUST work for every leftover shape inside the boundary (a, a2, b, c, d: including a C1 failure, a partial C1, a partial C2 and a folder recreated after a complete rollback) and for the deletable AD-5 residue (ordinary sessions and chats). Its session step MUST enumerate every session for X through the existing API (`flat=true`, every `next_cursor` followed, no stop on a page without X rows, IDs collected before deleting, errors reported as incomplete), under a stable-list precondition, with a fresh verification scan after deleting; without a stable list the result MUST be reported as incomplete/unverified. For a record-absent rollback it MUST include the workspace repair through the existing `PUT /api/v1/workspaces/<id>` with revision (step 5). It MUST state that a protected main chat remains and is not removed by retention in S1. After it a new upload MUST be admitted (Dataset D15 rows 1–12).
- **FR-038**: One apply at a time per install (409 `import_in_progress`).
- **FR-039**: State transitions as in "Staging lifecycle".
- **FR-040**: Exactly one audit entry per apply outcome and per safety refusal at upload. An audit failure never changes an upload refusal or a rollback answer (it goes to the gateway log); after the commit point it is the warning `audit_failed`, never "nothing imported".
- **FR-041**: The result MUST list the created elements (source ID, new ID, final name, workspace), warnings including `changed_since_preview`, and `needs_secrets`.
- **FR-042**: Apply MUST re-run the preview checks on the current state before its first write ("Preview freshness").
- **FR-043** **[S1b]**: An imported external worker's result MUST carry the existing "cannot run" text when its tool has no program set, and `runtime_settings_removed` pointing to Edit → Runtime for environment overrides and additional arguments.
- **FR-044**: An upload that finds an orphan staging with an unfinished journal MUST warn `unfinished_import_found`, naming the agent ID, the staging folder and the recovery section (moved from S1b to S1a in revision 3).
- **FR-047**: Import MUST store `voice`, `memory_enabled` and `context_window_override` with the `create_agent` tool's rules: first its runtime check (`agentmutation.ValidateFields`, which refuses `memory_enabled` and `context_window_override` for external workers **[S1b]**), then the shared applier (`applySharedConfigArgs`). REST create's behaviour MUST stay unchanged.
- **FR-048**: Every field name of the agent tools' create and update lists, the REST create schemas and `AgentUpdateRequest` MUST resolve, through the alias table, to Field mapping rows (nested leaves included), or be a listed control name with its decision (`TestImportFieldCoverage`). No unlisted ignore set.

**Access**
- **FR-045**: The routes MUST be registered in local mode only, with authentication and `RequireNotBypass`. The upload route MUST accept bodies up to 64 MiB, copying the whole body into the staging folder before decoding, and MUST answer 413 `package_too_large` for a larger body whether or not its length is declared; export and apply keep the 1 MiB JSON limit. Tests MUST go through the registered routes.
- **FR-046**: No password re-entry in New mode (DA-1).

**Command line**
- **FR-050**: `omnipus export --agent <id|name> -o <file> [--force]`: resolve by exact ID first, then by name (letter case ignored); refuse unknown or ambiguous agents (listing the IDs); refuse an existing output unless `--force`; write atomically; print the free-text notice.
- **FR-051**: `omnipus import <file> [--workspace <id|name>] [--yes] [--mode new]`: resolve the workspace by ID, then by unique name (ambiguous → refuse, listing IDs); upload; print the preview; confirm; apply; print the result. On a lost apply connection, print the "may or may not have completed" text.
- **FR-052**: `inspect` reads only the bounded manifest and makes no integrity claim; `verify` performs every format check. Neither needs a gateway nor writes anything.
- **FR-053**: Exit codes 0 / 1 / 2 as defined.
- **FR-054**: `export`/`import` start a stopped gateway through the shared auto-start steps and authenticate with `cli.token`.
- **FR-055**: The CLI MUST NOT read or write the data folder for export or import, except `cli.token` and the gateway address.

**Frontend, contracts, docs**
- **FR-056**: On `agent_created`, the app MUST also invalidate every workspace detail and delegation query.
- **FR-060**: Contracts and regeneration come before code; only generated types are used.
- **FR-061**: User-facing docs are updated per DOC-001…003 (checked by the docs-verifier audit, not by a code test).

## Success Criteria

- **SC-001**: The two-home round trip passes against Dataset D3's expected record (export column, then import column), with a new ID and the listed warnings.
- **SC-002**: A one-byte change in any packaged file fails `verify` and upload, naming the file.
- **SC-003**: `format_version` 2 is refused by `inspect`, `verify` and upload.
- **SC-004**: For every Dataset D7 row 1–5, 10–12 and 18–21 (wired reload), the rollback oracle checks 1–7 pass: X's record, home folder (with anything another request saved there before C3), team entry and every edge involving X **in W** are gone, other workspaces keep completed edits (X references there are AD-5, D7 row 21); when C4 is confirmed `GET /api/v1/agents` lacks the new ID after the answer; the only other state for X is the AD-5 residue outside the boundary that the test itself created (D7 rows 10 and 12), and in D7 row 20 the AD-6 home content written after C3, which the final clean-up removes. Manual recovery step 4 then removes the ordinary sessions; a protected main chat remains. For D7 rows 15–16 the answer carries `activation: "pending"` and the neutral restart line.
- **SC-010**: After the manual recovery of each Dataset D15 row, a new upload is admitted.
- **SC-005**: The structured-secret scan finds zero seeded values (none of them present in free text).
- **SC-006**: `inspect`/`verify` leave an empty `OMNIPUS_HOME` empty.
- **SC-007**: The regression table passes, with only `TestNewRootCommand_KeptCommandsPresent` changed.
- **SC-008**: `config.json` is byte-identical after any import, successful or rolled back.
- **SC-009**: When publication is confirmed, an open Team tab shows the imported member within 5 s, without a reload (Playwright row). When it is pending, the CLI and API say so with restart guidance (D7 rows 7, 17).
- Performance figures are targets (Machine-Verifiable Constraints), not success criteria.

---

## Reachability

- **Tool registration**: none, on purpose. No tool-policy file changes.
- **Command line**: `export`, `import`, `package inspect|verify` in `pkg/app/app.go::NewRootCommand`, from the **new** `pkg/app/internal/pkgcmd/command.go`; asserted by `TestNewRootCommand_KeptCommandsPresent`.
- **REST**: three routes in the local-mode block of `pkg/gateway/rest.go`, from the **new** `pkg/gateway/rest_packages.go`.
- **Screen**: no new screen. The existing Team tab and sidebar show the imported agent, and T-7 makes an open Team tab refresh.
- **Imported Main agent**: reachable by chat at once through its own main chat once publication is confirmed (FR-036a); with `activation: pending`, after a gateway restart.
- **Imported native worker**: not a chat target (`IsChatTarget`). Reachable through the existing paths only: a delegation line from a Main agent drawn by the owner in the Team graph (then that Main's delegation directory lists it), or a workspace task assigned to it. Import adds no such line itself (FR-035, FR-036b).
- **Test plan execution**: RED (qa-lead), GREEN (backend-lead for T-1…T-6, T-9, T-10; the T-7 frontend change by frontend-lead, or by backend-lead under the bounded small-feature commission), CHECK (a different qa-lead instance). E2E and UAT rows run on the cut commit; UAT-S1-01 runs before the founder demo.

## User-facing documentation

| TODO | Change | Page | Update |
|---|---|---|---|
| DOC-001 | New commands | `docs/using-omnipus-cli.md` (new section "How to move an agent to another install"; "Limits and things to watch") | Commands, flags, exit codes, preview and confirm, New mode only (the default becomes Replace later), the free-text notice, the suffix rule, the reserved names `export`/`import`/`package`. **"Recover from a failed import"**: the full Manual recovery procedure from "Rollback contract" (where to find the agent ID; record present → Delete agent, or GET for `revision` then DELETE with it; folder only → stop the gateway and delete `agents/<id>/`; then delete the staging folder to free the upload slot), plus what to do after a lost connection (check the agent list before importing again). Also say plainly that an import is visible to other screens and scripts while it runs; that a failed import removes the agent's record, its home folder **with everything inside it, whoever saved it there** (for example a private memory note), its team entry and its delegation lines; that chats and sessions other screens created for the agent stay; that work already running can write into the home folder again after the rollback; and the **final clean-up**: stop the gateway, delete `agents/<id>/` if it is there, start the gateway, then, if the record was already gone, remove the agent from other workspaces with the workspace API (`GET /api/v1/workspaces?status=all`, then `PUT /api/v1/workspaces/<id>` with the revision, without the agent in the team or its edges), then, with other session activity paused, find every session for the agent (`GET /api/v1/sessions?flat=true&agent_id=<id>`, following every `next_cursor`, not stopping on an empty page, collecting the IDs first), delete them, and run a fresh check from the start; if activity could not be paused, the clean-up is unverified. A main chat cannot be deleted and is **not** removed by normal retention: it can remain indefinitely (L16). Explain "activation pending" as **an unconfirmed reload only**: after a successful import, the agent is stored but may not be live yet (restart the gateway or wait and check the agent list); after a rollback, whether anything is left comes from the rollback line (complete or incomplete), not from the pending line. Also reconcile the page's outdated statements (the retired Ava/Admin roster, "a down gateway needs a manual start"), as the grill noted. |
| DOC-002 | Agents can move between installs | `docs/agents.md` (after "How to create your own agent") | What travels and what changes (Field mapping, in plain words: prompt trimmed, connectors and missing skills dropped, retired delegation list not carried); the new ID; the default workspace; that pasted secrets in prompt files travel. An imported Main agent has its main chat; an imported worker is reached by drawing a delegation line to it from a Main agent in the Team graph. **[S1b]** external workers: the program comes from Settings → Third-party agents on the new install; environment overrides and additional arguments are re-entered in Edit → Runtime; a durable import does not mean the worker can run. The same recovery steps as DOC-001. |
| DOC-003 | New REST endpoints | `docs/agents.md` "Without the app" | One sentence pointing to the three endpoints |

## Traceability Matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) | Docs |
|---|---|---|---|---|
| FR-001 | US-1, US-2 | Export a Main agent…; Damaged… (D1 17–19) | TestArchive_ManifestFirstAndUniversalChecks; TestPackagesImport_Route | DOC-001 |
| FR-002 | US-1 | Export a Main agent… | TestPackagesExport_Route | DOC-001 |
| FR-003 | US-2, US-3 | Verify a changed package; Damaged… (D1 3–6, D12 5–6) | TestVerify_Checksums; TestManifest_Relationships | DOC-001 |
| FR-004 | US-2, US-3 | Newer package refused offline; Damaged… (D1 7–9) | TestManifest_VersionRules | DOC-001 |
| FR-005 | US-3 | Damaged… (D1 10) | TestManifest_VersionRules | none: error text only |
| FR-006 | US-3 | [S1b] Unknown element types…; [S1b] A damaged file in an unknown element…; [S1a only] … | TestPackages_S1bExternalAndUnknown; TestPackages_S1aRefusals | DOC-001 |
| FR-007 | US-3 | Damaged… (D2); Backup and Restore unchanged | TestArchive_ManifestFirstAndUniversalChecks; TestBackupRestore_UnchangedAfterMove | none: safety check |
| FR-008 | US-2, US-3, US-4 | Damaged… (D1 11–16, 26); Oversized decoder window…; Upload size through the real route | TestArchive_SizeCaps; TestArchive_DecoderLimits; TestPackagesImport_Route | DOC-001 |
| FR-009 | US-3 | Damaged… (D12) | TestManifest_Relationships | none: error text only |
| FR-010 | US-1, US-4 | Export a Main agent…; Export an agent with no AGENT.md; Export via REST…; [S1a only] External worker export… | TestPackagesExport_Route; TestPackages_S1aRefusals | DOC-001, DOC-003 |
| FR-011 | US-1 | Built-in agents are refused | TestExportSelection_Refusals | DOC-002 |
| FR-012 | US-1 | The exported record follows Field mapping; [S1b] External worker runtime values… | TestFieldMapping_Export | DOC-002 |
| FR-013 | US-1 | Structured secrets…; A secret pasted into the prompt travels | TestPackagesExport_Route | DOC-002 |
| FR-014 | US-1 | [S1b] External worker runtime values… | TestPackages_S1bExternalAndUnknown | DOC-002 |
| FR-015 | US-1 | Export a Main agent… | TestPackagesExport_Route; TestExportSourceRead (D11 7) | DOC-001 |
| FR-016 | US-1 | Export a Main agent… (audit assertion); Export is refused when it cannot be audited | TestPackagesExport_Route; TestPackagesExport_AuditFailure | none: audit only |
| FR-017 | US-1 | Overwrite with --force is atomic | TestCLI_ExportImportE2E; TestPackagesExport_Route | none |
| FR-018 | US-1 | Unsafe or oversized source files… | TestExportSourceRead | DOC-001 |
| FR-019 | US-1 | Export refusals leave no file (D10 7) | TestExportSelection_Refusals | DOC-001 |
| FR-020 | US-3 | Damaged…; Package declares a core agent; [S1a only] External-worker package refused at upload | TestPackagesImport_Route; TestPackages_S1aRefusals | DOC-001 |
| FR-021 | US-3 | Upload gives a provisional preview… | TestPackagesImport_Route | DOC-001 |
| FR-022 | US-4 | Staging that is not usable; Staging races; Manual recovery frees everything | TestStaging_StateMachine; TestPackages_ManualRecovery | DOC-001 |
| FR-023 | US-3 | Upload gives a provisional preview…; Damaged… | TestPackagesImport_Route | none |
| FR-030 | US-3 | Replace and Merge are not available | TestPackagesApply_ModesConcurrency | DOC-001 |
| FR-031 | US-3 | Confirmed import creates…; Two-install round trip | TestRemap_NewIDNeverCollides; TestPackages_TwoHomeRoundTrip | DOC-002 |
| FR-032 | US-3 | Name clash gets a suffix | TestNameSuffix | DOC-001 |
| FR-033 | US-3 | References the target lacks; Confirmed import creates…; [S1b] Executor values… | TestFieldMapping_Import | DOC-002 |
| FR-034 | US-3 | Import and the create paths give the same result…; REST create unchanged… | TestImportCreateParity; TestCreateExtraction_RESTUnchanged; TestPackages_ConfigUntouched | none: internal reuse |
| FR-035 | US-3 | Confirmed import creates…; Import into a named workspace; Workspace selection problems; Agent tools unchanged…; A native worker is imported… | TestPackagesApply_NewMode; TestCreateAgentTool_JoinUnchanged; TestPackagesApply_NativeWorkerReachable | DOC-002 |
| FR-036 | US-3 | Confirmed import creates…; Failures after the commit point… (pending publication, D7 rows 7, 17); Team tab refreshes while open | TestPackagesApply_NewMode; TestPackagesApply_PostCommitWarnings | DOC-001 |
| FR-036a | US-3 | The main chat is created for the imported Main only; Failures after the commit point… | TestPackagesApply_MainChatScope; TestPackagesApply_PostCommitWarnings | DOC-002 |
| FR-036b | US-3 | A native worker is imported and reached by delegation | TestPackagesApply_NativeWorkerReachable | DOC-002 |
| FR-037 | US-3 | Failure before the commit point…; A competing workspace edit survives…; Another reader sees the import…; Sessions and messages created during the import are not rolled back…; Content in the agent's home folder follows the folder…; A failed membership removal stops the rollback…; Incomplete rollback…; The rollback reload is not confirmed | TestJournal_CompensationOrder; TestPackagesApply_Rollback | DOC-001 |
| FR-037a | US-3 | Manual recovery frees everything (D15 rows 1–10, including C1 failure, partial C1, recreated folder and paged session residue); A failed membership removal stops the rollback… | TestPackages_ManualRecovery | DOC-001 |
| FR-038 | US-4 | One apply at a time | TestPackagesApply_ModesConcurrency | none |
| FR-039 | US-4 | Staging that is not usable; Workspace selection problems | TestStaging_StateMachine | none |
| FR-040 | US-3 | Failure before the commit point… (audit); Failures after the commit point… (audit row); Damaged… (audit) | TestPackagesApply_Rollback; TestPackagesApply_PostCommitWarnings; TestPackagesImport_Route | none: audit only |
| FR-041 | US-3 | Confirmed import creates…; Changes between preview and apply… | TestPackagesApply_NewMode | DOC-001 |
| FR-042 | US-3 | Changes between preview and apply…; Workspace selection problems | TestPackagesApply_NewMode | DOC-001 |
| FR-043 | US-3 | [S1b] External worker without a program… | TestPackages_S1bExternalAndUnknown | DOC-002 |
| FR-044 | US-3 | Staging races (D13 5); Manual recovery frees everything (D15 3) | TestStaging_StateMachine; TestPackages_ManualRecovery | DOC-001 |
| FR-047 | US-3 | Import and the create paths give the same result…; REST create unchanged…; [S1b] External-worker fields the runtime does not support | TestImportCreateParity; TestCreateExtraction_RESTUnchanged; TestPackages_S1bExternalAndUnknown | DOC-002 |
| FR-048 | US-3 | Every editable field has an import decision (control and alias names included); Agent tools unchanged… | TestImportFieldCoverage | none: internal invariant |
| FR-045 | US-4 | Access rules…; Upload size through the real route (counting body; declared-length and chunked limit + 1) | TestPackages_AccessRules; TestPackagesImport_Route | DOC-003 |
| FR-046 | US-4 | CLI token works without a password prompt | TestPackages_AccessRules | DOC-001 |
| FR-050 | US-1 | CLI usage and lookup errors; Export refusals…; Overwrite with --force… | TestPkgCmd_UsageLookupsExitCodes; TestCLI_ExportImportE2E | DOC-001 |
| FR-051 | US-3 | Interactive confirm; Declined…; Lost connection…; CLI usage and lookup errors (D10 11–14) | TestPkgCmd_PromptAndLostConnection; TestPkgCmd_UsageLookupsExitCodes | DOC-001 |
| FR-052 | US-2 | Inspect reads only the manifest; Verify…; Newer package…; Oversized decoder window… | TestInspect_ManifestOnly; TestVerify_Checksums | DOC-001 |
| FR-053 | US-1–3 | CLI usage and lookup errors | TestPkgCmd_UsageLookupsExitCodes | DOC-001 |
| FR-054 | US-4 | Gateway not running; CLI token works… | TestCLI_ExportImportE2E | DOC-001 |
| FR-055 | US-4 | Gateway not running | TestCLI_ExportImportE2E | none |
| FR-056 | US-3 | Team tab refreshes while open | TestAgentCreatedInvalidatesTeamViews; team-tab-import.spec.ts | none: the screen now behaves as users expect |
| FR-060 | US-4 | Export via REST…; Confirmed import creates… | CI `verify-contracts`; generated types used in TestPackagesExport_Route | DOC-003 |
| FR-061 | all | **Exception: no BDD scenario** | **Exception: docs-verifier audit, not a code test** | DOC-001–003 |

**Completeness check**: every FR has a scenario and a test, except **FR-061**, which is checked by the docs-verifier audit by design. Every scenario appears at least once. A test name in a row is a commitment for RED to assert that behaviour; it is not proof of coverage until CHECK confirms the assertions.

### Traceability to ADR decisions

| ADR decision | S1 | Requirements |
|---|---|---|
| D1 engine, REST, offline inspect/verify, local mode | As decided | FR-010, FR-020, FR-030, FR-045, FR-052, FR-054, FR-055, FR-060 |
| D2 format, manifest, versions, SHA-256 | As decided, plus relationship rules | FR-001–FR-009 |
| D3 libraries, reused checks, caps | Predicates moved (restore's skip policy kept); caps and decoder limits set | FR-007, FR-008 |
| D4 stage → preview → add, journal rollback, one import at a time | **Narrowed (amended in ADR.md A2, A7):** rollback covers in-process failures before the commit point and resyncs the live list by reload; no isolation from other readers (L16); start-up recovery stays in S6; manual recovery documented; logical-state oracle with allowed differences; rollback boundary defined by places and records (record, home folder with everything inside it, team entry, edges involving X in the import workspace); outside it other requests' sessions and chats stay (AD-5); a running writer can recreate home content (AD-6), removed by the final clean-up | FR-020–FR-023, FR-034, FR-036–FR-040, FR-037a |
| D5 new IDs and remapping | **Amended:** `allow_agents` is not remapped but removed (retired by ADR-037); connector bindings dropped until S3; create rules applied (Field mapping) | FR-031, FR-033 |
| D6 secrets | Structured sources excluded; **amended:** `cli_args` and `cli_path` also stripped; free-text redaction deferred, stated plainly | FR-012–FR-014, FR-018 |
| D7 modes | New only | FR-030, FR-032 |
| D8, D9 | Deferred (S10, S7) | — |
| R1 create rules in the handler | Shared create function and Field mapping | FR-034 |
| R2 delete side effects | A narrow compensation list instead of the broad cascade | FR-037 |
| R3 CLI and password gate | DA-1 | FR-046 |
| R4 large exports | Caps and decoder limits | FR-008 |
| R5 free-text secrets | Notice and docs | FR-013, FR-050, DOC-002 |

---

## Known Limits (S1)

| # | Limit | Why | Fixed in |
|---|---|---|---|
| L1 | One custom agent per package; no built-in agents | Scope; Q1 | S7, S6 |
| L2 | Skills, connectors, memory, workspaces and chats do not travel; connector assignments always dropped | Later slices | S2–S5, S9 |
| L3 | New mode only; the CLI default changes to Replace in S6 | Q2 | S6 |
| L4 | **No automatic recovery when the gateway stops during an apply.** The record itself is always whole, but team membership may be missing. The journal is kept and the next upload warns about it; the owner follows the Manual recovery procedure, which also frees the upload slot | Start-up recovery is S6 | S6 |
| L5 | A staging is lost on restart (upload again) | In-memory registry | S6 if needed |
| L6 | Prompt files are copied as they are; pasted secrets travel | Free text cannot be proven secret-free | Open |
| L7 | **[S1b]** External workers arrive without environment overrides and additional arguments (re-enter them in Edit → Runtime). The program comes from the new install's Settings → Third-party agents. A durable import does not mean the worker can run | Host-specific, possibly secret | By design |
| L8 | No signing | Proportionate | — |
| L9 | Agents named `export`, `import` or `package` cannot be run by name from the CLI; no reserved-verb check at create time | Subcommands resolve first | Follow-up issue (chief) |
| L10 | Local mode only | Same scope as Backup & Restore | Later if wanted |
| L11 | No new screen | S8 | S8 |
| L12 | Caps sized for agent-only packages | Proportionate | S5, S9 |
| L13 | The prompt is stored trimmed; legacy `model.fallbacks`, `subagents.allow_agents` and `subagents.model` are not carried; non-canonical colours, figures or roles in a hand-made package are refused | Import follows the create rules (DA-4) | By design |
| L14 | After a rollback, harmless markers may differ (AD-1…AD-4); AD-5 (residue outside the boundary) and AD-6 (home content recreated after C3) are not markers but other requests' effects (L16) | Real side effects of the store | By design |
| L15 | **[S1a until S1b lands]** External workers cannot be exported or imported; packages with unknown element types are refused | Split | S1b |
| L16 | **An import is not isolated while it runs, and rollback is defined by the places and records it removes, not by who wrote them.** For a short window, another reload can list the new agent, and other requests can then use it: create ordinary sessions, send chat messages (saved to transcripts), start turns that run to completion (including tool calls such as saving a private memory note), or prepare its main chat. Rollback removes X's record, X's home folder with **everything inside it** (including content those requests saved there), X's team entry and every delegation edge involving X **in the import's workspace**, and requests a reload. It does **not** remove what those requests wrote outside that boundary (AD-5: sessions, chats, main chat, membership or edges for X in other workspaces, task or audit entries). A turn already running is not stopped by the reload and can recreate home content after C3 (AD-6); the final clean-up (stop the gateway, delete the folder, start, repair other workspaces, delete the sessions with a stable list and a verification scan) removes it. A protected main chat cannot be deleted and is not removed by retention: its identity and newest content can remain **indefinitely** in S1. Founder acceptance of this narrower promise is pending | No isolation machinery in S1 (DA-5, team-lead rulings r3 and r4) | S6 (journal-based clean-up) |
| L17 | **REST create does not store `voice` today** (the field is accepted and dropped). S1 keeps that behaviour; import stores `voice` the way the `create_agent` tool does. So an agent made through REST create and then exported carries an empty voice | Existing behaviour, outside S1; changing it is a REST behaviour change | Separate issue (chief to file); not a rider |
| L18 | A native worker is not a chat partner after import: the owner draws a delegation line to it, or assigns it a workspace task | Existing product model | By design |

## Assumptions

- **[ASSUMED-Q1 = A]**, **[ASSUMED-Q3 = A]**, **[ASSUMED-Q5 = A]**; DA-1…DA-5.
- The default workspace normally exists but is **not** guaranteed (boot logs a failure and continues), hence `default_workspace_missing`.
- The target build must ship S1; older builds lack the endpoints.
- No template section was removed. The UI sections say "no new screen" and describe the T-7 change.

## Clarifications

### 2026-11-19 (revision 1)
- New-mode password re-entry → none (now DA-1).
- Where the agent lives → the default workspace or the named one (FR-035).
- Same-named connectors → never bound (FR-033).
- `cli_path`/`cli_args` → stripped (FR-012).
- Caps → FR-008.

### 2026-11-19 (revision 2, team-lead rulings and grill r1)
- DA-1…DA-4 recorded (team lead).
- Import follows the create rules; Field mapping is the oracle (B-01).
- Rollback has a commit point, a compensation list, a logical-state oracle and allowed differences (B-02).
- Automatic crash recovery stays in S6 (team-lead ruling 4, matching the grill's recommended option A). The chief may want to tell the founder that the published guarantee is narrower (see the open question in the report).

### 2026-11-19 (revision 3, team-lead direction and grill r2)
- Keep promises small; move what the code cannot keep into Known Limits (DA-5).
- Publication and rollback resync use the existing full reload; the store notifier is not relied on.
- No isolation of a running import (L16); manual recovery is complete and in S1a.
- `voice`, `memory_enabled`, `context_window_override` follow the `create_agent` tool; REST create unchanged (L17).
- Only Main agents are chat-ready after import; native workers are reached by delegation (L18).
- S1a refuses external-worker packages on upload.
- **Not founder-approved yet:** the narrower public guarantee (in-process rollback only, no isolation). The team lead ruled on it; founder acceptance is for the chief to confirm.

### 2026-11-19 (revision 4, team-lead ruling on grill r3)
- (Superseded in revision 5 by the place-and-record boundary below.) Rollback cleans up only what the import owns: the agent record, its home folder and its team membership. It does not undo what other requests created while the agent was visible: ordinary sessions (empty or with messages), accepted chat messages, turns already running, a main chat (AD-5, L16). Manual recovery step 4 removes the ordinary sessions; a main chat stays until normal retention.
- `agent_created` and the main chat only after the publishing reload is confirmed; an unconfirmed reload reports `activation: "pending"` with restart guidance (FR-036).
- A failed membership removal (C1) stops the rollback before the record and folder are deleted; a partial record delete (C2) still continues to the folder (FR-037).
- Upload is copied to staging in full before decoding; the size limit holds with and without a declared length (FR-045).
- Control and alias names are mapped in one table and enforced by the field-coverage test (FR-048).
- S1b keeps the per-runtime check (`agentmutation.ValidateFields`) before `applySharedConfigArgs` (FR-047).
- **Still not founder-approved (Q1 open):** the narrower guarantee now explicitly includes that residue from other requests stays after a rollback. Founder acceptance is for the chief to obtain.

### 2026-11-19 (revision 5, team-lead ruling on grill r4)
- Rollback is defined by the **places and records** it removes, not by who wrote them: the record, the home folder with everything inside it (including content other requests saved there, such as a private memory note), the team entry and every edge involving X (in the import workspace; made explicit in revision 6). Shared-store chats and ordinary sessions outside that boundary stay (AD-5).
- A writer already admitted for X can recreate home content after C3 (AD-6); the final clean-up (gateway stopped, folder deleted, sessions deleted) removes it.
- A protected main chat is not removed by retention: in S1 it can remain indefinitely.
- Session recovery enumerates every page (`flat=true`, every `next_cursor`, IDs collected first).
- The delegation-save fault case persists a removable edge first (partial C1), and D15 recovers that shape.
- **Still not founder-approved (Q1 open):** the acceptance request must present the boundary above, the possible recreation by running work, and the indefinite main-chat residue. The chief owns it.

### 2026-11-19 (revision 6, final, grill r5)
- The C1 boundary is the import workspace W only; X references other requests added in other workspaces are AD-5 and are repaired by Manual recovery step 5 with the existing workspace update (revision, `core_team`, `delegation`).
- Session clean-up is complete only under a stable session list with a fresh verification scan; otherwise it is reported unverified.
- The after-C3 home oracle accepts exactly the real writer's note plus its identified derived index artifacts; the test keeps the writer from before C3 and closes it itself.
- **Still not founder-approved (Q1 open):** the acceptance request must also say that references in other workspaces and sessions need the manual steps, and that the clean-up is not an all-data guarantee.

---

## Grill r1 resolution

| Finding | Fixed / declined | What changed | Section(s) |
|---|---|---|---|
| B-01 Lossless import versus create rules | **Fixed** (DA-4) | Import follows the create rules; a field-by-field table with import, REST create and REST update columns; prompt trimmed, `AGENT.md` byte-identical; blank native prompt and worker without a description refused at export and upload; `memory_enabled` and `context_window_override` applied with the update rule; legacy and retired fields not carried; parity test; D3 rewritten with non-default and whitespace cases and all three kinds; `create_agent`/update regression rows | Field mapping; Dataset D3; FR-012, FR-015, FR-019, FR-033, FR-034; TestImportCreateParity; Regression table; header "Agent tool field table" |
| B-02 Unattainable rollback | **Fixed** (DA-4) | Commit point; effect inventory E1–E10 with compensations; `config.json` never written (coverage check split from the config writer); explicit `AGENT.md`/folder removal; membership removed with a fresh re-read (concurrent edits kept); lock files never deleted; skill stamp allowed; logical-state oracle with four allowed differences; real-store faults including partial persistence; `GET /api/v1/agents` checked; broad cascade replaced by narrow compensations | Rollback contract; Dataset D7; FR-034, FR-037; SC-004, SC-008; Impact Assessment |
| S-01 1 MiB wrapper on upload | **Fixed** | Upload route uses a 64 MiB limit with authentication and `RequireNotBypass`; JSON routes keep 1 MiB; tests on registered routes with a valid package over 1 MiB | FR-045; D1 rows 11, 12, 25; D8 row 7; BDD "Upload size through the real route" |
| S-02 Archive interpretation | **Fixed** | Universal checks versus agent layout checks; unknown elements get universal checks plus checksums only; cardinality, ownership, ID and name agreement, `counts` validated; zero-element case; inspect reads the manifest only and makes no integrity claim | FR-003, FR-006, FR-007, FR-009, FR-052; Datasets D2, D12; BDD "Inspect reads only the manifest", "[S1b] A damaged file in an unknown element…" |
| S-03 Staging races | **Fixed** | Staging lifecycle states; slot reserved before decoding; the clock starts at `ready`; `applying`/`needs-cleanup` pinned; transitions under one lock; race tests | Staging lifecycle; FR-022, FR-039; Datasets D9, D13 |
| S-04 Restart versus transaction promise | **Fixed** (team-lead ruling 4 = the grill's option A) | Guarantee narrowed to in-process failures before the commit point; journal kept; unfinished-journal warning **[S1b]**; lost-connection CLI text; recovery in DOC-001/002; ADR D4 and SLICES amended | Overview; Rollback contract; L4; FR-044, FR-051; DOC-001, DOC-002; ADR.md amendments |
| S-05 Preview freshness | **Fixed** | Preview provisional; apply re-checks; additive differences proceed and are listed as `changed_since_preview`; refusals before writing; missing default workspace handled | Preview freshness; FR-021, FR-042, FR-035; Dataset D14 |
| S-06 Decoder allocation | **Fixed** | Max window 8 MiB, max memory 256 MiB, concurrency 1 for upload, verify and inspect; writer window ≤ 8 MiB; the whole decoded stream counted; an oversized-window test separate from the bomb | FR-008; D1 rows 13, 26; BDD "Oversized decoder window refused offline"; TestArchive_DecoderLimits |
| S-07 Export source reads | **Fixed** | Bounded, link-refusing, regular-file reads; unreadable ≠ absent; hashes from the captured bytes; refuses what import would refuse; output atomic | FR-018, FR-019, FR-017; Dataset D11; BDD "Unsafe or oversized source files…" |
| S-08 Open Team view refresh | **Fixed** | T-7: `agent_created` also invalidates workspace detail and delegation; the frame is sent after membership and publish; a Playwright row with the Team tab open; UAT-S1-01 opens Team first | UI Screens and States; FR-036, FR-056; SC-009; UAT-S1-01 |
| S-09.1 Expiry boundary | **Fixed** | 29:59 accepted, exactly 30:00 expired | Dataset D9 |
| S-09.2 Status for `max_tool_iterations` | **Fixed** | Deliberately 422 `element_not_creatable` with `field` (package content, not request shape), stated | Machine-Verifiable Constraints; D5 row 11 |
| S-09.3 Round-trip oracle | **Fixed** | Expected = export column then import column; new ID and warnings asserted | SC-001; BDD "Two-install round trip"; D3 |
| S-09.4 CLI coverage | **Fixed** | Unknown, ambiguous, existing output, blank prompt, workspace ambiguity and missing default added, with output-path state asserted | Dataset D10; D14 row 5 |
| S-09.5 Audit | **Fixed** | Exactly-one audit assertions on export, apply success and rollback, and safety refusals; audit failure after commit is a warning | FR-016, FR-040; D7 row 9; BDD steps |
| S-09.6 Performance | **Fixed** | Relabelled as targets with a named benchmark and protocol; not a gate | Machine-Verifiable Constraints; SC list |
| S-09.7 Literal completeness | **Fixed** | FR-061 named as the exception; a test name is a commitment, not proof | Traceability Matrix note |
| S-10 Absolute secret wording | **Fixed** | Promise limited to structured sources and listed fields; the fixture excludes values in free text; a separate pasted-secret scenario shows the limit; redaction deferral recorded in the ADR trace | Security and User Promises; FR-013; SC-005; BDD "A secret pasted into the prompt travels"; ADR trace D6 |
| S-11 Wrong recovery setting | **Fixed** | Program → Settings → Third-party agents; environment overrides and additional arguments → Edit → Runtime; existing "cannot run" text surfaced; an import with no program configured | FR-043; L7; DOC-002; BDD "[S1b] External worker without a program…" |
| P-01 `replace`/`merge` in the enum | **Declined** | Kept on purpose: S6 then adds behaviour without changing the generated Go/TypeScript types or the CLI `--mode` flag values, and S1 already refuses them with a clear code | Contract Changes |
| P-02 "Matching Backup & Restore" | **Fixed** | Clarified: the same local-mode scope, stricter middleware | Security and User Promises |
| P-03 Cite by title | **Fixed** | CLI Minimization, Built-in agent configuration…, Remove the Global Per-Agent Delegation Policy, and the Vault records specification are cited by title | Throughout; ADR.md amendments |
| Grill confirmation: password | **Resolved by team-lead ruling DA-1** | No separate sign-off for S1 | DA-1 |
| Grill confirmation: join extraction | **Resolved by team-lead ruling DA-2** | Task T-4; re-estimate; S1a/S1b split | Size and split; SLICES.md |
| Grill confirmation: ADR-024 | **Resolved by team-lead ruling DA-3** | Amendment text added | ADR.md |
| Grill note: rider confirmation | **Open, owned by the chief** | Riders marked as candidates | Header |
| Grill note: outdated CLI doc statements | **Fixed in scope** | Added to DOC-001 | DOC-001 |

(Revision 3 superseded two entries above: the `unfinished_import_found` warning moved from S1b to S1a, and "Field mapping" now has agent-tool columns. See below.)

---

## Grill r2 resolution

Team-lead direction for this round: keep promises small and testable; where the code cannot keep a promise, state the smaller guarantee and move the rest to Known Limits with the slice that fixes it, rather than adding machinery (DA-5).

| Finding | Fixed / declined | What changed | Section(s) |
|---|---|---|---|
| R2-B-01 Field preservation (`voice`), unchanged REST create and parity conflict | **Fixed (smaller guarantee)** | REST create stays as it is (it still drops `voice`; pinned by a test and recorded as L17, separate issue, not a rider). Import stores `voice`, `memory_enabled` and `context_window_override` with the `create_agent` tool's existing rule (`applySharedConfigArgs`, exported unchanged). Parity split into two checks on the normalised input: import versus REST create for fields REST create stores (the three import transformations and the three tool-rule fields excluded), and import versus `create_agent` for the three fields. Non-canonical figure/role/colour are **refused** like REST create, not normalised (D3 row 7, D1 row 29) | Field mapping; Parity rule; D1 rows 28–29; D3 rows 4, 7; FR-047; BDD "Import and the create paths give the same result…", "REST create unchanged…"; L13, L17; Regression table |
| R2-B-02 Rollback assumes an unwired notifier, no publication or isolation boundary | **Fixed (smaller guarantee)** | The notifier assumption is removed (it is not wired in production). Publication is the existing full reload (`triggerReloadAndWaitOutcome`, as `deleteAgent` uses) after commit; rollback ends with the same reload (C4), so the live list matches the disk. Compensation order C1 membership → C2 record → C3 folder. **No isolation is promised**: another reader may see the agent mid-import and start its main chat; that stray empty chat is allowed difference AD-5 and Known Limit L16, fixed in S6. No new lock, filter or transaction (DA-5). A real-store, real-reload case forces a reload after E4 and a workspace read after E8 | Overview; Existing Codebase Context; Rollback contract; D7 row 10; FR-036, FR-037; BDD "Another reader sees the import…"; L16; ADR A7 |
| R2-S-01 Manual recovery incomplete; no slot release | **Fixed** | Manual recovery for all three leftover shapes (record present, folder only, nothing), with the `revision` needed by DELETE; retirement = delete the staging folder, which the next upload notices and which frees the slot without a restart; `unfinished_import_found` moved into S1a with agent ID and staging folder; error answers carry agent and staging IDs | Rollback contract "Manual recovery"; Staging lifecycle; D13 rows 5–6; D15; FR-022, FR-037a, FR-044; US-3 AS-14; DOC-001; SC-010; size table (T-5) |
| R2-S-02 S1a must refuse external-worker packages on upload | **Fixed** | 422 `external_worker_not_supported_yet` at upload before `ready`, with and without runtime values; same code at export; becomes S1b acceptance when T-9 lands | Machine-Verifiable Constraints; D1 rows 30–31; FR-020; US-3 AS-13; BDD "[S1a only] External-worker package refused at upload"; TestPackages_S1aRefusals; L15 |
| R2-S-03 Two test setups contradict production rules | **Fixed** | Size: a genuinely valid ~2 MiB package (1 MiB random `SOUL.md` + 1 MiB random `AGENT.md`) → 201; the 64 MiB transport boundary tested with exactly 64 MiB of random bytes → 422 (not 413); 64 MiB + 1 → 413; no test-only layout. Concurrency: the self-row fails, the join helper releases the lock, the competing writer finishes, then compensation runs; the lock is never bypassed | US-4 AS-6; BDD "Upload size through the real route", "A competing workspace edit survives…"; D1 rows 11, 25; D7 row 5; Integration Boundaries; TestPackagesImport_Route, TestPackagesApply_Rollback |
| R2-S-04 Upload no-write oracle versus audit and staging maintenance; audit failure outcomes | **Fixed** | FR-023 now promises no agent, workspace, skill, connector or configuration change, and allows its own staging, staging maintenance and the audit log; snapshot oracles use those exceptions. Audit failure: export refused (500 `audit_failed`, no bytes); upload refusal and rollback keep their true answer (gateway log); post-commit is a warning | FR-016, FR-023, FR-040; Security and User Promises; error table; BDD "Upload gives a provisional preview…", "Damaged…", "Export is refused when it cannot be audited"; D7 row 11 |
| R2-S-05 Main-chat helper provisions every member | **Fixed** | `ensureMainSession` for the imported Main and its workspace only, when `mainSessionPairEligible`; `ensureWorkspaceMains` not used; other members' chats untouched and their errors not reported | Existing Codebase Context; Rollback contract P3; FR-036a; US-3 AS-12; BDD "The main chat is created for the imported Main only"; TestPackagesApply_MainChatScope |
| R2-S-06 Native workers cannot be chatted with | **Fixed** | Immediate chat promised for Main agents only. A native worker gets no main chat and no delegation line; the result says how to reach it; acceptance: after the owner draws a line from a Main, that Main's delegation directory lists it. UAT row added | Overview; US-3 AS-11; User Journey; Reachability; FR-036b; BDD "A native worker is imported and reached by delegation"; UAT-S1-07; L18; DOC-002 |
| R2-S-07 Field table and parity test do not cover agent tools | **Fixed** | `create_agent` and `update_agent` columns (and "perm" for `set_agent_permissions`), nested rows for fallback effort and the two model parameters; new `TestImportFieldCoverage` fails when any tool or REST field name lacks a row; existing tool parity tests kept as regression; no tool schema changes | Header; Field mapping; FR-048; BDD "Every editable field has an import decision", "Agent tools unchanged…"; Regression table |
| Polish: S1a total 4,530, not 4,050 | **Fixed** | Arithmetic corrected; with the round-2 work S1a is now ≈ 4,930 | Size and split; SLICES.md |
| Polish: S1b is size M | **Fixed** | S1b ≈ 430 lines (warning moved to S1a), size M | Size and split; SLICES.md |
| §4 warning: founder acceptance of the narrower guarantee not evidenced | **Open, owned by the chief** | Recorded as not founder-approved in Clarifications | Clarifications (revision 3) |
| §4 warning: riders (#1440, #1295) unconfirmed | **Open, owned by the chief** | Unchanged: #1440 closes only on a passing create-then-GET; #1295 for the chief's rider decision | Header |
| §4 note: carry the ADR-024 amendment into the canonical ADR when promoted | **Accepted** | Noted in ADR.md A1 | ADR.md |

## Grill r3 resolution

Keyed to the numbering in `GRILL-S1-r3.md` (on disk), plus the points the chief relayed from the earlier, overwritten round-3 report. Team-lead ruling on R3-B-01: DA-5 stays, no isolation subsystem; take the grill's smallest correction.

| Finding | Outcome | What changed | Where |
|---|---|---|---|
| R3-B-01 No isolation permits populated sessions, not merely an empty main | **Fixed (smaller guarantee, founder acceptance pending)** | Rollback promises clean-up of the import-owned record, home folder, team membership and delegation row only. Everything other requests created while X was visible stays and is named: ordinary sessions (empty or populated), accepted chat messages in transcripts, turns already running, a main chat. "One harmless empty main chat" is gone. Oracle scoped to import-owned effects (checks 1–7). Manual recovery step 4 deletes ordinary sessions with `DELETE /api/v1/sessions/<id>`; a protected main chat stays until normal retention. The answer's `details.note` says so. Synchronised case: an ordinary session via `POST /api/v1/sessions` plus one saved WebSocket message, created after the provisional reload and before C1; both remain; after confirmed C4 a new message to X is refused | Overview; Existing Codebase Context; Rollback contract (What rollback covers, AD-5, oracle); BDD "Sessions and messages created during the import are not rolled back"; D7 rows 10, 12; D15 row 7; FR-037; SC-004; L14, L16; DOC-001; Ambiguity Warning 10; Clarifications (revision 4); ADR.md A7; SLICES.md |
| R3-S-01 Pending reload outcomes contradict unconditional visibility promises | **Fixed** | Publication (P1) and rollback reload (C4) each report confirmed, error or timeout. `agent_created`, the main chat and "chat at once" only after confirmed P1; otherwise `activation: "pending"` with restart guidance, and the data is reported truthfully as stored. The rollback oracle's live-list part applies only when C4 is confirmed; failed and timed-out C4 cases added. Tests use a wired reload (`ErrReloadNotConfigured` would count as confirmed) | Rollback contract P1/C4; US-3 AS-2, AS-10; BDD "The rollback reload is not confirmed", post-commit outline, Team tab; D7 rows 7, 15–17; FR-036, FR-036a; SC-004, SC-009; Reachability; Error table; CLI rows |
| R3-S-02 C1 failure in the compensation order | **Fixed** | A failed C1 (membership removal or delegation save) stops C2 and C3, keeps the record as the recovery handle, returns `import_rollback_incomplete` with `failed_step`, and still attempts C4. A partial C2 continues to C3. Leftover shapes listed; Manual recovery covers the C1 shape (D15 row 5). Test #14 states the explicit C1 → C2 → C3 → C4 sequence and branches (this also resolves the polish item) | Rollback contract (compensation failure rules, leftover shapes); BDD "A failed membership removal stops the rollback…"; D7 rows 13–14; D15 row 5; Manual recovery step 2; FR-037, FR-037a; tests #14, #21d, #26 |
| R3-S-03 Reader test repeats the join-lock ordering trap | **Fixed** | D7 row 10: E4, unrelated reload confirmed, self-row fails, join lock released, C1 held, a real workspace-detail request completes (response received), then C1 released; the recorded order is asserted. The production lock is not bypassed. The separate populated-session case is D7 row 12 | BDD "Another reader sees the import while it runs"; D7 rows 10, 12; test #26 |
| R3-S-04 Boundary response does not prove full upload consumption | **Fixed** | The handler copies the whole body to staging before decoding. Exactly 64 MiB through a counting body → 422 and all 67,108,864 bytes read; 64 MiB + 1 → 413 `package_too_large` with a declared length and with chunked transfer, through the registered route. The valid ~2 MiB package stays the positive control | BDD "Upload size through the real route"; US-4 AS-6; D1 rows 11, 12, 32; FR-045; test #24 |
| R3-S-05 Field-coverage mappings and exclusions | **Fixed** | New "Control and alias names" table: `revision` and `env_overrides_remove` not transported; `workspace_id` chosen at apply; `id` selector; `soul` → `SOUL.md`; `agent_md` → `AGENT.md`; `agent_type`/`type` → `type`; `tools_cfg`, `tool_policy_changes`, `mcp_servers` → tool rows; `executor`/`cli` → executor rows; `model`/`provider` → model rows. The coverage test resolves aliases and nested leaves, has no unlisted ignore set, and reuses `agentmutation` `MappingRows`/`ExcludedRows`. No tool schema changes | Field mapping (Control and alias names; Field-coverage rule); FR-048; test #21a |
| Earlier report: S1b external workers need the per-runtime check | **Fixed** | Import runs `agentmutation.ValidateFields` before `applySharedConfigArgs`, as `create_agent` does, so `memory_enabled` and `context_window_override` on an external worker are refused (422 `element_not_creatable` with `field`) | Field mapping rows; BDD "[S1b] External-worker fields the runtime does not support"; D1 rows 33–34; FR-047; test #32; ADR.md A8; SLICES.md S1b |
| Earlier report: control and alias fields | **Fixed** | Same as R3-S-05 | as above |
| Earlier report: session residue ruling | **Fixed** | Same as R3-B-01 | as above |
| Polish: "never-published has no history" in Existing Codebase Context | **Fixed** | Removed; the `RunAgentDeleteCascade` row and the new sessions row describe the real behaviour | Existing Codebase Context |
| Polish: test row 14 "Reverse order" | **Fixed** | Explicit C1 → C2 → C3 → C4 with failure branches | Test #14 |
| Polish: L14 listed AD-5 as a harmless marker | **Fixed** | L14 now AD-1…AD-4; AD-5 points to L16 | Known Limits |
| §5 warning: founder acceptance of the narrower guarantee | **Open, owned by the chief** | Recorded as not founder-approved in Clarifications (revision 4), now including the residue from other requests | Clarifications |
| §5 warning: riders (#1440, #1295) | **Open, owned by the chief** | Unchanged | Header |
| Size | **Updated** | S1a ≈ 5,210 (≈2,560 production and docs + ≈2,650 tests); S1b ≈ 480 (M). Estimates, not measurements | Size and split; SLICES.md |

## Grill r4 resolution

Keyed to `GRILL-S1-r4.md`. Team-lead ruling on R4-B-01: take the grill's smallest correction; DA-5 stays (no isolation, no new machinery).

| Finding | Outcome | What changed | Where |
|---|---|---|---|
| R4-B-01 Other requests' outputs cannot all survive recursive home deletion | **Fixed (smaller guarantee, founder acceptance pending)** | Rollback is now defined by the **places and records** it removes, not by who wrote them: X's record, X's home folder with everything inside it (including content other requests saved there, such as a private memory note), X's team entry and every delegation edge involving X. Outside that boundary, shared-store chats, ordinary sessions, main chats and other records stay (AD-5). A writer already admitted for X can recreate home content after C3 (new AD-6); the final clean-up (stop the gateway, delete the folder, start, delete the sessions) removes it. Oracle check 5 now requires the home to be absent including other requests' content, with the single AD-6 exception for the after-C3 case; check 3 drops edges involving X. Two synchronised cases use the real private-memory writer (`RememberTool.Execute`, room `private`), with success asserted, before C3 and after C3. Every sentence stating the promise was aligned | Overview; Existing Codebase Context; Rollback contract (E6, E9, C1, C3, leftover shapes, What rollback covers, AD-5, AD-6, oracle checks 3, 5, 6, D7 rows 19–20); Manual recovery (final clean-up); US-3 AS-5; CLI row; Behavioral contract; Error table; Ambiguity Warning 10; BDD "Content in the agent's home folder follows the folder…"; D7 rows 19–20; D15 row 10; FR-037; SC-004; L14; L16; DOC-001; ADR trace D4; Clarifications (revision 5); ADR.md A7; SLICES.md |
| R4-S-01 Main chats do not disappear through normal retention | **Fixed** | Every "stays until normal retention" is gone. Text now says a protected main chat cannot be deleted in S1 and is not removed by retention: its identity and newest content can remain indefinitely; only older rolled content expires. The pre-existing 409 message in `deleteSession` that says "kept until normal retention" is noted for the chief, not changed in S1 | Manual recovery step 4; What rollback covers; AD-5; L16; DOC-001; Existing Codebase Context; Clarifications; ADR.md A7 |
| R4-S-02 Manual recovery does not enumerate all ordinary sessions | **Fixed** | Step 4 uses `GET /api/v1/sessions?flat=true&agent_id=<id>`, follows every `next_cursor` as `offset`, does not stop on a page without X rows (the agent filter runs after paging), collects IDs before deleting, and reports a failed or degraded request as an incomplete clean-up. New D15 row 9: X's session beyond the first 50 rows plus a child session, recovered through the documented REST steps (not store enumeration) | Manual recovery step 4; FR-037a; D15 rows 7, 9; test #21d; DOC-001 |
| R4-S-03 Delegation-save fault lacks an edge C1 can remove | **Fixed** | D7 row 14 now persists an edge A → X through a separate valid graph edit after the failed join releases W's lock and while C1 is held, then fails C1's own delegation save. The test asserts the save fault was reached, the membership was already removed, the edge is still stored, C2/C3 were skipped, the record is kept and C4 was attempted. New leftover shape (a2) and D15 row 8 recover it with Delete agent (which cleans edges in every workspace) | Rollback contract (C1, shape a2, D7 row 14 bullet); BDD "A failed membership removal stops the rollback…"; D7 row 14; D15 rows 5, 8; Manual recovery step 1; tests #14, #26 |
| Polish: C4 line said "Nothing was imported" even after a C1 failure | **Fixed** | C4 line is neutral and follows the outcome line (complete or incomplete); combined C1-failure + C4-pending message asserted | Rollback contract C4; CLI row; BDD "The rollback reload is not confirmed" |
| Polish: DOC-001 described pending as "the agent is stored" for rollbacks too | **Fixed** | Pending explained as an unconfirmed reload only; what is left after a rollback comes from the rollback line | DOC-001 |
| §5 founder acceptance of the narrower guarantee | **Open, owned by the chief** | Clarifications (revision 5) lists what the acceptance request must present: the place-and-record boundary, recreation by running work, the indefinite main-chat residue | Clarifications |
| §5 riders (#1440, #1295) | **Open, owned by the chief** | Unchanged | Header |
| Size | **Updated** | S1a ≈ 5,380 (≈2,580 production and docs + ≈2,800 tests); S1b ≈ 480 (M), unchanged. Estimates, not measurements | Size and split; SLICES.md |

## Grill r5 resolution

Keyed to `GRILL-S1-r5.md` (PASS, no blockers, 3 should-fixes, 2 polish). This is the one final fixing round; the review cap is reached and no sixth grill follows. No new machinery and no scope growth; sizes unchanged (the added fixtures fit within the existing T-5/T-8 estimates).

| Finding | Outcome | What changed | Where |
|---|---|---|---|
| R5-S-01 After-C3 oracle excludes files the real writer creates | **Fixed** | The recreated home must hold exactly the note (expected content) plus the derived artifacts of that real write: the persistent search index under `agents/X/.omnipus/.index/bleve/` and any signature bookkeeping file, identified by a control run of the same writer into an empty folder; record, `SOUL.md`, `AGENT.md` and any other file must be absent. The writer is constructed and kept before C3 (cold index), home absence is asserted right after C3, then the write runs. The test closes its own writer's handles when simulating the gateway stop and then asserts the whole home is gone. Row 19 keeps its before-C3 assertion. No fake writer, no production memory change | Oracle check 5; D7 rows 19–20 bullets and table rows; BDD "Content in the agent's home folder follows the folder…"; D15 row 10; test #26 |
| R5-S-02 Collect-before-delete is not a stable snapshot under offset paging | **Fixed** | Step 4 now states the precondition (a stable session list: pause other session activity, not only X's), keeps collect-before-delete, and adds a fresh verification scan from offset 0 after deleting; leftovers mean another pass; without a stable list the result is reported incomplete/unverified, whatever the HTTP answers were. New D15 row 11 deletes an unrelated first-page session between requests | Manual recovery step 4 and final clean-up; FR-037a; BDD "Manual recovery frees everything"; D15 row 11; test #21d; DOC-001; ADR.md A7; SLICES.md |
| R5-S-03 All-workspace oracle is wider than the C1 boundary | **Fixed (boundary kept)** | Check 3 removes X only from the import workspace W; other workspaces keep completed edits, and X references another request added there are AD-5. New Manual recovery step 5 repairs them when the record is gone, with the existing `GET /api/v1/workspaces?status=all` and `PUT /api/v1/workspaces/<id>` carrying `revision`, `core_team` and `delegation`; errors or revision conflicts mean incomplete, re-read and retry; no new endpoint. Final clean-up end state is limited to the named places and says it is not an all-data guarantee. New D7 row 21 and D15 row 12. "Every edge" sentences now say "in the import workspace" | Oracle check 3; AD-5; What rollback covers; Manual recovery step 5 and final clean-up; US-3 AS-5; Behavioral contract; Ambiguity Warning 10; BDD outlines; D7 row 21; D15 row 12; FR-037a; SC-004; L16; DOC-001; ADR trace D4; ADR.md A7; SLICES.md |
| Polish: Integration Boundaries "entry + self-row" | **Fixed** | Leave now removes X's entry and every edge involving X in the import workspace | Integration Boundaries / Workspace membership |
| Polish: ADR amendments intro points at revision 3 | **Fixed** | Points at revision 6 (final); amendment rows keep their round history | ADR.md Amendments |
| Founder acceptance of the narrower guarantee | **Open, owned by the chief** | Clarifications (revision 6) adds what the request must also say (other-workspace references and sessions need manual steps; not an all-data guarantee) | Clarifications |
| Riders (#1440, #1295) | **Open, owned by the chief** | Unchanged | Header |
| Pre-existing 409 text "kept until normal retention" in `deleteSession` | **Noted for the chief** | Not changed in S1 | Existing Codebase Context |
| Size | **Unchanged** | S1a ≈ 5,380 (≈2,580 production and docs + ≈2,800 tests); S1b ≈ 480 (M). Estimates, not measurements | Size and split; SLICES.md |
