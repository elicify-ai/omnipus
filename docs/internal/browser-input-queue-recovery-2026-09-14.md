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
