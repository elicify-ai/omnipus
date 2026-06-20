# Post-UAT Remediation — Decision Log

**Started:** 2026-06-20 · **Owner:** Daniel Piatkowski · **Source:** `uat-report-agent-features-2026-06-20.md`
**Status:** living document — we are walking every decision before any implementation.

This log is the single source of truth for what we decide to fix and how. Decisions marked
**LOCKED** are settled; **OPEN** items have a recommendation and await sign-off. Architecture-grade
decisions feed an `/albert` ADR; feature-grade ones feed `/plan-spec`.

---

## Part 1 — Decisions LOCKED

### D1 — Scope: fix all "wrong" and "missing" UI, including v0.3-flagged structural surfaces
We are not limiting to cheap frontend patches. The effort covers correctness bugs, missing UI,
and the structural items (delegation visibility, task model) even where they overlap v0.3.

### D2 — Delegation visibility = Path A (steering), NOT result-plumbing
Background on the three delegation **modes** (`pkg/config/config.go:921-931`):
- **await** → `subagent` tool — synchronous; caller blocks; result folded into the caller's reply.
- **background** → `spawn` tool — async sub-turn; **already renders the SubagentBlock** in chat.
- **task** → `task_create` tool — fire-and-forget; enqueues a task; **no push to the caller** (it polls).

Decision:
- **Steer interactive delegation** ("do this for me now") to **await/background**, which already ride the
  existing spawn implementation and render visibly. Reserve **task** for "file this for later."
  Implementation: tool-description + gating changes; **no contract change**.
- **Render a denied delegation tool-result as a distinct "Delegation denied: <reason>" block** (UX-1),
  independent of mode. Frontend only.
- **Path B (plumb async `task` results back into chat) is REJECTED** — it fights `task` mode's intended
  fire-and-forget semantics. `task`-mode visibility belongs on the **board** (see D3), not in chat.

### D3 — Tasks: ONE unified task model, multiple views; humans and agents are peers
There are currently **two** separate task stores: GTD board tasks (`pkg/boardtask`, `~/.omnipus/tasks/`,
statuses inbox/next/active/waiting/done/failed) and workflow tasks (`pkg/taskstore`,
`~/.omnipus/workflow-tasks/`, statuses queued/assigned/running/completed/failed). This is wrong.

Decision: **collapse to a single task model.** Humans and agents create and work the *same* tasks; the
boards exist to give humans visibility into that shared work. Multiple **views** over one store:
- **Board** (kanban) · **List** (flat/filterable) · **Execution** (what's running now) ·
  **Graph (NEW)** — horizontal DAG of order + dependencies (the `blocked_by` data already exists in both
  stores incl. cycle detection — the graph is mostly a *rendering* of existing data).

Unified status lifecycle (one vocabulary serving human triage + agent execution):

| Human/GTD today | Agent/queue today | **Unified** |
|---|---|---|
| Inbox | — | **Inbox** (captured, untriaged) |
| Next | Queued | **Next** (ready to start) |
| Waiting | — | **Blocked** (unmet dependency) |
| Active | Assigned → Running | **Active** (a human *or* agent is working it) |
| Done | Completed | **Done** |
| Failed | Failed | **Failed** |

Key consequences:
- **"Active" is decoupled from `/start`** — it means *in progress* (human drags a card there, or
  assign+Run hands it to an agent). This removes the "active only via /start → 403" friction (known-issue #5).
- A **delegated `task` becomes a visible card** on the shared board → resolves the task-mode visibility gap
  *without* chat plumbing (closes D2's loop).
- This **subsumes** report items: WS-I (two systems), WS-D (dependency UI), WS-E (board realtime),
  and the task-side of WS-H.
- This **is** the core of the v0.3 Workspaces redesign → land it there; v0.3 is *fresh-build, no
  back-compat*, so there is **no two-stores-to-one migration burden**.
- **Process: capture as an `/albert` ADR** (unified entity, status lifecycle, 4 view definitions,
  human/agent peering, delegated-task visibility) before code.

---

## Part 2 — Decisions OPEN (recommendation given; awaiting sign-off)

> Walk these top-to-bottom. Most are mechanical with a clear recommendation; the **★ product forks**
> are the ones that genuinely need your call.

### O1 — Release routing — **LOCKED (2026-06-20): everything is 0.1.0**
**We are still on 0.1.0 (NOT 0.2/0.3).** 0.1.0 must set the **foundation for everything to avoid large
refactors later** — so the structural pieces (data model, contracts) must be right NOW.
- **Wave 1 (correctness, security, operability)** → **0.1.0.** O2, O3, O4, O5, O7, O11, O12·1, O12·2, O13,
  O14, D2-steering. (O10 closed.)
- **Wave 2 (unified task/workflow model, Tier 2)** → **0.1.0.** The **data model + contracts must be COMPLETE
  and forward-compatible** (so Wave 3 is purely additive). **"Minimum" applies ONLY to the feature *scope*
  (no Wave-3 engine) — NOT to UI quality.** The UI must be **designed WELL** (Sovereign Deep design system,
  real wireframes → real screens for Board/List/Graph/Calendar, create flow, delegation roll-ups, heartbeat).
  Everything shipped in 0.1.0 is done properly; we are not building a throwaway MVP UI. *(Operator: "the ui
  minimal is wrong, we need to design it well.")*
- **Wave 3 (workflow engine)** → **post-0.1.0**, BUT its foundations (action field, extensible `{type,config}`
  triggers, `blocked_by` designed to grow into conditional edges, `surface`, parent linkage, the DSL-shaped
  schema) **must be present in the 0.1.0 data model** so adding the engine later needs no refactor.
- Phase labels v0.2/v0.3 from the old release strategy are **superseded** by this "0.1.0 = foundation" call.

### O2 — MAJ-1: core roster hidden behind collapsed accordion — **LOCKED (2026-06-20)**
Decision:
- **Rename the "BASE AGENTS" section → "MAIN AGENTS"** (matches the `+ New Main` button and the
  Main/Subagent/subagent_3p taxonomy). This section is for **custom Main agents**.
- **Keep the "BUILT-IN ROSTER" accordion** as a separate section holding the core four (Mia/Jim/Ava/Ray).
- **Adaptive expand of the BUILT-IN ROSTER:** expanded when there are **no** custom Main agents (core
  agents visible on first visit); collapsed once the user has custom Main agents (their own agents stay
  prominent).
- **Empty-state copy** for Main Agents: *"No custom Main agents yet. Create one, or use a built-in below."*
- Net: the screen is never misleading — a fresh user sees the core roster immediately, and the Main Agents
  empty state correctly refers only to *custom* agents.

### O3 — MIN-1 + model selector: make provider+model two fields — **LOCKED (2026-06-20): STRUCTURAL**
Root finding: the system is split-brain. **Fallback models, agent defaults, and provider entries already
store `{model, provider}` as two fields, but the PRIMARY agent model is a single combined slug**
(`AgentConfig.Model string`, `Agent.yaml:70`) and so is the **ModelSelector value** (`value: string`).
That single slug (`openrouter/google/gemini-2.5-flash`) can't match the catalog entry
(`google/gemini-2.5-flash`) → the false "UNRESOLVED / calls will fail" warning.

Decision — **STRUCTURAL** (the right end-state; staged delivery):
1. **Make the primary model a `{model, provider}` pair** everywhere — agent config, Agent wire schema,
   and the ModelSelector value — consistent with `fallback_models`. Provider becomes explicit, never inferred.
   → contract change (5-step process), config change, **config-load migration** (existing single-slug →
   split into model+provider), and all 5 selector call sites.
2. **Selector grouping/ordering:** show provider-headed sections even with **one** provider, and **sort**
   models within each group (and groups in a stable order).
3. **Drop the "calls will fail" copy.** With an explicit provider, resolution is unambiguous; only show a
   warning when the model genuinely isn't in the chosen provider's catalog, with softer wording.
- **Staging:** selector grouping/ordering + copy fix can ship early; the `{model, provider}` refactor is the
  contract+migration piece. Phase pinned in O1 (likely v0.2 hardening or v0.3).
- Note: this also makes `agent.model` self-describing, which simplifies routing/validation across the board.

### O4 — MAJ-7 → universal "restart required" UX + self-restart — **LOCKED (2026-06-20)**
Generalized from the port bug to a single pattern for **all 11 restart-gated settings** (`RestartGatedKeys`,
`pkg/gateway/rest_pending_restart.go:36`: sandbox mode/audit/paths, gateway host/port/preview*/public_url/
preview_listener, session DM scope, web-serve warmup). Decision:
1. **New backend self-restart capability** — a UI-triggerable graceful restart (re-exec / clean exit for a
   supervisor). New endpoint → contract change.
2. **Modal on save** of any restart-gated setting: "Gateway restart required" → **[Restart now] [Later]**
   (replaces the old passive banner).
3. **Later defers** — the pending state persists; nothing is lost.
4. **Persistent control in Settings → Gateway tab** — a **"Restart gateway"** button that (a) shows a
   "restart required" notice + the changed keys when a restart is pending, and (b) is always available as a
   maintenance action even when nothing is pending. This is the recoverable home for a deferred restart.
5. **Honest status** — the Gateway tab shows the *running* value and the *saved* value separately
   ("Online :6067 — saved :6070, restart to apply"), never the saved value as if live.
6. **Reattach + success** — on restart the SPA uses existing WS reconnect + `pending-restart` polling to
   detect the gateway going down and coming back, then clears the modal and shows a success toast.
- Routes to ~v0.2 (backend + contract + frontend). Reuses the existing `pending-restart` API + `usePendingRestart`.

### O5 — auto-save everywhere + fix errors on navigation — **LOCKED (2026-06-20)**
Decision:
- **Principle: auto-save everywhere it's feasible** — no explicit Save buttons where auto-save works
  (profiles, settings, forms). Exceptions that still confirm: restart-gated settings (auto-save writes,
  then the O4 modal pops), destructive actions, and multi-step create wizards (commit at the end).
- **Consistent save feedback** — a subtle uniform `Saving… / Saved` indicator per field/panel so auto-saves
  are never silent. *(Resolves MIN-7: task agent-assignment saved with no feedback.)*
- **MAJ-4 fix (inside the auto-save rework):** auto-save **never fires on initial hydration** (only on real
  user edits), and **never sends fields invalid for the entity** (no heartbeat fields for a worker).
- **MIN-9:** defer `cli-detect` (and other authed fetches) until the auth token is present.
- **MIN-10:** fix the malformed `//#/agents/worker` hash URL that throws on Worker-profile open.
- Frontend; no contract change.

### O6 — Per-agent heartbeat (Main only) — **LOCKED (2026-06-20)**
Decision: **heartbeat becomes truly per-agent for Main agents; subagents have no heartbeat.**
- **Main agents** (core + custom Main): own on/off + own interval + own `HEARTBEAT.md`. Move
  `HeartbeatEnabled`/`HeartbeatInterval` from the global `Config.Heartbeat` into `AgentConfig`; add a
  **config migration** for the existing global value. *(The wire contract already exposes these per-agent at
  `Agent.yaml:141-149`, so the change is backend persistence + GET/PUT, not a schema change.)*
- **Subagents** (Subagent / subagent_3p): **no heartbeat** — they run only via delegation. UI hides the
  controls entirely; backend continues to reject (already does).
- **Resolves UX-9** — no more global bleed onto the Worker, and no misleading "cannot have heartbeat" footer.
- Structural backend + migration → ~v0.3 (phase pinned in O1).

**AMENDED (2026-06-20): heartbeat IS a schedule.** A heartbeat is a recurring schedule whose "message" is
the agent's `HEARTBEAT.md`. So heartbeat is implemented as a **built-in *kind* of schedule** in the one
Schedules system (O8/O9) — NOT as separate config fields. An agent's heartbeat = a per-agent recurring
(interval/cron) schedule running its HEARTBEAT.md; per-agent + Main-only still hold (subagents get no such
schedule). Schedules becomes **the single mechanism for all periodic agent runs**, heartbeat included.
Mirrors D3 (one model, typed kinds).

### O7 — WS-G: tool-policy enforcement gap (security) — **LOCKED (2026-06-20)**
Global `sandbox.tool_policies` show as enforced in the REST response but are **dropped before runtime
enforcement** (`agentToolsCfgToPolicy` doesn't pass GlobalPolicies to `FilterToolsByPolicy`,
`pkg/agent/instance.go:596-614`) → an admin's global deny doesn't actually block the tool.
Decision: **fix the loop to merge global + per-agent policy at call time with MOST-RESTRICTIVE-WINS
(`deny > ask > allow`)** — a global deny always blocks; an agent may tighten but never loosen the global rule.
Owner: security-lead. → v0.2. *(This also corrects report CG-1: per-agent tool UI already exists.)*

### D4 — Workflows = DAGs of tasks; automation/schedule/heartbeat dissolve — **DIRECTION SET (2026-06-20), ADR pending**
Evolves D3. Emerging foundation (the real v0.3 core, "Workspaces" → **Workflows**):
- **Everything is a task.** Tasks compose into a **DAG** = a **workflow**.
- **An LLM plan IS a workflow** (a DAG of tasks the agent generated) — unifies planning, execution, and
  human task-management on one substrate; gives observability + human-in-the-loop + replay for free.
- **A workflow is a thin named container** holding trigger(s) + its task graph.
- **Triggers are a separate primitive** from dependencies: a *trigger* starts a workflow (time: once/every/
  cron; or event: another trigger/task-status); a *dependency* orders tasks inside it.
- **Tasks get an `action` type:** `llm` (run an agent — today) + room for `human` (approval gate), `tool`,
  `notify`, `sub_workflow`. Ship `llm` first; design extensible.
- **Automation, schedule, heartbeat all dissolve into workflow shapes:** automation = workflow with a
  trigger; heartbeat = recurring-trigger workflow running HEARTBEAT.md; "schedule" is just a trigger.
  Keep friendly UI labels/entry points over the same model.

**TIER 2 = the "works now" subset — LOCKED (2026-06-20).** Ship a strict subset of D4 so nothing is thrown
away. ONE task store. **IN now:** (1) one unified task entity + one status lifecycle; (2) simple `blocked_by`
dependencies (ordering only) + a Graph view rendering them; (3) `action` field = `llm` only; (4) time
triggers only (once/every/cron) on a task — absorbs today's schedules; heartbeat = a recurring-trigger task
(Main only); (5) delegation Path A (delegated work = a visible task); (6) drop the separate Automations/
Schedules screens — Tasks board is the home. **DEFER to v0.3 ADR:** conditional/branching edges, retries,
fan-out/in, non-LLM action types, event triggers + chaining, the first-class Workflow container,
LLM-plan-as-workflow. Tier 2 is being detailed now (see "Tier 2 detail" section below).

**Tier 2 detail — Detail #1: status lifecycle — LOCKED (2026-06-20).** 7 states:
`inbox → next → planning → in progress → done` (+ `failed`), with `blocked` as an auto side-state.
- **inbox** captured · **next** ready · **planning** agent decomposing (optional in path; light in Tier 2,
  full in v0.3 when it spawns the sub-task DAG) · **in progress** being worked by a human OR agent ·
  **blocked** unmet dependency (auto-set; clears to `next` when deps done) · **done** · **failed**.
- **"in progress" is decoupled from `/start`** — a human can set it manually; assigning+running an agent
  also sets it. (Removes the old "active only via /start → 403" rule.)
- **No separate manual "waiting"** — `blocked` (dependency-driven) covers it.
- Renamed from "active" → **"in progress"** per operator.

**Tier 2 detail — Detail #2: unified task fields — LOCKED (2026-06-20).** One entity merging
`boardtask.Task` + `taskstore.TaskEntity`. Fields: `id`, **`title`** (the name field), `description?`,
`prompt`, **`action`** (NEW enum — `llm` only now; reserves `human`/`tool`/`notify`/`sub_workflow`),
`status` (7-state, Detail #1), `agent_id?` (optional — human-only tasks have none), `priority` (1–5),
`blocked_by[]`, `parent_task_id` (delegation/sub-task link), `workspace_id`, `milestone_id?`,
**`trigger`** (NEW object `{kind: once|every|cron, …}` — folds board `start`/`recurrence` + workflow
`trigger_type`; time-only in Tier 2), `due?` (deadline — separate from trigger), `session_id`, `result`,
`artifacts[]`, `owner`/`created_by`, **`source_channel`/`source_chat_id`** (delegated-task result delivery),
`created_at`/`updated_at`/`started_at`/`completed_at`. New contract type (Constraint #8); replaces both
`BoardTask` and `Task` wire schemas.

**Tier 2 detail — Detail #3: trigger types — LOCKED (2026-06-20).** A task has `triggers` (0..N). Tier 2 types:
- **once** (`at`) · **every** (interval) · **recurring** (cron-based; renamed from "cron") · **manual**
  (no trigger — starts by dragging the card into **in progress**, or Run; for `llm` tasks that runs the agent).
- One-shot = normal board lifecycle; **every/recurring spawn a fresh run each fire** (fresh session + run
  history + pause), reusing the existing per-agent Schedules scheduler engine.
- **v0.3 trigger types (design the `{type,config}` + multi-trigger model for these now):** `on_task`
  (another task hits a status), `on_workflow`, **`on_agent`** (idle/error — **idle = the autonomous-loop
  primitive**), `on_message` (channel match), `webhook`, `on_condition` (threshold). **Dropped:** on-file.
- **Boolean composition (v0.3):** triggers combine with **AND/OR** (a trigger expression, not a flat OR list).

**Tier 2 detail — Detail #4: views — LOCKED (2026-06-20).** Tasks surface has four views:
**Board** (one-shot tasks, kanban by status) · **List** (flat) · **Graph** (dependency DAG) · **Calendar**
(recurring tasks by fire time). The Calendar **replaces the old Execution "second board"** (the UAT's
confusing duplicate).

**Tier 2 detail — Detail #5: `surface` marker (dedicated-UI tasks) — LOCKED (2026-06-20).** A task carries a
**`surface`** field (default `user`). `surface = user` → shows on all four views. A non-`user` surface (first:
`heartbeat`) → **hidden from ALL general views** and rendered only by its owning feature's dedicated UI
(heartbeat → the agent profile). This is a reusable pattern: future system-ish features set their own
`surface`, get the task+trigger engine for free, and never clutter the board/calendar.

**Tier 2 detail — Detail #6: delegation appearance — LOCKED (2026-06-20).** **Everything is a task** (uniform
model); **visibility is a property of the VIEW, not the data.**
- Every delegation (await / background / task) is a task with a **`parent_task_id`** link.
- **Board** = roots + **roll-ups**: a parent card shows a one-line "▸ N sub-agents running" badge with avatars;
  children **nest, collapsed** — they never get top-level cards, so the board stays meaningful. A **depth/
  altitude toggle** lets power users expand ("top-level" default → "show all").
- **Graph view** = the full live delegation tree (every sub-turn in real time) — for watching the machine.
- **Chat** = the inline SubagentBlock for await/background.
- **Transient sub-turns auto-collapse to history** — a short await/background pulses on its parent while
  running, then folds into history; never lingers as clutter.
- **task-mode delegated tasks** also report their result back to the originating chat (`source_channel`/
  `source_chat_id`).

**Agency / backlog model (extends Detail #6) — LOCKED (2026-06-20):**
- An agent **can create tasks freely** — immediate, scheduled, or any trigger type; the LLM has full power
  over *what* and *when* (via the task-create tool + the planning DSL).
- **The only guardrail is assignment** — the **existing delegation rules engine** (trust set + modes + depth)
  decides which agents a task may be assigned to.
- A task **delegated to a Main agent lands in that agent's BACKLOG (inbox) — it does NOT auto-start** (it
  *can* be marked immediate). The target agent **picks it up when its heartbeat fires.**
- This is **instruction-driven, not hardcoded:** the picking behaviour is written in the agent's
  **HEARTBEAT.md** ("check your backlog, decide what to pull"). The **seeded default HEARTBEAT.md for Main
  agents includes this guidance.** Heartbeat thus = the agent's **autonomous work loop** (pairs with the
  `on_agent_idle` loop trigger).
- **Main agents only.** Delegating to a **subagent** is the **immediate await/background** path (subagents
  have no heartbeat, no backlog) — runs when invoked.

**Tier 2 detail — Detail #8: create flow & workspaces — LOCKED (2026-06-20).**
- **One unified create form** (replaces the two old ones): title, prompt, agent (optional), priority,
  trigger (None=manual / Once / Every / Recurring), depends-on (blocked_by), action (`llm`).
- **Landing: everything lands in `inbox`.** Quick-capture (title only) → inbox. **Nothing auto-lands in
  `next`.** Only **fully-captured** tasks can be **moved to `next`** (manual triage); partial tasks can't
  advance until complete.
- **Start semantics:** no trigger (manual) → starts when dragged to **in progress** / **Run**; with a
  trigger → fires itself; **Create & Run now** = create + start immediately (resolves UAT UX-14's confusing
  "Create vs Create & Start").
- **Workspaces** stay as-is for Tier 2 (rework deferred to v0.3). Boards + tasks are per-workspace, so the
  data model carries **`workspace_id` on tasks, workflows, AND triggers** — everything is workspace-scoped.

**=> Tier 2 details COMPLETE (Detail #1–#8).** Next: the v0.2 small O-items, then O1 routing, then the
`/albert` ADR (D4 workflows) + `/plan-spec` (v0.2 bundle).

**KEY OPEN DESIGN QUESTION (biggest risk, v0.3) — edge/condition model:**

**Tier 2 detail — Detail #7: migration — LOCKED (2026-06-20): NONE.** No users yet → **no backwards
compatibility, no migration, no data conversion.** Build the unified store fresh: one entity (Detail #2),
one status vocabulary (Detail #1), one task API. **Delete** the old `pkg/boardtask` + `pkg/taskstore`
stores and their REST endpoints; the new unified Task schema replaces `BoardTask` + `Task` outright (no
compat shims). The status-mapping table is retained only as the *definition* of the new vocabulary, not as
a converter. This makes Tier 2 effectively a fresh build of the unified model — lighter and lower-risk.

**KEY OPEN DESIGN QUESTION (biggest risk, v0.3) — edge/condition model:** `blocked_by` only expresses ordering /
AND-joins. "Different outcomes" needs **typed/conditional edges** (run-on-success / on-failure / on-value),
with the graph kept **acyclic** (retries/loops as bounded constructs, not back-edges). This must be nailed in
the ADR before code.

**Serialization format (v0.3) — DECIDED DIRECTION (2026-06-20):** a workflow/plan is expressed in a
**GitHub-Actions-shaped JSON/YAML DSL** (own vocabulary, but `tasks:` / `needs:` (deps) / `on:` (triggers)
/ `if:` (conditions) / `action:` echo the most LLM-trained workflow syntax that exists) → maximises the
reliability of "LLM authors the DAG." Backed by a **JSON Schema**, generated via **structured output /
tool-use** (not free text). Emit **Mermaid/DOT** *from* the DSL purely to render the Graph view.
**Fallback if a formal standard is preferred: CNCF Serverless Workflow** (closest real spec; less LLM
training data). NOT Airflow (arbitrary code → unsafe generation/sandbox).

**Planning tool (v0.3) — DECIDED DIRECTION:** give the LLM a single tool (e.g. `create_workflow` / `plan`)
that accepts an **entire workflow/plan in the DSL** and materialises the whole DAG (tasks + deps + triggers
+ conditions) in ONE call — and can edit an existing workflow. This **collapses dozens of
create_task/add_dependency tool calls into one** for complex planning, and is the concrete mechanism behind
"an LLM plan becomes a workflow." Pairs with the JSON-Schema structured-output above.

Process: **`/albert` ADR for the Workflow/Task foundation** (entity model, edge/condition semantics, action
types, trigger model, status lifecycle, the views, the DSL + JSON schema, the planning tool). This is v0.3
and **must not block the v0.2 correctness fixes**.

### ★ O8 / O9 — Scheduling & the Automations screen — **SUPERSEDED by D4**
(Automations/schedules/heartbeat no longer separate concepts — see D4. The only near-term action that
survives independently: kill the dead "Command Center" pointer / fix schedule-creation discoverability *if*
any scheduling ships before the D4 workflow model lands.)
- **UX-2:** the Automations screen says "manage from the Command Center" but that redirects to Tasks, and
  real schedule creation is buried under Agent Profile → Advanced.
- Recommendation: make the **Automations screen the real home for schedule CRUD** (agent picker inline +
  launch the schedule form there); fix/remove the dead Command-Center pointer; add human-readable cron
  (UX-8). *Open question: does the Automations "trigger→action rules" concept stay distinct from schedules,
  or merge?* — needs your view.

### O10 — MAJ-6: handoff "empty response" — **RESOLVED (2026-06-20): NOT a bug (weak model)**
Investigated two ways: (1) **Code** — the empty-response path (`pkg/agent/loop.go:5340-5379`) is generic and
agent-agnostic: it retries, falls back to reasoning content, and only emits the
`"model returned an empty response…"` string when the model returns empty *twice*. Nothing handoff-specific.
(2) **Empirical retest on `z-ai/glm-5.2`** — Mia → handoff → Jim works: header switches to Jim, Jim responds,
**no empty-response fallback** (evidence: `/tmp/uat/handoff2/2-after-handoff.png`). The UAT hit it on the
weak `gemini-2.5-flash`. **Action:** none on the handoff path; use a capable default model. *Optional minor:*
soften the fallback copy (less alarming than "provider error or token limit"). **MAJ-6 downgraded/closed.**

### O11 — Chat polish — **LOCKED (2026-06-20)**
Revised per operator:
- **DROP** the in-stream handoff card (UX-13) — the header change + agents naming themselves already convey it.
- **DROP** the Stop-button change (UX-6) — Stop is already easily reachable in the chat.
- **ADD: slash-command autocomplete/dropdown** — typing `/` opens an in-chat command menu with autocomplete
  (the real discoverability fix).
- **NEW BUG — O11b (post-handoff icon misattribution):** after a handoff the agent icon updates correctly for
  NEW messages but **also retroactively changes the PREVIOUS agent's messages** to the new agent's icon.
  Each message must render the icon of **its own author**, not the current active agent. Root cause: bubble
  renders icon from the active agent rather than the message's `author_id`/`agent_id`. Real misattribution
  bug — fix so historical messages keep their author's identity.
- **MIN-2:** de-duplicate the "(interrupted)" indicator (render once). *(retained unless deprioritised.)*

### O12 — going one-by-one
**O12·1 + O12·3 → Agent form unification + subagent inherit — LOCKED (2026-06-20).**
- **Main and Subagent create/edit forms are unified** (same structure/fields).
- A subagent can set **model, fallback models, tools, and skills** to **"Inherit from caller" (the default)**
  or **set them explicitly**. Subagents do NOT auto-inherit — inherit is an explicit (default) option.
- **The inherit-vs-explicit choice is made at CREATION time** — by a **user** (via the form) or by **Ava**
  (the agent-builder LLM) when it creates an agent. NOT a runtime decision.
- **At runtime/delegation time**, an "inherit" facet *resolves* from the calling agent; explicit wins.
- **Data model:** subagent config expresses inherit (`null`/`"inherit"`) vs explicit per facet
  (model / fallback / tools / skills). Small contract addition.
- This makes the Worker "Tools" tab legitimate (inherit toggle + the same tool-policy editor a main has),
  resolving the MIN-5 "mislabel," and puts **fallback models in one place** (the model section), resolving
  the MIN-12 duplication.

**O12·4 → Remove the Schedules list — LOCKED (2026-06-20).** The standalone **Schedules list (Agent Profile →
Advanced) is REMOVED.** Scheduling is now **scheduled tasks** (tasks with triggers) + the **Calendar view** —
no separate schedules surface. **Only the heartbeat stays at the agent level** (its own recurring self-run:
on/off + interval + `HEARTBEAT.md`; a `surface: heartbeat` task shown in the agent profile, hidden from
board/calendar). This makes MIN-3 (silently-vanishing one-shot schedule) **moot** — the list is gone.

**O12·2 → worker-card text overflow (MIN-11) — LOCKED:** trivial CSS fix (truncate model name / give the
"last run" label its own line). Just do it.

Already folded: UX-9 (→O6), MIN-7 (→O5 save feedback), UX-11 (onboarding gate — pending O-onboarding).

### O13 — sandbox profile — **LOCKED (2026-06-20)**
- **Editable for ALL agents, including locked core agents** (NOT read-only) — a core agent's "locked" status
  does NOT extend to its sandbox profile; users can change it. Show the *actual* profile (not vague "Built-in").
- **Editable independent of god-mode** — you can pick a profile regardless of god-mode state.
- **God-mode is a global override that auto-switches the sandbox OFF** when active (regardless of per-agent
  profile). *(God-mode itself is being redesigned — see "God-mode" section below; the current
  `--allow-god-mode` boot-flag gating is considered not good.)*
- **subagent_3p: hide sandbox, tools, AND skills entirely** — the external CLI manages its own isolation,
  tools, and skills.
- **Sandbox joins the inherit facets** for subagents (extends O12·1): a subagent **defaults to inheriting the
  caller's sandbox** (also the safer default — can't run less-confined than its caller); settable explicitly
  at creation. Inherit facets are now: **model, fallback, tools, skills, sandbox.**

### O14 — God-mode redesign — **DECIDED (most of it) 2026-06-20; a few borderline items open**
God-mode = **Claude Code "bypass-permissions" as ONE global switch.** It removes capability restraints and
gives agents full freedom.
- **Effect (locked):**
  - **Every agent's tools flip "ask" → "allow"** (no permission prompts) — the #1 effect.
  - **Kernel sandbox → off** (full host fs + syscalls); **network egress → open**; **shell guard /
    deny-patterns → off**.
  - **Audit logging stays ON** (proposed — confirm).
- **Two enablement routes:**
  1. **UI switch** in **Settings → Gateway**, flipped via **password re-entry** (step-up auth — there are no
     admin/normal-user roles anymore; sensitive settings use password step-up). Interactive path.
  2. **Boot flag / env var** (keep `--allow-god-mode`-style) for **headless** runs (no UI / no password).
  Replaces the *old* model where the boot flag was the ONLY way AND coupled sandbox editing to a restart.
- **Non-destructive override** — god-mode ignores per-agent settings while on, **remembers and restores**
  them when switched off.
- **Step-up-auth pattern (reusable):** high-blast-radius settings require password re-entry to change
  (god-mode is the first; pattern applies to other sensitive settings).
- **Borderline items — LOCKED (2026-06-20):** (a) **audit logging always ON** (never disabled); (b)
  **prompt-injection guard + rate limiting stay ON** in god-mode (they defend external threats, not agent
  freedom); (c) **per-agent sandbox `off` is DROPPED** — per-agent profiles are `workspace / workspace+net /
  host`; "no sandbox" is reachable only via the global god-mode switch; (d) **keep the `nogodmode` hardened
  build** (god-mode compiled out for locked-down deployments). **O14 fully locked.**

---

## Part 3 — Workstream → release map (derived from the above; updated as we lock decisions)

| WS | Theme | Subsumed by / status | Phase |
|----|-------|----------------------|-------|
| A | Stop-lying & broken-on-open | O2,O3,O4,O5,O12 | v0.2 (some v0.1.1?) |
| B | Scheduling discoverability | O8,O9 | v0.2 |
| C | Chat/handoff polish | O11, O10(investigate) | v0.2 |
| D | Task-dependency UI | → D3 (Graph view) | v0.3 |
| E | Board-task realtime | → D3 (one store, one stream) | v0.3 |
| F | Per-agent heartbeat | O6 | v0.3 (if structural) |
| G | Tool-policy enforcement gap | O7 (security) | v0.2 |
| H | Delegation visibility | D2 (Path A) | v0.2 |
| I | Two task systems | → D3 (unify) | v0.3 |

---

## Part 4 — Next steps (after decisions lock)
**No Albert ADR** (operator decision 2026-06-20) — **this decision log is the spec of record.**
1. **O1 — release routing** (last open decision; slots everything into phases).
2. `/plan-spec` directly from this log — per phase (v0.2 correctness bundle; v0.3 workflow/task model).
3. `/taskify` → epic + child issues, phase-routed.
4. Implement in waves (parallel frontend/backend/security agents) → 7-reviewer gate → grill-code.
