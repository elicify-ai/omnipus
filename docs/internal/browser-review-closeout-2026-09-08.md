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

Fault checks will remove picture/gesture boundaries and navigation exemption,
then restore code and run the affected selections. Existing malformed-delta,
discrete-order and identical-gesture cases remain controls. This does not prove
sustained real-user timing or every browser platform.

## Fresh independent findings

The relay/capture review read 78 complete changed files; manager review read 52
(22 production and 30 tests). Source findings still require reproductions and
corrections; severity does not imply runtime reproduction.

| Finding | Owner / state |
|---|---|
| Old wheel increments relabeled as current picture | Root; corrected, affected race checks passed |
| Wrong Back wire classifier | Root; corrected, affected race checks passed |
| Unbounded native ingest preparation after cancellation | Capture worker |
| Startup waiter ignores its own cancellation | Capture worker |
| Stop does not cancel complete encoder startup | Capture worker |
| Fired grace timer stops replacement viewer | Capture worker |
| Recovery fixtures lack qualified sender; health oracle/wiring tests obsolete | Capture worker |
| Dead capture foreground retry and lifecycle comments | Capture worker |
| Untrusted PATH candidate executes before trust gate | Manager worker |
| Markerless held Unix launch lock can be bypassed | Manager worker |
| Configuration reload overwrites per-workspace profile identity | Manager worker |
| Deletion does not retire pending startup / drain teardown | Manager worker |
| Idle and pressure eviction race with activity admission | Manager worker |
| Legacy OpenTab startup survives Shutdown | Manager worker |
| Popup adoption lacks opener membership check | Manager worker |
| Same-target document navigation lacks fresh frame fence | Pending implementation assignment |
| First switch after attachment lacks original-tab baseline | Pending implementation assignment |
| Retired death watcher can stop replacement capture | Pending implementation assignment |
| Four live/tab/viewport fixture migration groups | Pending implementation assignment |
| Manager locking/startup comments inaccurate | Manager worker |
| Retired gateway helpers, measured fixtures and bounded combined notice | Startup worker closeout |

Each implementation requires a separate independent recheck. Source review of
these slices does not cover every remaining file in the release-base diff.

## Intermediate runtime evidence

Isolated port 11094 runs binary built from `b80287c49`; installed port 10994 is
preserved. This intermediate build excludes subsequent annotation/encoder and
review corrections, so final acceptance needs the final build.

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
Long duration, 100-action, audio, remote network, native Mac app and final CI
acceptance remain pending.

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
  updated typecheck remains part of integration validation.

Raw logs are under
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/`:
`wheel-back-{red,green,fault,restored}.log` and
`wheel-ui-{red,fault,restored}.log`. These component checks do not close the
100-action latency or sustained interaction requirements.
