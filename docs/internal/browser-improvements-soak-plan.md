# Isolated browser soak acceptance

Prepared harness:
`/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-worktrees/startup/tests/e2e/browser-improvements-soak.spec.ts`

**Preparation only. The harness has not been run.** Root coordinates the actual
run after integrating and building the final application. Never run against the
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
   each followed by an A/B key pair. Every tenth cycle adds a simultaneous
   mouse/key hold, a decoded held-state checkpoint, then both releases. This is
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
