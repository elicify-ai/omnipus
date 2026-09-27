# ADR-095 — Thinking & reasoning: the per-login visibility gate and the signed-thinking-block plumbing

- **Status:** Accepted — the one `grill-spec` ADR-mode round is complete (verdict **BLOCK**, `ADR-095-thinking-visibility-gate-and-signed-blocks-review.md`); the founder interview it triggered produced **D15–D21** in the spec's Decisions Log (`docs/internal/specs/spec-thinking-reasoning.md`); this document is that round's **one correction**, resolving every review finding F1–F15. It now feeds `plan-spec`. ADR-local decision IDs **D1–D8** below are this ADR's namespace only — the spec's D1–D21 live in the spec's Decisions Log and are cited as "spec D<n>".
- **Date:** 2026-09-27 (written and corrected same day, one process round)
- **Deciders:** Daniel Piatkowski (founder — spec D15–D21, via team-lead, answering the grill's Q1–Q7); architect (technical shape, original and this correction)
- **Evidence baseline:** this correction re-verified every code claim first-hand on `feat/thinking-reasoning` @ `a5f02fc345` (worktree `thinking-reasoning`), read-only. The spec's CF1–CF11 were verified on `origin/release/v0.1.1` @ `73bdad862`; this worktree is cut from `4d9ecc13c` on the same lineage.
- **Related:** `docs/internal/specs/spec-thinking-reasoning.md` (Decisions Log, source of record for *why*); the grill round's review file (findings F1–F15, superseded Q1–Q7 — answered); issues #703, #274, #751, #752, #929, #750, #943; `elicify-ai/omnipus-provider-catalog#29`.

## Correction record (2026-09-27)

The grill round overturned the ADR's two core designs and the founder ruled the fixes:

1. **Persistence — spec D15 amends spec D12/D13.** The original D8 ("raw thinking is memory-only; strip-on-write for `context.jsonl`") was proven impossible by review finding F1: the LLM-context history is re-read from disk at the start of every turn (`pkg/memory/jsonl.go::GetHistory` → `readMessages`, no cache) and again at every mid-turn rebuild (proactive trim, timeout recovery, context-overflow recovery), so a memory-only store loses cross-turn re-send on the very next turn and strips the signed block Anthropic requires to accompany a `tool_use` block. The founder ruled: **raw thinking stays in `context.jsonl` on disk, as it already does today** — D8 is replaced below.
2. **Visibility — spec D19 amends spec D2's gate.** The original D2 put a single construction-time gate keyed on one owner account. The founder ruled **per-login visibility**: each login's own toggle decides for that login. This reverses the original rejection of hub-level filtering; the redesigned mechanism is decision D2 below.

Every other finding (F2–F15) is worked into the decisions below; the mapping is recorded in the Consequences and in the correction's delivery report to squad-lead.

## Context

### The two structural questions, as they stand after D15/D19

**The gate (spec D2 + D19).** Thinking is stored unconditionally; the server withholds it per **login** across every SPA-visible surface — live streamed frames, journal catch-up, attach snapshot/replay, REST reads — unless **that login's** toggle is on; toggling on later reveals older sessions. Verified mechanism facts the design stands on:

- `pkg/gateway/ws_session_hub.go::publishMeta` serializes each frame once, journals the exact seq-stamped bytes, and enqueues the same bytes to every bound connection. `hubConn` is only `enqueue(frame []byte) bool` — the hub itself holds no per-connection identity.
- But the identity already exists one layer down: `pkg/gateway/websocket.go::wsConn` carries `userID` ("username resolved at auth time; used for session_state scoping (FR-073)", set by `websocket.go::authenticateWS`) and `isCLIToken`.
- Per-connection delivery decisions are established precedents in this package: notifications are delivered "ONLY to the recipient user's connections (filtered by `wc.userID`)" (#264, `pkg/gateway/websocket_forward.go`), and WhatsApp-pairing frames are scoped to connections that subscribed (#283, `websocket_forward.go::onWhatsAppPairing` via `websocket.go::wsConn.wantsPairing`).
- `pkg/gateway/auth.go::resolveBearerIdentity`/`checkBearerAuth` distinguish real accounts from synthetic identities: a real login resolves to a pointer into `cfg.Gateway.Users` (bearer, id-tagged or legacy, or the `omnipus-session` cookie via `middleware.ResolveUserFromCookie`); the CLI token resolves to a synthetic `UserConfig{Username: "cli"}` flagged `ViaCLIToken`; dev bypass and the legacy env token resolve to the synthetic `devBypassUser` (`_dev_bypass`) and `envTokenUser` (`_env_token`).
- `pkg/gateway/ws_hub_projection.go` — a snapshot catch-up is "persisted transcript, read AFTER the connection was bound" **plus** the projection delta of "published but not persisted" state; `hubKindDone` clears it (persist-before-done). Frames that update no projection item are the no-op class.
- `pkg/gateway/sse.go` — `POST /api/v1/chat` is legacy: `partitions` is always nil, it is not the bus stream delegate ("the WebSocket handler is the primary stream delegate"), and `sseStreamer` is a bare token pipe implementing `Update/Finalize/Cancel`.
- Streamers that write transcripts exist far beyond the web: `pkg/gateway/sse.go`, `pkg/gateway/websocket_streamer.go`, `pkg/channels/manager.go`, `pkg/channels/telegram/telegram.go`, `pkg/channels/wecom/wecom.go` all implement `Finalize`; transcript entries are also written agent-side by `pkg/agent/turn_transcript.go`'s writers (including the intermediate assistant entry).

**The carrier (spec D5b).** CF4 stands re-verified: no Anthropic thinking-signature capture or block round-trip exists. `pkg/providers/anthropic/provider.go::parseResponse` flattens `thinking` blocks into `LLMResponse.Reasoning`; the streaming switch counts bytes only (`case "thinking"` / `case "redacted_thinking"`); the streaming path funnels into the same `parseResponse` via `msg.Accumulate` → `parseResponse(&msg)`. Anthropic's real block shapes (context7 `/llmstxt/platform_claude_llms_txt`, and `anthropic-sdk-go` v1.48.0 `message.go::ThinkingConfigAdaptiveParam`): `thinking` = `{type, thinking, signature}` (signature "must be returned exactly as received"); `redacted_thinking` = `{type, data}` ("opaque and encrypted; pass it back unchanged"); the request-side thinking config carries `display` with values `summarized` / `omitted`, `omitted` being the not-set default (`display,omitzero`).

### A persistence fact the original decisions were not made against (now the foundation)

`context.jsonl` carries `reasoning_content` today: `pkg/memory/jsonl.go::addMsg` marshals `ArchivedMessage{Message: msg}` wholesale, `rewriteJSONL` marshals each message wholesale, and `pkg/agent/loop_run_turn_response.go::recordToolCalls` sets `ReasoningContent` on the stored assistant message. `GetHistory` reads the file fresh on every call. `pkg/agent/session_recovery.go::RecoverOrphanedToolCalls` is safe to call on every session load — it is not a restart-only event (the original ADR mislabeled it one; review F1 corrected this). D15 makes this the foundation instead of a problem.

### ADR-057 FR-038, stated precisely

`pkg/gateway/replay.go::streamReplay`'s contract comment ("no read boundary may reintroduce a transcript visibility filter") — from ADR-057, "**ADR-057: Unify delegate sub-turns onto the own-session execution path**" — forbids a filter whose subject is **parent/child delegation provenance** (after FR-034/FR-038, child narration never lands in the parent's transcript, so there is nothing to filter). The D19 gate is **user-preference-scoped**, a different subject; it does not resurrect the FR-038-forbidden filter. Stated so no reviewer reads a contradiction where there is none.

## Decisions

### D1 — Toggle home: `config.UserConfig.ShowThinking`, per login row (amended for D19)

The per-login "show thinking" toggle is `ShowThinking bool` (wire key `show_thinking`) on `pkg/config/config.go::UserConfig` — one per account row, so each login's own row decides for that login (spec D19). Live-read at every read boundary — deliberately **not** added to `RestartGatedKeys` (`pkg/gateway/rest_pending_restart.go`; the restart-gated vs live-read classification of [ADR-044 — "Serve /preview/ on the main gateway listener (path approach)", `ADR-044-preview-on-main-listener.md`]). Runtime mutation follows the existing Users-row mutation precedent (login/logout already mutate `Users` rows and persist `config.json`).

The toggle is **not** writable through generic config writes — `gateway.users` is a blocked path (`pkg/gateway/blocked_paths.go`), which also keeps the agent's config tool away from it. It gets a dedicated contract-first REST endpoint (spec T2's one contract change), which never serializes any `UserConfig` hash. Toggle changes are **audited** (an audit row records the login and the new value; the audit value is the boolean, never a token or hash). Each toggle persists `config.json` and takes the existing full config-reload path — accepted (F14).

- **Rejected:** a generic per-user preference store — no precedent in the repo; `UserConfig` already is the per-user state.
- **Rejected:** a global (not per-login) setting — contradicts spec D2/D19.

### D2 — The gate: per-login visibility, enforced per connection at every delivery boundary (redesigned for D19)

One predicate, `visibleFor(identity)` — live-read at every boundary:

> Resolve the authenticated identity of the connection or request. A real `Gateway.Users` row → that row's `ShowThinking`. A synthetic identity (CLI token — `wsConn.isCLIToken` / `AuthResult.ViaCLIToken`; dev bypass — `_dev_bypass`; env token — `_env_token`), a lookup miss (a login whose row no longer exists), or a config-read error → **toggle off, no thinking shown** (fail closed).

This adopts the founder's recommendation for rowless logins; nothing in the code makes it unworkable: the synthetic identities are already first-class distinguishable (`auth.go`'s `AuthResult.ViaCLIToken`, the `_dev_bypass`/`_env_token` synthetic usernames, `wsConn.isCLIToken`), so "off" is a clean, exact default rather than a guess. The CLI collision edge (a real account literally named `cli`) is covered because `isCLIToken`/`ViaCLIToken` key on *how* the caller authenticated, not the username string.

**Boundary 1 — WS live (the hub).** Per-connection filtering in the hub — the mechanism this ADR originally rejected and D19 now requires. It is contained surgery, not a rewrite, because the journal stays single-copy and every gated component already exists:

1. Producers publish thinking frames **unconditionally** — this reverses the original D2's "the streamer does not build or submit thinking frames at all" (construction-time gating cannot serve two logins with different toggles viewing one session). Producers mark the frame via `hubFrameMeta` gaining a thinking flag.
2. `sessionHub.publishMeta` journals the full thinking bytes once; per bound connection, the enqueue loop substitutes a content-free `seq_skip {type, session_id, seq}` when that connection's gate says hidden. The placeholder retains the hidden frame's sequence, contains zero thinking bytes, and is neither overflow nor a disconnect.
3. `journalEntry` gains `thinking bool`, set at publish; `sessionHub.bind`'s journal-tail loop performs the same substitution for a hidden connection during reconnect catch-up. A gated login receives no thinking payload, but its cursor still observes a contiguous sequence (spec D29).
4. The **projection is deliberately kept out of the gated surface set**: thinking frames are the no-op projection class (`hubKindOther`-behavior — `ws_hub_projection.go::activeTurnProjection.update`'s default branch), so the projection needs no thinking awareness and a snapshot's projection delta is thinking-free by construction. A snapshot's thinking arrives only through the gated transcript read (Boundary 2). Accepted cost: a mid-turn attach shows thinking-so-far only up to the last intermediate transcript persist — the same accepted-cost class the projection budget already documents for evicted items.
5. Thinking frames **never** ride `websocket_forward_hub.go::hubPublishAndDeliverAlsoTo`'s `alsoTo` unsequenced side-channel, never `hubBroadcastWithSequencedCopy`'s broadcastOthers leg, and no unsequenced copy of a thinking frame ever exists.

The delivery guarantee narrows deliberately and is documented as such: every visible frame is delivered byte-identical to every connection the gate delivers it to; for a hidden thinking frame, the gated connection instead sees **contiguous sequence via a content-free skip placeholder and zero thinking bytes**. This preserves the SPA's cursor contract (`src/store/chat/cursor.ts::gateFrameBySeq`, `src/store/chat/slices/frames.ts::applySeqGate`) and prevents false re-attach diagnostics (spec D29). Filter semantics otherwise stay consistent because the filter is deterministic per (login, toggle state): D16 (founder-confirmed) accepts reconnect re-delivery of previously shown thinking; toggle-on-later makes journaled thinking deliverable to that login from the next tail replay or transcript read — exactly spec D2's "turning it on reveals older sessions". The BE-DESIGN.md §1/§2 "byte-identical to every bound connection" phrasing is narrowed by this decision for thinking frames only; the ADR records the narrowing rather than editing that design doc.

Precedents already in the package: #264's per-user notification filter and #283's per-connection pairing subscription (both named in Context).

**Boundary 2 — WS attach snapshot / transcript replay.** `pkg/gateway/replay.go::streamReplay` gains the requesting login's gate result as a parameter, supplied by its production caller `pkg/gateway/websocket_replay.go::streamReplay` (the attach path, which holds the `wsConn` identity). With the toggle off, replay emits message frames with thinking fields omitted and emits no thinking-row frames; with the toggle on, thinking rows replay from the stored redacted copy (spec D3).

**Boundary 3 — REST.** Both serializers are gated (review F8): `pkg/gateway/rest_sessions.go::getSession` → `jsonSessionDetail`, and `getSessionMessages` — served `TranscriptEntry` rows are stripped of thinking fields unless `visibleFor` says on for the requesting principal from the request context.

**Boundary 4 — SSE, ruled out of thinking delivery entirely.** `POST /api/v1/chat` is legacy (`sse.go`: no partitions, not the bus stream delegate, token pipe only). Thinking delivery to streamers goes through an **optional web-only add-on interface** (implemented by `websocket_streamer.go`'s streamer and the hub-publishing webchat producer, `webchat_channel.go`); `sseStreamer` does **not** implement it, and the turn-side delivery type-asserts the add-on, so SSE receives nothing — no thinking event type is added to SSE. There is nothing to gate: SSE is a no-thinking surface by construction, which trivially satisfies the gate. Channel/messenger paths are out of scope by spec D1 (web chat only); of the five `Finalize` implementations the review listed, only the web ones implement the add-on and can ever receive thinking — the channel streamers never do, so they need no gate.

- **Rejected:** construction-time gating keyed on one owner account — cannot serve per-login visibility (spec D19); superseded.
- **Rejected:** per-connection **serialize-per-connection** at publish — pays per-delta full-frame serialization for every connection; the single-copy journal with content-free substitution at enqueue delivers the same visibility outcome without serializing thinking per connection.
- **Rejected:** client-side hiding — spec D2 forbids it outright ("no client can bypass the gate by reading raw frames or replay data").
- **Rejected:** filtering at the toggle endpoint only / trusting the SPA — not server-side; D2 requires server-side.

### D3 — Journal catch-up edge: accepted, documented (founder-confirmed as spec D16)

Toggle ON→OFF mid-session leaves thinking frames in the hub journal from the ON period; reconnect catch-up re-delivers those bytes to the same login — a deliberate, deterministic filter over a journal that keeps the full bytes. **Accepted** (spec D16): those bytes were legitimately delivered to the same user while the toggle was on. The gate's promise is against *bypass* (never-delivered data reaching a client), not re-delivery of previously delivered data. Bounded by journal retention (`hubJournalMaxFrames`/`hubJournalMaxBytes`, idle sweep, global budget). No catch-up-time filtering — parse-and-rewrite in the catch-up path buys nothing the gate's fail-closed default doesn't already prevent.

### D4 — Capture, redaction, and the reasoning-only fallback (amended; spec D20 applied)

Capture happens **once per round, in the turn itself** (the `turnState` pipeline), not in a streamer — review F2: many turns have no web streamer (channel inbound, cron, heartbeats, task runs, steered sessions, non-streaming providers), and transcript entries are written agent-side by `pkg/agent/turn_transcript.go`'s writers (including `appendIntermediateAssistantTranscript`). A capture point inside a streamer would store no thinking for those turns, breaking spec D2's "stored unconditionally".

Redaction happens once, at capture, on a **copy**: the turn applies the audit credential-pattern redactor (spec D3/CF10) to the **whole accumulated thinking text**, never to individual deltas — review F3: a secret split across two streamed pieces would match no per-piece pattern. Live frames carry only text from the redacted accumulated string that can no longer match any pattern (a bounded hold-back tail); on attach the full redacted text-so-far is sent as one snapshot. The raw text stays in the loop's in-memory response for the provider round-trip (D15 keeps it on disk too — see D8). Live and stored copies are identical by construction (spec D3's identical-render rule).

**The reasoning-only fallback is removed** (spec D20): the two substitution sites in `pkg/agent/loop_run_turn_iterations.go` (`er.cn.responseContent = …ReasoningContent` and `cn.responseContent = …ReasoningContent`) and the `pkg/agent/session_end.go` `resp.Reasoning` substitution are deleted. A round with thinking but no answer text produces a typed no-answer outcome (contract work under spec T2) that the SPA renders as the D20 gentle notice ("The model (Provider · Model) did not respond", #711 console-strip style, with Retry); the thinking itself still goes into the normal gated thinking row — the gate (D2) decides who sees it. This closes review F4: today's fallback bypasses the gate by streaming and persisting raw reasoning as the visible answer.

### D5 — ADR-057 FR-038 carve-out, stated

The D19 gate is user-preference-scoped and deliberately not a provenance filter; it does not resurrect the transcript visibility filter forbidden by **ADR-057: Unify delegate sub-turns onto the own-session execution path** (`ADR-057-session-parent-child-parity.md`), whose subject (parent/child delegation provenance) it does not touch. `pkg/gateway/replay.go::streamReplay`'s contract is otherwise unchanged — the gate parameter added in D2 is a user-preference input, not a provenance filter.

### D6 — Signed-block carrier: provider-neutral field on the shared protocol Message (fixed per F11)

The signed thinking blocks travel on the shared provider protocol type: `ThinkingBlocks []ThinkingBlock` on `pkg/providers/protocoltypes/types.go::Message`, with

```go
type ThinkingBlock struct {
    Type      string // "thinking" | "redacted_thinking" (Anthropic block type, carried verbatim)
    Thinking  string // Type "thinking": the thinking text (raw, for round-trip)
    Signature string // Type "thinking": Anthropic's signature — round-trips byte-exact, untouched
    Data      string // Type "redacted_thinking": the opaque encrypted data — round-trips byte-exact, untouched
}
```

This matches Anthropic's real shapes (Context above): a `thinking` block is `{type, thinking, signature}`; a `redacted_thinking` block is `{type, data}`. Only `Type == "thinking"` blocks with non-empty `Thinking` text feed the redacted display copy; a signature-only block (empty text — e.g. `display: omitted` default on Anthropic's newest models) is **no displayable thinking**: no empty "Thinking…" row, metadata only. Round-trip echoes every block **byte-exact and in order**, thinking blocks preceding text/`tool_use` blocks in an assistant message as Anthropic requires. `openai_compat` never sets `ThinkingBlocks`; its reasoning remains the `ReasoningContent` string (CF1/CF2 path). OpenRouter structured reasoning (`LLMResponse.ReasoningDetails`, which has no signature field) is **out of v1** per spec D21/#943 — `ReasoningDetails` joins D6's precedent list (`SystemParts`, Gemini `ThoughtSignature`) as a typed structured carrier the loop already passes through.

- **Rejected:** an Anthropic-only sidecar — breaks the shared-type adapter convention; every consumer needs type assertions.
- **Rejected:** reusing `ReasoningContent` to smuggle signatures — corrupts the openai-compat field CF7 re-sends.
- **Rejected:** the original `ThinkingBlock{Text, Signature, Redacted bool}` — has no field for `redacted_thinking`'s `data`; cannot round-trip it byte-exact (F11).

### D7 — Request shaping and capture point (corrected per MIN-003; spec D18 applied)

**Signed-block capture is `parseResponse`-only; display text has two sources.** Both streaming and non-streaming paths funnel into `pkg/providers/anthropic/provider.go::parseResponse` (streaming: `msg.Accumulate(event)` → `parseResponse(&msg)`); `anthropic-sdk-go` v1.48.0's `Accumulate` assembles thinking text, signatures and `data` from stream events. Signatures and opaque block data are captured only there — the original separate streaming-side `signature_delta` handling remains dropped. Display text, however, also comes from the accumulated streaming callback for intermediate live frames. The stored display copy comes from the post-strip response parse, and the final live frame carries that stored post-strip text so live and reload reconcile byte-identically. The growth-only progress callbacks (`ToolCallProgress.ReasoningBytes`) stay as internal byte-count telemetry; no reasoning byte-count field is added to any ungated wire frame.

**Spec D18 — the request always asks for the summary, never conditioned on the toggle.** Whenever Omnipus sends a thinking-enabled Anthropic request, the adapter pins `ThinkingConfigAdaptiveParam.Display = "summarized"` regardless of any user's D19 toggle state — the toggle gates *display to users*, never *what is requested*. Per spec D9, when effort is unset Omnipus sends no thinking config at all (the provider's own default applies — on the newest models that default is `display: omitted`, yielding signature-only blocks, which D6 rules "no displayable thinking": no empty rows). The SPA labels Anthropic-originated thinking rows **"summarized thinking"** (D18); the stored `TranscriptEntry` thinking fields carry a provider-summary flag (contract work under spec T2) so the label is data-driven, not hardcoded in the UI.

### D8 — Lifecycle: raw thinking persists in `context.jsonl` (replaces the original strip-on-write design, per spec D15)

1. **What carries raw thinking, per carrier.** openai-compat: the existing `providers.Message.ReasoningContent` (today's path, unchanged — `pkg/providers/common/common.go` re-sends it). Anthropic: the new `Message.ThinkingBlocks` (D6), serialized through the same wholesale marshal — signatures and `redacted_thinking` `data` included, which is the point: they must survive to the next request.
2. **Both write paths carry it, and that is by design.** `pkg/memory/jsonl.go::addMsg` (marshals `ArchivedMessage{Message: msg}`) and `rewriteJSONL` (reached by `SetHistory`, the compaction-path rewrites; `TruncateHistory`/`RollbackAppended` likewise rewrite persisted messages as-is) need **no strip step anywhere**. Review F7 named `rewriteJSONL` as a path strip-on-write missed; with no strip-on-write, F7 is moot by construction — there is no step to forget. F7's `json:"-"` recommendation would violate D15 and is not adopted; `ThinkingBlocks` serializes to disk (unlike `ToolCall.ThoughtSignature`'s `json:"-"`, which stays untouched — Gemini behavior is out of scope).
3. **What differs across a gateway restart: nothing — and that is the answer to spec D15's explicit question.** `context.jsonl` survives a restart; `GetHistory` reads disk fresh on every call; `RecoverOrphanedToolCalls` runs on every session load. Cross-turn re-send therefore continues **across restarts** for both carriers — spec D12's cross-turn clause holds as today's behavior, now including the restart case; spec D13's restart clause ("earlier thinking is NOT re-sent after a restart") no longer describes any real behavior and is **retired**. What remains of D13 is a rule, not a restart fact: **the redacted transcript copy is never mapped into a provider-bound request** (the redacted copy is not byte-exact and must not be substituted). Verified as already true today and locked by design: `pkg/agent/attach_hydrate.go::HydrateAgentHistoryFromTranscript` builds `Message{Role, Content}` from `e.Content` only — no thinking field is read — and `pkg/agent/recall_conversation.go` projects no reasoning field (grep-verified, zero references).
4. **F1 resolved by construction.** Cross-turn re-send does not stop at the next turn (history *is* disk); the mid-turn rebuilds (proactive trim, timeout recovery, context-overflow recovery) re-read disk and **restore** the thinking blocks on the assistant `tool_use` message — Anthropic thinking-enabled tool turns survive trims and recoveries, which was exactly F1's failure scenario B.
5. **Adapter availability guard (F1's mandatory item, adopted).** When a request would carry thinking enabled but the immediately-preceding assistant `tool_use` message carries no thinking blocks — a pre-feature session (spec D17: old files untouched, no migration, no backfill), a cross-provider fallback switch (spec D10), or a hook edit — the Anthropic adapter **omits thinking from that request and logs it**, rather than letting the turn fail on a missing block.
6. **D14 — the debug log stops carrying raw reasoning (its own decision, now implemented).** The one raw site is `pkg/agent/loop_run_turn_response.go`'s `llmResponseFields["reasoning"]` → `logger.DebugCF` (today it logs `response.Reasoning` raw): replaced by a length/boolean (`reasoning_chars`, `thinking_blocks` count). Never reasoning text, never a signature. Applies from the feature's ship date forward (spec D17 — old debug logs age out by rotation, untouched).

**Adjacent consumers made explicit (F12's sweep, all verified this round):** `pkg/agent/msg_normalize.go` preserves `ReasoningContent` through merges — it must preserve `ThinkingBlocks` identically (byte-exact, order-stable); `pkg/agent/context_budget.go` and `pkg/utils/context.go` count `ReasoningContent` — they must count `ThinkingBlocks` (`Thinking`/`Data` lengths) or thinking is under-counted against the budget; the LLM-response process hooks (`pkg/agent/hooks.go::cloneLLMResponse` clones `LLMResponse` wholesale) already receive raw reasoning today and keep doing so — hooks are an operator-trusted extension outside the D19 gate, but a hook edit touching `ThinkingBlocks` invalidates signatures, which D8.5's guard absorbs (thinking omitted, logged) instead of failing the turn.

## Consequences

**Positive**

- F1's two failure modes are gone by construction: disk is the store; every rebuild path restores blocks. Anthropic thinking-enabled tool turns survive trims, recoveries and restarts.
- The per-login gate reuses established per-connection precedents (#264 notifications by `wc.userID`, #283 pairing subscriptions) — hub surgery is contained: a flag on `hubFrameMeta`/`journalEntry`, one content-free substitution in the enqueue loop, the same substitution in `bind`'s tail loop, one injected predicate, identity that already lives on `wsConn`.
- The carrier matches Anthropic's real API shapes (context7 + SDK v1.48.0 verified), so round-trip fidelity is structural, not aspirational.
- Every raw-reasoning consumer is enumerated with its ruling: providers (by design), debug log (lengths only, D14), hooks (operator-trusted, unchanged), transcript (redacted, gated), recall/hydration (Content only, verified).

**Negative**

- Hub-level filtering is real surgery on the #823 chokepoint and narrows the documented "byte-identical to every bound connection" guarantee for thinking frames; the BE-DESIGN.md phrasing is deliberately not edited — this ADR records the narrowing, and the guard tests must cover the journal-tail and enqueue-skip paths, not just REST.
- Raw thinking **and signatures** now persist at rest in `context.jsonl` — founder-ruled (spec D15), with D17 leaving pre-existing files untouched; the disk file is outside every SPA-visible surface, which is what the gate actually promises.
- Whole-string redaction re-scans the accumulated text per delta (bounded by the hold-back tail) — O(n) per delta on the accumulated string; accepted for realistic thinking lengths.
- One more field on the shared protocol `Message` — every adapter sees the type change (compile-time only; `openai_compat` ignores it).
- Each toggle flip persists `config.json` and takes the full config-reload path (F14 — accepted; audit row added).

**Neutral**

- Spec D17: no migration; pre-existing `reasoning_content` in old `context.jsonl` lines still rehydrates for old sessions (forward-only guarantees; "greenfield, no upgrade path", founder 2026-09-15).
- Spec D16: journal catch-up re-delivers previously shown thinking after a toggle OFF flip (D3 above) — accepted.
- Toggle OFF→ON mid-turn: live frames flow from the flip onward; the thinking-so-far already persisted in intermediate transcript entries is reachable immediately via a client refetch (REST gate now on); the in-flight turn's as-yet-unpersisted thinking-so-far is not retro-served live — accepted, and the SPA toggle handler refetches (spec/UI work).
- D11's duration/thinking-token metadata rides the gated thinking row (it renders only for toggle-on users); thinking tokens in aggregate usage totals stay ungated (the cost signal). No `ReasoningBytes` field is added to any ungated wire frame.
- **Acceptance testing is qa-lead's job at the spec/RED stage, not this ADR's** (F15). The spec must carry the proving-test intents the review named: every gated surface with the toggle off (WS live, SSE-nothing, WS attach, REST detail, REST messages, tool-result content); a signature-marker sweep across every file under a test `OMNIPUS_HOME` including logs; blocks surviving a mid-turn rebuild; a secret split across stream pieces; the reasoning-only fallback; hydration never mapping thinking; every `context.jsonl` writer carrying blocks intact.

## Alternatives considered (rejected)

1. **The original construction-time gate** (this ADR's pre-correction D2): streamer never submits thinking frames while one owner toggle is off — exact under a single account, structurally unable to serve per-login visibility; superseded by D19. Recorded because the rejection of hub filtering was sound *given its premise*, and the premise died.
2. **Memory-only side store re-attached at every rebuild** (the grill's option A): impossible — the projection tracks caps/empties, not message bodies (`pkg/memory/projection.go::ProjectionMeta`); "merge back at every rebuild" re-invents disk with worse properties. Founder ruled D15 instead.
3. **Strip-on-write** (the original D8): proven impossible by F1 (disk rebuild every turn would drop blocks on the very next turn and break Anthropic tool turns at mid-turn rebuilds).
4. **`json:"-"` on `ThinkingBlocks`** (F7's recommendation under the strip-on-write premise): would violate D15 — the blocks must persist; moot with no strip step.
5. **Serialize-per-connection at publish** (hub variants): pays per-delta full-frame serialization per connection to deliver what content-free substitution at enqueue provides with one full serialization.
6. **Client-side hiding**: spec D2 forbids it; raw bytes on the wire are the bypass it exists to prevent.
7. **New per-user preference store**: no repo precedent; `UserConfig` already is the per-user state.

## Affected components

| Component | Change | Decision |
|---|---|---|
| `pkg/config` (`config.go::UserConfig`) | `show_thinking` field on every account row; live-read | D1 |
| `pkg/gateway` (`rest_pending_restart.go::RestartGatedKeys`) | `show_thinking` deliberately NOT restart-gated | D1 |
| `contracts/` (spec T2's one contract change) | toggle endpoint; thinking live frame + replay/REST thinking fields (`Message.yaml` — `TranscriptEntry` reaches the wire by json-tag match, so schema and struct land together); no-answer outcome marker; summary flag | D2, D4, D7, T2 |
| `pkg/gateway` (`ws_session_hub.go::publishMeta`, `bind`; `hubFrameMeta`/`journalEntry` thinking flag; injected gate predicate) | per-connection live + journal-tail filtering with content-free skip substitution; projection untouched | D2, spec D29 |
| `pkg/gateway` (`websocket.go::wsConn` — identity already there) | predicate input: `userID` + `isCLIToken` | D2 |
| `pkg/gateway` (`replay.go::streamReplay` + `websocket_replay.go` call site) | requesting-login gate parameter; thinking-row frames gated | D2 |
| `pkg/gateway` (`rest_sessions.go::getSession`, `getSessionMessages`) | both serializers strip thinking unless visible | D2 |
| `pkg/gateway` (`sse.go::sseStreamer`) | must NOT implement the web thinking add-on (no thinking surface) | D2 |
| `pkg/gateway` (`websocket_streamer.go`, `webchat_channel.go`) | implement the web thinking add-on; publish redacted-accumulated frames | D2, D4 |
| `pkg/agent` (turn pipeline / `turnState`) | capture once per round; whole-string redaction with hold-back; D11 metadata; delete the reasoning-only substitution sites | D4 |
| `pkg/agent` (`loop_run_turn_iterations.go`, `session_end.go`) | reasoning-only substitutions deleted (D20 notice path takes over) | D4 |
| `pkg/providers/protocoltypes` (`types.go::Message`) | `ThinkingBlocks` carrier + `ThinkingBlock{Type, Thinking, Signature, Data}` | D6 |
| `pkg/providers/anthropic` (`provider.go::parseResponse`; request build) | block capture; `display: summarized` pin; missing-block availability guard | D7, D8.5 |
| `pkg/memory` (`jsonl.go`) | **no change** — wholesale marshal already carries `ThinkingBlocks` once the field exists | D8 |
| `pkg/agent` (`attach_hydrate.go`, `recall_conversation.go`) | no change — Content-only mapping locked by guard test | D8.3 |
| `pkg/agent` (`msg_normalize.go`, `context_budget.go`, `pkg/utils/context.go`) | preserve/count `ThinkingBlocks` | D8 |
| `pkg/agent` (`loop_run_turn_response.go` debug fields) | raw reasoning value → length/boolean | D8.6 |
| `pkg/providers/openai_compat` | stop dropping streamed reasoning text (D5a/CF1) → `ReasoningContent` | D8 |

## Founder decisions consumed (no open questions)

The grill round's seven founder questions are answered as spec D15–D21 and are fully absorbed above: **D15** → D8 (raw on disk; restart semantics stated); **D16** → D3 (journal edge accepted); **D17** → D8.5/D8.6 and Neutral consequences (no migration, forward-only); **D18** → D7 (summary always requested, toggle-independent, "summarized thinking" label); **D19** → D1/D2 (per-login gate, per-connection mechanism, rowless-logins default adopted); **D20** → D4 (fallback removed, gentle notice + Retry, thinking stays in the gated row); **D21** → D6 (OpenRouter structured reasoning out of v1, #943). The ADR's original Q1–Q3 are superseded (Q1 by D15, Q2 by D16, Q3 by D17); nothing here is left open for plan-spec to decide.

## Status note

Written by the architect 2026-09-27; grilled once (`ADR-095-thinking-visibility-gate-and-signed-blocks-review.md`, verdict BLOCK, findings F1–F15, Q1–Q7); founder answered via team-lead (spec D15–D21); corrected once on 2026-09-27 by the architect — the one correction round, per root `CLAUDE.md` ("Spec-Driven Workflow"). Renumbered ADR-094 → ADR-095 on 2026-09-27 (collision: ADR-094 taken by `feature/gateway-security`'s preview-isolation ADR). This correction re-verified every code claim it relies on first-hand on `feat/thinking-reasoning` @ `a5f02fc345`; its evidence table is in the correction's delivery report to squad-lead. Next step: `plan-spec`.

## Amendment 2026-09-27 (founder D29; spec fix round 2)

Founder D29 corrects D2 Boundary 1's transport detail without changing the per-login server-side gate: every thinking frame hidden from a connection is replaced by the contract-first, content-free `seq_skip {type, session_id, seq}` frame, both live and in reconnect journal-tail replay. The placeholder advances the SPA cursor, has zero thinking bytes, and prevents `chatSeqGapReattach`; a gated stream is contiguous rather than deliberately gapped.

The spec fix also corrects D7's capture-source wording: signed thinking blocks, signatures, and opaque data remain `parseResponse`-only, while the display copy has an accumulated streaming source for intermediate live frames and a post-strip response-parse source for storage. The `final` thinking frame carries the stored post-strip text, reconciling the live row to the byte-identical reload value.
