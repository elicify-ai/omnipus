# Browser startup implementation and evidence

## Contract and scope

The pipe allocator must reject a browser that exits before its first browser-control response promptly, preserve its exit status, stop and reap on parent cancellation, and preserve caller-owned profiles. One process waiter owns `exec.Cmd.Wait`. A live browser that never responds still receives the configured readiness deadline. Returned diagnostics must not disclose raw Chrome stderr, which can contain private paths or URLs.

Changes are confined to the pipe allocator and its tests. No security or GPU launch flags, executable resolution, production profiles, or service configuration are changed.

## Tests planned before implementation

Use a real helper subprocess as the external process boundary; the allocator, operating-system pipes, bridge, browser-control request, context cancellation, and cleanup remain real.

| Case | Required outcome |
|---|---|
| Child exits with status 37 and profile diagnostic | Fail before a three-second probe budget; retain typed status 37 and safe fixed diagnostic |
| Child exits with status zero before readiness | Explicit exit-before-readiness error |
| Live child keeps pipe open without replying | `context.DeadlineExceeded` |
| Ready child, then parent cancellation | Owned temporary profile removed, process reaped, concurrent cancel calls finish |
| Ready child with caller-owned profile | Cancellation preserves marker content exactly |
| Stderr sizes 0, 1, 4095, 4096, 4097, 8192 | At most 4096 newest bytes retained; only fixed classifications returned |

Timing tolerance: subprocess exit must complete within one second, allowing scheduling while decisively rejecting the former three-second probe timeout. The helper disables only the race detector's artificial one-second post-exit sleep, retaining race checks.

Planned mutations: remove process-exit cancellation, remove safe diagnostic from startup error, remove parent-cancellation cleanup, and exceed the diagnostic memory budget.

## Initial red evidence

`go test -tags 'goolm stdjson' ./pkg/tools/browser/cdppipe -run 'TestStartup' -count=1` exited 1. Exit-status and clean-exit tests each waited about three seconds and incorrectly returned `context deadline exceeded`. Parent cancellation left the allocator-owned temporary profile. The existing silent-child deadline behavior passed; it is a preservation check, not a newly reproduced defect.

## Correction to the incident attribution

At 16:46 Jakarta, the unchanged allocator successfully launched the actual installed Chrome 151.0.7922.77 bundle with an isolated new profile and only `--headless=new` as an additional flag. Startup took 3.371 seconds and browser evaluation of `1+1` returned exactly 2. macOS display-link warnings occurred, but did not prevent readiness or evaluation.

The same Mach rendezvous/parent-died messages appeared **after successful evaluation during teardown**. This proves those messages alone cannot establish the cause of the earlier production startup timeout. Earlier production messages referenced rendezvous server PID 1, consistent with orphaned helpers, but the original main-process exit status was discarded by the old allocator and remains unavailable.

Production was not restarted, its profile was not modified, and no speculative Chrome flags were introduced. The corrected allocator will preserve actionable evidence for a recurrence. The exact historical startup cause remains unverified.

Platform boundary: the subprocess tests use inherited file descriptors 3/4, the production pipe allocator mechanism. Go does not support `exec.Cmd.ExtraFiles` on Windows; these process tests are therefore selected only on non-Windows platforms, matching the existing allocator test boundary. Windows browser transport support is not established by this work.

## Implementation validation

Focused startup tests passed with the race detector (exit 0). The complete pipe package then passed with the race detector and shuffled test order (exit 0, 8.528 seconds). Four independent mutations were applied and all were caught:

| Mutation | Observed failure |
|---|---|
| Remove process-exit cancellation | Exit fixture consumed 3.018 seconds instead of failing promptly |
| Omit diagnostic classification | Exit-status fixture reported the missing safe profile diagnostic |
| Remove automatic connection/context cleanup | Parent cancellation left the owned profile behind |
| Double retained diagnostic budget | 4097/8192-byte cases and newest-tail retention exceeded the 4096-byte contract |

All mutations were restored before the final full-package race run. The existing global dialer test initially broke subsequent allocator tests by restoring the dialer callback while leaving its one-time installation consumed. Its original assertions now run in a dedicated subprocess; no assertions were removed or weakened. This allows allocator tests to run independently and in shuffled order.

GitNexus impact against the registered installed-source checkout classified allocator creation, launch, teardown, diagnostic writer, and the modified tests as LOW risk. Creation/start/teardown each have one direct caller; no indexed execution processes were reported. `detect_changes --scope all` was attempted for this worktree and failed because it is not registered. Manual diff verification confines changes to the allocator, its tests, and this report; integrated index verification remains the parent branch's gate.

The final allocator also launched the same installed Chrome 151 bundle with a second fresh isolated profile and returned `1+1 = 2` (process exit 0). Launch took 6.343 seconds at 17:05 Jakarta. This and the earlier 3.371-second sample were taken under uncontrolled concurrent machine load; they do not establish improved or regressed cold-start performance. Display-link warnings continued, and Mach parent-died warnings again appeared during teardown after successful evaluation. Repeated controlled end-to-end performance measurement remains necessary.
