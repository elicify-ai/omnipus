# Parallel planning — dependency graph, safe parallelism, waves, sizes

Detail behind SKILL.md §1. Design source: sections 5.6, 7.1 and 7.6 of
`docs/internal/design/dev-team-setup-design-2026-09-25.md`.
Last reviewed: 2026-10-28 — dev-team roster update (ten roles)

Current ownership, topology and review rules: root `CLAUDE.md` ("Subagent Workflow");
cut and landing procedure: `knowledge/landing-trains.md`.

## The planning loop, step by step

### 1. Build the dependency graph

- Decompose the commissioned work into units small enough to size and dispatch: one
  unit is a coherent deliverable a single dispatch or stream can own.
- For every pair of units, decide: does one need the other's output? Cross-stack work
  has a fixed order — contract first, then the stacks in parallel, then one combined
  review over the combined diff.
- Record the edges. A unit with no unfinished predecessors is a wave candidate.

### 2. Detect safely-parallel work — two checks per pair

**By dependencies:** a unit runs only after its dependencies are complete. Independent
units (no path between them in either direction) can share a wave.

**By files and owner:**
- Name the implementer for each whole feature area and the feature's one test author.
  Keep backend/frontend owners separate only when the larger feature needs that split;
  never split an area by finding. Record the bounded cross-stack commission explicitly.
- Resolve any overlap with the chief before launching: consolidate related scope into
  its existing owner or sequence a true dependency. Different features supply the
  parallel work; a separate worktree alone does not authorize duplicate ownership.
- Give every writer its own worktree and branch. CHECK and UAT validation are separate
  instances, never extra authors in that worktree.
- Width follows independent features, file ownership, true dependencies and capacity
  (`knowledge/capacity-check.md`), not convenience or a made-up numerical cap.

### 3. Cut the graph into waves

- Wave N contains every unit whose dependencies all finished in waves < N.
- Dispatch each wave's independent units together, in one message, so they run
  concurrently.
- Keep the same test author through RED, test fixes and test-side reconciliation.
  A large epic may have different feature owners, not several authors for one feature.
  Commission a fresh CHECK auditor against the combined feature diff.

### 4. Size every unit — three sizes, fixed gates

Use root `CLAUDE.md` ("Change sizes and the review gate") as the single sizing and
reviewer-set table. Record size, combined review scope and named owners in the plan.
Feature reviews use the combined feature branch; small/standard work queues for the
combined train review. Security focus-area review and the whole-epic gate still apply.
If a standard change becomes structural or security-relevant, re-size it before proceeding.

### 5. Branching follows size

- Feature-size: one combined feature branch; the chief merges worker commits directly
  and dispatches its one feature gate. New work is coordinated directly by team-lead.
- Small/standard: short-lived work branches, then the next eligible train candidate.
- Large standalone features retain their own integration candidate and draft PR.
  Workers push their own branches only; candidate CI belongs to the chief.
- Record each item's readiness and intended cut in the ledger; use
  `knowledge/landing-trains.md` rather than extending an open-ended bundle.

### 6. Identify the integration branch — never hard-coded

It changes over time and is never written into any repo asset. Identification loop: the
founder names the current integration branch when commissioning an epic; team-lead
confirms it at engagement start, repeats it in every dispatch brief that needs it, and
re-confirms with the founder at each landing.
