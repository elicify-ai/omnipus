---
name: parallel-agents-git-index-race
description: "Parallel agent waves sharing one checkout can cross-commit each other's files; use `git commit --only`"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-08-03T18:32:32.840Z
---

When several agents work the same checkout concurrently (the mandated wave pattern —
up to 8 parallel dev agents), they share ONE git index. `git add <files>` followed by
`git commit` is **not atomic**: another agent can stage its files into the shared index
between the two calls, and the committing agent then sweeps in work it does not own.

Observed 2026-08-03 during the ADR-057 fix wave: FIX-8 (docs) committed three files
belonging to FIX-1 (`pkg/session/lifecycle_index.go`, `unified.go`, and an untracked
new test file). It caught this in `git show --stat` and repaired it.

**How to apply:** brief every parallel agent to commit with
`git commit --only <their-files> -m "..."`, which snapshots only the named paths and is
immune to concurrent staging. Repair a contaminated commit with
`git reset --soft HEAD~1 && git restore --staged <foreign-files>` then re-commit with
`--only` — `reset --soft` preserves the other agent's changes, so never discard them.
Always verify with `git show --stat HEAD` that every listed file is owned.

**Why:** file-ownership boundaries are what let 30 parallel units land without collision
(see [[mechanism-not-property-defect-class]] for the review discipline that pairs with
this). Ownership discipline in *editing* is defeated at *commit* time unless `--only` is
used — the boundary has to hold at both steps.

Related: this environment's commits must also carry no Claude/Anthropic
`Co-Authored-By:` trailer (CLA gate hard-fails), so a swept-in commit can compound into
an authorship problem too.
