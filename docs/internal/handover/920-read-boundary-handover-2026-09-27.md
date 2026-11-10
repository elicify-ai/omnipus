# Handover — squad #920 read-boundary consistency (2026-09-27)

Written by the squad lead of session_01R3ZGi2cdSB4q4oAMEuh8pH, on the founder's pause instruction (relayed by team-lead, 2026-09-27T06:37Z; usage limit). For a SUCCESSOR SQUAD LEAD with no memory of that session. Read `.claude/agents/squad-lead.md`, load `omnipus-planning-orchestration` and `omnipus-shared-rules` first.

## GOAL

`feature/920-read-boundary` fully gated (ADR amendments reviewed, spec, RED/GREEN/CHECK, 8-reviewer gate clean), then — with the founder's explicit per-branch yes — landed on the integration branch `release/v0.1.1` under the landing sequence (`omnipus-planning-orchestration` knowledge/coordination-ledger.md). Never push to or merge into `main`. The PR body must say `Closes #920`.

## Status per flow step

| Step | Status | Where |
|---|---|---|
| Founder interview | Done; Decisions D1–D13 FINAL | `docs/internal/specs/read-boundary-consistency-interview.md` (D8–D13 were added in this session and are already in that file) |
| ADR amendments (ADR-081 D4/D11, ADR-092 D9/J2) | Done: one grill (BLOCK, CRIT-001) → founder interview (D8, D9) → one correction round (a809546) | `docs/internal/architecture/ADR-092-shell-permission-modes-review.md` (+ ADR-081 pointer file) |
| Spec | **Approved** (0f47879), plus a dated FR-004 wording correction (b03486b) | `docs/internal/specs/read-boundary-consistency-spec.md` |
| Spec grill round 1 / 2 | Done: REVISE / REVISE, both fixed; founder decisions D11, D12 from round 1; round 2 had no founder questions | `...-spec-review.md`, `...-spec-review-round2.md` |
| RED (qa-lead) | Done, merged into feature branch. Test files only; red evidence per test recorded in its report (see RED findings below) | branch `feature/920-read-boundary-red` |
| Tool Description() text (prometheus) | Done, merged. `TestGrepDescription_Requirements` + `TestReadListDescription_Requirements` PASS on the feature branch (narrow run, exit 0) | branch `feature/920-read-boundary-prompts` |
| GREEN code (backend-lead) | **Incomplete, NOT verified.** Worker was stopped for the pause. Committed: WIP 9dfc4c2 (auto_approve/resolvepath/audit/filesystem/seed/instance edits from an earlier worker cut off by the spend limit, untested), 4159a27 (D13 test, red), d7b6baa (grep single read decision, `grep_scope.go`, D13 skip, FR-010 seam, RED hook hand-over done), a896791 (windows-tools-tests job, platform-support doc, docsref regen). `go vet` of pkg/tools, pkg/agent, pkg/audit on a896791: exit 0. **No test run results exist for GREEN** — no worker report was delivered. Treat every RED test as unknown | branch `feature/920-read-boundary-green` |
| UI text (frontend-lead) | Done: two stale Auto-approve strings corrected, vitest red→green, typecheck exit 0 | branch `feature/920-read-boundary-ui` |
| User docs draft (backend-lead) | Done (tools.md, security.md, goals.md); not yet audited by docs-verifier. Change note dated 2026-09-27 — update if landing is on another day | branch `feature/920-read-boundary-docs` |
| CHECK (second qa-lead) | Not started | — |
| 8-reviewer gate + grill-code | Not started | — |
| docs-verifier audit | Not started | — |
| Draft PR / CI / landing | Not started. Founder has NOT yet been asked about opening a draft PR against `release/v0.1.1` | — |

## Branches (all pushed to origin)

| Branch | Head | Merged into `feature/920-read-boundary`? |
|---|---|---|
| `feature/920-read-boundary` | c4f65b8 (before this note's commit) | — (the feature branch) |
| `feature/920-read-boundary-red` | 2b61449 | yes |
| `feature/920-read-boundary-prompts` | 089fbb1 | yes |
| `feature/920-read-boundary-green` | a896791 | **no** |
| `feature/920-read-boundary-docs` | bdb7e5f | **no** |
| `feature/920-read-boundary-ui` | 86761ef | **no** |

`feature/920-read-boundary` is 0 commits behind `release/v0.1.1` as of 2026-09-26 (d35e386); re-check.

## Open findings (none needs a founder decision today)

1. GREEN completeness is unknown. Still to confirm or do against spec FR-001..FR-032 + D13: `AutoPin.Class` / RecheckAutoPin skip (D8) and read_file/list_directory → AutoRuns; ReadConfined mounts-only exception (D6, no regex-grant injection, FSOpSend confined); `path.search_roots` registered; grep audit logger wiring (FR-020/021); FR-032 handle close; Windows parsing (DS-4); stale comments (FR-016: resolvepath.go WithReadConfined doc, seed_system.go Plan Supervisor comment, grep.go package doc / `restrict` comment, pkg/agent/instance.go "workspace_id argument"); DS-2 open point — a refused secret-set call under Auto: keep current behaviour and state whether it writes `tool.auto_approved`.
2. RED findings to carry: (a) no seam injects grep's file limit (tests 5, W8 run on shipped limits, 60 s watchdog); (b) S-2.4 as written is unachievable (project shelves live under hidden `.omnipus/skills` / `.claude/skills`, which filegrep prunes) — the test uses the shelf path; spec needs a dated correction at spec-sync; (c) `pkg/tools/auto_approve_test.go` updates under FR-018 — confirmed acceptable by squad lead; (d) DS-2 R1 covered at pin level, not loop level.
3. prometheus leftovers: `context_lines` param text still hard-codes "(0-5)" (no constant exists) — acceptable per DR-G7; `docs/reference/built-in-tools.md` must be regenerated after every merge that changes classes or descriptions (`go run ./cmd/docsref`, check with `scripts/check-docs-reference.sh`).
4. UI: `SecuritySection.tsx` has a code comment above `AutoApproveControl` ("never leaves the workspace/sandbox") that is now stale — code comment only.
5. The new `windows-tools-tests` CI job is not automatically a required check; making it required is a branch-protection setting (founder/operator). Could not verify current required checks (no `gh` in container).
6. docs branch note: security.md avoids the spec's "stricter than the shell" wording for writes (unverifiable); docs-verifier should confirm.
7. GitHub reported 26 Dependabot alerts on `main` at push time — outside #920, flagged only.

## Founder decisions made in this session

All are recorded in the interview record with IDs and dates: D8 (general AutoPin fix), D9 (keep ReadConfined change in #920, dedicated security-lead check), D10 (Plan Supervisor stays unconfined — accepted risk), D11 (grep/list_directory name asymmetry accepted), D12 (scoped Windows CI step sufficient), D13 (grep walk skips /proc, /sys, /dev; never reads device files, pipes, sockets). Also: ADR review model = sonnet for all grills (recorded in each review header).

## Pending questions for the founder

None open. Asks still to come later in the flow (do not pre-ask): (1) "May I open a DRAFT pull request from `feature/920-read-boundary` against `release/v0.1.1` so CI runs?" (2) the per-branch landing ask.

## Exact next 3 steps

1. Resume GREEN: dispatch `backend-lead` on `/home/user/wt-920-green`-equivalent worktree of `feature/920-read-boundary-green` (a896791): review 9dfc4c2 critically, finish open finding 1, then run every RED test narrowly (one tagged `go test -run` at a time, serially) and report a red→green table plus the D13 test. Do not edit test logic.
2. Merge `-green`, `-ui`, `-docs` into `feature/920-read-boundary`; regenerate docsref; vet (linux/windows/darwin). Then CHECK with a *different* `qa-lead` (mutation check + `test-integrity-audit`), and `grill-code` in parallel.
3. 8-reviewer gate (paste `.claude/templates/plugin-reviewer-dispatch.md` at the head of each plugin-reviewer prompt, fill via Python `str.replace`, verify the `agent-discipline:shared-traits:start` marker) + `architect` + `security-lead` (with the D9/D10/D13 checklist in the spec's Security section) + `docs-verifier` on the docs; then ask the founder about the draft PR.

## Traps that burned time

- API spend limit killed two workers mid-task (2026-09-27 ~06:00Z) — uncommitted edits were rescued as WIP commits; always tell workers to commit frequently.
- The container restarted once; the ledger lives inside the container (`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/coordination/`) and is recreated from this note if missing (squad row + GOAL line, formats in coordination-ledger.md).
- A subagent stopped mid-flight cannot be messaged after a restart — its report is lost; re-dispatch with its branch state instead.
- Tests referencing not-yet-existing symbols break the whole `pkg/tools` test build: RED isolated the one unavoidable hook behind a build tag (hand-over already done in d7b6baa).
- New worktrees need the SPA embed stub (`pkg/gateway/spa/index.html`) and their own `npm ci` for vitest.
- No `gh` CLI, no GitNexus, no fly CI cluster, no Playwright browser in this container.

## Evidence

| Claim | Evidence | Certainty |
|---|---|---|
| All #920 branches pushed; heads as listed | `git ls-remote origin refs/heads/<branch>` for all six | Verified |
| red/prompts merged, green/docs/ui not | `git merge-base --is-ancestor <head> origin/feature/920-read-boundary` | Verified |
| GREEN code compiles | `CGO_ENABLED=0 go vet -tags goolm,stdjson ./pkg/tools/ ./pkg/agent/ ./pkg/audit/` on a896791 → exit 0 | Verified |
| GREEN tests pass | not run; worker stopped before reporting | Unknown |
| GREEN worker stopped cleanly, tree clean | TaskStop success; `git status --short` in the green worktree empty apart from the SPA stub | Verified |
| D8–D13 in interview record | `grep -c '^| D'` → 13 rows | Verified |
| Description tests pass on feature branch | narrow `go test -run` exit 0, 2 `--- PASS` | Verified |
| No self check-ins scheduled by this session | this session created no send_later/trigger | Verified |
| Self-check | Re-read the pause instruction's four steps against this note: no new dispatch, everything pushed, note covers GOAL, per-step status, branches+SHAs+merge state, open findings, decisions, pending questions, next 3 steps, traps, evidence | Verified |
