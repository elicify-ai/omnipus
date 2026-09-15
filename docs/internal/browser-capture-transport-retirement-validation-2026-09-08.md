# Capture socket transport retirement — 2026-09-08

## Failure and boundary

A canceled recapture request could hide a fatal socket write error: the callback checked the request context before retiring its connection. The ingest answer helper had the same problem when it converted any error for a superseded frame into a harmless stale-offer result. Gorilla retains write failures permanently, so leaving that socket bound prevents further control writes even if the read side remains alive.

The correction distinguishes errors from attempted socket operations from canceled admission. Only socket deadline-setting/write failures carry `captureIngestTransportError`. The original recapture socket is retired on that type regardless of the originating request's cancellation. The answer helper preserves this error before its stale-offer conversion, allowing the existing offer loop to close the failed socket. No offer-loop or relay implementation changes are included.

## Targeted evidence

The fixture uses an actual HTTP upgrade, Gorilla WebSocket writer, `serveBoundIngest`, and CaptureSession binding/recapture. Only the underlying `net.Conn.Write` is controlled: after a request reaches the real writer, it waits until caller cancellation/deadline or frame replacement, then returns a timeout error. Thus Gorilla's fatal write state and application cleanup remain real. All fixture handlers and sockets drain during cleanup.

- Red `29227`: terminal exit 1, 10.255s. Three failures reproduced: canceled recapture, expired recapture, and frame-superseded answer each retained socket binding 1 after fatal write failure. The canceled-before-dispatch control preserved its socket and successfully sent the next valid recapture.
- Closeout `50303`: authoritative terminal exit 0. Initial affected green passed 18 leaves in 7.043s. Two approved fault classes (removed transport marking; restored cancellation/stale suppression at the two consumers) each reproduced all three negative failures. Restored focused race/shuffle passed 18 leaves in 20.975s, zero skips or race warnings.
- Followup validation is restricted to these new cases and existing writer admission/current-offer controls. No browser launch, installed app restart, or broad CI run belongs to this fix.

## Scope review

GitNexus impact calls `21902` (`sendJSONContext`), `18229` (`serveBoundIngest`), and `8227` (`answerCaptureOffer`) completed with UNKNOWN because these new symbols were absent from the index. Manual tracing found the shared writer serves ingest control, recapture, answer, and error frames; the two changed consumers are recapture transport retirement and the answer helper's stale conversion. Media's signaling commit `fb14c4ca2` is the dependency. All edits were made in the isolated startup worktree.

Precommit `detect_changes --scope staged` was attempted in the isolated worktree (`2073`, exit 1); the tool could not select this unregistered worktree among multiple indexes. Manual staged review confirmed only the three assigned production regions, focused tests, and this evidence document. Whitespace checks passed; parent performs integrated indexed review.
