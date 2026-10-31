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

## Step 1–3 authoring evidence (partial pack)

- Core RED: `/workspace/logs/t10-red/step1-core-red-rerun.log`, exit 1. Missing `JobSpec.Kind`, retained retired/unknown jobs, missing named load-drop logs, fixed-capacity fields/defaults/semaphore. Dispatcher and memory-provider adapters are **binding gaps**, not behavioral coverage. Preserve-behavior retry/retention/no-deadline rows PASS. Targeted coverage: config 2.6%, cron 42.6% (not whole-suite coverage).
- Loop/task RED: `/workspace/logs/t10-red/step1-2-agent-red.log`, exit 1. Actual missing initial delay, stale callback execution, worker refusal missing, and task reconcile deletes foreign jobs. Recovery/post-return/single-write/G5 child/audit adapters are loud **uncovered binding gaps**. The initial task-failure fixture lacked workspace_id; that was a test defect, corrected before final rerun (not a production RED claim).
- Production boot/routing: `/workspace/logs/t10-red/step1-3-gateway-red-rerun.log`, exit 1, targeted coverage 1.8%. Actual private engine identity and auxiliary file access failures; actual heartbeat reconciler prunes a foreign loop named with heartbeat prefix; all four creating producers omit Kind; removed routes/types remain. Checked-load abort, internal heartbeat dispatch, generic JSON API fallback with SPA/CSRF controls, and failure-notifications REST PASS on this base.
- Additional premise drift: the generic `/api/` JSON 404 fallback already exists in `/workspace/wt-t10/pkg/gateway/gateway_boot.go` at lines 1410–1422. S-G.5 is a passing characterization. The spec premise that deleting schedules must otherwise fall into SPA HTML is not true on this base. No false RED is manufactured.
- Targeted existing regressions: `/workspace/logs/t10-red/step1-2-existing-targeted.log`, exit 0: 12 named tests in config/cron/agent, including Stop/runLoop, retry, real loop progression/cap/stop/chaining, once trigger, reconciliation, and restore safety. This does NOT claim the full existing suites passed.
- Nonbehavioral attempt receipts retained: `/workspace/logs/t10-red/step1-loop-initial.log` (test pointer compile defect); `/workspace/logs/t10-red/step1-core-red.log` (runner output directory absent); `/workspace/logs/t10-red/step1-3-gateway-red.log` (test used removed UserConfig.Role). Corrected runs above supersede them; failures are not counted as behavioral RED.

## Self-check

Tests and clearly marked compile scaffolding only; no production Go/TypeScript changed; no existing test weakened, skipped or deleted; no push. The pack is still partial. Binding/dependency declarations are FAIL, never skipped, but do not count as completed behavior tests. Remaining scenario/dataset gaps will be enumerated rather than claimed green.
