# Thinking & Reasoning Effort — Founder Interview Output

Status: Decisions captured 2026-09-27. This file is interview-me-shaped input for
`plan-spec`, which writes `docs/internal/specs/thinking-reasoning-spec.md` from it.
Do not hand-edit the spec's Decisions Log once plan-spec has copied it forward — this
file is the source of record for *why*; the spec is the source of record for *how*.

> **Correction (architect, ADR-095, 2026-09-27):** CF6 below said "Reasoning is not
> persisted anywhere" — wrong. `context.jsonl` (the LLM-context store) already carries
> raw `reasoning_content` when populated: `pkg/memory/jsonl.go::addMsg` marshals
> `ArchivedMessage{Message: msg}` wholesale, and `pkg/agent/loop_run_turn_response.go::recordToolCalls`
> sets `ReasoningContent` on the stored assistant message. Correct statement: reasoning is
> not persisted in `transcript.jsonl` or any wire frame, but it IS persisted, unredacted,
> in `context.jsonl` — and a restart-rebuild (`session_recovery.go::RecoverOrphanedToolCalls`
> → `store.GetHistory`) re-sends it today, which conflicts with D13. See ADR-095 D8, which
> rules this a deliberate strip-on-write change so D13 holds by construction (founder
> ratification pending — ADR-095 Q1).

## 0. Scope — issues this feature closes or edits

| Issue | Title | This feature's relationship to it |
|---|---|---|
| [#703](https://github.com/elicify-ai/omnipus/issues/703) | Expose model reasoning as an opt-in setting (default off) | Core: streaming reasoning capture + opt-in surfacing (D2, D4, D5) |
| [#274](https://github.com/elicify-ai/omnipus/issues/274) | Chat UX: Chain-of-Thought display + Selection Toolbar | Chain-of-Thought half only (D4); Selection Toolbar is explicitly OUT of this feature's scope — not mentioned in any founder decision below, so it stays a separate backlog item |
| [#751](https://github.com/elicify-ai/omnipus/issues/751) | Reasoning effort backend: catalog-driven variants + per-provider translation | Core: D6–D10 |
| [#752](https://github.com/elicify-ai/omnipus/issues/752) | Reasoning effort UI: every model-selector surface + `/effort` | Core: D6, D8 |
| [#929](https://github.com/elicify-ai/omnipus/issues/929) | ~~Add messenger reasoning-channel UI~~ → retitled: remove the messenger reasoning channel | Reversed by founder decision D1 — the issue's original ask (build UI for `reasoning_channel_id`) is superseded; the new ask is deletion |
| [#750](https://github.com/elicify-ai/omnipus/issues/750) | Anthropic prompt-caching marker never sent (dispatch routes around the SDK adapter) | Same adapter switch as D5's Anthropic native thinking work; T3 says land together, one dedicated issue for the adapter switch |
| [elicify-ai/omnipus-provider-catalog#29](https://github.com/elicify-ai/omnipus-provider-catalog/issues/29) | Catalog schema: carry `reasoning`/`reasoning_options` (and related fields) from models.dev + LiteLLM | External dependency (D7) — its own work package, separate repo, separate PR |

## 1. Code facts (verified on `origin/release/v0.1.1` @ `73bdad862`; worktree cut from
`4d9ecc13c`, which additionally includes PR #933's messenger-reasoning-publish fix —
D1 removes that fix's target entirely)

| # | Fact | Where |
|---|---|---|
| CF1 | Streaming path parses `reasoning`/`reasoning_content`/`reasoning_details` only to count bytes for the stall watchdog — the text itself is dropped, never reaches the caller | `pkg/providers/openai_compat` streaming delta handling |
| CF2 | Non-streaming path keeps reasoning: `Reasoning: choice.Message.Reasoning` is populated and preferred over `ReasoningContent` | `pkg/providers/common/common.go::ParseResponse` |
| CF3 | `pkg/providers/anthropic` (the SDK adapter, implements `ThinkingCapable`) is never constructed — the factory routes every Anthropic-protocol row to `anthropic_messages`, which has no thinking support at all | `pkg/providers/factory_provider.go` (`ProtocolAnthropic` case) |
| CF4 | No Anthropic thinking-signature capture and no `ThinkingBlockParam` round-trip exists anywhere in the codebase — this is new work, not a wiring fix | repo-wide (verified: no non-test reference) |
| CF5 | `ModelConfig.ThinkingLevel` is set but dropped: the `ThinkingCapable` type-assertion fails for every real provider (only the unreached SDK adapter implements it), so the warning branch always fires and the value never reaches a provider request | `pkg/agent/loop_run_turn.go` (thinking-level gate) |
| CF6 | Reasoning is not persisted anywhere; there is no contract frame or field for it; raw reasoning text is written to the debug log today | repo-wide; `contracts/asyncapi.yaml` has no reasoning frame |
| CF7 | `common.go` re-sends `Message.ReasoningContent` as `reasoning_content` on the *next* request — today's existing cross-turn re-send behavior, kept per D12 | `pkg/providers/common/common.go` |
| CF8 | `@assistant-ui/react` 0.14.27 ships `ReasoningMessagePart` / `ChainOfThoughtPrimitive` / `SelectionToolbarPrimitive`, currently unused | `package.json`, `@assistant-ui/react` |
| CF9 | Model-selector surfaces that need effort exposure: agent default model setting, chat composer `ModelPicker`, chat footer/model display, chat controls, default model (settings), agent model + fallback candidates, agent creation wizard/modal, onboarding model step, Settings → Memory recap model (`recap_model` + fallbacks), and a new `/effort` slash command via `useSlashMenu` | `src/components/chat/composer/ModelPicker.tsx`, `src/components/chat/ModelFooter.tsx`, `src/components/chat/ChatControls.tsx`, `src/components/settings/DefaultModelCard.tsx`, `src/components/agents/AgentProfile.tsx`, `src/components/agents/wizard/Step1Identity.tsx`, `src/components/agents/CreateAgentModal.tsx`, `CreateAgentWizard.tsx`, `src/routes/onboarding.tsx`, `src/lib/onboarding/defaultModel.ts`, `src/components/settings/MemorySection.tsx`, `src/components/ui/model-selector.tsx`, `src/hooks/useSlashMenu.ts` |
| CF10 | Audit redaction is always on (credential patterns; emails are kept as-is) — the mechanism D3 reuses for stored thinking text | `pkg/audit` redaction patterns |
| CF11 | Messenger reasoning channel to be deleted per D1: `reasoning_channel_id` (26 occurrences across per-channel settings types), `pkg/agent/loop.go::handleReasoning` / `spawnReasoningPublish` publish path, PR #933's fix to that path, the associated env vars (`OMNIPUS_CHANNELS_TELEGRAM_REASONING_CHANNEL_ID` and the Slack/Discord/WhatsApp/Google Chat/Feishu/DingTalk/QQ/Weixin equivalents), and the connector docs mentioning it (`docs/connectors/{slack,google-chat,feishu}.md`) | `pkg/config/config_channels_instance.go`, `pkg/agent/loop.go`, `docs/connectors/*.md` |

## 2. Decisions Log

| ID | Topic | Decision | Rationale | Source | Date |
|---|---|---|---|---|---|
| D1 | Messenger reasoning channel | Show thinking in the WEB CHAT ONLY. Remove the messenger reasoning channel entirely — `reasoning_channel_id` on every channel's settings type, `handleReasoning`/`spawnReasoningPublish` and its publish path (including PR #933's fix to it), the associated env vars, and the connector docs mentioning it. No shim, no deprecation comment (repo rule: delete superseded code outright). | Reasoning is a rich, collapsible chat UI concept; forwarding raw reasoning text into a plain messenger chat has no UI to render it well and the feature was never wired to a UI in the first place. #929 is retitled to reflect the reversal — its original ask (build UI for the channel) is superseded by deleting the channel. | Founder | 2026-09-27 |
| D2 | Per-user visibility gate | A per-user "show thinking" toggle, OFF by default, enforced SERVER-SIDE: the server withholds thinking from a user (live streamed frames, replay, REST/API reads) unless that specific user's toggle is on. Turning the toggle on later replays thinking for that user's older sessions too — thinking is stored unconditionally, regardless of the toggle's state at the time it was generated. | Server-side enforcement means no client can bypass the gate by reading raw frames or replay data; storing unconditionally means the toggle is a display preference, not a data-retention decision, so switching it on is never a data-loss event for the user turning it on. | Founder | 2026-09-27 |
| D3 | Storage and redaction | Thinking is saved as part of the chat transcript, redacted through the existing audit credential-pattern redaction (same patterns as audit logs; emails are kept, matching current audit behavior). Live view and a reloaded/replayed view render identically — the redacted, stored copy is what both paths read. | Reuses an existing, already-audited redaction mechanism instead of building a second one; the "live and reload look the same" rule prevents a see-then-refresh discrepancy that would look like data loss or a bug. | Founder | 2026-09-27 |
| D4 | Live rendering | Streamed live as a collapsed "Thinking…" row that fills as the model thinks, click to expand — reusing the existing tool-row collapse pattern already in the chat UI. Only rendered for users whose D2 toggle is on. | Consistent with an existing, already-designed UI pattern (tool-call rows) rather than inventing a new one; keeps the chat surface calm by default (collapsed) while making the detail available on demand. | Founder | 2026-09-27 |
| D5 | Provider scope, v1 | ALL providers in v1: (a) OpenAI-compatible including OpenRouter — forward the streamed reasoning text that is today parsed-then-dropped (CF1) instead of dropping it; (b) Anthropic native — switch the Anthropic protocol row from `anthropic_messages` to the SDK adapter (`pkg/providers/anthropic`), landing together with #750's caching fix since both land in the same adapter switch. Anthropic thinking-block signature capture and in-turn round-trip is its own work package — it does not exist anywhere today (CF4) and is new construction, not a wiring fix. | OpenAI-compatible is a small, low-risk fix (stop discarding data already being parsed). Anthropic is structurally different — it requires the adapter switch #750 already needs for caching, and thinking-block signatures are a new, from-scratch mechanism; scoping it as its own work package keeps the two kinds of work (data plumbing vs. new plumbing) from being estimated or reviewed as one thing. | Founder | 2026-09-27 |
| D6 | Effort control shape | Effort is expressed only as named levels (not Anthropic's raw token-budget "adaptive" mode), presented in the UI as a SLIDER. Anthropic's "adaptive" mode is deliberately left out of v1. | A slider over named levels is one consistent UI idiom across every provider's differently-shaped effort surface (enum, budget, toggle — see #751/#752), rather than a per-provider-shaped control; "adaptive" has no named-level equivalent and introducing it now would break that consistency. | Founder | 2026-09-27 |
| D7 | Catalog dependency | Per-model effort options are sourced from the provider catalog. Extend `elicify-ai/omnipus-provider-catalog#29` first (a change in a separate repo) so the catalog carries `reasoning`/`reasoning_options`. No hardcoded interim list in Omnipus itself while waiting on the catalog. | A hardcoded interim list would need to be thrown away the moment the catalog lands, and risks silently drifting from the catalog's own data (the exact bug #29 exists to fix upstream — see #29's coverage tables). | Founder | 2026-09-27 |
| D8 | Where effort is set | Effort is settable in four places: (a) the agent's default model, (b) per chat (via the model picker and the `/effort` slash command), (c) each fallback model's own setting (independently, sourced from the catalog), and (d) the recap model in Settings → Memory, which also gets the slider. | Matches the full inventory of model-selection surfaces already catalogued in #752 (CF9) — a control that only reached the primary model would leave fallback chains and the recap model unable to express an effort choice at all. | Founder | 2026-09-27 |
| D9 | Unset behavior | When effort is left unset, Omnipus sends nothing to the provider — the provider's own default applies. The slider's unset state displays literally as "Default", not a specific level. | Matches OpenCode's precedent (cited in #751/#752) of "None / provider default" being a real, explicitly reachable state rather than an implicit absence; avoids Omnipus silently picking a level on the operator's behalf. | Founder | 2026-09-27 |
| D10 | Fallback mismatch handling | Each fallback model in a chain carries its own effort setting, drawn from its own catalog row. There is no cross-model mismatch handling — a fallback candidate's effort is simply whatever was set for that candidate, independent of the primary model's setting. | Consistent with D8 (each fallback has its own setting) and D7 (catalog is authoritative per model) — trying to reconcile or validate cross-model effort compatibility would require semantics the catalog does not provide and the founder did not ask for. | Founder | 2026-09-27 |
| D11 | Usage/duration display | The collapsed thinking row shows elapsed duration plus a thinking-token count, when the provider reports one. Thinking tokens are recorded in usage totals (alongside input/output tokens). | Mirrors the level of detail already shown on tool-call rows (the pattern D4 reuses) and gives the operator a cost signal for a feature that can materially increase token spend. | Founder | 2026-09-27 |
| D12 | In-turn vs. cross-turn send-back | Within a single tool-using turn, thinking is sent back to the provider unchanged, kept in memory only — the raw, unredacted text is never written to disk mid-turn. Across separate user messages, keep today's existing behavior: `reasoning_content` is re-sent on the next request (CF7), unchanged from current code. | Anthropic (and providers with similar signed-thinking-block requirements) needs the exact, unaltered thinking block to validate a multi-step tool-using turn — redacting or persisting a mutated copy mid-turn would break that validation. Cross-turn re-send is pre-existing, working behavior this feature does not need to change. | Founder | 2026-09-27 |
| D13 | Restart behavior | After a gateway restart in the middle of a conversation, earlier thinking is NOT re-sent to the provider on the next turn — Omnipus never sends the stored, redacted (D3) copy in its place, since that redacted copy is not the byte-exact block some providers require. | A restart loses the in-memory-only unredacted copy (D12); resending the redacted disk copy would silently feed a provider a different value than what it originally produced, which is unsafe for providers whose thinking blocks are cryptographically signed. | Founder | 2026-09-27 |
| D14 | Debug log | The debug log no longer contains raw reasoning text (today it does — CF6). | Reasoning is now a stored, user-visible, gated feature (D2, D3) — leaving an unredacted copy in the debug log would bypass the per-user visibility gate and the redaction step entirely. | Founder | 2026-09-27 |
| T1 | Catalog gap handling | A stored effort level that is missing from the catalog (for example, the catalog was updated and dropped a level a user had already selected) resolves to the provider's default, plus a visible note — never a hard block. The value itself is still stored as a plain string, not validated against a fixed enum at write time. | Keeps a stale-catalog situation degrading gracefully (same "provider default" semantics as D9) rather than breaking a chat or agent config outright; storing as a plain string avoids coupling the stored value's validity to catalog version at write time. | team-lead | 2026-09-27 |
| T2 | Contract sequencing | One contract change (`contracts/openapi.yaml` / `contracts/asyncapi.yaml`, per Hard Constraint #8) covers all of this feature's new wire surfaces (reasoning frames, effort fields, catalog-derived variant lists) and lands first, before the backend/frontend code that depends on it. | Hard Constraint #8 requires contract-first; batching the whole feature's wire surface into one contract change avoids repeated regeneration churn (`make gen-contracts`) across what would otherwise be several small contract PRs landing out of order. | team-lead | 2026-09-27 |
| T3 | Anthropic adapter switch sequencing | The Anthropic protocol's switch from `anthropic_messages` to the SDK adapter (`pkg/providers/anthropic`) lands together with #750's caching fix, in one dedicated issue for the adapter switch itself (issue text proposed by squad-lead, filed by team-lead — never by squad-lead or any specialist). | Both #750 (caching) and D5's Anthropic-native thinking work require the same adapter switch (CF3) — landing them together means the switch (a real behavior change to every Anthropic-protocol-routed model, including DeepSeek/Moonshot/Z.ai/MiniMax per #750's blast-radius list) is reviewed and tested once, not twice. | team-lead | 2026-09-27 |

## 3. Dependency Graph & Implementation Order (for plan-spec and team-lead's build plan)

```
omnipus-provider-catalog#29 (separate repo, external dependency)
        │  ships reasoning / reasoning_options (+ related fields, D7)
        ▼
T2 contract change (openapi.yaml/asyncapi.yaml: reasoning frame, effort fields,
    catalog-derived variant surface) ── lands first, before any dependent code
        │
        ├──► Backend: openai_compat streaming — stop dropping reasoning text (D5a, CF1)
        │        │  (independent of the Anthropic adapter switch; smaller, lower-risk)
        │        ▼
        │    Backend: reasoning persistence + redaction (D3, CF10) + server-side
        │        visibility gate (D2) + debug-log stop-leaking raw reasoning (D14, CF6)
        │        ▼
        │    Frontend: collapsed "Thinking…" row, live + replay (D4, D11) — gated by D2
        │
        ├──► Backend: Anthropic adapter switch (anthropic_messages → pkg/providers/anthropic)
        │        + #750 caching fix, same change, one dedicated issue (T3)
        │        ▼
        │    Backend: Anthropic thinking-block signature capture + in-turn round-trip
        │        (D5b, CF4) — new construction, depends on the adapter switch landing first
        │        ▼
        │    Backend: send-back semantics — in-turn unchanged round-trip in memory only
        │        (D12), no re-send after restart (D13)
        │
        └──► Backend: effort variant plumbing per #751 (ThinkingCapable per real adapter,
                 replace the dropped ModelConfig.ThinkingLevel gate at loop_run_turn.go,
                 CF5) — consumes the T2 contract's catalog-derived variant lists (D7);
                 catalog-gap fallback per T1
                     │
                     ▼
                 Frontend: effort slider on every surface in CF9 (D6, D8) + /effort
                     slash command — consumes the same contract surface
                     ▼
                 Frontend: usage/duration display on the thinking row consumes thinking-
                     token counts once the backend records them (D11)

Parallel, independent of all of the above: D1's deletion (messenger reasoning channel —
reasoning_channel_id, handleReasoning/spawnReasoningPublish, PR #933's fix, env vars,
docs) — touches only pkg/config/config_channels_instance.go, pkg/agent/loop.go's
messenger publish path, and docs/connectors/*.md; no dependency on the contract change
or either provider adapter.
```

## 4. Process note (for plan-spec and grill-spec — not a founder decision, procedural record)

Per root `CLAUDE.md` and `omnipus-planning-orchestration`: architect first decides
whether any design decision is still open enough to need an ADR — likely candidates are
the server-side per-user gating mechanism for frames/replay/REST (D2), the transcript
storage shape for thinking (D3), and the in-turn signed-block plumbing (D12/D13/D5b).
ADR only if genuinely open; if written, exactly one `grill-spec` ADR-mode round (Opus)
plus one founder interview (via team-lead) plus one correction, no second round. Then
`plan-spec` (GLM → Grok → Sonnet ladder, per the founder's personal-layer rule) writes
`docs/internal/specs/thinking-reasoning-spec.md` from this file — backend and frontend
equally, with a Reachability section, a traceability table, and the catalog repo change
(#29) called out as its own work package. Then exactly two `grill-spec` rounds (Opus),
with a founder interview after each round before its fix round; any blocking finding
still open after round 2 is escalated to team-lead rather than iterated a third time.
