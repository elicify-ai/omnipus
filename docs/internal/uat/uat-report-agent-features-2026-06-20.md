# UAT Report: Agent Features — Human-Impersonated Exploratory Testing

**Date:** 2026-06-20
**Branch:** `hotfix/v0.1.1`
**Method:** 7 LLM "users" (Claude Sonnet) each drove an isolated headless-Chromium browser against its own
dedicated gateway instance, impersonating a human across the 10 journeys in
`uat-plan-agent-features.md`. 207 screenshots captured. Findings were then verified against source code
(`file:line` citations below).
**Provider/model:** OpenRouter `openrouter/google/gemini-2.5-flash` (real LLM responses).
**Plan deviation:** the plan said "no parallel subagents (one browser)". That applies only to the shared
Playwright MCP. We ran **7 isolated gateways** (`OMNIPUS_HOME` + ports 6061–6067) each with its own
Chromium process — true parallelism, zero file contention. Total wall-clock ≈ 13 min vs ~60 min sequential.

---

## Executive summary

**The core agent features work end-to-end.** Onboarding, chat with streaming, agent CRUD (all 3 types),
handoff, delegation, task creation, scheduling, and settings all function. The LLM path is healthy
(verified PONG round-trip, real token/cost accounting).

**The problems are almost entirely in the UI surface, not the engine** — and they cluster around
*visibility and discoverability of agentic behaviour*:

- The **delegation graph is genuinely good** (renders, readable, editable). But **runtime delegation is
  largely invisible**: when an agent delegates via the natural-language path, the user sees a bare
  "Task create" badge, never the delegated result, and no sub-agent bracket. Denials and depth-limits
  surface only as LLM prose or an unexplained red "Failed" badge.
- The **seeded core roster (Mia/Jim/Ava/Ray) is hidden** behind a collapsed accordion; a fresh user sees
  "No base agents yet."
- **Schedules are undiscoverable** (the Automations screen points at a "Command Center" that redirects to
  Tasks; real creation is buried under Agent Profile → Advanced).
- **Two task vocabularies** (GTD board vs Execution view) collide with no bridge.
- Several **config surfaces are silent no-ops**: external-CLI `cli_path`/`cli_args`/`env_overrides` are
  stored but never used at dispatch; heartbeat is presented per-agent but written globally.

All 5 suspected "known issues" from the plan are **CONFIRMED** in code. No Critical (core-blocking)
defects. The headline is a **delegation-enforcement visibility gap** and a **discoverability** problem,
not engine instability.

> **Test-config caveat (read before triaging):** the first pass ran every gateway with
> `gateway.dev_mode_bypass=true`. This makes the `RequireNotBypass` admin guard return **HTTP 503** on
> `/api/v1/performance`, `/api/v1/devices`, `/api/v1/config/pending-restart`, `/api/v1/security/skill-trust`
> (`pkg/gateway/middleware/bypass_gate.go:35-49`, registered via `adminWrap` at `rest.go:3781,3783,3798,3888`).
> Those 503s are **artifacts of the test config, not product bugs** — they blinded Group 7's first pass to the
> Settings admin surfaces. **Group 7 was therefore re-run against an authenticated gateway
> (`dev_mode_bypass=false`, real admin token); those endpoints returned 200 and the surfaces were exercised
> for real** (screenshots in `screenshots/group-7b/`). The re-run **corrected** the first pass: the Devices
> tab is real/functional (not "coming soon" — that was the 503 placeholder) and the Performance tab renders
> real metrics. The Group 7 findings below reflect the authenticated re-run.

---

## Bug list (consolidated, severity-ranked, code-verified)

Severity: **Critical** blocks a core function · **Major** feature broken/misleading but workaround exists ·
**Minor** cosmetic/edge.

### Major

**MAJ-1 — Seeded core roster hidden; Agents screen shows "No base agents yet."**
The API returns Mia/Jim/Ava/Ray (`type:core, locked:true`), but the **BASE AGENTS** section excludes them
and shows an empty state; the core 4 live in a **BUILT-IN ROSTER** accordion that is **collapsed by
default**. A fresh user concludes they have no agents even though Mia is the active chat agent.
*Intentional bucketing, but the default-collapsed + empty-state copy is misleading.*
Root cause: `src/components/screens/AgentListScreen.tsx:49-55` (buckets: `mainAgents` excludes
`type==='core' && locked`), `:218-224` (empty state), `:362-395` (accordion, default collapsed).
Seen by Groups 1, 2, 4, 7. Evidence: `group-1/15-agents-screen-initial.png`, `group-1/16-agents-built-in-expanded.png`.

**MAJ-2 — Natural-language delegation produces no visible result and no sub-agent bracket.**
Asking Jim "delegate to Ava: …" makes Jim call the async `task_create` tool. The user sees only a green
"Task create" badge; **Ava's output never appears in the chat**, and **no SubagentBlock renders**. The
SubagentBlock only appears for the synchronous `spawn` tool path. This is the single biggest answer to the
plan's key question "is delegation visible?" — for the path the LLM actually chooses, it is not.
Root cause: SubagentBlock keys off `subagent_start` WS frames only (`src/store/chat.ts:2153`,
`ChatScreen.tsx:279,587`); `task_create` is fire-and-forget into a separate task session
(`pkg/tools/task.go:154-218`) and emits no `subagent_start`. Group 4. Evidence: `group-4/05-delegation-response.png`.

**MAJ-3 — Heartbeat is GLOBAL but presented as per-agent.**
The agent profile shows per-agent heartbeat controls, but the values are read from / written to a single
**global** config. Setting heartbeat on one agent changes it for all. This also produces the confusing
Worker footer (MAJ-4 / UX-9). *Confirms known-issue #3.*
Root cause: no `HeartbeatEnabled`/`HeartbeatInterval` on `AgentConfig`; global `Config.Heartbeat`
(`pkg/config/config.go:2214-2217`); GET applies global to every agent (`pkg/gateway/rest.go:1255-1268`);
PUT writes top-level `m["heartbeat"]` (`rest.go:2480-2492`); SPA renders per-agent UI
(`src/components/agents/AgentProfile.tsx:1118-1151`). Group 7.
*Nuance (authenticated re-run):* the heartbeat **instructions** (HEARTBEAT.md content, the Personality-tab
textarea) ARE stored per-agent; only the **enable toggle + interval** are global. So a user editing one
agent's heartbeat *text* is fine, but flipping the *toggle* silently affects all agents.

**MAJ-4 — `PUT /api/v1/agents/worker` 400 fired merely by opening the Worker profile.**
Opening a worker profile auto-saves on hydration, sending `heartbeat_enabled`/`heartbeat_interval`; the
backend rejects any heartbeat on a worker → 400 (silent, console-only). A read action triggers a failed write.
Root cause: `AgentProfile.tsx:401` `useAutoSave` over `formData` which always includes heartbeat fields
(`:371-372`); backend rejects for workers (`pkg/gateway/rest.go:2053-2062`). Seen by Groups 1, 4, 7.

**MAJ-5 — External-CLI config (`cli_path`/`cli_args`/`env_overrides`) is a silent no-op.**
The create wizard exposes all three fields (Step 1), the API stores them, but **dispatch never reads them** —
drivers hardcode the binary name. A user who sets a custom CLI path or args gets no effect and no warning.
*Confirms known-issue #1.*
Root cause: `pkg/agent/external_dispatch.go:60-163` reads only `Executor.CLI`; `RunOptions` has no
path/args fields (`pkg/agent/runner/runner.go:162-183`); `claudeBinName="claude"` (`driver_claude.go:52`),
`opencodeBinName="opencode"` (`driver_opencode.go:37`). Wizard field: `src/components/agents/wizard/Step1Identity.tsx:309-324`.
Group 2. *(Correction: Group 2 reported `cli_args` missing from the wizard — it is present on Step 1, not Step 3.)*

**MAJ-6 — Handoff Mia→Jim: Jim's first turn returned "empty response" error.**
After a successful handoff (header switched to Jim correctly), Jim's first generation showed
"The model returned an empty response." Possibly provider-side (empty completion) rather than an Omnipus
defect — **needs repro**; not reproduced/verified in code. Group 3. Evidence: `group-3/15-after-handoff.png`.

**MAJ-7 — Gateway tab shows the saved-but-not-applied port as live; restart banner is delayed.**
*(Authenticated re-run.)* Changing `gateway.port` and saving immediately updates the status bar to
"Listening on 127.0.0.1:<new>" even though the gateway still runs on the old port, and **no restart banner
appears on the Gateway tab itself** — it only shows after navigating away and back. An operator can be
misled into thinking the change already took effect.
Evidence: after save, `:6067/health` ok but `:6070/health` refused; `pending-restart` API shows
`{key:gateway.port, applied:6067, persisted:6070}`. Group 7b (`group-7b/56c-…`, `group-7b/60-…`).

### Minor

- **MIN-1 — "UNRESOLVED" model badge (and "calls will fail" warning) despite working chats.** The model pill
  shows `UNRESOLVED`, and the profile shows "Model not in any connected provider — calls will fail", even
  after successful LLM calls. Root cause (pinned in the re-run): the seeded agents carry model
  `openrouter/google/gemini-2.5-flash` but the provider's catalog lists `google/gemini-2.5-flash` (no
  `openrouter/` prefix), so the slug-match in `model-selector.tsx:103-104` / `model-validation.ts:88-100`
  fails. The false "calls will fail" warning is the real bug. *Partly test-influenced:* our onboarding
  payload used the prefixed slug; a user picking from the provider list would get the bare slug. Seen by all groups.
- **MIN-2 — Duplicate "(interrupted)" indicator** after `/cancel` (inline + floating). Group 3 (`group-3/18-after-cancel-command.png`).
- **MIN-3 — One-shot schedule vanishes silently after "Run now"** — no toast/status. Group 6 (`group-6/16-run-now-clicked.png`).
- **MIN-4 — Create-agent: "Next" disabled with no inline reason** when required fields empty (button-disabled-only validation). Group 7 (`group-7/69-validation-after-empty-name.png`).
- **MIN-5 — Worker "Tools" tab is mislabeled** — shows Fallback Models, not tool policy. Group 7 (`group-7/61-worker-tools-tab-content.png`).
- **MIN-6 — "Set as default" star changes the system default with no confirmation/undo.** Group 7 (`group-7/20-worker-set-default-attempt.png`).
- **MIN-7 — Task agent-assignment saves silently** (no toast, unlike task creation). Group 5 (`group-5/07-agent-assigned-jim.png`).
- **MIN-8 — No error state on failed admin-settings loads (infinite shimmer).** When `/api/v1/performance`
  etc. fail, the tab shows a perpetual shimmer rather than an error/retry. *In the authenticated re-run the
  Performance tab loads real data fine* (`group-7b/02-performance-tab.png`) — so this is a robustness gap in
  the error path only, not a broken feature. (Original shimmer was the 503 artifact.)
- **MIN-9 — `GET /api/v1/system/cli-detect` 401 on every page load** (the SPA fires it before reading the
  token from localStorage; a direct authed curl returns 200). Also surfaces a spurious "Could not detect
  installed external CLIs" banner on the Agents screen. Group 7b.
- **MIN-10 — pushState error opening the Worker profile** — a malformed double-slash hash URL
  (`//#/agents` → `http:/#/agents/worker`) throws `Failed to execute 'pushState'`. Group 7b.
- **MIN-11 — Worker card text overflow** (model name collides with the "last run" label). Group 7b (`group-7b/14-agents-list.png`).
- **MIN-12 — "Fallback models" block duplicated** across the Basics and Tools tabs of an agent profile. Group 7b.

### Reclassified / not confirmed

- **Trust-graph "click an edge opens a profile" (Group 3, reported Major) → NOT a code bug.** The inline
  edge mode/depth editor IS wired (`src/components/.../DelegationGraph.tsx:321-409,412,545`); node vs edge
  click targets are distinct. The observation was most likely a misclick / React-Flow hit-testing on short
  edges. **UX nuance:** edges are fiddly to select — worth larger hit targets (UX-4).

---

## UX issues

- **UX-1 — Delegation enforcement is not surfaced as structured UI.** Self-delegation denial appears only in
  Jim's prose ("delegation denied… not in my trust set"); a depth-limit failure appears as a bare red
  "Task create — Failed" badge with **no reason**. There is no "Delegation denied: <why>" component.
  Groups 4. (`group-4/12-…`, `group-4/15-…`)
- **UX-2 — Schedules are undiscoverable.** The Automations screen says "manage rules from the Command
  Center", but that text isn't a link and `/#/command-center` **redirects to Tasks**. Real creation lives
  under Agent Profile → Advanced → Schedules (a 4-click, no-breadcrumb path). *Confirms known-issue #4.*
  Group 6 (`group-6/01-…`, `group-6/03-…`).
- **UX-3 — Two task systems collide.** Board uses GTD terms (Inbox/Next/Active/Waiting/Done/Failed); the
  Execution view uses QUEUED/RUNNING/COMPLETED/FAILED and **hides Inbox/Next/Waiting entirely**
  (`ExecutionView.tsx:5-10` columns = next/active/done/failed). No bridge or explanation. Group 5.
- **UX-4 — Trust-graph edge selection is fiddly**; drag-to-connect vs drag-to-reposition is ambiguous, with
  no "edge added" confirmation. Group 3.
- **UX-5 — No delegation editing from the agent profile, and no link to the trust graph.** Group 3.
- **UX-6 — Cancel is undiscoverable.** The `/cancel` command works but is unhinted; the Stop button vanishes
  for fast responses. Group 3.
- **UX-7 — GTD column names opaque; no drag-and-drop; "Start Task" doesn't signal "runs an agent."** Group 5.
- **UX-8 — Cron builder is raw-expression-only**; the preview echoes the raw cron instead of a human-readable
  translation. Group 6 (`group-6/13-…`).
- **UX-9 — Worker profile shows a persistent red "a worker cannot have heartbeat enabled" footer** with no
  corresponding control — a consequence of the global heartbeat (MAJ-3). Group 7.
- **UX-10 — Gateway "unauthenticated access" warning has no in-UI fix** (requires hand-editing config.json). Group 7.
- **UX-11 — Onboarding Step 3 "Complete Setup" disabled-gate is subtle** (only an asterisk signals the
  model is required after a successful connection). Group 1.
- **UX-12 — Settings polish (authenticated re-run).** Performance shows the raw clamp formula
  `[2, min(NumCPU-2, RAM_GB/1.5)]` (opaque to non-technical operators); Devices is functional but lacks a
  "how to pair" CTA; the Gateway token row shows Copy/Rotate with no masked value; Integrations rows for
  SearXNG / Audio Model have neither a status badge nor a configure button. *(Earlier "Devices coming soon"
  and "Performance broken" were 503 artifacts — corrected.)* Group 7b.
- **UX-13 — Handoff has no in-stream transition message** (only the header changes + a collapsed badge). Group 3.
- **UX-14 — Create-task has two similar buttons** ("Create" vs "Create & Start") whose status semantics
  (inbox vs next) aren't explained. Group 5.

---

## Coverage gaps (API/feature exists or expected, UI doesn't expose it well)

- **CG-1 — Per-agent tool policy (allow/ask/deny) has no UI.** Only a global Settings → Security toggle
  (Must-ask-first / Run-freely) exists; the agent profile "Tools" tab shows locked fallback-model config.
  The plan assumed a per-agent allow/ask/deny grid; it isn't present. Group 7.
- **CG-2 — Sandbox profile (workspace/workspace+net/host/off) not editable** for seeded agents (locked); not
  observed for a new custom agent. Group 7.
- **CG-3 — Task dependencies (`blocked_by` / DAG) have no UI** (and likely no API field). Group 5.
- **CG-4 — No delete-task action in the UI.** Group 5.
- **CG-5 — Delegated-task result has no path back into the originating chat** (MAJ-2's structural cause). Group 4.
- **CG-6 — External-CLI config is write-only** (stored, never consumed — MAJ-5).

---

## Answers to the 6 key questions

1. **Does the UI cover all agent functionality?** *Mostly, with real gaps.* CRUD (all 3 types), running,
   chat, handoff, and trust-graph editing are covered well. Gaps: no per-agent tool-policy UI (CG-1), no
   editable sandbox profile (CG-2), external-CLI config is a no-op (MAJ-5), no task dependencies (CG-3), and
   delegated results aren't shown (MAJ-2).
2. **Is the delegation graph good?** *Yes — it's the strongest feature.* It renders cleanly, is readable
   (nodes, mode-labelled edges, Worker-as-leaf), and is **editable** from the UI (drag-to-connect; inline
   edge editor for await/background/task + depth, which IS wired). Weaknesses are interaction polish
   (UX-4) and the lack of a link from chat/profile to the graph (UX-5).
3. **Is delegation enforcement visible?** *Largely no.* Enforcement *works* (self-delegation blocked,
   depth-limit blocked), but the UI surfaces it only as LLM prose or an unexplained red "Failed" badge —
   no structured "denied: why" component, and the depth limit value is never shown (UX-1, MAJ-2).
4. **Two task systems — confusing?** *Yes.* GTD board vs Execution view use different vocabularies and the
   Execution view silently hides Inbox/Next/Waiting; no bridge is offered (UX-3).
5. **Automations read-only dead-end?** *Confirmed.* The Command-Center pointer redirects to Tasks; schedule
   creation is buried under Agent Profile → Advanced and is undiscoverable from Automations (UX-2).
6. **Heartbeat global, shown per-agent — misleading?** *Confirmed.* Per-agent UI, single global write
   (MAJ-3); it also drives the confusing Worker footer (UX-9).

---

## Verification of the 5 suspected known issues — all CONFIRMED

| # | Suspected issue | Verdict | Evidence (`file:line`) |
|---|-----------------|---------|------------------------|
| 1 | `cli_path`/`env_overrides`/`cli_args` not consumed at dispatch | **CONFIRMED** | `pkg/agent/external_dispatch.go:60-163`; drivers hardcode `claudeBinName`/`opencodeBinName` (`driver_claude.go:52`, `driver_opencode.go:37`) |
| 2 | GTD board tasks have no WS frame (polling; WS invalidates wrong key) | **CONFIRMED** | board query polls `refetchInterval:15_000` (`WorkspaceDetailScreen.tsx:90-92`); `task_status_changed` invalidates `['tasks']` not `['board-tasks']` (`src/store/chat.ts:2264`); GTD tasks emit no bus event |
| 3 | Heartbeat global, not per-agent | **CONFIRMED** | global `Config.Heartbeat` (`pkg/config/config.go:2214-2217`); PUT writes top-level `m["heartbeat"]` (`rest.go:2480-2492`) |
| 4 | `/command-center` → `/tasks` dead-end; real schedule UI elsewhere | **CONFIRMED** | redirect confirmed; Automations pointer is non-clickable text; creation under Agent Profile → Advanced |
| 5 | GTD `active` only via `/start` (PUT status=active → 403) | **CONFIRMED** | `contracts/components/schemas/BoardTaskUpdateStatus.yaml:1-4`; `pkg/gateway/rest_board.go:95-98` returns 403 |

---

## Test-config artifacts (NOT product bugs)

- **HTTP 503 on `/api/v1/performance`, `/api/v1/devices`, `/api/v1/config/pending-restart`,
  `/api/v1/security/skill-trust`** — caused by `gateway.dev_mode_bypass=true` in the first pass. These routes
  are `adminWrap`-guarded; `RequireNotBypass` returns 503 under bypass mode
  (`pkg/gateway/middleware/bypass_gate.go:35-49`; `rest.go:3781,3783,3798,3888`). **The Group 7 re-run
  (`dev_mode_bypass=false`, authenticated) confirmed all four return 200** and the surfaces work. Two first-pass
  conclusions were *wrong* because of this artifact and have been corrected: the **Devices tab is functional**
  (not "coming soon") and the **Performance tab renders real metrics** (not broken). The only surviving UI
  finding is MIN-8 (weak error-state handling).
- **Gateway "unauthenticated access enabled" banner** (first pass) is expected under `dev_mode_bypass=true`;
  the re-run confirmed it is correctly absent when `false`. (UX-10 — no in-UI toggle to disable bypass — still valid.)

---

## Methodology & reproducibility

- Harness: `docs/internal/uat/harness/lib.mjs` (Playwright launch + screenshot + console/network capture),
  `mkauth.mjs` (storageState auth), `GUIDE.md` (subagent brief). 7 gateways booted from one binary
  (`/tmp/uat/omnipus`) with isolated `OMNIPUS_HOME=/tmp/uat/home-{1..7}` on ports 6061–6067.
- Group→journey map: G1 onboarding+roster · G2 create-agents · G3 trust-graph+chat/handoff · G4
  delegation-in-action · G5 task-board · G6 schedules · G7 settings+edge-cases. **G7 was run twice** — first
  with `dev_mode_bypass=true` (`group-7/`, admin surfaces 503-blinded), then re-run authenticated with
  `dev_mode_bypass=false` (`group-7b/`, authoritative for Settings). The Group 7 findings above reflect `group-7b`.
- Screenshots: `docs/internal/uat/screenshots/group-{1..7,7b}/` (~235 total).
- LLM model: `openrouter/google/gemini-2.5-flash` (plan named `z-ai/glm-5-turbo`; substituted the
  e2e-validated model — the GLM variant is a dead model on OpenRouter).
