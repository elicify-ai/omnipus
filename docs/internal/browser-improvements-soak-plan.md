# Isolated browser soak acceptance

Prepared harness:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/startup/tests/e2e/browser-improvements-soak.spec.ts`

**The first full run failed latency acceptance** (details below). Root
coordinates further runs after integration. The new evidence/diagnostic
correction has been typechecked but has not been executed. Never run against the
installed gateway on port 10994. This spec refuses any gateway except localhost
or 127.0.0.1 port 11094 and requires an explicit isolated runtime home beneath the
Omnipus project workspace.

## Acceptance and independent oracle

1. Seed one self-authored HTML page beneath the isolated runtime workspace's
   existing `work` directory. Jim uses `serve_web`, then `browser_navigate` to its
   allowed gateway-origin preview URL. No additional input tools are requested.
   Open Watch live and navigate to that preview again through the real address
   bar. Wait for the authored zero-event picture in the actual video stream.
2. Observe a genuinely static page for ten minutes. Every five seconds, decode
   its visible pixels and check the original MediaStream/video track, browser
   socket, capture identity and generation. Do not require new frames merely
   because time passed: a static source may legitimately stop producing them.
   Renegotiation, replacement, failure/recovery and unexpected input fail.
3. For ten further minutes, issue 100 normal clicks spaced six seconds apart,
   each followed by an ArrowLeft/ArrowRight key-down/up pair. These nonprintable
   keys use the UI’s forwarded key-event path; printable characters use
   `Input.insertText` and are not a native key-down/up oracle. Every tenth cycle
   adds a simultaneous mouse/key hold, a decoded held-state checkpoint, then both releases. This is
   550 expected native page events: 330 mouse events and 220 key events. The
   first click proves interaction still works after the full idle period.
4. Read only the decoded `<video>` pixels for action results. A 96-bit visible
   grid contains event count, rolling order checksum, last event, held bits,
   first mismatch index, and a unique run nonce. The page separately compares
   every actual trusted-input event against the predeclared 550-symbol sequence
   and latches its first mismatch. The checksum is diagnostic, not the sole
   order oracle: loss, duplication or reordering cannot pass through a checksum
   collision. The harness never reads the captured page's DOM or internal state.
5. All 100 normal clicks must appear in decoded video. Start timing at the
   viewer's trusted `pointerdown` listener, before React dispatch; finish at the
   first video-frame callback whose decoded state matches that click. Nearest-
   rank p95 (sorted sample 95 of 100) must be at most 200ms. Held sequences are
   separate and do not inflate or dilute those 100 samples. Polling only obtains
   the already-recorded browser timestamp, so it does not set the latency.

A magenta page border identifies the viewport inside encoded padding; binary
cells are sampled at their centers with explicit black/white thresholds. Any
unreadable or stale picture fails within the checkpoint budget. Human-readable
count, checksum, held state and first-error text accompany the binary grid.

## Runtime setup and evidence

The coordinator supplies `OMNIPUS_URL=http://localhost:11094`,
`SOAK_RUNTIME_HOME` pointing to the existing isolated runtime home, and a valid
runtime-owned Playwright authentication state. Use a minimal configuration
stored under the runtime directory with this one spec, one worker, zero retries,
baseURL 11094 and the already-supported Chromium viewer launch flags. Do not use
the repository-wide global setup: it can seed gateway configuration and is not
needed by this acceptance test. Authentication/setup failure is a failed or
blocked run, never a skipped pass. No short-duration environment override exists.

The spec records before-idle, after-idle and final video screenshots, plus JSON
containing phase start/end times, every click's timestamp/latency, static pixel
samples, capture/offer lifecycle observations and uncaught viewer errors. It
retains no SDP or credentials. The fixture remains only inside the runtime's own
work directory. Normal test teardown closes the viewer; it never restarts the
server or managed browser.

Explicit limits: audio content, native macOS app behavior and a remote-network/
TURN path are **unverified** by this local Chromium harness. No preparation,
TypeScript check or fixture screenshot counts as a successful 20-minute run.
Before reporting acceptance, retain the actual terminal result and evidence;
if p95 exceeds 200ms, report the measured value without widening the threshold.


## Evidence persistence correction

The first actual 20.7-minute run completed all 550 event/order/held-state and
continuity assertions, then failed the unchanged 200ms threshold with
p95=247.199999928ms. The line reporter did not persist attachment bodies, so
its detailed in-memory JSON and phase screenshots were unavailable afterward.
This is an evidence defect, not a passing latency result.

The harness now writes each PNG and JSON to `testInfo.outputPath` before
attaching its file path. A line-only reporter therefore retains the files.
Phase start/completion and the final measured count/p95 are also printed.
The full acceptance durations, 100-click sample and 200ms threshold are unchanged.

## Short latency diagnostic (not soak acceptance)

`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/startup/tests/e2e/browser-improvements-latency.spec.ts`
uses the same authored fixture, decoder and UI setup through
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/startup/tests/e2e/fixtures/browser-input-probe.ts`.
It performs 100 clicks, paced over at least 50 seconds, with 300 exact mouse events
and no ten-minute idle or mixed-key phase. Its 200ms p95 check is unchanged; a
short diagnostic pass does not establish the soak acceptance criteria. Select
one spec explicitly so both do not run accidentally.

Only this diagnostic installs browser-side observation before application code:

- Retain real viewer `RTCPeerConnection` instances without changing their
  configuration. Record actual receiver `jitterBufferTarget` and legacy
  `playoutDelayHint` values; absence is null, not an assumed setting.
- Observe WebSocket/data-channel mouse sends around the original native send
  method. Record route, pointerdown time, attempted/returned send times and
  success. No payload, SDP or coordinates are saved, and send exceptions retain
  their original behavior. A successful send means local enqueue, not delivery.
- Record the matching decoded-frame callback time, pixel-sampling duration and
  browser-provided presentation/expected-display, processing, capture/receive
  and RTP timestamps when available. All viewer timestamps share its monotonic
  clock; browser-provided capture/receive metadata remains raw diagnostic data,
  not a claimed synchronized measurement of the remote browser.
- Snapshot video receiver statistics before input, after every 20 clicks, and at
  completion/failure. Persist raw counters for jitter-buffer delay/target/emitted
  count, decoding/processing time, decoded/dropped frames, frame rate, packet
  loss, jitter, candidate-pair/remote RTT and feedback counters when supported.
  Printed derived means use counter deltas divided by their corresponding
  emitted/decoded counts. Missing/reset counters produce null, never fabricated
  zero latency. RTT remains available in the raw candidate/remote reports.

The diagnostic writes `latency-evidence.json` and real PNG files beneath the
runner's configured output directory before attaching paths. It prints progress
at 20-click milestones, final p95/count and receiver means. Instrumentation cost
is visible in the per-click sampling duration; the diagnostic neither tunes
production settings nor bypasses the UI input route. No runtime measurement of
this correction is claimed yet.


## Decoder overhead correction

Diagnostic 69699 completed all 300 ordered mouse events but failed p95 at 267ms.
Its retained evidence showed pixel sampling mean 23.81ms, p95 60.9ms and maximum
139.7ms. Those costs cannot be subtracted to claim a passing physical-display
latency; the earlier run remains a failed latency result.

The test decoder now locates the authored magenta border only on initial
sampling or a change in native video width/height. It retains those original
border coordinates for pointer mapping. Each subsequent sample crops the same
12-by-8 authored binary grid directly from the decoded video into a fixed 12-by-8
canvas with image smoothing disabled, then reads all 96 actual RGB pixels.
The black/white ambiguity thresholds, bit order, run nonce, exact count/order
latch, held-state checks, callback timing and 200ms acceptance threshold remain
unchanged. No fixture values are substituted into observed pixels.

The revised decoder passed targeted TypeScript checking; its runtime sampling
cost and latency are not yet measured. Independent review and the coordinated
short diagnostic must precede any claim of improved measurement overhead.

## Explicit video-only A/B experiment

The optimized-decoder diagnostic 71735 still failed latency at p95 306.1ms,
while observed sampling cost fell to mean 2.48ms/p95 4ms/maximum 4.6ms. Receiver
buffering was substantially above the reported network-minimum delay. Audio/
video synchronization is a hypothesis to test, not an established cause.

The separate spec
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/startup/tests/e2e/browser-improvements-video-only-latency.spec.ts`
selects only the explicit `latency-video-only` mode. Its harness-owned peer
constructor overrides an actual `addTransceiver('audio', ...)` request to use
`direction: 'inactive'`. Video negotiation, input routing, the exact 300-event
oracle, 100 clicks, at least 50-second pacing and 200ms p95 check are unchanged.
The ordinary diagnostic passes the original transceiver arguments unchanged;
the full soak installs no such observer or override.

The experiment persists its mode in `latency-video-only-evidence.json`, records
the override count and each audio transceiver's requested/current direction,
and asserts that audio is inactive or unnegotiated and that any audio receiver
report has zero packets. The existing 100 decoded-click and real video receiver
statistics assertions remain mandatory. An experiment whose override never ran
or whose audio remained active fails instead of being classified as video-only.

Both short diagnostics now retain audio receiver statistics alongside video,
including buffer/sample/concealment/energy counters where supported. Derived
video means explicitly filter video reports, so audio cannot dilute the video
summary. Missing fields remain null. This is test-only instrumentation; a faster
video-only result would not be a product fix, an audio acceptance result, or a
passing 20-minute soak. Runtime execution is still coordinated by root.
