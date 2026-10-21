package gateway

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// The attention-gap RED (founder ruling 2026-10-18: no banner; an approval
// must trigger the attention signals and the main agent's sidebar entry —
// Session.needs_attention on a MAIN). attention_ack.go::
// pendingApprovalMainsFor walks ParentSessionID up to the first MAIN, so
// these chains get no cue anywhere:
//
//	GAP 1: a channel turn in per-person conversation mode (a
//	       SessionTypeChannel session, no parent);
//	GAP 2: a direct chat in a NON-main chat session (SessionTypeChat,
//	       no parent), and a helper under it.
//
// Ruling: mark the root agent's main in the root's workspace. The oracle
// surface is the real sidebar listing, GET /api/v1/sessions, where a valid
// main row carries needs_attention true or false (omitted = unknown), plus
// the live approval card itself. The fake is the paid provider edge only.

type l1AttentionHarness struct {
	h     *wu15RoomHarness
	probe *askProbeTool
	conn  *websocket.Conn
}

func l1AttentionSetup(t *testing.T) *l1AttentionHarness {
	t.Helper()
	h := wu15NewHarness(t)
	// The wu15 boot wires no delegation mesh: mia→jim must exist for the
	// real delegate call in the helper cases (ADR-037 workspace graph).
	require.NoError(t, workspace.SaveDelegation(h.home, wu15Workspace, []workspace.DelegationEdge{
		{FromAgent: "mia", ToAgent: "jim"},
	}))
	probe := &askProbeTool{}
	h.al.RegisterTool(probe)
	// Per-agent policies through the live store: the probe calls are Ask —
	// the approval stimulus — and mia may delegate and report. Nothing else
	// is loosened.
	for _, agentPolicies := range []struct {
		id       string
		policies map[string]config.ToolPolicy
	}{
		{"mia", map[string]config.ToolPolicy{"delegate": config.ToolPolicyAllow, "message_parent": config.ToolPolicyAllow, askProbeToolName: config.ToolPolicyAsk}},
		{"jim", map[string]config.ToolPolicy{"message_parent": config.ToolPolicyAllow, askProbeToolName: config.ToolPolicyAsk}},
		{"ray", map[string]config.ToolPolicy{askProbeToolName: config.ToolPolicyAsk}},
	} {
		inst, ok := h.al.GetRegistry().GetAgent(agentPolicies.id)
		require.True(t, ok)
		policy := *inst.LoadToolPolicy()
		policy.Policies = agentPolicies.policies
		inst.StoreToolPolicy(&policy)
	}
	// A bound channel instance for GAP 1's per-person conversation (the
	// intake reads the workspace binding live from the config pointer).
	h.cfg.Channels = map[string]config.ChannelInstanceConfig{
		"telegram-ops": {Type: "telegram", Enabled: true, WorkspaceID: wu15Workspace},
	}
	conn := h.connect()
	// Post-auth sync: an approval frame can beat the async auth handshake on
	// a fast turn, and a pre-auth frame is dropped. Attaching a session once
	// gives a deterministic in-order point after auth.
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	wu15ReadFrame(t, conn, "session_state")
	h.startLoop()
	return &l1AttentionHarness{h: h, probe: probe, conn: conn}
}

// l1AskAsk scripts agent's next turn to call the ask probe.
func (a *l1AttentionHarness) l1AskAsk(t *testing.T, agentID string) {
	t.Helper()
	a.h.provider(agentID, &wu15ScriptProvider{tool: askProbeToolName, toolArgs: map[string]any{}})
}

// l1AskDelegate scripts agent's next turn to delegate to target.
func (a *l1AttentionHarness) l1AskDelegate(t *testing.T, agentID, target string) {
	t.Helper()
	a.h.provider(agentID, &wu15ScriptProvider{tool: "delegate", toolArgs: map[string]any{
		"agent_id": target, "task": "write the report", "critical": true,
	}})
}

// l1Ask drives one turn for agent in sessionID through the real inbound
// intake — the same envelope the WebSocket chat path publishes for a
// session-bound message.
func (a *l1AttentionHarness) l1Ask(t *testing.T, agentID, sessionID, content string) {
	t.Helper()
	require.NoError(t, a.h.bus.PublishInbound(a.h.ctx, bus.InboundMessage{
		Channel: "webchat", ChatID: sessionID, SessionID: sessionID,
		Content: content,
		Sender:  bus.SenderInfo{DisplayName: "wu15-tester", CanonicalID: "wu15-tester"},
		Metadata: map[string]string{
			"agent_id": agentID, "workspace_id": wu15Workspace, "gateway_user_id": "wu15-tester",
		},
	}))
}

// l1AwaitCard waits for the pending approval card and returns it.
func (a *l1AttentionHarness) l1AwaitCard(t *testing.T) gen.ToolApprovalRequiredFrame {
	t.Helper()
	return l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, a.conn, "tool_approval_required"))
}

// l1MainNeedsAttention reads the real sidebar listing and returns mia's
// main row: (found, needsAttention value, present).
func (a *l1AttentionHarness) l1MainNeedsAttention(t *testing.T) (bool, bool, bool) {
	t.Helper()
	status, data := a.h.http(http.MethodGet, "/api/v1/sessions", nil)
	require.Equal(t, http.StatusOK, status, "sessions listing: %s", data)
	var listing struct {
		Sessions []gen.Session `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(data, &listing), "%s", data)
	rows := listing.Sessions
	mainID := a.h.main("mia")
	for _, row := range rows {
		if row.Id == mainID {
			return true, row.NeedsAttention != nil && *row.NeedsAttention, row.NeedsAttention != nil
		}
	}
	return false, false, false
}

// l1RequireMainMarked is the ruling's oracle: while the approval pends, the
// agent's main row carries needs_attention true (present, never omitted).
func (a *l1AttentionHarness) l1RequireMainMarked(t *testing.T) {
	t.Helper()
	require.Eventually(t, func() bool {
		found, value, _ := a.l1MainNeedsAttention(t)
		return found && value
	}, 15*time.Second, 20*time.Millisecond,
		"the founder ruling: a pending approval must set needs_attention true on the agent's main")
}

// l1PendingOnFreshAttach attaches a fresh connection to sessionID and
// returns the first pending approval card from its session_state.
func (a *l1AttentionHarness) l1PendingOnFreshAttach(t *testing.T, sessionID string) gen.ToolApprovalRequiredFrame {
	t.Helper()
	conn := a.h.connect()
	require.NoError(t, conn.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: sessionID}))
	var state gen.SessionStateFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, conn, "session_state"), &state))
	require.NotEmpty(t, state.PendingApprovals, "the pending approval must appear on a fresh attach")
	// This base predates B3's required snapshot fields, so the approval id
	// alone identifies the card for the approve call.
	return gen.ToolApprovalRequiredFrame{ApprovalId: state.PendingApprovals[0].ApprovalId}
}

// l1ApproveCard answers the pending card through the real approval endpoint.
func (a *l1AttentionHarness) l1ApproveCard(t *testing.T, card gen.ToolApprovalRequiredFrame) {
	t.Helper()
	status, body := a.h.http(http.MethodPost, "/api/v1/tool-approvals/"+card.ApprovalId,
		gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.Equal(t, http.StatusOK, status, "%s", body)
}

// l1NonMainChat creates a NON-main chat session through the real POST
// /api/v1/sessions (the SPA's own session-creation call) and pins its shape.
func (a *l1AttentionHarness) l1NonMainChat(t *testing.T, agentID string) string {
	t.Helper()
	status, data := a.h.http(http.MethodPost, "/api/v1/sessions", map[string]any{
		"agent_id": agentID, "workspace_id": wu15Workspace,
	})
	require.Equal(t, http.StatusCreated, status, "session create: %s", data)
	var created struct {
		ID string `json:"id"`
	}
	require.NoError(t, json.Unmarshal(data, &created))
	require.NotEmpty(t, created.ID)
	want, err := session.MainSessionID(wu15Workspace, agentID)
	require.NoError(t, err)
	require.NotEqual(t, want, created.ID, "fixture: a non-main chat session")
	meta, err := a.h.al.GetSessionStore().GetMeta(created.ID)
	require.NoError(t, err)
	require.Equal(t, session.SessionTypeChat, meta.Type, "fixture: SessionTypeChat, no parent")
	require.Empty(t, meta.ParentSessionID)
	return created.ID
}

func TestAttentionGap_ChannelPersonSessionMarksTheMain(t *testing.T) {
	a := l1AttentionSetup(t)
	a.l1AskAsk(t, "mia")
	// GAP 1: a per-person channel conversation — the real channel intake
	// creates a SessionTypeChannel session with no parent.
	require.NoError(t, a.h.bus.PublishInbound(a.h.ctx, bus.InboundMessage{
		Channel: "telegram", InstanceID: "telegram-ops", ChatID: "anna-chat",
		Content: "Write the report",
		Sender:  bus.SenderInfo{DisplayName: "Anna", CanonicalID: "telegram:anna"},
		Metadata: map[string]string{
			"agent_id": "mia", "workspace_id": wu15Workspace,
		},
	}))
	card := a.l1AwaitCard(t)
	require.Equal(t, "mia", card.AgentId)
	meta, err := a.h.al.GetSessionStore().GetMeta(card.SessionId)
	require.NoError(t, err)
	require.Equal(t, session.SessionTypeChannel, meta.Type, "fixture: a channel per-person session")
	require.Empty(t, meta.ParentSessionID, "GAP 1: the channel session has no parent to walk")
	a.l1RequireMainMarked(t)
	a.l1ApproveCard(t, card)
}

func TestAttentionGap_NonMainChatSessionMarksTheMain(t *testing.T) {
	a := l1AttentionSetup(t)
	a.l1AskAsk(t, "mia")
	// GAP 2: a direct chat in a NON-main chat session (no parent).
	chatID := a.l1NonMainChat(t, "mia")
	a.l1Ask(t, "mia", chatID, "Write the report")
	card := a.l1AwaitCard(t)
	require.Equal(t, chatID, card.SessionId, "the card lives in the non-main chat session")
	a.l1RequireMainMarked(t)
	a.l1ApproveCard(t, card)
}

func TestAttentionGap_HelperUnderNonMainChatMarksTheMain(t *testing.T) {
	a := l1AttentionSetup(t)
	// GAP 2 + helper: the non-main chat delegates; the helper's ask is one
	// more link the walk must resolve back to the root agent's main.
	a.l1AskDelegate(t, "mia", "jim")
	a.l1AskAsk(t, "jim")
	chatID := a.l1NonMainChat(t, "mia")
	a.l1Ask(t, "mia", chatID, "Delegate the report")
	card := a.l1AwaitCard(t)
	require.Equal(t, "jim", card.AgentId, "the helper's own ask is the pending approval")
	a.l1RequireMainMarked(t)
	a.l1ApproveCard(t, card)
}

func TestAttentionGap_DirectInMainStillMarksTheMain(t *testing.T) {
	a := l1AttentionSetup(t)
	a.l1AskAsk(t, "mia")
	// Control: an approval directly in the main marks it (existing
	// behaviour; must keep passing). The conn attached to the acting session
	// gets the card via the inline/replay path rather than the broadcast
	// frame, so the card is read from a fresh attach's session_state.
	a.l1Ask(t, "mia", a.h.main("mia"), "Write the report")
	a.l1RequireMainMarked(t)
	card := a.l1PendingOnFreshAttach(t, a.h.main("mia"))
	a.l1ApproveCard(t, card)
	require.Eventually(t, func() bool { return a.probe.runs() == 1 }, 30*time.Second, 10*time.Millisecond,
		"the approved in-main ask really runs")
}

func TestAttentionGap_HelperUnderMainStillMarksTheMain(t *testing.T) {
	a := l1AttentionSetup(t)
	// Control: a helper under the main marks it (existing behaviour).
	a.l1AskDelegate(t, "mia", "jim")
	a.l1AskAsk(t, "jim")
	a.l1Ask(t, "mia", a.h.main("mia"), "Delegate the report")
	card := a.l1AwaitCard(t)
	require.Equal(t, "jim", card.AgentId)
	a.l1RequireMainMarked(t)
	a.l1ApproveCard(t, card)
}

func TestAttentionGap_NoMainGivesNoCueButTheCardShows(t *testing.T) {
	a := l1AttentionSetup(t)
	a.l1AskAsk(t, "ray")
	// Ray has no main in sales (his membership is marketing): no cue is
	// correct — but the card still shows in its own chat.
	chatID := a.l1NonMainChat(t, "ray")
	a.l1Ask(t, "ray", chatID, "Write the report")
	card := a.l1AwaitCard(t)
	require.Equal(t, "ray", card.AgentId)
	status, data := a.h.http(http.MethodGet, "/api/v1/sessions", nil)
	require.Equal(t, http.StatusOK, status, "%s", data)
	var listing struct {
		Sessions []gen.Session `json:"sessions"`
	}
	require.NoError(t, json.Unmarshal(data, &listing), "%s", data)
	rows := listing.Sessions
	sawRayMain := false
	for _, row := range rows {
		if row.AgentId != "ray" {
			continue
		}
		if row.Type == "main" {
			sawRayMain = true
		}
		require.True(t, row.NeedsAttention == nil || !*row.NeedsAttention,
			"no-main: no ray row may claim an attention cue")
	}
	require.True(t, sawRayMain, "fixture: ray's own (marketing) main is listed and stays unmarked")
}
