package tools

// Q7 / T-35: stage 1's oracles are the literal T-10 table in channels-spec-r8,
// never the resolver's observed output. Consent is exercised separately through
// the real gateway flow. Existing-correct cases are explicit positive controls.

import (
	"context"
	"maps"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQ7T35_AlwaysAskMarkIsExactlyTheSpecifiedTool(t *testing.T) {
	require.Equal(t, []string{"set_channel_outbound"}, AlwaysAskTools, "FR-036: the fixed list has one exact name")
	require.True(t, IsAlwaysAsk("set_channel_outbound"), "FR-036: WP-0's false shell is not an implemented mark")
}

func TestQ7T35_AlwaysAskNearMissesAreUnmarked_PositiveControl(t *testing.T) {
	for _, name := range []string{"", "connect_channel", "SET_CHANNEL_OUTBOUND", "set_channel_outbound.extra", "set_channel_outbound_", " set_channel_outbound", "set_channel_outbound "} {
		t.Run(name, func(t *testing.T) {
			require.False(t, IsAlwaysAsk(name), "FR-036: no prefix, whitespace or case folding for %q", name)
		})
	}
}

func TestQ7T35_StoredPolicyTruthTable(t *testing.T) {
	// Controls are named before execution, from existing strictest-wins and the
	// spec's unchanged-behavior clauses, not by classifying a run afterward.
	rows := []struct {
		row, global, agent, want string
		god, cleared, control    bool
	}{
		{"R01_shipped_ask", "ask", "", "ask", false, false, true},
		{"R02_GodMode_shipped_ask", "ask", "", "ask", true, false, false},
		{"R03_hand_edited_allow_off", "allow", "", "ask", false, false, false},
		{"R03_hand_edited_allow_on", "allow", "", "ask", true, false, false},
		{"R04_global_deny_off", "deny", "", "deny", false, false, true},
		{"R05_global_deny_on", "deny", "", "deny", true, false, false},
		{"R06_deny_agent_global_ask_off", "ask", "deny", "deny", false, false, true},
		{"R06_deny_agent_global_ask_on", "ask", "deny", "deny", true, false, true},
		{"R06_deny_agent_global_allow_off", "allow", "deny", "deny", false, false, true},
		{"R06_deny_agent_global_allow_on", "allow", "deny", "deny", true, false, true},
		{"R06_deny_agent_global_deny_off", "deny", "deny", "deny", false, false, true},
		{"R06_deny_agent_global_deny_on", "deny", "deny", "deny", true, false, true},
		{"R07_ask_agent_global_ask_off", "ask", "ask", "ask", false, false, true},
		{"R07_ask_agent_global_ask_on", "ask", "ask", "ask", true, false, true},
		{"R07_ask_agent_global_allow_off", "allow", "ask", "ask", false, false, true},
		{"R07_ask_agent_global_allow_on", "allow", "ask", "ask", true, false, true},
		{"R07b_global_deny_agent_ask_off", "deny", "ask", "deny", false, false, true},
		{"R07b_global_deny_agent_ask_on", "deny", "ask", "deny", true, false, false},
		{"R08_cleared_agent_global_deny_off", "deny", "", "deny", false, true, true},
		{"R08_cleared_agent_global_deny_on", "deny", "", "deny", true, true, false},
		{"R09_cleared_agent_global_ask_off", "ask", "", "ask", false, true, true},
		{"R09_cleared_agent_global_ask_on", "ask", "", "ask", true, true, false},
		{"R10_deny_before_Auto_off", "deny", "", "deny", false, false, true},
		{"R10_deny_before_Auto_on", "deny", "", "deny", true, false, false},
		{"R12_deny_before_grant_off", "deny", "", "deny", false, false, true},
		{"R12_deny_before_grant_on", "deny", "", "deny", true, false, false},
	}
	for _, row := range rows {
		name := row.row
		if row.control {
			name += "_positive_control"
		}
		t.Run(name, func(t *testing.T) {
			cfg := &ToolPolicyCfg{
				GlobalPolicies: map[string]config.ToolPolicy{"set_channel_outbound": config.ToolPolicy(row.global)},
				Policies:       map[string]config.ToolPolicy{}, GodMode: row.god,
			}
			if row.agent != "" {
				cfg.Policies["set_channel_outbound"] = config.ToolPolicy(row.agent)
			}
			if row.cleared {
				cfg.Policies["set_channel_outbound"] = config.ToolPolicyDeny
				delete(cfg.Policies, "set_channel_outbound") // T-10's explicit clear, not stored Ask.
			}
			beforeGlobal, beforeAgent := maps.Clone(cfg.GlobalPolicies), maps.Clone(cfg.Policies)
			assert.Equal(t, row.want, ResolveEffectivePolicy(cfg, "set_channel_outbound"), "T-10 %s: stored-layer verdict", row.row)
			assert.Equal(t, row.want, EffectiveToolPolicy(cfg, ScopeCore, "core", "set_channel_outbound"), "T-10 %s: execution resolver must agree", row.row)
			coveredVerdict, covered := ResolveEffectivePolicyIfCovered(cfg, "set_channel_outbound")
			assert.True(t, covered, "fixture supplies an explicit global entry")
			assert.Equal(t, row.want, coveredVerdict, "T-10 %s: covered resolver must agree", row.row)
			assert.Equal(t, beforeGlobal, cfg.GlobalPolicies, "FR-036: resolution must not rewrite stored global policy")
			assert.Equal(t, beforeAgent, cfg.Policies, "FR-036: resolution must not rewrite stored agent policy")
		})
	}
}

func TestQ7T35_OtherToolsKeepTheGodModeFloor_PositiveControl(t *testing.T) {
	for _, name := range []string{"connect_channel", "set_agent_permissions", "set_config"} {
		for _, global := range []config.ToolPolicy{config.ToolPolicyAsk, config.ToolPolicyDeny} {
			t.Run(name+"_"+string(global), func(t *testing.T) {
				cfg := &ToolPolicyCfg{GlobalPolicies: map[string]config.ToolPolicy{name: global}, GodMode: true}
				require.Equal(t, "allow", ResolveEffectivePolicy(cfg, name), "FR-036/NB-15: the exception must not spread to %s", name)
				cfg.Policies = map[string]config.ToolPolicy{name: config.ToolPolicyDeny}
				require.Equal(t, "deny", ResolveEffectivePolicy(cfg, name), "unchanged: the floor never lifts the agent's own Deny")
			})
		}
	}
}

func TestQ7T35_AutoClassificationHasAnExplicitAskEntry(t *testing.T) {
	class, present := AutoApproveClassTable()["set_channel_outbound"]
	require.True(t, present, "FR-032/T-10 R11: an unknown-name fallback must not masquerade as explicit AutoAsks registration")
	require.Equal(t, AutoAsks, class)
	verdict := ClassifyAutoApprove(context.Background(), "set_channel_outbound", nil, nil)
	require.False(t, verdict.Run, "FR-033: Auto cannot settle this call")
	require.Equal(t, AutoVerdictClassAsks, verdict.Class)
}
