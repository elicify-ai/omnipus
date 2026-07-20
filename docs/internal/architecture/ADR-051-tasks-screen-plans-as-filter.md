# ADR-051 — Tasks Screen: Plans-as-Filter over a Combined Task Board

- **Status:** Accepted (interview-ratified 2026-07-20). Documentation ADR — no full plan-spec (operator direction; presentational change over the existing model).
- **Supersedes:** [ADR-050](ADR-050-default-plan-portfolio-plans-page.md) (rejected — its default-plan / per-Planner-backlog / tasks-always-in-a-plan model is NOT built).
- **Branch:** `feature/plan-swimlane-board` (built on the ADR-049 Plan entity + REST + contracts).
- **Grounded in:** operator mockup `Open Blank Landscape.png` (2026-07-20) + `/ux-psychology` review (same day).

## Context

Iterations through swimlanes and a drill-down board over-engineered the plan/task relationship. Decision: **keep the existing data model unchanged** — plans are optional groupings, tasks may be plan-less, `owner_agent_id` stays, nothing about required plans/default plans/backlogs — and **only change the presentation**. The board is workspace-wide; plans become a lightweight overview + FILTER above it.

## Decisions

### D1 — One "Tasks" screen (tab renamed Board → Tasks)
The workspace tab `board` is relabeled **"Tasks"** and becomes the single task surface. **Board / List / Graph collapse into a view switcher on this one screen** (segmented control), replacing the separate `list` and `graph` top-level tabs. Workspace menu: **Chat · Tasks · Calendar · Team**.
- **Confidence: High.** `WorkspaceTasksTab` already renders Board+List via a `mode` prop; `WorkspaceGraphTab` already reads the shared plan scope — combining them on one route is wiring, not new capability.

### D2 — Plans band = overview + single-select FILTER (not navigation)
A horizontal band of plan tiles sits above the board. **Clicking a tile FILTERS the task board below to that plan's tasks** — it does not navigate/drill. A leading **"All tasks"** tile shows the unfiltered board and is the default selected state; re-clicking the active plan tile also clears the filter. The selected tile carries a distinct selected state (Von Restorff — gold ring). The board's heading is **dynamic**: "Tasks" when All, "{Plan title} — tasks" when a plan is selected (Visibility of System Status). Only real plans render (no ghost placeholder tiles); a **＋ New Plan** affordance sits at the band's end/top-right.
- **Confidence: High.** Basis: master–detail filter pattern (Jakob's Law). The filter is client-side over already-loaded tasks (`filterByTag`-style), no new endpoint.

### D3 — Plan tile has an explicit EDIT affordance (because click = filter)
Since a tile's body click filters, editing a plan cannot be the body. Each tile exposes an **edit (pencil) control** on hover plus the **⋯ menu** (Approve / Stop / Clear), both `stopPropagation` so they never trigger the filter. Reuses the unified plan-card shell + the PlanCard control-isolation pattern already shipped.
- **Confidence: High.**

### D4 — Task board is workspace-wide, tasks-only, with column dividers restored
The board shows the workspace's tasks in status columns as a proper kanban — **vertical column dividers restored**, full-height. Plan cards do NOT appear on the board (plans live only in the band). Loose (plan-less) tasks appear normally — no special bucket; the existing model already allows them.
- **Confidence: High.**

### D5 — Remove the `planning` task status (7 → 6)
With plans first-class, the **`planning` task status is redundant and removed**. New canonical task status set: **`inbox` · `next` · `in_progress` · `blocked` · `done` · `failed`** (6). This is a data-model change: the `Task.status` enum (contract + `pkg/task`), `STATUS_ORDER`/`STATUS_LABELS`/`STATUS_COLORS` (`src/lib/statusColors.ts`), board columns, and any status-machine transition tables drop `planning`. **Migration:** tasks currently in `planning` are remapped to **`next`** (nearest "queued, not started" meaning) — idempotent backfill; `derivePlanColumn` and similar helpers drop the `planning` branch.
- **Confidence: Medium.** Basis: `planning` overlaps `next` for a task once plans own the "what's the plan" concern. Risk: any code/tests hardcoding `planning`; a repo-wide sweep is required. Improvement path: confirm no external consumer depends on the value before dropping from the wire enum.

### D6 — Agent (owner) filter combines with the plan filter as AND
The board toolbar's **Agent dropdown** (labeled e.g. "Owner: All") filters by owning agent and **stacks with the plan filter** (plan AND owner). Active filters read as a breadcrumb above the board ("Payments revamp · Owner: Jim"). ＋ New Task lives on the board toolbar.
- **Confidence: Medium.** Basis: flexibility; low cost (client-side). Could ship after D1–D5 if scope needs trimming.

## Contract surface (Constraint #8)

| Type | Change |
|------|--------|
| `contracts/components/schemas/Task.yaml` (`status` enum) | remove `planning` (7 → 6 values) |
| any status-machine / transition schema | drop `planning` |

Regenerate `pkg/api/generated/` + `src/lib/api/generated/`; `make verify-contracts` green. Backend `pkg/task` transition table + validation drop `planning`; a boot/idempotent backfill remaps existing `planning` → `next`.

## Consequences

- **Positive:** minimal model change (presentational); one screen, stable layout; plans clarified as a filter/overview, not a nav maze; board is a real tasks-only kanban again; one fewer status to reason about.
- **Negative / watch:** removing a status value touches contract + backend + SPA + tests — needs a full `planning` sweep and a migration; the plan-filter interaction must be made legible (selected state + dynamic heading + "All" tile) or it regresses to "clever but confusing" (the #1 review finding).
- **Rejected alternatives:** ADR-050's required-plan/default-plan/backlog model (over-engineered); plan tiles as drill-in navigation (the maze we're leaving); keeping Board/List/Graph as separate tabs (fragmented).

## Build outline (waves, no full spec)

1. **Status removal** — contracts (`Task.status` drop `planning`) → regen → verify-contracts; `pkg/task` transitions + backfill `planning`→`next`; SPA `statusColors.ts` + board columns + `derivePlanColumn` + a repo-wide `planning` sweep.
2. **Tasks screen** — rename `board` tab → "Tasks"; combine Board/List/Graph into one screen with a view switcher; retire the `list`/`graph` top-level tabs; restore board column dividers; menu → Chat·Tasks·Calendar·Team.
3. **Plans-as-filter band** — plan tiles (unified card) with selected state + "All tasks" tile + dynamic board heading; edit pencil + ⋯; ＋ New Plan.
4. **Owner filter** — Agent dropdown ANDs with the plan filter; active-filter breadcrumb; ＋ New Task.
5. Tests + live UAT.
