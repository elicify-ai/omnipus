# Isolating streamed-browser input delay

Status: diagnostic implementation passed focused verification and independent review; live stage measurements pending.

The prior Linux measurements place most variable delay between the viewer sending a mouse-up and the corresponding changed video frame arriving. They do not distinguish server input admission, Chrome input processing, rendering, capture, encoding, and transport. The existing 100-click test uses an immediate canvas response, without website navigation or network requests. Website loading time must not be counted as Omnipus overhead.

## Measurement boundaries

- Viewer: trusted pointer dispatch, outbound mouse-down/up send, changed-frame arrival and presentation metadata, pixel-sampling duration.
- Server: WebSocket message read completion, queue submission/start, live-input admission, tab-operation gate, input gate, coordinate mapping, final Chrome input command start/completion, and handler completion. These offsets use one Go monotonic clock.
- Controlled page: event handler entry, drawing-command start/completion, and bracketed wall-clock samples. Drawing-command completion is not proof of physical paint or completed video capture.
- Media: selected candidate pair and its actual protocol/address/ports, plus existing audio/video receiver statistics. A partial packet observation cannot establish that the entire media path is timely.

Server diagnostics require `OMNIPUS_BROWSER_INPUT_TIMING=1`, are disabled by default, and are bounded to 512 mouse-down/up records per connection. Logs exclude input text, keys, coordinates, page addresses, and arbitrary client-supplied identifiers. Synchronous logging occurs after the recorded completion boundary; its possible effect on following queued inputs must remain visible in the overall test and should be compared with a control.

Target-page diagnostics require `BROWSER_PROBE_TARGET_TIMING=1`. Timestamps are retained in memory, without per-click network traffic, and retrieved after the measured window using the already-authorized browser evaluation tool. An initial HTTPS-to-loopback collector preflight failed before any request reached the collector; no browser security flags were weakened. That collector approach is not part of the implementation.

## Clock and attribution limits

Server offsets can be compared directly within one input. Viewer and target `performance.now()` values have different origins and must never be subtracted directly. Linux target and viewer run on the same machine; bracketed `Date.now()` readings provide wall-minus-monotonic bounds, including millisecond quantization and bracket duration. Before/after samples must be checked for inconsistent offsets. This is a same-host experiment, not evidence of synchronized remote clocks.

Report the controlled page's own processing/drawing work and the test's pixel-detection overhead separately. Do not label the entire click-to-picture interval as Omnipus processing time. Even after these measurements, rendering/capture/encoding/delivery may remain a combined interval until additional evidence separates them.

## Separate regression work

The consolidated checks for commit `7ada86e18` reported unchecked type assertions in the new media-owner test, a test-fixture data race from changing the browser executor after asynchronous attachment discovery had started, and tab-switch recapture failures. The first two received test-only corrections in `17a38d33c`. Five focused repetitions after the fixture correction reported no race but still reproduced tab-switch functional failures.

A deterministic regression subsequently proved that late document-listener initialization replaced an already measured tab-switch picture: its generation changed from 5 to 7 without any page navigation. The correction makes replacement-listener initialization discover metadata only when the real tab-change path already owns measured refresh. Initial attachment, baseline recovery, failed-watch recovery, and genuine navigation retain picture initialization/invalidation. The test also queues real navigation during blocked discovery and requires old-picture invalidation followed by one measured recapture. Independent review found no remaining high-confidence issue.

After restoring three deliberate guard faults, 27 affected race-test cases passed with no skips or races. All four original failure groups then passed five repetitions each (20 passes, no skips or races). The corrected tab-switch source is separate from the deployed timing experiment's `17a38d33c` snapshot. Live verification of this additional correction and final combined checks remain pending. No latency threshold or failing assertion has been weakened.

Evidence is retained under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-review-evidence/` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/`. The branch remains `browser-improvements`; `main` and the installed Mac instance are untouched.

## Focused verification

The restored Go timing tests recorded 19 passing test/subtest entries, no skips, and no race warnings across the affected browser and gateway packages. Three deliberate faults independently failed the pointer-mapping stage, record-limit, and millisecond-offset assertions; all were restored before the final run. Seven standard Vitest cases and the focused TypeScript check passed. Three separate JavaScript faults were caught for handler timing, pixel-state integrity, and clock quantization margin. Independent reviews of both implementations found no remaining high-confidence issue. These checks validate the measurement machinery; they are not live speed acceptance or a replacement for the still-failing tab-switch checks.
