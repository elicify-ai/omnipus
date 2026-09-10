# ADR-081 — Dedicated browser input connection

Date: 2026-09-10. Status: accepted direction for an experiment; performance unproven.

## Context

The browser-improvements candidate preserves input and resizing more consistently, but the user finds remote typing and scrolling too slow. It changed human input from a WebRTC data channel to an ordered WebSocket. Existing Amsterdam endurance evidence used a viewer on the server, not the user's Mac-to-Amsterdam path. Transport causality is therefore open; geographic delay and video buffering remain alternative contributors.

## Decision

Use two WebRTC PeerConnections per attached viewer:

1. **Input connection:** one reliable ordered channel for typing, clicks, key/button releases, scrolling and dragging; one unordered, zero-retransmission channel for replaceable hover positions only.
2. **Existing media connection:** native audio and video tracks together. Preserve encoding, clocks, buffering configuration and media ownership during this experiment.

WebSocket retains authenticated attachment, signaling, tab/navigation controls and explicit comparison mode. The new input connection belongs to that authenticated attachment; it grants no new authority. Both input channels feed the existing authorization, current-frame and input execution guards. Bound queues and release held inputs when ownership/connection expires.

A click carries its own coordinates. Drag movement stays reliable and ordered with button transitions. Hover packets carry a sequence and gesture barrier: late/reordered/duplicate positions cannot overwrite newer positions or cross a drag/tab/resize transition. Scroll deltas remain reliable; consecutive compatible deltas may be summed but never silently discarded.

Allow a development-only per-viewer A/B selector, fixed before attach: current WebSocket input or dedicated WebRTC input. No automatic fallback or dual sending. Switching modes tears down the old input owner before establishing the new one; uncertain clicks/text are never replayed. Keep the known candidate binary/image for rollback.

## Consequences and alternatives

Input transport and media recovery can operate independently, but input still pauses when the current picture is unsafe. Separating transport does not remove server queue delay, network propagation or media buffering. An additional connection requires separate signaling, authentication binding, timeout and cleanup tests. Two channels still share their input connection's congestion budget.

Do not split audio and video connections now: synchronization and independent congestion controllers add complexity without evidence of benefit. Do not introduce WebTransport, rewrite the encoder, relax the memory guard, or change tool-policy/discovery code.

## Delivery and decision gate

Implement the smallest safe A/B slice, run focused ordering/lifetime/security tests and a short real-browser smoke, then give the user the test link immediately. Do not wait for another full CI or 20-minute soak before early feedback. Label it experimental. Retain the 200 ms controlled-fixture p95 target; report remote-path timing separately without attributing website load to the harness. Do not promote the new mode by default until comparison supports it.

One combined ADR/spec grill round only, as requested. Detailed requirements and ownership are in /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/plan/browser-input-connection/spec.md and /Users/danielpiatkowski/AI-Agent-Workspace/omnipus/repo/docs/plan/browser-input-connection/tasks.md.
