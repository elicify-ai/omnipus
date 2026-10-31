// Package effortdialect — RED SCAFFOLDING for the effort-levels feature
// (train T7, branch landing/effort). Spec:
// /workspace/omnipus-coordination/designs-20261029/effort-levels-spec-r10.md
// ("Integration Boundaries > LLM providers", FR-037a, FR-049):
//
//	Contract: one encoder table keyed by (protocol, provider id, model family)
//	in pkg/providers/effortdialect; levels and switch encodings only; no budget
//	encoder. The same table filters the served catalog (FR-049).
//
// GREEN replaces this stub with the real table (one registration per route of
// S-17/S-18) and makes the adapters read it. The RED tests pin the table API
// and the served-catalog filter; the adapter wire tests capture serialised
// bodies through in-process HTTP servers and need no scaffold. The stub
// registry is empty, so every lookup fails until the real routes register.
package effortdialect

// EffortOptionKey is the internal request option that carries the resolved
// effort value ("high", "none", "on", "off") from the per-candidate resolver
// to the adapter. Mirrors the existing options["max_tokens"] convention.
const EffortOptionKey = "reasoning_effort"

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
	rows map[Route]Encoding
}

// Default returns the process encoder table. RED scaffold: empty.
func Default() *Table { return &Table{} }

// Register adds one route's encoding. RED scaffold: stores into an unbacked
// map so the lookup below still reports nothing sendable until GREEN lands.
func (t *Table) Register(r Route, e Encoding) {
	_ = r
	_ = e
}

// EncodingFor reports what the route can encode. The second return is false
// when the route has no encoder row ("other Bedrock families" etc.).
func (t *Table) EncodingFor(r Route) (Encoding, bool) {
	_ = r
	return Encoding{}, false
}

// FilterServed returns the served reasoning capability for a model on a route:
// the levels and switch the route can actually encode. GREEN implements the
// FR-049 filter the served catalog uses. RED scaffold: reports nothing
// sendable for every input.
func FilterServed(levels []string, hasSwitch bool, r Route) (servedLevels []string, servedSwitch bool) {
	_ = levels
	_ = hasSwitch
	_ = r
	return nil, false
}
