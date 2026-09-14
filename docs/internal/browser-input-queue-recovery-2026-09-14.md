# Browser input recovery after a slow page

## Decision

Keep the dedicated binary WebRTC input transport and its one-second queue freshness limit. When waiting reliable input expires, retire that control source, cancel its active dispatch, and discard its pending actions. Preserve a healthy peer connection. The client requests the existing release handshake; the server joins old dispatch and releases held input before acknowledging it. Only an explicit Resume action can enable new input. Never replay abandoned clicks, text, or key presses.

A genuinely closed channel still needs connection retry. A delayed server failure reason may clarify the matching failed connection's message; it must not change retirement acknowledgment or act on a replacement connection.

Retain sixteen sanitized timing summaries throughout a long connection, beyond the initial sample quotas. Emit bounded failure windows at most once per ten seconds. Keep raw text, keys, coordinates, URLs and page content out of these diagnostics.

## Reproduction before the change

Amsterdam runtime `027306f0952e7752f5c1494063e29b740ca6f437`, September 14 at 06:50 UTC: the isolated fixture blocks its renderer for 1.5 seconds during a keydown, then the viewer sends another key after 250 milliseconds. The current implementation reports `queue_expired`, cancels the active dispatch around 1.257 seconds, and enters the failed connection state. The acceptance test fails because a recoverable pause was expected. This is a controlled reproduction of the recovery defect, not proof of what originally slowed the user's page.

The original Mia process had already exited when retrospective page profiling was attempted; a read-only panel attachment showed the browser start page. A short host sample outside the deliberate busy interval cannot establish host pressure during the user's earlier failure. Its CPU-steal fraction was 0.04%; cumulative counters and unrelated time windows are not causal evidence.

## Validation required for the candidate

- Expiry preserves a negotiated peer and both input channels.
- Old control cannot resume, including after its worker exits.
- Release joins active dispatch and clears held state before acknowledgment.
- Pending old actions never replay; fresh control starts its own sequence.
- Repeated media availability cannot silently resume paused input.
- Native channel errors still retire the connection; delayed reasons remain identity fenced.
- Long sessions retain bounded diagnostics, including connection closure without active input.
- Repeat the same Linux renderer-stall fixture and ordinary binary/international input checks after deployment.

The change makes recovery safer and more informative. It does not claim to eliminate page or Chrome stalls, or reduce measured input latency.

## Verified candidate

Amsterdam machine `784041ef9ed398` runs source `6be7ce9f07fce3626a34bb453f0f88ba6625bb3e`. Installed and running binary SHA256 both match `3116abc2d25a8c00570402fdbe1452c9e6a2313ebdb166788afdf13b6a7b2c0b`. Machine configuration and region were preserved; input timing remains enabled.

The same Linux renderer-stall regression passed (23.2-second test body): the original two input channels and media connection survived, the queued A key never executed, B was released, and explicit Resume admitted exactly one fresh C down/up. Final state was two downs, two ups, zero held keys, zero errors and one C action. All eight gesture packets used binary dedicated input. At 07:26:00 UTC, the logs retained both the `queue_expired` failure window and the canceled active dispatch (about 1.337 seconds after Chrome dispatch began). The controlled fixture deliberately caused that wait; it is not evidence of harness-added delay.

The broader normal-URL live test passed (53.6-second test body), covering exact clicks, Unicode, 300-pixel scrolling, dragging, tabs, resizing, held-key cleanup, actual connection loss, Retry and a post-recovery click.

Focused validation: 35 frontend tests and relevant lint passed; production TypeScript/Vite and Linux builds passed. Gateway dedicated-control/timing and input-sink checks passed. New queue/native-peer recovery tests passed. Two existing native connection subtests timed out in the first aggregate run and passed unchanged when rerun in isolation; the initial result is retained. Three isolated queue defects, four client defects and missing canceled-dispatch diagnostics were caught by deliberate mutations. Five isolated timing tests also passed. Independent reviews found no remaining concrete blocker in this change.

A bounded host sample covering the deliberately busy interval recorded 19.85% aggregate CPU busy time and 0.64% CPU steal over roughly four seconds. These numbers describe this controlled test only; they do not identify the cause of the user's previous slowdown. Full CI, extended stability and physical OS input-method coverage remain outside this focused validation.

The native international-composition live test also passed (31.8-second test body): exact final text `a@日本é🙂🙂`, four committed insertions, three physical key pairs, zero held keys and zero fixture/viewer errors. Canceled and blurred candidates did not leak into the remote page. This uses the Chromium test driver and is not exhaustive physical keyboard or OS input-method coverage.
