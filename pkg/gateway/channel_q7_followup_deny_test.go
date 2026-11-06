package gateway

import (
	"encoding/json"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/require"
)

func q7FollowupStoredPolicies(t *testing.T, f *wu6Fixture, caller, global, perAgent string) {
	t.Helper()
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Sandbox.ToolPolicies["set_channel_outbound"] = global
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	store := agentstore.New(f.f1277.home)
	state, err := store.ReadState(caller)
	require.NoError(t, err)
	_, err = store.MutateState(caller, state.Revision, func(ac *config.AgentConfig) error {
		delete(ac.Tools.Builtin.Policies, "set_channel_outbound")
		if perAgent != "" {
			ac.Tools.Builtin.Policies["set_channel_outbound"] = config.ToolPolicy(perAgent)
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.NoError(t, f.api.refreshConfigAndRewireServices(f.f1277.configPath))
	_, err = f.al.UpsertAgentFast(f.al.GetConfig(), caller)
	require.NoError(t, err)
	q7WireRealTools(t, f)
	require.NoError(t, wireWU6ScriptedProviderPool(f.al, f.provider))
}

// R-Q4(d), T-10 R4–R8/R10/R12: a denied ordinary agent never reaches any
// approval/grant/Auto path. This is separate from the Ask-mode positive cases.
func TestQ7FollowupQ4_OrdinaryDenyBeatsGodAutoAndRememberedGrants(t *testing.T) {
	for _, tc := range []struct {
		name, global, perAgent string
		god, auto, grant       bool
	}{
		{"R04", "deny", "", false, false, false}, {"R05", "deny", "", true, false, false},
		{"R06", "ask", "deny", true, true, true}, {"R07b", "deny", "ask", true, true, true},
		{"R08", "deny", "", true, false, false}, {"R10", "deny", "", true, true, false},
		{"R12", "deny", "", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "jim")
			sid := q7FollowupCaller(t, f, "mia", tc.god, tc.auto)
			q7FollowupStoredPolicies(t, f, "mia", tc.global, tc.perAgent)
			args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
			if tc.grant {
				require.True(t, f.al.ApprovalGrants().Record(sid, "mia", "set_channel_outbound", args))
			}
			before := t13Before(t, f)
			f.provider.setDefaultScript(q7Script("q4-denied", args))
			events := q7FollowupStartAs(t, f, sid, "mia")
			entry := q7FollowupPendingOrCompleted(t, f, events, sid, "q4-denied")
			if entry != nil {
				f.reg.resolve(entry.ApprovalID, ApprovalActionCancel, false)
				q7WaitTurn(t, f, events, sid)
			}
			require.Nil(t, entry, "T-10: policy Deny ends before consent")
			var result map[string]any
			require.NoError(t, json.Unmarshal([]byte(f.provider.lastToolResult()), &result), "instrument: scripted tool was attempted")
			require.NotEqual(t, "saved", result["status"])
			require.Contains(t, f.provider.lastToolResult(), "denied", "T-10: actual policy refusal, not OMNIPUS_ONLY")
			t13Unchanged(t, f, before)
		})
	}
}
