package tools

import "github.com/elicify-ai/omnipus/pkg/config"

// Always-ask mark (channels-spec-r8, FR-036, FR-038, T-10; founder Q22 B).
// resolveEffectivePolicyWith skips the God Mode floor for a listed tool and
// turns its allow verdict into ask; the agent consent layer never consults or
// records a standing grant for it; the approval endpoint refuses its lasting
// allow action; and the policy writers refuse to store allow for it.

// AlwaysAskTools is the fixed list of tools that always ask for a fresh
// approval, also in God Mode, and whose approvals are never remembered.
var AlwaysAskTools = []string{"set_channel_outbound"}

// AlwaysAskAllowRefusal is the refusal every policy writer and the approval
// endpoint return when asked to store or grant a lasting allow for a listed
// tool (FR-037, FR-038, T-10 rows 15 and 16).
func AlwaysAskAllowRefusal(name string) string {
	return name + " always asks for approval; it can be set to ask or deny, not allow"
}

// IsAlwaysAsk reports whether name is in AlwaysAskTools (exact match, no
// prefix or case folding).
func IsAlwaysAsk(name string) bool {
	for _, listed := range AlwaysAskTools {
		if listed == name {
			return true
		}
	}
	return false
}

// AlwaysAskAllowViolation reports the first always-ask tool (in AlwaysAskTools
// order) that policies would store as allow. Every tool-policy writer refuses
// such a write with AlwaysAskAllowRefusal before saving anything (FR-037).
func AlwaysAskAllowViolation[V ~string](policies map[string]V) (string, bool) {
	for _, name := range AlwaysAskTools {
		if value, ok := policies[name]; ok && string(value) == string(config.ToolPolicyAllow) {
			return name, true
		}
	}
	return "", false
}
