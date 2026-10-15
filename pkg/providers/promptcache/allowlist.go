package promptcache

import "strings"

// CacheTransport is the documented cache hint for a catalog provider. Every
// identity it carries is a session id, never an agent id or a credential.
type CacheTransport struct {
	SendPromptCacheKey        bool
	SessionHeader             string
	SendBodySessionID         bool
	SendEphemeralCacheControl bool
}

// CacheTransportFor is an allow-list, not a best-effort probe. Unknown hosts
// and implicit-only providers receive no additional request fields.
func CacheTransportFor(providerID, modelID string) CacheTransport {
	var t CacheTransport
	switch providerID {
	case "openai", "mistral", "meta", "fireworks-ai", "togetherai", "cerebras", "deepinfra", "volcengine":
		t.SendPromptCacheKey = true
	case "vercel":
		t.SendPromptCacheKey = true
		m := strings.ToLower(modelID)
		t.SendEphemeralCacheControl = strings.HasPrefix(m, "anthropic/claude") || strings.HasPrefix(m, "claude")
	case "xai":
		t.SessionHeader = "x-grok-conv-id"
	case "baseten", "cloudflare-workers-ai", "sarvam":
		t.SessionHeader = "x-session-affinity"
	case "openrouter":
		t.SendBodySessionID = true
		t.SendEphemeralCacheControl = true
	case "alibaba", "alibaba-cn":
		t.SendEphemeralCacheControl = true
	}
	return t
}
