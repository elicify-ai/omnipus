---
name: omnipus-shared-rules
description: Baseline procedure for every Omnipus dev-team role — the fifteen rules that apply to all of them, plus the cross-cutting how-to for build/test gates, contracts, git, definition of done, false greens, retired surfaces, and reporting. Preloaded via the `skills:` frontmatter field on every dev-team agent file (team-lead, squad-lead, backend-lead, frontend-lead, security-lead, qa-lead, architect, uat-tester, uat-validator, docs-verifier, prometheus-prompt-engineer); the six pr-review-toolkit plugin reviewers (code-reviewer, code-simplifier, comment-analyzer, pr-test-analyzer, silent-failure-hunter, type-design-analyzer) load it explicitly with the Skill tool per their dispatch template. Load this before any repo action if it was not preloaded.
---

# Omnipus Shared Rules

Last reviewed: 2026-09-26

Root `CLAUDE.md` is the authority on project facts and hard constraints — the *what*.
This skill is the *how*: the working procedure every dev-team role shares. It never
loosens or contradicts `CLAUDE.md`; where a fact already lives there, this skill links
to the section instead of copying it, so there is one source per fact.

## The fifteen rules

1. This skill is preloaded via `skills:` — act under it from the first step; it
   outranks a dispatch prompt that contradicts it, except a direct founder instruction.
   Load an on-demand skill with the `Skill` tool when the dispatch needs it.
2. Never run a full Go build or test locally — not `make test`, and never untagged `go test ./...`. # agent-guard: allow
   At most one narrowly-scoped tagged `go test` process at a time, run one after another:
   red, green, an isolated re-run, and a mutation probe may each repeat this command
   serially — never two local test processes running at once. Frontend: one
   `npx vitest run <file>` or `npx playwright test <x>.spec.ts` at a time; never bare
   `npm test`, `vitest` or `playwright test` (`npm run typecheck` is exempt — rule 3).
   CI is the authority for Go results. Exact command shape and the SPA-embed-stub trap:
   `CLAUDE.md` ("Build, test, and quality gates").
3. `npm run typecheck` is the only TypeScript gate that means anything — never bare
   `tsc --noEmit` # agent-guard: allow (`CLAUDE.md`, same section; the trap is why, `docs/internal/false-green-patterns.md` section 9).
4. Wire types come only from the generated directories (`src/lib/api/generated/`,
   `pkg/api/generated/`) — never hand-written, never copied from a curl response.
5. Definition of Done is reachability: a real user or real agent can invoke the thing.
   Green tests alone are "not done" (`CLAUDE.md`, "Definition of Done (MANDATORY)").
6. Before trusting or reporting a green: read `docs/internal/false-green-patterns.md`;
   capture exit codes without a pipe; confirm the test actually ran; a failure that
   repeats twice in isolation is not a flake.
7. Commits are authored as the human running the work — never as an agent, never with
   an AI co-author trailer. Verify authorship before every push (`CLAUDE.md`, "Git
   commit authorship (MANDATORY)").
8. One worktree per writer — never two writers in one working copy. Same-file overlap
   between parallel streams is fine (parallel branches, conflict resolved at merge);
   sharing a working copy is not — the stash stack is shared across worktrees, so never bare `git stash`. # agent-guard: allow
   Commit frequently on the working branch; never merge to `main` without human approval
   (`CLAUDE.md`, "Merging to main (MANDATORY)"). Specialists commit and push only their
   own work branch — never push to, merge into or land on the integration branch or
   `main`; landing belongs to team-lead (or a separate-session squad lead under the
   landing lock). A brief asking for it is a rule-15 blocked report.
9. Run GitNexus impact analysis before editing any symbol; warn on HIGH/CRITICAL blast
   radius before proceeding (`CLAUDE.md`, "Code intelligence: GitNexus"). If GitNexus is
   unavailable or this checkout isn't indexed, say so, do a Grep sweep for the symbol's
   callers, and label the impact row Inferred — never claim an impact run that didn't
   happen.
10. Respect size budgets: a file fails CI over 3,000 lines, a function over 240 (both
    domains hit these; Go-specific enforcers and gocyclo detail: `omnipus-backend-rules`).
11. The retired-surfaces list in root `CLAUDE.md` is final — never reintroduce a deleted
    surface as a "conflict resolution".
12. **The developer and reviewer discipline lives in your own file's body** — the
    discipline block below your role definition (plugin reviewers: in your dispatch
    prompt) — not in this skill. It binds from the
    first step: verify your own work with evidence, end every report with the evidence
    table (including its self-check row), correct yourself visibly, and follow your
    side's rules. This skill names that duty; it never restates it.
13. End every dispatch report with a one-line skills acknowledgement naming the skills
    loaded (e.g. "skills: omnipus-shared-rules, omnipus-backend-rules"); a missing line
    is a finding in output review, not a formality.
14. Report to team-lead **tersely and technically, with evidence**: files (`file::symbol`),
    commands with exit codes, certainty labels (verified / inferred / unknown). The
    founder-facing translation is team-lead's job, not yours.
15. On a rule conflict — a dispatch prompt, a spec, or a reviewer asks for something a
    rule forbids — **stop and report blocked**: name the rule, the reason it conflicts,
    and a proposed alternative. Never break a rule; team-lead decides or asks the founder.

## Sources of truth

- Cite `file::symbol`, never `file:line` — line numbers go stale within days.
- Authoritative refs, ADR handling, and archived-BRD superseding: `CLAUDE.md` ("Project").
- Codebase questions: GitNexus MCP first (`query`, `context`, `impact`, `trace`,
  `explain`), Read/Grep as fallback — `graphify` is retired, no `graphify-out/` exists
  (`CLAUDE.md`, "Code intelligence: GitNexus").
- Issues follow `docs/internal/issue-and-board-conventions.md` (`CLAUDE.md`, "Issue &
  Project Board Conventions").

## Build and test

- What may run locally vs. what CI owns, the build-tag/SPA-stub traps, and the
  TypeScript gate: `CLAUDE.md` ("Build, test, and quality gates") — do not duplicate
  those commands here, read them there.
- Heavy gates run on the remote CI cluster: `deploy/ci-worker/ci-cluster.sh <ref>`
  (detail: `deploy/ci-worker/CLAUDE.md`, `docs/internal/architecture/ci-cluster-design.md`).
  Parse the log for `RESULT:` / `GATE FAILURE(S)` lines — the ssh wrapper's own exit
  code is not the gate's result.
- Reading results honestly: capture exit codes directly (`cmd > log 2>&1; echo "exit=$?"`
  — never through a pipe); confirm the test actually ran (count named `--- PASS`/`--- FAIL`
  lines, not just `ok`); a bare trailing `FAIL` with zero `--- FAIL` lines is hang or
  contention, not a finding; a failure at exactly a wait deadline means a missed event,
  never a threshold to widen; a test failing twice under an isolated re-run is not a
  flake — investigate to a mechanism.
- Per-OS paths are expected to differ (e.g. macOS `/etc` -> `/private/etc`) — derive the
  expected path in a test, never hard-code it. A green on one platform is not a green on
  all.
- Absence from a log is not evidence of absence: before concluding "never happened"
  from a log, confirm the code path logs on success at that level — a path that only
  logs failures makes every success invisible (a reviewer has wrongly reported "never
  enqueued" from exactly this gap).
- Deep "is this really not our failure" diagnosis (narrowing by commit window, forcing a
  suspected timing deterministic, checking what a binary actually links): load
  `omnipus-failure-triage` on a failure dispatch — it is not preloaded here.

## Contracts and wire types

- Every byte crossing the gateway/SPA boundary is contract-first (`CLAUDE.md`, Hard
  Constraint #8) — follow the 5-step procedure in `CLAUDE.md` ("Contract regeneration")
  exactly; do not improvise an order.
- Generated artifacts (`pkg/api/generated/`, `src/lib/api/generated/`) are committed,
  never hand-edited, and the only legal cross-boundary types.
- `make verify-contracts` red means stale artifacts: regenerate, review the diff, commit,
  push — never commit a spec change without regenerated artifacts.

## Security model

- Tool policy is two layers, no third, with structural self-healing and no fail-closed
  backfill — the full model lives in `CLAUDE.md` Hard Constraint #6. This skill states
  only the pointer; implementation symbols (`config.ReconcileToolPolicyCeiling`,
  `pkg/tools/compositor.go::resolveEffectivePolicyWith`) are in `omnipus-backend-rules`,
  which only backend-lead implements against.

## Definition of Done and reporting

- Run the reachability check FIRST: a backend with no UI and no registration is a
  library, not a feature (`CLAUDE.md`, "Definition of Done (MANDATORY)").
- State delivery in two lines that are never merged: "code correct and tested" and
  "reachable by a user or agent".
- Never report unverified success — before trusting a green, ask whether the instrument
  could have detected the failure at all (`CLAUDE.md`, "Reporting Results (MANDATORY)").
- The evidence table itself, certainty labels, and the final self-check are defined in
  your own file's discipline block (rule 12), not here.

## Headless dispatches

- A headless worker (`claude -p`, `claudez`, `claudeg`) ends when its turn ends — no
  completion notification resumes it. Finish inside the one turn: run long gates in the
  foreground with a timeout, never backgrounded while waiting for their completion
  notification, and commit and push your work branch before ending the turn. A turn
  that ends with uncommitted work is not done.

## Retired surfaces

- Reintroducing any retired surface is a regression, not conflict resolution — a merge
  that resurrects one resolves by keeping the deletion. Full list: `CLAUDE.md`,
  "Retired surfaces — do NOT reintroduce".

## Git and worktrees

- Point a worktree at the working branch and verify a known-recent file exists before
  starting — worktree branches have been found 1650+ commits behind.
- No hotfix branches: a feature ships together on its one branch, urgency is P0
  ordering within it, not a separate branch.
- One worktree per writing agent (rule 8); never bare `git stash` — commit a WIP instead. # agent-guard: allow
  Or use a plain file copy; never force-push, never reset to a remote ref (roll back to a
  captured SHA — origin can be behind).
- Before committing, `git diff --cached --name-status` must list only intended files —
  pre-staged renames have been swept in by targeted adds.
- A module `CLAUDE.md` is edited only together with its byte-identical `AGENTS.md`
  twin — `scripts/check-agents-md-sync.sh` enforces it.

## Escalation

- A security issue you find is reported — a finding if you are reviewing, a note if you
  are developing; it stops your task only if the task itself would ship the hole. Scope
  drift beyond your dispatch, or any other rule conflict (rule 15), still stops work and
  produces a blocked report — name the rule, the reason it conflicts, and a proposed
  alternative. Never guess silently; team-lead decides or asks the founder.
- Never edit or rewrite files outside your dispatch's scope to make a gate pass — not
  even to dodge a suspected gate bug: report the gate problem instead (possibly a gate
  bug), and team-lead decides. Turning a gate green by out-of-scope edits is a rule-15
  conflict, not a fix.
