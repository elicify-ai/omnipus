# ADR-095 review — grill-spec (ADR mode), round 1 of 1

- **Reviewed:** `docs/internal/architecture/ADR-095-thinking-visibility-gate-and-signed-blocks.md` (renumbered from ADR-094 at `e52a13227`; includes Q3)
- **Branch / HEAD:** `feat/thinking-reasoning` @ `e52a13227`, read-only
- **Reviewers:** architect, an independent instance (not the author), plus the `grill-spec` skill's own forked read-only agent. Their findings are merged below. Each merged finding was re-checked first-hand by the architect (see the evidence table); IDs in brackets (CRIT-/MAJ-/MIN-/OBS-) are the fork's.
- **Date:** 2026-09-27
- **Verdict:** **BLOCK**. Both passes independently reached it. D8, one of the ADR's two core decisions, cannot work against the code as written. The ADR can be fixed in its one correction round, but the fix reopens Q1, so it needs a founder decision first.

## 1. Structural checklist

| Check | Result | Note |
|---|---|---|
| Status / Date / Deciders header | PASS, with a defect | The Deciders line says "the two Questions for the founder"; there are now three [MIN-002]. |
| Context / Decision / Consequences | PASS | Alternatives and affected components are present. |
| Existing ADRs cited by title | **FAIL (Low)** | "ADR-044's restart-gated vs live-read classification" is cited by number alone, and three files share that number (`ADR-044-live-browser-video-streaming.md`, `ADR-044-preview-on-main-listener.md`, `ADR-044-spike-results.md`). ADR-057 is quoted as "A delegated child owns its own session"; its heading is "Unify delegate sub-turns onto the own-session execution path". |
| Says whether it opens or closes a question | PARTIAL | It closes the gate and carrier questions, but leaves open the transcript storage shape for thinking, which the spec's process note (section 4) assigned to this ADR (F9). |

## 2. Findings

Severity: Critical / High / Medium / Low. Certainty: Verified (read in this task) / Inferred (reasoned; the reason is given).

### F1 — Critical — D8's "memory-only, re-sent across turns until restart" design cannot work: history is rebuilt from disk every turn and mid-turn [CRIT-001, CRIT-002]

- **Failure scenario A (across turns):** turn N+1 starts. `assembleInitialContext` → `RecoverOrphanedToolCalls` → `UnifiedStore.GetHistory` → `JSONLStore.GetHistory` reads `context.jsonl` from disk; there is no cache.
  - Under strip-on-write, the stripped lines carry no `reasoning_content`.
  - So CF7's cross-turn re-send stops **on the very next turn**, not at restart.
  - That contradicts D12 ("keep today's existing behavior"), the ADR's Negative consequence, and Q1's premise.
- **Failure scenario B (within a turn, Anthropic):** the three mid-turn recovery rebuilds replace the turn's message list with a fresh disk read, then `assembleMessages`:
  - Site-2: proactive trim
  - Site-3: timeout recovery
  - Site-4: context-overflow recovery

  The stripped assistant `tool_use` message loses its thinking block. Anthropic requires the block to "accompany the tool_use block", so the turn fails. This hits exactly the long, tool-heavy turns where trims fire. A mid-turn fallback onto an Anthropic model (spec D10 chain) is exposed the same way.
- **The carrier named in D7 does not exist.** D7 says the raw text lives "in the in-memory store projection". `pkg/memory/projection.go::ProjectionMeta` is `{Entries ProjectionSet, Hydrated bool}`: it tracks which tool results were capped or emptied, and holds no message bodies. The Context section also mislabels the every-turn `RecoverOrphanedToolCalls` call as a "restart-rebuild".
- **Evidence (Verified):**
  - `pkg/agent/loop_run_turn.go::assembleInitialContext`
  - `pkg/agent/session_recovery.go::RecoverOrphanedToolCalls` (doc: "Safe to call on every session load")
  - `pkg/memory/jsonl.go::GetHistory` (`readMessages(s.jsonlPath(...))` on each call)
  - Sites 2–4 in `pkg/agent/loop_run_turn.go` and `pkg/agent/loop_run_turn_response.go`
  - Anthropic docs via context7 (`/llmstxt/platform_claude_llms_txt`, "Preserving thinking blocks")
- **Recommendation:** D8 must be redesigned before the spec. Three shapes, put to the founder as Q1 below:
  - **(A) Memory-only sidecar.** A per-session store, owned by the agent, that is merged back at every `GetHistory` → `assembleMessages` site, keyed to archive lines and cleared on truncate, compact and delete. Add a guard test that fails when a new rebuild site bypasses it.
  - **(B) Persist raw, strip after restart** (the ADR's rejected Alternative 3).
  - **(C) Amend D12: thinking round-trips only within a turn.** After Sites 2–4, re-append the current turn's blocks, following the ADR-087 D6.7 `continuationChain` re-append precedent. Nothing is re-sent across turns.

  The fork recommends C. Its argument: CF1 means streamed openai-compat reasoning never filled `ReasoningContent`, so today's cross-turn re-send only happens on non-streaming paths. See the Q1 note on the one open risk.
- **Mandatory in every option:** the Anthropic adapter should switch thinking off for a request (and log it) whose last assistant `tool_use` message carries no blocks, rather than letting the turn fail.

### F2 — High — the live gate misses live delivery paths and puts persistence in the wrong layer [MAJ-001, MAJ-002]

- **Failure scenario 1 (another live path).** The live gate is placed in `wsStreamer.Update`, as the Affected-components table says. But `POST /api/v1/chat` (SSE, still registered) delivers through `sseStreamer`, which has no gate.
- **Failure scenario 2 (thinking reaches messaging channels).** If thinking is added to the shared `bus.Streamer` interface itself, it reaches every streamer: the channel manager, Telegram and WeCom. That contradicts spec D1 (web chat only).
- **Failure scenario 3 (thinking not stored).** D4 puts capture and redaction in "the streamer". Many turns have no web streamer: channel inbound, cron, heartbeats, task runs, steered sessions, non-streaming providers. Transcript entries are also written by the agent itself (intermediate and final assistant entries), not only by `wsStreamer.Finalize`. Those turns either store no thinking, which breaks D2 "stored unconditionally", or someone later adds an unredacted write, which breaks D3.
- **Evidence (Verified):**
  - `pkg/gateway/gateway_boot.go` (`/api/v1/chat` → `newSSEHandler`); `pkg/gateway/sse.go::GetStreamer`
  - `Finalize(ctx…)` implementations in `pkg/gateway/sse.go`, `pkg/gateway/websocket_streamer.go`, `pkg/channels/manager.go`, `pkg/channels/telegram/telegram.go`, `pkg/channels/wecom/wecom.go`
  - `pkg/agent/turn_transcript.go` (four writers, including `appendIntermediateAssistantTranscript` and `appendAssistantTranscript`)
- **Recommendation:**
  - Capture, redact and persist once, in the turn itself (`turnState`).
  - Deliver thinking to a streamer only through an optional add-on interface that only the web streamers (WS and SSE) implement.
  - The gate predicate decides whether thinking is sent live, nothing else.

### F3 — High — redacting each streamed piece separately lets a split secret through, and the live view no longer matches replay [MAJ-004]

- **Failure scenario:** a credential in the thinking text arrives split across two streamed pieces. The redaction pattern matches neither piece, so the raw secret is sent live. The saved, accumulated copy is redacted, so live ≠ replay. That breaks D3's identical-render rule and bypasses redaction on the wire.
- **Evidence:**
  - Verified: `pkg/audit/redactor.go::RedactCredentials` and `Redactor.Redact` take one whole string and apply regex patterns.
  - Inferred (high confidence): the split-token miss follows from applying whole-string patterns to each piece; not run.
- **Recommendation:** live frames carry the redacted accumulated text, either as full snapshots or as an append stream that holds back a tail until it can no longer be part of a match. Never send raw pieces.

### F4 — High — an existing fallback bypasses the gate: a reasoning-only reply becomes the visible answer [MAJ-005]

- **Failure scenario:** a model returns reasoning and empty `Content`. The loop substitutes `ReasoningContent` as the reply text. That text is streamed and saved as a normal assistant message: unredacted, and visible with the toggle off.
- **Evidence (Verified):** `pkg/agent/loop_run_turn_iterations.go`, both sites (`er.cn.responseContent = …ReasoningContent` and `cn.responseContent = …ReasoningContent`). A related site: `pkg/agent/session_end.go` substitutes `resp.Reasoning` when the response text is empty.
- **Recommendation:** founder decision (Q6).

### F5 — High — the gate's identity is undefined for most turns and several kinds of login [MAJ-003]

- **Failure scenarios:**
  - `turnState.userID` is empty for any turn not started from the web chat.
  - Three logins have no config row whose toggle could be stored or read: the dev-bypass user (`devBypassUser`), the environment-token user (`envTokenUser`) and the CLI token (a synthetic `Username: "cli"`). End-to-end and user-acceptance runs may use dev bypass.
  - Leftover extra `Users` rows are kept on older installs (the `matchUserBearer` doc says so), so "at most one entry" is a comment, not a guarantee.
  - D2's live gate keys on "the turn's owning user row" while the REST gate uses the requesting principal. Those can differ, and the session hub fans the same bytes out to every connection bound to the session.
- **Evidence (Verified):**
  - `pkg/agent/turn.go` (the `userID` field)
  - `pkg/gateway/auth.go` (`devBypassUser`, `envTokenUser`, the synthetic `cli` user, the `matchUserBearer` doc)
- **Recommendation:**
  - One rule everywhere: the owner account's toggle decides, following the `Users[0]` / `ownerUsername` precedent in `gateway_boot.go` and `schedules.go`.
  - Zero rows, a synthetic identity or a config read error means off (fail closed).
  - State what the toggle endpoint returns to a synthetic identity (Q5).

### F6 — High — Anthropic's `display` parameter is undecided

- **Failure scenario:**
  - The newest models (Fable 5, Mythos 5) default to `display: "omitted"`: `thinking: ""` plus a signature. Users with the toggle on would see empty "Thinking…" rows.
  - `"summarized"` returns a summary, not the model's raw thinking.
  - Tying `display` to the toggle breaks D2's "turning it on reveals older sessions".
- **Evidence (Verified, docs):** context7 Anthropic docs (`thinking_config_enabled.display`; release notes of 2026-03-16 and 2026-06-09).
- **Recommendation:** founder decision (Q4). Treat a signature-only block as "no displayable thinking": no empty row.

### F7 — High — strip-on-write names one write path; signatures can reach disk through the others [MAJ-009]

- **Failure scenario:** `context.jsonl` is also rewritten wholesale by `rewriteJSONL`, which marshals each message. That rewrite is reached through compaction and `SetHistory` (used by hydration), and `TruncateHistory` and `RollbackAppended` also write the file. A writer that forgets the strip puts `ThinkingBlocks` and their signatures on disk, breaking D7's "never reaches a disk write".
- **Evidence (Verified):** `pkg/memory/jsonl.go::addMsg` (`json.Marshal(archived)`) and `pkg/memory/jsonl.go::rewriteJSONL` (`json.Marshal(msg)` → `WriteFileAtomic`).
- **Recommendation:**
  - Tag `ThinkingBlocks` `json:"-"`, following the `ToolCall.ThoughtSignature` "Internal use only" precedent in `protocoltypes/types.go`, so it can never be serialized.
  - Put any `ReasoningContent` strip in one shared encode step that every writer uses.

### F8 — High — the read-side gate names one handler of two; agent-side readers are safe only by accident [MAJ-007, MIN-007]

- **Failure scenario:** `GET /api/v1/sessions/{id}` (`getSession` → `jsonSessionDetail`) also serializes `[]session.TranscriptEntry`, and D2 names only "GET messages". Agent-side readers use only `Content` today, so they are safe by default, but nothing locks that in. A later change that projects the thinking field leaks it into tool rows or into model history (breaking D13). Those readers are `inspect_session`, `delegate_status`, `handoff`, recap, and `HydrateAgentHistoryFromTranscript`, the one path that copies the transcript back into model history.
- **Evidence (Verified):**
  - `pkg/gateway/rest_sessions.go::getSession` / `jsonSessionDetail` / `getSessionMessages`
  - `pkg/tools/inspect_session.go`, `pkg/tools/delegate_status.go`, `pkg/agent/attach_hydrate.go`
  - The WebSocket attach snapshot IS covered: `pkg/gateway/websocket_replay.go` calls `streamReplay`.
- **Recommendation:** name both handlers. Better, make it fail-closed: the thinking field loads only through an explicit opt-in transcript read used by the gated boundaries. D8.4 names `HydrateAgentHistoryFromTranscript` as must-not-map, with a guard test.

### F9 — Medium — the transcript storage shape for thinking is undecided

- **Failure scenario:** the spec's section 4 routed "the transcript storage shape for thinking (D3)" to this ADR. The shape decides whether REST "strips fields" or replay "omits frames", and whether D11's duration and token counts are gated. plan-spec would have to invent it.
- **Recommendation:** decide it:
  - Either a field on assistant `TranscriptEntry` rows, or a new entry type.
  - Say whether duration and token count are shown with the toggle off [OBS-001].
  - Say whether `ReasoningBytes` in progress frames is acceptable metadata [OBS-002].

### F10 — Medium — OpenRouter's signed reasoning is out of view [MAJ-008]

- **Failure scenario:** OpenRouter is the user-acceptance provider. Anthropic and Gemini models reached through it return structured `reasoning_details`. D6 says openai-compat never sets `ThinkingBlocks`, yet D12's rationale covers "providers with similar signed-thinking-block requirements". If OpenRouter needs those details intact across tool loops, thinking-enabled tool turns through OpenRouter break.
- **Evidence:**
  - Verified: `protocoltypes/types.go::LLMResponse.ReasoningDetails` and `ReasoningDetail{Format, Index, Type, Text}`, which has no signature or data field.
  - Inferred (medium): OpenRouter's requirement to keep reasoning details intact; not checked against its docs in this task.
- **Recommendation:** founder decision (Q7). List `ReasoningDetails` among D6's precedents.

### F11 — Medium — D6's `ThinkingBlock` shape does not fit Anthropic's blocks [MIN-004]

- **Failure scenario:** `redacted_thinking` carries an opaque `data` field, and the proposed struct has no field for it. Omitted-display blocks need a rule too. Block order on the build side (thinking before text and `tool_use`) is also unstated.
- **Evidence (Verified):** `pkg/providers/anthropic/provider.go` streaming switch (`case "redacted_thinking": … block.Data`).
- **Recommendation:** use `{Type, Thinking, Signature, Data}` (names indicative). Only `Type=thinking` with non-empty text feeds the display copy; round-trip echoes every block byte-exact and in order.

### F12 — Medium — D14 is claimed "by construction" but has no decision; there are other raw-reasoning consumers [MAJ-006, MIN-005]

- **Failure scenario 1 (debug log).** Nothing in D1–D8 touches the debug log, which still records `response.Reasoning`.
- **Failure scenario 2 (process hooks).** Before- and after-LLM process hooks (external programs) would receive raw reasoning and signatures, and a hook's edit invalidates the signature.
- **Failure scenario 3 (context budget).** `context_budget.go` and `utils/context.go` count only `ReasoningContent`, so `ThinkingBlocks` would be under-counted.
- **Failure scenario 4 (message merging).** `msg_normalize.go` merges messages without knowing about the new field.
- **Evidence:**
  - Verified: `pkg/agent/loop_run_turn_response.go` (`llmResponseFields["reasoning"]` → `logger.DebugCF`); `ReasoningContent` counting in `pkg/agent/context_budget.go` and `pkg/utils/context.go`
  - Verified by the fork only: the hook paths (`hooks.go::cloneLLMResponse`, `hook_process.go`); exposure Inferred
- **Recommendation:** add a D14 decision (log lengths and booleans only, never text or signature) and Affected-components rows for hooks, budget and normalize.

### F13 — Medium — Q3 is sound but misstated [MIN-006]

- **Assessment:** no migration fits the greenfield ruling. Old `reasoning_content` in `context.jsonl` is not a gate bypass, because the file is not served to the SPA and `pkg/agent/recall_conversation.go` projects no reasoning field.
- **Problems with Q3 as written:**
  - It paraphrases D12 as "raw never written to disk", but D12 says "never written to disk *mid-turn*".
  - It ties D14 (the debug log) to `context.jsonl`.
  - It omits that D13 is also forward-only for older sessions.
  - It offers a false choice: a read-side strip, or F7's rewrite paths, removes old lines' effect without a migration.

### F14 — Low — smaller corrections

| Item | Evidence |
|---|---|
| `RestartGatedKeys` lives in `pkg/gateway/rest_pending_restart.go`, not `pkg/config/keys.go` as D1 and Affected components say [MIN-001] | Verified |
| D7's streaming `signature_delta` handling is unneeded work: the stream path calls `msg.Accumulate` (SDK v1.48.0 accumulates signatures), then `parseResponse`, so one capture in `parseResponse` suffices [MIN-003] | Verified (`msg.Accumulate`, `go.mod` v1.48.0); SDK internals per fork |
| Contract detail: new thinking fields must be added to `Message.yaml` together with the Go struct, because `TranscriptEntry` reaches the wire by json-tag match | Verified (`rest_sessions.go::jsonSessionDetail` doc) |
| The toggle needs a dedicated endpoint (`gateway.users` is blocked from generic config writes and from the agent's config tool) that never serializes `UserConfig`'s hashes | Verified (`pkg/gateway/blocked_paths.go`) |
| Undefined edge cases: toggle off→on mid-turn (the row starts mid-thought); toggle changes are not audited; each toggle triggers a full config reload [MIN-008] | Fork-verified reload path; others Inferred |
| Q2's journal edge is narrower than stated: fresh tabs use the gated transcript snapshot, and only seq-cursor reconnects replay raw journal frames [OBS-003] | Fork-verified; consistent with `websocket_replay.go` → `streamReplay` |

### F15 — Medium — no test plan [MAJ-010]

- **Failure scenario:** tests cover the three named boundaries and pass, while SSE, `getSession`, split-secret redaction or the reasoning-only fallback leak. That is a false green.
- **Recommendation:** the ADR names these proving tests:
  - (a) Every user-facing surface with the toggle off: WS live, SSE, WS attach, REST detail, REST messages, tool-result content.
  - (b) A known signature marker scanned across every file under a test `OMNIPUS_HOME`, including logs.
  - (c) Blocks survive a Site-2/3/4 rebuild.
  - (d) A secret split across stream pieces.
  - (e) The reasoning-only fallback.
  - (f) Hydration never maps thinking.
  - (g) Every `context.jsonl` writer strips.

## 3. Lens sweep (all twelve)

| Lens | Result |
|---|---|
| Ambiguity | F5 (whose toggle), F9 (storage shape), F11 (block fields), F2 ("streamer/turn code"). |
| Incompleteness | F2, F4, F6, F8, F9, F10, F12, F15. |
| Inconsistency with ADRs / AS-IS | F1 (vs the ADR's own Consequences and Q1); ADR citation defects (section 1). D5's reading of ADR-057 FR-038 is correct: `pkg/gateway/replay.go::streamReplay` contract comment, delegation-provenance context. |
| Infeasibility | F1. |
| Contract-first gaps | F14 (`Message.yaml`, dedicated endpoint), F9. |
| Security | F2, F3, F4, F5, F7, F8, F12 (disclosure); the toggle endpoint lacks an audit trail (F14); F1-B (availability: the turn fails). Error-path text via `pkg/agent/translate_error.go`: Inferred low risk, not verified against real Anthropic error bodies. |
| Reachability | The toggle endpoint and UI are deferred to T2/spec. Acceptable for an ADR, but the spec must name the screen and route, and F5's synthetic identities need a defined response. |
| UI states / journey | No UI is decided here. The spec must cover: toggle off→on mid-turn; signature-only blocks (F6); the reasoning-only reply (F4, Q6). |
| Accessibility / keyboard | N/A: no UI decided. The spec inherits the tool-row collapse pattern (spec D4) and must confirm its keyboard and screen-reader behaviour. |
| Design-system reuse / brand | N/A here; the spec reuses the tool-row component. |
| Testability / false green | F15. |
| Overcomplexity | D1 and D3 are proportionate. Rejecting hub-level filtering is sound: `pkg/gateway/ws_session_hub.go::publishMeta` journals and fans out one byte slice. D7's streaming `signature_delta` work is unnecessary (F14). F1's fix adds at most one component. |

## 4. Assessment of the ADR's own founder questions

| ADR question | Verdict |
|---|---|
| Q1 (strip-on-write) | **Built on a false premise (F1): must be replaced.** Superseded by Q1 below. |
| Q2 (journal edge) | **Sound; keep,** with OBS-003's narrower scope. Verified: `publishMeta` → `appendJournalLocked`, bounded by `hubJournalMaxFrames = 2048` and `hubJournalMaxBytes = 1 MiB`. |
| Q3 (pre-existing raw reasoning) | **The recommendation holds; the wording must be fixed (F13).** |
| Missing | The display parameter (F6), identity (F5), the reasoning-only fallback (F4), OpenRouter scope (F10). Added below as Q4–Q7. |

## 5. Questions for the founder

Merges and supersedes ADR-095's Q1–Q3. Answer in one line, e.g. "Q1 A, Q2 A, Q3 A, Q4 A, Q5 A, Q6 A, Q7 A".

- **Q1 — How should Omnipus keep a model's raw thinking between steps, given that the chat history is re-read from disk at every step?** (supersedes ADR Q1)
  **Context:** the ADR assumed raw thinking could live "in memory until a restart". In the code, the history the model sees is rebuilt from the saved file at the start of every turn, and again mid-turn whenever long conversations are trimmed. So "don't save it" currently means "lose it on the next turn". It also breaks long tool-using turns on Anthropic models, which reject a tool step whose thinking was dropped.
  **Options:**
  - **A — Memory-only side store, re-attached at every rebuild (recommended by the architect).** Nothing raw on disk; re-sent across turns until a restart, never after. Keeps today's behaviour (D12) exactly. One new component.
  - **B — Save raw thinking in the model-context file; drop it only when rebuilding after a restart.** Simpler; raw thinking sits unredacted on disk.
  - **C — Keep thinking only within a single turn; stop re-sending it across turns (recommended by the grill agent).** Smallest change: today's cross-turn re-send already happens only on non-streaming paths. It amends your D12.

  **Open risk for C, and for D13 generally:** Anthropic's 2026-06-09 note says the newest models' thinking blocks "must be passed back unchanged in multi-turn conversations". Whether those models reject a conversation whose earlier turns lack their blocks is **unknown**. D13 already drops them after a restart, so a live probe is needed before the spec either way.

- **Q2 — Accept that reconnecting can re-show thinking already shown before the toggle was switched off?** (ADR Q2)
  **Context:** after switching thinking on, then off, a reconnect in the next few minutes can replay recent frames, including thinking already shown to you (and to your other devices). A fresh tab does not; it uses the gated history. Filtering reconnects would add rewrite work to every reconnect.
  **Options:** **A — Accept (recommended).** B — Filter on reconnect.

- **Q3 — Leave reasoning that is already saved (before this feature ships) as it is?** (ADR Q3, corrected)
  **Context:** today's model-context files and debug logs already hold raw reasoning. Left alone, older conversations can still re-send that old reasoning to the model, and old debug logs still contain it. Simply ignoring the old saved field when history is rebuilt is not a migration.
  **Options:**
  - **A — Leave old files untouched; the rebuild ignores the old saved reasoning from now on (recommended).**
  - B — Leave everything untouched, including old conversations' re-send.
  - C — One-time clean-up at upgrade. Conflicts with your 2026-09-15 no-migrations ruling.

- **Q4 — For Anthropic models, what thinking should Omnipus ask for?**
  **Context:** Anthropic's newest models return no thinking text by default, only a hidden signature. Even when asked, they return a *summary* of the thinking, not the raw chain. If Omnipus doesn't ask, users who turn thinking on see empty rows on those models. If Omnipus asks only while the toggle is on, turning it on later cannot reveal older turns, which breaks your D2.
  **Options:**
  - **A — Always ask for the summary where supported, whatever the toggle says; label it "summarized thinking" (recommended).** No extra token cost per Anthropic's docs.
  - B — Ask only while the toggle is on. Faster streaming; older turns stay blank.
  - C — Never ask; Anthropic rows show only duration and token count.

- **Q5 — Whose toggle applies, and what about logins that have no account row?**
  **Context:** Omnipus is single-user, but older installs can still carry a leftover second account. Scheduled and channel-started turns have no web user at all, and the command-line, dev-bypass and environment-token logins have no account row. The ADR doesn't say whose setting decides.
  **Options:**
  - **A — The owner account's toggle decides everywhere; logins without an account row never see thinking (recommended).**
  - B — Each login's own row decides. The live stream would then need per-connection filtering, which the ADR rejected.

- **Q6 — When a model returns only thinking and no answer, what should the user see?**
  **Context:** today Omnipus quietly shows the model's thinking *as the answer* in that case. That exposes thinking to users who have the toggle off, unredacted.
  **Options:**
  - **A — Show a clear "the model returned no answer" error; the thinking goes into the gated thinking row like any other (recommended).**
  - B — Keep showing thinking as the answer, but redacted and only when the toggle is on; otherwise show the error.
  - C — Keep today's behaviour. Contradicts your D2.

- **Q7 — Should thinking round-trips through OpenRouter (Anthropic or Gemini models reached via OpenRouter) be in v1?**
  **Context:** OpenRouter is the provider used for acceptance testing. Its thinking-enabled models return structured reasoning details that may need to be passed back intact during tool use. Omnipus's carrier for them has no field for the signed parts. Not handled means possible failures on thinking-enabled tool turns through OpenRouter.
  **Options:**
  - **A — Out of v1: tracked follow-up issue, and thinking-enabled tool turns via OpenRouter tested before release (recommended).**
  - B — In v1: extend the carrier now (more scope).

## 6. Evidence table

| Claim | Evidence | Certainty |
|---|---|---|
| History rebuilt from disk every turn | `loop_run_turn.go::assembleInitialContext` → `session_recovery.go::RecoverOrphanedToolCalls` → `memory/jsonl.go::GetHistory` | Verified |
| Mid-turn rebuilds at Sites 2–4 | `loop_run_turn.go`, `loop_run_turn_response.go` | Verified |
| No in-memory message projection | `memory/projection.go::ProjectionMeta` | Verified |
| Anthropic thinking-block and `display` rules | context7 `/llmstxt/platform_claude_llms_txt` | Verified (docs) |
| Five `Finalize` streamer implementations incl. SSE | grep of `Finalize(ctx…)` implementations; `gateway_boot.go` route | Verified |
| Transcript writes in `turn_transcript.go` | file read | Verified |
| Redactor is whole-string regex | `audit/redactor.go::RedactCredentials`, `Redactor.Redact` | Verified; split-secret miss Inferred |
| Reasoning-only fallback | `loop_run_turn_iterations.go` (two sites), `session_end.go` | Verified |
| Synthetic identities; empty turn `userID` | `gateway/auth.go` (`devBypassUser`, `envTokenUser`, `cli`); `agent/turn.go` | Verified |
| `rewriteJSONL` is a second marshal path | `memory/jsonl.go::rewriteJSONL` | Verified |
| `getSession` uncovered; WS attach covered | `rest_sessions.go`; `websocket_replay.go` → `streamReplay` | Verified |
| `ReasoningDetail` has no signature field | `protocoltypes/types.go` | Verified; OpenRouter requirement Inferred |
| `RestartGatedKeys` location | `gateway/rest_pending_restart.go` | Verified |
| SDK accumulates in stream path | `anthropic/provider.go` `msg.Accumulate`; `go.mod` sdk v1.48.0 | Verified (call); SDK internals per fork |
| `gateway.users` blocked | `gateway/blocked_paths.go` | Verified |
| Hook exposure, `cloneLLMResponse` | fork report only | Inferred |
| Remaining ~30 agent-side `TranscriptEntry` users not audited | grep listing only | Unknown |
| Newest Anthropic models reject missing prior-turn blocks | not tested | Unknown |
| Self-check | Re-read the merged file against ADR-095 @ `e52a13227` and both reviewers' findings; spot-checked each merged fork claim first-hand (batch grep, exit 0); only this file untracked | Verified |
