package tools

// Always-ask mark WP-0 shell (channels-spec-r8, FR-036, T-10; founder Q22 B).
// Nothing reads it yet: resolveEffectivePolicyWith, checkStandingGrant, the
// grant recorder and the approval endpoint start consulting it in WP-2.

// AlwaysAskTools is the fixed list of tools that always ask for a fresh
// approval, also in God Mode, and whose approvals are never remembered.
var AlwaysAskTools = []string{"set_channel_outbound"}

// IsAlwaysAsk reports whether name is in AlwaysAskTools (exact match, no
// prefix or case folding).
//
// WP-0 shell: returns false.
func IsAlwaysAsk(name string) bool {
	return false
}
