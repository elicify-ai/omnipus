---
name: pr-target-release-branch-not-main
description: "PRs from feature branches target release/v0.1.1, never main — operator instruction 2026-08-06"
metadata: 
  node_type: memory
  type: feedback
  originSessionId: 9a5cc9d5-94c8-4246-b11e-938e082e3387
  modified: 2026-08-06T06:36:50.582Z
---

**Never open a PR against `main`.** Feature-branch PRs target the release branch
(`release/v0.1.1` as of 2026-08-06), and even that only when the operator asks —
do not open a PR proactively.

**Why:** the operator stated it directly on 2026-08-06 ("no pr, cancel it and
defenatly no pr to main, you do a pr to the realease branch not main") and had me
close PR #551, which had been auto-created against `main` for
`feature/plan-swimlane-board`. `main` is protected and human-review-gated (see
[[merging-to-main-requires-human-approval]] territory in CLAUDE.md); release
integration happens on the release branch first.

**How to apply:** push feature branches freely to run CI — that is safe and is how
backend changes get validated. But do not create a PR unless asked, and when asked,
`--base release/v0.1.1`. Note the repo's `gh` default and the harness's own
"Main branch (you will usually use this for PRs): main" hint are BOTH wrong here;
this instruction overrides them.

Related trap: a PR to a non-default base does **not** auto-close issues on merge —
closure happens when the release branch later merges to `main`, and only if the
keywords ride along in that merge's PR body.
