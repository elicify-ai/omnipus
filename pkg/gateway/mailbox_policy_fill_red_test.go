package gateway

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Oracle: D19 / MC-26 / B-46 / FR-013. Configuring a mailbox writes ask for
// send_email and reply, and allow for the read tools plus create_email_draft,
// only where that key is absent. An explicit allow, ask or deny is never
// rewritten. A seeded Admin inventory deny stays deny.

func TestMailboxConfigurePolicyFill(t *testing.T) {
	t.Run("absent keys", func(t *testing.T) {
		got := applyMailboxPolicyFill(t, map[string]config.ToolPolicy{
			"create_task": config.ToolPolicyAllow,
		})
		assertFilled(t, got, map[string]config.ToolPolicy{
			"send_email":         config.ToolPolicyAsk,
			"reply":              config.ToolPolicyAsk,
			"read_inbox":         config.ToolPolicyAllow,
			"search_email":       config.ToolPolicyAllow,
			"read_message":       config.ToolPolicyAllow,
			"create_email_draft": config.ToolPolicyAllow,
			"create_task":        config.ToolPolicyAllow,
		})
	})
	t.Run("explicit deny unchanged", func(t *testing.T) {
		got := applyMailboxPolicyFill(t, map[string]config.ToolPolicy{
			"send_email": config.ToolPolicyDeny,
			"reply":      config.ToolPolicyDeny,
		})
		if got["send_email"] != config.ToolPolicyDeny || got["reply"] != config.ToolPolicyDeny {
			t.Fatalf("MC-26: explicit deny became send_email=%s reply=%s", got["send_email"], got["reply"])
		}
	})
	t.Run("explicit ask unchanged", func(t *testing.T) {
		got := applyMailboxPolicyFill(t, map[string]config.ToolPolicy{
			"send_email": config.ToolPolicyAsk,
		})
		if got["send_email"] != config.ToolPolicyAsk {
			t.Fatalf("MC-26: explicit ask became %s", got["send_email"])
		}
	})
	t.Run("explicit allow unchanged", func(t *testing.T) {
		got := applyMailboxPolicyFill(t, map[string]config.ToolPolicy{
			"send_email": config.ToolPolicyAllow,
		})
		if got["send_email"] != config.ToolPolicyAllow {
			t.Fatalf("MC-26: explicit allow became %s", got["send_email"])
		}
	})
	t.Run("seeded admin deny stays", func(t *testing.T) {
		seed := coreagent.ADR090RolePolicyInventory(coreagent.IDAdmin)
		before := map[string]config.ToolPolicy{}
		for name, policy := range seed {
			before[name] = policy
		}
		got := applyMailboxPolicyFill(t, before)
		for _, name := range []string{"send_email", "reply", "read_inbox", "search_email", "read_message"} {
			if seed[name] != config.ToolPolicyDeny {
				t.Fatalf("test setup: Admin inventory %s = %s, want deny", name, seed[name])
			}
			if got[name] != config.ToolPolicyDeny {
				t.Fatalf("MC-26: seeded Admin %s became %s, want deny", name, got[name])
			}
		}
		if got["create_email_draft"] != config.ToolPolicyAllow {
			t.Fatalf("FR-013: Admin create_email_draft = %s, want allow (the key is absent from the inventory)", got["create_email_draft"])
		}
	})
	t.Run("effective policy allow after fill", func(t *testing.T) {
		filled := applyMailboxPolicyFill(t, map[string]config.ToolPolicy{})
		ceiling := map[string]config.ToolPolicy{}
		for name, policy := range config.DefaultConfig().Sandbox.ToolPolicies {
			ceiling[name] = config.ToolPolicy(policy)
		}
		cfg := &tools.ToolPolicyCfg{GlobalPolicies: ceiling, Policies: filled}
		if got := tools.EffectiveToolPolicy(cfg, tools.ScopeGeneral, "custom", "create_email_draft"); got != "allow" {
			t.Fatalf("FR-013: effective create_email_draft = %q, want allow", got)
		}
		if got := tools.EffectiveToolPolicy(cfg, tools.ScopeGeneral, "custom", "send_email"); got != "ask" {
			t.Fatalf("MC-26: effective send_email = %q, want ask", got)
		}
	})
}

func applyMailboxPolicyFill(t *testing.T, policies map[string]config.ToolPolicy) map[string]config.ToolPolicy {
	t.Helper()
	home := t.TempDir()
	store := agentstore.New(home)
	copied := map[string]config.ToolPolicy{}
	for name, policy := range policies {
		copied[name] = policy
	}
	if err := store.Create("mia", &config.AgentConfig{
		ID:    "mia",
		Tools: &config.AgentToolsCfg{Builtin: config.AgentBuiltinToolsCfg{Policies: copied}},
	}); err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	grantEmailToolAllows(home, "mia")
	got, err := store.Get("mia")
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if got.Tools == nil {
		t.Fatal("MC-26: agent tools config is nil after mailbox policy fill")
	}
	return got.Tools.Builtin.Policies
}

func assertFilled(t *testing.T, got, want map[string]config.ToolPolicy) {
	t.Helper()
	for name, policy := range want {
		if got[name] != policy {
			t.Errorf("MC-26: %s = %q, want %q", name, got[name], policy)
		}
	}
}
