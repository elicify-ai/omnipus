# U1 stopped-state RED plan — 2026-09-30

## Scope and independent oracles

Rewrite only the two deferred-child cases and the cancellation case assigned to this RED pack. A stopped child is resumable, non-terminal, and produces one independent direct-parent notice. Session stopping and goal ending are separate claims.

| Oracle | Requirement used |
|---|---|
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/adr-subagent-control-plane/docs/internal/architecture/ADR-20260928-sub-agent-control-plane.md` — D6 | `stopped` is non-terminal, same generation; stop note persists; every actual stopped transition persists one direct-parent notice independently of a goal claim. The notice identifies cause, actor and time and offers RESUME, redirect, doing the work, reporting it open, and clearing the helper's goal. A working parent is woken once. Live retry and boot replay deduplicate by parent/child/generation/stop sequence. |
| Same ADR — D6 Goal row / D7 | Stop all preserves every session-owned active goal. This substantively reverses the old FD1=A pair-ending oracle; it is not a rename of `cancelled`. Deliberate goal clear and the separate explicit idle-goal policy may end a goal, but stopping alone may not. |
| Dispatcher ruling for these two deferred fixtures | Goal clear / idle expiry may settle a child whose turn already exited at the completion gate to stopped/non-terminal and notify its parent. This does not authorize stopping a live turn on goal clear. |
| `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/u1-stopped-state/docs/internal/specs/planning-goals-spec.md` — FR-064, FR-038 | The independent idle-age policy winds down goals idle for at least seven days by default. The default is explicit in the spec, not derived from a test run. |

## Boundary

Real: launcher, cancellation/completion paths, lifecycle and goal stores, durable parent inbox, upward deliverer, notifier, and boot recovery. The external model provider is the existing mock; no model turn is dispatched. An observer records notifier events without replacing delivery. Reopened stores read the same real files. No future Go notice type or message-ID serialization is chosen by the tests.

## Cases and negative assertions

| Case | Input | Expected | Source |
|---|---|---|---|
| Explicit clear | A goal-bearing child, running record, no live turn; `/goal clear` | Deliberately cleared goal ends. Child is stopped/non-terminal in its original generation. One D6 notice reaches its direct parent; not a fatal terminal report. | Dispatcher deferred-child ruling; D6 Goal and stopped-child notice rows |
| Idle expiry | Same deferred fixture; goal activity predates the default seven-day policy | Goal ends under that policy, with the idle-expiry reason asserted separately. Child is stopped/non-terminal in its original generation with one notice. | FR-064 / FR-038; D6 independent idle-policy exception; dispatcher ruling |
| Cancel / Stop all | Session-owned active child goal; human principal `qa-red-947` | Same goal ID remains active and bound to the child, with no terminal reason. Child is stopped/non-terminal; notice identifies cascade, human actor, time, options and owner-first advice. | D6 / D7 / T20 |
| Retry and recovery, in each case | Repeat the operation, then reopen stores and run real recovery twice | One stable notice ID and stop note; no additional working-parent wake. Goal and generation assertions still hold after recovery. | D6 deterministic deduplication; T6 |
| Forbidden substitute, in each case | Legacy `<child>:<generation>:final` or fatal terminal error | It does not satisfy the notice assertion. Missing readable D6 notice fails loudly with `BLOCKED`, never skips. | D6 non-terminal stop and independent notice requirement |

These state, ordering and negative assertions are within the three requested cases. The plan does not claim the skill's general negative-case ratio or complete numeric-boundary catalogue has been met.

## Planned fresh-CHECK mutation probes

| Mutant | Expected detecting assertion |
|---|---|
| Add stopped to the terminal-state map | `Terminal() == false` |
| Suppress the independent notice or substitute the old fatal/final report | Exactly one readable non-terminal D6 notice |
| Restore FD1=A goal pair-ending on cancel | Original goal remains active, same ID and binding, no terminal reason |
| Remove live-retry / boot notice deduplication | Stable single notice ID and exactly one parent wake |
| Suppress the explicit idle-age goal policy | Independent idle-expiry goal assertion |

GREEN, mutation execution, and the skill's Proof-of-failability gate are deferred to a different CHECK instance. This RED instance never audits its own suite.

## Deliberate gaps

No live-turn clear, all-parent-state wake matrix, delivery-failure retry, new stop after resume, sequence-key collision campaign, task/plan cascade, question/lifetime timeout, accepted/refused goal-claim campaign, or full ADR acceptance is claimed. The tests validate a nonzero contract message timestamp and persisted stop-event time, but do not prove the parent-facing prose accurately presents that event time; that remains CHECK/UAT work because D6 specifies no prose/display format. No production implementation or API/contract edit belongs to this pack.

## Execution record

Starting code: `842574ef9d9e6d5a257ca6646bcc9fb65c9c1da4`; corrected ADR: `cd20cf8b365e7bc8a010c731a8f6830e14397c23`.

Source discovery found retired lifecycle constants in five additional test files outside this brief. They are not silently changed. A package compile failure is NOT behavioral RED; actual named failing assertions are required before RED is proved.

The final-source narrow run used `lockf -t 1800 /tmp/omnipus-gotest.lock`, `CGO_ENABLED=0`, tags `goolm,stdjson`, `-count=1 -v -p 1`, and `-run '^TestGoal984_GoalEnderRoutesDeferredChildThroughTail$'` against `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/u1-stopped-state/pkg/agent`. Direct exit code: **1**. Receipt: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/u1-stopped-state/tests/receipts/u1-stopped-state-20260930/red.log`; command and raw exit receipts are `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/u1-stopped-state/tests/receipts/u1-stopped-state-20260930/red.command.txt` and `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus-worktrees/u1-stopped-state/tests/receipts/u1-stopped-state-20260930/red.exit.txt`.

The compiler reported undefined `session.LifecyclePaused`, `session.LifecycleCancelled`, and `session.LifecycleTimedOut` in the out-of-scope boot-sweep, conformance, cancel, and goal-failure test files, then stopped with `too many errors` and `[build failed]`. A source search also found a retired constant in the launcher tests. **Zero named tests executed; behavioral RED is UNVERIFIED.** The cancel preservation run was not repeated against the same package-wide compile blocker. The remaining test files need a separate scoped cleanup before these assertions can be observed RED. This is a blocker, not a waived or closed failure; no production GREEN or mutation claim is made.

## U1 CHECK-BLOCK fix round — 2026-10-01 (D2/CRIT-001 stop_note fixture repair)

Following up on a CHECK BLOCK against backend-lead's stop_note commit
(`57c1a20ba`): 10 pre-existing tests across `pkg/session`, `pkg/tools`,
`pkg/agent` never constructed a `StopNote` and were rejected by the new
`persistLocked` invariant (D2/CRIT-001: any record landing
`LifecycleStopped` MUST carry a non-nil `StopNote`). Test-file-only fix;
production invariant is correct and untouched.

| file::test | old construction | new construction | justification |
|---|---|---|---|
| `pkg/session/lifecycle_stopped_test.go::TestLifecycleStopped_CanResumeSameGeneration` | `LifecycleRecord{State: "stopped", ...}` with no `StopNote` | adds `StopNote{At, By: "human:qa-lead", Seq: 1, Cause: StopCauseStop}` | D2/CRIT-001; stale fixture predates the stop_note invariant. Cause "stop": the session named is itself the direct target (`StopCauseStop`'s own doc comment). |
| `pkg/session/lifecycle_test.go::TestLifecycleStore_Mutate_AppliesAndPersists` | `rec.State = LifecycleStopped` inside `Mutate`, no note | adds `rec.StopNote = &StopNote{..., Cause: StopCauseStop}` in the same mutation | D2/CRIT-001; stale fixture. Same "stop" cause rationale as above — a direct single-session mutation to stopped. |
| `pkg/tools/delegate_meta_mirror_947_test.go::TestDelegateChildTransition_MirrorsTerminalStatusToUnifiedMeta` | seeds a queued child, then calls `droppedQueuedResult` directly (production passes `note=nil`, relying on a prior `stampStop`) | seeds the queued record WITH `StopNote{Cause: StopCauseStop}` already attached, mirroring what `steer_cancel.go::stampStop` stamps onto the record in the SAME mutation that sets the Stop fence, before the record ever reaches `LifecycleStopped` | D2/CRIT-001 + `delegate_run.go::droppedQueuedResult`'s own comment (line ~673-677): reached only after cancelHard/cancelSoft already stamped the note. Test skipped that precondition; now matches it. |
| `pkg/tools/delegate_meta_mirror_947_test.go::TestDelegateChildTransition_UnwiredUnifiedStore_StillTransitions` | same as above, unwired-UnifiedStore variant | same fix as above | same justification |
| `pkg/agent/boot_sweep_test.go::TestBootSweep_AwaitingCorrectionOwnerExempt` | `LifecycleRecord{State: session.LifecycleStopped, ...}` (sess-owner), no note | adds `StopNote{Cause: StopCauseRedirectPause}` | D2/CRIT-001; stale fixture. Cause "redirect_pause": this durably-parked pre-rename `paused` owner is D2/D6's closest match to a fresh-instruction pause rather than stop/cascade/restart/timeout — same match `delegate_park.go`'s own precedent comment draws for this shape. |
| `pkg/agent/boot_sweep_test.go::TestBootSweep_PausedOwnerNotAwaitingCorrection_Swept` | same (sess-owner-2) | same fix | same justification |
| `pkg/agent/boot_sweep_test.go::TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart` | same (owner-rs) | same fix | same justification |
| `pkg/agent/conformance_design_test.go::TestConformance_bootsweep_Design` | same (bs-owner) | same fix | same justification |
| `pkg/tools/delegate_adr053_test.go::TestDelegateTool_Cancel_SoftThenHardBackstop` | stub hard-cancel hook returns descendant ids with no stamping | stub now also stamps `rec.StopNote{Cause: StopCauseStop}` via `lc.Mutate`, mirroring `steer_cancel.go::stampStop`'s real effect | **Investigated, not a production gap**: the real non-stubbed path (`al.cancelDelegatedSubtree` → `SteerCanceller.CancelSubtree`/`StopSubtree` → `stampStop`, `pkg/agent/steer_cancel.go:628`) correctly stamps `StopNote` (cause "stop") in the same mutation as the Stop fence, confirmed by reading the code — this test's hand-rolled stub hook bypassed that real path entirely. Stale/unrealistic double, not a defect; per `delegate_run.go:832-835`'s own comment, `transitionLifecycle` is called with `note=nil` because the caller's cancelHard is expected to have already stamped it. |
| `pkg/tools/delegate_adr053_test.go::TestDelegateTool_Cancel_Hard_SkipsGrace` | same stub pattern | same fix | same investigation and conclusion as above |

### Escalation check (Group B)

Per dispatch brief: read the real hard-cancel path (`pkg/agent/steer_delegate_cancel.go::cancelDelegatedSubtree` → `pkg/agent/steer_cancel.go::SteerCanceller.CancelSubtree`/`StopSubtree` → `stampStop`). Confirmed `stampStop` (steer_cancel.go:628-659) sets `rec.StopNote` in the SAME `Mutate` call that sets `rec.Stop`, cause taken from the caller (cascade uses `StopCauseCascade`, the direct target uses `StopCauseStop` per `delegate_run.go`'s own comments at lines 676-677 and 832-834). The shared terminal-landing closure (`steer_cancel.go:211-224`) also carries a defensive fallback synthesizing a `StopCauseStop` note if none exists, so even an edge-case caller cannot strand a record. **Conclusion: no production gap — verdict is stale test double, fixed as a standard test-file repair.**

## U1 small fix round — 2026-10-01 (post-CI: lint + 2 more stale-fence fixtures)

Following a real CI green run (`https://github.com/elicify-ai/omnipus/actions/runs/36797255093`,
241s, no hang/leak — prior local contention ruled out, not a regression). CI found two
lint findings in one already-owned test file, plus two more pre-existing stale-fence
assertions in `steer_delegate_cancel_test.go`, same class as the 8 fixed in the round
above but in a file that round's scope list didn't cover.

### Lint (mechanical, no behavior change)

| file::location | finding | before | after |
|---|---|---|---|
| `pkg/agent/stopped_child_notice_u1_test.go::u1NoticeBody` (prealloc) | `var parts []string` grown via unbounded `append` in a `for range fields` loop | `var parts []string` | `parts := make([]string, 0, len(fields))` |
| `pkg/agent/stopped_child_notice_u1_test.go` (QF1001, De Morgan's law) | `!(strings.Contains(body, "ask") \|\| strings.Contains(body, "check with"))` | `!(strings.Contains(body, "ask") \|\| strings.Contains(body, "check with"))` inside `(!strings.Contains(body, "owner") \|\| !(...))` | `(!strings.Contains(body, "ask") && !strings.Contains(body, "check with"))` inside `(!strings.Contains(body, "owner") \|\| (...))` |

Verified clean: `golangci-lint run --build-tags=goolm,stdjson ./pkg/agent` → `0 issues` (exit 0); the file is no longer named in output (single-file invocation fails typecheck on package-external symbols, so the package-scoped run is the correct instrument — confirmed by reading its own error list first).

### Stale-fence fixtures (D2/CRIT-001 pattern, same class as the 8 above)

| file::test | old assertion | new assertion | justification |
|---|---|---|---|
| `pkg/agent/steer_delegate_cancel_test.go::TestDelegateCancel_QueuedSubagentIsDroppedAndNeverStarts` | `if rec.Stop == nil && !rec.Terminal() { t.Fatalf(...) }` — demanded a live Stop fence OR Terminal()==true | `if rec.State != session.LifecycleStopped { t.Fatalf(...) }` followed by `if rec.StopNote == nil { t.Fatalf(...) }` | `session.LifecycleStopped` is deliberately non-terminal (`lifecycle.go::LifecycleStopped` doc comment, `terminalLifecycleStates` excludes it) and `Stop` (the in-flight fence) is documented to clear "the instant the stop it names is carried out" (`lifecycle_edge.go::StopNote` doc comment) — so a correctly-landed stopped record always has `Stop == nil` and `Terminal() == false`, making the old assertion fire on EVERY correct landing. `State == LifecycleStopped` + non-nil `StopNote` is the durable, authoritative, D2/CRIT-001-backed proof the cancel actually landed; stronger than the old proxy, not weaker. |
| `pkg/agent/steer_delegate_cancel_test.go::TestDelegateCancel_RunningSubagentStopsItsGrandchildren` | `if childRec.Stop == nil { t.Fatalf(...) }` | `if childRec.State != session.LifecycleStopped { t.Fatalf(...) }` followed by `if childRec.StopNote == nil { t.Fatalf(...) }` | Same class/reason as above. `childID` is the direct hard-cancel target, which lands synchronously by the time the tool call returns (`res.ForLLM` already reports "hard-cancelled immediately"), so its fence has already cleared — `Stop == nil` is expected, not a failure. Scope note: the neighbouring `grandRec.Stop == nil \|\| grandRec.Stop.Generation != grandRec.Generation` check (same test, checked a few lines earlier) was deliberately left untouched — read `lifecycle_edge.go::Stop`'s own doc comment confirming the fence is written per-descendant and cleared only once THAT descendant's own turn lands; the grandchild's async turn is still winding down at the point of that check (the test waits on `grandTS.Finished()` only afterward), so a still-live fence there is the correct in-flight proof, not a stale one — out of the dispatch brief's scope and confirmed correct by source reading, not skipped. |

### Proof (compile + named runs, under the shared lock, serial)

| Probe | Command (abbreviated) | Exit | Result |
|---|---|---|---|
| Compile-only | `go vet -tags goolm,stdjson ./pkg/agent/` | 0 | clean |
| `TestDelegateCancel_QueuedSubagentIsDroppedAndNeverStarts` | `go test -tags goolm,stdjson -p 1 -run '^TestDelegateCancel_QueuedSubagentIsDroppedAndNeverStarts$' ./pkg/agent/ -v` | 0 | `--- PASS (4.67s)` |
| `TestDelegateCancel_RunningSubagentStopsItsGrandchildren` | `go test -tags goolm,stdjson -p 1 -run '^TestDelegateCancel_RunningSubagentStopsItsGrandchildren$' ./pkg/agent/ -v` | 0 | `--- PASS (1.83s)` |
| D6 regression check (file untouched — `git diff --stat` empty) | `go test -tags goolm,stdjson -p 1 -run '^TestComplete_StopThatCausedThisCompletionLandsTerminal$' ./pkg/agent/ -v` | 1 | `--- FAIL`, message still names `hasRunningOrQueuedDescendant` — the same, correctly-tracked D6 gap, not touched by this round |

No production code was edited this round; test-file-only, same as every prior round.

## U1 original mechanical-rename round — commit `4f006b138` (2026-10-01, documented retroactively)

CHECK's second unit-level audit found this plan missing a changed-test-list table
for the ORIGINAL rename round, pre-dating the two rounds documented above. Added
here, retroactively, from reading `git show 4f006b138` directly — not from the
file list in the dispatch brief that requested this table, which did not match
the commit: the brief named `cancel_lifecycle_bridge_test.go`,
`cancel_orchestration_adr057_test.go`, `task_executor_superseded_session_test.go`,
`list_jobs_row_test.go`, and `tests/adr091/steered_sessions_test.go`, none of
which this commit touches, and omitted `pkg/agent/boot_sweep_test.go`,
`pkg/agent/conformance_design_test.go` and `pkg/tools/delegate_adr053_test.go`,
which it does. The table below reflects the actual diff (`git show 4f006b138`),
confirmed by direct read, not the brief's list.

15 `session.LifecyclePaused` / `LifecycleCancelled` / `LifecycleTimedOut` →
`LifecycleStopped` constant substitutions, almost all mechanical, plus two
ADR-grounded fixes and one new BLOCKED stub that the commit message itself
flags as non-mechanical.

| file::test | old assertion | new assertion | justification |
|---|---|---|---|
| `pkg/agent/boot_sweep_test.go::TestBootSweep_AwaitingCorrectionOwnerExempt` | seed `State: session.LifecyclePaused` | seed `State: session.LifecycleStopped` | Mechanical constant rename (`Paused` retired). This test is touched again in the later StopNote fixture-repair round (table at "U1 CHECK-BLOCK fix round" above) for a separate `StopNote` construction fix — not duplicated here. |
| `pkg/agent/boot_sweep_test.go::TestBootSweep_PausedOwnerNotAwaitingCorrection_Swept` | seed `State: session.LifecyclePaused` | seed `State: session.LifecycleStopped` | Same mechanical rename; same later-round cross-reference as above. |
| `pkg/agent/boot_sweep_test.go::TestBootSweep_AwaitingCorrectionOwnerNotSweptAcrossRestart` | seed `State: session.LifecyclePaused`; assertion `if owner.State != session.LifecyclePaused` | seed + assertion both renamed to `session.LifecycleStopped` | Same mechanical rename (seed and assertion both moved together); same later-round cross-reference as above. |
| `pkg/agent/conformance_design_test.go::TestConformance_bootsweep_Design` | seed `State: session.LifecyclePaused`; assertion `if owner.State != session.LifecyclePaused` | seed + assertion renamed to `session.LifecycleStopped` | Mechanical rename; touched again in the later StopNote fixture-repair round (table above) — not duplicated here. |
| `pkg/agent/steer_cancel_test.go::TestSteerGenerationCancel_NeverRanChildUnblocksParent` | `if after.State != session.LifecycleCancelled { t.Fatalf(...) }` | `if after.State != session.LifecycleStopped { t.Fatalf(...) }` | Mechanical rename only. The failure message text still literally reads "want cancelled" (stale wording, not a behavioral assertion) — not touched by this round or either later round; out of this closing round's scope too. |
| `pkg/agent/steer_cancel_test.go::TestReportSteeredSessionTerminalUpward_NoDelivererLeavesRecordRunnable` | `reportSteeredSessionTerminalUpward(..., session.LifecycleCancelled, ...)` then `if rec.Terminal() { t.Fatalf("written TERMINAL with no deliverer") }` | captures `before := lc.Load(childID)` first; calls `reportSteeredSessionTerminalUpward(..., session.LifecycleStopped, ...)`; asserts `if rec.State == session.LifecycleStopped { t.Fatalf(...) }` AND `if rec.State != before.State { t.Fatalf(...) }` | **Not a pure rename** — one of the two ADR-grounded fixes the commit message names explicitly ("steer_cancel_test.go: strengthen to reject a stopped write specifically"). Because `Stopped` is non-terminal (ADR Vocabulary line 133), the old `!Terminal()` guard could no longer detect an erroneous write with no upward deliverer wired — a stray `stopped` write would pass `!Terminal()` trivially. The strengthened version instead pins the exact pre-report state and fails on ANY state change with no deliverer, which is strictly stronger. Not touched again in either later table in this plan. |
| `pkg/agent/steer_goal_failure_test.go::TestGoalDelegation_StopNoteCauseRequiredByControlPlane` | did not exist before this commit | new test: `t.Fatal("BLOCKED: separate persisted stop_note.cause not implemented — required by ADR-20260928-sub-agent-control-plane Vocabulary lines 133/137 and D2 line 209")` | **Not a rename — a new, deliberate honest stub** (team-lead-ruled item 1 of this commit), marking a verified-absent production field (commit message: grep exit 1, positive-control exit 0) rather than inventing it or skipping silently. Resolved later in a separate, undocumented-in-this-plan commit `2412e09a1` ("wire stop_note.cause into stampStop/goal-failure/E2E stop tests") — out of scope for this plan's two documented rounds. |
| `pkg/agent/steer_launcher_test.go::TestWake_AppliesConfiguredTimeout` | `if rec.State != session.LifecycleTimedOut { t.Fatalf(...) }` | `if rec.State != session.LifecycleStopped { t.Fatalf(...) }` | Mechanical rename only (`TimedOut` retired). Not touched again in either later table in this plan. |
| `pkg/tools/delegate_adr053_test.go::TestDelegateTool_Cancel_SoftThenHardBackstop` | `rec.State == session.LifecycleCancelled` (loop-exit check); `t.Fatal("...never persisted as cancelled")` | `rec.State == session.LifecycleStopped`; `t.Fatal("...never persisted as stopped")`; two added ADR D7 line 397 comments | Mechanical rename plus comment update, no assertion-strength change. Touched again in the later StopNote fixture-repair round (table above) for a separate `StopNote`-construction fix on the same test — not duplicated here. |
| `pkg/tools/delegate_adr053_test.go::TestDelegateTool_Cancel_Hard_SkipsGrace` | `if rec.State != session.LifecycleCancelled { t.Errorf("state = %q, want %q", rec.State, session.LifecycleCancelled) }` | `if rec.State != session.LifecycleStopped { t.Errorf("state = %q, want %q", rec.State, session.LifecycleStopped) }` plus an added ADR D7 comment | Mechanical rename only. Touched again in the later StopNote fixture-repair round (table above) for the same reason as the row above. |
| `pkg/tools/delegate_adr053_test.go::TestDelegateTool_Respond_3P_OriginalNotLeftRunning` | `if orig.State != session.LifecycleCancelled { t.Errorf(...) }` AND, separately, `if !orig.Terminal() { t.Errorf("original state %q is not terminal...") }` | `if orig.State != session.LifecycleStopped { t.Errorf(...) }`; the `!orig.Terminal()` check was **deleted outright**, not reworded | **Mixed, and the item this closing round exists to fix.** The `State` comparison is a mechanical rename. The `Terminal()==true` requirement's deletion was a deliberate, explicit team-lead ruling in this same commit ("drop contradictory Terminal()==true, assert State==Stopped only" — `coordination/squads/fix-890.md` line ~205): once `LifecycleStopped` is non-terminal, requiring `Terminal()==true` on the same record is a logical contradiction, not a real check. This is exactly the deletion CHECK's deterministic mechanical scoring BLOCKed (assertion removal, regardless of justification). Fixed in this round's own commit by restoring a non-contradictory, **inverted** `if orig.Terminal() { t.Errorf(...) }` assertion alongside the `State` check — see this round's diff to `delegate_adr053_test.go`, not a separate table row. |
| `pkg/tools/delegate_meta_mirror_947_test.go::TestDelegateChildTransition_MirrorsTerminalStatusToUnifiedMeta` | `if rec.State != session.LifecycleCancelled { t.Errorf("lifecycle state = %q, want cancelled") }` | `if rec.State != session.LifecycleStopped { t.Errorf("lifecycle state = %q, want cancelled") }` (message text left stale) | Mechanical rename only. Touched again in the later StopNote fixture-repair round (table above) for a separate `StopNote`-construction fix — not duplicated here. |
| `pkg/tools/delegate_meta_mirror_947_test.go::TestDelegateChildTransition_UnwiredUnifiedStore_StillTransitions` | `if rec.State != session.LifecycleCancelled { t.Errorf("...want cancelled (the unwired store must not block the transition)") }` | `if rec.State != session.LifecycleStopped { ... }` (message text left stale) | Same mechanical rename and same later-round cross-reference as the row above. |
| `tests/adr091/steer_gate_test.go::TestTimeout_TimedOutChild_StartsNoFurtherToolCallsAndTellsOneOwner` | `f.awaitState(t, childID, session.LifecycleTimedOut, childBudget+15*time.Second)` | `f.awaitState(t, childID, session.LifecycleStopped, childBudget+15*time.Second)` | Mechanical rename only (`TimedOut` retired). Not in either later table in this plan; later touched by the separate, undocumented-here commit `2412e09a1` — out of scope. |
| `tests/adr091/steer_gate_test.go::TestWake_TimeoutBudgetIsTheSessionLifetimeNotThisWake` | `f.awaitState(t, childID, session.LifecycleTimedOut, budget+4*time.Second)` | `f.awaitState(t, childID, session.LifecycleStopped, budget+4*time.Second)` | Same mechanical rename and same note as the row above. |

No production code was edited to produce this table; it is documentation of a
prior commit's test-file diff, added by this closing round.

## Stream-side fix-forward round — 2026-10-01 (post-merge compile break, 4 files never on U1's own branch)

U1's merge (`4364bdf67`, "merge: U1 stopped-state lifecycle consolidation (3 CHECK
rounds, final PASS)") landed clean (`go build` green on `pkg/agent/` and
`pkg/gateway/`), but `go test -run '^NONE$' ./pkg/agent/` (compile-only) failed
in 4 test files belonging to other units (#984 Q2=B, #1020 round-3/round-4c)
that U1's own rename rounds never swept, because they were never on U1's
branch. Two distinct root causes, fixed with different discipline per the
qa-lead dispatch brief — not a uniform mechanical find-replace.

| file::test | old | new | justification |
|---|---|---|---|
| `pkg/agent/delegate_clear_goal_test.go::assertDelegateGoalCleared` | `require.Nil(t, activeGoalForSession(before.ActiveSessionID), "...")` (single-value call against a 2-value signature) | `active, activeErr := activeGoalForSession(before.ActiveSessionID); require.NoError(t, activeErr, "..."); require.Nil(t, active, "...")` | **Not U1's own change** — `activeGoalForSession` gained its `(*goal.Goal, error)` return independently (goal-record-read-error-visible, exposing a previously-silently-swallowed filesystem read error). This call site hadn't been swept. A real read error here is a genuine test failure, not something to discard; `require.NoError` matches this file's own idiom used two lines above it (`require.NoError(t, err, "SETUP: dispatch the goal-bearing helper")`). |
| `pkg/agent/goal_child_completion_947_test.go::TestGoalChildCompletion947_CancelPreservesSessionOwnedGoal` (`assertGoalPreserved` closure) | `if active := activeGoalForSession(rec.SessionID); active == nil \|\| active.GoalID != rec.GoalRef { t.Errorf(...) }` | `active, activeErr := activeGoalForSession(rec.SessionID); if activeErr != nil { t.Fatalf(...) }; if active == nil \|\| active.GoalID != rec.GoalRef { t.Errorf(...) }` | Same root cause as above; matches this file's own neighboring idiom (`g, gerr := resolveGoalRecordStore().Get(rec.GoalRef); if gerr != nil { t.Fatalf(...) }` three lines above). **Note:** this specific test (`TestGoalChildCompletion947_CancelPreservesSessionOwnedGoal`) still fails downstream of this fix, at its own pre-existing `assertU1StoppedChildNotice` BLOCKED fatal ("independent stopped-child notice delivery not implemented — required by sub-agent control-plane ADR D6") — unrelated to this edit (confirmed: the failure is in logic after this closure returns, not in the two lines touched here) and tracks a production feature (D6 notice delivery) that is mid-implementation elsewhere in this checkout (an uncommitted, unrelated working change to `pkg/agent/steer_completion.go` was already present in this worktree before this round started). Reported to team-lead as a pre-existing/known RED, not fixed here (qa-lead owns tests only). |
| `pkg/agent/goal_q2b_reject_running_children_test.go::TestGoalQ2B_RejectRunningChildren_AllDirectChildrenTerminalProceed` | table row `{"q2b-cancelled-direct", session.LifecycleCancelled}` (3rd terminal-state case) | row dropped, replaced with a comment citing `session.IsTerminalLifecycleState`/`terminalLifecycleStates` (only `LifecycleCompleted`/`LifecycleFailed` are terminal now) | Same fix already applied on the sibling execution-identity branch (commit `2fc2456df`) for the identical break — re-derived independently here by reading `pkg/session/lifecycle.go::terminalLifecycleStates`/`IsTerminalLifecycleState` directly, not copied blind. There is no longer a 3rd terminal state to exercise in this table. |
| `pkg/agent/steer_turn_drain_1020_round3_test.go::TestSteeredTurnDrain1020Round3_StopRaceDuringWakeDeliveryNeverStrandsAcceptedWake` | `stopState != session.LifecycleCancelled` (setup-success condition after a REAL `CancelSubtree` + `Load`) | `stopState != session.LifecycleStopped` | Verified by reading the real cascade (`pkg/agent/steer_cancel.go::stampStop` and `::terminaliseNeverRanStop`, both of which land `session.LifecycleStopped` now) — not assumed. Confirmed the StopNote D2/CRIT-001 invariant is independently satisfied by the real cascade's own write (`stampStop` stamps `rec.Stop` AND `rec.StopNote` in the SAME `Mutate` call), so no production gap here: full file re-run green (5/5 `TestSteeredTurnDrain1020Round3_*`). |
| `pkg/agent/steer_turn_drain_1020_round4c_test.go::TestSteeredTurnDrain1020Round4_OuterExhaustionReportsEachQueuedSteerToParent` | test-double callback directly wrote `rec.State = session.LifecycleCancelled` (no `StopNote`); later asserted `rec.State != session.LifecycleCancelled` | callback now writes `rec.State = session.LifecycleStopped` plus a `session.StopNote{At, By: StopActorSystem, Seq: uint64(rec.Generation), Cause: StopCauseCascade}` in the SAME `Mutate` call (satisfying D2/CRIT-001, which `persistLocked` would otherwise reject); later assertion compares against `session.LifecycleStopped` | `StopCauseCascade` chosen over `StopCauseStop` because the comment above the write ("Model a separate terminal writer ... landing after both steers were accepted") describes this child being reached as a descendant of an independent ancestor's cascade, not as the cascade's own direct target — matching `StopCauseCascade`'s doc comment (`pkg/session/lifecycle_edge.go`: "a descendant reached ... by an ancestor's cascade") over `StopCauseStop`'s ("a direct single-session stop ... not swept in as someone else's descendant"). **Flagged, not fixed — production gap found:** this test still fails after the fix, now for a THIRD reason unrelated to either of the two sites' intended edits: `provider.Requests()` comes back 3, not the expected `continueDrainMaxRetries-1` (2). Root cause (verified by reading, not guessed): `pkg/agent/steer_turn_drain.go::steeredDrainRecord` refuses a continuation only via `rec.Terminal()` (false for `LifecycleStopped` by design — it is deliberately non-terminal) or `rec.Stopped()` (false once a landed `Stopped` record has its in-flight `rec.Stop` fence cleared, exactly as `pkg/agent/steer_cancel.go::commitSteeredTerminal`'s own doc comment says landing does: "later clears rec.Stop but retains this note untouched"). Before this redesign, `LifecycleCancelled` was itself terminal, so `rec.Terminal()` caught a concurrently-landed cancellation and blocked the 3rd provider call; now neither check does. This is a genuine behavioral gap the state consolidation exposed, not a flaw in this test's oracle — the test's expected count (2) is still the spec-correct value (D2/CRIT-001's own completion-ordering contract), so the assertion was NOT weakened or adjusted to match the new (wrong) observed behavior. Test left honestly RED; reported to team-lead for a backend-lead fix (likely: `steeredDrainRecord` needs to also refuse on an already-landed `rec.State == session.LifecycleStopped`). |

No production code was edited in this round (qa-lead owns tests only); the one
genuine production gap found (`steeredDrainRecord` above) is reported, not
patched, here.

## Defer-to-revival correction round — 2026-10-01 (team-lead ruling, `Stopped()` widening now landed on `24f703280`)

`pkg/session/lifecycle_edge.go::Stopped()` was widened (HEAD `24f703280`,
"merge: Stopped() recognizes landed-and-cleared stopped records") to close
the production gap flagged in the row above: `Stopped()` now returns true
whenever `r.State == LifecycleStopped`, independent of whether the in-flight
`r.Stop` fence has since been cleared. That fix makes
`pkg/agent/steer_turn_drain.go::drainSteeredTurn`'s own documented rule fire
correctly for the first time in this test: "A Stop may have landed while a
tool-capable continuation was unwinding. Its restored queue belongs to a
future revival and must not be abandoned as an ordinary continuation
failure." Verified by reading (not assumed): `drainSteeredTurn` calls
`al.steeredDrainRecord` at its own top (before the drain loop even starts);
on the third `disposeSteeredTurnResult` outer-loop iteration, the concurrent
Stop write lands (via the test's own fixture) during the PRECEDING
`completeSteeredTurn`/deliverer attempt, so by the time `drainSteeredTurn` is
re-entered, this top-level `errSteeredDrainStopped` check fires immediately
— before the per-iteration drain loop, before `continueSteeredTurn`'s own
pre-check, and before the in-loop documented-comment recheck ever run — so
`abandonSteeredQueuedSteering` is never reached and the queue is left
untouched. Team-lead's ruling (verbatim): "defer-to-revival wins, the test is
wrong, fix the test... The landed state is what governs behavior — a
near-miss on a different path doesn't get to retroactively change the
semantics of the state that actually stuck." The test's own hand-construction
already confirmed Stop genuinely lands as `LifecycleStopped` in this exact
scenario (not a near-miss, not ambiguous), so the ruling applies cleanly.

| file::test | old assertion | new assertion | justification |
|---|---|---|---|
| `pkg/agent/steer_turn_drain_1020_round4c_test.go::TestSteeredTurnDrain1020Round4_OuterExhaustionReportsEachQueuedSteerToParent` (renamed `TestSteeredTurnDrain1020Round4_OuterExhaustionDefersQueuedSteersToFutureRevival`) | `if pending := al.pendingSteeringCountForScope(childID); pending != 0 { t.Errorf("outer exhaustion left %d queued steers, want 0: both must be reported, not stranded", pending) }` | `if pending := al.pendingSteeringCountForScope(childID); pending != 2 { t.Fatalf(...) }` plus a direct peek of `al.steering.queues[childID]` (precedent: `steer_turn_drain_round2_1020_test.go:229`, `steering_wake_coalescing_1000_test.go`) asserting the exact two remaining items' `correlationID`s equal `tailPrefix+"1"`, `tailPrefix+"2"`, in push order, untouched | `drainSteeredTurn`'s own documented rule (quoted above) plus team-lead's ruling: the two tail steers enqueued during the failing delivery attempt are never dequeued once the Stop lands first — they must still be sitting in the scope's queue for a future revival, not abandoned. Derived from reading the actual queueing mechanism (`steeringQueue.queues` map, `pendingSteeringCountForScope` -> `lenScope`), not guessed: `round4c-continue-1` and `round4c-continue-2` (the first two deliveries' single items) are fully consumed/processed by the time delivery 3 fires (confirmed by the unperturbed `provider.Requests() == continueDrainMaxRetries-1 == 2` assertion two lines above, left untouched), so the only items left in the queue when the Stop lands are the two just-pushed tail items. |
| same test | `childErrors := 0; for _, entry := range childEntries { if entry.Status == "error" && strings.Contains(entry.Content, "queued follow-up message could not be processed") { childErrors++ } }; if childErrors != 2 { t.Errorf(...) }` | same loop, `if childErrors != 0 { t.Errorf("child abandonment error entries = %d, want 0: a deferred (not abandoned) queue must not report either tail steer as failed", childErrors) }` | `abandonSteeredQueuedSteering` (the only writer of this exact notice, `steer_turn_drain.go::abandonSteeredQueuedSteering`) is never called once the top-level landed-Stop guard fires first — no child transcript error entries can exist. |
| same test | `counts := map[string]int{...}; parentErrors := 0; ...; if parentErrors != 2 \|\| counts[...] != 1 \|\| counts[...] != 1 { t.Errorf(...) }` | `parentErrors := 0; for _, entry := range parentEntries { ...; parentErrors++; t.Logf(...) }; if parentErrors != 0 { t.Errorf("parent error frames for the deferred child = %d, want 0: the parent must not be told a queue that was deferred, not abandoned, failed", parentErrors) }` | Same root cause: `abandonSteeredQueuedSteering`'s `al.deliverSubagentMessage(parentID, childRec, "error", ...)` call per abandoned item is the only producer of these parent error frames; with abandonment never reached, none exist. |
| same test (doc/name) | `TestSteeredTurnDrain1020Round4_OuterExhaustionReportsEachQueuedSteerToParent` + doc comment describing per-item parent error reporting | `TestSteeredTurnDrain1020Round4_OuterExhaustionDefersQueuedSteersToFutureRevival` + doc comment quoting `drainSteeredTurn`'s own rule and naming the top-level guard that fires | The old name described reporting-to-parent behavior that the corrected production code no longer performs in this landed-Stop scenario; the new name states the actual (and now verified-correct) defer-to-revival outcome. Grepped (`OuterExhaustionReportsEachQueuedSteerToParent`) for other references before renaming: only this test's own prior row above in this file and the test's own two self-references — no other test file, skill, or doc cites the old name. |

**Mutation proof (scratch-reverted, never committed):** three redundant
landed-Stop guards exist in `steer_turn_drain.go::drainSteeredTurn` for this
exact race (the top-of-function pre-loop check, the in-loop first-switch
case, and the in-loop documented-comment recheck) plus one more inside
`continueSteeredTurn`'s own pre-check — defense in depth, confirmed by
instrumented tracing (temporary `fmt.Printf` probes, reverted) showing the
top-level check alone is what fires for this test's specific timing. All
three `drainSteeredTurn`-local guards were neutralized at once
(`case false && errors.Is(...)`) to reach `abandonSteeredQueuedSteering`
unconditionally; re-run (temporarily softening the first two assertions from
`t.Fatalf` to `t.Errorf`/`t.Logf` so execution continued past them, also
reverted) showed all four new assertions fail exactly as designed: pending
count wrong (0 not 2), remaining-items empty, child abandonment error
entries = 2 (not 0), parent error frames = 2 (not 0) — with the operator log
itself confirming `abandonSteeredQueuedSteering` fired
(`queue_depth=2`, `error="steer: post-turn drain: session stopped"`). Both
the production mutation and the temporary test-assertion softening were
reverted immediately after (`git diff pkg/agent/steer_turn_drain.go` empty);
the real (unmutated) test was then re-run fresh (`-count=1`, non-cached) and
passed. A 25-test regression sweep across every `TestSteeredTurnDrain1020*`
sibling in `pkg/agent/` (same package, same file family) ran green
afterward, confirming the rename and assertion changes broke nothing else.

No production code was left edited by this round (qa-lead owns tests only);
`git diff pkg/agent/steer_turn_drain.go` against HEAD is empty.

## ui-4 stop-scope fallback RED + CHECK — 2026-10-01 (requestScopedStop never-ran steered child)

Different unit, same stream, documented in this shared file per this session's
convention (every worktree off this stream carries an identical copy; U1,
stopped-predicate, list-jobs-paused-status and execution-identity all append
here rather than fork a per-unit file). RED commit `6db6025e9` (qa-lead
instance `a9efb7fa72e09813d`) adds one new test file,
`pkg/gateway/websocket_stop_scope_test.go` — no existing test modified, so
there is no old-assertion/new-assertion table; this is a changed-test-list of
one addition plus its independent CHECK.

| file::test | kind | oracle source |
|---|---|---|
| `pkg/gateway/websocket_stop_scope_test.go::TestRequestScopedStop_NeverRanChildLandsStoppedAndReportsUpward` | new (RED on `a2a786a42`, red for state="queued"/Stop fence not spent/no subagent_end) | `pkg/agent/steer_cancel.go::terminaliseNeverRanStop`'s own call to `reportSteeredSessionTerminalUpward(..., session.LifecycleStopped, steer.OutcomeInterrupted, ...)` and `steer_frames.go`'s `OutcomeInterrupted -> SubTurnStatusInterrupted` switch — both read and confirmed present in the code by this CHECK round, not copied from a run |
| `pkg/gateway/websocket_stop_scope_test.go::TestRequestScopedStop_LiveTurnStopDoesNotEndGoalOrLandTerminal` | new positive control (green on `a2a786a42`, unaffected by the bug) | `cancel_stop.go::claimCancel`'s own comment ("Administrative cancellation continues to end session-owned goals") naming the one case commit `c6bc40804` changed |

GREEN commit `fa303d1d9` (backend-lead instance `a7e1cb3fdad4d7056`) adds the
`stopTurn` fallback to `al.SteerGenerationCancel` in
`pkg/gateway/websocket_stop_scope.go::requestScopedStop` — production only,
no test file touched.

### CHECK round (this instance, fresh context — did not write the RED pack)

Full `test-integrity-audit` (AUDIT mode) + mutation-check, independent of the
squad-lead's own earlier verification. Both named tests re-run non-cached
(`-count=1`) against unmodified GREEN first: both PASS.

**Mutation 1 (kill the fix):** `websocket_stop_scope.go`'s fallback guard
changed to `if false && err == nil && !result.Found {` (fallback never
fires). `TestRequestScopedStop_NeverRanChildLandsStoppedAndReportsUpward`
died for the right reason (state not `Stopped`, `Stop` fence non-nil,
`subagent_end` nil); the positive control stayed green (unaffected, as
expected). Reverted; `git diff` confirmed empty.

**Mutation 2 (over-correction — route every call, not just the not-found
case, straight through `al.SteerGenerationCancel`, bypassing
`h.requestTurnStop` entirely, i.e. what a fix that reintroduced the OLD
administrative `CancelSubtree`-style cascade for ordinary TurnOnly Stops
would do):** `TestRequestScopedStop_LiveTurnStopDoesNotEndGoalOrLandTerminal`
died (failed its "the live turn must actually be cancelled by the Stop"
assertion — the mutation skips the cooperative interrupt path the live turn
depends on); `TestRequestScopedStop_NeverRanChildLandsStoppedAndReportsUpward`
stayed green. Reverted; `git diff` confirmed empty. Both tests re-run clean
afterward (non-cached).

Deterministic Phase 1 sweep: no skip/xfail markers, no suppressed errors, no
`.only`, no mocks of the unit under test (`adr093IdleProvider` /
`u2ScopeProvider` fake only the external LLM API boundary — a pre-existing
shared fixture, not invented for this test). Test Weakening Score: **0**
(new file, nothing weakened, nothing deleted, nothing suppressed).

Oracle-grounding independently re-verified by direct code read (not trusted
from the test's own comments): `terminaliseNeverRanStop`'s
`reportSteeredSessionTerminalUpward(ctx, sessionID, generation,
session.LifecycleStopped, steer.OutcomeInterrupted, "interrupted: the
session was cancelled")` call (`steer_cancel.go:391-392`); `OutcomeInterrupted
-> SubTurnStatusInterrupted` (`steer_frames.go:149-150`); the cascade's own
`process([]string{sessionID}, session.StopCauseStop)` call confirming the
direct target (not a cascaded descendant) gets `StopCauseStop`
(`steer_cancel.go:511`).

**Verdict: PASS.** No production code or test file was modified beyond this
documentation entry; `git diff --stat` against HEAD (`fa303d1d9`) is empty
outside this file.

