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
