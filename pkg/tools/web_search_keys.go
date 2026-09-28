package tools

// web_search_keys.go — gate round 1 finding K1: the D4a live key resolver.
//
// The ADR-096 dynamic tool reads provider keys at CALL time through the same
// live config the usability test reads (WebSearchToolOptions.Roles →
// config.WebToolsConfig.<Provider>.APIKey(), an os.Getenv read), so a key
// that appears after the tool was built makes that provider callable without
// re-registration (AC-16, spec test 43). The construction snapshot — the
// WebSearchToolOptions key fields — stays as the fallback for the legacy
// path (Roles nil) and direct constructions. A provider whose effective key
// set is empty is "not usable" (D16 vocabulary, final class), never the
// "all api keys failed, last error: %!w(<nil>)" wrapper that classified as a
// hop.

import "sync/atomic"

// singleKeyList mirrors pkg/agent/loop.go's braveKeys/tavilyKeys/perplexityKeys
// (pkg/tools cannot import pkg/agent): "" → nil, else a one-key list.
func singleKeyList(key string) []string {
	if key == "" {
		return nil
	}
	return []string{key}
}

// rotatedSearchKeys returns keys in this call's trial order, advancing a
// per-provider round-robin so successive calls start on successive keys —
// the same cross-call rotation APIKeyPool.NewIterator gave the construction
// snapshot. The counter is per-provider state; keys must be non-empty.
func rotatedSearchKeys(counter *uint32, keys []string) []string {
	n := len(keys)
	start := int(atomic.AddUint32(counter, 1)-1) % n
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, keys[(start+i)%n])
	}
	return out
}

// snapshotKeys returns a pool's key slice (nil-safe).
func snapshotKeys(pool *APIKeyPool) []string {
	if pool == nil {
		return nil
	}
	return pool.keys
}

// errNoAPIKey is the D16 vocabulary for a provider whose effective key set
// is empty: "no API key", final — hopClass false, so the ladder reports
// "not usable" and never hops on it.
func errNoAPIKey() *searchProviderError {
	return &searchProviderError{class: classNotUsable, msg: "no API key"}
}

// currentKeys returns this call's Tavily key list in trial order, or nil
// when the effective key set is empty: the live resolver's keys (D4a, read
// at call time) win over the construction snapshot when it yields any.
func (p *TavilySearchProvider) currentKeys() []string {
	keys := snapshotKeys(p.keyPool)
	if p.keySource != nil {
		if live := p.keySource(); len(live) > 0 {
			keys = live
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return rotatedSearchKeys(&p.rotation, keys)
}

// currentKeys is the Perplexity twin of Tavily's (see above).
func (p *PerplexitySearchProvider) currentKeys() []string {
	keys := snapshotKeys(p.keyPool)
	if p.keySource != nil {
		if live := p.keySource(); len(live) > 0 {
			keys = live
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return rotatedSearchKeys(&p.rotation, keys)
}

// currentKeys is the Brave twin of Tavily's (see above).
func (p *BraveSearchProvider) currentKeys() []string {
	keys := snapshotKeys(p.keyPool)
	if p.keySource != nil {
		if live := p.keySource(); len(live) > 0 {
			keys = live
		}
	}
	if len(keys) == 0 {
		return nil
	}
	return rotatedSearchKeys(&p.rotation, keys)
}

// currentKey returns this call's GLM key: the live resolver's key (D4a,
// read at call time) wins over the construction snapshot when it yields
// one; "" when the effective key is empty.
func (p *GLMSearchProvider) currentKey() string {
	if p.keySource != nil {
		if k := p.keySource(); k != "" {
			return k
		}
	}
	return p.apiKey
}

// currentKey is the Baidu twin of GLM's (see above).
func (p *BaiduSearchProvider) currentKey() string {
	if p.keySource != nil {
		if k := p.keySource(); k != "" {
			return k
		}
	}
	return p.apiKey
}

// currentKey is the Exa twin of GLM's (see above).
func (p *ExaSearchProvider) currentKey() string {
	if p.keySource != nil {
		if k := p.keySource(); k != "" {
			return k
		}
	}
	return p.apiKey
}
