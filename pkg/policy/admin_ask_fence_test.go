// Tests for the admin-ask fence (FR-061) and the IsAdmin predicate (FR-015).
//
// These tests verify the *security invariant* that the privilege-escalation
// guard enforces. They do NOT exercise the resolver itself — that is A1's
// scope — they exercise the fence as a pure post-resolution operation, so
// regardless of how the resolver evolves, the fence's downgrade logic is
// proven independently.

package policy

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestIsAdmin — FR-015 admin role predicate.
func TestIsAdmin(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		u    *config.UserConfig
		want bool
	}{
		{"nil_user_is_not_admin", nil, false},
		{"admin_role", &config.UserConfig{Role: config.UserRoleAdmin}, true},
		{"user_role_is_not_admin", &config.UserConfig{Role: config.UserRoleUser}, false},
		{"empty_role_is_not_admin", &config.UserConfig{Role: ""}, false},
		{
			"username_does_not_imply_admin",
			&config.UserConfig{Username: "admin", Role: config.UserRoleUser}, false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			if got := IsAdmin(c.u); got != c.want {
				t.Fatalf("IsAdmin = %v, want %v", got, c.want)
			}
		})
	}
}

// TestApplyAdminAskFence_AdminAskFenceOnCustomAgents covers FR-061's
// three-case privilege-escalation invariant:
//
//  1. custom + RequiresAdminAsk + allow  → ask  (fence applied)
//  2. core   + RequiresAdminAsk + allow  → allow (fence NOT applied)
//  3. custom + non-RequiresAdminAsk + allow → allow (fence NOT applied — scoped)
//
// Plus boundary checks: deny stays deny; ask stays ask.
//
// FR-061 explicitly requires the test to assert "at least three
// RequiresAdminAsk tools and one non-RequiresAdminAsk tool" — we go further
// and assert all three cases per tool.
func TestApplyAdminAskFence_AdminAskFenceOnCustomAgents(t *testing.T) {
	t.Parallel()

	// Tools with RequiresAdminAsk = true (i.e. the system.* tools).
	adminAskTools := []string{
		"system.config.set",
		"system.agent.create",
		"system.exec",
	}
	// Tool with RequiresAdminAsk = false (i.e. an ordinary builtin).
	benignTool := "read_file"

	requiresAdminAsk := func(name string) bool {
		for _, t := range adminAskTools {
			if t == name {
				return true
			}
		}
		return false
	}
	isCoreAgent := func(id string) bool {
		// Mirror coreagent.GetPrompt(id) != "" semantics.
		switch id {
		case "ava", "billy", "celia", "dax", "eve":
			return true
		}
		return false
	}

	// Case 1: custom + RequiresAdminAsk + allow → ask (fence applied)
	for _, tool := range adminAskTools {
		got, fenceApplied := ApplyAdminAskFence("allow", tool, "my-custom-agent",
			requiresAdminAsk, isCoreAgent)
		if got != "ask" || !fenceApplied {
			t.Errorf("custom+admin+allow: tool=%s got=(%q,%v), want=(ask,true)",
				tool, got, fenceApplied)
		}
	}

	// Case 2: core + RequiresAdminAsk + allow → allow (NOT downgraded)
	for _, tool := range adminAskTools {
		got, fenceApplied := ApplyAdminAskFence("allow", tool, "ava",
			requiresAdminAsk, isCoreAgent)
		if got != "allow" || fenceApplied {
			t.Errorf("core+admin+allow: tool=%s got=(%q,%v), want=(allow,false)",
				tool, got, fenceApplied)
		}
	}

	// Case 3: custom + non-RequiresAdminAsk + allow → allow (scope-correct)
	got, fenceApplied := ApplyAdminAskFence("allow", benignTool, "my-custom-agent",
		requiresAdminAsk, isCoreAgent)
	if got != "allow" || fenceApplied {
		t.Errorf("custom+benign+allow: got=(%q,%v), want=(allow,false)",
			got, fenceApplied)
	}

	// Boundary: deny stays deny regardless.
	for _, tool := range adminAskTools {
		got, fenceApplied := ApplyAdminAskFence("deny", tool, "my-custom-agent",
			requiresAdminAsk, isCoreAgent)
		if got != "deny" || fenceApplied {
			t.Errorf("custom+admin+deny: tool=%s got=(%q,%v), want=(deny,false)",
				tool, got, fenceApplied)
		}
	}

	// Boundary: ask stays ask regardless.
	for _, tool := range adminAskTools {
		got, fenceApplied := ApplyAdminAskFence("ask", tool, "my-custom-agent",
			requiresAdminAsk, isCoreAgent)
		if got != "ask" || fenceApplied {
			t.Errorf("custom+admin+ask: tool=%s got=(%q,%v), want=(ask,false)",
				tool, got, fenceApplied)
		}
	}
}

// TestApplyAdminAskFence_NilPredicates — the fence MUST be a safe no-op
// when its predicates are unset (e.g. early-boot, tests). Specifically a
// nil `requiresAdminAsk` must NOT downgrade anything: this is fail-open
// for the fence, but fail-closed for the system because `allow` is the
// pre-fence state and the fence is purely a tightening pass.
func TestApplyAdminAskFence_NilPredicates(t *testing.T) {
	t.Parallel()
	got, applied := ApplyAdminAskFence("allow", "system.config.set", "custom-agent",
		nil, nil)
	if got != "allow" || applied {
		t.Fatalf("nil predicates should not change effective: got=(%q,%v)", got, applied)
	}
}
