# Draft architecture — module map and per-module CLAUDE.md

**Status:** draft (shape agreed 2026-09-12; re-baselined 2026-09-15 after the library-improvements merge ff11e8249; not implemented, not an ADR, not a spec)

**Agreed so far**

- Product module map (Workspace contains chat + work; kernel is not a screen).
- One `CLAUDE.md` per module, both sides; root stays thin.
- `loop.go` file list (13 `loop_*` + 3 moves into `turn.go` / `goal_loop.go` / `task_executor.go`).
- `rest.go` file list (10 new `rest_*`; do not recreate tasks/workspaces/plans/auth). **Founder agreed 2026-09-12.**

- Tools: channel grain (one package per family, not per `Name()`); dissolve `pkg/sysagent/tools` into product families. **Founder agreed 2026-09-12.**

**Changes 2026-09-15** (validation after ff11e8249; every number below is re-measured against `release/v0.1.1` @ `1f996b01d`, not carried over):

- Every giant grew again (`loop.go` now 16,515 lines / 246 funcs, `rest.go` 11,338, `gateway.go` 6,641, `chat.ts` 6,695, and seven more) — both size tables re-baselined.
- The library-improvements merge landed a **Knowledge Base** module (`pkg/knowledge`, `pkg/records`, `pkg/vaultimport`, `pkg/vaultprops`, `pkg/library`, ~83k lines across 139 files) with no home in this map. Added below; needs a founder call on `pkg/library` vs `pkg/media/library`.
- The giants inventory missed five files that already existed on 2026-09-12 (`vaultimport/infer.go`, `knowledge/index.go`, etc.) — this map now says explicitly that the inventory must come from a size scan, not memory.
- `rest.go`'s split target was aimed at a **6-of-16 slice**; `pkg/gateway/` already has 63 `rest_*.go` files today. Reworded so the 10 proposed files finish emptying `rest.go`, and the existing 63 get sorted into modules separately.
- `funlen` correction: it is configured (120 lines/40 statements) but **disabled** — not merely "not enforced on these files," not running anywhere.
- Proposed `loop_commands.go` collided with the real `pkg/agent/loop_command.go` (singular) — renamed the proposal.
- `pkg/tools/` grew from 64 to 73 files (nine new). Two of the new ones (`goal_claim.go`, `set_goal.go`) were already in the `work/` family table. The other seven new files, plus four older files the 2026-09-12 pass missed, are assigned below: eleven assignments in total.
- ADR numbers 067 and 068 each have three unrelated files today (see the docs map). Cite ADRs by title, not number alone.

**Still open:** file-budget ratchet implementation; nested `CLAUDE.md` landing.

**Decisions for the founder (2026-09-15), each with a recommendation:**

| # | Decision | Recommendation |
|---|---|---|
| D1 | `pkg/library` (text file tree) and `pkg/media/library` (binary media) merge or stay separate | Stay separate. The code already treats them as different jobs. Detail in the Knowledge Base section. |
| D2 | UI folder for the knowledge base: promote to `src/components/knowledge/` or keep nested under `src/components/library/knowledge/` | Promote, so the backend package `pkg/knowledge` and the UI folder carry the same name, as the map's first rule requires. Do it in Sequence step 4b, not before. |
| D3 | `argrepair.go` home | Hub by default; see the tools table. |

## Intent

The tree grew by accretion. The UI was recut around **workspaces**. The backend was not. Agents pay for that: a calendar change still means opening the HTTP pile, the turn-loop pile, and a tools grab-bag.

Two paired decisions:

1. **One product module = one name on both sides** (Go package and UI folder).
2. **One `CLAUDE.md` per module**, plus a thin root file. Module files load only when the agent works in that folder.

## Product vs kernel

**Workspace is the product.** Chat is a workspace tab, same as Board, Calendar, and Goals. It is not a sibling product. The UI already says this: `/workspaces/:id/chat` sits next to `/board` and `/calendar`.

**Kernel is not a screen.** It is the engine every tab sits on: the turn loop, the transcript log, sandbox, credentials, providers, config. There is no Kernel tab. If it broke, every tab would break.

Agents stay outside Workspace: the same Mia can sit on several workspaces. Connectors stay outside: Slack is a door, not a room.

```mermaid
flowchart TB
  subgraph product [Product]
    WS[workspaces]
    WS --> chat
    WS --> work["work: board, list, calendar, goals, plans, tasks"]
    WS --> team
    WS --> graph
    WS --> media
    agents
    connectors
    browser
    skills
    library
    knowledge["knowledge (base)"]
    settings
    preview
  end
  subgraph kernel [Kernel — no screen]
    runtime["runtime: turn loop"]
    transcript["transcript: conversation log"]
    sandbox
    credentials
    providers
    config
  end
  chat --> runtime
  chat --> transcript
  work --> runtime
  connectors --> WS
  agents --> WS
  knowledge --> library
```

**Knowledge (base)** landed 2026-09-14/15, after the module map's shape was agreed, in `pkg/knowledge` + `pkg/records` + `pkg/vaultimport` + `pkg/vaultprops` (backend) and `src/components/library/knowledge/` + `src/components/library/preview/` (UI, already nested under Library, not a sibling folder). It sits next to Library, not inside it, because the backend is a separate package family — but see "Knowledge Base — the missing module" below for the naming tension this creates and the decision it needs.

## Target folders (same names both sides)

Go and TypeScript stay in separate trees. Alignment is **names and seams**, not mixed folders.

```
pkg/workspaces/                 src/components/workspaces/   # until src/modules/
  identity/                       # workspace record, team, settings
  chat/                         src/components/chat/
  work/                         src/components/calendar/ + board/list still under workspaces/
  graph/
  media/

pkg/agents/                     src/components/agents/
pkg/connectors/                 src/components/connectors/   # today's pkg/channels
pkg/browser/                    src/components/browser/
pkg/skills/                     src/components/skills/
pkg/library/                    src/components/library/      # file-explorer surface (workspace work/ tree)
pkg/knowledge/                  src/components/library/knowledge/  # today: nested under library, not a sibling — see decision below
  records/                        # today's pkg/records (typed record model, schemas, search)
  vaultimport/                    # today's pkg/vaultimport (Bases importer)
  vaultprops/                     # today's pkg/vaultprops (knowledge_find tool wrapper)
pkg/settings/                   src/components/settings/ + providers/ + usage screens

pkg/kernel/
  runtime/                      # today's pkg/agent loop
  transcript/                   # today's pkg/session
  sandbox / audit / policy / credentials / providers / config

pkg/gateway/                    # THIN: mux, auth, SPA embed, /preview — no domain REST
```

`src/components/ui/`, `layout/`, `shared/` are chrome, not a product. No module `CLAUDE.md`, or a five-line "this is chrome." `src/components/screens/` is a leftover dump; those screens belong with their module (Agents, Connectors, Settings, Usage, Calendar).

Still one Go binary. SPA still embedded. Not microservices.

## CLAUDE.md — one per module

Root file: every session, **under ~200 lines**, rules that apply everywhere (authorship, no force-merge, single binary, cite `file::symbol`, pointers). **Current state (2026-09-15): root `CLAUDE.md` is 441 lines** — over twice the target, and none of this module's file split has happened yet. Only two nested `CLAUDE.md` files exist in the whole tree today: `.claude/CLAUDE.md` (GitNexus pointer) and `deploy/ci-worker/CLAUDE.md`. Every module row in the table below is still unimplemented.

Module file: lives in that module's folder; loads only when the agent reads there. An agent fixing Calendar never pays for Landlock essays.

**What belongs in a module file**

- how to run *its* tests (narrow `-run`, not `./...`)
- gotchas unique to it
- local "never resurrect X" (JPEG screencast lives with browser)

**What does not**

- architecture tours, ADR novels
- procedures → a **skill** in that module's `.claude/skills/`
- "always format / never do X" that must not be optional → **hook** or `scripts/check-no-*.sh`

**Anti-rot:** every line must name a failure it prevents. If it does not, cut it. Otherwise each module file grows the same way the root one did (catastrophic remembering).

## Until packages move — drop files on today's seams

Do not wait for the rename. Nested files are cheap; a wrong package graph is expensive.

| Module | Backend `CLAUDE.md` now | UI `CLAUDE.md` now |
|---|---|---|
| Root | `CLAUDE.md` (cut) | — |
| Runtime (kernel) | `pkg/agent/CLAUDE.md` | — (no screen) |
| HTTP shell | `pkg/gateway/CLAUDE.md` | — |
| Workspaces | `pkg/workspace/CLAUDE.md` | `src/components/workspaces/CLAUDE.md` |
| Chat | (under workspace until recut) | `src/components/chat/CLAUDE.md` |
| Work | `pkg/task/`, `pkg/goal/`, `pkg/plan/` (one file that points at the other two, or three short ones) | `src/components/calendar/CLAUDE.md` + board/list notes in workspaces file |
| Agents | `pkg/coreagent/CLAUDE.md` | `src/components/agents/CLAUDE.md` |
| Connectors | `pkg/channels/CLAUDE.md` | `src/components/connectors/CLAUDE.md` |
| Browser | `pkg/tools/browser/CLAUDE.md` | `src/components/browser/CLAUDE.md` |
| Skills | `pkg/skills/CLAUDE.md` | `src/components/skills/CLAUDE.md` |
| Library | `pkg/library/CLAUDE.md` (file-explorer) | `src/components/library/CLAUDE.md` |
| Knowledge Base | `pkg/knowledge/CLAUDE.md` (points at `records/`, `vaultimport/`, `vaultprops/`) | `src/components/library/knowledge/CLAUDE.md` |
| Settings | `pkg/providers/CLAUDE.md` | `src/components/settings/CLAUDE.md` |

`pkg/media/library/` (binary media storage, ADR-051) is a different package on purpose — see the decision note below. Its file already says as much in its own header, so no `CLAUDE.md` correction is needed there, only in this map.

When packages move, the files move with them.

## Knowledge Base — the missing module (added 2026-09-15)

ADR-089 renamed "vault" to "knowledge base" across the product. Five backend packages landed with it on 2026-09-14/15 and had no home in this map:

| Package | Files | Lines | What it is |
|---|---|---|---|
| `pkg/knowledge` | 53 | ~36,200 | Knowledge base engine and its agent tools: full-text index (`index.go`), authoring (`authoring_tools.go`, `knowledge_edit.go`, `knowledge_restructure.go`), collection setup (`knowledge_base_create.go`, `knowledge_configure.go`), listing (`knowledge_list.go`) |
| `pkg/records` | 57 | ~29,600 | Typed record model behind the knowledge base: schemas, property inference plumbing, `knowledgefind/` (the retrieval path) |
| `pkg/vaultimport` | 18 | ~13,400 | Importer for existing Obsidian-style vaults (Bases translation, frontmatter type inference) |
| `pkg/vaultprops` | 5 | ~2,200 | Thin wrapper exposing `knowledge_find` (`find_tool.go`) as a tool |
| `pkg/library` | 6 | ~1,700 | The workspace file-explorer surface the knowledge base is built on top of (see decision below) — **not** the same thing as `pkg/media/library` |

On the wire, `pkg/media/library.go` also moved to `pkg/media/library/library.go` in the same merge — a path change, not a rename of what it does (binary media storage, ADR-051).

**Decision needed (flagging for founder ratification):** `pkg/library`'s own header already states its boundary in code — it is the workspace-relative file-explorer (rooted at `workspaces/<id>/work/`, path-safety is its whole job) and is explicitly **not** `pkg/media/library` (the UUID-keyed binary media store). `pkg/knowledge` is layered on top of `pkg/library`, not on top of `pkg/media/library`. Recommendation: **keep `pkg/library` and `pkg/media/library` separate** — they solve different problems (path-safe text file access vs. content-addressed binary storage) and the code already treats them as such. Name the product module **Knowledge Base**, backed by `pkg/knowledge` + `pkg/records` + `pkg/vaultimport` + `pkg/vaultprops`, sitting next to (not inside) Library.

**Second decision needed:** on the UI side, the knowledge base is already nested under `src/components/library/knowledge/` and `src/components/library/preview/knowledgeMarkdown.tsx` — not a sibling folder. That is the opposite shape from the backend (`pkg/knowledge` is its own package, not nested under `pkg/library`). The "one product module = one name on both sides" rule this map opened with is not satisfied today. Two ways to resolve it, founder's call: (a) promote the UI to `src/components/knowledge/` to match the backend, or (b) treat Knowledge Base as UI-nested-under-Library by design (it is, after all, a view *of* the library) and relax the pairing rule for this one case.

**Rule going forward:** the giants inventory (below) and this module inventory both come from a size/`ls` scan run at write time, never from what a prior pass remembered. The five files above already existed on 2026-09-12 and were missed because the original list was not re-scanned.

## Other unmapped top-level `pkg/` dirs (added 2026-09-15)

These pre-existing packages are not in the mermaid diagram or target-folders block above and are not products in their own right. Listed here so the map is total — none of these get a `CLAUDE.md` of their own beyond a short pointer, and none belong on the giants list unless they cross 2,000 lines.

| Kernel (part of the runtime engine) | Shared / cross-cutting util | Module-internal (belongs to a product module above, not top-level) |
|---|---|---|
| `bus` (MessageBus), `security`, `policy`, `sandbox`, `credentials`, `auth`, `entity`, `state`, `daemon`, `migrate` | `fileutil`, `pathsafe`, `fspolicy`, `filegrep`, `logger`, `constants`, `utils`, `validation`, `testutil`, `identity`, `clidetect`, `docextract`, `gitevidence` (new 2026-09-15 — evidence capture for git operations) | `agentstore`, `askuser`, `commands`, `cron`, `datamodel`, `devices`, `email` (tools family, not top-level), `health`, `heartbeat`, `memrooms`, `notifications`, `onboarding`, `pairing`, `voice` |

`env.go` at `pkg/` root is a loose file, not a package dir — leave it where it is.

## Sequence (after acceptance)

1. Cut root `CLAUDE.md` and add the nested files above. No package moves.
2. Install the file-budget ratchet (below). Grandfather the current giants so they may only shrink.
3. Split `rest.go` along the section comments that already exist (Sessions, Agents, uploads). Lowest risk: same package, same type, new files.
4. Split `src/lib/api.ts` the same way (one client file per module).
4b. Knowledge Base module: apply D1 and D2. Promote `src/components/library/knowledge/` to `src/components/knowledge/`, add the two nested `CLAUDE.md` files, and sort the `rest_knowledge*` / `rest_library*` handlers into the module when step 5 moves domain REST out of the gateway. No Go package moves before D1 is ratified.
5. Thin gateway: domain REST registers from its module (`Register(mux)`).
6. Merge work (task / plan / goal / cron) behind the workspace **work** name.
7. Rename `channels` → `connectors`.
8. Split runtime files (`loop.go`, `turn.go`, `plan_engine.go`) last — same package, many files, one type.

## How files grew (so we do not repeat it)

This repo *configures* a function-length cap (`funlen`: 120 lines / 40 statements in `.golangci.yaml`) but it is **disabled** — `funlen` sits in `linters.disable` (`default: all`) and is excluded again in the file's `issues.exclude-rules`, so it never runs. There is no file cap either. `pkg/agent/loop.go` is 16,515 lines and 246 top-level functions (re-measured 2026-09-15; was 14,912 / 224 on 2026-09-12). Most of those functions are individually legal even against the configured-but-off cap. They all landed in one file because **new methods of `AgentLoop` were added to the file that already held `AgentLoop`**.

The same pattern, four times, all grown since 2026-09-12:

| File | Lines (2026-09-15) | Lines (2026-09-12) | What actually happened |
|---|---|---|---|
| `pkg/agent/loop.go` | 16,515 | 14,912 | Constructor, idle tickers, wiring, accessors, delegation, Run/Stop, events, tool-loading — one type, one file |
| `pkg/gateway/rest.go` | 11,338 | 11,008 | Sessions, agents, uploads, media, CORS helpers. Tasks and workspaces already escaped to `rest_tasks.go` / `rest_workspaces.go`; the rest never followed |
| `src/lib/api.ts` | 5,939 | 5,023 | Every HTTP wrapper in one module. Generated types are already separate; the hand-written client is not |
| `src/store/chat.ts` | 6,695 | 6,250 | One Zustand store for messages, streaming, tool-result clamping, buckets |

Go does **not** require this. Methods of the same type may live in many files in the same package. `net/http` does it. We already do it for REST (`rest_tasks.go` is `restAPI` methods that used to belong in `rest.go`). The missing rule is: **the next function goes in the file whose one-sentence job matches, not the file that already compiles.**

## How to split (same package, same type, new file)

A file has a one-sentence job. If you need “and” to describe it, it is two files.

Do not split by size (“take the bottom 4,000 lines”). Split by **lifecycle or resource**:

- *Construct / run / stop* vs *idle* vs *wire dependencies* vs *emit events* vs *delegation policy*
- *Mux + JSON helpers* vs *sessions HTTP* vs *agents HTTP* vs *uploads HTTP*

The type stays. The package stays. Only the filename changes. Callers do not change. Tests move with the functions they cover (`loop_idle_test.go` next to `loop_idle.go`).

**First cuts, named from the files as they are today**

`pkg/gateway/rest.go` (11,338 lines, 154 functions, 99 of them `restAPI` methods — re-measured 2026-09-15; was 11,008 / 151 on 2026-09-12). Same package, same `restAPI` type. Banners already mark the cuts (`// --- Sessions ---`, `// --- Agents ---`, …). Tasks / workspaces / plans / auth / onboarding **already left** (`rest_tasks.go`, `rest_workspaces.go`, `rest_plans.go`, `rest_auth.go`, `rest_onboarding.go`, `rest_sign_in.go`) — finish that job.

**The split is bigger than it looks.** `pkg/gateway/` already has **63** `rest_*.go` files today (`ls pkg/gateway/rest_*.go`, non-test), not "6 exist + 10 to create." The 10 proposed below still finish emptying `rest.go` itself — every one of their handlers is confirmed still living there. Separately, the gateway-thinning step (Sequence step 5) needs to sort the existing 63 into the same product modules as everything else in this map, not leave them as an undifferentiated `rest_*` pile:

| Module | Existing `rest_*.go` files | Count |
|---|---|---|
| Tool / exec | `agent_executor`, `clivalidate`, `commands`, `exec`, `executor_preview`, `executor_smoketest`, `inbound_validate`, `tool_policies`, `tool_registry`, `tool_results` | 10 |
| Sign-in / auth | `auth`, `integrations_auth`, `onboarding`, `sign_in`, `signin_copilot` | 5 |
| Security | `audit_log`, `god_mode`, `prompt_guard`, `sandbox_config`, `security_wave3`, `security_wave4`, `security_wave5`, `skill_trust` | 8 |
| Settings | `context_settings`, `default_model`, `gateway_restart`, `memory_settings`, `pending_restart`, `performance`, `rate_limits`, `retention`, `settings` | 9 |
| Knowledge | `knowledge`, `knowledge_base_views`, `knowledge_find`, `knowledge_record`, `knowledge_relation`, `knowledge_view`, `knowledge_views`, `library_knowledge_cascade` | 8 |
| Library | `library`, `library_files_search`, `library_preview` | 3 |
| Providers | `providers_catalog`, `providers_delete`, `providers_entitlement` | 3 |
| Task / work | `automations`, `plans`, `task_runs`, `tasks` | 4 |
| Workspace | `session_scope`, `workspace`, `workspace_delegation`, `workspace_instructions`, `workspace_media`, `workspace_mounts`, `workspaces` | 7 |
| Misc | `host_folders`, `mailbox`, `preview`, `preview_audit`, `stats`, `voice` | 6 |

63 total. `rest_task_runs.go` already exists (224 lines) — drop it from the second-cuts table below; it is not a file still to create.

**Stay as `rest_*.go` (new files, still `package gateway`):**

| File | One-sentence job | Takes from `rest.go` |
|---|---|---|
| `rest.go` | Mux, CORS, JSON helpers. No domain handlers | `setCORSHeaders`, `withAuth`, `writeJSON` / `jsonErr`, `registerAdditionalEndpoints` (thin: only `Handle*` wiring) |
| `rest_sessions.go` | Session CRUD and messages | `HandleSessions`, `listSessions`, `getSession`, `getSessionMessages`, `createSessionHTTP`, `renameSession`, `deleteSession` |
| `rest_agents.go` | Agent CRUD, runner test, tool-policy guards | `HandleAgents` through `updateAgent` (create ~500 lines, update ~900 lines) |
| `rest_config.go` | Read/write `config.json` and credential refs | `HandleConfig`, `getConfig`, `updateConfig`, `storeCredential`, `safeUpdateConfigJSON` |
| `rest_skills.go` | Installed skills and marketplace | `HandleSkills`, `listSkills`, `searchSkills`, `installSkill`, `deleteSkill` |
| `rest_providers.go` | LLM provider catalog and keys | `HandleProviders` (~1,000 lines today) |
| `rest_mcp.go` | MCP server CRUD, tools, test | `HandleMCPServers`, `addMCPServer`, `patchMCPServer`, `testMCPServer` |
| `rest_tools.go` | Tool registry and per-agent tool visibility | `HandleTools`, `HandleMCPTools`, `updateAgentTools` |
| `rest_channels.go` | Connectors: enable, configure, routing, test | `HandleChannels` through `testChannel` (~1,600 lines) |
| `rest_uploads.go` | Uploads and media serve | `HandleUpload`, `HandleServeUpload`, `HandleMedia`, `HandleMediaByRef` |
| `rest_status.go` | Doctor, state, version, devices, activity, storage | `HandleDoctor`, `HandleState`, `HandleStatus`, `HandleVersion`, `HandleDevices`, `HandleActivity`, `HandleStorageStats`, `HandleUserContext` |

Do **not** create `rest_tasks.go` / `rest_workspaces.go` / `rest_plans.go` / `rest_auth.go` — they already exist. The leftover `// --- Tasks ---` stub in `rest.go` (`validateEntityID`) moves into `rest_tasks.go`.

That is **10 new files**. Split because they are **mixed concerns**, not because of a line quota. If `rest_agents.go` is still one job (agent CRUD) at 1,200 lines, leave it. Split again only if it becomes agents *and* something else. **Founder agreed this REST split 2026-09-12.**

## Tools — channel grain, not Name() grain

Copy the **channel** pattern at the grain channels actually use: one package per independently failing product, files split inside it. Do **not** copy it at one package per `Name()` (~80 packages). That would recreate `channels/manager.go` as a registry that imports every tool.

Today: one package `pkg/tools` (73 files, up from 64 on 2026-09-12), plus `pkg/tools/browser/` (the existence proof), plus `pkg/sysagent/tools` (fossil name — not a chat agent). MCP tools stay wrappers.

**Eleven files with no family, assigned now.** Seven are new since 2026-09-12 (the five `shell_escape_sweep*` files and the two `task_*` files). Four are older and were simply missed (`grep.go`, `argrepair.go`, `ingest_bound.go`, `session_manager_export.go`). The other two new files, `goal_claim.go` and `set_goal.go`, were already listed under `work/`.

| File | Family | Why |
|---|---|---|
| `grep.go` | `fs/` | New `grep` tool, Category filesystem — same family as `read_file`/`edit_file` |
| `argrepair.go` | hub | Argument-repair helper. Default: hub, because it is called from more than one family. Move into `fs/` only if a grep at extract time shows filesystem tools are its only caller. |
| `ingest_bound.go` | hub | Bounds-checking shared across tool input, not one family's concern |
| `session_manager_export.go` | `chat/` | Session export lives with the other session/chat tools |
| `shell_escape_sweep.go`, `shell_escape_sweep_ctime_darwin.go`, `shell_escape_sweep_ctime_linux.go`, `shell_escape_sweep_other.go`, `shell_escape_sweep_unix.go` | `shell/` | All five are shell-escape-timing guards, same family as `shell.go` / `shell_guard.go` |
| `task_assignee_readiness.go` | `work/` | Task-readiness check, same family as `task.go` / `run_task.go` |
| `task_attempt_budget.go` | `work/` | Task-attempt budget, same family |

Boot already registers tools explicitly (`BuiltinRegistry.RegisterBuiltin`). Keep that. Each **family** package exposes `Register(reg)` so the hub lists ~16 families, not ~80 names. No `init()` if-ladder.

**No `pkg/sysagent` in the target.** That chat agent does not exist. The live *System Agents* category is only Judge + Plan Supervisor (locked, not chat targets, not these tools). Admin tools dissolve into the families below.

### Hub — stays `pkg/tools/`

Interface, registry, compositor, results, catalog, MCP wrapper, ToolSearch. Not a user-facing capability.

Files: `base.go`, `registry.go`, `builtin_registry.go`, `compositor.go`, `result.go`, `types.go`, `validate.go`, `manifest.go`, `general_builtin_catalog.go`, `mcp_tool.go`, `mcp_registry.go`, `tools_tool.go`, `channel_ownership.go`, `normalization.go`, `metadata_guard.go`. Helpers `deps.go` / `registry.go` / `category.go` from `pkg/sysagent/tools` fold in here or into the family that needs them.

### Families (under `pkg/tools/` until the module map moves them)

| Package | Tool names | Files in (today) | Split inside |
|---|---|---|---|
| `browser/` | ~18 browser tools | already `pkg/tools/browser/` | `manager.go` (4,124) and `live.go` (3,672) later |
| `delegate/` | `delegate` | `delegate.go` | `run.go` / `status.go` / `followup.go` / `park.go` |
| `shell/` | `bash` | `shell.go`, `shell_guard.go`, `shell_subst_guard.go`, `shell_process_*` | `shell.go` vs `shell_bg.go` |
| `fs/` | `read_file`, `write_file`, `list_directory`, `edit_file`, `append_file`, `request_mount`, `list_mounts` | `filesystem.go`, `edit.go`, `resolvepath.go`, `fserrors.go`, `path_audit.go`, `list_mounts.go`, `request_mount.go` | `resolvepath` stays shared; `filesystem.go` only if still over 800 |
| `web/` | `search_web`, `fetch_url`, `web_serve` | `web.go`, `web_serve.go`, `search_tool.go`, `search_ambiguity.go`, `fuzzy.go` | `search.go` / `fetch.go` / `serve.go` |
| `work/` | `task`, `run_task`, `plan`, `plan_correct`, `stop_plan`, `set_goal`, `goal_claim`, `todos`, `list_jobs`, `create_task_in_workspace`, `update_task_in_workspace`, `delete_task_in_workspace`, `list_tasks_in_workspace` | `task.go`, `run_task.go`, `plan.go`, `plan_correct.go`, `stop_plan.go`, `set_goal.go`, `goal_claim.go`, `todos.go`, `list_jobs*.go` **+** `pkg/sysagent/tools/task.go` | `task.go` mutate vs query; `set_goal.go` if still over 800 |
| `chat/` | `send_message`, `message_parent`, `ask_user_question`, `handoff`, session inspect/recall | `message.go`, `message_parent.go`, `ask_user_question.go`, `handoff.go`, `session.go`, `inspect_session.go`, `recall_conversation_meta.go`, `session_process_*` | already small enough |
| `agents/` | `create_agent`, `update_agent`, `delete_agent`, `read_agent_metadata` | `pkg/sysagent/tools/agent.go`, `metadata.go` | — |
| `workspaces/` | `create_workspace`, `update_workspace`, `delete_workspace`, `list_workspaces`, `get_workspace` | `pkg/sysagent/tools/workspace.go` | — |
| `connectors/` | `enable_channel`, `configure_channel`, `disable_channel`, `list_channels`, `test_channel` | `pkg/sysagent/tools/channel.go` | — |
| `skills/` | `find_skills`, `install_skill`, `remove_skill`, `list_skills`, `create_skill`, `edit_skill` | `skill.go`, `skills_search.go`, `skills_install.go` **+** `pkg/sysagent/tools/skill.go`, `skill_authoring.go` | — |
| `library/` | `library_list`, `library_read`, `send_file` | `library_tool.go`, `send_file.go` | — |
| `knowledge/` (added 2026-09-15) | `knowledge_search`, `knowledge_graph`, `knowledge_describe`, `knowledge_read`, `knowledge_configure`, `knowledge_base_create`, `knowledge_create_note`, `knowledge_link`, `knowledge_set_property`, `knowledge_append_section`, `knowledge_rename`, `knowledge_move`, `knowledge_tasks`, `knowledge_restructure`, `knowledge_edit`, `knowledge_list`, `knowledge_find` (17 tools, grepped from `Name() string` across `pkg/knowledge` and `pkg/vaultprops`) | `pkg/knowledge/{tools,authoring_tools,knowledge_edit,knowledge_restructure,knowledge_base_create,knowledge_configure,knowledge_list}.go`, `pkg/vaultprops/find_tool.go` | already product-shaped; the underlying `pkg/records`/`pkg/vaultimport` logic packages stay non-tool support code, not part of this family |
| `memory/` | memory tools | `memory.go`, `memory_rate_limit.go` | — |
| `email/` | email | `email.go` | — |
| `settings/` | `get_config`, `set_config`, `configure_provider`, `list_providers`, `test_provider`, `list_models`, `add_mcp_server`, `remove_mcp_server`, `list_mcp_servers`, `run_doctor`, `get_usage` | `pkg/sysagent/tools/{config,provider,mcp,diag}.go` | — |

That is **16 families + hub** (was 15 on 2026-09-12; `knowledge/` added). **`pkg/sysagent/tools` is gone** as a package.

When the module map moves, the family is the unit that moves: `work/` and `chat/` under `pkg/workspaces/`; `delegate/` and `shell/` stay kernel; `browser/` stays a product.

**Extract order:** `delegate/` → `shell/` → `fs/` → `work/` → dissolve sysagent into `agents/` `workspaces/` `connectors/` `skills/` `settings/` → browser file splits → hub shrinks.

`pkg/agent/loop.go` (16,515 lines, 246 functions — re-measured 2026-09-15; was 14,912 / 224 on 2026-09-12). Same package, same `AgentLoop` type. Some pieces **move into files that already exist** rather than getting a new `loop_*` name.

**Collision check (2026-09-15):** `pkg/agent/` already has five organic `loop_*.go` files — `loop_command.go`, `loop_env.go`, `loop_mcp.go`, `loop_media.go`, `loop_scheduler.go`. The proposed `loop_commands.go` (plural) below collides in spirit with the existing `loop_command.go` (singular) — checked and renamed to `loop_slash.go` to avoid it, and to keep the two files distinct (`loop_command.go` already owns whatever it owns; the new file must not be a near-duplicate name a future edit picks by autocomplete). The other 12 proposed `loop_*` names below were checked against the existing five and against each other — no other collisions.

**Stay as `loop_*.go` (new files, still `package agent`):**

| File | One-sentence job | Takes from `loop.go` |
|---|---|---|
| `loop.go` | Construct, run, and stop the loop | `NewAgentLoop`, `Run`, `Stop`, `Close`, `WaitForActiveRequests`, `ProcessDirect`, `ProcessScheduled`, `processMessage`, `runAgentLoop` |
| `loop_idle.go` | Idle timeout tickers | `RegisterIdleTicker`, `resetIdleTicker`, `fireIdleTimeout` |
| `loop_wire.go` | Attach tools and deps onto agents | `registerSharedTools`, `wireExecToolDeps`, `WireTier13Deps`, `WireSysagentDeps`, `wirePlanToolsForAgent`, `wireJobRosterForAgent` |
| `loop_delegation.go` | Workspace delegation edges and deny checkers | `currentDelegationDepth`, `findDelegationEdge`, `enforceEdgeModeAndDepth`, `buildDelegationDenyChecker*` |
| `loop_events.go` | Event bus and hooks | `SubscribeEvents`, `emitEvent`, `Emit*`, `MountHook` |
| `loop_config.go` | Live config / model / reload | `ReloadProviderAndConfig`, `SwapConfig`, `MutateConfig`, `TriggerReload`, `ApplyAgentModel` |
| `loop_browser.go` | Resolve a browser manager for an agent | `BrowserManagerForKey`, `BrowserPool`, `rewireBrowserManagerForKey` |
| `loop_session.go` | Channel session index and listing | `resolveOrCreateChannelSession`, `ListAllSessions`, `forgetSession`, `GetCurrentSession` |
| `loop_inbound.go` | Turn an inbound message into a routed agent | continuation targets, transcription, `resolveMessageRoute`, `resolveSteeringTarget`, `processSystemMessage` |
| `loop_slash.go` (renamed from the collision `loop_commands.go`; not the existing `loop_command.go`) | Slash / skill / memory commands | `handleCommand`, `applyExplicitSkillCommand`, `applyMemoryCommandPrompt` |
| `loop_window.go` | Sliding window and model switch | `assembleMessages`, `windowTrim`, `handleModelSwitch`, `selectCandidates` |
| `loop_policy.go` | Tool policy and approval at exec time | `resolveToolPolicyAtExec`, `CheckGrantOrRequestApproval`, `emitPolicyDenyAudit` |
| `loop_search.go` | Dynamic tool load and search promotion | `markToolsLoaded`, `tickSearchPromotionHorizon` |

**Move into siblings that already exist (do not create a fourth copy):**

| Move into | One-sentence job | Takes from `loop.go` |
|---|---|---|
| `turn.go` (already 2,718 lines, was 2,440 on 2026-09-12 — will itself need a follow-on split) | Run one turn | `runTurn` (`loop.go::runTurn`, lines 9740–14148, ~4,400 lines **in one function** — was ~3,809 lines at 8785–12594 on 2026-09-12), `abortTurn`, `typedTurnExit` |
| `goal_loop.go` | Goal forcing during a turn | `evaluateGoalForcing`, `bumpGoalQuestionRoundsUsed`, `goalRubricNoteForBudget` |
| `task_executor.go` | Board/task dispatch | `processTaskDirect`, `processTaskDirectExternalCLI`, `ExecuteBoardTask` |

Accessors (`AuditLogger()`, `GetConfig()`, `SetMediaStore()`, …) stay next to the field they touch, or in a thin `loop_accessors.go` if they clutter `loop.go`. They are not a product concern.

That is **13 new `loop_*` files + 3 moves into existing files**. Split by lifecycle (idle vs wire vs events), not to hit a quota. Do **not** dump all of `runTurn` into `turn.go` — extract helpers first (next subsection).

`src/lib/api.ts` — one file per product module, matching the map above: `api/agents.ts`, `api/sessions.ts`, `api/workspaces.ts`, `api/tasks.ts`. `api.ts` becomes a barrel of re-exports or disappears.

`src/store/chat.ts` — slices, not a 6,000-line store: messages, streaming, tool-result clamp, session bucket.

**Do not** create 80-line micro-files. A 400-line `rest_sessions.go` is the target, not a 40-line file per handler.

### `runTurn` — divide the function, not only the file

`runTurn` (`loop.go::runTurn`) is now **~4,400 lines** (lines 9740–14148, just before `typedTurnExit`) — was 3,809 lines at 8785–12594 on 2026-09-12. Still one function. The loop-split bench showed file cuts do not help it: Grep still needs two 2,000-line Reads. It is already three acts; they were never named (almost no `// ---` banners). Cite `loop.go::runTurn` going forward, not raw line numbers — this file's own line numbers moved by ~1,000 between the two passes above.

| Act | Lines (approx., 2026-09-15) | Job |
|---|---|---|
| Setup | ~657 (9740–10397) | Inject context, **register defers**, workspace gate, model switch, assemble history, three pre-turn gates (provider / model / window) |
| `turnLoop:` (label confirmed at 10397) | ~3,580 | Each iteration: rate-limit → steer → filter tools → call LLM (stream/retry) → run tools → persist narration → budget → park/abort/continue |
| Epilogue | ~126 | Search-promotion tick, late steering (`goto turnLoop`, confirmed at 14004), write assistant text if no streamer |

**Must stay in `runTurn`:** the defers. LIFO order is load-bearing (`clearActiveTurn` before `finalizeStreamer` before `Finish`; the “don’t claim success on error” defer must run first). Moving them into a helper ties them to that helper’s return and breaks cancel/`done` tests. Also stay: the `turnLoop:` label, or replace `goto turnLoop` with `continue` once the loop is one function. Do not scatter `goto` across files.

**Extract** — `runTurn` remains a short conductor (~200–400 lines): defers + `for`.

| New function | Takes |
|---|---|
| `prepareTurnWorkDir` | Workspace re-root / membership refuse (call the existing gate; don’t copy) |
| `runPreTurnGates` | Provider / model / window refusals |
| `assembleTurnMessages` | Initial history + attachments (already almost `assembleMessages`) |
| `prepareIteration` | Hard abort, iteration ceiling, SEC-26 rate limit, steering poll |
| `assembleIterationTools` | Policy filter, ToolSearch force-include, dedup |
| `callTurnLLM` | Stream, empty-retry, progress callback (the block just inside `turnLoop:`, near the top of the iteration). If still huge: stream vs retry inside it |
| `executeTurnTools` | Run calls, persist narration, citations |
| `applyMidTurnBudget` | Window / spend thrash-guard |
| `finishTurn` | Search-promotion tick, transcript fallback |

Do **not** slice `runTurn` by line number (1–2000 / 2001–3800). That would cut the `for` loop and the defer contract.

Files: `loop_turn.go` or `turn.go` holds the conductor; `turn_llm.go`, `turn_tools.go`, `turn_exit.go` hold the helpers. Each helper aimed at a few hundred lines, none over 2,000.

## Giants — final breakdown (meet the 2,000 / 4,000 rule)

Leave a file alone if it is **one job and ≤ 2,000 lines**. That includes `goal_loop.go` (1,816, was 1,658), `rest_plans.go` (1,458, unchanged), `rest_auth.go` (1,376, was 1,268), `context.go` (1,811, unchanged), `task/store.go` (1,859, was 1,815), `pkg/media/library/library.go` (1,891, unchanged — moved from `pkg/media/library.go`, see the Knowledge Base section), `telegram.go` (1,056, unchanged), settings sections ~1,300, `ws.ts` (1,021, unchanged). Lines re-measured 2026-09-15.

Must split: **> 4,000 lines**, or any size that mixes jobs. Should split: **2,000–4,000** that mix jobs. Every dest file below is aimed at **≤ 2,000**; none may exceed **4,000** except while a giant *function* is still being extracted (`runTurn`).

### Over 4,000 — dest files

Lines re-measured 2026-09-15 (`wc -l` against `release/v0.1.1` @ `1f996b01d`); prior (2026-09-12) value in parentheses where it differs.

| Today | Lines | Becomes (one sentence each) |
|---|---|---|
| `pkg/agent/loop.go` | 16,515 (was 14,912) | The 16 `loop_*.go` files already listed. Then **`runTurn` (~4,400 lines, one function)** extracted into `loop_turn.go` (orchestration) + `turn_llm.go` + `turn_tools.go` + `turn_exit.go` so no function needs two Reads. |
| `pkg/gateway/rest.go` | 11,338 (was 11,008) | The 10 `rest_*.go` files already listed (sessions, agents, config, skills, providers, mcp, tools, channels, uploads, status). Mux stays in `rest.go`. The other 63 existing `rest_*.go` files get sorted into modules separately — see the compact table above. |
| `pkg/gateway/gateway.go` | 6,641 (was 6,579) | `gateway.go` (`Run`/`RunContext`); `gateway_boot.go` (credentials, souls, roster); `gateway_reload.go` (watcher, `restartServices`); `gateway_sandbox.go` (egress, tool-policy repair) |
| `src/store/chat.ts` | 6,695 (was 6,250) | `chatMessages.ts`; `chatStreaming.ts`; `chatTools.ts` (clamp/truncation); `chatBuckets.ts` |
| `pkg/gateway/websocket.go` | 6,219 (was 5,614) | `websocket.go` (conn, auth, ServeHTTP); `websocket_chat.go` (`handleChatMessage`); `websocket_cancel.go`; `websocket_replay.go` (attach/replay); `websocket_pump.go` (`writePump`) |
| `pkg/agent/plan_engine.go` | 6,130 (was 5,351) | `plan_engine.go` (type, Start/Stop, Tick); `plan_engine_play.go`; `plan_engine_supervise.go` (unmet DoD / signature gate) |
| `pkg/config/config.go` | 5,278 (was 5,171) | `config.go` (`Config` + load/save); `config_agents.go`; `config_gateway.go`; `config_retention.go` (memory/compaction helpers) |
| `src/lib/api.ts` | 5,939 (was 5,023) | `api/agents.ts`, `api/sessions.ts`, `api/workspaces.ts`, `api/tasks.ts`, `api/plans.ts`, `api/channels.ts`, `api/skills.ts`, `api/config.ts` — barrel re-export only in `api.ts` |
| `pkg/tools/delegate.go` | 4,145 (was 4,026) | `delegate/run.go`, `status.go`, `followup.go`, `park.go` (family package) |
| `pkg/tools/browser/manager.go` | 4,124 (was 4,102) | `manager.go` (construct, config); `manager_tabs.go`; `manager_lease.go` |

### 2,000–4,000 — dest files

Lines re-measured 2026-09-15; prior (2026-09-12) value in parentheses where it differs. **The giants inventory below comes from a size scan (`find … | xargs wc -l | sort -rn`) run at write time, not from what a prior pass remembered** — the ten rows below the first divider were missed entirely on 2026-09-12 even though most already existed then.

| Today | Lines | Becomes |
|---|---|---|
| `pkg/coreagent/core.go` | 3,992 (was 3,434) — **8 lines under the 4,000 hard ceiling; flagging, not yet over** | `core.go` (roster literals, All/BaseAgents); `seed.go` (`SeedConfig`); `seed_system.go` (Judge / Plan Supervisor) |
| `pkg/tools/browser/live.go` | 3,672 (was 3,590) | `live.go` (registry, attach/detach); `live_viewport.go`; `live_idle.go` (sweeper / stand-down) |
| `src/components/chat/ChatScreen.tsx` | 3,607 (was 3,457) | `ChatThread.tsx`; `ChatComposer.tsx`; `ChatScreen.tsx` (shell) |
| `pkg/gateway/rest_tasks.go` | 3,489 (was 3,165) | `rest_tasks.go` (list/get/create/update); `rest_task_wire.go` (criteria/DoD mapping). Drop the previously-proposed `rest_task_runs.go` — it already exists (224 lines, start/runs) as of this merge. |
| `pkg/vaultimport/infer.go` | 3,279 (new to this map, existed 2026-09-12) | Mixes observation-collection (`LoadNotes`, `BuildNameIndex`, `CollectTypeGroups`) with type classification (`InferSchema`, `classifyProperty`). Split: `infer_collect.go` (collection/indexing) + `infer_classify.go` (schema classification). |
| `src/components/agents/AgentProfile.tsx` | 3,211 (was 3,223) | `AgentProfile.tsx` (identity); `AgentProfileTools.tsx`; `AgentProfileModel.tsx` |
| `pkg/knowledge/index.go` | 3,177 (new to this map, existed 2026-09-12) | Bleve full-text index lifecycle (open/rebuild/mapping-drift) plus a progress-reporting helper that doesn't need to live in the same file. Split: `index.go` (type, Open/Close, core lifecycle) + `index_health.go` (rebuild reason, mapping drift, format checks) + fold the small `progressCoalescer` into whichever of the two calls it more. |
| `src/components/browser/BrowserLiveView.tsx` | 3,057 (unchanged) | `BrowserLiveView.tsx` (video); `BrowserLiveControls.tsx`; `BrowserLiveError.tsx` |
| `pkg/tools/shell.go` | 2,736 (was 2,406) | `shell.go` (foreground); `shell_bg.go` |
| `pkg/agent/turn.go` | 2,718 (was 2,440) | stays until it absorbs `runTurn` helpers; then the `turn_*.go` files above |
| `pkg/agent/task_executor.go` | 2,696 (was 3,449 — **shrank**, moved below the 4,000/2,000 split threshold on its own) | `task_executor.go` (dispatch/lifecycle); `task_executor_run.go`; `task_executor_judge.go` (claim/verdict) if it grows again — currently close enough to leave |
| `pkg/agent/subturn.go` | 2,605 (was 2,453) | `subturn.go` (spawn); `subturn_identity.go` (ADR-032 fields) |
| `src/components/library/preview/knowledgeMarkdown.tsx` | 2,410 (new to this map) | **Leave, one job** — its own header states this is a deliberate third composition layered by object reference on `chat/markdown-shared.tsx` and `LibraryMarkdownPreview.tsx` specifically to avoid re-drifting a parser/renderer stack that has already drifted three times; splitting it risks recreating that exact problem. |
| `pkg/vaultimport/view_write.go` | 2,328 (new to this map) | Mixes assembling the translated view (`TranslateBase`, `translateOneView`) with leaf/tree resolution (`leafResolver` methods, `conjoin`, `translateLayout`). Split: `view_write.go` (assembly + outcome) + `view_write_resolve.go` (leaf resolution). |
| `pkg/agent/verifier_adjudication.go` | 2,272 (new to this map) | Mixes turn construction/soul-seeding with verifier session **window-text rendering** (several `*WindowText` functions) and availability tracking. Split: `verifier_adjudication.go` (turn construction) + `verifier_window.go` (window-text rendering) + `verifier_availability.go` (tracker/escalation gate). |
| `pkg/tools/task.go` | 2,256 (new to this map) | Mixes goal-store sync helpers (`GoalStoreForTasks`, `syncTaskGoalRecord`, `TerminateTaskGoalRecord`) with tool implementations (`TaskListTool`, `TaskCreateTool`). Split: `task.go` (goal-sync helpers) + `task_tools.go` (the `Tool` types). |
| `pkg/agent/goal_triggers.go` | 2,216 (new to this map) | **Leave, one job** — its own header states this is the single per-goal-id trigger state machine and the shared adjudication body both the claim path and idle-settlement path call; a second goal store or a split state machine is the exact drift it warns against (DoD-11). |
| `pkg/gateway/browser_ws.go` | 2,546 (was 2,531) | `browser_ws.go` (control); `browser_ws_input.go` |
| `pkg/gateway/rest_library.go` | 2,141 (new to this map) | Mixes entry CRUD (list/create/delete) with content get/put plus version/lock/conflict handling and knowledge-base-collection annotation. Split: `rest_library.go` (entry CRUD) + `rest_library_content.go` (get/put, ETag/version/conflict, lock resolution). |
| `pkg/session/unified.go` | 2,384 (was 2,367) | `unified.go` (create/read); keep lock in existing `unified_lock.go` — move any leftover lock code there |
| `pkg/records/knowledgefind/find.go` | 2,116 (new to this map) | **Leave, one job** — its own header frames this as "the retrieval path and its two bounds" (ADR-068 D15.3); the cursor-restore/caching logic is intrinsic to that one retrieval algorithm, not a second concern. |
| `pkg/channels/manager.go` | 2,180 (unchanged) | shrink by `RegisterFactory` per channel; leftover is start/stop only |
| `pkg/gateway/rest_workspaces.go` | 2,146 (was 2,138) | `rest_workspaces.go` (CRUD); `rest_workspace_team.go` (delegation/team) |
| `pkg/knowledge/knowledge_edit.go` | 2,051 (new to this map) | **Leave, one job** — its own header explicitly rules out a further split: unlike `knowledge_describe.go`/`knowledge_read.go`, this file already imports `pkg/tools` transitively through the mutation preamble, so there is no clean boundary left to cut along. |
| `pkg/gateway/browser_webrtc.go` | 2,108 (unchanged) | `browser_webrtc.go` (signaling); `browser_webrtc_session.go` |

### Order of work

1. `loop.go` (mapping already in the bench splitter) including **`runTurn` function extract**.
2. `rest.go` (banners already mark the cuts).
3. `delegate.go` + `shell.go` (tool families).
4. `gateway.go` + `websocket.go`.
5. `plan_engine.go` + `task_executor.go`.
6. `config.go` + `coreagent/core.go`.
7. SPA: `api.ts` → `chat.ts` → screens.
8. Second cuts: `rest_tasks.go`, `rest_workspaces.go`, browser `manager.go` / `live.go`.
9. `channels/manager.go` once families self-register.

## The rule (plain English)

**One file, one job. One function, one job.**

If you need the word “and” to describe the file, it is two files. Same package and same type is fine — only the filename changes.

**Size is a ceiling, not a reason to dice a good file.**

| | Why | Production | Tests |
|---|---|---|---|
| Target | One Read in this Claude Code harness (2,000-line default) | 2,000 lines | 3,000 lines |
| Hard | Two Reads; new files must not exceed; old giants must not grow | 4,000 lines | 6,000 lines |

A 1,200-line file that is only session HTTP is allowed. A 1,200-line file that is sessions *and* agents *and* uploads is not.

**A giant function is its own violation.** Splitting `loop.go` did not help `runTurn` (~4,400 lines): Grep still needs two Reads. Extract helpers until a function fits in one Read. (`funlen` is configured at 120 lines/40 statements in `.golangci.yaml` but is **disabled** — it sits in `linters.disable` and is excluded again in `issues.exclude-rules`, so it does not run at all today, on these files or any others. Function-length baseline: `function-length-baseline-2026-09-15.md` in this folder. 315 Go and 184 TypeScript production functions exceed 120 lines; the longest are `AgentLoop.runTurn` at 4,364 lines and the single Zustand `create` call in `src/store/chat.ts` at 4,568.)

**Do not** create a dozen tiny files for one feature. That is the other way to lose the agent.

## How we enforce it (mechanical, not hope)

Prose in `CLAUDE.md` will not hold. Same pattern as `scripts/check-no-jpeg-screencast.sh`.

1. **`scripts/check-file-budget.sh` in CI** (`make lint` / PR workflow), skip `generated/`, `spa/`, `node_modules/`:
   - New production file > 4,000 lines → fail.
   - File on the grandfather list → fail if `wc -l` is **higher** than the listed number. Shrink is allowed. Drop off the list when under 4,000.
   - New files never join the list.
2. **Grandfather list** starts as today’s files already over 4,000 (`loop.go`, `rest.go`, `gateway.go`, `websocket.go`, `plan_engine.go`, `config.go`, `chat.ts`, `api.ts`, `delegate.go`, `browser/manager.go`, `browser/live.go`, …). The list only shrinks.
3. **Function ratchet** (separate, or the same script): `runTurn` and any function over 2,000 lines listed by name/size; they may not grow; they leave the list when under 2,000. Do not turn `funlen` 120 on for the whole tree in one go — it would fail hundreds of files and block shipping.
4. **Review:** “Does this belong in this file’s one-sentence job?” After `rest_sessions.go` exists, a new session handler in `rest.go` is a reject. The script is the backstop; this is the human check.
5. **Root `CLAUDE.md` gets two lines**, not an essay: the one-job rule + “do not add to a grandfathered file; extract first.”

The splitter at `/Users/danielpiatkowski/AI-Agent-Workspace/loop-split-bench/cmd/splitfile` is the mechanical *how* for a cut, not the gate. The gate is CI.

## Out of scope until a later draft

Enabling gopls/TypeScript LSP plugins; Read-deny on generated trees; PostToolUse format hooks. Those are harness work, not the module map.
