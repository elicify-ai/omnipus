// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

//go:build linux

package sandbox

// EnforcingChildren reports whether per-thread restriction actually confines
// spawned children: the backend exists, its boot policy was applied, and the
// saved mode is enforce. RestrictCurrentThreadWithPolicy is a silent no-op
// in every other state, so a caller that must DISCLOSE an unconfined child
// (office-preview spec AD-7, "converted without sandbox: <reason>") asks
// here instead of inferring confinement from a nil error.
func (lb *LinuxBackend) EnforcingChildren() bool {
	return lb != nil && lb.policyApplied && lb.savedMode == ModeEnforce
}
