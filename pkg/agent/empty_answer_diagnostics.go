// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"github.com/elicify-ai/omnipus/pkg/providers"
)

// emptyAnswerLogFields builds the ONE warn line logged when an empty answer
// survives the turn's retry and the turn ends on defaultResponse (provider
// hotfix H5, founder decision 2026-10-13): what actually came back, so the
// next empty-answer defect is diagnosable from the log alone. An empty
// answer is far more likely our own parsing defect than a provider outage,
// so it is reported, never silently routed to a fallback model.
//
// Content-free by construction: provider and model ids, the finish reason,
// token counts and the provider's stream-shape summary
// (providers.ResponseDiagnostics — event types with counts, output item
// types, output source, status, incomplete reason). Never message text,
// tool arguments or credentials.
func emptyAnswerLogFields(
	agentID string, iteration int,
	answeredProvider, answeredModel, requestedModel string,
	resp *providers.LLMResponse,
) map[string]any {
	// The answering provider is the one the call site stamped from the
	// attempt that actually ran — never looked up by model id, because the
	// same model can be a candidate on two providers. "unknown" when no
	// call site stamped one.
	provider := answeredProvider
	if provider == "" {
		provider = "unknown"
	}
	model := answeredModel
	if model == "" {
		model = requestedModel
	}
	fields := map[string]any{
		"agent_id":  agentID,
		"iteration": iteration,
		"provider":  provider,
		"model":     model,
	}
	if resp == nil {
		fields["response"] = "nil"
		return fields
	}
	fields["finish_reason"] = resp.FinishReason
	fields["tool_calls"] = len(resp.ToolCalls)
	if u := resp.Usage; u != nil {
		fields["tokens_in"] = u.PromptTokens
		fields["tokens_out"] = u.CompletionTokens
		fields["tokens_total"] = u.TotalTokens
	} else {
		fields["usage"] = "unreported"
	}
	d := resp.Diagnostics
	if d == nil {
		fields["stream_diagnostics"] = "unavailable for this provider"
		return fields
	}
	fields["stream_events"] = d.EventCounts
	fields["output_item_types"] = d.OutputItemTypes
	fields["output_source"] = d.OutputSource
	fields["status"] = d.Status
	fields["incomplete_reason"] = d.IncompleteReason
	return fields
}
