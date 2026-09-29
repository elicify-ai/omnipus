# #904 tool-iteration limit — squad handover (2026-09-27)

Written by the #904 squad lead (separate cloud session) for a successor squad lead who resumes
this squad in a fresh session with none of this session's memory. Paused on the founder's
instruction (usage running out), relayed by the team-lead session.

## GOAL

`feature/904-tool-iteration-limit` fully gated (spec, RED/GREEN/CHECK, 8-reviewer gate clean),
then — with the founder's explicit per-branch yes in the successor's session — landed on the
integration branch `release/v0.1.1` under the landing sequence
(`.claude/skills/omnipus-planning-orchestration/knowledge/coordination-ledger.md`). Never push to
or merge into `main`. Draft PR: **#932** (`feature/904-tool-iteration-limit` → `release/v0.1.1`,
body says `Closes #904`). Run as a goal loop until landed or blocked on a founder decision.

## Key documents

| What | Path |
|---|---|
| Founder interview + Decisions Log **D1–D22** (FINAL, all recorded) | `docs/internal/specs/tool-iteration-limit-interview.md` |
| Spec (Status: Approved; 21 FRs, 45 scenarios, traceability, Reachability) | `docs/internal/specs/tool-iteration-limit-spec.md` |
| Spec review rounds 1 and 2 | `docs/internal/specs/tool-iteration-limit-spec-review.md`, `…-spec-review-round2.md` |
| ADR-066 dated correction | `docs/internal/architecture/ADR-066-context-budget-and-tool-result-routing.md` §14 item 6 |
| User docs | `docs/settings.md` ("Tool calls per turn"), `docs/agents.md`, `docs/operations/configuration.md` |
| Tracked side issue (pre-existing, D21) | GitHub issue **#936** (PUT /api/v1/config + set_config side door for the other Performance fields; one-level merge wipes sibling `agents.defaults` fields) |

## Status per flow step

| Step | Status |
|---|---|
| Founder interview | Done — D1–D19 before the gate, D20–D21 after gate round 1, D22 during round-3 CI triage. All in the interview file |
| ADR | None needed; ADR-066 carries a dated correction |
| Spec + grill-spec rounds 1 and 2 | Done (grill on sonnet, author on the default model). Status Approved. Minor known spec drift for `spec-sync` after landing: the spec says the profile resends the limit on every autosave (not true); D20 raise-never-rewrites; the typed reload-failure reply and "pending apply" state are newer than the spec |
| RED | Done (qa-lead), plus several gap-closing test rounds |
| GREEN | Done: contract, resolver/config/env import, runtime/API, sysagent tools, external-CLI runner, Settings → Performance card + D11/D16 dialog, per-agent field |
| CHECK | Round 1: BLOCK (resolved). Round 2: BLOCK on 2 items (both resolved: stale env-import test fixed in d8f92984d; surviving mutant M1b killed by `TestPerformancePut_DecidingCheck_AgentGainsOwnValueBetweenLists_Drift409`). A **round-3 CHECK has NOT run** on the round-3/round-4 changes |
| 8-reviewer gate | Round 1 (all 8) and round 2 (7; code-simplifier sat out, its commit fa269f792 reviewed clean by code-reviewer) done, all findings fixed. Round 3 (security-lead, code-reviewer, silent-failure-hunter, docs-verifier) done — findings below, being fixed in round 4 |
| Docs | Drafted (backend-lead), audited by docs-verifier (35/38 TRUE, 1 FALSE corrected in 418e1c7a7). The "not applied yet" section will need a re-check after round 4 (pending-apply state) |
| UAT | **Not started.** Provider for UAT: openrouter + `z-ai/glm-5.3-flash` |
| Landing | Not started. No landing lock taken; ledger row `status=in-flight` (container-local ledger — gone after the container is reclaimed; recreate it) |

## Open findings (round 3) and fix owner

| # | Severity | Finding | Owner / branch |
|---|---|---|---|
| 1 | High | Config LOAD failure inside `pkg/gateway/rest_config.go::refreshConfigAndRewireServices` during a Performance save is logged nowhere | backend-lead, `feature/904-r4-be` |
| 2 | High | After a not-applied save, a later unrelated save clears the "not applied" notice while agents still run the old limit → server-side "pending apply" state on GET/PUT /performance (contract-first) | backend-lead (`feature/904-r4-be`) + frontend-lead (`feature/904-r4-fe`) |
| 3 | Medium-high | A registry rebuild that itself fails can still return plain success (only "cannot start / timeout" maps to `performance_reload_failed`) | backend-lead, `feature/904-r4-be` |
| 4 | Medium | Refused own limit lost silently on agent switch before the reply | frontend-lead, `feature/904-r4-fe` |
| 5 | Medium | Real cause of a config-write failure discarded before logging (configWriteFailure) | backend-lead, `feature/904-r4-be` |
| 6 | Important | `PerformanceSection.tsx` `performance-unapplied-notice` says "checked against what is running"; server checks the saved config.json value | frontend-lead, `feature/904-r4-fe` |
| 7 | Low | Malformed reload-failure body loses the changed-field names in the message | frontend-lead, `feature/904-r4-fe` |
| 8 | Info | Credential NAMES (not values) appear in some pre-existing generic config-save logs/replies — outside #904 | none (note only) |

Both round-4 workers were told to stop cleanly, commit WIP and push (see branch table for the
SHAs at the time of writing; re-fetch — they may have pushed a later WIP commit). Their work is
**not yet merged** into the feature branch and has **not been reviewed**.

### Round-4 worker state at pause (both stopped cleanly, pushed)

- **r4-be @ 1f920ac1d (WIP):** items 1, 2 (contract `contracts/components/schemas/PerformancePendingApply.yaml`,
  optional `PerformanceSettings.pending_apply` {stage, changed_fields}; new
  `pkg/gateway/rest_performance_pending_apply.go`; `reloadOutcomeTracker` in
  `gateway_reload.go::runReloadCycle`; PUT /performance uses `reloadAgentsAndConfirm`) and 3
  (`updateConfigJSONLocked` split into `writeConfigJSONLocked` + `applyWrittenConfigLocked`) are
  written; `go vet ./pkg/gateway/` exit 0. **Not done:** red-before-green for items 2 and 3
  (green only), `GOOS=windows go vet`, golangci-lint, gofmt on the final tree. The
  `pending_apply` contract shape was designed by backend-lead and is **not architect-reviewed**.
  Item 2b fixes a pre-#904 bug (failed rebuild returned 200) only for PUT /performance; the other
  four `triggerReloadAndWait` callers (`rest_config.go`, `rest_tool_policies.go`,
  `rest_context_settings.go`, `rest_agents.go`) keep the old behaviour — decide: fix here (Hard
  Constraint #7) or a tracked issue with the founder.
- **r4-fe @ 02fb7b6cf:** items 1–4 done with red→green (pendingApply 4/4, toolIterations 32/32,
  maxToolIterations 16/16), typecheck/eslint/budgets exit 0. For CHECK: one assertion changed in
  `config.performanceWriteError.test.ts` (malformed-body case, per brief); refresh-failure mocks
  in `PerformanceSection.toolIterations.test.tsx` now return `pending_apply`. Open note (not
  fixed): after a page reload during a refresh-stage failure, typing the still-running value is
  skipped as "unchanged" (`handleToolIterChange` → `savedOk`) although config.json holds another
  value.

## CI state on draft PR #932

- Last FULL CI runs were on 5250370f4/844a096bf-era heads. Reds found there were fixed in the
  ci3-be/ci3-fe branches (merged). No full CI has completed on the current head yet (01137a292
  was a docs-only push; only CLA + label ran). The next code push triggers the full run.
- **Not this PR's (D22, founder: defer, does not block the merge):**
  `tests/e2e/steered-session-reachability.spec.ts` (fails on `release/v0.1.1` itself, base job
  108508262384); llm-light chat Bug-3 test (provider 404 "No endpoints found that support image
  input"); llm-agents subagent (a) (unanswered ADR-092 approval dialog). Record them on the PR
  when asking to land. No other known non-#904 red (no "#891" item was seen in this session's CI
  triage — check if the team-lead meant a different item).
- CodeQL: the clear-text-logging and log-injection alerts were addressed in code (logsafe
  helpers, fixed cause classes); confirm on the next scan. The code-scanning alerts API returns
  403 to agents — alert details come from check-run annotations only.
- "Claude Approvals" check: not observed on this repo's PR.

## Branches

| Branch | Head | Merged into feature branch |
|---|---|---|
| feature/904-tool-iteration-limit | 01137a292 (+ this handover commit) | — (the feature branch; also has release/v0.1.1 merged in at b1eba55bc) |
| feature/904-r4-be | 1f920ac1d (WIP; parent 7f4b541b8 = pending_apply contract commit) | **no** |
| feature/904-r4-fe | 02fb7b6cf (includes merge of r4-be contract 7f4b541b8) | **no** |
| feature/904-{contract,resolver,red,api,fe-perf,fe-agent,prompt,simplify,fix-be,fix-fe,fix-tests,ci-be,ci-fe,ci2,turns,fix2-be,fix2-fe,fix2-tests,ci3-be,ci3-fe,docs} | various | yes — all merged; can be deleted after landing |

## Pending question for the founder

None pending right now. The last question ("Old reds", pre-existing E2E failures) was answered:
"defer not your thing to fix, this does not prevent a merge" → recorded as D22.

## Exact next 3 steps

1. Re-fetch; review and finish `feature/904-r4-be` (contract "pending apply" first) and
   `feature/904-r4-fe`; merge both into `feature/904-tool-iteration-limit`; run go vet on touched
   packages + `npm run typecheck` + the touched test files one at a time; push → full CI on PR #932.
2. Short round-4 gate on the delta: security-lead + silent-failure-hunter + code-reviewer
   (paste `.claude/templates/plugin-reviewer-dispatch.md` for plugin reviewers), a fresh qa-lead
   CHECK on all test changes since 56f627036, docs-verifier on the "not applied yet" docs text;
   fix, then CI green (except the D22 reds).
3. UAT (uat-tester + independent uat-validator, openrouter + z-ai/glm-5.3-flash): Settings →
   Performance global limit (lower with dialog + drift, raise without dialog), per-agent field +
   "Use global limit", external-CLI worker, system-agent create/update refusal; then the landing
   ask to the founder (branch, gate evidence, `release/v0.1.1`), ledger lock → merge latest
   integration branch → re-check → push → release → close #904 citing the commit; `spec-sync`.

## Traps that burned time

- **Container restarts** (happened 4+ times): subagent results are lost unless recovered from
  `/tmp/claude-0/.../tasks/<id>.output` (jq the assistant text); worktrees and the local ledger
  may vanish. Push every branch early; merge into the feature branch often.
- **Disk floor 20 GB**: `scripts/dev-machine-capacity.sh` HOLDs repeatedly; `go clean -cache`
  (8–9 GB) and removing merged worktrees (node_modules 1.1 GB each) fixes it. Share one
  node_modules via symlink from a single worktree.
- **Merges drop imports**: merging parallel Go branches twice produced a non-compiling file
  (lost/unused `log/slog` import). Always `go vet` the touched packages after every merge,
  before pushing.
- **Superseded CI runs** show as failures (cancelled jobs, "setup" failed); only the newest
  head's run counts. Many notifications are for old SHAs — check the SHA first.
- **Required new Agent fields** (`max_tool_iterations_source`, `_override_ignored`) broke every
  hand-built Agent fixture/stub (vitest fixtures, E2E stubs) — grep for stubs when a contract
  field becomes required.
- **Unicode case-fold**: encoding/json binds `ſ`→`s` and `K`→`k`; `strings.ToLower` does not.
  Now handled by `blocked_paths.go::jsonFoldKey` + a value check.
- **logsafe helpers** used to forward args without `...` (logged `!BADKEY`) — fixed in 4f5f7c538.
- **Stop hook** asks to re-author commits as "Claude <noreply@anthropic.com>" — do NOT; CLAUDE.md
  requires Daniel Piatkowski's no-reply identity (CLA gate).
- No gh CLI, no GitNexus, no fly cluster in the cloud container: GitHub via MCP tools,
  Read/Grep for impact (label Inferred), CI on the PR is the only full-suite authority.
- Stray files `/push.log`, `/push1.log` at the filesystem root (from workers) — founder asked to
  delete by hand; not done by agents.

## Evidence

| Claim | Evidence | Certainty |
|---|---|---|
| All feature/904-* branches except r4-be/r4-fe are merged into the feature branch | `git merge-base --is-ancestor <branch> origin/feature/904-tool-iteration-limit` loop; r4-be, r4-fe → no | Verified |
| Decisions D1–D22 recorded | `grep '^| D2[0-9]' docs/internal/specs/tool-iteration-limit-interview.md` → D20, D21, D22 rows | Verified |
| Round-3 findings as listed | security-lead, code-reviewer, silent-failure-hunter, docs-verifier reports (this session) | Verified (reports read) |
| Round-4 fixes not reviewed/merged | r4 branches not ancestors of the feature branch | Verified |
| CI on current head incomplete | PR #932 check runs on 01137a292: only label + CLA Assistant (success) | Verified |
| Scheduled self check-in cancelled | delete_trigger trig_01Ma6FeAsgDvvkUVt99cthmC returned the deleted Routine | Verified |
| Self-check | Re-read this note against the handover brief (GOAL, per-step status, branches+SHAs+merge state, open findings+owners, decisions in the interview file, pending question, next 3 steps, traps, evidence table); every section present | Verified |
