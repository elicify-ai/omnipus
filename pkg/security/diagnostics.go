// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package security

// DiagnosticConfig holds the configuration fields relevant for security diagnostics.
type DiagnosticConfig struct {
	// ExecToolEnabled is true when exec tool is available to agents.
	ExecToolEnabled bool
	// ExecProxyEnabled is true when the exec HTTP proxy (SEC-28) is running.
	ExecProxyEnabled bool
}

// DiagnosticWarning is a single diagnostic finding.
type DiagnosticWarning struct {
	Code    string // e.g., "SEC-29"
	Message string
}

// CheckExecEgress checks whether the exec tool is enabled without adequate
// network egress control, per FR-030 / SEC-29. Returns warnings for `omnipus doctor`.
func CheckExecEgress(cfg DiagnosticConfig) []DiagnosticWarning {
	if !cfg.ExecToolEnabled {
		return nil
	}

	var warnings []DiagnosticWarning

	if !cfg.ExecProxyEnabled {
		warnings = append(warnings, DiagnosticWarning{
			Code:    "SEC-29",
			Message: "Exec tool is enabled but the exec HTTP proxy (SEC-28) is not running. Child processes can make unfiltered outbound requests. Enable the exec proxy or disable the exec tool.",
		})
	}

	// SEC-05's binary-allowlist warning is retired alongside the allowlist
	// itself (ADR-091 D2/D5 — folded into the D3 rule engine and the
	// Ask/Auto/God Mode selector; there is no longer a
	// security.policy.exec.allowed_binaries key to recommend configuring).

	return warnings
}
