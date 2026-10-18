package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/task"
	"github.com/stretchr/testify/require"
)

// Gate round 1 CR F06 / architect F2 follow-up (brief train3-red-gaps-r1):
// 8bb50d6bd tested only a HELD group request joining a web-started turn. These
// tests drive the peer and channel joins. Oracles: spec §6.18 ("a turn's
// origin is the set of inputs it consumed"), the chief ruling of 2026-10-18
// (channel > room > peer > web, never downgraded, first of a class keeps the
// label) as recorded in docs/security.md "Where a request came from", and
// amendment R6 (a card already pending keeps the label it was raised with).
// Labels are the spec's own strings: "asked by <Agent>" for a peer request,
// "while answering <person> on <connection>" for a channel message.
//
// Mechanics (read from the code, not assumed): an input arriving for a session
// with a live turn is enqueued as steering with its server-derived origin
// (session_worker.go::trySteerIntoLiveTurn → steering.go::
// enqueueSteeringFromMessage); the turn consumes queued steering at its
// post-tool boundary — the dequeue injects it into the next provider call —
// and only approvals raised AFTER that consumption carry the joined origin.
// A peer request's wake names the receiver MAIN session explicitly
// (address_router.go::publishRequestWake), and a channel conversation bound to
// the main chat stamps that session the same way; both join the live
// main-chat turn.

const train3GapsJoinContent = "also run the probe"

// train3GapsChannelJoinContent is the channel join's text, addressed to the
// live turn's session by train3GapsJoinChannel.
const train3GapsChannelJoinContent = "please also check the diff"

// train3GapsRecordingProvider wraps the scripted receiver provider and records
// every model-call's input messages, so a test can observe — at a real
// provider boundary — that a joined input reached the model context.
type train3GapsRecordingProvider struct {
	inner  *l11263Provider
	mu     sync.Mutex
	inputs [][]providers.Message
}

func (p *train3GapsRecordingProvider) Chat(ctx context.Context, msgs []providers.Message, defs []providers.ToolDefinition, model string, opts map[string]any) (*providers.LLMResponse, error) {
	p.mu.Lock()
	p.inputs = append(p.inputs, append([]providers.Message(nil), msgs...))
	p.mu.Unlock()
	return p.inner.Chat(ctx, msgs, defs, model, opts)
}

func (p *train3GapsRecordingProvider) GetDefaultModel() string { return p.inner.GetDefaultModel() }

// sawJoinInput reports whether any recorded model call already carried text
// (i.e. the joined input was consumed and injected at a post-tool boundary).
func (p *train3GapsRecordingProvider) sawJoinInput(fragment string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, call := range p.inputs {
		for _, msg := range call {
			if msg.Role == "system" {
				continue
			}
			if strings.Contains(msg.Content, fragment) {
				return true
			}
		}
	}
	return false
}

// train3GapsJoinReceiver wires the address-router peer path and returns the
// harness plus the receiver leaf's recorded provider and main session id
// (created the same way admitRequest creates it). The provider asks seven
// times, so enough cards observe the turn's origin as inputs join.
func train3GapsJoinReceiver(t *testing.T) (*l11263Harness, *train3GapsRecordingProvider, string) {
	t.Helper()
	h := newL11263Harness(t, false, config.ToolPolicyAsk, nil)
	h.ws.home = h.api.homePath
	h.api.agentLoop.SetAddressDeps(gatewayAddressDeps{h: h.ws})
	steps := make([]providers.ToolCall, 0, 7)
	for i := 1; i <= 7; i++ {
		steps = append(steps, l11263Call(
			"join-write-"+string(rune('0'+i)), "write_file",
			map[string]any{"path": l11263File, "content": strings.Repeat("x", i)}))
	}
	inner := l11263Script(steps...)
	recorded := &train3GapsRecordingProvider{inner: inner}
	rInst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Leaf)
	require.True(t, ok)
	rInst.Provider = recorded
	meta, err := h.api.agentLoop.GetSessionStore().GetOrCreateMainSession(testHarnessWorkspaceMembershipID, l11263Leaf)
	require.NoError(t, err)
	t.Cleanup(func() {
		for _, e := range h.reg.pendingApprovals() {
			h.reg.resolve(e.ApprovalID, ApprovalActionCancel, false)
		}
	})
	return h, recorded, meta.ID
}

// train3GapsStartReceiverWebTurn starts the person's own web turn in the
// leaf's main chat: a webchat message bound to that session (the same envelope
// shape publishRequestWake uses for the main session), so the live turn and
// every join below share one session.
func train3GapsStartReceiverWebTurn(t *testing.T, h *l11263Harness, mainID string) {
	t.Helper()
	require.NoError(t, h.ws.msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "webchat", ChatID: "webchat:" + mainID,
		SessionID: mainID,
		Content:   "Write the file",
		Metadata:  map[string]string{"agent_id": l11263Leaf, "workspace_id": testHarnessWorkspaceMembershipID},
	}))
}

// train3GapsJoinSender asks the leaf for work as a peer agent: one real
// send_message through the address router, landing while the leaf's main-chat
// turn is live (the receiver is blocked on its pending card, so the wake joins
// as steering, never starts a second turn). The sender runs as a real task —
// the same identity-complete dispatch the existing peer fixture uses — because
// a peer send needs the sender session's agent and workspace identity.
func train3GapsJoinSender(t *testing.T, h *l11263Harness) gen.Task {
	t.Helper()
	sender := l11263Script(
		l11263Call("join-peer-request", "send_message", map[string]any{
			"workspace_id": testHarnessWorkspaceMembershipID, "agent_id": l11263Leaf, "content": train3GapsJoinContent,
		}),
		l11263Claim(),
	)
	sInst, ok := h.api.agentLoop.GetRegistry().GetAgent(l11263Agent)
	require.True(t, ok)
	sInst.Provider = sender
	created := h.createTask(t, "")
	h.startBoard(t, created)
	return created
}

// train3GapsJoinChannel publishes one real channel person's message onto the
// bus while the receiver's turn is live, addressed to the session the live
// turn runs in (a channel conversation bound to the agent's main chat — the
// shared-main-chat case §6.18 names).
func train3GapsJoinChannel(t *testing.T, h *l11263Harness, sessionID string) {
	t.Helper()
	require.NoError(t, h.ws.msgBus.PublishInbound(context.Background(), bus.InboundMessage{
		Channel: "telegram", InstanceID: "telegram-ops", ChatID: "join-chat",
		SessionID: sessionID,
		Content:   train3GapsChannelJoinContent,
		Sender:    bus.SenderInfo{DisplayName: "Anna", CanonicalID: "telegram:anna"},
		Metadata:  map[string]string{"agent_id": l11263Leaf, "workspace_id": testHarnessWorkspaceMembershipID},
	}))
}

// train3GapsSnapshotPending reloads the reconnect snapshot on its OWN fake
// connection, registered under a key unique to this approval. The shared
// apprResAttachConns helper re-registers the fixed "tab-a" key, which would
// evict the harness tab from the broadcast audience and swallow every later
// card's live frame — these tests read several cards from the tab.
func train3GapsSnapshotPending(t *testing.T, h *l11263Harness, approvalID string) gen.SessionStatePendingApproval {
	t.Helper()
	h.ws.mu.Lock()
	wc := &wsConn{sendCh: make(chan []byte, 64), doneCh: make(chan struct{}), userID: "tab"}
	key := "train3-gaps-snap-" + approvalID
	h.ws.sessions[key] = wc
	h.ws.mu.Unlock()
	t.Cleanup(func() {
		h.ws.mu.Lock()
		delete(h.ws.sessions, key)
		h.ws.mu.Unlock()
	})
	h.ws.emitSessionState(wc, "")
	snapshot := apprResReadFrame(t, wc, "session_state")
	pending, ok := snapshot["pending_approvals"].([]any)
	require.True(t, ok, "pending_approvals must be an array, never null")
	for _, raw := range pending {
		item, ok := raw.(map[string]any)
		require.True(t, ok)
		if item["approval_id"] == approvalID {
			return wu8TypedFrame[gen.SessionStatePendingApproval](t, item)
		}
	}
	t.Fatalf("approval %s missing from the reconnect snapshot", approvalID)
	return gen.SessionStatePendingApproval{}
}

// train3GapsAwaitSenderSettled waits for the sender task to reach a terminal
// state. It reads the task store directly: the harness's awaitTask asserts no
// approval was EVER raised, which cannot hold here — the receiver's own cards
// were raised and resolved in the same registry.
func train3GapsAwaitSenderSettled(t *testing.T, h *l11263Harness, created gen.Task) {
	t.Helper()
	require.Eventually(t, func() bool {
		current, err := h.api.taskStore.Get(created.Id)
		return err == nil && task.IsTerminal(current.Status)
	}, 15*time.Second, 10*time.Millisecond, "the sender task settles")
}

// train3GapsNextLabelledCard resolves pending cards until the join text has
// reached the model context (observed at a real provider boundary), then
// returns the first card raised after that consumption. The caller passes the
// currently pending card and its already-read frame; every card resolved on
// the way was raised BEFORE the join was consumed, so the loop also proves the
// R6 rule against each of them: its label is still wantBefore (nil for a web
// turn, the peer label while only the peer input has been consumed, …).
func train3GapsNextLabelledCard(t *testing.T, h *l11263Harness, recorded *train3GapsRecordingProvider, joinFragment, wantBefore string, entry *approvalEntry, frame gen.ToolApprovalRequiredFrame) (*approvalEntry, gen.ToolApprovalRequiredFrame) {
	t.Helper()
	for attempt := 0; attempt < 6; attempt++ {
		if recorded.sawJoinInput(joinFragment) {
			require.NotNil(t, frame.OriginLabel,
				"the first card raised after the join reached the model context carries the joined origin")
			return entry, frame
		}
		if wantBefore == "" {
			require.Nil(t, frame.OriginLabel,
				"R6: a card raised before the join was consumed keeps its raised label")
		} else {
			require.NotNil(t, frame.OriginLabel)
			require.Equal(t, wantBefore, *frame.OriginLabel,
				"R6: a card raised before the next join was consumed keeps the earlier label")
		}
		resolved, gone := h.reg.resolve(entry.ApprovalID, ApprovalActionApprove, false)
		require.True(t, resolved && !gone, "the person answers the card")
		entry = h.awaitInteractiveApproval(t)
		frame = wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, apprResReadFrame(t, h.tab, "tool_approval_required"))
	}
	t.Fatalf("the joined input never reached the model context within the scripted asks")
	return nil, gen.ToolApprovalRequiredFrame{}
}

// TestTrain3Fix1_PeerInputJoiningAWebTurnLabelsLaterApprovals: a peer request
// joins a RUNNING web-started turn; the first card raised after the turn
// consumed it is labelled "asked by <Agent>", while every card raised before
// keeps the label-free web origin it was raised with (R6).
func TestTrain3Fix1_PeerInputJoiningAWebTurnLabelsLaterApprovals(t *testing.T) {
	h, recorded, mainID := train3GapsJoinReceiver(t)
	h.startPump(t)
	train3GapsStartReceiverWebTurn(t, h, mainID)

	card1Entry := h.awaitInteractiveApproval(t)
	card1 := wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, apprResReadFrame(t, h.tab, "tool_approval_required"))
	require.Equal(t, mainID, card1.SessionId, "fixture: the live turn runs in the leaf's main chat")
	require.Nil(t, card1.OriginLabel, "§6.18: a plain web turn's card carries no origin label")
	require.Nil(t, card1.RoomRef, "a web turn's card carries no room ref")

	senderTask := train3GapsJoinSender(t, h)

	// R6: the pending card keeps its raised (web, no) label while the peer
	// request waits in the steering queue.
	snap1 := train3GapsSnapshotPending(t, h, card1Entry.ApprovalID)
	require.Nil(t, snap1.OriginLabel, "R6: the pending card keeps its raised label while the peer input waits")

	card2Entry, card2 := train3GapsNextLabelledCard(t, h, recorded, train3GapsJoinContent, "", card1Entry, card1)
	require.NotNil(t, card2.OriginLabel,
		"§6.18: the turn consumed the peer request, so its later card names the asker")
	require.Equal(t, "asked by Approval Worker", *card2.OriginLabel)
	require.Nil(t, card2.RoomRef, "a peer request is not a room ask")
	snap2 := train3GapsSnapshotPending(t, h, card2Entry.ApprovalID)
	require.NotNil(t, snap2.OriginLabel, "the peer label survives the reload")
	require.Equal(t, card2.OriginLabel, snap2.OriginLabel, "§1.5: live and snapshot labels equal")

	resolved, gone := h.reg.resolve(card2Entry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone, "the person answers the labelled card")
	train3GapsAwaitSenderSettled(t, h, senderTask)
}

// TestTrain3Fix1_ChannelInputJoiningAPeerTurnLabelsLaterApprovals: a channel
// person's message joins a turn whose origin is already PEER; the label rises
// to the channel class ("while answering …"), and every still-pending card
// keeps the label it was raised with (R6) while the higher-class input waits.
func TestTrain3Fix1_ChannelInputJoiningAPeerTurnLabelsLaterApprovals(t *testing.T) {
	h, recorded, mainID := train3GapsJoinReceiver(t)
	h.startPump(t)
	train3GapsStartReceiverWebTurn(t, h, mainID)

	card1Entry := h.awaitInteractiveApproval(t)
	card1 := wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, apprResReadFrame(t, h.tab, "tool_approval_required"))
	require.Nil(t, card1.OriginLabel, "fixture: card 1 is the web-started turn's own ask")

	senderTask := train3GapsJoinSender(t, h)
	peerEntry, peerFrame := train3GapsNextLabelledCard(t, h, recorded, train3GapsJoinContent, "", card1Entry, card1)
	require.Equal(t, "asked by Approval Worker", *peerFrame.OriginLabel)

	// The channel person's message joins the peer-labelled live turn.
	train3GapsJoinChannel(t, h, mainID)

	// R6: the pending peer card keeps its raised label while the channel
	// input waits in the steering queue.
	snapPeer := train3GapsSnapshotPending(t, h, peerEntry.ApprovalID)
	require.NotNil(t, snapPeer.OriginLabel)
	require.Equal(t, "asked by Approval Worker", *snapPeer.OriginLabel,
		"R6: the pending card keeps its raised peer label while the channel input waits")

	channelEntry, channelFrame := train3GapsNextLabelledCard(t, h, recorded, train3GapsChannelJoinContent, "asked by Approval Worker", peerEntry, peerFrame)
	require.NotNil(t, channelFrame.OriginLabel,
		"§6.18: the consumed channel input raises the turn's label to the channel class")
	require.Equal(t, "while answering Anna on telegram-ops", *channelFrame.OriginLabel)
	snapChannel := train3GapsSnapshotPending(t, h, channelEntry.ApprovalID)
	require.Equal(t, channelFrame.OriginLabel, snapChannel.OriginLabel, "§1.5: live and snapshot labels equal")

	resolved, gone := h.reg.resolve(channelEntry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone, "the person answers the channel-labelled card")
	train3GapsAwaitSenderSettled(t, h, senderTask)
}

// TestTrain3Fix1_PeerInputNeverDropsAChannelLabel: a PEER request (a labelled
// origin of a LOWER class) joins a turn that is already channel-labelled; the
// label must NOT drop to "asked by …" — the never-downgraded half of the chief
// ruling, exercised with a labelled lower-class input (a plain web join is
// already refused by the empty-label guard alone).
func TestTrain3Fix1_PeerInputNeverDropsAChannelLabel(t *testing.T) {
	h, recorded, mainID := train3GapsJoinReceiver(t)
	h.startPump(t)
	train3GapsStartReceiverWebTurn(t, h, mainID)

	card1Entry := h.awaitInteractiveApproval(t)
	card1 := wu8TypedFrame[gen.ToolApprovalRequiredFrame](t, apprResReadFrame(t, h.tab, "tool_approval_required"))
	require.Nil(t, card1.OriginLabel, "fixture: card 1 is the web-started turn's own ask")

	// The channel person's message joins first; the turn becomes channel-labelled.
	train3GapsJoinChannel(t, h, mainID)
	channelEntry, channelFrame := train3GapsNextLabelledCard(t, h, recorded, train3GapsChannelJoinContent, "", card1Entry, card1)
	require.Equal(t, "while answering Anna on telegram-ops", *channelFrame.OriginLabel)

	// Now a peer request — a labelled LOWER-class origin — joins the same turn.
	senderTask := train3GapsJoinSender(t, h)

	// R6: the pending channel card keeps its raised label while the peer
	// request waits in the steering queue.
	snapChannel := train3GapsSnapshotPending(t, h, channelEntry.ApprovalID)
	require.NotNil(t, snapChannel.OriginLabel)
	require.Equal(t, "while answering Anna on telegram-ops", *snapChannel.OriginLabel,
		"R6: the pending card keeps its channel label while the peer input waits")

	peerEntry, peerFrame := train3GapsNextLabelledCard(t, h, recorded, train3GapsJoinContent,
		"while answering Anna on telegram-ops", channelEntry, channelFrame)
	require.NotNil(t, peerFrame.OriginLabel, "§6.18: the label never moves down to a lesser class")
	require.Equal(t, "while answering Anna on telegram-ops", *peerFrame.OriginLabel,
		"the consumed peer request leaves the channel label in place")
	snapPeer := train3GapsSnapshotPending(t, h, peerEntry.ApprovalID)
	require.Equal(t, peerFrame.OriginLabel, snapPeer.OriginLabel, "§1.5: live and snapshot labels equal")

	resolved, gone := h.reg.resolve(peerEntry.ApprovalID, ApprovalActionApprove, false)
	require.True(t, resolved && !gone, "the person answers the card")
	train3GapsAwaitSenderSettled(t, h, senderTask)
}
