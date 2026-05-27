# ADR-016 — SPA Streaming Resilience

**Status:** Accepted
**Date:** 2026-05-23
**Deciders:** backend-lead, frontend-lead, architect

---

## Context

The Omnipus SPA was originally engineered around the assumption that the user reads a streaming reply at the rate the LLM produces tokens — a few tokens per second, with occasional tool-call frames between. In May 2026 we discovered three operational realities that broke that assumption:

1. **Idle reconnect cycle.** With no application-layer pong from the server, the SPA's client-driven heartbeat treated 60 s of WS silence as a dead connection and force-closed the socket. The resulting reconnect was invisible on desktop Chrome but accumulated handler/closure references in iPad WebKit, hitting the ~300–450 MB per-tab heap limit within minutes.
2. **Bursty error frames.** When the agent retries a failing tool (audit-degraded `web_serve`, an `exec` hitting `fork: cannot allocate`, or any rapid-retry loop), the WS produces 30+ frames per minute for the duration. The SPA's reducer ran in O(N) per frame against an unbounded message array with no virtualization — every burst trashed the GC and the un-virtualized DOM.
3. **Replay traffic on reconnect.** After a reconnect (whether ours or the network's), the server re-emitted the full session transcript every time. A two-hour session re-played in full whenever the user came back from a tab switch or a network blip.

The platform fixes that *enabled* normal session lifetimes (per-session worker, concurrent-session unblocking, handoff-state preservation) made the SPA's fragility load-bearing: the agent now stays alive across conditions where it previously died fast, and the SPA finally meets a load profile it was never engineered for.

## Decision

Adopt a three-layer "streaming resilience" architecture on the gateway/SPA boundary. All three layers ship together as the v0.1 patch on `feature/iframe-preview-tier13` and are contract-first (wire schemas in `contracts/`, generated to `pkg/api/generated/` and `src/lib/api/generated/`).

### Layer 1 — Application-layer heartbeat with explicit pong

- The SPA sends `{"type":"ping"}` every 30 s (unchanged).
- The gateway responds with `{"type":"pong"}` (new — `contracts/components/schemas/PongFrame.yaml`).
- Server response is debounced to 1 pong / 100 ms / connection so a misbehaving client cannot amplify its pings into outbound traffic against `writePump`'s serialized `sendCh`.
- The gorilla/websocket protocol-level ping/pong cycle (`SetPongHandler` on the server, `pingPump` writing control frames every 30 s) is **kept as defense-in-depth NAT-keepalive**. It works without JS being awake; the app-layer cycle works regardless of intermediary proxies that don't relay protocol-level frames.

**Why two layers:** the protocol-level cycle is silent NAT-keepalive that works when JS is throttled or paused (backgrounded tab). The app-layer cycle is the only one observable to the SPA's liveness logic, which is the one the iPad bug actually depended on.

### Layer 2 — Cursor-based reconnect replay

- `AttachSessionFrame` gets an optional `since: ISO 8601` field (`contracts/components/schemas/AttachSessionFrame.yaml`).
- When the SPA reconnects, it sends `since = bucket.lastReceivedEventTime` so the server replays only frames whose timestamp is strictly after the cursor.
- Omitting `since` requests a full replay (the legacy behaviour) — used when the SPA's bucket is empty or the cursor has never been set.
- Entries in the JSONL transcript with zero timestamps (legacy data) are treated as oldest and dropped when a non-zero cursor is set. The server logs `slog.Warn` with the count so operators see legacy sessions whose users need full replay to recover.

**Monotonicity contract:** the cursor uses RFC3339 / RFC3339Nano. SPA-side lexicographic comparison is safe only because we never accept non-`Z`-suffixed timestamps. If a future change emits offset timestamps, the cursor breaks silently. Locked in `applySinceCursor` godoc and schema description.

**Why strict `> cursor` not `>=`:** the SPA already has the boundary frame in its bucket. Re-emitting it produces a duplicate in the reducer's message map. Strict-after is correct and documented.

### Layer 3 — Tiered tool-result storage with lazy fetch

Tool results are routed by JSON-encoded size:

| Size | Wire shape | Body retrievable? |
|---|---|---|
| ≤ 50 KiB | Inline in `tool_call_result.result` | n/a — sent in band |
| (50 KiB, 1 MiB] | `ToolResultRef{_ref:true, ref, original_size_bytes, preview}` — preview is the first 4 KiB | Yes, lazy via `GET /api/v1/sessions/{session_id}/tool-results/{ref}` |
| > 1 MiB | `TruncatedResult{_truncated:true, original_size_bytes, preview}` — preview is the first 10 KiB | **No** — hard cap, full body never preserved |
| Marshal failure | `MarshalErrorResult{_marshal_error: <reason>}` | n/a |

Offloaded bodies live at `$OMNIPUS_HOME/tool_results/<session_id>/<ref>.json` with mode 0600. Session-scoped paths mean cross-session fetch is impossible at the URL level (`pkg/gateway/rest_tool_results.go` accepts both path params and joins them directly — no `WalkDir`, no ambient discovery).

When `saveJSON` fails (disk full, EPERM), `maybeOffloadResult` emits a `TruncatedResult` sentinel instead of re-inlining the 1 MiB body. Re-inlining would be a silent regression to the pre-offload behaviour that this layer exists to prevent.

**Retention:** `toolResultStore.retentionSweep` runs on the same nightly schedule as the transcript sweep, using the same `Storage.Retention.RetentionSessionDays`. Wired in `pkg/gateway/retention_goroutine.go::executeSweepTick` via the `retentionToolResultSweepFn` hook so tests can replace it.

### Frontend complements

The SPA-side bookkeeping that makes the three layers work — bounded ring buffer (`MAX_MESSAGES_PER_SESSION = 500`), Map-indexed message store, immer-based hot-path reducers, O(1) `spanByParentCallId` index, frame batching via `requestAnimationFrame`, Zod parse in a Web Worker, virtualised `<ThreadPrimitive.Messages>` with `@tanstack/react-virtual`, memory observer with `liteMode` flag — is documented in `/home/Daniel/.claude/plans/spa-streaming-refactor.md` and not duplicated here. The SPA side is implementation in service of the protocol contract this ADR documents.

## Consequences

### Positive

- Idle iPad chat survives ≥5 minutes without browser eviction (validated manually; automated burst test in `tests/e2e/burst-resilience.spec.ts`).
- Reconnect replay traffic drops from O(session history) to O(missed window) — a 2-hour session reconnecting after a 30 s blip transfers ~10 frames instead of ~10,000.
- Tool results > 50 KiB no longer pin client heap. Preview-by-default makes the chat list cheaper to render even before virtualisation kicks in.
- Server can drop the agent's runaway tool-retry storm onto the wire without taking the SPA down — the SPA bounds its own consumption.

### Negative

- More wire-format types to maintain (`PongFrame`, `ToolResultRef`, `since` field).
- Older SPAs (theoretical only — SPA and gateway ship together via `go:embed`) receiving a `ToolResultRef` they don't recognise will render a "Result truncated client-side" toast and treat the body as an unfetchable preview.
- `gorilla` protocol-level heartbeat continues to run alongside the app-layer one — small redundancy in steady-state outbound bytes.
- Cursor protocol relies on the timestamp-monotonicity contract above. If a future writer emits a non-`Z` timestamp, the cursor breaks. The contract is enforced by reading code, not by lint.

### Neutral

- `audit_degraded: true` no longer surfaces when the operator deliberately leaves `sandbox.audit_log: false` (the v0.1 default). It still surfaces when the operator asked for audit and the logger is broken, or when the skipped-write counter is non-zero. Documented at `pkg/health/server.go` and tested in `pkg/gateway/health_audit_degraded_test.go`.

## Rejected alternatives

- **Server-driven heartbeat only.** Drop the app-layer ping/pong, rely on `SetPongHandler` + the browser's transparent WS protocol pong. Cleaner protocol-wise but removes the SPA's only signal for "I am observing the server" — a half-open NAT-mangled socket (browser hasn't received the close handshake) would never close. Rejected.
- **Inlining every result up to 1 MiB.** Simpler client. Rejected: a single 1 MiB frame allocates a 1 MiB string in the React reducer, allocates another in the Zustand store, allocates another in the virtual DOM. For agents that produce 5–10 large results per turn that's enough to OOM constrained clients. The 50 KiB inline cap is the smallest size that handles realistic tool outputs (stack traces, error blobs) without offload overhead.
- **Per-tool-result file with session-id as ambient context.** The original implementation used a `WalkDir` over `tool_results/<*>/<ref>.json` and let the first match win. Discovered to be a cross-session leak (any authenticated user could fetch any other session's ref). Replaced with the session-scoped URL above.
- **Server-driven pong watchdog without app-layer ping.** Server-pings-client-replies inverts who keeps the connection alive. Cleaner against iOS JS throttling. Rejected for v0.1: changes the SPA's reconnect semantics and the test surface is much larger. Worth revisiting if the iPad symptom recurs.

## References

- Plan: `/home/Daniel/.claude/plans/spa-streaming-refactor.md`
- Heartbeat fix commit: `cb8d0ef feat(gateway): WS pong + since-cursor replay + lazy tool-result store`
- SPA refactor commit: `ead95db feat(spa): bounded ring-buffer chat store + frame batching + virtualization`
- Reviewer corrections commit: `c003523 fix(spa): historical-message renderer reaches parity with live MarkdownText`
- Cross-session leak finding: 7-reviewer pipeline, `pr-review-toolkit:code-reviewer` BLOCK #1 (in-session conversation log)
- Hard constraints (CLAUDE.md): #4 (graceful degradation), #7 (release responsibility), #8 (contract-first wire formats)
