// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

// ResolveBool returns the concrete bool value for a *bool config field,
// applying the caller-supplied default when the pointer is nil.
//
// Pattern: safe-default-true bools use *bool so their zero (unset) state
// survives a SaveConfig round-trip without being serialized as explicit `false`.
// nil means "not set — use the default"; callers document the expected default
// in their field comment (e.g. `// default true via nil-resolver`).
//
// Usage example ( / PathGuardAuditFailClosed):
//
//	failClosed := ResolveBool(cfg.Sandbox.PathGuardAuditFailClosed, true)
func ResolveBool(v *bool, def bool) bool {
	if v == nil {
		return def
	}
	return *v
}

// ResolveInt returns the concrete int value for a *int config field,
// applying the caller-supplied default when the pointer is nil. Mirrors
// ResolveBool's nil-means-unset contract.
//
// Usage example (GatewayConfig.OrphanedTurnGraceSeconds, ADR-045):
//
//	grace := ResolveInt(cfg.Gateway.OrphanedTurnGraceSeconds, DefaultOrphanedTurnGraceSeconds)
//
// Unlike ResolveBool, an explicit non-nil value of 0 (or negative) is
// returned verbatim rather than treated as "unset" — callers that use 0 to
// mean "disabled" (e.g. the orphan-turn watchdog) rely on this to
// distinguish "operator explicitly disabled" from "operator never set it".
func ResolveInt(v *int, def int) int {
	if v == nil {
		return def
	}
	return *v
}
