package providers

import "strings"

// ModelRef represents a parsed model reference with provider and model name.
type ModelRef struct {
	Provider string
	Model    string
}

// ParseModelRef parses "anthropic/claude-opus" into {Provider: "anthropic", Model: "claude-opus"}.
// If no slash present, uses defaultProvider.
// Returns nil for empty input.
func ParseModelRef(raw string, defaultProvider string) *ModelRef {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	if idx := strings.Index(raw, "/"); idx > 0 {
		provider := NormalizeProvider(raw[:idx])
		model := strings.TrimSpace(raw[idx+1:])
		if model == "" {
			return nil
		}
		return &ModelRef{Provider: provider, Model: model}
	}

	return &ModelRef{
		Provider: NormalizeProvider(defaultProvider),
		Model:    raw,
	}
}

// NormalizeProvider trims a provider identifier and returns it unchanged
// otherwise.
//
// It used to be an alias table — `z.ai`/`z-ai` → `zai`, three `qwen-*`
// spellings collapsed into one, `google` → `gemini`, and a dozen more. That
// table was the last rename mechanism in the binary, and ADR-067's greenfield
// rule removed every one of them (FR-011, SC-009): provider ids are the
// registry's, compared EXACTLY after trimming, never case-folded (A-19). The
// function survives only because dedup keys and refs read cleaner with one
// named trim than with a bare strings.TrimSpace at each site.
func NormalizeProvider(provider string) string {
	return strings.TrimSpace(provider)
}

// ModelKey returns a canonical "provider/model" key for deduplication.
func ModelKey(provider, model string) string {
	return NormalizeProvider(provider) + "/" + strings.ToLower(strings.TrimSpace(model))
}
