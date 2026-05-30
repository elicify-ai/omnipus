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
