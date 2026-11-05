package gateway

// Oracles: /workspace/logs/channels-q7-dev/q2b/contract.md sections 1–6,
// including the final Q1 B amendment. The real loop, tool, approval registry,
// HTTP action, writer, config file and audit logger are never substituted.

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const cq2AfterCard = "This connection's settings changed after the card was shown; nothing was saved. Make the call again to see a new card with the current settings."

func cq2Args(id, policy string, targets []string) map[string]any {
	args := q7Args(id, policy, "", targets)
	delete(args, "expected_revision")
	return args
}

func cq2JSON(t *testing.T, value any) any {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	var normalized any
	require.NoError(t, json.Unmarshal(raw, &normalized))
	return normalized
}

func cq2Caller(t *testing.T, f *wu6Fixture, caller string, god, auto bool) string {
	t.Helper()
	sid := q7FollowupCaller(t, f, caller, god, auto)
	if caller != "omnipus" {
		store := agentstore.New(f.f1277.home)
		state, err := store.ReadState(caller)
		require.NoError(t, err)
		_, err = store.MutateState(caller, state.Revision, func(ac *config.AgentConfig) error {
			ac.Tools.Builtin.Policies["list_channels"] = config.ToolPolicyDeny
			return nil
		}, nil)
		require.NoError(t, err)
		require.NoError(t, f.api.refreshConfigAndRewireServices(f.f1277.configPath))
		_, err = f.al.UpsertAgentFast(f.al.GetConfig(), caller)
		require.NoError(t, err)
		q7WireRealTools(t, f)
		require.NoError(t, wireWU6ScriptedProviderPool(f.al, f.provider))
		inst, ok := f.al.GetRegistry().GetAgent(caller)
		require.True(t, ok)
		require.Equal(t, "deny", tools.ResolveEffectivePolicy(inst.LoadToolPolicy(), "list_channels"), "Q2-B: chat agent receives no channel-read permission")
		require.Equal(t, "ask", tools.ResolveEffectivePolicy(inst.LoadToolPolicy(), "set_channel_outbound"), "Q2-B: real chat-agent policy is Ask")
	}
	require.NotNil(t, f.al.AuditLogger(), "instrument: real boot audit logger enabled")
	return sid
}

func cq2Start(t *testing.T, f *wu6Fixture, sid, caller, callID string, args map[string]any) agent.EventSubscription {
	t.Helper()
	f.provider.setDefaultScript(q7Script(callID, args))
	return q7FollowupStartAs(t, f, sid, caller)
}

func cq2RequestCount(f *wu6Fixture, sid, callID string) int {
	f.reg.mu.Lock()
	defer f.reg.mu.Unlock()
	count := 0
	for _, entry := range f.reg.entries {
		if entry.SessionID == sid && entry.ToolCallID == callID {
			count++
		}
	}
	return count
}

func cq2Action(t *testing.T, f *wu6Fixture, entry *approvalEntry, action string) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"action": action})
	require.NoError(t, err)
	rec := t14Request(t, f, http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, string(body), true)
	require.Equal(t, http.StatusOK, rec.Code, "Q2-B: registered real approval action: %s", rec.Body.String())
}

func cq2NoCard(t *testing.T, f *wu6Fixture, events agent.EventSubscription, sid, callID string) {
	t.Helper()
	entry := q7FollowupPendingOrCompleted(t, f, events, sid, callID)
	if entry != nil {
		// Drain a wrongly-created card through the real refusal action. Never
		// approve an invalid call just to obtain the post-approval refusal.
		cq2Action(t, f, entry, "deny")
		q7WaitTurn(t, f, events, sid)
	}
	assert.Nil(t, entry, "Q2-B: validity failure must finish BEFORE a person is asked")
	assert.Zero(t, cq2RequestCount(f, sid, callID), "Q2-B: no approval request may have been created, including a resolved one")
	rows := q7AuditRead(t, f, sid, "set_channel_outbound", false)
	requests := 0
	for _, row := range rows {
		fields, _ := row["fields"].(map[string]any)
		if row["event"] == audit.EventToolPolicyAskRequested && fields["tool_call_id"] == callID {
			requests++
		}
	}
	assert.Zero(t, requests, "Q2-B: audit must independently show zero ask requests")
}

func cq2Failure(t *testing.T, text, code, reason string) map[string]any {
	t.Helper()
	failure := q7Error(t, tools.ErrorResult(text), code)
	if reason != "" {
		require.Equal(t, reason, failure["reason"], "Q2-B: precise refusal reason")
	}
	return failure
}

func cq2OutcomeRows(t *testing.T, f *wu6Fixture, sid, callID string) []map[string]any {
	t.Helper()
	rows := q7AuditRead(t, f, sid, "set_channel_outbound", false)
	out := []map[string]any{}
	for _, row := range rows {
		fields, _ := row["fields"].(map[string]any)
		if row["event"] == audit.EventChannelOutboundWrite && fields["tool_call_id"] == callID && fields["session_id"] == sid {
			out = append(out, fields)
		}
	}
	return out
}

func cq2RefusedAudit(t *testing.T, f *wu6Fixture, sid, caller, callID, id, reason string) {
	t.Helper()
	rows := cq2OutcomeRows(t, f, sid, callID)
	require.Len(t, rows, 1, "Q2-B section 3 / #1623: exactly one real pre-card outbound refusal audit")
	for key, want := range map[string]string{
		"outcome": "refused_before_approval", "reason": reason, "approval_id": "",
		"agent_id": caller, "tool_call_id": callID, "session_id": sid, "channel_id": id,
	} {
		require.Equal(t, want, rows[0][key], "Q2-B: audit field %s", key)
	}
	require.NotEmpty(t, rows[0]["turn_id"], "Q2-B: refusal is correlated to its actual turn")
}

func cq2Stored(t *testing.T, f *wu6Fixture, id, policy string, targets []string) {
	t.Helper()
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		inst := cfg.Channels[id]
		inst.Outbound = &config.ChannelOutboundConfig{Policy: policy, Targets: append([]string{}, targets...)}
		cfg.Channels[id] = inst
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
}

func cq2TranscriptCall(t *testing.T, f *wu6Fixture, sid, callID string) session.ToolCall {
	t.Helper()
	entries, err := f.al.GetSessionStore().ReadTranscript(sid)
	require.NoError(t, err)
	calls := []session.ToolCall{}
	for _, entry := range entries {
		for _, call := range entry.ToolCalls {
			if string(call.ID) == callID {
				calls = append(calls, call)
			}
		}
	}
	require.Len(t, calls, 1, "instrument: one real persisted call, not a fabricated observation")
	return calls[0]
}

// A non-mutating public AfterTool observer sees the SAME ex.toolArgs passed to
// the real registry and Execute; it neither wraps nor replaces the real tool.
type cq2ExecuteObserver struct {
	mu    sync.Mutex
	calls []*agent.ToolResultHookResponse
}

func (o *cq2ExecuteObserver) BeforeTool(_ context.Context, _ *agent.ToolCallHookRequest) (*agent.ToolCallHookRequest, agent.HookDecision, error) {
	return nil, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

func (o *cq2ExecuteObserver) AfterTool(_ context.Context, result *agent.ToolResultHookResponse) (*agent.ToolResultHookResponse, agent.HookDecision, error) {
	if result.Tool == "set_channel_outbound" {
		o.mu.Lock()
		o.calls = append(o.calls, result.Clone())
		o.mu.Unlock()
	}
	return nil, agent.HookDecision{Action: agent.HookActionContinue}, nil
}

// Structural interface deliberately lets RED compile before the new hook
// exists. A missing implementation is a loud assertion-level BLOCKED failure.
type cq2Preparer interface {
	PrepareForApproval(ctx context.Context, args map[string]any) (map[string]any, any, *tools.ToolResult)
}

func cq2RealPreparer(t *testing.T, f *wu6Fixture) cq2Preparer {
	t.Helper()
	tool, ok := f.api.builtinRegistry.Get("set_channel_outbound")
	require.True(t, ok, "instrument: real boot-wired tool")
	preparer, ok := tool.(cq2Preparer)
	if !ok {
		t.Fatal("BLOCKED: set_channel_outbound PrepareForApproval not implemented — required by Q2-B contract section 2 / cases 10 and 17")
	}
	return preparer
}
