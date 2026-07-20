# ADR-050 — Default Plan, Plan-Scoped Task Board & Portfolio Plans Page

- **Status:** Accepted (interview-ratified 2026-07-20). Documentation ADR — no full plan-spec (operator direction; medium change, most infra already exists).
- **Supersedes (in intent):** the "mixed drill-down board" from the plan-swimlane / drill-down iterations (plans and tasks shared one board). That mixing is the problem this ADR removes.
- **Phase:** treat as v0.3-adjacent (touches the Plan data model), but built on `feature/plan-swimlane-board` since the Plan entity + REST + contracts already live there (ADR-049).

## Context & problem

We iterated the workspace board through: milestones → plan swimlanes → a drill-down board where the **top level mixed plan cards and loose task cards in the same 7 status columns**. Each iteration fought the same tension: **plans and tasks are different kinds of object** forced into one board.

- `[FACT]` A `Plan` (`pkg/plan/plan.go:205`) is a container with its own 5-state lifecycle (`draft/approved/running/done/failed`, `pkg/plan/plan.go:50-54`), goal, DoD, owner, and a `blocked_by` task DAG. A `Task` is a leaf work item with a status, todos, and dependencies.
- `[FACT]` Tasks may currently have an empty `plan_id` ("loose" tasks), so the board had to invent a "Loose tasks" bucket and mix it with plan cards.
- `[INFERENCE]` Putting two object types with different state models in one kanban is the root cause of the "cognitive overload" and "cards don't match" feedback — no card design reconciles a plan (portfolio object) with a task (work item).

**Reframe:** stop mixing. A **board is for tasks only**; **plans get their own page**; and **every task belongs to a plan** so there is never a "loose" set to reconcile.

## Decisions

### D1 — Every task belongs to a plan (soft-enforced)
A task always has a plan. There is no "loose task" concept in the UI. Enforcement is **soft, not a hard DB constraint**: a task created with no `plan_id` is assigned the workspace's **default plan** (D2); pre-existing plan-less tasks are treated as default-plan members (D6). This avoids a brittle backend NOT-NULL migration while giving users the "always in a plan" guarantee.
- **Confidence: High.** Basis: mirrors the default-workspace pattern; soft enforcement is reversible.

### D2 — A default plan per workspace ("Task Backlog"), analogous to the default workspace
Each workspace auto-seeds one **default plan**, titled **"Task Backlog"**, flagged `Plan.Default = true`, **non-deletable**, and resolvable like the default workspace (`pkg/workspace/default.go`'s `IsDefault` + `ResolveDefaultID`). It is a *real* plan (full capabilities — you can give it a goal and run it), it just also carries the default flag and is the fallback home for unfiled tasks.
- **Confidence: High.** Basis: direct analogy to `WorkspaceMeta.IsDefault` (`pkg/workspace/default.go:34`) and `AgentConfig.Default` (seeded by `coreagent.SeedConfig`). New field `Plan.Default bool` (`json:"default"`, matching `AgentConfig.Default`).

### D3 — The Plans page is a portfolio card grid (NOT a board)
The workspace's top-level task surface is a **Plans** page: a responsive grid of plan cards. Each card is a plan-at-a-glance — state chip, title, goal, a **task-status mini-bar** (aggregate of member task statuses), progress `done/total`, owner — and opens that plan on click. The **default plan is pinned first** with a ★/"default" marker. Plans are grouped/sortable by lifecycle state (running first).
- **Confidence: High.** Basis: monitoring use case (agents run plans, human oversees); ux-psychology dashboard rules (chunk by state, surface health, recognition-over-recall). Rejected: a plan-state kanban (we're moving *away* from boards for plans).

### D4 — Plan detail = plan-scoped task Board / List / Graph
Opening a plan shows a **plan header** (goal · state · progress · owner · actions) above the plan's **tasks**, with a view switcher **Board / List / Graph** — all scoped to that plan. The **Board is a proper kanban** (tasks only, 7 status columns *with* vertical column dividers restored, full-height). Graph = the plan's DAG (drag-to-connect deps, ADR-049 follow-ups). This unifies the previously-separate Board/List/Graph tabs into one plan-scoped view stack.
- **Confidence: High.** Basis: `WorkspaceTasksTab` already renders Board+List via a `mode` prop; `WorkspaceGraphTab` already reads the shared `activePlanId` scope.

### D5 — Create-task slide-out gains a Plan selector (+ New Plan), smart-defaulted
`CreateTaskSlideOver` gets a **Plan** dropdown listing all plans **plus a "＋ New Plan" entry** (inline-create a plan, then the task lands in it). It is **pre-selected**: to **Task Backlog** when creating from the Plans level, and to **the current plan** when creating from inside a plan's board. So a task always lands in a plan, with one obvious default the user can override in the same panel.
- **Confidence: High.** Basis: `CreateTaskSlideOver` already takes a `planId` prop and posts `plan_id`; this adds an in-form selector + a create-plan affordance.

### D6 — Lazy migration of existing loose tasks
No irreversible bulk migration. Plan-less tasks are surfaced as **members of the default plan** (the default plan's board/list includes tasks whose `plan_id` is empty *or* equals the default plan id). An optional idempotent backfill (`plan_id = defaultPlanId` where empty) can run later; correctness does not depend on it.
- **Confidence: Medium.** Basis: safest path; risk is only cosmetic (a task appears under Task Backlog until explicitly moved). Improvement path: a one-shot backfill once the field enforcement is trusted.

## Contract surface (Constraint #8)

| Type | Change |
|------|--------|
| `contracts/components/schemas/Plan.yaml` | add `default: boolean` (read-only; server-set) |
| `PlanCreateRequest` | (optional) no change — default flag is server-only; operators don't create defaults |
| `TaskCreateRequest.plan_id` | already present; UI now always sends it |

Regenerate `pkg/api/generated/` + `src/lib/api/generated/` via `scripts/gen-contracts.sh`; `make verify-contracts` green (per the 5-step process in CLAUDE.md).

## Consequences

- **Positive:** one object type per surface (Plans grid vs task board) → the card-parity and overload problems dissolve; "every task in a plan" is a clean invariant; the default plan is a familiar pattern (mirrors default workspace).
- **Negative / watch:** the default plan must never be deletable (guard in the delete path, mirroring default-workspace/default-agent protection); the Plans page is a new top-level surface to design + test; the mixed drill-down board code is retired.
- **Rejected alternatives:** (a) mixed plan+task board — tried across three iterations, overloaded; (b) plan-state kanban for plans — re-introduces a board for plans; (c) hard NOT-NULL `plan_id` migration — brittle/irreversible, unnecessary given soft enforcement.

## Next steps (build, no full spec)

1. Contracts: `Plan.default` → regen → verify-contracts.
2. Backend: seed "Task Backlog" per workspace (mirror default-workspace seeding); `Plan.Default` + a resolver; default-plan guard in delete; task-create defaults `plan_id` to the default plan when empty.
3. UI: **Plans portfolio grid** (new) + **plan detail** (header + Board/List/Graph switcher, plan-scoped, column dividers restored); retire the mixed drill-down top level.
4. UI: `CreateTaskSlideOver` Plan selector + "＋ New Plan" + smart default.
5. Tests (unit + the create-flow + default-plan seeding/guard) + live UAT.
