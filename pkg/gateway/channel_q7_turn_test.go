package gateway

// FR-033/036/038, T-22/T-35: real model-turn assembly, policy compositor,
// tool execution, registry/card transport, HTTP approval and dedicated save.
// The provider scripts only the external model response, never a fake tool.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

func q7TurnPolicy(t *testing.T, f *wu6Fixture, global, perAgent string, god, auto bool) {
	t.Helper()
	f.al.SetAllowGodMode(true)
	t.Cleanup(func() { f.al.SetAllowGodMode(false) })
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		cfg.Sandbox.ToolPolicies["set_channel_outbound"] = global
		cfg.Sandbox.GodMode = god
		cfg.Sandbox.AutoApprove = auto
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	store := agentstore.New(f.f1277.home)
	state, err := store.ReadState(string(coreagent.IDOmnipus))
	require.NoError(t, err)
	_, err = store.MutateState(string(coreagent.IDOmnipus), state.Revision, func(ac *config.AgentConfig) error {
		if ac.Tools == nil {
			ac.Tools = &config.AgentToolsCfg{}
		}
		if ac.Tools.Builtin.Policies == nil {
			ac.Tools.Builtin.Policies = map[string]config.ToolPolicy{}
		}
		delete(ac.Tools.Builtin.Policies, "set_channel_outbound")
		if perAgent != "" {
			ac.Tools.Builtin.Policies["set_channel_outbound"] = config.ToolPolicy(perAgent)
		}
		return nil
	}, nil)
	require.NoError(t, err)
	require.NoError(t, f.api.refreshConfigAndRewireServices(f.f1277.configPath), "instrument: real entity-to-config policy publication")
	_, err = f.al.UpsertAgentFast(f.al.GetConfig(), string(coreagent.IDOmnipus))
	require.NoError(t, err)
	q7WireRealTools(t, f)
	require.NoError(t, wireWU6ScriptedProviderPool(f.al, f.provider))
	require.Equal(t, god, agent.GodModeActive(f.al.GetConfig()), "instrument: actual God Mode, not a test-only boolean on a mock")
}

func q7TurnSession(t *testing.T, f *wu6Fixture) string {
	t.Helper()
	seedWU5Membership(t, f.f1277.home)
	return f.mainSession("sales", string(coreagent.IDOmnipus))
}

func q7Script(callID string, args map[string]any) []providers.LLMResponse {
	return []providers.LLMResponse{
		wu7Search("load-"+callID, "set_channel_outbound"),
		wu7Call(callID, "set_channel_outbound", args),
		{Content: "Q7 turn complete", FinishReason: "stop"},
	}
}

func q7StartTurn(t *testing.T, f *wu6Fixture, sid string) agent.EventSubscription {
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
		require.True(t, f.al.WaitForActiveRequestsContext(ctx), "Q7: canceled/finished request must drain before fixture teardown")
		f.al.UnsubscribeEvents(events.ID)
	})
	require.NoError(t, f.bus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: "q7-webchat", SessionID: sid, Content: "change the connection posting policy",
		UserInitiated: true, Metadata: map[string]string{"agent_id": string(coreagent.IDOmnipus), "workspace_id": "sales"},
	}))
	return events
}

func q7PendingOrFailOnTurnEnd(t *testing.T, f *wu6Fixture, events agent.EventSubscription, sid, callID string) *approvalEntry {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for {
		for _, entry := range f.reg.pendingApprovals() {
			if entry.SessionID == sid && entry.ToolCallID == callID {
				return entry
			}
		}
		select {
		case event := <-events.C:
			if event.Kind == agent.EventKindTurnEnd {
				end, ok := event.Payload.(agent.TurnEndPayload)
				require.True(t, ok)
				if end.SessionID == sid {
					t.Fatalf("FR-033/038: turn ended without a fresh card: %s", f.provider.lastToolResult())
				}
			}
		case <-deadline.C:
			t.Fatal("FR-033/038: real turn produced no fresh approval card")
		case <-ticker.C:
		}
	}
}

func q7WaitTurn(t *testing.T, f *wu6Fixture, events agent.EventSubscription, sid string) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case event, open := <-events.C:
			require.True(t, open, "instrument: turn subscription remains open")
			if event.Kind == agent.EventKindTurnEnd {
				end, ok := event.Payload.(agent.TurnEndPayload)
				require.True(t, ok)
				if end.SessionID == sid {
					require.Equal(t, agent.TurnEndStatusCompleted, end.Status)
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					require.True(t, f.al.WaitForActiveRequestsContext(ctx))
					return
				}
			}
		case <-deadline.C:
			t.Fatal("Q7: actual turn never completed")
		}
	}
}

func TestQ7T22_RealTurnsAlwaysAskAndApproveOnceRepeatsIdentically(t *testing.T) {
	for _, tc := range []struct {
		name, global, grant string
		god, auto           bool
	}{
		{"R01_Ask", "ask", "", false, false},
		{"R02_GodMode", "ask", "", true, false},
		{"R03_storedAllow", "allow", "", false, false},
		{"R03_storedAllow_GodMode", "allow", "", true, false},
		{"R11_Auto", "ask", "", false, true},
		{"R14_exactGrant", "ask", "exact", false, false},
		{"R14_inheritedGrant", "ask", "inherited", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "mia")
			q7TurnPolicy(t, f, tc.global, "", tc.god, tc.auto)
			sid := q7TurnSession(t, f)
			// Identical no-op saves keep the same valid revision. No test-side
			// revision substitution can hide failure to ask on an identical call.
			args := q7Args("telegram", "reply_only", t14Read(t, f, "telegram").Revision, []string{})
			grants := f.al.ApprovalGrants()
			if tc.grant == "exact" {
				require.True(t, grants.Record(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args))
			}
			if tc.grant == "inherited" {
				require.True(t, grants.Record("q7-parent", string(coreagent.IDOmnipus), "set_channel_outbound", args))
				grants.InheritFrom("q7-parent", string(coreagent.IDOmnipus), sid, string(coreagent.IDOmnipus))
			}
			require.Equal(t, tc.grant != "", grants.IsAllowed(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args), "instrument: actual preexisting matching grant")
			var previous string
			for i, callID := range []string{"q7-once", "q7-identical-repeat"} {
				if i == 0 {
					f.provider.setDefaultScript(q7Script(callID, args))
				} else {
					f.provider.appendDefaultScript(q7Script(callID, args))
				}
				events := q7StartTurn(t, f, sid)
				entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
				require.NotEqual(t, previous, entry.ApprovalID, "FR-038: identical repeats need distinct fresh approval identities")
				require.Equal(t, args, entry.Args)
				previous = entry.ApprovalID
				rec := t14Request(t, f, http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, `{"action":"allow_once"}`, true)
				require.Equal(t, http.StatusOK, rec.Code, "real registered approval endpoint: %s", rec.Body.String())
				q7WaitTurn(t, f, events, sid)
				var body map[string]any
				require.NoError(t, json.Unmarshal([]byte(f.provider.lastToolResult()), &body))
				require.Equal(t, "saved", body["status"], "FR-030/033: approval authorizes the actual dedicated writer")
				t13AssertOutbound(t, f, "telegram", "reply_only", []string{})
				require.Equal(t, args["expected_revision"], t14Read(t, f, "telegram").Revision, "no-op retains the identical valid input token")
				require.Equal(t, tc.grant != "", grants.IsAllowed(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args), "FR-038: no new grant, existing grants ignored rather than removed")
			}
		})
	}
}

func TestQ7T35_RealDeniedTurnsReachNeitherAutoGrantNorCard(t *testing.T) {
	for _, tc := range []struct {
		row, global, perAgent string
		god, auto, grant      bool
	}{
		{"R04", "deny", "", false, false, false}, {"R05", "deny", "", true, false, false},
		{"R06", "ask", "deny", true, true, true}, {"R07b", "deny", "ask", true, true, true},
		{"R08", "deny", "", true, false, false}, {"R10", "deny", "", true, true, false},
		{"R12", "deny", "", true, false, true},
	} {
		t.Run(tc.row, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "mia")
			q7TurnPolicy(t, f, tc.global, tc.perAgent, tc.god, tc.auto)
			sid := q7TurnSession(t, f)
			args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
			if tc.grant {
				require.True(t, f.al.ApprovalGrants().Record(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args))
			}
			before := t13Before(t, f)
			f.provider.setDefaultScript(q7Script("q7-denied", args))
			events := q7StartTurn(t, f, sid)
			q7WaitTurn(t, f, events, sid)
			require.Empty(t, f.reg.pendingApprovals(), "T-10: denied calls create no consent card")
			require.NotEmpty(t, f.provider.lastToolResult(), "instrument: script attempted its concrete tool call")
			require.NotContains(t, f.provider.lastToolResult(), `"status":"saved"`)
			t13Unchanged(t, f, before)
		})
	}
}

func TestQ7T35_UnattendedRealTurnRefusesEvenWithMatchingGrant(t *testing.T) {
	for _, inherited := range []bool{false, true} {
		name := "exact"
		if inherited {
			name = "inherited"
		}
		t.Run(name, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "mia")
			q7TurnPolicy(t, f, "ask", "", false, true)
			sid := q7TurnSession(t, f)
			args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
			grants := f.al.ApprovalGrants()
			if inherited {
				require.True(t, grants.Record("q7-headless-parent", string(coreagent.IDOmnipus), "set_channel_outbound", args))
				grants.InheritFrom("q7-headless-parent", string(coreagent.IDOmnipus), sid, string(coreagent.IDOmnipus))
			} else {
				require.True(t, grants.Record(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args))
			}
			require.True(t, grants.IsAllowed(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args))
			before := t13Before(t, f)
			f.provider.setDefaultScript(q7Script("q7-headless", args))
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_, err := f.al.ProcessScheduled(ctx, string(coreagent.IDOmnipus), sid, "change posting policy", "webchat", "q7-headless")
			require.NoError(t, err, "T-10 R13: headless refusal returns normally, never waits for a human")
			require.Empty(t, f.reg.pendingApprovals(), "FR-038: unattended asks never create a pending card")
			require.NotContains(t, f.provider.lastToolResult(), `"status":"saved"`)
			t13Unchanged(t, f, before)
		})
	}
}
