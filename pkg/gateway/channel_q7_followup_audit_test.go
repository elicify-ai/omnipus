package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// R-S3 and security report S3 supersede the old no-new-audit scope. The
// existing shared audit contract supplies event/field names and digest format:
// tool-registry-redesign-spec FR-038/047/054/074/080, Audit & Observability.
// Only the external model is scripted. Consent, identity, audit file, writer,
// stale refusal, timeout transition and grant store are all production objects.
func TestQ7FollowupS3_RealAuditBindsEachConsentDecisionToItsCall(t *testing.T) {
	for _, tc := range []struct {
		name, action, terminal, reason string
		stale, nonChannels             bool
	}{
		{"approve_once_save", "allow_once", audit.EventToolPolicyAskGranted, "", false, false},
		{"approve_once_stale_refusal", "allow_once", audit.EventToolPolicyAskGranted, "", true, false},
		{"deny", "deny", audit.EventToolPolicyAskDenied, "user", false, false},
		{"cancel", "cancel", audit.EventToolPolicyAskDenied, "cancel", false, false},
		{"expired", "expired", audit.EventToolPolicyAskDenied, "timeout", false, false},
		{"non_channels_ask_approve_once", "allow_once", audit.EventToolPolicyAskGranted, "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "mia")
			q7FollowupContext(t, f)
			q7TurnPolicy(t, f, "ask", "", false, false)
			require.NotNil(t, f.al.AuditLogger(), "instrument: the real boot-created audit logger is enabled")
			toolName := "set_channel_outbound"
			args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"987654321"})
			if tc.nonChannels {
				// T8 r17 made get_agent's fields parameter required (merge of
				// release/v0.1.1): name the one property the read needs.
				toolName, args = "get_agent", map[string]any{"id": "mia", "fields": []any{"name"}}
				require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
					cfg.Sandbox.ToolPolicies[toolName] = "ask"
					return config.SaveConfig(f.f1277.configPath, cfg)
				}))
				_, err := f.al.UpsertAgentFast(f.al.GetConfig(), "omnipus")
				require.NoError(t, err)
				q7WireRealTools(t, f)
				require.NoError(t, wireWU6ScriptedProviderPool(f.al, f.provider))
			}
			sid := q7TurnSession(t, f)
			request := q7AuditAuth(t, f, sid)
			before := t13Before(t, f)
			const callID = "q7-audit-call"
			f.provider.setDefaultScript([]providers.LLMResponse{
				wu7Search("q7-audit-load", toolName), wu7Call(callID, toolName, args),
				{Content: "audit turn complete", FinishReason: "stop"},
			})
			events := q7StartTurn(t, f, sid)
			entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
			require.Equal(t, args, entry.Args, "instrument: digest covers the exact displayed request")
			// These arguments contain only ASCII keys/string values and arrays.
			// JSON's sorted map keys/no whitespace equal FR-080 canonicaljson here;
			// no production hashing helper is used to manufacture the oracle.
			approvedHash := q7FollowupDigest(t, args)
			require.False(t, f.al.ApprovalGrants().IsAllowed(sid, "omnipus", toolName, args))
			if tc.stale {
				body, err := json.Marshal(gen.ChannelOutboundUpdateRequest{
					Policy:  gen.ChannelOutboundUpdateRequestPolicyAgentMayStart,
					Targets: []string{"333"}, ExpectedRevision: args["expected_revision"].(string),
				})
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, request(http.MethodPut, "/api/v1/channels/telegram/outbound", string(body)).Code, "instrument: an actual concurrent operator save makes the displayed revision stale")
				before = t13Before(t, f)
			}
			if tc.action == "expired" {
				f.reg.fireTimeout(entry.ApprovalID) // real timer callback; no invented audit emission
			} else {
				resolved := request(http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, `{"action":"`+tc.action+`"}`)
				require.Equal(t, http.StatusOK, resolved.Code, "instrument: real authenticated decision route")
			}
			q7WaitTurn(t, f, events, sid)
			require.False(t, f.al.ApprovalGrants().IsAllowed(sid, "omnipus", toolName, args), "R-S3: no terminal action here records a standing grant")
			var resultingRevision string
			switch {
			case tc.stale:
				require.Contains(t, f.provider.lastToolResult(), `"CONFLICT"`, "instrument: approved call was actually refused as stale")
				t13Unchanged(t, f, before)
			case tc.nonChannels:
				require.Contains(t, f.provider.lastToolResult(), `"mia"`, "instrument: actual non-Channels read returned the requested fixture agent")
				t13Unchanged(t, f, before)
			case tc.action == "allow_once":
				resultingRevision = q7AuditSavedOutbound(t, f, request)
				require.NotEqual(t, args["expected_revision"], resultingRevision, "instrument: result revision cannot be satisfied by the old requested token")
			default:
				t13Unchanged(t, f, before)
			}
			rows := q7AuditRead(t, f, sid, toolName, tc.action == "allow_once")
			if tc.nonChannels {
				for _, row := range rows {
					if row["event"] == audit.EventToolCall && row["tool"] == toolName && row["session_id"] == sid {
						require.Equal(t, string(audit.DecisionAllow), row["decision"], "instrument: approved non-Channels tool actually succeeded")
					}
				}
			}
			q7AuditPrivateArguments(t, rows, toolName)
			requested := q7AuditEvent(t, rows, audit.EventToolPolicyAskRequested, entry.ApprovalID)
			q7AuditIdentity(t, requested, entry, approvedHash)
			terminal := q7AuditEvent(t, rows, tc.terminal, entry.ApprovalID)
			q7AuditIdentity(t, terminal, entry, approvedHash)
			approver := q7AuditApprover
			if tc.action == "expired" {
				approver = ""
			} // no person made the system timeout decision
			require.Equal(t, approver, terminal["approver_user_id"], "R-S3: exact real authenticated decider, never the initiating agent")
			if tc.reason != "" {
				require.Equal(t, tc.reason, terminal["reason"], "FR-047: denial/cancel/expiry remain distinguishable")
			}
			if !tc.nonChannels && tc.action == "allow_once" {
				q7AuditOutcome(t, rows, entry, resultingRevision, tc.stale)
			}
		})
	}
}
