# Manager lifecycle and launch security regressions

Base: `c11facff8`. This batch implements the independent manager review; the
live-view/frame/navigation changes are owned separately.

## Independent expectations

The source of the expectations is the browser-improvements FR-016 cancellation
and profile-integrity requirement, ADR-075 workspace-profile isolation and
deletion rules, and the documented `TrustPathChrome` security contract.

| Case | Required observable outcome |
| --- | --- |
| Untrusted PATH candidate, valid packaged Chrome | Candidate is never executed, including for a version probe; packaged path is returned. |
| Explicitly trusted PATH candidate | Candidate executes its version probe and its path is returned. |
| Held Unix launch lock, no ownership marker yet | Second launch is refused; locked inode remains linked at the same path. |
| Reload with a different template profile root | Each existing workspace retains its profile identity; cleanup still addresses that profile; runtime TTL updates. |
| Shutdown during legacy first OpenTab launch | Launch receives cancellation, no browser/session is published, rejected browser is disposed. |
| Close during a pending pool launch | Launch is retired and drained before Close returns; it cannot publish a live instance. |
| Two Close calls overlap | Both return only after process teardown; ordinary close permits a later reopen. |
| Deleted workspace, retained caller | Acquire refuses; it cannot recreate the removed profile. |
| New tool call during process teardown | It cannot obtain the old tab; waiting respects its own cancellation. |
| Popup with another tab set's opener | Only the opener's actual tab set adopts it. |
| Call/viewer/tab appears after victim selection | Final retirement claim refuses; the live instance remains available. |
| Memory is unmeasurable while a browser is exiting | Existing one-browser floor refuses another launch until exit completes. |

## Boundaries and proof

The manager, coordinator, resolver, pool, command gates and startup cohorts
remain real. Tests replace process launch/CDP at existing process boundaries,
use real temporary filesystem state, and control ordering with channels.
Popup-event quiescence uses Go's synchronization test support rather than a
wall-clock delay as evidence that no adoption occurred.

First run these regressions against unchanged production code. Compilation or
fixture errors do not establish a behavioral red. Then implement finite
launch-security, manager-lifetime and pool-retirement commits. Deliberately
restore faults in at least three independent protections and require their
specific regressions to fail, restore all faults, and run the affected tests
with race detection. Record actual commands, exit codes and collected cases
in the final validation evidence.

The team serializes Go test/build work. Full CI, real Chrome profile persistence,
cross-platform process locking and end-to-end UI acceptance are not established
by this focused batch. Independent review is required after implementation.

## Lifecycle design constraints

- Normal close is temporary; workspace deletion permanently retires that key
  within the pool's lifetime.
- Key retirement and pending-startup publication must agree under the pool
  mutex. Process cleanup and waiting must not hold that mutex.
- A new call must not use a manager's old sessions after its instance is
  selected for retirement. Admission and victim selection need a shared
  synchronization boundary.
- Repeated close joins the original teardown rather than inferring process
  death from absence in the live-instance map.
- Profile-root changes remain restart-scoped, matching existing operator
  notices; reloaded runtime settings still take effect.

## Initial observed failures

The first focused run exited 1 and reproduced untrusted PATH execution,
markerless held-lock bypass, and foreign popup adoption. Its new popup fixture
then failed cleanup because fake chromedp contexts were not shut down inside
the synchronization-test scope; later cases did not run. Manager cleanup was
added without changing the expected behavior.

The second focused run collected all nine original top-level cases and exited
1. Trust/lock/popup corrections passed, including the trusted-PATH positive
control. All six unchanged lifecycle cases failed for their intended behavioral
reason: profile identity changed, legacy startup was not canceled, pending Close
did not retire startup, repeated Close returned early, a deleted key reopened,
and a new call obtained the retiring tab.

Final-claim and exiting-process-floor tests were added with the new retirement
helper. Their proof comes from deliberate guard-removal faults, not a claim
that they ran against a helper that did not previously exist.

## Focused implementation evidence

The restored implementation passed all 11 new top-level regression groups,
including the two PATH controls and three final-claim activity subcases:
`go test -tags goolm,stdjson -p 1 ./pkg/tools/browser -run '<11 exact new test names>' -count=1 -v -timeout=90s`
with `CGO_ENABLED=0`, exit 0, package duration 23.724s.

One combined fault run then disabled six protections in the isolated
worktree. It exited 1 (25.004s):

| Deliberate fault | Observed result |
| --- | --- |
| Bypass Unix held-lock refusal | Held-lock test failed: second lock incorrectly accepted. |
| Omit popup opener membership | Popup test failed: foreign tab set adopted the popup. |
| Keep retiring manager marked started | New-call test failed: old tab returned instead of waiting to its deadline. |
| Omit exiting browsers from unmeasurable floor | Floor test failed: another browser started. |
| Omit final activity checks | Call, viewer and tab subcases all failed: busy instance claimed. |
| Bypass pre-probe PATH trust check | Inconclusive: real shell probe timed out before creating its sentinel, so the negative test survived this run. Initial baseline did reproduce execution. |

The five source files touched by those faults were restored byte-for-byte
from their saved originals before the final race batch. The PATH mutation
result is not counted as successful fault detection.

The affected race batch used `CGO_ENABLED=1`, `-race`, `-p 1`,
`-tags goolm,stdjson`, `-count=1`, `-shuffle=on`, and `-timeout=240s`.
It collected 63 top-level groups: 59 passed and four failed (exit 1,
163.942s). It reported no data race or skip. Its scope included all pool
tests, the new regressions, startup cancellation/drain/registration controls,
runtime interval/config readers, package/PATH resolution ordering, and held
lock refusal.

Three new groups failed before their behavioral assertions because their
fake-tab setup still sampled actual host memory. Their local
`memoryPressureFn` seams now supply a healthy reading; the production memory
gate and test assertions were not changed. The fourth failure was the existing
trusted-PATH positive control: its real shell probe timed out after five
seconds and resolution correctly fell back to packaged Chrome. The focused
correction race run covered exactly those four groups. It exited 1 (27.871s):
all three corrected lifecycle groups passed, including call/viewer/tab
subcases, with no race report or skip. The unchanged trusted-PATH control
again timed out in its real five-second probe and fell back to packaged
Chrome. Its expectation remains unchanged; this is an unresolved validation
limit, not a passing control. Across both race runs, 62 of the 63 affected
top-level groups have passing evidence.

The correction command was:

```sh
CGO_ENABLED=1 go test -race -tags goolm,stdjson -p 1 ./pkg/tools/browser -run '^(TestPassivePopupAdoptionRequiresAnOwnedOpener|TestPoolEvictionNewCallCannotUseRetiringBrowser|TestPoolRetirementClaimRechecksNewActivity|TestResolve_Step3_TrustPathChromeTrue_AllowsPATH)$' -count=1 -shuffle=on -v -timeout=90s
```

No full suite, real-browser persistence test, UI acceptance run, or independent
re-review is claimed by this evidence.

## Profile-safety finding carried from the initial batch

At the end of the initial finite batch, `DeleteProfile` coordinated only with
this pool's processes. A second gateway could hold the same workspace's launch
lock while this pool saw no live instance and accepted deletion. That delivery
explicitly left the cross-process protocol unresolved. Putting its coordination
lock inside the directory being removed would permit a new process to lock a
replacement inode.

## Bounded cross-process follow-up design

The accepted follow-up puts each per-profile lock beside the profile, outside
the removed directory, using its immutable configured profile identity.
Coordinator launch, cache trimming, boot reconciliation and deletion must all
use the same helper. Deletion retains the lock through filesystem removal;
launch creates the profile only after taking that lock. A held legacy
in-profile lock also causes refusal, preserving an already-running older
coordinator's profile. Older binaries do not honor the new sibling lock, so
this is not full serialization against a newly starting legacy gateway.

Five focused cases use real temporary profiles and OS locks: another current
coordinator's held lock preserves cookie bytes; a held legacy lock blocks
launch/deletion; a concurrent launch cannot recreate the profile while a
deletion is finishing; deletion preserves the sibling lock inode and releases
it; a failed filesystem removal preserves data and releases its lock for retry.
The removal seam defaults to `os.RemoveAll` and only controls that filesystem
boundary in tests. These expectations precede the lock-protocol changes.

The five-group baseline ran with race detection and exited 1 (11.605s).
Four groups failed for their intended behavior: held current/legacy profiles
were deleted, concurrent launch recreated the directory during deletion, and
the guard inode was removed. Failed-removal retry passed as a positive control.
The shared sibling-lock implementation and its held-legacy check were applied
only after this result. The restored affected race batch passed all 19
top-level groups (exit 0, 32.776s), with no skip or race report. Its scope was
the five new groups, current/legacy lock refusal, per-key lock placement and
reconciliation, Close/drain, and all trim regressions:

```sh
CGO_ENABLED=1 go test -race -tags goolm,stdjson -p 1 ./pkg/tools/browser -run '^(TestProfileDeletion.*|TestLaunchSecurityMarkerlessHeldLockRemainsExclusive|TestCoordinator_LaunchLock_LiveOwnerRejected|TestPool_(PerKeyLockAndMarker|DeleteProfileOnWorkspaceDeletionOnly|ReconcileMarkersAtBoot|ReconcileRefusesWhenLockHeld)|TestPoolCloseDrainsPendingStartupBeforeProfileDeletion|TestPoolCloseJoinsConcurrentTeardown|TestTrim_.*)$' -count=1 -shuffle=on -v -timeout=120s
```

The sibling guard is derived from the fixed profile configuration. Existing
directory/marker filters exclude it from cache sweeps and reconciliation scans.
Unix locking is exercised here; no cross-platform runtime or full mixed-version
serialization claim is made.
