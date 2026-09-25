# Plugin-reviewer dispatch template

Team-lead: paste the block between the two markers at the **head** of every dispatch to a `pr-review-toolkit` reviewer — `code-reviewer`, `code-simplifier`, `comment-analyzer`, `pr-test-analyzer`, `silent-failure-hunter`, `type-design-analyzer`. Fill the two placeholders, then append the task brief below the block. Two errors this template exists to prevent: dispatching a plugin reviewer without it, and pasting the `omnipus-shared-rules` skill body into the dispatch — the reviewer loads the skill itself; only the discipline block and the load instruction travel with the dispatch.

--- paste from here ---

Load the `omnipus-shared-rules` skill with the Skill tool NOW, before any review step. If the load fails, stop and report that failure — never review without the repo rules. End your report with a one-line skills acknowledgement naming what you loaded (e.g. `skills: omnipus-shared-rules`).

## Reviewer discipline (binds this dispatch)

Canonical source: `.claude/templates/agent-discipline.md`.

<!-- agent-discipline:reviewer-rules:start -->
### Reviewer discipline (every reviewer-side role)

- **Every claim is untrue until you have verified it.** Re-check every claim your verdict depends on — read the code, read the CI run, inspect the evidence artifacts — first-hand, in this task.
- **Local re-runs are not the reviewer's tool (N3).** Reviewers verify by reading — code, CI results, artifacts. CI is the authority; the **single** local narrow re-run allowed at a time is performed by team-lead, on request, under the one-at-a-time machine-load rule. A reviewer who wants a re-run asks team-lead for it.
- **A claim you cannot verify is marked UNVERIFIED** in your report and produces a **WARNING, not a block**. team-lead decides (5.5): verify it itself, dispatch a verification, or accept it with the gap stated to the founder. An UNVERIFIED claim never silently passes, and never blocks alone.
- **Every finding carries four things**: a **failure scenario** (this input or this state leads to this wrong result), **evidence** (the `file::symbol` read, the command run), **severity**, and **certainty**. A style preference with no failure scenario is not a finding — it is a comment at most, and it does not gate.
<!-- agent-discipline:reviewer-rules:end -->

End the report with the evidence table — one row per claim: Claim | Evidence (the command plus its exit code plus the key output line, or the `file::symbol` read, or a commit SHA) | Certainty (Verified / Inferred / Unknown) — and a mandatory final **self-check** row: what you re-read and re-ran against this review's done-criteria, and its result.

## code-simplifier only

You are the one plugin reviewer allowed to edit the branch under review. Your edits become part of the change under review: they are covered by the remaining reviewers, or — when you run after them — by a follow-up `code-reviewer` pass on your diff alone. Keep every edit behaviour-preserving, and report each edit in the evidence table like any other claim.

## Scope of this dispatch

Review: `<files / diff / PR scope>`. Work branch: `<branch>`. Done when every claim in scope carries evidence and the report ends with the evidence table, its self-check row, and the skills acknowledgement.

--- paste to here ---
