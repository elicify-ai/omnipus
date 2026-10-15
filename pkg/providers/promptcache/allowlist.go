package promptcache

// CacheTransport is what a catalog provider may be sent so the host can
// cache. Every value that carries an identity is the session id, never the
// agent id, and never a secret. The skeleton sends nothing.
type CacheTransport struct {
	// SendPromptCacheKey adds the body field prompt_cache_key.
	SendPromptCacheKey bool
	// SessionHeader is a header name whose value is the session id
	// (for example x-grok-conv-id or x-session-affinity). Empty means no header.
	SessionHeader string
	// SendBodySessionID adds OpenRouter's body field session_id.
	SendBodySessionID bool
	// SendEphemeralCacheControl adds a manual cache_control mark of type
	// ephemeral. Used for a Vercel Claude model, OpenRouter, and Alibaba.
	// It is not the Vercel caching:auto rewrite.
	SendEphemeralCacheControl bool
}

// CacheTransportFor decides which cache key, header, or manual mark a catalog
// provider id may receive. modelID distinguishes a Vercel Claude model from
// an implicit upstream on the same provider row. The skeleton returns the
// zero decision, which sends nothing.
func CacheTransportFor(providerID, modelID string) CacheTransport {
	_, _ = providerID, modelID
	return CacheTransport{}
}
