// Package effortdialect is the effort encoder table of the effort-levels
// feature (effort-levels-spec-r10.md, "Integration Boundaries > LLM
// providers", FR-037, FR-037a, FR-049):
//
//	one encoder table keyed by (protocol, provider id, model family); levels
//	and switch encodings only; no budget encoder. The same table filters the
//	served catalog (FR-049).
//
// A lookup tries the exact (protocol, provider, family) row first, then the
// provider's row for every family (ModelFamily ""), then the family's row for
// every provider of the protocol (ProviderID ""), then the protocol's row for
// every provider and family. A route with no row at any level has no encoder
// ("other Bedrock families"): nothing is sent and the served object is muted.
package effortdialect

import (
	"slices"
	"strings"
	"sync"
)

// EffortOptionKey is the internal request option that carries the resolved
// effort value ("high", "none", "on", "off") from the per-candidate resolver
// to the adapter. Mirrors the existing options["max_tokens"] convention.
const EffortOptionKey = "reasoning_effort"

// Wire protocols of the encoder table.
const (
	ProtocolOpenAIChat        = "openai-chat"
	ProtocolOpenAIResponses   = "openai-responses"
	ProtocolAnthropicMessages = "anthropic-messages"
	ProtocolBedrockConverse   = "bedrock-converse"
)

// Route identifies one encoder row: the provider protocol, the provider id,
// and the model family (the catalog base id, not a profile-id prefix).
type Route struct {
	Protocol    string // e.g. "openai-chat", "openai-responses", "anthropic-messages", "bedrock-converse"
	ProviderID  string // e.g. "openrouter", "deepseek", "z-ai", "anthropic", "azure", "bedrock", "openai-chatgpt"
	ModelFamily string // family/base id of the model, e.g. "deepseek-v4.1-flash", "minimax-m3", "claude"
}

// Encoding is what a route can encode: levels, the on/off switch, or both.
// No budget encoding exists (FR-037a; the served catalog carries none).
type Encoding struct {
	Levels bool
	Switch bool
}

// Table is the encoder table. The zero value has no rows.
type Table struct {
	mu   sync.RWMutex
	rows map[Route]Encoding
}

// Register adds one route's encoding. A route with an empty ModelFamily
// applies to every family of that provider; a route with an empty ProviderID
// and ModelFamily applies to every provider of that protocol.
func (t *Table) Register(r Route, e Encoding) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.rows == nil {
		t.rows = make(map[Route]Encoding)
	}
	t.rows[normalizeRoute(r)] = e
}

// EncodingFor reports what the route can encode. The second return is false
// when the route has no encoder row ("other Bedrock families" etc.).
func (t *Table) EncodingFor(r Route) (Encoding, bool) {
	if t == nil {
		return Encoding{}, false
	}
	r = normalizeRoute(r)
	t.mu.RLock()
	defer t.mu.RUnlock()
	for _, k := range []Route{
		r,
		{Protocol: r.Protocol, ProviderID: r.ProviderID},
		{Protocol: r.Protocol, ModelFamily: r.ModelFamily},
		{Protocol: r.Protocol},
	} {
		if e, ok := t.rows[k]; ok {
			return e, true
		}
	}
	return Encoding{}, false
}

func normalizeRoute(r Route) Route {
	return Route{
		Protocol:    strings.ToLower(strings.TrimSpace(r.Protocol)),
		ProviderID:  strings.ToLower(strings.TrimSpace(r.ProviderID)),
		ModelFamily: strings.ToLower(strings.TrimSpace(r.ModelFamily)),
	}
}

var (
	defaultOnce  sync.Once
	defaultTable *Table
)

// Default returns the process encoder table: one registration per route of
// the spec's "Provider request forms" (S-17, S-18).
func Default() *Table {
	defaultOnce.Do(func() {
		t := &Table{}
		levels := Encoding{Levels: true}
		both := Encoding{Levels: true, Switch: true}
		// Other OpenAI-compatible rows: flat reasoning_effort levels, no switch (FR-037a).
		t.Register(Route{Protocol: ProtocolOpenAIChat}, levels)
		// OpenAI chat: flat reasoning_effort; Off is the level "none" where offered.
		t.Register(Route{Protocol: ProtocolOpenAIChat, ProviderID: "openai"}, levels)
		// OpenRouter: reasoning {effort} and reasoning {enabled}.
		t.Register(Route{Protocol: ProtocolOpenAIChat, ProviderID: "openrouter"}, both)
		t.Register(Route{Protocol: ProtocolOpenAIChat, ProviderID: "openrouter", ModelFamily: "minimax-m3"}, Encoding{Switch: true})
		// DeepSeek native, Z.ai native: reasoning_effort levels, thinking {type} switch.
		t.Register(Route{Protocol: ProtocolOpenAIChat, ProviderID: "deepseek"}, both)
		t.Register(Route{Protocol: ProtocolOpenAIChat, ProviderID: "z-ai"}, both)
		// ChatGPT login (Codex Responses): reasoning {effort} levels only.
		t.Register(Route{Protocol: ProtocolOpenAIResponses, ProviderID: "openai-chatgpt"}, levels)
		// Native Anthropic and Anthropic-protocol rows: adaptive thinking +
		// output_config.effort; thinking disabled / adaptive switch.
		t.Register(Route{Protocol: ProtocolAnthropicMessages}, both)
		// Bedrock (any Bedrock provider row): Anthropic and Nova families only
		// (family from the catalog base id).
		t.Register(Route{Protocol: ProtocolBedrockConverse, ModelFamily: "claude"}, levels)
		t.Register(Route{Protocol: ProtocolBedrockConverse, ModelFamily: "nova"}, levels)
		defaultTable = t
	})
	return defaultTable
}

// FilterServed returns the served reasoning capability for a model on a route:
// the levels and switch the route can actually encode (FR-049). The served
// levels are never nil ("levels": [] on the wire); a route with no encoder row
// serves the muted object (no levels, no switch).
func FilterServed(levels []string, hasSwitch bool, r Route) (servedLevels []string, servedSwitch bool) {
	return Default().FilterServed(levels, hasSwitch, r)
}

// FilterServed is the table-scoped form of the package-level FilterServed.
func (t *Table) FilterServed(levels []string, hasSwitch bool, r Route) (servedLevels []string, servedSwitch bool) {
	servedLevels = []string{}
	enc, ok := t.EncodingFor(r)
	if !ok {
		return servedLevels, false
	}
	if enc.Levels {
		for _, l := range levels {
			if l != "" && !slices.Contains(servedLevels, l) {
				servedLevels = append(servedLevels, l)
			}
		}
	}
	return servedLevels, enc.Switch && hasSwitch
}

// ProtocolForCatalog maps a catalog provider's protocol and id onto the
// encoder protocol its adapter speaks.
func ProtocolForCatalog(providerID, catalogProtocol string) string {
	if strings.EqualFold(providerID, "openai-chatgpt") {
		return ProtocolOpenAIResponses
	}
	switch strings.ToLower(catalogProtocol) {
	case "anthropic":
		return ProtocolAnthropicMessages
	case "bedrock":
		return ProtocolBedrockConverse
	case "openai-compatible", "":
		return ProtocolOpenAIChat
	}
	return strings.ToLower(catalogProtocol)
}

// FamilyOf returns the model family the table is keyed by: the catalog base
// id's last path segment, and for Bedrock the vendor family ("claude" for
// Anthropic models, "nova" for Amazon Nova), never a cross-region inference
// profile prefix.
func FamilyOf(protocol, modelID string) string {
	id := strings.ToLower(strings.TrimSpace(modelID))
	if i := strings.LastIndex(id, "/"); i >= 0 {
		id = id[i+1:]
	}
	if protocol == ProtocolBedrockConverse {
		switch {
		case strings.Contains(id, "anthropic.") || strings.Contains(id, "claude"):
			return "claude"
		case strings.Contains(id, "nova"):
			return "nova"
		}
	}
	return id
}

// RouteFor builds the encoder route for a provider, its catalog protocol and
// a model id.
func RouteFor(providerID, catalogProtocol, modelID string) Route {
	p := ProtocolForCatalog(providerID, catalogProtocol)
	return Route{Protocol: p, ProviderID: providerID, ModelFamily: FamilyOf(p, modelID)}
}
