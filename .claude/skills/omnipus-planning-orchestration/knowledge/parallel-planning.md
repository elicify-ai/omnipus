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
  worktree and branch, and the conflict is resolved at merge time (landing step 3 —
  "merge the latest integration branch", `knowledge/coordination-ledger.md`).
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

### 4. Size every unit — three sizes plus the hotfix lane, fixed gates

| Path | What it is | Gate | Rough effort |
|---|---|---|---|
| **Small** | A typo, a one-line follow-up, a mechanical edit with no design choice. Carries no RED step and is exempt from red-before-green evidence | One code-reviewer pass on the work branch | Minutes to an hour |
| **Standard** | Real implementation work that is not structural: build with tests in the same step, then review | The 3 fixed reviewers: code-reviewer, silent-failure-hunter, pr-test-analyzer | A few hours to a day |
| **Feature** | Structural, security-relevant, cross-tree, or anything needing a spec — the size that runs as a squad | Full RED/GREEN/CHECK plus the 8-reviewer gate (6 plugin reviewers + architect + security-lead) | Days |
| **Hotfix** | Fix-only repair of a red integration branch or a founder-marked live bug — the fourth delivery path, with its own entry test (§4a) | Reproduction first, the whole affected-area test set locally, reviews **after** the merge (§4a) | Minutes to hours |

- **Urgent is not a size and not the hotfix lane**: urgent work is small or standard and
  moves to the front of the dispatch queue. Only the hotfix lane's entry test (§4a)
  opens the fourth path.
- The standard-size reviewers are fixed — never a rotating pick; security-lead sits in
  the feature gate only, plus on demand for changes touching its focus areas.
- Sizing is team-lead's judgement, checked by the same evidence review as everything
  else. A standard change that turns out security-relevant is re-sized to feature.
- **Security fix rounds get two independent reviewers** — security-lead plus a second,
  independent reviewer instance on every round of a security fix; on #638 that pairing
  caught every partial fix across three rounds.

### 4a. The hotfix lane (founder decision 2026-09-26)

This section is the lane's one full statement; every other file points here. It
supersedes, for this lane only, the design's rule that every change lands after its gate
and a per-branch founder yes (design 7.1).

| Aspect | Rule |
|---|---|
| Entry | The integration branch is red, **or** the founder marks a live defect "hotfix". Urgency alone never qualifies |
| Scope | Fix-only: no new behaviour, no design choice, one area. Anything bigger is re-sized to standard or feature |
| Founder yes | **Standing yes** for fix-only merges while the integration branch is red — team-lead merges, then reports. A founder-marked live-bug hotfix still gets a one-line yes before its merge |
| Lander | team-lead. A squad lead that has a fix for a red integration branch hands it to team-lead |
| Feature work | The "no hotfix branches" rule (`omnipus-shared-rules`, "Git and worktrees") is about feature work and still holds; this lane exists only for a red integration branch or a founder-marked live bug |

1. **Reproduce first.** Run the exact failing test or run and record the command, its
   exit code and the failing line — this record goes into the PR body. A failure that
   does not reproduce is not a hotfix yet: it goes to a failure dispatch with
   `omnipus-failure-triage` loaded.
2. **Fix only.** The developer owning the tree (`backend-lead` / `frontend-lead`, with
   `omnipus-failure-triage` loaded) fixes it on a short-lived branch cut from the
   current integration-branch tip. A test edit is allowed only with proof the check did
   not weaken (the coverage moved, or a mutation still fails it); test files stay
   qa-lead's to edit.
3. **Run the whole test set of each affected area locally** — for example every vitest
   file under the touched `src/components/<area>/`, or the one touched Go package —
   never a hand-picked subset; still one test process at a time (shared rule 2). No Fly
   CI run: the GitHub run on the integration branch after the merge is the CI check.
4. **PR, then merge at once.** team-lead opens the PR against the integration branch
   (reproduction record and closing keywords in the body) and merges it immediately
   under the landing lock, through the branch's normal protection (0 approvals, no
   required checks) — never an admin or auto merge, never a push past protection. Log
   the landing only after the merge succeeded, and space merges so one merge's CI run
   is not cancelled by the next: both in `knowledge/coordination-ledger.md`, "The
   landing sequence".
5. **Watch the run.** The lander watches the GitHub run on the integration branch until
   it finishes green. Still red: the next fix or a revert, through this same lane.
6. **Reviews after the merge, in parallel:** `code-reviewer` plus the reviewer closest
   to the failure (for example `silent-failure-hunter` for a swallowed error,
   `pr-test-analyzer` for a test edit). Any test edit needs `pr-test-analyzer`'s
   explicit ruling "no assertion weakened". A fix touching a `security-lead` focus area
   keeps its `security-lead` review **before** the merge (team-lead §9) — the one
   review this lane does not move.
7. **Findings become follow-up PRs** — through this lane while they are fix-only,
   otherwise sized as standard or feature.

### 5. Branching follows size

- Feature-size: own feature branch and own squad, working in its own worktree(s),
  merged into the integration branch when done.
- Small and standard: a short-lived work branch cut from the integration branch.

### 6. Identify the integration branch — never hard-coded

It changes over time and is never written into any repo asset. Identification loop: the
founder names the current integration branch when commissioning an epic; team-lead
confirms it at engagement start, repeats it in every dispatch brief that needs it, and
re-confirms with the founder at each landing.
