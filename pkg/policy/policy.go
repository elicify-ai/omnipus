// Package policy implements the declarative security policy engine for Omnipus.
//
// It handles SEC-04 (tool allow/deny), SEC-05 (per-binary exec control),
// SEC-07 (deny-by-default), SEC-11 (JSON policy files), SEC-12 (static policies),
// and SEC-17 (explainable decisions).
//
// Policies are loaded once at startup from the security section of config.json
// and are immutable after initialization. Concurrent reads are safe without locking.
package policy

// DefaultPolicy is a named type for the security default policy value.
type DefaultPolicy string

const (
	PolicyAllow DefaultPolicy = "allow"
	PolicyDeny  DefaultPolicy = "deny"
)

// Decision represents the outcome of a policy evaluation.
type Decision struct {
	Allowed    bool   // Whether the action is permitted
	Policy     string // Resolved policy value: "allow", "ask", or "deny"
	PolicyRule string // Human-readable explanation of which rule matched (SEC-17)
}

// ExecPolicy defines exec tool policy.
type ExecPolicy struct {
	AllowedBinaries []string `json:"allowed_binaries,omitempty"`
	Approval        string   `json:"approval,omitempty"`
}

// PolicySection groups sub-policies (exec).
type PolicySection struct {
	Exec ExecPolicy `json:"exec,omitempty"`
}

// PromptGuardConfig configures prompt injection defenses (SEC-25).
// Field path in config.json: security.prompt_guard.strictness
type PromptGuardConfig struct {
	// Strictness controls how aggressively untrusted content is sanitized.
	// Valid values: "low", "medium", "high". Default is "medium".
	Strictness string `json:"strictness,omitempty"`
}

// SecurityConfig is the primary security configuration type.
type SecurityConfig struct {
	DefaultPolicy DefaultPolicy     `json:"default_policy,omitempty"`
	Policy        PolicySection     `json:"policy,omitempty"`
	PromptGuard   PromptGuardConfig `json:"prompt_guard,omitempty"`
}

// IsSystemAgent returns true if the agent type is a privileged (core or system)
// agent, which is exempt from rate limits and certain policy restrictions.
// Privileges flow from agent type, not from a hardcoded agent ID (FR-045).
func IsSystemAgent(agentType string) bool {
	return agentType == "core" || agentType == "system"
}
