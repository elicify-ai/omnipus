# ADR-081 — Dedicated browser input connection

## User-directed amendment — 2026-09-13

Dedicated WebRTC gesture input is mandatory on this branch. The user explicitly revoked the A/B/default-WebSocket rollout after a misleading manual handoff. Normal URLs and any legacy `browserInput=websocket` query must select dedicated input. Mouse movement, buttons, keys, text and wheel must never execute through WebSocket or the media peer's legacy data channel, including before dedicated negotiation and after failure. There is no fallback. WebSocket remains solely for authenticated setup, navigation/tab/viewport intent and control/release coordination. Missing or failed dedicated input must remain visibly unavailable with explicit recovery.

Verify default and legacy URLs against the real dedicated input session, reject legacy gestures at server boundaries, preserve safe control and held-release behavior, and migrate old test fixtures without restoring obsolete fallback expectations. Existing historical comparisons below remain evidence from earlier revisions, not current route requirements. User's latest dedicated-mode errors and viewport timeout reports remain an independent active stability investigation; do not claim they are fixed merely by changing the default. No new design-review round or approval is required for this explicit instruction.

Date: 2026-09-10; amended 2026-09-13. Status: mandatory dedicated input accepted; performance improvement remains unproven.

## Context

The browser-improvements candidate preserves input and resizing more consistently, but the user finds remote typing and scrolling too slow. It changed human input from a WebRTC data channel to an ordered WebSocket. Existing Amsterdam endurance evidence used a viewer on the server, not the user's Mac-to-Amsterdam path. Transport causality is therefore open; geographic delay and video buffering remain alternative contributors.

## Decision

Use two WebRTC PeerConnections per attached viewer:

1. **Input connection:** one reliable ordered channel for typing, clicks, key/button releases, scrolling and dragging; one unordered, zero-retransmission channel for replaceable hover positions only.
2. **Existing media connection:** native audio and video tracks together. Preserve encoding, clocks, buffering configuration and media ownership during this experiment.

WebSocket retains authenticated attachment, signaling and tab/navigation controls. The new input connection belongs to that authenticated attachment; it grants no new authority. Both input channels feed the existing authorization, current-frame and input execution guards. Bound queues and release held inputs when ownership/connection expires.

A click carries its own coordinates. Drag movement stays reliable and ordered with button transitions. Hover packets carry a sequence and gesture barrier: late/reordered/duplicate positions cannot overwrite newer positions or cross a drag/tab/resize transition. Scroll deltas remain reliable; consecutive compatible deltas may be summed but never silently discarded.

All viewers use dedicated input. Historical URL selectors cannot enable WebSocket gestures. No fallback or dual sending; uncertain clicks/text are never replayed. Keep the known candidate binary/image for an explicit deployment rollback.

## Consequences and alternatives

Input transport and media recovery can operate independently, but input still pauses when the current picture is unsafe. Separating transport does not remove server queue delay, network propagation or media buffering. An additional connection requires separate signaling, authentication binding, timeout and cleanup tests. Two channels still share their input connection's congestion budget.

Do not split audio and video connections now: synchronization and independent congestion controllers add complexity without evidence of benefit. Do not introduce WebTransport, rewrite the encoder, relax the memory guard, or change tool-policy/discovery code.

## Delivery and decision gate

Deliver mandatory dedicated routing, run focused ordering/lifetime/security tests and a short real-browser smoke, then give the user the test link immediately. Do not wait for another full CI or 20-minute soak before early feedback. Label it experimental. Retain the 200 ms controlled-fixture p95 target; report remote-path timing separately without attributing website load to the harness. Default routing follows the user’s explicit decision; do not claim a speed gain without measurements.

One combined ADR/spec grill round only, as requested. Detailed requirements and ownership are in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/plan/browser-input-connection/spec.md and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/plan/browser-input-connection/tasks.md.
