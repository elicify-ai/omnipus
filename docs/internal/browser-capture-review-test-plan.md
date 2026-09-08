# Capture lifecycle review corrections

Scope: release-base review findings R2–R9, based on `b9820ca977`.
Requirements: browser-improvements spec FR-009 (original identity), FR-011
(cancellation responsiveness), FR-013 (qualified recovery), FR-016 (bounded
lifecycle cleanup), and the independent review's concrete failing timelines.

The real capture/session and relay implementations remain under test. Fake
functions represent only native interface enumeration, target creation/navigation,
and the encoder control transport. No production timeout is shortened for the new
regressions. Waiting deadlines detect a stuck caller; counters and identities prove
the effects. Existing legacy adapter cases remain explicitly separate.

| Case | Required result | Deliberate fault |
|---|---|---|
| Four canceled ingest preparations remain blocked | Fifth request rejected; installed media unchanged | Release admission when caller cancels |
| Native preparation returns but candidate cleanup remains blocked | Capacity remains occupied until cleanup completes | Release before cleanup |
| Retired candidate finishes; next request arrives | Capacity reusable; only current candidate installs | Never release reservation |
| Second Start caller cancels while original starter is blocked | Second returns cancellation; original remains live; one startup | Wait inside sync.Once |
| Already-canceled Start after successful startup | Cancellation, no new startup or target close | Return cached success without context check |
| Stop during target creation/navigation | Browser operation cancels, no surviving target or successful startup | Omit capture-stop lifetime link |
| Successful startup followed by original caller completion | Target remains alive until Stop | Keep temporary cancellation linked |
| Fired grace callback resumes after new active/pending viewer admission | No shutdown or relay close | Unconditional Stop callback |
| Current grace callback with no viewers | Exactly one shutdown | Always ignore grace callbacks |
| Current qualified recovery, with a frame change or binding retirement | Only original valid transport claim can send | Legacy fixture or ignore claim |
| New pending frame followed by old health sample | Empty new-frame evidence; socket heartbeat advances | Retain old health on frame change |
| Real frame transition while recovery callback is paused | Old continuation cannot send or publish current health | Delete transition-to-reset wiring |

Shared-start ownership remains unchanged: the first caller owns the one startup
attempt and its sticky outcome; other callers cancel only their own waiting.
The native preparation limit follows the existing viewer-leg four-candidate
capacity. No wire contract changes are needed.

Execution is serialized with the parent. Record exact focused red, green, and
mutation results in the delivery report. Full CI and real decoded video/audio,
browser interaction, network adaptation, and application layout are parent-owned
verification and are not established by these tests.

## Recorded checks

Worktree: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/capture-review-fixes`.
Gateway dependency `b2f52e361` was integrated as `a6eca3392`; its removed scale
fixture callers allowed deletion of the unused capture scale cache. The wire
injection still carries the immutable measured frame scale.

- Baseline browser run (session 14271, exit 1): canceled waiter and cached canceled
  Start returned success; Stop failed to cancel both native creation and navigation;
  retained grace callbacks stopped legacy viewers, current requests, pending requests,
  and a later grace period. The idle grace control passed.
- Baseline native ingest run (55908, exit 1): the fifth blocked preparation was
  accepted, displaced the installed connected ingest, and capacity was still
  available while actual Pion interceptor cleanup was blocked.
- First corrected browser run (82690, exit 0) included the existing concurrent-start,
  sticky-error and stopped-while-starting regressions. Corrected native ingest run
  (75308, exit 0) retained the original connected ingest at saturation and reused
  capacity after cleanup.
- Deliberate production faults (46746): omitting the actual frame transition reset
  failed five health cases; moving private candidate Close into a goroutine failed
  the cleanup-held capacity assertion. Both packages exited 1. A `finally` block
  restored the exact original production text. A preceding wrapper syntax error
  occurred before any mutation or Go execution and is not counted as a red test.

The native regression establishes real Pion connection retention and admission
limits; it does not claim decoded browser video or audio continuity. The clock
fixture retains the callback actually scheduled by production and advances it
manually after active/pending viewer admission, so its race scenario does not
rely on scheduler luck. Health fixtures use the qualified recapture transport,
commit an initial matching packet, and compare the original frame captured before
loss. Frame transition tests never invoke the reset helper themselves.

GitNexus reported HIGH impact for Session (NewSession → NewCaptureSession →
ensureCaptureSession); the parent approved the single ingest-pool field before edit.
Start and grace scheduling were LOW. Current ingest internals and frame-transition
symbols were absent from the stale index and were manually traced through actual
admission/installation, measured frame preparation, and current test callsites.
Scale setter/getter were LOW; the setter's indexed gateway callers were obsolete,
and the current source had no executable callers after dependency integration.

Final affected race check (23213, exit 0):

```sh
CGO_ENABLED=1 GOMAXPROCS=2 go test -race -tags 'goolm,stdjson' -p 1 -count=1 -v github.com/elicify-ai/omnipus/pkg/tools/browser github.com/elicify-ai/omnipus/pkg/tools/browser/webrtc -run '^(TestCapture(Start|Stop|Grace|Health|Ingest|Session_)|TestIngest|TestHandleIngestOffer_)' -timeout 120s
```

All 207 test/subtest entries passed, with no skips or reported races. The raw log
is `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/capture-review-fixes/affected-race.log`.
The final waiter fixture also invokes real `runEncoderStartup` through the native
create/navigation boundary. Follow-up race check 53755 passed its five top-level
tests and four subtests, including successful persistent target lifetime,
cancellation at both startup stages, and retained navigation failure:

```sh
CGO_ENABLED=1 GOMAXPROCS=2 go test -race -tags 'goolm,stdjson' -p 1 -count=1 -v github.com/elicify-ai/omnipus/pkg/tools/browser -run '^(TestCaptureStartWaiterCancellationPreservesOwner|TestCaptureStopCancelsWholeEncoderStartup|TestEncoderStartup)' -timeout 60s
```

No full CI, browser end-to-end suite, decoded audio/video check, or manager/live/CDP
regression suite was run in this implementation slice. Root owns the wheel fix,
remaining independent reviews, application browser testing, and integrated CI.

Final staged GitNexus check reported 14 files, 25 symbols, zero affected indexed
processes and LOW risk. Shifted stale-index symbols were reconciled against the
manual staged diff; only the approved single pool field changes Session. The
staged diff contains no gateway, inputdc, generated contract, or manager edits,
and whitespace validation passed.
