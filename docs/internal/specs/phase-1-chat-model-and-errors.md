# Phase 1 — Chat Model Selection, Error Persistence, Fallback Schema

**Status**: design (pre-implementation)
**Branch target**: `hotfix/v0.1.1` (no PR — direct commits per user)
**Author**: Daniel Piatkowski `<10800669+daniel-piatkowski-ai@users.noreply.github.com>`
**Date**: 2026-06-17
**Stack**: Go 1.26.4 (tags `goolm,stdjson`), TypeScript / React 19 / assistant-ui 0.14.14, JSONL transcripts, single-binary embedded SPA, `hot_reload` config.

---

## 1. Context

### 1.1 Problem statement

Five connected defects in the current chat (verified manually on the dev pod at `https://pod-omnipus.fly.dev`):

1. **Error replay gap.** Rate-limit denials are surfaced in the live chat via an in-memory `rateLimitEvent` bucket (`src/store/chat.ts:2409-2444`). The JSONL transcript never records the event. On session reopen, `useSessionReplay` rebuilds the bucket from the transcript → no error is shown. Operators see "GLM 5.2 rate limited" once, then never again.
2. **Model change does not apply.** `resolvedModelConfig` in `pkg/agent/model_resolution.go:111` uses only `cfg.GetModelConfig(modelName)` — which has no passthrough-provider fallback. The UI-side `buildModelListResolver` (same file, line 11) DOES have a fallback to passthrough providers (openrouter, vivgrid). When the user picks a model like `z-ai/glm-5-turbo` (routed through openrouter, no dedicated provider entry), the UI shows it as available, the API persists it to `config.json`, `ApplyAgentModel` (`pkg/agent/loop.go:2723`) returns an error from `resolvedModelConfig` without updating `agent.Model`. The in-memory agent keeps the old model; the next chat still hits the rate-limited `z-ai/glm-5.2`. **The two resolvers are inconsistent** — UI shows what chat can't use.
3. **Fallback schema is provider-blind.** `fallback_models: [string]` (slugs only) means a fallback to `z-ai/glm-5-turbo` reuses the *same provider* as the rate-limited primary. When the provider is the bottleneck (rate limit, outage), the fallback is useless. The schema needs `[{model, provider}]` (object form) so the fallback can route via a different provider.
4. **No per-thread model picker in chat.** Operators can only switch models by editing the agent's config and reloading. The assistant-ui runtime has a built-in `<ModelSelector>` (`@assistant-ui/react` 0.14.14, the package we already use), and ChatGPT shipped model selection in the composer in April 2026 as the industry-standard UX.
5. **Context-window safety at switch time.** When a model is switched mid-conversation, the next LLM call may exceed the new model's context window. Omnipus already has `isOverContextBudget` (`pkg/agent/loop.go:4206`) and `forceCompression` (line 4209) — but these run on the next turn, after the model has been switched. A proactive compress at switch time avoids one visible "request failed" round-trip.

### 1.2 Scope (in / out)

**In scope**:
- 5 sub-changes coordinated across 3 waves
- Per-wave worktree isolation
- Code-regen for `pkg/api/generated` and `src/lib/api/generated` (Phase 1B)
- BDD + TDD tests for each sub-change
- E2E verification on `ci-omnipus` after each wave

**Explicitly out of scope** (deferred to a later phase):
- Multi-agent handoff mid-conversation (already works — covered by `sessionActiveAgent` at `pkg/agent/loop.go:86`, agent_id is recorded per turn per existing schema)
- Thinking-effort controls in the model picker (industry-standard, but not requested)
- Per-provider rate-limit budgets (out of scope — global daily-cost cap is enough for v0.1)
- Migration of any historical transcripts' model field (legacy turns show "model not recorded")
- A real DB-backed history store (we use `ExternalStoreThreadData` + JSONL transcripts; the schema change here is bounded)
- Per-model pricing display, per-provider health indicators
- The `chore/ci-e2e-gate` branch and its e2e fixes (already merged into hotfix as `fc193e8`)

### 1.3 Constraints

- **No new runtime dependencies** (Hard Constraint #1 in CLAUDE.md). `assistant-ui` is already in `package.json`. We add a new assistant-ui primitive use (`ModelContext` + `useThreadModelContext`) but no new npm packages.
- **No new breaking schema for old `fallback_models: [string]`** — migration accepts both forms (Q2 user-decision).
- **`main` stays untouched.** All commits on `hotfix/v0.1.1`.
- **Author identity**: every commit authored as `Daniel Piatkowski <10800669+daniel-piatkowski-ai@users.noreply.github.com>` (per CLAUDE.md `git log -1 --format='%an <%ae>'` rule).
- **Build constraints**: `CGO_ENABLED=0 go build -tags goolm,stdjson` (matches CI; CLAUDE.md Hard Constraint #1 — single-binary embedded SPA).
- **E2E gate on `ci-omnipus` must stay green** after each wave (112 passed, 0 hard-failed baseline at `fc193e8`).

### 1.4 Integration points

- `pkg/agent/model_resolution.go` — single source of truth for model resolution (after refactor)
- `pkg/agent/loop.go` — error event emission, context-budget enforcement, switch-time compress
- `pkg/agent/turn.go` — per-turn transcript model record
- `pkg/session/` — transcript schema (add `model` field on assistant message)
- `pkg/config/config.go` — `AgentConfig.FallbackModels` schema (object form)
- `pkg/gateway/rest.go` — `updateAgent` handler, `handleChatMessage` WS dispatch
- `pkg/gateway/websocket.go` — model passed in WS message frame
- `contracts/components/schemas/Agent.yaml` + `AgentUpdateRequest.yaml` + (NEW) `FallbackModel.yaml` — schema
- `src/components/chat/ChatScreen.tsx` (OmnipusComposer at line 1010) — model selector in composer
- `src/components/agents/AgentProfile.tsx` (line 685: `fallbackModels.map`) — provider-aware fallback editor
- `src/components/ui/model-selector.tsx` — already has `providerGroups` prop (lines 21, 44, 62, 67); reuse, do not duplicate
- `src/lib/omnipus-runtime.ts` — wire `useThreadModelContext` to the `LocalRuntimeOptions.historyAdapter`
- `src/lib/api/generated/...` — generated from openapi-types (regen after schema change)

---

## 2. Existing Codebase Context

### 2.1 Symbols involved

| Symbol | File:Line | Role in this feature |
|---|---|---|
| `resolvedModelConfig` | `pkg/agent/model_resolution.go:111` | **Modifies** — add passthrough fallback; or replace with new `resolveModel` |
| `buildModelListResolver` | `pkg/agent/model_resolution.go:11` | **Modifies** — share the new `resolveModel` helper |
| `ApplyAgentModel` | `pkg/agent/loop.go:2723` | **Reads** — uses `resolvedModelConfig`; should benefit from fix automatically |
| `isPassthroughProvider` | `pkg/agent/model_resolution.go:73` | **Reuses** — already correctly identifies openrouter / vivgrid |
| `forceCompression` | `pkg/agent/loop.go:4209` | **Reuses** — used in new switch-time compress path |
| `isOverContextBudget` | `pkg/agent/loop.go:4206` | **Reuses** — used in new switch-time check |
| `emitEvent(EventKindError, ...)` | `pkg/agent/loop.go:2375` | **Pattern to reuse** — write the rate-limit payload as an `error` event in addition to the WS frame |
| `handleChatMessage` | `pkg/gateway/websocket.go:896` | **Extends** — read `model` from frame metadata, attach to `bus.InboundMessage` |
| `bus.InboundMessage.Metadata` | `pkg/bus/types.go` | **Extends** — `model_name` key already used by some tests; document the contract |
| `cfg.Agents.List[i].FallbackModels` | `pkg/config/config.go` | **Modifies** — `[]string` → `[]FallbackModel` (object form) |
| `agentLoop.GetRegistry().GetAgent(agentID).Model` | `pkg/agent/loop.go` (many call sites) | **Reads** — benefits from fix automatically; no caller changes |
| `ModelSelector` | `src/components/ui/model-selector.tsx:24` | **Reuses** — already supports `providerGroups` and `models` props |
| `OmnipusComposer` | `src/components/chat/ChatScreen.tsx:1010` | **Extends** — add `<ModelSelector>` in the composer action area |
| `useExternalStoreRuntime` | `src/lib/omnipus-runtime.ts:211` | **Modifies** — pass `LocalRuntimeOptions.historyAdapter` to persist `modelContext` |
| `AgentConfig.FallbackModels` (Zod schema) | `src/lib/api/generated/schemas.ts` | **Modifies** — string[] → object[] after regen |
| `assertUserManagementEmbedPresent` removal | `tests/e2e/setup.ts:88-104` (already removed in `fc193e8`) | n/a — confirming the path forward |

### 2.2 Impact assessment

| Symbol modified | Risk level | Direct dependents | Indirect dependents |
|---|---|---|---|
| `resolvedModelConfig` | LOW | `ApplyAgentModel` (loop.go:2723), `apply_agent_model_test.go` (4 tests) | `runAgentLoop` (loop.go, all chat calls) — all benefit automatically |
| `buildModelListResolver` | LOW | UI agent selector (AgentProfile), chat composer | none — call sites are read-only |
| `cfg.Agents.List[i].FallbackModels` (schema change) | MEDIUM | `pkg/config/`, `pkg/api/generated/`, `src/lib/api/generated/`, `AgentProfile.tsx`, `gateway/rest.go` (updateAgent) | `pkg/gateway/rest_onboarding.go`, `pkg/agent/loop.go` (fallback chain) — all regenerate-safe |
| Per-turn `model` field on assistant message | MEDIUM | `pkg/session/`, `pkg/agent/turn.go`, `src/lib/omnipus-runtime.ts` (external store), `src/store/chat.ts` (replay) | `src/components/chat/ChatScreen.tsx` (rendering), `src/components/chat/SessionPanel.tsx` (replay) — UI must handle "model not recorded" for legacy turns |
| `ModelSelector` placement in composer | LOW | OmnipusComposer (ChatScreen.tsx:1010), `ExternalStoreThreadData` | none — purely additive UI |
| `forceCompression` invocation at switch time | LOW | `pkg/gateway/websocket.go` (handleChatMessage), `pkg/agent/turn.go` (new turn creation) | none |
| `EventKindError` emission on rate_limit | LOW | `pkg/agent/events.go` (already exists), `pkg/agent/loop.go` | `src/store/chat.ts` (replay) — benefit automatically |

### 2.3 Relevant execution flows

| Flow | Relevance |
|---|---|
| Chat submit → WS message → `handleChatMessage` (`websocket.go:896`) → `PublishInbound` → `processMessage` → `runAgentLoop` → `agent.Provider.Chat(ctx, ..., agent.Model, ...)` | This is the path the model-resolution fix protects. Every chat submit re-fetches the in-memory `agent.Model` from the registry; if the value is stale, the wrong model is used. |
| `updateAgent` REST handler (`rest.go:1766`) → `ApplyAgentModel` (`loop.go:2723`) | This is the path the schema fix protects. `req.Model` → `ApplyAgentModel` → `resolvedModelConfig` → either success (in-memory updated) or error (in-memory stays). The fix moves this to a single resolver with passthrough fallback. |
| Rate-limit denial → `loop.go:920` → `EventKindRateLimit` (NOT `EventKindError`) | This is the flow that needs the fix. The event is emitted in-memory for the live UI but never written to the transcript. Adding `EventKindError` alongside (or in place of) the rate_limit emission, with the rate_limit payload, will make it appear in the JSONL. |
| `useSessionReplay` → `chatStore.applyReplayFrame` (replay) | The flow that benefits from per-turn `model` field. Replay re-builds each assistant message with its recorded `model` so the user sees which model produced which turn. |

---

## 3. User-decisions already captured (from Q1–Q8)

| Q | Decision | Reference |
|---|---|---|
| 1 | One PR-style commit chain on `hotfix/v0.1.1` directly, no PRs | user message 2026-06-17 |
| 2 | `fallback_models` schema accepts both old `[string]` and new `[{model, provider}]` forms; normalize at load | user Q2 response |
| 3 | Group models under provider headings (use existing `ModelSelector.providerGroups` prop) | user Q3 response |
| 4 | Auto-compress at switch time when new `ContextWindow < current conversation size`; insert synthetic system message | user Q4 response |
| 5 | Per-thread model in `ExternalStoreThreadData.metadata`; agent_id is already per-turn (handoff works) | user Q5 response |
| 6 | Record `model` on each assistant message in the JSONL transcript | user Q6 response |
| 7 | Replace `fallback_models: [string]` with `[{model, provider}]` (object form); accept old form during migration | user Q7 response |
| 8 | Phased with parallel fan-out agents in worktrees, fast-forward-merge to hotfix between waves | user Q8 response |

---

## 4. User stories

### US-1 (P0) — Errors persist across chat reopens
As an operator, when a turn is rate-limited (or any provider error), I see the error in the live chat AND in the transcript after I navigate away and back, so I don't have to re-trigger the error to learn what happened.

**Why P0**: Operators lose operational visibility today. They see the error once, can't reproduce it, and have no record to debug. Fix unblocks a class of "silent failure" tickets.
**Independent test**: trigger a rate-limit error, navigate away, return, confirm error still visible.

### US-2 (P0) — Model change applies immediately to the chat
As an operator, when I change Ava's model in the agent config, the next chat I send to Ava uses the new model (not the old one). The change applies without a gateway restart.
**Why P0**: This is the literal bug the user reported. The model selector is useless today because the change doesn't take effect. Fix unblocks the entire "per-agent model" feature.
**Independent test**: change Ava's model from `z-ai/glm-5.2` to `z-ai/glm-5-turbo`, send a chat, confirm the LLM call hits `glm-5-turbo` (gateway log or response latency signature).

### US-3 (P1) — Per-agent fallback can target a different provider
As an operator, when Ava's primary `z-ai/glm-5.2` is rate-limited, the fallback routes to a different provider (e.g., direct Anthropic) — not the same provider that's already failing.
**Why P1**: Without this, fallbacks are useless in rate-limit scenarios. Without it, the rate-limit "fix" is just hiding the same error.
**Independent test**: configure Ava primary = `openrouter/z-ai/glm-5.2`, fallback = `{model: "claude-sonnet-4.6", provider: "anthropic"}`. Trigger rate-limit on primary. Verify the next attempt uses Anthropic (per LLM call log).

### US-4 (P1) — Model selector in the chat composer
As an operator, I see a model picker inside the chat composer (next to the input box) showing my active model with a dropdown of all available models grouped by provider. The picker is a "next message will use this" indicator (not a per-thread preference). The picker auto-defaults to the LAST model in this session's history (or the agent's default if no history). I can pick a different model — the next message I send uses the new model. Mid-conversation, the conversation is auto-compressed to fit the new model's context window with a real LLM-generated summary bounded by 50% of the new window.
**Why P1**: Industry standard (ChatGPT, Cursor, assistant-ui). Operators today must leave chat, edit agent config, reload, return. The friction is the bottleneck for ad-hoc model experimentation.
**Independent test**: open a chat, change model via the composer dropdown, send a message, verify the LLM call uses the new model (per LLM call log). The transcript's assistant message records the new model.

### US-5 (P1) — Mid-conversation model switch is context-safe
As an operator, when I switch from a large-context model to a small-context model mid-conversation, the next LLM call doesn't fail with a context-overflow error — the conversation is automatically compressed and a synthetic system message tells the new model what was happening.
**Why P1**: Without this, the model picker from US-4 is a footgun. Operators switch → next turn fails → they switch back, never trying again. The auto-compress is what makes the picker safe to use.
**Independent test**: chat with a long thread using `z-ai/glm-5.2` (large context), switch to a smaller-context model mid-conversation, send next message, verify the LLM call succeeded (no context overflow) and the response shows awareness of the prior context.

---

## 5. Acceptance scenarios

### US-1

1. **Given** a chat is open and a turn is rate-limited, **When** the WS `rate_limit` frame is received, **Then** the error is rendered in the live chat AND emitted as an `EventKindError` to the transcript.
2. **Given** a chat with a recorded error event, **When** the user navigates away and back to the same chat, **Then** the error is still visible in the chat.
3. **Given** a chat with a non-rate-limit provider error (e.g. auth failure), **When** the user navigates away and back, **Then** the error is still visible (the fix is not rate-limit-specific).

### US-2

1. **Given** Ava is configured with model `z-ai/glm-5.2`, **When** the operator changes the model to `z-ai/glm-5-turbo` and sends a chat, **Then** the LLM call uses `z-ai/glm-5-turbo` (verifiable in the gateway log or by the LLM call latency).
2. **Given** Ava is configured with a model whose model string is NOT in the providers list (only reachable via passthrough provider), **When** the operator changes to it, **Then** the model change applies without error.
3. **Given** Ava is configured with a model that DOES exist in providers, **When** the operator changes to it, **Then** the behavior is unchanged (no regression).

### US-3

1. **Given** Ava primary = `z-ai/glm-5.2` (via openrouter), fallback = `{model: "claude-sonnet-4.6", provider: "anthropic"}`, **When** the primary is rate-limited, **Then** the next attempt uses Anthropic.
2. **Given** a legacy agent with `fallback_models: ["glm-5-turbo"]` (no provider), **When** the agent is loaded, **Then** the fallback is treated as a passthrough openrouter fallback (no breakage of legacy config).
3. **Given** an agent with both new and legacy fallback forms in the same config, **When** loaded, **Then** both lists are normalized and applied in order.

### US-4

1. **Given** an open chat with provider-groups available (≥2 providers), **When** the user clicks the model selector, **Then** models are grouped by provider heading.
2. **Given** a chat with a per-thread model selected, **When** the user navigates away and back, **Then** the model selection is preserved.
3. **Given** the composer model selector with a selection, **When** the user sends a message, **Then** the model is sent in the WS message frame as `Metadata["model_name"]`.

### US-5

1. **Given** an open chat with a 10-turn history using a large-context model, **When** the user switches to a model with a 4× smaller context window, **Then** the conversation is auto-compressed before the next LLM call.
2. **Given** the switch above, **When** the next LLM call is made, **Then** a synthetic system message is included that names the old model, the new model, and summarizes the prior context.
3. **Given** the switch above, **When** the new model responds, **Then** the response shows awareness of the prior context (per the summary).

---

## 6. Edge cases

| Edge case | Expected behavior |
|---|---|
| Empty `fallback_models` | Empty list. No fallback. Behavior unchanged from today. |
| Empty `providers` array | `resolveModel` returns "not found" for any model. `ApplyAgentModel` errors out. UI selector shows "No models found". |
| Single provider (no `providerGroups` possible) | `ModelSelector` renders flat list with no provider heading. |
| Model name with prefix `openai/gpt-4o` when no `openai` provider is configured | `buildModelListResolver` returns false. Selector does not show it. Chat-side rejects with "model not found" (unchanged). |
| Model name with prefix `openrouter/some-model` when openrouter is configured but no entry for `some-model` | `buildModelListResolver` returns true via passthrough fallback. UI shows it. Chat-side `resolvedModelConfig` MUST match this (the fix). |
| User switches model in the middle of a streaming response | Mid-stream model change is queued: applies on the NEXT turn (not the current one). Avoids in-flight corruption. |
| User switches model on an empty session (no prior turns) | No compression needed. The model applies on the first turn. |
| User has legacy `fallback_models: [string]` AND new `fallback_models: [{model, provider}]` in the same agent | Both lists are normalized and applied. Order: legacy first, then new (or vice versa — see ambiguity). |
| Rate-limit happens on the NEW (auto-compressed) model | Same recursion: try the next fallback, or surface the error in the transcript. |
| `model` field is missing on a legacy transcript turn (old session) | UI shows "(model not recorded)" placeholder. No crash. |
| `useThreadModelContext` returns null (no model selected for the thread) | Default to the agent's `model` config. The thread-level model is an override, not a replacement. |
| Two clients have the same thread open with different per-thread models | Last-writer-wins on the persisted model (consistent with current `ExternalStoreThreadData` behavior). |
| `Metadata["model_name"]` is set in the WS frame but the agent's config has a different model | Backend uses the WS frame's `model_name` (per-thread override wins). The agent config is the default. |
| Synthetic system message would push the conversation over the new model's window | Compress MORE aggressively (drop the synthetic if needed, then drop older turns). Eventually the message fits. |
| `forceCompression` fails during switch | Fall back to the existing on-LLM-call overflow path. Surface a warning in the chat but don't fail the switch. |

---

## 7. Behavioral contract

- When the user changes a model in the agent config, the change applies to the next LLM call without a restart.
- When a turn is rate-limited (or any provider error), the error is recorded in the transcript and visible after page reload.
- When the user opens the model selector in the chat composer, models are grouped by provider (when ≥2 providers exist).
- When the user switches model mid-conversation, the next LLM call uses the new model.
- When the new model's context window is smaller than the current conversation, the conversation is auto-compressed at switch time and a synthetic system message tells the new model what was happening.
- When the user picks a primary model that becomes rate-limited, the fallback chain tries each fallback in order, each via its own provider.

## 8. Explicit non-behaviors

- The system must NOT silently retry on rate-limit. The rate-limit error must be visible to the user (in chat and in transcript).
- The system must NOT change the model mid-stream (during a turn). Mid-stream model change is queued for the NEXT turn.
- The system must NOT remove legacy `fallback_models: [string]` support. The migration is forward-compatible (accept both forms).
- The system must NOT require a gateway restart to apply a model change. The change applies in-memory immediately.
- The system must NOT retry the same rate-limited model more than N times in a row (N=2 in v0.1; same as today).
- The system must NOT show the per-thread model picker for non-core agents that don't have a model config. (Locked agents are not editable; their default config applies.)
- The system must NOT synthesize a fake model history. Legacy turns without a `model` field show "(model not recorded)".

---

## 9. Integration boundaries

### 9.1 OpenRouter / passthrough providers
- **In**: `pkg/providers/openrouter` (or wherever the openrouter client lives — TBD per `pkg/providers/`)
- **Out**: per-fallback provider routing is handled in `pkg/agent/loop.go`'s retry path
- **Contract**: `ApplyAgentModel` succeeds; `Provider.Chat(ctx, ..., model, ...)` accepts the new model string
- **Failure behavior**: if the provider init fails, the user sees a 200 with a `reload_warning` field on the REST response (matches current behavior)

### 9.2 Assistant-UI ModelContext
- **In**: `src/components/chat/OmnipusComposer.tsx` (composes `<ModelSelector>`), `src/lib/omnipus-runtime.ts` (persists via `useExternalStoreRuntime`'s `historyAdapter`)
- **Out**: assistant-ui's built-in `<Thread>` rendering
- **Contract**: `useThreadModelContext()` returns `{getModelContext, setModelContext}`; we read it before each send and write it after each model change
- **Failure behavior**: if assistant-ui's API is unavailable, we fall back to a plain dropdown in the composer; the per-thread model is in `chatStore.activeModel` (a parallel store, not in the store but in `useThreadListItemState.metadata`)

### 9.3 JSONL transcript
- **In**: `pkg/session/` (UnifiedStore), `pkg/agent/turn.go` (writes), `src/lib/omnipus-runtime.ts` (reads for replay)
- **Out**: external storage (deferred)
- **Contract**: each assistant message has a `model` field (string, optional — legacy turns may lack it)
- **Failure behavior**: missing `model` is treated as "unknown", shown as "(model not recorded)" in the UI

### 9.4 dev-mode-bypass (existing)
- **In**: dev pods only (`dev_mode_bypass: true` in `gateway` config)
- **Out**: production deployments
- **Contract**: bearer auth is skipped; SPA may persist `model_name` overrides without a session
- **Failure behavior**: in production, the bearer check enforces per-user persistence; per-thread model is keyed by user

---

## 10. BDD scenarios (by category)

### 10.1 Happy Path (12)

| # | Category | Scenario | Traces to |
|---|---|---|---|
| BDD-1 | Happy | Rate-limit error in chat is visible after navigating away and back | US-1 / Acc 2 |
| BDD-2 | Happy | Provider error in chat is visible after navigating away and back | US-1 / Acc 3 |
| BDD-3 | Happy | Model change from z-ai/glm-5.2 to z-ai/glm-5-turbo applies to next LLM call | US-2 / Acc 1 |
| BDD-4 | Happy | Model change to a passthrough-provider-only model (e.g. openrouter/z-ai/glm-5-turbo) applies to next LLM call | US-2 / Acc 2 |
| BDD-5 | Happy | Model change to a model that IS in providers (regression check) | US-2 / Acc 3 |
| BDD-6 | Happy | Fallback to a different provider when primary is rate-limited | US-3 / Acc 1 |
| BDD-7 | Happy | Legacy `fallback_models: [string]` still works (backward compat) | US-3 / Acc 2 |
| BDD-8 | Happy | Model picker in composer groups models by provider (≥2 providers) | US-4 / Acc 1 |
| BDD-9 | Happy | Per-thread model selection survives page reload | US-4 / Acc 2 |
| BDD-10 | Happy | Composer model change is sent in WS message frame as `model_name` | US-4 / Acc 3 |
| BDD-11 | Happy | Mid-conversation switch to smaller-context model auto-compresses | US-5 / Acc 1 |
| BDD-12 | Happy | Synthetic system message names old and new model + summary | US-5 / Acc 2 |

### 10.2 Alternate Path (6)

| # | Category | Scenario | Traces to |
|---|---|---|---|
| BDD-13 | Alt | User opens chat picker on a single-provider config (flat list, no headings) | US-4 / Acc 1 |
| BDD-14 | Alt | User picks the same model as agent config (no-op, no compress) | US-5 / Acc 1 |
| BDD-15 | Alt | User switches model on empty session (no prior turns) | US-5 / Acc 1 |
| BDD-16 | Alt | User switches model mid-stream (change applies on NEXT turn) | Edge case |
| BDD-17 | Alt | Two clients have same thread with different models (last-writer-wins on persist) | Edge case |
| BDD-18 | Alt | `forceCompression` fails during switch (fall back to on-call overflow path) | Edge case |

### 10.3 Error Path (8)

| # | Category | Scenario | Traces to |
|---|---|---|---|
| BDD-19 | Error | No providers configured → selector shows "No models found" | Edge case |
| BDD-20 | Error | Empty `fallback_models` → no fallback applied | Edge case |
| BDD-21 | Error | Empty `providers` array → `resolveModel` returns "not found" | Edge case |
| BBD-22 | Error | Rate-limit recurses through fallbacks (all fail) | US-3 / Edge case |
| BDD-23 | Error | Model name with `openai/` prefix when no `openai` provider configured | Edge case |
| BDD-24 | Error | `useThreadModelContext` returns null (no model selected) | Edge case |
| BDD-25 | Error | `Metadata["model_name"]` set in WS but agent config disagrees | Edge case |
| BDD-26 | Error | Synthetic system message would push conversation over new model's window | Edge case |

### 10.4 Edge Case (8)

| # | Category | Scenario | Traces to |
|---|---|---|---|
| BDD-27 | Edge | User has BOTH legacy and new fallback forms in same agent config | US-3 / Acc 3 |
| BDD-28 | Edge | `model` field missing on legacy transcript turn | Edge case |
| BDD-29 | Edge | `forceCompression` invoked but conversation is empty (zero turns) | Edge case |
| BDD-30 | Edge | Two clients, same thread, different models (last-writer-wins) | US-5 / Edge case |
| BDD-31 | Edge | Rate-limit on the new (auto-compressed) model | US-3 / Edge case |
| BDD-32 | Edge | Mid-stream model change (queued for next turn) | Edge case |
| BDD-33 | Edge | Both legacy + new fallback forms | US-3 / Acc 3 |
| BDD-34 | Edge | Synthetic system message would push over new model's window | US-5 / Edge case |

---

## 11. Test datasets

### Dataset 1: Model resolution (TDD row 1-6)

| `cfg.Providers` | Input `modelName` | Expected `resolveModel` result | Traces to |
|---|---|---|---|
| `[]` | `"gpt-4o"` | `("gpt-4o", false)` (not found) | BDD-21 |
| `[{"model_name": "gpt-4o", "model": "openai/gpt-4o", "provider": "openai"}]` | `"gpt-4o"` | `("openai/gpt-4o", true)` | BDD-5 |
| `[{"model_name": "z-ai/glm-5.2", "model": "z-ai/glm-5.2", "provider": "openrouter"}]` | `"z-ai/glm-5-turbo"` | `("openrouter/z-ai/glm-5-turbo", true)` (passthrough) | BDD-4 |
| `[{"model_name": "z-ai/glm-5.2", "model": "z-ai/glm-5.2", "provider": "openrouter"}]` | `"openai/gpt-4o"` | `("", false)` (no openai provider) | BDD-23 |
| `[{"model_name": "gpt-4o", "model": "gpt-4o", "provider": "openai"}, {"model_name": "z-ai/glm-5.2", "model": "z-ai/glm-5.2", "provider": "openrouter"}]` | `"z-ai/glm-5-turbo"` | `("openrouter/z-ai/glm-5-turbo", true)` | BDD-4 |
| `[{"model_name": "gpt-4o", "model": "gpt-4o", "provider": "openai"}, {"model_name": "z-ai/glm-5.2", "model": "z-ai/glm-5.2", "provider": "openrouter"}]` | `"gpt-4o"` | `("gpt-4o", true)` | BDD-5 |

### Dataset 2: fallback_models legacy migration (TDD row 7-9)

| Input `cfg.Agents.List[i].fallback_models` | Expected normalized output | Traces to |
|---|---|---|
| (absent) | `[]FallbackModel{}` | BDD-20 |
| `["glm-5-turbo"]` | `[{model: "glm-5-turbo", provider: <openrouter from passthrough lookup>}]` | BDD-7 |
| `["claude-sonnet-4.6"]` | `[{model: "claude-sonnet-4.6", provider: "anthropic"}]` | BDD-7 |
| `[{model: "gpt-4o", provider: "openai"}]` | unchanged | BDD-6 |
| `["glm-5-turbo", {model: "claude-sonnet-4.6", provider: "anthropic"}]` | both lists normalized and merged in order | BDD-27 |

### Dataset 3: Context-budget switch (TDD row 10-12)

| Old model `ContextWindow` | New model `ContextWindow` | Current conversation size | Expected action | Traces to |
|---|---|---|---|---|
| 200,000 | 200,000 | 50,000 | No compress (new model fits) | BDD-14 |
| 200,000 | 8,000 | 50,000 | Compress before switch | BDD-11 |
| 200,000 | 8,000 | 2,000 | No compress (fits) | BDD-15 |
| 200,000 | 8,000 | 0 | No compress (empty) | BDD-29 |
| 8,000 | 200,000 | 7,000 | No compress (new model has more room) | BDD-15 |
| 200,000 | 8,000 | 7,500 | Compress (just over) | BDD-26 |

### Dataset 4: Per-thread model in WS frame (TDD row 13-14)

| `msg.Metadata["model_name"]` | Agent config `model` | Expected LLM call model | Traces to |
|---|---|---|---|
| `"z-ai/glm-5-turbo"` | `"z-ai/glm-5.2"` | `"z-ai/glm-5-turbo"` (override wins) | BDD-10 |
| (absent) | `"z-ai/glm-5.2"` | `"z-ai/glm-5.2"` (default) | BDD-24 |
| `"openrouter/some-model"` | (any) | `"openrouter/some-model"` (passthrough) | BDD-4 |
| `""` (empty string) | (any) | agent default (empty override = use default) | BDD-24 |

### Dataset 5: Per-turn `model` field in transcript (TDD row 15)

| Turn | `model` in transcript | UI display | Traces to |
|---|---|---|---|
| 1 | `"z-ai/glm-5.2"` | "z-ai/glm-5.2" | BDD-12 |
| 1 (legacy) | (absent) | "(model not recorded)" | BDD-28 |
| 1 | `""` (empty) | "(unknown model)" | BDD-22 |
| 1 | `null` | "(model not recorded)" | BDD-28 |

### Dataset 6: Model picker provider grouping (TDD row 16)

| `cfg.Providers` count | `providerGroups` rendered | Expected UI | Traces to |
|---|---|---|---|
| 0 | `[]` | "No models found." | BDD-19 |
| 1 | `[{provider: "openrouter", models: [...]}]` | Flat list, no heading | BDD-13 |
| 2 | `[{openrouter}, {anthropic}]` | Two `<CommandGroup>` headings | BDD-8 |
| 5 | 5 groups | Five headings | BDD-8 |

### Dataset 7: rate_limit error transcript (TDD row 17)

| Trigger | Event emitted to transcript | Replay visibility | Traces to |
|---|---|---|---|
| `rate_limit` WS frame | `EventKindError` with `RateLimitPayload` | Yes (visible after reload) | BDD-1 |
| Provider auth failure | `EventKindError` with `ErrorPayload` | Yes | BDD-2 |
| Validation error | `EventKindError` with `ErrorPayload` | Yes | BDD-2 |

---

## 12. TDD plan

| Order | Test Name | Level | Traces to BDD | Description |
|---|---|---|---|---|
| 1 | `TestResolveModel_EmptyProviders_ReturnsNotFound` | Unit | BDD-21 | No providers, any model returns false |
| 2 | `TestResolveModel_ExactMatchInProviders` | Unit | BDD-5 | "gpt-4o" matches `model_name: "gpt-4o"` |
| 3 | `TestResolveModel_PassthroughProviderFallback` | Unit | BDD-4 | openrouter + "z-ai/glm-5-turbo" (not in providers) returns true |
| 4 | `TestResolveModel_OpenaiPrefixWithoutOpenaiProvider_ReturnsFalse` | Unit | BDD-23 | "openai/gpt-4o" without openai provider returns false |
| 5 | `TestResolveModel_MultipleProviders_GroupByPassthrough` | Unit | BDD-4, BDD-8 | Multiple providers, passthrough selects correct one |
| 6 | `TestApplyAgentModel_PassthroughModel_UpdatesInMemory` | Unit | BDD-4 | ApplyAgentModel with passthrough model actually updates `agent.Model` |
| 7 | `TestFallbackMigration_LegacyString_ConvertsToObject` | Unit | BDD-7 | `["glm-5-turbo"]` → `[{model: "glm-5-turbo", provider: "openrouter"}]` |
| 8 | `TestFallbackMigration_NewObject_Unchanged` | Unit | BDD-6 | `[{model, provider}]` → unchanged |
| 9 | `TestFallbackMigration_BothForms_Ordered` | Unit | BDD-27 | Legacy first, then new, in order |
| 10 | `TestSwitchTimeCompress_LargerToSmaller_TriggersCompress` | Unit | BDD-11 | Old=200k, new=8k, conv=50k → compress |
| 11 | `TestSwitchTimeCompress_SameWindow_NoOp` | Unit | BDD-14 | Old=200k, new=200k, conv=50k → no compress |
| 12 | `TestSwitchTimeCompress_EmptySession_NoOp` | Unit | BDD-15, BDD-29 | Old=200k, new=8k, conv=0 → no compress |
| 13 | `TestWebSocketFrame_ModelInMetadata` | Unit | BDD-10, BDD-24 | `msg.Metadata["model_name"]` correctly forwarded to chat |
| 14 | `TestWebSocketFrame_EmptyMetadata_UsesAgentDefault` | Unit | BDD-24 | Empty metadata → agent config model |
| 15 | `TestTranscript_AssistantMessage_HasModelField` | Unit | BDD-12, BDD-28 | New assistant message has `model` field; legacy (decoded) may lack it |
| 16 | `TestModelSelector_RendersProviderGroups` | Unit (vitest) | BDD-8 | ≥2 providers → groups rendered |
| 17 | `TestRateLimit_EmitsErrorEvent` | Unit | BDD-1 | rate_limit WS path emits `EventKindError` |
| 18 | `TestProviderError_EmitsErrorEvent` | Unit | BDD-2 | non-rate-limit errors emit `EventKindError` |
| 19 | `TestEndToEnd_ModelChange_TakesEffect` | E2E | BDD-3 | Ava model change → next chat uses new model |
| 20 | `TestEndToEnd_PassthroughModel_AppliesViaFallback` | E2E | BDD-4 | Passthrough model works end-to-end |
| 21 | `TestEndToEnd_FallbackToDifferentProvider_OnRateLimit` | E2E | BDD-6 | Ava primary rate-limited → fallback uses different provider |
| 22 | `TestEndToEnd_ComposerModelSelector_PersistsAcrossReload` | E2E | BDD-9 | Per-thread model survives reload |
| 23 | `TestEndToEnd_ContextSwitch_AutoCompresses` | E2E | BDD-11, BDD-12 | Mid-conv switch compresses + synthetic message |
| 24 | `TestEndToEnd_ErrorReplay_VisibleAfterReload` | E2E | BDD-1, BDD-2 | Rate-limit error visible after page reload |

Test implementation order: Unit tests first (rows 1–18), then integration, then E2E (rows 19–24). Within each level, order by dependency (resolution first, then apply, then schema, then WS, then transcript, then UI).

---

## 13. Functional requirements

- **FR-001** (US-1): The system MUST emit an `EventKindError` event to the transcript whenever a `rate_limit` WS frame is emitted. The event payload MUST include the `RateLimitPayload` fields.
- **FR-002** (US-1): The system MUST emit an `EventKindError` event to the transcript for any provider error (auth failure, validation error, etc.), not just rate-limit.
- **FR-003** (US-2): `resolvedModelConfig` and `buildModelListResolver` MUST share a single underlying resolution function. Both MUST use the passthrough-provider fallback.
- **FR-004** (US-2): `ApplyAgentModel` MUST update `agent.Model`, `agent.Provider`, and `agent.Candidates` in-memory on success. On failure, it MUST log an error AND include a `reload_warning` in the 200 response (matches today's contract).
- **FR-005** (US-3): `AgentConfig.FallbackModels` MUST accept `[{model, provider}]` (object form) going forward. The schema MUST also accept the legacy `[string]` form during migration.
- **FR-006** (US-3): At config load time, the system MUST normalize legacy `fallback_models: [string]` to the new object form. The order MUST be preserved.
- **FR-007** (US-3): When the primary model errors with a rate-limit, the system MUST try the next fallback. The fallback MUST use its own provider, not the primary's.
- **FR-008** (US-4): The chat composer MUST include a model selector built on `<ModelSelector>` from assistant-ui. The selector MUST use the `providerGroups` prop when ≥2 providers are configured. The selector is a "next message will use this" indicator (not a per-thread preference).
- **FR-009** (US-4): On session reopen (continue from history), the picker MUST auto-default to the LAST model in the transcript's assistant messages. If the transcript has no model field (empty / legacy), it MUST default to the agent's `model` config.
- **FR-010** (US-4): On send, the model selection MUST be sent in the WS message frame as `msg.Metadata["model_name"]`.
- **FR-011** (US-5): When the model is switched mid-conversation, the system MUST check `if isOverContextBudget(newModel.ContextWindow, currentMessages, ...)`. If the new window is smaller than the current conversation, the system MUST (a) call a new `summarizeDroppedTurns()` to produce a real LLM-generated summary of the dropped turns, (b) the summary MUST be bounded to ≤50% of the new model's context window, and (c) `forceCompression` MUST be invoked before the next LLM call.
- **FR-012** (US-5): A synthetic system message MUST be inserted into the conversation at switch time. The message MUST name the old model, the new model, the timestamp, and include the ≤50%-bounded summary.
- **FR-013** (US-5): Each assistant message in the JSONL transcript MUST have a `model` field recording the model that produced it.
- **FR-014** (US-1, US-4): Replay re-builds the chat from the transcript MUST show the recorded error events and the recorded model per turn. The UI MUST conditionally show the model field — only when the turn has a `model` field recorded. Legacy turns without a `model` field MUST NOT show the model info (no placeholder text, no "model not recorded" string).

## 14. Success criteria

- **SC-001**: 100% of new BDD scenarios pass (`go-test ./...` for unit, `npx vitest run` for vitest, `npx playwright test` for e2e).
- **SC-002**: All previously-passing e2e tests still pass (regression: 112/127 baseline must hold).
- **SC-003**: After the changes, `go-test` on the worker reports 0 failures for all gates (`gofmt`, `go-build`, `go-vet`, `contracts`, `spa`, `go-test`).
- **SC-004**: After the changes, the e2e gate on `hotfix/v0.1.1` reports ≥112 passed, 0 hard failures.
- **SC-005**: The model-resolution unit tests cover both exact-match AND passthrough paths (datasets 1 + 5).
- **SC-006**: The fallback migration tests cover legacy `[string]`, new `[{model, provider}]`, AND mixed forms (dataset 2).
- **SC-007**: The context-switch tests cover all 5 (WindowNew, WindowSmaller, WindowLarger, ConversationEmpty, JustOver) (dataset 3).
- **SC-008**: Each per-wave commit on `hotfix/v0.1.1` triggers the e2e gate on `ci-omnipus`. All waves end with the gate green.

## 15. Traceability matrix

| Requirement | User Story | BDD Scenario(s) | Test Name(s) |
|---|---|---|---|
| FR-001 | US-1 | BDD-1 | Test 17 |
| FR-002 | US-1 | BDD-2 | Test 18 |
| FR-003 | US-2 | BDD-3, BDD-4, BDD-5 | Test 1, 2, 3, 4, 5 |
| FR-004 | US-2 | BDD-3, BDD-4 | Test 6 |
| FR-005 | US-3 | BDD-6, BDD-7, BDD-27 | Test 7, 8, 9 |
| FR-006 | US-3 | BDD-7, BDD-27 | Test 7, 9 |
| FR-007 | US-3 | BDD-6, BDD-22 | Test 21 |
| FR-008 | US-4 | BDD-8, BDD-13 | Test 16 |
| FR-009 | US-4 | BDD-9 | Test 22 |
| FR-010 | US-4 | BDD-10, BDD-24, BDD-25 | Test 13, 14 |
| FR-011 | US-5 | BDD-11, BDD-15, BDD-26, BDD-29 | Test 10, 11, 12, 23 |
| FR-012 | US-5 | BDD-12 | Test 23 |
| FR-013 | US-5 | BDD-12, BDD-28 | Test 15 |
| FR-014 | US-1, US-4 | BDD-1, BDD-2, BDD-28 | Test 15, 17, 18, 24 |

## 16. Per-wave acceptance criteria

### Wave 1A (model-resolution fix + errors preserved) — 2 parallel agents
- FR-001, FR-002, FR-003, FR-004 fully implemented
- `go-test ./pkg/agent/...` passes
- `npx vitest run src/components/ui` passes (no UI change here)
- `go-test` on the worker: green
- E2E on `hotfix/v0.1.1` post-merge: still ≥112 passed, 0 hard failures

### Wave 1B (fallback schema + per-turn model record) — 1 agent with regen
- FR-005, FR-006, FR-007, FR-013 fully implemented
- `make gen-contracts` succeeds; `make verify-contracts` passes
- `go-test ./pkg/... ./contracts/...` passes
- `npm run typecheck` (regen produced new Zod types) passes
- E2E on `hotfix/v0.1.1` post-merge: still ≥112 passed, 0 hard failures

### Wave 2 (UI: agent fallback editor + chat composer model selector) — 2 parallel agents
- FR-008, FR-009, FR-010, FR-014 partially (legacy turns display) implemented
- `npx vitest run src/components/agents src/components/chat src/components/ui` passes
- New e2e: per-thread model picker test (US-4 / BDD-9)
- E2E on `hotfix/v0.1.1` post-merge: ≥112 passed, 0 hard failures

### Wave 3 (auto-compress at model switch) — 1 agent
- FR-011, FR-012 fully implemented
- New e2e: mid-conversation switch test (US-5 / BDD-11)
- E2E on `hotfix/v0.1.1` post-merge: ≥112 passed, 0 hard failures

## 17. Out of scope (explicit non-goals)

- **Multi-agent handoff UI** (already works via `sessionActiveAgent` per `pkg/agent/loop.go:86`)
- **Thinking-effort controls** in the model picker (industry-standard, but not requested)
- **Per-provider rate-limit budgets** (global daily-cost cap is enough for v0.1)
- **Migration of historical transcripts' model field** (legacy turns show "model not recorded")
- **A real DB-backed history store** (`ExternalStoreThreadData` + JSONL is the v0.1 source of truth)
- **Per-model pricing display, per-provider health indicators**
- **The `chore/ci-e2e-gate` branch and its e2e fixes** (already merged into hotfix as `fc193e8`)
- **New npm packages** — assistant-ui is already in `package.json`
- **A migration plan for users** (config schema change accepts both forms; auto-migration at load time; no user action required)

## 18. Resolved design decisions (from Q1–Q6 in-session)

| # | Decision | Rationale |
|---|---|---|
| Q1 | **Full cutover to `[{model, provider}]`**, no legacy `[string]` support. One-time conversion of any existing legacy data at first load. | User direction: "we do not have to be backward compatible". Simpler code, no two paths to maintain. |
| Q2 | **Mid-stream model change is queued for the next turn.** The in-flight turn finishes on the old model; the new model applies on the next outgoing message. | Avoids in-flight corruption. Matches `sessionActiveAgent` semantics (handoff is per-turn). |
| Q3 | **Picker is a "next message will use this" selector, not a per-thread preference.** Picker state is local to the page-load (not persisted). On session reopen (continue from history), the picker auto-defaults to the **last model in the transcript**. The complete model-resolution chain at send time is: (1) the picker value if user has selected one this session, else (2) the last model from history, else (3) the agent's `model` config, else (4) the first entry of `fallback_models`. | User clarification. There is no per-thread model concept in this architecture (one session = one thread). The model is recorded per-message in the transcript; the picker just picks what the NEXT message will use. |
| Q4 | **Plain wording for synthetic switch message**: "Conversation moved to {new_model} from {old_model} on {timestamp}. The prior turns have been compressed to fit the new context window. Summary: {summary}". | User direction. Clear, explicit, names both models. |
| Q5 | **Real LLM-generated summary, bounded by ≤50% of the new model's context window.** Existing `forceCompression` (loop.go:6238-6308) only drops oldest half of turns and adds a meta-note. We add a new `summarizeDroppedTurns(...)` that uses a small LLM call to produce a real content summary of the dropped turns, sized to ≤50% of the new window. | User direction. The meta-note isn't sufficient; the new model needs actual context about what was happening. The 50% cap leaves ≥50% of the new window for the actual conversation. |
| Q6 | **No legacy data migration.** The UI conditionally shows the `model` field — only when the turn has it recorded. Legacy turns just don't display the model info. No placeholder text. | User direction. No migration cost. Honest about what we know. |

## 19. Holdout evaluation scenarios (not used during development)

These will be used to verify the implementation after development. They are NOT in the TDD plan.

- **HE-1**: Open Ava's profile, change her model from `z-ai/glm-5.2` to a model from a different provider. Close the profile, send 5 chat messages, then change to a third model. Reload the page. Each turn's `model` field should match the model active at that time.
- **HE-2**: Configure Ava with primary = openrouter, fallback = anthropic. Trigger an OpenRouter rate limit (or mock it). Verify the next attempt uses Anthropic. Verify the rate-limit error appears in the transcript.
- **HE-3**: Open a chat, change the model from a 200k-context model to a 8k-context model mid-conversation. Send a long-ish history (50+ messages) before the switch. Verify the next LLM call succeeds and the response references the prior context.
- **HE-4**: Trigger 3 different types of errors (rate-limit, auth failure, validation). Each should appear in the live chat AND in the transcript after reload.
- **HE-5**: Open two browser tabs pointing at the same chat. Change the model in tab A. Send a message in tab B. Verify the model used is the latest persisted (last-writer-wins).

## 20. References

- [assistant-ui ModelSelector docs](https://www.assistant-ui.com/docs/ui/model-selector)
- [assistant-ui Model Context](https://www.assistant-ui.com/docs/copilots/model-context)
- [ChatGPT release notes (model picker in composer, 2026-04-28)](https://help.openai.com/en/articles/6825453-chatgpt-release-notes)
- [Twig: Model Switching Mid-Conversation](https://www.twig.so/dev/rag-scenarios-and-solutions/llm/model-switching)
- [Agenta: 6 techniques to manage context length](https://agenta.ai/blog/top-6-techniques-to-manage-context-length-in-llms)
- [Atlan: LLM Context Window Limitations 2026 (MECW)](https://atlan.com/know/llm-context-window-limitations)
- [Reddit: Real users on model switching (scratchpad pattern)](https://www.reddit.com/r/artificial/comments/1rv6tfi/does_anyone_actually_switch_between_ai_models)
- [hastewire: ChatGPT model switching workflow](https://hastewire.com/blog/how-to-use-chat-gpt-changer-for-model-switching)
- `pkg/agent/model_resolution.go` (the bug location)
- `pkg/agent/loop.go:2723` (`ApplyAgentModel`)
- `src/store/chat.ts:2409-2444` (rate_limit WS handler — currently in-memory only)
- `src/components/ui/model-selector.tsx:24` (existing `ModelSelector` with `providerGroups` prop)
- `src/components/chat/ChatScreen.tsx:1010` (OmnipusComposer — model picker drops in here)
- `tests/e2e/setup.ts:88-104` (already removed in `fc193e8`)

---

## Summary

- **User stories**: 5 (P0 × 2, P1 × 3)
- **BDD scenarios**: 34 (12 Happy + 6 Alternate + 8 Error + 8 Edge)
- **Test datasets**: 7 datasets, ~50 total test data rows
- **TDD plan**: 24 tests (18 unit + 6 e2e; integration folded into unit where appropriate)
- **Functional requirements**: 14 (FR-001 through FR-014)
- **Success criteria**: 8 (SC-001 through SC-008)
- **Ambiguity warnings**: 6 (Q1–Q6 in §18) — to resolve before implementation
- **Holdout evaluation scenarios**: 5 (HE-1 through HE-5)
- **Out of scope items**: 9 (per §17)
