package agent

// T34 - TestEffectiveDraftToolPolicy_BothAgentKinds (spec section 7 row 34,
// FR-013, SC-007 reachability - the vault-records bug class: a tool no agent
// can call is a library, not a feature).
// Oracle from the spec row: seeded core agent AND operator-created agent,
// both with an enabled mailbox: create_email_draft effective policy = allow
// AND the tool present in the agent's registry.

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// defaultCeiling copies the shipped global ceiling (what Reconcile leaves on
// a fresh install).
func defaultCeiling() map[string]config.ToolPolicy {
	ceiling := map[string]config.ToolPolicy{}
	for name, policy := range config.DefaultConfig().Sandbox.ToolPolicies {
		ceiling[name] = config.ToolPolicy(policy)
	}
	return ceiling
}

// mailboxCfg seeds one enabled mailbox for (agentID, wsID) whose password
// resolves through the env-injection pattern (PasswordRef -> os.Getenv).
func mailboxCfg(t *testing.T, cfg *config.Config, agentID, wsID, envRef string) {
	t.Helper()
	t.Setenv(envRef, "test-password")
	if cfg.Mailboxes == nil {
		cfg.Mailboxes = config.MailboxesConfig{}
	}
	cfg.Mailboxes[agentID] = map[string]config.MailboxConfig{
		wsID: {
			Enabled:     true,
			WorkspaceID: wsID,
			IMAPHost:    "127.0.0.1",
			IMAPPort:    993,
			SMTPHost:    "127.0.0.1",
			SMTPPort:    465,
			Username:    "agent@example.test",
			PasswordRef: envRef,
		},
	}
}

func TestEffectiveDraftToolPolicy_BothAgentKinds(t *testing.T) {
	const wsID = "ws-mail"

	t.Run("seeded core agent", func(t *testing.T) {
		cfg := &config.Config{}
		mailboxCfg(t, cfg, "mia", wsID, "T34_MIA_PW")
		ag := &AgentInstance{ID: "mia", AgentType: "core", Tools: tools.NewToolRegistry()}

		registerEmailToolsForAgent(cfg, "mia", ag)

		tool, found := ag.Tools.Get("create_email_draft")
		require.True(t, found, "SC-007: create_email_draft missing from the seeded core agent's registry - unreachable, the vault-records bug class")
		require.NotNil(t, tool)

		// The seeded agent's policy layer is the ADR-090 role inventory; the
		// ceiling is the shipped global default. Effective = allow (FR-013).
		polCfg := &tools.ToolPolicyCfg{
			GlobalPolicies: defaultCeiling(),
			Policies:       coreagent.ADR090RolePolicyInventory(coreagent.IDMia),
		}
		if got := tools.EffectiveToolPolicy(polCfg, tool.Scope(), ag.AgentType, "create_email_draft"); got != "allow" {
			t.Fatalf("FR-013: seeded core agent create_email_draft effective policy = %q, want allow", got)
		}
	})

	t.Run("operator-created agent with no explicit email entries", func(t *testing.T) {
		cfg := &config.Config{}
		mailboxCfg(t, cfg, "operator-agent", wsID, "T34_OP_PW")
		ag := &AgentInstance{ID: "operator-agent", AgentType: "custom", Tools: tools.NewToolRegistry()}

		registerEmailToolsForAgent(cfg, "operator-agent", ag)

		tool, found := ag.Tools.Get("create_email_draft")
		require.True(t, found, "SC-007: create_email_draft missing from an operator-created agent's registry despite an enabled mailbox - unreachable, the vault-records bug class")
		require.NotNil(t, tool)

		// No per-agent email entries: the tool rides the global ceiling, which
		// ships allow for create_email_draft (pkg/config/defaults.go).
		polCfg := &tools.ToolPolicyCfg{
			GlobalPolicies: defaultCeiling(),
			Policies:       map[string]config.ToolPolicy{},
		}
		if got := tools.EffectiveToolPolicy(polCfg, tool.Scope(), ag.AgentType, "create_email_draft"); got != "allow" {
			t.Fatalf("FR-013: operator-created agent create_email_draft effective policy = %q, want allow (rides the ceiling)", got)
		}
	})
}
