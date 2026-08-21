package routing

import (
	"regexp"
	"strings"
)

const (
	// DefaultMainKey is the session-key SUFFIX for an agent's main (non-peer)
	// session: "agent:<id>:main". It is NOT the retired "main" sentinel agent
	// and never was — it only shares the word. It stays because session keys
	// are a naming scheme, not an identity.
	DefaultMainKey = "main"

	DefaultAccountID = "default"
	MaxAgentIDLength = 64
)

var (
	validIDRe      = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)
	invalidCharsRe = regexp.MustCompile(`[^a-z0-9_-]+`)
	leadingDashRe  = regexp.MustCompile(`^-+`)
	trailingDashRe = regexp.MustCompile(`-+$`)
)

// NormalizeAgentID sanitizes an agent ID to [a-z0-9][a-z0-9_-]{0,63}.
// Invalid characters are collapsed to "-". Leading/trailing dashes stripped.
//
// Empty input returns EMPTY. It used to return the "main" sentinel, which
// quietly turned "nobody was named" into "this specific agent" at 26 call
// sites — the sentinel's main route into persisted user data. With the
// sentinel gone there is no honest name to invent here: this function
// sanitizes a string and cannot see config, so it cannot know the seeded
// default. Callers that need a default must resolve it themselves and handle
// an empty id explicitly rather than being handed one silently.
func NormalizeAgentID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if validIDRe.MatchString(lower) {
		return lower
	}
	result := invalidCharsRe.ReplaceAllString(lower, "-")
	result = leadingDashRe.ReplaceAllString(result, "")
	result = trailingDashRe.ReplaceAllString(result, "")
	if len(result) > MaxAgentIDLength {
		result = result[:MaxAgentIDLength]
	}
	if result == "" {
		return ""
	}
	return result
}

// NormalizeAccountID sanitizes an account ID. Empty returns DefaultAccountID.
func NormalizeAccountID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return DefaultAccountID
	}
	lower := strings.ToLower(trimmed)
	if validIDRe.MatchString(lower) {
		return lower
	}
	result := invalidCharsRe.ReplaceAllString(lower, "-")
	result = leadingDashRe.ReplaceAllString(result, "")
	result = trailingDashRe.ReplaceAllString(result, "")
	if len(result) > MaxAgentIDLength {
		result = result[:MaxAgentIDLength]
	}
	if result == "" {
		return DefaultAccountID
	}
	return result
}
