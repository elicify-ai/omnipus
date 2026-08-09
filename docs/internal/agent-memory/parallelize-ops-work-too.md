---
name: parallelize-ops-work-too
description: "Operator wants parallel agents for ALL phases — ops/CI/fix work included, not just implementation waves"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-08-07T08:09:16.080Z
---

Operator feedback (2026-08-07, during the #597 CI campaign): "i see you always
delegating to one subagents, can we do going forward do more in parallel please."

**Why:** CLAUDE.md already mandates parallel waves for implementation; the
operator extends that expectation to EVERY phase — CI failure triage, fix
loops, issue filing, housekeeping. A single serial "closer" agent looping on
CI waves left independent work (issue filing, reindexing, doc updates)
queued behind it for hours.

**How to apply:** at every dispatch point, list the independent work items
first, then spawn one agent per item in the same message. Serialize ONLY on
genuine shared resources: the git index (still use `git commit --only` +
disjoint file sets per the parallel-agents-git-index-race memory), a single
CI wave verdict, the shared Playwright browser (never parallelize UI
testing), and the pod's RAM (never two Go test suites at once). Filing
GitHub issues, reindexing GitNexus, writing docs/memories, and watching CI
are all parallel-safe with each other and with a code-fix agent working
disjoint files.
