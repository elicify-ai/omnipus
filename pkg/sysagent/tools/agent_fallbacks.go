package systools

import (
	"unicode/utf8"

	"github.com/elicify-ai/omnipus/pkg/agentmutation"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// fallbackModelsParameters mirrors the shared FallbackModel REST contract.
// Each entry retains its provider; a string-only chain cannot express it.
func fallbackModelsParameters() map[string]any {
	return map[string]any{
		"type": "array", "maxItems": 2,
		"description": "Ordered fallback model/provider objects. At most two; [] clears the chain. Unsupported for external CLI workers.",
		"items": map[string]any{"type": "object", "additionalProperties": false, "required": []string{"model"}, "properties": map[string]any{
			"model":    map[string]any{"type": "string", "maxLength": 256},
			"provider": map[string]any{"type": "string", "maxLength": 64},
		}},
	}
}

func applyFallbackModels(a *config.AgentConfig, raw any) error {
	invalid := func(reason string) error {
		return &agentmutation.FieldError{Code: agentmutation.InvalidInput, Fields: []string{"fallback_models"}, Reason: reason}
	}
	if a.IsExternalCLIWorker() {
		return invalid("external CLI workers manage their own retries")
	}
	values, ok := raw.([]any)
	if !ok || len(values) > 2 {
		return invalid("must be an array of at most two model/provider objects; use [] to clear")
	}
	chain := make(config.FallbackModelSlice, 0, len(values))
	for _, value := range values {
		fields, ok := value.(map[string]any)
		if !ok {
			return invalid("each entry must be a model/provider object")
		}
		for key := range fields {
			if key != "model" && key != "provider" {
				return invalid("entries accept only model and provider")
			}
		}
		model, ok := fields["model"].(string)
		if !ok || utf8.RuneCountInString(model) > 256 {
			return invalid("model is required and must be a string of at most 256 characters")
		}
		provider := ""
		if value, present := fields["provider"]; present {
			provider, ok = value.(string)
			if !ok || utf8.RuneCountInString(provider) > 64 {
				return invalid("provider must be a string of at most 64 characters")
			}
		}
		chain = append(chain, config.FallbackModel{Model: model, Provider: provider})
	}
	a.FallbackModels = chain
	// The legacy field is not the routing chain. Clear it when replacing the
	// canonical chain so future normalization cannot resurrect stale entries.
	if a.Model != nil {
		a.Model.Fallbacks = nil
	}
	return nil
}
