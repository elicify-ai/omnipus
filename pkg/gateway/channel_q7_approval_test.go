package gateway

// FR-038 / T-22: actual registry, HTTP action handler, grant recorder and
// agent-loop consent gate. These rows intentionally do not require the absent
// tool constructor: its missing consent exception is independently observable.

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func q7PostApproval(t *testing.T, api *restAPI, id, action, scope string) *httptest.ResponseRecorder {
	t.Helper()
	body := gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestAction(action)}
	if scope != "" {
		value := gen.ToolApprovalActionRequestScope(scope)
		body.Scope = &value
	}
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	r := withAdminRole(httptest.NewRequest(http.MethodPost, "/api/v1/tool-approvals/"+id, bytes.NewReader(raw)))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	api.HandleToolApprovals(w, r)
	return w
}

func TestQ7T22_LastingAllowIsRefusedBeforePendingApprovalResolves(t *testing.T) {
	for _, scope := range []string{"", "exact", "prefix"} {
		t.Run("scope_"+scope, func(t *testing.T) {
			api, reg := newTestRestAPIWithApprovalReg(t)
			args := q7Args("telegram", "reply_only", "q7-token", []string{})
			entry, accepted := reg.requestApproval("q7-allow", "set_channel_outbound", args, string(coreagent.IDOmnipus), "q7-session", "q7-turn")
			require.True(t, accepted, "instrument: a real pending request exists")
			t.Cleanup(func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) })
			w := q7PostApproval(t, api, entry.ApprovalID, "allow", scope)
			assert.Equal(t, http.StatusBadRequest, w.Code, "FR-038/Q3 A: lasting Allow must be refused before approval: %s", w.Body.String())
			assert.JSONEq(t, `{"error":"`+q7AlwaysAskText+`"}`, w.Body.String(), "FR-038: exact always-ask refusal")
			assert.Equal(t, ApprovalStatePending, reg.get(entry.ApprovalID).state, "FR-038: rejected Allow leaves the existing card usable")
			assert.False(t, api.agentLoop.ApprovalGrants().IsAllowed(entry.SessionID, entry.AgentID, entry.ToolName, args), "FR-038: no exact, fallback-prefix or parent grant")
			select {
			case outcome := <-entry.resultCh:
				t.Errorf("FR-038: rejected Allow must not resolve or authorize the call; got %+v", outcome)
			default:
			}
			require.Equal(t, ApprovalStatePending, reg.get(entry.ApprovalID).state)
			once := postToolApproval(t, api, entry.ApprovalID, "allow_once")
			require.Equal(t, http.StatusOK, once.Code, "the original card still accepts approve-once")
			require.False(t, api.agentLoop.ApprovalGrants().IsAllowed(entry.SessionID, entry.AgentID, entry.ToolName, args))
		})
	}
}

func TestQ7T22_GrantRecorderCannotRememberTheAlwaysAskTool(t *testing.T) {
	for _, scope := range []gen.ToolApprovalActionRequestScope{gen.ToolApprovalActionRequestScopeExact, gen.ToolApprovalActionRequestScopePrefix} {
		t.Run(string(scope), func(t *testing.T) {
			api, reg := newTestRestAPIWithApprovalReg(t)
			args := q7Args("telegram", "reply_only", "q7-token", []string{})
			entry, accepted := reg.requestApproval("q7-recorder", "set_channel_outbound", args, string(coreagent.IDOmnipus), "q7-acting", "q7-turn")
			require.True(t, accepted)
			t.Cleanup(func() { reg.resolve(entry.ApprovalID, ApprovalActionCancel, false) })
			record, _ := api.approvalGrantRecorder(entry, scope)
			require.NotNil(t, record, "instrument: the real grant recorder must be wired, not absent")
			assert.False(t, record("q7-acting"), "FR-038: callback cannot install an acting-session grant")
			assert.False(t, record("q7-parent"), "FR-038: callback cannot install a parent-session grant")
			for _, sid := range []string{"q7-acting", "q7-parent"} {
				assert.False(t, api.agentLoop.ApprovalGrants().IsAllowed(sid, entry.AgentID, entry.ToolName, args), "FR-038: no remembered grant under %s", sid)
			}
		})
	}
}

func TestQ7T22_OneRequestAndRefusalActionsGrantNothing_PositiveControl(t *testing.T) {
	for _, tc := range []struct {
		action   string
		state    ApprovalState
		approved bool
	}{
		{"allow_once", ApprovalStateApproved, true},
		{"deny", ApprovalStateDeniedUser, false},
		{"cancel", ApprovalStateDeniedCancel, false},
	} {
		t.Run(tc.action, func(t *testing.T) {
			api, reg := newTestRestAPIWithApprovalReg(t)
			args := q7Args("telegram", "reply_only", "q7-token", []string{})
			entry, accepted := reg.requestApproval("q7-actions", "set_channel_outbound", args, string(coreagent.IDOmnipus), "q7-actions", "q7-turn")
			require.True(t, accepted)
			w := postToolApproval(t, api, entry.ApprovalID, tc.action)
			require.Equal(t, http.StatusOK, w.Code, "Q3 A: the refusal actions remain valid: %s", w.Body.String())
			require.Equal(t, tc.state, reg.get(entry.ApprovalID).state)
			select {
			case outcome := <-entry.resultCh:
				require.Equal(t, tc.approved, outcome.Approved)
				require.False(t, outcome.RecordGrant)
			default:
				t.Fatal("instrument: a real terminal action must deliver its outcome")
			}
			require.False(t, api.agentLoop.ApprovalGrants().IsAllowed(entry.SessionID, entry.AgentID, entry.ToolName, args))
		})
	}
}

func TestQ7T22_OtherToolsKeepLastingAllow_PositiveControl(t *testing.T) {
	api, reg := newTestRestAPIWithApprovalReg(t)
	args := map[string]any{"id": "telegram", "action": "enable"}
	entry, accepted := reg.requestApproval("q7-other-allow", "enable_channel", args, string(coreagent.IDOmnipus), "q7-other-session", "q7-turn")
	require.True(t, accepted)
	w := postToolApproval(t, api, entry.ApprovalID, "allow")
	require.Equal(t, http.StatusOK, w.Code, "FR-038/NB-19: ordinary Allow retains its behavior")
	var response gen.ToolApprovalResponse
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.NotNil(t, response.GrantRecorded)
	require.True(t, *response.GrantRecorded)
	require.True(t, api.agentLoop.ApprovalGrants().IsAllowed(entry.SessionID, entry.AgentID, entry.ToolName, args))
}

func TestQ7T22_RealApprovalPathAsksAgainWithMatchingAndInheritedGrants(t *testing.T) {
	for _, scope := range []string{"none_positive_control", "exact", "inherited_exact"} {
		t.Run(scope, func(t *testing.T) {
			api, reg := newTestRestAPIWithApprovalReg(t)
			ws := &WSHandler{agentLoop: api.agentLoop, approvalRegV2: reg}
			api.agentLoop.SetToolApprover(newPolicyApproverAdapter(reg, ws))
			args := q7Args("telegram", "reply_only", "q7-identical-token", []string{})
			const sid = "q7-real-consent"
			grants := api.agentLoop.ApprovalGrants()
			switch scope {
			case "exact":
				require.True(t, grants.Record(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args))
			case "inherited_exact":
				require.True(t, grants.Record("q7-consent-parent", string(coreagent.IDOmnipus), "set_channel_outbound", args))
				grants.InheritFrom("q7-consent-parent", string(coreagent.IDOmnipus), sid, string(coreagent.IDOmnipus))
			}
			require.Equal(t, scope != "none_positive_control", grants.IsAllowed(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args), "instrument: actual grant/fingerprint precondition")
			var previousID string
			for _, callID := range []string{"q7-first", "q7-identical-repeat"} {
				ctx, cancel := context.WithCancel(context.Background())
				type verdict struct {
					approved bool
					reason   string
					record   bool
				}
				result := make(chan verdict, 1)
				joined := make(chan struct{})
				go func() {
					defer close(joined)
					ok, reason, record := api.agentLoop.CheckGrantOrRequestApproval(ctx, sid, string(coreagent.IDOmnipus), "set_channel_outbound", callID, "q7-turn", args)
					result <- verdict{ok, reason, record}
				}()
				// Cancellation and join run even when a require aborts this row.
				t.Cleanup(func() { cancel(); <-joined })
				var entry *approvalEntry
				deadline := time.NewTimer(5 * time.Second)
				ticker := time.NewTicker(time.Millisecond)
				for entry == nil {
					for _, pending := range reg.pendingApprovals() {
						if pending.ToolCallID == callID {
							entry = pending
							break
						}
					}
					if entry != nil {
						break
					}
					select {
					case early := <-result:
						deadline.Stop()
						ticker.Stop()
						cancel()
						<-joined
						t.Fatalf("FR-038/T-10 R14: returned without a fresh approval card for %s: %+v", callID, early)
					case <-deadline.C:
						ticker.Stop()
						t.Fatal("FR-038: real consent path produced neither a card nor a result")
					case <-ticker.C:
					}
				}
				deadline.Stop()
				ticker.Stop()
				require.NotEqual(t, previousID, entry.ApprovalID, "identical request gets a new approval id")
				require.Equal(t, args, entry.Args)
				require.Equal(t, sid, entry.SessionID)
				require.Equal(t, string(coreagent.IDOmnipus), entry.AgentID)
				previousID = entry.ApprovalID
				w := postToolApproval(t, api, entry.ApprovalID, "allow_once")
				require.Equal(t, http.StatusOK, w.Code)
				got := <-result
				cancel()
				<-joined
				require.True(t, got.approved)
				require.False(t, got.record, "FR-038: real endpoint approve-once installs no standing grant")
				require.Equal(t, scope != "none_positive_control", grants.IsAllowed(sid, string(coreagent.IDOmnipus), "set_channel_outbound", args), "old grants stay untouched; no new grant is recorded")
			}
		})
	}
}
