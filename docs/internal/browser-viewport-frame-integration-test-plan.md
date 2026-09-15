# Measured viewport and panel refresh integration

The browser must stop accepting input against the old picture before the first viewport write. Only actual CSS dimensions and device pixel ratio may authorize a new capture request. Browser admission is released before encoder transport. A repeated request whose measured target and geometry are unchanged does not resize or rebuild. Failed browser work remains unconfirmed. Tab changes use this same measured flow, including panels that have never sent a viewport request. Same-tab commands target the original chat panel. Queued viewport commands and their errors retain the original attachment lifetime.

Tests use the real manager admission, live-view operations, capture generation state, typed recapture adapter, queue dispatch and outbound admission. Only browser protocol actions and encoder transport are replaced at external boundaries. Expected 1000x700 CSS and DPR 1.25 are fixture observations, deliberately different from requested DPR 2. The send callback reacquires both manager and input gates to prove transport is outside admission. Healthy host-memory admission is installed before fake tab creation.

## Observed failures and fixes

- `97026`: six browser leaves ran, five intended behavioral failures and canceled-admission control, package 2.226 s.
- `18240`: original six leaves passed after implementation. Three new routing leaves failed: no-viewport new-target refresh, legacy same-tab route, context-bound same-tab route. Package 7.495 s.
- Same retained `18240`: both gateway scope regressions failed behaviorally, package 20.021 s. A retired queued resize emitted a result for the replacement attachment; a current refusal omitted operation-only semantics.

The original route bodies and gateway handler were unchanged for those reds. New no-viewport delivery fixture signals after recording the command, so its synchronization cannot race its own assertion.

## Impact and ownership

GitNexus upstream impacts were LOW for dispatchViewport, handleViewport, applyColdStartRecapture, reapplyViewportPass, onTabsChanged, recaptureForTabChange and SwitchTab. applyViewportContext/applyViewportAdmitted and the newer SwitchTabContext were absent from the partial index (UNKNOWN); actual call sites were traced manually. Both manager same-tab callers are included. CS Begin/Commit and immutable recapture implementation remain owned by the root integration lane.

Final focused and mutation verification is recorded below. No full CI or actual browser acceptance is claimed here.

## Integrated initial verification

`35148` passed the affected browser selection (20 leaves/24 records, zero skips, 2.613 s) and integrated gateway selection (11 leaves, zero skips, 9.045 s). Gateway tests connect the real manager and live registry to a controlled WebSocket CDP endpoint. The pending-geometry case blocks that external measurement until its independent 641x479/DPR1.25 observation is available. Initial cold correction observes 800x600/DPR1; a requested DPR2 is never treated as measured evidence.

The first two mutations were caught: publishing nonzero dimensions before the bounds write, and routing same-tab recapture through the operator capture. The third mutation initially survived: moving the attachment lookup into the queued job still happened before the test's old in-handler blocker. The driver stopped and restored source. The test was strengthened to block a preceding real queue job, enqueue the viewport, replace the attachment, and only then release execution. The raw failed mutation audit is retained; its corrected mutation and restored race results follow below.

Existing fake viewport-context tests now seed healthy host memory before fake Session creation, matching the new fixture. This isolates browser-control assertions from unrelated host pressure without changing runtime admission.


## Final closeout

`51834` finished with exit 0. The corrected queued-lookup mutation failed the strengthened real-queue test. All three planned faults were caught. Restored source then passed shuffled race selections:

- Browser: 20 leaves/24 test records, zero failures/skips/race warnings, 4.189 s.
- Gateway: 11 leaves/11 records, zero failures/skips/race warnings, 10.738 s.

Commands use `CGO_ENABLED=1 GOMAXPROCS=2 go test -tags goolm,stdjson -p 1`, with `-count=1 -json -race -shuffle=on`. Browser selection: `^TestViewport(Frame|TabRefresh|SameTabCommand|Context)`. Gateway selection: `^TestViewport(QueuedCommand|CurrentRefusal|ColdRefresh)|^TestWebRTCHandler`. The first two mutation runs and initial greens were reused, not repeated after strengthening the third test.

The cold helper consumes the original resolved attachment and temporary negotiation context. It performs real CDP measurement, skips unchanged source geometry, and never applies remembered requested scale as an observation. The previous two remembered-scale assertions were replaced with measured-DPR and unchanged-source tests. Frontend viewport scheduling already depends on `attached`, so premature attachment-less resize work can be discarded without late route substitution.

Additional impacts: removed dead reapply watchdog constant and obsolete cold-scale tests were LOW; newer fixture/test symbols were UNKNOWN in the partial index and manually scoped to their callers. Local GitNexus detection is attempted before commit; this isolated worktree is not registered, so the root integrator must map the exact commit against its registered index.

Precommit `detect-changes --scope staged --repo /Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/ui` ran as `3715`, exit 1: repository not registered. Manual staged review found exactly the 14 assigned viewport/cold-helper/test/evidence files, no unstaged source, and clean whitespace. The parent integrator approved this explicit fallback and will run detection on its registered root index.
