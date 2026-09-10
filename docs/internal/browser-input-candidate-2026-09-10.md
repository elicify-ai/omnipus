# Dedicated browser input candidate — 2026-09-10

Status: experimental Amsterdam candidate deployed; both input modes passed the live interaction smoke. User comparison links handed off. Default input remains WebSocket. Installed Mac instance on port 10994 is untouched.

## Deployed version

- Branch: `browser-improvements`, based on `release/v0.1.1`. Main was not modified.
- Source: `f092b634d32b8acc5b9fba6b0cd18f011a4ebc3e`.
- Application: `uat-omnipus`; Amsterdam machine `784041ef9ed398`.
- Image: `registry.fly.io/uat-omnipus:browser-input-f092b634d@sha256:00cfc41716ab8abf8e0e323b66cee20425d1e0288cc35ef9690b886405879a36`.
- Binary SHA256: `0ccf3a4d99d8b8991c3b62b82bbd8f86b47111a9be3a696f64088cf74012d18b`, verified against the running process executable.
- Health endpoint returned HTTP200. Environment, services, mounts, machine resources, startup configuration and restart policy match the original machine configuration.

## User comparison

Use Browser UAT Test on the explicit workspace route; the root redirect drops query parameters.

- Dedicated input: https://uat-omnipus.fly.dev/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=dedicated
- Existing WebSocket input: https://uat-omnipus.fly.dev/workspaces/01M01TTSDZBFGM28NPHGTFZ17T/chat?browserInput=websocket

Close one panel before opening the other: both modes act on the same workspace browser. Credentials remain in the operator's existing configuration and are not included in this document.

## Changes and validation

Input uses a separate WebRTC connection with reliable typing/click/scroll/drag and a separate lossy hover channel. Media stays on the existing audio/video connection. Input failure offers explicit Retry without automatic WebSocket fallback. Controls retire old input, release its held state and wait for the acknowledged picture before further gestures.

Focused frontend checks passed, including the production TypeScript check and build. The embedded interface matches the final 669-file production manifest. Focused Go tests and race checks passed for transport, owned-input retirement, gateway lifecycle, control acknowledgments and existing command/media routing. Real WebRTC tests delivered exact Unicode text and verified dispatch joining on shutdown. Additional regression/race checks passed for navigation supersession; a canceled older navigation no longer closes the peer needed by its successor.

A Mac-to-Amsterdam connection-only check on the preceding source `50bf2a9e6` confirmed a connected data-only input peer, both expected channels, separate connected media, decoded video and live audio/video tracks, with no page errors. It did not establish audible sound, input correctness or latency. On final source `f092b634d`, the dedicated-input interaction smoke passed all 13 checkpoints: ten exact clicks, Unicode text, exact 300-pixel scroll, drag, tab return and click, resize and click, held-key release after forced input disconnection, input-only Retry and post-recovery click. Media identity was preserved throughout, with dedicated-channel-only action routing and no page errors. This checks live audio tracks, not audible content. The final baseline rerun passed all 12 checkpoints, including post-tab and post-resize clicks, held-key release, signaling reconnect and post-recovery input. Its expected media replacement during signaling reconnect is distinct from the dedicated mode preserving media during input-only recovery.

No full CI or long soak was run for this experiment. Earlier candidate CI failures and the isolated Mac memory refusal remain separate. No speed improvement or release readiness is claimed without the live comparison.

Evidence: `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/browser-input-candidate/` and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/`.

## Rollback

Original image: `registry.fly.io/uat-omnipus:browser-improvements-32de1e610-stability@sha256:b5aaaa0a383e8cda1bc8c240212ec6c02b9070b3d427078098aaae660dc0339a`. Image-only update of the same machine preserves its configuration and volume. Static preview registrations are in-memory and must be recreated after any gateway restart; preview files themselves remain on the volume.

## Early timing observations

The paired initial ten-click portions used the same harness: median click-to-observed-picture was 345.3 ms for WebSocket and 361.5 ms for dedicated input; observed maxima were 413.8 ms and 491.8 ms. Scroll observation was 438.2 ms and 474.2 ms respectively. This small sequential sample does not demonstrate a speed improvement. Measurements include viewer automation and pixel polling overhead; they are not isolated server-input latency or the 100-click acceptance benchmark. The controlled fixture has no external page load in these gesture measurements.

The first smoke clicked while the UI still displayed its input-readiness warning. No action was sent in either mode. The harness now waits for that existing status to clear before gesture stages and after control transitions, retaining exact state, route and media assertions. Failed traces remain in the evidence directory; they are not presented as successful transport measurements.

Final passing evidence is under `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/remote-smoke-gate-f092b634d/` (dedicated) and `/Users/danielpiatkowski/Documents/Agent-Workspace/omnipus/validation/browser-input-connection/remote-baseline-final-f092b634d/` (baseline). The harness readiness correction passed its focused TypeScript check. No automated browser interaction remains active at handoff.
