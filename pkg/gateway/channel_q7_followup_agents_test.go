package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/require"
)

func q7FollowupCaller(t *testing.T, f *wu6Fixture, caller string, god, auto bool) string {
	t.Helper()
	q7FollowupContext(t, f)
	store := agentstore.New(f.f1277.home)
	all, skipped, err := store.List()
	require.NoError(t, err)
	require.Empty(t, skipped)
	found := false
	for _, ac := range all {
		found = found || ac.ID == caller
	}
	if !found {
		require.NoError(t, store.Create(caller, &config.AgentConfig{ID: caller, Name: caller, Type: config.AgentTypeCustom, Tools: coreagent.NewCustomAgentToolsCfg()}))
	}
	q7TurnPolicy(t, f, "ask", "", god, auto)
	state, err := store.ReadState(caller)
	require.NoError(t, err)
	_, err = store.MutateState(caller, state.Revision, func(ac *config.AgentConfig) error {
		if ac.Tools == nil {
			ac.Tools = &config.AgentToolsCfg{}
		}
		if ac.Tools.Builtin.Policies == nil {
			ac.Tools.Builtin.Policies = map[string]config.ToolPolicy{}
		}
		delete(ac.Tools.Builtin.Policies, "set_channel_outbound")
		if caller != "omnipus" {
			// Runtime case precondition is effective Ask. Fresh/upgrade seed
			// behavior is tested separately, without overriding its output.
			ac.Tools.Builtin.Policies["set_channel_outbound"] = config.ToolPolicyAsk
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.NoError(t, f.api.refreshConfigAndRewireServices(f.f1277.configPath))
	_, err = f.al.UpsertAgentFast(f.al.GetConfig(), caller)
	require.NoError(t, err)
	q7WireRealTools(t, f)
	require.NoError(t, wireWU6ScriptedProviderPool(f.al, f.provider))
	require.NoError(t, os.MkdirAll(filepath.Join(f.f1277.home, "workspaces"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(f.f1277.home, "workspaces", "sales.json"), []byte(`{"id":"sales","core_team":["mia","jim","q4-custom"]}`), 0o644))
	instance, ok := f.al.GetRegistry().GetAgent(caller)
	require.True(t, ok)
	require.Equal(t, config.ToolPolicyAsk, instance.LoadToolPolicy().GlobalPolicies["set_channel_outbound"], "instrument: the runtime uses the real Ask ceiling")
	return f.mainSession("sales", caller)
}

func q7FollowupStartAs(t *testing.T, f *wu6Fixture, sid, caller string) agent.EventSubscription {
	t.Helper()
	events := f.al.SubscribeEvents(256)
	t.Cleanup(func() {
		for _, entry := range f.reg.pendingApprovals() {
			if entry.SessionID == sid {
				f.reg.resolve(entry.ApprovalID, ApprovalActionCancel, false)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.True(t, f.al.WaitForActiveRequestsContext(ctx), "instrument: actual requests drain before fixture teardown")
		f.al.UnsubscribeEvents(events.ID)
	})
	require.NoError(t, f.bus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: "q7-webchat", SessionID: sid, Content: "change the connection posting policy",
		UserInitiated: true, Metadata: map[string]string{"agent_id": caller, "workspace_id": "sales"},
	}))
	return events
}

func q7FollowupSavedTurn(t *testing.T, f *wu6Fixture, events agent.EventSubscription, sid string) {
	t.Helper()
	q7WaitTurn(t, f, events, sid)
	var body map[string]any
	require.NoError(t, json.Unmarshal([]byte(f.provider.lastToolResult()), &body))
	require.Equal(t, "saved", body["status"], "Q4: approved ordinary caller must actually save, not OMNIPUS_ONLY")
}

// R-Q4(a/d), T-10 and S-29, now for ordinary chat agents too. Two IDENTICAL
// calls keep the same revision by making no-op saves, so grants cannot hide a
// missing fresh card. The changed-value save is a separate positive case.
func TestQ7FollowupQ4_ChatCapableAgentsAlwaysAskInEveryConsentMode(t *testing.T) {
	for _, tc := range []struct {
		name, grant            string
		god, auto, legacyAllow bool
	}{
		{name: "Ask"}, {name: "GodMode", god: true}, {name: "Auto", auto: true},
		{name: "RememberedGrant", grant: "exact"}, {name: "InheritedGrantGodAndAuto", grant: "inherited", god: true, auto: true},
		{name: "StoredAllowStillAsksInGodMode", god: true, legacyAllow: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "jim")
			sid := q7FollowupCaller(t, f, "mia", tc.god, tc.auto)
			if tc.legacyAllow {
				// T-10 R3/R9: represent a hand-edited legacy file, not a legal
				// policy-writer request. Both layers are Allow; consent still wins.
				q7FollowupStoredPolicies(t, f, "mia", "allow", "allow")
			}
			args := q7Args("telegram", "reply_only", t14Read(t, f, "telegram").Revision, []string{})
			grants := f.al.ApprovalGrants()
			if tc.grant == "exact" {
				require.True(t, grants.Record(sid, "mia", "set_channel_outbound", args))
			} else if tc.grant == "inherited" {
				require.True(t, grants.Record("q4-parent", "mia", "set_channel_outbound", args))
				grants.InheritFrom("q4-parent", "mia", sid, "mia")
			}
			require.Equal(t, tc.grant != "", grants.IsAllowed(sid, "mia", "set_channel_outbound", args), "instrument: actual matching grant state")
			var previous string
			for _, callID := range []string{"q4-first", "q4-identical-repeat"} {
				if previous == "" {
					f.provider.setDefaultScript(q7Script(callID, args))
				} else {
					f.provider.appendDefaultScript(q7Script(callID, args))
				}
				events := q7FollowupStartAs(t, f, sid, "mia")
				entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
				require.Equal(t, "mia", entry.AgentID)
				require.NotEqual(t, previous, entry.ApprovalID, "Q4/T-10: fresh approval identity for every identical call")
				require.Equal(t, args, entry.Args)
				previous = entry.ApprovalID
				refused := t14Request(t, f, http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, `{"action":"allow"}`, true)
				require.Equal(t, http.StatusBadRequest, refused.Code)
				require.JSONEq(t, `{"error":"`+q7AlwaysAskText+`"}`, refused.Body.String())
				rec := t14Request(t, f, http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, `{"action":"allow_once"}`, true)
				require.Equal(t, http.StatusOK, rec.Code)
				q7FollowupSavedTurn(t, f, events, sid)
				t13AssertOutbound(t, f, "telegram", "reply_only", []string{})
				require.Equal(t, args["expected_revision"], t14Read(t, f, "telegram").Revision)
				require.Equal(t, tc.grant != "", grants.IsAllowed(sid, "mia", "set_channel_outbound", args), "Q4: approve-once never records a new grant")
			}
		})
	}
}

func TestQ7FollowupQ4_ChatCapableAgentsApprovedOtherConnectionReallySaves(t *testing.T) {
	for _, caller := range []string{"omnipus", "mia", "jim", "q4-custom"} {
		t.Run(caller, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "other-bound-agent")
			sid := q7FollowupCaller(t, f, caller, false, false)
			args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
			f.provider.setDefaultScript(q7Script("q4-other", args))
			events := q7FollowupStartAs(t, f, sid, caller)
			entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, "q4-other")
			require.Equal(t, caller, entry.AgentID)
			require.Equal(t, args, entry.Args)
			rec := t14Request(t, f, http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, `{"action":"allow_once"}`, true)
			require.Equal(t, http.StatusOK, rec.Code)
			q7FollowupSavedTurn(t, f, events, sid)
			t13AssertOutbound(t, f, "telegram", "agent_may_start", []string{"telegram:222"})
			require.False(t, f.al.ApprovalGrants().IsAllowed(sid, caller, "set_channel_outbound", args))
		})
	}
}

func TestQ7FollowupQ4_ChatCapableOwnConnectionRefusesBeforeAnyCard(t *testing.T) {
	for _, caller := range []string{"omnipus", "mia", "jim", "q4-custom"} {
		t.Run(caller, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", caller)
			sid := q7FollowupCaller(t, f, caller, true, true)
			before := t13Before(t, f)
			args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
			f.provider.setDefaultScript(q7Script("q4-own", args))
			events := q7FollowupStartAs(t, f, sid, caller)
			entry := q7FollowupPendingOrCompleted(t, f, events, sid, "q4-own")
			if entry != nil {
				// Drain even on RED; a refused own-connection request should never
				// have offered the person an approval card in the first place.
				f.reg.resolve(entry.ApprovalID, ApprovalActionCancel, false)
				q7WaitTurn(t, f, events, sid)
			}
			require.Nil(t, entry, "Q4/FD-11: OWN_CONNECTION is refused before any card")
			var body struct {
				Success bool `json:"success"`
				Error   struct {
					Code    string `json:"code"`
					Message string `json:"message"`
				} `json:"error"`
			}
			require.NoError(t, json.Unmarshal([]byte(f.provider.lastToolResult()), &body))
			require.False(t, body.Success)
			require.Equal(t, "OWN_CONNECTION", body.Error.Code)
			require.Equal(t, "a connection's own agent cannot change its posting list; ask the operator", body.Error.Message)
			t13Unchanged(t, f, before)
		})
	}
}

func TestQ7FollowupQ4_OrdinaryUnattendedRunRefusesEvenWithRememberedGrant(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := q7FollowupCaller(t, f, "mia", true, true)
	args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
	require.True(t, f.al.ApprovalGrants().Record(sid, "mia", "set_channel_outbound", args))
	require.True(t, f.al.ApprovalGrants().IsAllowed(sid, "mia", "set_channel_outbound", args))
	before := t13Before(t, f)
	f.provider.setDefaultScript(q7Script("q4-unattended", args))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := f.al.ProcessScheduled(ctx, "mia", sid, "change posting policy", "webchat", "q4-unattended")
	require.NoError(t, err)
	require.Empty(t, f.reg.pendingApprovals(), "T-10 R13: unattended run cannot wait for an absent person")
	require.Contains(t, f.provider.lastToolResult(), "denied", "instrument: the scripted ask call was attempted and refused, not silently omitted")
	require.NotContains(t, f.provider.lastToolResult(), `"status":"saved"`)
	t13Unchanged(t, f, before)
}
