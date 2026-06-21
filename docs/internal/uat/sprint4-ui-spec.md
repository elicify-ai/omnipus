# Sprint 4 — UI-first IA reframe (workspace-as-project) + Sprint 3 BE

**Spec of record:** `remediation-decisions.md` (IA reframe ~651, workspace detail ~594, M5/M6, Tier-2 Detail #4/#6).
**Builds on:** Sprint 2 unified Task contract. **Branch:** `feat/0.1.0-s34-delivery` (off s2).
**This is a UI-first app — the result must be visually excellent, not utilitarian.**

## Design system — Sovereign Deep (dark-first). Tokens in `src/styles/globals.css`.
- Accent **#d4af37** (Forge Gold). Surfaces **#0a0a0b / #111113 / #1a1a1e / #222228** (0→3). Text #e2e8f0; muted #9ca3af; border #2d3748.
- Status: success #10b981, error #ef4444, warning #EAB308, info #3B82F6, cancelled #F97316.
- Fonts: **Outfit** (headline), **Inter** (body), **JetBrains Mono** (code/mono). `--ease-spring: cubic-bezier(0.34,1.56,0.64,1)`.
- shadcn/ui in `src/components/ui/`; Phosphor icons; Framer Motion; TanStack Router (file routes) + Query; Zustand.
- **No emoji** in stored data or UI chrome (Phosphor only). Chat-first. Tool calls visible+collapsible. Motion: purposeful, spring-eased, never gratuitous.

## The IA reframe (the headline change)
**Mental model:** you are always *inside a workspace*. The sidebar switches project or opens a global library.
App default front door = **My Workspace → Chat → Mia**.

### Sidebar (overlay drawer, pinnable) — reorganized around workspaces
- **WORKSPACES** (primary list): My Workspace ⭐ (default) + named workspaces + Archive section. Click → enter it (lands on Chat). Active workspace highlighted. `[+ New workspace]`.
- **Global libraries:** **Agents** · **Connectors** · **Skills & Tools**.
- Footer: **Settings** · **Sign out** · pin toggle.
- **Removed from sidebar:** the old top-level Chat, Tasks, Calendar, **Automations** (route deleted), Command Center.

### Workspace = a container with 7 TABS (the workspace detail screen)
Route `workspaces.$workspaceId` with tab sub-routes. Tab bar (sticky, Sovereign Deep, Outfit labels, gold active underline):
**Chat** (default) · **Board** · **List** · **Graph** · **Calendar** · **Team** · **Settings**.

1. **Chat** (default landing — chat-first). The existing chat, reframed as the workspace's front view:
   - Agent picker **scoped to the workspace's team** (not all agents).
   - The Sessions/history panel **filtered by the active `workspace_id`** (this project's conversations; a session may appear in >1 workspace if multi-tagged).
   - Delegation renders inline (SubagentBlock for await/background); the existing chat polish from S1 stays.
2. **Board** — task **kanban** by the 7-state lifecycle (inbox/next/planning/in_progress/blocked/done/failed). One-shot tasks. **Delegation roll-ups** (Detail #6): a parent card shows a one-line "▸ N sub-agents running" badge w/ avatars; children nest collapsed; a **depth/altitude toggle** (top-level default → show all). `surface!=='user'` hidden. Quick-capture → inbox; partial tasks can't advance to `next`.
3. **List** — flat, filterable (status/agent/priority/trigger). Same data, table view.
4. **Graph (NEW)** — the **TASK DAG**: tasks as nodes, `blocked_by` as dependency edges, left→right by order. Live status colour per node. Pan/zoom. This is the marquee new view — make it beautiful and legible (auto-layout; clear edge routing; node = title + status chip + agent avatar). Use a graph lib already in deps if present, else a clean SVG/Canvas layout.
5. **Calendar** — scheduled/recurring tasks by fire time (replaces the old Execution "second board"). Month/week; events = once/every/recurring triggers.
6. **Team (NEW)** — **IS the per-workspace DELEGATION GRAPH editor**, not a list:
   - Nodes = agents on this project. **[+ Add agent]** adds a node (= team membership); remove node = remove from team.
   - **Edges = delegation**: drag node→node to connect; click an edge → popover to set **modes** (await/background/task) + **depth**. Click a node → open the **existing `AgentProfile` slide-over** to edit the global agent (cue: *"Editing Mia — applies everywhere she's used"*).
   - Managing the team and managing delegation are the **same action**. Backed by the per-workspace delegation contract (Sprint 3 BE).
7. **Settings** — workspace properties: name, description, repository (opens new tab), owner (read-only), archive, default-team. Auto-save (S1 pattern).

### Agents area (global library) — two views
- **Agents (library):** all agent definitions; **filter [All | by workspace]** to see membership. Keep the S1 roster IA (Main Agents + adaptive Built-in Roster accordion). `+ New Main` / `+ New Subagent` / `+ New External (subagent_3p)`.
- **Workspace Teams:** an index of every per-workspace team → click → that workspace's **delegation graph** (the SAME graph as the workspace Team tab — one source of truth, workspace-scoped).
- **Agent form** (the `AgentProfile` slide-over, reused everywhere): Main ≈ Subagent (same form minus heartbeat+voice which are Main-only, plus inherit toggles for model/fallback/tools/skills/sandbox + required description). subagent_3p hides tools/skills/sandbox, adds CLI executor config (cli·cli_path·env_overrides·cli_args), free-text model. Model selector = grouped-by-provider (S1) → full `{model,provider}` two-field (S3 BE).

## Routing changes
- `index.tsx` (global chat) → redirect to `workspaces/<default>/chat` (or render the default workspace's Chat).
- `tasks.tsx`, `command-center.tsx`, `automations.tsx`, standalone `CalendarScreen` route → folded into workspace tabs; remove `automations` route + sidebar item.
- `workspaces.$workspaceId.tsx` becomes the tabbed container; add tab routes (board/list/graph/calendar/team/settings; chat default).
- Preserve deep-linkability (each tab a route). Keep existing auth layout (`_app`).

## Frontend wave plan (two sub-waves to avoid clobbering the interconnected IA)
**F1 — IA shell (ONE agent, first):** sidebar reorg + the workspace tabbed container + routing + the Chat-as-workspace-view wiring (agent picker scoped to team, sessions filtered by workspace_id). Produces the scaffold the views slot into. Owns: sidebar/layout, `workspaces.$workspaceId.*` routes, the tab bar, routing/redirects.
**F2 — views (parallel agents, after F1):** Board (+roll-ups) · List · **Graph DAG** · Calendar · **Team delegation graph** · Agents library + Workspace Teams. Each owns its view component; all consume the F1 shell + generated contract types.

## Sprint 3 BE (parallel backend; provides contracts F2 needs)
- **Per-workspace delegation** (M5): move delegation graph from per-agent `delegation_policy` (`config.go:752`) → per-workspace (stored with the workspace, keyed by `core_team`). New contract: per-workspace delegation edges + endpoints (`GET/PUT /workspaces/{id}/delegation` or extend Workspace). **Remove the worker-leaf rejection** (`rest_agent_delegation.go:233`) → bounded subagent delegation (depth-capped).
- **Seed Planner / Explorer / Researcher** specialists (`coreagent.SeedConfig`) as Subagents with bounded policies (Planner → Explorer/Researcher).
- **`{model,provider}` two fields** (O3 structural): add `provider` to Agent/AgentCreate/AgentUpdate; config-load migration (split existing single slug); resolution uses explicit provider. Regen.
- **Per-agent heartbeat** (O6, "heartbeat IS a schedule"): per-agent recurring schedule running HEARTBEAT.md (Main only) via the schedules engine — NOT loop.go. Migrate the global value.
- **Form unification + inherit** (O12·1): inherit toggles resolution.
- DoD: contracts regen + `make verify-contracts` clean; scoped tests; `go build` green.

## go-test unblock (parallel; pre-existing + S2 fallout)
- **`pkg/gateway` ordering-hang** (10-min timeout): a serial test's `t.Cleanup(al.Close)` hangs (`al.recapWG.Wait()` blocks on a stuck recap goroutine; leaked bleve scorch loops never closed). Bisect on the ci-omnipus worker; fix `al.Close()` to never block (bound the recap drain) and close the bleve memory indexes on teardown so goroutines don't accumulate. Owns: `pkg/agent/loop.go` Close, `pkg/agent` memory teardown, `pkg/gateway` test helpers.
- **`pkg/sysagent/tools`** real failure from the S2 task-tool rewrite — fix it.

## Quality gates (final, on the Fly runner)
`gofmt` · `lint` · `go-build` · `go-vet` · `verify-contracts` · `typecheck` · `vitest` · `go-test` all green. 7-reviewer gate ×2. No merge to main without human review.
