# Browser CI corrections after PR 685

Scope: browser package and CDP pipe subpackage, excluding the WebRTC relay.
The CI logs are the regression baseline; no test assertion, duration or threshold
is relaxed to turn a failing run green.

## Collected failures

The lease structural test counted `startupCohort.join` as a second exclusive
lease because both APIs return a function and a boolean. That exact method
registers concurrent startup waiters: its closure removes one waiter and does
not acquire the browser write lease. The scanner now exempts only the exact
file, pointer receiver and method. An additional acquisition-shaped function,
even in that same file, must still fail the original exactly-one assertion.

Three recapture tests failed from the shared fake browser's incomplete document
protocol. The fake returned successful GetFrameTree/paint actions without
returning a frame. Initial discovery could leave measured geometry pending;
same-index recovery consequently refused the unmeasured picture. Two race
reports independently showed the tests replacing `lv.runCDP` while the already
started discovery goroutine was reading it.

The shared fixture now installs its executor before actual AttachContext,
answers document frame/isolated-world/paint calls at the protocol boundary,
and awaits completion of initial metadata discovery before starting capture.
The resize fixture forwards document commands through that executor. Real tab
callbacks, target selection, frame transitions and qualified ingest recapture
remain in use. Original exact counts, order and measured-size assertions remain.

## Mechanical lint corrections

Errors use distinct lexical variable names; interface parameters are named;
fixture type assertions are checked and still fail loudly on a wrong type;
context keys have their own type; redundant assignments and proven-unused
helpers are removed. Two repeated stateful failure reports are assigned to
separate variables so both invocations and their acceptance remain explicit.

Narrow documented exceptions preserve intentional absent optional document
work (`nil,nil`) and original-context retention that the fatcontext linter
mistook for repeated context derivation. No blanket linter or structural-guard
exclusion was added. Frame-wait initialization still creates a root lifetime
only while absent, never a chain across loop iterations.

## Evidence and validation scope

CI baselines are retained in:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/ci-go-tests.log`,
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/ci-race.log`, and
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/ci-linter.log`.
The race stacks identify concurrent command-handler replacement in the two
recapture fixtures, not a speculative race inferred from source alone.

Local initial focused race session 29634 exited 0 (4.836s), covering seven
lease/recapture groups with no race reports. An earlier local compile attempt
10361 failed from a mechanical declaration edit and is not behavioral evidence.
Final package lint, deliberate alternate-acquirer rejection and restored
focused checks are recorded below after their authoritative terminal results.

Final pinned golangci-lint 2.10.1 session 55411 exited 0 with `0 issues` for
browser and cdppipe, clearing the 87 assigned findings across 34 CI-listed files.
Final driver 28488 exited 0: the injected `unexpectedAdditionalLease` function
in the otherwise-exempt cohort file failed the exact guard with two acquisition
symbols. The source was restored byte-for-byte before the final tests.

Restored race/shuffle checks passed 11 browser groups/13 records (4.533s) and
three pipe groups/three records (2.035s), with zero skips, failures or race
reports. The selected browser groups cover all collected lease/recapture
failures plus preserved navigation cancellation/rejection and repeated health
failure handling. The pipe selection covers canceled process reaping,
pre-canceled no-spawn and accepted browser lifetime.

Raw local evidence is retained beneath:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/startup-ci-browser/`.

A separate read-only reviewer checked the exact lease exception, pre-Attach
executor/discovery ordering, resize document forwarding and production shadow
renames without finding an actionable issue. This was a bounded correction
review, not an independent audit of every mechanical test edit. Local Darwin
checks do not replace the integrated Linux CI rerun or real-browser acceptance.
