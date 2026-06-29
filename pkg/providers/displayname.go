// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import "strings"

// knownDisplayNames maps lowercase provider IDs to their branded display names.
// Used by DisplayName to produce human-readable names in FR-7 validation messages.
var knownDisplayNames = map[string]string{
	"openrouter": "OpenRouter",
	"openai":     "OpenAI",
	"anthropic":  "Anthropic",
	"gemini":     "Google Gemini",
	"google":     "Google Gemini",
	"groq":       "Groq",
	"deepseek":   "DeepSeek",
	"zhipu":      "Zhipu AI",
	"z-ai":       "Z.AI",
	"moonshot":   "Moonshot",
	"azure":      "Azure OpenAI",
}

// DisplayName returns the branded display name for the given provider ID (e.g.
// "openrouter" → "OpenRouter"). The single source of truth for FR-7 user-facing
// messages in the gateway (used at all three ValidateKey call sites).
//
// Falls back to title-casing the first letter of the providerID for unknown
// providers so messages are still readable without a curated entry.
func DisplayName(providerID string) string {
	pid := strings.ToLower(strings.TrimSpace(providerID))
	if name, ok := knownDisplayNames[pid]; ok {
		return name
	}
	// Title-case fallback for unknown providers.
	if len(providerID) == 0 {
		return providerID
	}
	return strings.ToUpper(providerID[:1]) + providerID[1:]
}
