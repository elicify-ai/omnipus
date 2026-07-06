package agent

import (
	"github.com/elicify-ai/omnipus/pkg/agent/envcontext"
)

// environmentProvider returns operator-facing runtime state (paths, sandbox
// mode, active warnings) that the env preamble renders. Injected via
// WithEnvironmentProvider; nil by default so legacy callers see no preamble.
type contextBuilderEnv struct {
	provider envcontext.Provider
}

// WithEnvironmentProvider wires an envcontext.Provider into the builder so
// BuildSystemPrompt renders the ## Environment preamble as parts[0]. Fix A
// (FR-057). Subagents share the same ContextBuilder pointer as their parent,
// so any provider set here is inherited automatically (see subturn.go).
func (cb *ContextBuilder) WithEnvironmentProvider(p envcontext.Provider) *ContextBuilder {
	cb.env.provider = p
	return cb
}

// EnvironmentProvider returns the currently-configured provider (may be nil).
// Exposed for subturn inheritance assertions and test doubles.
func (cb *ContextBuilder) EnvironmentProvider() envcontext.Provider {
	return cb.env.provider
}

// GetEnvironmentContext renders the env preamble for the current process/
// agent. Returns an empty string when no provider is wired, which preserves
// legacy behavior and makes the parts[0] insertion a no-op in tests that do
// not exercise env awareness. The real body is installed by lane E via
// envcontext.Render.
func (cb *ContextBuilder) GetEnvironmentContext() string {
	if cb.env.provider == nil {
		return ""
	}
	return envcontext.Render(cb.env.provider, cb.workspace)
}
