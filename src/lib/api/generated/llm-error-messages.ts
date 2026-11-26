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
export const llmErrorCodes = ["media_unsupported", "media_too_large", "provider_rejected", "request_too_large", "provider_auth_failed", "rate_limited", "quota_billing", "network", "provider_stalled", "provider_empty_reply", "content_policy", "context_too_long", "tool_args", "tool_call_truncated", "schema", "agent_not_configured", "workspace_unavailable", "model_unavailable", "model_retired", "needs_provider", "model_unassigned", "turn_canceled", "turn_timed_out", "judge_time_limit", "judge_conclusion_failed", "delegated_task_limit", "context_window_unknown", "bad_model", "provider_request_rejected", "model_route_unavailable", "external_cli_invalid_args", "external_cli_model_unavailable", "external_cli_needs_signin", "external_cli_failed", "external_cli_turn_limit", "external_cli_upstream_refused", "provider_error", "agent_request_limit", "tool_assembly_duplicate", "transcript_save_failed", "goal_state_unavailable", "external_cli_not_found", "external_cli_invalid_config", "delegation_not_resumable", "unknown"] as const

/**
 * The sentence a user sees for each code. Exhaustive by construction: codegen
 * aborts if any code lacks an entry, and the `Record<LLMErrorCode, string>`
 * annotation makes a stale catalogue a `tsc -b --noEmit` failure.
 */
export const llmErrorUserMessages: Record<LLMErrorCode, string> = {
  media_unsupported: "This model can’t use that kind of attachment. Try a different file format, or switch to a model that supports it.",
  media_too_large: "An image in this conversation is too large for this model’s size limit, even after Omnipus resized it automatically. Try a smaller image, or open Verbose chat for details.",
  provider_rejected: "The model declined to respond to this request. Try rephrasing it, or switch models.",
  request_too_large: "We built a request that was too large for this model to accept. Try shortening your message, or switch to a model with a larger limit.",
  provider_auth_failed: "The model provider rejected our credentials. Check this provider’s API key in Settings.",
  rate_limited: "The model provider is temporarily overloaded. Wait a moment, then retry.",
  quota_billing: "The model provider says your account is out of credit.",
  network: "We couldn’t reach the model provider. Check your internet connection and retry.",
  provider_stalled: "The model provider stopped responding: nothing arrived for 5 minutes (or the silence limit set for this provider), so the call was ended. Retry — if it keeps happening, open Verbose chat for details.",
  provider_empty_reply: "The model provider ended its answer without sending anything. Retry — if it keeps happening, open Verbose chat for details.",
  content_policy: "The model provider blocked this request under its content policy. Try rephrasing to remove the flagged content.",
  context_too_long: "This turn needed more context than the model can hold, even after trimming older turns automatically. Try a model with a larger context window, or shorten this message.",
  tool_args: "The model filled in a tool’s arguments incorrectly. Retry — Verbose chat shows which tool and what went wrong.",
  tool_call_truncated: "The model's tool call was cut off at its output limit before it finished. Ask for less in one step, or split the work.",
  schema: "We sent the model provider a request it couldn’t process — that’s a bug on our side, not yours. Retry the turn, or open Verbose chat for technical details.",
  agent_not_configured: "This agent isn’t on any workspace yet, so it has nowhere to work. Add it to a workspace team to get started.",
  workspace_unavailable: "This agent’s working folder could not be opened. Check that the disk has space and the folder is writable.",
  model_unavailable: "The model you picked isn’t available for this turn, so this reply used the previous model. Check the model in Settings.",
  model_retired: "This model is no longer offered by its provider. Pick a new model in the agent's settings.",
  needs_provider: "Your sign-in for this provider expired. Sign in again under Settings → Providers.",
  model_unassigned: "This agent has no model. Pick one in the agent's settings.",
  turn_canceled: "This turn was stopped before it finished.",
  turn_timed_out: "The model provider didn’t answer this request in time. Retry — if it keeps happening, open Verbose chat for details.",
  judge_time_limit: "The Judge reached its own check time limit before it finished checking this work. Raise judge.timeout_sec in config.json if checks need longer, then check again.",
  judge_conclusion_failed: "Omnipus could not fully save or deliver the Judge's conclusion. Check disk space, file permissions and the chat's channel connection, then reload the chat. Open Verbose chat for details.",
  delegated_task_limit: "A delegated task reached its configured limit before finishing. Open its task or session, then adjust the named limit if more work is needed.",
  context_window_unknown: "This endpoint did not report a context length for this model. Set it under Settings → Models → Model overrides → Context length.",
  bad_model: "The model configured for this agent is not offered by its provider (bad_model). Pick a new model in the agent's settings.",
  provider_request_rejected: "We sent the model provider a request it rejected (HTTP 400: Bad Request; format). Open Verbose chat for technical details.",
  model_route_unavailable: "The provider for this model could not be set up from its saved settings, so no request was sent. Check the provider's API key and endpoint settings.",
  external_cli_invalid_args: "I couldn't start {cli}: it rejected its own command line, so nothing ran. Check the worker's Additional CLI arguments setting.",
  external_cli_model_unavailable: "I couldn't run the task in {cli}: it couldn't use the model {model}. Set this worker's model to one {cli} supports, in the agent's settings.",
  external_cli_needs_signin: "{cli} is not signed in on the machine it runs on, so the run stopped. Sign in to {cli} there, then retry the task.",
  external_cli_failed: "I couldn't finish the run in {cli}: it stopped with an error I couldn't identify. Retry the task; if it keeps happening, check that {cli} works on its own.",
  external_cli_turn_limit: "I stopped the run in {cli}: it reached its limit of {n} turns for this task. Split the work into smaller steps, or raise the limit in the agent's settings.",
  external_cli_upstream_refused: "The model {cli} uses is limiting requests right now. Try the task again in a moment.",
  provider_error: "This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model.",
  agent_request_limit: "I've reached my limit of model requests for this hour. I can continue in {minutes} minutes, or you can raise the limit in Settings.",
  tool_assembly_duplicate: "I couldn't get my tools ready because two of them have the same name, so this turn didn't run. We don't know why this happened; if it keeps happening, please report it to the Omnipus.ai team.",
  transcript_save_failed: "This turn finished, but I couldn't save its final state. Recent messages may be missing from your history; check the answer above before repeating anything.",
  goal_state_unavailable: "I couldn't read the goal for this conversation, so I didn't continue working on it. Try again in a moment.",
  external_cli_not_found: "I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings.",
  external_cli_invalid_config: "I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings.",
  delegation_not_resumable: "I couldn't continue this delegation in {cli}: its previous conversation can't be resumed. Start a new delegation.",
  unknown: "This turn didn’t finish, and we can’t tell why. Retry — if it keeps happening, open Verbose chat for details, or try a different model.",
}

/** The fault attribution for each code. Exhaustive by construction. */
export const llmErrorUserAttributions: Record<LLMErrorCode, LLMErrorAttribution> = {
  media_unsupported: "model",
  media_too_large: "model",
  provider_rejected: "model",
  request_too_large: "product",
  provider_auth_failed: "config",
  rate_limited: "provider",
  quota_billing: "config",
  network: "ambiguous",
  provider_stalled: "provider",
  provider_empty_reply: "provider",
  content_policy: "provider",
  context_too_long: "product",
  tool_args: "model",
  tool_call_truncated: "model",
  schema: "product",
  agent_not_configured: "config",
  workspace_unavailable: "config",
  model_unavailable: "config",
  model_retired: "config",
  needs_provider: "user",
  model_unassigned: "config",
  turn_canceled: "user",
  turn_timed_out: "provider",
  judge_time_limit: "config",
  judge_conclusion_failed: "ambiguous",
  delegated_task_limit: "config",
  context_window_unknown: "config",
  bad_model: "config",
  provider_request_rejected: "product",
  model_route_unavailable: "config",
  external_cli_invalid_args: "config",
  external_cli_model_unavailable: "config",
  external_cli_needs_signin: "user",
  external_cli_failed: "ambiguous",
  external_cli_turn_limit: "config",
  external_cli_upstream_refused: "ambiguous",
  provider_error: "provider",
  agent_request_limit: "config",
  tool_assembly_duplicate: "ambiguous",
  transcript_save_failed: "ambiguous",
  goal_state_unavailable: "ambiguous",
  external_cli_not_found: "config",
  external_cli_invalid_config: "config",
  delegation_not_resumable: "ambiguous",
  unknown: "unknown",
}

/**
 * The templated variant of the sentence for codes that carry one (the optional
 * `provider_message` sibling in x-user-messages, provider-messages spec §6).
 * A code absent from this map has no template — the catalogue message is the
 * only copy. Slots come from the closed vocabulary enforced by codegen. The
 * Go half is LLMErrorProviderMessages in llm_error_messages.gen.go.
 */
export const llmErrorProviderMessages: Partial<Record<LLMErrorCode, string>> = {
  provider_auth_failed: "{provider} rejected the API key. Check the key in Settings → Providers.",
  rate_limited: "{provider} is busy right now. You can retry the turn.",
  quota_billing: "{provider} says your account is out of credit.",
  model_retired: "This model is no longer offered by {provider}. Pick a new model in the agent's settings.",
  bad_model: "Model {model} is not offered by provider {provider} (bad_model). Pick a new model in the agent's settings.",
  provider_request_rejected: "Provider {provider} rejected the request for model {model} (HTTP 400: Bad Request; format). Open Verbose chat for technical details.",
  model_route_unavailable: "{provider} cannot serve model {model}: no usable connection could be set up from its saved settings, so no request was sent. Check the provider's API key and endpoint settings.",
}

/**
 * Catalogue variants (#1857, notifications spec §13.8): variant key → sentence
 * for the codes whose instance classes need different sentences. A code absent
 * from this map has only its catalogue message. Sentences may carry the closed
 * message slots ({cli} {model} {n} {minutes}). The Go half is
 * LLMErrorMessageVariants in llm_error_messages.gen.go.
 */
export const llmErrorMessageVariants: Partial<Record<LLMErrorCode, Record<string, string>>> = {
  external_cli_failed: {
    start: "I couldn't start {cli}: the program could not be launched. Check the CLI worker's program path in Settings.",
    stream_read: "I couldn't finish the run in {cli}: reading its output failed. Retry the task; if it keeps happening, check that {cli} works on its own.",
  },
  external_cli_upstream_refused: {
    out_of_credit: "{cli}'s model provider account is out of credit. Top up that account to run tasks with {cli}.",
  },
  tool_assembly_duplicate: {
    mcp: "I couldn't get my tools ready: two tools have the same name, and one comes from a connected MCP server. Check this agent's tools and MCP servers.",
  },
  external_cli_invalid_config: {
    no_program: "I couldn't start the coding program: its settings for this agent are missing or invalid. Check the agent's coding program settings.",
  },
  delegation_not_resumable: {
    no_cli: "I couldn't continue this delegation: its previous conversation can't be resumed. Start a new delegation.",
  },
}
