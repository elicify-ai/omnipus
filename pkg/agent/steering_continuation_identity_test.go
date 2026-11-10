package agent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// The provider and channel boundary are external to the continuation pipeline.
// Observe the actual session passed to the channel, but decline streaming so
// the real agent transcript writer must also persist each reply.
type continuationIdentityProvider struct{ scopeFixCountingProvider }

func (p *continuationIdentityProvider) ChatStream(
	ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition,
	model string, options map[string]any, onChunk func(string), _ providers.OnToolCallProgress,
) (*providers.LLMResponse, error) {
	response, err := p.Chat(ctx, messages, tools, model, options)
	if err == nil && onChunk != nil {
		onChunk(response.Content)
	}
	return response, err
}

type continuationIdentityDelegate struct {
	mu         sync.Mutex
	sessionIDs []string
}

func (d *continuationIdentityDelegate) GetStreamer(_ context.Context, _, _, sessionID string) (bus.Streamer, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sessionIDs = append(d.sessionIDs, sessionID)
	return nil, false
}

// A follow-up that arrives after processMessage returns still belongs to the
// execution processTurn has not yet settled. Its stream address and saved
// assistant reply must name that same session, not just its routing key.
func TestContinue_AdmittedFollowUpKeepsSessionIdentityAndTranscript(t *testing.T) {
	cfg := newContinueScopeFixConfig(t)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := &continuationIdentityProvider{scopeFixCountingProvider{scripted: []string{"first answer", "continued answer"}}}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	al.SetSessionMessagingStores(nil, session.NewLifecycleStore(filepath.Join(t.TempDir(), "lifecycle")))
	mintGenuineBootEpochForLoop(t, al)
	store := al.GetSessionStore()
	meta, err := store.NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	other, err := store.NewSession(session.SessionTypeChat, "webchat", testDefaultAgentID)
	require.NoError(t, err)
	delegate := &continuationIdentityDelegate{}
	msgBus.SetStreamDelegate(delegate)

	ctx, entry := ordinaryExecutionContext(context.Background())
	defer func() { require.NoError(t, al.finishExecutionDisposition(entry.disposition)) }()
	message := bus.InboundMessage{
		Channel: "webchat", ChatID: "follow-up-chat", SessionID: meta.ID,
		Content: "first question", UserInitiated: true,
		Metadata: map[string]string{"agent_id": testDefaultAgentID},
	}
	reply, owner, err := al.processMessage(ctx, message)
	require.NoError(t, err)
	require.Equal(t, "first answer", reply)
	require.NotNil(t, entry.disposition, "the initial turn must establish a real admitted execution")

	key := agentSessionKey(owner.ID, message)
	_, _, err = al.enqueueHumanSteeringMessage(key, owner.ID, providers.Message{Role: "user", Content: "follow-up question"}, "")
	require.NoError(t, err)
	reply, err = al.Continue(ctx, key, message.Channel, message.ChatID, "")
	require.NoError(t, err)
	require.Equal(t, "continued answer", reply)
	require.Equal(t, 0, al.pendingSteeringCountForScope(key))
	delegate.mu.Lock()
	addresses := append([]string(nil), delegate.sessionIDs...)
	delegate.mu.Unlock()
	require.Equal(t, []string{meta.ID, meta.ID}, addresses, "both turns must request a streamer for their admitted session")

	entries, err := store.ReadTranscript(meta.ID)
	require.NoError(t, err)
	var answers []string
	for _, transcriptEntry := range entries {
		if transcriptEntry.Role == "assistant" {
			answers = append(answers, transcriptEntry.Content)
		}
	}
	require.Equal(t, []string{"first answer", "continued answer"}, answers, "the continued answer must survive history reload")
	unrelated, err := store.ReadTranscript(other.ID)
	require.NoError(t, err)
	require.Empty(t, unrelated, "a continuation must not write to another chat")
}

// Empty-session and missing-session suffixes still execute successfully without
// a transcript binding; they are not execution refusals. This is intentional
// unchanged behaviour in this release-scoped transplant.
func TestContinue_UnboundKeysDoNotGuessSessionIdentity(t *testing.T) {
	cfg := newContinueScopeFixConfig(t)
	msgBus := bus.NewMessageBus()
	t.Cleanup(msgBus.Close)
	provider := &continuationIdentityProvider{scopeFixCountingProvider{scripted: []string{"continued answer"}}}
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)
	owner, ok := al.GetRegistry().GetAgent(testDefaultAgentID)
	require.True(t, ok)

	for _, tc := range []struct{ name, key, wantError string }{
		// NewArchiveDayStore's documented empty-session refusal happens
		// before a blank routing key can reach a provider or streamer.
		{"blank", "", "archive: empty session id"},
		{"chat scoped", "agent:" + owner.ID + ":chat:webchat:unbound-chat", ""},
		{"empty session", "agent:" + owner.ID + ":session:", ""},
		{"missing session", "agent:" + owner.ID + ":session:session_missing", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delegate := &continuationIdentityDelegate{}
			msgBus.SetStreamDelegate(delegate)
			reply, err := al.continueWithSteeringMessages(context.Background(), owner,
				tc.key, "webchat", "unbound-chat", "",
				[]providers.Message{{Role: "user", Content: "continue"}}, nil)
			if tc.wantError != "" {
				require.EqualError(t, err, tc.wantError)
				require.Empty(t, reply)
				delegate.mu.Lock()
				addresses := append([]string(nil), delegate.sessionIDs...)
				delegate.mu.Unlock()
				require.Empty(t, addresses, "an invalid key must be refused before any streamer lookup")
				return
			}
			require.NoError(t, err)
			require.Equal(t, "continued answer", reply)
			delegate.mu.Lock()
			addresses := append([]string(nil), delegate.sessionIDs...)
			delegate.mu.Unlock()
			require.Equal(t, []string{""}, addresses, "an unbound key must not invent a concrete session")
		})
	}
}
