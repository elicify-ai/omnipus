# Parallel planning — dependency graph, safe parallelism, waves, sizes

Detail behind SKILL.md §1. Design source: sections 5.6, 7.1 and 7.6 of
`docs/internal/design/dev-team-setup-design-2026-09-25.md`.
Last reviewed: 2026-10-29 — landing branches, file-conflict table, riders
Process version: 2026-10-29.1

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
- Give every writer its own worktree on the train's landing branch, with its own file
  area. CHECK and UAT validation are separate instances, never extra authors in that
  worktree.
- The chief keeps the table of files that two streams must not edit at once in the
  coordination repo's `LANDING-PLAN.md` ("File conflicts between parallel streams");
  planning reads it before cutting waves. Packages that share files run in sequence on
  one owner, or one rebases onto the other.
- Add each package's riders (open hardening issues on the same files), confirmed with
  the architect before the design is final.
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
Feature reviews use the landing branch at the cut commit; small/standard work queues
for the combined train review at the cut. Security focus-area review and the whole-epic gate still apply.
If a standard change becomes structural or security-relevant, re-size it before proceeding.

### 5. Branching follows size

- Every size: the train's one landing branch (`landing/<train>`); authors push small
  commits to it directly under the own-tests rule and keep only local scratch work. No
  work-package branches, no PR into the landing branch. New work is coordinated
  directly by team-lead; the feature gate runs once at the cut.
- A standalone feature has its own landing branch, built the same way; the cut PR and
  its single CI run belong to the chief.
- Record each item's readiness and train in the ledger; use
  `knowledge/landing-trains.md` rather than extending an open-ended bundle.

### 6. Name the landing branch and confirm the release branch — never hard-coded

Neither is written into a repo asset except as an example. The chief names
`landing/<train>` at planning and repeats it in every dispatch brief. The release branch
(the integration target the lock covers) changes over time: team-lead confirms it at
engagement start and re-confirms it with the founder at each landing.
