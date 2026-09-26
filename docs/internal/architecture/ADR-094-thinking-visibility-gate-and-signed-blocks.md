# ADR-094 — Thinking & reasoning: the per-user visibility gate and the signed-thinking-block plumbing

- **Status:** Proposed — written for the thinking & reasoning feature. The founder's product decisions (D1–D14, T1–T3 in `docs/internal/specs/spec-thinking-reasoning.md`) settle *what*; this ADR settles the two structural questions those decisions left open. One `grill-spec` ADR-mode round, one founder interview (via team-lead), one correction — per root `CLAUDE.md` ("Spec-Driven Workflow").
- **Date:** 2026-09-27
- **Deciders:** Daniel Piatkowski (founder — ratifies the two "Questions for the founder" below); architect (technical shape)
- **Evidence baseline:** `feat/thinking-reasoning` @ `a3cdaaaed` (worktree `thinking-reasoning`), read-only. Code facts CF1–CF11 in the spec were verified on `release/v0.1.1` @ `73bdad862`; this worktree is cut from `4d9ecc13c` on the same lineage.
- **Related:** `docs/internal/specs/spec-thinking-reasoning.md` (Decisions Log, source of record for *why*); issues #703, #274, #751, #752, #929, #750; `elicify-ai/omnipus-provider-catalog#29`.

## Context

### The two open structural questions

**D2's gate.** D2 decides the *behavior*: thinking is stored unconditionally; the server withholds it from a user across three surfaces — live streamed frames, replay, REST/API reads — unless that user's toggle is on; toggling on later reveals older sessions. No existing mechanism implements this. Two facts make the mechanism a real design question rather than a mechanical check:

1. **There is no per-user display-preference pattern to reuse.** The only per-user state in the gateway is the `GatewayConfig.Users` row (`pkg/config/config.go::UserConfig` — Username, PasswordHash, TokenHash, Tokens, SessionTokenHash, Name: auth fields only). Comment on `GatewayConfig.Users`: "single-user model: holds at most one entry". So the gate keys on the one authenticated user row; there is no generic preferences store, and building one is not warranted under the single-user model.
2. **The live delivery path cannot filter per connection.** `pkg/gateway/ws_session_hub.go::publishMeta` serializes each frame once, journals those exact seq-stamped bytes, and enqueues the *same* bytes to every bound connection; reconnect catch-up re-delivers journaled bytes verbatim. There is no per-connection frame filtering anywhere in the hub. A "strip thinking at delivery" gate is therefore not implementable at the hub; the gate must sit before serialization.

**D12/D13/D5b — signed blocks.** CF4 is verified first-hand: no Anthropic thinking-signature capture exists anywhere. `pkg/providers/anthropic/provider.go::parseResponse` *flattens* thinking blocks into `LLMResponse.Reasoning` (case `"thinking"`: `reasoning.WriteString(tb.Thinking)`), the streaming switch counts bytes only (`case "thinking"` / `case "redacted_thinking"`: `reasoning += len(block.Thinking)`), and the shared provider `Message` type (`pkg/providers/protocoltypes/types.go::Message`) carries `ReasoningContent string` only — no signature, no block list. There is one in-repo precedent for a typed signature carrier: Gemini's `ThoughtSignature` on `ToolCall`/`FunctionCall` (`protocoltypes/types.go::ToolCall.ThoughtSignature`), parsed from Google's `extra_content` (`pkg/providers/common/common.go`).

### A persistence fact the decisions were not made against

`context.jsonl` — the LLM-context store — **carries `reasoning_content` today when populated**: `pkg/memory/jsonl.go::addMsg` marshals `ArchivedMessage{Message: msg}` wholesale (embedding `providers.Message`, `reasoning_content,omitempty` included), and the loop's tool-call rounds store `ReasoningContent` on the assistant history message (`pkg/agent/loop_run_turn_response.go::recordToolCalls`). So a restart-rebuild (`pkg/agent/session_recovery.go::RecoverOrphanedToolCalls` → `store.GetHistory`) re-sends reasoning today *across* restarts. D13 says the opposite: after a restart, earlier thinking is **not** re-sent. D12's cross-turn half ("keep today's existing behavior" for `reasoning_content` re-send) and D13 together imply: cross-turn re-send within process lifetime, never across a restart — which today's persistence does not deliver. The spec's CF6 ("Reasoning is not persisted anywhere") is imprecise on this point.

### ADR-057 FR-038, stated precisely

`pkg/gateway/replay.go`'s contract comment (ADR-057, "A delegated child owns its own session") forbids "a transcript visibility filter at any read boundary" — a prohibition whose subject is **parent/child delegation provenance** (after FR-034/FR-038, child narration never lands in the parent's transcript, so there is nothing to filter). D2's gate is **user-preference-scoped**, a different subject; it does not resurrect the FR-038-forbidden filter. Stating this here so no reviewer reads a contradiction where there is none.

## Decisions

### D1 — Toggle home: `config.UserConfig`

The per-user "show thinking" toggle is `ShowThinking bool` (wire key `show_thinking`) on `pkg/config/config.go::UserConfig`. Live-read at every read boundary — deliberately **not** added to `RestartGatedKeys` (`pkg/config/keys.go`; ADR-044's restart-gated vs live-read classification): a display preference that needed a restart would contradict D2's purpose. Runtime mutation follows the existing Users-row mutation precedent (login/logout already mutate `Users` rows and persist `config.json`). The REST endpoint that mutates it is contract work under T2.

- **Rejected:** a new generic per-user preference store — no precedent in the repo, and the single-user model gives it nothing to serve.
- **Rejected:** a global (not per-user) setting — contradicts D2's per-user wording.

### D2 — Gate placement: one predicate at the three read/serialization boundaries

The D2 gate is a single predicate over the authenticated user's toggle, applied at each point where thinking is serialized toward a client — not inside the hub, not in the client:

1. **Live:** gate at frame *construction*. While the toggle is off, the turn's streamer/turn code does not build or submit thinking frames to the session hub at all. The streamer still accumulates thinking for the redacted persist — persist-always, live-send only when the toggle is on. This is the same shape `wsStreamer` already uses for stream-ownership withholding: "still accumulates every token into its own private wsStreamer.accumulated (so its Finalize-written transcript entry stays fully correct) but withholds the live TokenFrame send" (`pkg/gateway/websocket.go::streamOwners` doc, `pkg/gateway/websocket_streamer.go::Update`).
2. **Replay:** `pkg/gateway/replay.go::streamReplay` withholds/omits thinking-bearing frames unless the toggle is on. Storage is unconditional (D2), so toggling on later reveals older sessions exactly as D2 requires.
3. **REST:** the sessions message-read handler (`pkg/gateway/rest_sessions.go::HandleSessions` GET messages — serves `[]session.TranscriptEntry` directly) strips thinking fields from served rows unless the toggle is on.

- **Rejected:** per-connection filtering inside `sessionHub.publishMeta` — the hub enqueues identical bytes to every bound connection and journals those bytes; filtering there means hub surgery (parse-and-rewrite per connection, journal/projection implications) for a user model ("Users holds at most one entry") that does not exist.
- **Rejected:** client-side hiding — D2 forbids it outright ("no client can bypass the gate by reading raw frames or replay data").
- **Multi-user note:** the construction-time gate resolves the toggle from the turn's owning user row. Under today's single-user model this is exact. If the gateway ever becomes multi-user with per-user toggles viewing one shared session, the construction-time gate is insufficient and hub-level per-connection filtering must be revisited — a tracked consequence, not built now.

### D3 — Journal catch-up edge: accepted, documented

Toggle ON→OFF mid-session leaves thinking frames in the hub journal from the ON period; reconnect catch-up re-delivers those bytes verbatim (`publishMeta` → `appendJournalLocked` → catch-up replay). **Accepted**: those bytes were legitimately delivered to the same user while the toggle was on. The gate's promise is against *bypass* (never-delivered data reaching a client), not re-delivery of previously delivered data. Bounded by journal retention (`hubJournalMaxFrames`/`hubJournalMaxBytes`, idle sweep). No per-frame journal filtering — that would put parse-and-rewrite into the catch-up path for no bypass risk removed.

### D4 — Redaction point: once, at capture, on a copy

D3 ("live view and a reloaded/replayed view render identically — the redacted, stored copy is what both paths read") becomes structural only if redaction happens once, at capture: the streamer redacts a **copy** for display + persist; the raw text stays in the loop's in-memory path for the provider round-trip (D12). Rejected alternatives: redact-at-persist with live sending raw (live ≠ replay until refresh — the exact see-then-refresh discrepancy D3 exists to prevent); redact-at-read (redaction cost per read, and a raw copy at rest on disk, against D3's own wording).

### D5 — ADR-057 FR-038 carve-out, stated

D2's gate is user-preference-scoped and deliberately not a provenance filter; it does not resurrect the ADR-057 FR-038-forbidden transcript visibility filter, whose subject (parent/child delegation provenance) it does not touch. `pkg/gateway/replay.go`'s streamReplay contract is otherwise unchanged.

### D6 — Signed-block carrier: provider-neutral field on the shared protocol Message

The signed thinking block travels on the shared provider protocol type: `ThinkingBlocks []ThinkingBlock` on `pkg/providers/protocoltypes/types.go::Message`, with `ThinkingBlock{Text, Signature string, Redacted bool}`. Precedents for structured carriers on the shared types: `SystemParts []ContentBlock` (per-block cache_control) and Gemini's `ThoughtSignature` on `ToolCall`/`FunctionCall` (`protocoltypes/types.go::ToolCall.ThoughtSignature`). The `pkg/providers/anthropic` adapter maps `ThinkingBlocks` ↔ `anthropic.ThinkingBlockParam` / `RedactedThinkingParam` on build/parse — the SDK shapes the wire side (CF4/D5b). `openai_compat` never sets `ThinkingBlocks`; its reasoning remains the `ReasoningContent` string (CF1/CF2 path).

- **Rejected:** an Anthropic-only sidecar (e.g. a keyed map out-of-band) — breaks the shared-type adapter convention; every consumer would need type assertions against the adapter type.
- **Rejected:** reusing `ReasoningContent` to smuggle the signature — would corrupt the existing openai-compat field's meaning (CF7 re-send reads it).

### D7 — Capture points: one capture at the adapter, two consumers

Capture happens once, at the `pkg/providers/anthropic` adapter: streaming handles `signature_delta` alongside `thinking_delta` (today bytes are counted only), and non-streaming `parseResponse` captures `block.Signature` instead of flattening. Two consumers derive from the single capture: (a) the display/persist path gets the text for the redacted copy (D4); (b) the round-trip path keeps the raw block in the loop's in-memory message list (D12). **The signature never reaches a disk write or the gateway/SPA wire.** The openai-compat streamed reasoning text (D5a, CF1) is display/persist-only — no round-trip consumer — and is never written raw to any disk file; the raw text lives only in the in-memory store projection, which is what serves CF7's cross-turn `reasoning_content` re-send within process lifetime (D8).

### D8 — Lifecycle: raw reasoning is memory-only in the LLM-context path

1. **In-turn:** raw blocks round-trip byte-exact via the in-memory message list (D12's in-turn half).
2. **context.jsonl never carries raw reasoning text going forward.** This is a deliberate change to today's behavior (today `addMsg` marshals `providers.Message` wholesale and `recordToolCalls` populates `ReasoningContent` → restart re-sends reasoning, which D13 forbids). The store write path strips reasoning text fields (`Message.ReasoningContent`, `Message.ThinkingBlocks`) on write. Cross-turn re-send (D12's CF7 half) therefore works in-memory until restart; after a restart, `GetHistory` rebuilds without reasoning → D13 holds **by construction**. `ToolCall.ThoughtSignature` (Gemini) is untouched — existing behavior, not this feature's target.
3. **transcript.jsonl** stores only the redacted copy (D3); never the signature.
4. **D13's implementation meaning:** the transcript's redacted copy is never mapped back into provider-bound history — `GetHistory` after restart yields no thinking, so nothing is re-sent; the redacted copy never feeds a provider request.

This decision needs founder ratification — see Q1 below.

## Consequences

**Positive**

- `plan-spec` writes the spec without inventing gate/carrier shapes; the grill has one place to check them.
- Reuses established patterns: wsStreamer's accumulate-always/conditional-send; Users-row mutation for a per-user setting; `SystemParts`/`ThoughtSignature` typed-field precedent; CF10's audit redaction patterns.
- D13 and D14 hold by construction (strip-on-write; signature never at rest).
- ADR-057 FR-038 preserved for its actual subject (delegation provenance).

**Negative**

- The D2 live gate is construction-time, keyed on the turn's owning user row — exact under the single-user model; a future multi-user model with differing per-user toggles on one shared session needs hub-level per-connection filtering (tracked consequence, revisit then).
- Journal catch-up re-delivers previously delivered thinking frames after a toggle ON→OFF flip (D3 above) — accepted.
- Strip-on-write is a behavior change to today's store path: cross-turn re-send now ends at a restart, deliberately (D13).
- One more field on the shared protocol `Message` — every adapter sees the type change (compile-time only; `openai_compat` ignores it).

**Neutral**

- Pre-change `context.jsonl` lines that already carry `reasoning_content` still rehydrate with reasoning for old sessions — greenfield ruling ("no migrations, no upgrade backfills", founder 2026-09-15): forward behavior only, no backfill, no new raw writes.
- D11's duration/thinking-token data rides the `TranscriptEntry` extension; not gated beyond D2.
- Spec CF6's wording ("not persisted anywhere") is imprecise — flagged to squad-lead for a spec correction note.

## Alternatives considered (rejected)

1. **Per-connection hub filtering** (gate at `sessionHub.publishMeta`): parse-and-rewrite per connection plus journal/projection surgery, for a multi-user model that does not exist. D2's promise is deliverable without touching the hub.
2. **New per-user preference store:** no repo precedent; `UserConfig` already is the per-user state; a new store adds a second home for identity-adjacent state.
3. **Persist raw reasoning in context.jsonl, strip at rebuild time:** keeps raw reasoning at rest beyond what D3 contemplates (against its "redacted, stored copy" wording and D14's spirit), and adds rebuild-time plumbing that strip-on-write makes unnecessary — same visible outcome, worse properties.
4. **Anthropic-only sidecar for signed blocks:** breaks the shared-protocol-type adapter convention (`SystemParts`, `ThoughtSignature` precedents) and forces type assertions in the loop.
5. **Redact at persist only (live sends raw):** live ≠ replay until refresh — the exact see-then-refresh discrepancy D3 forbids.

## Affected components

| Component | Change | Decision |
|---|---|---|
| `pkg/config` (`config.go::UserConfig`, `keys.go`) | `show_thinking` field; live-read classification | D1 |
| `contracts/` (per T2's one contract change) | toggle endpoint, reasoning frames, effort surfaces | T2 + D2 |
| `pkg/gateway` (`websocket_streamer.go::Update`, `replay.go::streamReplay`, `rest_sessions.go::HandleSessions`) | gate predicate at three boundaries | D2, D4 |
| `pkg/providers/protocoltypes` (`types.go::Message`) | `ThinkingBlocks` carrier field | D6 |
| `pkg/providers/anthropic` (`provider.go::parseResponse` + streaming) | signature capture, block round-trip | D7 |
| `pkg/memory` (`jsonl.go::addMsg`) | strip-on-write for reasoning text fields | D8 |
| `pkg/agent` (loop in-memory message list) | raw blocks round-trip; no new persistence | D8 |

## Questions for the founder (grill interview)

- **Q1 (D8's strip-on-write is a deliberate change to today's persistence behavior).** Today `context.jsonl` carries raw `reasoning_content` when populated, and a restart re-sends it. D8 stops those writes so D13 ("after a restart, earlier thinking is not re-sent") holds by construction. **Recommendation: approve** — the alternative (persist raw + strip at rebuild) keeps raw reasoning at rest and adds plumbing for the same outcome. Confirm, or rule restart re-send acceptable (which would amend D13).
- **Q2 (journal catch-up edge).** After a toggle ON→OFF flip, reconnect catch-up can re-deliver thinking frames published while the toggle was on. **Recommendation: accept** — same user, previously legitimately delivered, bounded by journal retention. Confirm, or require catch-up-time filtering (cost: parse-and-rewrite in the catch-up path).

## Status note

Written by the architect 2026-09-27 from the spec's Decisions Log and first-hand code reads on `feat/thinking-reasoning` @ `a3cdaaaed`. Awaiting the one `grill-spec` ADR-mode round, the founder interview on Q1/Q2, and the one correction round.
