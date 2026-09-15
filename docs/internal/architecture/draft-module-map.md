# Draft architecture — module map and per-module CLAUDE.md

**Status:** draft (shape agreed 2026-09-12; not implemented, not an ADR, not a spec)

**Agreed so far**

- Product module map (Workspace contains chat + work; kernel is not a screen).
- One `CLAUDE.md` per module, both sides; root stays thin.
- `loop.go` file list (13 `loop_*` + 3 moves into `turn.go` / `goal_loop.go` / `task_executor.go`).
- `rest.go` file list (10 new `rest_*`; do not recreate tasks/workspaces/plans/auth). **Founder agreed 2026-09-12.**

- Tools: channel grain (one package per family, not per `Name()`); dissolve `pkg/sysagent/tools` into product families. **Founder agreed 2026-09-12.**

**Still open:** file-budget ratchet implementation; nested `CLAUDE.md` landing.

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
```

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
pkg/library/                    src/components/library/
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

Root file: every session, **under ~200 lines**, rules that apply everywhere (authorship, no force-merge, single binary, cite `file::symbol`, pointers).

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
| Library | `pkg/media/CLAUDE.md` or `pkg/library/` | `src/components/library/CLAUDE.md` |
| Settings | `pkg/providers/CLAUDE.md` | `src/components/settings/CLAUDE.md` |

When packages move, the files move with them.

## Sequence (after acceptance)

1. Cut root `CLAUDE.md` and add the nested files above. No package moves.
2. Install the file-budget ratchet (below). Grandfather the current giants so they may only shrink.
3. Split `rest.go` along the section comments that already exist (Sessions, Agents, uploads). Lowest risk: same package, same type, new files.
4. Split `src/lib/api.ts` the same way (one client file per module).
5. Thin gateway: domain REST registers from its module (`Register(mux)`).
6. Merge work (task / plan / goal / cron) behind the workspace **work** name.
7. Rename `channels` → `connectors`.
8. Split runtime files (`loop.go`, `turn.go`, `plan_engine.go`) last — same package, many files, one type.

## How files grew (so we do not repeat it)

This repo already caps a *function* (`funlen`: 120 lines in `.golangci.yaml`). It never caps a *file*. `pkg/agent/loop.go` is 14,912 lines and 224 top-level functions. Most of those functions are individually legal. They all landed in one file because **new methods of `AgentLoop` were added to the file that already held `AgentLoop`**.

The same pattern, three times:

| File | Lines | What actually happened |
|---|---|---|
| `pkg/agent/loop.go` | 14,912 | Constructor, idle tickers, wiring, accessors, delegation, Run/Stop, events, tool-loading — one type, one file |
| `pkg/gateway/rest.go` | 11,008 | Sessions, agents, uploads, media, CORS helpers. Tasks and workspaces already escaped to `rest_tasks.go` / `rest_workspaces.go`; the rest never followed |
| `src/lib/api.ts` | 5,023 | Every HTTP wrapper in one module. 199 exports. Generated types are already separate; the hand-written client is not |
| `src/store/chat.ts` | 6,250 | One Zustand store for messages, streaming, tool-result clamping, buckets |

Go does **not** require this. Methods of the same type may live in many files in the same package. `net/http` does it. We already do it for REST (`rest_tasks.go` is `restAPI` methods that used to belong in `rest.go`). The missing rule is: **the next function goes in the file whose one-sentence job matches, not the file that already compiles.**

## How to split (same package, same type, new file)

A file has a one-sentence job. If you need “and” to describe it, it is two files.

Do not split by size (“take the bottom 4,000 lines”). Split by **lifecycle or resource**:

- *Construct / run / stop* vs *idle* vs *wire dependencies* vs *emit events* vs *delegation policy*
- *Mux + JSON helpers* vs *sessions HTTP* vs *agents HTTP* vs *uploads HTTP*

The type stays. The package stays. Only the filename changes. Callers do not change. Tests move with the functions they cover (`loop_idle_test.go` next to `loop_idle.go`).

**First cuts, named from the files as they are today**

`pkg/gateway/rest.go` (11,008 lines, 151 functions). Same package, same `restAPI` type. Banners already mark the cuts (`// --- Sessions ---`, `// --- Agents ---`, …). Tasks / workspaces / plans / auth / onboarding **already left** (`rest_tasks.go`, `rest_workspaces.go`, `rest_plans.go`, `rest_auth.go`, `rest_onboarding.go`, `rest_sign_in.go`) — finish that job.

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

Today: one package `pkg/tools` (64 files), plus `pkg/tools/browser/` (the existence proof), plus `pkg/sysagent/tools` (fossil name — not a chat agent). MCP tools stay wrappers.

Boot already registers tools explicitly (`BuiltinRegistry.RegisterBuiltin`). Keep that. Each **family** package exposes `Register(reg)` so the hub lists ~14 families, not ~80 names. No `init()` if-ladder.

**No `pkg/sysagent` in the target.** That chat agent does not exist. The live *System Agents* category is only Judge + Plan Supervisor (locked, not chat targets, not these tools). Admin tools dissolve into the families below.

### Hub — stays `pkg/tools/`

Interface, registry, compositor, results, catalog, MCP wrapper, ToolSearch. Not a user-facing capability.

Files: `base.go`, `registry.go`, `builtin_registry.go`, `compositor.go`, `result.go`, `types.go`, `validate.go`, `manifest.go`, `general_builtin_catalog.go`, `mcp_tool.go`, `mcp_registry.go`, `tools_tool.go`, `channel_ownership.go`, `normalization.go`, `metadata_guard.go`. Helpers `deps.go` / `registry.go` / `category.go` from `pkg/sysagent/tools` fold in here or into the family that needs them.

### Families (under `pkg/tools/` until the module map moves them)

| Package | Tool names | Files in (today) | Split inside |
|---|---|---|---|
| `browser/` | ~18 browser tools | already `pkg/tools/browser/` | `manager.go` (4,102) and `live.go` (3,590) later |
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
| `memory/` | memory tools | `memory.go`, `memory_rate_limit.go` | — |
| `email/` | email | `email.go` | — |
| `settings/` | `get_config`, `set_config`, `configure_provider`, `list_providers`, `test_provider`, `list_models`, `add_mcp_server`, `remove_mcp_server`, `list_mcp_servers`, `run_doctor`, `get_usage` | `pkg/sysagent/tools/{config,provider,mcp,diag}.go` | — |

That is **15 families + hub**. **`pkg/sysagent/tools` is gone** as a package.

When the module map moves, the family is the unit that moves: `work/` and `chat/` under `pkg/workspaces/`; `delegate/` and `shell/` stay kernel; `browser/` stays a product.

**Extract order:** `delegate/` → `shell/` → `fs/` → `work/` → dissolve sysagent into `agents/` `workspaces/` `connectors/` `skills/` `settings/` → browser file splits → hub shrinks.

`pkg/agent/loop.go` (14,912 lines, 224 functions). Same package, same `AgentLoop` type. Some pieces **move into files that already exist** rather than getting a new `loop_*` name.

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
| `loop_commands.go` | Slash / skill / memory commands | `handleCommand`, `applyExplicitSkillCommand`, `applyMemoryCommandPrompt` |
| `loop_window.go` | Sliding window and model switch | `assembleMessages`, `windowTrim`, `handleModelSwitch`, `selectCandidates` |
| `loop_policy.go` | Tool policy and approval at exec time | `resolveToolPolicyAtExec`, `CheckGrantOrRequestApproval`, `emitPolicyDenyAudit` |
| `loop_search.go` | Dynamic tool load and search promotion | `markToolsLoaded`, `tickSearchPromotionHorizon` |

**Move into siblings that already exist (do not create a fourth copy):**

| Move into | One-sentence job | Takes from `loop.go` |
|---|---|---|
| `turn.go` (already 2,440 lines — will itself need a follow-on split) | Run one turn | `runTurn` (~line 8785–12594, ~3,800 lines **in one function**), `abortTurn`, `typedTurnExit` |
| `goal_loop.go` | Goal forcing during a turn | `evaluateGoalForcing`, `bumpGoalQuestionRoundsUsed`, `goalRubricNoteForBudget` |
| `task_executor.go` | Board/task dispatch | `processTaskDirect`, `processTaskDirectExternalCLI`, `ExecuteBoardTask` |

Accessors (`AuditLogger()`, `GetConfig()`, `SetMediaStore()`, …) stay next to the field they touch, or in a thin `loop_accessors.go` if they clutter `loop.go`. They are not a product concern.

That is **13 new `loop_*` files + 3 moves into existing files**. Split by lifecycle (idle vs wire vs events), not to hit a quota. Do **not** dump all of `runTurn` into `turn.go` — extract helpers first (next subsection).

`src/lib/api.ts` — one file per product module, matching the map above: `api/agents.ts`, `api/sessions.ts`, `api/workspaces.ts`, `api/tasks.ts`. `api.ts` becomes a barrel of re-exports or disappears.

`src/store/chat.ts` — slices, not a 6,000-line store: messages, streaming, tool-result clamp, session bucket.

**Do not** create 80-line micro-files. A 400-line `rest_sessions.go` is the target, not a 40-line file per handler.

### `runTurn` — divide the function, not only the file

`runTurn` is **3,809 lines** (`loop.go` 8785–12594), one function. The loop-split bench showed file cuts do not help it: Grep still needs two 2,000-line Reads. It is already three acts; they were never named (almost no `// ---` banners).

| Act | Lines | Job |
|---|---|---|
| Setup | ~8785–9418 (~630) | Inject context, **register defers**, workspace gate, model switch, assemble history, three pre-turn gates (provider / model / window) |
| `turnLoop:` | ~9419–12431 (**~3,000**) | Each iteration: rate-limit → steer → filter tools → call LLM (stream/retry) → run tools → persist narration → budget → park/abort/continue |
| Epilogue | ~12432–12594 (~160) | Search-promotion tick, late steering (`goto turnLoop`), write assistant text if no streamer |

**Must stay in `runTurn`:** the defers. LIFO order is load-bearing (`clearActiveTurn` before `finalizeStreamer` before `Finish`; the “don’t claim success on error” defer must run first). Moving them into a helper ties them to that helper’s return and breaks cancel/`done` tests. Also stay: the `turnLoop:` label, or replace `goto turnLoop` with `continue` once the loop is one function. Do not scatter `goto` across files.

**Extract** — `runTurn` remains a short conductor (~200–400 lines): defers + `for`.

| New function | Takes |
|---|---|
| `prepareTurnWorkDir` | Workspace re-root / membership refuse (call the existing gate; don’t copy) |
| `runPreTurnGates` | Provider / model / window refusals |
| `assembleTurnMessages` | Initial history + attachments (already almost `assembleMessages`) |
| `prepareIteration` | Hard abort, iteration ceiling, SEC-26 rate limit, steering poll |
| `assembleIterationTools` | Policy filter, ToolSearch force-include, dedup |
| `callTurnLLM` | Stream, empty-retry, progress callback (the ~9800–10800 block). If still huge: stream vs retry inside it |
| `executeTurnTools` | Run calls, persist narration, citations |
| `applyMidTurnBudget` | Window / spend thrash-guard |
| `finishTurn` | Search-promotion tick, transcript fallback |

Do **not** slice `runTurn` by line number (1–2000 / 2001–3800). That would cut the `for` loop and the defer contract.

Files: `loop_turn.go` or `turn.go` holds the conductor; `turn_llm.go`, `turn_tools.go`, `turn_exit.go` hold the helpers. Each helper aimed at a few hundred lines, none over 2,000.

## Giants — final breakdown (meet the 2,000 / 4,000 rule)

Leave a file alone if it is **one job and ≤ 2,000 lines**. That includes `goal_loop.go` (1,658), `rest_plans.go` (1,458), `rest_auth.go` (1,268), `context.go` (1,811), `task/store.go` (1,815), `library.go` (1,891), `telegram.go` (1,056), settings sections ~1,300, `ws.ts` (1,021).

Must split: **> 4,000 lines**, or any size that mixes jobs. Should split: **2,000–4,000** that mix jobs. Every dest file below is aimed at **≤ 2,000**; none may exceed **4,000** except while a giant *function* is still being extracted (`runTurn`).

### Over 4,000 — dest files

| Today | Lines | Becomes (one sentence each) |
|---|---|---|
| `pkg/agent/loop.go` | 14,912 | The 16 `loop_*.go` files already listed. Then **`runTurn` (~3,800 lines, one function)** extracted into `loop_turn.go` (orchestration) + `turn_llm.go` + `turn_tools.go` + `turn_exit.go` so no function needs two Reads. Experiment: `loop_turn.go` was 4,205 until that extract. |
| `pkg/gateway/rest.go` | 11,008 | The 10 `rest_*.go` files already listed (sessions, agents, config, skills, providers, mcp, tools, channels, uploads, status). Mux stays in `rest.go`. |
| `pkg/gateway/gateway.go` | 6,579 | `gateway.go` (`Run`/`RunContext`); `gateway_boot.go` (credentials, souls, roster); `gateway_reload.go` (watcher, `restartServices`); `gateway_sandbox.go` (egress, tool-policy repair) |
| `src/store/chat.ts` | 6,250 | `chatMessages.ts`; `chatStreaming.ts`; `chatTools.ts` (clamp/truncation); `chatBuckets.ts` |
| `pkg/gateway/websocket.go` | 5,614 | `websocket.go` (conn, auth, ServeHTTP); `websocket_chat.go` (`handleChatMessage`); `websocket_cancel.go`; `websocket_replay.go` (attach/replay); `websocket_pump.go` (`writePump`) |
| `pkg/agent/plan_engine.go` | 5,351 | `plan_engine.go` (type, Start/Stop, Tick); `plan_engine_play.go`; `plan_engine_supervise.go` (unmet DoD / signature gate) |
| `pkg/config/config.go` | 5,171 | `config.go` (`Config` + load/save); `config_agents.go`; `config_gateway.go`; `config_retention.go` (memory/compaction helpers) |
| `src/lib/api.ts` | 5,023 | `api/agents.ts`, `api/sessions.ts`, `api/workspaces.ts`, `api/tasks.ts`, `api/plans.ts`, `api/channels.ts`, `api/skills.ts`, `api/config.ts` — barrel re-export only in `api.ts` |
| `pkg/tools/browser/manager.go` | 4,102 | `manager.go` (construct, config); `manager_tabs.go`; `manager_lease.go` |
| `pkg/tools/delegate.go` | 4,026 | `delegate/run.go`, `status.go`, `followup.go`, `park.go` (family package) |

### 2,000–4,000 — dest files

| Today | Lines | Becomes |
|---|---|---|
| `pkg/tools/browser/live.go` | 3,590 | `live.go` (registry, attach/detach); `live_viewport.go`; `live_idle.go` (sweeper / stand-down) |
| `src/components/chat/ChatScreen.tsx` | 3,457 | `ChatThread.tsx`; `ChatComposer.tsx`; `ChatScreen.tsx` (shell) |
| `pkg/agent/task_executor.go` | 3,449 | `task_executor.go` (dispatch/lifecycle); `task_executor_run.go`; `task_executor_judge.go` (claim/verdict) |
| `pkg/coreagent/core.go` | 3,434 | `core.go` (roster literals, All/BaseAgents); `seed.go` (`SeedConfig`); `seed_system.go` (Judge / Plan Supervisor) |
| `src/components/agents/AgentProfile.tsx` | 3,223 | `AgentProfile.tsx` (identity); `AgentProfileTools.tsx`; `AgentProfileModel.tsx` |
| `pkg/gateway/rest_tasks.go` | 3,165 | `rest_tasks.go` (list/get/create/update); `rest_task_runs.go` (start/runs); `rest_task_wire.go` (criteria/DoD mapping) |
| `src/components/browser/BrowserLiveView.tsx` | 3,057 | `BrowserLiveView.tsx` (video); `BrowserLiveControls.tsx`; `BrowserLiveError.tsx` |
| `pkg/gateway/browser_ws.go` | 2,531 | `browser_ws.go` (control); `browser_ws_input.go` |
| `pkg/agent/subturn.go` | 2,453 | `subturn.go` (spawn); `subturn_identity.go` (ADR-032 fields) |
| `pkg/agent/turn.go` | 2,440 | stays until it absorbs `runTurn` helpers; then the `turn_*.go` files above |
| `pkg/tools/shell.go` | 2,406 | `shell.go` (foreground); `shell_bg.go` |
| `pkg/session/unified.go` | 2,367 | `unified.go` (create/read); keep lock in existing `unified_lock.go` — move any leftover lock code there |
| `pkg/channels/manager.go` | 2,180 | shrink by `RegisterFactory` per channel; leftover is start/stop only |
| `pkg/gateway/rest_workspaces.go` | 2,138 | `rest_workspaces.go` (CRUD); `rest_workspace_team.go` (delegation/team) |
| `pkg/gateway/browser_webrtc.go` | 2,108 | `browser_webrtc.go` (signaling); `browser_webrtc_session.go` |

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

**A giant function is its own violation.** Splitting `loop.go` did not help `runTurn` (~3,800 lines): Grep still needs two Reads. Extract helpers until a function fits in one Read. (`funlen` at 120 lines/function is the existing lint; it is not enforced on these files today.)

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
