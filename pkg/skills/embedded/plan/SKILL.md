---
name: Plan
description: Break a complex or multi-step goal into an ordered, trackable plan before acting. Use when a request has several steps, dependencies, or unknowns, or when the user asks you to plan, scope, or sequence work.
context: global
---

# Plan

Turn a fuzzy or multi-step goal into a concrete, ordered plan before doing the work.
This is the Orchestrator's default reflex: think first, sequence, then execute.

## When to use (trigger phrases)

- "plan ...", "how should we approach ..."
- "break this down", "scope this out", "sequence the work"
- Any request with multiple steps, dependencies, or unknowns.

## Method

1. **Clarify the goal.** Restate the desired end state in one sentence. If it is
   ambiguous, ask at most 2–3 sharp questions before planning.
2. **Scope the workspace.** If the work is non-trivial or long-running, create a
   dedicated workspace for it (`system.workspace.create`) so artifacts, sessions,
   and tasks stay scoped. Reuse an existing workspace when one fits.
3. **Decompose.** List the concrete steps. Keep each step a single, verifiable unit
   of work.
4. **Order by dependency.** Put steps that unblock others first. Flag anything that
   can run in parallel.
5. **Identify risks & unknowns.** Note what could fail, what needs a decision, and
   what information is missing.
6. **Assign / delegate.** For each step, decide who does it — yourself, a tool, or a
   delegated agent (`spawn` / `subagent` / `handoff`).
7. **Track.** Materialise the plan as tasks (`system.task.create`) so progress
   is visible and nothing is dropped. Link dependencies with `blocked_by` to
   form a DAG the task system can sequence — a task with an open `blocked_by`
   will not be picked up until its predecessor completes.

## Output shape

Present the plan as a numbered list. For each item give: the action, who/what
performs it, and its dependency (if any). End with the single next action to take.

## Planning checklist (use BEFORE `create_plan`)

Work through every item. If you cannot answer one, resolve it before creating
the plan — a missing answer becomes a livelock mid-execution.

### 1. Ingest the inputs

- [ ] **Prompt.** The user's request, restated in one sentence as the goal.
- [ ] **Goal record.** The compiled Definition of Done (DoD) — the acceptance
      criteria the plan Judge will evaluate. Every criterion must be
      machine-checkable or behavior-scannable (D9); reject "feels maintainable"
      at compile time, never mid-run.
- [ ] **Reference documents.** Specs, design docs, prior session transcripts,
      linked issues. Name them in the plan's `rationale` so a reviewer (or a
      delegated researcher) can trace the decomposition back to its sources.

### 2. Decompose into a DAG

- [ ] Each member is a single, verifiable unit of work (one concrete deliverable).
- [ ] Dependencies are wired via `blocked_by` so the engine can sequence
      dispatch. A member with an unsatisfied `blocked_by` will not start.
- [ ] The critical path is identified (the longest dependency chain).

### 3. Author per-member criteria + DoD

- [ ] Each member carries its own `criteria` (what "done" means for THIS step).
- [ ] The plan carries a `dod` (what "done" means for the WHOLE plan — the
      Judge evaluates this on the all-terminal state).
- [ ] Prefer `check` (machine) and `behavior` (scan) criteria over `prose`
      (self-attested). Prose criteria are weighted lowest by the evidence ladder.

### 4. Persist the rationale

- [ ] Set the plan's `rationale` field: WHY this decomposition, what alternatives
      were considered, what the write-set/stream split is. This is the
      delegable record a reviewer or researcher reads to understand the plan.
      Required when creating via the `create_plan` tool.

### 5. Gaming guard (N-2)

- [ ] The evidence ladder weights deterministic rungs (machine checks, behavior
      scans) over prose self-attestation. Plan for verifiable evidence, not
      narrative.
- [ ] Any artifact produced AFTER an unmet verdict is flagged post-hoc by the
      gaming guard. Do not plan to "fill in" evidence after the fact — plan to
      produce it as a natural output of the work.

## Parallelization chain-of-thought (§3c)

When the plan has two or more steps that could run concurrently, work through
this chain BEFORE authoring the plan. The output determines the write-set
declaration, the isolation rung, and whether a join member is needed.

### Step 1 — Dependency analysis

For each pair of steps, ask: "Must A finish before B starts?"

- If YES → serial edge (`B.blocked_by = [A]`).
- If NO → they are parallel-eligible. Assign them to the same `stream` group
  or to separate streams.

### Step 2 — Write-set contention

For each parallel-eligible step, declare the concrete file paths it will create
or edit (`write_set`). Then check for overlaps:

- **Disjoint write-sets** → streams can run in parallel safely. Each writes
  its own files; no contention.
- **Overlapping write-sets** → the plan-lint will REJECT this at approve
  (G-16). You must either (a) merge the two steps into one serial member,
  (b) partition the overlap into disjoint shards (each stream writes a
  different subset), or (c) declare one step as exploratory (no write-set)
  and accept the highest available isolation rung.
- **Exploratory (unknowable write-set)** → declare no `write_set`. The member
  runs in its own isolated checkout at the highest available rung
  (system-git worktree → go-git clone → subdir, per runtime capability). A
  genuine same-file conflict at merge surfaces as a plan-correction event,
  never silently resolved.

### Step 3 — Mergeability class

Classify the convergence point:

| Topology | Write-set pattern | Join member? |
|----------|-------------------|--------------|
| Serial | N/A (no parallelism) | No |
| Disjoint parallel | Each stream writes different files | No (files coexist) |
| Shard + assemble | Each stream writes disjoint shards of one artifact | YES — one assemble member builds the artifact |
| Exploratory merge | Unknown write-sets, potential overlap | YES — a merge member resolves conflicts |

### Step 4 — Isolation per stream

Assign each parallel stream the highest isolation rung the runtime supports:

1. **system-git worktree** (preferred) — true filesystem isolation.
2. **go-git clone** — degraded when system-git worktree is unavailable.
3. **subdir** — last resort; shared checkout, write-set-scoped commits only.

Never author a join that requires a rung the runtime cannot execute (D10). If
the runtime only supports subdir, plan for write-set-scoped commits and a
merge member that handles contention explicitly.

### Step 5 — Author the join as a first-class member

If the mergeability class requires a join (shard+assemble, exploratory merge):

- [ ] Create a dedicated join/assemble member with `is_join: true`.
- [ ] It has its OWN `criteria` (the Judge verifies the merged result, never
      raw streams — FR-159).
- [ ] It is `blocked_by` every stream it depends on.
- [ ] The plan-lint REJECTS a convergence point without an authored join
      member (G-16, "join-less plan").

## Re-planning checklist (use when the DoD is UNMET)

When the plan Judge returns UNMET and the plan enters `awaiting_supervision`,
work through this checklist to choose the right correction verb. The DoD stays
immutable (G-11) — you never change the criteria; you change the plan's
execution to meet them.

### 1. Diagnose the failure

- [ ] Read the Judge's per-criterion verdict (the unmet reasons in the
      handover text).
- [ ] For each unmet criterion, identify WHICH member's outcome is responsible.
- [ ] Classify each failure:
      - **Wrong outcome** — the member completed (`done`) but its result is
        incorrect. → Consider SUPERSEDE.
      - **Transient failure** — the member failed (`failed`) due to a
        recoverable error (timeout, flaky test, missing dependency). →
        Consider TARGETED-RETRY.
      - **Missing capability** — no existing member addresses this criterion.
        → Consider APPEND (add a new tail member).

### 2. Choose the correction verb

| Situation | Verb | What it does |
|-----------|------|--------------|
| A done member's outcome is wrong | **SUPERSEDE** | Marks the done member's outcome ignored-by-Judge (record stays immutable). Optionally append a replacement tail member. |
| A failed member should be retried individually | **TARGETED-RETRY** | Resets one specific failed member to `next` without a full Stop/Play. |
| The plan is missing a step | **APPEND** | Adds new tail member(s) + their dependency edges to the DAG. |

### 3. Record the falsified assumption

Every correction records a revision entry with a `falsified_assumption` — the
specific assumption from the original plan that turned out to be wrong. This is
the audit trail: "we assumed X, the Judge showed not-X, so we correct by Y."

### 4. Auto-reset awareness

When you APPEND or SUPERSEDE, the engine auto-resets ALL other failed members
(giving them another chance with the correction's changes). Done members are
frozen — they are NOT re-run (unless you SUPERSEDE them). When you
TARGETED-RETRY, only the specified member is reset; others stay as-is.

### 5. Honest exit (G-10)

If the DoD is structurally unreachable after your correction (every remaining
path depends on a frozen outcome that cannot be produced), the engine takes the
honest-exit path — it fails the plan with a descriptive handover rather than
livelocking. Do not try to force a correction that cannot reach the DoD; let
it exit and re-plan from scratch with a Play if the goal is still worth pursuing.

## Play (resume from last commit)

When a plan has been Stopped (failed/stopped_by_user), Play resumes it:

- A new owner-session generation is minted (`resumed_from`).
- Done members are preserved (not re-run).
- Failed/cancelled members resume from the last git boundary commit (the
  gitevidence checkpoint). If no commit is available (degraded rung, fresh
  workspace), they get a fresh attempt.
- JudgeRounds reset to 0.

Use Play when the plan was stopped prematurely or when a correction is too
large for the append-only tail (a full re-plan is needed). Do NOT use Play for
individual member retries — that is TARGETED-RETRY.

## Anti-patterns

- Do not start executing before the plan is stated.
- Do not produce a plan with steps you cannot verify are done.
- Do not bury the critical path under low-value busywork.
- Do not author parallel streams with overlapping write-sets (plan-lint rejects).
- Do not author a convergence point without a join member (plan-lint rejects).
- Do not plan prose criteria when a machine check is possible (gaming guard, N-2).
- Do not change the DoD after creation — it is immutable. Correct the execution,
  not the criteria.
- Do not create a forked "Planner" agent — this skill is the planning behavior.
  Any `create_plan`-granted agent reuses it (BOM, FR-146).
