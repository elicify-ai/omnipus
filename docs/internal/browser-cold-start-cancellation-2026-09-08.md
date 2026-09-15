# Cold browser startup cancellation — implementation evidence

Status: implementation and focused fault verification complete; related regression and race verification pending.

## Observed failures

The caller-limited initial capture path reached `BrowserManager.Session`, then
pool/coordinator registration with `context.Background()`. The actual managed
pipe launcher also discarded its caller context. Passing context only through a
fake launcher would therefore miss the real subprocess cancellation defect.

Observed tests in the isolated `browser-improvements-startup` worktree:

- Browser startup red, retained process handle 74316, exit 1, 10.400 seconds:
  pool/coordinator cancellation, joined follower survival, late publication,
  precanceled registration, and cold manager/first-attach failures reproduced.
  The selection contained 12 pool/coordinator and five manager/attach cases;
  positive controls are not counted as failures.
- Pipe startup red, handle 82837, exit 1, 4.182 seconds: caller cancellation
  waited 2.984851473 seconds for the probe deadline and returned the deadline
  error; a precanceled request spawned a child. The accepted-browser lifetime
  control passed. All child processes were reaped.
- Manager shutdown red, handle 37353, exit 1, 2.668 seconds: shutdown failed
  to cancel the pending request promptly; the late launch republished
  `started=true` after shutdown. The fixture subsequently released and drained
  the blocked launch.

Artifacts are under
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/startup/.startup-probe/`.
Subprocess temporary directories are beneath that same project directory.

## Intended lifetime boundary

`SessionContext` uses caller-cancelable tab admission. A request still queued
for a tab command has not joined a startup cohort. Once admitted, its cold
creation is also canceled when lifecycle removal retires that tab-command gate.
The existing `Session` API explicitly retains its legacy background lifetime.

A pool/coordinator launch has an independent temporary context shared by its
joined original callers. Cancellation removes only that caller. A still-live
joined follower keeps the original launch alive; a sole canceled request cannot
publish its late result. Publication checks original caller contexts directly,
not merely whether scheduled cancellation callbacks have run. An abandoned
cohort drains its resources before a replacement launch uses the same profile.

The pipe allocator distinguishes temporary handshake cancellation from the
accepted browser's lifetime. Canceling the request before acceptance tears down
and reaps the child; canceling it after acceptance must leave the browser usable.
Browser/tab creation similarly stops its temporary caller link before returning
a healthy target.

## Scope and pending checks

The legacy manager-without-coordinator fallback now shares one startup cohort
outside the manager mutex. Both Session APIs inherit gate retirement for creation;
legacy Session retains its existing admission timeout and background caller policy.
The original gate is checked synchronously even before its cancellation callback
runs. A dead root cannot be accepted as a warm coordinator.

Additional red handle 81880 exited 1 in 4.433 seconds: all five local cancellation,
contextual/legacy shutdown, gate retirement, and dead-root acceptance cases failed.
The dead-root case also proved that a false warm state prevented a healthy retry.

Focused green evidence:

- 83079: 20 shared startup/manager/cohort leaves passed in 7.639 seconds.
- 48401: the five additional formerly red cases passed in 3.153 seconds.
- 67272: all three real subprocess checks passed in 0.936 seconds. The blocked
  handshake test took 0.02 seconds including process setup and reaping; this is
  not a measured Chrome startup latency.
- Mutation driver 57846 exited 0: all 13 deliberate behavioral faults were caught.
  Restored browser startup tests passed in 16.131 seconds; restored pipe tests
  passed in 1.494 seconds. Faults covered original waiter checks, live follower
  survival, last-caller cancellation, exact completion ownership, temporary pipe
  lifetime, Session context forwarding, gate retirement, local child disposal,
  dead roots, first attach cancellation, and returned target lifetime.

No installed Chrome instance was restarted or modified. No real-browser speed
improvement is claimed by these subprocess and protocol-boundary tests.

## Change-impact check

GitNexus queries against `omnipus-browser-improvements` reported LOW impact for
indexed startup symbols. `Session` had two direct callers/five total affected
symbols (including gateway execution), `ensureStarted` three/seven,
`createFirstTab` three/six, and `createTab` four/ten. Pool launch had three direct
callers/fourteen affected symbols; coordinator launch had one/five. Coordinator
shutdown had one direct caller/seven affected symbols, including gateway
service lifecycle. The pipe allocator had one direct caller/one affected symbol.

The recently added manager tab-command gate helpers are absent from that index;
manual call tracing identified manager mutation and lifecycle paths. That index
limitation was reported before the approved gate edits. The owning parent keeps
panel teardown and live frame-transition changes separate from this lane.

## Related regression check

Handle 72205 exited 1 in 26.365 seconds. Of 78 related leaf cases, 76 passed;
only the two admitting cases in `TestPool_PressureGateAtTheBoundary` failed.
Their 300ms request deadline expired during the fake binary's OS version probe,
which took about half a second on this run. The former synchronous launch had
ignored that caller deadline. The correction seeds that fixture's known binary
version in the existing version cache; it preserves the 300ms deadline, exact
byte boundary, and launcher-count assertions. Only this three-case test will be
rerun, with an exact-floor fault check; the other passing related tests will not
be repeated before final integration.

## Final review: delayed death notification

A rejected startup may already have armed `watchForCrash`. Its old
`Browser.LostConnection` notification can be delivered after a replacement is
installed. The old watcher checked only `launched`, so it could retire the new
root rather than the browser whose death it observed. Controlled red handle
36837 exited 1 in 1.986 seconds: the replacement was canceled and an erroneous
relaunch occurred, while the genuine current-browser crash control passed.
The guard now checks the installed Browser identity before clearing any state.
Closeout driver 56797 exited 0. The focused watcher passed in 4.859 seconds;
both accepting the old browser and ignoring the current browser were caught as
deliberate faults. The corrected three-case pressure fixture passed in 4.224
seconds and caught the exact-floor off-by-one fault. Restored focused startup
race/shuffle checks passed: 35 browser leaves in 52.552 seconds and 16 pipe
leaves in 4.695 seconds, with no
skips or race warnings. No broader tests were repeated. GitNexus reports LOW
impact for the watcher: one direct caller/four affected symbols, no indexed
execution processes. No real browser is needed for this identity test.

## Commit scope check

The staged GitNexus `detect_changes --scope staged` attempt (98829) exited 1:
this isolated worktree is unregistered and the tool could not select among the
machine's indexed repositories. It was not redirected to the root checkout,
which would inspect a different working tree. The staged diff contains only
startup cancellation, its direct manager/pipe boundaries, fixtures, and this
report; `git diff --cached --check` passed. The parent will inspect the final
integrated graph and run full CI there.
