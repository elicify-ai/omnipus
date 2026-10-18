package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/stretchr/testify/require"
)

// Rooms half of the joined-origin gap round (see
// train3_red_gaps_joined_origin_peer_test.go for the oracles and mechanics).
// These tests drive channel- and web-input joins on the wu15 rooms harness,
// where a turn can also start FROM a room request ("for group <name>").

// wu15JoinStep is one scripted model edge: a tool call (the ask stimulus) or
// nothing (a plain answer that ends the turn).
type wu15JoinStep struct {
	tool     string
	toolArgs map[string]any
}

// wu15JoinStepProvider scripts a SEQUENCE of model edges for one agent: each
// provider call returns the next step, so one turn can ask more than once and
// an input consumed between two asks is visible on the later card. A step
// advances per provider CALL, exactly like the l11263Provider — robust to the
// steering message the loop appends after a consumed join.
type wu15JoinStepProvider struct {
	mu    sync.Mutex
	steps []wu15JoinStep
	next  int
}

func (p *wu15JoinStepProvider) Chat(ctx context.Context, msgs []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	n := p.next
	p.next++
	p.mu.Unlock()
	if n >= len(p.steps) {
		return &providers.LLMResponse{Content: "Joined-input probe finished.", FinishReason: "stop"}, nil
	}
	step := p.steps[n]
	return &providers.LLMResponse{
		ToolCalls: []providers.ToolCall{{
			ID: fmt.Sprintf("join-step-%d", n), Type: "function",
			Name: step.tool, Arguments: step.toolArgs,
		}},
		FinishReason: "tool_calls",
	}, nil
}

func (*wu15JoinStepProvider) GetDefaultModel() string { return "wu15-unpriced" }

func (p *wu15JoinStepProvider) ChatStream(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any, onChunk func(string), onProgress providers.OnToolCallProgress) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, msgs, defs, model, opts)
	return wu15StreamResponse(response, err, onChunk)
}

// wu15JoinAskSteps scripts two probe asks (the second is the post-join card).
func wu15JoinAskSteps() []wu15JoinStep {
	return []wu15JoinStep{
		{tool: askProbeToolName, toolArgs: map[string]any{}},
		{tool: askProbeToolName, toolArgs: map[string]any{}},
	}
}

// wu15JoinSetProvider installs the stepped provider through the same
// per-instance seam the harness's own provider setter uses (the model edge is
// the only scripted boundary).
func wu15JoinSetProvider(t *testing.T, h *wu15RoomHarness, id string, p *wu15JoinStepProvider) {
	t.Helper()
	inst, ok := h.al.GetRegistry().GetAgent(id)
	require.True(t, ok, "SETUP: registered agent %s", id)
	inst.Provider = p
	inst.StoreProviderPool(map[string]providers.LLMProvider{})
}

// wu15JoinPublishTelegram publishes one channel person's message onto the real
// bus while mia's turn is live, addressed to the session the live turn runs in
// (a channel conversation bound to the agent's shared main chat — the case
// §6.18 names alongside the per-person conversation; without the session stamp
// the router opens a fresh per-chat session instead of joining the turn).
func wu15JoinPublishTelegram(t *testing.T, h *wu15RoomHarness, person, instance, text string) {
	t.Helper()
	require.NoError(t, h.bus.PublishInbound(h.ctx, bus.InboundMessage{
		Channel: "telegram", InstanceID: instance, ChatID: "join-" + instance,
		SessionID: h.main("mia"),
		Content:   text,
		Sender:    bus.SenderInfo{DisplayName: person, CanonicalID: "telegram:" + strings.ToLower(person)},
		Metadata:  map[string]string{"agent_id": "mia", "workspace_id": wu15Workspace},
	}))
}

// wu15JoinPending reloads the reconnect snapshot on a fresh connection
// attached to the receiver main and returns the pending card for approvalID.
func wu15JoinPending(t *testing.T, h *wu15RoomHarness, approvalID string) gen.SessionStatePendingApproval {
	t.Helper()
	reloaded := h.connect()
	require.NoError(t, reloaded.WriteJSON(gen.AttachSessionFrame{Type: "attach_session", SessionId: h.main("mia")}))
	var state gen.SessionStateFrame
	require.NoError(t, json.Unmarshal(wu15ReadFrame(t, reloaded, "session_state"), &state))
	for _, pending := range state.PendingApprovals {
		if pending.ApprovalId == approvalID {
			return pending
		}
	}
	t.Fatalf("approval %s missing from the reload snapshot", approvalID)
	return gen.SessionStatePendingApproval{}
}

// wu15JoinApprove answers one pending card through the real approval endpoint.
func wu15JoinApprove(t *testing.T, h *wu15RoomHarness, approvalID string) {
	t.Helper()
	status, body := h.http(http.MethodPost, "/api/v1/tool-approvals/"+approvalID,
		gen.ToolApprovalActionRequest{Action: gen.ToolApprovalActionRequestActionAllowOnce})
	require.Equal(t, http.StatusOK, status, "approve %s: %s", approvalID, body)
}

// TestTrain3Fix1_ChannelInputJoiningAWebTurnLabelsLaterApprovals: a channel
// person's message joins a RUNNING web-started turn (card 1 raised before, no
// label); the card raised after the turn consumed it carries "while answering
// <person> on <connection>", equal on the reload snapshot, and the pending
// card 1 keeps its raised label-free origin while the input waits (R6).
func TestTrain3Fix1_ChannelInputJoiningAWebTurnLabelsLaterApprovals(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	_ = room
	wu15JoinSetProvider(t, h, "mia", &wu15JoinStepProvider{steps: wu15JoinAskSteps()})
	conn := h.connect()
	h.startLoop()
	h.sendMain(conn, "mia", "start the report")

	card1 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.Equal(t, h.main("mia"), card1.SessionId, "fixture: the web turn runs in the receiver main")
	require.Nil(t, card1.OriginLabel, "§6.18: a plain web turn's card carries no origin label")

	wu15JoinPublishTelegram(t, h, "Anna", "telegram-ops", "also add the chart")

	snap1 := wu15JoinPending(t, h, card1.ApprovalId)
	require.Nil(t, snap1.OriginLabel,
		"R6: the pending card keeps its raised (web, no) label while the channel input waits")

	wu15JoinApprove(t, h, card1.ApprovalId)

	card2 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.NotNil(t, card2.OriginLabel,
		"§6.18: the turn consumed the channel input, so its later card names the person and the connection")
	require.Equal(t, "while answering Anna on telegram-ops", *card2.OriginLabel)
	snap2 := wu15JoinPending(t, h, card2.ApprovalId)
	require.Equal(t, card2.OriginLabel, snap2.OriginLabel, "§1.5: the label survives the reload equal")

	wu15JoinApprove(t, h, card2.ApprovalId)
}

// TestTrain3Fix1_ChannelInputJoiningARoomTurnLabelsLaterApprovals: a channel
// person's message joins a turn that STARTED from a room request; the label
// rises from "for group <name>" to the channel class, the still-pending room
// card keeps its raised label and room ref (R6), and the post-join card
// carries no room ref (the channel origin is not a room ask).
func TestTrain3Fix1_ChannelInputJoiningARoomTurnLabelsLaterApprovals(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	wu15JoinSetProvider(t, h, "mia", &wu15JoinStepProvider{steps: wu15JoinAskSteps()})
	conn := h.connect()
	h.attach(conn, room.Id, 0)
	h.startLoop()
	h.post(room.Id, "channel-join-ask", "@Mia write the report")

	card1 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.NotNil(t, card1.OriginLabel, "K7/§6.18: a room member turn's card is labelled for its group")
	require.Equal(t, "for group "+room.Name, *card1.OriginLabel)
	require.NotNil(t, card1.RoomRef)
	require.Equal(t, room.Id, card1.RoomRef.RoomId)

	wu15JoinPublishTelegram(t, h, "Anna", "telegram-ops", "also add the chart")

	snap1 := wu15JoinPending(t, h, card1.ApprovalId)
	require.NotNil(t, snap1.OriginLabel)
	require.Equal(t, "for group "+room.Name, *snap1.OriginLabel,
		"R6: the pending card keeps its raised room label while the channel input waits")

	wu15JoinApprove(t, h, card1.ApprovalId)

	card2 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.NotNil(t, card2.OriginLabel, "§6.18: the consumed channel input raises the label to the channel class")
	require.Equal(t, "while answering Anna on telegram-ops", *card2.OriginLabel)
	require.Nil(t, card2.RoomRef, "a channel-origin card is not a room ask")
	snap2 := wu15JoinPending(t, h, card2.ApprovalId)
	require.Equal(t, card2.OriginLabel, snap2.OriginLabel, "§1.5: the label survives the reload equal")

	wu15JoinApprove(t, h, card2.ApprovalId)
}

// TestTrain3Fix1_WebInputNeverDropsAChannelLabel: after a channel input made
// the turn channel-labelled, a plain web message joins the same running turn;
// the label must NOT move down — the next card stays "while answering …"
// (chief ruling: precedence channel > room > peer > web, never downgraded).
func TestTrain3Fix1_WebInputNeverDropsAChannelLabel(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	_ = room
	wu15JoinSetProvider(t, h, "mia", &wu15JoinStepProvider{steps: []wu15JoinStep{
		{tool: askProbeToolName, toolArgs: map[string]any{}},
		{tool: askProbeToolName, toolArgs: map[string]any{}},
		{tool: askProbeToolName, toolArgs: map[string]any{}},
	}})
	conn := h.connect()
	h.startLoop()
	h.sendMain(conn, "mia", "start the report")

	card1 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.Nil(t, card1.OriginLabel, "fixture: card 1 is the web turn's own ask")

	wu15JoinPublishTelegram(t, h, "Anna", "telegram-ops", "also add the chart")
	wu15JoinApprove(t, h, card1.ApprovalId)

	card2 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.NotNil(t, card2.OriginLabel)
	require.Equal(t, "while answering Anna on telegram-ops", *card2.OriginLabel)

	// A plain web message joins the channel-labelled live turn.
	h.sendMain(conn, "mia", "actually keep it short")

	snap2 := wu15JoinPending(t, h, card2.ApprovalId)
	require.NotNil(t, snap2.OriginLabel)
	require.Equal(t, "while answering Anna on telegram-ops", *snap2.OriginLabel,
		"R6: the pending card keeps its channel label while the web input waits")

	wu15JoinApprove(t, h, card2.ApprovalId)

	card3 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.NotNil(t, card3.OriginLabel, "§6.18: the label never moves down to web")
	require.Equal(t, "while answering Anna on telegram-ops", *card3.OriginLabel,
		"the consumed web input leaves the channel label in place")

	wu15JoinApprove(t, h, card3.ApprovalId)
}

// TestTrain3Fix1_FirstChannelInputOfAClassKeepsTheLabel: two channel inputs
// from DIFFERENT persons on DIFFERENT connections join the same running turn;
// within a class the FIRST consumed input keeps the label — the card names
// Anna on telegram-ops, never Ben on telegram-personal.
func TestTrain3Fix1_FirstChannelInputOfAClassKeepsTheLabel(t *testing.T) {
	h, room, _ := l1WU8RoomsSetup(t)
	_ = room
	wu15JoinSetProvider(t, h, "mia", &wu15JoinStepProvider{steps: wu15JoinAskSteps()})
	conn := h.connect()
	h.startLoop()
	h.sendMain(conn, "mia", "start the report")

	card1 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.Nil(t, card1.OriginLabel, "fixture: card 1 is the web turn's own ask")

	wu15JoinPublishTelegram(t, h, "Anna", "telegram-ops", "first join")
	wu15JoinPublishTelegram(t, h, "Ben", "telegram-personal", "second join")

	snap1 := wu15JoinPending(t, h, card1.ApprovalId)
	require.Nil(t, snap1.OriginLabel, "R6: card 1 keeps its raised label while both joins wait")

	wu15JoinApprove(t, h, card1.ApprovalId)

	card2 := l1WU8RoomsFrame[gen.ToolApprovalRequiredFrame](t, wu15ReadFrame(t, conn, "tool_approval_required"))
	require.NotNil(t, card2.OriginLabel)
	require.Equal(t, "while answering Anna on telegram-ops", *card2.OriginLabel,
		"chief ruling: within a class the FIRST consumed input keeps the label")

	wu15JoinApprove(t, h, card2.ApprovalId)
}
