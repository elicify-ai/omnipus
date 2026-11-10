package agent

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// S1 final-gate M10 (T15/N3): the model archive records WHO a non-assistant input
// came from, derived from the session, not "user" for everything.
func TestInputProducer_ClassifiesConnectorParentAndUserInput(t *testing.T) {
	f := boundConnFixture(t)
	al := f.al
	store := al.GetSessionStore()

	main, err := store.GetOrCreateMainSession(addrWS, addrReceiver)
	require.NoError(t, err)
	chat, err := store.NewSession(session.SessionTypeChat, "webchat", addrReceiver)
	require.NoError(t, err)
	helper, err := store.NewSession(session.SessionTypeChat, "webchat", addrReceiver)
	require.NoError(t, err)
	lc := al.GetSessionLifecycleStore()
	if lc == nil {
		lc = wireLifecycle(t, f)
	}
	require.NoError(t, lc.Persist(&session.LifecycleRecord{
		SessionID: helper.ID, Generation: 1, State: session.LifecycleQueued, AgentID: addrReceiver,
		OwnerScopeKind: session.OwnerScopeParentSession, OwnerScopeID: "parent-1",
		SteeredBy: &session.SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "parent-1"},
	}))

	cases := []struct {
		name string
		ts   *turnState
		want string
	}{
		{"main fed by a connector", &turnState{channel: "telegram", transcriptSessionID: main.ID, sessionKey: main.ID}, "connector"},
		{"steered helper session", &turnState{channel: "webchat", transcriptSessionID: helper.ID, sessionKey: helper.ID}, "parent"},
		{"ordinary chat", &turnState{channel: "webchat", transcriptSessionID: chat.ID, sessionKey: chat.ID}, "user"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := al.inputProducer(c.ts).modelAppend(providers.Message{Role: "user", Content: "x"}, addrReceiver)
			require.Equal(t, c.want, got.Source.Kind)
		})
	}
	// The assistant kind is unchanged.
	require.Equal(t, "agent", windowProducerAssistant.modelAppend(providers.Message{Role: "assistant"}, "a").Source.Kind)
}
