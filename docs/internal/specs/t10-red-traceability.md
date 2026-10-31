# T10 RED traceability — final specification r6

Source of behavior: `/workspace/omnipus-coordination/designs-20261029/schedules-t10-spec-r6.md` only. T8 inheritance is pinned to `/workspace/omnipus-coordination/designs-20261029/tool-parity-spec-r17.md` and `/workspace/omnipus-coordination/designs-20261029/t8-rebase-note-6b-replay.md`. All historical B-only content is in scope (founder answer C). No production implementation is supplied by this test author.

## Base and premise drift

- Requested new branch `landing/t10`, worktree `/workspace/wt-t10`, created after fetch from `origin/release/v0.1.1` at `1d3989df060887c9afe37c367562ce6975550724`.
- The spec's code pin is `da7c1d941`; the release base now includes the 6a shutdown fix: `/workspace/wt-t10/pkg/gateway/shutdown.go` already stops task-trigger and loop schedulers before draining. The claim that #1490 is untouched is stale on the requested base. Step 0 is a passing characterization, NOT a manufactured RED delta.
- `/workspace/wt-t10/pkg/agent/task_trigger.go` also now stops its engine before joining the recovery sweep. Saved diff: `/workspace/logs/t10-red/spec-pin-drift.diff`.
- T8's specified backend is not present on this base. A test labelled **needs T8 backend** is a loud failing dependency declaration, not behavioral coverage or a passing substitute.
- A fresh checkout lacks the embed input directory. `/workspace/wt-t10/pkg/gateway/spa/index.html` is **RED compile scaffolding only**, copied from the separately marked fixture `/workspace/wt-t10/pkg/gateway/testdata/t10-red-spa/index.html`. It is NOT a compiled SPA; remove/replace this scaffold with actual frontend build output before release. Router tests can prove JSON fallback versus HTML, but cannot claim browser redirect or built-SPA acceptance from this fixture.

## Step 0 evidence

| Run | Saved log | Exit | Result |
|---|---|---:|---|
| Original #1450 repro, count 150, cpu 1 | `/workspace/logs/t10-red/step0-1450.log` | 1 | Infrastructure failure: missing embed input. Not behavioral RED. |
| Repro with embed fixture | `/workspace/logs/t10-red/step0-1450-with-embed-fixture.log` | 75 | Command passed on runner, copy-back failed. NOT green. |
| Repeated repro with tracked compile scaffold | `/workspace/logs/t10-red/step0-1450-rerun.log` | 0 | Verified 150 executions of the spec's named repro. |
| Shutdown characterization | `/workspace/logs/t10-red/step0-shutdown.log` | 0 | Real timer stores unchanged after shutdown and while drain held; budget cancellation and resource close asserted. |

Issue closure is left to the chief: this task changes tests only and does not mutate GitHub issues or push.

## Scenario evidence (populated by subsequent build-order commits)

| Scenario | Test | File | Failing reason / status |
|---|---|---|---|
| S-C.1 | `TestShutdown_EngineStopsBeforeDrain` | `/workspace/wt-t10/pkg/gateway/t10_shutdown_test.go` | PASS characterization; #1490 already implemented on requested base. |
| S-C.2 | `TestShutdown_EngineStopsBeforeDrain/existing-budget-cancels-wait-and-closes-loop` | `/workspace/wt-t10/pkg/gateway/t10_shutdown_test.go` | PASS characterization; bounded cancellation and close, not a no-error-only test. |

## Self-check

Tests and clearly marked compile scaffolding only; no production Go/TypeScript changed; no existing test weakened, skipped or deleted; no push. Remaining RED work is in progress, not claimed complete by this initial step-0 commit.
