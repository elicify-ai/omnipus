# Capture ownership per resolved panel

Status: storage, lifecycle, and overlapping-teardown admission passed focused
verification and six fault injections. Production routing migration remains
pending.

Each resolved panel tab set owns its capture. Two viewers of the same tab set
reuse that capture; different tab sets must never reuse it. A missing panel
capture must return no capture, even when another panel has one. This preserves
the connection's existing page selection and keeps displayed video aligned with
input routing.

The manager will store captures by resolved tab-set ID. Existing no-panel APIs
will address the operator tab set explicitly; they must not return an arbitrary
capture. Cleanup must compare the exact capture identity, so an old stop cannot
remove a replacement or another panel. Manager shutdown must stop every capture
outside the manager locks. Health observers must be installed on every existing
and newly created capture, with immutable capture/frame identity on events.

Initial tests use real manager storage with inert capture objects as independent
identities. They require distinct A/B captures, same-panel reuse, no fallback for
an unknown panel, isolated factory failure/retry, and identity-safe cleanup.
Lifecycle tests will use real capture sessions for shutdown and observer fan-out.
Production integration must update capture construction, registry token lookup,
input admission, tab changes, viewport updates, and browser-death cleanup. Passing
the storage tests alone will not prove independent video playback.

Impact analysis reports LOW for EnsureCaptureSession (one direct caller, five
affected symbols, one gateway execution flow) and BrowserManager (one indexed
direct dependency). The type's graph coverage is incomplete; all capture callers
must also be checked explicitly during migration.

The initial storage run 93015 failed all three intended cases (terminal exit 1,
2.817 seconds). With storage keyed by panel, run 74111 passed those cases
(terminal exit 0, 2.387 seconds). An earlier attempt 77091 was a test-fixture
callback-signature compilation error, corrected before 74111; it is not
behavioral failure evidence. Existing fixtures that directly assigned the old
single capture now address the operator map entry, preserving their existing
default-API behavior and assertions until explicit caller migration.

Lifecycle run 74382 failed all three intended leaves (terminal exit 1,
2.041 seconds): shutdown and connection invalidation left both panel captures
alive, and observer registration/unregistration missed existing panel entries.
Teardown tests use real CaptureSession instances with inert relays and a real
on-stopped callback that clears the manager entry. Observer tests exercise
manager registration directly; they do not claim video-health delivery through
the gateway. Mutation checks and restored race verification remain pending.

Independent review found an admission race during the snapshot-to-Stop interval:
a new capture can be installed while teardown drains, then survive that teardown.
The regression will hold an actual capture's relay Close at an explicit barrier,
require a new panel factory to be rejected without invocation, and repeat after
an overlapping teardown completes. Once all teardown work finishes, manager
reuse may create a fresh capture. This requires an active-teardown count rather
than a single boolean that one overlapping completion could clear too early.

Lifecycle run 5287 passed the six storage/lifecycle leaves (terminal exit 0,
1.866 seconds). The overlapping-teardown run 35452 then failed both operation
leaves with four explicit admission violations (terminal exit 1, 1.650 seconds).
The manager now counts active teardowns before snapshotting captures and rejects
new capture factories until every teardown finishes. Green verification of this
last change remains pending; no browser playback claim follows from these tests.

Driver 80704 completed with terminal exit 0. The initial complete panel selection
passed in 1.051 seconds. All six deliberate faults failed at the intended
behavioral assertions: unknown-panel fallback, stale cleanup clearing other
entries, ignoring teardown admission, treating overlapping completion as a
boolean reset, omitting lifecycle captures, and omitting existing observer
registration. Source was restored after each fault. The restored selection
passed with race detection and shuffled ordering in 10.794 seconds, with no race
warnings. These are test durations, not browser latency measurements.

A second bounded independent review confirmed the admission counter covers a
factory already holding the lock, the snapshot interval, and overlapping
teardown completion. No additional defect was found in that bounded review.
The required full release-base review remains separate and incomplete.
