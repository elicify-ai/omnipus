# Capture-stage failure tracker validation

Scope: the private gateway tracker in `pkg/gateway/browser_capture_health.go`.
The root integration owns watchdog wiring, atomic observation ownership, and
recovery-start packet baselines.

## Contract and independent expectations

The review reproduced a finite repaint that advances source frames from 20 to
21 while encoding remains at 20. Subsequent fresh, complete observations of
21/20 must retain the unresolved failure; lack of a second repaint is not
recovery. The same rule applies to encoded frames waiting for sending and
sent packets waiting for receipt at the relay. This receipt is independent of
forwarding to viewers and does not prove presentation.

- Encoding failure clears when measured encoded frames advance.
- Sending failure clears when measured sent packets advance; that may reveal
  a separate unresolved relay-delivery failure.
- Relay failure clears only through `noteRelayProgress()`, which returns the
  updated verdict for the current watchdog tick.
- Binding epoch or local generation changes, and counter rollback, reset the
  comparison and all old counter evidence.
- Stale, muted, or incomplete required telemetry discards counter latches and
  their comparison baseline. Hidden failure through such unknown intervals is
  not claimed to be covered. A later complete sample establishes a new baseline.
- Fresh ended/absent track and failed/closed peer state remain independent
  positive failure evidence even if counters are missing or the track is muted.
- Repeated polling of the same fresh snapshot retains evidence. A newly
  received counter sample with a nonadvancing sample timestamp is not fresh
  counter evidence and resets the counter latch.

## Test plan

Use the real tracker and explicit supplied times; no clock sleeps or mocks.
Pure tracker cases live in `pkg/gateway/browser_capture_health_tracker_test.go`.
Mutation runs compile that test file with the actual production health source;
the normal gateway package health/watchdog selection is still required afterward.
Exact error strings come from the existing stage contract. Cases include each
of the three finite-repaint stages, zero counters, downstream catch-up, current
relay progress, repeated polling, generation reset, binding reset with equal
local generation, each counter rollback, missing required and irrelevant
counters, mute/unmute, absent observations, repeated/regressed sample times,
and the stale threshold immediately before, exactly at, and immediately after.

Mutation candidates: erase a latch on every idle sample; ignore binding resets;
ignore counter rollback; keep evidence through missing/muted telemetry; ignore
relay progress. Each must produce a behavioral assertion failure, then be
restored before the final run.

## Execution evidence

Dependencies picked into the UI worktree: root `c0a8fb833`, `212a75a03`,
`bf99b5dae`, and metadata-only `447e0538f`. Production binding stamping and
watchdog integration remain owned by the root lane.

The first test attempt failed at setup because this worktree lacked the ignored
`pkg/gateway/spa` embed directory. Copied the existing root-built SPA into that
ignored build directory; no tracked product code or running instance changed.

The retry used `CGO_ENABLED=0 GOMAXPROCS=2 go test -tags goolm,stdjson -p 1
./pkg/gateway -run 'TestCaptureHealthTracker' -count=1` and exited 1. Eight
behavioral cases failed against the stateless baseline: the three finite-repaint
stages, valid zero counters, same-generation binding replacement, unrelated
relay progress, and the two still-fresh age boundaries. The baseline delegated
to the unchanged classifier, so the failure demonstrated loss of state rather
than a missing symbol or import. Other negative controls already passed against
that baseline; mutation evidence is still required before claiming verification.

The real production source plus pure tracker test file passed all 27 leaf cases:

```sh
CGO_ENABLED=0 GOMAXPROCS=2 go test -tags goolm,stdjson -p 1 pkg/gateway/browser_capture_health.go pkg/gateway/browser_capture_health_tracker_test.go -count=1
```

Initial green exited 0 (2.974 seconds of test execution). Six deliberate source
mutations then each exited 1 with behavioral equality failures, not build errors:

| Mutation | Failing cases |
| --- | ---: |
| Erase pending evidence on the next idle observation | 6 |
| Ignore binding changes with equal local generation | 1 |
| Ignore counter rollback | 3 |
| Keep a pending failure through muted telemetry | 1 |
| Keep encoding evidence when required counters disappear | 2 |
| Ignore measured receipt at the relay | 1 |

Every mutation was restored, including on failure. The same command then passed
again, exit 0 (1.705 seconds). Tests also verify detection can resume after an
unknown interval; resetting evidence must not disable future failure detection.
The normal gateway package health/watchdog selection remains pending root
integration and the server binding-epoch producer update.


## Scope verification and integration handoff

GitNexus impact attempts for the tracker symbols returned target-not-found with
UNKNOWN risk because the available index predates these additions. Manual
callsite analysis places the private tracker in the watchdog and its focused
tests. The required `detect_changes --scope all` attempt exited 1 because this
UI worktree is not registered; the parent will repeat scope analysis after
integration. Manual diff inspection and `git diff --check` passed; `gofmt -l`
reported no files.

The parent explicitly accepted this independent tracker commit before its
server binding-stamping update. The parent owns the corresponding exact wire
snapshot expectation update (`BindingEpoch: first`) and the final combined
gateway health/watchdog package run. This helper verification is not a claim
that the production watchdog integration or the final full-diff review is done.
