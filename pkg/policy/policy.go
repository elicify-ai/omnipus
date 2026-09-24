// Package policy implements the surviving pieces of the Omnipus security
// policy engine: SEC-25 prompt-injection defense configuration and the
// approval-saturation cap (see saturation.go). The exec binary allowlist
// (formerly SEC-05) was retired under ADR-092 — bash's own per-mode
// escalation machinery (pkg/tools/shell_permission_mode.go, pkg/agent/
// loop_policy.go) and the compositor's allow/ask/deny tool-policy gate
// (pkg/tools/compositor.go) are the live enforcement paths for command
// execution.
package policy

// PromptGuardConfig configures prompt injection defenses (SEC-25).
// Field path in config.json: security.prompt_guard.strictness
type PromptGuardConfig struct {
	// Strictness controls how aggressively untrusted content is sanitized.
	// Valid values: "low", "medium", "high". Default is "medium".
	Strictness string `json:"strictness,omitempty"`
}

// IsSystemAgent returns true if the agent type is a privileged (core or system)
// agent, which is exempt from rate limits and certain policy restrictions.
// Privileges flow from agent type, not from a hardcoded agent ID (FR-045).
func IsSystemAgent(agentType string) bool {
	return agentType == "core" || agentType == "system"
}
