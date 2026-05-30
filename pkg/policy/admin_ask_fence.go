// Package policy — admin-ask fence (FR-061).
//
// FR-061 is the security invariant that closes the privilege-escalation
// gap exposed by H-01 in the rev-5 grill: a custom agent with
// `policies: {"system.config.set": "allow"}` MUST NOT receive an
// unattended `allow` decision for that tool. The fence downgrades the
// effective policy from `allow` to `ask` whenever:
//
//	(a) the resolved tool's `RequiresAdminAsk()` returns true, AND
//	(b) the agent is NOT a core agent (`coreagent.GetPrompt(id) == ""`).
//
// The fence is applied AFTER the global × agent precedence resolution
// (deny > ask > allow) but BEFORE the filter emits the per-tool effective
// policy map. Concretely: if the resolved policy is `deny`, the fence
// does nothing — `deny` already strictly dominates `ask`.
//
// Core agents (Ava etc.) are unaffected: their `allow` stays `allow`
// because their constructor seeds explicit, reviewer-audited
// `system.*: allow` entries (FR-022).
//
// This file does NOT depend on `pkg/tools/` to avoid an import cycle
// (`pkg/tools/` imports `pkg/policy/` for the same admin-ask predicate).
// The caller in pkg/tools/compositor.go injects a predicate that calls
// the Tool's RequiresAdminAsk method.

package policy

// AdminAskPredicate reports whether the named tool requires admin approval.
// Callers in pkg/gateway inject `func(name string) bool { t, ok := agent.Tools.Get(name); return ok && t.RequiresAdminAsk() }`.
// The indirection avoids importing `pkg/tools` from `pkg/policy`.
type AdminAskPredicate func(toolName string) bool

// IsCoreAgentPredicate reports whether the supplied agent ID belongs to a
// core agent (i.e. one with a compiled-in prompt). Pass
// `func(id string) bool { return coreagent.GetPrompt(id) != "" }` from the
// gateway lane. The indirection avoids importing `pkg/coreagent` from
// `pkg/policy` (which would create a layering inversion: coreagent
// depends on tools depends on policy).
type IsCoreAgentPredicate func(agentID string) bool

// ApplyAdminAskFence returns the post-fence effective policy for one
// (tool, agent) pair. `effective` is the policy already resolved through
// `global × agent × deny>ask>allow`.
//
// Behavior:
//
//	deny  → returned as-is (deny dominates).
//	ask   → returned as-is (already at the fence's target).
//	allow → returned as-is when the agent is a core agent OR the tool
//	         does not require admin-ask. Otherwise downgraded to "ask".
//
// `fenceApplied` is true exactly when the function altered `effective`.
// REST endpoints that surface posture (`GET /api/v1/agents/{id}/tools`,
// FR-086) use this flag to render the operator-visible badge.
//
// FR-061.
func ApplyAdminAskFence(
	effective string,
	toolName, agentID string,
	requiresAdminAsk AdminAskPredicate,
	isCoreAgent IsCoreAgentPredicate,
) (resolved string, fenceApplied bool) {
	if effective != "allow" {
		return effective, false
	}
	if isCoreAgent != nil && isCoreAgent(agentID) {
		return effective, false
	}
	if requiresAdminAsk == nil || !requiresAdminAsk(toolName) {
		return effective, false
	}
	return "ask", true
}
