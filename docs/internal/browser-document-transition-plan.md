# Document transition correction

Status: old-picture acceptance reproduced and corrected; integrated focused
race verification passed. Not yet verified in the running browser.

Before this correction, same-tab navigation left the old capture generation ready. A
successful navigation command only means Chrome accepted the request. It does
not prove the replacement document has painted or reached the viewer.

## Required behavior

1. Validate a requested destination before changing capture state. A refused
   destination leaves the healthy picture intact.
2. Before accepted navigation, history or reload reaches Chrome, retire the
   old displayed generation. Keep navigation and owned key/button releases
   available while ordinary picture-dependent input is blocked.
3. Observe main-document navigation independently of UI commands, including
   page-initiated navigation, reload, redirects and same-document history.
   Retain exact target/watch and loader/document ownership across waits.
4. Only complete the current transition after the replacement document has
   committed and crossed a browser paint opportunity. Then measure its actual
   geometry, request qualified capture, and await the existing video timestamp
   boundary at the viewer. Document paint is not itself viewer readiness.
5. A newer document, target, capture, detached watch or shutdown retires older
   completion work. Canceled/failed navigation must settle safely on the actual
   document; it must not permanently lock an unchanged page or claim an old
   picture represents a replacement.

## Capture contract

Use an internal opaque pending-document token owned under the capture lock.
Beginning a document transition must force a fresh generation even when target
and geometry are unchanged. Retire the previous frame lifetime and its health
evidence, then publish the existing transitioning state.

Ordinary geometry measurement, health recovery, ingest boundaries and viewer
offers must not bypass the pending-document fence. Completing it requires the
exact current document token and target, with measured geometry. Layout changes
during navigation must remain usable: preserve the requested viewport and apply
or remeasure it at current-document completion; do not silently lose a resize.
Use the existing wire generation and presentation gate; no new wire fields are
proposed. Keep slow document waits outside the ordered input/tab command gate.

## Verification

| Scenario | Independent expected result |
|---|---|
| Navigate / Back / reload | Old picture rejected before browser command, still rejected after command acknowledgement |
| Refused URL | Zero navigation commands; original healthy frame unchanged |
| Same-target new document | New generation; old input rejected until actual replacement boundary |
| Navigation B supersedes A | A's late commit/paint/measurement cannot publish or send capture for B |
| Resize during pending navigation | No early authorization; final measured dimensions represent the requested viewport |
| Page-initiated navigation / history | Same fence without a UI navigation command |
| Slow destination | Subsequent navigation, cancellation, health and held-state releases remain responsive |
| Abort / no history / protocol failure | Actual unchanged page can recover without replaying uncertain input |
| Subframe navigation | Does not falsely treat a subframe as the main document |

Keep registry, input, capture state and event dispatch real. Script only the
Chrome protocol, paint and transport boundaries for deterministic ordering.
Real browser tests must additionally prove page content and input follow the
presented document. Deliberate faults will remove pre-command invalidation,
pending-capture admission, and obsolete-document completion checks.

Manual callsite inspection covers preparation, viewport, ingest boundary, input
and recapture paths. This is a high-risk change because it changes shared
capture authorization; focused protocol tests and actual browser verification
are both required.

The live integration identifies canceled provisional documents through their
exact network request and loader. A generic frame-stopped event has no loader
identity and is insufficient to reopen an unchanged picture. Back with no
history and explicit protocol refusal can also recover the unchanged document,
after the same paint and measurement checks. Uncertain transport failure is
never treated as proof that navigation did not happen. A bounded failure is
reported with reload/reconnect recovery if no current document can be proven.

## Integrated evidence

- Original command-boundary reproduction failed all three navigation kinds
  because the previous picture remained accepted. The refused-URL control's
  initial fixture compared a pre-commit snapshot; that fixture was corrected
  to retain the actual ready picture, without changing its refusal oracle.
- First integrated selection exited 1 with exactly one new behavioral failure:
  an admitted input remained alive after its frame retired. All other selected
  navigation, paint, initialization, event, input and viewport cases passed.
- The correction binds ordinary admitted commands to their exact frame lifetime.
  A frame-only cancellation is a benign retirement, not a connection failure;
  uncertain press delivery still retains the existing held-input cleanup.
- Four combined production faults removed pre-command retirement, frame-bound
  cancellation, final watch qualification and the post-paint document check.
  Each corresponding test failed. A separate fault made initialization use
  newer work instead of its retained token; the delayed-snapshot case failed.
- Exact source restoration followed by the affected race/shuffle selection:
  **122 passing test/subtest entries, zero skips, zero race warnings**, 8.393s.

Evidence and reproducible driver:

- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/live-document-first.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/live-document-faults.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/live-document-initial-fault.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/live-document-restored-race.log`
- `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/live-document-milestone.py`

Independent review identified and drove corrections for retained initialization
errors/snapshots, subframe discovery, overflow recovery, producer ordering, and
atomic original-watch qualification at capture publication. Actual Chrome paint,
viewer decoding, sustained interaction and final-commit CI remain required.

## Reverse initialization verification

A final independent review found the reverse ordering: explicit navigation can
start before initial document discovery. The regression failed on the old code
(session 98915), which replaced the pending generation and measured the old page.
Initialization now retains existing navigation work while discovering metadata.
An independent read-only recheck found no remaining behavior issue in this fix.

The restored focused race run (session 71487) passed 123 test entries with no
skips or race reports in 5.422 seconds. Its log is
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/live-document-final-race.log`.

The attempted whole browser/relay race run (84333) was not green: the browser
package exhausted its five-minute limit after an older real-browser test spent
280 seconds downloading and starting Chrome, followed by another download.
The relay package passed in 83.753 seconds. This does not close whole-package
or runtime acceptance. The complete log is
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-improvements-integrated-race.log`.
