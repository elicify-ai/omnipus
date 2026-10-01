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

