# Adversarial Review: Thinking & Reasoning Effort

**Document reviewed**: `docs/internal/specs/thinking-reasoning-spec.md` (1271 lines, 24 sections; commit `26b482e1d` on `feat/thinking-reasoning`)
**Mode**: Spec
**Round**: Round 1 of 2
**Review date**: 2026-09-27
**Verdict**: BLOCK

**Reviewer notes**: this is an independent review. The reviewer did not write the spec or ADR-095. Every code claim below was checked against the tree at `26b482e1d` using Grep and Read. The GitNexus MCP tools were not available to this reviewer, so impact statements are Grep-based. ADR-095 (Accepted) is treated as settled. It is re-opened only where the spec contradicts it, misstates it, or drops one of its clauses.

## Executive Summary

The review found 36 findings: 3 CRITICAL, 18 MAJOR, 12 MINOR and 3 OBSERVATION. The verdict is BLOCK.

The three CRITICAL findings are:

- **Effort wire surfaces.** The one-contract change (WP-0) misidentifies where effort travels for four of its surfaces: per-chat, the default model, recap, and fallback candidates.
- **Storage shape.** The spec never defines how thinking is stored. Tool-only rounds, which are the common Anthropic pattern, have no transcript entry to carry it, so their thinking would be lost on reload.
- **Live streaming.** Nothing in the spec or ADR delivers thinking *text* live from a provider. Both adapters stream only byte lengths, so the P0 "fills live" row is not buildable as specified.

Frontend coverage (lenses 5, 8, 9 and 10) was reviewed to the same depth as backend. The frontend gaps found:

- the "Thinking…" label collides with the existing `ThinkingIndicator`
- the catalogued `DisclosureRow` primitive is not named
- the Slider wrapper does not forward `aria-valuetext`
- two surfaces in the effort list are not model selectors
- the Profile page is labelled "stored in this browser"

One of the spec's three self-resolved clarifications (#711) is factually wrong.

| Severity | Count |
|----------|-------|
| CRITICAL | 3 |
| MAJOR | 18 |
| MINOR | 12 |
| OBSERVATION | 3 |
| **Total** | **36** |

---

## Findings

### CRITICAL Findings

#### [CRIT-001] Effort wire surfaces misidentified — the "one contract change" is incomplete, and effort is unreachable on three CF9 surfaces

- **Lens**: Contract-first gaps / Reachability
- **Affected section**: §1 C5; §8.1 "Effort … per-chat (riding the same agent model-settings persistence the model picker uses today)"; §8.2; FR-019, FR-026; §23 WP-0
- **Failure scenario**: WP-0 lands with `reasoning_effort` only on `AgentModelParams.yaml` and the agent request schemas. Three consequences follow:
  - **Per chat.** The chat model picker never persists anything to the agents API. The effort a user picks has no field to travel in, so D8(b) is unreachable.
  - **Default model.** `PUT /providers/default-model` rejects an effort key.
  - **Recap model.** Settings → Memory sends `recap_model` through the memory-settings API, not the agents API, so it also has nowhere to put effort.

  A second contract change becomes unavoidable, which breaks T2 and FR-026, or the frontend improvises an untyped key, which breaks Hard Constraint #8.
- **Evidence**:
  - **Per-chat model.** It travels as `metadata.model_name` on the inbound WS chat frame (`contracts/asyncapi.yaml`, metadata `model_name`; `contracts/components/schemas/MessageFrame.yaml` "Typed keys today: `model_name`"). The SPA sends it per message: `src/lib/omnipus-runtime.ts` → `sendMessage(text, { model_name: nextModel })`. `nextModel` is client-store only (`src/store/chat/store.ts::setNextModel`).
  - **Default model.** `contracts/components/schemas/DefaultModelUpdateRequest.yaml` has `additionalProperties: false` and only `provider`/`model`.
  - **Recap model.** `contracts/components/schemas/MemorySettings.yaml` (`recap_model`, `recap_fallback_models` → `FallbackModel.yaml`), saved by `src/components/settings/MemorySection.tsx::updateMemorySettings`.
  - **Fallbacks.** `FallbackModel.yaml` has `additionalProperties: false` (`model`, `provider` only). It is shared by agent chains and the recap chain but is not named in C5.
  - **`AgentModelParams.yaml`** is agent-scoped (`temperature`, `max_tokens`), not per model.
- **Recommendation**: Rewrite C5 as an explicit per-surface table:
  - (a) `FallbackModel.yaml` gains `reasoning_effort`. This covers agent and recap fallbacks.
  - (b) agent primary: `AgentModelParams` or a sibling field (decide which).
  - (c) `DefaultModel.yaml` + `DefaultModelUpdateRequest.yaml`.
  - (d) `MemorySettings.yaml` for `recap_model`.
  - (e) the inbound chat frame's `metadata` gains a typed `reasoning_effort` next to `model_name`.
  - (f) the onboarding model step's payload.

  State the resolution order when several apply: per-message metadata, then agent primary, then default model. See also Q2.

---

#### [CRIT-002] Thinking storage shape undefined; rounds with thinking but no answer text have no transcript entry, so their thinking is lost

- **Lens**: Incompleteness / Contract-first gaps
- **Affected section**: §1 C3 ("Additive fields on the transcript entry"); §8.1 ("captured once per round"); US-3 AS1; FR-001, FR-007
- **Failure scenario**: The typical Anthropic tool round is thinking → `tool_use`, with no text.
  1. The turn captures the round's thinking.
  2. `appendIntermediateAssistantTranscript` returns early because `content == ""`, so no assistant entry exists for that round.
  3. C3's fields have no row to live on.
  4. Live, the row showed thinking. After reload it is gone.

  This breaks D2 ("stored unconditionally"), D3 ("live and reload identical") and US-3 AS1. The contract (WP-0, "lands first, atomically") cannot be authored until this is decided.
- **Evidence**:
  - `pkg/agent/turn_transcript.go::appendIntermediateAssistantTranscript` begins `if ts.transcriptStore == nil || ts.transcriptSessionID == "" || content == "" { return }`.
  - C3 says "fields on the transcript entry". ADR-095 D2 Boundary 2 says replay "emits no thinking-row frames", which implies separate thinking-row frames.
  - The spec never says whether thinking is fields on the assistant or `tool_call` entry, or a separate entry type.
- **Recommendation**: Decide and state the storage unit. For example: one thinking payload per round, stored on (i) that round's assistant entry when one exists, else (ii) a new `type: thinking` entry keyed by the round's message id.

  If (ii) is chosen, update `Message.type` and `ReplayMessageFrame`. Then list every `ReadTranscript` consumer that must ignore or strip it (`pkg/tools/inspect_session.go`, `pkg/tools/handoff.go`, `pkg/tools/delegate_status.go`, `pkg/agent/task_run_loop.go::turnWasReasoningOnly`, the judge/verifier evidence readers). Add a BDD scenario: "thinking from a tool-only round survives reload".

---

#### [CRIT-003] No provider-to-row path for live thinking text exists or is specified; US-2's "fills live" contradicts the ADR's capture points

- **Lens**: Infeasibility / Security
- **Affected section**: US-2 AS1 ("its content grows as thinking arrives"); §4; §9.2 "Loading/streaming"; C2; §2.2 (no provider streaming symbol listed)
- **Failure scenario**: Both adapters stream only text and byte counts. The spec relies on ADR-095 D7, where capture is parseResponse-only at the end of the round, and D4, where capture is once per round. As specified, the row therefore fills only at round end, not live.

  An implementer who must satisfy US-2 AS1 will improvise a text channel. The nearest one is `protocoltypes.ToolCallProgress`, the ungated progress path that feeds `turnState.recordToolCallProgress` and `delegate_status` tool output. Routing text through it would be a gate bypass.
- **Evidence**:
  - `pkg/providers/anthropic/provider.go` streaming loop: `case "thinking": reasoning += len(block.Thinking)` ("Length only"). `onChunk` carries text blocks only.
  - CF1: the `openai_compat` streaming path counts reasoning bytes and drops the text.
  - `pkg/agent/turn.go::recordToolCallProgress`; `pkg/tools/delegate_status.go` renders "%d bytes of reasoning so far".
  - ADR-095 D7: "one capture in `parseResponse` covers both paths".
- **Recommendation**: Specify a new, gated streaming carrier. For example, an `OnReasoningChunk(accumulated string)` provider callback on `protocoltypes`, implemented by both adapters. It is consumed only by the turn's capture, which applies redaction and hold-back and publishes only through the web add-on.

  State explicitly that `ToolCallProgress` must never carry text. Add it to §2.2 and to WP-B/WP-F. Add a proving test that a thinking-text sentinel never appears in any `ToolCallProgress` value or `delegate_status` output. If live filling is not wanted, change US-2 AS1 to "appears at round end" instead.

---

### MAJOR Findings

#### [MAJ-001] C1 toggle endpoint: "flip" is not idempotent, the current state has no read surface, and the Profile page says "stored in this browser"

- **Lens**: Contract-first gaps / UI states / Ambiguity
- **Affected section**: §1 C1; §8.2 row 1; §9.1; §14 row 1
- **Failure scenario**: The problems stack up:
  - A double-click or a client retry on a "flip" endpoint toggles twice, leaving the user's state the opposite of what they intended.
  - §9.1's "renders in its saved state once the profile loads" has no GET to load from. C1 defines only the mutation.
  - The switch is placed in `ProfileSection.tsx`, whose header says "Personal preferences stored in this browser." Every other preference there goes to localStorage (`savePref`). Users would believe the privacy toggle is per browser.
  - C1's anchor, `/user-context`, is the Workspace USER.md endpoint, not an account endpoint.
  - The screen is `/profile` (`ProfileScreen`), not "Settings → Profile".
- **Evidence**:
  - `contracts/openapi.yaml` `/user-context` (tag `Workspace`, "Returns the current content of USER.md").
  - `src/components/settings/ProfileSection.tsx` (header text; `useAutoSave(prefsData, … savePref(...))`).
  - `src/components/screens/ProfileScreen.tsx`; `src/routes/_app/profile.tsx`.
- **Recommendation**: Make C1 a `GET` plus an idempotent `PUT` with a body of `{show_thinking: boolean}` that returns the persisted state. Name the operationIds and state that CSRF is required as on the other cookie-authenticated mutations. Pick the host screen explicitly (see Q1) and state the copy that tells the user this is stored on the server, per login.

---

#### [MAJ-002] C6 catalog "passthrough" is false; new upstream fields are dropped unless Omnipus changes its catalog parser, and no work package owns that

- **Lens**: Inconsistency with code / Contract-first gaps
- **Affected section**: §1 C6 ("Per-model `reasoning` + `reasoning_options` passthrough … the gateway serves the cached catalog document"); §7; §23 WP-EXT/WP-G
- **Failure scenario**:
  - **Fields dropped.** The catalog repo ships the new fields. Omnipus parses the pulled document into typed Go structs and re-serializes a served envelope from those structs, so the new fields are silently dropped. Every model shows "Default" only, and FR-027's degrade state looks like success.
  - **Version gate.** If the catalog repo bumps `schema_version` for the extension, every existing install rejects the pulled document via `ErrSchemaVersion`.
  - **Embedded snapshot.** The committed build-time snapshot must also be regenerated.
- **Evidence**:
  - `pkg/providers/catalog/parse.go` (`json.Unmarshal` into a DTO; `if dto.SchemaVersion != SchemaVersion` → `ErrSchemaVersion`).
  - `pkg/providers/catalog/document.go::Model` (no reasoning fields).
  - `pkg/providers/catalog/served.go::buildServed` (typed `envelopeJSON`/`providerToJSON`).
  - `contracts/components/schemas/CatalogModel.yaml` (`additionalProperties: false`); `ProvidersCatalog.yaml` (`schema_version` enum `"2.0.0"` only).
- **Recommendation**: Add `pkg/providers/catalog` (DTO, `Model`, `served.go`, the embedded `data/` snapshot) to §2.2 and assign it to a named Omnipus work package. Require WP-EXT to add the fields under schema `2.0.0` with no version bump. Add a test that a document carrying `reasoning_options` round-trips to the served JSON.

---

#### [MAJ-003] D11/US-7 "thinking tokens in usage totals" has no contract surface and no plumbing, and is mis-sequenced behind the external catalog

- **Lens**: Contract-first gaps / Incompleteness
- **Affected section**: US-7; FR-024; §1 (absent); §23 ("D11 usage display rides WP-D's row + WP-G's counts")
- **Failure scenario**: Usage totals have no thinking field and no reasoning-token plumbing exists in the tree. WP-0 therefore lands without it, and FR-024 forces a second contract change.

  §23 also ties the counts to WP-G (effort plumbing), which depends on WP-EXT. Token counting for OpenAI-compatible providers and Anthropic would then wait on an external repo it has no need of.
- **Evidence**:
  - `contracts/components/schemas/ModelTokens.yaml`, `TokenUsageSummary.yaml` (in/out/cache only).
  - `grep -rn -iE 'ReasoningTokens|reasoning_tokens|ThinkingTokens' pkg` → 0 hits.
  - The SDK already reports it: `anthropic-sdk-go@v1.48.0` `OutputTokensDetails.ThinkingTokens`.
- **Recommendation**: Add the usage-totals schemas to C1–C7, adding e.g. `thinking` to `ModelTokens.yaml` and to the per-agent/session totals. Name the provider parse points: Anthropic `OutputTokensDetails.ThinkingTokens`; openai-compat `completion_tokens_details.reasoning_tokens`. Move the backend counting to WP-B/WP-F, not WP-G. State whether thinking tokens are a subset of `out` or additive, and pin that with a test.

---

#### [MAJ-004] C4 no-answer outcome: no live frame, no replay field, and non-web consumers are unspecified

- **Lens**: Incompleteness / Contract-first gaps
- **Affected section**: §1 C4; US-8; §9.3; FR-018
- **Failure scenario**:
  - **No live frame.** C4 adds a `Message.type` value only, which is REST. The live turn has no frame to render the notice at turn end, and replay has no field (`ReplayMessageFrame.role` is an enum without it).
  - **Non-web consumers.** Removing the substitution changes what they receive, and the spec does not say what should happen:
    - Messenger users (Telegram, Slack) receive an empty reply.
    - SSE clients receive empty text.
    - A delegating parent receives an empty child answer.
    - The task runner's reasoning-only detector only recognises truncated empty entries, so a non-truncated reasoning-only try becomes "no claim" instead of counting toward the documented "two reasoning-only tries in a row" failure (a silent change to `Task.attempt_count` semantics).
- **Evidence**:
  - `contracts/components/schemas/ReplayMessageFrame.yaml` (`role` enum user/assistant/system/turn_canceled).
  - `pkg/agent/task_run_loop.go::turnWasReasoningOnly` (`return e.Truncated && e.TruncationReason == truncationReasonMaxOutputTokens && strings.TrimSpace(e.Content) == ""`).
  - `contracts/components/schemas/Task.yaml` `attempt_count` ("after two reasoning-only tries in a row").
  - `pkg/agent/session_end.go` substitution (`responseText = rr.resp.Reasoning`).
  - `pkg/agent/loop_truncation.go::evaluateTruncatedSuccess` comment on the substitution.
- **Recommendation**: Extend C4 to three places: `Message.yaml`, `ReplayMessageFrame.yaml`, and the live terminal/done frame, or a new outcome frame. Add one requirement per non-web consumer: messenger, SSE, delegation result, task runner, heartbeat/cron. Update `turnWasReasoningOnly` to key on the new marker and add it to §16's regression table. See Q5.

---

#### [MAJ-005] The existing `thinking_level` config key and its closed enum are left undecided next to the new `reasoning_effort`

- **Lens**: Inconsistency with code / Ambiguity
- **Affected section**: §2.2 row "`pkg/agent/loop_run_turn.go` thinking-level gate … Replaced"; C5; T1; FR-022
- **Failure scenario**: Two effort fields end up coexisting:
  - **`ModelConfig.ThinkingLevel`**: per model-list entry, parsed into a closed enum that includes `adaptive`.
  - **`reasoning_effort`**: per agent, a plain string.

  An implementer keeps both, or maps the new string through `parseThinkingLevel`. Mapping through `parseThinkingLevel` enforces the fixed enum T1 forbids, and `adaptive` survives despite D6. It is also unclear which field wins when both are set.
- **Evidence**:
  - `pkg/config/config.go::ModelConfig.ThinkingLevel` (`json:"thinking_level"`, "off|low|medium|high|xhigh|adaptive").
  - `pkg/agent/thinking.go::parseThinkingLevel`, `ThinkingAdaptive`.
  - `pkg/agent/instance.go` (`nai.thinkingLevel = parseThinkingLevel(...)`).
  - `pkg/agent/loop_run_turn.go` (`llmOpts["thinking_level"]`).
- **Recommendation**: State the fate explicitly. Under D1's delete-superseded rule and D17's greenfield ruling, the expected fate is: `thinking_level`, `ThinkingLevel`, `parseThinkingLevel` and the `thinking_level` `llmOpts` key are deleted, and `reasoning_effort` replaces them. Add a zero-trace check for them. State that effort attaches to a (provider, model) choice, not to the agent. See CRIT-001's resolution order.

---

#### [MAJ-006] The hold-back tail and the frame delta/cumulative semantics are unspecified; a fixed hold-back cannot satisfy "no matchable partial secret" with unbounded patterns

- **Lens**: Infeasibility / Security / Ambiguity
- **Affected section**: §5 edge case 1; C2 ("the redacted thinking-so-far (hold-back applied)"); §16 test 2 ("bounded tail property"); FR-006, FR-007
- **Failure scenario**: The audit patterns have unbounded quantifiers (`sk-[a-zA-Z0-9\-]{20,}`, JWT, `Bearer\s+…`). With a fixed N-character hold-back, `sk-` plus 19 characters of a key is emitted live because it is not yet "matchable". When the rest arrives, the stored copy masks the whole key, but 19 key characters already reached the browser and live no longer equals stored (FR-007).

  C2 also does not say whether a frame carries a delta or the whole text-so-far. The reconciliation strategy depends on that: a client that appends deltas can never un-show text.
- **Evidence**:
  - `pkg/audit/redactor.go::defaultPatterns` (unbounded `{20,}`, `+`).
  - ADR-095 D4: "text … that can no longer match any pattern (a bounded hold-back tail)". No length or rule is given anywhere.
- **Recommendation**: Specify the rule precisely. Suggested: hold back the trailing run of non-delimiter characters (`[A-Za-z0-9._~+/=\-]`) plus any `Bearer\s*` / `sk-` / `key-` prefix. Emit only up to the last delimiter, so a token is released only once complete, and therefore already masked.

  Also specify C2 as "whole redacted text-so-far, client replaces", or "append-only deltas plus a final authoritative whole-text frame". Rewrite test 2 so it can fail: "for every prefix of the stream, no emitted text contains ≥ 1 character of any credential that the final redaction masks".

---

#### [MAJ-007] Thinking-frame size and journal pressure are unbounded; a long thinking stream evicts non-thinking frames for every connection

- **Lens**: Security (DoS) / Incompleteness
- **Affected section**: C2; §7 "Gateway delivery"; FR-008; test dataset row 7 ("multi-KB … no numeric gate invented")
- **Failure scenario**: The per-session journal is capped at 1 MiB and 2048 frames. If C2 frames carry the cumulative text, a 20 KB thinking stream over 200 deltas is about 2 MB of frames, and even deltas at high frequency consume frame slots.

  The journal trims its oldest frames, so answer and tool frames for **all** connections, including toggle-off ones, fall out of seq-cursor catch-up. Reconnects then fall back to snapshot, or lose frames.
- **Evidence**:
  - `pkg/gateway/ws_session_hub.go` (`hubJournalMaxFrames = 2048`, `hubJournalMaxBytes = 1 << 20`, `hubGlobalJournalMaxBytes = 32 << 20`).
  - `pkg/gateway/ws_hub_projection.go` (`hubProjectionTokenChunk` notes that `TokenFrame.content` is `maxLength 65536`). No equivalent bound is stated for C2.
- **Recommendation**: Give C2 a `maxLength`. Specify coalescing, for example at most one thinking frame per 250 ms per round, and delta-only frames. Add an integration test showing that a 50 KB thinking stream leaves a toggle-off connection's seq-cursor catch-up of the answer frames intact.

---

#### [MAJ-008] The messenger-channel deletion (WP-A / US-9) is materially under-scoped, and its "docs included" zero-trace sweep fails on this spec itself

- **Lens**: Incompleteness / Testability
- **Affected section**: §2.2 rows for `ReasoningChannelID` ("13 occurrences") and `docs/connectors/{slack,google-chat,feishu}.md`; US-9; §6 "Scope"; §16 test 17; §23 WP-A ("touches only …")
- **Failure scenario**: WP-A is estimated and reviewed as a config-and-loop change. In reality `ReasoningChannelID()` is a method on the `channels.Channel` interface, implemented by `BaseChannel` and `webchatChannel` and used in channel packages. WP-A also edits `pkg/gateway/webchat_channel.go` and `loop_run_turn_response.go`, which WP-C and WP-D change as well.

  The test 17 sweep "repository-wide … docs included → zero hits" is red by construction, because `docs/internal/specs/thinking-reasoning-spec.md` and the interview file name the key. The RED author will then write an exclusion list, and an over-broad exclusion is a false-green risk.
- **Evidence**: `grep -rn 'reasoning_channel_id|ReasoningChannelID|REASONING_CHANNEL_ID'` gives:
  - 26 in `pkg/config/config_channels_instance.go` (not 13)
  - `pkg/channels/base.go` (interface method, `WithReasoningChannelID`)
  - `pkg/channels/{qq,telegram,wecom,weixin,whatsapp_native}/*.go`
  - `pkg/gateway/webchat_channel.go::ReasoningChannelID`
  - `pkg/gateway/config.json` (13)
  - `config/config.example.json` (12)
  - `pkg/channels/README.md`
  - **13** connector docs: `docs/connectors/{wecom,whatsapp_native,weixin,telegram,slack,qq,matrix,line,irc,google-chat,feishu,discord,dingtalk}.md`

  `docs/internal/architecture/AS-IS-architecture.md` also lists `ReasoningChannelID() string` on the Channel interface.
- **Recommendation**: Replace the §2.2 rows and WP-A scope with the full inventory above. Specify the sweep's exact scope: all of `pkg/`, `cmd/`, `src/`, `config/`, `docs/` **excluding only** `docs/internal/specs/` and `docs/internal/_archive/`. Add the AS-IS doc update.

---

#### [MAJ-009] The work-package graph contradicts its own prose and is missing dependencies

- **Lens**: Inconsistency / Incompleteness
- **Affected section**: §23 diagram and "Sequencing rules"; §22 first assumption; §3 US-6/US-11 priority text
- **Failure scenario**: The problems:
  - **External repo gates everything.** The diagram draws WP-EXT → WP-0 → everything. An external-repo PR therefore gates the P0 privacy core, the D20 gate-bypass fix and WP-A. That contradicts §23's own prose ("Blocks WP-G and WP-H's data (not the visibility core)"), §22 ("slippage degrades, it does not block") and "WP-A needs no contract".
  - **Missing edge.** WP-G (effort plumbing) has no WP-E edge, but Anthropic effort cannot be sent until the SDK adapter is live (CF3: `anthropic_messages` has no thinking support).
  - **Unassigned work.** No package owns the backend deletion of the D20 substitution sites or the typed no-answer marker. WP-C lists gate, persistence and debug log; WP-D lists the frontend notice.
  - **Wrong home for counts.** Thinking-token counts are attached to WP-G (see MAJ-003).
- **Evidence**: §23 diagram vs §23 prose vs §22 bullet 1; `pkg/providers/factory_provider.go` (`anthropicmessages.NewProviderWithTimeout` for the Anthropic protocol).
- **Recommendation**: Redraw the graph:
  - WP-EXT feeds only WP-G's data, with C6's Omnipus-side shape fixed in WP-0 (see Q4).
  - WP-A is a sibling of WP-0, not under it.
  - Add WP-E → WP-G (Anthropic effort).
  - Assign the D20 backend deletion and marker to WP-C explicitly.
  - Move the token counts to WP-B/WP-F.

  WP-EXT is correctly its own package in a separate repo and PR. Keep that, but also add the Omnipus-side catalog plumbing from MAJ-002 to an Omnipus package.

---

#### [MAJ-010] Clarification 2 and §12 (#711 console strip) are factually wrong: the commit is a Storybook demo on a different branch, and no production component exists

- **Lens**: Inconsistency with code / Design-system reuse
- **Affected section**: §24 clarification 2; §9.3; §12 "No-answer notice" row; §22 bullet 2
- **Failure scenario**: The spec says Option C was "landed on `feat/provider-messages`, commit `02eeba8c5`" and that the notice should reuse it or copy "the same tokens/classes". In fact `02eeba8c5` is not on `feat/provider-messages`. It exists only on `feat/provider-messages-visual-demo`, and it changes only `ProviderMessageVisualOptions.stories.tsx`, a Storybook options demo.

  There is no production component to reuse on any branch. An implementer following §12 copies classes out of a demo story, which is exactly the one-off styling the design-system gate exists to stop.
- **Evidence**:
  - `git merge-base --is-ancestor 02eeba8c5 origin/feat/provider-messages` → exit 1 (not an ancestor).
  - `git branch -a --contains 02eeba8c5` → `feat/provider-messages-visual-demo` only.
  - `git show --stat 02eeba8c5` → 1 file, `src/components/chat/ProviderMessageVisualOptions.stories.tsx`.
  - `origin/feat/provider-messages:docs/internal/specs/provider-messages-spec.md` D10 records Option C as the approved *direction*.
- **Recommendation**: Correct clarification 2. Name who builds the production console-strip component: either provider-messages (#711) lands it first as a catalogued component, or this feature builds it with the four-part publication contract (export, `design-system/catalog.json` entry, `@source` line, manifest) and #711 then reuses it. State which feature owns it to avoid two builds. The other two clarifications checked out: `af6a7f63e` did delete `src/lib/onboarding/defaultModel.ts`, 2026-09-16, and is an ancestor of HEAD; D12/D13 are amended by D15 per the interview's second correction.

---

#### [MAJ-011] The Reachability section asserts rather than proves, and some rows name non-selector surfaces or miss real ones

- **Lens**: Reachability
- **Affected section**: §14; §9.4; §9.5; US-6 AS7; FR-019, FR-023
- **Failure scenario**:
  - **`ModelFooter.tsx`** is the read-only per-message model slug in each message footer. §14's "chat footer / controls — both expose the same control" puts an effort slider in every message footer.
  - **`ChatControls.tsx`** no longer holds the model picker.
  - **`RemoveProviderDialog.tsx`** also uses `model-selector`, so §12's "the shared control the slider slots into on every surface" would add effort to provider removal.
  - **`wizard/Step3Tools.tsx`** holds the wizard's fallback editor, but it is not listed, so wizard fallbacks get no effort control (D8c).
  - **`/effort`** is not reachable by editing `useSlashMenu.ts`. The palette is served from `GET /api/v1/commands` (`fetchCommands('web')`) and fed by `pkg/commands/builtin.go::BuiltinDefinitions`. `/model` is a `Definition` with `Surfaces` and `DeliveryClient`. The spec names no backend definition, surfaces, delivery or argument syntax, and US-6 AS7 has two alternative behaviours ("opens … (or the level picker is offered inline)").
- **Evidence**:
  - `src/components/chat/ModelFooter.tsx` header ("per-turn model slug displayed in the message footer … The Agent picker, Model selector … used to live here but …").
  - `grep -rln "ui/model-selector'" src` → includes `src/components/settings/RemoveProviderDialog.tsx`.
  - `src/components/agents/CreateAgentWizard.tsx` ("Step3Tools.tsx — tools_cfg, skills, fallback_models").
  - `pkg/commands/cmd_model.go::modelCommand`; `src/hooks/useSlashMenu.ts` (`fetchCommands`).
- **Recommendation**: Rebuild §14 so each row cites the exact mounting point (file::component and route) and the registration point. For `/effort`: `pkg/commands` `Definition` (name, `Surfaces`, `Delivery`, usage `/effort [level|default]`). Drop ModelFooter and ChatControls, exclude RemoveProviderDialog, add Step3Tools. Pick one AS7 behaviour. See Q6 and Q7.

---

#### [MAJ-012] ADR-095's accepted risks and proving tests are partly dropped: hooks, the tool-result gate test, and raw thinking at rest

- **Lens**: Inconsistency with ADR-095 / Security / Testability
- **Affected section**: §20 (claims to carry the ADR's accepted risks); §13 promises 1 and 3; §6 "Debug log" sweep; §16
- **Failure scenario**: The spec leaves out three things the ADR states:
  - **Hooks.** ADR-095 D8 records that LLM-response process hooks receive raw reasoning (`cloneLLMResponse`) as an operator-trusted extension outside the gate. The spec never mentions hooks except as a "hook edit" trigger, but still promises "zero thinking bytes on every surface" and a data-directory sweep with matches "only in the context file and the redacted transcript". A hook that logs to the data directory falsifies SC-003 and §13 promise 3, with no accepted-risk record.
  - **Tool-result test.** ADR-095 Neutral lists "tool-result content" among the mandatory toggle-off proving tests. §16 has no such test.
  - **At-rest risk.** ADR-095 Negative's "raw thinking and signatures persist at rest in `context.jsonl`" is absent from §20. It appears only in §13 item 4, reworded.

  The six §20 rows that are present do trace to real ADR clauses: D16/D3, D17/Neutral, the OFF→ON Neutral, the O(n) Negative, the D2 narrowing, and D1/F14.
- **Evidence**: `pkg/agent/hooks.go::cloneLLMResponse`; ADR-095 D8 "Adjacent consumers"; ADR-095 Consequences Negative bullets 2 and 4, Neutral bullet 5; `grep -n -i hook docs/internal/specs/thinking-reasoning-spec.md` → 3 hits, none about hook delivery.
- **Recommendation**: Add three §20 rows: hooks outside the gate, raw thinking and signatures at rest, and the shared-type change for every adapter. Scope §13 promise 1 and SC-003 to exclude operator-installed hooks. Add §16 test "toggle-off: no thinking sentinel in any tool-result frame (`inspect_session`, `delegate_status`, `handoff`)".

---

#### [MAJ-013] "No changes to `docs/security.md` / `docs/tools.md`" is wrong; "Secrets never persist in the display copy" overstates the redaction

- **Lens**: Security (user promises)
- **Affected section**: §13 verdict and promise 2; no user-docs plan anywhere in the spec
- **Failure scenario**: The feature adds three things users rely on: a per-login privacy control, a new at-rest store of raw provider reasoning and signatures, and a reuse of the audit redaction. `docs/security.md` publicly documents exactly what that redaction misses: `github_pat_…`, `AIza…`, AWS secret keys, and plain passwords.

  The spec promises users that "Secrets never persist in the display copy". An `AIza…` key in thinking text is stored and shown unmasked, which contradicts that promise, and nothing tells users about it. Separately, the rows that change the redaction list, the default of the new toggle and the new effort control have no user-doc owner, so `docs-verifier` has nothing to audit.
- **Evidence**: `docs/security.md` audit-redaction paragraph ("Anything else is not caught: … a GitHub fine-grained token (`github_pat_…`) or a Google API key (`AIza…`)"); `pkg/audit/redactor.go::defaultPatterns`; `pkg/audit/audit.go` (`RedactPatterns` adds operator patterns — the spec doesn't say whether thinking redaction includes them).
- **Recommendation**: Reword promise 2 to "recognised credential formats (the audit list in `docs/security.md`) are masked; unrecognised formats are not". State whether operator `RedactPatterns` apply. Add a user-docs work item (`docs/security.md` redaction plus at-rest note, `docs/settings.md` or `docs/using-omnipus-ui.md` for the toggle, `docs/providers-and-models.md` for effort), drafted by the implementing leads and audited by docs-verifier.

---

#### [MAJ-014] Size budgets: two touched units are grandfathered shrink-only and at or over their limits

- **Lens**: Infeasibility (repo hard rule)
- **Affected section**: §9.2 and §9.3 (ChatScreen retry path); §2.2 (`replay.go::streamReplay` "Extended")
- **Failure scenario**: The spec adds the thinking row, the notice and retry wiring to `ChatScreen.tsx`, which is at 3604 lines, grandfathered at 3607 and shrink-only. It also adds a gate parameter plus gating to `streamReplay`, which is grandfathered at 283 lines (limit 240) and shrink-only. Either change turns `make lint-budgets` red, and the plan has no extract-first step.
- **Evidence**: `wc -l src/components/chat/ChatScreen.tsx` → 3604; `scripts/budgets/files.txt` → `src/components/chat/ChatScreen.tsx 3607`; `scripts/budgets/functions.txt` → `pkg/gateway/replay.go streamReplay 283`; root `CLAUDE.md` "Size budgets" ("Grandfathered entries … may only shrink — … extract first").
- **Recommendation**: Add extract-first steps. Put the thinking row and notice in new components under `src/components/chat/`, mounted from `MessageItem`. Put the thinking gating in a helper called from `streamReplay` whose net line change is ≤ 0. Name both in WP-C and WP-D.

---

#### [MAJ-015] UI label collision with the existing "Thinking…" indicator; the catalogued `DisclosureRow` is not named and conflicts with "expandable mid-stream"

- **Lens**: Design-system reuse & brand / UI states & journey
- **Affected section**: §9.2; §12 "Thinking-row collapse"; US-2 AS1
- **Failure scenario**:
  - **Label collision.** A toggle-on user sees the existing pre-first-token `ThinkingIndicator`, whose opening phrase is "Thinking…", and the new "Thinking…" row at the same time. A toggle-off user sees "Thinking…" (the indicator) and then no row, which reads as the gate "leaking a label" or as a broken row.
  - **Hand-built row.** §12 says to reuse "the existing tool-call row collapse pattern" but never names the catalogued primitive that is that pattern, `src/components/ui/disclosure-row.tsx` (`DisclosureRow`). A hand-built row is the likely outcome.
  - **Disabled while streaming.** `DisclosureRow` is disabled, with `aria-expanded` omitted, "while there is nothing to disclose yet (e.g. the call is still running)". That conflicts with §9.2's "expandable mid-stream" unless the row passes `hasDetail` as soon as text exists.
- **Evidence**: `src/components/chat/ChatScreen.tsx::ThinkingIndicator` / `InlineThinkingIndicator` (phrase list opening "Thinking…"); `design-system/catalog.json` entry `src/components/ui/disclosure-row.tsx` (primitive, exports `DisclosureRow`); `src/components/ui/disclosure-row.tsx` header comment.
- **Recommendation**: Name `DisclosureRow` as the reuse target with `hasDetail = thinking text non-empty`. Specify how the row and indicator coexist (for example, the row replaces the indicator once the first thinking text arrives). Give the row a distinct label, for example "Reasoning" or "Summarized thinking" per D18, and drop the verbatim "Thinking…".

---

#### [MAJ-016] False-green risk in the signed-block, restart and sweep tests: the oracles cannot be observed with recorded mocks

- **Lens**: Testability & false-green risk
- **Affected section**: §15 Group C (mid-turn rebuild, restart, pre-feature guard), Group A (D16 "may be re-delivered"), Group F (debug-log sweep); §16 tests 13–16; SC-003, SC-004
- **Failure scenario**:
  - **Mock oracles.** "Completes without a provider rejection about missing or altered thinking blocks" can only fail against a real Anthropic endpoint. §7 says integration tests use "recorded provider shapes", and a recorded mock accepts any request. The tests stay green even when blocks are dropped or reordered, which is the exact F1 regression.
  - **Unknown search term.** "sweep … for that turn's reasoning text" with a real model has no known search string.
  - **Unfalsifiable scenario.** The D16 scenario's "may be re-delivered" is satisfied either way.
- **Evidence**: §7 LLM providers "Development: … unit/integration tests use recorded provider shapes"; §15 D16 scenario "Then recently-delivered on-period thinking frames may be re-delivered".
- **Recommendation**: Restate the oracles on the *outgoing request*:
  - the assistant `tool_use` message sent after a trim, recovery or restart carries the byte-identical `ThinkingBlocks` (type, thinking, signature, data) in the original order, placed before `tool_use`
  - the pre-feature case sends no thinking config and logs one named field

  Plant a unique sentinel through a mock provider for the sweep. Make the D16 scenario deterministic: "reconnect with a seq cursor inside retention after an ON→OFF flip → the journal-tail replay contains the on-period thinking frames (asserted present)". Keep the real-Anthropic checks as UAT rows.

---

#### [MAJ-017] US-5 AS1 contradicts ADR-095 D7/D9: with effort unset, no thinking config is sent, so there is no summary and no Anthropic thinking row by default

- **Lens**: Inconsistency with ADR-095 / Ambiguity
- **Affected section**: US-5 AS1; §15 "Anthropic always requests the summary, regardless of toggles"; FR-012
- **Failure scenario**: ADR-095 D7: "when effort is unset Omnipus sends no thinking config at all". "Default" is the shipped state for every model (D9). The spec's unconditional "the provider request asks for the summary" is therefore false in the default configuration.

  The RED author must either write a test the ADR says should fail, or silently add a precondition. The product consequence is not stated anywhere: Anthropic users who never touch effort never see thinking.

  Also noted, UNVERIFIED: the ADR claims `display` defaults to `omitted`, but `anthropic-sdk-go@v1.48.0` documents `ThinkingConfigAdaptiveParam.Display` as "Defaults to `summarized`". Which is true for the newest models was not checked against current Anthropic docs in this review.
- **Evidence**: ADR-095 D7 paragraph 2; `/Users/danielpiatkowski/go/pkg/mod/github.com/anthropics/anthropic-sdk-go@v1.48.0/message.go::ThinkingConfigAdaptiveParam` doc comment.
- **Recommendation**: Add the precondition "effort set to a named level" to US-5 AS1, the BDD scenario and FR-012. State the default-state consequence plainly in §20. Put the product question to the founder (Q3).

---

#### [MAJ-018] Effort slider: the catalogued Slider cannot carry `aria-valuetext`, and "Default" position and level order are undefined

- **Lens**: Accessibility & keyboard / Design-system reuse / Ambiguity
- **Affected section**: §11 "Effort slider"; §12 Slider row; US-6 AS1; §15 effort outline ("Default, low, medium, high")
- **Failure scenario**:
  - **No value text.** The catalogued `Slider` wrapper passes only `aria-label`/`aria-labelledby`/`aria-describedby` to the Thumb and spreads everything else onto Root. A11y requires `aria-valuetext`, which cannot be set without a primitive change, so screen readers announce "0, 1, 2".
  - **Undefined order.** Nothing says whether "Default" sits at the left end, which implies it is "less effort than low", when it is actually "provider decides". Nothing says whether `reasoning_options` from the catalog are guaranteed ordered low→high. The outline's expected ordering is an assumption with no source.
- **Evidence**: `src/components/ui/slider.tsx` (Thumb receives only the three label props); `design-system/manifests/slider.json` exists (a publication-contract edit is needed); D6, D9 (no ordering rule).
- **Recommendation**: Specify a Slider primitive extension that forwards `aria-valuetext` (a `getAriaValueText` prop) through the manifest and lock scripts, or use a catalogued segmented control instead. Specify "Default" as a separate reset affordance, or as position 0 with the stated meaning. Require WP-EXT to publish `reasoning_options` in ascending effort order, and test it.

---

### MINOR Findings

#### [MIN-001] Wrong paths and counts in §2.2
- **Lens**: Inconsistency with code
- **Affected section**: §2.2
- **Failure scenario**: The problems: `pkg/channels/webchat_channel.go` does not exist (the file is `pkg/gateway/webchat_channel.go`), "13 occurrences" should be 26, and the list names 3 connector docs where there are 13. Implementers and reviewers are misled.
- **Recommendation**: Correct the paths and counts (see MAJ-008).

#### [MIN-002] US-1 AS1's "no thinking metadata … on any surface" is contradicted by the existing `delegate_status` output
- **Lens**: Inconsistency
- **Affected section**: US-1 AS1
- **Failure scenario**: `pkg/tools/delegate_status.go` renders "%d bytes of reasoning so far" into a tool result that toggle-off logins can see. Either AS1 is false or the tool must change.
- **Recommendation**: Scope AS1 to "thinking text, thinking rows and per-row thinking metadata", or gate or remove the byte count from that tool text.

#### [MIN-003] WS dev bypass yields an empty `userID`, not `_dev_bypass`
- **Lens**: Ambiguity
- **Affected section**: US-1 AS5; gated-boundary dataset rows 3–5
- **Failure scenario**: `pkg/gateway/websocket.go::authenticateWS` dev-bypass branch returns `true` without setting `wc.userID` (`websocket_chat.go`: "empty on dev-mode bypass"). A predicate keyed on the `_dev_bypass` string misses this case.
- **Recommendation**: State that an empty identity means hidden, and add a dataset row for it.

#### [MIN-004] The toggle's audit event name, registration and CSRF are unspecified
- **Lens**: Security
- **Affected section**: US-1 AS6; FR-004
- **Failure scenario**: `pkg/audit` has exhaustive event-name contract tests (`events_exhaustive_test.go`, `event_name_contract_test.go`). An unnamed event is improvised, or the tests go red.
- **Recommendation**: Name the audit event and its fields (`actor`, `show_thinking`). Require the double-submit CSRF header.

#### [MIN-005] The word "adaptive" is used for two different things
- **Lens**: Ambiguity
- **Affected section**: US-6 AS8; §6 prohibition; FR-019
- **Failure scenario**: D6 bans the Anthropic "raw token-budget 'adaptive' mode", but ADR-095 D7 pins `ThinkingConfigAdaptiveParam` (type `adaptive`). Named effort goes through `OutputConfigParam.Effort` (`low…max`) in `anthropic-sdk-go@v1.48.0`. An implementer may ban the very config type the ADR requires.
- **Recommendation**: Name the banned control as `ThinkingConfigEnabledParam{BudgetTokens}` (the budget input). State that the effort mapping is `output_config.effort` plus the adaptive thinking type.

#### [MIN-006] The orphan-markup strip mutates `ReasoningContent`, and the display source field is unspecified
- **Lens**: Incompleteness
- **Affected section**: §2.2; US-4
- **Failure scenario**: `pkg/agent/loop_truncation.go::stripOrphanToolCallMarkup` rewrites `response.ReasoningContent`, while `LLMResponse.Reasoning` is preferred by CF2. Depending on which field the capture reads, and whether it reads before or after the strip, the thinking row may show raw tool-call markup residue that this strip exists to hide.
- **Recommendation**: Name the capture field (`Reasoning` falling back to `ReasoningContent`) and state that the capture reads after the strip.

#### [MIN-007] Accessibility gaps: streaming row, timer, narrow screens
- **Lens**: Accessibility & keyboard / UI states
- **Affected section**: §11
- **Failure scenario**: The spec doesn't say the growing thinking content must **not** be a live region, which would flood screen readers, or that the elapsed timer is not announced. It doesn't cover touch or narrow layout for a slider inside the composer's model picker (UIJ-03).
- **Recommendation**: State that the row content is `aria-live="off"` and the timer is `aria-hidden`, or announced only at completion. Specify the slider's narrow-width fallback, for example a stacked segmented control.

#### [MIN-008] No concurrency scenario for the gate
- **Lens**: Testability (Phase 3 item 4)
- **Affected section**: §16
- **Failure scenario**: A toggle flip during a live delivery loop, or a config-reload pointer swap during hub enqueue, is untested. The predicate's config read on every delta and every connection has no cost bound.
- **Recommendation**: Add a `-race` integration test that flips the toggle 100 times during a stream and asserts no thinking frame after the persisted OFF plus one delivery. State that the predicate reads a snapshot, not a locked config.

#### [MIN-009] The stale-level note's location and copy, and the notice's name source, are unspecified
- **Lens**: Ambiguity (AMB-05)
- **Affected section**: US-6 AS6; §9.3, §9.4
- **Failure scenario**: The "visible note" could be in chat, on the control or both, with free-form text. "Provider · Model" could use display names or keys. Two engineers build two different things.
- **Recommendation**: Give the exact copy and location for each. Use the catalog `name` for the model and the provider display name.

#### [MIN-010] The test plan omits the CI-authority and local-run rules and the frontend gates
- **Lens**: Testability (TEST-04)
- **Affected section**: §16
- **Failure scenario**: The plan never says CI is the authority for Go results, never limits local runs to one narrow test at a time, and never names `npm run typecheck` or the design-system lock scripts for Slider, DisclosureRow or the new notice component.
- **Recommendation**: Add a "Gates" paragraph covering these.

#### [MIN-011] Traceability mis-traces
- **Lens**: Inconsistency (CON-02)
- **Affected section**: §19
- **Failure scenario**: FR-009 → test 2 is the hold-back test, and "dataset row 6" belongs to the security dataset. FR-011 → test 15 is the OpenRouter gap test. FR-025 → "OpenRouter's structured-reasoning gap" does not test ungated fields.
- **Recommendation**: Re-trace these to the correct tests, and add missing tests where none exist.

#### [MIN-012] "Settings → Profile" names the wrong surface
- **Lens**: UI journey
- **Affected section**: §9.1; §10 step 1; §14 row 1
- **Failure scenario**: `ProfileSection` renders on the `/profile` route (`ProfileScreen`), not in Settings. `onboarding.tsx` also references `ProfileSection` fields. A tester looking in Settings finds nothing.
- **Recommendation**: Name the route. Resolve the host screen together with Q1.

### Observations

#### [OBS-001] C7 is not a contract change
- **Lens**: Overcomplexity
- **Affected section**: §1 C7
- **Suggestion**: "SSE: deliberately nothing" is a prohibition, not a contract change. Move it to §6 and keep test 12.

#### [OBS-002] The interview's §3 graph still carries struck D12/D13 nodes
- **Lens**: Inconsistency
- **Affected section**: §23 "Sequencing rules from the interview (§3)"
- **Suggestion**: The interview's §3 still has the "send-back … memory only (D12), no re-send after restart (D13)" node. Say that §23 supersedes that node, so nobody implements it.

#### [OBS-003] FR-028 is not testable
- **Lens**: Testability
- **Affected section**: FR-028
- **Suggestion**: As a MAY with no observable, FR-028 is untestable. Delete it, or give the partial state a visible marker and a test.

---

## Structural Integrity

### Variant A: Spec mode (plan-spec output)

| Check | Result | Notes |
|-------|--------|-------|
| `Status:` field present, valid value | PASS | "Draft — ready for /grill-spec" |
| ADR linked (or explicitly stated not needed) | PASS | ADR-095, Accepted |
| Contract changes stated first, citing `contracts/` | FAIL | Stated first, but incomplete: CRIT-001, MAJ-003, MAJ-004, MAJ-001 (no GET) |
| API and data section | PASS | §8 exists; the storage-shape gap is CRIT-002 |
| UI screens and states (loading/empty/error/partial) | PASS (with gaps) | §9 names all four states per screen; gaps in MAJ-015, MIN-007 |
| User journey section | PASS | §10 |
| Accessibility and keyboard section | PASS (with gaps) | §11; MAJ-018, MIN-007 |
| Design-system components, catalogue-first | FAIL | DisclosureRow not named, the #711 component doesn't exist (MAJ-010, MAJ-015) |
| Security and user promises section (when touched) | FAIL | Present, but the docs verdict is wrong (MAJ-013) and ADR risks were dropped (MAJ-012) |
| BDD acceptance scenarios, oracle from spec | FAIL | Mostly derived from D-decisions, but some oracles are unobservable or unfalsifiable (MAJ-016) and one contradicts the ADR (MAJ-017) |
| Traceability table: requirement → scenario → test | PASS (with errors) | MIN-011 |
| Reachability section (tool policy / screen wiring) | FAIL | Asserts only; wrong surfaces; `/effort` registration missing (MAJ-011) |

---

## Test Coverage Assessment

### Missing Test Categories

| Category | Gap Description | Affected Scenarios |
|----------|----------------|-------------------|
| Integration (gate) | No test that thinking never appears in tool-result frames | ADR-095 F15 list; MAJ-012 |
| Integration (storage) | No test that thinking from a tool-only round survives reload | CRIT-002 |
| Integration (streaming) | No test that `ToolCallProgress` and `delegate_status` never carry thinking text | CRIT-003 |
| Integration (hub) | No journal-pressure test (a long thinking stream must not evict answer frames from catch-up) | MAJ-007 |
| Concurrency | No test of a toggle flip or config swap during delivery (under `-race`) | MIN-008 |
| Regression | `turnWasReasoningOnly` / task `attempt_count` semantics after the substitution is removed | MAJ-004 |
| Regression | Catalog round-trip of the new fields; schema_version unchanged | MAJ-002 |
| Frontend component states | Row and indicator coexistence; row disabled until text; slider `aria-valuetext`; narrow layout | MAJ-015, MAJ-018, MIN-007 |
| Build gates | `make lint-budgets` after the ChatScreen and streamReplay changes | MAJ-014 |

### Dataset Gaps

| Dataset | Missing Boundary Type | Recommendation |
|---------|----------------------|----------------|
| Thinking text security sweep | Unrecognised credential formats (`AIza…`, `github_pat_…`) | Assert they are NOT masked, and that the docs say so (MAJ-013) |
| Thinking text security sweep | Credential that straddles the hold-back boundary at every offset | Property test over all split points (MAJ-006) |
| Effort value handling | Model switched per chat to a model whose variants don't include the agent's stored level | Expect the stale-level path (T1) on the per-chat model (CRIT-001) |
| Gated boundary matrix | Empty `userID` (WS dev bypass) | Expect hidden (MIN-003) |
| Gated boundary matrix | Tool-only round with thinking | Expect present on reload for toggle-on users (CRIT-002) |

---

## STRIDE Threat Summary

| Component | S | T | R | I | D | E | Notes |
|-----------|---|---|---|---|---|---|-------|
| Toggle endpoint (C1) | ok | risk | ok | ok | ok | ok | Non-idempotent flip; CSRF unstated (MAJ-001, MIN-004) |
| Hub per-connection filter | ok | ok | ok | ok | risk | ok | Journal pressure from thinking frames (MAJ-007) |
| Live capture / streaming | ok | ok | ok | risk | ok | ok | No specified text path; risk of an improvised ungated route (CRIT-003) |
| Redaction / hold-back | ok | ok | ok | risk | ok | ok | Unbounded patterns versus a fixed tail (MAJ-006); unrecognised formats (MAJ-013) |
| Transcript readers (tools) | ok | ok | ok | risk | ok | ok | Storage shape undecided (CRIT-002); no tool-result test (MAJ-012) |
| Hooks | ok | ok | ok | risk | ok | ok | ADR-accepted raw access dropped from the spec (MAJ-012) |
| context.jsonl at rest | ok | ok | ok | risk | ok | ok | ADR-accepted; not recorded in §20 or the user docs (MAJ-012, MAJ-013) |

**Legend**: risk = identified threat not mitigated in the document, ok = adequately addressed or not applicable

---

## Reachability Check

| Question | Answer | Evidence |
|----------|--------|----------|
| Agent-facing tool: registered + policy entry for every agent? | N/A | The feature adds no tool. `/effort` is a slash command and needs a `pkg/commands/builtin.go::BuiltinDefinitions` entry (MAJ-011) |
| User-facing: named screen/component renders it? | Partially | Toggle: `ProfileSection.tsx` on `/profile`, whose header is wrong (MAJ-001). Row: no component named, and the catalogued `DisclosureRow` is not cited (MAJ-015). Notice: the #711 component doesn't exist (MAJ-010). Effort: two listed surfaces aren't selectors, one is missing (MAJ-011) |
| Test plan describes execution, not just authorship? | Partially | SC-010 requires UAT evidence per row. The E2E rows exist, but several oracles are unobservable (MAJ-016) |

---

## Lens Coverage Statement

All twelve lenses were applied:

| # | Lens | Findings |
|---|---|---|
| 1 | Ambiguity | MIN-005, MIN-009, MAJ-018, MAJ-005 |
| 2 | Incompleteness | CRIT-002, MAJ-004, MAJ-008, MIN-006 |
| 3 | Inconsistency with ADR-095 / AS-IS / code | MAJ-002, MAJ-010, MAJ-012, MAJ-017, MIN-001 |
| 4 | Infeasibility | CRIT-003, MAJ-006, MAJ-014 |
| 5 | Contract-first gaps | CRIT-001, MAJ-001, MAJ-003, MAJ-004 |
| 6 | Security | MAJ-007, MAJ-012, MAJ-013, MIN-004 |
| 7 | Reachability | MAJ-011, CRIT-001 |
| 8 | UI states and journey | MAJ-015, MIN-012 |
| 9 | Accessibility and keyboard | MAJ-018, MIN-007 |
| 10 | Design-system reuse and brand | MAJ-010, MAJ-015. Brand/no-emoji: satisfied (§6, §12 state it) |
| 11 | Testability and false-green risk | MAJ-016, MIN-010, MIN-011 |
| 12 | Overcomplexity | OBS-001, OBS-003. The spec is otherwise lean; the ModelFooter/ChatControls slider (MAJ-011) is the main scope creep |

---

## Unasked Questions

1. What is the unit of thinking storage when a round has no assistant text entry (CRIT-002)?
2. What is the exact hold-back rule, and does a C2 frame carry a delta or the whole text-so-far (MAJ-006)?
3. What bounds a thinking frame's size and rate (MAJ-007)?
4. Which provider callback carries live thinking text, and how is it kept out of `ToolCallProgress` (CRIT-003)?
5. What is the fate of `ModelConfig.ThinkingLevel` and `parseThinkingLevel` (MAJ-005)?
6. Which Omnipus package owns the catalog parser, document and served-envelope changes (MAJ-002)?
7. Do operator `RedactPatterns` apply to thinking redaction (MAJ-013)?
8. Who builds the production console-strip component, this feature or #711 (MAJ-010)?

---

## Questions for the founder

1. **Q1 — Where does "Show thinking" live?** The spec puts it on the Profile page. Everything else on that page is stored only in the browser, and the page says so in its header. Settings → Chat already has "Verbose chat", which controls similar display behaviour but is per browser. (A) Profile page, with its header copy changed; (B) Settings → Chat, next to Verbose chat, clearly labelled "applies to your login on every device"; (C) both. **Recommendation: B.**
2. **Q2 — What does "effort per chat" mean?** Today the chat model picker is per message: the choice rides each message and is not saved per chat (it re-seeds from the last model used). (A) Effort is per message, like the model picker: chosen in the picker, sent with each message, and re-seeded the same way; (B) a saved per-chat setting (new server state). **Recommendation: A** (no new state; matches the model picker).
3. **Q3 — Anthropic thinking in the default state.** Under the accepted design, when effort is left at "Default" Omnipus sends no thinking settings to Anthropic. Anthropic then returns no thinking, so users who never touch effort never see Anthropic thinking, even with the toggle on. (A) Accept: thinking appears only once a level is chosen; (B) when "Show thinking" is on for anyone, or always, request thinking with the provider's default effort (changes D9 for Anthropic only); (C) show a hint on the effort control ("choose a level to see thinking"). **Recommendation: A + C.**
4. **Q4 — One contract change versus the external catalog.** T2 says the whole feature's wire surface lands in one contract change, first. The effort-options field depends on the external catalog's schema (#29), so as drawn the entire feature, including the privacy gate and the "reasoning shown as answer" bug fix, waits on another repo. (A) Fix Omnipus's own shape for the effort-options field now in the one contract change, and let the catalog repo match it; (B) allow a second, small contract change for the catalog fields after #29 lands (an exception to T2). **Recommendation: A.**
5. **Q5 — What do non-web surfaces get when a model thinks but gives no answer?** Today they receive the raw thinking as the answer. After this feature they would receive nothing. Affected: a Telegram or Slack user, a parent agent waiting on a sub-agent, and a task run (which today counts "reasoning-only tries"). (A) Send the same plain notice text ("The model (Provider · Model) did not respond") as a normal message on messengers, and count it as a reasoning-only try for tasks; (B) send nothing on messengers and treat it as a failed try for tasks; (C) web-only change, leaving the rest to engineering defaults. **Recommendation: A.**
6. **Q6 — Should `/effort` work outside the web chat?** `/model` works in the web chat, the CLI and messengers. (A) Web chat only; (B) everywhere `/model` works. **Recommendation: A** for v1 (D1 keeps thinking web-only, and effort without the slider UI is text-only elsewhere).
7. **Q7 — Trim the effort surfaces?** Two of the listed places are not model choosers: the model name shown under each message, and the chat controls bar. The shared model chooser is also used when removing a provider. (A) Drop the effort control from the per-message footer, the chat controls bar and the remove-provider dialog, and add it to the agent wizard's fallback step (missing today); (B) keep the list as written. **Recommendation: A.**

---

## Verdict Rationale

**BLOCK.** CRIT-001 and CRIT-002 mean WP-0 cannot be written correctly: the effort fields are placed on the wrong schemas for three surfaces, and thinking storage for tool-only rounds is undefined. The contract is meant to "land first, atomically", so every downstream package would build on an incomplete contract. CRIT-003 means the P0 headline behaviour (a row that fills live) has no specified mechanism, and the nearest improvisation is an ungated path.

The eighteen MAJOR findings fall into four groups:

- **Code facts misread.** C6 passthrough (MAJ-002), the #711 component (MAJ-010), the deletion scope (MAJ-008) and the size budgets (MAJ-014).
- **ADR-095 content dropped or contradicted.** Hooks, the tool-result test and at-rest risk (MAJ-012), and US-5 AS1 (MAJ-017).
- **Wire gaps.** The toggle read and idempotency (MAJ-001), usage tokens (MAJ-003) and the no-answer live and replay carriers (MAJ-004).
- **Frontend gaps.** The label collision and DisclosureRow (MAJ-015), the Slider a11y and ordering (MAJ-018), and the reachability rows (MAJ-011).

The gate design itself, the four ADR-095 boundaries, is carried faithfully into §6, §8 and §15. The gaps sit around it: storage unit, streaming path, tool results and hooks.

### Recommended Next Actions

- [ ] Rewrite C5 per surface, and add usage-totals, no-answer live/replay, and toggle GET/PUT to §1 (CRIT-001, MAJ-003, MAJ-004, MAJ-001)
- [ ] Decide the per-round storage unit and list every transcript reader's handling (CRIT-002)
- [ ] Specify the live thinking-text carrier and ban text on `ToolCallProgress` (CRIT-003)
- [ ] Specify the hold-back rule, frame semantics and frame/journal bounds (MAJ-006, MAJ-007)
- [ ] Add `pkg/providers/catalog` plumbing and the schema_version rule; redraw the WP graph (MAJ-002, MAJ-009)
- [ ] Correct the #711 clarification and assign component ownership (MAJ-010)
- [ ] Replace the deletion inventory and scope the sweep (MAJ-008)
- [ ] Carry the missing ADR-095 risks and tests; fix §13 promises and add a user-docs plan (MAJ-012, MAJ-013)
- [ ] Add extract-first steps for ChatScreen.tsx and streamReplay (MAJ-014)
- [ ] Name DisclosureRow and the indicator coexistence; add a Slider `aria-valuetext` extension (MAJ-015, MAJ-018)
- [ ] Rebuild §14 with mount and registration points, including the `/effort` `pkg/commands` Definition (MAJ-011)
- [ ] Restate the mock-level oracles on outgoing requests; plant sweep sentinels (MAJ-016)
- [ ] Add the effort precondition to US-5 AS1 and FR-012 (MAJ-017)

### Next step in the process

```
Verdict: BLOCK

Review written to: docs/internal/specs/thinking-reasoning-spec-review.md

This is grill round 1 of 2 (fixed). Next: team-lead interviews the
founder on "Questions for the founder", then the spec author fixes
round-1 findings, then grill-spec runs SPEC MODE ROUND 2 on the
corrected spec at docs/internal/specs/thinking-reasoning-spec.md — regardless of
this round's verdict.
```
