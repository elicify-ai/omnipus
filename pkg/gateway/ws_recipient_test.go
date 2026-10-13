package gateway

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/addressing"
	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// Session-core U8 over the WebSocket intake: MessageFrame.recipient (C-ADDRESS,
// FR-045; BDD-08.1/08.2). Oracles: a refused recipient admits nothing (not even
// the user's message); an admitted one goes to the recipient's main as a
// request whose answer returns to the owning chat authored by the guest, with
// the owner unchanged and no turn started for the owner.

type recipientDeps struct {
	mu        sync.Mutex
	eligible  map[string]bool
	published []session.TranscriptEntry
}

func (d *recipientDeps) PairEligible(ws, a string) (bool, error) { return d.eligible[ws+"/"+a], nil }
func (d *recipientDeps) PublishGuestReply(_ string, e session.TranscriptEntry) {
	d.mu.Lock()
	d.published = append(d.published, e)
	d.mu.Unlock()
}

func newRecipientHandler(t *testing.T) (*WSHandler, *bus.MessageBus, *recipientDeps) {
	t.Helper()
	return newRecipientHandlerWith(t)
}

// newRecipientHandlerWith is newRecipientHandler plus extra registered agents
// that allow send_message (so only the address, not the policy, can refuse).
func newRecipientHandlerWith(t *testing.T, extra ...string) (*WSHandler, *bus.MessageBus, *recipientDeps) {
	t.Helper()
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	msgBus := bus.NewMessageBus()
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080, DevModeBypass: true},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{Home: t.TempDir(), DefaultModel: config.DefaultModel{Model: "m"}, MaxTokens: 4096},
			List:     append([]config.AgentConfig{{ID: "ann"}, {ID: "ray"}}, extraAgents(extra)...),
		},
	}
	al := mustAgentLoop(t, cfg, msgBus, &restMockProvider{})
	handler := newWSHandler(msgBus, al, "")
	t.Cleanup(handler.Wait)
	deps := &recipientDeps{eligible: map[string]bool{"ws-1/ray": true}}
	al.SetAddressDeps(deps)
	inst, ok := al.GetRegistry().GetAgent("ray")
	require.True(t, ok)
	inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAllow}})
	for _, id := range extra {
		x, found := al.GetRegistry().GetAgent(id)
		require.True(t, found)
		x.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{"send_message": config.ToolPolicyAllow}})
	}
	return handler, msgBus, deps
}

func extraAgents(ids []string) []config.AgentConfig {
	out := make([]config.AgentConfig, 0, len(ids))
	for _, id := range ids {
		out = append(out, config.AgentConfig{ID: id})
	}
	return out
}

func nextInbound(t *testing.T, b *bus.MessageBus, wait time.Duration) (bus.InboundMessage, bool) {
	t.Helper()
	select {
	case m := <-b.InboundChan():
		return m, true
	case <-time.After(wait):
		return bus.InboundMessage{}, false
	}
}

// mintOwnerChat mints ann's chat in ws-1 and drains the turn it publishes.
func mintOwnerChat(t *testing.T, h *WSHandler, b *bus.MessageBus, wc *wsConn) string {
	t.Helper()
	h.handleChatMessage(context.Background(), "chat-owner", "", "hello", "ann", nil, "", "ws-1", false, wc)
	first, ok := nextInbound(t, b, 2*time.Second)
	require.True(t, ok, "owner chat turn not published")
	require.NotEmpty(t, first.SessionID)
	return first.SessionID
}

func TestRecipient_AdmitsRequestToPeerMainAndLeavesOwnerTurnUnstarted(t *testing.T) {
	h, b, _ := newRecipientHandler(t)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)

	to := addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}
	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@ray please review X", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

	in, ok := nextInbound(t, b, 2*time.Second)
	require.True(t, ok, "the recipient's turn must be started")
	wantMain, _ := session.MainSessionID("ws-1", "ray")
	assert.Equal(t, wantMain, in.SessionID)
	assert.Equal(t, "ray", in.Metadata["agent_id"])
	assert.False(t, in.UserInitiated)
	_, second := nextInbound(t, b, 300*time.Millisecond)
	assert.False(t, second, "the owning chat's agent must not get a turn for an @request")

	store := h.agentLoop.ResolveSessionStore(sid)
	owner, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	var saved bool
	for _, e := range owner {
		if e.Role == "user" && e.Content == "@ray please review X" {
			saved = true
		}
	}
	assert.True(t, saved, "the user's @-message stays verbatim in the owning chat")

	reqs, err := store.ReadTranscript(wantMain)
	require.NoError(t, err)
	require.Len(t, reqs, 1)
	assert.Equal(t, in.TranscriptEntryID, reqs[0].ID)
	assert.Contains(t, reqs[0].Content, "please review X")

	// The answer returns to the owning chat, authored by the guest.
	rec, err := h.agentLoop.NewAddressRouter().Reply(context.Background(), tools.ReplyRequest{
		ReplyTo: reqs[0].ID, Content: "reviewed",
		Author:             tools.SendOrigin{WorkspaceID: "ws-1", AgentID: "ray"},
		ResponderSessionID: wantMain,
	})
	require.NoError(t, err)
	owner, err = store.ReadTranscript(sid)
	require.NoError(t, err)
	last := owner[len(owner)-1]
	assert.Equal(t, rec.ReplyID, last.ID)
	assert.Equal(t, "ray", last.AgentID)
	assert.Equal(t, reqs[0].ID, last.ReplyToMessageID)
	meta, err := store.GetMeta(sid)
	require.NoError(t, err)
	assert.Equal(t, "ann", meta.AgentID, "owner unchanged")
}

func TestRecipient_RefusalsAdmitNothingAndSaveNoMessage(t *testing.T) {
	cases := map[string]struct {
		sessionID func(owner string) string
		to        addressing.Pair
		setup     func(*recipientDeps)
	}{
		"no session_id":    {func(string) string { return "" }, addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}, nil},
		"unknown session":  {func(string) string { return "missing-session" }, addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}, nil},
		"ineligible pair":  {func(o string) string { return o }, addressing.Pair{WorkspaceID: "ws-1", AgentID: "ray"}, func(d *recipientDeps) { d.eligible = map[string]bool{} }},
		"owner's own pair": {func(o string) string { return o }, addressing.Pair{WorkspaceID: "ws-1", AgentID: "ann"}, func(d *recipientDeps) { d.eligible["ws-1/ann"] = true }},
		"malformed pair":   {func(o string) string { return o }, addressing.Pair{WorkspaceID: "ws-1"}, nil},
		"other-workspace":  {func(o string) string { return o }, addressing.Pair{WorkspaceID: "ws-2", AgentID: "ray"}, nil},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			h, b, deps := newRecipientHandler(t)
			wc := makeTestConn()
			sid := mintOwnerChat(t, h, b, wc)
			if tc.setup != nil {
				tc.setup(deps)
			}
			store := h.agentLoop.ResolveSessionStore(sid)
			before, err := store.ReadTranscript(sid)
			require.NoError(t, err)
			to := tc.to

			h.handleChatMessageToRecipient(context.Background(), "chat-owner", tc.sessionID(sid), "@x do it", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

			_, started := nextInbound(t, b, 300*time.Millisecond)
			assert.False(t, started, "a refused recipient must start no turn")
			after, err := store.ReadTranscript(sid)
			require.NoError(t, err)
			assert.Len(t, after, len(before), "a refused recipient must not save the user's message")
			for _, e := range after {
				assert.False(t, strings.Contains(e.Content, "@x do it"))
			}
			mainID, _ := session.MainSessionID(to.WorkspaceID, to.AgentID)
			if mainID != "" {
				if _, err := store.GetMeta(mainID); err == nil {
					t.Error("a refused recipient created the recipient's main session")
				}
			}
		})
	}
}

var _ agent.AddressDeps = (*recipientDeps)(nil)

// F7: a pair whose components are each valid but whose computed main id is over
// the 255-byte limit is refused BEFORE the user's message is saved.
func TestRecipient_OverlongComputedAddressRefusedBeforeSave(t *testing.T) {
	longAgent := strings.Repeat("a", 128)
	h, b, deps := newRecipientHandlerWith(t, longAgent)
	wc := makeTestConn()
	sid := mintOwnerChat(t, h, b, wc)
	to := addressing.Pair{WorkspaceID: strings.Repeat("w", 128), AgentID: longAgent}
	require.NoError(t, to.Validate(), "setup: each component is within its own bound")
	_, idErr := session.MainSessionID(to.WorkspaceID, to.AgentID)
	require.Error(t, idErr, "setup: the computed id must be over the limit")
	deps.eligible[to.WorkspaceID+"/"+to.AgentID] = true

	store := h.agentLoop.ResolveSessionStore(sid)
	before, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	h.handleChatMessageToRecipient(context.Background(), "chat-owner", sid, "@long do it", "ann", nil, "", "ws-1", false, "", nil, &to, wc)

	_, started := nextInbound(t, b, 300*time.Millisecond)
	assert.False(t, started)
	after, err := store.ReadTranscript(sid)
	require.NoError(t, err)
	assert.Len(t, after, len(before), "the source message must not be saved for an unaddressable recipient")
}
