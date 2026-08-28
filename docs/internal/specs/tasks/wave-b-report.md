# Wave B report — ADR-066 / ADR-067 / ADR-068 task streams

Branch: `feat/context-budget-and-tool-result-routing`
Base at wave start: `64503f91` (Wave A.1 report). Head at wave end: `7139e762` (verified identical on `origin`).
Written 2026-08-23. Everything below is observed from `git log`, `git ls-remote`, the per-task result records and the integrator record; anything not observed is listed under "Unverified".

## 1. Outcome in one paragraph

Of the 61 tasks in the landing queue (60 plus the placeholder `B5-066-CHAT`), **3 landed** (T068-01, T066-01, T066-11) and **57 were dependency-blocked** without writing code. The blocked tasks were not wrong to stop: every one of them depends, directly or transitively, on one of seven root tasks (T067-02, T067-03, T067-04, T068-02, T066-02, T066-03, T066-05) that were never pushed to `origin`. Three of those roots (T067-02, T066-03, T068-06) have local worktrees sitting at the base commit `64503f91` with zero commits. The branch head is green on `quick`, `contracts` and `spa`; `go-test` and `lint` are red only on two inherited baseline failures that pre-date the wave.

## 2. What landed

Branch history `64503f91..7139e762`, linear, authored as Daniel's GitHub no-reply identity, no `Co-Authored-By` trailers (checked with `git log --format=%(trailers:key=Co-authored-by)`).

| Task | Commit on branch (task-branch commit) | Tests written | Gate evidence |
|---|---|---|---|
| T068-01 — No-removed-providers CI gate (script, allow-list, `pr.yml` / `runci.sh` / Makefile wiring) | `3bde7d29` (same SHA on `task/T068-01`) | 1 (self-check script) | Local: self-check GREEN, guard RED on current tree (expected until T068-02/03 delete the 38 offenders), shellcheck GREEN. Fly lint @3bde7d29: RED — baseline only (12 gosec G115 in `pkg/sandbox`). |
| T066-01 — S66 contract additions (recall-mark + argument-refusal asyncapi schemas, wire-type register entries, contract_test rows) | `56bbb5bb` (rebased from `7fa23c5e` on `task/T066-01`) | 3 | Tests-first RED observed; scoped `pkg/api/generated` GREEN; gofmt/golangci-lint GREEN; `make verify-contracts` GREEN; Fly contracts @56bbb5bb GREEN (exit 0). |
| T066-11 — D7 typed turn exits (`turn_canceled` / `turn_timed_out` / `context_unrecoverable`) | `5ab74bb7` (rebased from `b085f91f` on `task/T066-11`) | 2 | Red-first PASS; scoped `pkg/agent` + `pkg/api/generated` GREEN; gofmt/lint GREEN; Fly go-test @5ab74bb7 RED (see section 4). |
| Integrator fix-forward for T066-11 | `7139e762` — widens the ADR-057 closed-consumer-set guard (`pkg/agent/routing_session_id_consumer_set_adr057_test.go`, +8/-6) for the new `typedTurnExit` payload stamp | 0 (test edit) | Fly go-test @7139e762: only the pkg/config baseline remains (section 5). |

No file overlaps between the three task commits; all rebases were clean.

## 3. What did not land, and why

57 tasks returned `done=false` with no commits. Every blocker is the same shape: "dependency X is neither integrated into the feature branch nor present as `task/X` on `origin`". The only task branches that ever existed on `origin` were `task/T068-01`, `task/T066-01`, `task/T066-11`.

Root causes, grouped by the missing root that blocks each chain:

| Missing root | Observed state | Directly blocked tasks | Transitively blocked |
|---|---|---|---|
| T067-02 (document-backed catalog: `document.go` / `parse.go` / `resolve.go` / `locality.go`) | local worktree `wt-task-T067-02` at `64503f91`, no commits, not on origin | T067-03, T068-05, T068-11, T068-13, T066-09 | all of S67 (T067-04..14), most of S68, S66 window chain |
| T067-03 / T067-04 (version + puller; refresh transaction / store / served pair) | no branch anywhere | T067-06, T067-07, T067-11 | T067-08..14 |
| T068-02 (delete the OAuth-only Google provider) | no branch anywhere | T068-03, T068-04, T068-05, T068-07 | T068-08..31 |
| T068-06 (ADR-068 own contracts) | local worktree `wt-task-T068-06` at `64503f91`, no commits | T068-07, T068-09, T068-13, T068-14 | S68 UI tail |
| T066-02 (pkg/memory projection state, `RollbackAppended` emptied-set) | no branch anywhere | T066-05, T066-12 | T066-06/07/13/14/18 |
| T066-03 (`config.ContextSettings` + budget-B helper) | local worktree `wt-task-T066-03` at `64503f91`, no commits | T066-05, T066-07, T066-08, T066-09, T066-15, T066-16, T068-17 | T066-10/17, T068-29/30 |
| T066-05 (tool-result choke point) | no branch anywhere | T066-06, T066-07, T066-12, T066-13, T066-14, T066-15 | T066-18, B5-066-CHAT |

Two blocked tasks raised questions that need an integrator answer before re-dispatch:

- **T068-16**: the `OnboardingProviderApiKey` / `OnboardingProviderSignIn` oneOf schemas it consumes are absent from `contracts/components/schemas/` on the branch. Unclear whether they belong to an A-CONTRACT follow-up or to T068-16 itself.
- **T068-28**: asked whether it may be built against A-CONTRACT generated types alone, ahead of T068-08; the task entry lists T068-08 as hard, so it did not proceed.
- **B5-066-CHAT** is still a placeholder with no real task id or owner (queue note 10).

## 4. Integration conflicts and reverts

- **Conflicts:** none. The three commits touch disjoint files.
- **Reverts:** none.
- **Fix-forward:** one, `7139e762`. T066-11's `typedTurnExit` added a 29th `routingSessionID` read, which tripped `TestRoutingSessionID_ConsumerSetIsClosed` (the ADR-057 closed-consumer-set guard). This was task-intrinsic — T066-11's own Fly go-test gate was reported PENDING, never green — not a merge interaction, so the integrator widened the guard rather than reverting.

## 5. Final Fly verdicts on the branch head `7139e762`

| Gate | Verdict | Exit | Note |
|---|---|---|---|
| quick | GREEN | 0 | also run after each of the three integrations |
| contracts | GREEN | 0 | |
| spa | GREEN | 0 | typecheck 0, vitest 0; first attempt's SSH was cut by a local 10-minute timeout, re-run captured fully |
| go-test | RED (baseline only) | 1 | only `pkg/config TestNoAgentConfigWorkspaceIdentifier`, in both parallel and isolated runs; 107 packages ok; no FLAKE lines |
| lint | RED (baseline only) | 1 | measured at `3bde7d29`; 12 gosec G115 in `pkg/sandbox/sandbox_linux.go` + `seccomp_linux.go`; not re-run at `7139e762` (no Go files changed by T068-01, and T066-01/11 passed `golangci-lint` on their packages locally) |

Intermediate: Fly go-test @`5ab74bb7` RED on `pkg/agent TestRoutingSessionID_ConsumerSetIsClosed` + the pkg/config baseline — fixed forward in `7139e762`.

## 6. Inherited reds (baseline, pre-date this wave)

Both are ours under Constraint #7 and neither is owned by any task in the three lists:

1. **`pkg/config TestNoAgentConfigWorkspaceIdentifier`** — flags `.Workspace` usages in `pkg/migrate/sources/openclaw/openclaw_config.go`; file untouched by this wave; reproduced failing locally on bare base `64503f91` in the `wt-task-T066-03` worktree.
2. **Fly lint: 12 gosec G115** in `pkg/sandbox/sandbox_linux.go` and `seccomp_linux.go` — reported as an accepted baseline being fixed on another branch.

Also inherited from Wave A.1 and still true: `pkg/providers/capabilities/` is still present; `pkg/providers/catalog/data/providers_catalog.json` is still the old hand-generated file.

## 7. HIGH / CRITICAL impacts reported

- **T066-05** (blocked, not executed) pre-reported that when it runs it will touch the CRITICAL symbol `loop.go::runTurn` (twelve `Role:"tool"` producer sites) and should be integrated alone with its own Fly go-test.
- No landed task reported a HIGH or CRITICAL impact. T066-01 was additive (no existing symbol edited). T066-11 ran impact via the `gitnexus` CLI against the `wt-context-budget` index (GitNexus MCP tools were not exposed to the subagents); no HIGH/CRITICAL was reported.

## 8. Unverified

- The new no-removed-providers guard has **not executed on the Fly worker**: `/cache/runci.sh` there is the pre-change cached copy. Redeploy per `deploy/ci-worker/CLAUDE.md` (md5 of `deploy/ci-worker/runci.sh` must match) once the worker is idle. After that the lint gate is RED on every branch until T068-02/03 land — by design.
- The GitHub Actions run of the modified `pr.yml` job was not observed (no PR opened).
- T066-01's field/discriminator names (`tool_arguments_too_large`, `tool_result_recall_mark`, `size_chars` / `cap_chars` / `turn` / `hint`, `content_state`) are the task author's design; the spec pins no literals. T066-04 must adopt them or the contract commit must be amended.
- `pkg/gateway structured_failure_discriminator_coverage_test.go` was not run locally for T066-01; it passed inside the Fly go-test @`7139e762` run (107 packages ok) — that is the only evidence.
- Fly lint was not re-run at `7139e762` (see section 5).
- Whether any blocked root task has uncommitted work in another session's worktree — every task only inspected `origin` refs and `git worktree list`; the three zero-commit worktrees (`wt-task-T067-02`, `wt-task-T066-03`, `wt-task-T068-06`) were not inspected for dirty files.
- Queue planning items carried over unratified: the 13 list inconsistencies in the queue notes (067-first ordering vs ADR-068's deletion-first advice; T067-10/T068-04, T067-11/T068-17, T067-12/T068-13 scope splits; `cli_driver` vs `cli_kind` naming — T066-09 must use `cli_kind`; T067-06 pinning B2 `v2026.8.23.1` rather than the first release).

## 9. Tasks remaining for Wave B.1

Same landing order as the Wave B queue with the three landed tasks removed. The seven roots in **bold** must be dispatched and pushed first; nothing else can start until they exist on `origin`.

**Stream S67:** **T067-02**, T067-03, T067-04, T067-06, T067-07, T067-08, T067-09, T067-10, T067-11, T067-12, T067-13, T067-14

**Stream S68:** **T068-02**, T068-03, T068-05, T068-04, **T068-06**, T068-07, T068-08, T068-11, T068-09, T068-10, T068-12, T068-13, T068-14, T068-15, T068-16, T068-18, T068-19, T068-20, T068-22, T068-23, T068-21, T068-24, T068-25, T068-26, T068-27, T068-28, T068-17 (after T066-03), T068-29, T068-30, T068-31 (Wave D only)

**Stream S66:** **T066-02**, **T066-03**, T066-04, T066-08, T066-16, **T066-05**, T066-06, T066-07, T066-12, T066-13, T066-09, T066-10, T066-14, T066-15, T066-18, T066-17

**Unowned:** B5-066-CHAT (needs a real task id + owner); baseline fixes for `pkg/config TestNoAgentConfigWorkspaceIdentifier` and the 12 gosec G115 findings (need an owner/task before ship).

Full interleaved order (57 + placeholder): T067-02, T067-03, T067-04, T067-06, T067-07, T067-08, T067-09, T067-10, T067-11, T067-12, T068-02, T068-03, T068-05, T068-04, T068-06, T068-07, T068-08, T068-11, T068-09, T068-10, T068-12, T068-13, T068-14, T068-15, T068-16, T068-18, T067-13, T067-14, T068-19, T068-20, T068-22, T068-23, T068-21, T068-24, T068-25, T068-26, T068-27, T068-28, T066-02, T066-03, T068-17, T066-04, T066-08, T066-16, T066-05, T066-06, T066-07, T066-12, T066-13, T066-09, T066-10, T066-14, T066-15, T066-18, T066-17, T068-29, T068-30, B5-066-CHAT, T068-31.

Dispatch rule for B.1 that this wave's failure makes explicit: a task is "available" only when every `dependsOn` id has a pushed `task/<id>` branch on `origin` or a commit on the feature branch — a local worktree at the base commit does not count. T066-04 and T068-28-style "contract-only" tasks that depend solely on A-CONTRACT plus a landed commit (T066-01 is now on the branch) can be dispatched immediately.
