---
name: shared-checkout-wave-clobbers
description: "8 parallel agents in ONE checkout: stash/reset/amend clobbered uncommitted work 5+ times; commit-per-finding + end-of-run symbol-grep is the survival pattern"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-08-08T08:46:11.221Z
---

During the 2026-08-08 14-lens fix wave (8 parallel agents committing directly to
release/v0.1.1 in the single /home/dev/omnipus3 checkout), uncommitted work was
clobbered at least 5 times: one agent's `git stash`/`pop` while debugging a
transient build break swept every sibling's WIP; another's recovery
`commit --amend` without `--only` absorbed ~6 unrelated staged files
(content-neutral but attribution noise); several agents saw their files
silently reverted mid-edit. All work was recovered because agents re-verified
before committing — but only because of that discipline. See also
[[parallel-agents-git-index-race]].

**Why:** `git stash`, `git reset --hard`, `git checkout -- <path>`, and bare
`--amend` are tree-wide operations; in a shared checkout they act on every
agent's uncommitted state, not just the runner's own files.

**How to apply:** in any multi-agent shared-checkout wave, the dispatch brief
must include: (1) commit each finding IMMEDIATELY after its verification
(never batch to the end); (2) `git commit --only <exact paths>` always —
including on `--amend`; (3) NEVER run `git stash`, `reset --hard`, or
tree-wide checkout — on a transient build break from a sibling's in-flight
edit, poll and retry instead; (4) after finishing, re-verify your fix
SYMBOLS exist in the final tree (grep), not just that your commit exists;
(5) the coordinator's final integrity pass symbol-greps every reported fix
before pushing. Worktree-per-agent is the structural fix when disk allows
(~211MB source-only per worktree in this repo; check `df -h /home/dev` first).
