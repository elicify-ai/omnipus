/**
 * This file was auto-generated from contracts/asyncapi.yaml
 * (components.schemas.LLMError → x-user-messages).
 * Do not make direct changes to the file.
 * Re-run: node scripts/_gen-asyncapi-types.mjs
 *
 * The user-facing copy and the fault attribution for every LLMError code are
 * contract data, not code. Edit the x-user-messages block in
 * contracts/components/schemas/LLMError.yaml (and its three sibling copies)
 * and re-run `make gen-contracts`. The Go half of this catalogue,
 * pkg/api/generated/llm_error_messages.gen.go, is emitted from the same block.
 */

import type { LLMError } from "./asyncapi-types"

/** Every wire-stable LLMError code, as a union. */
export type LLMErrorCode = LLMError["code"]

/**
 * Who owns the fault behind a code, so the UI (and a copy test) can tell an
 * upstream failure from one Omnipus caused.
 */
export type LLMErrorAttribution =
  | "model"
  | "provider"
  | "product"
  | "config"
  | "ambiguous"
  | "unknown"
  | "user"

/** The closed attribution vocabulary, in contract order. */
export const llmErrorAttributionValues = ["model", "provider", "product", "config", "ambiguous", "unknown", "user"] as const

/** Every LLMError code, in contract (enum) order. */
export const llmErrorCodes = ["media_unsupported", "provider_rejected", "request_too_large", "provider_auth_failed", "rate_limited", "network", "content_policy", "context_too_long", "tool_args", "schema", "agent_not_configured", "workspace_unavailable", "model_unavailable", "needs_provider", "model_unassigned", "turn_canceled", "turn_timed_out", "context_unrecoverable", "context_window_unknown", "unknown"] as const

/**
 * The sentence a user sees for each code. Exhaustive by construction: codegen
 * aborts if any code lacks an entry, and the `Record<LLMErrorCode, string>`
 * annotation makes a stale catalogue a `tsc -b --noEmit` failure.
 */
export const llmErrorUserMessages: Record<LLMErrorCode, string> = {
  media_unsupported: "This model can’t use that kind of attachment. Try a different file format, or switch to a model that supports it.",
  provider_rejected: "The model declined to respond to this request. Try rephrasing it, or switch models.",
  request_too_large: "We built a request that was too large for this model to accept. Try shortening your message, or switch to a model with a larger limit.",
  provider_auth_failed: "The model provider rejected our credentials. Check this provider’s API key in Settings.",
  rate_limited: "The model provider is temporarily overloaded. Wait a moment, then retry.",
  network: "We couldn’t reach the model provider. Check your internet connection and retry.",
  content_policy: "The model provider blocked this request under its content policy. Try rephrasing to remove the flagged content.",
  context_too_long: "This turn needed more context than the model can hold, even after trimming older turns automatically. Try a model with a larger context window, or shorten this message.",
  tool_args: "The model filled in a tool’s arguments incorrectly. Retry — Verbose chat shows which tool and what went wrong.",
  schema: "We sent the model provider a request it couldn’t process — that’s a bug on our side, not yours. Retry the turn, or open Verbose chat for technical details.",
  agent_not_configured: "This agent isn’t on any workspace yet, so it has nowhere to work. Add it to a workspace team to get started.",
  workspace_unavailable: "This agent’s working folder could not be opened. Check that the disk has space and the folder is writable.",
  model_unavailable: "The model you picked isn’t available for this turn, so this reply used the previous model. Check the model in Settings.",
  needs_provider: "This agent's provider isn't configured. Open Settings → Providers to connect one.",
  model_unassigned: "This agent has no model. Pick one in the agent's settings.",
  turn_canceled: "This turn was stopped before it finished.",
  turn_timed_out: "The model provider didn’t finish this turn in time, so it was stopped. Retry — if it keeps happening, open Verbose chat for details.",
  context_unrecoverable: "We couldn’t fit this turn into the model’s context even after clearing older tool results — that’s a bug on our side, not yours. Start a new session, or open Verbose chat for technical details.",
  context_window_unknown: "This endpoint did not report a context length for this model. Set it under Settings → Models → Model overrides → Context length.",
  unknown: "This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model.",
}

/** The fault attribution for each code. Exhaustive by construction. */
export const llmErrorUserAttributions: Record<LLMErrorCode, LLMErrorAttribution> = {
  media_unsupported: "model",
  provider_rejected: "model",
  request_too_large: "product",
  provider_auth_failed: "config",
  rate_limited: "provider",
  network: "ambiguous",
  content_policy: "provider",
  context_too_long: "product",
  tool_args: "model",
  schema: "product",
  agent_not_configured: "config",
  workspace_unavailable: "config",
  model_unavailable: "config",
  needs_provider: "config",
  model_unassigned: "config",
  turn_canceled: "user",
  turn_timed_out: "provider",
  context_unrecoverable: "product",
  context_window_unknown: "config",
  unknown: "unknown",
}
