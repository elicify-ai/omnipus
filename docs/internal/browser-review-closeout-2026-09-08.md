# Browser review closeout

Branch: `browser-improvements`; release base: `fbcbc5edc9845f1fbecb01b423f15d09fe405f5c`.
This is an open findings ledger, not release acceptance.

## Input correction test plan

The frame protocol and navigation recovery requirements supply the oracle:
ordinary input belongs to its original presented picture; navigation controls
remain available when that picture is unavailable. Wheel increments may be
combined only when their original picture, coordinates and modifiers agree.

| Boundary | Cases and exact expected result | Real unit / external boundary |
|---|---|---|
| Server navigation | Missing, stale and pending picture: actual `navigate_back` dispatches history lookup and entry 17; text dispatches nothing | Registry, input admission and actions real; Chrome executor scripted |
| Queued scrolling | Capture ID, generation, modifiers, x/y, dimensions or future basis changes: retain both original events and their 100/1 deltas | Real queue and contextual drain; outgoing sink observes complete frames |
| UI scrolling | Position or Control modifier changes: send original 100 delta before new 1 delta; unchanged burst still sums | Real component handlers/pacer; transport boundary observed |

The planned fault checks removed picture/gesture boundaries and the navigation
exemption, then restored code and ran the affected selections (results below). Existing malformed-delta,
discrete-order and identical-gesture cases remain controls. This does not prove
sustained real-user timing or every browser platform.

## Fresh independent findings

The relay/capture review read 78 complete changed files; manager review read 52
(22 production and 30 tests). The table records correction status, not end-to-end acceptance. Severity alone
does not imply runtime reproduction.

| Finding | Owner / state |
|---|---|
| Old wheel increments relabeled as current picture | Root; corrected, affected race checks passed |
| Wrong Back wire classifier | Root; corrected, affected race checks passed |
| Unbounded native ingest preparation after cancellation | Integrated `cc027da56`; affected race passed; independent capture lifecycle recheck completed |
| Startup waiter ignores its own cancellation | Integrated `cc027da56`; affected race passed; independent capture lifecycle recheck completed |
| Stop does not cancel complete encoder startup | Integrated `cc027da56`; affected race passed; independent capture lifecycle recheck completed |
| Fired grace timer stops replacement viewer | Integrated `cc027da56`; affected race passed; independent capture lifecycle recheck completed |
| Recovery fixtures lack qualified sender; health oracle/wiring tests obsolete | Integrated `cc027da56`; affected race passed; independent capture lifecycle recheck completed |
| Dead capture foreground retry and lifecycle comments | Integrated `cc027da56`; affected race passed; independent capture lifecycle recheck completed |
| Untrusted PATH candidate executes before trust gate | Integrated `257d2b9b6` / `eed3f05a6` / `22116776e`; see validation limit below |
| Markerless held Unix launch lock can be bypassed | Integrated `257d2b9b6` / `eed3f05a6` / `22116776e`; see validation limit below |
| Configuration reload overwrites per-workspace profile identity | Integrated `257d2b9b6` / `eed3f05a6` / `22116776e`; see validation limit below |
| Deletion does not retire pending startup / drain teardown | Integrated `257d2b9b6` / `eed3f05a6` / `22116776e`; see validation limit below |
| Idle and pressure eviction race with activity admission | Integrated `257d2b9b6` / `eed3f05a6` / `22116776e`; see validation limit below |
| Legacy OpenTab startup survives Shutdown | Integrated `257d2b9b6` / `eed3f05a6` / `22116776e`; see validation limit below |
| Popup adoption lacks proven opener ownership | Integrated `257d2b9b6` / `eed3f05a6` / `22116776e`; see validation limit below |
| Same-target document navigation lacks fresh frame fence | Capture API `9dcb178fb`, live integration `4d035c62c`, reverse-initialization fix `2259cd81f`; final focused race selection passed 123 entries, zero skips/races, in 5.422s; independent correction recheck clear; runtime acceptance pending |
| First switch after attachment lacks original-tab baseline | Integrated `3b31c61e2`; affected race passed, independent source review complete |
| Retired death watcher can stop replacement capture | Integrated `3b31c61e2`; affected race passed, independent source review complete |
| Four live/tab/viewport fixture migration groups | Integrated `3b31c61e2`; affected race passed |
| Manager locking/startup comments inaccurate | Integrated `257d2b9b6` / `eed3f05a6` / `22116776e`; see validation limit below |
| Retired gateway helpers, measured fixtures and bounded combined notice | Integrated `e2a3223c3` / `7bfcb05e4`; independent cleanup/notice recheck clear; Linux-specific execution remains pending |
| Profile deletion can remove another process's active profile | Integrated `766f940d8`; 31 affected race entries passed; mixed-version/Unix-only limits recorded |
| Previously installed ingest cleanup is unbounded | Integrated `8aece98b4`; seven focused race entries passed; authenticated capacity bound only |
| Late registration republishes a retired pool instance | Integrated `57c25fa00`; 39 affected race records passed |
| Popup retry/attachment changes original session ownership | Integrated `46ef9b117`; 39 affected race entries passed; independent correction review completed |
| Reconciliation snapshot adopts into another session lifetime | Integrated `301b860ef`; 47 affected race entries passed; independent correction review completed |

The independent recheck of root wheel/Back and annotation corrections found no new high-confidence defect. Same-generation DOM movement remains best-effort for annotation enrichment.

Manager validation: 11 new groups passed; five fault families were detected. Across the original and corrected-fixture race runs, 62/63 affected groups passed without race reports or skips. The unchanged trusted-PATH positive control repeatedly timed out in its real five-second shell probe in the earlier runs; their cause remains unproven. The initial PATH trust defect was reproduced, but its later mutation run was inconclusive.

The unchanged focused trusted-PATH race control subsequently passed in run
`58637` (exit 0; test 1.05s, package 6.140s). This closes the current focused
control gap. It neither retroactively passes the earlier failed batches nor
establishes contention as their cause. Retained log:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/trusted-path-final-race.log`.

Independent capture review found no additional document API or lifecycle defect
in its assigned scope. Its installed-ingest cleanup finding is now corrected by
`8aece98b4`. The separate manager findings have finite corrections above;
independent popup/reconciliation recheck found no high-confidence defect.
Historical opener ownership is sufficient while the original session survives:
a legitimate popup may outlive its opener. Same-ID replacement interleaving in
the reconciliation followup is source-proven, not directly scheduler-forced;
tests force original-owner retirement while its old browser cleanup remains held.

Profile deletion retains an external sibling lock through removal and refuses
already-held legacy in-profile locks. An older binary starting without honoring
the sibling lock can still race; full mixed-version serialization is not claimed.
The executed lock tests cover Unix, not every supported platform. Native ingest
cleanup is not interruptible: authenticated unfinished preparation/retirement
work is capped at four, and a healthy installed connection consumes no slot once
preparation finishes. Legacy unbound ingest cleanup is outside this bound.

The initialization correction is integrated as `2259cd81f`; retained run `71487`
passed 123 focused race entries with zero skips/races in 5.422s. Independent
manager recheck found no high-confidence residual defect. The separate gateway
cleanup/notice and pending-viewport (`5db25b399`) recheck also found no new
high-confidence behavioral defect. Original attachment admission, actual combined
notice/schema assertions and current contextual input callers were inspected;
this read-only review did not execute Linux-only fixtures or prove visible input.

Build `39294` passed, including 669 embedded SPA files; contract generation
produced no changes. Binary source includes `2259cd81f`; later commits at this
checkpoint changed only documentation/tests. Combined race run `84333` exited 1:
the browser package reached its five-minute timeout after an actual-Chrome test
spent about 280 seconds downloading Chrome and another download began. The
complete relay package passed in 83.753s. Neither a passing whole-browser batch
nor full CI is claimed.

The rebuilt isolated instance is process 83139 on port 11094 (tool session
38626). Installed process 64851 on port 10994 remains preserved. The completed
soak result follows; the tested binary source remains `2259cd81f` with 669
embedded SPA files.

## Completed soak: latency acceptance failed

Run `99078` exited 1 after 20.7 minutes. The sole failure was the final 100-click
decoded-video latency assertion: p95 was **247.19999992847443ms**, above the
unchanged **200ms** target. Subsequent normal diagnostics also failed at p95
267ms (`69699`) and 306.1ms (`71735`) on unchanged source `2259cd81f`.

Before that assertion, both idle and mixed-input phases met their independent
600,000ms minimums. Assertions passed for 550 exact ordered trusted native
events, visible held inputs and exactly-once releases, no held state at the end,
continuous original capture/connection identity, no renegotiation or lifecycle
failure, and no uncaught viewer errors. The final screenshot shows count 550
and first mismatch 0; the integration owner visually checked it. These preceding
passes provide duration and correctness evidence, but the overall soak failed.

Evidence retention is incomplete: the line reporter did not persist body
attachments, including all 100 latency samples, detailed JSON and phase images.
Their absence prevents independent reconstruction of the latency distribution
from retained artifacts. The recorded p95 failure and final screenshot survive;
artifact persistence and the short diagnostic were subsequently integrated in
`b293c9808`. This does not recover the original missing artifacts. Retained files:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/final-soak.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/final-soak/browser-improvements-soak--73306-exact-mixed-input-for-10min/test-failed-1.png`

This result does not prove audio, remote network/TURN behavior, the native macOS
app, startup/resize targets or final CI. Those acceptance requirements remain open.

## Subsequent latency diagnostics

Both normal 100-click diagnostics failed the 200ms target, even after a test-only
pixel-decoder optimization reduced sampling overhead. Explicit video-only run
`67712` passed at p95 156.2ms with inactive audio negotiation and 300 exact ordered
events. This is a hypothesis experiment, not a production/audio/soak acceptance
pass. All three runs used production source `2259cd81f`; detailed JSON was retained.
Both actual sender-clock baselines were reproduced and corrected by `d9352ceea`
(worker `5ff70a549`). Final focused race verification passed seven groups/nine
records in 15.118s, zero skips/races, with actual packet replay, receiver-report
and picture-loss feedback controls. Independent review and isolated build
`42762` are running; no post-fix runtime result is claimed. Latency causality
is not established by one fresh-session audio comparison. Metrics,
interpretation limits and exact artifact paths are recorded in
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/internal/runtime-latency-validation.md`.

## Gateway viewport fixture correction

Focused gateway race run `99178` exited 1 after 34.114s: 33 groups passed, one
failed and 11 Linux-only groups skipped, with no race warnings. The sole failure
was `TestBrowserWS_ReadLoop_StaysResponsiveWhileViewportHandlerRuns`: its socket
was authenticated but never attached, so the current viewport admission guard
returned before the blocking handler seam. This was a stale fixture prerequisite,
not evidence that the production reader blocked.

Integrated correction `7a404a82e` uses the existing external browser-protocol
fixture and a real socket attachment, then installs the original handler blocker.
The handler-entered assertion, next-frame response assertion and original
timeouts remain unchanged. Driver `43028` completed with exit 0:

- Both viewport/attach reader checks passed initially in 18.420s.
- Temporary inline viewport dispatch failed the socket-response assertion
  (fault run exit 1), after successfully reaching the handler-entry assertion.
- Production bytes were restored exactly; both checks passed with race detection
  in 17.538s, with zero skips or race reports.

The specific fixture failure is closed without a production change. This does
not relabel the original batch as green or establish Linux execution. The binary
still contains source `2259cd81f`. Retained logs:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/gateway-final-focused-race.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/viewport-reader-final-green.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/viewport-reader-final-fault.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/viewport-reader-final-restored-race.log`

## Release-base inventory and review limits

Inventory at `57c25fa00`, compared with
`fbcbc5edc9845f1fbecb01b423f15d09fe405f5c`: 335 changed files, comprising nine
canonical contract files, one generated Go API file, 76 gateway files, 171
browser files (including the pipe allocator), 28 frontend files and 50 docs.
No changed file falls outside those known source/contract/document areas. This
is an exact snapshot inventory, not a claim that every later correction has
independent review or that all release requirements are verified. Reconciliation
was subsequently integrated as `301b860ef`.

Earlier validation plans retain historical red/green checkpoints. The current
status here supersedes historical statements that implementation is pending.
Canonical protocol pending-implementation statements and the spec's obsolete
graph-tool requirement have been corrected. The document-transition plan now
labels the original old-picture defect historically, alongside the initialization
followup. These are documentation status corrections, not new source findings. Current checks use direct source/caller
inspection and git diffs following the user's instruction to stop GitNexus.

## Intermediate runtime evidence

The earlier isolated instance on port 11094 ran build `b80287c49`, while
installed port 10994 was preserved. These historical smoke results exclude
subsequent annotation/encoder and review corrections. They do not establish
acceptance for the newly rebuilt instance described above.

- UAT-13: exit 0, one passed in 49.5s, no skips. Video appeared after 18.004s:
  **opening exceeds even the 10s cold target**. Cold versus warm
  startup was not controlled in this smoke test. Scrolling measured 19.37 frames/s over
  13.2s with 39/64 image regions changing. Concurrent available-memory samples
  stayed above 23%, exceeding the application's 15% admission threshold.
- UAT-14: exit 0, one passed in 46.5s, no skips. Actual click at (52,411) in a
  552×624 capture reached the destination address in 904ms, including external
  page loading. Typed characters appeared in 380ms. This single measurement
  **does not establish the 200ms p95 input target**.
- Actual full-application failure screenshot shows the long error fitting the
  browser panel with Retry visible. Previous reconstructed CSS checks alone
  did not establish this.

Logs and images are under
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-runtime/evidence/`:
`review-smoke-memory-uat.log`, `review-smoke-memory.jsonl`,
`review-smoke-click.log`, `review-smoke-click-frames/`, and `review-smoke-uat/`.
These intermediate smoke runs alone do not establish long-duration or
100-action acceptance. The later completed soak above supplies duration and
ordered-input evidence but fails latency acceptance; audio, remote network,
native Mac app and final CI remain pending.

## Input correction evidence

- Initial Go reproduction exited 1: eight queued-wheel semantic boundaries
  and all three Back readiness cases failed for the intended behavior.
- Restored focused Go selection exited 0 with race detection and shuffled
  order after combined removal of wheel separation, Back exemption and delta
  accumulation was caught. Identity, arithmetic and navigation failures were
  individually visible; this was a combined fault run, not isolated mutants.
- Initial UI reproduction failed both gesture boundaries. Splitting position
  into independent x/y cases and removing the gesture guard failed all three
  selected cases. Restored affected input and annotation files: 80 passed,
  no skips. Integrated annotation typecheck passed before the wheel change;
  updated typecheck and focused lint also passed.

Raw logs are under
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/`:
`wheel-back-{red,green,fault,restored}.log` and
`wheel-ui-{red,fault,restored}.log`. These component checks do not close the
100-action latency or sustained interaction requirements.
