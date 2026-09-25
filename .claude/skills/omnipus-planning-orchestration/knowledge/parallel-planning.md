# Parallel planning — dependency graph, safe parallelism, waves, sizes

Detail behind SKILL.md §1. Design source: sections 5.6, 7.1 and 7.6 of
`docs/internal/design/dev-team-setup-design-2026-09-25.md`.

## The planning loop, step by step

### 1. Build the dependency graph

- Decompose the commissioned work into units small enough to size and dispatch: one
  unit is a coherent deliverable a single dispatch or squad can own.
- For every pair of units, decide: does one need the other's output? Cross-stack work
  has a fixed order — contract first, then the stacks in parallel, then one combined
  review over the combined diff.
- Record the edges. A unit with no unfinished predecessors is a wave candidate.

### 2. Detect safely-parallel work — two checks per pair

**By dependencies:** a unit runs only after its dependencies are complete. Independent
units (no path between them in either direction) can share a wave.

**By files:**
- Prefer decomposition into disjoint file trees — the same discipline the v0.1
  implementation plan uses per wave (`docs/internal/v01-implementation-plan.md`).
- A same-file overlap **does not serialise anything**: both streams run, each in its own
  worktree and branch, and the conflict is resolved at merge time (landing step 2).
  Mark every such pair **parallel-merge-later** in the plan so the cost is visible and
  the landing actor expects the conflict.
- Non-negotiable either way: **never two writers in one working copy**. Sharing a
  working copy is what thrashed files in earlier campaigns; parallel branches cost only
  a merge.
- There is no width cap. Only file ownership, true serialization and machine capacity
  bound width — never narrow fan-out for convenience (capacity is checked, not assumed:
  SKILL.md §3).

### 3. Cut the graph into waves

- Wave N contains every unit whose dependencies all finished in waves < N.
- Dispatch each wave's independent units together, in one message, so they run
  concurrently.
- RED test authorship is the one qualified case: the default is ONE qa-lead instance in
  one worktree on the feature's work branch (disjoint test-file trees, immediate-commit
  discipline). Several qa-lead instances in parallel — one per area, each in its own
  worktree on its own per-area branch cut from the feature's work branch — is for large
  epics only; the dispatcher merges each RED pack into the feature's work branch.

### 4. Size every unit — three sizes, fixed gates

| Size | What it is | Gate | Rough effort |
|---|---|---|---|
| **Small** | A typo, a one-line follow-up, a mechanical edit with no design choice. Carries no RED step and is exempt from red-before-green evidence | One code-reviewer pass on the work branch | Minutes to an hour |
| **Standard** | Real implementation work that is not structural: build with tests in the same step, then review | The 3 fixed reviewers: code-reviewer, silent-failure-hunter, pr-test-analyzer | A few hours to a day |
| **Feature** | Structural, security-relevant, cross-tree, or anything needing a spec — the size that runs as a squad | Full RED/GREEN/CHECK plus the 8-reviewer gate (6 plugin reviewers + architect + security-lead) | Days |

- **Urgent is not a size**: urgent work is small or standard and moves to the front of
  the dispatch queue.
- The standard-size reviewers are fixed — never a rotating pick; security-lead sits in
  the feature gate only, plus on demand for changes touching its focus areas.
- Sizing is team-lead's judgement, checked by the same evidence review as everything
  else. A standard change that turns out security-relevant is re-sized to feature.

### 5. Branching follows size

- Feature-size: own feature branch and own squad, working in its own worktree(s),
  merged into the integration branch when done.
- Small and standard: a short-lived work branch cut from the integration branch.

### 6. Identify the integration branch — never hard-coded

It changes over time and is never written into any repo asset. Identification loop: the
founder names the current integration branch when commissioning an epic; team-lead
confirms it at engagement start, repeats it in every dispatch brief that needs it, and
re-confirms with the founder at each landing.
