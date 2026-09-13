# Latest Amsterdam picture-confirmation failure

## Timeline

Running candidate: `f65e743ff3afd5f1051773a39ac8910a25a43809`.
All times below are UTC on 2026-09-13.

- 18:36:45: latest user chat session attached.
- 18:36:47–18:37:27: browser attachment, viewport and document transitions;
  capture reported recovery after the earlier transitions.
- 18:37:56–18:37:57: another document transition began.
- 18:37:57: `live view document refresh failed`, error
  `capture session: stale frame claim`.
- 18:38:12: the same error category, now
  `new document did not provide a confirmed picture in time`.

The user-facing text is: “The browser could not confirm the new page picture.
Reload the page or retry the browser connection.”

## What is established

The latest session has 313 recorded input dispatches, all completed, including
42 wheel dispatches. There is no recorded dedicated-input queue failure or
input-dispatch deadline in this window. The failure is in document/picture
confirmation during a page transition.

The code checks that Chrome's current document matches the expected document
before authorizing input against its picture. A stale result is an intentional
refusal to authorize an uncertain picture. The 15-second deadline concerns the
whole confirmation operation; it does not prove a 15-second video decode delay
or a 15-second website load.

## Recovery gap and remaining uncertainty

Source review found that `liveDocumentWatch.settle` marks work as settling,
but on a current-work stale failure does not restart settlement or retire the
transition. Its original watchdog remains armed. This explains how an early
stale failure can be followed by a timeout without a second independent fault.

A possible trigger is disagreement between ordered navigation notifications
and Chrome's current document identity. The handler rejects a commit with a
different loader as an older navigation. Existing logs do not identify which
document comparison failed or prove the exact notification order.

The next focused reproduction should cover overlapping navigation/redirects
and failure while settling. Recovery must revalidate the actual current
document with bounded retries; it must not authorize input against an old
picture, replay gestures, or merely extend the timeout.

## Evidence

Persistent Amsterdam gateway log, downloaded read-only to:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/latest-picture-persistent.log`.
Filtered session evidence:
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/.local/browser-input-final-validation/latest-picture-session.jsonl`.

Runtime sources: `/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/live_document_frame.go` and
`/Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/pkg/tools/browser/live_document_paint.go`. No runtime changes, deployment,
browser navigation or reproduction were performed for the initial investigation.

## Authorized fix

Recovery now retries one complete picture proof when current, previously
committed work becomes stale, including during the later geometry check.
It preserves the original deadline, exact watch/work/capture ownership and
navigation-event sequence. Known newer provisional navigation or queued events
prevent adopting an old page. Reading Chrome's current document is only a
candidate for a fresh paint and geometry proof; the media boundary is still
required before input is authorized. Gestures are never replayed.

Failure reporting is bounded to once per document work item, preventing the
old stale-error-then-watchdog-error duplication. A real failure remains visible.

The deterministic regression failed before the implementation: expected one
recapture after stale current paint, observed zero. Focused document tests
passed after implementation. A real Amsterdam navigation/redirect probe passed
on the old candidate after correcting its initial media-readiness prerequisite;
it is compatibility coverage, not a deterministic reproduction of the race.
Three isolated source mutations were caught by behavioral assertions: disabling
recovery, removing navigation-event fences, and allowing duplicate terminal
errors. The real source was never mutated. The final targeted document/capture tests also passed with race detection.
Amsterdam now runs `88f12e6c37f73ac9bb11308f3f6f90f0b6d034ba`.
Installed and running binary SHA256 both match
`96f7d929f7459f0150652be7995f3923893e4c15b6beadc89b2aa8f636346c6c`.
Existing machine configuration and Amsterdam region were preserved.
The deployed navigation/history/client-redirect test passed in 20.3 seconds
(22.4 seconds including runner), requiring final-document pixels and exact
click, text and key-release state on dedicated input channels.
The live probe did not log a reconciliation attempt; the deterministic
regression and mutation checks establish that specific recovery path.
The user's original page and long-duration sessions still need retesting.
