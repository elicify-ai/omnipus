// config_agents.go: Agent configuration: the AgentConfig types and their helpers

package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (m *AgentModelConfig) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		m.Primary = s
		m.Fallbacks = nil
		m.Provider = ""
		return nil
	}
	type raw struct {
		Primary   string   `json:"primary"`
		Fallbacks []string `json:"fallbacks"`
		Provider  string   `json:"provider"`
	}
	var r raw
	if err := json.Unmarshal(data, &r); err != nil {
		return fmt.Errorf("AgentModelConfig.UnmarshalJSON: %w", err)
	}
	m.Primary = r.Primary
	m.Fallbacks = r.Fallbacks
	m.Provider = r.Provider
	return nil
}

func (m AgentModelConfig) MarshalJSON() ([]byte, error) {
	// Emit the bare-string form only when there is nothing but a primary slug —
	// no fallbacks and no explicit provider. Once Provider is set the object form
	// is required so the routing key round-trips (O3).
	if len(m.Fallbacks) == 0 && m.Provider == "" && m.Primary != "" {
		b, err := json.Marshal(m.Primary)
		if err != nil {
			return nil, fmt.Errorf("AgentModelConfig.MarshalJSON: %w", err)
		}
		return b, nil
	}
	type raw struct {
		Primary   string   `json:"primary,omitempty"`
		Fallbacks []string `json:"fallbacks,omitempty"`
		Provider  string   `json:"provider,omitempty"`
	}
	b, err := json.Marshal(raw(m))
	if err != nil {
		return nil, fmt.Errorf("AgentModelConfig.MarshalJSON: %w", err)
	}
	return b, nil
}

// UnmarshalJSON decodes either form (FR-005 + FR-006).
//
// Examples accepted:
//   - ["claude-sonnet-4.6", "gpt-4o-mini"]
//   - [{"model":"claude-sonnet-4.6","provider":"anthropic"}]
//   - ["openrouter/foo", {"model":"claude-sonnet-4.6","provider":"anthropic"}]
//
// Empty / missing / null decodes to a nil slice (semantically identical to
// "no fallback configured").
func (f *FallbackModelSlice) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*f = nil
		return nil
	}

	// 1) Try the homogeneous []FallbackModel form first.
	var objs []FallbackModel
	if err := json.Unmarshal(data, &objs); err == nil {
		*f = objs
		return nil
	}

	// 2) Try []string for the legacy wire form.
	var legacy []string
	if err := json.Unmarshal(data, &legacy); err == nil {
		out := make(FallbackModelSlice, len(legacy))
		for i, s := range legacy {
			out[i] = FallbackModel{Model: s}
		}
		*f = out
		return nil
	}

	// 3) Mixed form: an array of either strings or objects. Walk element by
	// element so we preserve order in a mixed legacy + new payload.
	var raw []json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("fallback_models must be a JSON array of strings or {model, provider} objects: %w", err)
	}
	out := make(FallbackModelSlice, 0, len(raw))
	for i, r := range raw {
		var s string
		if err := json.Unmarshal(r, &s); err == nil {
			out = append(out, FallbackModel{Model: s})
			continue
		}
		var fb FallbackModel
		if err := json.Unmarshal(r, &fb); err != nil {
			return fmt.Errorf("fallback_models[%d]: must be a string or an object: %w", i, err)
		}
		out = append(out, fb)
	}
	*f = out
	return nil
}

// MarshalJSON writes the canonical object form (FR-005). Always emits
// [{"model":"...","provider":"..."}]; the legacy string-only form is never
// emitted on write — loaders see only the normalized object form on
// round-trip.
func (f FallbackModelSlice) MarshalJSON() ([]byte, error) {
	type wire struct {
		Model    string `json:"model"`
		Provider string `json:"provider,omitempty"`
	}
	if len(f) == 0 {
		return []byte("[]"), nil
	}
	out := make([]wire, len(f))
	for i, fb := range f {
		out[i] = wire(fb)
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("FallbackModelSlice.MarshalJSON: %w", err)
	}
	return b, nil
}

// NormalizeFallbacks is the single entry-point used at config load to
// resolve fallback entries that arrived without a provider field (legacy
// strings, or empty-providers on legacy objects). The resolver mirrors the
// chat-side `buildModelListResolver` passthrough logic: any slug that
// matches a configured provider entry is taken verbatim; any slug that
// doesn't match but where a passthrough provider (openrouter, vivgrid)
// is configured is routed through the passthrough provider; otherwise the
// entry is left with an empty provider (the resolver above will fail
// closed at apply time).
//
// Order is preserved (FR-006). nil input → nil output. Already-resolved
// entries (Provider != "") are passed through unchanged.
//
// Traces to: spec §11 Dataset 2 / FR-006 / FR-007.
func NormalizeFallbacks(cfg *Config, in []FallbackModel) []FallbackModel {
	if len(in) == 0 {
		return nil
	}
	out := make([]FallbackModel, len(in))
	for i, fb := range in {
		if strings.TrimSpace(fb.Model) == "" {
			continue // drop empty entries
		}
		if strings.TrimSpace(fb.Provider) != "" {
			out[i] = fb // already resolved
			continue
		}
		// Legacy string entry — resolve provider.
		out[i] = FallbackModel{
			Model:    fb.Model,
			Provider: resolveFallbackProvider(cfg, fb.Model),
		}
	}
	return out
}

// resolveFallbackProvider picks a provider for a fallback model slug when
// the caller didn't pin one. Mirrors the passthrough logic in
// pkg/agent/model_resolution.go::buildModelListResolver (kept duplicated
// here to avoid a config→agent import cycle — pkg/agent already imports
// pkg/config).
//
// Resolution order (mirrors FindModelConfigBySlug's rungs):
//  1. Exact match against any configured provider's Model (the slug)
//     → that provider's Provider field.
//  2. Any configured provider is a passthrough (openrouter / vivgrid) →
//     that passthrough provider.
//  3. Otherwise empty string (apply-time resolver will error out).
//
// The display-alias rung is gone with ModelConfig.ModelName (ADR-067
// FR-013 / X-25): a row is addressed by its (provider, model) pair.
//
// Step 3 cannot call providers.IsPassthroughProvider directly — pkg/providers
// already imports pkg/config, so the reverse direction would be a cycle. The
// check below mirrors that helper's passthrough-name list, but the provider-id
// comparison itself is exact after TrimSpace with no case folding (ADR-067
// FR-036: every provider-id comparison in pkg/agent, pkg/gateway, pkg/providers
// — and this in-package duplicate — MUST be exact). pkg/providers' own copy is
// deliberately case-insensitive on the name (its own doc comment says so) and
// is out of scope here; the two are no longer byte-identical by design.
func resolveFallbackProvider(cfg *Config, slug string) string {
	provider, _ := ResolveSlugProvider(cfg, slug)
	return provider
}

// ResolveSlugProvider resolves the provider a bare model slug would route
// through today, and reports whether that resolution happened only via the
// passthrough rung (rule 3: openrouter / vivgrid). It is the exported face
// of resolveFallbackProvider's rungs, added for ADR-068 FR-012: the
// dependents computation in pkg/gateway (provider_dependents.go) must apply
// the exact same rungs — an agent whose slug exact-matches a provider row is
// a `primary` dependent, one that resolves only through rule 3 is a
// `passthrough` dependent — so the rule lives here once and is consumed
// there, never duplicated.
//
// Returns ("", false) when nothing configured can serve the slug.
func ResolveSlugProvider(cfg *Config, slug string) (provider string, viaPassthrough bool) {
	if cfg == nil {
		return "", false
	}
	slug = strings.TrimSpace(slug)
	if slug == "" {
		return "", false
	}

	// 1: match against what each provider row serves.
	for _, p := range cfg.Providers {
		if p == nil {
			continue
		}
		if strings.TrimSpace(p.Model) == slug {
			return strings.TrimSpace(p.Provider), false
		}
	}
	// 2: passthrough fallback (openrouter, vivgrid).
	for _, p := range cfg.Providers {
		if p == nil {
			continue
		}
		provName := strings.TrimSpace(p.Provider)
		if provName == "openrouter" || provName == "vivgrid" ||
			strings.Contains(strings.ToLower(p.APIBase), "openrouter.ai") {
			return provName, true
		}
	}

	return "", false
}

// MemoryEnabledEffective resolves the memory-injection flag (ADR-052
// FR-039): a non-nil MemoryEnabled wins; nil (the field was never set)
// resolves to true, preserving pre-FR-039 behavior for every agent that
// doesn't opt out.
func (a AgentConfig) MemoryEnabledEffective() bool {
	return a.MemoryEnabled == nil || *a.MemoryEnabled
}

const (
	AgentTypeSystem AgentType = "system"
	AgentTypeCore   AgentType = "core"
	AgentTypeCustom AgentType = "custom"
	// AgentTypeWorker is a sub-agent worker: a depth-limited, ephemeral labor
	// tier invoked ONLY via delegation. A worker is NOT a chat target (it never
	// receives inbound channel messages and is never resolved as the default
	// agent), has no heartbeat, and cannot be marked as the routing default.
	// Workers carry an Executor (Subagents.Executor) selecting native /
	// external-cli / remote-a2a. "A tool you point at work, not a colleague."
	AgentTypeWorker AgentType = "worker"
)

// ResolveType returns the effective agent type. If the Type field is set, it is
// returned directly. Otherwise the type is inferred: known core agent IDs →
// AgentTypeCore; everything else → AgentTypeCustom. The caller must provide
// isCoreAgent to avoid an import cycle with the coreagent package.
func (a AgentConfig) ResolveType(isCoreAgent func(string) bool) AgentType {
	if a.Type != "" {
		return a.Type
	}
	if isCoreAgent != nil && isCoreAgent(a.ID) {
		return AgentTypeCore
	}
	return AgentTypeCustom
}

// IsWorker reports whether this agent is a sub-agent worker (Type==worker).
//
// Worker is an EXPLICIT classification — it is only ever set via the Type field
// (workers are not inferred from an ID list), so the check does not need the
// isCoreAgent resolver and is safe to call without it. A worker is a
// delegation-only labor tier: never a chat target, never the routing default,
// no heartbeat. See AgentTypeWorker.
func (a AgentConfig) IsWorker() bool {
	return a.Type == AgentTypeWorker
}

// IsSystem reports whether this agent is a System Agent (Type==system, ADR-049
// D3) — a seeded, locked, non-privileged internal-LLM agent (e.g. the Judge)
// that executes as a no-tools structured call. System Agents are NOT chat
// targets and are excluded from default-fallback, routing bindings, delegation
// pickers, and team rosters. Like IsWorker, this is an EXPLICIT classification
// carried only via the Type field (System Agents are never inferred from an ID
// list), so the check is safe to call without the isCoreAgent resolver.
func (a AgentConfig) IsSystem() bool {
	return a.Type == AgentTypeSystem
}

// IsChatTarget reports whether this agent may receive inbound channel messages
// and be resolved as the default/routing agent. Every agent kind is a chat
// target EXCEPT a worker and a System Agent. Routing (resolveDefaultAgentID,
// first-enabled fallback) and the default-agent setter/repair use this to
// exclude workers; System Agents are excluded for the same reason (ADR-049 D3):
// the Judge is an out-of-turn internal-LLM agent, never a live persona a user
// can address.
func (a AgentConfig) IsChatTarget() bool {
	return !a.IsWorker() && !a.IsSystem()
}

const (
	// ExecutorKindNative is the default: sub-agent runs inside the Omnipus agent loop.
	ExecutorKindNative ExecutorKind = "native"
	// ExecutorKindExternalCLI drives an external CLI agent (claude-code, codex,
	// opencode) over a JSON-streaming subprocess. ACTIVE in v0.1.0: dispatch resolves
	// it to runner.DispatchKindExternalCLI and runs it worktree-isolated under the
	// CLI's own sandbox (consent best-effort post-hoc — see consent.go).
	ExecutorKindExternalCLI ExecutorKind = "external-cli"
	// ExecutorKindRemoteA2A is reserved. Accepted in schema; rejected at dispatch in v0.1.0.
	ExecutorKindRemoteA2A ExecutorKind = "remote-a2a"
)

// IsExternalCLIWorker reports whether this agent is a subagent_3p — a worker
// that delegates to an external CLI tool (claude-code, codex, opencode, …)
// rather than running on the native Omnipus agent engine.
//
// The predicate is true when BOTH conditions hold:
//  1. Type == AgentTypeWorker (IsWorker() is true).
//  2. Subagents.Executor.Kind == ExecutorKindExternalCLI ("external-cli").
//
// Subagent_3p agents run on a separate engine and their token usage is not
// tracked through Omnipus's provider layer, so they must be excluded from
// token aggregation reports.  This is the single authoritative implementation;
// both rest_stats.go and the get_usage sysagent tool delegate to it via
// (*Config).IsExternalCLIWorkerID.
func (a AgentConfig) IsExternalCLIWorker() bool {
	return a.IsWorker() &&
		a.Subagents != nil &&
		a.Subagents.Executor != nil &&
		a.Subagents.Executor.Kind == ExecutorKindExternalCLI
}

// IsExternalCLIWorkerID reports whether the agent with the given ID is a
// subagent_3p (external CLI worker).  Returns false when agentID is empty,
// when cfg is nil, or when no agent with that ID exists in the config list.
//
// This is the lookup variant used by callers that have a *Config and an agent
// ID string (rest_stats.go, the get_usage sysagent tool) so that neither
// caller needs to inline the two-condition predicate.
func (c *Config) IsExternalCLIWorkerID(agentID string) bool {
	if c == nil || agentID == "" {
		return false
	}
	for i := range c.Agents.List {
		if c.Agents.List[i].ID == agentID {
			return c.Agents.List[i].IsExternalCLIWorker()
		}
	}
	return false
}

// AgentRefKind enumerates the legal values for AgentRef.Kind.
//
// AgentRef.Validate() checks a non-empty value against this set, but ADR-037
// removed AgentRef's last production caller (AgentConfig.DelegationPolicy /
// AgentDefaults.DelegationPolicy no longer exist, and nothing else in the
// runtime constructs a user-supplied AgentRef) — Validate now has no
// production caller at all; it is exercised only by TestAgentRef_Validate.
// AgentRef itself survives as coreagent's compile-time seed-DTO shape
// (config.DelegationPolicy.To — see coreagent.SeedDelegationEdges), which is
// hardcoded Go data, not user input, so there is nothing left to validate at
// load time.
const (
	// AgentRefKindLocal resolves the ref by id within the running instance.
	AgentRefKindLocal = "local"
	// AgentRefKindRemoteA2A is reserved for the future A2A protocol; the kind is
	// accepted by validation but not enforced/dispatched in v0.1.0.
	AgentRefKindRemoteA2A = "remote-a2a"
)

// Validate rejects a non-empty AgentRef.Kind that is outside the known set.
// An empty Kind is accepted for back-compat: callers default an absent kind to
// "local". Only a present-but-unknown value (a typo) is an error, so it fails
// loudly rather than silently downgrading routing.
//
// The Kind is canonicalized (lowercased + trimmed) BEFORE the membership check
// so Validate accepts exactly what the API write path and route.go accept —
// both normalize the kind the same way. Validating the raw value would reject a
// mixed-case/whitespace payload (e.g. {"kind":"Local"}) that the API gate let
// through and that routes fine, bricking the very next config load. Genuinely-
// unknown values (e.g. "robot") are still rejected.
func (r AgentRef) Validate() error {
	switch strings.ToLower(strings.TrimSpace(r.Kind)) {
	case "", AgentRefKindLocal, AgentRefKindRemoteA2A:
		return nil
	default:
		return fmt.Errorf("invalid agent ref kind %q (want %q or %q)",
			r.Kind, AgentRefKindLocal, AgentRefKindRemoteA2A)
	}
}

// EffectiveKind returns the ExecutorKind with nil-safe defaulting to native.
func (ec *ExecutorConfig) EffectiveKind() ExecutorKind {
	if ec == nil || ec.Kind == "" {
		return ExecutorKindNative
	}
	return ec.Kind
}

// GetIdleTimeoutMinutes returns the idle timeout, defaulting to 30.
func (d *AgentDefaults) GetIdleTimeoutMinutes() int {
	if d.IdleTimeoutMinutes <= 0 {
		return 30
	}
	return d.IdleTimeoutMinutes
}

// GetBootstrapRecapMaxPerMinute returns the rate limit, defaulting to 5.
func (d *AgentDefaults) GetBootstrapRecapMaxPerMinute() int {
	if d.BootstrapRecapMaxPerMinute <= 0 {
		return 5
	}
	return d.BootstrapRecapMaxPerMinute
}

const DefaultMaxMediaSize = 20 * 1024 * 1024

// 20 MB

func (d *AgentDefaults) GetMaxMediaSize() int {
	if d.MaxMediaSize > 0 {
		return d.MaxMediaSize
	}
	return DefaultMaxMediaSize
}

// GetToolFeedbackMaxArgsLength returns the max args preview length for tool feedback messages.
func (d *AgentDefaults) GetToolFeedbackMaxArgsLength() int {
	if d.ToolFeedback.MaxArgsLength > 0 {
		return d.ToolFeedback.MaxArgsLength
	}
	return 300
}

// IsToolFeedbackEnabled returns true when tool feedback messages should be sent to the chat.
func (d *AgentDefaults) IsToolFeedbackEnabled() bool {
	return d.ToolFeedback.Enabled
}

// IsZero reports whether no default model is set (no model half).
func (d DefaultModel) IsZero() bool {
	return strings.TrimSpace(d.Model) == ""
}

// String renders the pair as "provider/model" ("model" alone when the
// provider half is empty) for logs and error text.
func (d DefaultModel) String() string {
	if d.IsZero() {
		return ""
	}
	if p := strings.TrimSpace(d.Provider); p != "" {
		return p + "/" + strings.TrimSpace(d.Model)
	}
	return strings.TrimSpace(d.Model)
}
