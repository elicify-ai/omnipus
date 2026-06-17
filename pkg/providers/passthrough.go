// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import "strings"

// IsPassthroughProvider reports whether the given provider type forwards
// arbitrary model slugs to its backend without per-slug registration.
// OpenRouter is the canonical example; vivgrid follows the same pattern.
//
// The check is case-insensitive on provider name and falls back to sniffing
// the APIBase URL for "openrouter.ai" — a provider entry that points at the
// OpenRouter base URL without setting Provider="openrouter" still counts.
//
// This helper is the single source of truth for passthrough detection;
// byte-identical copies previously lived in pkg/config/config.go and
// pkg/agent/model_resolution.go, both deleted in favour of this one (the
// pkg/config copy in Wave 2, the pkg/agent copy in Wave 4 cleanup).
//
// Note: pkg/config cannot import this package (providers → config is the
// existing import direction; reversing it would be a cycle). The single
// in-package use of this helper from pkg/config inlines a 3-line equivalent
// check with a clear pointer comment.
func IsPassthroughProvider(provider, apiBase string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openrouter", "vivgrid":
		return true
	}
	return strings.Contains(strings.ToLower(apiBase), "openrouter.ai")
}
